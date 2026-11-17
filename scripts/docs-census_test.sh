#!/usr/bin/env bash
# Tests for scripts/docs-census.sh, with fake `git` and `go` on PATH: NOTHING
# here fetches a real remote, adds a real worktree or runs a real Go test. The
# fake git answers the four calls the census makes -- fetch, worktree add,
# worktree remove, diff -- out of files the case wrote, and records every call
# it is given; the fake go records the arguments and the directory it ran in and
# fails when the case says so, printing a log shaped like go's.
#
# What matters here is that the census reads the merged tree rather than the
# checkout it was run from, that both suites run even when the first fails, that
# a failure is one line naming the command plus the test names, and that the
# cases where it cannot run at all -- no go, no git -- skip instead of failing.
#
# Usage: bash scripts/docs-census_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/docs-census.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${DOCS_CENSUS_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${DOCS_CENSUS_TEST_HEAD:-900}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_equal() { # got want what
  [ "$1" = "$2" ] || fail "$3: expected '$2', got '$1'"
}

bin=$work/bin
mkdir -p "$bin"
cat >"$bin/git" <<'FAKE'
#!/usr/bin/env bash
set -Eeuo pipefail
dir=${FAKE_GIT_DIR:?}
printf '%s\n' "$*" >>"$dir/gitcalls"
case "$1 ${2:-}" in
"fetch origin")
  if [ -f "$dir/fetch.fail" ]; then
    printf 'fatal: could not read from remote repository\n' >&2
    printf 'Please make sure you have the correct access rights\n' >&2
    exit 128
  fi
  printf '%s\n' "${3:-}" >"$dir/fetched"
  ;;
"worktree add")
  if [ -f "$dir/worktree.fail" ]; then
    printf "fatal: '%s' is not a commit\n" "${5:-}" >&2
    exit 128
  fi
  # add --detach PATH REF: the path is made, so the fake go runs somewhere real
  # and a case can see that it is the worktree rather than the case directory.
  printf '%s\n' "${4:-}" >"$dir/tree"
  printf '%s\n' "${5:-}" >"$dir/tree-ref"
  mkdir -p "${4:-}"
  ;;
"worktree remove")
  [ -n "${4:-}" ] && rm -rf "${4:-}"
  ;;
diff*)
  [ -f "$dir/diff.fail" ] && exit 128
  [ -f "$dir/diff.names" ] && cat "$dir/diff.names"
  ;;
*)
  printf 'fake git: unsupported invocation: %s\n' "$*" >&2
  exit 1
  ;;
esac
FAKE
chmod +x "$bin/git"

cat >"$bin/go" <<'FAKE'
#!/usr/bin/env bash
set -Eeuo pipefail
dir=${FAKE_GO_DIR:?}
printf '%s\n' "$*" >>"$dir/gocalls"
printf '%s\n' "$PWD" >>"$dir/gocwd"
pattern=""
[ -f "$dir/go.fail" ] && pattern=$(sed -n 1p "$dir/go.fail")
if [ -n "$pattern" ]; then
  case "$*" in
  *"$pattern"*)
    if [ -f "$dir/go.output" ]; then
      # A command that failed without naming a test: a build error, a timeout.
      cat "$dir/go.output"
    else
      printf '%s\n' "--- FAIL: $(sed -n 2p "$dir/go.fail") (0.01s)"
      printf '%s\n' "FAIL"
      printf 'FAIL\tgithub.com/dimipaun/fugaro/x\t0.02s\n'
    fi
    exit 1
    ;;
  esac
fi
printf 'ok  \tgithub.com/dimipaun/fugaro/x\t0.01s\n'
FAKE
chmod +x "$bin/go"

# A PATH that reaches every tool the census needs except one: that is how a
# `census: skipped (...)` case is made to be about the tool and not about the
# machine the suite runs on.
minimal_bin() { # minimal_bin DIR -- tools to symlink as the remaining arguments
  local dir=$1 tool
  shift
  rm -rf "$dir"
  mkdir -p "$dir"
  for tool in "$@"; do
    ln -sf "$(command -v "$tool")" "$dir/$tool"
  done
}

