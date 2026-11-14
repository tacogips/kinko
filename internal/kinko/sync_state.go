package kinko

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const configKeyBWSSyncState = "sync.bws.v1"

type syncStateEntry struct {
	SecretID     string    `json:"secret_id"`
	Name         string    `json:"name"`
	Profile      string    `json:"profile"`
	Scope        scopeKind `json:"scope"`
	Path         string    `json:"path,omitempty"`
	Key          string    `json:"key"`
	RevisionDate string    `json:"revision_date"`
	ValueSHA256  string    `json:"value_sha256"`
}

type bwsSyncState struct {
	Format    int                       `json:"format"`
	MachineID string                    `json:"machine_id"`
	ProjectID string                    `json:"project_id"`
	Entries   map[string]syncStateEntry `json:"entries"`
}

func loadBWSSyncState(config map[string]string) (*bwsSyncState, error) {
	encoded, exists := config[configKeyBWSSyncState]
	if !exists {
		return emptyBWSSyncState(), nil
	}
	if encoded == "" {
		return nil, newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state is empty.", errMetadataInvalid)
	}
	var state bwsSyncState
	if err := json.Unmarshal([]byte(encoded), &state); err != nil {
		return nil, newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state is malformed.", fmt.Errorf("decode BWS sync state: %w", err))
	}
	if state.Format != 1 {
		return nil, newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state has an unsupported format.", fmt.Errorf("BWS sync state format %d", state.Format))
	}
	if state.MachineID != "" && !isValidMachineID(state.MachineID) {
		return nil, newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state has an invalid machine_id.", errMetadataInvalid)
	}
	if state.Entries == nil {
		state.Entries = map[string]syncStateEntry{}
	}
	if err := validateBWSSyncState(&state); err != nil {
		return nil, newCLIError(exitCodeMetadataInvalid, "Encrypted BWS sync state is semantically invalid.", err)
	}
	return &state, nil
}

func saveBWSSyncState(config map[string]string, state *bwsSyncState) error {
	if config == nil {
		return errors.New("config map is nil")
	}
	if state == nil {
		return errors.New("BWS sync state is nil")
	}
	if state.Format == 0 {
		state.Format = 1
	}
	if state.Format != 1 {
		return fmt.Errorf("unsupported BWS sync state format %d", state.Format)
	}
	if state.MachineID != "" && !isValidMachineID(state.MachineID) {
		return errors.New("BWS sync state machine_id is invalid")
	}
	if state.Entries == nil {
		state.Entries = map[string]syncStateEntry{}
	}
	if err := validateBWSSyncState(state); err != nil {
		return fmt.Errorf("validate BWS sync state: %w", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode BWS sync state: %w", err)
	}
	config[configKeyBWSSyncState] = string(encoded)
	return nil
}

func emptyBWSSyncState() *bwsSyncState {
	return &bwsSyncState{Format: 1, Entries: map[string]syncStateEntry{}}
}

func validateBWSSyncState(state *bwsSyncState) error {
	if state == nil {
		return errors.New("state is nil")
	}
	if len(state.Entries) > 0 {
		if !isValidMachineID(state.MachineID) {
			return errors.New("machine_id is required when entries are present")
		}
		if strings.TrimSpace(state.ProjectID) == "" {
			return errors.New("project_id is required when entries are present")
		}
	}
	secretNamesByID := make(map[string]string, len(state.Entries))
	refs := make([]scopeRef, 0, len(state.Entries))
	for mapName, entry := range state.Entries {
		if err := validateBWSSyncStateEntry(state.MachineID, mapName, entry); err != nil {
			return fmt.Errorf("entry %q: %w", mapName, err)
		}
		if previousName, exists := secretNamesByID[entry.SecretID]; exists {
			return fmt.Errorf("entries %q and %q use the same secret_id", previousName, mapName)
		}
		secretNamesByID[entry.SecretID] = mapName
		refs = append(refs, scopeRef{profile: entry.Profile, kind: entry.Scope, path: entry.Path})
	}
	if err := detectScopeHashCollisions(refs); err != nil {
		return fmt.Errorf("state scope collision: %w", err)
	}
	return nil
}

func validateBWSSyncStateEntry(machineID, mapName string, entry syncStateEntry) error {
	if mapName == "" || entry.Name != mapName {
		return errors.New("map key and name must be identical and non-empty")
	}
	if strings.TrimSpace(entry.SecretID) == "" {
		return errors.New("secret_id is required")
	}
	if strings.TrimSpace(entry.RevisionDate) == "" {
		return errors.New("revision_date is required")
	}
	if !isLowerHex(entry.ValueSHA256, 64) {
		return errors.New("value_sha256 must be 64 lowercase hex characters")
	}
	if err := validateEnvKey(entry.Key); err != nil {
		return fmt.Errorf("key is invalid: %w", err)
	}
	if entry.Key == sharedKeyBWSAccessToken {
		return errors.New("reserved BWS access-token key must not appear in sync state")
	}
	ref, err := normalizeScopeRef(scopeRef{profile: entry.Profile, kind: entry.Scope, path: entry.Path})
	if err != nil {
		return fmt.Errorf("scope is invalid: %w", err)
	}
	if ref.profile != entry.Profile || ref.kind != entry.Scope || ref.path != entry.Path {
		return errors.New("scope fields are not normalized")
	}
	if !isValidMachineID(machineID) {
		return errors.New("state machine_id is invalid")
	}
	if buildBWSSecretName(machineID, ref, entry.Key) != entry.Name {
		return errors.New("name does not match machine_id, scope, and key")
	}
	return nil
}
