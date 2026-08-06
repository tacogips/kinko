package kinko

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseAndValidateSyncPathMaps(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	parsed, err := parseSyncPathMap("work=" + root)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != (syncPathMap{Anchor: "work", Root: root}) {
		t.Fatalf("parsed=%+v", parsed)
	}
	tests := []string{
		"missing-separator",
		"Bad=" + root,
		"a_b=" + root,
		"work=relative",
		"work=" + root + string(filepath.Separator),
		`work=C:\vault`,
		`work=C:/vault`,
		`work=\\server\share`,
	}
	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			if _, err := parseSyncPathMap(value); err == nil {
				t.Fatalf("invalid map %q was accepted", value)
			}
		})
	}
}

func TestValidateSyncPathMapsAliasesAndLongestPrefix(t *testing.T) {
	base := filepath.Clean(t.TempDir())
	maps := []syncPathMap{
		{Anchor: "work", Root: base},
		{Anchor: "project", Root: filepath.Join(base, "project")},
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		t.Fatal(err)
	}
	logical, err := mapLocalToLogical(filepath.Join(base, "project", "child"), maps)
	if err != nil {
		t.Fatal(err)
	}
	if logical != "project/child" {
		t.Fatalf("longest-prefix logical=%q", logical)
	}
	if err := validateSyncPathMaps([]syncPathMap{{Anchor: "one", Root: base}, {Anchor: "two", Root: base}}, false); err == nil {
		t.Fatal("equal roots were accepted")
	}
	caseAlias := []syncPathMap{{Anchor: "work", Root: base}, {Anchor: "WORK", Root: strings.ToUpper(base)}}
	if err := validateSyncPathMaps(caseAlias, true); err == nil {
		t.Fatal("case-fold aliases were accepted")
	}
}

func TestSyncPathMapRoundTripContainmentAndNoFilesystemCreation(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	maps := []syncPathMap{{Anchor: "work", Root: root}}
	logical := "work/project/nested"
	wantLocal := filepath.Join(root, "project", "nested")
	local, err := mapLogicalToLocal(logical, maps)
	if err != nil {
		t.Fatal(err)
	}
	if local != wantLocal {
		t.Fatalf("local=%q want %q", local, wantLocal)
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatalf("mapping unexpectedly created a filesystem path: %v", err)
	}
	roundTrip, err := mapLocalToLogical(local, maps)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip != logical {
		t.Fatalf("logical=%q want %q", roundTrip, logical)
	}
	for _, invalid := range []string{"", "/work", "work/", "work//x", "work/./x", "work/../x", `work\x`, "work/C:/x", "work/\x00"} {
		if _, err := mapLogicalToLocal(invalid, maps); err == nil {
			t.Fatalf("invalid logical path %q was accepted", invalid)
		}
	}
	outside := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-outside")
	if _, err := mapLocalToLogical(outside, maps); err == nil {
		t.Fatal("outside path was mapped")
	}
}

func TestEncryptedSyncPathMapsAndInvocationOverride(t *testing.T) {
	firstRoot := filepath.Clean(t.TempDir())
	secondRoot := filepath.Clean(t.TempDir())
	config := map[string]string{"unrelated": "keep"}
	stored := []syncPathMap{{Anchor: "stored", Root: firstRoot}}
	if err := saveEncryptedSyncPathMaps(config, stored); err != nil {
		t.Fatal(err)
	}
	if config[configKeyBWSSyncPaths] == "" || config["unrelated"] != "keep" {
		t.Fatalf("config=%+v", config)
	}
	loaded, err := resolveSyncPathMaps(config, nil)
	if err != nil || !reflect.DeepEqual(loaded, stored) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	override := []syncPathMap{{Anchor: "override", Root: secondRoot}}
	resolved, err := resolveSyncPathMaps(config, override)
	if err != nil || !reflect.DeepEqual(resolved, override) {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}
