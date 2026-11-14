package kinko

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHookEnterWithOptions_RendersTrackingBeforeEscapedExports(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var setOut bytes.Buffer
	if err := runSet(opts, []string{
		"--shared",
		"SHARED_KEY=shared",
		envKinkoHookKeys + "=must-not-export",
	}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"LOCAL_KEY=can't stop"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runHookEnterWithOptions(
		opts,
		hookOptions{shell: shellBash, enter: true},
		strings.NewReader(""),
		&out,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatal(err)
	}

	got := out.String()
	wantTracking := "export KINKO_HOOK_KEYS='LOCAL_KEY SHARED_KEY'\n"
	if !strings.HasPrefix(got, wantTracking) {
		t.Fatalf("tracking assignment must be first: %q", got)
	}
	if !strings.Contains(got, "export SHARED_KEY='shared'\n") {
		t.Fatalf("missing shared export: %q", got)
	}
	if !strings.Contains(got, "export LOCAL_KEY='can'\"'\"'t stop'\n") {
		t.Fatalf("missing escaped local export: %q", got)
	}
	if strings.Contains(got, "must-not-export") {
		t.Fatalf("reserved tracking value leaked into output: %q", got)
	}
	if strings.Count(got, "export KINKO_HOOK_KEYS=") != 1 {
		t.Fatalf("reserved key must only be the generated tracking assignment: %q", got)
	}
	if strings.Contains(got, "# kinko:scope=") {
		t.Fatalf("hook output should remain concise: %q", got)
	}
}

func TestRunHookEnterWithOptions_TracksOverlappingScopeKeyOnce(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var setOut bytes.Buffer
	if err := runSet(opts, []string{"--shared", "SAME=shared"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"SAME=local"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runHookEnterWithOptions(opts, hookOptions{shell: shellBash}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "export KINKO_HOOK_KEYS='SAME'\n") {
		t.Fatalf("overlapping key should be tracked once: %q", out.String())
	}
}

func TestRunHookLeaveWithOptions_RendersValidatedCleanup(t *testing.T) {
	t.Setenv(envKinkoHookKeys, "SECOND FIRST SECOND")

	var out bytes.Buffer
	if err := runHookLeaveWithOptions(hookOptions{shell: shellBash}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "unset FIRST\nunset SECOND\nunset KINKO_HOOK_KEYS\n"; got != want {
		t.Fatalf("cleanup=%q want=%q", got, want)
	}
}

func TestRunHookLeaveWithOptions_MissingStateOnlyUnsetsTracker(t *testing.T) {
	t.Setenv(envKinkoHookKeys, "")

	var out bytes.Buffer
	if err := runHookLeaveWithOptions(hookOptions{shell: shellBash}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "unset KINKO_HOOK_KEYS\n"; got != want {
		t.Fatalf("cleanup=%q want=%q", got, want)
	}
}

func TestRunHookLeaveWithOptions_RejectsManipulatedTrackingBeforeOutput(t *testing.T) {
	tests := []string{
		"SAFE BAD;COMMAND",
		"SAFE " + envKinkoHookKeys,
	}
	for _, tracking := range tests {
		t.Run(tracking, func(t *testing.T) {
			t.Setenv(envKinkoHookKeys, tracking)
			var out bytes.Buffer
			err := runHookLeaveWithOptions(hookOptions{shell: shellBash}, &out)
			if err == nil {
				t.Fatal("expected invalid tracking error")
			}
			if ExitCode(err) != exitCodePolicyFailed {
				t.Fatalf("exit=%d want=%d: %v", ExitCode(err), exitCodePolicyFailed, err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid tracking produced partial output: %q", out.String())
			}
		})
	}
}

func TestHookCommands_RejectUnsupportedShells(t *testing.T) {
	for _, run := range []func() error{
		func() error {
			return runHookEnterWithOptions(globalOptions{}, hookOptions{shell: shellFish}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		},
		func() error {
			return runHookLeaveWithOptions(hookOptions{shell: shellZsh}, &bytes.Buffer{})
		},
	} {
		err := run()
		if err == nil {
			t.Fatal("expected unsupported shell error")
		}
		if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), "supported shell: bash") {
			t.Fatalf("unexpected unsupported-shell error: exit=%d err=%v", ExitCode(err), err)
		}
	}
}

func TestRunHookEnterWithOptions_LockedSessionFailsButLeaveNeedsNoVault(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	opts := globalOptions{
		dataDir: dataDir,
		profile: defaultProfile,
		path:    t.TempDir(),
	}

	err := runHookEnterWithOptions(opts, hookOptions{shell: shellBash}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected locked-session error")
	}
	if ExitCode(err) != exitCodeIOFailed {
		t.Fatalf("enter exit=%d want=%d: %v", ExitCode(err), exitCodeIOFailed, err)
	}

	t.Setenv(envKinkoHookKeys, "LOCKED_KEY")
	var out bytes.Buffer
	if err := runHookLeaveWithOptions(hookOptions{shell: shellBash}, &out); err != nil {
		t.Fatalf("leave should not inspect vault: %v", err)
	}
	if got := out.String(); got != "unset LOCKED_KEY\nunset KINKO_HOOK_KEYS\n" {
		t.Fatalf("unexpected leave output: %q", got)
	}
}

func TestNewHookCommand_PreflightBoundary(t *testing.T) {
	preflightErr := errors.New("preflight marker")
	preflightCalls := 0
	newCommand := func(stdout *bytes.Buffer) *cobraCommandHarness {
		ctx := &runtimeContext{
			stdin:  strings.NewReader(""),
			stdout: stdout,
			stderr: &bytes.Buffer{},
		}
		return &cobraCommandHarness{command: newHookCommand(ctx, func() error {
			preflightCalls++
			return preflightErr
		})}
	}

	t.Setenv(envKinkoHookKeys, "NO_VAULT")
	var leaveOut bytes.Buffer
	leave := newCommand(&leaveOut)
	leave.command.SetArgs([]string{hookLeave, shellBash})
	if err := leave.command.Execute(); err != nil {
		t.Fatalf("leave invoked preflight: %v", err)
	}
	if preflightCalls != 0 {
		t.Fatalf("leave preflight calls=%d want=0", preflightCalls)
	}

	enter := newCommand(&bytes.Buffer{})
	enter.command.SetArgs([]string{hookEnter, shellBash})
	err := enter.command.Execute()
	if !errors.Is(err, preflightErr) {
		t.Fatalf("enter error=%v want preflight marker", err)
	}
	if preflightCalls != 1 {
		t.Fatalf("enter preflight calls=%d want=1", preflightCalls)
	}
}

// cobraCommandHarness keeps the preflight-boundary fixture focused without
// broadening production interfaces solely for tests.
type cobraCommandHarness struct {
	command interface {
		SetArgs([]string)
		Execute() error
	}
}

func TestRun_HookCobraWiring(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var setOut bytes.Buffer
	if err := runSet(opts, []string{"WIRED=through-cobra"}, strings.NewReader(""), &setOut); err != nil {
		t.Fatal(err)
	}

	base := []string{
		"--kinko-dir", opts.dataDir,
		"--config", filepath.Join(t.TempDir(), "missing-bootstrap.toml"),
		"--profile", opts.profile,
		"--path", opts.path,
	}
	var enterOut bytes.Buffer
	if err := Run(append(base, cmdHook, hookEnter, shellBash), strings.NewReader(""), &enterOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("cobra enter failed: %v", err)
	}
	if !strings.Contains(enterOut.String(), "export WIRED='through-cobra'") {
		t.Fatalf("unexpected cobra enter output: %q", enterOut.String())
	}

	t.Setenv(envKinkoHookKeys, "WIRED")
	var leaveOut bytes.Buffer
	missingDataDir := filepath.Join(t.TempDir(), "missing-vault")
	if err := Run([]string{"--kinko-dir", missingDataDir, cmdHook, hookLeave}, strings.NewReader(""), &leaveOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("cobra leave should not require vault preflight: %v", err)
	}
	if got := leaveOut.String(); got != "unset WIRED\nunset KINKO_HOOK_KEYS\n" {
		t.Fatalf("unexpected cobra leave output: %q", got)
	}
}

func TestRun_HookLeaveIgnoresInvalidBootstrapFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
	if err := os.WriteFile(configPath, []byte("not valid bootstrap syntax"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKinkoHookKeys, "SAFE")

	var out bytes.Buffer
	if err := Run([]string{"--config", configPath, cmdHook, hookLeave, shellBash}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("leave unexpectedly read bootstrap config: %v", err)
	}
	if got := out.String(); got != "unset SAFE\nunset KINKO_HOOK_KEYS\n" {
		t.Fatalf("unexpected leave output: %q", got)
	}
}
