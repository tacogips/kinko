# Move Values Between Scopes Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/command.md#kinko-move-local-to-shared-key`, `design-docs/specs/command.md#kinko-move-shared-to-local-key`, `design-docs/specs/architecture.md#kinko-move-local-to-shared-key--kinko-move-shared-to-local-key`, `design-docs/specs/design-shared-keys.md#movement-between-local-and-shared-scopes`
**Created**: 2026-06-16
**Last Updated**: 2026-06-16

---

## Design Document Reference

**Source**: `design-docs/specs/command.md`, `design-docs/specs/architecture.md`, `design-docs/specs/design-shared-keys.md`

### Summary
Implement `kinko move local-to-shared <key>` and `kinko move shared-to-local <key>` as explicit single-key vault maintenance commands. Each command copies the existing plaintext value from the source scope to the destination scope, deletes the source key, and persists one encrypted vault mutation without changing the vault format or existing shared/local scope resolution semantics.

### Scope
**Included**: Cobra `move` command surface, runtime argument parsing, single-key movement in both directions, `--overwrite`, `--yes` / `-y`, unlocked-session and mutation-lock behavior, atomic encrypted vault persistence through the existing save path, runtime and Cobra tests, README documentation, and command/design consistency verification.
**Excluded**: Bulk moves, copy-only commands, wildcard/profile-wide movement, direct password re-entry for single-key moves, vault schema changes, and changes to `get`, `show`, `exec`, or `export` merge precedence.

### Accepted Review Feedback
- Step 3 accepted the design with no high or mid findings.
- The plan preserves the accepted design decisions: explicit directional subcommands, exactly one key, source deletion only as part of one persisted vault mutation, destination conflict failure unless `--overwrite` is set, confirmation bypass only via `--yes`, and no secret value output.

---

## Modules

### 1. CLI Command Surface

#### internal/kinko/constants.go

**Status**: COMPLETED

```go
const (
	cmdMove = "move"
)

const (
	moveLocalToShared = "local-to-shared"
	moveSharedToLocal = "shared-to-local"
)
```

**Checklist**:
- [x] Add `cmdMove`, `moveLocalToShared`, and `moveSharedToLocal` constants.
- [x] Preserve existing command names and completed command behavior.

#### internal/kinko/cobra_runtime.go

**Status**: COMPLETED

```go
func newMoveCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Register `newMoveCommand` on the root command.
- [x] Add `move local-to-shared KEY` and `move shared-to-local KEY` subcommands.
- [x] Add `--overwrite`, `--yes`, and `-y` flags to both move directions.
- [x] Reject missing keys, extra keys, and help-only parent positional arguments at the Cobra boundary.
- [x] Pass parsed direction and flags to runtime without exposing secret values.

---

### 2. Move Runtime Model and Parsing

#### internal/kinko/move.go

**Status**: COMPLETED

```go
type moveDirection string

const (
	moveDirectionLocalToShared moveDirection = "local-to-shared"
	moveDirectionSharedToLocal moveDirection = "shared-to-local"
)

type moveSecretOptions struct {
	Direction moveDirection
	Key       string
	Overwrite bool
	Yes       bool
}

type moveSecretResult struct {
	Direction moveDirection
	Key       string
	Profile   string
	Path      string
}

