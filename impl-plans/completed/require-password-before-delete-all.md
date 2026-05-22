# Require Password Before Delete All Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/architecture.md#kinko-delete---all`, `design-docs/specs/command.md#kinko-delete-key`, `design-docs/specs/design-shared-keys.md#delete-shared-key`
**Created**: 2026-05-22
**Last Updated**: 2026-05-22

---

## Design Document Reference

**Sources**:
- `design-docs/specs/command.md:193`
- `design-docs/specs/architecture.md:240`
- `design-docs/specs/design-shared-keys.md:83`
- `design-docs/specs/design-show-all-scopes.md:87`

### Summary

Require direct vault password verification for `kinko delete --all` and `kinko delete --shared --all` before vault loading, target-key listing, confirmation prompting, stdout output, or mutation. `--yes` skips only destructive confirmation, never password verification.

### Scope

**Included**:
- Current profile/path `delete --all` password verification.
- Shared scope `delete --shared --all` password verification.
- Stderr-only password prompts and verification errors.
- Empty stdout and unchanged vault data when verification fails or is canceled.
- Focused runtime tests and Cobra wiring tests.
- README and plan progress updates after implementation.

**Excluded**:
- Adding password verification to `delete <key>` or `delete --shared <key>`.
- Changing `show --all-scopes` behavior beyond regression protection.
- Changing vault format, password policy, or session unlock semantics.

### Codex Agent References

No external codex-agent references were provided. This plan uses the local repository root as the source of truth and intentionally preserves local `show --all-scopes` password-verification behavior as a reference pattern.

---

## Modules

### 1. Runtime Delete Authorization

#### `internal/kinko/runtime.go`

**Status**: COMPLETED

**Target signatures and touched functions**:
- `func runDelete(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error`
- Reuse or add a small helper shaped like `func verifyVaultPasswordForBulkDelete(opts globalOptions, stdin io.Reader, stderr io.Writer) (io.Reader, error)`
- Reuse `passwordVerificationInputFor`, `readSecret`, `readSecretWithPromptBuffered`, and `verifyVaultPasswordValue`

**Checklist**:
- [x] Parse and validate delete flags before password verification.
- [x] For `delete --all`, verify password before mutation lock, `loadUnlockedDEK`, `loadVault`, target-key listing, confirmation, stdout, or mutation.
- [x] For `delete --shared --all`, apply the same ordering.
- [x] Preserve the current single-key delete flow.
- [x] Preserve non-TTY buffered input so a valid password line does not consume later confirmation input.
- [x] Keep the prompt aligned with existing behavior: `Re-enter password: ` on stderr.

### 2. Cobra Delete Command Coverage

#### `internal/kinko/cobra_runtime.go`

**Status**: COMPLETED

**Target function**:
- `func newDeleteCommand(ctx *runtimeContext, preflight func() error) *cobra.Command`

**Checklist**:
- [x] Confirm existing Cobra flag-to-`runDelete` argument plumbing passes `--all`, `--shared`, and `--yes` consistently.
- [x] Make only minimal Cobra changes if runtime test coverage exposes an argument-ordering or stdin/stderr plumbing issue.
- [x] Preserve help text and positional-argument behavior.

### 3. Runtime Tests

#### `internal/kinko/set_test.go`

**Status**: COMPLETED

**Checklist**:
- [x] Add current-scope `delete --all --yes` wrong-password test that asserts returned error, stderr prompt, empty stdout, and unchanged data.
- [x] Add shared `delete --shared --all --yes` wrong-password test with the same assertions and no shared-key disclosure.
- [x] Add valid-password current-scope `delete --all` test with confirmation input after the password line.
- [x] Add valid-password shared `delete --shared --all` test with confirmation input after the password line.
- [x] Update existing target-key listing tests to include password input and verify listing still occurs after successful verification.
- [x] Preserve single-key delete tests without new password input.

### 4. Cobra Tests

#### `internal/kinko/cobra_runtime_test.go`

**Status**: COMPLETED

**Checklist**:
- [x] Add Cobra-level `delete --all --yes` wrong-password regression.
- [x] Add Cobra-level `delete --shared --all --yes` wrong-password regression.
- [x] Assert stderr contains `Re-enter password: ` and stdout is empty on auth failure.
- [x] Assert vault data remains readable after failed bulk delete.
- [x] Keep existing `show --all-scopes` Cobra tests passing.

### 5. User-Facing Documentation Refresh

#### `README.md`, `.agents/skills/kinko-secret-ops/SKILL.md`

**Status**: COMPLETED

**Checklist**:
- [x] Document that `delete --all` and `delete --shared --all` require vault password re-entry.
- [x] Document that `--yes` skips only confirmation.
- [x] Document that failed verification does not reveal target keys and leaves data unchanged.
- [x] Refresh user-facing kinko secret operation guidance for bulk-delete password verification.
- [x] Avoid changing `show --all-scopes` documentation except for necessary consistency fixes.

### 6. Implementation Plan Maintenance

#### `impl-plans/completed/require-password-before-delete-all.md`

**Status**: COMPLETED

**Checklist**:
- [x] Update task statuses during implementation.
- [x] Add a progress-log entry for each implementation session.
- [x] Mark completion criteria only after verification commands pass or failures are documented.
- [x] Move to `impl-plans/completed/` only after implementation, documentation, review, and commit workflow steps are done.

---

## Task Breakdown

