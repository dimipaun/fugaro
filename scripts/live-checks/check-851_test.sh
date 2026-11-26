#!/usr/bin/env bash
# Tests for scripts/live-checks/check-851.sh. NOTHING here is a cloud, a project,
# an installation, an update or a run: every reading the check makes is a
# fixture in a temporary directory, served by a fake gcloud and a fake fugaro on
# PATH, and the cases assert what the script printed and what it refused.
#
# The bends are R11's and R12's own. A doctor document with a warning is an
# update that left a step behind it; an update log without the in-flight sentence
# is a release that does not say what the HARD RULE asks it to say; a log with a
# step that did not land is the operator step the HARD RULE forbids; a run that
# is not terminal, or one whose halt falls inside the update's window, is the run
# the update was not allowed to touch. R12 and §9 are the member that never
# turned the switch on: a warning on it is the release having moved an
# installation that uses nothing of the personal half, and a line of the
# workspace's own service naming a `fugaro_u_` container for its run is the half
# having reached it anyway. Beside them are the readings the check has to be
# honest about not having taken: no log path, no run id, no workspace project, no
# quiet member, a listing or a service log that cannot be read, a run id nobody
# has.
#
# Usage: bash scripts/live-checks/check-851_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-851.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK851_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK851_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK851_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

[ -f "$script" ] || {
  echo "check-851_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
mkdir -p "$work/bin" "$fx"

# ---------------------------------------------------------------- the fakes
write_fake_fugaro() {
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor)
  project=fugaro
  json=no
  shift || true
  while [ $# -gt 0 ]; do
    case "$1" in
    --project) project=${2:-fugaro}; shift 2 ;;
    --json) json=yes; shift ;;
    --all) shift ;;
    *) shift ;;
    esac
  done
  if [ "$json" = yes ]; then
    if [ "$mode" = no-doctor-json ]; then echo "fake: doctor --json unreadable" >&2; exit 1; fi
    if [ ! -r "$fx/doctor-$project.json" ]; then echo "fake: no doctor document for $project" >&2; exit 1; fi
    cat "$fx/doctor-$project.json"
  else
    echo "release: nothing to move"
    echo "project: fugaro (GCP fugaro-dev, us-east5)"
    echo "secrets: none"
  fi
  ;;
"runs")
  if [ "${2:-}" = "ls" ]; then
    if [ "$mode" = no-runs ]; then echo "fake: runs unreadable" >&2; exit 1; fi
    cat "$fx/runs.json"
    exit 0
  fi
  echo "fake fugaro: unknown invocation $*" >&2
  exit 2
  ;;
*) echo "fake fugaro: unknown invocation $*" >&2; exit 2 ;;
esac
SH
  chmod +x "$work/bin/fugaro"
}
write_fake_fugaro

cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
args="$*"
case "$args" in
*"config get-value account"*) echo "owner@example.com" ;;
*"logging read"*)
  if [ "$mode" = no-log ]; then echo "fake gcloud: logging read failed" >&2; exit 1; fi
  cat "$fx/log-quiet.json"
  ;;
*) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/gcloud"

# ---------------------------------------------------------------- the fixtures
# doctor_json PROJECT WARNINGS...: doctor's document, one warning per problem.
doctor_json() { # doctor_json PROJECT PROBLEM...
  local project=$1
  shift
  local checks="" one
  for one in ${1+"$@"}; do
    checks="${checks}${checks:+, }{\"id\": \"check\", \"severity\": \"warning\", \"problem\": \"$one\"}"
  done
  local ok=true
  [ -z "$checks" ] || ok=false
  printf '{"ok": %s, "checks": [{"id": "base", "severity": "info", "problem": ""}%s]}' \
    "$ok" "${checks:+,$checks}" >"$fx/doctor-$project.json"
}

# update_log PATH KEEP_INFLIGHT EXTRA_LINES...
update_log() { # update_log PATH KEEP_INFLIGHT LINE...
  local path=$1 keep=$2
  shift 2
  : >"$path"
  echo "fugaro update: starting" >>"$path"
  if [ "$keep" = yes ]; then
    echo "fugaro update: 2 in flight: update waits for none and changes nothing they hold" >>"$path"
  fi
  local one
  for one in ${1+"$@"}; do echo "$one" >>"$path"; done
  echo "fugaro update: done" >>"$path"
}

