package kinko

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const syncPlanFormatV2 = 2

type syncOperation string

const (
	syncOperationPush      syncOperation = "push"
	syncOperationPull      syncOperation = "pull"
	syncOperationBootstrap syncOperation = "bootstrap"
	syncOperationReset     syncOperation = "reset"
	syncOperationReconcile syncOperation = "reconcile"
	syncOperationPrune     syncOperation = "prune"
)

type syncPrecondition struct {
	ProviderIdentity string `json:"provider_identity"`
	Endpoint         string `json:"endpoint"`
	OrganizationID   string `json:"organization_id,omitempty"`
	ProjectID        string `json:"project_id"`
	MachineID        string `json:"machine_id"`
	SecretID         string `json:"secret_id,omitempty"`
	Name             string `json:"name,omitempty"`
	Revision         string `json:"revision,omitempty"`
	NoteSHA256       string `json:"note_sha256,omitempty"`
	ValueSHA256      string `json:"value_sha256,omitempty"`
}

type syncPlannedAction struct {
	ActionID             string            `json:"action_id"`
	EntryID              string            `json:"entry_id"`
	Kind                 syncActionKind    `json:"kind"`
	Identity             syncIdentity      `json:"identity"`
	Precondition         *syncPrecondition `json:"precondition,omitempty"`
	RequiredCapabilities []syncCapability  `json:"required_capabilities,omitempty"`
	Reason               string            `json:"reason,omitempty"`
	Resolution           syncResolution    `json:"resolution,omitempty"`
	LocalPresent         bool              `json:"local_present"`
	RemotePresent        bool              `json:"remote_present"`
	BaselinePresent      bool              `json:"baseline_present,omitempty"`
	RemoteDeleteAllowed  bool              `json:"remote_delete_allowed,omitempty"`
	IntendedName         string            `json:"intended_name,omitempty"`
	IntendedNoteSHA256   string            `json:"intended_note_sha256,omitempty"`
	IntendedValueSHA256  string            `json:"intended_value_sha256,omitempty"`
}

type syncPlanV2 struct {
	Format           int                  `json:"format"`
	Operation        syncOperation        `json:"operation"`
	ProviderIdentity string               `json:"provider_identity"`
	SelectorDigest   string               `json:"selector_digest"`
	PathMapDigest    string               `json:"path_map_digest,omitempty"`
	PlanDigest       string               `json:"plan_digest"`
	Actions          []syncPlannedAction  `json:"actions"`
	Conflicts        []syncConflict       `json:"conflicts"`
	Maintenance      *syncMaintenancePlan `json:"maintenance,omitempty"`
}

type syncPlanContext struct {
	ProviderIdentity string        `json:"provider_identity"`
	Endpoint         string        `json:"endpoint"`
	OrganizationID   string        `json:"organization_id,omitempty"`
	ProjectID        string        `json:"project_id"`
	MachineID        string        `json:"machine_id"`
	PathMapDigest    string        `json:"path_map_digest,omitempty"`
	PathMaps         []syncPathMap `json:"-"`
}

type syncPlanObservation struct {
	identity syncIdentity
	local    *syncEntry
	remote   *bwsSecret
	state    *syncStateEntryV2
	owner    *syncOwnershipRecord
}

// buildSyncPlanV2 is the compatibility entry point described by the plan. New
// callers should use buildSyncPlanV2WithContext with resolved provider pins.
func buildSyncPlanV2(operation syncOperation, entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, selector syncSelector) (*syncPlanV2, error) {
	ctx, err := inferSyncPlanContext(remote, envelope)
	if err != nil {
		return nil, err
	}
	return buildSyncPlanV2WithContext(operation, entries, remote, envelope, selector, ctx)
}

