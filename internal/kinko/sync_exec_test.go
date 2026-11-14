package kinko

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApplyPushPlanPartialStateTracksOnlySuccessfulPrefix(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	entries := []syncEntry{
		{ref: ref, key: "FIRST_KEY", value: "fixture-first"},
		{ref: ref, key: "SECOND_KEY", value: "fixture-second"},
	}
	plan, err := buildPushPlan(entries, nil, emptyBWSSyncState(), syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
			calls++
			if calls == 2 {
				return nil, []byte("fixture-token fixture-second"), errors.New("fixture-second")
			}
			payload, err := json.Marshal(bwsSecret{
				ID:           "fixture-id-one",
				ProjectID:    args[4],
				Key:          args[2],
				Value:        args[3],
				Note:         args[6],
				RevisionDate: "revision-one",
			})
			return payload, nil, err
		},
	}
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if !result.Partial || result.Created != 1 || len(result.Actions) != 1 || len(state.Entries) != 1 {
		t.Fatalf("partial=%t created=%d actions=%d state=%d", result.Partial, result.Created, len(result.Actions), len(state.Entries))
	}
	if strings.Contains(err.Error(), "fixture-token") || strings.Contains(err.Error(), "fixture-second") {
		t.Fatal("provider error leaked a sensitive fixture")
	}
}

func TestApplyPushPlanFirstProviderFailureIsNotPartial(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	plan, err := buildPushPlan([]syncEntry{{ref: ref, key: "FIXTURE_KEY", value: "fixture-value"}}, nil, emptyBWSSyncState(), syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			return nil, nil, errors.New("fixture provider failure")
		},
	}
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
	if err == nil || result.Partial || len(state.Entries) != 0 {
		t.Fatalf("err=%v partial=%t entries=%d", err, result.Partial, len(state.Entries))
	}
}

func TestApplyPushPlanDoesNotPersistMismatchedMutationResponses(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	tests := []struct {
		name  string
		setup func(*testing.T) (*syncPlan, *bwsSyncState)
	}{
		{
			name: "create",
			setup: func(t *testing.T) (*syncPlan, *bwsSyncState) {
				state := emptyBWSSyncState()
				state.MachineID = syncTestMachineID
				state.ProjectID = "fixture-project"
				plan, err := buildPushPlan([]syncEntry{{ref: ref, key: "CREATE_KEY", value: "fixture-create-value"}}, nil, state, syncTestMachineID)
				if err != nil {
					t.Fatal(err)
				}
				return plan, state
			},
		},
		{
			name: "edit",
			setup: func(t *testing.T) (*syncPlan, *bwsSyncState) {
				entry := syncEntry{ref: ref, key: "EDIT_KEY", value: "fixture-edit-value"}
				name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
				remote := syncTestRemote(t, name, ref, entry.key, "fixture-before-edit", "revision-one")
				state := emptyBWSSyncState()
				state.MachineID = syncTestMachineID
				state.ProjectID = "fixture-project"
				state.Entries[name] = syncTestStateEntry(name, ref, entry.key, remote.Value, remote.RevisionDate)
				plan, err := buildPushPlan([]syncEntry{entry}, []bwsSecret{remote}, state, syncTestMachineID)
				if err != nil {
					t.Fatal(err)
				}
				return plan, state
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, state := test.setup(t)
			before := make(map[string]syncStateEntry, len(state.Entries))
			for name, entry := range state.Entries {
				before[name] = entry
			}
			client := &bwsClient{
				binPath: "fixture-bws",
				token:   "fixture-token",
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
					response := bwsSecret{ProjectID: "fixture-project", Key: "WRONG_KEY", RevisionDate: "revision-response"}
					if args[1] == "create" {
						response.ID = "fixture-created-id"
						response.Value = args[3]
						response.Note = args[6]
					} else {
						response.ID = args[2]
						response.Value = args[4]
						response.Note = args[6]
					}
					payload, err := json.Marshal(response)
					return payload, nil, err
				},
			}
			result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
			if !errors.Is(err, errBWSInvalidJSON) || !result.Partial {
				t.Fatalf("error=%v partial=%t", err, result.Partial)
			}
			if !reflect.DeepEqual(state.Entries, before) {
				t.Fatal("mismatched mutation response changed persisted sync state")
			}
		})
	}
}

