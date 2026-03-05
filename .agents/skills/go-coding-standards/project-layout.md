# Project Layout Conventions

Standard Go project structure following community conventions and the golang-standards/project-layout guidelines.

## Standard Go Project Layout

### Primary Directories

```
project/
  cmd/                  # Application entry points
    myapp/
      main.go           # Minimal - imports from internal/pkg
    worker/
      main.go
  internal/             # Private code (compiler-enforced)
    app/                # Application logic
    pkg/                # Internal shared libraries
  pkg/                  # Public libraries (external import allowed)
  api/                  # API definitions (OpenAPI, proto)
  web/                  # Web assets (HTML, CSS, JS)
  configs/              # Configuration templates
  scripts/              # Build and operational scripts
  build/                # CI/CD and packaging
  deployments/          # Deployment configs (k8s, docker-compose)
  test/                 # Integration tests and test data
  docs/                 # Documentation
  go.mod
  go.sum
```

### Directory Descriptions

**`/cmd`**
- Each subdirectory is a separate executable
- Keep `main.go` minimal - import and wire dependencies
- Directory name matches binary name

```go
// cmd/myapp/main.go
package main

import (
    "log"
    "myproject/internal/app"
)

func main() {
    if err := app.Run(); err != nil {
        log.Fatal(err)
    }
}
```

**`/internal`**
- Private application code
- Cannot be imported by external projects (compiler-enforced)
- Use for all application-specific logic

**`/pkg`**
- Library code safe for external import
- Only use when intentionally creating reusable libraries
- Not required for most projects

## Layered Architecture

When implementing Clean Architecture or Hexagonal Architecture, place all layers under `/internal/`:

```
internal/
  domain/               # Core business entities and rules
    model/              # Domain models
      user.go
      order.go
    repository/         # Repository interfaces (ports)
      user_repository.go

  usecase/              # Application business logic
    user_service.go
    order_service.go

  repository/           # Repository implementations (adapters)
    postgres/
      user_repository.go
    memory/
      user_repository.go

  handler/              # Presentation layer
    http/
      user_handler.go
      router.go
    grpc/
      user_server.go
```

### Layer Responsibilities

**Domain Layer** (`/internal/domain/`)
- Pure business logic and entities
- No dependencies on outer layers
- Repository interfaces (dependency inversion)

```go
// internal/domain/model/user.go
package model

type User struct {
    ID        string
    Email     string
    Name      string
    CreatedAt time.Time
}

// Business rule: validate email format
func (u *User) Validate() error {
    if !strings.Contains(u.Email, "@") {
        return ErrInvalidEmail
    }
    return nil
}
```

```go
// internal/domain/repository/user.go
package repository

import "myproject/internal/domain/model"

type UserRepository interface {
    Find(id string) (*model.User, error)
    Save(user *model.User) error
    Delete(id string) error
}
```

**Use Case Layer** (`/internal/usecase/`)
- Application-specific workflows
- Orchestrates domain entities
- Independent of delivery mechanism

```go
// internal/usecase/user_service.go
package usecase

type UserService struct {
    repo   repository.UserRepository
    hasher PasswordHasher
}

func NewUserService(repo repository.UserRepository, hasher PasswordHasher) *UserService {
    return &UserService{repo: repo, hasher: hasher}
}

func (s *UserService) CreateUser(email, password string) (*model.User, error) {
    // Business logic
    hash, err := s.hasher.Hash(password)
    if err != nil {
        return nil, fmt.Errorf("hashing password: %w", err)
    }

    user := &model.User{
        ID:    generateID(),
        Email: email,
    }

    if err := user.Validate(); err != nil {
        return nil, err
    }

    if err := s.repo.Save(user); err != nil {
        return nil, fmt.Errorf("saving user: %w", err)
    }

    return user, nil
}
```

**Repository Layer** (`/internal/repository/`)
- Concrete implementations of domain interfaces
- Database-specific code

