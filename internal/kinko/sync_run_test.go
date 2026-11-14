package kinko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveBWSProjectIDPrecedenceAndFallback(t *testing.T) {
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			return []byte(`[{"id":"sole-project","name":"fixture"}]`), nil, nil
		},
	}
	t.Setenv(envKinkoBWSProjectID, "environment-project")
	value, err := resolveBWSProjectID(context.Background(), client, map[string]string{configKeyBWSProjectID: "config-project"}, "flag-project", &bytes.Buffer{})
	if err != nil || value != "flag-project" {
		t.Fatalf("flag value=%q err=%v", value, err)
	}
	value, err = resolveBWSProjectID(context.Background(), client, map[string]string{configKeyBWSProjectID: "config-project"}, "", &bytes.Buffer{})
	if err != nil || value != "environment-project" {
		t.Fatalf("environment value=%q err=%v", value, err)
	}
	t.Setenv(envKinkoBWSProjectID, "")
	value, err = resolveBWSProjectID(context.Background(), client, map[string]string{configKeyBWSProjectID: "config-project"}, "", &bytes.Buffer{})
	if err != nil || value != "config-project" {
		t.Fatalf("config value=%q err=%v", value, err)
	}
	var stderr bytes.Buffer
	value, err = resolveBWSProjectID(context.Background(), client, map[string]string{}, "", &stderr)
	if err != nil || value != "sole-project" || !strings.Contains(stderr.String(), "sole-project") {
		t.Fatalf("sole value=%q err=%v stderr=%q", value, err, stderr.String())
	}
}

func TestResolveBWSProjectIDUnresolvedAndProviderFailure(t *testing.T) {
	t.Setenv(envKinkoBWSProjectID, "")
	client := &bwsClient{
		binPath: "fixture-bws",
		token:   "fixture-token",
		timeout: time.Second,
		runner: func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			return []byte("[]"), nil, nil
		},
	}
	_, err := resolveBWSProjectID(context.Background(), client, map[string]string{}, "", &bytes.Buffer{})
	if ExitCode(err) != exitCodePolicyFailed {
		t.Fatalf("unresolved exit=%d", ExitCode(err))
	}
	client.runner = func(_ context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
		return nil, nil, errors.New("fixture failure")
	}
	_, err = resolveBWSProjectID(context.Background(), client, map[string]string{}, "", &bytes.Buffer{})
	if ExitCode(err) != exitCodeProviderFailed {
		t.Fatalf("provider exit=%d", ExitCode(err))
	}
}

func TestSyncPasswordClassificationPreservesMetadataErrors(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "fixture-password"); err != nil {
		t.Fatal(err)
	}
	opts := globalOptions{dataDir: dataDir}
	_, err := verifyVaultPasswordValue(opts, "wrong-password")
	if ExitCode(err) != exitCodeAuthFailed {
		t.Fatalf("wrong-password exit=%d", ExitCode(err))
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.SaltPasswordB64 = "not-base64"
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}
	_, err = verifyVaultPasswordValue(opts, "fixture-password")
	if ExitCode(err) != exitCodeMetadataInvalid {
		t.Fatalf("metadata exit=%d", ExitCode(err))
	}
}

func TestSyncLocalDataErrorClassification(t *testing.T) {
	pathFailure := &os.PathError{Op: "read", Path: "fixture-path", Err: errors.New("fixture I/O failure")}
	if err := syncLocalDataError("fixture read failed", pathFailure); ExitCode(err) != exitCodeIOFailed {
		t.Fatalf("path failure exit=%d", ExitCode(err))
	}
	if err := syncLocalDataError("fixture metadata invalid", errors.New("fixture decoded metadata failure")); ExitCode(err) != exitCodeMetadataInvalid {
		t.Fatalf("decoded metadata exit=%d", ExitCode(err))
	}
}

