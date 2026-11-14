# Design: `kinko sync` with Bitwarden Secrets Manager (bws)

This document specifies remote synchronization of kinko secret values with
Bitwarden Secrets Manager (BWS), using the official CLI for compatible
control/read operations and a value-safe in-process transport for mutations,
plus the supporting machine and scope metadata.

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
   default; `--force` remains the compatibility override and granular rules
   provide explicit per-entry resolution.
3. The BWS access token used by kinko comes from the
   `KINKO_BWS_ACCESS_TOKEN` environment variable or from the kinko shared
   secret of the same name, and must never be confused with a
   `BWS_ACCESS_TOKEN` that the user may already have in the machine
   environment for their own bws usage.
4. BWS control/read operations may use the external `bws` command, while
   value-bearing mutations use a value-safe in-process transport by default.
   Provider capabilities and missing dependencies are diagnosed clearly.
5. Existing vaults (created before this feature) gain the new metadata via an
   explicit `kinko migration` command; new vaults get it at `kinko init`.
6. Secret values never appear in kinko output, logs, error messages, or
   process argument lists when the secure transport is used. The historical
   CLI mutation transport is retained only as an explicit compatibility mode.

Non-goals: implicit synchronization of folder-vault bytes or the complete
encrypted config payload, automatic takeover of another live machine id, and
field-level merging inside a secret value. Cross-machine copy/adoption,
portable scope mapping, selective operation, repair, and provider extension
points are specified below.

## Operational Completion Contract (2026-08-05)

This section is the normative extension for the completion features. The base
v1 sections below remain normative for an invocation that uses no new flag.
Existing note format 1, secret names, state key `sync.bws.v1`, JSON fields,
exit codes, and push/pull classification tables remain accepted. A new feature
must not silently migrate them or change a legacy plan.

### Compatibility and state-format rules

- Existing `sync push|pull --provider=bws` still selects all profiles, paths,
  and shared entries, propagates baseline-proven deletion, and interprets
  `--force` as "the command direction wins" (local for push, remote for pull).
  It is not equivalent to one fixed `source` policy. Explicit `--force` cannot
  be combined with `--on-conflict` or `--resolve`.
- Format-1 notes and format-1 state are read indefinitely. Legacy-only runs
  keep writing format 1. The first operation that needs logical paths,
  selectors, checkpoints, or per-entry policy writes state format 2 under the
  same encrypted key. An old binary then rejects the unsupported format rather
  than acting on stale state. The format-2 decoder preserves unknown fields as
  raw JSON on read/modify/write; unknown major formats fail closed.
- Format 2 records provider endpoint, organization, project, machine, remote
  secret id/name/revision, local and logical scope identity, key, baseline
  value hash, and a schema/version discriminator. Provider identity is a hash
  of canonical non-secret endpoints plus organization and project ids; profile
  labels alone are not identity. No token, value, path-map root, or checkpoint
  plaintext is stored outside the existing encrypted config blob.
- Format 2 also retains a value-free ownership record for each confirmed kinko
  create (id, identity, and last kinko-confirmed revision). Resetting a
  baseline does not erase ownership proof. Ownership records are removed only
  after a confirmed remote deletion; a revision mismatch makes the record
  ineligible for automatic prune and requires exact-id acknowledgement. Thus
  the ledger is bounded by live or ambiguously deleted kinko-created records.
- Selected writes merge only selected records into state. Unselected records,
  including their unknown fields, are retained byte-for-byte as raw JSON and
  are never inferred to be absent. `sync reset` is the only workflow that can
  intentionally discard a selected baseline.
- Bootstrap provenance and maintenance checkpoints are not normal baselines.
  In particular, a bootstrap never associates a source-machine secret id with
  the current machine; the subsequent current-machine push therefore creates
  a new remote record instead of editing or deleting the source.

All sync flags and selector syntax are validated before password/provider
access. Every sync command that reads encrypted cross-scope data, including a
preview or status, requires direct password re-entry. It then takes the vault
mutation lock for a consistent snapshot, reloads metadata/vault/config under
that lock, and holds the lock through final persistence or output. Push/pull
retain their apply-by-default behavior and `--dry-run`; bootstrap, reset,
reconcile, and prune are preview-by-default and require `--yes` to apply. A
complete value-free plan is built before the first mutation. `--yes` confirms
only that pinned plan and never weakens password, selection, or revision gates.

