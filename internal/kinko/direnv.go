package kinko

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runDirenvExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	exportOpts, err := parseDirenvExportOptions(args)
	if err != nil {
		return err
	}
	// pathExplicit defaults to false here because this entrypoint is used
	// directly (e.g. by tests and any non-cobra caller) with no notion of
	// whether a --path flag was explicitly set by a user at the cobra
	// layer. This preserves the existing DIRENV_DIR-wins-by-default
	// behavior for direct callers of runDirenvExport.
	return runDirenvExportWithOptions(opts, exportOpts, stdin, stdout, stderr, false)
}

func parseDirenvExportOptions(args []string) (exportOptions, error) {
	fs := flag.NewFlagSet("direnv export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	rawExcludeKeys := stringListFlag{}
	exportOpts := exportOptions{
		shell:             shellBash,
		withScopeComments: true,
	}
	fs.BoolVar(&exportOpts.withScopeComments, "with-scope-comments", true, "include # kinko:scope markers in export output")
	fs.BoolVar(&exportOpts.sharedOnly, "shared-only", false, "export only shared scope keys")
	fs.Var(&rawExcludeKeys, "exclude", "comma-separated key denylist to omit from export output (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exportOptions{}, err
	}

	switch fs.NArg() {
	case 0:
	case 1:
		exportOpts.shell = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
		if exportOpts.shell == "" {
			return exportOptions{}, errors.New("shell name must not be empty")
		}
	default:
		return exportOptions{}, errors.New("direnv export accepts at most one shell argument")
	}
	exportOpts.excludeKeys = append([]string{}, rawExcludeKeys...)
	return exportOpts, nil
}

func runDirenvExportWithOptions(opts globalOptions, exportOpts exportOptions, stdin io.Reader, stdout, stderr io.Writer, pathExplicit bool) error {
	if strings.TrimSpace(exportOpts.shell) == "" {
		return errors.New("shell name must not be empty")
	}
	scopePath := resolveDirenvScope(opts.path, os.Getenv("DIRENV_DIR"), pathExplicit)
	nonInteractive := opts
	nonInteractive.path = scopePath
	nonInteractive.force = true
	nonInteractive.confirm = false
	return runExportWithOptions(nonInteractive, exportOpts, stdin, stdout, stderr)
}

// resolveDirenvScope determines which path scope direnv export should use.
// When pathExplicit is true, the user explicitly passed --path on the
// command line, so that value always wins and DIRENV_DIR is ignored
// entirely. When pathExplicit is false (the default, e.g. --path was not
// passed and only derives from cwd/KINKO_PATH), DIRENV_DIR is preferred
// when it resolves to a valid file or directory, preserving prior behavior.
func resolveDirenvScope(fallbackPath, direnvDir string, pathExplicit bool) string {
	if pathExplicit {
		return fallbackPath
	}
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
