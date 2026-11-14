#!/usr/bin/env bash
# Tests for scripts/live-checks/check492.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a run or a forge, no agent runs
# a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the calls
# the check makes (version, the three `--help` probes, doctor, the two launches,
# the runs list and the cancel — a launch is served per leg, so `--security` is
# answered with a different run) and the fake `gcloud` serves each run's record.
# The cases that matter are the ones where the model is bent: a build older than
# the release, a build whose `fugaro run --help` does not name --security, a
# checkout that turns the step off, a doctor that does not answer, a launch that
# is refused or names no run, a run that never ends, a record that cannot be
# read, is another run's or is not a document, a mode the step does not govern, a
# record with no triage block, a floor finding a verdict deferred (the failure the
# check exists for), a round whose entry is not the floor's at all, a deferral
# that names the floor's own key, a level-3 round that never ran, a level-3 round
# with nothing to hold, and a cancel that does not answer.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check492_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check492.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK492_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK492_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK492_TEST_TIMEOUT:-60}

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

project=fugaro-chk492-test
# The two runs the two legs launch: the first with no level asked for, the second
# with `--security`. The fake `fugaro` serves the second one only when the call
# carries `--security`, and the fake `gcloud` serves each run's own record — so a
# case can tell which leg read what.
default_run=run-20261114100000-1
security_run=run-20261114110000-2
# The repository's slug, as the runs listing carries it: ONE path segment, where
# the repository is named `owner/name`. Each record's object name is built from
# it, so a case that reads a key back is reading the check's choice.
slug=acme-sandbox-9f8e7d6c
repo=acme/sandbox
# Three findings, one per place the floor can hold one: the first run's finding,
# and the second run's finding at its second and third levels. They are distinct
# keys on purpose — a block that read one leg's finding as the other's would pass
# on a fixture that reused one.
floor_key=floorhold000001
sec_key=floorhold000002
sec_key3=floorhold000003
# The table's own sentence for a finding the severity override covers, verbatim
# from internal/triage/apply.go: the rows pass on reading it, so the fixture and
# the check must not drift from it.
floor_reason="the floor holds this finding: no verdict may defer it"
# A reason that is NOT the floor's: the row is about the floor holding a finding,
# and an entry with some other reason is not one.
other_reason="the change is self-contained and the reviewer is content"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check492.sh's test only. It
# answers the account read the check makes and serves the record of the run the
# object key ends in. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk492-owner@belong.example\n' ;;
    *) printf 'chk492-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    object=${3:-}
    case "$object" in
    */result.json) ;;
    *)
      echo "gcloud: unexpected object ${object:-none}" >&2
      exit 1
      ;;
    esac
    run=${object%/result.json}
    run=${run##*/}
    printf '%s\n' "$run" >>"$FAKE_LOG_DIR/record.calls"
    case "$mode:$run" in
    record-missing:"$FAKE_DEFAULT_RUN" | record-hangs:"$FAKE_DEFAULT_RUN")
      if [ "$mode" = record-hangs ]; then sleep 5; exit 0; fi
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
      ;;
    esac
    # The record of the state this case is about, when it wrote one: the bent
    # document is named after the state, so a case bends one leg's record by
    # naming the state rather than by rewriting the file the other leg reads.
    doc="$FAKE_JSON_DIR/record-$run-$mode.json"
    [ -f "$doc" ] || doc="$FAKE_JSON_DIR/record-$run.json"
    if [ -f "$doc" ]; then
      cat "$doc"
    else
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
    fi
  fi
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check492.sh's test only. The
# `--json` reads come from files the case wrote, the three `--help` probes print
# the usage lines the real commands print, a launch is served per leg (the call
# that carries `--security` is the second run), and every call is logged with the
# directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.23.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.24.0\n' ;;
  esac
  ;;
followups)
  case " $* " in
  *" --help "*)
    case "$mode" in
    no-feature)
      printf 'fugaro: unknown command "followups"\n' >&2
      exit 1
      ;;
    *) printf 'Usage:\n  fugaro followups RUN [--launch] [--max N] [--json]\n' ;;
    esac
    exit 0
    ;;
  esac
  printf 'fugaro: unexpected followups argument\n' >&2
  exit 1
  ;;
