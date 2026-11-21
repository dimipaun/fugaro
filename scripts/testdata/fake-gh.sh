#!/bin/sh
# A fake `gh` for scripts/release_test.go. All configuration comes from the
# environment of the release.sh process under test, which this inherits:
#   FAKE_GH_DIR      state directory (required): log, pr_number, pr_state,
#                    pr_branch, pr_merge_sha, pr_check_fail, pr_armed
#   FAKE_GH_ORIGIN   path of the bare repo standing in for the real origin,
#                    so "gh pr merge" and the lazy auto-merge below can
#                    perform a real squash merge against it
#   FAKE_GH_REPO     nameWithOwner to report (default o/r)
#   FAKE_GH_UNAUTH   non-empty: "gh auth status" fails
#   FAKE_GH_CHECK_TEST / _TERRAFORM / _RULES   success (default) |
#                    failed | missing | pending | cancelled: what the
#                    check-runs api reports for that name
#   FAKE_GH_CHECK_CONSOLE_UBUNTU / _CONSOLE_MACOS  the same for the two console
#                    legs scripts/release-gate.sh adds under
#                    GATE_REQUIRE_CONSOLE=1, which it reads from the commit's
#                    check runs (they come from console-tests.yml) and from
#                    FAKE_GH_POST_CONSOLE_* / FAKE_GH_HEAD_CONSOLE_* once a PR
#                    has merged
#   FAKE_GH_POST_TEST / _TERRAFORM / _RULES   same, but only once a
#                    PR has merged (overrides the above from then on)
#   FAKE_GH_PRE_PENDING_CALLS  before the merge, the api reports "pending"
#                    for the first N calls per check name (CI still running
#                    on main), then the configured FAKE_GH_CHECK_* value
#   FAKE_GH_MISSING_CALLS  after the merge, the api reports "missing" for
#                    the first N calls per check name (CI not started yet)
#   FAKE_GH_ADVANCE_AFTER_MERGE  non-empty: another commit lands on main
#                    right after the squash merge
#   FAKE_GH_CORRUPT_MERGE  non-empty: the merge commit lacks the plugin bump
#   FAKE_GH_AUTOMERGE_FAIL  non-empty: "gh pr merge" fails (auto-merge off)
#   FAKE_GH_PR_CREATE_FAIL  non-empty: "gh pr create" fails
#   FAKE_GH_IDENTICAL_PR  non-empty: after the merge, the commit's pulls api
#                    lists the PR (head = the pushed release branch); unset,
#                    it lists none (the identical-tree lookup finds nothing)
#   FAKE_GH_HEAD_TEST / _TERRAFORM / _RULES   success (default) |
#                    failed | missing | pending | cancelled on that PR head
#   FAKE_GH_HEAD_TREE      override the tree sha the api reports for the PR head
#   FAKE_GH_PULLS_FAIL    non-empty: the pulls api call fails
#   FAKE_GH_GIT_FAIL      non-empty: the git commits (tree) api call fails
#   FAKE_GH_BAD_PR_FIRST  non-empty: the pulls api lists first a merged PR
#                    for the same merge commit whose head has another tree
#   FAKE_GH_PULL_UNMERGED  non-empty: the listed PR has merged_at null
#   FAKE_GH_PULL_MERGE_SHA the merge_commit_sha the listed PR reports
#                    (default: the real merge commit)
#   FAKE_GH_FLAKY / FAKE_GH_FLAKY_CALLS  the first N invocations whose
#                    arguments start with FAKE_GH_FLAKY fail as a dropped
#                    connection does ("net/http: TLS handshake timeout"), and
#                    do nothing else
#   ci_runs          a state FILE (not an env var): one
#                    "<sha><TAB><title><TAB><suite>[<TAB><number>]" line per `ci`
#                    run, which is what the gate's read of a commit's runs
#                    answers with. The number is the run's run_number, the line's
#                    own position when it is not written, and it is what orders
#                    the runs: the gate counts the newest full one, so a re-
#                    dispatch is a higher number than the run it replaced. The
#                    lines come back in the order they are written, as the API
#                    lists them. Without the file every commit has one full run,
#                    whose check suite is the commit itself. A suite id that is
#                    a commit sha answers that commit's checks; "<sha>-selected"
#                    is a `ci (selected)` run's suite and "<sha>-red" a full
#                    run's whose checks failed, both answering failed for every
#                    required name, so a gate that read one of them where a
#                    green run's suite was called for could not pass.
#                    "@merge" stands for the merge commit the squash merge will
#                    make, which a test cannot know when it writes the file:
#                    every occurrence is replaced by that commit's sha as the
#                    merge happens.
#   FAKE_GH_RUN_RELEASE / FAKE_GH_RUN_IMAGES / FAKE_GH_RUN_WINDOWS
#                    success (default) | failure | cancelled | pending |
#                    missing: what the runs api reports for that workflow's
#                    run on the tag's commit, which is the read
#                    scripts/verify-release.sh waits on (it asks for the runs
#                    whose head branch is the tag and whose event is push).
#                    `pending` answers in_progress, `missing` answers no run
#                    at all. Every strict vX.Y.Z tag starts all three
#                    (release.yml, images.yml and console-windows.yml), so
#                    they are answered together. The tag is read from the
#                    origin repo, so a test must have pushed it.
#   FAKE_GH_RUN_OTHER  an extra run on the tag, "name:state" (default none),
#                    for the rule that any other workflow the tag started has
#                    to be green too
# A `gh workflow run` appends the run it started to ci_runs -- the workflow's
# newest run, which is what a dispatch is: the head is the tip of the ref the
# dispatch named (`--ref`) or of origin's own HEAD when it named none, the title
# is `ci (full)` when the dispatch asked for the full variant (`-f full=true`)
# and a bare `ci` otherwise, and the suite is the run's own head, so the checks
# it answers with are that commit's. Its run_number is above every number already
# in the file, which is how a reader tells the run a dispatch started from the
# ones before it. The invocation itself is still recorded in workflow_run, one
# line per call, so a test can assert what was dispatched and against which ref.
# The api asserts that, once a PR has merged, it is only asked about the
# merge commit and that PR's head (checks and tree).
# A required check name with no arm above -- one added to release-gate.sh's
# `names` that no test here sets up -- is an **error**, not a default
# `success`: a fake that vouched for any name it did not recognise kept every
# release.sh test green when a sixth name nobody produces entered the gate.
# TestFakeGHAnswersEveryCheckTheGateRequires holds the two lists together.
set -eu
dir=$FAKE_GH_DIR
printf '%s\n' "$*" >>"$dir/log"
repo=${FAKE_GH_REPO:-o/r}

