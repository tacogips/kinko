# BWS Sync Command Implementation Plan

**Status**: Planning (blocked on foundation plan)
**Design Reference**: design-docs/specs/design-bws-sync.md
**Created**: 2026-07-13
**Last Updated**: 2026-07-13

## Related Plans

- **Previous / Depends On**: `impl-plans/active/bws-sync-foundation.md`
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

**Status**: NOT_STARTED

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
    entry   *syncEntry // nil for remote-only rows
    remote  *bwsSecret // nil when absent remotely
    forced  syncActionKind // resolution kind under --force (conflicts only)
    reason  string         // human-readable, values-free
}

type syncPlan struct {
    actions   []syncAction
    conflicts []syncAction // subset of actions with kind==syncActionConflict
}

// buildPushPlan / buildPullPlan implement the two decision tables from
// design-docs/specs/design-bws-sync.md#sync-semantics. Remote validation
// happens first: duplicate machine-owned names or note/name mismatches
// (verifyNoteMatchesName) return a policy error, not a plan.
func buildPushPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error)
func buildPullPlan(entries []syncEntry, remote []bwsSecret, state *bwsSyncState, machineID string) (*syncPlan, error)
```

**Checklist**:
- [ ] Every row of both decision tables covered by table-driven tests,
      including the both-sides-deleted row (drop stale state record)
- [ ] Remote validation errors (duplicates, bad notes) are policy errors
      even with `--force`
- [ ] Reserved `KINKO_BWS_ACCESS_TOKEN` key excluded on push (collect) and
      ignored on pull (remote entry with that key name)
- [ ] Pull creates missing profiles/path scopes named by remote notes
- [ ] Deterministic action ordering (stable output/tests)

---

### 2. Push and Pull Executors

#### internal/kinko/sync_exec.go

**Status**: NOT_STARTED

```go
// syncResult summarizes an applied (or dry-run) plan; no values.
type syncResult struct {
    Created   int
    Updated   int
    Deleted   int
    Unchanged int
    Adopted   int
    Conflicts []string // "profile / scope-label / KEY: reason"
    Partial   bool     // push only: a bws call failed midway
}

// applyPushPlan validates first, then per action calls
// createSecret/editSecret/deleteSecrets (batched), updating state entries
// for exactly the actions that succeeded. On a midway bws failure it
// returns the error with Partial=true and the state already reflecting the
// applied prefix (re-running push converges).
func applyPushPlan(ctx context.Context, client *bwsClient, projectID string, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error)

