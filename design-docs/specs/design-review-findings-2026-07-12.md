# Code Review Findings (2026-07-12 Round)

Second full review round, following `design-review-findings-2026-07.md`
(F-01..F-35, all remediated). This round covered four areas with independent
reviews: crypto/session/init, CLI runtime, folder vaults, and IO/export/
import/backup. Findings are numbered G-01.. for stable reference. All fixes
in this round were applied with regression tests in the same change set.

Severity: High = correctness/security defect. Medium = real defect with
bounded impact. Low = hygiene/robustness.

## High Severity (all remediated 2026-07-13)

### G-01 `kinko init` destructively overwrote partially-damaged vaults

`isInitializedDataDir` required meta.v1.json AND vault.v1.bin AND
config.v1.bin before refusing init (`internal/kinko/app.go`). A vault
missing any one file let `kinko init` silently overwrite the surviving
wrapped DEK and destroy all secrets. Fixed: init refuses when ANY vault
artifact (three files or the vault marker) exists, with recovery guidance.

### G-02 `kinko explosion` echoed the master password in cleartext

`runExplosion` wrapped stdin in a buffered reader and read the password with
an echoing line read even on a TTY (`internal/kinko/runtime_admin.go`).
Fixed: TTY-aware no-echo read (term.ReadPassword path) before any buffered
stdin consumption; non-TTY behavior unchanged.

### G-03 `folder remove` lost concurrent config updates

The remove flow read-modify-wrote the encrypted config holding only the
per-folder lifecycle lock, never the vault mutation lock
(`internal/kinko/folder.go`); a concurrent `folder add`/`config set` was
silently erased. Fixed: mutation lock acquired (same order as `folder add`)
and config re-loaded under both locks.

### G-04 Mount detection ignored symlinked paths (false "not mounted")

Darwin `Status` compared hdiutil plist mount-points (resolved paths, e.g.
`/private/tmp/...`) against unresolved record paths (e.g. `/tmp/...`)
(`internal/kinko/folder_backend_darwin.go`). `folder lock` could report
locked without unmounting; `folder remove --yes` could delete a
live-attached sparsebundle. Fixed: both sides canonicalized with
EvalSymlinks (clean-path fallback) before comparison.

## Medium Severity (all remediated 2026-07-13)

- G-05 Unlock-time legacy session-key migration wrote vault metadata without
  the mutation lock, racing password change (silent password-wrap revert).
  Fixed: migration path acquires the lock and re-checks staleness under it;
  normal unlocks stay lock-free. (`session.go`)
- G-06 Atomic writes staged at a fixed `path+".tmp"` with pre-remove; two
  concurrent writers could rename each other's partial files into place
  (worst case truncating meta.v1.json). Fixed: os.CreateTemp random-suffix
  staging, chmod-before-write, no pre-remove. (`vault.go`,
  `password_change.go`)
- G-07 Stale mutation-lock takeover was remove-then-recreate; two processes
  could both win. Fixed: fixed-path O_CREATE|O_EXCL takeover-intent mutex
  serializes takeover; staleness re-verified after winning.
  (`password_change.go`)
- G-08 Explosion purged without the mutation lock (concurrent `set` could
  recreate vault files after purge). Fixed: lock acquired after final
  confirmation, target re-validated under it; layout validator tolerates the
  lock file. (`runtime_admin.go`)
- G-09 Mutation lock was held across unbounded interactive prompts in
  delete/move (an idle prompt blocked all mutations). Fixed: prompt first,
  then acquire lock and re-validate state before mutating.
  (`runtime_mutation.go`, `move.go`)
- G-10 `direnv export` silently discarded an explicit `--path` in favor of
  DIRENV_DIR. Fixed: explicit `--path` (flag-changed) now wins.
  (`direnv.go`, `cobra_runtime.go`)
- G-11 `exec` parsed interspersed flags, so `kinko exec --env FOO cmd --env
  BAR` without `--` silently injected the wrong secret and corrupted child
  argv. Fixed: SetInterspersed(false). (`cobra_runtime.go`)
- G-12 Posix/fish export of newline-containing values could not be
  re-imported (line-based parser vs multi-line single-quoted output). Fixed:
  posix/fish import parsers continue across lines for unterminated quoted
  values; round-trip regression tests added. (`runtime_io_commands.go`)
- G-13 Folder unlock hold-mode registered signal handling only after mount
  and never handled SIGHUP (dropped ssh session left the folder mounted).
  Fixed: SIGINT/SIGTERM/SIGHUP registered before mount. (`folder.go`)
- G-14 Hold-exit unmounted unconditionally without re-checking state,
  breaking externally-locked or second-session mounts. Fixed: status check
  before unmount on hold-exit. (`folder.go`)

## Low Severity (remediated 2026-07-13)

- G-15 `--current-fd` reads left O_NONBLOCK set on the caller's shared file
  description. Fixed: original flags captured and restored.
  (`password_change_fd_unix.go`)
- G-16 `doctor` treated any session-token read error as "no token" (false
  OK). Fixed: only ErrNotExist is silent. (`doctor.go`)
- G-17 Lock-conflict errors from set/copy/move/config-set bypassed the
  structured exit-code contract. Fixed: mapped to exit code 12.
- G-18 `kinko set` via stdin failed on values over 64 KiB (bufio.Scanner
  default). Fixed: 4 MiB buffer with clear overflow error.
  (`runtime_mutation.go`)
- G-19 Hand-written posix double-quoted import values with interior/trailing
  escaped quotes were accepted with shell-divergent results. Fixed: strict
  escape-aware scan; divergent inputs now error. (`runtime_io_commands.go`)
- G-20 `import --file` drained stdin unboundedly to verify emptiness. Fixed:
  single bounded read. (`runtime_io_commands.go`)
- G-21 `.gitignore` handling could panic on a create race (nil FileInfo) and
  remove rolled back from a stale snapshot. Panic fixed with re-stat; stale
  snapshot rollback risk documented in code. (`folder.go`)
- G-22 `folder remove` deleted storage before persisting the config change.
  Fixed: config saved first; storage-deletion failure reports the leftover
  path. (`folder.go`)
- G-23 Backup created the destination directory before validating it was
  outside the data dir, leaving residue on policy rejection. Fixed: lexical
  containment pre-check before MkdirAll. (`backup.go`)
- G-24 Custom ZIP writer silently wrapped 16/32-bit fields on oversized
  input. Fixed: explicit errors for >65535 entries or >4 GiB sizes/offsets.
  (`backup.go`)

## Deferred (documented, not fixed this round)

- Unlock refresh (`--timeout` while unlocked) locks the current session
  before re-authentication; repeated typos strand the user locked. Candidate
  for a verify-then-swap flow.
- Darwin `Ensure` adopts any pre-existing directory at the image path as a
  valid sparsebundle (partial-create adoption after a kill during
  `hdiutil create`); needs an integrity probe or repair path.
- Backup archive entry-name collisions (a stray `manifest.json` at the
  data-dir root shadows the real manifest); needs dedup/refusal in the walk.
- Aborted destructive operations exit 0; scripts cannot distinguish
  "declined" from "done".
- Folder exit-code classification uses message substrings and can mislabel
  I/O failures as policy failures.
- `sessionStatus` wrap-key TOCTOU between concurrent unlocks (self-healing;
  mostly mitigated by G-05's locking).

## Verification

Each fix landed with at least one regression test; several were verified to
fail pre-fix. Full package suite, vet, and build pass; concurrency-relevant
fixes were additionally verified under `-race`.
