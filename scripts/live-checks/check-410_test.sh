#!/usr/bin/env bash
# Tests for scripts/live-checks/check-410.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, an installation or a model, no
# agent runs a live command, and the fakes record every call they are given so a
# case can assert what the check did — and did not — do.
#
# The fakes are a small model of one machine: the fake `fugaro` fails an update
# the way the real one does (its log in the state directory it was given, its
# failure line on the way out) and answers a diagnosis the way the real one does
# (an outcome line and the evidence folder), and the fake `gcloud` answers the
# one read the check makes about the project it made up. The cases that matter
# are the ones where that model is bent — a failure line that names nothing, a
# diagnosis that never ran, an update that succeeded where it cannot.
#
# Usage: bash scripts/live-checks/check-410_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-410.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK410_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK410_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk410-test
probe_id=update-20261111172639-48213

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-410.sh's test only. It
# answers the two reads the check makes and nothing else. Nothing here reaches a
# real project.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    no-account) : ;;
    *) printf 'chk410-owner@example.com\n' ;;
    esac
  fi
  ;;
projects)
  if [ "${2:-}" = "describe" ]; then
    case "$mode" in
    project-exists)
      printf '{"projectId":"%s","lifecycleState":"ACTIVE"}\n' "${3:-}"
      ;;
    project-unreadable)
      echo "ERROR: (gcloud.projects.describe) PERMISSION_DENIED: the caller does not have permission" >&2
      exit 1
      ;;
    *)
      echo "ERROR: (gcloud.projects.describe) NOT_FOUND: Requested entity was not found." >&2
      exit 1
      ;;
    esac
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
# A stand-in for `fugaro`, for scripts/live-checks/check-410.sh's test only. An
# update fails the way the real one does — it opens its log in the state
# directory it was given, writes the failure into it, and prints the failure
# line on its way out (T-DG9) — and a diagnosis answers the way the real one
# does: an outcome line, and the evidence folder beside the log.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
printf '%s\n' "${XDG_STATE_HOME:-none}" >>"$FAKE_LOG_DIR/state.dirs"
mode=${FAKE_MODE:-default}
logs=${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/logs

case "${1:-}" in
version) printf '0.21.0\n' ;;
update)
  if [ "$mode" = update-succeeds ]; then
    echo "Done in 1.0s, 3 steps"
    exit 0
  fi
  if [ "$mode" = update-slow ]; then
    sleep 30
    exit 2
  fi
  mkdir -p "$logs"
  if [ "$mode" != update-no-log ]; then
    {
      printf '  the state bucket did not answer\n'
      printf 'Failed in 1.2s, 2 steps\n'
    } >"$logs/${FAKE_RUN_ID}.log"
  fi
  printf '  something went wrong\n' >&2
  if [ "$mode" != no-line ]; then
    if [ "$mode" = wrong-id ]; then
      printf 'Something went wrong. Run: fugaro troubleshoot update-20261111170000-1\n'
    else
      printf 'Something went wrong. Run: fugaro troubleshoot %s\n' "$FAKE_RUN_ID"
    fi
  fi
  exit 2
  ;;
