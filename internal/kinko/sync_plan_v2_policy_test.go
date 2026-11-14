package kinko

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSyncPlanV2OwnershipProofRequiresExactRevision(t *testing.T) {
	planContext, secret, identity := planningPolicyFixture(t)
	for _, test := range []struct {
		name     string
		revision string
		allowed  bool
	}{
		{name: "confirmed revision", revision: secret.RevisionDate, allowed: true},
		{name: "changed revision", revision: "older-revision", allowed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			envelope := ownershipEnvelope(t, identity, secret.ID, test.revision)
			plan, err := buildSyncPlanV2WithContext(syncOperationPrune, nil, []bwsSecret{secret}, envelope, syncSelector{}, planContext)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Actions[0].RemoteDeleteAllowed != test.allowed {
				t.Fatalf("remote delete allowed=%v, want %v", plan.Actions[0].RemoteDeleteAllowed, test.allowed)
			}
		})
	}
}

func TestBuildSyncPlanV2RejectsOwnershipAcrossPinnedBoundary(t *testing.T) {
	planContext, secret, identity := planningPolicyFixture(t)
	identity.ProjectID = "different-project"
	envelope := ownershipEnvelope(t, identity, secret.ID, secret.RevisionDate)
	if _, err := buildSyncPlanV2WithContext(syncOperationPrune, nil, []bwsSecret{secret}, envelope, syncSelector{}, planContext); err == nil || !strings.Contains(err.Error(), "boundary") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildSyncPlanV2SelectorFenceAndEmptyMutation(t *testing.T) {
	planContext, _, _ := planningPolicyFixture(t)
	entries := []syncEntry{
		{ref: scopeRef{kind: scopeKindShared}, key: "INCLUDED_KEY", value: "included-canary"},
		{ref: scopeRef{kind: scopeKindShared}, key: "EXCLUDED_KEY", value: "excluded-sensitive-canary"},
	}
	plan, err := buildSyncPlanV2WithContext(syncOperationPush, entries, nil, syncStateEnvelope{}, syncSelector{IncludeKeys: []string{"INCLUDED_KEY"}}, planContext)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	if len(plan.Actions) != 1 || strings.Contains(string(encoded), "EXCLUDED_KEY") || strings.Contains(string(encoded), "excluded-sensitive-canary") {
		t.Fatalf("selector fence failed: %s", encoded)
	}
	_, err = buildSyncPlanV2WithContext(syncOperationPush, entries, nil, syncStateEnvelope{}, syncSelector{IncludeKeys: []string{"MISSING_KEY"}}, planContext)
	if err == nil || !strings.Contains(err.Error(), "selection is empty") {
		t.Fatalf("empty mutation error=%v", err)
	}
}

func TestCapabilitiesForPlannedActionSeparatesDeleteFromValueMutation(t *testing.T) {
	deleteCaps := capabilitiesForPlannedAction(syncPlannedAction{Kind: syncActionDelete}, syncOperationPush)
	if len(deleteCaps) != 1 || deleteCaps[0] != syncCapabilityDelete {
		t.Fatalf("delete capabilities=%v", deleteCaps)
	}
	updateCaps := capabilitiesForPlannedAction(syncPlannedAction{Kind: syncActionUpdate}, syncOperationPush)
	if !containsSyncCapability(updateCaps, syncCapabilityValueSafeMutation) || containsSyncCapability(updateCaps, syncCapabilityDelete) {
		t.Fatalf("update capabilities=%v", updateCaps)
	}
}

func TestValidatePinnedSyncPlanDeleteNeedsDeleteCapabilityOnly(t *testing.T) {
	planContext, secret, identity := planningPolicyFixture(t)
	plan, err := buildSyncPlanV2WithContext(
		syncOperationPush,
		nil,
		[]bwsSecret{secret},
		legacyPlanningEnvelope(t, secret, identity),
		syncSelector{},
		planContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Actions[0].Kind != syncActionDelete {
		t.Fatalf("legacy baseline action=%v, want delete", plan.Actions[0].Kind)
	}
	provider := &planTestProvider{
		secret:       secret,
		projects:     []bwsProject{{ID: secret.ProjectID}},
		capabilities: map[syncCapability]bool{syncCapabilityDelete: true},
	}
	if err := validatePinnedSyncPlan(plan, provider); err != nil {
		t.Fatalf("delete-only provider rejected: %v", err)
	}
}

func TestValidatePinnedSyncPlanCapabilityFailurePrecedesProviderRead(t *testing.T) {
	planContext, _, _ := planningPolicyFixture(t)
	plan, err := buildSyncPlanV2WithContext(syncOperationPush, []syncEntry{{ref: scopeRef{kind: scopeKindShared}, key: "CREATE_KEY", value: "sensitive-create-canary"}}, nil, syncStateEnvelope{}, syncSelector{}, planContext)
	if err != nil {
		t.Fatal(err)
	}
	provider := &planTestProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true}}
	err = validatePinnedSyncPlan(plan, provider)
	if err == nil || !strings.Contains(err.Error(), string(syncCapabilityValueSafeMutation)) || strings.Contains(err.Error(), "sensitive-create-canary") {
		t.Fatalf("capability error=%v", err)
	}
}

