#!/usr/bin/env bash
# Cuts a release: bumps the plugin through a PR, waits for it to merge, then
# tags the merged commit. See docs/release.md ("Cutting a release" section)
# for the full picture; this script automates every step of it, the tag
# push included: the release gate is the safeguard, there is no prompt.
#
#   scripts/release.sh X.Y.Z [--yes] [--dry-run] [--timeout-minutes N]
#
# --yes is accepted and does nothing (there is no tag prompt any more); every
# precondition below still runs. --dry-run stops after printing the plan for
# step 2 (branch/PR), creating nothing. --timeout-minutes (default 45, a
# whole number >= 1) bounds each of the two waits: step 3 (the release PR
# merging) and step 4 (CI's test/terraform/rules finishing on the merge
# commit).
#
# Re-running after an interruption is safe: the script re-derives its state
# from git and gh (an existing release/vX.Y.Z PR, merged or open, is found
# and continued) rather than keeping any state of its own. It never force-
# pushes, never pushes to main directly, and never deletes a tag. However it
# exits, it leaves the checkout on main.
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
      if [ $# -lt 2 ]; then
        echo "--timeout-minutes needs a value (a whole number of minutes, at least 1)" >&2
        exit 2
      fi
      shift
      timeout_minutes=$1
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
if ! [[ $timeout_minutes =~ ^[1-9][0-9]*$ ]]; then
  echo "--timeout-minutes wants a whole number of minutes, at least 1; got '$timeout_minutes'" >&2
  exit 2
fi

# restore_main runs on every exit: whatever the script did on the release
# branch, the checkout ends on main so a re-run starts from the state its
# own preconditions demand. Installed only once the "on main" precondition
# has passed, so it never moves a user who started elsewhere.
restore_main() {
  cur=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  if [ -n "$cur" ] && [ "$cur" != main ]; then
    git switch --quiet --discard-changes main >/dev/null 2>&1 ||
      echo "could not switch back to main; run: git switch main" >&2
  fi
}
on_signal() {
  echo "
interrupted; nothing was torn down. Re-run \"scripts/release.sh $version\" to resume: it finds an existing release branch or pull request and continues from there, and refuses outright if the tag already exists on origin." >&2
  exit 130
}

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

# latest_released_version prints the highest existing strict vX.Y.Z tag's
# version (other than the one being released; -rc and four-part tags do not
# count), or nothing when there is none. Sorted via a zero-padded numeric key
# so it needs only a plain `sort`, not GNU sort -V (this runs on a
# developer's machine, not just CI).
latest_released_version() {
  git tag -l 'v*' | grep -E '^v(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){2}$' | grep -vxF "$tag" | sed 's/^v//' | awk -F. '{printf "%020d%020d%020d %s\n", $1, $2, $3, $0}' | sort | tail -1 | awk '{print $2}'
}

poll_seconds=${RELEASE_SH_POLL_SECONDS:-30}
# RELEASE_SH_TIMEOUT_SECONDS exists for the tests only (--timeout-minutes has
# a one-minute floor).
timeout_seconds=${RELEASE_SH_TIMEOUT_SECONDS:-$((timeout_minutes * 60))}

# 1. Preconditions, checked in order; the first failure stops the script.
if ! [[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
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
trap restore_main EXIT
trap on_signal INT TERM HUP

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
# gh resolves its repo from the checkout's remotes (or a "default" setting);
# the tag and the PR must land where origin points, so the two must agree.
origin_url=$(git remote get-url origin)
url_path=${origin_url%/}
url_path=${url_path%.git}
url_name=${url_path##*[/:]}
url_rest=${url_path%[/:]*}
url_repo="${url_rest##*[/:]}/$url_name"
if [ "$(printf '%s' "$url_repo" | tr '[:upper:]' '[:lower:]')" != "$(printf '%s' "$repo" | tr '[:upper:]' '[:lower:]')" ]; then
  echo "origin ($origin_url) is $url_repo but gh targets $repo; fix it with 'gh repo set-default' or run from the right checkout" >&2
  exit 1
fi

if [ -n "$(git ls-remote --tags origin "refs/tags/$tag")" ]; then
  echo "tag $tag already exists on origin; a release is never re-tagged (ship the fix as the next patch instead, see docs/release.md#rolling-back)" >&2
  exit 1
fi
# A tag that exists only locally (an earlier run created it and its push
# failed) is allowed: if the release PR is merged and the tag sits on the
# merge commit, step 6 pushes it. Anything else is refused there.
local_tag=0
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  local_tag=1
fi

latest=$(latest_released_version)
if [ -n "$latest" ] && ! version_gt "$version" "$latest"; then
  echo "version $version is not greater than the latest tag v$latest" >&2
  exit 1
fi

# Checks still running on main (a just-merged PR) are waited for, up to
# --timeout-minutes; a failed check fails at once.
echo "checking that main's checks are green at $local_main (waiting up to ${timeout_minutes}m for any still running)..."
GITHUB_SHA="$local_main" GITHUB_REPOSITORY="$repo" GATE_WAIT_SECONDS="$timeout_seconds" GATE_POLL_SECONDS="$poll_seconds" scripts/release-gate.sh "$tag"

# 2. Branch, bump, PR (skipped by --dry-run; resumed from an existing PR).
if [ "$dry_run" = 1 ]; then
  cat <<EOF

Dry run for $tag: every precondition above passed; nothing was changed.
Without --dry-run this would:
  2. create $branch from main, bump the plugin to $version (scripts/bump-plugin-version.sh), commit "release: $tag", push it, open a PR to main, and enable squash auto-merge
  3. wait up to ${timeout_minutes}m for that PR to merge
  4. wait up to ${timeout_minutes}m for the merge commit's test, terraform and rules checks, and require them to pass (the release gate)
  5. ask to create and push the tag $tag
EOF
  exit 0
fi

commit_body="Bump the plugin version and every skill header to $version (scripts/bump-plugin-version.sh) so the release gate's version check passes once this merges to main.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
pr_body="Bumps plugin/.claude-plugin/plugin.json and every skill header to $version (scripts/bump-plugin-version.sh), per docs/release.md. Opened by scripts/release.sh; auto-merge (squash) is enabled."

closed_message() {
  echo "pull request #$1 ($2) for $branch was closed without merging.
To retry: delete the release branch (git push origin --delete $branch, and git branch -D $branch if it exists locally), then re-run scripts/release.sh $version; a fresh pull request is opened. Or reopen the pull request on GitHub and re-run." >&2
}

pr_line=$(gh pr list --base main --head "$branch" --state all --limit 1 --json number,state --jq '.[0] | select(.) | "\(.number) \(.state)"')
pr_number=""
pr_listed_state=""
if [ -n "$pr_line" ]; then
  pr_number=${pr_line%% *}
  pr_listed_state=${pr_line#* }
fi
remote_branch=0
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  remote_branch=1
fi
if [ "$pr_listed_state" = CLOSED ]; then
  if [ "$remote_branch" = 1 ]; then
    closed_message "$pr_number" "https://github.com/$repo/pull/$pr_number"
    exit 1
  fi
  # Closed and its branch deleted: that is the documented recovery, start over.
  pr_number=""
fi
if [ "$local_tag" = 1 ] && [ "$pr_listed_state" != MERGED ]; then
  echo "tag $tag already exists locally but the release pull request is not merged; delete the local tag (git tag -d $tag) and re-run, or merge the pull request first" >&2
  exit 1
fi

pr_url=""
if [ -n "$pr_number" ]; then
  pr_url="https://github.com/$repo/pull/$pr_number"
  echo "resuming existing pull request $pr_url"
else
  base_ref=""
  base_desc=""
  if [ "$remote_branch" = 1 ]; then
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

  if ! git push --quiet -u origin "$branch"; then
    echo "pushing $branch failed; fix the cause (permissions, a branch rule) and re-run scripts/release.sh $version: it resumes from the local branch" >&2
    exit 1
  fi

  if ! pr_url=$(gh pr create --title "release: $tag" --base main --head "$branch" --body "$pr_body"); then
    echo "'gh pr create' failed; $branch is pushed, so re-run scripts/release.sh $version once fixed: it resumes from that branch and opens the pull request" >&2
    exit 1
  fi
  pr_number=${pr_url##*/}
  echo "opened pull request $pr_url"
  git switch --quiet main
fi

# 3. Wait for the merge.

poll_pr() {
  n=$1
  deadline=$(($(date +%s) + timeout_seconds))
  cancelled_seen=""
  while :; do
    state=$(gh pr view "$n" --json state --jq .state)
    case "$state" in
      MERGED)
        gh pr view "$n" --json mergeCommit --jq .mergeCommit.oid
        return 0
        ;;
      CLOSED)
        closed_message "$n" "$pr_url"
        return 1
        ;;
    esac
    # Any terminal-but-not-ok state, not just FAILURE: a timed-out or errored
    # required check otherwise keeps this polling silently until
    # --timeout-minutes, instead of stopping right away and naming it. A
    # CANCELLED check is retryable once (a superseded or re-triggered run is
    # the usual cause); seen again on a later poll, it is a failure.
    bad=$(gh pr checks "$n" --json name,state --jq '([.[] | select(.state == "FAILURE" or .state == "ERROR" or .state == "CANCELLED" or .state == "TIMED_OUT" or .state == "ACTION_REQUIRED" or .state == "STARTUP_FAILURE" or .state == "STALE")][0] | select(.) | "\(.name) \(.state)") // empty' 2>/dev/null || true)
    if [ -n "$bad" ]; then
      bad_name=${bad% *}
      bad_state=${bad##* }
      if [ "$bad_state" = CANCELLED ] && [ -z "$cancelled_seen" ]; then
        cancelled_seen=$bad_name
        echo "check '$bad_name' was cancelled on pull request #$n; waiting once for a re-run" >&2
      else
        echo "check '$bad_name' failed ($bad_state) on pull request #$n ($pr_url)" >&2
        return 1
      fi
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "timed out after ${timeout_minutes}m waiting for pull request #$n ($pr_url) to merge" >&2
      return 1
    fi
    sleep "$poll_seconds"
  done
}

pr_state=$(gh pr view "$pr_number" --json state --jq .state)
case "$pr_state" in
  MERGED)
    merge_sha=$(gh pr view "$pr_number" --json mergeCommit --jq .mergeCommit.oid)
    ;;
  CLOSED)
    # Before any 'gh pr merge': a closed pull request cannot be merged.
    closed_message "$pr_number" "$pr_url"
    exit 1
    ;;
  *)
    if ! merge_err=$(gh pr merge --auto --squash "$pr_number" 2>&1 >/dev/null); then
      echo "could not enable auto-merge on pull request #$pr_number ($pr_url): $merge_err
Auto-merge may be off for this repository. Merge the pull request yourself, then re-run scripts/release.sh $version to continue." >&2
      exit 1
    fi
    echo "waiting for $pr_url to merge (checking every ${poll_seconds}s, giving up after ${timeout_minutes}m)..."
    if ! merge_sha=$(poll_pr "$pr_number"); then
      exit 1
    fi
    ;;
esac

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
# CI starts on the merge commit only now, so its checks are usually missing
# or still running: the gate waits for them (up to --timeout-minutes), naming
# what is pending, and fails at once on a conclusive failure. It does not wait
# for CI on a squash-merge commit whose tree equals the green PR head's: it
# uses the head's checks and says so.
echo "waiting for test, terraform and rules to pass on $head_sha (up to ${timeout_minutes}m)..."
GITHUB_SHA="$head_sha" GITHUB_REPOSITORY="$repo" GATE_WAIT_SECONDS="$timeout_seconds" GATE_POLL_SECONDS="$poll_seconds" scripts/release-gate.sh "$tag"

# 5. Summary and confirmation.
subject=$(git log -1 --format=%s "$head_sha")
cat <<EOF

Release summary
  tag:     $tag
  commit:  $head_sha ($subject)
  checks:  test, terraform and rules all succeeded, on $head_sha or on the identical-tree head of the merged PR (scripts/release-gate.sh says which, above)

A pushed tag is permanent: the Go module proxy and the public ghcr.io images
cache vX.Y.Z forever, so it is never moved or re-cut (docs/release.md#rolling-back).
EOF

if [ "$local_tag" = 1 ]; then
  existing=$(git rev-parse "refs/tags/$tag^{commit}")
  if [ "$existing" != "$head_sha" ]; then
    echo "tag $tag already exists locally at $existing, not the verified merge commit $head_sha; delete it (git tag -d $tag) and re-run" >&2
    exit 1
  fi
  echo "Tag $tag already exists locally at this commit but is not on origin; pushing it."
fi

# 6. Tag and push.
if [ "$local_tag" != 1 ]; then
  git tag -a "$tag" -m "Fugaro $tag" "$head_sha"
fi
if ! git push origin "refs/tags/$tag"; then
  remote_sha=$(git ls-remote origin "refs/tags/$tag^{}" | cut -f1)
  if [ "$remote_sha" = "$head_sha" ]; then
    echo "tag $tag is already on origin at $head_sha; nothing to push."
    exit 0
  fi
  echo "pushing tag $tag failed. It exists locally at $head_sha; once the cause is fixed, push it with:
  git push origin refs/tags/$tag
or re-run scripts/release.sh $version, which pushes it." >&2
  exit 1
fi

cat <<EOF
tag $tag pushed. Watch:
  https://github.com/$repo/actions/workflows/release.yml
  https://github.com/$repo/actions/workflows/images.yml

Post-release checklist (docs/release.md):
  - verify-public: each image must be anonymously pullable with a linux/amd64 build (images/verify-public.sh)
  - the first release of a new image package: make it public once (package settings, Danger Zone, Change visibility)
  - verify the release end to end (docs/release.md#verifying-a-release)
EOF
