package kinko

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type syncDeleteMode string

const (
	syncDeleteAuto    syncDeleteMode = "auto"
	syncDeleteKeep    syncDeleteMode = "keep"
	syncDeleteConfirm syncDeleteMode = "confirm"
)

func applySyncDeletionPolicy(plan *syncPlanV2, mode syncDeleteMode, yes bool) error {
	if plan == nil {
		return errors.New("sync plan is nil")
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return fmt.Errorf("validate sync plan before deletion policy: %w", err)
	}
	working := cloneSyncPlanV2(plan)
	if err := applySyncDeletionPolicyMutable(working, mode, yes); err != nil {
		return err
	}
	*plan = *working
	return nil
}

func applySyncDeletionPolicyMutable(plan *syncPlanV2, mode syncDeleteMode, yes bool) error {
	if mode == "" {
		mode = syncDeleteAuto
	}
	if mode != syncDeleteAuto && mode != syncDeleteKeep && mode != syncDeleteConfirm {
		return fmt.Errorf("unsupported sync deletion mode %q", mode)
	}
	deletionCount := 0
	for index := range plan.Actions {
		action := &plan.Actions[index]
		if action.Kind != syncActionDelete {
			continue
		}
		deletionCount++
		if mode == syncDeleteKeep {
			action.Kind = syncActionIgnore
			action.Reason = "deletion retained by keep policy"
			action.RequiredCapabilities = []syncCapability{syncCapabilityRead}
			continue
		}
		if deletesRemote(*action, plan.Operation) && !action.RemoteDeleteAllowed {
			return fmt.Errorf("remote deletion %s lacks baseline, ownership, or exact prune acknowledgement", action.EntryID)
		}
		action.RequiredCapabilities = capabilitiesForPlannedAction(*action, plan.Operation)
	}
	if mode == syncDeleteConfirm && deletionCount > 0 && !yes {
		return errors.New("confirm deletion mode requires yes for a plan containing deletions")
	}
	return finalizeSyncPlan(plan)
}

// applySyncPruneAcknowledgements converts exact, already-selected and pinned
// remote ids into prune deletions. It does not expand selection or weaken any
// provider, project, machine, identity, or revision precondition.
func applySyncPruneAcknowledgements(plan *syncPlanV2, secretIDs []string) error {
	if plan == nil {
		return errors.New("sync plan is nil")
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return fmt.Errorf("validate sync plan before prune acknowledgements: %w", err)
	}
	if plan.Operation != syncOperationPrune {
		return fmt.Errorf("exact remote deletion acknowledgements require a prune plan, got %s", plan.Operation)
	}
	working := cloneSyncPlanV2(plan)
	if err := applySyncPruneAcknowledgementsMutable(working, secretIDs); err != nil {
		return err
	}
	*plan = *working
	return nil
}

func applySyncPruneAcknowledgementsMutable(plan *syncPlanV2, secretIDs []string) error {
	seen := make(map[string]struct{}, len(secretIDs))
	acknowledgedEntries := make(map[string]struct{}, len(secretIDs))
	for _, secretID := range secretIDs {
		if strings.TrimSpace(secretID) == "" {
			return errors.New("prune acknowledgement secret id is empty")
		}
		if _, duplicate := seen[secretID]; duplicate {
			return fmt.Errorf("duplicate prune acknowledgement for secret id %q", secretID)
		}
		seen[secretID] = struct{}{}
		matched := false
		for index := range plan.Actions {
			action := &plan.Actions[index]
			if action.Precondition == nil || action.Precondition.SecretID != secretID {
				continue
			}
			if !action.RemotePresent {
				return fmt.Errorf("prune acknowledgement %q does not identify a present remote side", secretID)
			}
			action.Kind = syncActionDelete
			action.Resolution = syncResolveDeleteRemote
			action.Reason = "remote deletion explicitly acknowledged by immutable secret id"
			action.RemoteDeleteAllowed = true
			action.RequiredCapabilities = []syncCapability{syncCapabilityDelete}
			acknowledgedEntries[action.EntryID] = struct{}{}
			matched = true
			break
		}
		if !matched {
			return fmt.Errorf("prune acknowledgement %q matches no selected pinned remote secret", secretID)
		}
	}
	remaining := plan.Conflicts[:0]
	for _, conflict := range plan.Conflicts {
		if _, acknowledged := acknowledgedEntries[conflict.EntryID]; !acknowledged {
			remaining = append(remaining, conflict)
		}
	}
	plan.Conflicts = remaining
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].EntryID < plan.Actions[j].EntryID })
	return finalizeSyncPlan(plan)
}

func deletesRemote(action syncPlannedAction, operation syncOperation) bool {
	if action.Resolution == syncResolveDeleteRemote || action.Resolution == syncResolveLocal && !action.LocalPresent {
		return true
	}
	if action.Resolution == syncResolveDeleteLocal || action.Resolution == syncResolveRemote && !action.RemotePresent {
		return false
	}
	return operation == syncOperationPush || operation == syncOperationPrune || operation == syncOperationReconcile
}
