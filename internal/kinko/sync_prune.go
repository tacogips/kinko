package kinko

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type syncPruneOptions struct {
	Provider          string
	MachineID         string
	AckRetiredMachine string
	SecretIDs         []string
	AckMalformed      bool
	PruneEmptyScopes  bool
	Yes               bool
	JSON              bool
	Selector          syncSelector
}

type syncPruneCandidate struct {
	SecretID     string           `json:"secret_id"`
	Reason       string           `json:"reason"`
	Precondition syncPrecondition `json:"precondition"`
	Malformed    bool             `json:"malformed,omitempty"`
}

func buildSyncPrunePlan(remote []bwsSecret, envelope syncStateEnvelope, data *vaultData, options syncPruneOptions) (*syncPlanV2, error) {
	if options.Provider != "" && options.Provider != supportedSyncProvider {
		return nil, fmt.Errorf("unsupported sync provider %q", options.Provider)
	}
	if envelope.Format != bwsSyncStateFormatV2 {
		return nil, errors.New("sync prune requires format-2 ownership state")
	}
	if data == nil {
		return nil, errors.New("sync prune requires a loaded vault snapshot")
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		return nil, err
	}
	contextValue, err := inferMaintenanceContext(state)
	if err != nil {
		return nil, err
	}
	targetMachine := contextValue.MachineID
	if options.MachineID != "" {
		if !isValidMachineID(options.MachineID) {
			return nil, errors.New("prune machine id is invalid")
		}
		targetMachine = options.MachineID
	}
	if targetMachine != contextValue.MachineID {
		if options.AckRetiredMachine != targetMachine {
			return nil, errors.New("cross-machine prune requires an exactly matching retired-machine acknowledgement")
		}
	} else if options.AckRetiredMachine != "" && options.AckRetiredMachine != targetMachine {
		return nil, errors.New("retired-machine acknowledgement does not match the selected machine")
	}
	contextValue.MachineID = targetMachine
	selector, selectorDigest, err := normalizeSyncSelector(options.Selector)
	if err != nil {
		return nil, err
	}
	exactIDs, err := exactSecretIDSet(options.SecretIDs)
	if err != nil {
		return nil, err
	}
	classified, malformed, duplicates, err := classifyPruneRemote(remote, contextValue)
	if err != nil {
		return nil, err
	}
	for id := range malformed {
		if _, exact := exactIDs[id]; !exact || !options.AckMalformed {
			return nil, fmt.Errorf("malformed machine-prefixed secret %q requires exact id and ack-malformed", id)
		}
	}
	for id := range duplicates {
		if _, exact := exactIDs[id]; !exact || !options.AckMalformed {
			return nil, fmt.Errorf("duplicate machine-prefixed secret %q requires exact id and ack-malformed", id)
		}
	}
	identities := make([]syncIdentity, 0, len(classified))
	for _, item := range classified {
		identities = append(identities, item.identity)
	}
	selected, err := selectSyncIdentities(selector, identities)
	if err != nil {
		return nil, err
	}
	automatic := automaticPruneProofs(state, data)
	warnings := []string{}
	if targetMachine != inferCurrentMachine(state) {
		warnings = append(warnings, "kinko cannot prove that another machine is retired")
	}
	plan := &syncPlanV2{
		Format: syncPlanFormatV2, Operation: syncOperationPrune,
		ProviderIdentity: contextValue.ProviderIdentity, SelectorDigest: selectorDigest,
		Actions: []syncPlannedAction{}, Conflicts: []syncConflict{},
		Maintenance: &syncMaintenancePlan{
			Apply:           options.Yes,
			PruneCandidates: []syncPruneCandidate{},
			Warnings:        warnings,
		},
	}
	consumedExactIDs := map[string]struct{}{}
	ids := make([]string, 0, len(classified))
	for secretID := range classified {
		ids = append(ids, secretID)
	}
	sort.Strings(ids)
	for _, secretID := range ids {
		item := classified[secretID]
		entryID := syncEntryID(item.identity)
		if _, ok := selected[entryID]; !ok {
			continue
		}
		_, exact := exactIDs[secretID]
		reason, proved := automatic[secretID]
		if targetMachine != inferCurrentMachine(state) {
			proved = false
		}
		if !proved && !exact {
			continue
		}
		if exact {
			consumedExactIDs[secretID] = struct{}{}
		}
		ownerRevision := ownershipRevisionForSecret(state, secretID)
		if ownerRevision != "" && ownerRevision != item.secret.RevisionDate && !exact {
			continue
		}
		if !proved {
			reason = "remote deletion explicitly acknowledged by immutable secret id"
		} else if ownerRevision != "" && ownerRevision != item.secret.RevisionDate {
			reason = "ownership revision mismatch explicitly acknowledged by immutable secret id"
		}
		precondition := preconditionForSecret(item.secret, contextValue)
		candidate := syncPruneCandidate{SecretID: secretID, Reason: reason, Precondition: *precondition}
		plan.Maintenance.PruneCandidates = append(plan.Maintenance.PruneCandidates, candidate)
		plan.Actions = append(plan.Actions, syncPlannedAction{
			EntryID: entryID, Identity: item.identity, Kind: syncActionDelete,
			Precondition: precondition, RequiredCapabilities: []syncCapability{syncCapabilityDelete},
			Reason: syncMaintenanceReasonPrune + ": " + reason, RemotePresent: true, RemoteDeleteAllowed: true,
		})
	}
	for secretID := range malformed {
		secret := malformed[secretID]
		candidate := rawPruneCandidate(secret, contextValue, "malformed metadata explicitly acknowledged")
		plan.Maintenance.PruneCandidates = append(plan.Maintenance.PruneCandidates, candidate)
		consumedExactIDs[secretID] = struct{}{}
	}
	for secretID := range duplicates {
		if _, already := malformed[secretID]; already {
			continue
		}
		secret := duplicates[secretID]
		candidate := rawPruneCandidate(secret, contextValue, "duplicate metadata explicitly acknowledged")
		plan.Maintenance.PruneCandidates = append(plan.Maintenance.PruneCandidates, candidate)
		consumedExactIDs[secretID] = struct{}{}
	}
	for secretID := range exactIDs {
		if _, consumed := consumedExactIDs[secretID]; !consumed {
			return nil, fmt.Errorf("prune secret id %q is outside the selected machine, project, or selector boundary", secretID)
		}
	}
	if options.PruneEmptyScopes {
		plan.Maintenance.EmptyScopes = selectedEmptySyncScopes(state, data, selector)
	}
	if len(plan.Maintenance.PruneCandidates) == 0 && len(plan.Maintenance.EmptyScopes) == 0 {
		return nil, errors.New("effective sync selection is empty for a mutating workflow")
	}
	if err := finalizeSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func applySyncPrune(ctx context.Context, provider syncProvider, plan *syncPlanV2, data *vaultData, state *bwsSyncStateV2) error {
	if plan == nil || plan.Maintenance == nil || data == nil || state == nil {
		return errors.New("sync prune apply is not initialized")
	}
	if err := requireMaintenanceApply(plan, syncOperationPrune, plan != nil && plan.Maintenance != nil && plan.Maintenance.Apply); err != nil {
		return err
	}
	if len(plan.Maintenance.PruneCandidates) > 0 {
		if err := requireSyncCapabilities(provider, syncCapabilityDelete); err != nil {
			return err
		}
	}
	// Preflight the complete remote set before the first deletion.
	for _, candidate := range plan.Maintenance.PruneCandidates {
		secret, err := provider.GetSecret(ctx, candidate.SecretID)
		if err != nil {
			return fmt.Errorf("preflight prune secret %q: %w", candidate.SecretID, err)
		}
		if err := validatePrunePrecondition(candidate, secret); err != nil {
			return err
		}
	}
	for _, candidate := range plan.Maintenance.PruneCandidates {
		if err := deleteOnceAndConfirm(ctx, provider, candidate.SecretID); err != nil {
			return fmt.Errorf("delete prune secret %q: %w", candidate.SecretID, err)
		}
		removeConfirmedPruneState(state, candidate.SecretID)
	}
	working := cloneVaultData(data)
	for _, identity := range plan.Maintenance.EmptyScopes {
		if identity.Scope != scopeKindPath {
			continue
		}
		path := strings.TrimPrefix(identity.Path, "local:")
		if strings.HasPrefix(identity.Path, "logical:") {
			continue
		}
		if scopes := working.Profiles[identity.Profile]; scopes != nil {
			if values, exists := scopes[path]; exists && len(values) == 0 {
				delete(scopes, path)
				if len(scopes) == 0 {
					delete(working.Profiles, identity.Profile)
				}
			}
		}
	}
	*data = *working
	return nil
}

type pruneRemoteItem struct {
	secret   bwsSecret
	identity syncIdentity
}

func classifyPruneRemote(remote []bwsSecret, contextValue syncPlanContext) (map[string]pruneRemoteItem, map[string]bwsSecret, map[string]bwsSecret, error) {
	classified := map[string]pruneRemoteItem{}
	malformed := map[string]bwsSecret{}
	duplicates := map[string]bwsSecret{}
	byName := map[string][]string{}
	for _, secret := range remote {
		if secret.ProjectID != contextValue.ProjectID || (contextValue.OrganizationID != "" && secret.OrganizationID != contextValue.OrganizationID) {
			continue
		}
		if !strings.HasPrefix(secret.Key, contextValue.MachineID+"_") {
			continue
		}
		if _, exists := classified[secret.ID]; exists {
			return nil, nil, nil, fmt.Errorf("duplicate remote secret id %q", secret.ID)
		}
		identity, err := identityForRemoteSecret(secret, contextValue)
		if err != nil {
			malformed[secret.ID] = secret
			continue
		}
		classified[secret.ID] = pruneRemoteItem{secret: secret, identity: identity}
		byName[secret.Key] = append(byName[secret.Key], secret.ID)
	}
	for _, ids := range byName {
		if len(ids) < 2 {
			continue
		}
		for _, id := range ids {
			duplicates[id] = classified[id].secret
			delete(classified, id)
		}
	}
	return classified, malformed, duplicates, nil
}

func automaticPruneProofs(state *bwsSyncStateV2, data *vaultData) map[string]string {
	result := map[string]string{}
	for id, owner := range state.Ownership {
		localKnown, localExists := maintenanceLocalPresence(data, state.Entries[id], owner.Identity)
		if localKnown && !localExists {
			result[owner.SecretID] = "confirmed kinko ownership record with no local entry"
		}
	}
	if state.Checkpoint != nil && state.Checkpoint.Phase == syncCheckpointComplete {
		actions := make(map[string]syncPlannedAction, len(state.Checkpoint.Actions))
		for _, action := range state.Checkpoint.Actions {
			actions[action.ActionID] = action
		}
		for _, confirmed := range state.Checkpoint.Confirmed {
			action, exists := actions[confirmed.ActionID]
			if confirmed.SecretID != "" && exists {
				localKnown, localExists := maintenanceLocalPresence(data, state.Entries[action.EntryID], action.Identity)
				if localKnown && !localExists {
					result[confirmed.SecretID] = "completed sync checkpoint with no local entry"
				}
			}
		}
	}
	for _, entry := range state.Entries {
		known, exists := maintenanceLocalPresence(data, entry, identityForStateEntry(entry))
		if known && !exists {
			result[entry.SecretID] = "selected baseline tombstone"
		}
	}
	return result
}

func maintenanceLocalPresence(data *vaultData, entry syncStateEntryV2, identity syncIdentity) (known bool, exists bool) {
	if identity.Scope == scopeKindShared {
		_, exists := data.Shared[identity.Key]
		return true, exists
	}
	if entry.LocalPath != "" {
		_, exists := data.Profiles[entry.Profile][entry.LocalPath][entry.Key]
		return true, exists
	}
	if strings.HasPrefix(identity.Path, "local:") {
		_, exists := localValueForIdentity(data, identity)
		return true, exists
	}
	// A logical identity without its local materialization mapping cannot prove
	// local absence. Exact-id acknowledgement remains available to the operator.
	return false, false
}

func selectedEmptySyncScopes(state *bwsSyncStateV2, data *vaultData, selector syncSelector) []syncIdentity {
	identities := []syncIdentity{}
	owned := map[string]struct{}{}
	for id := range state.Ownership {
		owned[id] = struct{}{}
	}
	for id, entry := range state.Entries {
		if _, ok := owned[id]; !ok || entry.Scope != scopeKindPath || entry.LocalPath == "" {
			continue
		}
		values, exists := data.Profiles[entry.Profile][entry.LocalPath]
		if !exists || len(values) != 0 {
			continue
		}
		identity := identityForStateEntry(entry)
		identity.Path = "local:" + entry.LocalPath
		identities = append(identities, identity)
	}
	selected, err := selectSyncIdentities(selector, identities)
	if err != nil {
		return nil
	}
	result := []syncIdentity{}
	for _, identity := range identities {
		if _, ok := selected[syncEntryID(identity)]; ok {
			result = append(result, identity)
		}
	}
	sort.Slice(result, func(i, j int) bool { return syncEntryID(result[i]) < syncEntryID(result[j]) })
	return result
}

func rawPruneCandidate(secret bwsSecret, contextValue syncPlanContext, reason string) syncPruneCandidate {
	return syncPruneCandidate{SecretID: secret.ID, Reason: reason, Precondition: syncPrecondition{
		ProviderIdentity: contextValue.ProviderIdentity, Endpoint: contextValue.Endpoint, OrganizationID: contextValue.OrganizationID,
		ProjectID: contextValue.ProjectID, MachineID: contextValue.MachineID, SecretID: secret.ID,
		Name: secret.Key, Revision: secret.RevisionDate, NoteSHA256: valueSHA256(secret.Note), ValueSHA256: valueSHA256(secret.Value),
	}, Malformed: true}
}

func validatePrunePrecondition(candidate syncPruneCandidate, secret bwsSecret) error {
	pin := candidate.Precondition
	if secret.ID != pin.SecretID || secret.ProjectID != pin.ProjectID || secret.OrganizationID != pin.OrganizationID ||
		secret.Key != pin.Name || secret.RevisionDate != pin.Revision || valueSHA256(secret.Note) != pin.NoteSHA256 || valueSHA256(secret.Value) != pin.ValueSHA256 {
		return fmt.Errorf("pinned prune precondition changed for secret %q", candidate.SecretID)
	}
	if !candidate.Malformed {
		return validateSecretPrecondition(&pin, secret)
	}
	if !strings.HasPrefix(secret.Key, pin.MachineID+"_") {
		return fmt.Errorf("malformed prune target %q is outside the acknowledged machine namespace", candidate.SecretID)
	}
	return nil
}

func deleteOnceAndConfirm(ctx context.Context, provider syncProvider, secretID string) error {
	err := provider.DeleteSecret(ctx, secretID)
	if err == nil {
		return nil
	}
	if !mutationOutcomeMayBeAmbiguous(err) {
		return err
	}
	_, getErr := provider.GetSecret(ctx, secretID)
	if errors.Is(getErr, errBWSSyncSecretNotFound) {
		return nil
	}
	if getErr != nil {
		return errors.Join(err, fmt.Errorf("reconcile ambiguous delete by id: %w", getErr))
	}
	return errors.Join(err, errors.New("ambiguous delete left the secret present; deletion was not retried"))
}

func removeConfirmedPruneState(state *bwsSyncStateV2, secretID string) {
	for id, owner := range state.Ownership {
		if owner.SecretID == secretID {
			delete(state.Ownership, id)
		}
	}
	for id, entry := range state.Entries {
		if entry.SecretID == secretID {
			delete(state.Entries, id)
		}
	}
}

func exactSecretIDSet(ids []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("prune secret id is empty")
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("duplicate prune secret id %q", id)
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func ownershipRevisionForSecret(state *bwsSyncStateV2, secretID string) string {
	for _, owner := range state.Ownership {
		if owner.SecretID == secretID {
			return owner.Revision
		}
	}
	return ""
}

func inferCurrentMachine(state *bwsSyncStateV2) string {
	for _, entry := range state.Entries {
		return entry.MachineID
	}
	for _, owner := range state.Ownership {
		return owner.Identity.MachineID
	}
	return ""
}
