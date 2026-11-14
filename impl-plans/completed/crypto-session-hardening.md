# Crypto and Session Hardening

## Status
Completed

## Design Reference
- `design-docs/specs/design-review-findings-2026-07.md`
- Findings: F-06, F-18, F-19, F-20, F-23

## Objective
Remediate the P2 crypto and key-handling review slice while preserving compatibility with existing vault metadata, session tokens, and backup archives.

## Deliverables
- `internal/kinko/vault.go`
  - Add role/context-specific AEAD helpers.
  - Preserve old nil-AAD blob decryption.
  - Use contextual AAD for new vault, config, wrapped DEK, session private key, and session DEK blobs.
- `internal/kinko/session.go`
  - Use contextual AEAD for session DEK blobs.
  - Expose keychain wrap-key cleanup by metadata snapshot so password changes can remove the old account.
- `internal/kinko/password_change.go`
  - Delete the pre-change session wrap-key account before replacing session metadata.
  - Keep rollback behavior for session-token revocation failures.
- `internal/kinko/folder_backend_darwin.go`
  - Invoke `/usr/bin/hdiutil` directly.
- `internal/kinko/folder_backend.go`
  - Keep backend command environment minimal and deterministic.
- `README.md`, `design-docs/specs/command.md`
  - Document legacy session migration guidance and ZipCrypto limitations.
- Tests for contextual AEAD migration, keychain cleanup, and hdiutil invocation.

## Subtasks

### TASK-001: Contextual AEAD With Legacy Compatibility
**Status**: Completed
**Parallelizable**: No
**Deliverables**: `internal/kinko/vault.go`, `internal/kinko/vault_test.go`

**Completion Criteria**:
- [x] New blobs are sealed with role-specific AAD.
- [x] Legacy nil-AAD blobs still decrypt at runtime.
- [x] Context mismatch tests fail closed for contextual blobs.

### TASK-002: Session and Password-Change Keychain Cleanup
**Status**: Completed
**Parallelizable**: No
**Deliverables**: `internal/kinko/session.go`, `internal/kinko/password_change.go`, tests

**Completion Criteria**:
- [x] Password change removes the old wrap-key account, not the new account.
- [x] Revocation rollback behavior remains covered.
- [x] Session DEK blobs use contextual AEAD.

### TASK-003: Darwin Backend Command Hardening
**Status**: Completed
**Parallelizable**: Yes
**Deliverables**: `internal/kinko/folder_backend.go`, `internal/kinko/folder_backend_darwin.go`, Darwin tests

**Completion Criteria**:
- [x] Darwin backend invokes `/usr/bin/hdiutil` directly.
- [x] Command environment no longer depends on user-writable PATH entries.
- [x] Tests cover backend command path construction.

### TASK-004: Documentation and Review Status
**Status**: Completed
**Parallelizable**: Yes
**Deliverables**: `README.md`, `design-docs/specs/command.md`, `design-docs/specs/design-review-findings-2026-07.md`, `design-docs/specs/notes.md`

**Completion Criteria**:
- [x] README documents legacy session migration guidance.
- [x] Backup password documentation labels ZipCrypto as convenience, not strong encryption.
- [x] Review findings are updated with remediation notes.

### TASK-005: Verification and Archive
**Status**: Completed
**Parallelizable**: No
**Deliverables**: Verification output, archived plan

**Completion Criteria**:
- [x] Targeted tests pass.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] `git diff --check` passes.
- [x] Plan is moved to `impl-plans/completed/` and `impl-plans/README.md` is updated.

## Progress Log

### Session: 2026-07-03
**Tasks Completed**: TASK-001 through TASK-005.
**Notes**: Remediated P2 findings F-06, F-18, F-19, F-20, and F-23. Targeted tests, `go test ./...`, `go vet ./...`, and `git diff --check` passed.
