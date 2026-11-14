package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type syncDirection string

const (
	syncDirectionPush syncDirection = "push"
	syncDirectionPull syncDirection = "pull"
)

type syncOptions struct {
	direction syncDirection
	provider  string
	force     bool
	dryRun    bool
	projectID string
	jsonOut   bool
}

const (
	envKinkoBWSProjectID  = "KINKO_BWS_PROJECT_ID"
	configKeyBWSProjectID = "sync.bws.project_id"
	supportedSyncProvider = "bws"
)

func resolveBWSProjectID(ctx context.Context, client *bwsClient, config map[string]string, flagValue string, stderr io.Writer) (string, error) {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv(envKinkoBWSProjectID)); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(config[configKeyBWSProjectID]); value != "" {
		return value, nil
	}
	projects, err := client.listProjects(ctx)
	if err != nil {
		return "", providerCLIError("Could not list BWS projects.", err)
	}
	if len(projects) == 1 {
		_, _ = fmt.Fprintf(stderr, "NOTICE: using the only accessible BWS project %s.\n", projects[0].ID)
		return projects[0].ID, nil
	}
	return "", newCLIError(
		exitCodePolicyFailed,
		fmt.Sprintf("BWS project id is required from --project-id, %s, config key %s, or an access token with exactly one project.", envKinkoBWSProjectID, configKeyBWSProjectID),
		errors.New("BWS project id is not configured"),
	)
}

func runSyncWithOptions(opts globalOptions, syncOpts syncOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if syncOpts.provider == "" {
		return newCLIError(exitCodePolicyFailed, "--provider is required; supported provider: bws.", errors.New("sync provider is required"))
	}
	if syncOpts.provider != supportedSyncProvider {
		return newCLIError(exitCodePolicyFailed, fmt.Sprintf("Unsupported sync provider %q; supported provider: bws.", syncOpts.provider), errors.New("unsupported sync provider"))
	}
	if syncOpts.direction != syncDirectionPush && syncOpts.direction != syncDirectionPull {
		return newCLIError(exitCodePolicyFailed, "Sync direction must be push or pull.", errors.New("invalid sync direction"))
	}
	initialMeta, err := loadSyncMetadata(opts.dataDir)
	if err != nil {
		return err
	}
	if !isValidMachineID(initialMeta.MachineID) {
		return newCLIError(exitCodePolicyFailed, "Vault machine id is missing or invalid; run 'kinko migration --yes'.", errors.New("machine id unavailable"))
	}

	dek, _, err := verifyVaultPasswordDEKForShow(opts, stdin, stderr, "Re-enter password: ")
	if err != nil {
		return syncPasswordError(err)
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Vault mutation is already in progress.", err)
	}
	defer release()

	meta, err := loadSyncMetadata(opts.dataDir)
	if err != nil {
		return err
	}
	if !isValidMachineID(meta.MachineID) {
		return newCLIError(exitCodePolicyFailed, "Vault machine id is missing or invalid; run 'kinko migration --yes'.", errors.New("machine id unavailable"))
	}
	data, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Vault data could not be loaded for sync.", err)
	}
	config, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Encrypted config could not be loaded for sync.", err)
	}
	state, err := loadBWSSyncState(config)
	if err != nil {
		return err
	}
	if state.MachineID != "" && state.MachineID != meta.MachineID {
		_, _ = fmt.Fprintln(stderr, "WARNING: encrypted BWS sync state belongs to another machine id; treating this vault as never synced.")
		state = emptyBWSSyncState()
	}

	entries, err := collectSyncEntries(data)
	if err != nil {
		return err
	}
	token, err := resolveBWSAccessToken(os.Getenv, data.Shared, stderr)
	if err != nil {
		return err
	}
	client, err := newBWSClient(token, stderr)
	if err != nil {
		return providerCLIError("BWS provider is unavailable.", err)
	}
	ctx := context.Background()
	projectID, err := resolveBWSProjectID(ctx, client, config, syncOpts.projectID, stderr)
	if err != nil {
		return err
	}
	if state.ProjectID != "" && state.ProjectID != projectID {
		_, _ = fmt.Fprintf(stderr, "WARNING: BWS project changed from %s to %s; treating this project as never synced.\n", state.ProjectID, projectID)
		state = emptyBWSSyncState()
	}
	remote, err := client.listSecrets(ctx, projectID)
	if err != nil {
		return providerCLIError("Could not list BWS secrets.", err)
	}

	var plan *syncPlan
	if syncOpts.direction == syncDirectionPush {
		plan, err = buildPushPlan(entries, remote, state, meta.MachineID)
	} else {
		plan, err = buildPullPlan(entries, remote, state, meta.MachineID)
	}
	if err != nil {
		return err
	}
	preview, previewErr := prepareSyncResult(plan, syncOpts.force)
	if syncOpts.dryRun || previewErr != nil {
		if err := printSyncSummary(stdout, preview, syncOpts.jsonOut); err != nil {
			return newCLIError(exitCodeIOFailed, "Could not write sync output.", err)
		}
		return previewErr
	}

	state.MachineID = meta.MachineID
	state.ProjectID = projectID
	if state.Entries == nil {
		state.Entries = map[string]syncStateEntry{}
	}
	var result syncResult
	if syncOpts.direction == syncDirectionPush {
		result, err = applyPushPlan(ctx, client, projectID, plan, state, syncOpts.force)
		if saveErr := persistSyncState(opts.dataDir, dek, config, state); saveErr != nil {
			// applyPushPlan may already have mutated BWS (created, updated,
			// or deleted secrets) before persisting the new state failed, so
			// the user still needs to see which entries were applied.
			_ = printSyncSummary(stdout, result, syncOpts.jsonOut)
			if err != nil {
				return errors.Join(saveErr, providerCLIError("BWS sync did not complete.", err))
			}
			return saveErr
		}
	} else {
		result, err = applyPullPlan(data, plan, state, syncOpts.force)
		if err == nil {
			if saveErr := saveVault(opts.dataDir, dek, data); saveErr != nil {
				return newCLIError(exitCodeIOFailed, "Could not save the synchronized vault.", saveErr)
			}
			if saveErr := persistSyncState(opts.dataDir, dek, config, state); saveErr != nil {
				// The vault file was already saved with the synchronized
				// data before persisting the new sync state failed, so the
				// user still needs to see which entries were applied.
				_ = printSyncSummary(stdout, result, syncOpts.jsonOut)
				return saveErr
			}
		}
	}
	if outputErr := printSyncSummary(stdout, result, syncOpts.jsonOut); outputErr != nil {
		return newCLIError(exitCodeIOFailed, "Could not write sync output.", outputErr)
	}
	if err != nil {
		return providerCLIError("BWS sync did not complete.", err)
	}
	return nil
}

