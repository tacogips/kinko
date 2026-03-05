# Architecture Design

This document describes system architecture and design decisions for `kinko`.

## Overview

`kinko` is a local encrypted secret store for environment variables.
The architecture prioritizes accidental leakage prevention (git/log/history/agent output) while acknowledging that full OS-compromise defense is out of scope for MVP.

---

## Goals

- Secure local persistence of secret values at rest
- Ergonomic CLI/TUI for profile + path scoped secret management
- Explicit lock/unlock state with configurable auto-lock timeout
- Safe-by-default UX: no plaintext value output unless intentional
- Reproducible behavior for shell workflows (`export`, `exec`)
- End-to-end encrypted-at-rest storage for both secrets and config

## Non-Goals (MVP)

- Defense against root-level compromise
- Defense against same-UID malicious process memory inspection
- Remote secret synchronization service
- Multi-user shared vault semantics

## Storage Layout

Default paths:
- Data directory: `~/.local/kinko`
- Config directory: `~/.config/kinko`

Proposed structure:

```text
~/.local/kinko/
  vault/
    vault.v1.bin            # encrypted payload (AEAD ciphertext)
    meta.v1.json            # non-secret metadata: format version, KDF params, salts
    config.v1.bin           # encrypted config payload
  lock/
    session.token           # signed/encrypted unlock session artifact (no raw DEK)

~/.config/kinko/
  bootstrap.toml            # minimal bootstrap only (non-secret): pointers, UX defaults
```

## Data Model

Logical key space:
- `profile` (string, default `default`)
- `path` (normalized absolute path, exact match only)
- `key` (environment variable name)
- `value` (secret)

Canonical record:

```text
secret_id = hash(profile + "\x00" + normalized_path + "\x00" + key)
```

Vault plaintext object (before encryption):
- `version`
- `profiles`: map profile -> path-map
- `paths`: map normalized_path -> key-values
- metadata per entry: `updated_at`, `updated_by`, `value_checksum` (optional)

Lookup policy:
- Exact path-only matching is required (no ancestor/wildcard inheritance).
- This reduces accidental over-exposure across directory boundaries.

Shared scope extension:
- Vault plaintext includes a vault-wide `shared` key map.
- Runtime resolution merges scopes in this order:
  1. `shared`
  2. `profiles[profile][path]`
- Repository-specific values override `shared` on key conflicts.

## Cryptography Design

### Recommended primitives (MVP)

- KDF: Argon2id
- AEAD: XChaCha20-Poly1305 (or AES-256-GCM as fallback)
- Randomness: OS CSPRNG

### Wallet-inspired key hierarchy (required)

`kinko` should follow a wallet-like separation between data key and user credentials:

1. Generate a random vault `DEK` (Data Encryption Key) once at `kinko init`.
2. Encrypt all vault secret payloads with `DEK` using AEAD.
3. Derive `KEK_password` from user password via Argon2id (`salt_password`).
5. Store `DEK` only as wrapped ciphertext:
   - `wrapped_dek_by_password = AEAD_Encrypt(KEK_password, DEK)`

Important:
- Password change should only re-wrap `DEK` with a new password-derived KEK.
- Password change flow and atomic persistence protocol are specified in
  `design-docs/specs/design-password-change.md`.

### Wrap model decision (MVP)

Chosen model: **Single password-wrap in MVP**

- `DEK` is random and stable for the vault data lifecycle.
- `wrapped_dek_by_password` is the only required wrap record in MVP.
- Recovery/escrow wraps are explicitly out of scope until a separate design specifies:
  - authority and trust boundaries,
  - key custody and rotation lifecycle,
  - disaster recovery and revocation behavior.

Implication:
- Password loss means vault loss in MVP.
- This tradeoff is accepted to avoid introducing an underspecified recovery trust model.

### Key handling

2. Argon2id derives the corresponding KEK from stored salt + KDF parameters.
3. KEK unwraps `DEK`.
4. `DEK` decrypts vault payload in memory.
5. Decrypted key material is kept only in process memory and erased on `lock`/timeout.

### Integrity

- AEAD authentication failure must hard-fail as corruption/tamper.
- Optional detached metadata signatures are deferred beyond MVP.

### Local key material persistence policy

What may be stored locally:
- encrypted vault payload
- encrypted wrapped `DEK` blobs
- non-secret salts and KDF parameters
- non-secret metadata (format version, timestamps)

