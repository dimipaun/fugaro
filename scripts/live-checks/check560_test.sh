#!/usr/bin/env bash
# Tests for scripts/live-checks/check560.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project or a licence server, no agent
# runs a live command, and both fakes record every call — with the directory it
# was made in — so a case can assert what the check read, that it ran the CLI in
# the sandbox checkout, and that it fired the Scheduler job exactly once.
#
# The fake `gcloud` serves the installation's own shapes out of the case's files:
# the Scheduler locations, the Scheduler job (its state, its target and the
# account its call is authorised as), the Cloud Run job, the executions of that
# job, and the job's own lines out of Cloud Logging. The fake `fugaro` serves
# `doctor --json` and the two `licence status --json` reads (before and after the
# fire), which is where a case bends the premise and the term. No licence key is
# built here at all: the documents this check reads carry none, and the key IDs
# the fixtures use are words a case can look for.
#
# Usage: bash scripts/live-checks/check560_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check560_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check560_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check560.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK560_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK560_TEST_HEAD:-1600}")" ;;
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
# expect_row: assert on the ONE row's own text. Every row is a sentence the
# register quotes, so a case reads the row it is about rather than the block.
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}
# no_calls: neither fake was given anything at all, which is how a case says a
# guard stopped the check before it read the installation.
no_calls() { # what
  local log
  for log in "$dir/state/logs/gcloud.calls" "$dir/state/logs/fugaro.calls"; do
    if [ -s "$log" ]; then
      fail "$1: a call was made: $(cat "$log")"
    fi
  done
}
# no_fire: the Scheduler job was never run. It is the ONE write this check makes,
# so the cases about a premise that does not hold say it did not happen.
no_fire() { # what
  if [ -e "$dir/state/logs/fire" ]; then
    fail "$1: the Scheduler job was fired: $(cat "$dir/state/logs/fire")"
  fi
}
# no_object_reads: no gcloud call of any kind reached the runs bucket. The
# licence object holds every key the project has, and `gcloud storage cat` on it
# would print them: the check must never read it.
no_object_reads() { # what
  local calls=""
  [ -f "$dir/state/logs/gcloud.calls" ] && calls=$(cat "$dir/state/logs/gcloud.calls")
  case "$calls" in
  *storage* | *bucket* | *"objects"*) fail "$1: a read of the runs bucket was made: $calls" ;;
  esac
}

project=chk560-gcp-proj
fugaro_project=chk560-install
region=europe-west2
sched_region=europe-west1
job=fugarolicence
sched_job=fugaro-licence-renew
# The two key IDs: words a case can look for. Neither is a key: the check reads
# documents that carry none, and the specification calls the ID a support handle.
pre_key=chk560-key-old
successor=chk560-key-new

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check560_test.sh only: every
# answer is served out of $FAKE560_DIR and every call is recorded there with the
# directory it was made in. Nothing here is a real API call.
set -euo pipefail
dir=${FAKE560_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/gcloud.calls"
sub=""
for a in "$@"; do
  case "$a" in
  -*) break ;;
  esac
  sub="$sub${sub:+ }$a"
done
location=""
region=""
prev=""
for a in "$@"; do
  case "$prev" in
  --location) location=$a ;;
  --region) region=$a ;;
  esac
  prev=$a
done
case "$sub" in
"scheduler locations list")
  if [ -f "$dir/locations.rc" ]; then
    cat "$dir/locations.rc" >&2
    exit "$(cat "$dir/locations.rc")"
  fi
  cat "$dir/locations.json"
  ;;
"scheduler jobs describe"*)
  if [ -n "$location" ] && [ -f "$dir/sched.$location.json" ]; then
    cat "$dir/sched.$location.json"
    exit 0
  fi
  printf 'ERROR: (gcloud.scheduler.jobs.describe) NOT_FOUND: job not found in %s\n' "${location:-no location}" >&2
  exit 1
  ;;
"scheduler jobs run"*)
  printf 'gcloud scheduler jobs run %s --location %s\n' "$4" "$location" >>"$dir/logs/fire"
  if [ -f "$dir/fire.rc" ]; then
    printf 'ERROR: (gcloud.scheduler.jobs.run) job could not be run\n' >&2
    exit "$(cat "$dir/fire.rc")"
  fi
  printf 'Job execution triggered.\n'
  ;;
