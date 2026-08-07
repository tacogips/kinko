package kinko

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type logicalScopeRef struct {
	Profile     string
	Kind        scopeKind
	LogicalPath string
	LocalPath   string
}

type bwsNoteMetadataV2 struct {
	KinkoSyncFormat int       `json:"kinko_sync_format"`
	MachineID       string    `json:"machine_id"`
	Profile         string    `json:"profile,omitempty"`
	Scope           scopeKind `json:"scope"`
	LogicalPath     string    `json:"logical_path,omitempty"`
	Key             string    `json:"key"`
}

func deriveScopeHashV2(ref logicalScopeRef) string {
	payload := strings.Join([]string{"kinko.scope.v2", ref.Profile, string(ref.Kind), ref.LogicalPath}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:4])
}

func buildBWSSecretNameV2(machineID string, ref logicalScopeRef, key string) string {
	return machineID + "_" + deriveScopeHashV2(ref) + "_" + key
}

func encodeBWSNoteV2(metadata bwsNoteMetadataV2) (string, error) {
	normalized, err := normalizeBWSNoteMetadataV2(metadata)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode BWS format-2 note: %w", err)
	}
	return string(encoded), nil
}

func parseBWSNoteV2(note string) (bwsNoteMetadataV2, error) {
	if strings.TrimSpace(note) == "" {
		return bwsNoteMetadataV2{}, errors.New("BWS note is missing")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(note))
	decoder.DisallowUnknownFields()
	var metadata bwsNoteMetadataV2
	if err := decoder.Decode(&metadata); err != nil {
		return bwsNoteMetadataV2{}, fmt.Errorf("parse BWS format-2 note: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return bwsNoteMetadataV2{}, fmt.Errorf("parse BWS format-2 note: %w", err)
	}
	return normalizeBWSNoteMetadataV2(metadata)
}

func verifyNoteMatchesNameV2(machineID, name string, metadata bwsNoteMetadataV2) error {
	metadata, err := normalizeBWSNoteMetadataV2(metadata)
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
	ref := logicalScopeRef{Profile: metadata.Profile, Kind: metadata.Scope, LogicalPath: metadata.LogicalPath}
	if deriveScopeHashV2(ref) != scopeHash {
		return errors.New("BWS note logical scope does not match secret name")
	}
	if metadata.Key != key {
		return errors.New("BWS note key does not match secret name")
	}
	return nil
}

func normalizeBWSNoteMetadataV2(metadata bwsNoteMetadataV2) (bwsNoteMetadataV2, error) {
	if metadata.KinkoSyncFormat != bwsSyncStateFormatV2 {
		return bwsNoteMetadataV2{}, fmt.Errorf("unsupported kinko sync note format %d", metadata.KinkoSyncFormat)
	}
	if !isValidMachineID(metadata.MachineID) {
		return bwsNoteMetadataV2{}, errors.New("BWS note machine_id is invalid")
	}
	if err := validateEnvKey(metadata.Key); err != nil {
		return bwsNoteMetadataV2{}, fmt.Errorf("BWS note key is invalid: %w", err)
	}
	if metadata.Key == sharedKeyBWSAccessToken {
		return bwsNoteMetadataV2{}, errors.New("reserved BWS access-token key must not appear in a sync note")
	}
	switch metadata.Scope {
	case scopeKindShared:
		if metadata.Profile != "" || metadata.LogicalPath != "" {
			return bwsNoteMetadataV2{}, errors.New("shared BWS note must not include profile or logical_path")
		}
	case scopeKindPath:
		if metadata.Profile == "" {
			return bwsNoteMetadataV2{}, errors.New("path BWS note profile is required")
		}
		if err := validateLogicalPath(metadata.LogicalPath); err != nil {
			return bwsNoteMetadataV2{}, fmt.Errorf("BWS note logical_path is invalid: %w", err)
		}
	default:
		return bwsNoteMetadataV2{}, fmt.Errorf("unsupported BWS note scope %q", metadata.Scope)
	}
	return metadata, nil
}
