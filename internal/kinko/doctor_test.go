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

// TestDiagnoseSessionToken_MissingTokenIsSilentNoWarning verifies the happy
// path: a genuinely absent session token file (os.ErrNotExist) must still
// produce no diagnostic at all, confirming no regression from the fix that
// makes other read errors surface a warning.
func TestDiagnoseSessionToken_MissingTokenIsSilentNoWarning(t *testing.T) {
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

	// No session.token file has been created (never unlocked).
	warnings := diagnoseSessionToken(dataDir, meta)
	if warnings != nil {
		t.Fatalf("expected no diagnostics for missing token, got %v", warnings)
	}
}

// TestDiagnoseSessionToken_NonNotExistReadErrorProducesWarning guards
// against the bug where ANY error reading the session token (permission
// denied, I/O error, corruption, etc.) was treated identically to "no
// token present" and silently produced no diagnostic. Only os.ErrNotExist
// may be silent; every other read error must surface a warning so real
// problems are not hidden from the user. A directory is used in place of
// the token file to force a portable non-ENOENT read error ("is a
// directory") without relying on permission semantics that vary across
// test sandboxes.
func TestDiagnoseSessionToken_NonNotExistReadErrorProducesWarning(t *testing.T) {
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

	tokenPath := filepath.Join(dataDir, "lock", "session.token")
	if err := os.MkdirAll(tokenPath, 0o700); err != nil {
		t.Fatal(err)
	}

	warnings := diagnoseSessionToken(dataDir, meta)
	if len(warnings) == 0 {
		t.Fatal("expected a warning diagnostic for a non-ENOENT session token read error, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "WARNING session-token") && strings.Contains(w, "could not be read") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'could not be read' warning, got %v", warnings)
	}
}

// TestRunDoctorWarnsForUnreadableSessionToken exercises the same scenario
// through runDoctor end-to-end, confirming doctor surfaces the warning
// (rather than printing the silent "OK session-token" line) when the token
// path exists but cannot be read as a regular file.
func TestRunDoctorWarnsForUnreadableSessionToken(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dataDir, "lock", "session.token")
	if err := os.MkdirAll(tokenPath, 0o700); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runDoctor(globalOptions{dataDir: dataDir}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING session-token: session token could not be read") {
		t.Fatalf("expected unreadable session-token warning, got %q", out.String())
	}
	if strings.Contains(out.String(), "OK session-token") {
		t.Fatalf("doctor must not print silent OK when session token is unreadable, got %q", out.String())
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
