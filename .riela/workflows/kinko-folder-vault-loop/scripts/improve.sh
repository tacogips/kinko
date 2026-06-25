#!/usr/bin/env sh
set -eu

rg -q "Folder commands implemented" impl-plans/completed/kinko-folder-vault.md
rg -q "Linux backend remains gated" impl-plans/completed/kinko-folder-vault.md
rg -q "go test ./..." impl-plans/completed/kinko-folder-vault.md
rg -q "go vet ./..." impl-plans/completed/kinko-folder-vault.md
rg -q "kinko run --sandbox --folder" design-docs/specs/architecture.md
rg -F -q '`folder unlock` is foreground-owned by default' impl-plans/completed/kinko-folder-vault.md
rg -F -q 'leading `-`, control characters' design-docs/specs/architecture.md
rg -q "Unmount failures include retry guidance" impl-plans/completed/kinko-folder-vault.md
rg -q "before backend storage side effects" impl-plans/completed/kinko-folder-vault.md

printf '%s\n' '{"step":"improve","status":"passed","summary":"final docs and implementation plan reflect reviewed constraints, foreground folder ownership, macOS-only release gating, folder-name hardening, unmount retry guidance, gitignore-before-backend ordering, verified implementation, and deferred sandbox lifecycle work"}'
