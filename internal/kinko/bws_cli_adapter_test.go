package kinko

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInspectBWSVersionExactMutationGate(t *testing.T) {
	for _, test := range []struct {
		output string
		allow  bool
		bad    bool
	}{{"bws 2.0.0\n", true, false}, {"2.0.0\n", true, false}, {"bws 2.0.1\n", false, false}, {"bws 0.3.0\n", false, false}, {"bws 2.0.0 extra\n", false, true}, {"\n", false, true}} {
		t.Run(strings.TrimSpace(test.output), func(t *testing.T) {
			client := testBWSClient(func(_ context.Context, _ string, _ []string, arguments ...string) ([]byte, []byte, error) {
				if len(arguments) != 1 || arguments[0] != "--version" {
					t.Fatalf("args=%v", arguments)
				}
				return []byte(test.output), nil, nil
			})
			gate, err := inspectBWSVersion(context.Background(), client)
			if test.bad {
				if err == nil {
					t.Fatal("malformed version accepted")
				}
				return
			}
			if err != nil || gate.MutationAllowed != test.allow {
				t.Fatalf("gate=%+v err=%v", gate, err)
			}
		})
	}
}

func TestInspectBWSVersionRedactsTokenAndIsolatedEnvironment(t *testing.T) {
	token := strings.Repeat("version-canary-", 2)
	client := testBWSClient(func(_ context.Context, _ string, environment []string, _ ...string) ([]byte, []byte, error) {
		if joined := strings.Join(environment, "\n"); !strings.Contains(joined, envBWSAccessToken+"="+token) || strings.Contains(joined, "PARENT_SECRET") {
			t.Fatalf("environment=%q", joined)
		}
		return nil, []byte("rejected " + token), errors.New("failure " + token)
	})
	client.token = token
	client.environmentBuilder = func(token string) ([]string, func(), error) {
		return []string{envBWSAccessToken + "=" + token}, func() {}, nil
	}
	_, err := inspectBWSVersion(context.Background(), client)
	if !errors.Is(err, errBWSCommandFailed) || strings.Contains(err.Error(), token) {
		t.Fatalf("version error=%v", err)
	}
}

func TestIsolatedBWSEnvironmentPinsConfigAndCleansUp(t *testing.T) {
	t.Setenv("PARENT_SECRET", strings.Repeat("parent-canary-", 2))
	t.Setenv("BWS_SERVER_URL", "https://parent.invalid")
	config, err := resolveBWSRuntimeConfig(bwsConfigOptions{ServerURL: "https://pinned.example"}, nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	environment, cleanup, err := isolatedBWSEnvironmentBuilder(config)(strings.Repeat("token-canary-", 2))
	if err != nil {
		t.Fatal(err)
	}
	values := envSliceToMap(environment)
	if _, ok := values["PARENT_SECRET"]; ok {
		t.Fatal("parent secret leaked")
	}
	if _, ok := values["BWS_SERVER_URL"]; ok {
		t.Fatal("parent endpoint leaked")
	}
	configPath := values["BWS_CONFIG_FILE"]
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%o", info.Mode().Perm())
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "[profiles.default]") || !strings.Contains(string(contents), "https://pinned.example/api") || !strings.Contains(string(contents), `state_opt_out = "true"`) {
		t.Fatalf("isolated config=%q", contents)
	}
	home := values["HOME"]
	cleanup()
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated home cleanup error=%v", err)
	}
}

func TestIsolatedBWSConfigContentsMatchesBWSCLIExpectations(t *testing.T) {
	base, err := url.Parse("https://vault.bitwarden.com")
	if err != nil {
		t.Fatal(err)
	}
	api, err := url.Parse("https://vault.bitwarden.com/api")
	if err != nil {
		t.Fatal(err)
	}
	contents := isolatedBWSConfigContents(bwsRuntimeConfig{Endpoints: bwsEndpointSet{BaseURL: base, APIURL: api}})
	if !strings.HasPrefix(contents, "[profiles.default]\n") {
		t.Fatalf("section header=%q", contents)
	}
	if !strings.Contains(contents, `server_base = "https://vault.bitwarden.com"`) {
		t.Fatalf("server_base missing: %q", contents)
	}
	if !strings.Contains(contents, `server_api = "https://vault.bitwarden.com/api"`) {
		t.Fatalf("server_api missing: %q", contents)
	}
	if strings.Contains(contents, "server_identity") {
		t.Fatalf("empty endpoint emitted: %q", contents)
	}
	if !strings.Contains(contents, `state_opt_out = "true"`) {
		t.Fatalf("state_opt_out missing: %q", contents)
	}
}

