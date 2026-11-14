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
- `profiles`: map profile -> normalized path -> key -> value
- `shared`: vault-wide key -> value map

Per-entry IDs, timestamps, actor metadata, and value checksums are future schema
extensions rather than fields in the current vault payload.

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
5. Decrypted key material is not persisted in plaintext. In-process cleanup is
   best-effort memory hygiene within Go runtime limits and must not be
   documented as guaranteed erasure.

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

Current implementation:
1. `kinko unlock` authenticates user and unwraps `DEK`.
2. kinko creates or loads a random session wrap key in the OS keychain.
3. kinko writes `lock/session.token`, which contains an expiry timestamp and
   the `DEK` encrypted by that keychain-held wrap key.
4. The token payload is signed by random session key metadata stored in
   `meta.v1.json`.
5. Later commands verify the token signature and expiry, load the wrap key from
   the OS keychain, decrypt the session `DEK`, and continue.

Future daemon option:
- A local daemon (`kinkod`) could hold `DEK` in memory only and serve commands
  over a same-user Unix domain socket.
- That model remains future work, not current behavior.

Conclusion:
- Signature verification of expiry is useful for tamper detection.
- Session confidentiality relies on the token plus OS keychain wrap key split;
  the token alone is not plaintext-equivalent.

## Lock/Unlock Session Model

States:
- `Locked`
- `Unlocked(expires_at)`

Rules:
- `unlock` writes a signed session token with an expiry timer
- Any secret-read operation checks lock state first
- Auto-lock occurs when now >= expires_at
- `lock` removes `lock/session.token` and attempts best-effort session wrap-key
  cleanup
- `status` reports remaining unlocked duration

Timeout:
- Default `9h`
- User-configurable with `kinko unlock --timeout`; encrypted config and
  environment timeout fallback are not current behavior.

## Command Runtime Data Flow

### Folder Vault Architecture

Folder vaults extend kinko from key/value environment secret management into
project-scoped encrypted directories. A folder vault is registered by name
under the current path scope and appears in the project tree only while it is
unlocked.

Initial goals:
- `kinko folder add <name>` registers `<current-path>/<name>` as an encrypted
  folder vault and adds the plaintext folder path to `.gitignore`.
- `kinko folder unlock <name>` opens the existing encrypted backing store,
  mounts it at `<current-path>/<name>`, keeps kinko in the foreground as the
  mount owner, and soft-unmounts the folder when the command exits.
- `kinko folder lock <name>` performs a normal, non-force detach/unmount.
- `kinko folder path <name>` prints the plaintext mount path only when the
  folder is mounted.
- `kinko folder status [name]` reports configured folders and mount state.

Non-goals for the initial implementation:
- no long-lived daemon or LaunchAgent,
- no force unmount by default,
- no claim that same-UID processes are cryptographically prevented from reading
  an already mounted plaintext path,
- no cross-platform encrypted container format compatibility.

#### Storage Layout

Folder vault metadata is stored in the encrypted config payload, not in the
plaintext bootstrap config. The encrypted backing data lives under the kinko
data directory so repository trees contain only the transient plaintext mount
path.

```text
<kinko-data-dir>/
  folders/
    <folder-id>/
      meta.json              # non-secret operational metadata
      macos.sparsebundle/    # macOS encrypted disk image backend
      linux.cipher/          # reserved for a future Linux backend

<project>/
  <name>/                    # plaintext mountpoint, present only while unlocked
```

`folder-id` is a stable digest of profile, normalized path, and folder name.
Folder names are restricted to a single relative path element. Absolute paths,
`..`, empty names, leading `-`, control characters, path separators, and
existing non-directory files are rejected.

Current folder vault registrations are not relocatable. The encrypted
registration lookup and folder secret derivation both bind the folder to the
registered profile, normalized absolute path, name, and folder ID. Moving or
renaming the project directory requires a future reattach/move workflow rather
than transparent reuse of the old registration.

Encrypted config shape:

```go
type FolderRecord struct {
    Name       string
    Profile    string
    Path       string
    Backend    string
    FolderID   string
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

The mountpoint is intentionally not stored as trusted state; it is derived from
the current registered path and name. This avoids stale absolute path metadata
inside the encrypted config if a repository is moved.

Because folder registrations are encrypted with the rest of the config payload,
folder subcommands require an unlocked kinko session to read or mutate
registration records. If `kinko lock` is run while a `kinko folder unlock`
owner is still holding a mount, the owner process remains responsible for the
soft-unmount on exit.

#### Backend Selection

Default backend selection is OS-specific:
- macOS: `hdiutil` encrypted sparsebundle backend.
- Linux: unsupported for the current release; do not advertise or select
  `gocryptfs` yet.
- Other OSes: folder vault commands fail with an unsupported-platform error.

The current macOS sparsebundle capacity is fixed at `1g`. User-selectable
folder capacity, validation, and resize behavior are future folder-backend
work.

The backend interface isolates platform-specific command execution:

```go
type FolderBackend interface {
    Ensure(ctx context.Context, record FolderRecord, secret string) error
    Mount(ctx context.Context, record FolderRecord, secret string, mountpoint string) error
    Unmount(ctx context.Context, record FolderRecord, mountpoint string) error
    Status(ctx context.Context, record FolderRecord, mountpoint string) (FolderMountStatus, error)
}
```

All subprocess calls must use argument arrays, never shell interpolation. The
environment passed to backend commands is minimal and must not inherit arbitrary
secret-bearing environment variables.

macOS backend:
- creates an encrypted sparsebundle with `hdiutil create` when the backing
  image does not exist,
- attaches with `hdiutil attach -mountpoint <project>/<name>`,
- detaches with `hdiutil detach <mountpoint>`,
- reports busy detach failures without force by default.

Linux backend:
- remains gated behind unsupported-platform behavior for the current release,
- must not advertise `gocryptfs` as enabled in backend metadata,
- may use `gocryptfs` in a later phase once install, FUSE permission, lifecycle,
  and diagnostics requirements are ready.

#### Unlock And Lifecycle

Folder unlock requires the normal kinko vault session to be unlocked at mount
time. The folder backend passphrase is derived from the vault DEK plus folder
identity and is never printed or stored as plaintext. Once the folder is
mounted, a later `kinko lock` invalidates future secret reads and future mount
attempts, but it does not silently force-detach the active folder mount. The
foreground mount owner is the `kinko folder unlock` process, and it must
soft-unmount when that command exits.

Initial lifecycle behavior is intentionally conservative:
- `unlock` refuses to mount over an existing non-empty directory,
- `unlock` refuses to take ownership of an already-mounted folder,
- `unlock` stays in the foreground after a successful mount and unmounts on
  interrupt, terminate, or normal command exit,
- `kinko lock` while `unlock` is still running leaves the mount intact until
  the folder owner exits,
- `lock` attempts a normal soft unmount/detach,
- busy unmount errors surface a clear message and leave the mount intact to
  avoid corrupting active writes; users must close files and retry
  `kinko folder lock <name>`,
- no force-detach flag is included until the risk model is separately designed.

The daemon-free process lifecycle mode is the default unlock behavior:

```bash
kinko folder unlock private
```

This command mounts, waits in the foreground, then attempts a normal soft
unmount when interrupted, terminated, or otherwise exiting. It improves cleanup
ergonomics but is not a hard same-terminal security boundary without
sandboxing. A later `kinko run --folder` can extend the same lifecycle around a
child command.

#### Security Boundary

Folder vaults provide encryption at rest and reduce accidental repository
leakage. They do not by themselves prevent another same-user process from
reading the mounted plaintext directory if it can access the mount path. A
future `kinko run --sandbox --folder <name>` design is required before kinko can
make stronger process-tree visibility claims.

`.gitignore` integration is a leak-prevention guardrail, not a confidentiality
boundary. `folder add` appends the folder name to the project `.gitignore` when
it is not already ignored, must avoid duplicating entries, and must preserve the
permissions of an existing `.gitignore` file when appending. Backend storage
is not created until the project `.gitignore` has first been validated as a
local, snapshot-able file. Backend storage created during `folder add` remains
provisional until the encrypted registration is committed; if registration
fails, newly-created storage is removed so retries do not leave untracked
ciphertext state. If the `.gitignore` guardrail is written but encrypted
registration persistence then fails, kinko restores the previous `.gitignore`
state, including file mode, so the command does not leave a half-applied
project-tree mutation. Commented rules and negated rules are not treated as
existing ignore coverage; literal folder names that begin with `#` or `!` are
written with gitignore escaping. A project `.gitignore` that is a symlink is
rejected rather than followed because kinko cannot safely provide project-local
rollback semantics for a linked target.

### `kinko backup <directory>`

1. Resolve output directory.
2. Read the current password using interactive prompt, stdin, or fd-based input.
3. Acquire the vault mutation lock to freeze a mutation-stable snapshot.
4. Verify the password by unwrapping the persisted `DEK`; this command does not rely on unlocked session state.
5. Enumerate persisted files under the data directory and include all regular files needed to restore stored state.
   - Required baseline files include:
     - `vault/meta.v1.json`
     - `vault/vault.v1.bin`
     - `vault/config.v1.bin`
     - `vault/.kinko-vault-marker`
   - Additional regular files under the data directory are included so future persisted state is not silently omitted.
   - bootstrap config file is included when present.
