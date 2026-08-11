#!/usr/bin/env bash
# Find first available review branch number (checks both local and remote)
# Usage: ./find-available-branch.sh <current_branch>
# Outputs: REVIEW_BRANCH

set -e

CURRENT_BRANCH="$1"

if [ -z "$CURRENT_BRANCH" ]; then
  echo "Error: CURRENT_BRANCH argument required" >&2
  exit 1
fi

# Function to check if branch exists (local or remote)
branch_exists() {
  local branch_name=$1
  # Check local branches
  if git show-ref --verify --quiet refs/heads/$branch_name; then
    return 0
  fi
  # Check remote branches
  if git ls-remote --heads origin $branch_name | grep -q $branch_name; then
    return 0
  fi
  return 1
}

# Find first available review branch number
n=1
while branch_exists "${CURRENT_BRANCH}_review_${n}"; do
  n=$((n + 1))
done

REVIEW_BRANCH="${CURRENT_BRANCH}_review_${n}"
echo "REVIEW_BRANCH=$REVIEW_BRANCH"
