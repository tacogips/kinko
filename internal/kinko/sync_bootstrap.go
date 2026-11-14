package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	syncBootstrapPlanFormat      = 1
	configKeyBootstrapProvenance = "sync.bootstrap.provenance.v1"
)

type syncBootstrapOptions struct {
	Provider        string
	ProjectID       string
	FromMachineID   string
	TargetMachineID string
	Merge           bool
	Yes             bool
	JSON            bool
	Selector        syncSelector
	PathMaps        []syncPathMap
	ConflictPolicy  syncConflictPolicy
	Resolutions     map[string]syncResolution
}

type syncBootstrapTargetPrecondition struct {
	EntryID     string `json:"entry_id"`
	Present     bool   `json:"present"`
	ValueSHA256 string `json:"value_sha256,omitempty"`
}

type syncBootstrapPlan struct {
	Format           int                               `json:"format"`
	SourceMachineID  string                            `json:"source_machine_id"`
	TargetMachineID  string                            `json:"target_machine_id"`
	ProviderIdentity string                            `json:"provider_identity"`
	SelectorDigest   string                            `json:"selector_digest"`
	PathMapDigest    string                            `json:"path_map_digest"`
	PlanDigest       string                            `json:"plan_digest"`
	ReadSet          []syncPrecondition                `json:"read_set"`
	TargetReadSet    []syncBootstrapTargetPrecondition `json:"target_read_set"`
	Actions          []syncPlannedAction               `json:"actions"`
	Conflicts        []syncConflict                    `json:"conflicts"`
}

type syncBootstrapProvenance struct {
	Format           int    `json:"format"`
	PlanDigest       string `json:"plan_digest"`
	ProviderIdentity string `json:"provider_identity"`
	SourceMachineID  string `json:"source_machine_id"`
	TargetMachineID  string `json:"target_machine_id"`
	SelectorDigest   string `json:"selector_digest"`
	PathMapDigest    string `json:"path_map_digest"`
	Selected         int    `json:"selected"`
}