func buildSyncPlanV2WithContext(operation syncOperation, entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, selector syncSelector, planContext syncPlanContext) (*syncPlanV2, error) {
	if err := validateSyncOperation(operation); err != nil {
		return nil, err
	}
	if err := validateSyncPlanContext(planContext); err != nil {
		return nil, err
	}
	normalizedSelector, selectorDigest, err := normalizeSyncSelector(selector)
	if err != nil {
		return nil, err
	}
	observations, err := collectSyncPlanObservations(entries, remote, envelope, planContext)
	if err != nil {
		return nil, err
	}
	identities := make([]syncIdentity, 0, len(observations))
	for _, observation := range observations {
		identities = append(identities, observation.identity)
	}
	selected, err := selectSyncIdentities(normalizedSelector, identities)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, errors.New("effective sync selection is empty for a mutating workflow")
	}
	plan := &syncPlanV2{
		Format: syncPlanFormatV2, Operation: operation, ProviderIdentity: planContext.ProviderIdentity,
		SelectorDigest: selectorDigest, PathMapDigest: planContext.PathMapDigest,
		Actions: []syncPlannedAction{}, Conflicts: []syncConflict{},
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		observation := observations[id]
		action := classifySyncPlanObservation(operation, id, observation, planContext)
		plan.Actions = append(plan.Actions, action)
		if action.Kind == syncActionConflict {
			plan.Conflicts = append(plan.Conflicts, syncConflict{
				EntryID: id, Reason: action.Reason, LocalPresent: action.LocalPresent,
				RemotePresent: action.RemotePresent, BaselinePresent: action.BaselinePresent,
				RemoteDeleteAllowed: action.RemoteDeleteAllowed,
			})
		}
	}
	if err := finalizeSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func inferSyncPlanContext(remote []bwsSecret, envelope syncStateEnvelope) (syncPlanContext, error) {
	var inferred syncPlanContext
	if envelope.Format == bwsSyncStateFormatV2 {
		state, err := decodeBWSSyncStateV2(envelope)
		if err != nil {
			return syncPlanContext{}, err
		}
		if state.Context != nil {
			inferred = *state.Context
		}
		for _, entry := range state.Entries {
			candidate := syncPlanContext{ProviderIdentity: entry.ProviderIdentity, Endpoint: entry.Endpoint, OrganizationID: entry.OrganizationID, ProjectID: entry.ProjectID, MachineID: entry.MachineID}
			if inferred.ProviderIdentity == "" {
				inferred = candidate
				continue
			}
			merged, ok := coalesceSyncProviderContextOrganization(inferred, candidate)
			if !ok {
				return syncPlanContext{}, errors.New("format-2 state contains more than one provider context")
			}
			inferred = merged
		}
	}
	for _, secret := range remote {
		if inferred.ProjectID == "" {
			inferred.ProjectID, inferred.OrganizationID = secret.ProjectID, secret.OrganizationID
		}
		if inferred.ProjectID != secret.ProjectID || (inferred.OrganizationID != "" && secret.OrganizationID != "" && inferred.OrganizationID != secret.OrganizationID) {
			return syncPlanContext{}, errors.New("remote snapshot spans provider projects or organizations")
		}
	}
	if inferred.ProviderIdentity == "" || inferred.Endpoint == "" || inferred.MachineID == "" {
		return syncPlanContext{}, errors.New("resolved provider identity, endpoint, project, and machine context are required to build a pinned sync plan")
	}
	return inferred, nil
}

func sameSyncProviderContext(left, right syncPlanContext) bool {
	return left.ProviderIdentity == right.ProviderIdentity && left.Endpoint == right.Endpoint && left.OrganizationID == right.OrganizationID && left.ProjectID == right.ProjectID && left.MachineID == right.MachineID && left.PathMapDigest == right.PathMapDigest
}

// coalesceSyncProviderContextOrganization compares two provider contexts on
// the boundary fields that a state built by a single sync must always agree
// on exactly (provider identity, endpoint, project, machine). It tolerates
// one side's OrganizationID being empty: a state entry recorded by a create
// (no precondition, no organization configured) legitimately disagrees on
// that one field with an entry recorded through a precondition, which pins
// the organization actually observed on the remote secret (see
// preconditionForSecret). Two differing non-empty organizations still
// disagree. It returns the coalesced context, keeping whichever
// OrganizationID is non-empty, and whether the two contexts agree.
func coalesceSyncProviderContextOrganization(left, right syncPlanContext) (syncPlanContext, bool) {
	if left.ProviderIdentity != right.ProviderIdentity || left.Endpoint != right.Endpoint || left.ProjectID != right.ProjectID || left.MachineID != right.MachineID {
		return syncPlanContext{}, false
	}
	if left.OrganizationID != "" && right.OrganizationID != "" && left.OrganizationID != right.OrganizationID {
		return syncPlanContext{}, false
	}
	merged := left
	if merged.OrganizationID == "" {
		merged.OrganizationID = right.OrganizationID
	}
	return merged, true
}

func validateSyncOperation(operation syncOperation) error {
	switch operation {
	case syncOperationPush, syncOperationPull, syncOperationBootstrap, syncOperationReset, syncOperationReconcile, syncOperationPrune:
		return nil
	default:
		return fmt.Errorf("unsupported sync operation %q", operation)
	}
}

