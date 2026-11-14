package kinko

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCobraSyncHelpAndFlagSurface(t *testing.T) {
	var rootHelp bytes.Buffer
	if err := Run([]string{cmdSync, "--help"}, strings.NewReader(""), &rootHelp, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{cmdSyncPush, cmdSyncPull} {
		if !strings.Contains(rootHelp.String(), direction) {
			t.Fatalf("sync help missing %s: %q", direction, rootHelp.String())
		}
		var help bytes.Buffer
		if err := Run([]string{cmdSync, direction, "--help"}, strings.NewReader(""), &help, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"--provider", "--force", "--dry-run", "--project-id", "--json"} {
			if !strings.Contains(help.String(), flag) {
				t.Fatalf("sync %s help missing %s: %q", direction, flag, help.String())
			}
		}
		if !strings.Contains(help.String(), "make the command direction authoritative on conflicts") {
			t.Fatalf("sync %s help does not explain --force authority: %q", direction, help.String())
		}
	}
	for _, subcommand := range []string{"bootstrap", "status", "reset", "reconcile", "prune"} {
		if !strings.Contains(rootHelp.String(), subcommand) {
			t.Fatalf("sync help missing %s: %q", subcommand, rootHelp.String())
		}
		var help bytes.Buffer
		if err := Run([]string{cmdSync, subcommand, "--help"}, strings.NewReader(""), &help, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"--provider", "--select-profile", "--select-path", "--select-key", "--exclude-profile", "--exclude-path", "--exclude-key", "--shared", "--map-path", "--bws-config-file", "--bws-profile", "--bws-server-url", "--bws-transport", "--max-retries", "--resume", "--progress"} {
			if !strings.Contains(help.String(), flag) {
				t.Fatalf("sync %s help missing %s", subcommand, flag)
			}
		}
	}
}

func TestCobraSyncCompletionValidationPrecedesVaultAccess(t *testing.T) {
	tests := [][]string{
		{cmdSync, "status", "--online"},
		{cmdSync, "bootstrap", "--provider=bws", "--from-machine-id=bad"},
		{cmdSync, "prune", "--provider=other"},
		{cmdDoctor, "--online"},
	}
	for _, args := range tests {
		err := Run(args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if ExitCode(err) != exitCodePolicyFailed {
			t.Fatalf("args=%v exit=%d err=%v", args, ExitCode(err), err)
		}
		if strings.Contains(err.Error(), "metadata") || strings.Contains(err.Error(), "password") {
			t.Fatalf("args=%v reached vault access before option validation: %v", args, err)
		}
	}
}

func TestCobraSyncPushAndPullRejectPrune(t *testing.T) {
	for _, direction := range []string{cmdSyncPush, cmdSyncPull} {
		t.Run(direction, func(t *testing.T) {
			err := Run([]string{cmdSync, direction, "--prune"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			if ExitCode(err) != exitCodePolicyFailed || !strings.Contains(err.Error(), "unknown flag: --prune") {
				t.Fatalf("sync %s --prune exit=%d err=%v", direction, ExitCode(err), err)
			}
		})
	}
}

func TestCobraSyncPushAcceptsCLILegacyWithArgvAcknowledgement(t *testing.T) {
	originalCompletion := runCompletionSyncCommand
	t.Cleanup(func() { runCompletionSyncCommand = originalCompletion })
	runCompletionSyncCommand = func(globalOptions, syncDirectionV2Options, io.Reader, io.Writer, io.Writer) error { return nil }
	newContext := func() *runtimeContext {
		return &runtimeContext{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	}
	acknowledged := newSyncDirectionCommand(newContext(), func() error { return nil }, syncDirectionPush)
	for name, value := range map[string]string{"provider": "bws", "bws-transport": "cli-legacy", "allow-secret-argv": "true", "progress": "plain"} {
		if err := acknowledged.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := acknowledged.RunE(acknowledged, nil); err != nil {
		t.Fatalf("cli-legacy with --allow-secret-argv rejected at preflight: %v", err)
	}

	unacknowledged := newSyncDirectionCommand(newContext(), func() error { return nil }, syncDirectionPush)
	for name, value := range map[string]string{"provider": "bws", "bws-transport": "cli-legacy", "progress": "plain"} {
		if err := unacknowledged.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	err := unacknowledged.RunE(unacknowledged, nil)
	if ExitCode(err) != exitCodePolicyFailed || !errors.Is(err, errBWSSecureMutationUnavailable) {
		t.Fatalf("cli-legacy without --allow-secret-argv exit=%d err=%v", ExitCode(err), err)
	}
}

func TestCobraSyncDirectionDispatchesLegacyAndCompletionExactly(t *testing.T) {
	originalLegacy, originalCompletion := runLegacySyncCommand, runCompletionSyncCommand
	t.Cleanup(func() { runLegacySyncCommand, runCompletionSyncCommand = originalLegacy, originalCompletion })
	legacyCalls, completionCalls := 0, 0
	runLegacySyncCommand = func(globalOptions, syncOptions, io.Reader, io.Writer, io.Writer) error {
		legacyCalls++
		return nil
	}
	var captured syncDirectionV2Options
	runCompletionSyncCommand = func(_ globalOptions, options syncDirectionV2Options, _ io.Reader, _, _ io.Writer) error {
		completionCalls++
		captured = options
		return nil
	}
	newContext := func() *runtimeContext {
		return &runtimeContext{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	}

	legacy := newSyncDirectionCommand(newContext(), func() error { return nil }, syncDirectionPush)
	if err := legacy.Flags().Set("provider", "bws"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.RunE(legacy, nil); err != nil {
		t.Fatal(err)
	}
	if legacyCalls != 1 || completionCalls != 0 {
		t.Fatalf("legacy=%d completion=%d", legacyCalls, completionCalls)
	}

	completion := newSyncDirectionCommand(newContext(), func() error { return nil }, syncDirectionPull)
	for name, value := range map[string]string{"provider": "bws", "select-key": "ONLY", "progress": "none", "bws-transport": "auto"} {
		if err := completion.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := completion.RunE(completion, nil); err != nil {
		t.Fatal(err)
	}
	if legacyCalls != 1 || completionCalls != 1 || captured.Direction != syncDirectionPull || len(captured.Selector.IncludeKeys) != 1 || captured.Selector.IncludeKeys[0] != "ONLY" {
		t.Fatalf("legacy=%d completion=%d captured=%+v", legacyCalls, completionCalls, captured)
	}
}
