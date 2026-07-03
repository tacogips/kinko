package kinko

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const mutationLockFileName = ".mutation.lock"
const maxPasswordInputBytes = 4096
const defaultPasswordFDReadTimeout = 5 * time.Second

type mutationLockMetadata struct {
	PID       int    `json:"pid"`
	Hostname  string `json:"hostname"`
	CreatedAt string `json:"created_at"`
}

func runPassword(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("password requires subcommand: change")
	}
	switch args[0] {
	case "change":
		return runPasswordChange(opts, args[1:], stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown password subcommand %q", args[0])
	}
}

func runPasswordChange(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	inputOpts, err := parsePasswordChangeInputOptions(args)
	if err != nil {
		return err
	}
	return runPasswordChangeWithOptions(opts, inputOpts, stdin, stdout, stderr)
}

func parsePasswordChangeInputOptions(args []string) (passwordInputOptions, error) {
	fs := flag.NewFlagSet("password change", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	inputOpts := passwordInputOptions{
		currentFD: -1,
		newFD:     -1,
	}
	fs.BoolVar(&inputOpts.currentStdin, "current-stdin", false, "read current password from stdin")
	fs.BoolVar(&inputOpts.newStdin, "new-stdin", false, "read new password from stdin")
	fs.BoolVar(&inputOpts.forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	fs.IntVar(&inputOpts.currentFD, "current-fd", -1, "read current password from file descriptor")
	fs.IntVar(&inputOpts.newFD, "new-fd", -1, "read new password from file descriptor")

	if err := fs.Parse(args); err != nil {
		return passwordInputOptions{}, newCLIError(exitCodePolicyFailed, "Invalid password change arguments.", err)
	}
	if fs.NArg() != 0 {
		return passwordInputOptions{}, newCLIError(exitCodePolicyFailed, "password change does not accept positional arguments.", nil)
	}
	return inputOpts, nil
}

func runPasswordChangeWithOptions(opts globalOptions, inputOpts passwordInputOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	current, next, err := readPasswordChangeInputs(stdin, stderr, inputOpts)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	current, err = sanitizePasswordValue(current)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, "Current password is invalid.", err)
	}
	next, err = sanitizePasswordValue(next)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, "New password does not satisfy policy requirements.", err)
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Password change could not acquire mutation lock.", err)
	}
	defer release()

	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to load vault metadata.", err)
	}

	oldDEK, err := unwrapDEKWithPassword(meta, current)
	if err != nil {
		switch {
		case errors.Is(err, errMetadataInvalid):
			return newCLIError(exitCodeMetadataInvalid, "Metadata/KDF parameters rejected by safety validation.", err)
		case isCredentialMismatchError(err):
			return newCLIError(exitCodeAuthFailed, "Current password is invalid.", err)
		default:
			return newCLIError(exitCodeIOFailed, "Failed to verify current password.", err)
		}
	}
	if current == next {
		return newCLIError(exitCodePolicyFailed, "New password must differ from current password.", errors.New("new password must differ from current password"))
	}

	params, err := floorEnforcedPasswordKDFParams(meta.KDFParamsPassword)
	if err != nil {
		return newCLIError(exitCodeMetadataInvalid, "Metadata/KDF parameters rejected by safety validation.", err)
	}

	newSalt := mustRandom(saltLength)
	newKEK := deriveKEK(next, newSalt, params)
	newWrappedDEK, err := encryptBlobWithAAD(newKEK, oldDEK, []byte(aeadContextWrappedDEKPass))
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to produce new password wrap.", err)
	}
	newSessionPubB64, newEncSessionPriv, err := newRandomSessionKeyMaterial(oldDEK)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to update session key material.", err)
	}

	nextMeta := *meta
	nextMeta.SaltPasswordB64 = base64.StdEncoding.EncodeToString(newSalt)
	nextMeta.WrappedDEKPassB64 = newWrappedDEK
	nextMeta.SessionPubKeyB64 = newSessionPubB64
	nextMeta.EncSessionPrivB64 = newEncSessionPriv
	nextMeta.SessionKeySource = sessionKeyRandom
	nextMeta.KDFParamsPassword = params
	nextMeta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	prevMeta := cloneVaultMeta(meta)
	if err := saveMetaAtomically(opts.dataDir, &nextMeta); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to persist password update atomically. No changes were applied.", err)
	}

	if err := deleteSessionWrapKeyForMeta(opts.dataDir, prevMeta); err != nil {
		if rbErr := saveMetaAtomically(opts.dataDir, prevMeta); rbErr != nil {
			return newCLIError(exitCodeIOFailed, "Failed to revoke previous session wrap key after password update, and rollback failed.", fmt.Errorf("revoke old wrap key: %v; rollback: %w", err, rbErr))
		}
		return newCLIError(exitCodeIOFailed, "Failed to revoke previous session wrap key. Password change was rolled back.", err)
	}

	if err := lockSessionWithWarning(opts.dataDir, stderr); err != nil {
		if rbErr := saveMetaAtomically(opts.dataDir, prevMeta); rbErr != nil {
			return newCLIError(exitCodeIOFailed, "Failed to revoke active sessions after password update, and rollback failed.", fmt.Errorf("revoke: %v; rollback: %w", err, rbErr))
		}
		return newCLIError(exitCodeIOFailed, "Failed to revoke active sessions. Password change was rolled back.", err)
	}

	_, _ = fmt.Fprintln(stdout, "Password changed successfully. Vault is now locked.")
	return nil
}

