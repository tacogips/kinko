package kinko

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"
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
	key, value, shared, err := parseSetKeyArgs(args)
	if err != nil {
		return err
	}
	if strings.Contains(key, "=") {
		return errors.New("set-key expects key only (without '=')")
	}
	if err := validateEnvKey(key); err != nil {
		return err
	}
	if value == "" {
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

func parseSetKeyArgs(args []string) (string, string, bool, error) {
	key := ""
	value := ""
	shared := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--shared":
			shared = true
		case a == "--value":
			if i+1 >= len(args) {
				return "", "", false, errors.New("set-key requires value for --value")
			}
			value = args[i+1]
			i++
		case strings.HasPrefix(a, "--value="):
			value = strings.TrimPrefix(a, "--value=")
		case strings.HasPrefix(a, "-"):
			return "", "", false, fmt.Errorf("set-key: unknown flag %q", a)
		default:
			if key != "" {
				return "", "", false, errors.New("set-key requires a key")
			}
			key = a
		}
	}
	if key == "" {
		return "", "", false, errors.New("set-key requires a key")
	}
	return key, value, shared, nil
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
			ok, err := confirmPrompt(stdin, stderr, msg)
			if err != nil {
				return err
			}
			if !ok {
				_, _ = fmt.Fprintln(stdout, "aborted")
				return nil
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

func runExplosion(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	reader := bufio.NewReader(stdin)

	_, _ = fmt.Fprintln(stderr, "DANGER: This will permanently delete all vault data in the current data dir.")
	_, _ = fmt.Fprintln(stderr, "All registered data will be lost and this action cannot be undone.")
	_, _ = fmt.Fprintln(stderr, "Password re-entry is required for this operation.")

	if err := verifyExplosionPassword(opts, reader, stderr); err != nil {
		return err
	}
	if err := validateExplosionTarget(opts.dataDir); err != nil {
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
	allowedBases := []string{
		filepath.Clean(filepath.Join(home, ".local")),
		filepath.Clean(os.TempDir()),
	}
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
	allowedRoot := map[string]bool{"vault": true, "lock": true}
	for _, entry := range rootEntries {
		if !allowedRoot[entry.Name()] {
			return fmt.Errorf("refusing explosion: unexpected entry in data dir: %s", filepath.Join(dataDir, entry.Name()))
		}
	}
	for _, mustDir := range []string{"vault", "lock"} {
		info, err := os.Stat(filepath.Join(dataDir, mustDir))
		if err != nil {
			return fmt.Errorf("refusing explosion: missing required dir %s", filepath.Join(dataDir, mustDir))
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing explosion: %s must be a directory", filepath.Join(dataDir, mustDir))
		}
	}

	vaultEntries, err := os.ReadDir(filepath.Join(dataDir, "vault"))
	if err != nil {
		return fmt.Errorf("read vault dir: %w", err)
	}
	allowedVault := map[string]bool{
		"meta.v1.json":  true,
		"vault.v1.bin":  true,
		"config.v1.bin": true,
		vaultMarker:     true,
	}
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
	allowedLock := map[string]bool{
		"session.token": true,
	}
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
	files := []string{
		filepath.Join(dataDir, "vault", "meta.v1.json"),
		filepath.Join(dataDir, "vault", "vault.v1.bin"),
		filepath.Join(dataDir, "vault", "config.v1.bin"),
		filepath.Join(dataDir, "vault", vaultMarker),
		filepath.Join(dataDir, "lock", "session.token"),
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", p, err)
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
	return []string{
		filepath.Clean(string(filepath.Separator)),
		filepath.Clean(home),
		filepath.Clean(filepath.Dir(home)),
		filepath.Clean(os.TempDir()),
		"/bin",
		"/boot",
		"/dev",
		"/etc",
		"/home",
		"/lib",
		"/lib64",
		"/media",
		"/mnt",
		"/opt",
		"/proc",
		"/root",
		"/run",
		"/sbin",
		"/srv",
		"/sys",
		"/usr",
		"/var",
	}
}

func explosionConfirmationToken(dataDir string) string {
	sum := sha256.Sum256([]byte("kinko.explosion.v1:" + filepath.Clean(dataDir)))
	return strings.ToUpper(hex.EncodeToString(sum[:6]))
}

func verifyExplosionPassword(opts globalOptions, reader *bufio.Reader, stderr io.Writer) error {
	if _, err := fmt.Fprint(stderr, "Re-enter password: "); err != nil {
		return err
	}
	password, err := readSecretFromBuffered(reader)
	if err != nil {
		return err
	}

	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return fmt.Errorf("cannot verify password: %w", err)
	}
	if _, err := unwrapDEKWithPassword(meta, password); err != nil {
		return errors.New("password verification failed")
	}
	return nil
}

func runGet(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	key, reveal, force, err := parseGetArgs(args)
	if err != nil {
		return err
	}
	opts.force = opts.force || force
	v, ok, err := getSecret(opts, key)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("secret not found")
	}

	if reveal {
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
	reveal := false
	allScopes := false
	fs.BoolVar(&reveal, "reveal", false, "show plaintext")
	fs.BoolVar(&allScopes, "all-scopes", false, "show shared and all path scopes in current profile (ignores --path)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if allScopes {
		return runShowAllScopes(opts, stdin, stdout, stderr, reveal)
	}

	shared, repoSpecific, err := showSecretScopes(opts)
	if err != nil {
		return err
	}
	if reveal {
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
			if !reveal {
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
			if !reveal {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, v)
		}
	}
	return nil
}

func runShowAllScopes(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, reveal bool) error {
	shared, pathsByScope, err := showAllSecretScopes(opts)
	if err != nil {
		return err
	}
	if reveal {
		if err := guardSensitiveOutput(opts, stdin, stdout, stderr, "reveal all scopes"); err != nil {
			return err
		}
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

func runExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := guardSensitiveOutput(opts, stdin, stdout, stderr, "export secrets"); err != nil {
		return err
	}
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	withScopeComments := true
	var rawExcludeKeys stringListFlag
	fs.BoolVar(&withScopeComments, "with-scope-comments", true, "include # kinko:scope markers in export output")
	fs.Var(&rawExcludeKeys, "exclude", "comma-separated key denylist to omit from export output (repeatable)")

	shellArg := shellPosix
	parseArgs := args
	if len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		shellArg = parseArgs[0]
		parseArgs = parseArgs[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("export accepts at most one shell argument")
	}
	if fs.NArg() == 1 {
		if shellArg != shellPosix {
			return errors.New("export accepts at most one shell argument")
		}
		shellArg = fs.Arg(0)
	}

	shell, err := normalizeShell(shellArg)
	if err != nil {
		return err
	}
	shared, repoSpecific, err := showSecretScopes(opts)
	if err != nil {
		return err
	}
	excluded, err := parseExcludedKeys(rawExcludeKeys)
	if err != nil {
		return err
	}
	if len(excluded) > 0 {
		shared = filterSecretsByExclusion(shared, excluded)
		repoSpecific = filterSecretsByExclusion(repoSpecific, excluded)
	}
	if err := writeExportBlock(stdout, shell, "shared", "shared keys", shared, withScopeComments); err != nil {
		return err
	}
	repoTitle := fmt.Sprintf("repository-specific keys (profile=%s path=%s)", opts.profile, opts.path)
	if err := writeExportBlock(stdout, shell, "repo", repoTitle, repoSpecific, withScopeComments); err != nil {
		return err
	}
	return nil
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func parseExcludedKeys(raw []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, value := range raw {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			key := strings.TrimSpace(part)
			if key == "" {
				continue
			}
			if err := validateEnvKey(key); err != nil {
				return nil, fmt.Errorf("invalid --exclude key %q: %w", key, err)
			}
			out[key] = struct{}{}
		}
	}
	return out, nil
}

func filterSecretsByExclusion(in map[string]string, excluded map[string]struct{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if _, skip := excluded[k]; skip {
			continue
		}
		out[k] = v
	}
	return out
}

func runImport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filePath := ""
	autoYes := false
	confirmWithValues := false
	allowShared := true
	fs.StringVar(&filePath, "file", "", "import source file path")
	fs.BoolVar(&autoYes, "yes", false, "skip import confirmation flow")
	fs.BoolVar(&autoYes, "y", false, "skip import confirmation flow")
	fs.BoolVar(&confirmWithValues, "confirm-with-values", false, "show values in confirmation output")
	fs.BoolVar(&allowShared, "allow-shared", true, "allow # kinko:scope=shared blocks in import input")

	shellArg := shellPosix
	parseArgs := args
	if len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		shellArg = parseArgs[0]
		parseArgs = parseArgs[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("import accepts at most one shell argument")
	}
	if fs.NArg() == 1 {
		if shellArg != shellPosix {
			return errors.New("import accepts at most one shell argument")
		}
		shellArg = fs.Arg(0)
	}

	shell, err := normalizeShell(shellArg)
	if err != nil {
		return err
	}

	stdinIsTTY := isTerminalReader(stdin)
	var content []byte
	if filePath != "" {
		content, err = os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read --file: %w", err)
		}
	} else {
		if stdinIsTTY {
			return errors.New("import requires --file or stdin pipe input")
		}
		content, err = io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	}

	parsed, err := parseImportScopes(shell, string(content), allowShared)
	if err != nil {
		return err
	}
	sharedKeys := sortedKeys(parsed.shared)
	repoKeys := sortedKeys(parsed.repoSpecific)
	totalAssignments := len(sharedKeys) + len(repoKeys)
	if totalAssignments == 0 {
		return errors.New("import input contains no assignments")
	}

	stderrIsTTY := isTerminalWriter(stderr)
	if confirmWithValues && !autoYes && !stderrIsTTY && !opts.force {
		return errors.New("sensitive output blocked for non-tty/redirection (use --force)")
	}
	if !autoYes {
		if shouldPromptImportValueDisclosure(confirmWithValues, autoYes, stderrIsTTY) {
			ok, err := confirmPromptTTYAware(stdin, stderr, "Show values in confirmation summary? [y/N]: ")
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted")
			}
		}
		renderImportSummary(stderr, shell, opts.profile, opts.path, sharedKeys, repoKeys, parsed.shared, parsed.repoSpecific, confirmWithValues)
		if shouldPromptImportMutation(autoYes) {
			ok, err := confirmPromptTTYAware(stdin, stderr, fmt.Sprintf("Import %d assignments (shared=%d, repository-specific=%d) into profile=%q path=%q? [y/N]: ", totalAssignments, len(sharedKeys), len(repoKeys), opts.profile, opts.path))
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted")
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
	if len(repoKeys) > 0 && vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	if len(repoKeys) > 0 && vd.Profiles[opts.profile][opts.path] == nil {
		vd.Profiles[opts.profile][opts.path] = map[string]string{}
	}
	if vd.Shared == nil {
		vd.Shared = map[string]string{}
	}
	for _, k := range sharedKeys {
		vd.Shared[k] = parsed.shared[k]
	}
	for _, k := range repoKeys {
		vd.Profiles[opts.profile][opts.path][k] = parsed.repoSpecific[k]
	}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "imported %d keys\n", totalAssignments)
	return nil
}

func renderImportSummary(w io.Writer, shell, profile, path string, sharedKeys, repoKeys []string, shared, repoSpecific map[string]string, withValues bool) {
	_, _ = fmt.Fprintln(w, "Planned import:")
	_, _ = fmt.Fprintf(w, "  shell: %s\n", shell)
	_, _ = fmt.Fprintf(w, "  profile: %s\n", profile)
	_, _ = fmt.Fprintf(w, "  path: %s\n", path)
	_, _ = fmt.Fprintf(w, "  shared keys (%d): %s\n", len(sharedKeys), strings.Join(sharedKeys, ", "))
	_, _ = fmt.Fprintf(w, "  repository-specific keys (%d): %s\n", len(repoKeys), strings.Join(repoKeys, ", "))
	if withValues {
		for _, k := range sharedKeys {
			_, _ = fmt.Fprintf(w, "  [shared] %s=%s\n", k, shared[k])
		}
		for _, k := range repoKeys {
			_, _ = fmt.Fprintf(w, "  [repository] %s=%s\n", k, repoSpecific[k])
		}
	}
}

type importScopes struct {
	shared       map[string]string
	repoSpecific map[string]string
}

func (s importScopes) merged() map[string]string {
	out := map[string]string{}
	for k, v := range s.shared {
		out[k] = v
	}
	for k, v := range s.repoSpecific {
		out[k] = v
	}
	return out
}

func parseImportScopes(shell, content string, allowShared bool) (importScopes, error) {
	out := importScopes{
		shared:       map[string]string{},
		repoSpecific: map[string]string{},
	}
	currentScope := "repo"
	lines := strings.Split(content, "\n")
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			marker := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "#")))
			switch marker {
			case "kinko:scope=shared":
				if !allowShared {
					return importScopes{}, fmt.Errorf("import parse error (shell=%s, line=%d): shared scope markers require --allow-shared", shell, lineNo)
				}
				currentScope = "shared"
			case "kinko:scope=repo":
				currentScope = "repo"
			default:
				if strings.HasPrefix(marker, "kinko:scope=") {
					return importScopes{}, fmt.Errorf("import parse error (shell=%s, line=%d): invalid scope marker", shell, lineNo)
				}
			}
			continue
		}
		key, value, reason := parseImportLine(shell, line)
		if reason != "" {
			return importScopes{}, fmt.Errorf("import parse error (shell=%s, line=%d): %s", shell, lineNo, reason)
		}
		if currentScope == "shared" {
			out.shared[key] = value
			continue
		}
		out.repoSpecific[key] = value
	}
	return out, nil
}

