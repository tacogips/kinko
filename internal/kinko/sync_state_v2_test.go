package kinko

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const stateV2TestMachineID = "0123456789abcdef"

func TestDecodeBWSSyncStateFormatCompatibility(t *testing.T) {
	legacy := `{"format":1,"machine_id":"0123456789abcdef","project_id":"project","entries":{},"future":{"spacing": [1, 2]}}`
	envelope, err := decodeBWSSyncState(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Format != 1 || !bytes.Equal(envelope.Raw["future"], []byte(`{"spacing": [1, 2]}`)) {
		t.Fatalf("envelope=%+v", envelope)
	}
	for _, encoded := range []string{``, `null`, `[]`, `{"format":3}`, `{"format":"2"}`, `{"entries":{}}`, `{"format":2}{}`} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := decodeBWSSyncState(encoded); err == nil {
				t.Fatalf("invalid envelope %q was accepted", encoded)
			}
		})
	}
}

func TestLegacyDecoderRejectsFormatTwo(t *testing.T) {
	encoded := `{"format":2,"entries":{},"ownership":{}}`
	if _, err := decodeBWSSyncState(encoded); err != nil {
		t.Fatal(err)
	}
	_, err := loadBWSSyncState(map[string]string{configKeyBWSSyncState: encoded})
	if ExitCode(err) != exitCodeMetadataInvalid || !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("legacy decoder err=%v exit=%d", err, ExitCode(err))
	}
}

func TestMergeSelectedBWSSyncStatePreservesRawUnselectedDataAndOwnership(t *testing.T) {
	selectedIdentity := syncIdentity{Provider: "provider", ProjectID: "project", MachineID: stateV2TestMachineID, Scope: scopeKindShared, Key: "SELECTED_KEY"}
	excludedIdentity := syncIdentity{Provider: "provider", ProjectID: "project", MachineID: stateV2TestMachineID, Scope: scopeKindShared, Key: "EXCLUDED_KEY"}
	selectedID := syncEntryID(selectedIdentity)
	excludedID := syncEntryID(excludedIdentity)
	excludedRaw := json.RawMessage(`{"schema":"future.schema","secret":"must-not-decode","unknown": [ 1,  2 ]}`)
	ownershipRaw := json.RawMessage(`{"secret_id":"owned","future": { "keep": true }}`)
	checkpointRaw := json.RawMessage(`{ "operation": "opaque", "future": [ 3, 4 ] }`)
	rootRaw := json.RawMessage(`{ "root": [ 5, 6 ] }`)
	encoded, err := marshalRawObject(map[string]json.RawMessage{
		"format":     json.RawMessage("2"),
		"entries":    mustRawObject(t, map[string]json.RawMessage{selectedID: json.RawMessage(`{"old":true}`), excludedID: excludedRaw}),
		"ownership":  mustRawObject(t, map[string]json.RawMessage{selectedID: ownershipRaw}),
		"checkpoint": checkpointRaw,
		"future":     rootRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	desired := &bwsSyncStateV2{
		Format: bwsSyncStateFormatV2,
		Entries: map[string]syncStateEntryV2{
			selectedID: validSyncStateEntryV2("SELECTED_KEY"),
		},
		Ownership: map[string]syncOwnershipRecord{},
	}
	merged, err := mergeSelectedBWSSyncState(envelope, desired, map[string]struct{}{selectedID: {}})
	if err != nil {
		t.Fatal(err)
	}
	mergedEnvelope, err := decodeBWSSyncState(merged)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := rawObject(mergedEnvelope.Raw["entries"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(entries[excludedID], excludedRaw) {
		t.Fatalf("excluded raw changed:\n got %s\nwant %s", entries[excludedID], excludedRaw)
	}
	ownership, err := rawObject(mergedEnvelope.Raw["ownership"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ownership[selectedID], ownershipRaw) {
		t.Fatalf("ownership proof changed:\n got %s\nwant %s", ownership[selectedID], ownershipRaw)
	}
	if !bytes.Equal(mergedEnvelope.Raw["checkpoint"], checkpointRaw) || !bytes.Equal(mergedEnvelope.Raw["future"], rootRaw) {
		t.Fatalf("opaque fields changed: checkpoint=%s future=%s", mergedEnvelope.Raw["checkpoint"], mergedEnvelope.Raw["future"])
	}
}

func TestMergeSelectedBWSSyncStateResetDoesNotEraseOwnership(t *testing.T) {
	identity := syncIdentity{Scope: scopeKindShared, Key: "RESET_KEY"}
	id := syncEntryID(identity)
	ownership := json.RawMessage(`{ "secret_id": "proof", "revision": "one" }`)
	encoded := mustRawObject(t, map[string]json.RawMessage{
		"format":    json.RawMessage("2"),
		"entries":   mustRawObject(t, map[string]json.RawMessage{id: json.RawMessage(`{"baseline":true}`)}),
		"ownership": mustRawObject(t, map[string]json.RawMessage{id: ownership}),
	})
	envelope, err := decodeBWSSyncState(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeSelectedBWSSyncState(envelope, &bwsSyncStateV2{Format: 2}, map[string]struct{}{id: {}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBWSSyncState(merged)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := rawObject(decoded.Raw["entries"])
	proof, _ := rawObject(decoded.Raw["ownership"])
	if _, exists := entries[id]; exists {
		t.Fatal("selected baseline was not reset")
	}
	if !bytes.Equal(proof[id], ownership) {
		t.Fatalf("ownership proof changed: %s", proof[id])
	}
}

func TestSyncStateEntryV2UnknownFieldRoundTrip(t *testing.T) {
	entry := validSyncStateEntryV2("ROUND_TRIP_KEY")
	entry.Raw = map[string]json.RawMessage{"future": json.RawMessage(`{ "spacing": [ 1, 2 ] }`)}
	encoded, err := encodeSyncStateEntryV2(entry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSyncStateEntryV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Raw["future"], entry.Raw["future"]) {
		t.Fatalf("future field changed: %s", decoded.Raw["future"])
	}
}

func validSyncStateEntryV2(key string) syncStateEntryV2 {
	return syncStateEntryV2{
		Schema:           syncStateEntrySchemaV2,
		ProviderIdentity: strings.Repeat("a", 64),
		Endpoint:         "https://example.invalid",
		ProjectID:        "project",
		MachineID:        stateV2TestMachineID,
		SecretID:         "secret-id",
		Name:             "remote-name",
		Revision:         "revision-one",
		Key:              key,
		ValueSHA256:      strings.Repeat("b", 64),
		Scope:            scopeKindShared,
	}
}

func mustRawObject(t *testing.T, fields map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	encoded, err := marshalRawObject(fields)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSyncStateV2RetainsOwnershipOnlyProviderContext(t *testing.T) {
	contextValue := syncPlanContext{ProviderIdentity: strings.Repeat("a", 64), Endpoint: "https://api.example.invalid", OrganizationID: "org", ProjectID: "project", MachineID: stateV2TestMachineID}
	state := &bwsSyncStateV2{Format: 2, Context: &contextValue, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}}
	base := syncStateEnvelope{Format: 2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	encoded, err := mergeSelectedBWSSyncState(base, state, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	got, err := inferMaintenanceContext(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSyncProviderContext(got, contextValue) {
		t.Fatalf("context=%+v want %+v", got, contextValue)
	}
}
