package kinko

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runExplosion(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	_, _ = fmt.Fprintln(stderr, "DANGER: This will permanently delete all vault data in the current data dir.")
	_, _ = fmt.Fprintln(stderr, "All registered data will be lost and this action cannot be undone.")
	_, _ = fmt.Fprintln(stderr, "Password re-entry is required for this operation.")
	// verifyExplosionPassword performs a TTY-aware, no-echo password read
	// before any bufio.Reader buffers stdin bytes (Finding 1). It returns
	// the reader that must be used for all subsequent line-based prompts
	// (confirmation + confirmation token) so nothing is lost or
	// double-buffered between the password step and the rest of this flow.
	dek, reader, err := verifyExplosionPassword(opts, stdin, stderr)
	if err != nil {
		return err
	}
	if err := validateExplosionTarget(opts.dataDir); err != nil {
		return err
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		return fmt.Errorf("load encrypted config: %w", err)
	}
	records, err := loadFolderRecordsFromConfig(cfg)
	if err != nil {
		return err
	}
	if err := ensureNoMountedFolders(opts, records); err != nil {
		return err
	}
	ok, err := confirmPrompt(reader, stderr, "Are you absolutely sure? [y/N]: ")
	if err != nil {
		return err
	}
	if !ok {
		_, _ = fmt.Fprintln(stdout, "aborted")
		return nil
	}
	token := explosionConfirmationToken(opts.dataDir)
	if _, err := fmt.Fprintf(stderr, "Type confirmation token %q to proceed: ", token); err != nil {
		return err
	}
	input, err := readSecretFromBuffered(reader)
	if err != nil {
		return err
	}
	if input != token {
		_, _ = fmt.Fprintln(stdout, "aborted")
		return nil
	}

	// Finding 2: the confirmation-token check above may have taken an
	// arbitrarily long time (it waits on interactive input), during which a
	// concurrent mutator (e.g. `kinko set`) could have run and recreated
	// vault data files. Acquire the exclusive mutation lock and
	// re-validate the target layout under the lock immediately before the
	// irreversible purge, so no concurrent mutation can race with (and
	// survive) the destroy.
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Explosion could not acquire mutation lock.", err)
	}
	defer release()
	if err := validateExplosionTarget(opts.dataDir); err != nil {
		return err
	}
	if err := validateKinkoDataDirLayout(filepath.Clean(opts.dataDir)); err != nil {
		return err
	}

	if err := deleteSessionWrapKey(opts.dataDir); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: session wrap key cleanup failed: %v\n", err)
	}
	if err := purgeKinkoDataFiles(opts.dataDir); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "explosion completed: kinko data files removed")
	return nil
}

func validateExplosionTarget(dataDir string) error {
	clean := filepath.Clean(dataDir)
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	allowedBases := []string{filepath.Clean(filepath.Join(home, ".local")), filepath.Clean(os.TempDir())}
	if !isWithinAnyBase(clean, allowedBases) {
		return fmt.Errorf("refusing explosion for data dir outside allowed bases: %s", clean)
	}
	for _, denied := range explosionDenylist(home) {
		if clean == denied {
			return fmt.Errorf("refusing explosion for denied path: %s", clean)
		}
	}
	marker := filepath.Join(clean, "vault", vaultMarker)
	if !fileExists(marker) {
		return fmt.Errorf("refusing explosion: missing vault marker %s", marker)
	}
	if err := validateKinkoDataDirLayout(clean); err != nil {
		return err
	}
	return nil
}

