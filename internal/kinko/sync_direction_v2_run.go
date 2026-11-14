package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

type syncDirectionV2Options struct {
	Direction      syncDirection
	Provider       string
	ProjectID      string
	Force          bool
	DryRun         bool
	JSON           bool
	Yes            bool
	Selector       syncSelector
	PathMaps       []syncPathMap
	ConflictPolicy syncConflictPolicy
	Resolutions    map[string]syncResolution
	DeleteMode     syncDeleteMode
	Runtime        syncMaintenanceRuntimeOptions
	Retry          syncRetryPolicy
	Resume         syncResumeMode
	Progress       syncProgressMode
}

type directionV2CheckpointStore struct {
	snapshot *lockedSyncSnapshot
	state    *bwsSyncStateV2
}

func (store directionV2CheckpointStore) Save(checkpoint *syncCheckpoint) error {
	working := cloneBWSSyncStateV2(store.state)
	working.Checkpoint = cloneSyncCheckpoint(checkpoint)
	base := store.snapshot.Envelope
	if base.Format == 0 {
		base = syncStateEnvelope{Format: bwsSyncStateFormatV2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	}
	return persistFullSyncState(store.snapshot.DataDir, store.snapshot.DEK, store.snapshot.Config, base, working)
}

func runSyncDirectionV2(opts globalOptions, options syncDirectionV2Options, stdin io.Reader, stdout, stderr io.Writer) error {
	if options.Provider != supportedSyncProvider {
		return newCLIError(exitCodePolicyFailed, "Completion sync requires --provider=bws.", errors.New("unsupported sync provider"))
	}
	return withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		provider, runtime, err := directionV2Provider(snapshot, options.Runtime, stderr)
		if err != nil {
			return err
		}
		return executeSyncDirectionV2(snapshot, provider, runtime, options, stdout, stderr)
	})
}

func directionV2Provider(snapshot *lockedSyncSnapshot, options syncMaintenanceRuntimeOptions, stderr io.Writer) (syncProvider, bwsRuntimeConfig, error) {
	runtime, err := resolveBWSRuntimeConfig(options.Config, snapshot.Config, os.Getenv)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	token, err := resolveBWSAccessToken(os.Getenv, snapshot.Data.Shared, stderr)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	cliProvider, err := newBWSCLIAdapter(token, runtime, stderr)
	if err != nil {
		return nil, bwsRuntimeConfig{}, providerCLIError("BWS provider is unavailable.", err)
	}
	if runtime.ProjectID == "" {
		projects, listErr := cliProvider.ListProjects(context.Background())
		if listErr != nil {
			return nil, bwsRuntimeConfig{}, providerCLIError("Could not list BWS projects.", listErr)
		}
		if len(projects) != 1 {
			return nil, bwsRuntimeConfig{}, newCLIError(exitCodePolicyFailed, "BWS project id is required unless exactly one project is accessible.", nil)
		}
		options.Config.ProjectID = projects[0].ID
		runtime, err = resolveBWSRuntimeConfig(options.Config, snapshot.Config, os.Getenv)
		if err != nil {
			return nil, bwsRuntimeConfig{}, err
		}
	}
	secureProvider, _ := newBWSSDKProvider()
	provider, err := selectBWSTransport(options.Transport, options.AllowArgv, secureProvider, cliProvider)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	return provider, runtime, nil
}

