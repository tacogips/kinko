package kinko

import (
	"context"
	"errors"
	"testing"
)

func TestValidateBWSTransportSelection(t *testing.T) {
	tests := []struct {
		name            string
		mode            bwsTransportMode
		allowSecretArgv bool
		wantErr         bool
	}{
		{name: "empty mode defaults to auto", mode: "", wantErr: false},
		{name: "auto ok", mode: bwsTransportAuto, wantErr: false},
		{name: "cli-legacy with acknowledgement ok", mode: bwsTransportCLILegacy, allowSecretArgv: true, wantErr: false},
		{name: "cli-legacy without acknowledgement fails", mode: bwsTransportCLILegacy, allowSecretArgv: false, wantErr: true},
		{name: "unsupported mode fails", mode: bwsTransportMode("bogus"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBWSTransportSelection(test.mode, test.allowSecretArgv)
			if test.wantErr && err == nil {
				t.Fatalf("mode=%q allowSecretArgv=%v: expected error, got nil", test.mode, test.allowSecretArgv)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("mode=%q allowSecretArgv=%v: unexpected error: %v", test.mode, test.allowSecretArgv, err)
			}
		})
	}
}

func TestSelectBWSTransportCLILegacyGrantsValueSafeMutation(t *testing.T) {
	cli := &stubSyncProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true}}
	provider, err := selectBWSTransport(bwsTransportCLILegacy, true, nil, cli)
	if err != nil {
		t.Fatalf("cli-legacy selection err=%v", err)
	}
	capabilities := provider.Capabilities()
	for _, capability := range []syncCapability{syncCapabilityValueSafeMutation, syncCapabilityRead, syncCapabilityDelete} {
		if !capabilities[capability] {
			t.Fatalf("cli-legacy provider missing capability %q: %v", capability, capabilities)
		}
	}
	if err := requireSyncCapabilities(provider, syncCapabilityRead, syncCapabilityDelete, syncCapabilityValueSafeMutation); err != nil {
		t.Fatalf("cli-legacy provider failed capability requirement: %v", err)
	}
}

func TestSelectBWSTransportAutoStripsValueSafeMutationFromCLIOnlyProvider(t *testing.T) {
	cli := &stubSyncProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true}}
	provider, err := selectBWSTransport(bwsTransportAuto, false, nil, cli)
	if err != nil {
		t.Fatalf("auto selection err=%v", err)
	}
	if capabilities := provider.Capabilities(); capabilities[syncCapabilityValueSafeMutation] {
		t.Fatalf("auto mode advertised value-safe-mutation from CLI-only provider: %v", capabilities)
	}
	if _, err := provider.CreateSecret(context.Background(), bwsMutationRequest{}); !errors.Is(err, errBWSSecureMutationUnavailable) {
		t.Fatalf("auto mode create error=%v", err)
	}
	if _, err := provider.UpdateSecret(context.Background(), bwsMutationRequest{}); !errors.Is(err, errBWSSecureMutationUnavailable) {
		t.Fatalf("auto mode update error=%v", err)
	}
}
