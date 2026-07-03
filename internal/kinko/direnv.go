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
	return runDirenvExportWithOptions(opts, exportOpts, stdin, stdout, stderr)
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

func runDirenvExportWithOptions(opts globalOptions, exportOpts exportOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if strings.TrimSpace(exportOpts.shell) == "" {
		return errors.New("shell name must not be empty")
	}
	scopePath := resolveDirenvScope(opts.path, os.Getenv("DIRENV_DIR"))
	nonInteractive := opts
	nonInteractive.path = scopePath
	nonInteractive.force = true
	nonInteractive.confirm = false
	return runExportWithOptions(nonInteractive, exportOpts, stdin, stdout, stderr)
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