"run jobs describe"*)
  if [ -f "$dir/job.rc" ]; then
    printf 'ERROR: (gcloud.run.jobs.describe) NOT_FOUND: job not found\n' >&2
    exit "$(cat "$dir/job.rc")"
  fi
  cat "$dir/job.json"
  ;;
"run jobs executions list")
  n=0
  [ -f "$dir/execs.count" ] && n=$(cat "$dir/execs.count")
  n=$((n + 1))
  printf '%s\n' "$n" >"$dir/execs.count"
  if [ -f "$dir/execs.after" ] && [ "$n" -le "$(cat "$dir/execs.after")" ]; then
    printf '[]\n'
    exit 0
  fi
  if [ -f "$dir/execs.rc" ]; then
    printf 'ERROR: (gcloud.run.jobs.executions.list) permission denied\n' >&2
    exit "$(cat "$dir/execs.rc")"
  fi
  cat "$dir/execs.json"
  ;;
"logging read"*)
  if [ -f "$dir/logs.rc" ]; then
    printf 'ERROR: (gcloud.logging.read) permission denied\n' >&2
    exit "$(cat "$dir/logs.rc")"
  fi
  # The filter is honoured the way Cloud Logging honours it, with one liberty the
  # older fixtures need: an entry that carries no execution_name label is served
  # to any filter. A filter that names no execution (what this check read before
  # it named one) is served everything.
  python3 - "$dir/logs.json" "$*" <<'PY'
import json, re, sys

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
m = re.search(r'execution_name"="([^"]+)"', sys.argv[2])
name = m.group(1) if m else ""
out = []
for e in doc:
    if not isinstance(e, dict):
        continue
    if name:
        lab = (e.get("labels") or {}).get("run.googleapis.com/execution_name")
        if lab is not None and lab != name:
            continue
    out.append(e)
print(json.dumps(out))
PY
  ;;
*)
  printf 'fake gcloud: no route for: %s\n' "$*" >&2
  exit 9
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for the installed `fugaro`, for scripts/live-checks/check560_test.sh
# only: it answers doctor and the two licence status reads out of $FAKE560_DIR,
# and records every call with the directory it was made in (the check runs it
# inside the sandbox checkout, and a case asserts that). No licence key is read
# or printed here: the documents it serves carry none.
set -euo pipefail
dir=${FAKE560_DIR:?}
mkdir -p "$dir/logs"
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/fugaro.calls"
case "$*" in
"doctor --json")
  if [ -f "$dir/doctor.rc" ]; then
    printf 'fugaro: doctor: something is wrong with this installation\n' >&2
    exit "$(cat "$dir/doctor.rc")"
  fi
  cat "$dir/doctor.json"
  ;;
"licence status --json")
  n=0
  [ -f "$dir/status.count" ] && n=$(cat "$dir/status.count")
  n=$((n + 1))
  printf '%s\n' "$n" >"$dir/status.count"
  if [ -f "$dir/status.rc" ] && [ "$n" -ge 2 ]; then
    printf 'fugaro: licence status: the store could not be read\n' >&2
    exit "$(cat "$dir/status.rc")"
  fi
  if [ -f "$dir/status.after.json" ] && [ "$n" -ge 2 ]; then
    cat "$dir/status.after.json"
  else
    cat "$dir/status.before.json"
  fi
  ;;
*)
  printf 'fake fugaro: no route for: %s\n' "$*" >&2
  exit 9
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ fixtures
# status_doc FILE PAID_THROUGH KEY_ID SOURCE STATE MODE: K3.2's `licence status
# --json` shape, with the fields this check reads. It carries no key at all,
# which is the whole reason the check reads the licence this way.
status_doc() { # status_doc FILE PAID KEY SOURCE STATE MODE
  python3 - "$@" <<'PY'
import json, sys
out, paid, key, source, state, mode = sys.argv[1:7]
doc = {
    "project": "chk560-install",
    "state": state,
    "tier": "team",
    "seats": 5,
    "paid_through": paid,
    "kind": "subscription",
    "renewal_mode": mode,
    "source": source,
    "revoked": False,
    "last_check": {"at": "2026-11-20T04:23:11Z", "status": "ok", "key_id": key},
    "warning": None,
}
open(out, "w").write(json.dumps(doc, indent=2) + "\n")
PY
}

