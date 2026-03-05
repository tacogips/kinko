---
name: supply-chain-secure-publish
description: Use when publishing Go modules or managing module repositories. Provides supply chain attack countermeasures including version tagging security, repository access control, and CI/CD publishing pipelines. Adapted from Shai-Hulud npm attack lessons.
allowed-tools: Read, Write, Edit, Bash, Grep, Glob
user-invocable: true
---

# Supply Chain Secure Publish (Go Modules)

This skill provides guidelines for securely publishing and maintaining Go modules, adapted from the Shai-Hulud supply chain attacks. Go module publishing differs fundamentally from npm -- modules are published via git tags, not a registry upload -- but similar risks exist around repository compromise.

## When to Apply

Apply these guidelines when:
- Tagging and releasing new module versions
- Managing repository access for published modules
- Setting up CI/CD release pipelines
- Auditing existing module publishing workflows

## Go vs npm Publishing Model

| Aspect | npm | Go Modules |
|--------|-----|------------|
| Publish mechanism | `npm publish` to registry | `git tag` + `git push` |
| Registry | npmjs.com (mutable) | Module proxy caches from VCS (immutable) |
| Authentication | npm token | Git/GitHub credentials |
| Versioning | package.json version field | Git tags (semver) |
| Retraction | `npm unpublish` | `retract` directive in go.mod |

### Key Implication

In Go, **compromising the git repository IS compromising the module**. There is no separate publishing step that can be independently secured.

## Repository Access Security

### GitHub Account Protection

Since Go modules are published via git, protecting the repository IS protecting the module:

1. **Require 2FA** for all repository collaborators
2. **Use fine-grained PATs** (never classic PATs)
3. **Enable branch protection** on the default branch:
   - Require pull request reviews
   - Require signed commits
   - Restrict who can push
   - Require status checks to pass

### Fine-Grained PAT Scopes

```
# For CI/CD that only reads code:
Contents: read
Metadata: read

# For CI/CD that creates releases:
Contents: write
Metadata: read

# NEVER grant: Administration, Actions: write
# (Shai-Hulud used these to register runners and inject workflows)
```

### SSH Key Management

```bash
# Use deploy keys instead of user PATs for CI
# Deploy keys are scoped to a single repository

# Generate a deploy key
ssh-keygen -t ed25519 -f deploy_key -C "ci-deploy@project"

# Add as deploy key in GitHub repository settings
# Mark as read-only unless push is needed
```

## Secure Version Tagging

### Signed Tags (Mandatory for Published Modules)

```bash
# Configure git to sign tags
git config --global tag.gpgSign true

# Create a signed release tag
git tag -s v1.2.3 -m "Release v1.2.3"
git push origin v1.2.3

# Verify a signed tag
git tag -v v1.2.3
```

### Version Tagging Rules

1. **Always use semver**: `v1.2.3`, not `v1.2` or `release-1.2.3`
2. **Tag from the default branch**: Never tag from feature branches
3. **Verify the commit**: Ensure the tag points to the intended commit
4. **Sign tags**: Use GPG/SSH signing for verification

### Retraction (Go's Alternative to Unpublish)

If a compromised version is released:

```go
// go.mod - retract compromised versions
module example.com/mymodule

go 1.21

retract (
    v1.2.3 // Contains compromised dependency
    [v1.3.0, v1.3.2] // Range of affected versions
)
```

```bash
# Publish retraction by tagging a new version
git add go.mod
git commit -m "retract: v1.2.3 compromised dependency"
git tag v1.2.4
git push origin v1.2.4
```

## CI/CD Release Pipeline

### Secure Release Workflow

```yaml
name: Release
on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write  # Needed for GitHub release creation

jobs:
  release:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@<SHA>
        with:
          persist-credentials: false
          fetch-depth: 0  # Full history for changelog

      - uses: actions/setup-go@<SHA>
        with:
          go-version-file: go.mod

      # Verify before release
      - name: Verify modules
        run: go mod verify

      - name: Vulnerability check
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...

      - name: Test
        run: go test ./...

      - name: Build
        run: go build ./...

      # Create GitHub release (goreleaser recommended)
      - uses: goreleaser/goreleaser-action@<SHA>
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

### Pre-Release Checklist

- [ ] All tests pass
- [ ] `govulncheck` reports no vulnerabilities
- [ ] `go mod verify` passes
- [ ] `go mod tidy` produces no changes
- [ ] No `go:generate` directives pointing to external URLs
- [ ] No `replace` directives in go.mod (for public releases)
- [ ] CHANGELOG updated
- [ ] Tag is signed

## Module Proxy and Sumdb Considerations

### Immutability Guarantee

Once a module version is cached by the Go module proxy (proxy.golang.org), it cannot be modified. This prevents post-compromise modification of existing versions (unlike npm where a version can be unpublished and republished).

### Retraction vs Deletion

Go does not support deleting published module versions. Instead:
- Use `retract` directive in go.mod
- The retracted version remains available but `go get` warns about it
- This is more transparent than npm's unpublish

## Emergency Response: Module Compromise

1. **Immediately retract** the compromised version in go.mod
2. **Tag and push** a new version containing the retraction
3. **Rotate ALL credentials**: GitHub PATs, SSH keys, deploy keys
4. **Revoke GitHub App authorizations** if applicable
5. **Review recent commits** for unauthorized changes
6. **Check for unauthorized GitHub Actions** workflows
7. **Notify users** via GitHub Advisory and module documentation
8. **Report** to the Go security team

## References

- [Go Module Reference - Publishing](https://go.dev/ref/mod#publishing-modules)
- [Go Module Reference - Retraction](https://go.dev/ref/mod#go-mod-file-retract)
- [GitHub Fine-Grained PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
- [goreleaser - Release Automation](https://goreleaser.com/)
