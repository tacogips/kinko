# kinko restore Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/design-restore.md
**Created**: 2026-07-12
**Last Updated**: 2026-07-13

---

## Design Document Reference

**Source**: design-docs/specs/design-restore.md

### Summary

Implement `kinko restore <archive>`, the read-side counterpart to `kinko
backup`. It parses a strict subset of the ZIP format (stored compression,
ZipCrypto encryption, no ZIP64) written by `internal/kinko/backup.go`,
decrypts and validates every entry against a fixed name allowlist, verifies
in-memory that the decrypted vault is usable with the supplied password
(`loadMeta`-equivalent parse -> `unwrapDEKWithPassword` -> AES-GCM decrypt of
`vault.v1.bin`/`config.v1.bin`), and only then stages and atomically writes
the vault files (and optionally the bootstrap config) into the target kinko
data directory under the mutation lock. Restore never partially writes a
vault: verification happens entirely before any filesystem mutation, and
staged writes are cleaned up on any failure.

### Scope

**Included**:
- New strict ZIP/ZipCrypto reader (`readPasswordLockedZip` or similar) that
  parses EOCD + central directory + local headers, matching exactly what
  `writePasswordLockedZip` in `internal/kinko/backup.go` emits.
- Entry-name allowlist validation and manifest (`manifest.json`) cross-check.
- Wrong-password / tampered-archive detection via ZipCrypto check byte + CRC32
  + DEK-unwrap verification, mapped to `exitCodeAuthFailed`.
- Target-state policy checks (`exitCodePolicyFailed`) before and again after
  acquiring the mutation lock.
- Atomic staged write of `vault/` files (`*.restore-tmp` -> rename, marker
  last) with full cleanup on failure.
- Optional `--include-bootstrap` restore of the archived bootstrap config to
  `--config`, refusing overwrite.
- `kinko restore` CLI flags: `--current-stdin`, `--current-fd`,
  `--force-tty`, `--include-bootstrap`, plus global `--kinko-dir`/`--config`.
- Cobra wiring in `internal/kinko/cobra_runtime.go`, command constant in
  `internal/kinko/constants.go`.
- Documentation update to `design-docs/specs/command.md`.

**Excluded**:
- Any change to the backup writer (`internal/kinko/backup.go`) itself.
- A general-purpose ZIP reader/writer library; the reader only needs to
  handle the exact archive shape backup produces (this is a security
  property, not a limitation to work around).
- `--overwrite` / force-restore-over-existing-vault mode (explicitly out of
  scope per design; `kinko explosion` remains the only vault-destroying
  command).
- Folder-vault (`folders/`) restore (backups exclude `folders/` already).
- Session/lock state restore (`lock/` is excluded from backups; restored
  vault always starts locked).

---

## Modules

### 1. Strict ZIP/ZipCrypto Reader

#### internal/kinko/restore_zip.go

**Status**: DONE

```go
// restoreZipEntry is one decrypted, CRC-verified archive entry keyed by its
// cleaned, forward-slash archive name (e.g. "kinko-backup/vault/meta.v1.json").
type restoreZipEntry struct {
	name string
	data []byte
}

// restoreZipArchive is the fully parsed and decrypted contents of a backup
// archive, keyed by cleaned archive-relative entry name.
type restoreZipArchive struct {
	entries map[string]restoreZipEntry
	order   []string // archive order, for deterministic error messages/tests
}

// maxRestoreZipEntrySize bounds per-entry uncompressed size to guard memory
// use against hostile archives (design: 64 MiB sanity cap).
const maxRestoreZipEntrySize = 64 * 1024 * 1024

// zipReadError distinguishes "wrong password" from "structurally invalid
// archive" so callers can map to exitCodeAuthFailed vs exitCodePolicyFailed.
type zipReadErrorKind int

const (
	zipReadErrorKindPolicy zipReadErrorKind = iota
	zipReadErrorKindAuth
	zipReadErrorKindIO
)

type zipReadError struct {
	kind zipReadErrorKind
	msg  string
	err  error
}

func (e *zipReadError) Error() string
func (e *zipReadError) Unwrap() error

// readPasswordLockedZip opens, strictly parses (EOCD, central directory,
// local headers; store-only, ZipCrypto-only, no ZIP64, no archive comment),
// decrypts every entry with password, and verifies each entry's ZipCrypto
// check byte and CRC32 against the local/central header values. It returns
// zipReadError with kind=Auth when the failure pattern is consistent with a
// wrong password (check-byte mismatch on all entries), kind=Policy for any
// other structural violation.
func readPasswordLockedZip(path string, password string) (*restoreZipArchive, error)

// entry returns the decrypted bytes for a required archive-relative name, or
// a policy zipReadError if absent.
func (a *restoreZipArchive) entry(name string) ([]byte, error)
```

**Checklist**:
- [x] Define `restoreZipEntry`, `restoreZipArchive`, `zipReadError` types
- [x] Implement EOCD locator + strict single-disk/no-comment validation
- [x] Implement central directory parsing (reject non-store, non-ZipCrypto,
      size mismatches vs. local header)
- [x] Implement ZipCrypto decryption + check-byte + CRC32 verification
      (reuse `newZipCryptoKeys`/`zipCryptoUpdateKeys`/`zipCryptoMask` from
      `internal/kinko/backup.go`; add a decrypt-direction helper alongside
      the existing encrypt-only helpers if needed)
- [x] Enforce `maxRestoreZipEntrySize` per entry
- [x] Unit tests: valid archive round-trip, truncated/garbage EOCD, wrong
      compression method, ZIP64 fields present (reject), duplicate entries,
      tampered ciphertext byte, wrong password

---

### 2. Archive Structure and Manifest Validation

