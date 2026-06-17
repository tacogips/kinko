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
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")

		var initOut bytes.Buffer
		initIn := strings.NewReader("pw123456\npw123456\n")
		if err := Run([]string{"--kinko-dir", dataDir, "--config", configPath, "init"}, initIn, &initOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("init failed: %v", err)
		}

		var unlockOut bytes.Buffer
		unlockIn := strings.NewReader("pw123456\n")
		if err := Run([]string{"--kinko-dir", dataDir, "--config", configPath, "unlock", "--timeout", "5m"}, unlockIn, &unlockOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("unlock failed: %v", err)
		}
		if !strings.Contains(unlockOut.String(), "unlocked") {
			t.Fatalf("unexpected unlock output: %q", unlockOut.String())
		}

		var statusOut bytes.Buffer
		if err := Run([]string{"--kinko-dir", dataDir, "--config", configPath, "status"}, strings.NewReader(""), &statusOut, &bytes.Buffer{}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
		if !strings.Contains(statusOut.String(), "unlocked") {
			t.Fatalf("unexpected status output: %q", statusOut.String())
		}

		if err := Run([]string{"--kinko-dir", dataDir, "--config", configPath, "lock"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
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

	t.Run("profile list", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path}

		if err := Run(append(base, "--profile", "default", "set", "DEFAULT_KEY=one"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("default set failed: %v", err)
		}
		if err := Run(append(base, "--profile", "prod", "set", "PROD_KEY=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("prod set failed: %v", err)
		}

		var out bytes.Buffer
		if err := Run(append(base, "profile", "list"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("profile list failed: %v", err)
		}
		if got := strings.TrimSpace(out.String()); got != "default\nprod" {
			t.Fatalf("unexpected profile list output: %q", got)
		}
	})

	t.Run("path prune-missing", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		missingPath := filepath.Join(t.TempDir(), "missing")
		setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "STALE=one")
		base := []string{"--kinko-dir", opts.dataDir, "--path", filepath.Join(t.TempDir(), "ignored"), "--profile", opts.profile}

		var out bytes.Buffer
		if err := Run(append(base, "path", "prune-missing"), strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("path prune-missing failed: %v", err)
		}
		if !strings.Contains(out.String(), "candidate profile=default path="+missingPath+" keys=1") {
			t.Fatalf("unexpected path prune-missing output: %q", out.String())
		}
	})

	t.Run("set-key explicit empty value", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}

		var out bytes.Buffer
		if err := Run(append(base, "set-key", "EMPTY", "--value="), strings.NewReader("ignored\n"), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("set-key failed: %v", err)
		}
		if got := valueAtScope(t, opts, "EMPTY"); got != "" {
			t.Fatalf("EMPTY=%q", got)
		}
		if out.String() != "EMPTY set\n" {
			t.Fatalf("unexpected set-key output: %q", out.String())
		}
	})

	t.Run("backup", func(t *testing.T) {
		opts := setupBackupFixture(t)
		destDir := t.TempDir()
		base := []string{"--kinko-dir", opts.dataDir, "--config", opts.configPath, "backup", "--dest-path", destDir, "--current-stdin"}

		var out bytes.Buffer
		if err := Run(base, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
		archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("backup archive missing: %v", err)
		}
	})

	t.Run("backup without bootstrap config", func(t *testing.T) {
		opts := setupBackupFixture(t)
		if err := os.Remove(opts.configPath); err != nil {
			t.Fatalf("remove bootstrap config: %v", err)
		}
		destDir := t.TempDir()
		base := []string{"--kinko-dir", opts.dataDir, "--config", opts.configPath, "backup", "--dest-path", destDir, "--current-stdin"}

		var out bytes.Buffer
		if err := Run(base, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("backup failed without bootstrap config: %v", err)
		}
		archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("backup archive missing: %v", err)
		}
	})

	t.Run("backup defaults to current directory", func(t *testing.T) {
		opts := setupBackupFixture(t)
		cwd := t.TempDir()
		oldWD, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		defer func() {
			if chdirErr := os.Chdir(oldWD); chdirErr != nil {
				t.Fatalf("restore cwd: %v", chdirErr)
			}
		}()

		base := []string{"--kinko-dir", opts.dataDir, "--config", opts.configPath, "backup", "--current-stdin"}
		var out bytes.Buffer
		if err := Run(base, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
		archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
		gotDir, wantDir := filepath.Dir(archivePath), cwd
		if resolvedGot, err := filepath.EvalSymlinks(gotDir); err == nil {
			gotDir = resolvedGot
		}
		if resolvedWant, err := filepath.EvalSymlinks(wantDir); err == nil {
			wantDir = resolvedWant
		}
		if gotDir != wantDir {
			t.Fatalf("backup should default to cwd: got %q want dir %q", archivePath, cwd)
		}
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("backup archive missing: %v", err)
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

	t.Run("password change rejects unchanged password", func(t *testing.T) {
		opts := setupPasswordChangeFixture(t, "current-password-123")
		in := strings.NewReader("current-password-123\ncurrent-password-123\n")
		err := Run([]string{"--kinko-dir", opts.dataDir, "password", "change", "--current-stdin", "--new-stdin"}, in, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected unchanged password rejection")
		}
		if code := ExitCode(err); code != exitCodePolicyFailed {
			t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodePolicyFailed, err)
		}
		if got := err.Error(); got != "New password must differ from current password." {
			t.Fatalf("unexpected error message: %q", got)
		}
		if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
			t.Fatalf("current password should remain valid after rejection: %v", err)
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

func TestRun_PathPruneMissingRegisteredThroughCobra(t *testing.T) {
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	missingPath := filepath.Join(t.TempDir(), "missing")
	setPathScopeForPruneTest(t, opts, opts.profile, missingPath, "A=one")

	var out bytes.Buffer
	if err := Run([]string{"--kinko-dir", opts.dataDir, "--profile", opts.profile, "path", "prune-missing", "--json"}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"mode":"preview"`) {
		t.Fatalf("expected JSON preview output through Cobra, got %q", out.String())
	}
	if !strings.Contains(out.String(), missingPath) {
		t.Fatalf("expected stale path through Cobra, got %q", out.String())
	}
}

func TestRun_PathPruneMissingRejectsExplicitProfileWithAllProfiles(t *testing.T) {
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	devMissing := filepath.Join(t.TempDir(), "dev-missing")
	setPathScopeForPruneTest(t, opts, "dev", devMissing, "DEV=one")

	var out bytes.Buffer
	args := []string{"--kinko-dir", opts.dataDir, "--profile", "selected", "path", "prune-missing", "--all-profiles"}
	err := Run(args, strings.NewReader("pw\n"), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected explicit --profile with --all-profiles to fail")
	}
	if !strings.Contains(err.Error(), "cannot combine --all-profiles with explicit --profile") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on argument rejection, got %q", out.String())
	}
}

func TestRun_ShowAllScopesRequiresPasswordThroughCobra(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "--shared", "SHARED_ONLY=shared"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set shared failed: %v", err)
	}

	var failedOut bytes.Buffer
	var failedErr bytes.Buffer
	err := Run(append(base, "show", "--all-scopes"), strings.NewReader("wrong\n"), &failedOut, &failedErr)
	if err == nil {
		t.Fatal("expected all-scopes password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if failedOut.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", failedOut.String())
	}
	if !strings.Contains(failedErr.String(), "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", failedErr.String())
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run(append(base, "show", "--all-scopes"), strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatalf("show all-scopes failed after password verification: %v", err)
	}
	if !strings.Contains(out.String(), "# profile=default\n\n# shared\n") || !strings.Contains(out.String(), "SHARED_ONLY=") {
		t.Fatalf("unexpected all-scopes output: %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", errBuf.String())
	}
}

func TestRun_DeleteAllRequiresPasswordThroughCobra(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "A=one", "B=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run(append(base, "delete", "--all", "--yes"), strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected delete --all password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", gotErr)
	}
	if strings.Contains(gotErr, "Delete target keys") || strings.Contains(gotErr, "- A") || strings.Contains(gotErr, "- B") {
		t.Fatalf("auth failure must not list target keys, got stderr %q", gotErr)
	}
	if got := valueAtScope(t, opts, "A"); got != "one" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "two" {
		t.Fatalf("B=%q", got)
	}
}

func TestRun_DeleteAllDeclineSkipsPasswordThroughCobra(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "A=one", "B=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run(append(base, "delete", "--all"), strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatalf("delete --all decline failed: %v", err)
	}
	if out.String() != "aborted\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("declined delete-all must not prompt for password, got stderr %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- B\n") {
		t.Fatalf("expected target keys before confirmation, got stderr %q", gotErr)
	}
	if got := valueAtScope(t, opts, "A"); got != "one" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "two" {
		t.Fatalf("B=%q", got)
	}
}

func TestRun_DeleteSharedAllRequiresPasswordThroughCobra(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "--shared", "A=one", "B=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set shared failed: %v", err)
	}
	if err := Run(append(base, "set", "REPO_KEY=repo"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run(append(base, "delete", "--shared", "--all", "--yes"), strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected delete --shared --all password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", gotErr)
	}
	if strings.Contains(gotErr, "Delete target keys") || strings.Contains(gotErr, "- A") || strings.Contains(gotErr, "- B") {
		t.Fatalf("auth failure must not list shared target keys, got stderr %q", gotErr)
	}
	if got := valueAtShared(t, opts, "A"); got != "one" {
		t.Fatalf("A(shared)=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "two" {
		t.Fatalf("B(shared)=%q", got)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestRun_DeleteSharedAllDeclineSkipsPasswordThroughCobra(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "--shared", "A=one", "B=two"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set shared failed: %v", err)
	}
	if err := Run(append(base, "set", "REPO_KEY=repo"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run(append(base, "delete", "--shared", "--all"), strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatalf("delete --shared --all decline failed: %v", err)
	}
	if out.String() != "aborted\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("declined shared delete-all must not prompt for password, got stderr %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- B\n") {
		t.Fatalf("expected shared target keys before confirmation, got stderr %q", gotErr)
	}
	if got := valueAtShared(t, opts, "A"); got != "one" {
		t.Fatalf("A(shared)=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "two" {
		t.Fatalf("B(shared)=%q", got)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestCobraMoveCommands(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "set", "MOVE_LOCAL=local"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set local failed: %v", err)
	}

	var out bytes.Buffer
	if err := Run(append(base, "move", "local-to-shared", "MOVE_LOCAL", "--yes"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("local-to-shared failed: %v", err)
	}
	if got := valueAtShared(t, opts, "MOVE_LOCAL"); got != "local" {
		t.Fatalf("shared MOVE_LOCAL=%q", got)
	}

	out.Reset()
	if err := Run(append(base, "move", "shared-to-local", "MOVE_LOCAL", "-y"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("shared-to-local failed: %v", err)
	}
	if got := valueAtScope(t, opts, "MOVE_LOCAL"); got != "local" {
		t.Fatalf("local MOVE_LOCAL=%q", got)
	}
	vd := loadVaultForMoveTest(t, opts)
	if _, ok := vd.Shared["MOVE_LOCAL"]; ok {
		t.Fatal("expected shared source to be deleted")
	}
}

func TestCobraCopyCommands(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	sourcePath := filepath.Join(t.TempDir(), "source")
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append([]string{"--kinko-dir", opts.dataDir, "--path", sourcePath, "--profile", opts.profile}, "set", "COPY_LOCAL=local"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("set source local failed: %v", err)
	}

	var out bytes.Buffer
	if err := Run(append(base, "copy", "local-to-local", "COPY_LOCAL", "--from-path", sourcePath), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("local-to-local copy failed: %v", err)
	}
	if got := valueAtScope(t, opts, "COPY_LOCAL"); got != "local" {
		t.Fatalf("local COPY_LOCAL=%q", got)
	}

	out.Reset()
	if err := Run(append(base, "copy", "local-to-shared", "COPY_LOCAL"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("local-to-shared copy failed: %v", err)
	}
	if got := valueAtShared(t, opts, "COPY_LOCAL"); got != "local" {
		t.Fatalf("shared COPY_LOCAL=%q", got)
	}

	destinationPath := filepath.Join(t.TempDir(), "destination")
	out.Reset()
	destinationBase := []string{"--kinko-dir", opts.dataDir, "--path", destinationPath, "--profile", opts.profile}
	if err := Run(append(destinationBase, "copy", "shared-to-local", "COPY_LOCAL"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("shared-to-local copy failed: %v", err)
	}
	destinationOpts := opts
	destinationOpts.path = filepath.Clean(destinationPath)
	if got := valueAtScope(t, destinationOpts, "COPY_LOCAL"); got != "local" {
		t.Fatalf("destination COPY_LOCAL=%q", got)
	}
}

func TestCobraCopyHelpIncludesDirections(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"copy", "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"local-to-local", "local-to-shared", "shared-to-local"} {
		if !strings.Contains(got, want) {
			t.Fatalf("copy help missing %s: %q", want, got)
		}
	}
}

func TestCobraMoveHelpIncludesDirections(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"move", "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "local-to-shared") || !strings.Contains(got, "shared-to-local") {
		t.Fatalf("move help missing directions: %q", got)
	}
}

func TestCobraMoveRejectsInvalidArgs(t *testing.T) {
	withFakeSessionStore(t)

	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--path", opts.path, "--profile", opts.profile}
	tests := []struct {
		name string
		args []string
	}{
		{name: "parent positional", args: append(base, "move", "NOPE")},
		{name: "missing key", args: append(base, "move", "local-to-shared")},
		{name: "extra key", args: append(base, "move", "shared-to-local", "A", "B")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(tc.args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil {
				t.Fatal("expected invalid args error")
			}
		})
	}
}

func TestRun_CobraHelpIncludesProfileCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "profile") {
		t.Fatalf("root help missing profile command: %q", out.String())
	}
}

func TestRun_CobraHelpForProfileShowsList(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"profile", "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "list") {
		t.Fatalf("profile help missing list subcommand: %q", out.String())
	}
}

func TestRun_CobraHelpDoesNotExposeImplicitCompletion(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "completion") {
		t.Fatalf("root help should not expose Cobra default completion command: %q", out.String())
	}
}

func TestRun_CobraRejectsImplicitCompletionCommand(t *testing.T) {
	err := Run([]string{"completion"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported command error")
	}
}

func TestRun_CobraRejectsUnknownRootSubcommand(t *testing.T) {
	err := Run([]string{"frob"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported command error")
	}
}

func TestRun_CobraRejectsUnknownNestedSubcommand(t *testing.T) {
	err := Run([]string{"profile", "frob"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported nested command error")
	}
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
