package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunDoctorWarnsForLegacySessionKeyMetadata(t *testing.T) {
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
	meta.SessionKeySource = ""
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runDoctor(globalOptions{dataDir: dataDir}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING legacy-session-key") {
		t.Fatalf("expected legacy warning, got %q", out.String())
	}
}

func TestRunDoctorReportsRandomSessionKeyMetadata(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runDoctor(globalOptions{dataDir: dataDir}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "OK session-key-source") {
		t.Fatalf("expected OK diagnostic, got %q", out.String())
	}
}

func TestRunDoctorWarnsForCorruptSessionToken(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "lock", "session.token"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runDoctor(globalOptions{dataDir: dataDir}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING session-token: session token is not valid JSON") {
		t.Fatalf("expected session token warning, got %q", out.String())
	}
}

func TestRunDoctorWarnsForMissingSessionWrapKey(t *testing.T) {
	fake := withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	for key := range fake.data {
		delete(fake.data, key)
	}

	var out bytes.Buffer
	err := runDoctor(globalOptions{dataDir: dataDir}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING session-token: active session token exists but session wrap key cannot be loaded") {
		t.Fatalf("expected session wrap warning, got %q", out.String())
	}
}

func TestRunCobraDoctorWiring(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Run([]string{"--kinko-dir", dataDir, cmdDoctor}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "OK session-key-source") {
		t.Fatalf("expected doctor output, got %q", out.String())
	}
}
