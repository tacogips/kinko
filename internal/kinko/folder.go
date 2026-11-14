package kinko

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var waitForFolderOwnerExit = waitForInterruptOrTerminateSignal
var saveFolderConfig = saveConfig

func runFolder(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("folder requires subcommand: add|unlock|lock|remove|status|path")
	}
	switch args[0] {
	case folderAdd:
		return folderCommandError(runFolderAdd(opts, args[1:], stdout))
	case folderUnlock:
		return folderCommandError(runFolderUnlock(opts, args[1:], stdout))
	case folderLock:
		return folderCommandError(runFolderLock(opts, args[1:], stdout))
	case folderRemove:
		return folderCommandError(runFolderRemove(opts, args[1:], stdin, stdout, stderr))
	case folderStatus:
		return folderCommandError(runFolderStatus(opts, args[1:], stdout))
	case folderPath:
		return folderCommandError(runFolderPath(opts, args[1:], stdout))
	default:
		return folderCommandError(fmt.Errorf("unknown folder subcommand %q", args[0]))
	}
}

func folderCommandError(err error) error {
	if err == nil {
		return nil
	}
	var cliErr *cliError
	if errors.As(err, &cliErr) {
		return err
	}
	return newCLIError(folderErrorExitCode(err), err.Error(), err)
}

func folderErrorExitCode(err error) int {
	msg := err.Error()
	if strings.Contains(msg, "vault mutation in progress") || strings.Contains(msg, "folder lifecycle in progress") {
		return exitCodeLockConflict
	}
	if isFolderPolicyErrorMessage(msg) {
		return exitCodePolicyFailed
	}
	return exitCodeIOFailed
}