doctor_doc() { # doctor_doc FILE NAME GCP REGION
  python3 - "$@" <<'PY'
import json, sys
out, name, gcp, region = sys.argv[1:5]
open(out, "w").write(json.dumps({"ok": True, "checks": [], "project": {"name": name, "gcp_project": gcp, "region": region}}) + "\n")
PY
}

# execs_doc FILE CREATED OFFSET_SECONDS COMPLETED: the executions list. CREATED is
# "now" shifted by OFFSET_SECONDS, so a case can hand the check an execution made
# after the fire it is about to make (a positive offset) or one from before it
# (a negative one). COMPLETED is the Completed condition's status, "" for an
# execution that is still running.
execs_doc() { # execs_doc FILE OFFSET COMPLETED
  python3 - "$@" <<'PY'
import datetime, json, sys
out, offset, completed = sys.argv[1], int(sys.argv[2]), sys.argv[3]
now = datetime.datetime.now(datetime.timezone.utc)
created = (now + datetime.timedelta(seconds=offset)).strftime("%Y-%m-%dT%H:%M:%SZ")
status = {"startTime": created}
if completed:
    status["conditions"] = [{"type": "Completed", "status": completed}]
    status["completionTime"] = created
doc = [{
    "metadata": {"name": "projects/p/locations/l/jobs/fugarolicence/executions/fugarolicence-abc12", "creationTimestamp": created},
    "status": status,
}]
open(out, "w").write(json.dumps(doc, indent=2) + "\n")
PY
}

# logs_doc FILE LINE...: a Cloud Logging answer holding the job's own lines, each
# given with no timestamp of its own (they are newest-first as gcloud prints
# them, and the check sorts them by the timestamps written here).
logs_doc() { # logs_doc FILE LINE...
  python3 - "$@" <<'PY'
import datetime, json, sys
out, lines = sys.argv[1], sys.argv[2:]
now = datetime.datetime.now(datetime.timezone.utc)
entries = []
for i, text in enumerate(lines):
    at = (now + datetime.timedelta(seconds=i)).strftime("%Y-%m-%dT%H:%M:%S.000000Z")
    entries.append({"timestamp": at, "severity": "INFO", "textPayload": text})
open(out, "w").write(json.dumps(entries, indent=2) + "\n")
PY
}

# logs_exec_doc FILE "EXECUTION|LINE" ...: the same answer with the
# `run.googleapis.com/execution_name` label Cloud Run puts on each entry, oldest
# first, so a case can put a line from an execution that ran BEFORE the fire in
# the same answer.
logs_exec_doc() { # logs_exec_doc FILE EXECUTION|LINE...
  python3 - "$@" <<'PY'
import datetime, json, sys
out, rows = sys.argv[1], sys.argv[2:]
now = datetime.datetime.now(datetime.timezone.utc)
entries = []
for i, row in enumerate(rows):
    name, text = row.split("|", 1)
    at = (now + datetime.timedelta(seconds=i)).strftime("%Y-%m-%dT%H:%M:%S.000000Z")
    entries.append({
        "timestamp": at,
        "severity": "INFO",
        "textPayload": text,
        "labels": {"run.googleapis.com/execution_name": name},
    })
open(out, "w").write(json.dumps(entries, indent=2) + "\n")
PY
}

scheduler_doc() { # scheduler_doc FILE STATE URI METHOD SA SCOPE
  python3 - "$@" <<'PY'
import json, sys
out, state, uri, method, sa, scope = sys.argv[1:7]
doc = {
    "name": "projects/p/locations/l/jobs/fugaro-licence-renew",
    "schedule": "23 4 * * *",
    "timeZone": "Etc/UTC",
    "state": state,
    "httpTarget": {"uri": uri, "httpMethod": method, "oauthToken": {"serviceAccountEmail": sa, "scope": scope}},
    "retryConfig": {"retryCount": 0},
}
open(out, "w").write(json.dumps(doc, indent=2) + "\n")
PY
}