func validateSyncPlanContext(value syncPlanContext) error {
	if !isLowerHex(value.ProviderIdentity, 64) {
		return errors.New("provider identity must be a full lowercase SHA-256 digest")
	}
	if strings.TrimSpace(value.Endpoint) == "" || strings.TrimSpace(value.ProjectID) == "" {
		return errors.New("provider endpoint and project id are required")
	}
	if !isValidMachineID(value.MachineID) {
		return errors.New("machine id is invalid")
	}
	if value.PathMapDigest != "" && !isLowerHex(value.PathMapDigest, 64) {
		return errors.New("path-map digest must be a full lowercase SHA-256 digest")
	}
	return nil
}

func collectSyncPlanObservations(entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, planContext syncPlanContext) (map[string]syncPlanObservation, error) {
	result := map[string]syncPlanObservation{}
	for index := range entries {
		entry := entries[index]
		identity, err := identityForLocalSyncEntry(entry, planContext)
		if err != nil {
			return nil, fmt.Errorf("local entry %d: %w", index, err)
		}
		id := syncEntryID(identity)
		observation := result[id]
		if observation.local != nil {
			return nil, fmt.Errorf("duplicate local sync identity %s", id)
		}
		observation.identity, observation.local = identity, copySyncEntry(entry)
		result[id] = observation
	}
	remoteObservations, err := validateRemotePlanSecrets(remote, planContext)
	if err != nil {
		return nil, err
	}
	for id, remoteObservation := range remoteObservations {
		observation := result[id]
		if observation.identity.Key != "" && observation.identity != remoteObservation.identity {
			return nil, fmt.Errorf("remote identity %s disagrees with local identity", id)
		}
		observation.identity, observation.remote = remoteObservation.identity, remoteObservation.remote
		result[id] = observation
	}
	stateEntries, err := stateEntriesForPlan(envelope, planContext)
	if err != nil {
		return nil, err
	}
	for id, stateEntry := range stateEntries {
		observation := result[id]
		identity := identityForStateEntry(stateEntry)
		if observation.identity.Key != "" && observation.identity != identity {
			return nil, fmt.Errorf("state identity %s disagrees with current identity", id)
		}
		copyEntry := stateEntry
		observation.identity, observation.state = identity, &copyEntry
		result[id] = observation
	}
	ownership, err := ownershipRecordsForPlan(envelope, planContext)
	if err != nil {
		return nil, err
	}
	for id, record := range ownership {
		observation := result[id]
		if observation.identity.Key != "" && observation.identity != record.Identity {
			return nil, fmt.Errorf("ownership identity %s disagrees with current identity", id)
		}
		copyRecord := record
		observation.identity, observation.owner = record.Identity, &copyRecord
		result[id] = observation
	}
	return result, nil
}

func ownershipRecordsForPlan(envelope syncStateEnvelope, planContext syncPlanContext) (map[string]syncOwnershipRecord, error) {
	if envelope.Format != bwsSyncStateFormatV2 {
		return map[string]syncOwnershipRecord{}, nil
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		return nil, err
	}
	for id, record := range state.Ownership {
		if !isLowerHex(id, 64) || syncEntryID(record.Identity) != id {
			return nil, fmt.Errorf("ownership record %q has an invalid entry identity", id)
		}
		if err := validateSyncIdentity(record.Identity); err != nil {
			return nil, fmt.Errorf("ownership record %s: %w", id, err)
		}
		if record.ProviderIdentity != planContext.ProviderIdentity || record.Identity.Provider != planContext.ProviderIdentity || record.Identity.ProjectID != planContext.ProjectID || record.Identity.MachineID != planContext.MachineID {
			return nil, fmt.Errorf("ownership record %s crosses the pinned provider boundary", id)
		}
		if strings.TrimSpace(record.SecretID) == "" || strings.TrimSpace(record.Revision) == "" {
			return nil, fmt.Errorf("ownership record %s lacks a secret id or confirmed revision", id)
		}
	}
	return state.Ownership, nil
}

