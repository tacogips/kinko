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
