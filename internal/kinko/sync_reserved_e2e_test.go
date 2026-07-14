package kinko

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncRejectsDuplicateReservedRemoteNames(t *testing.T) {
	stub := buildStubBWS(t)
	dataDir, configPath, _ := setupSyncE2EVault(t)
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ref := scopeRef{kind: scopeKindShared}
	name := buildBWSSecretName(meta.MachineID, ref, sharedKeyBWSAccessToken)
	first := syncTestRemote(t, name, ref, sharedKeyBWSAccessToken, "fixture-token-one", "revision-one")
	second := syncTestRemote(t, name, ref, sharedKeyBWSAccessToken, "fixture-token-two", "revision-two")
	second.ID = "fixture-id-reserved-second"
	payload, err := json.Marshal([]bwsSecret{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub.remoteData, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKinkoBWSBin, stub.remote)
	t.Setenv(envKinkoBWSAccessToken, "fixture-kinko-token")
	vaultPath := filepath.Join(dataDir, "vault", "vault.v1.bin")
	configPathOnDisk := filepath.Join(dataDir, "vault", "config.v1.bin")
	vaultBefore := mustReadSyncTestFile(t, vaultPath)
	configBefore := mustReadSyncTestFile(t, configPathOnDisk)

	for _, test := range []struct {
		direction string
		force     bool
	}{
		{direction: cmdSyncPush},
		{direction: cmdSyncPull},
		{direction: cmdSyncPush, force: true},
		{direction: cmdSyncPull, force: true},
	} {
		name := test.direction
		if test.force {
			name += " with force"
		}
		t.Run(name, func(t *testing.T) {
			args := []string{"--kinko-dir", dataDir, "--config", configPath, cmdSync, test.direction, "--provider", supportedSyncProvider, "--project-id", "fixture-project"}
			if test.force {
				args = append(args, "--force")
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			runErr := Run(args, strings.NewReader("fixture-password\n"), &stdout, &stderr)
			if ExitCode(runErr) != exitCodePolicyFailed {
				t.Fatalf("exit=%d err=%v", ExitCode(runErr), runErr)
			}
			if !strings.Contains(runErr.Error(), "Duplicate machine-owned BWS secret names") {
				t.Fatalf("error=%q", runErr.Error())
			}
			if journal := mustReadOptionalSyncTestFile(t, stub.journal); len(journal) != 0 {
				t.Fatalf("duplicate-name validation performed remote mutation: %q", journal)
			}
			if !bytes.Equal(vaultBefore, mustReadSyncTestFile(t, vaultPath)) || !bytes.Equal(configBefore, mustReadSyncTestFile(t, configPathOnDisk)) {
				t.Fatal("duplicate-name validation mutated encrypted local files")
			}
			assertNoSyncFixtureLeak(t, stdout.String()+stderr.String()+runErr.Error())
		})
	}
}
