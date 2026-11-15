#!/usr/bin/env bash
# Tests for scripts/live-checks/check139.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a run or an account, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation: the fake `fugaro` answers the
# four calls the check makes (version, `doctor --all`, the runs list and
# `diagnose`), the fake `gcloud` answers the two it makes (`config get-value
# account` and `logging read`), and a run's entries are a JSON file the case
# writes, so a case reads the counts the check took rather than a script's own
# echo. The cases that matter are the ones where that model is bent: a listing
# whose only settled runs are oauth, a listing whose gateway runs carry no
# sub-agent, no entry at all, a read that comes back at the limit (gcloud answers
# newest first, so the run may be longer than what came back), a stage name
# carrying two sessions (which is not a finding: the runner reuses stage names
# across sessions by design), a sub-agent
# id spanning two sessions, a sub-agent sharing a session with an entry whose
# agent_id is empty (in another stage: the comparison is run-wide), an id seen
# only on refused entries, a background model that carries an id, a background
# model the run never called, a background model that is also a stage's own model
# (the run's effective background: the row is not taken rather than counted), an
# account, a run or a log view naming belong, a read that fails, hangs or answers
# something that is not a document (or not the shape this check reads), a record
# naming no execution, a record and a listing that disagree about the execution, a
# doctor that exits 1 (an answer) and one that does not answer at all.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check139_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check139.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK139_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK139_TEST_HEAD:-900}")" ;;
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

# Every command this file runs is bounded, and so is every case: an earlier
# live-check test (check340_test.sh) hung for 25 minutes, and the suite that runs
# this file has one deadline for all of them. `timeout` is coreutils and is not
# on a stock Mac, so the fallback is a watchdog of the same shape.
TIMEOUT_BIN=""
for candidate in timeout gtimeout; do
  if command -v "$candidate" >/dev/null 2>&1; then
    TIMEOUT_BIN=$candidate
    break
  fi
done
CASE_TIMEOUT=${CHECK139_TEST_TIMEOUT:-60}

bounded() { # bounded CMD...
  local rc=0
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" "$CASE_TIMEOUT" "$@" || rc=$?
    return "$rc"
  fi
  "$@" &
  local pid=$!
  ( sleep "$CASE_TIMEOUT"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

# The run the happy path reads, its execution, and the ids its entries carry.
# The ids are distinctive so a case can assert they never reach the block.
run_id=run-20261114131000-1
run_id2=run-20261114131000-2
execution=projects/fugaro-dev/locations/us-east5/jobs/fugaro-acme-sandbox/executions/fugaro-acme-sandbox-abc12
execution2=projects/fugaro-dev/locations/us-east5/jobs/fugaro-acme-sandbox/executions/fugaro-acme-sandbox-def34
sub_id=agent-7f3a
sub_id2=agent-8b4c
sub_session=sess-sub-7f3a
main_session=sess-main-7f3a

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check139.sh's test only. It
# answers the two calls the check makes — the account read the belong guard
# takes, and the log read the entries come from — out of the case's own
# directory. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk139-owner@belong.example\n' ;;
    *) printf 'chk139-owner@example.com\n' ;;
    esac
  fi
  ;;
--quiet)
  # `gcloud --quiet logging read ...`, the only other call the check makes.
  case "$mode" in
  logging-fails)
    printf 'ERROR: (gcloud.logging.read) the caller does not have permission\n' >&2
    exit 1
    ;;
  logging-hangs) sleep 60 ;;
  logging-not-json) printf 'not a document\n' ;;
  *) cat "$FAKE_JSON_DIR/calls.json" ;;
  esac
  ;;
*)
  printf 'gcloud: unexpected call: %s\n' "$*" >&2
  exit 2
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check139.sh's test only. It
# serves the runs list and the run's record out of the case's own directory and
# bends one behaviour per FAKE_MODE. Nothing here reaches a real installation.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
version)
  printf 'fugaro 9.9.9-fake\n'
  ;;
doctor)
  case "$mode" in
  doctor-other-project)
    printf 'project: fugaro (GCP another-project, us-east1)\n'
    ;;
  doctor-exit-1)
    # doctor exits 1 whenever any of its checks is not ok, warnings included,
    # and still prints its document: an answer, not a failure (#2719).
    printf 'project: fugaro (GCP fugaro-dev, us-east5)\n'
    printf 'warn: the policy is older than this build\n'
    exit 1
    ;;
  doctor-fails)
    printf 'fugaro: the doctor could not reach the installation\n' >&2
    exit 2
    ;;
  *) printf 'project: fugaro (GCP fugaro-dev, us-east5)\ncheck: ok\n' ;;
  esac
  ;;
runs)
  case "$mode" in
  runs-fails)
    printf 'fugaro: the runs bucket could not be read\n' >&2
    exit 2
    ;;
  *) cat "$FAKE_JSON_DIR/runs.json" ;;
  esac
  ;;
diagnose)
  case "$mode" in
  diag-fails)
    printf 'fugaro: the run could not be read\n' >&2
    exit 2
    ;;
  diag-exit-1)
    cat "$FAKE_JSON_DIR/diag.json"
    exit 1
    ;;
  diag-not-json) printf 'not a document\n' ;;
  *) cat "$FAKE_JSON_DIR/diag.json" ;;
  esac
  ;;
*)
  printf 'fugaro: unexpected call: %s\n' "$*" >&2
  exit 2
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------- fixtures
run_row() { # run_row RUN_ID BASIS EXECUTION STATUS OUTCOME CREATED
  printf '{"run_id":"%s","repo":"acme/sandbox","settled":true,"cost":{"model_basis":"%s"},"execution":"%s","status":"%s","outcome":"%s","created":"%s"}' \
    "$1" "$2" "$3" "$4" "$5" "$6"
}

write_runs() { # write_runs DIR ROW...
  local dir=$1 first=1 row
  shift
  {
    printf '{"runs":['
    for row in "$@"; do
      [ "$first" = 1 ] || printf ','
      first=0
      printf '%s' "$row"
    done
    printf ']}\n'
  } >"$dir/json/runs.json"
}

write_diag() { # write_diag DIR JSON
  printf '%s\n' "$2" >"$1/json/diag.json"
}

entry() { # entry STAGE MODEL AGENT SESSION STATUS SETTLED
  printf '{"jsonPayload":{"message":"model call","stage":"%s","model":"%s","agent_id":"%s","session_id":"%s","status":%s,"settled":"%s"}}' \
    "$1" "$2" "$3" "$4" "$5" "$6"
}

