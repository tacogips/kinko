# Import Command Design

This document defines the design for `kinko import`, the inverse operation of `kinko export`.

## Overview

`kinko import` reads shell assignment content and upserts secrets into the resolved `profile` + `path` scope.
Input is accepted from a file or from stdin pipe.

The command is designed for migration and synchronization use cases such as:
- `kinko export bash | kinko import bash`
- importing an existing `.env`-like export dump

The design prioritizes value confidentiality:
- confirmation shows keys only by default
- parse errors must never include secret values
- value display is opt-in and treated as sensitive output

## Goals

- Provide `kinko export` inverse behavior with shell-aware parsing
- Support two input sources: `--file` and stdin pipe
- Require explicit user confirmation before mutating vault data
- Support key-only confirmation by default, with optional value display
- Report parse/validation errors safely without exposing values

## Non-Goals

- Partial import with per-line continue-on-error in MVP
- Auto-detection of shell syntax without explicit shell selection
- Importing from process environment directly
- Preserving comments/formatting from source input

## Command Interface

Primary command:

```bash
kinko import [shell] [--file <path>]
```

Supported shell names are identical to `export`:
- `posix` (aliases: `bash`, `zsh`, `sh`)
- `fish`
- `nushell` (alias: `nu`)

Behavior:
- Positional `shell` is optional; default is `posix`.
- `--file <path>` reads import content from the given file.
- Without `--file`, command reads from stdin if stdin is non-interactive.
- If stdin is TTY and `--file` is omitted, command fails with usage error.
- `--file` and piped stdin cannot be combined.

Confirmation behavior:
- Confirmation summary always includes:
  - target profile
  - target path
  - key count
  - key list (sorted)
- Values are hidden by default in confirmation output.
- `--confirm-with-values` enables value display in confirmation summary.
- `--yes` skips import confirmation flow (automation mode).
- When `--confirm-with-values=true`, non-TTY or redirected `stderr` must be blocked unless `--force` is explicitly set.
- When stdin pipe is the import source and interactive confirmation is enabled, prompt input must be read from `/dev/tty`.
- If `/dev/tty` is unavailable in piped mode, fail with guidance to use `--yes`.

Flag precedence:
- `--yes` skips the entire import confirmation flow.
- Global `--confirm` does not affect import behavior.

Examples:

```bash
kinko import bash --file ./secrets.export
cat ./secrets.export | kinko import bash
kinko import fish --file ./fish-secrets.txt --confirm-with-values
kinko export nu --path . | kinko import nu --yes
```

## Parsing Rules

Import parser is shell-specific and expects output compatible with `kinko export`.

### POSIX parser (`posix`, `bash`, `zsh`, `sh`)

Accepted line formats:

```text
KEY=value
export KEY=value
export KEY='value'
export KEY="value"
```

Rules:
- Leading/trailing whitespace around the whole line is allowed.
- Empty lines are ignored.
- `KEY` must pass existing env key validation.
- Single-quoted value parsing supports the export escape sequence for `'`:
  - `'...'"'"'...'` pattern produced by exporter.
