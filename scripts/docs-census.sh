#!/usr/bin/env bash
# scripts/docs-census.sh — the census a batch that moved a pinned count breaks:
# the two suites that read docs/ and plugin/ text, run against the merged tree.
#
# A batch merge can leave the base red with no textual conflict at all: two pull
# requests each move the same pinned count in a doc — the docs-test census row
# in docs/rigor-modes/reconciliation.md, 141 to 142 by each of #2875 and #2876,
# so main said 142 for 143 files and seven pinned tests failed — and nobody
# finds out until a 25-minute CI run. The docs and plugin tests take seconds, so
# the census runs them here, on the tree the merges made rather than on the
# checkout that made them.
#
# It reads and tests only: it merges nothing, commits nothing, moves no branch
# and has no agent-session guard — nothing it does is a person's alone to do, so
# an agent landing pull requests under the owner's word runs it the same way.
# scripts/land-prs.sh runs it after a batch; the census lives here and nowhere
# else, since a copy under /tmp is one nobody re-runs.
#
# Usage: bash scripts/docs-census.sh [--base NAME] [--since SHA]
#
#   --base NAME   the branch the batch merged into; main by default
#   --since SHA   the commit the base was on before the batch, when the caller
#                 has one: `git diff --name-only SHA..origin/NAME` is read for
#                 the website reminder
#
# What it does, in order:
#
#   1. `git fetch origin <base>`, then a detached worktree of `origin/<base>` in
#      a `mktemp -d` directory, removed on exit: the suites run against the
#      merged tree, so the checkout the caller happens to be in — its branch,
#      its uncommitted work — is never the thing tested;
#   2. `go test ./internal/cli/ -run Docs -count=1 -timeout 9m` and `go test
#      ./plugin/ -count=1 -timeout 5m` in that worktree, the second even when
#      the first failed;
#   3. the website reminder, when a --since was given and website/ changed since
#      it: `npm run check` is minutes of Node rather than seconds, so it is not
#      run here and the reader is told to run it;
#   4. `census: ok`, or one `census: FAILED (<command>)` line per failing
#      command with the failing test names as go printed them.
#
# Exit: 0 when both suites pass, and when the census cannot run at all because
# go or git is not on PATH (a `census: skipped (...)` line names the tool, and
# nothing was read or tested); 1 when either suite failed, or when the fetch or
# the worktree did not answer; 2 for a usage error.
set -Eeuo pipefail

PROG=${0##*/}
BASE=main
SINCE=""

usage() {
  cat >&2 <<'USAGE'
usage: docs-census.sh [--base NAME] [--since SHA]
  --base NAME   the branch the batch merged into (default main)
  --since SHA   the commit the base was on before the batch, for the website reminder
USAGE
}
die_usage() {
  printf '%s: %s\n' "$PROG" "$*" >&2
  usage
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
  --base)
    [ $# -ge 2 ] || die_usage "--base needs a branch name"
    BASE=$2
    shift 2
    ;;
  --base=*)
    BASE=${1#--base=}
    [ -n "$BASE" ] || die_usage "--base needs a branch name"
    shift
    ;;
  --since)
    [ $# -ge 2 ] || die_usage "--since needs a commit"
    SINCE=$2
    shift 2
    ;;
  --since=*)
    SINCE=${1#--since=}
    [ -n "$SINCE" ] || die_usage "--since needs a commit"
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  --*) die_usage "unknown option $1" ;;
  *) die_usage "not an option: $1" ;;
  esac
done

# A tool that is not there is not a census that failed: nothing was read, so
# nothing is concluded, and the caller's own exit status stays what it was.
for tool in git mktemp go; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'census: skipped (%s is not on PATH)\n' "$tool"
    exit 0
  fi
done

last_line() { # last_line TEXT
  printf '%s\n' "$1" | awk 'NF { line = $0 } END { print line }'
}

TMP=$(mktemp -d)
TREE=$TMP/tree
# A worktree is registered in the checkout's .git, so removing the directory
# alone would leave its metadata behind. It is unregistered first, best effort:
# the trap also runs when a fetch failed and no worktree was ever made.
cleanup() {
  git worktree remove --force "$TREE" >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

# The command as the summary line names it, with the reason it failed under it:
# a fetch that did not answer names no test, so its own last line is what says
# why, and a suite that failed names the tests go printed.
failed_cmd() { # failed_cmd OUT
  local reason
  printf 'census: FAILED (%s)\n' "$1"
  reason=$(last_line "$2")
  if [ -n "$reason" ]; then printf '%s\n' "$reason"; fi
}

if ! out=$(git fetch origin "$BASE" 2>&1); then
  failed_cmd "git fetch origin $BASE" "$out"
  exit 1
fi

if ! out=$(git worktree add --detach "$TREE" "origin/$BASE" 2>&1); then
  failed_cmd "git worktree add --detach <dir> origin/$BASE" "$out"
  exit 1
fi

# The failing test names as go printed them, an indented subtest under its
# parent: the summary line says which command failed and these say which tests.
print_failures() { # print_failures LOG
  local names
  names=$(grep -E '^[[:space:]]*--- FAIL: ' "$1" || true)
  if [ -n "$names" ]; then
    printf '%s\n' "$names"
    return 0
  fi
  # A command that failed without naming a test — a build error, a package the
  # timeout killed — has nothing but its own output to say why.
  printf '%s\n' "$(last_line "$(cat "$1")")"
}

FAILED_CMD=()
FAILED_LOG=()

run_suite() { # run_suite NAME ARGS...: which command was run is the label
  local name=$1
  shift
  local log=$TMP/$name.log
  if (cd "$TREE" && go "$@") >"$log" 2>&1; then
    return 0
  fi
  FAILED_CMD+=("go $*")
  FAILED_LOG+=("$log")
}

run_suite cli test ./internal/cli/ -run Docs -count=1 -timeout 9m
run_suite plugin test ./plugin/ -count=1 -timeout 5m

# The website is the one surface the census cannot cover, so a reader whose
# batch moved it is told what is left to run. A --since this checkout does not
# have — a clone that never fetched it — is said rather than swallowed: the
# caller asked for a comparison it did not get.
if [ -n "$SINCE" ]; then
  if ! changed=$(git diff --name-only "$SINCE" "origin/$BASE" 2>/dev/null); then
    printf 'census: note: --since %s is not a commit in this checkout, so website/ was not compared\n' "$SINCE"
  elif printf '%s\n' "$changed" | grep -q '^website/'; then
    printf 'website content changed: run npm run check in website/\n'
  fi
fi

if [ "${#FAILED_CMD[@]}" -gt 0 ]; then
  for i in "${!FAILED_CMD[@]}"; do
    printf 'census: FAILED (%s)\n' "${FAILED_CMD[$i]}"
    print_failures "${FAILED_LOG[$i]}"
  done
  exit 1
fi
printf 'census: ok\n'
