package kinko

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

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

func runShowWithPasswordForTest(t *testing.T, opts globalOptions, args []string, stdout, stderr *bytes.Buffer) error {
	t.Helper()
	return runShow(opts, args, strings.NewReader("pw\n"), stdout, stderr)
}

func TestPasswordVerificationInputForUsesTerminalSecretReader(t *testing.T) {
	stdin := strings.NewReader("pw\n")
	input := passwordVerificationInputFor(stdin, func(io.Reader) bool { return true })
	if !input.terminalSecret {
		t.Fatal("expected terminal secret reader mode")
	}
	if input.secretInput != stdin {
		t.Fatal("terminal mode must use original stdin for hidden password reading")
	}
	if input.confirmationInput != stdin {
		t.Fatal("terminal mode must leave later prompts on original stdin")
	}
}

func TestPasswordVerificationInputForBuffersNonTerminalInput(t *testing.T) {
	stdin := strings.NewReader("pw\ny\n")
	input := passwordVerificationInputFor(stdin, func(io.Reader) bool { return false })
	if input.terminalSecret {
		t.Fatal("expected buffered non-terminal reader mode")
	}
	reader, ok := input.secretInput.(*bufio.Reader)
	if !ok {
		t.Fatalf("expected buffered secret reader, got %T", input.secretInput)
	}
	if input.confirmationInput != reader {
		t.Fatal("non-terminal mode must reuse buffered reader for later prompts")
	}
	password, err := readSecretWithPromptBuffered(reader, &bytes.Buffer{}, "Password: ")
	if err != nil {
		t.Fatal(err)
	}
	if password != "pw" {
		t.Fatalf("password=%q want pw", password)
	}
	ok, err = confirmPrompt(input.confirmationInput, &bytes.Buffer{}, "Confirm? ")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected confirmation input to remain buffered after password read")
	}
}

func TestRunShow_AllScopes_RequiresPasswordBeforeOutput(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var setupOut bytes.Buffer
	if err := runSet(opts, []string{"--shared", "SECRET=shared"}, strings.NewReader(""), &setupOut); err != nil {
		t.Fatal(err)
	}

	pathScope := opts
	pathScope.path = filepath.Join(t.TempDir(), "scope")
	if err := runSet(pathScope, []string{"PATH_SECRET=repo"}, strings.NewReader(""), &setupOut); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		stdin       string
		wantErrText string
	}{
		{name: "wrong password", stdin: "wrong\n", wantErrText: "password verification failed"},
		{name: "missing password", stdin: "", wantErrText: "empty secret not allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var errBuf bytes.Buffer
			err := runShow(opts, []string{"--all-scopes"}, strings.NewReader(tc.stdin), &out, &errBuf)
			if err == nil {
				t.Fatal("expected password verification error")
			}
			if !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("expected no stdout on auth failure, got %q", out.String())
			}
			if !strings.Contains(errBuf.String(), "Re-enter password: ") {
				t.Fatalf("expected password prompt on stderr, got %q", errBuf.String())
			}
		})
	}
}

func TestRunShow_CurrentScopeDoesNotRequirePasswordPrompt(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := runSet(opts, []string{"A=repo-a"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runShow(opts, []string{}, strings.NewReader("wrong-password\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "A=") {
		t.Fatalf("expected current-scope show output, got %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("current-scope show should not prompt for password, got stderr %q", errBuf.String())
	}
}

func TestRunShow_AllScopesRevealVerifiesPasswordBeforeRedirectGuard(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var setupOut bytes.Buffer
	if err := runSet(opts, []string{"--shared", "S=shared"}, strings.NewReader(""), &setupOut); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runShow(opts, []string{"--all-scopes", "--reveal"}, strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification error")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("expected password verification before reveal guard, got %v", err)
	}
	if strings.Contains(err.Error(), "sensitive output blocked") {
		t.Fatalf("reveal guard ran before password verification: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", errBuf.String())
	}
}

func TestRunShow_AllScopes_MaskedAndSortedByPathAndKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	var errBuf bytes.Buffer
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", errBuf.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "# profile=default\n\n# shared\n") {
		t.Fatalf("missing profile/shared headers: %q", got)
	}
	if !strings.Contains(got, "A_SHARED=sh****-a") {
		t.Fatalf("expected masked A_SHARED in output: %q", got)
	}
	if !strings.Contains(got, "Z_SHARED=sh****-z") {
		t.Fatalf("expected masked Z_SHARED in output: %q", got)
	}
	if !strings.Contains(got, "# path="+pathAValue) || !strings.Contains(got, "# path="+pathBValue) {
		t.Fatalf("missing path headers: %q", got)
	}
	if strings.Index(got, "# path="+pathAValue) > strings.Index(got, "# path="+pathBValue) {
		t.Fatalf("paths not sorted: %q", got)
	}
	if !strings.Contains(got, "A=****") || !strings.Contains(got, "B=****") {
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes", "--reveal"}, &out, &bytes.Buffer{}); err != nil {
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
	err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes", "--reveal"}, &out, &errBuf)
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "# profile=default\n\n# shared\n") {
		t.Fatalf("missing profile/shared headers for empty profile output: %q", got)
	}
	if strings.Contains(got, "# path=") {
		t.Fatalf("unexpected path section for empty profile output: %q", got)
	}
	if errBuf.String() != "Re-enter password: " {
		t.Fatalf("expected password prompt only on stderr, got %q", errBuf.String())
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{}); err != nil {
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{}); err != nil {
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
	if err := runShowWithPasswordForTest(t, showOpts, []string{"--all-scopes"}, &out, &bytes.Buffer{}); err != nil {
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
	err = runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected relative stored path rejection")
	}
	if !strings.Contains(err.Error(), "stored path") || !strings.Contains(err.Error(), "is relative") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", out.String())
	}
	if errBuf.String() != "Re-enter password: " {
		t.Fatalf("expected password prompt only on stderr, got %q", errBuf.String())
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes", "ignored-arg"}, &out, &bytes.Buffer{}); err != nil {
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
	err = runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{})
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
	err = runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{})
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
	err = runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{})
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
	if err := runShowWithPasswordForTest(t, opts, []string{"--all-scopes"}, &out, &bytes.Buffer{}); err != nil {
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
