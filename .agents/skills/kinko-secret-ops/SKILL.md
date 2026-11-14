---
name: kinko-secret-ops
description: Use when users want to manage encrypted environment variables with the kinko CLI, including init/unlock, shared vs repo scope, set/get/show/delete, stale path-scope pruning, export/import, and exec-based runtime injection.
allowed-tools: Read, Write, Glob, Grep, Bash
---

# Kinko Secret Ops

Operate `kinko` safely for end users.

## When to Apply

Apply this skill when users ask to:
- Initialize or unlock a `kinko` vault
- Set, get, show, or delete secrets
- Work with shared and repository-specific scopes
- Prune stale repository/path-scoped local vault entries
- Export/import `.env`-style assignments
- Run commands with secrets via `kinko exec`
- Diagnose lock/keychain/TTY safety errors

## Operating Rules

- Never print plaintext secret values unless the user explicitly requests reveal output.
- Prefer `kinko exec` for runtime injection instead of persistent shell export.
- Treat `--force` as high risk and explain why it is needed before using it.
- Respect scope precedence: repository scope (`--profile` + `--path`) overrides shared scope.
- For destructive operations (`delete --all`, `delete --shared --all`, `path prune-missing --yes`, `explosion`), require explicit user intent.
- For interactive bulk deletes, expect destructive confirmation first and direct vault password re-entry only after confirmation; `--yes` skips only the destructive confirmation, so password verification is still required before loading, listing, or mutation.
- For `path prune-missing`, expect direct vault password re-entry before any path metadata output in both preview and prune modes. Preview is default and non-mutating; `--yes` is required to delete stale local path scopes.

## Quick Workflow

1. Check state:
```bash
kinko status
```

2. First-time setup:
```bash
kinko init
kinko unlock --timeout 9h
```

3. Add secrets:
```bash
kinko set API_KEY=xxx DB_URL=postgres://localhost
kinko set --shared ORG_TOKEN=yyy
```

4. Verify without leaking values:
```bash
kinko show
kinko get API_KEY
```

Use `kinko show --all-scopes` only when the user needs profile-wide scope
inspection. It requires vault password re-entry before any output, including
masked output, because it may display scopes outside the current directory.

Use `kinko path prune-missing` only when the user wants to inspect or remove
stored path scopes whose directories no longer exist. It preserves shared scope
data and ignores inherited `--path` because it operates on stored path scopes.

5. Runtime injection (recommended):
```bash
kinko exec --env API_KEY,DB_URL -- <command>
# or
kinko exec --all -- <command>
```

## Scope Model

- Shared scope: `kinko set --shared KEY=VALUE`
- Repository scope: `kinko --profile <name> --path <dir> set KEY=VALUE`
- Resolution rule: repository scope wins over shared for duplicate keys.

## Export and Import

Export (sensitive output):
```bash
eval "$(kinko export --force --confirm=false)"
kinko export bash --exclude AWS_SECRET_ACCESS_KEY --force --confirm=false
```

Import:
```bash
kinko import --file .env.export
kinko import bash --file .env.export --yes
```

Use `--allow-shared=false` when shared-scope markers in input should be ignored.

## Safe Command Patterns

Set one key:
```bash
kinko set-key API_KEY --value "xxx"
```

Delete one key:
```bash
kinko delete API_KEY --yes
```

Delete all keys in selected scope:
```bash
kinko delete --all --yes
kinko delete --shared --all --yes
```

Bulk delete behavior:
- `kinko delete --all` and `kinko delete --shared --all` list target keys, ask for destructive confirmation, then prompt on stderr for vault password re-entry only after confirmation.
- Declined confirmation prints `aborted`, does not prompt for the vault password, and keeps vault data unchanged.
- With `--yes`, confirmation is skipped and password verification still happens before target keys are loaded, listed, or deleted.
- Failed password verification leaves stdout empty and keeps vault data unchanged.
- Single-key deletes such as `kinko delete API_KEY --yes` do not add this extra password verification step.

Preview stale path scopes:
```bash
kinko path prune-missing
kinko path prune-missing --all-profiles
kinko path prune-missing --json
```

Prune stale path scopes:
```bash
kinko path prune-missing --yes
```

Path prune behavior:
- Preview mode reports stale local profile/path scopes and skipped diagnostics without deleting anything.
- `--yes` prunes only stale local path scopes after direct password verification; it does not delete shared scope data, config, unlock state, backups, profile definitions, or existing path scopes.
- `--all-profiles` scans every stored profile and must not be combined with an explicit `--profile`.
- Text and JSON output include profile names, paths, key counts, skipped diagnostics, and totals. Secret values and key names are not printed.

## Troubleshooting

- Locked/session errors:
  - Run `kinko unlock`, then re-check `kinko status`.
- Keychain preflight failures:
  - Optionally retry with `--keychain-preflight best-effort` only if the user accepts weaker startup checks.
- Redirect/TTY safety block on `show --reveal` or `export`:
  - Use TTY output or explicitly choose `--force` with user confirmation.
- Unexpected value resolution:
  - Check both shared and repository scopes, then verify `--profile` and `--path`.

## Response Format

When using this skill, respond in this order:
1. Exact command(s) to run
2. Expected result (without exposing secrets)
3. One-line risk note when using `--force`, `--yes`, or destructive operations
