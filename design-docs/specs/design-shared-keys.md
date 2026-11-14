# Shared Key Scope Design

This document defines shared key scope semantics for `kinko`.

## Overview

Today, secret keys are stored under a repository-specific scope resolved by:
- `profile`
- `path` (normalized absolute directory)

This design adds a vault-wide `shared` key scope that is independent of repository path.
During runtime resolution and `export`, `shared` keys are combined with repository-specific keys.

## Goals

- Allow registering keys that are common across all repositories in one vault.
- Keep existing repository-specific registration model unchanged.
- Make `export` emit both scopes in deterministic order.
- Resolve duplicate keys safely with repository-specific precedence.
- Label exported blocks with shell-compatible comments to show origin.

## Non-Goals

- Multi-user shared vault semantics across different OS users.
- Cross-vault sync or remote distribution of shared values.
- Introducing wildcard path inheritance.

## Data Model

Vault plaintext object is extended with:

- `shared`: `map[string]string`
- existing `profiles[profile][path]`: unchanged

Conceptual schema:

```text
vaultData {
  shared: map[key]value
  profiles: map[profile]map[path]map[key]value
}
```

Compatibility requirement:
- Existing vaults without `shared` must load with `shared = {}`.

## Resolution Semantics

For resolved context `(profile, path)`:

1. Start with all keys from `shared`.
2. Overlay keys from `profiles[profile][path]`.

Duplicate key rule:
- If the same key exists in both scopes, repository-specific value wins.

This rule applies consistently to:
- `get`
- `show`
- `exec`
- `export`

## Command Interface

### Registration

Add `--shared` to mutation commands:

- `kinko set --shared KEY=VALUE [...]`
- `kinko set-key --shared KEY --value VALUE`

Without `--shared`, behavior remains repository-specific (`profile + path`).

### Deletion

Add `--shared` support for delete flows:

- `kinko delete --shared KEY`
- `kinko delete --shared --all`

Without `--shared`, deletion remains repository-specific.

Shared bulk deletion is protected the same way as repository-scoped bulk deletion:
- `kinko delete --shared --all` asks for destructive confirmation before direct vault password verification in the interactive flow.
- If the user declines shared delete-all confirmation, the command must preserve the existing aborted stdout, must not ask for the vault password, and must leave shared data unchanged.
- If the user confirms, the command must verify the vault password before deleting shared keys.
- `--yes` skips only the shared delete confirmation prompt; because confirmation is skipped, password verification is still mandatory before loading, listing, or deleting shared keys.
- Failed password verification must leave stdout empty and shared data unchanged.
- `kinko delete --shared KEY` remains a single-key delete and does not require the extra bulk-delete password verification step.

### Movement Between Local and Shared Scopes

Add explicit directional move commands:

- `kinko move local-to-shared KEY`
- `kinko move shared-to-local KEY`

These commands are intended for scope maintenance when the user decides that a value currently belongs in the other existing scope.
They do not introduce a new vault structure and do not alter merge precedence rules.

Movement semantics:
- A move operates on exactly one key.
- The source value is copied into the destination scope and then deleted from the source scope in one persisted vault mutation.
- The command requires an unlocked session and the normal mutation lock.
- The destination must not already contain the key unless `--overwrite` is specified.
- If source lookup, destination conflict checking, confirmation, or persistence fails, both scopes remain unchanged.
- Values are never printed, including in confirmation prompts and error messages.

Direction-specific rules:
- `local-to-shared` reads from `profiles[profile][path]` and writes to `shared`.
- `shared-to-local` reads from `shared` and writes to `profiles[profile][path]`.
- `shared-to-local` may create the selected profile/path map only after source and conflict checks pass.
- `local-to-shared` must not create an empty local scope when the source key is missing.

Confirmation:
- Interactive mode asks for confirmation because a successful move deletes the source key.
- `--yes` / `-y` skips only the confirmation prompt.
- Direct password re-entry is not added for these single-key moves; they follow the existing single-key `set` and `delete` authorization model.

## Export Format

`export` outputs scope-separated blocks:

1. shared block
2. repository-specific block

Each block starts with a shell comment line indicating origin.

Comment style:
- `posix/bash/zsh/sh`: `# ...`
- `fish`: `# ...`
- `nu/nushell`: `# ...`

Example (`posix`):

```bash
# shared keys
export API_BASE_URL='https://example.com'
export TOKEN='shared-token'
# repository-specific keys (profile=default path=/work/repo-a)
export TOKEN='repo-a-token'
export DB_URL='postgres://...'
```

Because repository-specific block is emitted later, shell evaluation naturally preserves precedence (`TOKEN=repo-a-token`).

## Import Compatibility

Since export now contains comments, import parser requirements are:

- Ignore empty lines.
- Ignore comment lines starting with `#`.
- Continue to parse shell assignment lines as before.

## Security and UX Considerations

- Scope comments improve reviewability without exposing additional secret material.
- Key conflict behavior is explicit and deterministic.
- Existing sensitive output guardrails (`--force`, confirmation behavior) remain unchanged.

## Testing Strategy (Design-Level)

Required categories:

- Backward compatibility load for vaults without `shared`.
- `set`/`set-key` with and without `--shared`.
- `delete` with `--shared` (single key and `--all`).
- `move local-to-shared` and `move shared-to-local` success paths.
- Move source-missing failures with no destination creation.
- Move destination-conflict failures and `--overwrite` success paths.
- Move confirmation decline and `--yes` prompt bypass behavior.
- Move scope isolation: unrelated shared keys, local keys, profiles, and paths remain unchanged.
- Resolution merge correctness across commands (`get/show/exec/export`).
- Duplicate key precedence (repo-specific over shared).
- Export block ordering and comment presence per shell renderer.
- Import parsing compatibility for commented export output.

## References

See:
- `design-docs/specs/command.md`
- `design-docs/specs/architecture.md`