write_calls() { # write_calls DIR ENTRY...
  local dir=$1 first=1 e
  shift
  {
    printf '['
    for e in "$@"; do
      [ "$first" = 1 ] || printf ','
      first=0
      printf '%s' "$e"
    done
    printf ']\n'
  } >"$dir/json/calls.json"
}

default_diag() { # default_diag DIR
  write_diag "$1" "{\"row\":{\"run_id\":\"${run_id}\",\"execution\":\"${execution}\",\"status\":\"succeeded\",\"outcome\":\"ready\",\"repo\":\"acme/sandbox\",\"created\":\"2026-11-14T13:10:00Z\"},\"stages\":[{\"name\":\"build\",\"agents\":1,\"model\":\"sonnet\"},{\"name\":\"implement\",\"agents\":2,\"model\":\"opus\"}]}"
}

# The happy path's entries: the main session's calls carry an empty agent_id and
# one session per stage, two sub-agents carry an id each, and the background
# model's one call carries none.
default_calls() { # default_calls DIR
  write_calls "$1" \
    "$(entry build sonnet "" "$main_session" 200 nonzero)" \
    "$(entry build haiku "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id2" sess-sub-8b4c 200 nonzero)"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/xdg" "$1/logs" "$1/tmp" "$1/json"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_runs "$1" "$(run_row "$run_id" api-list "$execution" succeeded ready 2026-11-14T13:10:00Z)"
  default_diag "$1"
  default_calls "$1"
}

# --------------------------------------------------------------- the runner
run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
}

run_copy() { # run_copy DIR COPY ENV... [-- ARGS...]: the same run, another script
  local dir=$1 copy=$2
  shift 2
  run_one "$dir" "$copy" "$@"
}

run_one() { # run_one DIR SCRIPT ENV... [-- ARGS...]
  local dir=$1 target=$2
  shift 2
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/logs" "$dir/home" "$dir/tmp" "$dir/xdg" "$dir/json"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  # FUGARO_CONFIG is emptied and XDG_CONFIG_HOME is pointed at the case's own
  # directory, so the project config the log_view is read from is the case's.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FUGARO_CONFIG= XDG_CONFIG_HOME="$dir/xdg" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" \
    CHECK139_DOCTOR_TIMEOUT_SECONDS=10 CHECK139_CLI_TIMEOUT_SECONDS=20 \
    CHECK139_LOG_TIMEOUT_SECONDS=10 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------- assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

# assert_read_only DIR WHAT: every call the check made, against the four reads it
# is allowed to make. This is the check's whole contract — it launches nothing,
# spends nothing and writes nothing — and it is asserted mechanically rather than
# trusted, because a check that launched a run would still print a block.
assert_read_only() { # assert_read_only DIR WHAT
  local dir=$1 what=$2 line
  while IFS= read -r line; do
    case "$line" in
    "" | "fugaro version"* | "fugaro doctor "* | "fugaro runs ls "* | "fugaro diagnose "*) ;;
    *) fail "$what: fugaro was given a call that is not one of the four reads: $line" ;;
    esac
  done <"$dir/logs/fugaro.calls"
  while IFS= read -r line; do
    case "$line" in
    "" | "gcloud config get-value account"* | "gcloud --quiet logging read"*) ;;
    *) fail "$what: gcloud was given a call that is not one of the two reads: $line" ;;
    esac
  done <"$dir/logs/gcloud.calls"
}

assert_no_calls() { # assert_no_calls DIR WHAT
  local dir=$1 what=$2
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "$what: fugaro was called: $(head -n 1 "$dir/logs/fugaro.calls")"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "$what: gcloud was called: $(head -n 1 "$dir/logs/gcloud.calls")"
}

# assert_no_reads DIR WHAT: a run that refuses or stops before its first step
# makes no fugaro call at all, and the one gcloud call that precedes the guards
# is the account read the belong rule is decided from.
assert_no_reads() { # assert_no_reads DIR WHAT
  local dir=$1 what=$2 line
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "$what: fugaro was called: $(head -n 1 "$dir/logs/fugaro.calls")"
  while IFS= read -r line; do
    case "$line" in
    "" | "gcloud config get-value account"*) ;;
    *) fail "$what: gcloud was given a call that is not the account read: $line" ;;
    esac
  done <"$dir/logs/gcloud.calls"
}

# assert_stopped_before_the_run DIR WHAT: the guards stopped it before any read of
# a run — the doctor and the version are the only calls it made.
assert_stopped_before_the_run() { # assert_stopped_before_the_run DIR WHAT
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "runs ls" "$what: no listing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "diagnose" "$what: no record is read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "$what: no log entry is read"
}

assert_no_listing() { # assert_no_listing DIR WHAT: with --run the check does not list
  expect_absent "$(cat "$1/logs/fugaro.calls")" "runs ls" "$2: the listing is not read"
}

assert_ids_never_printed() { # assert_ids_never_printed WHAT
  local what=$1 id
  for id in "$sub_id" "$sub_id2" "$sub_session" "$main_session"; do
    expect_absent "$out" "$id" "$what: the agent id $id never reaches the block"
    expect_absent "$err" "$id" "$what: the agent id $id never reaches the progress output"
  done
}

