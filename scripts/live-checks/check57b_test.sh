#!/usr/bin/env bash
# Tests for scripts/live-checks/check57b.sh, against a fake gcloud, a fake
# fugaro and fixture files on PATH: nothing here reaches the network, there is
# no cloud project, no credential, and no step of the live check is run.
#
# The fake gcloud answers every read the check makes -- the reaper role, the
# Cloud Run job, the Scheduler job, the execution, the log and the Batch jobs --
# out of files this test writes, and records every call verbatim, so a case says
# what the check read and what it never touched. The fake fugaro does the same
# for the local dry pass, and its only answered command is `reaper --dry-run`:
# a case that made the check reach for `setup` would fail here, which is the
# point -- the provisioning step is printed for the owner and never run.
#
# Two fixture files the check reads out of its own tree (the reaper role's
# permissions and the Scheduler location for us-east5) are written per case, and
# the script under test is COPIED into that fixture tree: the real checkout's
# iam.tf must not decide what this suite reads, or a case could not bend it.
#
# Usage: bash scripts/live-checks/check57b_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check57b.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK57B_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

# The fake tools' knobs are exported once here, empty: a prefix assignment on a
# FUNCTION call (FAKE57B_LOG=sweep run57b) is in the function's scope but is not
# exported to the tools it goes on to run, so a knob written that way would
# never reach the fake and every case would silently read the happy path.
export FAKE57B_ROLE="" FAKE57B_ROLE_PERMS="" FAKE57B_JOB="" FAKE57B_JOB_ACCOUNT=""
export FAKE57B_SCHED="" FAKE57B_SCHED_CRON="" FAKE57B_SCHED_ACCOUNT="" FAKE57B_RUN=""
export FAKE57B_EXECUTION="" FAKE57B_LOG="" FAKE57B_JOBS="" FAKE57B_SUBMIT=""
export FAKE57B_CREATED="" FAKE57B_FUGARO=""
export FAKE57B_GPU_QUOTA="" FAKE57B_MACHINE_TYPE="" FAKE57B_STATE=""

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... last run: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2 — " "$3: the row reads $2"
}
expect_no_cloud() { # what
  local g f
  g=$(cat "$dir/state/gcloud.calls" 2>/dev/null || true)
  f=$(cat "$dir/state/fugaro.calls" 2>/dev/null || true)
  [ -z "$g" ] || fail "$1: it ran gcloud, which it must not have: $(printf '%s' "$g" | head -c 300)"
  [ -z "$f" ] || fail "$1: it ran fugaro, which it must not have: $(printf '%s' "$f" | head -c 300)"
}

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(owner checkout tree belong role reaper-job scheduler invocation report local-pass stranded restore)

# The report the pass prints, as the check's own parse expects it. The three
# deadlines are the installation's own on this tree: 24h of MaxRunTime plus the
# queue slack, plus the reap margin and the run budget for the max age.
HEADLINE='reaped projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, run budget 24h17m0s, max age 48h19m0s'

# ---------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
# A fake gcloud for check57b_test.sh: it answers the reads and the one write the
# check makes, out of $FAKE57B_DIR, and reaches nothing. Every call is appended
# to $FAKE57B_DIR/gcloud.calls, argv verbatim, so a case can say what the check
# read -- and the dry-run case can say it read nothing at all.
import json
import os
import shutil
import sys
import datetime

state = os.environ["FAKE57B_DIR"]
args = sys.argv[1:]
with open(os.path.join(state, "gcloud.calls"), "a", encoding="utf-8") as fh:
    fh.write("gcloud " + " ".join(args) + "\n")

NOW = datetime.datetime.now(datetime.timezone.utc)


def ago(**kw):
    return (NOW - datetime.timedelta(**kw)).strftime("%Y-%m-%dT%H:%M:%SZ")

PROJECT = "fugaro-dev"
REGION = "us-east5"
REGION_ZONES = ["us-east5-a", "us-east5-b", "us-east5-c"]
ACCOUNT = "fugaro-reaper@%s.iam.gserviceaccount.com" % PROJECT
RUN_URI = ("https://us-east5-run.googleapis.com/apis/run.googleapis.com/v1/"
           "namespaces/%s/jobs/fugaroreap:run" % PROJECT)
HEADLINE = ("reaped projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, "
            "run budget 24h17m0s, max age 48h19m0s")

# The whole VM each machine type this suite's check may name carries, as the
# Batch API checks it at submit. e2-standard-4 is the type the check used to
# send an impossible share against -- the owner's run of 2026-11-14 was refused
# with 'machine_type "e2-standard-4" cannot satisfy compute_resource
# cpu_milli:64000 memory_mib:1048576' -- so this fake refuses that body in the
# API's own words rather than accepting whatever a case writes.
MACHINE_CAPACITY = {"e2-standard-4": (4, 16384), "g4-standard-48": (48, 184320)}


def env(name, default=""):
    return os.environ.get(name) or default


def die(msg):
    print("ERROR: " + msg, file=sys.stderr)
    sys.exit(1)


def flag(name, default=None):
    if name in args:
        return args[args.index(name) + 1]
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


if args[:3] == ["iam", "roles", "describe"]:
    if env("FAKE57B_ROLE") == "missing":
        die("NOT_FOUND: The role named 'fugaroReaper' was not found.")
    perms = env("FAKE57B_ROLE_PERMS",
                "batch.jobs.list,batch.jobs.get,batch.jobs.delete,"
                "compute.instances.list,compute.instances.delete").split(",")
    print(json.dumps({
        "name": "projects/%s/roles/fugaroReaper" % PROJECT,
        "title": "Fugaro Batch reaper",
        "permissions": [p for p in perms if p],
    }))
elif args[:3] == ["run", "jobs", "describe"]:
    if env("FAKE57B_JOB") == "missing":
        die("NOT_FOUND: The job 'fugaroreap' was not found.")
    job_env = [{"name": "FUGARO_GCP_PROJECT", "value": PROJECT},
               {"name": "FUGARO_REGION", "value": "us-east5"},
               {"name": "FUGARO_REAPER_JOB", "value": "1"}]
    if env("FAKE57B_JOB") == "no-marker":
        job_env = job_env[:2]
    print(json.dumps({
        "labels": {"fugaro": "managed"},
        "spec": {"template": {"spec": {"template": {"spec": {
            "serviceAccountName": env("FAKE57B_JOB_ACCOUNT", ACCOUNT),
            "containers": [{"args": ["reaper"], "env": job_env}],
        }}}}},
    }))
