# Password Change Design

This document defines the design for changing the vault password in `kinko` without re-encrypting all vault data.

## Overview

`kinko` uses a stable random Data Encryption Key (DEK) to encrypt vault and config payloads.
The vault password is used only to derive a Key Encryption Key (KEK) that wraps the DEK.

Password change is therefore designed as:
- authenticate current password
- unwrap DEK with current KEK
- derive new KEK from new password
- re-wrap DEK and atomically persist updated password-wrap metadata

This keeps runtime short and avoids full vault re-encryption.

## Goals

- Change password without modifying encrypted secret payload format
- Preserve all existing secrets/config as-is
- Use atomic persistence to avoid vault lockout from partial writes
- Provide clear and scriptable CLI behavior with safe defaults
- Enforce strong password input and confirmation flows

## Non-Goals

- Password reset without current password (recovery flow)
- Multi-factor authentication in MVP
- External key escrow or remote recovery
- Rotation of the DEK itself during password change

## Command Interface

Primary command:

```bash
kinko password change
```

Optional flags:

- `--current-stdin`: read current password from stdin (non-interactive mode)
- `--new-stdin`: read new password from stdin
- `--current-fd <n>`: read current password from file descriptor `n` (preferred for automation)
- `--new-fd <n>`: read new password from file descriptor `n` (preferred for automation)
- `--force-tty`: allow interactive prompts only when stdin is redirected

Interactive behavior:
- Prompt for current password (no echo)
- Prompt for new password (no echo)
- Prompt for new password confirmation (no echo)
- Fail if confirmation mismatch

Non-interactive behavior:
- Must use exactly one input mode pair:
  - `--current-fd` + `--new-fd` (preferred)
  - `--current-stdin` + `--new-stdin` (compatibility mode)
- Refuse partial mode (only one of current/new, or mixed fd/stdin modes)
- FD mode reads each password from its designated descriptor and trims exactly one trailing line terminator:
  - if the input ends with `\r\n`, trim that pair
  - else if the input ends with `\n`, trim that byte
  - no other normalization is performed
- Stdin compatibility mode reads from stdin in fixed order: line 1 = current password, line 2 = new password
- Embedded newline characters inside a password are rejected
- Cross-platform rule: after the line-terminator handling above, password bytes are treated identically on Unix and Windows.
- Refuse if stdin is TTY and stdin flags are set (to avoid ambiguous UX)
- Password values must not be accepted from command-line arguments or environment variables

Exit outcomes:
- `0`: password changed successfully
- `10`: current password authentication failed
- `11`: new password policy validation failed
- `12`: lock conflict / concurrent mutation in progress
- `13`: persistence / I/O failure
- `14`: metadata/KDF parameters rejected by safety validation
- non-zero values not listed above are internal/unexpected errors

## Security and Validation Requirements

### Current Password Verification

Password change requires successful unwrap of the DEK with the current password-derived KEK.
If unwrap/authentication fails, the command exits and performs no writes.

### New Password Policy (MVP)

Minimum requirements:
- length >= 12 characters
- must differ from current password
- after trimming leading and trailing whitespace, value must not be empty
- leading/trailing whitespace characters are not allowed
- accepted password bytes are UTF-8 text excluding control characters (`0x00`-`0x1F`, `0x7F`)

Policy extensibility:
- policy is enforced in a validator layer so future entropy/rules can be added without changing storage format

### Argon2id Baseline (Normative)

Required floor for password-derived KEK:
- algorithm: Argon2id
- memory cost: >= 64 MiB
- time cost: >= 3 iterations
- parallelism: >= 1 (recommended default: 4 where available)
- output length: 32 bytes
- salt length: >= 16 bytes from OS CSPRNG

Upgrade rule on password change:
- implementation must compare stored `kdf_params_password` against the floor
- if stored parameters are below floor, the new wrap must be written with floor-or-better parameters
- implementation must never downgrade KDF parameters during password change

