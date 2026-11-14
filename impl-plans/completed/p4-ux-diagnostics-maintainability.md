# P4 UX Diagnostics and Maintainability Cleanup

## Status
Completed

## Design Reference
- `design-docs/specs/design-review-findings-2026-07.md`
- Findings: F-21, F-22, F-24, F-25, F-28, F-29, F-31, F-32, F-33, F-34, F-35

## Objective
Close the remaining low-priority review findings with targeted behavior fixes, documentation corrections, and explicit future-work boundaries for larger refactors.

## Deliverables
- Masking and sensitive-output prompt fixes in `internal/kinko/runtime_display.go`.
- Session diagnostics in `doctor` for corrupt or mismatched session tokens.
- Darwin `hdiutil info -plist` parsing for folder mount status.
- Child exit status propagation for `kinko exec`.
- Import value-disclosure fallback behavior.
- Atomic metadata/session token write consistency.
- Typed option execution paths for the review-cited backup, unlock, export,
  import, and exec command families, plus display and
  set/set-key/delete/copy/move/folder/path prune-missing/direnv export paths.
- Current exit-code matrix documentation plus backup and delete auth/lock
  mappings.
- Documentation updates for memory hygiene, sparsebundle capacity, parser behavior, and future refactor work.

## Subtasks

### TASK-001: UX and Prompt Fixes
**Status**: Completed
**Parallelizable**: No
**Completion Criteria**:
- [x] Masked values no longer reveal prefix/suffix/length.
- [x] Sensitive-output confirmations use TTY-aware prompting.
- [x] Import disclosure "no" falls back to key-only summary.

### TASK-002: Diagnostics and Backend Parsing
**Status**: Completed
**Parallelizable**: No
**Completion Criteria**:
- [x] `doctor` reports corrupt/mismatched session-token diagnostics.
- [x] Darwin folder status uses `hdiutil info -plist`.
- [x] Sparsebundle default capacity is documented.

### TASK-003: Exec and Atomic Write Cleanup
**Status**: Completed
**Parallelizable**: No
**Completion Criteria**:
- [x] Child process exit status is propagated by `ExitCode`.
- [x] Metadata init uses atomic save.
- [x] Session token writes use atomic replacement.

### TASK-004: Documentation and Future Work Boundaries
**Status**: Completed
**Parallelizable**: Yes
**Completion Criteria**:
- [x] Architecture uses best-effort memory hygiene language.
- [x] Cited backup/unlock/export/import/exec parser duplication is reduced with typed option paths.
- [x] Get/show parser duplication is reduced with typed option paths.
- [x] Set/set-key parser duplication is reduced with typed option paths.
- [x] Delete parser duplication is reduced with typed option paths.
- [x] Copy/move parser duplication is reduced with typed option paths.
- [x] Folder parser duplication is reduced with typed option paths.
- [x] Path prune-missing parser duplication is reduced with typed option paths.
- [x] Direnv export parser duplication is reduced with typed option paths.
- [x] Password change parser duplication is reduced with typed option paths.
- [x] No Cobra argv reconstruction sites remain in `cobra_runtime.go`.
- [x] Current exit-code matrix is documented.
- [x] Unlock structured exit-code mappings are implemented and documented.
- [x] Import/export structured exit-code mappings are implemented and documented.
- [x] Folder lifecycle structured exit-code mappings are implemented and documented.
- [x] Remaining command-family typed-option and exit-code mapping work is documented as future work.
- [x] Posix import non-expansion of `$VAR` is documented.
- [x] Set-key/import value normalization is documented and tested.
- [x] Release-diff notes are summarized.

### TASK-005: Verification and Archive
**Status**: Completed
**Parallelizable**: No
**Completion Criteria**:
- [x] Targeted tests pass.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] `git diff --check` passes.
- [x] Plan is moved to `impl-plans/completed/` and indexed.

## Progress Log

### Session: 2026-07-03
**Tasks Completed**: TASK-001, TASK-002, TASK-003, and TASK-004.
**Notes**: Remediated low-risk P4 behavior/docs for F-21, F-22, F-24, F-25, F-28, F-29, F-31, F-32, F-33, F-34, and F-35. Added typed option execution paths for the review-cited backup/unlock/export/import/exec command families, get/show display paths, and set/set-key/delete/copy/move/folder/path prune-missing/direnv export/password change mutation paths. Added structured backup auth/lock, delete bulk-auth/lock, unlock policy/auth/I/O, import/export policy/lock/I/O, and folder lifecycle policy/lock/I/O exit-code mappings. Targeted tests, `go test ./...`, `go vet ./...`, and `git diff --check` passed after the latest get/show F-31 partial remediation; targeted delete tests, `go test ./...`, `go vet ./...`, and `git diff --check` passed after delete F-31/F-32 partial remediation; targeted set/set-key/delete tests passed after set/set-key F-31 partial remediation; targeted copy/move tests passed after copy/move F-31 partial remediation; targeted folder tests passed after folder F-31 partial remediation; targeted path prune-missing tests passed after path prune-missing F-31 partial remediation; targeted direnv export tests passed after direnv export F-31 partial remediation; targeted password-change tests passed after password-change F-31 remediation; targeted unlock tests passed after unlock F-32 partial remediation; targeted import/export tests passed after import/export F-32 partial remediation; targeted folder lifecycle tests passed after folder lifecycle F-32 remediation; targeted set-key/import normalization tests passed after F-35 remediation.

### Session: 2026-07-03 Final Verification
**Tasks Completed**: TASK-005.
**Notes**: Final verification passed with `go test ./...`, `go vet ./...`, `git diff --check`, and an explicit check that no Cobra `parseArgs` reconstruction sites remain.
