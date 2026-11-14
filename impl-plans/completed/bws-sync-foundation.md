# BWS Sync Foundation Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/design-bws-sync.md
**Created**: 2026-07-13
**Last Updated**: 2026-07-13

## Related Plans

- **Next**: `impl-plans/completed/bws-sync-command.md` (sync engine + CLI; depends on every module here)

---

## Design Document Reference

**Source**: design-docs/specs/design-bws-sync.md (sections: Machine ID,
Scope Hash, Remote Data Model, Access Token Isolation, `kinko migration`,
Sync state, bws Invocation Layer)

### Summary

Build the four independent foundations for BWS sync: (1) the per-vault
machine id (`machine_id` in `meta.v1.json`, generated at `kinko init`,
backfilled by a new `kinko migration` command, warned about by `kinko
doctor`), (2) the deterministic 8-hex scope hash plus the BWS secret-name
grammar and note-metadata codec, (3) the `bws` subprocess client with strict
token isolation (token from `KINKO_BWS_ACCESS_TOKEN` env or the shared
secret of the same name, minimal child env, argv-free token, stderr
redaction), and (4) the encrypted sync-state store under the reserved
config key `sync.bws.v1`.

### Scope

**Included**: `vaultMeta.MachineID` + init generation + doctor warning;
`kinko migration` command (preview/`--yes`/`--json`, cobra wiring,
constants); scope hash / name format / note metadata with collision
detection; bws client wrapper with injectable runner for tests; sync-state
load/save over the existing encrypted config.

**Excluded**: the `kinko sync` command itself, push/pull planning and
execution, project resolution, exit codes 15/16, user docs for sync (all in
`bws-sync-command.md`).

---

## Modules

### 1. Machine ID

#### internal/kinko/vault.go (modification), internal/kinko/app.go (modification), internal/kinko/doctor.go (modification)

**Status**: COMPLETED

```go
// vaultMeta gains one optional, backward-compatible field.
type vaultMeta struct {
    // ... existing fields unchanged ...
    MachineID string `json:"machine_id,omitempty"` // 16 lowercase hex chars
}

// newMachineID returns 16 lowercase hex chars from 8 crypto/rand bytes.
func newMachineID() (string, error)

// isValidMachineID reports whether s is exactly 16 lowercase hex chars.
func isValidMachineID(s string) bool
```

**Checklist**:
- [x] Add `MachineID` to `vaultMeta`; `initVault` sets it via `newMachineID`
- [x] Old metas (field absent) still load; no meta version bump
- [x] `kinko doctor` (`runDoctor`, works from `loadMeta` without unlocking)
      warns when `MachineID` is empty, hinting `kinko migration`
- [x] Unit tests: init populates it; legacy meta loads with empty id; validity

---

### 2. `kinko migration` Command

#### internal/kinko/migration.go, internal/kinko/cobra_runtime.go (modification), internal/kinko/constants.go (modification)

**Status**: COMPLETED

```go
type migrationOptions struct {
    yes     bool
    jsonOut bool
}

// migrationStep is one named vault-format migration; v1 ships exactly
// "assign-machine-id".
type migrationStep struct {
    name    string
    pending func(meta *vaultMeta) bool
    apply   func(meta *vaultMeta) error
}

func migrationSteps() []migrationStep

// runMigrationWithOptions: load meta -> list pending steps -> preview
// (default) or, with --yes, verify vault password, acquire mutation lock,
// apply steps, persist via saveMetaAtomically (password_change.go; NOT the
// non-atomic saveMeta). No pending steps => "no pending migrations", exit 0.
func runMigrationWithOptions(opts globalOptions, migOpts migrationOptions, stdin io.Reader, stdout, stderr io.Writer) error

func newMigrationCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] `cmdMigration = "migration"` constant; command registered on root
- [x] Preview default; `--yes` (with `-y` shorthand) applies after password
      verification (`unwrapDEKWithPassword`) under `acquireMutationLock`
- [x] Atomic meta rewrite; `--json` output (step names + pending/applied)
- [x] Idempotent: second run reports nothing pending
- [x] Errors map via `newCLIError` to existing exit codes (auth 10, policy
      11, lock 12, io 13, metadata 14); lock failures must use
      `newCLIError(exitCodeLockConflict, ...)`, not prune-missing's plain
      `fmt.Errorf` (which exits 1)
- [x] Unit tests + cobra regression subtests

---

### 3. Scope Hash, Name Grammar, Note Metadata

#### internal/kinko/sync_scope.go

**Status**: COMPLETED

```go
type scopeKind string

