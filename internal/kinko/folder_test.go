package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeFolderBackend struct {
	mu                  sync.Mutex
	mounted             map[string]bool
	ensures             int
	mounts              int
	locks               int
	statuses            int
	dataDir             string
	ensureSawStorageDir bool
	unmountErr          error
}

func (f *fakeFolderBackend) Ensure(_ context.Context, record FolderRecord, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensures++
	info, err := os.Lstat(folderStorageDir(f.dataDir, record))
	f.ensureSawStorageDir = err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
	return nil
}

func (f *fakeFolderBackend) Mount(_ context.Context, _ FolderRecord, _ string, mountpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mounts++
	f.mounted[mountpoint] = true
	return nil
}

func (f *fakeFolderBackend) Unmount(_ context.Context, _ FolderRecord, mountpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locks++
	if f.unmountErr != nil {
		return f.unmountErr
	}
	f.mounted[mountpoint] = false
	return nil
}

func (f *fakeFolderBackend) Status(_ context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses++
	return FolderMountStatus{Mounted: f.mounted[mountpoint]}, nil
}

func (f *fakeFolderBackend) isMounted(mountpoint string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mounted[mountpoint]
}

func (f *fakeFolderBackend) counts() (ensures, mounts, locks int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensures, f.mounts, f.locks
}

func (f *fakeFolderBackend) statusCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses
}

func withFakeFolderBackend(t *testing.T) *fakeFolderBackend {
	t.Helper()
	fake := &fakeFolderBackend{mounted: map[string]bool{}}
	prev := newFolderBackend
	newFolderBackend = func(dataDir string) FolderBackend {
		fake.dataDir = dataDir
		return fake
	}
	t.Cleanup(func() {
		newFolderBackend = prev
	})
	return fake
}

func withFolderOwnerExit(t *testing.T, wait func()) {
	t.Helper()
	prev := waitForFolderOwnerExit
	waitForFolderOwnerExit = func(<-chan os.Signal) {
		wait()
	}
	t.Cleanup(func() {
		waitForFolderOwnerExit = prev
	})
}

