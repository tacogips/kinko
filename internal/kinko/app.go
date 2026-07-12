package kinko

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultProfile = "default"
const maxCredentialAttempts = 3

type globalOptions struct {
	profile           string
	path              string
	dataDir           string
	configPath        string
	force             bool
	confirm           bool
	keychainPreflight string
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx := &runtimeContext{
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		rawArgs: append([]string{}, args...),
	}
	root, err := newRuntimeRootCommand(ctx)
	if err != nil {
		return err
	}
	root.SetArgs(args)
	return root.Execute()
}

func runInit(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("init does not accept positional arguments")
	}

	if isInitializedDataDir(opts.dataDir) {
		return fmt.Errorf("kinko is already initialized in %s", opts.dataDir)
	}
	if anyVaultArtifact(opts.dataDir) {
		return fmt.Errorf("data dir %s contains partial or complete vault data; move it aside or run 'kinko %s' first", opts.dataDir, cmdExplosion)
	}

	preflightMode := opts.keychainPreflight
	if preflightMode == "" {
		preflightMode = "required"
	}
	if preflightMode == "off" {
		_, _ = fmt.Fprintln(stderr, "WARNING: keychain preflight is disabled; init may succeed even if unlock later fails due to keychain unavailability.")
	}
	if preflightMode != "off" {
		if err := ensureSessionSecretStoreReady(); err != nil {
			if preflightMode == "required" {
				return fmt.Errorf("keychain preflight failed: %w", err)
			}
			_, _ = fmt.Fprintf(stderr, "WARNING: keychain preflight failed, continuing due to --keychain-preflight=best-effort: %v\n", err)
			_, _ = fmt.Fprintln(stderr, "WARNING: unlock/session may fail later until keychain backend access is restored.")
		}
	}
	if err := ensureDirLayout(opts.dataDir); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stderr, "Initialization will create an encrypted local vault.")
	_, _ = fmt.Fprintln(stderr, "You must remember your password.")
	_, _ = fmt.Fprintln(stderr, "WARNING: If you lose your password, vault data cannot be restored.")

	pass, err := readPasswordWithRetries(stdin, stderr, maxCredentialAttempts)
	if err != nil {
		return err
	}

	if err := initVault(opts.dataDir, pass); err != nil {
		return err
	}
	if err := writeBootstrapConfig(opts); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "initialized")
	return nil
}

func runUnlock(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	unlockOpts := unlockOptions{timeout: 9 * time.Hour}
	fs.DurationVar(&unlockOpts.timeout, "timeout", 9*time.Hour, "unlock timeout")
	if err := fs.Parse(args); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if fs.NArg() != 0 {
		return newCLIError(exitCodePolicyFailed, "unlock does not accept positional arguments", nil)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			unlockOpts.timeoutProvided = true
		}
	})
	return runUnlockWithOptions(opts, unlockOpts, stdin, stdout, stderr)
}

type unlockOptions struct {
	timeout         time.Duration
	timeoutProvided bool
}

func runUnlockWithOptions(opts globalOptions, unlockOpts unlockOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	locked, expiresAt, err := sessionStatus(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to inspect session status.", err)
	}
	if !locked {
		if unlockOpts.timeoutProvided {
			if err := lockSessionWithWarning(opts.dataDir, stderr); err != nil {
				return newCLIError(exitCodeIOFailed, "Lock existing session before unlock refresh failed.", err)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "unlocked (auto-lock at %s)\n", formatAutoLockTimeLocal(expiresAt))
			return nil
		}
	}
	preflightMode := opts.keychainPreflight
	if preflightMode == "" {
		preflightMode = "required"
	}
	if preflightMode != "off" {
		if err := ensureSessionSecretStoreReady(); err != nil {
			if preflightMode == "required" {
				return newCLIError(exitCodeIOFailed, "keychain unavailable for unlock", err)
			}
			_, _ = fmt.Fprintf(stderr, "WARNING: keychain preflight failed for unlock, continuing due to --keychain-preflight=best-effort: %v\n", err)
		}
	}

	if err := unlockWithRetries(opts, unlockOpts.timeout, stdin, stderr, maxCredentialAttempts); err != nil {
		return newCLIError(unlockFailureExitCode(err), err.Error(), err)
	}
	locked, expiresAt, err = sessionStatus(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to inspect session status after unlock.", err)
	}
	if locked {
		return newCLIError(exitCodeIOFailed, "locked", errors.New("session remained locked after unlock"))
	}
	_, _ = fmt.Fprintf(stdout, "unlocked (auto-lock at %s)\n", formatAutoLockTimeLocal(expiresAt))
	return nil
}

