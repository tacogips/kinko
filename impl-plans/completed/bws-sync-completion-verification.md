# BWS Sync Completion Verification Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#providerpayload-and-testing-boundaries`, `design-docs/specs/command.md#kinko-sync-with-bws`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous / Depends On**: every other active BWS sync completion plan
- **Next**: none; completion gate

## Design Reference and Scope

Complete hermetic coverage, opt-in ownership-scoped real-BWS tests, leakage audits, documentation, and repository-wide validation. This plan adds no untested product behavior.

## Modules and Signatures

### 1. Hermetic fixtures and leakage audit

#### `internal/kinko/bws_stub_test.go`, focused `sync_*_test.go` files

```go
type bwsStubScenario struct { Version string; Failures []string; Secrets []bwsSecret }
type sensitiveCanaryAudit struct { Values, Tokens []string; Files, Streams [][]byte }

func buildBWSContractStub(t *testing.T, bwsStubScenario) syncStubPaths
func assertNoSensitiveCanaries(t *testing.T, sensitiveCanaryAudit)
```

- [x] Cover adapters, selectors/maps, raw-state preservation, bootstrap, conflicts, all deletion gates, malformed/duplicates, retry/resume, diagnostics, and redaction.
- [x] Inspect argv, added child env, stdout/stderr, JSON/JSONL, errors, journals, and decrypted checkpoint fixtures.
- [x] Split tests by concern; target 700 and enforce 1,000 lines maximum.

### 2. Opt-in real BWS suite

#### `internal/kinko/bws_real_test.go`

```go
type realBWSTestOwnership struct { ProjectID, RunPrefix string; SnapshotIDs, CreatedIDs map[string]struct{} }

func requireRealBWSTest(t *testing.T) realBWSTestOwnership
func recordRealBWSCreatedID(t *testing.T, *realBWSTestOwnership, bwsSecret)
func cleanupRealBWSTest(t *testing.T, syncProvider, *realBWSTestOwnership)
```

- [x] Require `KINKO_TEST_REAL_BWS=1`, `KINKO_TEST_BWS_ACCESS_TOKEN`, and `KINKO_TEST_BWS_PROJECT_ID`.
- [x] Use cryptographically unique machine/name prefix; snapshot pre-existing ids; record discovered creates immediately.
- [x] Delete one-by-one only when absent from snapshot and still matching project/prefix.
- [x] Exclude from default/CI/race; failure writes a 0600 value-free allowlist manifest.

### 3. Documentation and release gates

#### `README.md`, `design-docs/specs/command.md`, build/release task files

```go
func TestGoFileLineLimits(t *testing.T)
func TestDefaultBuildHasNoBWSSDKDependency(t *testing.T)
```

- [x] Document compatibility defaults, bootstrap/recovery, maps/selectors, conflict/deletion policies, maintenance, resume/progress, config/version/transport gates, and residual races.
- [x] Document SDK license/CGO distribution gate and legacy secret-argv warning.
- [x] Add deterministic checks for source/test line limits and default dependency boundary.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| VERIFY-001 | Hermetic matrix/leak audit | all features | No | COMPLETED |
| VERIFY-002 | Real-BWS ownership suite | provider/executor | Yes | COMPLETED (live run 2026-08-06) |
| VERIFY-003 | Docs/release gates | all features | Yes | COMPLETED |
| VERIFY-004 | Full acceptance run | 001-003 | No | COMPLETED |

## Testing Requirements

- [x] `go test ./...`, `go vet ./...`, and `go test -race ./internal/kinko -timeout 30m` pass.
- [x] Hermetic provider tests use local stubs and retry tests use fake time; the default suite performs no live BWS access.
- [x] Default suite never reads real-BWS variables or mutates BWS.
- [x] `wc -l`/automated gate finds no Go source or test over 1,000 lines.
- [x] Self-review trace maps every normative completion-contract section to plan tasks and representative tests.

## Completion Criteria

- [x] No high/medium design capability is unimplemented or untested.
- [x] Real cleanup is ownership scoped and recoverable from its manifest.
- [x] Documentation and default build accurately describe/enforce security gates.
- [x] Plans are moved to completed only after all criteria across the dependency chain pass.

## Completion Contract Trace

