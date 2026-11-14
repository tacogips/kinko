package kinko

import (
	"context"
	"errors"
	"fmt"
)

type bwsTransportMode string

const (
	bwsTransportAuto      bwsTransportMode = "auto"
	bwsTransportCLILegacy bwsTransportMode = "cli-legacy"
)

var errBWSSecureMutationUnavailable = errors.New("value-safe BWS mutation transport is unavailable")

// validateBWSTransportSelection checks a requested transport mode and its
// argv acknowledgement independent of provider construction, so CLI preflight
// can reject bad flag combinations before any provider is built.
func validateBWSTransportSelection(mode bwsTransportMode, allowSecretArgv bool) error {
	switch mode {
	case "", bwsTransportAuto:
		return nil
	case bwsTransportCLILegacy:
		if !allowSecretArgv {
			return fmt.Errorf("%w: cli-legacy also requires --allow-secret-argv", errBWSSecureMutationUnavailable)
		}
		return nil
	default:
		return fmt.Errorf("unsupported BWS transport mode %q", mode)
	}
}

func selectBWSTransport(mode bwsTransportMode, allowSecretArgv bool, secureProvider, cliProvider syncProvider) (syncProvider, error) {
	if err := validateBWSTransportSelection(mode, allowSecretArgv); err != nil {
		return nil, err
	}
	if mode == bwsTransportCLILegacy {
		if cliProvider == nil {
			return nil, errors.New("legacy BWS CLI transport is unavailable")
		}
		// The operator has acknowledged both --bws-transport=cli-legacy and
		// --allow-secret-argv, accepting argv-exposed mutation in place of a
		// value-safe transport; grant the mutation capability accordingly.
		// The underlying adapter still enforces its own version gate and
		// emits its one-time argv warning.
		return legacyArgvBWSProvider{syncProvider: cliProvider}, nil
	}
	if secureProvider != nil && secureProvider.Capabilities()[syncCapabilityValueSafeMutation] {
		return secureProvider, nil
	}
	if cliProvider != nil {
		return autoBWSReadProvider{syncProvider: cliProvider}, nil
	}
	if secureProvider != nil {
		return autoBWSReadProvider{syncProvider: secureProvider}, nil
	}
	return nil, errBWSSecureMutationUnavailable
}

// legacyArgvBWSProvider wraps the legacy CLI adapter selected under
// cli-legacy transport so it advertises value-safe-mutation capability. Auto
// mode never uses this wrapper: it must keep stripping mutation capability
// from a CLI-only provider and never fall back to argv-exposed mutation.
type legacyArgvBWSProvider struct {
	syncProvider
}

func (provider legacyArgvBWSProvider) Capabilities() map[syncCapability]bool {
	capabilities := map[syncCapability]bool{syncCapabilityValueSafeMutation: true}
	for capability, available := range provider.syncProvider.Capabilities() {
		capabilities[capability] = available
	}
	return capabilities
}

type autoBWSReadProvider struct {
	syncProvider
}

func (provider autoBWSReadProvider) Capabilities() map[syncCapability]bool {
	capabilities := map[syncCapability]bool{}
	for capability, available := range provider.syncProvider.Capabilities() {
		if capability != syncCapabilityValueSafeMutation {
			capabilities[capability] = available
		}
	}
	return capabilities
}

func (autoBWSReadProvider) CreateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, errBWSSecureMutationUnavailable
}

func (autoBWSReadProvider) UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, errBWSSecureMutationUnavailable
}
