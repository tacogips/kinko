package kinko

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type executionTestProvider struct {
	projects      []bwsProject
	secrets       map[string]bwsSecret
	createCalls   int
	updateCalls   int
	deleteCalls   int
	createApplied bool
	getErr        map[string]error
	createErrs    []error
	updateErrs    []error
	deleteErrs    []error
	applyOnError  bool
}

type executionAmbiguousError string

func (err executionAmbiguousError) Error() string              { return string(err) }
func (executionAmbiguousError) MutationOutcomeAmbiguous() bool { return true }

func (provider *executionTestProvider) Capabilities() map[syncCapability]bool {
	return map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true, syncCapabilityValueSafeMutation: true}
}
func (provider *executionTestProvider) ListProjects(context.Context) ([]bwsProject, error) {
	return append([]bwsProject(nil), provider.projects...), nil
}
func (provider *executionTestProvider) ListSecrets(context.Context, string) ([]bwsSecret, error) {
	result := make([]bwsSecret, 0, len(provider.secrets))
	for _, secret := range provider.secrets {
		result = append(result, secret)
	}
	return result, nil
}
func (provider *executionTestProvider) GetSecret(_ context.Context, id string) (bwsSecret, error) {
	if err := provider.getErr[id]; err != nil {
		return bwsSecret{}, err
	}
	secret, ok := provider.secrets[id]
	if !ok {
		return bwsSecret{}, errBWSSyncSecretNotFound
	}
	return secret, nil
}
func (provider *executionTestProvider) CreateSecret(_ context.Context, request bwsMutationRequest) (bwsSecret, error) {
	provider.createCalls++
	secret := bwsSecret{ID: fmt.Sprintf("created-%d", provider.createCalls), OrganizationID: "org", ProjectID: request.ProjectID, Key: request.Name, Value: request.Value, Note: request.Note, RevisionDate: fmt.Sprintf("created-revision-%d", provider.createCalls)}
	var resultErr error
	if len(provider.createErrs) > 0 {
		resultErr, provider.createErrs = provider.createErrs[0], provider.createErrs[1:]
	}
	if resultErr == nil || provider.applyOnError || provider.createApplied {
		provider.secrets[secret.ID] = secret
	}
	if provider.createApplied {
		return bwsSecret{}, executionAmbiguousError("ambiguous transport failure")
	}
	if resultErr != nil {
		return bwsSecret{}, resultErr
	}
	return secret, nil
}
func (provider *executionTestProvider) UpdateSecret(_ context.Context, request bwsMutationRequest) (bwsSecret, error) {
	provider.updateCalls++
	secret := provider.secrets[request.SecretID]
	secret.Key, secret.Value, secret.Note, secret.RevisionDate = request.Name, request.Value, request.Note, "updated-revision"
	var resultErr error
	if len(provider.updateErrs) > 0 {
		resultErr, provider.updateErrs = provider.updateErrs[0], provider.updateErrs[1:]
	}
	if resultErr == nil || provider.applyOnError {
		provider.secrets[secret.ID] = secret
	}
	if resultErr != nil {
		return bwsSecret{}, resultErr
	}
	return secret, nil
}
func (provider *executionTestProvider) DeleteSecret(_ context.Context, id string) error {
	provider.deleteCalls++
	var resultErr error
	if len(provider.deleteErrs) > 0 {
		resultErr, provider.deleteErrs = provider.deleteErrs[0], provider.deleteErrs[1:]
	}
	if resultErr == nil || provider.applyOnError {
		delete(provider.secrets, id)
	}
	return resultErr
}

func TestSyncCheckpointCodecIsValueFreeAndFailsClosed(t *testing.T) {
	plan := executionCreatePlan(t, "sensitive-value-canary")
	checkpoint, err := newSyncCheckpoint(plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sensitive-value-canary", "access-token-canary"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("checkpoint leaked forbidden material: %s", encoded)
		}
	}
	decoded, err := decodeSyncCheckpoint(encoded)
	if err != nil || validateSyncCheckpoint(&decoded, plan) != nil {
		t.Fatalf("checkpoint round trip failed: %v", err)
	}
	changed := cloneSyncPlanV2(plan)
	changed.SelectorDigest = strings.Repeat("b", 64)
	if err := validateSyncCheckpoint(&decoded, changed); err == nil {
		t.Fatal("changed input unexpectedly resumed")
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	fields["unknown"] = true
	tampered, _ := json.Marshal(fields)
	if _, err := decodeSyncCheckpoint(tampered); err == nil {
		t.Fatal("unknown checkpoint field was accepted")
	}
}

func TestReconcileAmbiguousMutationsUsesExactRules(t *testing.T) {
	plan := executionCreatePlan(t, "create-canary")
	action := plan.Actions[0]
	provider := &executionTestProvider{secrets: map[string]bwsSecret{}}
	if _, retry, err := reconcileAmbiguousMutation(context.Background(), provider, action); err != nil || !retry {
		t.Fatalf("absent create reconciliation=(retry=%t, err=%v)", retry, err)
	}
	name, note, _ := intendedRemoteMetadata(action.Identity)
	provider.secrets["one"] = bwsSecret{ID: "one", OrganizationID: "org", ProjectID: "project", Key: name, Value: "create-canary", Note: note, RevisionDate: "r1"}
	result, retry, err := reconcileAmbiguousMutation(context.Background(), provider, action)
	if err != nil || retry || result.SecretID != "one" {
		t.Fatalf("exact create reconciliation=(%+v,%t,%v)", result, retry, err)
	}
	provider.secrets["two"] = bwsSecret{ID: "two", OrganizationID: "org", ProjectID: "project", Key: name, Value: "create-canary", Note: note, RevisionDate: "r2"}
	if _, _, err := reconcileAmbiguousMutation(context.Background(), provider, action); err == nil {
		t.Fatal("multiple create matches were accepted")
	}

	deleteAction := action
	deleteAction.Kind = syncActionDelete
	deleteAction.Precondition = &syncPrecondition{SecretID: "missing"}
	if result, retry, err := reconcileAmbiguousMutation(context.Background(), provider, deleteAction); err != nil || retry || result.SecretID != "missing" {
		t.Fatalf("missing delete reconciliation=(%+v,%t,%v)", result, retry, err)
	}
}

func executionCreatePlan(t *testing.T, value string) *syncPlanV2 {
	t.Helper()
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "EXEC_KEY"}
	name, note, err := intendedRemoteMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	plan := &syncPlanV2{Format: syncPlanFormatV2, Operation: syncOperationPush, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("c", 64), Actions: []syncPlannedAction{{EntryID: syncEntryID(identity), Kind: syncActionCreate, Identity: identity, RequiredCapabilities: []syncCapability{syncCapabilityRead, syncCapabilityValueSafeMutation}, LocalPresent: true, IntendedName: name, IntendedNoteSHA256: valueSHA256(note), IntendedValueSHA256: valueSHA256(value)}}, Conflicts: []syncConflict{}}
	if err := finalizeSyncPlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
