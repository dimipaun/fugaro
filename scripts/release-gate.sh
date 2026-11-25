#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml, images.yml,
# console-release.yml, mobile-release.yml): the tag is a strict vX.Y.Z -- or
# the mobile app's strict mobile-vX.Y.Z, the one stream this repository's tags
# carry a prefix on (docs/release.md, "Mobile releases") -- its commit is
# reachable from origin/main (or, for a patch release vX.Y.Z with Z of 1 or more,
# from the maintenance branch release/X.Y, which must descend from the vX.Y.0
# tag that is itself on main), and every check in `names` below succeeded on
# that commit, read from a **full** `ci` run: `test`, `terraform` and `rules`
# come from the check suite of the newest run of .github/workflows/ci.yml titled
# `ci (full)` (or `ci`, the title a ci.yml without a run-name: gives its runs),
# never from a `ci (selected)` run's (R1, D3040). A commit whose `ci` runs are
# none of them full is refused at once, with the command that dispatches one.
# Needs: a full-history checkout, GH_TOKEN (checks: read; actions: read too on a
# private repository, whose runs API a token without it cannot read -- this
# repository is public, and listed them with no credential at all when CS7
# measured it, 2026-11-20), GITHUB_SHA, GITHUB_REPOSITORY.
# Usage: scripts/release-gate.sh vX.Y.Z|mobile-vX.Y.Z
# Optional: GATE_WAIT_SECONDS=N (default RELEASE_GATE_TIMEOUT_MIN, 60, minutes)
# polls every GATE_POLL_SECONDS (default 30) while a check is missing, still
# running or cancelled, so scripts/release.sh can run it right after a merge,
# before CI has finished. A conclusive failure always fails at once, and so does
# a full `ci` run that completed red: waiting out a run that is over only spends
# the wait. The wait ends with the checks that are not green and the run's URL.
# A commit with no `ci` run at all -- CI runs on no commit by itself -- gets one
# dispatched on it by the gate itself (`gh workflow run ci --ref <the ref that
# points at it> -f full=true`), once per commit, then waits for it; nothing will
# ever turn green on a commit nobody dispatched CI on, which is what a gate that
# only polls does for its whole timeout. The dispatch needs a ref this gate
# knows that points at the commit (main, or the maintenance branch of a patch
# release); a commit no such ref points at is refused at once with that command.
# GATE_DRY_RUN=1 (scripts/release.sh --dry-run) reads the checks once, prints
# what it would do and dispatches nothing.
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
# The maintenance branch a patch release was resolved to, and its tip, set below
# and read where the dispatch hint picks the ref it names; empty for a tag on
# main. A dispatch runs a workflow at a ref's tip, so only a ref whose tip is
# the commit being gated puts a run on that commit.
maintenance_branch=""
branch_tip=""
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
      && branch_tip=$(git rev-parse --verify "refs/remotes/origin/$branch^{commit}") \
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
wait_seconds=${GATE_WAIT_SECONDS:-$(( ${RELEASE_GATE_TIMEOUT_MIN:-60} * 60 ))}
poll_seconds=${GATE_POLL_SECONDS:-30}
# The wait as a message reads it: whole minutes when it is whole minutes, and
# seconds otherwise (GATE_WAIT_SECONDS is the tests' own, and a wait of 45
# seconds must not read as 0m).
if [ "$((wait_seconds % 60))" = 0 ]; then
  wait_label="$((wait_seconds / 60))m"
else
  wait_label="${wait_seconds}s"
fi
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
# reading: printed when every `ci` run on it is a selected one, and run by the
# gate itself when the commit has no `ci` run at all. A dispatch is by ref, and
# a ref's tip is the commit the run lands on, so the command names a ref this
# gate knows whose tip is the commit: main for main's tip, the maintenance
# branch for the patch release the gate resolved above, and otherwise a ref of
# the person's own -- the gate invents none, and dispatches nothing.
can_dispatch=""
if [ "$GITHUB_SHA" = "$main_tip" ]; then
  dispatch_ref=main
  can_dispatch=1
elif [ -n "$maintenance_branch" ] && [ "$GITHUB_SHA" = "$branch_tip" ]; then
  dispatch_ref=$maintenance_branch
  can_dispatch=1
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
# file on SHA: the run's number, its check suite id, its display title, then
# the run's own status, conclusion and URL. `.path` is what makes a run a `ci`
# run: another workflow that copied the title must not vouch for the commit, and
# the title is what says whether the run was full (R1). The run number is what
# orders them -- a re-dispatch is a newer run -- and the status and conclusion
# are what say a run is over and red, which is a wait that cannot end any other
# way. A conclusion the API has not answered yet is written "-": a read splits
# these fields on tabs, and tab is IFS whitespace, so an empty field collapses
# and hands the fields after it to the wrong variables.
ci_run_rows() {
  retry_read gh api "repos/$GITHUB_REPOSITORY/actions/runs?head_sha=$1&per_page=100" \
    --jq '.workflow_runs[] | select(.path == ".github/workflows/ci.yml") | [.run_number, .check_suite_id, .display_title, .status, (.conclusion // "-"), .html_url] | @tsv'
}

