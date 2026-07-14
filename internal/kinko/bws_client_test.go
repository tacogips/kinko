package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResolveBWSAccessToken(t *testing.T) {
	environmentToken := strings.Repeat("e", 16)
	sharedToken := strings.Repeat("s", 16)
	tests := []struct {
		name        string
		environment string
		shared      map[string]string
		want        string
		wantNotice  bool
		wantPolicy  bool
	}{
		{name: "environment only", environment: environmentToken, shared: map[string]string{}, want: environmentToken},
		{name: "shared only", shared: map[string]string{sharedKeyBWSAccessToken: sharedToken}, want: sharedToken},
		{name: "environment shadows shared", environment: environmentToken, shared: map[string]string{sharedKeyBWSAccessToken: sharedToken}, want: environmentToken, wantNotice: true},
		{name: "neither", shared: map[string]string{}, wantPolicy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			getenv := func(key string) string {
				if key == envKinkoBWSAccessToken {
					return test.environment
				}
				return ""
			}
			got, err := resolveBWSAccessToken(getenv, test.shared, &stderr)
			if test.wantPolicy {
				if ExitCode(err) != exitCodePolicyFailed {
					t.Fatalf("exit=%d err=%v", ExitCode(err), err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("token resolution failed: err=%v", err)
			}
			if (stderr.Len() > 0) != test.wantNotice {
				t.Fatalf("notice=%q wantNotice=%v", stderr.String(), test.wantNotice)
			}
			if strings.Contains(stderr.String(), environmentToken) || strings.Contains(stderr.String(), sharedToken) {
				t.Fatal("token leaked in notice")
			}
		})
	}
}

func TestBuildBWSChildEnvIsMinimalAndOverridesParentToken(t *testing.T) {
	parentToken := strings.Repeat("p", 16)
	resolvedToken := strings.Repeat("r", 16)
	t.Setenv(envBWSAccessToken, parentToken)
	t.Setenv("UNRELATED_SECRET", strings.Repeat("u", 16))
	t.Setenv("HOME", "/tmp/test-home")

	environment := buildBWSChildEnv(resolvedToken)
	values := envSliceToMap(environment)
	if values[envBWSAccessToken] != resolvedToken {
		t.Fatal("resolved child token was not injected")
	}
	if strings.Contains(strings.Join(environment, "\n"), parentToken) {
		t.Fatal("parent BWS token leaked into child environment")
	}
	if _, exists := values["UNRELATED_SECRET"]; exists {
		t.Fatal("unrelated parent variable leaked into child environment")
	}
	if values["HOME"] != "/tmp/test-home" {
		t.Fatal("required HOME passthrough missing")
	}
}

func TestBWSClientJSONCallAndArguments(t *testing.T) {
	token := strings.Repeat("t", 16)
	var capturedArguments []string
	var capturedEnvironment []string
	client := &bwsClient{
		binPath: "bws-stub",
		token:   token,
		timeout: time.Second,
		runner: func(_ context.Context, binary string, environment []string, arguments ...string) ([]byte, []byte, error) {
			if binary != "bws-stub" {
				t.Fatalf("binary=%q", binary)
			}
			capturedArguments = append([]string(nil), arguments...)
			capturedEnvironment = append([]string(nil), environment...)
			return []byte(`[{"id":"project-id","name":"project-name"}]`), nil, nil
		},
	}
	projects, err := client.listProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != "project-id" {
		t.Fatalf("projects=%+v", projects)
	}
	wantArguments := []string{"project", "list", "--output", "json", "--color", "no"}
	if !reflect.DeepEqual(capturedArguments, wantArguments) {
		t.Fatalf("arguments=%v want %v", capturedArguments, wantArguments)
	}
	if envSliceToMap(capturedEnvironment)[envBWSAccessToken] != token {
		t.Fatal("runner did not receive resolved token")
	}
}

func TestBWSClientProviderFailureKindsAndRedaction(t *testing.T) {
	token := strings.Repeat("q", 20)
	tests := []struct {
		name    string
		timeout time.Duration
		runner  bwsRunner
		want    error
	}{
		{
			name:    "non-zero",
			timeout: time.Second,
			runner: func(context.Context, string, []string, ...string) ([]byte, []byte, error) {
				return nil, []byte("provider rejected " + token), errors.New("exit status 1 for " + token)
			},
			want: errBWSCommandFailed,
		},
		{
			name:    "bad JSON",
			timeout: time.Second,
			runner: func(context.Context, string, []string, ...string) ([]byte, []byte, error) {
				return []byte("not-json"), nil, nil
			},
			want: errBWSInvalidJSON,
		},
		{
			name:    "timeout",
			timeout: 5 * time.Millisecond,
			runner: func(ctx context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
				<-ctx.Done()
				return nil, nil, ctx.Err()
			},
			want: errBWSTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{binPath: "bws-stub", token: token, timeout: test.timeout, runner: test.runner}
			_, err := client.listProjects(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want wrapped %v", err, test.want)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatal("token leaked in provider error")
			}
		})
	}
}

