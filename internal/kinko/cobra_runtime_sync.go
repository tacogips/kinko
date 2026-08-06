package kinko

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

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
		newSyncBootstrapCommand(ctx, preflight),
		newSyncStatusCommand(ctx, preflight),
		newSyncResetCommand(ctx, preflight),
		newSyncReconcileCommand(ctx, preflight),
		newSyncPruneCommand(ctx, preflight),
	)
	return root
}

func newSyncDirectionCommand(ctx *runtimeContext, preflight func() error, direction syncDirection) *cobra.Command {
	common := newSyncCommonFlags()
	provider := ""
	dryRun := false
	projectID := ""
	jsonOutput := false
	yes := false
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
		RunE: func(command *cobra.Command, _ []string) error {
			selector, maps, err := common.options()
			if err != nil {
				return newCLIError(exitCodePolicyFailed, "Invalid sync options.", err)
			}
			if ctx.opts.force && (common.onConflict != "" || len(common.resolutions) > 0) {
				return newCLIError(exitCodePolicyFailed, "--force cannot be combined with --on-conflict or --resolve.", nil)
			}
			if err := preflight(); err != nil {
				return err
			}
			legacyOptions := syncOptions{
				direction: direction,
				provider:  provider,
				force:     ctx.opts.force,
				dryRun:    dryRun,
				projectID: projectID,
				jsonOut:   jsonOutput,
			}
			if !common.completionRequested(command) {
				return runLegacySyncCommand(ctx.opts, legacyOptions, ctx.stdin, ctx.stdout, ctx.stderr)
			}
			policy := syncConflictPolicy(common.onConflict)
			if policy == "" {
				policy = syncConflictFail
			}
			resolutions, _ := parseSyncResolutions(common.resolutions)
			return runCompletionSyncCommand(ctx.opts, syncDirectionV2Options{
				Direction: direction, Provider: provider, ProjectID: projectID,
				Force: ctx.opts.force, DryRun: dryRun, JSON: jsonOutput,
				Yes:      yes,
				Selector: selector, PathMaps: maps, ConflictPolicy: policy,
				Resolutions: resolutions, DeleteMode: syncDeleteMode(common.deleteMode),
				Runtime: common.runtime(projectID), Retry: syncRetryPolicy{
					MaxRetries: common.maxRetries, InitialDelay: defaultSyncRetryPolicy().InitialDelay,
					MaxDelay: common.retryMaxDelay, TotalBudget: defaultSyncRetryPolicy().TotalBudget,
				}, Resume: syncResumeMode(common.resume), Progress: syncProgressMode(common.progress),
			}, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	command.Flags().StringVar(&provider, "provider", "", "sync provider (required; bws)")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the sync plan without mutations")
	command.Flags().StringVar(&projectID, "project-id", "", "BWS project id")
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON output")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "confirm a plan containing deletion when --delete=confirm")
	common.bind(command)
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newCLIError(exitCodePolicyFailed, fmt.Sprintf("Invalid sync %s flag: %v", direction, err), err)
	})
	return command
}

var (
	runLegacySyncCommand     = runSyncWithOptions
	runCompletionSyncCommand = runSyncDirectionV2
)

type syncCommonFlags struct {
	includeProfiles, includePaths, includeKeys         []string
	excludeProfiles, excludePaths, excludeKeys         []string
	shared, onConflict, deleteMode                     string
	pathMaps, resolutions                              []string
	bwsConfigFile, bwsProfile, bwsServerURL, transport string
	allowArgv                                          bool
	maxRetries                                         int
	retryMaxDelay                                      time.Duration
	resume, progress                                   string
}

func newSyncCommonFlags() *syncCommonFlags {
	return &syncCommonFlags{shared: string(syncSharedInclude), deleteMode: string(syncDeleteAuto), transport: string(bwsTransportAuto), maxRetries: defaultSyncRetryPolicy().MaxRetries, retryMaxDelay: defaultSyncRetryPolicy().MaxDelay, resume: string(syncResumeAuto), progress: string(syncProgressAuto)}
}

