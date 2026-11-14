package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

const syncCheckpointFormat = 1

type syncCheckpointPhase string

const (
	syncCheckpointPrepared  syncCheckpointPhase = "prepared"
	syncCheckpointExecuting syncCheckpointPhase = "executing"
	syncCheckpointComplete  syncCheckpointPhase = "complete"
)

type syncCheckpointResult struct {
	ActionID string `json:"action_id"`
	SecretID string `json:"secret_id,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type syncCheckpoint struct {
	Format           int                    `json:"format"`
	Operation        syncOperation          `json:"operation"`
	ProviderIdentity string                 `json:"provider_identity"`
	SelectorDigest   string                 `json:"selector_digest"`
	PlanDigest       string                 `json:"plan_digest"`
	Actions          []syncPlannedAction    `json:"actions"`
	Phase            syncCheckpointPhase    `json:"phase"`
	Confirmed        []syncCheckpointResult `json:"confirmed"`
}

type syncResumeMode string

const (
	syncResumeAuto    syncResumeMode = "auto"
	syncResumeRequire syncResumeMode = "require"
	syncResumeNever   syncResumeMode = "never"
)

type syncCheckpointStore interface {
	Save(*syncCheckpoint) error
}

type encryptedSyncCheckpointStore struct {
	dataDir string
	dek     []byte
	config  map[string]string
}

func (store encryptedSyncCheckpointStore) Save(checkpoint *syncCheckpoint) error {
	return persistSyncCheckpoint(store.dataDir, store.dek, store.config, checkpoint)
}

type syncCheckpointStoreFunc func(*syncCheckpoint) error

func (save syncCheckpointStoreFunc) Save(checkpoint *syncCheckpoint) error { return save(checkpoint) }

type ambiguousSyncMutationError interface {
	MutationOutcomeAmbiguous() bool
}

func newSyncCheckpoint(plan *syncPlanV2) (*syncCheckpoint, error) {
	if err := validateSyncPlanShape(plan); err != nil {
		return nil, err
	}
	checkpoint := &syncCheckpoint{
		Format: syncCheckpointFormat, Operation: plan.Operation, ProviderIdentity: plan.ProviderIdentity,
		SelectorDigest: plan.SelectorDigest, PlanDigest: plan.PlanDigest,
		Actions: append([]syncPlannedAction(nil), plan.Actions...), Phase: syncCheckpointPrepared,
		Confirmed: []syncCheckpointResult{},
	}
	return checkpoint, nil
}

func decodeSyncCheckpoint(encoded []byte) (syncCheckpoint, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var checkpoint syncCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return syncCheckpoint{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return syncCheckpoint{}, err
	}
	if err := validateSyncCheckpointShape(&checkpoint); err != nil {
		return syncCheckpoint{}, err
	}
	return checkpoint, nil
}

func validateSyncCheckpoint(checkpoint *syncCheckpoint, plan *syncPlanV2) error {
	if err := validateSyncCheckpointShape(checkpoint); err != nil {
		return err
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return err
	}
	if checkpoint.Operation != plan.Operation || checkpoint.ProviderIdentity != plan.ProviderIdentity || checkpoint.SelectorDigest != plan.SelectorDigest || checkpoint.PlanDigest != plan.PlanDigest {
		return errors.New("sync checkpoint does not match the current pinned plan")
	}
	checkpointActions, err := json.Marshal(checkpoint.Actions)
	if err != nil {
		return err
	}
	planActions, err := json.Marshal(plan.Actions)
	if err != nil {
		return err
	}
	if !bytes.Equal(checkpointActions, planActions) {
		return errors.New("sync checkpoint actions differ from the current plan")
	}
	return nil
}

func validateSyncCheckpointShape(checkpoint *syncCheckpoint) error {
	if checkpoint == nil || checkpoint.Format != syncCheckpointFormat {
		return errors.New("sync checkpoint has an unsupported format")
	}
	if err := validateSyncOperation(checkpoint.Operation); err != nil {
		return err
	}
	if !isLowerHex(checkpoint.ProviderIdentity, 64) || !isLowerHex(checkpoint.SelectorDigest, 64) || !isLowerHex(checkpoint.PlanDigest, 64) {
		return errors.New("sync checkpoint digest pins are invalid")
	}
	switch checkpoint.Phase {
	case syncCheckpointPrepared, syncCheckpointExecuting, syncCheckpointComplete:
	default:
		return fmt.Errorf("unsupported sync checkpoint phase %q", checkpoint.Phase)
	}
	actions := make(map[string]struct{}, len(checkpoint.Actions))
	for _, action := range checkpoint.Actions {
		if !isLowerHex(action.ActionID, 64) {
			return errors.New("sync checkpoint contains an invalid action id")
		}
		if _, exists := actions[action.ActionID]; exists {
			return errors.New("sync checkpoint contains duplicate actions")
		}
		actions[action.ActionID] = struct{}{}
	}
	if len(checkpoint.Confirmed) > len(checkpoint.Actions) {
		return errors.New("sync checkpoint confirms more actions than it contains")
	}
	confirmed := map[string]struct{}{}
	for index, result := range checkpoint.Confirmed {
		if _, exists := actions[result.ActionID]; !exists {
			return errors.New("sync checkpoint confirms an unknown action")
		}
		if result.ActionID != checkpoint.Actions[index].ActionID {
			return errors.New("sync checkpoint confirmed actions are not an exact plan prefix")
		}
		if _, exists := confirmed[result.ActionID]; exists {
			return errors.New("sync checkpoint confirms an action more than once")
		}
		confirmed[result.ActionID] = struct{}{}
	}
	if checkpoint.Phase == syncCheckpointPrepared && len(checkpoint.Confirmed) != 0 {
		return errors.New("prepared sync checkpoint cannot contain confirmed actions")
	}
	if checkpoint.Phase == syncCheckpointComplete && len(checkpoint.Confirmed) != len(checkpoint.Actions) {
		return errors.New("complete sync checkpoint must confirm every action")
	}
	return nil
}

func persistSyncCheckpoint(dataDir string, dek []byte, config map[string]string, checkpoint *syncCheckpoint) error {
	if config == nil || checkpoint == nil {
		return errors.New("sync checkpoint persistence is not initialized")
	}
	if err := validateSyncCheckpointShape(checkpoint); err != nil {
		return err
	}
	envelope := syncStateEnvelope{Format: bwsSyncStateFormatV2, Raw: map[string]json.RawMessage{"format": json.RawMessage("2")}}
	desired := &bwsSyncStateV2{Format: bwsSyncStateFormatV2, Entries: map[string]syncStateEntryV2{}, Ownership: map[string]syncOwnershipRecord{}, Checkpoint: checkpoint}
	if encoded := strings.TrimSpace(config[configKeyBWSSyncState]); encoded != "" {
		var err error
		envelope, err = decodeBWSSyncState(encoded)
		if err != nil {
			return err
		}
		if envelope.Format == bwsSyncStateFormatV2 {
			existing, decodeErr := decodeBWSSyncStateV2(envelope)
			if decodeErr != nil {
				return decodeErr
			}
			desired.Entries, desired.Ownership, desired.Raw = existing.Entries, existing.Ownership, existing.Raw
		}
	}
	encoded, err := mergeSelectedBWSSyncState(envelope, desired, map[string]struct{}{})
	if err != nil {
		return err
	}
	nextConfig := make(map[string]string, len(config)+1)
	for key, value := range config {
		nextConfig[key] = value
	}
	nextConfig[configKeyBWSSyncState] = encoded
	if err := saveConfig(dataDir, dek, nextConfig); err != nil {
		return fmt.Errorf("persist encrypted sync checkpoint: %w", err)
	}
	config[configKeyBWSSyncState] = encoded
	return nil
}

func reconcileAmbiguousMutation(ctx context.Context, provider syncProvider, action syncPlannedAction) (syncCheckpointResult, bool, error) {
	if provider == nil {
		return syncCheckpointResult{}, false, errors.New("sync provider is nil")
	}
	switch action.Kind {
	case syncActionCreate:
		secrets, err := provider.ListSecrets(ctx, action.Identity.ProjectID)
		if err != nil {
			return syncCheckpointResult{}, false, fmt.Errorf("list secrets while reconciling create: %w", err)
		}
		matches := make([]bwsSecret, 0, 1)
		for _, secret := range secrets {
			if secret.ProjectID == action.Identity.ProjectID && secret.Key == action.IntendedName && valueSHA256(secret.Note) == action.IntendedNoteSHA256 && valueSHA256(secret.Value) == action.IntendedValueSHA256 {
				matches = append(matches, secret)
			}
		}
		if len(matches) == 0 {
			return syncCheckpointResult{}, true, nil
		}
		if len(matches) != 1 {
			return syncCheckpointResult{}, false, errors.New("ambiguous create reconciliation found multiple exact matches")
		}
		return checkpointResult(action, matches[0]), false, nil
	case syncActionUpdate:
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return syncCheckpointResult{}, false, fmt.Errorf("get secret while reconciling update: %w", err)
		}
		if secret.ProjectID == action.Precondition.ProjectID && secret.ID == action.Precondition.SecretID && secret.Key == action.IntendedName && valueSHA256(secret.Note) == action.IntendedNoteSHA256 && valueSHA256(secret.Value) == action.IntendedValueSHA256 {
			return checkpointResult(action, secret), false, nil
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err == nil {
			return syncCheckpointResult{}, true, nil
		}
		return syncCheckpointResult{}, false, errors.New("ambiguous update reconciliation found changed remote content")
	case syncActionDelete:
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if errors.Is(err, errBWSSyncSecretNotFound) {
			return syncCheckpointResult{ActionID: action.ActionID, SecretID: action.Precondition.SecretID}, false, nil
		}
		if err != nil {
			return syncCheckpointResult{}, false, fmt.Errorf("get secret while reconciling delete: %w", err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err == nil {
			return syncCheckpointResult{}, true, nil
		}
		return syncCheckpointResult{}, false, errors.New("ambiguous delete reconciliation found changed remote content")
	default:
		return syncCheckpointResult{}, false, errors.New("only remote mutations can be reconciled")
	}
}

func checkpointResult(action syncPlannedAction, secret bwsSecret) syncCheckpointResult {
	return syncCheckpointResult{ActionID: action.ActionID, SecretID: secret.ID, Revision: secret.RevisionDate}
}

func mutationOutcomeMayBeAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	var classified ambiguousSyncMutationError
	if errors.As(err, &classified) {
		return classified.MutationOutcomeAmbiguous()
	}
	if errors.Is(err, errBWSTimeout) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errBWSInvalidJSON) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}
