# Code and Specification Review Findings (2026-07)

This document records verified review findings for the current `kinko`
repository. It is documentation only: no product fixes are included here. Use
the findings as remediation inputs for future `impl-plans/` work.

## Review Status

Scope reviewed:

- Specs: `design-docs/specs/architecture.md`, `command.md`, `notes.md`, and
  supporting `design-*.md` files.
- Implementation evidence: Go sources under `cmd/kinko/`, `internal/build/`,
  and `internal/kinko/`, plus `go.mod` and `README.md`.

Current verification performed for this revision:

- `rg --files`
- `rg -n` evidence searches across `go.mod`, `cmd/`, `internal/`,
  `README.md`, and `design-docs/specs/`
- Line-numbered source inspection with `nl -ba ... | sed -n ...`
- Fish shell syntax checks with `fish -c ...` for the fish quoting finding

Evidence status labels:

| Status | Meaning |
|--------|---------|
| Verified | Directly supported by current source/spec evidence or local command behavior |
| Verified Inference | Source evidence is direct, but impact depends on runtime scale, host state, or user workflow |
| Needs Decision | The repository is internally inconsistent; remediation requires choosing spec or implementation as source of truth |

Severity legend:

| Severity | Meaning |
|----------|---------|
| High | Correctness/security defect or broken documented workflow |
| Medium | Real defect or bounded-impact drift that should be planned |
| Low | Hygiene, diagnostics, UX consistency, or future-hardening issue |

Findings are numbered `F-01` through `F-35` for stable references.

---

## 1. High-Severity Findings

### F-01 (High, Remediated 2026-07-03) Module path typo in module/import/build references

Evidence:

- Original review found the module/import/build references using a misspelled
  GitHub host path.
- Current remediation changed `go.mod`, internal imports, README examples,
  `Taskfile.yml`, and `flake.nix` to `github.com/tacogips/kinko`.

Impact:

- GitHub install commands using the expected host path cannot be the canonical
  module path while the module declares a different host.
- The typo creates avoidable supply-chain ambiguity because the declared module
  host is not the project repository host.

Remediation status:

- Completed by `impl-plans/completed/module-path-and-cli-contracts.md`.
- Verification includes `go mod tidy`, `go build -o /dev/null ./...`,
  `go test ./...`, `go vet ./...`, and a repository search for the misspelled
  module path.

### F-02 (High, Remediated 2026-07-03) `export fish` emitted invalid fish when a value ended in backslash

Evidence:

- Original review found `quoteFish` emitting single-quoted strings while
  escaping only single quotes.
- Original review found `parseFishQuotedImportValue` decoding `\'` while
  leaving other backslashes unchanged.
- Fish verification with fish 4.5.0 showed
  `set -gx X 'C:\path\'; ...` fails with unbalanced quotes.

Impact:

- Backslash-ending values exported for fish are syntactically invalid.
- Backslash-heavy values are not covered by a fish-shell-backed round-trip
  contract, so future changes can regress shell interoperability.

Remediation status:

- Completed by `impl-plans/completed/module-path-and-cli-contracts.md`.
- `quoteFish` now escapes backslashes before quotes.
- `parseFishQuotedImportValue` decodes `\\` and `\'` and rejects unsupported
  escapes explicitly.
- Regression tests cover trailing backslash, doubled backslash, embedded single
  quote, and invalid fish escape input.

### F-03 (High, Remediated 2026-07-03) `kinko backup` included folder-vault storage without size, streaming, or mount-state controls

Evidence:

- Original review found `walkBackupDataFiles` recursively walking the whole
  data dir while excluding only transient lock/mutation paths.
- Folder-vault storage is under `folders/<id>/macos.sparsebundle`
  (`internal/kinko/folder_backend.go:52-53`,
  `internal/kinko/folder_backend_darwin.go:102-107`).
- Backup reads each source file fully into memory and accumulates entries
  before writing (`internal/kinko/backup.go:190-224`, `334-352`).
- The custom ZIP writer stores positions and sizes in `uint32` fields and has
  no ZIP64 path (`internal/kinko/backup.go:374-456`).

Impact:

- Sparsebundle bands become backup input even though folder-vault backup
  semantics are not specified.
- Large folder vaults can drive backup memory usage and archive size beyond the
  implementation's model.
- Mounted images can be copied while changing; this is an integrity risk unless
  the feature explicitly rejects mounted folder storage before backup.

Remediation status:

- Completed by `impl-plans/completed/folder-backup-explosion-lifecycle.md`.
- Folder-vault storage under root `folders/` is intentionally excluded from
  backup traversal until a streaming/ZIP64 folder-backup design exists.
