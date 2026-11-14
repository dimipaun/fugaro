#!/usr/bin/env bash
# Tests for scripts/live-checks/check-c0.sh, with a fake `fugaro` on PATH:
# NOTHING here reaches a cloud, a project, an installation or a bucket, and the
# fake records every call it is given — with the directory it was made in and
# the FUGARO_PROJECT it carried — so a case can assert what the check read and
# where, and what it did not.
#
# The fake serves each read from a document the TEST writes, so a case knows the
# document's exact length and its counts: the byte count in the readings table
# is asserted against the file, not against the check's own arithmetic. The
# knobs a case bends are `FAKE_RUNS_EXIT` (a read that does not answer),
# `FAKE_RUNS_ERR` (what it says when it does not), `FAKE_RUNS_RAW` (a document
# that is not JSON) and `FAKE_COORD_RAW` (a document of the wrong shape).
#
# Usage: bash scripts/live-checks/check-c0_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-c0.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK_C0_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK_C0_TEST_HEAD:-1200}")" ;;
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
expect_count() { # haystack needle want what
  local got
  got=$(printf '%s\n' "$1" | grep -c -F "$2" || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 line(s) holding '$2', got $got"
}

# A value shaped like a token and nothing else. The tests never print it; a case
# plants it in a failed read's own words to prove the check refuses to echo one.
fake_token=ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
# Text no document would hold by accident, planted in a document's own fields.
sentinel=ZZSENTINELZZ-do-not-print-me

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check-c0.sh's test only. It
# answers the reads C0 makes out of documents the test wrote, and records every
# call with the directory it was made in and the FUGARO_PROJECT it carried.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
printf 'cwd=%s project=%s argv=%s\n' "$PWD" "${FUGARO_PROJECT:-none}" "$*" >>"$FAKE_LOG_DIR/fugaro.places"

case "${1:-} ${2:-}" in
"version ")
  printf '0.15.0\n'
  ;;
"runs ls")
  if [ "${FAKE_RUNS_EXIT:-0}" != 0 ]; then
    printf '%s\n' "${FAKE_RUNS_ERR:-the read did not answer}" >&2
    exit "$FAKE_RUNS_EXIT"
  fi
  if [ -n "${FAKE_RUNS_RAW:-}" ]; then
    printf '%s\n' "$FAKE_RUNS_RAW"
    exit 0
  fi
  cat "$FAKE_DOCS_DIR/runs.json"
  ;;
"coordinator status")
  if [ -n "${FAKE_COORD_RAW:-}" ]; then
    printf '%s\n' "$FAKE_COORD_RAW"
    exit 0
  fi
  cat "$FAKE_DOCS_DIR/coordinator.json"
  ;;
"plan status")
  cat "$FAKE_DOCS_DIR/plan.json"
  ;;
"budget show")
  cat "$FAKE_DOCS_DIR/budget.json"
  ;;
