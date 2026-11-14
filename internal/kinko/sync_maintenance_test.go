package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type maintenanceTestProvider struct {
	secrets     map[string]bwsSecret
	deleteCalls []string
	createCalls int
	deleteErr   error
}

func (*maintenanceTestProvider) Capabilities() map[syncCapability]bool {
	return map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true, syncCapabilityValueSafeMutation: true}
}
func (*maintenanceTestProvider) ListProjects(context.Context) ([]bwsProject, error) {
	return []bwsProject{{ID: "project"}}, nil
}
func (provider *maintenanceTestProvider) ListSecrets(context.Context, string) ([]bwsSecret, error) {
	result := make([]bwsSecret, 0, len(provider.secrets))
	for _, secret := range provider.secrets {
		result = append(result, secret)
	}
	return result, nil
}
func (provider *maintenanceTestProvider) GetSecret(_ context.Context, id string) (bwsSecret, error) {
	secret, ok := provider.secrets[id]
	if !ok {
		return bwsSecret{}, errBWSSyncSecretNotFound
	}
	return secret, nil
}
func (provider *maintenanceTestProvider) CreateSecret(_ context.Context, request bwsMutationRequest) (bwsSecret, error) {
	provider.createCalls++
	id := "replacement-" + request.Name
	secret := bwsSecret{ID: id, OrganizationID: "org", ProjectID: request.ProjectID, Key: request.Name, Value: request.Value, Note: request.Note, RevisionDate: "new-revision"}
	provider.secrets[id] = secret
	return secret, nil
}
func (*maintenanceTestProvider) UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, errors.New("unexpected update")
}
func (provider *maintenanceTestProvider) DeleteSecret(_ context.Context, id string) error {
	provider.deleteCalls = append(provider.deleteCalls, id)
	if provider.deleteErr != nil {
		return provider.deleteErr
	}
	delete(provider.secrets, id)
	return nil
}