- Regression tests verify folder storage is omitted from both archive entries
  and manifest contents.

### F-04 (High, Remediated 2026-07-03) `kinko explosion` was incompatible with folder-vault storage

Evidence:

- Original review found explosion validating root data-dir entries against only
  `vault` and `lock`.
- Folder vaults create a root `folders/` directory
  (`internal/kinko/folder_backend.go:52-53`).
- Original review found purge deleting only fixed vault and session files, not
  `folders/`.

Impact:

- Once a folder vault exists, explosion refuses the target as an unexpected
  layout.
- If the validator were relaxed without purge changes, folder ciphertext would
  survive an "irreversible destroy" operation.

Remediation status:

- Completed by `impl-plans/completed/folder-backup-explosion-lifecycle.md`.
- Explosion now allows a safe root `folders/` directory, refuses registered
  mounted folders, and removes folder storage after confirmation.
- Tests cover folder storage removal and mounted-folder refusal.

### F-05 (High, Remediated 2026-07-03) Stale mutation lock could permanently block writes

Evidence:

- Original review found `acquireMutationLock` using `O_CREATE|O_EXCL` on
  `vault/.mutation.lock`, returning on `os.ErrExist`, and removing the file
  only in the returned release function.
- Original review found the lock file had no PID, timestamp, host identity,
  lease, or repair guidance.

Impact:

- A killed process or power loss while holding the lock leaves all later
  mutations blocked until manual file deletion.
- The error message does not surface enough recovery guidance for ordinary CLI
  users.

Remediation status:

- Completed by `impl-plans/completed/folder-backup-explosion-lifecycle.md`.
- Mutation and folder lifecycle locks now write structured PID/hostname/time
  metadata and allow same-host dead-owner takeover.
- Corrupt, remote-host, or active-owner locks still block and include the lock
  path with recovery guidance.

### F-06 (High, Remediated 2026-07-03) Legacy password-derived session keys remain an offline password oracle until migration

Evidence:

- Original review found legacy derivation still existed:
  `deriveSessionKeyPairFromPassword` hashes
  `"kinko.session.seed.v1:password:" + strings.TrimSpace(password)` and derives
  ed25519 key material.
- Current init used random session key material
  (`internal/kinko/vault.go:88-100`).
- Legacy migration ran during unlock
  (`internal/kinko/session.go:58-69`,
  `internal/kinko/vault.go:292-317`).

Impact:

- Old `meta.v1.json` files without `session_key_source` expose a public key
  derived from a trimmed password at SHA-256 speed.
- Vaults not unlocked since the migration was introduced, plus old backups of
  legacy metadata, remain password-crackable material.

Remediation input:

- Keep the migration path until legacy support is intentionally dropped, but
  remove direct derivation from non-test runtime paths when possible.
- Add a README/security note: users with pre-migration vaults should unlock
  once with a current release, rotate the password, and treat old backups of
  `meta.v1.json` as sensitive.
- Add a `doctor` check that warns when `session_key_source` is empty.

Remediation status:

- Completed by `impl-plans/completed/crypto-session-hardening.md`.
- Password-derived session key derivation was removed from non-test runtime
  code; only test fixtures retain it to model legacy metadata.
- `kinko doctor` warns when `session_key_source` is missing.
- README migration guidance documents unlock, password rotation, and old
  `meta.v1.json` backup sensitivity.

### F-07 (High, Remediated 2026-07-03) Documented `kinko backup <directory>` positional form was rejected

Evidence:

- `command.md` specifies `kinko backup <directory>` and examples such as
  `kinko backup ./backups`.
- Original review found Cobra declaring `Args: cobra.NoArgs` for backup while
  exposing only `--dest-path`.
- Original review found runtime backup rejecting positional arguments.

Original impact:

- The documented backup command fails at argument parsing.

Remediation status:

- Completed by `impl-plans/completed/module-path-and-cli-contracts.md`.
- `kinko backup [directory]` is now accepted as the documented UX.
- `--dest-path` remains supported for compatibility.
- Mixed destination forms and multiple positional arguments are rejected.
- Runtime and Cobra regression tests cover the accepted and rejected forms.

---

## 2. Specification / Implementation Divergences

### F-08 (Medium, Remediated 2026-07-03) Unlock timeout config is split between 15m spec and 9h implementation

Evidence:

- Original review found `command.md` documenting `KINKO_UNLOCK_TIMEOUT`
  defaulting to `15m`.
- Original review found README examples and command summary documenting
  `--timeout 9h`.
- Cobra and runtime default unlock to `9h`
  (`internal/kinko/cobra_runtime.go:179-196`,
  `internal/kinko/app.go:95-100`).
