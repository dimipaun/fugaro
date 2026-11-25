#!/usr/bin/env bash
# Tests for scripts/live-checks/check-821.sh. NOTHING here is a cloud, an
# installation or a run: `fugaro` alone is faked, and every answer it gives a
# case owns. The check reads nothing else — no gcloud and no curl — so the fake
# is the whole world it sees:
#
#   * `fugaro doctor --json`: the installation the checkout belongs to (its
#     name, GCP project and region), whether the checkout's `fugaro.yaml` is
#     valid, and the locations row that is the record's only reader. The premise
#     and the record row read the same document, so a case bends one or the
#     other by bending the document.
#   * `fugaro runs ls --json --limit 200`: the launched run's row, keyed by run
#     id, with its status and the branch, push, pull request and outcome the
#     inflight row quotes. A case says what each poll answers, so the run can be
#     seen running and then finish, or finish before the update started, or be
#     gone.
#   * `fugaro run --json`: the launch's own document, with its run id.
#   * `fugaro update --project <name>`: the real converge, whose exit status,
#     transcript and `N step(s) left for you` sentence are the update row.
#   * `fugaro update --plan-only --json`: the plan's counts, which are U9's own
#     reading.
#
# Every verb, sub-verb and flag is checked against the real command's own and
# anything else is refused with 64. The cases are the readings bent one at a
# time: a checkout doctor does not name, another installation, another region,
# an invalid fugaro.yaml, a launch that fails or names no run, a run never seen
# running, a run not listed, a runs read that fails, an update that converges,
# refuses, hands off a step, fails in the cloud, ends on another code or
# outlives its deadline, an update that asks a question or prints nothing, a
# record with no locations row or the wrong four, a plan that is empty, changes
# something, only imports or is not there at all, a run that succeeds, ends
# 'failed', is never in flight or is gone, and every guard.
#
# Usage: bash scripts/live-checks/check-821_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-821.sh
bashbin=$(command -v bash)
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK821_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK821_TEST_HEAD:-2000}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '\n%s' "${out:-}" | head -c "${CHECK821_TEST_HEAD:-2000}")"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK821_TEST_HEAD:-2000}")"
}
expect_count() { # file needle want what
  local got
  got=$(grep -c -- "$2" "$1" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 line(s) matching '$2' in $(basename "$1"), got $got"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
emptybin=$work/emptybin
mkdir -p "$fakebin" "$emptybin"

# fake fugaro: the five call shapes the check makes, each flag checked against
# the real command's own (value-taking flags and boolean ones apart), with
# counters so a case can say what each successive call answers.
cat >"$fakebin/fugaro" <<'FUGARO'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE821_DIR:?}
printf 'fugaro %s\n' "$*" >>"${FAKE821_LOG:?}"

case "${1:-}" in
-h | --help)
  cat <<'USAGE'
Fugaro drives coding-agent runs in your own cloud project.

Usage:
  fugaro [command]

Available Commands:
  doctor      Check the installation, the local config and a checkout
  run         Launch a task in the cloud
  runs        Read the runs of an installation
  update      Converge an installation to this release
USAGE
  exit 0
  ;;
esac

key=${1:-}
n=1
case "$key" in
runs)
  key="$key ${2:-}"
  n=2
  ;;
esac
case "$key" in
doctor) val="--project --gcp-project --region --config --dir"; boolf="--json" ;;
"runs ls") val="--project --limit"; boolf="--json" ;;
run) val="--repo --project --title --task-file"; boolf="--json" ;;
update) val="--project"; boolf="--plan-only --json" ;;
*)
  printf 'fake fugaro: %s is not a command Check 821 may run\n' "$*" >&2
  exit 64
  ;;
esac
shift "$n"

