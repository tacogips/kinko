package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type syncMaintenanceRuntimeOptions struct {
	Config    bwsConfigOptions
	Transport bwsTransportMode
	AllowArgv bool
}

type lockedSyncSnapshot struct {
	DataDir  string
	Meta     *vaultMeta
	Data     *vaultData
	Config   map[string]string
	Envelope syncStateEnvelope
	DEK      []byte
}

func withLockedSyncSnapshot(opts globalOptions, stdin io.Reader, stderr io.Writer, operation func(*lockedSyncSnapshot) error) error {
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
	data, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Vault data could not be loaded for sync maintenance.", err)
	}
	config, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		return syncLocalDataError("Encrypted config could not be loaded for sync maintenance.", err)
	}
	envelope := syncStateEnvelope{}
	if encoded := config[configKeyBWSSyncState]; encoded != "" {
		envelope, err = decodeBWSSyncState(encoded)
		if err != nil {
			return newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state is invalid.", err)
		}
	}
	return operation(&lockedSyncSnapshot{DataDir: opts.dataDir, Meta: meta, Data: data, Config: config, Envelope: envelope, DEK: dek})
}

func maintenanceBWSProvider(snapshot *lockedSyncSnapshot, options syncMaintenanceRuntimeOptions, stderr io.Writer) (syncProvider, bwsRuntimeConfig, error) {
	runtime, err := resolveBWSRuntimeConfig(options.Config, snapshot.Config, os.Getenv)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	if runtime.ProjectID == "" {
		return nil, bwsRuntimeConfig{}, errors.New("BWS project id is required for online sync maintenance")
	}
	token, err := resolveBWSAccessToken(os.Getenv, snapshot.Data.Shared, stderr)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	cliProvider, err := newBWSCLIAdapter(token, runtime, stderr)
	if err != nil {
		return nil, bwsRuntimeConfig{}, providerCLIError("BWS provider is unavailable.", err)
	}
	provider, err := selectBWSTransport(options.Transport, options.AllowArgv, nil, cliProvider)
	if err != nil {
		return nil, bwsRuntimeConfig{}, err
	}
	return provider, runtime, nil
}

func maintenancePlanContext(runtime bwsRuntimeConfig, machineID string) syncPlanContext {
	return syncPlanContext{ProviderIdentity: runtime.ProviderIdentity, Endpoint: endpointString(runtime.Endpoints.APIURL), OrganizationID: runtime.OrganizationID, ProjectID: runtime.ProjectID, MachineID: machineID}
}

func persistFullSyncState(dataDir string, dek []byte, config map[string]string, envelope syncStateEnvelope, state *bwsSyncStateV2) error {
	selected := make(map[string]struct{}, len(state.Entries)+len(state.Ownership))
	for id := range state.Entries {
		selected[id] = struct{}{}
	}
	for id := range state.Ownership {
		selected[id] = struct{}{}
	}
	encoded, err := mergeSelectedBWSSyncState(envelope, state, selected)
	if err != nil {
		return err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &root); err != nil {
		return err
	}
	entries := make(map[string]json.RawMessage, len(state.Entries))
	for id, entry := range state.Entries {
		encodedEntry, encodeErr := encodeSyncStateEntryV2(entry)
		if encodeErr != nil {
			return fmt.Errorf("encode full sync state entry %q: %w", id, encodeErr)
		}
		entries[id] = encodedEntry
	}
	entriesJSON, err := marshalRawObject(entries)
	if err != nil {
		return err
	}
	root["entries"] = entriesJSON
	ownership, err := json.Marshal(state.Ownership)
	if err != nil {
		return err
	}
	root["ownership"] = ownership
	encodedRaw, err := marshalRawObject(root)
	if err != nil {
		return err
	}
	next := cloneStringMap(config)
	next[configKeyBWSSyncState] = string(encodedRaw)
	if err := saveConfig(dataDir, dek, next); err != nil {
		return err
	}
	config[configKeyBWSSyncState] = string(encodedRaw)
	return nil
}

func printSyncStatusResult(writer io.Writer, result syncStatusResult, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(result)
	}
	_, err := fmt.Fprintf(writer, "status online=%t formats=%v baseline=%s checkpoint=%s drift=%d selector=%s\n", result.Online, result.Formats, result.BaselineHealth, result.CheckpointHealth, len(result.Drift), result.SelectorDigest)
	return err
}

func printSyncMaintenancePlan(writer io.Writer, plan *syncPlanV2, applied, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(writer).Encode(struct {
			*syncPlanV2
			Applied bool `json:"applied"`
		}{syncPlanV2: plan, Applied: applied})
	}
	mode := "preview"
	if applied {
		mode = "applied"
	}
	_, err := fmt.Fprintf(writer, "%s=%s actions=%d conflicts=%d plan=%s\n", plan.Operation, mode, len(plan.Actions), len(plan.Conflicts), plan.PlanDigest)
	return err
}