func identityForLocalSyncEntry(entry syncEntry, planContext syncPlanContext) (syncIdentity, error) {
	ref, err := normalizeScopeRef(entry.ref)
	if err != nil {
		return syncIdentity{}, err
	}
	identity := syncIdentity{Provider: planContext.ProviderIdentity, ProjectID: planContext.ProjectID, MachineID: planContext.MachineID, Profile: ref.profile, Key: entry.key, Scope: ref.kind}
	if ref.kind == scopeKindPath {
		if len(planContext.PathMaps) > 0 {
			logicalPath, err := mapLocalToLogical(ref.path, planContext.PathMaps)
			if err != nil {
				return syncIdentity{}, err
			}
			identity.Path = "logical:" + logicalPath
		} else {
			identity.Path = "local:" + ref.path
		}
	}
	if err := validateSyncIdentity(identity); err != nil {
		return syncIdentity{}, err
	}
	return identity, nil
}

func identityForStateEntry(entry syncStateEntryV2) syncIdentity {
	identity := syncIdentity{Provider: entry.ProviderIdentity, ProjectID: entry.ProjectID, MachineID: entry.MachineID, Profile: entry.Profile, Key: entry.Key, Scope: entry.Scope}
	if entry.LogicalPath != "" {
		identity.Path = "logical:" + entry.LogicalPath
	} else if entry.LocalPath != "" {
		identity.Path = "local:" + entry.LocalPath
	}
	return identity
}

func stateEntriesForPlan(envelope syncStateEnvelope, planContext syncPlanContext) (map[string]syncStateEntryV2, error) {
	if envelope.Format == 0 {
		return map[string]syncStateEntryV2{}, nil
	}
	if envelope.Format == bwsSyncStateFormatV2 {
		state, err := decodeBWSSyncStateV2(envelope)
		if err != nil {
			return nil, err
		}
		return state.Entries, nil
	}
	if envelope.Format != 1 {
		return nil, fmt.Errorf("unsupported BWS sync state format %d", envelope.Format)
	}
	encoded, err := json.Marshal(envelope.Raw)
	if err != nil {
		return nil, err
	}
	var legacy bwsSyncState
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		return nil, fmt.Errorf("decode legacy BWS sync state: %w", err)
	}
	if err := validateBWSSyncState(&legacy); err != nil {
		return nil, fmt.Errorf("validate legacy BWS sync state: %w", err)
	}
	result := make(map[string]syncStateEntryV2, len(legacy.Entries))
	for _, entry := range legacy.Entries {
		converted := syncStateEntryV2{
			Schema: syncStateEntrySchemaV2, ProviderIdentity: planContext.ProviderIdentity, Endpoint: planContext.Endpoint,
			OrganizationID: planContext.OrganizationID, ProjectID: planContext.ProjectID, MachineID: planContext.MachineID,
			SecretID: entry.SecretID, Name: entry.Name, Revision: entry.RevisionDate, Profile: entry.Profile,
			Key: entry.Key, ValueSHA256: entry.ValueSHA256, Scope: entry.Scope, LocalPath: entry.Path,
		}
		result[syncEntryID(identityForStateEntry(converted))] = converted
	}
	return result, nil
}

func validateRemotePlanSecrets(secrets []bwsSecret, planContext syncPlanContext) (map[string]syncPlanObservation, error) {
	if err := validateUniqueRemoteSecretIDs(secrets); err != nil {
		return nil, err
	}
	result := map[string]syncPlanObservation{}
	seenNames := map[string]string{}
	var malformed []string
	for index := range secrets {
		secret := secrets[index]
		if secret.ProjectID != planContext.ProjectID || (planContext.OrganizationID != "" && secret.OrganizationID != planContext.OrganizationID) {
			return nil, fmt.Errorf("remote secret %q is outside the pinned organization/project", secret.ID)
		}
		if !strings.HasPrefix(secret.Key, planContext.MachineID+"_") {
			continue
		}
		identity, err := identityForRemoteSecret(secret, planContext)
		if err != nil {
			malformed = append(malformed, secret.ID)
			continue
		}
		if previous, exists := seenNames[secret.Key]; exists {
			return nil, fmt.Errorf("duplicate machine-owned BWS secret name %q on ids %q and %q", secret.Key, previous, secret.ID)
		}
		seenNames[secret.Key] = secret.ID
		id := syncEntryID(identity)
		if previous, exists := result[id]; exists {
			return nil, fmt.Errorf("duplicate remote sync identity %s on ids %q and %q", id, previous.remote.ID, secret.ID)
		}
		copySecret := secret
		result[id] = syncPlanObservation{identity: identity, remote: &copySecret}
	}
	if len(malformed) > 0 {
		sort.Strings(malformed)
		return nil, fmt.Errorf("malformed metadata on machine-owned BWS secret ids: %s", strings.Join(malformed, ", "))
	}
	return result, nil
}

