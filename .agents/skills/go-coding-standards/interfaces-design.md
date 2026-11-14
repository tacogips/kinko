# Interface Design

Go's interfaces enable loose coupling and testability through implicit implementation. This guide covers idiomatic interface patterns.

## Interface Fundamentals

### Implicit Implementation

Go interfaces are satisfied implicitly - no `implements` keyword needed:

```go
type Writer interface {
    Write(p []byte) (n int, err error)
}

// FileWriter implements Writer without explicit declaration
type FileWriter struct {
    file *os.File
}

func (f *FileWriter) Write(p []byte) (int, error) {
    return f.file.Write(p)
}

// Can be used anywhere Writer is expected
var w Writer = &FileWriter{file: f}
```

### Accept Interfaces, Return Structs

```go
// GOOD: Accept interface
func ProcessData(r io.Reader) error {
    data, err := io.ReadAll(r)
    // ...
}

// GOOD: Return concrete type
func NewUserService(db *sql.DB) *UserService {
    return &UserService{db: db}
}

// BAD: Return interface (hides implementation details)
func NewUserService(db *sql.DB) UserServiceInterface {
    return &UserService{db: db}
}
```

## Small Interfaces

### The "-er" Convention

Single-method interfaces use -er suffix:

```go
type Reader interface {
    Read(p []byte) (n int, err error)
}

type Writer interface {
    Write(p []byte) (n int, err error)
}

type Closer interface {
    Close() error
}

type Stringer interface {
    String() string
}
```

### Compose from Small Interfaces

```go
// Composed from smaller interfaces
type ReadWriter interface {
    Reader
    Writer
}

type ReadWriteCloser interface {
    Reader
    Writer
    Closer
}

// Custom composition
type Repository interface {
    Reader
    Writer
    Deleter
}
```

## Interface Segregation

### Define at Point of Use

Define interfaces where they're used, not where they're implemented:

```go
// In the consumer package
package orderservice

// Only the methods this package needs
type UserGetter interface {
    GetUser(id string) (*User, error)
}

type OrderService struct {
    users UserGetter  // Not the full UserService
}

// The UserService can be much larger
package userservice

type UserService struct {
    db *sql.DB
}

func (s *UserService) GetUser(id string) (*User, error) { ... }
func (s *UserService) CreateUser(u *User) error { ... }
func (s *UserService) DeleteUser(id string) error { ... }
func (s *UserService) ListUsers() ([]*User, error) { ... }
```

### Keep Interfaces Small

```go
// BAD: Large interface forces implementing unused methods
type Repository interface {
    Find(id string) (*Entity, error)
    FindAll() ([]*Entity, error)
    FindByName(name string) (*Entity, error)
    FindByEmail(email string) (*Entity, error)
    Create(e *Entity) error
    Update(e *Entity) error
    Delete(id string) error
    Count() (int, error)
    Exists(id string) (bool, error)
}

// GOOD: Split into focused interfaces
type Finder interface {
    Find(id string) (*Entity, error)
}

type Lister interface {
    FindAll() ([]*Entity, error)
}

type Creator interface {
    Create(e *Entity) error
}

type Updater interface {
    Update(e *Entity) error
}

type Deleter interface {
    Delete(id string) error
}

// Compose as needed
type ReadRepository interface {
    Finder
    Lister
}

type WriteRepository interface {
    Creator
    Updater
    Deleter
}

type Repository interface {
    ReadRepository
    WriteRepository
}
```

## Dependency Injection

### Constructor Injection

```go
type UserService struct {
    repo   UserRepository
    hasher PasswordHasher
    mailer Mailer
}

func NewUserService(
    repo UserRepository,
    hasher PasswordHasher,
    mailer Mailer,
) *UserService {
    return &UserService{
        repo:   repo,
        hasher: hasher,
        mailer: mailer,
    }
}
```

### Functional Options Pattern

For optional dependencies or configuration:

```go
type Server struct {
    addr    string
    timeout time.Duration
    logger  Logger
}

type ServerOption func(*Server)

func WithTimeout(d time.Duration) ServerOption {
    return func(s *Server) {
        s.timeout = d
    }
}

func WithLogger(l Logger) ServerOption {
    return func(s *Server) {
        s.logger = l
    }
}

func NewServer(addr string, opts ...ServerOption) *Server {
    s := &Server{
        addr:    addr,
        timeout: 30 * time.Second,  // Default
        logger:  defaultLogger,      // Default
    }

    for _, opt := range opts {
        opt(s)
    }

    return s
}

// Usage
server := NewServer(":8080",
    WithTimeout(60*time.Second),
    WithLogger(customLogger),
)
```