func (flags *syncCommonFlags) bind(command *cobra.Command) {
	f := command.Flags()
	f.StringArrayVar(&flags.includeProfiles, "select-profile", nil, "select an exact profile (repeatable)")
	f.StringArrayVar(&flags.includePaths, "select-path", nil, "select logical:<path> or local:<absolute-path> (repeatable)")
	f.StringArrayVar(&flags.includeKeys, "select-key", nil, "select an exact key or glob:<pattern> (repeatable)")
	f.StringArrayVar(&flags.excludeProfiles, "exclude-profile", nil, "exclude a profile (repeatable)")
	f.StringArrayVar(&flags.excludePaths, "exclude-path", nil, "exclude a logical or local path (repeatable)")
	f.StringArrayVar(&flags.excludeKeys, "exclude-key", nil, "exclude a key (repeatable)")
	f.StringVar(&flags.shared, "shared", string(syncSharedInclude), "shared selection: include|exclude|only")
	f.StringArrayVar(&flags.pathMaps, "map-path", nil, "map logical anchor to an absolute root (repeatable)")
	f.StringVar(&flags.onConflict, "on-conflict", "", "conflict policy: fail|local|remote|skip")
	f.StringArrayVar(&flags.resolutions, "resolve", nil, "resolve entry-id=policy (repeatable)")
	f.StringVar(&flags.deleteMode, "delete", string(syncDeleteAuto), "deletion policy: auto|keep|confirm")
	f.StringVar(&flags.bwsConfigFile, "bws-config-file", "", "BWS config file")
	f.StringVar(&flags.bwsProfile, "bws-profile", "", "BWS config profile")
	f.StringVar(&flags.bwsServerURL, "bws-server-url", "", "BWS server base URL")
	f.StringVar(&flags.transport, "bws-transport", string(bwsTransportAuto), "BWS transport: auto|cli-legacy")
	f.BoolVar(&flags.allowArgv, "allow-secret-argv", false, "allow legacy BWS mutations to expose values in argv")
	f.IntVar(&flags.maxRetries, "max-retries", defaultSyncRetryPolicy().MaxRetries, "maximum transient read retries")
	f.DurationVar(&flags.retryMaxDelay, "retry-max-delay", defaultSyncRetryPolicy().MaxDelay, "maximum retry delay")
	f.StringVar(&flags.resume, "resume", string(syncResumeAuto), "checkpoint policy: auto|require|never")
	f.StringVar(&flags.progress, "progress", string(syncProgressAuto), "progress mode: auto|plain|none|jsonl")
}

func (flags *syncCommonFlags) options() (syncSelector, []syncPathMap, error) {
	selector := syncSelector{IncludeProfiles: append([]string(nil), flags.includeProfiles...), IncludePaths: append([]string(nil), flags.includePaths...), IncludeKeys: append([]string(nil), flags.includeKeys...), ExcludeProfiles: append([]string(nil), flags.excludeProfiles...), ExcludePaths: append([]string(nil), flags.excludePaths...), ExcludeKeys: append([]string(nil), flags.excludeKeys...), Shared: syncSharedMode(flags.shared)}
	if _, _, err := normalizeSyncSelector(selector); err != nil {
		return syncSelector{}, nil, err
	}
	maps := make([]syncPathMap, 0, len(flags.pathMaps))
	for _, raw := range flags.pathMaps {
		mapping, err := parseSyncPathMap(raw)
		if err != nil {
			return syncSelector{}, nil, err
		}
		maps = append(maps, mapping)
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		return syncSelector{}, nil, err
	}
	policy := syncConflictPolicy(flags.onConflict)
	if policy == "" {
		policy = syncConflictFail
	}
	if err := validateSyncConflictPolicy(policy); err != nil {
		return syncSelector{}, nil, err
	}
	if _, err := parseSyncResolutions(flags.resolutions); err != nil {
		return syncSelector{}, nil, err
	}
	if mode := syncDeleteMode(flags.deleteMode); mode != syncDeleteAuto && mode != syncDeleteKeep && mode != syncDeleteConfirm {
		return syncSelector{}, nil, fmt.Errorf("unsupported sync deletion mode %q", mode)
	}
	if err := validateSyncRetryPolicy(syncRetryPolicy{MaxRetries: flags.maxRetries, MaxDelay: flags.retryMaxDelay, InitialDelay: defaultSyncRetryPolicy().InitialDelay, TotalBudget: defaultSyncRetryPolicy().TotalBudget}); err != nil {
		return syncSelector{}, nil, err
	}
	if mode := syncResumeMode(flags.resume); mode != syncResumeAuto && mode != syncResumeRequire && mode != syncResumeNever {
		return syncSelector{}, nil, fmt.Errorf("unsupported sync resume mode %q", mode)
	}
	if mode := syncProgressMode(flags.progress); mode != syncProgressAuto && mode != syncProgressPlain && mode != syncProgressNone && mode != syncProgressJSONL {
		return syncSelector{}, nil, fmt.Errorf("unsupported sync progress mode %q", mode)
	}
	if err := validateBWSTransportSelection(bwsTransportMode(flags.transport), flags.allowArgv); err != nil {
		return syncSelector{}, nil, err
	}
	return selector, maps, nil
}

