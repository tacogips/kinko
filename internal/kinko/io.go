package kinko

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

func readPasswordWithConfirm(stdin io.Reader, stderr io.Writer, prompt1, prompt2 string) (string, error) {
	var first, second string
	var err error
	if isTerminalReader(stdin) {
		first, err = readSecret(stdin, stderr, prompt1)
		if err != nil {
			return "", err
		}
		second, err = readSecret(stdin, stderr, prompt2)
		if err != nil {
			return "", err
		}
	} else {
		reader := bufio.NewReader(stdin)
		first, err = readSecretWithPromptBuffered(reader, stderr, prompt1)
		if err != nil {
			return "", err
		}
		second, err = readSecretWithPromptBuffered(reader, stderr, prompt2)
		if err != nil {
			return "", err
		}
	}
	if first != second {
		return "", errors.New("password mismatch")
	}
	if strings.TrimSpace(first) == "" {
		return "", errors.New("empty secret not allowed")
	}
	return first, nil
}

func readSecret(stdin io.Reader, stderr io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(stderr, prompt); err != nil {
		return "", err
	}
	if f, ok := stdin.(interface{ Fd() uintptr }); ok {
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(stderr)
		if err == nil {
			s := strings.TrimSpace(string(b))
			if s == "" {
				return "", errors.New("empty secret not allowed")
			}
			return s, nil
		}
	}
	return readSecretFromBuffered(bufio.NewReader(stdin))
}

func confirmPrompt(stdin io.Reader, stderr io.Writer, prompt string) (bool, error) {
	if _, err := fmt.Fprint(stderr, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	v := strings.TrimSpace(strings.ToLower(line))
	return v == "y" || v == "yes", nil
}

func confirmPromptTTYAware(stdin io.Reader, stderr io.Writer, prompt string) (bool, error) {
	if isTerminalReader(stdin) {
		return confirmPrompt(stdin, stderr, prompt)
	}
	tty, err := openConfirmationInput()
	if err != nil {
		return false, errors.New("interactive confirmation requires tty input (use --yes or --confirm=false)")
	}
	defer tty.Close()
	return confirmPrompt(tty, stderr, prompt)
}

func openConfirmationInput() (*os.File, error) {
	if runtime.GOOS == "windows" {
		return os.Open("CONIN$")
	}
	return os.Open("/dev/tty")
}

func readSecretFromBuffered(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	s := strings.TrimSpace(line)
	if s == "" {
		return "", errors.New("empty secret not allowed")
	}
	return s, nil
}

func readSecretWithPromptBuffered(reader *bufio.Reader, stderr io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(stderr, prompt); err != nil {
		return "", err
	}
	return readSecretFromBuffered(reader)
}

func isTerminalReader(r io.Reader) bool {
	f, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