const (
    scopeKindPath   scopeKind = "path"
    scopeKindShared scopeKind = "shared"
)

// scopeRef identifies one scope. Path scopes carry (profile, path); the
// shared scope is vault-wide (vaultData.Shared is a single map), so its
// ref has kind=scopeKindShared with empty profile and path.
type scopeRef struct {
    profile string // empty for shared
    kind    scopeKind
    path    string // normalized absolute path; empty for shared
}

// deriveScopeHash: hex(SHA-256(join("\x00", "kinko.scope.v1", profile,
// kind, path))[:4]) — 8 lowercase hex chars (design: Scope Hash).
func deriveScopeHash(ref scopeRef) string

// buildBWSSecretName returns "{machineID}_{scopeHash}_{key}".
func buildBWSSecretName(machineID string, ref scopeRef, key string) string

// parseBWSSecretName splits a fixed-width-prefixed name owned by machineID;
// ok=false when the name belongs to another machine or is malformed.
func parseBWSSecretName(machineID, name string) (scopeHash, key string, ok bool)

// detectScopeHashCollisions fails (policy) when two distinct refs collide.
func detectScopeHashCollisions(refs []scopeRef) error

// bwsNoteMetadata is the JSON stored in each BWS secret's note field.
type bwsNoteMetadata struct {
    KinkoSyncFormat int    `json:"kinko_sync_format"` // 1
    MachineID       string `json:"machine_id"`
    Profile         string `json:"profile,omitempty"` // absent for shared
    Scope           string `json:"scope"`             // "path" | "shared"
    Path            string `json:"path,omitempty"`    // absent for shared
    Key             string `json:"key"`
}

func encodeBWSNote(m bwsNoteMetadata) (string, error)
func parseBWSNote(note string) (bwsNoteMetadata, error)

// verifyNoteMatchesName recomputes the scope hash from the note fields and
// cross-checks machine id, hash, and key against the parsed name.
func verifyNoteMatchesName(machineID, name string, m bwsNoteMetadata) error
```

**Checklist**:
- [x] All functions implemented; path normalization composes
      `normalizePathInput` + `filepath.Abs` + `filepath.Clean` (the
      `normalizeStoredScopePathForPrune` pattern) — `normalizePathInput`
      alone is not trailing-slash-insensitive
- [x] Unit tests: fixed hash vectors; profile/shared separation;
      trailing-slash equivalence; name build/parse round-trip with
      underscore-bearing keys; other-machine names rejected; collision
      detection; note encode/parse/verify including mismatch cases

---

### 4. bws Subprocess Client

#### internal/kinko/bws_client.go

**Status**: COMPLETED

```go
const (
    envKinkoBWSAccessToken    = "KINKO_BWS_ACCESS_TOKEN"
    sharedKeyBWSAccessToken   = "KINKO_BWS_ACCESS_TOKEN" // shared-scope secret name; excluded from sync
    envKinkoBWSBin            = "KINKO_BWS_BIN"
    envBWSAccessToken         = "BWS_ACCESS_TOKEN" // child-only; never read
    bwsCallTimeout            = 30 * time.Second
)

// resolveBWSAccessToken: env var first (stderr notice when it shadows the
// shared secret), else the shared-scope secret sharedKeyBWSAccessToken from
// the already-decrypted vault data; empty result is a policy error naming
// both sources.
func resolveBWSAccessToken(getenv func(string) string, shared map[string]string, stderr io.Writer) (string, error)