run)
  case " $* " in
  *" --help "*)
    case "$mode" in
    no-security-flag) printf 'Usage:\n  fugaro run [--repo R] [--mode M] [--json] TASK\n' ;;
    *) printf 'Usage:\n  fugaro run [--repo R] [--mode M] [--security] [--json] TASK\n' ;;
    esac
    exit 0
    ;;
  esac
  leg=default
  case " $* " in
  *" --security "*) leg=security ;;
  esac
  printf 'run %s\n' "$leg" >>"$FAKE_LOG_DIR/run.calls"
  case "$mode:$leg" in
  launch-refused:* | launch-security-refused:security | launch-default-refused:default)
    printf 'fugaro: the sandbox refused to start a run\n' >&2
    exit 1
    ;;
  launch-hangs:* | launch-hangs-security:security)
    sleep 5
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/launch-$leg.json"
  ;;
runs)
  case "${2:-}" in
  ls)
    case " $* " in
    *" --help "*)
      case "$mode" in
      no-feature | no-debt-flag) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N]\n' ;;
      *) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N] [--debt]\n' ;;
      esac
      exit 0
      ;;
    esac
    printf 'ls %s\n' "$*" >>"$FAKE_LOG_DIR/ls.calls"
    n=$(wc -l <"$FAKE_LOG_DIR/ls.calls")
    case "$mode" in
    rows-hangs)
      sleep 5
      exit 0
      ;;
    rows-hangs-second-leg)
      # The first leg's listing answers at once; the second leg's never does. The
      # count is the number of listings the check has asked for, which is what
      # tells the two legs apart when both calls carry the same arguments.
      if [ "$n" -ge 2 ]; then sleep 5; exit 0; fi
      ;;
    esac
    cat "$FAKE_JSON_DIR/rows.json"
    ;;
  *)
    echo "fugaro: unexpected runs subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
doctor)
  case "$mode" in
  doctor-fails)
    printf 'fugaro: the installation could not be read\n' >&2
    exit 1
    ;;
  doctor-hangs)
    sleep 5
    exit 0
    ;;
  doctor-no-project)
    printf '{"version":1,"ok":true,"checks":[],"project":{}}\n'
    exit 0
    ;;
  belong-project)
    printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-belong-sandbox","region":"us-east5"}}\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
cancel)
  leg=default
  case "$*" in
  *"$FAKE_SECURITY_RUN"*) leg=security ;;
  esac
  printf 'cancel %s\n' "$leg" >>"$FAKE_LOG_DIR/cancel.calls"
  case "$mode:$leg" in
  cancel-fails:* | cancel-fails-security:security)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  cancel-hangs:* | cancel-hangs-security:security)
    sleep 5
    exit 0
    ;;
  esac
  printf '{"project":"%s","run":"%s","status":"cancelled"}\n' "${FAKE_PROJECT:-p}" "$leg"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk492","gcp_project":"$project","region":"us-east5"}}
JSON
  write_launch "$dir" default
  write_launch "$dir" security
  write_rows "$dir" ok
  write_record "$dir" "$default_run" ok
  write_record "$dir" "$security_run" ok
}

# A `--json` document is written and then read back by python3 before a case can
# use it: a fixture that is not a document would make a case pass or fail for a
# reason of its own, and the check's own reader would refuse it in a way that
# looks like a finding about the release.
as_document() { # as_document FILE
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$1" 2>/dev/null ||
    fail "the fixture $1 is not JSON: the case using it would measure the fixture rather than the check"
}

# write_launch DIR LEG: the document `fugaro run --json` answers the leg with.
write_launch() { # write_launch DIR LEG
  local dir=$1 leg=$2 run=$default_run
  [ "$leg" = security ] && run=$security_run
  cat >"$dir/json/launch-$leg.json" <<JSON
{"version":1,"run_id":"$run","repo":"$repo","status":"running","branch":"fugaro/20261114-100000-abcd","pr":0}
JSON
  as_document "$dir/json/launch-$leg.json"
}

# write_rows DIR STATE: the listing `fugaro runs ls --json --limit 1` answers
# with. Both runs' rows are in it — each leg reads its own by id — so a state
# that leaves one run out is a listing that does not know it yet.
write_rows() { # write_rows DIR STATE
  local dir=$1 state=$2
  local d_terminal=true d_status=succeeded d_stage=finalize
  local s_terminal=true s_status=succeeded s_stage=finalize
  case "$state" in
  ok) ;;
  no-run)
    # The default run's row is not in the listing at all: the check follows it by
    # id, and an id no row carries is a run the check cannot see end.
    cat >"$dir/json/rows.json" <<JSON
{"version":1,"project":"$project","runs":[{"run_id":"$security_run","run":"$slug/$security_run","repo":"$repo","status":"succeeded","terminal":true,"stage":"finalize","pr":80,"pr_url":"https://github.com/$repo/pull/80","outcome":"draft"}]}
JSON
    as_document "$dir/json/rows.json"
    return 0
    ;;
  default-not-terminal)
    d_terminal=false d_status=running d_stage=review
    ;;
  security-not-terminal)
    s_terminal=false s_status=running s_stage=review
    ;;
  esac
  cat >"$dir/json/rows.json" <<JSON
{"version":1,"project":"$project","runs":[
{"run_id":"$default_run","run":"$slug/$default_run","repo":"$repo","status":"$d_status","terminal":$d_terminal,"stage":"$d_stage","pr":78,"pr_url":"https://github.com/$repo/pull/78","outcome":"draft"},
{"run_id":"$security_run","run":"$slug/$security_run","repo":"$repo","status":"$s_status","terminal":$s_terminal,"stage":"$s_stage","pr":80,"pr_url":"https://github.com/$repo/pull/80","outcome":"draft"}]}
JSON
  as_document "$dir/json/rows.json"
}

