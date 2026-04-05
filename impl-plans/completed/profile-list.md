# Profile List Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/command.md#kinko-profile
**Created**: 2026-04-05
**Last Updated**: 2026-04-05

---

## Design Document Reference

**Source**: design-docs/specs/command.md#kinko-profile

### Summary
Implemented `kinko profile list` so the CLI can enumerate stored profile names from the encrypted vault.

### Scope
**Included**:
- Added `profile` Cobra command with `list` subcommand
- Implemented runtime logic to load the unlocked vault and print sorted stored profile names
- Added regression tests for runtime behavior and Cobra wiring
- Updated command summary documentation

**Excluded**:
- `profile create`
- `profile delete`
- `profile rename`
- Path-management commands from the same design section

---

## Modules

### 1. CLI Command Wiring

#### internal/kinko/constants.go

**Status**: COMPLETED

```go
const (
    cmdProfile = "profile"
)

const (
    profileList = "list"
)
```

**Checklist**:
- [x] Add profile command constants
- [x] Keep subcommand names centralized with existing command constants

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newProfileCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Register `profile` under the runtime root command
- [x] Add `profile list` Cobra handler
- [x] Reuse existing preflight flow before vault access

### 2. Runtime Listing Behavior

#### internal/kinko/runtime.go

**Status**: COMPLETED

```go
func runProfile(opts globalOptions, args []string, stdout io.Writer) error
```

**Checklist**:
- [x] Validate supported subcommands
- [x] Load unlocked vault data
- [x] Print sorted stored profile names
- [x] Return clear errors for unsupported usage

### 3. Verification and Docs

#### internal/kinko/runtime_test.go

**Status**: COMPLETED

**Checklist**:
- [x] Cover sorted runtime output
- [x] Cover shared-only data not creating profiles

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

**Checklist**:
- [x] Cover `Run(... "profile" "list")`
- [x] Assert visible CLI output contains stored profiles

#### README.md

**Status**: COMPLETED

**Checklist**:
- [x] Add `kinko profile list` to the command summary

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Command constants | `internal/kinko/constants.go` | COMPLETED | Covered indirectly |
| Cobra wiring | `internal/kinko/cobra_runtime.go` | COMPLETED | Passing |
| Runtime implementation | `internal/kinko/runtime.go` | COMPLETED | Passing |
| Runtime tests | `internal/kinko/runtime_test.go` | COMPLETED | Passing |
| Cobra tests | `internal/kinko/cobra_runtime_test.go` | COMPLETED | Passing |
| Command summary docs | `README.md` | COMPLETED | N/A |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| `profile list` CLI | Existing vault loading and Cobra runtime | Satisfied |

## Completion Criteria

- [x] `kinko profile list` is available from the Cobra CLI
- [x] Output lists stored profile names in sorted order
- [x] Shared-only data does not create false profile entries
- [x] Targeted tests pass
- [x] `go test ./...` passes
- [x] `go vet ./...` passes

## Progress Log

### Session: 2026-04-05 10:49 JST
**Tasks Completed**: Plan created, CLI wiring implemented, runtime logic added, tests added, docs updated
**Tasks In Progress**: None
**Blockers**: None
**Notes**: `profile list` currently enumerates stored profile names present in vault data. Profile lifecycle commands remain out of scope.

### Session: 2026-04-05 11:20 JST
**Tasks Completed**: Review iteration fixed Cobra `set-key --value=` handling, extracted profile name sorting helper, added profile error-path coverage
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The design remains aligned with `design-docs/specs/command.md#kinko-profile`; no design update was required for this iteration because the issues found were implementation-quality regressions rather than scope changes.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: None
