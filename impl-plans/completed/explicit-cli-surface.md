# Explicit CLI Surface Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/command.md#cli-metadata-source-of-truth
**Created**: 2026-04-05
**Last Updated**: 2026-04-05

---

## Design Document Reference

**Source**: design-docs/specs/command.md#cli-metadata-source-of-truth

### Summary
Keep the Cobra runtime as the single command metadata source while ensuring the exposed CLI surface stays limited to intentionally designed kinko commands and rejects unsupported command paths.

### Scope
**Included**:
- Disable Cobra's implicit default `completion` command
- Make help-only parent commands reject stray positional arguments and unsupported nested commands
- Add regression coverage for root help and command execution so implicit framework commands and silent fallback paths cannot reappear
- Update command design notes to require an explicit product command surface

**Excluded**:
- New end-user commands
- Shell completion feature design
- Changes to standard help behavior beyond explicit command-surface enforcement

---

## Modules

### 1. Explicit Root Command Surface

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newRuntimeRootCommand(ctx *runtimeContext) *cobra.Command
```

**Checklist**:
- [x] Disable the implicit Cobra completion command on the root command
- [x] Keep the existing help-driven runtime flow intact for zero-argument help entry
- [x] Reject unsupported positional arguments on help-only parent commands

### 2. Regression Coverage

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestRun_CobraHelpDoesNotExposeImplicitCompletion(t *testing.T)
func TestRun_CobraRejectsImplicitCompletionCommand(t *testing.T)
func TestRun_CobraRejectsUnknownRootSubcommand(t *testing.T)
func TestRun_CobraRejectsUnknownNestedSubcommand(t *testing.T)
```

**Checklist**:
- [x] Assert root help does not list `completion`
- [x] Assert `kinko completion` is rejected
- [x] Assert unsupported root and nested commands do not fall back to success/help

### 3. Design Alignment

#### design-docs/specs/command.md

**Status**: COMPLETED

**Checklist**:
- [x] Record that framework-injected commands must be explicitly opted into
- [x] Clarify that standard help behavior is allowed without expanding the product command surface
- [x] Require unsupported command paths to fail rather than silently printing help

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Explicit root command surface | `internal/kinko/cobra_runtime.go` | COMPLETED | Passing |
| Root help regressions | `internal/kinko/cobra_runtime_test.go` | COMPLETED | Passing |
| Command design alignment | `design-docs/specs/command.md` | COMPLETED | N/A |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Explicit command surface | Cobra runtime root command | Available |
| Regression coverage | Runtime help path | Available |

## Completion Criteria

- [x] Root help no longer exposes implicit `completion`
- [x] Runtime rejects `kinko completion`
- [x] Help-only parent commands reject unsupported command paths
- [x] Design explicitly documents the command-surface rule
- [x] `go test ./...` passes
- [x] `go vet ./...` passes

## Progress Log

### Session: 2026-04-05 13:10 JST
**Tasks Completed**: Reviewed today’s Cobra source-of-truth cleanup, found that the runtime still exposed Cobra’s implicit `completion` command and accepted unsupported subcommands as successful help requests, updated the design rule for an explicit command surface, disabled the implicit command, tightened help-only parents to reject stray args, added regression coverage, and re-verified the Go test and vet suite.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This is a design-alignment fix rather than a new feature; the main risk was shipping an undocumented framework command after removing the manual help layer.

## Related Plans

- **Previous**: `impl-plans/completed/cli-command-source-of-truth.md`
- **Next**: None
- **Depends On**: `design-docs/specs/command.md#cli-metadata-source-of-truth`
