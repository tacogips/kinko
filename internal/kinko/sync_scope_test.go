package kinko

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveScopeHashFixedVectorsAndSeparation(t *testing.T) {
	tests := []struct {
		name string
		ref  scopeRef
		want string
	}{
		{name: "path", ref: scopeRef{profile: "default", kind: scopeKindPath, path: "/work/project-a"}, want: "956100d4"},
		{name: "shared", ref: scopeRef{kind: scopeKindShared}, want: "402883a7"},
		{name: "other profile", ref: scopeRef{profile: "other", kind: scopeKindPath, path: "/work/project-a"}, want: "f75da4a9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := deriveScopeHash(test.ref); got != test.want {
				t.Fatalf("deriveScopeHash(%+v)=%q want %q", test.ref, got, test.want)
			}
		})
	}
}

func TestDeriveScopeHashNormalizesTrailingSlash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project")
	plain := scopeRef{profile: "default", kind: scopeKindPath, path: path}
	trailing := scopeRef{profile: "default", kind: scopeKindPath, path: path + string(filepath.Separator)}
	if deriveScopeHash(plain) != deriveScopeHash(trailing) {
		t.Fatalf("trailing slash changed scope hash: %q != %q", deriveScopeHash(plain), deriveScopeHash(trailing))
	}
}

func TestBWSSecretNameRoundTrip(t *testing.T) {
	machineID := "0123456789abcdef"
	ref := scopeRef{profile: "default", kind: scopeKindPath, path: "/work/project-a"}
	name := buildBWSSecretName(machineID, ref, "KEY_WITH_UNDERSCORES")
	hash, key, ok := parseBWSSecretName(machineID, name)
	if !ok || hash != deriveScopeHash(ref) || key != "KEY_WITH_UNDERSCORES" {
		t.Fatalf("parse name=(%q,%q,%v) name=%q", hash, key, ok, name)
	}
	if _, _, ok := parseBWSSecretName("fedcba9876543210", name); ok {
		t.Fatal("name for another machine was accepted")
	}
	malformed := machineID + "_ABCDEF12_KEY"
	if _, _, ok := parseBWSSecretName(machineID, malformed); ok {
		t.Fatal("uppercase scope hash was accepted")
	}
}

func TestDetectScopeHashCollisions(t *testing.T) {
	refs := []scopeRef{
		{profile: "default", kind: scopeKindPath, path: "/work/a"},
		{profile: "other", kind: scopeKindPath, path: "/work/b"},
	}
	if err := detectScopeHashCollisions(refs); err != nil {
		t.Fatalf("unexpected collision: %v", err)
	}
	err := detectScopeHashCollisionsWithHasher(refs, func(scopeRef) string { return "00000000" })
	if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), "Scope hash collision") {
		t.Fatalf("collision err=%v exit=%d", err, ExitCode(err))
	}
}

func TestBWSNoteEncodeParseAndVerify(t *testing.T) {
	machineID := "0123456789abcdef"
	path := filepath.Join(t.TempDir(), "project")
	metadata := bwsNoteMetadata{
		KinkoSyncFormat: 1,
		MachineID:       machineID,
		Profile:         "default",
		Scope:           scopeKindPath,
		Path:            path + string(filepath.Separator),
		Key:             "API_KEY",
	}
	note, err := encodeBWSNote(metadata)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseBWSNote(note)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != filepath.Clean(path) {
		t.Fatalf("parsed path=%q", parsed.Path)
	}
	name := buildBWSSecretName(machineID, scopeRef{profile: parsed.Profile, kind: parsed.Scope, path: parsed.Path}, parsed.Key)
	if err := verifyNoteMatchesName(machineID, name, parsed); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*bwsNoteMetadata)
	}{
		{name: "machine", mutate: func(value *bwsNoteMetadata) { value.MachineID = "fedcba9876543210" }},
		{name: "profile", mutate: func(value *bwsNoteMetadata) { value.Profile = "other" }},
		{name: "key", mutate: func(value *bwsNoteMetadata) { value.Key = "OTHER_KEY" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mismatch := parsed
			test.mutate(&mismatch)
			if err := verifyNoteMatchesName(machineID, name, mismatch); err == nil {
				t.Fatal("expected mismatch error")
			}
		})
	}
}

func TestBWSSharedNoteOmitsPathAndProfile(t *testing.T) {
	metadata := bwsNoteMetadata{
		KinkoSyncFormat: 1,
		MachineID:       "0123456789abcdef",
		Scope:           scopeKindShared,
		Key:             "SHARED_KEY",
	}
	note, err := encodeBWSNote(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(note), &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["profile"]; exists {
		t.Fatal("shared note contains profile")
	}
	if _, exists := raw["path"]; exists {
		t.Fatal("shared note contains path")
	}
	if _, err := parseBWSNote(note + " {}"); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

func TestParseBWSNoteRejectsMissingNote(t *testing.T) {
	for _, note := range []string{"", "   \n\t"} {
		if _, err := parseBWSNote(note); err == nil {
			t.Fatalf("missing note %q was accepted", note)
		}
	}
}
