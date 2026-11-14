# BWS Sync Provider and Configuration Implementation Plan

**Status**: Completed
**Design Reference**: `design-docs/specs/design-bws-sync.md#bws-configuration-diagnostics-and-version-gates`, `#secure-transport-and-dependency-boundary`
**Created**: 2026-08-05
**Last Updated**: 2026-08-05

## Related Plans

- **Previous**: `bws-sync-completion-file-splits.md`
- **Next**: `bws-sync-completion-planning.md`, `bws-sync-completion-checkpoint-execution.md`
- **Depends On**: `bws-sync-completion-file-splits.md`

## Design Reference and Scope

Introduce typed provider/payload capabilities, secure endpoint/profile resolution, isolated BWS 2.0 CLI control/read operations, exact version gates, and the value-safe mutation boundary. The official SDK adapter remains build/license gated; default artifacts must not gain an unapproved SDK dependency.

## Modules and Types

### 1. Provider contract

#### `internal/kinko/sync_provider.go`

```go
type syncPayloadKind string
const syncPayloadSecretEntryV1 syncPayloadKind = "secret-entry/v1"

type syncCapability string
const (
	syncCapabilityRead syncCapability = "read"
	syncCapabilityDelete syncCapability = "delete"
	syncCapabilityValueSafeMutation syncCapability = "value-safe-mutation"
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

type bwsMutationRequest struct { ProjectID, SecretID, Name, Value, Note string }
```

- [x] Reject unknown payload kinds and missing capabilities before mutation planning.
- [x] Enforce the 256 KiB/provider value limit before provider access.
- [x] Permanently exclude tokens, sync state/checkpoints, machine metadata, folder registrations, bootstrap paths, folder-vault, and config payloads.

### 2. Secure BWS configuration

#### `internal/kinko/bws_config.go`

```go
type bwsEndpointSet struct { BaseURL, APIURL, IdentityURL *url.URL }
type bwsRuntimeConfig struct {
	ConfigFile, Profile, OrganizationID, ProjectID string
	Endpoints bwsEndpointSet
	ProviderIdentity string
}
type bwsConfigOptions struct { ConfigFile, Profile, ServerURL string; AllowTestLoopback bool }

func resolveBWSRuntimeConfig(bwsConfigOptions, map[string]string, func(string) string) (bwsRuntimeConfig, error)
func canonicalizeBWSEndpoints(bwsEndpointSet, bool) (bwsEndpointSet, error)
func deriveBWSProviderIdentity(bwsEndpointSet, string, string) string
```

- [x] Implement flag > `KINKO_*` > encrypted config > BWS default/profile precedence.
- [x] Ignore parent `BWS_CONFIG_FILE`, `BWS_PROFILE`, and `BWS_SERVER_URL`.
- [x] Open config once, no final symlink; require current-user regular 0600/owner-safe file; reject duplicate keys.
- [x] Pin canonical HTTPS endpoints; allow only explicit test loopback.

### 3. CLI adapter and transport selection

#### `internal/kinko/bws_cli_adapter.go`, `internal/kinko/bws_transport.go`

```go
type bwsTransportMode string
const (
	bwsTransportAuto bwsTransportMode = "auto"
	bwsTransportCLILegacy bwsTransportMode = "cli-legacy"
)
type bwsVersionGate struct { Version string; MutationAllowed bool }

func newBWSCLIAdapter(string, bwsRuntimeConfig, io.Writer) (syncProvider, error)
func inspectBWSVersion(context.Context, *bwsClient) (bwsVersionGate, error)
func selectBWSTransport(bwsTransportMode, bool, syncProvider, syncProvider) (syncProvider, error)
```

- [x] Isolate CLI calls in a temporary 0700 home/config with pinned endpoints and `state_opt_out=true`.
- [x] Allow mutation only for exact fixture-covered CLI 2.0.0; unknown versions are read-only diagnostics.
- [x] `auto` never falls back for create/update; legacy mutation requires both mode and `--allow-secret-argv` and warns.
- [x] Keep SDK implementation behind an explicit build tag and documented license/CGO/target acceptance gate.
- [x] Release token/payload buffers promptly and treat request/response serialization as sensitive memory in tests.

## Status and Dependencies

| Task | Deliverable | Depends On | Parallelizable | Status |
|---|---|---|---|---|
| PROVIDER-001 | Contract/capabilities | split | Yes | COMPLETED |
| PROVIDER-002 | Config/endpoints | split | Yes | COMPLETED |
| PROVIDER-003 | CLI adapter/version fixtures | 001, 002 | No | COMPLETED |
| PROVIDER-004 | Transport selector/SDK gate | 001 | Yes | COMPLETED |

## Testing Requirements

- [x] Fixtures cover BWS 2.0.0 and reject other exact versions for mutation.
- [x] Config ownership, symlink, mode, duplicate-key, canonicalization, and TOCTOU pinning tests.
- [x] Canary values are absent from argv/env/output/errors in secure mode; legacy mode proves both acknowledgements.
- [x] Default builds and release targets contain no SDK dependency or CGO requirement.

## Completion Criteria

- [x] Provider/config contracts are usable by planners and doctor.
- [x] Secure transport fails closed and provider errors are redacted after classification.
- [x] All tests/build/vet/race checks pass; Go files respect the 1,000-line limit.

## Progress Log

### Session: 2026-08-05
**Tasks Completed**: Plan created and reviewed.  
**Tasks In Progress**: None.  
**Blockers**: SDK adapter remains intentionally gated by license and target acceptance.

### Session: 2026-08-05 (implementation)
**Tasks Completed**: PROVIDER-001 through PROVIDER-004. Added the typed provider boundary, payload/capability/value validation, secure configuration and canonical endpoint pinning, isolated CLI adapter and exact 2.0.0 gate, fail-closed transport selection, and dependency-free SDK build gates.  
**Verification**: Focused provider/config/adapter tests, formatting, and file-size checks passed. Full-suite/build/vet/race verification is delegated to the centralized verification stage.  
**Blockers**: None. The SDK transport remains intentionally unavailable in both default and tagged builds until its external acceptance gates are satisfied.

### Session: 2026-08-05 (final verification)
**Tasks Completed**: Centralized full tests, full race, build, CGO-disabled build, vet, module verification, redaction audit, and dependency-boundary checks passed.  
**Blockers**: None. The documented SDK acceptance gate remains an intentional product boundary, not unfinished default-build work.
