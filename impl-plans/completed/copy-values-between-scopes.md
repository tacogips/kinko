# Copy Values Between Scopes Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-copy-local-to-local---from-path-dir-key`, `design-docs/specs/command.md#kinko-copy-local-to-shared-key`, `design-docs/specs/command.md#kinko-copy-shared-to-local-key`
**Created**: 2026-06-16
**Last Updated**: 2026-06-16

## Design Document Reference

Add a copy-only vault maintenance command for local path scopes and local/shared scope transitions. Copy operations preserve source keys, write destination keys only after validating all requested keys, and reject destination conflicts unless `--overwrite` is explicitly set.

## Modules

### 1. CLI Command Surface

#### internal/kinko/constants.go

**Status**: COMPLETED

```go
const (
	cmdCopy = "copy"
)
```

**Checklist**:
- [x] Add `copy` command constant.
- [x] Reuse existing direction constants for local/shared transitions.

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newCopyCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Register `copy` under the root command.
- [x] Add `local-to-local --from-path DIR KEY|*`.
- [x] Add `local-to-shared KEY|*` and `shared-to-local KEY|*`.
- [x] Add `--overwrite` destination replacement flag.

### 2. Copy Runtime

#### internal/kinko/copy.go

**Status**: COMPLETED

```go
type copyDirection string

type copySecretOptions struct {
	Direction copyDirection
	Key       string
	FromPath  string
	Overwrite bool
}

type copySecretResult struct {
	Direction copyDirection
	Keys      []string
	Profile   string
	Source    string
	Destination string
}

func runCopy(opts globalOptions, args []string, stdout io.Writer) error
func parseCopyArgs(args []string) (copySecretOptions, error)
func copySecrets(vd *vaultData, opts globalOptions, copyOpts copySecretOptions) (copySecretResult, error)
```

**Checklist**:
- [x] Parse one key or `*`.
- [x] Normalize `--from-path` for local-to-local source lookup.
- [x] Validate destination conflicts before mutation.
- [x] Copy all selected values without deleting source keys.
- [x] Print only copied key names and scopes, never values.

### 3. Tests and Docs

#### internal/kinko/copy_test.go

**Status**: COMPLETED

```go
func TestCopyLocalToLocalKeySuccess(t *testing.T)
func TestCopyLocalToLocalAllSuccess(t *testing.T)
func TestCopyRequiresOverwriteForDestinationConflict(t *testing.T)
func TestCopyOverwriteReplacesDestination(t *testing.T)
func TestCopyLocalSharedDirections(t *testing.T)
func TestCopyRejectsInvalidArguments(t *testing.T)
```

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestCobraCopyCommands(t *testing.T)
func TestCobraCopyHelpIncludesDirections(t *testing.T)
```

#### README.md and design docs

**Status**: COMPLETED

**Checklist**:
- [x] Document copy command examples and overwrite behavior.
- [x] Update command/architecture docs for public CLI semantics.

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| CLI command surface | `internal/kinko/constants.go`, `internal/kinko/cobra_runtime.go` | COMPLETED | `internal/kinko/cobra_runtime_test.go` |
| Copy runtime | `internal/kinko/copy.go` | COMPLETED | `internal/kinko/copy_test.go` |
| Documentation | `README.md`, `design-docs/specs/*.md` | COMPLETED | Manual review |

## Dependencies

| Task | Depends On | Status |
|------|------------|--------|
| TASK-001 CLI command surface | User request | COMPLETED |
| TASK-002 Copy runtime | User request | COMPLETED |
| TASK-003 Tests and docs | TASK-001, TASK-002 | COMPLETED |

## Completion Criteria

- [x] Local-to-local copy supports one key and `*`.
- [x] Local-to-shared and shared-to-local copy support one key and `*`.
- [x] Destination conflicts fail by default and require `--overwrite`.
- [x] Source values are preserved by all copy directions.
- [x] Tests pass with `go test ./...`.
- [x] Plan is moved to `impl-plans/completed/`.

## Progress Log

### Session: 2026-06-16
**Tasks Completed**: Plan created.
**Tasks In Progress**: TASK-001, TASK-002.
**Blockers**: None.
**Notes**: Command shape chosen as `copy local-to-local --from-path DIR KEY|*`, `copy local-to-shared KEY|*`, and `copy shared-to-local KEY|*`.

### Session: 2026-06-16 Completion
**Tasks Completed**: TASK-001, TASK-002, TASK-003.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Implemented copy runtime, Cobra wiring, tests, README, and design docs. Verified with `go test ./...` and `go vet ./...`.
