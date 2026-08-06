# BWS Sync Planning and Policy Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#compatibility-and-state-format-rules`, `#granular-conflict-rules`, `#status-reset-reconcile-and-prune`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: `bws-sync-completion-provider-config.md`, `bws-sync-completion-state-selection.md`
- **Next**: `bws-sync-completion-checkpoint-execution.md`, `bws-sync-completion-bootstrap.md`

## Design Reference and Scope

Extend the value-free planner with pinned identities, complete preconditions, granular conflict rules, deletion policy, and capability checks while preserving legacy push/pull tables and direction-specific `--force` behavior.

## Modules and Types

### 1. Plan/action contract

#### `internal/kinko/sync_plan_v2.go`

```go
type syncOperation string
const (
	syncOperationPush syncOperation = "push"
	syncOperationPull syncOperation = "pull"
	syncOperationBootstrap syncOperation = "bootstrap"
	syncOperationReconcile syncOperation = "reconcile"
	syncOperationPrune syncOperation = "prune"
)
type syncPrecondition struct {
	ProviderIdentity, OrganizationID, ProjectID, MachineID string
	SecretID, Name, Revision, NoteSHA256, ValueSHA256 string
}
type syncPlannedAction struct {
	ActionID, EntryID string
	Kind syncActionKind
	Identity syncIdentity
	Precondition *syncPrecondition
	RequiredCapabilities []syncCapability
	Reason string
}
type syncPlanV2 struct {
	Format int
	Operation syncOperation
	ProviderIdentity, SelectorDigest, PathMapDigest, PlanDigest string
	Actions []syncPlannedAction
	Conflicts []syncConflict
}

func buildSyncPlanV2(syncOperation, []syncEntry, []bwsSecret, syncStateEnvelope, syncSelector) (*syncPlanV2, error)
func validatePinnedSyncPlan(*syncPlanV2, syncProvider) error
```

- [x] Plan all actions before mutation; emit only identities, hashes, reasons, revisions, and counts.
- [x] Re-get immutable ids before update/delete and verify project membership plus every precondition.
- [x] Reject malformed/duplicate machine-prefixed metadata before selector exclusion.

### 2. Conflict and deletion policy

#### `internal/kinko/sync_conflict.go`, `internal/kinko/sync_delete_policy.go`

```go
type syncConflictPolicy string
const (
	syncConflictFail syncConflictPolicy = "fail"
	syncConflictLocal syncConflictPolicy = "local"
	syncConflictRemote syncConflictPolicy = "remote"
	syncConflictSkip syncConflictPolicy = "skip"
)
type syncResolution string
const (
	syncResolveLocal syncResolution = "local"
	syncResolveRemote syncResolution = "remote"
	syncResolveDeleteLocal syncResolution = "delete-local"
	syncResolveDeleteRemote syncResolution = "delete-remote"
	syncResolveSkip syncResolution = "skip"
)
type syncConflict struct { EntryID, Reason string; LocalPresent, RemotePresent bool }
type syncDeleteMode string
const (
	syncDeleteAuto syncDeleteMode = "auto"
	syncDeleteKeep syncDeleteMode = "keep"
	syncDeleteConfirm syncDeleteMode = "confirm"
)

func applySyncConflictRules(*syncPlanV2, syncConflictPolicy, map[string]syncResolution, bool) error
func applySyncDeletionPolicy(*syncPlanV2, syncDeleteMode, bool) error
```

- [x] Reject duplicate/unmatched rules and deletion of an absent side.
- [x] `--force` conflicts with explicit rules and means local-wins push/remote-wins pull only.
- [x] Require baseline, revision-matched ownership proof, or exact prune acknowledgements for remote deletion; `confirm` deletion requires `--yes`.
- [x] No policy bypasses selector/provider/project/machine/scope/key/id/revision gates.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| PLAN-001 | V2 action/precondition model | provider, state | No | COMPLETED |
| PLAN-002 | Conflict policy | state selector IDs | Yes after state | COMPLETED |
| PLAN-003 | Deletion/ownership policy | state ownership | Yes after state | COMPLETED |
| PLAN-004 | Legacy compatibility matrix | 001-003 | No | COMPLETED |

## Testing Requirements

- [x] Preserve every v1 push/pull table row unchanged, including force directionality; focused compatibility coverage exercises legacy state in the V2 planner.
- [x] Table tests cover conflict rules, selector fences, capability failures, ownership proof, exact acknowledgement, and deletion modes.
- [x] Plan JSON contains stable full SHA-256 entry IDs and no values/tokens.
- [x] Simulated revision/project/membership changes abort before mutation.

## Completion Criteria

- [x] Legacy invocations produce legacy plans/state behavior.
- [x] New plans are complete, pinned, deterministic, value-free, and capability checked.
- [x] Tests/build/vet/race pass; source/test files remain below 1,000 lines.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (planning implementation)
**Tasks Completed**: PLAN-001 through PLAN-004 implementation and focused tests.  
**Tasks In Progress**: None.  
**Blockers**: None.  
**Notes**: Remote deletion now accepts only baseline, revision-matched ownership, or exact immutable-id acknowledgement proof and advertises only the delete capability. Focused tests cover selector fencing, ownership and boundary failures, exact acknowledgement atomicity, capability failure, and practical format-1 state compatibility.

### Session: 2026-08-05 (final verification)
**Tasks Completed**: Repository-wide build, tests, vet, full race, redaction, diff, and line-limit acceptance.  
**Blockers**: None.
