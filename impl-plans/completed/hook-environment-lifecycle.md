# Hook Environment Lifecycle Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-hook-enterleave-shell`
**Created**: 2026-08-10
**Last Updated**: 2026-08-10

## Design Document Reference

Implement a Bash lifecycle integration that lets directory managers evaluate
kinko-generated enter and leave code without parsing normal export output.
The command tracks exported names, removes them on leave, and keeps secret
values out of leave output. Native mise environment plugins, restoration of
pre-existing values, and nested lifecycle stacks are outside this patch.

## Modules

### 1. Hook Renderer

#### `internal/kinko/hook.go`

**Status**: COMPLETED

```go
type hookOptions struct {
    shell      string
    enter      bool
    exportOpts exportOptions
}

func runHookEnterWithOptions(opts globalOptions, hookOpts hookOptions, stdin io.Reader, stdout, stderr io.Writer) error
func runHookLeaveWithOptions(hookOpts hookOptions, stdout io.Writer) error
```

**Checklist**:
- [x] Render Bash tracking and export statements
- [x] Render Bash unset statements without vault access
- [x] Validate shell and inherited tracking names
- [x] Exclude the reserved tracking key

### 2. CLI Wiring

#### `internal/kinko/cobra_hook.go`, `internal/kinko/constants.go`

**Status**: COMPLETED

```go
func newHookCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Add `hook enter [shell]`
- [x] Add `hook leave [shell]`
- [x] Keep leave independent of vault preflight
- [x] Add focused help and usage text

### 3. Tests and Documentation

#### `internal/kinko/hook_test.go`, `internal/kinko/cobra_runtime_test.go`, `README.md`

**Status**: COMPLETED

**Checklist**:
- [x] Cover enter output and key tracking
- [x] Cover leave output and manipulated key rejection
- [x] Cover locked/missing state behavior
- [x] Document mise hook usage

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Hook renderer | `internal/kinko/hook.go` | COMPLETED | Passed |
| CLI wiring | `internal/kinko/cobra_hook.go` | COMPLETED | Passed |
| Verification/docs | `internal/kinko/hook_test.go`, `README.md` | COMPLETED | Passed |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| CLI wiring | Hook renderer | COMPLETED |
| Integration verification | Renderer and CLI wiring | COMPLETED |

## Completion Criteria

- [x] `kinko hook enter bash` emits evaluable exports and key tracking
- [x] `kinko hook leave bash` emits safe cleanup without vault access
- [x] Unsupported shells and manipulated tracking data fail safely
- [x] Focused and full Go tests pass
- [x] `go build ./...`, `go vet ./...`, and changed-code golangci-lint pass
- [x] README and command specification match behavior

## Progress Log

### Session: 2026-08-10
**Tasks Completed**: Design and implementation plan created
**Tasks In Progress**: Hook renderer and CLI wiring
**Blockers**: None
**Notes**: Bash-only lifecycle behavior matches the consuming mise templates.

### Session: 2026-08-10 (Completion)
**Tasks Completed**: Renderer, Cobra wiring, focused tests, documentation, and verification
**Tasks In Progress**: None
**Blockers**: None
**Notes**: Independent verification passed formatting, focused and full tests, build, and vet with Go 1.26.5. Golangci-lint reported zero issues for changes since HEAD.