func TestBWSCLIAdapterMutationValidationGateAndWarning(t *testing.T) {
	canary := strings.Repeat("payload-canary-", 2)
	var arguments []string
	client := testBWSClient(func(_ context.Context, _ string, environment []string, args ...string) ([]byte, []byte, error) {
		arguments = append([]string(nil), args...)
		if strings.Contains(strings.Join(environment, "\n"), canary) {
			t.Fatal("payload entered environment")
		}
		return []byte(`{"id":"id","projectId":"project","key":"name","value":"` + canary + `","note":"note","revisionDate":"revision"}`), nil, nil
	})
	var stderr bytes.Buffer
	adapter := &bwsCLIAdapter{client: client, gate: bwsVersionGate{Version: "2.0.0", MutationAllowed: true}, stderr: &stderr}
	_, err := adapter.CreateSecret(context.Background(), bwsMutationRequest{ProjectID: "project", Name: "name", Value: canary, Note: "note"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(arguments, "\n"), canary) {
		t.Fatal("legacy fixture did not prove argv exposure")
	}
	if strings.Contains(stderr.String(), canary) || !strings.Contains(stderr.String(), "WARNING") {
		t.Fatalf("stderr=%q", stderr.String())
	}

	adapter.gate = bwsVersionGate{Version: "2.0.1"}
	arguments = nil
	_, err = adapter.CreateSecret(context.Background(), bwsMutationRequest{ProjectID: "project", Name: "name", Value: canary})
	if !errors.Is(err, errBWSMutationVersionUnsupported) || arguments != nil {
		t.Fatalf("gate err=%v args=%v", err, arguments)
	}

	adapter.gate = bwsVersionGate{Version: "2.0.0", MutationAllowed: true}
	_, err = adapter.CreateSecret(context.Background(), bwsMutationRequest{ProjectID: "project", Name: "name", Value: strings.Repeat("x", maxBWSSecretValueBytes+1)})
	if !errors.Is(err, errBWSValueTooLarge) {
		t.Fatalf("limit err=%v", err)
	}
}

func TestSelectBWSTransportRequiresBothLegacyAcknowledgements(t *testing.T) {
	cli := &stubSyncProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityDelete: true}}
	auto, err := selectBWSTransport(bwsTransportAuto, false, nil, cli)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auto.CreateSecret(context.Background(), bwsMutationRequest{}); !errors.Is(err, errBWSSecureMutationUnavailable) {
		t.Fatalf("auto mutation error=%v", err)
	}
	if _, err := selectBWSTransport(bwsTransportCLILegacy, false, nil, cli); err == nil {
		t.Fatal("legacy mode accepted without argv acknowledgement")
	}
	selected, err := selectBWSTransport(bwsTransportCLILegacy, true, nil, cli)
	if err != nil {
		t.Fatalf("legacy selection err=%v", err)
	}
	legacyCapabilities := selected.Capabilities()
	if !legacyCapabilities[syncCapabilityValueSafeMutation] || !legacyCapabilities[syncCapabilityRead] || !legacyCapabilities[syncCapabilityDelete] {
		t.Fatalf("legacy selection capabilities=%v", legacyCapabilities)
	}
	secure := &stubSyncProvider{capabilities: map[syncCapability]bool{syncCapabilityRead: true, syncCapabilityValueSafeMutation: true}}
	selected, err = selectBWSTransport(bwsTransportAuto, false, secure, cli)
	if err != nil || selected != secure {
		t.Fatalf("secure selection=%T err=%v", selected, err)
	}
}

func testBWSClient(runner bwsRunner) *bwsClient {
	return &bwsClient{binPath: "bws-stub", token: strings.Repeat("token-", 4), timeout: time.Second, runner: runner}
}

type stubSyncProvider struct{ capabilities map[syncCapability]bool }

func (provider *stubSyncProvider) Capabilities() map[syncCapability]bool {
	return provider.capabilities
}
func (*stubSyncProvider) ListProjects(context.Context) ([]bwsProject, error)       { return nil, nil }
func (*stubSyncProvider) ListSecrets(context.Context, string) ([]bwsSecret, error) { return nil, nil }
func (*stubSyncProvider) GetSecret(context.Context, string) (bwsSecret, error) {
	return bwsSecret{}, nil
}
func (*stubSyncProvider) CreateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, nil
}
func (*stubSyncProvider) UpdateSecret(context.Context, bwsMutationRequest) (bwsSecret, error) {
	return bwsSecret{}, nil
}
func (*stubSyncProvider) DeleteSecret(context.Context, string) error { return nil }
