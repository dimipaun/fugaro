#!/usr/bin/env bash
# Tests for scripts/check-870-run-index-timings.sh. NOTHING here is a cloud, a
# project or a checkout: every case writes the documents the three reads answer
# in a temporary directory and drives the check against a fake `fugaro` on PATH,
# which records its whole argv, the directory it was run in and what its stdin
# turned out to be.
#
# The readings are the point. Check 870 judges the median of three timed samples
# of each read against a bound, and says why when a read is not a reading at all
# (an index that holds no run, a build that carries no graph, a checkout that
# joins no run). So the cases bend exactly those: a fast project that passes, a
# slow one that fails, a read that exits non-zero, an empty index, a core
# build's graph stub, a checkout with no repository, and a `belong` name. The
# fake sleeps what the case says per sample, so the check's own figure can be
# compared with the samples it prints: the median has to be the middle one.
#
# Every run has its stdin closed and a timeout, so a check that waited for input
# would end the case as a failure rather than hang it.
#
# Usage: bash scripts/check-870-run-index-timings_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-870-run-index-timings.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK870_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK870_TEST_HEAD:-2400}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; out: $(printf '%s' "$out" | head -c 2000); err: $(printf '%s' "$err" | head -c 800)"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c "${CHECK870_TEST_HEAD:-2400}")"
}
expect_no_row_state() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    fail "$4: expected no row '$2' reading $3"
  fi
}
expect_row_count() { # log prefix want what
  local got
  got=$(grep -c -- "$2" "$1" || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 calls matching '$2', got $got"
}

# expect_median_is_middle OUT KEY WHAT: the figure a read's row prints is the
# middle of the samples that same row prints. The fake's per-sample sleeps are
# wide apart, so a figure that were the mean, the smallest or the largest would
# fail this.
expect_median_is_middle() { # out key what
  local line
  line=$(printf '%s\n' "$1" | awk -v k="$2" '$1 == k && ($2 == "PASS" || $2 == "FAIL") { print }')
  [ -n "$line" ] || {
    fail "$3: no row '$2' to read a figure from"
    return 0
  }
  local figure samples middle sorted
  figure=$(printf '%s' "$line" | sed -n 's/.*median \([0-9]*\) ms.*/\1/p')
  samples=$(printf '%s' "$line" | sed -n 's/.*samples \([0-9, ]*\) (warm-up.*/\1/p')
  [ -n "$figure" ] && [ -n "$samples" ] || {
    fail "$3: the row '$2' prints no figure and samples to compare: $line"
    return 0
  }
  sorted=$(printf '%s\n' $(printf '%s' "$samples" | tr ',' ' ') | sort -n)
  middle=$(printf '%s\n' "$sorted" | sed -n 2p)
  [ "$figure" = "$middle" ] ||
    fail "$3: the figure $figure ms of '$2' is not the middle of the samples ($samples), which is $middle ms"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

# The fake fugaro answers the three reads Check 870 times and models the one
# thing its judgement rests on: how long each sample takes. FAKE870_<READ>_MS is
# a list of milliseconds, one per call of that read in this case (the warm-up
# first), the last of which is repeated for any call past the list. It logs its
# argv, the directory it was run in, and its stdin: a read on /dev/null returns
# EOF at once (status 1), where an open pipe blocks past the bound.
cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'fugaro %s (stdin=%s) (cwd=%s)\n' "$*" "$kind" "$PWD" >>"${FAKE870_LOG:?}"
dir=${FAKE870_DIR:?}
case "$1 $2" in
"runs ls") tag=runs ;;
"plan status") tag=plan ;;
"graph --json") tag=graph ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
count=$(cat "$dir/$tag.count" 2>/dev/null || printf 0)
count=$((count + 1))
printf '%s' "$count" >"$dir/$tag.count"
# The milliseconds this call of this read sleeps: the Nth of the read's list,
# and the last of it for every call past the list.
want=""
for entry in ${FAKE870_SLEEPS:-}; do
  case "$entry" in
  "$tag"=*)
    nth=$(printf '%s' "${entry#*=}" | tr ',' ' ')
    i=1
    for one in $nth; do
      want=$one
      [ "$i" -lt "$count" ] || break
      i=$((i + 1))
    done
    ;;
  esac
