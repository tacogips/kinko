package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	envKinkoBWSAccessToken  = "KINKO_BWS_ACCESS_TOKEN"
	sharedKeyBWSAccessToken = "KINKO_BWS_ACCESS_TOKEN"
	envKinkoBWSBin          = "KINKO_BWS_BIN"
	envBWSAccessToken       = "BWS_ACCESS_TOKEN"
)

var bwsCallTimeout = 30 * time.Second

var (
	errBWSBinaryMissing = errors.New("bws binary missing")
	errBWSCommandFailed = errors.New("bws command failed")
	errBWSInvalidJSON   = errors.New("bws returned invalid JSON")
	errBWSTimeout       = errors.New("bws command timed out")
)

type bwsSecret struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	ProjectID      string `json:"projectId"`
	Key            string `json:"key"`
	Value          string `json:"value"`
	Note           string `json:"note"`
	CreationDate   string `json:"creationDate"`
	RevisionDate   string `json:"revisionDate"`
}

type bwsProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type bwsRunner func(ctx context.Context, bin string, env []string, args ...string) (stdout []byte, stderr []byte, err error)

type bwsEnvironmentBuilder func(token string) (environment []string, cleanup func(), err error)

type bwsClient struct {
	binPath            string
	token              string
	timeout            time.Duration
	runner             bwsRunner
	environmentBuilder bwsEnvironmentBuilder
}

func resolveBWSAccessToken(getenv func(string) string, shared map[string]string, stderr io.Writer) (string, error) {
	environmentToken := getenv(envKinkoBWSAccessToken)
	sharedToken := shared[sharedKeyBWSAccessToken]
	if environmentToken != "" {
		if sharedToken != "" {
			_, _ = fmt.Fprintf(stderr, "NOTICE: %s is set; the shared secret of the same name is ignored.\n", envKinkoBWSAccessToken)
		}
		return environmentToken, nil
	}
	if sharedToken != "" {
		return sharedToken, nil
	}
	return "", newCLIError(
		exitCodePolicyFailed,
		fmt.Sprintf("BWS access token is required from %s or the shared secret of the same name.", envKinkoBWSAccessToken),
		errors.New("BWS access token is not configured"),
	)
}

func newBWSClient(token string, stderr io.Writer) (*bwsClient, error) {
	if token == "" {
		return nil, errors.New("BWS access token must not be empty")
	}
	if os.Getenv(envBWSAccessToken) != "" {
		_, _ = fmt.Fprintf(stderr, "NOTICE: parent %s is ignored for kinko sync.\n", envBWSAccessToken)
	}
	binary := os.Getenv(envKinkoBWSBin)
	if binary == "" {
		binary = "bws"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%w: install the Bitwarden Secrets Manager CLI or set %s: %v", errBWSBinaryMissing, envKinkoBWSBin, err)
	}
	return &bwsClient{
		binPath: resolved,
		token:   token,
		timeout: bwsCallTimeout,
		runner:  runBWSCommand,
	}, nil
}

func buildBWSChildEnv(token string) []string {
	environment := []string{envBWSAccessToken + "=" + token}
	for _, key := range []string{
		"HOME",
		"PATH",
		"TMPDIR",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"SSL_CERT_FILE",
		"SSL_CERT_DIR",
	} {
		if value := os.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

func redactBWSValues(output string, values ...string) string {
	ordered := append([]string(nil), values...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return len(ordered[i]) > len(ordered[j])
	})
	for _, value := range ordered {
		if value == "" {
			continue
		}
		output = strings.ReplaceAll(output, value, "[REDACTED]")
	}
	return output
}

func (client *bwsClient) listProjects(ctx context.Context) ([]bwsProject, error) {
	var projects []bwsProject
	if err := client.runJSON(ctx, &projects, "project", "list"); err != nil {
		return nil, err
	}
	if projects == nil {
		return nil, fmt.Errorf("%w: project list must be an array", errBWSInvalidJSON)
	}
	for index, project := range projects {
		if strings.TrimSpace(project.ID) == "" {
			return nil, fmt.Errorf("%w: project at index %d has no id", errBWSInvalidJSON, index)
		}
	}
	return projects, nil
}

func (client *bwsClient) listSecrets(ctx context.Context, projectID string) ([]bwsSecret, error) {
	var secrets []bwsSecret
	if err := client.runJSON(ctx, &secrets, "secret", "list", projectID); err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, fmt.Errorf("%w: secret list must be an array", errBWSInvalidJSON)
	}
	for index, secret := range secrets {
		if err := validateBWSSecret(secret); err != nil {
			return nil, fmt.Errorf("%w: secret at index %d: %v", errBWSInvalidJSON, index, err)
		}
		if secret.ProjectID != projectID {
			return nil, fmt.Errorf("%w: secret at index %d has a mismatched projectId", errBWSInvalidJSON, index)
		}
	}
	return secrets, nil
}

