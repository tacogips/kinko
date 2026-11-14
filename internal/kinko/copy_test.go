package kinko

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyLocalToLocalKeySuccess(t *testing.T) {
	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	setPathScopeForPruneTest(t, opts, opts.profile, sourcePath, "COPY_ME=from-source")

	var out bytes.Buffer
	if err := runCopy(opts, []string{"local-to-local", "COPY_ME", "--from-path", sourcePath}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "COPY_ME"); got != "from-source" {
		t.Fatalf("destination COPY_ME=%q", got)
	}
	vd := loadVaultForCopyTest(t, opts)
	if got := vd.Profiles[opts.profile][filepath.Clean(sourcePath)]["COPY_ME"]; got != "from-source" {
		t.Fatalf("source COPY_ME changed: %q", got)
	}
	if !strings.Contains(out.String(), "COPY_ME copied from profile=") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestCopyLocalToLocalAllSuccess(t *testing.T) {
	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	setPathScopeForPruneTest(t, opts, opts.profile, sourcePath, "B=two", "A=one")
	var out bytes.Buffer
	if err := runSet(opts, []string{"KEEP=keep"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runCopy(opts, []string{"local-to-local", "*", "--from-path", sourcePath}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "one" {
		t.Fatalf("destination A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "two" {
		t.Fatalf("destination B=%q", got)
	}
	if got := valueAtScope(t, opts, "KEEP"); got != "keep" {
		t.Fatalf("destination KEEP=%q", got)
	}
	if !strings.Contains(out.String(), "A,B copied") {
		t.Fatalf("expected sorted copied keys, got %q", out.String())
	}
}

func TestCopyRequiresOverwriteForDestinationConflict(t *testing.T) {
	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	setPathScopeForPruneTest(t, opts, opts.profile, sourcePath, "A=source", "B=source")
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=destination"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	err := runCopy(opts, []string{"local-to-local", "*", "--from-path", sourcePath}, &out)
	if err == nil {
		t.Fatal("expected destination conflict")
	}
	if !strings.Contains(err.Error(), "destination secret already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := valueAtScope(t, opts, "A"); got != "destination" {
		t.Fatalf("destination A changed: %q", got)
	}
	vd := loadVaultForCopyTest(t, opts)
	if _, ok := vd.Profiles[opts.profile][opts.path]["B"]; ok {
		t.Fatal("copy must not partially write non-conflicting keys after a conflict")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no success output on conflict, got %q", out.String())
	}
}

func TestCopyOverwriteReplacesDestination(t *testing.T) {
	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	setPathScopeForPruneTest(t, opts, opts.profile, sourcePath, "A=source")
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=destination"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runCopy(opts, []string{"local-to-local", "A", "--from-path", sourcePath, "--overwrite"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "source" {
		t.Fatalf("destination A=%q", got)
	}
	vd := loadVaultForCopyTest(t, opts)
	if got := vd.Profiles[opts.profile][filepath.Clean(sourcePath)]["A"]; got != "source" {
		t.Fatalf("source A changed: %q", got)
	}
}

func TestCopyLocalSharedDirections(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"LOCAL_ONLY=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "SHARED_ONLY=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runCopy(opts, []string{"local-to-shared", "LOCAL_ONLY"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "LOCAL_ONLY"); got != "local" {
		t.Fatalf("shared LOCAL_ONLY=%q", got)
	}
	if got := valueAtScope(t, opts, "LOCAL_ONLY"); got != "local" {
		t.Fatalf("local source was deleted: %q", got)
	}

	out.Reset()
	if err := runCopy(opts, []string{"shared-to-local", "SHARED_ONLY"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "SHARED_ONLY"); got != "shared" {
		t.Fatalf("local SHARED_ONLY=%q", got)
	}
	if got := valueAtShared(t, opts, "SHARED_ONLY"); got != "shared" {
		t.Fatalf("shared source was deleted: %q", got)
	}
}

func TestCopyLocalSharedAllDirections(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"B=two", "A=one"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runCopy(opts, []string{"local-to-shared", "*"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "A"); got != "one" {
		t.Fatalf("shared A=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "two" {
		t.Fatalf("shared B=%q", got)
	}
	if got := valueAtScope(t, opts, "A"); got != "one" {
		t.Fatalf("local source A changed: %q", got)
	}

	destination := opts
	destination.path = filepath.Join(t.TempDir(), "destination")
	out.Reset()
	if err := runCopy(destination, []string{"shared-to-local", "*"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, destination, "A"); got != "one" {
		t.Fatalf("destination A=%q", got)
	}
	if got := valueAtScope(t, destination, "B"); got != "two" {
		t.Fatalf("destination B=%q", got)
	}
	if !strings.Contains(out.String(), "A,B copied") {
		t.Fatalf("expected sorted copied keys, got %q", out.String())
	}
}

func TestCopyDoesNotPrintSecretValues(t *testing.T) {
	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	const secretValue = "do-not-print-copy-value"
	setPathScopeForPruneTest(t, opts, opts.profile, sourcePath, "COPY_ME="+secretValue)

	var out bytes.Buffer
	if err := runCopy(opts, []string{"local-to-local", "COPY_ME", "--from-path", sourcePath}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secretValue) {
		t.Fatalf("copy output exposed secret value: %q", out.String())
	}
}

func TestCopyRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing direction", args: nil, want: "copy requires a direction"},
		{name: "unknown direction", args: []string{"sideways", "A"}, want: "unknown direction"},
		{name: "missing key", args: []string{"local-to-local", "--from-path", "/tmp/source"}, want: "requires a key or *"},
		{name: "extra key", args: []string{"shared-to-local", "A", "B"}, want: "exactly one key"},
		{name: "invalid key", args: []string{"shared-to-local", "1BAD"}, want: "invalid environment key"},
		{name: "missing from path", args: []string{"local-to-local", "A"}, want: "requires --from-path"},
		{name: "from path on shared direction", args: []string{"shared-to-local", "A", "--from-path", "/tmp/source"}, want: "only valid with local-to-local"},
		{name: "unknown flag", args: []string{"shared-to-local", "A", "--force"}, want: "unknown flag"},
		{name: "overwrite value", args: []string{"shared-to-local", "A", "--overwrite=true"}, want: "does not accept a value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCopyArgs(tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in %q", tc.want, err.Error())
			}
		})
	}
}

func loadVaultForCopyTest(t *testing.T, opts globalOptions) *vaultData {
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
