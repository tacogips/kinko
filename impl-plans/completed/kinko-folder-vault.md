# Kinko Folder Vault Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/architecture.md#folder-vault-architecture`, `design-docs/specs/command.md#kinko-folder`
**Created**: 2026-06-23
**Last Updated**: 2026-06-25

## Design Document Reference

Implement the first daemon-free folder vault slice:
- register project-local folder vaults,
- store records in encrypted config,
- gitignore plaintext mountpoints,
- expose `folder add/status/path/unlock/lock`,
- support daemon-free foreground unlock ownership via `folder unlock`,
- provide macOS `hdiutil` backend behavior and keep Linux gated behind
  unsupported-platform behavior until `gocryptfs` release readiness is done.

Out of scope:
- long-running daemon or LaunchAgent,
- force unmount,
- sandboxed process-only visibility,
- cross-platform backing format compatibility.

## Modules

### 1. Folder Data Model

#### `internal/kinko/folder_model.go`

**Status**: COMPLETED

```go
type FolderRecord struct {
    Name      string    `json:"name"`
    Profile   string    `json:"profile"`
    Path      string    `json:"path"`
    Backend   string    `json:"backend"`
    FolderID  string    `json:"folder_id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type folderConfigPayload struct {
    Folders []FolderRecord `json:"folders,omitempty"`
}
```

**Checklist**:
- [x] Validate one-element folder names
- [x] Reject CLI-option-looking and control-character folder names
- [x] Derive stable folder IDs from profile/path/name
- [x] Load and save folder records through encrypted config
- [x] Preserve existing config keys

### 2. Folder Backend Interface

#### `internal/kinko/folder_backend.go`

**Status**: COMPLETED

```go
type FolderBackend interface {
    Ensure(ctx context.Context, record FolderRecord, secret string) error
    Mount(ctx context.Context, record FolderRecord, secret string, mountpoint string) error
    Unmount(ctx context.Context, record FolderRecord, mountpoint string) error
    Status(ctx context.Context, record FolderRecord, mountpoint string) (FolderMountStatus, error)
}

