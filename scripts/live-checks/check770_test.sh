#!/usr/bin/env bash
# Tests for scripts/live-checks/check770.sh. NOTHING here is a cloud, a project
# or an installation: every reading the check makes is a fixture in a temporary
# directory, served by a fake gcloud, a fake fugaro and a fake curl on PATH, and
# the cases assert what the script printed and what it refused.
#
# The bends are the point. Check 770 is read for the evidence a load leaves
# behind, and the readings that decide its rows are the ones a fixture can bend:
# a service whose document carries no limits, a log with no request in it, a log
# with a 429 in it, a log with fewer requests than a load of 100 runs leaves, a
# log whose newest entries are the service's own application lines rather than
# requests, a read that comes back at its own limit, a meter node with a
# reservation older than the TTL, a database that refuses the read, a token
# gcloud will not mint, and a shell that is a coding agent's session. Each of
# those is a run that has to end as it says it does — and none of them may end 0
# without having measured the row it is about.
#
# Usage: bash scripts/live-checks/check770_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check770.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK770_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK770_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK770_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

# the script is missing is itself a case: run770 fails loudly rather than
# silently passing, which is the red step the plan names.
[ -f "$script" ] || {
  echo "check770_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
mkdir -p "$work/bin" "$fx"

# ---------------------------------------------------------------- the fakes
# write_fake_fugaro serves the three readings the check makes of fugaro: doctor
# --all for the installation guard, --json for the config, and version. It is a
# function because one case bends doctor's answer and has to put the fake back.
write_fake_fugaro() {
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
case "${1:-}" in
version)
  echo "fugaro 9.9.9-test"
  ;;
doctor)
  if [ "$mode" = no-doctor ]; then exit 1; fi
  echo "release: nothing to move"
  echo "project: fugaro (GCP fugaro-dev, us-east5)"
  echo "secrets: none"
  ;;
config)
  # config show --project <name> --json
  if [ "$mode" = no-config ]; then echo "fake: config unreadable" >&2; exit 1; fi
  beat=true
  [ "$mode" = beat-off ] && beat=false
  cat <<JSON
{"version": 1, "values": [
  {"path": "budget.rtdb_url", "value": "https://fake-rtdb.example", "source": "default"},
  {"path": "agent.workspace_beat", "value": $beat, "source": "default"}
]}
JSON
  ;;
runs)
  if [ "$mode" = no-runs ]; then echo "fake: runs unreadable" >&2; exit 1; fi
  echo '{"version": 1, "project": "fugaro", "runs": [], "totals": {}}'
  ;;
*)
  echo "fake fugaro: unknown invocation $*" >&2
  exit 2
  ;;
esac
SH
  chmod +x "$work/bin/fugaro"
}
write_fake_fugaro

# fake_gcloud serves the service document, the request log, the account and the
# access token. Every fixture is written below, so a case can bend one reading
# without touching the others.
cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
args="$*"
case "$args" in
*"config get-value account"*)
  echo "owner@example.com"
  ;;
*"auth print-access-token"*)
  if [ "$mode" = no-token ]; then echo "fake: no token" >&2; exit 1; fi
  echo "fake-access-token"
  ;;
*"run services describe"*)
  if [ "$mode" = no-service ]; then echo "fake: no such service" >&2; exit 1; fi
  cat "$fx/service.json"
  ;;
*"logging read"*)
  if [ "$mode" = no-log ]; then echo "fake: logging denied" >&2; exit 1; fi
  # The arguments are kept, so a case can assert WHICH window and WHICH filter
  # the read asked for: the log holds the service's own application lines beside
  # its requests, and the filter is what keeps them out of a load's count.
  printf '%s\n' "$args" >"$fx/logging-args"
  cat "$fx/logs.json"
  ;;
*"iam roles describe"*)
  if [ "$mode" = no-role ]; then echo "fake: no such role" >&2; exit 1; fi
  cat "$fx/role.json"
  ;;
