package kinko

import (
	"fmt"
	"io"
	"os"
	"strings"
)

type hookOptions struct {
	shell      string
	enter      bool
	exportOpts exportOptions
}

func runHookEnterWithOptions(opts globalOptions, hookOpts hookOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if _, err := normalizeHookShell(hookOpts.shell); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}

	nonInteractive := opts
	nonInteractive.force = true
	nonInteractive.confirm = false
	if err := guardSensitiveOutput(nonInteractive, stdin, stdout, stderr, "export hook secrets"); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}

	shared, repoSpecific, err := showSecretScopes(nonInteractive)
	if err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	excluded, err := parseExcludedKeys(append(hookOpts.exportOpts.excludeKeys, envKinkoHookKeys))
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	shared = filterSecretsByExclusion(shared, excluded)
	repoSpecific = filterSecretsByExclusion(repoSpecific, excluded)
	if hookOpts.exportOpts.sharedOnly {
		repoSpecific = nil
	}

	tracked := make(map[string]string, len(shared)+len(repoSpecific))
	for key := range shared {
		tracked[key] = ""
	}
	for key := range repoSpecific {
		tracked[key] = ""
	}
	trackingLine, err := renderShellAssignment(shellPosix, envKinkoHookKeys, strings.Join(sortedKeys(tracked), " "))
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	if _, err := fmt.Fprintln(stdout, trackingLine); err != nil {
		return newCLIError(exitCodeIOFailed, "failed to write hook tracking state", err)
	}
	if err := writeExportBlock(stdout, shellPosix, "shared", "shared keys", shared, false); err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	repoTitle := fmt.Sprintf("repository-specific keys (profile=%s path=%s)", nonInteractive.profile, nonInteractive.path)
	if err := writeExportBlock(stdout, shellPosix, "repo", repoTitle, repoSpecific, false); err != nil {
		return newCLIError(exitCodeIOFailed, err.Error(), err)
	}
	return nil
}

func runHookLeaveWithOptions(hookOpts hookOptions, stdout io.Writer) error {
	if _, err := normalizeHookShell(hookOpts.shell); err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}

	tracked, err := parseHookTrackingKeys(os.Getenv(envKinkoHookKeys))
	if err != nil {
		return newCLIError(exitCodePolicyFailed, err.Error(), err)
	}
	for _, key := range tracked {
		if _, err := fmt.Fprintf(stdout, "unset %s\n", key); err != nil {
			return newCLIError(exitCodeIOFailed, "failed to write hook cleanup", err)
		}
	}
	if _, err := fmt.Fprintf(stdout, "unset %s\n", envKinkoHookKeys); err != nil {
		return newCLIError(exitCodeIOFailed, "failed to write hook cleanup", err)
	}
	return nil
}

func normalizeHookShell(shell string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(shell))
	if normalized == "" {
		normalized = shellBash
	}
	if normalized != shellBash {
		return "", fmt.Errorf("unsupported hook shell %q; supported shell: bash", shell)
	}
	return shellBash, nil
}

func parseHookTrackingKeys(raw string) ([]string, error) {
	keys := make(map[string]string)
	for _, key := range strings.Fields(raw) {
		if err := validateEnvKey(key); err != nil {
			return nil, fmt.Errorf("invalid %s entry %q: %w", envKinkoHookKeys, key, err)
		}
		if key == envKinkoHookKeys {
			return nil, fmt.Errorf("invalid %s entry %q: reserved tracking key", envKinkoHookKeys, key)
		}
		keys[key] = ""
	}
	return sortedKeys(keys), nil
}