### Cross-machine bootstrap and disaster recovery

`kinko sync bootstrap --provider=bws --from-machine-id <id>` copies a source
namespace into the current vault. Preview is the default and `--yes` applies.
The read plan is pinned to endpoint, organization, project, source machine,
secret ids, revisions, metadata, selector, and path-map digest. Apply re-gets
every selected source id and aborts before the single atomic vault write if any
precondition changed. It performs no BWS mutation, never changes either machine
id, never creates a normal baseline, and never treats an omitted source entry
as a deletion.

The default target must contain no user secret entries; the reserved BWS token,
encrypted provider configuration, and empty containers do not make it
non-empty. `--merge` allows existing target values. Equal values are unchanged;
missing values are created; every differing value requires an exact conflict
resolution. The entire local result is built in memory and committed once, so
an interrupted bootstrap cannot leave partial local containers. A value-free
checkpoint may cache only the validated plan and is safe to discard.

Restore has two supported forms:

1. A kinko backup retains its machine id and baseline. The operator runs
   `sync status --online` and a dry-run before the first mutation.
2. If only BWS survives, a new vault bootstraps from the lost id and later
   pushes under its new id. The lost namespace is only an orphan *candidate*.
   Pruning it requires an explicit retired-machine declaration described below.

Identity takeover remains unsupported because kinko cannot prove another
writer is dead. Exact identity recovery requires restoring vault metadata.

### Portable logical paths and format-2 notes

A format-2 path note stores only `logical_path`, not the source machine's
absolute display path. A logical path is a canonical, slash-separated anchor
and relative path such as `work/project-a`. Anchors match
`[a-z][a-z0-9-]{0,62}`; empty, `.`, `..`, repeated separators, backslashes,
NUL, and platform volume syntax are rejected. Shared notes have no path.
Format-2 names derive their eight-hex scope hash from the distinct
`kinko.scope.v2` domain, profile, scope kind, and logical path. Format-1 names
continue to use the absolute-path v1 domain.

Repeatable `--map-path <anchor>=<absolute-root>` overrides encrypted
`sync.paths.v1` mappings for that invocation. Roots are absolute and cleaned;
duplicate anchors, equal canonical roots, case-fold aliases on a
case-insensitive filesystem, or overlapping roots with an ambiguous longest
match fail closed. Push maps a stored absolute scope by longest root prefix.
Pull/bootstrap lexically joins the relative logical path below the mapped root,
cleans it, and verifies containment. These workflows create only vault scope
records, never directories or files. Unmapped logical paths are conflicts.

Merely changing a map changes local materialization, not remote metadata.
`sync reconcile --upgrade-metadata` previews a v1-to-v2 replacement. Apply
requires `--yes`, an exact current id/revision/value/note recheck, successful
creation and read-back of the v2 record, atomic state replacement, and then an
immediate revision recheck before deletion of the v1 id. An interruption may
temporarily leave both records; the checkpoint recognizes only that exact pair
and resumes without creating a third. Collisions stop the whole migration.

### Selection and exclusion boundary

Push, pull, bootstrap, status, reset, reconcile, and prune accept repeatable
`--select-profile`, `--select-path`, `--select-key`, and corresponding
`--exclude-*` flags plus `--shared=include|exclude|only`. The existing root
`--profile` and `--path` remain ignored by sync, including when explicitly set,
because that is existing behavior. Profile and key values are exact by default;
only a `glob:` prefix enables a case-sensitive, platform-independent key glob
with `*`, `?`, and bracket expressions; malformed patterns fail before access.
Paths must use `logical:<path>` or `local:<absolute-path>` so portable and
legacy identities cannot be confused.

Selection is evaluated against the union of local entries, validated remote
metadata, and state records. All inclusions intersect, exclusions win, and the
reserved access-token key is always excluded. An empty effective selection is
a successful no-op for status and a policy error for a mutating workflow.
Malformed machine-prefixed metadata cannot be safely selected or excluded and
blocks mutation, except for the exact malformed-record prune flow below.
Offline status and reset have no provider dependency and evaluate only local
and state records; online status and provider workflows add the pinned remote
set. This difference is explicit in their plan JSON.