elif args[:3] == ["scheduler", "jobs", "describe"]:
    if env("FAKE57B_SCHED") == "missing":
        die("NOT_FOUND: The job 'fugaro-reaper-sweep' was not found.")
    print(json.dumps({
        "schedule": env("FAKE57B_SCHED_CRON", "*/15 * * * *"),
        "state": "ENABLED",
        "httpTarget": {
            "uri": RUN_URI,
            "oauthToken": {"serviceAccountEmail": env("FAKE57B_SCHED_ACCOUNT", ACCOUNT)},
        },
    }))
elif args[:3] == ["scheduler", "jobs", "run"]:
    if env("FAKE57B_RUN") == "fails":
        die("ERROR: (gcloud.scheduler.jobs.run) PERMISSION_DENIED")
    open(os.path.join(state, "fired"), "w").write("yes\n")
elif args[:4] == ["run", "jobs", "executions", "list"]:
    names = [{"metadata": {"name": "fugaroreap-000001-abc"}}]
    # A fired schedule makes a NEW execution: the check judges the invocation by
    # the execution that was not there before, not by any execution at all.
    if os.path.exists(os.path.join(state, "fired")) and env("FAKE57B_RUN") != "no-execution":
        names.insert(0, {"metadata": {"name": "fugaroreap-000002-def"}})
    print(json.dumps(names))
elif args[:4] == ["run", "jobs", "executions", "describe"]:
    conds = [{"type": "Completed", "status": "True"}]
    if env("FAKE57B_EXECUTION") == "running":
        conds = [{"type": "Completed", "status": "False"}]
    print(json.dumps({"status": {"conditions": conds, "succeededCount": 1, "failedCount": 0}}))
elif args[:2] == ["logging", "read"]:
    mode = env("FAKE57B_LOG", "report")
    if mode == "empty":
        print("[]")
    elif mode == "garbage":
        print(json.dumps([{"textPayload": "hello, this is not a pass"},
                          {"textPayload": "and neither is this"}]))
    else:
        lines = [HEADLINE, "nothing to do"]
        if mode == "sweep":
            lines.append("deleted job fugaro-chk57b-20261113t1200 (queued for 1450 minutes "
                         "and never left QUEUED, past the queue deadline of 24h2m0s)")
        if mode == "stranger":
            lines.append("deleted job someone-elses-job (queued for 1450 minutes and never "
                         "left QUEUED, past the queue deadline of 24h2m0s)")
        print(json.dumps([{"textPayload": "\n".join(lines)}]))
elif args[:3] == ["compute", "regions", "describe"]:
    # The region's own resource, which is where the check reads the two facts
    # that decide whether stranding a job on a GPU machine type is free. The
    # quota list carries a CPU entry that is above zero in every case, so a case
    # can say the check looked at the GPU quotas and not at quotas at large.
    mode = env("FAKE57B_GPU_QUOTA", "none")
    if mode == "unread":
        die("PERMISSION_DENIED: Required 'compute.regions.get' permission for project %s." % PROJECT)
    gpu = 1.0 if mode == "granted" else 0.0
    print(json.dumps({
        "name": REGION,
        "quotas": [
            {"metric": "CPUS", "limit": 200.0, "usage": 4.0},
            {"metric": "NVIDIA_RTX_PRO_6000_GPUS", "limit": gpu, "usage": 0.0},
            {"metric": "GPUS_ALL_REGIONS", "limit": gpu, "usage": 0.0},
        ],
        "zones": ["https://www.googleapis.com/compute/v1/projects/%s/zones/%s" % (PROJECT, z)
                  for z in REGION_ZONES],
    }))
elif args[:3] == ["compute", "machine-types", "list"]:
    mode = env("FAKE57B_MACHINE_TYPE", "present")
    if mode == "unread":
        die("PERMISSION_DENIED: Required 'compute.machineTypes.list' permission for project %s." % PROJECT)
    if mode == "present" and "name=" + env("FAKE57B_TYPE", "g4-standard-48") in (flag("--filter", "") or ""):
        # The type is listed in the zones the check asked about, which is what
        # the real command answers for a type every zone of the region has.
        for z in (flag("--zones", "") or "").split(","):
            if z:
                print(z)
elif args[:3] == ["batch", "jobs", "list"]:
    mode = env("FAKE57B_JOBS", "none")
    if mode == "none":
        print("[]")
    else:
        # Relative to now, not to a date: a job inside the queue deadline and one
        # past it are read against the clock the check itself reads, so this
        # suite cannot go stale.
        created = {"fresh": ago(hours=1), "old": ago(days=3)}.get(mode, "not a time")
        print(json.dumps([{"name": "projects/%s/locations/us-east5/jobs/fugaro-chk57b-20261114t1300"
                                   % PROJECT,
                           "status": {"state": env("FAKE57B_STATE", "QUEUED")},
                           "createTime": created}]))
elif args[:3] == ["batch", "jobs", "submit"]:
    if env("FAKE57B_SUBMIT") == "fails":
        die("INVALID_ARGUMENT: the request is refused")
    cfg = flag("--config")
    body = json.load(open(cfg, encoding="utf-8"))
    machine = (((body.get("allocationPolicy") or {}).get("instances") or [{}])[0]
               .get("policy") or {}).get("machineType")
    res = (((body.get("taskGroups") or [{}])[0].get("taskSpec") or {})
           .get("computeResource") or {})
    if machine not in MACHINE_CAPACITY:
        die("INVALID_ARGUMENT: machine_type field is invalid. machine_type %r is not "
            "one this suite knows a capacity for" % machine)
    vcpu, mib = MACHINE_CAPACITY[machine]
    if int(res.get("cpuMilli", 0)) > vcpu * 1000 or int(res.get("memoryMib", 0)) > mib:
        die('INVALID_ARGUMENT: machine_type field is invalid. machine_type "%s" cannot '
            'satisfy compute_resource cpu_milli:%s memory_mib:%s.'
            % (machine, res.get("cpuMilli"), res.get("memoryMib")))
    shutil.copyfile(cfg, os.path.join(state, "submitted.json"))
