package kinko

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func runDirenvExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("direnv export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	withScopeComments := true
	rawExcludeKeys := stringListFlag{}
	fs.BoolVar(&withScopeComments, "with-scope-comments", true, "include # kinko:scope markers in export output")
	fs.Var(&rawExcludeKeys, "exclude", "comma-separated key denylist to omit from export output (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	shell := shellBash
	switch fs.NArg() {
	case 0:
	case 1:
		shell = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
		if shell == "" {
			return errors.New("shell name must not be empty")
		}
	default:
		return errors.New("direnv export accepts at most one shell argument")
	}

	scopePath := resolveDirenvScope(opts.path, os.Getenv("DIRENV_DIR"))
	nonInteractive := opts
	nonInteractive.path = scopePath
	nonInteractive.force = true
	nonInteractive.confirm = false

	parseArgs := []string{
		shell,
		"--with-scope-comments=" + strconv.FormatBool(withScopeComments),
	}
	for _, v := range rawExcludeKeys {
		parseArgs = append(parseArgs, "--exclude", v)
	}
	return runExport(nonInteractive, parseArgs, stdin, stdout, stderr)
}

func resolveDirenvScope(fallbackPath, direnvDir string) string {
	raw := strings.TrimSpace(direnvDir)
	if raw == "" {
		return fallbackPath
	}
	raw = strings.TrimPrefix(raw, "-")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallbackPath
	}

	target := normalizePathInput(raw)
	info, err := os.Stat(target)
	if err != nil {
		return fallbackPath
	}

	scope := target
	if !info.IsDir() {
		scope = filepath.Dir(target)
	}
	if abs, err := filepath.Abs(scope); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(scope)
}
