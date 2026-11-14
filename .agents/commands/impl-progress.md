---
description: Plan and execute implementation based on spec and progress tracking
argument-hint: "[options: feature-name, modification instructions, or empty to auto-continue]"
---

## Implementation Progress Command

This command delegates to the `impl-progress` subagent for implementation work.

### Current Context

- Working directory: !`pwd`
- Current branch: !`git branch --show-current`

### Arguments Received

$ARGUMENTS

---

## Instructions

Invoke the `impl-progress` subagent using the Task tool.

Pass `$ARGUMENTS` directly to the subagent prompt. The subagent will interpret the arguments as:

1. **Empty**: Auto-select the next incomplete feature and continue progress
2. **Feature name only**: Focus on that specific feature and continue its progress
3. **Modification instructions**: Detailed instructions describing what the current state is and what changes are needed

```
Task tool parameters:
  subagent_type: impl-progress
  prompt: |
    $ARGUMENTS
```

### Usage Examples

**No arguments - auto-continue progress**:
```
/impl-progress
```
The subagent will scan progress files, find the highest priority incomplete feature, and continue implementation.

**Feature name only**:
```
/impl-progress template-provider
```
Focus on the template-provider feature and continue its implementation.

**Detailed modification instructions**:
```
/impl-progress The template parser currently does not show line numbers in error messages.
Modify the parser to track line and column positions, and include them in error output.
```

**Current state and desired change**:
```
/impl-progress Currently the CLI init command creates config in the current directory.
Change it to accept an optional --path flag to specify the output directory.
Add validation to ensure the path exists and is writable.
```

After the subagent completes, report the results back to the user.
