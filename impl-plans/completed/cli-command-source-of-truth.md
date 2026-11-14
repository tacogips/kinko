# CLI Command Source Of Truth Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/command.md#cli-metadata-source-of-truth
**Created**: 2026-04-05
**Last Updated**: 2026-04-05

---

## Design Document Reference

**Source**: design-docs/specs/command.md#cli-metadata-source-of-truth

### Summary
Remove duplicated deprecated CLI/help structures so the Cobra runtime remains the single source of truth for command behavior and help output.

### Scope
**Included**:
- Remove dead manual help-command code that is no longer part of the `Run` path
- Remove obsolete global-option parsing code that duplicates Cobra normalization
- Add runtime-level help regression tests so new commands must appear in help output
- Keep existing command behavior unchanged for supported user flows

**Excluded**:
- New end-user commands
- CLI UX redesign beyond drift-prevention cleanup
- Reworking unrelated runtime command handlers

---

## Modules

### 1. Runtime Entry Cleanup

#### internal/kinko/app.go

**Status**: COMPLETED

```go
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error
func normalizePathInput(path string) string
```

**Checklist**:
- [x] Remove obsolete global-options parser helpers that are no longer used by runtime execution
- [x] Keep shared path normalization helpers used by Cobra finalization
- [x] Preserve current `Run` entry behavior

### 2. Canonical Help Surface

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newRuntimeRootCommand(ctx *runtimeContext) *cobra.Command
func newProfileCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
func defaultGlobalOptions() (globalOptions, error)
func finalizeGlobalOptions(opts *globalOptions) error
```

**Checklist**:
- [x] Keep Cobra as the sole command/help definition
- [x] Preserve `profile list` wiring and global option normalization

#### internal/kinko/help.go

**Status**: COMPLETED

```go
// File scheduled for removal because help is served by the Cobra runtime tree.
```

**Checklist**:
- [x] Remove dead duplicated help command tree
- [x] Remove unused helper functions that only supported the deleted path

### 3. Regression Coverage

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestRun_CobraHelpIncludesProfileCommand(t *testing.T)
func TestRun_CobraHelpForProfileShowsList(t *testing.T)
```

**Checklist**:
- [x] Assert root help exposes new runtime commands
- [x] Assert `profile --help` exposes `list`

#### internal/kinko/app_test.go

**Status**: COMPLETED

```go
func TestFinalizeGlobalOptions_PathCleanTrailingSlash(t *testing.T)
func TestFinalizeGlobalOptions_PathFromDirenvFormat(t *testing.T)
func TestFinalizeGlobalOptions_KeychainPreflightInvalid(t *testing.T)
```

**Checklist**:
- [x] Replace tests that target deleted dead helpers
- [x] Keep normalization and validation coverage for shared option finalization

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Runtime entry cleanup | `internal/kinko/app.go` | COMPLETED | Passing |
| Canonical Cobra runtime | `internal/kinko/cobra_runtime.go` | COMPLETED | Passing |
| Dead help removal | `internal/kinko/help.go` | COMPLETED | Covered by runtime help tests |
| Help regressions | `internal/kinko/cobra_runtime_test.go` | COMPLETED | Passing |
| Option finalization regressions | `internal/kinko/app_test.go` | COMPLETED | Passing |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Dead help removal | Cobra runtime already serving help | Available |
| Parser cleanup | Shared option finalization helpers | Available |
| Help regressions | Runtime command tree | Available |

## Completion Criteria

- [x] Duplicate manual help tree removed
- [x] Obsolete duplicated global-option parser removed
- [x] Root help and `profile --help` regression tests pass
- [x] `go test ./...` passes
- [x] `go vet ./...` passes

## Progress Log

### Session: 2026-04-05 12:05 JST
**Tasks Completed**: Identified architectural drift between the live Cobra runtime and dead duplicated help/parser code. Updated design to declare Cobra as the single source of truth.
**Tasks In Progress**: Removing dead code and replacing it with runtime/help regression coverage.
**Blockers**: None.
**Notes**: This cleanup is directly prompted by today’s `profile list` work, which exposed that new commands could be added to runtime execution without appearing in the stale duplicate help layer.

### Session: 2026-04-05 12:20 JST
**Tasks Completed**: Removed `internal/kinko/help.go`, deleted the dead parser path from `internal/kinko/app.go`, converted option normalization tests to the live finalization helpers, added help regressions for root and `profile --help`, and verified `go test ./...` plus `go vet ./...`.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: The runtime command tree is now the only maintained command/help definition, which prevents future drift like the one uncovered by `profile list`.

## Related Plans

- **Previous**: `impl-plans/completed/profile-list.md`
- **Next**: None
- **Depends On**: `design-docs/specs/command.md#cli-metadata-source-of-truth`
