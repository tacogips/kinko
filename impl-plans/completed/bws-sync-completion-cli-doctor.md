# BWS Sync CLI and Doctor Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-sync-with-bws`, `design-docs/specs/design-bws-sync.md#bws-configuration-diagnostics-and-version-gates`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: all completion implementation plans except verification
- **Next**: `bws-sync-completion-verification.md`

## Design Reference and Scope

Wire all completion flags/subcommands, perform all syntax validation before password/provider access, centralize locked snapshot orchestration, and add opt-in BWS doctor diagnostics while preserving existing no-flag doctor and legacy sync output.

## Modules and Types

### 1. Common CLI options and orchestration

#### `internal/kinko/cobra_runtime_sync.go`, `internal/kinko/sync_options.go`, `internal/kinko/sync_run_v2.go`

```go
type syncCommonOptions struct {
	Provider, ProjectID string
	Selector syncSelector
	PathMaps []syncPathMap
	ConflictPolicy syncConflictPolicy
	Resolutions map[string]syncResolution
	DeleteMode syncDeleteMode
	Transport bwsTransportMode
	AllowSecretArgv bool
	Retry syncRetryPolicy
	Resume syncResumeMode
	Progress syncProgressMode
	Yes, DryRun, JSON bool
}
type syncLockedSnapshot struct { Meta *vaultMeta; Vault *vaultData; Config map[string]string; State syncStateEnvelope; DEK []byte }

func validateSyncCommonOptions(syncCommonOptions, bool) error
func withLockedSyncSnapshot(globalOptions, io.Reader, io.Writer, func(*syncLockedSnapshot) error) error
func newSyncMaintenanceCommand(*runtimeContext, func() error) *cobra.Command
```

- [x] Wire selectors/exclusions/shared/maps/conflicts/delete/BWS config/transport/retry/resume/progress across applicable commands.
- [x] Bind the exact documented flags: `--select-*`, `--exclude-*`, `--shared`, `--map-path`, `--on-conflict`, `--resolve`, `--delete`, `--bws-config-file`, `--bws-profile`, `--bws-server-url`, `--bws-transport`, `--allow-secret-argv`, `--max-retries`, `--retry-max-delay`, `--resume`, and `--progress`.
- [x] Wire bootstrap/status/reset/reconcile/prune specific flags exactly as `command.md` specifies.
- [x] Keep root `--profile`/`--path` ignored; reject `--force` plus explicit conflict rules.
- [x] Validate flags/selectors before password/provider; then re-enter password, lock, reload all data, and hold through output/persistence.
- [x] Push/pull remain apply-by-default plus `--dry-run`; maintenance/bootstrap preview-by-default plus `--yes`.

### 2. Doctor BWS mode

#### `internal/kinko/doctor_bws.go`, `internal/kinko/cobra_runtime_sync.go`

```go
type doctorBWSOptions struct { Provider string; Online, CheckWrite, Yes, JSON bool }
type doctorBWSCheck struct { Name, Status, Detail, CleanupID string }
type doctorBWSResult struct { Checks []doctorBWSCheck }

func runDoctorBWS(globalOptions, doctorBWSOptions, io.Reader, io.Writer, io.Writer) error
func runBWSWriteCanary(context.Context, syncProvider, string) (doctorBWSCheck, error)
```

- [x] No new doctor flags preserves current local, non-interactive behavior/output byte-for-byte.
- [x] Provider mode checks binary/version/config/endpoints/transport/maps/state/checkpoint; encrypted checks require password.
- [x] Online distinguishes credentials, token, TLS/clock, assignment/project, read permission, and unknown write capability.
- [x] Only `--check-write --yes` mutates; record randomized canary id before read/delete and report value-free cleanup manifest on failure.

### 3. Backward-compatible output

#### `internal/kinko/sync_output_v2.go`

```go
type syncPlanOutput struct { Format int; Operation, SelectorDigest, PlanDigest string; Counts map[string]int; Actions []syncResultItem }
func printSyncPlanV2(io.Writer, *syncPlanV2, bool) error
```

- [x] Legacy stdout and JSON fields are unchanged; new fields are used only after an explicit completion flag selects the v2 path.
- [x] JSONL progress remains on stderr; no output contains values, tokens, raw provider bodies, or map roots.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| CLI-001 | Shared flag/options parsing | state/provider | No | Complete |
| CLI-002 | Subcommands/orchestration | bootstrap/maintenance/executor | No | Complete |
| CLI-003 | Doctor BWS mode | provider/state | Yes | Complete |
| CLI-004 | Compatible output | planning | Yes | Complete |

## Testing Requirements

- [x] Cobra help/flag matrix, invalid combinations, and pre-access validation ordering.
- [x] Password/lock/reload/hold-through-output tests for every command including preview/status.
- [x] Golden legacy sync/doctor text and JSON; additive new-mode output tests.
- [x] Doctor canary success and cleanup-manifest failure tests with no secret leakage.

## Completion Criteria

- [x] Entire documented CLI surface is wired with correct defaults and exits 10-16.
- [x] Existing behavior remains compatible when no completion flag/subcommand is used.
- [x] Tests/build/vet/race and line limits pass.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (CLI and maintenance integration)
**Tasks Completed**: Added the completion subcommands and documented flag matrix, pre-access validation, locked password-reentry orchestration, preview/apply maintenance output, opt-in BWS doctor mode, and guarded value-safe canary flow. Existing no-flag doctor and legacy push/pull paths remain unchanged.  
**Verification**: Focused Cobra, doctor, state-context, and metadata-upgrade tests pass; repository build, test, and vet pass.  
**Remaining**: Completion flags on push/pull are parsed and validated but are not yet dispatched to the v2 planner/executor. Doctor online failures still need the complete stable classification requested by the design, and golden output/cleanup-failure tests remain.

### Session: 2026-08-05 (push/pull dispatch and doctor completion)
**Tasks Completed**: Added exact legacy-versus-v2 dispatch based on whether a completion flag was explicitly supplied. The v2 path resolves pinned provider/project context, builds and resolves the v2 plan, applies deletion policy, emits value-free preview/progress, executes with durable encrypted checkpoints, and persists format-2 state and pull vault changes. Fixed nil action/conflict canonicalization so policy-normalized plans retain stable digests, and ensured newly created entries inherit the pinned endpoint/organization context. Added stable BWS doctor online categories for missing credentials, rejected/expired tokens, TLS/clock failures, unassigned projects, read denial, and untested write capability.  
**Verification**: Added exact dispatch, v2 dry-run/apply persistence, value-leakage, doctor taxonomy, cleanup, and legacy/new golden-output tests. `go build -o /dev/null ./...`, `go test ./... -count=1` (internal package 99.444s), `go vet ./...`, `git diff --check`, and source line-limit checks pass.  
**Remaining**: None in this plan; live authenticated BWS verification remains opt-in in the final verification plan.  
**Blockers**: None.
