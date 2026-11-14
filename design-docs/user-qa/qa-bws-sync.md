# QA: BWS Sync Design Decisions Taken by Default

Source design: `design-docs/specs/design-bws-sync.md`

The requirements fixed the BWS name format (`{machine_id}_{directory_hash}_{KEY_NAME}`),
the token sources (`KINKO_BWS_ACCESS_TOKEN` env var or a kinko shared
secret of the same name — user follow-up on 2026-07-13), the command shape
(`kinko sync {push|pull} --provider=bws`, conflict = error, `--force`),
`kinko migration` for old vaults, and bws-as-subprocess. Note: the
requirement's "store under the id ..." is realized as the BWS secret
*name*; the BWS `id` field is a server-assigned UUID. The points below
were not specified; each was decided by default in the design and can be
revisited before implementation.

## Q1: Profile handling in the directory hash

The name format has one slot between machine id and key, but kinko scopes
are (profile, path). **Default taken**: the profile is mixed into the
directory-hash input, so the same directory in two profiles gets two hashes.
Alternative: sync only the active profile per run (`--profile`); rejected
because the requirement says "all kinko values".

## Q2: Shared-scope representation

Shared scope has no directory (and is vault-wide: `vaultData.Shared` is a
single map, not per-profile). **Default taken**: shared scope gets one
constant hash per vault from the same derivation with `kind=shared` and
empty profile/path, keeping the fixed-width name grammar. Alternative: a
literal `shared` token in the name.

## Q3: Deletion propagation

**Default taken**: sync is a mirror for this machine's entries, so a key
deleted locally after a sync is deleted remotely on the next push (and vice
versa for pull), with the usual conflict check. Alternative: push/pull only
add and update, never delete (safer but remote accumulates stale secrets).

## Q4: Absolute paths and profile names stored in BWS notes

Scope hashes are one-way, so pull needs a reverse mapping; the design stores
(profile, scope, path, key) as JSON in each secret's note field. Anyone who
can read the BWS project sees local directory paths. The values themselves
are also there, so this adds little exposure, but it is a disclosure the
user should be aware of.

## Q5: Project resolution

`bws secret create` requires a project id, which the requirements did not
mention. **Default taken**: `--project-id` flag > `KINKO_BWS_PROJECT_ID` >
encrypted config `sync.bws.project_id` > auto-select when the token sees
exactly one project.

## Q6: Password re-entry for sync

**Default taken**: both push and pull require vault password re-entry
(sync enumerates every profile and scope, matching the `show --all-scopes`
and `path prune-missing` policy), even when a session is unlocked.

## Q7: Values on the bws command line

`bws secret create/edit` accept the value as a positional argument, so
secret values appear in the local process table for the duration of each
bws call. There is no stdin mode in the bws CLI for values. Accepted as a
documented limitation of the provider.

## Q8: Command name

The requirement says `kinko migration` (not `migrate`); the design uses
`kinko migration` verbatim.

## Q9: `--force` on pull

The requirement mentions force-*push* only. **Default taken**: pull gets a
symmetric `--force` that overwrites/deletes local values for this machine's
entries — the more dangerous direction, since local data loses. Alternative:
restrict `--force` to push and make pull conflicts terminal.

## Q10: Token precedence and self-exclusion

With two token sources, **defaults taken**: the env var wins over the
shared secret (explicit environment overrides stored config, with a stderr
notice), and the reserved shared key `KINKO_BWS_ACCESS_TOKEN` is excluded
from sync in both directions so the BWS access token is never stored in
BWS itself. Note that as a shared secret it is still exported by
`kinko export`/`exec --all` like any other shared value unless excluded.