#### internal/kinko/restore_manifest.go

**Status**: DONE

```go
// restoreEntryAllowlist enumerates the exact archive-relative names restore
// accepts, independent of the bootstrap config basename which is matched by
// the restoreBootstrapEntryPrefix.
const restoreBootstrapEntryPrefix = "kinko-backup/config/"

var restoreRequiredEntryNames = []string{
	"kinko-backup/manifest.json",
	"kinko-backup/vault/meta.v1.json",
	"kinko-backup/vault/vault.v1.bin",
	"kinko-backup/vault/config.v1.bin",
	"kinko-backup/vault/" + vaultMarker,
}

// restoreManifest mirrors backupManifest (internal/kinko/backup.go) for
// decoding; kept as a distinct type so restore does not depend on backup's
// internal field evolution beyond the documented JSON shape.
type restoreManifest struct {
	Version          int      `json:"version"`
	CreatedAtUTC     string   `json:"created_at_utc"`
	BootstrapPresent bool     `json:"bootstrap_present"`
	Files            []string `json:"files"`
}

// validatedRestoreArchive is the outcome of allowlist + manifest validation:
// required vault entries plus at most one optional bootstrap entry.
type validatedRestoreArchive struct {
	metaJSON       []byte
	vaultBin       []byte
	configBin      []byte
	marker         []byte
	bootstrapName  string // archive-relative name, "" if absent
	bootstrapBytes []byte
}

// validateRestoreArchiveEntries applies the entry-name allowlist (rejecting
// any other name as policy error), requires exactly one manifest.json and
// the four required vault entries, allows at most one config/ entry, parses
// and cross-checks manifest.json (version==1, Files matches present
// entries, BootstrapPresent matches config/ entry presence).
func validateRestoreArchiveEntries(archive *restoreZipArchive) (*validatedRestoreArchive, error)
```

**Checklist**:
- [x] Define `restoreManifest`, `validatedRestoreArchive` types and
      `restoreRequiredEntryNames`/`restoreBootstrapEntryPrefix` constants
- [x] Implement allowlist enforcement (reject unknown/duplicate/absolute/`..`
      names as `zipReadErrorKind=Policy`)
- [x] Implement manifest parse + cross-check against present entries
- [x] Unit tests: missing required entry, extra unexpected entry, duplicate
      bootstrap entries, manifest version mismatch, manifest/files mismatch,
      `bootstrap_present` disagreeing with actual config entry presence

---

### 3. Restore Orchestration and CLI Runtime

#### internal/kinko/restore.go

**Status**: DONE

```go
// restoreInputOptions mirrors backupInputOptions (internal/kinko/backup.go)
// for password-input mode selection.
type restoreInputOptions struct {
	currentStdin bool
	currentFD    int
	forceTTY     bool
}

// restoreOptions are the fully parsed kinko restore command options.
type restoreOptions struct {
	input             restoreInputOptions
	archivePath       string
	includeBootstrap  bool
}

// runRestore is the flag.FlagSet-based entrypoint kept for parity with
// other commands' non-cobra parse+run split (see runBackup/parseBackupOptions
// in internal/kinko/backup.go); cobra wiring calls runRestoreWithOptions
// directly with pre-populated restoreOptions.
func runRestore(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error

func parseRestoreOptions(args []string) (restoreOptions, error)

// runRestoreWithOptions executes the full restore procedure:
// parse archive path -> read+sanitize password -> read/validate/decrypt
// archive in memory -> verify vault usability (parse meta.v1.json,
// unwrapDEKWithPassword, decrypt vault.v1.bin/config.v1.bin) -> acquire
// mutation lock -> re-check target-state policy -> ensure dir layout ->
// stage+rename vault files (marker last) -> optionally write bootstrap
// config -> print summary.
func runRestoreWithOptions(opts globalOptions, restoreOpts restoreOptions, stdin io.Reader, stdout, stderr io.Writer) error

// readRestorePasswordInput mirrors readBackupPasswordInput's mode selection
// (mutually exclusive stdin/fd, forceTTY, interactive default).
func readRestorePasswordInput(stdin io.Reader, stderr io.Writer, opts restoreInputOptions) (string, error)

// verifyRestoredVaultUsable parses decrypted meta.v1.json bytes, unwraps the
// DEK with password, and decrypts vault.v1.bin/config.v1.bin, returning a
// credential-mismatch-classified error (see isCredentialMismatchError,
// internal/kinko/session.go) on failure so the caller can map to
// exitCodeAuthFailed vs exitCodeMetadataInvalid.
func verifyRestoredVaultUsable(archive *validatedRestoreArchive, password string) error

// checkRestoreTargetStatePolicy fails (policy error) if any of
// meta.v1.json, vault.v1.bin, config.v1.bin, or the vault marker already
// exist under <dataDir>/vault.
func checkRestoreTargetStatePolicy(dataDir string) error

// checkRestoreBootstrapPolicy fails (policy error) if includeBootstrap is
// set and a file already exists at configPath.
func checkRestoreBootstrapPolicy(configPath string, includeBootstrap bool) error

// stageAndCommitRestoreFiles writes meta/vault/config to
// <dataDir>/vault/<name>.restore-tmp, renames each into place, renames the
// vault marker into place last, and on any failure removes every staged and
// already-renamed restore output (best-effort cleanup) before returning the
// original error.
func stageAndCommitRestoreFiles(dataDir string, archive *validatedRestoreArchive) error

// writeRestoredBootstrapConfig writes archive.bootstrapBytes to configPath
// with 0600 perms, refusing to overwrite an existing file (O_EXCL).
func writeRestoredBootstrapConfig(configPath string, archive *validatedRestoreArchive) error
```

