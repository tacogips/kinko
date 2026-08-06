package kinko

import (
	"errors"
	"fmt"
	"sort"
)

type syncConflictPolicy string

const (
	syncConflictFail   syncConflictPolicy = "fail"
	syncConflictLocal  syncConflictPolicy = "local"
	syncConflictRemote syncConflictPolicy = "remote"
	syncConflictSkip   syncConflictPolicy = "skip"
)

type syncResolution string

const (
	syncResolveLocal        syncResolution = "local"
	syncResolveRemote       syncResolution = "remote"
	syncResolveDeleteLocal  syncResolution = "delete-local"
	syncResolveDeleteRemote syncResolution = "delete-remote"
	syncResolveSkip         syncResolution = "skip"
)

type syncConflict struct {
	EntryID             string `json:"entry_id"`
	Reason              string `json:"reason"`
	LocalPresent        bool   `json:"local_present"`
	RemotePresent       bool   `json:"remote_present"`
	BaselinePresent     bool   `json:"baseline_present,omitempty"`
	RemoteDeleteAllowed bool   `json:"remote_delete_allowed,omitempty"`
}

// applySyncConflictRules resolves every conflict as one policy operation. The
// rules map cannot itself express duplicate CLI flags, so callers must reject
// duplicates while parsing; unmatched map entries are rejected here.
func applySyncConflictRules(plan *syncPlanV2, policy syncConflictPolicy, rules map[string]syncResolution, force bool) error {
	if plan == nil {
		return errors.New("sync plan is nil")
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return fmt.Errorf("validate sync plan before conflict policy: %w", err)
	}
	working := cloneSyncPlanV2(plan)
	if err := applySyncConflictRulesMutable(working, policy, rules, force); err != nil {
		return err
	}
	*plan = *working
	return nil
}

func applySyncConflictRulesMutable(plan *syncPlanV2, policy syncConflictPolicy, rules map[string]syncResolution, force bool) error {
	if policy == "" {
		policy = syncConflictFail
	}
	if err := validateSyncConflictPolicy(policy); err != nil {
		return err
	}
	if force && (policy != syncConflictFail || len(rules) != 0) {
		return errors.New("force cannot be combined with explicit conflict policy or resolution rules")
	}
	conflicts := make(map[string]syncConflict, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		if _, exists := conflicts[conflict.EntryID]; exists {
			return fmt.Errorf("duplicate conflict entry %s", conflict.EntryID)
		}
		conflicts[conflict.EntryID] = conflict
	}
	for entryID, resolution := range rules {
		if _, exists := conflicts[entryID]; !exists {
			return fmt.Errorf("resolution rule %s matches no current conflict", entryID)
		}
		if err := validateSyncResolution(resolution); err != nil {
			return fmt.Errorf("resolution rule %s: %w", entryID, err)
		}
	}
	for index := range plan.Actions {
		action := &plan.Actions[index]
		conflict, exists := conflicts[action.EntryID]
		if !exists {
			continue
		}
		resolution, explicit := rules[action.EntryID]
		if !explicit {
			resolution = resolutionForPolicy(policy)
			if force {
				if plan.Operation == syncOperationPush {
					resolution = syncResolveLocal
				} else if plan.Operation == syncOperationPull {
					resolution = syncResolveRemote
				} else {
					return fmt.Errorf("force is not supported for %s", plan.Operation)
				}
			}
		}
		if resolution == "" {
			continue
		}
		if err := resolveSyncConflict(action, conflict, resolution, plan.Operation); err != nil {
			return fmt.Errorf("resolve conflict %s: %w", action.EntryID, err)
		}
	}
	remaining := make([]syncConflict, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		action := findPlannedAction(plan.Actions, conflict.EntryID)
		if action != nil && action.Kind == syncActionConflict {
			remaining = append(remaining, conflict)
		}
	}
	plan.Conflicts = remaining
	return finalizeSyncPlan(plan)
}

func validateSyncConflictPolicy(policy syncConflictPolicy) error {
	switch policy {
	case syncConflictFail, syncConflictLocal, syncConflictRemote, syncConflictSkip:
		return nil
	default:
		return fmt.Errorf("unsupported conflict policy %q", policy)
	}
}

func validateSyncResolution(resolution syncResolution) error {
	switch resolution {
	case syncResolveLocal, syncResolveRemote, syncResolveDeleteLocal, syncResolveDeleteRemote, syncResolveSkip:
		return nil
	default:
		return fmt.Errorf("unsupported conflict resolution %q", resolution)
	}
}

func resolutionForPolicy(policy syncConflictPolicy) syncResolution {
	switch policy {
	case syncConflictLocal:
		return syncResolveLocal
	case syncConflictRemote:
		return syncResolveRemote
	case syncConflictSkip:
		return syncResolveSkip
	default:
		return ""
	}
}

func resolveSyncConflict(action *syncPlannedAction, conflict syncConflict, resolution syncResolution, operation syncOperation) error {
	action.Resolution = resolution
	switch resolution {
	case syncResolveSkip:
		action.Kind = syncActionIgnore
	case syncResolveDeleteLocal:
		if !conflict.LocalPresent {
			return errors.New("cannot delete an absent local side")
		}
		action.Kind = syncActionDelete
	case syncResolveDeleteRemote:
		if !conflict.RemotePresent {
			return errors.New("cannot delete an absent remote side")
		}
		if !conflict.RemoteDeleteAllowed {
			return errors.New("remote deletion lacks baseline, ownership, or exact prune acknowledgement")
		}
		action.Kind = syncActionDelete
	case syncResolveLocal:
		if conflict.LocalPresent {
			if conflict.RemotePresent {
				action.Kind = syncActionUpdate
			} else {
				action.Kind = syncActionCreate
			}
		} else {
			if !conflict.RemoteDeleteAllowed {
				return errors.New("local-wins remote deletion lacks ownership proof")
			}
			action.Kind, action.Resolution = syncActionDelete, syncResolveDeleteRemote
		}
	case syncResolveRemote:
		if conflict.RemotePresent {
			if conflict.LocalPresent {
				action.Kind = syncActionUpdate
			} else {
				action.Kind = syncActionCreate
			}
		} else {
			if !conflict.LocalPresent {
				return errors.New("remote resolution has no present side")
			}
			action.Kind, action.Resolution = syncActionDelete, syncResolveDeleteLocal
		}
	default:
		return fmt.Errorf("unsupported conflict resolution %q", resolution)
	}
	action.Reason = "conflict resolved by " + string(action.Resolution)
	action.RequiredCapabilities = capabilitiesForPlannedAction(*action, operation)
	return nil
}

func findPlannedAction(actions []syncPlannedAction, entryID string) *syncPlannedAction {
	index := sort.Search(len(actions), func(index int) bool { return actions[index].EntryID >= entryID })
	if index < len(actions) && actions[index].EntryID == entryID {
		return &actions[index]
	}
	return nil
}