6. Exclude transient state from backup payload:
   - `lock/session.token`
   - session wrap key material in OS keychain
   - `vault/.mutation.lock`
   - symlinks and other non-regular filesystem nodes are rejected rather than followed, including the optional bootstrap config file
7. Write a password-locked ZIP archive into the requested destination directory.
   - The archive format must be readable by standard ZIP tools that support traditional PKZIP password protection.
   - The ZIP password layer is for backup package access control and interoperability; confidentiality of secret values still primarily relies on the encrypted vault artifacts stored inside the archive.

Backup consistency and scope rules:
- Backup is a persistence operation, not a session export operation.
- A successful backup must reflect a mutation-stable snapshot, so it acquires the same mutation lock used by write operations after credential input is collected.
- The backup password is the current vault password at the time of backup creation.
- Backup archives may contain encrypted vault files plus non-secret bootstrap metadata, but must not preserve unlocked runtime state.
- The output destination must be outside the kinko data directory to avoid self-inclusion and accidental capture of prior backup artifacts.
- Destination containment checks must use symlink-resolved paths so a symlinked output directory cannot bypass the self-inclusion guard.

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

### `kinko delete --all`

Bulk delete is a destructive scope-wide mutation and requires direct vault password verification before mutation. Interactive bulk delete asks for destructive confirmation first, then asks for direct vault password verification only after the user confirms. `--yes` skips confirmation, so direct password verification remains the first authorization gate before scope enumeration or mutation.

Interactive authorization order for both current profile/path scope and `--shared` scope when `--yes` is absent:
1. Parse flags and reject invalid argument combinations without loading vault contents.
2. Acquire the mutation lock, verify the unlocked session, and load the vault.
3. Resolve the delete target scope:
   - current profile/path when `--shared` is not set
   - vault-wide shared map when `--shared` is set
4. List target key names on stderr and ask for destructive confirmation.
5. If the user declines, write the existing aborted stdout, do not ask for the vault password, and do not mutate.
6. After confirmation, read and verify the current vault password using stderr for prompts and errors.
   - Verification unwraps the persisted password-wrapped DEK metadata directly.
   - An already-unlocked session is not sufficient for this authorization step.
7. Delete the selected scope and atomically persist the vault.
8. Write the success message to stdout only after persistence succeeds.

Non-interactive authorization order when `--yes` is present:
1. Parse flags and reject invalid argument combinations without loading vault contents.
2. Read and verify the current vault password using stderr for prompts and errors.
3. After successful verification, acquire the mutation lock.
4. Verify unlocked session, load the vault, resolve the target scope, delete it, and persist.
5. Write the success message to stdout only after persistence succeeds.

Failure rules:
- Declined interactive confirmation must not prompt for the vault password, must preserve the existing aborted stdout, and must leave vault data unchanged.
- Failed or canceled password verification after interactive confirmation must leave stdout empty and vault data unchanged.
- Failed or canceled password verification with `--yes` stops before vault loading, key listing, or mutation.
- `--yes` bypasses only the confirmation step; it does not bypass or weaken password verification.
- Empty-scope errors occur only after the command reaches target-scope resolution.
- Single-key delete keeps the existing session-gated behavior and is not upgraded to direct password verification by this design.

### `kinko move local-to-shared <key>` / `kinko move shared-to-local <key>`

Scope movement is a single-key vault mutation that changes where an existing value is stored without changing the encrypted vault format.
It reuses the same `shared` map and `profiles[profile][path]` maps that already back `set`, `delete`, `show`, `export`, and `exec`.

Common data flow:
1. Parse the `move` direction, `key`, `--overwrite`, and `--yes`; reject extra positional keys before loading vault state.
2. Validate the key with the same environment key rules used by `set`, `set-key`, `get`, and `delete`.
3. Acquire the vault mutation lock.
4. Verify the existing unlocked session and load the decrypted vault.
5. Resolve source and destination maps from the selected direction:
   - `local-to-shared`: source is `profiles[profile][path]`, destination is `shared`.
   - `shared-to-local`: source is `shared`, destination is `profiles[profile][path]`.
6. Check that the source key exists. If not, fail without creating the destination scope or modifying either scope.
7. Check destination conflict. If the destination already contains the key and `--overwrite` is absent, fail without modifying either scope.
8. Ask for confirmation unless `--yes` is set. Confirmation names the key and source/destination scopes but never prints the value.
9. In the in-memory vault snapshot, write the destination value first, then delete the source key.
10. Atomically persist the encrypted vault once.
11. Write success output only after persistence succeeds.