- Original review found init writing encrypted config `unlock_timeout: "9h"`
  while no inspected path read that value.

Remediation input:

- Pick one default and one source-of-truth order:
  `--timeout` > env > encrypted config > built-in default, or document that
  only the flag is supported.
- Remove or wire up the `unlock_timeout` encrypted config key.
- Align `README.md`, `command.md`, tests, and help text in the same plan.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- The supported timeout source is now documented as `kinko unlock --timeout`
  with a `9h` built-in default.
- The inert initial encrypted config value `unlock_timeout` was removed from
  new vault initialization.
- `KINKO_UNLOCK_TIMEOUT` was removed from current environment-variable docs.

### F-09 (Medium, Remediated 2026-07-03) Bootstrap `kinko_dir` is written and validated but not used for data-dir resolution

Evidence:

- Init writes `kinko_dir` to bootstrap config
  (`internal/kinko/runtime_admin.go:254-263`).
- Bootstrap validation only allows that key (`internal/kinko/bootstrap.go`,
  observed via evidence search and tests).
- Global options use defaults and CLI flags; no inspected runtime path resolves
  data dir from bootstrap content.

Impact:

- A user who initialized with custom `--kinko-dir` can later run a plain
  command and operate against the default data dir unless they keep passing
  flags/environment.

Remediation input:

- Implement data-dir resolution:
  `--kinko-dir` > `KINKO_DATA_DIR` > bootstrap `kinko_dir` > default.
- Add tests for custom init followed by status/get without `--kinko-dir`.
- If the pointer is intentionally inert, remove it from spec and bootstrap.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- Runtime option resolution now uses bootstrap `kinko_dir` when neither
  `--kinko-dir` nor `KINKO_DATA_DIR` is supplied.
- Tests cover loading `kinko_dir` and running `status` with only `--config`
  after custom-dir initialization.

### F-10 (Medium, Remediated 2026-07-03) Command surface drift exists in both directions

Evidence:

- Implemented root commands include `version`, `backup`, `explosion`, `folder`,
  `path`, `direnv`, and `password` (`internal/kinko/cobra_runtime.go:73-140`).
- `command.md` specifies unimplemented or partially implemented commands:
  `tui`, `doctor`, `config path|edit|export`,
  `profile create|delete|rename`, and `path list|show`.
- Implemented features underdocumented in `command.md` include `explosion`,
  `version`, `--keychain-preflight`, `backup --dest-path`,
  `export --shared-only`, `direnv export --shared-only`, hidden
  `folder unlock --hold`, and mandatory `exec --all|--env`.

Remediation input:

- Make `command.md` match shipped behavior first.
- Move unimplemented commands into an explicit "planned/not implemented"
  section or remove them from the command reference.
- Add a generated/help-based command-surface check so drift is detected in CI.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- `command.md` now separates implemented commands from planned commands.
- A Cobra-derived root command surface test asserts the shipped public root
  command set.

### F-11 (Medium, Remediated 2026-07-03) Global flags table overstates global flags

Evidence:

- Cobra persistent flags are `--profile`, `--path`, `--kinko-dir`,
  `--config`, `--keychain-preflight`, `--force`, and `--confirm`
  (`internal/kinko/cobra_runtime.go:52-58`).
- `command.md` lists `--json`, `--no-color`, `--verbose`, `--timeout`,
  `--reveal`, and `--shell` as global flags.
- `--json` is local to `path prune-missing`, `--timeout` to `unlock`, and
  `--reveal` to `get`/`show`; `--no-color`, `--verbose`, and `--shell` are not
  present in inspected Cobra flags.

Remediation input:

- Split the flags table into persistent flags and per-command flags.
- Remove undocumented/unimplemented flags or add them intentionally with tests.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- `command.md` now lists actual persistent flags separately from
  command-local flags and removes unimplemented global flags.

### F-12 (Medium, Remediated 2026-07-03) Export guardrails conflict with spec examples

Evidence:

- `guardSensitiveOutput` rejects non-TTY stdout unless `--force` is set
  (`internal/kinko/runtime_display.go:280-283`).
- `runExport` calls that guard before rendering
  (`internal/kinko/runtime_io_commands.go:13-16`).
- Some `command.md` export examples use eval/pipe style without `--force`;
  README examples include `--force --confirm=false`.

Impact:

- Spec examples fail in ordinary `eval "$(kinko export bash)"` usage unless
  users add `--force`.

Remediation input:

- Update every pipe/eval export example to include `--force --confirm=false`,
  or redesign the guardrail specifically for export.