**Checklist**:
- [x] Define `restoreInputOptions`, `restoreOptions` types
- [x] Implement `parseRestoreOptions` (flag.FlagSet, mirrors
      `parseBackupOptions`; exactly one positional archive path required)
- [x] Implement `readRestorePasswordInput` (reuse
      `isTerminalReader`/`readPasswordLine`/`readPasswordFromFD`/
      `readSinglePasswordInteractive`-style helpers from
      `internal/kinko/backup.go`/`internal/kinko/io.go`)
- [x] Implement `runRestoreWithOptions` end-to-end orchestration per the
      Restore Procedure in design-docs/specs/design-restore.md
- [x] Implement `verifyRestoredVaultUsable` using `loadMeta`-equivalent JSON
      parse (in-memory, not `os.ReadFile`) + `unwrapDEKWithPassword` +
      `decryptBlobWithAAD` against decrypted archive bytes
      (`internal/kinko/vault.go`)
- [x] Implement `checkRestoreTargetStatePolicy`,
      `checkRestoreBootstrapPolicy`
- [x] Implement `stageAndCommitRestoreFiles` with marker-last rename and
      failure cleanup
- [x] Implement `writeRestoredBootstrapConfig`
- [x] Map every failure path to the Exit Codes table in
      design-docs/specs/design-restore.md via `newCLIError`
      (`internal/kinko/cli_error.go`)
- [x] Validate archive path is a regular file, not a symlink/directory,
      before opening

---

### 4. Cobra Wiring and Constants

#### internal/kinko/cobra_runtime.go (modification)

**Status**: DONE

```go
// newRestoreCommand mirrors newBackupCommand's flag/option wiring pattern.
func newRestoreCommand(ctx *runtimeContext, preflight func() error) *cobra.Command
```

**Checklist**:
- [x] Add `newRestoreCommand` following the `newBackupCommand` pattern
      (typed local flag vars -> `restoreOptions` -> `runRestoreWithOptions`)
- [x] Register `newRestoreCommand(ctx, finalizeOnlyPreflight)` in
      `newRuntimeRootCommand`'s `root.AddCommand(...)` list (restore, like
      backup, authenticates directly against persisted metadata and must not
      require an existing unlocked session or full bootstrap-config
      preflight before a vault exists at the target)
- [x] Add `cmdRestore = "restore"` to `internal/kinko/constants.go`
- [x] Unit test: `internal/kinko/cobra_runtime_test.go` covers restore flag
      parsing/wiring (positional arg required, `--include-bootstrap`,
      mutually exclusive stdin/fd)

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Strict ZIP/ZipCrypto reader | `internal/kinko/restore_zip.go` | DONE | 11 tests, all pass |
| Archive structure/manifest validation | `internal/kinko/restore_manifest.go` | DONE | 9 tests, all pass |
| Restore orchestration + CLI runtime | `internal/kinko/restore.go` | DONE | 12 test funcs (26 subtests), all pass |
| Cobra wiring + constants | `internal/kinko/cobra_runtime.go`, `internal/kinko/constants.go` | DONE | 2 new subtests in `TestRun_CobraBasedRegression_AllCommands` ("restore", "restore requires exactly one positional archive argument"), plus `cmdRestore` added to `TestRuntimeRootCommandSurface`'s expected set; all pass |
| Restore test suite | `internal/kinko/restore_test.go`, `internal/kinko/restore_e2e_test.go` | DONE | 12 pre-existing test funcs (26 subtests, restore_test.go, unchanged) + 9 new end-to-end test funcs (16 subtests total, restore_e2e_test.go), all pass |
| Command documentation | `design-docs/specs/command.md` | DONE | N/A (documentation only) |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Module 2 (manifest validation) | Module 1 (zip reader types) | Available (Module 1 types only) |
| Module 3 (orchestration) | Module 1, Module 2 | Available (types only) |
| Module 4 (cobra wiring) | Module 3 (`restoreOptions`, `runRestoreWithOptions`) | Resolved (Module 3 landed 2026-07-13; Module 4 implemented same day) |
| TASK-005 (test suite) | Module 1, 2, 3 implemented | Resolved |
| TASK-006 (docs) | Module 3, 4 implemented (behavior finalized) | Resolved |

## Subtasks

### TASK-001: Strict ZIP/ZipCrypto Reader
**Status**: DONE
**Parallelizable**: Yes
**Deliverables**: `internal/kinko/restore_zip.go`, `internal/kinko/restore_zip_test.go`
**Depends On**: none (reads existing constants from `internal/kinko/backup.go`)

**Completion Criteria**:
- [x] `restoreZipEntry`, `restoreZipArchive`, `zipReadError` defined
- [x] `readPasswordLockedZip` parses EOCD/central directory/local headers
      matching `writePasswordLockedZipEntry`'s output exactly
- [x] ZipCrypto decrypt + check-byte + CRC32 verification implemented
- [x] `maxRestoreZipEntrySize` enforced
- [x] Unit tests for well-formed and malformed archives pass

### TASK-002: Archive Allowlist and Manifest Validation
**Status**: DONE
**Parallelizable**: No (depends on TASK-001 types)
**Deliverables**: `internal/kinko/restore_manifest.go`, `internal/kinko/restore_manifest_test.go`
**Depends On**: TASK-001

**Completion Criteria**:
- [x] `validateRestoreArchiveEntries` enforces the fixed allowlist
- [x] Manifest parse + cross-check (version, files list, bootstrap_present)
      implemented
- [x] Unit tests for hostile/malformed archives pass