The normalized selector and its digest appear in plans/checkpoints. Excluded
entries may be returned by BWS's project-wide list API, but their values are
discarded immediately after metadata classification; they never enter an
action, mutation request, comparison hash, state update, checkpoint, output, or
deletion inference. Tests byte-compare excluded local data and raw state.

### Status, reset, reconcile, and prune

- `sync status` is strictly non-mutating. It requires the normal sync password
  re-entry because offline status reads encrypted cross-scope data. Offline
  mode reports value-free local identity, maps, selector, baseline/checkpoint
  health, and formats. `--online` adds provider drift after a pinned read.
- `sync reset` previews selected baseline removal; `--checkpoint` selects only
  the checkpoint, while `--baseline` selects only baseline records and neither
  flag means both. A checkpoint is indivisible and can be removed only when
  its stored selector digest exactly matches the requested selector (or no
  selector was supplied); it is never partially rewritten. `--yes` applies
  one encrypted config write. It never changes vault or BWS values and
  explicitly reports the resurrection/deletion ambiguity that a future
  never-synced run can create.
- `sync reconcile` previews state adoption where local and remote value hashes,
  name, note, machine, endpoint, organization, project, scope, and key agree.
  `--yes` applies state only. Divergence remains a conflict. Metadata upgrade
  additionally requires `--upgrade-metadata` and follows the replacement gate
  above.
- `sync prune` previews by default and `--yes` applies. A current-machine entry
  absent from local data and the active baseline is merely *untracked*, not
  provably orphaned; it requires explicit `--secret-id`. Automatic candidates
  are limited to ids in a completed checkpoint/ownership ledger or selected
  baseline tombstone. A different namespace requires both
  `--machine-id <id>` and `--ack-retired-machine <same-id>`; the mismatch is an
  error and the warning states that kinko cannot prove retirement. Malformed or
  duplicate records additionally require exact repeatable `--secret-id` and
  `--ack-malformed`. Foreign names and other projects are never candidates.
- `--prune-empty-scopes` removes only empty vault map containers created by a
  selected sync. It never removes filesystem paths or folder vaults.

Before every remote update or delete, kinko re-gets the immutable id and
verifies endpoint, organization, project membership, machine, name, note,
value hash, and captured revision. Mutation is refused unless authoritative
provider metadata proves the secret belongs to exactly the selected project;
this prevents deleting or rewriting a secret shared with another project. The
current BWS CLI and official SDK expose no atomic
revision precondition, so a remaining check/mutation race is unavoidable. Bulk
delete is forbidden: ids are deleted one at a time, no delete is blindly
retried after an ambiguous response, and the result is reconciled by id.
Ordinary push/pull keeps compatible `--delete=auto`; `keep` suppresses
propagation and `confirm` requires `--yes` if the plan contains a deletion.

### Granular conflict rules

`--on-conflict=fail|local|remote|skip` sets a direction-independent default.
The plan emits a stable, value-free `entry_id` as the full lowercase SHA-256 of
the canonical provider/project/machine/scope/key identity. Repeatable
`--resolve <entry_id>=local|remote|delete-local|delete-remote|skip` addresses
exactly one conflict. A rule that is duplicate, matches no current conflict,
or requests deletion of an absent side is an error. A remote delete also needs
a baseline or the explicit prune acknowledgements; a local delete is included
in the one atomic vault write. No rule can cross selector, endpoint,
organization, project, machine, scope, key, secret id, or revision boundaries.
`--force` never bypasses those gates or malformed/duplicate validation.

### BWS configuration, diagnostics, and version gates