*)
  echo "fake fugaro: unexpected argv: $*" >&2
  exit 9
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# write_docs DIR — the four documents the reads answer with. The runs and
# budget documents are small and fixed; the coordinator one is padded to exactly
# 1200 bytes so the size the table must print for it is fixed too.
write_docs() { # dir
  mkdir -p "$1/docs"
  printf '%s\n' '{"project":"demo","runs":[{"id":"r1"},{"id":"r2"}],"totals":{"runs":2},"warnings":[]}' >"$1/docs/runs.json"
  printf '%s\n' '{"repo":"o/a","plans":[{"name":"p1"}],"totals":{"plans":1,"tasks":4,"closed":3,"open":1},"streams":[],"warnings":[]}' >"$1/docs/plan.json"
  printf '%s\n' '{"project":"demo","gcp_project":"gcp-demo","mode":"warn","day":"2026-11-14","repos":["o/a"],"running":[],"warnings":[]}' >"$1/docs/budget.json"
  python3 - "$1/docs/coordinator.json" <<'PY'
import json
import sys

doc = {"repos": [{
    "repo": "o/a", "provider": "github", "stored": True, "stale": None,
    "view": {"repo": "o/a", "collisions": [], "unread": [],
             "records": [{"runs": ["r1"], "pr": {"number": 1}, "in_flight": None}]},
}]}
raw = json.dumps(doc)
# Padding, inside the object, to a length the case can name: 1200 bytes with the
# newline, which is 1.1 KB by the same arithmetic the table uses.
raw = raw[:-1] + " " * (1199 - len(raw)) + "}"
assert len(raw) == 1199, len(raw)
open(sys.argv[1], "w").write(raw + "\n")
PY
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/fugaro.places"
  status=0
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # CHECK_C0_TEST_CWD is where the check is started from: the plan read's own
  # subject is the directory it runs in, so a case has to be able to choose it.
  (cd "${CHECK_C0_TEST_CWD:-$dir/tmp}" &&
    env \
      PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      FAKE_LOG_DIR="$dir/logs" FAKE_DOCS_DIR="$dir/docs" \
      ${args[@]+"${args[@]}"} \
      bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"}) >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  places=$(cat "$dir/logs/fugaro.places")
  calls=$(cat "$dir/logs/fugaro.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/co/docs/plans" "$1/elsewhere"
  write_fake_fugaro "$1"
  write_docs "$1"
}

assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

bytes_of() { wc -c <"$1" | tr -d ' '; }

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-c0.sh --project <name>" "--help: the command"
  expect_contains "$out" "spec.md" "--help: what the numbers are for"
  expect_contains "$out" "READ-ONLY" "--help: the header says it cannot write"
  expect_absent "$out" "CHECK C0 RESULT END" "--help: no block is printed"
  expect_absent "$calls" "fugaro" "--help: nothing is run"
}

test_dry_run_makes_no_call() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --project demo --checkout "$dir/co" --dry-run
  expect_status "$status" 0 "dry run"
  expect_contains "$err" "FUGARO_PROJECT=demo fugaro runs ls --json --since 24h" "dry run: the plan names the read"
  expect_contains "$err" "fugaro coordinator status --from-store --all --json" "dry run: the plan names the coordinator read"
  expect_contains "$err" "in $dir/co] fugaro plan status --json --all" "dry run: the plan names the checkout"
  expect_contains "$err" "fugaro budget show --json" "dry run: the plan names the budget read"
  expect_contains "$err" "fugaro runs ls --json --since 24h --limit 20" "dry run: the plan names both page sizes, which the plan and the spec disagree about"
  expect_contains "$out" "CHECK C0 RESULT" "dry run: the block"
  expect_contains "$out" "FACT runs-24h: INFO — not run: --dry-run" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row that says nothing was run"
  expect_contains "$out" "CHECK C0 RESULT END (exit 0)" "dry run: the end line"
  expect_absent "$calls" "fugaro" "dry run: no fugaro call at all"
  assert_scratch_removed "dry run"
}

test_the_reads_are_timed_and_sized() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" -- --project demo --checkout "$dir/co"
  expect_status "$status" 0 "the reads"
  expect_contains "$out" "FACT fugaro-version: PASS — 0.15.0" "the reads: the build is named"
  expect_contains "$out" "FACT belong: PASS" "the reads: the refused name is not this one"
  expect_contains "$out" "FACT project: PASS — demo" "the reads: the installation"
  expect_contains "$out" "FACT checkout: PASS — $dir/co" "the reads: the checkout"
  # The byte count is the document's own length: the test wrote the document.
  local runs_bytes coordinator_bytes
  runs_bytes=$(bytes_of "$dir/docs/runs.json")
  coordinator_bytes=$(bytes_of "$dir/docs/coordinator.json")
  expect_contains "$out" "FACT runs-24h: PASS — fugaro runs ls --json --since 24h (FUGARO_PROJECT=demo) — $runs_bytes bytes" "the reads: the 24h size"
  expect_contains "$out" "FACT runs-7d: PASS" "the reads: the 7d window"
  expect_contains "$out" "FACT runs-all: PASS" "the reads: no window"
  expect_contains "$out" "FACT runs-limit50: PASS — fugaro runs ls --json --since 24h --limit 50" "the reads: the page the plan names"
  expect_contains "$out" "FACT runs-limit20: PASS — fugaro runs ls --json --since 24h --limit 20" "the reads: the page the spec's own cell reads"
  expect_contains "$out" "holding runs 2" "the reads: what the listing held"
  expect_contains "$out" "FACT coordinator-demo: PASS" "the reads: the stored view"
  expect_contains "$out" "holding repos 1, records 1, runs 1, pull requests 1, stored 1, stale 0" "the reads: what the view held"
  expect_contains "$out" "FACT plan-status-1: PASS" "the reads: the plan walk"
  expect_contains "$out" "holding plans 1, tasks 4, open 1, closed 3" "the reads: what the walk held"
  expect_contains "$out" "FACT budget-show: PASS" "the reads: the budget document"
  expect_contains "$out" "holding repos 1, running 0" "the reads: what the budget held"
  expect_contains "$out" "FACT readings: PASS — 8 read(s) timed and sized" "the reads: the count of readings"
  expect_contains "$out" "CHECK C0 RESULT END (exit 0)" "the reads: the end line"
  # The table, and the lines the owner pastes into the spec's cells.
  expect_contains "$out" "C0 READINGS — bytes and seconds per read" "the reads: the table"
  expect_contains "$out" "CELLS — one line per read" "the reads: the cell lines"
  expect_contains "$out" "\`fugaro runs ls --json --since 24h\` — size about $runs_bytes B, time about" "the reads: the cell for the window"
  # A document of exactly 1200 bytes is 1.1 KB, so the size column is pinned
  # against the file the test wrote rather than against the check's own
  # arithmetic.
  expect_contains "$out" "$(printf '%-52s %10s %9s' 'fugaro coordinator status --from-store --all --json' "$coordinator_bytes" '1.1 KB')" "the reads: the human size"
  expect_contains "$out" "\`fugaro coordinator status --from-store --all --json\` — size about 1.1 KB" "the reads: the cell carries the human size"
  expect_contains "$out" "FACT restore: PASS" "the reads: the restore row"
  assert_scratch_removed "the reads"
}

