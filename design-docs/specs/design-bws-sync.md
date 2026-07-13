# Design: `kinko sync` with Bitwarden Secrets Manager (bws)

This document specifies remote synchronization of kinko secret values with
Bitwarden Secrets Manager (BWS) via the official `bws` CLI, plus the two
supporting vault-metadata features it requires: a per-vault kinko machine id
and deterministic short scope (directory) hashes.

## Overview

`kinko sync push --provider=bws` uploads every secret value in the vault
(all profiles, shared and path scopes) to Bitwarden Secrets Manager.
`kinko sync pull --provider=bws` applies this machine's remote entries back
into the local vault. Conflicts (the other side changed since the last sync)
fail with a dedicated exit code; `--force` overwrites the destination side.

Design goals:

1. Remote entries are machine-scoped: every BWS secret name is prefixed with
   the kinko machine id, so multiple machines can share one BWS project
   without colliding. Values are machine-specific, so cross-machine conflicts
   are not expected in normal operation.
2. Sync must never silently overwrite either side. Divergence is an error by
   default; `--force` is the only override, and the user owns conflict
   resolution.
3. The BWS access token used by kinko comes from the
   `KINKO_BWS_ACCESS_TOKEN` environment variable or from the kinko shared
   secret of the same name, and must never be confused with a
   `BWS_ACCESS_TOKEN` that the user may already have in the machine
   environment for their own bws usage.
4. `bws` is executed as an external command from Go (no Bitwarden SDK
   dependency); kinko degrades with a clear error when the binary is absent.
5. Existing vaults (created before this feature) gain the new metadata via an
   explicit `kinko migration` command; new vaults get it at `kinko init`.
6. Secret values never appear in kinko output, logs, error messages, or
   process argument lists.