func unlockFailureExitCode(err error) int {
	if isUnlockCredentialFailure(err) {
		return exitCodeAuthFailed
	}
	return exitCodeIOFailed
}

func isUnlockCredentialFailure(err error) bool {
	if errors.Is(err, errUnlockCredential) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "credential mismatch") || strings.Contains(msg, "wrapped-key integrity failure")
}

func runLock(opts globalOptions, stderr io.Writer) error {
	return lockSessionWithWarning(opts.dataDir, stderr)
}

func readPasswordWithRetries(stdin io.Reader, stderr io.Writer, maxAttempts int) (string, error) {
	for i := 1; i <= maxAttempts; i++ {
		pass, err := readPasswordWithConfirm(stdin, stderr, "New password: ", "Confirm password: ")
		if err == nil {
			return pass, nil
		}
		remaining := maxAttempts - i
		if remaining > 0 {
			_, _ = fmt.Fprintf(stderr, "Password setup failed: %v. Try again (%d attempts left).\n", err, remaining)
			continue
		}
		return "", fmt.Errorf("password setup failed after %d attempts: %w", maxAttempts, err)
	}
	return "", errors.New("unreachable")
}

func unlockWithRetries(opts globalOptions, timeout time.Duration, stdin io.Reader, stderr io.Writer, maxAttempts int) error {
	prompt := "Password: "
	var buffered *bufio.Reader
	if !isTerminalReader(stdin) {
		buffered = bufio.NewReader(stdin)
	}
	for i := 1; i <= maxAttempts; i++ {
		var (
			secret string
			err    error
		)
		if buffered != nil {
			secret, err = readSecretWithPromptBuffered(buffered, stderr, prompt)
		} else {
			secret, err = readSecret(stdin, stderr, prompt)
		}
		if err != nil {
			remaining := maxAttempts - i
			if remaining > 0 {
				_, _ = fmt.Fprintf(stderr, "Credential input failed: %v. Try again (%d attempts left).\n", err, remaining)
				continue
			}
			return fmt.Errorf("unlock failed after %d attempts: %w", maxAttempts, err)
		}
		if err := unlockSession(opts.dataDir, timeout, secret); err == nil {
			return nil
		} else {
			if !errors.Is(err, errUnlockCredential) {
				return fmt.Errorf("unlock failed: %w", err)
			}
			remaining := maxAttempts - i
			if remaining > 0 {
				_, _ = fmt.Fprintf(stderr, "Unlock failed. Try again (%d attempts left).\n", remaining)
				continue
			}
			return fmt.Errorf("unlock failed after %d attempts: credential mismatch or wrapped-key integrity failure", maxAttempts)
		}
	}
	return errors.New("unreachable")
}

func runStatus(opts globalOptions, stdout io.Writer) error {
	locked, expiresAt, err := sessionStatus(opts.dataDir)
	if err != nil {
		return err
	}
	if locked {
		_, _ = fmt.Fprintln(stdout, "locked")
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "unlocked (auto-lock at %s)\n", formatAutoLockTimeLocal(expiresAt))
	return nil
}

func formatAutoLockTimeLocal(expiresAt time.Time) string {
	local := expiresAt.In(time.Local)
	return local.Format("2006-01-02 15:04:05 MST")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func isInitializedDataDir(dataDir string) bool {
	meta := filepath.Join(dataDir, "vault", "meta.v1.json")
	vault := filepath.Join(dataDir, "vault", "vault.v1.bin")
	config := filepath.Join(dataDir, "vault", "config.v1.bin")
	return fileExists(meta) && fileExists(vault) && fileExists(config)
}

// anyVaultArtifact reports whether any vault artifact (complete or partial)
// exists under dataDir. Unlike isInitializedDataDir, which requires every
// artifact to be present, this treats a single surviving artifact as a
// signal that the data dir already holds vault state and must not be
// silently overwritten by init.
func anyVaultArtifact(dataDir string) bool {
	meta := filepath.Join(dataDir, "vault", "meta.v1.json")
	vault := filepath.Join(dataDir, "vault", "vault.v1.bin")
	config := filepath.Join(dataDir, "vault", "config.v1.bin")
	marker := filepath.Join(dataDir, "vault", vaultMarker)
	return fileExists(meta) || fileExists(vault) || fileExists(config) || fileExists(marker)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func normalizePathInput(path string) string {
	p := strings.TrimSpace(path)
	// direnv may expose path as "-/abs/path"; strip the prefix to treat it as absolute path.
	if strings.HasPrefix(p, "-/") {
		return p[1:]
	}
	return p
}