# runs_json writes a listing with the two runs the updates ran across.
runs_json() { # runs_json SHORT_TERMINAL SHORT_HALT LONG_TERMINAL LONG_HALT
  python3 - "$fx/runs.json" "$1" "$2" "$3" "$4" <<'PY'
import datetime
import json
import sys
path, short_terminal, short_halt, long_terminal, long_halt = sys.argv[1:6]
now = datetime.datetime.now(datetime.timezone.utc)

def row(run_id, terminal, halt):
    out = {"run_id": run_id, "status": "done" if halt == "" else "halted",
           "terminal": terminal == "yes", "outcome": "" if halt else "ready"}
    if halt:
        out["halt"] = {
            "reason": halt, "scope": "run",
            "at": (now - datetime.timedelta(seconds=30)).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "detail": "the credentials service could not be reached",
            "workspace_refusal": "",
        }
    return out

doc = {"version": 1, "project": "fugaro",
       "runs": [row("20261121-000000-shrt", short_terminal, short_halt),
                row("20261121-000000-long", long_terminal, long_halt)],
       "totals": {}}
with open(path, "w") as fh:
    json.dump(doc, fh)
PY
}

# quiet_log NAMED_PERSONAL: the workspace's credentials service's own lines for
# the run of a member whose switch was never turned on. With NAMED_PERSONAL=no
# they are what that member was served before — the shared entries — and never a
# fugaro_u_ container, which is R12 and §9 read as one reading.
quiet_log() { # quiet_log yes|no
  python3 - "$fx/log-quiet.json" "$1" <<'PY'
import json
import sys
path, named = sys.argv[1:3]

def row(credential, tier):
    return {"jsonPayload": {"msg": "credsvc: audit", "status": "ok", "credential": credential,
                            "tier": tier, "purpose": "workspace", "run": "20261121-000000-quit",
                            "slip": "slip-quiet", "owner": "owner@example.com"},
            "timestamp": "2026-11-26T00:00:00Z"}

rows = [row("shared-claude-oauth-token", "C")]
if named == "yes":
    rows.append(row("fugaro_u_1234-claude-oauth-token", "A"))
with open(path, "w") as fh:
    json.dump(rows, fh)
PY
}

# quiet_log_empty: a read that answered, with no line for the run at all.
quiet_log_empty() { printf '[]' >"$fx/log-quiet.json"; }

fixtures_ok() {
  doctor_json ws >"$fx/doctor-ws.json"
  doctor_json fugaro >"$fx/doctor-fugaro.json"
  doctor_json quiet >"$fx/doctor-quiet.json"
  update_log "$fx/ws-update.log" yes
  update_log "$fx/member-update.log" yes
  runs_json yes "" yes ""
  quiet_log no
}

all_options=(
  CHECK851_WORKSPACE_PROJECT=ws
  CHECK851_QUIET_PROJECT=quiet
  CHECK851_QUIET_RUN=quit
)

run851() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_MODE="${FAKE_MODE:-ok}" FAKE_FX="$fx" TMPDIR="$work" \
    ${RUN851_ENV[@]+"${RUN851_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  CHECK851_TEST_ENV=()
}

run_check() {
  RUN851_ENV=("${all_options[@]}" ${CHECK851_TEST_ENV[@]+"${CHECK851_TEST_ENV[@]}"})
  run851 "$@"
}

# ------------------------------------------------------------------- the cases
test_help() {
  run851 --help
  expect_status "$status" 0 "check-851.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "WORKSPACE_UPDATE_LOG" "--help names the logs the check reads"
}

test_dry_run_measures_nothing() {
  fixtures_ok
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT workspace-converged: INFO" "a planned row reads INFO, not PASS"
  expect_contains "$err" "fugaro doctor --project ws --all --json" "the plan names the reads it would make"
}

test_a_converged_pair_and_two_finished_runs_pass() {
  fixtures_ok
  CHECK851_TEST_ENV=(
    CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log"
    CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log"
    CHECK851_WORKSPACE_RUN=shrt
    CHECK851_MEMBER_RUN=long
  )
  run_check
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT workspace-converged: PASS" "the workspace's doctor is clean"
  expect_contains "$out" "FACT member-converged: PASS" "the member's doctor is clean"
  expect_contains "$out" "FACT quiet-converged: PASS" "and the member that never turned the switch on is untouched"
  expect_contains "$out" "NO warning after the update" "the row says what converged means"
  expect_contains "$out" "FACT workspace-update-line: PASS" "the update named the runs in flight"
  expect_contains "$out" "FACT member-update-line: PASS" "and so did the member's"
  expect_contains "$out" "no line naming a step that did not land" "the row says the update owed nothing more"
  expect_contains "$out" "FACT workspace-run: PASS" "the workspace's run finished"
  expect_contains "$out" "FACT member-run: PASS" "the member's run finished"
  expect_contains "$out" "the update reached neither its environment nor its route" "the row names the rule it read"
  expect_contains "$out" "FACT quiet-unchanged: PASS" "the quiet member's run was served exactly what it was served before"
  expect_contains "$out" "takes none of it" "the row names the rule it read"
}