- Document the exact rule for non-TTY output.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- Export pipe/eval examples in `command.md` now include
  `--force --confirm=false`.
- The existing guardrail remains the shipped behavior.

### F-13 (Medium, Remediated 2026-07-03) Permission checks are specified but only creation modes are implemented

Evidence:

- Architecture specifies file-permission requirements for data/config files.
- Creation paths use restrictive modes, for example `ensureDirLayout` creates
  dirs with `0700` and writers use `0600`
  (`internal/kinko/vault.go:123-129`, `internal/kinko/session.go:101`).
- No inspected shared preflight rejects pre-existing insecure permissions.
- The natural home for repair/reporting, `doctor`, is specified but not
  implemented.

Remediation input:

- Decide whether strict permission drift checks are required for MVP.
- If required, add shared preflight checks with clear remediation text and a
  `--force`/repair path.
- If deferred, move the architecture language to future work.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- Architecture now states that restrictive creation modes are current behavior
  and that permission drift auditing/repair is future `doctor`/repair work.

### F-14 (Low, Remediated 2026-07-03) Data-model spec describes metadata not present in `vaultData`

Evidence:

- Architecture describes per-secret IDs and metadata such as timestamps and
  checksums.
- Current vault payload is `Profiles map[string]map[string]map[string]string`
  plus `Shared map[string]string` (`internal/kinko/vault.go:59-62`).

Remediation input:

- Update architecture to describe the shipped nested map model.
- Move per-entry metadata to a future schema/versioning section.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- Architecture now describes the shipped `profiles[profile][path][key]` and
  `shared[key]` vault payload model, with per-entry metadata deferred.

### F-15 (Low, Remediated 2026-07-03) Shared-unlock architecture still recommends an unbuilt daemon model

Evidence:

- Architecture text recommends a daemon-based shared unlock model.
- Current implementation uses a signed session token plus keychain-held wrap
  key (`internal/kinko/session.go:20-31`, `194-230`).
- README describes the keychain/session-token split.

Remediation input:

- Update architecture to record the shipped hybrid session-token/keychain
  decision.
- Move daemon tradeoffs to future work only if still desired.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- Architecture now records the shipped signed session-token plus OS-keychain
  wrap-key model and moves daemon custody to future work.

### F-16 (Low, Remediated 2026-07-03) `show --all-scopes` requires both password re-entry and an unlocked session

Evidence:

- `runShowAllScopes` verifies the password first
  (`internal/kinko/runtime_display.go:181-185`).
- It then calls `showAllSecretScopes`, which loads the active unlocked DEK
  through session state (observed via source flow and contrasted with
  `path prune-missing`, which reads the DEK from password directly at
  `internal/kinko/path_prune_missing.go:69-72`).

Impact:

- The design says password re-entry is the authorization boundary, but a
  locked session plus correct password is still insufficient.

Remediation input:

- Reuse the password-derived DEK from verification for `show --all-scopes`, or
  document the additional unlock-session prerequisite.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- `show --all-scopes` now reuses the password-derived DEK from password
  verification and no longer requires an already-unlocked session.
- Regression coverage verifies locked-session all-scopes output after correct
  password re-entry.

### F-17 (Low, Remediated 2026-07-03) `import --file` silently wins over piped stdin

Evidence:

- `runImport` reads `--file` whenever it is set and otherwise reads non-TTY
  stdin; there is no exclusivity check (`internal/kinko/runtime_io_commands.go:146-161`).
- Spec text says `--file` and stdin pipe are mutually exclusive.

Remediation input:

- If both `--file` and piped stdin are present, return a clear error.
- Add tests for file-only, stdin-only, and both-inputs cases.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- `import --file` now rejects non-empty piped stdin with a clear mutually
  exclusive input-source error.
- Tests cover both-input rejection and file input with empty redirected stdin.

---

## 3. Security Observations

The threat model in `notes.md` excludes root compromise and same-UID process
inspection. Findings here are still relevant because they either affect normal
user workflows or are low-cost hardening work.

### F-18 (Medium, Remediated 2026-07-03) Backup ZIP password protection uses legacy ZipCrypto

Evidence:

- Backup implements the traditional ZIP crypto key schedule and byte mask
  (`internal/kinko/backup.go:506-535`).
- `writePasswordLockedZipEntry` sets encrypted, stored ZIP entries and writes
  classic ZIP headers (`internal/kinko/backup.go:433-456`).

Impact:

- Archive password protection should not be presented as strong encryption.
- Inner vault/config blobs still provide the real cryptographic boundary, but
  metadata and bootstrap files are exposed to ZipCrypto's weakness.

Remediation input:

- Prefer AES-256 ZIP if interoperability requirements allow it.
- Otherwise keep ZipCrypto but clearly label the backup password as an
  access-control convenience, not primary confidentiality.

Remediation status:

- Completed by `impl-plans/completed/crypto-session-hardening.md`.
- Current compatibility behavior is retained.
- README and command docs now label the backup password as an access-control
  convenience and identify encrypted vault/config blobs as the primary
  cryptographic boundary.

### F-19 (Medium, Remediated 2026-07-03) macOS folder backend resolves `hdiutil` through PATH

Evidence:

- Original review found `folderBackendEnv` setting PATH with `/usr/local/bin`
  and `/opt/homebrew/bin` before `/usr/bin`.
- Original review found the Darwin backend invoking `"hdiutil"` by name while
  sending the folder secret on stdin.

Impact:

- Same-UID PATH planting is outside the stated threat model, but the current
  code has no need to prefer user-writable PATH locations for a system tool.

Remediation input:

- Invoke `/usr/bin/hdiutil` directly on macOS.
- Keep `LANG=C`; remove unnecessary PATH dependence for this backend.

Remediation status:

- Completed by `impl-plans/completed/crypto-session-hardening.md`.
- Darwin backend now invokes `/usr/bin/hdiutil` directly.
- Backend command environment now contains only `LANG=C`.
- Darwin tests assert both the system binary path and PATH-free environment.

### F-20 (Medium, Remediated 2026-07-03) AEAD blobs lack associated-data context

Evidence:

- Original review found `encryptBlob` sealing with nil associated data.
- Original review found `decryptBlob` opening with nil associated data.

Impact:

- Blobs encrypted under the same effective key are not cryptographically bound
  to their file role or format version; confusion is caught only by later
  parsing/shape checks.

Remediation input:

- For a future format version, pass AAD such as
  `kinko.<role>.v<format-version>` for vault, config, metadata-wrapped keys,
  and session payloads.
- Add migration/version tests for old nil-AAD blobs and new contextual blobs.

Remediation status:

- Completed by `impl-plans/completed/crypto-session-hardening.md`.
- Newly written wrapped-DEK, session-private-key, vault-data, config, and
  session-DEK blobs use role-specific AEAD context.
- Existing nil-AAD blobs remain decryptable for migration compatibility.
- Tests cover contextual blob round-trip, context mismatch failure, legacy
  nil-AAD decryption, and session token contextual AAD.

### F-21 (Low, Remediated 2026-07-03) `maskValue` leaks prefix, suffix, and exact length

Evidence:

- Values of length greater than four reveal the first two and last two
  characters and preserve exact length (`internal/kinko/runtime_display.go:273-278`).

Impact:

- Short tokens and PIN-like secrets can reveal most of their content in the
  default masked view.

Remediation input:

- Use a fixed mask for short values and consider a fixed-width mask for all
  masked output.
- Update tests that currently assert prefix/suffix masking.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- `maskValue` now returns a fixed-width mask and tests no longer assert
  prefix/suffix disclosure.

### F-22 (Low, Remediated 2026-07-03) Spec promises stronger memory erasure than Go code can guarantee

Evidence:

- Original review found architecture saying decrypted key material is erased on
  lock/timeout.
- Inspected code keeps passwords as strings and DEKs as byte slices without
  systematic zeroization.

Impact:

- The spec overstates memory hygiene guarantees in a garbage-collected Go
  process.

Remediation input:

- Reword the spec to "no persistent plaintext; best-effort memory hygiene."
- Add best-effort byte-slice wipes at clear ownership boundaries where useful,
  without promising hard erasure.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- Architecture now documents no persistent plaintext and best-effort in-process
  memory hygiene within Go runtime limits.

### F-23 (Low, Remediated 2026-07-03) Password change can orphan old keychain wrap-key entries

Evidence:

- Keychain account names include `SessionPubKeyB64`
  (`internal/kinko/session.go:228-230`).
- Original review found password change writing new session public key metadata
  before calling `lockSessionWithWarning`.
- Original review found `deleteSessionWrapKey` computing the account from
  current metadata.

Impact:

- Cleanup can target the new account instead of the old account, leaving the
  previous wrap key in OS keychain storage. The deleted session token limits
  direct exposure, but keychain state accumulates.

Remediation input:

- Compute and delete the pre-change account before metadata replacement, or
  pass previous metadata into cleanup.
- Add tests with a fake secret store that verifies old-account deletion.

Remediation status:

- Completed by `impl-plans/completed/crypto-session-hardening.md`.
- Password change now deletes the pre-change session wrap-key account using a
  metadata snapshot before final session lock cleanup.
