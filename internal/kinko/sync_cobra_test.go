package kinko

import (
	"bytes"
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