func buildBootstrapPlan(remote []bwsSecret, data *vaultData, options syncBootstrapOptions, runtime bwsRuntimeConfig) (*syncBootstrapPlan, error) {
	if data == nil {
		return nil, errors.New("bootstrap target vault is nil")
	}
	if options.Provider != supportedSyncProvider {
		return nil, fmt.Errorf("unsupported bootstrap provider %q", options.Provider)
	}
	if !isValidMachineID(options.FromMachineID) || !isValidMachineID(options.TargetMachineID) {
		return nil, errors.New("bootstrap source and target machine ids must be valid")
	}
	if options.FromMachineID == options.TargetMachineID {
		return nil, errors.New("bootstrap source and target machine ids must differ")
	}
	if runtime.ProjectID == "" || runtime.ProviderIdentity == "" || !isLowerHex(runtime.ProviderIdentity, 64) {
		return nil, errors.New("bootstrap provider and project pins are incomplete")
	}
	if options.ProjectID != "" && options.ProjectID != runtime.ProjectID {
		return nil, errors.New("bootstrap project option differs from the resolved project")
	}
	endpoint := endpointString(runtime.Endpoints.APIURL)
	if endpoint == "" {
		return nil, errors.New("bootstrap provider endpoint is not resolved")
	}
	if !options.Merge {
		entries, err := collectSyncEntries(data)
		if err != nil {
			return nil, err
		}
		if len(entries) != 0 {
			return nil, errors.New("bootstrap target contains user secret entries; --merge is required")
		}
	}
	normalizedSelector, selectorDigest, err := normalizeSyncSelector(options.Selector)
	if err != nil {
		return nil, err
	}
	pathMaps := append([]syncPathMap(nil), options.PathMaps...)
	if err := validateSyncPathMaps(pathMaps, false); err != nil {
		return nil, err
	}
	pathMapDigest, err := bootstrapPathMapDigest(pathMaps)
	if err != nil {
		return nil, err
	}
	sourceContext := syncPlanContext{ProviderIdentity: runtime.ProviderIdentity, Endpoint: endpoint, OrganizationID: runtime.OrganizationID, ProjectID: runtime.ProjectID, MachineID: options.FromMachineID}
	observations, err := validateRemotePlanSecrets(remote, sourceContext)
	if err != nil {
		return nil, err
	}
	sourceIdentities := make([]syncIdentity, 0, len(observations))
	for _, observation := range observations {
		sourceIdentities = append(sourceIdentities, observation.identity)
	}
	selected, err := selectSyncIdentities(normalizedSelector, sourceIdentities)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, errors.New("effective bootstrap selection is empty")
	}
	for id, observation := range observations {
		if _, keep := selected[id]; keep {
			continue
		}
		if observation.remote != nil {
			observation.remote.Value = ""
		}
		delete(observations, id)
	}

	plan := &syncBootstrapPlan{Format: syncBootstrapPlanFormat, SourceMachineID: options.FromMachineID, TargetMachineID: options.TargetMachineID, ProviderIdentity: runtime.ProviderIdentity, SelectorDigest: selectorDigest, PathMapDigest: pathMapDigest, ReadSet: []syncPrecondition{}, TargetReadSet: []syncBootstrapTargetPrecondition{}, Actions: []syncPlannedAction{}, Conflicts: []syncConflict{}}
	targetIDs := make(map[string]struct{}, len(selected))
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, sourceID := range ids {
		observation := observations[sourceID]
		targetIdentity, err := bootstrapTargetIdentity(observation.identity, options.TargetMachineID, pathMaps)
		if err != nil {
			return nil, fmt.Errorf("materialize bootstrap identity: %w", err)
		}
		targetID := syncEntryID(targetIdentity)
		if _, exists := targetIDs[targetID]; exists {
			return nil, errors.New("selected bootstrap sources collide after target path mapping")
		}
		targetIDs[targetID] = struct{}{}
		localValue, localPresent := localValueForIdentity(data, targetIdentity)
		remoteHash := valueSHA256(observation.remote.Value)
		action := syncPlannedAction{EntryID: targetID, Identity: targetIdentity, Precondition: preconditionForSecret(*observation.remote, sourceContext), RequiredCapabilities: []syncCapability{syncCapabilityRead}, LocalPresent: localPresent, RemotePresent: true}
		targetPin := syncBootstrapTargetPrecondition{EntryID: targetID, Present: localPresent}
		if localPresent {
			targetPin.ValueSHA256 = valueSHA256(localValue)
		}
		switch {
		case !localPresent:
			action.Kind, action.Reason = syncActionCreate, "missing from bootstrap target"
		case targetPin.ValueSHA256 == remoteHash:
			action.Kind, action.Reason = syncActionUnchanged, "source and target hashes match"
		default:
			action.Kind, action.Reason = syncActionConflict, "source and target hashes differ"
			plan.Conflicts = append(plan.Conflicts, syncConflict{EntryID: targetID, Reason: action.Reason, LocalPresent: true, RemotePresent: true})
		}
		plan.ReadSet = append(plan.ReadSet, *action.Precondition)
		plan.TargetReadSet = append(plan.TargetReadSet, targetPin)
		plan.Actions = append(plan.Actions, action)
	}
	if err := resolveBootstrapConflicts(plan, options.ConflictPolicy, options.Resolutions); err != nil {
		return nil, err
	}
	if err := finalizeBootstrapPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func bootstrapTargetIdentity(source syncIdentity, targetMachineID string, maps []syncPathMap) (syncIdentity, error) {
	target := source
	target.MachineID = targetMachineID
	if strings.HasPrefix(source.Path, "logical:") {
		localPath, err := mapLogicalToLocal(strings.TrimPrefix(source.Path, "logical:"), maps)
		if err != nil {
			return syncIdentity{}, err
		}
		target.Path = "local:" + localPath
	}
	if err := validateSyncIdentity(target); err != nil {
		return syncIdentity{}, err
	}
	return target, nil
}

func bootstrapPathMapDigest(maps []syncPathMap) (string, error) {
	stable := append([]syncPathMap(nil), maps...)
	sort.Slice(stable, func(i, j int) bool { return stable[i].Anchor < stable[j].Anchor })
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", fmt.Errorf("encode bootstrap path maps: %w", err)
	}
	return fullSHA256(encoded), nil
}