job_doc() { # job_doc FILE LABEL_FUGARO LABEL_ROLE ARGS SA FUGARO_PROJECT GCP_PROJECT [TIMEOUT_SECONDS] [MAX_RETRIES]
  python3 - "$@" <<'PY'
import json, sys
out, lf, lr, args, sa, fp, gp = sys.argv[1:8]
timeout = sys.argv[8] if len(sys.argv) > 8 else "600"
retries = int(sys.argv[9]) if len(sys.argv) > 9 else 0
doc = {
    "metadata": {"name": "fugarolicence", "labels": {"fugaro": lf, "fugaro_role": lr}},
    "spec": {
        "template": {
            "spec": {
                "template": {
                    "spec": {
                        "serviceAccountName": sa,
                        "timeoutSeconds": timeout,
                        "maxRetries": retries,
                        "containers": [{
                            "image": "europe-west2-docker.pkg.dev/p/coordinator/fugaro:latest",
                            "command": ["/usr/local/bin/fugaro"],
                            "args": args.split(),
                            "env": [{"name": "FUGARO_PROJECT", "value": fp}, {"name": "FUGARO_GCP_PROJECT", "value": gp}, {"name": "FUGARO_BUCKET", "value": "chk560-runs"}],
                        }],
                    }
                }
            }
        }
    },
}
open(out, "w").write(json.dumps(doc, indent=2) + "\n")
PY
}

# prepare DIR: the happy path's whole state — a sandbox installation holding a
# short-lived auto key paid through a date, a Scheduler job that calls the Cloud
# Run job, an execution the fire makes, and the job's line saying it renewed.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home" "$dir/sandbox"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  doctor_doc "$dir/state/doctor.json" "$fugaro_project" "$project" "$region"
  status_doc "$dir/state/status.before.json" "2026-12-01T00:00:00Z" "$pre_key" project active auto
  status_doc "$dir/state/status.after.json" "2027-01-01T00:00:00Z" "$pre_key" project active auto
  scheduler_doc "$dir/state/sched.$sched_region.json" ENABLED \
    "https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/$job:run" \
    POST "fugaro-scheduler@$project.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  job_doc "$dir/state/job.json" managed licence "licence renew --job" \
    "fugaro-licence@$project.iam.gserviceaccount.com" "$fugaro_project" "$project"
  printf '[{"locationId":"%s"},{"locationId":"us-central1"}]\n' "$sched_region" >"$dir/state/locations.json"
  execs_doc "$dir/state/execs.json" 120 True
  logs_doc "$dir/state/logs.json" \
    "checked: the licence server answered that key $pre_key is good" \
    "renewed: key $successor is installed in project $fugaro_project beside the key it replaces (fugaro/licence.json)" \
    "  tier:            team" \
    "  seats:           5"
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
  mkdir -p "$dir/tmp" "$dir/home" "$dir/state/logs"
  : >"$dir/state/logs/gcloud.calls"
  : >"$dir/state/logs/fugaro.calls"
  rm -f "$dir/state/logs/fire" "$dir/state/execs.count" "$dir/state/status.count"
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
  # marker again, and a variable given as an argument wins over `env -u`. The
  # timing knobs are small here unless a case names its own: no case waits out
  # the real 600s a live run allows.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE560_DIR="$dir/state" \
    CHECK560_WAIT="${CHECK560_TEST_WAIT:-3}" CHECK560_POLL=1 CHECK560_LOG_WAIT=2 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check560\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  # CHECK560_TEST_DUMP=1 prints the block a case just read: a case that fails on
  # a row is much easier to read with the whole block in front of it.
  if [ -n "${CHECK560_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
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

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check560.sh --sandbox" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, who runs it and what it never prints.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "no licence key and no renewal secret" "--help: what is never printed"
  expect_contains "$out" "\`fugaro-prod\` is refused unless \`--production" "--help: the production guard"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$err" "gcloud scheduler jobs run $sched_job" "a dry run: the fire is planned"
  expect_contains "$err" "gcloud logging read" "a dry run: the job's lines are planned"
  expect_contains "$err" "fugaro licence status --json" "a dry run: the two status reads are planned"
  no_calls "a dry run"
  no_fire "a dry run"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row sandbox "$fugaro_project" "the happy path: the installation doctor answered for"
  expect_row sandbox "$region" "the happy path: the region came from doctor"
  expect_row project "$project" "the happy path: the GCP project"
  expect_row scheduler "Scheduler location $sched_region" "the happy path: the tick's own region"
  expect_row scheduler "schedule '23 4 * * *' in 'Etc/UTC'" "the happy path: the tick's own cron and time zone"
  expect_row scheduler "POST https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/$job:run" "the happy path: Scheduler's own call to the job"
  expect_row job "labels fugaro=managed and fugaro_role=licence" "the happy path: the job is this installation's licence job"
  expect_row job "argv 'licence renew --job'" "the happy path: the job's own verb"
  expect_row job "FUGARO_PROJECT=$fugaro_project and FUGARO_GCP_PROJECT=$project" "the happy path: the two names the module pins"
  expect_row premise "paid through 2026-12-01T00:00:00Z" "the happy path: the term before the fire"
  expect_row execution "Completed condition True" "the happy path: the execution finished"
  expect_row renewed "$successor" "the happy path: the successor key ID the job's own line names"
  expect_row renewed "key $successor is installed in project $fugaro_project" "the happy path: the job's own sentence"
  expect_row status "strictly later than 2026-12-01T00:00:00Z" "the happy path: the term moved"
  expect_row restore "It deleted nothing" "the happy path: what is left behind"
  expect_row scratch "removed from the exit trap" "the happy path: the scratch directory"
  # The one write: the fire, and the CLI run in the sandbox rather than anywhere.
  expect_contains "$(cat "$dir/state/logs/fire")" "gcloud scheduler jobs run $sched_job --location $sched_region" "the happy path: the Scheduler job's own fire"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "doctor --json (in $dir/sandbox)" "the happy path: doctor ran in the sandbox checkout"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "licence status --json (in $dir/sandbox)" "the happy path: licence status ran in the sandbox checkout"
  no_object_reads "the happy path"
  assert_scratch_removed "the happy path"
}

