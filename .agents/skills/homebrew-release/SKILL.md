---
name: homebrew-release
description: Use when building, validating, publishing, or tap-rendering Homebrew formula releases for this Go project, including scripts/build-homebrew-release.sh, scripts/render-homebrew-formula.sh, mise Homebrew tasks, GitHub Release assets, and tap formula verification.
---

# Homebrew Release

Use this skill for Formula releases installed with:

```bash
brew tap tacogips/tap
brew install kinko
```

## Release Contract

1. Confirm `internal/build/VERSION` is the intended release version.
2. Run local build and tests.
3. Build Homebrew tarballs with `scripts/build-homebrew-release.sh`.
4. Publish GitHub Release assets only when explicitly requested.
5. Render the formula only after all referenced archives and checksums exist.
6. Update, commit, push, and verify the tap formula after the GitHub Release is
   available.

Expected asset mapping:

| Homebrew platform | Release asset |
| --- | --- |
| macOS Apple Silicon | `kinko-<version>-darwin-arm64.tar.gz` |
| macOS Intel | `kinko-<version>-darwin-x64.tar.gz` |
| Linux ARM64 | `kinko-<version>-linux-arm64.tar.gz` |
| Linux x86_64 | `kinko-<version>-linux-x64.tar.gz` |

## Standard Commands

Build:

```bash
mise run test
mise run build:homebrew -- darwin-arm64 darwin-x64 linux-arm64 linux-x64
```

Render locally:

```bash
version="$(tr -d '[:space:]' < internal/build/VERSION)"
mise run homebrew:formula -- "$version"
```

Render into the default sibling tap:

```bash
version="$(tr -d '[:space:]' < internal/build/VERSION)"
mise run homebrew:tap-formula -- "$version"
```

## Publishing

Before rendering a public formula, ensure the release assets exist:

```bash
version="$(tr -d '[:space:]' < internal/build/VERSION)"
gh release view "v${version}" --repo tacogips/kinko
```

If publishing is explicitly requested:

```bash
version="$(tr -d '[:space:]' < internal/build/VERSION)"
gh release upload "v${version}" \
  "dist/homebrew/kinko-${version}-darwin-arm64.tar.gz" \
  "dist/homebrew/kinko-${version}-darwin-x64.tar.gz" \
  "dist/homebrew/kinko-${version}-linux-arm64.tar.gz" \
  "dist/homebrew/kinko-${version}-linux-x64.tar.gz" \
  --repo tacogips/kinko \
  --clobber
```

## Verification

From the tap checkout:

```bash
ruby -c Formula/kinko.rb
brew audit --strict kinko || brew audit --strict --formula kinko
brew install tacogips/tap/kinko
brew test tacogips/tap/kinko
```

If online audit fails due network, GitHub credentials, or rate limits, run the
non-online audit and report the limitation.

## Tap API Metadata Gate

After pushing the tap Formula, require the tap's `update-api-metadata.yml`
workflow to succeed for that commit. Derive the GitHub tap repository from
`tacogips/tap`, wait for the matching workflow run, then
verify `api/formula/kinko.json` from GitHub Raw.
The JSON release is incomplete unless `.versions.stable` equals the release
version and `.ruby_source_checksum.sha256` equals the SHA-256 of the committed
`Formula/kinko.rb`.