type FolderMountStatus struct {
    Mounted bool
    Detail  string
}
```

**Checklist**:
- [x] Select backend by GOOS
- [x] Use argument-array subprocess execution only
- [x] Use minimal subprocess environment
- [x] Return unsupported-platform errors for other OSes

### 3. macOS Backend

#### `internal/kinko/folder_backend_darwin.go`

**Status**: COMPLETED

```go
type hdiutilFolderBackend struct{}
```

**Checklist**:
- [x] Create encrypted sparsebundle backing store
- [x] Attach sparsebundle at requested mountpoint
- [x] Detach mountpoint without force
- [x] Detect mounted state without broad shell commands

### 4. Linux Backend

#### `internal/kinko/folder_backend_linux.go`

**Status**: GATED

```go
func newDefaultFolderBackend(_ string) FolderBackend
```

**Checklist**:
- [x] Return unsupported-platform backend behavior on Linux
- [x] Ensure `folderBackendName()` does not advertise `gocryptfs`
- [ ] Re-enable `gocryptfs` only after Linux release readiness is complete

### 5. Portable Folder Runtime

#### `internal/kinko/folder.go`

**Status**: COMPLETED

```go
func runFolder(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Implement `add`
- [x] Implement `unlock`
- [x] Implement foreground `unlock` soft-unmount on process exit
- [x] Keep mounted folders intact when `kinko lock` runs after mount
- [x] Implement `lock`
- [x] Implement `status`
- [x] Implement `path`
- [x] Derive backend secret from unlocked DEK and folder identity
- [x] Refuse unsafe mountpoints and busy/unrelated paths

### 6. Cobra Command Wiring

#### `internal/kinko/cobra_runtime.go`, `internal/kinko/constants.go`

**Status**: COMPLETED

```go
func newFolderCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Add explicit `folder` command family
- [x] Add help/runtime regression coverage
- [x] Preserve disabled implicit completion behavior

### 7. Tests

#### `internal/kinko/folder_test.go`, `internal/kinko/cobra_runtime_test.go`

**Status**: COMPLETED

```go
func TestFolderAddRegistersEncryptedConfigAndGitignore(t *testing.T)
func TestFolderRejectsUnsafeNames(t *testing.T)
func TestFolderPathRequiresMountedState(t *testing.T)
```

**Checklist**:
- [x] Test config persistence without plaintext bootstrap changes
- [x] Test `.gitignore` append idempotence
- [x] Test name/path validation
- [x] Test command surface/help wiring
- [x] Test foreground unlock soft-unmount behavior
- [x] Use fake backend for portable unit tests

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Folder data model | `internal/kinko/folder_model.go` | COMPLETED | PASS |
| Backend interface | `internal/kinko/folder_backend.go` | COMPLETED | PASS |
| macOS backend | `internal/kinko/folder_backend_darwin.go` | COMPLETED | PASS |
| Linux backend | `internal/kinko/folder_backend_linux.go` | GATED | PASS |
| Portable runtime | `internal/kinko/folder.go` | COMPLETED | PASS |
| Cobra wiring | `internal/kinko/cobra_runtime.go` | COMPLETED | PASS |
| Tests | `internal/kinko/folder_test.go` | COMPLETED | PASS |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Backend implementations | Backend interface | DONE |
| Runtime unlock/lock | Data model, backend interface | DONE |
| Cobra wiring | Runtime signatures | DONE |
| Tests | Data model, fake backend seam | DONE |

## Completion Criteria

- [x] Riela workflow validates and runs through design/review/improve
- [x] Design docs and references updated
- [x] Active implementation plan created and indexed
- [x] Folder commands implemented
- [x] Linux backend remains gated behind unsupported-platform behavior
- [x] Unit tests cover portable behavior
- [x] Linux-only compile test covers unavailable backend behavior
- [x] macOS backend status parsing avoids substring mountpoint false positives
- [x] `folder unlock` is foreground-owned by default and unmounts on owner exit
- [x] `folder unlock` requires unlocked kinko state only at mount time
- [x] Backing store `meta.json` is written without project path or folder name
- [x] Failed `folder add` attempts clean up newly-created backend storage before registration commits
- [x] `folder add` validates project `.gitignore` rollback state before backend storage side effects
- [x] `.gitignore` entries escape leading `#`, leading `!`, and glob metacharacters
- [x] `.gitignore` detection ignores commented and negated rules when deciding whether guardrail coverage already exists
- [x] Successful `.gitignore` appends preserve existing file permissions
- [x] Failed encrypted registration persistence restores any `.gitignore` change made by `folder add`
- [x] Symlinked project `.gitignore` files are rejected instead of followed
- [x] Backend command errors redact secret stdin before returning diagnostics
- [x] Compatibility `folder unlock --hold` parsing remains hidden from Cobra help
- [x] Explicit `folder lock` preserves the mountpoint directory after unmount
- [x] Unmount failures include retry guidance and leave mounts intact
- [x] Folder names reject leading `-` and control characters
- [x] `gofmt` applied to modified Go files
- [x] `go build -o /dev/null ./...` passes
- [x] `go test ./...` passes
- [x] `go vet ./...` passes

## Progress Log

### Session: 2026-06-23 00:00
**Tasks Completed**: Created design sections, references, and implementation plan.
**Tasks In Progress**: Riela workflow execution and Go implementation.
**Blockers**: None.
**Notes**: Initial implementation intentionally avoids daemon and force unmount behavior.

### Session: 2026-06-23 18:30
**Tasks Completed**: Implemented folder model, backend abstractions, macOS/Linux backends, portable runtime, Cobra wiring, and tests.
**Tasks In Progress**: Riela workflow execution.
**Blockers**: None.
**Notes**: Verified with `go build -o /dev/null ./...`, `go test ./...`, and `go vet ./...`.

### Session: 2026-06-23 18:39
**Tasks Completed**: Replaced recursive Riela agent nodes with deterministic command nodes and completed design/review/improve workflow run.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: `riela workflow validate kinko-folder-vault-loop --workflow-definition-dir .riela/workflows --output json` passed and `riela workflow run kinko-folder-vault-loop --workflow-definition-dir .riela/workflows --output json` completed with `status=completed`.

### Session: 2026-06-23 18:45
**Tasks Completed**: Added `kinko folder unlock --hold` foreground lifecycle mode with soft unmount on interrupt/terminate and portable test coverage.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This satisfies the daemon-free process-exit cleanup requirement without introducing force unmount.

### Session: 2026-06-23 19:10
**Tasks Completed**: Re-gated Linux folder backend for the current release, added Linux-only unsupported-platform test coverage, and updated Riela review gates to enforce macOS-only release behavior.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Linux keeps `gocryptfs` as a future backend candidate only; current Linux builds return unsupported-platform folder backend behavior.

### Session: 2026-06-23 19:35
**Tasks Completed**: Reviewed the folder feature through Riela, found macOS mount status could false-positive on prefix-related mountpoints, added exact `hdiutil info` parsing and Darwin-only parser tests, and updated Riela review gates for this risk.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: The improvement keeps Linux gated and narrows macOS status detection to exact mountpoint matches.

### Session: 2026-06-24 08:20
**Tasks Completed**: Re-ran Riela review, found labeled `hdiutil info` parsing failed for mountpoints containing colons, switched labeled parsing to first-colon splitting, added Darwin parser coverage, and updated the Riela review gate.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This keeps exact mountpoint detection while preserving valid macOS paths that include colon characters.

### Session: 2026-06-25 00:00
**Tasks Completed**: Updated design and implementation requirements so `folder unlock` is the foreground mount owner by default, mounting requires unlocked kinko state, later `kinko lock` does not force-unmount an owned folder, and owner exit soft-unmounts.
**Tasks In Progress**: Runtime and Cobra updates.
**Blockers**: None.
**Notes**: The folder lifecycle now models mount availability using the same unlocked-session gate as variable reads, with cleanup tied to the folder command lifecycle.

### Session: 2026-06-25 01:00
**Tasks Completed**: Reviewed current design, implementation, and git diff; added backing-store metadata writing, escaped `.gitignore` folder entries, secret redaction for backend command errors, and conservative mountpoint cleanup behavior.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Verified with `go build -o /dev/null ./...`, `go test ./...`, and `go vet ./...`.

### Session: 2026-06-25 02:00
**Tasks Completed**: Reviewed design and implementation against the current git diff; made `folder add` keep encrypted config unchanged when `.gitignore` update fails, added retry regression coverage, and clarified that folder subcommands require an unlocked session because folder registrations are encrypted.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Re-verification performed after the review fixes.

### Session: 2026-06-25 03:00
**Tasks Completed**: Reviewed the current git diff again, hardened folder name validation against leading option-like names and control characters, added unmount retry guidance, and documented the behavior.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Regression coverage added for invalid names and unmount failure guidance.

### Session: 2026-06-25 04:00
**Tasks Completed**: Reviewed design and implementation against the current git diff; made `folder add` treat new backend storage as provisional until encrypted registration is committed, cleaned it up on registration failure, and added regression coverage that preserves pre-existing storage.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This narrows partial-state behavior for failed `.gitignore` or config writes while preserving retry safety.

### Session: 2026-06-25 05:00
**Tasks Completed**: Reviewed implementation, design, and current git diff; made `folder add` restore the previous `.gitignore` state if encrypted config persistence fails after writing the guardrail entry, and added regression coverage for existing and newly-created `.gitignore` files.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This keeps project-tree guardrail updates and encrypted registration persistence from being left half-applied.

### Session: 2026-06-25 06:00
**Tasks Completed**: Reviewed implementation, design, and current git diff; made successful `.gitignore` appends preserve the existing file mode and added regression coverage.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Newly-created `.gitignore` files still use `0600`, while existing project files keep their prior permissions.

### Session: 2026-06-25 07:00
**Tasks Completed**: Reviewed implementation, design, and current git diff; fixed `.gitignore` active-rule detection so commented and negated rules do not suppress guardrail insertion, and added regression coverage for escaped literal `#` and `!` names.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Verified with focused folder tests plus full build, test, and vet checks.

### Session: 2026-06-25 08:00
**Tasks Completed**: Reviewed design, implementation, and current git diff; hid the no-op compatibility `folder unlock --hold` flag from Cobra help while preserving parser compatibility, and added CLI help regression coverage.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Keeps the documented CLI surface aligned with foreground unlock being the default behavior.

### Session: 2026-06-25 09:00
**Tasks Completed**: Reviewed implementation, design, and current git diff; rejected symlinked project `.gitignore` files before folder guardrail writes, added regression coverage, and documented the rollback boundary.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This prevents `folder add` from writing through a symlink target while still cleaning provisional backend storage on failure.

### Session: 2026-06-25 10:00
**Tasks Completed**: Reviewed implementation, design, and current git diff; moved `.gitignore` snapshot validation before backend storage creation and added regression assertions that backend `Ensure` is not called when `.gitignore` validation fails.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: This reduces partial-state risk by failing project-tree guardrail validation before invoking platform backend side effects.