func withFailingFolderConfigSave(t *testing.T, saveErr error) {
	t.Helper()
	prev := saveFolderConfig
	saveFolderConfig = func(string, []byte, map[string]string) error {
		return saveErr
	}
	t.Cleanup(func() {
		saveFolderConfig = prev
	})
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func setupUnlockedFolderTest(t *testing.T) globalOptions {
	t.Helper()
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	opts.path = t.TempDir()
	opts.configPath = filepath.Join(t.TempDir(), "bootstrap.toml")
	return opts
}

func TestFolderAddRegistersEncryptedConfigAndGitignore(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := os.WriteFile(filepath.Join(opts.path, ".gitignore"), []byte("private/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	cfg["editor"] = "vim"
	if err := saveConfig(opts.dataDir, dek, cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	if got := out.String(); got != "folder added: private\n" {
		t.Fatalf("unexpected output: %q", got)
	}
	if fake.ensures != 1 {
		t.Fatalf("Ensure calls=%d want 1", fake.ensures)
	}
	if !fake.ensureSawStorageDir {
		t.Fatal("backend Ensure should receive a pre-created non-symlink storage directory")
	}
	if fake.mounts != 0 {
		t.Fatalf("folder add must not mount, Mount calls=%d", fake.mounts)
	}
	if fake.dataDir != opts.dataDir {
		t.Fatalf("backend dataDir=%q want %q", fake.dataDir, opts.dataDir)
	}

	dek, err = loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg["editor"]; got != "vim" {
		t.Fatalf("existing config key was not preserved: editor=%q", got)
	}
	records, err := loadFolderRecordsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("folder records=%d want 1", len(records))
	}
	record := records[0]
	if record.Name != "private" || record.Profile != opts.profile || record.Path != opts.path {
		t.Fatalf("unexpected record: %#v", record)
	}
	if record.FolderID == "" || record.Backend == "" {
		t.Fatalf("record missing backend identity: %#v", record)
	}

	metaBytes, err := os.ReadFile(filepath.Join(opts.dataDir, "folders", record.FolderID, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata folderStorageMetadata
	if err := json.Unmarshal(metaBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.FormatVersion != 1 || metadata.Backend != record.Backend || metadata.FolderID != record.FolderID {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if strings.Contains(string(metaBytes), opts.path) || strings.Contains(string(metaBytes), record.Name) {
		t.Fatalf("metadata should not contain project path or folder name: %s", metaBytes)
	}

	gitignore, err := os.ReadFile(filepath.Join(opts.path, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(gitignore); got != "private/\n" {
		t.Fatalf(".gitignore entry should remain idempotent, got %q", got)
	}
}

func TestFolderAddDoesNotPersistRecordWhenGitignoreUpdateFails(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := os.Mkdir(filepath.Join(opts.path, ".gitignore"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "read .gitignore") {
		t.Fatalf("expected .gitignore update error, got %v", err)
	}

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	records, err := loadFolderRecordsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("folder record should not be persisted after failed add, got %#v", records)
	}
	if fake.ensures != 0 {
		t.Fatalf("backend Ensure should not run before .gitignore validation, calls=%d", fake.ensures)
	}
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	if _, err := os.Stat(folderStorageDir(opts.dataDir, record)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new folder storage should be cleaned after failed add, stat error=%v", err)
	}

	if err := os.Remove(filepath.Join(opts.path, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("retry folder add failed: %v", err)
	}
}

func TestFolderAddRestoresGitignoreWhenConfigSaveFails(t *testing.T) {
	withFakeFolderBackend(t)
	withFailingFolderConfigSave(t, errors.New("disk full"))
	opts := setupUnlockedFolderTest(t)
	gitignorePath := filepath.Join(opts.path, ".gitignore")
	originalGitignore := []byte("keep-this/\n")
	if err := os.WriteFile(gitignorePath, originalGitignore, 0o640); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "write encrypted folder config") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected encrypted config save error, got %v", err)
	}

	gitignore, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gitignore) != string(originalGitignore) {
		t.Fatalf(".gitignore should be restored after config save failure, got %q", gitignore)
	}
	info, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf(".gitignore mode=%#o want %#o", got, os.FileMode(0o640))
	}

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	records, err := loadFolderRecordsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("folder record should not be persisted after failed config save, got %#v", records)
	}
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	if _, err := os.Stat(folderStorageDir(opts.dataDir, record)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new folder storage should be cleaned after failed config save, stat error=%v", err)
	}
}

func TestFolderAddPreservesExistingGitignoreMode(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	gitignorePath := filepath.Join(opts.path, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("keep-this/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	info, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf(".gitignore mode=%#o want %#o", got, os.FileMode(0o644))
	}
}

func TestFolderAddRemovesNewGitignoreWhenConfigSaveFails(t *testing.T) {
	withFakeFolderBackend(t)
	withFailingFolderConfigSave(t, errors.New("disk full"))
	opts := setupUnlockedFolderTest(t)
	gitignorePath := filepath.Join(opts.path, ".gitignore")

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "write encrypted folder config") {
		t.Fatalf("expected encrypted config save error, got %v", err)
	}
	if _, err := os.Stat(gitignorePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new .gitignore should be removed after config save failure, stat error=%v", err)
	}
}

func TestFolderAddPreservesPreexistingStorageWhenRegistrationFails(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	storageDir := folderStorageDir(opts.dataDir, record)
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storageDir, "marker"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(opts.path, ".gitignore"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "read .gitignore") {
		t.Fatalf("expected .gitignore update error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(storageDir, "marker")); err != nil {
		t.Fatalf("pre-existing folder storage should be preserved after failed add: %v", err)
	}
}

func TestFolderAddRejectsSymlinkedStorageDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	storageDir := folderStorageDir(opts.dataDir, record)
	outsideDir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(storageDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, storageDir); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
		t.Fatalf("expected symlinked folder storage error, got %v", err)
	}
	if fake.ensures != 0 {
		t.Fatalf("backend Ensure should not run for symlinked storage, calls=%d", fake.ensures)
	}
	if _, err := os.Lstat(storageDir); err != nil {
		t.Fatalf("symlinked storage directory should not be removed: %v", err)
	}
}

func TestFolderAddRejectsSymlinkedStorageRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	foldersRoot := filepath.Join(opts.dataDir, "folders")
	if err := os.Symlink(t.TempDir(), foldersRoot); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "folder storage root must not be a symlink") {
		t.Fatalf("expected symlinked folder storage root error, got %v", err)
	}
	if fake.ensures != 0 {
		t.Fatalf("backend Ensure should not run for symlinked storage root, calls=%d", fake.ensures)
	}
}

func TestCleanupFolderStorageDoesNotFollowSymlinkedStorageRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	dataDir := t.TempDir()
	record := FolderRecord{FolderID: "folder-id"}
	outsideRoot := t.TempDir()
	outsideStorageDir := filepath.Join(outsideRoot, record.FolderID)
	if err := os.MkdirAll(outsideStorageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outsideStorageDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRoot, filepath.Join(dataDir, "folders")); err != nil {
		t.Fatal(err)
	}

	cleanupFolderStorage(dataDir, record)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup should not remove storage through symlinked root: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "folders")); err != nil {
		t.Fatalf("cleanup should leave symlinked storage root untouched: %v", err)
	}
}