// applyPullPlan mutates vaultData in memory and returns the new state; the
// caller persists with one saveVault + saveConfig under the mutation lock.
func applyPullPlan(data *vaultData, plan *syncPlan, state *bwsSyncState, force bool) (syncResult, error)
```

**Checklist**:
- [ ] Conflicts without force: no mutation at all, error carries all
      conflicts
- [ ] Force resolves conflicts in the command's direction only
- [ ] Pull is all-or-nothing for vault data (single `saveVault`), followed
      by the state `saveConfig` (vault first; crash window degrades
      conservatively); push partial failure reports applied prefix and
      persists matching state
- [ ] Create/edit response that fails to parse counts as a failed action:
      no state recorded; next push heals via the adopt row
- [ ] State `revisionDate` recorded from the create/edit response object,
      or from the list snapshot for adopt/unchanged baselines
- [ ] Unit tests with fake bws runner incl. midway failure

---

### 3. Orchestration, Project Resolution, Output

#### internal/kinko/sync_run.go

**Status**: NOT_STARTED

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
- [ ] Password re-entry before any scope enumeration or remote call
- [ ] `--dry-run` mutates nothing: vault, config/state, and remote all
      byte-identical afterward
- [ ] Project-id mismatch vs state.ProjectID prints a warning
- [ ] Output carries names/scopes/counts only — never values

---

### 4. Cobra Wiring and Exit Codes

#### internal/kinko/cobra_runtime.go, internal/kinko/constants.go, internal/kinko/cli_error.go (modifications)

**Status**: NOT_STARTED

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
- [ ] Registered on root; unknown provider -> policy error naming `bws`
- [ ] Conflict -> 15; bws binary missing/non-zero/bad JSON/timeout -> 16;
      other failures use existing 10-14 mapping
- [ ] Cobra regression subtests (flag surface, provider required,
      push/pull arg shape)

---

### 5. Stub-bws E2E Tests

#### internal/kinko/sync_e2e_test.go, internal/kinko/testdata (stub binary source)

**Status**: NOT_STARTED

```go
// buildStubBWS compiles a stub bws (Go test helper via os.Exec pattern or
// TestMain-built binary) into t.TempDir and returns its path for
// KINKO_BWS_BIN. The stub journals calls and serves a scripted secret set,
// supporting scenarios: success, non-zero exit, garbage JSON, slow
// response, duplicate names, malformed notes, revision drift.
func buildStubBWS(t *testing.T, scenario string) string
```

**Checklist**:
- [ ] init -> set (multi profile/scope) -> push creates expected names/notes
- [ ] local edit -> push updates; remote drift -> conflict (15) -> `--force`
- [ ] pull round-trip into a second vault fixture sharing the machine id;
      deletion propagation both directions
- [ ] `--dry-run` leaves vault/state/stub journal untouched
- [ ] token isolation: stub asserts child env has injected
      `BWS_ACCESS_TOKEN`, and parent-env `BWS_ACCESS_TOKEN` never reaches it
- [ ] token from shared secret: no env var set, token stored via
      `set --shared KINKO_BWS_ACCESS_TOKEN=...`, sync works, and the
      reserved key itself is never pushed

---

### 6. Documentation

#### design-docs/specs/command.md, README.md, impl-plans/README.md (modifications)

**Status**: NOT_STARTED

**Checklist**:
- [ ] `kinko sync` + `kinko migration` sections in command.md (flags,
      examples, exit-code rows 15/16), cross-referencing design-bws-sync.md
- [ ] README.md: sync usage section incl. `KINKO_BWS_ACCESS_TOKEN` /
      `KINKO_BWS_PROJECT_ID` / `KINKO_BWS_BIN` and token-isolation note
- [ ] impl-plans/README.md tables updated

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Plan engine | `internal/kinko/sync_plan.go` | NOT_STARTED | - |
| Executors | `internal/kinko/sync_exec.go` | NOT_STARTED | - |
| Orchestration | `internal/kinko/sync_run.go` | NOT_STARTED | - |
| Cobra + exit codes | `cobra_runtime.go`, `constants.go`, `cli_error.go` | NOT_STARTED | - |
| Stub-bws e2e | `internal/kinko/sync_e2e_test.go` | NOT_STARTED | - |
| Docs | `command.md`, `README.md` | NOT_STARTED | - |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| All modules | `bws-sync-foundation.md` complete | BLOCKED |
| Module 2 | Module 1 (plan types) | BLOCKED |
| Module 3 | Modules 1, 2 | BLOCKED |
| Module 4 | Module 3 (`syncOptions`, `runSyncWithOptions`) | BLOCKED |
| Module 5 | Modules 1-4 | BLOCKED |
| Module 6 | Modules 3, 4 (final behavior) | BLOCKED |

## Subtasks

### TASK-001: Entry collection + plan engine (Module 1)
**Status**: NOT_STARTED / **Parallelizable**: Yes (once foundation lands)

### TASK-002: Executors (Module 2)
**Status**: NOT_STARTED / **Parallelizable**: No (TASK-001)

### TASK-003: Orchestration + project resolution (Module 3)
**Status**: NOT_STARTED / **Parallelizable**: No (TASK-001, TASK-002)

### TASK-004: Cobra wiring + exit codes (Module 4)
**Status**: NOT_STARTED / **Parallelizable**: No (TASK-003)

### TASK-005: Stub-bws e2e suite (Module 5)
**Status**: NOT_STARTED / **Parallelizable**: No (TASK-001..004)

### TASK-006: Documentation (Module 6)
**Status**: NOT_STARTED / **Parallelizable**: No (TASK-003, TASK-004)

## Completion Criteria

- [ ] All modules implemented; every decision-table row unit-tested
- [ ] E2E scenarios from design Testing Strategy pass against the stub bws
- [ ] Conflict exit 15 / provider exit 16 verified end to end
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass
- [ ] `check-and-test-after-modify` agent run after every Go change
- [ ] command.md / README.md updated; plan moved to `impl-plans/completed/`

## Progress Log

### Session: 2026-07-13
**Tasks Completed**: (plan created)
**Notes**: Plan authored from design-docs/specs/design-bws-sync.md; blocked
on `bws-sync-foundation.md`.
