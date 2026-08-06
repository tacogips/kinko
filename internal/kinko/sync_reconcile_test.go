package kinko

import (
	"strings"
	"testing"
)

func TestInferMaintenanceContextCoalescesEmptyAndNonEmptyOrganization(t *testing.T) {
	withOrg := validSyncStateEntryV2("WITH_ORG")
	withOrg.OrganizationID = "observed-org"
	withoutOrg := validSyncStateEntryV2("WITHOUT_ORG")
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{
		syncEntryID(identityForStateEntry(withOrg)):    withOrg,
		syncEntryID(identityForStateEntry(withoutOrg)): withoutOrg,
	}, Ownership: map[string]syncOwnershipRecord{}}
	got, err := inferMaintenanceContext(state)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrganizationID != "observed-org" {
		t.Fatalf("organization=%q, want %q", got.OrganizationID, "observed-org")
	}
}

func TestInferMaintenanceContextTwoDifferentNonEmptyOrganizationsStillErrors(t *testing.T) {
	first := validSyncStateEntryV2("FIRST_ORG")
	first.OrganizationID = "org-one"
	second := validSyncStateEntryV2("SECOND_ORG")
	second.OrganizationID = "org-two"
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{
		syncEntryID(identityForStateEntry(first)):  first,
		syncEntryID(identityForStateEntry(second)): second,
	}, Ownership: map[string]syncOwnershipRecord{}}
	if _, err := inferMaintenanceContext(state); err == nil || !strings.Contains(err.Error(), "more than one provider context") {
		t.Fatalf("err=%v", err)
	}
}

// TestBuildSyncReconcilePlanTracksMixedOrganizationStateFromCreateThenUpdate
// reproduces the live-verified bug: a push that creates secrets while no BWS
// organization is configured records OrganizationID "" for the created entry,
// while a later pull that updates another entry pins the organization
// actually observed on the remote secret (preconditionForSecret). The
// resulting state mixes "" and a real organization across entries. Reconcile
// must still build a plan instead of failing with "more than one provider
// context".
func TestBuildSyncReconcilePlanTracksMixedOrganizationStateFromCreateThenUpdate(t *testing.T) {
	providerIdentity := strings.Repeat("a", 64)
	contextValue := syncPlanContext{ProviderIdentity: providerIdentity, Endpoint: "https://api.example.invalid", ProjectID: "project", MachineID: syncTestMachineID}
	createdIdentity := syncIdentity{Provider: providerIdentity, ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "CREATED_KEY"}
	updatedIdentity := syncIdentity{Provider: providerIdentity, ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "UPDATED_KEY"}
	createdName, createdNote, err := intendedRemoteMetadata(createdIdentity)
	if err != nil {
		t.Fatal(err)
	}
	updatedName, updatedNote, err := intendedRemoteMetadata(updatedIdentity)
	if err != nil {
		t.Fatal(err)
	}
	createdSecret := bwsSecret{ID: "created-id", OrganizationID: "real-org", ProjectID: "project", Key: createdName, Value: "created-value", Note: createdNote, RevisionDate: "created-revision"}
	updatedSecret := bwsSecret{ID: "updated-id", OrganizationID: "real-org", ProjectID: "project", Key: updatedName, Value: "updated-value", Note: updatedNote, RevisionDate: "updated-revision"}
	state := &bwsSyncStateV2{
		Format:  bwsSyncStateFormatV2,
		Context: &contextValue,
		Entries: map[string]syncStateEntryV2{
			// Recorded by a create with no organization configured: matches the
			// pre-fix updateV2StateForConfirmedAction fallback to state.Context.
			syncEntryID(createdIdentity): {
				Schema: syncStateEntrySchemaV2, ProviderIdentity: providerIdentity, Endpoint: contextValue.Endpoint,
				OrganizationID: "", ProjectID: "project", MachineID: syncTestMachineID,
				SecretID: createdSecret.ID, Name: createdSecret.Key, Revision: createdSecret.RevisionDate,
				Key: "CREATED_KEY", ValueSHA256: valueSHA256(createdSecret.Value), Scope: scopeKindShared,
			},
			// Recorded by an update through a precondition: pins the organization
			// actually observed on the remote secret (preconditionForSecret).
			syncEntryID(updatedIdentity): {
				Schema: syncStateEntrySchemaV2, ProviderIdentity: providerIdentity, Endpoint: contextValue.Endpoint,
				OrganizationID: "real-org", ProjectID: "project", MachineID: syncTestMachineID,
				SecretID: updatedSecret.ID, Name: updatedSecret.Key, Revision: updatedSecret.RevisionDate,
				Key: "UPDATED_KEY", ValueSHA256: valueSHA256(updatedSecret.Value), Scope: scopeKindShared,
			},
		},
		Ownership: map[string]syncOwnershipRecord{},
	}
	envelope := inferSyncPlanContextTestEnvelope(t, state)
	entries := []syncEntry{
		{ref: scopeRef{kind: scopeKindShared}, key: "CREATED_KEY", value: createdSecret.Value},
		{ref: scopeRef{kind: scopeKindShared}, key: "UPDATED_KEY", value: updatedSecret.Value},
	}
	plan, err := buildSyncReconcilePlan(entries, []bwsSecret{createdSecret, updatedSecret}, envelope, syncReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile plan build failed on mixed-organization state: %v", err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("actions=%+v, want 2", plan.Actions)
	}
	for _, action := range plan.Actions {
		if action.Kind != syncActionAdopt {
			t.Fatalf("action=%+v, want adopt", action)
		}
	}
}
