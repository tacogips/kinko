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
	if got := resolveDirenvScope(fallback, ""); got != fallback {
		t.Fatalf("empty direnv fallback=%q want=%q", got, fallback)
	}

	scopeDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveDirenvScope(fallback, "-"+scopeDir); got != filepath.Clean(scopeDir) {
		t.Fatalf("dir scope=%q want=%q", got, filepath.Clean(scopeDir))
	}

	envrcPath := filepath.Join(scopeDir, ".envrc")
	if err := os.WriteFile(envrcPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveDirenvScope(fallback, "-"+envrcPath); got != filepath.Clean(scopeDir) {
		t.Fatalf("file scope=%q want=%q", got, filepath.Clean(scopeDir))
	}

	if got := resolveDirenvScope(fallback, "-"+filepath.Join(t.TempDir(), "missing")); got != fallback {
		t.Fatalf("missing path fallback=%q want=%q", got, fallback)
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