- Regression coverage verifies the old fake keychain account is removed after
  session key metadata rotates.

### F-24 (Low, Remediated 2026-07-03) `sessionStatus` collapses session validation failures into "locked"

Evidence:

- `sessionStatus` returns real metadata load errors, but any failure from
  `verifyAndLoadSessionDEK` becomes `locked=true, err=nil`
  (`internal/kinko/session.go:121-130`).
- `verifyAndLoadSessionDEK` maps missing/corrupt token, keychain failure,
  signature failure, expiry, and decrypt failure mostly to `locked`
  (`internal/kinko/session.go:145-191`).

Impact:

- `kinko status` can hide corruption or keychain failures as normal locked
  state.

Remediation input:

- Preserve normal missing/expired-token behavior as locked.
- Surface corrupted token, metadata/key mismatch, and keychain unavailable as
  diagnostic states, or report them in `doctor`.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- `doctor` now reports corrupt active session token JSON/signature/payload,
  missing wrap-key, and invalid DEK diagnostics while preserving normal
  missing/expired token behavior.

### F-25 (Low, Remediated 2026-07-03) Sensitive-output confirmation is not TTY-aware everywhere

Evidence:

- `guardSensitiveOutput` prompts through `confirmPrompt(stdin, ...)`
  (`internal/kinko/runtime_display.go:280-292`).
- Import uses `confirmPromptTTYAware` for interactive confirmation
  (`internal/kinko/runtime_io_commands.go:176-195`).

Impact:

- If stdout is a TTY but stdin is piped, reveal/export confirmation can read
  from the pipe instead of the controlling terminal.

Remediation input:

- Route all sensitive-output confirmations through the TTY-aware prompt helper.
- Add tests for piped stdin plus TTY-like stderr/stdout fakes.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- `guardSensitiveOutput` now uses `confirmPromptTTYAware`.

---

## 4. Folder Vault Findings

### F-26 (Medium, Remediated 2026-07-03) No `folder remove` lifecycle command

Evidence:

- Original review found folder command wiring exposing only `add`, `unlock`,
  `lock`, `status`, and `path`.
- Original review found `runFolder` reporting the same command set.

Impact:

- Users can register folder vaults but cannot unregister them or delete
  backend storage through the CLI.
- This compounds F-04 because folder storage also blocks explosion.

Remediation status:

- Completed by `impl-plans/completed/folder-backup-explosion-lifecycle.md`.
- Added `kinko folder remove <name> [--keep-storage] [--yes|-y]`.
- Removal refuses mounted folders, confirms destructive storage deletion unless
  `--yes` is set, preserves storage with `--keep-storage`, and keeps the
  registration if storage deletion fails.

### F-27 (Medium, Remediated 2026-07-03) Folder secret derivation binds vault usability to absolute path

Evidence:

- Folder secret derivation includes profile, cleaned path, name, and folder ID
  (`internal/kinko/folder.go:320-330`).
- Folder records are looked up by profile/path/name in normal flows
  (observed in `runFolderAdd` and related folder flow inspection).

Impact:

- Moving or renaming a project path strands the normal registration because
  path matching and secret derivation both depend on the old path.
- The folder-vault spec language about avoiding stale absolute path metadata
  needs correction or a reattach design.

Remediation input:

- Document current non-relocatable behavior now.
- Plan either `folder move`/reattach, or a future derivation model that allows
  stable storage identity to survive path changes.

Remediation status:

- Completed by `impl-plans/completed/spec-command-reconciliation.md`.
- `command.md` and `architecture.md` now document that current folder vault
  registrations and derived folder secrets are bound to the resolved
  profile/path/name and are not relocatable without future reattach/move work.

### F-28 (Low, Remediated 2026-07-03) Mount state is parsed from human-readable `hdiutil info`

Evidence:

- Darwin status calls `hdiutil info`
  (`internal/kinko/folder_backend_darwin.go:91-99`).
- The parser scans text output rather than using `hdiutil info -plist`
  (parser evidence observed in `folder_backend_darwin.go` tests and source).

Impact:

- Text parsing is more vulnerable to output-format and locale drift.

Remediation input:

- Switch status detection to `hdiutil info -plist` and parse structured plist
  output.
- Keep existing text parser tests only as migration fixtures if useful.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- Darwin folder status now calls `hdiutil info -plist` and parses structured
  plist mount-point entries.
- Tests cover plist exact match, non-match, and malformed plist handling; old
  text parser tests remain as legacy fixtures.

### F-29 (Low, Remediated 2026-07-03) Sparsebundle capacity is fixed at 1 GiB and undocumented

Evidence:

- Darwin backend hardcodes `-size 1g`
  (`internal/kinko/folder_backend_darwin.go:41-52`).
- The inspected command/spec text does not document a user-visible size flag.

Impact:

- Users can hit a capacity limit without prior CLI/spec guidance.

Remediation input:

- Document the default capacity immediately.
- Consider `folder add --size <size>` plus validation and backend tests.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- Command and architecture docs now state the current macOS sparsebundle
  capacity is fixed at `1g`; a user-selectable size flag remains future work.

### F-30 (Low, Remediated 2026-07-03) Folder unlock had a status/mount race

Evidence:

- Original review found `runFolderUnlock` status and mount flow unprotected by
  mutation or folder-scoped locking.
- Original review found `folder add` used the mutation lock, while unlock/lock
  transitions did not.

Impact:

- Concurrent unlocks can race between "not mounted" detection and mount. The
  likely result is a backend attach failure, but it weakens lifecycle
  determinism.

Remediation status:

- Completed by `impl-plans/completed/folder-backup-explosion-lifecycle.md`.
- Folder unlock, lock, and remove status-changing transitions are serialized
  with a folder-scoped lifecycle lock.
- The lock is not held for the full foreground mount lifetime.
  Concurrent-unlock regression coverage uses a fake backend.

---

## 5. Reliability / Code-Quality Findings

### F-31 (Medium, Remediated 2026-07-03) Cobra and legacy runtime parsing duplicate command contracts

Evidence:

- Cobra command constructors parse flags and rebuild argv-like slices for
  runtime functions, for example backup (`internal/kinko/cobra_runtime.go:145-176`),
  unlock (`179-196`), export (`618-645`), import (`648-682`), and exec
  (`685-710`).
- Runtime functions parse those reconstructed args again with `flag.FlagSet`
  or hand parsing, for example backup (`internal/kinko/backup.go:58-75`) and
  export/import/exec (`internal/kinko/runtime_io_commands.go`).

Impact:

- Defaults and validation are duplicated, making spec drift and parser bugs
  more likely.

Remediation input:

- Introduce typed option structs at Cobra boundaries.
- Make runtime functions accept typed options instead of reparsing argv.
- Migrate one command family at a time, preserving CLI tests.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- The originally cited backup, unlock, export, import, and exec command paths,
  plus get/show display paths and the set/set-key/delete/copy/move/folder
  mutation paths plus path prune-missing, direnv export, and password change,
  now have typed option structs and typed execution functions.
- Cobra calls typed execution functions directly instead of rebuilding
  argv-like slices; no `parseArgs` reconstruction sites remain in
  `internal/kinko/cobra_runtime.go`.
- Legacy argv parsers remain as compatibility wrappers for direct runtime
  callers and parser-focused tests.

### F-32 (Medium, Remediated 2026-07-03) Exit-code contract is implemented unevenly

Evidence:

- Password change uses structured `cliError` codes
  (`internal/kinko/password_change.go:60-145`).
- Other inspected commands commonly return plain errors, including unlock,
  backup, import/export, and delete flows.
- `command.md` describes command-specific exit code categories.

Impact:

- Scripts cannot reliably distinguish auth failure, lock conflict, policy
  failure, and I/O failure across the command surface.

Remediation input:

- Define a command-by-command exit-code matrix.
- Wrap unlock, backup, bulk delete, import, and folder lifecycle errors in
  `cliError` where the spec promises stable codes.
- Add CLI-level tests asserting exit-code mapping.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- Backup authentication and mutation-lock conflicts now return structured
  `cliError` codes, with regression tests for auth and lock-conflict mappings.
- Bulk delete password verification failures and delete mutation-lock conflicts
  now return structured `cliError` codes, with regression tests for auth and
  lock-conflict mappings.
- Unlock now returns structured `cliError` codes for invalid arguments,
  credential failures, keychain/session I/O failures, and refresh status
  failures, with regression tests for policy, auth, and I/O mappings.
- Import/export now return structured `cliError` codes for policy/input
  failures, import mutation-lock conflicts, and file/vault/session I/O
  failures, with regression tests for representative policy, lock, and I/O
  mappings.
- Folder lifecycle commands now return structured `cliError` codes for
  validation/state policy failures, lifecycle lock conflicts, and storage or
  backend I/O failures, with regression tests for representative policy, lock,
  and I/O mappings.
- Child process exit status propagation is covered separately by F-33.

### F-33 (Low, Remediated 2026-07-03) `exec` does not propagate child process exit status

Evidence:

- `runExec` returns `cmd.Run()` directly
  (`internal/kinko/runtime_io_commands.go:553-590`).
