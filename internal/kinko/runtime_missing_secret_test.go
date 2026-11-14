package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGetMissingSecretExitStatus(t *testing.T) {
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--config", filepath.Join(t.TempDir(), "bootstrap.toml"), "--path", opts.path, "--profile", opts.profile, "--force", "--confirm=false"}
	for _, reveal := range []bool{false, true} {
		name := "masked"
		if reveal {
			name = "revealed"
		}
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{}, base...), "get", "KINKO_TEST_ABSENT")
			if reveal {
				args = append(args, "--reveal")
			}
			var out, stderr bytes.Buffer
			err := Run(args, strings.NewReader(""), &out, &stderr)
			if err == nil || !strings.Contains(err.Error(), "secret not found") {
				t.Fatalf("expected missing-secret error, got %v", err)
			}
			if got := ExitCode(err); got != 1 {
				t.Fatalf("exit code=%d want 1", got)
			}
			if out.Len() != 0 {
				t.Fatalf("missing get wrote stdout: %q", out.String())
			}
		})
	}
}

func TestRunExecMissingSecretDoesNotStartChild(t *testing.T) {
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--config", filepath.Join(t.TempDir(), "bootstrap.toml"), "--path", opts.path, "--profile", opts.profile}
	if err := runSet(opts, []string{"KINKO_TEST_PRESENT=synthetic"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KINKO_TEST_INHERITED", "synthetic-parent")
	for _, selection := range []string{
		"KINKO_TEST_ABSENT",
		"KINKO_TEST_PRESENT,KINKO_TEST_ABSENT",
		"KINKO_TEST_ABSENT,KINKO_TEST_PRESENT",
		"KINKO_TEST_INHERITED",
	} {
		t.Run(selection, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "child-started")
			args := append(append([]string{}, base...), "exec", "--env", selection, "--", executable, "-test.run=^TestMissingSecretChild$", "--", marker, "KINKO_TEST_PRESENT")
			var out, stderr bytes.Buffer
			err := Run(args, strings.NewReader(""), &out, &stderr)
			if err == nil || !strings.Contains(err.Error(), "secret not found for --env key") {
				t.Fatalf("expected missing-secret error, got %v", err)
			}
			if got := ExitCode(err); got != 1 {
				t.Fatalf("exit code=%d want 1", got)
			}
			if out.Len() != 0 {
				t.Fatalf("missing exec wrote stdout: %q", out.String())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("child must not start; marker stat error=%v", err)
			}
		})
	}
}

func TestRunRegisteredEmptyAndSharedSecretsSucceed(t *testing.T) {
	withFakeSessionStore(t)
	opts := setupUnlockedForSet(t)
	base := []string{"--kinko-dir", opts.dataDir, "--config", filepath.Join(t.TempDir(), "bootstrap.toml"), "--path", opts.path, "--profile", opts.profile, "--force", "--confirm=false"}
	if err := runSet(opts, []string{"KINKO_TEST_EMPTY="}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "KINKO_TEST_SHARED=synthetic-shared"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The registered empty value must override an inherited nonempty value.
	t.Setenv("KINKO_TEST_EMPTY", "synthetic-parent")
	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "KINKO_TEST_EMPTY", value: ""},
		{key: "KINKO_TEST_SHARED", value: "synthetic-shared"},
	} {
		t.Run(test.key, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := append(append([]string{}, base...), "get", test.key, "--reveal")
			if err := Run(args, strings.NewReader(""), &out, &stderr); err != nil {
				t.Fatalf("registered get failed: %v", err)
			}
			if got := out.String(); got != test.value+"\n" {
				t.Fatalf("get output=%q want %q", got, test.value+"\n")
			}
			marker := filepath.Join(t.TempDir(), "child-started")
			args = append(append([]string{}, base...), "exec", "--env", test.key, "--", executable, "-test.run=^TestMissingSecretChild$", "--", marker, test.key)
			if err := Run(args, strings.NewReader(""), &out, &stderr); err != nil {
				t.Fatalf("registered exec failed: %v; stderr=%s", err, stderr.String())
			}
			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("child did not write marker: %v", err)
			}
			if string(got) != "set:"+test.value {
				t.Fatalf("child environment=%q want %q", got, "set:"+test.value)
			}
		})
	}
}

// TestMissingSecretChild runs only when explicitly selected as an exec child.
// It records presence separately from the value so empty and absent differ.
func TestMissingSecretChild(t *testing.T) {
	if len(os.Args) != 5 || os.Args[2] != "--" {
		return
	}
	value, ok := os.LookupEnv(os.Args[4])
	result := "unset"
	if ok {
		result = "set:" + value
	}
	if err := os.WriteFile(os.Args[3], []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
}
