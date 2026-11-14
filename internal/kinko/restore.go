package kinko

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// restoreVaultTmpSuffix is appended to vault file names while they are being
// staged in the target data directory, before being renamed into place.
const restoreVaultTmpSuffix = ".restore-tmp"

// errRestoreCredentialMismatch classifies verifyRestoredVaultUsable failures
// that are consistent with a wrong backup password: the DEK failed to unwrap,
// or a subsequent AEAD decrypt failed with the same errDecryptFailed sentinel
// used for wrong-password failures elsewhere in the codebase (see the
// verifyRestoredVaultUsable doc comment for the deliberate simplification
// this implies for step (c) below).
var errRestoreCredentialMismatch = errors.New("restored vault credential mismatch")

// errRestoreMetadataInvalid classifies verifyRestoredVaultUsable failures
// that are not credential mismatches: malformed metadata, unsupported vault
// version, rejected KDF parameters, or any other unexpected failure while
// verifying the restored vault is usable.
var errRestoreMetadataInvalid = errors.New("restored vault metadata invalid")

// restoreInputOptions mirrors backupInputOptions (internal/kinko/backup.go)
// for password-input mode selection.
type restoreInputOptions struct {
	currentStdin bool
	currentFD    int
	forceTTY     bool
}

// restoreOptions are the fully parsed kinko restore command options.
type restoreOptions struct {
	input            restoreInputOptions
	archivePath      string
	includeBootstrap bool
}

// runRestore is the flag.FlagSet-based entrypoint kept for parity with other
// commands' non-cobra parse+run split (see runBackup/parseBackupOptions in
// internal/kinko/backup.go); cobra wiring calls runRestoreWithOptions
// directly with pre-populated restoreOptions.
func runRestore(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	restoreOpts, err := parseRestoreOptions(args)
	if err != nil {
		return err
	}
	return runRestoreWithOptions(opts, restoreOpts, stdin, stdout, stderr)
}

