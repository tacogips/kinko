package kinko

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const folderConfigKey = "folders.v1"

type FolderRecord struct {
	Name      string    `json:"name"`
	Profile   string    `json:"profile"`
	Path      string    `json:"path"`
	Backend   string    `json:"backend"`
	FolderID  string    `json:"folder_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type folderConfigPayload struct {
	Folders []FolderRecord `json:"folders,omitempty"`
}

type folderStorageMetadata struct {
	FormatVersion int       `json:"format_version"`
	Backend       string    `json:"backend"`
	FolderID      string    `json:"folder_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func validateFolderName(name string) error {
	if name == "" {
		return errors.New("folder name must not be empty")
	}
	if strings.TrimSpace(name) != name || name == "." || name == ".." {
		return fmt.Errorf("invalid folder name %q", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("folder name must not start with '-': %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("folder name must not contain control characters: %q", name)
		}
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("folder name must be one relative path element: %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("folder name must not contain path separators: %q", name)
	}
	if filepath.Clean(name) != name {
		return fmt.Errorf("folder name must be one relative path element: %q", name)
	}
	return nil
}

func deriveFolderID(profile, path, name string) string {
	input := strings.Join([]string{"kinko.folder.v1", profile, filepath.Clean(path), name}, "\x00")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func loadFolderRecordsFromConfig(cfg map[string]string) ([]FolderRecord, error) {
	raw := strings.TrimSpace(cfg[folderConfigKey])
	if raw == "" {
		return nil, nil
	}
	var payload folderConfigPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode folder config: %w", err)
	}
	records := append([]FolderRecord(nil), payload.Folders...)
	sortFolderRecords(records)
	return records, nil
}

func saveFolderRecordsToConfig(cfg map[string]string, records []FolderRecord) error {
	payload := folderConfigPayload{Folders: append([]FolderRecord(nil), records...)}
	sortFolderRecords(payload.Folders)
	if len(payload.Folders) == 0 {
		delete(cfg, folderConfigKey)
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode folder config: %w", err)
	}
	cfg[folderConfigKey] = string(b)
	return nil
}

func findFolderRecord(records []FolderRecord, profile, path, name string) (FolderRecord, bool) {
	cleanPath := filepath.Clean(path)
	for _, record := range records {
		if record.Profile == profile && filepath.Clean(record.Path) == cleanPath && record.Name == name {
			return record, true
		}
	}
	return FolderRecord{}, false
}

func newFolderRecord(profile, path, name, backend string, now time.Time) (FolderRecord, error) {
	if err := validateFolderName(name); err != nil {
		return FolderRecord{}, err
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return FolderRecord{}, errors.New("profile must not be empty")
	}
	cleanPath := filepath.Clean(path)
	return FolderRecord{
		Name:      name,
		Profile:   profile,
		Path:      cleanPath,
		Backend:   backend,
		FolderID:  deriveFolderID(profile, cleanPath, name),
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

func folderMountpoint(record FolderRecord) string {
	return filepath.Join(record.Path, record.Name)
}

func ensureFolderStorageMetadata(dataDir string, record FolderRecord) error {
	if err := ensureFolderStorageDirectory(dataDir, record); err != nil {
		return err
	}
	metadata := folderStorageMetadata{
		FormatVersion: 1,
		Backend:       record.Backend,
		FolderID:      record.FolderID,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
	b, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode folder storage metadata: %w", err)
	}
	b = append(b, '\n')
	dir, err := checkedFolderStorageDir(dataDir, record)
	if err != nil {
		return err
	}
	return writeFolderStorageMetadata(filepath.Join(dir, "meta.json"), b)
}

func writeFolderStorageMetadata(path string, b []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat folder storage metadata: %w", err)
		}
	} else {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("folder storage metadata must not be a symlink: %s", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("folder storage metadata path exists and is not a regular file: %s", path)
		}
	}
	if err := write0600Atomically(path, b); err != nil {
		return fmt.Errorf("write folder storage metadata: %w", err)
	}
	return nil
}

func folderRecordsForScope(records []FolderRecord, profile, path string) []FolderRecord {
	path = filepath.Clean(path)
	out := []FolderRecord{}
	for _, record := range records {
		if record.Profile == profile && filepath.Clean(record.Path) == path {
			out = append(out, record)
		}
	}
	sortFolderRecords(out)
	return out
}

func sortFolderRecords(records []FolderRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Profile != records[j].Profile {
			return records[i].Profile < records[j].Profile
		}
		if records[i].Path != records[j].Path {
			return records[i].Path < records[j].Path
		}
		return records[i].Name < records[j].Name
	})
}
