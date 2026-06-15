# Path Prune Missing Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-path-prune-missing`, `design-docs/specs/architecture.md#kinko-path-prune-missing`
**Created**: 2026-06-15
**Last Updated**: 2026-06-16

---

## Design Document Reference

**Source**: `design-docs/specs/command.md`, `design-docs/specs/architecture.md`

### Summary
Implement `kinko path prune-missing`, a local vault maintenance command that previews or deletes stored profile/path scopes whose stored directories no longer exist. The command is preview-only by default, requires direct vault password verification before any metadata output, requires `--yes` for destructive pruning, preserves encrypted vault format, and never enumerates or deletes shared scope data.

### Scope
**Included**: Cobra `path prune-missing` command, runtime pruning flow, path classification, text and JSON output, password verification, mutation locking, tests, README/command-summary documentation, and user-facing design verification.
**Excluded**: Deleting shared scope data, removing empty profiles, deleting config/unlock/backup data, adding profile cleanup, and changing secret value output.

### Accepted Review Feedback
- Step 3 accepted the design with no high or mid findings.
- Low finding addressed in this plan: `--path` remains ignored per accepted command spec because the command operates on stored path scopes; `--all-profiles` with an explicitly supplied inherited `--profile` is rejected at the Cobra boundary to avoid ambiguous selection.

---

## Modules

### 1. CLI Command Surface

#### internal/kinko/constants.go

**Status**: COMPLETED

```go
const (
	cmdPath = "path"
)

const (
	pathPruneMissing = "prune-missing"
)
```

**Checklist**:
- [x] Add `cmdPath` and `pathPruneMissing` constants.
- [x] Preserve existing command constants and names.

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newPathCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
func newPathPruneMissingCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Register `newPathCommand` on the root command.
- [x] Add `path prune-missing` with `--all-profiles`, `--yes`, and `--json`.
- [x] Pass parsed flags to runtime without adding a command-local `--path`.
- [x] Reject explicit inherited `--profile` when `--all-profiles` is also set.
- [x] Ensure Cobra help includes `path` and `prune-missing`.

---

### 2. Prune Runtime Model

#### internal/kinko/path_prune_missing.go

**Status**: COMPLETED

```go
type pathPruneMissingOptions struct {
	AllProfiles bool
	Yes         bool
	JSON        bool
}

type pathPruneMissingMode string

const (
	pathPruneMissingModePreview pathPruneMissingMode = "preview"
	pathPruneMissingModePrune   pathPruneMissingMode = "prune"
)

type pathPruneCandidate struct {
	Profile string
	RawPath string
	Path    string
	KeyCount int
}

type pathPruneSkippedDiagnostic struct {
	Profile string
	RawPath string
	Reason  string
}

type pathPruneMissingResult struct {
	Mode       pathPruneMissingMode
	Candidates []pathPruneCandidate
	Skipped    []pathPruneSkippedDiagnostic
	TotalScopes int
	TotalKeys   int
}

func runPath(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func runPathPruneMissing(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func parsePathPruneMissingArgs(args []string) (pathPruneMissingOptions, error)
```

**Checklist**:
- [x] Add `runPath` dispatcher for `prune-missing`.
- [x] Parse and validate `--all-profiles`, `--yes`, and `--json`.
- [x] Reject positional arguments and unknown subcommands.
- [x] Keep output empty on failed or canceled password verification.

---

### 3. Candidate Classification

#### internal/kinko/path_prune_missing.go

**Status**: COMPLETED

```go
type pathExistenceClassifier interface {
	ClassifyDirectory(path string) (pathPrunePathState, string)
}

type pathPrunePathState string

const (
	pathPrunePathStateStale  pathPrunePathState = "stale"
	pathPrunePathStateKept   pathPrunePathState = "kept"
	pathPrunePathStateSkipped pathPrunePathState = "skipped"
)

type osPathExistenceClassifier struct{}

func buildPathPruneMissingResult(vd *vaultData, selectedProfile string, allProfiles bool, classifier pathExistenceClassifier) (pathPruneMissingResult, error)
func selectPathPruneProfiles(vd *vaultData, selectedProfile string, allProfiles bool) []string
func classifyStoredPathScope(rawPath string, classifier pathExistenceClassifier) (pathPrunePathState, string, string)
```

**Checklist**:
- [x] Enumerate only profile path scopes; do not copy, list, or inspect `vd.Shared`.
- [x] Sort profiles and paths for deterministic text and JSON output.
- [x] Treat only normalized absolute paths missing as directories as stale.
- [x] Skip relative paths, invalid normalization, normalization collisions, existing files, broken symlinks, permission-denied paths, and ambiguous filesystem errors.
- [x] Include key counts but never key names or values.