case "$*" in
  "${FAKE_GH_FLAKY:-//none//}"*)
    flaky=$(($(cat "$dir/flaky_calls" 2>/dev/null || echo 0) + 1))
    echo "$flaky" >"$dir/flaky_calls"
    if [ "$flaky" -le "${FAKE_GH_FLAKY_CALLS:-0}" ]; then
      echo "Get \"https://api.github.com/graphql\": net/http: TLS handshake timeout" >&2
      exit 1
    fi
    ;;
esac

head_sha() { git -C "$FAKE_GH_ORIGIN" rev-parse "refs/heads/$(cat "$dir/pr_branch")"; }

head_conclusion() {
  case "$1" in
    test) printf '%s' "${FAKE_GH_HEAD_TEST:-success}" ;;
    terraform) printf '%s' "${FAKE_GH_HEAD_TERRAFORM:-success}" ;;
    rules) printf '%s' "${FAKE_GH_HEAD_RULES:-success}" ;;
    "console (ubuntu-latest)") printf '%s' "${FAKE_GH_HEAD_CONSOLE_UBUNTU:-success}" ;;
    "console (macos-latest)") printf '%s' "${FAKE_GH_HEAD_CONSOLE_MACOS:-success}" ;;
  esac
}

check_conclusion() {
  case "$1" in
    test) pre=${FAKE_GH_CHECK_TEST:-success} post=${FAKE_GH_POST_TEST:-$pre} ;;
    terraform) pre=${FAKE_GH_CHECK_TERRAFORM:-success} post=${FAKE_GH_POST_TERRAFORM:-$pre} ;;
    rules) pre=${FAKE_GH_CHECK_RULES:-success} post=${FAKE_GH_POST_RULES:-$pre} ;;
    # The console legs, which only console-release.yml's gate requires
    # (GATE_REQUIRE_CONSOLE=1): they come from console-tests.yml, so the gate
    # reads them from the commit's check runs, never from a `ci` run's suite.
    "console (ubuntu-latest)") pre=${FAKE_GH_CHECK_CONSOLE_UBUNTU:-success} post=${FAKE_GH_POST_CONSOLE_UBUNTU:-$pre} ;;
    "console (macos-latest)") pre=${FAKE_GH_CHECK_CONSOLE_MACOS:-success} post=${FAKE_GH_POST_CONSOLE_MACOS:-$pre} ;;
  esac
  if [ -s "$dir/pr_merge_sha" ]; then
    n=$(($(cat "$dir/api_calls_$1" 2>/dev/null || echo 0) + 1))
    echo "$n" >"$dir/api_calls_$1"
    if [ "$n" -le "${FAKE_GH_MISSING_CALLS:-0}" ]; then
      printf 'missing'
      return
    fi
    printf '%s' "$post"
  else
    n=$(($(cat "$dir/pre_calls_$1" 2>/dev/null || echo 0) + 1))
    echo "$n" >"$dir/pre_calls_$1"
    if [ "$n" -le "${FAKE_GH_PRE_PENDING_CALLS:-0}" ]; then
      printf 'pending'
      return
    fi
    printf '%s' "$pre"
  fi
}

