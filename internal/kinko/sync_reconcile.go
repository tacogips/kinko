package kinko

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type syncReconcileOptions struct {
	Provider        string
	UpgradeMetadata bool
	Yes             bool
	JSON            bool
	Selector        syncSelector
}

type syncMetadataUpgradeCheckpoint struct {
	Old   syncPrecondition `json:"old"`
	New   syncPrecondition `json:"new"`
	Phase string           `json:"phase"`
}

func validateSyncMetadataUpgradeCheckpoint(checkpoint *syncMetadataUpgradeCheckpoint) error {
	if checkpoint == nil {
		return nil
	}
	if checkpoint.Phase != "created" && checkpoint.Phase != "state-replaced" {
		return fmt.Errorf("unsupported metadata-upgrade phase %q", checkpoint.Phase)
	}
	for label, precondition := range map[string]syncPrecondition{"old": checkpoint.Old, "new": checkpoint.New} {
		if precondition.SecretID == "" || precondition.Name == "" || precondition.Revision == "" ||
			!isLowerHex(precondition.ProviderIdentity, 64) || !isLowerHex(precondition.NoteSHA256, 64) || !isLowerHex(precondition.ValueSHA256, 64) {
			return fmt.Errorf("metadata-upgrade %s precondition is incomplete", label)
		}
	}
	if checkpoint.Old.SecretID == checkpoint.New.SecretID || checkpoint.Old.Name == checkpoint.New.Name ||
		checkpoint.Old.ProviderIdentity != checkpoint.New.ProviderIdentity || checkpoint.Old.Endpoint != checkpoint.New.Endpoint ||
		checkpoint.Old.OrganizationID != checkpoint.New.OrganizationID || checkpoint.Old.ProjectID != checkpoint.New.ProjectID ||
		checkpoint.Old.MachineID != checkpoint.New.MachineID || checkpoint.Old.ValueSHA256 != checkpoint.New.ValueSHA256 {
		return errors.New("metadata-upgrade checkpoint does not describe one exact old/new pair")
	}
	return nil
}

