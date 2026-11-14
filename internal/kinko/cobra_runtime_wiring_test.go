package kinko

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This file continues cobra_runtime_test.go: it holds move/copy/help/misc
// cobra command wiring tests. Split out to keep individual test files
// under the project's 1000-line source file guideline (see
// .agents/skills/go-coding-standards/SKILL.md).

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

func TestRun_CobraFolderUnlockHelpHidesCompatibilityHoldFlag(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"folder", "unlock", "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "--hold") {
		t.Fatalf("folder unlock help should not expose compatibility --hold flag: %q", got)
	}
	if !strings.Contains(got, "Usage:") || !strings.Contains(got, "unlock NAME") {
		t.Fatalf("unexpected folder unlock help output: %q", got)
	}
}

func TestRun_CobraFolderRemoveWiring(t *testing.T) {
	withFakeSessionStore(t)
	fake := withFakeFolderBackend(t)
	opts := setupUnlockedForSet(t)
	opts.path = t.TempDir()
	configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
	base := []string{"--kinko-dir", opts.dataDir, "--config", configPath, "--path", opts.path, "--profile", opts.profile}
	if err := Run(append(base, "folder", "add", "private"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder add failed: %v", err)
	}

	var out bytes.Buffer
	if err := Run(append(base, "folder", "remove", "private", "--yes"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("folder remove failed: %v", err)
	}
	if got := out.String(); got != "folder removed: private\n" {
		t.Fatalf("unexpected remove output: %q", got)
	}
	if _, mounts, _ := fake.counts(); mounts != 0 {
		t.Fatalf("folder remove must not mount, mounts=%d", mounts)
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

// TestNewExecCommand_SetInterspersedPassesFlagLikeArgsThrough is a Finding
// 5 regression test proving that `kinko exec --env FOO node --env BAR`
// (without a `--` separator) does not have `--env BAR` reparsed as a
// kinko flag. It constructs the exec *cobra.Command directly and calls
// ParseFlags (not Execute/RunE, since RunE would try to actually exec a
// child process) to assert on cobra/pflag's flag-parsing behavior in
// isolation.
func TestNewExecCommand_SetInterspersedPassesFlagLikeArgsThrough(t *testing.T) {
	ctx := &runtimeContext{
		stdin:  strings.NewReader(""),
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	cmd := newExecCommand(ctx, func() error { return nil })

	if err := cmd.ParseFlags([]string{"--env", "FOO", "node", "--env", "BAR"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	gotArgs := cmd.Flags().Args()
	wantArgs := []string{"node", "--env", "BAR"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("Flags().Args()=%v want %v (flag-like tokens after the first positional must pass through untouched)", gotArgs, wantArgs)
	}

	envFlag := cmd.Flags().Lookup("env")
	if envFlag == nil {
		t.Fatal("expected --env flag to be registered")
	}
	if got := envFlag.Value.String(); got != "FOO" {
		t.Fatalf("--env value=%q want %q", got, "FOO")
	}
}