test_a_warning_on_the_member_with_the_switch_off_fails() {
  fixtures_ok
  doctor_json quiet "the personal half reached a member that never turned the switch on" >"$fx/doctor-quiet.json"
  run_check
  expect_status "$status" 1 "a member that never turned the switch on is untouched, or the release moved it"
  expect_contains "$out" "FACT quiet-converged: FAIL" "the quiet member's doctor row is the finding"
  expect_contains "$out" "never turned the switch on" "the row quotes the warning"
}

test_a_quiet_members_run_served_a_personal_token_fails() {
  fixtures_ok
  quiet_log yes
  run_check
  expect_status "$status" 1 "R12: a member without the switch takes none of the personal half"
  expect_contains "$out" "FACT quiet-unchanged: FAIL" "the quiet member's row is the finding"
  expect_contains "$out" "carry a fugaro_u_ container" "the row says what it read"
  expect_contains "$out" "never asked for it" "the row says what that means"
}

test_a_quiet_log_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-log
  run_check
  expect_status "$status" 2 "a log that cannot be read measures nothing"
  expect_contains "$out" "FACT quiet-unchanged: FAIL" "the quiet member's row is unmeasured"
  expect_contains "$out" "could not be read for the run quit" "the row names the read that failed"
}

test_a_quiet_run_the_service_wrote_nothing_for_is_unmeasured() {
  fixtures_ok
  quiet_log_empty
  run_check
  expect_status "$status" 2 "an id nobody called under measures nothing"
  expect_contains "$out" "FACT quiet-unchanged: FAIL" "the quiet member's row is unmeasured"
  expect_contains "$out" "wrote no line at all" "the row says what it found"
}

test_no_quiet_member_is_unmeasured_with_the_prompt() {
  fixtures_ok
  RUN851_ENV=(
    CHECK851_WORKSPACE_PROJECT=ws
    CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log"
    CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log"
    CHECK851_WORKSPACE_RUN=shrt
    CHECK851_MEMBER_RUN=long
  )
  run851
  expect_status "$status" 2 "without that member's run there is nothing to read"
  expect_contains "$out" "FACT quiet-unchanged: FAIL" "the quiet member's row reads FAIL"
  expect_contains "$out" "CHECK851_QUIET_RUN is unset" "the row names the knob it needs"
  expect_contains "$out" "quiet-run-prompt" "the prompt names the run to launch"
  expect_contains "$out" "FACT quiet-converged: FAIL — not reached" "no quiet member was named, so its doctor was not read"
}

test_a_warning_on_the_workspace_fails() {
  fixtures_ok
  doctor_json ws "the workspace's installation is at an older policy" >"$fx/doctor-ws.json"
  RUN851_ENV=(CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log" CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log")
  run_check
  expect_status "$status" 1 "a warning after the update is a step the update did not take"
  expect_contains "$out" "FACT workspace-converged: FAIL" "the workspace's doctor row is the finding"
  expect_contains "$out" "older policy" "the row quotes the warning"
  expect_contains "$out" "the fix belongs IN the update" "the row says where the fix belongs"
}

test_a_warning_on_the_member_fails() {
  fixtures_ok
  doctor_json fugaro "the plugin pin is at the previous release" >"$fx/doctor-fugaro.json"
  run_check
  expect_status "$status" 1 "the member's side converges too, or the update is not the whole upgrade"
  expect_contains "$out" "FACT member-converged: FAIL" "the member's doctor row is the finding"
  expect_contains "$out" "previous release" "the row quotes the warning"
}

test_a_doctor_document_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-doctor-json
  run_check
  expect_status "$status" 2 "a doctor document that cannot be read measures nothing"
  expect_contains "$out" "FACT workspace-converged: FAIL" "the workspace's row reads FAIL"
  expect_contains "$out" "fugaro doctor --project ws --all --json" "the row names the read that failed"
  expect_contains "$out" "FACT member-converged: FAIL" "and so does the member's"
}

