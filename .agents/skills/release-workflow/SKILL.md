---
name: release-workflow
description: Execute kinko release operations end-to-end for Go binaries, including local verification, artifact packaging, and optional GitHub release publishing. Use when users ask to release, publish a version, or build release binaries.
allowed-tools: Read, Write, Edit, Bash, Grep, Glob
user-invocable: true
---

# Release Workflow Skill (Go)

This skill standardizes release execution for `kinko` using the repository `Taskfile.yml` and version source `internal/build/VERSION`.

## When to Apply

Apply this skill when the user asks to:
- release a merged version
- publish artifacts
- create a GitHub release
- build distributable binaries

## Release Contract

In this repository, interpret an unscoped "release" request as:
1. Build and verify local cross-platform binaries
2. Create versioned release artifacts and checksums
3. Commit release artifacts if requested by the user
4. Push branch and tags to GitHub
5. Publish GitHub Release only if the user explicitly asks for remote publishing

If the user explicitly asks for "binary only", perform steps 1-2 only.

## Preconditions

1. Ensure branch is clean and up to date.
2. Ensure `internal/build/VERSION` is the intended release version.
3. Ensure Go toolchain is available (`go version`).
4. Ensure task runner is available (`task --version`) when using task-based commands.
5. Ensure GitHub CLI auth is valid for remote publishing:
   - `gh auth status`
6. Ensure remote is configured and writable:
   - `git remote -v`
   - `git push --dry-run origin <branch>`

## Standard Commands

### Local Cross-Platform Binary Release (default)

```bash
set -euo pipefail
VERSION="$(cat internal/build/VERSION)"
ARTIFACT_DIR="dist/release"
mkdir -p "${ARTIFACT_DIR}"

task clean
task build
task smoke

for target in \
  "linux amd64 tar.gz" \
  "linux arm64 tar.gz" \
  "darwin amd64 tar.gz" \
  "darwin arm64 tar.gz" \
  "windows amd64 zip" \
  "windows arm64 zip"
do
  set -- $target
  GOOS="$1"
  GOARCH="$2"
  PKG="$3"
  EXT=""
  if [ "$GOOS" = "windows" ]; then
    EXT=".exe"
  fi

  BIN="kinko_${VERSION}_${GOOS}_${GOARCH}${EXT}"
  OUT_BASE="kinko_${VERSION}_${GOOS}_${GOARCH}"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -ldflags "-s -w -X githus.com/tacogips/kinko/internal/build.version=${VERSION}" \
    -o "${ARTIFACT_DIR}/${BIN}" ./cmd/kinko

  if [ "$PKG" = "zip" ]; then
    (cd "${ARTIFACT_DIR}" && zip -q "${OUT_BASE}.zip" "${BIN}")
    rm -f "${ARTIFACT_DIR:?}/${BIN}"
  else
    (cd "${ARTIFACT_DIR}" && tar -czf "${OUT_BASE}.tar.gz" "${BIN}")
    rm -f "${ARTIFACT_DIR:?}/${BIN}"
  fi
done

(cd "${ARTIFACT_DIR}" && sha256sum kinko_${VERSION}_*.tar.gz kinko_${VERSION}_*.zip > SHA256SUMS)
```

### Commit and Push (when user requests push)

```bash
set -euo pipefail
VERSION="$(cat internal/build/VERSION)"
BRANCH="$(git branch --show-current)"
TAG="v${VERSION}"

git add dist/release
git status --short
git commit -m "release: build artifacts for ${TAG}"

# Push branch updates first
git push origin "${BRANCH}"

# Create and push tag
git tag "${TAG}"
git push origin "${TAG}"
```

Use signed tags when available:

```bash
git tag -s "v${VERSION}" -m "Release v${VERSION}"
git push origin "v${VERSION}"
```

### Optional GitHub Release Publish (after push/tag)

Run only when explicitly requested:

```bash
VERSION="$(cat internal/build/VERSION)"
TAG="v${VERSION}"
gh release create "${TAG}" \
  dist/release/kinko_${VERSION}_linux_amd64.tar.gz \
  dist/release/kinko_${VERSION}_linux_arm64.tar.gz \
  dist/release/kinko_${VERSION}_darwin_amd64.tar.gz \
  dist/release/kinko_${VERSION}_darwin_arm64.tar.gz \
  dist/release/kinko_${VERSION}_windows_amd64.zip \
  dist/release/kinko_${VERSION}_windows_arm64.zip \
  "dist/release/SHA256SUMS" \
  --title "${TAG}" \
  --notes "Release ${TAG}"
```

If release exists, use `gh release upload` with `--clobber`.

## Verification Checklist

After release commands finish:
1. `./kinko version` matches `internal/build/VERSION`.
2. `dist/release/kinko_<version>_<os>_<arch>.(tar.gz|zip)` exists for all target platforms.
3. `dist/release/SHA256SUMS` exists and validates:
   - `cd dist/release && sha256sum -c SHA256SUMS`
4. If push was requested, branch and `v<version>` tag are visible on origin.
5. Working tree has no unintended generated files beyond release artifacts.
6. If GitHub publish was requested, confirm release URL exists for `v<version>`.

## Failure Handling

1. If `task build` fails, run `go build ./...` to isolate compile errors.
2. If `task smoke` fails, stop and report failing test/build command output.
3. If version mismatch appears in `kinko version`, verify `LDFLAGS` wiring in `Taskfile.yml`.
4. If `gh release create` fails due to existing tag/release, use:
   - `gh release upload <tag> <files...> --clobber`
5. If checksum validation fails, rebuild artifact and regenerate `SHA256SUMS`.
6. If tag already exists, verify target commit and use `gh release upload` without recreating tag.
7. If `zip` is unavailable, generate Windows `.zip` with a temporary Go helper using `archive/zip`.