# write_record DIR RUN STATE: one run's `result.json`, as the bucket holds it.
# `ok` is what the surface between the runs looks like — a Startup run that
# reported the exploit the task asks for, whose triage round places it as `block`
# from the table with the floor's own sentence, and which left no deferral — and
# `ok` on the second run carries a second triage round at level 3. Every other
# state is one thing about that surface bent.
write_record() { # write_record DIR RUN STATE
  local dir=$1 run=$2 state=$3
  local rigor='{"mode":"startup","from":"mode","at":"2026-11-14T10:00:00Z"}'
  local reviews='[{"round":1,"tier":"standard","verdict":"changes_requested","findings":1,"head":"abc1234"},{"round":2,"tier":"standard","verdict":"pass","findings":0,"head":"def5678"}]'
  local status=succeeded outcome=draft cost=0.42 recorded=$run
  local l2key=$floor_key l2verdict=block l2source=table l2reason="$floor_reason"
  local l3=no l3verdict=block l3source=table l3entries=one
  local deferred='[]' deferred_total=0 triage=yes level=l2
  if [ "$run" = "$security_run" ]; then
    l2key=$sec_key
    # The second run carries the request, so its record carries the level-3 round
    # the request turns on.
    l3=yes
    level=l2
  fi
  case "$state" in
  ok) ;;
  foreign) recorded=run-20261114120000-9 ;;
  not-a-document) printf 'not a document at all\n' >"$dir/json/record-$run-$state.json" && return 0 ;;
  hotfix) rigor='{"mode":"hotfix","from":"area","at":"2026-11-14T10:00:00Z"}' ;;
  no-triage) triage=no ;;
  defer)
    l2verdict=defer
    l2source=agent
    deferred='[{"key":"'"$l2key"'","summary":"a shell argument is interpolated into a command","reason":"a follow-up task","group":"","source":"agent"}]'
    deferred_total=1
    ;;
  no-floor) l2reason="$other_reason" ;;
  deferred-floor)
    deferred='[{"key":"'"$l2key"'","summary":"a shell argument is interpolated into a command","reason":"a follow-up task","group":"","source":"agent"}]'
    deferred_total=1
    ;;
  l3-empty) l3entries=empty ;;
  no-l3) l3=no ;;
  esac
  local t1='{"round":1,"mode":"startup","level":"'"$level"'","model":"triage","account":"chk492-owner@example.com","failed":false,"total":1,"entries":[{"index":1,"key":"'"$l2key"'","verdict":"'"$l2verdict"'","reason":"'"$l2reason"'","group":"","source":"'"$l2source"'"}]}'
  local l3entries_json
  if [ "$l3entries" = empty ]; then
    l3entries_json='[]'
  else
    l3entries_json='[{"index":1,"key":"'"$sec_key3"'","verdict":"'"$l3verdict"'","reason":"'"$floor_reason"'","group":"","source":"'"$l3source"'"}]'
  fi
  local t2='{"round":2,"mode":"startup","level":"l3","model":"triage","account":"chk492-owner@example.com","failed":false,"total":1,"entries":'"$l3entries_json"'}'
  local rounds="[$t1]"
  [ "$l3" = yes ] && rounds="[$t1,$t2]"
  local triage_field=""
  if [ "$triage" = yes ]; then
    triage_field='"triage":{"rounds":'"$rounds"'},'
  fi
  local out="$dir/json/record-$run.json"
  [ "$state" = ok ] || out="$dir/json/record-$run-$state.json"
  cat >"$out" <<JSON
{"version":1,"run_id":"$recorded","repo":"$repo","workflow":"agent","status":"$status","stage":"finalize","outcome":"$outcome",$triage_field"rigor":$rigor,"cost":{"model_usd":$cost,"total_usd":$cost},"pr":78,"pr_url":"https://github.com/$repo/pull/78","stages":[{"name":"bootstrap","status":"succeeded"},{"name":"implement","status":"succeeded"},{"name":"review","status":"succeeded"},{"name":"finalize","status":"succeeded"}],"reviews":$reviews,"deferred":{"findings":$deferred,"total":$deferred_total}}
JSON
  as_document "$out"
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir [SWITCH]
  mkdir -p "$1"
  if [ "${2:-}" = off ]; then
    cat >"$1/fugaro.yaml" <<'YAML'