// bwsSecret mirrors bws --output json secret objects.
type bwsSecret struct {
    ID             string `json:"id"`
    OrganizationID string `json:"organizationId"`
    ProjectID      string `json:"projectId"`
    Key            string `json:"key"`
    Value          string `json:"value"`
    Note           string `json:"note"`
    CreationDate   string `json:"creationDate"`
    RevisionDate   string `json:"revisionDate"`
}

type bwsProject struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

// bwsRunner abstracts subprocess execution for tests.
type bwsRunner func(ctx context.Context, bin string, env []string, args ...string) (stdout []byte, stderr []byte, err error)

type bwsClient struct {
    binPath string
    token   string
    timeout time.Duration
    runner  bwsRunner
}

// newBWSClient takes the already-resolved token (resolveBWSAccessToken),
// resolves the binary (envKinkoBWSBin else exec.LookPath("bws")), and
// notes on stderr when the parent env carries BWS_ACCESS_TOKEN (ignored).
func newBWSClient(token string, stderr io.Writer) (*bwsClient, error)

// buildBWSChildEnv returns the minimal child environment: injected
// BWS_ACCESS_TOKEN plus passthrough of HOME, PATH, TMPDIR, and locale/TLS
// basics only. The parent's BWS_ACCESS_TOKEN is never inherited.
func buildBWSChildEnv(token string) []string

// redactBWSOutput replaces every occurrence of the token in s.
func redactBWSOutput(s, token string) string

func (c *bwsClient) listProjects(ctx context.Context) ([]bwsProject, error)
func (c *bwsClient) listSecrets(ctx context.Context, projectID string) ([]bwsSecret, error)
func (c *bwsClient) createSecret(ctx context.Context, projectID, key, value, note string) (bwsSecret, error)
func (c *bwsClient) editSecret(ctx context.Context, secretID, projectID, key, value, note string) (bwsSecret, error)
func (c *bwsClient) deleteSecrets(ctx context.Context, secretIDs []string) error
```

**Checklist**:
- [x] Every call: `--output json --color no`, per-call context timeout,
      stdin closed, no shell; errors carry redacted stderr
- [x] Missing binary / non-zero exit / bad JSON / timeout produce distinct
      wrapped errors (classified as provider failures by the sync command
      plan)
- [x] Unit tests with fake runner: env construction (parent
      `BWS_ACCESS_TOKEN` absent, token injected), redaction, JSON parse,
      timeout, non-zero exit, missing binary
- [x] `resolveBWSAccessToken` tests: env only, shared only, both (env wins
      + notice), neither (policy error)

---

### 5. Sync State Store

#### internal/kinko/sync_state.go

**Status**: COMPLETED

```go
const configKeyBWSSyncState = "sync.bws.v1"

type syncStateEntry struct {
    SecretID     string    `json:"secret_id"`
    Name         string    `json:"name"` // full BWS secret name
    Profile      string    `json:"profile"`
    Scope        scopeKind `json:"scope"`
    Path         string    `json:"path,omitempty"`
    Key          string    `json:"key"`
    RevisionDate string    `json:"revision_date"`
    ValueSHA256  string    `json:"value_sha256"`
}

type bwsSyncState struct {
    Format    int                       `json:"format"` // 1
    MachineID string                    `json:"machine_id"`
    ProjectID string                    `json:"project_id"`
    Entries   map[string]syncStateEntry `json:"entries"` // keyed by Name
}

// loadBWSSyncState reads configKeyBWSSyncState from a decrypted config map
// (loadConfig); absent key => empty state (never-synced).
func loadBWSSyncState(cfg map[string]string) (*bwsSyncState, error)