func buildSyncReconcilePlan(entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, options syncReconcileOptions) (*syncPlanV2, error) {
	if options.Provider != "" && options.Provider != supportedSyncProvider {
		return nil, fmt.Errorf("unsupported sync provider %q", options.Provider)
	}
	if options.UpgradeMetadata {
		return buildSyncMetadataUpgradePlan(entries, remote, envelope, options)
	}
	plan, err := buildSyncPlanV2(syncOperationReconcile, entries, remote, envelope, options.Selector)
	if err != nil {
		return nil, err
	}
	plan.Maintenance = &syncMaintenancePlan{Apply: options.Yes}
	for index := range plan.Actions {
		action := &plan.Actions[index]
		if action.Kind == syncActionAdopt || action.Kind == syncActionUnchanged {
			action.Kind = syncActionAdopt
			action.Reason = syncMaintenanceReasonAdopt
			action.RequiredCapabilities = []syncCapability{syncCapabilityRead}
			continue
		}
		action.Kind = syncActionConflict
		if action.Reason == "" {
			action.Reason = "local and remote do not match exactly"
		}
	}
	plan.Conflicts = conflictsFromMaintenanceActions(plan.Actions)
	if err := finalizeSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func buildSyncMetadataUpgradePlan(entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, options syncReconcileOptions) (*syncPlanV2, error) {
	if envelope.Format != bwsSyncStateFormatV2 {
		return nil, errors.New("metadata upgrade requires format-2 encrypted state with logical path mappings")
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		return nil, err
	}
	contextValue, err := inferMaintenanceContext(state)
	if err != nil {
		return nil, err
	}
	selector, selectorDigest, err := normalizeSyncSelector(options.Selector)
	if err != nil {
		return nil, err
	}
	remoteByID := make(map[string]bwsSecret, len(remote))
	remoteByName := make(map[string][]bwsSecret, len(remote))
	for _, secret := range remote {
		if secret.ProjectID != contextValue.ProjectID || (contextValue.OrganizationID != "" && secret.OrganizationID != contextValue.OrganizationID) {
			continue
		}
		if _, exists := remoteByID[secret.ID]; exists {
			return nil, fmt.Errorf("duplicate remote secret id %q", secret.ID)
		}
		remoteByID[secret.ID] = secret
		remoteByName[secret.Key] = append(remoteByName[secret.Key], secret)
	}
	locals := indexMaintenanceLocals(entries)
	identities := []syncIdentity{}
	stateByIdentity := map[string]syncStateEntryV2{}
	for _, stateEntry := range state.Entries {
		if stateEntry.LocalPath == "" || stateEntry.LogicalPath == "" {
			continue
		}
		identity := identityForStateEntry(stateEntry)
		id := syncEntryID(identity)
		identities = append(identities, identity)
		stateByIdentity[id] = stateEntry
	}
	selected, err := selectSyncIdentities(selector, identities)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, errors.New("effective sync selection is empty for a mutating workflow")
	}
	plan := &syncPlanV2{
		Format: syncPlanFormatV2, Operation: syncOperationReconcile,
		ProviderIdentity: contextValue.ProviderIdentity, SelectorDigest: selectorDigest,
		Actions: []syncPlannedAction{}, Conflicts: []syncConflict{},
		Maintenance: &syncMaintenancePlan{Apply: options.Yes, UpgradeMetadata: true},
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		stateEntry := stateByIdentity[id]
		oldSecretID := stateEntry.SecretID
		if checkpoint := state.MetadataUpgrade; checkpoint != nil && checkpoint.New.SecretID == stateEntry.SecretID {
			oldSecretID = checkpoint.Old.SecretID
		}
		oldSecret, exists := remoteByID[oldSecretID]
		if !exists {
			return nil, fmt.Errorf("metadata-upgrade old secret %q is absent", oldSecretID)
		}
		oldIdentity := syncIdentity{Provider: contextValue.ProviderIdentity, ProjectID: contextValue.ProjectID, MachineID: contextValue.MachineID, Profile: stateEntry.Profile, Scope: stateEntry.Scope, Key: stateEntry.Key}
		if stateEntry.Scope == scopeKindPath {
			oldIdentity.Path = "local:" + stateEntry.LocalPath
		}
		parsedOld, err := identityForRemoteSecret(oldSecret, contextValue)
		if err != nil || parsedOld != oldIdentity {
			return nil, fmt.Errorf("metadata-upgrade old secret %q does not exactly match format-1 state", stateEntry.SecretID)
		}
		local, ok := locals[maintenanceLocalKey(stateEntry.Profile, stateEntry.Scope, stateEntry.LocalPath, stateEntry.Key)]
		if !ok || valueSHA256(local.value) != valueSHA256(oldSecret.Value) || stateEntry.ValueSHA256 != valueSHA256(oldSecret.Value) {
			return nil, fmt.Errorf("metadata-upgrade value mismatch for secret %q", stateEntry.SecretID)
		}
		identity := identityForStateEntry(stateEntry)
		name, note, err := intendedRemoteMetadata(identity)
		if err != nil {
			return nil, err
		}
		matches := remoteByName[name]
		if len(matches) > 0 && !metadataUpgradeResumeMatch(state.MetadataUpgrade, oldSecret, matches, contextValue, name, note) {
			return nil, fmt.Errorf("metadata-upgrade collision on remote name %q", name)
		}
		action := syncPlannedAction{
			EntryID: id, Identity: identity, Kind: syncActionUpdate, LocalPresent: true, RemotePresent: true, BaselinePresent: true,
			Precondition:         preconditionForSecret(oldSecret, contextValue),
			RequiredCapabilities: []syncCapability{syncCapabilityRead, syncCapabilityDelete, syncCapabilityValueSafeMutation},
			Reason:               syncMaintenanceReasonUpgrade, IntendedName: name,
			IntendedNoteSHA256: valueSHA256(note), IntendedValueSHA256: valueSHA256(oldSecret.Value),
		}
		plan.Actions = append(plan.Actions, action)
	}
	if err := finalizeSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func applySyncReconcile(ctx context.Context, provider syncProvider, plan *syncPlanV2, state *bwsSyncStateV2) error {
	if plan == nil || plan.Maintenance == nil {
		return errors.New("sync reconcile plan is not initialized")
	}
	if err := requireMaintenanceApply(plan, syncOperationReconcile, plan != nil && plan.Maintenance != nil && plan.Maintenance.Apply); err != nil {
		return err
	}
	if state == nil {
		return errors.New("BWS sync state is nil")
	}
	if len(plan.Conflicts) > 0 {
		return errors.New("sync reconcile has unresolved divergence")
	}
	if plan.Maintenance.UpgradeMetadata {
		return applySyncMetadataUpgrade(ctx, provider, plan, state)
	}
	remote := make(map[string]bwsSecret, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.Kind != syncActionAdopt || action.Precondition == nil {
			return errors.New("reconcile state adoption contains a non-exact action")
		}
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return fmt.Errorf("revalidate reconcile secret: %w", err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return err
		}
		remote[action.ActionID] = secret
	}
	working := cloneBWSSyncStateV2(state)
	for _, action := range plan.Actions {
		secret := remote[action.ActionID]
		previousEntry, hadEntry := working.Entries[action.EntryID]
		previousOwner, owned := working.Ownership[action.EntryID]
		updateV2StateForConfirmedAction(working, plan, action, checkpointResult(action, secret), nil, secret)
		if hadEntry {
			updated := working.Entries[action.EntryID]
			updated.LocalPath = previousEntry.LocalPath
			updated.LogicalPath = previousEntry.LogicalPath
			updated.Raw = cloneRawMap(previousEntry.Raw)
			working.Entries[action.EntryID] = updated
		}
		// Reconciliation proves equality, not that kinko created the remote
		// record. Preserve prior ownership proof but never manufacture it.
		if owned {
			working.Ownership[action.EntryID] = previousOwner
		} else {
			delete(working.Ownership, action.EntryID)
		}
	}
	*state = *working
	return nil
}

func applySyncMetadataUpgrade(ctx context.Context, provider syncProvider, plan *syncPlanV2, state *bwsSyncStateV2) error {
	return applySyncMetadataUpgradeDurable(ctx, provider, plan, state, nil)
}

func applySyncMetadataUpgradeDurable(ctx context.Context, provider syncProvider, plan *syncPlanV2, state *bwsSyncStateV2, persist func(*bwsSyncStateV2) error) error {
	if err := requireSyncCapabilities(provider, syncCapabilityRead, syncCapabilityDelete, syncCapabilityValueSafeMutation); err != nil {
		return err
	}
	// Revalidate every old record and every target-name absence before creating
	// the first replacement, so a collision stops the whole migration.
	for _, action := range plan.Actions {
		if action.Precondition == nil || action.Reason != syncMaintenanceReasonUpgrade {
			return errors.New("invalid metadata-upgrade action")
		}
		old, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return err
		}
		if err := validateSecretPrecondition(action.Precondition, old); err != nil {
			return err
		}
		secrets, err := provider.ListSecrets(ctx, action.Identity.ProjectID)
		if err != nil {
			return err
		}
		for _, secret := range secrets {
			if secret.Key == action.IntendedName && !upgradeCheckpointNewMatches(state.MetadataUpgrade, action, secret) {
				return fmt.Errorf("metadata-upgrade collision on remote name %q", action.IntendedName)
			}
		}
	}
	for _, action := range plan.Actions {
		if err := applyOneMetadataUpgradeDurable(ctx, provider, action, state, plan, persist); err != nil {
			return err
		}
	}
	return nil
}

