---
name: impl-plan
description: Use when creating implementation plans from design documents. Provides plan structure, status tracking, and progress logging guidelines.
allowed-tools: Read, Write, Glob, Grep
---

# Implementation Plan Skill

This skill provides guidelines for creating and managing implementation plans from design documents.

## When to Apply

Apply this skill when:
- Translating design documents into actionable implementation plans
- Planning multi-session implementation work
- Breaking down large features into parallelizable subtasks
- Tracking implementation progress across sessions

## Purpose

Implementation plans bridge the gap between design documents (what to build) and actual implementation (how to build). They provide:
- Clear deliverables with Go type definitions
- Simple status tracking tables
- Checklist-based completion criteria
- Progress tracking across sessions

## Plan Granularity

**IMPORTANT**: Implementation plans and spec files do NOT need 1:1 mapping.

| Mapping | When to Use |
|---------|-------------|
| **1:N** (one spec -> multiple plans) | Large specs should be split into smaller, focused units |
| **N:1** (multiple specs -> one plan) | Related specs sharing dependencies can be combined |
| **1:1** (one spec -> one plan) | Well-bounded features with clear scope |

**Recommended granularity**:
- Each plan should be completable in 1-3 sessions
- Each plan should have 3-10 subtasks
- Maximize parallelizable subtasks

## File Size Limits

**CRITICAL**: Large implementation plan files cause Claude Code OOM (Out of Memory) errors.

### Hard Limits

| Metric | Limit | Reason |
|--------|-------|--------|
| **Line count** | MAX 400 lines | Prevents memory issues when agents read files |
| **Modules per plan** | MAX 8 modules | Keeps plans focused and manageable |
| **Tasks per plan** | MAX 10 tasks | Enables completion in 1-3 sessions |

### When to Split Plans

Split a plan into multiple files when ANY of these conditions are met:

1. **Line count exceeds 400 lines**: Split by phase or module category
2. **More than 8 modules**: Group related modules into separate plans
3. **More than 10 tasks**: Break into logical sub-plans
4. **Multiple phases with dependencies**: Create separate plans per phase

### Splitting Strategy

```
BEFORE (one large plan):
impl-plans/active/foundation-and-core.md (1100+ lines)

AFTER (split by phase):
impl-plans/active/foundation-interfaces.md (~200 lines)
impl-plans/active/foundation-mocks.md (~150 lines)
impl-plans/active/foundation-types.md (~150 lines)
impl-plans/active/foundation-core-services.md (~200 lines)
```

### Split Plan Naming Convention

When splitting, use consistent naming:
- `{feature}-{phase}.md` - For phase-based splits
- `{feature}-{category}.md` - For category-based splits

Example:
- `session-groups-types.md`
- `session-groups-repository.md`
- `session-groups-manager.md`

### Cross-References Between Split Plans

Each split plan MUST include:
```markdown
## Related Plans
- **Previous**: `impl-plans/active/foundation-interfaces.md` (Phase 1)
- **Next**: `impl-plans/active/foundation-core-services.md` (Phase 3)
- **Depends On**: `foundation-interfaces.md`, `foundation-types.md`
```

## Output Location

**IMPORTANT**: All implementation plans MUST be stored under `impl-plans/` subdirectories.

```
impl-plans/
├── README.md              # Index of all implementation plans
├── active/                # Currently active implementation plans
│   └── <feature>.md       # One file per feature being implemented
├── completed/             # Completed implementation plans (archive)
│   └── <feature>.md       # Completed plans for reference
└── templates/             # Plan templates
    └── plan-template.md   # Standard plan template
```

## Directory Rules

| Directory | Purpose |
|-----------|---------|
| `impl-plans/active/` | Implementation plans currently in progress |
| `impl-plans/completed/` | Archived completed plans for reference |
| `impl-plans/templates/` | Plan templates and examples |

**DO NOT** create implementation plan files outside `impl-plans/`.

## Implementation Plan Structure

Each implementation plan file MUST include:

### 1. Header Section
```markdown
# <Feature Name> Implementation Plan

**Status**: Planning | Ready | In Progress | Completed
**Design Reference**: design-docs/<file>.md#<section>
**Created**: YYYY-MM-DD
**Last Updated**: YYYY-MM-DD
```

### 2. Design Document Reference
- Link to specific design document section
- Summary of what is being implemented
- Scope boundaries (what is NOT included)

### 3. Modules and Types

List each module with its Go type definitions. **USE ACTUAL GO CODE** for interfaces, structs, and function signatures - not prose descriptions.