func identityForRemoteSecret(secret bwsSecret, planContext syncPlanContext) (syncIdentity, error) {
	var discriminator struct {
		Format int `json:"kinko_sync_format"`
	}
	if err := json.Unmarshal([]byte(secret.Note), &discriminator); err != nil {
		return syncIdentity{}, err
	}
	identity := syncIdentity{Provider: planContext.ProviderIdentity, ProjectID: planContext.ProjectID, MachineID: planContext.MachineID}
	switch discriminator.Format {
	case 1:
		metadata, err := parseBWSNote(secret.Note)
		if err != nil {
			return syncIdentity{}, err
		}
		if err := verifyNoteMatchesName(planContext.MachineID, secret.Key, metadata); err != nil {
			return syncIdentity{}, err
		}
		identity.Profile, identity.Scope, identity.Key = metadata.Profile, metadata.Scope, metadata.Key
		if metadata.Scope == scopeKindPath {
			identity.Path = "local:" + metadata.Path
		}
	case bwsSyncStateFormatV2:
		metadata, err := parseBWSNoteV2(secret.Note)
		if err != nil {
			return syncIdentity{}, err
		}
		if err := verifyNoteMatchesNameV2(planContext.MachineID, secret.Key, metadata); err != nil {
			return syncIdentity{}, err
		}
		identity.Profile, identity.Scope, identity.Key = metadata.Profile, metadata.Scope, metadata.Key
		if metadata.Scope == scopeKindPath {
			identity.Path = "logical:" + metadata.LogicalPath
		}
	default:
		return syncIdentity{}, fmt.Errorf("unsupported BWS note format %d", discriminator.Format)
	}
	return identity, validateSyncIdentity(identity)
}

func classifySyncPlanObservation(operation syncOperation, id string, observation syncPlanObservation, planContext syncPlanContext) syncPlannedAction {
	localPresent, remotePresent, statePresent := observation.local != nil, observation.remote != nil, observation.state != nil
	action := syncPlannedAction{EntryID: id, Identity: observation.identity, LocalPresent: localPresent, RemotePresent: remotePresent, BaselinePresent: statePresent}
	if remotePresent {
		action.Precondition = preconditionForSecret(*observation.remote, planContext)
	}
	localHash, remoteHash := "", ""
	if localPresent {
		localHash = valueSHA256(observation.local.value)
	}
	if remotePresent {
		remoteHash = valueSHA256(observation.remote.Value)
	}
	if localPresent && operation != syncOperationPull && operation != syncOperationBootstrap {
		action.IntendedValueSHA256 = localHash
		if name, note, err := intendedRemoteMetadata(action.Identity); err == nil {
			action.IntendedName = name
			action.IntendedNoteSHA256 = valueSHA256(note)
		}
	}
	stateHash, stateRevision := "", ""
	if statePresent {
		stateHash, stateRevision = observation.state.ValueSHA256, observation.state.Revision
		action.RemoteDeleteAllowed = observation.state.SecretID != "" && (!remotePresent || observation.state.SecretID == observation.remote.ID)
	}
	if observation.owner != nil && remotePresent && observation.owner.SecretID == observation.remote.ID && observation.owner.Revision == observation.remote.RevisionDate {
		action.RemoteDeleteAllowed = true
	}
	if operation == syncOperationPull || operation == syncOperationBootstrap {
		classifyV2Pull(&action, localHash, remoteHash, stateHash, stateRevision)
	} else {
		classifyV2Push(&action, localHash, remoteHash, stateHash, stateRevision)
	}
	action.RequiredCapabilities = capabilitiesForPlannedAction(action, operation)
	return action
}

