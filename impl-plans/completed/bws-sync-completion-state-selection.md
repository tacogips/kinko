# BWS Sync State, Paths, and Selection Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#compatibility-and-state-format-rules`, `#portable-logical-paths-and-format-2-notes`, `#selection-and-exclusion-boundary`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous**: `bws-sync-completion-file-splits.md`
- **Next**: `bws-sync-completion-planning.md`
- **Depends On**: `bws-sync-completion-file-splits.md`

## Design Reference and Scope

Add backward-compatible format-2 state under `sync.bws.v1`, raw unknown-field preservation, ownership/checkpoint records, portable logical paths/notes, encrypted maps, and union-based selectors. Legacy-only runs continue writing format 1.

## Modules and Types

### 1. Versioned state codec

#### `internal/kinko/sync_state_v2.go`, `internal/kinko/sync_state.go`

```go
type syncStateEnvelope struct { Format int; Raw map[string]json.RawMessage }
type syncStateEntryV2 struct {
	Schema string
	ProviderIdentity, Endpoint, OrganizationID, ProjectID, MachineID string
	SecretID, Name, Revision, Profile, Key, ValueSHA256 string
	Scope scopeKind
	LocalPath, LogicalPath string
	Raw map[string]json.RawMessage
}
type syncOwnershipRecord struct { SecretID, ProviderIdentity, Revision string; Identity syncIdentity }
type bwsSyncStateV2 struct {
	Format int
	Entries map[string]syncStateEntryV2
	Ownership map[string]syncOwnershipRecord
	Checkpoint *syncCheckpoint
	Raw map[string]json.RawMessage
}

func decodeBWSSyncState(string) (syncStateEnvelope, error)
func mergeSelectedBWSSyncState(syncStateEnvelope, *bwsSyncStateV2, map[string]struct{}) (string, error)
```

- [x] Read format 1 indefinitely; reject unknown major formats and make old binaries reject format 2.
- [x] Preserve unselected records/unknown fields byte-for-byte; reset does not erase ownership proof.
- [x] Upgrade to format 2 only when a completion feature requires it.
- [x] Bound ownership to live or ambiguously deleted kinko-created records; remove it only after confirmed deletion.

### 2. Logical paths and notes

#### `internal/kinko/sync_path_map.go`, `internal/kinko/sync_scope_v2.go`

```go
type syncPathMap struct { Anchor, Root string }
type logicalScopeRef struct { Profile string; Kind scopeKind; LogicalPath, LocalPath string }
type bwsNoteMetadataV2 struct {
	KinkoSyncFormat int `json:"kinko_sync_format"`
	MachineID, Profile, Scope, LogicalPath, Key string
}

func parseSyncPathMap(string) (syncPathMap, error)
func validateSyncPathMaps([]syncPathMap, bool) error
func mapLocalToLogical(string, []syncPathMap) (string, error)
func mapLogicalToLocal(string, []syncPathMap) (string, error)
func deriveScopeHashV2(logicalScopeRef) string
```

- [x] Enforce anchor/path grammar, absolute cleaned roots, containment, longest prefix, overlap/equality/case-fold safeguards.
- [x] Store maps only in encrypted `sync.paths.v1`; notes contain logical path only; never create filesystem directories.
- [x] Invocation maps override encrypted maps; keep format-1 hashing unchanged and use the distinct `kinko.scope.v2` domain for logical paths.

### 3. Selection engine

#### `internal/kinko/sync_selector.go`

```go
type syncSharedMode string
const (
	syncSharedInclude syncSharedMode = "include"
	syncSharedExclude syncSharedMode = "exclude"
	syncSharedOnly syncSharedMode = "only"
)
type syncSelector struct {
	IncludeProfiles, IncludePaths, IncludeKeys []string
	ExcludeProfiles, ExcludePaths, ExcludeKeys []string
	Shared syncSharedMode
}
type syncIdentity struct { Provider, ProjectID, MachineID, Profile, Path, Key string; Scope scopeKind }

func normalizeSyncSelector(syncSelector) (syncSelector, string, error)
func selectSyncIdentities(syncSelector, []syncIdentity) (map[string]struct{}, error)
func syncEntryID(syncIdentity) string
```

- [x] Exact profile/key matching unless `glob:`; deterministic case-sensitive glob validation before password access.
- [x] Require `logical:` or `local:` path prefixes; inclusions intersect and exclusions win.
- [x] Evaluate union of local, validated remote, and state; always exclude reserved token after ambiguity validation.
- [x] Discard excluded remote values immediately; never hash, checkpoint, output, mutate, or infer deletion from them.
- [x] Empty status selection succeeds; mutating workflows return a policy error.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| STATE-001 | State envelope/v2 codec | split | Yes | COMPLETED |
| STATE-002 | Logical maps/notes | split | Yes | COMPLETED |
| STATE-003 | Selector and entry IDs | split | Yes | COMPLETED |
| STATE-004 | Raw-preserving selected merge | 001, 003 | No | COMPLETED |

## Testing Requirements

- [x] Format-1 golden compatibility, format-2 rejection by legacy decoder, unknown-field byte preservation.
- [x] Portable mapping tests for traversal, roots, aliases, overlap, Windows volume syntax, and case-insensitive mode.
- [x] Selector union/exclusion/glob/reserved-key tests byte-compare excluded vault data and raw state.

## Completion Criteria

- [x] State, map, note, and selector contracts cover every normative fence owned by this plan.
- [x] No token, value, map root, or checkpoint plaintext is written outside encrypted config.
- [x] Tests/build/vet/race pass and Go files remain below 1,000 lines.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: None.

### Session: 2026-08-05 (Implementation)
**Tasks Completed**: STATE-001 through STATE-004; added format-2 envelope/entries/ownership and opaque checkpoint preservation, portable encrypted path maps and logical notes, deterministic selectors and entry ids, and raw-preserving selected merge tests.  
**Tasks In Progress**: None.  
**Verification**: `gofmt` clean; focused state compatibility test passed; the complete focused state/path/scope/selector command printed `ok github.com/tacogips/kinko/internal/kinko 0.277s` before the shell wrapper reached its timeout.  
**Blockers**: None; remaining verification belongs to the final verification stage.

### Session: 2026-08-05 (integration and final verification)
**Tasks Completed**: Empty-selection command policy, logical-path push/pull materialization, provider-context retention, and centralized full acceptance.  
**Verification**: Full tests, full race, build, vet, redaction, raw-state preservation, and line-limit gates passed.  
**Blockers**: None.
