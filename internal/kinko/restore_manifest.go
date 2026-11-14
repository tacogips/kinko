package kinko

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// restoreBootstrapEntryPrefix is the archive-relative prefix for the
// optional bootstrap config entry; the basename after this prefix must be a
// single path segment (no further "/").
const restoreBootstrapEntryPrefix = "kinko-backup/config/"

// restoreRequiredEntryNames enumerates the exact archive-relative names
// restore accepts, independent of the bootstrap config basename which is
// matched by restoreBootstrapEntryPrefix.
var restoreRequiredEntryNames = []string{
	"kinko-backup/manifest.json",
	"kinko-backup/vault/meta.v1.json",
	"kinko-backup/vault/vault.v1.bin",
	"kinko-backup/vault/config.v1.bin",
	"kinko-backup/vault/" + vaultMarker,
}

// restoreManifestEntryName is the archive-relative name of the manifest
// entry itself; it is excluded from its own Files list by the writer.
const restoreManifestEntryName = "kinko-backup/manifest.json"

// restoreManifest mirrors backupManifest (internal/kinko/backup.go) for
// decoding; kept as a distinct type so restore does not depend on backup's
// internal field evolution beyond the documented JSON shape.
type restoreManifest struct {
	Version          int      `json:"version"`
	CreatedAtUTC     string   `json:"created_at_utc"`
	BootstrapPresent bool     `json:"bootstrap_present"`
	Files            []string `json:"files"`
}

// validatedRestoreArchive is the outcome of allowlist + manifest validation:
// required vault entries plus at most one optional bootstrap entry.
type validatedRestoreArchive struct {
	metaJSON       []byte
	vaultBin       []byte
	configBin      []byte
	marker         []byte
	bootstrapName  string // archive-relative name, "" if absent
	bootstrapBytes []byte
}

// validateRestoreArchiveEntries applies the entry-name allowlist (rejecting
// any other name as policy error), requires exactly one manifest.json and
// the four required vault entries, allows at most one config/ entry, parses
// and cross-checks manifest.json (version==1, Files matches present
// entries, BootstrapPresent matches config/ entry presence).
func validateRestoreArchiveEntries(archive *restoreZipArchive) (*validatedRestoreArchive, error) {
	requiredSet := make(map[string]bool, len(restoreRequiredEntryNames))
	for _, name := range restoreRequiredEntryNames {
		requiredSet[name] = false
	}

	var bootstrapName string
	bootstrapCount := 0

	for _, name := range archive.order {
		if err := rejectHostileRestoreEntryName(name); err != nil {
			return nil, err
		}

		switch {
		case requiredSet[name] == false && isRestoreRequiredName(name):
			requiredSet[name] = true
		case strings.HasPrefix(name, restoreBootstrapEntryPrefix):
			basename := strings.TrimPrefix(name, restoreBootstrapEntryPrefix)
			if basename == "" || strings.Contains(basename, "/") {
				return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("invalid bootstrap archive entry name: %s", name)}
			}
			bootstrapCount++
			bootstrapName = name
		default:
			return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("unexpected archive entry name: %s", name)}
		}
	}

	for _, name := range restoreRequiredEntryNames {
		if !requiredSet[name] {
			return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("required archive entry missing: %s", name)}
		}
	}
	if bootstrapCount > 1 {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: "archive contains more than one bootstrap config entry"}
	}

	manifestBytes, err := archive.entry(restoreManifestEntryName)
	if err != nil {
		return nil, err
	}
	var manifest restoreManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("parse archive manifest: %v", err), err: err}
	}
	if manifest.Version != backupManifestVersion {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("unsupported manifest version: %d", manifest.Version)}
	}

	presentExcludingManifest := make([]string, 0, len(archive.order))
	for _, name := range archive.order {
		if name == restoreManifestEntryName {
			continue
		}
		presentExcludingManifest = append(presentExcludingManifest, name)
	}
	if !stringSetsEqual(manifest.Files, presentExcludingManifest) {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: "manifest file list does not match archive contents"}
	}

	if manifest.BootstrapPresent != (bootstrapName != "") {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: "manifest bootstrap_present does not match archive contents"}
	}

	metaJSON, err := archive.entry("kinko-backup/vault/meta.v1.json")
	if err != nil {
		return nil, err
	}
	vaultBin, err := archive.entry("kinko-backup/vault/vault.v1.bin")
	if err != nil {
		return nil, err
	}
	configBin, err := archive.entry("kinko-backup/vault/config.v1.bin")
	if err != nil {
		return nil, err
	}
	marker, err := archive.entry("kinko-backup/vault/" + vaultMarker)
	if err != nil {
		return nil, err
	}

	result := &validatedRestoreArchive{
		metaJSON:  metaJSON,
		vaultBin:  vaultBin,
		configBin: configBin,
		marker:    marker,
	}
	if bootstrapName != "" {
		bootstrapBytes, err := archive.entry(bootstrapName)
		if err != nil {
			return nil, err
		}
		result.bootstrapName = bootstrapName
		result.bootstrapBytes = bootstrapBytes
	}

	return result, nil
}

func isRestoreRequiredName(name string) bool {
	for _, required := range restoreRequiredEntryNames {
		if name == required {
			return true
		}
	}
	return false
}

// rejectHostileRestoreEntryName rejects names that are absolute or contain
// ".." path segments. This is defense in depth: readPasswordLockedZip's
// cleaned-name map and the allowlist compare above would already reject such
// names, but the plan calls for an explicit check.
func rejectHostileRestoreEntryName(name string) error {
	if strings.HasPrefix(name, "/") {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("archive entry name must not be absolute: %s", name)}
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("archive entry name must not contain \"..\": %s", name)}
		}
	}
	return nil
}

// stringSetsEqual compares two string slices as sets (same elements,
// duplicates and ordering ignored is not expected here since callers pass
// deduplicated archive entry names, but sorting keeps the comparison robust
// to ordering).
func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}