test_unknown_argument() {
  local dir=$work/args
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  no_calls "an unknown argument"
}

test_sandbox_missing() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" -- --sandbox "$dir/not-there"
  expect_status "$status" 2 "a sandbox that is not there"
  no_calls "a sandbox that is not there"
}

test_belong_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project belong-this
  expect_status "$status" 1 "a value naming belong"
  expect_contains "$err" "this check is never run there" "a value naming belong: the refusal"
  no_calls "a value naming belong"
  no_fire "a value naming belong"
}

test_production_refused() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_contains "$out" "refused unless --production" "fugaro-prod without --production: the refusal"
  no_calls "fugaro-prod without --production"
  no_fire "fugaro-prod without --production"
}

test_production_named() {
  local dir=$work/prod-ok
  prepare "$dir"
  # The installation doctor answers for is production, named with --production:
  # the row has to say so rather than reading as an ordinary sandbox run.
  doctor_doc "$dir/state/doctor.json" "$fugaro_project" fugaro-prod "$region"
  job_doc "$dir/state/job.json" managed licence "licence renew --job" \
    "fugaro-licence@fugaro-prod.iam.gserviceaccount.com" "$fugaro_project" fugaro-prod
  scheduler_doc "$dir/state/sched.$sched_region.json" ENABLED \
    "https://run.googleapis.com/v2/projects/fugaro-prod/locations/$region/jobs/$job:run" \
    POST "fugaro-scheduler@fugaro-prod.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  run_check "$dir" -- --project fugaro-prod --production
  expect_status "$status" 0 "production named with --production"
  expect_row project "production, named with --production" "production named with --production: the row says so"
}

test_agent_session_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "coding agent's session" "a coding agent's session: the refusal names it"
  no_calls "a coding agent's session"
  no_fire "a coding agent's session"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a tracing shell"
  expect_contains "$err" "this shell is tracing" "a tracing shell: the refusal"
  no_calls "a tracing shell"
}

test_doctor_fails() {
  local dir=$work/doctor
  prepare "$dir"
  printf '1\n' >"$dir/state/doctor.rc"
  run_check "$dir"
  expect_status "$status" 2 "doctor that does not answer"
  expect_row sandbox "exited 1 and printed" "doctor that does not answer: the row says what it printed"
  no_fire "doctor that does not answer"
}

