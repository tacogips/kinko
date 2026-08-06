package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBuildSyncPlanV2DeterministicValueFreeAndPinned(t *testing.T) {
	ctx := syncPlanContext{
		ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid",
		OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID,
	}
	entries := []syncEntry{
		{ref: scopeRef{kind: scopeKindShared}, key: "SECOND_KEY", value: "do-not-serialize-second"},
		{ref: scopeRef{kind: scopeKindShared}, key: "FIRST_KEY", value: "do-not-serialize-first"},
	}
	first, err := buildSyncPlanV2WithContext(syncOperationPush, entries, nil, syncStateEnvelope{}, syncSelector{}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildSyncPlanV2WithContext(syncOperationPush, []syncEntry{entries[1], entries[0]}, nil, syncStateEnvelope{}, syncSelector{}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanDigest != second.PlanDigest || len(first.Actions) != 2 {
		t.Fatalf("nondeterministic plans: first=%s second=%s", first.PlanDigest, second.PlanDigest)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"do-not-serialize", "token", "value\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("plan contains forbidden plaintext or field %q: %s", forbidden, encoded)
		}
	}
	for _, action := range first.Actions {
		if len(action.ActionID) != 64 || len(action.EntryID) != 64 {
			t.Fatalf("action is not fully pinned: %+v", action)
		}
	}
}

func TestBuildSyncPlanV2RejectsMalformedBeforeSelectorExclusion(t *testing.T) {
	ctx := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", ProjectID: "project", MachineID: syncTestMachineID}
	secret := bwsSecret{ID: "malformed-id", ProjectID: "project", Key: syncTestMachineID + "_deadbeef_HIDDEN_KEY", Note: "not-json", Value: "must-be-discarded", RevisionDate: "one"}
	selector := syncSelector{ExcludeKeys: []string{"HIDDEN_KEY"}}
	if _, err := buildSyncPlanV2WithContext(syncOperationPush, nil, []bwsSecret{secret}, syncStateEnvelope{}, selector, ctx); err == nil || !strings.Contains(err.Error(), "malformed-id") {
		t.Fatalf("malformed excluded metadata error=%v", err)
	}
}

func TestBuildSyncPlanV2PinsRemoteContentHashes(t *testing.T) {
	ctx := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID}
	ref := scopeRef{kind: scopeKindShared}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "PIN_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "secret-id", OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, ref, "PIN_KEY"), Note: note, Value: "remote-secret-value", RevisionDate: "revision-one"}
	plan, err := buildSyncPlanV2WithContext(syncOperationPull, nil, []bwsSecret{secret}, syncStateEnvelope{}, syncSelector{}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	precondition := plan.Actions[0].Precondition
	if precondition == nil || precondition.SecretID != secret.ID || precondition.Name != secret.Key || precondition.Revision != secret.RevisionDate || len(precondition.NoteSHA256) != 64 || len(precondition.ValueSHA256) != 64 {
		t.Fatalf("precondition=%+v", precondition)
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), secret.Value) || strings.Contains(string(encoded), note) {
		t.Fatalf("plan leaked remote content: %s", encoded)
	}
}

func TestValidatePinnedSyncPlanDetectsRevisionAndMembershipChanges(t *testing.T) {
	ctx := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", OrganizationID: "org", ProjectID: "project", MachineID: syncTestMachineID}
	note, _ := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "PIN_KEY"})
	secret := bwsSecret{ID: "secret-id", OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, "PIN_KEY"), Note: note, Value: "remote", RevisionDate: "one"}
	plan, err := buildSyncPlanV2WithContext(syncOperationPull, nil, []bwsSecret{secret}, syncStateEnvelope{}, syncSelector{}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	provider := &planTestProvider{secret: secret, projects: []bwsProject{{ID: "project"}}}
	if err := validatePinnedSyncPlan(plan, provider); err != nil {
		t.Fatal(err)
	}
	provider.secret.RevisionDate = "two"
	if err := validatePinnedSyncPlan(plan, provider); err == nil {
		t.Fatal("changed revision was accepted")
	}
	provider.secret = secret
	provider.projects = []bwsProject{{ID: "different"}}
	if err := validatePinnedSyncPlan(plan, provider); err == nil {
		t.Fatal("unassigned project was accepted")
	}
}

