package kinko

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteSyncDirectionV2DryRunUsesPlannerWithoutSecretLeakage(t *testing.T) {
	dataDir, _, dek := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: "fixture-project"}, config, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta, Data: data, Config: config, DEK: dek}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	var stdout, stderr bytes.Buffer
	options := syncDirectionV2Options{
		Direction: syncDirectionPush, Provider: supportedSyncProvider, DryRun: true,
		Selector:       syncSelector{IncludeKeys: []string{"SHARED_KEY"}},
		ConflictPolicy: syncConflictFail, DeleteMode: syncDeleteAuto,
		Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone,
	}
	if err := executeSyncDirectionV2(snapshot, provider, runtime, options, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 0 || !strings.Contains(stdout.String(), "created=1") {
		t.Fatalf("creates=%d stdout=%q", provider.createCalls, stdout.String())
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
	if _, exists := config[configKeyBWSSyncState]; exists {
		t.Fatal("dry-run persisted sync state")
	}
}

func TestExecuteSyncDirectionV2PushPersistsCheckpointAndFormat2State(t *testing.T) {
	dataDir, _, dek := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: "fixture-project"}, config, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta, Data: data, Config: config, DEK: dek}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	var stdout, stderr bytes.Buffer
	options := syncDirectionV2Options{
		Direction: syncDirectionPush, Provider: supportedSyncProvider,
		Selector: syncSelector{IncludeKeys: []string{"SHARED_KEY"}}, ConflictPolicy: syncConflictFail,
		DeleteMode: syncDeleteAuto, Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone,
	}
	if err := executeSyncDirectionV2(snapshot, provider, runtime, options, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 1 || !strings.Contains(stdout.String(), "created=1") {
		t.Fatalf("creates=%d stdout=%q", provider.createCalls, stdout.String())
	}
	reloadedConfig, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(reloadedConfig[configKeyBWSSyncState])
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if state.Context == nil || state.Context.ProjectID != "fixture-project" || len(state.Entries) != 1 || state.Checkpoint == nil || state.Checkpoint.Phase != syncCheckpointComplete {
		t.Fatalf("persisted state=%+v", state)
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}

func TestExecuteSyncDirectionV2CompletedCheckpointFromDifferentPlanDoesNotBlockNextRun(t *testing.T) {
	dataDir, _, dek := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: "fixture-project"}, config, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta, Data: data, Config: config, DEK: dek}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	var pushStdout, pushStderr bytes.Buffer
	pushOptions := syncDirectionV2Options{
		Direction: syncDirectionPush, Provider: supportedSyncProvider,
		Selector: syncSelector{IncludeKeys: []string{"SHARED_KEY"}}, ConflictPolicy: syncConflictFail,
		DeleteMode: syncDeleteAuto, Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone,
	}
	if err := executeSyncDirectionV2(snapshot, provider, runtime, pushOptions, &pushStdout, &pushStderr); err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 1 {
		t.Fatalf("push creates=%d", provider.createCalls)
	}

	// Reload from disk the way a fresh CLI invocation would: the push above
	// persisted a complete-phase checkpoint pinned to the push plan. A
	// differently shaped pull run must not be blocked by that leftover
	// checkpoint (live repro: push completes, then pull fails preflight with
	// "sync checkpoint does not match the current pinned plan").
	meta2, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config2, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := config2[configKeyBWSSyncState]; !exists {
		t.Fatal("push did not persist sync state")
	}
	snapshot2 := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta2, Data: data2, Config: config2, DEK: dek}
	pullOptions := syncDirectionV2Options{
		Direction: syncDirectionPull, Provider: supportedSyncProvider,
		Selector: syncSelector{IncludeKeys: []string{"SHARED_KEY"}}, ConflictPolicy: syncConflictFail,
		DeleteMode: syncDeleteAuto, Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone,
	}
	var pullStdout, pullStderr bytes.Buffer
	if err := executeSyncDirectionV2(snapshot2, provider, runtime, pullOptions, &pullStdout, &pullStderr); err != nil {
		t.Fatalf("pull after a completed push checkpoint from a different plan failed: %v", err)
	}
	assertNoSyncFixtureLeak(t, pushStdout.String()+pushStderr.String()+pullStdout.String()+pullStderr.String())
}

