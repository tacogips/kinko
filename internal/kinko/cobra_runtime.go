package kinko

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"githus.com/tacogips/kinko/internal/build"

	"github.com/spf13/cobra"
)

type runtimeContext struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	opts   globalOptions
}

func newRuntimeRootCommand(ctx *runtimeContext) *cobra.Command {
	defaults, err := defaultGlobalOptions()
	if err != nil {
		defaults = globalOptions{
			profile:           defaultProfile,
			path:              ".",
			dataDir:           ".",
			configPath:        ".",
			keychainPreflight: "required",
			confirm:           true,
		}
	}
	ctx.opts = defaults

	root := &cobra.Command{
		Use:           "kinko",
		Short:         "Encrypted local secret vault with scope-aware environment workflows",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetIn(ctx.stdin)
	root.SetOut(ctx.stdout)
	root.SetErr(ctx.stderr)
	root.TraverseChildren = true

	root.PersistentFlags().StringVar(&ctx.opts.profile, "profile", defaults.profile, "profile")
	root.PersistentFlags().StringVar(&ctx.opts.path, "path", defaults.path, "path")
	root.PersistentFlags().StringVar(&ctx.opts.dataDir, "kinko-dir", defaults.dataDir, "kinko data dir")
	root.PersistentFlags().StringVar(&ctx.opts.configPath, "config", defaults.configPath, "bootstrap config path")
	root.PersistentFlags().StringVar(&ctx.opts.keychainPreflight, "keychain-preflight", defaults.keychainPreflight, "keychain preflight mode: required|best-effort|off")
	root.PersistentFlags().BoolVar(&ctx.opts.force, "force", defaults.force, "override non-tty/redirection guardrails")
	root.PersistentFlags().BoolVar(&ctx.opts.confirm, "confirm", defaults.confirm, "confirm sensitive tty output")

	preflight := func() error {
		if err := finalizeGlobalOptions(&ctx.opts); err != nil {
			return err
		}
		return validateBootstrapConfigFile(ctx.opts.configPath)
	}

	root.AddCommand(
		&cobra.Command{
			Use:   cmdVersion,
			Short: "Print version",
			RunE: func(*cobra.Command, []string) error {
				_, _ = fmt.Fprintln(ctx.stdout, build.Version())
				return nil
			},
		},
		&cobra.Command{
			Use:   cmdInit,
			Short: "Initialize encrypted vault metadata and local storage",
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runInit(ctx.opts, nil, ctx.stdin, ctx.stdout, ctx.stderr)
			},
		},
		newUnlockCommand(ctx, preflight),
		&cobra.Command{
			Use:   cmdLock,
			Short: "Lock session",
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runLock(ctx.opts, ctx.stderr)
			},
		},
		&cobra.Command{
			Use:   cmdStatus,
			Short: "Print lock status",
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runStatus(ctx.opts, ctx.stdout)
			},
		},
		newSetCommand(ctx, preflight),
		newSetKeyCommand(ctx, preflight),
		newDeleteCommand(ctx, preflight),
		&cobra.Command{
			Use:   cmdExplosion,
			Short: "Irreversibly destroy vault data",
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runExplosion(ctx.opts, ctx.stdin, ctx.stdout, ctx.stderr)
			},
		},
		newGetCommand(ctx, preflight),
		newShowCommand(ctx, preflight),
		newConfigCommand(ctx, preflight),
		newExportCommand(ctx, preflight),
		newImportCommand(ctx, preflight),
		newExecCommand(ctx, preflight),
		newDirenvCommand(ctx, preflight),
		newPasswordCommand(ctx, preflight),
	)

	return root
}

func newUnlockCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	timeout := "9h"
	cmd := &cobra.Command{
		Use:   cmdUnlock,
		Short: "Unlock vault session",
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			return runUnlock(ctx.opts, []string{"--timeout", timeout}, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().StringVar(&timeout, "timeout", "9h", "unlock timeout")
	return cmd
}

func newSetCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	shared := false
	cmd := &cobra.Command{
		Use:   cmdSet,
		Short: "Set one or more secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := append([]string{}, cmd.Flags().Args()...)
			if shared {
				parseArgs = append([]string{"--shared"}, parseArgs...)
			}
			return runSet(ctx.opts, parseArgs, ctx.stdin, ctx.stdout)
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "set keys in shared scope")
	return cmd
}

func newSetKeyCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	shared := false
	value := ""
	cmd := &cobra.Command{
		Use:   cmdSetKey + " KEY",
		Short: "Set one secret key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := append([]string{}, cmd.Flags().Args()...)
			if shared {
				parseArgs = append(parseArgs, "--shared")
			}
			if value != "" {
				parseArgs = append(parseArgs, "--value", value)
			}
			return runSetKey(ctx.opts, parseArgs, ctx.stdin, ctx.stdout)
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "set key in shared scope")
	cmd.Flags().StringVar(&value, "value", "", "set key value directly")
	return cmd
}

func newDeleteCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	shared := false
	deleteAll := false
	autoYes := false
	cmd := &cobra.Command{
		Use:   cmdDelete,
		Short: "Delete secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if autoYes {
				parseArgs = append(parseArgs, "--yes")
			}
			if deleteAll {
				parseArgs = append(parseArgs, "--all")
			}
			if shared {
				parseArgs = append(parseArgs, "--shared")
			}
			parseArgs = append(parseArgs, cmd.Flags().Args()...)
			return runDelete(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "delete from shared scope")
	cmd.Flags().BoolVar(&deleteAll, "all", false, "delete all keys in selected scope")
	cmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "auto confirm deletion")
	return cmd
}

func newGetCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	reveal := false
	cmd := &cobra.Command{
		Use:   cmdGet + " KEY",
		Short: "Get one secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{args[0]}
			if reveal {
				parseArgs = append(parseArgs, "--reveal")
			}
			return runGet(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "show plaintext")
	return cmd
}

func newShowCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	reveal := false
	allScopes := false
	cmd := &cobra.Command{
		Use:   cmdShow,
		Short: "Show secrets",
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if reveal {
				parseArgs = append(parseArgs, "--reveal")
			}
			if allScopes {
				parseArgs = append(parseArgs, "--all-scopes")
			}
			return runShow(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "show plaintext values")
	cmd.Flags().BoolVar(&allScopes, "all-scopes", false, "show all profile path scopes")
	return cmd
}

func newConfigCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdConfig,
		Short: "Manage config",
	}
	root.AddCommand(
		&cobra.Command{
			Use:   configShow,
			Short: "Show config",
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runConfig(ctx.opts, []string{configShow}, ctx.stdout)
			},
		},
		&cobra.Command{
			Use:   configSet + " KEY VALUE",
			Short: "Set config key",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runConfig(ctx.opts, []string{configSet, args[0], args[1]}, ctx.stdout)
			},
		},
	)
	return root
}

func newExportCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	withScopeComments := true
	exclude := []string{}
	cmd := &cobra.Command{
		Use:   cmdExport + " [shell]",
		Short: "Export secrets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if len(args) == 1 {
				parseArgs = append(parseArgs, args[0])
			}
			parseArgs = append(parseArgs, "--with-scope-comments="+strconv.FormatBool(withScopeComments))
			for _, v := range exclude {
				parseArgs = append(parseArgs, "--exclude", v)
			}
			return runExport(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&withScopeComments, "with-scope-comments", true, "include scope comments")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "comma-separated key denylist")
	return cmd
}

func newImportCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	filePath := ""
	autoYes := false
	confirmWithValues := false
	allowShared := true
	cmd := &cobra.Command{
		Use:   cmdImport + " [shell]",
		Short: "Import secrets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if len(args) == 1 {
				parseArgs = append(parseArgs, args[0])
			}
			if filePath != "" {
				parseArgs = append(parseArgs, "--file", filePath)
			}
			if autoYes {
				parseArgs = append(parseArgs, "--yes")
			}
			if confirmWithValues {
				parseArgs = append(parseArgs, "--confirm-with-values")
			}
			parseArgs = append(parseArgs, "--allow-shared="+strconv.FormatBool(allowShared))
			return runImport(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "import source file path")
	cmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "skip import confirmation flow")
	cmd.Flags().BoolVar(&confirmWithValues, "confirm-with-values", false, "show plaintext values in confirmation output")
	cmd.Flags().BoolVar(&allowShared, "allow-shared", true, "allow shared scope markers")
	return cmd
}

func newExecCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	includeAll := false
	env := ""
	cmd := &cobra.Command{
		Use:                cmdExec + " (--all|--env KEY[,KEY...]) -- command [args...]",
		Short:              "Execute with injected secrets",
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if includeAll {
				parseArgs = append(parseArgs, "--all")
			}
			if strings.TrimSpace(env) != "" {
				parseArgs = append(parseArgs, "--env", env)
			}
			parseArgs = append(parseArgs, "--")
			parseArgs = append(parseArgs, cmd.Flags().Args()...)
			return runExec(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&includeAll, "all", false, "inject all resolved secrets")
	cmd.Flags().StringVar(&env, "env", "", "comma-separated allowlist")
	return cmd
}

func newPasswordCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdPassword,
		Short: "Password management operations",
	}
	change := newPasswordChangeCommand(ctx, preflight)
	root.AddCommand(change)
	return root
}

func newDirenvCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdDirenv,
		Short: "direnv-focused helpers",
	}

	withScopeComments := true
	exclude := []string{}
	exportCmd := &cobra.Command{
		Use:   cmdExport + " [shell]",
		Short: "Export secrets for direnv eval with automatic scope detection",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if len(args) == 1 {
				parseArgs = append(parseArgs, args[0])
			}
			parseArgs = append(parseArgs, "--with-scope-comments="+strconv.FormatBool(withScopeComments))
			for _, v := range exclude {
				parseArgs = append(parseArgs, "--exclude", v)
			}
			return runDirenvExport(ctx.opts, parseArgs, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	exportCmd.Flags().BoolVar(&withScopeComments, "with-scope-comments", true, "include scope comments")
	exportCmd.Flags().StringSliceVar(&exclude, "exclude", nil, "comma-separated key denylist")
	root.AddCommand(exportCmd)
	return root
}

func newPasswordChangeCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	currentStdin := false
	newStdin := false
	forceTTY := false
	currentFD := -1
	newFD := -1
	cmd := &cobra.Command{
		Use:   "change",
		Short: "Rotate vault password",
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			parseArgs := []string{}
			if currentStdin {
				parseArgs = append(parseArgs, "--current-stdin")
			}
			if newStdin {
				parseArgs = append(parseArgs, "--new-stdin")
			}
			if forceTTY {
				parseArgs = append(parseArgs, "--force-tty")
			}
			if currentFD >= 0 {
				parseArgs = append(parseArgs, "--current-fd", strconv.Itoa(currentFD))
			}
			if newFD >= 0 {
				parseArgs = append(parseArgs, "--new-fd", strconv.Itoa(newFD))
			}
			return runPassword(ctx.opts, append([]string{"change"}, parseArgs...), ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&currentStdin, "current-stdin", false, "read current password from stdin")
	cmd.Flags().BoolVar(&newStdin, "new-stdin", false, "read new password from stdin")
	cmd.Flags().BoolVar(&forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	cmd.Flags().IntVar(&currentFD, "current-fd", -1, "read current password from file descriptor")
	cmd.Flags().IntVar(&newFD, "new-fd", -1, "read new password from file descriptor")
	return cmd
}

func defaultGlobalOptions() (globalOptions, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return globalOptions{}, fmt.Errorf("resolve cwd: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return globalOptions{}, fmt.Errorf("resolve home dir: %w", err)
	}
	return globalOptions{
		profile:           envOrDefault("KINKO_PROFILE", defaultProfile),
		path:              envOrDefault("KINKO_PATH", cwd),
		dataDir:           envOrDefault("KINKO_DATA_DIR", filepath.Join(home, ".local", "kinko")),
		configPath:        envOrDefault("KINKO_CONFIG", filepath.Join(home, ".config", "kinko", "bootstrap.toml")),
		force:             false,
		confirm:           true,
		keychainPreflight: envOrDefault("KINKO_KEYCHAIN_PREFLIGHT", "required"),
	}, nil
}

func finalizeGlobalOptions(opts *globalOptions) error {
	if strings.TrimSpace(opts.profile) == "" {
		return fmt.Errorf("--profile must not be empty")
	}
	absPath, err := filepath.Abs(normalizePathInput(opts.path))
	if err != nil {
		return fmt.Errorf("resolve --path: %w", err)
	}
	opts.path = filepath.Clean(absPath)

	absDataDir, err := filepath.Abs(opts.dataDir)
	if err != nil {
		return fmt.Errorf("resolve --kinko-dir: %w", err)
	}
	opts.dataDir = filepath.Clean(absDataDir)

	absConfigPath, err := filepath.Abs(opts.configPath)
	if err != nil {
		return fmt.Errorf("resolve --config: %w", err)
	}
	opts.configPath = filepath.Clean(absConfigPath)

	switch opts.keychainPreflight {
	case "required", "best-effort", "off":
		return nil
	default:
		return fmt.Errorf("--keychain-preflight must be one of: required, best-effort, off")
	}
}