test_the_reads_run_where_they_must() {
  local dir=$work/places
  prepare "$dir"
  run_check "$dir" -- --project demo --checkout "$dir/co"
  expect_status "$status" 0 "where the reads run"
  # The plan read walks docs/ in the directory it is run from, so it is the one
  # read that has to run inside the checkout.
  expect_contains "$places" "cwd=$dir/co project=demo argv=plan status --json --all" "where the reads run: the plan walk is in the checkout"
  # The others are made outside every checkout, so no checkout's fugaro.yaml
  # decides which installation answers.
  expect_absent "$places" "cwd=$dir/co project=demo argv=runs ls" "where the reads run: the listing is not in the checkout"
  expect_absent "$places" "cwd=$dir/co project=demo argv=coordinator status" "where the reads run: the view is not in the checkout"
  expect_absent "$places" "cwd=$dir/co project=demo argv=budget show" "where the reads run: the budget is not in the checkout"
  expect_count "$places" "project=demo argv=runs ls" 5 "where the reads run: the three windows and the two page sizes"
  # The argv is the console's own words: nothing was added to it.
  expect_contains "$calls" "fugaro runs ls --json --since 24h --limit 50" "where the reads run: the page argv"
  expect_absent "$calls" "--project" "where the reads run: no --project is invented"
}

test_the_current_directory_is_a_checkout() {
  local dir=$work/cwd
  prepare "$dir"
  CHECK_C0_TEST_CWD="$dir/co" run_check "$dir" -- --project demo
  expect_status "$status" 0 "the current directory"
  expect_contains "$out" "FACT checkout: PASS — $dir/co" "the current directory: it is the checkout"
  expect_contains "$places" "cwd=$dir/co project=demo argv=plan status --json --all" "the current directory: the walk runs there"
}

test_two_installations_are_read_once_each() {
  local dir=$work/two
  prepare "$dir"
  run_check "$dir" -- --project demo --project demo-two --checkout "$dir/co"
  expect_status "$status" 0 "two installations"
  expect_contains "$out" "FACT coordinator-demo: PASS" "two installations: the first"
  expect_contains "$out" "FACT coordinator-demo-two: PASS" "two installations: the second"
  expect_count "$places" "project=demo argv=coordinator status" 1 "two installations: the first was read as itself"
  expect_count "$places" "project=demo-two argv=coordinator status" 1 "two installations: the second was read as itself"
  # One listing, not two: the window's cost is the bucket's, not the
  # installation's, so the runs rows stay four.
  expect_contains "$out" "FACT readings: PASS — 9 read(s)" "two installations: one listing per window and one view each"
}

test_a_read_that_does_not_answer_is_not_measured() {
  local dir=$work/nowrite
  prepare "$dir"
  run_check "$dir" FAKE_RUNS_EXIT=5 FAKE_RUNS_ERR='error: not signed in to project demo' -- --project demo --checkout "$dir/co"
  expect_status "$status" 2 "a read that does not answer"
  expect_contains "$out" "FACT runs-24h: FAIL — fugaro runs ls --json --since 24h (FUGARO_PROJECT=demo) did not answer (fugaro exited 5)" "a read that does not answer: the row"
  expect_contains "$out" "What it said: error: not signed in to project demo" "a read that does not answer: what it said"
  # The other reads are still measured: one failing read does not hide the rest.
  expect_contains "$out" "FACT coordinator-demo: PASS" "a read that does not answer: the view was still read"
  expect_contains "$out" "FACT budget-show: PASS" "a read that does not answer: the budget was still read"
  expect_contains "$out" "FACT readings: PASS — 3 read(s)" "a read that does not answer: the table carries what answered"
  expect_contains "$err" "could NOT measure runs-24h, runs-7d, runs-all, runs-limit50, runs-limit20" "a read that does not answer: the exit names the rows"
  expect_contains "$out" "CHECK C0 RESULT END (exit 2)" "a read that does not answer: the end line"
}