Non-goals (out of scope for v1): folder-vault storage sync, encrypted config
payload sync, providers other than bws, cross-machine value adoption
(pulling another machine id's entries), and field-level merge of conflicting
values.

## Terminology

| Term | Meaning |
|------|---------|
| machine id | Random identifier generated per vault instance at `kinko init` (or backfilled by `kinko migration`); identifies this machine's vault in remote storage |
| scope hash | Deterministic 8-hex-char hash identifying one (profile, scope) inside the vault; "directory hash" for path scopes |
| sync entry | One (profile, scope, KEY) = value unit that maps to one BWS secret |
| provider | A remote sync backend; v1 ships exactly one: `bws` |
| sync state | Encrypted local record of what was pushed/pulled last, used for conflict detection |

## Machine ID

- Format: 16 lowercase hex characters (8 bytes from `crypto/rand`).
  Example: `a1b2c3d4e5f60718`.
- Storage: new optional field `machine_id` in `vault/meta.v1.json`
  (`vaultMeta`, `internal/kinko/vault.go`). It is non-secret metadata (it
  appears in BWS secret names) and lives beside the existing KDF/session
  metadata. Older kinko binaries that parse `meta.v1.json` ignore unknown
  JSON fields, so this is a backward-compatible addition; the meta format
  version does not change. Plaintext meta (rather than the encrypted config)
  is chosen deliberately: `kinko doctor` must be able to report a missing
  machine id without unlocking, and meta already has the atomic
  staged-rewrite path used by the session-key migration
  (`saveMetaAtomically`, `internal/kinko/password_change.go`).
- Creation: `kinko init` generates it for every new vault.
- Backfill: `kinko migration` assigns one to vaults that predate this
  feature (see below). `kinko doctor` warns when it is missing and points at
  `kinko migration`.
- Uniqueness boundary: the id is per vault instance (per `kinko-dir`). In the
  standard one-vault-per-machine setup this is exactly "the machine id"; a
  user running multiple kinko-dirs on one machine gets one id per vault,
  which is the correct collision boundary for remote storage anyway.
- The machine id is immutable after creation. No command rotates it in v1
  (rotation would orphan remote entries; explicitly future work).

## Scope Hash (Directory Hash)

Every sync entry's remote name needs a short, stable identifier for its
(profile, scope) pair.

- Algorithm: `hex(SHA-256(join("\x00", "kinko.scope.v1", profile, kind,
  path))[:4])` producing 8 lowercase hex chars, where `kind` is `path` or
  `shared`, `path` is the normalized absolute path (empty for shared
  scope), and `profile` is the profile name for path scopes and empty for
  shared scope (the vault's shared map is vault-wide, not per-profile:
  `vaultData.Shared` is a single map). This mirrors the existing `deriveFolderID` construction
  (`internal/kinko/folder_model.go`: version-prefixed, NUL-joined SHA-256),
  truncated for the fixed-width name grammar. Normalization is the
  stored-path normalization existing commands compose: `normalizePathInput`
  (`internal/kinko/app.go`) followed by `filepath.Abs` + `filepath.Clean`,
  as in `normalizeStoredScopePathForPrune`
  (`internal/kinko/path_prune_missing.go`). `normalizePathInput` alone is
  not trailing-slash-insensitive; the full composition is required or
  hashes become slash-sensitive.
- The profile is part of the hash input for path scopes because the BWS
  name format has only one slot between machine id and key name; two
  profiles containing the same directory must not collide remotely.
- Shared scope gets a hash from the same derivation (with `kind=shared`,
  empty profile and path), yielding one constant shared-scope hash per
  vault. The hash, not a literal `shared` marker, keeps the name grammar
  uniform: fixed-width fields only.
- Scope hashes are derived, never persisted; there is nothing to migrate for
  them.
- Collision policy: before any remote mutation, sync computes the hashes of
  all local scopes; if two distinct scopes produce the same hash, sync fails
  with a policy error naming both scopes. With 32-bit hashes and per-vault
  scope counts this is vanishingly rare; failing hard is safer than silent
  cross-scope mixing. Widening the hash would be a format-version bump.

## Remote Data Model (BWS)

### Secret name format

```
{machine_id}_{scope_hash}_{KEY_NAME}
```

Example: `a1b2c3d4e5f60718_5f2a8c1d_GITHUB_TOKEN`.

Both prefix fields are fixed width (16 hex + `_` + 8 hex + `_`), so parsing
is unambiguous even though `KEY_NAME` itself may contain underscores.
A remote secret "belongs to this machine" iff its name starts with
`{machine_id}_` and the remainder parses as `{8 hex}_{KEY_NAME}`.

The same KEY in different scopes never collides remotely: `GITHUB_TOKEN`
in the shared scope and in `/work/project-a` of the `default` profile
produce two names differing in the scope-hash field.

Requirement note: the requirement phrase "store under the id
`{machine_id}_{directory_hash}_{KEY_NAME}`" is realized as the BWS secret
*name* (the `key` field); the BWS `id` field is a server-assigned UUID and
cannot be chosen by clients.

Key-name compatibility: kinko keys are environment-variable names
(`[A-Za-z_][A-Za-z0-9_]*`), a strict subset of what BWS secret names
accept, and the 25-character prefix leaves ample room under BWS name-length
limits for realistic env var names. If bws nevertheless rejects a name
(e.g. a pathological key length), the failure is a provider error naming
the key.

### Secret value

The kinko value in plaintext (from BWS's perspective; Bitwarden encrypts
server-side). Multiline values are passed through unchanged. Empty-string
values are legal and round-trip as empty strings. Values are passed as one
argv element (no shell), so OS argv limits (~256 KiB on darwin) bound the
practical value size; an oversize or BWS-rejected value is a provider error
that names the key, never the value.

### Secret note: sync metadata

Scope hashes are one-way, so each BWS secret's `note` field carries the
reverse mapping as JSON:

```json
{
  "kinko_sync_format": 1,
  "machine_id": "a1b2c3d4e5f60718",
  "profile": "default",
  "scope": "path",
  "path": "/work/project-a",
  "key": "GITHUB_TOKEN"
}
```

`path` and `profile` are omitted for `"scope": "shared"` (shared scope is
vault-wide). Pull reconstructs (profile,
scope, key) from the note and cross-checks it against the parsed name
(machine id, scope hash recomputed from the note fields, key name); any
mismatch, missing note, or unparseable note on a machine-owned secret fails
pull validation with a policy error listing the offending BWS secret ids
(never values). `--force` does not bypass malformed remote metadata; the
user must repair or delete those secrets in BWS.

Privacy note: profile names and absolute directory paths are visible to
anyone who can read the BWS project. This is strictly less sensitive than
the values themselves, which are also there; it is called out so users know
what sync discloses.

### Project

`bws secret create` requires a project id. Resolution order:

1. `--project-id` flag
2. `KINKO_BWS_PROJECT_ID` environment variable
3. Encrypted config key `sync.bws.project_id` (`kinko config set`)
4. If the access token can see exactly one project (`bws project list`
   returns one entry), that project is used and reported on stderr.

If none resolves, sync fails with a policy error explaining the four
options. Pull lists secrets from the resolved project only.

## Access Token Sources and Isolation

Token resolution order:

1. `KINKO_BWS_ACCESS_TOKEN` environment variable (explicit override).
2. The kinko shared-scope secret named `KINKO_BWS_ACCESS_TOKEN`
   (registered once with `kinko set --shared KINKO_BWS_ACCESS_TOKEN=...`).
   Sync always verifies the vault password before doing anything, so the
   shared secret is readable at exactly the point the token is needed; this
   keeps the token encrypted at rest with no extra setup on each shell.

If neither source yields a token, sync fails with a policy error naming
both options. When both are set, the environment variable wins and a
stderr notice says the shared secret was ignored.

The reserved key `KINKO_BWS_ACCESS_TOKEN` is excluded from sync in both
directions: push never uploads it (storing the BWS access token inside BWS
itself would be circular disclosure) and pull ignores a remote entry with
that key name.

Isolation rules:

- kinko never reads `BWS_ACCESS_TOKEN` from its own environment. If
  `BWS_ACCESS_TOKEN` is set in the parent environment, kinko prints a
  one-line stderr notice that it is ignored for kinko sync, to prevent the
  user from silently syncing into the wrong Bitwarden account or assuming
  their existing token is being used.
- The `bws` subprocess is started with a minimal, explicitly constructed
  environment: `BWS_ACCESS_TOKEN=<resolved kinko token>` plus only the
  variables bws needs to run (`HOME`, `PATH`, `TMPDIR`, and TLS/locale
  basics). The parent `BWS_ACCESS_TOKEN` is therefore structurally incapable
  of leaking into the subprocess.
- The token is injected via the child environment, never via
  `--access-token`, because command-line arguments are visible in the
  process table.
- The token value never appears in kinko output or error text; bws stderr is
  surfaced in kinko error messages only after redacting any occurrence of
  the token string.

## Command Interface

### `kinko sync <push|pull> --provider=bws`

```
kinko sync push --provider=bws [--force] [--dry-run] [--project-id <id>] [--json]
kinko sync pull --provider=bws [--force] [--dry-run] [--project-id <id>] [--json]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--provider` | (required) | Sync provider; v1 accepts only `bws` |
| `--force` | off | Overwrite the destination side on conflict |
| `--dry-run` | off | Print the plan (creates/updates/deletes/conflicts); mutate nothing, including sync state |
| `--project-id` | unset | BWS project id (highest-priority resolution source) |
| `--json` | off | Machine-readable plan/summary output (names and scopes only, never values) |

Behavior common to push and pull:

- Requires vault password re-entry before any scope enumeration or remote
  call, matching the existing cross-scope policy (`show --all-scopes`,
  `path prune-missing`): sync touches every profile and every scope.
- Requires the machine id; if absent, fails with a policy error pointing at
  `kinko migration`.
- Acquires the vault mutation lock (pull mutates the vault; push mutates the
  encrypted config's sync state; holding it in both directions also
  serializes concurrent syncs).
- Covers every profile, the vault-wide shared scope, and every path scope.
  Folder vaults, encrypted config payloads, and the reserved
  `KINKO_BWS_ACCESS_TOKEN` shared key are excluded.
- Prints a summary of counts (created/updated/deleted/adopted/unchanged/
  conflicts) with key names and scope labels only.

### `kinko migration`

```
kinko migration [--yes|-y] [--json]
```

- Inspects the vault and lists pending metadata migrations. v1 defines
  exactly one: `assign-machine-id` (vault has no `machine_id` in
  `meta.v1.json`).
- Preview by default (like `path prune-missing`): shows what would change,
  mutates nothing. `--yes` applies after vault password verification.
- Applies under the mutation lock; `meta.v1.json` is rewritten via staged
  temp file + rename (the existing atomic-write pattern).
- Running with nothing to migrate succeeds and reports "no pending
  migrations" (exit 0), so it is safe to script.
- The command is intentionally generic so future vault-format migrations
  slot in as new named migration steps.

### Examples

```bash
# One-time setup on an old vault
kinko migration --yes

# Register the token once as a kinko shared secret (preferred), or export
# KINKO_BWS_ACCESS_TOKEN in the environment for this run
kinko set --shared KINKO_BWS_ACCESS_TOKEN="0.abc...:def..."

# Configure project once (encrypted config), then push everything
kinko config set sync.bws.project_id 3fa85f64-5717-4562-b3fc-2c963f66afa6
kinko sync push --provider=bws

# See what pull would change without touching the vault
kinko sync pull --provider=bws --dry-run

# Remote diverged and local is authoritative
kinko sync push --provider=bws --force
```

## Sync Semantics

### Sync state

Sync state lives in the encrypted config blob (`config.v1.bin`) under the
reserved key `sync.bws.v1`, following the precedent of folder records under
`folders.v1`: a JSON document stored as a config value, read and written via
the existing `loadConfig`/`saveConfig` path (DEK-encrypted, atomic save, no
new file or AAD context). It holds:

- `format` (1), `machine_id`, `project_id` (last used; mismatch on a later
  run produces a warning),
- one record per synced entry: BWS secret id, BWS name, profile, scope kind,
  path, key name, BWS `revisionDate` at last sync, SHA-256 of the value at
  last sync.

Recorded `revisionDate` per action: after create/edit, the value from the
`bwsSecret` object bws returns for that call; for adopt and for the
unchanged baseline, the value from the `secret list` snapshot. Reusing the
list-time revision after an edit would make the next push a false conflict.

The state is encrypted (as all config is) because value hashes permit
offline guessing of low-entropy values and the record enumerates paths and
keys. Losing or clearing the state is non-fatal: every entry degrades to
"never synced" (see below), which is conservative (more conflicts, never
silent overwrites). One consequence to know: after state loss, a key
deleted locally is resurrected by the next pull (remote present, local
absent, no state = create) instead of conflicting — expected under the
never-synced degradation. A state blob whose `machine_id` differs from the
vault's (e.g. `config.v1.bin` restored from another vault) is treated as
absent, with a warning.

`kinko config show` prints every config key verbatim (it has no per-key
handling; `folders.v1` is printed raw today), so `sync.bws.v1` — value
hashes and paths included — appears in its output. That output already
requires an unlocked session, and the hashes disclose nothing beyond what
the same session can read directly, so this is accepted rather than
special-cased.

Push and pull both rewrite the state to reflect the post-command reality.
`--dry-run` never writes it.

### Change classification

For each entry, three observations drive the decision:

- L: local value (present/absent, SHA-256)
- R: remote secret (present/absent, value, `revisionDate`)
- S: state record (present/absent, last revisionDate, last value hash)

"Remote changed" means R's revisionDate differs from S's (or S is absent).
"Local changed" means L's hash differs from S's (or S is absent).

### Push (local is source of truth)

| Local | Remote | State | Action |
|-------|--------|-------|--------|
| present | absent | absent | create remote |
| present | absent | present | conflict (remote deleted elsewhere); `--force` recreates |
| present | present, value equal | any | adopt: no remote write, record state |
| present | present, value differs | absent | conflict; `--force` overwrites remote |
| present | present, value differs | present, remote unchanged | update remote (normal edit) |
| present | present, value differs | present, remote changed | conflict; `--force` overwrites remote |
| absent | present | present, remote unchanged | delete remote (local deletion propagates) |
| absent | present | present, remote changed | conflict; `--force` deletes remote |
| absent | present | absent | not ours to touch: ignore (never synced by this vault) |
| absent | absent | present | drop the stale state record (deleted on both sides; reported as unchanged) |

### Pull (remote is source of truth)

Only secrets owned by this machine id are considered.

| Remote | Local | State | Action |
|--------|-------|-------|--------|
| present | absent | absent | create locally |
| present | absent | present | conflict (deleted locally after sync); `--force` recreates |
| present, value equal | present | any | adopt: no local write, record state |
| present, value differs | present | absent | conflict; `--force` overwrites local |
| present, value differs | present | present, local unchanged | update local |
| present, value differs | present | present, local changed | conflict; `--force` overwrites local |
| absent | present | present, local unchanged | delete local (remote deletion propagates) |
| absent | present | present, local changed | conflict; `--force` deletes local |
| absent | present | absent | ignore (local-only entry; push owns it) |
| absent | absent | present | drop the stale state record (deleted on both sides; reported as unchanged) |

Pull creates missing local containers as needed: an entry whose note names a
profile or path scope that does not exist locally creates that profile/path
scope in the vault (the directory itself is not required to exist on disk;
`path prune-missing` remains the cleanup tool for such scopes).

### Conflict handling

- All entries are classified before anything mutates. If any entry is a
  conflict and `--force` is not set, the command prints every conflict
  (profile, scope label, key name, reason) and exits with the sync-conflict
  exit code without mutating vault, remote, or state.
- `--force` resolves every conflict in the command's direction (push: remote
  loses; pull: local loses). There is no per-key selection in v1; the error
  output is designed to let the user resolve manually (edit the losing side,
  or re-run with `--force`) — per the requirement, conflict resolution is
  the user's responsibility.
