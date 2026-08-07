package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingCheckpointStore struct {
	calls     int
	failAt    int
	forbidden string
	saved     []*syncCheckpoint
}

type failAtProgressSink struct {
	calls  int
	failAt int
}

func (sink *failAtProgressSink) Emit(syncProgressEvent) error {
	sink.calls++
	if sink.calls == sink.failAt {
		return errors.New("injected progress failure")
	}
	return nil
}

func (store *recordingCheckpointStore) Save(checkpoint *syncCheckpoint) error {
	store.calls++
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if store.forbidden != "" && bytes.Contains(encoded, []byte(store.forbidden)) {
		return errors.New("checkpoint leaked forbidden content")
	}
	if store.calls == store.failAt {
		return errors.New("injected checkpoint persistence failure")
	}
	store.saved = append(store.saved, cloneSyncCheckpoint(checkpoint))
	return nil
}

func TestExecuteSyncPlanV2PersistsBeforeMutationAndResumesCrashAfterAction(t *testing.T) {
	const canary = "crash-resume-secret-canary"
	plan := executionCreatePlanForValues(t, map[string]string{"FIRST": canary, "SECOND": "second-value"})
	provider := &executionTestProvider{secrets: map[string]bwsSecret{}}
	data := executionDataForValues(map[string]string{"FIRST": canary, "SECOND": "second-value", "UNSELECTED": "preserved"})
	state := emptyExecutionState()
	store := &recordingCheckpointStore{failAt: 2, forbidden: canary}

	result, err := executeSyncPlanV2WithOptions(context.Background(), provider, plan, data, state, discardSyncProgress{}, syncExecutionOptions{Checkpoints: store})
	if err == nil || provider.createCalls != 1 || !result.Partial {
		t.Fatalf("first run result=%+v err=%v creates=%d", result, err, provider.createCalls)
	}
	if len(store.saved) != 1 || len(store.saved[0].Confirmed) != 0 || store.saved[0].Phase != syncCheckpointExecuting {
		t.Fatalf("checkpoint before mutation=%+v", store.saved)
	}
	if state.Checkpoint != nil || data.Shared["UNSELECTED"] != "preserved" {
		t.Fatal("failed execution published working state")
	}

	resumedState := emptyExecutionState()
	resumedState.Checkpoint = cloneSyncCheckpoint(store.saved[0])
	resumeStore := &recordingCheckpointStore{forbidden: canary}
	result, err = executeSyncPlanV2WithOptions(context.Background(), provider, plan, data, resumedState, discardSyncProgress{}, syncExecutionOptions{Resume: syncResumeRequire, Checkpoints: resumeStore})
	if err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 2 || result.Created != 2 || resumedState.Checkpoint.Phase != syncCheckpointComplete {
		t.Fatalf("resume result=%+v creates=%d checkpoint=%+v", result, provider.createCalls, resumedState.Checkpoint)
	}
	if len(provider.secrets) != 2 {
		t.Fatalf("resume duplicated or lost a create: %+v", provider.secrets)
	}
}

func TestExecuteSyncPlanV2PersistenceFailureBeforeMutationFailsClosed(t *testing.T) {
	plan := executionCreatePlan(t, "value")
	provider := &executionTestProvider{secrets: map[string]bwsSecret{}}
	store := &recordingCheckpointStore{failAt: 1}
	_, err := executeSyncPlanV2WithOptions(context.Background(), provider, plan, executionDataForValues(map[string]string{"EXEC_KEY": "value"}), emptyExecutionState(), discardSyncProgress{}, syncExecutionOptions{Checkpoints: store})
	if err == nil || provider.createCalls != 0 {
		t.Fatalf("err=%v create calls=%d", err, provider.createCalls)
	}
}