func TestSyncResetPreviewAndApplyPreserveUnselectedRawStateAndOwnership(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	unknown := json.RawMessage(`{"future": [ 1, 2, 3 ]}`)
	envelope.Raw["future_root"] = unknown
	selectedID := maintenanceEntryID(state, "ONE")
	unselectedID := maintenanceEntryID(state, "TWO")
	unselectedRaw := append([]byte(nil), maintenanceRawObject(t, envelope.Raw["entries"])[unselectedID]...)

	preview, err := buildSyncResetPlan(envelope, syncResetOptions{Provider: "bws", Baseline: true, Selector: syncSelector{IncludeKeys: []string{"ONE"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := applySyncReset(t.TempDir(), make([]byte, dekLength), map[string]string{}, preview); err == nil {
		t.Fatal("preview plan unexpectedly applied")
	}

	plan, err := buildSyncResetPlan(envelope, syncResetOptions{Provider: "bws", Baseline: true, Yes: true, Selector: syncSelector{IncludeKeys: []string{"ONE"}}})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalRawObject(envelope.Raw)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]string{configKeyBWSSyncState: string(encoded), "unrelated": "same"}
	dek := make([]byte, dekLength)
	if err := applySyncReset(dataDir, dek, config, plan); err != nil {
		t.Fatal(err)
	}
	updated, err := decodeBWSSyncState(config[configKeyBWSSyncState])
	if err != nil {
		t.Fatal(err)
	}
	entries := maintenanceRawObject(t, updated.Raw["entries"])
	if _, exists := entries[selectedID]; exists {
		t.Fatal("selected baseline entry remains")
	}
	if string(entries[unselectedID]) != string(unselectedRaw) {
		t.Fatal("unselected raw entry bytes changed")
	}
	if string(updated.Raw["future_root"]) != string(unknown) || len(maintenanceRawObject(t, updated.Raw["ownership"])) != len(state.Ownership) {
		t.Fatal("unknown fields or ownership proof changed")
	}
}

func TestSyncResetCheckpointDigestGate(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	checkpointPlan := maintenancePlanForIdentity(t, identityForStateEntry(state.Entries[maintenanceEntryID(state, "ONE")]))
	checkpoint, err := newSyncCheckpoint(checkpointPlan)
	if err != nil {
		t.Fatal(err)
	}
	state.Checkpoint = checkpoint
	envelope = maintenanceEnvelope(t, state)
	_, err = buildSyncResetPlan(envelope, syncResetOptions{Checkpoint: true, Selector: syncSelector{IncludeKeys: []string{"ONE"}}})
	if err == nil || !strings.Contains(err.Error(), "selector digest") {
		t.Fatalf("checkpoint mismatch error = %v", err)
	}
}

func TestSyncResetUnscopedCheckpointApplyAcceptsAnyCheckpointSelector(t *testing.T) {
	state, _ := maintenanceTestState(t)
	identity := identityForStateEntry(state.Entries[maintenanceEntryID(state, "ONE")])
	checkpointPlan := maintenancePlanForIdentity(t, identity)
	checkpoint, err := newSyncCheckpoint(checkpointPlan)
	if err != nil {
		t.Fatal(err)
	}
	state.Checkpoint = checkpoint
	envelope := maintenanceEnvelope(t, state)
	plan, err := buildSyncResetPlan(envelope, syncResetOptions{Checkpoint: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalRawObject(envelope.Raw)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]string{configKeyBWSSyncState: string(encoded)}
	if err := applySyncReset(dataDir, make([]byte, dekLength), config, plan); err != nil {
		t.Fatal(err)
	}
	updated, err := decodeBWSSyncState(config[configKeyBWSSyncState])
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := updated.Raw["checkpoint"]; exists {
		t.Fatal("unscoped checkpoint reset left the checkpoint in state")
	}
}

func TestSyncStatusOfflineDoesNotRequireProviderSnapshot(t *testing.T) {
	_, envelope := maintenanceTestState(t)
	result, err := buildSyncStatusResult(nil, nil, envelope, syncStatusOptions{}, syncPlanContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Online || result.BaselineHealth != "healthy" || result.CheckpointHealth != "absent" {
		t.Fatalf("unexpected offline status: %+v", result)
	}
	_, err = buildSyncStatusResult(nil, nil, envelope, syncStatusOptions{Online: true}, syncPlanContext{}, nil)
	if err == nil {
		t.Fatal("online status accepted an absent provider snapshot")
	}
	absent, err := buildSyncStatusResult(nil, nil, syncStateEnvelope{}, syncStatusOptions{}, syncPlanContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if absent.BaselineHealth != "absent" || len(absent.Formats) != 0 {
		t.Fatalf("absent-state offline status = %+v", absent)
	}
}

func TestSyncStatusOnlineReportsPinnedProviderDrift(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	entry := state.Entries[maintenanceEntryID(state, "ONE")]
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	if err != nil {
		t.Fatal(err)
	}
	remote := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "remote-change", Note: note, RevisionDate: "remote-revision"}
	result, err := buildSyncStatusResult(
		[]syncEntry{{ref: scopeRef{kind: scopeKindShared}, key: "ONE", value: "one"}},
		[]bwsSecret{remote}, envelope, syncStatusOptions{Online: true},
		syncPlanContext{ProviderIdentity: entry.ProviderIdentity, Endpoint: entry.Endpoint, OrganizationID: entry.OrganizationID, ProjectID: entry.ProjectID, MachineID: entry.MachineID}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Online || len(result.Drift) != 1 || result.Drift[0].Key != "ONE" {
		t.Fatalf("online status drift = %+v", result)
	}
}

func TestSyncReconcileAdoptsOnlyExactMatchAndRevalidates(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	entry := state.Entries[maintenanceEntryID(state, "ONE")]
	ref := scopeRef{kind: scopeKindShared}
	note, _ := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	remote := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: entry.Revision}
	plan, err := buildSyncReconcilePlan([]syncEntry{{ref: ref, key: "ONE", value: "one"}}, []bwsSecret{remote}, envelope, syncReconcileOptions{Provider: "bws", Yes: true, Selector: syncSelector{IncludeKeys: []string{"ONE"}}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{remote.ID: remote}}
	working := cloneBWSSyncStateV2(state)
	unselectedBefore := working.Entries[maintenanceEntryID(state, "TWO")]
	if err := applySyncReconcile(context.Background(), provider, plan, working); err != nil {
		t.Fatal(err)
	}
	if len(working.Entries) != 2 {
		t.Fatalf("adopted entries = %d", len(working.Entries))
	}
	selectedAfter := working.Entries[maintenanceEntryID(state, "ONE")]
	unselectedAfter := working.Entries[maintenanceEntryID(state, "TWO")]
	if string(selectedAfter.Raw["future_entry"]) != `{"preserve" : true}` || string(unselectedAfter.Raw["future_entry"]) != string(unselectedBefore.Raw["future_entry"]) {
		t.Fatal("reconcile changed selected or unselected opaque state")
	}
	if len(working.Ownership) != 0 {
		t.Fatal("state reconciliation manufactured ownership proof")
	}
	changed := remote
	changed.Value = "changed"
	provider.secrets[remote.ID] = changed
	if err := applySyncReconcile(context.Background(), provider, plan, working); err == nil {
		t.Fatal("reconcile applied after remote value changed")
	}
}

func TestSyncMetadataUpgradeResumesExactCreatedAndStateReplacedPair(t *testing.T) {
	for _, phase := range []string{"created", "state-replaced"} {
		t.Run(phase, func(t *testing.T) {
			state, envelope, local, old := maintenanceUpgradeFixture(t)
			initial, err := buildSyncReconcilePlan(local, []bwsSecret{old}, envelope, syncReconcileOptions{UpgradeMetadata: true, Yes: true})
			if err != nil {
				t.Fatal(err)
			}
			action := initial.Actions[0]
			_, note, err := intendedRemoteMetadata(action.Identity)
			if err != nil {
				t.Fatal(err)
			}
			replacement := bwsSecret{ID: "replacement-id", OrganizationID: "org", ProjectID: "project", Key: action.IntendedName, Value: old.Value, Note: note, RevisionDate: "replacement-revision"}
			checkpoint := &syncMetadataUpgradeCheckpoint{
				Old:   *action.Precondition,
				New:   *preconditionForSecret(replacement, *action.PreconditionContext()),
				Phase: phase,
			}
			state.MetadataUpgrade = checkpoint
			if phase == "state-replaced" {
				if err := replaceMetadataUpgradeState(state, action, replacement, initial); err != nil {
					t.Fatal(err)
				}
				state.MetadataUpgrade.Phase = phase
			}
			envelope = maintenanceEnvelope(t, state)
			provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{old.ID: old, replacement.ID: replacement}}
			plan, err := buildSyncReconcilePlan(local, []bwsSecret{old, replacement}, envelope, syncReconcileOptions{UpgradeMetadata: true, Yes: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := applySyncReconcile(context.Background(), provider, plan, state); err != nil {
				t.Fatal(err)
			}
			if provider.createCalls != 0 || len(provider.deleteCalls) != 1 || provider.deleteCalls[0] != old.ID {
				t.Fatalf("creates=%d deletes=%v", provider.createCalls, provider.deleteCalls)
			}
			if state.MetadataUpgrade != nil {
				t.Fatal("completed metadata upgrade retained its checkpoint")
			}
			updated := state.Entries[action.EntryID]
			if updated.SecretID != replacement.ID || updated.LocalPath == "" || updated.LogicalPath == "" {
				t.Fatalf("upgraded state lost replacement or path mapping: %+v", updated)
			}
		})
	}
}

func TestSyncMetadataUpgradeCreatesReadsBackReplacesStateAndDeletesOld(t *testing.T) {
	state, envelope, local, old := maintenanceUpgradeFixture(t)
	plan, err := buildSyncReconcilePlan(local, []bwsSecret{old}, envelope, syncReconcileOptions{UpgradeMetadata: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{old.ID: old}}
	if err := applySyncReconcile(context.Background(), provider, plan, state); err != nil {
		t.Fatal(err)
	}
	if provider.createCalls != 1 || len(provider.deleteCalls) != 1 || provider.deleteCalls[0] != old.ID {
		t.Fatalf("creates=%d deletes=%v", provider.createCalls, provider.deleteCalls)
	}
	action := plan.Actions[0]
	updated := state.Entries[action.EntryID]
	if updated.SecretID == old.ID || updated.LocalPath == "" || updated.LogicalPath == "" || state.MetadataUpgrade != nil {
		t.Fatalf("metadata-upgrade state = %+v checkpoint=%+v", updated, state.MetadataUpgrade)
	}
	owner, exists := state.Ownership[action.EntryID]
	if !exists || owner.SecretID != updated.SecretID {
		t.Fatalf("replacement ownership = %+v", owner)
	}
}

func TestSyncMetadataUpgradePersistsEveryIrreversiblePhase(t *testing.T) {
	state, envelope, local, old := maintenanceUpgradeFixture(t)
	plan, err := buildSyncReconcilePlan(local, []bwsSecret{old}, envelope, syncReconcileOptions{UpgradeMetadata: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{old.ID: old}}
	var phases []string
	persist := func(current *bwsSyncStateV2) error {
		if current.MetadataUpgrade == nil {
			phases = append(phases, "complete")
		} else {
			phases = append(phases, current.MetadataUpgrade.Phase)
		}
		return nil
	}
	if err := applySyncMetadataUpgradeDurable(context.Background(), provider, plan, state, persist); err != nil {
		t.Fatal(err)
	}
	want := []string{"created", "state-replaced", "complete"}
	if len(phases) != len(want) {
		t.Fatalf("persisted phases=%v want %v", phases, want)
	}
	for index := range want {
		if phases[index] != want[index] {
			t.Fatalf("persisted phases=%v want %v", phases, want)
		}
	}
}

func TestDoctorBWSWriteCanaryReportsCleanupIDWithoutValue(t *testing.T) {
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	check, err := runBWSWriteCanary(context.Background(), provider, "project")
	if err != nil || check.Status != "ok" || check.CleanupID != "" || len(provider.secrets) != 0 {
		t.Fatalf("successful canary check=%+v err=%v remaining=%d", check, err, len(provider.secrets))
	}

	provider = &maintenanceTestProvider{secrets: map[string]bwsSecret{}, deleteErr: errors.New("delete unavailable")}
	check, err = runBWSWriteCanary(context.Background(), provider, "project")
	if err == nil || check.CleanupID == "" || !strings.Contains(check.Detail, "cleanup required") {
		t.Fatalf("failed canary check=%+v err=%v", check, err)
	}
	for _, secret := range provider.secrets {
		if strings.Contains(check.Detail, secret.Value) || strings.Contains(check.CleanupID, secret.Value) {
			t.Fatal("canary value leaked into cleanup diagnostics")
		}
	}
}

func TestSyncPruneOwnershipRevisionAndMalformedGates(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	id := maintenanceEntryID(state, "ONE")
	entry := state.Entries[id]
	note, _ := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	secret := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: "changed-revision"}
	state.Ownership[id] = syncOwnershipRecord{SecretID: secret.ID, ProviderIdentity: entry.ProviderIdentity, Revision: "old-revision", Identity: identityForStateEntry(entry)}
	envelope = maintenanceEnvelope(t, state)
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	if _, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{}); err == nil {
		t.Fatal("revision mismatch was pruned automatically")
	}
	plan, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{SecretIDs: []string{secret.ID}, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{secret.ID: secret}}
	if err := applySyncPrune(context.Background(), provider, plan, data, state); err != nil {
		t.Fatal(err)
	}
	if len(provider.deleteCalls) != 1 || len(state.Ownership) != 0 {
		t.Fatalf("delete calls=%v ownership=%d", provider.deleteCalls, len(state.Ownership))
	}

	malformed := secret
	malformed.ID, malformed.Note = "malformed-id", "not-json"
	if _, err := buildSyncPrunePlan([]bwsSecret{malformed}, envelope, data, syncPruneOptions{SecretIDs: []string{malformed.ID}}); err == nil {
		t.Fatal("malformed record accepted without acknowledgement")
	}
	malformedPlan, err := buildSyncPrunePlan([]bwsSecret{malformed}, envelope, data, syncPruneOptions{SecretIDs: []string{malformed.ID}, AckMalformed: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	malformedProvider := &maintenanceTestProvider{secrets: map[string]bwsSecret{malformed.ID: malformed}}
	if err := applySyncPrune(context.Background(), malformedProvider, malformedPlan, data, state); err != nil {
		t.Fatal(err)
	}
	if len(malformedProvider.deleteCalls) != 1 || malformedProvider.deleteCalls[0] != malformed.ID {
		t.Fatalf("malformed delete calls = %v", malformedProvider.deleteCalls)
	}
}

func TestSyncPruneDuplicateRecordsRequireEveryExactIDAndPreflightBeforeDelete(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	entry := state.Entries[maintenanceEntryID(state, "ONE")]
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	if err != nil {
		t.Fatal(err)
	}
	first := bwsSecret{ID: "duplicate-one", OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: "revision-one"}
	second := first
	second.ID = "duplicate-two"
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	_, err = buildSyncPrunePlan([]bwsSecret{first, second}, envelope, data, syncPruneOptions{SecretIDs: []string{first.ID}, AckMalformed: true})
	if err == nil || !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("partial duplicate acknowledgement error = %v", err)
	}
	plan, err := buildSyncPrunePlan([]bwsSecret{first, second}, envelope, data, syncPruneOptions{SecretIDs: []string{first.ID, second.ID}, AckMalformed: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{first.ID: first, second.ID: second}}
	changed := second
	changed.RevisionDate = "changed-before-apply"
	provider.secrets[second.ID] = changed
	if err := applySyncPrune(context.Background(), provider, plan, data, state); err == nil {
		t.Fatal("prune applied after a duplicate target changed")
	}
	if len(provider.deleteCalls) != 0 {
		t.Fatalf("preflight failure allowed deletes: %v", provider.deleteCalls)
	}
}

func TestSyncPruneAutomaticallyUsesOwnershipOnlyAfterLocalRemoval(t *testing.T) {
	state, _ := maintenanceTestState(t)
	id := maintenanceEntryID(state, "ONE")
	entry := state.Entries[id]
	state.Ownership[id] = syncOwnershipRecord{SecretID: entry.SecretID, ProviderIdentity: entry.ProviderIdentity, Revision: entry.Revision, Identity: identityForStateEntry(entry)}
	envelope := maintenanceEnvelope(t, state)
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: entry.Revision}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	plan, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Maintenance.PruneCandidates) != 1 {
		t.Fatalf("automatic ownership candidates = %+v", plan.Maintenance.PruneCandidates)
	}
}

func TestSyncPruneDoesNotDeleteLiveOwnedEntryWithoutExactID(t *testing.T) {
	state, _ := maintenanceTestState(t)
	id := maintenanceEntryID(state, "ONE")
	entry := state.Entries[id]
	identity := identityForStateEntry(entry)
	state.Ownership[id] = syncOwnershipRecord{SecretID: entry.SecretID, ProviderIdentity: entry.ProviderIdentity, Revision: entry.Revision, Identity: identity}
	envelope := maintenanceEnvelope(t, state)
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: entry.Revision}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{"ONE": "one"}}
	if _, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{}); err == nil || !strings.Contains(err.Error(), "effective sync selection is empty") {
		t.Fatalf("live owned secret prune error = %v", err)
	}
}

func TestSyncPruneUsesLocalMaterializationForLogicalOwnedEntry(t *testing.T) {
	state, _, _, _ := maintenanceUpgradeFixture(t)
	var id string
	var entry syncStateEntryV2
	for id, entry = range state.Entries {
		break
	}
	identity := identityForStateEntry(entry)
	name, note, err := intendedRemoteMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "logical-secret", OrganizationID: "org", ProjectID: "project", Key: name, Value: "path-value", Note: note, RevisionDate: "logical-revision"}
	state.Ownership[id] = syncOwnershipRecord{SecretID: secret.ID, ProviderIdentity: entry.ProviderIdentity, Revision: secret.RevisionDate, Identity: identity}
	envelope := maintenanceEnvelope(t, state)
	data := &vaultData{
		Profiles: map[string]map[string]map[string]string{entry.Profile: {entry.LocalPath: {entry.Key: secret.Value}}},
		Shared:   map[string]string{},
	}
	if _, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{}); err == nil || !strings.Contains(err.Error(), "effective sync selection is empty") {
		t.Fatalf("live logical owned secret prune error = %v", err)
	}
}

func TestSyncPruneExactIDCannotCrossProjectOrSelectorBoundary(t *testing.T) {
	state, envelope := maintenanceTestState(t)
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	foreign := bwsSecret{ID: "foreign-id", OrganizationID: "org", ProjectID: "other-project", Key: "foreign", RevisionDate: "revision"}
	if _, err := buildSyncPrunePlan([]bwsSecret{foreign}, envelope, data, syncPruneOptions{SecretIDs: []string{foreign.ID}}); err == nil || !strings.Contains(err.Error(), "outside the selected") {
		t.Fatalf("foreign project exact-id error = %v", err)
	}

	entry := state.Entries[maintenanceEntryID(state, "ONE")]
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "ONE"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: entry.SecretID, OrganizationID: "org", ProjectID: "project", Key: entry.Name, Value: "one", Note: note, RevisionDate: entry.Revision}
	_, err = buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, syncPruneOptions{SecretIDs: []string{secret.ID}, Selector: syncSelector{IncludeKeys: []string{"TWO"}}})
	if err == nil || !strings.Contains(err.Error(), "outside the selected") {
		t.Fatalf("excluded exact-id error = %v", err)
	}
}

