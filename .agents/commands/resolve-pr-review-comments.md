---
description: Verify and resolve PR review comments that have been addressed and merged (user)
argument-hint: [pr-url]
---

## Context

- Current branch: !`git branch --show-current`
- Repository: !`git remote get-url origin 2>/dev/null | sed 's/.*github.com[:/]\(.*\)\.git/\1/' || echo "Unknown"`

## Arguments

This command accepts an optional PR URL argument:

**Format**: `/resolve-pr-review-comments [pr-url]`

**Examples**:
- `/resolve-pr-review-comments` - Use the current branch's PR
- `/resolve-pr-review-comments https://github.com/owner/repo/pull/123` - Use specified PR

## Your Task

Verify which PR review comments have been addressed by commits and automatically resolve those comments on GitHub. This command compares the current source code with the source at the time of review to determine if issues have been fixed.

**No user confirmation is required** - resolved comments are automatically updated.

Use the `verify-pr-comment-resolution` agent to handle the verification and resolution.

## Workflow Summary

1. Identify the target PR (from argument or current branch)
2. Fetch all unresolved review comments from the PR
3. For each comment, compare the source at review time vs. current source
4. Determine if the issue has been fixed by analyzing code changes
5. Automatically resolve verified comments on GitHub
6. Display summary of resolved and remaining comments

## Agent Delegation

Use the `verify-pr-comment-resolution` agent (`.claude/agents/verify-pr-comment-resolution.md`) to perform the verification and resolution. The agent will:

1. Fetch review threads via GraphQL API
2. Compare source code at `originalCommit` vs. `HEAD`
3. Analyze if changes address the review feedback
4. Check for "no action needed" replies from PR author
5. Post reply comments before resolving threads
6. Execute GraphQL mutations to resolve threads

## Resolution Criteria

A comment is resolved when:
- **Code Changed + Issue Fixed**: The change directly addresses the review feedback
- **Code Deleted/Refactored**: The problematic code no longer exists
- **No Action Needed Reply**: PR author or maintainer explicitly stated no action required

A comment remains unresolved when:
- No code change at the commented location (without acknowledgment)
- Change does not address the specific issue raised
- Only partial fix applied
- Unable to determine with confidence

## Error Handling

- **No PR found**: Show error and exit
- **No unresolved comments**: Report success with "Nothing to resolve"
- **File not found at commit**: Log warning and skip that comment
- **API rate limit**: Report partial progress and suggest retry
- **Permission denied**: Verify write access to repository

## Important Notes

- **Automatic Resolution**: Comments are resolved automatically when verification passes
- **Conservative Approach**: Only resolve when there is clear evidence of the fix
- **Reply Before Resolve**: Always post a reply comment explaining the fix before resolving
- **Source Comparison**: Uses `git show {commit}:{path}` to compare code at different commits