// parseRestoreOptions parses kinko restore's flags and its single required
// positional archive path argument.
func parseRestoreOptions(args []string) (restoreOptions, error) {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	restoreOpts := restoreOptions{
		input: restoreInputOptions{
			currentFD: -1,
		},
	}
	fs.BoolVar(&restoreOpts.input.currentStdin, "current-stdin", false, "read backup password from stdin")
	fs.IntVar(&restoreOpts.input.currentFD, "current-fd", -1, "read backup password from file descriptor")
	fs.BoolVar(&restoreOpts.input.forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	fs.BoolVar(&restoreOpts.includeBootstrap, "include-bootstrap", false, "also restore the archived bootstrap config to the --config path")

	if err := fs.Parse(args); err != nil {
		return restoreOptions{}, fmt.Errorf("invalid restore arguments: %w", err)
	}
	switch fs.NArg() {
	case 0:
		return restoreOptions{}, errors.New("restore requires exactly one archive path argument")
	case 1:
		restoreOpts.archivePath = fs.Arg(0)
	default:
		return restoreOptions{}, errors.New("restore accepts at most one archive path argument")
	}
	return restoreOpts, nil
}

// runRestoreWithOptions executes the full restore procedure: parse archive
// path -> read+sanitize password -> read/validate/decrypt archive in memory
// -> verify vault usability (parse meta.v1.json, unwrapDEKWithPassword,
// decrypt vault.v1.bin/config.v1.bin) -> acquire mutation lock -> re-check
// target-state policy -> ensure dir layout -> stage+rename vault files
// (marker last) -> optionally write bootstrap config -> print summary.
func runRestoreWithOptions(opts globalOptions, restoreOpts restoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	dataDir, err := filepath.Abs(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to resolve kinko data directory.", err)
	}
	dataDir = filepath.Clean(dataDir)

	if err := validateRestoreArchivePath(restoreOpts.archivePath); err != nil {
		return err
	}

	password, err := readRestorePasswordInput(stdin, stderr, restoreOpts.input)
	if err != nil {
		return err
	}
	password, err = sanitizePasswordValue(password)
	if err != nil {
		return newCLIError(exitCodeAuthFailed, "Backup password is invalid.", err)
	}

	zipArchive, err := readPasswordLockedZip(restoreOpts.archivePath, password)
	if err != nil {
		return newCLIError(zipReadErrorExitCode(err), err.Error(), err)
	}

	archive, err := validateRestoreArchiveEntries(zipArchive)
	if err != nil {
		return newCLIError(zipReadErrorExitCode(err), err.Error(), err)
	}

	if err := verifyRestoredVaultUsable(archive, password); err != nil {
		switch {
		case errors.Is(err, errRestoreCredentialMismatch):
			return newCLIError(exitCodeAuthFailed, "Backup password does not unlock the archived vault.", err)
		default:
			// Anything that is not a clear credential mismatch (including
			// errRestoreMetadataInvalid and unexpected internal failures) is
			// treated as a metadata-safety-validation failure: it is neither
			// obviously a wrong password nor a filesystem IO problem.
			return newCLIError(exitCodeMetadataInvalid, "Restored vault metadata failed safety validation.", err)
		}
	}

	// ensureDirLayout runs before acquireMutationLock (rather than after, as
	// stated in the design's procedure ordering) because the mutation lock
	// file itself lives at <dataDir>/vault/<mutationLockFileName>: on a truly
	// fresh restore target (the common case - restoring into a directory that
	// has never held a kinko vault), <dataDir>/vault does not exist yet, and
	// acquireMutationLock's underlying os.OpenFile(O_CREATE) would fail with
	// ENOENT, which acquireMetadataLock does not treat as a takeover-eligible
	// os.ErrExist case, causing it to surface as a misleading lock-conflict
	// error. ensureDirLayout only creates the standard 0700 directory
	// skeleton (idempotent, safe to call before the lock) and does not write
	// any vault content itself, so this reordering does not weaken the
	// target-state or bootstrap policy checks below, which still run under
	// the lock exactly as designed.
	if err := ensureDirLayout(dataDir); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to prepare kinko data directory layout.", err)
	}

	release, err := acquireMutationLock(dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Restore could not acquire mutation lock.", err)
	}
	defer release()

	if err := checkRestoreTargetStatePolicy(dataDir); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if err := checkRestoreBootstrapPolicy(opts.configPath, restoreOpts.includeBootstrap); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}

	if err := stageAndCommitRestoreFiles(dataDir, archive); err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}

	bootstrapRestored := false
	if restoreOpts.includeBootstrap {
		if err := writeRestoredBootstrapConfig(opts.configPath, archive); err != nil {
			return newCLIError(exitCodeIOFailed, err.Error(), err)
		}
		bootstrapRestored = true
	}

	printRestoreSummary(stdout, archive, bootstrapRestored, opts.configPath)
	return nil
}