func TestSyncPruneRetiredMachineRequiresExactAcknowledgement(t *testing.T) {
	_, envelope := maintenanceTestState(t)
	retired := "fedcba9876543210"
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: retired, Scope: scopeKindShared, Key: "OLD"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "retired-id", OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(retired, scopeRef{kind: scopeKindShared}, "OLD"), Value: "old", Note: note, RevisionDate: "revision"}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	options := syncPruneOptions{MachineID: retired, SecretIDs: []string{secret.ID}}
	if _, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, options); err == nil || !strings.Contains(err.Error(), "retired-machine") {
		t.Fatalf("missing retired-machine acknowledgement error = %v", err)
	}
	options.AckRetiredMachine = retired
	plan, err := buildSyncPrunePlan([]bwsSecret{secret}, envelope, data, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Maintenance.PruneCandidates) != 1 || len(plan.Maintenance.Warnings) != 1 {
		t.Fatalf("retired prune plan = %+v", plan.Maintenance)
	}
}

func TestSyncPruneRemovesOnlySelectedEmptySyncScope(t *testing.T) {
	state, envelope, _, _ := maintenanceUpgradeFixture(t)
	var id string
	var entry syncStateEntryV2
	for id, entry = range state.Entries {
		break
	}
	state.Ownership[id] = syncOwnershipRecord{SecretID: entry.SecretID, ProviderIdentity: entry.ProviderIdentity, Revision: entry.Revision, Identity: identityForStateEntry(entry)}
	envelope = maintenanceEnvelope(t, state)
	data := &vaultData{
		Profiles: map[string]map[string]map[string]string{
			entry.Profile: {entry.LocalPath: {}},
			"unrelated":   {t.TempDir(): {}},
		},
		Shared: map[string]string{},
	}
	plan, err := buildSyncPrunePlan(nil, envelope, data, syncPruneOptions{PruneEmptyScopes: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &maintenanceTestProvider{secrets: map[string]bwsSecret{}}
	if err := applySyncPrune(context.Background(), provider, plan, data, state); err != nil {
		t.Fatal(err)
	}
	if _, exists := data.Profiles[entry.Profile]; exists {
		t.Fatal("selected empty sync scope remains")
	}
	if _, exists := data.Profiles["unrelated"]; !exists {
		t.Fatal("unrelated empty scope was removed")
	}
}

func maintenanceUpgradeFixture(t *testing.T) (*bwsSyncStateV2, syncStateEnvelope, []syncEntry, bwsSecret) {
	t.Helper()
	localPath := t.TempDir()
	identity := syncIdentity{
		Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID,
		Profile: "default", Scope: scopeKindPath, Path: "logical:work/project", Key: "PATH_KEY",
	}
	id := syncEntryID(identity)
	oldRef := scopeRef{profile: identity.Profile, kind: scopeKindPath, path: localPath}
	oldNote, err := encodeBWSNote(bwsNoteMetadata{
		KinkoSyncFormat: 1, MachineID: syncTestMachineID, Profile: identity.Profile,
		Scope: scopeKindPath, Path: localPath, Key: identity.Key,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := bwsSecret{
		ID: "old-secret", OrganizationID: "org", ProjectID: "project",
		Key:   buildBWSSecretName(syncTestMachineID, oldRef, identity.Key),
		Value: "path-value", Note: oldNote, RevisionDate: "old-revision",
	}
	state := &bwsSyncStateV2{
		Format: 2,
		Entries: map[string]syncStateEntryV2{id: {
			Schema: syncStateEntrySchemaV2, ProviderIdentity: identity.Provider,
			Endpoint: "https://api.example.invalid", OrganizationID: "org", ProjectID: "project",
			MachineID: syncTestMachineID, SecretID: old.ID, Name: old.Key, Revision: old.RevisionDate,
			Profile: identity.Profile, Key: identity.Key, ValueSHA256: valueSHA256(old.Value), Scope: scopeKindPath,
			LocalPath: localPath, LogicalPath: "work/project",
		}},
		Ownership: map[string]syncOwnershipRecord{},
	}
	return state, maintenanceEnvelope(t, state), []syncEntry{{ref: oldRef, key: identity.Key, value: old.Value}}, old
}

func maintenanceTestState(t *testing.T) (*bwsSyncStateV2, syncStateEnvelope) {
	t.Helper()
	state := &bwsSyncStateV2{Format: 2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}}
	for index, key := range []string{"ONE", "TWO"} {
		identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: key}
		name := buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, key)
		state.Entries[syncEntryID(identity)] = syncStateEntryV2{
			Schema: syncStateEntrySchemaV2, ProviderIdentity: identity.Provider, Endpoint: "https://api.example.invalid",
			OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID, SecretID: "secret-" + key,
			Name: name, Revision: "revision-" + string(rune('0'+index)), Key: key, ValueSHA256: valueSHA256(strings.ToLower(key)), Scope: scopeKindShared,
			Raw: map[string]json.RawMessage{"future_entry": json.RawMessage(`{"preserve" : true}`)},
		}
	}
	return state, maintenanceEnvelope(t, state)
}

func maintenanceEnvelope(t *testing.T, state *bwsSyncStateV2) syncStateEnvelope {
	t.Helper()
	base := syncStateEnvelope{Format: 2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	selected := map[string]struct{}{}
	for id := range state.Entries {
		selected[id] = struct{}{}
	}
	encoded, err := mergeSelectedBWSSyncState(base, state, selected)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func maintenanceEntryID(state *bwsSyncStateV2, key string) string {
	for id, entry := range state.Entries {
		if entry.Key == key {
			return id
		}
	}
	return ""
}

func maintenanceRawObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	result, err := rawObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func maintenancePlanForIdentity(t *testing.T, identity syncIdentity) *syncPlanV2 {
	t.Helper()
	plan := &syncPlanV2{Format: 2, Operation: syncOperationPush, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("b", 64), Actions: []syncPlannedAction{}, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
