package kinko

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type FolderBackend interface {
	Ensure(ctx context.Context, record FolderRecord, secret string) error
	Mount(ctx context.Context, record FolderRecord, secret string, mountpoint string) error
	Unmount(ctx context.Context, record FolderRecord, mountpoint string) error
	Status(ctx context.Context, record FolderRecord, mountpoint string) (FolderMountStatus, error)
}

type FolderMountStatus struct {
	Mounted bool
	Detail  string
}

var newFolderBackend = newDefaultFolderBackend

func folderStorageDir(dataDir string, record FolderRecord) string {
	return filepath.Join(folderStorageRoot(dataDir), record.FolderID)
}

func checkedFolderStorageDir(dataDir string, record FolderRecord) (string, error) {
	if err := validateFolderStorageID(record.FolderID); err != nil {
		return "", err
	}
	return folderStorageDir(dataDir, record), nil
}

func validateFolderStorageID(folderID string) error {
	if folderID == "" {
		return errors.New("folder storage id must not be empty")
	}
	if filepath.IsAbs(folderID) || folderID == "." || folderID == ".." || filepath.Clean(folderID) != folderID {
		return fmt.Errorf("folder storage id must be one relative path element: %q", folderID)
	}
	if strings.ContainsAny(folderID, `/\`) {
		return fmt.Errorf("folder storage id must not contain path separators: %q", folderID)
	}
	return nil
}

func folderStorageRoot(dataDir string) string {
	return filepath.Join(dataDir, "folders")
}

func folderBackendName() string {
	switch runtime.GOOS {
	case "darwin":
		return "hdiutil"
	default:
		return runtime.GOOS
	}
}

func folderBackendEnv() []string {
	return []string{
		"LANG=C",
	}
}

func runFolderBackendCommand(ctx context.Context, stdin string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = folderBackendEnv()
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := redactFolderBackendOutput(stderr.String(), stdin); msg != "" {
			return fmt.Errorf("%s failed: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func outputFolderBackendCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = folderBackendEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := redactFolderBackendOutput(stderr.String(), ""); msg != "" {
			return nil, fmt.Errorf("%s failed: %w: %s", name, err, msg)
		}
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}

func redactFolderBackendOutput(output, secretInput string) string {
	if output == "" {
		return ""
	}
	redacted := output
	for _, secret := range []string{secretInput, strings.TrimSpace(secretInput)} {
		if secret != "" {
			redacted = strings.ReplaceAll(redacted, secret, "[redacted]")
		}
	}
	return redacted
}