test_a_token_in_a_failed_reads_own_words_is_not_printed() {
  local dir=$work/token
  prepare "$dir"
  run_check "$dir" FAKE_RUNS_EXIT=5 FAKE_RUNS_ERR="error: the request with $fake_token failed" -- --project demo --checkout "$dir/co"
  expect_status "$status" 2 "a token in a failed read"
  expect_contains "$out" "NOT PRINTED HERE" "a token in a failed read: the row says so"
  expect_contains "$out" "github-token" "a token in a failed read: the kind is named"
  expect_absent "$out" "$fake_token" "a token in a failed read: the block"
  expect_absent "$err" "$fake_token" "a token in a failed read: the progress lines"
}

test_a_document_that_is_not_json_is_a_finding() {
  local dir=$work/notjson
  prepare "$dir"
  run_check "$dir" FAKE_RUNS_RAW='this is not json at all' -- --project demo --checkout "$dir/co"
  expect_status "$status" 1 "a document that is not JSON"
  expect_contains "$out" "FACT runs-24h: FAIL — fugaro runs ls --json --since 24h (FUGARO_PROJECT=demo) answered, and what it printed is not a document this check can read" "not JSON: the row"
  expect_contains "$out" "not JSON at line 1 column 1" "not JSON: where the reader stopped"
  expect_contains "$out" "FACT coordinator-demo: PASS" "not JSON: the other reads still stand"
  expect_contains "$out" "CHECK C0 RESULT END (exit 1)" "not JSON: the end line"
}

test_a_document_of_the_wrong_shape_is_a_finding() {
  local dir=$work/shape
  prepare "$dir"
  run_check "$dir" FAKE_COORD_RAW='{"repos":"ZZSENTINELZZ-not-a-list"}' -- --project demo --checkout "$dir/co"
  expect_status "$status" 1 "a document of the wrong shape"
  expect_contains "$out" "FACT coordinator-demo: FAIL — fugaro coordinator status --from-store --all --json (FUGARO_PROJECT=demo, the installation whose repositories it lists) answered with a document that is not the shape this check reads: repos-not-a-string" "the wrong shape: the row"
  expect_contains "$out" "named by a key path and a type, never by a value out of the document" "the wrong shape: what it refuses to name"
  expect_absent "$out" "not-a-list" "the wrong shape: no value out of the document"
  # Its size and time are still in the table: a number read off a document this
  # check does not read is not the page's cost, and the row says so.
  expect_contains "$out" "not the shape this check reads: repos-not-a-string" "the wrong shape: the table says what it held"
}

test_the_documents_own_words_are_never_printed() {
  local dir=$work/sentinel
  prepare "$dir"
  # The sentinel is a pull request's title, which is text somebody else wrote.
  python3 - "$dir/docs/coordinator.json" "$sentinel" <<'PY'
import json
import sys

path, sentinel = sys.argv[1], sys.argv[2]
doc = {"repos": [{
    "repo": sentinel, "provider": "github", "stored": True, "stale": None,
    "view": {"repo": sentinel, "collisions": [], "unread": [],
             "records": [{"runs": ["r1"], "pr": {"number": 1, "title": sentinel},
                          "in_flight": None}]},
}]}
open(path, "w").write(json.dumps(doc) + "\n")
PY
  run_check "$dir" -- --project demo --checkout "$dir/co"
  expect_status "$status" 0 "a document's own words"
  expect_contains "$out" "FACT coordinator-demo: PASS" "a document's own words: the read answered"
  expect_absent "$out" "$sentinel" "a document's own words: the block carries only counts"
  expect_absent "$err" "$sentinel" "a document's own words: the progress lines carry none either"
  expect_contains "$out" "holding repos 1, records 1, runs 1, pull requests 1" "a document's own words: what the table carries instead"
}

