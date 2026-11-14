//go:build unix

package kinko

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestReadPasswordBytesFromFD_DoesNotLeaveOriginalFDNonblocking guards
// against a regression where unix.SetNonblock, called on a dup'd fd, also
// flips O_NONBLOCK on the shared underlying open file description -- and
// therefore on the ORIGINAL fd (e.g. the caller's stdin) as well, since
// SetNonblock affects the file description, not just the fd number. If
// that flag is never restored, the caller's fd (like the invoking shell's
// stdin) is left in non-blocking mode after kinko exits.
func TestReadPasswordBytesFromFD_DoesNotLeaveOriginalFDNonblocking(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	origFD := int(r.Fd())
	before, err := unix.FcntlInt(uintptr(origFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("get original flags: %v", err)
	}
	if before&unix.O_NONBLOCK != 0 {
		t.Fatal("precondition: original fd must start in blocking mode")
	}

	if _, err := w.Write([]byte("secret-password\n")); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	if _, err := readPasswordBytesFromFD(origFD, time.Second, maxPasswordInputBytes); err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}

	after, err := unix.FcntlInt(uintptr(origFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("get flags after read: %v", err)
	}
	if after&unix.O_NONBLOCK != 0 {
		t.Fatal("original fd was left in non-blocking mode after readPasswordBytesFromFD returned")
	}
}

// TestReadPasswordBytesFromFD_RestoresBlockingModeOnTimeout verifies the
// flag restoration happens even on the timeout exit path, not only on
// success, since the fix uses a defer that must run regardless of how the
// function returns.
func TestReadPasswordBytesFromFD_RestoresBlockingModeOnTimeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	origFD := int(r.Fd())
	before, err := unix.FcntlInt(uintptr(origFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("get original flags: %v", err)
	}

	// No data is written and the write end stays open, so the read times
	// out rather than hitting EOF.
	if _, err := readPasswordBytesFromFD(origFD, 50*time.Millisecond, maxPasswordInputBytes); err == nil {
		t.Fatal("expected timeout error")
	}

	after, err := unix.FcntlInt(uintptr(origFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("get flags after timeout: %v", err)
	}
	if (before&unix.O_NONBLOCK != 0) != (after&unix.O_NONBLOCK != 0) {
		t.Fatalf("original fd nonblocking state changed after timeout: before=%v after=%v", before&unix.O_NONBLOCK != 0, after&unix.O_NONBLOCK != 0)
	}
}