- General error-to-exit handling maps ordinary errors to a generic failure
  code (observed via `cliError` usage pattern and command runner structure).

Impact:

- A child process exit status such as 7 can be collapsed to kinko's generic
  failure, breaking script composition.

Remediation input:

- Detect `*exec.ExitError` and propagate the child exit code.
- Add tests for child success, child non-zero exit, and missing command.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- `ExitCode` now detects `*exec.ExitError` and returns the child process exit
  status.
- Regression coverage verifies a child `exit 7` maps to exit code 7.

### F-34 (Low, Remediated 2026-07-03) Declining import value-disclosure prompt aborts instead of degrading to key-only summary

Evidence:

- With `--confirm-with-values`, `runImport` asks whether to show values and
  returns `aborted` when the answer is no
  (`internal/kinko/runtime_io_commands.go:176-185`).
- The key-only summary renderer is available in the same flow
  (`internal/kinko/runtime_io_commands.go:232-247`).

Impact:

- A privacy-preserving "no" stops the import rather than continuing with the
  safer summary.

Remediation input:

- Treat "no" to value disclosure as "continue without values."
- Keep the separate mutation confirmation as the actual abort point.
- Add tests for yes/no disclosure and yes/no mutation confirmation.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- A "no" answer to value disclosure now continues with a key-only summary;
  mutation confirmation remains the abort point.

### F-35 (Low, Remediated 2026-07-03) Assorted small issues worth batching

Evidence and remediation inputs:

- `saveMeta` is used during init while `saveMetaAtomically` exists
  (`internal/kinko/vault.go:103`); use the atomic path consistently.
- Session token writes use `write0600` directly
  (`internal/kinko/session.go:101`); consider atomic token replacement.
- `runFolder` discards `stdin` and `stderr`
  (`internal/kinko/folder.go:23-25`); retain them for future confirmations and
  backend warnings.
- `runSetKey` and import paths should share an explicit value-normalization
  contract for leading/trailing whitespace.
- `pkg/` is absent from the current `rg --files` output, so the prior claim
  that it contains only `.gitkeep` is not current and should not be used.
- The completed "Release-Diff Remediation Notes" section remains in
  `notes.md`; archive or summarize it when design notes are next reorganized.
- Posix import deliberately does not expand `$VAR` inside double-quoted input;
  document this as safer-than-shell parser behavior.

Remediation status:

- Completed under `impl-plans/active/p4-ux-diagnostics-maintainability.md`.
- `initVault` now writes metadata through the atomic metadata path.
- Session token writes now use atomic replacement.
- `runFolder` keeps stdin/stdout/stderr at its command boundary and passes
  streams through to the folder subcommands that currently need them.
- `set-key` and import value-normalization behavior is documented in
  `command.md` and covered by regression tests: `--value` preserves exact
  argument bytes, `set-key` stdin trims surrounding whitespace, and quoted
  import values preserve whitespace inside quotes.
- Posix import non-expansion of `$VAR` inside double-quoted input is
  documented as deterministic, safer-than-shell parsing.
- The stale release-diff remediation notes in `notes.md` were summarized into
  an archived decision note.

---

## 6. Prioritized Remediation Plan Inputs

| Priority | Findings | Suggested plan theme |
|----------|----------|----------------------|
| P0 | F-01, F-02, F-07 | Broken install/export/backup contracts |
| P1 | F-03, F-04, F-05, F-26 | Folder-vault, backup, explosion, and lock lifecycle integrity |
| P2 | F-06, F-18, F-19, F-20, F-23 | Crypto/key-handling hardening and legacy-session cleanup |
| P3 | F-08-F-17, F-27 | Spec/implementation reconciliation and command-surface source of truth |
| P4 | F-21, F-22, F-24, F-25, F-28-F-35 | UX, diagnostics, parser behavior, and maintainability |

Recommended plan split:

1. `impl-plans/completed/module-path-and-cli-contracts.md` for F-01, F-02, F-07.
2. `impl-plans/completed/folder-backup-explosion-lifecycle.md` for F-03, F-04,
   F-05, F-26, F-30.
3. `impl-plans/completed/spec-command-reconciliation.md` for F-08 through F-17
   and F-27.
4. `impl-plans/completed/crypto-session-hardening.md` for F-06, F-18, F-19,
   F-20, F-23.
5. Batch low-risk cleanup findings only after higher-priority behavior is
   scheduled.

## References

- Specs reviewed: `design-docs/specs/architecture.md`,
  `design-docs/specs/command.md`, `design-docs/specs/notes.md`, and
  `design-docs/specs/design-*.md`.
- External references index: `design-docs/references/README.md`.