func validateKinkoDataDirLayout(dataDir string) error {
	rootEntries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("read data dir: %w", err)
	}
	allowedRoot := map[string]bool{"vault": true, "lock": true, "folders": true}
	for _, entry := range rootEntries {
		if !allowedRoot[entry.Name()] {
			return fmt.Errorf("refusing explosion: unexpected entry in data dir: %s", filepath.Join(dataDir, entry.Name()))
		}
	}
	for _, mustDir := range []string{"vault", "lock"} {
		info, err := os.Lstat(filepath.Join(dataDir, mustDir))
		if err != nil {
			return fmt.Errorf("refusing explosion: missing required dir %s", filepath.Join(dataDir, mustDir))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing explosion: %s must not be a symlink", filepath.Join(dataDir, mustDir))
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing explosion: %s must be a directory", filepath.Join(dataDir, mustDir))
		}
	}
	if info, err := os.Lstat(folderStorageRoot(dataDir)); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing explosion: %s must not be a symlink", folderStorageRoot(dataDir))
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing explosion: %s must be a directory", folderStorageRoot(dataDir))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat folder storage root: %w", err)
	}
	vaultEntries, err := os.ReadDir(filepath.Join(dataDir, "vault"))
	if err != nil {
		return fmt.Errorf("read vault dir: %w", err)
	}
	// mutationLockFileName is permitted here because explosion itself
	// holds that lock file during the purge phase (Finding 2), and a
	// leftover lock file from a crashed process is not itself evidence of
	// tampering; it must never fail this validation, at either the
	// pre-prompt or post-lock validation call.
	allowedVault := map[string]bool{"meta.v1.json": true, "vault.v1.bin": true, "config.v1.bin": true, vaultMarker: true, mutationLockFileName: true}
	for _, entry := range vaultEntries {
		if entry.IsDir() {
			return fmt.Errorf("refusing explosion: unexpected subdirectory in vault dir: %s", filepath.Join(dataDir, "vault", entry.Name()))
		}
		if !allowedVault[entry.Name()] {
			return fmt.Errorf("refusing explosion: unexpected file in vault dir: %s", filepath.Join(dataDir, "vault", entry.Name()))
		}
	}
	lockEntries, err := os.ReadDir(filepath.Join(dataDir, "lock"))
	if err != nil {
		return fmt.Errorf("read lock dir: %w", err)
	}
	allowedLock := map[string]bool{"session.token": true}
	for _, entry := range lockEntries {
		if entry.IsDir() {
			return fmt.Errorf("refusing explosion: unexpected subdirectory in lock dir: %s", filepath.Join(dataDir, "lock", entry.Name()))
		}
		if !allowedLock[entry.Name()] {
			return fmt.Errorf("refusing explosion: unexpected file in lock dir: %s", filepath.Join(dataDir, "lock", entry.Name()))
		}
	}
	return nil
}

func purgeKinkoDataFiles(dataDir string) error {
	files := []string{filepath.Join(dataDir, "vault", "meta.v1.json"), filepath.Join(dataDir, "vault", "vault.v1.bin"), filepath.Join(dataDir, "vault", "config.v1.bin"), filepath.Join(dataDir, "vault", vaultMarker), filepath.Join(dataDir, "lock", "session.token")}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	if exists, err := folderStorageRootExists(dataDir); err != nil {
		return err
	} else if exists {
		if err := os.RemoveAll(folderStorageRoot(dataDir)); err != nil {
			return fmt.Errorf("remove folder storage root: %w", err)
		}
	}
	return nil
}

func ensureNoMountedFolders(opts globalOptions, records []FolderRecord) error {
	backend := newFolderBackend(opts.dataDir)
	for _, record := range records {
		status, err := backend.Status(context.Background(), record, folderMountpoint(record))
		if err != nil {
			return fmt.Errorf("check folder mount status for %s: %w", record.Name, err)
		}
		if status.Mounted {
			return fmt.Errorf("refusing explosion: folder is mounted: %s", record.Name)
		}
	}
	return nil
}

func isWithinAnyBase(path string, bases []string) bool {
	for _, b := range bases {
		if isWithinBase(path, b) {
			return true
		}
	}
	return false
}

func isWithinBase(path, base string) bool {
	path = filepath.Clean(path)
	base = filepath.Clean(base)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}

func explosionDenylist(home string) []string {
	return []string{filepath.Clean(string(filepath.Separator)), filepath.Clean(home), filepath.Clean(filepath.Dir(home)), filepath.Clean(os.TempDir()), "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/media", "/mnt", "/opt", "/proc", "/root", "/run", "/sbin", "/srv", "/sys", "/usr", "/var"}
}

func explosionConfirmationToken(dataDir string) string {
	sum := sha256.Sum256([]byte("kinko.explosion.v1:" + filepath.Clean(dataDir)))
	return strings.ToUpper(hex.EncodeToString(sum[:6]))
}

// verifyExplosionPassword re-verifies the vault password before allowing an
// explosion (irreversible purge) to proceed. It is TTY-aware (Finding 1):
// when stdin is a terminal, the password is read with the no-echo
// readSecret helper directly against the raw stdin, before anything wraps
// stdin in a *bufio.Reader. This avoids echoing the typed password to the
// screen and avoids buffering bytes past the password line that a
// bufio.Reader might read ahead. When stdin is not a terminal (e.g. piped
// input in tests or non-interactive automation), behavior is unchanged
// from before this fix: a single bufio.Reader is created up front and used
// for the password line plus all subsequent buffered reads.
//
// The returned io.Reader must be used for all subsequent line-based input
// (the "are you sure" confirmation and the confirmation-token entry): in
// the terminal case it is a freshly created *bufio.Reader wrapping the
// remaining stdin (safe, because the password itself was already consumed
// directly from the raw fd via term.ReadPassword, not through this
// reader); in the non-terminal case it is the very same *bufio.Reader used
// for the password line, preserving today's buffered-multi-line behavior.
func verifyExplosionPassword(opts globalOptions, stdin io.Reader, stderr io.Writer) ([]byte, *bufio.Reader, error) {
	var password string
	var err error
	var reader *bufio.Reader
	if isTerminalReader(stdin) {
		password, err = readSecret(stdin, stderr, "Re-enter password: ")
		if err != nil {
			return nil, nil, err
		}
		reader = bufio.NewReader(stdin)
	} else {
		bufReader := bufio.NewReader(stdin)
		password, err = readSecretWithPromptBuffered(bufReader, stderr, "Re-enter password: ")
		if err != nil {
			return nil, nil, err
		}
		reader = bufReader
	}
	password, err = sanitizePasswordValue(password)
	if err != nil {
		return nil, nil, fmt.Errorf("password is invalid: %w", err)
	}
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load vault metadata: %w", err)
	}
	dek, err := unwrapDEKWithPassword(meta, password)
	if err != nil {
		if isCredentialMismatchError(err) {
			return nil, nil, errors.New("password is invalid")
		}
		return nil, nil, fmt.Errorf("verify password: %w", err)
	}
	return dek, reader, nil
}

