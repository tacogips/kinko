# Command Design

This document describes CLI command interface design specifications for `kinko`.

## Overview

`kinko` is a local-first encrypted environment variable manager for development workflows.
It stores secrets in encrypted local storage and injects values into child process environments or shell export output only when unlocked.

---

## Command Principles

- Default profile: `default`
- Default path scope: current working directory (`$PWD`)
- Locked-by-default model: most read/export operations require an unlocked session
- Non-persistent plaintext: avoid writing decrypted values to disk
- Scriptable but safe defaults: machine-readable output should require explicit opt-in
- `exec` is the default recommended runtime path; `export` is convenience mode

## Subcommands

### `kinko init`

Initialize local vault and config files.

Examples:

```bash
kinko init
kinko init --kinko-dir ~/.local/kinko --config ~/.config/kinko/bootstrap.toml
```

### `kinko lock`

Immediately lock the current kinko session.

Examples:

```bash
kinko lock
```

### `kinko unlock`

Unlock the vault for a bounded session. Prompts for passphrase (or OS-backed auth in future mode).

Examples:

```bash
kinko unlock
kinko unlock --timeout 15m
```

### `kinko password change`

Change the vault password by re-wrapping the existing DEK with a new password-derived KEK.
This command does not re-encrypt all vault secret payloads.

Examples:

```bash
kinko password change
printf '%s\n%s\n' "$OLD_PASSWORD" "$NEW_PASSWORD" | kinko password change --current-stdin --new-stdin
exec 3<<<"$OLD_PASSWORD"
exec 4<<<"$NEW_PASSWORD"
kinko password change --current-fd 3 --new-fd 4
```

Security behavior:
- Requires current password verification before any write.
- Requires new password confirmation in interactive mode.
- On success, vault transitions to globally locked state and invalidates active unlocked sessions.
- Non-interactive mode requires paired `--current-fd/--new-fd` (preferred) or `--current-stdin/--new-stdin`.
- Password input from command arguments or environment variables is not supported.

Exit codes:
- `0`: success
- `10`: current password authentication failed
- `11`: new password policy validation failed
- `12`: lock conflict / concurrent mutation in progress
- `13`: persistence / I/O failure
- `14`: metadata/KDF parameters rejected by safety validation

### `kinko status`

Show lock state, active timeout, data dir, and current path/profile resolution.

### `kinko backup <directory>`

Create a password-locked ZIP archive in the specified destination directory.
The archive contains the vault persistence artifacts needed to restore stored data, plus the bootstrap config file when present.

Examples:

```bash
kinko backup ./backups
printf '%s\n' "$KINKO_PASSWORD" | kinko backup ./backups --current-stdin
kinko backup ./backups --current-fd 3
```

Behavior:
- Requires the current password even if the vault is already unlocked.
- Does not require an active unlocked session; it authenticates directly against persisted vault metadata.
- Creates the destination directory if needed.
- Includes all regular persisted files under the kinko data directory, not only a fixed allowlist of known vault files.
- Produces a ZIP archive that standard PKZIP-compatible readers can open with the current vault password.
- Refuses to embed transient unlock state such as `lock/session.token`.
- Rejects symlinks and other non-regular filesystem entries in the backup source tree, including a symlinked bootstrap config path.
- Refuses destination directories inside the kinko data directory, including destinations that only resolve inside it through symlinks.
- Fails if a concurrent vault mutation is in progress.

Input modes:
- interactive prompt on TTY stdin
- `--current-stdin` for non-interactive stdin
- `--current-fd` for descriptor-based password input
- `--force-tty` to allow line-based interactive prompting when stdin is redirected

### `kinko set <key>=<value> [<key>=<value> ...]`

Create or update one or more secret values under the resolved profile/path scope.
`set` accepts `KEY=VALUE` assignments as arguments or via non-interactive stdin.

Examples:

```bash
kinko set DATABASE_URL='postgres://...'
kinko set OPENAI_API_KEY="$OPENAI_API_KEY" SENTRY_DSN="$SENTRY_DSN"
printf '%s\n' "API_KEY=abc" "DB_URL=postgres://..." | kinko set
```

Shared scope:
- `--shared` writes to vault-wide shared scope instead of repository-specific scope.
- Example: `kinko set --shared API_BASE_URL='https://example.com'`

### `kinko set-key <key> --value <value>`

Set one key at a time using explicit value input (`--value` or stdin).

Shared scope:
- `--shared` writes the key to vault-wide shared scope.

### `kinko get <key>`

Read one secret value from resolved profile/path scope.
Default output is masked unless `--reveal` is explicitly set.

