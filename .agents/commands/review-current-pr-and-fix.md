---
description: Review the current directory's PR and fix identified issues (user)
argument-hint: [instruction]
---

## Context

- Current branch: !`git branch --show-current`
- Default branch: !`git remote show origin | grep 'HEAD branch' | cut -d' ' -f5`
- Existing PR for current branch: !`gh pr view --json number,title,isDraft,url 2>/dev/null || echo "No PR found"`

## Arguments

This command accepts optional instruction arguments that customize the review scope:

**Format**: `/review-current-pr-and-fix [instruction]`

**Examples**:
- `/review-current-pr-and-fix` - Review all changes in the PR (default behavior)
- `/review-current-pr-and-fix Review only files in pkg/document/` - Review only files in pkg/document/
- `/review-current-pr-and-fix Review only test files` - Review only test files

**Important**: Instructions apply to review phase only. Files excluded from review will not have issues identified.

## Your Task

Review the pull request for the current directory's branch, identify all review targets (diff sections and review comments), delegate each target to the review-single-target agent for analysis, then fix all identified issues in a separate review branch.

**This command only works when a PR exists for the current branch.**

Use the `review-current-pr-and-fix` agent to handle the complete workflow.

## Workflow Summary

1. Check current branch status and mode (Normal vs Continuation)
2. Verify PR exists
3. Collect review targets from PR diff (using `gh pr diff`)
4. Review each target (single-file, cross-file, and Go coding guideline compliance)
5. Create review branch (`{original_branch}_review_{n}`)
6. Delegate fixes by module/package to apply-pr-review-chunk agents
7. Commit and push
8. Create PR from review branch

See `.claude/agents/review-current-pr-and-fix.md` for complete workflow details.

## Workflow Modes

### Normal Mode (New Review)
- Current branch does NOT end with `_review_{n}`
- Creates new review branch with pattern `{original_branch}_review_{n}`
- Performs full three-stage review phase
- Posts new review comments to the original PR
- Creates a new PR from review branch back to original branch

### Continuation Mode (Resume Incomplete Fixes)
- Current branch DOES end with `_review_{n}`
- Extracts original branch name and review comment URLs from existing PR body
- Analyzes which comments are complete/incomplete/failed
- Continues with apply-pr-review-chunk to address remaining issues
- Updates existing PR with additional fixes

## Prerequisites

- PR must exist for the current branch (or current review branch in continuation mode)
- No uncommitted changes (normal mode only)
- GitHub CLI (gh) must be authenticated
- Required scripts and templates in `.claude/` directory

## Multi-Agent Architecture

This workflow orchestrates multiple specialized agents:

1. **review-single-target**: Analyzes individual files and posts PR comments for single-file issues
2. **collect-relative-files-in-pr**: Identifies groups of related files that should be reviewed together
3. **review-multiple-target**: Analyzes cross-file consistency and posts PR comments for integration issues
4. **go-coding-guideline**: Provides Go coding guidelines for compliance verification
5. **apply-pr-review-chunk**: Implements fixes per module/package using PR comment URLs
6. **git-pr**: Creates or updates the review fixes PR

## Key Outputs

- Review comments posted to the original PR (inline code comments with issues)
- Review fixes PR created from `{original_branch}_review_{n}` branch
- Response comments posted to completed fixes on original PR
- Comprehensive summary with verification results

## Important Notes

- **Always fetch diff from GitHub PR** (`gh pr diff`), NOT local git diff
- **Post review comments to PR** for every issue found (using gh api)
- **Check uncommitted changes** before starting (exit if any found in normal mode)
- **Run `gofmt`/`goimports`** on modified files before committing
- **CRITICAL**: Only commit and push if the user explicitly instructs to do so (default: show fixes for review)
