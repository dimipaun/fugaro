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
#   FAKE_GH_CHECK_TEST / _TERRAFORM / _RULES   success (default) | failed | missing
set -eu
dir=$FAKE_GH_DIR
printf '%s\n' "$*" >>"$dir/log"
repo=${FAKE_GH_REPO:-o/r}

check_conclusion() {
  case "$1" in
    test) printf '%s' "${FAKE_GH_CHECK_TEST:-success}" ;;
    terraform) printf '%s' "${FAKE_GH_CHECK_TERRAFORM:-success}" ;;
    rules) printf '%s' "${FAKE_GH_CHECK_RULES:-success}" ;;
  esac
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
  git -C "$work" -c user.name=test -c user.email=test@example.invalid commit --quiet -m "$subject"
  sha=$(git -C "$work" rev-parse HEAD)
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
    cat "$dir/pr_number" 2>/dev/null || true
    exit 0
    ;;
  "pr create")
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
    exit 0
    ;;
  "api "*)
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
