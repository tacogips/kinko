# Error Handling Patterns

Go's explicit error handling is a core language feature. This guide covers idiomatic patterns for creating, wrapping, and handling errors.

## Basic Error Handling

Always check and handle errors explicitly:

```go
// BAD: Ignoring errors
file, _ := os.Open("config.json")

// GOOD: Handle errors
file, err := os.Open("config.json")
if err != nil {
    return fmt.Errorf("failed to open config: %w", err)
}
defer file.Close()
```

## Error Wrapping (Go 1.13+)

Use `%w` verb to wrap errors with context while preserving the original error:

```go
func loadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("reading config file %s: %w", path, err)
    }

    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parsing config file %s: %w", path, err)
    }

    return &cfg, nil
}
```

### Error Chain Inspection

```go
// errors.Is checks if any error in the chain matches
if errors.Is(err, os.ErrNotExist) {
    // Handle file not found
}

// errors.As extracts a specific error type from the chain
var pathErr *os.PathError
if errors.As(err, &pathErr) {
    fmt.Printf("Path: %s, Operation: %s\n", pathErr.Path, pathErr.Op)
}
```

## Sentinel Errors

Define package-level error values for expected error conditions:

```go
package user

import "errors"

// Sentinel errors for user package
var (
    ErrNotFound      = errors.New("user not found")
    ErrAlreadyExists = errors.New("user already exists")
    ErrInvalidEmail  = errors.New("invalid email format")
)

func GetUser(id string) (*User, error) {
    user, ok := store[id]
    if !ok {
        return nil, ErrNotFound
    }
    return user, nil
}

// Caller code
user, err := user.GetUser(id)
if errors.Is(err, user.ErrNotFound) {
    // Handle not found case
}
```

## Custom Error Types

Create custom error types when you need to attach additional context:

```go
// ValidationError contains field-specific validation failures
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}

// Use in functions
func ValidateUser(u *User) error {
    if u.Email == "" {
        return &ValidationError{Field: "email", Message: "required"}
    }
    if !strings.Contains(u.Email, "@") {
        return &ValidationError{Field: "email", Message: "invalid format"}
    }
    return nil
}

// Extract in caller
var validationErr *ValidationError
if errors.As(err, &validationErr) {
    fmt.Printf("Field %s failed: %s\n", validationErr.Field, validationErr.Message)
}
```

### Multi-Field Validation Errors

```go
type ValidationErrors struct {
    Errors []ValidationError
}

func (e *ValidationErrors) Error() string {
    var msgs []string
    for _, err := range e.Errors {
        msgs = append(msgs, err.Error())
    }
    return strings.Join(msgs, "; ")
}

func (e *ValidationErrors) Add(field, message string) {
    e.Errors = append(e.Errors, ValidationError{Field: field, Message: message})
}

func (e *ValidationErrors) HasErrors() bool {
    return len(e.Errors) > 0
}

func ValidateUser(u *User) error {
    var errs ValidationErrors

    if u.Email == "" {
        errs.Add("email", "required")
    }
    if u.Name == "" {
        errs.Add("name", "required")
    }

    if errs.HasErrors() {
        return &errs
    }
    return nil
}
```

## Error Handling Patterns

### Early Return Pattern

```go
func processFile(path string) error {
    file, err := os.Open(path)
    if err != nil {
        return fmt.Errorf("opening file: %w", err)
    }
    defer file.Close()

    data, err := io.ReadAll(file)
    if err != nil {
        return fmt.Errorf("reading file: %w", err)
    }

    if err := validate(data); err != nil {
        return fmt.Errorf("validating data: %w", err)
    }

    return nil
}
```

### Error Handling with Cleanup

```go
func processWithCleanup() (err error) {
    resource, err := acquire()
    if err != nil {
        return fmt.Errorf("acquiring resource: %w", err)
    }

    // Use named return and defer for cleanup
    defer func() {
        if closeErr := resource.Close(); closeErr != nil {
            if err != nil {
                // Log cleanup error but return original
                log.Printf("cleanup error: %v", closeErr)
            } else {
                err = fmt.Errorf("closing resource: %w", closeErr)
            }
        }
    }()

    if err := process(resource); err != nil {
        return fmt.Errorf("processing: %w", err)
    }

    return nil
}
```

