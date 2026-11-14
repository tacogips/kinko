package kinko

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var errBWSMutationVersionUnsupported = errors.New("BWS CLI version is not approved for mutation")

type bwsVersionGate struct {
	Version         string
	MutationAllowed bool
}

type bwsCLIAdapter struct {
	client   *bwsClient
	gate     bwsVersionGate
	stderr   io.Writer
	warnOnce sync.Once
}

func newBWSCLIAdapter(token string, config bwsRuntimeConfig, stderr io.Writer) (syncProvider, error) {
	client, err := newBWSClient(token, stderr)
	if err != nil {
		return nil, err
	}
	client.environmentBuilder = isolatedBWSEnvironmentBuilder(config)
	gate, err := inspectBWSVersion(context.Background(), client)
	if err != nil {
		return nil, err
	}
	return &bwsCLIAdapter{client: client, gate: gate, stderr: stderr}, nil
}

func (adapter *bwsCLIAdapter) Capabilities() map[syncCapability]bool {
	return map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true}
}

func (adapter *bwsCLIAdapter) ListProjects(ctx context.Context) ([]bwsProject, error) {
	return adapter.client.listProjects(ctx)
}

func (adapter *bwsCLIAdapter) ListSecrets(ctx context.Context, projectID string) ([]bwsSecret, error) {
	return adapter.client.listSecrets(ctx, projectID)
}

func (adapter *bwsCLIAdapter) GetSecret(ctx context.Context, secretID string) (bwsSecret, error) {
	return adapter.client.getSecret(ctx, secretID)
}

func (adapter *bwsCLIAdapter) CreateSecret(ctx context.Context, request bwsMutationRequest) (bwsSecret, error) {
	if err := validateBWSMutationRequest(request, false); err != nil {
		return bwsSecret{}, err
	}
	if err := adapter.requireLegacyMutation(); err != nil {
		return bwsSecret{}, err
	}
	return adapter.client.createSecret(ctx, request.ProjectID, request.Name, request.Value, request.Note)
}

func (adapter *bwsCLIAdapter) UpdateSecret(ctx context.Context, request bwsMutationRequest) (bwsSecret, error) {
	if err := validateBWSMutationRequest(request, true); err != nil {
		return bwsSecret{}, err
	}
	if err := adapter.requireLegacyMutation(); err != nil {
		return bwsSecret{}, err
	}
	return adapter.client.editSecret(ctx, request.SecretID, request.ProjectID, request.Name, request.Value, request.Note)
}

func (adapter *bwsCLIAdapter) DeleteSecret(ctx context.Context, secretID string) error {
	if secretID == "" {
		return errors.New("BWS delete secret id is required")
	}
	return adapter.client.deleteSecrets(ctx, []string{secretID})
}

func (adapter *bwsCLIAdapter) requireLegacyMutation() error {
	if !adapter.gate.MutationAllowed {
		return fmt.Errorf("%w: inspected %q; exact 2.0.0 is required", errBWSMutationVersionUnsupported, adapter.gate.Version)
	}
	adapter.warnOnce.Do(func() {
		if adapter.stderr != nil {
			_, _ = fmt.Fprintln(adapter.stderr, "WARNING: legacy BWS CLI mutation exposes secret values in the child process argument list.")
		}
	})
	return nil
}

func inspectBWSVersion(ctx context.Context, client *bwsClient) (bwsVersionGate, error) {
	if client == nil || client.runner == nil || client.binPath == "" || client.timeout <= 0 {
		return bwsVersionGate{}, errors.New("BWS client is not initialized")
	}
	callContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	environment, cleanup, err := client.buildEnvironment()
	if err != nil {
		return bwsVersionGate{}, err
	}
	defer cleanup()
	stdout, stderr, err := client.runner(callContext, client.binPath, environment, "--version")
	if err != nil {
		if errors.Is(callContext.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return bwsVersionGate{}, fmt.Errorf("%w after %s", errBWSTimeout, client.timeout)
		}
		detail := strings.TrimSpace(redactBWSValues(string(stderr), client.token))
		if detail == "" {
			detail = redactBWSValues(err.Error(), client.token)
		}
		return bwsVersionGate{}, fmt.Errorf("%w while inspecting version: %s", errBWSCommandFailed, detail)
	}
	raw := strings.TrimSpace(string(stdout))
	version := strings.TrimSpace(strings.TrimPrefix(raw, "bws"))
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		return bwsVersionGate{}, fmt.Errorf("unrecognized BWS CLI version output %q", raw)
	}
	return bwsVersionGate{Version: version, MutationAllowed: version == "2.0.0"}, nil
}

func isolatedBWSEnvironmentBuilder(config bwsRuntimeConfig) bwsEnvironmentBuilder {
	return func(token string) ([]string, func(), error) {
		home, err := os.MkdirTemp("", "kinko-bws-")
		if err != nil {
			return nil, nil, err
		}
		cleanup := func() { _ = os.RemoveAll(home) }
		if err := os.Chmod(home, 0o700); err != nil {
			cleanup()
			return nil, nil, err
		}
		configDir := filepath.Join(home, ".bws")
		if err := os.Mkdir(configDir, 0o700); err != nil {
			cleanup()
			return nil, nil, err
		}
		configPath := filepath.Join(configDir, "config")
		contents := isolatedBWSConfigContents(config)
		if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
			cleanup()
			return nil, nil, err
		}
		environment := []string{
			envBWSAccessToken + "=" + token,
			"HOME=" + home,
			"BWS_CONFIG_FILE=" + configPath,
			"BWS_PROFILE=" + defaultBWSProfile,
		}
		for _, key := range []string{"PATH", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
			if value := os.Getenv(key); value != "" {
				environment = append(environment, key+"="+value)
			}
		}
		return environment, func() {
			for index := range environment {
				environment[index] = ""
			}
			cleanup()
		}, nil
	}
}

func isolatedBWSConfigContents(config bwsRuntimeConfig) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "[profiles.%s]\n", defaultBWSProfile)
	if value := endpointString(config.Endpoints.BaseURL); value != "" {
		fmt.Fprintf(&builder, "server_base = %q\n", value)
	}
	if value := endpointString(config.Endpoints.APIURL); value != "" {
		fmt.Fprintf(&builder, "server_api = %q\n", value)
	}
	if value := endpointString(config.Endpoints.IdentityURL); value != "" {
		fmt.Fprintf(&builder, "server_identity = %q\n", value)
	}
	builder.WriteString("state_opt_out = \"true\"\n")
	return builder.String()
}

var _ syncProvider = (*bwsCLIAdapter)(nil)
