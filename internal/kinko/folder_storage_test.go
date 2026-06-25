package kinko

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFolderStorageRejectsInvalidFolderIDBeforeCreatingRoot(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{FolderID: "../outside"}

	exists, err := folderStorageExists(dataDir, record)
	if err == nil || !strings.Contains(err.Error(), "folder storage id must") {
		t.Fatalf("expected invalid folder storage id from exists check, exists=%v err=%v", exists, err)
	}

	err = ensureFolderStorageDirectory(dataDir, record)
	if err == nil || !strings.Contains(err.Error(), "folder storage id must") {
		t.Fatalf("expected invalid folder storage id from ensure, got %v", err)
	}
	if _, statErr := os.Stat(folderStorageRoot(dataDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("storage root should not be created for invalid folder id, stat error=%v", statErr)
	}
}

func TestFolderUnlockRejectsInvalidStorageIDBeforeBackendAccess(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	record, err := newFolderRecord(opts.profile, opts.path, "private", folderBackendName(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	record.FolderID = "../outside"

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFolderRecordsToConfig(cfg, []FolderRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(opts.dataDir, dek, cfg); err != nil {
		t.Fatal(err)
	}

	err = runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "folder storage id must") {
		t.Fatalf("expected invalid folder storage id error, got %v", err)
	}
	ensures, mounts, _ := fake.counts()
	if ensures != 0 || mounts != 0 || fake.statusCalls() != 0 {
		t.Fatalf("backend should not be accessed for invalid folder id, ensures=%d mounts=%d statuses=%d", ensures, mounts, fake.statusCalls())
	}
	if _, statErr := os.Stat(filepath.Join(opts.dataDir, "outside")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid folder id should not create escaped storage path, stat error=%v", statErr)
	}
}

func TestFolderUnlockRejectsMissingStorageBeforeBackendAccess(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	storageDir := folderStorageDir(opts.dataDir, record)
	if err := os.RemoveAll(storageDir); err != nil {
		t.Fatal(err)
	}
	ensuresBefore, _, _ := fake.counts()
	statusesBefore := fake.statusCalls()

	err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "folder storage path does not exist") {
		t.Fatalf("expected missing folder storage error, got %v", err)
	}
	ensuresAfter, mounts, _ := fake.counts()
	if ensuresAfter != ensuresBefore {
		t.Fatalf("backend Ensure calls changed after missing storage error: before=%d after=%d", ensuresBefore, ensuresAfter)
	}
	if statusesAfter := fake.statusCalls(); statusesAfter != statusesBefore {
		t.Fatalf("backend Status calls changed after missing storage error: before=%d after=%d", statusesBefore, statusesAfter)
	}
	if mounts != 0 {
		t.Fatalf("Mount calls=%d want 0", mounts)
	}
	if _, statErr := os.Stat(storageDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing folder storage should not be recreated, stat error=%v", statErr)
	}
}

func TestFolderStorageMetadataRejectsSymlinkedMetadataFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:      "private",
		Profile:   "default",
		Path:      t.TempDir(),
		Backend:   folderBackendName(),
		FolderID:  "folder-id",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := ensureFolderStorageDirectory(dataDir, record); err != nil {
		t.Fatal(err)
	}
	storageDir := folderStorageDir(dataDir, record)
	target := filepath.Join(t.TempDir(), "target-meta.json")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(storageDir, "meta.json")); err != nil {
		t.Fatal(err)
	}

	err := ensureFolderStorageMetadata(dataDir, record)
	if err == nil || !strings.Contains(err.Error(), "folder storage metadata must not be a symlink") {
		t.Fatalf("expected symlinked metadata error, got %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("metadata symlink target was modified: %q", got)
	}
}
