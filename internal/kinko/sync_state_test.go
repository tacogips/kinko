package kinko

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBWSSyncStateAbsentAndMalformed(t *testing.T) {
	state, err := loadBWSSyncState(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Format != 1 || state.Entries == nil || len(state.Entries) != 0 {
		t.Fatalf("empty state=%+v", state)
	}

	_, err = loadBWSSyncState(map[string]string{configKeyBWSSyncState: "not-json"})
	if ExitCode(err) != exitCodeMetadataInvalid {
		t.Fatalf("malformed state exit=%d err=%v", ExitCode(err), err)
	}

	_, err = loadBWSSyncState(map[string]string{configKeyBWSSyncState: ""})
	if ExitCode(err) != exitCodeMetadataInvalid {
		t.Fatalf("present empty state exit=%d err=%v", ExitCode(err), err)
	}
}

func TestBWSSyncStateRealEncryptedConfigRoundTripAndCoexistence(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := unwrapDEKWithPassword(meta, "pw")
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config[folderConfigKey] = `{"format":1,"folders":[]}`
	config["user.setting"] = "user-value"

	entryName := buildBWSSecretName(meta.MachineID, scopeRef{kind: scopeKindShared}, "SHARED_KEY")
	want := &bwsSyncState{
		Format:    1,
		MachineID: meta.MachineID,
		ProjectID: "project-id",
		Entries: map[string]syncStateEntry{
			entryName: {
				SecretID:     "secret-id",
				Name:         entryName,
				Scope:        scopeKindShared,
				Key:          "SHARED_KEY",
				RevisionDate: "2026-07-13T00:00:00Z",
				ValueSHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		},
	}
	if err := saveBWSSyncState(config, want); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(dataDir, dek, config); err != nil {
		t.Fatal(err)
	}

	reloadedConfig, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedConfig[folderConfigKey] != config[folderConfigKey] || reloadedConfig["user.setting"] != "user-value" {
		t.Fatalf("coexisting config was changed: %+v", reloadedConfig)
	}
	got, err := loadBWSSyncState(reloadedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip state=%+v want %+v", got, want)
	}
}

func TestSaveBWSSyncStateInitializesFormatAndEntries(t *testing.T) {
	config := map[string]string{}
	state := &bwsSyncState{}
	if err := saveBWSSyncState(config, state); err != nil {
		t.Fatal(err)
	}
	if state.Format != 1 || state.Entries == nil || config[configKeyBWSSyncState] == "" {
		t.Fatalf("state=%+v config=%+v", state, config)
	}
}

func TestLoadBWSSyncStateRejectsSemanticallyInvalidEntries(t *testing.T) {
	ref := scopeRef{kind: scopeKindShared}
	name := buildBWSSecretName(syncTestMachineID, ref, "FIXTURE_KEY")
	valid := bwsSyncState{
		Format:    1,
		MachineID: syncTestMachineID,
		ProjectID: "fixture-project",
		Entries: map[string]syncStateEntry{
			name: syncTestStateEntry(name, ref, "FIXTURE_KEY", "fixture-value", "revision-one"),
		},
	}
	tests := []struct {
		name   string
		mutate func(*bwsSyncState)
	}{
		{name: "missing machine", mutate: func(state *bwsSyncState) { state.MachineID = "" }},
		{name: "missing project", mutate: func(state *bwsSyncState) { state.ProjectID = "" }},
		{name: "map name mismatch", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.Name = name + "_OTHER"
			state.Entries[name] = entry
		}},
		{name: "missing secret id", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.SecretID = ""
			state.Entries[name] = entry
		}},
		{name: "missing revision", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.RevisionDate = ""
			state.Entries[name] = entry
		}},
		{name: "invalid value hash", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.ValueSHA256 = "invalid"
			state.Entries[name] = entry
		}},
		{name: "invalid key", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.Key = "invalid-key"
			state.Entries[name] = entry
		}},
		{name: "internally consistent reserved key", mutate: func(state *bwsSyncState) {
			delete(state.Entries, name)
			reservedName := buildBWSSecretName(syncTestMachineID, ref, sharedKeyBWSAccessToken)
			state.Entries[reservedName] = syncTestStateEntry(reservedName, ref, sharedKeyBWSAccessToken, "fixture-value", "revision-one")
		}},
		{name: "invalid shared fields", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.Profile = "fixture-profile"
			state.Entries[name] = entry
		}},
		{name: "name inconsistent with scope", mutate: func(state *bwsSyncState) {
			entry := state.Entries[name]
			entry.Scope = scopeKindPath
			entry.Profile = "fixture-profile"
			entry.Path = "/fixture/path"
			state.Entries[name] = entry
		}},
		{name: "duplicate secret id", mutate: func(state *bwsSyncState) {
			otherName := buildBWSSecretName(syncTestMachineID, ref, "OTHER_KEY")
			other := syncTestStateEntry(otherName, ref, "OTHER_KEY", "fixture-other", "revision-two")
			other.SecretID = state.Entries[name].SecretID
			state.Entries[otherName] = other
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := valid
			state.Entries = map[string]syncStateEntry{name: valid.Entries[name]}
			test.mutate(&state)
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			_, err = loadBWSSyncState(map[string]string{configKeyBWSSyncState: string(encoded)})
			if ExitCode(err) != exitCodeMetadataInvalid {
				t.Fatalf("exit=%d", ExitCode(err))
			}
		})
	}
}

func TestLoadBWSSyncStateRejectsScopeHashCollision(t *testing.T) {
	firstRef, secondRef := findSyncScopeCollision(t)
	firstName := buildBWSSecretName(syncTestMachineID, firstRef, "FIRST_KEY")
	secondName := buildBWSSecretName(syncTestMachineID, secondRef, "SECOND_KEY")
	state := bwsSyncState{
		Format:    1,
		MachineID: syncTestMachineID,
		ProjectID: "fixture-project",
		Entries: map[string]syncStateEntry{
			firstName:  syncTestStateEntry(firstName, firstRef, "FIRST_KEY", "fixture-first", "revision-one"),
			secondName: syncTestStateEntry(secondName, secondRef, "SECOND_KEY", "fixture-second", "revision-two"),
		},
	}
	first := state.Entries[firstName]
	first.SecretID = "collision-secret-one"
	state.Entries[firstName] = first
	second := state.Entries[secondName]
	second.SecretID = "collision-secret-two"
	state.Entries[secondName] = second
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadBWSSyncState(map[string]string{configKeyBWSSyncState: string(encoded)})
	if ExitCode(err) != exitCodeMetadataInvalid {
		t.Fatalf("exit=%d err=%v", ExitCode(err), err)
	}
}
