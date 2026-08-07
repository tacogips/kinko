package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

const bootstrapSourceMachineID = "fedcba9876543210"

type bootstrapTestProvider struct {
	secrets     map[string]bwsSecret
	getCalls    int
	failGetAt   int
	createCalls int
	updateCalls int
	deleteCalls int
}

func (provider *bootstrapTestProvider) Capabilities() map[syncCapability]bool {
	return map[syncCapability]bool{syncCapabilityRead: true}
}
func (*bootstrapTestProvider) ListProjects(context.Context) ([]bwsProject, error) {
	return []bwsProject{{ID: "project"}}, nil
}
func (provider *bootstrapTestProvider) ListSecrets(context.Context, string) ([]bwsSecret, error) {
	result := make([]bwsSecret, 0, len(provider.secrets))
	for _, secret := range provider.secrets {
		result = append(result, secret)
	}
	return result, nil
}
func (provider *bootstrapTestProvider) GetSecret(_ context.Context, id string) (bwsSecret, error) {
	provider.getCalls++
	if provider.getCalls == provider.failGetAt {
		return bwsSecret{}, errors.New("injected bootstrap read interruption")
	}
	secret, ok := provider.secrets[id]
	if !ok {
		return bwsSecret{}, errBWSSyncSecretNotFound
	}
	return secret, nil
}
func (provider *bootstrapTestProvider) CreateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	provider.createCalls++
	return bwsSecret{}, errors.New("bootstrap attempted create")
}
func (provider *bootstrapTestProvider) UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	provider.updateCalls++
	return bwsSecret{}, errors.New("bootstrap attempted update")
}
func (provider *bootstrapTestProvider) DeleteSecret(context.Context, string) error {
	provider.deleteCalls++
	return errors.New("bootstrap attempted delete")
}