func resolveBootstrapConflicts(plan *syncBootstrapPlan, policy syncConflictPolicy, resolutions map[string]syncResolution) error {
	if policy == "" {
		policy = syncConflictFail
	}
	if err := validateSyncConflictPolicy(policy); err != nil {
		return err
	}
	conflicts := make(map[string]struct{}, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		conflicts[conflict.EntryID] = struct{}{}
	}
	for id, resolution := range resolutions {
		if _, ok := conflicts[id]; !ok {
			return fmt.Errorf("bootstrap resolution %s matches no current conflict", id)
		}
		if resolution != syncResolveLocal && resolution != syncResolveRemote && resolution != syncResolveSkip {
			return fmt.Errorf("bootstrap conflict %s has unsupported resolution %q", id, resolution)
		}
	}
	remaining := make([]syncConflict, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		action := findPlannedAction(plan.Actions, conflict.EntryID)
		resolution, explicit := resolutions[conflict.EntryID]
		if !explicit {
			resolution = resolutionForPolicy(policy)
		}
		switch resolution {
		case syncResolveRemote:
			action.Kind, action.Resolution = syncActionUpdate, resolution
		case syncResolveLocal, syncResolveSkip:
			action.Kind, action.Resolution = syncActionIgnore, resolution
		case "":
			remaining = append(remaining, conflict)
		default:
			return fmt.Errorf("bootstrap conflict %s requires local, remote, or skip resolution", conflict.EntryID)
		}
	}
	plan.Conflicts = remaining
	return nil
}

func finalizeBootstrapPlan(plan *syncBootstrapPlan) error {
	if plan == nil || plan.Format != syncBootstrapPlanFormat {
		return errors.New("bootstrap plan is invalid")
	}
	for index := range plan.Actions {
		copyAction := plan.Actions[index]
		copyAction.ActionID = ""
		encoded, err := json.Marshal(copyAction)
		if err != nil {
			return err
		}
		plan.Actions[index].ActionID = fullSHA256(encoded)
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].EntryID < plan.Actions[j].EntryID })
	sort.Slice(plan.Conflicts, func(i, j int) bool { return plan.Conflicts[i].EntryID < plan.Conflicts[j].EntryID })
	sort.Slice(plan.ReadSet, func(i, j int) bool { return plan.ReadSet[i].SecretID < plan.ReadSet[j].SecretID })
	sort.Slice(plan.TargetReadSet, func(i, j int) bool { return plan.TargetReadSet[i].EntryID < plan.TargetReadSet[j].EntryID })
	plan.PlanDigest = ""
	digest, err := bootstrapPlanDigest(plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = digest
	return nil
}

func bootstrapPlanDigest(plan *syncBootstrapPlan) (string, error) {
	copyPlan := *plan
	copyPlan.PlanDigest = ""
	encoded, err := json.Marshal(&copyPlan)
	if err != nil {
		return "", err
	}
	return fullSHA256(encoded), nil
}

func validateBootstrapPlan(plan *syncBootstrapPlan) error {
	if plan == nil || plan.Format != syncBootstrapPlanFormat || !isValidMachineID(plan.SourceMachineID) || !isValidMachineID(plan.TargetMachineID) || plan.SourceMachineID == plan.TargetMachineID || !isLowerHex(plan.ProviderIdentity, 64) || !isLowerHex(plan.SelectorDigest, 64) || !isLowerHex(plan.PathMapDigest, 64) || !isLowerHex(plan.PlanDigest, 64) {
		return errors.New("bootstrap plan has invalid identity or digest pins")
	}
	digest, err := bootstrapPlanDigest(plan)
	if err != nil {
		return err
	}
	if digest != plan.PlanDigest {
		return errors.New("bootstrap plan digest does not match its contents")
	}
	if !sort.SliceIsSorted(plan.Actions, func(i, j int) bool { return plan.Actions[i].EntryID < plan.Actions[j].EntryID }) || !sort.SliceIsSorted(plan.ReadSet, func(i, j int) bool { return plan.ReadSet[i].SecretID < plan.ReadSet[j].SecretID }) || !sort.SliceIsSorted(plan.TargetReadSet, func(i, j int) bool { return plan.TargetReadSet[i].EntryID < plan.TargetReadSet[j].EntryID }) {
		return errors.New("bootstrap plan collections are not in canonical order")
	}
	if len(plan.ReadSet) != len(plan.Actions) || len(plan.TargetReadSet) != len(plan.Actions) {
		return errors.New("bootstrap plan read sets do not cover every action")
	}
	readSet := make(map[string]syncPrecondition, len(plan.ReadSet))
	for _, precondition := range plan.ReadSet {
		if precondition.SecretID == "" {
			return errors.New("bootstrap source read set contains an empty id")
		}
		if _, exists := readSet[precondition.SecretID]; exists {
			return errors.New("bootstrap source read set contains a duplicate id")
		}
		readSet[precondition.SecretID] = precondition
	}
	targetSet := make(map[string]struct{}, len(plan.TargetReadSet))
	for _, precondition := range plan.TargetReadSet {
		if _, exists := targetSet[precondition.EntryID]; exists {
			return errors.New("bootstrap target read set contains a duplicate identity")
		}
		targetSet[precondition.EntryID] = struct{}{}
	}
	for _, action := range plan.Actions {
		copyAction := action
		copyAction.ActionID = ""
		encoded, err := json.Marshal(copyAction)
		if err != nil || fullSHA256(encoded) != action.ActionID {
			return errors.New("bootstrap action digest does not match its contents")
		}
		if action.Precondition == nil {
			return errors.New("bootstrap action lacks a source precondition")
		}
		pinned, exists := readSet[action.Precondition.SecretID]
		if !exists || pinned != *action.Precondition {
			return errors.New("bootstrap action differs from its source read-set pin")
		}
		if _, exists := targetSet[action.EntryID]; !exists {
			return errors.New("bootstrap action lacks a target read-set pin")
		}
		if action.Identity.MachineID != plan.TargetMachineID || action.Precondition.MachineID != plan.SourceMachineID || action.Precondition.ProviderIdentity != plan.ProviderIdentity {
			return errors.New("bootstrap action crosses a pinned identity boundary")
		}
		if action.Identity.Provider != plan.ProviderIdentity || action.Identity.ProjectID != action.Precondition.ProjectID || syncEntryID(action.Identity) != action.EntryID || !isLowerHex(action.ActionID, 64) {
			return errors.New("bootstrap action has invalid target identity pins")
		}
		if err := validateSyncIdentity(action.Identity); err != nil {
			return fmt.Errorf("bootstrap target identity is invalid: %w", err)
		}
		if action.Kind == syncActionDelete {
			return errors.New("bootstrap plan must never contain a deletion")
		}
	}
	return nil
}