// validateRestoreArchivePath validates the archive positional argument is a
// regular file, not a symlink or directory, before it is opened. Lstat
// itself failing (e.g. not found) is an IO failure; a symlink, directory, or
// other non-regular file is a policy violation.
func validateRestoreArchivePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return newCLIError(exitCodeIOFailed, fmt.Sprintf("Failed to stat archive path: %s.", path), err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		err := fmt.Errorf("archive path must not be a symlink: %s", path)
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if info.IsDir() {
		err := fmt.Errorf("archive path is a directory: %s", path)
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if !info.Mode().IsRegular() {
		err := fmt.Errorf("archive path must be a regular file: %s", path)
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	return nil
}

// zipReadErrorExitCode maps a *zipReadError's kind to the corresponding CLI
// exit code. If err is not a *zipReadError, it falls back to exitCodeIOFailed
// since the failure did not originate from a classified archive-read step.
func zipReadErrorExitCode(err error) int {
	var zre *zipReadError
	if errors.As(err, &zre) {
		switch zre.kind {
		case zipReadErrorKindAuth:
			return exitCodeAuthFailed
		case zipReadErrorKindPolicy:
			return exitCodePolicyFailed
		case zipReadErrorKindIO:
			return exitCodeIOFailed
		}
	}
	return exitCodeIOFailed
}

// readRestorePasswordInput mirrors readBackupPasswordInput's mode selection
// (mutually exclusive stdin/fd, forceTTY, interactive default), but reads
// the backup archive password rather than the vault's current password.
func readRestorePasswordInput(stdin io.Reader, stderr io.Writer, opts restoreInputOptions) (string, error) {
	useFD := opts.currentFD >= 0
	useStdin := opts.currentStdin

	switch {
	case useFD && useStdin:
		return "", errors.New("mixed stdin/fd password input modes are not supported")
	case useFD:
		return readPasswordFromFD(opts.currentFD)
	case useStdin:
		if isTerminalReader(stdin) {
			return "", errors.New("stdin is a TTY; non-interactive stdin mode is not allowed")
		}
		reader := bufio.NewReader(stdin)
		password, err := readPasswordLine(reader)
		if err != nil {
			return "", fmt.Errorf("read backup password: %w", err)
		}
		return password, nil
	default:
		return readRestorePasswordInteractive(stdin, stderr, opts.forceTTY)
	}
}

func readRestorePasswordInteractive(stdin io.Reader, stderr io.Writer, forceTTY bool) (string, error) {
	if isTerminalReader(stdin) {
		return readSecretNoTrim(stdin, stderr, "Backup password: ")
	}
	if !forceTTY {
		return "", errors.New("interactive password prompts require a TTY; use --current-stdin or --current-fd")
	}
	reader := bufio.NewReader(stdin)
	password, err := readPasswordLineWithPrompt(reader, stderr, "Backup password: ")
	if err != nil {
		return "", err
	}
	return password, nil
}

// parseVaultMetaBytes replicates loadMeta's (internal/kinko/vault.go)
// unmarshal + version-check + KDFParamsPassword-defaulting logic, but
// operates on in-memory bytes (decrypted archive contents) instead of
// reading vault/meta.v1.json from disk.
func parseVaultMetaBytes(b []byte) (*vaultMeta, error) {
	var m vaultMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse restored vault metadata: %w", err)
	}
	if m.Version != vaultVersion {
		return nil, fmt.Errorf("unsupported restored vault version: %d", m.Version)
	}
	if m.KDFParamsPassword == nil {
		m.KDFParamsPassword = defaultPasswordKDFParams()
	}
	if m.KDFParamsPassword.KeyLen == 0 {
		m.KDFParamsPassword.KeyLen = dekLength
	}
	return &m, nil
}

// verifyRestoredVaultUsable parses decrypted meta.v1.json bytes, unwraps the
// DEK with password, and decrypts vault.v1.bin/config.v1.bin, returning an
// error wrapping either errRestoreCredentialMismatch or
// errRestoreMetadataInvalid (via errors.Is) so the caller can map to
// exitCodeAuthFailed vs exitCodeMetadataInvalid.
//
// Classification notes:
//   - (a) parseVaultMetaBytes failures (bad JSON, unsupported version) are
//     always metadata-invalid: they indicate a structurally broken archive,
//     not a wrong password.
//   - (b) unwrapDEKWithPassword failures: errMetadataInvalid (rejected KDF
//     parameters) maps to metadata-invalid; isCredentialMismatchError (wrong
//     password) maps to credential-mismatch; anything else is treated as
//     metadata-invalid, consistent with runRestoreWithOptions's exit-code
//     policy of preferring metadata-invalid over IO for unexplained
//     non-credential failures.
//   - (c) decryptBlobWithAAD failures on vault.v1.bin/config.v1.bin: if the
//     DEK unwrap in (b) already succeeded with the correct password, a
//     subsequent AEAD failure here is more likely archive corruption or
//     tampering than a wrong password. However, decryptBlobWithAAD reports
//     both AES-GCM authentication failures and AAD mismatches through the
//     same errDecryptFailed sentinel that isCredentialMismatchError pattern
//     matches on. Rather than introducing a separate "integrity" class, this
//     function deliberately reuses the credential-mismatch classification
//     here too: it is a simplification (a wrong password is still the most
//     common real-world cause of ending up in this branch, e.g. multiple
//     backup archives from different points in time), not an oversight.
//     decryptBlobWithAAD is called exactly as loadVault/loadConfig call it -
//     no extra legacy-blob fallback logic is added or needed, since
//     decryptBlobWithAAD already transparently handles legacy no-AAD blobs
//     internally.
func verifyRestoredVaultUsable(archive *validatedRestoreArchive, password string) error {
	meta, err := parseVaultMetaBytes(archive.metaJSON)
	if err != nil {
		return fmt.Errorf("%w: %v", errRestoreMetadataInvalid, err)
	}

	dek, err := unwrapDEKWithPassword(meta, password)
	if err != nil {
		switch {
		case errors.Is(err, errMetadataInvalid):
			return fmt.Errorf("%w: %v", errRestoreMetadataInvalid, err)
		case isCredentialMismatchError(err):
			return fmt.Errorf("%w: %v", errRestoreCredentialMismatch, err)
		default:
			return fmt.Errorf("%w: %v", errRestoreMetadataInvalid, err)
		}
	}

	if _, err := decryptBlobWithAAD(dek, string(archive.vaultBin), []byte(aeadContextVaultData)); err != nil {
		if isCredentialMismatchError(err) {
			return fmt.Errorf("%w: %v", errRestoreCredentialMismatch, err)
		}
		return fmt.Errorf("%w: %v", errRestoreMetadataInvalid, err)
	}
	if _, err := decryptBlobWithAAD(dek, string(archive.configBin), []byte(aeadContextConfig)); err != nil {
		if isCredentialMismatchError(err) {
			return fmt.Errorf("%w: %v", errRestoreCredentialMismatch, err)
		}
		return fmt.Errorf("%w: %v", errRestoreMetadataInvalid, err)
	}

	return nil
}

// checkRestoreTargetStatePolicy fails (policy error) if any of meta.v1.json,
// vault.v1.bin, config.v1.bin, or the vault marker already exist under
// <dataDir>/vault. Existence is checked via os.Lstat; os.ErrNotExist on a
// given path is not an error condition, only successful Lstat (existence)
// triggers refusal. Any other Lstat error (e.g. permission errors)
// propagates as a wrapped error.
func checkRestoreTargetStatePolicy(dataDir string) error {
	candidates := []string{
		filepath.Join(dataDir, "vault", "meta.v1.json"),
		filepath.Join(dataDir, "vault", "vault.v1.bin"),
		filepath.Join(dataDir, "vault", "config.v1.bin"),
		filepath.Join(dataDir, "vault", vaultMarker),
	}
	for _, path := range candidates {
		_, err := os.Lstat(path)
		if err == nil {
			return fmt.Errorf("restore target already contains vault state at %s; use a different --kinko-dir or run kinko explosion first", path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check restore target state at %s: %w", path, err)
		}
	}
	return nil
}

// checkRestoreBootstrapPolicy fails (policy error) if includeBootstrap is
// set and a file already exists at configPath.
func checkRestoreBootstrapPolicy(configPath string, includeBootstrap bool) error {
	if !includeBootstrap {
		return nil
	}
	_, err := os.Lstat(configPath)
	if err == nil {
		return fmt.Errorf("restore target bootstrap config already exists at %s; refusing to overwrite", configPath)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check restore target bootstrap config at %s: %w", configPath, err)
	}
	return nil
}

// stageAndCommitRestoreFiles writes meta/vault/config/marker to
// <dataDir>/vault/<name>.restore-tmp, renames each into place (the vault
// marker last, as a hard design requirement so an interrupted restore never
// leaves a directory other kinko commands treat as initialized), and on any
// failure removes every staged and already-renamed restore output
// (best-effort cleanup) before returning the original error.
//
// Plain os.WriteFile (rather than O_EXCL) is used for the ".restore-tmp"
// staging files: these paths are always our own transient state, created and
// cleaned up entirely within this function's scope, so O_EXCL's
// already-exists protection would not add meaningful safety here and would
// only complicate retry/cleanup semantics for a scenario (concurrent restore
// into the same target) that acquireMutationLock already prevents.
func stageAndCommitRestoreFiles(dataDir string, archive *validatedRestoreArchive) error {
	vaultDir := filepath.Join(dataDir, "vault")

	type stagedFile struct {
		finalName string
		data      []byte
	}
	// Marker is listed last so the loop below stages/renames it last.
	staged := []stagedFile{
		{finalName: "meta.v1.json", data: archive.metaJSON},
		{finalName: "vault.v1.bin", data: archive.vaultBin},
		{finalName: "config.v1.bin", data: archive.configBin},
		{finalName: vaultMarker, data: archive.marker},
	}

	var tmpPathsWritten []string
	var finalPathsRenamed []string

	cleanup := func() {
		for _, p := range tmpPathsWritten {
			_ = os.Remove(p)
		}
		for _, p := range finalPathsRenamed {
			_ = os.Remove(p)
		}
	}

	for _, sf := range staged {
		tmpPath := filepath.Join(vaultDir, sf.finalName+restoreVaultTmpSuffix)
		if err := os.WriteFile(tmpPath, sf.data, 0o600); err != nil {
			cleanup()
			return fmt.Errorf("stage restore file %s: %w", sf.finalName, err)
		}
		tmpPathsWritten = append(tmpPathsWritten, tmpPath)
	}

	for _, sf := range staged {
		tmpPath := filepath.Join(vaultDir, sf.finalName+restoreVaultTmpSuffix)
		finalPath := filepath.Join(vaultDir, sf.finalName)
		if err := os.Rename(tmpPath, finalPath); err != nil {
			cleanup()
			return fmt.Errorf("commit restore file %s: %w", sf.finalName, err)
		}
		finalPathsRenamed = append(finalPathsRenamed, finalPath)
	}

	return nil
}

// writeRestoredBootstrapConfig writes archive.bootstrapBytes to configPath
// with 0600 perms, refusing to overwrite an existing file (O_EXCL). This is
// defense in depth on top of checkRestoreBootstrapPolicy's earlier check.
func writeRestoredBootstrapConfig(configPath string, archive *validatedRestoreArchive) error {
	f, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create restored bootstrap config %s: %w", configPath, err)
	}
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(configPath)
		}
	}()

	if _, err := f.Write(archive.bootstrapBytes); err != nil {
		return fmt.Errorf("write restored bootstrap config %s: %w", configPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close restored bootstrap config %s: %w", configPath, err)
	}
	success = true
	return nil
}