pos=()
plan_only=no
while [ $# -gt 0 ]; do
  case "$1" in
  --*=*)
    f=${1%%=*}
    case " $val $boolf " in *" $f "*) ;; *) printf 'fake fugaro: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
    [ "$f" = "--plan-only" ] && plan_only=yes
    shift
    ;;
  --*)
    f=$1
    case " $boolf " in
    *" $f "*)
      [ "$f" = "--plan-only" ] && plan_only=yes
      shift
      ;;
    *)
      case " $val " in *" $f "*) ;; *) printf 'fake fugaro: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
      shift
      [ $# -ge 1 ] || { printf 'fake fugaro: %s needs a value\n' "$f" >&2; exit 64; }
      pos+=("$1")
      shift
      ;;
    esac
    ;;
  -*)
    printf 'fake fugaro: %s is not a flag %s has\n' "$1" "$key" >&2
    exit 64
    ;;
  *)
    pos+=("$1")
    shift
    ;;
  esac
done

count() { # count <name>: this call's number, 1-based, kept in the case dir
  local f="$dir/.n.$1" n=0
  [ -f "$f" ] && n=$(cat "$f")
  n=$((n + 1))
  printf '%s' "$n" >"$f"
  printf '%s' "$n"
}

serve() { # serve <name> <call-number> <default-file>
  if [ -f "$dir/$1.$2" ]; then
    cat "$dir/$1.$2"
  else
    cat "$dir/$3"
  fi
}

refuse_if() { # refuse_if <name> <call number> <what>
  [ -f "$dir/$1.fail-at" ] || return 0
  [ "$2" = "$(cat "$dir/$1.fail-at")" ] || return 0
  printf 'fake fugaro: %s refused\n' "$3" >&2
  [ ! -f "$dir/$1.err" ] || cat "$dir/$1.err" >&2
  exit "$(cat "$dir/$1.status" 2>/dev/null || printf 1)"
}

case "$key" in
doctor)
  n=$(count doctor)
  refuse_if doctor "$n" doctor
  serve doctor "$n" doctor.json
  ;;
"runs ls")
  n=$(count runs)
  refuse_if runs "$n" runs
  serve runs "$n" runs.json
  ;;
run)
  n=$(count run)
  refuse_if run "$n" run
  [ ! -f "$dir/run.err" ] || cat "$dir/run.err" >&2
  serve run "$n" launch.json
  exit "$(cat "$dir/run.status" 2>/dev/null || printf 0)"
  ;;
update)
  if [ "$plan_only" = yes ]; then
    n=$(count plan)
    refuse_if plan "$n" plan
    serve plan "$n" plan.json
    exit "$(cat "$dir/plan.status" 2>/dev/null || printf 0)"
  fi
  n=$(count update)
  refuse_if update "$n" update
  [ ! -f "$dir/update.out" ] || cat "$dir/update.out"
  [ ! -f "$dir/update.err" ] || cat "$dir/update.err" >&2
  if [ -f "$dir/update.sleep" ]; then sleep "$(cat "$dir/update.sleep")"; fi
  exit "$(cat "$dir/update.status" 2>/dev/null || printf 0)"
  ;;
esac
FUGARO
chmod +x "$fakebin/fugaro"

# ---------------------------------------------------------------- the cases

DOGFOOD_ROW="its Firestore database is in us-east5; its Realtime Database is in us-central1; its secret containers are replicated with Google's automatic replication, which today means the United States; its run log bucket is in global"
RUN_ID=chk821-run-1

doctor_doc() { # doctor_doc <name> <region> <valid> <row>
  printf '{"project":{"name":"%s","gcp_project":"fugaro-prod","region":"%s"},' "$1" "$2" >"$dir/doctor.json"
  printf '"fugaro_yaml":{"path":"%s/repo/fugaro.yaml","valid":%s},' "$dir" "$3" >>"$dir/doctor.json"
  printf '"checks":[{"id":"project","problem":""},{"id":"locations","problem":"%s"}]}\n' "$4" >>"$dir/doctor.json"
}

run_doc() { # run_doc <path> <status> [outcome] [pr] [pushed] [branch]
  printf '{"project":"fugaro","runs":[{"run_id":"%s","title":"Check 821: a run in flight across the update","status":"%s","branch":"%s","pushed":%s,"pr":"%s","outcome":"%s","terminal":%s}]}\n' \
    "$RUN_ID" "$2" "${6:-fugaro/chk821}" "${5:-true}" "${4:-#3800}" "${3:-ready}" \
    "$([ "$2" = running ] && printf false || printf true)" >"$1"
}

