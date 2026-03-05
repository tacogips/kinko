# Concurrency Patterns

Go's concurrency model is built on goroutines and channels. This guide covers idiomatic patterns for safe, efficient concurrent programming.

## Goroutines

### Basic Goroutine Usage

```go
// Launch goroutine
go processItem(item)

// With anonymous function
go func() {
    result := heavyComputation()
    resultChan <- result
}()

// IMPORTANT: Don't capture loop variables by reference
// BAD: All goroutines see the same value
for _, item := range items {
    go process(item)  // item may change before goroutine runs
}

// GOOD: Pass as parameter
for _, item := range items {
    go process(item)  // item is copied to goroutine
}

// Also GOOD: Create new variable in loop
for _, item := range items {
    item := item  // Shadow with new variable
    go func() {
        process(item)
    }()
}
```

### Waiting for Goroutines

```go
import "sync"

func processAll(items []Item) {
    var wg sync.WaitGroup

    for _, item := range items {
        wg.Add(1)
        go func(it Item) {
            defer wg.Done()
            process(it)
        }(item)
    }

    wg.Wait()  // Block until all goroutines complete
}
```

## Channels

### Channel Types

```go
ch := make(chan int)       // Unbuffered channel
ch := make(chan int, 10)   // Buffered channel (capacity 10)

var sendOnly chan<- int    // Send-only channel
var recvOnly <-chan int    // Receive-only channel
```

### Basic Channel Operations

```go
// Send
ch <- value

// Receive
value := <-ch
value, ok := <-ch  // ok is false if channel is closed

// Close (only sender should close)
close(ch)

// Range over channel (until closed)
for value := range ch {
    process(value)
}
```

### Channel Patterns

**Fan-Out: Distribute work to multiple workers**

```go
func fanOut(input <-chan Job, workers int) []<-chan Result {
    outputs := make([]<-chan Result, workers)

    for i := 0; i < workers; i++ {
        outputs[i] = worker(input)
    }

    return outputs
}

func worker(jobs <-chan Job) <-chan Result {
    results := make(chan Result)
    go func() {
        defer close(results)
        for job := range jobs {
            results <- process(job)
        }
    }()
    return results
}
```

**Fan-In: Merge multiple channels into one**

```go
func fanIn(inputs ...<-chan Result) <-chan Result {
    var wg sync.WaitGroup
    merged := make(chan Result)

    output := func(ch <-chan Result) {
        defer wg.Done()
        for result := range ch {
            merged <- result
        }
    }

    wg.Add(len(inputs))
    for _, ch := range inputs {
        go output(ch)
    }

    go func() {
        wg.Wait()
        close(merged)
    }()

    return merged
}
```

**Pipeline: Chain processing stages**

```go
func pipeline() {
    // Stage 1: Generate
    nums := generate(1, 2, 3, 4, 5)

    // Stage 2: Square
    squared := square(nums)

    // Stage 3: Print
    for n := range squared {
        fmt.Println(n)
    }
}

func generate(nums ...int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        for _, n := range nums {
            out <- n
        }
    }()
    return out
}

func square(in <-chan int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        for n := range in {
            out <- n * n
        }
    }()
    return out
}
```

## Select Statement

Handle multiple channel operations:

```go
select {
case msg := <-msgChan:
    handleMessage(msg)
case err := <-errChan:
    handleError(err)
case <-time.After(5 * time.Second):
    handleTimeout()
default:
    // Non-blocking: execute if no channel ready
    doSomethingElse()
}
```

### Timeout Pattern

```go
func fetchWithTimeout(url string, timeout time.Duration) ([]byte, error) {
    resultChan := make(chan []byte, 1)
    errChan := make(chan error, 1)

    go func() {
        data, err := fetch(url)
        if err != nil {
            errChan <- err
            return
        }
        resultChan <- data
    }()

    select {
    case data := <-resultChan:
        return data, nil
    case err := <-errChan:
        return nil, err
    case <-time.After(timeout):
        return nil, fmt.Errorf("timeout after %v", timeout)
    }
}
```

