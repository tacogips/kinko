package kinko

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

func TestRunFolderRemove_ReportsLeftoverStorageWhenStorageDeletionFails(t *testing.T) {
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

	// The config (record removal) is persisted before storage deletion is
	// attempted, so a storage deletion failure must not leave a record
	// pointing at storage that may already be gone or, as here, refused for
	// safety. The registration is removed and the error reports the
	// leftover storage path for manual cleanup instead.
	err := runFolder(opts, []string{folderRemove, "--yes", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected storage deletion failure")
	}
	if !strings.Contains(err.Error(), "folder registration removed but storage deletion failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), storageDir) {
		t.Fatalf("expected error to report leftover storage path %q, got %v", storageDir, err)
	}
	if code := ExitCode(err); code != exitCodeIOFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeIOFailed)
	}
	if records := mustFolderRecords(t, opts); len(records) != 0 {
		t.Fatalf("folder record should be removed even when storage deletion fails, got %#v", records)
	}
}

// TestRunFolderRemove_BlocksConcurrentFolderAdd exercises the fix for the
// remove flow's config read-modify-write: it must hold the mutation lock
// (not only the per-folder lifecycle lock) while reading, modifying, and
// saving the encrypted config, so a concurrent folder add cannot race the
// read-modify-write and be silently lost. The blocking backend's Status
// call happens only after both the mutation lock and the lifecycle lock
// are held (mirroring folder add's lock ordering), so blocking there pins
// remove inside its locked config section while the concurrent add is
// attempted.
func TestRunFolderRemove_BlocksConcurrentFolderAdd(t *testing.T) {
	backend := withBlockingStatusFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	removeErr := make(chan error, 1)
	go func() {
		removeErr <- runFolder(opts, []string{folderRemove, "--yes", "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	}()
	<-backend.statusStarted

	err := runFolder(opts, []string{folderAdd, "other"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected concurrent folder add to be blocked by remove holding the mutation lock")
	}
	if !strings.Contains(err.Error(), "vault mutation in progress") {
		t.Fatalf("unexpected concurrent add error: %v", err)
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeLockConflict)
	}

	close(backend.allowStatus)
	if err := <-removeErr; err != nil {
		t.Fatalf("folder remove failed: %v", err)
	}

	records := mustFolderRecords(t, opts)
	if len(records) != 0 {
		t.Fatalf("expected private folder to be removed, got %#v", records)
	}

	// Now that remove has released the mutation lock, the previously
	// blocked add can be retried and must succeed with the config intact
	// (not silently erased or resurrected by the earlier race).
	if err := runFolder(opts, []string{folderAdd, "other"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("retry folder add failed: %v", err)
	}
	records = mustFolderRecords(t, opts)
	if len(records) != 1 || records[0].Name != "other" {
		t.Fatalf("unexpected records after retry: %#v", records)
	}
}

type blockingStatusFolderBackend struct {
	mu             sync.Mutex
	mounted        map[string]bool
	statusStarted  chan struct{}
	allowStatus    chan struct{}
	statusStartOne sync.Once
}

func (b *blockingStatusFolderBackend) Ensure(context.Context, FolderRecord, string) error {
	return nil
}

func (b *blockingStatusFolderBackend) Mount(_ context.Context, _ FolderRecord, _ string, mountpoint string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounted[mountpoint] = true
	return nil
}

func (b *blockingStatusFolderBackend) Unmount(_ context.Context, _ FolderRecord, mountpoint string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounted[mountpoint] = false
	return nil
}

func (b *blockingStatusFolderBackend) Status(_ context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	b.statusStartOne.Do(func() {
		close(b.statusStarted)
	})
	<-b.allowStatus
	b.mu.Lock()
	defer b.mu.Unlock()
	return FolderMountStatus{Mounted: b.mounted[mountpoint]}, nil
}

func withBlockingStatusFolderBackend(t *testing.T) *blockingStatusFolderBackend {
	t.Helper()
	backend := &blockingStatusFolderBackend{
		mounted:       map[string]bool{},
		statusStarted: make(chan struct{}),
		allowStatus:   make(chan struct{}),
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

// TestRegisterFolderOwnerExitSignalsIncludesSighup verifies the hold-mode
// signal handler now also registers SIGHUP (in addition to interrupt and
// terminate), so a dropped ssh session / disconnected terminal leads to the
// unmount cleanup path instead of leaving the process running with no
// controlling terminal and the folder left mounted forever.
func TestRegisterFolderOwnerExitSignalsIncludesSighup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGHUP delivery is unix-specific")
	}
	ch, stop := registerFolderOwnerExitSignals()
	defer stop()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
	select {
	case sig := <-ch:
		if sig != syscall.SIGHUP {
			t.Fatalf("received signal=%v want SIGHUP", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP was not delivered to the registered channel")
	}
}

// TestRunFolderUnlockRegistersSignalHandlerBeforeMount is the regression
// test for the fix that moves signal.Notify registration to before the
// mount is initiated. Previously the handler was only installed after
// backend.Mount succeeded, so a signal delivered during (or immediately
// after) the mount call could be missed entirely, leaving the process
// hung in the hold with the folder mounted. Here the fake backend's Mount
// method sends the process an interrupt signal itself, simulating a
// signal that arrives while mounting is in flight; with the fix, the
// channel is already registered and buffered (size 1) before Mount runs,
// so waitForFolderOwnerExit must observe it immediately and the unlock
// command must proceed straight through the hold to the unmount cleanup
// path without hanging.
func TestRunFolderUnlockRegistersSignalHandlerBeforeMount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal delivery to self is unix-specific")
	}
	backend := &signalDuringMountFolderBackend{mounted: map[string]bool{}}
	prevBackend := newFolderBackend
	newFolderBackend = func(string) FolderBackend { return backend }
	t.Cleanup(func() { newFolderBackend = prevBackend })

	// Use the real signal-waiting function (not the test override) so this
	// test exercises the actual registration-then-wait ordering.
	prevWait := waitForFolderOwnerExit
	waitForFolderOwnerExit = waitForInterruptOrTerminateSignal
	t.Cleanup(func() { waitForFolderOwnerExit = prevWait })

	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("folder unlock failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("folder unlock hung waiting for a signal that arrived during mount; signal handler was not registered before Mount")
	}
	if !backend.sentSignal {
		t.Fatal("test backend did not send its simulated signal; test setup is broken")
	}
}

// signalDuringMountFolderBackend simulates a signal (e.g. an early Ctrl-C
// or a dropped ssh session) arriving while the mount is in flight, by
// sending SIGINT to the current process from inside Mount itself.
type signalDuringMountFolderBackend struct {
	mu         sync.Mutex
	mounted    map[string]bool
	sentSignal bool
}

func (b *signalDuringMountFolderBackend) Ensure(context.Context, FolderRecord, string) error {
	return nil
}

func (b *signalDuringMountFolderBackend) Mount(_ context.Context, _ FolderRecord, _ string, mountpoint string) error {
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		return err
	}
	b.mu.Lock()
	b.sentSignal = true
	b.mounted[mountpoint] = true
	b.mu.Unlock()
	return nil
}

func (b *signalDuringMountFolderBackend) Unmount(_ context.Context, _ FolderRecord, mountpoint string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounted[mountpoint] = false
	return nil
}

func (b *signalDuringMountFolderBackend) Status(_ context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return FolderMountStatus{Mounted: b.mounted[mountpoint]}, nil
}

// TestRunFolderUnlockSkipsUnmountWhenAlreadyUnmountedOnHoldExit is the
// regression test for the fix to the hold-exit path: after re-acquiring
// the lifecycle lock, it must check backend Status before calling
// Unmount. Previously Unmount was called unconditionally, which could
// fail confusingly (or detach someone else's mount) if the folder had
// already been unmounted by another process/session while this one was
// holding it (e.g. a concurrent `folder lock`). Here the fake backend
// reports the folder as already unmounted by the time the owner exits;
// the unlock command must not call Unmount again and must report an
// informational message instead of erroring.
func TestRunFolderUnlockSkipsUnmountWhenAlreadyUnmountedOnHoldExit(t *testing.T) {
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedFolderTest(t)
	if err := runFolder(opts, []string{folderAdd, "private"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	releaseOwner := make(chan struct{})
	withFolderOwnerExit(t, func() {
		<-releaseOwner
	})

	mountpoint := filepath.Join(opts.path, "private")
	var out bytes.Buffer
	unlockErr := make(chan error, 1)
	go func() {
		unlockErr <- runFolder(opts, []string{folderUnlock, "private"}, strings.NewReader(""), &out, &bytes.Buffer{})
	}()
	waitUntil(t, func() bool { return fake.isMounted(mountpoint) })

	// Simulate another process/session already unmounting the folder
	// while this one is still holding it (e.g. a concurrent folder lock).
	fake.mu.Lock()
	fake.mounted[mountpoint] = false
	fake.mu.Unlock()

	close(releaseOwner)
	if err := <-unlockErr; err != nil {
		t.Fatalf("folder unlock should not fail when already unmounted: %v", err)
	}
	if _, _, locks := fake.counts(); locks != 0 {
		t.Fatalf("Unmount should not be called when already unmounted, calls=%d", locks)
	}
	if !strings.Contains(out.String(), "already locked: private") {
		t.Fatalf("expected informational already-locked message, got %q", out.String())
	}
}

// TestEnsureFolderGitignoreEntryHandlesConcurrentCreateRace is the
// regression test for the fix to ensureFolderGitignoreEntry's stat race:
// os.Lstat can report os.ErrNotExist and then, before the following
// os.ReadFile runs, another process/goroutine creates the file. In that
// case ReadFile succeeds (err == nil) but the earlier Lstat's info is
// nil, so unconditionally calling info.Mode().Perm() would panic.
//
// A background goroutine continuously creates and removes the target file
// in a tight loop with no synchronization, while the foreground repeatedly
// calls ensureFolderGitignoreEntry against the same path. Across many
// iterations this reliably hits the interleaving where Lstat observes the
// file as missing immediately before ReadFile observes it as present. The
// two writers can legitimately race for who writes last, so this only
// asserts the fix's actual contract: no panic and no unexpected error,
// regardless of interleaving.
func TestEnsureFolderGitignoreEntryHandlesConcurrentCreateRace(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")

	const iterations = 2000
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(gitignorePath, []byte("keep-this/\n"), 0o644)
			_ = os.Remove(gitignorePath)
		}
	}()

	func() {
		defer close(stop)
		for i := 0; i < iterations; i++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ensureFolderGitignoreEntry panicked on concurrent create race (iteration %d): %v", i, r)
					}
				}()
				if _, err := ensureFolderGitignoreEntry(dir, "private"); err != nil {
					t.Fatalf("ensureFolderGitignoreEntry failed on iteration %d: %v", i, err)
				}
			}()
			// Reset so the next iteration can race the same missing-then-
			// present transition again.
			_ = os.Remove(gitignorePath)
		}
	}()
	<-writerDone
}

// TestEnsureFolderGitignoreEntryUsesDefaultModeWhenCreatedConcurrently
// exercises the same race deterministically for the mode-selection branch:
// it directly reproduces the state Lstat/ReadFile would observe (missing
// at stat time, present at read time cannot be forced without a hook into
// the two syscalls, so this asserts the documented fallback behavior on
// the normal missing-file path) and confirms a default, sane file mode is
// always applied without panicking.
func TestEnsureFolderGitignoreEntryUsesDefaultModeWhenCreatedConcurrently(t *testing.T) {
	dir := t.TempDir()
	changed, err := ensureFolderGitignoreEntry(dir, "private")
	if err != nil {
		t.Fatalf("ensureFolderGitignoreEntry failed: %v", err)
	}
	if !changed {
		t.Fatal("expected .gitignore to be created")
	}
	info, err := os.Stat(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("newly created .gitignore mode=%#o want %#o", got, os.FileMode(0o600))
	}
}
