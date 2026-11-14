# Design: `kinko restore` (Backup Restore)

This document specifies the `kinko restore` command, which restores a vault
from an archive created by `kinko backup`. It completes the backup lifecycle:
today users can create password-locked backup archives but have no supported
CLI path to restore them.

## Overview

`kinko restore <archive>` reads a `kinko-backup-*.zip` archive produced by
`kinko backup`, decrypts its entries with the backup password, verifies
archive integrity and that the password actually opens the contained vault,
and writes the vault files into the target kinko data directory.

Design goals:

1. Restore must never silently overwrite an existing vault.
2. Restore must verify, before declaring success, that the restored vault is
   usable with the provided password (metadata parses, DEK unwraps, vault and
   config blobs decrypt).
3. Restore must be atomic from the user's perspective: on any failure, the
   target data directory is left without a partial vault.
4. Restore must be safe against malformed or hostile archives (path
   traversal, unexpected entry names, oversized/corrupt entries).

## Command Interface

```
kinko restore <archive> [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--current-stdin` | off | Read backup password from piped stdin |
| `--current-fd N` | off | Read backup password from file descriptor N |
| `--force-tty` | off | Allow interactive prompt with redirected stdin |
| `--include-bootstrap` | off | Also restore the archived bootstrap config to the `--config` path |

Global flags `--kinko-dir` and `--config` select the restore target locations
exactly as they select source locations for `backup`.

Positional argument rules:

- Exactly one archive path is required.
- The archive must be a regular file (not a symlink, not a directory).

### Examples

```bash
# Restore into the default data dir (must not already contain a vault)
kinko restore ./backups/kinko-backup-20260712T010203Z.zip

# Restore into a custom data dir, password from stdin
echo "$KINKO_PASSWORD" | kinko restore --current-stdin \
  --kinko-dir /tmp/kinko-restore kinko-backup-20260712T010203Z.zip

# Restore including the archived bootstrap config
kinko restore --include-bootstrap kinko-backup-20260712T010203Z.zip
```

## Password Semantics

The backup password is the vault master password at backup time. Restore uses
one password for two purposes:

1. ZipCrypto decryption of archive entries (weak access-control layer).
2. Post-restore verification: Argon2id KDF from restored `meta.v1.json`
   unwraps the DEK, and the DEK must decrypt `vault.v1.bin` and
   `config.v1.bin` (AES-256-GCM, the real cryptographic boundary).

Because ZipCrypto's per-entry check byte is weak (1 byte), a wrong password is
primarily detected by CRC mismatch after decryption and definitively by the
DEK unwrap verification step. A wrong password therefore fails with the
authentication exit code, never with a partially restored vault.

Password input follows the same modes as `backup`: interactive TTY prompt by
default, `--current-stdin`, `--current-fd`, `--force-tty`, mutually exclusive
stdin/fd modes, and `sanitizePasswordValue` normalization.

## Target-State Policy

Restore refuses to run when the target data dir already contains vault state:

- If `<kinko-dir>/vault/meta.v1.json`, `vault.v1.bin`, `config.v1.bin`, or
  the vault marker exists, restore fails with the policy exit code and a
  message directing the user to use a different `--kinko-dir` or run
  `kinko explosion` first.
- There is intentionally no `--overwrite` in v1. Overwrite-restore composes
  two destructive decisions; keeping `explosion` as the only vault-destroying
  command preserves the existing safety model.

`--include-bootstrap` similarly refuses to overwrite an existing file at the
`--config` path.

Restore acquires the mutation lock on the target data dir before writing, the
same lock used by all other mutations.

## Archive Validation

Read-side ZIP handling is a new, strict parser for exactly the format the
backup writer emits (stored compression, ZipCrypto flag, no ZIP64):

1. Locate and parse the end-of-central-directory record; reject archives with
   comments, multi-disk fields, or entry counts that disagree with the
   central directory.
2. For each central directory entry: require compression method `store`,
   the encryption flag set, and sizes consistent with the local header.
