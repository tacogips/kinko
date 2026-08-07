package kinko

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type syncResult struct {
	Created   int              `json:"created"`
	Updated   int              `json:"updated"`
	Deleted   int              `json:"deleted"`
	Unchanged int              `json:"unchanged"`
	Adopted   int              `json:"adopted"`
	Conflicts []string         `json:"conflicts"`
	Actions   []syncResultItem `json:"actions"`
	Partial   bool             `json:"partial"`
}

type syncResultItem struct {
	Action  string `json:"action"`
	Profile string `json:"profile,omitempty"`
	Scope   string `json:"scope"`
	Path    string `json:"path,omitempty"`
	Key     string `json:"key"`
	Reason  string `json:"reason,omitempty"`
}

func applyPushPlan(ctx context.Context, client *bwsClient, projectID string, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error) {
	_, err := prepareSyncResult(plan, force)
	if err != nil {
		preview, _ := prepareSyncResult(plan, false)
		return preview, err
	}
	result := syncResult{}
	if client == nil || state == nil {
		return result, errors.New("push executor is not initialized")
	}

	remoteMutationApplied := false
	var deletes []syncAction
	for _, action := range plan.actions {
		kind := effectiveActionKind(action, force)
		switch kind {
		case syncActionCreate:
			note, err := noteForAction(action, state.MachineID)
			if err != nil {
				return setPartial(result, remoteMutationApplied), err
			}
			created, err := client.createSecret(ctx, projectID, action.name, action.entry.value, note)
			if err != nil {
				return setPartial(result, mutationFailureIsPartial(remoteMutationApplied, err)), fmt.Errorf("create BWS secret %s: %w", actionLabel(action), err)
			}
			remoteMutationApplied = true
			state.Entries[action.name] = stateEntryFor(action, created, action.entry.value)
			appendAppliedAction(&result, action, kind)
		case syncActionUpdate:
			note, err := noteForAction(action, state.MachineID)
			if err != nil {
				return setPartial(result, remoteMutationApplied), err
			}
			updated, err := client.editSecret(ctx, action.remote.ID, projectID, action.name, action.entry.value, note)
			if err != nil {
				return setPartial(result, mutationFailureIsPartial(remoteMutationApplied, err)), fmt.Errorf("update BWS secret %s: %w", actionLabel(action), err)
			}
			remoteMutationApplied = true
			state.Entries[action.name] = stateEntryFor(action, updated, action.entry.value)
			appendAppliedAction(&result, action, kind)
		case syncActionDelete:
			deletes = append(deletes, action)
		case syncActionAdopt, syncActionUnchanged:
			if action.remote == nil {
				delete(state.Entries, action.name)
			} else {
				value := action.remote.Value
				state.Entries[action.name] = stateEntryFor(action, *action.remote, value)
			}
			appendAppliedAction(&result, action, kind)
		case syncActionIgnore:
			// Entries without a baseline belong to the other direction.
			appendAppliedAction(&result, action, kind)
		}
	}
	for _, action := range deletes {
		if err := client.deleteSecrets(ctx, []string{action.remote.ID}); err != nil {
			return setPartial(result, true), fmt.Errorf("delete BWS secret %s: %w", actionLabel(action), err)
		}
		delete(state.Entries, action.name)
		appendAppliedAction(&result, action, syncActionDelete)
	}
	return result, nil
}

func applyPullPlan(data *vaultData, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error) {
	result, err := prepareSyncResult(plan, force)
	if err != nil {
		return result, err
	}
	if data == nil || state == nil {
		return result, errors.New("pull executor is not initialized")
	}
	if data.Profiles == nil {
		data.Profiles = map[string]map[string]map[string]string{}
	}
	if data.Shared == nil {
		data.Shared = map[string]string{}
	}
	for _, action := range plan.actions {
		kind := effectiveActionKind(action, force)
		switch kind {
		case syncActionCreate, syncActionUpdate:
			setLocalSyncValue(data, action.ref, action.entry.key, action.remote.Value)
			state.Entries[action.name] = stateEntryFor(action, *action.remote, action.remote.Value)
		case syncActionDelete:
			deleteLocalSyncValue(data, action.ref, syncActionKey(action))
			delete(state.Entries, action.name)
		case syncActionAdopt, syncActionUnchanged:
			if action.remote == nil {
				delete(state.Entries, action.name)
			} else {
				state.Entries[action.name] = stateEntryFor(action, *action.remote, action.remote.Value)
			}
		case syncActionIgnore:
		}
	}
	return result, nil
}