func classifyV2Push(action *syncPlannedAction, localHash, remoteHash, stateHash, stateRevision string) {
	switch {
	case action.LocalPresent && !action.RemotePresent && !action.BaselinePresent:
		action.Kind = syncActionCreate
	case action.LocalPresent && !action.RemotePresent:
		action.Kind, action.Reason = syncActionConflict, "remote secret was deleted after the last sync"
	case action.LocalPresent && action.RemotePresent && localHash == remoteHash:
		action.Kind = syncActionAdopt
		if action.BaselinePresent && stateHash == remoteHash && stateRevision == action.Precondition.Revision {
			action.Kind = syncActionUnchanged
		}
	case action.LocalPresent && action.RemotePresent && !action.BaselinePresent:
		action.Kind, action.Reason = syncActionConflict, "local and remote values differ without a sync baseline"
	case action.LocalPresent && action.RemotePresent && stateRevision == action.Precondition.Revision:
		action.Kind = syncActionUpdate
	case action.LocalPresent && action.RemotePresent:
		action.Kind, action.Reason = syncActionConflict, "remote secret changed after the last sync"
	case !action.LocalPresent && action.RemotePresent && action.BaselinePresent && stateRevision == action.Precondition.Revision:
		action.Kind = syncActionDelete
	case !action.LocalPresent && action.RemotePresent && action.BaselinePresent:
		action.Kind, action.Reason = syncActionConflict, "remote secret changed after the local deletion"
	case !action.LocalPresent && action.RemotePresent:
		action.Kind = syncActionIgnore
	case !action.LocalPresent && !action.RemotePresent && action.BaselinePresent:
		action.Kind = syncActionUnchanged
	default:
		action.Kind = syncActionIgnore
	}
}

func classifyV2Pull(action *syncPlannedAction, localHash, remoteHash, stateHash, _ string) {
	switch {
	case action.RemotePresent && !action.LocalPresent && !action.BaselinePresent:
		action.Kind = syncActionCreate
	case action.RemotePresent && !action.LocalPresent:
		action.Kind, action.Reason = syncActionConflict, "local secret was deleted after the last sync"
	case action.RemotePresent && action.LocalPresent && remoteHash == localHash:
		action.Kind = syncActionAdopt
		if action.BaselinePresent && stateHash == localHash {
			action.Kind = syncActionUnchanged
		}
	case action.RemotePresent && action.LocalPresent && !action.BaselinePresent:
		action.Kind, action.Reason = syncActionConflict, "local and remote values differ without a sync baseline"
	case action.RemotePresent && action.LocalPresent && localHash == stateHash:
		action.Kind = syncActionUpdate
	case action.RemotePresent && action.LocalPresent:
		action.Kind, action.Reason = syncActionConflict, "local secret changed after the last sync"
	case !action.RemotePresent && action.LocalPresent && action.BaselinePresent && localHash == stateHash:
		action.Kind = syncActionDelete
	case !action.RemotePresent && action.LocalPresent && action.BaselinePresent:
		action.Kind, action.Reason = syncActionConflict, "local secret changed after the remote deletion"
	case !action.RemotePresent && action.LocalPresent:
		action.Kind = syncActionIgnore
	case !action.RemotePresent && !action.LocalPresent && action.BaselinePresent:
		action.Kind = syncActionUnchanged
	default:
		action.Kind = syncActionIgnore
	}
}

func preconditionForSecret(secret bwsSecret, planContext syncPlanContext) *syncPrecondition {
	// When no organization is configured, planContext.OrganizationID is
	// legitimately empty (see validateRemotePlanSecrets and
	// inferSyncPlanContext). Pin to the organization observed on the secret
	// at plan time instead: the pinned boundary is still enforced (apply-time
	// re-read must match this pin exactly), it just locks to what was
	// actually observed rather than to an absent configuration value.
	organizationID := planContext.OrganizationID
	if organizationID == "" {
		organizationID = secret.OrganizationID
	}
	return &syncPrecondition{
		ProviderIdentity: planContext.ProviderIdentity, Endpoint: planContext.Endpoint,
		OrganizationID: organizationID, ProjectID: planContext.ProjectID, MachineID: planContext.MachineID,
		SecretID: secret.ID, Name: secret.Key, Revision: secret.RevisionDate,
		NoteSHA256: valueSHA256(secret.Note), ValueSHA256: valueSHA256(secret.Value),
	}
}

func capabilitiesForPlannedAction(action syncPlannedAction, operation syncOperation) []syncCapability {
	capabilities := []syncCapability{syncCapabilityRead}
	if action.Kind == syncActionConflict || action.Kind == syncActionIgnore || action.Kind == syncActionUnchanged || action.Kind == syncActionAdopt {
		return capabilities
	}
	remoteMutation := operation != syncOperationPull && operation != syncOperationBootstrap
	if action.Resolution == syncResolveLocal {
		remoteMutation = true
	} else if action.Resolution == syncResolveRemote || action.Resolution == syncResolveDeleteLocal {
		remoteMutation = false
	}
	if remoteMutation && action.Kind == syncActionDelete {
		// Delete-capable transports perform the pinned re-read internally; a
		// delete plan must not accidentally require the value-mutation path (or
		// advertise an unrelated standalone read capability).
		capabilities = []syncCapability{syncCapabilityDelete}
	} else if remoteMutation {
		capabilities = append(capabilities, syncCapabilityValueSafeMutation)
	}
	return uniqueSortedCapabilities(capabilities)
}