func TestFolderStorageMetadataRejectsSymlinkedStorageDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	opts := setupUnlockedFolderTest(t)
	record, err := newFolderRecord(opts.profile, opts.path, "private", folderBackendName(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	storageDir := folderStorageDir(opts.dataDir, record)
	if err := os.MkdirAll(filepath.Dir(storageDir), 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, storageDir); err != nil {
		t.Fatal(err)
	}

	err = ensureFolderStorageMetadata(opts.dataDir, record)
	if err == nil || !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
		t.Fatalf("expected symlinked folder storage error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "meta.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not be written through symlink target, stat error=%v", err)
	}
}

func TestFolderAddRejectsSymlinkedGitignoreWithoutWritingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	target := filepath.Join(t.TempDir(), "outside-gitignore")
	original := []byte("keep-this/\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(opts.path, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), ".gitignore must not be a symlink") {
		t.Fatalf("expected symlinked .gitignore error, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("symlink target should not be modified, got %q", got)
	}
	if fake.ensures != 0 {
		t.Fatalf("backend Ensure should not run before .gitignore validation, calls=%d", fake.ensures)
	}
	record := FolderRecord{FolderID: deriveFolderID(opts.profile, opts.path, "private")}
	if _, err := os.Stat(folderStorageDir(opts.dataDir, record)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new folder storage should be cleaned after failed add, stat error=%v", err)
	}
}

func TestFolderGitignoreEscapesPatternNames(t *testing.T) {
	cases := map[string]string{
		"#secret":  `\#secret/`,
		"!secret":  `\!secret/`,
		"a*secret": `a\*secret/`,
		"a?secret": `a\?secret/`,
		"a[secret": `a\[secret/`,
		"a]secret": `a\]secret/`,
	}
	for name, want := range cases {
		if got := gitignoreFolderEntry(name); got != want {
			t.Fatalf("gitignoreFolderEntry(%q)=%q want %q", name, got, want)
		}
		if !gitignoreContainsFolderEntry(want+"\n", name) {
			t.Fatalf("gitignoreContainsFolderEntry did not recognize escaped entry %q", want)
		}
	}
}

func TestFolderGitignoreDoesNotTreatCommentsOrNegationsAsIgnored(t *testing.T) {
	if gitignoreContainsFolderEntry("#secret/\n", "#secret") {
		t.Fatal("commented #secret entry should not count as an active ignore rule")
	}
	if gitignoreContainsFolderEntry("!secret/\n", "!secret") {
		t.Fatal("negated !secret entry should not count as an active ignore rule")
	}
	if gitignoreContainsFolderEntry("# private/\n", "private") {
		t.Fatal("commented private entry should not count as an active ignore rule")
	}
	if !gitignoreContainsFolderEntry("\\#secret/\n", "#secret") {
		t.Fatal("escaped #secret entry should count as an active ignore rule")
	}
	if !gitignoreContainsFolderEntry("\\!secret/\n", "!secret") {
		t.Fatal("escaped !secret entry should count as an active ignore rule")
	}
}

func TestFolderBackendCommandRedactsSecretStdinFromErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires /bin/sh")
	}
	err := runFolderBackendCommand(context.Background(), "supersecret\n", "/bin/sh", "-c", "cat >&2; exit 1")
	if err == nil {
		t.Fatal("expected backend command failure")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("backend error leaked secret stdin: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("backend error did not include redaction marker: %v", err)
	}
}

func TestFolderRejectsUnsafeNames(t *testing.T) {
	badNames := []string{"", ".", "..", "-flag", "../secret", "a/b", `a\b`, "line\nbreak", string(filepath.Separator) + "absolute"}
	for _, name := range badNames {
		if err := validateFolderName(name); err == nil {
			t.Fatalf("validateFolderName(%q) succeeded", name)
		}
	}
}