| Normative design section | Implementation plan | Representative verification |
|---|---|---|
| Compatibility and state formats | state-selection, planning, CLI | `TestDecodeBWSSyncStateFormatCompatibility`, legacy/v2 dispatch and golden tests |
| Cross-machine bootstrap and recovery | bootstrap | bootstrap plan, atomic apply, read-only stub E2E, and value-free output tests |
| Portable logical paths and notes | state-selection, CLI | path-map round trips, encrypted-map precedence, push/pull materialization tests |
| Selection and exclusion | state-selection, planning | selector union/exclusion/reserved-key tests and pre-access Cobra validation |
| Status, reset, reconcile, and prune | maintenance, CLI | offline/online status, raw-preserving reset, upgrade-resume, and prune-gate tests |
| Granular conflicts and deletion | planning | conflict directionality and baseline/ownership/exact-id deletion-policy tests |
| BWS configuration and diagnostics | provider-config, CLI | secure-config, version-gate, doctor taxonomy/redaction, and golden-output tests |
| Secure transport and dependency boundary | provider-config, verification | isolated environment, dual legacy acknowledgement, SDK dependency gate, CGO-disabled build |
| Retry, progress, and resume | checkpoint-execution | fake-clock retry, encrypted checkpoint, crash/resume, ambiguity, and atomic pull tests |
| Provider/payload and testing boundaries | provider-config, verification | capability/value-size gates, redaction suite, line gate, and opt-in ownership-scoped real harness |

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (verification implementation)
**Tasks Completed**: Added a build-tagged, three-gate real-BWS CRUD harness with pre-existing-id snapshotting, cryptographic run ownership, immediate value-free 0600 cleanup manifests, and one-by-one ownership-revalidated cleanup. Added deterministic Go line-limit and default BWS SDK dependency-boundary tests. Expanded README operational and testing guidance. Default tests, build, vet, formatting, diff checks, and the compile-only skipped real-BWS target passed.  
**Tasks In Progress**: None.  
**Blockers**: None. An authenticated real-BWS run remains intentionally opt-in and was not performed because credentials and live mutation were not authorized.

### Session: 2026-08-05 (independent final acceptance)
**Tasks Completed**: VERIFY-001 and VERIFY-004, coverage trace, and dependency-chain acceptance.  
**Verification**: `go test ./... -count=1`, `go vet ./...`, `go build`, `CGO_ENABLED=0 go build`, `go mod verify`, focused sync race, full `go test -race ./internal/kinko -count=1 -timeout 30m` (739.029s), redaction tests, tagged live-target compile/skip, formatting, diff, and line limits all passed.  
**Blockers**: None. Live authenticated execution remains an optional operator action, not a completion requirement.

### Session: 2026-08-06 (live real-BWS verification run; VERIFY-002 executed)
**Tasks Completed**: Operator-authorized live verification against real Bitwarden Secrets Manager (bws CLI 2.0.0, disposable project created and deleted afterward; no pre-existing BWS data touched). `TestRealBWSCRUDOwnershipScoped` passed. End-to-end CLI verification with isolated vaults covered: legacy push, v2 push/pull with `--bws-transport cli-legacy --allow-secret-argv` (create/update/delete, remote-edit pull round trip), cross-machine `sync bootstrap` preview+apply with plaintext round-trip check, `sync status --online`, `doctor --provider=bws --online`, `sync reconcile` preview, `sync reset --checkpoint` preview, and `sync prune` policy gating.  
**Bugs Found by Live Run and Fixed** (each with regression tests; full suite re-passed after each):
1. Isolated BWS CLI config used `[default]`/boolean `state_opt_out`; bws 2.0.0 requires `[profiles.default]`, quoted `state_opt_out`, and no empty `server_*` keys (`bws_cli_adapter.go`).
2. `preconditionForSecret` pinned the configured (possibly empty) organization id, so apply-time pinned re-reads always failed when no org was configured (`sync_plan_v2.go`).
3. `--bws-transport cli-legacy --allow-secret-argv` was rejected at flag preflight (nil-provider probe) and the legacy transport never advertised the mutation capability (`bws_transport.go`, `cobra_runtime_sync.go`).
4. A retained complete-phase checkpoint blocked any subsequent differently-shaped run; complete checkpoints are now superseded instead of refusing resume (`sync_executor_v2.go`).
5. Created entries recorded an empty organization while updated/pulled entries recorded the observed one, breaking reconcile/prune context inference; entry recording and context inference now coalesce (`sync_executor_v2.go`, `sync_plan_v2.go`, `sync_reconcile.go`).
Additionally, `kinko init` with an explicit `--kinko-dir` no longer overwrites an existing bootstrap config pointing elsewhere (operator-reported hazard; `app.go`, `runtime_admin.go`, `cobra_runtime.go`, spec updated in `design-docs/specs/command.md`).  
**Blockers**: None. VERIFY-002 is now COMPLETED (live run performed with ownership-scoped cleanup; disposable project removed).