func TestLoadSyncMetadataClassifiesReadAndContentFailures(t *testing.T) {
	// runSyncWithOptions uses this helper for both its initial metadata read
	// and its reload after acquiring the mutation lock.
	t.Run("non-permission path failure is io", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := ensureDirLayout(dataDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dataDir, "vault", "meta.v1.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := runSyncWithOptions(
			globalOptions{dataDir: dataDir},
			syncOptions{direction: syncDirectionPull, provider: supportedSyncProvider, projectID: "fixture-project"},
			strings.NewReader(""),
			&bytes.Buffer{},
			&bytes.Buffer{},
		)
		if ExitCode(err) != exitCodeIOFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
	t.Run("malformed metadata is metadata invalid", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := ensureDirLayout(dataDir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "vault", "meta.v1.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := loadSyncMetadata(dataDir)
		if ExitCode(err) != exitCodeMetadataInvalid {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
}

func TestRunSyncClassifiesEncryptedFileReadAndMetadataFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*testing.T, string)
		wantExit int
	}{
		{
			name: "vault path read failure",
			mutate: func(t *testing.T, dataDir string) {
				path := filepath.Join(dataDir, "vault", "vault.v1.bin")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: exitCodeIOFailed,
		},
		{
			name: "malformed encrypted vault metadata",
			mutate: func(t *testing.T, dataDir string) {
				path := filepath.Join(dataDir, "vault", "vault.v1.bin")
				if err := os.WriteFile(path, []byte("not-an-encrypted-blob"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: exitCodeMetadataInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := ensureDirLayout(dataDir); err != nil {
				t.Fatal(err)
			}
			if err := initVault(dataDir, "fixture-password"); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, dataDir)
			err := runSyncWithOptions(
				globalOptions{dataDir: dataDir},
				syncOptions{direction: syncDirectionPull, provider: supportedSyncProvider, projectID: "fixture-project"},
				strings.NewReader("fixture-password\n"),
				&bytes.Buffer{},
				&bytes.Buffer{},
			)
			if ExitCode(err) != test.wantExit {
				t.Fatalf("exit=%d want=%d err=%v", ExitCode(err), test.wantExit, err)
			}
		})
	}
}

func TestPrintSyncSummaryJSONContainsNoValueFields(t *testing.T) {
	result := syncResult{
		Created: 1,
		Actions: []syncResultItem{{Action: "create", Scope: "shared", Key: "FIXTURE_KEY"}},
	}
	var output bytes.Buffer
	if err := printSyncSummary(&output, result, true); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(output.String()), "value") || strings.Contains(output.String(), "fixture-secret") {
		t.Fatal("JSON summary exposed a value-bearing field")
	}
	want := "{\"created\":1,\"updated\":0,\"deleted\":0,\"unchanged\":0,\"adopted\":0,\"conflicts\":[],\"actions\":[{\"action\":\"create\",\"scope\":\"shared\",\"key\":\"FIXTURE_KEY\"}],\"partial\":false}\n"
	if output.String() != want {
		t.Fatalf("output=%q want %q", output.String(), want)
	}
}

func TestPrintSyncSummaryHumanIsStableAndContainsNoValues(t *testing.T) {
	result := syncResult{
		Updated: 1,
		Actions: []syncResultItem{{Action: "update", Profile: "fixture-profile", Scope: "path", Path: "/fixture/path", Key: "FIXTURE_KEY"}},
	}
	var output bytes.Buffer
	if err := printSyncSummary(&output, result, false); err != nil {
		t.Fatal(err)
	}
	want := "update profile=\"fixture-profile\" path=\"/fixture/path\" / FIXTURE_KEY\ncreated=0 updated=1 deleted=0 adopted=0 unchanged=0 conflicts=0 partial=false\n"
	if output.String() != want {
		t.Fatalf("output=%q want %q", output.String(), want)
	}
	if strings.Contains(output.String(), "fixture-secret-value") {
		t.Fatal("human summary exposed a secret value")
	}
}

func TestProviderCLIErrorSurfacesRedactedDiagnostic(t *testing.T) {
	diagnostic := "rate limit exceeded; retry after 30 seconds; token=[REDACTED]"
	err := providerCLIError("BWS sync did not complete.", fmt.Errorf("%w: %s", errBWSCommandFailed, diagnostic))
	if ExitCode(err) != exitCodeProviderFailed {
		t.Fatalf("exit=%d err=%v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), diagnostic) {
		t.Fatalf("human error omitted provider diagnostic: %q", err.Error())
	}
	if strings.Contains(err.Error(), "fixture-token") || strings.Contains(err.Error(), "fixture-secret-value") {
		t.Fatal("human provider error leaked a token or secret value")
	}
}

func TestRunSyncMissingMachineIDFailsBeforePasswordOrProvider(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "fixture-password"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.MachineID = ""
	if err := saveMetaAtomically(dataDir, meta); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKinkoBWSBin, filepath.Join(t.TempDir(), "missing-bws"))
	input := strings.NewReader("fixture-password\n")
	before := input.Len()
	err = runSyncWithOptions(
		globalOptions{dataDir: dataDir},
		syncOptions{direction: syncDirectionPush, provider: supportedSyncProvider, projectID: "fixture-project"},
		input,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), cmdMigration) {
		t.Fatalf("exit=%d err=%v", ExitCode(err), err)
	}
	if input.Len() != before {
		t.Fatal("missing machine id consumed password input")
	}
}