case_dir=""
new_case() { # new_case NAME
  case_dir=$work/case-$1
  mkdir -p "$case_dir"
  : >"$case_dir/gitcalls"
  : >"$case_dir/gocalls"
  : >"$case_dir/gocwd"
}
go_fails() { # go_fails PATTERN [TEST NAME] -- the commands whose arguments match it
  printf '%s\n%s\n' "$1" "${2:-TestDocsPinCensus}" >"$case_dir/go.fail"
}
fetched_base() { cat "$case_dir/fetched" 2>/dev/null || true; }
tree_ref() { cat "$case_dir/tree-ref" 2>/dev/null || true; }
tree_dir() { cat "$case_dir/tree" 2>/dev/null || true; }
gitcalls() { cat "$case_dir/gitcalls"; }
gocalls() { cat "$case_dir/gocalls"; }
count_gocalls() { # count_gocalls PATTERN
  local n
  n=$(grep -c "$1" "$case_dir/gocalls" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}

OUT=""
RC=0
census() { # census ARGS...
  OUT=$(env PATH="$bin:$PATH" FAKE_GIT_DIR="$case_dir" FAKE_GO_DIR="$case_dir" \
    "$script" "$@" 2>&1 </dev/null) && RC=0 || RC=$?
}

# ------------------------------------------------------------------- the census
new_case ok
census
expect_status "$RC" 0 "both suites passing exits 0"
expect_contains "$OUT" "census: ok" "the census says it passed"
expect_equal "$(count_gocalls '^test ./internal/cli/ -run Docs -count=1 -timeout 9m$')" 1 "the docs suite runs once"
expect_equal "$(count_gocalls '^test ./plugin/ -count=1 -timeout 5m$')" 1 "the plugin suite runs once"
expect_contains "$(gitcalls)" "fetch origin main" "main is the base when none is given"
expect_equal "$(fetched_base)" "main" "the fetch names the base"
expect_equal "$(tree_ref)" "origin/main" "the worktree is the merged base, not a local branch"
# The suites run in the worktree the census made, never in the checkout it was
# run from: the fake go's directory is the path the fake git was handed.
expect_equal "$(sort -u "$case_dir/gocwd")" "$(tree_dir)" "the suites ran in the worktree"
# The worktree is removed on exit -- by the trap, which unregisters it first,
# so the caller's checkout is left without a worktree of the census's.
expect_contains "$(gitcalls)" "worktree remove --force $(tree_dir)" "the worktree is unregistered on exit"
if [ -d "$(tree_dir)" ]; then
  fail "the worktree directory is still there after the run: $(tree_dir)"
fi

# ----------------------------------------------------------- a failing suite
new_case failing-suite
go_fails "internal/cli" "TestDocsCensusRowIsPinned"
census
expect_status "$RC" 1 "a failing docs suite exits 1"
expect_contains "$OUT" "census: FAILED (go test ./internal/cli/ -run Docs -count=1 -timeout 9m)" \
  "the failing command is named as it was run"
expect_contains "$OUT" "--- FAIL: TestDocsCensusRowIsPinned (0.01s)" "the failing test names are printed as go prints them"
expect_absent "$OUT" "census: ok" "a failed census never says ok"
# Both suites are the census, so the plugin suite runs even though the docs one
# already failed: one run tells the reader everything that is red.
expect_equal "$(count_gocalls '^test ./plugin/ -count=1 -timeout 5m$')" 1 "the plugin suite runs after a failure too"

new_case both-fail
go_fails "test ." "TestEveryPin"
census
expect_status "$RC" 1 "both suites failing exits 1"
expect_contains "$OUT" "census: FAILED (go test ./internal/cli/ -run Docs -count=1 -timeout 9m)" \
  "the docs command is named"
expect_contains "$OUT" "census: FAILED (go test ./plugin/ -count=1 -timeout 5m)" \
  "the plugin command is named too"

# A command that fails without naming a test -- a build error -- still has to
# say something, and its own last line is all there is.
new_case failing-without-a-test
go_fails "internal/cli" ignored
printf 'internal/cli/x_test.go:3:2: undefined: y\n' >"$case_dir/go.output"
census
expect_status "$RC" 1 "a build error exits 1"
expect_contains "$OUT" "census: FAILED (go test ./internal/cli/ -run Docs -count=1 -timeout 9m)" "the command is named"
expect_contains "$OUT" "internal/cli/x_test.go:3:2: undefined: y" "the command's own last line is printed"

# ------------------------------------------------------------------- the base
new_case base-named
census --base release
expect_status "$RC" 0 "a named base exits 0"
expect_equal "$(fetched_base)" "release" "the fetch names the base the caller gave"
expect_equal "$(tree_ref)" "origin/release" "the worktree is that base's tip"
census --base=release
expect_equal "$(fetched_base)" "release" "--base=NAME is the same flag"

# ----------------------------------------------------------- the website note
new_case website-changed
printf 'docs/SPEC.md\nwebsite/src/pages/index.astro\n' >"$case_dir/diff.names"
census --since abc1234
expect_status "$RC" 0 "a website change is a note, not a failure"
expect_contains "$OUT" "website content changed: run npm run check in website/" "the reader is told what is left to run"
expect_contains "$(gitcalls)" "diff --name-only abc1234 origin/main" "the diff is the base before the batch"

new_case website-untouched
printf 'docs/SPEC.md\ninternal/cli/x.go\n' >"$case_dir/diff.names"
census --since abc1234
expect_absent "$OUT" "website content changed" "a batch that moved no website path prints no reminder"
# A path merely starting with the letters is not under website/.
new_case website-prefix
printf 'website-tools/x.go\n' >"$case_dir/diff.names"
census --since abc1234
expect_absent "$OUT" "website content changed" "a path outside website/ prints no reminder"

new_case since-unknown
: >"$case_dir/diff.fail"
census --since deadbeef
expect_status "$RC" 0 "a --since this checkout does not have does not fail the census"
expect_contains "$OUT" "census: note: --since deadbeef is not a commit in this checkout" "the missing comparison is said"

new_case no-since
printf 'website/src/pages/index.astro\n' >"$case_dir/diff.names"
census
expect_absent "$OUT" "website content changed" "without --since there is nothing to compare"
expect_absent "$(gitcalls)" "diff" "without --since no diff is read"

# ------------------------------------------------------ the tree was not read
new_case fetch-fails
: >"$case_dir/fetch.fail"
census
expect_status "$RC" 1 "a fetch that did not answer exits 1"
expect_contains "$OUT" "census: FAILED (git fetch origin main)" "the fetch is the command named"
expect_contains "$OUT" "Please make sure you have the correct access rights" "git's own last line says why"
expect_equal "$(gocalls)" "" "nothing is tested when the base was not fetched"
expect_absent "$(gitcalls)" "worktree add" "no worktree is made for a fetch that failed"

new_case worktree-fails
: >"$case_dir/worktree.fail"
census
expect_status "$RC" 1 "a worktree that was not made exits 1"
expect_contains "$OUT" "census: FAILED (git worktree add --detach <dir> origin/main)" "the worktree is the command named"
expect_contains "$OUT" "fatal: 'origin/main' is not a commit" "git's own last line says why"
expect_equal "$(gocalls)" "" "nothing is tested without a tree to test"

# ------------------------------------------------------------- a tool is missing
minimal_bin "$work/bin-no-go" bash mktemp rm mkdir awk grep cat tr sed
cp "$bin/git" "$work/bin-no-go/git"
minimal_bin "$work/bin-no-git" bash mktemp rm mkdir awk grep cat tr sed
cp "$bin/go" "$work/bin-no-git/go"

new_case no-go
OUT=$(env PATH="$work/bin-no-go" FAKE_GIT_DIR="$case_dir" FAKE_GO_DIR="$case_dir" \
  bash "$script" 2>&1 </dev/null) && RC=0 || RC=$?
expect_status "$RC" 0 "a census with no go on PATH exits 0"
expect_contains "$OUT" "census: skipped (go is not on PATH)" "the missing tool is named"
expect_equal "$(gitcalls)" "" "nothing is fetched when the census cannot run"

new_case no-git
OUT=$(env PATH="$work/bin-no-git" FAKE_GIT_DIR="$case_dir" FAKE_GO_DIR="$case_dir" \
  bash "$script" 2>&1 </dev/null) && RC=0 || RC=$?
expect_status "$RC" 0 "a census with no git on PATH exits 0"
expect_contains "$OUT" "census: skipped (git is not on PATH)" "the missing tool is named"

# --------------------------------------------------------------- usage errors
new_case usage
census --nope
expect_status "$RC" 2 "an unknown option is a usage error"
expect_contains "$OUT" "unknown option --nope" "the usage error names the option"
census --base
expect_status "$RC" 2 "--base without a value is a usage error"
expect_contains "$OUT" "--base needs a branch name" "the usage error says what is missing"
census --since
expect_status "$RC" 2 "--since without a value is a usage error"
census main
expect_status "$RC" 2 "a positional argument is a usage error"
expect_equal "$(gitcalls)" "" "a usage error reads nothing"
expect_equal "$(gocalls)" "" "a usage error tests nothing"

new_case help
census --help
expect_status "$RC" 0 "--help exits 0"
expect_contains "$OUT" "usage: docs-census.sh [--base NAME] [--since SHA]" "the usage line names both flags"

if [ "$failures" -ne 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "docs-census_test: ok"
