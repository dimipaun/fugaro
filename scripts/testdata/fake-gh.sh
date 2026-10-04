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
#   FAKE_GH_MISSING_CALLS  after the merge, the api reports "missing" for
#                    the first N calls per check name (CI not started yet)
#   FAKE_GH_ADVANCE_AFTER_MERGE  non-empty: another commit lands on main
#                    right after the squash merge
#   FAKE_GH_CORRUPT_MERGE  non-empty: the merge commit lacks the plugin bump
#   FAKE_GH_AUTOMERGE_FAIL  non-empty: "gh pr merge" fails (auto-merge off)
#   FAKE_GH_PR_CREATE_FAIL  non-empty: "gh pr create" fails
# The api asserts that, once a PR has merged, it is only asked about the
# merge commit.
set -eu
dir=$FAKE_GH_DIR
printf '%s\n' "$*" >>"$dir/log"
repo=${FAKE_GH_REPO:-o/r}

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
    sha=$(printf '%s' "$2" | sed -n 's|.*/commits/\([^/]*\)/check-runs.*|\1|p')
    if [ -s "$dir/pr_merge_sha" ] && [ "$sha" != "$(cat "$dir/pr_merge_sha")" ]; then
      echo "fake-gh.sh: api asked about $sha, but the merge commit is $(cat "$dir/pr_merge_sha")" >&2
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