func executeSyncDirectionV2(snapshot *lockedSyncSnapshot, provider syncProvider, runtime bwsRuntimeConfig, options syncDirectionV2Options, stdout, stderr io.Writer) error {
	if snapshot == nil || snapshot.Meta == nil || snapshot.Data == nil || snapshot.Config == nil || provider == nil {
		return errors.New("completion sync runtime is not initialized")
	}
	operation := syncOperationPush
	if options.Direction == syncDirectionPull {
		operation = syncOperationPull
	} else if options.Direction != syncDirectionPush {
		return newCLIError(exitCodePolicyFailed, "Sync direction must be push or pull.", nil)
	}
	pathMaps, err := resolveSyncPathMaps(snapshot.Config, options.PathMaps)
	if err != nil {
		return err
	}
	pathMapDigest, err := bootstrapPathMapDigest(pathMaps)
	if err != nil {
		return err
	}
	planContext := maintenancePlanContext(runtime, snapshot.Meta.MachineID)
	planContext.PathMapDigest = pathMapDigest
	planContext.PathMaps = pathMaps
	entries, err := collectSyncEntries(snapshot.Data)
	if err != nil {
		return err
	}
	remote, err := provider.ListSecrets(context.Background(), runtime.ProjectID)
	if err != nil {
		return providerCLIError("Could not list BWS secrets.", err)
	}
	plan, err := buildSyncPlanV2WithContext(operation, entries, remote, snapshot.Envelope, options.Selector, planContext)
	if err != nil {
		return newCLIError(exitCodePolicyFailed, "Could not build the completion sync plan.", err)
	}
	if err := applySyncConflictRules(plan, options.ConflictPolicy, options.Resolutions, options.Force); err != nil {
		return newCLIError(exitCodePolicyFailed, "Invalid sync conflict resolution.", err)
	}
	if err := applySyncDeletionPolicy(plan, options.DeleteMode, options.Yes); err != nil {
		return newCLIError(exitCodePolicyFailed, "Invalid sync deletion policy.", err)
	}
	if options.DryRun || len(plan.Conflicts) != 0 {
		if err := printSyncV2Preview(stdout, plan, options.JSON); err != nil {
			return newCLIError(exitCodeIOFailed, "Could not write completion sync preview.", err)
		}
		if len(plan.Conflicts) != 0 {
			return newCLIError(exitCodeSyncConflict, "Synchronization has unresolved conflicts.", errors.New("unresolved sync conflict"))
		}
		return nil
	}
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}}
	if snapshot.Envelope.Format != 0 {
		state.Entries, err = stateEntriesForPlan(snapshot.Envelope, planContext)
		if err != nil {
			return err
		}
		state.Ownership, err = ownershipRecordsForPlan(snapshot.Envelope, planContext)
		if err != nil {
			return err
		}
		if snapshot.Envelope.Format == bwsSyncStateFormatV2 {
			existing, decodeErr := decodeBWSSyncStateV2(snapshot.Envelope)
			if decodeErr != nil {
				return decodeErr
			}
			state.Checkpoint, state.MetadataUpgrade, state.Raw = existing.Checkpoint, existing.MetadataUpgrade, existing.Raw
		}
	}
	state.Context = &planContext
	progress := syncProgressSink(discardSyncProgress{})
	mode := options.Progress
	if mode == syncProgressAuto {
		mode = syncProgressPlain
	}
	if mode != syncProgressNone {
		progress = syncWriterProgress{mode: mode, writer: stderr}
	}
	result, err := executeSyncPlanV2WithOptions(context.Background(), provider, plan, snapshot.Data, state, progress, syncExecutionOptions{
		Resume: options.Resume, RetryPolicy: options.Retry, Clock: realSyncClock{}, RetryClassifier: defaultSyncRetryClassifier{},
		Checkpoints: directionV2CheckpointStore{snapshot: snapshot, state: state}, PathMaps: pathMaps,
	})
	if err != nil {
		_ = printSyncSummary(stdout, result, options.JSON)
		return providerCLIError("BWS completion sync did not complete.", err)
	}
	if operation == syncOperationPull {
		if err := saveVault(snapshot.DataDir, snapshot.DEK, snapshot.Data); err != nil {
			return newCLIError(exitCodeIOFailed, "Could not save the synchronized vault.", err)
		}
	}
	baseEnvelope := snapshot.Envelope
	if baseEnvelope.Format == 0 {
		baseEnvelope = syncStateEnvelope{Format: bwsSyncStateFormatV2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	}
	if err := persistFullSyncState(snapshot.DataDir, snapshot.DEK, snapshot.Config, baseEnvelope, state); err != nil {
		return newCLIError(exitCodeIOFailed, "Could not save encrypted completion sync state.", err)
	}
	return printSyncSummary(stdout, result, options.JSON)
}

func printSyncV2Preview(writer io.Writer, plan *syncPlanV2, jsonOutput bool) error {
	result := syncResult{Actions: []syncResultItem{}, Conflicts: []string{}}
	for _, action := range plan.Actions {
		appendV2Result(&result, action)
	}
	for _, conflict := range plan.Conflicts {
		result.Conflicts = append(result.Conflicts, conflict.EntryID)
	}
	return printSyncSummary(writer, result, jsonOutput)
}