test_doctor_without_project() {
  local dir=$work/doctor-empty
  prepare "$dir"
  printf '{}\n' >"$dir/state/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "doctor without a project"
  expect_row sandbox "needs all three" "doctor without a project"
  no_fire "doctor without a project"
}

test_locations_unreadable() {
  local dir=$work/locations
  prepare "$dir"
  printf '1\n' >"$dir/state/locations.rc"
  run_check "$dir"
  expect_status "$status" 2 "Scheduler locations that cannot be listed"
  expect_row scheduler "pass --scheduler-region" "Scheduler locations that cannot be listed: the way out"
  no_fire "Scheduler locations that cannot be listed"
}

test_scheduler_not_found() {
  local dir=$work/nosched
  prepare "$dir"
  rm -f "$dir/state/sched.$sched_region.json"
  run_check "$dir"
  expect_status "$status" 2 "an installation with no Scheduler job"
  expect_row scheduler "COORDINATOR'S OWN CONDITION" "an installation with no Scheduler job: the condition K4.2 applies it under"
  expect_row scheduler "renewal is by hand there (TN5)" "an installation with no Scheduler job: what the CLI says instead"
  no_fire "an installation with no Scheduler job"
}

test_scheduler_region_given() {
  local dir=$work/sched-region
  prepare "$dir"
  # With --scheduler-region the check never asks for the locations: the owner has
  # already said where the installation's Scheduler jobs live.
  run_check "$dir" -- --scheduler-region "$sched_region"
  expect_status "$status" 0 "--scheduler-region on the happy path"
  expect_absent "$(cat "$dir/state/logs/gcloud.calls")" "scheduler locations list" "--scheduler-region: the locations are not asked for"
}

test_scheduler_paused() {
  local dir=$work/paused
  prepare "$dir"
  scheduler_doc "$dir/state/sched.$sched_region.json" PAUSED \
    "https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/$job:run" \
    POST "fugaro-scheduler@$project.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  run_check "$dir"
  expect_status "$status" 1 "a paused schedule"
  expect_row scheduler "FAIL" "a paused schedule: the finding"
  expect_row scheduler "quietly stops renewing" "a paused schedule: why it matters"
  no_fire "a paused schedule"
}

test_scheduler_foreign_target() {
  local dir=$work/target
  prepare "$dir"
  scheduler_doc "$dir/state/sched.$sched_region.json" ENABLED \
    "https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/someone-elses-job:run" \
    POST "fugaro-scheduler@$project.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  run_check "$dir"
  expect_status "$status" 1 "a tick that calls another job"
  expect_contains "$err" "does not call the Cloud Run licence job" "a tick that calls another job"
  no_fire "a tick that calls another job"
}

test_scheduler_foreign_account() {
  local dir=$work/sched-sa
  prepare "$dir"
  scheduler_doc "$dir/state/sched.$sched_region.json" ENABLED \
    "https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/$job:run" \
    POST "someone@another-project.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  run_check "$dir"
  expect_status "$status" 1 "a tick authorised as another project's account"
  expect_contains "$err" "account that is not this installation's" "a tick authorised as another project's account"
  no_fire "a tick authorised as another project's account"
}

test_job_missing() {
  local dir=$work/nojob
  prepare "$dir"
  printf '1\n' >"$dir/state/job.rc"
  run_check "$dir"
  expect_status "$status" 2 "a Cloud Run job that is not there"
  expect_row job "COORDINATOR'S OWN CONDITION" "a Cloud Run job that is not there"
  no_fire "a Cloud Run job that is not there"
}

test_job_is_something_else() {
  local dir=$work/otherjob
  prepare "$dir"
  job_doc "$dir/state/job.json" other "workflow" "run --task" \
    "fugaro-licence@$project.iam.gserviceaccount.com" "$fugaro_project" "$project"
  run_check "$dir"
  expect_status "$status" 1 "a job by that name that is not the licence job"
  expect_contains "$err" "is not this installation's licence job" "a job by that name that is not the licence job"
  no_fire "a job by that name that is not the licence job"
}

