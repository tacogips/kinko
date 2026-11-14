package kinko

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type doctorBWSOptions struct {
	Provider   string
	Online     bool
	CheckWrite bool
	Yes        bool
	JSON       bool
}

type doctorBWSCheck struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	CleanupID string `json:"cleanup_id,omitempty"`
}

type doctorBWSResult struct {
	Checks []doctorBWSCheck `json:"checks"`
}

func runDoctorBWS(opts globalOptions, options doctorBWSOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if options.Provider != supportedSyncProvider {
		return newCLIError(exitCodePolicyFailed, "Doctor provider mode requires --provider=bws.", errors.New("unsupported doctor provider"))
	}
	if options.CheckWrite && (!options.Online || !options.Yes) {
		return newCLIError(exitCodePolicyFailed, "--check-write requires both --online and --yes.", nil)
	}
	result := doctorBWSResult{Checks: []doctorBWSCheck{}}
	err := withLockedSyncSnapshot(opts, stdin, stderr, func(snapshot *lockedSyncSnapshot) error {
		runtime, err := resolveBWSRuntimeConfig(bwsConfigOptions{}, snapshot.Config, os.Getenv)
		if err != nil {
			return err
		}
		result.Checks = append(result.Checks,
			doctorBWSCheck{Name: "config", Status: "ok", Detail: "profile=" + runtime.Profile},
			doctorBWSCheck{Name: "endpoint", Status: "ok", Detail: endpointString(runtime.Endpoints.APIURL)},
		)
		if runtime.ProjectID == "" {
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "project", Status: "warning", Detail: "project id is not configured"})
		}
		if snapshot.Envelope.Format == 0 {
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "state", Status: "ok", Detail: "absent"})
		} else {
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "state", Status: "ok", Detail: fmt.Sprintf("format=%d", snapshot.Envelope.Format)})
		}
		maps, err := loadEncryptedSyncPathMaps(snapshot.Config)
		if err != nil {
			return err
		}
		result.Checks = append(result.Checks, doctorBWSCheck{Name: "path-maps", Status: "ok", Detail: fmt.Sprintf("count=%d", len(maps))})
		gate, err := inspectLocalBWSVersion(runtime)
		if err != nil {
			return providerCLIError("BWS binary/version check failed.", err)
		}
		versionStatus := "warning"
		if gate.MutationAllowed {
			versionStatus = "ok"
		}
		result.Checks = append(result.Checks,
			doctorBWSCheck{Name: "binary-version", Status: versionStatus, Detail: gate.Version},
			doctorBWSCheck{Name: "transport", Status: "warning", Detail: "value-safe mutation transport is not compiled; read and revision-checked delete are available"},
		)
		token, err := resolveBWSAccessToken(os.Getenv, snapshot.Data.Shared, stderr)
		if err != nil {
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "credentials", Status: "failed", Detail: "missing-credentials"})
			if options.Online {
				return err
			}
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "credentials", Status: "warning", Detail: "not configured"})
			return nil
		}
		provider, err := newBWSCLIAdapter(token, runtime, stderr)
		if err != nil {
			return providerCLIError("BWS binary/version check failed.", err)
		}
		if !options.Online {
			return nil
		}
		projects, err := provider.ListProjects(context.Background())
		if err != nil {
			category := classifyDoctorBWSError(err)
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "online-auth", Status: "failed", Detail: category})
			return doctorBWSProviderError(category, err)
		}
		result.Checks = append(result.Checks, doctorBWSCheck{Name: "online-auth", Status: "ok", Detail: "accepted"})
		projectID := runtime.ProjectID
		if projectID == "" && len(projects) == 1 {
			projectID = projects[0].ID
		}
		if projectID == "" || !doctorProjectAssigned(projects, projectID) {
			category := "project-not-found-or-unassigned"
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "project-access", Status: "failed", Detail: category})
			return doctorBWSProviderError(category, errors.New(category))
		}
		if _, err := provider.ListSecrets(context.Background(), projectID); err != nil {
			category := classifyDoctorBWSError(err)
			if category == "provider-failure" {
				category = "read-forbidden"
			}
			result.Checks = append(result.Checks, doctorBWSCheck{Name: "online-read", Status: "failed", Detail: category})
			return doctorBWSProviderError(category, err)
		}
		result.Checks = append(result.Checks, doctorBWSCheck{Name: "project-access", Status: "ok", Detail: "assigned"}, doctorBWSCheck{Name: "online-read", Status: "ok", Detail: "allowed"})
		if options.CheckWrite {
			check, err := runBWSWriteCanary(context.Background(), provider, projectID)
			result.Checks = append(result.Checks, check)
			if err != nil {
				return doctorBWSProviderError("write-check-failed", err)
			}
			return nil
		}
		result.Checks = append(result.Checks, doctorBWSCheck{Name: "online-write", Status: "warning", Detail: "unknown-not-tested"})
		return nil
	})
	if outputErr := printDoctorBWSResult(stdout, result, options.JSON); outputErr != nil {
		return newCLIError(exitCodeIOFailed, "Could not write BWS doctor output.", outputErr)
	}
	return err
}

