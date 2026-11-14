# <Feature Name> Implementation Plan

**Status**: Planning | Ready | In Progress | Completed
**Design Reference**: design-docs/<file>.md#<section>
**Created**: YYYY-MM-DD
**Last Updated**: YYYY-MM-DD

---

## Design Document Reference

**Source**: design-docs/<file>.md

### Summary
Brief description of the feature being implemented.

### Scope
**Included**: What is being implemented
**Excluded**: What is NOT part of this implementation

---

## Modules

### 1. <Module Category>

#### internal/path/to/file.go

**Status**: NOT_STARTED

```go
type Example interface {
    Method(param string) error
}

type ExampleImpl struct {
    // fields
}
```

**Checklist**:
- [ ] Define Example interface
- [ ] Implement ExampleImpl struct
- [ ] Unit tests

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| Example interface | `internal/path/to/file.go` | NOT_STARTED | - |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| This feature | Foundation layer | Available |

## Completion Criteria

- [ ] All modules implemented
- [ ] All tests passing
- [ ] go build passes
- [ ] go vet passes

## Progress Log

### Session: YYYY-MM-DD HH:MM
**Tasks Completed**: None yet
**Tasks In Progress**: Starting implementation
**Blockers**: None
**Notes**: Initial session

## Related Plans

- **Previous**: (if split from larger plan)
- **Next**: (if continued in another plan)
- **Depends On**: (other plan files this depends on)
