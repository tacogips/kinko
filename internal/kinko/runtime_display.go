package kinko

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"
)

func verifyVaultPasswordForBulkDelete(opts globalOptions, input passwordVerificationInput, stderr io.Writer) error {
	return verifyVaultPasswordFromInput(opts, input, stderr, "Re-enter password: ")
}

type passwordVerificationInput struct {
	secretInput       io.Reader
	confirmationInput io.Reader
	terminalSecret    bool
}

func passwordVerificationInputFor(stdin io.Reader, isTerminal func(io.Reader) bool) passwordVerificationInput {
	if isTerminal(stdin) {
		return passwordVerificationInput{secretInput: stdin, confirmationInput: stdin, terminalSecret: true}
	}
	reader := bufio.NewReader(stdin)
	return passwordVerificationInput{secretInput: reader, confirmationInput: reader}
}

func verifyVaultPasswordForShow(opts globalOptions, stdin io.Reader, stderr io.Writer, prompt string) (io.Reader, error) {
	input := passwordVerificationInputFor(stdin, isTerminalReader)
	if _, err := verifyVaultPasswordDEKFromInput(opts, input, stderr, prompt); err != nil {
		return nil, err
	}
	return input.confirmationInput, nil
}

func verifyVaultPasswordDEKForShow(opts globalOptions, stdin io.Reader, stderr io.Writer, prompt string) ([]byte, io.Reader, error) {
	input := passwordVerificationInputFor(stdin, isTerminalReader)
	dek, err := verifyVaultPasswordDEKFromInput(opts, input, stderr, prompt)
	if err != nil {
		return nil, nil, err
	}
	return dek, input.confirmationInput, nil
}

func verifyVaultPasswordFromInput(opts globalOptions, input passwordVerificationInput, stderr io.Writer, prompt string) error {
	_, err := verifyVaultPasswordDEKFromInput(opts, input, stderr, prompt)
	return err
}

func verifyVaultPasswordDEKFromInput(opts globalOptions, input passwordVerificationInput, stderr io.Writer, prompt string) ([]byte, error) {
	var password string
	var err error
	if input.terminalSecret {
		password, err = readSecret(input.secretInput, stderr, prompt)
	} else {
		password, err = readSecretWithPromptBuffered(input.secretInput.(*bufio.Reader), stderr, prompt)
	}
	if err != nil {
		return nil, err
	}
	return verifyVaultPasswordValue(opts, password)
}

func verifyVaultPasswordValue(opts globalOptions, password string) ([]byte, error) {
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return nil, fmt.Errorf("cannot verify password: %w", err)
	}
	dek, err := unwrapDEKWithPassword(meta, password)
	if err != nil {
		if errors.Is(err, errDecryptFailed) {
			return nil, newCLIError(exitCodeAuthFailed, "password verification failed", err)
		}
		return nil, newCLIError(exitCodeMetadataInvalid, "vault password metadata is invalid", err)
	}
	return dek, nil
}

func runGet(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	key, reveal, force, err := parseGetArgs(args)
	if err != nil {
		return err
	}
	return runGetWithOptions(opts, getOptions{key: key, reveal: reveal, force: force}, stdin, stdout, stderr)
}

type getOptions struct {
	key    string
	reveal bool
	force  bool
}

func runGetWithOptions(opts globalOptions, getOpts getOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	opts.force = opts.force || getOpts.force
	v, ok, err := getSecret(opts, getOpts.key)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("secret not found")
	}
	if getOpts.reveal {
		if err := guardSensitiveOutput(opts, stdin, stdout, stderr, "reveal secret"); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, v)
		return nil
	}
	_, _ = fmt.Fprintln(stdout, maskValue(v))
	return nil
}

func parseGetArgs(args []string) (string, bool, bool, error) {
	key := ""
	reveal := false
	force := false
	for _, a := range args {
		switch {
		case a == "--reveal":
			reveal = true
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "--reveal="):
			v, err := strconv.ParseBool(strings.TrimPrefix(a, "--reveal="))
			if err != nil {
				return "", false, false, fmt.Errorf("invalid --reveal value %q", strings.TrimPrefix(a, "--reveal="))
			}
			reveal = v
		case strings.HasPrefix(a, "--force="):
			v, err := strconv.ParseBool(strings.TrimPrefix(a, "--force="))
			if err != nil {
				return "", false, false, fmt.Errorf("invalid --force value %q", strings.TrimPrefix(a, "--force="))
			}
			force = v
		case strings.HasPrefix(a, "-"):
			return "", false, false, fmt.Errorf("get: unknown flag %q", a)
		default:
			if key != "" {
				return "", false, false, errors.New("get requires a key")
			}
			key = a
		}
	}
	if key == "" {
		return "", false, false, errors.New("get requires a key")
	}
	return key, reveal, force, nil
}