```go
// internal/repository/postgres/user_repository.go
package postgres

type UserRepository struct {
    db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
    return &UserRepository{db: db}
}

func (r *UserRepository) Find(id string) (*model.User, error) {
    row := r.db.QueryRow("SELECT id, email, name FROM users WHERE id = $1", id)

    var user model.User
    if err := row.Scan(&user.ID, &user.Email, &user.Name); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, repository.ErrNotFound
        }
        return nil, fmt.Errorf("scanning user: %w", err)
    }

    return &user, nil
}
```

**Handler Layer** (`/internal/handler/`)
- HTTP/gRPC/CLI adapters
- Request validation and response formatting

```go
// internal/handler/http/user_handler.go
package http

type UserHandler struct {
    service *usecase.UserService
}

func NewUserHandler(service *usecase.UserService) *UserHandler {
    return &UserHandler{service: service}
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
    var req CreateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }

    user, err := h.service.CreateUser(req.Email, req.Password)
    if err != nil {
        handleError(w, err)
        return
    }

    json.NewEncoder(w).Encode(user)
}
```

### Dependency Flow

```
Handler -> UseCase -> Domain <- Repository
   |          |          ^          ^
 (HTTP)    (Logic)   (Interface) (Implementation)
```

Dependencies point inward. Domain has no outward dependencies.

## CLI/TUI Application Patterns

### Pattern 1: Flat Structure (Small CLI)

```
myapp/
  main.go           # Entry point with CLI setup
  config.go         # Configuration
  handler.go        # Command handlers
  go.mod
```

Best for: Single-purpose tools under 2000 lines.

### Pattern 2: Command-Based (Multiple Subcommands)

```
myapp/
  main.go           # Root command setup
  cmd/
    serve/
      serve.go      # 'serve' subcommand
    migrate/
      migrate.go    # 'migrate' subcommand
  internal/
    config/
    database/
  go.mod
```

Best for: CLI tools with multiple subcommands (like kubectl, docker).

### Pattern 3: Package-Based (Complex TUI)

```
myapp/
  cmd/
    myapp/
      main.go
  pkg/
    app/
      app.go
    gui/
      gui.go
      views/
    commands/
  go.mod
```

Best for: Full-featured TUI applications (like lazygit).

## Package Naming

### Conventions

| Rule | Example |
|------|---------|
| Short, lowercase | `user`, `http`, `json` |
| Singular | `user` not `users` |
| No underscores | `httputil` not `http_util` |
| Avoid generic names | `util`, `common`, `misc` |
| Match directory | `package user` in `/user/` |

### Avoid Stutter

```go
// BAD: Stutter
package user
type UserService struct{} // user.UserService

// GOOD: Clean
package user
type Service struct{} // user.Service
```

## Module Management

### Essential Commands

```bash
go mod init github.com/org/project  # Initialize module
go mod tidy                          # Sync dependencies
go mod download                      # Download dependencies
go mod verify                        # Verify checksums
go get package@version               # Add/update dependency
```

### Workflow

1. Write code with new imports
2. Run `go mod tidy`
3. Run `go build` and `go test`
4. Commit `go.mod` and `go.sum`

## Anti-Patterns to Avoid

```
// BAD: /src directory (Java-style)
project/
  src/
    main.go

// BAD: Deep nesting
project/
  internal/
    modules/
      core/
        services/
          user/
            service.go  // Too deep!

// BAD: Everything in main package
project/
  main.go          // 5000 lines of code

// BAD: Circular dependencies
// package a imports package b
// package b imports package a

// GOOD: Flat, focused packages
project/
  cmd/myapp/main.go
  internal/
    user/
    order/
    config/
```

## References

- [Standard Go Project Layout](https://github.com/golang-standards/project-layout)
- [Organizing Go Code](https://go.dev/blog/organizing-go-code)
- [Package Names](https://go.dev/blog/package-names)
- [Go Modules Reference](https://go.dev/ref/mod)