### `kinko show`

Show grouped key-value entries for resolved profile/path scope (current dir + selected profile).
Default output is masked; plaintext requires `--reveal`.

Default grouped sections:
- `# shared`
- `# path=<resolved path>`

Important semantics:
- No merge/override is applied between sections in output mode.
- Duplicate keys may appear in both sections.

Extended scope view:
- `--all-scopes` shows grouped entries for the current profile across all stored paths, plus shared scope.
- Intended as an inspection view; no cross-profile aggregation.
- Detailed format/semantics are documented in the dedicated `show --all-scopes` design spec.

Examples:

```bash
kinko show
kinko show --profile dev --path .
kinko show --reveal
kinko show --all-scopes
```

### `kinko delete <key>`

Delete a key from resolved profile/path scope.
`kinko delete --all` deletes all keys in the resolved scope.

Shared scope:
- `kinko delete --shared <key>` deletes from shared scope.
- `kinko delete --shared --all` deletes all shared keys.

### `kinko export <shell>`

Resolve profile/path and emit shell-specific export statements.

Supported shell names:
- `posix` (base renderer)
- `bash` (alias of `posix`)
- `zsh` (alias of `posix`)
- `sh` (alias of `posix`)
- `fish`
- `nushell` (alias of `nu`)

Examples:

```bash
eval "$(kinko export bash --profile default --path .)"
eval "$(kinko export zsh --path .)"
kinko export fish --path . | source
kinko export nu --path .
kinko export bash --exclude API_KEY,DB_URL
```

Export-specific flags:
- `--with-scope-comments` (default: true): include `# kinko:scope=...` marker comments
- `--exclude <k1,k2,...>`: exclude selected keys from export output (repeatable)

Security guardrails:
- TTY-only by default for plaintext-affecting export/reveal flows.
- Block pipe/redirection by default unless `--force` is explicitly set.
- Support optional confirmation prompt for TTY (`--confirm` true by default).
- Stdout includes scope comment blocks (`shared`, then repository-specific) and shell assignments.

Scope and precedence:
- Export emits shared keys and repository-specific keys for current `profile` + `path`.
- If the same key exists in both scopes, repository-specific assignment is emitted later and takes precedence.
- Keys listed in `--exclude` are removed from both scopes before rendering.

Detailed design:
- `design-docs/specs/design-export-exclude-keys.md`

### `kinko direnv export [shell]`

Export helper optimized for `direnv` `.envrc` usage.

Behavior:
- defaults shell to `bash`
- resolves scope path from `DIRENV_DIR` when available
  - trims leading `-` from `DIRENV_DIR`
  - if value points to a directory, uses that directory as `path`
  - if value points to a file, uses file parent directory as `path`
  - if value is missing/invalid, falls back to resolved global `--path`
- enforces non-interactive-safe export behavior internally
  - equivalent to `--force --confirm=false`
- supports same export formatting flags:
  - `--with-scope-comments`
  - `--exclude <k1,k2,...>` (repeatable)

Examples:

```bash
eval "$(kinko direnv export)"
eval "$(kinko direnv export bash --exclude AWS_SECRET_ACCESS_KEY)"
```

### `kinko import [shell]`

Parse shell-specific assignment content and import it into the resolved profile/path scope.
This command is the inverse of `kinko export`.

Supported shell names:
- `posix` (base parser)
- `bash` (alias of `posix`)
- `zsh` (alias of `posix`)
- `sh` (alias of `posix`)
- `fish`
- `nushell` (alias of `nu`)

Input:
- `--file <path>`: read content from file
- stdin pipe: read content from stdin when non-interactive
- `--file` and stdin pipe are mutually exclusive
- if stdin is TTY and `--file` is omitted, import fails with usage error
- when stdin pipe is used and interactive confirmation is enabled, prompt input must be read from `/dev/tty`
- if `/dev/tty` is unavailable in piped mode, import must fail with guidance to use `--yes`

Confirmation and safety:
- Import confirmation is required by default.
- Confirmation shows keys only by default.
- `--confirm-with-values` can be used to include values in confirmation output.
- Parse errors must report line/context safely without printing secret values.
- For `--confirm-with-values`, value-bearing confirmation output on `stderr` follows sensitive-output guardrails:
  - non-TTY/redirected `stderr` is blocked unless `--force` is set
  - TTY output requires explicit confirmation

Import-specific flags:
- `--file <path>`: import source file path
- `--yes`: skip import confirmation flow (no prompts, no summary output)
- `--confirm-with-values`: show values in confirmation output (opt-in)
- `--allow-shared`: compatibility flag (shared scope markers are already allowed by default)