func TestFolderUnlockPathStatusAndLockUseBackend(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	err := runFolder(opts, []string{folderPath, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("expected not mounted path error, got %v", err)
	}

	mountpoint := filepath.Join(opts.path, "private")
	releaseOwner := make(chan struct{})
	withFolderOwnerExit(t, func() {
		<-releaseOwner
	})
	var unlockOut bytes.Buffer
	unlockErr := make(chan error, 1)
	go func() {
		unlockErr <- runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &unlockOut, &bytes.Buffer{})
	}()
	waitUntil(t, func() bool {
		_, mounts, _ := fake.counts()
		return mounts == 1
	})
	if !fake.isMounted(mountpoint) {
		t.Fatal("folder should be mounted while unlock owner is running")
	}

	var pathOut bytes.Buffer
	if err := runFolder(opts, []string{folderPath, "private"}, strings.NewReader(""), &pathOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder path failed: %v", err)
	}
	if got := strings.TrimSpace(pathOut.String()); got != mountpoint {
		t.Fatalf("path=%q want %q", got, mountpoint)
	}

	var statusOut bytes.Buffer
	if err := runFolder(opts, []string{folderStatus, "private"}, strings.NewReader(""), &statusOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder status failed: %v", err)
	}
	if !strings.Contains(statusOut.String(), "private\tmounted") {
		t.Fatalf("unexpected status output: %q", statusOut.String())
	}

	close(releaseOwner)
	if err := <-unlockErr; err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}
	if !strings.Contains(unlockOut.String(), "path: "+mountpoint) {
		t.Fatalf("unexpected unlock output: %q", unlockOut.String())
	}

	var lockOut bytes.Buffer
	if err := runFolder(opts, []string{folderLock, "private"}, strings.NewReader(""), &lockOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder lock failed: %v", err)
	}
	_, _, locks := fake.counts()
	if locks != 1 {
		t.Fatalf("Unmount calls=%d want 1", locks)
	}
	if got := lockOut.String(); got != "folder locked: private\n" {
		t.Fatalf("unexpected lock output: %q", got)
	}
}

func TestFolderUnlockSoftUnmountsOnOwnerExitByDefault(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	withFolderOwnerExit(t, func() {})

	var out bytes.Buffer
	if err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}
	_, mounts, locks := fake.counts()
	if mounts != 1 {
		t.Fatalf("Mount calls=%d want 1", mounts)
	}
	if locks != 1 {
		t.Fatalf("Unmount calls=%d want 1", locks)
	}
	if !strings.Contains(out.String(), "holding folder unlock") || !strings.Contains(out.String(), "folder locked: private") {
		t.Fatalf("unexpected unlock output: %q", out.String())
	}
}

func TestFolderUnlockDoesNotEnsureBackendStorage(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	ensuresBefore, _, _ := fake.counts()

	withFolderOwnerExit(t, func() {})
	if err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}

	ensuresAfter, mounts, locks := fake.counts()
	if ensuresAfter != ensuresBefore {
		t.Fatalf("folder unlock must not recreate backend storage with Ensure: before=%d after=%d", ensuresBefore, ensuresAfter)
	}
	if mounts != 1 {
		t.Fatalf("Mount calls=%d want 1", mounts)
	}
	if locks != 1 {
		t.Fatalf("Unmount calls=%d want 1", locks)
	}
}

func TestFolderUnlockPreservesPreexistingEmptyMountpoint(t *testing.T) {
	withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	mountpoint := filepath.Join(opts.path, "private")
	if err := os.MkdirAll(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	withFolderOwnerExit(t, func() {})
	if err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("pre-existing mountpoint should remain after unlock exits: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mountpoint is not a directory after unlock exits: %s", mountpoint)
	}
}

