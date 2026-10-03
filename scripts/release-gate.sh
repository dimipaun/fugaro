#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml and images.yml):
# the tag is a strict vX.Y.Z, its commit is reachable from origin/main, and the
# GitHub Actions checks test, terraform and rules all succeeded on that commit.
# Needs: a full-history checkout, GH_TOKEN (checks: read), GITHUB_SHA,
# GITHUB_REPOSITORY. Usage: scripts/release-gate.sh vX.Y.Z
set -eu
tag=${1:-}
if ! printf '%s\n' "$tag" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "::error::tag '$tag' is not a strict vX.Y.Z release tag" >&2
  exit 1
fi
git fetch --quiet origin main
if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
  echo "::error::$tag ($GITHUB_SHA) is not on main; tag a commit that is reachable from main" >&2
  exit 1
fi
for name in test terraform rules; do
  # Only the GitHub Actions app counts: a third-party check named like a
  # required one must not satisfy the gate. Every run of that name must pass.
  concl=$(gh api "repos/$GITHUB_REPOSITORY/commits/$GITHUB_SHA/check-runs?per_page=100" \
    --jq "[.check_runs[] | select(.name == \"$name\" and .app.slug == \"github-actions\") | .conclusion] | if length == 0 then \"missing\" elif all(. == \"success\") then \"success\" else \"failed\" end")
  if [ "$concl" != success ]; then
    echo "::error::required check '$name' is $concl on $GITHUB_SHA (if CI is still running, re-run this job when it is green)" >&2
    exit 1
  fi
done
echo "gate passed for $tag at $GITHUB_SHA"