func parseImportAssignments(shell, content string) (map[string]string, error) {
	scopes, err := parseImportScopes(shell, content, true)
	if err != nil {
		return nil, err
	}
	return scopes.merged(), nil
}

func shouldPromptImportValueDisclosure(confirmWithValues, autoYes, stderrIsTTY bool) bool {
	return confirmWithValues && !autoYes && stderrIsTTY
}

func shouldPromptImportMutation(autoYes bool) bool {
	return !autoYes
}

func parseImportLine(shell, line string) (string, string, string) {
	switch shell {
	case shellPosix:
		return parseImportPosixLine(line)
	case shellFish:
		return parseImportFishLine(line)
	case shellNu:
		return parseImportNuLine(line)
	default:
		return "", "", "unsupported shell parser"
	}
}

func parseImportPosixLine(line string) (string, string, string) {
	body := strings.TrimSpace(line)
	if strings.HasPrefix(body, "export") {
		if len(body) == len("export") || (len(body) > len("export") && isASCIISpace(body[len("export")])) {
			body = strings.TrimSpace(body[len("export"):])
		}
	}
	eq := strings.Index(body, "=")
	if eq <= 0 {
		return "", "", posixImportAssignmentFormatError()
	}
	key := strings.TrimSpace(body[:eq])
	if err := validateEnvKey(key); err != nil {
		return "", "", "invalid key syntax"
	}
	valueExpr := strings.TrimSpace(body[eq+1:])
	value, reason := parsePosixImportValue(valueExpr)
	if reason != "" {
		return "", "", reason
	}
	return key, value, ""
}