# run_url RUN_ROW prints the URL of a row ci_run_rows produced, and the
# workflow's own URL when the row carries none (an API answer without a URL is
# no reason to print nothing: the reader needs a door to open).
run_url() {
  local url
  url=$(printf '%s' "$1" | cut -f6)
  printf '%s' "${url:-https://github.com/$GITHUB_REPOSITORY/actions/workflows/ci.yml}"
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

# full_run_in ROWS prints the newest full `ci` run among the ROWS ci_run_rows
# produced, and nothing when they hold no full run. Newest is the highest run
# number, whatever order the API listed them in: a full run dispatched after a
# red one is the one that counts, and the commit-level check-runs listing this
# reading replaces chose the same way (its filter=latest default answers with the
# newest run of each name), so a re-dispatch does not leave a tag blocked by the
# run it replaced.
full_run_in() {
  [ -n "$1" ] || return 0
  while IFS=$'\t' read -r number suite title status conclusion url; do
    [ -n "$suite" ] || continue
    if full_run_title "$title"; then
      printf '%s\t%s\t%s\t%s\t%s\t%s' "$number" "$suite" "$title" "$status" "$conclusion" "$url"
      return 0
    fi
  done <<<"$(printf '%s\n' "$1" | sort -rn -k1,1)"
}

# full_run_suite SHA prints the check suite id of the newest full `ci` run on
# SHA, and nothing when the commit has no full run. The polling loop reads a
# commit's runs once a pass and gets this from full_run_in; this is for the
# callers that hold a sha and not the rows.
full_run_suite() {
  rows=$(ci_run_rows "$1") || return 1
  full_run_in "$rows" | cut -f2
}

# run_red ROW succeeds when the run the row describes is over and did not
# succeed: the run's own conclusion is its verdict, and a cancelled run -- a
# superseded one -- is not a failure, exactly as it is not for a check. A
# conclusion the API did not answer is not a verdict either, neither the empty
# field an older answer gives nor the "-" ci_run_rows writes in its place.
run_red() {
  [ "$(printf '%s' "$1" | cut -f4)" = completed ] || return 1
  case "$(printf '%s' "$1" | cut -f5)" in
    "" | "-" | success | cancelled) return 1 ;;
    *) return 0 ;;
  esac
}

# no_run_in ROWS succeeds when ROWS holds no `ci` run at all -- neither running
# nor completed -- which is a commit nobody dispatched CI on, and on which no
# check can ever turn green.
no_run_in() {
  [ -z "$1" ]
}

# no_full_run_in ROWS succeeds when ROWS holds `ci` runs and not one of them is
# a full run: every run that could vouch for the commit is a selected one (or one
# whose title names neither variant), and no amount of waiting changes that.
no_full_run_in() {
  [ -n "$1" ] || return 1
  while IFS=$'\t' read -r number suite title status conclusion url; do
    [ -n "$suite" ] || continue
    if full_run_title "$title"; then
      return 1
    fi
  done <<<"$1"
  return 0
}

# suite_check_state SHA NAME [SUITE] prints missing | cancelled | failed |
# pending | success for NAME over the check suite of the newest full `ci` run on
# SHA (SUITE when the caller has already found it), with the same rules
# commit_check_state applies to one listing. missing is a commit with no full run
# at all, which is all a selected run leaves behind. SUITE is full_run_suite's
# answer when the caller has already read the commit's runs, so one pass over the
# three `ci` names lists them once rather than three times -- the gate polls, and
# each listing is a request against the token's hourly allowance.
suite_check_state() {
  suite=${3:-$(full_run_suite "$1")} || return 1
  [ -n "$suite" ] || { printf missing; return 0; }
  retry_read gh api "repos/$GITHUB_REPOSITORY/check-suites/$suite/check-runs?per_page=100" --jq "$(check_runs_jq "$2")"
}

# check_state SHA NAME [SUITE] prints missing | cancelled | failed | pending |
# success for the required check NAME: the three a `ci` run owns from that run's
# check suite, the console legs from the commit's own check runs. SUITE is
# passed on to suite_check_state for the callers that read a commit's runs once.
check_state() {
  case " ${suite_names[*]} " in
    *" $2 "*) suite_check_state "$1" "$2" "${3:-}" ;;
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

