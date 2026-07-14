package kinko

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tacogips/kinko/internal/build"

	"github.com/spf13/cobra"
)

type runtimeContext struct {
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	rawArgs []string
	opts    globalOptions
}

var (
	getWorkingDirectory = os.Getwd
	getUserHomeDir      = os.UserHomeDir
)

func newRuntimeRootCommand(ctx *runtimeContext) (*cobra.Command, error) {
	defaults, err := defaultGlobalOptions()
	if err != nil {
		return nil, err
	}
	ctx.opts = defaults

	root := &cobra.Command{
		Use:           "kinko",
		Short:         "Encrypted local secret vault with scope-aware environment workflows",
		Args:          cobra.NoArgs,
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
	root.CompletionOptions.DisableDefaultCmd = true

	root.PersistentFlags().StringVar(&ctx.opts.profile, "profile", defaults.profile, "profile")
	root.PersistentFlags().StringVar(&ctx.opts.path, "path", defaults.path, "path")
	root.PersistentFlags().StringVar(&ctx.opts.dataDir, "kinko-dir", defaults.dataDir, "kinko data dir")
	root.PersistentFlags().StringVar(&ctx.opts.configPath, "config", defaults.configPath, "bootstrap config path")
	root.PersistentFlags().StringVar(&ctx.opts.keychainPreflight, "keychain-preflight", defaults.keychainPreflight, "keychain preflight mode: required|best-effort|off")
	root.PersistentFlags().BoolVar(&ctx.opts.force, "force", defaults.force, "override non-tty/redirection guardrails; for sync, make the command direction authoritative on conflicts")
	root.PersistentFlags().BoolVar(&ctx.opts.confirm, "confirm", defaults.confirm, "confirm sensitive tty output")

	finalizeOnlyPreflight := func() error {
		dataDirExplicit := root.PersistentFlags().Changed("kinko-dir") || os.Getenv("KINKO_DATA_DIR") != ""
		if !dataDirExplicit {
			dataDir, ok, err := loadBootstrapDataDir(ctx.opts.configPath)
			if err != nil {
				return err
			}
			if ok {
				ctx.opts.dataDir = dataDir
			}
		}
		if err := finalizeGlobalOptions(&ctx.opts); err != nil {
			return err
		}
		return nil
	}
	preflight := func() error {
		if err := finalizeOnlyPreflight(); err != nil {
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
		newBackupCommand(ctx, finalizeOnlyPreflight),
		newRestoreCommand(ctx, finalizeOnlyPreflight),
		newSetCommand(ctx, preflight),
		newSetKeyCommand(ctx, preflight),
		newDeleteCommand(ctx, preflight),
		newCopyCommand(ctx, preflight),
		newMoveCommand(ctx, preflight),
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
		newFolderCommand(ctx, preflight),
		newProfileCommand(ctx, preflight),
		newPathCommand(ctx, preflight),
		newDirenvCommand(ctx, preflight, func() bool { return root.PersistentFlags().Changed("path") }),
		newPasswordCommand(ctx, preflight),
		newDoctorCommand(ctx, finalizeOnlyPreflight),
		newMigrationCommand(ctx, finalizeOnlyPreflight),
		newSyncCommand(ctx, preflight),
	)

	return root, nil
}

func newSyncCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdSync,
		Short: "Synchronize vault secrets with a remote provider",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.AddCommand(
		newSyncDirectionCommand(ctx, preflight, syncDirectionPush),
		newSyncDirectionCommand(ctx, preflight, syncDirectionPull),
	)
	return root
}

func newSyncDirectionCommand(ctx *runtimeContext, preflight func() error, direction syncDirection) *cobra.Command {
	provider := ""
	dryRun := false
	projectID := ""
	jsonOutput := false
	directionTitle := "Push"
	if direction == syncDirectionPull {
		directionTitle = "Pull"
	}
	command := &cobra.Command{
		Use:   string(direction),
		Short: fmt.Sprintf("%s vault secrets using a remote provider", directionTitle),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return newCLIError(exitCodePolicyFailed, fmt.Sprintf("sync %s does not accept positional arguments.", direction), errors.New("unexpected sync arguments"))
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			return runSyncWithOptions(ctx.opts, syncOptions{
				direction: direction,
				provider:  provider,
				force:     ctx.opts.force,
				dryRun:    dryRun,
				projectID: projectID,
				jsonOut:   jsonOutput,
			}, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	command.Flags().StringVar(&provider, "provider", "", "sync provider (required; bws)")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the sync plan without mutations")
	command.Flags().StringVar(&projectID, "project-id", "", "BWS project id")
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON output")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newCLIError(exitCodePolicyFailed, fmt.Sprintf("Invalid sync %s flag: %v", direction, err), err)
	})
	return command
}

func newDoctorCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Short: "Run local diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			return runDoctor(ctx.opts, ctx.stdout)
		},
	}
}

func newBackupCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	currentStdin := false
	forceTTY := false
	currentFD := -1
	destPath := "."
	cmd := &cobra.Command{
		Use:   cmdBackup + " [directory]",
		Short: "Create a password-locked ZIP backup",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && cmd.Flags().Changed("dest-path") {
				return errors.New("backup destination must be provided either as positional argument or --dest-path, not both")
			}
			if err := preflight(); err != nil {
				return err
			}
			if len(args) == 1 {
				destPath = args[0]
			}
			backupOpts := backupOptions{
				input: backupInputOptions{
					currentStdin: currentStdin,
					currentFD:    currentFD,
					forceTTY:     forceTTY,
				},
				destPath: destPath,
			}
			return runBackupWithOptions(ctx.opts, backupOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&currentStdin, "current-stdin", false, "read current password from stdin")
	cmd.Flags().BoolVar(&forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	cmd.Flags().IntVar(&currentFD, "current-fd", -1, "read current password from file descriptor")
	cmd.Flags().StringVar(&destPath, "dest-path", ".", "destination directory for backup archive")
	return cmd
}

func newRestoreCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	currentStdin := false
	forceTTY := false
	currentFD := -1
	includeBootstrap := false
	cmd := &cobra.Command{
		Use:   cmdRestore + " <archive>",
		Short: "Restore a vault from a password-locked ZIP backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			restoreOpts := restoreOptions{
				input: restoreInputOptions{
					currentStdin: currentStdin,
					currentFD:    currentFD,
					forceTTY:     forceTTY,
				},
				archivePath:      args[0],
				includeBootstrap: includeBootstrap,
			}
			return runRestoreWithOptions(ctx.opts, restoreOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&currentStdin, "current-stdin", false, "read backup password from stdin")
	cmd.Flags().BoolVar(&forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	cmd.Flags().IntVar(&currentFD, "current-fd", -1, "read backup password from file descriptor")
	cmd.Flags().BoolVar(&includeBootstrap, "include-bootstrap", false, "also restore the archived bootstrap config to the --config path")
	return cmd
}

func newUnlockCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	timeout := 9 * time.Hour
	permanent := false
	cmd := &cobra.Command{
		Use:   cmdUnlock,
		Short: "Unlock vault session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			timeoutProvided := cmd.Flags().Changed("timeout")
			if permanent && timeoutProvided {
				return newCLIError(exitCodePolicyFailed, "--permanent and --timeout are mutually exclusive", nil)
			}
			if err := preflight(); err != nil {
				return err
			}
			unlockOpts := unlockOptions{
				timeout:         timeout,
				timeoutProvided: timeoutProvided,
				permanent:       permanent,
			}
			return runUnlockWithOptions(ctx.opts, unlockOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 9*time.Hour, "unlock timeout")
	cmd.Flags().BoolVar(&permanent, "permanent", false, "unlock without automatic expiry")
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
			setOpts := setOptions{
				shared:      shared,
				assignments: append([]string{}, cmd.Flags().Args()...),
			}
			return runSetWithOptions(ctx.opts, setOpts, ctx.stdin, ctx.stdout)
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
			args := cmd.Flags().Args()
			if len(args) != 1 {
				return errors.New("set-key requires a key")
			}
			setKeyOpts := setKeyOptions{
				key:           args[0],
				value:         value,
				valueProvided: cmd.Flags().Changed("value"),
				shared:        shared,
			}
			return runSetKeyWithOptions(ctx.opts, setKeyOpts, ctx.stdin, ctx.stdout)
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
			deleteOpts := deleteOptions{
				autoYes:   autoYes,
				deleteAll: deleteAll,
				shared:    shared,
			}
			args := cmd.Flags().Args()
			if len(args) == 1 {
				deleteOpts.key = args[0]
			}
			if deleteOpts.deleteAll && len(args) > 0 {
				return errors.New("delete --all cannot be combined with a key")
			}
			if !deleteOpts.deleteAll && len(args) != 1 {
				return errors.New("delete requires a key or --all")
			}
			return runDeleteWithOptions(ctx.opts, deleteOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "delete from shared scope")
	cmd.Flags().BoolVar(&deleteAll, "all", false, "delete all keys in selected scope")
	cmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "auto confirm deletion")
	return cmd
}

func newMoveCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdMove,
		Short: "Move one secret between local and shared scopes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(
		newMoveDirectionCommand(ctx, preflight, moveLocalToShared, "Move one local profile/path secret into shared scope"),
		newMoveDirectionCommand(ctx, preflight, moveSharedToLocal, "Move one shared secret into the current local profile/path scope"),
	)
	return root
}

func newCopyCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdCopy,
		Short: "Copy secrets between local and shared scopes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(
		newCopyLocalToLocalCommand(ctx, preflight),
		newCopyDirectionCommand(ctx, preflight, moveLocalToShared, "Copy local profile/path secrets into shared scope"),
		newCopyDirectionCommand(ctx, preflight, moveSharedToLocal, "Copy shared secrets into the current local profile/path scope"),
	)
	return root
}

func newCopyLocalToLocalCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	fromPath := ""
	overwrite := false
	cmd := &cobra.Command{
		Use:   copyLocalToLocal + " --from-path DIR KEY|*",
		Short: "Copy secrets from another local path into the current local path",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			copyOpts := copySecretOptions{
				Direction: copyDirectionLocalToLocal,
				Key:       args[0],
				FromPath:  fromPath,
				Overwrite: overwrite,
			}
			return runCopyWithOptions(ctx.opts, copyOpts, ctx.stdout)
		},
	}
	cmd.Flags().StringVar(&fromPath, "from-path", "", "source local path scope")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing destination keys")
	return cmd
}

func newCopyDirectionCommand(ctx *runtimeContext, preflight func() error, direction string, short string) *cobra.Command {
	overwrite := false
	cmd := &cobra.Command{
		Use:   direction + " KEY|*",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			copyOpts := copySecretOptions{
				Direction: copyDirection(direction),
				Key:       args[0],
				Overwrite: overwrite,
			}
			return runCopyWithOptions(ctx.opts, copyOpts, ctx.stdout)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing destination keys")
	return cmd
}

func newMoveDirectionCommand(ctx *runtimeContext, preflight func() error, direction string, short string) *cobra.Command {
	overwrite := false
	autoYes := false
	cmd := &cobra.Command{
		Use:   direction + " KEY",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			moveOpts := moveSecretOptions{
				Direction: moveDirection(direction),
				Key:       args[0],
				Overwrite: overwrite,
				Yes:       autoYes,
			}
			return runMoveWithOptions(ctx.opts, moveOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing destination key")
	cmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "skip move confirmation")
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
			return runGetWithOptions(ctx.opts, getOptions{key: args[0], reveal: reveal}, ctx.stdin, ctx.stdout, ctx.stderr)
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
			return runShowWithOptions(ctx.opts, showOptions{reveal: reveal, allScopes: allScopes}, ctx.stdin, ctx.stdout, ctx.stderr)
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
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

func newProfileCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdProfile,
		Short: "Manage stored profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(
		&cobra.Command{
			Use:   profileList,
			Short: "List stored profiles",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				if err := preflight(); err != nil {
					return err
				}
				return runProfile(ctx.opts, []string{profileList}, ctx.stdout)
			},
		},
	)
	return root
}

func newFolderCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdFolder,
		Short: "Manage encrypted folder vaults",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(
		newFolderNamedCommand(ctx, preflight, folderAdd, "Register an encrypted folder vault"),
		newFolderUnlockCommand(ctx, preflight),
		newFolderNamedCommand(ctx, preflight, folderLock, "Lock and unmount an encrypted folder vault"),
		newFolderRemoveCommand(ctx, preflight),
		newFolderStatusCommand(ctx, preflight),
		newFolderNamedCommand(ctx, preflight, folderPath, "Print a mounted folder vault path"),
	)
	return root
}

func newFolderNamedCommand(ctx *runtimeContext, preflight func() error, subcommand string, short string) *cobra.Command {
	return &cobra.Command{
		Use:   subcommand + " NAME",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			folderOpts := folderNameOptions{name: args[0]}
			switch subcommand {
			case folderAdd:
				return folderCommandError(runFolderAddWithOptions(ctx.opts, folderOpts, ctx.stdout))
			case folderLock:
				return folderCommandError(runFolderLockWithOptions(ctx.opts, folderOpts, ctx.stdout))
			case folderPath:
				return folderCommandError(runFolderPathWithOptions(ctx.opts, folderOpts, ctx.stdout))
			default:
				return folderCommandError(fmt.Errorf("unknown folder subcommand %q", subcommand))
			}
		},
	}
}

func newFolderUnlockCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	hold := false
	cmd := &cobra.Command{
		Use:   folderUnlock + " NAME",
		Short: "Unlock and mount an encrypted folder vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			folderOpts := folderUnlockOptions{name: args[0], hold: hold}
			return folderCommandError(runFolderUnlockWithOptions(ctx.opts, folderOpts, ctx.stdout))
		},
	}
	cmd.Flags().BoolVar(&hold, "hold", false, "accepted for compatibility; unlock already holds the mount in the foreground")
	if flag := cmd.Flags().Lookup("hold"); flag != nil {
		flag.Hidden = true
	}
	return cmd
}

func newFolderRemoveCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	keepStorage := false
	yes := false
	cmd := &cobra.Command{
		Use:   folderRemove + " NAME",
		Short: "Remove an encrypted folder vault registration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			folderOpts := folderRemoveOptions{
				name:        args[0],
				keepStorage: keepStorage,
				yes:         yes,
			}
			return folderCommandError(runFolderRemoveWithOptions(ctx.opts, folderOpts, ctx.stdin, ctx.stdout, ctx.stderr))
		},
	}
	cmd.Flags().BoolVar(&keepStorage, "keep-storage", false, "remove registration without deleting encrypted folder storage")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete encrypted folder storage without prompting")
	return cmd
}

func newFolderStatusCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	return &cobra.Command{
		Use:   folderStatus + " [NAME]",
		Short: "Show encrypted folder vault status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			folderOpts := folderStatusOptions{}
			if len(args) == 1 {
				folderOpts.name = args[0]
			}
			return folderCommandError(runFolderStatusWithOptions(ctx.opts, folderOpts, ctx.stdout))
		},
	}
}

func newPathCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdPath,
		Short: "Manage stored path scopes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(newPathPruneMissingCommand(ctx, preflight))
	return root
}

func newPathPruneMissingCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	allProfiles := false
	autoYes := false
	jsonOutput := false
	cmd := &cobra.Command{
		Use:   pathPruneMissing,
		Short: "Preview or prune path scopes whose directories are missing",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			if allProfiles && explicitLongFlag(ctx.rawArgs, "profile") {
				return fmt.Errorf("%s %s cannot combine --all-profiles with explicit --profile", cmdPath, pathPruneMissing)
			}
			pruneOpts := pathPruneMissingOptions{
				AllProfiles: allProfiles,
				Yes:         autoYes,
				JSON:        jsonOutput,
			}
			return runPathPruneMissingWithOptions(ctx.opts, pruneOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&allProfiles, "all-profiles", false, "scan every stored profile")
	cmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "prune missing path scopes")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON output")
	return cmd
}