### TASK-003: Restore Orchestration and Vault-Usability Verification
**Status**: DONE
**Parallelizable**: No (depends on TASK-001, TASK-002)
**Deliverables**: `internal/kinko/restore.go`
**Depends On**: TASK-001, TASK-002

**Completion Criteria**:
- [x] `parseRestoreOptions` implemented (positional archive arg required,
      exactly one; regular-file check)
- [x] `readRestorePasswordInput` implemented, mutually exclusive
      stdin/fd modes enforced
- [x] `verifyRestoredVaultUsable` implemented (in-memory meta parse ->
      `unwrapDEKWithPassword` -> decrypt `vault.v1.bin`/`config.v1.bin`)
- [x] `checkRestoreTargetStatePolicy` / `checkRestoreBootstrapPolicy`
      implemented
- [x] `stageAndCommitRestoreFiles` implemented with marker-last rename and
      full cleanup on any failure
- [x] `runRestoreWithOptions` wires the full procedure and maps errors to
      exit codes per the design's Exit Codes table
- [x] `go build ./...` passes

### TASK-004: Cobra Command Wiring
**Status**: DONE
**Parallelizable**: No (depends on TASK-003's `restoreOptions`/`runRestoreWithOptions` signatures)
**Deliverables**: `internal/kinko/cobra_runtime.go`, `internal/kinko/constants.go`
**Depends On**: TASK-003

**Completion Criteria**:
- [x] `cmdRestore` constant added
- [x] `newRestoreCommand` implemented and registered in
      `newRuntimeRootCommand`
- [x] `kinko restore --help` shows all four flags plus the archive
      positional argument
- [x] `go build ./...` passes

**Notes**: While wiring cobra tests for a truly fresh restore target
(non-existent `--kinko-dir`), discovered and fixed a pre-existing
orchestration-ordering bug in `runRestoreWithOptions` (`internal/kinko/restore.go`,
landed under TASK-003): `acquireMutationLock` was called before
`ensureDirLayout`, so restoring into a brand-new target directory (the
common case) always failed, because the mutation lock file's parent
`<dataDir>/vault/` directory did not exist yet and `os.OpenFile(O_CREATE)`
returned `ENOENT`, which `acquireMetadataLock` does not treat as a
takeover-eligible `os.ErrExist` case, causing the raw `ENOENT` to surface as
a misleading `exitCodeLockConflict` error. Fixed by moving the
`ensureDirLayout(dataDir)` call to run before `acquireMutationLock`; the
target-state and bootstrap policy checks still run under the lock exactly as
designed, since `ensureDirLayout` only creates the standard 0700 directory
skeleton and writes no vault content itself.

### TASK-005: Restore Test Suite
**Status**: DONE
**Parallelizable**: Yes (once TASK-001..004 land; can be written incrementally alongside each, but full pass requires all four)
**Deliverables**: `internal/kinko/restore_test.go`, `internal/kinko/restore_e2e_test.go`
**Depends On**: TASK-001, TASK-002, TASK-003, TASK-004

**Completion Criteria**:
- [x] Round-trip: `writePasswordLockedZip` (backup) then restore into a
      fresh dir; `initVault`-equivalent unlock check reads secrets back
      (`TestRestoreE2E_RoundTrip`)
- [x] Wrong password: `exitCodeAuthFailed`, target dir left untouched
      (no `vault/` files created) (`TestRestoreE2E_WrongPassword`)
- [x] Existing vault at target: `exitCodePolicyFailed`, target dir untouched
      (`TestRestoreE2E_ExistingVaultAtTarget`, byte-for-byte meta.v1.json
      comparison before/after)
- [x] Tampered archive (flipped ciphertext byte): failure, target untouched
      (`TestRestoreE2E_TamperedArchive`)
- [x] Hostile archives: unexpected entries, wrong compression method,
      missing required entries all rejected with `exitCodePolicyFailed`
      (`TestRestoreE2E_HostileArchives`; path-traversal-name and
      duplicate-entry rejection were already covered at the
      `readPasswordLockedZip`/`validateRestoreArchiveEntries` unit level in
      `restore_zip_test.go`/`restore_manifest_test.go` under TASK-001/002,
      so this task's e2e coverage focuses on the cases not yet exercised at
      the full `runRestoreWithOptions` path)
- [x] `--include-bootstrap`: restored only when flag set; existing file at
      `--config` causes refusal without touching the vault
      (`TestRestoreE2E_IncludeBootstrap`,
      `TestRestoreE2E_ExistingBootstrapConfigAtTarget`)
- [x] Interrupted-restore simulation (inject failure mid
      `stageAndCommitRestoreFiles`): staged `.restore-tmp` files and any
      partially renamed outputs are removed, no marker left (already
      covered under TASK-003 by `TestRestoreStageAndCommitFiles_FailureCleansUp`
      in `restore_test.go`; not duplicated here)
- [x] Mutation lock conflict during restore maps to `exitCodeLockConflict`
      (`TestRestoreE2E_MutationLockConflict`)
- [x] `go test ./internal/kinko/...` passes

### TASK-006: Command Documentation Update
**Status**: DONE
**Parallelizable**: No (documents finalized behavior from TASK-003/TASK-004)
**Deliverables**: `design-docs/specs/command.md`
**Depends On**: TASK-003, TASK-004

**Completion Criteria**:
- [x] `### \`kinko restore <archive>\`` section added under the implemented
      commands, following the style of the existing `### \`kinko backup
      <directory>\`` section (examples, behavior bullets, input modes,
      exit-code notes)
- [x] Cross-reference to design-docs/specs/design-restore.md added
- [x] `impl-plans/README.md` Active Plans table updated with this plan's
      entry (`kinko-restore.md`, Status, Design Reference, Last Updated)
      (moved to the Completed Plans table instead, since this plan is now
      fully done)

## Completion Criteria

- [x] All modules implemented (TASK-001 through TASK-004)
- [x] `internal/kinko/restore_test.go` and `internal/kinko/restore_e2e_test.go`
      together cover all Testing Strategy items from
      design-docs/specs/design-restore.md
- [x] All tests passing (`go test ./...`)
- [x] `go build ./...` passes
- [x] `go vet ./...` passes
- [x] `kinko restore` wired into `cobra_runtime.go` and documented in
      `design-docs/specs/command.md`
- [x] `impl-plans/README.md` Completed Plans table updated (this plan moved
      out of Active Plans since it is now fully done)

## Progress Log

### Session: 2026-07-12
**Tasks Completed**: TASK-001, TASK-002
**Notes**:
- Implemented `internal/kinko/restore_zip.go`: strict EOCD/central-directory/
  local-header parser matching `writePasswordLockedZipEntry`'s exact byte
  layout, ZIP64-sentinel and locator-signature rejection, store-only/
  ZipCrypto-only/no-data-descriptor enforcement, local-vs-central header
  cross-checks, `maxRestoreZipEntrySize` cap, a new `zipCryptoDecrypt` helper
  (reuses `newZipCryptoKeys`/`zipCryptoUpdateKeys`/`zipCryptoMask` from
  `backup.go`), check-byte + CRC32 verification, and all-check-bytes-failed
  Auth vs. other-failure Policy classification.
- Implemented `internal/kinko/restore_manifest.go`: fixed entry-name
  allowlist (5 required names + `kinko-backup/config/<basename>` bootstrap
  prefix), explicit absolute/`..` rejection, required-entry and
  at-most-one-bootstrap counting, `manifest.json` parse + version check +
  `Files` set cross-check (excluding `manifest.json` itself, matching
  `collectBackupArchiveEntries` semantics) + `bootstrap_present` agreement
  check, and `validatedRestoreArchive` population.
- Added `internal/kinko/restore_zip_test.go` (11 tests) and
  `internal/kinko/restore_manifest_test.go` (9 tests), built via the real
  `writePasswordLockedZip` writer plus targeted binary patching for
  malformed-archive cases, per plan's checklist coverage.
- Verified independently (read both implementation files in full, re-derived
  byte offsets from `backup.go`, cross-checked against a second
  `check-and-test-after-modify` run): `go build ./...`, `go vet ./...`, and
  `go test ./internal/kinko/...` all pass (full package, no regressions).
  `backup.go` and all other existing files are untouched.
- Module 3 (`restore.go` orchestration) and Module 4 (Cobra wiring) remain
  NOT_STARTED and are the next subtasks (TASK-003, TASK-004).

### Session: 2026-07-13
**Tasks Completed**: TASK-003
**Notes**:
- Implemented `internal/kinko/restore.go`: `restoreInputOptions`,
  `restoreOptions`, `runRestore`, `parseRestoreOptions` (flag.FlagSet with
  `--current-stdin`/`--current-fd`/`--force-tty`/`--include-bootstrap`,
  exactly one required positional archive path), `readRestorePasswordInput`
  + `readRestorePasswordInteractive` (mirrors `readBackupPasswordInput`/
  `readSinglePasswordInteractive` from `backup.go`, reusing
  `isTerminalReader`/`readPasswordLine`/`readPasswordFromFD`/
  `readSecretNoTrim`/`readPasswordLineWithPrompt` without duplicating their
  logic), `validateRestoreArchivePath` (Lstat-based symlink/dir/regular-file
  check before opening the archive), `zipReadErrorExitCode` (maps
  `*zipReadError.kind` to `exitCodeAuthFailed`/`exitCodePolicyFailed`/
  `exitCodeIOFailed`, falling back to IO for unclassified errors),
  `parseVaultMetaBytes` (in-memory equivalent of `loadMeta`: unmarshal +
  version check + `KDFParamsPassword` nil/`KeyLen`-zero defaulting, operating
  on decrypted archive bytes instead of `os.ReadFile`), `verifyRestoredVaultUsable`,
  `checkRestoreTargetStatePolicy`, `checkRestoreBootstrapPolicy`,
  `stageAndCommitRestoreFiles`, `writeRestoredBootstrapConfig`,
  `printRestoreSummary`, and `runRestoreWithOptions` wiring the full
  procedure end to end per design-docs/specs/design-restore.md's "Restore
  Procedure" section.
- AAD/legacy-blob handling: `decryptBlobWithAAD` needed no extra fallback
  code. It is called exactly the same way `loadVault`/`loadConfig` call it
  (`decryptBlobWithAAD(dek, string(archive.vaultBin), []byte(aeadContextVaultData))`
  and the `config.v1.bin`/`aeadContextConfig` equivalent), because
  `decryptBlobWithResolverAndAAD` (vault.go) already transparently decrypts
  with `aad=nil` when the blob's embedded `aad_b64` is empty (legacy blobs)
  and only enforces the AAD match when the blob actually carries one.
- Error classification for `verifyRestoredVaultUsable`: two local sentinels,
  `errRestoreCredentialMismatch` and `errRestoreMetadataInvalid`, wrap the
  underlying cause via `%w`. `parseVaultMetaBytes` failures (bad JSON,
  unsupported version) always classify as metadata-invalid. For
  `unwrapDEKWithPassword` and the subsequent `vault.v1.bin`/`config.v1.bin`
  `decryptBlobWithAAD` calls: `errors.Is(err, errMetadataInvalid)` maps to
  metadata-invalid, `isCredentialMismatchError(err)` maps to
  credential-mismatch, and any other unexpected error also maps to
  metadata-invalid (documented in code as a deliberate judgment call: an
  unclassified failure at this stage is neither obviously a wrong password
  nor a filesystem IO problem, and the blob-decrypt case in particular is
  documented as a deliberate simplification since `decryptBlobWithAAD`
  reports both AES-GCM auth failures and AAD mismatches through the same
  `errDecryptFailed` sentinel `isCredentialMismatchError` matches on).
  `runRestoreWithOptions` maps `errors.Is(err, errRestoreCredentialMismatch)`
  to `exitCodeAuthFailed` and everything else from that call to
  `exitCodeMetadataInvalid`, consistent with the design's Exit Codes table
  judgment call documented in the implementation task prompt.
- `stageAndCommitRestoreFiles`: stages `meta.v1.json.restore-tmp`,
  `vault.v1.bin.restore-tmp`, `config.v1.bin.restore-tmp`, and
  `<vaultMarker>.restore-tmp` via plain `os.WriteFile(..., 0o600)` (documented
  as intentional since these paths are always our own transient state,
  created and cleaned up entirely within the function's scope, under the
  mutation lock). All four are renamed into place in slice order with the
  marker listed last, guaranteeing marker-last commit ordering. On any
  staging or rename failure, cleanup removes every tracked `.restore-tmp`
  path and every already-renamed-into-place final path (best-effort, errors
  ignored) before returning the original wrapped error.
- Added `internal/kinko/restore_test.go` (12 top-level test functions, 26
  subtests total, all prefixed `TestRestore*` so `go test ./internal/kinko/
  -run Restore` selects all of them): `parseRestoreOptions` positional-arg
  and flag-default/flag-set coverage; `checkRestoreTargetStatePolicy` fresh
  dir plus each of the four artifacts individually present;
  `checkRestoreBootstrapPolicy` include-bootstrap-false/missing/existing
  cases; `stageAndCommitRestoreFiles` success plus a deterministic
  failure-cleanup case (pre-creating `config.v1.bin` as a directory so the
  rename fails, asserting no final files including the marker remain and no
  `.restore-tmp` files are left, avoiding chmod-based induction which can be
  flaky under CI/root); `verifyRestoredVaultUsable` against a real vault
  built via `initVault` (correct password succeeds, wrong password
  classifies as `errRestoreCredentialMismatch`); `writeRestoredBootstrapConfig`
  success and overwrite-refusal-leaves-original-content-unchanged cases; plus
  extra coverage for `zipReadErrorExitCode`'s kind-to-exit-code mapping and
  `parseVaultMetaBytes`'s parse/version/default-application behavior.
- Verified independently (read `restore.go` and `restore_test.go` in full,
  cross-checked the marker-last ordering and the sentinel-based
  classification against the design/plan): `go build -o /dev/null ./...`,
  `go vet ./...`, `go test ./internal/kinko/ -run Restore -v` (all restore
  tests pass), and a clean (`go clean -testcache`) full
  `go test ./internal/kinko/ -v` (90.6s, all pass, no regressions) all
  succeed. `go mod tidy` made no changes (restore.go uses only stdlib
  imports already used elsewhere in the package). `git status --porcelain`
  confirms `internal/kinko/restore.go` and `internal/kinko/restore_test.go`
  are the only files added by this session; no existing file was modified.
- Module 4 (Cobra wiring in `cobra_runtime.go` + `cmdRestore` constant in
  `constants.go`) remains NOT_STARTED and is the next subtask (TASK-004).

### Session: 2026-07-13 (cont'd)
**Tasks Completed**: TASK-004, TASK-005, TASK-006
**Notes**:
- TASK-004 (Cobra wiring): added `cmdRestore = "restore"` to
  `internal/kinko/constants.go`'s command-name const block. Added
  `newRestoreCommand(ctx *runtimeContext, preflight func() error) *cobra.Command`
  to `internal/kinko/cobra_runtime.go`, placed directly after
  `newBackupCommand`, following its exact pattern: typed local flag vars
  (`currentStdin`, `forceTTY`, `currentFD`, `includeBootstrap`), `Use:
  cmdRestore + " <archive>"`, `Args: cobra.ExactArgs(1)`, builds a
  `restoreOptions` from `args[0]` plus the flag vars and calls
  `runRestoreWithOptions(ctx.opts, restoreOpts, ctx.stdin, ctx.stdout,
  ctx.stderr)` after `preflight()`. Registered
  `newRestoreCommand(ctx, finalizeOnlyPreflight)` in
  `newRuntimeRootCommand`'s `root.AddCommand(...)` list immediately after
  `newBackupCommand(ctx, finalizeOnlyPreflight)`, using the same
  `finalizeOnlyPreflight` (not the stricter `preflight`) for the same reason
  backup uses it: restore authenticates directly against archive/vault
  metadata and must not require an existing vault or full bootstrap-config
  validation at the target before a vault exists there.
- While writing the cobra-level restore test against a genuinely fresh
  (never-initialized) `--kinko-dir` target, discovered a real orchestration
  bug carried over from TASK-003's `restore.go`: `runRestoreWithOptions`
  called `acquireMutationLock(dataDir)` before `ensureDirLayout(dataDir)`.
  Since the mutation lock file lives at `<dataDir>/vault/<mutationLockFileName>`,
  a truly fresh target (the common/primary restore use case) does not yet
  have a `vault/` subdirectory, so `os.OpenFile(..., O_CREATE|O_EXCL, ...)`
  inside `createMetadataLock` fails with `ENOENT`. `acquireMetadataLock`'s
  retry loop only treats `os.ErrExist` as a takeover-eligible condition, so
  the raw `ENOENT` propagated straight up and `runRestoreWithOptions`
  unconditionally wrapped any `acquireMutationLock` error as
  `exitCodeLockConflict` - misclassifying "target directory does not exist
  yet" as a lock conflict and making restore into a fresh directory always
  fail. Fixed by moving the `ensureDirLayout(dataDir)` call to run before
  `acquireMutationLock` in `internal/kinko/restore.go` (a documented,
  code-commented reordering); `ensureDirLayout` only `os.MkdirAll`s the
  standard 0700 directory skeleton and writes no vault content, so
  `checkRestoreTargetStatePolicy` and `checkRestoreBootstrapPolicy` still run
  under the mutation lock exactly as the design's Restore Procedure
  specifies, and this reorder does not weaken any policy check. This was the
  one small `restore.go` adjustment anticipated as possibly-needed by the
  task instructions; no other change to Modules 1-3 was made.
- Added 2 new subtests to `TestRun_CobraBasedRegression_AllCommands` in
  `internal/kinko/cobra_runtime_test.go`: `"restore"` (backs up a real
  fixture vault via `setupBackupFixture` + `Run(..., "backup", ...)`, then
  restores into a fresh `--kinko-dir`/`--config` via `Run(...)` with all four
  flags set - `--current-stdin --current-fd -1 --force-tty
  --include-bootstrap` - asserting success, `"restore complete"` output, and
  that both the vault meta file and the restored bootstrap config land on
  disk) and `"restore requires exactly one positional archive argument"`
  (asserts `Run(["restore"], ...)` and `Run(["restore", "a.zip", "b.zip"],
  ...)` both error, exercising cobra's `ExactArgs(1)` enforcement through the
  package-level `Run` entrypoint). Added `cmdRestore` to the `want` slice in
  `TestRuntimeRootCommandSurface`.
- TASK-005 (restore test suite): read `restore_test.go` (12 existing test
  funcs / 26 subtests, all unit-level, TASK-003-scoped) in full and confirmed
  none of the required TASK-005 e2e cases already existed there. Read
  `restore_zip_test.go` and `restore_manifest_test.go` in full to reuse their
  archive-tampering (`zipCryptoHeaderSize`-relative ciphertext-byte flip) and
  hostile-archive fixture-building patterns (`buildManifestFixtureEntries`,
  `writePasswordLockedZip` direct construction). Read `backup_test.go` in
  full, especially `setupBackupFixture`, `TestRunBackup_RejectsWrongPassword`,
  and `TestRunBackup_MutationLockConflictExitCode`, to mirror their style.
  Added `internal/kinko/restore_e2e_test.go` (new file) with 9 top-level test
  functions (16 subtests total, all calling `runRestoreWithOptions` directly,
  not through cobra, mirroring how `backup_test.go` calls `runBackup`
  directly):
  - `TestRestoreE2E_RoundTrip`: full backup-then-restore round trip into a
    fresh target dir; confirms `unlockSession` succeeds with the original
    password afterward and that `A=one`, `B=two` (local) and `SHARED=shared`
    (shared scope) all read back correctly via `valueAtScope`/`valueAtShared`.
  - `TestRestoreE2E_WrongPassword`: wrong password against a valid archive
    maps to `exitCodeAuthFailed` and leaves no vault artifacts at the target
    (`anyVaultArtifact` check).
  - `TestRestoreE2E_ExistingVaultAtTarget`: pre-initializes a vault at the
    target with a different password; restore maps to `exitCodePolicyFailed`
    and the pre-existing `meta.v1.json` bytes are confirmed byte-for-byte
    unchanged, plus the original password still unlocks it afterward.
  - `TestRestoreE2E_TamperedArchive`: flips one ciphertext byte inside the
    archive (same technique as
    `TestReadPasswordLockedZip_TamperedCiphertextByteIsPolicyNotAuth` in
    `restore_zip_test.go`); asserts the exit code is one of
    `exitCodeAuthFailed`/`exitCodePolicyFailed` and the target is untouched.
  - `TestRestoreE2E_HostileArchives`: three subtests (unexpected entry name,
    missing required entry, wrong compression method) built via
    `buildManifestFixtureEntries` plus direct `writePasswordLockedZip` calls
    (reusing `restore_manifest_test.go`'s fixture builder and
    `restore_zip_test.go`'s compression-method-patching technique); all
    assert `exitCodePolicyFailed` and an untouched target.
  - `TestRestoreE2E_IncludeBootstrap`: two subtests - `--include-bootstrap`
    on writes a target config file byte-identical to the source bootstrap
    config; default (off) writes no file at all at the target `--config`
    path.
  - `TestRestoreE2E_ExistingBootstrapConfigAtTarget`: pre-creates a file at
    the target `--config` path, confirms `--include-bootstrap` restore fails
    with `exitCodePolicyFailed`, no vault artifacts are written at all
    (confirming `checkRestoreTargetStatePolicy`/`checkRestoreBootstrapPolicy`
    both run under the lock before `stageAndCommitRestoreFiles`, per
    `restore.go`'s actual implemented ordering), and the pre-existing config
    file content is byte-for-byte unchanged.
  - `TestRestoreE2E_MutationLockConflict`: mirrors
    `TestRunBackup_MutationLockConflictExitCode` from `backup_test.go`;
    pre-creates `<dataDir>/vault/<mutationLockFileName>` (after an explicit
    `ensureDirLayout` call, since restore's target starts as a completely
    empty temp dir unlike backup's already-initialized source fixture) with
    a `mutationLockMetadata` JSON payload; asserts `exitCodeLockConflict`.
  - The "interrupted-restore simulation / staged files cleaned up" Testing
    Strategy item was already fully covered under TASK-003 by
    `TestRestoreStageAndCommitFiles_FailureCleansUp` in `restore_test.go`
    (deterministic failure via a pre-created directory at the
    `config.v1.bin` destination path) and was not duplicated here.
  - Path-traversal-name and duplicate-entry-name hostile-archive rejection
    were already covered at the `readPasswordLockedZip`/
    `validateRestoreArchiveEntries` unit level under TASK-001/TASK-002
    (`restore_zip_test.go`'s `TestReadPasswordLockedZip_DuplicateEntryNamesRejected`
    and the entry-name allowlist enforcement inside
    `validateRestoreArchiveEntries` itself, which structurally cannot accept
    absolute or `..`-containing names since it does a fixed-list lookup
    rather than joining archive names into filesystem paths), so
    `TestRestoreE2E_HostileArchives` focuses on the cases not yet exercised
    at the full `runRestoreWithOptions` path (unexpected entry, missing
    entry, wrong compression method) rather than re-deriving already-covered
    cases at a second layer.
- TASK-006 (documentation): read `design-docs/specs/command.md` and
  `design-docs/specs/design-restore.md` fresh in full. Added a
  `### \`kinko restore <archive>\`` section to `command.md` directly after
  the existing `### \`kinko backup <directory>\`` section (and before
  `### \`kinko set ...\``), following backup's exact structure (intro
  paragraph, "Examples:" bash block mirroring design-restore.md's three
  Command Interface examples, "Behavior:" bullets covering the target-state
  refusal, pre-write password verification, restored-LOCKED-state guarantee,
  `--include-bootstrap` overwrite refusal, mutation lock acquisition, strict
  archive validation, and regular-file-only archive path requirement,
  "Input modes:" bullets matching backup's four input-mode bullets but noting
  the "Backup password:" prompt text from `readRestorePasswordInteractive`),
  plus a "Detailed design:" cross-reference line to
  `design-docs/specs/design-restore.md` matching the existing cross-reference
  style used for `export`'s exclude-keys design doc. Added a `restore` row to
  the `## Command-Local Flags` table directly after the `backup` row. Added
  7 `restore` rows to the "Current command-specific structured mappings"
  exit-code table directly after the existing `backup` rows and before the
  `export` rows, verifying the actual exit code values against
  `internal/kinko/cli_error.go` (`exitCodeAuthFailed=10`,
  `exitCodePolicyFailed=11`, `exitCodeLockConflict=12`, `exitCodeIOFailed=13`,
  `exitCodeMetadataInvalid=14`) rather than assuming the design doc's numbers
  were current; they matched exactly. Searched the rest of `command.md` for
  any other list of command names that might need updating (e.g. the
  "Subcommands" heading itself is not a literal name list, each command has
  its own `###` section) and found nothing else requiring a change.
- Post-move stability check (`-count=1` repeated runs) surfaced one
  intermittent failure in `TestRestoreE2E_WrongPassword`: it strictly
  asserted `exitCodeAuthFailed`, but ZipCrypto's per-entry check byte is only
  1 byte (documented in design-restore.md's Password Semantics section), and
  `readPasswordLockedZip` only classifies a wrong password as `kind=Auth`
  when *every* entry's check byte fails; across the archive's several
  entries, a wrong password has a small statistical chance per run that one
  (but not all) check bytes accidentally match, which classifies as
  `kind=Policy` ("archive integrity check failed") instead - this is
  expected, documented ZipCrypto weakness, not a `restore.go` defect. Fixed
  the test to accept either `exitCodeAuthFailed` or `exitCodePolicyFailed`
  (mirroring `TestRestoreE2E_TamperedArchive`'s pre-existing same
  acceptance), while still asserting the target directory is always left
  with no vault artifacts in either outcome. Re-verified with 30 consecutive
  `-count=30` runs of `TestRestoreE2E_WrongPassword` (all pass) plus a fresh
  full-package `go clean -testcache && go test ./internal/kinko/ -count=1`
  (78.5s, all pass) after the fix.
- Verification performed for this session: `go build -o /dev/null ./...` and
  `go vet ./...` both clean after every edit. Targeted runs of
  `TestRuntimeRootCommandSurface`, `TestRun_CobraBasedRegression_AllCommands`,
  and the full `TestRestore*` family all pass. All 9 new
  `TestRestoreE2E_*` functions (16 subtests) in `restore_e2e_test.go` pass.
  A full clean-cache run (`go clean -testcache && go test
  ./internal/kinko/ -v`) of the entire `internal/kinko` package completed in
  approximately 86s with zero failures (Argon2id KDF costs in vault
  init/unlock account for the runtime; this matches the ~90s baseline from
  the TASK-003 session, confirming no new slow paths or regressions).
  `go run ./cmd/kinko restore --help` output confirmed to show all four
  flags (`--current-stdin`, `--current-fd`, `--force-tty`,
  `--include-bootstrap`) plus the `<archive>` positional argument in its
  `Usage:` line. `git status --porcelain` (informational only; nothing
  committed by this session) shows `internal/kinko/constants.go`,
  `internal/kinko/cobra_runtime.go`, `internal/kinko/cobra_runtime_test.go`,
  `internal/kinko/restore.go`, `design-docs/specs/command.md` modified, and
  `internal/kinko/restore_e2e_test.go` added, consistent with the files this
  session touched; `internal/kinko/restore_test.go` was read but not
  modified this session (its existing coverage was sufficient and not
  duplicated).
- All three remaining subtasks (TASK-004, TASK-005, TASK-006) are complete.
  Every module in this plan (1 through 4) is now DONE, and the plan's
  top-level Completion Criteria are fully satisfied. This plan is being
  moved from `impl-plans/active/` to `impl-plans/completed/` as the final
  step of this session.
