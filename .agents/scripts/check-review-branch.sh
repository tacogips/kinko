#!/usr/bin/env bash
# Check if current branch is already a review branch
# Outputs: CURRENT_BRANCH, SKIP_BRANCH_PREP, REVIEW_BRANCH, CONTINUATION_MODE, ORIGINAL_BRANCH

set -e

CURRENT_BRANCH=$(git branch --show-current)

# Check if branch name ends with _review_{n} pattern
if [[ "$CURRENT_BRANCH" =~ _review_[0-9]+$ ]]; then
  echo "Already on a review branch: $CURRENT_BRANCH"
  echo "Entering continuation mode - will check for incomplete fixes"
  SKIP_BRANCH_PREP=true
  REVIEW_BRANCH="$CURRENT_BRANCH"
  CONTINUATION_MODE=true

  # Extract ORIGINAL_BRANCH from current review branch name
  # Example: feature/some_review_1 -> feature/some
  ORIGINAL_BRANCH="${CURRENT_BRANCH%_review_*}"
  echo "Detected original branch: $ORIGINAL_BRANCH"
else
  SKIP_BRANCH_PREP=false
  CONTINUATION_MODE=false
  ORIGINAL_BRANCH="$CURRENT_BRANCH"
fi

# Export variables for caller
echo "CURRENT_BRANCH=$CURRENT_BRANCH"
echo "SKIP_BRANCH_PREP=$SKIP_BRANCH_PREP"
echo "REVIEW_BRANCH=$REVIEW_BRANCH"
echo "CONTINUATION_MODE=$CONTINUATION_MODE"
echo "ORIGINAL_BRANCH=$ORIGINAL_BRANCH"
