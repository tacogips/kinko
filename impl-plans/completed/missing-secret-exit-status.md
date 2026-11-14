# Missing Secret Exit Status Implementation Plan

**Status**: Completed
**Design Reference**: [get](../../design-docs/specs/command.md#kinko-get-key), [exec](../../design-docs/specs/command.md#kinko-exec----command)
**Created**: 2026-11-14
**Last Updated**: 2026-11-14

## Scope

Verify and document existing failure behavior for absent requested secrets.
Use `get KEY` and `exec --env KEY -- command`; command aliases are out of scope.
No public API changes or new types are required.

## Modules and Tasks

### TASK-001: Command contract documentation

**Status**: Completed
**Parallelizable**: No
**Deliverables**: `design-docs/specs/command.md`, `README.md`.

- [x] Document missing keys returning status 1 and preventing child execution.
- [x] Distinguish absent keys from registered empty values.

### TASK-002: Runtime regression coverage

**Status**: Completed
**Parallelizable**: No (depends on TASK-001)
**Deliverables**: `internal/kinko/runtime_missing_secret_test.go`.
Exercise `Run` and `ExitCode` through existing command wiring. Cover masked and
revealed gets, missing and mixed exec selections, inherited environment variables,
registered empty values, and successful shared fallback. Assert child execution
does not occur for any missing selection. Change production code only if a test
exposes an actual defect.

- [x] Regression cases pass with isolated synthetic vaults.

### TASK-003: Independent verification

**Status**: Completed
**Parallelizable**: No (depends on TASK-002)
**Deliverables**: Verification result and completed plan.

- [x] Required verification agent confirms tests, build, and vet pass.

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Contract | `design-docs/specs/command.md`, `README.md` | Completed | N/A |
| Regression | `internal/kinko/runtime_missing_secret_test.go` | Completed | Passed |
| Verification | `internal/kinko` | Completed | Passed |

## Dependencies

| Task | Depends On | Status |
|------|------------|--------|
| TASK-002 | TASK-001 | Complete |
| TASK-003 | TASK-002 | Complete |

## Completion Criteria

- [x] Missing gets return status 1 without stdout values.
- [x] Missing exec selections return status 1 without starting a child.
- [x] Existing empty values and scope fallback still work.
- [x] Tests, build, and vet pass.
- [x] Documentation and plan index updated; plan archived.

## Progress Log

### Session: 2026-11-14

Inspection found existing missing-key errors in both runtime handlers and status
1 mapping in `ExitCode`; production changes are unnecessary unless regression
coverage reveals a defect.

Final verification: all new runtime regressions passed independently; full `go test ./...` passed (internal/kinko: 103.091s), `go build -o /dev/null ./...` and `go vet ./...` passed. `go mod tidy` left module files unchanged. No production defect was found; tests and documentation now preserve the existing contract. TASK-001 through TASK-003 completed.