func uniqueSortedCapabilities(capabilities []syncCapability) []syncCapability {
	seen := map[syncCapability]struct{}{}
	for _, capability := range capabilities {
		seen[capability] = struct{}{}
	}
	result := make([]syncCapability, 0, len(seen))
	for capability := range seen {
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func finalizeSyncPlan(plan *syncPlanV2) error {
	if plan == nil {
		return errors.New("sync plan is nil")
	}
	if plan.Actions == nil {
		plan.Actions = []syncPlannedAction{}
	}
	if plan.Conflicts == nil {
		plan.Conflicts = []syncConflict{}
	}
	for index := range plan.Actions {
		plan.Actions[index].RequiredCapabilities = uniqueSortedCapabilities(plan.Actions[index].RequiredCapabilities)
		copyAction := plan.Actions[index]
		copyAction.ActionID = ""
		encoded, err := json.Marshal(copyAction)
		if err != nil {
			return fmt.Errorf("encode sync action: %w", err)
		}
		plan.Actions[index].ActionID = fullSHA256(encoded)
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].EntryID < plan.Actions[j].EntryID })
	sort.Slice(plan.Conflicts, func(i, j int) bool { return plan.Conflicts[i].EntryID < plan.Conflicts[j].EntryID })
	plan.PlanDigest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode sync plan: %w", err)
	}
	plan.PlanDigest = fullSHA256(encoded)
	return nil
}

func fullSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func cloneSyncPlanV2(plan *syncPlanV2) *syncPlanV2 {
	if plan == nil {
		return nil
	}
	clone := *plan
	clone.Actions = append([]syncPlannedAction(nil), plan.Actions...)
	for index := range clone.Actions {
		clone.Actions[index].RequiredCapabilities = append([]syncCapability(nil), plan.Actions[index].RequiredCapabilities...)
		if plan.Actions[index].Precondition != nil {
			precondition := *plan.Actions[index].Precondition
			clone.Actions[index].Precondition = &precondition
		}
	}
	clone.Conflicts = append([]syncConflict(nil), plan.Conflicts...)
	clone.Maintenance = cloneSyncMaintenancePlan(plan.Maintenance)
	return &clone
}