```markdown
## Modules

### 1. Core Interfaces

#### internal/interfaces/filesystem.go

**Status**: NOT_STARTED

```go
type FileSystem interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, content []byte) error
    Exists(path string) (bool, error)
    Watch(path string) (<-chan WatchEvent, error)
}

type WatchEvent struct {
    Type WatchEventType
    Path string
}

type WatchEventType int

const (
    WatchCreate WatchEventType = iota
    WatchModify
    WatchDelete
)
```

**Checklist**:
- [ ] Define FileSystem interface
- [ ] Define WatchEvent struct
- [ ] Export from interfaces package
- [ ] Unit tests
```

### 4. Status Tracking Table

Use simple tables for overview tracking:

```markdown
## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| FileSystem interface | `internal/interfaces/filesystem.go` | NOT_STARTED | - |
| ProcessManager interface | `internal/interfaces/process.go` | NOT_STARTED | - |
| Mock implementations | `internal/test/mocks/*.go` | NOT_STARTED | - |
```

### 5. Dependencies

Simple table showing what depends on what:

```markdown
## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Phase 2: Repository | Phase 1: Interfaces | BLOCKED |
| Phase 3: Core Services | Phase 1, Phase 2 | BLOCKED |
```

### 6. Completion Criteria

Simple checklist:

```markdown
## Completion Criteria

- [ ] All modules implemented
- [ ] All tests passing
- [ ] go build passes
- [ ] go vet passes
- [ ] Integration verified
```

### 7. Progress Log

Track session-by-session progress:

```markdown
## Progress Log

### Session: YYYY-MM-DD HH:MM
**Tasks Completed**: Module 1, Module 2
**Tasks In Progress**: Module 3
**Blockers**: None
**Notes**: Discovered edge case in variable parsing
```

## Content Guidelines

### INCLUDE Go Code

**ALWAYS** include actual Go code for:
- Interface definitions
- Struct definitions
- Type aliases and constants
- Function signatures (name, parameters, return types)

Example:
```markdown
```go
type SessionGroup struct {
    ID        string       // Format: YYYYMMDD-HHMMSS-{slug}
    Name      string
    Status    GroupStatus
    Sessions  []GroupSession
    Config    GroupConfig
    CreatedAt time.Time
}

type GroupStatus string

const (
    GroupStatusCreated   GroupStatus = "created"
    GroupStatusRunning   GroupStatus = "running"
    GroupStatusPaused    GroupStatus = "paused"
    GroupStatusCompleted GroupStatus = "completed"
    GroupStatusFailed    GroupStatus = "failed"
)
```
```

### DO NOT Include

- Implementation logic (function bodies)
- Private/unexported functions
- Algorithm details
- Excessive prose descriptions

### Format Comparison

**GOOD** (Go-first):
```markdown
#### internal/interfaces/clock.go

```go
type Clock interface {
    Now() time.Time
    Timestamp() string
    Sleep(d time.Duration)
}
```

**Checklist**:
- [ ] Define Clock interface
- [ ] Export from interfaces package
```

**BAD** (Prose-heavy):
```markdown
**Exports**:
| Name | Type | Purpose | Called By |
|------|------|---------|-----------|
| `Clock` | interface | Time operations | Caching, logging |

**Function Signatures**:
Now() time.Time
  Purpose: Get current date/time
  Called by: Logger, Cache
```

## Parallelization Rules

Subtasks can be parallelized when:
1. No data dependencies between tasks
2. No shared file modifications
3. No order-dependent side effects

Mark dependencies explicitly in the status table.

## Workflow Integration

### Creating a Plan
1. Read the design document
2. Identify feature boundaries
3. Define Go interfaces, structs, and types
4. List modules with status tracking
5. Set completion criteria
6. Create plan file in `impl-plans/active/`

### During Implementation
1. Update module status as work progresses
2. Add progress log entries per session
3. Note blockers and decisions
4. Check off completion criteria

### Completing a Plan
1. Verify all completion criteria met
2. Update status to Completed
3. Add final progress log entry
4. Move file to `impl-plans/completed/`

## Quick Reference

| Section | Required | Format |
|---------|----------|--------|
| Header | Yes | Markdown metadata |
| Design Reference | Yes | Link + summary |
| Modules | Yes | Go code blocks + checklist |
| Status Table | Yes | Simple table |
| Dependencies | Yes | Simple table |
| Completion Criteria | Yes | Checklist |
| Progress Log | Yes | Session entries |