test_an_update_log_without_the_in_flight_line_fails() {
  fixtures_ok
  update_log "$fx/ws-update.log" no
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log" CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log")
  run_check
  expect_status "$status" 1 "the HARD RULE asks the update to say it in one line"
  expect_contains "$out" "FACT workspace-update-line: FAIL" "the update-log row is the finding"
  expect_contains "$out" "printed no 'in flight: update waits for none and changes nothing they hold' line" "the row quotes the sentence it looked for"
  expect_contains "$out" "grep 'in flight'" "the row says how to read the log yourself"
}

test_a_step_that_did_not_land_fails() {
  fixtures_ok
  update_log "$fx/member-update.log" yes "fugaro update: the policy publish was refused: permission denied"
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log" CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log")
  run_check
  expect_status "$status" 1 "a step the update printed as failed is an operator step the release ships"
  expect_contains "$out" "FACT member-update-line: FAIL" "the member's update-log row is the finding"
  expect_contains "$out" "the policy publish was refused" "the row quotes the line"
  expect_contains "$out" "the next release has to take it inside the update" "the row says what to do"
}

test_a_log_path_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_UPDATE_LOG="$fx/nothing.log" CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log")
  run_check
  expect_status "$status" 2 "a log nobody saved is not a reading"
  expect_contains "$out" "FACT workspace-update-line: FAIL" "the row reads FAIL"
  expect_contains "$out" "$fx/nothing.log" "the row names the path it could not read"
}

test_a_run_that_is_not_terminal_fails() {
  fixtures_ok
  runs_json no "" yes ""
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=long)
  run_check
  expect_status "$status" 1 "the update may not hold a run"
  expect_contains "$out" "FACT workspace-run: FAIL" "the run row is the finding"
  expect_contains "$out" "is not terminal after the update" "the row says what it read"
  expect_contains "$out" "fugaro runs show 20261121-000000-shrt" "the row names the read for it"
}

test_a_halt_inside_the_update_window_fails() {
  fixtures_ok
  runs_json yes "" yes budget_unavailable
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=long
    CHECK851_WORKSPACE_UPDATE_AT=$(( $(date -u +%s) - 300 )))
  run_check
  expect_status "$status" 1 "a halt inside the update's own window is a run the update stopped"
  expect_contains "$out" "FACT member-run: FAIL" "the member's run row is the finding"
  expect_contains "$out" "which R11 forbids" "the row names the rule"
  expect_contains "$out" "after the update began" "the row measures how long after"
}

test_a_halt_outside_the_window_still_fails() {
  fixtures_ok
  runs_json yes "" yes budget_unavailable
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=long
    CHECK851_WORKSPACE_UPDATE_AT=$(( $(date -u +%s) - 86400 )))
  run_check
  expect_status "$status" 1 "a halted run is a finding of its own, and the row says so"
  expect_contains "$out" "FACT member-run: FAIL" "the run row is the finding"
  expect_contains "$out" "halted after the update" "the row says what it read"
  expect_contains "$out" "needs the moment the update ran" "the row names the knob that would place it"
  expect_absent "$out" "a run whose halt falls inside the update's own window" "an old moment is not inside the window"
}

test_a_run_without_an_update_moment_still_reads_the_halt() {
  fixtures_ok
  runs_json yes "" yes budget_unavailable
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=long)
  run_check
  expect_status "$status" 1 "a halt is read whether or not the moment was named"
  expect_contains "$out" "FACT member-run: FAIL" "the run row is the finding"
  expect_contains "$out" "halted after the update" "the row says what it read"
}

test_a_run_id_nobody_has_is_unmeasured() {
  fixtures_ok
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=zzzz)
  run_check
  expect_status "$status" 2 "an id nobody has is not a run to read"
  expect_contains "$out" "FACT member-run: FAIL" "the member's run row reads FAIL"
  expect_contains "$out" "no run in the listing has an id ending in zzzz" "the row says what it looked for"
}

test_a_run_listing_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-runs
  CHECK851_TEST_ENV=(CHECK851_WORKSPACE_RUN=shrt CHECK851_MEMBER_RUN=long)
  run_check
  expect_status "$status" 2 "a listing that cannot be read measures nothing"
  expect_contains "$out" "FACT workspace-run: FAIL" "the workspace's run row reads FAIL"
  expect_contains "$out" "not read: the run listing could not be read" "the row says why"
}