### Handling Multiple Operations

```go
func setupDatabase() error {
    db, err := sql.Open("postgres", connStr)
    if err != nil {
        return fmt.Errorf("connecting to database: %w", err)
    }

    if err := db.Ping(); err != nil {
        db.Close()
        return fmt.Errorf("pinging database: %w", err)
    }

    if err := runMigrations(db); err != nil {
        db.Close()
        return fmt.Errorf("running migrations: %w", err)
    }

    return nil
}
```

## Panic and Recover

Use `panic` only for truly unrecoverable situations (programmer errors):

```go
// Acceptable: Unrecoverable programmer error
func MustCompileRegex(pattern string) *regexp.Regexp {
    re, err := regexp.Compile(pattern)
    if err != nil {
        panic(fmt.Sprintf("invalid regex pattern: %s", pattern))
    }
    return re
}

// Using recover for graceful degradation
func safeHandler(w http.ResponseWriter, r *http.Request) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("panic recovered: %v", r)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
    }()

    handleRequest(w, r)
}
```

## Error Handling Best Practices

### DO: Add Context When Wrapping

```go
// BAD: No context
return err

// BAD: Redundant "error" or "failed"
return fmt.Errorf("error: %w", err)
return fmt.Errorf("failed with error: %w", err)

// GOOD: Descriptive context
return fmt.Errorf("parsing user %s: %w", userID, err)
return fmt.Errorf("connecting to database at %s: %w", host, err)
```

### DO: Use errors.Is and errors.As

```go
// BAD: String comparison
if err.Error() == "user not found" { ... }

// BAD: Direct type assertion (misses wrapped errors)
if _, ok := err.(*ValidationError); ok { ... }

// GOOD: errors.Is for sentinel errors
if errors.Is(err, ErrNotFound) { ... }

// GOOD: errors.As for error types
var valErr *ValidationError
if errors.As(err, &valErr) { ... }
```

### DON'T: Panic for Expected Errors

```go
// BAD: Panic for expected case
func GetUser(id string) *User {
    user, ok := store[id]
    if !ok {
        panic("user not found")  // Never panic for expected cases
    }
    return user
}

// GOOD: Return error
func GetUser(id string) (*User, error) {
    user, ok := store[id]
    if !ok {
        return nil, ErrNotFound
    }
    return user, nil
}
```

### DON'T: Log and Return

```go
// BAD: Double logging
func process() error {
    if err := doSomething(); err != nil {
        log.Printf("error: %v", err)  // Logged here
        return err                     // And logged by caller
    }
    return nil
}

// GOOD: Return with context, let caller decide logging
func process() error {
    if err := doSomething(); err != nil {
        return fmt.Errorf("processing: %w", err)
    }
    return nil
}
```

## HTTP Error Handling Example

```go
type APIError struct {
    Code    int    `json:"-"`
    Message string `json:"message"`
    Details string `json:"details,omitempty"`
}

func (e *APIError) Error() string {
    return e.Message
}

var (
    ErrBadRequest   = &APIError{Code: 400, Message: "bad request"}
    ErrUnauthorized = &APIError{Code: 401, Message: "unauthorized"}
    ErrNotFound     = &APIError{Code: 404, Message: "not found"}
    ErrInternal     = &APIError{Code: 500, Message: "internal server error"}
)

func handleError(w http.ResponseWriter, err error) {
    var apiErr *APIError
    if errors.As(err, &apiErr) {
        w.WriteHeader(apiErr.Code)
        json.NewEncoder(w).Encode(apiErr)
        return
    }

    // Log unexpected errors
    log.Printf("unexpected error: %v", err)
    w.WriteHeader(500)
    json.NewEncoder(w).Encode(ErrInternal)
}
```

## References

- [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors)
- [Error handling and Go](https://go.dev/blog/error-handling-and-go)
- [Don't just check errors, handle them gracefully](https://dave.cheney.net/2016/04/27/dont-just-check-errors-handle-them-gracefully)
