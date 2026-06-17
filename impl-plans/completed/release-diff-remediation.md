# Release Diff Remediation Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/notes.md#release-diff-remediation-notes
**Created**: 2026-06-17
**Last Updated**: 2026-06-17

---

## Design Document Reference

**Source**: design-docs/specs/notes.md#release-diff-remediation-notes

### Summary
Address the accepted post-`v0.1.2` release-diff findings by splitting oversized runtime files along cohesive package-internal boundaries and making `dist/release/SHA256SUMS` cover every tracked release archive.

### Scope
**Included**:
- Split `internal/kinko/runtime.go` into package-internal files without behavior, prompt-order, output, error, or public API changes.
- Split `internal/kinko/runtime_test.go` into focused test files so the same 1000-line Go source limit is satisfied for tests.
- Regenerate or complete `dist/release/SHA256SUMS` so every tracked `dist/release/kinko_*` archive has one checksum entry.
- Verify the full package and release manifest checks.

**Excluded**:
- New CLI behavior, command renames, prompt wording changes, vault format changes, or release artifact rebuilds.
- Changes outside this isolated review worktree.
- Edits to unrelated local worktrees named in workflow input.

---

## Codex and Workflow References

- Workflow: `codex-design-and-implement-review-loop`
- Issue reference: `release-diff-review`, baseline `v0.1.2`, head `origin/main`
- Parent review handoff: `codex-recent-change-quality-loop:riel-codex-recent-change-quality-loop-1781657586-b51d8611:step3-handoff`
- Accepted design step: `step3-design-review`, exec `exec-000005`
- Implementation should use `.agents/agents/go-coding.md` for Go edits.
- After any Go edit, invoke `.agents/agents/check-and-test-after-modify.md` expectations by running the verification commands in this plan.
- Relevant standards: `.agents/skills/go-coding-standards/SKILL.md`, `.agents/skills/kinko-secret-ops/SKILL.md`

## Modules

### 1. Runtime Mutation Commands

#### internal/kinko/runtime_mutation.go

**Status**: COMPLETED

```go
func runSet(opts globalOptions, args []string, stdin io.Reader, stdout io.Writer) error
func runSetKey(opts globalOptions, args []string, stdin io.Reader, stdout io.Writer) error
func parseSetKeyArgs(args []string) (string, string, bool, bool, error)

type setAssignment struct {
	Key   string
	Value string
}

func parseSetAssignment(raw string) (string, string, error)
func parseSetAssignmentsFromReader(r io.Reader) ([]setAssignment, error)
func runDelete(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Move set, set-key, assignment parsing, and delete command code from `runtime.go`.
- [x] Preserve unexported names and package `kinko`.
- [x] Keep existing tests passing without output or prompt drift.

### 2. Runtime Display and Scope Commands

#### internal/kinko/runtime_display.go

**Status**: COMPLETED

```go
type passwordVerificationInput struct {
	Reader             *bufio.Reader
	NonTerminalStdin  io.Reader
	ReadPasswordSecure func(string) (string, error)
}

