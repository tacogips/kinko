package kinko

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type syncEntry struct {
	ref   scopeRef
	key   string
	value string
}

type syncActionKind int

const (
	syncActionCreate syncActionKind = iota
	syncActionUpdate
	syncActionDelete
	syncActionAdopt
	syncActionUnchanged
	syncActionConflict
	syncActionIgnore
)

type syncAction struct {
	kind   syncActionKind
	name   string
	ref    scopeRef
	entry  *syncEntry
	remote *bwsSecret
	forced syncActionKind
	reason string
}

type syncPlan struct {
	actions   []syncAction
	conflicts []syncAction
	direction syncDirection
}

func collectSyncEntries(data *vaultData) ([]syncEntry, error) {
	if data == nil {
		return nil, errors.New("vault data is nil")
	}
	refs := []scopeRef{{kind: scopeKindShared}}
	entries := make([]syncEntry, 0)
	for key, value := range data.Shared {
		if key == sharedKeyBWSAccessToken {
			continue
		}
		entries = append(entries, syncEntry{ref: scopeRef{kind: scopeKindShared}, key: key, value: value})
	}
	for profile, scopes := range data.Profiles {
		for path, values := range scopes {
			ref, err := normalizeScopeRef(scopeRef{profile: profile, kind: scopeKindPath, path: path})
			if err != nil {
				return nil, newCLIError(exitCodePolicyFailed, "A stored path scope is invalid for sync.", err)
			}
			refs = append(refs, ref)
			for key, value := range values {
				if key == sharedKeyBWSAccessToken {
					continue
				}
				entries = append(entries, syncEntry{ref: ref, key: key, value: value})
			}
		}
	}
	if err := detectScopeHashCollisions(refs); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		left := formatSyncEntry(entries[i])
		right := formatSyncEntry(entries[j])
		return left < right
	})
	return entries, nil
}

func buildPushPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error) {
	localByName, err := indexLocalSyncEntries(entries, machineID)
	if err != nil {
		return nil, err
	}
	remoteByName, remoteEntries, err := validateRemoteSyncSecrets(remote, machineID)
	if err != nil {
		return nil, err
	}
	return buildPlanWithRemoteEntries(syncDirectionPush, localByName, remoteByName, remoteEntries, state)
}

func buildPullPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error) {
	localByName, err := indexLocalSyncEntries(entries, machineID)
	if err != nil {
		return nil, err
	}
	remoteByName, remoteEntries, err := validateRemoteSyncSecrets(remote, machineID)
	if err != nil {
		return nil, err
	}
	for name, entry := range remoteEntries {
		if entry.key == sharedKeyBWSAccessToken {
			delete(remoteByName, name)
			delete(remoteEntries, name)
		}
	}
	return buildPlanWithRemoteEntries(syncDirectionPull, localByName, remoteByName, remoteEntries, state)
}

func indexLocalSyncEntries(entries []syncEntry, machineID string) (map[string]syncEntry, error) {
	localByName := make(map[string]syncEntry, len(entries))
	for _, entry := range entries {
		name := buildBWSSecretName(machineID, entry.ref, entry.key)
		if _, exists := localByName[name]; exists {
			return nil, newCLIError(exitCodePolicyFailed, "Multiple local sync entries resolve to the same BWS name.", errors.New("duplicate local sync name"))
		}
		localByName[name] = entry
	}
	return localByName, nil
}

