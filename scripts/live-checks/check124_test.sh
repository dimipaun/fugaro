#!/usr/bin/env bash
# Tests for scripts/live-checks/check124.sh, against a fake gcloud, a fake
# fugaro and a fake curl on PATH plus fixture files: nothing here reaches the
# network, there is no cloud project, no credential, and no live step of the
# check is run -- an agent never runs a check, and this suite is what says so.
#
# The fake fugaro answers only the commands the check is allowed to run, and
# `setup --provision-batch --project fugaro` is NOT one of them: the applying
# form is refused here, so a case that made the check run it fails, which is the
# point -- the register hands that step to the owner and the CLI itself refuses a
# coding agent's session it. `setup --plan-only` IS answered, because 124a-2's
# fact is what the check reads with it.
#
# The script under test is COPIED into a fixture tree that also carries a fixture
# internal/backend/batch/run.go, so 124c's exit code is read from a file this
# suite owns: a case can bend the value the tree carries and the value the job
# reports separately, which is the only way to say the check compares them rather
# than remembering one.
#
# Usage: bash scripts/live-checks/check124_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check124.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK124_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

# The fakes' knobs are exported once here, empty: a prefix assignment on a
# FUNCTION call is in the function's scope but is not exported to the tools it
# goes on to run, so a knob written that way would never reach the fake.
# The fakes' knobs, in one list so a case can start from nothing: they are
# unset and re-exported before every case, because a knob one case set would
# otherwise decide the next case's answer -- and the leak reads as a check that
# passed rather than as a test that never ran.
KNOBS="FAKE124_JOBS FAKE124_JOBJSON FAKE124_JOBLABELS FAKE124_STATE_SEQ
FAKE124_CODE FAKE124_RETRY FAKE124_MACHINE FAKE124_BOOT FAKE124_NET FAKE124_SUBNET
FAKE124_SPOT FAKE124_INSTANCES FAKE124_RUNVMS FAKE124_RUNVMS_SEQ FAKE124_TEMPLATES FAKE124_IMAGES
FAKE124_DELETE FAKE124_DOCTOR_ALL FAKE124_DOCTOR FAKE124_STRICT_EXIT
FAKE124_PLANONLY FAKE124_PLANONLY_EXIT FAKE124_VALIDATE FAKE124_VALIDATE_EXIT
FAKE124_BACKENDS FAKE124_BACKENDS_EXIT FAKE124_RUNS FAKE124_RUNS_FIRST
FAKE124_RUNS_TEXT FAKE124_RUN FAKE124_RUN_EXIT FAKE124_BUDGET
FAKE124_REAPER_DRY FAKE124_REAPER FAKE124_DECLINE FAKE124_DECLINE_EXIT
FAKE124_BRANCH_EXIT"
reset_knobs() {
  # shellcheck disable=SC2086 # the list is unquoted on purpose: it is names
  unset $KNOBS CHK124_TOKEN_FILE 2>/dev/null || true
  # shellcheck disable=SC2086
  export $KNOBS
}
reset_knobs

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
expect_block_keys() { # keys... what
  local what=${!#} key
  local keys=("$@")
  unset 'keys[${#keys[@]}-1]'
  for key in "${keys[@]}"; do
    expect_contains "$out" "FACT $key: " "$what: the block carries the $key row"
  done
  expect_contains "$out" "MANUAL: " "$what: the block carries the manual line"
}
expect_no_cloud() { # what
  local g f c
  g=$(cat "$dir/state/gcloud.calls" 2>/dev/null || true)
  f=$(cat "$dir/state/fugaro.calls" 2>/dev/null || true)
  c=$(cat "$dir/state/curl.calls" 2>/dev/null || true)
  [ -z "$g" ] || fail "$1: it ran gcloud, which it must not have: $(printf '%s' "$g" | head -c 300)"
  [ -z "$f" ] || fail "$1: it ran fugaro, which it must not have: $(printf '%s' "$f" | head -c 300)"
  [ -z "$c" ] || fail "$1: it ran curl, which it must not have: $(printf '%s' "$c" | head -c 300)"
}
no_setup_apply() { # what
  local f
  f=$(cat "$dir/state/fugaro.calls" 2>/dev/null || true)
  case "$f" in
  *"setup --provision-batch"*)
    fail "$1: the check ran the APPLYING fugaro setup form, which is the owner's step and never an agent's: $(printf '%s' "$f" | head -c 300)"
    ;;
  esac
}

# ------------------------------------------------------------ the fixtures
RUNID=20261114-130008-b001
LABELV=fugaro-check-20261114-130008-b001
TOKEN=sandbox-token-sentinel

write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
# A fake gcloud for check124_test.sh: it answers the reads and the ONE delete the
# check makes, out of $FAKE124_DIR, and reaches nothing. Every call is appended
# to $FAKE124_DIR/gcloud.calls, argv verbatim, so a case can say what the check
# read -- and a dry-run case can say it read nothing at all.
import json
import os
import sys

state = os.environ["FAKE124_DIR"]
args = sys.argv[1:]
with open(os.path.join(state, "gcloud.calls"), "a", encoding="utf-8") as fh:
    fh.write("gcloud " + " ".join(args) + "\n")

PROJECT = "fugaro-dev"
LOCATION = "us-east5"


def env(name, default=""):
    return os.environ.get(name) or default


def maybe(name, default=""):
    # A knob that is set to "-" means the EMPTY answer. env() cannot say it --
    # an empty variable falls back to the default -- and a case needs to say it:
    # an installation with no jobs before the check, a job with no subnetwork, a
    # project with no preload image.
    v = os.environ.get(name)
    if v is None or v == "":
        return default
    return "" if v == "-" else v


def emit(v):
    # An empty list prints nothing at all: a print("") would leave a blank line
    # in the check's before-state file and the count it reads would be 1.
    if v:
        print(v)


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


def has(name):
    return any(a == name or a.startswith(name + "=") for a in args)


def fmt():
    return flag("--format", "")


def jobname(kind):
    return "projects/%s/locations/%s/jobs/%s" % (PROJECT, LOCATION, kind)


def peek_state():
    # The state a describe REPORTS, without advancing: a job's own JSON carries
    # the same state as the value() read beside it.
    seq = env("FAKE124_STATE_SEQ", "RUNNING:FAILED").split(":")
    path = os.path.join(state, "stateseen")
    n = 0
    if os.path.exists(path):
        n = int(open(path).read().strip() or 0)
    return seq[min(n, len(seq) - 1)]


def next_state():
    # The state the check reads when it reads the state, advanced once per read
    # and sticking at the last: a real job goes RUNNING and then ends, and a
    # case says so with a sequence. Only the --format=value(status.state) call
    # advances it, which is the read the check's own loops make.
    seq = env("FAKE124_STATE_SEQ", "RUNNING:FAILED").split(":")
    path = os.path.join(state, "stateseen")
    n = 0
    if os.path.exists(path):
        n = int(open(path).read().strip() or 0)
    open(path, "w").write(str(n + 1))
    return seq[min(n, len(seq) - 1)]