func parsePosixImportValue(raw string) (string, string) {
	if raw == "" {
		return "", ""
	}
	if raw[0] == '\'' {
		return parsePosixSingleQuotedImportValue(raw)
	}
	if raw[0] == '"' {
		return parsePosixDoubleQuotedImportValue(raw)
	}
	if strings.ContainsAny(raw, " \t") {
		return "", posixImportAssignmentFormatError()
	}
	return raw, ""
}

func parsePosixSingleQuotedImportValue(raw string) (string, string) {
	if raw == "" {
		return "", "unterminated quoted value"
	}
	var b strings.Builder
	i := 0
	for {
		if i >= len(raw) || raw[i] != '\'' {
			return "", posixImportAssignmentFormatError()
		}
		i++
		segStart := i
		for i < len(raw) && raw[i] != '\'' {
			i++
		}
		if i >= len(raw) {
			return "", "unterminated quoted value"
		}
		b.WriteString(raw[segStart:i])
		i++
		if i == len(raw) {
			break
		}
		if !strings.HasPrefix(raw[i:], "\"'\"") {
			return "", "invalid single-quote sequence"
		}
		b.WriteByte('\'')
		i += len("\"'\"")
	}
	return b.String(), ""
}

func parsePosixDoubleQuotedImportValue(raw string) (string, string) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", "unterminated quoted value"
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(inner) {
			b.WriteByte('\\')
			continue
		}
		i++
		switch inner[i] {
		case '\\', '"', '$', '`':
			b.WriteByte(inner[i])
		case '\n':
			// shell line continuation inside double quotes
		default:
			// Keep unknown escapes verbatim.
			b.WriteByte('\\')
			b.WriteByte(inner[i])
		}
	}
	return b.String(), ""
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func posixImportAssignmentFormatError() string {
	return "unsupported assignment format (expected export KEY=value, export KEY='value', export KEY=\"value\", or KEY=value)"
}

