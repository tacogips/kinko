---
description: Execute parallel tasks across multiple files/items using sub-agents
argument-hint: <instruction>
---

You are tasked with executing parallel operations across multiple files or items based on the user's instruction.

**User Instruction:**
$ARGUMENTS

**Your Task:**

1. **Analyze and Decompose**: Carefully analyze the instruction to identify all independent subtasks that can be executed in parallel. Each subtask should:
   - Be independent (no dependencies on other subtasks)
   - Target specific files, directories, or items
   - Have a clear, focused objective

2. **Execute in Parallel**: Launch multiple Task tool invocations in a SINGLE message to execute subtasks concurrently. Use appropriate subagent types:
   - `Explore` for code exploration and searching
   - `general-purpose` for complex multi-step operations
   - Choose the most suitable agent type for each subtask

3. **Merge and Report**: After all parallel tasks complete:
   - Collect and synthesize results from all sub-agents
   - Identify patterns, conflicts, or inconsistencies across results
   - Present a unified, comprehensive report to the user with:
     - Summary of all findings
     - File-by-file or item-by-item breakdown
     - Any recommendations or next steps
     - Highlighted issues or conflicts found

**Important Guidelines:**
- Launch ALL sub-agents in a single message with multiple Task tool calls
- Each subtask should have a clear, specific prompt
- Specify what information each agent should return
- Wait for all agents to complete before merging results
- Do NOT perform the work yourself - delegate to sub-agents

**Example Execution Pattern:**

If instruction is "Check error handling in auth.go, db.go, and api.go":

Launch 3 agents in parallel:
- Agent 1: Analyze error handling in auth.go
- Agent 2: Analyze error handling in db.go
- Agent 3: Analyze error handling in api.go

Then merge their findings into a comprehensive report comparing error handling patterns across all three files.