- Double-quoted value parsing supports common shell escapes (`\\`, `\"`, `\$`, ``\` ``) and preserves unknown escapes as-is.
- Unquoted values are accepted when they contain no whitespace.
- Any unsupported tokenization is parse error.

### Fish parser (`fish`)

Accepted line format (MVP):

```text
set -gx KEY 'value';
```

Rules:
- Terminal `;` is required for strict round-trip compatibility with exporter output.
- `KEY` must pass env key validation.
- Fish single-quote escaping (`\'`) is supported.

### Nushell parser (`nu`, `nushell`)

Accepted line format (MVP):

```text
$env.KEY = "value"
```

Rules:
- `KEY` must pass env key validation.
- Parse supported double-quoted escapes emitted by exporter:
  - `\\`, `\"`, `\n`, `\r`, `\t`
- Unknown escape sequences are parse errors.

### Duplicate keys

- Within one import payload, last occurrence wins (line-order precedence).
- Confirmation summary shows each key once (final set only).

## Mutation Semantics

- Import performs upsert in resolved `profile` + `path` scope.
- Existing keys are overwritten by imported keys.
- Keys not present in import input remain unchanged.
- Single atomic vault write per command execution.
- On any parse/validation failure, no write occurs.

## Error Handling and Secret Redaction

### Parse error policy

Error message requirements:
- include line number
- include shell parser type
- include high-level reason category
- must not include raw value content

Recommended error format:

```text
import parse error (shell=posix, line=12): invalid single-quote sequence
```

Examples of safe reasons:
- `invalid key syntax`
- `unsupported assignment format`
- `unterminated quoted value`
- `invalid escape sequence`

Forbidden in errors:
- raw assignment line
- parsed/partial value
- quoted secret fragments

### Validation errors

- Invalid env keys fail with key-focused message only (no value echo).
- Input source conflicts (`--file` + pipe) return usage error.
- Empty effective input returns validation error without mutation.

## Confirmation UX

Confirmation output target:
- summary and prompt are written to stderr.
- value-bearing summaries (`--confirm-with-values`) are treated as sensitive stderr output.
- for value-bearing summaries, apply export-like guardrails:
  - if `stderr` is non-TTY and `--force=false`, fail before printing values
  - if `stderr` is TTY and `--confirm=true`, require explicit confirmation before printing values

Prompt:

```text
Import N keys into profile="<profile>" path="<path>"? [y/N]:
```

Default summary (safe):

```text
Planned import:
  shell: posix
  profile: default
  path: /work/project
  keys (3): API_KEY, DB_URL, SENTRY_DSN
```

Value-display summary (opt-in):
- Same as default plus `KEY=<value>` lines.
- Only enabled when `--confirm-with-values=true`.

Non-interactive guidance:
- For pipelines/automation, recommended `--yes`.
- Use `--yes` for command-local non-interactive import execution.

## Exit Behavior

- `0`: import succeeded
- `1`: invalid arguments/usage, parse failure, validation failure, lock/session failure, I/O failure, or aborted confirmation

Note:
- Current CLI exit policy returns `1` for non-`cliError` failures.
- Import should follow this policy unless explicit `cliError` mapping is introduced.

## Data Flow

1. Parse args and resolve input source (`--file` or stdin pipe).
2. Normalize shell name using same resolver as export.
3. Read all input content into memory for deterministic parse + confirm.
4. Parse into `map[key]value` with line-number-aware parser.
5. Build confirmation payload (keys-only or key+value).
6. Run confirmation flow (`--yes` / prompt based on flags).
7. Acquire mutation lock and verify unlocked session.
8. Upsert parsed map into resolved scope.
9. Persist vault atomically.
10. Print success summary without values.

## Implementation Notes

- Introduce a shared confirmation helper for piped mode:
  - behavior: read confirmation response from `/dev/tty` when stdin is consumed as import payload
  - fallback: fail with clear guidance when `/dev/tty` is unavailable
- Avoid reusing parser error patterns that interpolate raw input (for example `%q` with full assignment lines).
- Parser errors must use redaction-safe categories and line numbers only.

## Security Considerations

- No plaintext values written to disk during import.
- Parse and validation errors never echo values.
- Default confirmation is key-only.
- Value display requires explicit opt-in (`--confirm-with-values`) and should be avoided in shared terminals.
- Success output should not include values.

## Testing Strategy (Design-Level)

Required test categories:
- input source selection (`--file`, stdin pipe, invalid combinations)
- parser round-trip compatibility with each exporter format
- parse error redaction (no value leakage)
- duplicate key last-write-wins behavior
- confirmation paths (`--yes`, `--confirm-with-values`)
- mutation atomicity (no write on parse failure)
- overwrite behavior and unchanged non-imported keys
- non-interactive usage with `--yes`

## References

See:
- `design-docs/specs/command.md`
- `design-docs/specs/architecture.md`
- `design-docs/specs/notes.md`
