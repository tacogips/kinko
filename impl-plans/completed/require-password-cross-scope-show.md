# Require Password for Cross-Scope Show Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/design-show-all-scopes.md#security-and-guardrails
**Created**: 2026-05-22
**Last Updated**: 2026-05-22

---

## Design Document Reference

**Source**: `design-docs/specs/design-show-all-scopes.md`

### Summary
Require direct vault password verification before `kinko show --all-scopes` writes any stdout or enumerates stored scopes. Preserve current-scope-only `kinko show` behavior and keep existing `--reveal` sensitive-output guardrails.

### Scope
**Included**: `show --all-scopes` authorization ordering, focused runtime/cobra tests for failed and successful password verification, regressions proving default `show` remains unchanged, and user-facing documentation refresh for the changed command behavior.
**Excluded**: storage schema changes, cross-profile aggregation, changing unlock/session semantics, and new public flags.

### Accepted Design Decisions
- `show --all-scopes` requires password verification for masked and `--reveal` output.
- Verification must use persisted vault metadata; an unlocked session alone is insufficient.
- Password prompts and verification errors stay on stderr.
- No stdout may be written before successful verification.
- `--reveal` guardrails still run after password verification.

---

## Modules and Tasks

### TASK-001: Shared Password Verification Helper

**Status**: Completed
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime.go`
**Dependencies**: None

Relevant functions:
- `verifyExplosionPassword(opts globalOptions, reader *bufio.Reader, stderr io.Writer) error`
- new or refactored helper for direct vault password verification against `loadMeta` and `unwrapDEKWithPassword`

**Checklist**:
- [x] Preserve `runExplosion` behavior and prompt/error text.
- [x] Extract reusable verification logic without adding a new public CLI surface.
- [x] Keep prompts/errors on stderr.
- [x] Return password-authentication failure without loading or rendering secret data.

### TASK-002: All-Scopes Authorization Ordering

**Status**: Completed
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime.go`
**Dependencies**: TASK-001

Relevant functions:
- `runShow(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error`
- `runShowAllScopes(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, reveal bool) error`
- `showAllSecretScopes(opts globalOptions) (map[string]string, map[string]map[string]string, error)`

**Checklist**:
- [x] Call password verification at the start of `runShowAllScopes`.
- [x] Verify before `showAllSecretScopes`, `normalizePathScopes`, any stdout writes, and the `--reveal` guard.
- [x] Preserve default `runShow` current-scope behavior.
- [x] Preserve existing all-scopes output formatting after successful verification.
- [x] Preserve existing `--reveal` sensitive-output guard behavior after successful verification.

### TASK-003: Runtime Tests for Authorization

