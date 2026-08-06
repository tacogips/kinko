package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapStubBWSEndToEndIsAtomicReadOnlyAndIdentityIsolated(t *testing.T) {
	const canary = "bootstrap-e2e-sensitive-canary"
	remote := []bwsSecret{
		bootstrapSharedSecret(t, "first", "FIRST_KEY", canary),
		bootstrapSharedSecret(t, "second", "SECOND_KEY", "second-sensitive-value"),
	}
	remoteBefore, _ := json.Marshal(remote)
	dataDir, _, dek := setupEmptySyncE2EVault(t, syncTestMachineID)
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	metaPath := filepath.Join(dataDir, "vault", "meta.v1.json")
	vaultBefore, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	metaBefore, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildBootstrapPlan(remote, data, bootstrapOptions(false), bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}

	interrupted := &bootstrapTestProvider{secrets: indexBootstrapSecrets(remote), failGetAt: 2}
	if err := applyBootstrapPlan(context.Background(), interrupted, plan, data); err == nil {
		t.Fatal("expected interrupted source read")
	}
	vaultAfterInterruption, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vaultBefore, vaultAfterInterruption) {
		t.Fatal("interrupted bootstrap changed encrypted vault bytes")
	}

	provider := &bootstrapTestProvider{secrets: indexBootstrapSecrets(remote)}
	data, err = loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBootstrapPlan(context.Background(), provider, plan, data); err != nil {
		t.Fatal(err)
	}
	originalRename := atomicRename
	t.Cleanup(func() { atomicRename = originalRename })
	atomicRename = func(string, string) error { return errors.New("injected atomic bootstrap save failure") }
	if err := saveVault(dataDir, dek, data); err == nil {
		t.Fatal("expected atomic vault save failure")
	}
	atomicRename = originalRename
	vaultAfterSaveFailure, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vaultBefore, vaultAfterSaveFailure) {
		t.Fatal("failed atomic bootstrap save changed encrypted vault bytes")
	}
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistBootstrapProvenance(dataDir, dek, config, plan); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Shared["FIRST_KEY"] != canary || reloaded.Shared["SECOND_KEY"] != "second-sensitive-value" {
		t.Fatalf("bootstrapped vault=%+v", reloaded)
	}
	metaAfter, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	remoteAfter, _ := json.Marshal(remote)
	if !bytes.Equal(metaBefore, metaAfter) || !bytes.Equal(remoteBefore, remoteAfter) || provider.createCalls+provider.updateCalls+provider.deleteCalls != 0 {
		t.Fatalf("bootstrap changed identity or provider: provider=%+v", provider)
	}
	config, err = loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if config[configKeyBWSSyncState] != "" {
		t.Fatal("bootstrap created a normal sync baseline")
	}
	provenance := config[configKeyBootstrapProvenance]
	if provenance == "" || strings.Contains(provenance, canary) || strings.Contains(provenance, "second-sensitive-value") {
		t.Fatalf("invalid bootstrap provenance: %s", provenance)
	}
}

