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
1. Build and verify local binary
2. Create versioned release artifacts (`.tar.gz` and `SHA256SUMS`)
3. Publish GitHub Release only if the user explicitly asks for remote publishing

If the user explicitly asks for "binary only", perform steps 1-2 only.

## Preconditions

1. Ensure branch is clean and up to date.
2. Ensure `internal/build/VERSION` is the intended release version.
3. Ensure Go toolchain is available (`go version`).
4. Ensure task runner is available (`task --version`) when using task-based commands.
5. Ensure GitHub CLI auth is valid for remote publishing:
   - `gh auth status`

## Standard Commands

### Local Binary Release (default)

```bash
set -euo pipefail
VERSION="$(cat internal/build/VERSION)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
ARTIFACT_DIR="dist/release"
ARTIFACT_BASENAME="kinko_${VERSION}_${OS}_${ARCH}"

task clean
task build
task smoke

mkdir -p "${ARTIFACT_DIR}"
cp kinko "${ARTIFACT_DIR}/kinko"
tar -C "${ARTIFACT_DIR}" -czf "${ARTIFACT_DIR}/${ARTIFACT_BASENAME}.tar.gz" kinko
(cd "${ARTIFACT_DIR}" && sha256sum "${ARTIFACT_BASENAME}.tar.gz" > SHA256SUMS)
```

### Optional GitHub Release Publish

Run only when explicitly requested:

```bash
VERSION="$(cat internal/build/VERSION)"
TAG="v${VERSION}"
gh release create "${TAG}" \
  "dist/release/kinko_${VERSION}_$(go env GOOS)_$(go env GOARCH).tar.gz" \
  "dist/release/SHA256SUMS" \
  --title "${TAG}" \
  --notes "Release ${TAG}"
```

If release exists, use `gh release upload` with `--clobber`.

## Verification Checklist

After release commands finish:
1. `./kinko version` matches `internal/build/VERSION`.
2. `dist/release/kinko_<version>_<os>_<arch>.tar.gz` exists.
3. `dist/release/SHA256SUMS` exists and validates:
   - `cd dist/release && sha256sum -c SHA256SUMS`
4. Working tree has no unintended generated files beyond release artifacts.
5. If GitHub publish was requested, confirm release URL exists for `v<version>`.

## Failure Handling

1. If `task build` fails, run `go build ./...` to isolate compile errors.
2. If `task smoke` fails, stop and report failing test/build command output.
3. If version mismatch appears in `kinko version`, verify `LDFLAGS` wiring in `Taskfile.yml`.
4. If `gh release create` fails due to existing tag/release, use:
   - `gh release upload <tag> <files...> --clobber`
5. If checksum validation fails, rebuild artifact and regenerate `SHA256SUMS`.
