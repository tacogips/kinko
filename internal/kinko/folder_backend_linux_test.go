//go:build linux

package kinko

import (
	"context"
	"errors"
	"testing"
)

func TestLinuxFolderBackendUnavailable(t *testing.T) {
	if got := folderBackendName(); got == "gocryptfs" {
		t.Fatalf("folderBackendName()=%q, must not advertise gocryptfs on linux", got)
	}
	if got := folderBackendName(); got != "linux" {
		t.Fatalf("folderBackendName()=%q, want linux", got)
	}

	backend := newDefaultFolderBackend(t.TempDir())
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "folder-id",
	}

	assertFolderBackendUnsupported(t, backend.Ensure(context.Background(), record, "secret"))
	assertFolderBackendUnsupported(t, backend.Mount(context.Background(), record, "secret", t.TempDir()))
	assertFolderBackendUnsupported(t, backend.Unmount(context.Background(), record, t.TempDir()))
	_, err := backend.Status(context.Background(), record, t.TempDir())
	assertFolderBackendUnsupported(t, err)
}

func assertFolderBackendUnsupported(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errFolderUnsupportedPlatform) {
		t.Fatalf("error=%v, want errFolderUnsupportedPlatform", err)
	}
}
