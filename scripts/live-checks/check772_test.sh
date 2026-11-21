#!/usr/bin/env bash
# Tests for scripts/live-checks/check772.sh. NOTHING here is a cloud, a project,
# an installation or a run: every reading the check makes is a fixture in a
# temporary directory, served by a fake gcloud and a fake fugaro on PATH, and the
# cases assert what the script printed and what it refused.
#
# The bends are U4's own. A beat that is off with a floor of zero is the
# configuration the cold start has to be timed in; a beat that is ON with a
# floor of zero is a beat keeping nothing warm; a log with a silence in it holds
# a cold start, and one without holds none; a run that halted across the short
# outage is the bound being shorter than its name, and a run that halted across
# the long one carrying a workspace marker is not the outage being measured at
# all. Beside them are the readings the check has not taken: no beat setting to
# read, no run named, a listing that cannot be read.
#
# Usage: bash scripts/live-checks/check772_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check772.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK772_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK772_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK772_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

[ -f "$script" ] || {
  echo "check772_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
mkdir -p "$work/bin" "$fx"

# ---------------------------------------------------------------- the fakes
cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor)
  echo "release: nothing to move"
  echo "project: fugaro (GCP fugaro-dev, us-east5)"
  echo "secrets: none"
  ;;
config)
  if [ "$mode" = no-config ]; then echo "fake: config unreadable" >&2; exit 1; fi
  cat "$fx/config.json"
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

cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
args="$*"
case "$args" in
*"config get-value account"*) echo "owner@example.com" ;;
*"run services describe"*)
  if [ "$mode" = no-service ]; then echo "fake: no such service" >&2; exit 1; fi
  cat "$fx/service.json"
  ;;
*"logging read"*)
  if [ "$mode" = no-log ]; then echo "fake: logging denied" >&2; exit 1; fi
  cat "$fx/logs.json"
  ;;
*) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/gcloud"

# ---------------------------------------------------------------- the fixtures
service_json() { # service_json MIN
  cat <<JSON
{"status": {"url": "https://fugaro-workspace-credentials-fake.a.run.app",
            "latestReadyRevisionName": "fugaro-workspace-credentials-00042-abc"},
 "spec": {"template": {"scaling": {"minInstanceCount": $1, "maxInstanceCount": 10},
                       "spec": {"containers": [{"concurrency": 40}]}}}}
JSON
}

config_json() { # config_json BEAT
  printf '{"version": 1, "values": [{"path": "agent.workspace_beat", "value": %s, "source": "project"}]}' "$1"
}

# logs_json GAP: two calls 120ms apart, then a silence of GAP seconds, then one
# call — the cold start a window holds when the service went cold.
logs_json() { # logs_json GAP_SECONDS COLD_MS
  python3 - "$fx/logs.json" "$1" "$2" <<'PY'
import datetime
import json
import sys
path, gap, cold_ms = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
base = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=1)

def entry(offset, ms, instance):
    return {
        "timestamp": (base + datetime.timedelta(seconds=offset)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "httpRequest": {"status": 200, "latency": "%.3fs" % (ms / 1000.0),
                        "requestUrl": "https://svc/v1/messages"},
        "resource": {"labels": {"instance_id": instance}},
    }

out = [entry(0, 120, "inst-1"), entry(120, 130, "inst-1"),
       entry(120 + gap, cold_ms, "inst-2"), entry(120 + gap + 60, 125, "inst-2")]
with open(path, "w") as fh:
    json.dump(out, fh)
PY
}

