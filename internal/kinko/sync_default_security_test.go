package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSyncPushRejectsArgvMutation(t *testing.T) {
	dataDir, configPath, dek := setupSyncE2EVault(t)
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	data.Profiles = nil
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "bws")
	marker := filepath.Join(directory, "mutation")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'bws 2.0.0'; exit 0; fi\nif [ \"$1 $2\" = 'secret list' ]; then echo '[]'; exit 0; fi\n: > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", directory)
	t.Setenv(envKinkoBWSConfigFile, "")
	t.Setenv(envKinkoBWSServerURL, "")
	t.Setenv(envKinkoBWSBin, binary)
	t.Setenv(envKinkoBWSAccessToken, "")
	var stdout, stderr bytes.Buffer
	err = Run([]string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider=bws"}, strings.NewReader("fixture-password\n"), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "value-safe-mutation") {
		t.Fatalf("default push did not reject unsafe mutation: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unexpected mutation invocation: %v", err)
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}

func TestDefaultSyncValidatesConfiguredEndpointBeforeProvider(t *testing.T) {
	for _, direction := range []string{cmdSyncPush, cmdSyncPull} {
		t.Run(direction, func(t *testing.T) {
			dataDir, configPath, _ := setupSyncE2EVault(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv(envKinkoBWSConfigFile, "")
			t.Setenv(envKinkoBWSServerURL, "http://example.invalid")
			t.Setenv(envKinkoBWSBin, filepath.Join(t.TempDir(), "missing-bws"))
			err := Run([]string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, direction, "--provider=bws"}, strings.NewReader("fixture-password\n"), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
				t.Fatalf("endpoint validation bypassed: %v", err)
			}
		})
	}
}

func TestSyncCLIExplicitLegacyAcknowledgementPushesThroughV2(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, dek := setupSyncE2EVault(t)
	data, err := loadVault(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	data.Profiles = nil
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envKinkoBWSConfigFile, "")
	t.Setenv(envKinkoBWSServerURL, "")
	t.Setenv(envKinkoBWSBin, stub.stateful)
	t.Setenv(envKinkoBWSAccessToken, "")
	var stdout, stderr bytes.Buffer
	args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, cmdSyncPush, "--provider=bws", "--bws-transport=cli-legacy", "--allow-secret-argv"}
	if err := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeBWSSyncState(config[configKeyBWSSyncState])
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeBWSSyncStateV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 1 || strings.Count(string(mustReadSyncTestFile(t, stub.journal)), "create ") != 1 {
		t.Fatal("acknowledged CLI push did not persist its V2 baseline and one remote mutation")
	}
	if !strings.Contains(stderr.String(), "exposes secret values") {
		t.Fatal("acknowledged argv mutation omitted warning")
	}
	assertNoSyncFixtureLeak(t, stdout.String()+stderr.String())
}
