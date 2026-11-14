package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveLocalToSharedSuccess(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "MOVE_ME"); got != "local" {
		t.Fatalf("shared MOVE_ME=%q", got)
	}
	vd := loadVaultForMoveTest(t, opts)
	if _, ok := vd.Profiles[opts.profile][opts.path]["MOVE_ME"]; ok {
		t.Fatal("expected source local key to be deleted")
	}
	if !strings.Contains(out.String(), "MOVE_ME moved from profile=") || !strings.Contains(out.String(), "to shared scope") {
		t.Fatalf("unexpected success output: %q", out.String())
	}
}

func TestMoveSharedToLocalSuccess(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "MOVE_ME=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runMove(opts, []string{"shared-to-local", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "MOVE_ME"); got != "shared" {
		t.Fatalf("local MOVE_ME=%q", got)
	}
	vd := loadVaultForMoveTest(t, opts)
	if _, ok := vd.Shared["MOVE_ME"]; ok {
		t.Fatal("expected source shared key to be deleted")
	}
	if !strings.Contains(out.String(), "MOVE_ME moved from shared scope to profile=") {
		t.Fatalf("unexpected success output: %q", out.String())
	}
}

func TestMoveRequiresOverwriteForDestinationConflict(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "MOVE_ME=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected destination conflict")
	}
	if !strings.Contains(err.Error(), "destination secret already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := valueAtScope(t, opts, "MOVE_ME"); got != "local" {
		t.Fatalf("source local changed: %q", got)
	}
	if got := valueAtShared(t, opts, "MOVE_ME"); got != "shared" {
		t.Fatalf("destination shared changed: %q", got)
	}
	if out.Len() != 0 || errBuf.Len() != 0 {
		t.Fatalf("expected no output on conflict, stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestMoveOverwriteReplacesDestinationAndDeletesSource(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "MOVE_ME=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--overwrite", "--yes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "MOVE_ME"); got != "local" {
		t.Fatalf("shared MOVE_ME=%q", got)
	}
	vd := loadVaultForMoveTest(t, opts)
	if _, ok := vd.Profiles[opts.profile][opts.path]["MOVE_ME"]; ok {
		t.Fatal("expected overwritten move to delete source local key")
	}
}

func TestMoveMissingSourceDoesNotCreateDestinationScope(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	err := runMove(opts, []string{"shared-to-local", "MISSING", "--yes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing source error")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on missing source, got %q", out.String())
	}
	vd := loadVaultForMoveTest(t, opts)
	if vd.Profiles[opts.profile] != nil {
		if _, ok := vd.Profiles[opts.profile][opts.path]; ok {
			t.Fatal("missing shared source must not create local destination scope")
		}
	}

	err = runMove(opts, []string{"local-to-shared", "MISSING", "--yes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing local source error")
	}
	vd = loadVaultForMoveTest(t, opts)
	if len(vd.Shared) != 0 {
		t.Fatalf("missing local source must not change shared scope: %#v", vd.Shared)
	}
}

func TestMoveConfirmationDeclineLeavesVaultUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME"}, strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "aborted\n" {
		t.Fatalf("out=%q", out.String())
	}
	if !strings.Contains(errBuf.String(), `Move key "MOVE_ME" from profile=`) {
		t.Fatalf("expected confirmation prompt, got %q", errBuf.String())
	}
	if got := valueAtScope(t, opts, "MOVE_ME"); got != "local" {
		t.Fatalf("source changed after decline: %q", got)
	}
	vd := loadVaultForMoveTest(t, opts)
	if _, ok := vd.Shared["MOVE_ME"]; ok {
		t.Fatal("destination changed after decline")
	}
}

func TestMoveYesBypassesPromptOnly(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("--yes should not prompt, got stderr %q", errBuf.String())
	}
	if got := valueAtShared(t, opts, "MOVE_ME"); got != "local" {
		t.Fatalf("shared MOVE_ME=%q", got)
	}
}

func TestMoveDoesNotPrintSecretValues(t *testing.T) {
	opts := setupUnlockedForSet(t)
	const secretValue = "do-not-print-this"
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=" + secretValue}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME"}, strings.NewReader("y\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	combined := out.String() + errBuf.String()
	if strings.Contains(combined, secretValue) {
		t.Fatalf("move output exposed secret value: stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestMovePreservesUnrelatedScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	otherPath := filepath.Join(t.TempDir(), "other")
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local", "KEEP_LOCAL=local-keep"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "KEEP_SHARED=shared-keep"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	setPathScopeForPruneTest(t, opts, opts.profile, otherPath, "OTHER_PATH=other")

	if err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	vd := loadVaultForMoveTest(t, opts)
	if got := vd.Profiles[opts.profile][opts.path]["KEEP_LOCAL"]; got != "local-keep" {
		t.Fatalf("KEEP_LOCAL=%q", got)
	}
	if got := vd.Shared["KEEP_SHARED"]; got != "shared-keep" {
		t.Fatalf("KEEP_SHARED=%q", got)
	}
	if got := vd.Profiles[opts.profile][otherPath]["OTHER_PATH"]; got != "other" {
		t.Fatalf("OTHER_PATH=%q", got)
	}
}

func TestMovePersistenceFailureLeavesVaultUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"MOVE_ME=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "DEST=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	tmpDir := filepath.Join(opts.dataDir, "vault", "vault.v1.bin.tmp")
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "block"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(tmpDir, "block"))
		_ = os.Remove(tmpDir)
	})

	out.Reset()
	err := runMove(opts, []string{"local-to-shared", "MOVE_ME", "--yes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no success output on persistence failure, got %q", out.String())
	}
	vd := loadVaultForMoveTest(t, opts)
	if got := vd.Profiles[opts.profile][opts.path]["MOVE_ME"]; got != "local" {
		t.Fatalf("persisted source changed after save failure: %q", got)
	}
	if _, ok := vd.Shared["MOVE_ME"]; ok {
		t.Fatal("persisted destination changed after save failure")
	}
	if got := vd.Shared["DEST"]; got != "shared" {
		t.Fatalf("unrelated shared key changed: %q", got)
	}
}

func TestMoveRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing direction", args: nil, want: "move requires a direction"},
		{name: "unknown direction", args: []string{"sideways", "A"}, want: "unknown direction"},
		{name: "missing key", args: []string{"local-to-shared"}, want: "move requires a key"},
		{name: "extra key", args: []string{"local-to-shared", "A", "B"}, want: "exactly one key"},
		{name: "invalid key", args: []string{"local-to-shared", "1BAD"}, want: "invalid environment key"},
		{name: "unknown flag", args: []string{"local-to-shared", "A", "--force"}, want: "unknown flag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMoveArgs(tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in %q", tc.want, err.Error())
			}
		})
	}
}

func loadVaultForMoveTest(t *testing.T, opts globalOptions) *vaultData {
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
