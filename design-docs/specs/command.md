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

## CLI Metadata Source of Truth

- The Cobra runtime command tree is the canonical definition of subcommands, flags, help text, and examples.
- Help output must be generated from that same command tree rather than maintained in a second manual structure.
- New commands or flags are incomplete until both execution and `--help` output are covered by runtime-level regression tests.
- Duplicate command metadata layers are intentionally avoided because they drift independently and silently hide missing commands.
- The public command surface must remain explicit. Framework-provided utility commands such as Cobra's default `completion` command are disabled unless they are intentionally designed, documented, and tested as part of kinko itself.
- Standard help behavior may remain provided by Cobra, but it must not be used to smuggle undocumented product commands into the CLI surface.
- Help-only parent commands accept zero positional arguments. Unsupported subcommands or stray positional arguments must fail with a non-zero result rather than silently falling back to help output.

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
kinko unlock --timeout 9h
```

Behavior:
- Default unlock timeout is `9h`.
- `--timeout` is the supported timeout override. Encrypted config does not
  currently provide an unlock-timeout source.

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
- New password validation is limited to the shared password sanitation rules plus rejection of unchanged passwords.

Exit codes:
- `0`: success
- `10`: current password authentication failed
- `11`: new password policy validation failed
- `12`: lock conflict / concurrent mutation in progress
- `13`: persistence / I/O failure
- `14`: metadata/KDF parameters rejected by safety validation

### `kinko status`

Show lock state, active timeout, data dir, and current path/profile resolution.

### `kinko explosion`

Permanently remove kinko vault persistence after password re-entry and explicit
confirmation.

Folder-vault interaction:
- A safe root `folders/` directory is allowed in the data-dir layout.
- The command refuses to proceed while any registered folder is mounted.
- Confirmed explosion removes root `folders/` storage together with the core
  vault files.

### `kinko folder`

Manage project-scoped encrypted folder vaults.

Parent behavior:
- `kinko folder` is a help-only command and accepts no positional arguments.
- Folder commands resolve `--profile` and `--path` the same way as secret
  commands.
- Folder commands require initialized kinko metadata and an unlocked kinko
  session because folder registrations are stored in encrypted config.

#### `kinko folder add <name>`

Register `<name>` as an encrypted folder vault under the current resolved path.

Examples:

```bash
kinko folder add private
kinko --profile dev --path . folder add agent-work
```

Behavior:
- `<name>` must be one relative path element.
- `<name>` must not be empty, `.`, `..`, start with `-`, contain control
  characters, or contain path separators.
- The plaintext mountpoint is `<resolved-path>/<name>`.
- The command fails if the mountpoint already exists as a non-directory.
- The command creates encrypted folder metadata in the encrypted config payload.
- The command creates backend storage under the kinko data directory.
- On macOS, backend storage is an encrypted sparsebundle created with a
  current fixed default capacity of `1g`; a user-selectable `--size` flag is
  future work.
- The command adds `<name>/` to `.gitignore` in the resolved path when absent.
- Commented `.gitignore` rules and negated rules do not count as active
  protection for the folder mountpoint.
- A symlinked `.gitignore` is rejected instead of followed.
- If encrypted registration persistence fails after `.gitignore` is updated,
  the previous `.gitignore` state is restored.
- The command does not mount the folder.
- The registration and derived folder secret are bound to the resolved
  profile/path/name. Moving or renaming the project directory requires a future
  reattach/move workflow.

Output:

```text
folder added: private
```

#### `kinko folder unlock <name>`

Mount the encrypted folder so the plaintext directory appears in the project.

Examples:

```bash
kinko folder unlock private
```

Behavior:
- Requires an unlocked kinko session.
- Derives the backend secret from kinko key material and folder identity.
- Fails if the registered backend storage is missing instead of creating a new
  empty vault.
- Creates the mountpoint directory immediately before mounting when needed.
- Uses the OS default backend: `hdiutil` on macOS. Linux is intentionally
  unavailable for the current release and returns an unsupported-platform
  backend error.
- Fails if the folder has not been registered.
- Fails rather than mounting over unrelated content.
- Refuses to take ownership of an already-mounted folder.
- Serializes the status check and mount transition with a folder-scoped
  lifecycle lock, but does not hold that lock for the full foreground mount
  lifetime.
- Waits in the foreground after mounting and soft-unmounts when the command
  exits, including interrupt or terminate.
- A later `kinko lock` while this command is running does not force-unmount the
  folder; the running folder command remains responsible for cleanup.

Output:

```text
folder unlocked: private
path: ./private
holding folder unlock; send interrupt or terminate to lock: private
```

After soft unmount, stdout includes a final lock message.

#### `kinko folder lock <name>`

Soft-unmount the plaintext folder.

Examples:

```bash
kinko folder lock private
```

Behavior:
- Performs normal detach/unmount only.
- Requires an unlocked kinko session to read encrypted folder registration
  metadata. If `kinko lock` was already run while a foreground `folder unlock`
  owner is active, exit or interrupt that owner process to trigger cleanup.
- Does not force detach busy filesystems.
- Serializes status-changing lock/unmount work with a folder-scoped lifecycle
  lock.
- If the backend reports busy, the command fails with guidance and leaves the
  mount intact so the user can close files and retry `kinko folder lock <name>`.
- Does not infer ownership of an existing mountpoint directory. Directory
  cleanup is performed by the foreground `folder unlock` owner only when that
  command created the mountpoint for its own mount lifecycle.

#### `kinko folder remove <name>`

Remove a folder registration from encrypted config and delete its encrypted
backend storage by default.

Examples:

```bash
kinko folder remove private
kinko folder remove private --yes
kinko folder remove private --keep-storage
```

Behavior:
- Requires an unlocked kinko session.
- Refuses to remove a folder while the backend reports it as mounted.
- Removes the encrypted folder registration from config.
- Deletes encrypted storage under the kinko data directory unless
  `--keep-storage` is set.
- Prompts before deleting encrypted storage unless `--yes` is set.
- Serializes removal with folder unlock/lock status-changing operations through
  the folder-scoped lifecycle lock.

Output:

```text
folder removed: private
```

#### `kinko folder status [name]`

Show configured folder vaults and mount state.

Examples:

```bash
kinko folder status
kinko folder status private
```

Default text output lists names and states. JSON output can be added in a later
phase once the status schema is stable.

#### `kinko folder path <name>`

Print the plaintext mountpoint path for a currently mounted folder.

Examples:

```bash
kinko folder path private
```

Behavior:
- Succeeds only when the folder is registered and mounted.
- Prints the resolved mountpoint path.
- Does not unlock or mount by itself.

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
- Excludes root `folders/` encrypted folder-vault storage. Folder vault backup
  is intentionally out of scope until a streaming/ZIP64 folder backup design
  exists.
- Produces a ZIP archive that standard PKZIP-compatible readers can open with the current vault password.
- Uses traditional PKZIP-compatible password protection for interoperability;
  treat the backup password as an access-control convenience, not the primary
  confidentiality boundary. The encrypted vault/config blobs remain the primary
  cryptographic boundary.
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

Value normalization:
- `--value` preserves the provided argument exactly.
- stdin value mode reads one line and trims surrounding whitespace.

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
- Requires password verification before any output because it may display scopes outside the current directory, including when values are masked.
- A verified unlocked session alone is not enough for this command mode; the user must re-enter the vault password for the cross-scope display.
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

Bulk delete authorization:
- `kinko delete --all` and `kinko delete --shared --all` ask for destructive confirmation before direct vault password verification in the interactive flow.
- If the user declines interactive confirmation, the command must preserve the existing aborted stdout, must not prompt for the vault password, and must leave vault data unchanged.
- If the user confirms, the command must verify the vault password before deleting keys.
- `--yes` skips only the destructive confirmation prompt; because there is no confirmation step, password verification remains required before loading, listing, or deleting target keys.
- Password prompts and authentication errors are written to stderr.
- Failed or canceled password verification must write no stdout and must leave vault data unchanged.
- Single-key delete behavior is unchanged and does not add this extra password verification requirement.

Shared scope:
- `kinko delete --shared <key>` deletes from shared scope.
- `kinko delete --shared --all` deletes all shared keys.

### `kinko copy local-to-local --from-path <dir> <key|*>`

Copy one key or every key from another local path scope in the selected profile into the current local profile/path scope.
The operation preserves source values and does not expose plaintext values in output.

Examples:

```bash
kinko copy local-to-local --from-path /work/project-a GITHUB_TOKEN
kinko copy local-to-local --from-path /work/project-a '*'
kinko copy local-to-local --from-path /work/project-a '*' --overwrite
```

Behavior:
- Source scope is the selected `profile` plus the normalized `--from-path` directory.
- Destination scope is the resolved current `profile` plus `path`.
- The key argument must be exactly one environment key or `*`.
- `*` selects all keys currently present in the source scope.
- The command requires an unlocked session and the normal vault mutation lock.
- If any selected destination local key already exists, the command fails without changing either scope unless `--overwrite` is set.
- Wildcard copy checks all selected keys before writing, so conflict failures do not partially copy non-conflicting keys.
- If the source key does not exist, or wildcard source scope is empty, the command fails without creating or changing destination data.
- Source keys are never deleted.
- Output must never include secret values.

### `kinko copy local-to-shared <key|*>`

Copy one key or every key from the current local profile/path scope into vault-wide shared scope while preserving the local source.

Examples:

```bash
kinko copy local-to-shared GITHUB_TOKEN
kinko copy local-to-shared '*'
kinko copy local-to-shared '*' --overwrite
```

Behavior:
- Source scope is the resolved current `profile` plus `path`.
- Destination scope is vault-wide `shared`.
- The key argument must be exactly one environment key or `*`.
- The command requires an unlocked session and the normal vault mutation lock.
- Destination shared keys are not replaced unless `--overwrite` is set.
- Wildcard copy fails without partial writes if any selected destination key already exists and `--overwrite` is absent.
- Source keys are never deleted.
- Output must never include secret values.

### `kinko copy shared-to-local <key|*>`

Copy one key or every key from vault-wide shared scope into the current local profile/path scope while preserving the shared source.

Examples:

```bash
kinko copy shared-to-local GITHUB_TOKEN
kinko copy shared-to-local '*'
kinko copy shared-to-local '*' --overwrite
```

Behavior:
- Source scope is vault-wide `shared`.
- Destination scope is the resolved current `profile` plus `path`.
- The key argument must be exactly one environment key or `*`.
- The command requires an unlocked session and the normal vault mutation lock.
- Destination local keys are not replaced unless `--overwrite` is set.
- Wildcard copy fails without partial writes if any selected destination key already exists and `--overwrite` is absent.
- Source keys are never deleted.
- Output must never include secret values.

### `kinko move local-to-shared <key>`

Move one key from the current local profile/path scope into vault-wide shared scope.
The operation transfers the existing plaintext value without exposing it in output.

Examples:

```bash
kinko move local-to-shared GITHUB_TOKEN
kinko move local-to-shared GITHUB_TOKEN --yes
kinko move local-to-shared GITHUB_TOKEN --overwrite --yes
```

Behavior:
- Source scope is the resolved current `profile` + `path`.
- Destination scope is vault-wide `shared`.
- Exactly one key is accepted per invocation.
- The command requires an unlocked session and the normal vault mutation lock.
- If the destination shared key already exists, the command fails without changing either scope unless `--overwrite` is set.
- If the source key does not exist in the current local scope, the command fails without creating or changing shared data.
- On success, the destination receives the same value and the source local key is deleted in the same persisted vault mutation.
- Missing or empty local path scopes are not created by this direction.
- Output must never include the secret value.

Confirmation:
- Interactive mode asks for confirmation before deleting the source key.
- `--yes` / `-y` skips only this confirmation prompt.
- No additional direct password re-entry is required beyond the existing unlocked-session requirement because this is a single-key mutation, matching `set` and single-key `delete` semantics.

### `kinko move shared-to-local <key>`

Move one key from vault-wide shared scope into the current local profile/path scope.
The operation transfers the existing plaintext value without exposing it in output.

Examples:

```bash
kinko move shared-to-local GITHUB_TOKEN
kinko move shared-to-local GITHUB_TOKEN --yes
kinko move shared-to-local GITHUB_TOKEN --overwrite --yes
```

Behavior:
- Source scope is vault-wide `shared`.
- Destination scope is the resolved current `profile` + `path`.
- Exactly one key is accepted per invocation.
- The command requires an unlocked session and the normal vault mutation lock.
- If the destination local key already exists, the command fails without changing either scope unless `--overwrite` is set.
- If the source shared key does not exist, the command fails without creating or changing local data.
- On success, the local destination receives the same value and the shared source key is deleted in the same persisted vault mutation.
- The local profile/path map is created only after the source key is found and destination conflict checks pass.
- Output must never include the secret value.

Confirmation:
- Interactive mode asks for confirmation before deleting the source key.
- `--yes` / `-y` skips only this confirmation prompt.
- No additional direct password re-entry is required beyond the existing unlocked-session requirement because this is a single-key mutation, matching `set` and single-key `delete` semantics.

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
eval "$(kinko export bash --profile default --path . --force --confirm=false)"
eval "$(kinko export zsh --path . --force --confirm=false)"
kinko export fish --path . --force --confirm=false | source
kinko export nu --path . --force --confirm=false
kinko export bash --exclude API_KEY,DB_URL --force --confirm=false
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
- Posix-like parsers deliberately do not expand `$VAR` inside double-quoted
  input. This is safer than shell evaluation and keeps import parsing
  deterministic.
- Quoted shell values preserve leading and trailing whitespace inside the
  quotes. Parser whitespace around syntax tokens is ignored.

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

Implemented subcommands:
- `kinko profile list`

### `kinko path`

Manage path-scoped mappings and inspect path resolution.

Implemented subcommands:
- `kinko path prune-missing`

### `kinko path prune-missing`

Preview or prune stored local path scopes whose directories no longer exist.
This command is a local data maintenance command for repository/path-scoped vault entries only.
It never deletes vault-wide shared scope data.

Examples:

```bash
kinko path prune-missing
kinko path prune-missing --profile dev
kinko path prune-missing --all-profiles
kinko path prune-missing --yes
kinko path prune-missing --json
```

Scope selection:
- Default target is the selected profile (`--profile`, default `default`).
- `--all-profiles` scans path scopes in every stored profile.
- `--all-profiles` must not be combined with an explicitly supplied inherited `--profile`.
- Shared scope is out of scope and must not be listed as a prune candidate or deleted.
- `--path` is ignored for this command because the command operates over stored path scopes instead of the current resolved path.

Safety and authorization:
- Default mode is preview-only and deletes nothing.
- Destructive pruning requires explicit `--yes`; no mutation occurs without it.
- The command requires direct vault password verification before any profile/path-scope output, including preview output, because stored paths and key counts are vault metadata.
- A previously unlocked session is not sufficient for the cross-scope enumeration or destructive pruning authorization.
- `--yes` confirms only the prune operation; it does not skip or weaken password verification.

Output:
- Text preview reports each stale path scope as `profile`, `path`, and key count.
- Successful destructive output reports the same pruned profile/path scopes and a final total.
- `--json` emits machine-readable objects with `mode` (`preview` or `prune`), `profile`, `path`, `keyCount`, and aggregate totals.
- Secret values are never printed. Key names are not required for this command and should be omitted from default output.

Filesystem matching:
- A stored path scope is stale only when the normalized absolute stored path no longer exists as a directory at scan time.
- Existing files, broken symlinks, permission-denied paths, relative stored paths, and path normalization collisions are not pruned automatically; they are reported as skipped diagnostics.
- Symlinks are evaluated by their directory existence result after resolution. A symlink that resolves to an existing directory is not stale.

### `kinko config`

Read or edit encrypted configuration.

Implemented subcommands:
- `kinko config show`
- `kinko config set <key> <value>`

### Planned Commands

These commands are design ideas and are not shipped behavior yet:
- `kinko tui`
- `kinko config path`
- `kinko config edit`
- `kinko config export --format toml|json`
- `kinko profile create <name>`
- `kinko profile delete <name>`
- `kinko profile rename <old> <new>`
- `kinko path list`
- `kinko path show --path <dir>`

### `kinko doctor`

Run local diagnostics: permissions, lock-state health, config validity, vault integrity.

Current diagnostics include:
- Warn when `meta.v1.json` has no `session_key_source`, which identifies
  pre-migration session metadata. Users should unlock once with a current
  release, rotate the vault password, and treat old `meta.v1.json` backups as
  sensitive.

## Persistent Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--profile` | string | `default` | Profile name |
| `--path` | string | current directory | Logical path scope for key lookup |
| `--kinko-dir` | string | `KINKO_DATA_DIR`, bootstrap `kinko_dir`, or `~/.local/kinko` | Data directory |
| `--config` | string | `~/.config/kinko/bootstrap.toml` | Bootstrap config path |
| `--keychain-preflight` | enum | `required` | Keychain preflight mode: `required`, `best-effort`, or `off` |
| `--force` | bool | `false` | Override non-TTY / redirection guardrails |
| `--confirm` | bool | `true` | Require confirmation on sensitive TTY output |