case_dir() { # case_dir <name>
  dir=$work/$1
  rm -rf "$dir"
  mkdir -p "$dir/repo"
  : >"$dir/CALLS"
  : >"$dir/update.out"
  : >"$dir/update.err"
  printf 'project: fugaro\nregion: us-east5\n' >"$dir/repo/fugaro.yaml"
  doctor_doc fugaro us-east5 true "$DOGFOOD_ROW"
  printf '{"run_id":"%s","repo":"acme/app","status":"launched"}\n' "$RUN_ID" >"$dir/launch.json"
  # The launch's own poll sees the run running; by the time the update is over
  # the run has finished, which is the reading the inflight row wants.
  run_doc "$dir/runs.1" running
  run_doc "$dir/runs.json" succeeded
  printf 'fugaro: the installation was converged\n' >"$dir/update.out"
  printf '{"changes":{"imports":0,"creates":0,"updates":0,"deletes":0,"replaces":0},"left_for_you":[]}\n' >"$dir/plan.json"
}

run821_raw() { # run821_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    CHECK821_POLL_SECONDS=1 \
    bash "$script" "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
}

run821() { # run821 [extra args...]
  run821_raw --repo "$dir/repo" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run821
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 821 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" run PASS "the happy path"
  expect_row "$out" update PASS "the happy path"
  expect_row "$out" asked PASS "the happy path"
  expect_row "$out" record PASS "the happy path"
  expect_row "$out" plan PASS "the happy path"
  expect_row "$out" inflight PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  # The whole sequence, in the order the check runs it: the launch and the poll
  # before the converge, the converge, the record, the plan, then the run again.
  expect_count "$dir/CALLS" "fugaro doctor --json" 2 "the happy path"
  expect_count "$dir/CALLS" "fugaro run --repo $dir/repo --project fugaro --title Check 821: a run in flight across the update --task-file" 1 "the happy path"
  expect_count "$dir/CALLS" "fugaro runs ls --json --project fugaro --limit 200" 2 "the happy path"
  expect_count "$dir/CALLS" "fugaro update --project fugaro" 2 "the happy path"
  expect_count "$dir/CALLS" "fugaro update --project fugaro --plan-only --json" 1 "the happy path"
  # The launch came before the converge, the converge before the plan, and the
  # poll that saw the run finish after all of them: the update really did run
  # with the run in flight.
  local launch converge plan last_poll
  launch=$(grep -n 'fugaro run --repo' "$dir/CALLS" | cut -d: -f1)
  converge=$(grep -n 'fugaro update --project fugaro$' "$dir/CALLS" | cut -d: -f1)
  plan=$(grep -n -- '--plan-only' "$dir/CALLS" | cut -d: -f1)
  last_poll=$(grep -n 'fugaro runs ls' "$dir/CALLS" | cut -d: -f1 | tail -1)
  { [ -n "$launch" ] && [ "$launch" -lt "$converge" ]; } || fail "the happy path: the run was not launched before the converge ($launch vs $converge)"
  { [ -n "$plan" ] && [ "$converge" -lt "$plan" ]; } || fail "the happy path: the plan did not come after the converge ($converge vs $plan)"
  { [ -n "$last_poll" ] && [ "$plan" -lt "$last_poll" ]; } || fail "the happy path: the run was not followed after the plan ($plan vs $last_poll)"
  # The update's own transcript was read, and the block quotes the four things
  # the dogfood installation really has.
  expect_contains "$out" "its Firestore database is in us-east5" "the happy path"
  expect_contains "$out" "replicated with Google's automatic replication" "the happy path"
  expect_contains "$out" "its run log bucket is in global" "the happy path"
  expect_contains "$out" "status succeeded" "the happy path"
  expect_contains "$out" "#3800" "the happy path"
}

test_help_prints_the_usage_and_calls_nothing() {
  case_dir help
  run821_raw --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 821" "--help"
  expect_contains "$out" "bash scripts/live-checks/check-821.sh [--project <name>]" "--help"
  [ ! -s "$dir/CALLS" ] || fail "--help: it called fugaro: $(head -c 300 "$dir/CALLS")"
  expect_not_contains "$out" "CHECK 821 RESULT" "--help"
}

