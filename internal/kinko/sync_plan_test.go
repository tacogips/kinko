package kinko

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const syncTestMachineID = "0123456789abcdef"

func TestBuildPushPlanDecisionTable(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	entry := syncEntry{ref: ref, key: "FIXTURE_KEY", value: "fixture-local"}
	name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
	baseRemote := syncTestRemote(t, name, ref, entry.key, "fixture-remote", "revision-current")
	baseState := syncTestStateEntry(name, ref, entry.key, "fixture-baseline", "revision-baseline")

	tests := []struct {
		name        string
		local       bool
		remote      bool
		state       bool
		localValue  string
		remoteValue string
		remoteRev   string
		want        syncActionKind
		wantForced  syncActionKind
	}{
		{name: "present absent absent creates", local: true, localValue: "fixture-local", want: syncActionCreate},
		{name: "present absent present conflicts", local: true, state: true, localValue: "fixture-local", want: syncActionConflict, wantForced: syncActionCreate},
		{name: "equal values adopt", local: true, remote: true, localValue: "fixture-equal", remoteValue: "fixture-equal", remoteRev: "revision-current", want: syncActionAdopt},
		{name: "equal concurrent changes adopt", local: true, remote: true, state: true, localValue: "fixture-equal", remoteValue: "fixture-equal", remoteRev: "revision-current", want: syncActionAdopt},
		{name: "equal unchanged baseline is unchanged", local: true, remote: true, state: true, localValue: "fixture-baseline", remoteValue: "fixture-baseline", remoteRev: "revision-baseline", want: syncActionUnchanged},
		{name: "different absent state conflicts", local: true, remote: true, localValue: "fixture-local", remoteValue: "fixture-remote", remoteRev: "revision-current", want: syncActionConflict, wantForced: syncActionUpdate},
		{name: "different remote unchanged updates", local: true, remote: true, state: true, localValue: "fixture-local", remoteValue: "fixture-remote", remoteRev: "revision-baseline", want: syncActionUpdate},
		{name: "different remote changed conflicts", local: true, remote: true, state: true, localValue: "fixture-local", remoteValue: "fixture-remote", remoteRev: "revision-current", want: syncActionConflict, wantForced: syncActionUpdate},
		{name: "local absent remote unchanged deletes", remote: true, state: true, remoteValue: "fixture-remote", remoteRev: "revision-baseline", want: syncActionDelete},
		{name: "local absent remote changed conflicts", remote: true, state: true, remoteValue: "fixture-remote", remoteRev: "revision-current", want: syncActionConflict, wantForced: syncActionDelete},
		{name: "remote only ignored", remote: true, remoteValue: "fixture-remote", remoteRev: "revision-current", want: syncActionIgnore},
		{name: "both absent stale state dropped", state: true, want: syncActionUnchanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var entries []syncEntry
			if test.local {
				local := entry
				local.value = test.localValue
				entries = []syncEntry{local}
			}
			var remote []bwsSecret
			if test.remote {
				item := baseRemote
				item.Value = test.remoteValue
				item.RevisionDate = test.remoteRev
				remote = []bwsSecret{item}
			}
			state := emptyBWSSyncState()
			if test.state {
				state.Entries[name] = baseState
			}
			plan, err := buildPushPlan(entries, remote, state, syncTestMachineID)
			if err != nil {
				t.Fatal(err)
			}
			assertSinglePlanAction(t, plan, test.want, test.wantForced)
		})
	}
}

