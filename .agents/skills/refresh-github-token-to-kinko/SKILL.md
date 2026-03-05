---
name: refresh-github-token-to-kinko
description: Use when updating GitHub authentication token scopes and re-registering the token into kinko shared secrets. Handles gh auth refresh, avoiding GITHUB_TOKEN env precedence, writing shared GITHUB_TOKEN, and hash-based verification.
allowed-tools: Read, Bash, Grep, Glob
---

# Refresh GitHub Token To Kinko

Refresh GitHub CLI auth scope and sync the usable token into kinko shared secret `GITHUB_TOKEN`.

## Steps

1. Check current auth source and scopes.
```bash
gh auth status -h github.com
```

2. Refresh GitHub CLI token scopes with browser flow.
```bash
env -u GITHUB_TOKEN gh auth refresh -h github.com -s workflow
```

3. Read token from keyring-backed `gh` auth (not environment override) and write to kinko shared.
```bash
kinko status
kinko set-key GITHUB_TOKEN --shared --value "$(env -u GITHUB_TOKEN gh auth token)"
```

4. Verify equality without printing secret plaintext.
```bash
kinko_sha="$(kinko --force --profile shared get GITHUB_TOKEN --reveal | tr -d '\n' | sha256sum | awk '{print $1}')"
gh_sha="$(env -u GITHUB_TOKEN gh auth token | tr -d '\n' | sha256sum | awk '{print $1}')"
[ "$kinko_sha" = "$gh_sha" ] && echo "sync_ok"
```

## Important Notes

- `GITHUB_TOKEN` environment variable has higher precedence than keyring auth in `gh`.
- Use `env -u GITHUB_TOKEN` when the goal is to use refreshed keyring credentials.
- `kinko get/show --reveal` is sensitive output; use only when necessary and avoid printing values.
