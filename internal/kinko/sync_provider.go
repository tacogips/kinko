package kinko

import (
	"context"
	"errors"
	"fmt"
)

type syncPayloadKind string

const syncPayloadSecretEntryV1 syncPayloadKind = "secret-entry/v1"

type syncCapability string

const (
	syncCapabilityRead              syncCapability = "read"
	syncCapabilityDelete            syncCapability = "delete"
	syncCapabilityValueSafeMutation syncCapability = "value-safe-mutation"

	maxBWSSecretValueBytes = 256 * 1024
)

var (
	errUnsupportedSyncPayload    = errors.New("unsupported sync payload")
	errSyncCapabilityUnavailable = errors.New("sync provider capability unavailable")
	errBWSValueTooLarge          = errors.New("BWS secret value exceeds provider limit")
	errBWSSyncSecretNotFound     = errors.New("BWS sync secret not found")
)

type syncProvider interface {
	Capabilities() map[syncCapability]bool
	ListProjects(context.Context) ([]bwsProject, error)
	ListSecrets(context.Context, string) ([]bwsSecret, error)
	GetSecret(context.Context, string) (bwsSecret, error)
	CreateSecret(context.Context, bwsMutationRequest) (bwsSecret, error)
	UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error)
	DeleteSecret(context.Context, string) error
}

type bwsMutationRequest struct {
	ProjectID string
	SecretID  string
	Name      string
	Value     string
	Note      string
}

func validateSyncPayload(kind syncPayloadKind) error {
	if kind != syncPayloadSecretEntryV1 {
		return fmt.Errorf("%w: %q", errUnsupportedSyncPayload, kind)
	}
	return nil
}

func requireSyncCapabilities(provider syncProvider, capabilities ...syncCapability) error {
	if provider == nil {
		return fmt.Errorf("%w: provider is not configured", errSyncCapabilityUnavailable)
	}
	available := provider.Capabilities()
	for _, capability := range capabilities {
		if !available[capability] {
			return fmt.Errorf("%w: %s", errSyncCapabilityUnavailable, capability)
		}
	}
	return nil
}

func validateBWSMutationRequest(request bwsMutationRequest, update bool) error {
	if request.ProjectID == "" {
		return errors.New("BWS mutation project id is required")
	}
	if update && request.SecretID == "" {
		return errors.New("BWS update secret id is required")
	}
	if request.Name == "" {
		return errors.New("BWS mutation name is required")
	}
	if len([]byte(request.Value)) > maxBWSSecretValueBytes {
		return errBWSValueTooLarge
	}
	return nil
}
