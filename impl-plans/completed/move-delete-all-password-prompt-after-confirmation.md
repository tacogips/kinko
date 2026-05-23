# Move Delete-All Password Prompt After Confirmation Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/architecture.md#kinko-delete---all`, `design-docs/specs/command.md#kinko-delete-key`, `design-docs/specs/design-shared-keys.md#deletion`, `design-docs/specs/design-show-all-scopes.md#authorization`
**Created**: 2026-05-23
**Last Updated**: 2026-05-23

---

## Design Document Reference

**Sources**:
- `design-docs/specs/architecture.md:240`
- `design-docs/specs/command.md:194`
- `design-docs/specs/design-shared-keys.md:84`
- `design-docs/specs/design-show-all-scopes.md:87`

### Summary

Move interactive `kinko delete --all` and `kinko delete --shared --all` authorization so destructive confirmation happens before direct vault password verification. A declined confirmation must print the existing `aborted` stdout, must not prompt for the password, and must leave data unchanged. `--yes` skips confirmation only, so password verification remains required before target enumeration or mutation.

### Scope

**Included**:
- Current profile/path interactive `delete --all` confirmation-before-password ordering.
- Shared interactive `delete --shared --all` confirmation-before-password ordering.
- `--yes` password verification before loading, listing, deleting, or mutating.
- Buffered stdin handling for the new `y\npassword\n` ordering.
- Focused runtime and Cobra tests for confirm, decline, wrong password, `--yes`, single-key delete, and `show --all-scopes` preservation.
- README, kinko secret-operation skill docs, and implementation-plan history updates.

**Excluded**:
- Adding direct password verification to `delete <key>` or `delete --shared <key>`.
- Changing `show --all-scopes` authorization behavior.
- Changing vault format, password policy, session unlock semantics, or sensitive-output guardrails.

### Codex Agent References

No external codex-agent references were provided. This plan traces to `runtimeVariables.workflowInput.issueTitle` and the accepted Step 3 design review. The intentional divergence from `impl-plans/completed/require-password-before-delete-all.md` is limited to interactive bulk delete ordering; `--yes` retains the prior password-before-enumeration protection.

---

## Modules

### 1. Runtime Delete Authorization

#### `internal/kinko/runtime.go`

**Status**: COMPLETED

**Target functions and helper shapes**:
- `func runDelete(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error`
- `func verifyVaultPasswordForBulkDelete(opts globalOptions, input passwordVerificationInput, stderr io.Writer) error`
- Add or refactor a small helper that verifies from an already prepared `passwordVerificationInput` so the same buffered reader can be used first for confirmation and then for password input.

**Checklist**:
- [x] Keep flag parsing and invalid argument validation before any vault load or password prompt.
- [x] For interactive `delete --all`, acquire lock, verify session, load vault, resolve scope, list target keys, and ask confirmation before password verification.
- [x] For interactive `delete --shared --all`, apply the same ordering to the shared scope.
- [x] If confirmation is declined, write `aborted` to stdout, do not call password verification, do not mutate, and release the lock.
- [x] If confirmation is accepted, verify the vault password before deleting or saving.
- [x] For `--yes`, keep password verification before mutation lock acquisition, vault loading, key listing, deletion, or stdout output.
- [x] Preserve single-key delete flow and prompts.
- [x] Preserve `show --all-scopes` password verification helper behavior.
- [x] Preserve buffered stdin so tests can provide `y\npw\n` and `y\nwrong\n` without losing the password line.

### 2. Runtime Tests

#### `internal/kinko/set_test.go`

**Status**: COMPLETED

**Checklist**:
- [x] Add current-scope interactive decline test: stdout `aborted`, no `Re-enter password:`, target keys/prompt on stderr, data unchanged.
- [x] Add shared interactive decline test with the same assertions and shared data unchanged.
- [x] Update current-scope confirm test to use `y\npw\n` and assert confirmation text appears before `Re-enter password:`.
- [x] Update shared confirm test to use `y\npw\n` and assert confirmation text appears before `Re-enter password:`.
- [x] Add wrong-password-after-confirmation tests using `y\nwrong\n` for current and shared scopes: error returned, stdout empty, data unchanged.
- [x] Keep `--yes` wrong-password tests proving no key listing and no mutation before successful password verification.
- [x] Keep single-key delete tests without password input changes.