`--bws-config-file`, `--bws-profile`, and `--bws-server-url` have matching
`KINKO_BWS_*` variables and encrypted config keys. Precedence is flag,
`KINKO_` environment, encrypted config, then the BWS default config/profile.
Parent `BWS_CONFIG_FILE`, `BWS_PROFILE`, `BWS_SERVER_URL`, and
`BWS_ACCESS_TOKEN` are ignored. Kinko reads only the selected profile's
`server_base`, `server_api`, and `server_identity`; an explicit server URL
overrides the base and derives API/identity URLs using BWS 2.0 rules. Endpoints
must be absolute HTTPS URLs except an explicit test-only loopback transport;
userinfo, query, and fragment are forbidden. Canonicalization lowercases
scheme/host, removes a default port, normalizes the required API/identity path
and trailing slash, and preserves non-default ports before identity hashing.
Kinko does not reuse or mutate the user's BWS authentication state. A CLI
control call uses a temporary 0700 home/config with resolved endpoints and
`state_opt_out=true`.
External config is opened once without following a final symlink, must be a
regular file owned by the current user, and must not be group/world writable;
unsafe ownership, permissions, parse errors, or duplicate profile keys fail
before the token is used. Resolved endpoints are pinned for the operation, so
a later file change cannot redirect an in-flight token.

`kinko doctor` with no new flags preserves today's local, non-interactive
behavior and output. `kinko doctor --provider=bws` adds local binary/version,
config/profile/endpoint, transport capability, path-map, and encrypted
state/checkpoint checks; checks needing encrypted data use normal password
re-entry. `--online` distinguishes missing credentials, rejected/expired
token, TLS/clock failure, project not found/not assigned, read forbidden, and
write capability unknown. `--check-write --yes` is the only doctor mode that
mutates: it creates one randomized canary, records its id before further work,
reads it back, and deletes that exact id. Delete failure reports a value-free
cleanup id/manifest.

Only exact CLI versions covered by adapter contract fixtures are enabled for
mutation. The initial allowlist contains only installed/inspected `2.0.0`;
`0.3+` or an untested `2.0.x` patch is not proof of output compatibility.
Unknown versions may run `doctor` and explicit read-only diagnostics with a
warning, but mutation fails closed. There is no version-gate override for a
mutation.

### Secure transport and dependency boundary

Installed `bws 2.0.0` requires the value in `secret create <KEY> <VALUE>
<PROJECT_ID>` and `secret edit --value <VALUE>` and has no stdin/fd option.
Therefore a subprocess wrapper cannot make those mutations value-safe.

`--bws-transport=auto` is the default. List/get and revision-checked delete may
use the isolated CLI adapter, but create/update require an in-process transport
with a `value-safe-mutation` capability. Synced payload values are passed only
as in-memory request fields: kinko never adds them to argv or an environment,
and redaction wrappers cover provider errors before formatting. If the
capability is not compiled/available, a plan containing create/update fails
before any mutation; auto never falls back to CLI mutation.

The official Go SDK v2.1.0 was inspected: it provides in-process CRUD but uses
CGO, requires explicit API/identity and organization identity, provides no
revision-conditional mutation, and has a restrictive SDK license that may not
permit redistribution in kinko's normal artifacts. It is therefore a candidate
adapter, not an approved unconditional dependency. Distribution/license review
and target-matrix validation are mandatory capability gates. A separately
built SDK-enabled artifact may be used only where its license is affirmatively
accepted. `--bws-transport=cli-legacy --allow-secret-argv` retains current
create/edit behavior, always warns, and requires both flags even in a TTY.

The access token may already enter kinko through the documented
`KINKO_BWS_ACCESS_TOKEN` source; secure mode does not copy it to argv, output,
progress, errors, or checkpoints. A CLI control adapter receives only the token
in its isolated `BWS_ACCESS_TOKEN` environment. Synced payload values never
enter any environment. Token and payload buffers are released promptly, and
tests treat provider request/response serialization as sensitive memory.

### Bounded retry, progress, and resume

Transient read/list/get failures (network, timeout, 429, and 5xx) retry with
full-jitter capped exponential backoff and `Retry-After`. Defaults are five
retries, 500 ms initial delay, 30 s per-delay cap, and two minutes total delay;
`--max-retries` and `--retry-max-delay` may increase these only up to ten
retries per request, 60 seconds per delay, and a five-minute global retry-delay
budget per operation. Authentication, permission, validation,
conflict, and non-idempotent mutations are not blindly retried.

