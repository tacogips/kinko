# Password Change Policy Alignment Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/design-password-change.md#new-password-policy-mvp
**Created**: 2026-03-08
**Last Updated**: 2026-03-08

---

## Design Document Reference

**Source**: design-docs/specs/design-password-change.md

### Summary
Align the password-change command with the intended MVP policy: accept any sanitized non-empty new password, but reject unchanged passwords with a deterministic policy error.

### Scope
**Included**: Password-change policy review, design-spec updates, regression tests for short-password acceptance and unchanged-password rejection, and CLI contract verification.
**Excluded**: New password-complexity requirements, vault format changes, KDF changes, and password-reset flows.

---

## Modules

### 1. Password Change Contract

#### internal/kinko/password_change.go

**Status**: COMPLETED

```go
func runPasswordChange(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Keep shared password sanitation as the only generic password validation
- [x] Reject unchanged passwords with a specific policy error
- [x] Preserve atomic metadata update and session revocation flow

#### internal/kinko/password_change_test.go

**Status**: COMPLETED

```go
func TestRunPasswordChange_AllowsShortNewPassword(t *testing.T)
func TestRunPasswordChange_RejectsSamePasswordWithSpecificMessage(t *testing.T)
func TestRunPasswordChange_RejectsWhitespaceOnlyPasswordChange(t *testing.T)
```

**Checklist**:
- [x] Cover short sanitized password acceptance
- [x] Cover unchanged password rejection
- [x] Cover whitespace-only changes that normalize back to the current password
- [x] Verify rejected changes leave the current password usable

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestRun_CobraRuntime(t *testing.T)
```

**Checklist**:
- [x] Cover the unchanged-password contract through the public CLI entrypoint

#### design-docs/specs/design-password-change.md

**Status**: COMPLETED

```go
// Documentation-only deliverable
```

**Checklist**:
- [x] Remove the stale minimum-length requirement
- [x] Describe the actual MVP sanitation and equality rules
- [x] Update testing expectations to match the intended policy

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Password change command | `internal/kinko/password_change.go` | COMPLETED | Covered |
| Password change unit tests | `internal/kinko/password_change_test.go` | COMPLETED | Passing |
| CLI contract regression | `internal/kinko/cobra_runtime_test.go` | COMPLETED | Passing |
| Design spec alignment | `design-docs/specs/design-password-change.md` | COMPLETED | N/A |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Password change policy alignment | Existing password-change implementation | Available |

## Completion Criteria

- [x] Password-change behavior reviewed against current architecture
- [x] Design documents updated to match implemented behavior
- [x] Regression tests cover intended policy behavior
- [x] go test passes

## Progress Log

### Session: 2026-03-08 00:00
**Tasks Completed**: Reviewed the in-flight diff, confirmed the architecture already treats passwords as sanitized opaque strings, updated the password-change design to remove the stale length rule, added CLI-level regression coverage, added a normalization regression test for whitespace-only changes, and verified tests.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The main issue was spec drift: `kinko init` and password-authenticated flows already accepted short sanitized passwords, so the removed length check brings `password change` back into alignment instead of weakening a consistently enforced policy.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: None