*"projects get-iam-policy"*)
  if [ "$mode" = no-policy ]; then echo "fake: permission denied" >&2; exit 1; fi
  cat "$fx/policy.json"
  ;;
*)
  echo "fake gcloud: unknown invocation $args" >&2
  exit 2
  ;;
esac
SH
chmod +x "$work/bin/gcloud"

cat >"$work/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
dest=""
path=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o) dest=$2; shift 2 ;;
  -w) shift 2 ;;
  -H) shift 2 ;;
  --max-time) shift 2 ;;
  -sS) shift ;;
  *) path=$1; shift ;;
  esac
done
case "$mode" in
db-denied)
  printf '%s' '{"error": "Permission denied"}' >"$dest"
  printf '403'
  ;;
db-404)
  printf '%s' 'null' >"$dest"
  printf '404'
  ;;
*)
  cat "$fx/meter.json" >"$dest"
  printf '200'
  ;;
esac
exit 0
SH
chmod +x "$work/bin/curl"

# ---------------------------------------------------------------- the fixtures
service_json() { # service_json MAXINSTANCES CONCURRENCY
  cat <<JSON
{"status": {"url": "https://fugaro-workspace-credentials-fake.a.run.app",
            "latestReadyRevisionName": "fugaro-workspace-credentials-00042-abc"},
 "spec": {"template": {"scaling": {"minInstanceCount": 0, "maxInstanceCount": $1},
                       "spec": {"containers": [{"concurrency": $2}]}}}}
JSON
}