def task_exit_code():
    return int(env("FAKE124_CODE", "4242"))


def runvms():
    # The VMs ONE `compute instances list` answers with, advanced once per call
    # and sticking at the last: the in-flight read and the reclaim read are two
    # calls of the same command, and a case that needs them to disagree says so
    # here -- `;` between two calls' answers, `|` between the VMs of one answer.
    # A case that leaves this unset gets FAKE124_RUNVMS on every call.
    seq = env("FAKE124_RUNVMS_SEQ")
    if not seq:
        return maybe("FAKE124_RUNVMS", "fugaro-run-vm\tus-east5-a")
    parts = seq.split(";")
    path = os.path.join(state, "runvmsseen")
    n = 0
    if os.path.exists(path):
        n = int(open(path).read().strip() or 0)
    open(path, "w").write(str(n + 1))
    return parts[min(n, len(parts) - 1)].replace("|", "\n")


def job_json(jobid, labelv):
    alloc = {
        "instances": [{"policy": {
            "machineType": env("FAKE124_MACHINE", "e2-standard-4"),
            "bootDisk": {"image": maybe(
                "FAKE124_BOOT",
                "projects/fugaro-dev/global/images/family/fugaro-preload-deadbeef")},
        }}],
        "network": {"networkInterfaces": [{
            "network": maybe("FAKE124_NET",
                             "projects/fugaro-dev/global/networks/fugaro-batch"),
            "subnetwork": maybe("FAKE124_SUBNET",
                                "projects/fugaro-dev/regions/us-east5/subnetworks/fugaro-batch"),
        }]},
    }
    if env("FAKE124_SPOT", "no") != "no":
        alloc["provisioningModel"] = "SPOT"
    retry = env("FAKE124_RETRY", "0")
    spec = {} if retry == "absent" else {"maxRetryCount": int(retry)}
    return {
        "name": jobname(jobid),
        "labels": {"fugaro": "managed", "fugaro_run": labelv},
        "taskGroups": [{"taskSpec": spec}],
        "allocationPolicy": alloc,
        "status": {
            "state": peek_state(),
            # The task's exit code rides a task-level status event, which is the
            # only place Batch v1 has it (StatusEvent.TaskExecution.ExitCode),
            # and the shape internal/backend/batch/batch_test.go's own
            # preemptEvent() writes. A code anywhere else is a shape the API
            # does not have, and the check reads the API's.
            "statusEvents": [
                {"description": "Job state changed to FAILED"},
                {"type": "STATUS_CHANGED", "description": "Task state is FAILED",
                 "taskState": "FAILED",
                 "taskExecution": {"exitCode": task_exit_code()}},
            ],
        },
    }


if args[:3] == ["batch", "jobs", "list"]:
    if fmt() == "json":
        print(env("FAKE124_JOBJSON", json.dumps([job_json("fugaro-124b-job",
                                                          env("FAKE124_LABELV", "x"))])))
    elif fmt().startswith("value(name,labels"):
        emit(maybe("FAKE124_JOBLABELS",
                   "%s\t%s" % (jobname("fugaro-124b-job"), env("FAKE124_LABELV", "x"))))
    else:
        emit(maybe("FAKE124_JOBS", jobname("fugaro-124b-job") + "\tFAILED"))
elif args[:3] == ["batch", "jobs", "describe"]:
    if fmt().startswith("value(status"):
        print(next_state())
    else:
        print(json.dumps(job_json(env("FAKE124_JOBID", "fugaro-124b-job"),
                                  env("FAKE124_LABELV", "x"))))
elif args[:3] == ["compute", "instances", "list"]:
    if "zone.basename" in fmt():
        emit(runvms())
    elif env("FAKE124_INSTANCES") == "fails":
        die("PERMISSION_DENIED: the caller does not have compute.instances.list")
    else:
        emit(maybe("FAKE124_INSTANCES"))
elif args[:3] == ["compute", "instances", "delete"]:
    if env("FAKE124_DELETE") == "fails":
        die("RESOURCE_IN_USE: the instance is being used")
    print("Deleted [https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s]."
          % (PROJECT, flag("--zone", "?"), args[3]), file=sys.stderr)
elif args[:3] == ["compute", "instance-templates", "list"]:
    if env("FAKE124_TEMPLATES") == "fails":
        die("PERMISSION_DENIED: the caller does not have compute.instanceTemplates.list")
    emit(maybe("FAKE124_TEMPLATES"))
elif args[:3] == ["compute", "images", "list"]:
    emit(maybe("FAKE124_IMAGES", "fugaro-preload-deadbeef"))
else:
    die("this test's fake gcloud has no answer for: %s" % " ".join(args))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check124_test.sh. It answers the reads the check makes and
# NOTHING else: `setup --provision-batch` -- the applying form, whose
# confirmation is the owner's -- and any command no case expects are a failure
# here, which is how this suite says no agent runs that step.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE124_DIR/fugaro.calls"
emit() { # VAR DEFAULT
  local v=${!1-}
  if [ -n "$v" ]; then printf '%s\n' "$v"; else printf '%s\n' "$2"; fi
}
case "$*" in
"doctor --project fugaro --all")
  emit FAKE124_DOCTOR_ALL 'project: fugaro (GCP fugaro-dev, us-east5)
batch: configured'
  ;;
"doctor --strict --project fugaro")
  emit FAKE124_DOCTOR 'info batch-artifact-boundary: the run accounts may read the artifact repository and nothing else
fugaro doctor: 12 checks, 0 warnings'
  exit "${FAKE124_STRICT_EXIT:-0}"
  ;;
"doctor --project fugaro")
  emit FAKE124_DOCTOR 'info batch-artifact-boundary: the run accounts may read the artifact repository and nothing else
fugaro doctor: 12 checks, 0 warnings'
  exit "${FAKE124_STRICT_EXIT:-0}"
  ;;
"setup --plan-only --provision-batch --project fugaro")
  emit FAKE124_PLANONLY 'Plan: 0 to add, 0 to change, 0 to destroy.
--plan-only: nothing applied; the plan is /tmp/fugaro-plan.tfplan (keep it, or delete it: it is yours)'
  exit "${FAKE124_PLANONLY_EXIT:-0}"
  ;;
"validate")
  emit FAKE124_VALIDATE 'fugaro.yaml is valid'
  exit "${FAKE124_VALIDATE_EXIT:-0}"
  ;;
"backends ls --project fugaro --json")
  emit FAKE124_BACKENDS '{"backends":[{"name":"cloud-run","in_use":false},{"name":"cloud-batch","in_use":true}],"project":"fugaro","backend":"cloud-batch","complete":true}'
  exit "${FAKE124_BACKENDS_EXIT:-0}"
  ;;