test_a_dry_run_prints_the_plan_and_calls_nothing() {
  case_dir dry_run
  run821 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 821 plan (nothing is called: no fugaro at all)" "a dry run"
  expect_contains "$out" "one throwaway run of the checkout" "a dry run"
  [ ! -s "$dir/CALLS" ] || fail "a dry run: it called fugaro: $(head -c 300 "$dir/CALLS")"
  expect_not_contains "$out" "CHECK 821 RESULT" "a dry run"
}

test_a_dry_run_with_no_run_says_so() {
  case_dir dry_run_no_run
  run821 --dry-run --no-run
  expect_status "$status" 0 "a dry run with --no-run"
  expect_contains "$out" "no run at all" "a dry run with --no-run"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    bash "$script" --repo "$dir/repo" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "an agent session"
  expect_contains "$err" "this shell carries CLAUDECODE" "an agent session"
  [ ! -s "$dir/CALLS" ] || fail "an agent session: it called fugaro"
}

test_a_traced_shell_is_refused() {
  case_dir xtrace
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    "$bashbin" -x "$script" --repo "$dir/repo" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a traced shell"
  expect_contains "$err" "this shell is tracing" "a traced shell"
  [ ! -s "$dir/CALLS" ] || fail "a traced shell: it called fugaro"
}

test_an_unknown_argument_is_refused() {
  case_dir unknown_argument
  run821 --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
  [ ! -s "$dir/CALLS" ] || fail "an unknown argument: it called fugaro"
}

test_a_positional_argument_is_refused() {
  case_dir positional
  run821 fugaro
  expect_status "$status" 2 "a positional argument"
  expect_contains "$err" "unexpected argument fugaro" "a positional argument"
  [ ! -s "$dir/CALLS" ] || fail "a positional argument: it called fugaro"
}

test_a_flag_without_a_value_is_refused() {
  case_dir flag_without_value
  run821 --deadline
  expect_status "$status" 2 "--deadline with no value"
  expect_contains "$err" "--deadline needs a number of seconds" "--deadline with no value"
}

test_a_wait_that_is_not_a_number_stops_the_check() {
  case_dir not_a_number
  run821 --wait soon
  expect_status "$status" 2 "a wait that is not a number"
  expect_contains "$err" "--wait needs a number of seconds and was given 'soon'" "a wait that is not a number"
  [ ! -s "$dir/CALLS" ] || fail "a wait that is not a number: it called fugaro"
}

test_a_deadline_of_zero_stops_the_check() {
  case_dir deadline_zero
  run821 --deadline 0
  expect_status "$status" 2 "a deadline of zero"
  expect_contains "$err" "--deadline needs a number of seconds greater than zero" "a deadline of zero"
}

test_a_repo_that_is_not_a_directory_stops_the_check() {
  case_dir no_repo
  run821_raw --repo "$dir/nowhere"
  expect_status "$status" 2 "a repo that is not a directory"
  expect_contains "$err" "is not a directory" "a repo that is not a directory"
  [ ! -s "$dir/CALLS" ] || fail "a repo that is not a directory: it called fugaro"
}

test_a_checkout_without_a_fugaro_yaml_stops_the_check() {
  case_dir no_yaml
  rm -f "$dir/repo/fugaro.yaml"
  run821
  expect_status "$status" 2 "a checkout with no fugaro.yaml"
  expect_contains "$err" "holds no fugaro.yaml" "a checkout with no fugaro.yaml"
  [ ! -s "$dir/CALLS" ] || fail "a checkout with no fugaro.yaml: it called fugaro"
}

test_a_checkout_whose_doctor_names_nothing_stops_the_check() {
  case_dir unnamed
  doctor_doc "" us-east5 true "$DOGFOOD_ROW"
  run821
  expect_status "$status" 2 "a checkout doctor does not name"
  expect_row "$out" premise FAIL "a checkout doctor does not name"
  expect_contains "$out" "named no installation" "a checkout doctor does not name"
  expect_count "$dir/CALLS" "fugaro run" 0 "a checkout doctor does not name"
}

