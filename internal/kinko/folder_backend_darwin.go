//go:build darwin

package kinko

import (
	"bufio"
	"bytes"
	"context"
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
	imagePath := b.imagePath(record)
	if _, err := os.Stat(imagePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
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
	args := []string{
		"attach",
		b.imagePath(record),
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

func (b hdiutilFolderBackend) imagePath(record FolderRecord) string {
	return filepath.Join(folderStorageDir(b.dataDir, record), "macos.sparsebundle")
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