func TestNewBWSClientMissingBinaryAndParentNotice(t *testing.T) {
	token := strings.Repeat("x", 16)
	t.Setenv(envKinkoBWSBin, "definitely-not-a-real-bws-binary")
	if _, err := newBWSClient(token, &bytes.Buffer{}); !errors.Is(err, errBWSBinaryMissing) {
		t.Fatalf("missing binary err=%v", err)
	}

	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKinkoBWSBin, path)
	t.Setenv(envBWSAccessToken, strings.Repeat("p", 16))
	var stderr bytes.Buffer
	client, err := newBWSClient(token, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if client.binPath == "" || !strings.Contains(stderr.String(), "ignored") {
		t.Fatalf("client=%+v stderr=%q", client, stderr.String())
	}
	if strings.Contains(stderr.String(), token) {
		t.Fatal("token leaked in parent-token notice")
	}
}

func TestBWSClientCreateEditDeleteCalls(t *testing.T) {
	var calls [][]string
	client := &bwsClient{
		binPath: "bws-stub",
		token:   strings.Repeat("z", 16),
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, arguments ...string) ([]byte, []byte, error) {
			calls = append(calls, append([]string(nil), arguments...))
			if len(arguments) >= 2 && arguments[1] == "delete" {
				return []byte("2 secrets deleted successfully."), nil, nil
			}
			response := bwsSecret{
				ID:           "secret-id",
				ProjectID:    "project-id",
				Key:          "KEY",
				Value:        arguments[4],
				Note:         arguments[6],
				RevisionDate: "2026-07-13T00:00:00Z",
			}
			if arguments[1] == "create" {
				response.Value = arguments[3]
			}
			payload, err := json.Marshal(response)
			return payload, nil, err
		},
	}
	if _, err := client.createSecret(context.Background(), "project-id", "KEY", strings.Repeat("v", 8), `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", strings.Repeat("w", 8), `{}`); err != nil {
		t.Fatal(err)
	}
	if err := client.deleteSecrets(context.Background(), []string{"id-1", "id-2"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls=%d", len(calls))
	}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "--output json") || !strings.Contains(joined, "--color no") {
			t.Fatalf("missing required global flags: %v", call)
		}
	}
}

func TestBWSClientCreateAndEditFailuresRedactSecretValues(t *testing.T) {
	token := strings.Repeat("t", 16)
	secretValue := strings.Repeat("sensitive-value-", 3)

	tests := []struct {
		name  string
		value string
		call  func(*bwsClient, string) error
	}{
		{
			name:  "create",
			value: secretValue,
			call: func(client *bwsClient, value string) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", value, `{}`)
				return err
			},
		},
		{
			name:  "edit",
			value: secretValue,
			call: func(client *bwsClient, value string) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", value, `{}`)
				return err
			},
		},
		{
			name:  "create value contains token",
			value: strings.Repeat("value-prefix-", 2) + token + strings.Repeat("-value-suffix", 2),
			call: func(client *bwsClient, value string) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", value, `{}`)
				return err
			},
		},
		{
			name:  "edit value contains token",
			value: strings.Repeat("value-prefix-", 2) + token + strings.Repeat("-value-suffix", 2),
			call: func(client *bwsClient, value string) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", value, `{}`)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{
				binPath: "bws-stub",
				token:   token,
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return nil, []byte("provider echoed " + test.value), errors.New("provider request failed for " + test.value)
				},
			}
			err := test.call(client, test.value)
			if !errors.Is(err, errBWSCommandFailed) {
				t.Fatal("provider failure was not classified correctly")
			}
			if strings.Contains(err.Error(), test.value) || strings.Contains(err.Error(), "value-prefix-") || strings.Contains(err.Error(), "-value-suffix") {
				t.Fatal("secret value leaked in provider error")
			}
			if strings.Count(err.Error(), "[REDACTED]") < 2 {
				t.Fatal("stderr and runner error were not both redacted")
			}
		})
	}
}

func TestBWSClientDeleteAcceptsPlainTextSuccess(t *testing.T) {
	tests := []struct {
		name      string
		secretIDs []string
		response  string
	}{
		{name: "singular", secretIDs: []string{"secret-id"}, response: "1 secret deleted successfully."},
		{name: "plural", secretIDs: []string{"first-id", "second-id"}, response: "2 secrets deleted successfully."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{
				binPath: "bws-stub",
				token:   strings.Repeat("t", 16),
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return []byte(test.response), nil, nil
				},
			}

			if err := client.deleteSecrets(context.Background(), test.secretIDs); err != nil {
				t.Fatalf("plain-text delete success failed: %v", err)
			}
		})
	}
}

func TestBWSClientDeleteFailureRemainsRedacted(t *testing.T) {
	token := strings.Repeat("delete-token-", 2)
	client := &bwsClient{
		binPath: "bws-stub",
		token:   token,
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			return nil, []byte("provider rejected " + token), errors.New("exit status 1 for " + token)
		},
	}

	err := client.deleteSecrets(context.Background(), []string{"secret-id"})
	if !errors.Is(err, errBWSCommandFailed) {
		t.Fatalf("delete failure error=%v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Count(err.Error(), "[REDACTED]") < 2 {
		t.Fatal("delete failure did not preserve token redaction")
	}
}

func TestBWSClientDocumentCallsRejectEmptyOutput(t *testing.T) {
	tests := []struct {
		name string
		call func(*bwsClient) error
	}{
		{
			name: "list projects",
			call: func(client *bwsClient) error {
				_, err := client.listProjects(context.Background())
				return err
			},
		},
		{
			name: "list secrets",
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
		{
			name: "create secret",
			call: func(client *bwsClient) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name: "edit secret",
			call: func(client *bwsClient) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", "value", `{}`)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{
				binPath: "bws-stub",
				token:   strings.Repeat("t", 16),
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return nil, nil, nil
				},
			}

			if err := test.call(client); !errors.Is(err, errBWSInvalidJSON) {
				t.Fatalf("empty output error=%v", err)
			}
		})
	}
}

func TestBWSClientListCallsRejectInvalidResponseShapes(t *testing.T) {
	tests := []struct {
		name     string
		response string
		call     func(*bwsClient) error
	}{
		{
			name:     "projects null",
			response: `null`,
			call: func(client *bwsClient) error {
				_, err := client.listProjects(context.Background())
				return err
			},
		},
		{
			name:     "projects wrong top-level type",
			response: `{}`,
			call: func(client *bwsClient) error {
				_, err := client.listProjects(context.Background())
				return err
			},
		},
		{
			name:     "projects null element",
			response: `[null]`,
			call: func(client *bwsClient) error {
				_, err := client.listProjects(context.Background())
				return err
			},
		},
		{
			name:     "projects missing id",
			response: `[{"name":"project-name"}]`,
			call: func(client *bwsClient) error {
				_, err := client.listProjects(context.Background())
				return err
			},
		},
		{
			name:     "secrets null",
			response: `null`,
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
		{
			name:     "secrets wrong top-level type",
			response: `{}`,
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
		{
			name:     "secrets null element",
			response: `[null]`,
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
		{
			name:     "secrets missing revision date",
			response: `[{"id":"secret-id"}]`,
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
		{
			name:     "secrets project mismatch",
			response: `[{"id":"secret-id","projectId":"other-project","revisionDate":"revision-one"}]`,
			call: func(client *bwsClient) error {
				_, err := client.listSecrets(context.Background(), "project-id")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{
				binPath: "bws-stub",
				token:   strings.Repeat("t", 16),
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return []byte(test.response), nil, nil
				},
			}

			if err := test.call(client); !errors.Is(err, errBWSInvalidJSON) {
				t.Fatalf("response=%s error=%v", test.response, err)
			}
		})
	}
}

func TestBWSClientCreateAndEditRejectInvalidResponseShapes(t *testing.T) {
	tests := []struct {
		name     string
		response string
		call     func(*bwsClient) error
	}{
		{
			name:     "create null",
			response: `null`,
			call: func(client *bwsClient) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "create wrong top-level type",
			response: `[]`,
			call: func(client *bwsClient) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "create empty object",
			response: `{}`,
			call: func(client *bwsClient) error {
				_, err := client.createSecret(context.Background(), "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "edit null",
			response: `null`,
			call: func(client *bwsClient) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "edit wrong top-level type",
			response: `[]`,
			call: func(client *bwsClient) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "edit empty object",
			response: `{}`,
			call: func(client *bwsClient) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", "value", `{}`)
				return err
			},
		},
		{
			name:     "edit missing revision date",
			response: `{"id":"secret-id"}`,
			call: func(client *bwsClient) error {
				_, err := client.editSecret(context.Background(), "secret-id", "project-id", "KEY", "value", `{}`)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &bwsClient{
				binPath: "bws-stub",
				token:   strings.Repeat("t", 16),
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return []byte(test.response), nil, nil
				},
			}

			if err := test.call(client); !errors.Is(err, errBWSInvalidJSON) {
				t.Fatalf("response=%s error=%v", test.response, err)
			}
		})
	}
}

func TestBWSClientMutationResponsesMustMatchRequests(t *testing.T) {
	const (
		secretID  = "secret-id"
		projectID = "project-id"
		key       = "FIXTURE_KEY"
		value     = "fixture-sensitive-value"
		note      = `{"fixture":"sensitive-note"}`
	)
	valid := bwsSecret{
		ID:           secretID,
		ProjectID:    projectID,
		Key:          key,
		Value:        value,
		Note:         note,
		RevisionDate: "revision-one",
	}
	tests := []struct {
		name   string
		edit   bool
		mutate func(*bwsSecret)
	}{
		{name: "create project mismatch", mutate: func(secret *bwsSecret) { secret.ProjectID = "other-project" }},
		{name: "create key mismatch", mutate: func(secret *bwsSecret) { secret.Key = "OTHER_KEY" }},
		{name: "create value mismatch", mutate: func(secret *bwsSecret) { secret.Value = "other-value" }},
		{name: "create note mismatch", mutate: func(secret *bwsSecret) { secret.Note = `{}` }},
		{name: "edit id mismatch", edit: true, mutate: func(secret *bwsSecret) { secret.ID = "other-id" }},
		{name: "edit project mismatch", edit: true, mutate: func(secret *bwsSecret) { secret.ProjectID = "other-project" }},
		{name: "edit key mismatch", edit: true, mutate: func(secret *bwsSecret) { secret.Key = "OTHER_KEY" }},
		{name: "edit value mismatch", edit: true, mutate: func(secret *bwsSecret) { secret.Value = "other-value" }},
		{name: "edit note mismatch", edit: true, mutate: func(secret *bwsSecret) { secret.Note = `{}` }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := valid
			test.mutate(&response)
			payload, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			client := &bwsClient{
				binPath: "bws-stub",
				token:   strings.Repeat("t", 16),
				timeout: time.Second,
				runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
					return payload, nil, nil
				},
			}
			if test.edit {
				_, err = client.editSecret(context.Background(), secretID, projectID, key, value, note)
			} else {
				_, err = client.createSecret(context.Background(), projectID, key, value, note)
			}
			if !errors.Is(err, errBWSInvalidJSON) {
				t.Fatalf("mismatch error=%v", err)
			}
			if strings.Contains(err.Error(), value) || strings.Contains(err.Error(), note) {
				t.Fatal("mutation mismatch error leaked request content")
			}
		})
	}
}

func envSliceToMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
