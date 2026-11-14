package kinko

import (
	"errors"
	"os/exec"
)

const (
	exitCodeOK              = 0
	exitCodeAuthFailed      = 10
	exitCodePolicyFailed    = 11
	exitCodeLockConflict    = 12
	exitCodeIOFailed        = 13
	exitCodeMetadataInvalid = 14
)

type cliError struct {
	code int
	msg  string
	err  error
}

func (e *cliError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "command failed"
}

func (e *cliError) Unwrap() error { return e.err }

func newCLIError(code int, msg string, err error) error {
	return &cliError{code: code, msg: msg, err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return exitCodeOK
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
