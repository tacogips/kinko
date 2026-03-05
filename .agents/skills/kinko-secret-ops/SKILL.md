---
name: kinko-secret-ops
description: Use when users want to manage encrypted environment variables with the kinko CLI, including init/unlock, shared vs repo scope, set/get/show, export/import, and exec-based runtime injection.
allowed-tools: Read, Write, Glob, Grep, Bash
---

# Kinko Secret Ops

Operate `kinko` safely for end users.

## When to Apply

Apply this skill when users ask to:
- Initialize or unlock a `kinko` vault
- Set, get, show, or delete secrets
- Work with shared and repository-specific scopes
- Export/import `.env`-style assignments
- Run commands with secrets via `kinko exec`
- Diagnose lock/keychain/TTY safety errors

## Operating Rules

- Never print plaintext secret values unless the user explicitly requests reveal output.
- Prefer `kinko exec` for runtime injection instead of persistent shell export.
- Treat `--force` as high risk and explain why it is needed before using it.
- Respect scope precedence: repository scope (`--profile` + `--path`) overrides shared scope.
- For destructive operations (`delete --all`, `explosion`), require explicit user intent.

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
