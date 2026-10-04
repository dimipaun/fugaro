#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml and images.yml):
# the tag is a strict vX.Y.Z, its commit is reachable from origin/main, and the
# GitHub Actions checks test, terraform and rules all succeeded on that commit.
# Needs: a full-history checkout, GH_TOKEN (checks: read), GITHUB_SHA,
# GITHUB_REPOSITORY. Usage: scripts/release-gate.sh vX.Y.Z
# Optional: GATE_WAIT_SECONDS=N (default 0: no waiting, as in the workflows)
# polls every GATE_POLL_SECONDS (default 30) while a check is missing, still
# running or cancelled, so scripts/release.sh can run it right after a merge,
# before CI has finished. A conclusive failure always fails at once.
# When a check is missing or running on the commit, the checks of the merged
# pull request's head count if its tree is identical (see identical_tree_checks).
set -eu
tag=${1:-}
if ! [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::tag '$tag' is not a strict vX.Y.Z release tag" >&2
  exit 1
fi
git fetch --quiet origin main
if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
  echo "::error::$tag ($GITHUB_SHA) is not on main; tag a commit that is reachable from main" >&2
  exit 1
fi
wait_seconds=${GATE_WAIT_SECONDS:-0}
poll_seconds=${GATE_POLL_SECONDS:-30}
deadline=$(($(date +%s) + wait_seconds))
names="test terraform rules"

# check_state SHA NAME prints missing | cancelled | failed | pending | success
# for the GitHub Actions runs named NAME on SHA. Only the GitHub Actions app
# counts: a third-party check named like a required one must not satisfy the
# gate. Cancelled runs (superseded by a newer one) are ignored while another
# run of the name exists; every other run must have completed successfully.
check_state() {
  gh api "repos/$GITHUB_REPOSITORY/commits/$1/check-runs?per_page=100" \
    --jq "[.check_runs[] | select(.name == \"$2\" and .app.slug == \"github-actions\")] as \$r | if (\$r | length) == 0 then \"missing\" else [\$r[] | select(.conclusion != \"cancelled\")] as \$live | if (\$live | length) == 0 then \"cancelled\" elif any(\$live[]; .status == \"completed\" and .conclusion != \"success\") then \"failed\" elif any(\$live[]; .status != \"completed\") then \"pending\" else \"success\" end end"
}

# identical_tree_checks prints the head sha of a pull request whose checks may
# stand in for the target's, and succeeds, or fails printing nothing. A squash
# merge makes a new commit, so CI starts from nothing on it even when the pull
# request head (which carries the same files) already passed. The target's own
# runs are never overridden (the caller only asks when none failed); a
# candidate must satisfy every rule, each checked from the API, not assumed:
#   - it is the head of a pull request that is merged AND whose merge commit
#     is exactly the target, so an unrelated commit with the same tree on some
#     other branch (or an open or closed-unmerged pull request) cannot vouch;
#   - its git tree id equals the target's tree id: same files, byte for byte;
#   - test, terraform and rules are all success on it, with the same strictness
#     as on the target (a failed, cancelled, still running or missing run, or
#     a check from another app, is never accepted).
identical_tree_checks() {
  target_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$GITHUB_SHA" --jq .tree.sha 2>/dev/null) || return 1
  [ -n "$target_tree" ] || return 1
  # "-" stands in for a null so the tab-separated fields never collapse.
  prs=$(gh api "repos/$GITHUB_REPOSITORY/commits/$GITHUB_SHA/pulls?per_page=100" \
    --jq '.[] | [(.merged_at // "-"), (.merge_commit_sha // "-"), .head.sha] | @tsv' 2>/dev/null) || return 1
  while IFS=$'\t' read -r merged_at merge_sha head_sha; do
    [ -n "$head_sha" ] || continue
    [ "$merged_at" != "-" ] || continue
    [ "$merge_sha" = "$GITHUB_SHA" ] || continue
    head_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$head_sha" --jq .tree.sha 2>/dev/null) || continue
    [ "$head_tree" = "$target_tree" ] || continue
    ok=1
    for name in $names; do
      [ "$(check_state "$head_sha" "$name" 2>/dev/null)" = success ] || { ok=""; break; }
    done
    if [ -n "$ok" ]; then
      printf '%s' "$head_sha"
      return 0
    fi
  done <<<"$prs"
  return 1
}

while :; do
  pending=""
  for name in $names; do
    state=$(check_state "$GITHUB_SHA" "$name")
    case "$state" in
      success) ;;
      missing | pending | cancelled)
        [ -n "$pending" ] || pending="$name $state"
        ;;
      *)
        echo "::error::required check '$name' is $state on $GITHUB_SHA" >&2
        exit 1
        ;;
    esac
  done
  [ -n "$pending" ] || break
  # No conclusive failure on the target, only missing or running checks: a
  # release bump merged by squash has the tree its (green) pull request had.
  if used=$(identical_tree_checks); then
    echo "using the checks of $used (identical tree): test, terraform and rules succeeded on the head of the pull request merged as $GITHUB_SHA"
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error::required check '${pending% *}' is ${pending#* } on $GITHUB_SHA (if CI is still running, re-run this job when it is green)" >&2
    exit 1
  fi
  echo "still waiting: check '${pending% *}' is ${pending#* } on $GITHUB_SHA"
  sleep "$poll_seconds"
done
echo "gate passed for $tag at $GITHUB_SHA"