func isFolderPolicyErrorMessage(msg string) bool {
	policyFragments := []string{
		"requires",
		"unknown folder subcommand",
		"invalid folder",
		"folder already registered",
		"folder not registered",
		"folder already mounted",
		"folder is mounted",
		"folder is not mounted",
		"mountpoint",
	}
	for _, fragment := range policyFragments {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

func runFolderAdd(opts globalOptions, args []string, stdout io.Writer) error {
	name, err := parseFolderNameArg(folderAdd, args)
	if err != nil {
		return err
	}
	return runFolderAddWithOptions(opts, folderNameOptions{name: name}, stdout)
}

type folderNameOptions struct {
	name string
}

type folderUnlockOptions struct {
	name string
	hold bool
}

type folderRemoveOptions struct {
	name        string
	keepStorage bool
	yes         bool
}

type folderStatusOptions struct {
	name string
}

func validateFolderOptionName(command string, name string) error {
	if name == "" {
		return fmt.Errorf("folder %s requires <name>", command)
	}
	return validateFolderName(name)
}

func runFolderAddWithOptions(opts globalOptions, folderOpts folderNameOptions, stdout io.Writer) error {
	name := folderOpts.name
	if err := validateFolderOptionName(folderAdd, name); err != nil {
		return err
	}
	if err := validateProjectPathForFolder(opts.path); err != nil {
		return err
	}
	mountpoint := filepath.Join(opts.path, name)
	if err := validateFolderMountpointForAdd(mountpoint); err != nil {
		return err
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return fmt.Errorf("vault mutation in progress: %w", err)
	}
	defer release()

	dek, cfg, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	if _, exists := findFolderRecord(records, opts.profile, opts.path, name); exists {
		return fmt.Errorf("folder already registered: %s", name)
	}
	record, err := newFolderRecord(opts.profile, opts.path, name, folderBackendName(), time.Now())
	if err != nil {
		return err
	}
	gitignoreSnapshot, err := snapshotFolderGitignore(opts.path)
	if err != nil {
		return err
	}
	storageExisted, err := folderStorageExists(opts.dataDir, record)
	if err != nil {
		return err
	}
	storageFinalized := false
	if !storageExisted {
		defer func() {
			if !storageFinalized {
				cleanupFolderStorage(opts.dataDir, record)
			}
		}()
	}
	secret := deriveFolderSecret(dek, record)
	backend := newFolderBackend(opts.dataDir)
	if err := ensureFolderStorageDirectory(opts.dataDir, record); err != nil {
		return err
	}
	if err := backend.Ensure(context.Background(), record, secret); err != nil {
		return err
	}
	if err := ensureFolderStorageMetadata(opts.dataDir, record); err != nil {
		return err
	}
	gitignoreChanged, err := ensureFolderGitignoreEntry(opts.path, name)
	if err != nil {
		return err
	}
	records = append(records, record)
	if err := saveFolderRecordsToConfig(cfg, records); err != nil {
		return rollbackFolderGitignoreOnError(gitignoreSnapshot, gitignoreChanged, err)
	}
	if err := saveFolderConfig(opts.dataDir, dek, cfg); err != nil {
		return rollbackFolderGitignoreOnError(gitignoreSnapshot, gitignoreChanged, fmt.Errorf("write encrypted folder config: %w", err))
	}
	storageFinalized = true
	_, _ = fmt.Fprintf(stdout, "folder added: %s\n", name)
	return nil
}

func runFolderUnlock(opts globalOptions, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("folder unlock", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var folderOpts folderUnlockOptions
	fs.BoolVar(&folderOpts.hold, "hold", false, "accepted for compatibility; unlock already holds the mount in the foreground")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := parseFolderNameArg(folderUnlock, fs.Args())
	if err != nil {
		return err
	}
	folderOpts.name = name
	return runFolderUnlockWithOptions(opts, folderOpts, stdout)
}

func runFolderUnlockWithOptions(opts globalOptions, folderOpts folderUnlockOptions, stdout io.Writer) error {
	name := folderOpts.name
	if err := validateFolderOptionName(folderUnlock, name); err != nil {
		return err
	}
	dek, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}
	release, err := acquireFolderLifecycleLock(opts.dataDir, record)
	if err != nil {
		return fmt.Errorf("folder lifecycle in progress: %w", err)
	}
	mountpoint := folderMountpoint(record)
	storageExists, err := folderStorageExists(opts.dataDir, record)
	if err != nil {
		release()
		return err
	}
	if !storageExists {
		release()
		dir, err := checkedFolderStorageDir(opts.dataDir, record)
		if err != nil {
			return err
		}
		return fmt.Errorf("folder storage path does not exist: %s", dir)
	}
	backend := newFolderBackend(opts.dataDir)
	status, err := backend.Status(context.Background(), record, mountpoint)
	if err != nil {
		release()
		return err
	}
	if status.Mounted {
		release()
		return fmt.Errorf("folder already mounted: %s", record.Name)
	}
	created, err := prepareFolderMountpoint(mountpoint)
	if err != nil {
		release()
		return err
	}
	// Register the hold-mode exit signal handler before mounting so a signal
	// delivered during or immediately after Mount is never missed; a signal
	// registered only after Mount succeeds could be lost to a dropped ssh
	// session or an early interrupt, leaving the folder mounted forever.
	sigCh, stopSignals := registerFolderOwnerExitSignals()
	defer stopSignals()
	secret := deriveFolderSecret(dek, record)
	if err := backend.Mount(context.Background(), record, secret, mountpoint); err != nil {
		release()
		cleanupCreatedMountpoint(mountpoint, created)
		return err
	}
	release()
	_, _ = fmt.Fprintf(stdout, "folder unlocked: %s\npath: %s\n", record.Name, mountpoint)
	_, _ = fmt.Fprintf(stdout, "holding folder unlock; send interrupt or terminate to lock: %s\n", record.Name)
	waitForFolderOwnerExit(sigCh)
	release, err = acquireFolderLifecycleLock(opts.dataDir, record)
	if err != nil {
		return fmt.Errorf("folder lifecycle in progress: %w", err)
	}
	defer release()
	status, err = backend.Status(context.Background(), record, mountpoint)
	if err != nil {
		return err
	}
	if !status.Mounted {
		cleanupCreatedMountpoint(mountpoint, created)
		_, _ = fmt.Fprintf(stdout, "folder already locked: %s\n", record.Name)
		return nil
	}
	if err := backend.Unmount(context.Background(), record, mountpoint); err != nil {
		return folderUnmountError(record.Name, err)
	}
	cleanupCreatedMountpoint(mountpoint, created)
	_, _ = fmt.Fprintf(stdout, "folder locked: %s\n", record.Name)
	return nil
}

func runFolderLock(opts globalOptions, args []string, stdout io.Writer) error {
	name, err := parseFolderNameArg(folderLock, args)
	if err != nil {
		return err
	}
	return runFolderLockWithOptions(opts, folderNameOptions{name: name}, stdout)
}

func runFolderLockWithOptions(opts globalOptions, folderOpts folderNameOptions, stdout io.Writer) error {
	name := folderOpts.name
	if err := validateFolderOptionName(folderLock, name); err != nil {
		return err
	}
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}
	release, err := acquireFolderLifecycleLock(opts.dataDir, record)
	if err != nil {
		return fmt.Errorf("folder lifecycle in progress: %w", err)
	}
	defer release()
	backend := newFolderBackend(opts.dataDir)
	mountpoint := folderMountpoint(record)
	status, err := backend.Status(context.Background(), record, mountpoint)
	if err != nil {
		return err
	}
	if status.Mounted {
		if err := backend.Unmount(context.Background(), record, mountpoint); err != nil {
			return folderUnmountError(record.Name, err)
		}
	}
	_, _ = fmt.Fprintf(stdout, "folder locked: %s\n", record.Name)
	return nil
}

func runFolderRemove(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("folder remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var folderOpts folderRemoveOptions
	fs.BoolVar(&folderOpts.keepStorage, "keep-storage", false, "remove folder registration without deleting encrypted folder storage")
	fs.BoolVar(&folderOpts.yes, "yes", false, "delete encrypted folder storage without prompting")
	fs.BoolVar(&folderOpts.yes, "y", false, "delete encrypted folder storage without prompting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := parseFolderNameArg(folderRemove, fs.Args())
	if err != nil {
		return err
	}
	folderOpts.name = name
	return runFolderRemoveWithOptions(opts, folderOpts, stdin, stdout, stderr)
}

func runFolderRemoveWithOptions(opts globalOptions, folderOpts folderRemoveOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	name := folderOpts.name
	if err := validateFolderOptionName(folderRemove, name); err != nil {
		return err
	}
	// A first, unlocked lookup only resolves the folder's identity (its
	// FolderID never changes for a given profile/path/name) so the
	// lifecycle lock path below can be computed. The authoritative config
	// read happens after both locks are held; see the reload below.
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}

	// Acquire the mutation lock before the lifecycle lock, matching the
	// lock ordering used by folder add, to avoid deadlocking against a
	// concurrent add/config-set that also takes the mutation lock first.
	releaseMutation, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return fmt.Errorf("vault mutation in progress: %w", err)
	}
	defer releaseMutation()
	releaseLifecycle, err := acquireFolderLifecycleLock(opts.dataDir, record)
	if err != nil {
		return fmt.Errorf("folder lifecycle in progress: %w", err)
	}
	defer releaseLifecycle()

	// Re-load the config now that both locks are held. Loading before the
	// locks were acquired would let a concurrent folder add / config set
	// race with this read-modify-write and be silently lost.
	dek, cfg, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err = requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}

	backend := newFolderBackend(opts.dataDir)
	status, err := backend.Status(context.Background(), record, folderMountpoint(record))
	if err != nil {
		return err
	}
	if status.Mounted {
		return fmt.Errorf("folder is mounted: %s", record.Name)
	}
	if !folderOpts.keepStorage && !folderOpts.yes {
		ok, err := confirmPrompt(stdin, stderr, fmt.Sprintf("Remove folder %q and delete encrypted storage? [y/N]: ", record.Name))
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return nil
		}
	}

	// Persist the config change (record removed) before deleting storage.
	// If storage deletion were attempted first and then the config save
	// failed, the record would be left pointing at deleted storage. Saving
	// the config first means a subsequent storage-deletion failure only
	// leaves an orphaned, already-unregistered storage directory, which is
	// reported below for manual cleanup.
	next := removeFolderRecord(records, record)
	if err := saveFolderRecordsToConfig(cfg, next); err != nil {
		return err
	}
	if err := saveFolderConfig(opts.dataDir, dek, cfg); err != nil {
		return fmt.Errorf("write encrypted folder config: %w", err)
	}
	if !folderOpts.keepStorage {
		if err := removeFolderStorage(opts.dataDir, record); err != nil {
			dir, dirErr := checkedFolderStorageDir(opts.dataDir, record)
			if dirErr != nil {
				dir = ""
			}
			if dir != "" {
				return fmt.Errorf("folder registration removed but storage deletion failed; remove leftover storage manually at %s: %w", dir, err)
			}
			return fmt.Errorf("folder registration removed but storage deletion failed; remove leftover storage manually: %w", err)
		}
	}
	_, _ = fmt.Fprintf(stdout, "folder removed: %s\n", record.Name)
	return nil
}