func (flags *syncCommonFlags) runtime(projectID string) syncMaintenanceRuntimeOptions {
	return syncMaintenanceRuntimeOptions{Config: bwsConfigOptions{ConfigFile: flags.bwsConfigFile, Profile: flags.bwsProfile, ServerURL: flags.bwsServerURL, ProjectID: projectID}, Transport: bwsTransportMode(flags.transport), AllowArgv: flags.allowArgv}
}

func (flags *syncCommonFlags) completionRequested(command *cobra.Command) bool {
	if command.Flags().Changed("yes") {
		return true
	}
	for _, name := range []string{
		"select-profile", "select-path", "select-key", "exclude-profile", "exclude-path", "exclude-key", "shared", "map-path",
		"on-conflict", "resolve", "delete", "bws-config-file", "bws-profile", "bws-server-url", "bws-transport", "allow-secret-argv",
		"max-retries", "retry-max-delay", "resume", "progress",
	} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func newSyncBootstrapCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	common := newSyncCommonFlags()
	options := syncBootstrapOptions{}
	command := &cobra.Command{Use: "bootstrap", Short: "Preview or bootstrap from another machine namespace", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		selector, maps, err := common.options()
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid bootstrap options.", err)
		}
		options.Selector, options.PathMaps = selector, maps
		if options.Provider != supportedSyncProvider || !isValidMachineID(options.FromMachineID) {
			return newCLIError(exitCodePolicyFailed, "Bootstrap requires --provider=bws and a valid --from-machine-id.", nil)
		}
		policy := syncConflictPolicy(common.onConflict)
		if policy == "" {
			policy = syncConflictFail
		}
		options.ConflictPolicy = policy
		options.Resolutions, _ = parseSyncResolutions(common.resolutions)
		if err := preflight(); err != nil {
			return err
		}
		return runSyncBootstrap(ctx.opts, options, ctx.stdin, ctx.stdout, ctx.stderr)
	}}
	command.Flags().StringVar(&options.Provider, "provider", "", "sync provider (required; bws)")
	command.Flags().StringVar(&options.ProjectID, "project-id", "", "BWS project id")
	command.Flags().StringVar(&options.FromMachineID, "from-machine-id", "", "source machine id")
	command.Flags().BoolVar(&options.Merge, "merge", false, "merge into a non-empty target")
	command.Flags().BoolVarP(&options.Yes, "yes", "y", false, "apply the pinned bootstrap plan")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	common.bind(command)
	return command
}

func newSyncStatusCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	common := newSyncCommonFlags()
	options := syncStatusOptions{}
	command := &cobra.Command{Use: "status", Short: "Show sync state and optional provider drift", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		selector, _, err := common.options()
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid status options.", err)
		}
		options.Selector = selector
		if (options.Provider != "" && options.Provider != supportedSyncProvider) || (options.Online && options.Provider != supportedSyncProvider) {
			return newCLIError(exitCodePolicyFailed, "Online status requires --provider=bws.", nil)
		}
		if err := preflight(); err != nil {
			return err
		}
		return runSyncStatus(ctx.opts, options, common.runtime(""), ctx.stdin, ctx.stdout, ctx.stderr)
	}}
	command.Flags().StringVar(&options.Provider, "provider", "", "sync provider (bws)")
	command.Flags().BoolVar(&options.Online, "online", false, "include provider drift")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	common.bind(command)
	return command
}

func newSyncResetCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	common := newSyncCommonFlags()
	options := syncResetOptions{}
	command := &cobra.Command{Use: "reset", Short: "Preview or reset encrypted sync history", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		selector, _, err := common.options()
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid reset options.", err)
		}
		options.Selector = selector
		if options.Provider != "" && options.Provider != supportedSyncProvider {
			return newCLIError(exitCodePolicyFailed, "Reset supports only --provider=bws.", nil)
		}
		if err := preflight(); err != nil {
			return err
		}
		return runSyncReset(ctx.opts, options, ctx.stdin, ctx.stdout, ctx.stderr)
	}}
	command.Flags().StringVar(&options.Provider, "provider", "", "sync provider (bws)")
	command.Flags().BoolVar(&options.Baseline, "baseline", false, "reset selected baseline")
	command.Flags().BoolVar(&options.Checkpoint, "checkpoint", false, "reset checkpoint")
	command.Flags().BoolVarP(&options.Yes, "yes", "y", false, "apply the reset")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	common.bind(command)
	return command
}

func newSyncReconcileCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	common := newSyncCommonFlags()
	options := syncReconcileOptions{}
	projectID := ""
	command := &cobra.Command{Use: "reconcile", Short: "Preview or reconcile exact local/remote matches", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		selector, _, err := common.options()
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid reconcile options.", err)
		}
		options.Selector = selector
		if options.Provider != supportedSyncProvider {
			return newCLIError(exitCodePolicyFailed, "Reconcile requires --provider=bws.", nil)
		}
		if err := preflight(); err != nil {
			return err
		}
		return runSyncReconcile(ctx.opts, options, common.runtime(projectID), ctx.stdin, ctx.stdout, ctx.stderr)
	}}
	command.Flags().StringVar(&options.Provider, "provider", "", "sync provider (required; bws)")
	command.Flags().StringVar(&projectID, "project-id", "", "BWS project id")
	command.Flags().BoolVar(&options.UpgradeMetadata, "upgrade-metadata", false, "replace v1 remote metadata with v2 metadata")
	command.Flags().BoolVarP(&options.Yes, "yes", "y", false, "apply the reconcile plan")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	common.bind(command)
	return command
}

func newSyncPruneCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	common := newSyncCommonFlags()
	options := syncPruneOptions{}
	projectID := ""
	command := &cobra.Command{Use: "prune", Short: "Preview or prune proven remote orphans", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		selector, _, err := common.options()
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid prune options.", err)
		}
		options.Selector = selector
		if options.Provider != supportedSyncProvider {
			return newCLIError(exitCodePolicyFailed, "Prune requires --provider=bws.", nil)
		}
		if err := preflight(); err != nil {
			return err
		}
		return runSyncPrune(ctx.opts, options, common.runtime(projectID), ctx.stdin, ctx.stdout, ctx.stderr)
	}}
	command.Flags().StringVar(&options.Provider, "provider", "", "sync provider (required; bws)")
	command.Flags().StringVar(&projectID, "project-id", "", "BWS project id")
	command.Flags().StringVar(&options.MachineID, "machine-id", "", "machine namespace to prune")
	command.Flags().StringVar(&options.AckRetiredMachine, "ack-retired-machine", "", "acknowledge exact retired machine id")
	command.Flags().StringArrayVar(&options.SecretIDs, "secret-id", nil, "exact immutable BWS secret id (repeatable)")
	command.Flags().BoolVar(&options.AckMalformed, "ack-malformed", false, "acknowledge malformed or duplicate metadata")
	command.Flags().BoolVar(&options.PruneEmptyScopes, "prune-empty-scopes", false, "remove selected empty sync-created vault scopes")
	command.Flags().BoolVarP(&options.Yes, "yes", "y", false, "apply the prune plan")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	common.bind(command)
	return command
}

func parseSyncResolutions(values []string) (map[string]syncResolution, error) {
	result := map[string]syncResolution{}
	for _, value := range values {
		id, raw, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("invalid resolution %q", value)
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("duplicate resolution for %q", id)
		}
		resolution := syncResolution(raw)
		if err := validateSyncResolution(resolution); err != nil {
			return nil, err
		}
		result[id] = resolution
	}
	return result, nil
}

func newDoctorCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	options := doctorBWSOptions{}
	command := &cobra.Command{
		Use:   cmdDoctor,
		Short: "Run local diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if (options.Online || options.CheckWrite || options.Yes || options.JSON) && options.Provider != supportedSyncProvider {
				return newCLIError(exitCodePolicyFailed, "Doctor provider flags require --provider=bws.", nil)
			}
			if err := preflight(); err != nil {
				return err
			}
			if options.Provider != "" || options.Online || options.CheckWrite || options.Yes || options.JSON {
				return runDoctorBWS(ctx.opts, options, ctx.stdin, ctx.stdout, ctx.stderr)
			}
			return runDoctor(ctx.opts, ctx.stdout)
		},
	}
	command.Flags().StringVar(&options.Provider, "provider", "", "provider diagnostics (bws)")
	command.Flags().BoolVar(&options.Online, "online", false, "check provider authentication and read access")
	command.Flags().BoolVar(&options.CheckWrite, "check-write", false, "test value-safe provider write capability")
	command.Flags().BoolVarP(&options.Yes, "yes", "y", false, "confirm the write canary")
	command.Flags().BoolVar(&options.JSON, "json", false, "emit JSON output")
	return command
}