### 3. Cobra Runtime Coverage

#### `internal/kinko/cobra_runtime.go`, `internal/kinko/cobra_runtime_test.go`

**Status**: COMPLETED

**Checklist**:
- [x] Confirm Cobra flag plumbing for `--all`, `--shared`, and `--yes` still passes the intended args into `runDelete`.
- [x] Add or update Cobra current-scope decline coverage: aborted stdout, no password prompt, data unchanged.
- [x] Add or update Cobra shared decline coverage: aborted stdout, no password prompt, shared data unchanged.
- [x] Preserve Cobra `--yes` wrong-password coverage for current and shared scopes.
- [x] Keep `TestRun_ShowAllScopesRequiresPasswordThroughCobra` passing without behavior changes.
- [x] Make runtime-only changes unless Cobra tests expose a wiring issue.

### 4. User-Facing Documentation Refresh

#### `README.md`, `.agents/skills/kinko-secret-ops/SKILL.md`

**Status**: COMPLETED

**Checklist**:
- [x] Document that interactive `delete --all` and `delete --shared --all` confirm first, then ask for password only after confirmation.
- [x] Document that declined confirmation prints `aborted`, does not prompt for password, and leaves data unchanged.
- [x] Document that `--yes` skips confirmation only and still requires password verification before mutation.
- [x] Preserve single-key delete and `show --all-scopes` wording except for consistency references.

### 5. Implementation Plan Maintenance

#### `impl-plans/completed/move-delete-all-password-prompt-after-confirmation.md`, `impl-plans/completed/require-password-before-delete-all.md`, `impl-plans/README.md`

**Status**: COMPLETED

**Checklist**:
- [x] Update this plan's task status and progress log after each implementation session.
- [x] Add a supersession note or equivalent history update to the completed prior plan if it would otherwise read as the current contract.
- [x] Move this plan to `impl-plans/completed/` only after implementation, documentation refresh, review gates, and commit workflow steps complete.
- [x] Keep `impl-plans/README.md` active/completed tables synchronized.

---

## Task Breakdown

| Task | Deliverables | Dependencies | Parallelizable | Verification |
|------|--------------|--------------|----------------|--------------|
| TASK-001 Runtime ordering | `internal/kinko/runtime.go` | Accepted design | No | Focused runtime tests |
| TASK-002 Runtime tests | `internal/kinko/set_test.go` | TASK-001 behavior shape | No | `go test ./internal/kinko -run 'TestRunDelete'` |
| TASK-003 Cobra tests/wiring | `internal/kinko/cobra_runtime.go`, `internal/kinko/cobra_runtime_test.go` | TASK-001 | No | Cobra focused tests |
| TASK-004 Docs refresh | `README.md`, `.agents/skills/kinko-secret-ops/SKILL.md` | Stable behavior wording from TASK-001 | Yes, after TASK-001 contract is settled | Documentation review plus full tests |
| TASK-005 Plan maintenance | Implementation plan files and index | Each completed task | No | Plan checkboxes and status tables updated |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Runtime ordering | Step 3 accepted design | DONE |
| Runtime tests | Runtime ordering contract | DONE |
| Cobra tests | Runtime ordering and existing command wiring | DONE |
| Docs refresh | Final user-facing behavior | DONE |
| Completion archive | Implementation, tests, docs, and reviews | DONE |

## Parallelizable Tasks

- TASK-004 can run in parallel with test polishing only after TASK-001 establishes final externally visible wording.
- TASK-001, TASK-002, TASK-003, and TASK-005 should run serially because they write overlapping runtime, test, and progress-tracking surfaces.

## Verification Plan

Run these commands during implementation:

```bash
go test ./internal/kinko -run 'TestRunDelete'
go test ./internal/kinko -run 'TestRun_Delete.*All|TestRun_ShowAllScopesRequiresPasswordThroughCobra'
go test ./internal/kinko
go test ./...
go vet ./...
task test
```

