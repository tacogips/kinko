package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseImportAssignments_PosixRoundTrip(t *testing.T) {
	line, err := renderShellAssignment(shellPosix, "API_KEY", "a'b")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseImportAssignments(shellPosix, line+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["API_KEY"] != "a'b" {
		t.Fatalf("value=%q want=%q", got["API_KEY"], "a'b")
	}
}

func TestParseImportAssignments_FishRoundTrip(t *testing.T) {
	line, err := renderShellAssignment(shellFish, "DB_URL", "postgres://a b")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseImportAssignments(shellFish, line+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["DB_URL"] != "postgres://a b" {
		t.Fatalf("value=%q want=%q", got["DB_URL"], "postgres://a b")
	}
}

func TestParseImportAssignments_FishEscapes(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "trailing backslash", value: `C:\path\`},
		{name: "doubled backslash", value: `C:\\path`},
		{name: "embedded single quote", value: "can't"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, err := renderShellAssignment(shellFish, "VALUE", tc.value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := parseImportAssignments(shellFish, line+"\n")
			if err != nil {
				t.Fatalf("parse failed for %q: %v", line, err)
			}
			if got["VALUE"] != tc.value {
				t.Fatalf("value=%q want=%q", got["VALUE"], tc.value)
			}
		})
	}
}

func TestParseImportAssignments_FishRejectsInvalidEscapes(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantReason string
	}{
		// `'abc\';` is genuinely unbalanced fish quoting: `\'` is a
		// supported escape for a literal embedded quote (not a
		// terminator), so the quote never actually closes and the value is
		// unterminated. Verified against real fish 4.5.0: `fish -c "set
		// -gx VALUE 'abc\'; echo ok"` reports "Unexpected end of string,
		// quotes are not balanced". quoteFish never emits a lone trailing
		// backslash before the closing quote (it doubles backslashes), so
		// this input only exercises the parser's rejection of malformed
		// hand-crafted input, not a round-trip case.
		{name: "trailing backslash", line: `set -gx VALUE 'abc\';`, wantReason: "unterminated quoted value"},
		{name: "unsupported escape", line: `set -gx VALUE 'abc\n';`, wantReason: "unsupported escape sequence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseImportAssignments(shellFish, tc.line+"\n")
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseImportAssignments_NuRoundTrip(t *testing.T) {
	line, err := renderShellAssignment(shellNu, "MSG", "hi\nthere")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseImportAssignments(shellNu, line+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["MSG"] != "hi\nthere" {
		t.Fatalf("value=%q want=%q", got["MSG"], "hi\nthere")
	}
}

func TestParseImportAssignments_RedactsRawInput(t *testing.T) {
	payload := "export API_KEY=TOPSECRET trailing-token\n"
	_, err := parseImportAssignments(shellPosix, payload)
	if err == nil {
		t.Fatal("expected parse error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "import parse error (shell=posix, line=1)") {
		t.Fatalf("unexpected error format: %q", msg)
	}
	if strings.Contains(msg, "TOPSECRET") {
		t.Fatalf("error leaks value: %q", msg)
	}
	if strings.Contains(msg, "export API_KEY=") {
		t.Fatalf("error leaks raw assignment line: %q", msg)
	}
}

func TestParseImportAssignments_PosixCommonFormats(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		key     string
		wantVal string
	}{
		{
			name:    "export unquoted",
			line:    "export AWS_PROFILE=my-profile",
			key:     "AWS_PROFILE",
			wantVal: "my-profile",
		},
		{
			name:    "export single quoted",
			line:    "export API_KEY='abc123'",
			key:     "API_KEY",
			wantVal: "abc123",
		},
		{
			name:    "export double quoted",
			line:    "export API_KEY=\"abc123\"",
			key:     "API_KEY",
			wantVal: "abc123",
		},
		{
			name:    "plain assignment",
			line:    "PLAIN_KEY=plain",
			key:     "PLAIN_KEY",
			wantVal: "plain",
		},
		{
			name:    "empty value",
			line:    "export EMPTY=",
			key:     "EMPTY",
			wantVal: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseImportAssignments(shellPosix, tc.line+"\n")
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if got[tc.key] != tc.wantVal {
				t.Fatalf("value=%q want=%q", got[tc.key], tc.wantVal)
			}
		})
	}
}

func TestParseImportAssignments_PreservesQuotedWhitespace(t *testing.T) {
	got, err := parseImportAssignments(shellPosix, "export SPACED='  value  '\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["SPACED"] != "  value  " {
		t.Fatalf("SPACED=%q", got["SPACED"])
	}
}

func TestParseImportAssignments_LongLine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-line parse test in short mode")
	}
	longValue := strings.Repeat("a", 11*1024*1024)
	line, err := renderShellAssignment(shellPosix, "BIG", longValue)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseImportAssignments(shellPosix, line+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["BIG"] != longValue {
		t.Fatalf("value length=%d want=%d", len(got["BIG"]), len(longValue))
	}
}

func TestParseImportAssignments_IgnoresCommentLines(t *testing.T) {
	content := "# shared keys\nexport API_KEY='shared'\n# repository-specific keys\nexport API_KEY='repo'\n"
	got, err := parseImportAssignments(shellPosix, content)
	if err != nil {
		t.Fatal(err)
	}
	if got["API_KEY"] != "repo" {
		t.Fatalf("value=%q want=%q", got["API_KEY"], "repo")
	}
}

func TestParseImportScopes_OnlyExplicitMarkersSwitchScope(t *testing.T) {
	content := "# shared keys\nexport K='plain-comment'\n# kinko:scope=shared\nexport K='shared'\n# repository-specific keys\nexport K='still-shared'\n# kinko:scope=repo\nexport K='repo'\n"
	parsed, err := parseImportScopes(shellPosix, content, true)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.shared["K"] != "still-shared" {
		t.Fatalf("shared K=%q want=%q", parsed.shared["K"], "still-shared")
	}
	if parsed.repoSpecific["K"] != "repo" {
		t.Fatalf("repo K=%q want=%q", parsed.repoSpecific["K"], "repo")
	}
}

func TestRunImport_YesSkipsSummaryAndPromptsAndImports(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.confirm = true

	in := strings.NewReader("export API_KEY='secret'\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes"}, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	errOut := errBuf.String()
	if strings.Contains(errOut, "Planned import:") {
		t.Fatalf("unexpected summary output: %q", errOut)
	}
	if strings.Contains(errOut, "Import 1 keys into profile=") {
		t.Fatalf("unexpected mutation prompt: %q", errOut)
	}
	if got := valueAtScope(t, opts, "API_KEY"); got != "secret" {
		t.Fatalf("API_KEY=%q", got)
	}
	if out.String() != "imported 1 keys\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunImport_YesConfirmWithValuesWithoutForceSkipsPrompts(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.confirm = true

	in := strings.NewReader("export API_KEY='secret'\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes", "--confirm-with-values"}, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	errOut := errBuf.String()
	if strings.Contains(errOut, "Show values in confirmation summary? [y/N]: ") {
		t.Fatalf("unexpected value-disclosure prompt: %q", errOut)
	}
	if strings.Contains(errOut, "Import 1 keys into profile=") {
		t.Fatalf("unexpected mutation prompt: %q", errOut)
	}
	if strings.Contains(errOut, "Planned import:") {
		t.Fatalf("unexpected summary output: %q", errOut)
	}
	if strings.Contains(errOut, "API_KEY=secret") {
		t.Fatalf("unexpected value output: %q", errOut)
	}
	if got := valueAtScope(t, opts, "API_KEY"); got != "secret" {
		t.Fatalf("API_KEY=%q", got)
	}
	if out.String() != "imported 1 keys\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestImportPromptPolicy(t *testing.T) {
	cases := []struct {
		name              string
		confirmWithValues bool
		autoYes           bool
		stderrIsTTY       bool
		wantValuePrompt   bool
		wantMutation      bool
	}{
		{
			name:              "confirm with values on tty",
			confirmWithValues: true,
			autoYes:           false,
			stderrIsTTY:       true,
			wantValuePrompt:   true,
			wantMutation:      true,
		},
		{
			name:              "yes disables all prompts",
			confirmWithValues: true,
			autoYes:           true,
			stderrIsTTY:       true,
			wantValuePrompt:   false,
			wantMutation:      false,
		},
		{
			name:              "no value prompt on non tty",
			confirmWithValues: true,
			autoYes:           false,
			stderrIsTTY:       false,
			wantValuePrompt:   false,
			wantMutation:      true,
		},
		{
			name:              "confirm-with-values disabled",
			confirmWithValues: false,
			autoYes:           false,
			stderrIsTTY:       true,
			wantValuePrompt:   false,
			wantMutation:      true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPromptImportValueDisclosure(tc.confirmWithValues, tc.autoYes, tc.stderrIsTTY); got != tc.wantValuePrompt {
				t.Fatalf("shouldPromptImportValueDisclosure()=%v want=%v", got, tc.wantValuePrompt)
			}
			if got := shouldPromptImportMutation(tc.autoYes); got != tc.wantMutation {
				t.Fatalf("shouldPromptImportMutation()=%v want=%v", got, tc.wantMutation)
			}
		})
	}
}

func TestRunExport_EmitsSharedAndRepoSpecificBlocksWithCommentsAndOverride(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "API_KEY=shared", "GLOBAL_ONLY=global"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"API_KEY=repo", "LOCAL_ONLY=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runExport(opts, []string{"posix"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	exported := out.String()
	if !strings.Contains(exported, "# kinko:scope=shared") {
		t.Fatalf("missing shared marker by default: %q", exported)
	}
	if !strings.Contains(exported, "# kinko:scope=repo") {
		t.Fatalf("missing repo marker by default: %q", exported)
	}
	if strings.Index(exported, "export API_KEY='shared'") > strings.Index(exported, "export API_KEY='repo'") {
		t.Fatalf("expected shared API_KEY before repo API_KEY for override precedence: %q", exported)
	}
	parsed, err := parseImportAssignments(shellPosix, exported)
	if err != nil {
		t.Fatal(err)
	}
	if parsed["API_KEY"] != "repo" {
		t.Fatalf("API_KEY=%q want=%q", parsed["API_KEY"], "repo")
	}
	if parsed["GLOBAL_ONLY"] != "global" {
		t.Fatalf("GLOBAL_ONLY=%q want=%q", parsed["GLOBAL_ONLY"], "global")
	}
	if parsed["LOCAL_ONLY"] != "local" {
		t.Fatalf("LOCAL_ONLY=%q want=%q", parsed["LOCAL_ONLY"], "local")
	}
}

func TestRunExport_WithScopeCommentsEmitsMarkers(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "API_KEY=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"API_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runExport(opts, []string{"posix", "--with-scope-comments"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	exported := out.String()
	if !strings.Contains(exported, "# kinko:scope=shared") {
		t.Fatalf("missing shared marker: %q", exported)
	}
	if !strings.Contains(exported, "# kinko:scope=repo") {
		t.Fatalf("missing repo marker: %q", exported)
	}
}

func TestRunExport_SharedOnlyOmitsRepoSpecificBlock(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "API_KEY=shared", "GLOBAL_ONLY=global"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"API_KEY=repo", "LOCAL_ONLY=local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runExport(opts, []string{"posix", "--shared-only"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	exported := out.String()
	if !strings.Contains(exported, "# kinko:scope=shared") {
		t.Fatalf("missing shared marker by default: %q", exported)
	}
	if strings.Contains(exported, "# kinko:scope=repo") {
		t.Fatalf("unexpected repo marker in shared-only export: %q", exported)
	}
	if !strings.Contains(exported, "export API_KEY='shared'") {
		t.Fatalf("missing shared API_KEY in shared-only export: %q", exported)
	}
	if strings.Contains(exported, "export API_KEY='repo'") {
		t.Fatalf("unexpected repo API_KEY in shared-only export: %q", exported)
	}
	if strings.Contains(exported, "LOCAL_ONLY") {
		t.Fatalf("unexpected repo-only key in shared-only export: %q", exported)
	}
}

func TestRunExport_ExcludeFiltersSharedAndRepoScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "API_KEY=shared", "GLOBAL_ONLY=global", "DROP_ME=shared_drop"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"API_KEY=repo", "LOCAL_ONLY=local", "DROP_ME=repo_drop"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runExport(opts, []string{"posix", "--exclude", "API_KEY,DROP_ME"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	exported := out.String()
	if strings.Contains(exported, "API_KEY=") {
		t.Fatalf("excluded API_KEY found in export: %q", exported)
	}
	if strings.Contains(exported, "DROP_ME=") {
		t.Fatalf("excluded DROP_ME found in export: %q", exported)
	}
	if !strings.Contains(exported, "export GLOBAL_ONLY='global'") {
		t.Fatalf("missing non-excluded shared key: %q", exported)
	}
	if !strings.Contains(exported, "export LOCAL_ONLY='local'") {
		t.Fatalf("missing non-excluded repo key: %q", exported)
	}
}

func TestRunExport_ExcludeRepeatableAndWhitespaceAndUnknownKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"C=3"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runExport(opts, []string{"posix", "--exclude", "A, MISSING ", "--exclude", "C"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	exported := out.String()
	if strings.Contains(exported, "A=") || strings.Contains(exported, "C=") {
		t.Fatalf("excluded key found in export: %q", exported)
	}
	if !strings.Contains(exported, "export B='2'") {
		t.Fatalf("expected non-excluded key B in export: %q", exported)
	}
}

func TestRunExport_ExcludeRejectsInvalidKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runExport(opts, []string{"posix", "--exclude", "1INVALID"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected invalid exclude key error")
	}
	if !strings.Contains(err.Error(), "invalid --exclude key \"1INVALID\"") {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodePolicyFailed)
	}
}

func TestRunExport_LockedSessionExitCode(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	opts := globalOptions{
		dataDir: dataDir,
		profile: defaultProfile,
		path:    t.TempDir(),
		force:   true,
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runExport(opts, []string{"posix"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected locked session error")
	}
	if code := ExitCode(err); code != exitCodeIOFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeIOFailed)
	}
}

func TestRunExport_ExcludeScopeCommentBehaviorWhenBlocksBecomeEmpty(t *testing.T) {
	t.Run("one block empty after exclusion", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		opts.force = true
		opts.confirm = false

		var out bytes.Buffer
		if err := runSet(opts, []string{"--shared", "DROP_SHARED=1"}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
		if err := runSet(opts, []string{"KEEP_REPO=2"}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}

		out.Reset()
		var errBuf bytes.Buffer
		if err := runExport(opts, []string{"posix", "--with-scope-comments", "--exclude", "DROP_SHARED"}, strings.NewReader(""), &out, &errBuf); err != nil {
			t.Fatal(err)
		}
		exported := out.String()
		if strings.Contains(exported, "# kinko:scope=shared") {
			t.Fatalf("unexpected shared scope marker for emptied shared block: %q", exported)
		}
		if !strings.Contains(exported, "# kinko:scope=repo") {
			t.Fatalf("missing repo scope marker: %q", exported)
		}
		if !strings.Contains(exported, "export KEEP_REPO='2'") {
			t.Fatalf("missing repo assignment: %q", exported)
		}
	})

	t.Run("both blocks empty after exclusion", func(t *testing.T) {
		opts := setupUnlockedForSet(t)
		opts.force = true
		opts.confirm = false

		var out bytes.Buffer
		if err := runSet(opts, []string{"--shared", "DROP_SHARED=1"}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
		if err := runSet(opts, []string{"DROP_REPO=2"}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}

		out.Reset()
		var errBuf bytes.Buffer
		if err := runExport(opts, []string{"posix", "--with-scope-comments", "--exclude", "DROP_SHARED,DROP_REPO"}, strings.NewReader(""), &out, &errBuf); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("expected empty export when all blocks are excluded, got %q", out.String())
		}
	})
}

func TestRunImport_ExportRoundTripWithExcludeImportsOnlyNonExcludedAssignments(t *testing.T) {
	src := setupUnlockedForSet(t)
	src.force = true
	src.confirm = false

	var srcOut bytes.Buffer
	if err := runSet(src, []string{"--shared", "SHARED_KEEP=shared", "SHARED_DROP=drop", "DUP=shared_dup"}, strings.NewReader(""), &srcOut); err != nil {
		t.Fatal(err)
	}
	if err := runSet(src, []string{"REPO_KEEP=repo", "REPO_DROP=drop", "DUP=repo_dup"}, strings.NewReader(""), &srcOut); err != nil {
		t.Fatal(err)
	}

	srcOut.Reset()
	var srcErr bytes.Buffer
	if err := runExport(src, []string{"posix", "--exclude", "SHARED_DROP,REPO_DROP,DUP"}, strings.NewReader(""), &srcOut, &srcErr); err != nil {
		t.Fatal(err)
	}
	exported := srcOut.String()

	dst := setupUnlockedForSet(t)
	var dstOut bytes.Buffer
	var dstErr bytes.Buffer
	if err := runImport(dst, []string{"--yes"}, strings.NewReader(exported), &dstOut, &dstErr); err != nil {
		t.Fatal(err)
	}

	if got := valueAtShared(t, dst, "SHARED_KEEP"); got != "shared" {
		t.Fatalf("SHARED_KEEP(shared)=%q want=%q", got, "shared")
	}
	if got := valueAtScope(t, dst, "REPO_KEEP"); got != "repo" {
		t.Fatalf("REPO_KEEP(repo)=%q want=%q", got, "repo")
	}

	dek, err := loadUnlockedDEK(dst.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(dst.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vd.Shared["SHARED_DROP"]; ok {
		t.Fatalf("SHARED_DROP must not be imported: %v", vd.Shared)
	}
	if _, ok := vd.Shared["DUP"]; ok {
		t.Fatalf("DUP must not be imported in shared scope: %v", vd.Shared)
	}
	if profileScope := vd.Profiles[dst.profile]; profileScope != nil {
		if repoScope := profileScope[dst.path]; repoScope != nil {
			if _, ok := repoScope["REPO_DROP"]; ok {
				t.Fatalf("REPO_DROP must not be imported: %v", repoScope)
			}
			if _, ok := repoScope["DUP"]; ok {
				t.Fatalf("DUP must not be imported in repo scope: %v", repoScope)
			}
		}
	}
}

func TestRunImport_ExportRoundTripPreservesSharedAndRepoSpecificScopesByDefault(t *testing.T) {
	src := setupUnlockedForSet(t)
	src.force = true
	src.confirm = false

	var srcOut bytes.Buffer
	if err := runSet(src, []string{"--shared", "SHARED_ONLY=shared", "DUP=shared"}, strings.NewReader(""), &srcOut); err != nil {
		t.Fatal(err)
	}
	if err := runSet(src, []string{"DUP=repo", "REPO_ONLY=repo"}, strings.NewReader(""), &srcOut); err != nil {
		t.Fatal(err)
	}

	srcOut.Reset()
	var srcErr bytes.Buffer
	if err := runExport(src, []string{"posix"}, strings.NewReader(""), &srcOut, &srcErr); err != nil {
		t.Fatal(err)
	}
	exported := srcOut.String()

	dst := setupUnlockedForSet(t)
	var dstOut bytes.Buffer
	var dstErr bytes.Buffer
	if err := runImport(dst, []string{"--yes"}, strings.NewReader(exported), &dstOut, &dstErr); err != nil {
		t.Fatal(err)
	}

	if got := valueAtShared(t, dst, "SHARED_ONLY"); got != "shared" {
		t.Fatalf("SHARED_ONLY(shared)=%q want=%q", got, "shared")
	}
	if got := valueAtShared(t, dst, "DUP"); got != "shared" {
		t.Fatalf("DUP(shared)=%q want=%q", got, "shared")
	}
	if got := valueAtScope(t, dst, "DUP"); got != "repo" {
		t.Fatalf("DUP(repo)=%q want=%q", got, "repo")
	}
	if got := valueAtScope(t, dst, "REPO_ONLY"); got != "repo" {
		t.Fatalf("REPO_ONLY(repo)=%q want=%q", got, "repo")
	}
}

func TestRunImport_SharedOnlyDoesNotCreateRepoScope(t *testing.T) {
	opts := setupUnlockedForSet(t)
	input := "# kinko:scope=shared\nexport SHARED_ONLY='value'\n"

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes", "--allow-shared"}, strings.NewReader(input), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if got := vd.Shared["SHARED_ONLY"]; got != "value" {
		t.Fatalf("SHARED_ONLY(shared)=%q want=%q", got, "value")
	}
	if _, ok := vd.Profiles[opts.profile]; ok {
		t.Fatalf("unexpected profile created for shared-only import: %q", opts.profile)
	}
}

func TestRunImport_AcceptsSharedScopeMarkersWithoutAllowShared(t *testing.T) {
	opts := setupUnlockedForSet(t)
	input := "# kinko:scope=shared\nexport SHARED_ONLY='value'\n"

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runImport(opts, []string{"--yes"}, strings.NewReader(input), &out, &errBuf)
	if err != nil {
		t.Fatalf("import failed unexpectedly: %v", err)
	}
	if got := valueAtShared(t, opts, "SHARED_ONLY"); got != "value" {
		t.Fatalf("SHARED_ONLY(shared)=%q want=%q", got, "value")
	}
}

func TestRunImport_RejectsFileAndNonEmptyStdin(t *testing.T) {
	opts := setupUnlockedForSet(t)
	filePath := filepath.Join(t.TempDir(), "envrc.private")
	if err := os.WriteFile(filePath, []byte("export API_KEY='from-file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdinPayload := strings.NewReader("export API_KEY='from-stdin'\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runImport(opts, []string{"--yes", "--file", filePath}, stdinPayload, &out, &errBuf)
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

func TestRunImport_MissingFileExitCode(t *testing.T) {
	opts := setupUnlockedForSet(t)
	missingPath := filepath.Join(t.TempDir(), "missing.env")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runImport(opts, []string{"--yes", "--file", missingPath}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "read --file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := ExitCode(err); code != exitCodeIOFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeIOFailed)
	}
}

func TestRunImport_FileInputAllowsEmptyRedirectedStdin(t *testing.T) {
	opts := setupUnlockedForSet(t)
	filePath := filepath.Join(t.TempDir(), "envrc.private")
	if err := os.WriteFile(filePath, []byte("export API_KEY='from-file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes", "--file", filePath}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "API_KEY"); got != "from-file" {
		t.Fatalf("API_KEY=%q want=%q", got, "from-file")
	}
}

func TestRunImport_RejectsInvalidScopeMarker(t *testing.T) {
	opts := setupUnlockedForSet(t)
	input := "# kinko:scope=shraed\nexport SHARED_ONLY='value'\n"

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runImport(opts, []string{"--yes"}, strings.NewReader(input), &out, &errBuf)
	if err == nil {
		t.Fatal("expected invalid scope marker to fail")
	}
	if !strings.Contains(err.Error(), "invalid scope marker") {
		t.Fatalf("err=%v", err)
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodePolicyFailed)
	}
}

func TestRunImport_MutationLockConflictExitCode(t *testing.T) {
	opts := setupUnlockedForSet(t)
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err = runImport(opts, []string{"--yes"}, strings.NewReader("export API_KEY='secret'\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected mutation lock conflict")
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeLockConflict)
	}
}