func runFolderStatus(opts globalOptions, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("folder status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("folder status accepts at most one folder name")
	}
	folderOpts := folderStatusOptions{}
	if fs.NArg() == 1 {
		folderOpts.name = fs.Arg(0)
	}
	return runFolderStatusWithOptions(opts, folderOpts, stdout)
}

func runFolderStatusWithOptions(opts globalOptions, folderOpts folderStatusOptions, stdout io.Writer) error {
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	if folderOpts.name != "" {
		name := folderOpts.name
		if err := validateFolderName(name); err != nil {
			return err
		}
		record, err := requireFolderRecord(records, opts, name)
		if err != nil {
			return err
		}
		return printFolderStatus(context.Background(), newFolderBackend(opts.dataDir), stdout, record)
	}
	scoped := folderRecordsForScope(records, opts.profile, opts.path)
	if len(scoped) == 0 {
		_, _ = fmt.Fprintln(stdout, "no folders configured")
		return nil
	}
	backend := newFolderBackend(opts.dataDir)
	for _, record := range scoped {
		if err := printFolderStatus(context.Background(), backend, stdout, record); err != nil {
			return err
		}
	}
	return nil
}

func runFolderPath(opts globalOptions, args []string, stdout io.Writer) error {
	name, err := parseFolderNameArg(folderPath, args)
	if err != nil {
		return err
	}
	return runFolderPathWithOptions(opts, folderNameOptions{name: name}, stdout)
}