func buildPlanWithRemoteEntries(direction syncDirection, local map[string]syncEntry, remote map[string]bwsSecret, remoteEntries map[string]syncEntry, state *bwsSyncState) (*syncPlan, error) {
	if state == nil {
		state = emptyBWSSyncState()
	}
	if err := validateSyncPlanScopes(local, remoteEntries, state); err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(local)+len(remote)+len(state.Entries))
	for name := range local {
		names[name] = struct{}{}
	}
	for name := range remote {
		names[name] = struct{}{}
	}
	for name := range state.Entries {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	plan := &syncPlan{direction: direction}
	for _, name := range ordered {
		localEntry, localPresent := local[name]
		remoteSecret, remotePresent := remote[name]
		stateEntry, statePresent := state.Entries[name]
		action := syncAction{name: name}
		if localPresent {
			action.entry = copySyncEntry(localEntry)
			action.ref = localEntry.ref
		} else if remoteEntry, ok := remoteEntries[name]; ok {
			action.entry = copySyncEntry(remoteEntry)
			action.ref = remoteEntry.ref
		} else if statePresent {
			action.ref = scopeRef{profile: stateEntry.Profile, kind: stateEntry.Scope, path: stateEntry.Path}
		}
		if remotePresent {
			copy := remoteSecret
			action.remote = &copy
		}
		if direction == syncDirectionPush {
			classifyPushAction(&action, localPresent, remotePresent, statePresent, localEntry, remoteSecret, stateEntry)
		} else {
			classifyPullAction(&action, localPresent, remotePresent, statePresent, localEntry, remoteSecret, stateEntry)
		}
		plan.actions = append(plan.actions, action)
		if action.kind == syncActionConflict {
			plan.conflicts = append(plan.conflicts, action)
		}
	}
	return plan, nil
}

func validateSyncPlanScopes(local, remote map[string]syncEntry, state *bwsSyncState) error {
	refs := make([]scopeRef, 0, len(local)+len(remote)+len(state.Entries))
	for _, entry := range local {
		refs = append(refs, entry.ref)
	}
	for _, entry := range remote {
		refs = append(refs, entry.ref)
	}
	for _, entry := range state.Entries {
		refs = append(refs, scopeRef{profile: entry.Profile, kind: entry.Scope, path: entry.Path})
	}
	return detectScopeHashCollisions(refs)
}

func classifyPushAction(action *syncAction, localPresent, remotePresent, statePresent bool, local syncEntry, remote bwsSecret, state syncStateEntry) {
	switch {
	case localPresent && !remotePresent && !statePresent:
		action.kind = syncActionCreate
	case localPresent && !remotePresent && statePresent:
		setConflict(action, syncActionCreate, "remote secret was deleted after the last sync")
	case localPresent && remotePresent && valuesEqual(local.value, remote.Value):
		setAdoptOrUnchanged(action, statePresent, state, remote)
	case localPresent && remotePresent && !statePresent:
		setConflict(action, syncActionUpdate, "local and remote values differ without a sync baseline")
	case localPresent && remotePresent && remote.RevisionDate == state.RevisionDate:
		action.kind = syncActionUpdate
	case localPresent && remotePresent:
		setConflict(action, syncActionUpdate, "remote secret changed after the last sync")
	case !localPresent && remotePresent && statePresent && remote.RevisionDate == state.RevisionDate:
		action.kind = syncActionDelete
	case !localPresent && remotePresent && statePresent:
		setConflict(action, syncActionDelete, "remote secret changed after the local deletion")
	case !localPresent && remotePresent:
		action.kind = syncActionIgnore
	case !localPresent && !remotePresent && statePresent:
		action.kind = syncActionUnchanged
	default:
		action.kind = syncActionIgnore
	}
}

func classifyPullAction(action *syncAction, localPresent, remotePresent, statePresent bool, local syncEntry, remote bwsSecret, state syncStateEntry) {
	switch {
	case remotePresent && !localPresent && !statePresent:
		action.kind = syncActionCreate
	case remotePresent && !localPresent && statePresent:
		setConflict(action, syncActionCreate, "local secret was deleted after the last sync")
	case remotePresent && localPresent && valuesEqual(local.value, remote.Value):
		setAdoptOrUnchanged(action, statePresent, state, remote)
	case remotePresent && localPresent && !statePresent:
		setConflict(action, syncActionUpdate, "local and remote values differ without a sync baseline")
	case remotePresent && localPresent && valueSHA256(local.value) == state.ValueSHA256:
		action.kind = syncActionUpdate
	case remotePresent && localPresent:
		setConflict(action, syncActionUpdate, "local secret changed after the last sync")
	case !remotePresent && localPresent && statePresent && valueSHA256(local.value) == state.ValueSHA256:
		action.kind = syncActionDelete
	case !remotePresent && localPresent && statePresent:
		setConflict(action, syncActionDelete, "local secret changed after the remote deletion")
	case !remotePresent && localPresent:
		action.kind = syncActionIgnore
	case !remotePresent && !localPresent && statePresent:
		action.kind = syncActionUnchanged
	default:
		action.kind = syncActionIgnore
	}
}

