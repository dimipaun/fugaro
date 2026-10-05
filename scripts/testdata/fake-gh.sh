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
#   FAKE_GH_CHECK_TEST / _TERRAFORM / _RULES   success (default) | failed |
#                    missing | pending | cancelled: what the check-runs api
#                    reports for that name
#   FAKE_GH_POST_TEST / _TERRAFORM / _RULES    same, but only once a PR has
#                    merged (overrides the above from then on)
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
#   FAKE_GH_HEAD_TEST / _TERRAFORM / _RULES   success (default) | failed |
#                    missing | pending | cancelled on that PR head
#   FAKE_GH_HEAD_TREE      override the tree sha the api reports for the PR head
#   FAKE_GH_PULLS_FAIL    non-empty: the pulls api call fails
#   FAKE_GH_GIT_FAIL      non-empty: the git commits (tree) api call fails
#   FAKE_GH_BAD_PR_FIRST  non-empty: the pulls api lists first a merged PR
#                    for the same merge commit whose head has another tree
#   FAKE_GH_PULL_UNMERGED  non-empty: the listed PR has merged_at null
#   FAKE_GH_PULL_MERGE_SHA the merge_commit_sha the listed PR reports
#                    (default: the real merge commit)
# The api asserts that, once a PR has merged, it is only asked about the
# merge commit and that PR's head (checks and tree).
set -eu
dir=$FAKE_GH_DIR
printf '%s\n' "$*" >>"$dir/log"
repo=${FAKE_GH_REPO:-o/r}

head_sha() { git -C "$FAKE_GH_ORIGIN" rev-parse "refs/heads/$(cat "$dir/pr_branch")"; }

head_conclusion() {
  case "$1" in
    test) printf '%s' "${FAKE_GH_HEAD_TEST:-success}" ;;
    terraform) printf '%s' "${FAKE_GH_HEAD_TERRAFORM:-success}" ;;
    rules) printf '%s' "${FAKE_GH_HEAD_RULES:-success}" ;;
  esac
}

check_conclusion() {
  case "$1" in
    test) pre=${FAKE_GH_CHECK_TEST:-success} post=${FAKE_GH_POST_TEST:-$pre} ;;
    terraform) pre=${FAKE_GH_CHECK_TERRAFORM:-success} post=${FAKE_GH_POST_TERRAFORM:-$pre} ;;
    rules) pre=${FAKE_GH_CHECK_RULES:-success} post=${FAKE_GH_POST_RULES:-$pre} ;;
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
  "api "*)
    sha=$(printf '%s' "$2" | sed -n 's|.*/commits/\([^/?]*\)/check-runs.*|\1|p')
    gsha=$(printf '%s' "$2" | sed -n 's|.*/git/commits/\([^/?]*\)$|\1|p')
    psha=$(printf '%s' "$2" | sed -n 's|.*/commits/\([^/?]*\)/pulls.*|\1|p')
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
    echo success
    exit 0
    ;;
esac

echo "fake-gh.sh: unhandled invocation: $*" >&2
exit 1
