# Export Exclude Keys Design

This document defines how `kinko export` can exclude specific environment variable keys from export output.

## Overview

`kinko export` currently emits all resolved keys from:
- `shared` scope
- repository-specific scope (`profile` + `path`)

This design adds an explicit exclusion filter so users can suppress selected keys at export time.

## Goals

- Allow users to exclude specific env var keys from `export` output.
- Keep current export format and shell rendering behavior unchanged for non-excluded keys.
- Keep behavior deterministic and script-friendly.
- Preserve existing sensitive output guardrails.

## Non-Goals

- Persisting exclusion settings in vault/config for automatic reuse.
- Wildcard or regex-based exclusion in MVP.
- Changing scope merge precedence rules.

## Command Interface

Primary command remains:

```bash
kinko export [shell] [--with-scope-comments] [--exclude <k1,k2,...>]
```

New flag:
- `--exclude <k1,k2,...>`: comma-separated list of keys to exclude from output.

Rules:
- Key parsing trims whitespace around each comma-separated token.
- Empty tokens are ignored.
- Duplicate keys in exclusion input are deduplicated.
- Each key must pass existing env key validation.
- Unknown keys (not present in resolved export set) are ignored without error.
- Flag may be specified multiple times; all entries are merged.

Examples:

```bash
kinko export bash --exclude API_KEY
kinko export posix --exclude API_KEY,DB_URL
kinko export nu --exclude API_KEY --exclude SENTRY_DSN
```

## Filtering Semantics

1. Resolve export scopes exactly as today (`shared`, then repository-specific).
2. Build exclusion set from `--exclude`.
3. Remove excluded keys from both scopes before rendering.
4. Render remaining keys using existing shell assignment logic.

Precedence behavior remains unchanged for non-excluded duplicate keys:
- repository-specific keys still override shared keys by emission order.

Excluded duplicate key behavior:
- If a key exists in both scopes and is excluded, both assignments are omitted.

## Output Behavior

- Existing `--with-scope-comments` behavior remains unchanged.
- If a scope becomes empty after exclusion, that scope block is omitted (same as current empty-scope behavior).
- No new informational lines are added to stdout/stderr for excluded keys.

## Error Handling

Validation errors:
- Invalid key in `--exclude` returns an error and exits non-zero.

Recommended error format:

```text
invalid --exclude key "1INVALID"
```

Non-errors:
- Excluding keys that do not exist in resolved scopes.
- Repeating the same key in exclusion input.

## Security Considerations

- Exclusion reduces accidental exposure surface in export output.
- No change to TTY/pipe guardrails (`--force`, `--confirm` behavior).
- No secret values are logged in exclusion-related errors.

## Implementation Notes

- Parse `--exclude` as repeatable string input in `runExport`.
- Reuse existing env key validation helper for exclusion tokens.
- Apply filtering before `writeExportBlock`.

Possible helper signatures:

```text
parseExcludeKeys(values []string) (set, error)
filterSecretsByExclusion(in map[string]string, excluded set) map[string]string
```

## Testing Strategy (Design-Level)

Required test categories:
- No exclusion: output unchanged from current behavior.
- Single and multiple key exclusion.
- Repeated `--exclude` usage with merge behavior.
- Invalid key in exclusion input returns error.
- Unknown exclusion key is ignored (no failure).
- Key present in both shared/repo scopes is fully omitted when excluded.
- Scope comment behavior when one or both blocks become empty after exclusion.
- Round-trip sanity: `export --exclude ... | import ...` only imports non-excluded assignments.

## References

See:
- `design-docs/specs/command.md`
- `design-docs/specs/design-shared-keys.md`
