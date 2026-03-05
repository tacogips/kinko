# Show All Scopes Design

This document defines `kinko show` and `kinko show --all-scopes` grouped-scope behavior.

## Overview

`kinko show` renders grouped sections for one resolved `(profile, path)`:
- `shared`
- repository-specific entries at the resolved path

`kinko show --all-scopes` is a profile-wide multi-scope view:
- include `shared`
- include every repository path scope under the current profile
- do not include other profiles

## Goals

- Provide one-command visibility into all stored keys for the current profile.
- Keep safe defaults (masked output by default).
- Preserve existing reveal guardrails for plaintext output.
- Provide deterministic, grep-friendly text output grouped by scope.

## Non-Goals

- Cross-profile aggregation in a single `show` call.
- Changing secret resolution precedence rules.
- Changing storage schema.

## Command Interface

Commands:

```bash
kinko show [--reveal]
kinko show --all-scopes [--reveal]
```

Scope semantics:
- Default `show` outputs two grouped sections:
  1. shared scope
  2. resolved path scope (`--path`, default current directory)
- `--all-scopes` targets the selected profile only (`--profile`, default `default`).
- Output includes:
  1. shared scope
  2. all repository paths in that profile
- For backward compatibility, extra non-flag positional tokens are ignored (same as existing `show` behavior).

Path semantics:
- `--path` is ignored when `--all-scopes` is set, because path selection is replaced by profile-wide enumeration.

## Output Contract

Output is grouped using shell-comment-style headers:

```text
# profile=<profile>

# shared
KEY_A=****
KEY_B=****

# path=/abs/path/one
KEY_A=****
KEY_C=****

# path=/abs/path/two
KEY_D=****
```

Formatting rules:
- First line is always `# profile=<profile>`.
- Section order is deterministic:
  1. `# shared`
  2. path sections sorted lexicographically by normalized absolute path.
- Keys inside each section are sorted lexicographically.
- Empty sections are omitted except:
  - `# shared` section is still printed (header only) for consistent structure.

Value rendering:
- default: masked (`maskValue` behavior identical to current `show`).
- `--reveal`: plaintext values.

## Security and Guardrails

- `--reveal` with `--all-scopes` must use the same sensitive-output guardrail as current `show --reveal`.
- Non-TTY or redirected output remains blocked unless global override (`--force`) is provided.
- Confirmation flow behavior remains unchanged from existing sensitive output flows.

## Data Access Semantics

For current `profile`:
- Read `shared` map.
- Enumerate all paths from `profiles[profile]`.
- Do not apply merge/override between sections in output mode.
- Normalize each stored path to a cleaned absolute path.
- If a stored path is relative, fail with an error (no partial output).
- If multiple stored path keys normalize to the same absolute path, fail with an error rather than merging sections.

Note:
- This command is an inspection view, not a resolved-runtime view.
- `kinko show` and `kinko show --all-scopes` both preserve scope boundaries in output.

## Error Handling

- If profile has no entries and shared is empty, output still prints:
  - `# profile=<profile>`
  - `# shared`
- If any stored path is relative, `show --all-scopes` fails and reports the offending stored path.
- If two stored path scopes normalize to the same path, `show --all-scopes` fails and reports the colliding stored paths.
- Unknown flag combinations should fail with existing flag parsing behavior.

## Testing Strategy (Design-Level)

- `show --all-scopes` prints `# profile=<profile>` and `# shared` headers.
- Path sections are present for all stored paths in profile and sorted.
- Keys in each section are sorted.
- Values are masked by default.
- `--reveal` prints plaintext and is guarded by sensitive-output policy.
- Default `show` prints `# shared` and `# path=<resolved path>` headers.
- Default `show` can emit duplicate keys across sections when shared/repo scopes overlap.
- Relative stored paths are rejected with a hard error.

## References

See:
- `design-docs/specs/command.md`
- `design-docs/specs/design-shared-keys.md`