test_a_hole_beats_a_finding() {
  # One read did not answer and another came back the wrong shape: the run ends
  # 2, because the table it printed is short a read and a hole is settled before
  # a finding is read.
  local dir=$work/mixed
  prepare "$dir"
  run_check "$dir" FAKE_RUNS_EXIT=5 FAKE_COORD_RAW='{"repos":{}}' -- --project demo --checkout "$dir/co"
  expect_status "$status" 2 "a hole and a finding"
  expect_contains "$out" "FACT runs-24h: FAIL" "a hole and a finding: the hole"
  expect_contains "$out" "FACT coordinator-demo: FAIL" "a hole and a finding: the finding"
  expect_contains "$err" "could NOT measure runs-24h" "a hole and a finding: the exit names the hole"
}

test_belong_is_refused_before_any_read() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project fugaro-belong-demo
  expect_status "$status" 2 "an installation naming belong"
  expect_contains "$out" "FACT belong: FAIL" "belong: the row"
  expect_contains "$err" "never runs against 'belong'" "belong: the refusal"
  expect_absent "$calls" "fugaro" "belong: no read is made at all"
}

test_a_checkout_that_is_not_one_is_not_measured() {
  local dir=$work/notco
  prepare "$dir"
  run_check "$dir" -- --project demo --checkout "$dir/elsewhere"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$out" "FACT checkout: FAIL — $dir/elsewhere is not a checkout" "not a checkout: the row"
  expect_contains "$out" "FACT plan-status-1: FAIL — the plan read was not made in $dir/elsewhere" "not a checkout: the plan row"
  expect_absent "$calls" "plan status" "not a checkout: no walk is made"
  expect_contains "$out" "FACT budget-show: PASS" "not a checkout: the other reads still measured"
}

test_no_checkout_at_all_is_not_measured() {
  local dir=$work/noco
  prepare "$dir"
  run_check "$dir" -- --project demo
  expect_status "$status" 2 "no checkout at all"
  expect_contains "$out" "FACT checkout: FAIL — no checkout was named and the current directory is not one" "no checkout: the row"
  expect_contains "$out" "FACT plan-status: FAIL — the plan read was not made" "no checkout: the plan row"
  expect_contains "$err" "could NOT measure checkout, plan-status" "no checkout: the exit names the rows"
  expect_absent "$calls" "plan status" "no checkout: no walk is made"
}

test_the_xtrace_guard() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x --project demo --checkout "$dir/co"
  expect_status "$status" 2 "bash -x"
  expect_contains "$out" "FACT xtrace: FAIL" "bash -x: the row"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$calls" "fugaro" "bash -x: no read is made"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK_C0_KEEP_SCRATCH=1 -- --project demo --checkout "$dir/co"
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT restore: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK_C0_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--wat'" "an unknown argument: the message"
}

test_a_missing_project_is_refused() {
  local dir=$work/noproject
  prepare "$dir"
  run_check "$dir" -- --checkout "$dir/co"
  expect_status "$status" 2 "no --project"
  expect_contains "$err" "--project is required" "no --project: the message"
  expect_absent "$calls" "fugaro" "no --project: nothing is run"
}

test_an_empty_name_is_refused() {
  # FUGARO_PROJECT="" is the default installation, which is not one this check
  # was told to read: an empty argument is an argument mistake, not a reading.
  local dir=$work/empty
  prepare "$dir"
  run_check "$dir" -- --project "" --checkout "$dir/co"
  expect_status "$status" 2 "an empty --project"
  expect_contains "$err" "--project needs a Fugaro project name" "an empty --project: the message"
  expect_absent "$calls" "fugaro" "an empty --project: nothing is run"
  run_check "$dir" -- --project demo --checkout ""
  expect_status "$status" 2 "an empty --checkout"
  expect_contains "$err" "--checkout needs a path" "an empty --checkout: the message"
  expect_absent "$calls" "fugaro" "an empty --checkout: nothing is run"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_dry_run_makes_no_call
t test_the_reads_are_timed_and_sized
t test_the_reads_run_where_they_must
t test_the_current_directory_is_a_checkout
t test_two_installations_are_read_once_each
t test_a_read_that_does_not_answer_is_not_measured
t test_a_token_in_a_failed_reads_own_words_is_not_printed
t test_a_document_that_is_not_json_is_a_finding
t test_a_document_of_the_wrong_shape_is_a_finding
t test_the_documents_own_words_are_never_printed
t test_a_hole_beats_a_finding
t test_belong_is_refused_before_any_read
t test_a_checkout_that_is_not_one_is_not_measured
t test_no_checkout_at_all_is_not_measured
t test_the_xtrace_guard
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_a_missing_project_is_refused
t test_an_empty_name_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-c0_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-c0_test.sh: ok"
