# Folder Vault Design Proposal

Concise proposal for project-scoped encrypted folders managed by `kinko`.

## Overview

Folder vaults let a project keep sensitive working files in an encrypted
backend while exposing a normal plaintext directory only during an explicit
unlock period.

Primary goals:
- prevent accidental commits of plaintext working folders
- reuse kinko profile/path scoping and unlock key material
- rely on proven platform backends instead of a custom encrypted filesystem
- keep initial lifecycle behavior conservative and scriptable

Non-goals:
- preventing same-UID processes from reading an already mounted folder
- cross-platform encrypted container compatibility
- force-unmounting busy filesystems by default
- background cleanup across sleep/wake/crash without a later watcher design

## Command Behavior

Command family:

```bash
kinko folder add <name>
kinko folder unlock <name>
kinko folder lock <name>
kinko folder status [name]
kinko folder path <name>
```

Behavior:
- `add` registers one folder name under the resolved profile/path scope,
  creates backend storage, and adds `<name>/` to the project `.gitignore` when
  absent. It does not mount the folder.
- all folder subcommands require an unlocked kinko session to read encrypted
  folder registration metadata.
- `unlock` requires an unlocked kinko session, derives or retrieves the backend
  credential, requires the registered backend storage to exist, creates the
  mountpoint if needed, and mounts the plaintext folder at
  `<resolved-path>/<name>`.
- `unlock` keeps kinko in the foreground as the mount owner and soft-unmounts
  when the command exits, including interrupt or terminate.
- `kinko lock` after a successful `folder unlock` blocks future mount attempts
  but does not force-unmount the already-owned folder mount.
- `lock` performs a normal detach/unmount while the kinko session is unlocked.
  If `kinko lock` was already run while a foreground `folder unlock` owner is
  active, exiting or interrupting that owner process is the cleanup path. If the
  mount is busy, `lock` fails with guidance and leaves the mount intact.
- `status` reports configured folders and whether each is mounted.
- `path` prints the plaintext mount path only when the folder is configured and
  currently mounted.

Validation:
- `<name>` must be a single relative path element.
- Empty names, absolute paths, `..`, leading `-`, control characters, path
  separators, and existing non-directory mountpoint files are rejected.
- kinko refuses to mount over unrelated existing content.

## Storage Layout

Folder metadata belongs in encrypted config, not plaintext bootstrap config.
Backend ciphertext lives under the kinko data directory, outside repository
trees.

```text
<kinko-data-dir>/
  folders/
    <folder-id>/
      meta.json
      macos.sparsebundle/
      linux.cipher/

<project>/
  <name>/              # plaintext mountpoint while unlocked
```

`folder-id` is a stable digest over profile, normalized path, and folder name.
`meta.json` stores non-secret operational data such as backend type, format
version, and creation timestamps. Encrypted config stores the authoritative
record binding `name`, `profile`, `path`, `folder-id`, and backend.

## macOS Backend

Default backend: encrypted sparsebundle disk image through `hdiutil`.

Design:
- Create one sparsebundle per folder vault.
- Use AES-encrypted disk image settings supported by macOS.
- Supply the per-folder backend secret through stdin or a temporary descriptor,
  never through command arguments.
- Attach with an explicit mountpoint under the project path.
- Detach with ordinary `hdiutil detach`; do not use force detach by default.

Rationale:
- ships with macOS
- avoids requiring macFUSE for the first implementation
- gives users familiar disk-image recovery and inspection tools

## Linux Backend

Default backend: unavailable in the current release.

Design:
- Do not advertise or select `gocryptfs` as enabled yet.
- Return unsupported-platform backend behavior on Linux.
- Reserve `<kinko-data-dir>/folders/<folder-id>/linux.cipher` for a future
  backend once release requirements are met.

Rationale:
- mature encrypted directory backend
- clean separation between ciphertext directory and plaintext mountpoint
- scriptable CLI behavior aligned with kinko, but Linux FUSE release readiness
  is deferred

## Lifecycle Cleanup

Initial cleanup is best-effort and local to explicit commands:
- `lock` unmounts but does not infer ownership of an existing mountpoint
  directory.
- `unlock` attempts the same soft unmount when the foreground process exits.
- `unlock` removes an empty mountpoint directory after unmount only when that
  command created the mountpoint for its own lifecycle.
- `add` validates the project `.gitignore` can be snapshotted before creating
  backend storage.
- failed `unlock` attempts remove empty mountpoints created during that attempt.
- failed `add` attempts remove backend storage created during that attempt
  before the encrypted folder registration is committed.
- failed encrypted registration persistence after `.gitignore` is written
  restores the previous `.gitignore` state.
- commented `.gitignore` rules and negated rules are not considered active
  protection for the plaintext mountpoint; literal names beginning with `#` or
  `!` are escaped when written.
- symlinked project `.gitignore` files are rejected instead of followed because
  rollback and permission preservation must remain local to the project file.
- `status` detects stale metadata versus actual mount state and reports it.
- `add` keeps `.gitignore` updated so plaintext mountpoints are not committed.

Deferred cleanup:
- TTL-based unmount
- crash recovery
- sleep/wake handling
- retrying busy unmounts
- LaunchAgent/systemd watcher integration

Those require a separate watcher/daemon design because a one-shot CLI cannot
reliably clean up after process death or OS lifecycle events.

## Security Boundaries

In scope:
- encrypted-at-rest folder contents while locked/unmounted
- no plaintext folder contents stored under kinko metadata
- no backend passwords in process arguments, logs, errors, or config files
- `.gitignore` guardrail against accidental commits
- exact profile/path/folder-name binding for each vault

Out of scope:
- root, kernel, or physical-device compromise
- malicious same-UID processes reading mounted plaintext
- preventing other allowed processes from traversing the mounted directory
- hiding filenames from the selected backend if the backend exposes them

Key model:
- kinko uses its existing unlocked DEK material to derive per-folder backend
  secrets with domain separation.
- A folder password/secret is not user-visible and is not persisted in
  plaintext.
- Backend command invocations must be redacted in diagnostics.

## Implementation Risks

- Backend availability varies by host: macOS has `hdiutil`; Linux remains
  intentionally unavailable until `gocryptfs` and FUSE readiness work is
  completed.
- Mount lifecycle behavior differs across platforms, especially busy unmount
  errors and stale mount detection.
- Passing backend secrets safely requires careful process spawning and test
  coverage to prevent leaks through argv, environment, logs, or shell history.
- macOS sparsebundle behavior can vary by filesystem and OS version.
- FUSE permissions and desktop distributions can produce confusing Linux
  failures that need strong `doctor` diagnostics.
- A mounted folder is ordinary plaintext to local processes, so documentation
  must avoid implying sandbox-grade isolation.
- Backup/export behavior must decide whether to include folder ciphertext in
  kinko backups and must never include active plaintext mountpoints.

## Recommendation

Implement folder vaults behind a small backend interface with macOS `hdiutil`
enabled first and Linux returning unsupported-platform behavior for now. Ship
explicit `add`, `unlock`, `lock`, `status`, and `path` commands first, with no
daemon and no force unmount. Treat Linux `gocryptfs`, watcher-based cleanup,
and stronger process isolation as follow-up designs.
