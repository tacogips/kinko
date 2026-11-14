//go:build linux

package kinko

func newDefaultFolderBackend(_ string) FolderBackend {
	return unsupportedFolderBackend{}
}
