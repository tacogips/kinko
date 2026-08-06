package kinko

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type syncResetOptions struct {
	Provider   string
	Baseline   bool
	Checkpoint bool
	Yes        bool
	JSON       bool
	Selector   syncSelector
}

func buildSyncResetPlan(envelope syncStateEnvelope, options syncResetOptions) (*syncPlanV2, error) {
	if options.Provider != "" && options.Provider != supportedSyncProvider {
		return nil, fmt.Errorf("unsupported sync provider %q", options.Provider)
	}
	selector, selectorDigest, err := normalizeSyncSelector(options.Selector)
	if err != nil {
		return nil, err
	}
	resetBaseline, resetCheckpoint := options.Baseline, options.Checkpoint
	if !resetBaseline && !resetCheckpoint {
		resetBaseline, resetCheckpoint = true, true
	}
	plan := &syncPlanV2{
		Format: syncPlanFormatV2, Operation: syncOperationReset,
		ProviderIdentity: maintenanceProviderIdentity(envelope), SelectorDigest: selectorDigest,
		Actions: []syncPlannedAction{}, Conflicts: []syncConflict{},
		Maintenance: &syncMaintenancePlan{
			Apply:                   options.Yes,
			ResetBaseline:           resetBaseline,
			ResetCheckpoint:         resetCheckpoint,
			ResetCheckpointUnscoped: resetCheckpoint && isDefaultSyncSelector(options.Selector),
			Warnings:                []string{"resetting sync history can make a future run unable to distinguish resurrection from deletion"},
		},
	}
	if resetBaseline {
		actions, err := selectedResetActions(envelope, selector)
		if err != nil {
			return nil, err
		}
		plan.Actions = actions
	}
	if resetCheckpoint {
		checkpoint, err := checkpointFromEnvelope(envelope)
		if err != nil {
			return nil, err
		}
		if checkpoint != nil && checkpoint.SelectorDigest != selectorDigest && !plan.Maintenance.ResetCheckpointUnscoped {
			return nil, errors.New("checkpoint selector digest does not match the requested selector")
		}
	}
	if len(plan.Actions) == 0 && !resetCheckpoint {
		return nil, errors.New("effective sync selection is empty for a mutating workflow")
	}
	if err := finalizeSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func applySyncReset(dataDir string, dek []byte, config map[string]string, plan *syncPlanV2) error {
	if err := requireMaintenanceApply(plan, syncOperationReset, plan != nil && plan.Maintenance != nil && plan.Maintenance.Apply); err != nil {
		return err
	}
	if config == nil || plan.Maintenance == nil {
		return errors.New("sync reset persistence is not initialized")
	}
	encoded := strings.TrimSpace(config[configKeyBWSSyncState])
	if encoded == "" {
		return errors.New("BWS sync state is unavailable for reset")
	}
	envelope, err := decodeBWSSyncState(encoded)
	if err != nil {
		return err
	}
	root := cloneRawMap(envelope.Raw)
	if plan.Maintenance.ResetBaseline {
		entries, err := rawObject(root["entries"])
		if err != nil {
			return fmt.Errorf("decode reset baseline entries: %w", err)
		}
		for _, action := range plan.Actions {
			if action.Reason != syncMaintenanceReasonResetBaseline {
				continue
			}
			delete(entries, resetStateMapKey(action))
		}
		root["entries"], err = marshalRawObject(entries)
		if err != nil {
			return err
		}
	}
	if plan.Maintenance.ResetCheckpoint {
		checkpoint, err := checkpointFromEnvelope(envelope)
		if err != nil {
			return err
		}
		if checkpoint != nil && checkpoint.SelectorDigest != plan.SelectorDigest && !plan.Maintenance.ResetCheckpointUnscoped {
			return errors.New("checkpoint changed or its selector no longer matches the reset plan")
		}
		delete(root, "checkpoint")
	}
	nextState, err := marshalRawObject(root)
	if err != nil {
		return err
	}
	nextConfig := cloneStringMap(config)
	nextConfig[configKeyBWSSyncState] = string(nextState)
	if err := saveConfig(dataDir, dek, nextConfig); err != nil {
		return fmt.Errorf("persist encrypted sync reset: %w", err)
	}
	config[configKeyBWSSyncState] = string(nextState)
	return nil
}

func selectedResetActions(envelope syncStateEnvelope, selector syncSelector) ([]syncPlannedAction, error) {
	identities, keys, err := resetStateIdentities(envelope)
	if err != nil {
		return nil, err
	}
	selected, err := selectSyncIdentities(selector, identities)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	actions := make([]syncPlannedAction, 0, len(ids))
	for _, id := range ids {
		identity := identities[keys[id].index]
		actions = append(actions, syncPlannedAction{
			EntryID: id, Identity: identity, Kind: syncActionDelete,
			BaselinePresent: true, Reason: syncMaintenanceReasonResetBaseline,
			IntendedName: keys[id].mapKey,
		})
	}
	return actions, nil
}

type resetStateKey struct {
	index  int
	mapKey string
}

func resetStateIdentities(envelope syncStateEnvelope) ([]syncIdentity, map[string]resetStateKey, error) {
	identities := []syncIdentity{}
	keys := map[string]resetStateKey{}
	if envelope.Format == 0 {
		return identities, keys, nil
	}
	if envelope.Format == bwsSyncStateFormatV2 {
		state, err := decodeBWSSyncStateV2(envelope)
		if err != nil {
			return nil, nil, err
		}
		for mapKey, entry := range state.Entries {
			identity := identityForStateEntry(entry)
			id := syncEntryID(identity)
			keys[id] = resetStateKey{index: len(identities), mapKey: mapKey}
			identities = append(identities, identity)
		}
		return identities, keys, nil
	}
	legacy, err := decodeLegacySyncEnvelope(envelope)
	if err != nil {
		return nil, nil, err
	}
	for mapKey, entry := range legacy.Entries {
		identity := syncIdentity{ProjectID: legacy.ProjectID, MachineID: legacy.MachineID, Profile: entry.Profile, Key: entry.Key, Scope: entry.Scope}
		if entry.Scope == scopeKindPath {
			identity.Path = "local:" + entry.Path
		}
		id := syncEntryID(identity)
		keys[id] = resetStateKey{index: len(identities), mapKey: mapKey}
		identities = append(identities, identity)
	}
	return identities, keys, nil
}

func resetStateMapKey(action syncPlannedAction) string { return action.IntendedName }

func checkpointFromEnvelope(envelope syncStateEnvelope) (*syncCheckpoint, error) {
	if envelope.Format != bwsSyncStateFormatV2 {
		return nil, nil
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		return nil, err
	}
	return state.Checkpoint, nil
}

func decodeLegacySyncEnvelope(envelope syncStateEnvelope) (*bwsSyncState, error) {
	encoded, err := json.Marshal(envelope.Raw)
	if err != nil {
		return nil, err
	}
	var state bwsSyncState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return nil, err
	}
	if err := validateBWSSyncState(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func maintenanceProviderIdentity(envelope syncStateEnvelope) string {
	if envelope.Format == bwsSyncStateFormatV2 {
		state, err := decodeBWSSyncStateV2(envelope)
		if err == nil {
			for _, entry := range state.Entries {
				return entry.ProviderIdentity
			}
			for _, owner := range state.Ownership {
				return owner.ProviderIdentity
			}
		}
	}
	return fullSHA256([]byte("kinko.sync.maintenance.local"))
}

func isDefaultSyncSelector(selector syncSelector) bool {
	return len(selector.IncludeProfiles) == 0 && len(selector.IncludePaths) == 0 && len(selector.IncludeKeys) == 0 &&
		len(selector.ExcludeProfiles) == 0 && len(selector.ExcludePaths) == 0 && len(selector.ExcludeKeys) == 0 &&
		(selector.Shared == "" || selector.Shared == syncSharedInclude)
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