func validatePinnedSyncPlan(plan *syncPlanV2, provider syncProvider) error {
	if err := validateSyncPlanShape(plan); err != nil {
		return err
	}
	capabilities := []syncCapability{}
	for _, action := range plan.Actions {
		capabilities = append(capabilities, action.RequiredCapabilities...)
	}
	if err := requireSyncCapabilities(provider, uniqueSortedCapabilities(capabilities)...); err != nil {
		return err
	}
	projects, err := provider.ListProjects(context.Background())
	if err != nil {
		return fmt.Errorf("list projects for pinned plan: %w", err)
	}
	projectIDs := map[string]struct{}{}
	for _, project := range projects {
		projectIDs[project.ID] = struct{}{}
	}
	for _, action := range plan.Actions {
		if action.Precondition == nil || action.Precondition.SecretID == "" {
			continue
		}
		if _, exists := projectIDs[action.Precondition.ProjectID]; !exists {
			return fmt.Errorf("pinned project %q is not assigned to the provider", action.Precondition.ProjectID)
		}
		secret, err := provider.GetSecret(context.Background(), action.Precondition.SecretID)
		if err != nil {
			return fmt.Errorf("re-get pinned secret %q: %w", action.Precondition.SecretID, err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return err
		}
	}
	return nil
}

func validateSyncPlanShape(plan *syncPlanV2) error {
	if plan == nil || plan.Format != syncPlanFormatV2 || !isLowerHex(plan.ProviderIdentity, 64) || !isLowerHex(plan.SelectorDigest, 64) || !isLowerHex(plan.PlanDigest, 64) {
		return errors.New("sync plan has invalid format or digest pins")
	}
	copyPlan := *plan
	copyPlan.Actions = make([]syncPlannedAction, len(plan.Actions))
	copy(copyPlan.Actions, plan.Actions)
	copyPlan.Conflicts = make([]syncConflict, len(plan.Conflicts))
	copy(copyPlan.Conflicts, plan.Conflicts)
	if err := finalizeSyncPlan(&copyPlan); err != nil {
		return err
	}
	for index := range plan.Actions {
		if copyPlan.Actions[index].ActionID != plan.Actions[index].ActionID {
			return fmt.Errorf("sync action digest does not match its contents: got %s want %s", plan.Actions[index].ActionID, copyPlan.Actions[index].ActionID)
		}
	}
	if copyPlan.PlanDigest != plan.PlanDigest {
		return fmt.Errorf("sync plan digest does not match its contents: got %s want %s", plan.PlanDigest, copyPlan.PlanDigest)
	}
	seenActions := map[string]struct{}{}
	for _, action := range plan.Actions {
		if !isLowerHex(action.ActionID, 64) || !isLowerHex(action.EntryID, 64) || syncEntryID(action.Identity) != action.EntryID {
			return fmt.Errorf("sync action %q has invalid identity pins", action.ActionID)
		}
		if _, exists := seenActions[action.EntryID]; exists {
			return fmt.Errorf("duplicate planned entry %s", action.EntryID)
		}
		seenActions[action.EntryID] = struct{}{}
		if action.Precondition != nil {
			if action.Precondition.ProviderIdentity != plan.ProviderIdentity || action.Precondition.ProjectID != action.Identity.ProjectID || action.Precondition.MachineID != action.Identity.MachineID {
				return fmt.Errorf("action %s precondition crosses a pinned boundary", action.ActionID)
			}
			if !isLowerHex(action.Precondition.NoteSHA256, 64) || !isLowerHex(action.Precondition.ValueSHA256, 64) {
				return fmt.Errorf("action %s has invalid content hashes", action.ActionID)
			}
		}
		if action.IntendedValueSHA256 != "" && (!isLowerHex(action.IntendedValueSHA256, 64) || !isLowerHex(action.IntendedNoteSHA256, 64) || action.IntendedName == "") {
			return fmt.Errorf("action %s has invalid intended remote content pins", action.ActionID)
		}
	}
	return nil
}

func intendedRemoteMetadata(identity syncIdentity) (string, string, error) {
	if err := validateSyncIdentity(identity); err != nil {
		return "", "", err
	}
	if strings.HasPrefix(identity.Path, "logical:") {
		logicalPath := strings.TrimPrefix(identity.Path, "logical:")
		ref := logicalScopeRef{Profile: identity.Profile, Kind: identity.Scope, LogicalPath: logicalPath}
		note, err := encodeBWSNoteV2(bwsNoteMetadataV2{KinkoSyncFormat: 2, MachineID: identity.MachineID, Profile: identity.Profile, Scope: identity.Scope, LogicalPath: logicalPath, Key: identity.Key})
		return buildBWSSecretNameV2(identity.MachineID, ref, identity.Key), note, err
	}
	path := strings.TrimPrefix(identity.Path, "local:")
	ref := scopeRef{profile: identity.Profile, kind: identity.Scope, path: path}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: identity.MachineID, Profile: identity.Profile, Scope: identity.Scope, Path: path, Key: identity.Key})
	return buildBWSSecretName(identity.MachineID, ref, identity.Key), note, err
}

func validateSecretPrecondition(precondition *syncPrecondition, secret bwsSecret) error {
	if precondition == nil {
		return errors.New("secret precondition is nil")
	}
	if secret.ID != precondition.SecretID || secret.ProjectID != precondition.ProjectID || secret.OrganizationID != precondition.OrganizationID || secret.Key != precondition.Name || secret.RevisionDate != precondition.Revision || valueSHA256(secret.Note) != precondition.NoteSHA256 || valueSHA256(secret.Value) != precondition.ValueSHA256 {
		return fmt.Errorf("pinned precondition changed for secret %q", precondition.SecretID)
	}
	identity, err := identityForRemoteSecret(secret, syncPlanContext{ProviderIdentity: precondition.ProviderIdentity, Endpoint: precondition.Endpoint, OrganizationID: precondition.OrganizationID, ProjectID: precondition.ProjectID, MachineID: precondition.MachineID})
	if err != nil {
		return fmt.Errorf("validate pinned secret metadata %q: %w", precondition.SecretID, err)
	}
	if identity.MachineID != precondition.MachineID {
		return fmt.Errorf("pinned machine changed for secret %q", precondition.SecretID)
	}
	return nil
}