## Command-Local Flags

| Command | Flags |
|---------|-------|
| `unlock` | `--timeout` |
| `get`, `show` | `--reveal` |
| `show` | `--all-scopes` |
| `path prune-missing` | `--all-profiles`, `--yes`/`-y`, `--json` |
| `backup` | `--dest-path`, `--current-stdin`, `--current-fd`, `--force-tty` |
| `export`, `direnv export` | optional shell argument, `--shared-only`, `--with-scope-comments`, `--exclude` |
| `import` | optional shell argument, `--file`, `--yes`/`-y`, `--confirm-with-values`, `--allow-shared` |
| `exec` | `--all`, `--env` |
| `password change` | `--current-stdin`, `--new-stdin`, `--current-fd`, `--new-fd`, `--force-tty` |
| `folder remove` | `--keep-storage`, `--yes`/`-y` |

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `KINKO_PROFILE` | No | `default` | Default profile override |
| `KINKO_PATH` | No | current directory | Default path override |
| `KINKO_DATA_DIR` | No | `~/.local/kinko` | Data directory override |
| `KINKO_CONFIG` | No | `~/.config/kinko/bootstrap.toml` | Bootstrap config override |
| `KINKO_KEYCHAIN_PREFLIGHT` | No | `required` | Keychain preflight mode override |

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
- `ExitCode(err)` returns child process exit status for `kinko exec` when the
  child command exits non-zero.
