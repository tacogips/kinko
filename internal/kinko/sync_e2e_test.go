package kinko

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSyncPushStubBWSEndToEnd(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, dek := setupSyncE2EVault(t)
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
	vaultBefore := mustReadSyncTestFile(t, vaultPath)
	configBefore := mustReadSyncTestFile(t, configBlobPath)

	t.Setenv(envKinkoBWSBin, stub.stateful)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	t.Setenv(envBWSAccessToken, "fixture-parent-token")
	t.Setenv("PARENT_ONLY_MARKER", "must-not-reach-child")
	base := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}

	var dryOut bytes.Buffer
	if err := Run(append(base, "--dry-run"), strings.NewReader("fixture-password\n"), &dryOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configBlobPath)) {
		t.Fatal("dry-run changed encrypted local files")
	}
	if journal := mustReadOptionalSyncTestFile(t, stub.journal); len(journal) != 0 {
		t.Fatal("dry-run performed a remote mutation")
	}
	assertNoSyncFixtureLeak(t, dryOut.String())

	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(base, strings.NewReader("fixture-password\n"), &out, &stderr); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	assertNoSyncFixtureLeak(t, out.String()+stderr.String())
	journal := string(mustReadSyncTestFile(t, stub.journal))
	if strings.Count(journal, "create ") != 3 || strings.Contains(journal, "fixture-shared") || strings.Contains(journal, "fixture-local") || strings.Contains(journal, "fixture-kinko-token") {
		t.Fatalf("unexpected redacted mutation journal: %q", journal)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 3 || state.ProjectID != "fixture-project" {
		t.Fatalf("state entries=%d project=%q", len(state.Entries), state.ProjectID)
	}

	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	secondDir, secondConfig, secondDEK := setupEmptySyncE2EVault(t, meta.MachineID)
	secondArgs := []string{"--kinko-dir", secondDir, "--config", secondConfig, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
	if err := Run(secondArgs, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("second-vault pull failed: %v", err)
	}
	secondData, err := loadVault(secondDir, secondDEK)
	if err != nil {
		t.Fatal(err)
	}
	if secondData.Shared["SHARED_KEY"] != "fixture-shared" || countVaultSyncKeys(secondData) != 3 {
		t.Fatal("second vault did not round-trip the pushed entries")
	}

	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	data.Shared["SHARED_KEY"] = "fixture-updated"
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	if err := Run(base, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("stateful update failed: %v", err)
	}
	config, err = loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err = loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	sharedName := buildBWSSecretName(meta.MachineID, scopeRef{kind: scopeKindShared}, "SHARED_KEY")
	if state.Entries[sharedName].RevisionDate != "revision-edit" || strings.Count(string(mustReadSyncTestFile(t, stub.journal)), "edit ") != 1 {
		t.Fatal("stateful edit did not record the returned revision baseline")
	}
	data, err = loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	for _, scopes := range data.Profiles {
		for _, values := range scopes {
			delete(values, "LOCAL_KEY")
		}
	}
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	if err := Run(base, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("stateful deletion push failed: %v", err)
	}
	config, err = loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err = loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	var remoteAfterDelete []bwsSecret
	if err := json.Unmarshal(mustReadSyncTestFile(t, stub.stateData), &remoteAfterDelete); err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 2 || len(remoteAfterDelete) != 2 || strings.Count(string(mustReadSyncTestFile(t, stub.journal)), "delete\n") != 1 {
		t.Fatalf("push deletion state=%d remote=%d", len(state.Entries), len(remoteAfterDelete))
	}

	remoteAfterDelete[0].Value = "fixture-divergent"
	remoteAfterDelete[0].RevisionDate = "revision-remote-drift"
	driftPayload, err := json.Marshal(remoteAfterDelete)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub.stateData, driftPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKinkoBWSBin, stub.stateful)
	var conflictOut bytes.Buffer
	err = Run(base, strings.NewReader("fixture-password\n"), &conflictOut, &bytes.Buffer{})
	if ExitCode(err) != exitCodeSyncConflict {
		t.Fatalf("conflict exit=%d err=%v", ExitCode(err), err)
	}
	assertNoSyncFixtureLeak(t, conflictOut.String())

	forceArgs := append(append([]string{}, base...), "--force")
	if err := Run(forceArgs, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("forced push failed: %v", err)
	}
}

