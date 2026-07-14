# BWS Sync Command Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/design-bws-sync.md
**Created**: 2026-07-13
**Last Updated**: 2026-07-13

## Related Plans

- **Previous / Depends On**: `impl-plans/completed/bws-sync-foundation.md`
  (machine id, migration, scope hash, bws client, sync state store)

---

## Design Document Reference

**Source**: design-docs/specs/design-bws-sync.md (sections: Command
Interface, Sync Semantics, Exit Codes, Security Considerations, Testing
Strategy)

### Summary

Implement `kinko sync push|pull --provider=bws` on top of the foundation
plan: enumerate every profile/scope of the vault into sync entries, classify
each entry against the remote secret list and the stored sync state (the
push/pull decision tables in the design), fail on conflicts with a new
`exitCodeSyncConflict` unless `--force`, apply mutations (remote via the bws
client for push; one atomic vault save for pull), rewrite the sync state,
and print a values-free summary (text or `--json`). Includes vault password
re-entry, mutation lock, project resolution, `--dry-run`, new exit codes
15/16, e2e tests against a stub bws binary, and user documentation.

### Scope

**Included**: entry collection, plan/classification engine, push and pull
executors, project resolution, `kinko sync` cobra wiring, exit codes,
stub-bws e2e tests, `design-docs/specs/command.md` + `README.md` updates.

**Excluded**: everything delivered by the foundation plan; providers other
than bws; cross-machine pull; per-key conflict resolution.

---

## Modules

### 1. Entry Collection and Plan Engine

#### internal/kinko/sync_plan.go

**Status**: COMPLETED

```go
// syncEntry is one local (profile, scope, key) = value unit.
type syncEntry struct {
    ref   scopeRef
    key   string
    value string
}

// collectSyncEntries flattens vaultData (every profile's path scopes plus
// the vault-wide shared map) into entries, excludes the reserved
// sharedKeyBWSAccessToken key, and runs detectScopeHashCollisions over the
// refs.
func collectSyncEntries(data *vaultData) ([]syncEntry, error)

type syncActionKind int

const (
    syncActionCreate    syncActionKind = iota // write to destination
    syncActionUpdate                          // overwrite destination value
    syncActionDelete                          // delete from destination
    syncActionAdopt                           // values equal; record state only
    syncActionUnchanged                       // nothing to do
    syncActionConflict                        // divergence; blocked without --force
    syncActionIgnore                          // not owned by this sync
)

type syncAction struct {
    kind    syncActionKind
    name    string     // BWS secret name
    ref     scopeRef    // normalized local, remote, or baseline scope
    entry   *syncEntry // local or machine-owned remote entry; nil for stale-state-only rows
    remote  *bwsSecret // nil when absent remotely
    forced  syncActionKind // resolution kind under --force (conflicts only)
    reason  string         // human-readable, values-free
}

type syncPlan struct {
    actions   []syncAction
    conflicts []syncAction // subset of actions with kind==syncActionConflict
    direction syncDirection
}

// buildPushPlan / buildPullPlan implement the two decision tables from
// design-docs/specs/design-bws-sync.md#sync-semantics. Remote validation
// happens first: duplicate machine-owned names or note/name mismatches
// (verifyNoteMatchesName) return a policy error, not a plan.
func buildPushPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error)
func buildPullPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error)
```

**Checklist**:
- [x] Every row of both decision tables covered by table-driven tests,
      including the both-sides-deleted row (drop stale state record)
- [x] Remote validation errors (duplicates, bad notes) are policy errors
      even with `--force`
- [x] Reserved `KINKO_BWS_ACCESS_TOKEN` key excluded on push (collect) and
      ignored on pull (remote entry with that key name)
- [x] Pull creates missing profiles/path scopes named by remote notes
- [x] Deterministic action ordering (stable output/tests)

---

### 2. Push and Pull Executors

#### internal/kinko/sync_exec.go

**Status**: COMPLETED

```go
// syncResult summarizes an applied (or dry-run) plan; no values.
type syncResult struct {
    Created   int
    Updated   int
    Deleted   int
    Unchanged int
    Adopted   int
    Conflicts []string // "profile / scope-label / KEY: reason"
    Actions   []syncResultItem
    Partial   bool     // push only: a bws call failed midway
}

// applyPushPlan validates first, then per action calls
// createSecret/editSecret/deleteSecrets, updating state entries for exactly
// the actions that succeeded. Deletes are sent one at a time so a midway bws
// failure can retain the exact applied prefix. On failure it returns the error
// with Partial=true, including an uncertain first-delete failure, and the state
// reflecting only the confirmed prefix (re-running push converges).
func applyPushPlan(ctx context.Context, client *bwsClient, projectID string, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error)

// applyPullPlan mutates vaultData in memory and returns the new state; the
// caller persists with one saveVault + saveConfig under the mutation lock.
func applyPullPlan(data *vaultData, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error)
```