func runFolderPathWithOptions(opts globalOptions, folderOpts folderNameOptions, stdout io.Writer) error {
	name := folderOpts.name
	if err := validateFolderOptionName(folderPath, name); err != nil {
		return err
	}
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}
	mountpoint := folderMountpoint(record)
	status, err := newFolderBackend(opts.dataDir).Status(context.Background(), record, mountpoint)
	if err != nil {
		return err
	}
	if !status.Mounted {
		return fmt.Errorf("folder is not mounted: %s", name)
	}
	_, _ = fmt.Fprintln(stdout, mountpoint)
	return nil
}

func parseFolderNameArg(command string, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("folder %s requires <name>", command)
	}
	if err := validateFolderName(args[0]); err != nil {
		return "", err
	}
	return args[0], nil
}

func removeFolderRecord(records []FolderRecord, target FolderRecord) []FolderRecord {
	next := make([]FolderRecord, 0, len(records))
	for _, record := range records {
		if record.Profile == target.Profile && filepath.Clean(record.Path) == filepath.Clean(target.Path) && record.Name == target.Name {
			continue
		}
		next = append(next, record)
	}
	return next
}

func loadFolderConfig(opts globalOptions) ([]byte, map[string]string, []FolderRecord, error) {
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, err := loadConfig(opts.dataDir, dek)
	if err != nil {
		return nil, nil, nil, err
	}
	records, err := loadFolderRecordsFromConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return dek, cfg, records, nil
}

func requireFolderRecord(records []FolderRecord, opts globalOptions, name string) (FolderRecord, error) {
	record, ok := findFolderRecord(records, opts.profile, opts.path, name)
	if !ok {
		return FolderRecord{}, fmt.Errorf("folder not registered: %s", name)
	}
	return record, nil
}

func printFolderStatus(ctx context.Context, backend FolderBackend, stdout io.Writer, record FolderRecord) error {
	status, err := backend.Status(ctx, record, folderMountpoint(record))
	if err != nil {
		return err
	}
	state := "locked"
	if status.Mounted {
		state = "mounted"
	}
	if strings.TrimSpace(status.Detail) != "" {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.Name, state, status.Detail)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\n", record.Name, state)
	return nil
}

func deriveFolderSecret(dek []byte, record FolderRecord) string {
	mac := hmac.New(sha256.New, dek)
	_, _ = mac.Write([]byte(strings.Join([]string{
		"kinko.folder.secret.v1",
		record.Profile,
		filepath.Clean(record.Path),
		record.Name,
		record.FolderID,
	}, "\x00")))
	return base64.RawStdEncoding.EncodeToString(mac.Sum(nil))
}