func parseImportFishLine(line string) (string, string, string) {
	if !strings.HasPrefix(line, "set -gx ") {
		return "", "", "unsupported assignment format"
	}
	if !strings.HasSuffix(line, ";") {
		return "", "", "unsupported assignment format"
	}
	body := strings.TrimSpace(strings.TrimSuffix(line, ";"))
	rest := strings.TrimSpace(strings.TrimPrefix(body, "set -gx "))
	if rest == "" {
		return "", "", "unsupported assignment format"
	}
	sep := strings.IndexAny(rest, " \t")
	if sep <= 0 {
		return "", "", "unsupported assignment format"
	}
	key := rest[:sep]
	if err := validateEnvKey(key); err != nil {
		return "", "", "invalid key syntax"
	}
	valueExpr := strings.TrimSpace(rest[sep+1:])
	value, reason := parseFishQuotedImportValue(valueExpr)
	if reason != "" {
		return "", "", reason
	}
	return key, value, ""
}

func parseFishQuotedImportValue(raw string) (string, string) {
	if len(raw) < 2 || raw[0] != '\'' || raw[len(raw)-1] != '\'' {
		return "", "unterminated quoted value"
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' {
			if i+1 < len(inner) && inner[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			b.WriteByte(c)
			continue
		}
		if c == '\'' {
			return "", "unsupported assignment format"
		}
		b.WriteByte(c)
	}
	return b.String(), ""
}