test_job_foreign_installation() {
  local dir=$work/job-env
  prepare "$dir"
  job_doc "$dir/state/job.json" managed licence "licence renew --job" \
    "fugaro-licence@$project.iam.gserviceaccount.com" "another-install" "$project"
  run_check "$dir"
  expect_status "$status" 1 "a job whose environment names another installation"
  expect_contains "$err" "environment names another installation" "a job whose environment names another installation"
  no_fire "a job whose environment names another installation"
}

test_job_with_another_timeout_is_refused() {
  local dir=$work/job-timeout
  prepare "$dir"
  job_doc "$dir/state/job.json" managed licence "licence renew --job" \
    "fugaro-licence@$project.iam.gserviceaccount.com" "$fugaro_project" "$project" 900 2
  run_check "$dir"
  expect_status "$status" 1 "a licence job with another timeout and retries"
  expect_row job "600 seconds and no retries" "a licence job with another timeout and retries: the row names what K4.2 pins"
  expect_contains "$err" "timeout and retries are not the ones" "a licence job with another timeout and retries"
  no_fire "a licence job with another timeout and retries"
}

test_premise_machine_key() {
  local dir=$work/machine-key
  prepare "$dir"
  status_doc "$dir/state/status.before.json" "2026-12-01T00:00:00Z" "$pre_key" machine active auto
  run_check "$dir"
  expect_status "$status" 2 "an installation whose licence answers from a machine's key"
  expect_row premise "D2137" "a machine's key: the rule the row names"
  no_fire "a machine's key"
}

test_premise_manual_key() {
  local dir=$work/manual
  prepare "$dir"
  status_doc "$dir/state/status.before.json" "2026-12-01T00:00:00Z" "$pre_key" project active manual
  run_check "$dir"
  expect_status "$status" 2 "a manual key"
  expect_row premise "SHORT-LIVED key whose renewal_mode is auto" "a manual key: what the check needs instead"
  no_fire "a manual key"
}

test_premise_no_key() {
  local dir=$work/nokey
  prepare "$dir"
  python3 - "$dir/state/status.before.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc.update({"state": "free", "tier": "", "source": "", "renewal_mode": "", "paid_through": None, "last_check": None})
open(sys.argv[1], "w").write(json.dumps(doc) + "\n")
PY
  run_check "$dir"
  expect_status "$status" 2 "an installation with no project key"
  expect_row premise "fugaro licence set" "an installation with no project key: what installs one"
  no_fire "an installation with no project key"
}

test_fire_fails() {
  local dir=$work/fire
  prepare "$dir"
  printf '1\n' >"$dir/state/fire.rc"
  run_check "$dir"
  expect_status "$status" 2 "a Scheduler fire that fails"
  expect_contains "$err" "no renewal ran" "a Scheduler fire that fails"
}

test_execution_never_appears() {
  local dir=$work/noexec
  prepare "$dir"
  execs_doc "$dir/state/execs.json" -3600 True
  run_check "$dir"
  expect_status "$status" 2 "a fire that started no execution"
  expect_row execution "was created in the" "a fire that started no execution: the row says so"
  expect_row execution "roles/run.invoker" "a fire that started no execution: the grant to look at"
}

test_execution_failed() {
  local dir=$work/exfailed
  prepare "$dir"
  execs_doc "$dir/state/execs.json" 120 False
  run_check "$dir"
  expect_status "$status" 2 "an execution that ended failed"
  expect_row execution "the job itself failed" "an execution that ended failed"
}

test_window_closed() {
  local dir=$work/window
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "checked: the licence server answered that key $pre_key is good" \
    "nothing to do: key $pre_key is paid through 2026-12-01T00:00:00Z, its renewal window opens 2026-11-24T00:00:00Z, and its last check was 2026-11-20T04:23:11Z"
  run_check "$dir"
  expect_status "$status" 2 "a renewal window that is not open"
  expect_row renewed "SHORT-LIVED auto key" "a window that is not open: what the check needs"
  expect_row renewed "its renewal window opens 2026-11-24T00:00:00Z" "a window that is not open: the job's own date"
  # Nothing after the row can be read: the check stops where the premise failed.
  expect_row status "not reached" "a window that is not open: no term was read after it"
}

test_renewed_the_same_key() {
  local dir=$work/samekey
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "renewed: key $pre_key is installed in project $fugaro_project beside the key it replaces (fugaro/licence.json)"
  run_check "$dir"
  expect_status "$status" 1 "a renewal whose successor is the key already held"
  expect_row renewed "FAIL" "a renewal whose successor is the key already held: the finding"
}