// printRestoreSummary prints the list of restored file names (never secret
// values) plus a note that the vault is restored in LOCKED state and a hint
// to run `kinko doctor` when the restored metadata uses a legacy,
// password-derived session key.
func printRestoreSummary(stdout io.Writer, archive *validatedRestoreArchive, bootstrapRestored bool, configPath string) {
	_, _ = fmt.Fprintln(stdout, "restore complete; restored files:")
	_, _ = fmt.Fprintln(stdout, "  vault/meta.v1.json")
	_, _ = fmt.Fprintln(stdout, "  vault/vault.v1.bin")
	_, _ = fmt.Fprintln(stdout, "  vault/config.v1.bin")
	_, _ = fmt.Fprintf(stdout, "  vault/%s\n", vaultMarker)
	if bootstrapRestored {
		_, _ = fmt.Fprintf(stdout, "  %s\n", filepath.Base(configPath))
	}
	_, _ = fmt.Fprintln(stdout, "vault is restored in LOCKED state; run `kinko unlock` to begin a session.")

	if meta, err := parseVaultMetaBytes(archive.metaJSON); err == nil {
		if usesLegacyPasswordDerivedSessionKey(meta) {
			_, _ = fmt.Fprintln(stdout, "hint: restored vault uses a legacy session key derivation; run `kinko doctor` after unlocking.")
		}
	}
}
