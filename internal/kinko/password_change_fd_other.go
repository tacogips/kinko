//go:build !unix

package kinko

import (
	"fmt"
	"time"
)

func readPasswordBytesFromFD(_ int, _ time.Duration, _ int) ([]byte, error) {
	return nil, fmt.Errorf("fd password input is not supported on this platform; use --current-stdin/--new-stdin")
}