test_term_did_not_move() {
  local dir=$work/noterm
  prepare "$dir"
  status_doc "$dir/state/status.after.json" "2026-12-01T00:00:00Z" "$pre_key" project active auto
  run_check "$dir"
  expect_status "$status" 1 "a renewal whose term did not move"
  expect_row status "did not move" "a renewal whose term did not move: the finding"
  expect_contains "$err" "did not move" "a renewal whose term did not move: the refusal says so too"
}

test_unreachable_server() {
  local dir=$work/unreachable
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "note: the check of key $pre_key did not complete, so the key keeps its state and nothing was renewed (only an explicit answer revokes, D2140): dial tcp: connection refused"
  run_check "$dir"
  expect_status "$status" 2 "a job that could not reach the licence server"
  expect_row renewed "route from the job to the licence server did not answer" "a job that could not reach the licence server"
}

test_unknown_job_line() {
  local dir=$work/unknown
  prepare "$dir"
  logs_doc "$dir/state/logs.json" "something this check has never seen"
  run_check "$dir"
  expect_status "$status" 2 "a job line this check does not know"
  expect_row renewed "gcloud logging read" "a job line this check does not know: the way to read it"
  # A line the check cannot name is a line it cannot vouch for, so it does not
  # print it: the row names the command instead.
  expect_absent "$out" "something this check has never seen" "a job line this check does not know: the line is not printed"
}

test_status_unreadable_after() {
  local dir=$work/statusfail
  prepare "$dir"
  printf '1\n' >"$dir/state/status.rc"
  run_check "$dir"
  expect_status "$status" 2 "a licence status that stops answering after the fire"
  expect_row post "exited 1 and printed" "a licence status that stops answering after the fire: the row says what it printed"
}

test_older_execution_line_is_not_read() {
  local dir=$work/older
  prepare "$dir"
  # The execution this fire starts answers with a check and nothing else; the one
  # before it is the execution that renewed the key. Read with no execution
  # filter, the newest terminal line is THAT renewal, and the check would report
  # this fire's renewal from a line another execution wrote.
  logs_exec_doc "$dir/state/logs.json" \
    "fugarolicence-older|renewed: key $successor is installed in project $fugaro_project beside the key it replaces (fugaro/licence.json)" \
    "fugarolicence-abc12|checked: the licence server answered that key $pre_key is good"
  run_check "$dir"
  expect_status "$status" 2 "a line from an execution that ran before this fire"
  expect_row renewed "no sentence this check recognises" "an older execution's renewal line is not this fire's answer"
  # The read names the execution step_execution found, which is what makes the
  # line above unreadable to this run.
  expect_contains "$(cat "$dir/state/logs/gcloud.calls")" \
    'logging read resource.type="cloud_run_job" AND resource.labels.job_name="'"$job"'" AND labels."run.googleapis.com/execution_name"="fugarolicence-abc12"' \
    "the job's lines are read for the execution this fire started"
  assert_scratch_removed "a line from an older execution"
}

# ------------------------------------------------------------- the run itself
t test_help
t test_dry_run
t test_happy_path
t test_unknown_argument
t test_sandbox_missing
t test_belong_refused
t test_production_refused
t test_production_named
t test_agent_session_refused
t test_xtrace_refused
t test_doctor_fails
t test_doctor_without_project
t test_locations_unreadable
t test_scheduler_not_found
t test_scheduler_region_given
t test_scheduler_paused
t test_scheduler_foreign_target
t test_scheduler_foreign_account
t test_job_missing
t test_job_is_something_else
t test_job_foreign_installation
t test_job_with_another_timeout_is_refused
t test_premise_machine_key
t test_premise_manual_key
t test_premise_no_key
t test_fire_fails
t test_execution_never_appears
t test_execution_failed
t test_window_closed
t test_renewed_the_same_key
t test_term_did_not_move
t test_unreachable_server
t test_unknown_job_line
t test_older_execution_line_is_not_read
t test_status_unreadable_after

if [ "$failures" -ne 0 ]; then
  echo "check560_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check560_test.sh: ok"
