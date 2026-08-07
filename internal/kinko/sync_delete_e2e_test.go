package kinko

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSyncPushPartialDeletePersistsExactAppliedPrefix(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, dek := setupSyncE2EVault(t)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	t.Setenv(envKinkoBWSBin, stub.stateful)
	args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
	if err := Run(args, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("initial push failed: %v", err)
	}

	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	delete(data.Shared, "SHARED_KEY")
	for _, scopes := range data.Profiles {
		for _, values := range scopes {
			delete(values, "LOCAL_KEY")
		}
	}
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envKinkoBWSBin, stub.partialDelete)
	var stdout bytes.Buffer
	runErr := Run(args, strings.NewReader("fixture-password\n"), &stdout, &bytes.Buffer{})
	if ExitCode(runErr) != exitCodeProviderFailed {
		t.Fatalf("partial-delete exit=%d err=%v", ExitCode(runErr), runErr)
	}
	if !strings.Contains(runErr.Error(), "provider rate limit: retry after 30 seconds") || !strings.Contains(runErr.Error(), "[REDACTED]") {
		t.Fatalf("human error omitted redacted provider diagnostic: %q", runErr.Error())
	}
	var result syncResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode partial-delete result: %v: %q", err, stdout.String())
	}
	if !result.Partial || result.Deleted != 1 || result.Unchanged != 1 || len(result.Actions) != 2 {
		t.Fatalf("partial=%t deleted=%d unchanged=%d actions=%d", result.Partial, result.Deleted, result.Unchanged, len(result.Actions))
	}
	deletedKey := ""
	for _, action := range result.Actions {
		if action.Action == syncActionKindName(syncActionDelete) {
			deletedKey = action.Key
		}
	}
	if deletedKey == "" {
		t.Fatal("partial result omitted the successfully deleted key")
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 2 {
		t.Fatalf("state entries=%d want 2", len(state.Entries))
	}
	for _, entry := range state.Entries {
		if entry.Key == deletedKey {
			t.Fatalf("state retained successfully deleted key %q", entry.Key)
		}
	}
	var remote []bwsSecret
	if err := json.Unmarshal(mustReadSyncTestFile(t, stub.stateData), &remote); err != nil {
		t.Fatal(err)
	}
	if len(remote) != 2 {
		t.Fatalf("remote secrets=%d want 2", len(remote))
	}
	assertNoSyncFixtureLeak(t, stdout.String()+runErr.Error())

	t.Setenv(envKinkoBWSBin, stub.stateful)
	stdout.Reset()
	if err := Run(args, strings.NewReader("fixture-password\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("converging push failed: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Partial {
		t.Fatalf("converging deleted=%d partial=%t", result.Deleted, result.Partial)
	}
	config, err = loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err = loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 1 {
		t.Fatalf("converged state entries=%d want 1", len(state.Entries))
	}
}
