# Kinko Backup Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/architecture.md#command-runtime-data-flow, design-docs/specs/command.md#kinko-backup-directory
**Created**: 2026-03-08
**Last Updated**: 2026-03-08

---

## Design Document Reference

**Source**: design-docs/specs/architecture.md, design-docs/specs/command.md

### Summary
Add `kinko backup <directory>` to create a password-locked ZIP archive containing all persisted files required to restore stored data, while excluding transient runtime artifacts.

### Scope
**Included**: CLI command wiring, current-password verification flow, persisted file discovery, password-locked ZIP writing, and regression tests.
**Excluded**: Restore/import-from-backup workflow, keychain backup, and cross-version migration tooling beyond preserving existing persisted files.

---

## Modules

### 1. Backup Runtime

#### internal/kinko/backup.go

**Status**: DONE

```go
func runBackup(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error

type backupInputOptions struct {
	currentStdin bool
	currentFD    int
	forceTTY     bool
}

type backupSourceFile struct {
	SourcePath  string
	ArchivePath string
}
```

**Checklist**:
- [x] Parse `backup` flags and destination directory argument
- [x] Read and verify current password against vault metadata
- [x] Acquire mutation lock for a stable backup snapshot
- [x] Discover persisted files under the data directory and exclude transient lock artifacts
- [x] Reject unsafe source entries such as symlinks and reject destinations inside the data directory
- [x] Emit a password-locked ZIP archive and success output

---

### 2. CLI and Help Surface

#### internal/kinko/constants.go
#### internal/kinko/cobra_runtime.go
#### internal/kinko/help.go

**Status**: DONE

**Checklist**:
- [x] Add `backup` command constant and runtime wiring
- [x] Document flags and examples in help output
- [x] Keep command behavior consistent between Cobra runtime and package runtime

---

### 3. Tests

#### internal/kinko/backup_test.go
#### internal/kinko/cobra_runtime_test.go

**Status**: DONE

**Checklist**:
- [x] Add archive content and exclusion tests
- [x] Add current-password validation tests
- [x] Add Cobra integration coverage for the new subcommand

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Backup runtime | `internal/kinko/backup.go` | DONE | Passing |
| CLI/help wiring | `internal/kinko/constants.go`, `internal/kinko/cobra_runtime.go`, `internal/kinko/help.go` | DONE | Passing |
| Backup tests | `internal/kinko/backup_test.go`, `internal/kinko/cobra_runtime_test.go` | DONE | Passing |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Backup runtime | Existing vault metadata/session helpers | Available |
| Backup tests | Backup runtime and CLI wiring | Completed |

## Completion Criteria

- [x] `kinko backup <directory>` creates a password-locked ZIP archive
- [x] Archive contains all regular persisted files under the data directory and bootstrap config when present
- [x] Archive excludes transient session/mutation-lock artifacts
- [x] Backup rejects symlinked/non-regular source entries and output paths inside the data directory
- [x] Current password verification works for interactive and non-interactive flows
- [x] `go test ./...` passes
- [x] `go vet ./...` passes

## Progress Log

### Session: 2026-03-08 14:15
**Tasks Completed**: Reviewed current architecture/command surface and identified that persistent state spans `dataDir` plus bootstrap config while transient session state must be excluded from backup scope.
**Tasks In Progress**: Updating design docs, creating this plan, and implementing backup runtime plus tests.
**Blockers**: None
**Notes**: The backup command will verify the current password directly instead of depending on unlocked session state.

### Session: 2026-03-08 15:02
**Tasks Completed**: Added the backup command, help/design updates, password-locked ZIP writer, archive/content regression tests, and Cobra wiring.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: Verification completed with `go test ./...`, `go vet ./...`, and `go build ./cmd/kinko` using sandbox-local `HOME`, `GOCACHE`, and `GOMODCACHE`.

### Session: 2026-03-08 15:34
**Tasks Completed**: Reviewed the in-progress diff as continuation work, reconciled the architecture doc with the implemented lock-after-prompt flow, and added regression coverage for the missing-bootstrap backup path plus destination directory creation.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The backup design now explicitly avoids holding the mutation lock across interactive password entry while preserving snapshot consistency during archive creation.

### Session: 2026-03-08 16:20
**Tasks Completed**: Re-reviewed the backup diff against the intended persistence-scope behavior, corrected the plan's design references, and added regression coverage proving `kinko backup` works while the vault is locked.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: Backup remains a current-password-authenticated persistence snapshot and does not depend on unlocked runtime session state.

### Session: 2026-03-08 17:05
**Tasks Completed**: Tightened the backup design and implementation to match the intended "backup everything persisted" scope by walking the data directory, excluding transient runtime artifacts, rejecting symlinks, and rejecting destinations inside the data directory.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The earlier fixed-file allowlist was too narrow for the stated product intent because future persisted files could be silently omitted.

### Session: 2026-03-08 17:42
**Tasks Completed**: Reviewed the uncommitted backup diff as continuation work, found that the generated ZIP was not interoperable with standard password-aware ZIP readers, fixed the PKZIP encryption implementation, and added regression coverage for the cipher vector plus Cobra init config wiring under sandboxed HOME conditions.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The design now explicitly requires ZIP-reader interoperability instead of only testing a package-local decryptor.

### Session: 2026-03-08 18:18
**Tasks Completed**: Re-reviewed the continuation diff against the intended "backup everything persisted" scope, fixed symlink handling so backup now rejects a symlinked bootstrap config and destination paths that resolve into the data directory, and added regression tests for both cases.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: The earlier implementation enforced symlink rejection only inside the walked data directory and used a lexical destination check, which left a gap between the design intent and actual filesystem behavior.

### Session: 2026-03-08 18:44
**Tasks Completed**: Re-reviewed the continuation diff at the Cobra entrypoint, found that shared bootstrap-config preflight incorrectly blocked `kinko backup` when the config file was absent, switched backup to finalize-only preflight, and added a Cobra regression test for the missing-bootstrap path.
**Tasks In Progress**: None
**Blockers**: None
**Notes**: This aligns the Cobra command path with the design and package runtime semantics that treat the bootstrap config as optional backup input.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: None