// saveBWSSyncState serializes into the config map; caller persists with
// saveConfig under the mutation lock.
func saveBWSSyncState(cfg map[string]string, state *bwsSyncState) error
```

**Checklist**:
- [x] Round-trip via real `loadConfig`/`saveConfig` (folders.v1 precedent)
- [x] Absent key -> empty state; malformed JSON -> metadata error
- [x] Unit tests incl. coexistence with `folders.v1` and user config keys

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Machine ID | `internal/kinko/vault.go`, `app.go`, `doctor.go` | COMPLETED | `machine_id_test.go`, `doctor_test.go` |
| migration command | `internal/kinko/migration.go`, `cobra_runtime.go`, `constants.go` | COMPLETED | `migration_test.go`, `cobra_runtime_test.go` |
| Scope hash / name / note | `internal/kinko/sync_scope.go` | COMPLETED | `sync_scope_test.go` |
| bws client | `internal/kinko/bws_client.go` | COMPLETED | `bws_client_test.go` |
| Sync state store | `internal/kinko/sync_state.go` | COMPLETED | `sync_state_test.go` |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Module 2 (migration) | Module 1 (`MachineID`, `newMachineID`) | SATISFIED |
| Module 5 (state store) | Module 3 (`scopeKind`) | SATISFIED |
| `bws-sync-command.md` (all) | Modules 1-5 | COMPLETED |

## Subtasks

### TASK-001: Machine ID in meta + init + doctor
**Status**: COMPLETED
**Parallelizable**: Yes
**Deliverables**: Module 1 + tests

### TASK-002: kinko migration command
**Status**: COMPLETED
**Parallelizable**: No (depends on TASK-001)
**Deliverables**: Module 2 + tests + `design-docs/specs/command.md` migration section

### TASK-003: Scope hash / name grammar / note metadata
**Status**: COMPLETED
**Parallelizable**: Yes
**Deliverables**: Module 3 + tests

### TASK-004: bws subprocess client
**Status**: COMPLETED
**Parallelizable**: Yes
**Deliverables**: Module 4 + tests

### TASK-005: Sync state store
**Status**: COMPLETED
**Parallelizable**: No (depends on TASK-003's `scopeKind` type)
**Deliverables**: Module 5 + tests

## Completion Criteria

- [x] All five modules implemented with unit tests
- [x] `kinko init` on a fresh dir yields a valid `machine_id`; `kinko
      migration --yes` backfills a legacy fixture; doctor warning appears
      before and disappears after
- [x] `go build ./...`, `go vet ./...`, `go test ./...` pass
- [x] `check-and-test-after-modify` agent run after every Go change

## Progress Log

### Session: 2026-07-13
**Tasks Completed**: (plan created)
**Notes**: Plan authored from design-docs/specs/design-bws-sync.md; no code yet.

### Session: 2026-07-13 (foundation implementation)
**Tasks Completed**: TASK-001, TASK-002, TASK-003, TASK-004, TASK-005
**Evidence**:
- Fresh and legacy machine-id behavior, migration preview/apply/idempotence,
  JSON/Cobra behavior, help flags, I/O and other exit-code classification,
  and doctor warning lifecycle are covered by `machine_id_test.go`,
  `migration_test.go`, and `doctor_test.go`.
- Fixed scope-hash vectors, normalization, name parsing, collision policy,
  and note verification are covered by `sync_scope_test.go`.
- Token precedence, child-environment isolation, token and create/edit secret
  value redaction from stderr and runner errors, JSON parsing,
  timeout/non-zero/missing-binary failures, and create/edit/delete invocation
  flags are covered by `bws_client_test.go`.
- Encrypted sync-state round trips through real `saveConfig`/`loadConfig`,
  absent/malformed state, and coexistence with `folders.v1` and user keys are
  covered by `sync_state_test.go`.
- `design-docs/specs/command.md` documents the shipped migration command,
  flags, doctor lifecycle, and exit-code mapping.
- `go mod tidy`, `gofmt`, `go build ./...`, `go vet ./...`, and
  `go test ./...` completed successfully.
- The required independent `check-and-test-after-modify` pass was repeated
  after the final Go hardening edits; focused tests, tidy, formatting, build,
  vet, the full test suite, diff checks, and the Go file line limit all passed.

**Notes**: The foundation work and its independent verification are complete.
The plan remains active because the dependent `bws-sync-command.md` push/pull
work is intentionally not part of this implementation.

### Session: 2026-07-13 (review revisions)
**Tasks Completed**: TASK-002 and TASK-004 review hardening
**Evidence**:
- BWS error redaction now processes sensitive strings longest-first; create
  and edit regressions cover values containing the access token.
- Successful delete output is treated as opaque when `bws` exits zero because
  the CLI emits plain text even with `--output json`; nonzero failures retain
  provider-error classification and redaction.
- Migration password verification classifies only `errDecryptFailed` as
  authentication failure; malformed salt and wrapped-DEK encodings are
  covered as metadata-invalid failures.
- The required independent `check-and-test-after-modify` verification passed
  focused tests, `go build -o /dev/null ./...`, `go test ./... -count=1`,
  `go vet ./...`, formatting, diff checks, and Go file line limits.

**Notes**: All three middle-severity review findings were resolved without
expanding into the dependent sync push/pull command plan. The foundation plan
remains active for the existing dependency boundary.

### Session: 2026-07-13 (locked-snapshot and empty-output review revisions)
**Tasks Completed**: TASK-002, TASK-004, and TASK-005 review hardening
**Evidence**:
- BWS list, create, and edit operations now reject empty successful output as
  invalid JSON; focused regressions cover both list variants and both mutation
  variants while delete continues to allow an empty response.
- Migration reads password input before locking, then reloads and authenticates
  against metadata under the mutation lock. A deterministic password-rotation
  regression proves stale pre-lock metadata cannot authorize the migration.
- Sync-state loading now distinguishes an absent key from a present empty value;
  the present empty value is covered as metadata-invalid.
- The required independent `check-and-test-after-modify` verification passed
  focused regressions, `go mod tidy` with unchanged module files, formatting,
  `go build -o /dev/null ./...`, `go test ./... -count=1`, `go vet ./...`,
  `git diff --check`, and the repository-wide Go file line limit.

**Notes**: The three latest middle-severity review findings were resolved. The
plan remains active because dependent sync push/pull work is still intentionally
out of scope.

### Session: 2026-07-13 (BWS response-shape review revision)
**Tasks Completed**: TASK-004 response validation hardening
**Evidence**:
- Project and secret list calls now require non-null JSON arrays and reject
  invalid elements; projects require an id, and secrets require both id and
  revisionDate.
- Create and edit calls now reject null, wrong top-level types, empty objects,
  and secret objects missing required identity or revision fields as
  `errBWSInvalidJSON`.
- Focused table-driven regressions cover `null`, wrong top-level types,
  `[null]`, empty create/edit objects, and missing required fields.
- The required independent `check-and-test-after-modify` verification passed
  the focused BWS tests under the race detector, `go test ./... -count=1`,
  `go build -o /dev/null ./...`, `go vet ./...`, formatting, diff checks, and
  Go file line limits.

**Notes**: The latest middle-severity response-shape finding was resolved. The
plan remains active because dependent sync push/pull work is still intentionally
out of scope.

### Session: 2026-07-13 (semantic-state validation and archival)
**Tasks Completed**: Foundation archive gate and accepted review feedback
**Evidence**:
- `loadBWSSyncState` validates every loaded entry before the sync engine can
  use it, including normalized scope/name/key identity, required fields,
  unique secret ids, lowercase SHA-256 values, reserved-key exclusion, and
  scope-hash collision safety.
- The final independent verification passed tidy/format stability, build,
  focused and full tests, focused race tests, vet, diff checks, and the Go
  file line limit under Go 1.25.8.
- Module verification, govulncheck, changed-code lint, and secure subprocess
  audits passed; the dependent command plan completed all of its gates.

**Notes**: The foundation and dependent command work are complete, so this
plan is archived with its exact verification evidence.

### Session: 2026-07-13 (mutation-response identity review revision)
**Tasks Completed**: TASK-004 response/request consistency hardening
**Evidence**:
- BWS create responses must match the requested project, key, value, and note;
  edit responses must additionally match the requested secret id.
- Response validation reports only mismatched field names and never includes
  secret values or note contents.
- Executor regressions prove an unconfirmed create/edit response cannot replace
  the affected persisted sync-state entry.
- Focused regressions passed with and without the race detector, and the full
  implementation verification passed tidy, formatting, build, full tests,
  vet, diff checks, and the repository-wide Go file limit.

**Notes**: The latest middle-severity BWS response-identity finding is resolved.
The completed foundation plan remains archived.

### Session: 2026-07-13 (plain-text delete-output review revision)
**Tasks Completed**: TASK-004 delete-response compatibility correction
**Changed Files**:
- `internal/kinko/bws_client.go`
- `internal/kinko/bws_client_test.go`
- `internal/kinko/sync_exec_test.go`
- `internal/kinko/sync_e2e_test.go`

**Evidence**:
- `deleteSecrets` now accepts any stdout after a zero-exit `bws secret delete`
  call, including the CLI's singular and plural plain-text success messages;
  delete output is not JSON-decoded.
- Client regressions cover `1 secret deleted successfully.` and
  `2 secrets deleted successfully.`, while a nonzero delete still maps to the
  provider error and redacts the access token from stderr and runner errors.
- Executor coverage proves per-secret remote deletion clears only confirmed
  sync state after singular plain-text output and preserves the exact applied
  prefix if a later delete fails. The stub-BWS end-to-end path emits the real
  singular output form used by that execution strategy and verifies push
  deletion convergence.
- No additional Go behavior, staging, commit, push, remote mutation, or Riela
  workflow was required for this documentation correction.

**Exact verification commands**:
- `go test ./... -count=1`
- `go vet ./...`
- `git diff --check`
- `find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 wc -l | awk '$1 >= 1000 && $2 != "total" { print; bad=1 } END { exit bad }'`

**Notes**: The obsolete JSON-validation evidence is corrected, and the final
plain-text delete behavior and verification evidence are now recorded. The
completed foundation plan remains archived.

### Session: 2026-07-13 (final remote-validation revision)

**Tasks Completed**: MODULE-2, MODULE-3, MODULE-4, and archive gate

**Changed Files**:
- `internal/kinko/sync_plan.go`
- `internal/kinko/sync_exec.go`
- `internal/kinko/sync_plan_test.go`
- `internal/kinko/sync_exec_test.go`
- `internal/kinko/sync_reserved_e2e_test.go`
- `design-docs/specs/design-bws-sync.md`
- `impl-plans/completed/bws-sync-foundation.md`
- `impl-plans/completed/bws-sync-command.md`
- `impl-plans/README.md`

**Evidence**:
- Machine-owned names are checked for duplicates before the reserved token is
  excluded, so reserved-name ambiguity is never ignored or force-resolved.
- Per-secret delete execution preserves exact confirmed-prefix state, reports
  every failed delete as partial/uncertain, and accepts supported BWS
  human-readable zero-exit delete confirmations without weakening JSON checks
  for list/create/edit responses.
- Focused command E2E coverage proves push, pull, and forced variants return
  policy exit `11` for duplicate reserved names with no local or remote
  mutation.

**Exact final verification**:
- `go mod tidy`, repository-wide `gofmt`, `go build -o /dev/null ./...`,
  `go vet ./...`, and `go test ./... -count=1` passed; the full suite contained
  450 top-level tests plus subtests.
- The focused BWS/sync race suite passed in 80.929s with 46 top-level tests,
  including the reserved-duplicate and delete-prefix regressions.
- `go mod verify` passed; `govulncheck@v1.6.0` found no called
  vulnerabilities; changed-code `golangci-lint` reported zero issues.
- `nix build 'path:.#kinko' --no-link`, `git diff --check`, the forbidden
  subprocess/credential/local-path audit, and the under-1000-line Go-file gate
  passed.

**Notes**: The final foundation-facing remote validation and provider-output
contracts are verified. The plan is ready for archival and fresh Riela review.
Nothing was staged, committed, pushed, or remotely mutated.
