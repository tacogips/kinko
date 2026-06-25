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

Context:
- Review range: `v0.1.2..HEAD` on latest `origin/main`.
- Parent review handoff: `codex-recent-change-quality-loop:riel-codex-recent-change-quality-loop-1781657586-b51d8611:step3-handoff`.
- Blocking findings are limited to `internal/kinko/runtime.go` maintainability and `dist/release/SHA256SUMS` release artifact consistency.

Runtime file organization decision:
- `internal/kinko/runtime.go` must be split along cohesive package-internal runtime boundaries when it reaches or exceeds the Go source file size limit in `.agents/skills/go-coding-standards/SKILL.md`.
- The split must preserve the public package surface, CLI behavior, command output, prompt order, error behavior, and tests.
- Preferred boundaries are command/runtime concerns already present in the package, such as import parsing, export rendering, delete/show behavior, set helpers, backup helpers, and shared scope helpers.
- New files remain in package `kinko`; this is an internal organization change, not an architecture or API change.
- Adapter-specific behavior is not introduced by this remediation.

Release artifact manifest decision:
- `dist/release/SHA256SUMS` is the verification source for tracked files in `dist/release/`.
- The release directory must use one consistent policy per commit:
  - if multiple release versions are tracked, every tracked archive must have a checksum entry; or
  - if only the latest release is represented by the manifest, older tracked archives must be removed in the same change.
- For the current post-`v0.1.2` review, the preferred remediation is to include checksum entries for every tracked `dist/release/kinko_*` archive so the historical baseline artifacts and newer artifacts can be verified together.
- Verification must include `cd dist/release && shasum -a 256 -c SHA256SUMS` and an explicit tracked-file coverage check comparing `git ls-files dist/release` against `SHA256SUMS`.

Rollout constraints:
- Keep fixes scoped to this isolated review worktree.
- Do not touch unrelated local worktrees named in workflow input.
- Preserve current CLI behavior and public package surface.
- Run `gofmt` on moved Go code and verify with `go test ./...`, `go vet ./...`, and review-range diff checks.

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
