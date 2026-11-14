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
- Resolution merge correctness across commands (`get/show/exec/export`).
- Duplicate key precedence (repo-specific over shared).
- Export block ordering and comment presence per shell renderer.
- Import parsing compatibility for commented export output.

## References

See:
- `design-docs/specs/command.md`
- `design-docs/specs/architecture.md`
