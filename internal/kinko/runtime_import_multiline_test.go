package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers regression tests for three code-review findings in
// internal/kinko/runtime_io_commands.go:
//
//   - Finding 1: posix/fish import parsing must support quoted values that
//     span multiple physical lines, since quotePosix/quoteFish can emit
//     literal embedded newlines inside single-quoted output.
//   - Finding 2: parsePosixDoubleQuotedImportValue must scan byte-by-byte
//     with escape awareness instead of only checking the first/last byte.
//   - Finding 3: runImportWithOptions's --file/stdin-pipe conflict check
//     must use a single bounded Read instead of an unbounded io.ReadAll.
//
// Kept in a separate file (rather than appended to
// runtime_import_export_test.go) to keep individual test files under the
// project's 1000-line soft limit for Go source files.

// --- Finding 1: multi-line quoted value round-trip (posix/fish) ---

func TestParseImportAssignments_MultilineQuotedValueRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "single embedded newline", value: "line1\nline2"},
		{name: "embedded newline and single quote", value: "line1's\nline2"},
		{name: "trailing newline", value: "line1\nline2\n"},
		{name: "value is only a trailing newline", value: "value\n"},
	}
	for _, shell := range []string{shellPosix, shellFish} {
		shell := shell
		for _, tc := range cases {
			tc := tc
			t.Run(shell+"/"+tc.name, func(t *testing.T) {
				line, err := renderShellAssignment(shell, "SOME_KEY", tc.value)
				if err != nil {
					t.Fatal(err)
				}
				got, err := parseImportAssignments(shell, line+"\n")
				if err != nil {
					t.Fatalf("parse failed for rendered assignment: %v", err)
				}
				if got["SOME_KEY"] != tc.value {
					t.Fatalf("value=%q want=%q", got["SOME_KEY"], tc.value)
				}
			})
		}
	}
}

func TestParseImportAssignments_MultilineQuotedValueRoundTrip_MultiKeyDocument(t *testing.T) {
	for _, shell := range []string{shellPosix, shellFish} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			first, err := renderShellAssignment(shell, "BEFORE", "plain")
			if err != nil {
				t.Fatal(err)
			}
			multi, err := renderShellAssignment(shell, "MULTI", "line1's\nline2")
			if err != nil {
				t.Fatal(err)
			}
			last, err := renderShellAssignment(shell, "AFTER", "plain2")
			if err != nil {
				t.Fatal(err)
			}
			content := first + "\n" + multi + "\n" + last + "\n"
			got, err := parseImportAssignments(shell, content)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if got["BEFORE"] != "plain" {
				t.Fatalf("BEFORE=%q", got["BEFORE"])
			}
			if got["MULTI"] != "line1's\nline2" {
				t.Fatalf("MULTI=%q", got["MULTI"])
			}
			if got["AFTER"] != "plain2" {
				t.Fatalf("AFTER=%q", got["AFTER"])
			}
		})
	}
}

// TestParseImportAssignments_CRLFInsideQuotesUnchanged pins the current
// (pre- and post-Finding-1) behavior for '\r' bytes that appear WITHIN a
// single physical line's quoted value (not at a "\n" line-split boundary):
// none of the posix/fish quoted-value scanners special-case '\r', so it
// passes through as ordinary literal content inside quotes, both before and
// after Finding 1's multi-line restructuring.
//
// Note: a '\r' immediately followed by '\n' (i.e. a real CRLF line ending)
// is a different case - parseImportScopes's per-physical-line
// strings.TrimSpace(raw) call already strips trailing '\r' from a line
// before that line is joined with the next one (this is pre-existing
// behavior, not something Finding 1 changed), so CRLF-terminated lines are
// not covered by this "unchanged" pin; a mid-line '\r' not touching a "\n"
// boundary is the faithful case to pin here.
func TestParseImportAssignments_CRLFInsideQuotesUnchanged(t *testing.T) {
	cases := []struct {
		shell string
		line  string
		key   string
		want  string
	}{
		{shell: shellPosix, line: "export CRLF='a\rb'", key: "CRLF", want: "a\rb"},
		{shell: shellFish, line: "set -gx CRLF 'a\rb';", key: "CRLF", want: "a\rb"},
		{shell: shellPosix, line: "export CRLF=\"a\rb\"", key: "CRLF", want: "a\rb"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			got, err := parseImportAssignments(tc.shell, tc.line+"\n")
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if got[tc.key] != tc.want {
				t.Fatalf("value=%q want=%q", got[tc.key], tc.want)
			}
		})
	}
}

// --- Finding 1: error line number must cite the ORIGINAL starting line ---