### Cancellation with Context

```go
func processWithContext(ctx context.Context, items []Item) error {
    for _, item := range items {
        select {
        case <-ctx.Done():
            return ctx.Err()  // Context cancelled
        default:
            if err := process(item); err != nil {
                return err
            }
        }
    }
    return nil
}

// Usage
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := processWithContext(ctx, items); err != nil {
    log.Printf("processing failed: %v", err)
}
```

## Sync Package

### Mutex for Shared State

```go
type SafeCounter struct {
    mu    sync.Mutex
    count int
}

func (c *SafeCounter) Increment() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.count++
}

func (c *SafeCounter) Value() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.count
}
```

### RWMutex for Read-Heavy Workloads

```go
type Cache struct {
    mu    sync.RWMutex
    items map[string]Item
}

func (c *Cache) Get(key string) (Item, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    item, ok := c.items[key]
    return item, ok
}

func (c *Cache) Set(key string, item Item) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.items[key] = item
}
```

### Once for Single Initialization

```go
var (
    instance *Database
    once     sync.Once
)

func GetDatabase() *Database {
    once.Do(func() {
        instance = connectToDatabase()
    })
    return instance
}
```

### Pool for Object Reuse

```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        return new(bytes.Buffer)
    },
}

func process(data []byte) {
    buf := bufferPool.Get().(*bytes.Buffer)
    defer func() {
        buf.Reset()
        bufferPool.Put(buf)
    }()

    buf.Write(data)
    // Use buffer...
}
```

## Error Handling in Concurrent Code

### errgroup Package

```go
import "golang.org/x/sync/errgroup"

func fetchAll(urls []string) ([]Result, error) {
    var g errgroup.Group
    results := make([]Result, len(urls))

    for i, url := range urls {
        i, url := i, url  // Capture variables
        g.Go(func() error {
            result, err := fetch(url)
            if err != nil {
                return fmt.Errorf("fetching %s: %w", url, err)
            }
            results[i] = result
            return nil
        })
    }

    if err := g.Wait(); err != nil {
        return nil, err  // Returns first error
    }

    return results, nil
}
```

### errgroup with Context

```go
func processWithCancel(ctx context.Context, items []Item) error {
    g, ctx := errgroup.WithContext(ctx)

    for _, item := range items {
        item := item
        g.Go(func() error {
            return processItem(ctx, item)
        })
    }

    return g.Wait()  // Cancels context on first error
}
```

## Graceful Shutdown

```go
func main() {
    server := startServer()

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    // Graceful shutdown with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := server.Shutdown(ctx); err != nil {
        log.Printf("shutdown error: %v", err)
    }
}
```

## Anti-Patterns to Avoid

```go
// BAD: Goroutine leak (no way to stop)
go func() {
    for {
        process()
    }
}()

// GOOD: Stoppable goroutine
go func() {
    for {
        select {
        case <-stopChan:
            return
        default:
            process()
        }
    }
}()

// BAD: Unbuffered channel with no receiver
ch := make(chan int)
ch <- 1  // Blocks forever!

// GOOD: Buffered or ensure receiver exists
ch := make(chan int, 1)
ch <- 1

// BAD: Closing channel from receiver
// Only sender should close

// BAD: Multiple closes
close(ch)
close(ch)  // Panic!

// GOOD: Close once, from sender
go func() {
    defer close(ch)
    for _, item := range items {
        ch <- item
    }
}()

// BAD: Shared memory without synchronization
var counter int
go func() { counter++ }()
go func() { counter++ }()

// GOOD: Use mutex or atomic
var counter int64
go func() { atomic.AddInt64(&counter, 1) }()
go func() { atomic.AddInt64(&counter, 1) }()
```

## References

- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Share Memory By Communicating](https://go.dev/blog/codelab-share)
- [Go Concurrency Patterns: Context](https://go.dev/blog/context)
- [sync package documentation](https://pkg.go.dev/sync)
- [errgroup package](https://pkg.go.dev/golang.org/x/sync/errgroup)