func applyBootstrapPlan(ctx context.Context, provider syncProvider, plan *syncBootstrapPlan, data *vaultData) error {
	if data == nil {
		return errors.New("bootstrap target vault is nil")
	}
	if err := validateBootstrapPlan(plan); err != nil {
		return err
	}
	if len(plan.Conflicts) != 0 {
		return errors.New("bootstrap plan contains unresolved conflicts")
	}
	if err := requireSyncCapabilities(provider, syncCapabilityRead); err != nil {
		return err
	}
	provider = retryingSyncProvider{syncProvider: provider, policy: defaultSyncRetryPolicy(), clock: realSyncClock{}, classifier: defaultSyncRetryClassifier{}, budget: &syncRetryBudget{}}
	for _, targetPin := range plan.TargetReadSet {
		action := findPlannedAction(plan.Actions, targetPin.EntryID)
		if action == nil {
			return errors.New("bootstrap target pin matches no action")
		}
		value, present := localValueForIdentity(data, action.Identity)
		if present != targetPin.Present || present && valueSHA256(value) != targetPin.ValueSHA256 {
			return errors.New("bootstrap target changed after preview")
		}
	}
	remoteByAction := make(map[string]bwsSecret, len(plan.Actions))
	for _, action := range plan.Actions {
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return fmt.Errorf("re-get bootstrap source secret: %w", err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return err
		}
		remoteByAction[action.ActionID] = secret
	}
	working := cloneVaultData(data)
	for _, action := range plan.Actions {
		switch action.Kind {
		case syncActionCreate, syncActionUpdate:
			secret := remoteByAction[action.ActionID]
			setLocalSyncValue(working, identityScopeRef(action.Identity), action.Identity.Key, secret.Value)
			secret.Value = ""
			remoteByAction[action.ActionID] = secret
		case syncActionUnchanged, syncActionIgnore:
		default:
			return fmt.Errorf("unsupported bootstrap action kind %d", action.Kind)
		}
	}
	*data = *working
	return nil
}

func persistBootstrapProvenance(dataDir string, dek []byte, config map[string]string, plan *syncBootstrapPlan) error {
	if err := validateBootstrapPlan(plan); err != nil {
		return err
	}
	provenance := syncBootstrapProvenance{Format: 1, PlanDigest: plan.PlanDigest, ProviderIdentity: plan.ProviderIdentity, SourceMachineID: plan.SourceMachineID, TargetMachineID: plan.TargetMachineID, SelectorDigest: plan.SelectorDigest, PathMapDigest: plan.PathMapDigest, Selected: len(plan.Actions)}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return err
	}
	next := make(map[string]string, len(config)+1)
	for key, value := range config {
		next[key] = value
	}
	next[configKeyBootstrapProvenance] = string(encoded)
	if err := saveConfig(dataDir, dek, next); err != nil {
		return fmt.Errorf("persist bootstrap provenance: %w", err)
	}
	config[configKeyBootstrapProvenance] = string(encoded)
	return nil
}