func runShow(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showOpts := showOptions{}
	fs.BoolVar(&showOpts.reveal, "reveal", false, "show plaintext")
	fs.BoolVar(&showOpts.allScopes, "all-scopes", false, "show shared and all path scopes in current profile (ignores --path)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runShowWithOptions(opts, showOpts, stdin, stdout, stderr)
}

type showOptions struct {
	reveal    bool
	allScopes bool
}

func runShowWithOptions(opts globalOptions, showOpts showOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if showOpts.allScopes {
		return runShowAllScopes(opts, stdin, stdout, stderr, showOpts.reveal)
	}
	shared, repoSpecific, err := showSecretScopes(opts)
	if err != nil {
		return err
	}
	if showOpts.reveal {
		if err := guardSensitiveOutput(opts, stdin, stdout, stderr, "reveal all secrets"); err != nil {
			return err
		}
	}
	if len(shared) == 0 && len(repoSpecific) == 0 {
		return nil
	}
	if len(shared) > 0 {
		_, _ = fmt.Fprintln(stdout, "# shared")
		for _, k := range sortedKeys(shared) {
			v := shared[k]
			if !showOpts.reveal {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, v)
		}
	}
	if len(repoSpecific) > 0 {
		if len(shared) > 0 {
			_, _ = fmt.Fprintln(stdout)
		}
		_, _ = fmt.Fprintf(stdout, "# path=%s\n", opts.path)
		for _, k := range sortedKeys(repoSpecific) {
			v := repoSpecific[k]
			if !showOpts.reveal {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, v)
		}
	}
	return nil
}

func runShowAllScopes(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, reveal bool) error {
	dek, confirmationInput, err := verifyVaultPasswordDEKForShow(opts, stdin, stderr, "Re-enter password: ")
	if err != nil {
		return err
	}
	if reveal {
		if err := guardSensitiveOutput(opts, confirmationInput, stdout, stderr, "reveal all scopes"); err != nil {
			return err
		}
	}
	shared, pathsByScope, err := showAllSecretScopesWithDEK(opts, dek)
	if err != nil {
		return err
	}
	normalizedPathsByScope, err := normalizePathScopes(pathsByScope)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "# profile=%s\n\n", opts.profile)
	_, _ = fmt.Fprintln(stdout, "# shared")
	for _, k := range sortedKeys(shared) {
		v := shared[k]
		if !reveal {
			v = maskValue(v)
		}
		_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, v)
	}
	pathScopes := make([]string, 0, len(normalizedPathsByScope))
	for p := range normalizedPathsByScope {
		pathScopes = append(pathScopes, p)
	}
	sort.Strings(pathScopes)
	for _, p := range pathScopes {
		scope := normalizedPathsByScope[p]
		if len(scope) == 0 {
			continue
		}
		_, _ = fmt.Fprintln(stdout)
		_, _ = fmt.Fprintf(stdout, "# path=%s\n", p)
		for _, k := range sortedKeys(scope) {
			v := scope[k]
			if !reveal {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, v)
		}
	}
	return nil
}

func normalizePathScopes(pathsByScope map[string]map[string]string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	rawPathByNormalizedPath := map[string]string{}
	rawPaths := make([]string, 0, len(pathsByScope))
	for rawPath := range pathsByScope {
		rawPaths = append(rawPaths, rawPath)
	}
	sort.Strings(rawPaths)
	for _, rawPath := range rawPaths {
		normalizedPath, err := normalizeStoredScopePath(rawPath)
		if err != nil {
			return nil, err
		}
		if existingRawPath, exists := rawPathByNormalizedPath[normalizedPath]; exists {
			return nil, fmt.Errorf("cannot render all scopes: stored paths %q and %q normalize to the same path %q", existingRawPath, rawPath, normalizedPath)
		}
		rawPathByNormalizedPath[normalizedPath] = rawPath
		if out[normalizedPath] == nil {
			out[normalizedPath] = map[string]string{}
		}
		for k, v := range pathsByScope[rawPath] {
			out[normalizedPath][k] = v
		}
	}
	return out, nil
}

func normalizeStoredScopePath(path string) (string, error) {
	p := normalizePathInput(path)
	if p == "" {
		return "", fmt.Errorf("cannot render all scopes: stored path %q is invalid", path)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("cannot render all scopes: stored path %q is relative; only normalized absolute paths are supported", path)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("cannot render all scopes: normalize stored path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

func maskValue(v string) string {
	return "********"
}

func guardSensitiveOutput(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, action string) error {
	if !isTerminalWriter(stdout) && !opts.force {
		return errors.New("sensitive output blocked for non-tty/redirection (use --force)")
	}
	if isTerminalWriter(stdout) && opts.confirm {
		ok, err := confirmPromptTTYAware(stdin, stderr, "Confirm "+action+"? [y/N]: ")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted")
		}
	}
	return nil
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