func TestFolderLockPreservesMountpointDirectory(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	mountpoint := filepath.Join(opts.path, "private")
	if err := os.MkdirAll(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	fake.mounted[mountpoint] = true

	if err := runFolder(opts, []string{folderLock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder lock failed: %v", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("mountpoint should remain after explicit lock: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mountpoint is not a directory after explicit lock: %s", mountpoint)
	}
}

func TestFolderLockReportsUnmountGuidance(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	mountpoint := filepath.Join(opts.path, "private")
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	fake.mounted[mountpoint] = true
	fake.unmountErr = errors.New("device busy")

	err := runFolder(opts, []string{folderLock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected folder lock to fail")
	}
	if !strings.Contains(err.Error(), "folder unmount failed for private") ||
		!strings.Contains(err.Error(), "close files in the folder") ||
		!strings.Contains(err.Error(), "device busy") {
		t.Fatalf("unexpected unmount error: %v", err)
	}
	if !fake.isMounted(mountpoint) {
		t.Fatal("mount should remain marked mounted after unmount failure")
	}
}

func TestFolderUnlockHoldAcceptedAsCompatibilityNoOp(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	withFolderOwnerExit(t, func() {})

	if err := runFolder(opts, []string{folderUnlock, "--hold", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder unlock --hold failed: %v", err)
	}
	_, mounts, locks := fake.counts()
	if mounts != 1 {
		t.Fatalf("Mount calls=%d want 1", mounts)
	}
	if locks != 1 {
		t.Fatalf("Unmount calls=%d want 1", locks)
	}
}

func TestFolderUnlockRejectsSymlinkedStorageBeforeBackendAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}
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
	if err := os.Symlink(t.TempDir(), storageDir); err != nil {
		t.Fatal(err)
	}
	ensuresBefore, _, _ := fake.counts()
	statusesBefore := fake.statusCalls()

	withFolderOwnerExit(t, func() {})
	err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
		t.Fatalf("expected symlinked folder storage error, got %v", err)
	}
	ensuresAfter, mounts, _ := fake.counts()
	if ensuresAfter != ensuresBefore {
		t.Fatalf("backend Ensure calls changed after symlinked storage error: before=%d after=%d", ensuresBefore, ensuresAfter)
	}
	if statusesAfter := fake.statusCalls(); statusesAfter != statusesBefore {
		t.Fatalf("backend Status calls changed after symlinked storage error: before=%d after=%d", statusesBefore, statusesAfter)
	}
	if mounts != 0 {
		t.Fatalf("Mount calls=%d want 0", mounts)
	}
}

func TestFolderUnlockRequiresUnlockedSession(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	if err := lockSession(opts.dataDir); err != nil {
		t.Fatalf("lock session: %v", err)
	}

	withFolderOwnerExit(t, func() {})
	err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("expected locked error, got %v", err)
	}
	_, mounts, _ := fake.counts()
	if mounts != 0 {
		t.Fatalf("Mount calls=%d want 0", mounts)
	}
}

func TestFolderLockRequiresUnlockedSessionBeforeUnmount(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	fake.mounted[filepath.Join(opts.path, "private")] = true
	if err := lockSession(opts.dataDir); err != nil {
		t.Fatalf("lock session: %v", err)
	}

	err := runFolder(opts, []string{folderLock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("expected locked error, got %v", err)
	}
	_, _, locks := fake.counts()
	if locks != 0 {
		t.Fatalf("Unmount calls=%d want 0", locks)
	}
}

func TestFolderUnlockKeepsMountAfterKinkoLockUntilOwnerExit(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	releaseOwner := make(chan struct{})
	withFolderOwnerExit(t, func() {
		<-releaseOwner
	})

	var out bytes.Buffer
	unlockErr := make(chan error, 1)
	go func() {
		unlockErr <- runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &out, &bytes.Buffer{})
	}()

	mountpoint := filepath.Join(opts.path, "private")
	waitUntil(t, func() bool {
		return fake.isMounted(mountpoint)
	})
	if err := lockSession(opts.dataDir); err != nil {
		t.Fatalf("lock session: %v", err)
	}
	_, _, locks := fake.counts()
	if locks != 0 {
		t.Fatalf("kinko lock unmounted folder before owner exit, Unmount calls=%d want 0", locks)
	}
	if !fake.isMounted(mountpoint) {
		t.Fatal("folder should remain mounted after kinko lock while owner is running")
	}

	close(releaseOwner)
	if err := <-unlockErr; err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}
	_, _, locks = fake.counts()
	if locks != 1 {
		t.Fatalf("owner exit Unmount calls=%d want 1", locks)
	}
	if fake.isMounted(mountpoint) {
		t.Fatal("folder should be unmounted after owner exit")
	}
}

func TestFolderUnlockRejectsNonEmptyMountpoint(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	mountpoint := filepath.Join(opts.path, "private")
	if err := os.MkdirAll(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	withFolderOwnerExit(t, func() {})
	err := runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected non-empty mountpoint error, got %v", err)
	}
	_, mounts, _ := fake.counts()
	if mounts != 0 {
		t.Fatalf("Mount calls=%d want 0", mounts)
	}
}

func TestFolderCobraWiring(t *testing.T) {
	withFakeFolderBackend(t)
	withFolderOwnerExit(t, func() {})
	opts := setupUnlockedFolderTest(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}

	var addOut bytes.Buffer
	if err := Run(append(base, "folder", "add", "private"), strings.NewReader(""), &addOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}
	if got := addOut.String(); got != "folder added: private\n" {
		t.Fatalf("unexpected add output: %q", got)
	}

	var unlockOut bytes.Buffer
	if err := Run(append(base, "folder", "unlock", "private"), strings.NewReader(""), &unlockOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder unlock failed: %v", err)
	}
	if !strings.Contains(unlockOut.String(), "folder unlocked: private") {
		t.Fatalf("unexpected unlock output: %q", unlockOut.String())
	}
}