# log_fixture REQUESTS INSTANCES writes a request log of that many calls over
# that many instance ids, every one a 200 — the shape a loaded service leaves,
# and wide enough that a load of CHECK770_RUNS runs is not read as too small.
log_fixture() { # log_fixture REQUESTS INSTANCES
  python3 - "$fx/logs.json" "$1" "$2" <<'PY'
import json
import sys
path, count, instances = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
out = []
for i in range(count):
    out.append({
        "timestamp": "2026-11-21T%02d:%02d:%02dZ" % (i // 3600, (i // 60) % 60, i % 60),
        "httpRequest": {"status": 200, "latency": "0.%03ds" % (100 + i % 800),
                        "requestUrl": "https://svc/v1/messages"},
        "resource": {"labels": {"instance_id": "inst-%d" % (i % instances + 1)}},
    })
with open(path, "w") as fh:
    json.dump(out, fh)
PY
}

fixtures_ok() {
  service_json 10 40 >"$fx/service.json"
  log_fixture 320 3
  meter_with_reservation 1
}

# run770 runs the script with an agent's markers out of the environment: the
# suite is itself run inside an agent's session, and one of the check's own
# refusals is exactly those markers — the case below sets one back to read it.
run770() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" \
    FAKE_MODE="${FAKE_MODE:-ok}" FAKE_FX="$fx" \
    TMPDIR="$work" \
    ${RUN770_ENV[@]+"${RUN770_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN770_ENV=()
}

# now_ms writes the meter fixture's `at` for a reservation held the given number
# of hours ago, so the TTL case is the TTL rather than a date that ages.
meter_with_reservation() { # meter_with_reservation HOURS_AGO
  local now age
  now=$(( $(date -u +%s) * 1000 ))
  age=$(( now - $1 * 3600 * 1000 ))
  printf '{"month": "2026-11", "members": {"member-a": 1200000}, "reservations": {"call-1": {"member": "member-a", "amount": 50000, "at": %s}}}' "$age" >"$fx/meter.json"
}

all_options=(
  CHECK770_DRY_RUN=
  CHECK770_DATABASE_URL=https://fake-rtdb.example
  CHECK770_WORKSPACE=demo
  CHECK770_WINDOW_MINUTES=60
)

run_check() { # run_check [extra args]
  RUN770_ENV=("${all_options[@]}" ${CHECK770_TEST_ENV[@]+"${CHECK770_TEST_ENV[@]}"})
  run770 "$@"
}

# ------------------------------------------------------------------- the cases
test_help() {
  run770 --help
  expect_status "$status" 0 "check770.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "check770.sh --dry-run" "--help names the dry run"
  expect_contains "$out" "DATABASE_URL" "--help names the knobs"
}

test_dry_run_measures_nothing() {
  fixtures_ok
  meter_with_reservation 1
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "CHECK 770 RESULT" "the block is printed"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT load-window: INFO" "a planned row reads INFO, not PASS"
  expect_absent "$out" "FACT load-window: PASS" "the dry run measured nothing"
  expect_contains "$err" "gcloud run services describe" "the plan names the describe it would make"
}

test_a_healthy_load_passes() {
  fixtures_ok
  meter_with_reservation 1
  run_check
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT service: PASS" "the service was described"
  expect_contains "$out" "fugaro-workspace-credentials-00042-abc" "the row names the revision"
  expect_contains "$out" "FACT limits: PASS" "the platform holds R9's first values"
  expect_contains "$out" "FACT load-window: PASS" "the window holds the load"
  expect_contains "$out" "320 requests over 3 distinct instances" "the count is the log's own"
  expect_contains "$out" "FACT reservations: PASS" "the meter was read and holds nothing stale"
  expect_contains "$out" "FACT restore: PASS" "nothing was created or deleted"
  expect_contains "$out" "FACT scratch-removed: PASS" "the scratch directory is gone"
  expect_contains "$out" "FACT load-ceiling: INFO" "the ceiling reading is a note and not a criterion"
  expect_contains "$out" "FACT load-prompt: INFO" "the load itself is the owner's, and the row says so"
}

test_a_missing_service_is_not_a_measurement() {
  fixtures_ok
  meter_with_reservation 1
  FAKE_MODE=no-service
  run_check
  expect_status "$status" 2 "a service that cannot be described is not a pass"
  expect_contains "$err" "could not be described" "the refusal says what failed"
  expect_contains "$err" "fake: no such service" "the refusal carries the tool's own line"
  expect_contains "$out" "FACT service: FAIL — not reached" "the block still prints every key"
}

test_limits_that_differ_from_r9_fail() {
  service_json 100 80 >"$fx/service.json"
  cat >"$fx/logs.json" <<'JSON'
[{"timestamp": "2026-11-21T00:01:00Z", "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}}]
JSON
  meter_with_reservation 1
  run_check
  expect_status "$status" 1 "a deployed limit that is not R9's is a measured failure"
  expect_contains "$out" "FACT limits: FAIL" "the limits row is the finding"
  expect_contains "$out" "R9 states 10 and 40" "the row names what the design says"
  expect_contains "$out" "correct R9" "the row says what O1 asks the owner to do"
}

test_a_document_without_limits_is_unmeasured() {
  cat >"$fx/service.json" <<'JSON'
{"status": {"url": "https://svc", "latestReadyRevisionName": "rev-1"}}
JSON
  cat >"$fx/logs.json" <<'JSON'
[{"timestamp": "2026-11-21T00:01:00Z", "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}}]
JSON
  meter_with_reservation 1
  run_check
  expect_status "$status" 2 "a limit the document does not carry is not a pass"
  expect_contains "$out" "FACT limits: FAIL" "the row reads FAIL"
  expect_contains "$out" "carries no max instance count" "the row says why it could not measure"
  expect_contains "$err" "could NOT measure limits" "the run says which rows it could not measure"
}

test_a_refusal_in_the_window_fails() {
  service_json 10 40 >"$fx/service.json"
  cat >"$fx/logs.json" <<'JSON'
[{"timestamp": "2026-11-21T00:01:00Z", "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}},
 {"timestamp": "2026-11-21T00:01:10Z", "httpRequest": {"status": 429, "latency": "0.010s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}},
 {"timestamp": "2026-11-21T00:01:20Z", "httpRequest": {"status": 503, "latency": "0.010s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-2"}}}]
JSON
  meter_with_reservation 1
  run_check
  expect_status "$status" 1 "a refusal under load is what R9's values are corrected for"
  expect_contains "$out" "FACT load-window: FAIL" "the window row is the finding"
  expect_contains "$out" "REFUSED some of them with 429 503" "the row names the statuses"
  expect_contains "$out" "raise the maximum instances or the concurrency" "the row says what O1 asks for"
}

test_an_empty_window_is_not_a_measurement() {
  service_json 10 40 >"$fx/service.json"
  printf '[]' >"$fx/logs.json"
  meter_with_reservation 1
  run_check
  expect_status "$status" 2 "no request to read is no load to measure"
  expect_contains "$out" "FACT load-window: FAIL" "the row reads FAIL rather than PASS"
  expect_contains "$out" "holds no request at all" "the row says the window is empty"
  expect_contains "$out" "CHECK770_RUNS=100" "the row names the load the owner has to run"
  expect_contains "$out" "across CHECK770_MEMBERS=2 members" "and how many members it spans"
}

test_a_window_too_small_for_the_load_is_unmeasured() {
  service_json 10 40 >"$fx/service.json"
  cat >"$fx/logs.json" <<'JSON'
[{"timestamp": "2026-11-21T00:01:00Z", "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}},
 {"timestamp": "2026-11-21T00:01:00Z", "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
  "resource": {"labels": {"instance_id": "inst-1"}}}]
JSON
  meter_with_reservation 1
  run_check
  expect_status "$status" 2 "fewer requests than a load leaves is not this load"
  expect_contains "$out" "FACT load-window: FAIL" "the row reads FAIL"
  expect_contains "$out" "which is fewer than the 100" "the row says why"
  expect_contains "$out" "widen CHECK770_WINDOW_MINUTES" "the row says what to do"
}

test_a_log_that_cannot_be_read_is_unmeasured() {
  service_json 10 40 >"$fx/service.json"
  FAKE_MODE=no-log
  meter_with_reservation 1
  run_check
  expect_status "$status" 2 "a log that cannot be read measures nothing"
  expect_contains "$out" "FACT load-window: FAIL" "the row reads FAIL"
  expect_contains "$out" "roles/logging.viewer" "the row names the role the read needs"
}

test_a_stale_reservation_fails() {
  fixtures_ok
  meter_with_reservation 9
  run_check
  expect_status "$status" 1 "a reservation past the TTL is room no settle will find"
  expect_contains "$out" "FACT reservations: FAIL" "the meter row is the finding"
  expect_contains "$out" "1 reservation(s) are held" "the row counts them"
  expect_contains "$out" "TTL 6h" "the row names the bound it broke"
  expect_contains "$out" "member-a" "the row names the member the call was for"
}

test_a_database_that_refuses_the_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=db-denied
  run_check
  expect_status "$status" 2 "a refused database read is not a pass"
  expect_contains "$out" "FACT reservations: FAIL" "the row reads FAIL"
  expect_contains "$out" "refused the read with 403" "the row names the status"
}

test_no_database_url_is_not_asked_for() {
  fixtures_ok
  RUN770_ENV=("CHECK770_DRY_RUN=" "CHECK770_WINDOW_MINUTES=60")
  run770
  expect_status "$status" 2 "a row nobody asked for is not measured"
  expect_contains "$out" "FACT reservations: FAIL" "the row reads FAIL"
  expect_contains "$out" "CHECK770_DATABASE_URL" "the row names the knob that would read it"
  expect_contains "$out" "meter/workspace/<name>/<month>" "the row names the path"
}

test_a_token_gcloud_will_not_mint_is_refused() {
  fixtures_ok
  FAKE_MODE=no-token
  run_check
  expect_status "$status" 2 "without a token the meter cannot be read"
  expect_contains "$err" "could not mint an access token" "the refusal says what failed"
  expect_contains "$out" "FACT reservations: FAIL — not reached" "the row was never reached"
}

test_application_lines_beside_one_request_are_not_a_load() {
  service_json 10 40 >"$fx/service.json"
  # 150 of the service's OWN lines in the window — credsvc writes an audit line
  # for every served call — and ONE request entry. Counting the application lines
  # as requests reads this window as 151 requests and passes the load row over a
  # window with no load in it.
  python3 - "$fx/logs.json" <<'PY'
import json
import sys
out = []
for i in range(150):
    out.append({"timestamp": "2026-11-21T00:%02d:00Z" % (i % 60),
                "textPayload": "credsvc: audit run=2026 repository=owner/repo purpose=messages status=ok",
                "resource": {"labels": {"instance_id": "inst-1"}}})
out.append({"timestamp": "2026-11-21T00:59:00Z",
            "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
            "resource": {"labels": {"instance_id": "inst-1"}}})
with open(sys.argv[1], "w") as fh:
    json.dump(out, fh)
PY
  meter_with_reservation 1
  run_check
  expect_status "$status" 2 "one request is not a load of 100 runs, however many lines sit beside it"
  expect_contains "$out" "FACT load-window: FAIL" "the row reads FAIL rather than PASS"
  expect_absent "$out" "FACT load-window: PASS" "the application lines are not a served load"
  expect_contains "$out" "which is fewer than the 100" "the row counts the requests, not the entries"
  expect_contains "$(cat "$fx/logging-args")" "httpRequest.status>=0" "the read asked the log for its request entries"
}

test_a_read_at_its_own_limit_is_unmeasured() {
  service_json 10 40 >"$fx/service.json"
  # Five entries and a limit of five: the read came back full, so whatever the
  # service logged older than the newest five was never read — and a truncated
  # window's refusals are exactly what a pass would be hiding.
  python3 - "$fx/logs.json" <<'PY'
import json
import sys
out = []
for i in range(5):
    out.append({"timestamp": "2026-11-21T00:%02d:00Z" % i,
                "httpRequest": {"status": 200, "latency": "0.120s", "requestUrl": "https://svc/v1/messages"},
                "resource": {"labels": {"instance_id": "inst-1"}}})
with open(sys.argv[1], "w") as fh:
    json.dump(out, fh)
PY
  meter_with_reservation 1
  CHECK770_TEST_ENV=(CHECK770_LOG_LIMIT=5)
  run_check
  expect_status "$status" 2 "a window read at its limit is a window nobody read"
  expect_contains "$out" "FACT load-window: FAIL" "the row reads FAIL rather than PASS"
  expect_absent "$out" "FACT load-window: PASS" "a truncated read is never a pass"
  expect_contains "$out" "the read came back at its own limit" "the row says what happened"
  expect_contains "$out" "--limit=5" "the row names the limit it read at"
  expect_contains "$out" "Narrow CHECK770_WINDOW_MINUTES" "the row says how to read it whole"
}

test_the_installation_guard_refuses_another_project() {
  fixtures_ok
  meter_with_reservation 1
  RUN770_ENV=("CHECK770_DRY_RUN=" "CHECK770_DATABASE_URL=https://fake-rtdb.example" "CHECK770_WORKSPACE=demo")
  # A doctor that prints no fugaro-dev project line: the check reads the wrong
  # installation and has to stop rather than measure it.
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor) echo "project: other (GCP other-prod, us-central1)" ;;
*) echo "fake fugaro: unknown" >&2; exit 2 ;;
esac
SH
  chmod +x "$work/bin/fugaro"
  run770
  expect_status "$status" 1 "another installation is not this check's subject"
  expect_contains "$err" "this is not the fugaro-dev installation" "the refusal says what it read"
  expect_contains "$out" "FACT service: FAIL — not reached" "nothing was measured"
  write_fake_fugaro
}

test_a_belong_name_is_refused() {
  fixtures_ok
  meter_with_reservation 1
  RUN770_ENV=("CHECK770_DRY_RUN=" "CHECK770_SERVICE=belong-svc")
  run770
  expect_status "$status" 1 "this check is never run on belong"
  expect_contains "$err" "is belong" "the refusal names it"
}

test_an_agent_session_is_refused() {
  fixtures_ok
  meter_with_reservation 1
  RUN770_ENV=(CLAUDECODE=1)
  run770
  expect_status "$status" 1 "no agent runs any step of Check 770"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_contains "$err" "coding agent" "the refusal says what the session is"
  expect_absent "$out" "FACT service: PASS" "nothing was read before the refusal"
}

test_an_xtraced_shell_is_refused() {
  fixtures_ok
  meter_with_reservation 1
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_MODE=ok FAKE_FX="$fx" TMPDIR="$work" \
    CHECK770_DATABASE_URL=https://fake-rtdb.example CHECK770_WORKSPACE=demo \
    bash -x "$script" 2>"$work/err") || status=$?
  err=$(cat "$work/err" 2>/dev/null || true)
  expect_status "$status" 1 "a traced shell is refused"
  expect_contains "$err" "this shell is tracing" "the refusal says so"
}

test_keep_scratch_keeps_it_and_says_so() {
  fixtures_ok
  meter_with_reservation 1
  RUN770_ENV=("CHECK770_DRY_RUN=" "CHECK770_DATABASE_URL=https://fake-rtdb.example" "CHECK770_WORKSPACE=demo" "CHECK770_KEEP_SCRATCH=1")
  run770
  expect_status "$status" 1 "a scratch directory that is still there is not a clean run"
  expect_contains "$out" "FACT scratch-removed: FAIL" "the row says the directory is still there"
  scratch=$(printf '%s\n' "$err" | sed -n 's/^check770\.sh: scratch directory: \(.*\) (mode 0700.*/\1/p' | head -n 1)
  [ -n "$scratch" ] || fail "the run printed no scratch directory"
  [ -d "$scratch" ] || fail "CHECK770_KEEP_SCRATCH kept nothing: $scratch is gone"
  rm -rf "$scratch"
}

test_a_row_never_reached_reads_fail_not_reached() {
  fixtures_ok
  meter_with_reservation 1
  FAKE_MODE=no-service
  run_check
  expect_contains "$out" "FACT limits: FAIL — not reached" "a row the run never reached says so"
  expect_contains "$out" "FACT load-prompt: FAIL — not reached" "and so does the last one"
  expect_contains "$out" "FACT restore: PASS" "the restore ran anyway"
}

test_an_unknown_argument_is_refused() {
  run770 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own files under
# $work), CHECK770_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK770_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK770_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  FAKE_MODE=ok
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_a_healthy_load_passes
t test_a_missing_service_is_not_a_measurement
t test_limits_that_differ_from_r9_fail
t test_a_document_without_limits_is_unmeasured
t test_a_refusal_in_the_window_fails
t test_an_empty_window_is_not_a_measurement
t test_a_window_too_small_for_the_load_is_unmeasured
t test_a_log_that_cannot_be_read_is_unmeasured
t test_application_lines_beside_one_request_are_not_a_load
t test_a_read_at_its_own_limit_is_unmeasured
t test_a_stale_reservation_fails
t test_a_database_that_refuses_the_read_is_unmeasured
t test_no_database_url_is_not_asked_for
t test_a_token_gcloud_will_not_mint_is_refused
t test_the_installation_guard_refuses_another_project
t test_a_belong_name_is_refused
t test_an_agent_session_is_refused
t test_an_xtraced_shell_is_refused
t test_keep_scratch_keeps_it_and_says_so
t test_a_row_never_reached_reads_fail_not_reached
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check770_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check770_test.sh: ok"
