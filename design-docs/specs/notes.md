# Design Notes

This document contains research findings, investigations, and miscellaneous design notes for `kinko`.

## Overview

Notable items that do not fit into architecture or command specs.

---

## Threat Model Notes (MVP)

### In-Scope Threats

- Accidental secret leakage via git commit of `.env`-style files
- Secret leakage via terminal history, logs, or CI output
- Secret leakage caused by copy/paste and overly permissive command output
- LLM/agent-assisted accidental disclosure during development workflows

### Out-of-Scope Threats

- Root or kernel-level host compromise
- Same-UID malicious process inspection (`/proc/<pid>/environ`, ptrace)
- Hardware or firmware-level compromise

Interpretation:
- `kinko` is designed as a strong "accident prevention" and "at-rest encryption" tool.
- It is not a full host-compromise-resistant secret system.

## Web3 Wallet-Inspired Design Principles

`kinko` should explicitly borrow proven local key management patterns from web3 wallets:

- Use a randomly generated data key as the root encryption key for secret payloads.
- Never derive vault payload encryption directly from mutable user password alone.
- Enable credential reset by re-wrapping data key, not by re-encrypting all user data from scratch.


## Why `lock/unlock` is still useful

Even with local attacks out of scope, lock/unlock reduces routine exposure by:
- requiring explicit user intent before value retrieval
- shrinking plaintext lifetime in memory
- creating safer defaults for interactive and scripted usage

## Plaintext Config Decision Rationale

Question: Is plaintext config in `~/.config/kinko` acceptable?

Answer: No for primary config. Primary config must be encrypted at rest.

Rationale:
- User requirement is encrypted-at-rest storage for both secrets and config.
- Keep only minimal bootstrap metadata in plaintext.
- All operational config should be stored in encrypted config payload.

## Command Surface Refinement Notes

Required command families for MVP:
- Lifecycle: `init`, `lock`, `unlock`, `status`
- Secret CRUD: `set`, `get`, `show`, `delete`
- Execution: `export`, `exec`
- Operator UX: `tui`, `doctor`, `config`

Deferred families:
- remote sync backends
- team collaboration and ACL management
- plugin runtime

## Safety UX Defaults

- Never print secret values in logs.
- `get` should default to masked output unless `--reveal` is passed.
- `show` should default to masked output unless `--reveal` is passed.
- `export` output should be shell-safe and avoid debug noise on stdout.
- `tui` should mask values by default with explicit reveal action.
- `exec` is the recommended default execution path for runtime usage.

Guardrails:
- Require TTY by default.
- Require explicit confirmation when output target is terminal.
- Refuse by default on pipe/redirection unless `--force` is provided.

## Confirmed Decisions

- Shared unlock across processes is required.
- Path lookup must be exact path-only matching.
- Export output must be assignment-only on stdout.
- Import parse errors must not include values.
- Import confirmation must show keys only by default.
- Import supports same shell set as export (`posix`, `bash`, `zsh`, `sh`, `fish`, `nu`/`nushell`).
- `show/get` are masked by default and need `--reveal`.
- Primary config is encrypted; TUI and CLI can edit config via decrypt/re-encrypt flow.

## Use Cases

### Use Case 1: direnv-managed development shell

Scenario:
- Developer enters a project directory with `.envrc`.
- `.envrc` runs `kinko export <shell>` to populate project secrets dynamically.

Expected behavior:
- If unlocked, `kinko` emits exports and direnv loads them.
- If locked, `kinko` exits with locked status code and clear unlock guidance.
- No plaintext secret file is created in repository tree.
- Because direnv is non-interactive, it should use `--force --confirm=false`.

---
