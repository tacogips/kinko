//go:build darwin

package kinko

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const hdiutilPath = "/usr/bin/hdiutil"

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
	return runFolderBackendCommand(ctx, secret+"\n", hdiutilPath, args...)
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
	return runFolderBackendCommand(ctx, secret+"\n", hdiutilPath, args...)
}

func (b hdiutilFolderBackend) Unmount(ctx context.Context, _ FolderRecord, mountpoint string) error {
	return runFolderBackendCommand(ctx, "", hdiutilPath, "detach", mountpoint)
}

func (b hdiutilFolderBackend) Status(ctx context.Context, _ FolderRecord, mountpoint string) (FolderMountStatus, error) {
	out, err := outputFolderBackendCommand(ctx, hdiutilPath, "info", "-plist")
	if err != nil {
		return FolderMountStatus{}, err
	}
	mounted, err := parseHdiutilInfoPlistMounted(out, mountpoint)
	if err != nil {
		return FolderMountStatus{}, err
	}
	if mounted {
		return FolderMountStatus{Mounted: true, Detail: "mounted"}, nil
	}
	return FolderMountStatus{Mounted: false, Detail: "not mounted"}, nil
}

// canonicalizeMountpointForComparison resolves symlinks in path so mount
// detection compares real, canonical filesystem locations rather than
// symlinked aliases. macOS commonly symlinks directories such as /tmp to
// /private/tmp; hdiutil reports the resolved /private/... path while a
// caller-supplied mountpoint may still name the /tmp/... alias (or vice
// versa). Comparing filepath.Clean output alone would treat these as
// different paths and could report a live mount as "locked" (leaving it
// attached) or cause a remove to os.RemoveAll a live-attached sparsebundle.
// If the path does not exist or symlinks cannot be resolved, the cleaned
// path is used as a fallback so an unmounted/nonexistent mountpoint still
// compares sensibly.
func canonicalizeMountpointForComparison(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return cleaned
	}
	return resolved
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

func parseHdiutilInfoPlistMounted(info []byte, mountpoint string) (bool, error) {
	target := canonicalizeMountpointForComparison(mountpoint)
	decoder := xml.NewDecoder(bytes.NewReader(info))
	expectMountPointValue := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("parse hdiutil plist: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var key string
				if err := decoder.DecodeElement(&key, &t); err != nil {
					return false, fmt.Errorf("parse hdiutil plist key: %w", err)
				}
				expectMountPointValue = strings.TrimSpace(key) == "mount-point"
			case "string":
				if !expectMountPointValue {
					continue
				}
				var value string
				if err := decoder.DecodeElement(&value, &t); err != nil {
					return false, fmt.Errorf("parse hdiutil plist mount point: %w", err)
				}
				expectMountPointValue = false
				if canonicalizeMountpointForComparison(value) == target {
					return true, nil
				}
			default:
				expectMountPointValue = false
			}
		}
	}
}

func cleanHdiutilMountpointCandidate(candidate string) string {
	return filepath.Clean(strings.TrimSpace(candidate))
}