func TestExecuteSyncPlanV2PullDeletionsRemainAtomicInMemory(t *testing.T) {
	providerIdentity := strings.Repeat("a", 64)
	actions := make([]syncPlannedAction, 0, 2)
	for _, key := range []string{"FIRST", "SECOND"} {
		identity := syncIdentity{Provider: providerIdentity, ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: key}
		actions = append(actions, syncPlannedAction{EntryID: syncEntryID(identity), Kind: syncActionDelete, Identity: identity, RequiredCapabilities: []syncCapability{syncCapabilityRead}, LocalPresent: true, BaselinePresent: true})
	}
	plan := &syncPlanV2{Format: syncPlanFormatV2, Operation: syncOperationPull, ProviderIdentity: providerIdentity, SelectorDigest: strings.Repeat("c", 64), Actions: actions, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	data := executionDataForValues(map[string]string{"FIRST": "one", "SECOND": "two", "UNSELECTED": "preserved"})
	before, _ := json.Marshal(data)
	progress := &failAtProgressSink{failAt: 4}
	_, err := executeSyncPlanV2(context.Background(), &executionTestProvider{secrets: map[string]bwsSecret{}}, plan, data, emptyExecutionState(), progress)
	if err == nil {
		t.Fatal("expected failure after the first in-memory deletion")
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatalf("pull published a partial local snapshot: before=%s after=%s", before, after)
	}
}

func TestExecuteSyncPlanV2ResumeReloadsLocalValuesAndRemotePreconditions(t *testing.T) {
	plan := executionCreatePlan(t, "original")
	checkpoint, err := newSyncCheckpoint(plan)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyExecutionState()
	state.Checkpoint = checkpoint
	provider := &executionTestProvider{secrets: map[string]bwsSecret{}}
	_, err = executeSyncPlanV2WithOptions(context.Background(), provider, plan, executionDataForValues(map[string]string{"EXEC_KEY": "changed"}), state, discardSyncProgress{}, syncExecutionOptions{Resume: syncResumeRequire})
	if err == nil || provider.createCalls != 0 {
		t.Fatalf("changed local input resumed: err=%v calls=%d", err, provider.createCalls)
	}
}

func TestExecuteSyncPlanV2AmbiguousMutationsAllowAtMostOneConditionalRetry(t *testing.T) {
	t.Run("create stops after one retry", func(t *testing.T) {
		plan := executionCreatePlan(t, "value")
		provider := &executionTestProvider{secrets: map[string]bwsSecret{}, createErrs: []error{executionAmbiguousError("first"), executionAmbiguousError("second")}}
		_, err := executeSyncPlanV2(context.Background(), provider, plan, executionDataForValues(map[string]string{"EXEC_KEY": "value"}), emptyExecutionState(), discardSyncProgress{})
		if err == nil || provider.createCalls != 2 {
			t.Fatalf("err=%v create calls=%d", err, provider.createCalls)
		}
	})

	t.Run("update adopts applied outcome", func(t *testing.T) {
		plan, original := executionExistingSecretPlan(t, syncActionUpdate, "new-value")
		provider := &executionTestProvider{secrets: map[string]bwsSecret{original.ID: original}, updateErrs: []error{executionAmbiguousError("lost response")}, applyOnError: true}
		result, err := executeSyncPlanV2(context.Background(), provider, plan, executionDataForValues(map[string]string{"EXISTING": "new-value"}), emptyExecutionState(), discardSyncProgress{})
		if err != nil || provider.updateCalls != 1 || result.Updated != 1 {
			t.Fatalf("result=%+v err=%v update calls=%d", result, err, provider.updateCalls)
		}
	})

	t.Run("delete adopts missing outcome", func(t *testing.T) {
		plan, original := executionExistingSecretPlan(t, syncActionDelete, "")
		provider := &executionTestProvider{secrets: map[string]bwsSecret{original.ID: original}, deleteErrs: []error{executionAmbiguousError("lost response")}, applyOnError: true}
		result, err := executeSyncPlanV2(context.Background(), provider, plan, executionDataForValues(nil), emptyExecutionState(), discardSyncProgress{})
		if err != nil || provider.deleteCalls != 1 || result.Deleted != 1 {
			t.Fatalf("result=%+v err=%v delete calls=%d", result, err, provider.deleteCalls)
		}
	})
}

func TestPersistSyncCheckpointEncryptsAndDoesNotMutateConfigOnFailure(t *testing.T) {
	const canary = "encrypted-checkpoint-value-canary"
	plan := executionCreatePlan(t, canary)
	checkpoint, err := newSyncCheckpoint(plan)
	if err != nil {
		t.Fatal(err)
	}
	dataDir, _, dek := setupSyncE2EVault(t)
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistSyncCheckpoint(dataDir, dek, config, checkpoint); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := os.ReadFile(filepath.Join(dataDir, "vault", "config.v1.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(canary)) || strings.Contains(config[configKeyBWSSyncState], canary) {
		t.Fatal("checkpoint persistence exposed the local value")
	}
	reloaded, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(reloaded[configKeyBWSSyncState])
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBWSSyncStateV2(envelope)
	if err != nil || decoded.Checkpoint == nil {
		t.Fatalf("decrypted checkpoint missing: %v", err)
	}
	encoded, _ := json.Marshal(decoded.Checkpoint)
	if bytes.Contains(encoded, []byte(canary)) {
		t.Fatal("decrypted checkpoint exposed the local value")
	}

	brokenConfig := map[string]string{configKeyBWSSyncState: config[configKeyBWSSyncState]}
	before := brokenConfig[configKeyBWSSyncState]
	if err := persistSyncCheckpoint(filepath.Join(t.TempDir(), "missing"), dek, brokenConfig, checkpoint); err == nil {
		t.Fatal("expected persistence failure")
	}
	if brokenConfig[configKeyBWSSyncState] != before {
		t.Fatal("failed persistence mutated caller config")
	}
}

func TestSelectExecutionCheckpointCompletedDifferentPlanResumeAutoStartsFresh(t *testing.T) {
	completed := completedCheckpointForPlan(t, executionCreatePlan(t, "value"))
	plan := executionCreatePlanForValues(t, map[string]string{"OTHER_KEY": "value"})
	checkpoint, resumed, err := selectExecutionCheckpoint(completed, plan, syncResumeAuto)
	if err != nil || resumed {
		t.Fatalf("checkpoint=%+v resumed=%t err=%v", checkpoint, resumed, err)
	}
	want, err := newSyncCheckpoint(plan)
	if err != nil {
		t.Fatal(err)
	}
	gotEncoded, _ := json.Marshal(checkpoint)
	wantEncoded, _ := json.Marshal(want)
	if !bytes.Equal(gotEncoded, wantEncoded) {
		t.Fatalf("fresh checkpoint mismatch: got=%s want=%s", gotEncoded, wantEncoded)
	}
}

func TestSelectExecutionCheckpointCompletedDifferentPlanResumeNeverStartsFresh(t *testing.T) {
	completed := completedCheckpointForPlan(t, executionCreatePlan(t, "value"))
	plan := executionCreatePlanForValues(t, map[string]string{"OTHER_KEY": "value"})
	checkpoint, resumed, err := selectExecutionCheckpoint(completed, plan, syncResumeNever)
	if err != nil || resumed || checkpoint == nil || checkpoint.Phase != syncCheckpointPrepared {
		t.Fatalf("checkpoint=%+v resumed=%t err=%v", checkpoint, resumed, err)
	}
}

func TestSelectExecutionCheckpointCompletedDifferentPlanResumeRequireErrors(t *testing.T) {
	completed := completedCheckpointForPlan(t, executionCreatePlan(t, "value"))
	plan := executionCreatePlanForValues(t, map[string]string{"OTHER_KEY": "value"})
	if _, _, err := selectExecutionCheckpoint(completed, plan, syncResumeRequire); err == nil {
		t.Fatal("expected resume=require to refuse a completed checkpoint from a different plan")
	}
}

func TestSelectExecutionCheckpointCompletedSamePlanResumes(t *testing.T) {
	plan := executionCreatePlan(t, "value")
	completed := completedCheckpointForPlan(t, plan)
	for _, mode := range []syncResumeMode{syncResumeAuto, syncResumeRequire} {
		checkpoint, resumed, err := selectExecutionCheckpoint(completed, plan, mode)
		if err != nil || !resumed || checkpoint == nil || checkpoint.Phase != syncCheckpointComplete {
			t.Fatalf("mode=%s checkpoint=%+v resumed=%t err=%v", mode, checkpoint, resumed, err)
		}
	}
	if _, _, err := selectExecutionCheckpoint(completed, plan, syncResumeNever); err == nil {
		t.Fatal("expected resume=never to still refuse an existing checkpoint even when it matches the plan")
	}
}

func TestSelectExecutionCheckpointInFlightDifferentPlanRefusesResume(t *testing.T) {
	inFlight, err := newSyncCheckpoint(executionCreatePlan(t, "value"))
	if err != nil {
		t.Fatal(err)
	}
	inFlight.Phase = syncCheckpointExecuting
	plan := executionCreatePlanForValues(t, map[string]string{"OTHER_KEY": "value"})
	if _, _, err := selectExecutionCheckpoint(inFlight, plan, syncResumeAuto); err == nil {
		t.Fatal("expected an in-flight checkpoint mismatch to refuse resume")
	}
	if _, _, err := selectExecutionCheckpoint(inFlight, plan, syncResumeNever); err == nil {
		t.Fatal("expected an in-flight checkpoint to require reset before --resume=never")
	}
}

func completedCheckpointForPlan(t *testing.T, plan *syncPlanV2) *syncCheckpoint {
	t.Helper()
	checkpoint, err := newSyncCheckpoint(plan)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Phase = syncCheckpointComplete
	for _, action := range checkpoint.Actions {
		checkpoint.Confirmed = append(checkpoint.Confirmed, syncCheckpointResult{ActionID: action.ActionID, SecretID: "confirmed-secret-id", Revision: "confirmed-revision"})
	}
	return checkpoint
}

func executionCreatePlanForValues(t *testing.T, values map[string]string) *syncPlanV2 {
	t.Helper()
	providerIdentity := strings.Repeat("a", 64)
	actions := make([]syncPlannedAction, 0, len(values))
	for key, value := range values {
		identity := syncIdentity{Provider: providerIdentity, ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: key}
		name, note, err := intendedRemoteMetadata(identity)
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, syncPlannedAction{EntryID: syncEntryID(identity), Kind: syncActionCreate, Identity: identity, RequiredCapabilities: []syncCapability{syncCapabilityRead, syncCapabilityValueSafeMutation}, LocalPresent: true, IntendedName: name, IntendedNoteSHA256: valueSHA256(note), IntendedValueSHA256: valueSHA256(value)})
	}
	plan := &syncPlanV2{Format: syncPlanFormatV2, Operation: syncOperationPush, ProviderIdentity: providerIdentity, SelectorDigest: strings.Repeat("c", 64), Actions: actions, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func executionExistingSecretPlan(t *testing.T, kind syncActionKind, intendedValue string) (*syncPlanV2, bwsSecret) {
	t.Helper()
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "EXISTING"}
	name, note, err := intendedRemoteMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	original := bwsSecret{ID: "existing-id", OrganizationID: "org", ProjectID: "project", Key: name, Value: "old-value", Note: note, RevisionDate: "old-revision"}
	context := syncPlanContext{ProviderIdentity: identity.Provider, Endpoint: "https://api.example.test", OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID}
	action := syncPlannedAction{EntryID: syncEntryID(identity), Kind: kind, Identity: identity, Precondition: preconditionForSecret(original, context), RequiredCapabilities: []syncCapability{syncCapabilityRead}, RemotePresent: true, IntendedName: name, IntendedNoteSHA256: valueSHA256(note), IntendedValueSHA256: valueSHA256(intendedValue)}
	if kind == syncActionUpdate {
		action.RequiredCapabilities = append(action.RequiredCapabilities, syncCapabilityValueSafeMutation)
		action.LocalPresent = true
	} else {
		action.RequiredCapabilities = append(action.RequiredCapabilities, syncCapabilityDelete)
		action.IntendedName, action.IntendedNoteSHA256, action.IntendedValueSHA256 = "", "", ""
	}
	plan := &syncPlanV2{Format: syncPlanFormatV2, Operation: syncOperationPush, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("c", 64), Actions: []syncPlannedAction{action}, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan, original
}

func executionDataForValues(values map[string]string) *vaultData {
	shared := map[string]string{}
	for key, value := range values {
		shared[key] = value
	}
	return &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: shared}
}

func emptyExecutionState() *bwsSyncStateV2 {
	return &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}}
}
