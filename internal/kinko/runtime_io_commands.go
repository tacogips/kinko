package kinko

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func runExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	exportOpts, err := parseExportOptions(args)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	return runExportWithOptions(opts, exportOpts, stdin, stdout, stderr)
}

type exportOptions struct {
	shell             string
	withScopeComments bool
	sharedOnly        bool
	excludeKeys       []string
}

func parseExportOptions(args []string) (exportOptions, error) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var rawExcludeKeys stringListFlag
	exportOpts := exportOptions{
		shell:             shellPosix,
		withScopeComments: true,
	}
	fs.BoolVar(&exportOpts.withScopeComments, "with-scope-comments", true, "include # kinko:scope markers in export output")
	fs.BoolVar(&exportOpts.sharedOnly, "shared-only", false, "export only shared scope keys")
	fs.Var(&rawExcludeKeys, "exclude", "comma-separated key denylist to omit from export output (repeatable)")
	parseArgs := args
	if len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		exportOpts.shell = parseArgs[0]
		parseArgs = parseArgs[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return exportOptions{}, err
	}
	if fs.NArg() > 1 {
		return exportOptions{}, errors.New("export accepts at most one shell argument")
	}
	if fs.NArg() == 1 {
		if exportOpts.shell != shellPosix {
			return exportOptions{}, errors.New("export accepts at most one shell argument")
		}
		exportOpts.shell = fs.Arg(0)
	}
	exportOpts.excludeKeys = append([]string{}, rawExcludeKeys...)
	return exportOpts, nil
}

func runExportWithOptions(opts globalOptions, exportOpts exportOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := guardSensitiveOutput(opts, stdin, stdout, stderr, "export secrets"); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	shell, err := normalizeShell(exportOpts.shell)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	shared, repoSpecific, err := showSecretScopes(opts)
	if err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	excluded, err := parseExcludedKeys(exportOpts.excludeKeys)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if len(excluded) > 0 {
		shared = filterSecretsByExclusion(shared, excluded)
		repoSpecific = filterSecretsByExclusion(repoSpecific, excluded)
	}
	if exportOpts.sharedOnly {
		repoSpecific = nil
	}
	if err := writeExportBlock(stdout, shell, "shared", "shared keys", shared, exportOpts.withScopeComments); err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	repoTitle := fmt.Sprintf("repository-specific keys (profile=%s path=%s)", opts.profile, opts.path)
	if err := writeExportBlock(stdout, shell, "repo", repoTitle, repoSpecific, exportOpts.withScopeComments); err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
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
	importOpts, err := parseImportOptions(args)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	return runImportWithOptions(opts, importOpts, stdin, stdout, stderr)
}

type importOptions struct {
	shell             string
	filePath          string
	autoYes           bool
	confirmWithValues bool
	allowShared       bool
}

func parseImportOptions(args []string) (importOptions, error) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	importOpts := importOptions{
		shell:       shellPosix,
		allowShared: true,
	}
	fs.StringVar(&importOpts.filePath, "file", "", "import source file path")
	fs.BoolVar(&importOpts.autoYes, "yes", false, "skip import confirmation flow")
	fs.BoolVar(&importOpts.autoYes, "y", false, "skip import confirmation flow")
	fs.BoolVar(&importOpts.confirmWithValues, "confirm-with-values", false, "show values in confirmation output")
	fs.BoolVar(&importOpts.allowShared, "allow-shared", true, "allow # kinko:scope=shared blocks in import input")
	parseArgs := args
	if len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		importOpts.shell = parseArgs[0]
		parseArgs = parseArgs[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return importOptions{}, err
	}
	if fs.NArg() > 1 {
		return importOptions{}, errors.New("import accepts at most one shell argument")
	}
	if fs.NArg() == 1 {
		if importOpts.shell != shellPosix {
			return importOptions{}, errors.New("import accepts at most one shell argument")
		}
		importOpts.shell = fs.Arg(0)
	}
	return importOpts, nil
}