func explicitLongFlag(args []string, name string) bool {
	exact := "--" + name
	prefix := exact + "="
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == exact || strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func newExportCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	withScopeComments := true
	sharedOnly := false
	exclude := []string{}
	cmd := &cobra.Command{
		Use:   cmdExport + " [shell]",
		Short: "Export secrets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			exportOpts := exportOptions{
				shell:             shellPosix,
				withScopeComments: withScopeComments,
				sharedOnly:        sharedOnly,
				excludeKeys:       append([]string{}, exclude...),
			}
			if len(args) == 1 {
				exportOpts.shell = args[0]
			}
			return runExportWithOptions(ctx.opts, exportOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&withScopeComments, "with-scope-comments", true, "include scope comments")
	cmd.Flags().BoolVar(&sharedOnly, "shared-only", false, "export only shared scope keys")
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
			importOpts := importOptions{
				shell:             shellPosix,
				filePath:          filePath,
				autoYes:           autoYes,
				confirmWithValues: confirmWithValues,
				allowShared:       allowShared,
			}
			if len(args) == 1 {
				importOpts.shell = args[0]
			}
			return runImportWithOptions(ctx.opts, importOpts, ctx.stdin, ctx.stdout, ctx.stderr)
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
			execOpts := execOptions{
				includeAll: includeAll,
				envList:    env,
				cmdArgs:    append([]string{}, cmd.Flags().Args()...),
			}
			return runExecWithOptions(ctx.opts, execOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVar(&includeAll, "all", false, "inject all resolved secrets")
	cmd.Flags().StringVar(&env, "env", "", "comma-separated allowlist")
	// Stop flag parsing at the first non-flag argument so that flag-like
	// tokens belonging to the child command (e.g. `exec --env FOO node --env BAR`)
	// are passed through as positional args instead of being reparsed as
	// kinko flags. Without this, pflag's default interspersed parsing would
	// scan the entire argument list for registered flags, allowing a child
	// command's own flags to be misinterpreted as kinko flags.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newPasswordCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdPassword,
		Short: "Password management operations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	change := newPasswordChangeCommand(ctx, preflight)
	root.AddCommand(change)
	return root
}

func newDirenvCommand(ctx *runtimeContext, preflight func() error, pathChanged func() bool) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdDirenv,
		Short: "direnv-focused helpers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	withScopeComments := true
	sharedOnly := false
	exclude := []string{}
	exportCmd := &cobra.Command{
		Use:   cmdExport + " [shell]",
		Short: "Export secrets for direnv eval with automatic scope detection",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := preflight(); err != nil {
				return err
			}
			exportOpts := exportOptions{
				shell:             shellBash,
				withScopeComments: withScopeComments,
				sharedOnly:        sharedOnly,
				excludeKeys:       append([]string{}, exclude...),
			}
			if len(args) == 1 {
				exportOpts.shell = args[0]
			}
			pathExplicit := pathChanged != nil && pathChanged()
			return runDirenvExportWithOptions(ctx.opts, exportOpts, ctx.stdin, ctx.stdout, ctx.stderr, pathExplicit)
		},
	}
	exportCmd.Flags().BoolVar(&withScopeComments, "with-scope-comments", true, "include scope comments")
	exportCmd.Flags().BoolVar(&sharedOnly, "shared-only", false, "export only shared scope keys")
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
			inputOpts := passwordInputOptions{
				currentStdin: currentStdin,
				newStdin:     newStdin,
				currentFD:    currentFD,
				newFD:        newFD,
				forceTTY:     forceTTY,
			}
			return runPasswordChangeWithOptions(ctx.opts, inputOpts, ctx.stdin, ctx.stdout, ctx.stderr)
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
	cwd, err := getWorkingDirectory()
	if err != nil {
		return globalOptions{}, fmt.Errorf("resolve cwd: %w", err)
	}
	home, err := getUserHomeDir()
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
