# Spec and Command Reconciliation

## Status
Completed

## Design Reference
- `design-docs/specs/design-review-findings-2026-07.md`
- Findings: F-08, F-09, F-10, F-11, F-12, F-13, F-14, F-15, F-16, F-17, F-27

## Objective
Align shipped CLI behavior, command documentation, and targeted runtime behavior for the P3 review group without introducing broad command redesign.

## Deliverables
- `internal/kinko/bootstrap.go`, `internal/kinko/cobra_runtime.go`
  - Resolve data dir from bootstrap `kinko_dir` when no CLI/env override is used.
- `internal/kinko/runtime_display.go`
  - Make `show --all-scopes` use the password-derived DEK from re-entry, so a locked session plus correct password works.
- `internal/kinko/runtime_io_commands.go`
  - Reject simultaneous `--file` and piped stdin for import.
- `README.md`, `design-docs/specs/command.md`, `design-docs/specs/architecture.md`
  - Align unlock timeout, command surface, flag tables, export examples, data model, and session architecture wording with shipped behavior.
- Tests for bootstrap data-dir resolution, show-all-scopes authorization, import input exclusivity, and command-surface drift guardrails.

## Subtasks

### TASK-001: Bootstrap Data-Dir Resolution
**Status**: Completed
**Parallelizable**: No
**Deliverables**: bootstrap/runtime option code and tests

**Completion Criteria**:
- [x] `--kinko-dir` has highest priority.
- [x] `KINKO_DATA_DIR` overrides bootstrap.
- [x] Bootstrap `kinko_dir` is used when no explicit data-dir source is present.
- [x] Default data dir remains the fallback.

### TASK-002: Show and Import Behavior Fixes
**Status**: Completed
**Parallelizable**: No
**Deliverables**: display/import code and tests

**Completion Criteria**:
- [x] `show --all-scopes` works from locked state after password re-entry.
- [x] `show --all-scopes --reveal` still applies sensitive-output guardrails.
- [x] `import --file` plus piped stdin fails clearly.
- [x] File-only and stdin-only import paths remain covered.

### TASK-003: Command-Surface and Spec Documentation
**Status**: Completed
**Parallelizable**: Yes
**Deliverables**: README and design docs

**Completion Criteria**:
- [x] Unlock timeout docs consistently use the shipped 9h default and source order.
- [x] Persistent flags are split from command-local flags.
- [x] Export eval/pipe examples include `--force --confirm=false`.
- [x] Shipped data model and session-token/keychain architecture are documented.
- [x] Unimplemented command ideas are marked as planned/future rather than current behavior.

### TASK-004: Drift Guard and Verification
**Status**: Completed
**Parallelizable**: No
**Deliverables**: tests and archived plan

**Completion Criteria**:
- [x] Runtime command-surface test covers documented shipped root commands.
- [x] Targeted tests pass.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] `git diff --check` passes.

## Progress Log

### Session: 2026-07-03
**Tasks Completed**: TASK-001 through TASK-004.
**Notes**: Remediated P3 findings F-08 through F-17 and F-27. Targeted tests, `go test ./...`, `go vet ./...`, and `git diff --check` passed.
