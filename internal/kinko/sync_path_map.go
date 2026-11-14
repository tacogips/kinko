package kinko

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const configKeyBWSSyncPaths = "sync.paths.v1"

type syncPathMap struct {
	Anchor string `json:"anchor"`
	Root   string `json:"root"`
}

func parseSyncPathMap(value string) (syncPathMap, error) {
	anchor, root, ok := strings.Cut(value, "=")
	if !ok || anchor == "" || root == "" {
		return syncPathMap{}, errors.New("path map must use <anchor>=<absolute-root>")
	}
	if err := validateLogicalAnchor(anchor); err != nil {
		return syncPathMap{}, fmt.Errorf("path map anchor is invalid: %w", err)
	}
	if err := validateCleanAbsoluteLocalPath(root); err != nil {
		return syncPathMap{}, fmt.Errorf("path map root is invalid: %w", err)
	}
	return syncPathMap{Anchor: anchor, Root: root}, nil
}

func validateSyncPathMaps(maps []syncPathMap, caseInsensitive bool) error {
	anchors := make(map[string]string, len(maps))
	roots := make(map[string]string, len(maps))
	for _, pathMap := range maps {
		if err := validateLogicalAnchor(pathMap.Anchor); err != nil {
			return fmt.Errorf("path map anchor %q is invalid: %w", pathMap.Anchor, err)
		}
		if err := validateCleanAbsoluteLocalPath(pathMap.Root); err != nil {
			return fmt.Errorf("path map root for %q is invalid: %w", pathMap.Anchor, err)
		}
		anchorKey := pathMap.Anchor
		rootKey := pathMap.Root
		if caseInsensitive {
			anchorKey = strings.ToLower(anchorKey)
			rootKey = strings.ToLower(rootKey)
		}
		if previous, exists := anchors[anchorKey]; exists {
			return fmt.Errorf("path map anchors %q and %q are aliases", previous, pathMap.Anchor)
		}
		if previous, exists := roots[rootKey]; exists {
			return fmt.Errorf("path map roots for %q and %q are equal or aliases", previous, pathMap.Anchor)
		}
		anchors[anchorKey] = pathMap.Anchor
		roots[rootKey] = pathMap.Anchor
	}
	return nil
}

func mapLocalToLogical(localPath string, maps []syncPathMap) (string, error) {
	if err := validateCleanAbsoluteLocalPath(localPath); err != nil {
		return "", fmt.Errorf("local path is invalid: %w", err)
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		return "", err
	}
	var best *syncPathMap
	for index := range maps {
		candidate := &maps[index]
		if !pathContains(candidate.Root, localPath, false) {
			continue
		}
		if best == nil || len(candidate.Root) > len(best.Root) {
			best = candidate
		}
	}
	if best == nil {
		return "", fmt.Errorf("local path %q is not covered by a sync path map", localPath)
	}
	relative, err := filepath.Rel(best.Root, localPath)
	if err != nil {
		return "", fmt.Errorf("derive logical path: %w", err)
	}
	logical := best.Anchor
	if relative != "." {
		logical += "/" + filepath.ToSlash(relative)
	}
	if err := validateLogicalPath(logical); err != nil {
		return "", fmt.Errorf("derived logical path is invalid: %w", err)
	}
	return logical, nil
}

func mapLogicalToLocal(logicalPath string, maps []syncPathMap) (string, error) {
	if err := validateLogicalPath(logicalPath); err != nil {
		return "", fmt.Errorf("logical path is invalid: %w", err)
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		return "", err
	}
	anchor, relative, _ := strings.Cut(logicalPath, "/")
	for _, pathMap := range maps {
		if pathMap.Anchor != anchor {
			continue
		}
		localPath := pathMap.Root
		if relative != "" {
			localPath = filepath.Clean(filepath.Join(pathMap.Root, filepath.FromSlash(relative)))
		}
		if !pathContains(pathMap.Root, localPath, false) {
			return "", errors.New("logical path escapes its mapped root")
		}
		return localPath, nil
	}
	return "", fmt.Errorf("logical path anchor %q is not mapped", anchor)
}