func TestApplyPushPlanConflictMakesNoProviderOrStateMutation(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	entry := syncEntry{ref: ref, key: "FIXTURE_KEY", value: "fixture-local"}
	name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
	remote := syncTestRemote(t, name, ref, entry.key, "fixture-remote", "revision-two")
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	state.Entries[name] = syncTestStateEntry(name, ref, entry.key, "fixture-baseline", "revision-one")
	wantState := state.Entries[name]
	plan, err := buildPushPlan([]syncEntry{entry}, []bwsSecret{remote}, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			calls++
			return nil, nil, nil
		},
	}
	result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
	if ExitCode(err) != exitCodeSyncConflict || result.Partial || calls != 0 || state.Entries[name] != wantState {
		t.Fatalf("exit=%d partial=%t calls=%d state=%+v", ExitCode(err), result.Partial, calls, state.Entries[name])
	}
}

func TestApplyPushPlanReportsEveryConflictDeterministicallyWithoutMutation(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	entries := []syncEntry{
		{ref: ref, key: "SECOND_KEY", value: "fixture-second-local"},
		{ref: ref, key: "FIRST_KEY", value: "fixture-first-local"},
	}
	remote := make([]bwsSecret, 0, len(entries))
	for _, entry := range entries {
		name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
		item := syncTestRemote(t, name, ref, entry.key, "fixture-remote", "revision-two")
		item.ID = "fixture-id-" + entry.key
		remote = append(remote, item)
		state.Entries[name] = syncTestStateEntry(name, ref, entry.key, "fixture-baseline", "revision-one")
	}
	wantState := make(map[string]syncStateEntry, len(state.Entries))
	for name, entry := range state.Entries {
		wantState[name] = entry
	}
	plan, err := buildPushPlan(entries, remote, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			calls++
			return nil, nil, nil
		},
	}
	result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
	wantConflicts := []string{
		"shared / FIRST_KEY: remote secret changed after the last sync",
		"shared / SECOND_KEY: remote secret changed after the last sync",
	}
	if ExitCode(err) != exitCodeSyncConflict || calls != 0 || !reflect.DeepEqual(result.Conflicts, wantConflicts) || !reflect.DeepEqual(state.Entries, wantState) {
		t.Fatalf("exit=%d calls=%d conflicts=%v stateChanged=%t", ExitCode(err), calls, result.Conflicts, !reflect.DeepEqual(state.Entries, wantState))
	}
}