func persistSyncState(dataDir string, dek []byte, config map[string]string, state *bwsSyncState) error {
	if err := saveBWSSyncState(config, state); err != nil {
		return newCLIError(exitCodeMetadataInvalid, "Could not encode BWS sync state.", err)
	}
	if err := saveConfig(dataDir, dek, config); err != nil {
		return newCLIError(exitCodeIOFailed, "Could not save encrypted BWS sync state.", err)
	}
	return nil
}

func printSyncSummary(writer io.Writer, result syncResult, jsonOutput bool) error {
	if jsonOutput {
		if result.Conflicts == nil {
			result.Conflicts = []string{}
		}
		if result.Actions == nil {
			result.Actions = []syncResultItem{}
		}
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(result)
	}
	for _, action := range result.Actions {
		label := action.Scope
		if action.Scope == string(scopeKindPath) {
			label = fmt.Sprintf("profile=%q path=%q", action.Profile, action.Path)
		}
		if action.Reason == "" {
			_, _ = fmt.Fprintf(writer, "%s %s / %s\n", action.Action, label, action.Key)
		} else {
			_, _ = fmt.Fprintf(writer, "%s %s / %s: %s\n", action.Action, label, action.Key, action.Reason)
		}
	}
	_, err := fmt.Fprintf(writer, "created=%d updated=%d deleted=%d adopted=%d unchanged=%d conflicts=%d partial=%t\n", result.Created, result.Updated, result.Deleted, result.Adopted, result.Unchanged, len(result.Conflicts), result.Partial)
	return err
}

func providerCLIError(message string, err error) error {
	if err == nil {
		return nil
	}
	diagnostic := strings.TrimSpace(err.Error())
	if diagnostic != "" {
		message = strings.TrimSpace(message) + " " + diagnostic
	}
	return newCLIError(exitCodeProviderFailed, message, err)
}

func syncPasswordError(err error) error {
	if err == nil {
		return nil
	}
	if code := ExitCode(err); code != 1 {
		return err
	}
	if strings.Contains(err.Error(), "password verification failed") {
		return newCLIError(exitCodeAuthFailed, "Vault password verification failed.", err)
	}
	return newCLIError(exitCodeIOFailed, "Could not read or verify the vault password.", err)
}

func loadSyncMetadata(dataDir string) (*vaultMeta, error) {
	meta, err := loadMeta(dataDir)
	if err != nil {
		return nil, syncLocalDataError("Vault metadata could not be loaded for sync.", err)
	}
	return meta, nil
}

func syncLocalDataError(message string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return newCLIError(exitCodeIOFailed, message, err)
	}
	return newCLIError(exitCodeMetadataInvalid, message, err)
}