test_another_installation_than_the_one_named_stops_the_check() {
  case_dir other_installation
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT CHECK821_PROJECT=aurora \
    PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    bash "$script" --repo "$dir/repo" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "another installation than the one named"
  expect_contains "$out" "you named the installation aurora and this checkout answers fugaro" "another installation than the one named"
  expect_count "$dir/CALLS" "fugaro run" 0 "another installation than the one named"
}

test_an_installation_in_another_region_stops_the_check() {
  case_dir other_region
  doctor_doc chk820 europe-west1 true "$DOGFOOD_ROW"
  run821
  expect_status "$status" 2 "an installation in another region"
  expect_row "$out" premise FAIL "an installation in another region"
  expect_contains "$out" "the installation chk820 is in europe-west1" "an installation in another region"
  expect_contains "$out" "Check 820 covers a new EU installation" "an installation in another region"
  expect_count "$dir/CALLS" "fugaro run" 0 "an installation in another region"
}

test_a_fugaro_yaml_that_is_not_valid_stops_the_check() {
  case_dir invalid_yaml
  doctor_doc fugaro us-east5 false "$DOGFOOD_ROW"
  run821
  expect_status "$status" 2 "an invalid fugaro.yaml"
  expect_row "$out" premise FAIL "an invalid fugaro.yaml"
  expect_contains "$out" "is not valid" "an invalid fugaro.yaml"
  expect_count "$dir/CALLS" "fugaro run" 0 "an invalid fugaro.yaml"
}

test_no_run_measures_everything_but_the_run_in_flight() {
  case_dir no_run
  run821 --no-run
  expect_status "$status" 3 "no run"
  expect_row "$out" premise PASS "no run"
  expect_row "$out" run UNVERIFIED "no run"
  expect_row "$out" update PASS "no run"
  expect_row "$out" record PASS "no run"
  expect_row "$out" plan PASS "no run"
  expect_row "$out" inflight UNVERIFIED "no run"
  expect_count "$dir/CALLS" "fugaro run" 0 "no run"
}

test_a_launch_that_fails_is_a_finding() {
  case_dir launch_failed
  printf '1' >"$dir/run.status"
  printf 'fugaro: the checkout has no origin remote\n' >"$dir/run.err"
  run821
  expect_status "$status" 1 "a launch that fails"
  expect_row "$out" run FAIL "a launch that fails"
  expect_contains "$out" "exited 1, so no run was launched" "a launch that fails"
  expect_contains "$out" "the checkout has no origin remote" "a launch that fails"
}

test_a_launch_with_no_run_id_is_a_finding() {
  case_dir no_run_id
  printf '{"status":"launched"}\n' >"$dir/launch.json"
  run821
  expect_status "$status" 1 "a launch with no run id"
  expect_row "$out" run FAIL "a launch with no run id"
  expect_contains "$out" "the launch printed no run id" "a launch with no run id"
}

test_a_run_never_seen_running_is_unverified() {
  case_dir never_live
  run_doc "$dir/runs.1" succeeded
  run_doc "$dir/runs.json" succeeded
  run821 --start-wait 1
  expect_status "$status" 3 "a run never seen running"
  expect_row "$out" run UNVERIFIED "a run never seen running"
  expect_contains "$out" "never read running" "a run never seen running"
  expect_row "$out" inflight UNVERIFIED "a run never seen running"
}

test_a_run_that_is_not_listed_is_unverified() {
  case_dir not_listed
  printf '{"project":"fugaro","runs":[]}\n' >"$dir/runs.1"
  printf '{"project":"fugaro","runs":[]}\n' >"$dir/runs.json"
  run821 --start-wait 1
  expect_status "$status" 1 "a run that is not listed"
  expect_row "$out" run UNVERIFIED "a run that is not listed"
  expect_contains "$out" "was not listed by 'fugaro runs ls --json" "a run that is not listed"
  # Nothing was ever in flight, so the record of it is gone by the end too.
  expect_row "$out" inflight FAIL "a run that is not listed"
}

test_a_runs_read_the_launch_cannot_make_is_a_finding() {
  case_dir runs_unreadable
  printf '1' >"$dir/runs.fail-at"
  printf 'fugaro: the runs bucket could not be read\n' >"$dir/runs.err"
  run821
  expect_status "$status" 1 "a runs read that fails"
  expect_row "$out" run FAIL "a runs read that fails"
  expect_contains "$out" "could not be read back" "a runs read that fails"
}

