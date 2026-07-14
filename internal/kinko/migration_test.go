package kinko

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMigrationPreviewApplyIdempotenceAndDoctorLifecycle(t *testing.T) {
	dataDir := newLegacyMachineIDVault(t)
	opts := globalOptions{dataDir: dataDir}

	var doctorBefore bytes.Buffer
	if err := runDoctor(opts, &doctorBefore); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctorBefore.String(), "WARNING machine-id") {
		t.Fatalf("doctor before migration=%q", doctorBefore.String())
	}

	var preview bytes.Buffer
	if err := runMigrationWithOptions(opts, migrationOptions{}, strings.NewReader(""), &preview, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.String(), "pending assign-machine-id") {
		t.Fatalf("preview=%q", preview.String())
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MachineID != "" {
		t.Fatal("preview changed metadata")
	}

	var applied bytes.Buffer
	if err := runMigrationWithOptions(opts, migrationOptions{yes: true}, strings.NewReader("pw\n"), &applied, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied.String(), "applied assign-machine-id") {
		t.Fatalf("apply output=%q", applied.String())
	}
	meta, err = loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidMachineID(meta.MachineID) {
		t.Fatalf("migrated machine_id=%q", meta.MachineID)
	}

	var doctorAfter bytes.Buffer
	if err := runDoctor(opts, &doctorAfter); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doctorAfter.String(), "WARNING machine-id") {
		t.Fatalf("doctor after migration=%q", doctorAfter.String())
	}

	var second bytes.Buffer
	if err := runMigrationWithOptions(opts, migrationOptions{yes: true}, strings.NewReader(""), &second, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(second.String()) != "no pending migrations" {
		t.Fatalf("idempotent output=%q", second.String())
	}
}

func TestMigrationJSONAndCobraFlags(t *testing.T) {
	dataDir := newLegacyMachineIDVault(t)
	var out bytes.Buffer
	if err := Run([]string{"--kinko-dir", dataDir, cmdMigration, "--json"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result migrationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode migration JSON: %v", err)
	}
	if result.Mode != "preview" || len(result.Steps) != 1 || result.Steps[0].Name != "assign-machine-id" || !result.Steps[0].Pending {
		t.Fatalf("migration JSON=%+v", result)
	}

	out.Reset()
	if err := Run([]string{"--kinko-dir", dataDir, cmdMigration, "-y", "--json"}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	result = migrationResult{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Mode != "apply" || !result.Steps[0].Applied {
		t.Fatalf("apply JSON=%+v", result)
	}
}

func TestMigrationHelpDocumentsFlags(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{cmdMigration, "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, flag := range []string{"--yes", "-y,", "--json"} {
		if !strings.Contains(help, flag) {
			t.Fatalf("migration help does not contain %q: %q", flag, help)
		}
	}
}

func TestMigrationErrorClassification(t *testing.T) {
	t.Run("I/O", func(t *testing.T) {
		err := runMigrationWithOptions(globalOptions{dataDir: t.TempDir()}, migrationOptions{}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeIOFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})

	t.Run("policy", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		err := Run([]string{"--kinko-dir", dataDir, cmdMigration, "unexpected"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})

	t.Run("authentication", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		err := runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{yes: true}, strings.NewReader("wrong\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeAuthFailed {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})

	t.Run("unsafe KDF metadata", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		meta.KDFParamsPassword.Algorithm = "unsupported"
		if err := saveMeta(dataDir, meta); err != nil {
			t.Fatal(err)
		}
		err = runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{yes: true}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeMetadataInvalid {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})

	t.Run("malformed password salt encoding", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		meta.SaltPasswordB64 = "not-valid-base64"
		if err := saveMeta(dataDir, meta); err != nil {
			t.Fatal(err)
		}
		err = runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{yes: true}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeMetadataInvalid {
			t.Fatalf("exit=%d want=%d", ExitCode(err), exitCodeMetadataInvalid)
		}
	})

	t.Run("malformed wrapped DEK encoding", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		meta.WrappedDEKPassB64 = "not-valid-wrapped-dek"
		if err := saveMeta(dataDir, meta); err != nil {
			t.Fatal(err)
		}
		err = runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{yes: true}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeMetadataInvalid {
			t.Fatalf("exit=%d want=%d", ExitCode(err), exitCodeMetadataInvalid)
		}
	})

	t.Run("lock conflict", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		release, err := acquireMutationLock(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		err = runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{yes: true}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeLockConflict {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})

	t.Run("invalid metadata", func(t *testing.T) {
		dataDir := newLegacyMachineIDVault(t)
		meta, err := loadMeta(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		meta.MachineID = "invalid"
		if err := saveMeta(dataDir, meta); err != nil {
			t.Fatal(err)
		}
		err = runMigrationWithOptions(globalOptions{dataDir: dataDir}, migrationOptions{}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodeMetadataInvalid {
			t.Fatalf("exit=%d err=%v", ExitCode(err), err)
		}
	})
}

func TestMigrationAuthenticatesAgainstLockedMetadataSnapshot(t *testing.T) {
	dataDir := newLegacyMachineIDVault(t)
	opts := globalOptions{dataDir: dataDir}
	reader := &hookReader{
		Reader: strings.NewReader("pw\n"),
		hook: func() {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := runPassword(
				opts,
				[]string{"change", "--current-stdin", "--new-stdin"},
				strings.NewReader("pw\nchanged-password\n"),
				&stdout,
				&stderr,
			)
			if err != nil {
				t.Fatalf("change password during migration input: %v", err)
			}
		},
	}

	err := runMigrationWithOptions(opts, migrationOptions{yes: true}, reader, &bytes.Buffer{}, &bytes.Buffer{})
	if ExitCode(err) != exitCodeAuthFailed {
		t.Fatalf("exit=%d err=%v", ExitCode(err), err)
	}
	meta, loadErr := loadMeta(dataDir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if meta.MachineID != "" {
		t.Fatal("migration applied after authenticating against stale metadata")
	}
	if _, unwrapErr := unwrapDEKWithPassword(meta, "changed-password"); unwrapErr != nil {
		t.Fatalf("password change did not persist: %v", unwrapErr)
	}
}

type hookReader struct {
	io.Reader
	hook func()
	done bool
}

func (reader *hookReader) Read(buffer []byte) (int, error) {
	if !reader.done {
		reader.done = true
		reader.hook()
	}
	return reader.Reader.Read(buffer)
}

func newLegacyMachineIDVault(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.MachineID = ""
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}
	return dataDir
}