project: chk492
agent:
  triage: false
YAML
  else
    cat >"$1/fugaro.yaml" <<'YAML'
project: chk492
agent:
  triage: true
YAML
  fi
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
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
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/cancel.calls"
  : >"$dir/logs/run.calls"
  : >"$dir/logs/ls.calls"
  : >"$dir/logs/record.calls"
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
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_PROJECT="$project" \
    FAKE_DEFAULT_RUN="$default_run" FAKE_SECURITY_RUN="$security_run" \
    CHECK492_SANDBOX="$dir/sandbox" CHECK492_YES=1 \
    CHECK492_POLL_SECONDS=1 CHECK492_RUN_TIMEOUT_SECONDS=2 \
    CHECK492_CLI_TIMEOUT_SECONDS=2 CHECK492_LAUNCH_TIMEOUT_SECONDS=5 \
    CHECK492_CANCEL_TIMEOUT_SECONDS=5 CHECK492_DOCTOR_TIMEOUT_SECONDS=5 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_scratch_kept() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ -e "$scratch" ] || fail "$1: the scratch directory $scratch was removed"
}

# assert_no_run STARTED: the launches the check made, read from the fake's log —
# `run default` and `run security` tell the two legs apart.
assert_legs_launched() { # dir what WANT
  local got
  got=$(cat "$1/logs/run.calls" 2>/dev/null)
  [ "$got" = "$3" ] || fail "$2: expected the legs '$3' to be launched; got: $(printf '%s' "$got" | tr '\n' ' ')"
}

assert_no_launch() { # dir what
  expect_absent "$(cat "$1/logs/run.calls")" "run" "$2: nothing is launched"
}

assert_no_cancel() { # dir what
  expect_absent "$(cat "$1/logs/cancel.calls")" "cancel" "$2: no run is cancelled"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check492.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that it is theirs to run and that it starts real runs.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "TWO sandbox runs" "--help: the cost is stated"
  expect_contains "$out" "whose whole point is an exploit" "--help: the change the task asks for"
  expect_contains "$out" "carries no per-finding class" "--help: what the record cannot say about the class"
  assert_no_launch "$dir" "--help"
  assert_no_cancel "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK492_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 492 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the cost is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 492 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT run-default: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT level3-security: INFO — planned" "dry run: the level-3 row is planned too"
  expect_contains "$err" "BILLABLE" "dry run: the plan says the runs cost money"
  expect_contains "$err" "from the exit trap" "dry run: the plan says what happens to a run that does not end"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  assert_no_launch "$dir" "dry run"
  assert_no_cancel "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nope
  expect_status "$status" 2 "unknown argument"
  expect_contains "$err" "unknown argument: --nope" "unknown argument: the refusal"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK492_SANDBOX=
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "Check 492 needs --sandbox" "no --sandbox: the refusal"
  expect_absent "$out" "FACT " "no --sandbox: no block is printed for a check that never started"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/badpath
  prepare "$dir"
  run_check "$dir" CHECK492_SANDBOX="$dir/nothing-here"
  expect_status "$status" 2 "not a checkout"
  expect_contains "$err" "is not a directory" "not a checkout: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "not a checkout: the installation is not read"
}