func validateLogicalAnchor(anchor string) error {
	if len(anchor) == 0 || len(anchor) > 63 {
		return errors.New("anchor length must be between 1 and 63 bytes")
	}
	for index := 0; index < len(anchor); index++ {
		character := anchor[index]
		if index == 0 {
			if character < 'a' || character > 'z' {
				return errors.New("anchor must start with a lowercase ASCII letter")
			}
			continue
		}
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return errors.New("anchor contains an unsupported character")
	}
	return nil
}

func validateLogicalPath(logicalPath string) error {
	if logicalPath == "" {
		return errors.New("logical path is empty")
	}
	if strings.ContainsAny(logicalPath, "\\\x00") {
		return errors.New("logical path contains a backslash or NUL")
	}
	if strings.HasPrefix(logicalPath, "/") || strings.HasSuffix(logicalPath, "/") || strings.Contains(logicalPath, "//") {
		return errors.New("logical path contains an empty segment")
	}
	parts := strings.Split(logicalPath, "/")
	if err := validateLogicalAnchor(parts[0]); err != nil {
		return err
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return errors.New("logical path contains a non-canonical segment")
		}
		if strings.Contains(part, ":") || looksLikeWindowsVolume(part) {
			return errors.New("logical path contains platform volume syntax")
		}
	}
	return nil
}

func validateCleanAbsoluteLocalPath(localPath string) error {
	if localPath == "" || strings.ContainsRune(localPath, '\x00') {
		return errors.New("path is empty or contains NUL")
	}
	if looksLikeWindowsVolume(localPath) || strings.HasPrefix(localPath, `\\`) {
		return errors.New("path uses unsupported platform volume syntax")
	}
	if !filepath.IsAbs(localPath) {
		return errors.New("path must be absolute")
	}
	if filepath.Clean(localPath) != localPath {
		return errors.New("path must already be cleaned")
	}
	return nil
}

func looksLikeWindowsVolume(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func pathContains(root, candidate string, caseInsensitive bool) bool {
	if caseInsensitive {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	if root == candidate {
		return true
	}
	if root == string(filepath.Separator) {
		return strings.HasPrefix(candidate, root)
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}

func loadEncryptedSyncPathMaps(config map[string]string) ([]syncPathMap, error) {
	encoded, exists := config[configKeyBWSSyncPaths]
	if !exists {
		return nil, nil
	}
	var maps []syncPathMap
	if err := json.Unmarshal([]byte(encoded), &maps); err != nil {
		return nil, fmt.Errorf("decode encrypted sync path maps: %w", err)
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		return nil, fmt.Errorf("validate encrypted sync path maps: %w", err)
	}
	return maps, nil
}

func saveEncryptedSyncPathMaps(config map[string]string, maps []syncPathMap) error {
	if config == nil {
		return errors.New("config map is nil")
	}
	if err := validateSyncPathMaps(maps, false); err != nil {
		return err
	}
	stable := append([]syncPathMap(nil), maps...)
	sort.Slice(stable, func(i, j int) bool { return stable[i].Anchor < stable[j].Anchor })
	encoded, err := json.Marshal(stable)
	if err != nil {
		return fmt.Errorf("encode encrypted sync path maps: %w", err)
	}
	config[configKeyBWSSyncPaths] = string(encoded)
	return nil
}

func resolveSyncPathMaps(config map[string]string, invocation []syncPathMap) ([]syncPathMap, error) {
	if len(invocation) > 0 {
		if err := validateSyncPathMaps(invocation, false); err != nil {
			return nil, err
		}
		return append([]syncPathMap(nil), invocation...), nil
	}
	return loadEncryptedSyncPathMaps(config)
}