# runs_json writes a listing with the two runs the outage was forced across.
runs_json() { # runs_json SHORT_HALT LONG_HALT
  python3 - "$fx/runs.json" "$1" "$2" <<'PY'
import datetime
import json
import sys
path, short_halt, long_halt = sys.argv[1], sys.argv[2], sys.argv[3]
now = datetime.datetime.now(datetime.timezone.utc)

def row(run_id, halt):
    out = {"run_id": run_id, "status": "done" if not halt else "halted",
           "terminal": True, "outcome": "" if halt else "ready"}
    if halt:
        out["halt"] = {
            "reason": halt, "scope": "run",
            "at": (now - datetime.timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "detail": "the credentials service could not be reached",
            "workspace_refusal": "cap" if halt == "marker" else "",
        }
    return out

doc = {"version": 1, "project": "fugaro",
       "runs": [row("20261121-000000-shrt", None if short_halt == "none" else short_halt),
                row("20261121-000000-long", None if long_halt == "none" else long_halt)],
       "totals": {}}
with open(path, "w") as fh:
    json.dump(doc, fh)
PY
}

fixtures_ok() {
  service_json 0 >"$fx/service.json"
  config_json false >"$fx/config.json"
  logs_json 900 2400 >"$fx/logs.json"
  runs_json none budget_unavailable
}

run772() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_MODE="${FAKE_MODE:-ok}" FAKE_FX="$fx" TMPDIR="$work" \
    ${RUN772_ENV[@]+"${RUN772_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN772_ENV=()
}

all_options=(
  CHECK772_DRY_RUN=
  CHECK772_WINDOW_MINUTES=180
  CHECK772_IDLE_GAP_SECONDS=600
)

run_check() {
  RUN772_ENV=("${all_options[@]}" ${CHECK772_TEST_ENV[@]+"${CHECK772_TEST_ENV[@]}"})
  run772 "$@"
}

# ------------------------------------------------------------------- the cases
test_help() {
  run772 --help
  expect_status "$status" 0 "check772.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "CHECK772_IDLE_GAP_SECONDS" "--help names the knobs"
}

test_dry_run_measures_nothing() {
  fixtures_ok
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT cold-start: INFO" "a planned row reads INFO, not PASS"
  expect_contains "$err" "fugaro config show" "the plan names the reads it would make"
}

test_a_cold_start_with_the_beat_off_passes() {
  fixtures_ok
  RUN772_ENV=("CHECK772_SHORT_RUN=shrt" "CHECK772_LONG_RUN=long" "CHECK772_OUTAGE_AT=$(( $(date -u +%s) - 900 ))")
  run772
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT beat: PASS" "the switch was read"
  expect_contains "$out" "is false in fugaro" "the row says what the setting is"
  expect_contains "$out" "FACT warm: PASS" "with the beat off the floor is zero by choice"
  expect_contains "$out" "FACT cold-start: PASS" "the cold start was timed"
  expect_contains "$out" "1 cold start(s) after a silence of at least 600s" "the row counts it"
  expect_contains "$out" "steady median of 125ms" "the row reads the steady latency beside it"
  expect_contains "$out" "U4's figure is that difference" "the row says what U4 was asked"
  expect_contains "$out" "FACT outage-short: PASS" "the short outage was ridden out"
  expect_contains "$out" "FACT outage-long: PASS" "the long outage ended in a halt"
  expect_contains "$out" "waited" "the halt's wait was measured"
  expect_contains "$out" "FACT outage-prompt: INFO" "the outage is the owner's, and the row says so"
}

test_a_beat_that_keeps_nothing_warm_fails() {
  service_json 0 >"$fx/service.json"
  config_json true >"$fx/config.json"
  logs_json 3600 2400 >"$fx/logs.json"
  runs_json none budget_unavailable
  run_check
  expect_status "$status" 1 "a beat that is on and a floor of zero is a warm claim the deployment does not hold"
  expect_contains "$out" "FACT warm: FAIL" "the warm row is the finding"
  expect_contains "$out" "nothing keeps the service warm" "the row says what is wrong"
}

test_a_warm_beat_that_never_goes_cold_passes() {
  service_json 1 >"$fx/service.json"
  config_json true >"$fx/config.json"
  logs_json 60 130 >"$fx/logs.json"
  runs_json none none
  RUN772_ENV=("CHECK772_IDLE_GAP_SECONDS=600" "CHECK772_SHORT_RUN=shrt" "CHECK772_LONG_RUN=long")
  run772
  expect_status "$status" 1 "a long outage that stopped nothing is the one finding here"
  expect_contains "$out" "FACT warm: PASS" "the floor is above zero"
  expect_contains "$out" "FACT cold-start: PASS" "with the beat on and no silence, there is no cold start"
  expect_contains "$out" "never went cold" "the row says the beat is doing its work"
  expect_contains "$out" "FACT outage-long: FAIL" "a long outage that stopped nothing is a failure"
  expect_contains "$out" "nothing stopped it" "the row says the run is still going"
}

test_a_beat_that_is_on_but_went_cold_fails() {
  service_json 1 >"$fx/service.json"
  config_json true >"$fx/config.json"
  logs_json 1200 2400 >"$fx/logs.json"
  runs_json none budget_unavailable
  run_check
  expect_status "$status" 1 "a service that went cold with the beat on is the beat not holding it"
  expect_contains "$out" "FACT cold-start: FAIL" "the cold-start row is the finding"
  expect_contains "$out" "the service went cold anyway" "the row says what happened"
}

test_a_window_with_no_silence_is_unmeasured_with_the_beat_off() {
  service_json 0 >"$fx/service.json"
  config_json false >"$fx/config.json"
  logs_json 60 130 >"$fx/logs.json"
  runs_json none budget_unavailable
  run_check
  expect_status "$status" 2 "no silence is no cold start to time"
  expect_contains "$out" "FACT cold-start: FAIL" "the cold-start row reads FAIL"
  expect_contains "$out" "Leave the service alone for at least 600s" "the row says how to produce the reading"
  expect_contains "$out" "then make ONE call" "and what call to make"
}

test_a_beat_absent_from_the_config_is_on_by_default() {
  fixtures_ok
  printf '{"version": 1, "values": [{"path": "budget.rtdb_url", "value": "https://fake", "source": "default"}]}' >"$fx/config.json"
  service_json 1 >"$fx/service.json"
  logs_json 3600 2400 >"$fx/logs.json"
  runs_json none budget_unavailable
  run_check
  expect_contains "$out" "FACT beat: PASS" "an absent key is the default and a pass"
  expect_contains "$out" "is absent from the resolved config" "the row says so"
  expect_contains "$out" "the beat is ON (D2985, R10)" "the row names the rule"
}

test_a_beat_value_that_is_not_a_switch_fails() {
  fixtures_ok
  config_json '"sometimes"' >"$fx/config.json"
  run_check
  expect_status "$status" 1 "a value no reader can take as a switch is a beat nobody can turn off"
  expect_contains "$out" "FACT beat: FAIL" "the beat row is the finding"
  expect_contains "$out" "neither true nor false" "the row says what it read"
}

test_a_config_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-config
  run_check
  expect_status "$status" 2 "a config that cannot be read measures nothing"
  expect_contains "$out" "FACT beat: FAIL" "the beat row reads FAIL"
  expect_contains "$out" "fugaro config show --project fugaro --json" "the row names the read that failed"
}

