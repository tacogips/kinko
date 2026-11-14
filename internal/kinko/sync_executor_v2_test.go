package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestExecuteSyncPlanV2AdoptsAmbiguousCreateWithoutBlindRetry(t *testing.T) {
	const canary = "executor-sensitive-canary"
	plan := executionCreatePlan(t, canary)
	provider := &executionTestProvider{projects: []bwsProject{{ID: "project"}}, secrets: map[string]bwsSecret{}, createApplied: true}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{"EXEC_KEY": canary, "UNSELECTED": "unchanged"}}
	state := &bwsSyncStateV2{Format: 2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}}
	var progress bytes.Buffer
	result, err := executeSyncPlanV2(context.Background(), provider, plan, data, state, syncWriterProgress{mode: syncProgressJSONL, writer: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 1 || result.Created != 1 || state.Checkpoint == nil || state.Checkpoint.Phase != syncCheckpointComplete {
		t.Fatalf("calls=%d result=%+v checkpoint=%+v", provider.createCalls, result, state.Checkpoint)
	}
	if strings.Contains(progress.String(), canary) {
		t.Fatalf("progress leaked value: %s", progress.String())
	}
	decoder := json.NewDecoder(bytes.NewBufferString(progress.String()))
	for decoder.More() {
		var event syncProgressEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
	}
	if data.Shared["UNSELECTED"] != "unchanged" {
		t.Fatal("executor changed unselected vault data")
	}
}

func TestExecuteSyncPlanV2CreateRecordsRemoteOrganizationWithoutPreconditionOrContext(t *testing.T) {
	plan := executionCreatePlan(t, "value")
	provider := &executionTestProvider{secrets: map[string]bwsSecret{}}
	data := executionDataForValues(map[string]string{"EXEC_KEY": "value"})
	state := emptyExecutionState()
	if state.Context != nil {
		t.Fatal("test fixture must start without a pinned provider context")
	}
	result, err := executeSyncPlanV2(context.Background(), provider, plan, data, state, discardSyncProgress{})
	if err != nil || result.Created != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	entry, ok := state.Entries[plan.Actions[0].EntryID]
	if !ok {
		t.Fatal("created entry missing from state")
	}
	remoteSecret, ok := provider.secrets[entry.SecretID]
	if !ok {
		t.Fatal("created secret missing from provider")
	}
	if entry.OrganizationID == "" || entry.OrganizationID != remoteSecret.OrganizationID {
		t.Fatalf("entry organization=%q, want remote-confirmed organization %q", entry.OrganizationID, remoteSecret.OrganizationID)
	}
}

func TestUpdateV2StateForConfirmedActionCreateFallsBackToRemoteOrganization(t *testing.T) {
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ORG_KEY"}
	name, note, err := intendedRemoteMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	action := syncPlannedAction{EntryID: syncEntryID(identity), Kind: syncActionCreate, Identity: identity, IntendedName: name, IntendedNoteSHA256: valueSHA256(note), IntendedValueSHA256: valueSHA256("value")}
	plan := &syncPlanV2{ProviderIdentity: identity.Provider}
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2}
	remote := bwsSecret{ID: "created-id", OrganizationID: "remote-observed-org", ProjectID: "project", Key: name, Value: "value", Note: note, RevisionDate: "revision-one"}
	confirmed := syncCheckpointResult{SecretID: remote.ID, Revision: remote.RevisionDate}
	updateV2StateForConfirmedAction(state, plan, action, confirmed, nil, remote)
	entry, ok := state.Entries[action.EntryID]
	if !ok {
		t.Fatal("state entry was not recorded")
	}
	if entry.OrganizationID != remote.OrganizationID {
		t.Fatalf("organization=%q, want %q", entry.OrganizationID, remote.OrganizationID)
	}
}

func TestExecuteSyncPlanV2PullPreflightFailureLeavesVaultByteEquivalent(t *testing.T) {
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "PULL_KEY"}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: identity.Key})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "remote-id", OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, identity.Key), Value: "remote-canary", Note: note, RevisionDate: "revision"}
	action := syncPlannedAction{EntryID: syncEntryID(identity), Kind: syncActionUpdate, Identity: identity, Precondition: preconditionForSecret(secret, syncPlanContext{ProviderIdentity: identity.Provider, Endpoint: "https://api.example.test", OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID}), RequiredCapabilities: []syncCapability{syncCapabilityRead}, LocalPresent: true, RemotePresent: true}
	plan := &syncPlanV2{Format: 2, Operation: syncOperationPull, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("c", 64), Actions: []syncPlannedAction{action}, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	provider := &executionTestProvider{secrets: map[string]bwsSecret{"remote-id": secret}, getErr: map[string]error{"remote-id": errors.New("read failed")}}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{"PULL_KEY": "local-original", "UNSELECTED": "unchanged"}}
	before, _ := json.Marshal(data)
	_, err = executeSyncPlanV2(context.Background(), provider, plan, data, &bwsSyncStateV2{Format: 2}, discardSyncProgress{})
	if err == nil {
		t.Fatal("expected preflight failure")
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatalf("vault changed on preflight failure: before=%s after=%s", before, after)
	}
}
