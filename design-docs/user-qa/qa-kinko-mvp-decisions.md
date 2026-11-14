# Kinko MVP Decisions (Confirmed)

This file records confirmed MVP decisions.

## 1. Storage at Rest

Decision:
- Do not keep primary secret/config state in plaintext.
- Use encrypted vault and encrypted config payloads.

## 2. Unlock Session Scope

Decision:
- Shared unlock across processes is required.

Implementation direction:
- Prefer local daemon memory custody for unlocked key material.
- Avoid disk-resident plaintext-equivalent unlock tokens.


Decision:

## 4. Export/Reveal Guardrails

Decision:
- TTY-only reveal/export by default.
- Block pipe/redirection unless `--force`.
- Confirmation prompt enabled by default (can disable explicitly).

## 5. Shell Support Scope

Decision:
- Keep shell set: `posix`, `bash`, `zsh`, `sh`, `fish`, `nu`/`nushell`.
- `bash`/`zsh`/`sh` are aliases to `posix` renderer.

## 6. Recommended Runtime Mode

Decision:
- Documentation defaults to `kinko exec` as safer path.
- `kinko export` is convenience mode.

## 7. Path Resolution

Decision:
- Exact path-only matching for secret lookup.

## 8. Output Contract and Reveal Defaults

Decision:
- `export` stdout must be assignment-only (no comments/logs).
- `show/get` are masked by default and require `--reveal`.

## 9. Config UX

Decision:
- Add config output/edit commands.
- Config can also be changed from TUI.

## 10. Command Surface

Decision:
- `apply` is removed.
- `export` and `exec` are both implemented.

