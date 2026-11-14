package kinko

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportCommentMetadataCannotExecuteShellCommands(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, field := range []string{"path", "profile"} {
		t.Run(field, func(t *testing.T) {
			opts := setupUnlockedForSet(t)
			opts.keychainPreflight = "off"
			payload := "untrusted\nprintf INJECTED #"
			if field == "path" {
				opts.path = filepath.Join(t.TempDir(), payload)
			} else {
				opts.profile = payload
			}
			if err := finalizeGlobalOptions(&opts); err != nil {
				t.Fatal(err)
			}
			if err := runSet(opts, []string{"SAFE=dummy"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			opts.force = true
			var output bytes.Buffer
			if err := runExportWithOptions(opts, exportOptions{shell: shellPosix, withScopeComments: true}, strings.NewReader(""), &output, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(sh, "-c", output.String()+"\nprintf '%s' \"$SAFE\"")
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("evaluate export: %v: %s", err, got)
			}
			if string(got) != "dummy" {
				t.Fatalf("export evaluation produced %q; want only dummy value", got)
			}
		})
	}
}

func TestExportCommentMetadataEscapesControlCharacters(t *testing.T) {
	for _, shell := range []string{shellPosix, shellFish, shellNu} {
		t.Run(shell, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeExportBlock(&output, shell, "repo", "profile=a\r\n\t\x1bpath=b", map[string]string{"SAFE": "dummy"}, true); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
			if len(lines) != 3 || !strings.HasPrefix(lines[1], "# ") || strings.ContainsAny(lines[1], "\r\t\x1b") {
				t.Fatalf("unsafe comment output: %q", output.String())
			}
		})
	}
}

func TestSetMalformedInputDoesNotDisclosePlaintext(t *testing.T) {
	const dummy = "DUMMY_PRIVATE_INPUT"
	for _, input := range []string{dummy, "=" + dummy, dummy + " invalid=value"} {
		t.Run(fmt.Sprintf("input-%d", len(input)), func(t *testing.T) {
			_, _, err := parseSetAssignment(input)
			if err == nil || strings.Contains(err.Error(), dummy) {
				t.Fatalf("expected redacted parse failure, got %v", err)
			}
			_, err = parseSetAssignmentsFromReader(strings.NewReader("SAFE=dummy\n" + input + "\n"))
			if err == nil || strings.Contains(err.Error(), dummy) || !strings.Contains(err.Error(), "line 2") {
				t.Fatalf("expected redacted failure with line number, got %v", err)
			}
		})
	}
}