done
if [ -n "$want" ]; then
  python3 -c 'import sys, time; time.sleep(int(sys.argv[1]) / 1000.0)' "$want"
fi
case "$tag" in
runs)
  cat "$dir/runs.json"
  exit "${FAKE870_RUNS_STATUS:-0}"
  ;;
plan)
  cat "$dir/plan.json"
  exit "${FAKE870_PLAN_STATUS:-0}"
  ;;
graph)
  if [ -n "${FAKE870_GRAPH_STUB:-}" ]; then
    printf 'graph is part of Fugaro Subscription Edition; this build does not include it\n' >&2
    exit 1
  fi
  cat "$dir/graph.json"
  exit "${FAKE870_GRAPH_STATUS:-0}"
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

case_dir() { # case_dir <name>
  cdir=$work/$1
  answers=$cdir/answers
  checkout=$cdir/checkout
  mkdir -p "$answers" "$checkout"
  CALLS=$cdir/calls
  : >"$CALLS"
  FAKE870_SLEEPS=""
  FAKE870_RUNS_STATUS=0
  FAKE870_PLAN_STATUS=0
  FAKE870_GRAPH_STATUS=0
  FAKE870_GRAPH_STUB=
  CHECK870_BOUND_MS=1000
  CHECK870_REPEATS=3
  CHECK870_MERGES=no
  rm -f "$answers"/*.count
  write_runs
  write_plan
  write_graph
}

# The listing's document as the index answers it: the page's rows, the index's
# own totals, and its two completeness fields.
write_runs() { # write_runs [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/runs.json"
  else
    cat >"$answers/runs.json" <<'JSON'
{"runs":[{"run":"acme-app/20261120-101112-abcd","status":"succeeded"}],"totals":{"runs":4200,"total_usd":12.5},"index_complete":true,"folded_through":"2026-11-25T09:30:00Z"}
JSON
  fi
}

write_plan() { # write_plan [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/plan.json"
  else
    cat >"$answers/plan.json" <<'JSON'
{"repo":"acme/app","plans":[],"totals":{"plans":3,"tasks":41,"closed":21,"open":20},"streams":[],"warnings":[],"index_complete":true,"folded_through":"2026-11-25T09:30:00Z"}
JSON
  fi
}

write_graph() { # write_graph [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/graph.json"
  else
    cat >"$answers/graph.json" <<'JSON'
{"version":1,"assembled_at":"2026-11-25T09:30:00Z","sources":[{"scope":"all","state":"up"}],"nodes":[],"edges":[],"warnings":[]}
JSON
  fi
}

# ------------------------------------------------------------------ the runs

run870_raw() { # run870_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE870_LOG="${CALLS:-/dev/null}" FAKE870_DIR="${answers:-$work}" \
    FAKE870_SLEEPS="${FAKE870_SLEEPS:-}" FAKE870_RUNS_STATUS="${FAKE870_RUNS_STATUS:-0}" \
    FAKE870_PLAN_STATUS="${FAKE870_PLAN_STATUS:-0}" FAKE870_GRAPH_STATUS="${FAKE870_GRAPH_STATUS:-0}" \
    FAKE870_GRAPH_STUB="${FAKE870_GRAPH_STUB:-}" \
    CHECK870_BOUND_MS="${CHECK870_BOUND_MS:-1000}" CHECK870_REPEATS="${CHECK870_REPEATS:-3}" \
    CHECK870_MERGES="${CHECK870_MERGES:-no}" \
    perl -e 'alarm shift; exec @ARGV or die' 300 bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run870() { # run870 <args...> — the case's own checkout, then what the case passes
  run870_raw --checkout "${checkout:-$work/none}" "$@" </dev/null
}

# ------------------------------------------------------------------- the cases

test_a_fast_project_passes() {
  case_dir fast
  run870 fugaro
  expect_status "$status" 0 "a fast project"
  expect_contains "$out" "CHECK 870 RESULT: PASS (exit 0)" "a fast project"
  expect_row "$out" runs-ls PASS "a fast project"
  expect_row "$out" plan-status PASS "a fast project"
  expect_row "$out" graph PASS "a fast project"
  expect_row "$out" index PASS "a fast project"
  expect_row "$out" scale PASS "a fast project"
  expect_row "$out" belong PASS "a fast project"
  expect_row "$out" restore PASS "a fast project"
  expect_contains "$out" "the read covers 4200 runs" "a fast project: the scale is printed"
  expect_no_row_state "$out" runs-ls FAIL "a fast project has no failure"
}

test_the_figure_is_the_median_of_the_samples() {
  case_dir median
  FAKE870_SLEEPS="runs=10,220,60 plan=10,300,40 graph=10,20,400"
  run870 fugaro
  expect_status "$status" 0 "the median"
  expect_median_is_middle "$out" runs-ls "the median: the listing"
  expect_median_is_middle "$out" plan-status "the median: the plan join"
  expect_median_is_middle "$out" graph "the median: the graph"
  expect_not_contains "$out" "median 400 ms" "the median: the largest sample is not the figure"
  expect_not_contains "$out" "median 10 ms" "the median: the warm-up is not the figure"
}

test_a_slow_read_fails() {
  case_dir slow
  FAKE870_SLEEPS="runs=10,1200,1200 graph=10,10,10"
  run870 fugaro
  expect_status "$status" 1 "a read at or over the bound"
  expect_contains "$out" "CHECK 870 RESULT: FAIL (exit 1)" "a read at or over the bound"
  expect_row "$out" runs-ls FAIL "a read at or over the bound"
  expect_contains "$out" "at or over the 1000 ms bound" "the bound is named"
  expect_row "$out" plan-status PASS "the other reads are still judged"
  expect_row "$out" graph PASS "the other reads are still judged"
}

test_the_bound_and_the_repeats_are_asked_for() {
  case_dir bound
  FAKE870_SLEEPS="runs=10,300,300"
  CHECK870_BOUND_MS=500
  CHECK870_REPEATS=1
  run870 fugaro
  expect_status "$status" 0 "a bound the samples fit"
  expect_contains "$out" "under the 500 ms bound" "the bound this run was given is the one judged by"
  expect_contains "$err" "against 500 ms" "the progress line names it"
  expect_row_count "$CALLS" "runs ls " 2 "one warm-up and one timed sample"
}

test_an_empty_index_is_not_a_reading() {
  case_dir empty_index
  write_runs '{"runs":[],"totals":{"runs":0}}'
  run870 fugaro
  expect_status "$status" 3 "an index that holds no run"
  expect_contains "$out" "CHECK 870 RESULT: INCOMPLETE (exit 3)" "an index that holds no run"
  expect_row "$out" scale UNVERIFIED "an index that holds no run"
  expect_contains "$out" "measures nothing" "the reason is named"
  expect_row "$out" index INFO "an index that folded nothing is said rather than failed"
  expect_contains "$out" "fugaro runs reindex" "the fix is named"
}

test_an_unfinished_index_is_said() {
  case_dir building
  write_runs '{"runs":[],"totals":{"runs":12},"index_complete":false,"folded_through":"2026-11-25T09:00:00Z"}'
  run870 fugaro
  expect_status "$status" 0 "an index still building"
  expect_row "$out" index INFO "an index still building"
  expect_contains "$out" "the index is still building" "an index still building"
  expect_row "$out" runs-ls PASS "the figures are still taken and judged"
}

test_a_build_without_the_graph_is_unverified() {
  case_dir no_graph
  FAKE870_GRAPH_STUB=1
  run870 fugaro
  expect_status "$status" 3 "a core build"
  expect_contains "$out" "CHECK 870 RESULT: INCOMPLETE (exit 3)" "a core build"
  expect_row "$out" graph UNVERIFIED "a core build"
  expect_contains "$out" "Subscription Edition stub" "the stub is named"
  expect_contains "$out" "released binary" "the fix is named"
  expect_row "$out" runs-ls PASS "the other reads were measured"
}

test_a_checkout_that_joins_no_run_is_unverified() {
  case_dir no_repo
  write_plan '{"repo":"","plans":[],"totals":{"plans":0,"tasks":0},"streams":[],"warnings":[],"index_complete":true}'
  run870 fugaro
  expect_status "$status" 3 "a checkout with no repository"
  expect_row "$out" plan-status UNVERIFIED "a checkout with no repository"
  expect_contains "$out" "joined no run" "the reason is named"
  expect_contains "$out" "--checkout" "the fix is named"
  expect_row "$out" runs-ls PASS "the listing is still judged"
}

test_a_read_that_did_not_answer_fails() {
  case_dir no_answer
  FAKE870_PLAN_STATUS=1
  run870 fugaro
  expect_status "$status" 1 "a read that exited non-zero"
  expect_row "$out" plan-status FAIL "a read that exited non-zero"
  expect_contains "$out" "exited 1" "the exit status is named"
  expect_row "$out" graph PASS "the other reads were measured"

  case_dir garbage
  write_graph 'not a document'
  run870 fugaro
  expect_status "$status" 1 "a read whose answer is not a document"
  expect_row "$out" graph FAIL "a read whose answer is not a document"
  expect_contains "$out" "answered no document" "the reason is named"
}

test_only_the_reads_are_called() {
  case_dir calls
  run870 fugaro
  expect_status "$status" 0 "the call set"
  local line bad=""
  while IFS= read -r line; do
    case "$line" in
    "fugaro runs ls --json --project fugaro (stdin=closed) (cwd="*) ;;
    "fugaro plan status --json --all --no-merges --project fugaro (stdin=closed) (cwd=$checkout)") ;;
    "fugaro graph --json --project fugaro (stdin=closed) (cwd="*) ;;
    *) bad="${bad}${line}"$'\n' ;;
    esac
  done <"$CALLS"
  [ -z "$bad" ] || fail "the call set: calls outside the read set: $bad"
  expect_row_count "$CALLS" "runs ls " 4 "one warm-up and three timed samples"
  expect_row_count "$CALLS" "plan status " 4 "one warm-up and three timed samples"
  expect_row_count "$CALLS" "graph --json" 4 "one warm-up and three timed samples"
  expect_not_contains "$(cat "$CALLS")" "runs launch" "nothing is launched"
  expect_not_contains "$(cat "$CALLS")" "fugaro run " "no run is started"
}

# The plan join reads the directory it is run in and refuses one named
# elsewhere, so the check cds into the checkout it was given: the fake records
# the directory it was actually run in.
test_the_plan_join_runs_in_the_checkout() {
  case_dir in_checkout
  run870 fugaro
  expect_status "$status" 0 "the plan join's directory"
  expect_contains "$(cat "$CALLS")" "plan status --json --all --no-merges --project fugaro (stdin=closed) (cwd=$checkout)" "the plan join ran in the checkout"
}

test_merges_can_be_asked_for() {
  case_dir merges
  CHECK870_MERGES=yes
  run870 fugaro
  expect_status "$status" 0 "the plan's literal reading"
  expect_contains "$(cat "$CALLS")" "plan status --json --all --project fugaro" "no --no-merges is passed"
  expect_not_contains "$(cat "$CALLS")" "--no-merges" "the plan's literal reading carries no --no-merges"
}

test_stdin_is_closed() {
  case_dir stdin
  # An open pipe nobody writes to: a call that inherited it would read it as open.
  run870 fugaro < <(sleep 20)
  expect_status "$status" 0 "stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "a call inherited the check's stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=line)" "a call read a line of the check's stdin"
  expect_contains "$(cat "$CALLS")" "(stdin=closed)" "the calls ran with stdin closed"
}

test_a_dry_run_calls_nothing() {
  case_dir dry
  run870_raw --dry-run --checkout "$checkout" fugaro </dev/null
  expect_status "$status" 0 "--dry-run"
  expect_contains "$out" "CHECK 870 PLAN" "--dry-run prints the plan"
  expect_contains "$out" "runs ls --json --project fugaro" "--dry-run names the listing"
  expect_contains "$out" "graph --json --project fugaro" "--dry-run names the graph"
  expect_contains "$out" "Nothing was called" "--dry-run says so"
  expect_not_contains "$out" "RESULT:" "--dry-run gives no verdict"
  [ ! -s "$CALLS" ] || fail "--dry-run: it called something: $(head -c 400 "$CALLS")"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE870_LOG="$CALLS" FAKE870_DIR="$answers" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  expect_contains "$err" "coding agent's session" "an agent session: the guard names it"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir xtrace
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE870_LOG="$CALLS" FAKE870_DIR="$answers" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash -x "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  [ ! -s "$CALLS" ] || fail "bash -x: it called something before refusing: $(head -c 400 "$CALLS")"
}

# The owner's own installation is never timed, whatever the check was asked for.
test_the_owners_installation_is_refused() {
  local name
  for name in belong Belong belong-dev edge-devel-dimi edgeappinc; do
    case_dir "belong_$name"
    run870_raw "$name" </dev/null
    expect_status "$status" 2 "a project named $name"
    expect_contains "$err" "the owner's own installation" "$name is refused by name"
    [ ! -s "$CALLS" ] || fail "$name: it called something: $(head -c 400 "$CALLS")"
  done

  case_dir belong_checkout
  run870_raw --checkout "$work/belong" fugaro </dev/null
  expect_status "$status" 2 "a checkout named belong"
  [ ! -s "$CALLS" ] || fail "a checkout named belong: it called something: $(head -c 400 "$CALLS")"
}

test_bad_arguments_are_refused() {
  case_dir args
  run870_raw </dev/null
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no Fugaro project was named" "no project"
  run870_raw one two </dev/null
  expect_status "$status" 2 "two projects"
  expect_contains "$err" "one project is timed at a time" "two projects"
  run870_raw --nope fugaro </dev/null
  expect_status "$status" 2 "an unknown flag"
  run870_raw --checkout </dev/null
  expect_status "$status" 2 "--checkout with no directory"
  run870_raw 'Acme/App' </dev/null
  expect_status "$status" 2 "a project name that is not one"
  CHECK870_BOUND_MS=soon
  run870_raw fugaro </dev/null
  expect_status "$status" 2 "a bound that is not a number"
  CHECK870_BOUND_MS=1000
  CHECK870_REPEATS=0
  run870_raw fugaro </dev/null
  expect_status "$status" 2 "no repeats"
  CHECK870_REPEATS=3
  [ ! -s "$CALLS" ] || fail "bad arguments: it called something: $(head -c 400 "$CALLS")"
}

test_a_checkout_that_is_not_there_is_refused() {
  case_dir missing
  run870_raw --checkout "$work/nowhere" fugaro </dev/null
  expect_status "$status" 2 "a checkout that does not exist"
  expect_contains "$err" "is not a directory" "a checkout that does not exist"
  [ ! -s "$CALLS" ] || fail "a missing checkout: it called something: $(head -c 400 "$CALLS")"
}

test_the_scratch_can_be_kept() {
  case_dir keep
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT CHECK870_KEEP_SCRATCH=yes \
    PATH="$fakebin:$PATH" FAKE870_LOG="$CALLS" FAKE870_DIR="$answers" \
    perl -e 'alarm shift; exec @ARGV or die' 300 bash "$script" --checkout "$checkout" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 0 "CHECK870_KEEP_SCRATCH"
  expect_row "$out" restore INFO "CHECK870_KEEP_SCRATCH"
  expect_contains "$out" "is kept at" "CHECK870_KEEP_SCRATCH: the directory is named"
  expect_contains "$err" "kept the scratch directory" "CHECK870_KEEP_SCRATCH: the progress line says so"
}

test_help() {
  CALLS=
  run870_raw --help </dev/null
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help"
  expect_contains "$out" "--checkout" "--help names the checkout flag"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK870_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK870_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_a_fast_project_passes
t test_the_figure_is_the_median_of_the_samples
t test_a_slow_read_fails
t test_the_bound_and_the_repeats_are_asked_for
t test_an_empty_index_is_not_a_reading
t test_an_unfinished_index_is_said
t test_a_build_without_the_graph_is_unverified
t test_a_checkout_that_joins_no_run_is_unverified
t test_a_read_that_did_not_answer_fails
t test_only_the_reads_are_called
t test_the_plan_join_runs_in_the_checkout
t test_merges_can_be_asked_for
t test_stdin_is_closed
t test_a_dry_run_calls_nothing
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_the_owners_installation_is_refused
t test_bad_arguments_are_refused
t test_a_checkout_that_is_not_there_is_refused
t test_the_scratch_can_be_kept
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check-870-run-index-timings_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-870-run-index-timings_test.sh: ok"