Before a remote mutation, an encrypted checkpoint stores operation/provider
identity, selector/plan digests, action ids, expected revisions/hashes, phase,
and confirmed result ids/revisions, but no values or token. It is persisted
before the first action and after each confirmed action. An ambiguous create is
reconciled by the exact deterministic name, note, project, and value hash;
zero matches permits one retry, one match adopts it, and multiple matches stop.
An ambiguous update re-gets its immutable id: intended content is adopted, an
unchanged precondition permits one retry, and any other content stops. An
ambiguous delete treats a missing id as confirmed, permits one retry only if
the complete precondition is still present, and otherwise stops. Resume
reloads values from the vault and verifies their hashes plus every remote
precondition.
`--resume=auto|require|never` is bounded to the one matching checkpoint;
changed inputs refuse resume. `sync reset --checkpoint --yes` discards it.

Progress is value-free stderr output. `--progress=auto|plain|none|jsonl`
defaults to TTY-aware `auto`; JSONL stays on stderr. Existing final stdout and
legacy JSON fields remain unchanged; new fields are additive only when a new
feature is used. Provider errors are classified before redaction and never
include raw request/response bodies.

### Provider/payload and testing boundaries

The sync core accepts typed payload descriptors and advertised provider
capabilities. Only `secret-entry/v1` is enabled. Unknown payloads or missing
capabilities fail before planning mutation. `folder-vault` and `config` remain
disabled; access tokens, sync state/checkpoints, machine metadata, folder
registrations, and bootstrap paths are permanently excluded from any future
config payload.

Hermetic stub tests cover provider and CLI adapters, selectors, maps,
bootstrap, raw-state preservation, conflicts, every deletion gate, malformed
records, fake-clock retries, ambiguous results, resume, diagnostics, and
redaction. Secure-mode tests use canary values and inspect argv, added child
environment, stdout/stderr, structured output, errors, progress, and decrypted
checkpoint fixtures for any occurrence.

Real BWS tests require all of `KINKO_TEST_REAL_BWS=1`,
`KINKO_TEST_BWS_ACCESS_TOKEN`, and `KINKO_TEST_BWS_PROJECT_ID`. Each run uses a
cryptographically unique machine/name prefix, snapshots all pre-existing ids,
records each confirmed or discovered created id immediately, and deletes only
ids both absent from the snapshot and still matching the run's project/prefix.
The suite never bulk-deletes and is excluded from default tests, CI, and race
runs. Failure leaves a 0600, value-free allowlist manifest.

No Go source or test file may exceed 1000 lines; new files target 700. The
currently oversized `cobra_runtime.go` (1002 lines) and `sync_e2e_test.go`
(1010 lines) must be split mechanically before feature work, with tests and
package-private behavior unchanged.

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
values are legal and round-trip as empty strings. The secure transport sends
values in-process and rejects a value larger than 256 KiB before provider
access; a lower documented/provider limit also applies. The legacy
CLI transport passes a value as one argv element and is available only with
the acknowledgements specified in the operational completion contract.

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
that key name. Exclusion happens only after remote-name validation: duplicate
machine-owned entries with this reserved name are still ambiguous and fail
with a policy error, including when `--force` is set.

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
- CLI calls run with `--output json`, `--color no`, a per-invocation
  timeout (30s default) enforced via `context`, stdin closed, and the
  minimal child environment described above. No shell is involved.
- Calls used: `bws project list`, `bws secret list <project_id>` (one call
  supplies names, values, notes, and revision dates for the whole plan),
  `bws secret create <key> <value> <project_id> --note <json>`,
  `bws secret edit <id> --value ... --note ...`, and one
  `bws secret delete <id>` call per secret. Individual delete calls provide
  exact successful-prefix state when a later deletion fails.
  Create/edit through this path are legacy-only because values appear in bws
  argv; secure-mode create/edit use the in-process provider transport.
- A non-zero exit becomes a provider error carrying bws's redacted stderr.
  Successful list/create/edit calls must return valid JSON. Successful delete
  output is intentionally treated as opaque because supported BWS CLI versions
  return human-readable singular or plural confirmation text even when JSON
  output is requested. BWS rate limiting therefore surfaces as a provider
  error with the server message intact.

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
