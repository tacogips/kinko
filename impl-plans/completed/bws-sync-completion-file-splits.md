# BWS Sync Completion File Splits Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#providerpayload-and-testing-boundaries`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous**: `impl-plans/completed/bws-sync-command.md`
- **Next**: `bws-sync-completion-provider-config.md`, `bws-sync-completion-state-selection.md`
- **Depends On**: completed BWS v1 plans only

## Design Reference and Scope

Mechanically split the two files already over the 1,000-line repository limit before feature work. Preserve package-private behavior, test names, CLI help, and test fixtures exactly. No BWS completion behavior is added here.

## Modules and Signatures

### 1. Cobra command extraction

#### `internal/kinko/cobra_runtime_sync.go`, `internal/kinko/cobra_runtime.go`

```go
func newSyncCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
func newSyncDirectionCommand(ctx *runtimeContext, preflight func() error, direction syncDirection) *cobra.Command
func newDoctorCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

- [x] Move sync/doctor builders without semantic edits.
- [x] Keep every Go source file at or below 1,000 lines, targeting 700.

### 2. E2E fixture extraction

#### `internal/kinko/sync_e2e_helpers_test.go`, `internal/kinko/sync_e2e_test.go`

```go
type syncStubPaths struct {
	BinaryPath string
	StatePath  string
	JournalPath string
}

func buildStubBWS(t *testing.T, remotePayload ...string) syncStubPaths
func setupSyncE2EVault(t *testing.T) (dataDir, password string, dek []byte)
func setupEmptySyncE2EVault(t *testing.T, machineID string) (dataDir, password string, dek []byte)
```

- [x] Move helpers and fixtures without changing tests or stub protocol.
- [x] Keep every Go test file at or below 1,000 lines, targeting 700.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| SPLIT-001 | Cobra extraction | - | Yes | COMPLETED |
| SPLIT-002 | E2E helper extraction | - | Yes | COMPLETED |
| SPLIT-003 | Behavior and size verification | SPLIT-001, SPLIT-002 | No | COMPLETED |

## Testing Requirements

- [x] `go test ./internal/kinko` passes after the behavior-preserving split; named legacy tests remain present.
- [x] `go test ./...`, `go vet ./...`, and `go test -race ./internal/kinko -timeout 30m` pass.
- [x] `wc -l internal/kinko/*.go` reports no file over 1,000 lines.

## Completion Criteria

- [x] Both oversized files are split mechanically.
- [x] No observable legacy behavior, fixture protocol, or API changes.
- [x] Required Go coding and post-modification test agents are used.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (implementation)
**Tasks Completed**: SPLIT-001 and SPLIT-002; extracted command builders and E2E fixtures without intentional semantic changes.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (final verification)
**Tasks Completed**: SPLIT-003.  
**Verification**: Full tests, vet, the full race suite with a 30-minute bound, formatting, diff checks, and the automated 1,000-line gate passed.  
**Blockers**: None.
