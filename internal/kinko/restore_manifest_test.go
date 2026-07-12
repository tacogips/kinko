package kinko

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// buildManifestFixtureEntries builds a full set of archive entries matching
// the required allowlist (plus an optional bootstrap entry) with a manifest
// that correctly cross-references them, mirroring what
// collectBackupArchiveEntries (internal/kinko/backup.go) produces.
func buildManifestFixtureEntries(t *testing.T, includeBootstrap bool) []backupArchiveEntry {
	t.Helper()
	now := time.Now().UTC()

	files := []string{
		"kinko-backup/vault/meta.v1.json",
		"kinko-backup/vault/vault.v1.bin",
		"kinko-backup/vault/config.v1.bin",
		"kinko-backup/vault/" + vaultMarker,
	}
	var bootstrapName string
	if includeBootstrap {
		bootstrapName = "kinko-backup/config/bootstrap.toml"
		files = append(files, bootstrapName)
	}

	manifest := restoreManifest{
		Version:          backupManifestVersion,
		CreatedAtUTC:     now.Format(time.RFC3339),
		BootstrapPresent: includeBootstrap,
		Files:            files,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}

	entries := []backupArchiveEntry{
		{name: "kinko-backup/manifest.json", data: manifestBytes, modTime: now},
		{name: "kinko-backup/vault/meta.v1.json", data: []byte(`{"meta":true}`), modTime: now},
		{name: "kinko-backup/vault/vault.v1.bin", data: []byte("vault-bin-payload"), modTime: now},
		{name: "kinko-backup/vault/config.v1.bin", data: []byte("config-bin-payload"), modTime: now},
		{name: "kinko-backup/vault/" + vaultMarker, data: []byte("kinko-vault-v1\n"), modTime: now},
	}
	if includeBootstrap {
		entries = append(entries, backupArchiveEntry{
			name:    bootstrapName,
			data:    []byte("kinko_dir=/tmp/example\n"),
			modTime: now,
		})
	}
	return entries
}

func buildValidatedRestoreZipArchive(t *testing.T, entries []backupArchiveEntry, password string) *restoreZipArchive {
	t.Helper()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "manifest-test.zip")
	if err := writePasswordLockedZip(archivePath, password, entries); err != nil {
		t.Fatalf("writePasswordLockedZip: %v", err)
	}
	archive, err := readPasswordLockedZip(archivePath, password)
	if err != nil {
		t.Fatalf("readPasswordLockedZip: %v", err)
	}
	return archive
}

func TestValidateRestoreArchiveEntries_ValidArchiveWithoutBootstrap(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	got, err := validateRestoreArchiveEntries(archive)
	if err != nil {
		t.Fatalf("validateRestoreArchiveEntries: %v", err)
	}
	if string(got.metaJSON) != `{"meta":true}` {
		t.Fatalf("metaJSON=%q", got.metaJSON)
	}
	if string(got.vaultBin) != "vault-bin-payload" {
		t.Fatalf("vaultBin=%q", got.vaultBin)
	}
	if string(got.configBin) != "config-bin-payload" {
		t.Fatalf("configBin=%q", got.configBin)
	}
	if string(got.marker) != "kinko-vault-v1\n" {
		t.Fatalf("marker=%q", got.marker)
	}
	if got.bootstrapName != "" {
		t.Fatalf("bootstrapName=%q want empty", got.bootstrapName)
	}
	if got.bootstrapBytes != nil {
		t.Fatalf("bootstrapBytes=%q want nil", got.bootstrapBytes)
	}
}

func TestValidateRestoreArchiveEntries_ValidArchiveWithBootstrap(t *testing.T) {
	entries := buildManifestFixtureEntries(t, true)
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	got, err := validateRestoreArchiveEntries(archive)
	if err != nil {
		t.Fatalf("validateRestoreArchiveEntries: %v", err)
	}
	if got.bootstrapName != "kinko-backup/config/bootstrap.toml" {
		t.Fatalf("bootstrapName=%q", got.bootstrapName)
	}
	if string(got.bootstrapBytes) != "kinko_dir=/tmp/example\n" {
		t.Fatalf("bootstrapBytes=%q", got.bootstrapBytes)
	}
}

func TestValidateRestoreArchiveEntries_MissingRequiredEntry(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	// Drop the vault.v1.bin entry, but keep the manifest's Files list
	// pointing at it (missing-entry check should fire before/independent of
	// the files-list cross-check).
	filtered := make([]backupArchiveEntry, 0, len(entries))
	for _, e := range entries {
		if e.name == "kinko-backup/vault/vault.v1.bin" {
			continue
		}
		filtered = append(filtered, e)
	}
	archive := buildValidatedRestoreZipArchive(t, filtered, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_ExtraUnexpectedEntry(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	entries = append(entries, backupArchiveEntry{
		name:    "kinko-backup/unexpected.txt",
		data:    []byte("surprise"),
		modTime: time.Now().UTC(),
	})
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_DuplicateBootstrapEntries(t *testing.T) {
	entries := buildManifestFixtureEntries(t, true)
	entries = append(entries, backupArchiveEntry{
		name:    "kinko-backup/config/other-bootstrap.toml",
		data:    []byte("kinko_dir=/tmp/other\n"),
		modTime: time.Now().UTC(),
	})
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_ManifestVersionMismatch(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	for i, e := range entries {
		if e.name != "kinko-backup/manifest.json" {
			continue
		}
		var manifest restoreManifest
		if err := json.Unmarshal(e.data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Version = 2
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].data = b
	}
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_ManifestFilesListMismatch(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	for i, e := range entries {
		if e.name != "kinko-backup/manifest.json" {
			continue
		}
		var manifest restoreManifest
		if err := json.Unmarshal(e.data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, "kinko-backup/vault/phantom-file.bin")
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].data = b
	}
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_BootstrapPresentTrueButAbsent(t *testing.T) {
	entries := buildManifestFixtureEntries(t, false)
	for i, e := range entries {
		if e.name != "kinko-backup/manifest.json" {
			continue
		}
		var manifest restoreManifest
		if err := json.Unmarshal(e.data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.BootstrapPresent = true
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].data = b
	}
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func TestValidateRestoreArchiveEntries_BootstrapPresentFalseButPresent(t *testing.T) {
	entries := buildManifestFixtureEntries(t, true)
	for i, e := range entries {
		if e.name != "kinko-backup/manifest.json" {
			continue
		}
		var manifest restoreManifest
		if err := json.Unmarshal(e.data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.BootstrapPresent = false
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].data = b
	}
	archive := buildValidatedRestoreZipArchive(t, entries, "pw")

	_, err := validateRestoreArchiveEntries(archive)
	assertRestoreManifestPolicyError(t, err)
}

func assertRestoreManifestPolicyError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var zerr *zipReadError
	if !asZipReadError(err, &zerr) {
		t.Fatalf("expected *zipReadError, got %T: %v", err, err)
	}
	if zerr.kind != zipReadErrorKindPolicy {
		t.Fatalf("kind=%v want=%v (%v)", zerr.kind, zipReadErrorKindPolicy, err)
	}
}