3. Entry-name allowlist (after `filepath.Clean`, forward-slash form):
   - `kinko-backup/manifest.json` (required, exactly once)
   - `kinko-backup/vault/meta.v1.json` (required)
   - `kinko-backup/vault/vault.v1.bin` (required)
   - `kinko-backup/vault/config.v1.bin` (required)
   - `kinko-backup/vault/.kinko-vault-marker` (required)
   - `kinko-backup/config/<basename>` (optional, at most one; bootstrap)
   - Any other entry name (absolute paths, `..`, unexpected files,
     duplicates) rejects the whole archive with a policy error.
4. Decrypt each entry, verify the ZipCrypto header check byte and the CRC32
   of the plaintext. Any mismatch aborts with an authentication error
   (wrong password) when the check byte fails on all entries, otherwise an
   integrity error.
5. Parse `manifest.json`: require `version == 1` and require the manifest
   file list to match the vault entries actually present in the archive.
   `bootstrap_present` must agree with the presence of a `config/` entry.

Entries map to fixed output paths derived from the allowlist; archive entry
names are never joined into the destination path directly, so path traversal
is structurally impossible.

## Restore Procedure

```
parse args -> read password -> open+validate archive (in memory)
  -> decrypt+verify all entries (CRC, manifest)
  -> verify vault usability against decrypted bytes:
       parse meta.v1.json -> unwrapDEKWithPassword -> decrypt vault/config blobs
  -> acquire mutation lock on target data dir
  -> re-check target-state policy under the lock
  -> ensure dir layout (0700 dirs)
  -> write vault files to <kinko-dir>/vault/ via staged temp names
  -> rename staged files into place (marker last)
  -> optionally write bootstrap config (0600, refuse overwrite)
  -> print summary; vault is restored in LOCKED state
```

Notes:

- Vault usability verification happens on the decrypted in-memory bytes
  BEFORE anything is written, so a wrong password or corrupt archive never
  touches the target directory.
- Files are staged as `<name>.restore-tmp` in the destination `vault/`
  directory and renamed into place only after all staged writes succeed; on
  any failure all staged and renamed restore outputs are removed.
- The vault marker is renamed into place last so an interrupted restore never
  leaves a directory that other kinko commands treat as an initialized vault.
- No session state is restored (backups exclude `lock/`); the restored vault
  starts locked and `kinko unlock` establishes a fresh session and keychain
  wrap key.
- Restored archives may contain a legacy `session_key_source`-less metadata;
  restore does not migrate it (the existing unlock-time migration handles it)
  but the summary prints the `kinko doctor` hint in that case.

## Exit Codes

Consistent with the existing `cliError` mapping:

| Condition | Exit code |
|-----------|-----------|
| Invalid arguments / mixed password modes | policy (11) |
| Target vault already exists / bootstrap overwrite | policy (11) |
| Malformed or disallowed archive structure | policy (11) |
| Wrong password (check byte/CRC/DEK unwrap auth failure) | auth (10) |
| Mutation lock conflict | lock conflict (12) |
| File read/write failures | io (13) |
| Restored metadata fails safety validation | metadata (14) |

## Security Considerations

- ZipCrypto remains an access-control convenience; confidentiality of vault
  contents is provided by the inner AES-256-GCM blobs (same position as the
  documented backup stance).
- The strict entry allowlist plus fixed output-path mapping removes archive
  path traversal as a class.
- Restore never prints secret values; the summary lists restored file names
  only.
- The archive is size-bounded by the same in-memory model as backup creation;
  entries above a sanity cap (64 MiB per entry) are rejected to bound memory.

## Testing Strategy

- Round-trip: `backup` then `restore` into a fresh dir; unlock and read
  secrets back.
- Wrong password: auth exit code, target dir untouched.
- Existing vault at target: policy exit code, target dir untouched.
- Tampered archive (flipped ciphertext byte): integrity failure, untouched.
- Hostile archives: traversal names, duplicate entries, unexpected entries,
  wrong compression method, missing required entries.
- Bootstrap: restored only with `--include-bootstrap`; overwrite refused.
- Interrupted-restore simulation: staged files cleaned up, no marker left.

## References

- `design-docs/specs/command.md` (command surface; restore added under
  implemented commands once shipped)
- `internal/kinko/backup.go` (archive writer this reader must match)
- See `design-docs/references/README.md` for external references.