# do_merge performs a real squash merge of $dir/pr_branch into origin's main
# and records the result; called once, lazily, when auto-merge is armed.
do_merge() {
  branch=$(cat "$dir/pr_branch")
  work=$(mktemp -d)
  git clone --quiet "$FAKE_GH_ORIGIN" "$work"
  git -C "$work" fetch --quiet origin "$branch"
  git -C "$work" checkout --quiet main
  subject=$(git -C "$work" log -1 --format=%s "origin/$branch")
  git -C "$work" -c user.name=test -c user.email=test@example.invalid merge --squash --quiet "origin/$branch" >/dev/null
  if [ -n "${FAKE_GH_CORRUPT_MERGE:-}" ]; then
    git -C "$work" checkout --quiet HEAD -- plugin/.claude-plugin/plugin.json
  fi
  git -C "$work" -c user.name=test -c user.email=test@example.invalid commit --quiet -m "$subject"
  sha=$(git -C "$work" rev-parse HEAD)
  if [ -n "${FAKE_GH_ADVANCE_AFTER_MERGE:-}" ]; then
    git -C "$work" -c user.name=test -c user.email=test@example.invalid commit --quiet --allow-empty -m "unrelated commit"
  fi
  git -C "$work" push --quiet origin main
  rm -rf "$work"
  echo MERGED >"$dir/pr_state"
  printf '%s' "$sha" >"$dir/pr_merge_sha"
  # A test writing ci_runs before the release runs cannot know the merge commit,
  # so "@merge" is what it writes and this is where it becomes the sha.
  if [ -f "$dir/ci_runs" ]; then
    sed "s/@merge/$sha/g" "$dir/ci_runs" >"$dir/ci_runs.merge"
    mv "$dir/ci_runs.merge" "$dir/ci_runs"
  fi
}

