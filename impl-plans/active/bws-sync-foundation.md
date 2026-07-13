# BWS Sync Foundation Implementation Plan

**Status**: Ready
**Design Reference**: design-docs/specs/design-bws-sync.md
**Created**: 2026-07-13
**Last Updated**: 2026-07-13

## Related Plans

- **Next**: `impl-plans/active/bws-sync-command.md` (sync engine + CLI; depends on every module here)

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

**Status**: NOT_STARTED

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
- [ ] Add `MachineID` to `vaultMeta`; `initVault` sets it via `newMachineID`
- [ ] Old metas (field absent) still load; no meta version bump
- [ ] `kinko doctor` (`runDoctor`, works from `loadMeta` without unlocking)
      warns when `MachineID` is empty, hinting `kinko migration`
- [ ] Unit tests: init populates it; legacy meta loads with empty id; validity

---

### 2. `kinko migration` Command

#### internal/kinko/migration.go, internal/kinko/cobra_runtime.go (modification), internal/kinko/constants.go (modification)

**Status**: NOT_STARTED

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
- [ ] `cmdMigration = "migration"` constant; command registered on root
- [ ] Preview default; `--yes` (with `-y` shorthand) applies after password
      verification (`unwrapDEKWithPassword`) under `acquireMutationLock`
- [ ] Atomic meta rewrite; `--json` output (step names + pending/applied)
- [ ] Idempotent: second run reports nothing pending
- [ ] Errors map via `newCLIError` to existing exit codes (auth 10, policy
      11, lock 12, io 13, metadata 14); lock failures must use
      `newCLIError(exitCodeLockConflict, ...)`, not prune-missing's plain
      `fmt.Errorf` (which exits 1)
- [ ] Unit tests + cobra regression subtests

---

### 3. Scope Hash, Name Grammar, Note Metadata

#### internal/kinko/sync_scope.go

**Status**: NOT_STARTED

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
- [ ] All functions implemented; path normalization composes
      `normalizePathInput` + `filepath.Abs` + `filepath.Clean` (the
      `normalizeStoredScopePathForPrune` pattern) — `normalizePathInput`
      alone is not trailing-slash-insensitive
- [ ] Unit tests: fixed hash vectors; profile/shared separation;
      trailing-slash equivalence; name build/parse round-trip with
      underscore-bearing keys; other-machine names rejected; collision
      detection; note encode/parse/verify including mismatch cases

---

### 4. bws Subprocess Client

#### internal/kinko/bws_client.go

**Status**: NOT_STARTED

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
func (c *bwsClient) editSecret(ctx context.Context, secretID, value, note string) (bwsSecret, error)
func (c *bwsClient) deleteSecrets(ctx context.Context, secretIDs []string) error
```

**Checklist**:
- [ ] Every call: `--output json --color no`, per-call context timeout,
      stdin closed, no shell; errors carry redacted stderr
- [ ] Missing binary / non-zero exit / bad JSON / timeout produce distinct
      wrapped errors (classified as provider failures by the sync command
      plan)
- [ ] Unit tests with fake runner: env construction (parent
      `BWS_ACCESS_TOKEN` absent, token injected), redaction, JSON parse,
      timeout, non-zero exit, missing binary
- [ ] `resolveBWSAccessToken` tests: env only, shared only, both (env wins
      + notice), neither (policy error)

---

### 5. Sync State Store

#### internal/kinko/sync_state.go

**Status**: NOT_STARTED

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
- [ ] Round-trip via real `loadConfig`/`saveConfig` (folders.v1 precedent)
- [ ] Absent key -> empty state; malformed JSON -> metadata error
- [ ] Unit tests incl. coexistence with `folders.v1` and user config keys

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Machine ID | `internal/kinko/vault.go`, `app.go`, `doctor.go` | NOT_STARTED | - |
| migration command | `internal/kinko/migration.go`, `cobra_runtime.go`, `constants.go` | NOT_STARTED | - |
| Scope hash / name / note | `internal/kinko/sync_scope.go` | NOT_STARTED | - |
| bws client | `internal/kinko/bws_client.go` | NOT_STARTED | - |
| Sync state store | `internal/kinko/sync_state.go` | NOT_STARTED | - |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Module 2 (migration) | Module 1 (`MachineID`, `newMachineID`) | BLOCKED |
| Module 5 (state store) | Module 3 (`scopeKind`) | BLOCKED (types only) |
| `bws-sync-command.md` (all) | Modules 1-5 | BLOCKED |

## Subtasks

### TASK-001: Machine ID in meta + init + doctor
**Status**: NOT_STARTED
**Parallelizable**: Yes
**Deliverables**: Module 1 + tests

### TASK-002: kinko migration command
**Status**: NOT_STARTED
**Parallelizable**: No (depends on TASK-001)
**Deliverables**: Module 2 + tests + `design-docs/specs/command.md` migration section

### TASK-003: Scope hash / name grammar / note metadata
**Status**: NOT_STARTED
**Parallelizable**: Yes
**Deliverables**: Module 3 + tests

### TASK-004: bws subprocess client
**Status**: NOT_STARTED
**Parallelizable**: Yes
**Deliverables**: Module 4 + tests

### TASK-005: Sync state store
**Status**: NOT_STARTED
**Parallelizable**: No (depends on TASK-003's `scopeKind` type)
**Deliverables**: Module 5 + tests

## Completion Criteria

- [ ] All five modules implemented with unit tests
- [ ] `kinko init` on a fresh dir yields a valid `machine_id`; `kinko
      migration --yes` backfills a legacy fixture; doctor warning appears
      before and disappears after
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass
- [ ] `check-and-test-after-modify` agent run after every Go change

## Progress Log

### Session: 2026-07-13
**Tasks Completed**: (plan created)
**Notes**: Plan authored from design-docs/specs/design-bws-sync.md; no code yet.
