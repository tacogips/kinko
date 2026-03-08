# Password Verifier Hardening Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/architecture.md#local-key-material-persistence-policy
**Created**: 2026-03-08
**Last Updated**: 2026-03-08

---

## Design Document Reference

**Source**: design-docs/specs/architecture.md

### Summary
Remove the cheap offline password verifier created by password-derived session key material in `meta.v1.json` while preserving CLI behavior and compatibility with existing vaults.

### Scope
**Included**: Session key generation hardening, compatibility-aware metadata loading, migration on successful authenticated operations, and tests.
**Excluded**: New CLI flags, format-wide signed metadata redesign, or unrelated vault format changes.

---

## Modules

### 1. Vault Metadata and Migration

#### internal/kinko/vault.go

**Status**: DONE

**Checklist**:
- [x] Add explicit metadata support for password-derived versus random session keys
- [x] Generate random session signing keypairs for new vaults
- [x] Preserve compatibility when loading legacy metadata
- [x] Add migration helper invoked after successful authentication
- [x] Unit tests for legacy and hardened metadata handling

---

### 2. Session and Password Change Flow

#### internal/kinko/session.go
#### internal/kinko/password_change.go

**Status**: DONE

**Checklist**:
- [x] Migrate legacy metadata after successful unlock without changing CLI usage
- [x] Preserve session token verification semantics across migrated vaults
- [x] Ensure password change writes hardened metadata
- [x] Unit tests for unlock and password change migration paths

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Vault metadata hardening | `internal/kinko/vault.go` | DONE | Passing |
| Session compatibility path | `internal/kinko/session.go` | DONE | Passing |
| Password change hardening | `internal/kinko/password_change.go` | DONE | Passing |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Password verifier hardening | Existing vault/session runtime | Available |

## Completion Criteria

- [x] All modules implemented
- [x] All tests passing
- [x] go build passes
- [x] go vet passes

## Progress Log

### Session: 2026-03-08 11:16
**Tasks Completed**: Created implementation plan and identified password-derived `session_pub_key_b64` as the compatibility-sensitive verifier leak.
**Tasks In Progress**: Implementing random session key generation plus legacy metadata migration on authenticated paths.
**Blockers**: None
**Notes**: Compatibility target is unchanged CLI usage with lazy migration for existing vaults.

### Session: 2026-03-08 12:03
**Tasks Completed**: Replaced password-derived session key generation for new writes, added unlock-time migration for legacy metadata, updated password rotation behavior, and added compatibility regression tests.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: Verification completed with `go test ./... -timeout 45s`, `go vet ./...`, and `task build`.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: None