What must never be stored locally in plaintext:
- raw `DEK`
- password
- decrypted vault snapshots

## Shared Unlock Model (cross-process)

Decision:
- Shared unlock is required.

Recommended implementation (preferred):
1. `kinko unlock` authenticates user and unwraps `DEK`.
2. A local daemon (`kinkod`) holds `DEK` in memory only.
3. CLI commands (`kinko export`, `kinko exec`, `kinko get/show`) call daemon via Unix domain socket.
4. Socket permissions (`0600`) and peer credential checks enforce same-user access.
5. Daemon enforces unlock TTL and zeroizes `DEK` at expiry/lock.

Alternative (file token) implementation:
- Store time-bounded session artifact in `~/.local/kinko/lock/session.token`.
- Artifact can be signed for integrity, but signature alone does not protect confidentiality.
- To be secure, token must still require local secret material not persisted in plaintext.

Conclusion:
- Signature verification of expiry is useful for tamper detection.
- Shared unlock security should rely on in-memory key custody (daemon) rather than reusable plaintext-equivalent tokens on disk.

## Lock/Unlock Session Model

States:
- `Locked`
- `Unlocked(expires_at)`

Rules:
- `unlock` sets in-memory active session and expiry timer
- Any secret-read operation checks lock state first
- Auto-lock occurs when now >= expires_at
- `lock` zeroizes in-memory key material immediately
- `status` reports remaining unlocked duration

Timeout:
- Default `15m`
- User-configurable via config/env/flag precedence

## Command Runtime Data Flow

### `kinko export <shell>`

1. Resolve `profile`, `path`
2. Verify unlocked session
3. Read matching key/value pairs from decrypted vault state
4. Emit shell-specific export statements using selected renderer
5. Do not write temporary plaintext files

### `kinko import [shell]`

1. Resolve `profile`, `path`
2. Resolve input source (`--file` or stdin pipe)
3. Normalize selected shell parser (`posix|fish|nu`)
4. Parse input into key/value map (line-aware parser)
5. Render confirmation summary (keys-only by default)
6. Confirm mutation (`--yes` or prompt)
   - when stdin is import payload, confirmation input is read via tty-aware confirmation primitive (`/dev/tty`)
7. Acquire mutation lock and verify unlocked session
8. Upsert keys in resolved scope and atomically persist vault

Import confidentiality constraints:
- Parse and validation errors must never include raw values.
- Confirmation output must hide values by default.
- Value display requires explicit opt-in (`--confirm-with-values`).
- Value-bearing confirmation output on `stderr` must follow sensitive-output guardrails (`--force` required for non-TTY redirection).

### `kinko exec -- cmd`

1. Resolve `profile`, `path`
2. Verify unlocked session
3. Build child env (`parent env + selected secrets`)
4. Start child process directly
5. No secret values printed to stdout/stderr

### `kinko tui`

1. Verify unlocked session for value-bearing actions
2. Allow search across profile/path/key metadata
3. Default to masked value rendering
4. Copy/reveal actions are explicit and audited in session log (metadata only)

## Config File Security Policy (`~/.config/kinko/bootstrap.toml`)
Storage policy:
- Primary config is encrypted at rest (`~/.local/kinko/vault/config.v1.bin`).
- `~/.config/kinko/bootstrap.toml` may exist only for non-secret bootstrap and UX defaults.

Allowed in bootstrap plaintext:
- data directory pointer
- UI defaults that do not impact secret confidentiality

Forbidden in bootstrap plaintext:
- secret values
- master passphrase
- raw encryption keys
- unlock session artifacts

## File Permission Requirements

- `~/.local/kinko` and `~/.config/kinko`: `0700`
- Vault/config files: `0600`
- Refuse operation (or require `--force`) when insecure permissions are detected.

## TUI Architecture (MVP)

Panels:
- Left: profile/path tree
- Center: key list + metadata
- Right: masked value/detail panel
- Top: lock state, timeout countdown, active filters

Search:
- fuzzy search over profile/path/key names
- optional metadata search
- value-content search is disabled by default to reduce plaintext exposure surface

## Future Extensions

- OS keychain backed KEK wrapping mode (Keychain, Secret Service, Windows DPAPI)
- Hardware-backed keys (TPM/Secure Enclave) where available
- Optional audit log (local, tamper-evident)
- Profile import/export with explicit re-encryption flow

---