func TestApplyPullPlanCreatesContainersAndPropagatesDeletion(t *testing.T) {
	path := t.TempDir()
	ref := scopeRef{profile: "new-profile", kind: scopeKindPath, path: path}
	name := buildBWSSecretName(syncTestMachineID, ref, "FIXTURE_KEY")
	remote := syncTestRemote(t, name, ref, "FIXTURE_KEY", "fixture-remote", "revision-one")
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	plan, err := buildPullPlan(nil, []bwsSecret{remote}, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	result, err := applyPullPlan(data, plan, state, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || data.Profiles[ref.profile][ref.path]["FIXTURE_KEY"] != "fixture-remote" || len(state.Entries) != 1 {
		t.Fatal("pull did not create the missing profile/path container and baseline")
	}

	deletePlan, err := buildPullPlan([]syncEntry{{ref: ref, key: "FIXTURE_KEY", value: "fixture-remote"}}, nil, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	result, err = applyPullPlan(data, deletePlan, state, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || len(data.Profiles[ref.profile][ref.path]) != 0 || len(state.Entries) != 0 {
		t.Fatal("pull did not propagate the remote deletion")
	}
}

func TestApplyPullPlanConflictIsAtomicAndForceResolves(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	name := buildBWSSecretName(syncTestMachineID, ref, "FIXTURE_KEY")
	remote := syncTestRemote(t, name, ref, "FIXTURE_KEY", "fixture-remote", "revision-two")
	state := emptyBWSSyncState()
	state.Entries[name] = syncTestStateEntry(name, ref, "FIXTURE_KEY", "fixture-baseline", "revision-one")
	plan, err := buildPullPlan([]syncEntry{{ref: ref, key: "FIXTURE_KEY", value: "fixture-local"}}, []bwsSecret{remote}, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	data := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{"FIXTURE_KEY": "fixture-local"}}
	result, err := applyPullPlan(data, plan, state, false)
	if ExitCode(err) != exitCodeSyncConflict || len(result.Conflicts) != 1 {
		t.Fatalf("exit=%d conflicts=%d", ExitCode(err), len(result.Conflicts))
	}
	if data.Shared["FIXTURE_KEY"] != "fixture-local" {
		t.Fatal("non-forced conflict mutated local data")
	}
	result, err = applyPullPlan(data, plan, state, true)
	if err != nil || result.Updated != 1 || data.Shared["FIXTURE_KEY"] != "fixture-remote" {
		t.Fatal("forced pull did not make remote authoritative")
	}
}

func TestApplyPushPlanDeletesIndividuallyAndDropsState(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	state := emptyBWSSyncState()
	state.MachineID = syncTestMachineID
	state.ProjectID = "fixture-project"
	var remote []bwsSecret
	for _, key := range []string{"FIRST_KEY", "SECOND_KEY"} {
		name := buildBWSSecretName(syncTestMachineID, ref, key)
		item := syncTestRemote(t, name, ref, key, "fixture-remote", "revision-one")
		item.ID = "fixture-id-" + key
		remote = append(remote, item)
		state.Entries[name] = syncTestStateEntry(name, ref, key, "fixture-remote", "revision-one")
	}
	plan, err := buildPushPlan(nil, remote, state, syncTestMachineID)
	if err != nil {
		t.Fatal(err)
	}
	deleteCalls := 0
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
			deleteCalls++
			if len(args) != 7 || args[0] != "secret" || args[1] != "delete" {
				t.Fatalf("unexpected delete argv shape")
			}
			return []byte("1 secret deleted successfully."), nil, nil
		},
	}
	result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 2 || deleteCalls != 2 || len(state.Entries) != 0 {
		t.Fatalf("deleted=%d calls=%d state=%d", result.Deleted, deleteCalls, len(state.Entries))
	}
}

func TestApplyPushPlanDeleteFailuresTrackExactAppliedPrefix(t *testing.T) {
	tests := []struct {
		name        string
		failAt      int
		wantPartial bool
		wantDeleted int
	}{
		{name: "first delete fails", failAt: 1, wantPartial: true, wantDeleted: 0},
		{name: "second delete fails", failAt: 2, wantPartial: true, wantDeleted: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := scopeRef{kind: scopeKindShared}
			state := emptyBWSSyncState()
			state.MachineID = syncTestMachineID
			state.ProjectID = "fixture-project"
			var remote []bwsSecret
			for _, key := range []string{"FIRST_KEY", "SECOND_KEY"} {
				name := buildBWSSecretName(syncTestMachineID, ref, key)
				item := syncTestRemote(t, name, ref, key, "fixture-remote", "revision-one")
				item.ID = "fixture-id-" + key
				remote = append(remote, item)
				state.Entries[name] = syncTestStateEntry(name, ref, key, "fixture-remote", "revision-one")
			}
			plan, err := buildPushPlan(nil, remote, state, syncTestMachineID)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			token := "fixture-delete-token"
			client := &bwsClient{
				binPath: "fixture-bws",
				token:   token,
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
					calls++
					if len(args) != 7 || args[0] != "secret" || args[1] != "delete" {
						t.Fatalf("unexpected delete argv: %v", args)
					}
					if calls == test.failAt {
						return nil, []byte("rate limit from provider for " + token), errors.New("exit status 4 for " + token)
					}
					return []byte("1 secret deleted successfully."), nil, nil
				},
			}
			result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
			if !errors.Is(err, errBWSCommandFailed) {
				t.Fatalf("error=%v", err)
			}
			if result.Partial != test.wantPartial || result.Deleted != test.wantDeleted || len(result.Actions) != test.wantDeleted {
				t.Fatalf("partial=%t deleted=%d actions=%d", result.Partial, result.Deleted, len(result.Actions))
			}
			if calls != test.failAt || len(state.Entries) != 2-test.wantDeleted {
				t.Fatalf("calls=%d state=%d", calls, len(state.Entries))
			}
			for index := 0; index < test.wantDeleted; index++ {
				if _, exists := state.Entries[plan.actions[index].name]; exists {
					t.Fatalf("state retained successfully deleted entry %q", plan.actions[index].name)
				}
			}
			for index := test.wantDeleted; index < len(plan.actions); index++ {
				if _, exists := state.Entries[plan.actions[index].name]; !exists {
					t.Fatalf("state dropped unapplied entry %q", plan.actions[index].name)
				}
			}
			if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[REDACTED]") {
				t.Fatal("delete provider failure did not preserve redaction")
			}
		})
	}
}