- Remote-side ambiguity is never auto-resolved, even with `--force`: two
  remote secrets with the same name, or machine-owned secrets with
  malformed/mismatched notes, fail validation with a policy error.

### Atomicity

- Pull: all local changes are applied to the in-memory vault and persisted
  in one encrypted vault write (the same all-or-nothing save every other
  mutation uses), followed by the sync-state save into `config.v1.bin` —
  two writes, vault first. A crash between them leaves correct vault data
  with stale state, which degrades conservatively (extra conflicts on the
  next run, never silent overwrites).
- Push: remote mutations are per-secret bws calls and cannot be atomic. Sync
  validates everything first, then applies; if a bws call fails midway, the
  command reports which entries were applied, records state for exactly
  those, and exits non-zero. A create/edit whose response bws returns but
  kinko cannot parse counts as a failed action: no state is recorded for it,
  and the next push heals it through the adopt row (values equal, state
  absent). Re-running push is convergent (already-applied entries classify
  as unchanged/adopt).

## bws Invocation Layer

- Binary resolution: `KINKO_BWS_BIN` (absolute path or name looked up in
  `PATH`) when set, else `exec.LookPath("bws")`. A missing binary is a
  provider error with install guidance.
- Every call runs with `--output json`, `--color no`, a per-invocation
  timeout (30s default) enforced via `context`, stdin closed, and the
  minimal child environment described above. No shell is involved.
