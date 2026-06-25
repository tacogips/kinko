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

var errFolderUnsupportedPlatform = errors.New("folder vault backend is unsupported on this platform")

var newFolderBackend = newDefaultFolderBackend

func folderStorageDir(dataDir string, record FolderRecord) string {
	return filepath.Join(dataDir, "folders", record.FolderID)
}

func folderBackendName() string {
	switch runtime.GOOS {
	case "darwin":
		return "hdiutil"
	default:
		return runtime.GOOS
	}
}

type unsupportedFolderBackend struct{}

func (unsupportedFolderBackend) Ensure(context.Context, FolderRecord, string) error {
	return errFolderUnsupportedPlatform
}

func (unsupportedFolderBackend) Mount(context.Context, FolderRecord, string, string) error {
	return errFolderUnsupportedPlatform
}

func (unsupportedFolderBackend) Unmount(context.Context, FolderRecord, string) error {
	return errFolderUnsupportedPlatform
}

func (unsupportedFolderBackend) Status(context.Context, FolderRecord, string) (FolderMountStatus, error) {
	return FolderMountStatus{}, errFolderUnsupportedPlatform
}

func folderBackendEnv() []string {
	return []string{
		"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
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
