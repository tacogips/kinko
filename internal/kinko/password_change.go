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
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const mutationLockFileName = ".mutation.lock"
const maxPasswordInputBytes = 4096
const defaultPasswordFDReadTimeout = 5 * time.Second

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
	fs := flag.NewFlagSet("password change", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		currentStdin bool
		newStdin     bool
		forceTTY     bool
		currentFD    int
		newFD        int
	)
	currentFD = -1
	newFD = -1
	fs.BoolVar(&currentStdin, "current-stdin", false, "read current password from stdin")
	fs.BoolVar(&newStdin, "new-stdin", false, "read new password from stdin")
	fs.BoolVar(&forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	fs.IntVar(&currentFD, "current-fd", -1, "read current password from file descriptor")
	fs.IntVar(&newFD, "new-fd", -1, "read new password from file descriptor")

	if err := fs.Parse(args); err != nil {
		return newCLIError(exitCodePolicyFailed, "Invalid password change arguments.", err)
	}
	if fs.NArg() != 0 {
		return newCLIError(exitCodePolicyFailed, "password change does not accept positional arguments.", nil)
	}

	current, next, err := readPasswordChangeInputs(stdin, stderr, passwordInputOptions{
		currentStdin: currentStdin,
		newStdin:     newStdin,
		currentFD:    currentFD,
		newFD:        newFD,
		forceTTY:     forceTTY,
	})
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if err := validateNewPassword(current, next); err != nil {
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

	params, err := floorEnforcedPasswordKDFParams(meta.KDFParamsPassword)
	if err != nil {
		return newCLIError(exitCodeMetadataInvalid, "Metadata/KDF parameters rejected by safety validation.", err)
	}

	newSalt := mustRandom(saltLength)
	newKEK := deriveKEK(next, newSalt, params)
	newWrappedDEK, err := encryptBlob(newKEK, oldDEK)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to produce new password wrap.", err)
	}
	pub, priv := deriveSessionKeyPairFromPassword(next)
	newEncSessionPriv, err := encryptBlob(oldDEK, priv)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to update session key material.", err)
	}

	nextMeta := *meta
	nextMeta.SaltPasswordB64 = base64.StdEncoding.EncodeToString(newSalt)
	nextMeta.WrappedDEKPassB64 = newWrappedDEK
	nextMeta.SessionPubKeyB64 = base64.StdEncoding.EncodeToString(pub)
	nextMeta.EncSessionPrivB64 = newEncSessionPriv
	nextMeta.KDFParamsPassword = params
	nextMeta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := saveMetaAtomically(opts.dataDir, &nextMeta); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to persist password update atomically. No changes were applied.", err)
	}

	prevMeta := cloneVaultMeta(meta)
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
		if next != confirm {
			return "", "", errors.New("new password confirmation does not match")
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
	if next != confirm {
		return "", "", errors.New("new password confirmation does not match")
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

func validateNewPassword(current, next string) error {
	if next == current {
		return errors.New("new password must differ from current password")
	}
	if utf8.RuneCountInString(next) < 12 {
		return errors.New("new password must be at least 12 characters")
	}
	if strings.TrimSpace(next) == "" {
		return errors.New("new password must not be empty after trimming whitespace")
	}
	if strings.TrimSpace(next) != next {
		return errors.New("leading/trailing whitespace is not allowed")
	}
	for _, r := range next {
		if !utf8.ValidRune(r) {
			return errors.New("password must be valid UTF-8")
		}
		if unicode.IsControl(r) {
			return errors.New("control characters are not allowed in password")
		}
	}
	return nil
}

func acquireMutationLock(dataDir string) (func(), error) {
	lockPath := filepath.Join(dataDir, "vault", mutationLockFileName)
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock exists: %w", err)
		}
		return nil, err
	}
	release := func() {
		_ = f.Close()
		_ = os.Remove(lockPath)
	}
	return release, nil
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