test_no_run_id_and_no_log_is_unmeasured_with_the_prompt() {
  fixtures_ok
  run_check
  expect_status "$status" 2 "without the knobs there is nothing to read"
  expect_contains "$out" "FACT workspace-update-line: FAIL" "the workspace's log row reads FAIL"
  expect_contains "$out" "CHECK851_WORKSPACE_UPDATE_LOG is unset" "the row names the knob it needs"
  expect_contains "$out" "FACT workspace-run: FAIL" "the workspace's run row reads FAIL"
  expect_contains "$out" "CHECK851_WORKSPACE_RUN is unset" "the row names the knob it needs"
  expect_contains "$out" "FACT update-prompt: INFO" "the two updates are the owner's, and the row says so"
}

test_no_workspace_project_is_unmeasured() {
  fixtures_ok
  CHECK851_TEST_ENV=(
    CHECK851_WORKSPACE_UPDATE_LOG="$fx/ws-update.log"
    CHECK851_MEMBER_UPDATE_LOG="$fx/member-update.log"
    CHECK851_WORKSPACE_RUN=shrt
    CHECK851_MEMBER_RUN=long
  )
  run851
  expect_status "$status" 2 "the workspace's project is a reading this check cannot guess"
  expect_contains "$out" "FACT workspace-converged: FAIL" "the workspace's doctor row reads FAIL"
  expect_contains "$out" "CHECK851_WORKSPACE_PROJECT was not set" "the row names the knob it needs"
}

test_the_installation_guard_refuses_another_project() {
  fixtures_ok
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor) echo "project: other (GCP other-prod, us-central1)" ;;
esac
SH
  chmod +x "$work/bin/fugaro"
  run_check
  write_fake_fugaro
  expect_status "$status" 1 "another installation is not this check's subject"
  expect_contains "$err" "this is not the fugaro-dev installation" "the refusal says what it read"
  expect_contains "$out" "FACT workspace-converged: FAIL — not reached" "nothing was measured"
}

test_an_agent_session_is_refused() {
  fixtures_ok
  RUN851_ENV=(CLAUDECODE=1 CHECK851_WORKSPACE_PROJECT=ws)
  run851
  expect_status "$status" 1 "no agent runs any step of Check 851"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_absent "$out" "FACT workspace-converged: PASS" "nothing was read before the refusal"
}

test_a_zero_update_window_is_refused() {
  fixtures_ok
  CHECK851_TEST_ENV=("CHECK851_UPDATE_WINDOW_SECONDS=0")
  run_check
  expect_status "$status" 2 "a window of zero is not a window"
  expect_contains "$err" "CHECK851_UPDATE_WINDOW_SECONDS is 0" "the refusal says what it read"
}

test_an_unknown_argument_is_refused() {
  run851 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK851_TEST_ONLY=name runs that one.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK851_TEST_ONLY:-}" ]; then
    [ "$1" = "${CHECK851_TEST_ONLY}" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  FAKE_MODE=ok
  RUN851_ENV=()
  CHECK851_TEST_ENV=()
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_a_converged_pair_and_two_finished_runs_pass
t test_a_warning_on_the_workspace_fails
t test_a_warning_on_the_member_fails
t test_a_warning_on_the_member_with_the_switch_off_fails
t test_a_quiet_members_run_served_a_personal_token_fails
t test_a_quiet_log_that_cannot_be_read_is_unmeasured
t test_a_quiet_run_the_service_wrote_nothing_for_is_unmeasured
t test_a_doctor_document_that_cannot_be_read_is_unmeasured
t test_an_update_log_without_the_in_flight_line_fails
t test_a_step_that_did_not_land_fails
t test_a_log_path_that_cannot_be_read_is_unmeasured
t test_a_run_that_is_not_terminal_fails
t test_a_halt_inside_the_update_window_fails
t test_a_halt_outside_the_window_still_fails
t test_a_run_without_an_update_moment_still_reads_the_halt
t test_a_run_id_nobody_has_is_unmeasured
t test_a_run_listing_that_cannot_be_read_is_unmeasured
t test_no_run_id_and_no_log_is_unmeasured_with_the_prompt
t test_no_quiet_member_is_unmeasured_with_the_prompt
t test_no_workspace_project_is_unmeasured
t test_the_installation_guard_refuses_another_project
t test_an_agent_session_is_refused
t test_a_zero_update_window_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-851_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-851_test.sh: ok"