type passwordInputOptions struct {
	currentStdin bool
	newStdin     bool
	currentFD    int
	newFD        int
	forceTTY     bool
}

func readPasswordChangeInputs(stdin io.Reader, stderr io.Writer, opts passwordInputOptions) (string, string, error) {
	useFD := opts.currentFD >= 0 || opts.newFD >= 0
	useStdin := opts.currentStdin || opts.newStdin

	switch {
	case useFD && useStdin:
		return "", "", errors.New("mixed stdin/fd password input modes are not supported")
	case useFD:
		if opts.currentFD < 0 || opts.newFD < 0 {
			return "", "", errors.New("both --current-fd and --new-fd are required")
		}
		current, err := readPasswordFromFD(opts.currentFD)
		if err != nil {
			return "", "", err
		}
		next, err := readPasswordFromFD(opts.newFD)
		if err != nil {
			return "", "", err
		}
		return current, next, nil
	case useStdin:
		if !opts.currentStdin || !opts.newStdin {
			return "", "", errors.New("both --current-stdin and --new-stdin are required")
		}
		if isTerminalReader(stdin) {
			return "", "", errors.New("stdin is a TTY; non-interactive stdin mode is not allowed")
		}
		reader := bufio.NewReader(stdin)
		current, err := readPasswordLine(reader)
		if err != nil {
			return "", "", fmt.Errorf("read current password: %w", err)
		}
		next, err := readPasswordLine(reader)
		if err != nil {
			return "", "", fmt.Errorf("read new password: %w", err)
		}
		return current, next, nil
	default:
		return readPasswordInteractive(stdin, stderr, opts.forceTTY)
	}
}

func readPasswordInteractive(stdin io.Reader, stderr io.Writer, forceTTY bool) (string, string, error) {
	if isTerminalReader(stdin) {
		current, err := readSecretNoTrim(stdin, stderr, "Current password: ")
		if err != nil {
			return "", "", err
		}
		next, err := readSecretNoTrim(stdin, stderr, "New password: ")
		if err != nil {
			return "", "", err
		}
		confirm, err := readSecretNoTrim(stdin, stderr, "Confirm new password: ")
		if err != nil {
			return "", "", err
		}
		next, err = normalizeConfirmedPassword(next, confirm)
		if err != nil {
			return "", "", err
		}
		return current, next, nil
	}
	if !forceTTY {
		return "", "", errors.New("interactive password prompts require a TTY; use --current-stdin/--new-stdin or --current-fd/--new-fd")
	}
	reader := bufio.NewReader(stdin)
	current, err := readPasswordLineWithPrompt(reader, stderr, "Current password: ")
	if err != nil {
		return "", "", err
	}
	next, err := readPasswordLineWithPrompt(reader, stderr, "New password: ")
	if err != nil {
		return "", "", err
	}
	confirm, err := readPasswordLineWithPrompt(reader, stderr, "Confirm new password: ")
	if err != nil {
		return "", "", err
	}
	next, err = normalizeConfirmedPassword(next, confirm)
	if err != nil {
		return "", "", err
	}
	return current, next, nil
}

func readSecretNoTrim(stdin io.Reader, stderr io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(stderr, prompt); err != nil {
		return "", err
	}
	f, ok := stdin.(interface{ Fd() uintptr })
	if !ok {
		return "", errors.New("stdin does not expose a file descriptor")
	}
	b, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	s, err := normalizePasswordInput(b)
	if err != nil {
		return "", err
	}
	return s, nil
}

func readPasswordLineWithPrompt(reader *bufio.Reader, stderr io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(stderr, prompt); err != nil {
		return "", err
	}
	return readPasswordLine(reader)
}

func readPasswordFromFD(fd int) (string, error) {
	if fd < 0 {
		return "", errors.New("invalid file descriptor")
	}
	timeout := passwordFDReadTimeout()
	data, err := readPasswordBytesFromFD(fd, timeout, maxPasswordInputBytes)
	if err != nil {
		return "", err
	}
	if len(data) > maxPasswordInputBytes {
		return "", fmt.Errorf("password input exceeds maximum size (%d bytes)", maxPasswordInputBytes)
	}
	return normalizePasswordInput(data)
}

func passwordFDReadTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("KINKO_PASSWORD_FD_TIMEOUT"))
	if v == "" {
		return defaultPasswordFDReadTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return defaultPasswordFDReadTimeout
	}
	return d
}

func cloneVaultMeta(meta *vaultMeta) *vaultMeta {
	if meta == nil {
		return nil
	}
	clone := *meta
	if meta.KDFParamsPassword != nil {
		kdfClone := *meta.KDFParamsPassword
		clone.KDFParamsPassword = &kdfClone
	}
	return &clone
}

func readPasswordLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(line) == 0 && errors.Is(err, io.EOF) {
		return "", io.EOF
	}
	return normalizePasswordInput(line)
}

func normalizePasswordInput(raw []byte) (string, error) {
	b := raw
	if len(b) >= 2 && b[len(b)-2] == '\r' && b[len(b)-1] == '\n' {
		b = b[:len(b)-2]
	} else if len(b) >= 1 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	s := string(b)
	if strings.ContainsRune(s, '\n') || strings.ContainsRune(s, '\r') {
		return "", errors.New("embedded newline characters are not allowed in password input")
	}
	return s, nil
}

func sanitizePasswordValue(password string) (string, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return "", errors.New("password must not be empty after trimming whitespace")
	}
	if !utf8.ValidString(password) {
		return "", errors.New("password must be valid UTF-8")
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return "", errors.New("control characters are not allowed in password")
		}
	}
	return password, nil
}

func normalizeConfirmedPassword(next, confirm string) (string, error) {
	next, err := sanitizePasswordValue(next)
	if err != nil {
		return "", err
	}
	confirm, err = sanitizePasswordValue(confirm)
	if err != nil {
		return "", err
	}
	if next != confirm {
		return "", errors.New("new password confirmation does not match")
	}
	return next, nil
}

func acquireMutationLock(dataDir string) (func(), error) {
	lockPath := filepath.Join(dataDir, "vault", mutationLockFileName)
	return acquireMetadataLock(lockPath)
}

func acquireMetadataLock(lockPath string) (func(), error) {
	metadata, err := currentMutationLockMetadata()
	if err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode lock metadata: %w", err)
	}
	payload = append(payload, '\n')

	for {
		release, err := createMetadataLock(lockPath, payload)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		takeover, err := canTakeOverMetadataLock(lockPath, metadata.Hostname)
		if err != nil {
			return nil, err
		}
		if !takeover {
			return nil, lockConflictError(lockPath)
		}
		if err := os.Remove(lockPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("remove stale lock %s: %w", lockPath, err)
		}
	}
}

func createMetadataLock(lockPath string, payload []byte) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("write lock metadata: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("sync lock metadata: %w", err)
	}
	release := func() {
		_ = f.Close()
		_ = os.Remove(lockPath)
	}
	return release, nil
}

func currentMutationLockMetadata() (mutationLockMetadata, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return mutationLockMetadata{}, fmt.Errorf("resolve hostname for lock metadata: %w", err)
	}
	return mutationLockMetadata{
		PID:       os.Getpid(),
		Hostname:  hostname,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func canTakeOverMetadataLock(lockPath, hostname string) (bool, error) {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("read lock metadata %s: %w", lockPath, err)
	}
	var metadata mutationLockMetadata
	if err := json.Unmarshal(b, &metadata); err != nil {
		return false, lockConflictError(lockPath)
	}
	if metadata.PID <= 0 || metadata.Hostname == "" || metadata.CreatedAt == "" {
		return false, lockConflictError(lockPath)
	}
	if metadata.Hostname != hostname {
		return false, lockConflictError(lockPath)
	}
	running, err := processIsRunning(metadata.PID)
	if err != nil {
		return false, lockConflictError(lockPath)
	}
	return !running, nil
}

func processIsRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, err
}

func lockConflictError(lockPath string) error {
	return fmt.Errorf("lock exists at %s; if no kinko process is active, inspect the lock file and remove it manually", lockPath)
}

func saveMetaAtomically(dataDir string, meta *vaultMeta) error {
	metaPath := filepath.Join(dataDir, "vault", "meta.v1.json")
	if err := validateMetaTarget(metaPath); err != nil {
		return err
	}

	tmpPath := metaPath + ".tmp"
	_ = os.Remove(tmpPath)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, metaPath); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(metaPath))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return err
	}
	return nil
}

func validateMetaTarget(metaPath string) error {
	fi, err := os.Lstat(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("meta target must not be a symlink")
	}
	if !fi.Mode().IsRegular() {
		return errors.New("meta target must be a regular file")
	}
	return nil
}