func TestRunSyncBootstrapPreviewsThenAppliesThroughReadOnlyStub(t *testing.T) {
	const canary = "bootstrap-orchestration-sensitive-canary"
	remote := []bwsSecret{
		bootstrapSharedSecret(t, "orchestrated-first", "ORCHESTRATED_FIRST", canary),
		bootstrapSharedSecret(t, "orchestrated-second", "ORCHESTRATED_SECOND", "second-orchestration-value"),
	}
	remotePayload, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	stub := buildStubBWS(t, string(remotePayload))
	t.Setenv(envKinkoBWSBin, stub.remote)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	t.Setenv("BWS_ACCESS_TOKEN", "fixture-parent-token")
	t.Setenv("PARENT_ONLY_MARKER", "fixture-parent-marker")

	dataDir, configPath, dek := setupEmptySyncE2EVault(t, syncTestMachineID)
	opts := globalOptions{dataDir: dataDir, configPath: configPath}
	bootstrapOpts := bootstrapOptions(false)
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	metaPath := filepath.Join(dataDir, "vault", "meta.v1.json")
	vaultBefore := mustReadSyncTestFile(t, vaultPath)
	metaBefore := mustReadSyncTestFile(t, metaPath)
	remoteBefore := mustReadSyncTestFile(t, stub.remoteData)

	var previewOut, previewErr bytes.Buffer
	if err := runSyncBootstrap(opts, bootstrapOpts, strings.NewReader("fixture-password\n"), &previewOut, &previewErr); err != nil {
		t.Fatalf("preview bootstrap: %v", err)
	}
	if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) {
		t.Fatal("bootstrap preview changed encrypted vault bytes")
	}
	if !strings.Contains(previewOut.String(), "bootstrap=preview") {
		t.Fatalf("preview output omitted mode: %s", previewOut.String())
	}

	bootstrapOpts.Yes = true
	var applyOut, applyErr bytes.Buffer
	if err := runSyncBootstrap(opts, bootstrapOpts, strings.NewReader("fixture-password\n"), &applyOut, &applyErr); err != nil {
		t.Fatalf("apply bootstrap: %v", err)
	}
	if !strings.Contains(applyOut.String(), "bootstrap=applied") {
		t.Fatalf("apply output omitted mode: %s", applyOut.String())
	}
	for _, output := range []string{previewOut.String(), previewErr.String(), applyOut.String(), applyErr.String()} {
		for _, forbidden := range []string{canary, "second-orchestration-value", "fixture-kinko-token", "fixture-parent-token"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("bootstrap orchestration output leaked %q: %s", forbidden, output)
			}
		}
	}

	reloaded, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Shared["ORCHESTRATED_FIRST"] != canary || reloaded.Shared["ORCHESTRATED_SECOND"] != "second-orchestration-value" {
		t.Fatalf("orchestrated bootstrap values were not applied: %+v", reloaded.Shared)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if config[configKeyBWSSyncState] != "" || config[configKeyBootstrapProvenance] == "" {
		t.Fatalf("bootstrap baseline/provenance isolation failed: %+v", config)
	}
	if !bytes.Equal(metaBefore, mustReadSyncTestFile(t, metaPath)) || !bytes.Equal(remoteBefore, mustReadSyncTestFile(t, stub.remoteData)) {
		t.Fatal("bootstrap changed machine metadata or remote BWS fixture")
	}
	if journal := mustReadOptionalSyncTestFile(t, stub.journal); len(journal) != 0 {
		t.Fatalf("bootstrap attempted a BWS mutation: %s", journal)
	}
}

func TestBootstrapOutputAndProvenanceAreValueFree(t *testing.T) {
	const canary = "bootstrap-output-sensitive-canary"
	plan, err := buildBootstrapPlan([]bwsSecret{bootstrapSharedSecret(t, "source", "OUTPUT_KEY", canary)}, executionDataForValues(nil), bootstrapOptions(false), bootstrapRuntime())
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonOutput := range []bool{false, true} {
		var output bytes.Buffer
		if err := printSyncBootstrapResult(&output, bootstrapResultForPlan(plan, false), jsonOutput); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), canary) || !strings.Contains(output.String(), plan.PlanDigest) {
			t.Fatalf("bootstrap output leaked value or omitted plan pin: %s", output.String())
		}
	}
	provenance := syncBootstrapProvenance{Format: 1, PlanDigest: plan.PlanDigest, ProviderIdentity: plan.ProviderIdentity, SourceMachineID: plan.SourceMachineID, TargetMachineID: plan.TargetMachineID, SelectorDigest: plan.SelectorDigest, PathMapDigest: plan.PathMapDigest, Selected: len(plan.Actions)}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(canary)) {
		t.Fatal("bootstrap provenance leaked source value")
	}
}