Failure rules:
- A failed source lookup, destination conflict, declined confirmation, canceled prompt, lock conflict, session failure, or persistence failure leaves the previous encrypted vault state intact.
- `--overwrite` only permits replacing the destination key; it does not weaken source existence checks or confirmation behavior.
- `--yes` skips only the move confirmation prompt; it does not bypass key validation, session checks, mutation locking, source lookup, or destination conflict handling.
- No command output, prompt, log line, or error message may include the secret value.
- Moving a key can change runtime precedence. After `local-to-shared`, another local override may still shadow the shared value in other paths; after `shared-to-local`, other paths no longer receive the moved shared value.

### `kinko copy local-to-local --from-path <dir> <key|*>` / `kinko copy local-to-shared <key|*>` / `kinko copy shared-to-local <key|*>`

Scope copy is a non-destructive vault mutation that writes existing values into another scope without deleting source keys.
It reuses the same `shared` map and `profiles[profile][path]` maps as `set`, `delete`, `show`, `export`, `exec`, and `move`.

Common data flow:
1. Parse the `copy` direction, one key or `*`, `--overwrite`, and optional `--from-path`; reject extra positional keys before loading vault state.
2. Validate a concrete key with the same environment key rules used by `set`, `set-key`, `get`, and `delete`; allow `*` as the all-keys selector.
3. Normalize `--from-path` for `local-to-local`; reject it for local/shared directions.
4. Acquire the vault mutation lock.
5. Verify the existing unlocked session and load the decrypted vault.
6. Resolve source and destination maps from the selected direction:
   - `local-to-local`: source is `profiles[profile][fromPath]`, destination is `profiles[profile][path]`.
   - `local-to-shared`: source is `profiles[profile][path]`, destination is `shared`.
   - `shared-to-local`: source is `shared`, destination is `profiles[profile][path]`.
7. Select one source key or all sorted source keys. Missing concrete keys and empty wildcard source scopes fail without mutation.
8. Check destination conflicts for every selected key. If any selected destination key exists and `--overwrite` is absent, fail without modifying either scope.
9. Create the destination map only after source and conflict checks pass.
10. Write all selected destination values into the in-memory vault snapshot without deleting source keys.
11. Atomically persist the encrypted vault once.
12. Write success output only after persistence succeeds.

Failure rules:
- A failed source lookup, empty source, destination conflict, lock conflict, session failure, or persistence failure leaves the previous encrypted vault state intact.
- `--overwrite` only permits replacing destination keys; it does not weaken source existence checks.
- Wildcard copies are all-or-nothing with respect to destination conflict checks.
- No command output, prompt, log line, or error message may include secret values.

### `kinko path prune-missing`

Missing-path pruning is a vault maintenance operation over stored local profile/path scopes.
It removes encrypted path-scope entries only when the stored directory no longer exists locally, while preserving the vault format and all shared-scope data.

Preview data flow:
1. Parse command flags and reject invalid combinations before loading vault state.
2. Read and verify the current vault password before any stored profile/path metadata is written to stdout.
3. Load and decrypt the vault through the existing password verification path.
4. Select candidate profiles:
   - selected `--profile` by default
   - every stored profile when `--all-profiles` is set
5. Enumerate stored path scopes for those profiles. Do not enumerate or display shared scope as a candidate.
6. Normalize each stored path and inspect the filesystem.
7. Render stale path scopes and skipped diagnostics without printing secret values or key names.

Destructive data flow when `--yes` is set:
1. Read and verify the current vault password before candidate output or mutation.
2. Acquire the vault mutation lock.
3. Reload the mutation-stable vault snapshot.
4. Recompute prune candidates from the locked snapshot to avoid acting on stale preview state.
5. Delete only candidate path-scope maps from their owning profile.
6. Preserve empty profiles unless a separate profile-cleanup design explicitly permits removing them.
7. Atomically persist the vault in the existing encrypted vault format.
8. Report pruned profile/path scopes and totals only after persistence succeeds.

Filesystem classification:
- `stale`: normalized stored path does not exist as a directory.
- `kept`: normalized stored path exists as a directory.
- `skipped`: stored path is relative, cannot be normalized safely, collides with another stored path after normalization, points to an existing non-directory file, resolves through an unreadable path, or cannot be classified deterministically.

Failure rules:
- Failed or canceled password verification must write no stdout and leave vault data unchanged.
- Preview mode must not acquire destructive intent from prompts or defaults; it is non-mutating even when stale candidates exist.
- `--yes` is the only destructive confirmation flag for this command and must be present for deletion.
- Persistence failures must leave the previous encrypted vault state intact via the existing atomic write path.
- Missing-path pruning must not delete shared scope data, config payloads, unlock state, backup artifacts, profile definitions, or path scopes outside the selected profile set.

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
- Current write paths create directories and files with restrictive modes.
- Auditing and repairing permission drift on pre-existing files is future
  `doctor`/repair work rather than a shared preflight requirement in the
  current implementation.

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