# The other half of that promise, and the one a scratch directory kept with
# CHECK139_KEEP_SCRATCH=1 makes testable: no id reaches any FILE the check writes
# either. The raw `gcloud logging read` output is the one document that carries
# one, so it must not survive the read that derived the counts from it — the ids
# are asserted against every file under the scratch root, not just stdout.
assert_ids_never_on_disk() { # assert_ids_never_on_disk DIR WHAT
  local dir=$1 what=$2 id hits
  for id in "$sub_id" "$sub_id2" "$sub_session" "$main_session"; do
    hits=$(grep -rl -- "$id" "$dir/tmp" 2>/dev/null || true)
    [ -z "$hits" ] || fail "$what: $hits holds the agent id $id — no id may reach a file this check writes, kept scratch or not"
  done
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check139.sh --run <run-id>" "--help: the run flag it documents"
  expect_contains "$out" "bash scripts/live-checks/check139.sh --dry-run" "--help: the dry run it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that nothing is created, that it costs nothing, and that
  # the check is theirs and not an agent's.
  expect_contains "$out" "nothing but wall clock" "--help: the cost is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "GATES task T7.2" "--help: what it gates"
  assert_no_calls "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "--dry-run"
  expect_contains "$out" "FACT dry-run: PASS" "--dry-run: the row"
  expect_contains "$out" "FACT run: INFO — planned" "--dry-run: the run row is planned, not measured"
  expect_contains "$out" "FACT u4: INFO — planned" "--dry-run: the verdict row is planned, not measured"
  expect_contains "$out" "CHECK 139 RESULT END (exit 0)" "--dry-run: the block ends clean"
  # The plan says what it would read, and the dry run reads none of it: the one
  # call it makes is the doctor that says which installation this is.
  expect_contains "$err" "gcloud logging read" "--dry-run: the plan names the log read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "runs ls" "--dry-run: no listing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "diagnose" "--dry-run: no record is read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "--dry-run: no log entry is read"
  assert_read_only "$dir" "--dry-run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" -- --bg-model haiku
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "FACT u4: PASS" "the happy path: U4 holds"
  expect_contains "$out" "a distinct-agent count IS a sub-agent count" "the happy path: the verdict"
  expect_contains "$out" "FACT run: PASS — ${run_id}" "the happy path: the run it found"
  expect_contains "$out" "found in the listing" "the happy path: how it found it"
  expect_contains "$out" "FACT execution: PASS — ${execution}" "the happy path: the execution"
  expect_contains "$out" "FACT entries: PASS — 5 model call entries" "the happy path: the entry count"
  expect_contains "$out" "3 of them carry an empty agent_id and 2 distinct non-empty agent ids" "the happy path: the counts"
  expect_contains "$out" "FACT main-empty: PASS" "the happy path: the main session"
  expect_contains "$out" "build 2 entries/1 session, implement 1 entry/1 session" "the happy path: the per-stage counts"
  expect_contains "$out" "FACT subagents-distinct: PASS" "the happy path: the sub-agents"
  expect_contains "$out" "FACT background-none: PASS" "the happy path: the background model"
  expect_contains "$out" "haiku made 1 call(s) and not one of them carries an agent_id" "the happy path: the background reading"
  expect_contains "$out" "FACT refused-ids: PASS" "the happy path: the refused entries"
  expect_contains "$out" "FACT record-agents: INFO" "the happy path: the record's own figure"
  expect_contains "$out" "build=1, implement=2" "the happy path: the record's agents"
  expect_contains "$out" "DIFFERENT figure" "the happy path: named as a different figure"
  expect_contains "$out" "FACT paste-back: PASS — Check 139 — ${run_id} (acme/sandbox)" "the happy path: the sentence"
  expect_contains "$out" "U4: PASS — a distinct-agent count IS a sub-agent count" "the happy path: the sentence's verdict"
  expect_contains "$out" "FACT scratch-removed: PASS" "the happy path: the scratch directory"
  assert_scratch_removed "the happy path"
  assert_ids_never_printed "the happy path"
  assert_read_only "$dir" "the happy path"
  # Nothing is written and nothing is launched: the check reads a run that
  # already exists and starts none.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "the happy path: no run is launched"
}

test_the_block_prints_every_row_in_order() {
  local dir=$work/order
  prepare "$dir"
  run_check "$dir" -- --bg-model haiku
  expect_status "$status" 0 "the block's order"
  local keys
  keys=$(printf '%s\n' "$out" | sed -n 's/^FACT \([a-z0-9-]*\):.*/\1/p' | tr '\n' ' ')
  [ "$keys" = "fugaro-version project-region log-view xtrace agent-session belong dry-run run execution entries main-empty subagents-distinct background-none refused-ids record-agents u4 paste-back restore scratch-removed " ] ||
    fail "the block's rows: expected the fixed order, got: $keys"
}

test_the_run_named_with_run_is_read_without_a_listing() {
  local dir=$work/named
  prepare "$dir"
  # The listing serves nothing at all: a check that listed would find no run.
  write_runs "$dir"
  run_check "$dir" -- --run "$run_id" --bg-model haiku
  expect_status "$status" 0 "the named run"
  expect_contains "$out" "FACT run: PASS — ${run_id}" "the named run: the row"
  expect_contains "$out" "named with --run" "the named run: how it was chosen"
  expect_contains "$out" "FACT u4: PASS" "the named run: the verdict"
  assert_no_listing "$dir" "the named run"
  assert_read_only "$dir" "the named run"
}

test_no_run_to_read() {
  local dir=$work/no-run
  prepare "$dir"
  # Every settled run is an oauth one, which makes no gateway call at all, and
  # one settled gateway run carries no execution.
  write_runs "$dir" \
    "$(run_row "$run_id" subscription "$execution" succeeded ready 2026-11-14T13:10:00Z)" \
    "$(run_row "$run_id2" api-list "" succeeded ready 2026-11-14T12:00:00Z)"
  run_check "$dir"
  expect_status "$status" 1 "no run to read"
  expect_contains "$out" "FACT run: FAIL — no run to read" "no run to read: the row"
  expect_contains "$out" "1 settled run(s) are oauth" "no run to read: the oauth count"
  expect_contains "$out" "1 carries no execution" "no run to read: the execution-less row"
  expect_contains "$out" "launch a short gateway run that spawns a sub-agent" "no run to read: what to launch"
  expect_contains "$out" "never oauth" "no run to read: the budget has to be on"
  expect_contains "$out" "This check launches nothing itself and spends nothing" "no run to read: the check launches nothing"
  expect_contains "$out" "not a finding about U4" "no run to read: it is not a finding"
  expect_contains "$out" "FACT execution: FAIL — not reached" "no run to read: the rows after it are not reached"
  expect_contains "$out" "FACT paste-back: FAIL — not reached" "no run to read: the sentence is not reached"
  # The one thing it must not do about having no run is make one.
  assert_read_only "$dir" "no run to read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no run to read: no run is launched"
}

test_a_listing_whose_gateway_runs_carry_no_subagent_is_no_run_to_read() {
  local dir=$work/no-subagent-listing
  prepare "$dir"
  # A settled gateway run whose entries are all the main session's: the listing
  # cannot tell, and the entries say it is not a run U4 can be answered from.
  write_calls "$dir" "$(entry build sonnet "" "$main_session" 200 nonzero)"
  run_check "$dir"
  expect_status "$status" 1 "a listing with no sub-agent"
  expect_contains "$out" "FACT run: FAIL — no run to read" "a listing with no sub-agent: the row"
  expect_contains "$out" "read the entries of 1 of those runs and not one carries a non-empty agent_id" "a listing with no sub-agent: what was read"
  assert_read_only "$dir" "a listing with no sub-agent"
}

test_a_listing_that_cannot_be_read_is_not_measured() {
  local dir=$work/listing-fails
  prepare "$dir"
  FAKE_MODE=runs-fails run_check "$dir"
  expect_status "$status" 2 "a listing that cannot be read"
  expect_contains "$out" "FACT run: FAIL" "a listing that cannot be read: the row"
  expect_contains "$out" "FACT aborted: FAIL" "a listing that cannot be read: the abort row"
  expect_contains "$out" "fugaro runs ls --json did not answer" "a listing that cannot be read: what happened"
  expect_contains "$out" "CHECK 139 RESULT END (exit 2)" "a listing that cannot be read: the exit"
  assert_scratch_removed "a listing that cannot be read"
}