Required behavioral checks:
- Current-scope declined interactive `delete --all` prints `aborted`, does not print `Re-enter password:`, and leaves data unchanged.
- Shared declined interactive `delete --shared --all` prints `aborted`, does not print `Re-enter password:`, and leaves shared data unchanged.
- Current and shared confirmed interactive delete-all prompt for password after confirmation and mutate only after successful verification.
- Wrong password after confirmation leaves stdout empty and data unchanged.
- `--yes` current and shared delete-all still require password verification before target enumeration or mutation.
- Single-key delete behavior is unchanged.
- Existing `show --all-scopes` tests remain passing.

## Completion Criteria

- [x] `delete --all` interactive flow asks destructive confirmation before password verification.
- [x] `delete --shared --all` interactive flow asks destructive confirmation before password verification.
- [x] Declined interactive confirmation preserves `aborted` stdout and avoids password prompting.
- [x] Confirmed interactive flow verifies password before mutation.
- [x] `--yes` still verifies password before loading, listing, deleting, or mutating.
- [x] Wrong password leaves stdout empty and data unchanged.
- [x] Single-key delete behavior is preserved.
- [x] `show --all-scopes` behavior is preserved.
- [x] Focused runtime tests are added or updated and passing.
- [x] Focused Cobra tests are added or updated and passing.
- [x] README and kinko secret-operation skill docs reflect the new ordering.
- [x] Implementation-plan history and `impl-plans/README.md` are synchronized.
- [x] `go test ./...`, `go vet ./...`, and `task test` pass or any environment-only failures are documented.

## Progress Log

### Session: 2026-05-23 07:51
**Tasks Completed**: Implementation plan created from accepted Step 3 design.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Step 3 accepted the design with no high or mid findings. Plan explicitly preserves `--yes`, single-key delete, and `show --all-scopes` behavior while changing only interactive bulk-delete ordering.

### Session: 2026-05-23 08:00
**Tasks Completed**: TASK-001 runtime ordering, TASK-002 runtime tests, TASK-003 Cobra tests, TASK-004 documentation refresh, TASK-005 progress/history updates.
**Tasks In Progress**: Completion archive remains pending later workflow review gates.
**Blockers**: None.
**Notes**: Interactive `delete --all` and `delete --shared --all` now confirm before password verification, declined confirmations skip password prompts, `--yes` remains password-first, and buffered stdin supports `y\npw\n` and `y\nwrong\n`. Focused tests pass. Plain full-test commands hit macOS `/var` vs `/private/var` temp path comparison failures in unrelated backup cwd tests; reruns with canonical `TMPDIR` passed.

### Session: 2026-05-23 08:32
**Tasks Completed**: Completed local verification and archived this plan under `impl-plans/completed/`.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: The divedra workflow passed implementation review and docs refresh, then failed during commit-message generation after five adapter attempts. The reviewed patch was verified locally with focused tests, `go vet ./...`, `go test ./...`, and `task test`; final commit/push was completed manually.

## Addressed Feedback

- Step 3 design review findings: none.
- Step 3 review decision: accepted design; this plan maps each accepted behavior into explicit runtime, test, documentation, and verification tasks.
- Step 2 self-review residual risk: plan calls out buffered stdin ordering for `y\npw\n` and `y\nwrong\n` so confirmation does not consume later password input.

## Risks

- Interactive delete-all now lists target key names before direct password verification; this is accepted by design but must remain limited to non-`--yes` flows.
- Using separate buffered readers for confirmation and password input can drop the password line in non-TTY tests and scripts.
- Moving password verification too late in the `--yes` path would weaken the prior no-enumeration-before-password guarantee.
- Shared and current-scope delete-all paths can drift if only one receives decline and wrong-password coverage.
- Refactoring shared password helpers can regress `show --all-scopes` password-gated output.

## Related Plans

- **Previous**: `impl-plans/completed/require-password-before-delete-all.md`
- **Next**: None
- **Depends On**: Accepted Step 3 design updates in `design-docs/specs/architecture.md`, `design-docs/specs/command.md`, `design-docs/specs/design-shared-keys.md`, and `design-docs/specs/design-show-all-scopes.md`