test_an_update_that_hands_off_a_step_is_unverified() {
  case_dir handoff
  printf 'left for you 1. Run the retirement: fugaro update --repo . --retire\n' >"$dir/update.out"
  printf '1 step(s) left for you\n' >"$dir/update.err"
  printf '1' >"$dir/update.status"
  run821
  expect_status "$status" 3 "an update that hands off a step"
  expect_row "$out" update UNVERIFIED "an update that hands off a step"
  expect_contains "$out" "1 step(s) left for you" "an update that hands off a step"
  expect_contains "$out" "Run the retirement" "an update that hands off a step"
}

test_an_update_that_refuses_is_a_finding() {
  case_dir refused
  printf '1' >"$dir/update.status"
  printf 'fugaro: the installation is not managed by this machine\n' >"$dir/update.err"
  run821
  expect_status "$status" 1 "an update that refuses"
  expect_row "$out" update FAIL "an update that refuses"
  expect_contains "$out" "with exit 1 and no 'step(s) left for you' sentence" "an update that refuses"
  expect_contains "$out" "the installation is not managed by this machine" "an update that refuses"
}

test_a_cloud_failure_is_a_finding() {
  case_dir cloud_failure
  printf '2' >"$dir/update.status"
  printf 'fugaro: the Terraform apply failed\n' >"$dir/update.err"
  run821
  expect_status "$status" 1 "a cloud failure"
  expect_row "$out" update FAIL "a cloud failure"
  expect_contains "$out" "with exit 2 — a cloud failure" "a cloud failure"
}

test_an_update_that_ends_on_another_code_is_a_finding() {
  case_dir other_code
  printf '7' >"$dir/update.status"
  printf 'fugaro: something else\n' >"$dir/update.err"
  run821
  expect_status "$status" 1 "an update that ends on another code"
  expect_row "$out" update FAIL "an update that ends on another code"
  expect_contains "$out" "with exit 7, which is not one of update's own codes" "an update that ends on another code"
}

test_an_update_that_outlives_its_deadline_is_unverified() {
  case_dir deadline
  printf '5' >"$dir/update.sleep"
  printf 'fugaro: applying the roots\n' >"$dir/update.out"
  run821 --deadline 1
  expect_status "$status" 3 "an update that outlives its deadline"
  expect_row "$out" update UNVERIFIED "an update that outlives its deadline"
  expect_contains "$out" "was still running after 1s" "an update that outlives its deadline"
  # What it did print is still read for a question, and it asked none.
  expect_row "$out" asked PASS "an update that outlives its deadline"
  expect_row "$out" record PASS "an update that outlives its deadline"
}

test_an_update_that_asks_a_question_is_a_finding() {
  case_dir asked
  printf 'fugaro: continue? [y/N] ' >"$dir/update.out"
  printf 'fugaro: the images will be rebuilt\n' >"$dir/update.err"
  run821
  expect_status "$status" 1 "an update that asks a question"
  expect_row "$out" asked FAIL "an update that asks a question"
  expect_contains "$out" "1 line(s) of the update's own output ask for an answer" "an update that asks a question"
  expect_contains "$out" "[y/N]" "an update that asks a question"
}

test_an_update_that_asks_nothing_but_prints_nothing_is_unverified() {
  case_dir silent
  : >"$dir/update.out"
  : >"$dir/update.err"
  run821
  expect_status "$status" 3 "an update that prints nothing"
  expect_row "$out" update PASS "an update that prints nothing"
  expect_row "$out" asked UNVERIFIED "an update that prints nothing"
  expect_contains "$out" "printed nothing at all" "an update that prints nothing"
}

test_a_record_with_no_locations_row_is_unverified() {
  case_dir no_locations_row
  doctor_doc fugaro us-east5 true ""
  run821
  expect_status "$status" 3 "no locations row"
  expect_row "$out" record UNVERIFIED "no locations row"
  expect_contains "$out" "carries no locations row" "no locations row"
}

