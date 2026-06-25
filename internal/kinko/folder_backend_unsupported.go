//go:build !darwin

package kinko

import (
	"context"
	"errors"
)

var errFolderUnsupportedPlatform = errors.New("folder vault backend is unsupported on this platform")

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
