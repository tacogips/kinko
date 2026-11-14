package kinko

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	bwsSyncStateFormatV2   = 2
	syncStateEntrySchemaV2 = "kinko.sync.state.entry.v2"
)

type syncStateEnvelope struct {
	Format int
	Raw    map[string]json.RawMessage
}

type syncStateEntryV2 struct {
	Schema           string                     `json:"schema"`
	ProviderIdentity string                     `json:"provider_identity"`
	Endpoint         string                     `json:"endpoint"`
	OrganizationID   string                     `json:"organization_id,omitempty"`
	ProjectID        string                     `json:"project_id"`
	MachineID        string                     `json:"machine_id"`
	SecretID         string                     `json:"secret_id"`
	Name             string                     `json:"name"`
	Revision         string                     `json:"revision"`
	Profile          string                     `json:"profile,omitempty"`
	Key              string                     `json:"key"`
	ValueSHA256      string                     `json:"value_sha256"`
	Scope            scopeKind                  `json:"scope"`
	LocalPath        string                     `json:"local_path,omitempty"`
	LogicalPath      string                     `json:"logical_path,omitempty"`
	Raw              map[string]json.RawMessage `json:"-"`
}

type syncOwnershipRecord struct {
	SecretID         string       `json:"secret_id"`
	ProviderIdentity string       `json:"provider_identity"`
	Revision         string       `json:"revision"`
	Identity         syncIdentity `json:"identity"`
}

type bwsSyncStateV2 struct {
	Format          int                            `json:"format"`
	Context         *syncPlanContext               `json:"context,omitempty"`
	Entries         map[string]syncStateEntryV2    `json:"entries"`
	Ownership       map[string]syncOwnershipRecord `json:"ownership"`
	Checkpoint      *syncCheckpoint                `json:"checkpoint,omitempty"`
	MetadataUpgrade *syncMetadataUpgradeCheckpoint `json:"metadata_upgrade,omitempty"`
	Raw             map[string]json.RawMessage     `json:"-"`
}

func decodeBWSSyncState(encoded string) (syncStateEnvelope, error) {
	if strings.TrimSpace(encoded) == "" {
		return syncStateEnvelope{}, errors.New("BWS sync state is empty")
	}
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	if err := decoder.Decode(&raw); err != nil {
		return syncStateEnvelope{}, fmt.Errorf("decode BWS sync state envelope: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return syncStateEnvelope{}, fmt.Errorf("decode BWS sync state envelope: %w", err)
	}
	if raw == nil {
		return syncStateEnvelope{}, errors.New("BWS sync state must be a JSON object")
	}
	formatRaw, ok := raw["format"]
	if !ok {
		return syncStateEnvelope{}, errors.New("BWS sync state format is required")
	}
	var format int
	if err := json.Unmarshal(formatRaw, &format); err != nil {
		return syncStateEnvelope{}, fmt.Errorf("decode BWS sync state format: %w", err)
	}
	if format != 1 && format != bwsSyncStateFormatV2 {
		return syncStateEnvelope{}, fmt.Errorf("unsupported BWS sync state format %d", format)
	}
	return syncStateEnvelope{Format: format, Raw: cloneRawMap(raw)}, nil
}

func decodeBWSSyncStateV2(envelope syncStateEnvelope) (*bwsSyncStateV2, error) {
	if envelope.Format != bwsSyncStateFormatV2 {
		return nil, fmt.Errorf("BWS sync state format %d is not format 2", envelope.Format)
	}
	state := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Raw: cloneRawMap(envelope.Raw)}
	if raw := envelope.Raw["context"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &state.Context); err != nil {
			return nil, fmt.Errorf("decode BWS sync provider context: %w", err)
		}
		if err := validateSyncPlanContext(*state.Context); err != nil {
			return nil, fmt.Errorf("validate BWS sync provider context: %w", err)
		}
	}
	if raw := envelope.Raw["entries"]; len(raw) > 0 {
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("decode BWS sync state entries: %w", err)
		}
		state.Entries = make(map[string]syncStateEntryV2, len(entries))
		for id, encoded := range entries {
			entry, err := decodeSyncStateEntryV2(encoded)
			if err != nil {
				return nil, fmt.Errorf("decode BWS sync state entry %q: %w", id, err)
			}
			state.Entries[id] = entry
		}
	}
	if state.Entries == nil {
		state.Entries = map[string]syncStateEntryV2{}
	}
	if raw := envelope.Raw["ownership"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &state.Ownership); err != nil {
			return nil, fmt.Errorf("decode BWS sync ownership: %w", err)
		}
	}
	if state.Ownership == nil {
		state.Ownership = map[string]syncOwnershipRecord{}
	}
	if raw := envelope.Raw["checkpoint"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		checkpoint, err := decodeSyncCheckpoint(raw)
		if err != nil {
			return nil, fmt.Errorf("decode BWS sync checkpoint: %w", err)
		}
		state.Checkpoint = &checkpoint
	}
	if raw := envelope.Raw["metadata_upgrade"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &state.MetadataUpgrade); err != nil {
			return nil, fmt.Errorf("decode BWS metadata-upgrade checkpoint: %w", err)
		}
		if err := validateSyncMetadataUpgradeCheckpoint(state.MetadataUpgrade); err != nil {
			return nil, fmt.Errorf("validate BWS metadata-upgrade checkpoint: %w", err)
		}
	}
	return state, nil
}