test_a_record_that_is_short_of_the_four_is_a_finding() {
  case_dir short_record
  doctor_doc fugaro us-east5 true "its Firestore database is in us-east5; its Realtime Database is in us-central1; its secret containers are replicated with Google's automatic replication; its run log bucket is in us-east5"
  run821
  expect_status "$status" 1 "a record short of the four"
  expect_row "$out" record FAIL "a record short of the four"
  expect_contains "$out" "its run log bucket is in global" "a record short of the four"
}

test_a_plan_that_changes_something_is_a_finding() {
  case_dir plan_changes
  printf '{"changes":{"imports":0,"creates":1,"updates":0,"deletes":0,"replaces":0},"left_for_you":[]}\n' >"$dir/plan.json"
  run821
  expect_status "$status" 1 "a plan that changes something"
  expect_row "$out" plan FAIL "a plan that changes something"
  expect_contains "$out" "the plan after the converge is not empty" "a plan that changes something"
  expect_contains "$out" "creates=1" "a plan that changes something"
}

test_a_plan_that_only_imports_is_unverified() {
  case_dir plan_imports
  printf '{"changes":{"imports":2,"creates":0,"updates":0,"deletes":0,"replaces":0},"left_for_you":[]}\n' >"$dir/plan.json"
  run821
  expect_status "$status" 3 "a plan that only imports"
  expect_row "$out" plan UNVERIFIED "a plan that only imports"
  expect_contains "$out" "it adopts 2 existing resource(s)" "a plan that only imports"
}

test_a_plan_that_is_not_there_is_unverified() {
  case_dir plan_missing
  printf '{"error":"no plan for this root"}\n' >"$dir/plan.json"
  run821
  expect_status "$status" 3 "a plan that is not there"
  expect_row "$out" plan UNVERIFIED "a plan that is not there"
  expect_contains "$out" "carried no plan" "a plan that is not there"
}

test_a_run_that_is_gone_after_the_update_is_a_finding() {
  case_dir run_gone
  printf '{"project":"fugaro","runs":[]}\n' >"$dir/runs.json"
  run821
  expect_status "$status" 1 "a run that is gone after the update"
  expect_row "$out" inflight FAIL "a run that is gone after the update"
  expect_contains "$out" "is NOT in 'fugaro runs ls --json" "a run that is gone after the update"
  expect_contains "$out" "the finding this row exists for" "a run that is gone after the update"
}

test_a_run_that_ends_failed_is_unverified() {
  case_dir run_failed
  run_doc "$dir/runs.json" failed failed "" false ""
  run821
  expect_status "$status" 3 "a run that ends failed"
  expect_row "$out" inflight UNVERIFIED "a run that ends failed"
  expect_contains "$out" "ended 'failed'" "a run that ends failed"
}

test_a_run_that_is_still_going_is_unverified() {
  case_dir run_still_going
  run_doc "$dir/runs.json" running
  run821 --wait 1
  expect_status "$status" 3 "a run that is still going"
  expect_row "$out" inflight UNVERIFIED "a run that is still going"
  expect_contains "$out" "is still running after 1s" "a run that is still going"
}

test_a_runs_read_the_follow_cannot_make_is_a_finding() {
  case_dir follow_unreadable
  printf '2' >"$dir/runs.fail-at"
  printf 'fugaro: the runs bucket could not be read\n' >"$dir/runs.err"
  run821
  expect_status "$status" 1 "a runs read the follow cannot make"
  expect_row "$out" inflight FAIL "a runs read the follow cannot make"
  expect_contains "$out" "while following" "a runs read the follow cannot make"
}

test_keep_scratch_leaves_the_directory_and_says_so() {
  case_dir kept
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT CHECK821_KEEP_SCRATCH=1 \
    PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    CHECK821_POLL_SECONDS=1 \
    bash "$script" --repo "$dir/repo" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 3 "kept scratch"
  expect_row "$out" restore UNVERIFIED "kept scratch"
  expect_contains "$out" "was KEPT on request" "kept scratch"
  local kept
  kept=$(sed -n 's/^.*keeping \(.*\) (CHECK821_KEEP_SCRATCH is set)$/\1/p' "$dir/err" | tail -1)
  [ -n "$kept" ] || fail "kept scratch: the check did not say which directory it kept"
  [ -d "$kept" ] || fail "kept scratch: $kept is not there"
  # It is the check's own scratch: the task the run was launched with is in it.
  [ -f "$kept/task.md" ] || fail "kept scratch: the directory kept is not the check's own scratch"
  rm -rf "$kept"
}