test_a_listing_that_is_not_a_document_is_not_measured() {
  local dir=$work/listing-garbage
  prepare "$dir"
  printf 'not a document\n' >"$dir/json/runs.json"
  run_check "$dir"
  expect_status "$status" 2 "a listing that is not a document"
  expect_contains "$out" "printed a document this check could not read" "a listing that is not a document: what happened"
}

test_no_entry_at_all_is_not_measured() {
  local dir=$work/no-entries
  prepare "$dir"
  write_calls "$dir"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "no entry at all"
  expect_contains "$out" "FACT entries: FAIL — no model call entry" "no entry at all: the row"
  expect_contains "$out" "the run did not go through the gateway" "no entry at all: the likely reason"
  expect_contains "$err" "could NOT measure entries" "no entry at all: the notice"
  expect_contains "$out" "CHECK 139 RESULT END (exit 2)" "no entry at all: the exit"
  assert_scratch_removed "no entry at all"
}

# The read is bounded by --limit and gcloud answers with the newest entries
# first, so a reading that came back AT the limit is part of the run: every count
# below it would be a count of that part, and a partial reading can show a
# finding the whole run does not have. It is refused rather than counted — and
# one entry below the limit still reads.
test_a_read_at_the_limit_is_not_measured() {
  local dir=$work/at-limit
  prepare "$dir"
  # The fixture is five entries: a limit of five is a read that came back at the
  # limit, and a limit of six is one that did not.
  run_check "$dir" CHECK139_LIMIT=5 -- --run "$run_id" --bg-model haiku
  expect_status "$status" 2 "a read at the limit"
  expect_contains "$out" "FACT entries: FAIL — 5 model call entries were read" "a read at the limit: the row"
  expect_contains "$out" "which is the whole of --limit=5" "a read at the limit: why it stops"
  expect_contains "$out" "Raise CHECK139_LIMIT" "a read at the limit: what to do about it"
  expect_absent "$out" "distinct non-empty agent ids are in them" "a read at the limit: the counts are not taken"
  expect_absent "$out" "FACT u4: PASS" "a read at the limit: no verdict is taken"
  expect_contains "$err" "could NOT measure entries" "a read at the limit: the notice"
  expect_contains "$out" "CHECK 139 RESULT END (exit 2)" "a read at the limit: the exit"
  assert_scratch_removed "a read at the limit"

  local under=$work/under-limit
  prepare "$under"
  run_check "$under" CHECK139_LIMIT=6 -- --run "$run_id" --bg-model haiku
  expect_status "$status" 0 "a read under the limit"
  expect_contains "$out" "FACT entries: PASS — 5 model call entries" "a read under the limit: the row"
  expect_contains "$out" "FACT u4: PASS" "a read under the limit: the verdict"
}

test_a_run_with_no_subagent_is_not_measured() {
  local dir=$work/no-subagent
  prepare "$dir"
  write_calls "$dir" \
    "$(entry build sonnet "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "" "$main_session" 200 nonzero)"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "a run with no sub-agent"
  expect_contains "$out" "FACT entries: PASS" "a run with no sub-agent: the entries were read"
  expect_contains "$out" "FACT subagents-distinct: FAIL — no entry of ${run_id} carries a non-empty agent_id" "a run with no sub-agent: the row"
  expect_contains "$out" "spawned no sub-agent the gateway saw" "a run with no sub-agent: why it stops"
  expect_contains "$err" "could NOT measure subagents-distinct" "a run with no sub-agent: the notice"
  expect_contains "$out" "FACT u4: FAIL — not reached" "a run with no sub-agent: no verdict is taken"
}

test_a_logging_read_that_fails_is_not_measured() {
  local dir=$work/logging-fails
  prepare "$dir"
  FAKE_MODE=logging-fails run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "a log read that fails"
  expect_contains "$out" "FACT entries: FAIL" "a log read that fails: the row"
  expect_contains "$out" "the run's log entries could not be read" "a log read that fails: what happened"
  expect_contains "$out" "roles/logging.viewer" "a log read that fails: the permission it needs"
  expect_contains "$out" "CHECK 139 RESULT END (exit 2)" "a log read that fails: the exit"
  assert_scratch_removed "a log read that fails"
}

test_a_logging_read_that_never_answers_is_not_measured() {
  local dir=$work/logging-hangs
  prepare "$dir"
  FAKE_MODE=logging-hangs run_check "$dir" CHECK139_LOG_TIMEOUT_SECONDS=3 -- --run "$run_id"
  expect_status "$status" 2 "a log read that never answers"
  expect_contains "$out" "FACT entries: FAIL" "a log read that never answers: the row"
  expect_contains "$out" "timed out after 3s" "a log read that never answers: the bound"
  assert_scratch_removed "a log read that never answers"
}

test_entries_that_are_not_a_document_are_not_measured() {
  local dir=$work/logging-garbage
  prepare "$dir"
  FAKE_MODE=logging-not-json run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "entries that are not a document"
  expect_contains "$out" "the log entries are not the document this check reads" "entries that are not a document: what happened"
}

# A stage NAME is not a session. The runner reuses stage names across sessions
# by design — every senior review round and every retry is r.stage("review") with
# a new session (internal/runner/runner.go), a fix with no session to resume
# starts one, and an account-limited attempt gets a new one — so a run with two
# review rounds reads two sessions under one name, which is not a finding.
test_a_stage_name_carrying_two_sessions_is_not_a_finding() {
  local dir=$work/two-review-rounds
  prepare "$dir"
  write_calls "$dir" \
    "$(entry build sonnet "" "$main_session" 200 nonzero)" \
    "$(entry build haiku "" "$main_session" 200 nonzero)" \
    "$(entry review opus "" sess-review-1-7f3a 200 nonzero)" \
    "$(entry review opus "" sess-review-2-7f3a 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id2" sess-sub-8b4c 200 nonzero)"
  run_check "$dir" -- --run "$run_id" --bg-model haiku
  expect_status "$status" 0 "two review rounds"
  expect_contains "$out" "FACT main-empty: INFO" "two review rounds: the row is not a finding"
  expect_contains "$out" "a stage name carried more than one session — review/(2 sessions)" "two review rounds: the stage that carried two"
  expect_contains "$out" "the runner reuses stage names across sessions by design" "two review rounds: why it is not a finding"
  expect_contains "$out" "FACT subagents-distinct: PASS" "two review rounds: the ids are still sub-agents"
  expect_contains "$out" "FACT u4: PASS" "two review rounds: the verdict"
  assert_ids_never_printed "two review rounds"
}