elif args[:3] == ["batch", "jobs", "describe"]:
    # Two reads of the same job, told apart by the field the check asked for:
    # the creation time the sweep is measured from, and the state the check
    # requires to be QUEUED before it stands on the job at all.
    if "status.state" in " ".join(args):
        print(env("FAKE57B_STATE", "QUEUED"))
    else:
        print(env("FAKE57B_CREATED", ago(hours=1)))
else:
    die("this test's fake gcloud has no answer for: %s" % " ".join(args))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check57b_test.sh. `reaper --dry-run` is the only command it
# answers; anything else -- `setup` above all -- is a failure here, because the
# check must print the provisioning step for the owner and never run it.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE57B_DIR/fugaro.calls"
case "$*" in
"reaper --dry-run --project fugaro --gcp-project fugaro-dev")
  case "${FAKE57B_FUGARO:-ok}" in
  refuse)
    echo "fugaro: this checkout's project has no batch: block" >&2
    exit 1
    ;;
  silent) : ;;
  disagree)
    echo "would reap projects/fugaro-dev/locations/us-east5: queue deadline 25h2m0s, run budget 25h17m0s, max age 50h19m0s"
    ;;
  *)
    echo "would reap projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, run budget 24h17m0s, max age 48h19m0s"
    echo "nothing to do"
    ;;
  esac
  ;;
*)
  echo "fake fugaro: this check must never run '$*'" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The two fixture files the check reads out of the tree it lives in. Both are
# per case: the permission list is the authority the live role is judged against
# and the Scheduler map is where the job's location comes from, so a case that
# bends either must bend the file, not a variable.
write_tree() { # dir [IAM] [NAMES]
  local dir=$1 iam=${2:-default} names=${3:-default}
  mkdir -p "$dir/tree/scripts/live-checks" "$dir/tree/deploy/terraform/gcp/modules/compute" \
    "$dir/tree/internal/backend/gcp"
  cp "$script" "$dir/tree/scripts/live-checks/check57b.sh"
  case "$iam" in
  none)
    rm -f "$dir/tree/deploy/terraform/gcp/modules/compute/iam.tf"
    ;;
  squeezed)
    # The module's list with a permission the reaper must not have: the live
    # role is the five, so this fixture is what makes the tree the authority.
    cat >"$dir/tree/deploy/terraform/gcp/modules/compute/iam.tf" <<'TF'
resource "google_project_iam_custom_role" "reaper" {
  role_id = "fugaroReaper"
  permissions = [
    "batch.jobs.list",
    "batch.jobs.get",
    "batch.jobs.delete",
    "compute.instances.get",
    "compute.instances.list",
    "compute.instances.delete",
  ]
}
TF
    ;;
  *)
    cat >"$dir/tree/deploy/terraform/gcp/modules/compute/iam.tf" <<'TF'
resource "google_project_iam_custom_role" "reaper" {
  role_id     = "fugaroReaper"
  title       = "Fugaro Batch reaper"
  permissions = [
    "batch.jobs.list",
    "batch.jobs.get",
    "batch.jobs.delete",
    "compute.instances.list",
    "compute.instances.delete",
  ]
}
TF
    ;;
  esac
  case "$names" in
  unserved)
    cat >"$dir/tree/internal/backend/gcp/names.go" <<'GO'
package gcp

var SchedulerRegions = []string{
	"us-east1", "us-east4", "us-central1",
}
GO
    ;;
  *)
    cat >"$dir/tree/internal/backend/gcp/names.go" <<'GO'
package gcp

var SchedulerRegions = []string{
	"us-east1", "us-east4", "us-central1",
}

var nearestSchedulerRegion = map[string]string{
	"us-east5": "us-east4",
}
GO
    ;;
  esac
}

write_checkout() { # dir [ORIGIN]
  local dir=$1 origin=${2:-git@github.com:edgeappinc/fugarosandbox.git}
  mkdir -p "$dir/checkout"
  git -C "$dir/checkout" init -q
  if [ "$origin" = none ]; then
    git -C "$dir/checkout" remote remove origin 2>/dev/null || true
  else
    git -C "$dir/checkout" remote remove origin 2>/dev/null || true
    git -C "$dir/checkout" remote add origin "$origin"
  fi
}

# setup_case NAME [IAM] [NAMES] [ORIGIN] — a case's own directory, fakes, fixture
# tree and fixture checkout.
setup_case() { # NAME [IAM] [NAMES] [ORIGIN]
  dir="$work/$1"
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home"
  write_fakes "$dir"
  : >"$dir/state/gcloud.calls"
  : >"$dir/state/fugaro.calls"
  write_tree "$dir" "${2:-default}" "${3:-default}"
  write_checkout "$dir" "${4:-git@github.com:edgeappinc/fugarosandbox.git}"
}

# run57b [ARGS...] — runs the check on the case at hand, from the case's
# fixture checkout. Sets $status, $out and $dir/err. RUN57B_OWNER=no leaves the
# owner marker unexported; RUN57B_ENV adds "<name>=<value>" entries to the
# environment; RUN57B_DIR runs the check from somewhere else.
RUN57B_OWNER=yes
RUN57B_ENV=""
RUN57B_DIR=""
run57b_exec() { # [ARGS...]
  cd "${RUN57B_DIR:-$dir/checkout}" || exit 1
  local extra=()
  [ -n "$RUN57B_ENV" ] && extra=("$RUN57B_ENV")
  if [ "$RUN57B_OWNER" = yes ]; then
    extra+=(CHK57B_OWNER_RUN=yes)
  fi
  exec env TMPDIR="$dir/tmp" FAKE57B_DIR="$dir/state" HOME="$dir/home" \
    "${extra[@]}" PATH="$dir/bin:$PATH" bash "$dir/tree/scripts/live-checks/check57b.sh" "$@"
}
run57b() {
  status=0
  (run57b_exec "$@") >"$dir/out" 2>"$dir/err" || status=$?
  RUN57B_OWNER=yes
  RUN57B_ENV=""
  RUN57B_DIR=""
  out=$(cat "$dir/out")
}

