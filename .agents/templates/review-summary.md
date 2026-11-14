================================================================
[SUCCESS] Review Fix Workflow Complete
================================================================

## [SUMMARY] Execution Summary

**Mode**: {{MODE}}
**Original Branch**: {{ORIGINAL_BRANCH}}
**Fix Branch**: {{FINAL_REVIEW_BRANCH}}

----------------------------------------------------------------

## [PR] Created/Updated PR

**Fix PR**: {{REVIEW_PR_URL}}
**Base Branch**: {{ORIGINAL_BRANCH}}
**Original PR**: {{ORIGINAL_PR_URL}}

----------------------------------------------------------------

## [REVIEW] Review Comment Status

**Total Comments**: {{TOTAL_COMMENTS}}
- [COMPLETED] **Completed**: {{COMPLETED_COUNT}}
- [INCOMPLETE] **Incomplete**: {{INCOMPLETE_COUNT}}
- [MANUAL] **Manual Action Required**: {{MANUAL_COUNT}}

### [COMPLETED] Completed Comments ({{COMPLETED_COUNT}})

{{COMPLETED_COMMENTS_LIST}}

### [INCOMPLETE] Incomplete Comments ({{INCOMPLETE_COUNT}})

{{INCOMPLETE_COMMENTS_LIST}}

### [MANUAL] Comments Requiring Manual Action ({{MANUAL_COUNT}})

{{MANUAL_COMMENTS_LIST}}

----------------------------------------------------------------

## [RESPONSE] Posted Response Comments

**Posted to Original PR**: {{RESPONSE_COUNT}} (out of {{TOTAL_COMMENTS}} total)

{{RESPONSE_COMMENTS_LIST}}

----------------------------------------------------------------

## [FILES] Changed Files

**Files Changed**: {{FILE_COUNT}}
**Lines Added**: +{{ADDITIONS}}
**Lines Deleted**: -{{DELETIONS}}

### Changes by Package:

{{FILE_CHANGES_BY_PACKAGE}}

----------------------------------------------------------------

## [VERIFY] Verification Results

**Compilation**: {{COMPILATION_STATUS}}
**Tests**: {{TEST_STATUS}}
**Coverage**: {{TEST_COVERAGE}}

----------------------------------------------------------------

## [TODO] Next Actions

### Immediate Actions:
1. Review fix PR: {{REVIEW_PR_URL}}
2. Check fix status in original PR: {{ORIGINAL_PR_URL}}
3. If there are incomplete fixes:
   - Switch to fix branch with `git checkout {{FINAL_REVIEW_BRANCH}}`
   - Run `/review-current-pr-and-fix` again to continue

### Items Requiring Manual Intervention:
{{MANUAL_INTERVENTION_ITEMS}}

================================================================
[COMPLETE] Workflow Complete
================================================================