**Checklist**:
- [x] Conflicts without force: no mutation at all, error carries all
      conflicts
- [x] Force resolves conflicts in the command's direction only
- [x] Pull is all-or-nothing for vault data (single `saveVault`), followed
      by the state `saveConfig` (vault first; crash window degrades
      conservatively); push partial failure reports applied prefix and
      persists matching state
- [x] Create/edit response that fails to parse counts as a failed action:
      no state recorded; next push heals via the adopt row
- [x] State `revisionDate` recorded from the create/edit response object,
      or from the list snapshot for adopt/unchanged baselines
- [x] Unit tests with fake bws runner incl. midway failure

---

### 3. Orchestration, Project Resolution, Output

#### internal/kinko/sync_run.go

**Status**: COMPLETED

```go
type syncDirection string

const (
    syncDirectionPush syncDirection = "push"
    syncDirectionPull syncDirection = "pull"
)

type syncOptions struct {
    direction syncDirection
    provider  string // must be "bws" in v1
    force     bool
    dryRun    bool
    projectID string
    jsonOut   bool
}

const envKinkoBWSProjectID = "KINKO_BWS_PROJECT_ID"
const configKeyBWSProjectID = "sync.bws.project_id"

// resolveBWSProjectID: flag > env > encrypted config > sole accessible
// project (reported on stderr) > policy error listing all four options.
func resolveBWSProjectID(ctx context.Context, client *bwsClient, cfg map[string]string, flagValue string, stderr io.Writer) (string, error)

// runSyncWithOptions: validate flags -> require machine id (policy error
// hinting `kinko migration`) -> vault password re-entry (cross-scope
// policy; reuse the verifyVaultPasswordDEKForShow/readVaultPasswordDEK
// pattern — pick ONE shared helper, do not add a third near-duplicate) ->
// acquire mutation lock -> load vault+config+state ->
// resolveBWSAccessToken (env else shared secret) -> newBWSClient -> bws
// list -> build plan -> dry-run prints plan and stops -> apply -> persist
// vault (pull) then state -> print summary / --json.
func runSyncWithOptions(opts globalOptions, syncOpts syncOptions, stdin io.Reader, stdout, stderr io.Writer) error

func printSyncSummary(w io.Writer, res syncResult, jsonOut bool) error
```

**Checklist**:
- [x] Password re-entry before any scope enumeration or remote call
- [x] `--dry-run` mutates nothing: vault, config/state, and remote all
      byte-identical afterward
- [x] Project-id mismatch vs state.ProjectID prints a warning
- [x] Output carries names/scopes/counts only — never values

---

### 4. Cobra Wiring and Exit Codes

#### internal/kinko/cobra_runtime.go, internal/kinko/constants.go, internal/kinko/cli_error.go (modifications)

**Status**: COMPLETED