func TestBuildPullPlanDecisionTable(t *testing.T) {
	ref := scopeRef{profile: "fixture-profile", kind: scopeKindPath, path: t.TempDir()}
	entry := syncEntry{ref: ref, key: "FIXTURE_KEY", value: "fixture-local"}
	name := buildBWSSecretName(syncTestMachineID, ref, entry.key)
	baseRemote := syncTestRemote(t, name, ref, entry.key, "fixture-remote", "revision-current")
	baseState := syncTestStateEntry(name, ref, entry.key, "fixture-baseline", "revision-baseline")

	tests := []struct {
		name        string
		local       bool
		remote      bool
		state       bool
		localValue  string
		remoteValue string
		remoteRev   string
		stateValue  string
		want        syncActionKind
		wantForced  syncActionKind
	}{
		{name: "present remote absent local absent state creates", remote: true, remoteValue: "fixture-remote", want: syncActionCreate},
		{name: "present remote absent local present state conflicts", remote: true, state: true, remoteValue: "fixture-remote", stateValue: "fixture-baseline", want: syncActionConflict, wantForced: syncActionCreate},
		{name: "equal values adopt", local: true, remote: true, localValue: "fixture-equal", remoteValue: "fixture-equal", want: syncActionAdopt},
		{name: "equal concurrent changes adopt", local: true, remote: true, state: true, localValue: "fixture-equal", remoteValue: "fixture-equal", stateValue: "fixture-baseline", want: syncActionAdopt},
		{name: "equal unchanged baseline is unchanged", local: true, remote: true, state: true, localValue: "fixture-baseline", remoteValue: "fixture-baseline", remoteRev: "revision-baseline", stateValue: "fixture-baseline", want: syncActionUnchanged},
		{name: "different absent state conflicts", local: true, remote: true, localValue: "fixture-local", remoteValue: "fixture-remote", want: syncActionConflict, wantForced: syncActionUpdate},
		{name: "different local unchanged updates", local: true, remote: true, state: true, localValue: "fixture-baseline", remoteValue: "fixture-remote", stateValue: "fixture-baseline", want: syncActionUpdate},
		{name: "different local changed conflicts", local: true, remote: true, state: true, localValue: "fixture-local", remoteValue: "fixture-remote", stateValue: "fixture-baseline", want: syncActionConflict, wantForced: syncActionUpdate},
		{name: "remote absent local unchanged deletes", local: true, state: true, localValue: "fixture-baseline", stateValue: "fixture-baseline", want: syncActionDelete},
		{name: "remote absent local changed conflicts", local: true, state: true, localValue: "fixture-local", stateValue: "fixture-baseline", want: syncActionConflict, wantForced: syncActionDelete},
		{name: "local only ignored", local: true, localValue: "fixture-local", want: syncActionIgnore},
		{name: "both absent stale state dropped", state: true, stateValue: "fixture-baseline", want: syncActionUnchanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var entries []syncEntry
			if test.local {
				local := entry
				local.value = test.localValue
				entries = []syncEntry{local}
			}
			var remote []bwsSecret
			if test.remote {
				item := baseRemote
				item.Value = test.remoteValue
				if test.remoteRev != "" {
					item.RevisionDate = test.remoteRev
				}
				remote = []bwsSecret{item}
			}
			state := emptyBWSSyncState()
			if test.state {
				value := test.stateValue
				if value == "" {
					value = "fixture-baseline"
				}
				stored := baseState
				stored.ValueSHA256 = valueSHA256(value)
				state.Entries[name] = stored
			}
			plan, err := buildPullPlan(entries, remote, state, syncTestMachineID)
			if err != nil {
				t.Fatal(err)
			}
			assertSinglePlanAction(t, plan, test.want, test.wantForced)
		})
	}
}