---

### 4. Destructive Prune Persistence

#### internal/kinko/path_prune_missing.go

**Status**: COMPLETED

```go
func pruneMissingPathScopes(vd *vaultData, candidates []pathPruneCandidate)
func pathPruneCandidateKey(candidate pathPruneCandidate) string
```

#### internal/kinko/vault.go

**Status**: COMPLETED

```go
func write0600Atomically(path string, data []byte) error
```

**Checklist**:
- [x] Verify password before acquiring the mutation lock and before any candidate output.
- [x] Acquire mutation lock only for `--yes`.
- [x] Reload vault after acquiring mutation lock and recompute candidates from the locked snapshot.
- [x] Delete only candidate profile/path maps.
- [x] Preserve empty profile maps.
- [x] Persist with the same encrypted vault JSON payload format.
- [x] Use atomic replacement for `saveVault` if existing write path is not atomic.

---

### 5. Output Rendering

#### internal/kinko/path_prune_missing.go

**Status**: COMPLETED

```go
type pathPruneMissingJSONOutput struct {
	Mode        string                         `json:"mode"`
	Pruned      []pathPruneMissingJSONScope    `json:"pruned,omitempty"`
	Candidates  []pathPruneMissingJSONScope    `json:"candidates,omitempty"`
	Skipped     []pathPruneMissingJSONSkipped  `json:"skipped,omitempty"`
	TotalScopes int                            `json:"totalScopes"`
	TotalKeys   int                            `json:"totalKeys"`
}

type pathPruneMissingJSONScope struct {
	Profile  string `json:"profile"`
	Path     string `json:"path"`
	KeyCount int    `json:"keyCount"`
}

type pathPruneMissingJSONSkipped struct {
	Profile string `json:"profile"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
}

func renderPathPruneMissingText(w io.Writer, result pathPruneMissingResult) error
func renderPathPruneMissingJSON(w io.Writer, result pathPruneMissingResult) error
```

**Checklist**:
- [x] Render preview candidates as profile, path, and key count.
- [x] Render destructive output only after `saveVault` succeeds.
- [x] Include aggregate scope and key totals.
- [x] Render skipped diagnostics without secret values or key names.
- [x] Emit valid JSON when `--json` is set.

---

### 6. Tests and Documentation Verification

#### internal/kinko/path_prune_missing_test.go

**Status**: COMPLETED

```go
func TestRunPathPruneMissingPreviewRequiresPasswordBeforeOutput(t *testing.T)
func TestRunPathPruneMissingPreviewReportsOnlyMissingPathScopes(t *testing.T)
func TestRunPathPruneMissingYesDeletesOnlyStalePathScopes(t *testing.T)
func TestRunPathPruneMissingDoesNotDeleteSharedScope(t *testing.T)
func TestRunPathPruneMissingAllProfiles(t *testing.T)
func TestRunPathPruneMissingSkipsAmbiguousPaths(t *testing.T)
func TestRunPathPruneMissingJSONOutput(t *testing.T)
```

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestRun_PathPruneMissingRegisteredThroughCobra(t *testing.T)
func TestRun_PathPruneMissingRejectsExplicitProfileWithAllProfiles(t *testing.T)
```

**Checklist**:
- [x] Cover wrong password leaving stdout empty and vault unchanged.
- [x] Cover preview mode leaving stale scopes intact.
- [x] Cover `--yes` pruning stale path scopes and preserving kept scopes.
- [x] Cover all-profile selection and selected-profile default.
- [x] Cover shared scope preservation.
- [x] Cover JSON shape and totals.
- [x] Cover help/registration through Cobra.

#### README.md

**Status**: COMPLETED

**Checklist**:
- [x] Add `kinko path prune-missing` to the command summary.
- [x] Document preview-by-default, `--yes`, `--all-profiles`, `--json`, password re-entry, ignored `--path`, and shared-scope preservation.

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| CLI constants | `internal/kinko/constants.go` | COMPLETED | `TestRun_PathPruneMissingRegisteredThroughCobra` |
| Cobra path command | `internal/kinko/cobra_runtime.go` | COMPLETED | `internal/kinko/cobra_runtime_test.go` |
| Runtime dispatcher and parser | `internal/kinko/path_prune_missing.go` | COMPLETED | `internal/kinko/path_prune_missing_test.go` |
| Candidate classification | `internal/kinko/path_prune_missing.go` | COMPLETED | `internal/kinko/path_prune_missing_test.go` |
| Destructive pruning | `internal/kinko/path_prune_missing.go`, `internal/kinko/vault.go` | COMPLETED | `internal/kinko/path_prune_missing_test.go` |
| Output rendering | `internal/kinko/path_prune_missing.go` | COMPLETED | `internal/kinko/path_prune_missing_test.go` |
| User-facing docs | `README.md` | COMPLETED | Documentation review |