test_a_subagent_id_spanning_two_sessions_is_a_finding() {
  local dir=$work/id-spans
  prepare "$dir"
  write_calls "$dir" \
    "$(entry implement opus "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" sess-other-7f3a 200 nonzero)"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "an id spanning two sessions"
  expect_contains "$out" "FACT subagents-distinct: FAIL" "an id spanning two sessions: the row"
  expect_contains "$out" "1 of them span more than one session" "an id spanning two sessions: the count"
  expect_contains "$out" "implement/opus" "an id spanning two sessions: where it was"
  expect_contains "$out" "an id that outlives its session is a rename rather than a sub-agent" "an id spanning two sessions: why"
  expect_contains "$out" "FACT u4: FAIL" "an id spanning two sessions: the verdict"
}

test_a_subagent_sharing_a_session_with_an_empty_agent_id_is_a_finding() {
  local dir=$work/shared-session
  prepare "$dir"
  # The sub-agent runs under a DIFFERENT stage from the entry whose agent_id is
  # empty, and shares its session: the comparison is run-wide, because a stage
  # name is reused across sessions and comparing within one stage would miss the
  # same session under two names.
  write_calls "$dir" \
    "$(entry build sonnet "" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "a sub-agent sharing a session with an empty-agent_id entry"
  expect_contains "$out" "FACT subagents-distinct: FAIL" "a sub-agent sharing a session with an empty-agent_id entry: the row"
  expect_contains "$out" "name a session an entry with an empty agent_id also names (anywhere in the run)" "a sub-agent sharing a session with an empty-agent_id entry: why"
  expect_contains "$out" "FACT u4: FAIL" "a sub-agent sharing a session with an empty-agent_id entry: the verdict"
}

test_an_id_only_on_refused_entries_is_a_finding() {
  local dir=$work/refused
  prepare "$dir"
  # A call refused before it reserved still logs its agent_id, and the gateway
  # counts it: the count is then not only of the sub-agents whose calls were
  # admitted.
  write_calls "$dir" \
    "$(entry implement opus "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id2" sess-sub-8b4c 403 zero)"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "an id only on refused entries"
  expect_contains "$out" "FACT refused-ids: FAIL — 1 agent id(s) appear on refused entries only, over 1 refused entry" "an id only on refused entries: the row"
  expect_contains "$out" "implement/opus" "an id only on refused entries: where it was"
  expect_contains "$out" "settled nothing" "an id only on refused entries: why"
  expect_contains "$out" "FACT u4: FAIL" "an id only on refused entries: the verdict"
}

test_the_refused_ids_row_is_load_bearing() {
  local dir=$work/refused-mutation
  prepare "$dir"
  write_calls "$dir" \
    "$(entry implement opus "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id2" sess-sub-8b4c 403 zero)"
  # A copy that reads `>= 0` where the real script reads `== 0`: every fixture
  # passes the row, which is exactly the defect the case above catches.
  local copy=$dir/check139-mutated.sh
  sed 's/if \[ "${refused_only:-0}" -eq 0 \]; then/if [ "${refused_only:-0}" -ge 0 ]; then/' "$script" >"$copy"
  grep -q 'refused_only:-0}" -ge 0' "$copy" || fail "the mutation: the copy was not bent (the line moved)"
  run_copy "$dir" "$copy" -- --run "$run_id"
  expect_status "$status" 0 "the mutation: the bent copy passes the row"
  expect_contains "$out" "FACT refused-ids: PASS" "the mutation: the bent copy passes it"
  expect_absent "$out" "FACT refused-ids: FAIL" "the mutation: the bent copy does not fail it"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT refused-ids: FAIL" "the real script: the row"
}

test_a_background_model_that_carries_an_id_is_a_finding() {
  local dir=$work/background-id
  prepare "$dir"
  write_calls "$dir" \
    "$(entry build haiku "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" \
    "$(entry implement haiku "$sub_id2" sess-sub-8b4c 200 nonzero)"
  run_check "$dir" -- --run "$run_id" --bg-model haiku
  expect_status "$status" 1 "a background model carrying an id"
  expect_contains "$out" "FACT background-none: FAIL" "a background model carrying an id: the row"
  expect_contains "$out" "the background model haiku made 2 call(s) and 1 of them carry an agent_id" "a background model carrying an id: the count"
  expect_contains "$out" "implement/haiku" "a background model carrying an id: where it was"
  expect_contains "$out" "would make the distinct-agent count wrong" "a background model carrying an id: why"
  expect_contains "$out" "FACT u4: FAIL" "a background model carrying an id: the verdict"
}

test_a_background_model_the_run_never_called_is_not_measured() {
  local dir=$work/background-absent
  prepare "$dir"
  write_calls "$dir" \
    "$(entry build sonnet "" "$main_session" 200 nonzero)" \
    "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)"
  run_check "$dir" -- --run "$run_id" --bg-model haiku
  expect_status "$status" 2 "a background model the run never called"
  expect_contains "$out" "FACT background-none: FAIL — no entry of ${run_id} names the background model haiku" "a background model the run never called: the row"
  expect_contains "$out" "The models the entries name are opus x1, sonnet x1" "a background model the run never called: the names it did see"
  expect_contains "$err" "could NOT measure background-none" "a background model the run never called: the notice"
  expect_contains "$out" "FACT u4: FAIL" "a background model the run never called: the verdict"
}

test_a_background_model_not_named_is_info() {
  local dir=$work/background-unnamed
  prepare "$dir"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 0 "a background model not named"
  expect_contains "$out" "FACT background-none: INFO" "a background model not named: the row"
  expect_contains "$out" "not taken: no background model was named" "a background model not named: why"
  expect_contains "$out" "agent.models.background" "a background model not named: where its name is"
  expect_contains "$out" "The models the entries name are opus x3, haiku x1, sonnet x1" "a background model not named: the names it saw"
  expect_contains "$out" "FACT u4: PASS" "a background model not named: U4 still holds"
  expect_contains "$out" "the background model was not named so its row was not taken" "a background model not named: the verdict says so"
}

test_a_record_with_no_stage_agents_is_named_as_absent() {
  local dir=$work/no-agents
  prepare "$dir"
  write_diag "$dir" "{\"row\":{\"run_id\":\"${run_id}\",\"execution\":\"${execution}\",\"status\":\"succeeded\",\"outcome\":\"ready\",\"repo\":\"acme/sandbox\",\"created\":\"2026-11-14T13:10:00Z\"},\"stages\":[{\"name\":\"build\",\"model\":\"sonnet\"}]}"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 0 "a record with no stage agents"
  expect_contains "$out" "FACT record-agents: INFO — the run's record names no stage agents" "a record with no stage agents: the row"
  expect_contains "$out" "it is absent rather than zero when nobody counted" "a record with no stage agents: why it is not a zero"
  expect_contains "$out" "the record's own stage agents read none" "a record with no stage agents: the sentence"
}