func validateRemoteSyncSecrets(secrets []bwsSecret, machineID string) (map[string]bwsSecret, map[string]syncEntry, error) {
	if err := validateUniqueRemoteSecretIDs(secrets); err != nil {
		return nil, nil, err
	}
	byName := map[string]bwsSecret{}
	entries := map[string]syncEntry{}
	seenOwnedNames := map[string]struct{}{}
	var invalidIDs []string
	var duplicateNames []string
	for _, secret := range secrets {
		_, key, owned := parseBWSSecretName(machineID, secret.Key)
		if !owned {
			continue
		}
		if _, exists := seenOwnedNames[secret.Key]; exists {
			duplicateNames = append(duplicateNames, secret.Key)
			continue
		}
		seenOwnedNames[secret.Key] = struct{}{}
		if key == sharedKeyBWSAccessToken {
			continue
		}
		metadata, err := parseBWSNote(secret.Note)
		if err == nil {
			err = verifyNoteMatchesName(machineID, secret.Key, metadata)
		}
		if err != nil {
			invalidIDs = append(invalidIDs, secret.ID)
			continue
		}
		ref := scopeRef{profile: metadata.Profile, kind: metadata.Scope, path: metadata.Path}
		byName[secret.Key] = secret
		entries[secret.Key] = syncEntry{ref: ref, key: metadata.Key, value: secret.Value}
	}
	if len(duplicateNames) > 0 {
		sort.Strings(duplicateNames)
		return nil, nil, newCLIError(exitCodePolicyFailed, "Duplicate machine-owned BWS secret names: "+strings.Join(uniqueStrings(duplicateNames), ", ")+".", errors.New("ambiguous remote secrets"))
	}
	if len(invalidIDs) > 0 {
		sort.Strings(invalidIDs)
		return nil, nil, newCLIError(exitCodePolicyFailed, "Malformed metadata on machine-owned BWS secret ids: "+strings.Join(invalidIDs, ", ")+".", errors.New("invalid remote sync metadata"))
	}
	return byName, entries, nil
}

func validateUniqueRemoteSecretIDs(secrets []bwsSecret) error {
	seen := make(map[string]struct{}, len(secrets))
	var duplicateIDs []string
	for _, secret := range secrets {
		if _, exists := seen[secret.ID]; exists {
			duplicateIDs = append(duplicateIDs, secret.ID)
			continue
		}
		seen[secret.ID] = struct{}{}
	}
	if len(duplicateIDs) == 0 {
		return nil
	}
	sort.Strings(duplicateIDs)
	return newCLIError(exitCodePolicyFailed, "Duplicate BWS secret ids: "+strings.Join(uniqueStrings(duplicateIDs), ", ")+".", errors.New("ambiguous remote secret ids"))
}

func setConflict(action *syncAction, forced syncActionKind, reason string) {
	action.kind = syncActionConflict
	action.forced = forced
	action.reason = reason
}

func setAdoptOrUnchanged(action *syncAction, statePresent bool, state syncStateEntry, remote bwsSecret) {
	if statePresent && state.SecretID == remote.ID && state.RevisionDate == remote.RevisionDate && state.ValueSHA256 == valueSHA256(remote.Value) {
		action.kind = syncActionUnchanged
		return
	}
	action.kind = syncActionAdopt
}

func valuesEqual(left, right string) bool {
	return valueSHA256(left) == valueSHA256(right)
}

func valueSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func copySyncEntry(entry syncEntry) *syncEntry {
	copy := entry
	return &copy
}

func formatSyncEntry(entry syncEntry) string {
	return formatScopeRef(entry.ref) + " / " + entry.key
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func formatSyncAction(action syncAction) string {
	key := "unknown"
	if action.entry != nil {
		key = action.entry.key
	} else if _, parsed, ok := parseAnyBWSKey(action.name); ok {
		key = parsed
	}
	return fmt.Sprintf("%s / %s: %s", formatScopeRef(action.ref), key, action.reason)
}

func parseAnyBWSKey(name string) (string, string, bool) {
	if len(name) < 26 || name[16] != '_' || name[25] != '_' {
		return "", "", false
	}
	return name[17:25], name[26:], true
}