func runSyncStatus(opts globalOptions, options syncStatusOptions, runtimeOptions syncMaintenanceRuntimeOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		entries, err := collectSyncEntries(snapshot.Data)
		if err != nil {
			return err
		}
		maps, err := resolveSyncPathMaps(snapshot.Config, nil)
		if err != nil {
			return err
		}
		var remote []bwsSecret
		planContext := syncPlanContext{}
		if options.Online {
			provider, runtime, err := maintenanceBWSProvider(snapshot, runtimeOptions, stderr)
			if err != nil {
				return err
			}
			remote, err = provider.ListSecrets(context.Background(), runtime.ProjectID)
			if err != nil {
				return providerCLIError("Could not list BWS secrets for status.", err)
			}
			planContext = maintenancePlanContext(runtime, snapshot.Meta.MachineID)
		}
		result, err := buildSyncStatusResult(entries, remote, snapshot.Envelope, options, planContext, maps)
		if err != nil {
			return err
		}
		return printSyncStatusResult(stdout, result, options.JSON)
	})
}

func runSyncReset(opts globalOptions, options syncResetOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		plan, err := buildSyncResetPlan(snapshot.Envelope, options)
		if err != nil {
			return err
		}
		if !options.Yes {
			return printSyncMaintenancePlan(stdout, plan, false, options.JSON)
		}
		if err := applySyncReset(opts.dataDir, snapshot.DEK, snapshot.Config, plan); err != nil {
			return err
		}
		return printSyncMaintenancePlan(stdout, plan, true, options.JSON)
	})
}

func runSyncReconcile(opts globalOptions, options syncReconcileOptions, runtimeOptions syncMaintenanceRuntimeOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		provider, runtime, err := maintenanceBWSProvider(snapshot, runtimeOptions, stderr)
		if err != nil {
			return err
		}
		remote, err := provider.ListSecrets(context.Background(), runtime.ProjectID)
		if err != nil {
			return err
		}
		entries, err := collectSyncEntries(snapshot.Data)
		if err != nil {
			return err
		}
		state, err := decodeBWSSyncStateV2(snapshot.Envelope)
		if err != nil {
			return err
		}
		contextValue := maintenancePlanContext(runtime, snapshot.Meta.MachineID)
		state.Context = &contextValue
		temporaryEnvelope := maintenanceEnvelopeFromState(snapshot.Envelope, state)
		plan, err := buildSyncReconcilePlan(entries, remote, temporaryEnvelope, options)
		if err != nil {
			return err
		}
		if !options.Yes || len(plan.Conflicts) > 0 {
			return printSyncMaintenancePlan(stdout, plan, false, options.JSON)
		}
		persist := func(current *bwsSyncStateV2) error {
			return persistFullSyncState(opts.dataDir, snapshot.DEK, snapshot.Config, snapshot.Envelope, current)
		}
		if options.UpgradeMetadata {
			err = applySyncMetadataUpgradeDurable(context.Background(), provider, plan, state, persist)
		} else {
			err = applySyncReconcile(context.Background(), provider, plan, state)
			if err == nil {
				err = persist(state)
			}
		}
		if err != nil {
			return err
		}
		return printSyncMaintenancePlan(stdout, plan, true, options.JSON)
	})
}

func runSyncPrune(opts globalOptions, options syncPruneOptions, runtimeOptions syncMaintenanceRuntimeOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		provider, runtime, err := maintenanceBWSProvider(snapshot, runtimeOptions, stderr)
		if err != nil {
			return err
		}
		remote, err := provider.ListSecrets(context.Background(), runtime.ProjectID)
		if err != nil {
			return err
		}
		state, err := decodeBWSSyncStateV2(snapshot.Envelope)
		if err != nil {
			return err
		}
		contextValue := maintenancePlanContext(runtime, snapshot.Meta.MachineID)
		state.Context = &contextValue
		temporaryEnvelope := maintenanceEnvelopeFromState(snapshot.Envelope, state)
		plan, err := buildSyncPrunePlan(remote, temporaryEnvelope, snapshot.Data, options)
		if err != nil {
			return err
		}
		if !options.Yes {
			return printSyncMaintenancePlan(stdout, plan, false, options.JSON)
		}
		if err := applySyncPrune(context.Background(), provider, plan, snapshot.Data, state); err != nil {
			return err
		}
		if err := saveVault(opts.dataDir, snapshot.DEK, snapshot.Data); err != nil {
			return err
		}
		if err := persistFullSyncState(opts.dataDir, snapshot.DEK, snapshot.Config, snapshot.Envelope, state); err != nil {
			return err
		}
		return printSyncMaintenancePlan(stdout, plan, true, options.JSON)
	})
}

func maintenanceEnvelopeFromState(base syncStateEnvelope, state *bwsSyncStateV2) syncStateEnvelope {
	selected := map[string]struct{}{}
	for id := range state.Entries {
		selected[id] = struct{}{}
	}
	encoded, err := mergeSelectedBWSSyncState(base, state, selected)
	if err != nil {
		return base
	}
	envelope, err := decodeBWSSyncState(encoded)
	if err != nil {
		return base
	}
	return envelope
}