- Calls used: `bws project list`, `bws secret list <project_id>` (one call
  supplies names, values, notes, and revision dates for the whole plan),
  `bws secret create <key> <value> <project_id> --note <json>`,
  `bws secret edit <id> --value ... --note ...`, `bws secret delete <ids...>`.
  Secret values do appear in bws argv (create/edit take them positionally);
  this is a bws CLI limitation, is local to the user's own machine and
  process table, and is documented; batching deletes reduces call count.
- Non-zero exit or unparseable JSON becomes a provider error carrying bws's
  redacted stderr. bws rate limiting therefore surfaces as a provider error
  with the server message intact.

## Exit Codes

Consistent with the existing `cliError` mapping; two new codes:

| Condition | Exit code |
|-----------|-----------|
| Missing/invalid flags, missing machine id, scope-hash collision, malformed remote metadata, project unresolvable | policy (11) |
| Vault password verification failure | auth (10) |
| Mutation lock conflict | lock conflict (12) |
| Local file IO failures | io (13) |
| Sync conflict detected without `--force` | sync conflict (15) |
| bws binary missing, bws non-zero exit, bad JSON, timeout, rate limit | provider failure (16) |

`internal/kinko/cli_error.go` currently defines 0 and 10-14
(`exitCodeMetadataInvalid` is the highest), so 15 (`exitCodeSyncConflict`)
and 16 (`exitCodeProviderFailed`) are free.

