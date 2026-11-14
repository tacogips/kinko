#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
binary_name="kinko"
artifact_name="kinko"

usage() {
  cat <<EOF
Usage:
  scripts/build-homebrew-release.sh [target ...]

Targets:
  darwin-arm64  darwin-x64  linux-arm64  linux-x64

Environment:
  RELEASE_VERSION  Override package version used in archive names.
  RELEASE_DIR      Output directory. Defaults to dist/homebrew.
  GO_CMD_PATH      Package path to build. Defaults to ./cmd/$binary_name.
  GO_LDFLAGS       Optional extra linker flags.

Examples:
  scripts/build-homebrew-release.sh
  scripts/build-homebrew-release.sh darwin-arm64 linux-x64

This builder stages standalone Homebrew archives. It does not publish release
assets, mutate a tap, render a formula, or push commits.
EOF
}

detect_target() {
  local kernel arch
  kernel="$(uname -s)"
  arch="$(uname -m)"

  case "$kernel:$arch" in
    Darwin:arm64) printf '%s\n' "darwin-arm64" ;;
    Darwin:x86_64) printf '%s\n' "darwin-x64" ;;
    Linux:aarch64 | Linux:arm64) printf '%s\n' "linux-arm64" ;;
    Linux:x86_64) printf '%s\n' "linux-x64" ;;
    *)
      printf 'unsupported Homebrew host platform: %s/%s\n' "$kernel" "$arch" >&2
      return 1
      ;;
  esac
}

validate_target() {
  case "$1" in
    darwin-arm64 | darwin-x64 | linux-arm64 | linux-x64) ;;
    *)
      printf 'unsupported Homebrew target: %s\n' "$1" >&2
      usage >&2
      return 1
      ;;
  esac
}

goos_for_target() {
  case "$1" in
    darwin-arm64 | darwin-x64) printf '%s\n' "darwin" ;;
    linux-arm64 | linux-x64) printf '%s\n' "linux" ;;
  esac
}

goarch_for_target() {
  case "$1" in
    darwin-arm64 | linux-arm64) printf '%s\n' "arm64" ;;
    darwin-x64 | linux-x64) printf '%s\n' "amd64" ;;
  esac
}

validate_version() {
  local version
  version="$1"

  if [[ "$version" == *..* || ! "$version" =~ ^[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z][0-9A-Za-z.+-]*)?$ ]]; then
    printf 'unsafe release version: %s\n' "$version" >&2
    printf 'expected archive-safe semver-like value without path separators or parent traversal\n' >&2
    return 1
  fi
}

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$repo_root" "$1" ;;
  esac
}

assert_child_path() {
  local root child
  root="${1%/}"
  child="$2"

  if [[ -z "$root" || "$root" == "/" || "$child" != "$root"/* ]]; then
    printf 'unsafe path outside release directory: %s\n' "$child" >&2
    return 1
  fi
}

write_sha256() {
  local file dir base
  file="$1"
  dir="$(dirname "$file")"
  base="$(basename "$file")"

  if command -v shasum >/dev/null 2>&1; then
    ( cd "$dir" && shasum -a 256 "$base" )
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dir" && sha256sum "$base" )
    return
  fi

  printf 'missing checksum tool: expected shasum or sha256sum\n' >&2
  return 1
}

package_version() {
  if [[ -n "${RELEASE_VERSION:-}" ]]; then
    printf '%s\n' "$RELEASE_VERSION"
    return
  fi

  tr -d '[:space:]' < "$repo_root/internal/build/VERSION"
}

build_target() {
  local version target release_dir work_dir archive binary goos goarch cmd_path
  version="$1"
  target="$2"
  release_dir="$3"
  work_dir="$release_dir/work/$artifact_name-$version-$target"
  archive="$release_dir/$artifact_name-$version-$target.tar.gz"
  binary="$work_dir/bin/$binary_name"
  goos="$(goos_for_target "$target")"
  goarch="$(goarch_for_target "$target")"
  cmd_path="${GO_CMD_PATH:-./cmd/$binary_name}"

  assert_child_path "$release_dir" "$work_dir"
  assert_child_path "$release_dir" "$archive"

  rm -rf "$work_dir" "$archive" "$archive.sha256"
  mkdir -p "$work_dir/bin"

  (
    cd "$repo_root"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
      go build -trimpath -ldflags "${GO_LDFLAGS:--s -w}" -o "$binary" "$cmd_path"
  )
  chmod 0755 "$binary"
  if [[ -f "$repo_root/README.md" ]]; then
    cp "$repo_root/README.md" "$work_dir/README.md"
  fi

  tar -C "$work_dir" -czf "$archive" .
  write_sha256 "$archive" > "$archive.sha256"

  printf 'built %s\n' "$archive"
  cat "$archive.sha256"
}

main() {
  if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    usage
    return
  fi

  local version release_dir
  version="$(package_version)"
  validate_version "$version"
  release_dir="$(absolute_path "${RELEASE_DIR:-dist/homebrew}")"

  local -a targets
  if [[ "$#" -eq 0 ]]; then
    targets=("$(detect_target)")
  else
    targets=("$@")
  fi

  local target
  for target in "${targets[@]}"; do
    validate_target "$target"
    mkdir -p "$release_dir"
    build_target "$version" "$target" "$release_dir"
  done

  printf '\nRender a formula after all platform archives exist:\n'
  printf '  scripts/render-homebrew-formula.sh %s\n' "$version"
}

main "$@"