func applyOneMetadataUpgrade(ctx context.Context, provider syncProvider, action syncPlannedAction, state *bwsSyncStateV2, plan *syncPlanV2) error {
	return applyOneMetadataUpgradeDurable(ctx, provider, action, state, plan, nil)
}

func applyOneMetadataUpgradeDurable(ctx context.Context, provider syncProvider, action syncPlannedAction, state *bwsSyncStateV2, plan *syncPlanV2, persist func(*bwsSyncStateV2) error) error {
	checkpoint := state.MetadataUpgrade
	if checkpoint != nil && !upgradeCheckpointMatchesAction(checkpoint, action) {
		return errors.New("metadata-upgrade checkpoint belongs to a different old/new pair")
	}
	old, err := provider.GetSecret(ctx, action.Precondition.SecretID)
	if err != nil {
		return err
	}
	if err := validateSecretPrecondition(action.Precondition, old); err != nil {
		return err
	}
	var replacement bwsSecret
	if checkpoint == nil {
		name, note, err := intendedRemoteMetadata(action.Identity)
		if err != nil || name != action.IntendedName || valueSHA256(note) != action.IntendedNoteSHA256 {
			return errors.New("metadata-upgrade intended metadata changed")
		}
		replacement, err = provider.CreateSecret(ctx, bwsMutationRequest{ProjectID: action.Identity.ProjectID, Name: name, Note: note, Value: old.Value})
		if err != nil {
			return fmt.Errorf("create metadata replacement: %w", err)
		}
		replacement, err = provider.GetSecret(ctx, replacement.ID)
		if err != nil {
			return fmt.Errorf("read back metadata replacement: %w", err)
		}
		if err := validateMetadataReplacement(action, replacement); err != nil {
			return err
		}
		newPrecondition := preconditionForSecret(replacement, *action.PreconditionContext())
		state.MetadataUpgrade = &syncMetadataUpgradeCheckpoint{Old: *action.Precondition, New: *newPrecondition, Phase: "created"}
		if persist != nil {
			if err := persist(state); err != nil {
				return fmt.Errorf("persist created metadata replacement checkpoint: %w", err)
			}
		}
		checkpoint = state.MetadataUpgrade
	} else {
		replacement, err = provider.GetSecret(ctx, checkpoint.New.SecretID)
		if err != nil {
			return fmt.Errorf("resume exact metadata replacement: %w", err)
		}
		if err := validateSecretPrecondition(&checkpoint.New, replacement); err != nil {
			return err
		}
	}
	if checkpoint.Phase == "created" {
		working := cloneBWSSyncStateV2(state)
		if err := replaceMetadataUpgradeState(working, action, replacement, plan); err != nil {
			return err
		}
		working.MetadataUpgrade.Phase = "state-replaced"
		*state = *working
		if persist != nil {
			if err := persist(state); err != nil {
				return fmt.Errorf("persist replaced metadata baseline: %w", err)
			}
		}
		checkpoint = state.MetadataUpgrade
	}
	old, err = provider.GetSecret(ctx, checkpoint.Old.SecretID)
	if err != nil {
		return fmt.Errorf("immediate old metadata recheck: %w", err)
	}
	if err := validateSecretPrecondition(&checkpoint.Old, old); err != nil {
		return err
	}
	if err := deleteOnceAndConfirm(ctx, provider, checkpoint.Old.SecretID); err != nil {
		return err
	}
	state.MetadataUpgrade = nil
	if persist != nil {
		if err := persist(state); err != nil {
			return fmt.Errorf("persist completed metadata upgrade: %w", err)
		}
	}
	return nil
}