func passwordVerificationInputFor(stdin io.Reader, isTerminal func(io.Reader) bool) passwordVerificationInput
func verifyVaultPasswordForBulkDelete(opts globalOptions, input passwordVerificationInput, stderr io.Writer) error
func verifyVaultPasswordForShow(opts globalOptions, stdin io.Reader, stderr io.Writer, prompt string) (io.Reader, error)
func verifyVaultPasswordFromInput(opts globalOptions, input passwordVerificationInput, stderr io.Writer, prompt string) error
func verifyVaultPasswordValue(opts globalOptions, password string) error
func runGet(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func parseGetArgs(args []string) (string, bool, bool, error)
func runShow(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func runShowAllScopes(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, reveal bool) error
func normalizePathScopes(pathsByScope map[string]map[string]string) (map[string]map[string]string, error)
func normalizeStoredScopePath(path string) (string, error)
func maskValue(v string) string
func guardSensitiveOutput(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, action string) error
func guardSensitiveStderr(opts globalOptions, stdin io.Reader, stderr io.Writer, action string) error
```

**Checklist**:
- [x] Move get/show/all-scopes and password verification helpers together.
- [x] Preserve password-before-output ordering for all-scopes reveal and delete-all flows.
- [x] Keep terminal and redirected-output guards unchanged.

### 3. Runtime Import, Export, Exec, and Shell Helpers

#### internal/kinko/runtime_io_commands.go

**Status**: COMPLETED

```go
func runExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error

type stringListFlag []string

func (f *stringListFlag) String() string
func (f *stringListFlag) Set(v string) error
func parseExcludedKeys(raw []string) (map[string]struct{}, error)
func filterSecretsByExclusion(in map[string]string, excluded map[string]struct{}) map[string]string
func runImport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func renderImportSummary(w io.Writer, shell, profile, path string, sharedKeys, repoKeys []string, shared, repoSpecific map[string]string, withValues bool)
func parseImportScopes(shell, content string, allowShared bool) (importScopes, error)
func parseImportAssignments(shell, content string) (map[string]string, error)
func runExec(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func selectExecSecrets(secrets map[string]string, includeAll bool, envList string) (map[string]string, error)
func writeExportBlock(w io.Writer, shell, scope, title string, secrets map[string]string, withScopeComments bool) error
func normalizeShell(shell string) (string, error)
func renderShellAssignment(shell, key, value string) (string, error)
func validateEnvKey(key string) error
```

**Checklist**:
- [x] Move import/export/exec and shell-rendering helpers without parser behavior changes.
- [x] Keep raw import values redacted in errors.
- [x] Preserve scope markers, exclusion filtering, and shell quoting behavior.

### 4. Runtime Administrative Helpers

#### internal/kinko/runtime_admin.go

**Status**: COMPLETED

```go
func runExplosion(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer) error
func validateExplosionTarget(dataDir string) error
func validateKinkoDataDirLayout(dataDir string) error
func purgeKinkoDataFiles(dataDir string) error
func explosionDenylist(home string) []string
func explosionConfirmationToken(dataDir string) string
func verifyExplosionPassword(opts globalOptions, reader *bufio.Reader, stderr io.Writer) error
func runConfig(opts globalOptions, args []string, stdout io.Writer) error
func runProfile(opts globalOptions, args []string, stdout io.Writer) error
func storedProfileNames(vd *vaultData) []string
func writeBootstrapConfig(opts globalOptions) error
func getSecret(opts globalOptions, key string) (string, bool, error)
func showSecrets(opts globalOptions) (map[string]string, error)
func showSecretScopes(opts globalOptions) (map[string]string, map[string]string, error)
func showAllSecretScopes(opts globalOptions) (map[string]string, map[string]map[string]string, error)
```

**Checklist**:
- [x] Move explosion/config/profile/bootstrap and shared vault lookup helpers.
- [x] Preserve destructive-operation validation and denylist behavior.
- [x] Keep shared/profile/path scope resolution unchanged.

### 5. Runtime Test Organization

#### internal/kinko/runtime_import_export_test.go
#### internal/kinko/runtime_show_test.go
#### internal/kinko/runtime_get_exec_profile_test.go

**Status**: COMPLETED

```go
func TestParseImportAssignments_PosixRoundTrip(t *testing.T)
func TestRunShow_AllScopes_RequiresPasswordBeforeOutput(t *testing.T)
func TestRunExport_ExcludeFiltersSharedAndRepoScopes(t *testing.T)
func TestRunGet_SameKeyAcrossDirectoriesResolvesBySelectedPath(t *testing.T)
func TestSelectExecSecrets(t *testing.T)
func TestRunProfile_ListSortedStoredProfiles(t *testing.T)
```

**Checklist**:
- [x] Split `runtime_test.go` by command family, keeping test names and assertions intact.
- [x] Keep shared test helpers in one package-local test file only if needed.
- [x] Ensure every touched `internal/kinko/*.go` and `internal/kinko/*_test.go` file is below 1000 lines.

### 6. Release Manifest Coverage

#### dist/release/SHA256SUMS

**Status**: COMPLETED

```go
// Manifest-only deliverable: no Go API change.
```

**Checklist**:
- [x] Add checksum entries for every tracked `dist/release/kinko_*` archive, including existing `v0.1.2` and `v0.1.3` files.
- [x] Keep one filename entry per archive and no stale entries.
- [x] Preserve the two-space checksum file format accepted by `shasum -a 256 -c`.

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Runtime mutation commands | `internal/kinko/runtime_mutation.go` | COMPLETED | `go test ./internal/kinko` |
| Runtime display and scopes | `internal/kinko/runtime_display.go` | COMPLETED | `go test ./internal/kinko` |
| Runtime import/export/exec | `internal/kinko/runtime_io_commands.go` | COMPLETED | `go test ./internal/kinko` |
| Runtime admin helpers | `internal/kinko/runtime_admin.go` | COMPLETED | `go test ./internal/kinko` |
| Runtime test split | `internal/kinko/runtime_*_test.go` | COMPLETED | `go test ./internal/kinko` |
| Release checksum manifest | `dist/release/SHA256SUMS` | COMPLETED | `shasum -a 256 -c SHA256SUMS` |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Runtime mutation split | Current `internal/kinko/runtime.go` | COMPLETED |
| Runtime display split | Current `internal/kinko/runtime.go` | COMPLETED |
| Runtime import/export/exec split | Current `internal/kinko/runtime.go` | COMPLETED |
| Runtime admin split | Current `internal/kinko/runtime.go` | COMPLETED |
| Runtime test split | Existing `internal/kinko/runtime_test.go` and moved runtime files | COMPLETED |
| Release manifest coverage | Tracked `dist/release/kinko_*` files | COMPLETED |

## Task Breakdown

### TASK-001: Split Runtime Mutation Commands
**Status**: COMPLETED
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime_mutation.go`, reduced `internal/kinko/runtime.go`
**Dependencies**: None

**Completion Criteria**:
- [x] Set/delete related code moved without changing package surface.
- [x] `gofmt` applied to touched Go files.
- [x] Focused tests involving set/delete still pass.

### TASK-002: Split Runtime Display and Scope Commands
**Status**: COMPLETED
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime_display.go`, reduced `internal/kinko/runtime.go`
**Dependencies**: None

**Completion Criteria**:
- [x] Get/show/all-scopes and password verification code moved.
- [x] Prompt order and sensitive-output guards remain covered by existing tests.
- [x] `gofmt` applied to touched Go files.

### TASK-003: Split Runtime Import, Export, Exec, and Admin Helpers
**Status**: COMPLETED
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime_io_commands.go`, `internal/kinko/runtime_admin.go`, reduced `internal/kinko/runtime.go`
**Dependencies**: None

**Completion Criteria**:
- [x] Import/export/exec/admin helpers moved into cohesive files.
- [x] `runtime.go` is removed or remains below 1000 lines.
- [x] `go test ./internal/kinko` passes.

### TASK-004: Split Oversized Runtime Tests
**Status**: COMPLETED
**Parallelizable**: No
**Deliverables**: `internal/kinko/runtime_import_export_test.go`, `internal/kinko/runtime_show_test.go`, `internal/kinko/runtime_get_exec_profile_test.go`, reduced `internal/kinko/runtime_test.go`
**Dependencies**: TASK-001, TASK-002, TASK-003

**Completion Criteria**:
- [x] `runtime_test.go` is removed or remains below 1000 lines.
- [x] No duplicate test helper definitions are introduced.
- [x] `go test ./internal/kinko` passes.

### TASK-005: Complete Release Checksum Manifest
**Status**: COMPLETED
**Parallelizable**: Yes
**Deliverables**: `dist/release/SHA256SUMS`
**Dependencies**: None

**Completion Criteria**:
- [x] `SHA256SUMS` contains exactly one entry for every tracked `dist/release/kinko_*` archive.
- [x] `cd dist/release && shasum -a 256 -c SHA256SUMS` passes.
- [x] Explicit coverage check confirms no tracked archive is missing from the manifest.

### TASK-006: Final Verification and Progress Update
**Status**: COMPLETED
**Parallelizable**: No
**Deliverables**: Updated progress log in this plan
**Dependencies**: TASK-001, TASK-002, TASK-003, TASK-004, TASK-005

**Completion Criteria**:
- [x] All verification commands pass or failures are documented with cause.
- [x] Module status table and task statuses are updated.
- [x] Progress log records implementation session notes and blockers.

## Parallelizable Tasks

| Task | Parallelizable | Reason |
|------|----------------|--------|
| TASK-001 | No | Shares `runtime.go` write scope with other runtime split tasks. |
| TASK-002 | No | Shares `runtime.go` write scope with other runtime split tasks. |
| TASK-003 | No | Shares `runtime.go` write scope with other runtime split tasks. |
| TASK-004 | No | Shares test helper scope with runtime test split. |
| TASK-005 | Yes | Only writes `dist/release/SHA256SUMS` and does not depend on Go refactor files. |
| TASK-006 | No | Final join after all implementation tasks. |

## Verification

Run these commands before marking the plan complete:

```bash
gofmt -w internal/kinko/*.go
go test ./internal/kinko
go test ./...
go vet ./...
wc -l internal/kinko/*.go internal/kinko/*_test.go
git diff --check v0.1.2..HEAD -- internal/kinko README.md design-docs impl-plans .agents dist/release go.mod
(cd dist/release && shasum -a 256 -c SHA256SUMS)
git ls-files dist/release | awk -F/ '/kinko_/ {print $NF}' | sort > /tmp/kinko-release-files.txt
awk '{print $2}' dist/release/SHA256SUMS | sort > /tmp/kinko-release-sums.txt
diff -u /tmp/kinko-release-files.txt /tmp/kinko-release-sums.txt
```

## Completion Criteria

- [x] `internal/kinko/runtime.go` is removed or below 1000 lines.
- [x] `internal/kinko/runtime_test.go` is removed or below 1000 lines.
- [x] No touched Go source file remains at or above 1000 lines.
- [x] Public package surface, CLI behavior, prompt order, command output, and error behavior are unchanged.
- [x] `dist/release/SHA256SUMS` covers every tracked `dist/release/kinko_*` archive.
- [x] `go test ./...`, `go vet ./...`, release checksum verification, and coverage diff checks pass.
- [x] This plan's module table, task statuses, and progress log are updated after implementation.

## Progress Log

### Session: 2026-06-17 10:39 JST
**Tasks Completed**: Step 7 adversarial-review documentation privacy revision.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Addressed adversarial mid-severity findings by replacing host-specific absolute worktree paths in the design notes and completed implementation plan with generic isolated-worktree wording.

### Session: 2026-06-17 10:27 JST
**Tasks Completed**: Step 7 implementation-review bookkeeping revision.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Addressed Step 7 mid-severity finding by moving this completed plan from `impl-plans/active/` to `impl-plans/completed/`, removing the active README row, adding the completed README row, and clearing the active phase mapping.

### Session: 2026-06-17 10:16 JST
**Tasks Completed**: TASK-001, TASK-002, TASK-003, TASK-004, TASK-005, TASK-006.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Split `runtime.go` into cohesive package-internal runtime files, split `runtime_test.go` by command family, regenerated `dist/release/SHA256SUMS` for all tracked release archives, and verified with `gofmt -w internal/kinko/*.go`, `go test ./internal/kinko`, `go test ./...`, `go vet ./...`, `git diff --check v0.1.2..HEAD -- internal/kinko README.md design-docs impl-plans .agents dist/release go.mod`, `(cd dist/release && shasum -a 256 -c SHA256SUMS)`, and the tracked-file coverage diff.

### Session: 2026-06-17 00:00 JST
**Tasks Completed**: Created implementation plan from the accepted Step 3 design review.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Plan intentionally adds the `runtime_test.go` split because current repository standards apply the 1000-line limit to `*_test.go` files and current verification already includes test-file line counts.

## Related Plans

- **Previous**: None
- **Next**: None
- **Depends On**: `design-docs/specs/notes.md#release-diff-remediation-notes`