func validateProjectPathForFolder(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat folder scope path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("folder scope path is not a directory: %s", path)
	}
	return nil
}

func validateFolderMountpointForAdd(mountpoint string) error {
	info, err := os.Lstat(mountpoint)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat folder mountpoint: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("folder mountpoint exists and is not a directory: %s", mountpoint)
	}
	return nil
}

func folderStorageExists(dataDir string, record FolderRecord) (bool, error) {
	if err := validateFolderStorageID(record.FolderID); err != nil {
		return false, err
	}
	rootExists, err := folderStorageRootExists(dataDir)
	if err != nil {
		return false, err
	}
	if !rootExists {
		return false, nil
	}
	dir := folderStorageDir(dataDir, record)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat folder storage: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("folder storage path must not be a symlink: %s", dir)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("folder storage path exists and is not a directory: %s", dir)
	}
	return true, nil
}

func folderStorageRootExists(dataDir string) (bool, error) {
	root := folderStorageRoot(dataDir)
	info, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat folder storage root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("folder storage root must not be a symlink: %s", root)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("folder storage root exists and is not a directory: %s", root)
	}
	return true, nil
}

func ensureFolderStorageRoot(dataDir string) error {
	root := folderStorageRoot(dataDir)
	info, err := os.Lstat(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat folder storage root: %w", err)
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("create folder storage root: %w", err)
		}
		info, err = os.Lstat(root)
		if err != nil {
			return fmt.Errorf("stat folder storage root after create: %w", err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("folder storage root must not be a symlink: %s", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("folder storage root exists and is not a directory: %s", root)
	}
	return nil
}

func ensureFolderStorageDirectory(dataDir string, record FolderRecord) error {
	if err := validateFolderStorageID(record.FolderID); err != nil {
		return err
	}
	if err := ensureFolderStorageRoot(dataDir); err != nil {
		return err
	}
	dir := folderStorageDir(dataDir, record)
	info, err := os.Lstat(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat folder storage: %w", err)
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return fmt.Errorf("create folder storage: %w", err)
		}
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("folder storage path must not be a symlink: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("folder storage path exists and is not a directory: %s", dir)
	}
	return nil
}

func cleanupFolderStorage(dataDir string, record FolderRecord) {
	if ok, err := folderStorageRootExists(dataDir); err != nil || !ok {
		return
	}
	dir, err := checkedFolderStorageDir(dataDir, record)
	if err != nil {
		return
	}
	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return
	}
	_ = os.RemoveAll(dir)
}

func removeFolderStorage(dataDir string, record FolderRecord) error {
	if ok, err := folderStorageRootExists(dataDir); err != nil {
		return err
	} else if !ok {
		return nil
	}
	dir, err := checkedFolderStorageDir(dataDir, record)
	if err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat folder storage: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("folder storage path must not be a symlink: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("folder storage path exists and is not a directory: %s", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove folder storage: %w", err)
	}
	return nil
}

func acquireFolderLifecycleLock(dataDir string, record FolderRecord) (func(), error) {
	if err := validateFolderStorageID(record.FolderID); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dataDir, "lock", "folder-"+record.FolderID+".lifecycle.lock")
	return acquireMetadataLock(lockPath)
}

func prepareFolderMountpoint(mountpoint string) (bool, error) {
	info, err := os.Lstat(mountpoint)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("stat folder mountpoint: %w", err)
		}
		if err := os.MkdirAll(mountpoint, 0o700); err != nil {
			return false, fmt.Errorf("create folder mountpoint: %w", err)
		}
		return true, nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("folder mountpoint exists and is not a directory: %s", mountpoint)
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return false, fmt.Errorf("read folder mountpoint: %w", err)
	}
	if len(entries) > 0 {
		return false, fmt.Errorf("folder mountpoint is not empty: %s", mountpoint)
	}
	return false, nil
}

func cleanupCreatedMountpoint(mountpoint string, created bool) {
	if created {
		_ = os.Remove(mountpoint)
	}
}

func folderUnmountError(name string, err error) error {
	return fmt.Errorf("folder unmount failed for %s; close files in the folder and retry folder lock: %w", name, err)
}

