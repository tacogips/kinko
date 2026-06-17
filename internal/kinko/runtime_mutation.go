package kinko

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func runSet(opts globalOptions, args []string, stdin io.Reader, stdout io.Writer) error {
	shared := false
	assignments := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--shared" {
			shared = true
			continue
		}
		if a == "--value" || strings.HasPrefix(a, "--value=") {
			return errors.New("set only accepts KEY=VALUE assignments; use set-key for --value mode")
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("set: unknown flag %q", a)
		}
		assignments = append(assignments, a)
	}
	keys := []string{}
	values := map[string]string{}
	if len(assignments) > 0 {
		for _, a := range assignments {
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
		return fmt.Errorf("vault mutation in progress: %w", err)
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
	if !shared && vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	if !shared && vd.Profiles[opts.profile][opts.path] == nil {
		vd.Profiles[opts.profile][opts.path] = map[string]string{}
	}
	for _, k := range keys {
		if shared {
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
	key, value, valueProvided, shared, err := parseSetKeyArgs(args)
	if err != nil {
		return err
	}
	if strings.Contains(key, "=") {
		return errors.New("set-key expects key only (without '=')")
	}
	if err := validateEnvKey(key); err != nil {
		return err
	}
	if !valueProvided {
		v, err := readLine(stdin)
		if err != nil {
			return err
		}
		if v == "" {
			return errors.New("set-key requires --value or stdin value")
		}
		value = v
	}
	setArgs := []string{key + "=" + value}
	if shared {
		setArgs = append([]string{"--shared"}, setArgs...)
	}
	return runSet(opts, setArgs, strings.NewReader(""), stdout)
}

func parseSetKeyArgs(args []string) (string, string, bool, bool, error) {
	key := ""
	value := ""
	valueProvided := false
	shared := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--shared":
			shared = true
		case a == "--value":
			if i+1 >= len(args) {
				return "", "", false, false, errors.New("set-key requires value for --value")
			}
			value = args[i+1]
			valueProvided = true
			i++
		case strings.HasPrefix(a, "--value="):
			value = strings.TrimPrefix(a, "--value=")
			valueProvided = true
		case strings.HasPrefix(a, "-"):
			return "", "", false, false, fmt.Errorf("set-key: unknown flag %q", a)
		default:
			if key != "" {
				return "", "", false, false, errors.New("set-key requires a key")
			}
			key = a
		}
	}
	if key == "" {
		return "", "", false, false, errors.New("set-key requires a key")
	}
	return key, value, valueProvided, shared, nil
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
		return nil, err
	}
	return out, nil
}

func runDelete(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	autoYes := false
	deleteAll := false
	shared := false
	fs.BoolVar(&autoYes, "yes", false, "auto confirm deletion")
	fs.BoolVar(&autoYes, "y", false, "auto confirm deletion")
	fs.BoolVar(&deleteAll, "all", false, "delete all keys in resolved profile/path scope")
	fs.BoolVar(&shared, "shared", false, "delete keys from shared scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if deleteAll && fs.NArg() > 0 {
		return errors.New("delete --all cannot be combined with a key")
	}
	if !deleteAll && fs.NArg() != 1 {
		return errors.New("delete requires a key or --all")
	}
	confirmationInput := stdin
	var passwordInput passwordVerificationInput
	if deleteAll {
		passwordInput = passwordVerificationInputFor(stdin, isTerminalReader)
		confirmationInput = passwordInput.confirmationInput
		if autoYes {
			if err := verifyVaultPasswordForBulkDelete(opts, passwordInput, stderr); err != nil {
				return err
			}
		}
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return fmt.Errorf("vault mutation in progress: %w", err)
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
	if deleteAll {
		scope := map[string]string{}
		if shared {
			scope = vd.Shared
		} else if vd.Profiles[opts.profile] != nil && vd.Profiles[opts.profile][opts.path] != nil {
			scope = vd.Profiles[opts.profile][opts.path]
		}
		if len(scope) == 0 {
			if shared {
				return errors.New("no secrets found in shared scope")
			}
			return errors.New("no secrets found in current profile/path scope")
		}
		if !autoYes {
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
			if shared {
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
				return err
			}
		}
		if shared {
			vd.Shared = map[string]string{}
		} else {
			delete(vd.Profiles[opts.profile], opts.path)
		}
		if err := saveVault(opts.dataDir, dek, vd); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "deleted all")
		return nil
	}
	key := fs.Arg(0)
	if err := validateEnvKey(key); err != nil {
		return err
	}
	if shared {
		if vd.Shared == nil {
			return errors.New("secret not found")
		}
		if _, ok := vd.Shared[key]; !ok {
			return errors.New("secret not found")
		}
	} else {
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
	}
	if !autoYes {
		ok, err := confirmPrompt(stdin, stderr, fmt.Sprintf("Delete key %q? [y/N]: ", key))
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return nil
		}
	}
	if shared {
		delete(vd.Shared, key)
	} else {
		delete(vd.Profiles[opts.profile][opts.path], key)
	}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "deleted")
	return nil
}

func readLine(r io.Reader) (string, error) {
	v, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(v), nil
}