expect_block_keys() { # what
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key: " "$1: the block carries the $key row"
  done
  expect_contains "$out" "MANUAL: " "$1: the block carries the manual line"
  expect_contains "$out" "CHECK 57B RESULT: " "$1: and the verdict"
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^check57b.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | tail -n 1
}
expect_scratch_gone() { # what
  local s
  s=$(scratch_of "$dir")
  [ -n "$s" ] || fail "$1: the run never announced a scratch directory"
  [ ! -e "$s" ] || fail "$1: the scratch directory $s is still there"
}

# ------------------------------------------------------------------- the cases
test_happy_path_creates_the_stranded_job() {
  setup_case happy
  run57b
  expect_status "$status" 0 "happy path"
  expect_block_keys "happy path"
  expect_fact owner PASS "happy path"
  expect_fact checkout PASS "happy path"
  expect_fact tree PASS "happy path"
  expect_fact belong PASS "happy path"
  expect_fact role PASS "happy path"
  expect_contains "$out" "batch.jobs.delete batch.jobs.get batch.jobs.list compute.instances.delete compute.instances.list" \
    "happy path: the five permissions, read out of the tree and matched against the live role, in the block"
  expect_fact reaper-job PASS "happy path"
  expect_contains "$out" "fugaro-reaper@fugaro-dev.iam.gserviceaccount.com" "happy path: the reaper account the job runs as"
  expect_fact scheduler PASS "happy path"
  expect_contains "$out" "lives in us-east4" "happy path: the Scheduler location read out of the tree's map"
  expect_contains "$out" "*/15 * * * *" "happy path: the cadence"
  expect_fact invocation PASS "happy path"
  expect_contains "$out" "fugaroreap-000002-def" "happy path: the execution the fired schedule made, by name"
  expect_contains "$out" "completed=True succeeded=1 failed=0" "happy path: and the reading that says it ended"
  expect_fact report PASS "happy path"
  expect_contains "$out" "$HEADLINE" "happy path: the pass's own headline, verbatim"
  expect_contains "$out" "queue deadline 24h2m0s" "happy path: the deadline every judgement below is made against"
  expect_fact local-pass PASS "happy path"
  expect_contains "$out" "would reap projects/fugaro-dev/locations/us-east5" "happy path: the local dry run's own headline"
  expect_fact stranded INFO "happy path"
  expect_contains "$out" "so this run created one: fugaro-chk57b-" "happy path: the job this run stranded, named"
  expect_contains "$out" "g4-standard-48" "happy path: and the machine type whose GPU quota keeps it out of QUEUED"
  expect_contains "$out" "2000m/4096MiB share that fits it" "happy path: which the request's own share fits"
  expect_contains "$out" "run this script again after then" "happy path: the row says what a later run will show instead"
  expect_fact restore PASS "happy path"
  expect_contains "$out" "is LEFT IN PLACE on purpose" "happy path: the restore row names the job it left"
  expect_contains "$out" "gcloud batch jobs delete fugaro-chk57b-" "happy path: and the one command that removes it"
  expect_contains "$out" "CHECK 57B RESULT: PASS" "happy path: the verdict"
  # The subject of the sweep is this check's own job, and the pass attributes it
  # to a run: the installation's marker and a well-formed fugaro_run value sit
  # beside the check's own label in BOTH places the reaper reads labels from.
  local stranded_reading
  stranded_reading=$(python3 - "$dir/state/submitted.json" <<'PY'
import json, re, sys
d = json.load(open(sys.argv[1]))
top, alloc = d.get("labels") or {}, (d.get("allocationPolicy") or {}).get("labels") or {}
bad = []
for where, labels in (("labels", top), ("allocationPolicy.labels", alloc)):
    for k, v in {"fugaro": "managed", "fugaro_chk": "57b"}.items():
        if labels.get(k) != v:
            bad.append("%s.%s is %r" % (where, k, labels.get(k)))
    if not re.fullmatch(r"[a-z0-9_-]{1,63}", labels.get("fugaro_run", "")):
        bad.append("%s.fugaro_run is %r" % (where, labels.get("fugaro_run")))
if top.get("fugaro_run") != alloc.get("fugaro_run"):
    bad.append("the two fugaro_run values differ, so the reaper cannot attribute the job to a run")
# The job the check strands must be one the API ACCEPTS and still cannot
# schedule: it names the accelerator-optimized type whose GPU is attached with
# the VM and whose quota this project has never been granted, and its share is
# well inside that VM -- so the shape the check used to send (a share no machine
# type can hold) is a failure here, and the fake's submit refuses it as the API
# does.
machine = (((d.get("allocationPolicy") or {}).get("instances") or [{}])[0]
           .get("policy") or {}).get("machineType")
res = (d.get("taskGroups") or [{}])[0].get("taskSpec", {}).get("computeResource", {})
if machine != "g4-standard-48":
    bad.append("the machine type is %r, not the accelerator-optimized g4-standard-48 whose "
               "GPU quota is what keeps the job in QUEUED" % machine)
if int(res.get("cpuMilli", 0)) > 48000 or int(res.get("memoryMib", 0)) > 184320:
    bad.append("the requested share (%s) is one no g4-standard-48 could hold, which is the "
               "request the Batch API refuses at submit" % res)
print("OK" if not bad else "BAD " + "; ".join(bad))
PY
)
  [ "$stranded_reading" = OK ] || fail "happy path: the stranded job's request is $stranded_reading"
  # The job it strands is one, and the run it made is read back, not assumed.
  expect_contains "$(cat "$dir/state/gcloud.calls")" "batch jobs submit fugaro-chk57b-" "happy path: the submit, recorded"
  expect_contains "$(cat "$dir/state/gcloud.calls")" "scheduler jobs run fugaro-reaper-sweep" "happy path: the schedule fired by hand"
  expect_contains "$(cat "$dir/state/fugaro.calls")" "reaper --dry-run --project fugaro --gcp-project fugaro-dev" \
    "happy path: the local dry pass"
  # The provisioning step is the owner's: nothing in the run reached for it.
  expect_absent "$(cat "$dir/state/fugaro.calls")" "setup" "happy path: fugaro setup is never run by this check"
  # The only row that may name those installations is the guard's own, which
  # names them to say it refuses them.
  expect_absent "$(printf '%s\n' "$out" | grep -v '^FACT belong:')" "belong" "happy path: no other row names the installation this check must never touch"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "belong" "happy path: and no command names it either"
  expect_scratch_gone "happy path"
}

