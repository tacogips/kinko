package kinko

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBWSRuntimeConfigPrecedenceAndParentIsolation(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "bws-config")
	writeTestBWSConfig(t, configPath, "[selected]\nserver_base = \"https://file.example\"\nserver_api = \"https://file.example/api\"\nserver_identity = \"https://file.example/identity\"\norganization_id = \"file-org\"\nproject_id = \"file-project\"\n")
	environment := map[string]string{
		envKinkoBWSConfigFile:     configPath,
		envKinkoBWSProfile:        "selected",
		envKinkoBWSServerURL:      "https://env.example",
		envKinkoBWSOrganizationID: "env-org",
		envKinkoBWSProjectID:      "env-project",
		"BWS_CONFIG_FILE":         filepath.Join(directory, "ignored"), "BWS_PROFILE": "ignored", "BWS_SERVER_URL": "https://ignored.example",
	}
	getenv := func(key string) string { return environment[key] }
	config, err := resolveBWSRuntimeConfig(bwsConfigOptions{ServerURL: "https://flag.example", ProjectID: "flag-project"}, map[string]string{
		configKeyBWSServerURL: "https://encrypted.example", configKeyBWSOrganizationID: "encrypted-org", configKeyBWSProjectID: "encrypted-project",
	}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if config.Profile != "selected" || config.ProjectID != "flag-project" || config.OrganizationID != "env-org" {
		t.Fatalf("resolved config=%+v", config)
	}
	if got := config.Endpoints.BaseURL.String(); got != "https://flag.example" {
		t.Fatalf("base=%q", got)
	}
	if config.Endpoints.APIURL.String() != "https://flag.example/api" || config.Endpoints.IdentityURL.String() != "https://flag.example/identity" {
		t.Fatalf("derived endpoints=%+v", config.Endpoints)
	}
	if config.ProviderIdentity == "" {
		t.Fatal("provider identity was not derived")
	}
}

func TestResolveBWSRuntimeConfigUsesDefaultFileWithoutBWSParentVariables(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".bws", "config")
	if err := os.Mkdir(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestBWSConfig(t, configPath, "[default]\nserver_base=https://default.example\nserver_api=https://default.example/api\nserver_identity=https://default.example/identity\n")
	config, err := resolveBWSRuntimeConfig(bwsConfigOptions{}, nil, func(key string) string {
		if key == "HOME" {
			return home
		}
		if strings.HasPrefix(key, "BWS_") {
			return "parent-must-be-ignored"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ConfigFile != configPath || config.Profile != defaultBWSProfile || config.Endpoints.BaseURL.Host != "default.example" {
		t.Fatalf("default config=%+v", config)
	}
}

func TestCanonicalizeBWSEndpointsSecurity(t *testing.T) {
	parse := func(raw string) *url.URL {
		value, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid, err := canonicalizeBWSEndpoints(bwsEndpointSet{
		BaseURL: parse("HTTPS://EXAMPLE.COM:443/"), APIURL: parse("https://EXAMPLE.com:8443/api/"), IdentityURL: parse("https://example.com/identity/"),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if valid.BaseURL.String() != "https://example.com" || valid.APIURL.String() != "https://example.com:8443/api" {
		t.Fatalf("canonical=%+v", valid)
	}
	for _, raw := range []string{"http://example.com", "https://user@example.com", "https://example.com?query=x", "https://example.com/#fragment", "/relative"} {
		_, err := canonicalizeBWSEndpoints(bwsEndpointSet{BaseURL: parse(raw), APIURL: parse("https://example.com/api"), IdentityURL: parse("https://example.com/identity")}, false)
		if err == nil {
			t.Fatalf("unsafe endpoint %q accepted", raw)
		}
	}
	loopback := bwsEndpointSet{BaseURL: parse("http://127.0.0.1:8080"), APIURL: parse("http://127.0.0.1:8080/api"), IdentityURL: parse("http://127.0.0.1:8080/identity")}
	if _, err := canonicalizeBWSEndpoints(loopback, false); err == nil {
		t.Fatal("loopback accepted without test opt-in")
	}
	if _, err := canonicalizeBWSEndpoints(loopback, true); err != nil {
		t.Fatalf("test loopback rejected: %v", err)
	}
}

func TestReadSecureBWSConfigRejectsUnsafeFilesAndDuplicates(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid")
	writeTestBWSConfig(t, valid, "[default]\nserver_base=https://one.example\nserver_base=https://two.example\n")
	if _, err := readSecureBWSConfig(valid, "default"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error=%v", err)
	}
	unsafeMode := filepath.Join(directory, "unsafe-mode")
	writeTestBWSConfig(t, unsafeMode, "[default]\nserver_base=https://one.example\n")
	if err := os.Chmod(unsafeMode, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecureBWSConfig(unsafeMode, "default"); err == nil {
		t.Fatal("unsafe mode accepted")
	}
	symlink := filepath.Join(directory, "link")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecureBWSConfig(symlink, "default"); err == nil {
		t.Fatal("final symlink accepted")
	}
}

func TestDeriveBWSProviderIdentityIsCanonicalAndScoped(t *testing.T) {
	parse := func(raw string) *url.URL { value, _ := url.Parse(raw); return value }
	first, err := canonicalizeBWSEndpoints(bwsEndpointSet{BaseURL: parse("https://EXAMPLE.com:443/"), APIURL: parse("https://example.com/api/"), IdentityURL: parse("https://example.com/identity/")}, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalizeBWSEndpoints(bwsEndpointSet{BaseURL: parse("https://example.com"), APIURL: parse("https://example.com/api"), IdentityURL: parse("https://example.com/identity")}, false)
	if err != nil {
		t.Fatal(err)
	}
	one := deriveBWSProviderIdentity(first, "org", "project")
	if one != deriveBWSProviderIdentity(second, "org", "project") {
		t.Fatal("canonical equivalents changed identity")
	}
	if one == deriveBWSProviderIdentity(second, "org", "other") {
		t.Fatal("project did not scope identity")
	}
}

func writeTestBWSConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
