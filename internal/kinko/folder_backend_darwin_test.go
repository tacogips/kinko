//go:build darwin

package kinko

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHdiutilFolderBackendRejectsSymlinkedImagePath(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "folder-id",
	}
	backend := hdiutilFolderBackend{dataDir: dataDir}
	imagePath, err := backend.imagePath(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), imagePath); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "ensure",
			run: func() error {
				return backend.Ensure(context.Background(), record, "secret")
			},
		},
		{
			name: "mount",
			run: func() error {
				return backend.Mount(context.Background(), record, "secret", t.TempDir())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "folder image path must not be a symlink") {
				t.Fatalf("expected symlinked image path error, got %v", err)
			}
		})
	}
}

func TestHdiutilFolderBackendRejectsSymlinkedStorageDirectory(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "folder-id",
	}
	backend := hdiutilFolderBackend{dataDir: dataDir}
	storageDir := folderStorageDir(dataDir, record)
	if err := os.MkdirAll(filepath.Dir(storageDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), storageDir); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "ensure",
			run: func() error {
				return backend.Ensure(context.Background(), record, "secret")
			},
		},
		{
			name: "mount",
			run: func() error {
				return backend.Mount(context.Background(), record, "secret", t.TempDir())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "folder storage path must not be a symlink") {
				t.Fatalf("expected symlinked storage directory error, got %v", err)
			}
		})
	}
}

func TestHdiutilFolderBackendRejectsNonDirectoryImagePath(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "folder-id",
	}
	backend := hdiutilFolderBackend{dataDir: dataDir}
	imagePath, err := backend.imagePath(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("not a sparsebundle directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "ensure",
			run: func() error {
				return backend.Ensure(context.Background(), record, "secret")
			},
		},
		{
			name: "mount",
			run: func() error {
				return backend.Mount(context.Background(), record, "secret", t.TempDir())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "folder image path exists and is not a directory") {
				t.Fatalf("expected non-directory image path error, got %v", err)
			}
		})
	}
}

func TestHdiutilFolderBackendMountRequiresExistingImagePath(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "folder-id",
	}
	backend := hdiutilFolderBackend{dataDir: dataDir}
	if err := ensureFolderStorageDirectory(dataDir, record); err != nil {
		t.Fatal(err)
	}

	err := backend.Mount(context.Background(), record, "secret", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "folder image path does not exist") {
		t.Fatalf("expected missing image path error, got %v", err)
	}
}

func TestHdiutilFolderBackendRejectsInvalidFolderStorageID(t *testing.T) {
	dataDir := t.TempDir()
	record := FolderRecord{
		Name:     "private",
		Profile:  "default",
		Path:     t.TempDir(),
		Backend:  folderBackendName(),
		FolderID: "../outside",
	}
	backend := hdiutilFolderBackend{dataDir: dataDir}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "ensure",
			run: func() error {
				return backend.Ensure(context.Background(), record, "secret")
			},
		},
		{
			name: "mount",
			run: func() error {
				return backend.Mount(context.Background(), record, "secret", t.TempDir())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "folder storage id must") {
				t.Fatalf("expected invalid folder storage id error, got %v", err)
			}
		})
	}
}

func TestParseHdiutilInfoMountedRequiresExactMountpoint(t *testing.T) {
	info := []byte("/dev/disk4s1\tApple_APFS\t/private/tmp/project/private-other\n")
	if parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("substring mountpoint was reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsExactMountpoint(t *testing.T) {
	info := []byte("/dev/disk4s1\tApple_APFS\t/private/tmp/project/private\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("exact mountpoint was not reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsLabeledMountpoint(t *testing.T) {
	info := []byte("mount-point: /private/tmp/project/private\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("labeled exact mountpoint was not reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsLabeledMountpointWithColon(t *testing.T) {
	info := []byte("mount-point: /private/tmp/project/private:with-colon\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private:with-colon") {
		t.Fatal("labeled exact mountpoint containing colon was not reported as mounted")
	}
}