test_a_later_run_reads_the_sweep() {
  setup_case later-run
  FAKE57B_LOG=sweep run57b
  FAKE57B_LOG=""
  expect_status "$status" 0 "later run"
  expect_fact stranded PASS "later run"
  expect_contains "$out" "deleted job fugaro-chk57b-20261113t1200" "later run: the reaper's own line, verbatim"
  expect_contains "$out" "queued for 1450 minutes" "later run: with the reason it was deleted for"
  expect_contains "$out" "CHECK 57B RESULT: PASS" "later run: the verdict"
  expect_scratch_gone "later run"
}

test_a_job_past_its_deadline_that_was_not_swept_is_a_fail() {
  setup_case overdue
  FAKE57B_JOBS=old run57b
  FAKE57B_JOBS=""
  expect_status "$status" 1 "overdue job"
  expect_fact stranded FAIL "overdue job"
  expect_contains "$out" "still QUEUED past the queue deadline" "overdue job: the finding, in the reaper's own terms"
  expect_contains "$out" "do not adjust this check to it" "overdue job: and it says not to bend the check"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "overdue job: the verdict"
}

# The three facts the shape stands on, each read out of the live project before
# anything is submitted, and each one able to stop the run: a project that holds
# GPU quota in the region can be granted the VM the job names, and a machine type
# no zone of the region offers makes a job that fails rather than one that stays
# queued. Both cases end with no job: the row that says 'bills nothing' is only
# ever written about a job that cannot run.
test_gpu_quota_above_zero_stops_before_submitting() {
  setup_case quota-granted
  FAKE57B_GPU_QUOTA=granted run57b
  FAKE57B_GPU_QUOTA=""
  expect_status "$status" 1 "GPU quota held"
  expect_fact stranded FAIL "GPU quota held"
  expect_contains "$out" "this project holds GPU quota in us-east5 (NVIDIA_RTX_PRO_6000_GPUS limit 1.0, GPUS_ALL_REGIONS limit 1.0)" \
    "GPU quota held: the quota the region's own resource holds, named"
  # The CPU quota is above zero in every case, so a guard that refused on any
  # quota at all would pass the row above and be wrong about why.
  expect_absent "$out" "CPUS limit" "GPU quota held: the CPU quota is not what the guard read"
  expect_contains "$out" "Nothing was submitted" "GPU quota held: and that no job was made"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "batch jobs submit" "GPU quota held: no job was submitted"
  [ -e "$dir/state/submitted.json" ] && fail "GPU quota held: a request was written where none may be"
  expect_contains "$out" "the owner's run of 2026-11-14" "GPU quota held: the row names the shape to use instead, with the refusal that made it that"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "GPU quota held: the verdict"
  return 0
}

test_a_machine_type_no_zone_offers_stops_before_submitting() {
  setup_case type-absent
  FAKE57B_MACHINE_TYPE=absent run57b
  FAKE57B_MACHINE_TYPE=""
  expect_status "$status" 1 "type not offered"
  expect_fact stranded FAIL "type not offered"
  expect_contains "$out" "no zone of us-east5 (us-east5-a,us-east5-b,us-east5-c) lists the machine type g4-standard-48" \
    "type not offered: the zones that were asked about, named"
  expect_contains "$out" "Nothing was submitted" "type not offered: and that no job was made"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "batch jobs submit" "type not offered: no job was submitted"
  [ -e "$dir/state/submitted.json" ] && fail "type not offered: a request was written where none may be"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "type not offered: the verdict"
  return 0
}

# A region whose own resource cannot be read is a FAIL and not a pass: without it
# the check cannot show the project is short of the GPU the job names, and a job
# it cannot show that about is one that may run and bill.
test_a_region_that_cannot_be_read_stops_before_submitting() {
  setup_case region-unread
  FAKE57B_GPU_QUOTA=unread run57b
  FAKE57B_GPU_QUOTA=""
  expect_status "$status" 1 "region unread"
  expect_fact stranded FAIL "region unread"
  expect_contains "$out" "the region us-east5's own resource could not be read" "region unread: the read that failed, named"
  expect_contains "$out" "PERMISSION_DENIED" "region unread: with gcloud's own words quoted"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "batch jobs submit" "region unread: no job was submitted"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "region unread: the verdict"
  return 0
}

# The job the run made is read back, not assumed to be the one it asked for: a
# job Batch handed a VM to is not the job the queue deadline deletes, so the run
# measures nothing and says which state it found and how to remove the job.
test_a_submitted_job_that_is_not_queued_is_a_fail() {
  setup_case not-queued
  FAKE57B_STATE=SCHEDULED run57b
  FAKE57B_STATE=""
  expect_status "$status" 1 "a job that left QUEUED"
  expect_fact stranded FAIL "a job that left QUEUED"
  expect_contains "$out" "is in state SCHEDULED and not QUEUED" "a job that left QUEUED: the state it found, named"
  expect_contains "$out" "means a 48-vCPU GPU machine is billing now" "a job that left QUEUED: and what that state costs"
  expect_contains "$out" "gcloud batch jobs delete fugaro-chk57b-" "a job that left QUEUED: the one command that removes it"
  expect_contains "$out" "the guards above — the region's own resource read, no GPU quota in us-east5, and g4-standard-48 offered by one of its zones" \
    "a job that left QUEUED: the guards the row is judged against, all three named"
  # The restore row must not leave a job it cannot vouch for: 'holds no VM and
  # bills nothing' is written about a QUEUED job only.
  expect_contains "$out" "is in state SCHEDULED, and this check strands only a job that never left QUEUED" \
    "a job that left QUEUED: the restore row names the state"
  expect_absent "$out" "is LEFT IN PLACE on purpose" "a job that left QUEUED: the job is not left in place as if it were the sweep's subject"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "a job that left QUEUED: the verdict"
  return 0
}

