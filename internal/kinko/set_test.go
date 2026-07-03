package kinko

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupUnlockedForSet(t *testing.T) globalOptions {
	t.Helper()
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	return globalOptions{
		dataDir: dataDir,
		profile: defaultProfile,
		path:    filepath.Clean("/tmp/project"),
	}
}

func valueAtScope(t *testing.T, opts globalOptions, key string) string {
	t.Helper()
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	return vd.Profiles[opts.profile][opts.path][key]
}

func valueAtShared(t *testing.T, opts globalOptions, key string) string {
	t.Helper()
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	return vd.Shared[key]
}

func assertSubstringOrder(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	if firstIndex < 0 {
		t.Fatalf("missing %q in %q", first, text)
	}
	secondIndex := strings.Index(text, second)
	if secondIndex < 0 {
		t.Fatalf("missing %q in %q", second, text)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q in %q", first, second, text)
	}
}

func TestRunSet_AssignmentArg(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=12312313"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "12312313" {
		t.Fatalf("A=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_MultiAssignmentsArg(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=111", "B=222"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "111" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "222" {
		t.Fatalf("B=%q", got)
	}
	if out.String() != "A,B set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_AssignmentsFromStdin(t *testing.T) {
	opts := setupUnlockedForSet(t)
	in := strings.NewReader("A=aaaa\nB=bbbb\n\n")
	var out bytes.Buffer
	if err := runSet(opts, nil, in, &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "aaaa" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "bbbb" {
		t.Fatalf("B=%q", got)
	}
	if out.String() != "A,B set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_RejectsValueFlagAndSuggestsSetKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	err := runSet(opts, []string{"--value", "xyz", "A"}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "use set-key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSetKey_ValueFlagWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"--value", "xyz", "A"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "xyz" {
		t.Fatalf("A=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSetKey_ExplicitEmptyValueFlagWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"A", "--value="}, strings.NewReader("ignored\n"), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "" {
		t.Fatalf("A=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSetKey_ValueFlagPreservesWhitespace(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"A", "--value", "  spaced  "}, strings.NewReader("ignored\n"), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "  spaced  " {
		t.Fatalf("A=%q", got)
	}
}

func TestRunSetKey_StdinValueTrimsWhitespace(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"A"}, strings.NewReader("  spaced  \n"), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "spaced" {
		t.Fatalf("A=%q", got)
	}
}

func TestRunSetKey_KeyFirstValueFlagWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"A", "--value", "xyz"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtScope(t, opts, "A"); got != "xyz" {
		t.Fatalf("A=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_AssignmentArgShared(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=shared-value"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "A"); got != "shared-value" {
		t.Fatalf("A(shared)=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_SharedFlagAfterAssignmentWorks(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=shared-value", "--shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "A"); got != "shared-value" {
		t.Fatalf("A(shared)=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunSet_SharedDoesNotCreateRepoScope(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=shared-value"}, strings.NewReader(""), &out); err != nil {
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
	if _, ok := vd.Profiles[opts.profile][opts.path]; ok {
		t.Fatalf("unexpected repo scope created for shared write: profile=%q path=%q", opts.profile, opts.path)
	}
}

func TestRunSetKey_ValueFlagWorksShared(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSetKey(opts, []string{"--shared", "--value", "xyz", "A"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := valueAtShared(t, opts, "A"); got != "xyz" {
		t.Fatalf("A(shared)=%q", got)
	}
	if out.String() != "A set\n" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunDelete_SharedKey(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "SHARED_KEY=shared"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"REPO_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--shared", "--yes", "SHARED_KEY"}, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "deleted\n" {
		t.Fatalf("out=%q", out.String())
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vd.Shared["SHARED_KEY"]; ok {
		t.Fatal("expected shared key to be deleted")
	}
}

func TestRunDelete_SharedAll(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"REPO_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--shared", "--all", "--yes"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "deleted all\n" {
		t.Fatalf("out=%q", out.String())
	}
	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	if len(vd.Shared) != 0 {
		t.Fatalf("expected shared scope empty, got: %#v", vd.Shared)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestRunDelete_AllWrongPasswordLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runDelete(opts, []string{"--all", "--yes"}, strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification failure")
	}
	if code := ExitCode(err); code != exitCodeAuthFailed {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeAuthFailed)
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", gotErr)
	}
	if strings.Contains(gotErr, "Delete target keys") || strings.Contains(gotErr, "- A") || strings.Contains(gotErr, "- B") {
		t.Fatalf("auth failure must not list target keys, got stderr %q", gotErr)
	}
	if got := valueAtScope(t, opts, "A"); got != "1" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "2" {
		t.Fatalf("B=%q", got)
	}
}

func TestRunDelete_MutationLockConflictExitCode(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	out.Reset()
	err = runDelete(opts, []string{"--yes", "A"}, strings.NewReader(""), &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected lock conflict")
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("ExitCode(err)=%d want %d", code, exitCodeLockConflict)
	}
}

func TestRunDelete_AllDeclineSkipsPasswordPromptAndLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--all"}, strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "aborted\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("declined delete-all must not prompt for password, got stderr %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- B\n") {
		t.Fatalf("expected sorted delete target keys in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete all 2 keys in profile=") {
		t.Fatalf("missing delete-all prompt in stderr: %q", gotErr)
	}
	if got := valueAtScope(t, opts, "A"); got != "1" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "2" {
		t.Fatalf("B=%q", got)
	}
}

func TestRunDelete_AllWrongPasswordAfterConfirmationLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runDelete(opts, []string{"--all"}, strings.NewReader("y\nwrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	assertSubstringOrder(t, gotErr, "Delete all 2 keys in profile=", "Re-enter password: ")
	if got := valueAtScope(t, opts, "A"); got != "1" {
		t.Fatalf("A=%q", got)
	}
	if got := valueAtScope(t, opts, "B"); got != "2" {
		t.Fatalf("B=%q", got)
	}
}

func TestRunDelete_SharedAllWrongPasswordLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"REPO_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runDelete(opts, []string{"--shared", "--all", "--yes"}, strings.NewReader("wrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt on stderr, got %q", gotErr)
	}
	if strings.Contains(gotErr, "Delete target keys") || strings.Contains(gotErr, "- A") || strings.Contains(gotErr, "- B") {
		t.Fatalf("auth failure must not list shared target keys, got stderr %q", gotErr)
	}
	if got := valueAtShared(t, opts, "A"); got != "1" {
		t.Fatalf("A(shared)=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "2" {
		t.Fatalf("B(shared)=%q", got)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestRunDelete_SharedAllDeclineSkipsPasswordPromptAndLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"REPO_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--shared", "--all"}, strings.NewReader("n\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "aborted\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("declined shared delete-all must not prompt for password, got stderr %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- B\n") {
		t.Fatalf("expected sorted shared delete target keys in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete all 2 keys in shared scope? [y/N]: ") {
		t.Fatalf("missing shared delete-all prompt in stderr: %q", gotErr)
	}
	if got := valueAtShared(t, opts, "A"); got != "1" {
		t.Fatalf("A(shared)=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "2" {
		t.Fatalf("B(shared)=%q", got)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestRunDelete_SharedAllWrongPasswordAfterConfirmationLeavesDataUnchanged(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "A=1", "B=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"REPO_KEY=repo"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runDelete(opts, []string{"--shared", "--all"}, strings.NewReader("y\nwrong\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected password verification failure")
	}
	if !strings.Contains(err.Error(), "password verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on auth failure, got %q", out.String())
	}
	gotErr := errBuf.String()
	assertSubstringOrder(t, gotErr, "Delete all 2 keys in shared scope? [y/N]: ", "Re-enter password: ")
	if got := valueAtShared(t, opts, "A"); got != "1" {
		t.Fatalf("A(shared)=%q", got)
	}
	if got := valueAtShared(t, opts, "B"); got != "2" {
		t.Fatalf("B(shared)=%q", got)
	}
	if got := valueAtScope(t, opts, "REPO_KEY"); got != "repo" {
		t.Fatalf("REPO_KEY=%q", got)
	}
}

func TestRunDelete_AllShowsTargetKeysBeforeConfirm(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"Z=1", "A=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--all"}, strings.NewReader("y\npw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "deleted all\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- Z\n") {
		t.Fatalf("expected sorted delete target keys in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete all 2 keys in profile=") {
		t.Fatalf("missing delete-all prompt in stderr: %q", gotErr)
	}
	assertSubstringOrder(t, gotErr, "Delete all 2 keys in profile=", "Re-enter password: ")
}

func TestRunDelete_SharedAllShowsTargetKeysBeforeConfirm(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "B=1", "A=2"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	if err := runDelete(opts, []string{"--shared", "--all"}, strings.NewReader("y\npw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if out.String() != "deleted all\n" {
		t.Fatalf("out=%q", out.String())
	}
	gotErr := errBuf.String()
	if !strings.Contains(gotErr, "Re-enter password: ") {
		t.Fatalf("expected password prompt in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete target keys:\n- A\n- B\n") {
		t.Fatalf("expected sorted shared delete target keys in stderr, got %q", gotErr)
	}
	if !strings.Contains(gotErr, "Delete all 2 keys in shared scope? [y/N]: ") {
		t.Fatalf("missing shared delete-all prompt in stderr: %q", gotErr)
	}
	assertSubstringOrder(t, gotErr, "Delete all 2 keys in shared scope? [y/N]: ", "Re-enter password: ")
}

func TestRunDelete_SharedKeyWithoutSharedFlagReturnsHint(t *testing.T) {
	opts := setupUnlockedForSet(t)
	var out bytes.Buffer
	if err := runSet(opts, []string{"--shared", "ONLY_SHARED=1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	var errBuf bytes.Buffer
	err := runDelete(opts, []string{"--yes", "ONLY_SHARED"}, strings.NewReader(""), &out, &errBuf)
	if err == nil {
		t.Fatal("expected delete to fail without --shared")
	}
	if !strings.Contains(err.Error(), "use --shared") {
		t.Fatalf("err=%v", err)
	}
}
