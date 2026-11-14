#!/usr/bin/env bash
# Post a response comment to a review comment on the original PR
# Usage: ./post-response-comment.sh <repo_owner> <repo_name> <review_comment_url> <review_pr_url> <fix_description> <changed_files> <test_details>

set -e

REPO_OWNER="$1"
REPO_NAME="$2"
REVIEW_COMMENT_URL="$3"
REVIEW_PR_URL="$4"
FIX_DESCRIPTION="$5"
CHANGED_FILES="$6"
TEST_DETAILS="$7"

# Extract review comment ID from URL
# URL format: https://github.com/owner/repo/pull/123#discussion_r456789
REVIEW_COMMENT_ID=$(echo "$REVIEW_COMMENT_URL" | grep -oP 'discussion_r\K\d+')

if [ -z "$REVIEW_COMMENT_ID" ]; then
  echo "Error: Could not extract review comment ID from URL: $REVIEW_COMMENT_URL" >&2
  exit 1
fi

# Create comment body from template
COMMENT_BODY=$(cat <<EOF
[FIXED] Fixed

This issue has been addressed.

**Fix PR**: $REVIEW_PR_URL

**Fix Description**:
$FIX_DESCRIPTION

**Changed Files**:
$CHANGED_FILES

**Verification Results**:
- Compilation: PASSED
- Tests: PASSED$TEST_DETAILS
EOF
)

# Post reply to the review comment
gh api "repos/$REPO_OWNER/$REPO_NAME/pulls/comments/$REVIEW_COMMENT_ID/replies" \
  -f body="$COMMENT_BODY" || {
  echo "Warning: Failed to post response comment to review comment thread" >&2
  echo "Review comment URL: $REVIEW_COMMENT_URL" >&2
  exit 1
}

echo "Posted response comment to: $REVIEW_COMMENT_URL"
