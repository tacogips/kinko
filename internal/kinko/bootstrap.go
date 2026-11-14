package kinko

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

var allowedBootstrapKeys = map[string]struct{}{
	"kinko_dir": {},
}

var sensitiveKeyFragments = []string{
	"secret",
	"password",
	"passphrase",
	"private",
	"token",
	"api_key",
	"key",
}

func validateBootstrapConfigFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open bootstrap config: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid bootstrap config line %d: expected key=value", lineNo)
		}

		key := strings.TrimSpace(parts[0])
		if key == "" {
			return fmt.Errorf("invalid bootstrap config line %d: empty key", lineNo)
		}
		if looksSensitiveKey(key) {
			return fmt.Errorf("bootstrap config contains sensitive-looking key %q (line %d), which is forbidden", key, lineNo)
		}
		if _, ok := allowedBootstrapKeys[key]; !ok {
			return fmt.Errorf("unsupported bootstrap key %q (line %d); bootstrap config must remain minimal and non-secret", key, lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read bootstrap config: %w", err)
	}
	return nil
}

func looksSensitiveKey(key string) bool {
	l := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(l, fragment) {
			return true
		}
	}
	return false
}