test_a_service_without_a_floor_is_unmeasured() {
  printf '{"status": {"url": "https://svc", "latestReadyRevisionName": "rev-1"}}' >"$fx/service.json"
  config_json true >"$fx/config.json"
  logs_json 60 130 >"$fx/logs.json"
  runs_json none budget_unavailable
  run_check
  expect_status "$status" 2 "a floor the document does not carry is not a pass"
  expect_contains "$out" "FACT warm: FAIL" "the warm row reads FAIL"
  expect_contains "$out" "no minimum instance count" "the row says what is missing"
}

test_a_log_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-log
  run_check
  expect_status "$status" 2 "a log that cannot be read measures nothing"
  expect_contains "$out" "FACT cold-start: FAIL" "the cold-start row reads FAIL"
  expect_contains "$out" "roles/logging.viewer" "the row names the role the read needs"
}

test_a_short_outage_that_stopped_the_run_fails() {
  service_json 0 >"$fx/service.json"
  config_json false >"$fx/config.json"
  logs_json 900 2400 >"$fx/logs.json"
  runs_json budget_unavailable budget_unavailable
  RUN772_ENV=("CHECK772_SHORT_RUN=shrt" "CHECK772_LONG_RUN=long")
  run772
  expect_status "$status" 1 "an outage shorter than the bound must not end a run"
  expect_contains "$out" "FACT outage-short: FAIL" "the short row is the finding"
  expect_contains "$out" "the bound is shorter than its name" "the row says what that means"
}