- Non-`cliError` command failures otherwise map to `1`.
- Commands that require specialized exit semantics must wrap errors with
  `cliError`.

Current command-specific structured mappings:

| Command | Condition | Exit code |
|---------|-----------|-----------|
| `password change` | Current password authentication failure | `10` |
| `password change` | New password policy failure | `11` |
| `password change` | Mutation lock conflict | `12` |
| `password change` | Persistence / I/O failure | `13` |
| `password change` | Metadata/KDF validation failure | `14` |
| `unlock` | Current password authentication failure | `10` |
| `unlock` | Invalid arguments / timeout policy failure | `11` |
| `unlock` | Session/keychain persistence / I/O failure | `13` |
| `backup` | Current password authentication failure | `10` |
| `backup` | Mutation lock conflict | `12` |
| `backup` | Invalid destination policy | `11` |
| `backup` | Persistence / I/O failure | `13` |
| `export` | Invalid shell / exclude / sensitive-output policy failure | `11` |
| `export` | Session/vault/output I/O failure | `13` |
| `import` | Invalid shell / input / confirmation policy failure | `11` |
| `import` | Mutation lock conflict | `12` |
| `import` | File/vault/session persistence / I/O failure | `13` |
| `delete --all` | Current password authentication failure | `10` |
| `delete` / `delete --all` | Mutation lock conflict | `12` |
| `delete` / `delete --all` | Persistence / I/O failure | `13` |
| `folder add/unlock/lock/remove/status/path` | Validation / state policy failure | `11` |
| `folder add/unlock/lock/remove/status/path` | Mutation or lifecycle lock conflict | `12` |
| `folder add/unlock/lock/remove/status/path` | Storage/backend/config I/O failure | `13` |
| `exec` | Child process exits non-zero | Child exit code |

No additional command-specific structured mappings are currently planned for
this P4 pass.

---
