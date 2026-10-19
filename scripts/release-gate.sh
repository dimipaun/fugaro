#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml and images.yml):
# the tag is a strict vX.Y.Z, its commit is reachable from origin/main, and
# every GitHub Actions check in `names` below succeeded on that commit.
# Needs: a full-history checkout, GH_TOKEN (checks: read), GITHUB_SHA,
# GITHUB_REPOSITORY. Usage: scripts/release-gate.sh vX.Y.Z
# Optional: GATE_WAIT_SECONDS=N (default 0: no waiting, as in the workflows)
# polls every GATE_POLL_SECONDS (default 30) while a check is missing, still
# running or cancelled, so scripts/release.sh can run it right after a merge,
# before CI has finished. A conclusive failure always fails at once.
# When a check is missing or running on the commit, the checks of the merged
# pull request's head count if its tree is identical (see identical_tree_checks);
# this applies to the tag workflows too, with the same rules.
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
# names is the required-check set: what must be success on the tag's commit
# and, in the identical_tree_checks fallback, on the pull request head whose
# tree matches. An array, not a whitespace-split string, because two of the
# names carry a space: the console job in .github/workflows/console-tests.yml
# is a matrix over operating systems, and GitHub names each matrix leg's check
# run "<job> (<values>)". The bare `console` is a check run on no commit, so
# requiring it would make the gate look for a check it can never find -- and a
# missing check is not a success -- refusing every tag in this repository, the
# CLI-only releases included. Both legs are required, and check_state's own
# rule (every completed run of the name must be successful) is what makes the
# two platforms one requirement rather than two.
# The console legs exist as check runs on `main` only once the `console`
# branch (which brings console-tests.yml with this list) is merged into `main`
# and a push has run that workflow: do not run this gate on a commit before it.
#
# The five `#   ` lines below are this list in prose, and
# TestReleaseGateRequiresTheConsoleCheck reads both and asserts they name the
# same set: keep them in step, one name per line, with no prose between the
# last of them and the assignment (that adjacency is what the test reads).
#   test
#   terraform
#   rules
#   console (ubuntu-latest)
#   console (macos-latest)
names=("test" "terraform" "rules" "console (ubuntu-latest)" "console (macos-latest)")

# check_state SHA NAME prints missing | cancelled | failed | pending | success
# for the GitHub Actions runs named NAME on SHA. Only the GitHub Actions app
# counts: a third-party check named like a required one must not satisfy the
# gate. Cancelled runs (superseded by a newer one) are ignored while another
# run of the name exists; every other run must have completed successfully.
check_state() {
  gh api "repos/$GITHUB_REPOSITORY/commits/$1/check-runs?per_page=100" \
    --jq "[.check_runs[] | select(.name == \"$2\" and .app.slug == \"github-actions\")] as \$r | if (\$r | length) == 0 then \"missing\" else [\$r[] | select(.conclusion != \"cancelled\")] as \$live | if (\$live | length) == 0 then \"cancelled\" elif any(\$live[]; .status == \"completed\" and .conclusion != \"success\") then \"failed\" elif any(\$live[]; .status != \"completed\") then \"pending\" else \"success\" end end"
}

# lookup_failed WHICH notes, once per run on stderr, that an identical-tree
# lookup call failed. It never changes pass/fail: the gate then just keeps
# waiting for the target's own checks, as it did before the shortcut existed.
noted=""
lookup_failed() {
  [ -n "$noted" ] || echo "identical-tree lookup unavailable ($1)" >&2
  noted=1
}