func TestPreconditionForSecretPinsObservedOrganizationWhenUnconfigured(t *testing.T) {
	ctx := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", ProjectID: "project", MachineID: syncTestMachineID}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "PIN_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "secret-id", OrganizationID: "observed-org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, "PIN_KEY"), Note: note, Value: "remote", RevisionDate: "one"}
	precondition := preconditionForSecret(secret, ctx)
	if precondition.OrganizationID != secret.OrganizationID {
		t.Fatalf("precondition organization = %q, want secret organization %q", precondition.OrganizationID, secret.OrganizationID)
	}
	if err := validateSecretPrecondition(precondition, secret); err != nil {
		t.Fatalf("precondition rejected unchanged secret: %v", err)
	}
}

func TestPreconditionForSecretPinsConfiguredOrganizationVerbatim(t *testing.T) {
	ctx := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", OrganizationID: "configured-org", ProjectID: "project", MachineID: syncTestMachineID}
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "PIN_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	secret := bwsSecret{ID: "secret-id", OrganizationID: "configured-org", ProjectID: "project", Key: buildBWSSecretName(syncTestMachineID, scopeRef{kind: scopeKindShared}, "PIN_KEY"), Note: note, Value: "remote", RevisionDate: "one"}
	precondition := preconditionForSecret(secret, ctx)
	if precondition.OrganizationID != ctx.OrganizationID {
		t.Fatalf("precondition organization = %q, want configured organization %q", precondition.OrganizationID, ctx.OrganizationID)
	}
	if err := validateSecretPrecondition(precondition, secret); err != nil {
		t.Fatalf("precondition rejected unchanged secret: %v", err)
	}
}

func TestInferSyncPlanContextCoalescesEmptyAndNonEmptyOrganization(t *testing.T) {
	withOrg := validSyncStateEntryV2("WITH_ORG")
	withOrg.OrganizationID = "observed-org"
	withoutOrg := validSyncStateEntryV2("WITHOUT_ORG")
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{
		syncEntryID(identityForStateEntry(withOrg)):    withOrg,
		syncEntryID(identityForStateEntry(withoutOrg)): withoutOrg,
	}, Ownership: map[string]syncOwnershipRecord{}}
	ctx, err := inferSyncPlanContext(nil, inferSyncPlanContextTestEnvelope(t, state))
	if err != nil {
		t.Fatal(err)
	}
	if ctx.OrganizationID != "observed-org" {
		t.Fatalf("organization=%q, want %q", ctx.OrganizationID, "observed-org")
	}
}

func TestInferSyncPlanContextTwoDifferentNonEmptyOrganizationsStillErrors(t *testing.T) {
	first := validSyncStateEntryV2("FIRST_ORG")
	first.OrganizationID = "org-one"
	second := validSyncStateEntryV2("SECOND_ORG")
	second.OrganizationID = "org-two"
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{
		syncEntryID(identityForStateEntry(first)):  first,
		syncEntryID(identityForStateEntry(second)): second,
	}, Ownership: map[string]syncOwnershipRecord{}}
	if _, err := inferSyncPlanContext(nil, inferSyncPlanContextTestEnvelope(t, state)); err == nil || !strings.Contains(err.Error(), "more than one provider context") {
		t.Fatalf("err=%v", err)
	}
}

func inferSyncPlanContextTestEnvelope(t *testing.T, state *bwsSyncStateV2) syncStateEnvelope {
	t.Helper()
	base := syncStateEnvelope{Format: bwsSyncStateFormatV2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	selected := make(map[string]struct{}, len(state.Entries))
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

type planTestProvider struct {
	secret       bwsSecret
	projects     []bwsProject
	capabilities map[syncCapability]bool
}

func (provider *planTestProvider) Capabilities() map[syncCapability]bool {
	if provider.capabilities != nil {
		return provider.capabilities
	}
	return map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true, syncCapabilityValueSafeMutation: true}
}
func (provider *planTestProvider) ListProjects(context.Context) ([]bwsProject, error) {
	return provider.projects, nil
}
func (*planTestProvider) ListSecrets(context.Context, string) ([]bwsSecret, error) {
	return nil, errors.New("unused")
}
func (provider *planTestProvider) GetSecret(context.Context, string) (bwsSecret, error) {
	return provider.secret, nil
}
func (*planTestProvider) CreateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, errors.New("unused")
}
func (*planTestProvider) UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, errors.New("unused")
}
func (*planTestProvider) DeleteSecret(context.Context, string) error { return errors.New("unused") }
