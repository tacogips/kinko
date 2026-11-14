package kinko

import (
	"reflect"
	"testing"
)

func TestNormalizeSyncSelectorDeterministicAndRejectsMalformedInput(t *testing.T) {
	first, firstDigest, err := normalizeSyncSelector(syncSelector{IncludeKeys: []string{"glob:API_*", "EXACT", "EXACT"}, IncludeProfiles: []string{"z", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := normalizeSyncSelector(syncSelector{IncludeProfiles: []string{"a", "z"}, IncludeKeys: []string{"EXACT", "glob:API_*"}, Shared: syncSharedInclude})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || firstDigest != secondDigest {
		t.Fatalf("normalization differs: %+v %q / %+v %q", first, firstDigest, second, secondDigest)
	}
	invalid := []syncSelector{
		{Shared: "invalid"},
		{IncludeKeys: []string{"glob:["}},
		{IncludeKeys: []string{"invalid-key"}},
		{IncludeProfiles: []string{"glob:a/b"}},
		{IncludePaths: []string{"work/project"}},
		{IncludePaths: []string{"logical:work/../project"}},
		{IncludePaths: []string{"local:relative"}},
	}
	for _, selector := range invalid {
		if _, _, err := normalizeSyncSelector(selector); err == nil {
			t.Fatalf("invalid selector %+v was accepted", selector)
		}
	}
}

func TestSelectSyncIdentitiesUnionIntersectionExclusionAndReservedKey(t *testing.T) {
	localPath := "local:/workspace/project"
	identities := []syncIdentity{
		{Profile: "default", Path: "logical:work/project", Key: "API_TOKEN", Scope: scopeKindPath},
		{Profile: "default", Path: "logical:work/project", Key: "API_PRIVATE", Scope: scopeKindPath},
		{Profile: "other", Path: "logical:work/project", Key: "API_TOKEN", Scope: scopeKindPath},
		{Profile: "default", Path: localPath, Key: "API_TOKEN", Scope: scopeKindPath},
		{Key: "API_TOKEN", Scope: scopeKindShared},
		{Key: sharedKeyBWSAccessToken, Scope: scopeKindShared},
	}
	// A duplicate models the same identity arriving from local, remote, and
	// state union members. Selection remains deterministic and value-free.
	identities = append(identities, identities[0], identities[0])
	selector := syncSelector{
		IncludeProfiles: []string{"default"},
		IncludePaths:    []string{"logical:work/project"},
		IncludeKeys:     []string{"glob:API_*"},
		ExcludeKeys:     []string{"API_PRIVATE"},
		Shared:          syncSharedExclude,
	}
	selected, err := selectSyncIdentities(selector, identities)
	if err != nil {
		t.Fatal(err)
	}
	wantID := syncEntryID(identities[0])
	if len(selected) != 1 {
		t.Fatalf("selected=%+v", selected)
	}
	if _, exists := selected[wantID]; !exists {
		t.Fatalf("wanted identity %q was not selected", wantID)
	}
}

func TestSelectSyncIdentitiesSharedModesAndCaseSensitiveGlob(t *testing.T) {
	shared := syncIdentity{Scope: scopeKindShared, Key: "API_KEY"}
	pathIdentity := syncIdentity{Profile: "default", Path: "logical:work/project", Scope: scopeKindPath, Key: "API_KEY"}
	identities := []syncIdentity{shared, pathIdentity}
	tests := []struct {
		name     string
		selector syncSelector
		want     []syncIdentity
	}{
		{name: "include", selector: syncSelector{Shared: syncSharedInclude}, want: identities},
		{name: "exclude", selector: syncSelector{Shared: syncSharedExclude}, want: []syncIdentity{pathIdentity}},
		{name: "only", selector: syncSelector{Shared: syncSharedOnly}, want: []syncIdentity{shared}},
		{name: "case sensitive miss", selector: syncSelector{IncludeKeys: []string{"glob:api_*"}}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, err := selectSyncIdentities(test.selector, identities)
			if err != nil {
				t.Fatal(err)
			}
			if len(selected) != len(test.want) {
				t.Fatalf("selected=%+v want %d", selected, len(test.want))
			}
			for _, identity := range test.want {
				if _, exists := selected[syncEntryID(identity)]; !exists {
					t.Fatalf("missing identity %+v", identity)
				}
			}
		})
	}
}

func TestSelectSyncIdentitiesValidatesBeforeReservedExclusion(t *testing.T) {
	malformedReserved := syncIdentity{Profile: "default", Scope: scopeKindShared, Key: sharedKeyBWSAccessToken}
	if _, err := selectSyncIdentities(syncSelector{}, []syncIdentity{malformedReserved}); err == nil {
		t.Fatal("malformed reserved identity was silently excluded")
	}
}
