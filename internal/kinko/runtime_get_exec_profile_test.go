package kinko

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeShell(t *testing.T) {
	cases := map[string]string{
		"posix": "posix",
		"bash":  "posix",
		"zsh":   "posix",
		"sh":    "posix",
		"fish":  "fish",
		"nu":    "nu",
	}
	for in, want := range cases {
		got, err := normalizeShell(in)
		if err != nil {
			t.Fatalf("normalizeShell(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeShell(%q)=%q want=%q", in, got, want)
		}
	}
}

func TestMaskValue(t *testing.T) {
	if got := maskValue("abcd"); got != "********" {
		t.Fatalf("mask short=%q", got)
	}
	if got := maskValue("abcdefgh"); got != "********" {
		t.Fatalf("mask long=%q", got)
	}
}

func TestParseGetArgs_AllowsFlagsAfterKey(t *testing.T) {
	key, reveal, force, err := parseGetArgs([]string{"GITHUB_TOKEN", "--reveal", "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "GITHUB_TOKEN" {
		t.Fatalf("key=%q want=%q", key, "GITHUB_TOKEN")
	}
	if !reveal {
		t.Fatal("expected reveal=true")
	}
	if !force {
		t.Fatal("expected force=true")
	}
}

func TestRunGet_RevealAndForceAfterKeyWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)

	var out bytes.Buffer
	if err := runSet(opts, []string{"GITHUB_TOKEN=secret-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(opts, []string{"GITHUB_TOKEN", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); got != "secret-value\n" {
		t.Fatalf("output=%q want=%q", got, "secret-value\n")
	}
}

func TestRunGet_SameKeyAcrossDirectoriesResolvesBySelectedPath(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	pathA := filepath.Join(base, "a")
	pathB := filepath.Join(base, "b")

	optsA := opts
	optsA.path = pathA
	if err := runSet(optsA, []string{"DUP=alpha"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	optsB := opts
	optsB.path = pathB
	if err := runSet(optsB, []string{"DUP=bravo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(optsA, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get(pathA) failed: %v", err)
	}
	if got := out.String(); got != "alpha\n" {
		t.Fatalf("get(pathA)=%q want=%q", got, "alpha\n")
	}

	out.Reset()
	errBuf.Reset()
	if err := runGet(optsB, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get(pathB) failed: %v", err)
	}
	if got := out.String(); got != "bravo\n" {
		t.Fatalf("get(pathB)=%q want=%q", got, "bravo\n")
	}
}

func TestRunGet_PrefersDirectoryLocalOverSharedForSameKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer

	if err := runSet(opts, []string{"--shared", "DUP=shared-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"DUP=local-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(opts, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got := out.String(); got != "local-value\n" {
		t.Fatalf("get=%q want=%q", got, "local-value\n")
	}
}

func TestRenderPosix(t *testing.T) {
	got, err := renderShellAssignment("posix", "API_KEY", "a'b")
	if err != nil {
		t.Fatal(err)
	}
	want := "export API_KEY='a'\"'\"'b'"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestSelectExecSecrets(t *testing.T) {
	secrets := map[string]string{"FOO": "bar", "BAR": "baz"}

	all, err := selectExecSecrets(secrets, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["FOO"] != "bar" || all["BAR"] != "baz" {
		t.Fatalf("unexpected all selection: %#v", all)
	}

	subset, err := selectExecSecrets(secrets, false, "FOO")
	if err != nil {
		t.Fatal(err)
	}
	if len(subset) != 1 || subset["FOO"] != "bar" {
		t.Fatalf("unexpected subset selection: %#v", subset)
	}
}

func TestSelectExecSecrets_RequiresSelection(t *testing.T) {
	_, err := selectExecSecrets(map[string]string{"FOO": "bar"}, false, "")
	if err == nil {
		t.Fatal("expected selection error")
	}
}

func TestRunProfile_ListSortedStoredProfiles(t *testing.T) {
	opts := setupUnlockedForSet(t)

	if err := runSet(opts, []string{"DEFAULT_KEY=default"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	dev := opts
	dev.profile = "dev"
	if err := runSet(dev, []string{"DEV_KEY=dev"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	prod := opts
	prod.profile = "prod"
	if err := runSet(prod, []string{"PROD_KEY=prod"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	if err := runSet(opts, []string{"--shared", "SHARED_ONLY=value"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runProfile(opts, []string{profileList}, &out); err != nil {
		t.Fatal(err)
	}

	got := strings.TrimSpace(out.String())
	if got != "default\ndev\nprod" {
		t.Fatalf("unexpected profile list output: %q", got)
	}
}

func TestRunProfile_ListEmptyWhenOnlySharedKeysExist(t *testing.T) {
	opts := setupUnlockedForSet(t)

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	vd.Profiles = map[string]map[string]map[string]string{}
	vd.Shared["SHARED_ONLY"] = "value"
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runProfile(opts, []string{profileList}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" {
		t.Fatalf("expected no stored profiles, got %q", out.String())
	}
}

func TestRunProfile_RejectsExtraArgs(t *testing.T) {
	opts := setupUnlockedForSet(t)

	err := runProfile(opts, []string{profileList, "extra"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "does not accept positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunProfile_RejectsUnknownSubcommand(t *testing.T) {
	opts := setupUnlockedForSet(t)

	err := runProfile(opts, []string{"rename"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `unknown profile subcommand "rename"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
