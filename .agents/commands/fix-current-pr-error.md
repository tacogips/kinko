---
description: Fix GitHub Actions errors from the current PR (user)
---

## Context

- Current branch: !`git branch --show-current`
- Default branch: !`git remote show origin | grep 'HEAD branch' | cut -d' ' -f5`
- Existing PR for current branch: !`gh pr view --json number,title,url 2>/dev/null || echo "No PR found"`

## Your Task

Fetch the current branch's pull request, retrieve GitHub Actions check failures and error messages, analyze the errors, and fix them.

**This command only works when a PR exists for the current branch.**

## GitHub Actions Error Fix Process

### Step 1: Verify PR exists for current branch

```bash
gh pr view --json number,title,url,baseRefName,headRefName
```

- If no PR exists: Show error message and exit
- If PR exists: Extract PR details (number, title, URL, base branch, head branch)

### Step 2: Fetch GitHub Actions check status

```bash
gh pr checks
```

Parse the check output to identify:
- Failed checks (status shows failure indicator)
- Check names and their status
- If all checks are passing: Report success and exit
- If checks are pending: Notify user that checks are still running
- If checks failed: Proceed to error analysis

### Step 3: Fetch detailed error logs from failed checks

**Using gh CLI:**
```bash
# List all check runs and find failed ones
gh api repos/{owner}/{repo}/commits/{head_sha}/check-runs \
  --jq '.check_runs[] | select(.conclusion == "failure") | {name: .name, id: .id, html_url: .html_url}'

# Get logs for failed runs
gh run view {run_id} --log-failed
```

**Using GraphQL API (for more detailed check information):**
```bash
gh api graphql -f query='
query {
  repository(owner: "{owner}", name: "{repo}") {
    pullRequest(number: {pr_number}) {
      commits(last: 1) {
        nodes {
          commit {
            oid
            statusCheckRollup {
              state
              contexts(first: 50) {
                nodes {
                  ... on CheckRun {
                    name
                    conclusion
                    status
                    detailsUrl
                    text
                    summary
                    startedAt
                    completedAt
                  }
                  ... on StatusContext {
                    context
                    state
                    targetUrl
                    description
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}'
```

**Extracting failed checks from GraphQL response:**
```bash
# Parse and filter failed checks
gh api graphql -f query='...' | jq '
  .data.repository.pullRequest.commits.nodes[0].commit.statusCheckRollup.contexts.nodes
  | map(select(.conclusion == "FAILURE" or .state == "FAILURE"))
  | .[] | {name: (.name // .context), url: (.detailsUrl // .targetUrl), summary: .summary}
'
```

Extract error messages by looking for:
- `error:` or `ERROR:` for compilation/lint errors
- `FAILED` or `--- FAIL` for test failures
- `panic:` for runtime panics
- Stack traces and file:line references

### Step 4: Categorize and analyze errors

Group errors by type:

1. **Compilation Errors**: Go compiler errors
   - Type mismatches, undefined identifiers, import errors
   - Pattern: `./path/file.go:line:col: error message`

2. **Test Failures**: Failed unit/integration tests
   - Pattern: `--- FAIL: TestName (duration)`
   - Look for assertion failures, unexpected values, panic messages

3. **Lint Errors**: golangci-lint warnings/errors
   - Pattern: `path/file.go:line:col: linter-name: message`
   - Common: unused variables, error handling, formatting

4. **Build Errors**: Dependency or build system issues
   - Module resolution failures
   - Missing dependencies

For each error, extract:
- Error type/category
- File path and line number
- Error message
- Suggested fix direction (from compiler output if available)

### Step 5: Display error summary and plan fixes

Display error summary:

```
## GitHub Actions Error Summary

**PR**: #{number} - {title}
**URL**: {pr_url}
**Failed Checks**: {count}

### Compilation Errors ({count}):
1. {file_path}:{line} - {error_message}

### Test Failures ({count}):
1. {test_name} in {file_path} - {failure_reason}

### Lint Errors ({count}):
1. {file_path}:{line} - {lint_message}
```