func TestSyncStubBWSProviderFailuresAndCLIValidation(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, _ := setupSyncE2EVault(t)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	base := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush}

	t.Run("provider required", func(t *testing.T) {
		err := Run(base, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("unknown provider", func(t *testing.T) {
		err := Run(append(base, "--provider", "fixture-provider"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), supportedSyncProvider) {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
	t.Run("positional argument rejected", func(t *testing.T) {
		err := Run(append(base, "--provider", supportedSyncProvider, "unexpected"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("unknown flag rejected", func(t *testing.T) {
		err := Run(append(base, "--provider", supportedSyncProvider, "--unknown-sync-flag"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d", ExitCode(err))
		}
	})
	t.Run("garbage JSON", func(t *testing.T) {
		t.Setenv(envKinkoBWSBin, stub.garbage)
		err := Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeProviderFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
		assertNoSyncFixtureLeak(t, err.Error())
	})
	t.Run("missing binary", func(t *testing.T) {
		t.Setenv(envKinkoBWSBin, filepath.Join(t.TempDir(), "missing-bws"))
		err := Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeProviderFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
	t.Run("non-zero exit", func(t *testing.T) {
		t.Setenv(envKinkoBWSBin, stub.nonzero)
		err := Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeProviderFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
		if !strings.Contains(err.Error(), "provider rate limit: retry after 30 seconds") || !strings.Contains(err.Error(), "[REDACTED]") {
			t.Fatalf("human error omitted redacted provider diagnostic: %q", err.Error())
		}
		assertNoSyncFixtureLeak(t, err.Error())
	})
	t.Run("slow response through command", func(t *testing.T) {
		t.Setenv(envKinkoBWSBin, stub.slow)
		previousTimeout := bwsCallTimeout
		bwsCallTimeout = 20 * time.Millisecond
		defer func() { bwsCallTimeout = previousTimeout }()
		err := Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeProviderFailed || !errors.Is(err, errBWSTimeout) {
			t.Fatalf("exit=%d timeout error=%v", ExitCode(err), err)
		}
	})
	t.Run("forced pull rejects a secret from another project without mutation", func(t *testing.T) {
		dataDir, configPath, dek := setupSyncE2EVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		name := buildBWSSecretName(meta.MachineID, scopeRef{kind: scopeKindShared}, "SHARED_KEY")
		note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: "SHARED_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal([]bwsSecret{{
			ID:           "other-project-secret-id",
			ProjectID:    "other-project",
			Key:          name,
			Value:        "fixture-remote",
			Note:         note,
			RevisionDate: "revision-other-project",
		}})
		if err != nil {
			t.Fatal(err)
		}
		mismatchStub := buildStubBWS(t, string(payload))
		t.Setenv(envKinkoBWSBin, mismatchStub.remote)

		vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
		configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
		vaultBefore := mustReadSyncTestFile(t, vaultPath)
		configBefore := mustReadSyncTestFile(t, configBlobPath)
		config, err := loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		stateBefore, err := loadBWSSyncState(config)
		if err != nil {
			t.Fatal(err)
		}

		args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--force", "--json"}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		runErr := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr)
		if ExitCode(runErr) != exitCodeProviderFailed || !errors.Is(runErr, errBWSInvalidJSON) {
			t.Fatalf("exit=%d provider classification mismatch", ExitCode(runErr))
		}
		assertNoSyncFixtureLeak(t, stdout.String()+stderr.String()+runErr.Error())
		if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configBlobPath)) {
			t.Fatal("cross-project provider response mutated encrypted local files")
		}
		config, err = loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		stateAfter, err := loadBWSSyncState(config)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stateAfter, stateBefore) {
			t.Fatal("cross-project provider response mutated sync state")
		}
	})
	t.Run("duplicate machine-owned names", func(t *testing.T) {
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		ref := scopeRef{kind: scopeKindShared}
		name := buildBWSSecretName(meta.MachineID, ref, "DUPLICATE_KEY")
		note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: "DUPLICATE_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal([]bwsSecret{
			{ID: "duplicate-one", ProjectID: "fixture-project", Key: name, Note: note, Value: "fixture-remote", RevisionDate: "revision-one"},
			{ID: "duplicate-two", ProjectID: "fixture-project", Key: name, Note: note, Value: "fixture-remote", RevisionDate: "revision-two"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stub.remoteData, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(envKinkoBWSBin, stub.remote)
		err = Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--force"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
	t.Run("duplicate machine-owned ids leave pull files unchanged", func(t *testing.T) {
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		ref := scopeRef{kind: scopeKindShared}
		firstName := buildBWSSecretName(meta.MachineID, ref, "FIRST_DUPLICATE_ID_KEY")
		secondName := buildBWSSecretName(meta.MachineID, ref, "SECOND_DUPLICATE_ID_KEY")
		firstNote, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: "FIRST_DUPLICATE_ID_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		secondNote, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: "SECOND_DUPLICATE_ID_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal([]bwsSecret{
			{ID: "duplicate-id", ProjectID: "fixture-project", Key: firstName, Note: firstNote, Value: "fixture-first", RevisionDate: "revision-one"},
			{ID: "duplicate-id", ProjectID: "fixture-project", Key: secondName, Note: secondNote, Value: "fixture-second", RevisionDate: "revision-two"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stub.remoteData, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
		configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
		vaultBefore := mustReadSyncTestFile(t, vaultPath)
		configBefore := mustReadSyncTestFile(t, configBlobPath)
		t.Setenv(envKinkoBWSBin, stub.remote)
		pullArgs := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--force"}
		err = Run(pullArgs, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
		if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configBlobPath)) {
			t.Fatal("duplicate remote ids mutated pull files")
		}
	})
	t.Run("force rejects ids duplicated by filtered records", func(t *testing.T) {
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		ref := scopeRef{kind: scopeKindShared}
		ownedName := buildBWSSecretName(meta.MachineID, ref, "OWNED_KEY")
		ownedNote, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: "OWNED_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		owned := bwsSecret{ID: "globally-duplicate-id", ProjectID: "fixture-project", Key: ownedName, Note: ownedNote, Value: "fixture-owned", RevisionDate: "revision-owned"}
		reservedName := buildBWSSecretName(meta.MachineID, ref, sharedKeyBWSAccessToken)
		reservedNote, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Scope: scopeKindShared, Key: sharedKeyBWSAccessToken})
		if err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name     string
			filtered bwsSecret
		}{
			{
				name:     "other machine",
				filtered: bwsSecret{ID: owned.ID, ProjectID: "fixture-project", Key: "fedcba9876543210_deadbeef_OTHER_KEY", Value: "fixture-other", RevisionDate: "revision-other"},
			},
			{
				name:     "reserved key",
				filtered: bwsSecret{ID: owned.ID, ProjectID: "fixture-project", Key: reservedName, Note: reservedNote, Value: "fixture-token", RevisionDate: "revision-reserved"},
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				payload, err := json.Marshal([]bwsSecret{owned, test.filtered})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stub.remoteData, payload, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(envKinkoBWSBin, stub.remote)
				args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--force"}
				err = Run(args, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
				if ExitCode(err) != exitCodePolicyFailed {
					t.Fatalf("exit=%d err=%v", ExitCode(err), err)
				}
			})
		}
	})
	t.Run("malformed machine-owned note", func(t *testing.T) {
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		name := buildBWSSecretName(meta.MachineID, scopeRef{kind: scopeKindShared}, "MALFORMED_KEY")
		payload, err := json.Marshal([]bwsSecret{{ID: "malformed-one", ProjectID: "fixture-project", Key: name, Note: "not-json", Value: "fixture-remote", RevisionDate: "revision-one"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stub.remoteData, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(envKinkoBWSBin, stub.remote)
		err = Run(append(base, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--force"), strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
}

func TestSyncPullStubBWSCreatesAndDeletesWithSharedToken(t *testing.T) {
	dataDir, configPath, dek := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ref := scopeRef{profile: "remote-profile", kind: scopeKindPath, path: filepath.Join(t.TempDir(), "remote-scope")}
	name := buildBWSSecretName(meta.MachineID, ref, "REMOTE_KEY")
	note, err := encodeBWSNote(bwsNoteMetadata{KinkoSyncFormat: 1, MachineID: meta.MachineID, Profile: ref.profile, Scope: ref.kind, Path: ref.path, Key: "REMOTE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal([]bwsSecret{{ID: "remote-id", ProjectID: "fixture-project", Key: name, Value: "fixture-remote", Note: note, RevisionDate: "revision-remote"}})
	if err != nil {
		t.Fatal(err)
	}
	stub := buildStubBWS(t, string(payload))
	t.Setenv(envKinkoBWSBin, stub.remote)
	t.Setenv(envKinkoBWSAccessToken, "")
	base := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
	vaultBefore := mustReadSyncTestFile(t, vaultPath)
	configBefore := mustReadSyncTestFile(t, configBlobPath)
	var dryRunOutput bytes.Buffer
	if err := Run(append(base, "--dry-run"), strings.NewReader("fixture-password\n"), &dryRunOutput, &bytes.Buffer{}); err != nil {
		t.Fatalf("pull dry-run failed: %v", err)
	}
	if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configBlobPath)) {
		t.Fatal("pull dry-run changed encrypted local files")
	}
	assertNoSyncFixtureLeak(t, dryRunOutput.String())
	var out bytes.Buffer
	if err := Run(base, strings.NewReader("fixture-password\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("pull create failed: %v", err)
	}
	assertNoSyncFixtureLeak(t, out.String())
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if data.Profiles[ref.profile][ref.path]["REMOTE_KEY"] != "fixture-remote" {
		t.Fatal("pull did not create the remote profile and path")
	}

	t.Setenv(envKinkoBWSBin, stub.success)
	if err := Run(base, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("pull delete failed: %v", err)
	}
	data, err = loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := data.Profiles[ref.profile][ref.path]["REMOTE_KEY"]; exists {
		t.Fatal("pull did not propagate the remote deletion")
	}
}

func TestSyncProjectMismatchWarningAndPartialPushPersistence(t *testing.T) {
	t.Run("project mismatch warning", func(t *testing.T) {
		stub := buildStubBWS(t)
		dataDir, configPath, dek := setupSyncE2EVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		config, err := loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		state := emptyBWSSyncState()
		state.MachineID = meta.MachineID
		state.ProjectID = "previous-project"
		if err := saveBWSSyncState(config, state); err != nil {
			t.Fatal(err)
		}
		if err := saveConfig(dataDir, dek, config); err != nil {
			t.Fatal(err)
		}
		t.Setenv(envKinkoBWSBin, stub.success)
		t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
		args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "current-project", "--dry-run"}
		var stderr bytes.Buffer
		if err := Run(args, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stderr.String(), "previous-project") || !strings.Contains(stderr.String(), "current-project") {
			t.Fatalf("missing project mismatch warning: %q", stderr.String())
		}
		assertNoSyncFixtureLeak(t, stderr.String())
	})

	t.Run("pull from empty new project preserves local secrets", func(t *testing.T) {
		stub := buildStubBWS(t)
		dataDir, configPath, dek := setupSyncE2EVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		config, err := loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		ref := scopeRef{kind: scopeKindShared}
		name := buildBWSSecretName(meta.MachineID, ref, "SHARED_KEY")
		state := emptyBWSSyncState()
		state.MachineID = meta.MachineID
		state.ProjectID = "previous-project"
		state.Entries[name] = syncTestStateEntry(name, ref, "SHARED_KEY", "fixture-shared", "revision-previous")
		if err := saveBWSSyncState(config, state); err != nil {
			t.Fatal(err)
		}
		if err := saveConfig(dataDir, dek, config); err != nil {
			t.Fatal(err)
		}
		before, err := loadVault(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(envKinkoBWSBin, stub.success)
		t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
		args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPull, "--provider", supportedSyncProvider, "--project-id", "current-project", "--force", "--json"}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		after, err := loadVault(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatal("pulling an empty new project changed local secrets")
		}
		config, err = loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		state, err = loadBWSSyncState(config)
		if err != nil {
			t.Fatal(err)
		}
		if state.ProjectID != "current-project" || len(state.Entries) != 0 {
			t.Fatalf("project=%q entries=%d", state.ProjectID, len(state.Entries))
		}
		if !strings.Contains(stderr.String(), "treating this project as never synced") {
			t.Fatalf("missing project reset warning: %q", stderr.String())
		}
		assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
	})

	t.Run("partial push persists applied prefix", func(t *testing.T) {
		stub := buildStubBWS(t)
		dataDir, configPath, dek := setupSyncE2EVault(t)
		t.Setenv(envKinkoBWSBin, stub.partial)
		t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
		args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		runErr := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr)
		if ExitCode(runErr) != exitCodeProviderFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(runErr), runErr)
		}
		var result syncResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode partial result: %v: %q", err, stdout.String())
		}
		if !result.Partial || result.Created != 1 || len(result.Actions) != 1 {
			t.Fatalf("partial=%t created=%d actions=%d", result.Partial, result.Created, len(result.Actions))
		}
		config, err := loadConfig(dataDir, dek)
		if err != nil {
			t.Fatal(err)
		}
		state, err := loadBWSSyncState(config)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Entries) != 1 || strings.Count(string(mustReadSyncTestFile(t, stub.journal)), "create ") != 1 {
			t.Fatalf("state=%d journal=%q", len(state.Entries), string(mustReadSyncTestFile(t, stub.journal)))
		}
		assertNoSyncFixtureLeak(t, stdout.String()+stderr.String()+runErr.Error())
	})
}

func TestSyncWrongPasswordStopsBeforeProviderAndLeavesFilesUnchanged(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, _ := setupSyncE2EVault(t)
	t.Setenv(envKinkoBWSBin, stub.stateful)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
	vaultBefore := mustReadSyncTestFile(t, vaultPath)
	configBefore := mustReadSyncTestFile(t, configBlobPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
	err := Run(args, strings.NewReader("wrong-password\n"), &stdout, &stderr)
	if ExitCode(err) != exitCodeAuthFailed {
		t.Fatalf("exit=%d err=%v", ExitCode(err), err)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "SHARED_KEY") || strings.Contains(stderr.String(), "LOCAL_KEY") {
		t.Fatalf("wrong-password output exposed sync scope: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if journal := mustReadOptionalSyncTestFile(t, stub.journal); len(journal) != 0 {
		t.Fatalf("wrong-password run reached provider mutation: %q", journal)
	}
	if calls := mustReadOptionalSyncTestFile(t, stub.callLog); len(calls) != 0 {
		t.Fatalf("wrong-password run reached provider: %q", calls)
	}
	if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configBlobPath)) {
		t.Fatal("wrong-password run changed encrypted local files")
	}
}

func TestSyncMachineMismatchStateWarnsAndUsesNeverSyncedBaseline(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, dek := setupSyncE2EVault(t)
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	otherMachineID := "fedcba9876543210"
	ref := scopeRef{kind: scopeKindShared}
	oldName := buildBWSSecretName(otherMachineID, ref, "OLD_KEY")
	state := &bwsSyncState{
		Format:    1,
		MachineID: otherMachineID,
		ProjectID: "fixture-project",
		Entries: map[string]syncStateEntry{
			oldName: syncTestStateEntry(oldName, ref, "OLD_KEY", "fixture-old", "revision-old"),
		},
	}
	if err := saveBWSSyncState(config, state); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(dataDir, dek, config); err != nil {
		t.Fatal(err)
	}
	configPathOnDisk := filepath.Join(dataDir, "vault", "config.v1.bin")
	configBefore := mustReadSyncTestFile(t, configPathOnDisk)
	t.Setenv(envKinkoBWSBin, stub.success)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--dry-run", "--json"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result syncResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Created != 3 || len(result.Actions) != 3 || !strings.Contains(stderr.String(), "another machine id") {
		t.Fatalf("created=%d actions=%d stderr=%q", result.Created, len(result.Actions), stderr.String())
	}
	if !bytes.Equal(configBefore, mustReadSyncTestFile(t, configPathOnDisk)) {
		t.Fatal("machine-mismatch dry-run rewrote sync state")
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}

func TestSyncPushPersistStateFailureReportsSummary(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, dek := setupSyncE2EVault(t)
	t.Setenv(envKinkoBWSBin, stub.success)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")

	// Fail only the sync-state persistence write (config.v1.bin) after the
	// remote push mutations have already succeeded, so the test exercises
	// the case where applyPushPlan applied changes but persistSyncState
	// could not record them.
	configBlobPath := filepath.Join(dataDir, "vault", "config.v1.bin")
	originalRename := atomicRename
	atomicRename = func(oldpath, newpath string) error {
		if newpath == configBlobPath {
			return errors.New("injected sync state persistence failure")
		}
		return originalRename(oldpath, newpath)
	}
	t.Cleanup(func() { atomicRename = originalRename })

	args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider", supportedSyncProvider, "--project-id", "fixture-project", "--json"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runErr := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr)
	if ExitCode(runErr) != exitCodeIOFailed {
		t.Fatalf("exit=%d err=%v", ExitCode(runErr), runErr)
	}

	var result syncResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode summary despite persist failure: %v: %q", err, stdout.String())
	}
	if result.Created != 3 || len(result.Actions) != 3 {
		t.Fatalf("created=%d actions=%d", result.Created, len(result.Actions))
	}

	journal := string(mustReadSyncTestFile(t, stub.journal))
	if strings.Count(journal, "create ") != 3 {
		t.Fatalf("remote mutations were not applied before the persistence failure: %q", journal)
	}

	// The vault and remote state changed, but the local sync-state record
	// could not be persisted; confirm the local config still reflects the
	// pre-push (never-synced) state rather than a torn write.
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadBWSSyncState(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 0 {
		t.Fatalf("sync state was persisted despite the injected failure: entries=%d", len(state.Entries))
	}

	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String()+runErr.Error())
}