test_a_record_naming_no_execution_is_not_measured() {
  local dir=$work/no-execution
  prepare "$dir"
  write_diag "$dir" "{\"row\":{\"run_id\":\"${run_id}\",\"status\":\"succeeded\",\"outcome\":\"ready\",\"repo\":\"acme/sandbox\",\"created\":\"2026-11-14T13:10:00Z\"},\"stages\":[]}"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "a record naming no execution"
  expect_contains "$out" "FACT execution: FAIL — ${run_id} names no execution" "a record naming no execution: the row"
  expect_contains "$out" "there are no entries to read" "a record naming no execution: why it stops"
  expect_contains "$out" "FACT entries: FAIL — not reached" "a record naming no execution: the rows after it"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "a record naming no execution: no log is read"
}

test_an_execution_the_listing_and_the_record_disagree_about_is_a_finding() {
  local dir=$work/execution-mismatch
  prepare "$dir"
  write_diag "$dir" "{\"row\":{\"run_id\":\"${run_id}\",\"execution\":\"${execution2}\",\"status\":\"succeeded\",\"outcome\":\"ready\",\"repo\":\"acme/sandbox\",\"created\":\"2026-11-14T13:10:00Z\"},\"stages\":[]}"
  run_check "$dir"
  expect_status "$status" 1 "an execution the listing and the record disagree about"
  expect_contains "$out" "FACT execution: FAIL" "an execution the listing and the record disagree about: the row"
  expect_contains "$out" "and the run's own record names ${execution2}" "an execution the listing and the record disagree about: both names"
  expect_contains "$out" "read both before this check's answer is recorded" "an execution the listing and the record disagree about: what to do"
}

test_a_run_the_installation_does_not_know_is_not_measured() {
  local dir=$work/unknown-run
  prepare "$dir"
  write_diag "$dir" '{"row":{},"stages":[]}'
  run_check "$dir" -- --run run-20261114000000-9
  expect_status "$status" 2 "a run the installation does not know"
  expect_contains "$out" "FACT execution: FAIL — not reached" "a run the installation does not know: the rows after it"
  expect_contains "$out" "FACT aborted: FAIL" "a run the installation does not know: the abort row"
  expect_contains "$out" "named no run" "a run the installation does not know: what happened"
  expect_contains "$out" "fugaro runs ls --all --project fugaro --json" "a run the installation does not know: how to find one"
}

test_a_diagnose_that_fails_is_not_measured() {
  local dir=$work/diag-fails
  prepare "$dir"
  FAKE_MODE=diag-fails run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "a diagnose that fails"
  expect_contains "$out" "FACT aborted: FAIL" "a diagnose that fails: the abort row"
  expect_contains "$out" "fugaro diagnose ${run_id} --project fugaro --json did not answer" "a diagnose that fails: what happened"
}

test_a_diagnose_that_exits_one_is_still_an_answer() {
  local dir=$work/diag-exit-1
  prepare "$dir"
  # diagnose exits 1 when the execution is one it will not follow, and still
  # prints its document (#2719): the check goes on.
  FAKE_MODE=diag-exit-1 run_check "$dir" -- --run "$run_id" --bg-model haiku
  expect_status "$status" 0 "a diagnose that exits 1"
  expect_contains "$out" "FACT execution: PASS" "a diagnose that exits 1: the record was read"
  expect_contains "$out" "FACT u4: PASS" "a diagnose that exits 1: the verdict"
  expect_absent "$out" "FACT aborted: FAIL" "a diagnose that exits 1: no abort row"
}

test_a_diagnose_that_is_not_a_document_is_not_measured() {
  local dir=$work/diag-garbage
  prepare "$dir"
  FAKE_MODE=diag-not-json run_check "$dir" -- --run "$run_id"
  expect_status "$status" 2 "a diagnose that is not a document"
  expect_contains "$out" "printed a document this check could not read" "a diagnose that is not a document: what happened"
}

test_the_log_view_is_read_from_the_project_config() {
  local dir=$work/log-view
  prepare "$dir"
  local view=projects/fugaro-dev/locations/global/buckets/fugaro-logs/views/_Default
  mkdir -p "$dir/xdg/fugaro/projects"
  printf 'project: fugaro\nlog_view: %s\n' "$view" >"$dir/xdg/fugaro/projects/fugaro.yaml"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 0 "the log view"
  expect_contains "$out" "FACT log-view: PASS" "the log view: the row"
  expect_contains "$out" "the project config ${dir}/xdg/fugaro/projects/fugaro.yaml names the view ${view}" "the log view: where it was read"
  expect_contains "$out" "which is the only way gcloud takes a full resource name" "the log view: why --resource-names"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--resource-names=${view}" "the log view: the read is scoped to it"
  # --view is refused without --bucket and --location, and the config holds the
  # whole resource name rather than a view's leaf.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" " --view" "the log view: --view is never used"
  expect_contains "$out" "--resource-names=${view}" "the log view: the entries row names it"
}

test_without_a_log_view_the_read_is_project_scoped() {
  local dir=$work/no-log-view
  prepare "$dir"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 0 "no log view"
  expect_contains "$out" "FACT log-view: INFO" "no log view: the row"
  expect_contains "$out" "no project config for fugaro was found" "no log view: why"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--project=fugaro-dev" "no log view: the read is scoped to the project"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "--resource-names" "no log view: no view is named"
}

test_the_project_and_the_region_are_hard_coded() {
  local dir=$work/hard-coded
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the hard-coded project"
  expect_contains "$out" "fugaro on GCP project fugaro-dev (sandbox only; never belong)" "the hard-coded project: the row"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --project fugaro --all" "the hard-coded project: the doctor read"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "runs ls --all --project fugaro" "the hard-coded project: the listing"
  # The project is never read from the environment: a stray gcloud config would
  # point the check at another installation.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "get-value project" "the hard-coded project: no gcloud config read"
  assert_read_only "$dir" "the hard-coded project"
}

test_a_doctor_that_names_another_project_stops_the_check() {
  local dir=$work/other-project
  prepare "$dir"
  FAKE_MODE=doctor-other-project run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "a doctor naming another project"
  expect_contains "$out" "FACT aborted: FAIL" "a doctor naming another project: the abort row"
  expect_contains "$out" "this is not the fugaro-dev installation" "a doctor naming another project: why"
  expect_contains "$out" "never belong" "a doctor naming another project: the refusal says never belong"
  expect_contains "$out" "FACT run: FAIL — not reached" "a doctor naming another project: nothing is read"
  assert_stopped_before_the_run "$dir" "a doctor naming another project: it stops before the reads it has not made"
}

