package kinko

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// maxSetAssignmentLineSize is the maximum single-line size accepted when
// reading KEY=VALUE assignments from stdin. This is larger than the
// bufio.Scanner default (bufio.MaxScanTokenSize, 64 KiB) so that reasonably
// large secret values do not fail with a confusing "token too long" error.
const maxSetAssignmentLineSize = 4 * 1024 * 1024

func runSet(opts globalOptions, args []string, stdin io.Reader, stdout io.Writer) error {
	setOpts, err := parseSetOptions(args)
	if err != nil {
		return err
	}
	return runSetWithOptions(opts, setOpts, stdin, stdout)
}

type setOptions struct {
	shared      bool
	assignments []string
}

func parseSetOptions(args []string) (setOptions, error) {
	setOpts := setOptions{
		assignments: make([]string, 0, len(args)),
	}
	for _, a := range args {
		if a == "--shared" {
			setOpts.shared = true
			continue
		}
		if a == "--value" || strings.HasPrefix(a, "--value=") {
			return setOptions{}, errors.New("set only accepts KEY=VALUE assignments; use set-key for --value mode")
		}
		if strings.HasPrefix(a, "-") {
			return setOptions{}, fmt.Errorf("set: unknown flag %q", a)
		}
		setOpts.assignments = append(setOpts.assignments, a)
	}
	return setOpts, nil
}

func runSetWithOptions(opts globalOptions, setOpts setOptions, stdin io.Reader, stdout io.Writer) error {
	keys := []string{}
	values := map[string]string{}
	if len(setOpts.assignments) > 0 {
		for _, a := range setOpts.assignments {
			key, val, err := parseSetAssignment(a)
			if err != nil {
				return err
			}
			if _, seen := values[key]; !seen {
				keys = append(keys, key)
			}
			values[key] = val
		}
	} else {
		if isTerminalReader(stdin) {
			return errors.New("set requires at least one KEY=VALUE argument when stdin is interactive")
		}
		assignments, err := parseSetAssignmentsFromReader(stdin)
		if err != nil {
			return err
		}
		if len(assignments) == 0 {
			return errors.New("set requires KEY=VALUE input")
		}
		for _, a := range assignments {
			if _, seen := values[a.key]; !seen {
				keys = append(keys, a.key)
			}
			values[a.key] = a.value
		}
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
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return err
	}
	if vd.Shared == nil {
		vd.Shared = map[string]string{}
	}
	if !setOpts.shared && vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	if !setOpts.shared && vd.Profiles[opts.profile][opts.path] == nil {
		vd.Profiles[opts.profile][opts.path] = map[string]string{}
	}
	for _, k := range keys {
		if setOpts.shared {
			vd.Shared[k] = values[k]
			continue
		}
		vd.Profiles[opts.profile][opts.path][k] = values[k]
	}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s set\n", strings.Join(keys, ","))
	return nil
}

func runSetKey(opts globalOptions, args []string, stdin io.Reader, stdout io.Writer) error {
	setKeyOpts, err := parseSetKeyArgs(args)
	if err != nil {
		return err
	}
	return runSetKeyWithOptions(opts, setKeyOpts, stdin, stdout)
}

type setKeyOptions struct {
	key           string
	value         string
	valueProvided bool
	shared        bool
}

func runSetKeyWithOptions(opts globalOptions, setKeyOpts setKeyOptions, stdin io.Reader, stdout io.Writer) error {
	if strings.Contains(setKeyOpts.key, "=") {
		return errors.New("set-key expects key only (without '=')")
	}
	if err := validateEnvKey(setKeyOpts.key); err != nil {
		return err
	}
	if !setKeyOpts.valueProvided {
		v, err := readTrimmedLine(stdin)
		if err != nil {
			return err
		}
		if v == "" {
			return errors.New("set-key requires --value or stdin value")
		}
		setKeyOpts.value = v
	}
	setOpts := setOptions{
		shared:      setKeyOpts.shared,
		assignments: []string{setKeyOpts.key + "=" + setKeyOpts.value},
	}
	return runSetWithOptions(opts, setOpts, strings.NewReader(""), stdout)
}