# The pass deletes a job that never left QUEUED and only that: a job of this
# check's that Batch ran is past its deadline and unswept, and judging it by the
# queue deadline would report the reaper as having missed a sweep it was never
# owed -- a false FAIL that only gets written once, on the next run.
test_an_overdue_job_that_left_queued_is_not_read_as_a_missed_sweep() {
  setup_case overdue-not-queued
  FAKE57B_JOBS=old FAKE57B_STATE=SUCCEEDED run57b
  FAKE57B_JOBS="" FAKE57B_STATE=""
  expect_status "$status" 1 "overdue and not QUEUED"
  expect_fact stranded FAIL "overdue and not QUEUED"
  expect_contains "$out" "is in state SUCCEEDED, not QUEUED, so it is not the stranded job this step stands on" \
    "overdue and not QUEUED: the state it found, named"
  expect_contains "$out" "nothing here says a pass failed to make it" "overdue and not QUEUED: and that no sweep was owed"
  expect_absent "$out" "still QUEUED past the queue deadline" "overdue and not QUEUED: the reaper is not blamed for a job it must leave"
  expect_absent "$out" "do not adjust this check to it" "overdue and not QUEUED: and no finding is written about the pass"
  expect_contains "$out" "gcloud batch jobs delete fugaro-chk57b-20261114t1300" "overdue and not QUEUED: the job is named for removal"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "overdue and not QUEUED: the verdict"
  return 0
}

test_a_job_inside_its_deadline_is_a_reading_not_a_verdict() {
  setup_case inside
  FAKE57B_JOBS=fresh run57b
  FAKE57B_JOBS=""
  expect_status "$status" 0 "inside the deadline"
  expect_fact stranded INFO "inside the deadline"
  expect_contains "$out" "is inside the queue deadline the pass decides by" "inside the deadline: the reading"
  expect_contains "$out" "whose own line shows it left it alone" "inside the deadline: the pass left a job it must leave"
}

test_a_sweep_of_some_other_job_is_not_this_checks() {
  setup_case stranger
  FAKE57B_LOG=stranger FAKE57B_JOBS=fresh run57b
  FAKE57B_LOG="" FAKE57B_JOBS=""
  expect_status "$status" 0 "another job's deletion"
  expect_fact stranded INFO "another job's deletion"
  expect_absent "$out" "deleted job someone-elses-job" "another job's deletion: a deletion line for a job that is not this check's is not evidence"
}

test_a_sixth_permission_on_the_role_is_a_fail() {
  setup_case sixth
  FAKE57B_ROLE_PERMS="batch.jobs.list,batch.jobs.get,batch.jobs.delete,compute.instances.get,compute.instances.list,compute.instances.delete" run57b
  FAKE57B_ROLE_PERMS=""
  expect_status "$status" 1 "sixth permission"
  expect_fact role FAIL "sixth permission"
  expect_contains "$out" "compute.instances.get" "sixth permission: the permission the module does not grant, named"
  expect_contains "$out" "deploy/terraform/gcp/modules/compute/iam.tf" "sixth permission: and the authority it is judged against"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "sixth permission: the verdict"
}

test_the_tree_is_the_authority_for_the_role() {
  # The live role is the five permissions the module grants on main; the fixture
  # module grants six. A check that remembered its own list would pass this.
  setup_case tree-authority squeezed
  run57b
  expect_status "$status" 1 "the tree's list"
  expect_fact role FAIL "the tree's list"
  expect_contains "$out" "compute.instances.get" "the tree's list: the permission the fixture module grants and the live role does not hold"
}

test_the_local_pass_must_decide_by_the_same_deadlines() {
  setup_case disagree
  FAKE57B_FUGARO=disagree run57b
  FAKE57B_FUGARO=""
  expect_status "$status" 1 "disagreeing passes"
  expect_fact local-pass FAIL "disagreeing passes"
  expect_contains "$out" "decides by a queue deadline of 25h2m0s while the pass the Cloud Run job ran decided by 24h2m0s" \
    "disagreeing passes: the two deadlines, both named"
  expect_contains "$out" "refused by validate" "disagreeing passes: and why the two must agree"
}

test_a_local_pass_that_refuses_is_a_fail() {
  setup_case refuse-local
  FAKE57B_FUGARO=refuse run57b
  FAKE57B_FUGARO=""
  expect_status "$status" 1 "refused local pass"
  expect_fact local-pass FAIL "refused local pass"
  expect_contains "$out" "no batch: block in its project config" "refused local pass: the refusal read out of what fugaro said"
}

test_a_report_that_never_arrived_is_a_fail() {
  setup_case no-report
  FAKE57B_LOG=empty run57b
  FAKE57B_LOG=""
  expect_status "$status" 1 "no report"
  expect_fact report FAIL "no report"
  expect_contains "$out" "found no entries at all for the Cloud Run job" "no report: the reading"
}

test_a_log_that_carries_no_headline_is_a_fail() {
  setup_case no-headline
  FAKE57B_LOG=garbage run57b
  FAKE57B_LOG=""
  expect_status "$status" 1 "no headline"
  expect_fact report FAIL "no headline"
  expect_contains "$out" "hello, this is not a pass" "no headline: the lines it did find, quoted"
  expect_contains "$out" "reaped projects/<project>/locations/<region>" "no headline: and the shape it looked for"
}

test_a_schedule_that_makes_no_execution_is_a_fail() {
  setup_case no-execution
  RUN57B_ENV="CHK57B_WAIT_SECONDS=0" FAKE57B_RUN=no-execution run57b
  FAKE57B_RUN=""
  expect_status "$status" 1 "no execution"
  expect_fact invocation FAIL "no execution"
  expect_contains "$out" "no execution of fugaroreap that ends appeared within 0s" "no execution: the wait, named"
  expect_contains "$out" "CHK57B_WAIT_SECONDS raised" "no execution: and what to do about a slow pass"
  # The report below it is still read: this is the pass's own log, and a row
  # that read it as absent would confuse "no pass ran" with "I did not wait".
  expect_fact report PASS "no execution"
}

test_a_schedule_that_cannot_be_fired_is_a_fail() {
  setup_case run-refused
  FAKE57B_RUN=fails run57b
  FAKE57B_RUN=""
  expect_status "$status" 1 "firing refused"
  expect_fact invocation FAIL "firing refused"
  expect_contains "$out" "PERMISSION_DENIED" "firing refused: what gcloud said, quoted"
}