func runSyncBootstrap(opts globalOptions, bootstrapOptions syncBootstrapOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if bootstrapOptions.Provider != supportedSyncProvider {
		return newCLIError(exitCodePolicyFailed, "Bootstrap requires --provider=bws.", errors.New("unsupported bootstrap provider"))
	}
	meta, err := loadSyncMetadata(opts.dataDir)
	if err != nil {
		return err
	}
	bootstrapOptions.TargetMachineID = meta.MachineID
	dek, _, err := verifyVaultPasswordDEKForShow(opts, stdin, stderr, "Re-enter password: ")
	if err != nil {
		return syncPasswordError(err)
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Vault mutation is already in progress.", err)
	}
	defer release()
	meta, err = loadSyncMetadata(opts.dataDir)
	if err != nil {
		return err
	}
	if meta.MachineID != bootstrapOptions.TargetMachineID || !isValidMachineID(meta.MachineID) {
		return newCLIError(exitCodePolicyFailed, "Vault machine identity changed before bootstrap planning.", errors.New("bootstrap target identity changed"))
	}
	data, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Vault data could not be loaded for bootstrap.", err)
	}
	config, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Encrypted config could not be loaded for bootstrap.", err)
	}
	token, err := resolveBWSAccessToken(os.Getenv, data.Shared, stderr)
	if err != nil {
		return err
	}
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: bootstrapOptions.ProjectID}, config, os.Getenv)
	if err != nil {
		return err
	}
	client, err := newBWSClient(token, stderr)
	if err != nil {
		return providerCLIError("BWS provider is unavailable.", err)
	}
	client.environmentBuilder = isolatedBWSEnvironmentBuilder(runtime)
	projectID, err := resolveBWSProjectID(context.Background(), client, config, bootstrapOptions.ProjectID, stderr)
	if err != nil {
		return err
	}
	runtime, err = resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: projectID}, config, os.Getenv)
	if err != nil {
		return err
	}
	bootstrapOptions.ProjectID = projectID
	bootstrapOptions.PathMaps, err = resolveSyncPathMaps(config, bootstrapOptions.PathMaps)
	if err != nil {
		return err
	}
	client.environmentBuilder = isolatedBWSEnvironmentBuilder(runtime)
	provider := &bwsCLIAdapter{client: client, stderr: stderr}
	readProvider := retryingSyncProvider{syncProvider: provider, policy: defaultSyncRetryPolicy(), clock: realSyncClock{}, classifier: defaultSyncRetryClassifier{}, budget: &syncRetryBudget{}}
	remote, err := readProvider.ListSecrets(context.Background(), projectID)
	if err != nil {
		return providerCLIError("Could not list BWS secrets for bootstrap.", err)
	}
	plan, err := buildBootstrapPlan(remote, data, bootstrapOptions, runtime)
	if err != nil {
		return err
	}
	result := bootstrapResultForPlan(plan, false)
	if !bootstrapOptions.Yes || len(plan.Conflicts) != 0 {
		if err := printSyncBootstrapResult(stdout, result, bootstrapOptions.JSON); err != nil {
			return newCLIError(exitCodeIOFailed, "Could not write bootstrap preview.", err)
		}
		if len(plan.Conflicts) != 0 {
			return newCLIError(exitCodePolicyFailed, "Bootstrap has unresolved conflicts.", errors.New("bootstrap conflict"))
		}
		return nil
	}
	if err := applyBootstrapPlan(context.Background(), readProvider, plan, data); err != nil {
		return err
	}
	if err := saveVault(opts.dataDir, dek, data); err != nil {
		return newCLIError(exitCodeIOFailed, "Could not atomically save the bootstrapped vault.", err)
	}
	if err := persistBootstrapProvenance(opts.dataDir, dek, config, plan); err != nil {
		return newCLIError(exitCodeIOFailed, "Vault was bootstrapped but provenance could not be saved.", err)
	}
	return printSyncBootstrapResult(stdout, bootstrapResultForPlan(plan, true), bootstrapOptions.JSON)
}
