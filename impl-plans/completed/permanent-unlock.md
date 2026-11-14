# Permanent Unlock Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-unlock`, `design-docs/specs/architecture.md#lockunlock-session-model`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

---

## Design Document Reference

**Sources**: `design-docs/specs/command.md`, `design-docs/specs/architecture.md`

### Summary

Add `kinko unlock --permanent` as an explicit non-expiring session mode while
retaining signature verification, keychain-backed DEK wrapping, manual lock,
and password-change invalidation.

### Scope

**Included**: CLI parsing in both runtime paths, mutual exclusion with an
explicit `--timeout`, signed permanent-session representation, status/unlock
output, doctor handling, unit and Cobra runtime tests, and user documentation.

**Excluded**: Changing the default `9h` timeout, environment/config timeout
sources, daemon-based unlock, or making permanent unlock the default.

---

## Modules

### 1. Session Representation and Verification

#### `internal/kinko/session.go`

**Status**: COMPLETE

```go
type sessionPayload struct {
    ExpiresAtUnix int64  `json:"expires_at_unix"`
    Permanent     bool   `json:"permanent,omitempty"`
    EncDEK        string `json:"enc_dek"`
}

func unlockSession(dataDir string, timeout time.Duration, secret string) error
func unlockSessionWithMode(dataDir string, timeout time.Duration, permanent bool, secret string) error
```

**Checklist**:
- [x] Persist an explicit signed permanent marker rather than a distant expiry.
- [x] Preserve bounded-session expiry behavior and reject invalid bounded timeouts.
- [x] Verify signed permanent sessions without applying an expiry check.
- [x] Keep legacy/malformed non-permanent zero-expiry payloads locked.
- [x] Cover bounded, permanent, tampered, and explicit-lock behavior in tests.

### 2. Unlock and Status Command Behavior

#### `internal/kinko/app.go`
#### `internal/kinko/cobra_runtime.go`

**Status**: COMPLETE

```go
type unlockOptions struct {
    timeout         time.Duration
    timeoutProvided bool
    permanent       bool
}

func unlockWithRetries(opts globalOptions, timeout time.Duration, stdin io.Reader, stderr io.Writer, maxAttempts int) error
func unlockWithRetriesMode(opts globalOptions, timeout time.Duration, permanent bool, stdin io.Reader, stderr io.Writer, maxAttempts int) error
```

**Checklist**:
- [x] Register the correctly spelled `--permanent` boolean flag in both parsers.
- [x] Reject `--permanent` combined with explicitly supplied `--timeout` as a policy error.
- [x] Reauthenticate when permanent mode refreshes an existing session.
- [x] Print `unlocked (permanent)` for permanent unlock and status output.
- [x] Preserve existing bounded output and no-op behavior.

### 3. Diagnostics and Contract Tests

#### `internal/kinko/doctor.go`
#### `internal/kinko/*_test.go`

**Status**: COMPLETE

```go
func diagnoseSessionToken(dataDir string, meta *vaultMeta) []string
```

**Checklist**:
- [x] Treat a valid signed permanent token as active during diagnostics.
- [x] Add legacy parser and Cobra coverage for successful permanent unlock.
- [x] Add mutual-exclusion, refresh, status, and manual-lock coverage.
- [x] Confirm no secret value is emitted by new output paths.

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Session representation | `internal/kinko/session.go` | COMPLETE | Passed |
| CLI behavior | `internal/kinko/app.go`, `internal/kinko/cobra_runtime.go` | COMPLETE | Passed |
| Diagnostics and tests | `internal/kinko/doctor.go`, `internal/kinko/*_test.go` | COMPLETE | 7 focused tests passed |
| Documentation | `README.md`, `design-docs/specs/*.md` | COMPLETE | N/A |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Permanent session payload | Existing signed session-token format | Available |
| CLI permanent mode | Permanent session payload | Complete |
| Diagnostics and verification | Payload and CLI behavior | Complete |

## Completion Criteria

- [x] `kinko unlock --permanent` creates a non-expiring signed session.
- [x] `--permanent` and explicit `--timeout` cannot be combined.
- [x] `kinko status` and repeated `kinko unlock` report permanent state clearly.
- [x] `kinko lock` and password change invalidate permanent sessions.
- [x] Existing bounded sessions remain compatible and auto-expire normally.
- [x] `go mod tidy` completes without changes beyond necessary module consistency.
- [x] `go build -o /dev/null ./...` passes.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] `git diff --check` passes.

## Progress Log

### Session: 2026-08-05

**Tasks Completed**: Design contract and implementation plan created.
**Tasks In Progress**: Go implementation and verification.
**Blockers**: None.
**Notes**: Permanent mode is explicit in the signed payload; a zero expiry alone
does not imply permanence.

### Session: 2026-08-05 (Completion)

**Tasks Completed**: Session representation, CLI integration, diagnostics,
focused regression coverage, documentation, and independent verification.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Independent uncached repository-wide tests, build, vet, formatting,
and diff checks all passed.

## Related Plans

- **Depends On**: `impl-plans/completed/crypto-session-hardening.md`