test_the_two_runs_need_yes() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK492_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "so it asks for --yes" "no --yes: the refusal"
  expect_contains "$err" "TWO sandbox runs" "no --yes: what it would have spent"
  assert_no_launch "$dir" "no --yes"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT premise: PASS" "happy path: the repository leaves the step on"
  expect_contains "$out" "no agent.triage: false" "happy path: what the premise read"
  expect_contains "$out" "FACT class: PASS" "happy path: the class row"
  expect_contains "$out" "neither carries the class the reviewer scored" "happy path: the record cannot confirm the word"
  expect_contains "$out" "FACT run-default: PASS" "happy path: the first launch"
  expect_contains "$out" "FACT run-security: PASS" "happy path: the second launch"
  expect_contains "$out" "FACT terminal-default: PASS" "happy path: the first run ends"
  expect_contains "$out" "FACT terminal-security: PASS" "happy path: the second run ends"
  expect_contains "$out" "FACT record-default: PASS" "happy path: the first record"
  expect_contains "$out" "FACT record-security: PASS" "happy path: the second record"
  expect_contains "$out" "FACT mode-default: PASS" "happy path: the mode the first run resolved"
  expect_contains "$out" "FACT mode-security: PASS" "happy path: the mode the second run resolved"
  expect_contains "$out" "FACT triage-default: PASS" "happy path: the floor holds the finding"
  expect_contains "$out" "each reading 'block' from source 'table'" "happy path: what the floor's hold reads as"
  expect_contains "$out" "FACT deferred-default: PASS" "happy path: no deferral took effect"
  expect_contains "$out" "FACT triage-security: PASS" "happy path: the second run's floor"
  expect_contains "$out" "FACT level3-security: PASS" "happy path: the level the request turned on"
  expect_contains "$out" "FACT deferred-security: PASS" "happy path: the second run defers nothing"
  expect_contains "$out" "FACT cost: PASS" "happy path: the cost"
  expect_contains "$out" "0.84" "happy path: both records' money, summed"
  expect_contains "$out" "FACT cancelled: INFO" "happy path: the runs ended on their own"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "LEFT OPEN" "happy path: the pull requests are left alone"
  expect_contains "$out" "CHECK 492 RESULT END (exit 0)" "happy path: the end line"
  # Both legs were launched, and each reads its own run's record: the keys the
  # two blocks hold are not each other's.
  assert_legs_launched "$dir" "happy path" $'run default\nrun security'
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "run --repo $dir/sandbox --mode startup --json" "happy path: the first run is launched with no level asked for"
  expect_contains "$calls" "run --repo $dir/sandbox --mode startup --security --json" "happy path: the second asks for level 3"
  expect_contains "$calls" "(in $dir/sandbox)" "happy path: the commands run in the checkout"
  expect_absent "$calls" "(in $dir/other)" "happy path: nothing runs in another checkout"
  local records
  records=$(cat "$dir/logs/record.calls")
  expect_contains "$records" "$default_run" "happy path: the first run's record is read"
  expect_contains "$records" "$security_run" "happy path: the second run's record is read"
  expect_contains "$out" "$floor_key" "happy path: the first run's own key is named"
  expect_contains "$out" "$sec_key" "happy path: the second run's own key is named"
  assert_scratch_removed "$dir" "happy path"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: why"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "xtrace: the check stops before it reads anything"
  assert_no_launch "$dir" "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "agent session"
  expect_contains "$out" "FACT agent-session: FAIL" "agent session: the row"
  expect_contains "$out" "CLAUDECODE is set" "agent session: the marker it read"
  assert_no_launch "$dir" "agent session"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongaccount
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "belong account"
  expect_contains "$out" "FACT belong: FAIL" "belong account: the row"
  expect_contains "$out" "names belong" "belong account: why"
  assert_no_launch "$dir" "belong account"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belongproject
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-project
  expect_status "$status" 1 "belong project"
  expect_contains "$out" "FACT sandbox: FAIL" "belong project: the row"
  expect_contains "$out" "fugaro-belong-sandbox" "belong project: the project it refused"
  assert_no_launch "$dir" "belong project"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "old build"
  expect_contains "$out" "FACT release: FAIL" "old build: the row"
  expect_contains "$out" "0.23.0 predates release 0.24.0" "old build: the release it names"
  expect_contains "$out" "fugaro update" "old build: the one command that updates it"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "old build: nothing is read beyond the version"
  assert_no_launch "$dir" "old build"
}

test_a_build_whose_run_help_does_not_name_security_is_refused() {
  # The second run's request is the level-3 probe: a build whose `run --help`
  # does not name --security cannot be asked for the level this check grades.
  local dir=$work/nosecurity
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-security-flag
  expect_status "$status" 1 "no --security"
  expect_contains "$out" "FACT release: FAIL" "no --security: the row"
  expect_contains "$out" "does not name --security" "no --security: the probe that failed"
  assert_no_launch "$dir" "no --security"
}

test_a_build_without_the_debt_listing_is_refused() {
  local dir=$work/nodebt
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-debt-flag
  expect_status "$status" 1 "no --debt"
  expect_contains "$out" "FACT release: FAIL" "no --debt: the row"
  expect_contains "$out" "does not name --debt" "no --debt: the probe that failed"
  assert_no_launch "$dir" "no --debt"
}