func prepareSyncResult(plan *syncPlan, force bool) (syncResult, error) {
	if plan == nil {
		return syncResult{}, errors.New("sync plan is nil")
	}
	result := syncResult{}
	for _, action := range plan.actions {
		kind := effectiveActionKind(action, force)
		result.Actions = append(result.Actions, resultItemFor(action, kind))
		switch kind {
		case syncActionCreate:
			result.Created++
		case syncActionUpdate:
			result.Updated++
		case syncActionDelete:
			result.Deleted++
		case syncActionAdopt:
			result.Adopted++
		case syncActionUnchanged:
			result.Unchanged++
		case syncActionConflict:
			result.Conflicts = append(result.Conflicts, formatSyncAction(action))
		}
	}
	if len(result.Conflicts) > 0 && !force {
		sort.Strings(result.Conflicts)
		return result, newCLIError(exitCodeSyncConflict, fmt.Sprintf("Sync conflicts detected: %d. Re-run with --force to make the %s side authoritative.", len(result.Conflicts), conflictAuthority(plan)), errors.New("sync conflict"))
	}
	return result, nil
}

func effectiveActionKind(action syncAction, force bool) syncActionKind {
	if action.kind == syncActionConflict && force {
		return action.forced
	}
	return action.kind
}

func stateEntryFor(action syncAction, remote bwsSecret, value string) syncStateEntry {
	return syncStateEntry{
		SecretID:     remote.ID,
		Name:         action.name,
		Profile:      action.ref.profile,
		Scope:        action.ref.kind,
		Path:         action.ref.path,
		Key:          syncActionKey(action),
		RevisionDate: remote.RevisionDate,
		ValueSHA256:  valueSHA256(value),
	}
}

func noteForAction(action syncAction, machineID string) (string, error) {
	return encodeBWSNote(bwsNoteMetadata{
		KinkoSyncFormat: 1,
		MachineID:       machineID,
		Profile:         action.ref.profile,
		Scope:           action.ref.kind,
		Path:            action.ref.path,
		Key:             syncActionKey(action),
	})
}

func syncActionKey(action syncAction) string {
	if action.entry != nil {
		return action.entry.key
	}
	_, key, _ := parseAnyBWSKey(action.name)
	return key
}

func setLocalSyncValue(data *vaultData, ref scopeRef, key, value string) {
	if ref.kind == scopeKindShared {
		data.Shared[key] = value
		return
	}
	if data.Profiles[ref.profile] == nil {
		data.Profiles[ref.profile] = map[string]map[string]string{}
	}
	if data.Profiles[ref.profile][ref.path] == nil {
		data.Profiles[ref.profile][ref.path] = map[string]string{}
	}
	data.Profiles[ref.profile][ref.path][key] = value
}

func deleteLocalSyncValue(data *vaultData, ref scopeRef, key string) {
	if ref.kind == scopeKindShared {
		delete(data.Shared, key)
		return
	}
	if scopes := data.Profiles[ref.profile]; scopes != nil {
		if values := scopes[ref.path]; values != nil {
			delete(values, key)
		}
	}
}

func resultItemFor(action syncAction, kind syncActionKind) syncResultItem {
	return syncResultItem{
		Action:  syncActionKindName(kind),
		Profile: action.ref.profile,
		Scope:   string(action.ref.kind),
		Path:    action.ref.path,
		Key:     syncActionKey(action),
		Reason:  action.reason,
	}
}

func syncActionKindName(kind syncActionKind) string {
	switch kind {
	case syncActionCreate:
		return "create"
	case syncActionUpdate:
		return "update"
	case syncActionDelete:
		return "delete"
	case syncActionAdopt:
		return "adopt"
	case syncActionUnchanged:
		return "unchanged"
	case syncActionConflict:
		return "conflict"
	default:
		return "ignore"
	}
}

func setPartial(result syncResult, partial bool) syncResult {
	result.Partial = partial
	return result
}

func mutationFailureIsPartial(previouslyApplied bool, err error) bool {
	return previouslyApplied || errors.Is(err, errBWSInvalidJSON)
}

func appendAppliedAction(result *syncResult, action syncAction, kind syncActionKind) {
	result.Actions = append(result.Actions, resultItemFor(action, kind))
	switch kind {
	case syncActionCreate:
		result.Created++
	case syncActionUpdate:
		result.Updated++
	case syncActionDelete:
		result.Deleted++
	case syncActionAdopt:
		result.Adopted++
	case syncActionUnchanged:
		result.Unchanged++
	}
}

func actionLabel(action syncAction) string {
	return fmt.Sprintf("%s / %s", formatScopeRef(action.ref), syncActionKey(action))
}

func conflictAuthority(plan *syncPlan) string {
	if plan != nil && plan.direction == syncDirectionPush {
		return "local"
	}
	return "remote"
}
