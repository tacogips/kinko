package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun_CobraBasedRegression_AllCommands(t *testing.T) {
	withFakeSessionStore(t)

	t.Run("version", func(t *testing.T) {
		var out bytes.Buffer
		if err := Run([]string{"version"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Fatal("expected non-empty version output")
		}
	})

	t.Run("init unlock status lock", func(t *testing.T) {
		dataDir := t.TempDir()

		var initOut bytes.Buffer
		initIn := strings.NewReader("pw123456\npw123456\n")
		if err := Run([]string{"--kinko-dir", dataDir, "init"}, initIn, &initOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("init failed: %v", err)
		}

		var unlockOut bytes.Buffer
		unlockIn := strings.NewReader("pw123456\n")
		if err := Run([]string{"--kinko-dir", dataDir, "unlock", "--timeout", "5m"}, unlockIn, &unlockOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("unlock failed: %v", err)
		}
		if !strings.Contains(unlockOut.String(), "unlocked") {
			t.Fatalf("unexpected unlock output: %q", unlockOut.String())
		}

		var statusOut bytes.Buffer
		if err := Run([]string{"--kinko-dir", dataDir, "status"}, strings.NewReader(""), &statusOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
		if !strings.Contains(statusOut.String(), "unlocked") {
			t.Fatalf("unexpected status output: %q", statusOut.String())
		}

		if err := Run([]string{"--kinko-dir", dataDir, "lock"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("lock failed: %v", err)
		}
	})

	t.Run("set set-key get show delete", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}

		if err := Run(append(base, "set", "A=one", "B=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set failed: %v", err)
		}
		if err := Run(append(base, "set-key", "C", "--value", "three"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set-key failed: %v", err)
		}

		var getOut bytes.Buffer
		if err := Run(append(base, "get", "C", "--reveal", "--force"), strings.NewReader(""), &getOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got := getOut.String(); got != "three\n" {
			t.Fatalf("unexpected get output: %q", got)
		}

		var showOut bytes.Buffer
		if err := Run(append(base, "show", "--reveal", "--force"), strings.NewReader(""), &showOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("show failed: %v", err)
		}
		if !strings.Contains(showOut.String(), "C=three") {
			t.Fatalf("unexpected show output: %q", showOut.String())
		}

		if err := Run(append(base, "delete", "C", "--yes"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("delete failed: %v", err)
		}
	})

	t.Run("config", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}

		if err := Run(append(base, "config", "set", "editor", "vim"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("config set failed: %v", err)
		}
		var out bytes.Buffer
		if err := Run(append(base, "config", "show"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("config show failed: %v", err)
		}
		if !strings.Contains(out.String(), "editor=vim") {
			t.Fatalf("unexpected config output: %q", out.String())
		}
	})

	t.Run("export import", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}

		if err := Run(append(base, "set", "--shared", "SHARED_ONLY=shared"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set shared failed: %v", err)
		}
		if err := Run(append(base, "set", "IMPORT_ME=hello"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set failed: %v", err)
		}

		var exported bytes.Buffer
		if err := Run(append(base, "export", "bash", "--exclude", "NOPE", "--force"), strings.NewReader(""), &exported, &bytes.Buffer{}); err != nil {
			t.Fatalf("export failed: %v", err)
		}
		if !strings.Contains(exported.String(), "IMPORT_ME") {
			t.Fatalf("unexpected export output: %q", exported.String())
		}

		var sharedOnly bytes.Buffer
		if err := Run(append(base, "export", "bash", "--shared-only", "--force"), strings.NewReader(""), &sharedOnly, &bytes.Buffer{}); err != nil {
			t.Fatalf("shared-only export failed: %v", err)
		}
		if !strings.Contains(sharedOnly.String(), "SHARED_ONLY") {
			t.Fatalf("shared-only export missing shared key: %q", sharedOnly.String())
		}
		if strings.Contains(sharedOnly.String(), "IMPORT_ME") {
			t.Fatalf("shared-only export unexpectedly contains repo key: %q", sharedOnly.String())
		}

		dst := setupUnlockedForSet(t)
		dstBase := []string{"--kinko-dir", dst.dataDir, "--path", dst.path, "--profile", dst.profile}
		if err := Run(append(dstBase, "import", "bash", "--yes"), strings.NewReader(exported.String()), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("import failed: %v", err)
		}
		if got := valueAtScope(t, dst, "IMPORT_ME"); got != "hello" {
			t.Fatalf("imported value=%q want=%q", got, "hello")
		}
	})

	t.Run("exec", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
		if err := Run(append(base, "set", "EXEC_KEY=ok"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set failed: %v", err)
		}
		if err := Run(append(base, "exec", "--env", "EXEC_KEY", "--", "true"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("exec failed: %v", err)
		}
	})

	t.Run("direnv export", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		repoRoot := filepath.Join(t.TempDir(), "repo")
		if err := os.MkdirAll(repoRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		envrcPath := filepath.Join(repoRoot, ".envrc")
		if err := os.WriteFile(envrcPath, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}

		base := []string{"--kinko-dir", opts.dataDir, "--path", repoRoot, "--profile", opts.profile}
		if err := Run(append(base, "set", "DIRENV_KEY=ok"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("set failed: %v", err)
		}

		t.Setenv("DIRENV_DIR", "-"+envrcPath)
		var out bytes.Buffer
		if err := Run([]string{"--kinko-dir", opts.dataDir, "--path", filepath.Join(t.TempDir(), "other"), "--profile", opts.profile, "direnv", "export"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("direnv export failed: %v", err)
		}
		if !strings.Contains(out.String(), "export DIRENV_KEY='ok'") {
			t.Fatalf("unexpected direnv export output: %q", out.String())
		}

		var shellOut bytes.Buffer
		if err := Run([]string{"--kinko-dir", opts.dataDir, "--path", filepath.Join(t.TempDir(), "other"), "--profile", opts.profile, "direnv", "export", "bash"}, strings.NewReader(""), &shellOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("direnv export bash failed: %v", err)
		}
		if !strings.Contains(shellOut.String(), "export DIRENV_KEY='ok'") {
			t.Fatalf("unexpected direnv export bash output: %q", shellOut.String())
		}
	})

	t.Run("password change", func(t *testing.T) {
		opts := setupPasswordChangeFixture(t, "current-password-123")
		if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
			t.Fatal(err)
		}
		in := strings.NewReader("current-password-123\nnext-password-456\n")
		if err := Run([]string{"--kinko-dir", opts.dataDir, "password", "change", "--current-stdin", "--new-stdin"}, in, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("password change failed: %v", err)
		}
		if err := unlockSession(opts.dataDir, 5*time.Minute, "next-password-456"); err != nil {
			t.Fatalf("new password should unlock: %v", err)
		}
	})

	t.Run("explosion command wiring", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		in := strings.NewReader("wrong-password\n")
		err := Run([]string{"--kinko-dir", opts.dataDir, "explosion"}, in, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected explosion to fail with wrong password")
		}
		if !strings.Contains(err.Error(), "password verification failed") {
			t.Fatalf("unexpected explosion error: %v", err)
		}
	})
}

func TestRun_NoArgs_ShowsCobraHelp(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run(nil, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Usage:") {
		t.Fatalf("expected Cobra usage output, got: %q", got)
	}
	if !strings.Contains(got, "Available Commands:") {
		t.Fatalf("expected Cobra command list, got: %q", got)
	}
	if strings.Contains(got, "Use: kinko <subcommand>") {
		t.Fatalf("legacy no-command hint should not be shown, got: %q", got)
	}
}