test_a_build_without_the_feature_is_refused() {
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "no feature"
  expect_contains "$out" "FACT release: FAIL" "no feature: the row"
  expect_contains "$out" "does not answer" "no feature: the probe that failed"
  assert_no_launch "$dir" "no feature"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/devbuild
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "dev build"
  expect_contains "$out" "FACT release: PASS" "dev build: the row"
  expect_contains "$out" "not a release number" "dev build: why the probes decide"
  expect_contains "$out" "FACT triage-default: PASS" "dev build: the check still measures"
}

test_a_checkout_that_turns_the_step_off_stops_the_check() {
  local dir=$work/switch
  prepare "$dir"
  write_checkout "$dir/sandbox" off
  run_check "$dir"
  expect_status "$status" 1 "premise off"
  expect_contains "$out" "FACT premise: FAIL" "premise off: the row"
  expect_contains "$out" "turns the step off" "premise off: why"
  expect_contains "$out" "FACT run-default: FAIL — nothing was launched" "premise off: the runs are not launched"
  assert_no_launch "$dir" "premise off"
  assert_scratch_removed "$dir" "premise off"
}

test_a_doctor_that_does_not_answer_is_unmeasured() {
  local dir=$work/doctor
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fails
  expect_status "$status" 2 "doctor fails"
  expect_contains "$out" "FACT sandbox: FAIL" "doctor fails: the row"
  expect_contains "$err" "could NOT measure" "doctor fails: the run says so"
  assert_no_launch "$dir" "doctor fails"
}

test_a_doctor_with_no_project_is_unmeasured() {
  local dir=$work/noproject
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "no project"
  expect_contains "$out" "FACT sandbox: FAIL" "no project: the row"
  expect_contains "$out" "without a project block" "no project: what was missing"
  assert_no_launch "$dir" "no project"
}

test_a_launch_that_is_refused_is_a_finding() {
  local dir=$work/launchrefused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-refused
  expect_status "$status" 1 "launch refused"
  expect_contains "$out" "FACT run-default: FAIL" "launch refused: the row"
  expect_contains "$out" "exited 1" "launch refused: the status it read"
  # The second leg is not launched on top of a first that was not graded: the
  # check does not spend a second run on a comparison it cannot make.
  assert_legs_launched "$dir" "launch refused" "run default"
  expect_contains "$out" "FACT run-security: FAIL — nothing was launched" "launch refused: the second leg is not asked for"
  assert_no_cancel "$dir" "launch refused"
  assert_scratch_removed "$dir" "launch refused"
}

test_a_launch_that_names_no_run_is_a_finding() {
  local dir=$work/norunid
  prepare "$dir"
  cat >"$dir/json/launch-default.json" <<JSON
{"version":1,"run_id":"","repo":"$repo","status":"running","pr":0}
JSON
  run_check "$dir"
  expect_status "$status" 1 "no run id"
  expect_contains "$out" "FACT run-default: FAIL" "no run id: the row"
  expect_contains "$out" "named no run" "no run id: why"
  expect_contains "$out" "FACT terminal-default: FAIL — there is no run to follow" "no run id: nothing is followed"
  assert_no_cancel "$dir" "no run id"
  assert_scratch_removed "$dir" "no run id"
}

test_a_run_that_never_ends_is_unmeasured_and_cancelled() {
  local dir=$work/hangs
  prepare "$dir"
  write_rows "$dir" default-not-terminal
  run_check "$dir"
  expect_status "$status" 2 "never ends"
  expect_contains "$out" "FACT terminal-default: FAIL" "never ends: the row"
  expect_contains "$out" "had not ended after 2s" "never ends: how long it waited"
  # A run this check started and could not finish is cancelled from the exit
  # trap: nothing it launches outlives it.
  expect_contains "$out" "FACT cancelled: PASS" "never ends: the run is cancelled"
  expect_contains "$(cat "$dir/logs/cancel.calls")" "cancel default" "never ends: the run itself"
  expect_absent "$(cat "$dir/logs/cancel.calls")" "cancel security" "never ends: the second leg is not launched, so there is nothing more to cancel"
  assert_scratch_removed "$dir" "never ends"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "no record"
  expect_contains "$out" "FACT record-default: FAIL" "no record: the row"
  expect_contains "$out" "could not be read" "no record: why"
  expect_contains "$out" "FACT triage-default: FAIL — this run's reading is not graded" "no record: the rows past it are not graded"
  assert_scratch_removed "$dir" "no record"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/foreign
  prepare "$dir"
  write_record "$dir" "$default_run" foreign
  run_check "$dir" FAKE_MODE=foreign
  expect_status "$status" 1 "another run's record"
  expect_contains "$out" "FACT record-default: FAIL" "another run's record: the row"
  expect_contains "$out" "run-20261114120000-9" "another run's record: the id it found"
  expect_contains "$out" "is another run's" "another run's record: why"
  assert_scratch_removed "$dir" "another run's record"
}

