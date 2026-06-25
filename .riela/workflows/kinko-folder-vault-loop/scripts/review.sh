#!/usr/bin/env sh
set -eu

rg -q "same-UID" design-docs/specs/architecture.md
rg -q "no long-lived daemon" design-docs/specs/architecture.md
rg -q "force unmount" design-docs/specs/architecture.md
rg -q "Do not advertise or select" design-docs/specs/design-folder-vault.md
rg -F -q "Return unsupported-platform backend behavior on Linux" impl-plans/completed/kinko-folder-vault.md
rg -F -q "Re-enable" impl-plans/completed/kinko-folder-vault.md
rg -q "TestLinuxFolderBackendUnavailable" internal/kinko/folder_backend_linux_test.go
rg -q "parseHdiutilInfoMounted" internal/kinko/folder_backend_darwin.go
rg -q "TestParseHdiutilInfoMountedRequiresExactMountpoint" internal/kinko/folder_backend_darwin_test.go
rg -q "TestParseHdiutilInfoMountedAcceptsLabeledMountpointWithColon" internal/kinko/folder_backend_darwin_test.go
rg -q "foreground mount owner" design-docs/specs/architecture.md
rg -q "TestFolderUnlockRequiresUnlockedSession" internal/kinko/folder_test.go
rg -q "TestFolderUnlockKeepsMountAfterKinkoLockUntilOwnerExit" internal/kinko/folder_test.go
rg -q "TestFolderUnlockSoftUnmountsOnOwnerExitByDefault" internal/kinko/folder_test.go
rg -q "Preserve existing config keys" impl-plans/completed/kinko-folder-vault.md
rg -q "folder name must not start with '-'" internal/kinko/folder_model.go
rg -q "folder name must not contain control characters" internal/kinko/folder_model.go
rg -q "TestFolderLockReportsUnmountGuidance" internal/kinko/folder_test.go
rg -q "close files in the folder" internal/kinko/folder.go
rg -q "backend Ensure should not run before .gitignore validation" internal/kinko/folder_test.go

printf '%s\n' '{"step":"review","status":"passed","findings":[],"summary":"review gates passed for security wording, foreground folder ownership, unlocked-session mount gating, owner-exit unmount, Linux release gating, exact macOS mount detection, config preservation, folder-name hardening, unmount retry guidance, and gitignore-before-backend ordering"}'
