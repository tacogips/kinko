# Folder Backup and Explosion Lifecycle Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-review-findings-2026-07.md#6-prioritized-remediation-plan-inputs`
**Created**: 2026-07-03
**Last Updated**: 2026-07-03

## Design Document Reference

This plan implements P1 review findings:

- F-03: keep folder-vault storage out of `kinko backup` until a streaming/ZIP64
  folder-backup design exists.
- F-04: make `kinko explosion` compatible with folder storage and destroy it
  only after refusing active mounts.
- F-05: add stale mutation-lock recovery metadata.
- F-26: add `kinko folder remove <name>` lifecycle command.
- F-30: add folder-scoped lifecycle locking around mount/unmount transitions.

Scope boundaries:

- Does not implement streaming backup, ZIP64, or folder backup inclusion.
- Does not redesign folder path relocation or sparsebundle sizing.
- Does not add a full `doctor` command.

## Modules

### 1. Backup Folder Exclusion

#### `internal/kinko/backup.go`

**Status**: COMPLETED

```go
func walkBackupDataFiles(dataDir string) ([]backupSourceFile, error)
func isTransientBackupPath(relPath string, d os.DirEntry) bool
```

**Checklist**:
- [x] Skip root `folders/` from backup traversal.
- [x] Add regression test proving folder storage entries are omitted.
- [x] Document backup exclusion in command/reference docs.

### 2. Explosion Folder Compatibility

#### `internal/kinko/runtime_admin.go`

**Status**: COMPLETED

```go
func validateKinkoDataDirLayout(dataDir string) error
func purgeKinkoDataFiles(dataDir string) error
func ensureNoMountedFolders(opts globalOptions, records []FolderRecord) error
```

**Checklist**:
- [x] Allow non-symlink root `folders/` in explosion layout validation.
- [x] Refuse explosion if any registered folder status is mounted.
- [x] Remove `folders/` during purge after validation and mount checks.
- [x] Add tests for folder storage removal and mounted refusal.

### 3. Stale Mutation Lock Recovery

#### `internal/kinko/password_change.go`

**Status**: COMPLETED

```go
type mutationLockMetadata struct {
    PID       int    `json:"pid"`
    Hostname  string `json:"hostname"`
    CreatedAt string `json:"created_at"`
}

func acquireMutationLock(dataDir string) (func(), error)
```

**Checklist**:
- [x] Write PID, hostname, and creation timestamp to the lock file.
- [x] Allow takeover when metadata proves the same-host owner process is gone.
- [x] Keep corrupt/unknown lock files blocking, but include lock path guidance.
- [x] Add tests for active lock refusal and stale lock takeover.

### 4. Folder Remove Command

#### `internal/kinko/folder.go`, `internal/kinko/cobra_runtime.go`, `internal/kinko/constants.go`

**Status**: COMPLETED

```go
func runFolderRemove(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Add `kinko folder remove <name> [--keep-storage] [--yes]`.
- [x] Refuse removal while mounted.
- [x] Remove config record and storage by default.
- [x] Preserve storage with `--keep-storage`.
- [x] Confirm destructive storage deletion unless `--yes` is set.
- [x] Add runtime and Cobra tests.

### 5. Folder Lifecycle Locking

#### `internal/kinko/folder.go`

**Status**: COMPLETED

```go
func acquireFolderLifecycleLock(dataDir string, record FolderRecord) (func(), error)
```

**Checklist**:
- [x] Serialize `folder unlock`, `folder lock`, and `folder remove` per folder.
- [x] Avoid holding the lock for the full foreground mount lifetime.
- [x] Add a concurrent unlock test with fake backend.

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Backup folder exclusion | `internal/kinko/backup.go` | COMPLETED | Backup tests passing |
| Explosion compatibility | `internal/kinko/runtime_admin.go` | COMPLETED | Explosion tests passing |
| Stale mutation lock | `internal/kinko/password_change.go` | COMPLETED | Lock tests passing |
| Folder remove | `internal/kinko/folder.go`, Cobra wiring | COMPLETED | Folder runtime/Cobra tests passing |
| Folder lifecycle lock | `internal/kinko/folder.go` | COMPLETED | Concurrency tests passing |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Backup folder exclusion | None | COMPLETED |
| Stale mutation lock | None | COMPLETED |
| Folder lifecycle lock | None | COMPLETED |
| Folder remove | Folder lifecycle lock | COMPLETED |
| Explosion compatibility | Folder mount checks | COMPLETED |

## Completion Criteria

- [x] F-03 is remediated by excluding folder storage from backup with tests/docs.
- [x] F-04 is remediated by explosion layout/purge/mount checks with tests/docs.
- [x] F-05 is remediated by stale mutation-lock recovery with tests.
- [x] F-26 is remediated by `folder remove` with tests/docs.
- [x] F-30 is remediated by folder lifecycle locking with tests.
- [x] `gofmt` completed for changed Go files.
- [x] `go build -o /dev/null ./...` passes.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.

## Progress Log

### Session: 2026-07-03 01:30
**Tasks Completed**: Created P1 implementation plan and selected safety
semantics for folder storage.
**Tasks In Progress**: Backup exclusion, explosion compatibility, stale locks,
folder remove, and folder lifecycle locking.
**Blockers**: None.
**Notes**: Folder backup inclusion is deferred intentionally because the current
backup writer has no streaming/ZIP64 path.

### Session: 2026-07-03 03:20
**Tasks Completed**: F-03, F-04, F-05, F-26, and F-30 implementation and
regression coverage.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Added focused folder lifecycle tests in
`internal/kinko/folder_lifecycle_test.go` to avoid growing the existing
`folder_test.go` past the project file-size limit. Verification completed with
`gofmt`, `go mod tidy`, `go build -o /dev/null ./...`, `go test ./...`, and
`go vet ./...`. Leave active for main-agent review and archive.
