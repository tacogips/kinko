//go:build unix

package kinko

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func readPasswordBytesFromFD(fd int, timeout time.Duration, maxBytes int) ([]byte, error) {
	dupFD, err := unix.Dup(fd)
	if err != nil {
		return nil, fmt.Errorf("dup file descriptor: %w", err)
	}
	file := os.NewFile(uintptr(dupFD), fmt.Sprintf("fd:%d", fd))
	if file == nil {
		return nil, errors.New("failed to open file descriptor")
	}
	defer file.Close()

	// unix.SetNonblock operates via fcntl(F_SETFL), which changes flags on
	// the underlying shared open file description, not just this fd number.
	// That means it also flips O_NONBLOCK on the caller's original fd (e.g.
	// their stdin), which would otherwise remain non-blocking after this
	// process exits and break the calling shell/terminal. Capture the
	// original flags before changing them and restore them via F_SETFL
	// once we're done, on every exit path.
	origFlags, err := unix.FcntlInt(uintptr(dupFD), unix.F_GETFL, 0)
	if err != nil {
		return nil, fmt.Errorf("get fd flags: %w", err)
	}
	if err := unix.SetNonblock(dupFD, true); err != nil {
		return nil, fmt.Errorf("set nonblock on fd: %w", err)
	}
	defer func() {
		_, _ = unix.FcntlInt(uintptr(dupFD), unix.F_SETFL, origFlags)
	}()

	deadline := time.Now().Add(timeout)
	return readFDWithTimeout(dupFD, deadline, timeout, maxBytes)
}

func readFDWithTimeout(fd int, deadline time.Time, timeout time.Duration, maxBytes int) ([]byte, error) {
	buf := make([]byte, 0, maxBytes+1)
	tmp := make([]byte, 512)
	pollfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}

	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			return nil, fmt.Errorf("password input read timed out after %s", timeout)
		}
		timeoutMS := int(remain.Milliseconds())
		if timeoutMS < 1 {
			timeoutMS = 1
		}

		n, err := unix.Poll(pollfds, timeoutMS)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("password input read timed out after %s", timeout)
		}

		revents := pollfds[0].Revents
		if revents&unix.POLLERR != 0 {
			return nil, errors.New("password input fd reported poll error")
		}
		if revents&(unix.POLLIN|unix.POLLHUP) == 0 {
			continue
		}

		for {
			readN, readErr := unix.Read(fd, tmp)
			if readN > 0 {
				buf = append(buf, tmp[:readN]...)
				if len(buf) > maxBytes {
					return buf, nil
				}
			}
			if readErr != nil {
				if errors.Is(readErr, unix.EINTR) {
					continue
				}
				if errors.Is(readErr, unix.EAGAIN) || errors.Is(readErr, unix.EWOULDBLOCK) {
					break
				}
				return nil, readErr
			}
			if readN == 0 {
				return buf, nil
			}
			if readN < len(tmp) {
				break
			}
		}
	}
}
