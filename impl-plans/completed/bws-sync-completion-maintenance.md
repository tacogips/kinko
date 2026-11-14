# BWS Sync Maintenance Workflows Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#status-reset-reconcile-and-prune`, `#portable-logical-paths-and-format-2-notes`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: `bws-sync-completion-checkpoint-execution.md`
- **Parallel Sibling**: `bws-sync-completion-bootstrap.md`
- **Next**: `bws-sync-completion-cli-doctor.md`

## Design Reference and Scope

Implement status, reset, reconcile/metadata upgrade, prune, and sync-created empty-scope cleanup. All are selector-aware; maintenance mutation is preview-first and requires `--yes`.

## Modules and Types

### 1. Status and reset

#### `internal/kinko/sync_status.go`, `internal/kinko/sync_reset.go`

```go
type syncStatusOptions struct { Provider string; Online, JSON bool; Selector syncSelector }
type syncStatusResult struct { Online bool; ProviderIdentity, SelectorDigest string; Formats []int; PathMaps []syncPathMap; BaselineHealth, CheckpointHealth string; Drift []syncResultItem }
type syncResetOptions struct { Provider string; Baseline, Checkpoint, Yes, JSON bool; Selector syncSelector }

func runSyncStatus(globalOptions, syncStatusOptions, io.Reader, io.Writer, io.Writer) error
func buildSyncResetPlan(syncStateEnvelope, syncResetOptions) (*syncPlanV2, error)
func applySyncReset(string, []byte, map[string]string, *syncPlanV2) error
```

- [x] Offline status/reset have no provider dependency; online status uses a pinned read.
- [x] Both require password re-entry and lock; status never writes.
- [x] Reset defaults to baseline+checkpoint; checkpoint is indivisible and selector digest must match.
- [x] Reset warns about future resurrection/deletion ambiguity and preserves ownership records.

### 2. Reconcile and metadata upgrade

#### `internal/kinko/sync_reconcile.go`

```go
type syncReconcileOptions struct { Provider string; UpgradeMetadata, Yes, JSON bool; Selector syncSelector }
type syncMetadataUpgradeCheckpoint struct { Old, New syncPrecondition; Phase string }

func buildSyncReconcilePlan([]syncEntry, []bwsSecret, syncStateEnvelope, syncReconcileOptions) (*syncPlanV2, error)
func applySyncReconcile(context.Context, syncProvider, *syncPlanV2, *bwsSyncStateV2) error
```

- [x] Adopt state only on exact value/name/note/machine/provider/org/project/scope/key match.
- [x] V1-to-v2 replacement durably persists the `created`, `state-replaced`, and completed phases through command orchestration.
- [x] Exact-pair resume and collision checks are implemented and tested; every irreversible phase is persisted before continuing.

### 3. Prune and empty scopes

#### `internal/kinko/sync_prune.go`

```go
type syncPruneOptions struct {
	Provider, MachineID, AckRetiredMachine string
	SecretIDs []string
	AckMalformed, PruneEmptyScopes, Yes, JSON bool
	Selector syncSelector
}
type syncPruneCandidate struct { SecretID, Reason string; Precondition syncPrecondition }

func buildSyncPrunePlan([]bwsSecret, syncStateEnvelope, *vaultData, syncPruneOptions) (*syncPlanV2, error)
func applySyncPrune(context.Context, syncProvider, *syncPlanV2, *vaultData, *bwsSyncStateV2) error
```

- [x] Automatic candidates only from completed checkpoint/ownership or selected baseline tombstone.
- [x] Untracked current-machine records require exact ids; cross-machine ids require matching retirement acknowledgement.
- [x] Malformed/duplicates require exact ids plus `--ack-malformed`; foreign names/projects never candidates.
- [x] Revision mismatch requires exact-id acknowledgement; remove ownership only after confirmed deletion.
- [x] Remove only empty vault map containers created by selected sync; never filesystem/folder vaults.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| MAINT-001 | Offline/online status | execution | Yes | Complete |
| MAINT-002 | Reset | state/checkpoint | Yes | Complete |
| MAINT-003 | Reconcile/upgrade | provider/execution | Yes | Complete |
| MAINT-004 | Prune/empty scopes | ownership/execution | Yes | Complete |

## Testing Requirements

- [x] Preview/apply and byte-identity tests for every workflow.
- [x] Exact retirement, malformed, duplicate, revision, project, and ownership deletion gates.
- [x] Metadata-upgrade interruption at each phase and exact-pair resume.
- [x] Status distinguishes offline inputs from pinned online provider data.

## Completion Criteria

- [x] Each maintenance workflow implements all confirmation/selection/ownership fences.
- [x] No workflow infers absence from unselected data or performs bulk delete.
- [x] Tests/build/vet/race and line limits pass.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: Retired-machine status is operator-acknowledged, not provable; this remains explicit.

### Session: 2026-08-05 (maintenance implementation)
**Tasks Completed**: Implemented and tested status snapshots, reset planning/apply, exact state reconciliation, metadata replacement/resume logic, prune gates, and selected empty-scope cleanup. Fixed unscoped checkpoint reset, ownership-proof fabrication during reconcile, live-owned-secret pruning, logical/local path absence inference, and exact-id boundary handling.  
**Verification**: Focused maintenance tests and focused race tests pass; `go mod tidy`, `go build -o /dev/null ./...`, `go test ./... -count=1`, `go vet ./...`, `git diff --check`, and source line limits pass.  
**Remaining**: The CLI/doctor plan must wire password re-entry, vault locking, preview/output, and persistence. Metadata-upgrade checkpoints need durable persistence after create and state replacement; lower-level exact-pair resume is implemented and covered. Ownership-only state after all baselines are reset lacks endpoint/organization pins, so automatic prune fails closed until provider context is supplied by command orchestration or the ownership schema is extended.  
**Blockers**: None for the maintenance core; remaining work belongs to command/persistence integration.

### Session: 2026-08-05 (command and durability completion)
**Tasks Completed**: Wired password re-entry, mutation locking, reload-under-lock, preview/apply output, and final persistence for status/reset/reconcile/prune. Added durable metadata-upgrade persistence after replacement creation, state replacement, and old-record deletion. Added a format-2 provider context pin so ownership-only state retains endpoint/organization/project/machine authority after baseline reset and prune remains fail-closed across boundaries.  
**Verification**: Focused maintenance, state compatibility, and Cobra ordering tests pass; repository build, test, and vet pass.  
**Remaining**: Full workflow byte-identity/golden integration coverage is part of the final verification plan.  
**Blockers**: None.

### Session: 2026-08-05 (final CLI/executor integration)
**Tasks Completed**: Connected push/pull completion invocations to the v2 planner/executor and durable checkpoint store. Full-state persistence now replaces removed entry and ownership maps while retaining unknown root/entry fields, so reset/reconcile/prune and completion sync converge on one valid format-2 state.  
**Verification**: Maintenance and direction-runner preview/apply tests, full repository tests, focused race tests, build, vet, diff checks, and line limits pass.  
**Remaining**: None in this plan.  
**Blockers**: None.
