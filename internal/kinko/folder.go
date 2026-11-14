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
	_ = stdin
	_ = stderr
	if len(args) == 0 {
		return errors.New("folder requires subcommand: add|unlock|lock|status|path")
	}
	switch args[0] {
	case folderAdd:
		return runFolderAdd(opts, args[1:], stdout)
	case folderUnlock:
		return runFolderUnlock(opts, args[1:], stdout)
	case folderLock:
		return runFolderLock(opts, args[1:], stdout)
	case folderStatus:
		return runFolderStatus(opts, args[1:], stdout)
	case folderPath:
		return runFolderPath(opts, args[1:], stdout)
	default:
		return fmt.Errorf("unknown folder subcommand %q", args[0])
	}
}

func runFolderAdd(opts globalOptions, args []string, stdout io.Writer) error {
	name, err := parseFolderNameArg(folderAdd, args)
	if err != nil {
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
	hold := false
	fs.BoolVar(&hold, "hold", false, "accepted for compatibility; unlock already holds the mount in the foreground")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := parseFolderNameArg(folderUnlock, fs.Args())
	if err != nil {
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
	backend := newFolderBackend(opts.dataDir)
	mountpoint := folderMountpoint(record)
	status, err := backend.Status(context.Background(), record, mountpoint)
	if err != nil {
		return err
	}
	if status.Mounted {
		return fmt.Errorf("folder already mounted: %s", record.Name)
	}
	created, err := prepareFolderMountpoint(mountpoint)
	if err != nil {
		return err
	}
	secret := deriveFolderSecret(dek, record)
	if err := backend.Ensure(context.Background(), record, secret); err != nil {
		cleanupCreatedMountpoint(mountpoint, created)
		return err
	}
	if err := backend.Mount(context.Background(), record, secret, mountpoint); err != nil {
		cleanupCreatedMountpoint(mountpoint, created)
		return err
	}
	_, _ = fmt.Fprintf(stdout, "folder unlocked: %s\npath: %s\n", record.Name, mountpoint)
	_, _ = fmt.Fprintf(stdout, "holding folder unlock; send interrupt or terminate to lock: %s\n", record.Name)
	waitForFolderOwnerExit()
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
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	record, err := requireFolderRecord(records, opts, name)
	if err != nil {
		return err
	}
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

func runFolderStatus(opts globalOptions, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("folder status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("folder status accepts at most one folder name")
	}
	_, _, records, err := loadFolderConfig(opts)
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		name := fs.Arg(0)
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
	dir := folderStorageDir(dataDir, record)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat folder storage: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("folder storage path exists and is not a directory: %s", dir)
	}
	return true, nil
}

func cleanupFolderStorage(dataDir string, record FolderRecord) {
	_ = os.RemoveAll(folderStorageDir(dataDir, record))
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
		mode = info.Mode().Perm()
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

func waitForInterruptOrTerminateSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(ch)
	<-ch
}