func parseImportNuLine(line string) (string, string, string) {
	if !strings.HasPrefix(line, "$env.") {
		return "", "", "unsupported assignment format"
	}
	body := strings.TrimPrefix(line, "$env.")
	eq := strings.Index(body, "=")
	if eq <= 0 {
		return "", "", "unsupported assignment format"
	}
	key := strings.TrimSpace(body[:eq])
	if err := validateEnvKey(key); err != nil {
		return "", "", "invalid key syntax"
	}
	valueExpr := strings.TrimSpace(body[eq+1:])
	value, reason := parseNuQuotedImportValue(valueExpr)
	if reason != "" {
		return "", "", reason
	}
	return key, value, ""
}

func parseNuQuotedImportValue(raw string) (string, string) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", "unterminated quoted value"
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '"' {
			return "", "unsupported assignment format"
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(inner) {
			return "", "invalid escape sequence"
		}
		i++
		switch inner[i] {
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			return "", "invalid escape sequence"
		}
	}
	return b.String(), ""
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
			return fmt.Errorf("vault mutation in progress: %w", err)
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

func runExec(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	includeAll := false
	envList := ""
	fs.BoolVar(&includeAll, "all", false, "inject all secrets into child environment")
	fs.StringVar(&envList, "env", "", "comma-separated key allowlist to inject into child environment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		return errors.New("exec requires command after flags")
	}
	m, err := showSecrets(opts)
	if err != nil {
		return err
	}
	selected, err := selectExecSecrets(m, includeAll, envList)
	if err != nil {
		return err
	}

	env := os.Environ()
	for k, v := range selected {
		if err := validateEnvKey(k); err != nil {
			return err
		}
		env = append(env, k+"="+v)
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func selectExecSecrets(secrets map[string]string, includeAll bool, envList string) (map[string]string, error) {
	if includeAll && strings.TrimSpace(envList) != "" {
		return nil, errors.New("exec flags --all and --env cannot be combined")
	}
	if !includeAll && strings.TrimSpace(envList) == "" {
		return nil, errors.New("exec requires secret selection: use --all or --env KEY[,KEY...]")
	}
	if includeAll {
		out := make(map[string]string, len(secrets))
		for k, v := range secrets {
			out[k] = v
		}
		return out, nil
	}

	out := map[string]string{}
	parts := strings.Split(envList, ",")
	for _, part := range parts {
		k := strings.TrimSpace(part)
		if k == "" {
			continue
		}
		if err := validateEnvKey(k); err != nil {
			return nil, err
		}
		v, ok := secrets[k]
		if !ok {
			return nil, fmt.Errorf("secret not found for --env key %q", k)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, errors.New("exec --env resolved to no keys")
	}
	return out, nil
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

func writeExportBlock(w io.Writer, shell, scope, title string, secrets map[string]string, withScopeComments bool) error {
	if len(secrets) == 0 {
		return nil
	}
	if withScopeComments {
		_, _ = fmt.Fprintf(w, "%s kinko:scope=%s\n", shellCommentPrefix(shell), scope)
		_, _ = fmt.Fprintf(w, "%s %s\n", shellCommentPrefix(shell), title)
	}
	for _, k := range sortedKeys(secrets) {
		line, err := renderShellAssignment(shell, k, secrets[k])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, line)
	}
	return nil
}

func shellCommentPrefix(shell string) string {
	switch shell {
	case shellPosix, shellFish, shellNu:
		return "#"
	default:
		return "#"
	}
}

func normalizeShell(shell string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case shellPosix, shellSh, shellBash, shellZsh:
		return shellPosix, nil
	case shellFish:
		return shellFish, nil
	case shellNu, shellNushell:
		return shellNu, nil
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

func renderShellAssignment(shell, key, value string) (string, error) {
	if err := validateEnvKey(key); err != nil {
		return "", err
	}
	switch shell {
	case shellPosix:
		return fmt.Sprintf("export %s=%s", key, quotePosix(value)), nil
	case shellFish:
		return fmt.Sprintf("set -gx %s %s;", key, quoteFish(value)), nil
	case shellNu:
		return fmt.Sprintf("$env.%s = %s", key, quoteNu(value)), nil
	default:
		return "", fmt.Errorf("unsupported normalized shell %q", shell)
	}
}

func quotePosix(v string) string {
	if v == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
}

func quoteFish(v string) string {
	if v == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(v, "'", "\\'") + "'"
}

func quoteNu(v string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return "\"" + replacer.Replace(v) + "\""
}

func validateEnvKey(key string) error {
	if key == "" {
		return errors.New("environment key must not be empty")
	}
	for i, r := range key {
		if i == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
				return fmt.Errorf("invalid environment key %q", key)
			}
			continue
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return fmt.Errorf("invalid environment key %q", key)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func maskValue(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:2] + strings.Repeat("*", len(v)-4) + v[len(v)-2:]
}

func guardSensitiveOutput(opts globalOptions, stdin io.Reader, stdout, stderr io.Writer, action string) error {
	if !isTerminalWriter(stdout) && !opts.force {
		return errors.New("sensitive output blocked for non-tty/redirection (use --force)")
	}
	if isTerminalWriter(stdout) && opts.confirm {
		ok, err := confirmPrompt(stdin, stderr, "Confirm "+action+"? [y/N]: ")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted")
		}
	}
	return nil
}

func guardSensitiveStderr(opts globalOptions, stdin io.Reader, stderr io.Writer, action string) error {
	if !isTerminalWriter(stderr) && !opts.force {
		return errors.New("sensitive output blocked for non-tty/redirection (use --force)")
	}
	if isTerminalWriter(stderr) && opts.confirm {
		ok, err := confirmPrompt(stdin, stderr, "Confirm "+action+"? [y/N]: ")
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

func readLine(r io.Reader) (string, error) {
	v, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(v), nil
}
