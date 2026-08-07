package kinko

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeriveScopeHashV2DistinctDomainAndStable(t *testing.T) {
	logical := logicalScopeRef{Profile: "default", Kind: scopeKindPath, LogicalPath: "work/project-a"}
	first := deriveScopeHashV2(logical)
	if len(first) != 8 || first != deriveScopeHashV2(logical) {
		t.Fatalf("unstable hash %q", first)
	}
	legacy := deriveScopeHash(scopeRef{profile: "default", kind: scopeKindPath, path: "work/project-a"})
	if first == legacy {
		t.Fatalf("format-2 hash reused format-1 domain: %q", first)
	}
}

func TestBWSFormatTwoNoteContainsLogicalPathOnly(t *testing.T) {
	metadata := bwsNoteMetadataV2{
		KinkoSyncFormat: 2,
		MachineID:       stateV2TestMachineID,
		Profile:         "default",
		Scope:           scopeKindPath,
		LogicalPath:     "work/project-a",
		Key:             "API_KEY",
	}
	note, err := encodeBWSNoteV2(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(note), &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["path"]; exists || strings.Contains(note, "/Users/") || strings.Contains(note, `C:\`) {
		t.Fatalf("note leaked a local path: %s", note)
	}
	parsed, err := parseBWSNoteV2(note)
	if err != nil {
		t.Fatal(err)
	}
	name := buildBWSSecretNameV2(stateV2TestMachineID, logicalScopeRef{Profile: parsed.Profile, Kind: parsed.Scope, LogicalPath: parsed.LogicalPath}, parsed.Key)
	if err := verifyNoteMatchesNameV2(stateV2TestMachineID, name, parsed); err != nil {
		t.Fatal(err)
	}
	if _, err := parseBWSNoteV2(note[:len(note)-1] + `,"unknown":true}`); err == nil {
		t.Fatal("unknown note field was accepted")
	}
}

func TestBWSFormatTwoSharedNoteOmitsPortablePathFields(t *testing.T) {
	note, err := encodeBWSNoteV2(bwsNoteMetadataV2{KinkoSyncFormat: 2, MachineID: stateV2TestMachineID, Scope: scopeKindShared, Key: "SHARED_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "logical_path") || strings.Contains(note, "profile") {
		t.Fatalf("shared note has path fields: %s", note)
	}
}