func replaceMetadataUpgradeState(state *bwsSyncStateV2, action syncPlannedAction, replacement bwsSecret, plan *syncPlanV2) error {
	entry, exists := state.Entries[action.EntryID]
	if !exists || action.Precondition == nil || entry.SecretID != action.Precondition.SecretID {
		return errors.New("metadata-upgrade baseline changed before state replacement")
	}
	entry.ProviderIdentity = plan.ProviderIdentity
	entry.Endpoint = action.Precondition.Endpoint
	entry.OrganizationID = action.Precondition.OrganizationID
	entry.ProjectID = action.Identity.ProjectID
	entry.MachineID = action.Identity.MachineID
	entry.SecretID = replacement.ID
	entry.Name = replacement.Key
	entry.Revision = replacement.RevisionDate
	entry.ValueSHA256 = valueSHA256(replacement.Value)
	state.Entries[action.EntryID] = entry
	state.Ownership[action.EntryID] = syncOwnershipRecord{
		SecretID: replacement.ID, ProviderIdentity: plan.ProviderIdentity,
		Revision: replacement.RevisionDate, Identity: action.Identity,
	}
	return nil
}

func (action syncPlannedAction) PreconditionContext() *syncPlanContext {
	precondition := action.Precondition
	return &syncPlanContext{ProviderIdentity: precondition.ProviderIdentity, Endpoint: precondition.Endpoint, OrganizationID: precondition.OrganizationID, ProjectID: precondition.ProjectID, MachineID: precondition.MachineID}
}

func validateMetadataReplacement(action syncPlannedAction, secret bwsSecret) error {
	if secret.ID == "" || secret.ID == action.Precondition.SecretID || secret.ProjectID != action.Identity.ProjectID ||
		secret.Key != action.IntendedName || valueSHA256(secret.Note) != action.IntendedNoteSHA256 ||
		valueSHA256(secret.Value) != action.IntendedValueSHA256 || secret.RevisionDate == "" {
		return errors.New("metadata replacement read-back does not match the exact intended record")
	}
	return nil
}

