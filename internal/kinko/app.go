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
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	root := newRuntimeRootCommand(ctx)
	root.SetArgs(args)
	return root.Execute()
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return globalOptions{}, nil, fmt.Errorf("resolve cwd: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return globalOptions{}, nil, fmt.Errorf("resolve home dir: %w", err)
	}

	fs := flag.NewFlagSet("kinko", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := globalOptions{}
	fs.StringVar(&opts.profile, "profile", envOrDefault("KINKO_PROFILE", defaultProfile), "profile")
	fs.StringVar(&opts.path, "path", envOrDefault("KINKO_PATH", cwd), "path")
	fs.StringVar(&opts.dataDir, "kinko-dir", envOrDefault("KINKO_DATA_DIR", filepath.Join(home, ".local", "kinko")), "kinko data dir")
	fs.StringVar(&opts.configPath, "config", envOrDefault("KINKO_CONFIG", filepath.Join(home, ".config", "kinko", "bootstrap.toml")), "bootstrap config path")
	fs.StringVar(&opts.keychainPreflight, "keychain-preflight", envOrDefault("KINKO_KEYCHAIN_PREFLIGHT", "required"), "keychain preflight mode: required|best-effort|off")
	fs.BoolVar(&opts.force, "force", false, "override non-tty/redirection guardrails")
	fs.BoolVar(&opts.confirm, "confirm", true, "confirm sensitive tty output")

	if err := fs.Parse(args); err != nil {
		return globalOptions{}, nil, err
	}
	if strings.TrimSpace(opts.profile) == "" {
		return globalOptions{}, nil, errors.New("--profile must not be empty")
	}

	absPath, err := filepath.Abs(normalizePathInput(opts.path))
	if err != nil {
		return globalOptions{}, nil, fmt.Errorf("resolve --path: %w", err)
	}
	opts.path = filepath.Clean(absPath)

	absDataDir, err := filepath.Abs(opts.dataDir)
	if err != nil {
		return globalOptions{}, nil, fmt.Errorf("resolve --kinko-dir: %w", err)
	}
	opts.dataDir = filepath.Clean(absDataDir)
	absConfigPath, err := filepath.Abs(opts.configPath)
	if err != nil {
		return globalOptions{}, nil, fmt.Errorf("resolve --config: %w", err)
	}
	opts.configPath = filepath.Clean(absConfigPath)
	switch opts.keychainPreflight {
	case "required", "best-effort", "off":
	default:
		return globalOptions{}, nil, errors.New("--keychain-preflight must be one of: required, best-effort, off")
	}

	return opts, fs.Args(), nil
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
	timeout := 9 * time.Hour
	fs.DurationVar(&timeout, "timeout", 9*time.Hour, "unlock timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	locked, expiresAt, err := sessionStatus(opts.dataDir)
	if err != nil {
		return err
	}
	if !locked {
		_, _ = fmt.Fprintf(stdout, "unlocked (auto-lock at %s)\n", formatAutoLockTimeLocal(expiresAt))
		return nil
	}
	preflightMode := opts.keychainPreflight
	if preflightMode == "" {
		preflightMode = "required"
	}
	if preflightMode != "off" {
		if err := ensureSessionSecretStoreReady(); err != nil {
			if preflightMode == "required" {
				return fmt.Errorf("keychain unavailable for unlock: %w", err)
			}
			_, _ = fmt.Fprintf(stderr, "WARNING: keychain preflight failed for unlock, continuing due to --keychain-preflight=best-effort: %v\n", err)
		}
	}

	if err := unlockWithRetries(opts, timeout, stdin, stderr, maxCredentialAttempts); err != nil {
		return err
	}
	locked, expiresAt, err = sessionStatus(opts.dataDir)
	if err != nil {
		return err
	}
	if locked {
		return errors.New("locked")
	}
	_, _ = fmt.Fprintf(stdout, "unlocked (auto-lock at %s)\n", formatAutoLockTimeLocal(expiresAt))
	return nil
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
