package kinko

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunFolderRemove_DeletesConfigAndStorageByDefault(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := mustFolderRecord(t, opts, "private")
	storageDir := folderStorageDir(opts.dataDir, record)

	var out bytes.Buffer
	if err := runFolder(opts, []string{folderRemove, "--yes", "private"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder remove failed: %v", err)
	}
	if got := out.String(); got != "folder removed: private\n" {
		t.Fatalf("unexpected output: %q", got)
	}
	if _, err := os.Stat(storageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("storage should be deleted, stat error=%v", err)
	}
	if records := mustFolderRecords(t, opts); len(records) != 0 {
		t.Fatalf("folder record should be removed, got %#v", records)
	}
}

func TestRunFolderRemove_PreservesStorageWithKeepStorage(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := mustFolderRecord(t, opts, "private")
	storageDir := folderStorageDir(opts.dataDir, record)

	if err := runFolder(opts, []string{folderRemove, "--keep-storage", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder remove failed: %v", err)
	}
	if _, err := os.Stat(storageDir); err != nil {
		t.Fatalf("storage should be preserved: %v", err)
	}
	if records := mustFolderRecords(t, opts); len(records) != 0 {
		t.Fatalf("folder record should be removed, got %#v", records)
	}
}

func TestRunFolderRemove_ConfirmsStorageDeletion(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := mustFolderRecord(t, opts, "private")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runFolder(opts, []string{folderRemove, "private"}, strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatalf("declined remove should not fail: %v", err)
	}
	if got := out.String(); got != "aborted\n" {
		t.Fatalf("unexpected abort output: %q", got)
	}
	if !strings.Contains(errBuf.String(), "delete encrypted storage") {
		t.Fatalf("expected destructive prompt, got %q", errBuf.String())
	}
	if _, err := os.Stat(folderStorageDir(opts.dataDir, record)); err != nil {
		t.Fatalf("storage should remain after abort: %v", err)
	}
	if records := mustFolderRecords(t, opts); len(records) != 1 {
		t.Fatalf("folder record should remain after abort, got %#v", records)
	}
}

func TestRunFolderRemove_RefusesMountedFolder(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := mustFolderRecord(t, opts, "private")
	fake.mu.Lock()
	fake.mounted[folderMountpoint(record)] = true
	fake.mu.Unlock()

	err := runFolder(opts, []string{folderRemove, "--yes", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected mounted folder refusal")
	}
	if !strings.Contains(err.Error(), "folder is mounted: private") {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodePolicyFailed)
	}
	if records := mustFolderRecords(t, opts); len(records) != 1 {
		t.Fatalf("folder record should remain after mounted refusal, got %#v", records)
	}
}

func TestRunFolderRemove_KeepsRecordWhenStorageDeletionFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	record := mustFolderRecord(t, opts, "private")
	storageDir := folderStorageDir(opts.dataDir, record)
	if err := os.RemoveAll(storageDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), storageDir); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderRemove, "--yes", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected storage deletion failure")
	}
	if !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := ExitCode(err); code != exitCodeIOFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeIOFailed)
	}
	if records := mustFolderRecords(t, opts); len(records) != 1 {
		t.Fatalf("folder record should remain after storage deletion failure, got %#v", records)
	}
}

func TestRunFolderUnlock_SerializesMountTransition(t *testing.T) {
	backend := withBlockingFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	record, err := newFolderRecord(opts.profile, opts.path, "private", folderBackendName(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dek, cfg, _, err := loadFolderConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureFolderStorageDirectory(opts.dataDir, record); err != nil {
		t.Fatal(err)
	}
	if err := saveFolderRecordsToConfig(cfg, []FolderRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := saveFolderConfig(opts.dataDir, dek, cfg); err != nil {
		t.Fatal(err)
	}
	withFolderOwnerExit(t, func() {})

	done := make(chan error, 1)
	go func() {
		done <- runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	}()
	<-backend.mountStarted

	err = runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected concurrent unlock to be serialized")
	}
	if !strings.Contains(err.Error(), "folder lifecycle in progress") {
		t.Fatalf("unexpected concurrent unlock error: %v", err)
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeLockConflict)
	}
	close(backend.allowMount)
	if err := <-done; err != nil {
		t.Fatalf("first unlock failed: %v", err)
	}
	if got := backend.mountCount(); got != 1 {
		t.Fatalf("expected one backend mount, got %d", got)
	}
}

type blockingFolderBackend struct {
	mu           sync.Mutex
	mounted      map[string]bool
	mountStarted chan struct{}
	allowMount   chan struct{}
	mounts       int
	startOnce    sync.Once
}

func (b *blockingFolderBackend) Ensure(context.Context, FolderRecord, string) error {
	return nil
}

func (b *blockingFolderBackend) Mount(_ context.Context, _ FolderRecord, _ string, mountpoint string) error {
	b.startOnce.Do(func() {
		close(b.mountStarted)
	})
	<-b.allowMount
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounts++
	b.mounted[mountpoint] = true
	return nil
}

func (b *blockingFolderBackend) Unmount(_ context.Context, _ FolderRecord, mountpoint string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounted[mountpoint] = false
	return nil
}

func (b *blockingFolderBackend) Status(_ context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return FolderMountStatus{Mounted: b.mounted[mountpoint]}, nil
}

func (b *blockingFolderBackend) mountCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mounts
}

func withBlockingFolderBackend(t *testing.T) *blockingFolderBackend {
	t.Helper()
	backend := &blockingFolderBackend{
		mounted:      map[string]bool{},
		mountStarted: make(chan struct{}),
		allowMount:   make(chan struct{}),
	}
	prev := newFolderBackend
	newFolderBackend = func(string) FolderBackend {
		return backend
	}
	t.Cleanup(func() {
		newFolderBackend = prev
	})
	return backend
}

func mustFolderRecord(t *testing.T, opts globalOptions, name string) FolderRecord {
	t.Helper()
	records := mustFolderRecords(t, opts)
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func mustFolderRecords(t *testing.T, opts globalOptions) []FolderRecord {
	t.Helper()
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