test_a_tool_that_is_not_on_path_stops_the_check() {
  case_dir no_tools
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT PATH="$emptybin" \
    FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" \
    "$bashbin" "$script" --repo "$dir/repo" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a tool that is not on PATH"
  expect_contains "$err" "is not on PATH, and this check is built on it" "a tool that is not on PATH"
}

# The fake is the strictness every case rests on, so it is driven directly: the
# flag sets are the real command's own, and anything else is 64.
test_the_fake_refuses_what_the_real_command_does_not_have() {
  case_dir fake_strictness
  local st
  st=0
  PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" fugaro runs bogus >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake fugaro: a sub-verb it does not have was not refused with 64 (got $st)"
  st=0
  PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" fugaro doctor --yes >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake fugaro: a flag doctor does not have was not refused with 64 (got $st)"
  st=0
  PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" fugaro update --project fugaro --location eu >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake fugaro: a flag update does not have was not refused with 64 (got $st)"
  local help
  help=$(PATH="$fakebin:$PATH" FAKE821_DIR="$dir" FAKE821_LOG="$dir/CALLS" fugaro --help 2>&1 || true)
  expect_contains "$help" "update" "the fake fugaro's own --help"
  expect_contains "$help" "doctor" "the fake fugaro's own --help"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK821_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK821_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help_prints_the_usage_and_calls_nothing
t test_a_dry_run_prints_the_plan_and_calls_nothing
t test_a_dry_run_with_no_run_says_so
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_an_unknown_argument_is_refused
t test_a_positional_argument_is_refused
t test_a_flag_without_a_value_is_refused
t test_a_wait_that_is_not_a_number_stops_the_check
t test_a_deadline_of_zero_stops_the_check
t test_a_repo_that_is_not_a_directory_stops_the_check
t test_a_checkout_without_a_fugaro_yaml_stops_the_check
t test_a_checkout_whose_doctor_names_nothing_stops_the_check
t test_another_installation_than_the_one_named_stops_the_check
t test_an_installation_in_another_region_stops_the_check
t test_a_fugaro_yaml_that_is_not_valid_stops_the_check
t test_no_run_measures_everything_but_the_run_in_flight
t test_a_launch_that_fails_is_a_finding
t test_a_launch_with_no_run_id_is_a_finding
t test_a_run_never_seen_running_is_unverified
t test_a_run_that_is_not_listed_is_unverified
t test_a_runs_read_the_launch_cannot_make_is_a_finding
t test_an_update_that_hands_off_a_step_is_unverified
t test_an_update_that_refuses_is_a_finding
t test_a_cloud_failure_is_a_finding
t test_an_update_that_ends_on_another_code_is_a_finding
t test_an_update_that_outlives_its_deadline_is_unverified
t test_an_update_that_asks_a_question_is_a_finding
t test_an_update_that_asks_nothing_but_prints_nothing_is_unverified
t test_a_record_with_no_locations_row_is_unverified
t test_a_record_that_is_short_of_the_four_is_a_finding
t test_a_plan_that_changes_something_is_a_finding
t test_a_plan_that_only_imports_is_unverified
t test_a_plan_that_is_not_there_is_unverified
t test_a_run_that_is_gone_after_the_update_is_a_finding
t test_a_run_that_ends_failed_is_unverified
t test_a_run_that_is_still_going_is_unverified
t test_a_runs_read_the_follow_cannot_make_is_a_finding
t test_keep_scratch_leaves_the_directory_and_says_so
t test_a_tool_that_is_not_on_path_stops_the_check
t test_the_fake_refuses_what_the_real_command_does_not_have

echo
if [ "$failures" -eq 0 ]; then
  echo "check-821_test.sh: ok"
else
  echo "check-821_test.sh: $failures failure(s)" >&2
  exit 1
fi