func TestParseImportScopes_UnterminatedMultilineValueCitesOriginalStartLine(t *testing.T) {
	cases := []struct {
		name    string
		shell   string
		content string
	}{
		{
			name:    "posix single-quoted, EOF before closing quote",
			shell:   shellPosix,
			content: "export A=1\nexport B=2\n\nexport BROKEN='opening line four\nmore content\nmore content again\n",
		},
		{
			name:    "fish single-quoted, EOF before closing quote",
			shell:   shellFish,
			content: "set -gx A 'ok';\nset -gx B 'ok';\n\nset -gx BROKEN 'opening line four\nmore content\nmore content again\n",
		},
		{
			name:    "posix double-quoted, EOF before closing quote",
			shell:   shellPosix,
			content: "export A=1\nexport B=2\n\nexport BROKEN=\"opening line four\nmore content\nmore content again\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseImportAssignments(tc.shell, tc.content)
			if err == nil {
				t.Fatal("expected parse error")
			}
			wantSubstr := "line=4)"
			if !strings.Contains(err.Error(), wantSubstr) {
				t.Fatalf("error=%q does not cite original start line (want substring %q)", err.Error(), wantSubstr)
			}
			if !strings.Contains(err.Error(), "unterminated quoted value") {
				t.Fatalf("unexpected reason: %v", err)
			}
		})
	}
}

// --- Finding 2: parsePosixDoubleQuotedImportValue byte-by-byte scanning ---

func TestParseImportAssignments_PosixDoubleQuoteEscapes(t *testing.T) {
	got, err := parseImportAssignments(shellPosix, `export K="a\\b\"c\$d\`+"`"+`e"`+"\n")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	want := "a\\b\"c$d`e"
	if got["K"] != want {
		t.Fatalf("K=%q want=%q", got["K"], want)
	}
}

func TestParseImportAssignments_PosixDoubleQuoteTrailingContentIsParseError(t *testing.T) {
	_, err := parseImportAssignments(shellPosix, `K="a"b"`+"\n")
	if err == nil {
		t.Fatal("expected parse error for trailing content after closing quote")
	}
	msg := err.Error()
	if strings.Contains(msg, `K="a"b"`) {
		t.Fatalf("error must not leak raw assignment line: %q", msg)
	}
	if strings.Contains(msg, `"a"b"`) {
		t.Fatalf("error must not leak parsed/partial value content: %q", msg)
	}
}

func TestParseImportAssignments_PosixDoubleQuoteEscapedTrailingQuoteIsUnterminated(t *testing.T) {
	// Raw value text is literally `"abc\"` - an opening quote, `abc`, a
	// backslash, then a quote. The trailing `"` is escaped by the preceding
	// backslash (part of the \" escape sequence, not a terminator), so this
	// value never actually closes.
	_, err := parseImportAssignments(shellPosix, "K=\"abc\\\"\n")
	if err == nil {
		t.Fatal("expected parse error for unterminated escaped-quote value")
	}
	if !strings.Contains(err.Error(), "unterminated quoted value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Finding 3: bounded stdin read for --file/pipe conflict detection ---

func TestRunImport_FileInputAllowsLargeWhitespaceStdinUnderBound(t *testing.T) {
	opts := setupUnlockedForSet(t)
	filePath := filepath.Join(t.TempDir(), "envrc.private")
	if err := os.WriteFile(filePath, []byte("export API_KEY='from-file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	whitespace := strings.Repeat(" \n", 2000) // 4000 bytes, under the 4096 bound
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes", "--file", filePath}, strings.NewReader(whitespace), &out, &errBuf); err != nil {
		t.Fatalf("expected no conflict for whitespace-only stdin under bound, got: %v", err)
	}
	if got := valueAtScope(t, opts, "API_KEY"); got != "from-file" {
		t.Fatalf("API_KEY=%q want=%q", got, "from-file")
	}
}

func TestRunImport_FileInputDetectsConflictWithinBoundedRead(t *testing.T) {
	opts := setupUnlockedForSet(t)
	filePath := filepath.Join(t.TempDir(), "envrc.private")
	if err := os.WriteFile(filePath, []byte("export API_KEY='from-file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := strings.Repeat(" ", 2000) + "export API_KEY='from-stdin'\n"
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runImport(opts, []string{"--yes", "--file", filePath}, strings.NewReader(payload), &out, &errBuf)
	if err == nil {
		t.Fatal("expected --file plus stdin rejection")
	}
	if !strings.Contains(err.Error(), "either --file or stdin pipe") {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodePolicyFailed)
	}
}