func mergeSelectedBWSSyncState(envelope syncStateEnvelope, desired *bwsSyncStateV2, selected map[string]struct{}) (string, error) {
	if desired == nil {
		return "", errors.New("desired BWS sync state is nil")
	}
	if desired.Format == 0 {
		desired.Format = bwsSyncStateFormatV2
	}
	if desired.Format != bwsSyncStateFormatV2 {
		return "", fmt.Errorf("unsupported desired BWS sync state format %d", desired.Format)
	}
	if envelope.Format != 1 && envelope.Format != bwsSyncStateFormatV2 {
		return "", fmt.Errorf("unsupported existing BWS sync state format %d", envelope.Format)
	}

	root := cloneRawMap(envelope.Raw)
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	existingEntries, err := rawObject(root["entries"])
	if err != nil {
		return "", fmt.Errorf("decode existing BWS sync entries: %w", err)
	}
	for id := range selected {
		entry, exists := desired.Entries[id]
		if !exists {
			delete(existingEntries, id)
			continue
		}
		encoded, err := encodeSyncStateEntryV2(entry)
		if err != nil {
			return "", fmt.Errorf("encode selected BWS sync entry %q: %w", id, err)
		}
		existingEntries[id] = encoded
	}
	entriesJSON, err := marshalRawObject(existingEntries)
	if err != nil {
		return "", fmt.Errorf("encode BWS sync entries: %w", err)
	}
	root["entries"] = entriesJSON

	existingOwnership, err := rawObject(root["ownership"])
	if err != nil {
		return "", fmt.Errorf("decode existing BWS sync ownership: %w", err)
	}
	// An absent desired ownership record never removes proof. Confirmed remote
	// deletion must explicitly remove it through the maintenance layer.
	for id, record := range desired.Ownership {
		if _, isSelected := selected[id]; !isSelected {
			continue
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", fmt.Errorf("encode BWS sync ownership %q: %w", id, err)
		}
		existingOwnership[id] = encoded
	}
	ownershipJSON, err := marshalRawObject(existingOwnership)
	if err != nil {
		return "", fmt.Errorf("encode BWS sync ownership: %w", err)
	}
	root["ownership"] = ownershipJSON
	if desired.Context != nil {
		if err := validateSyncPlanContext(*desired.Context); err != nil {
			return "", fmt.Errorf("validate BWS sync provider context: %w", err)
		}
		contextJSON, err := json.Marshal(desired.Context)
		if err != nil {
			return "", fmt.Errorf("encode BWS sync provider context: %w", err)
		}
		root["context"] = contextJSON
	}

	if desired.Checkpoint != nil {
		checkpoint, err := json.Marshal(desired.Checkpoint)
		if err != nil {
			return "", fmt.Errorf("encode BWS sync checkpoint: %w", err)
		}
		root["checkpoint"] = checkpoint
	}
	if desired.MetadataUpgrade != nil {
		checkpoint, err := json.Marshal(desired.MetadataUpgrade)
		if err != nil {
			return "", fmt.Errorf("encode BWS metadata-upgrade checkpoint: %w", err)
		}
		root["metadata_upgrade"] = checkpoint
	} else {
		delete(root, "metadata_upgrade")
	}
	for key, value := range desired.Raw {
		if isKnownSyncStateV2Field(key) {
			continue
		}
		root[key] = append(json.RawMessage(nil), value...)
	}
	root["format"] = json.RawMessage("2")
	encoded, err := marshalRawObject(root)
	if err != nil {
		return "", fmt.Errorf("encode BWS sync state: %w", err)
	}
	return string(encoded), nil
}