test_a_record_that_is_not_a_document_is_a_finding() {
  local dir=$work/notdoc
  prepare "$dir"
  write_record "$dir" "$default_run" not-a-document
  run_check "$dir" FAKE_MODE=not-a-document
  expect_status "$status" 1 "not a document"
  expect_contains "$out" "FACT record-default: FAIL" "not a document: the row"
  expect_contains "$out" "is not a document this check could read" "not a document: why"
  assert_scratch_removed "$dir" "not a document"
}

test_a_mode_the_step_does_not_govern_is_a_finding() {
  local dir=$work/hotfix
  prepare "$dir"
  write_record "$dir" "$default_run" hotfix
  run_check "$dir" FAKE_MODE=hotfix
  expect_status "$status" 1 "hotfix mode"
  expect_contains "$out" "FACT mode-default: FAIL" "hotfix mode: the row"
  expect_contains "$out" "mode hotfix" "hotfix mode: the mode it read"
  expect_contains "$out" "governs neither" "hotfix mode: why the block cannot be read"
  assert_scratch_removed "$dir" "hotfix mode"
}

test_a_record_with_no_triage_block_is_a_finding() {
  local dir=$work/notriage
  prepare "$dir"
  write_record "$dir" "$default_run" no-triage
  run_check "$dir" FAKE_MODE=no-triage
  expect_status "$status" 1 "no triage block"
  expect_contains "$out" "FACT triage-default: FAIL" "no triage block: the row"
  expect_contains "$out" "no triage block at all" "no triage block: why"
  assert_scratch_removed "$dir" "no triage block"
}

test_a_floor_finding_a_verdict_deferred_is_a_finding() {
  # The failure this check exists for: the reviewer reported the exploit, the
  # triage step's verdict moved it, and the run left it behind as a follow-up.
  local dir=$work/defer
  prepare "$dir"
  write_record "$dir" "$default_run" defer
  run_check "$dir" FAKE_MODE=defer
  expect_status "$status" 1 "a deferred floor finding"
  expect_contains "$out" "FACT triage-default: FAIL" "a deferred floor finding: the row"
  expect_contains "$out" "as 'defer' from source 'agent'" "a deferred floor finding: the verdict it read"
  expect_contains "$out" "FACT deferred-default: FAIL" "a deferred floor finding: the deferral took effect"
  expect_contains "$out" "$floor_key" "a deferred floor finding: the key is named"
  expect_contains "$out" "file it as work somebody may merge around" "a deferred floor finding: what it would cost"
  assert_scratch_removed "$dir" "a deferred floor finding"
}

test_a_round_with_no_floor_entry_is_a_finding() {
  local dir=$work/nofloor
  prepare "$dir"
  write_record "$dir" "$default_run" no-floor
  run_check "$dir" FAKE_MODE=no-floor
  expect_status "$status" 1 "no floor entry"
  expect_contains "$out" "FACT triage-default: FAIL" "no floor entry: the row"
  expect_contains "$out" "no entry of the triage block carries the floor's own sentence" "no floor entry: why"
  expect_contains "$out" "FACT deferred-default: FAIL — this run's reading is not graded" "no floor entry: no deferral is compared"
  assert_scratch_removed "$dir" "no floor entry"
}

test_a_deferral_naming_the_floor_key_is_a_finding() {
  # The block says `block` and the run deferred it anyway: the two readings are
  # of the same record, so a check that read only the first would pass this.
  local dir=$work/deferredfloor
  prepare "$dir"
  write_record "$dir" "$default_run" deferred-floor
  run_check "$dir" FAKE_MODE=deferred-floor
  expect_status "$status" 1 "a deferral of the floor's key"
  expect_contains "$out" "FACT triage-default: PASS" "a deferral of the floor's key: the block reads block"
  expect_contains "$out" "FACT deferred-default: FAIL" "a deferral of the floor's key: the deferral is the finding"
  expect_contains "$out" "$floor_key" "a deferral of the floor's key: the key is named"
  assert_scratch_removed "$dir" "a deferral of the floor's key"
}

test_a_level_3_round_that_never_ran_is_a_finding() {
  local dir=$work/nol3
  prepare "$dir"
  write_record "$dir" "$security_run" no-l3
  run_check "$dir" FAKE_MODE=no-l3
  expect_status "$status" 1 "no level-3 round"
  expect_contains "$out" "FACT level3-security: FAIL" "no level-3 round: the row"
  expect_contains "$out" "holds no round at level l3" "no level-3 round: why"
  expect_contains "$out" "D1847" "no level-3 round: the ruling it reads against"
  assert_scratch_removed "$dir" "no level-3 round"
}

