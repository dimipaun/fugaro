#!/usr/bin/env bash
# Tests for scripts/live-checks/check561.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project or a licence server, no agent
# runs a live command, and both fakes record every call — with the directory it
# was made in — so a case can assert what the check read, that it fired the
# Scheduler job exactly once, and that a guard stopped the check before it read
# anything.
#
# The fake `gcloud` serves the installation's own shapes out of the case's files:
# the Scheduler locations and job, the Cloud Run job, the executions of that job,
# and the job's own lines out of Cloud Logging — where a case puts the sentence
# the check has to read: the revocation the server answered, the check that did
# not complete, or a key that is still good. The fake `fugaro` serves
# `doctor --json` and the two `licence status --json` reads: the premise and the
# state the installation reads afterwards. No licence key is built here at all —
# the documents carry none — and the key IDs the fixtures use are words a case
# can look for.
#
# Usage: bash scripts/live-checks/check561_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check561_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check561_test.sh

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check561.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK561_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK561_TEST_HEAD:-1600}")" ;;
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

project=chk561-gcp-proj
fugaro_project=chk561-install
region=europe-west2
sched_region=europe-west1
job=fugarolicence
sched_job=fugaro-licence-renew
# The key ID: a word a case can look for. It is not a key — the check reads
# documents that carry none, and the specification calls the ID a support handle.
# The check holds the job's own line against the key the installation holds, so
# the fixture's check record names this same key.
pre_key=chk561-key-old

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check561_test.sh only: every
# answer is served out of $FAKE561_DIR and every call is recorded there with the
# directory it was made in. Nothing here is a real API call.
set -euo pipefail
dir=${FAKE561_DIR:?}
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
# A stand-in for the installed `fugaro`, for scripts/live-checks/check561_test.sh
# only: it answers doctor and the two licence status reads out of $FAKE561_DIR,
# and records every call with the directory it was made in (the check runs it
# inside the sandbox checkout, and a case asserts that). No licence key is read
# or printed here: the documents it serves carry none.
set -euo pipefail
dir=${FAKE561_DIR:?}
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
  if [ -f "$dir/status.first.rc" ] && [ "$n" -eq 1 ]; then
    printf 'fugaro: licence status: the store could not be read\n' >&2
    exit "$(cat "$dir/status.first.rc")"
  fi
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
# status_doc FILE [KEY=VALUE...]: K3.2's `licence status --json` shape, with the
# fields this check reads and the ones a case bends. It carries no key at all,
# which is the whole reason the check reads the licence this way; the key ID in
# its check record is a word a case can look for and not a key. The bare call is
# the premise both parts need: the project's own active key, not revoked, with a
# check record that reached the server. A case overrides one field at a time:
# state=, revoked=, tier= (empty for a key whose claims have gone), source=,
# last_status=, last_key=, last_failures=, last_ok=, last_at=, warning=,
# warning_at= (no check record at all).
status_doc() { # status_doc FILE KEY=VALUE...
  python3 - "$@" <<'PY'
import json, sys
out = sys.argv[1]
doc = {
    "project": "chk561-install",
    "state": "active",
    "tier": "team",
    "seats": 5,
    "paid_through": "2026-12-01T00:00:00Z",
    "kind": "subscription",
    "renewal_mode": "auto",
    "source": "project",
    "revoked": False,
    "last_check": {
        "at": "2026-11-20T04:23:11Z",
        "status": "ok",
        # The key the installation holds is this suite's own $pre_key: the job's
        # lines name that same key, and the check holds the two against each
        # other, so a record about another key is the finding, not the fixture.
        "key_id": "chk561-key-old",
        "failures": 1,
        "last_ok": "2026-11-20T04:23:11Z",
    },
    "warning": None,
}
top = {"project": str, "state": str, "tier": str, "seats": int, "kind": str,
       "renewal_mode": str, "source": str, "revoked": lambda v: v == "true",
       "paid_through": lambda v: v or None}
check = {"at": str, "status": str, "key_id": str, "failures": int, "last_ok": str}
for arg in sys.argv[2:]:
    k, _, v = arg.partition("=")
    if k == "no_check":
        doc["last_check"] = None
    elif k == "warning":
        doc["warning"] = {"windows": [], "message": v}
    elif k.startswith("last_"):
        name = k[len("last_"):]
        name = {"status": "status", "key": "key_id", "at": "at",
                "failures": "failures", "ok": "last_ok"}[name]
        doc["last_check"][name] = check[name](v) if v else (0 if name == "failures" else "")
    elif k in top:
        doc[k] = top[k](v)
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

job_doc() { # job_doc FILE LABEL_FUGARO LABEL_ROLE ARGS SA FUGARO_PROJECT GCP_PROJECT
  python3 - "$@" <<'PY'
import json, sys
out, lf, lr, args, sa, fp, gp = sys.argv[1:8]
doc = {
    "metadata": {"name": "fugarolicence", "labels": {"fugaro": lf, "fugaro_role": lr}},
    "spec": {
        "template": {
            "spec": {
                "template": {
                    "spec": {
                        "serviceAccountName": sa,
                        "timeoutSeconds": "600",
                        "maxRetries": 0,
                        "containers": [{
                            "image": "europe-west2-docker.pkg.dev/p/coordinator/fugaro:latest",
                            "command": ["/usr/local/bin/fugaro"],
                            "args": args.split(),
                            "env": [{"name": "FUGARO_PROJECT", "value": fp}, {"name": "FUGARO_GCP_PROJECT", "value": gp}, {"name": "FUGARO_BUCKET", "value": "chk561-runs"}],
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

# prepare DIR: `--part revoke`'s whole state — a sandbox installation holding
# the project's own active key with a check record that reached the server, a
# Scheduler job that calls the Cloud Run job, an execution the fire makes, the
# job's line saying the licence server answered that the key is revoked, and the
# status the installation reads afterwards: revoked, expired, with the key's own
# claims still readable.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home" "$dir/sandbox"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  doctor_doc "$dir/state/doctor.json" "$fugaro_project" "$project" "$region"
  status_doc "$dir/state/status.before.json"
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=revoked
  scheduler_doc "$dir/state/sched.$sched_region.json" ENABLED \
    "https://run.googleapis.com/v2/projects/$project/locations/$region/jobs/$job:run" \
    POST "fugaro-scheduler@$project.iam.gserviceaccount.com" "https://www.googleapis.com/auth/cloud-platform"
  job_doc "$dir/state/job.json" managed licence "licence renew --job" \
    "fugaro-licence@$project.iam.gserviceaccount.com" "$fugaro_project" "$project"
  printf '[{"locationId":"%s"},{"locationId":"us-central1"}]\n' "$sched_region" >"$dir/state/locations.json"
  execs_doc "$dir/state/execs.json" 120 True
  logs_doc "$dir/state/logs.json" \
    "checked: the licence server answered that key $pre_key is good" \
    "the licence server answered that key $pre_key is revoked: project $fugaro_project runs on Free from now on, nothing has been deleted, and a new key restores everything (\`fugaro licence set\`)"
}

# prepare_unreachable DIR: `--part unreachable`'s whole state — the same
# installation, with the tick's own line saying the check did not complete, and
# the status afterwards reading exactly what it read before except for the check
# record: unreachable, one more failure, and the same last-ok time.
prepare_unreachable() { # dir
  local dir=$1
  prepare "$dir"
  status_doc "$dir/state/status.after.json" last_status=unreachable last_failures=2
  logs_doc "$dir/state/logs.json" \
    "note: the check of key $pre_key did not complete, so the key keeps its state and nothing was renewed (only an explicit answer revokes, D2140): dial tcp 34.1.2.3:443: i/o timeout"
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
    FAKE561_DIR="$dir/state" \
    CHECK561_WAIT="${CHECK561_TEST_WAIT:-3}" CHECK561_POLL=1 CHECK561_LOG_WAIT=2 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check561\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  # CHECK561_TEST_DUMP=1 prints the block a case just read: a case that fails on
  # a row is much easier to read with the whole block in front of it.
  if [ -n "${CHECK561_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
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
  expect_contains "$out" "bash scripts/live-checks/check561.sh --sandbox" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, who runs it, both parts and what it never prints.
  expect_contains "$out" "by YOU" "--help: who runs it"
  expect_contains "$out" "--part revoke" "--help: the revoke part"
  expect_contains "$out" "--part unreachable" "--help: the unreachable part"
  expect_contains "$out" "no licence key and no renewal secret" "--help: what is never printed"
  expect_contains "$out" "is refused unless" "--help: the production guard"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_absent "$out" "MANUAL:" "a dry run: no owner's part is printed, because the owner's part is a step of a real run and a dry run stops at the plan"
  expect_contains "$err" "YOUR PART, before this command: revoke that key" "a dry run: the plan says the part is the owner's"
  expect_contains "$err" "gcloud scheduler jobs run $sched_job" "a dry run: the fire is planned"
  expect_contains "$err" "the revocation the server answered" "a dry run: the answer read is planned"
  no_calls "a dry run"
  no_fire "a dry run"
  assert_scratch_removed "a dry run"
}

test_dry_run_unreachable() {
  local dir=$work/dry-unreachable
  prepare_unreachable "$dir"
  run_check "$dir" -- --part unreachable --dry-run
  expect_status "$status" 0 "a dry run of the unreachable part"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run of the unreachable part: the row"
  expect_absent "$out" "MANUAL:" "a dry run of the unreachable part: no owner's part is printed, because a dry run stops at the plan"
  expect_contains "$err" "YOUR PART, before this command: take the licence job's route" "a dry run: the plan names the block"
  no_calls "a dry run of the unreachable part"
  no_fire "a dry run of the unreachable part"
}

test_unknown_argument() {
  local dir=$work/unknown-arg
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  no_calls "an unknown argument"
}

test_part_unknown() {
  local dir=$work/bad-part
  prepare "$dir"
  run_check "$dir" -- --part nonsense
  expect_status "$status" 2 "a part this check does not know"
  expect_contains "$err" "--part takes revoke or unreachable" "a part this check does not know: the refusal"
  expect_absent "$out" "CHECK 561 RESULT" "a part this check does not know: no block is printed"
  no_calls "a part this check does not know"
}

test_sandbox_missing() {
  local dir=$work/no-sandbox
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
  expect_contains "$err" "refused unless --production" "fugaro-prod without --production: the reason"
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

test_scheduler_region_wrong() {
  local dir=$work/sched-region-bad
  prepare "$dir"
  # A region the job is not in: the check is told where to look and the job is
  # not there, which is a row it could not measure rather than a finding.
  run_check "$dir" -- --scheduler-region us-central1
  expect_status "$status" 2 "--scheduler-region that is the wrong one"
  expect_row scheduler "is not in the location --scheduler-region named" "--scheduler-region that is the wrong one: the row"
  no_fire "--scheduler-region that is the wrong one"
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
  job_doc "$dir/state/job.json" other workflow "run --task" \
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

test_premise_machine_key() {
  local dir=$work/machine-key
  prepare "$dir"
  status_doc "$dir/state/status.before.json" source=machine
  run_check "$dir"
  expect_status "$status" 2 "an installation running on a machine key"
  expect_row premise "fugaro licence set" "an installation running on a machine key: what installs the project's"
  no_fire "an installation running on a machine key"
}

test_premise_already_revoked() {
  local dir=$work/already-revoked
  prepare "$dir"
  status_doc "$dir/state/status.before.json" state=expired revoked=true
  run_check "$dir"
  expect_status "$status" 2 "a key the installation already reads as revoked"
  expect_row premise "already reads its key as revoked" "a key the installation already reads as revoked: the row"
  no_fire "a key the installation already reads as revoked"
}

test_premise_not_serving() {
  local dir=$work/not-serving
  prepare "$dir"
  status_doc "$dir/state/status.before.json" state=expired
  run_check "$dir"
  expect_status "$status" 2 "a key that is not serving"
  expect_row premise "not the good key a revocation is measured against" "a key that is not serving: the row"
  no_fire "a key that is not serving"
}

test_premise_no_check_key() {
  local dir=$work/no-check
  prepare "$dir"
  status_doc "$dir/state/status.before.json" no_check=1
  run_check "$dir"
  expect_status "$status" 2 "a check record that names no key"
  expect_row premise "carries no key_id" "a check record that names no key: the row"
  no_fire "a check record that names no key"
}

test_premise_status_unreadable() {
  local dir=$work/premise-fail
  prepare "$dir"
  printf '1\n' >"$dir/state/status.first.rc"
  run_check "$dir"
  expect_status "$status" 2 "a licence status that does not answer before the fire"
  expect_row premise "exited 1 and printed" "a licence status that does not answer: the row says what it printed"
  no_fire "a licence status that does not answer before the fire"
}

test_fire_fails() {
  local dir=$work/fire
  prepare "$dir"
  printf '1\n' >"$dir/state/fire.rc"
  run_check "$dir"
  expect_status "$status" 2 "a Scheduler fire that fails"
  expect_contains "$err" "the Scheduler job could not be fired" "a Scheduler fire that fails"
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
  local dir=$work/execfailed
  prepare "$dir"
  execs_doc "$dir/state/execs.json" 120 False
  run_check "$dir"
  expect_status "$status" 2 "an execution that ended failed"
  expect_row execution "the job itself failed" "an execution that ended failed: the row"
}

test_answer_key_still_good() {
  local dir=$work/still-good
  prepare "$dir"
  logs_doc "$dir/state/logs.json" "checked: the licence server answered that key $pre_key is good"
  run_check "$dir"
  expect_status "$status" 2 "a key the server still answers is good"
  expect_row answer "the tick DID reach the server" "a key the server still answers is good: the row"
  expect_row answer "portal's operations screens" "a key the server still answers is good: the step the owner has not made"
}

test_answer_key_still_good_unreachable() {
  local dir=$work/still-good-unreachable
  prepare_unreachable "$dir"
  logs_doc "$dir/state/logs.json" "checked: the licence server answered that key $pre_key is good"
  run_check "$dir" -- --part unreachable
  expect_status "$status" 2 "the route out was not taken away"
  expect_row answer "the tick REACHED the server" "the route out was not taken away: the row"
  expect_row answer "Take the licence job's route to the licence server away first" "the route out was not taken away: the step that was not made"
}

test_answer_nothing_to_do() {
  local dir=$work/nothing
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "nothing to do: key $pre_key is paid through 2026-12-01T00:00:00Z, its renewal window opens 2026-11-24T00:00:00Z, and its last check was 2026-11-20T04:23:11Z"
  run_check "$dir"
  expect_status "$status" 2 "a tick that recorded no check"
  expect_row answer "records no new check" "a tick that recorded no check: the row"
  expect_row answer "Wait one check interval" "a tick that recorded no check: what to do"
}

test_answer_unknown_line() {
  local dir=$work/unknown-line
  prepare "$dir"
  logs_doc "$dir/state/logs.json" "something this check has never seen"
  run_check "$dir"
  expect_status "$status" 2 "a job line this check does not know"
  expect_row answer "hold no sentence this check recognises" "a job line this check does not know: the row"
  # A line the check cannot name is a line it cannot vouch for, so it does not
  # print it: the row names the command instead.
  expect_absent "$out" "something this check has never seen" "a job line this check does not know: the line is not printed"
}

test_answer_another_key() {
  local dir=$work/other-key
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "the licence server answered that key chk561-someone-elses-key is revoked: project $fugaro_project runs on Free from now on, nothing has been deleted, and a new key restores everything (\`fugaro licence set\`)"
  run_check "$dir"
  expect_status "$status" 1 "a revocation that names another key"
  expect_row answer "is a row about another licence" "a revocation that names another key: the finding"
  expect_row aborted "named a key this installation does not hold" "a revocation that names another key: what stopped the run"
}

test_answer_offline_key() {
  local dir=$work/offline
  prepare "$dir"
  logs_doc "$dir/state/logs.json" \
    "key $pre_key renews manually (renewal_mode: manual), so the job checks it and never renews it: \`fugaro licence refresh\` fetches the next key when a person asks for it"
  run_check "$dir"
  expect_status "$status" 2 "a key that contacts no licence server"
  expect_row answer "contacts no licence server" "a key that contacts no licence server: the row"
}

test_state_not_recorded() {
  local dir=$work/not-recorded
  prepare "$dir"
  # The job's own line reports the revocation and the installation still reads
  # the key as good: that is K4.1's write missing, not a row about the network.
  status_doc "$dir/state/status.after.json"
  run_check "$dir"
  expect_status "$status" 1 "a revocation the installation never recorded"
  expect_row aborted "did not record the revocation the job reported" "a revocation the installation never recorded: the finding"
}

test_state_not_expired() {
  local dir=$work/not-expired
  prepare "$dir"
  status_doc "$dir/state/status.after.json" state=active revoked=true last_status=revoked
  run_check "$dir"
  expect_status "$status" 1 "a revoked key whose state is not the one it reads"
  expect_row aborted "is not the one a revoked key reads" "a revoked key whose state is not the one it reads: the finding"
}

test_state_claims_gone() {
  local dir=$work/claims-gone
  prepare "$dir"
  # D436's read-only: a revocation records an answer and deletes nothing, so the
  # key's own claims have to stay readable.
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=revoked tier= paid_through=
  run_check "$dir"
  expect_status "$status" 1 "a revocation that took the key's claims with it"
  expect_row aborted "no longer readable" "a revocation that took the key's claims with it: the finding"
}

test_state_record_not_revoked() {
  local dir=$work/record-ok
  prepare "$dir"
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=ok
  run_check "$dir"
  expect_status "$status" 1 "a revoked key whose record says otherwise"
  expect_row aborted "does not carry the revocation" "a revoked key whose record says otherwise: the finding"
}

test_state_key_changed() {
  local dir=$work/key-changed
  prepare "$dir"
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=revoked last_key=chk561-someone-elses-key
  run_check "$dir"
  expect_status "$status" 1 "a record about another key"
  expect_row aborted "names a key other than the one the tick checked" "a record about another key: the finding"
}

test_state_source_moved() {
  local dir=$work/source-moved
  prepare "$dir"
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=revoked source=machine
  run_check "$dir"
  expect_status "$status" 1 "an installation whose licence moved while the tick wrote"
  expect_row aborted "source that is not the project's" "an installation whose licence moved while the tick wrote: the finding"
}

test_unreachable_revoked_by_failure() {
  local dir=$work/revoked-by-failure
  prepare_unreachable "$dir"
  # The exact failure D2140 exists for: a check that never reached the server
  # must never turn into a revocation.
  status_doc "$dir/state/status.after.json" state=expired revoked=true last_status=unreachable last_failures=2
  run_check "$dir" -- --part unreachable
  expect_status "$status" 1 "a key given up on a failed check"
  expect_row state "turns a failed check into a revocation is a key given up on the strength of a network error" "a key given up on a failed check: the finding"
  expect_row aborted "revoked the key on a check that never reached the server" "a key given up on a failed check: what stopped the run"
}

test_unreachable_state_moved() {
  local dir=$work/unreachable-moved
  prepare_unreachable "$dir"
  status_doc "$dir/state/status.after.json" state=expired last_status=unreachable last_failures=2
  run_check "$dir" -- --part unreachable
  expect_status "$status" 1 "a key whose state moved with no answer"
  expect_row state "a state that moved is a lapse decided on a failed check rather than an answer" "a key whose state moved with no answer: the finding"
}

test_unreachable_failure_not_counted() {
  local dir=$work/uncounted
  prepare_unreachable "$dir"
  status_doc "$dir/state/status.after.json" last_status=unreachable last_failures=1
  run_check "$dir" -- --part unreachable
  expect_status "$status" 1 "a failed check the installation did not count"
  expect_row state "left it standing is a failure the installation did not count" "a failed check the installation did not count: the finding"
}

test_unreachable_last_ok_moved() {
  local dir=$work/last-ok-moved
  prepare_unreachable "$dir"
  status_doc "$dir/state/status.after.json" last_status=unreachable last_failures=2 last_ok=2026-11-21T04:23:11Z
  run_check "$dir" -- --part unreachable
  expect_status "$status" 1 "a failed check that moved the last-ok time"
  expect_row state "makes an installation whose route is broken read as freshly checked" "a failed check that moved the last-ok time: the finding"
}

test_unreachable_record_not_unreachable() {
  local dir=$work/record-wrong
  prepare_unreachable "$dir"
  status_doc "$dir/state/status.after.json" last_status=revoked last_failures=2
  run_check "$dir" -- --part unreachable
  expect_status "$status" 1 "a failed check recorded as an answer"
  expect_row state "a record that says anything else is a failed check recorded as an answer" "a failed check recorded as an answer: the finding"
}

test_warning_window_reported() {
  local dir=$work/warning
  prepare_unreachable "$dir"
  status_doc "$dir/state/status.after.json" last_status=unreachable last_failures=31 \
    warning="cannot verify revocation: the licence server has not answered a check of this installation for 30 days"
  run_check "$dir" -- --part unreachable
  expect_status "$status" 0 "an open warning window"
  expect_contains "$out" "FACT warning: INFO" "an open warning window: the row"
  expect_contains "$out" "cannot verify revocation" "an open warning window: the sentence the installation carries"
}

test_status_unreadable_after() {
  local dir=$work/statusfail
  prepare "$dir"
  printf '1\n' >"$dir/state/status.rc"
  run_check "$dir"
  expect_status "$status" 2 "a licence status that stops answering after the fire"
  expect_row post "exited 1 and printed" "a licence status that stops answering after the fire: the row says what it printed"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  # The premise, the owner's own part, the fire and its execution, the job's own
  # line and the installation's own answer afterwards: each is its own row.
  expect_row premise "reads revoked false" "the happy path: the revocation has not landed yet"
  expect_row manual "the revocation itself, made on the portal's operations screens" "the happy path: the owner's part"
  expect_row execution "Completed condition True" "the happy path: the execution finished"
  expect_row answer "the licence server answered that key $pre_key is revoked" "the happy path: the job's own line"
  expect_row answer "nothing has been deleted" "the happy path: the sentence's own words"
  expect_row state "revoked true" "the happy path: the installation recorded it"
  expect_row state "D436's read-only" "the happy path: the key's claims are still readable"
  expect_row restore "It deleted nothing" "the happy path: what is left behind"
  # The one call this check makes is the tick's own fire, and it is made once.
  expect_contains "$(cat "$dir/state/logs/fire")" "gcloud scheduler jobs run $sched_job --location $sched_region" "the happy path: the Scheduler job's own fire"
  # `tr -d ' '` because BSD `wc` pads its count, so `= 1` would read "      1"
  # there and this case would fail on the machine the check is run from.
  [ "$(wc -l <"$dir/state/logs/fire" | tr -d ' ')" = 1 ] || fail "the happy path: the Scheduler job was fired $(wc -l <"$dir/state/logs/fire" | tr -d ' ') times"
  expect_contains "$out" "MANUAL: revoke key $pre_key" "the happy path: the owner's own part is printed"
  no_object_reads "the happy path"
  assert_scratch_removed "the happy path"
}

test_happy_path_unreachable() {
  local dir=$work/happy-unreachable
  prepare_unreachable "$dir"
  run_check "$dir" -- --part unreachable
  expect_status "$status" 0 "the unreachable part's happy path"
  expect_row manual "the route out" "the unreachable part: the owner's part"
  expect_row answer "did not complete" "the unreachable part: the job's own line"
  expect_row answer "unreachable server never revokes" "the unreachable part: what the row settles"
  expect_row state "revoked false" "the unreachable part: the key was not revoked"
  expect_row state "the same state as before it" "the unreachable part: nothing moved"
  expect_row state "up from 1 to 2" "the unreachable part: the failure was counted"
  expect_row restore "the key was never touched" "the unreachable part: what is left behind"
  no_object_reads "the unreachable part"
  assert_scratch_removed "the unreachable part"
}


test_older_execution_answer_is_not_read() {
  local dir=$work/older
  prepare "$dir"
  # The revocation the job answered yesterday, and the check this fire's own
  # execution made. Read with no execution filter, the newest terminal line is
  # the OLD one, and this run would report a revocation that landed before it
  # ran as this fire's answer.
  logs_exec_doc "$dir/state/logs.json" \
    "fugarolicence-older|the licence server answered that key $pre_key is revoked: project $fugaro_project runs on Free from now on, nothing has been deleted, and a new key restores everything (\`fugaro licence set\`)" \
    "fugarolicence-abc12|checked: the licence server answered that key $pre_key is good"
  run_check "$dir"
  expect_status "$status" 2 "an answer from an execution that ran before this fire"
  # The fire's own execution answered 'checked:', so that is the row this run
  # reads: the tick DID reach the server and the key is still good.
  expect_row answer "the tick DID reach the server" "an older execution's answer is not this fire's"
  # The read names the execution step_execution found, which is what makes the
  # line above unreadable to this run.
  expect_contains "$(cat "$dir/state/logs/gcloud.calls")" \
    'logging read resource.type="cloud_run_job" AND resource.labels.job_name="'"$job"'" AND labels."run.googleapis.com/execution_name"="fugarolicence-abc12"' \
    "the job's lines are read for the execution this fire started"
  assert_scratch_removed "an answer from an older execution"
}

# ------------------------------------------------------------- the run itself
t test_help
t test_dry_run
t test_dry_run_unreachable
t test_unknown_argument
t test_part_unknown
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
t test_scheduler_region_wrong
t test_scheduler_paused
t test_scheduler_foreign_target
t test_scheduler_foreign_account
t test_job_missing
t test_job_is_something_else
t test_job_foreign_installation
t test_premise_machine_key
t test_premise_already_revoked
t test_premise_not_serving
t test_premise_no_check_key
t test_premise_status_unreadable
t test_fire_fails
t test_execution_never_appears
t test_execution_failed
t test_happy_path
t test_happy_path_unreachable
t test_answer_key_still_good
t test_answer_key_still_good_unreachable
t test_answer_nothing_to_do
t test_answer_unknown_line
t test_older_execution_answer_is_not_read
t test_answer_another_key
t test_answer_offline_key
t test_state_not_recorded
t test_state_not_expired
t test_state_claims_gone
t test_state_record_not_revoked
t test_state_key_changed
t test_state_source_moved
t test_unreachable_revoked_by_failure
t test_unreachable_state_moved
t test_unreachable_failure_not_counted
t test_unreachable_last_ok_moved
t test_unreachable_record_not_unreachable
t test_warning_window_reported
t test_status_unreadable_after

echo
echo "$test_index case(s) run"
if [ "$failures" -eq 0 ]; then
  echo "check561_test.sh: ok"
else
  echo "check561_test.sh: $failures failure(s)"
  exit 1
fi