type folderGitignoreSnapshot struct {
	path    string
	exists  bool
	content []byte
	mode    os.FileMode
}

func snapshotFolderGitignore(projectPath string) (folderGitignoreSnapshot, error) {
	gitignorePath := filepath.Join(projectPath, ".gitignore")
	info, err := os.Lstat(gitignorePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return folderGitignoreSnapshot{path: gitignorePath}, nil
		}
		return folderGitignoreSnapshot{}, fmt.Errorf("stat .gitignore: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return folderGitignoreSnapshot{}, fmt.Errorf(".gitignore must not be a symlink: %s", gitignorePath)
	}
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return folderGitignoreSnapshot{}, fmt.Errorf("read .gitignore: %w", err)
	}
	return folderGitignoreSnapshot{
		path:    gitignorePath,
		exists:  true,
		content: append([]byte(nil), content...),
		mode:    info.Mode().Perm(),
	}, nil
}

func rollbackFolderGitignoreOnError(snapshot folderGitignoreSnapshot, changed bool, original error) error {
	if !changed {
		return original
	}
	if err := restoreFolderGitignore(snapshot); err != nil {
		return fmt.Errorf("%w; additionally failed to restore .gitignore: %v", original, err)
	}
	return original
}

func restoreFolderGitignore(snapshot folderGitignoreSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(snapshot.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.WriteFile(snapshot.path, snapshot.content, snapshot.mode); err != nil {
		return err
	}
	return nil
}

func ensureFolderGitignoreEntry(projectPath, name string) (bool, error) {
	entry := gitignoreFolderEntry(name)
	gitignorePath := filepath.Join(projectPath, ".gitignore")
	info, statErr := os.Lstat(gitignorePath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("stat .gitignore: %w", statErr)
	}
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf(".gitignore must not be a symlink: %s", gitignorePath)
	}
	content, err := os.ReadFile(gitignorePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read .gitignore: %w", err)
	}
	if gitignoreContainsFolderEntry(string(content), name) {
		return false, nil
	}
	mode := os.FileMode(0o600)
	if err == nil {
		if info == nil {
			// The Lstat above reported the file as missing, but the
			// ReadFile that just followed succeeded: the file was created
			// concurrently between the two calls. Re-stat now that we know
			// it exists instead of dereferencing the nil info from the
			// earlier Lstat. If the file has since been removed again by
			// the same concurrent actor, fall back to the default mode;
			// the WriteFile below will simply recreate it.
			restated, statErr := os.Lstat(gitignorePath)
			switch {
			case statErr == nil:
				info = restated
				mode = info.Mode().Perm()
			case errors.Is(statErr, os.ErrNotExist):
				// Fall through with the default mode.
			default:
				return false, fmt.Errorf("stat .gitignore: %w", statErr)
			}
		} else {
			mode = info.Mode().Perm()
		}
	}
	var next strings.Builder
	next.Write(content)
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		next.WriteByte('\n')
	}
	next.WriteString(entry)
	next.WriteByte('\n')
	if err := os.WriteFile(gitignorePath, []byte(next.String()), mode); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

func gitignoreContainsFolderEntry(content, name string) bool {
	entry := gitignoreFolderEntry(name)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if line == entry || line == name || line == name+"/" || line == "/"+name || line == "/"+name+"/" {
			return true
		}
	}
	return false
}

func gitignoreFolderEntry(name string) string {
	var b strings.Builder
	for i, r := range name {
		if (i == 0 && (r == '#' || r == '!')) || strings.ContainsRune(`*?[]`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('/')
	return b.String()
}

// registerFolderOwnerExitSignals installs the hold-mode exit signal handler
// and returns the channel it delivers to along with a function that stops
// the notification. It must be called before initiating the mount so that a
// signal arriving during or immediately after the mount is never missed; a
// handler installed only after a successful mount can leave the folder
// mounted if the process is interrupted (dropped ssh session, early
// Ctrl-C, etc.) before the handler is registered. SIGHUP is included so a
// disconnected terminal also leads to the unmount cleanup path.
func registerFolderOwnerExitSignals() (chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

func waitForInterruptOrTerminateSignal(ch <-chan os.Signal) {
	<-ch
}