func metadataUpgradeResumeMatch(checkpoint *syncMetadataUpgradeCheckpoint, old bwsSecret, matches []bwsSecret, contextValue syncPlanContext, name, note string) bool {
	if checkpoint == nil || len(matches) != 1 || checkpoint.Old.SecretID != old.ID || checkpoint.New.SecretID != matches[0].ID {
		return false
	}
	return validateSecretPrecondition(&checkpoint.Old, old) == nil && validateSecretPrecondition(&checkpoint.New, matches[0]) == nil &&
		checkpoint.New.Name == name && checkpoint.New.NoteSHA256 == valueSHA256(note) && checkpoint.New.ProviderIdentity == contextValue.ProviderIdentity
}

func upgradeCheckpointNewMatches(checkpoint *syncMetadataUpgradeCheckpoint, action syncPlannedAction, secret bwsSecret) bool {
	return checkpoint != nil && checkpoint.Old.SecretID == action.Precondition.SecretID && checkpoint.New.SecretID == secret.ID &&
		validateSecretPrecondition(&checkpoint.New, secret) == nil
}

func upgradeCheckpointMatchesAction(checkpoint *syncMetadataUpgradeCheckpoint, action syncPlannedAction) bool {
	return checkpoint != nil && action.Precondition != nil && checkpoint.Old == *action.Precondition &&
		checkpoint.New.Name == action.IntendedName && checkpoint.New.NoteSHA256 == action.IntendedNoteSHA256 && checkpoint.New.ValueSHA256 == action.IntendedValueSHA256
}

func inferMaintenanceContext(state *bwsSyncStateV2) (syncPlanContext, error) {
	var result syncPlanContext
	if state.Context != nil {
		result = *state.Context
	}
	for _, entry := range state.Entries {
		candidate := syncPlanContext{ProviderIdentity: entry.ProviderIdentity, Endpoint: entry.Endpoint, OrganizationID: entry.OrganizationID, ProjectID: entry.ProjectID, MachineID: entry.MachineID}
		if result.ProviderIdentity == "" {
			result = candidate
			continue
		}
		merged, ok := coalesceSyncProviderContextOrganization(result, candidate)
		if !ok {
			return syncPlanContext{}, errors.New("maintenance state spans more than one provider context")
		}
		result = merged
	}
	if result.ProviderIdentity == "" && state.MetadataUpgrade != nil {
		old := state.MetadataUpgrade.Old
		result = syncPlanContext{ProviderIdentity: old.ProviderIdentity, Endpoint: old.Endpoint, OrganizationID: old.OrganizationID, ProjectID: old.ProjectID, MachineID: old.MachineID}
	}
	for id, owner := range state.Ownership {
		if !isLowerHex(id, 64) || syncEntryID(owner.Identity) != id || owner.SecretID == "" || owner.Revision == "" {
			return syncPlanContext{}, fmt.Errorf("maintenance ownership record %q is invalid", id)
		}
		if err := validateSyncIdentity(owner.Identity); err != nil {
			return syncPlanContext{}, fmt.Errorf("maintenance ownership record %q identity: %w", id, err)
		}
		if owner.ProviderIdentity != owner.Identity.Provider {
			return syncPlanContext{}, fmt.Errorf("maintenance ownership record %q has inconsistent provider identity", id)
		}
		if result.ProviderIdentity != "" && (owner.ProviderIdentity != result.ProviderIdentity || owner.Identity.Provider != result.ProviderIdentity || owner.Identity.ProjectID != result.ProjectID || owner.Identity.MachineID != result.MachineID) {
			return syncPlanContext{}, fmt.Errorf("maintenance ownership record %q crosses the pinned provider boundary", id)
		}
	}
	if err := validateSyncPlanContext(result); err != nil {
		return syncPlanContext{}, err
	}
	return result, nil
}

func indexMaintenanceLocals(entries []syncEntry) map[string]syncEntry {
	result := make(map[string]syncEntry, len(entries))
	for _, entry := range entries {
		result[maintenanceLocalKey(entry.ref.profile, entry.ref.kind, entry.ref.path, entry.key)] = entry
	}
	return result
}

func maintenanceLocalKey(profile string, scope scopeKind, localPath, key string) string {
	return strings.Join([]string{profile, string(scope), localPath, key}, "\x00")
}

func conflictsFromMaintenanceActions(actions []syncPlannedAction) []syncConflict {
	result := []syncConflict{}
	for _, action := range actions {
		if action.Kind == syncActionConflict {
			result = append(result, syncConflict{EntryID: action.EntryID, Reason: action.Reason, LocalPresent: action.LocalPresent, RemotePresent: action.RemotePresent, BaselinePresent: action.BaselinePresent})
		}
	}
	return result
}