Use TodoWrite to create fix tasks ordered by priority:
1. Compilation errors (highest - blocking)
2. Test failures (high)
3. Lint errors (medium)

### Step 6: Fix errors systematically

For each error in priority order:

1. **Mark todo as in_progress**

2. **Read the relevant file** using Read tool

3. **Analyze the error context**:
   - Read the surrounding code
   - Understand the function/module structure
   - Check for related files (imports, dependencies)
   - Read error message details from GitHub Actions logs

4. **Apply the fix** using Edit tool:
   - Follow Go best practices and CLAUDE.md guidelines
   - Handle errors properly
   - Ensure proper formatting

5. **Verify the fix locally**:
   ```bash
   # For compilation errors:
   go build ./...

   # For specific package:
   go build ./internal/{package_name}

   # For lint errors:
   golangci-lint run ./...

   # For test failures:
   go test -run TestName -v ./path/to/package

   # For formatting issues:
   gofmt -l .
   ```

6. **Mark todo as completed**

7. **Handle verification failures**:
   - If local check still fails: Re-analyze the error and try alternative approach
   - If fix causes new errors: Revert and try different solution
   - If fix cannot be automated: Mark as "needs manual intervention" and continue

### Step 7: Final verification

Run comprehensive local checks:

```bash
# Full workspace compilation check
go build ./...

# Lint check
golangci-lint run ./...

# Format check
gofmt -l .

# Run all tests
go test ./... -v
```

Generate and display fix summary:

```
## Fix Summary

**PR**: #{number} - {title}

### Successfully Fixed:
- {count} Compilation errors
- {count} Test failures
- {count} Lint errors

### Changes Made:
1. {file_path} - {description_of_fix}

### Verification Results:
- Compilation: PASS/FAIL
- Lint: PASS/FAIL
- Format: PASS/FAIL
- Tests: PASS/FAIL

### Needs Manual Intervention:
- {list of remaining issues if any}
```

### Step 8: Commit and push fixes (optional)

**CRITICAL**: Only perform this step if the user explicitly requests to commit and push.

```bash
git add -A
git commit -m "fix: resolve GitHub Actions errors from PR #{pr_number}

- Fix compilation errors in {files}
- Fix test failures: {test_names}
- Fix lint errors in {files}
"
git push origin HEAD
```

## Important Notes

### Current Branch Only
- This command only works for PRs associated with the current branch
- It does NOT accept PR number arguments
- Execute in the repository directory with an active PR

### Error Detection
The command detects errors from:
- GitHub Actions workflow failures
- Compilation errors (go build, go vet)
- Test failures (go test)
- Lint errors (golangci-lint, gofmt)

### Automated Fixing
The command attempts to automatically fix:
- Common compilation errors (type mismatches, missing imports)
- Simple lint violations
- Formatting issues
- Some test failures (if the fix is clear from error messages)

### Manual Intervention Required
Some errors require manual intervention:
- Complex logic errors
- Architectural issues
- Unclear error messages
- Test failures requiring domain knowledge

### Progress Tracking
Use TodoWrite to track fix progress:
- One todo item per error to fix
- Mark items as in_progress before starting
- Mark items as completed immediately after fixing

### Verification
Always verify fixes locally before pushing:
- Run go build, golangci-lint, gofmt, and go test
- Ensure no new errors are introduced

### Commit Policy
- **CRITICAL**: Only commit and push if the user explicitly instructs to do so
- Default behavior: Show the fixes and let the user review
- If user says "commit" or "push": Then perform Step 8

### GitHub API Usage
The command uses:
- `gh pr view` - Get PR information
- `gh pr checks` - List check statuses
- `gh api` - Fetch detailed check run logs and error messages
- `gh run view` - View workflow run logs