test_a_long_outage_that_carries_a_marker_fails() {
  service_json 0 >"$fx/service.json"
  config_json false >"$fx/config.json"
  logs_json 900 2400 >"$fx/logs.json"
  runs_json none marker
  RUN772_ENV=("CHECK772_SHORT_RUN=shrt" "CHECK772_LONG_RUN=long")
  run772
  expect_status "$status" 1 "a marker means the service answered, which an outage is not"
  expect_contains "$out" "FACT outage-long: FAIL" "the long row is the finding"
  expect_contains "$out" "carrying the workspace's own refusal marker" "the row says what it read"
}

test_no_run_named_is_unmeasured() {
  fixtures_ok
  run_check
  expect_status "$status" 2 "without a run id there is nothing to read"
  expect_contains "$out" "FACT outage-short: FAIL" "the short row reads FAIL"
  expect_contains "$out" "CHECK772_SHORT_RUN was not set" "the row names the knob it needs"
  expect_contains "$out" "FACT outage-long: FAIL" "the long row reads FAIL"
  expect_contains "$out" "CHECK772_LONG_RUN was not set" "the row names the knob it needs"
}

test_a_run_id_that_is_not_in_the_listing_is_unmeasured() {
  fixtures_ok
  RUN772_ENV=("CHECK772_SHORT_RUN=zzzz" "CHECK772_LONG_RUN=long")
  run772
  expect_status "$status" 2 "an id nobody has is not a run to read"
  expect_contains "$out" "FACT outage-short: FAIL" "the short row reads FAIL"
  expect_contains "$out" "no run in the listing has an id ending in zzzz" "the row says what it looked for"
}

test_a_run_listing_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-runs
  RUN772_ENV=("CHECK772_SHORT_RUN=shrt" "CHECK772_LONG_RUN=long")
  run772
  expect_status "$status" 2 "a listing that cannot be read measures nothing"
  expect_contains "$out" "FACT outage-long: FAIL" "the long row reads FAIL"
  expect_contains "$out" "fugaro runs ls --json" "the row names the read that failed"
  expect_contains "$out" "FACT outage-short: FAIL" "and the row that reads the same listing"
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
  expect_status "$status" 1 "another installation is not this check's subject"
  expect_contains "$err" "this is not the fugaro-dev installation" "the refusal says what it read"
  expect_contains "$out" "FACT service: FAIL — not reached" "nothing was measured"
}

test_an_agent_session_is_refused() {
  fixtures_ok
  RUN772_ENV=(CLAUDECODE=1)
  run772
  expect_status "$status" 1 "no agent runs any step of Check 772"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_absent "$out" "FACT service: PASS" "nothing was read before the refusal"
}

test_a_zero_idle_gap_is_refused() {
  fixtures_ok
  RUN772_ENV=("CHECK772_IDLE_GAP_SECONDS=0")
  run772
  expect_status "$status" 2 "a gap of zero is not a silence"
  expect_contains "$err" "CHECK772_IDLE_GAP_SECONDS is 0" "the refusal says what it read"
}

test_an_unknown_argument_is_refused() {
  run772 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK772_TEST_ONLY=name runs that one.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK772_TEST_ONLY:-}" ]; then
    [ "$1" = "${CHECK772_TEST_ONLY}" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  FAKE_MODE=ok
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_a_cold_start_with_the_beat_off_passes
t test_a_beat_that_keeps_nothing_warm_fails
t test_a_warm_beat_that_never_goes_cold_passes
t test_a_beat_that_is_on_but_went_cold_fails
t test_a_window_with_no_silence_is_unmeasured_with_the_beat_off
t test_a_beat_absent_from_the_config_is_on_by_default
t test_a_beat_value_that_is_not_a_switch_fails
t test_a_config_that_cannot_be_read_is_unmeasured
t test_a_service_without_a_floor_is_unmeasured
t test_a_log_that_cannot_be_read_is_unmeasured
t test_a_short_outage_that_stopped_the_run_fails
t test_a_long_outage_that_carries_a_marker_fails
t test_no_run_named_is_unmeasured
t test_a_run_id_that_is_not_in_the_listing_is_unmeasured
t test_a_run_listing_that_cannot_be_read_is_unmeasured
t test_the_installation_guard_refuses_another_project
t test_an_agent_session_is_refused
t test_a_zero_idle_gap_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check772_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check772_test.sh: ok"
