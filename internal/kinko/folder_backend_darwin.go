//go:build darwin

package kinko

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type hdiutilFolderBackend struct {
	dataDir string
}

func newDefaultFolderBackend(dataDir string) FolderBackend {
	return hdiutilFolderBackend{dataDir: dataDir}
}

func (b hdiutilFolderBackend) Ensure(ctx context.Context, record FolderRecord, secret string) error {
	if err := ensureFolderStorageDirectory(b.dataDir, record); err != nil {
		return err
	}
	imagePath, err := b.imagePath(record)
	if err != nil {
		return err
	}
	exists, err := hdiutilImagePathExists(imagePath)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
		return err
	}
	args := []string{
		"create",
		"-type", "SPARSEBUNDLE",
		"-fs", "APFS",
		"-size", "1g",
		"-volname", record.Name,
		"-encryption", "AES-256",
		"-stdinpass",
		imagePath,
	}
	return runFolderBackendCommand(ctx, secret+"\n", "hdiutil", args...)
}

func (b hdiutilFolderBackend) Mount(ctx context.Context, record FolderRecord, secret string, mountpoint string) error {
	storageExists, err := folderStorageExists(b.dataDir, record)
	if err != nil {
		return err
	}
	if !storageExists {
		dir, err := checkedFolderStorageDir(b.dataDir, record)
		if err != nil {
			return err
		}
		return fmt.Errorf("folder storage path does not exist: %s", dir)
	}
	imagePath, err := b.imagePath(record)
	if err != nil {
		return err
	}
	exists, err := hdiutilImagePathExists(imagePath)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("folder image path does not exist: %s", imagePath)
	}
	args := []string{
		"attach",
		imagePath,
		"-stdinpass",
		"-mountpoint", mountpoint,
		"-nobrowse",
	}
	return runFolderBackendCommand(ctx, secret+"\n", "hdiutil", args...)
}

func (b hdiutilFolderBackend) Unmount(ctx context.Context, _ FolderRecord, mountpoint string) error {
	return runFolderBackendCommand(ctx, "", "hdiutil", "detach", mountpoint)
}

func (b hdiutilFolderBackend) Status(ctx context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	out, err := outputFolderBackendCommand(ctx, "hdiutil", "info")
	if err != nil {
		return FolderMountStatus{}, err
	}
	if parseHdiutilInfoMounted(out, mountpoint) {
		return FolderMountStatus{Mounted: true, Detail: "mounted"}, nil
	}
	return FolderMountStatus{Mounted: false, Detail: "not mounted"}, nil
}

func (b hdiutilFolderBackend) imagePath(record FolderRecord) (string, error) {
	dir, err := checkedFolderStorageDir(b.dataDir, record)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "macos.sparsebundle"), nil
}

func hdiutilImagePathExists(imagePath string) (bool, error) {
	info, err := os.Lstat(imagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat folder image path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("folder image path must not be a symlink: %s", imagePath)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("folder image path exists and is not a directory: %s", imagePath)
	}
	return true, nil
}

func parseHdiutilInfoMounted(info []byte, mountpoint string) bool {
	target := filepath.Clean(mountpoint)
	scanner := bufio.NewScanner(bytes.NewReader(info))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if cleanHdiutilMountpointCandidate(line) == target {
			return true
		}
		if _, candidate, ok := strings.Cut(line, ":"); ok {
			if cleanHdiutilMountpointCandidate(candidate) == target {
				return true
			}
		}
		for _, sep := range []string{"\t", " "} {
			if strings.HasSuffix(line, sep+mountpoint) {
				return true
			}
		}
	}
	return false
}

func cleanHdiutilMountpointCandidate(candidate string) string {
	return filepath.Clean(strings.TrimSpace(candidate))
}
