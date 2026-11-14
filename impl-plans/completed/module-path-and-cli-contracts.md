# Module Path and CLI Contracts Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-review-findings-2026-07.md#6-prioritized-remediation-plan-inputs`
**Created**: 2026-07-03
**Last Updated**: 2026-07-03

## Design Document Reference

This plan implements P0 review findings from
`design-docs/specs/design-review-findings-2026-07.md`:

- F-01: fix the misspelled module path previously used for the kinko module.
- F-02: make fish export/import quoting round-trip backslash values.
- F-07: accept the documented `kinko backup <directory>` positional form while
  preserving `--dest-path` compatibility.

Scope boundaries:

- Does not address folder-vault backup semantics, explosion semantics, mutation
  lock recovery, legacy-session hardening, or broader command-surface drift.
- Does not redesign backup archive format or encryption.

## Modules

### 1. Module Path Rewrite

#### `go.mod`, `cmd/kinko/main.go`, `internal/kinko/cobra_runtime.go`, `README.md`, `Taskfile.yml`, `flake.nix`

**Status**: Completed

```go
module github.com/tacogips/kinko
```

**Checklist**:
- [x] Update `go.mod` module path.
- [x] Rewrite internal imports from the misspelled module path to
      `github.com/tacogips/kinko`.
- [x] Update README install/build examples.
- [x] Update Taskfile and Nix build-version linker paths.
- [x] Run `go mod tidy`.

### 2. Fish Quote Round Trip

#### `internal/kinko/runtime_io_commands.go`

**Status**: Completed

```go
func quoteFish(v string) string
func parseFishQuotedImportValue(raw string) (string, string)
```

**Checklist**:
- [x] Escape backslashes before single quotes in fish output.
- [x] Decode `\\` and `\'` in fish import values.
- [x] Reject unsupported fish escape sequences explicitly.

#### `internal/kinko/runtime_import_export_test.go`

**Status**: Completed

```go
func TestParseImportAssignments_FishEscapes(t *testing.T)
func TestParseImportAssignments_FishRejectsInvalidEscapes(t *testing.T)
```

**Checklist**:
- [x] Add table tests for trailing backslash values.
- [x] Add tests for doubled backslashes and embedded single quotes.
- [x] Add invalid escape tests.

### 3. Backup Positional Destination

#### `internal/kinko/cobra_runtime.go`, `internal/kinko/backup.go`

**Status**: Completed

```go
func newBackupCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
func runBackup(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Accept zero or one positional destination directory.
- [x] Keep `--dest-path` as a compatibility option.
- [x] Reject specifying both positional destination and `--dest-path`.
- [x] Preserve the default destination of `.`.

#### `internal/kinko/backup_test.go` or Cobra command tests

**Status**: Completed

```go
func TestRunBackup_DestinationArguments(t *testing.T)
```

**Checklist**:
- [x] Test default destination remains valid.
- [x] Test positional destination is accepted.
- [x] Test `--dest-path` remains accepted.
- [x] Test positional plus `--dest-path` is rejected.
- [x] Test more than one positional argument is rejected.

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Module path rewrite | `go.mod`, imports, README, build config | Completed | Build/test/vet |
| Fish quote round trip | `internal/kinko/runtime_io_commands.go` | Completed | Unit tests |
| Backup positional destination | `internal/kinko/backup.go`, `internal/kinko/cobra_runtime.go` | Completed | Unit/CLI tests |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Module path rewrite | None | Completed |
| Fish quote round trip | None | Completed |
| Backup positional destination | None | Completed |
| Final verification | All modules | Completed |

## Completion Criteria

- [x] F-01 is remediated in module path, imports, README examples, and build config.
- [x] F-02 is remediated with fish quote/import tests.
- [x] F-07 is remediated with backup argument tests.
- [x] `go mod tidy` completed.
- [x] `gofmt` completed for changed Go files.
- [x] `go build -o /dev/null ./...` passes.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.

## Progress Log

### Session: 2026-07-03 00:00
**Tasks Completed**: Created implementation plan for P0 review findings.
**Tasks In Progress**: Module path, fish quoting, and backup destination fixes.
**Blockers**: None.
**Notes**: Plan is intentionally scoped to P0 because the full review has 35
findings and recommends separate plans for P1-P4 remediation.

### Session: 2026-07-03 01:00
**Tasks Completed**: Remediated F-01, F-02, and F-07; updated module path,
fish quote/import escaping, backup positional destination handling, README
examples, and focused regression tests.
**Tasks In Progress**: None.
**Blockers**: None.
**Verification**: `go mod tidy`, `gofmt`, `go build -o /dev/null ./...`,
`go test ./...`, and `go vet ./...` completed successfully.