test_a_doctor_that_does_not_answer_stops_the_check() {
  local dir=$work/doctor-fails
  prepare "$dir"
  FAKE_MODE=doctor-fails run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "a doctor that does not answer"
  expect_contains "$out" "FACT aborted: FAIL" "a doctor that does not answer: the abort row"
  expect_contains "$out" "did not answer (exit 2)" "a doctor that does not answer: the exit"
  expect_contains "$out" "it is how this check knows which installation it is reading" "a doctor that does not answer: why it stops"
  assert_stopped_before_the_run "$dir" "a doctor that does not answer"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent-session
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1 -- --run "$run_id"
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the marker"
  expect_contains "$out" "no agent runs any step of it" "a coding agent's session: why"
  assert_no_reads "$dir" "a coding agent's session"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace --run "$run_id"
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing (bash -x)" "a traced shell: why"
  expect_contains "$out" "a trace of this run would copy a run's own log to the terminal" "a traced shell: what a trace would leak"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belong-account
  prepare "$dir"
  FAKE_MODE=belong-account run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$out" "FACT belong: FAIL" "an account naming belong: the row"
  expect_contains "$out" "Check 139 is never run there" "an account naming belong: the refusal"
  expect_contains "$out" "reads the sandbox installation only" "an account naming belong: what it reads instead"
  assert_no_reads "$dir" "an account naming belong"
}

test_a_run_naming_belong_is_refused() {
  local dir=$work/belong-run
  prepare "$dir"
  run_check "$dir" -- --run belong-run-20261114131000
  expect_status "$status" 1 "a run naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a run naming belong: the row"
  expect_contains "$out" "Check 139 is never run there" "a run naming belong: the refusal"
  assert_no_reads "$dir" "a run naming belong"
}

# The log view is a name this check reads with, so it is judged by the same rule
# as the account, the project and the run — and judged BEFORE the guard's own row
# is recorded, so the block's belong row is the refusal and not a PASS the stop
# came after.
test_a_log_view_naming_belong_is_refused() {
  local dir=$work/belong-view
  prepare "$dir"
  run_check "$dir" CHECK139_LOG_VIEW=projects/belong-dev/locations/global/buckets/belong-logs/views/_Default -- --run "$run_id"
  expect_status "$status" 1 "a log view naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a log view naming belong: the row"
  expect_contains "$out" "Check 139 is never run there" "a log view naming belong: the refusal"
  expect_contains "$out" "log view this check would read names belong" "a log view naming belong: which name it was"
  expect_absent "$out" "FACT belong: PASS" "a log view naming belong: the guard does not pass it and stop after"
  assert_no_reads "$dir" "a log view naming belong"
}

# The same name can come from the project config, which no flag of this check
# wrote: the config's own log_view line is refused by the same rule.
test_a_project_config_naming_belongs_log_view_is_refused() {
  local dir=$work/belong-view-config
  prepare "$dir"
  mkdir -p "$dir/xdg/fugaro/projects"
  printf 'project: fugaro\nlog_view: projects/edgeweb/locations/global/buckets/logs/views/_Default\n' \
    >"$dir/xdg/fugaro/projects/fugaro.yaml"
  run_check "$dir" -- --run "$run_id"
  expect_status "$status" 1 "a project config naming EdgeWeb's log view"
  expect_contains "$out" "FACT belong: FAIL" "a project config naming EdgeWeb's log view: the row"
  expect_contains "$out" "Check 139 is never run there" "a project config naming EdgeWeb's log view: the refusal"
  expect_absent "$out" "FACT log-view: PASS" "a project config naming EdgeWeb's log view: no view row is recorded"
  assert_no_reads "$dir" "a project config naming EdgeWeb's log view"
}

# The background model is the run's EFFECTIVE one (config.EffectiveBackground),
# so a repository that names none has its coder's own model for a background. A
# name that is also a stage's model cannot tell a background call from that
# stage's calls, so the row is NOT taken: counting them would FAIL it on the
# sub-agents it counts, which is the false finding this case holds out.
test_a_background_model_that_is_a_stage_model_is_not_counted() {
  local dir=$work/background-stage-model
  prepare "$dir"
  run_check "$dir" -- --run "$run_id" --bg-model opus
  expect_status "$status" 2 "a background model that is also a stage's model"
  expect_contains "$out" "FACT background-none: FAIL — the model named (opus) is also a stage's own model" "a background model that is also a stage's model: the row"
  expect_contains "$out" "implement ran on it" "a background model that is also a stage's model: which stage"
  expect_contains "$out" "every call of the stage matches, its sub-agents' included" "a background model that is also a stage's model: why the name cannot separate them"
  expect_contains "$out" "config.EffectiveBackground" "a background model that is also a stage's model: the premise, named"
  expect_contains "$out" "a model no stage runs" "a background model that is also a stage's model: what makes it measurable"
  # The false finding: opus's entries DO carry ids (two sub-agents), and a row
  # that counted them would read FAIL and gate T7.2 on its own mistake.
  expect_absent "$out" "FACT background-none: FAIL — the background model opus made" "a background model that is also a stage's model: no call count is taken"
  expect_absent "$out" "carry an agent_id" "a background model that is also a stage's model: no id is counted against the stage"
  expect_contains "$out" "FACT u4: FAIL — U4's first two parts hold" "a background model that is also a stage's model: U4 is not answered whole"
  expect_contains "$err" "could NOT measure background-none" "a background model that is also a stage's model: the notice"
  expect_contains "$out" "the background model named (opus) not counted" "a background model that is also a stage's model: the paste-back sentence"
  assert_read_only "$dir" "a background model that is also a stage's model"
}

