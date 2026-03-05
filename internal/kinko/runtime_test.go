package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeShell(t *testing.T) {
	cases := map[string]string{
		"posix": "posix",
		"bash":  "posix",
		"zsh":   "posix",
		"sh":    "posix",
		"fish":  "fish",
		"nu":    "nu",
	}
	for in, want := range cases {
		got, err := normalizeShell(in)
		if err != nil {
			t.Fatalf("normalizeShell(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeShell(%q)=%q want=%q", in, got, want)
		}
	}
}

func TestMaskValue(t *testing.T) {
	if got := maskValue("abcd"); got != "****" {
		t.Fatalf("mask short=%q", got)
	}
	if got := maskValue("abcdefgh"); got != "ab****gh" {
		t.Fatalf("mask long=%q", got)
	}
}

func TestParseGetArgs_AllowsFlagsAfterKey(t *testing.T) {
	key, reveal, force, err := parseGetArgs([]string{"GITHUB_TOKEN", "--reveal", "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "GITHUB_TOKEN" {
		t.Fatalf("key=%q want=%q", key, "GITHUB_TOKEN")
	}
	if !reveal {
		t.Fatal("expected reveal=true")
	}
	if !force {
		t.Fatal("expected force=true")
	}
}

func TestRunGet_RevealAndForceAfterKeyWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)

	var out bytes.Buffer
	if err := runSet(opts, []string{"GITHUB_TOKEN=secret-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(opts, []string{"GITHUB_TOKEN", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); got != "secret-value\n" {
		t.Fatalf("output=%q want=%q", got, "secret-value\n")
	}
}