```go
const (
    cmdSync     = "sync"
    cmdSyncPush = "push"
    cmdSyncPull = "pull"
)

const (
    exitCodeSyncConflict   = 15
    exitCodeProviderFailed = 16
)

// newSyncCommand builds `kinko sync` with push/pull subcommands sharing
// --provider (required, "bws"), --force, --dry-run, --project-id, --json.
func newSyncCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Registered on root; unknown provider -> policy error naming `bws`
- [x] Conflict -> 15; bws binary missing/non-zero/bad JSON/timeout -> 16;
      other failures use existing 10-14 mapping
- [x] Cobra regression subtests (flag surface, provider required,
      push/pull arg shape)

---

### 5. Stub-bws E2E Tests

#### internal/kinko/sync_e2e_test.go, internal/kinko/testdata (stub binary source)

**Status**: COMPLETED

```go
// buildStubBWS compiles a stub bws (Go test helper via os.Exec pattern or
// TestMain-built binary) into t.TempDir and returns its path for
// KINKO_BWS_BIN. The stub journals calls and serves a scripted secret set,
// supporting scenarios: success, non-zero exit, garbage JSON, slow
// response, duplicate names, malformed notes, revision drift.
func buildStubBWS(t *testing.T, scenario string) string
```

**Checklist**:
- [x] init -> set (multi profile/scope) -> push creates expected names/notes
- [x] local edit -> push updates; remote drift -> conflict (15) -> `--force`
- [x] pull round-trip into a second vault fixture sharing the machine id;
      deletion propagation both directions
- [x] `--dry-run` leaves vault/state/stub journal untouched
- [x] token isolation: stub asserts child env has injected
      `BWS_ACCESS_TOKEN`, and parent-env `BWS_ACCESS_TOKEN` never reaches it
- [x] token from shared secret: no env var set, token stored via
      `set --shared KINKO_BWS_ACCESS_TOKEN=...`, sync works, and the
      reserved key itself is never pushed

---

### 6. Documentation

#### design-docs/specs/command.md, README.md, impl-plans/README.md (modifications)

**Status**: COMPLETED

**Checklist**:
- [x] `kinko sync` + `kinko migration` sections in command.md (flags,
      examples, exit-code rows 15/16), cross-referencing design-bws-sync.md
- [x] README.md: sync usage section incl. `KINKO_BWS_ACCESS_TOKEN` /
      `KINKO_BWS_PROJECT_ID` / `KINKO_BWS_BIN` and token-isolation note
- [x] impl-plans/README.md tables updated

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Plan engine | `internal/kinko/sync_plan.go` | COMPLETED | `sync_plan_test.go` |
| Executors | `internal/kinko/sync_exec.go` | COMPLETED | `sync_exec_test.go` |
| Orchestration | `internal/kinko/sync_run.go` | COMPLETED | `sync_run_test.go`, `sync_e2e_test.go` |
| Cobra + exit codes | `cobra_runtime.go`, `constants.go`, `cli_error.go` | COMPLETED | `sync_cobra_test.go`, `sync_e2e_test.go` |
| Stub-bws e2e | `internal/kinko/sync_e2e_test.go` | COMPLETED | `sync_e2e_test.go` |
| Docs | `command.md`, `README.md` | COMPLETED | reviewed against shipped flags/output |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| All modules | `bws-sync-foundation.md` complete | SATISFIED |
| Module 2 | Module 1 (plan types) | SATISFIED |
| Module 3 | Modules 1, 2 | SATISFIED |
| Module 4 | Module 3 (`syncOptions`, `runSyncWithOptions`) | SATISFIED |
| Module 5 | Modules 1-4 | SATISFIED |
| Module 6 | Modules 3, 4 (final behavior) | SATISFIED |

## Subtasks

### TASK-001: Entry collection + plan engine (Module 1)
**Status**: COMPLETED / **Parallelizable**: Yes (once foundation lands)

### TASK-002: Executors (Module 2)
**Status**: COMPLETED / **Parallelizable**: No (TASK-001)

### TASK-003: Orchestration + project resolution (Module 3)
**Status**: COMPLETED / **Parallelizable**: No (TASK-001, TASK-002)

### TASK-004: Cobra wiring + exit codes (Module 4)
**Status**: COMPLETED / **Parallelizable**: No (TASK-003)

### TASK-005: Stub-bws e2e suite (Module 5)
**Status**: COMPLETED / **Parallelizable**: No (TASK-001..004)

### TASK-006: Documentation (Module 6)
**Status**: COMPLETED / **Parallelizable**: No (TASK-003, TASK-004)

## Completion Criteria

- [x] All modules implemented; every decision-table row unit-tested
- [x] E2E scenarios from design Testing Strategy pass against the stub bws
- [x] Conflict exit 15 / provider exit 16 verified end to end
- [x] `go build ./...`, `go vet ./...`, `go test ./...` pass
- [x] `check-and-test-after-modify` agent run after every Go change
- [x] command.md / README.md updated; plan moved to `impl-plans/completed/`

## Progress Log

### Session: 2026-07-13
**Tasks Completed**: (plan created)
**Notes**: Plan authored from design-docs/specs/design-bws-sync.md; blocked
on `bws-sync-foundation.md`.

### Session: 2026-07-13 (implementation and archival verification)
**Tasks Completed**: TASK-001 through TASK-006
**Evidence**:
- Both 12-row decision tables, equal concurrent changes, stale baselines,
  force direction, deterministic multi-conflict output, reserved keys,
  duplicate names/ids, local and remote scope collisions, and malformed notes
  are covered by `sync_plan_test.go` and `sync_exec_test.go`.
- Stub-BWS tests cover argv shape, isolated environment, first sync, updates,
  remote drift, both deletion directions, second-vault pull, dry-run byte
  identity, partial-prefix persistence, provider failure exits, and redaction.
- Loaded state entries are semantically validated before planning: normalized
  scope/name/key identity, machine/project presence, unique secret ids,
  revision, lowercase SHA-256, reserved key exclusion, and scope collisions.
- Required independent verification passed `go mod tidy` without module-file
  changes, `gofmt`, `go build -o /dev/null ./...`, focused tests, focused race
  tests, `go test ./... -count=1`, `go vet ./...`, `git diff --check`, and the
  repository-wide Go file limit.
- Security gates passed with Go 1.25.8: `go mod verify`,
  `govulncheck@v1.6.0 ./...` (zero called vulnerabilities),
  `golangci-lint run ./... --new-from-rev=HEAD` (zero new issues), and the
  secure-subprocess forbidden-pattern audit.

**Exact verification commands**:
- `go mod tidy`
- `gofmt -l internal/kinko/*.go`
- `go build -o /dev/null ./...`
- `go test ./internal/kinko -count=1 -timeout=10m -run "Test(BWS|BuildPush|BuildPull|BuildPlans|CollectSync|ApplyPush|ApplyPull|ResolveBWS|Sync|RunSync|PrintSync|CobraSync|DeriveScope|DetectScope|ParseBWS|NewMachineID|MachineID|InitVaultPopulates|Migration|LoadBWSSyncState|SaveBWSSyncState)"`
- `go test -race ./internal/kinko -count=1 -timeout=10m -run "Test(BWS|BuildPush|BuildPull|BuildPlans|CollectSync|ApplyPush|ApplyPull|ResolveBWS|Sync|RunSync|PrintSync|CobraSync|DeriveScope|DetectScope|ParseBWS|NewMachineID|MachineID|InitVaultPopulates|Migration|LoadBWSSyncState|SaveBWSSyncState)"`
- `go test ./... -count=1`
- `go vet ./...`
- `git diff --check`
- `find . -type f -name "*.go" -not -path "./.git/*" -not -path "./.direnv/*" -exec wc -l {} + | awk '$2 != "total" && $1 >= 1000 { print }'`
- `go mod verify`
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`
- `golangci-lint run ./... --new-from-rev=HEAD`

**Notes**: Documentation matches the shipped command and automatic deletion
propagation semantics. All completion and archive gates are satisfied.

### Session: 2026-07-13 (project, identity, and I/O review revisions)
**Tasks Completed**: TASK-001, TASK-002, TASK-003, and TASK-005 review hardening
**Evidence**:
- A changed BWS project now discards the previous project's baselines before
  planning. A forced pull from an empty new project preserves unchanged local
  secrets and records the new project as a never-synced baseline.
- Provider secret ids are validated for global uniqueness before machine
  ownership and reserved-key filtering; cross-machine and reserved-key
  collisions remain policy failures under `--force`.
- Sync metadata and encrypted-file path failures map to local I/O exit 13,
  including non-permission `os.PathError` values and the locked metadata
  reload, while malformed decrypted content remains metadata exit 14.
- Create/edit response objects are checked against each request's applicable
  id, project, key, value, and note fields before their revisions enter sync
  state. Mismatched responses leave the affected state entry unchanged.
- The implementation verification passed `go mod tidy`, formatting,
  `go build -o /dev/null ./...`, `go test ./...`, `go vet ./...`,
  `git diff --check`, and the repository-wide Go file limit. The independent
  post-modification agent separately passed the focused regressions with and
  without the race detector.

**Exact review verification commands**:
- `go test ./internal/kinko -run '^(TestSyncProjectMismatchWarningAndPartialPushPersistence|TestBuildPlansRemoteValidationAndReservedKey|TestSyncLocalDataErrorClassification|TestLoadSyncMetadataClassifiesReadAndContentFailures|TestRunSyncClassifiesEncryptedFileReadAndMetadataFailures|TestBWSClientMutationResponsesMustMatchRequests|TestApplyPushPlanDoesNotPersistMismatchedMutationResponses|TestApplyPushPlanInvalidMutationResponseHealsThroughAdopt)$'`
- `go test -race ./internal/kinko -run '^(TestSyncProjectMismatchWarningAndPartialPushPersistence|TestBuildPlansRemoteValidationAndReservedKey|TestSyncLocalDataErrorClassification|TestLoadSyncMetadataClassifiesReadAndContentFailures|TestRunSyncClassifiesEncryptedFileReadAndMetadataFailures|TestBWSClientMutationResponsesMustMatchRequests|TestApplyPushPlanDoesNotPersistMismatchedMutationResponses|TestApplyPushPlanInvalidMutationResponseHealsThroughAdopt)$'`
- `go mod tidy`
- `go build -o /dev/null ./...`
- `go test ./...`
- `go vet ./...`
- `git diff --check`

**Notes**: All four high/middle review findings are resolved. The completed
plan remains archived; no new implementation plan was created.

### Session: 2026-07-13 (plain-text delete-output review revision)
**Tasks Completed**: TASK-002 and TASK-005 delete-path compatibility coverage
**Changed Files**:
- `internal/kinko/bws_client.go`
- `internal/kinko/bws_client_test.go`
- `internal/kinko/sync_exec_test.go`
- `internal/kinko/sync_e2e_test.go`

**Evidence**:
- `deleteSecrets` accepts zero-exit `bws secret delete` output without JSON
  parsing, matching the CLI's singular and plural plain-text success output.
- Client tests cover both real output forms and preserve redacted provider
  errors for nonzero deletes. Executor tests cover per-secret deletion, sync
  state cleanup after singular output, and exact applied-prefix state when a
  later delete fails.
- The stub-BWS end-to-end path emits the real singular success form used by
  per-secret execution and verifies that push deletion updates remote data and
  encrypted sync state without a false provider failure.
- No additional Go behavior, staging, commit, push, remote mutation, or Riela
  workflow was required for this documentation correction.

**Exact verification commands**:
- `go test ./... -count=1`
- `go vet ./...`
- `git diff --check`
- `find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 wc -l | awk '$1 >= 1000 && $2 != "total" { print; bad=1 } END { exit bad }'`

**Notes**: Final plain-text delete behavior, changed files, regression coverage,
and exact verification evidence are now recorded. The completed command plan
remains archived.

### Session: 2026-07-13 (final ambiguity and delete-uncertainty revision)

**Tasks Completed**: TASK-001, TASK-002, TASK-005, TASK-006, and archive gate

**Changed Files**:
- `internal/kinko/sync_plan.go`
- `internal/kinko/sync_exec.go`
- `internal/kinko/sync_plan_test.go`
- `internal/kinko/sync_exec_test.go`
- `internal/kinko/sync_reserved_e2e_test.go`
- `design-docs/specs/design-bws-sync.md`
- `impl-plans/completed/bws-sync-command.md`
- `impl-plans/completed/bws-sync-foundation.md`
- `impl-plans/README.md`

**Evidence**:
- Remote-name ambiguity is recorded before reserved-token filtering. Duplicate
  machine-owned `KINKO_BWS_ACCESS_TOKEN` names now fail with policy exit `11`
  for push, pull, and both `--force` variants without provider mutation or
  encrypted local-file changes.
- Push executes deletes individually. Every delete provider failure is marked
  partial/uncertain, including the first delete, while state and result actions
  contain only the exact confirmed successful prefix.
- Provider diagnostics remain redacted and visible, sync help describes
  direction-authoritative `--force`, and push/pull reject `--prune` with policy
  exit `11` before password or provider activity.
- The provider design now distinguishes JSON-bearing list/create/edit success
  from opaque zero-exit delete confirmation output.

**Exact final verification**:
- `go mod tidy` preserved `go.mod` and `go.sum` hashes.
- `gofmt -l $(rg --files -g '*.go')` produced no output.
- `go build -o /dev/null ./...` passed.
- `go vet ./...` passed.
- `go test ./... -count=1` passed: 450 top-level tests plus subtests;
  `internal/kinko` completed in 105.761s.
- `go test -race ./internal/kinko -count=1 -run 'Test(BWS|NewBWS|BuildPushPlan|BuildPullPlan|BuildPlans|ApplyPushPlan|ApplyPullPlan|Sync|LoadBWSSyncState|SaveBWSSyncState|RunSync)'`
  passed in 80.929s with 46 top-level tests, including reserved-duplicate and
  delete-prefix regressions.
- `go mod verify` passed with all modules verified.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` reported no called
  vulnerabilities.
- `nix develop -c golangci-lint run ./... --new-from-rev=HEAD` passed with
  zero issues after removal of the final ineffectual assignment.
- `nix build 'path:.#kinko' --no-link` passed.
- `git diff --check`, the forbidden subprocess/credential/local-path audit,
  and the repository-wide under-1000-line Go-file gate passed.

**Notes**: The Riela adversarial review finding is resolved. The plan is ready
for final archival and a fresh Riela acceptance review. Nothing was staged,
committed, pushed, or remotely mutated.