func TestBuildPlansRemoteValidationAndReservedKey(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	name := buildBWSSecretName(syncTestMachineID, ref, "FIXTURE_KEY")
	valid := syncTestRemote(t, name, ref, "FIXTURE_KEY", "fixture-value", "revision-one")

	t.Run("duplicate exact name", func(t *testing.T) {
		_, err := buildPushPlan(nil, []bwsSecret{valid, valid}, emptyBWSSyncState(), syncTestMachineID)
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("malformed note", func(t *testing.T) {
		invalid := valid
		invalid.Note = "not-json"
		_, err := buildPullPlan(nil, []bwsSecret{invalid}, emptyBWSSyncState(), syncTestMachineID)
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("duplicate remote id", func(t *testing.T) {
		otherName := buildBWSSecretName(syncTestMachineID, ref, "OTHER_KEY")
		other := syncTestRemote(t, otherName, ref, "OTHER_KEY", "fixture-other", "revision-two")
		other.ID = valid.ID
		_, err := buildPullPlan(nil, []bwsSecret{valid, other}, emptyBWSSyncState(), syncTestMachineID)
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("duplicate id across machines", func(t *testing.T) {
		otherMachine := valid
		otherMachine.Key = "fedcba9876543210_deadbeef_OTHER_KEY"
		_, err := buildPullPlan(nil, []bwsSecret{valid, otherMachine}, emptyBWSSyncState(), syncTestMachineID)
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("duplicate id with reserved key", func(t *testing.T) {
		reservedName := buildBWSSecretName(syncTestMachineID, ref, sharedKeyBWSAccessToken)
		reserved := syncTestRemote(t, reservedName, ref, sharedKeyBWSAccessToken, "fixture-token", "revision-two")
		reserved.ID = valid.ID
		_, err := buildPullPlan(nil, []bwsSecret{valid, reserved}, emptyBWSSyncState(), syncTestMachineID)
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("duplicate reserved name", func(t *testing.T) {
		reservedName := buildBWSSecretName(syncTestMachineID, ref, sharedKeyBWSAccessToken)
		first := syncTestRemote(t, reservedName, ref, sharedKeyBWSAccessToken, "fixture-token-one", "revision-one")
		second := syncTestRemote(t, reservedName, ref, sharedKeyBWSAccessToken, "fixture-token-two", "revision-two")
		second.ID = "fixture-id-reserved-second"
		for _, build := range []struct {
			name string
			fn   func([]syncEntry, []bwsSecret, *bwsSyncState, string) (*syncPlan, error)
		}{
			{name: "push", fn: buildPushPlan},
			{name: "pull", fn: buildPullPlan},
		} {
			t.Run(build.name, func(t *testing.T) {
				_, err := build.fn(nil, []bwsSecret{first, second}, emptyBWSSyncState(), syncTestMachineID)
				if ExitCode(err) != exitCodePolicyFailed {
					t.Fatalf("exit=%d err=%v", ExitCode(err), err)
				}
			})
		}
	})
	t.Run("other machine ignored", func(t *testing.T) {
		other := valid
		other.Key = "fedcba9876543210_deadbeef_FIXTURE_KEY"
		plan, err := buildPullPlan(nil, []bwsSecret{other}, emptyBWSSyncState(), syncTestMachineID)
		if err != nil || len(plan.actions) != 0 {
			t.Fatalf("err=%v actions=%d", err, len(plan.actions))
		}
	})
	t.Run("reserved remote key ignored", func(t *testing.T) {
		reservedName := buildBWSSecretName(syncTestMachineID, ref, sharedKeyBWSAccessToken)
		reserved := syncTestRemote(t, reservedName, ref, sharedKeyBWSAccessToken, "fixture-token", "revision-one")
		plan, err := buildPullPlan(nil, []bwsSecret{reserved}, emptyBWSSyncState(), syncTestMachineID)
		if err != nil || len(plan.actions) != 0 {
			t.Fatalf("err=%v actions=%d", err, len(plan.actions))
		}
	})
}

func TestBuildPlansRejectDuplicateNormalizedLocalNames(t *testing.T) {
	path := t.TempDir()
	entries := []syncEntry{
		{ref: scopeRef{profile: "fixture-profile", kind: scopeKindPath, path: path}, key: "FIXTURE_KEY", value: "fixture-one"},
		{ref: scopeRef{profile: "fixture-profile", kind: scopeKindPath, path: path + string(filepath.Separator)}, key: "FIXTURE_KEY", value: "fixture-two"},
	}
	for _, build := range []struct {
		name string
		fn   func([]syncEntry, []bwsSecret, *bwsSyncState, string) (*syncPlan, error)
	}{
		{name: "push", fn: buildPushPlan},
		{name: "pull", fn: buildPullPlan},
	} {
		t.Run(build.name, func(t *testing.T) {
			_, err := build.fn(entries, nil, emptyBWSSyncState(), syncTestMachineID)
			if ExitCode(err) != exitCodePolicyFailed {
				t.Fatalf("exit=%d err=%v", ExitCode(err), err)
			}
		})
	}
}

func TestBuildPlansRejectRemoteScopeHashCollision(t *testing.T) {
	firstRef, secondRef := findSyncScopeCollision(t)
	firstName := buildBWSSecretName(syncTestMachineID, firstRef, "FIRST_KEY")
	secondName := buildBWSSecretName(syncTestMachineID, secondRef, "SECOND_KEY")
	remote := []bwsSecret{
		syncTestRemote(t, firstName, firstRef, "FIRST_KEY", "fixture-first", "revision-one"),
		syncTestRemote(t, secondName, secondRef, "SECOND_KEY", "fixture-second", "revision-two"),
	}
	remote[0].ID = "collision-secret-one"
	remote[1].ID = "collision-secret-two"
	for _, build := range []struct {
		name string
		fn   func([]syncEntry, []bwsSecret, *bwsSyncState, string) (*syncPlan, error)
	}{
		{name: "push", fn: buildPushPlan},
		{name: "pull", fn: buildPullPlan},
	} {
		t.Run(build.name, func(t *testing.T) {
			_, err := build.fn(nil, remote, emptyBWSSyncState(), syncTestMachineID)
			if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), "Scope hash collision") {
				t.Fatalf("exit=%d err=%v", ExitCode(err), err)
			}
		})
	}
}

func findSyncScopeCollision(t *testing.T) (scopeRef, scopeRef) {
	t.Helper()
	path := t.TempDir()
	byHash := make(map[string]scopeRef)
	for index := 0; index < 1_000_000; index++ {
		ref := scopeRef{profile: fmt.Sprintf("collision-%d", index), kind: scopeKindPath, path: path}
		hash := deriveScopeHash(ref)
		if previous, exists := byHash[hash]; exists {
			return previous, ref
		}
		byHash[hash] = ref
	}
	t.Fatal("could not find a 32-bit scope-hash collision")
	return scopeRef{}, scopeRef{}
}

func TestCollectSyncEntriesExcludesReservedKeyAndSorts(t *testing.T) {
	path := t.TempDir()
	data := &vaultData{
		Shared: map[string]string{sharedKeyBWSAccessToken: "fixture-token", "Z_KEY": "fixture-z"},
		Profiles: map[string]map[string]map[string]string{
			"fixture-profile": {path: {"A_KEY": "fixture-a", sharedKeyBWSAccessToken: "fixture-path-token"}},
		},
	}
	entries, err := collectSyncEntries(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].key != "A_KEY" || entries[1].key != "Z_KEY" {
		t.Fatalf("unexpected collected keys")
	}
}

func syncTestRemote(t *testing.T, name string, ref scopeRef, key, value, revision string) bwsSecret {
	t.Helper()
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: syncTestMachineID, Profile: ref.profile, Scope: ref.kind, Path: ref.path, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	return bwsSecret{ID: "fixture-secret-id", ProjectID: "fixture-project", Key: name, Value: value, Note: note, RevisionDate: revision}
}

func syncTestStateEntry(name string, ref scopeRef, key, value, revision string) syncStateEntry {
	return syncStateEntry{SecretID: "fixture-secret-id", Name: name, Profile: ref.profile, Scope: ref.kind, Path: ref.path, Key: key, RevisionDate: revision, ValueSHA256: valueSHA256(value)}
}

func assertSinglePlanAction(t *testing.T, plan *syncPlan, want, wantForced syncActionKind) {
	t.Helper()
	if len(plan.actions) != 1 {
		t.Fatalf("actions=%d want 1", len(plan.actions))
	}
	if plan.actions[0].kind != want || plan.actions[0].forced != wantForced {
		t.Fatalf("kind=%d forced=%d want kind=%d forced=%d", plan.actions[0].kind, plan.actions[0].forced, want, wantForced)
	}
}