func TestBuildBootstrapPlanPinsSelectionMapsAndIdentitiesWithoutValues(t *testing.T) {
	const canary = "bootstrap-source-value-canary"
	root := filepath.Join(t.TempDir(), "mapped-root")
	shared := bootstrapSharedSecret(t, "source-shared", "SHARED_KEY", canary)
	logical := bootstrapLogicalSecret(t, "source-logical", "work/project", "PATH_KEY", "path-value")
	excluded := bootstrapSharedSecret(t, "source-excluded", "EXCLUDED_KEY", "excluded-value")
	foreign := bootstrapSharedSecretForMachine(t, "foreign", syncTestMachineID, "FOREIGN_KEY", "foreign-value")
	options := bootstrapOptions(false)
	options.Selector.IncludeKeys = []string{"SHARED_KEY", "PATH_KEY"}
	options.PathMaps = []syncPathMap{{Anchor: "work", Root: root}}
	plan, err := buildBootstrapPlan([]bwsSecret{foreign, excluded, logical, shared}, executionDataForValues(map[string]string{sharedKeyBWSAccessToken: "reserved-token"}), options, bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 2 || len(plan.ReadSet) != 2 || plan.SourceMachineID != bootstrapSourceMachineID || plan.TargetMachineID != syncTestMachineID {
		t.Fatalf("plan=%+v", plan)
	}
	for _, action := range plan.Actions {
		if action.Identity.MachineID != syncTestMachineID || action.Precondition.MachineID != bootstrapSourceMachineID {
			t.Fatalf("identity takeover in action: %+v", action)
		}
		if action.Identity.Key == "PATH_KEY" && action.Identity.Path != "local:"+filepath.Join(root, "project") {
			t.Fatalf("logical path was not mapped: %+v", action.Identity)
		}
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{canary, "path-value", "excluded-value", "foreign-value", "reserved-token"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("bootstrap plan leaked %q: %s", forbidden, encoded)
		}
	}
	reversed, err := buildBootstrapPlan([]bwsSecret{shared, logical, excluded, foreign}, executionDataForValues(map[string]string{sharedKeyBWSAccessToken: "reserved-token"}), options, bootstrapRuntime())
	if err != nil || reversed.PlanDigest != plan.PlanDigest {
		t.Fatalf("bootstrap plan is not deterministic: err=%v first=%s second=%s", err, plan.PlanDigest, reversed.PlanDigest)
	}
	emptySelection := options
	emptySelection.Selector.IncludeKeys = []string{"MISSING_KEY"}
	if _, err := buildBootstrapPlan([]bwsSecret{shared}, executionDataForValues(nil), emptySelection, bootstrapRuntime()); err == nil {
		t.Fatal("empty effective bootstrap selection was accepted")
	}
}

func TestBuildBootstrapPlanEnforcesEmptyTargetMergeAndExactConflicts(t *testing.T) {
	remote := []bwsSecret{bootstrapSharedSecret(t, "source", "CONFLICT_KEY", "source-value")}
	data := executionDataForValues(map[string]string{"CONFLICT_KEY": "target-value"})
	options := bootstrapOptions(false)
	if _, err := buildBootstrapPlan(remote, data, options, bootstrapRuntime()); err == nil || !strings.Contains(err.Error(), "--merge") {
		t.Fatalf("non-empty target without merge err=%v", err)
	}
	options.Merge = true
	preview, err := buildBootstrapPlan(remote, data, options, bootstrapRuntime())
	if err != nil || len(preview.Conflicts) != 1 || preview.Actions[0].Kind != syncActionConflict {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	entryID := preview.Conflicts[0].EntryID
	for _, test := range []struct {
		name       string
		resolution syncResolution
		wantKind   syncActionKind
	}{{"source wins", syncResolveRemote, syncActionUpdate}, {"target wins", syncResolveLocal, syncActionIgnore}, {"skip", syncResolveSkip, syncActionIgnore}} {
		t.Run(test.name, func(t *testing.T) {
			resolved := options
			resolved.Resolutions = map[string]syncResolution{entryID: test.resolution}
			plan, err := buildBootstrapPlan(remote, data, resolved, bootstrapRuntime())
			if err != nil || len(plan.Conflicts) != 0 || plan.Actions[0].Kind != test.wantKind || plan.Actions[0].Resolution != test.resolution {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
		})
	}
	options.Resolutions = map[string]syncResolution{strings.Repeat("d", 64): syncResolveRemote}
	if _, err := buildBootstrapPlan(remote, data, options, bootstrapRuntime()); err == nil {
		t.Fatal("unmatched bootstrap resolution was accepted")
	}
	options.Resolutions = nil
	options.ConflictPolicy = syncConflictRemote
	plan, err := buildBootstrapPlan(remote, data, options, bootstrapRuntime())
	if err != nil || plan.Actions[0].Kind != syncActionUpdate {
		t.Fatalf("remote conflict policy plan=%+v err=%v", plan, err)
	}
}

func TestApplyBootstrapPlanRegetsAllSourcesAndPublishesAtomically(t *testing.T) {
	remote := []bwsSecret{bootstrapSharedSecret(t, "first", "FIRST_KEY", "one"), bootstrapSharedSecret(t, "second", "SECOND_KEY", "two")}
	data := executionDataForValues(map[string]string{"UNSELECTED": "preserved"})
	plan, err := buildBootstrapPlan(remote, data, bootstrapOptions(true), bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	provider := &bootstrapTestProvider{secrets: indexBootstrapSecrets(remote), failGetAt: 2}
	before, _ := json.Marshal(data)
	if err := applyBootstrapPlan(context.Background(), provider, plan, data); err == nil {
		t.Fatal("source interruption was accepted")
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatalf("interrupted bootstrap published partial data: before=%s after=%s", before, after)
	}

	provider = &bootstrapTestProvider{secrets: indexBootstrapSecrets(remote)}
	data = executionDataForValues(map[string]string{"UNSELECTED": "preserved"})
	if err := applyBootstrapPlan(context.Background(), provider, plan, data); err != nil {
		t.Fatal(err)
	}
	if provider.getCalls != len(remote) || provider.createCalls+provider.updateCalls+provider.deleteCalls != 0 || data.Shared["FIRST_KEY"] != "one" || data.Shared["SECOND_KEY"] != "two" || data.Shared["UNSELECTED"] != "preserved" {
		t.Fatalf("provider=%+v data=%+v", provider, data)
	}
}

func TestBootstrapSourceDriftTargetDriftAndUnresolvedConflictFailClosed(t *testing.T) {
	secret := bootstrapSharedSecret(t, "source", "DRIFT_KEY", "source")
	plan, err := buildBootstrapPlan([]bwsSecret{secret}, executionDataForValues(nil), bootstrapOptions(false), bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	drifted := secret
	drifted.RevisionDate = "changed"
	if err := applyBootstrapPlan(context.Background(), &bootstrapTestProvider{secrets: map[string]bwsSecret{secret.ID: drifted}}, plan, executionDataForValues(nil)); err == nil {
		t.Fatal("source revision drift was accepted")
	}
	target := executionDataForValues(map[string]string{"DRIFT_KEY": "appeared-after-preview"})
	if err := applyBootstrapPlan(context.Background(), &bootstrapTestProvider{secrets: map[string]bwsSecret{secret.ID: secret}}, plan, target); err == nil {
		t.Fatal("target drift was accepted")
	}

	options := bootstrapOptions(true)
	conflictPlan, err := buildBootstrapPlan([]bwsSecret{secret}, executionDataForValues(map[string]string{"DRIFT_KEY": "local"}), options, bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBootstrapPlan(context.Background(), &bootstrapTestProvider{secrets: map[string]bwsSecret{secret.ID: secret}}, conflictPlan, executionDataForValues(map[string]string{"DRIFT_KEY": "local"})); err == nil {
		t.Fatal("unresolved conflict was applied")
	}
}

func TestBootstrapLaterPushCreatesTargetMachineRecords(t *testing.T) {
	secret := bootstrapSharedSecret(t, "source", "RECOVERED_KEY", "recovered")
	plan, err := buildBootstrapPlan([]bwsSecret{secret}, executionDataForValues(nil), bootstrapOptions(false), bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	data := executionDataForValues(nil)
	provider := &bootstrapTestProvider{secrets: map[string]bwsSecret{secret.ID: secret}}
	if err := applyBootstrapPlan(context.Background(), provider, plan, data); err != nil {
		t.Fatal(err)
	}
	entries, err := collectSyncEntries(data)
	if err != nil {
		t.Fatal(err)
	}
	runtime := bootstrapRuntime()
	pushContext := syncPlanContext{ProviderIdentity: runtime.ProviderIdentity, Endpoint: endpointString(runtime.Endpoints.APIURL), OrganizationID: runtime.OrganizationID, ProjectID: runtime.ProjectID, MachineID: syncTestMachineID}
	push, err := buildSyncPlanV2WithContext(syncOperationPush, entries, []bwsSecret{secret}, syncStateEnvelope{}, syncSelector{}, pushContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(push.Actions) != 1 || push.Actions[0].Kind != syncActionCreate || push.Actions[0].Precondition != nil || push.Actions[0].Identity.MachineID != syncTestMachineID {
		t.Fatalf("later push could reuse a source id: %+v", push)
	}
}

func bootstrapOptions(merge bool) syncBootstrapOptions {
	return syncBootstrapOptions{Provider: supportedSyncProvider, ProjectID: "project", FromMachineID: bootstrapSourceMachineID, TargetMachineID: syncTestMachineID, Merge: merge}
}

func bootstrapRuntime() bwsRuntimeConfig {
	api, _ := url.Parse("https://api.example.test")
	return bwsRuntimeConfig{OrganizationID: "org", ProjectID: "project", ProviderIdentity: strings.Repeat("a", 64), Endpoints: bwsEndpointSet{APIURL: api}}
}

func bootstrapSharedSecret(t *testing.T, id, key, value string) bwsSecret {
	t.Helper()
	return bootstrapSharedSecretForMachine(t, id, bootstrapSourceMachineID, key, value)
}

func bootstrapSharedSecretForMachine(t *testing.T, id, machineID, key, value string) bwsSecret {
	t.Helper()
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: machineID, Scope: scopeKindShared, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	return bwsSecret{ID: id, OrganizationID: "org", ProjectID: "project", Key: buildBWSSecretName(machineID, scopeRef{kind: scopeKindShared}, key), Value: value, Note: note, RevisionDate: "revision-" + id}
}

func bootstrapLogicalSecret(t *testing.T, id, logicalPath, key, value string) bwsSecret {
	t.Helper()
	profile := "default"
	note, err := encodeBWSNoteV2(bwsNoteMetadataV2{KinkoSyncFormat: 2, MachineID: bootstrapSourceMachineID, Profile: profile, Scope: scopeKindPath, LogicalPath: logicalPath, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	name := buildBWSSecretNameV2(bootstrapSourceMachineID, logicalScopeRef{Profile: profile, Kind: scopeKindPath, LogicalPath: logicalPath}, key)
	return bwsSecret{ID: id, OrganizationID: "org", ProjectID: "project", Key: name, Value: value, Note: note, RevisionDate: "revision-" + id}
}

func indexBootstrapSecrets(secrets []bwsSecret) map[string]bwsSecret {
	result := make(map[string]bwsSecret, len(secrets))
	for _, secret := range secrets {
		result[secret.ID] = secret
	}
	return result
}