# dispatched_for is the sha a dispatch has already been started for: one commit
# gets one `ci` run from this gate, however many passes the polling makes.
dispatched_for=""
while :; do
  # The first missing/running check is remembered as two variables rather than
  # one "$name $state" string: a name may carry a space, so "${pending% *}"
  # and "${pending#* }" would split it in the wrong place. not_green is the whole
  # list of them, for the line the wait ends on.
  pending_name=""
  pending_state=""
  not_green=""
  failed=""
  # The commit's `ci` runs, read once a pass: the newest full one for the three
  # `ci` names below, and the same rows for what they say about run and wait.
  rows=$(ci_run_rows "$GITHUB_SHA")
  full_run=$(full_run_in "$rows")
  target_suite=$(printf '%s' "$full_run" | cut -f2)
  for name in "${names[@]}"; do
    state=$(check_state "$GITHUB_SHA" "$name" "$target_suite")
    case "$state" in
      success) ;;
      missing | pending | cancelled)
        [ -n "$pending_name" ] || { pending_name="$name"; pending_state="$state"; }
        not_green="$not_green$name ($state), "
        ;;
      *)
        echo "::error::required check '$name' is $state on $GITHUB_SHA" >&2
        failed=1
        ;;
    esac
  done
  if [ -n "$failed" ]; then
    echo "ci run: $(run_url "$full_run")" >&2
    exit 1
  fi
  [ -n "$pending_name" ] || break
  # A commit with no `ci` run at all can never satisfy the gate: CI runs on no
  # commit by itself, so nothing will turn green and polling only spends the
  # wait. When a ref this gate knows points at the commit, the gate puts a full
  # run there itself, once, and waits for it; a commit no such ref points at --
  # the commit an `--at` release names, generally -- is refused at once, with
  # the command that puts one there.
  if no_run_in "$rows"; then
    if [ -z "$can_dispatch" ]; then
      echo "::error::required check '$pending_name' is $pending_state on $GITHUB_SHA: that commit has no ci run at all, and no ref this gate knows points at it to dispatch one on" >&2
      echo "dispatch a full run on a ref that points at it and re-run this job: $full_run_command" >&2
      exit 1
    fi
    if [ -n "${GATE_DRY_RUN:-}" ]; then
      echo "no ci run on $GITHUB_SHA: the real run would dispatch one ($full_run_command)"
      exit 0
    fi
    if [ "$dispatched_for" != "$GITHUB_SHA" ]; then
      if ! dispatch_err=$(gh workflow run ci --ref "$dispatch_ref" -f full=true 2>&1); then
        printf '%s\n' "$dispatch_err" >&2
        echo "::error::this gate could not dispatch a ci run on $GITHUB_SHA (\"$full_run_command\"), and no run will appear on its own: dispatch it by hand and re-run this job" >&2
        exit 1
      fi
      dispatched_for=$GITHUB_SHA
      echo "no ci run on $GITHUB_SHA: dispatched one ($full_run_command), waiting for it"
    fi
  elif no_full_run_in "$rows"; then
    # A commit with `ci` runs but no full one can never satisfy the gate either:
    # the checks it requires are read from a full run's check suite, and only a
    # dispatch makes a full run (D3040). Polling to the deadline for one that
    # does not exist only spends the wait, so the gate refuses now and prints the
    # command that puts the full run there.
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
  if run_red "$full_run"; then
    # The newest full run is over and red: it will produce no further check run,
    # so the wait can only end on the deadline. The checks that are not green are
    # what it reported before it stopped -- missing, for a job that never
    # started -- and the run's URL is where its log is.
    echo "::error::the ci run on $GITHUB_SHA concluded '$(printf '%s' "$full_run" | cut -f5)' and these required checks are not green: ${not_green%, }" >&2
    echo "ci run: $(run_url "$full_run")" >&2
    exit 1
  fi
  # A dry run reads the checks once: what is still missing or running is what
  # the real run would wait for, and it waits for it there rather than here.
  if [ -n "${GATE_DRY_RUN:-}" ]; then
    echo "check '$pending_name' is $pending_state on $GITHUB_SHA: the real run would wait up to $wait_label"
    exit 0
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error::required check '$pending_name' is $pending_state on $GITHUB_SHA: $wait_label is up, not green: ${not_green%, }; ci run: $(run_url "$full_run")" >&2
    exit 1
  fi
  echo "still waiting: check '$pending_name' is $pending_state on $GITHUB_SHA"
  sleep "$poll_seconds"
done
echo "gate passed for $tag at $GITHUB_SHA"
