package kinko

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPathPruneMissingPreviewRequiresPasswordBeforeOutput(t *testing.T) {
	opts := setupUnlockedForSet(t)
	missingPath := filepath.Join(t.TempDir(), "missing")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "A=one")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runPathPruneMissing(opts, nil, strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", errBuf.String())
	}
	if !hasPathScopeForPruneTest(t, opts, opts.profile, missingPath) {
		t.Fatal("auth failure must not mutate stale path scope")
	}
}

func TestRunPathPruneMissingPreviewReportsOnlyMissingPathScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	keptPath := filepath.Join(t.TempDir(), "kept")
	if err := os.MkdirAll(keptPath, 0o755); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")
	filePath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	setPathScopeForPruneTest(t, opts, opts.profile, keptPath, "KEEP=one")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "STALE=two", "STALE2=three")
	setPathScopeForPruneTest(t, opts, opts.profile, filePath, "FILE=four")
	setSharedForPruneTest(t, opts, "SHARED=shared")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, nil, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "path prune-missing preview") {
		t.Fatalf("missing preview header: %q", got)
	}
	if !strings.Contains(got, "candidate profile=default path="+missingPath+" keys=2") {
		t.Fatalf("missing stale path candidate: %q", got)
	}
	if strings.Contains(got, keptPath) {
		t.Fatalf("existing directory must not be reported as candidate: %q", got)
	}
	if !strings.Contains(got, "skipped profile=default path="+filePath+" reason=path exists but is not a directory") {
		t.Fatalf("existing file should be skipped: %q", got)
	}
	if strings.Contains(got, "SHARED") {
		t.Fatalf("shared key names must not be printed: %q", got)
	}
	if !strings.Contains(got, "total scopes=1 keys=2") {
		t.Fatalf("unexpected totals: %q", got)
	}
	if !hasPathScopeForPruneTest(t, opts, opts.profile, missingPath) {
		t.Fatal("preview must not delete stale path scope")
	}
}

func TestRunPathPruneMissingYesDeletesOnlyStalePathScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	keptPath := filepath.Join(t.TempDir(), "kept")
	if err := os.MkdirAll(keptPath, 0o755); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")

	setPathScopeForPruneTest(t, opts, opts.profile, keptPath, "KEEP=one")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "STALE=two")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, []string{"--yes"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "path prune-missing pruned") {
		t.Fatalf("missing prune header: %q", out.String())
	}
	if hasPathScopeForPruneTest(t, opts, opts.profile, missingPath) {
		t.Fatal("stale path scope should be deleted")
	}
	if !hasPathScopeForPruneTest(t, opts, opts.profile, keptPath) {
		t.Fatal("existing directory scope should be preserved")
	}
	if _, ok := loadVaultForPruneTest(t, opts).Profiles[opts.profile]; !ok {
		t.Fatal("profile map should be preserved")
	}
}

func TestRunPathPruneMissingDoesNotDeleteSharedScope(t *testing.T) {
	opts := setupUnlockedForSet(t)
	missingPath := filepath.Join(t.TempDir(), "missing")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "STALE=two")
	setSharedForPruneTest(t, opts, "SHARED_ONLY=shared")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, []string{"--yes"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	vd := loadVaultForPruneTest(t, opts)
	if got := vd.Shared["SHARED_ONLY"]; got != "shared" {
		t.Fatalf("shared scope value=%q want shared", got)
	}
}

func TestRunPathPruneMissingAllProfiles(t *testing.T) {
	opts := setupUnlockedForSet(t)
	defaultMissing := filepath.Join(t.TempDir(), "default-missing")
	devMissing := filepath.Join(t.TempDir(), "dev-missing")
	setPathScopeForPruneTest(t, opts, defaultProfile, defaultMissing, "A=one")
	setPathScopeForPruneTest(t, opts, "dev", devMissing, "B=two")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, nil, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), defaultMissing) {
		t.Fatalf("selected profile candidate missing: %q", out.String())
	}
	if strings.Contains(out.String(), devMissing) {
		t.Fatalf("non-selected profile should not be scanned without --all-profiles: %q", out.String())
	}

	out.Reset()
	errBuf.Reset()
	opts.profile = "selected"
	if err := runPathPruneMissing(opts, []string{"--all-profiles"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), defaultMissing) || !strings.Contains(out.String(), devMissing) {
		t.Fatalf("--all-profiles should scan every stored profile: %q", out.String())
	}
}

