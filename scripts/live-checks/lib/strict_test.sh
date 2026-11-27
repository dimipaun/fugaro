#!/usr/bin/env bash
# Tests for scripts/live-checks/lib/strict.sh and lib/preflight.sh: the harness
# every live check's test now drives its fakes with. Nothing here touches the
# network, a home directory or a cloud.
#
# Usage: bash scripts/live-checks/lib/strict_test.sh
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export LIVE_CHECK_LIB=$here

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: '$1'" ;;
  esac
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

. "$here/strict.sh"

# ------------------------------------------------------------------ fugaro
# The CLI's own surface: a real command and flag passes, anything else is
# refused with the CLI's own wording.
run_fugaro() { # ARGS...
  strict_fugaro_verify "$@" 2>"$work/err"
}

test_fugaro_accepts_the_real_surface() {
  run_fugaro accounts pool add --strategy failover chk-pool
  expect_status "$?" 0 "a real command and flag"
  run_fugaro accounts ls --json --repo a/b
  expect_status "$?" 0 "accounts ls with its own flags"
  run_fugaro run --title T --task-file f task-brief
  expect_status "$?" 0 "run with a value flag and an argument"
  run_fugaro --version
  expect_status "$?" 0 "the root's --version"
  run_fugaro doctor --all
  expect_status "$?" 0 "doctor --all"
  # A value-taking flag's value is not a flag of its own, even spelled like one:
  # the arity is the only thing that says so, and a verifier that read it wrong
  # would refuse a command line the real CLI takes.
  run_fugaro accounts ls --repo --nope
  expect_status "$?" 0 "a value that is spelled like an unknown flag"
}

test_fugaro_refuses_an_unknown_flag() {
  run_fugaro accounts add --project x
  expect_status "$?" 1 "an unknown flag"
  expect_contains "$(cat "$work/err")" "unknown flag: --project" "the CLI's own wording"
  run_fugaro accounts pool add --nope
  expect_status "$?" 1 "an unknown flag on a real command"
}

test_fugaro_refuses_an_unknown_command() {
  run_fugaro nosuchcmd
  expect_status "$?" 1 "an unknown command"
  expect_contains "$(cat "$work/err")" 'unknown command "nosuchcmd"' "the CLI's own wording"
}

test_fugaro_refuses_an_unknown_shorthand() {
  run_fugaro accounts ls -Z
  expect_status "$?" 1 "an unknown shorthand"
  expect_contains "$(cat "$work/err")" "unknown shorthand flag" "the CLI's own wording"
}

# ------------------------------------------------------------------ gcloud/gh
write_allow() { # FILE LINES...
  local file=$1
  shift
  : >"$file"
  local line
  for line in "$@"; do printf '%s\n' "$line" >>"$file"; done
}

test_gcloud_accepts_and_refuses() {
  write_allow "$work/x.gcloud" \
    $'cmd\trun jobs list' \
    $'flag\t--region' \
    $'flag\t--limit' \
    $'cmd\tconfig get-value'
  local st
  strict_gcloud_verify "$work/x.gcloud" run jobs list --project P --region R --format=json 2>"$work/err"
  expect_status "$?" 0 "a listed subcommand"
  strict_gcloud_verify "$work/x.gcloud" run jobs list --project P --nope 2>"$work/err" && st=0 || st=$?
  expect_status "$st" 1 "an unlisted flag"
  expect_contains "$(cat "$work/err")" "unknown flag: --nope" "the flag refusal"
  strict_gcloud_verify "$work/x.gcloud" runs jobs list --project P 2>"$work/err" && st=0 || st=$?
  expect_status "$st" 1 "an unlisted subcommand"
  expect_contains "$(cat "$work/err")" "unknown command runs" "the subcommand refusal"
}

test_gh_accepts_and_refuses() {
  write_allow "$work/x.gh" $'cmd\tapi' $'cmd\tpr view' $'flag\t--json'
  local st
  strict_gh_verify "$work/x.gh" api /repos/a/b/installation 2>"$work/err"
  expect_status "$?" 0 "a listed gh call"
  strict_gh_verify "$work/x.gh" pr create --title t 2>"$work/err" && st=0 || st=$?
  expect_status "$st" 1 "an unlisted gh subcommand"
}

test_allow_list_missing_is_a_refusal() {
  strict_gcloud_verify "$work/nope.gcloud" run jobs list 2>"$work/err" && st=0 || st=$?
  expect_status "$st" 1 "a check whose allow-list is missing"
  expect_contains "$(cat "$work/err")" "allow-list" "the missing allow-list is named"
}

# ------------------------------------------------------------------ preflight
. "$here/preflight.sh"
PREFLIGHT_TOOL=check999

run_preflight() { # FN
  PREFLIGHT_BLOCKS=""
  PREFLIGHT_NOTES=""
  ( "$1"; preflight_report ) 2>"$work/pf" && st=0 || st=$?
}

test_preflight_reports_every_blocker_at_once() {
  two_blockers() {
    preflight_block alpha "the first thing is wrong" "fugaro alpha fix --project P"
    preflight_block beta "the second thing is wrong" "gcloud beta fix --project P"
  }
  run_preflight two_blockers
  expect_status "$st" 1 "a preflight with blockers"
  local out; out=$(cat "$work/pf")
  expect_contains "$out" "BLOCKED alpha: the first thing is wrong" "the first blocker"
  expect_contains "$out" "BLOCKED beta: the second thing is wrong" "the second blocker"
  expect_contains "$out" "fix: fugaro alpha fix --project P" "the first fix"
  expect_contains "$out" "found 2 blocker(s)" "the count"
}

test_preflight_passes_when_nothing_blocks() {
  no_blockers() { preflight_note alpha "the config declares two credentials"; }
  run_preflight no_blockers
  expect_status "$st" 0 "a preflight with no blockers"
  expect_contains "$(cat "$work/pf")" "preflight passed" "the pass line"
}

test_preflight_refuses_an_agent_session() {
  ( CLAUDECODE=1 preflight_agent_session ) 2>"$work/pf" && st=0 || st=$?
  expect_status "$st" 2 "a coding agent's session"
  expect_contains "$(cat "$work/pf")" "CLAUDECODE is set" "the marker is named"
}

for t in $(declare -F | awk '{ print $3 }' | grep '^test_'); do
  "$t"
done

if [ "$failures" -gt 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "strict_test.sh: ok"