func TestApplySyncPruneAcknowledgementsExactAndAtomic(t *testing.T) {
	planContext, secret, _ := planningPolicyFixture(t)
	plan, err := buildSyncPlanV2WithContext(syncOperationPrune, nil, []bwsSecret{secret}, syncStateEnvelope{}, syncSelector{}, planContext)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := plan.PlanDigest
	if err := applySyncPruneAcknowledgements(plan, []string{secret.ID, "unmatched-id"}); err == nil || plan.PlanDigest != originalDigest {
		t.Fatalf("failed acknowledgement mutated plan: digest=%s error=%v", plan.PlanDigest, err)
	}
	if err := applySyncPruneAcknowledgements(plan, []string{secret.ID, secret.ID}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate acknowledgement error=%v", err)
	}
	if err := applySyncPruneAcknowledgements(plan, []string{secret.ID}); err != nil {
		t.Fatal(err)
	}
	action := plan.Actions[0]
	if action.Kind != syncActionDelete || !action.RemoteDeleteAllowed || !containsSyncCapability(action.RequiredCapabilities, syncCapabilityDelete) || containsSyncCapability(action.RequiredCapabilities, syncCapabilityValueSafeMutation) {
		t.Fatalf("acknowledged action=%+v", action)
	}
}

func planningPolicyFixture(t *testing.T) (syncPlanContext, bwsSecret, syncIdentity) {
	t.Helper()
	planContext := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "REMOTE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "immutable-secret-id", OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, "REMOTE_KEY"), Note: note, Value: "remote-sensitive-canary", RevisionDate: "confirmed-revision"}
	identity := syncIdentity{Provider: planContext.ProviderIdentity, ProjectID: planContext.ProjectID, MachineID: planContext.MachineID, Scope: scopeKindShared, Key: "REMOTE_KEY"}
	return planContext, secret, identity
}

func ownershipEnvelope(t *testing.T, identity syncIdentity, secretID, revision string) syncStateEnvelope {
	t.Helper()
	id := syncEntryID(identity)
	state := bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{id: {SecretID: secretID, ProviderIdentity: identity.Provider, Revision: revision, Identity: identity}}}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func legacyPlanningEnvelope(t *testing.T, secret bwsSecret, identity syncIdentity) syncStateEnvelope {
	t.Helper()
	state := bwsSyncState{
		Format:    1,
		MachineID: identity.MachineID,
		ProjectID: identity.ProjectID,
		Entries: map[string]syncStateEntry{
			secret.Key: {
				SecretID: secret.ID, Name: secret.Key, Scope: identity.Scope, Key: identity.Key,
				RevisionDate: secret.RevisionDate, ValueSHA256: valueSHA256(secret.Value),
			},
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func containsSyncCapability(capabilities []syncCapability, wanted syncCapability) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}