func parseSetKeyArgs(args []string) (setKeyOptions, error) {
	var setKeyOpts setKeyOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--shared":
			setKeyOpts.shared = true
		case a == "--value":
			if i+1 >= len(args) {
				return setKeyOptions{}, errors.New("set-key requires value for --value")
			}
			setKeyOpts.value = args[i+1]
			setKeyOpts.valueProvided = true
			i++
		case strings.HasPrefix(a, "--value="):
			setKeyOpts.value = strings.TrimPrefix(a, "--value=")
			setKeyOpts.valueProvided = true
		case strings.HasPrefix(a, "-"):
			return setKeyOptions{}, fmt.Errorf("set-key: unknown flag %q", a)
		default:
			if setKeyOpts.key != "" {
				return setKeyOptions{}, errors.New("set-key requires a key")
			}
			setKeyOpts.key = a
		}
	}
	if setKeyOpts.key == "" {
		return setKeyOptions{}, errors.New("set-key requires a key")
	}
	return setKeyOpts, nil
}

type setAssignment struct {
	key   string
	value string
}

func parseSetAssignment(raw string) (string, string, error) {
	i := strings.Index(raw, "=")
	if i <= 0 {
		return "", "", fmt.Errorf("invalid assignment %q (expected KEY=VALUE)", raw)
	}
	key := strings.TrimSpace(raw[:i])
	val := strings.TrimRight(raw[i+1:], "\r")
	if err := validateEnvKey(key); err != nil {
		return "", "", err
	}
	return key, val, nil
}

func parseSetAssignmentsFromReader(r io.Reader) ([]setAssignment, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxSetAssignmentLineSize)
	out := []setAssignment{}
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		k, v, err := parseSetAssignment(line)
		if err != nil {
			return nil, fmt.Errorf("invalid assignment at line %d: %w", lineNo, err)
		}
		out = append(out, setAssignment{key: k, value: v})
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("set: input line exceeds maximum size of %d bytes (line %d): %w", maxSetAssignmentLineSize, lineNo+1, err)
		}
		return nil, err
	}
	return out, nil
}

func runDelete(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	deleteOpts, err := parseDeleteOptions(args)
	if err != nil {
		return err
	}
	return runDeleteWithOptions(opts, deleteOpts, stdin, stdout, stderr)
}

type deleteOptions struct {
	key       string
	autoYes   bool
	deleteAll bool
	shared    bool
}

func parseDeleteOptions(args []string) (deleteOptions, error) {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var deleteOpts deleteOptions
	fs.BoolVar(&deleteOpts.autoYes, "yes", false, "auto confirm deletion")
	fs.BoolVar(&deleteOpts.autoYes, "y", false, "auto confirm deletion")
	fs.BoolVar(&deleteOpts.deleteAll, "all", false, "delete all keys in resolved profile/path scope")
	fs.BoolVar(&deleteOpts.shared, "shared", false, "delete keys from shared scope")
	if err := fs.Parse(args); err != nil {
		return deleteOptions{}, err
	}
	if deleteOpts.deleteAll && fs.NArg() > 0 {
		return deleteOptions{}, errors.New("delete --all cannot be combined with a key")
	}
	if !deleteOpts.deleteAll && fs.NArg() != 1 {
		return deleteOptions{}, errors.New("delete requires a key or --all")
	}
	if fs.NArg() == 1 {
		deleteOpts.key = fs.Arg(0)
	}
	return deleteOpts, nil
}