func runMove(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func parseMoveArgs(args []string) (moveSecretOptions, error)
```

**Checklist**:
- [x] Parse direction, key, `--overwrite`, `--yes`, and `-y`.
- [x] Require exactly one key after the direction.
- [x] Validate the key with the existing `validateEnvKey` rules.
- [x] Return deterministic errors for unknown direction, missing key, extra key, and invalid flags.

---

### 3. Scope Movement Mutation

#### internal/kinko/move.go

**Status**: COMPLETED

```go
func moveSecretBetweenScopes(vd *vaultData, opts globalOptions, moveOpts moveSecretOptions) (moveSecretResult, error)
func ensureMoveDestinationScope(vd *vaultData, opts globalOptions, direction moveDirection) map[string]string
func describeMoveScopes(opts globalOptions, direction moveDirection) (source string, destination string)
```

**Checklist**:
- [x] Use the current local `profiles[profile][path]` map as source for `local-to-shared`.
- [x] Use `vd.Shared` as source for `shared-to-local`.
- [x] Fail without mutation when the source key is missing.
- [x] Fail without mutation when the destination key exists and `--overwrite` is absent.
- [x] Create the local destination map for `shared-to-local` only after source and conflict checks pass.
- [x] Do not create an empty local scope for missing-source `local-to-shared`.
- [x] Write destination value and delete source key in the same in-memory vault snapshot before one `saveVault` call.
- [x] Never include the secret value in result data, prompts, stdout, stderr, or errors.

---

### 4. Runtime Flow and Output

#### internal/kinko/move.go

**Status**: COMPLETED

```go
func confirmMoveSecret(stdin io.Reader, stderr io.Writer, result moveSecretResult, source, destination string) (bool, error)
func renderMoveSecretSuccess(stdout io.Writer, result moveSecretResult, source, destination string) error
```

**Checklist**:
- [x] Acquire the existing vault mutation lock before loading the mutation snapshot.
- [x] Require an unlocked session through the existing mutation flow.
- [x] Prompt for confirmation unless `--yes` is set.
- [x] Confirmation output names only key and scopes, never value.
- [x] On declined confirmation, write `aborted` to stdout and leave vault data unchanged.
- [x] Write success output only after encrypted vault persistence succeeds.

---

### 5. Runtime Tests

#### internal/kinko/move_test.go

**Status**: COMPLETED

```go
func TestMoveLocalToSharedSuccess(t *testing.T)
func TestMoveSharedToLocalSuccess(t *testing.T)
func TestMoveRequiresOverwriteForDestinationConflict(t *testing.T)
func TestMoveOverwriteReplacesDestinationAndDeletesSource(t *testing.T)
func TestMoveMissingSourceDoesNotCreateDestinationScope(t *testing.T)
func TestMoveConfirmationDeclineLeavesVaultUnchanged(t *testing.T)
func TestMoveYesBypassesPromptOnly(t *testing.T)
func TestMoveDoesNotPrintSecretValues(t *testing.T)
func TestMovePreservesUnrelatedScopes(t *testing.T)
func TestMovePersistenceFailureLeavesVaultUnchanged(t *testing.T)
func TestMoveRejectsInvalidArguments(t *testing.T)
```

**Checklist**:
- [x] Cover both movement directions.
- [x] Cover missing source, destination conflict, overwrite success, confirmation decline, and `--yes`.
- [x] Assert source deletion and destination value preservation after success.
- [x] Assert unrelated shared keys, local keys, profiles, and paths remain unchanged.
- [x] Assert output and errors do not contain secret values.
- [x] Assert missing-source `local-to-shared` does not create local or shared data.
- [x] Assert missing-source `shared-to-local` does not create local profile/path maps.
- [x] Assert forced persistence failure leaves the previously persisted source and destination scopes unchanged.

---

### 6. Cobra and Documentation Tests

#### internal/kinko/cobra_runtime_test.go

**Status**: COMPLETED

```go
func TestCobraMoveCommands(t *testing.T)
func TestCobraMoveHelpIncludesDirections(t *testing.T)
func TestCobraMoveRejectsInvalidArgs(t *testing.T)
```

#### README.md

**Status**: COMPLETED

**Checklist**:
- [x] Add user-facing `kinko move local-to-shared KEY` and `kinko move shared-to-local KEY` documentation.
- [x] Document `--overwrite`, `--yes` / `-y`, no secret value output, and source deletion after successful persistence.
- [x] Document that vault format and scope precedence remain unchanged.
- [x] Add runtime-level Cobra tests for command execution, help visibility, and invalid argument rejection.

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| CLI command surface | `internal/kinko/constants.go`, `internal/kinko/cobra_runtime.go` | COMPLETED | `internal/kinko/cobra_runtime_test.go` |
| Runtime model and parsing | `internal/kinko/move.go` | COMPLETED | `internal/kinko/move_test.go` |
| Scope movement mutation | `internal/kinko/move.go` | COMPLETED | `internal/kinko/move_test.go` |
| Runtime flow and output | `internal/kinko/move.go` | COMPLETED | `internal/kinko/move_test.go` |
| Runtime tests | `internal/kinko/move_test.go` | COMPLETED | `go test ./internal/kinko` |
| Cobra and docs | `internal/kinko/cobra_runtime_test.go`, `README.md` | COMPLETED | `go test ./internal/kinko` |

## Dependencies

| Task | Depends On | Status |
|------|------------|--------|
| TASK-001 CLI command surface | Accepted design | COMPLETED |
| TASK-002 Runtime model and parsing | Accepted design | COMPLETED |
| TASK-003 Scope movement mutation | TASK-002 | COMPLETED |
| TASK-004 Runtime flow and output | TASK-002, TASK-003 | COMPLETED |
| TASK-005 Runtime tests | TASK-002, TASK-003, TASK-004 | COMPLETED |
| TASK-006 Cobra and documentation tests | TASK-001, TASK-004 | COMPLETED |

## Task Breakdown

### TASK-001: CLI Command Surface
**Status**: COMPLETED
**Parallelizable**: Yes, only until integration with runtime call wiring
**Deliverables**: `internal/kinko/constants.go`, `internal/kinko/cobra_runtime.go`

### TASK-002: Runtime Model and Parsing
**Status**: COMPLETED
**Parallelizable**: Yes
**Deliverables**: `internal/kinko/move.go`

### TASK-003: Scope Movement Mutation
**Status**: COMPLETED
**Parallelizable**: No, depends on TASK-002 and shares `move.go`
**Deliverables**: `internal/kinko/move.go`

### TASK-004: Runtime Flow and Output
**Status**: COMPLETED
**Parallelizable**: No, depends on TASK-002 and TASK-003
**Deliverables**: `internal/kinko/move.go`

### TASK-005: Runtime Tests
**Status**: COMPLETED
**Parallelizable**: Yes after TASK-002 through TASK-004 settle
**Deliverables**: `internal/kinko/move_test.go`

### TASK-006: Cobra Tests and README Documentation
**Status**: COMPLETED
**Parallelizable**: Yes after TASK-001 and TASK-004 settle
**Deliverables**: `internal/kinko/cobra_runtime_test.go`, `README.md`

## Verification

- [x] `gofmt -w internal/kinko/constants.go internal/kinko/cobra_runtime.go internal/kinko/move.go internal/kinko/move_test.go internal/kinko/cobra_runtime_test.go`
- [x] `go test ./internal/kinko`
- [x] `go test ./...`
- [x] `go test ./internal/kinko -run 'TestMove|TestCobraMove'`
- [x] `go test ./internal/kinko -run TestMovePersistenceFailureLeavesVaultUnchanged`
- [x] `go test ./internal/kinko -run 'TestCobraMoveCommands|TestCobraMoveHelpIncludesDirections|TestCobraMoveRejectsInvalidArgs'`
- [x] `git diff --check -- internal/kinko/constants.go internal/kinko/cobra_runtime.go internal/kinko/move.go internal/kinko/move_test.go internal/kinko/cobra_runtime_test.go README.md`

## Completion Criteria

- [x] `kinko move local-to-shared KEY` moves exactly one current profile/path key into shared scope.
- [x] `kinko move shared-to-local KEY` moves exactly one shared key into current profile/path scope.
- [x] Source key is deleted only when destination write and vault persistence succeed.
- [x] Persistence failure leaves the previously persisted encrypted vault state unchanged for both source and destination scopes.
- [x] Destination conflict fails without mutation unless `--overwrite` is set.
- [x] Missing source fails without creating or changing destination data.
- [x] Interactive confirmation and `--yes` semantics match the accepted design.
- [x] No output, prompt, error, test log, or documentation example exposes secret values from moved entries.
- [x] Existing encrypted vault format and shared/local precedence behavior are preserved.
- [x] Runtime, Cobra, and full Go test commands pass.
- [x] README documents the new user-facing commands and safety behavior.

## Progress Log

### Session: 2026-06-16 00:00
**Tasks Completed**: Implementation plan created from accepted Step 3 design.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Later implementation sessions must update task statuses, module checklists, verification checkboxes, and this progress log after each session. Go implementation must use `.agents/agents/go-coding.md` conventions and invoke `.agents/agents/check-and-test-after-modify.md` validation after Go file modifications.

### Session: 2026-06-16 00:10
**Tasks Completed**: Self-review tightened Cobra verification command to reference the planned move-specific Cobra tests.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: No design revision required.

### Session: 2026-06-16 00:20
**Tasks Completed**: Addressed Step 5 mid finding by adding explicit persistence-failure atomicity test coverage, verification command, and completion criterion.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Design already covered persistence-failure atomicity; only the implementation plan needed revision.

### Session: 2026-06-16 00:55
**Tasks Completed**: TASK-001 through TASK-006 completed. Implemented move runtime, Cobra command surface, runtime/Cobra tests, README documentation, and a macOS symlink-stable backup cwd assertion needed for the full package test run.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Verified with gofmt, targeted move tests, full `go test ./internal/kinko`, `go test ./...`, `go build -o /dev/null ./...`, `go vet ./...`, `go mod tidy`, and `git diff --check`.