test_a_level_3_round_with_nothing_to_hold_reads_plainly() {
  # The adversarial round reported nothing: the row must say so rather than read
  # a pass out of an empty list.
  local dir=$work/l3empty
  prepare "$dir"
  write_record "$dir" "$security_run" l3-empty
  run_check "$dir" FAKE_MODE=l3-empty
  expect_status "$status" 0 "level-3 round with nothing to hold"
  expect_contains "$out" "FACT level3-security: PASS" "level-3 round with nothing to hold: the row"
  expect_contains "$out" "does NOT read the floor holding AT level 3" "level-3 round with nothing to hold: it says what it cannot read"
  expect_contains "$out" "FACT deferred-security: PASS" "level-3 round with nothing to hold: the second run still defers nothing"
  assert_scratch_removed "$dir" "level-3 round with nothing to hold"
}

test_a_cost_over_the_bound_is_a_finding() {
  local dir=$work/cost
  prepare "$dir"
  run_check "$dir" CHECK492_MAX_MODEL_USD=0.5
  expect_status "$status" 1 "over the bound"
  expect_contains "$out" "FACT cost: FAIL" "over the bound: the row"
  expect_contains "$out" "over CHECK492_MAX_MODEL_USD=0.5" "over the bound: the bound it names"
  assert_scratch_removed "$dir" "over the bound"
}

test_a_cancel_that_does_not_answer_is_a_finding() {
  local dir=$work/cancelfails
  prepare "$dir"
  write_rows "$dir" default-not-terminal
  run_check "$dir" FAKE_MODE=cancel-fails
  # 1, not 2: a run that did not answer is a criterion that did not hold, and a
  # block with a failure in it is not one that could not measure.
  expect_status "$status" 1 "cancel fails"
  expect_contains "$out" "FACT cancelled: FAIL" "cancel fails: the row"
  expect_contains "$out" "did not answer" "cancel fails: why"
  expect_contains "$out" "FACT restore: FAIL" "cancel fails: the restore says the run may still be going"
  expect_contains "$out" "cancel it yourself" "cancel fails: what the owner does next"
  assert_scratch_removed "$dir" "cancel fails"
}

test_keep_scratch_leaves_the_directory() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK492_KEEP_SCRATCH=1
  expect_status "$status" 1 "kept scratch"
  expect_contains "$out" "FACT scratch-removed: FAIL" "kept scratch: the row"
  expect_contains "$out" "is still there, at" "kept scratch: where"
  assert_scratch_kept "kept scratch"
  rm -rf "$scratch"
}

test_the_floor_row_is_load_bearing() {
  # The happy path is read once more with the one thing under it bent: a record
  # whose triage entry is not the floor's at all. A case that bent nothing would
  # pass whatever the row said.
  local dir=$work/loadbearing
  prepare "$dir"
  write_record "$dir" "$default_run" no-floor
  run_check "$dir" FAKE_MODE=no-floor
  expect_status "$status" 1 "load-bearing: the bent fixture is a finding"
  expect_contains "$out" "FACT triage-default: FAIL" "load-bearing: the floor's own sentence is what passes"
}

t() { "$1"; }
t test_help
t test_dry_run
t test_an_unknown_argument_is_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_the_two_runs_need_yes
t test_happy_path
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_build_whose_run_help_does_not_name_security_is_refused
t test_a_build_without_the_debt_listing_is_refused
t test_a_build_without_the_feature_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_checkout_that_turns_the_step_off_stops_the_check
t test_a_doctor_that_does_not_answer_is_unmeasured
t test_a_doctor_with_no_project_is_unmeasured
t test_a_launch_that_is_refused_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_unmeasured_and_cancelled
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_record_that_is_not_a_document_is_a_finding
t test_a_mode_the_step_does_not_govern_is_a_finding
t test_a_record_with_no_triage_block_is_a_finding
t test_a_floor_finding_a_verdict_deferred_is_a_finding
t test_a_round_with_no_floor_entry_is_a_finding
t test_a_deferral_naming_the_floor_key_is_a_finding
t test_a_level_3_round_that_never_ran_is_a_finding
t test_a_level_3_round_with_nothing_to_hold_reads_plainly
t test_a_cost_over_the_bound_is_a_finding
t test_a_cancel_that_does_not_answer_is_a_finding
t test_keep_scratch_leaves_the_directory
t test_the_floor_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check492_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check492_test.sh: ok"
