package kinko

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode_WrappedCLIError(t *testing.T) {
	base := newCLIError(exitCodeMetadataInvalid, "metadata invalid", errors.New("cause"))
	wrapped := fmt.Errorf("outer: %w", base)
	if got := ExitCode(wrapped); got != exitCodeMetadataInvalid {
		t.Fatalf("ExitCode(wrapped)=%d want=%d", got, exitCodeMetadataInvalid)
	}
}