func TestRunGet_SameKeyAcrossDirectoriesResolvesBySelectedPath(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	pathA := filepath.Join(base, "a")
	pathB := filepath.Join(base, "b")

	optsA := opts
	optsA.path = pathA
	if err := runSet(optsA, []string{"DUP=alpha"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	optsB := opts
	optsB.path = pathB
	if err := runSet(optsB, []string{"DUP=bravo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(optsA, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get(pathA) failed: %v", err)
	}
	if got := out.String(); got != "alpha\n" {
		t.Fatalf("get(pathA)=%q want=%q", got, "alpha\n")
	}

	out.Reset()
	errBuf.Reset()
	if err := runGet(optsB, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get(pathB) failed: %v", err)
	}
	if got := out.String(); got != "bravo\n" {
		t.Fatalf("get(pathB)=%q want=%q", got, "bravo\n")
	}
}

func TestRunGet_PrefersDirectoryLocalOverSharedForSameKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer

	if err := runSet(opts, []string{"--shared", "DUP=shared-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"DUP=local-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runGet(opts, []string{"DUP", "--reveal", "--force"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got := out.String(); got != "local-value\n" {
		t.Fatalf("get=%q want=%q", got, "local-value\n")
	}
}

func TestRenderPosix(t *testing.T) {
	got, err := renderShellAssignment("posix", "API_KEY", "a'b")
	if err != nil {
		t.Fatal(err)
	}
	want := "export API_KEY='a'\"'\"'b'"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestSelectExecSecrets(t *testing.T) {
	secrets := map[string]string{"FOO": "bar", "BAR": "baz"}

	all, err := selectExecSecrets(secrets, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["FOO"] != "bar" || all["BAR"] != "baz" {
		t.Fatalf("unexpected all selection: %#v", all)
	}

	subset, err := selectExecSecrets(secrets, false, "FOO")
	if err != nil {
		t.Fatal(err)
	}
	if len(subset) != 1 || subset["FOO"] != "bar" {
		t.Fatalf("unexpected subset selection: %#v", subset)
	}
}

func TestSelectExecSecrets_RequiresSelection(t *testing.T) {
	_, err := selectExecSecrets(map[string]string{"FOO": "bar"}, false, "")
	if err == nil {
		t.Fatal("expected selection error")
	}
}

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

func TestShowSecrets_MergesSharedAndRepoSpecific(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=shared", "B=shared-b"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"A=repo", "C=repo-c"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	got, err := showSecrets(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "repo" {
		t.Fatalf("A=%q want=%q", got["A"], "repo")
	}
	if got["B"] != "shared-b" {
		t.Fatalf("B=%q want=%q", got["B"], "shared-b")
	}
	if got["C"] != "repo-c" {
		t.Fatalf("C=%q want=%q", got["C"], "repo-c")
	}
}

func TestRunShow_DefaultViewGroupsSharedAndResolvedPathScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false
	var out bytes.Buffer

	if err := runSet(opts, []string{"--shared", "DUP=shared", "SHARED_ONLY=shared-a"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"DUP=repo", "REPO_ONLY=repo-b"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--reveal"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "# shared\n") {
		t.Fatalf("missing shared header: %q", got)
	}
	if !strings.Contains(got, "\n# path="+opts.path+"\n") {
		t.Fatalf("missing resolved path header: %q", got)
	}
	if strings.Count(got, "DUP=") != 2 {
		t.Fatalf("default show must preserve per-scope DUP values: %q", got)
	}
	if !strings.Contains(got, "DUP=shared") || !strings.Contains(got, "DUP=repo") {
		t.Fatalf("expected both shared and repo DUP values: %q", got)
	}
	if !strings.Contains(got, "SHARED_ONLY=shared-a") {
		t.Fatalf("missing shared-only key: %q", got)
	}
	if !strings.Contains(got, "REPO_ONLY=repo-b") {
		t.Fatalf("missing repo-only key: %q", got)
	}
}

func TestRunShow_AllScopes_MaskedAndSortedByPathAndKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	pathAValue := filepath.Join(base, "a")
	pathBValue := filepath.Join(base, "b")

	if err := runSet(opts, []string{"--shared", "Z_SHARED=shared-z", "A_SHARED=shared-a"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	pathA := opts
	pathA.path = pathAValue
	if err := runSet(pathA, []string{"B=2", "A=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	pathB := opts
	pathB.path = pathBValue
	if err := runSet(pathB, []string{"C=3"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "# profile=default\n\n# shared\n") {
		t.Fatalf("missing profile/shared headers: %q", got)
	}
	if strings.Index(got, "A_SHARED=sh****-a") == -1 {
		t.Fatalf("expected masked A_SHARED in output: %q", got)
	}
	if strings.Index(got, "Z_SHARED=sh****-z") == -1 {
		t.Fatalf("expected masked Z_SHARED in output: %q", got)
	}
	if strings.Index(got, "# path="+pathAValue) == -1 || strings.Index(got, "# path="+pathBValue) == -1 {
		t.Fatalf("missing path headers: %q", got)
	}
	if strings.Index(got, "# path="+pathAValue) > strings.Index(got, "# path="+pathBValue) {
		t.Fatalf("paths not sorted: %q", got)
	}
	if strings.Index(got, "A=****") == -1 || strings.Index(got, "B=****") == -1 {
		t.Fatalf("expected masked path values: %q", got)
	}
	if strings.Index(got, "A=****") > strings.Index(got, "B=****") {
		t.Fatalf("keys in /tmp/a not sorted: %q", got)
	}
}

func TestRunShow_AllScopes_RevealShowsPlaintext(t *testing.T) {
	opts := setupUnlockedForSet(t)
	opts.force = true
	opts.confirm = false
	var out bytes.Buffer
	base := t.TempDir()
	pathAValue := filepath.Join(base, "a")

	if err := runSet(opts, []string{"--shared", "S=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	pathA := opts
	pathA.path = pathAValue
	if err := runSet(pathA, []string{"A=repo-a"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--all-scopes", "--reveal"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "S=shared") {
		t.Fatalf("expected shared plaintext: %q", got)
	}
	if !strings.Contains(got, "A=repo-a") {
		t.Fatalf("expected path plaintext: %q", got)
	}
}

func TestRunShow_AllScopes_RevealBlockedWithoutForceOnRedirectedOutput(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := runSet(opts, []string{"--shared", "S=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := runShow(opts, []string{"--all-scopes", "--reveal"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected reveal guard error")
	}
	if !strings.Contains(err.Error(), "sensitive output blocked for non-tty/redirection") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShow_AllScopes_EmptyProfileStillPrintsProfileAndSharedHeaders(t *testing.T) {
	opts := setupUnlockedForSet(t)

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "# profile=default\n\n# shared\n") {
		t.Fatalf("missing profile/shared headers for empty profile output: %q", got)
	}
	if strings.Contains(got, "# path=") {
		t.Fatalf("unexpected path section for empty profile output: %q", got)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("expected no warning stderr, got %q", errBuf.String())
	}
}

func TestRunShow_AllScopes_OmitsEmptyPathSections(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	base := t.TempDir()
	emptyScopePath := filepath.Join(base, "empty-scope")
	nonEmptyScopePath := filepath.Join(base, "non-empty-scope")

	emptyScope := opts
	emptyScope.path = emptyScopePath
	if err := runSet(emptyScope, []string{"ONLY=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDelete(emptyScope, []string{"--yes", "ONLY"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	nonEmptyScope := opts
	nonEmptyScope.path = nonEmptyScopePath
	if err := runSet(nonEmptyScope, []string{"A=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "# path="+emptyScopePath) {
		t.Fatalf("unexpected empty path section: %q", got)
	}
	if !strings.Contains(got, "# path="+nonEmptyScopePath) {
		t.Fatalf("missing non-empty path section: %q", got)
	}
}

func TestRunShow_AllScopes_NormalizesPathHeadersAndSortOrder(t *testing.T) {
	opts := setupUnlockedForSet(t)
	base := t.TempDir()
	rawPathA := base + string(filepath.Separator) + "z" + string(filepath.Separator) + ".." + string(filepath.Separator) + "a"
	pathAValue := filepath.Join(base, "a")
	pathBValue := filepath.Join(base, "b")

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	vd.Profiles[opts.profile][rawPathA] = map[string]string{"A": "1"}
	vd.Profiles[opts.profile][pathBValue] = map[string]string{"B": "2"}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "# path="+rawPathA) {
		t.Fatalf("unexpected non-normalized path header: %q", got)
	}
	if !strings.Contains(got, "# path="+pathAValue) || !strings.Contains(got, "# path="+pathBValue) {
		t.Fatalf("missing normalized path headers: %q", got)
	}
	if strings.Index(got, "# path="+pathAValue) > strings.Index(got, "# path="+pathBValue) {
		t.Fatalf("normalized path headers not sorted: %q", got)
	}
}

func TestRunShow_AllScopes_IgnoresPathOption(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	pathAValue := filepath.Join(base, "a")
	pathBValue := filepath.Join(base, "b")
	unrelatedPath := filepath.Join(base, "unrelated")

	pathA := opts
	pathA.path = pathAValue
	if err := runSet(pathA, []string{"A=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	pathB := opts
	pathB.path = pathBValue
	if err := runSet(pathB, []string{"B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	showOpts := opts
	showOpts.path = unrelatedPath
	out.Reset()
	if err := runShow(showOpts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "# path="+pathAValue) || !strings.Contains(got, "# path="+pathBValue) {
		t.Fatalf("expected all profile path scopes regardless of --path, got %q", got)
	}
}

func TestRunShow_AllScopes_RejectsRelativeStoredPaths(t *testing.T) {
	opts := setupUnlockedForSet(t)
	base := t.TempDir()
	relativePath := filepath.FromSlash("./rel/../scope")
	absPath := filepath.Join(base, "ok")

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	vd.Profiles[opts.profile][relativePath] = map[string]string{"A": "1"}
	vd.Profiles[opts.profile][absPath] = map[string]string{"B": "2"}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err = runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected relative stored path rejection")
	}
	if !strings.Contains(err.Error(), "stored path") || !strings.Contains(err.Error(), "is relative") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("expected no stderr warning output, got %q", errBuf.String())
	}
}

func TestRunShow_IgnoresPositionalArgs(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer

	if err := runSet(opts, []string{"A=repo-a"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"ignored-arg"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "A=") {
		t.Fatalf("expected regular show output, got %q", out.String())
	}
}

func TestRunShow_AllScopes_IgnoresPositionalArgs(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	pathAValue := filepath.Join(base, "a")
	pathBValue := filepath.Join(base, "b")

	pathA := opts
	pathA.path = pathAValue
	if err := runSet(pathA, []string{"A=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	pathB := opts
	pathB.path = pathBValue
	if err := runSet(pathB, []string{"B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--all-scopes", "ignored-arg"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "# path="+pathAValue) || !strings.Contains(got, "# path="+pathBValue) {
		t.Fatalf("expected all-scopes output with ignored positional arg, got %q", got)
	}
}

func TestRunShow_AllScopes_RejectsCollidingNormalizedStoredPaths(t *testing.T) {
	opts := setupUnlockedForSet(t)
	base := t.TempDir()
	pathAValue := filepath.Join(base, "a")
	rawPathCollidingWithA := base + string(filepath.Separator) + "z" + string(filepath.Separator) + ".." + string(filepath.Separator) + "a"

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	vd.Profiles[opts.profile][pathAValue] = map[string]string{"A": "1"}
	vd.Profiles[opts.profile][rawPathCollidingWithA] = map[string]string{"B": "2"}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected normalized-path collision rejection")
	}
	if !strings.Contains(err.Error(), "normalize to the same path") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", out.String())
	}
}

func TestRunShow_AllScopes_AllowsCaseVariantPathsAsDistinctScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	base := t.TempDir()
	pathLower := filepath.Join(base, "scope")
	pathUpper := filepath.Join(base, "SCOPE")

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	vd.Profiles[opts.profile][pathLower] = map[string]string{"A": "1"}
	vd.Profiles[opts.profile][pathUpper] = map[string]string{"B": "2"}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "# path="+pathLower) || !strings.Contains(got, "# path="+pathUpper) {
		t.Fatalf("expected both case-variant path scopes, got %q", got)
	}
}

func TestRunShow_AllScopes_AllowsCaseVariantNonExistentPathsAsDistinctScopes(t *testing.T) {
	opts := setupUnlockedForSet(t)
	base := t.TempDir()
	pathLower := filepath.Join(base, "scope")
	pathUpper := filepath.Join(base, "SCOPE")

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if vd.Profiles[opts.profile] == nil {
		vd.Profiles[opts.profile] = map[string]map[string]string{}
	}
	vd.Profiles[opts.profile][pathLower] = map[string]string{"A": "1"}
	vd.Profiles[opts.profile][pathUpper] = map[string]string{"B": "2"}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "# path="+pathLower) || !strings.Contains(got, "# path="+pathUpper) {
		t.Fatalf("expected both non-existent case-variant path scopes, got %q", got)
	}
}

func TestRunShow_AllScopes_UsesSelectedProfileOnly(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	base := t.TempDir()
	defaultPath := filepath.Join(base, "default")
	otherPath := filepath.Join(base, "other")

	defaultScope := opts
	defaultScope.path = defaultPath
	if err := runSet(defaultScope, []string{"DEFAULT_ONLY=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	otherProfile := opts
	otherProfile.profile = "other"
	otherProfile.path = otherPath
	if err := runSet(otherProfile, []string{"OTHER_ONLY=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "# profile=default") {
		t.Fatalf("missing selected profile header: %q", got)
	}
	if !strings.Contains(got, "# path="+defaultPath) {
		t.Fatalf("missing selected profile path scope: %q", got)
	}
	if strings.Contains(got, "# path="+otherPath) {
		t.Fatalf("unexpected path from another profile: %q", got)
	}
	if strings.Contains(got, "OTHER_ONLY") {
		t.Fatalf("unexpected key from another profile: %q", got)
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

func TestRunImport_FileInputPreferredOverNonTTYStdin(t *testing.T) {
	opts := setupUnlockedForSet(t)
	filePath := filepath.Join(t.TempDir(), "envrc.private")
	if err := os.WriteFile(filePath, []byte("export API_KEY='from-file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdinPayload := strings.NewReader("export API_KEY='from-stdin'\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runImport(opts, []string{"--yes", "--file", filePath}, stdinPayload, &out, &errBuf); err != nil {
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
}
