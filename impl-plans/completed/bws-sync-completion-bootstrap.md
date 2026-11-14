# BWS Sync Bootstrap Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#cross-machine-bootstrap-and-disaster-recovery`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: `bws-sync-completion-state-selection.md`, `bws-sync-completion-planning.md`, `bws-sync-completion-checkpoint-execution.md`
- **Next**: `bws-sync-completion-maintenance.md`, `bws-sync-completion-cli-doctor.md`

## Design Reference and Scope

Implement preview-first cross-machine bootstrap that reads a pinned source namespace and atomically materializes selected values locally without BWS mutation, machine-id takeover, or normal baseline creation.

## Modules and Types

### 1. Bootstrap plan

#### `internal/kinko/sync_bootstrap.go`

```go
type syncBootstrapOptions struct {
	Provider, ProjectID, FromMachineID string
	Merge, Yes, JSON bool
	Selector syncSelector
	PathMaps []syncPathMap
	ConflictPolicy syncConflictPolicy
	Resolutions map[string]syncResolution
}
type syncBootstrapPlan struct {
	SourceMachineID, TargetMachineID string
	ProviderIdentity, SelectorDigest, PathMapDigest, PlanDigest string
	ReadSet []syncPrecondition
	Actions []syncPlannedAction
	Conflicts []syncConflict
}

func buildBootstrapPlan([]bwsSecret, *vaultData, syncBootstrapOptions, bwsRuntimeConfig) (*syncBootstrapPlan, error)
func applyBootstrapPlan(context.Context, syncProvider, *syncBootstrapPlan, *vaultData) error
func runSyncBootstrap(globalOptions, syncBootstrapOptions, io.Reader, io.Writer, io.Writer) error
```

- [x] Default target permits only reserved token, encrypted provider config, and empty containers; otherwise require `--merge`.
- [x] Equal unchanged, missing create, differing exact conflict resolution; empty effective selection is policy error.
- [x] Re-get every selected source id and validate endpoint/org/project/machine/id/revision/note/value/map before one vault save.
- [x] Never mutate BWS, source/current machine id, or normal baseline; provenance/checkpoint remains value-free and isolated.

### 2. Bootstrap output/provenance

#### `internal/kinko/sync_bootstrap_output.go`

```go
type syncBootstrapResult struct { Created, Unchanged, Conflicts int; Applied bool; PlanDigest string; Actions []syncResultItem }

func printSyncBootstrapResult(io.Writer, syncBootstrapResult, bool) error
```

- [x] Preview is default; `--yes` confirms only the pinned plan.
- [x] Report disaster-recovery guidance without calling a lost namespace orphan proof.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| BOOT-001 | Source validation/plan | state, planner | No | DONE |
| BOOT-002 | Atomic local apply | execution | No | DONE |
| BOOT-003 | Output/provenance | BOOT-001 | Yes | DONE |
| BOOT-004 | Orchestration tests | 001-003 | No | DONE |

## Testing Requirements

- [x] Preview/apply, empty-target, merge, every conflict resolution, selectors/maps, and source drift.
- [x] Byte-compare BWS and machine metadata; injected interruption leaves vault byte-identical.
- [x] Subsequent current-machine push creates new records and never edits/deletes source ids.
- [x] No omitted source entry becomes a deletion; no values appear in output/checkpoint.

## Completion Criteria

- [x] Bootstrap is pinned, atomic locally, identity-isolated, preview-first, and value-safe.
- [x] Restore workflows described by the design are covered by tests/documentation.
- [x] Focused tests/race, repository build/vet, formatting, and line-limit checks pass.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (Implementation)
**Tasks Completed**: BOOT-001 through BOOT-004. Implemented deterministic source/target pinning, exact merge conflict handling, atomic in-memory materialization, value-free provenance/output, and preview/apply orchestration through the hermetic read-only BWS stub. Added source/target drift, interruption, identity isolation, later-push, selector/path-map, and output-redaction coverage.  
**Verification**: Focused bootstrap tests and focused race tests passed; `go build -o /dev/null ./...`, `go vet ./...`, formatting, diff, and Go source line-limit checks passed.  
**Tasks In Progress**: None.  
**Blockers**: None.
