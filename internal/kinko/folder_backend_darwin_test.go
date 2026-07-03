//go:build darwin

package kinko

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHdiutilPathUsesSystemBinary(t *testing.T) {
	if hdiutilPath != "/usr/bin/hdiutil" {
		t.Fatalf("hdiutilPath=%q want /usr/bin/hdiutil", hdiutilPath)
	}
}

func TestFolderBackendEnvDoesNotSetPath(t *testing.T) {
	if got := folderBackendEnv(); !reflect.DeepEqual(got, []string{"LANG=C"}) {
		t.Fatalf("folderBackendEnv()=%q want LANG only", got)
	}
}

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

func TestParseHdiutilInfoPlistMountedAcceptsExactMountpoint(t *testing.T) {
	info := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>images</key>
  <array>
    <dict>
      <key>system-entities</key>
      <array>
        <dict>
          <key>mount-point</key>
          <string>/private/tmp/project/private</string>
        </dict>
      </array>
    </dict>
  </array>
</dict>
</plist>`)
	mounted, err := parseHdiutilInfoPlistMounted(info, "/private/tmp/project/private")
	if err != nil {
		t.Fatal(err)
	}
	if !mounted {
		t.Fatal("exact plist mountpoint was not reported as mounted")
	}
}

func TestParseHdiutilInfoPlistMountedRejectsDifferentMountpoint(t *testing.T) {
	info := []byte(`<plist><dict><key>mount-point</key><string>/private/tmp/project/private-other</string></dict></plist>`)
	mounted, err := parseHdiutilInfoPlistMounted(info, "/private/tmp/project/private")
	if err != nil {
		t.Fatal(err)
	}
	if mounted {
		t.Fatal("different plist mountpoint was reported as mounted")
	}
}

func TestParseHdiutilInfoPlistMountedRejectsMalformedPlist(t *testing.T) {
	_, err := parseHdiutilInfoPlistMounted([]byte(`<plist><dict>`), "/private/tmp/project/private")
	if err == nil {
		t.Fatal("expected malformed plist error")
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