troubleshoot)
  if [ "$mode" = diag-fails ]; then
    echo "fugaro: no credential is available here" >&2
    exit 2
  fi
  dir=${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/troubleshoot/${2:-}
  mkdir -p "$dir"
  chmod 700 "$dir"
  printf 'the log the diagnosis read\n' >"$dir/${2:-}.log"
  if [ "$mode" != diag-thin-evidence ]; then
    : >"$dir/doctor.txt"
    : >"$dir/version.txt"
    : >"$dir/config.yaml"
  fi
  if [ "$mode" != diag-no-outcome ]; then
    printf 'Run:       %s (update)\n' "${2:-}"
    printf 'Outcome:   configuration\n'
    printf 'Evidence:  %s\n' "$dir"
  fi
  exit 0
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
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
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/state.dirs"
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
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_RUN_ID="$probe_id" \
    CHECK410_PROJECT="$project" \
    CHECK410_NAME=chk410-test \
    CHECK410_REGION=us-east5 \
    CHECK410_UPDATE_TIMEOUT_SECONDS="${CHECK410_UPDATE_TIMEOUT_SECONDS:-30}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_ran_in_the_scratch() { # dir what
  local dirs d
  dirs=$(cat "$1/logs/state.dirs" 2>/dev/null || true)
  [ -n "$dirs" ] || {
    fail "$2: no command this check ran recorded a state directory"
    return 0
  }
  while IFS= read -r d; do
    [ -n "$d" ] || continue
    # `none` is a command that reads no state at all (`fugaro version`); anything
    # else has to be a directory inside this run's own scratch.
    case "$d" in
    none) ;;
    "$scratch" | "$scratch"/*) ;;
    *) fail "$2: a command the check ran had XDG_STATE_HOME=$d, which is not the check's own scratch directory" ;;
    esac
  done <<<"$dirs"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-410.sh --dry-run" "--help: the dry run is offered"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "--help: no update is run"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK410_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 410 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 410 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS.
  expect_contains "$out" "FACT failure-line: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "the project does not exist" "dry run: the plan says why the probe is harmless"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "dry run: no update is run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "projects describe" "dry run: the project is not read either"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT project-absent: PASS" "happy path: the premise"
  expect_contains "$out" "FACT update-failed: PASS" "happy path: a real failed update"
  expect_contains "$out" "exited 2 against fugaro-chk410-test" "happy path: what failed, and against what"
  expect_contains "$out" "FACT run-logged: PASS" "happy path: the log"
  expect_contains "$out" "${probe_id} in the state directory" "happy path: the run ID is the log's stem"
  expect_contains "$out" "FACT failure-line: PASS" "happy path: T-DG9's line"
  expect_contains "$out" "the line naming ${probe_id}" "happy path: the ID the line named"
  expect_contains "$out" "FACT run-id-resolves: PASS" "happy path: the ID is the run"
  expect_contains "$out" "FACT diagnosis: PASS" "happy path: the diagnosis ran"
  expect_contains "$out" "Outcome:   configuration" "happy path: what it returned"
  expect_contains "$out" "FACT evidence: PASS" "happy path: the evidence folder"
  expect_contains "$out" "FACT no-setup: PASS" "happy path: nothing was set up first"
  expect_contains "$out" "no fugaro setup, no flag, no credential exported" "happy path: what no setup means"
  expect_contains "$out" "CHECK 410 RESULT END (exit 0)" "happy path: the end line"
  # The commands really ran, in the scratch state directory and nowhere else.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "update --name chk410-test --gcp-project ${project} --region us-east5 --yes" "happy path: the probe's own command line"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "troubleshoot ${probe_id}" "happy path: the diagnosis is the one command the line named"
  assert_ran_in_the_scratch "$dir" "happy path"
  assert_scratch_removed "$dir" "happy path"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "bash -x: no update is run"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK410_PROJECT=fugaro-chk410-belong
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "belong: no update is run"
}

test_a_project_that_exists_stops_the_check() {
  # The premise: the probe is harmless BECAUSE the project is not there. A name
  # that turns out to be taken means this check would run a real update against
  # somebody's project, so it stops before running anything.
  local dir=$work/exists
  prepare "$dir"
  run_check "$dir" FAKE_MODE=project-exists
  expect_status "$status" 2 "a project that exists"
  expect_contains "$err" "needs a project that does not exist" "a project that exists: the refusal"
  expect_contains "$err" "CHECK410_PROJECT" "a project that exists: what to do about it"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a project that exists: no update is run"
}

test_a_project_gcloud_will_not_answer_about_stops_the_check() {
  # A read that is not NOT FOUND is not a reading of absence: a permission error
  # says nothing about whether the project is there.
  local dir=$work/unreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=project-unreadable
  expect_status "$status" 2 "a project gcloud will not answer about"
  expect_contains "$err" "needs a project that does not exist" "a project gcloud will not answer about: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a project gcloud will not answer about: no update is run"
}

test_a_successful_update_is_a_finding() {
  local dir=$work/succeeded
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-succeeds
  expect_status "$status" 1 "an update that succeeded"
  expect_contains "$err" "SUCCEEDED" "an update that succeeded: the finding"
}

test_an_update_that_logged_nothing_is_a_finding() {
  # A failed run that wrote no log is the defect this check is looking for, not a
  # row it could not measure: the line such a run prints sends the person to a
  # diagnosis that reads that log.
  local dir=$work/nolog
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-log
  expect_status "$status" 1 "an update that logged nothing"
  expect_contains "$out" "FACT update-failed: PASS" "an update that logged nothing: the failure is still measured"
  expect_contains "$out" "FACT run-logged: FAIL" "an update that logged nothing: the row"
  expect_contains "$out" "left no log under" "an update that logged nothing: what it found"
  # The line named an ID, that ID has no log, and the check does not spend a
  # model call on a run it cannot read.
  expect_contains "$out" "FACT run-id-resolves: FAIL" "an update that logged nothing: the ID has no log"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "an update that logged nothing: no diagnosis is run"
}

test_the_failure_line_absent_is_a_finding() {
  # T-DG9's sentence: a failed update that tells the person nothing about the
  # diagnosis it carries is the finding this check exists for.
  local dir=$work/noline
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-line
  expect_status "$status" 1 "no failure line"
  expect_contains "$out" "FACT update-failed: PASS" "no failure line: the failure itself is still measured"
  expect_contains "$out" "FACT failure-line: FAIL" "no failure line: the row"
  expect_contains "$out" "Something went wrong. Run: fugaro troubleshoot <id>" "no failure line: the sentence it looked for"
  expect_contains "$out" "T-DG9" "no failure line: which task writes it"
  # Nothing was diagnosed: the command the check runs is the one the line names,
  # and there is no line, so it spends no model call on an ID of its own choosing.
  expect_contains "$out" "FACT run-id-resolves: FAIL" "no failure line: the ID row"
  expect_contains "$out" "FACT diagnosis: FAIL" "no failure line: the diagnosis row"
  expect_contains "$out" "the failure line named none that resolves" "no failure line: why the diagnosis did not run"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "no failure line: no diagnosis is run"
}

test_a_line_naming_the_wrong_run_is_a_finding() {
  local dir=$work/wrongid
  prepare "$dir"
  run_check "$dir" FAKE_MODE=wrong-id
  expect_status "$status" 1 "a line naming another run"
  expect_contains "$out" "FACT run-id-resolves: FAIL" "a line naming another run: the row"
  expect_contains "$out" "named update-20261111170000-1 and the run that failed logged itself as ${probe_id}" "a line naming another run: the two IDs"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "a line naming another run: no diagnosis is run"
}

test_a_diagnosis_that_did_not_run_is_not_measured() {
  local dir=$work/diagfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=diag-fails
  expect_status "$status" 2 "a diagnosis that did not run"
  expect_contains "$out" "FACT diagnosis: FAIL" "a diagnosis that did not run: the row"
  expect_contains "$out" "exited 2" "a diagnosis that did not run: what it saw"
  expect_contains "$out" "FACT evidence: FAIL" "a diagnosis that did not run: no evidence either"
  expect_contains "$err" "could NOT measure diagnosis, evidence, no-setup" "a diagnosis that did not run: the exit names the rows"
}

test_a_diagnosis_without_an_outcome_is_not_measured() {
  local dir=$work/nooutcome
  prepare "$dir"
  run_check "$dir" FAKE_MODE=diag-no-outcome
  expect_status "$status" 2 "a diagnosis with no outcome"
  expect_contains "$out" "exited 0 without printing an outcome line" "a diagnosis with no outcome: what it saw"
}

test_an_evidence_folder_missing_a_file_is_a_finding() {
  local dir=$work/thin
  prepare "$dir"
  run_check "$dir" FAKE_MODE=diag-thin-evidence
  expect_status "$status" 1 "an evidence folder missing a file"
  expect_contains "$out" "FACT evidence: FAIL" "an evidence folder missing a file: the row"
  expect_contains "$out" "missing doctor.txt, version.txt, config.yaml" "an evidence folder missing a file: which ones"
}

test_the_diagnosis_can_be_left_out() {
  # The half that costs a model call is the half an owner may not want every
  # time: the failure line and the log it names still get measured.
  local dir=$work/skip
  prepare "$dir"
  run_check "$dir" CHECK410_SKIP_DIAGNOSIS=1
  expect_status "$status" 0 "the diagnosis left out"
  expect_contains "$out" "FACT failure-line: PASS" "the diagnosis left out: the line is still measured"
  expect_contains "$out" "FACT diagnosis: INFO" "the diagnosis left out: the row"
  expect_contains "$out" "not asked for (CHECK410_SKIP_DIAGNOSIS)" "the diagnosis left out: the reason"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "the diagnosis left out: no diagnosis is run"
}

test_a_probe_that_hangs_is_not_measured() {
  local dir=$work/slow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-slow CHECK410_UPDATE_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a probe that hangs"
  expect_contains "$out" "did not finish within 1s and was ended" "a probe that hangs: what it saw"
  expect_contains "$err" "could NOT measure update-failed" "a probe that hangs: the exit names the row"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK410_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK410_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
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
t test_xtrace_refused
t test_belong_is_refused_by_name
t test_a_project_that_exists_stops_the_check
t test_a_project_gcloud_will_not_answer_about_stops_the_check
t test_a_successful_update_is_a_finding
t test_the_failure_line_absent_is_a_finding
t test_an_update_that_logged_nothing_is_a_finding
t test_a_line_naming_the_wrong_run_is_a_finding
t test_a_diagnosis_that_did_not_run_is_not_measured
t test_a_diagnosis_without_an_outcome_is_not_measured
t test_an_evidence_folder_missing_a_file_is_a_finding
t test_the_diagnosis_can_be_left_out
t test_a_probe_that_hangs_is_not_measured
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-410_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-410_test.sh: ok"
