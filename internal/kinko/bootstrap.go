package kinko

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func loadBootstrapDataDir(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open bootstrap config: %w", err)
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
			return "", false, fmt.Errorf("invalid bootstrap config line %d: expected key=value", lineNo)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key != "kinko_dir" {
			continue
		}
		dataDir, err := parseBootstrapStringValue(value)
		if err != nil {
			return "", false, fmt.Errorf("invalid bootstrap kinko_dir line %d: %w", lineNo, err)
		}
		if strings.TrimSpace(dataDir) == "" {
			return "", false, fmt.Errorf("invalid bootstrap kinko_dir line %d: empty value", lineNo)
		}
		return filepath.Clean(dataDir), true, nil
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read bootstrap config: %w", err)
	}
	return "", false, nil
}

func parseBootstrapStringValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted := strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
		unquoted = strings.ReplaceAll(unquoted, `\"`, `"`)
		unquoted = strings.ReplaceAll(unquoted, `\\`, `\`)
		return unquoted, nil
	}
	if strings.ContainsAny(value, `"'`) {
		return "", errors.New("quoted values must use a complete double-quoted string")
	}
	return value, nil
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