// cliOnlyCapabilityTestProvider mimics the un-wrapped bwsCLIAdapter, which
// advertises read and delete but never value-safe-mutation on its own.
type cliOnlyCapabilityTestProvider struct {
	syncProvider
}

func (cliOnlyCapabilityTestProvider) Capabilities() map[syncCapability]bool {
	return map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true}
}

func TestExecuteSyncDirectionV2PushCreatesThroughCLILegacyTransport(t *testing.T) {
	dataDir, _, dek := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: "fixture-project"}, config, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta, Data: data, Config: config, DEK: dek}
	cliOnly := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	// Reproduce the runtime path an operator takes with the acknowledged
	// escape hatch: selectBWSTransport must grant value-safe-mutation to the
	// otherwise CLI-only provider before it reaches the executor.
	provider, err := selectBWSTransport(bwsTransportCLILegacy, true, nil, cliOnlyCapabilityTestProvider{syncProvider: cliOnly})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options := syncDirectionV2Options{
		Direction: syncDirectionPush, Provider: supportedSyncProvider,
		Selector: syncSelector{IncludeKeys: []string{"SHARED_KEY"}}, ConflictPolicy: syncConflictFail,
		DeleteMode: syncDeleteAuto, Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone,
	}
	if err := executeSyncDirectionV2(snapshot, provider, runtime, options, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if cliOnly.createCalls != 1 || !strings.Contains(stdout.String(), "created=1") {
		t.Fatalf("creates=%d stdout=%q", cliOnly.createCalls, stdout.String())
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}

func TestPrintSyncV2PreviewConflictExitDataIsValueFree(t *testing.T) {
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "KEY"}
	plan := &syncPlanV2{Format: 2, Operation: syncOperationPush, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("b", 64), Actions: []syncPlannedAction{{EntryID: syncEntryID(identity), Identity: identity, Kind: syncActionConflict, Reason: "diverged"}}, Conflicts: []syncConflict{{EntryID: syncEntryID(identity), Reason: "diverged", LocalPresent: true, RemotePresent: true}}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printSyncV2Preview(&out, plan, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "fixture-secret-value") || !strings.Contains(out.String(), `"conflicts":["`) {
		t.Fatalf("preview output=%q", out.String())
	}
}

func TestExecuteSyncDirectionV2PullMaterializesLogicalPathMap(t *testing.T) {
	dataDir, _, dek := setupEmptySyncE2EVault(t, syncTestMachineID)
	meta, _ := loadMeta(dataDir)
	data, _ := loadVault(dataDir, dek)
	config, _ := loadConfig(dataDir, dek)
	runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{ProjectID: "fixture-project"}, config, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mapping, err := parseSyncPathMap("work=" + root)
	if err != nil {
		t.Fatal(err)
	}
	note, err := encodeBWSNoteV2(bwsNoteMetadataV2{KinkoSyncFormat: 2, MachineID: syncTestMachineID, Profile: "default", Scope: scopeKindPath, LogicalPath: "work/project", Key: "REMOTE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	name := buildBWSSecretNameV2(syncTestMachineID, logicalScopeRef{Profile: "default", Kind: scopeKindPath, LogicalPath: "work/project"}, "REMOTE_KEY")
	secret := bwsSecret{ID: "remote-id", ProjectID: "fixture-project", Key: name, Value: "fixture-remote", Note: note, RevisionDate: "revision"}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{secret.ID: secret}}
	snapshot := &lockedSyncSnapshot{DataDir: dataDir, Meta: meta, Data: data, Config: config, DEK: dek}
	options := syncDirectionV2Options{Direction: syncDirectionPull, Provider: supportedSyncProvider, Selector: syncSelector{IncludeKeys: []string{"REMOTE_KEY"}}, PathMaps: []syncPathMap{mapping}, ConflictPolicy: syncConflictFail, DeleteMode: syncDeleteAuto, Retry: defaultSyncRetryPolicy(), Resume: syncResumeAuto, Progress: syncProgressNone}
	var stdout, stderr bytes.Buffer
	if err := executeSyncDirectionV2(snapshot, provider, runtime, options, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Profiles["default"][filepath.Join(root, "project")]["REMOTE_KEY"]; got != "fixture-remote" {
		t.Fatalf("materialized value=%q", got)
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}