func runConfig(opts globalOptions, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("config requires subcommand: show|set")
	}
	switch args[0] {
	case configShow:
		dek, err := loadUnlockedDEK(opts.dataDir)
		if err != nil {
			return err
		}
		cfg, err := loadConfig(opts.dataDir, dek)
		if err != nil {
			return err
		}
		for _, k := range sortedKeys(cfg) {
			_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, cfg[k])
		}
		return nil
	case configSet:
		if len(args) != 3 {
			return errors.New("config set requires <key> <value>")
		}
		release, err := acquireMutationLock(opts.dataDir)
		if err != nil {
			return newCLIError(exitCodeLockConflict, "Vault mutation in progress.", err)
		}
		defer release()
		dek, err := loadUnlockedDEK(opts.dataDir)
		if err != nil {
			return err
		}
		cfg, err := loadConfig(opts.dataDir, dek)
		if err != nil {
			return err
		}
		cfg[args[1]] = args[2]
		return saveConfig(opts.dataDir, dek, cfg)
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func runProfile(opts globalOptions, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("profile requires subcommand: list")
	}
	switch args[0] {
	case profileList:
		if len(args) != 1 {
			return errors.New("profile list does not accept positional arguments")
		}
		dek, err := loadUnlockedDEK(opts.dataDir)
		if err != nil {
			return err
		}
		vd, err := loadVault(opts.dataDir, dek)
		if err != nil {
			return err
		}
		for _, name := range storedProfileNames(vd) {
			_, _ = fmt.Fprintln(stdout, name)
		}
		return nil
	default:
		return fmt.Errorf("unknown profile subcommand %q", args[0])
	}
}

func storedProfileNames(vd *vaultData) []string {
	names := make([]string, 0, len(vd.Profiles))
	for name := range vd.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeBootstrapConfig(opts globalOptions) error {
	parent := filepath.Dir(opts.configPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	content := fmt.Sprintf("kinko_dir=%q\n", opts.dataDir)
	if err := os.WriteFile(opts.configPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write bootstrap config: %w", err)
	}
	return nil
}

func getSecret(opts globalOptions, key string) (string, bool, error) {
	if err := validateEnvKey(key); err != nil {
		return "", false, err
	}
	m, err := showSecrets(opts)
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

func showSecrets(opts globalOptions) (map[string]string, error) {
	shared, repoSpecific, err := showSecretScopes(opts)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range shared {
		out[k] = v
	}
	for k, v := range repoSpecific {
		out[k] = v
	}
	return out, nil
}

func showSecretScopes(opts globalOptions) (map[string]string, map[string]string, error) {
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return nil, nil, err
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return nil, nil, err
	}
	shared := map[string]string{}
	for k, v := range vd.Shared {
		shared[k] = v
	}
	repoSpecific := map[string]string{}
	if vd.Profiles[opts.profile] != nil && vd.Profiles[opts.profile][opts.path] != nil {
		for k, v := range vd.Profiles[opts.profile][opts.path] {
			repoSpecific[k] = v
		}
	}
	return shared, repoSpecific, nil
}

func showAllSecretScopes(opts globalOptions) (map[string]string, map[string]map[string]string, error) {
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return nil, nil, err
	}
	return showAllSecretScopesWithDEK(opts, dek)
}

func showAllSecretScopesWithDEK(opts globalOptions, dek []byte) (map[string]string, map[string]map[string]string, error) {
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return nil, nil, err
	}
	shared := map[string]string{}
	for k, v := range vd.Shared {
		shared[k] = v
	}
	pathsByScope := map[string]map[string]string{}
	profileScopes := vd.Profiles[opts.profile]
	if profileScopes == nil {
		return shared, pathsByScope, nil
	}
	for path, values := range profileScopes {
		scope := map[string]string{}
		for k, v := range values {
			scope[k] = v
		}
		pathsByScope[path] = scope
	}
	return shared, pathsByScope, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