"runs ls --project fugaro --json")
  # The check's two runs, in the two documents the listing has: the one while
  # they are in flight (the FIRST call, so part b can read the draft row) and
  # the one they have ended (every call after). A case that reads the listing
  # once, after the fact -- parts c and d -- sets FAKE124_RUNS_FIRST=same.
  if [ -f "$FAKE124_DIR/jsonseen" ]; then
    emit FAKE124_RUNS '{"runs":[
      {"run_id":"20261114-130008-b001","title":"check 124b: one task on the Batch backend","status":"succeeded","outcome":"ready","pr":41,"pr_url":"https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/41","pr_state":"open","cost":{"model_usd":2.83,"compute_usd":0.42,"compute_estimated":false,"total_usd":3.25}},
      {"run_id":"20261114-140000-c002","title":"check 124c: the preemption","status":"failed","derived":"preempted","reason":"the job ended FAILED and the read side derives a preemption from it","cost":{"model_usd":1.44,"compute_usd":0.31,"compute_estimated":false,"total_usd":1.75}}
    ]}'
  else
    : >"$FAKE124_DIR/jsonseen"
    case "${FAKE124_RUNS_FIRST:-}" in
    same)
      emit FAKE124_RUNS '{"runs":[
      {"run_id":"20261114-130008-b001","title":"check 124b: one task on the Batch backend","status":"succeeded","outcome":"ready","pr":41,"pr_url":"https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/41","pr_state":"open","cost":{"model_usd":2.83,"compute_usd":0.42,"compute_estimated":false,"total_usd":3.25}},
      {"run_id":"20261114-140000-c002","title":"check 124c: the preemption","status":"failed","derived":"preempted","reason":"the job ended FAILED and the read side derives a preemption from it","cost":{"model_usd":1.44,"compute_usd":0.31,"compute_estimated":false,"total_usd":1.75}}
    ]}'
      ;;
    "")
      printf '%s\n' '{"runs":[
        {"run_id":"20261114-130008-b001","title":"check 124b: one task on the Batch backend","status":"running","stage":"implement","pr":41,"pr_url":"https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/41","pr_state":"draft","cost":{"model_usd":0.4,"compute_usd":0.1,"compute_estimated":true,"total_usd":0.5}},
        {"run_id":"20261114-140000-c002","title":"check 124c: the preemption","status":"running","stage":"implement","cost":{"model_usd":0.3,"compute_usd":0.1,"compute_estimated":true,"total_usd":0.4}}
      ]}'
      ;;
    *)
      printf '%s\n' "$FAKE124_RUNS_FIRST"
      ;;
    esac
  fi
  ;;
"runs ls --project fugaro")
  emit FAKE124_RUNS_TEXT '20261114-130008-b001  running'
  ;;
"run --json "*)
  [ "${FAKE124_RUN_EXIT:-0}" = 0 ] || {
    echo "fugaro: the launcher refused this task" >&2
    exit "${FAKE124_RUN_EXIT}"
  }
  emit FAKE124_RUN '{"run_id":"20261114-130008-b001","branch":"fugaro/20261114-130008-b001","log_url":"https://console.cloud.google.com/run/jobs"}'
  ;;
"budget show --project fugaro")
  emit FAKE124_BUDGET 'mode: enforce
day: 20261114
largest single lease (max reserve): $12.00
  fugaro: daily cap $40.00, per-run cap $12.00
  today: counted $0.00, spent $0.00, notional $0.00 (subscription, not billed)
  headroom: $40.00
  kill switch: none
  running: none'
  ;;
"reaper --dry-run --project fugaro")
  emit FAKE124_REAPER_DRY 'would reap projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, run budget 24h17m0s, max age 48h19m0s
nothing to do'
  ;;
"reaper --project fugaro")
  emit FAKE124_REAPER 'reaped projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, run budget 24h17m0s, max age 48h19m0s
nothing to do'
  ;;
*)
  echo "fake fugaro: this check must never run '$*'" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check124_test.sh: it answers the two Bitbucket calls 124b's
# restore makes and nothing else. The --config file the check hands over is read
# here the way curl would, so a case can say the token travelled in the file and
# never on the command line.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE124_DIR/curl.calls"
url=""
cfg=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config) cfg=$2; shift ;;
  --config=*) cfg=${1#--config=} ;;
  http*) url=$1 ;;
  esac
  shift
done
if [ -n "$cfg" ]; then
  # The header the check built: recorded so a case can read what it sent.
  grep '^header = ' "$cfg" >>"$FAKE124_DIR/curl.headers" 2>/dev/null || true
fi
case "$url" in
*/decline)
  [ "${FAKE124_DECLINE_EXIT:-0}" = 0 ] || {
    echo '{"type":"error","error":{"message":"You do not have access to this repository"}}' >&2
    exit "${FAKE124_DECLINE_EXIT}"
  }
  printf '%s\n' "${FAKE124_DECLINE:-{\"state\":\"DECLINED\"}}"
  ;;