Flag precedence:
- `--yes` skips the entire import confirmation flow.
- global `--confirm` does not affect import behavior.

MVP accepted assignment formats:
- `posix`/`bash`/`zsh`/`sh`: `export KEY='value'`
- `fish`: `set -gx KEY 'value';`
- `nu`/`nushell`: `$env.KEY = "value"`

Error redaction contract:
- Parse errors include line number and reason category.
- Parse and validation errors must never include raw values or raw assignment lines.
- Import parser must not reuse raw-input error formatting patterns (for example `%q` with full assignment lines).

Examples:

```bash
kinko import bash --file ./secrets.export
kinko export bash | kinko import bash --yes
kinko import fish --file ./secrets.fish
```

### `kinko exec -- <command...>`

Run a child process with resolved secrets injected into environment variables without exporting to parent shell.

Examples:

```bash
kinko exec --profile dev --path . -- go test ./...
```

### `kinko profile`

Manage profiles.

Subcommands:
- `kinko profile list`
- `kinko profile create <name>`
- `kinko profile delete <name>`
- `kinko profile rename <old> <new>`

### `kinko path`

Manage path-scoped mappings and inspect path resolution.

Subcommands:
- `kinko path list`
- `kinko path show --path <dir>`

### `kinko tui`

Start terminal UI for cross-cutting browse/search across profile/path/keys/metadata.
Values are hidden by default and reveal requires explicit action.
TUI supports encrypted config editing.

### `kinko config`

Read or edit encrypted configuration.

Subcommands:
- `kinko config show`
- `kinko config set <key> <value>`
- `kinko config path`
- `kinko config edit`
- `kinko config export --format toml|json`



Subcommands:

### `kinko doctor`

Run local diagnostics: permissions, lock-state health, config validity, vault integrity.

## Global Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--profile` | string | `default` | Profile name |
| `--path` | string | current directory | Logical path scope for key lookup |
| `--kinko-dir` | string | `~/.local/kinko` | Data directory |
| `--config` | string | `~/.config/kinko/bootstrap.toml` | Bootstrap config path |
| `--json` | bool | `false` | JSON output where supported |
| `--no-color` | bool | `false` | Disable ANSI output |
| `--verbose` | bool | `false` | Verbose diagnostic logs (never print secret values) |
| `--timeout` | duration | command-specific | Unlock session duration |
| `--reveal` | bool | `false` | Show plaintext values for `get`/`show` |
| `--shell` | string | command-specific | Explicit shell renderer selection where applicable |
| `--force` | bool | `false` | Override non-TTY / redirection guardrails |
| `--confirm` | bool | `true` | Require confirmation on sensitive TTY output |

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `KINKO_PROFILE` | No | `default` | Default profile override |
| `KINKO_PATH` | No | current directory | Default path override |
| `KINKO_DATA_DIR` | No | `~/.local/kinko` | Data directory override |
| `KINKO_CONFIG` | No | `~/.config/kinko/bootstrap.toml` | Bootstrap config override |
| `KINKO_UNLOCK_TIMEOUT` | No | `15m` | Auto-lock timeout fallback |
| `KINKO_ASKPASS` | No | unset | External passphrase command helper |

## Integration Use Cases

### Use Case 1: direnv integration

Goal:
- Run `kinko` inside `.envrc` and load secrets into direnv-managed environment.

Example `.envrc`:

```bash
export KINKO_PROFILE=default
KINKO_SCOPE_DIR="${DIRENV_DIR#-}"
export KINKO_DATA_DIR="${KINKO_SCOPE_DIR}/.direnv/kinko"
eval "$(kinko direnv export)"
```

Operational notes:
- `kinko direnv export` output remains machine-parseable export assignments.
- `direnv` is non-interactive; the command applies safe non-interactive defaults internally.
- `direnv` users should run `kinko unlock` before entering directory (or use short-lived unlock flow).

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Command failure (includes usage/argument errors unless mapped as `cliError`) |
| 10 | Authentication failed (command-specific `cliError`) |
| 11 | Policy validation failed (command-specific `cliError`) |
| 12 | Lock conflict / concurrent mutation (command-specific `cliError`) |
| 13 | Persistence / I/O failure (command-specific `cliError`) |
| 14 | Metadata/KDF validation failure (command-specific `cliError`) |

Implementation note:
- `ExitCode(err)` currently returns `1` for non-`cliError` failures.
- Commands that require specialized exit semantics must wrap errors with `cliError`.

---
