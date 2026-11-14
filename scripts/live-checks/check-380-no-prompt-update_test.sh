#!/usr/bin/env bash
# Tests for scripts/live-checks/check-380-no-prompt-update.sh, against a fake
# `fugaro` on PATH: nothing here reaches the network, there is no cloud project,
# no installation is converged and no agent runs any live step of the check.
#
# The fake models the two calls the check makes and nothing else: `doctor --json`
# answers out of the case's own directory (so a case can say which installation
# is in force, or that doctor refused), and `update` plays the transcript a
# real run would print — the shapes the check has to tell apart (a step left for
# you, a y/N question, a question left open, an enter-prompt, a cloud failure, a
# run that prints nothing, a run that never ends) — and it records what its own
# stdin turned out to be, which is how a case says the check really closed it.
#
# Usage: bash scripts/live-checks/check-380-no-prompt-update_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-380-no-prompt-update.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK380_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (noprompt|update|prompt|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(xtrace agent-session project belong stdin update prompt noprompt teardown)

# The installation every case runs against: its project is not `belong`.
PROJECT=sbx
GCP=sbx-proj

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-380-no-prompt-update.sh's test only. Which
# transcript `update` plays and what `doctor` answers come from two files in the
# case's own directory (doctor.mode, update.mode), and every call is logged with
# its whole argv -- and the update call with what its stdin turned out to be.
set -euo pipefail
dir=${FAKE380_DIR:?FAKE380_DIR is not set}
mode_of() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; else printf 'ok\n'; fi; }
case "${1:-}" in
doctor)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  mode=$(mode_of doctor)
  case "$mode" in
  fail)
    printf 'Error: the installation could not be read\n' >&2
    exit 1
    ;;
  belong)
    printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-proj"}}\n'
    ;;
  name=*)
    printf '{"ok":true,"project":{"name":"%s","gcp_project":"sbx-proj"}}\n' "${mode#name=}"
    ;;
  *)
    printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s"}}\n' "$FAKE380_PROJECT" "$FAKE380_GCP"
    ;;
  esac
  ;;
update)
  # The check's whole point is that this ran with its stdin closed, and this is
  # the reading of it: a read on /dev/null returns EOF at once (a status of 1),
  # where a terminal or an open pipe would block past the bound (a status above
  # 128) or hand a line over.
  kind=closed
  if IFS= read -r -t 2 _line; then
    kind=line
  else
    rc=$?
    if [ "$rc" -gt 128 ]; then kind=open; fi
  fi
  printf 'fugaro %s (stdin=%s)\n' "$*" "$kind" >>"$dir/calls"
  mode=$(mode_of update)
  case "$mode" in
  ok)
    printf '\xe2\x9a\xa0 CONFIRM (project %s, GCP project %s): the layer file this run publishes?\n' "$FAKE380_PROJECT" "$FAKE380_GCP"
    printf '  this project is managed, so this step takes no confirmation\n'
    printf 'No changes\n'
    ;;
  leaves-step)
    printf '\xe2\x9a\xa0 CONFIRM (project %s, GCP project %s): the Firestore database location?\n' "$FAKE380_PROJECT" "$FAKE380_GCP"
    printf 'Error: not confirmed (the confirmation of this step needs a real terminal: run this in your own terminal window, not through a coding agent); undone: the database\n' >&2
    exit 1
    ;;
  prompt)
    printf 'Apply the changes to the project? [y/N]: '
    ;;
  prompt-open)
    printf 'Apply these changes? '
    ;;
  enter)
    printf 'Press Enter to continue'
    ;;
  silent) : ;;
  cloud)
    printf 'Error: the terraform apply failed: the project refused the call\n' >&2
    exit 2
    ;;
  odd)
    printf 'Error: something else went wrong\n' >&2
    exit 64
    ;;
  hang) exec sleep 300 ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The tools the check itself needs, symlinked into a directory of their own, so a
# case can run it with a PATH that holds everything BUT fugaro.
write_tool_path() { # dir names...
  local dir=$1
  shift
  mkdir -p "$dir"
  local tool path
  for tool in "$@"; do
    path=$(command -v "$tool") || {
      fail "the test needs $tool on PATH"
      continue
    }
    ln -sf "$path" "$dir/$tool"
  done
}

