package kinko

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSyncPayloadAllowsOnlySecretEntryV1(t *testing.T) {
	if err := validateSyncPayload(syncPayloadSecretEntryV1); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []syncPayloadKind{"", "folder-vault", "config", "token", "sync-state", "checkpoint", "machine-metadata", "folder-registration", "bootstrap-path"} {
		if err := validateSyncPayload(payload); !errors.Is(err, errUnsupportedSyncPayload) {
			t.Fatalf("payload %q error=%v", payload, err)
		}
	}
}

func TestValidateBWSMutationRequestLimit(t *testing.T) {
	request := bwsMutationRequest{ProjectID: "project", Name: "name", Value: strings.Repeat("v", maxBWSSecretValueBytes)}
	if err := validateBWSMutationRequest(request, false); err != nil {
		t.Fatal(err)
	}
	request.Value += "v"
	if err := validateBWSMutationRequest(request, false); !errors.Is(err, errBWSValueTooLarge) {
		t.Fatalf("oversized value error=%v", err)
	}
	request.Value = strings.Repeat("é", maxBWSSecretValueBytes/2+1)
	if err := validateBWSMutationRequest(request, false); !errors.Is(err, errBWSValueTooLarge) {
		t.Fatalf("UTF-8 byte limit error=%v", err)
	}
}

func TestRequireSyncCapabilitiesFailsClosed(t *testing.T) {
	provider := &stubSyncProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true}}
	if err := requireSyncCapabilities(provider, syncCapabilityRead); err != nil {
		t.Fatal(err)
	}
	if err := requireSyncCapabilities(provider, syncCapabilityValueSafeMutation); !errors.Is(err, errSyncCapabilityUnavailable) {
		t.Fatalf("missing capability error=%v", err)
	}
	if err := requireSyncCapabilities(nil, syncCapabilityRead); !errors.Is(err, errSyncCapabilityUnavailable) {
		t.Fatalf("nil provider error=%v", err)
	}
}