func runDeleteWithOptions(opts globalOptions, deleteOpts deleteOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	confirmationInput := stdin
	var passwordInput passwordVerificationInput
	if deleteOpts.deleteAll {
		passwordInput = passwordVerificationInputFor(stdin, isTerminalReader)
		confirmationInput = passwordInput.confirmationInput
		if deleteOpts.autoYes {
			if err := verifyVaultPasswordForBulkDelete(opts, passwordInput, stderr); err != nil {
				return newCLIError(exitCodeAuthFailed, err.Error(), err)
			}
		}
	}

	// Load vault state without holding the mutation lock (Finding 3): this
	// lets us compute the preview (target scope/keys) shown before any
	// blocking confirmation prompt without blocking concurrent mutators
	// while waiting on user input.
	_, previewVD, err := loadUnlockedVaultForDelete(opts)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to load vault.", err)
	}

	if deleteOpts.deleteAll {
		scope := deleteAllScope(previewVD, opts, deleteOpts)
		if len(scope) == 0 {
			if deleteOpts.shared {
				return errors.New("no secrets found in shared scope")
			}
			return errors.New("no secrets found in current profile/path scope")
		}
		if !deleteOpts.autoYes {
			keys := sortedKeys(scope)
			if _, err := fmt.Fprintln(stderr, "Delete target keys:"); err != nil {
				return err
			}
			for _, key := range keys {
				if _, err := fmt.Fprintf(stderr, "- %s\n", key); err != nil {
					return err
				}
			}
			msg := fmt.Sprintf("Delete all %d keys in profile=%q path=%q? [y/N]: ", len(scope), opts.profile, opts.path)
			if deleteOpts.shared {
				msg = fmt.Sprintf("Delete all %d keys in shared scope? [y/N]: ", len(scope))
			}
			ok, err := confirmPrompt(confirmationInput, stderr, msg)
			if err != nil {
				return err
			}
			if !ok {
				_, _ = fmt.Fprintln(stdout, "aborted")
				return nil
			}
			if err := verifyVaultPasswordForBulkDelete(opts, passwordInput, stderr); err != nil {
				return newCLIError(exitCodeAuthFailed, err.Error(), err)
			}
		}

		release, err := acquireMutationLock(opts.dataDir)
		if err != nil {
			return newCLIError(exitCodeLockConflict, "Vault mutation in progress.", err)
		}
		defer release()

		// Re-load fresh under the lock and re-validate: a concurrent
		// mutator may have changed the scope while the prompt/password
		// verification was in progress. Do not blindly wipe a scope that
		// was already emptied/changed by someone else without telling the
		// user.
		dek, vd, err := loadUnlockedVaultForDelete(opts)
		if err != nil {
			return newCLIError(exitCodeIOFailed, "Failed to load vault.", err)
		}
		scope = deleteAllScope(vd, opts, deleteOpts)
		if len(scope) == 0 {
			return errors.New("delete aborted: scope was emptied concurrently since confirmation, please retry")
		}
		if deleteOpts.shared {
			vd.Shared = map[string]string{}
		} else {
			delete(vd.Profiles[opts.profile], opts.path)
		}
		if err := saveVault(opts.dataDir, dek, vd); err != nil {
			return newCLIError(exitCodeIOFailed, "Failed to save vault.", err)
		}
		_, _ = fmt.Fprintln(stdout, "deleted all")
		return nil
	}

	key := deleteOpts.key
	if err := validateEnvKey(key); err != nil {
		return err
	}
	if err := checkDeleteKeyExists(previewVD, opts, deleteOpts, key); err != nil {
		return err
	}
	if !deleteOpts.autoYes {
		ok, err := confirmPrompt(stdin, stderr, fmt.Sprintf("Delete key %q? [y/N]: ", key))
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return nil
		}
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Vault mutation in progress.", err)
	}
	defer release()

	// Re-load fresh under the lock and re-check the key still exists in
	// the expected scope before deleting it: if it was deleted by a
	// concurrent process in the meantime, fail clearly rather than
	// silently succeeding or silently no-op'ing.
	dek, vd, err := loadUnlockedVaultForDelete(opts)
	if err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to load vault.", err)
	}
	if err := checkDeleteKeyExists(vd, opts, deleteOpts, key); err != nil {
		return fmt.Errorf("key no longer exists (deleted concurrently), delete aborted: %w", err)
	}
	if deleteOpts.shared {
		delete(vd.Shared, key)
	} else {
		delete(vd.Profiles[opts.profile][opts.path], key)
	}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to save vault.", err)
	}
	_, _ = fmt.Fprintln(stdout, "deleted")
	return nil
}

func loadUnlockedVaultForDelete(opts globalOptions) ([]byte, *vaultData, error) {
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return nil, nil, err
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return nil, nil, err
	}
	return dek, vd, nil
}

func deleteAllScope(vd *vaultData, opts globalOptions, deleteOpts deleteOptions) map[string]string {
	if deleteOpts.shared {
		return vd.Shared
	}
	if vd.Profiles[opts.profile] != nil && vd.Profiles[opts.profile][opts.path] != nil {
		return vd.Profiles[opts.profile][opts.path]
	}
	return map[string]string{}
}

func checkDeleteKeyExists(vd *vaultData, opts globalOptions, deleteOpts deleteOptions, key string) error {
	if deleteOpts.shared {
		if vd.Shared == nil {
			return errors.New("secret not found")
		}
		if _, ok := vd.Shared[key]; !ok {
			return errors.New("secret not found")
		}
		return nil
	}
	if vd.Profiles[opts.profile] == nil || vd.Profiles[opts.profile][opts.path] == nil {
		if _, ok := vd.Shared[key]; ok {
			return fmt.Errorf("secret not found in current profile/path scope; key %q exists in shared scope (use --shared)", key)
		}
		return errors.New("secret not found")
	}
	if _, ok := vd.Profiles[opts.profile][opts.path][key]; !ok {
		if _, ok := vd.Shared[key]; ok {
			return fmt.Errorf("secret not found in current profile/path scope; key %q exists in shared scope (use --shared)", key)
		}
		return errors.New("secret not found")
	}
	return nil
}

func readTrimmedLine(r io.Reader) (string, error) {
	v, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(v), nil
}