# ------------------------------------------------------------------- harness
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home"
  write_fake "$dir"
  : >"$dir/state/calls"
  status=0
  out=""
  err=""
  scratch=""
  RUN380_ARGS=()
  RUN380_ENV=()
  RUN380_BASH=(bash)
}

run380() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE380_DIR="$dir/state" FAKE380_PROJECT="$PROJECT" FAKE380_GCP="$GCP" \
    ${RUN380_ENV[@]+"${RUN380_ENV[@]}"} \
    "${RUN380_BASH[@]}" "$script" ${RUN380_ARGS[@]+"${RUN380_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN380_ARGS=()
  RUN380_ENV=()
  RUN380_BASH=(bash)
}
run380_default() { run380 --project "$PROJECT" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 380 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 380 RESULT END (exit " "$1: the block's end"
}

calls() { cat "$dir/state/calls"; }

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

expect_call() { # what needle
  expect_contains "$(calls)" "$2" "$1"
}
expect_no_call() { # what needle
  expect_absent "$(calls)" "$2" "$1"
}
expect_call_count() { # what n
  local n
  n=$(wc -l <"$dir/state/calls" | tr -d ' ')
  [ "$n" = "$2" ] || fail "$1: expected $2 calls, got $n: $(tr '\n' ';' <"$dir/state/calls")"
}

assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

mode() { # name value
  printf '%s\n' "$2" >"$dir/state/$1.mode"
}

# rewrite_script DEST OLD NEW: a copy of the check with one exact line replaced,
# so a self-test can prove a case's assertion is not vacuous rather than argue it.
rewrite_script() { # dest old new
  python3 - "$script" "$1" "$2" "$3" <<'PY'
import sys

src, dest, old, new = sys.argv[1:5]
with open(src) as fh:
    text = fh.read()
if old not in text:
    raise SystemExit("rewrite_script: the line to replace is not in the script: %r" % old)
with open(dest, "w") as fh:
    fh.write(text.replace(old, new, 1))
PY
  chmod +x "$1"
}

# --------------------------------------------------------------- the readings
# The whole point, end to end: the run ends by itself, asks nothing, and the
# check says so — with the run's own stdin closed and the exit status it had.
test_a_run_that_ends_and_asks_nothing_passes() {
  setup_case ends-and-asks-nothing
  run380_default
  expect_status "$status" 0 "the run ends and asks nothing"
  expect_block_keys "the run ends and asks nothing"
  expect_contains "$(fact_line xtrace)" "PASS" "the run ends and asks nothing: not tracing"
  expect_contains "$(fact_line agent-session)" "PASS" "the run ends and asks nothing: the owner's own session"
  expect_contains "$(fact_line project)" "PASS" "the run ends and asks nothing: doctor's installation"
  expect_contains "$(fact_line belong)" "PASS" "the run ends and asks nothing: not belong"
  expect_contains "$(fact_line stdin)" "PASS" "the run ends and asks nothing: how the run was started"
  expect_contains "$(fact_line stdin)" "/dev/null" "the run ends and asks nothing: stdin named"
  expect_contains "$(fact_line update)" "PASS" "the run ends and asks nothing: the run row"
  expect_contains "$(fact_line update)" "exit 0" "the run ends and asks nothing: the exit status"
  expect_contains "$(fact_line update)" "No changes" "the run ends and asks nothing: the run's own last line"
  expect_contains "$(fact_line prompt)" "PASS" "the run ends and asks nothing: nothing asked"
  expect_contains "$(fact_line noprompt)" "T7 HOLDS" "the run ends and asks nothing: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "the run ends and asks nothing: teardown"
  expect_call "the run ends and asks nothing: doctor was asked" "fugaro doctor --json"
  expect_call "the run ends and asks nothing: the project travelled on" "--project sbx"
  expect_call "the run ends and asks nothing: the run's stdin was closed" "(stdin=closed)"
  expect_call_count "the run ends and asks nothing: two calls" 2
  assert_scratch_removed "" "the run ends and asks nothing"
}

# The banner is the one thing the policy keeps: it is the note of a step, not a
# read, and it ends at its own question mark. A check that failed on it would
# fail every real run.
test_the_confirm_banner_is_not_a_prompt() {
  setup_case banner
  run380_default
  expect_status "$status" 0 "the banner"
  expect_contains "$(fact_line prompt)" "PASS" "the banner: no question"
  expect_contains "$(fact_line prompt)" "CONFIRM banners" "the banner: the row says why"
  expect_absent "$(fact_line prompt)" "FAIL" "the banner: not a finding"
}

# A step left for you is the policy's answer for a run that cannot confirm it,
# and it is NOT a prompt: the run asked nothing, said what it had left, and
# ended. The row says both.
test_a_step_left_for_you_is_not_a_prompt() {
  setup_case leaves-step
  mode update leaves-step
  run380_default
  expect_status "$status" 0 "a step left for you"
  expect_contains "$(fact_line update)" "PASS" "a step left for you: the run row"
  expect_contains "$(fact_line update)" "exit 1" "a step left for you: the exit status"
  expect_contains "$(fact_line update)" "needs a real terminal" "a step left for you: the run's own sentence"
  expect_contains "$(fact_line prompt)" "PASS" "a step left for you: nothing was asked"
  expect_contains "$(fact_line noprompt)" "T7 HOLDS" "a step left for you: the verdict"
}

test_a_y_n_question_is_a_fail() {
  setup_case y-n
  mode update prompt
  run380_default
  expect_status "$status" 1 "a y/N question"
  expect_contains "$(fact_line prompt)" "FAIL" "a y/N question: the row"
  expect_contains "$(fact_line prompt)" "[y/N]" "a y/N question: the line quoted"
  expect_contains "$(fact_line noprompt)" "T7 does NOT hold" "a y/N question: the verdict"
}

test_a_question_left_open_is_a_fail() {
  setup_case open-question
  mode update prompt-open
  run380_default
  expect_status "$status" 1 "a question left open"
  expect_contains "$(fact_line prompt)" "FAIL" "a question left open: the row"
  expect_contains "$(fact_line prompt)" "Apply these changes?" "a question left open: the line quoted"
}

test_an_enter_prompt_is_a_fail() {
  setup_case enter
  mode update enter
  run380_default
  expect_status "$status" 1 "an enter-prompt"
  expect_contains "$(fact_line prompt)" "FAIL" "an enter-prompt: the row"
  expect_contains "$(fact_line prompt)" "Press Enter" "an enter-prompt: the line quoted"
}

# The reading only means something if the scan is what found it: with the needles
# emptied, the same transcript passes — so the FAIL above is the scan's, and not
# this case's expectations reading their own answer back.
test_the_prompt_scan_is_not_vacuous() {
  setup_case not-vacuous
  mode update prompt
  rewrite_script "$dir/scanless.sh" \
    'NEEDLES = ("[y/n]", "(y/n)", "(yes/no)", "to confirm:", "press enter", "hit enter", "press return", "hit return")' \
    'NEEDLES = ()'
  local saved=$script
  script=$dir/scanless.sh
  run380_default
  script=$saved
  expect_status "$status" 0 "the prompt scan is not vacuous"
  expect_contains "$(fact_line prompt)" "PASS" "the prompt scan is not vacuous: with no needles, nothing is found"
}

# A run that never ends is the failure this check exists for, and the deadline is
# what ends it: the row says the run was killed, and the verdict says so too.
test_a_run_that_never_ends_is_a_fail() {
  setup_case hang
  mode update hang
  local started=$SECONDS
  run380_default --deadline 5
  local took=$((SECONDS - started))
  expect_status "$status" 1 "a run that never ends"
  expect_contains "$(fact_line update)" "FAIL" "a run that never ends: the run row"
  expect_contains "$(fact_line update)" "still running after 5s" "a run that never ends: the deadline"
  expect_contains "$(fact_line update)" "killed" "a run that never ends: it was killed"
  expect_contains "$(fact_line noprompt)" "T7 does NOT hold" "a run that never ends: the verdict"
  expect_contains "$(fact_line stdin)" "PASS" "a run that never ends: stdin was still closed"
  [ "$took" -lt 30 ] || fail "a run that never ends: the check took ${took}s to give up on a 5s deadline"
}

# A transcript with nothing in it is not evidence: the row is not measured rather
# than passed, and the run ends as one that could not tell.
test_a_silent_run_is_not_measured() {
  setup_case silent
  mode update silent
  run380_default
  expect_status "$status" 2 "a silent run"
  expect_contains "$(fact_line prompt)" "FAIL" "a silent run: the prompt row"
  expect_contains "$(fact_line prompt)" "printed nothing" "a silent run: the reason"
  expect_contains "$(fact_line noprompt)" "NOT MEASURED" "a silent run: the verdict"
}

# A cloud failure is a finding about the installation and not a prompt: the run
# row FAILs (so the check exits 1) while the claim itself still holds.
test_a_cloud_failure_is_a_fail() {
  setup_case cloud
  mode update cloud
  run380_default
  expect_status "$status" 1 "a cloud failure"
  expect_contains "$(fact_line update)" "FAIL" "a cloud failure: the run row"
  expect_contains "$(fact_line update)" "cloud failure" "a cloud failure: the reason"
  expect_contains "$(fact_line noprompt)" "T7 HOLDS" "a cloud failure: the claim still held"
}

test_an_unexpected_exit_status_is_a_fail() {
  setup_case odd
  mode update odd
  run380_default
  expect_status "$status" 1 "an unexpected exit status"
  expect_contains "$(fact_line update)" "not one of update's own codes" "an unexpected exit status: the row"
}

# ---------------------------------------------------------------- the guards
test_a_coding_agents_session_is_refused() {
  setup_case agent
  RUN380_ENV=(CLAUDECODE=1)
  run380_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE is set" "a coding agent's session: the marker named"
  expect_contains "$(fact_line agent-session)" "Check 380 is yours to run" "a coding agent's session: the refusal"
  expect_no_calls "a coding agent's session: nothing was called"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case xtrace
  RUN380_BASH=(bash -x)
  run380_default
  expect_status "$status" 2 "a tracing shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a tracing shell: the row"
  expect_contains "$err" "tracing" "a tracing shell: the reason"
  expect_no_calls "a tracing shell: nothing was called"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run380 --project belong
  expect_status "$status" 2 "belong in what you typed"
  expect_contains "$out" "FACT belong: FAIL" "belong in what you typed: the row"
  expect_contains "$err" "matches the installation 'belong'" "belong in what you typed: the refusal"
  expect_no_calls "belong in what you typed: nothing was called"
  assert_scratch_removed "" "belong in what you typed"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  # No --project: the guard that has to catch this one reads doctor's own answer.
  run380
  expect_status "$status" 2 "belong in what doctor answers"
  expect_contains "$(fact_line project)" "belong" "belong in what doctor answers: the project row"
  expect_contains "$out" "FACT belong: FAIL" "belong in what doctor answers: the row"
  expect_contains "$err" "matches the installation 'belong'" "belong in what doctor answers: the refusal"
  expect_call_count "belong in what doctor answers: doctor only" 1
  expect_no_call "belong in what doctor answers: no update" "update "
}

test_the_belong_guard_is_not_vacuous() {
  setup_case belong-not-vacuous
  rewrite_script "$dir/unguarded.sh" '  *belong*) return 0 ;;' '  *belong*) return 1 ;;'
  # Doctor answers belong too, so nothing but the guard stands between this run
  # and the installation it must never touch: with the guard out of the copy the
  # run goes ahead, which is the FAIL of the case above read back.
  mode doctor belong
  local saved=$script
  script=$dir/unguarded.sh
  run380 --project belong
  script=$saved
  expect_status "$status" 0 "the belong guard is not vacuous"
  expect_call "the belong guard is not vacuous: with the guard out, the run went ahead" "update --project belong"
}

test_a_project_that_is_not_doctors_stops() {
  setup_case other-project
  run380 --project other
  expect_status "$status" 2 "a project that is not doctor's"
  expect_contains "$err" "two different installations" "a project that is not doctor's: the refusal"
  expect_no_call "a project that is not doctor's: no update" "update "
}

test_doctor_refused_with_a_project_typed_carries_on() {
  setup_case doctor-refused-typed
  mode doctor fail
  run380_default
  expect_status "$status" 0 "doctor refused with a project typed"
  expect_contains "$(fact_line project)" "INFO" "doctor refused with a project typed: the row"
  expect_contains "$(fact_line project)" "the one you typed" "doctor refused with a project typed: the project it went on with"
  expect_call "doctor refused with a project typed: the update still ran" "update --project sbx"
}

test_doctor_refused_with_no_project_typed_stops() {
  setup_case doctor-refused-untyped
  mode doctor fail
  run380
  expect_status "$status" 2 "doctor refused with no project typed"
  expect_contains "$(fact_line project)" "FAIL" "doctor refused with no project typed: the row"
  expect_no_call "doctor refused with no project typed: no update" "update "
}

test_a_missing_tool_stops_with_one_line() {
  setup_case missing-tool
  # Every tool but the one under test, so the case reads the check's own line
  # about fugaro and not a shell that could not get off the ground.
  write_tool_path "$dir/tools" bash mktemp grep sed head tail cut awk python3 cat tr wc
  run380_tools_only
  expect_status "$status" 2 "a missing fugaro"
  expect_contains "$err" "needs 'fugaro' on PATH" "a missing fugaro: the line"
  expect_no_calls "a missing fugaro: no call"
}

run380_tools_only() {
  status=0
  out=$(PATH="$dir/tools" HOME="$dir/home" TMPDIR="$dir/tmp" FAKE380_DIR="$dir/state" \
    "$(command -v bash)" "$script" --project "$PROJECT" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
}

# ---------------------------------------------------------------- the plan
test_help() {
  setup_case help
  run380 --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check-380-no-prompt-update.sh" "help: the usage"
  expect_absent "$out" "FACT " "help: no block"
  expect_no_calls "help: nothing was called"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry-run
  run380_default --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "CHECK 380 RESULT" "a dry run: the block"
  expect_contains "$(fact_line dry-run)" "PASS" "a dry run: the row"
  expect_contains "$(fact_line stdin)" "INFO" "a dry run: the run is only planned"
  expect_contains "$(fact_line noprompt)" "INFO" "a dry run: the verdict is not measured"
  expect_contains "$err" "dry run: NOTHING below was done" "a dry run: the plan"
  expect_no_calls "a dry run: nothing was called"
  assert_scratch_removed "" "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-run-belong
  run380 --project belong --dry-run
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$out" "FACT belong: FAIL" "a dry run pointed at belong: the row"
  expect_no_calls "a dry run pointed at belong: nothing was called"
}

test_usage_and_arguments() {
  setup_case usage
  run380 --deadline abc
  expect_status "$status" 2 "a non-numeric deadline"
  expect_contains "$err" "whole number of seconds" "a non-numeric deadline: the message"
  run380 --deadline 2
  expect_status "$status" 2 "too short a deadline"
  expect_contains "$err" "too short" "too short a deadline: the message"
  run380 --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--wat'" "an unknown argument: the message"
  expect_no_calls "usage and arguments: nothing was called"
}

test_kept_scratch_is_reported() {
  setup_case kept-scratch
  mode update prompt
  RUN380_ENV=(CHECK380_KEEP_SCRATCH=1)
  run380_default
  expect_contains "$err" "CHECK380_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named none"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  [ -f "$scratch/update.out" ] || fail "a kept scratch directory: the transcript is not there"
  expect_contains "$(cat "$scratch/update.out")" "[y/N]" "a kept scratch directory: the transcript is the run's own"
  rm -rf "$scratch" 2>/dev/null || true
}

# Sharding, so the package's Go test can run the tests of this file side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and CHECK_TEST_ONLY=name
# runs that one test alone. Neither set: all run.
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
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_run_that_ends_and_asks_nothing_passes
t test_the_confirm_banner_is_not_a_prompt
t test_a_step_left_for_you_is_not_a_prompt
t test_a_y_n_question_is_a_fail
t test_a_question_left_open_is_a_fail
t test_an_enter_prompt_is_a_fail
t test_the_prompt_scan_is_not_vacuous
t test_a_run_that_never_ends_is_a_fail
t test_a_silent_run_is_not_measured
t test_a_cloud_failure_is_a_fail
t test_an_unexpected_exit_status_is_a_fail
t test_a_coding_agents_session_is_refused
t test_a_shell_that_arrived_tracing_is_refused
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_the_belong_guard_is_not_vacuous
t test_a_project_that_is_not_doctors_stops
t test_doctor_refused_with_a_project_typed_carries_on
t test_doctor_refused_with_no_project_typed_stops
t test_a_missing_tool_stops_with_one_line
t test_usage_and_arguments
t test_kept_scratch_is_reported

if [ "$failures" -ne 0 ]; then
  echo "check-380-no-prompt-update_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-380-no-prompt-update_test.sh: all tests passed" >&2
