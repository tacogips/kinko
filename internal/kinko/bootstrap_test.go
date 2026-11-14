package kinko

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBootstrapConfigFile_OK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bootstrap.toml")
	content := "# non-secret bootstrap\nkinko_dir=\"/tmp/kinko\"\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBootstrapConfigFile(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateBootstrapConfigFile_RejectSensitiveKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bootstrap.toml")
	content := "api_key=\"abc\"\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBootstrapConfigFile(p); err == nil {
		t.Fatal("expected sensitive key rejection")
	}
}

func TestValidateBootstrapConfigFile_RejectUnsupportedKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bootstrap.toml")
	content := "workspace=\"/tmp\"\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBootstrapConfigFile(p); err == nil {
		t.Fatal("expected unsupported key rejection")
	}
}

func TestLoadBootstrapDataDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bootstrap.toml")
	want := filepath.Join(t.TempDir(), "kinko-data")
	content := "# non-secret bootstrap\nkinko_dir=\"" + want + "\"\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok, err := loadBootstrapDataDir(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected bootstrap data dir")
	}
	if got != filepath.Clean(want) {
		t.Fatalf("dataDir=%q want %q", got, filepath.Clean(want))
	}
}