func TestRunPathPruneMissingSkipsAmbiguousPaths(t *testing.T) {
	opts := setupUnlockedForSet(t)
	collisionBase := filepath.Join(t.TempDir(), "collision")
	relativePath := "relative/path"

	mutateVaultForPruneTest(t, opts, func(vd *vaultData) {
		if vd.Profiles[opts.profile] == nil {
			vd.Profiles[opts.profile] = map[string]map[string]string{}
		}
		vd.Profiles[opts.profile][collisionBase] = map[string]string{"A": "one"}
		vd.Profiles[opts.profile][collisionBase+string(os.PathSeparator)+"."] = map[string]string{"B": "two"}
		vd.Profiles[opts.profile][relativePath] = map[string]string{"C": "three"}
	})

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, nil, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if strings.Contains(got, "candidate") {
		t.Fatalf("ambiguous paths should not be candidates: %q", got)
	}
	if !strings.Contains(got, "normalized path collision") {
		t.Fatalf("collision should be reported as skipped: %q", got)
	}
	if !strings.Contains(got, "stored path is relative") {
		t.Fatalf("relative path should be reported as skipped: %q", got)
	}
}

func TestRunPathPruneMissingJSONOutput(t *testing.T) {
	opts := setupUnlockedForSet(t)
	missingPath := filepath.Join(t.TempDir(), "missing")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "A=one")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runPathPruneMissing(opts, []string{"--json"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	var got pathPruneMissingJSONOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v; output=%q", err, out.String())
	}
	if got.Mode != "preview" || got.TotalScopes != 1 || got.TotalKeys != 1 {
		t.Fatalf("unexpected JSON totals: %+v", got)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Profile != opts.profile || got.Candidates[0].Path != missingPath || got.Candidates[0].KeyCount != 1 {
		t.Fatalf("unexpected JSON candidates: %+v", got.Candidates)
	}
	if len(got.Pruned) != 0 {
		t.Fatalf("preview JSON should not populate pruned: %+v", got.Pruned)
	}
}

func setPathScopeForPruneTest(t *testing.T, opts globalOptions, profile, path string, assignments ...string) {
	t.Helper()
	scopeOpts := opts
	scopeOpts.profile = profile
	scopeOpts.path = filepath.Clean(path)
	if err := runSet(scopeOpts, assignments, strings.NewReader(""), ioDiscardForPruneTest{}); err != nil {
		t.Fatal(err)
	}
}

func setSharedForPruneTest(t *testing.T, opts globalOptions, assignment string) {
	t.Helper()
	if err := runSet(opts, []string{"--shared", assignment}, strings.NewReader(""), ioDiscardForPruneTest{}); err != nil {
		t.Fatal(err)
	}
}

func hasPathScopeForPruneTest(t *testing.T, opts globalOptions, profile, path string) bool {
	t.Helper()
	vd := loadVaultForPruneTest(t, opts)
	_, ok := vd.Profiles[profile][filepath.Clean(path)]
	return ok
}

func loadVaultForPruneTest(t *testing.T, opts globalOptions) *vaultData {
	t.Helper()
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	return vd
}

func mutateVaultForPruneTest(t *testing.T, opts globalOptions, mutate func(*vaultData)) {
	t.Helper()
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	mutate(vd)
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}
}

type ioDiscardForPruneTest struct{}

func (ioDiscardForPruneTest) Write(p []byte) (int, error) {
	return len(p), nil
}
