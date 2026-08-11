---
name: go-coding-standards
description: Use when writing, reviewing, or refactoring Go code. Provides error handling patterns, project layout, concurrency, and interface design guidelines.
allowed-tools: Read, Grep, Glob
---

# Go Coding Standards

This skill provides modern Go coding guidelines and best practices for this project.

## When to Apply

Apply these standards when:
- Writing new Go code
- Reviewing or refactoring existing Go code
- Designing package APIs and interfaces
- Implementing error handling strategies

## Core Principles

1. **Simplicity Over Cleverness** - Clear, readable code beats clever abstractions
2. **Composition Over Inheritance** - Use interfaces and embedding
3. **Explicit Over Implicit** - Make behavior obvious, avoid magic
4. **Fail Fast** - Return errors early, handle them explicitly

## Source File Size

- **Hard limit**: No Go source file (`*.go`, including `*_test.go`) should stay above **1000 lines**. If a file is at or past that size, **split it** in the same change set or as a focused follow-up.
- **How to split**: Prefer cohesive package-internal boundaries such as command handlers, adapters, domain services, helpers, fixtures, or focused test files. If external callers depend on an API, preserve the public package surface with small wrapper functions, type aliases, or moved code inside the same package.
- **Agents**: When editing or reviewing code, if a touched file is **1000+ lines**, treat splitting as **in scope** for the task unless the user explicitly excludes it.

## Quick Reference

### Must-Use Patterns

| Pattern | Use Case |
|---------|----------|
| Error wrapping | `fmt.Errorf("context: %w", err)` for error chains |
| Table-driven tests | Multiple test cases in structured format |
| Functional options | Configurable constructors with defaults |
| Interface segregation | Small, focused interfaces (1-3 methods) |
| Context propagation | Pass `context.Context` as first parameter |

### Must-Avoid Anti-Patterns

| Anti-Pattern | Alternative |
|--------------|-------------|
| Ignoring errors | Always handle: `if err != nil { return err }` |
| Panic for expected failures | Return `error` type |
| Package-level state | Dependency injection |
| Large interfaces | Small, composable interfaces |
| Deep package nesting | Flat, focused packages |
| `init()` functions | Explicit initialization |

## Detailed Guidelines

For comprehensive guidance, see:
- [Error Handling Patterns](./error-handling.md) - Error wrapping, sentinel errors, custom types
- [Project Layout Conventions](./project-layout.md) - Standard layout, layered architecture
- [Concurrency Patterns](./concurrency.md) - Goroutines, channels, sync primitives
- [Interface Design](./interfaces-design.md) - Composition, small interfaces, mocking
- [Security Guidelines](./security.md) - Credential protection, path sanitization, sensitive data handling

## Code Style

### Naming Conventions

| Item | Convention | Example |
|------|------------|---------|
| Packages | short, lowercase, singular | `user`, `http`, `json` |
| Exported | PascalCase | `NewServer`, `UserService` |
| Unexported | camelCase | `parseConfig`, `handleRequest` |
| Interfaces | -er suffix for single method | `Reader`, `Writer`, `Closer` |
| Acronyms | consistent case | `HTTPServer`, `xmlParser` |

### Documentation

```go
// Package user provides user management functionality.
package user

// User represents a registered user in the system.
// It contains the user's identity and profile information.
type User struct {
    ID    string
    Email string
    Name  string
}

// NewUser creates a new User with the given email.
// Returns an error if the email format is invalid.
func NewUser(email string) (*User, error) {
    // ...
}
```

## Tooling

Always run these before committing:

```bash
go fmt ./...           # Format code
go vet ./...           # Static analysis
go test ./...          # Run tests
go mod tidy            # Sync dependencies
```

Consider adding:
- `golangci-lint` - Comprehensive linter
- `staticcheck` - Advanced static analysis

## References

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Standard Go Project Layout](https://github.com/golang-standards/project-layout)
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
