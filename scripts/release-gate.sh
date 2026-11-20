#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml, images.yml,
# console-release.yml, mobile-release.yml): the tag is a strict vX.Y.Z -- or
# the mobile app's strict mobile-vX.Y.Z, the one stream this repository's tags
# carry a prefix on (docs/release.md, "Mobile releases") -- its commit is
# reachable from origin/main (or, for a patch release vX.Y.Z with Z of 1 or more,
# from the maintenance branch release/X.Y, which must descend from the vX.Y.0
# tag that is itself on main), and every check in `names` below succeeded on
# that commit, read from a **full** `ci` run: `test`, `terraform` and `rules`
# come from the check suite of the run of .github/workflows/ci.yml titled
# `ci (full)` (or `ci`, the title a ci.yml without a run-name: gives its runs),
# never from a `ci (selected)` run's (R1, D3040). A commit whose `ci` runs are
# none of them full is refused at once, with the command that dispatches one.
# Needs: a full-history checkout, GH_TOKEN (checks: read; actions: read too on a
# private repository, whose runs API a token without it cannot read -- this
# repository is public, and listed them with no credential at all when CS7
# measured it, 2026-11-20), GITHUB_SHA, GITHUB_REPOSITORY.
# Usage: scripts/release-gate.sh vX.Y.Z|mobile-vX.Y.Z
# Optional: GATE_WAIT_SECONDS=N (default 0: no waiting, as in the workflows)
# polls every GATE_POLL_SECONDS (default 30) while a check is missing, still
# running or cancelled, so scripts/release.sh can run it right after a merge,
# before CI has finished. A conclusive failure always fails at once.
# When a check is missing or running on the commit, the checks of the merged
# pull request's head count if its tree is identical (see identical_tree_checks);
# this applies to the tag workflows too, with the same rules.
set -eu
# A dropped connection to GitHub while polling is retried, a bounded number of
# times, rather than failing the gate (scripts/retry.sh).
. "$(dirname "$0")/retry.sh"
tag=${1:-}
# The version under the stream's own prefix, and the whole tag when there is
# none: the mobile app is its own artifact with its own tag (D125), and its
# `mobile-` prefix is stripped here so that both streams are held to the one
# shape below. A tag that is not one of the two -- `mobile-1.0.0`, which carries
# no `v`, or `mobile-v01.0.0`, whose version is not strict -- is refused by the
# same check as `vtest`, and nothing else about the gate changes: the required
# checks are the same set, read from the same commit.
version=${tag#mobile-}
if ! [[ $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::tag '$tag' is not a strict vX.Y.Z or mobile-vX.Y.Z release tag" >&2
  exit 1
fi
# The maintenance branch a patch release was resolved to, set below and read
# where the dispatch hint picks the ref it names; empty for a tag on main.
maintenance_branch=""
retry_read git fetch --quiet origin main
if ! git merge-base --is-ancestor "$GITHUB_SHA" refs/remotes/origin/main; then
  # The one other place a release commit may be: a maintenance branch, for a
  # patch release of a line that was itself released from main (a hotfix, docs/
  # release.md "Hotfix releases"). It is narrow on purpose, every condition
  # below must hold, and each is a refusal and never a skip:
  #   - the tag is a CLI tag vX.Y.Z with Z of 1 or more (the app's mobile- tags
  #     and a vX.Y.0 stay main-only: a minor's first release is cut from main),
  #   - the branch is the line's own, release/X.Y, and the commit is on it,
  #   - vX.Y.0 exists and is reachable from main, so the line was gated on main
  #     once and the patch cannot be the first release of anything,
  #   - the commit descends from that vX.Y.0, so the branch is the line's
  #     history and not an unrelated one named like it,
  #   - and main's own plugin version is the tag's or newer, so the patch
  #     release's plugin bump landed on main too (docs/release.md "Hotfix
  #     releases") and main never publishes a plugin older than a released tag.
  # The required checks below are read from the commit itself, on any branch, so
  # CI dispatched on the maintenance branch is what vouches for the commit.
  maintenance_ok=""
  if [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.([1-9][0-9]*)$ ]]; then
    line="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
    branch="release/$line"
    # Fully qualified refs throughout: a tag named like a remote-tracking branch
    # (`origin/release/1.2`) would otherwise be resolved first, and a commit that
    # is not on the branch could pass for one that is.
    if retry_read git fetch --quiet origin "refs/heads/$branch:refs/remotes/origin/$branch" \
      && git merge-base --is-ancestor "$GITHUB_SHA" "refs/remotes/origin/$branch" \
      && first=$(git rev-parse --quiet --verify "refs/tags/v$line.0^{commit}") \
      && git merge-base --is-ancestor "$first" refs/remotes/origin/main \
      && git merge-base --is-ancestor "$first" "$GITHUB_SHA"; then
      # A patch of an older line would be published as the newest release (GitHub
      # marks a newly published release latest and the Homebrew cask follows the
      # tag), so only the newest line may be patched this way.
      newest=$(git tag -l 'v*' | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' | sort -V | tail -n1)
      if [ "${newest%.*}" != "v$line" ]; then
        echo "::error::$tag is a patch of the line v$line, but a newer line exists ($newest): a maintenance release would be published as the latest release; only the newest line may be patched from its maintenance branch" >&2
        exit 1
      fi
      # The patch release's plugin bump must be on main as well (docs/release.md
      # "Hotfix releases"): the branch is never merged back, so a plugin version
      # main does not carry outlives the release, and a main behind a published
      # tag is a marketplace that serves a plugin older than the binary that
      # installed it (0.21.1, 2026-11-09). A version main already carries counts
      # whatever the line: only never older is required.
      main_plugin=$(git show refs/remotes/origin/main:plugin/.claude-plugin/plugin.json 2>/dev/null \
        | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
      if [[ ! $main_plugin =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
        || [ "$(printf '%s\n%s\n' "$main_plugin" "${tag#v}" | sort -V | tail -n1)" != "$main_plugin" ]; then
        echo "::error::main's plugin/.claude-plugin/plugin.json says '$main_plugin' and $tag is ${tag#v}: a patch release's plugin bump is made on main and cherry-picked onto release/$line, like the fix and the gate change (docs/release.md, \"Hotfix releases\"), so that main and the released tag never disagree about the plugin" >&2
        exit 1
      fi
      maintenance_ok=1
      maintenance_branch=$branch
      echo "$tag ($GITHUB_SHA) is on the maintenance branch $branch, which descends from v$line.0 on main"
    fi
  fi
  if [ -z "$maintenance_ok" ]; then
    echo "::error::$tag ($GITHUB_SHA) is not on main; tag a commit that is reachable from main (a patch release vX.Y.Z may instead be tagged on release/X.Y when vX.Y.0 exists on main and the commit descends from it)" >&2
    exit 1
  fi
fi
wait_seconds=${GATE_WAIT_SECONDS:-0}
poll_seconds=${GATE_POLL_SECONDS:-30}
deadline=$(($(date +%s) + wait_seconds))
main_tip=$(git rev-parse refs/remotes/origin/main)
# names is the required-check set: what must be success on the tag's commit
# and, in the identical_tree_checks fallback, on the pull request head whose
# tree matches. An array so every loop keeps a name whole.
#
# The `#   ` lines below are the base set in prose, and
# TestReleaseGateRequiresTheConsoleCheck reads both and asserts they name the
# same set: keep them in step, one name per line, with no prose between the
# last of them and the assignment (that adjacency is what the test reads).
#   test
#   terraform
#   rules
names=("test" "terraform" "rules")
# suite_names is the part of that set a full `ci` run owns: the three jobs of
# .github/workflows/ci.yml, whose check suite carries them. They are read from
# the run's suite and never from the commit's own check runs, which is what
# makes D3040 hold -- a `ci (selected)` run answers a different check suite and
# is not read at all (R1). The console legs appended below come from
# console-tests.yml, which no `ci` run owns, and keep the older reading.
suite_names=("${names[@]}")
# A CLI or mobile tag must not wait on, or be refused for, console tests: the
# console releases on its own stream (console-vX.Y.Z). Only console-release.yml
# sets GATE_REQUIRE_CONSOLE=1, which adds the two matrix legs GitHub names for
# the console job; the bare `console` is a check run on no commit.
if [ "${GATE_REQUIRE_CONSOLE:-}" = 1 ]; then
  names+=("console (ubuntu-latest)" "console (macos-latest)")
fi

# full_run_command is the command that puts a full run on the commit the gate is
# reading, printed when every `ci` run on it is a selected one. A dispatch is by
# ref, so the command names one that is at the commit: main for main's tip, the
# maintenance branch for a patch release the gate resolved above, and otherwise
# a ref of the person's own -- the gate invents none.
if [ "$GITHUB_SHA" = "$main_tip" ]; then
  dispatch_ref=main
elif [ -n "$maintenance_branch" ]; then
  dispatch_ref=$maintenance_branch
else
  dispatch_ref="<a ref pointing at $GITHUB_SHA>"
fi
full_run_command="gh workflow run ci --ref $dispatch_ref -f full=true"

# check_runs_jq NAME is the --jq every reading of a required check name asks the
# API with: the check runs named NAME that the GitHub Actions app owns --
# a check of that name from another app must never satisfy the gate -- reduced
# to one state. No runs at all is missing; every one of them cancelled is
# cancelled (a superseded run is not a failure); any other run that completed
# without success is failed; any still going is pending; and only then success.
check_runs_jq() {
  printf '%s' "[.check_runs[] | select(.name == \"$1\" and .app.slug == \"github-actions\")] as \$r | if (\$r | length) == 0 then \"missing\" else [\$r[] | select(.conclusion != \"cancelled\")] as \$live | if (\$live | length) == 0 then \"cancelled\" elif any(\$live[]; .status == \"completed\" and .conclusion != \"success\") then \"failed\" elif any(\$live[]; .status != \"completed\") then \"pending\" else \"success\" end end"
}

# commit_check_state SHA NAME prints missing | cancelled | failed | pending |
# success for NAME over the commit's own check runs. This is the reading the
# console legs keep: they belong to console-tests.yml, whose runs are not `ci`
# runs and have no suite for the gate to ask for.
commit_check_state() {
  retry_read gh api "repos/$GITHUB_REPOSITORY/commits/$1/check-runs?per_page=100" --jq "$(check_runs_jq "$2")"
}

# ci_run_rows SHA prints one tab-separated line per run of the `ci` workflow
# file on SHA: the run's check suite id and its display title. `.path` is what
# makes a run a `ci` run: another workflow that copied the title must not vouch
# for the commit, and the title is what says whether the run was full (R1).
ci_run_rows() {
  retry_read gh api "repos/$GITHUB_REPOSITORY/actions/runs?head_sha=$1&per_page=100" \
    --jq '.workflow_runs[] | select(.path == ".github/workflows/ci.yml") | [.check_suite_id, .display_title] | @tsv'
}

# full_run_title TITLE succeeds for the two titles that name a full run:
# `ci (full)`, which run-name: gives a ci.yml that has one, and a bare `ci`,
# which is what every run on a commit whose ci.yml predates run-name: is
# titled. A `ci (selected)` run is neither, and vouches for nothing here.
full_run_title() {
  case "$1" in
    "ci (full)" | ci) return 0 ;;
    *) return 1 ;;
  esac
}

# full_run_suites SHA prints, one per line, the check suite ids of the full
# `ci` runs on SHA, in the order the API lists them.
full_run_suites() {
  rows=$(ci_run_rows "$1") || return 1
  while IFS=$'\t' read -r suite title; do
    [ -n "$suite" ] || continue
    if full_run_title "$title"; then
      printf '%s\n' "$suite"
    fi
  done <<<"$rows"
}

# no_full_ci_run SHA succeeds when SHA has `ci` runs and not one of them is a
# full run: every run that could vouch for the commit is a selected one (or one
# whose title names neither variant), and no amount of waiting changes that.
no_full_ci_run() {
  rows=$(ci_run_rows "$1") || return 1
  [ -n "$rows" ] || return 1
  while IFS=$'\t' read -r suite title; do
    [ -n "$suite" ] || continue
    if full_run_title "$title"; then
      return 1
    fi
  done <<<"$rows"
  return 0
}

# merge_state A B prints what two readings of one check name say together: a
# failure anywhere is a failure, a running check anywhere is a wait, a name that
# only ever ran cancelled is cancelled. It is the rule one reading always
# applied, carried across the suites of several full runs on one commit (a
# nightly that landed on a release's tip, a re-dispatch).
merge_state() {
  case "$1$2" in
    *failed*) printf failed ;;
    *pending*) printf pending ;;
    *success*) printf success ;;
    *cancelled*) printf cancelled ;;
    *) printf missing ;;
  esac
}

# suite_check_state SHA NAME prints missing | cancelled | failed | pending |
# success for NAME over the check suites of the full `ci` runs on SHA, with the
# same rules commit_check_state applies to one listing. missing is a commit with
# no full run at all, which is all a selected run leaves behind.
suite_check_state() {
  suites=$(full_run_suites "$1") || return 1
  [ -n "$suites" ] || { printf missing; return 0; }
  state=""
  while IFS= read -r suite; do
    [ -n "$suite" ] || continue
    one=$(retry_read gh api "repos/$GITHUB_REPOSITORY/check-suites/$suite/check-runs?per_page=100" --jq "$(check_runs_jq "$2")") || return 1
    state=$(merge_state "$state" "$one")
  done <<<"$suites"
  printf '%s' "$state"
}

# check_state SHA NAME prints missing | cancelled | failed | pending | success
# for the required check NAME: the three a `ci` run owns from that run's check
# suite, the console legs from the commit's own check runs.
check_state() {
  case " ${suite_names[*]} " in
    *" $2 "*) suite_check_state "$1" "$2" ;;
    *) commit_check_state "$1" "$2" ;;
  esac
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
#   - every check in `names` (the same set the target is held to, console legs included when
#     GATE_REQUIRE_CONSOLE=1) is success on it, with the same strictness as on
#     the target (a failed, cancelled, still running or missing run, or a
#     check from another app, is never accepted) -- read the same way too, so
#     the head's `test`, `terraform` and `rules` must come from a full run of
#     its own and a selected one there vouches for nothing either (R1).
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
  # one "$name $state" string: a name may carry a space, so "${pending% *}"
  # and "${pending#* }" would split it in the wrong place.
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
  # A commit with `ci` runs but no full one can never satisfy the gate: the
  # checks it requires are read from a full run's check suite, and only a
  # dispatch makes a full run (D3040). Polling to the deadline for one that does
  # not exist only spends the wait, so the gate refuses now and prints the
  # command that puts the full run there.
  if no_full_ci_run "$GITHUB_SHA"; then
    echo "::error::required check '$pending_name' is $pending_state on $GITHUB_SHA: no ci run on that commit is a full one (its titles are neither 'ci (full)' nor 'ci'), and the gate requires the checks of a full run" >&2
    echo "dispatch a full run and re-run this job: $full_run_command" >&2
    exit 1
  fi
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