## Task Breakdown

| Task | Deliverables | Dependencies | Parallelizable |
|------|--------------|--------------|----------------|
| TASK-001 CLI surface | Constants and Cobra `path prune-missing` registration | None | No |
| TASK-002 Runtime parser/model | Runtime option/result types and `runPath` dispatcher | TASK-001 | No |
| TASK-003 Classification | Stored path normalization, collision detection, filesystem classification | TASK-002 | No |
| TASK-004 Preview and output | Password-gated preview flow plus text/JSON rendering | TASK-003 | No |
| TASK-005 Destructive prune | Lock, reload, recompute, delete, encrypted persist | TASK-004 | No |
| TASK-006 Tests | Runtime and Cobra coverage | TASK-002 through TASK-005 | No |
| TASK-007 Documentation | README command summary and behavior notes | TASK-001 through TASK-005 | Yes |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| `path prune-missing` Cobra command | Existing Cobra runtime | AVAILABLE |
| Password-gated metadata output | `verifyVaultPasswordFromInput`, `passwordVerificationInputFor` | AVAILABLE |
| Vault mutation safety | `acquireMutationLock`, `loadVault`, `saveVault` | AVAILABLE |
| Path normalization | `normalizeStoredScopePath` | AVAILABLE |
| Deterministic output | `sortedKeys`, `storedProfileNames` patterns | AVAILABLE |

## Completion Criteria

- [x] `kinko path prune-missing` is available through Cobra help and runtime execution.
- [x] Preview mode requires direct password verification and never mutates vault data.
- [x] `--yes` requires direct password verification, mutation lock, locked-snapshot recomputation, and deletes only stale local path scopes.
- [x] Shared scope, config payloads, unlock state, backup artifacts, profile definitions, and non-selected profile scopes are preserved.
- [x] Text and JSON output include profile, path, key count, skipped diagnostics, and totals without secret values or key names.
- [x] Tests cover preview, prune, all profiles, selected profile, skipped cases, JSON, shared preservation, and password failure.
- [x] README documents `kinko path prune-missing` behavior and flags.
- [x] Feature-specific tests pass; repository-wide `go test ./...` was attempted and remains red due to unrelated backup cwd normalization assertions.
- [x] `go vet ./...` passes.

## Verification Plan

```bash
go test ./...
go vet ./...
go test ./internal/kinko -run 'TestRunPathPruneMissing|TestRun_PathPruneMissing'
go test ./internal/kinko -run 'TestRunShow_AllScopes|TestRun_Delete'
git diff -- internal/kinko/constants.go internal/kinko/cobra_runtime.go internal/kinko/path_prune_missing.go internal/kinko/path_prune_missing_test.go internal/kinko/cobra_runtime_test.go internal/kinko/vault.go
git diff -- README.md design-docs/specs/command.md design-docs/specs/architecture.md
```

## Progress Log

### Session: 2026-06-15
**Tasks Completed**: Plan creation from accepted design.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Implementation must run the required post-Go-modification check/test pass after any Go edits.

### Session: 2026-06-15 23:54 JST
**Tasks Completed**: TASK-001 through TASK-007.
**Tasks In Progress**: Repository-wide `go test ./...` remains red due to pre-existing backup cwd path normalization assertions.
**Blockers**: `go test ./...` fails in `TestRunBackup_DefaultsToCurrentWorkingDirectory` and `TestRun_CobraBasedRegression_AllCommands/backup_defaults_to_current_directory` because macOS reports backup paths under `/private/var/...` while tests expect `/var/...`.
**Notes**: Feature-specific verification passed with `go test ./internal/kinko -run 'TestRunPathPruneMissing|TestRun_PathPruneMissing'`; `go vet ./...` passed.

### Session: 2026-06-16
**Tasks Completed**: Completion-state check archived the plan after Step 7 acceptance and Step 8 documentation refresh.
**Tasks In Progress**: None.
**Blockers**: None for accepted `path prune-missing` implementation work.
**Notes**: Repository-wide `go test ./...` failure remains tracked as unrelated residual risk; feature-specific tests, vet, diff check, review acceptance, and documentation refresh are complete.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: Accepted design updates in `design-docs/specs/command.md` and `design-docs/specs/architecture.md`