test_a_job_that_is_not_the_reapers_invocation_is_a_fail() {
  setup_case wrong-job
  FAKE57B_JOB_ACCOUNT="fugaro-runner@fugaro-dev.iam.gserviceaccount.com" FAKE57B_JOB=no-marker run57b
  FAKE57B_JOB_ACCOUNT="" FAKE57B_JOB=""
  expect_status "$status" 1 "another identity"
  expect_fact reaper-job FAIL "another identity"
  expect_contains "$out" "runs as 'fugaro-runner@fugaro-dev.iam.gserviceaccount.com'" "another identity: the account it does run as"
  expect_contains "$out" "FUGARO_REAPER_JOB=1" "another identity: the marker variable it is missing"
}

test_a_schedule_at_another_cadence_is_a_fail() {
  setup_case cadence
  FAKE57B_SCHED_CRON="0 * * * *" run57b
  FAKE57B_SCHED_CRON=""
  expect_status "$status" 1 "another cadence"
  expect_fact scheduler FAIL "another cadence"
  expect_contains "$out" "its cadence is '0 * * * *' and not '*/15 * * * *'" "another cadence: both cadences, named"
}

test_an_unprovisioned_installation_points_at_the_owners_step() {
  setup_case unprovisioned
  FAKE57B_ROLE=missing FAKE57B_JOB=missing FAKE57B_SCHED=missing run57b
  FAKE57B_ROLE="" FAKE57B_JOB="" FAKE57B_SCHED=""
  expect_status "$status" 1 "unprovisioned"
  expect_fact role FAIL "unprovisioned"
  expect_fact reaper-job FAIL "unprovisioned"
  expect_fact scheduler FAIL "unprovisioned"
  expect_contains "$out" "fugaro setup --provision-batch --project fugaro" "unprovisioned: the step printed for the owner"
  expect_contains "$out" "asks you to type the installation's name" "unprovisioned: with the confirmation it needs"
  # Printed, never run: this is the whole of what a coding agent's session is
  # refused, and the fake fugaro fails the check if it is reached for.
  expect_absent "$(cat "$dir/state/fugaro.calls")" "setup" "unprovisioned: the check did not run it"
}

test_a_refused_stranded_job_is_a_fail() {
  setup_case submit-refused
  FAKE57B_SUBMIT=fails run57b
  FAKE57B_SUBMIT=""
  expect_status "$status" 1 "refused submit"
  expect_fact stranded FAIL "refused submit"
  expect_contains "$out" "INVALID_ARGUMENT: the request is refused" "refused submit: the API's own words, quoted"
  expect_contains "$out" "a refusal of THIS request is a fact about the Batch API to record" "refused submit: and that the refusal is the finding"
}

# The fake's teeth, and the reason the case above is not vacuous: `batch jobs
# submit` holds the machine type against the task's share the way the API's
# submit-time validation does, in the API's own words -- the owner's run of
# 2026-11-14 was refused with 'machine_type "e2-standard-4" cannot satisfy
# compute_resource cpu_milli:64000 memory_mib:1048576'. So the request the check
# writes now is one this fake accepts, and the shape it wrote before is one no
# case can get past.
test_the_fake_refuses_the_request_the_api_refuses() {
  setup_case fake-teeth
  local status=0
  cat >"$dir/old.json" <<'JSON'
{
  "taskGroups": [{"taskSpec": {"computeResource": {"cpuMilli": "64000", "memoryMib": "1048576"}}}],
  "allocationPolicy": {"instances": [{"policy": {"machineType": "e2-standard-4"}}]}
}
JSON
  FAKE57B_DIR="$dir/state" "$dir/bin/gcloud" batch jobs submit fugaro-chk57b-old \
    --project fugaro-dev --location us-east5 --config "$dir/old.json" 2>"$dir/old.err" || status=$?
  expect_status "$status" 1 "the shape the check used to send"
  expect_contains "$(cat "$dir/old.err")" \
    'machine_type "e2-standard-4" cannot satisfy compute_resource cpu_milli:64000 memory_mib:1048576' \
    "the shape the check used to send: refused in the API's own words"
  status=0
  cat >"$dir/new.json" <<'JSON'
{
  "taskGroups": [{"taskSpec": {"computeResource": {"cpuMilli": "2000", "memoryMib": "4096"}}}],
  "allocationPolicy": {"instances": [{"policy": {"machineType": "g4-standard-48"}}]}
}
JSON
  FAKE57B_DIR="$dir/state" "$dir/bin/gcloud" batch jobs submit fugaro-chk57b-new \
    --project fugaro-dev --location us-east5 --config "$dir/new.json" || status=$?
  expect_status "$status" 0 "the shape the check sends now"
}

test_a_stranded_job_with_no_readable_time_measures_nothing() {
  setup_case unreadable
  FAKE57B_JOBS=unreadable run57b
  FAKE57B_JOBS=""
  expect_status "$status" 0 "unreadable creation time"
  expect_fact stranded INFO "unreadable creation time"
  expect_contains "$out" "could not be read" "unreadable creation time: said plainly, rather than read as a pass"
}

test_dry_run_runs_nothing() {
  setup_case dry
  run57b --dry-run
  expect_status "$status" 0 "dry run"
  expect_block_keys "dry run"
  expect_fact dry-run PASS "dry run"
  expect_contains "$out" "FACT role: INFO — not taken" "dry run: the live rows are not taken"
  expect_contains "$out" "FACT stranded: INFO — not taken" "dry run: and neither is the write"
  expect_contains "$(cat "$dir/err")" "gcloud iam roles describe fugaroReaper --project=fugaro-dev" "dry run: the commands it prints"
  expect_contains "$(cat "$dir/err")" "gcloud batch jobs submit <name>" "dry run: including the one write a real run makes"
  expect_contains "$(cat "$dir/err")" "fugaro setup --provision-batch --project fugaro" "dry run: and the owner's own step, printed"
  expect_contains "$out" "FACT restore: PASS — the scratch directory is gone" "dry run: the teardown ran and nothing was created"
  expect_no_cloud "dry run"
  expect_scratch_gone "dry run"
}

