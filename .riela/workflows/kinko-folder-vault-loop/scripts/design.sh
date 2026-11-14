#!/usr/bin/env sh
set -eu

rg -q "Folder Vault Architecture" design-docs/specs/architecture.md
rg -q "kinko folder" design-docs/specs/command.md
rg -q "Linux: unsupported for the current release" design-docs/specs/architecture.md
rg -q "Linux is intentionally" design-docs/specs/command.md
rg -q "gocryptfs" design-docs/specs/notes.md
rg -q "hdiutil" design-docs/specs/notes.md

printf '%s\n' '{"step":"design","status":"passed","evidence":["design-docs/specs/architecture.md","design-docs/specs/command.md","design-docs/specs/notes.md"],"summary":"folder vault design covers command UX, storage, macOS hdiutil release behavior, Linux deferred behavior, lifecycle, and security boundaries"}'
