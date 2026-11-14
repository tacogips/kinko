package kinko

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestExitCode_WrappedCLIError(t *testing.T) {
	base := newCLIError(exitCodeMetadataInvalid, "metadata invalid", errors.New("cause"))
	wrapped := fmt.Errorf("outer: %w", base)
	if got := ExitCode(wrapped); got != exitCodeMetadataInvalid {
		t.Fatalf("ExitCode(wrapped)=%d want=%d", got, exitCodeMetadataInvalid)
	}
}

func TestExitCode_PropagatesChildExitStatus(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 7").Run()
	if err == nil {
		t.Fatal("expected child exit error")
	}
	if got := ExitCode(err); got != 7 {
		t.Fatalf("ExitCode(child)=%d want=7", got)
	}
}
