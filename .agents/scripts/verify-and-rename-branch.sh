#!/usr/bin/env bash
# Pre-commit branch verification and rename if collision detected
# Usage: ./verify-and-rename-branch.sh <review_branch>
# Outputs: FINAL_REVIEW_BRANCH

set -e

REVIEW_BRANCH="$1"

if [ -z "$REVIEW_BRANCH" ]; then
  echo "Error: REVIEW_BRANCH argument required" >&2
  exit 1
fi

# Function to check if branch exists in remote
remote_branch_exists() {
  local branch_name=$1
  if git ls-remote --heads origin $branch_name | grep -q $branch_name; then
    return 0
  fi
  return 1
}

# Check if REVIEW_BRANCH now exists in remote
FINAL_REVIEW_BRANCH="$REVIEW_BRANCH"
if remote_branch_exists "$REVIEW_BRANCH"; then
  echo "Warning: Review branch $REVIEW_BRANCH now exists in remote (created by another team member)"
  echo "Finding next available branch number..."

  # Extract base name and current number
  BASE_NAME="${REVIEW_BRANCH%_review_*}"
  CURRENT_NUM="${REVIEW_BRANCH##*_review_}"

  # Find next available number
  n=$((CURRENT_NUM + 1))
  while remote_branch_exists "${BASE_NAME}_review_${n}"; do
    n=$((n + 1))
  done

  FINAL_REVIEW_BRANCH="${BASE_NAME}_review_${n}"
  echo "Using new review branch: $FINAL_REVIEW_BRANCH"

  # Create and checkout the new branch
  git checkout -b "$FINAL_REVIEW_BRANCH"
fi

echo "FINAL_REVIEW_BRANCH=$FINAL_REVIEW_BRANCH"