| Task | Deliverables | Dependencies | Parallelizable | Verification |
|------|--------------|--------------|----------------|--------------|
| TASK-001 Runtime authorization | `internal/kinko/runtime.go` | Accepted design | No | Focused `TestRunDelete` runtime tests |
| TASK-002 Runtime tests | `internal/kinko/set_test.go` | TASK-001 behavior contract | No | `go test ./internal/kinko -run 'TestRunDelete'` |
| TASK-003 Cobra tests and minimal wiring | `internal/kinko/cobra_runtime.go`, `internal/kinko/cobra_runtime_test.go` | TASK-001 | No | `go test ./internal/kinko -run 'TestRun_.*Delete|TestRun_ShowAllScopesRequiresPasswordThroughCobra'` |
| TASK-004 README refresh | `README.md` | TASK-001 behavior settled | Yes, after TASK-001 contract is stable | README review plus full tests |
| TASK-005 Plan progress updates | This plan | Each completed task | No | Plan checkboxes and progress log updated |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Runtime password gate | Accepted design review | DONE |
| Runtime tests | Runtime password gate shape | DONE |
| Cobra tests | Runtime password gate and command wiring | DONE |
| README refresh | Final user-facing behavior | DONE |
| Completion archive | All implementation and review gates | BLOCKED |

## Parallelizable Tasks

- TASK-004 can run in parallel with test polishing after TASK-001 establishes final behavior wording.
- All Go-code tasks write overlapping runtime/test surfaces and should be executed serially.

## Verification Plan

Run these commands during implementation:

```bash
go test ./internal/kinko -run 'TestRunDelete'
go test ./internal/kinko -run 'TestRun_.*Delete|TestRun_ShowAllScopesRequiresPasswordThroughCobra'
go test ./internal/kinko
go test ./...
go vet ./...
task test
```

Required behavioral checks:
- Wrong password for current-scope `delete --all` leaves stdout empty and data unchanged.
- Wrong password for shared `delete --shared --all` leaves stdout empty and data unchanged.
- `--yes` never bypasses password verification.
- Empty-scope errors happen only after successful password verification.
- Valid password plus confirmation preserves target-key listing and delete success.
- Single-key delete behavior is unchanged.
- Existing `show --all-scopes` tests remain passing.

## Completion Criteria

- [x] `delete --all` verifies the vault password before vault loading, target listing, confirmation, stdout, or mutation.
- [x] `delete --shared --all` verifies the vault password with the same ordering.
- [x] Password prompts and verification failures are written to stderr.
- [x] Failed or canceled password verification leaves stdout empty.
- [x] Failed or canceled password verification leaves vault data unchanged.
- [x] `--yes` skips only confirmation.
- [x] Single-key delete behavior is preserved.
- [x] Focused runtime tests are added and passing.
- [x] Focused Cobra tests are added and passing.
- [x] Existing `show --all-scopes` tests remain passing.
- [x] README is updated.
- [x] `go test ./...`, `go vet ./...`, and `task test` pass or documented failures are reviewed.

## Progress Log

### Session: 2026-05-22 00:00
**Tasks Completed**: Implementation plan created.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Step 3 design review accepted the design and requested that authorization ordering remain explicit in this plan.

### Session: 2026-05-22 14:45
**Tasks Completed**: TASK-001, TASK-002, TASK-003, TASK-004, TASK-005 progress update.
**Tasks In Progress**: Completion archive remains pending later review and commit workflow steps.
**Blockers**: None for implementation. Plain `go test ./internal/kinko` and `go test ./...` initially exposed the existing macOS `/var` versus `/private/var` temp-path assertion in backup tests; rerunning with `TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/"` passed.
**Notes**: Bulk delete now reuses the buffered password verification flow before acquiring the mutation lock or loading the vault. Runtime and Cobra tests cover current and shared wrong-password failures, empty stdout, stderr prompt, no key listing, unchanged data, valid password plus confirmation input, and unchanged single-key delete behavior.

### Session: 2026-05-22 15:10
**Tasks Completed**: Step 8 user-facing documentation refresh.
**Tasks In Progress**: Completion archive remains pending later commit workflow steps.
**Blockers**: None.
**Notes**: Refreshed `README.md` wording and `.agents/skills/kinko-secret-ops/SKILL.md` operational guidance so both repository and skill surfaces document that current-scope and shared `delete --all` require password re-entry even with `--yes`, keep prompts/errors on stderr, leave stdout empty on verification failure, and preserve single-key delete behavior.

### Session: 2026-05-22 23:35
**Tasks Completed**: Archived completed plan under `impl-plans/completed/`.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Follow-up bookkeeping after the divedra workflow committed and pushed the implementation branch.

## Addressed Feedback

- Step 2 self-review low feedback: The plan explicitly aligns the bulk-delete password prompt with existing `Re-enter password: ` behavior.
- Step 3 design-review feedback: The plan explicitly requires password verification before vault loading, scope enumeration, confirmation, stdout output, or mutation.

## Risks

- Password verification inserted after `loadVault` would still disclose scope metadata.
- Buffered password input could consume confirmation input if helper selection is wrong.
- `--yes` could be accidentally treated as an authentication bypass instead of only a confirmation bypass.
- Shared and current-scope delete-all paths may diverge if tests do not cover both.
- Refactoring shared helpers could regress `show --all-scopes` password-gated behavior.

## Related Plans

- **Previous**: `impl-plans/completed/require-password-cross-scope-show.md`
- **Next**: None
- **Depends On**: Accepted Step 2 design updates in `design-docs/specs/command.md`, `design-docs/specs/architecture.md`, and `design-docs/specs/design-shared-keys.md`