# The branch above is the fix for a name that is also a stage's model: a copy
# that never takes it counts the stage's calls as background calls and reads the
# false FAIL the real script must not read.
test_the_stage_model_collision_row_is_load_bearing() {
  local dir=$work/background-stage-model-mutation
  prepare "$dir"
  local copy=$dir/check139-mutated.sh
  sed 's/elif \[ -n "$bg_stage" \]; then/elif [ -z "$bg_stage" ]; then/' "$script" >"$copy"
  grep -q 'elif \[ -z "$bg_stage" \]; then' "$copy" || fail "the mutation: the copy was not bent (the line moved)"
  run_copy "$dir" "$copy" -- --run "$run_id" --bg-model opus
  expect_status "$status" 1 "the mutation: the bent copy reads the false FAIL"
  expect_contains "$out" "FACT background-none: FAIL — the background model opus made 3 call(s) and 2 of them carry an agent_id" "the mutation: the bent copy counts the stage's calls"
  expect_contains "$out" "FACT u4: FAIL — a distinct-agent count is NOT a sub-agent count" "the mutation: the bent copy gates T7.2 on it"
  # And the real script, on the same fixture, takes the row instead.
  run_check "$dir" -- --run "$run_id" --bg-model opus
  expect_status "$status" 2 "the real script: the row is not taken"
  expect_absent "$out" "FACT u4: FAIL — a distinct-agent count is NOT" "the real script: no false finding"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK139_KEEP_SCRATCH=1 -- --run "$run_id" --bg-model haiku
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$out" "CHECK 139 RESULT END (exit 1)" "a kept scratch directory: a row that failed is not a clean exit"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named no scratch directory"
  [ -d "$scratch" ] || fail "a kept scratch directory: it was removed anyway"
  rm -rf "$scratch"
}

# A kept scratch directory is the only way a file the check wrote can be looked
# at after the run, so it is the only way to hold the promise the header makes
# about files: what survives the read is the counts, and no agent id with them.
test_a_kept_scratch_directory_holds_no_id() {
  local dir=$work/kept-ids
  prepare "$dir"
  run_check "$dir" CHECK139_KEEP_SCRATCH=1 -- --run "$run_id" --bg-model haiku
  [ -n "$scratch" ] || fail "a kept scratch and no id: the check named no scratch directory"
  [ -d "$scratch" ] || fail "a kept scratch and no id: it was removed anyway"
  [ -f "$scratch/calls.txt" ] || fail "a kept scratch and no id: the counts the rows are read from are gone too"
  expect_contains "$(cat "$scratch/calls.txt" 2>/dev/null || true)" "entries=5" "a kept scratch and no id: the counts are what is kept"
  assert_ids_never_on_disk "$dir" "a kept scratch and no id"
  rm -rf "$scratch"
}

# The reader's other way out: gcloud answers a document, and it carries an id,
# but it is not the shape this check reads — so the raw output is on disk with
# the ids in it, the row is not measured, and the raw output goes with the answer
# it did not give. The message says so rather than naming a file that is gone.
test_a_kept_scratch_of_a_read_of_another_shape_holds_no_id() {
  local dir=$work/kept-ids-shaped
  prepare "$dir"
  printf '{"entries":[%s]}\n' "$(entry implement opus "$sub_id" "$sub_session" 200 nonzero)" >"$dir/json/calls.json"
  run_check "$dir" CHECK139_KEEP_SCRATCH=1 -- --run "$run_id" --bg-model haiku
  # 1, not 2: keeping the scratch directory is itself the FAIL row that decides
  # the exit, so the unmeasured row beside it does not get to make it 2.
  expect_status "$status" 1 "a kept scratch of a read of another shape"
  expect_contains "$out" "the log entries are not the document this check reads" "a kept scratch of a read of another shape: what happened"
  expect_absent "$out" "calls.out" "a kept scratch of a read of another shape: the message does not point at raw entries that are not kept"
  [ -n "$scratch" ] || fail "a kept scratch of a read of another shape: the check named no scratch directory"
  [ -d "$scratch" ] || fail "a kept scratch of a read of another shape: it was removed anyway"
  assert_ids_never_on_disk "$dir" "a kept scratch of a read of another shape"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown-arg
  prepare "$dir"
  run_check "$dir" -- --nope
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --nope" "an unknown argument: the refusal"
  assert_no_calls "$dir" "an unknown argument"
}

test_a_candidate_count_of_zero_is_refused() {
  local dir=$work/no-candidates
  prepare "$dir"
  run_check "$dir" CHECK139_MAX_CANDIDATES=0
  expect_status "$status" 2 "a candidate count of zero"
  expect_contains "$err" "CHECK139_MAX_CANDIDATES is 0" "a candidate count of zero: the refusal"
  assert_no_reads "$dir" "a candidate count of zero"
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
t test_dry_run
t test_happy_path
t test_the_block_prints_every_row_in_order
t test_the_run_named_with_run_is_read_without_a_listing
t test_no_run_to_read
t test_a_listing_whose_gateway_runs_carry_no_subagent_is_no_run_to_read
t test_a_listing_that_cannot_be_read_is_not_measured
t test_a_listing_that_is_not_a_document_is_not_measured
t test_no_entry_at_all_is_not_measured
t test_a_read_at_the_limit_is_not_measured
t test_a_run_with_no_subagent_is_not_measured
t test_a_logging_read_that_fails_is_not_measured
t test_a_logging_read_that_never_answers_is_not_measured
t test_entries_that_are_not_a_document_are_not_measured
t test_a_stage_name_carrying_two_sessions_is_not_a_finding
t test_a_subagent_id_spanning_two_sessions_is_a_finding
t test_a_subagent_sharing_a_session_with_an_empty_agent_id_is_a_finding
t test_an_id_only_on_refused_entries_is_a_finding
t test_the_refused_ids_row_is_load_bearing
t test_a_background_model_that_carries_an_id_is_a_finding
t test_a_background_model_the_run_never_called_is_not_measured
t test_a_background_model_not_named_is_info
t test_a_record_with_no_stage_agents_is_named_as_absent
t test_a_record_naming_no_execution_is_not_measured
t test_an_execution_the_listing_and_the_record_disagree_about_is_a_finding
t test_a_run_the_installation_does_not_know_is_not_measured
t test_a_diagnose_that_fails_is_not_measured
t test_a_diagnose_that_exits_one_is_still_an_answer
t test_a_diagnose_that_is_not_a_document_is_not_measured
t test_the_log_view_is_read_from_the_project_config
t test_without_a_log_view_the_read_is_project_scoped
t test_the_project_and_the_region_are_hard_coded
t test_a_doctor_that_names_another_project_stops_the_check
t test_a_doctor_that_does_not_answer_stops_the_check
t test_a_coding_agents_session_is_refused
t test_xtrace_refused
t test_an_account_naming_belong_is_refused
t test_a_run_naming_belong_is_refused
t test_a_log_view_naming_belong_is_refused
t test_a_project_config_naming_belongs_log_view_is_refused
t test_a_background_model_that_is_a_stage_model_is_not_counted
t test_the_stage_model_collision_row_is_load_bearing
t test_a_kept_scratch_directory_is_reported
t test_a_kept_scratch_directory_holds_no_id
t test_a_kept_scratch_of_a_read_of_another_shape_holds_no_id
t test_an_unknown_argument_is_refused
t test_a_candidate_count_of_zero_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check139_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check139_test.sh: ok"
