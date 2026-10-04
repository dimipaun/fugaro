#!/usr/bin/env bash
# Cuts a release: bumps the plugin through a PR, waits for it to merge, then
# tags the merged commit. See docs/release.md ("Cutting a release" section)
# for the full picture; this script automates every step of it but the tag
# push's confirmation.
#
#   scripts/release.sh X.Y.Z [--yes] [--dry-run] [--timeout-minutes N]
#
# --yes skips only the final "create and push the tag?" prompt; every
# precondition below still runs. --dry-run stops after printing the plan for
# step 2 (branch/PR), creating nothing. --timeout-minutes (default 45) bounds
# how long step 3 waits for the release PR to merge.
#
# Re-running after an interruption is safe: the script re-derives its state
# from git and gh (an existing release/vX.Y.Z PR, merged or open, is found
# and continued) rather than keeping any state of its own. It never force-
# pushes, never pushes to main directly, and never deletes a tag.
set -eu

repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"

yes=0
dry_run=0
timeout_minutes=45
version=""
while [ $# -gt 0 ]; do
  case "$1" in
    --yes) yes=1 ;;
    --dry-run) dry_run=1 ;;
    --timeout-minutes)
      shift
      timeout_minutes=${1:-}
      ;;
    -*)
      echo "unknown flag: $1" >&2
      exit 2
      ;;
    *)
      if [ -n "$version" ]; then
        echo "unexpected argument: $1" >&2
        exit 2
      fi
      version=$1
      ;;
  esac
  shift
done
if [ -z "$version" ]; then
  echo "usage: $0 X.Y.Z [--yes] [--dry-run] [--timeout-minutes N]" >&2
  exit 2
fi
case "$timeout_minutes" in
  ''|*[!0-9]*)
    echo "--timeout-minutes wants a whole number of minutes, got '$timeout_minutes'" >&2
    exit 2
    ;;
esac

on_interrupt() {
  echo "
interrupted; nothing was torn down. Re-run \"scripts/release.sh $version\" to resume: it finds an existing release branch or pull request and continues from there, and refuses outright if the tag already exists." >&2
  exit 130
}
trap on_interrupt INT

# version_gt exits 0 (true) when strict-semver $1 is greater than $2.
version_gt() {
  awk -v a="$1" -v b="$2" '
    BEGIN {
      split(a, A, ".")
      split(b, B, ".")
      for (i = 1; i <= 3; i++) {
        if (A[i] + 0 > B[i] + 0) exit 0
        if (A[i] + 0 < B[i] + 0) exit 1
      }
      exit 1
    }'
}

# latest_released_version prints the highest existing vX.Y.Z tag's version,
# or nothing when there is none. Sorted via a zero-padded numeric key so it
# needs only a plain `sort`, not GNU sort -V (this runs on a developer's
# machine, not just CI).
latest_released_version() {
  git tag -l 'v[0-9]*.[0-9]*.[0-9]*' | sed 's/^v//' | awk -F. '{printf "%020d%020d%020d %s\n", $1, $2, $3, $0}' | sort | tail -1 | awk '{print $2}'
}

# 1. Preconditions, checked in order; the first failure stops the script.
if ! printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "version '$version' is not strict SemVer X.Y.Z (no leading v, no pre-release suffix, no leading zeros in a component)" >&2
  exit 1
fi
tag="v$version"
branch="release/$tag"

if [ -n "$(git status --porcelain)" ]; then
  echo "the working tree is not clean; commit or stash first" >&2
  exit 1
fi

current_branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$current_branch" != main ]; then
  echo "the current branch is '$current_branch', not main; switch to main first" >&2
  exit 1
fi

git fetch --quiet origin main --tags --prune
local_main=$(git rev-parse main)
origin_main=$(git rev-parse origin/main)
if [ "$local_main" != "$origin_main" ]; then
  echo "main ($local_main) is not origin/main ($origin_main); pull or push first" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is not installed; see https://cli.github.com" >&2
  exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
  echo "gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner) || {
  echo "'gh repo view' failed; run this from inside the fugaro checkout" >&2
  exit 1
}

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "tag $tag already exists; a release is never re-tagged (ship the fix as the next patch instead, see docs/release.md#rolling-back)" >&2
  exit 1
fi

latest=$(latest_released_version)
if [ -n "$latest" ] && ! version_gt "$version" "$latest"; then
  echo "version $version is not greater than the latest tag v$latest" >&2
  exit 1
fi

echo "checking that main's checks are green at $local_main..."
GITHUB_SHA="$local_main" GITHUB_REPOSITORY="$repo" scripts/release-gate.sh "$tag"

# 2. Branch, bump, PR (skipped by --dry-run; resumed from an existing PR).
if [ "$dry_run" = 1 ]; then
  cat <<EOF

Dry run for $tag: every precondition above passed; nothing was changed.
Without --dry-run this would:
  2. create $branch from main, bump the plugin to $version (scripts/bump-plugin-version.sh), commit "release: $tag", push it, open a PR to main, and enable squash auto-merge
  3. wait up to ${timeout_minutes}m for that PR to merge
  4. verify the merge commit on main passes the release gate
  5. ask to create and push the tag $tag
EOF
  exit 0
fi

commit_body="Bump the plugin version and every skill header to $version (scripts/bump-plugin-version.sh) so the release gate's version check passes once this merges to main.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
pr_body="Bumps plugin/.claude-plugin/plugin.json and every skill header to $version (scripts/bump-plugin-version.sh), per docs/release.md. Opened by scripts/release.sh; auto-merge (squash) is enabled."