Safety cap validation (tamper/DoS resistance):
- implementation must enforce local safety caps when parsing `kdf_params_password`
- metadata values above safety caps must be rejected before any expensive KDF attempt
- default safety caps for MVP:
  - memory cost: <= 1 GiB
  - time cost: <= 10 iterations
  - parallelism: <= 16
- rejection due to out-of-cap parameters must use exit code `14` and failure category `metadata_invalid`

### Secret Handling Rules

- Password byte slices must be zeroized after use where practical
- No password material or derived keys in logs/errors
- Diagnostics must use redacted generic messages
- Clipboard integration is out of scope; CLI never copies passwords

## Data Model and Persistence

Password change updates only password-wrap-related metadata and ciphertext.

Affected logical fields:
- `wrapped_dek_by_password` (new ciphertext)
- `salt_password` (new random salt)
- `kdf_params_password` (possibly updated Argon2id parameters)
- `updated_at` metadata for auditability (non-secret)

Unchanged fields:
- vault encrypted payload
- encrypted config payload
- DEK value itself
- profile/path/key secret records

### Atomic Write Protocol

1. Load current metadata + wrapped DEK.
2. Verify current password by unwrap.
3. Derive new KEK and produce new wrapped DEK.
4. Write staged metadata file (`meta.v1.json.tmp`) with fsync.
5. Atomic rename tmp -> `meta.v1.json`.
6. fsync parent directory.

Security constraints for staged write:
- Create tmp with mode `0600` and verify owner is current user.
- Refuse to follow symlinks when opening or replacing metadata targets.
- Refuse replacement if target path is not a regular file.
- Ensure rename occurs within the same filesystem directory.

Failure handling:
- If any step fails before rename, original metadata remains authoritative.
- If failure occurs after rename but before final fsync, startup integrity checks treat renamed file as latest and verify readability.

## Concurrency and Lock State

Password change requires unlocked-capable operation but must not rely on an existing long-lived unlocked session token.

Rules:
- command acquires exclusive vault mutation lock
- concurrent `set/unset/config set` operations must block or fail fast
- if a shared unlock daemon is active, it must reload wrap metadata after successful change
- on successful password change, all active unlock sessions are invalidated immediately
- subsequent secret-read/mutate requests must fail until explicit `kinko unlock` with the new password

MVP behavior choice:
- after successful password change, force global lock across all processes and daemon clients
- user must run `kinko unlock` with the new password for subsequent access

## User Experience

Success message (single line, no secret data):
- `Password changed successfully. Vault is now locked.`

Failure message examples:
- `Current password is invalid.`
- `New password does not satisfy policy requirements.`
- `Failed to persist password update atomically. No changes were applied.`

Messages should be deterministic and safe for scripts.

## Compatibility and Migration

- No vault format version bump is required if field names/types remain identical.
- If Argon2id parameters are upgraded during change, store them with the wrap metadata used for future unlock.
- Existing metadata with weaker-than-floor Argon2id parameters is upgraded opportunistically at successful password change.
- Existing vaults without optional metadata timestamps can be upgraded in place by adding non-breaking fields.

## Observability

Allowed telemetry/events (non-secret):
- password change attempted
- password change succeeded/failed
- failure category (`auth_failed`, `policy_failed`, `io_failed`, `lock_conflict`)
- failure category `metadata_invalid` for rejected metadata/KDF params

Forbidden telemetry:
- passwords, password length, key material, raw salts

## Testing Strategy (Design-Level)

Required test categories for implementation phase:

- Happy path: valid current/new password updates wrap metadata
- Auth failure: wrong current password causes no write
- Policy failure: weak/matching new password causes no write
- Atomicity: simulated write interruption preserves recoverable state
- Concurrency: simultaneous mutate commands produce deterministic conflict handling
- Post-change behavior: old password fails, new password succeeds, vault data unchanged
- Revocation behavior: pre-existing unlocked sessions are rejected immediately after successful change

## References

See:
- `design-docs/specs/architecture.md` (key hierarchy and lock model)
- `design-docs/specs/command.md` (CLI contract)
- `design-docs/references/README.md` (Argon2 and AEAD references)
