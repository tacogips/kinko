---
name: impl-progress
description: Plan and execute implementation based on spec and progress tracking. Accepts optional feature name, modification instructions describing current state and desired changes, or empty to auto-continue progress.
---

# Implementation Progress Subagent

## Overview

This subagent manages the implementation workflow for the ign project by:
1. Checking documentation and progress files
2. Planning what to implement in this session
3. Executing the implementation
4. Updating progress tracking

## Arguments

The Task prompt can contain one of the following:

1. **Empty or generic continue request**: Auto-select the next incomplete feature and continue progress
2. **Feature name only**: A short identifier like `template-provider`, `cli-commands` - focus on that specific feature
3. **Modification instructions**: Detailed description of what the current state is, what problem exists, and what changes are needed

### Argument Interpretation

**Empty/Generic** (auto-continue):
- Prompt is empty, whitespace only, or contains generic phrases like "continue", "proceed", "next"
- Action: Scan progress files, find highest priority incomplete feature, continue implementation

**Feature Name**:
- Prompt contains only a short identifier (1-3 words, no sentences)
- Examples: `template-provider`, `cli-commands`, `config parser`
- Action: Focus on that feature's progress file and continue its implementation

**Modification Instructions**:
- Prompt contains sentences describing current state and/or desired changes
- Should describe: what currently exists, what the problem is, what changes are needed
- Action: Analyze the instructions, identify affected feature(s), execute the modifications

### Example Invocations

**Auto-continue (empty)**:
```
Task tool:
  subagent_type: impl-progress
  prompt: ""
```

**Auto-continue (generic)**:
```
Task tool:
  subagent_type: impl-progress
  prompt: "Continue implementation work."
```

**Feature name only**:
```
Task tool:
  subagent_type: impl-progress
  prompt: "template-provider"
```

**Modification instructions - describe current state and desired change**:
```
Task tool:
  subagent_type: impl-progress
  prompt: |
    The template parser currently does not track line numbers.
    When an error occurs, users only see the error message without location information.

    Modify the parser to:
    - Track line and column positions during parsing
    - Include line:column in all error messages
    - Add a context snippet showing the problematic line
```

**Modification instructions - bug fix**:
```
Task tool:
  subagent_type: impl-progress
  prompt: |
    The CLI init command currently always overwrites existing config files without warning.

    Fix this by:
    - Checking if config file exists before writing
    - Prompting user for confirmation if file exists
    - Adding a --force flag to skip the confirmation
```

---

## Phase 1: Parse Arguments and Gather Context

### 1.1 Interpret the Task Prompt

Analyze the prompt to determine the argument type:

1. **Check for empty/generic**: Is the prompt empty, whitespace, or generic continue phrase?
   - Yes: Set mode to AUTO_CONTINUE

2. **Check for feature name**: Is the prompt a short identifier (1-3 words, no verbs/sentences)?
   - Yes: Set mode to FEATURE_FOCUS, extract feature name

3. **Otherwise**: Treat as modification instructions
   - Set mode to MODIFICATION
   - Parse for: current state description, problem description, desired changes

### 1.2 Gather Documentation and Progress

Read the following files to understand the current state:

1. **Specification**: Read `docs/spec.md` for overall requirements
2. **Reference Documentation**: Check `docs/reference/` for detailed syntax/command specs
3. **Architecture**: Read `docs/implementation/architecture.md` for design patterns
4. **Progress Files**: Check `docs/progress/` for implementation status

**For FEATURE_FOCUS mode**: Read `docs/progress/<feature-name>.md` specifically
**For MODIFICATION mode**: Identify which feature(s) are affected by the instructions

---

## Phase 2: Plan Implementation

### 2.1 Identify Target

**AUTO_CONTINUE mode**:
- Scan all progress files
- Prioritize: "Not Started" > "In Progress" features
- Consider dependencies between features
- Select the highest priority incomplete feature

**FEATURE_FOCUS mode**:
- Use the specified feature as the target
- Read its progress file to understand current state

**MODIFICATION mode**:
- Analyze the instructions to identify affected files/features
- May span multiple features
- The instructions themselves define the scope

### 2.2 Define Scope

**AUTO_CONTINUE / FEATURE_FOCUS mode**:
- Review the Remaining items in the progress file
- Break down into concrete implementation tasks
- Identify files to create/modify, interfaces, tests

**MODIFICATION mode**:
- Extract specific changes from the instructions
- Identify files that need modification
- Define test requirements based on the changes

### 2.3 Set Completion Criteria

**AUTO_CONTINUE / FEATURE_FOCUS mode**:
- Code compiles without errors
- Tests pass
- Progress file items are completed