pr_number=$(gh pr list --base main --head "$branch" --state all --limit 1 --json number --jq '.[0].number // empty')
pr_url=""
if [ -n "$pr_number" ]; then
  pr_url="https://github.com/$repo/pull/$pr_number"
  echo "resuming existing pull request $pr_url"
else
  base_ref=""
  base_desc=""
  if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
    git fetch --quiet origin "$branch"
    base_ref="origin/$branch"
    base_desc="pushed branch $branch (no pull request yet)"
  elif git show-ref --quiet --verify "refs/heads/$branch"; then
    base_ref="refs/heads/$branch"
    base_desc="local branch $branch (not yet pushed)"
  fi

  # A branch from an earlier, interrupted run is only safe to resume if it
  # is still based on the current origin/main; otherwise (main advanced
  # since) rebuild it fresh rather than opening a PR against stale history.
  if [ -n "$base_ref" ] && [ "$(git merge-base "$base_ref" origin/main)" = "$origin_main" ]; then
    echo "resuming $base_desc"
    git switch --quiet -C "$branch" "$base_ref"
  else
    [ -n "$base_ref" ] && echo "$base_desc predates main ($origin_main); rebuilding $branch from origin/main" >&2
    git switch --quiet -C "$branch" origin/main
  fi

  if ! scripts/bump-plugin-version.sh --check "$version" >/dev/null 2>&1; then
    scripts/bump-plugin-version.sh "$version" >/dev/null
    git add -A
    git commit --quiet -m "release: $tag" -m "$commit_body"
  fi

  git push --quiet -u origin "$branch"

  pr_url=$(gh pr create --title "release: $tag" --base main --head "$branch" --body "$pr_body")
  pr_number=${pr_url##*/}
  echo "opened pull request $pr_url"
  git switch --quiet main
fi

# 3. Wait for the merge.
poll_seconds=${RELEASE_SH_POLL_SECONDS:-30}

poll_pr() {
  n=$1
  deadline=$(($(date +%s) + timeout_minutes * 60))
  while :; do
    state=$(gh pr view "$n" --json state --jq .state)
    case "$state" in
      MERGED)
        gh pr view "$n" --json mergeCommit --jq .mergeCommit.oid
        return 0
        ;;
      CLOSED)
        echo "pull request #$n ($pr_url) was closed without merging" >&2
        return 1
        ;;
    esac
    # Any terminal-but-not-ok state, not just FAILURE: a cancelled or
    # timed-out required check otherwise keeps this polling silently until
    # --timeout-minutes, instead of stopping right away and naming it.
    failed=$(gh pr checks "$n" --json name,state --jq '([.[] | select(.state == "FAILURE" or .state == "ERROR" or .state == "CANCELLED" or .state == "TIMED_OUT" or .state == "ACTION_REQUIRED" or .state == "STARTUP_FAILURE" or .state == "STALE")][0].name) // empty' 2>/dev/null || true)
    if [ -n "$failed" ]; then
      echo "check '$failed' failed on pull request #$n ($pr_url)" >&2
      return 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "timed out after ${timeout_minutes}m waiting for pull request #$n ($pr_url) to merge" >&2
      return 1
    fi
    sleep "$poll_seconds"
  done
}

pr_state=$(gh pr view "$pr_number" --json state --jq .state)
if [ "$pr_state" = MERGED ]; then
  merge_sha=$(gh pr view "$pr_number" --json mergeCommit --jq .mergeCommit.oid)
else
  gh pr merge --auto --squash "$pr_number" >/dev/null
  echo "waiting for $pr_url to merge (checking every ${poll_seconds}s, giving up after ${timeout_minutes}m)..."
  if ! merge_sha=$(poll_pr "$pr_number"); then
    exit 1
  fi
fi

# 4. Verify the merged commit on main.
echo "pull request #$pr_number merged as $merge_sha; verifying main"
git switch --quiet main
git pull --quiet --ff-only origin main
head_sha=$(git rev-parse HEAD)
if [ "$head_sha" != "$merge_sha" ]; then
  echo "HEAD ($head_sha) is not the release merge commit ($merge_sha); something else landed on main first, inspect before tagging" >&2
  exit 1
fi
scripts/bump-plugin-version.sh --check "$version"
GITHUB_SHA="$head_sha" GITHUB_REPOSITORY="$repo" scripts/release-gate.sh "$tag"

# 5. Summary and confirmation.
subject=$(git log -1 --format=%s "$head_sha")
cat <<EOF

Release summary
  tag:     $tag
  commit:  $head_sha ($subject)
  checks:  test, terraform and rules all succeeded on $head_sha (scripts/release-gate.sh, above)

A pushed tag is permanent: the Go module proxy and the public ghcr.io images
cache vX.Y.Z forever, so it is never moved or re-cut (docs/release.md#rolling-back).
EOF

if [ "$yes" != 1 ]; then
  printf 'Create and push tag %s? [y/N] ' "$tag"
  read -r answer || answer=""
  case "$answer" in
    y | Y | yes | YES) ;;
    *)
      echo "aborted: tag not created" >&2
      exit 1
      ;;
  esac
fi

# 6. Tag and push.
git tag -a "$tag" -m "Fugaro $tag" "$head_sha"
git push origin "$tag"

cat <<EOF
tag $tag pushed. Watch:
  https://github.com/$repo/actions/workflows/release.yml
  https://github.com/$repo/actions/workflows/images.yml

Post-release checklist (docs/release.md):
  - verify-public: each image must be anonymously pullable with a linux/amd64 build (images/verify-public.sh)
  - the first release of a new image package: make it public once (package settings, Danger Zone, Change visibility)
  - verify the release end to end (docs/release.md#verifying-a-release)
EOF