test_every_guard_stops() {
  setup_case no-owner-marker
  RUN57B_OWNER=no run57b
  RUN57B_OWNER=yes
  expect_status "$status" 2 "no owner marker"
  expect_fact owner FAIL "no owner marker"
  expect_contains "$out" "export CHK57B_OWNER_RUN=yes" "no owner marker: the marker it wants"
  expect_contains "$out" "no agent performs any step of it" "no owner marker: and that the check is the owner's"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "no owner marker: a stop is not a pass"
  expect_no_cloud "no owner marker"
  expect_scratch_gone "no owner marker"

  setup_case not-a-checkout
  mkdir -p "$dir/plain"
  RUN57B_DIR="$dir/plain" run57b
  RUN57B_DIR=""
  expect_status "$status" 2 "not a checkout"
  expect_contains "$out" "this is not a git checkout" "not a checkout: the stop"
  expect_no_cloud "not a checkout"

  setup_case no-origin default default none
  run57b
  expect_status "$status" 2 "no origin"
  expect_contains "$out" "this checkout has no origin remote" "no origin: the stop"
  expect_no_cloud "no origin"

  setup_case other-repo default default git@github.com:acme/webapp.git
  run57b
  expect_status "$status" 2 "another repository"
  expect_contains "$out" "is not the sandbox" "another repository: the stop names the one repository Check 57b runs against"
  expect_no_cloud "another repository"

  setup_case belong
  RUN57B_ENV="CLOUDSDK_CORE_PROJECT=belong" run57b
  RUN57B_ENV=""
  expect_status "$status" 2 "belong"
  expect_contains "$out" "the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch" \
    "belong: the refusal"
  expect_contains "$out" "every command here names --project=fugaro-dev itself" "belong: and that the check does not need the ambient project"
  expect_no_cloud "belong"

  setup_case no-iam none
  run57b
  expect_status "$status" 2 "no iam.tf"
  expect_contains "$out" "does not carry deploy/terraform/gcp/modules/compute/iam.tf" "no iam.tf: the stop"
  expect_no_cloud "no iam.tf"

  setup_case unserved-region default unserved
  run57b
  expect_status "$status" 2 "a region no Scheduler location serves"
  expect_contains "$out" "has no Cloud Scheduler location this tree knows of" "unserved region: the stop"
  expect_no_cloud "unserved region"

  # A scratch directory inside a git work tree is the one place this check could
  # write what somebody then commits: it refuses rather than trying to be tidy.
  setup_case scratch-in-tree
  RUN57B_ENV="TMPDIR=$dir/checkout" run57b
  RUN57B_ENV=""
  expect_status "$status" 2 "scratch inside a work tree"
  expect_contains "$out" "is inside a git work tree" "scratch inside a work tree: the stop"
  expect_contains "$out" "set TMPDIR outside every repository" "scratch inside a work tree: and what to do"
  expect_no_cloud "scratch inside a work tree"
}

test_a_stopped_run_still_prints_the_whole_block() {
  setup_case stopped-shape
  RUN57B_OWNER=no run57b
  RUN57B_OWNER=yes
  expect_status "$status" 2 "stopped run"
  expect_block_keys "stopped run"
  expect_fact role FAIL "stopped run"
  expect_contains "$out" "FACT role: FAIL — not reached" "stopped run: a row it never reached reads not reached"
  expect_fact owner FAIL "stopped run"
  expect_contains "$out" "FACT restore: PASS — " "stopped run: the teardown ran anyway"
  expect_contains "$out" "MANUAL: not reached" "stopped run: the manual line says what was not measured"
  # The last line is the verdict, and it is the one the exit status agrees with.
  [ "$(printf '%s\n' "$out" | tail -n 1)" = "CHECK 57B RESULT: FAIL" ] \
    || fail "stopped run: the block's last line is the verdict"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  (cd "$dir/checkout" &&
    exec env TMPDIR="$dir/tmp" FAKE57B_DIR="$dir/state" HOME="$dir/home" CHK57B_OWNER_RUN=yes \
      PATH="$dir/bin:$PATH" bash -x "$dir/tree/scripts/live-checks/check57b.sh") >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace"
  expect_fact xtrace FAIL "xtrace"
  expect_contains "$out" "this shell is tracing (bash -x)" "xtrace: the stop, naming the flag"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "xtrace: the verdict"
  expect_no_cloud "xtrace"
  expect_scratch_gone "xtrace"
}

test_usage() {
  setup_case usage
  run57b --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check57b.sh [--dry-run]" "help: the usage line"
  expect_no_cloud "help"

  run57b --nonsense
  expect_status "$status" 2 "unknown argument"
  expect_contains "$out" "unknown argument '--nonsense'" "unknown argument: the argument it names"
  expect_contains "$out" "CHECK 57B RESULT: FAIL" "unknown argument: a fact, so the block is still printable"
  expect_no_cloud "unknown argument"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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

t test_happy_path_creates_the_stranded_job
t test_a_later_run_reads_the_sweep
t test_a_job_past_its_deadline_that_was_not_swept_is_a_fail
t test_an_overdue_job_that_left_queued_is_not_read_as_a_missed_sweep
t test_a_job_inside_its_deadline_is_a_reading_not_a_verdict
t test_gpu_quota_above_zero_stops_before_submitting
t test_a_machine_type_no_zone_offers_stops_before_submitting
t test_a_region_that_cannot_be_read_stops_before_submitting
t test_a_submitted_job_that_is_not_queued_is_a_fail
t test_a_sweep_of_some_other_job_is_not_this_checks
t test_a_sixth_permission_on_the_role_is_a_fail
t test_the_tree_is_the_authority_for_the_role
t test_the_local_pass_must_decide_by_the_same_deadlines
t test_a_local_pass_that_refuses_is_a_fail
t test_a_report_that_never_arrived_is_a_fail
t test_a_log_that_carries_no_headline_is_a_fail
t test_a_schedule_that_makes_no_execution_is_a_fail
t test_a_schedule_that_cannot_be_fired_is_a_fail
t test_a_job_that_is_not_the_reapers_invocation_is_a_fail
t test_a_schedule_at_another_cadence_is_a_fail
t test_an_unprovisioned_installation_points_at_the_owners_step
t test_a_refused_stranded_job_is_a_fail
t test_the_fake_refuses_the_request_the_api_refuses
t test_a_stranded_job_with_no_readable_time_measures_nothing
t test_dry_run_runs_nothing
t test_every_guard_stops
t test_a_stopped_run_still_prints_the_whole_block
t test_xtrace_is_refused
t test_usage

if [ "$failures" -ne 0 ]; then
  echo "check57b_test: $failures failure(s)" >&2
  exit 1
fi
echo "check57b_test: all tests passed" >&2