**MODIFICATION mode**:
- The described changes are implemented
- Any mentioned edge cases are handled
- Tests verify the new behavior
- Code compiles and tests pass

### 2.4 Create Todo List

Use TodoWrite to track the implementation tasks.

### 2.5 Present Plan and Proceed

Present the plan with:
- Feature(s) being worked on
- Spec reference (if applicable)
- Implementation tasks (numbered list)
- Completion criteria
- Estimated file changes

**AUTO_CONTINUE / FEATURE_FOCUS mode**: Ask for user confirmation before proceeding
**MODIFICATION mode**: Proceed directly (user has already provided specific direction)

---

## Phase 3: Execute Implementation

Execute the implementation using the **go-coding subagent**.

For each implementation task, invoke the Task tool with:
- `subagent_type`: `go-coding`
- `prompt`: Must include all required fields:
  - **Purpose**: What this implementation achieves
  - **Reference Document**: Path to relevant spec/reference doc (or "User instructions" for MODIFICATION mode)
  - **Implementation Target**: Specific files/functions to implement
  - **Completion Criteria**: What defines "done"

Example invocation format:
```
Purpose: Implement the TemplateProvider interface for GitHub sources
Reference Document: docs/spec.md (Section 2.3), docs/implementation/architecture.md
Implementation Target: Create internal/provider/github.go with GitHubProvider struct
Completion Criteria:
  - Implements TemplateProvider interface (Fetch, List, Validate methods)
  - Handles github.com/owner/repo URL parsing
  - Returns TemplateRoot with file contents
  - Unit tests cover success and error cases
  - go build and go test pass
```

After each go-coding subagent completes:
- Review the implementation result
- Mark the corresponding todo item as completed
- If there were issues, note them for the progress file

---

## Phase 4: Update Progress

After implementation is complete (or partially complete), update the progress file:

1. **Create progress file if needed**: If `docs/progress/<feature-name>.md` doesn't exist, create it

2. **Update status**:
   - `Not Started` -> `In Progress` (if work began)
   - `In Progress` -> `Completed` (if all items done)

3. **Update Implemented list**: Add completed items with file paths
   ```markdown
   - [x] TemplateProvider interface (`internal/provider/provider.go:15`)
   - [x] GitHubProvider implementation (`internal/provider/github.go`)
   ```

4. **Update Remaining list**: Mark completed items, add any new discovered items

5. **Add Design Decisions**: Document any notable decisions made during implementation

6. **Add Notes**: Record any issues, considerations, or follow-up items

---

## Progress File Template

If creating a new progress file, use this structure:

```markdown
# <Feature Name>

**Status**: Not Started | In Progress | Completed

## Spec Reference
- docs/spec.md Section X.X
- docs/reference/xxx.md

## Implemented
- [ ] Sub-feature A
- [ ] Sub-feature B

## Remaining
- [ ] Sub-feature C
- [ ] Sub-feature D

## Design Decisions
- (none yet)

## Notes
- (none yet)
```

---

## Response Format

### Success Response

After completing the implementation work, return:

```
## Implementation Session Complete

### Mode: <AUTO_CONTINUE | FEATURE_FOCUS | MODIFICATION>
### Feature: <feature-name>

### Work Completed
- [x] Task 1: <description>
- [x] Task 2: <description>
- [ ] Task 3: <description> (incomplete - reason)

### Files Changed
- `path/to/file1.go`: <brief description>
- `path/to/file2.go`: <brief description>

### Test Results
<summary of test execution>

### Progress File Updated
- Status: <new status>
- Implemented: <count> items
- Remaining: <count> items

### Next Steps
- <what to work on next>
```

### Partial Completion Response

If work could not be fully completed:

```
## Implementation Session Partial

### Mode: <AUTO_CONTINUE | FEATURE_FOCUS | MODIFICATION>
### Feature: <feature-name>

### Work Completed
- [x] Task 1: <description>

### Blockers
- <what prevented completion>

### Files Changed
- `path/to/file.go`: <brief description>

### Recommended Next Steps
- <what needs to be resolved>
```

---

## Important Guidelines

1. **Always read before implementing**: Never propose changes to code you haven't read
2. **Follow existing patterns**: Match the project's coding standards from CLAUDE.md
3. **Respect provided instructions**: If modification instructions are given, follow them precisely
4. **Minimal changes**: Only implement what's needed for the current task
5. **Test coverage**: Ensure tests are written for new functionality
6. **Atomic progress**: Update progress file after each logical unit of work
7. **No over-engineering**: Implement to spec, no extras unless requested