**Status**: Completed
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime_test.go`
**Dependencies**: TASK-002

Target tests:
- Wrong/missing password for `show --all-scopes` returns an auth error and leaves stdout empty.
- Valid password for `show --all-scopes` emits the existing masked grouped output.
- Default `show` still does not require the all-scopes password prompt.
- `show --all-scopes --reveal` verifies password before applying the redirected-output guard.

**Checklist**:
- [x] Update existing all-scopes tests to provide valid password input.
- [x] Add focused failure coverage for no-output-on-auth-failure.
- [x] Add regression coverage that default `show` succeeds with current inputs.
- [x] Assert prompt/error behavior uses stderr where practical.

### TASK-004: Cobra-Level Regression

**Status**: Completed
**Parallelizable**: Yes, after TASK-002
**Deliverables**: `internal/kinko/cobra_runtime_test.go`
**Dependencies**: TASK-002

Target behavior:
- Cobra `show --all-scopes` path passes stdin/stdout/stderr through to runtime password verification.
- Failed verification through `Run(...)` leaks no stdout.

**Checklist**:
- [x] Add or update a focused cobra runtime test for `show --all-scopes`.
- [x] Keep command metadata and flag behavior unchanged.

### TASK-005: User-Facing Documentation Refresh

**Status**: Completed
**Parallelizable**: Yes, after TASK-002
**Deliverables**: `README.md`
**Dependencies**: TASK-002

Target behavior:
- Public command documentation reflects that `kinko show --all-scopes` requires vault password re-entry before any output.
- Documentation does not imply default current-scope `kinko show` requires this extra password verification.

**Checklist**:
- [x] Update README command behavior if `show --all-scopes` is documented there.
- [x] Keep design docs as source references; do not duplicate excessive design detail in user docs.

---

## Module Status

| Task | File Path | Status | Tests |
|------|-----------|--------|-------|
| TASK-001 password helper | `internal/kinko/runtime.go` | Completed | runtime/explosion regressions |
| TASK-002 authorization order | `internal/kinko/runtime.go` | Completed | runtime show tests |
| TASK-003 runtime tests | `internal/kinko/runtime_test.go` | Completed | focused `TestRunShow_AllScopes_*` |
| TASK-004 cobra regression | `internal/kinko/cobra_runtime_test.go` | Completed | focused `Run(...)` test |
| TASK-005 docs refresh | `README.md` | Completed | docs review |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Password helper | Existing vault metadata and `unwrapDEKWithPassword` | Available |
| All-scopes authorization order | TASK-001 | Complete |
| Runtime tests | TASK-002 | Complete |
| Cobra regression | TASK-002 | Complete |
| User-facing documentation refresh | TASK-002 | Complete |

## Parallelization

- TASK-004 and TASK-005 are parallelizable with TASK-003 only after TASK-002 lands because they write disjoint files.
- TASK-001 and TASK-002 are not parallelizable because both edit `internal/kinko/runtime.go` and must preserve authorization order.
- TASK-003 is not parallelizable with TASK-002 because existing all-scopes tests will need coordinated input updates.

## Verification Plan

Run after implementation:

```bash
go test ./internal/kinko -run 'TestRunShow_AllScopes|TestRunShow_DefaultViewGroupsSharedAndResolvedPathScopes|TestRunShow_IgnoresPositionalArgs'
go test ./internal/kinko -run 'TestRunExplosion|TestRunShow'
go test ./internal/kinko
go test ./...
go vet ./...
task test
```

Manual review checks:
- No stdout is written on `show --all-scopes` password failure.
- Password prompt/error text is on stderr.
- `show --all-scopes --reveal` still blocks redirected output unless forced, after password verification succeeds.
- Current-scope-only `show` does not consume password input.

## Completion Criteria

- [x] `show --all-scopes` requires direct vault password verification before scope enumeration and stdout output.
- [x] Failed/canceled verification returns an auth error and leaks no secret output or scope existence.
- [x] Successful verification preserves existing grouped masked output.
- [x] `--reveal` all-scopes output still uses existing sensitive-output guardrails.
- [x] Current-scope-only `show` behavior remains unchanged.
- [x] Focused runtime and cobra tests pass.
- [x] `go test ./...` passes with canonical `TMPDIR`; default environment path-symlink failure is recorded in the progress log.
- [x] `go vet ./...` passes.
- [x] `task test` passes with canonical `TMPDIR`; default environment path-symlink failure is recorded in the progress log.
- [x] User-facing documentation is refreshed or documented as already current.

## Progress Log

### Session: 2026-05-22 22:34 JST
**Tasks Completed**: Created implementation plan from accepted Step 3 design review.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Step 5 self-review should verify this plan stays below 400 lines and traces every accepted design requirement to implementation/test tasks.

### Session: 2026-05-22 22:43 JST
**Tasks Completed**: TASK-001, TASK-002, TASK-003, TASK-004, TASK-005.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Implemented shared direct vault password verification and required it before `show --all-scopes` reveal guard, scope loading, normalization, or stdout writes. Added focused runtime/cobra tests for wrong and missing passwords, no stdout leakage, successful output after verification, current-scope behavior, and reveal ordering. Updated README user-facing command notes. Focused tests, `go vet ./...`, and canonical-`TMPDIR` full test/task runs pass. Default `go test ./...` and `task test` fail in existing backup cwd assertions because macOS resolves `/var/...` temp paths to `/private/var/...`; no product failure was observed in the changed show path.

### Session: 2026-05-22 22:52 JST
**Tasks Completed**: Step 7 revision for password-reader selection and plan archival.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Updated `show --all-scopes` verification to use the existing TTY-aware hidden password reader for terminal stdin while preserving buffered stdin for non-TTY password input and later reveal confirmation prompts. Added focused helper tests for terminal vs buffered input selection. Moved this completed plan to `impl-plans/completed/`.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: None
