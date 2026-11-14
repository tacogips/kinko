package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDirenvScope(t *testing.T) {
	fallback := filepath.Clean(filepath.Join(t.TempDir(), "fallback"))
	if got := resolveDirenvScope(fallback, "", false); got != fallback {
		t.Fatalf("empty direnv fallback=%q want=%q", got, fallback)
	}

	scopeDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveDirenvScope(fallback, "-"+scopeDir, false); got != filepath.Clean(scopeDir) {
		t.Fatalf("dir scope=%q want=%q", got, filepath.Clean(scopeDir))
	}

	envrcPath := filepath.Join(scopeDir, ".envrc")
	if err := os.WriteFile(envrcPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDirenvScope(fallback, "-"+envrcPath, false); got != filepath.Clean(scopeDir) {
		t.Fatalf("file scope=%q want=%q", got, filepath.Clean(scopeDir))
	}

	if got := resolveDirenvScope(fallback, "-"+filepath.Join(t.TempDir(), "missing"), false); got != fallback {
		t.Fatalf("missing path fallback=%q want=%q", got, fallback)
	}
}

// TestResolveDirenvScope_ExplicitPathWins covers Finding 4: when the caller
// indicates that --path was explicitly set by the user (pathExplicit=true),
// resolveDirenvScope must return fallbackPath unconditionally, even when
// DIRENV_DIR points at a completely valid, different directory. DIRENV_DIR
// must not be consulted at all in that case.
func TestResolveDirenvScope_ExplicitPathWins(t *testing.T) {
	fallback := filepath.Clean(filepath.Join(t.TempDir(), "explicit-path"))

	scopeDir := filepath.Join(t.TempDir(), "direnv-repo")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := resolveDirenvScope(fallback, "-"+scopeDir, true); got != fallback {
		t.Fatalf("pathExplicit=true scope=%q want fallback=%q (DIRENV_DIR must be ignored)", got, fallback)
	}

	// Also confirm this holds when DIRENV_DIR is unset/empty (trivially true, but
	// keeps the explicit-path contract obvious regardless of DIRENV_DIR state).
	if got := resolveDirenvScope(fallback, "", true); got != fallback {
		t.Fatalf("pathExplicit=true with empty DIRENV_DIR scope=%q want fallback=%q", got, fallback)
	}
}

// TestResolveDirenvScope_ImplicitPathPreservesDirenvDirPrecedence covers
// Finding 4's regression guard: when pathExplicit=false (no --path flag
// was passed by the user), the pre-existing DIRENV_DIR-wins behavior must
// be preserved unchanged.
func TestResolveDirenvScope_ImplicitPathPreservesDirenvDirPrecedence(t *testing.T) {
	fallback := filepath.Clean(filepath.Join(t.TempDir(), "fallback"))
	scopeDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveDirenvScope(fallback, "-"+scopeDir, false); got != filepath.Clean(scopeDir) {
		t.Fatalf("pathExplicit=false scope=%q want DIRENV_DIR-derived=%q", got, filepath.Clean(scopeDir))
	}
}

func TestRunDirenvExport_UsesDirenvScopeAndBypassesRedirectGuard(t *testing.T) {
	opts := setupUnlockedForSet(t)
	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	envrcPath := filepath.Join(repoRoot, ".envrc")
	if err := os.WriteFile(envrcPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	scopeOpts := opts
	scopeOpts.path = repoRoot
	var setOut bytes.Buffer
	if err := runSet(scopeOpts, []string{"DIRENV_KEY=loaded"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DIRENV_DIR", "-"+envrcPath)
	opts.path = filepath.Join(t.TempDir(), "other")
	opts.force = false
	opts.confirm = true

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runDirenvExport(opts, []string{"bash"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("runDirenvExport failed: %v", err)
	}
	if !strings.Contains(out.String(), "export DIRENV_KEY='loaded'") {
		t.Fatalf("unexpected export output: %q", out.String())
	}
}

// TestRun_DirenvExportCobra_ExplicitPathFlagWinsOverDirenvDir covers Finding 4
// at the cobra wiring layer: an explicit --path flag must take precedence
// over DIRENV_DIR even though DIRENV_DIR resolves to a different, valid
// directory that also has a secret set.
func TestRun_DirenvExportCobra_ExplicitPathFlagWinsOverDirenvDir(t *testing.T) {
	opts := setupUnlockedForSet(t)

	explicitPath := filepath.Join(t.TempDir(), "explicit")
	if err := os.MkdirAll(explicitPath, 0o755); err != nil {
		t.Fatal(err)
	}
	explicitOpts := opts
	explicitOpts.path = explicitPath
	var setOut bytes.Buffer
	if err := runSet(explicitOpts, []string{"WHICH=explicit-path"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	direnvRepo := filepath.Join(t.TempDir(), "direnv-repo")
	if err := os.MkdirAll(direnvRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	envrcPath := filepath.Join(direnvRepo, ".envrc")
	if err := os.WriteFile(envrcPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	direnvOpts := opts
	direnvOpts.path = direnvRepo
	if err := runSet(direnvOpts, []string{"WHICH=direnv-dir"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DIRENV_DIR", "-"+envrcPath)

	base := []string{"--kinko-dir", opts.dataDir, "--profile", opts.profile}

	// With --path explicitly passed, the explicit path's secret must win.
	var out bytes.Buffer
	var errBuf bytes.Buffer
	explicitArgs := append(append([]string{}, base...), "--path", explicitPath, "direnv", "export", "bash")
	if err := Run(explicitArgs, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("direnv export with explicit --path failed: %v", err)
	}
	if !strings.Contains(out.String(), "export WHICH='explicit-path'") {
		t.Fatalf("expected explicit --path secret in output, got %q", out.String())
	}
	if strings.Contains(out.String(), "direnv-dir") {
		t.Fatalf("explicit --path must not fall back to DIRENV_DIR, got %q", out.String())
	}

	// Without --path passed at all, DIRENV_DIR must still win (existing default).
	out.Reset()
	errBuf.Reset()
	noPathArgs := append(append([]string{}, base...), "direnv", "export", "bash")
	if err := Run(noPathArgs, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("direnv export without --path failed: %v", err)
	}
	if !strings.Contains(out.String(), "export WHICH='direnv-dir'") {
		t.Fatalf("expected DIRENV_DIR secret in output when --path was not passed, got %q", out.String())
	}
}
