package kinko

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoFileLineLimits(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".riela" || entry.Name() == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		lines := 0
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines++
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		if lines > 1000 {
			return fmt.Errorf("%s has %d lines; Go files must not exceed 1000", filepath.ToSlash(path), lines)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBuildHasNoBWSSDKDependency(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if bytes.Contains(contents, []byte("github.com/bitwarden/sdk-go")) {
			t.Fatalf("default module graph contains the unapproved BWS SDK in %s", name)
		}
	}
	command := exec.Command("go", "list", "-deps", "./...")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect default dependency graph: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if strings.HasPrefix(dependency, "github.com/bitwarden/sdk-go") {
			t.Fatalf("default build imports unapproved BWS SDK package %s", dependency)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository policy test location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
