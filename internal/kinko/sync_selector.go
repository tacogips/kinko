package kinko

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

type syncSharedMode string

const (
	syncSharedInclude syncSharedMode = "include"
	syncSharedExclude syncSharedMode = "exclude"
	syncSharedOnly    syncSharedMode = "only"
)

type syncSelector struct {
	IncludeProfiles []string       `json:"include_profiles,omitempty"`
	IncludePaths    []string       `json:"include_paths,omitempty"`
	IncludeKeys     []string       `json:"include_keys,omitempty"`
	ExcludeProfiles []string       `json:"exclude_profiles,omitempty"`
	ExcludePaths    []string       `json:"exclude_paths,omitempty"`
	ExcludeKeys     []string       `json:"exclude_keys,omitempty"`
	Shared          syncSharedMode `json:"shared"`
}

type syncIdentity struct {
	Provider  string    `json:"provider,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	MachineID string    `json:"machine_id,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	Path      string    `json:"path,omitempty"`
	Key       string    `json:"key"`
	Scope     scopeKind `json:"scope"`
}

func normalizeSyncSelector(selector syncSelector) (syncSelector, string, error) {
	if selector.Shared == "" {
		selector.Shared = syncSharedInclude
	}
	if selector.Shared != syncSharedInclude && selector.Shared != syncSharedExclude && selector.Shared != syncSharedOnly {
		return syncSelector{}, "", fmt.Errorf("unsupported shared selection mode %q", selector.Shared)
	}
	var err error
	if selector.IncludeProfiles, err = normalizeMatchValues(selector.IncludeProfiles, matchDimensionProfile); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize included profiles: %w", err)
	}
	if selector.ExcludeProfiles, err = normalizeMatchValues(selector.ExcludeProfiles, matchDimensionProfile); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize excluded profiles: %w", err)
	}
	if selector.IncludeKeys, err = normalizeMatchValues(selector.IncludeKeys, matchDimensionKey); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize included keys: %w", err)
	}
	if selector.ExcludeKeys, err = normalizeMatchValues(selector.ExcludeKeys, matchDimensionKey); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize excluded keys: %w", err)
	}
	if selector.IncludePaths, err = normalizeSelectorPaths(selector.IncludePaths); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize included paths: %w", err)
	}
	if selector.ExcludePaths, err = normalizeSelectorPaths(selector.ExcludePaths); err != nil {
		return syncSelector{}, "", fmt.Errorf("normalize excluded paths: %w", err)
	}
	encoded, err := json.Marshal(selector)
	if err != nil {
		return syncSelector{}, "", fmt.Errorf("encode normalized selector: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return selector, hex.EncodeToString(digest[:]), nil
}

func selectSyncIdentities(selector syncSelector, identities []syncIdentity) (map[string]struct{}, error) {
	normalized, _, err := normalizeSyncSelector(selector)
	if err != nil {
		return nil, err
	}
	validated := make(map[string]syncIdentity, len(identities))
	for _, identity := range identities {
		if err := validateSyncIdentity(identity); err != nil {
			return nil, fmt.Errorf("invalid sync identity: %w", err)
		}
		id := syncEntryID(identity)
		if previous, exists := validated[id]; exists && previous != identity {
			return nil, fmt.Errorf("ambiguous sync identity id %q", id)
		}
		validated[id] = identity
	}
	selected := make(map[string]struct{}, len(validated))
	for id, identity := range validated {
		// Reserved credential material is excluded only after every identity has
		// been validated, so malformed or ambiguous metadata cannot hide behind
		// the reserved-key boundary.
		if identity.Key == sharedKeyBWSAccessToken {
			continue
		}
		if !selectorIncludesIdentity(normalized, identity) {
			continue
		}
		selected[id] = struct{}{}
	}
	return selected, nil
}

func syncEntryID(identity syncIdentity) string {
	payload := strings.Join([]string{
		"kinko.sync.entry.v2",
		identity.Provider,
		identity.ProjectID,
		identity.MachineID,
		identity.Profile,
		string(identity.Scope),
		identity.Path,
		identity.Key,
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

type matchDimension int

const (
	matchDimensionProfile matchDimension = iota
	matchDimensionKey
)

func normalizeMatchValues(values []string, dimension matchDimension) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("selector value is empty or contains NUL")
		}
		if strings.HasPrefix(value, "glob:") {
			pattern := strings.TrimPrefix(value, "glob:")
			if pattern == "" || strings.Contains(pattern, "/") {
				return nil, errors.New("glob pattern is empty or contains a path separator")
			}
			if _, err := path.Match(pattern, ""); err != nil {
				return nil, fmt.Errorf("malformed glob %q: %w", pattern, err)
			}
		} else if dimension == matchDimensionKey {
			if err := validateEnvKey(value); err != nil {
				return nil, fmt.Errorf("invalid exact key %q: %w", value, err)
			}
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeSelectorPaths(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		var err error
		switch {
		case strings.HasPrefix(value, "logical:"):
			err = validateLogicalPath(strings.TrimPrefix(value, "logical:"))
		case strings.HasPrefix(value, "local:"):
			err = validateCleanAbsoluteLocalPath(strings.TrimPrefix(value, "local:"))
		default:
			return nil, fmt.Errorf("path selector %q must start with logical: or local:", value)
		}
		if err != nil {
			return nil, fmt.Errorf("path selector %q is invalid: %w", value, err)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validateSyncIdentity(identity syncIdentity) error {
	if err := validateEnvKey(identity.Key); err != nil {
		return fmt.Errorf("key is invalid: %w", err)
	}
	switch identity.Scope {
	case scopeKindShared:
		if identity.Profile != "" || identity.Path != "" {
			return errors.New("shared identity must not include profile or path")
		}
	case scopeKindPath:
		if identity.Profile == "" {
			return errors.New("path identity profile is required")
		}
		if _, err := normalizeSelectorPaths([]string{identity.Path}); err != nil {
			return fmt.Errorf("path identity is invalid: %w", err)
		}
	default:
		return fmt.Errorf("unsupported scope kind %q", identity.Scope)
	}
	return nil
}

func selectorIncludesIdentity(selector syncSelector, identity syncIdentity) bool {
	if identity.Scope == scopeKindShared {
		if selector.Shared == syncSharedExclude {
			return false
		}
		return matchesAllKeyRules(selector, identity.Key)
	}
	if selector.Shared == syncSharedOnly {
		return false
	}
	if len(selector.IncludeProfiles) > 0 && !matchesAny(selector.IncludeProfiles, identity.Profile) {
		return false
	}
	if len(selector.IncludePaths) > 0 && !containsExact(selector.IncludePaths, identity.Path) {
		return false
	}
	if len(selector.IncludeKeys) > 0 && !matchesAny(selector.IncludeKeys, identity.Key) {
		return false
	}
	if matchesAny(selector.ExcludeProfiles, identity.Profile) || containsExact(selector.ExcludePaths, identity.Path) || matchesAny(selector.ExcludeKeys, identity.Key) {
		return false
	}
	return true
}

func matchesAllKeyRules(selector syncSelector, key string) bool {
	if len(selector.IncludeKeys) > 0 && !matchesAny(selector.IncludeKeys, key) {
		return false
	}
	return !matchesAny(selector.ExcludeKeys, key)
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "glob:") {
			matched, err := path.Match(strings.TrimPrefix(pattern, "glob:"), value)
			if err == nil && matched {
				return true
			}
			continue
		}
		if pattern == value {
			return true
		}
	}
	return false
}

func containsExact(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}