## Security Considerations

- Pushing to BWS intentionally places plaintext values (from kinko's
  perspective) in Bitwarden's custody; Bitwarden's server-side encryption
  and the access token become part of the trust boundary. This is the
  feature's purpose but is stated explicitly.
- Vault password re-entry is required for both directions because sync
  enumerates all scopes, matching the strictest existing read policy.
- Values never appear in kinko stdout/stderr/JSON output; plans and
  summaries carry names, scopes, and counts only. Value comparison uses
  constant-size hashes in memory where possible.
- The token redaction pass over bws stderr prevents accidental token
  disclosure in error messages.
- The minimal child environment prevents both directions of token
  confusion: the user's own `BWS_ACCESS_TOKEN` cannot reach kinko's bws
  calls, and the resolved kinko token is not exported to any subprocess
  other than bws itself. Note that storing the token as the shared secret
  `KINKO_BWS_ACCESS_TOKEN` means `kinko export`/`exec --all` will expose it
  to shells and child processes like any other shared secret; users who
  want it available only to sync should keep it out of exports with the
  existing `--exclude KINKO_BWS_ACCESS_TOKEN` / `--env` selection.
- Sync state is DEK-encrypted at rest for the same reason vault data is.
- `--force` is scoped: it overrides divergence conflicts only, never
  validation failures (ambiguous names, bad notes, hash collisions).