func (client *bwsClient) getSecret(ctx context.Context, secretID string) (bwsSecret, error) {
	var secret bwsSecret
	if err := client.runJSON(ctx, &secret, "secret", "get", secretID); err != nil {
		return bwsSecret{}, err
	}
	if err := validateBWSSecret(secret); err != nil {
		return bwsSecret{}, fmt.Errorf("%w: fetched secret: %v", errBWSInvalidJSON, err)
	}
	return secret, nil
}

func (client *bwsClient) createSecret(ctx context.Context, projectID, key, value, note string) (bwsSecret, error) {
	var secret bwsSecret
	if err := client.runJSONWithRedactions(ctx, &secret, []string{value}, "secret", "create", key, value, projectID, "--note", note); err != nil {
		return bwsSecret{}, err
	}
	if err := validateBWSSecret(secret); err != nil {
		return bwsSecret{}, fmt.Errorf("%w: created secret: %v", errBWSInvalidJSON, err)
	}
	if err := validateBWSMutationResponse(secret, "", projectID, key, value, note); err != nil {
		return bwsSecret{}, err
	}
	return secret, nil
}

func (client *bwsClient) editSecret(ctx context.Context, secretID, projectID, key, value, note string) (bwsSecret, error) {
	var secret bwsSecret
	if err := client.runJSONWithRedactions(ctx, &secret, []string{value}, "secret", "edit", secretID, "--value", value, "--note", note); err != nil {
		return bwsSecret{}, err
	}
	if err := validateBWSSecret(secret); err != nil {
		return bwsSecret{}, fmt.Errorf("%w: edited secret: %v", errBWSInvalidJSON, err)
	}
	if err := validateBWSMutationResponse(secret, secretID, projectID, key, value, note); err != nil {
		return bwsSecret{}, err
	}
	return secret, nil
}

func validateBWSMutationResponse(secret bwsSecret, secretID, projectID, key, value, note string) error {
	var mismatches []string
	if secretID != "" && secret.ID != secretID {
		mismatches = append(mismatches, "id")
	}
	if secret.ProjectID != projectID {
		mismatches = append(mismatches, "projectId")
	}
	if secret.Key != key {
		mismatches = append(mismatches, "key")
	}
	if secret.Value != value {
		mismatches = append(mismatches, "value")
	}
	if secret.Note != note {
		mismatches = append(mismatches, "note")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: mutation response does not match request fields: %s", errBWSInvalidJSON, strings.Join(mismatches, ", "))
	}
	return nil
}

func validateBWSSecret(secret bwsSecret) error {
	if strings.TrimSpace(secret.ID) == "" {
		return errors.New("missing id")
	}
	if strings.TrimSpace(secret.RevisionDate) == "" {
		return errors.New("missing revisionDate")
	}
	return nil
}

func (client *bwsClient) deleteSecrets(ctx context.Context, secretIDs []string) error {
	if len(secretIDs) == 0 {
		return nil
	}
	arguments := append([]string{"secret", "delete"}, secretIDs...)
	_, err := client.run(ctx, nil, arguments...)
	return err
}

func (client *bwsClient) runJSON(ctx context.Context, destination any, arguments ...string) error {
	return client.runJSONWithRedactions(ctx, destination, nil, arguments...)
}

func (client *bwsClient) runJSONWithRedactions(ctx context.Context, destination any, sensitiveValues []string, arguments ...string) error {
	stdout, err := client.run(ctx, sensitiveValues, arguments...)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(stdout)) == 0 {
		return fmt.Errorf("%w: empty response", errBWSInvalidJSON)
	}
	if err := json.Unmarshal(stdout, destination); err != nil {
		return fmt.Errorf("%w: %v", errBWSInvalidJSON, err)
	}
	return nil
}

func (client *bwsClient) run(ctx context.Context, sensitiveValues []string, arguments ...string) ([]byte, error) {
	if client == nil || client.runner == nil || client.binPath == "" || client.timeout <= 0 {
		return nil, errors.New("BWS client is not initialized")
	}
	arguments = append(arguments, "--output", "json", "--color", "no")
	callContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	environment, cleanup, err := client.buildEnvironment()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	stdout, stderr, err := client.runner(callContext, client.binPath, environment, arguments...)
	if err != nil {
		if errors.Is(callContext.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w after %s", errBWSTimeout, client.timeout)
		}
		redactions := append([]string{client.token}, sensitiveValues...)
		redacted := strings.TrimSpace(redactBWSValues(string(stderr), redactions...))
		redactedError := redactBWSValues(err.Error(), redactions...)
		if redacted == "" {
			return nil, fmt.Errorf("%w: %s", errBWSCommandFailed, redactedError)
		}
		return nil, fmt.Errorf("%w: %s: %s", errBWSCommandFailed, redacted, redactedError)
	}
	return stdout, nil
}

func (client *bwsClient) buildEnvironment() ([]string, func(), error) {
	if client.environmentBuilder == nil {
		return buildBWSChildEnv(client.token), func() {}, nil
	}
	environment, cleanup, err := client.environmentBuilder(client.token)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare isolated BWS environment: %w", err)
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	return environment, cleanup, nil
}

func runBWSCommand(ctx context.Context, binary string, environment []string, arguments ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = append([]string(nil), environment...)
	command.Stdin = nil
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
