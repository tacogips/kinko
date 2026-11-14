package kinko

import (
	"errors"
	"fmt"
)

const (
	syncMaintenanceReasonResetBaseline = "reset selected baseline"
	syncMaintenanceReasonAdopt         = "adopt exact local and remote match"
	syncMaintenanceReasonUpgrade       = "replace format-1 metadata with format-2 metadata"
	syncMaintenanceReasonPrune         = "prune pinned remote secret"
)

type syncMaintenancePlan struct {
	Apply           bool `json:"-"`
	ResetBaseline   bool `json:"reset_baseline,omitempty"`
	ResetCheckpoint bool `json:"reset_checkpoint,omitempty"`
	// ResetCheckpointUnscoped records that the operator supplied no selector.
	// The design permits that explicit whole-checkpoint reset even when the
	// checkpoint was created with a non-default selector.
	ResetCheckpointUnscoped bool                 `json:"reset_checkpoint_unscoped,omitempty"`
	UpgradeMetadata         bool                 `json:"upgrade_metadata,omitempty"`
	EmptyScopes             []syncIdentity       `json:"empty_scopes,omitempty"`
	PruneCandidates         []syncPruneCandidate `json:"prune_candidates,omitempty"`
	Warnings                []string             `json:"warnings,omitempty"`
}

func cloneSyncMaintenancePlan(plan *syncMaintenancePlan) *syncMaintenancePlan {
	if plan == nil {
		return nil
	}
	clone := *plan
	clone.EmptyScopes = append([]syncIdentity(nil), plan.EmptyScopes...)
	clone.PruneCandidates = append([]syncPruneCandidate(nil), plan.PruneCandidates...)
	clone.Warnings = append([]string(nil), plan.Warnings...)
	return &clone
}

func requireMaintenanceApply(plan *syncPlanV2, operation syncOperation, yes bool) error {
	if plan == nil {
		return errors.New("sync maintenance plan is nil")
	}
	if plan.Operation != operation {
		return fmt.Errorf("expected %s maintenance plan, got %s", operation, plan.Operation)
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return fmt.Errorf("validate maintenance plan: %w", err)
	}
	if !yes || plan.Maintenance == nil || !plan.Maintenance.Apply {
		return errors.New("sync maintenance apply requires explicit yes confirmation")
	}
	return nil
}
