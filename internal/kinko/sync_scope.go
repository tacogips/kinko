package kinko

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type scopeKind string

const (
	scopeKindPath   scopeKind = "path"
	scopeKindShared scopeKind = "shared"
)

type scopeRef struct {
	profile string
	kind    scopeKind
	path    string
}

type bwsNoteMetadata struct {
	KinkoSyncFormat int       `json:"kinko_sync_format"`
	MachineID       string    `json:"machine_id"`
	Profile         string    `json:"profile,omitempty"`
	Scope           scopeKind `json:"scope"`
	Path            string    `json:"path,omitempty"`
	Key             string    `json:"key"`
}

func deriveScopeHash(ref scopeRef) string {
	normalized, err := normalizeScopeRef(ref)
	if err == nil {
		ref = normalized
	}
	payload := strings.Join([]string{"kinko.scope.v1", ref.profile, string(ref.kind), ref.path}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:4])
}

func buildBWSSecretName(machineID string, ref scopeRef, key string) string {
	return machineID + "_" + deriveScopeHash(ref) + "_" + key
}

func parseBWSSecretName(machineID, name string) (scopeHash, key string, ok bool) {
	if !isValidMachineID(machineID) || !strings.HasPrefix(name, machineID+"_") {
		return "", "", false
	}
	remainder := strings.TrimPrefix(name, machineID+"_")
	if len(remainder) < 10 || remainder[8] != '_' {
		return "", "", false
	}
	scopeHash, key = remainder[:8], remainder[9:]
	if !isLowerHex(scopeHash, 8) {
		return "", "", false
	}
	if err := validateEnvKey(key); err != nil {
		return "", "", false
	}
	return scopeHash, key, true
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			continue
		}
		if r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}

func detectScopeHashCollisions(refs []scopeRef) error {
	return detectScopeHashCollisionsWithHasher(refs, deriveScopeHash)
}

func detectScopeHashCollisionsWithHasher(refs []scopeRef, hasher func(scopeRef) string) error {
	byHash := make(map[string]scopeRef, len(refs))
	for _, rawRef := range refs {
		ref, err := normalizeScopeRef(rawRef)
		if err != nil {
			return newCLIError(exitCodePolicyFailed, "Invalid sync scope.", err)
		}
		hash := hasher(ref)
		if previous, exists := byHash[hash]; exists && previous != ref {
			return newCLIError(
				exitCodePolicyFailed,
				fmt.Sprintf("Scope hash collision %s between %s and %s.", hash, formatScopeRef(previous), formatScopeRef(ref)),
				errors.New("scope hash collision"),
			)
		}
		byHash[hash] = ref
	}
	return nil
}

func encodeBWSNote(metadata bwsNoteMetadata) (string, error) {
	metadata, err := normalizeBWSNoteMetadata(metadata)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode BWS note: %w", err)
	}
	return string(payload), nil
}

func parseBWSNote(note string) (bwsNoteMetadata, error) {
	if strings.TrimSpace(note) == "" {
		return bwsNoteMetadata{}, errors.New("BWS note is missing")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(note))
	decoder.DisallowUnknownFields()
	var metadata bwsNoteMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return bwsNoteMetadata{}, fmt.Errorf("parse BWS note: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return bwsNoteMetadata{}, fmt.Errorf("parse BWS note: %w", err)
	}
	return normalizeBWSNoteMetadata(metadata)
}

func verifyNoteMatchesName(machineID, name string, metadata bwsNoteMetadata) error {
	metadata, err := normalizeBWSNoteMetadata(metadata)
	if err != nil {
		return err
	}
	scopeHash, key, ok := parseBWSSecretName(machineID, name)
	if !ok {
		return errors.New("BWS secret name is malformed or belongs to another machine")
	}
	if metadata.MachineID != machineID {
		return errors.New("BWS note machine_id does not match secret name")
	}
	ref := scopeRef{profile: metadata.Profile, kind: metadata.Scope, path: metadata.Path}
	if deriveScopeHash(ref) != scopeHash {
		return errors.New("BWS note scope does not match secret name")
	}
	if metadata.Key != key {
		return errors.New("BWS note key does not match secret name")
	}
	return nil
}

func normalizeScopeRef(ref scopeRef) (scopeRef, error) {
	switch ref.kind {
	case scopeKindShared:
		if ref.profile != "" || ref.path != "" {
			return scopeRef{}, errors.New("shared scope must not include profile or path")
		}
		return scopeRef{kind: scopeKindShared}, nil
	case scopeKindPath:
		if ref.profile == "" {
			return scopeRef{}, errors.New("path scope profile is required")
		}
		path := normalizePathInput(ref.path)
		if path == "" {
			return scopeRef{}, errors.New("path scope path is required")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return scopeRef{}, fmt.Errorf("normalize scope path: %w", err)
		}
		return scopeRef{profile: ref.profile, kind: scopeKindPath, path: filepath.Clean(absolute)}, nil
	default:
		return scopeRef{}, fmt.Errorf("unsupported scope kind %q", ref.kind)
	}
}

func normalizeBWSNoteMetadata(metadata bwsNoteMetadata) (bwsNoteMetadata, error) {
	if metadata.KinkoSyncFormat != 1 {
		return bwsNoteMetadata{}, fmt.Errorf("unsupported kinko sync note format %d", metadata.KinkoSyncFormat)
	}
	if !isValidMachineID(metadata.MachineID) {
		return bwsNoteMetadata{}, errors.New("BWS note machine_id is invalid")
	}
	if err := validateEnvKey(metadata.Key); err != nil {
		return bwsNoteMetadata{}, fmt.Errorf("BWS note key is invalid: %w", err)
	}
	ref, err := normalizeScopeRef(scopeRef{profile: metadata.Profile, kind: metadata.Scope, path: metadata.Path})
	if err != nil {
		return bwsNoteMetadata{}, fmt.Errorf("BWS note scope is invalid: %w", err)
	}
	metadata.Profile = ref.profile
	metadata.Scope = ref.kind
	metadata.Path = ref.path
	return metadata, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func formatScopeRef(ref scopeRef) string {
	if ref.kind == scopeKindShared {
		return "shared"
	}
	return fmt.Sprintf("profile=%q path=%q", ref.profile, ref.path)
}
