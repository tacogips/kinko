package kinko

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreStagingDoesNotFollowPreexistingSymlink(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unrelated-file")
	original := []byte("original unrelated data")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	stagingPath := filepath.Join(dataDir, "vault", "meta.v1.json"+restoreVaultTmpSuffix)
	if err := os.Symlink(target, stagingPath); err != nil {
		t.Fatal(err)
	}
	if err := checkRestoreTargetStatePolicy(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := stageAndCommitRestoreFiles(dataDir, buildFakeValidatedRestoreArchive()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("restore overwrote unrelated target: %q", got)
	}
	info, err := os.Lstat(stagingPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("restore modified the pre-existing staging symlink")
	}
	assertPrivateRestoreFiles(t, dataDir)
}

func TestRestoreStagingDoesNotReusePermissiveFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	stagingPath := filepath.Join(dataDir, "vault", "meta.v1.json"+restoreVaultTmpSuffix)
	original := []byte("pre-existing staging file")
	if err := os.WriteFile(stagingPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stagingPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stageAndCommitRestoreFiles(dataDir, buildFakeValidatedRestoreArchive()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stagingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("restore modified pre-existing staging file: %q", got)
	}
	assertPrivateRestoreFiles(t, dataDir)
}

func TestRestoreStagingFailurePreservesPreexistingStagingPath(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	vaultDir := filepath.Join(dataDir, "vault")
	stagingPath := filepath.Join(vaultDir, "meta.v1.json"+restoreVaultTmpSuffix)
	if err := os.WriteFile(stagingPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(vaultDir, "config.v1.bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stageAndCommitRestoreFiles(dataDir, buildFakeValidatedRestoreArchive()); err == nil {
		t.Fatal("expected commit failure")
	}
	got, err := os.ReadFile(stagingPath)
	if err != nil || string(got) != "keep" {
		t.Fatalf("pre-existing staging path modified: data=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("unexpected leftovers after rollback: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(vaultDir, vaultMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker exists after failed restore: %v", err)
	}
}

func assertPrivateRestoreFiles(t *testing.T, dataDir string) {
	t.Helper()
	for _, name := range []string{"meta.v1.json", "vault.v1.bin", "config.v1.bin", vaultMarker} {
		info, err := os.Lstat(filepath.Join(dataDir, "vault", name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("restored %s has mode %v, want regular 0600", name, info.Mode())
		}
	}
}