*/refs/branches/*)
  [ "${FAKE124_BRANCH_EXIT:-0}" = 0 ] || {
    echo '{"type":"error","error":{"message":"Branch not found"}}' >&2
    exit "${FAKE124_BRANCH_EXIT}"
  }
  ;;
*)
  echo "fake curl: no answer for $url" >&2
  exit 1
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"

  cat >"$1/yaml" <<'YAML'
# the sandbox repository's own config
image: ghcr.io/dimipaun/fugaro-base:latest
YAML
}

write_tree() { # dir [RUNGO]
  local dir=$1 rungo=${2:-default}
  mkdir -p "$dir/tree/scripts/live-checks" "$dir/tree/internal/backend/batch"
  cp "$script" "$dir/tree/scripts/live-checks/check124.sh"
  case "$rungo" in
  none)
    rm -f "$dir/tree/internal/backend/batch/run.go"
    ;;
  *)
    cat >"$dir/tree/internal/backend/batch/run.go" <<'GO'
package batch

const (
	// preemptExitCode is the exit code a task returns when the platform
	// reclaimed its VM. It is a fixture in this suite, deliberately not the
	// value the real tree carries: the check must read the file, not know it.
	preemptExitCode = 4242
)
GO
    ;;
  esac
}

write_checkout() { # dir [ORIGIN] [TREE]
  local dir=$1 origin=${2:-git@github.com:edgeappinc/fugarosandbox.git} tree=${3:-none}
  mkdir -p "$dir/checkout"
  git -C "$dir/checkout" init -q
  git -C "$dir/checkout" remote remove origin 2>/dev/null || true
  if [ "$origin" != none ]; then
    git -C "$dir/checkout" remote add origin "$origin"
  fi
  cp "$dir/yaml" "$dir/checkout/fugaro.yaml"
  if [ "$tree" = tree ]; then
    mkdir -p "$dir/checkout/internal/backend/batch"
    cp "$dir/tree/internal/backend/batch/run.go" "$dir/checkout/internal/backend/batch/run.go"
  fi
  git -C "$dir/checkout" add -A
  git -C "$dir/checkout" -c user.email=t@t -c user.name=t commit -q -m x
}

# setup_case NAME [RUNGO] [ORIGIN] [TREE]
setup_case() { # NAME [RUNGO] [ORIGIN] [TREE]
  dir="$work/$1"
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/work"
  write_fakes "$dir"
  : >"$dir/state/gcloud.calls"
  : >"$dir/state/fugaro.calls"
  : >"$dir/state/curl.calls"
  reset_knobs
  write_tree "$dir" "${2:-default}"
  write_checkout "$dir" "${3:-git@github.com:edgeappinc/fugarosandbox.git}" "${4:-none}"
  printf '%s\n' "$TOKEN" >"$dir/state/token"
}

# run124 [ARGS...] — runs the check on the case at hand, from the case's fixture
# checkout. Sets $status, $out and $dir/err. RUN124_OWNER=no leaves the owner
# marker unexported; RUN124_ENV adds one "<name>=<value>" entry; RUN124_DIR runs
# the check from somewhere else.
RUN124_OWNER=yes
RUN124_ENV=""
RUN124_DIR=""
run124_exec() { # [ARGS...]
  cd "${RUN124_DIR:-$dir/checkout}" || exit 1
  local extra=(
    CHK124_WORK="$dir/work"
    CHK124_POLL_SECONDS=0
    CHK124_RUN_WAIT_SECONDS=3
    CHK124_INFLIGHT_WAIT_SECONDS=0
    CHK124_TERMINAL_WAIT_SECONDS=0
    FAKE124_LABELV="$LABELV"
  )
  [ -n "$RUN124_ENV" ] && extra+=("$RUN124_ENV")
  if [ "$RUN124_OWNER" = yes ]; then
    extra+=(CHK124_OWNER_RUN=yes)
  fi
  exec env TMPDIR="$dir/tmp" FAKE124_DIR="$dir/state" HOME="$dir/home" \
    "${extra[@]}" PATH="$dir/bin:$PATH" bash "$dir/tree/scripts/live-checks/check124.sh" "$@"
}
run124() {
  status=0
  (run124_exec "$@") >"$dir/out" 2>"$dir/err" || status=$?
  RUN124_OWNER=yes
  RUN124_ENV=""
  RUN124_DIR=""
  out=$(cat "$dir/out")
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^check124.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | tail -n 1
}
expect_scratch_gone() { # what
  local s
  s=$(scratch_of "$dir")
  [ -n "$s" ] || fail "$1: the run never announced a scratch directory"
  [ ! -e "$s" ] || fail "$1: the scratch directory $s is still there"
}

# ---------------------------------------------------------------------- 124a
test_part_a_reads_the_plan_and_the_two_readbacks() {
  setup_case a-happy
  export FAKE124_JOBS=- FAKE124_INSTANCES=- FAKE124_TEMPLATES=-
  run124 --part a
  expect_status "$status" 0 "part a"
  expect_block_keys owner installation checkout belong workdir xtrace before plan-only apply doctor validate restore "part a"
  expect_contains "$out" "FACT before: INFO" "part a: the before-state is a reading"
  expect_contains "$out" "jobs-before.txt 0, instances-before.txt 0, templates-before.txt 0" \
    "part a: the three before-state files, written and counted"
  [ -f "$dir/work/jobs-before.txt" ] || fail "part a: the before-state was not written into the work directory"
  expect_fact plan-only PASS "part a"
  expect_fact apply INFO "part a"
  expect_contains "$out" "fugaro setup --provision-batch --project fugaro" "part a: the applying form is NAMED for the owner"
  no_setup_apply "part a"
  expect_contains "$(cat "$dir/state/fugaro.calls")" "setup --plan-only --provision-batch --project fugaro" \
    "part a: and the plan-only form is what the check ran"
  expect_fact doctor PASS "part a"
  expect_fact validate PASS "part a"
  expect_contains "$out" "CHECK 124A RESULT: PASS" "part a: the verdict"
  expect_scratch_gone "part a"
  [ -f "$dir/work/.chk124-work" ] || fail "part a: the work directory must be kept, with its marker"
}

test_part_a_a_compute_address_in_the_plan_only_output_is_a_fail() {
  setup_case a-compute
  export FAKE124_PLANONLY='Plan: 4 to add, 0 to change, 0 to destroy.
module.compute.google_batch_job.reaper will be created
--plan-only: nothing applied; the plan is /tmp/fugaro-plan.tfplan'
  run124 --part a
  expect_status "$status" 1 "compute address in plan-only"
  expect_fact plan-only FAIL "compute address in plan-only"
  expect_contains "$out" "CHECK 124A RESULT: FAIL" "compute address in plan-only: the verdict"
}

test_part_a_a_refused_plan_only_is_a_fail() {
  setup_case a-plan-refused
  export FAKE124_PLANONLY_EXIT=1 FAKE124_PLANONLY="fugaro: no compute root for this installation"
  run124 --part a
  expect_status "$status" 1 "refused plan-only"
  expect_fact plan-only FAIL "refused plan-only"
}

test_part_a_a_batch_warning_is_a_fail() {
  setup_case a-warning
  export FAKE124_DOCTOR='info batch-artifact-boundary: the run accounts may read the artifact repository and nothing else
warning batch-preload-family: the family fugaro-preload-deadbeef is not there
fugaro doctor: 12 checks, 1 warning'
  export FAKE124_STRICT_EXIT=1
  run124 --part a
  expect_status "$status" 1 "batch warning"
  expect_fact doctor FAIL "batch warning"
  expect_contains "$out" "fugaro-preload-deadbeef is not there" "batch warning: the line is quoted whole"
}

test_part_a_a_strict_exit_without_a_batch_warning_is_a_reading() {
  setup_case a-strict-other
  # doctor as a whole is not OK, and the Batch half is: the row says both rather
  # than turning another part's warning into this check's finding.
  export FAKE124_DOCTOR='info batch-artifact-boundary: the run accounts may read the artifact repository and nothing else
warning ghcr-login: the base image is not pulled
fugaro doctor: 12 checks, 1 warning'
  export FAKE124_STRICT_EXIT=1
  run124 --part a
  expect_status "$status" 0 "strict exit without a batch warning"
  expect_fact doctor INFO "strict exit without a batch warning"
  expect_contains "$out" "NO batch- line is a warning" "strict exit without a batch warning: the row says which half is OK"
}

test_part_a_a_doctor_with_no_batch_line_is_a_fail() {
  setup_case a-nobatch
  export FAKE124_DOCTOR='fugaro doctor: 11 checks, 0 warnings'
  run124 --part a
  expect_status "$status" 1 "no batch line"
  expect_fact doctor FAIL "no batch line"
}

test_part_a_the_old_validate_refusal_is_a_fail() {
  setup_case a-validate
  export FAKE124_VALIDATE='fugaro.yaml: backend cloud-batch is not enabled yet' FAKE124_VALIDATE_EXIT=1
  run124 --part a
  expect_status "$status" 1 "old validate refusal"
  expect_fact validate FAIL "old validate refusal"
  expect_contains "$out" "predates it" "old validate refusal: the row says what a refusal now means"
}

test_part_a_dry_run_touches_no_cloud() {
  setup_case a-dry
  run124 --part a --dry-run
  expect_status "$status" 0 "part a dry run"
  expect_no_cloud "part a dry run"
  expect_contains "$out" "CHECK 124A RESULT: PASS" "part a dry run: the verdict"
  expect_contains "$out" "FACT plan-only: INFO" "part a dry run: the live rows are readings"
  expect_contains "$(cat "$dir/err")" "[yours]  fugaro setup --provision-batch --project fugaro" \
    "part a dry run: the owner's step is printed in the plan"
  [ ! -e "$dir/work/jobs-before.txt" ] || fail "part a dry run: it wrote the before-state, which is a cloud read"
}

# ---------------------------------------------------------------------- 124b
test_part_b_runs_one_task_to_its_ready_pull_request() {
  setup_case b-happy
  export CHK124_TOKEN_FILE="$dir/state/token"
  run124 --part b
  expect_status "$status" 0 "part b"
  expect_block_keys owner installation checkout belong workdir xtrace backend repo-yaml launch job draft run decline restore "part b"
  expect_fact backend PASS "part b"
  expect_contains "$out" "cloud-run cloud-batch(in use)" "part b: both names, and the selected one"
  expect_fact repo-yaml PASS "part b"
  expect_fact launch PASS "part b"
  expect_contains "$out" "$RUNID" "part b: the run it launched"
  expect_fact job PASS "part b"
  expect_contains "$out" "fugaro-preload-deadbeef" "part b: the boot image family the job boots"
  expect_contains "$out" "fugaro-batch" "part b: the network and subnetwork"
  expect_contains "$out" "no Spot provisioning model" "part b: no Spot anywhere in the allocation policy"
  expect_contains "$out" "FACT draft: INFO" "part b: the row read while the run was in flight"
  expect_fact run PASS "part b"
  expect_fact decline PASS "part b"
  expect_contains "$(cat "$dir/state/curl.calls")" "/pullrequests/41/decline" "part b: the decline"
  expect_contains "$(cat "$dir/state/curl.calls")" "/refs/branches/fugaro/$RUNID" "part b: the branch delete"
  expect_absent "$(cat "$dir/state/curl.calls")" "$TOKEN" "part b: the token never reached a command line"
  expect_contains "$(cat "$dir/state/curl.headers")" "$TOKEN" "part b: it travelled in the --config file"
  expect_contains "$out" "CHECK 124B RESULT: PASS" "part b: the verdict"
  expect_scratch_gone "part b"
}

test_part_b_a_run_that_ends_a_draft_is_a_fail() {
  setup_case b-draft
  export FAKE124_RUNS='{"runs":[{"run_id":"'"$RUNID"'","title":"check 124b: one task on the Batch backend","status":"failed","outcome":"draft","reason":"tests failed","pr":41,"pr_url":"https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/41","pr_state":"draft"}]}'
  export FAKE124_RUNS_FIRST="$FAKE124_RUNS" CHK124_TOKEN_FILE="$dir/state/token"
  run124 --part b
  expect_status "$status" 1 "run ended a draft"
  expect_fact run FAIL "run ended a draft"
}

test_part_b_a_backend_that_is_not_selected_is_a_fail() {
  setup_case b-backend
  export FAKE124_BACKENDS='{"backends":[{"name":"cloud-run","in_use":true},{"name":"cloud-batch","in_use":false}],"project":"fugaro","backend":"cloud-run","complete":true}'
  run124 --part b
  expect_status "$status" 1 "backend not selected"
  expect_fact backend FAIL "backend not selected"
}

test_part_b_an_unselected_project_is_a_fail() {
  setup_case b-incomplete
  export FAKE124_BACKENDS='{"backends":[{"name":"cloud-run","in_use":false},{"name":"cloud-batch","in_use":false}],"complete":false,"note":"no project config could be selected"}'
  run124 --part b
  expect_status "$status" 1 "nothing selected"
  expect_fact backend FAIL "nothing selected"
}

test_part_b_a_repository_backend_key_is_a_fail() {
  setup_case b-yaml
  printf 'backend: cloud-batch\n' >>"$dir/checkout/fugaro.yaml"
  run124 --part b
  expect_status "$status" 1 "backend: in the repository config"
  expect_fact repo-yaml FAIL "backend: in the repository config"
}

test_part_b_a_job_on_a_default_network_is_a_fail() {
  setup_case b-network
  export FAKE124_NET=default FAKE124_SUBNET=-
  run124 --part b
  expect_status "$status" 1 "a job with no subnetwork"
  expect_fact job FAIL "a job with no subnetwork"
}

test_part_b_a_spot_job_is_a_fail() {
  setup_case b-spot
  export FAKE124_SPOT=yes
  run124 --part b
  expect_status "$status" 1 "a Spot job"
  expect_fact job FAIL "a Spot job"
}

test_part_b_without_a_token_file_nothing_is_deleted() {
  setup_case b-notoken
  run124 --part b
  expect_status "$status" 1 "no token file"
  expect_fact decline FAIL "no token file"
  expect_contains "$out" "the decline and the branch delete are made with the sandbox token" \
    "no token file: the row names the export the owner needs"
  local c
  c=$(cat "$dir/state/curl.calls" 2>/dev/null || true)
  [ -z "$c" ] || fail "no token file: it made a Bitbucket call without a token: $c"
}

test_part_b_a_refused_decline_leaves_the_branch_alone() {
  setup_case b-refused
  export CHK124_TOKEN_FILE="$dir/state/token" FAKE124_DECLINE_EXIT=22
  run124 --part b
  expect_status "$status" 1 "refused decline"
  expect_fact decline FAIL "refused decline"
  expect_absent "$(cat "$dir/state/curl.calls")" "refs/branches" \
    "refused decline: the branch delete is not attempted over an open pull request"
}

test_part_b_dry_run_touches_no_cloud() {
  setup_case b-dry
  run124 --part b --dry-run
  expect_status "$status" 0 "part b dry run"
  expect_no_cloud "part b dry run"
  expect_contains "$out" "CHECK 124B RESULT: PASS" "part b dry run: the verdict"
}

# ---------------------------------------------------------------------- 124c
test_part_c_forces_a_preemption_and_reads_the_tree() {
  setup_case c-happy
  export FAKE124_RUNS_FIRST=same
  export FAKE124_STATE_SEQ="RUNNING:FAILED"
  run124 --part c
  expect_status "$status" 0 "part c"
  expect_block_keys owner installation checkout belong workdir xtrace exit-code launch in-flight reclaim terminal reaper restore "part c"
  expect_contains "$out" "FACT exit-code: INFO" "part c: the exit code is a reading"
  expect_contains "$out" "preemptExitCode = 4242" "part c: read out of the tree, with the line"
  expect_fact in-flight PASS "part c"
  expect_contains "$out" "fugaro-run-vm" "part c: the one VM carrying the job's own label value"
  expect_fact reclaim PASS "part c"
  expect_contains "$(cat "$dir/state/gcloud.calls")" "compute instances delete fugaro-run-vm --project=fugaro-dev --zone=us-east5-a --quiet" \
    "part c: the one delete, and its scope"
  expect_fact terminal PASS "part c"
  expect_contains "$out" "maxRetryCount is present and 0" "part c: the retry count really zero"
  expect_contains "$out" "4242" "part c: the task's exit code, and it equals the tree's"
  expect_fact reaper PASS "part c"
  expect_contains "$out" "CHECK 124C RESULT: PASS" "part c: the verdict"
  expect_scratch_gone "part c"
}

test_part_c_an_exit_code_the_tree_does_not_carry_is_a_fail() {
  setup_case c-code
  export FAKE124_STATE_SEQ="RUNNING:FAILED" FAKE124_CODE=50001
  run124 --part c
  expect_status "$status" 1 "an exit code the tree does not carry"
  expect_fact terminal FAIL "an exit code the tree does not carry"
  expect_contains "$out" "'50001' where the tree's preemptExitCode is '4242'" \
    "an exit code the tree does not carry: both values, and the instruction to record it"
}

test_part_c_the_checkout_is_the_tree_when_it_carries_it() {
  setup_case c-tree default git@github.com:edgeappinc/fugarosandbox.git tree
  export FAKE124_RUNS_FIRST=same
  export FAKE124_STATE_SEQ="RUNNING:FAILED"
  run124 --part c
  expect_status "$status" 0 "the checkout carries the tree"
  expect_contains "$out" "read out of the tree, not remembered" "the checkout carries the tree"
  expect_absent "$out" "the checkout does not carry the Fugaro tree" \
    "the checkout carries the tree: the checkout is the source, not the fixture tree"
  [ -f "$dir/checkout/internal/backend/batch/run.go" ] || fail "the checkout carries the tree: the fixture is wrong"
}

test_part_c_a_tree_without_run_go_is_a_fail() {
  setup_case c-notree none
  run124 --part c
  expect_status "$status" 1 "no run.go anywhere"
  expect_fact exit-code FAIL "no run.go anywhere"
}

test_part_c_two_vms_refuse_to_be_deleted() {
  setup_case c-two
  export FAKE124_STATE_SEQ="RUNNING" FAKE124_RUNVMS='fugaro-run-vm	us-east5-a
someone-elses-vm	us-east5-b'
  run124 --part c
  expect_status "$status" 1 "two VMs carrying the label value"
  expect_fact reclaim FAIL "two VMs carrying the label value"
  # Which row refused, and why: the in-flight step is the one that sees the two,
  # so the reclaim step is never taken -- a different reason from the guard's
  # own, and the two cases below this one tell them apart.
  expect_contains "$out" "FACT in-flight: FAIL — the job " \
    "two VMs carrying the label value: the row that refuses is the in-flight one"
  expect_contains "$out" "2 VM(s) carry its label value" \
    "two VMs carrying the label value: the in-flight row's own count"
  expect_contains "$out" "FACT reclaim: FAIL — not taken:" \
    "two VMs carrying the label value: the reclaim row says it was never taken"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "instances delete" "two VMs: nothing was deleted"
}

test_part_c_a_second_vm_at_the_delete_is_refused() {
  # The guard that makes the delete safe is the RECLAIM step's own read: the
  # in-flight row saw exactly one VM, and a second one carrying the job's own
  # label value by the time of the delete is what the guard has to catch. The
  # fake answers the two reads from a sequence, so this case really does reach
  # the guard's refusal branch -- the case above refuses a step earlier and
  # would stay green if the guard were dropped.
  setup_case c-two-at-reclaim
  export FAKE124_RUNS_FIRST=same
  export FAKE124_STATE_SEQ="RUNNING:FAILED"
  export FAKE124_RUNVMS_SEQ='fugaro-run-vm	us-east5-a;fugaro-run-vm	us-east5-a|someone-elses-vm	us-east5-b'
  run124 --part c
  expect_status "$status" 1 "a second VM at the delete"
  expect_contains "$out" "FACT exit-code: INFO" \
    "a second VM at the delete: the run went through the tree read before the selection"
  expect_fact in-flight PASS "a second VM at the delete"
  expect_contains "$out" "FACT reclaim: FAIL — the selection found 2 VM(s) carrying the job's own label value (${LABELV})" \
    "a second VM at the delete: the guard's own reason, with the count and the label value it found them under"
  expect_contains "$out" "Nothing was deleted" \
    "a second VM at the delete: the row says the refusal deleted nothing"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "instances delete" "a second VM at the delete: no delete"
}

test_part_c_a_job_that_never_runs_is_not_deleted() {
  setup_case c-queued
  export FAKE124_STATE_SEQ="QUEUED"
  run124 --part c
  expect_status "$status" 1 "a job stuck in QUEUED"
  expect_fact in-flight FAIL "a job stuck in QUEUED"
  expect_contains "$out" "the reaper's business" "a job stuck in QUEUED: the row says whose finding it is"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "instances delete" "a job stuck in QUEUED: nothing was deleted"
}

test_part_c_a_job_that_ends_succeeded_is_a_fail() {
  setup_case c-succeeded
  export FAKE124_STATE_SEQ="RUNNING:SUCCEEDED"
  run124 --part c
  expect_status "$status" 1 "a job that ended SUCCEEDED"
  expect_fact terminal FAIL "a job that ended SUCCEEDED"
}

test_part_c_a_missing_retry_count_is_a_fail() {
  setup_case c-retry
  export FAKE124_STATE_SEQ="RUNNING:FAILED" FAKE124_RETRY=absent
  run124 --part c
  expect_status "$status" 1 "no maxRetryCount"
  expect_fact terminal FAIL "no maxRetryCount"
  expect_contains "$out" "ForceSendFields" "no maxRetryCount: the row says why a zero is written with it"
}

test_part_c_a_reaper_report_naming_the_checks_own_vm_is_a_fail() {
  setup_case c-reaper
  export FAKE124_STATE_SEQ="RUNNING:FAILED" \
    FAKE124_REAPER='reaped projects/fugaro-dev/locations/us-east5: queue deadline 24h2m0s, run budget 24h17m0s, max age 48h19m0s
deleted VM fugaro-run-vm (fugaro_run='"$LABELV"' answers to no job that is still running, and the VM is 40 minutes old)'
  run124 --part c
  expect_status "$status" 1 "the reaper naming the check's own VM"
  expect_fact reaper FAIL "the reaper naming the check's own VM"
}

test_part_c_a_launch_that_is_refused_is_a_fail() {
  setup_case c-nolaunch
  export FAKE124_RUN_EXIT=1
  run124 --part c
  expect_status "$status" 1 "a refused launch"
  expect_fact launch FAIL "a refused launch"
  expect_absent "$(cat "$dir/state/gcloud.calls")" "instances delete" "a refused launch: nothing was deleted"
}

test_part_c_dry_run_reads_the_tree_and_no_cloud() {
  setup_case c-dry
  run124 --part c --dry-run
  expect_status "$status" 0 "part c dry run"
  expect_no_cloud "part c dry run"
  expect_contains "$out" "preemptExitCode = 4242" "part c dry run: the exit code is a local read, so it is made"
  expect_contains "$out" "CHECK 124C RESULT: PASS" "part c dry run: the verdict"
}

# ---------------------------------------------------------------------- 124d
test_part_d_reads_the_spend_and_diffs_the_cleanup() {
  setup_case d-happy
  export FAKE124_RUNS_FIRST=same
  printf '%s\n' "$RUNID  FAILED" >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_JOBS="projects/fugaro-dev/locations/us-east5/jobs/fugaro-124b-job	FAILED
projects/fugaro-dev/locations/us-east5/jobs/fugaro-124c-job	FAILED"
  export FAKE124_RUNS='{"runs":[
    {"run_id":"'"$RUNID"'","title":"check 124b: one task on the Batch backend","status":"succeeded","cost":{"total_usd":3.25,"compute_usd":0.42}},
    {"run_id":"20261114-140000-c002","title":"check 124c: the preemption","status":"failed","cost":{"total_usd":1.75,"compute_usd":0.31}}
  ]}'
  run124 --part d
  expect_status "$status" 0 "part d"
  expect_block_keys owner installation checkout belong workdir xtrace spend diff whole restore "part d"
  expect_fact spend PASS "part d"
  expect_contains "$out" "5.0000 USD for the whole check" "part d: the total the rows add up to"
  expect_contains "$out" "running: none" "part d: no run in flight, and no reserve left"
  expect_fact diff PASS "part d"
  expect_contains "$out" "are both EMPTY" "part d: the instances and templates diffs are empty"
  expect_fact whole PASS "part d"
  expect_contains "$out" "NOTHING was torn down" "part d: the sentence the absence of a command cannot show"
  expect_contains "$out" "CHECK 124D RESULT: PASS" "part d: the verdict"
}

test_part_d_without_the_before_state_is_a_fail() {
  setup_case d-nobefore
  run124 --part d
  expect_status "$status" 1 "no before-state"
  expect_fact diff FAIL "no before-state"
  expect_contains "$out" "run --part a first" "no before-state: the row names the step that writes it"
}

test_part_d_a_leaked_vm_is_a_fail() {
  setup_case d-vm
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_INSTANCES="fugaro-leaked-vm"
  run124 --part d
  expect_status "$status" 1 "a leaked VM"
  expect_fact diff FAIL "a leaked VM"
  expect_contains "$out" "the INSTANCES diff is not empty" "a leaked VM: the row says which diff"
}

test_part_d_a_job_still_running_is_a_fail() {
  setup_case d-running
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_JOBS="projects/fugaro-dev/locations/us-east5/jobs/fugaro-124b-job	RUNNING"
  run124 --part d
  expect_status "$status" 1 "a job still running"
  expect_fact diff FAIL "a job still running"
}

test_part_d_an_instance_template_that_is_someone_elses_is_a_fail() {
  setup_case d-template
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_TEMPLATES="fugaro-preload-deadbeef"
  run124 --part d
  expect_status "$status" 1 "an instance template appeared"
  expect_contains "$out" "the TEMPLATES diff is not empty" "an instance template appeared"
}

test_part_d_a_vm_read_that_never_answered_is_a_fail() {
  # The diff row is what says nothing is orphaned, so an empty list from a read
  # that FAILED is not an empty diff: against 124a's before-state, which is
  # empty in the ordinary case, the two would be indistinguishable and the row
  # would report a PASS from a read that never answered.
  setup_case d-vm-read
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_INSTANCES=fails
  run124 --part d
  expect_status "$status" 1 "a VM read that never answered"
  expect_fact diff FAIL "a VM read that never answered"
  expect_contains "$out" "the VMs could not be listed now" \
    "a VM read that never answered: the row names the read, and why an empty answer is not a reading"
  expect_absent "$out" "are both EMPTY" "a VM read that never answered: no PASS claims the diffs are empty"
}

test_part_d_a_template_read_that_never_answered_is_a_fail() {
  setup_case d-template-read
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_TEMPLATES=fails
  run124 --part d
  expect_status "$status" 1 "a template read that never answered"
  expect_fact diff FAIL "a template read that never answered"
  expect_contains "$out" "the instance templates could not be listed now" \
    "a template read that never answered: the row names the read"
  expect_absent "$out" "are both EMPTY" "a template read that never answered: no PASS claims the diffs are empty"
}

test_part_d_a_new_batch_warning_is_a_fail() {
  setup_case d-warning
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_DOCTOR='warning batch-log-sink: the sink is gone
fugaro doctor: 12 checks, 1 warning'
  run124 --part d
  expect_status "$status" 1 "a new batch warning"
  expect_fact whole FAIL "a new batch warning"
}

test_part_d_a_missing_preload_image_is_a_fail() {
  setup_case d-noimage
  : >"$dir/work/jobs-before.txt"
  : >"$dir/work/instances-before.txt"
  : >"$dir/work/templates-before.txt"
  export FAKE124_IMAGES=-
  run124 --part d
  expect_status "$status" 1 "no preload image"
  expect_fact whole FAIL "no preload image"
}

test_part_d_dry_run_touches_no_cloud() {
  setup_case d-dry
  run124 --part d --dry-run
  expect_status "$status" 0 "part d dry run"
  expect_no_cloud "part d dry run"
  expect_contains "$out" "CHECK 124D RESULT: PASS" "part d dry run: the verdict"
}

# ------------------------------------------------------------------- guards
test_the_owner_marker_is_required() {
  setup_case g-owner
  RUN124_OWNER=no
  run124 --part a
  expect_status "$status" 2 "no owner marker"
  expect_fact owner FAIL "no owner marker"
  expect_contains "$out" "no agent performs any step" "no owner marker: the refusal says why"
  expect_no_cloud "no owner marker"
}

test_another_checkout_is_refused() {
  setup_case g-origin default git@github.com:acme/webapp.git
  run124 --part a
  expect_status "$status" 2 "another repository"
  expect_fact aborted FAIL "another repository"
  expect_contains "$out" "is not the sandbox" "another repository: the refusal names the origin it read"
  expect_no_cloud "another repository"
}

test_a_checkout_with_no_origin_is_refused() {
  setup_case g-noorigin default none
  run124 --part a
  expect_status "$status" 2 "no origin"
  expect_fact aborted FAIL "no origin"
  expect_contains "$out" "no origin remote" "no origin: the refusal says so"
}

test_the_belong_project_is_refused() {
  setup_case g-belong
  RUN124_ENV="CLOUDSDK_CORE_PROJECT=belong-prod"
  run124 --part a
  expect_status "$status" 2 "the owner's own project"
  expect_fact belong FAIL "the owner's own project"
  # The refusal itself names belong, and says so: what must not name it is any
  # command the run made.
  expect_absent "$(cat "$dir/state/gcloud.calls" 2>/dev/null || true)" "belong" "the owner's own project: no gcloud call named it"
  expect_absent "$(cat "$dir/state/fugaro.calls" 2>/dev/null || true)" "belong" "the owner's own project: no fugaro call named it"
}

test_the_installation_line_is_the_stop() {
  setup_case g-install
  export FAKE124_DOCTOR_ALL='project: something-else (GCP another-project, us-central1)'
  run124 --part a
  expect_status "$status" 2 "another installation"
  expect_fact installation FAIL "another installation"
  expect_no_cloud_after_doctor "another installation"
}

# expect_no_cloud_after_doctor: the installation guard is the one that reads the
# cloud before it decides, so the case says instead that nothing AFTER it ran.
expect_no_cloud_after_doctor() { # what
  local g
  g=$(cat "$dir/state/gcloud.calls" 2>/dev/null || true)
  [ -z "$g" ] || fail "$1: it went on to gcloud after doctor refused the installation: $(printf '%s' "$g" | head -c 300)"
  expect_absent "$(cat "$dir/state/fugaro.calls")" "setup" "$1: it went on to setup after doctor refused the installation"
}

test_xtrace_is_refused() {
  setup_case g-xtrace
  status=0
  ( cd "$dir/checkout" && exec env TMPDIR="$dir/tmp" FAKE124_DIR="$dir/state" HOME="$dir/home" \
    CHK124_WORK="$dir/work" CHK124_OWNER_RUN=yes FAKE124_LABELV="$LABELV" \
    PATH="$dir/bin:$PATH" bash -x "$dir/tree/scripts/live-checks/check124.sh" --part a ) \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace"
  expect_fact xtrace FAIL "xtrace"
  expect_no_cloud "xtrace"
}

test_the_usage_is_the_usage() {
  setup_case g-usage
  run124 --help
  expect_status "$status" 0 "--help"
  expect_contains "$dir/err$out" "--part a|b|c|d" "--help: the part list"
  run124 --part e
  expect_status "$status" 2 "an unknown part"
  run124
  expect_status "$status" 2 "no part at all"
  run124 --part a --nope
  expect_status "$status" 2 "an unknown flag"
  expect_no_cloud "usage"
}

test_a_stopped_run_still_prints_the_whole_block() {
  setup_case g-block
  RUN124_OWNER=no
  run124 --part c
  expect_status "$status" 2 "a stopped run"
  expect_contains "$out" "CHECK 124C RESULT: FAIL" "a stopped run: the verdict is FAIL, not an absence"
  expect_block_keys owner installation checkout belong workdir xtrace exit-code launch in-flight reclaim terminal reaper restore "a stopped run"
}

# ------------------------------------------------------------------- runner
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

t test_part_a_reads_the_plan_and_the_two_readbacks
t test_part_a_a_compute_address_in_the_plan_only_output_is_a_fail
t test_part_a_a_refused_plan_only_is_a_fail
t test_part_a_a_batch_warning_is_a_fail
t test_part_a_a_strict_exit_without_a_batch_warning_is_a_reading
t test_part_a_a_doctor_with_no_batch_line_is_a_fail
t test_part_a_the_old_validate_refusal_is_a_fail
t test_part_a_dry_run_touches_no_cloud
t test_part_b_runs_one_task_to_its_ready_pull_request
t test_part_b_a_run_that_ends_a_draft_is_a_fail
t test_part_b_a_backend_that_is_not_selected_is_a_fail
t test_part_b_an_unselected_project_is_a_fail
t test_part_b_a_repository_backend_key_is_a_fail
t test_part_b_a_job_on_a_default_network_is_a_fail
t test_part_b_a_spot_job_is_a_fail
t test_part_b_without_a_token_file_nothing_is_deleted
t test_part_b_a_refused_decline_leaves_the_branch_alone
t test_part_b_dry_run_touches_no_cloud
t test_part_c_forces_a_preemption_and_reads_the_tree
t test_part_c_an_exit_code_the_tree_does_not_carry_is_a_fail
t test_part_c_the_checkout_is_the_tree_when_it_carries_it
t test_part_c_a_tree_without_run_go_is_a_fail
t test_part_c_two_vms_refuse_to_be_deleted
t test_part_c_a_second_vm_at_the_delete_is_refused
t test_part_c_a_job_that_never_runs_is_not_deleted
t test_part_c_a_job_that_ends_succeeded_is_a_fail
t test_part_c_a_missing_retry_count_is_a_fail
t test_part_c_a_reaper_report_naming_the_checks_own_vm_is_a_fail
t test_part_c_a_launch_that_is_refused_is_a_fail
t test_part_c_dry_run_reads_the_tree_and_no_cloud
t test_part_d_reads_the_spend_and_diffs_the_cleanup
t test_part_d_without_the_before_state_is_a_fail
t test_part_d_a_leaked_vm_is_a_fail
t test_part_d_a_job_still_running_is_a_fail
t test_part_d_an_instance_template_that_is_someone_elses_is_a_fail
t test_part_d_a_vm_read_that_never_answered_is_a_fail
t test_part_d_a_template_read_that_never_answered_is_a_fail
t test_part_d_a_new_batch_warning_is_a_fail
t test_part_d_a_missing_preload_image_is_a_fail
t test_part_d_dry_run_touches_no_cloud
t test_the_owner_marker_is_required
t test_another_checkout_is_refused
t test_a_checkout_with_no_origin_is_refused
t test_the_belong_project_is_refused
t test_the_installation_line_is_the_stop
t test_xtrace_is_refused
t test_the_usage_is_the_usage
t test_a_stopped_run_still_prints_the_whole_block

if [ "$failures" -ne 0 ]; then
  echo "check124_test: $failures failure(s)" >&2
  exit 1
fi
echo "check124_test: all tests passed" >&2