func decodeSyncStateEntryV2(encoded json.RawMessage) (syncStateEntryV2, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return syncStateEntryV2{}, err
	}
	var entry syncStateEntryV2
	if err := json.Unmarshal(encoded, &entry); err != nil {
		return syncStateEntryV2{}, err
	}
	entry.Raw = cloneRawMap(raw)
	if entry.Schema != syncStateEntrySchemaV2 {
		return syncStateEntryV2{}, fmt.Errorf("unsupported schema %q", entry.Schema)
	}
	if err := validateSyncStateEntryV2(entry); err != nil {
		return syncStateEntryV2{}, err
	}
	return entry, nil
}

func encodeSyncStateEntryV2(entry syncStateEntryV2) (json.RawMessage, error) {
	if entry.Schema == "" {
		entry.Schema = syncStateEntrySchemaV2
	}
	if err := validateSyncStateEntryV2(entry); err != nil {
		return nil, err
	}
	known, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(known, &fields); err != nil {
		return nil, err
	}
	merged := cloneRawMap(entry.Raw)
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	for key, value := range fields {
		merged[key] = value
	}
	return marshalRawObject(merged)
}

func validateSyncStateEntryV2(entry syncStateEntryV2) error {
	if entry.Schema != syncStateEntrySchemaV2 {
		return fmt.Errorf("unsupported schema %q", entry.Schema)
	}
	for field, value := range map[string]string{
		"provider_identity": entry.ProviderIdentity,
		"endpoint":          entry.Endpoint,
		"project_id":        entry.ProjectID,
		"secret_id":         entry.SecretID,
		"name":              entry.Name,
		"revision":          entry.Revision,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	if !isValidMachineID(entry.MachineID) {
		return errors.New("machine_id is invalid")
	}
	if err := validateEnvKey(entry.Key); err != nil {
		return fmt.Errorf("key is invalid: %w", err)
	}
	if entry.Key == sharedKeyBWSAccessToken {
		return errors.New("reserved BWS access-token key must not appear in sync state")
	}
	if !isLowerHex(entry.ValueSHA256, 64) {
		return errors.New("value_sha256 must be 64 lowercase hex characters")
	}
	switch entry.Scope {
	case scopeKindShared:
		if entry.Profile != "" || entry.LocalPath != "" || entry.LogicalPath != "" {
			return errors.New("shared scope must not include profile or paths")
		}
	case scopeKindPath:
		if entry.Profile == "" {
			return errors.New("path scope profile is required")
		}
		if entry.LocalPath == "" && entry.LogicalPath == "" {
			return errors.New("path scope requires a local or logical path")
		}
		if entry.LocalPath != "" {
			if err := validateCleanAbsoluteLocalPath(entry.LocalPath); err != nil {
				return fmt.Errorf("local_path is invalid: %w", err)
			}
		}
		if entry.LogicalPath != "" {
			if err := validateLogicalPath(entry.LogicalPath); err != nil {
				return fmt.Errorf("logical_path is invalid: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported scope kind %q", entry.Scope)
	}
	return nil
}

func rawObject(encoded json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(encoded)) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]json.RawMessage{}
	}
	return result, nil
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	return clone
}

func isKnownSyncStateV2Field(key string) bool {
	switch key {
	case "format", "context", "entries", "ownership", "checkpoint", "metadata_upgrade":
		return true
	default:
		return false
	}
}

func marshalRawObject(fields map[string]json.RawMessage) (json.RawMessage, error) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range keys {
		value := fields[key]
		if !json.Valid(value) {
			return nil, fmt.Errorf("field %q contains invalid JSON", key)
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("encode field name %q: %w", key, err)
		}
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}