func runImportWithOptions(opts globalOptions, importOpts importOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	shell, err := normalizeShell(importOpts.shell)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	stdinIsTTY := isTerminalReader(stdin)
	var content []byte
	if importOpts.filePath != "" {
		if !stdinIsTTY {
			if err := checkNoConflictingStdinPipe(stdin); err != nil {
				return err
			}
		}
		content, err = os.ReadFile(importOpts.filePath)
		if err != nil {
			wrapped := fmt.Errorf("read --file: %w", err)
			return newCLIError(exitCodeIOFailed, wrapped.Error(), wrapped)
		}
	} else {
		if stdinIsTTY {
			err := errors.New("import requires --file or stdin pipe input")
			return newCLIError(exitCodePolicyFailed, err.Error(), err)
		}
		content, err = io.ReadAll(stdin)
		if err != nil {
			wrapped := fmt.Errorf("read stdin: %w", err)
			return newCLIError(exitCodeIOFailed, wrapped.Error(), wrapped)
		}
	}
	parsed, err := parseImportScopes(shell, string(content), importOpts.allowShared)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	sharedKeys := sortedKeys(parsed.shared)
	repoKeys := sortedKeys(parsed.repoSpecific)
	totalAssignments := len(sharedKeys) + len(repoKeys)
	if totalAssignments == 0 {
		err := errors.New("import input contains no assignments")
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	stderrIsTTY := isTerminalWriter(stderr)
	if importOpts.confirmWithValues && !importOpts.autoYes && !stderrIsTTY && !opts.force {
		err := errors.New("sensitive output blocked for non-tty/redirection (use --force)")
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if !importOpts.autoYes {
		renderValues := importOpts.confirmWithValues
		if shouldPromptImportValueDisclosure(importOpts.confirmWithValues, importOpts.autoYes, stderrIsTTY) {
			ok, err := confirmPromptTTYAware(stdin, stderr, "Show values in confirmation summary? [y/N]: ")
			if err != nil {
				return newCLIError(exitCodePolicyFailed, err.Error(), err)
			}
			if !ok {
				renderValues = false
			}
		}
		renderImportSummary(stderr, shell, opts.profile, opts.path, sharedKeys, repoKeys, parsed.shared, parsed.repoSpecific, renderValues)
		if shouldPromptImportMutation(importOpts.autoYes) {
			ok, err := confirmPromptTTYAware(stdin, stderr, fmt.Sprintf("Import %d assignments (shared=%d, repository-specific=%d) into profile=%q path=%q? [y/N]: ", totalAssignments, len(sharedKeys), len(repoKeys), opts.profile, opts.path))
			if err != nil {
				return newCLIError(exitCodePolicyFailed, err.Error(), err)
			}
			if !ok {
				err := errors.New("aborted")
				return newCLIError(exitCodePolicyFailed, err.Error(), err)
			}
		}
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		wrapped := fmt.Errorf("vault mutation in progress: %w", err)
		return newCLIError(exitCodeLockConflict, wrapped.Error(), wrapped)
	}
	defer release()
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
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
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	_, _ = fmt.Fprintf(stdout, "imported %d keys\n", totalAssignments)
	return nil
}

// stdinConflictProbeBufSize bounds the single Read call used by
// checkNoConflictingStdinPipe to detect a conflicting --file-plus-piped-stdin
// invocation (Finding 3).
const stdinConflictProbeBufSize = 4096

// checkNoConflictingStdinPipe detects the mutually-exclusive combination of
// `--file` plus non-empty piped stdin without draining stdin unboundedly.
//
// The previous implementation called io.ReadAll(stdin) purely to see whether
// stdin also carried piped data. If a process inherits stdin as an open pipe
// that is never closed, io.ReadAll blocks forever waiting for EOF, and once
// data does start flowing it buffers an unbounded amount of it in memory.
//
// This replaces that with a SINGLE bounded Read call into a fixed-size
// buffer. Residual limitation: a single bounded Read call still blocks if
// stdin is an open pipe that is idle (no data yet, not closed) - it does not
// eliminate blocking for that specific case. It only prevents unbounded
// blocking/memory growth once data does start flowing or once EOF arrives:
// this turns "blocks forever AND buffers unboundedly once data flows" into
// "blocks only until the first data-or-EOF from this one Read call, bounded
// to stdinConflictProbeBufSize bytes read."
func checkNoConflictingStdinPipe(stdin io.Reader) error {
	buf := make([]byte, stdinConflictProbeBufSize)
	n, err := stdin.Read(buf)
	if err != nil && err != io.EOF {
		wrapped := fmt.Errorf("read stdin: %w", err)
		return newCLIError(exitCodeIOFailed, wrapped.Error(), wrapped)
	}
	// n == 0 (whether err is io.EOF, nil, or unset) means stdin is empty:
	// io.Reader's contract permits a zero-length read without error, and
	// EOF may arrive together with (or instead of) data on the same call.
	if n == 0 {
		return nil
	}
	for _, b := range buf[:n] {
		if !isASCIISpace(b) {
			err := errors.New("import accepts either --file or stdin pipe input, not both")
			return newCLIError(exitCodePolicyFailed, err.Error(), err)
		}
	}
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

// unterminatedQuotedValueReason is the parse-error reason string returned by
// the posix/fish quoted-value parsers when an opening quote has no matching
// closing quote within the text scanned so far. parseImportScopes treats this
// specific reason as a signal that more physical lines may complete the
// value (see Finding 1: multi-line quoted value round-trip), rather than an
// immediate hard failure. It is only surfaced to the caller as a final error
// once no further input lines remain.
const unterminatedQuotedValueReason = "unterminated quoted value"

// shellSupportsMultilineQuotedValues reports whether the given normalized
// shell's quoted-value export format (quotePosix/quoteFish) can emit literal
// embedded newlines inside a single quoted token, which requires the import
// parser to be able to rejoin physical lines when scanning for the closing
// quote. Nu escapes newlines as "\n" in its double-quoted output
// (quoteNu/parseNuQuotedImportValue) and therefore never needs this.
func shellSupportsMultilineQuotedValues(shell string) bool {
	return shell == shellPosix || shell == shellFish
}

func parseImportScopes(shell, content string, allowShared bool) (importScopes, error) {
	out := importScopes{shared: map[string]string{}, repoSpecific: map[string]string{}}
	currentScope := "repo"
	lines := strings.Split(content, "\n")
	multiline := shellSupportsMultilineQuotedValues(shell)
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		raw := lines[i]
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
		// assembled accumulates the logical assignment text, starting from
		// the current line and progressively growing by one physical line
		// (rejoined with a single "\n", matching what quotePosix/quoteFish
		// actually emit for embedded newlines) whenever the value parser
		// reports an unterminated quote and further input is available.
		assembled := line
		consumedTo := i
		var key, value, reason string
		for {
			key, value, reason = parseImportLine(shell, assembled)
			if reason != unterminatedQuotedValueReason || !multiline {
				break
			}
			if consumedTo+1 >= len(lines) {
				// No more input to draw from: this is a genuine unterminated
				// quoted value, reported against the original starting line.
				break
			}
			consumedTo++
			assembled = assembled + "\n" + lines[consumedTo]
		}
		if reason != "" {
			return importScopes{}, fmt.Errorf("import parse error (shell=%s, line=%d): %s", shell, lineNo, reason)
		}
		i = consumedTo
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

// posixSingleQuoteEscape is the literal token quotePosix emits in place of a
// single embedded ' character: '...'"'"'...' - closing the quoted segment,
// emitting an escaped literal quote via a double-quoted single-quote, then
// reopening the quoted segment.
const posixSingleQuoteEscape = "\"'\""

func parsePosixSingleQuotedImportValue(raw string) (string, string) {
	if raw == "" {
		return "", unterminatedQuotedValueReason
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
			return "", unterminatedQuotedValueReason
		}
		b.WriteString(raw[segStart:i])
		i++
		if i == len(raw) {
			break
		}
		// A run of remaining bytes shorter than the full escape sequence
		// might simply be a partial match because more input (a further
		// physical line) has not been joined in yet - treat that as
		// "unterminated" so the multi-line continuation logic in
		// parseImportScopes (Finding 1) can retry with another line before
		// deciding it is a genuine syntax error.
		if !strings.HasPrefix(raw[i:], posixSingleQuoteEscape) {
			if len(raw[i:]) < len(posixSingleQuoteEscape) && strings.HasPrefix(posixSingleQuoteEscape, raw[i:]) {
				return "", unterminatedQuotedValueReason
			}
			return "", "invalid single-quote sequence"
		}
		b.WriteByte('\'')
		i += len(posixSingleQuoteEscape)
	}
	return b.String(), ""
}

// parsePosixDoubleQuotedImportValue scans raw (which the caller guarantees
// starts with '"') byte-by-byte with escape awareness, looking for the
// unescaped '"' that terminates the value. This replaces a prior
// first-byte/last-byte check that mishandled two cases (Finding 2):
//
//  1. A trailing `\"` (an escaped quote, i.e. a literal `"` inside the
//     value) is not a terminator; scanning must continue past it. The old
//     code treated any string ending in `"` as terminated, silently
//     accepting genuinely unterminated input like `"abc\"` as `abc\`.
//  2. Trailing non-whitespace content after the real closing quote (e.g.
//     `"a"b"`) is rejected as a parse error rather than silently
//     concatenated. Implementing full POSIX word-concatenation semantics
//     (quote-then-bareword-then-quote joining) is out of scope and
//     ambiguous, and silently reinterpreting it risks mis-importing secret
//     values. Erroring out is the safer choice for a tool handling secret
//     values, consistent with command.md's "Any unsupported tokenization is
//     parse error" rule for the POSIX parser and design-import.md's
//     non-goal of shell-evaluation-equivalent parsing.
func parsePosixDoubleQuotedImportValue(raw string) (string, string) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", "unterminated quoted value"
	}
	var b strings.Builder
	i := 1
	closed := false
	for i < len(raw) {
		c := raw[i]
		if c == '"' {
			closed = true
			i++
			break
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		// Backslash: if it's the last byte of currently available input,
		// this value is incomplete rather than a decided error - the
		// multi-line continuation logic in parseImportScopes (Finding 1)
		// will retry with more input if any remains. Reporting
		// "unterminated quoted value" here lets that mechanism decide
		// whether to append another line or surface a hard error once no
		// more input is available.
		if i+1 >= len(raw) {
			return "", unterminatedQuotedValueReason
		}
		switch raw[i+1] {
		case '\\', '"', '$', '`':
			b.WriteByte(raw[i+1])
		default:
			b.WriteByte('\\')
			b.WriteByte(raw[i+1])
		}
		i += 2
	}
	if !closed {
		return "", unterminatedQuotedValueReason
	}
	if strings.TrimFunc(raw[i:], func(r rune) bool { return r <= 0xFF && isASCIISpace(byte(r)) }) != "" {
		return "", posixImportAssignmentFormatError()
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
	// Note: unlike the posix path, the fish format requires a terminal ';'
	// (design-import.md "Fish parser" rules), which can only ever appear on
	// the LAST physical line of a multi-line value. So, unlike the old
	// implementation, this function no longer trims a trailing ';' off the
	// whole line up front - instead the quoted-value scan below consumes
	// exactly the quoted token and then requires the trailing ';' (and
	// nothing else but whitespace) to immediately follow the closing quote.
	if !strings.HasPrefix(line, "set -gx ") {
		return "", "", "unsupported assignment format"
	}
	rest := strings.TrimPrefix(line, "set -gx ")
	rest = strings.TrimLeft(rest, " \t")
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
	valueExpr := strings.TrimLeft(rest[sep+1:], " \t")
	value, reason := parseFishQuotedImportValue(valueExpr)
	if reason != "" {
		return "", "", reason
	}
	return key, value, ""
}

// parseFishQuotedImportValue scans raw (which the caller guarantees starts
// with '\”) byte-by-byte, decoding the two fish single-quote escape
// sequences: \\ for a literal backslash (required so backslash-ending
// values round-trip, per Finding F-02) and \' for a literal embedded quote
// (design-import.md "Fish parser" rules: "Fish single-quote escaping (\')
// is supported"). After the closing quote, only the mandatory terminal ';'
// (design-import.md: "Terminal ';' is required") plus surrounding
// whitespace may follow.
func parseFishQuotedImportValue(raw string) (string, string) {
	if len(raw) == 0 || raw[0] != '\'' {
		return "", unterminatedQuotedValueReason
	}
	var b strings.Builder
	i := 1
	closed := false
	for i < len(raw) {
		c := raw[i]
		if c == '\'' {
			closed = true
			i++
			break
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		// As with the posix double-quote scanner, a trailing backslash with
		// no following byte in the currently available input is treated as
		// "unterminated" so Finding 1's multi-line continuation logic in
		// parseImportScopes can retry with another line before deciding
		// it's a genuine error.
		if i+1 >= len(raw) {
			return "", unterminatedQuotedValueReason
		}
		switch raw[i+1] {
		case '\\':
			b.WriteByte('\\')
		case '\'':
			b.WriteByte('\'')
		default:
			return "", "unsupported escape sequence"
		}
		i += 2
	}
	if !closed {
		return "", unterminatedQuotedValueReason
	}
	trailer := strings.TrimFunc(raw[i:], func(r rune) bool { return r <= 0xFF && isASCIISpace(byte(r)) })
	if trailer != ";" {
		return "", "unsupported assignment format"
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

func runExec(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	execOpts, err := parseExecOptions(args)
	if err != nil {
		return err
	}
	return runExecWithOptions(opts, execOpts, stdin, stdout, stderr)
}

type execOptions struct {
	includeAll bool
	envList    string
	cmdArgs    []string
}

func parseExecOptions(args []string) (execOptions, error) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var execOpts execOptions
	fs.BoolVar(&execOpts.includeAll, "all", false, "inject all secrets into child environment")
	fs.StringVar(&execOpts.envList, "env", "", "comma-separated key allowlist to inject into child environment")
	if err := fs.Parse(args); err != nil {
		return execOptions{}, err
	}
	execOpts.cmdArgs = fs.Args()
	if len(execOpts.cmdArgs) > 0 && execOpts.cmdArgs[0] == "--" {
		execOpts.cmdArgs = execOpts.cmdArgs[1:]
	}
	return execOpts, nil
}

func runExecWithOptions(opts globalOptions, execOpts execOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(execOpts.cmdArgs) == 0 {
		return errors.New("exec requires command after flags")
	}
	m, err := showSecrets(opts)
	if err != nil {
		return err
	}
	selected, err := selectExecSecrets(m, execOpts.includeAll, execOpts.envList)
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
	cmd := exec.Command(execOpts.cmdArgs[0], execOpts.cmdArgs[1:]...)
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
	replacer := strings.NewReplacer("\\", "\\\\", "'", "\\'")
	return "'" + replacer.Replace(v) + "'"
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
