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

## Release-Diff Remediation Notes

Archived summary:
- Runtime files should be split along cohesive package-internal command/runtime
  boundaries before any Go source file reaches the project size limit.
- Release archives and `dist/release/SHA256SUMS` must use one consistent
  tracked-artifact policy per commit and be verified together.
- Preserve CLI behavior, public package surface, prompt order, and error
  behavior when applying release-diff remediation.

## Use Cases

## Folder Vault Research Notes

Comparable tools:
- `gocryptfs`: closest Linux backend fit because it separates a cipher
  directory from a plaintext FUSE mountpoint and supports command-line
  operation.
- macOS encrypted sparsebundle/disk images via `hdiutil`: best macOS default
  because it uses built-in OS disk image support and avoids macFUSE as an
  initial dependency.
- Cryptomator and VeraCrypt: similar vault/container UX, but heavier as kinko
  embedded CLI backends.
- sandbox tools such as macOS sandboxing or containers: relevant for future
  process visibility boundaries, but separate from encrypted-at-rest folder
  storage.

Design implication:
- kinko should not implement a custom encrypted filesystem.
- The initial feature should wrap stable OS/tool backends behind a small Go
  interface and keep lifecycle behavior conservative.
- Automatic unmount is feasible without a daemon when `kinko folder unlock`
  owns a foreground mount process, but TTL, crash cleanup, sleep/wake handling,
  and retrying busy mounts would require a separate watcher/LaunchAgent design.

Security wording:
- "Unlocked only in this terminal" is an ergonomic lifecycle goal, not a strict
  OS access-control guarantee for ordinary mounts.
- Stronger isolation requires a future sandboxed `kinko run` mode that limits
  which paths a child process can read.

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

## Code and Specification Review (2026-07)

A full review of the specs and Go implementation was performed in July 2026,
covering correctness defects, spec/implementation divergences, security
observations, folder-vault lifecycle gaps, and code-quality improvements.

Findings are numbered `F-01`..`F-35` with severities and a prioritized
remediation plan. Completed remediation now covers P0 module/fish/backup CLI
contracts, P1 folder-vault backup/explosion/removal/lifecycle lock items, and
P2 crypto/key-handling hardening for legacy session metadata, backup password
wording, AEAD context binding, macOS `hdiutil` invocation, and password-change
keychain cleanup, and P3 spec/command reconciliation for unlock timeout,
bootstrap data-dir resolution, command-surface docs, flag tables, export
examples, permission-scope wording, data-model/session architecture, all-scopes
authorization, import input exclusivity, and folder relocation behavior.
Remaining work is the P4 UX, diagnostics, parser behavior, and maintainability
findings.

Detailed findings: `design-docs/specs/design-review-findings-2026-07.md`

---