case "$1 ${2:-}" in
  "auth status")
    [ -n "${FAKE_GH_UNAUTH:-}" ] && exit 1
    exit 0
    ;;
  "repo view")
    printf '%s\n' "$repo"
    exit 0
    ;;
  "pr list")
    if [ -f "$dir/pr_number" ]; then
      echo "$(cat "$dir/pr_number") $(cat "$dir/pr_state" 2>/dev/null || echo OPEN)"
    fi
    exit 0
    ;;
  "pr create")
    [ -n "${FAKE_GH_PR_CREATE_FAIL:-}" ] && { echo "pr create refused" >&2; exit 1; }
    head=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--head" ] && head=$a
      prev=$a
    done
    n=1
    [ -f "$dir/pr_number" ] && n=$(($(cat "$dir/pr_number") + 1))
    echo "$n" >"$dir/pr_number"
    echo OPEN >"$dir/pr_state"
    printf '%s' "$head" >"$dir/pr_branch"
    echo "https://github.com/$repo/pull/$n"
    exit 0
    ;;
  "pr merge")
    [ -n "${FAKE_GH_AUTOMERGE_FAIL:-}" ] && { echo "Pull request auto-merge is not allowed for this repository" >&2; exit 1; }
    : >"$dir/pr_armed"
    exit 0
    ;;
  "pr view")
    json=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--json" ] && json=$a
      prev=$a
    done
    case "$json" in
      state)
        calls=$(($(cat "$dir/pr_view_calls" 2>/dev/null || echo 0) + 1))
        echo "$calls" >"$dir/pr_view_calls"
        if [ -f "$dir/pr_armed" ] && [ "$(cat "$dir/pr_state" 2>/dev/null || echo OPEN)" = OPEN ] && [ ! -s "$dir/pr_check_fail" ] && [ "$calls" -gt "${FAKE_GH_MERGE_DELAY_CALLS:-0}" ]; then
          do_merge
        fi
        cat "$dir/pr_state" 2>/dev/null || echo OPEN
        ;;
      mergeCommit)
        cat "$dir/pr_merge_sha" 2>/dev/null || true
        ;;
    esac
    exit 0
    ;;
  "pr checks")
    cat "$dir/pr_check_fail" 2>/dev/null || true
    # pr_check_once: the failure shows on one poll only (a retried check).
    [ -f "$dir/pr_check_once" ] && : >"$dir/pr_check_fail"
    exit 0
    ;;
  "workflow run")
    # `gh workflow run ci --ref release/X.Y -f ...`, which scripts/release.sh
    # dispatches -- on main's tip in the ordinary path, on the maintenance branch
    # in --hotfix. It is recorded so a test can assert what was dispatched and
    # against which ref, and the run it starts is appended to ci_runs: the ref's
    # tip, the title the dispatch asked for, the commit's own checks, and a run
    # number above everything already there (the file's header).
    printf '%s\n' "$*" >>"$dir/workflow_run"
    ref=""
    full=false
    prev=""
    for a in "$@"; do
      [ "$prev" = "--ref" ] && ref=$a
      [ "$a" = "full=true" ] && full=true
      prev=$a
    done
    [ -n "$ref" ] || ref=$(git -C "$FAKE_GH_ORIGIN" symbolic-ref --short HEAD)
    head=$(git -C "$FAKE_GH_ORIGIN" rev-parse "refs/heads/$ref")
    title=ci
    if [ "$full" = true ]; then
      title="ci (full)"
    fi
    tab=$(printf '\t')
    number=0
    if [ -f "$dir/ci_runs" ]; then
      n=0
      while IFS="$tab" read -r rsha _rtitle _rsuite rnumber; do
        [ -n "$rsha" ] || continue
        n=$((n + 1))
        if [ "${rnumber:-$n}" -gt "$number" ]; then
          number=${rnumber:-$n}
        fi
      done <"$dir/ci_runs"
    fi
    printf '%s\t%s\t%s\t%s\n' "$head" "$title" "$head" "$((number + 1))" >>"$dir/ci_runs"
    exit 0
    ;;
  "api "*)
    case "$2" in
      */actions/workflows/ci.yml/runs*)
        # scripts/release.sh's read of the newest run of the `ci` workflow,
        # whatever commit it is on (repos/.../actions/workflows/ci.yml/runs):
        # one line, "<head sha><TAB><run number>", for the highest-numbered run
        # in $dir/ci_runs -- the run a dispatch just started, when there is one.
        # The runs the file does not list do not exist for this read, which is
        # what makes the file the fake's whole answer here.
        case "$*" in
          *".workflow_runs[0]"*) ;;
          *)
            echo "fake-gh.sh: the workflow's runs api was asked with a --jq this fake does not answer: $*" >&2
            exit 1
            ;;
        esac
        tab=$(printf '\t')
        number=0
        row=""
        if [ -f "$dir/ci_runs" ]; then
          n=0
          while IFS="$tab" read -r rsha _rtitle _rsuite rnumber; do
            [ -n "$rsha" ] || continue
            n=$((n + 1))
            if [ "${rnumber:-$n}" -gt "$number" ]; then
              number=${rnumber:-$n}
              row=$(printf '%s\t%s' "$rsha" "$number")
            fi
          done <"$dir/ci_runs"
        fi
        [ -z "$row" ] || printf '%s\n' "$row"
        exit 0
        ;;
      */actions/runs*)
        case "$*" in
          *".display_title"*)
            # scripts/release-gate.sh's read of a commit's `ci` runs: one
            # tab-separated line per run, in the shape its --jq asks for
            # ([run_number, check_suite_id, display_title] | @tsv), in the order
            # the lines are written, exactly as the api lists runs -- the gate
            # orders them itself, so this fake must not. The runs come from
            # $dir/ci_runs (see the header); the gate keeps the newest row whose
            # title says the run was full and reads that run's check suite.
            sha=$(printf '%s' "$2" | sed -n 's|.*head_sha=\([^&]*\).*|\1|p')
            if [ -f "$dir/ci_runs" ]; then
              # A real tab: this file runs under /bin/sh, where $'\t' is not an
              # escape but the four literal characters it is written with.
              tab=$(printf '\t')
              number=0
              while IFS="$tab" read -r rsha title suite rnumber; do
                [ -n "$rsha" ] || continue
                number=$((number + 1))
                [ "$rsha" = "$sha" ] || continue
                printf '%s\t%s\t%s\n' "${rnumber:-$number}" "$suite" "$title"
              done <"$dir/ci_runs"
            else
              printf '1\t%s\t%s\n' "$sha" "ci (full)"
            fi
            exit 0
            ;;
        esac
        # scripts/verify-release.sh's read of the runs a tag push started: one
        # tab-separated line per run, in the shape its --jq asks for
        # ([name, status, conclusion, head branch, event] | @tsv). The tag's own
        # runs are the three a strict vX.Y.Z tag starts -- release, images and
        # console-windows, from FAKE_GH_RUN_RELEASE / _IMAGES / _WINDOWS -- plus
        # FAKE_GH_RUN_OTHER; three more rows below belong to no verification --
        # a push to main at this commit, main's nightly refresh, and a dispatch
        # against the tag's ref -- each red on purpose, so that each clause of
        # verify-release.sh's filter has a row only it rejects and deleting a
        # clause turns a green test red. This arm's rows and verify-release.sh's
        # --jq are one shape in two files: keep them in step.
        case "$*" in
          *".workflow_runs[]"*"@tsv"*) ;;
          *)
            echo "fake-gh.sh: the runs api was asked with a --jq this fake does not answer: $*" >&2
            exit 1
            ;;
        esac
        sha=$(printf '%s' "$2" | sed -n 's|.*head_sha=\([^&]*\).*|\1|p')
        branch=$(git -C "$FAKE_GH_ORIGIN" tag --points-at "$sha" 2>/dev/null | head -n1)
        # run_rows NAME STATE: one row for the workflow named NAME (the name
        # GitHub reports, which is the workflow's own `name:`).
        run_rows() {
          case "$2" in
            missing) return 0 ;;
            success | failure | cancelled)
              printf '%s\t%s\t%s\t%s\t%s\n' "$1" completed "$2" "$branch" push
              ;;
            pending)
              printf '%s\t%s\t%s\t%s\t%s\n' "$1" in_progress - "$branch" push
              ;;
            *)
              echo "fake-gh.sh: the state configured for the $1 run is '$2', not success, failure, cancelled, pending or missing" >&2
              exit 1
              ;;
          esac
        }
        if [ -n "$branch" ]; then
          run_rows release "${FAKE_GH_RUN_RELEASE:-success}"
          run_rows images "${FAKE_GH_RUN_IMAGES:-success}"
          run_rows console-windows "${FAKE_GH_RUN_WINDOWS:-success}"
        fi
        if [ -n "${FAKE_GH_RUN_OTHER:-}" ]; then
          run_rows "${FAKE_GH_RUN_OTHER%%:*}" "${FAKE_GH_RUN_OTHER#*:}"
        fi
        # Not this tag's push. The first is a push to main at this very commit --
        # the tag is usually main's tip, and shared-tests is the one workflow here
        # that a branch push starts -- so the head-branch clause is the only thing
        # keeping it out. The second is main's nightly images refresh at the same
        # commit, which either clause keeps out. The third is a dispatch against
        # the tag's ref, the shape images.yml's own notes say a workflow_dispatch
        # run has, and it ran console-windows, a required workflow, so the event
        # clause is the only thing keeping it out. All three are red on purpose,
        # so that a filter that lost a clause fails on the row it should have
        # rejected rather than passing it silently.
        printf '%s\t%s\t%s\t%s\t%s\n' shared-tests completed failure main push
        printf '%s\t%s\t%s\t%s\t%s\n' images completed failure main schedule
        printf '%s\t%s\t%s\t%s\t%s\n' console-windows completed failure "$branch" workflow_dispatch
        exit 0
        ;;
    esac
    sha=$(printf '%s' "$2" | sed -n 's|.*/commits/\([^/?]*\)/check-runs.*|\1|p')
    gsha=$(printf '%s' "$2" | sed -n 's|.*/git/commits/\([^/?]*\)$|\1|p')
    psha=$(printf '%s' "$2" | sed -n 's|.*/commits/\([^/?]*\)/pulls.*|\1|p')
    ssuite=$(printf '%s' "$2" | sed -n 's|.*/check-suites/\([^/?]*\)/check-runs.*|\1|p')
    # The gate reads test, terraform and rules from the check suite of the full
    # `ci` run it found, never from a selected one's (R1). A suite id that is a
    # commit sha is that commit's own checks, read exactly as the commit arm
    # below reads them; "<sha>-selected" is a selected run's suite and
    # "<sha>-red" a full run's whose checks failed, and both answer failed for
    # every required name: they are red here on purpose, so a gate that let
    # either count where a green full run's suite was called for cannot pass.
    if [ -n "$ssuite" ]; then
      case "$ssuite" in
        *-selected | *-red)
          for name in test terraform rules; do
            case "$*" in
              *".name == \"$name\""*)
                printf failed
                exit 0
                ;;
            esac
          done
          echo "fake-gh.sh: the api asked a red or selected run's check suite about a required check name this fake has no arm for: $*" >&2
          exit 1
          ;;
        *) sha=$ssuite ;;
      esac
    fi
    merge=$(cat "$dir/pr_merge_sha" 2>/dev/null || true)
    if [ -n "$gsha" ]; then
      [ -z "${FAKE_GH_GIT_FAIL:-}" ] || { echo "fake-gh.sh: git commits api down" >&2; exit 1; }
      if [ -n "${FAKE_GH_HEAD_TREE:-}" ] && [ -s "$dir/pr_branch" ] && [ "$gsha" = "$(head_sha)" ]; then
        printf '%s\n' "$FAKE_GH_HEAD_TREE"
      else
        git -C "$FAKE_GH_ORIGIN" rev-parse "$gsha^{tree}"
      fi
      exit 0
    fi
    if [ -n "$psha" ]; then
      [ -z "${FAKE_GH_PULLS_FAIL:-}" ] || { echo "fake-gh.sh: pulls api down" >&2; exit 1; }
      if [ -n "${FAKE_GH_IDENTICAL_PR:-}" ] && [ -n "$merge" ] && [ "$psha" = "$merge" ]; then
        merged_at=2026-01-01T00:00:00Z
        # The merge commit's parent: a different tree, no checks of its own.
        [ -z "${FAKE_GH_BAD_PR_FIRST:-}" ] || printf '%s\t%s\t%s\n' "$merged_at" "$merge" "$(git -C "$FAKE_GH_ORIGIN" rev-parse "$merge^")"
        [ -z "${FAKE_GH_PULL_UNMERGED:-}" ] || merged_at=-
        printf '%s\t%s\t%s\n' "$merged_at" "${FAKE_GH_PULL_MERGE_SHA:-$merge}" "$(head_sha)"
      fi
      exit 0
    fi
    if [ -n "$merge" ] && [ -s "$dir/pr_branch" ] && [ "$sha" = "$(head_sha)" ]; then
      for name in test terraform rules; do
        case "$*" in
          *".name == \"$name\""*)
            head_conclusion "$name"
            exit 0
            ;;
        esac
      done
    fi
    if [ -n "$merge" ] && [ "$sha" != "$merge" ]; then
      echo "fake-gh.sh: api asked about $sha, but the merge commit is $merge" >&2
      exit 1
    fi
    for name in test terraform rules; do
      case "$*" in
        *".name == \"$name\""*)
          check_conclusion "$name"
          exit 0
          ;;
      esac
    done
    # The console legs, which only console-release.yml's gate requires
    # (GATE_REQUIRE_CONSOLE=1). They come from console-tests.yml, so the gate
    # reads them from the commit's check runs and never from a `ci` run's
    # suite: a check-suites read that asks for one is the error below, not an
    # answer, because no `ci` run's suite carries it.
    if [ -z "$ssuite" ]; then
      for name in "console (ubuntu-latest)" "console (macos-latest)"; do
        case "$*" in
          *".name == \"$name\""*)
            if [ -n "$merge" ] && [ -s "$dir/pr_branch" ] && [ "$sha" = "$(head_sha)" ]; then
              head_conclusion "$name"
            else
              check_conclusion "$name"
            fi
            exit 0
            ;;
        esac
      done
    fi
    printf 'fake-gh.sh: the api asked about a required check name this fake has no arm for: %s\n' "$*" >&2
    exit 1
    ;;
esac

echo "fake-gh.sh: unhandled invocation: $*" >&2
exit 1
