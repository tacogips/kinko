# BWS Sync Retry, Checkpoint, and Execution Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#bounded-retry-progress-and-resume`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: `bws-sync-completion-provider-config.md`, `bws-sync-completion-state-selection.md`, `bws-sync-completion-planning.md`
- **Next**: `bws-sync-completion-bootstrap.md`, `bws-sync-completion-maintenance.md`

## Design Reference and Scope

Implement bounded read retries, encrypted value-free checkpoints, mutation reconciliation/resume, progress, and final selected state persistence. Preserve pull's single vault write and forbid blind mutation retries or bulk delete.

## Modules and Types

### 1. Retry policy

#### `internal/kinko/sync_retry.go`

```go
type syncRetryPolicy struct { MaxRetries int; InitialDelay, MaxDelay, TotalBudget time.Duration }
type syncRetryClassifier interface { Retryable(error) (bool, time.Duration) }
type syncClock interface { Now() time.Time; Sleep(context.Context, time.Duration) error }

func withSyncReadRetry[T any](context.Context, syncRetryPolicy, syncClock, syncRetryClassifier, func(context.Context) (T, error)) (T, error)
```

- [x] Defaults: 5, 500 ms, 30 s, 2 min; maxima: 10, 60 s, 5 min global delay.
- [x] Full jitter plus `Retry-After`; never retry auth, permission, validation, conflict, or non-idempotent mutation blindly.

### 2. Checkpoint/resume

#### `internal/kinko/sync_checkpoint.go`

```go
type syncCheckpointPhase string
type syncCheckpointResult struct { ActionID, SecretID, Revision string }
type syncCheckpoint struct {
	Format int
	Operation syncOperation
	ProviderIdentity, SelectorDigest, PlanDigest string
	Actions []syncPlannedAction
	Phase syncCheckpointPhase
	Confirmed []syncCheckpointResult
}
type syncResumeMode string
const (
	syncResumeAuto syncResumeMode = "auto"
	syncResumeRequire syncResumeMode = "require"
	syncResumeNever syncResumeMode = "never"
)

func validateSyncCheckpoint(*syncCheckpoint, *syncPlanV2) error
func persistSyncCheckpoint(string, []byte, map[string]string, *syncCheckpoint) error
func reconcileAmbiguousMutation(context.Context, syncProvider, syncPlannedAction) (syncCheckpointResult, bool, error)
```

- [x] Persist before first mutation and after each confirmed action; never store values/token.
- [x] Implement exact create/update/delete ambiguity rules and at most one conditional retry.
- [x] Resume only one digest-matching checkpoint after reloading vault values and remote preconditions.

### 3. Executor and progress

#### `internal/kinko/sync_executor_v2.go`, `internal/kinko/sync_progress.go`

```go
type syncProgressMode string
const (
	syncProgressAuto syncProgressMode = "auto"
	syncProgressPlain syncProgressMode = "plain"
	syncProgressNone syncProgressMode = "none"
	syncProgressJSONL syncProgressMode = "jsonl"
)
type syncProgressEvent struct { Operation, Phase, ActionID, EntryID, Status string }
type syncProgressSink interface { Emit(syncProgressEvent) error }

func executeSyncPlanV2(context.Context, syncProvider, *syncPlanV2, *vaultData, *bwsSyncStateV2, syncProgressSink) (syncResult, error)
```

- [x] Update/delete revalidation uses immutable id and exact project ownership; deletes are one-by-one.
- [x] Pull/local deletions are built in memory and persisted with one atomic vault write.
- [x] Merge selected state only; retain unselected raw records; progress is value-free stderr.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| EXEC-001 | Retry helper | provider | Yes | Completed |
| EXEC-002 | Checkpoint codec/validation | state, plan | No | Completed |
| EXEC-003 | Ambiguous mutation reconciliation | provider, plan | No | Completed |
| EXEC-004 | Executor/progress/persistence | 001-003 | No | Completed |

## Testing Requirements

- [x] Fake-clock retry/jitter/budget/`Retry-After` tests without wall-clock sleeps.
- [x] Crash-after-action and ambiguous create/update/delete resume tests.
- [x] Checkpoint decrypted fixtures and progress output contain no canary/token.
- [x] Byte-compare unselected state/vault data; verify executor uses only per-secret deletion.

## Completion Criteria

- [x] Resume is convergent and input changes fail closed.
- [x] Every mutation is preflighted/reconciled without claiming atomic provider semantics.
- [x] Focused tests and formatting pass; line limits hold. Full build/vet/race remain owned by the verification plan.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: Provider lacks atomic revision-conditional mutation; exact revalidation is the accepted residual control.

### Session: 2026-08-05 (execution implementation)
**Tasks Completed**: EXEC-001, EXEC-002, EXEC-003, EXEC-004.  
**Notes**: Added globally budgeted read retry, strict value-free checkpoint prefix validation and encrypted persistence, exact resume revalidation, one-retry ambiguity reconciliation, immediate mutation preflight, atomic in-memory pull application, value-free progress, and focused crash/persistence/security tests. No dependencies changed.  
**Blockers**: None for this plan. The provider's lack of atomic revision-conditional mutation remains documented and controlled by exact immediate revalidation.