func TestApplyPushPlanInvalidMutationResponseHealsThroughAdopt(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			return []byte("{"), nil, nil
		},
	}

	t.Run("create", func(t *testing.T) {
		entry := syncEntry{ref: ref, key: "CREATE_KEY", value: "fixture-created"}
		name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
		state := emptyBWSSyncState()
		state.MachineID = syncTestMachineID
		state.ProjectID = "fixture-project"
		plan, err := buildPushPlan([]syncEntry{entry}, nil, state, syncTestMachineID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
		if err == nil || !result.Partial || len(state.Entries) != 0 {
			t.Fatalf("err=%v partial=%t entries=%d", err, result.Partial, len(state.Entries))
		}

		createdRemotely := syncTestRemote(t, name, ref, entry.key, entry.value, "revision-created")
		healingPlan, err := buildPushPlan([]syncEntry{entry}, []bwsSecret{createdRemotely}, state, syncTestMachineID)
		if err != nil {
			t.Fatal(err)
		}
		assertSinglePlanAction(t, healingPlan, syncActionAdopt, 0)
	})

	t.Run("edit", func(t *testing.T) {
		entry := syncEntry{ref: ref, key: "EDIT_KEY", value: "fixture-edited"}
		name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
		listedRemote := syncTestRemote(t, name, ref, entry.key, "fixture-before-edit", "revision-before-edit")
		state := emptyBWSSyncState()
		state.MachineID = syncTestMachineID
		state.ProjectID = "fixture-project"
		state.Entries[name] = syncTestStateEntry(name, ref, entry.key, listedRemote.Value, listedRemote.RevisionDate)
		plan, err := buildPushPlan([]syncEntry{entry}, []bwsSecret{listedRemote}, state, syncTestMachineID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := applyPushPlan(context.Background(), client, state.ProjectID, plan, state, false)
		if err == nil || !result.Partial || state.Entries[name].RevisionDate != listedRemote.RevisionDate {
			t.Fatalf("err=%v partial=%t state=%+v", err, result.Partial, state.Entries[name])
		}

		editedRemotely := syncTestRemote(t, name, ref, entry.key, entry.value, "revision-after-edit")
		healingPlan, err := buildPushPlan([]syncEntry{entry}, []bwsSecret{editedRemotely}, state, syncTestMachineID)
		if err != nil {
			t.Fatal(err)
		}
		assertSinglePlanAction(t, healingPlan, syncActionAdopt, 0)
	})
}