func inspectLocalBWSVersion(runtime bwsRuntimeConfig) (bwsVersionGate, error) {
	binary := os.Getenv(envKinkoBWSBin)
	if binary == "" {
		binary = "bws"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return bwsVersionGate{}, fmt.Errorf("%w: %v", errBWSBinaryMissing, err)
	}
	client := &bwsClient{binPath: resolved, timeout: bwsCallTimeout, runner: runBWSCommand, environmentBuilder: isolatedBWSEnvironmentBuilder(runtime)}
	return inspectBWSVersion(context.Background(), client)
}

func doctorProjectAssigned(projects []bwsProject, projectID string) bool {
	for _, project := range projects {
		if project.ID == projectID {
			return true
		}
	}
	return false
}

func classifyDoctorBWSError(err error) string {
	if err == nil {
		return "ok"
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"no access token", "access token is required", "missing credential", "not configured"} {
		if strings.Contains(message, marker) {
			return "missing-credentials"
		}
	}
	for _, marker := range []string{"unauthorized", "authentication", "invalid token", "expired token", "status 401"} {
		if strings.Contains(message, marker) {
			return "rejected-or-expired-token"
		}
	}
	for _, marker := range []string{"x509", "certificate", "tls", "clock skew", "not yet valid"} {
		if strings.Contains(message, marker) {
			return "tls-or-clock-failure"
		}
	}
	for _, marker := range []string{"project not found", "not assigned", "status 404"} {
		if strings.Contains(message, marker) {
			return "project-not-found-or-unassigned"
		}
	}
	for _, marker := range []string{"forbidden", "permission", "status 403"} {
		if strings.Contains(message, marker) {
			return "read-forbidden"
		}
	}
	return "provider-failure"
}

func doctorBWSProviderError(category string, cause error) error {
	return newCLIError(exitCodeProviderFailed, "BWS online diagnostic failed: "+category+".", cause)
}

func runBWSWriteCanary(ctx context.Context, provider syncProvider, projectID string) (doctorBWSCheck, error) {
	check := doctorBWSCheck{Name: "write-canary", Status: "failed"}
	if err := requireSyncCapabilities(provider, syncCapabilityRead, syncCapabilityDelete, syncCapabilityValueSafeMutation); err != nil {
		check.Detail = "value-safe mutation capability unavailable"
		return check, err
	}
	if projectID == "" {
		check.Detail = "project id is required"
		return check, errors.New(check.Detail)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return check, fmt.Errorf("generate doctor canary identity: %w", err)
	}
	name := "kinko_doctor_" + hex.EncodeToString(random[:8])
	value := hex.EncodeToString(random[8:])
	created, err := provider.CreateSecret(ctx, bwsMutationRequest{ProjectID: projectID, Name: name, Value: value, Note: "kinko doctor value-safe write canary"})
	value = ""
	if err != nil {
		check.Detail = "canary create failed"
		return check, err
	}
	check.CleanupID = created.ID
	readBack, err := provider.GetSecret(ctx, created.ID)
	if err != nil {
		check.Detail = "canary read-back failed; cleanup required"
		return check, err
	}
	readBack.Value = ""
	if err := provider.DeleteSecret(ctx, created.ID); err != nil {
		check.Detail = "canary delete failed; cleanup required"
		return check, err
	}
	check.Status, check.Detail, check.CleanupID = "ok", "create/read/delete succeeded", ""
	return check, nil
}

func printDoctorBWSResult(writer io.Writer, result doctorBWSResult, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(writer).Encode(result)
	}
	for _, check := range result.Checks {
		if check.CleanupID != "" {
			if _, err := fmt.Fprintf(writer, "%s %s: %s cleanup-id=%s\n", check.Status, check.Name, check.Detail, check.CleanupID); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(writer, "%s %s: %s\n", check.Status, check.Name, check.Detail); err != nil {
			return err
		}
	}
	return nil
}