## Testing with Interfaces

### Mock Implementations

```go
// Interface in production code
type UserRepository interface {
    Find(id string) (*User, error)
    Save(user *User) error
}

// Mock for testing
type MockUserRepository struct {
    FindFunc func(id string) (*User, error)
    SaveFunc func(user *User) error
}

func (m *MockUserRepository) Find(id string) (*User, error) {
    return m.FindFunc(id)
}

func (m *MockUserRepository) Save(user *User) error {
    return m.SaveFunc(user)
}

// Test
func TestUserService_CreateUser(t *testing.T) {
    mockRepo := &MockUserRepository{
        SaveFunc: func(user *User) error {
            return nil
        },
    }

    service := NewUserService(mockRepo)
    err := service.CreateUser("test@example.com")

    if err != nil {
        t.Errorf("unexpected error: %v", err)
    }
}
```

### Table-Driven Tests with Interfaces

```go
func TestUserService_Find(t *testing.T) {
    tests := []struct {
        name      string
        setupRepo func() UserRepository
        userID    string
        wantErr   bool
    }{
        {
            name: "user found",
            setupRepo: func() UserRepository {
                return &MockUserRepository{
                    FindFunc: func(id string) (*User, error) {
                        return &User{ID: id, Name: "Test"}, nil
                    },
                }
            },
            userID:  "123",
            wantErr: false,
        },
        {
            name: "user not found",
            setupRepo: func() UserRepository {
                return &MockUserRepository{
                    FindFunc: func(id string) (*User, error) {
                        return nil, ErrNotFound
                    },
                }
            },
            userID:  "999",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            service := NewUserService(tt.setupRepo())
            _, err := service.Find(tt.userID)

            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

## Type Assertions and Type Switches

### Type Assertion

```go
// Single type assertion
writer, ok := w.(io.Writer)
if !ok {
    return errors.New("not a writer")
}

// Without ok - panics if assertion fails
writer := w.(io.Writer)  // Use only when certain
```

### Type Switch

```go
func describe(i interface{}) string {
    switch v := i.(type) {
    case nil:
        return "nil"
    case int:
        return fmt.Sprintf("int: %d", v)
    case string:
        return fmt.Sprintf("string: %q", v)
    case io.Reader:
        return "implements io.Reader"
    default:
        return fmt.Sprintf("unknown type: %T", v)
    }
}
```

## Empty Interface

Use `interface{}` (or `any` in Go 1.18+) sparingly:

```go
// AVOID: Loses type safety
func Process(data interface{}) { ... }

// BETTER: Use generics (Go 1.18+)
func Process[T any](data T) { ... }

// BEST: Use specific interface
func Process(data Processable) { ... }
```

## Interface Best Practices

### DO: Keep Interfaces Small

```go
// GOOD: Focused, single-purpose
type Validator interface {
    Validate() error
}

type Saver interface {
    Save() error
}
```

### DO: Define at Consumer

```go
// In package that USES the interface
package handler

type UserFinder interface {
    Find(id string) (*User, error)
}
```

### DON'T: Premature Interface

```go
// BAD: Interface with single implementation
type UserServiceInterface interface {
    GetUser(id string) (*User, error)
    CreateUser(u *User) error
}

// Only implementation
type UserService struct { ... }

// Just use the struct directly until you need abstraction
```

### DON'T: Interface Pollution

```go
// BAD: Everything is an interface
type StringerInterface interface {
    String() string
}

type ErrorInterface interface {
    Error() string
}

// GOOD: Use standard library interfaces
var _ fmt.Stringer = (*MyType)(nil)
var _ error = (*MyError)(nil)
```

## Compile-Time Interface Checks

```go
// Ensure type implements interface at compile time
var _ io.Reader = (*MyReader)(nil)
var _ io.Writer = (*MyWriter)(nil)
var _ fmt.Stringer = (*MyType)(nil)

// Useful for documentation and catching errors early
type MyReader struct{}

func (r *MyReader) Read(p []byte) (int, error) {
    // Implementation
}
```

## References

- [Effective Go - Interfaces](https://go.dev/doc/effective_go#interfaces)
- [Go Proverbs](https://go-proverbs.github.io/)
- [Interface Segregation in Go](https://dave.cheney.net/2016/08/20/solid-go-design)
- [Accept Interfaces, Return Structs](https://bryanftan.medium.com/accept-interfaces-return-structs-in-go-d4cab29a301b)