# identical_tree_checks succeeds and sets $used to the head sha of a pull
# request whose checks may stand in for the target's, or fails. A squash merge
# makes a new commit, so CI starts from nothing on it even when the pull
# request head (which carries the same tree) already passed. The target's own
# runs are never overridden (the caller only asks when none failed); a
# candidate must satisfy every rule, each checked from the API, not assumed:
#   - it is the head of a pull request that is merged AND whose merge commit
#     is exactly the target, so an unrelated commit with the same tree on some
#     other branch (or an open or closed-unmerged pull request) cannot vouch;
#   - its git tree id equals the target's tree id: identical content to the
#     target. The pull request's checks ran on its head merged with the base
#     of that moment (pull_request jobs check out the merge ref), not on the
#     head alone; that is accepted because the tree equals the merged result
#     that became the target;
#   - every check in `names` (the same set the target is held to, the console
#     matrix legs included) is success on it, with the same strictness as on
#     the target (a failed, cancelled, still running or missing run, or a
#     check from another app, is never accepted).
# The commits/SHA/pulls listing needs no token on a public repository, so the
# tag workflows may use this too (same rules); on a private repository without
# pull-requests: read it fails, is noted, and the old behaviour remains. Only
# the first page (per_page=100) of associated pull requests is read: a commit
# is merged by one pull request in practice, and more is never needed.
identical_tree_checks() {
  used=""
  target_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$GITHUB_SHA" --jq .tree.sha 2>/dev/null) || { lookup_failed "git commit $GITHUB_SHA"; return 1; }
  [ -n "$target_tree" ] || { lookup_failed "git commit $GITHUB_SHA"; return 1; }
  # "-" stands in for a null so the tab-separated fields never collapse.
  prs=$(gh api "repos/$GITHUB_REPOSITORY/commits/$GITHUB_SHA/pulls?per_page=100" \
    --jq '.[] | [(.merged_at // "-"), (.merge_commit_sha // "-"), .head.sha] | @tsv' 2>/dev/null) || { lookup_failed "pulls of $GITHUB_SHA"; return 1; }
  while IFS=$'\t' read -r merged_at merge_sha head_sha; do
    [ -n "$head_sha" ] || continue
    [ "$merged_at" != "-" ] || continue
    [ "$merge_sha" = "$GITHUB_SHA" ] || continue
    head_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$head_sha" --jq .tree.sha 2>/dev/null) || { lookup_failed "git commit $head_sha"; continue; }
    [ "$head_tree" = "$target_tree" ] || continue
    ok=1
    for name in "${names[@]}"; do
      if ! st=$(check_state "$head_sha" "$name" 2>/dev/null); then
        lookup_failed "check-runs of $head_sha"
        ok=""
        break
      fi
      [ "$st" = success ] || { ok=""; break; }
    done
    if [ -n "$ok" ]; then
      used=$head_sha
      return 0
    fi
  done <<<"$prs"
  return 1
}

while :; do
  # The first missing/running check is remembered as two variables rather than
  # one "$name $state" string: a name may carry a space (the console matrix
  # legs), so "${pending% *}" and "${pending#* }" would split it in the wrong
  # place and print a name the workflow never reports.
  pending_name=""
  pending_state=""
  for name in "${names[@]}"; do
    state=$(check_state "$GITHUB_SHA" "$name")
    case "$state" in
      success) ;;
      missing | pending | cancelled)
        [ -n "$pending_name" ] || { pending_name="$name"; pending_state="$state"; }
        ;;
      *)
        echo "::error::required check '$name' is $state on $GITHUB_SHA" >&2
        exit 1
        ;;
    esac
  done
  [ -n "$pending_name" ] || break
  # No conclusive failure on the target, only missing or running checks: a
  # release bump merged by squash has the tree its (green) pull request had.
  if identical_tree_checks; then
    echo "using the checks of $used (identical tree): ${names[*]} succeeded on the head of the pull request merged as $GITHUB_SHA"
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error::required check '$pending_name' is $pending_state on $GITHUB_SHA (if CI is still running, re-run this job when it is green)" >&2
    exit 1
  fi
  echo "still waiting: check '$pending_name' is $pending_state on $GITHUB_SHA"
  sleep "$poll_seconds"
done
echo "gate passed for $tag at $GITHUB_SHA"