## Testing Strategy

- Unit: scope-hash vectors (path/shared/profile separation, normalization,
  collision detection); name format parse/build round-trip; note metadata
  encode/decode/cross-check; push/pull classification tables driven as
  table tests covering every row above; state encrypt/decrypt round-trip;
  child environment construction (parent `BWS_ACCESS_TOKEN` never present;
  token injected; redaction).
- bws integration is tested against a stub `bws` executable (a small Go
  test binary installed into a temp `PATH` via `KINKO_BWS_BIN`) that speaks
  the JSON protocol and simulates: success, missing binary, non-zero exit,
  garbage JSON, slow response (timeout), duplicate names, malformed notes,
  and revisionDate changes between list and edit.
- E2E: init -> set values in multiple profiles/scopes -> push (assert stub
  received expected creates) -> mutate local -> push (updates) -> simulate
  remote change -> push (conflict, then `--force`) -> pull round-trip into a
  second vault dir sharing the same machine id fixture -> deletion
  propagation both directions -> `--dry-run` mutates nothing (byte-compare
  vault, state, stub journal).
- Migration: old-vault fixture (meta without `machine_id`) -> preview ->
  `--yes` applies -> idempotent second run; doctor warning present before,
  absent after.

## Open Questions Resolved by Default (see user-qa)

Recorded in `design-docs/user-qa/qa-bws-sync.md`: profile inclusion in the
scope hash, shared-scope representation, deletion propagation semantics,
project auto-resolution, paths appearing in BWS notes, token precedence and
self-exclusion, and `--force` on pull.

## References

- `design-docs/references/README.md` — bws CLI documentation links
- `design-docs/specs/design-restore.md` — staged-write and password-input
  patterns this design reuses
- `internal/kinko/vault.go` — `loadConfig`/`saveConfig`, the encrypted
  config path the sync state rides (no new file or AAD context)
