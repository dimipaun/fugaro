#!/usr/bin/env bash
# Tests for scripts/live-checks/check-830.sh, with a fake `gcloud` on PATH:
# NOTHING here reaches a cloud, a project, a VM or a log, no agent runs a live
# command, and the fake records every call it is given so a case can assert what
# the check did and what it did NOT do. Check 830 creates real Batch jobs, a real
# VM and real log entries in `fugaro-dev`, which is why every reading it takes is
# measured here against a model of the platform rather than by running it.
#
# The fake is a small model of Batch and Compute, driven by files a case writes:
#
#   cfg            key=value knobs — submit_fail, delete_fail, delete_lists,
#                  create_fail, create_refuse_reserved, create_refuse_blind,
#                  refuse_reuse, hide_vms
#   uid.map        job name <TAB> its UID, which is what its logs are read by
#   state.map      job name <TAB> its state
#   vmjob.map      job name <TAB> the instance it ran on
#   vm.map         instance name <TAB> the file `compute instances describe`
#                  answers with
#   instances.base the instances that were there before the check ran
#   jobs           the jobs that exist right now (submit adds; delete removes,
#                  after `delete_lists` more list calls when that is set, which
#                  is how a Batch delete behaves: accepted at once, finished
#                  later, the job listed as DELETION_IN_PROGRESS in between)
#   created        the instances made by hand right now (create adds, delete
#                  removes), which is where O8's plain VM lives
#   mades/<name>.json  the object Compute made out of a `create` call: the labels
#                  and metadata items that call carried, which is what a
#                  `describe` of a hand-made VM answers with
#   submits/       every job configuration the check submitted, kept so a case
#                  can read the body it sent — including the marker's value,
#                  which the test asserts never reaches the block
#   log-<uid>.json the entries that job's task logs answer with
#   log-value.json the entries a search of the whole project answers with
#   audit.json     the entries the CreateJob audit filter answers with
#
# `@MARKER@` in any of those files stands for the marker's value, which the fake
# reads out of the job configuration the check submitted — the test never has to
# know a value the check generated.
#
# The tree is a fixture too: the check reads the log filter out of
# internal/backend/batch/logs.go, the label vocabulary out of
# internal/backend/gcp/names.go and the wrapped variable's name out of
# docs/batch-run-identity/, and two cases bend those files to prove the reads
# are reads.
#
# Usage: bash scripts/live-checks/check-830_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check-830_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check-830_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-830.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK830_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK830_TEST_HEAD:-1800}")" ;;
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
# expect_verdict: the row's verdict alone.
expect_verdict() { # key want what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  case "$row" in
  "FACT $1: $2 "*) ;;
  *) fail "$3: expected FACT $1 to be $2; got: ${row:-<no row>}" ;;
  esac
}

# The names the check uses, spelled out here so a case can name the job it means
# rather than matching a stem.
job_a=fugaro-chk830-probe
job_b=fugaro-chk830-forge
handmade=fugaro-chk830-probe-handmade
uid_a=77001
uid_b=77002
# The probe's own VM on the platform the default fixture models: Batch's name
# for it carries no part of the job's identity, so its name is not a field the
# link could rest on. `vm_job_named` is the same VM on the other platform, the
# one the `name-forges` bend models, where Batch names the VM after the job.
vm_a=fugaro-chk830-vm-abc123
vm_job_named=fugaro-chk830-probe-abc123
vm_b=fugaro-chk830-forge-def456
zone=us-east5-a
instance_id=9900112233

# --------------------------------------------------------------------- the fake
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check-830_test.sh only.

It answers the calls Check 830 makes, out of the files its case wrote under
FAKE830_DIR, and appends every invocation to FAKE830_DIR/gcloud.calls before it
answers — so a case can assert the calls the check made, in order, and the ones
it never made. Anything it does not know how to answer is an error on purpose:
an unhandled call is a call a case must model rather than one the fake hides.
"""
import json
import os
import re
import shutil
import sys

state = os.environ["FAKE830_DIR"]


def cfg(key, default=""):
    try:
        with open(os.path.join(state, "cfg")) as fh:
            for line in fh:
                line = line.strip()
                if line.startswith(key + "="):
                    return line[len(key) + 1:]
    except OSError:
        pass
    return default


def rows(name):
    out = []
    try:
        with open(os.path.join(state, name)) as fh:
            for line in fh:
                if line.strip():
                    out.append(line.rstrip("\n").split("\t"))
    except OSError:
        pass
    return out


def read_list(name):
    try:
        with open(os.path.join(state, name)) as fh:
            return [l.strip() for l in fh if l.strip()]
    except OSError:
        return []


def write_list(name, items):
    with open(os.path.join(state, name), "w") as fh:
        for item in items:
            fh.write(item + "\n")


def lookup(name, key, default=""):
    for row in rows(name):
        if row[0] == key:
            return row[1] if len(row) > 1 else default
    return default


def counts(name):
    out = {}
    for row in rows(name):
        try:
            out[row[0]] = int(row[1])
        except (IndexError, ValueError):
            continue
    return out


def write_counts(name, m):
    with open(os.path.join(state, name), "w") as fh:
        for k in sorted(m):
            fh.write("%s\t%d\n" % (k, m[k]))


def canned(name):
    try:
        with open(os.path.join(state, name)) as fh:
            body = fh.read()
    except OSError:
        return "[]"
    return body.replace("@MARKER@", marker_value())


def marker_value():
    d = os.path.join(state, "submits")
    if os.path.isdir(d):
        for f in sorted(os.listdir(d)):
            try:
                with open(os.path.join(d, f)) as fh:
                    body = fh.read()
            except OSError:
                continue
            m = re.search(r'"FUGARO_BUDGET_TOKEN_WRAPPED"\s*:\s*"([^"]+)"', body)
            if m:
                return m.group(1)
    return ""


def die(msg):
    sys.stderr.write(msg + "\n")
    sys.exit(1)


def flag(name):
    for a in argv:
        if a.startswith(name + "="):
            return a.split("=", 1)[1]
    return ""


def present_instance(name):
    """An instance is there because a job of ours is running on it, or because
    the check made one by hand and has not deleted it yet."""
    if name in read_list("created"):
        return True
    jobs = read_list("jobs")
    return any(len(row) > 1 and row[1] == name and row[0] in jobs for row in rows("vmjob.map"))


def vm_of_job(job):
    return lookup("vmjob.map", job)


def describe_file(name):
    return lookup("vm.map", name)


argv = sys.argv[1:]
with open(os.path.join(state, "gcloud.calls"), "a") as fh:
    fh.write(" ".join(argv) + "\n")

if argv[:2] == ["compute", "instances"]:
    op = argv[2] if len(argv) > 2 else ""
    name = argv[3] if len(argv) > 3 else ""
    if op == "list":
        want_zone = "," in flag("--format")
        names = list(read_list("instances.base"))
        if cfg("hide_vms") != "yes":
            for job in read_list("jobs"):
                vm = vm_of_job(job)
                if vm and vm not in names:
                    names.append(vm)
        for n in names:
            print("%s\t%s" % (n, cfg("zone", "us-east5-a")) if want_zone else n)
        sys.exit(0)
    if op == "describe":
        # An instance made by hand is the object Compute made out of the create
        # call: the same labels and metadata items the call carried, exactly as
        # the platform would report them. That is what makes the plain VM's row
        # a measurement of what a launcher can write rather than of what this
        # file chose to write.
        path = os.path.join(state, "mades", name + ".json")
        if not os.path.exists(path):
            f = describe_file(name)
            path = os.path.join(state, f) if f else ""
        if not path or not os.path.exists(path) or not present_instance(name):
            die("ERROR: The resource '%s' was not found" % name)
        if "--format=json" in argv:
            with open(path) as fh:
                sys.stdout.write(fh.read().replace("@MARKER@", marker_value()))
            sys.exit(0)
        fmt = flag("--format")
        if not fmt:
            print("name: %s" % name)
            sys.exit(0)
        if "machineType" in fmt:
            print("e2-standard-4")
            sys.exit(0)
        if "serviceAccounts" in fmt:
            print("1234-compute@developer.gserviceaccount.com")
            sys.exit(0)
        die("fake gcloud: unhandled describe format: %s" % fmt)
    if op == "create":
        reserved = cfg("reserved_key")
        if cfg("create_refuse_blind") == "yes":
            # A refusal that names no field of the request: the check may drop a
            # field from a second attempt only when the platform says which one
            # it will not take, so this has to end the row rather than make a
            # guess at what to leave out.
            die("ERROR: INVALID_ARGUMENT: the request was refused by the platform")
        if cfg("create_refuse_reserved") == "yes" and reserved and reserved in flag("--labels"):
            die("ERROR: Invalid value for field 'resource.labels.%s': the key is reserved" % reserved)
        if cfg("create_fail") == "yes":
            die("ERROR: QUOTA_EXCEEDED: the zone has no capacity for this request")
        zone = cfg("zone", "us-east5-a")


        def pairs(arg):
            out = {}
            for part in flag(arg).split(","):
                if "=" in part:
                    k, v = part.split("=", 1)
                    out[k] = v
            return out


        os.makedirs(os.path.join(state, "mades"), exist_ok=True)
        obj = {
            "name": name,
            "id": "9900200001",
            "zone": "https://www.googleapis.com/compute/v1/projects/fugaro-dev/zones/%s" % zone,
            "machineType": "https://www.googleapis.com/compute/v1/projects/fugaro-dev/zones/%s/machineTypes/%s"
            % (zone, flag("--machine-type") or "e2-standard-4"),
            "labels": pairs("--labels"),
            "metadata": {
                "kind": "compute#metadata",
                "items": [{"key": k, "value": v} for k, v in pairs("--metadata").items()],
            },
            "serviceAccounts": [{"email": "1234-compute@developer.gserviceaccount.com"}],
        }
        with open(os.path.join(state, "mades", name + ".json"), "w") as fh:
            json.dump(obj, fh, indent=1)
            fh.write("\n")
        created = read_list("created")
        if name not in created:
            created.append(name)
        write_list("created", created)
        print(
            "Created [https://www.googleapis.com/compute/v1/projects/p/zones/%s/instances/%s]."
            % (zone, name)
        )
        sys.exit(0)
    if op == "delete":
        if cfg("delete_fail") == "yes":
            die("ERROR: PERMISSION_DENIED: this account cannot delete instances")
        write_list("created", [n for n in read_list("created") if n != name])
        try:
            os.remove(os.path.join(state, "mades", name + ".json"))
        except OSError:
            pass
        print("Deleted [%s]." % name)
        sys.exit(0)
    if op == "get-serial-port-output":
        # What the VM's serial console holds. A case that wants a specific tail
        # writes serial/<name>.txt; without one the console answered with the
        # boot line a Batch VM really prints. It is a reading the check takes
        # when no line of the container's own arrived (probe-diag).
        path = os.path.join(state, "serial", name + ".txt")
        try:
            with open(path) as fh:
                sys.stdout.write(fh.read())
        except OSError:
            sys.stdout.write("Nov 25 03:51:28 batch-vm systemd[1]: Started Batch task container.\n")
        sys.exit(0)
    die("fake gcloud: unhandled compute call: %s" % " ".join(argv))

if argv[:2] == ["batch", "jobs"]:
    op = argv[2] if len(argv) > 2 else ""
    name = argv[3] if len(argv) > 3 else ""
    if op == "submit":
        if cfg("submit_fail") == "yes":
            die("ERROR: PERMISSION_DENIED: the caller does not have batch.jobs.create")
        refuse = int(cfg("refuse_reuse", "0") or "0")
        if name == cfg("job_a") and os.path.exists(os.path.join(state, "deleted_a")):
            tries_path = os.path.join(state, "reuse_tries")
            tries = int(open(tries_path).read().strip() or "0") if os.path.exists(tries_path) else 0
            if tries < refuse:
                with open(tries_path, "w") as fh:
                    fh.write(str(tries + 1) + "\n")
                die("ERROR: ALREADY_EXISTS: job '%s' already exists" % name)
        d = os.path.join(state, "submits")
        os.makedirs(d, exist_ok=True)
        n = len(os.listdir(d))
        src = flag("--config")
        shutil.copyfile(src, os.path.join(d, "%02d-%s.json" % (n, name)))
        jobs = read_list("jobs")
        if name not in jobs:
            jobs.append(name)
        write_list("jobs", jobs)
        print("Job %s submitted." % name)
        sys.exit(0)
    if op == "describe":
        fmt = flag("--format")
        if fmt == "json":
            obj = {}
            try:
                with open(os.path.join(state, "jobjson", name + ".json")) as fh:
                    obj = json.loads(fh.read().replace("@MARKER@", marker_value()))
            except (OSError, ValueError):
                obj = {}
            # diag/<job>.json is what the platform says about a job that ran:
            # its own state and status events, and each task's state with the
            # events it carries — the task's exit code among them. It replaces
            # the object's own status and adds its task groups, which is what a
            # run whose lines never arrived is read through (probe-diag) and
            # what the second channel is read out of (probe-exit).
            try:
                with open(os.path.join(state, "diag", name + ".json")) as fh:
                    obj.update(json.loads(fh.read()))
            except (OSError, ValueError):
                pass
            # A job's own labels are the launcher's: Compute and Batch both
            # report back what the submit carried, so the object this answers
            # with carries the labels of the config that was submitted for that
            # job. Without that, the fields a launcher writes on the job's own
            # object could not be judged by anything.
            d = os.path.join(state, "submits")
            if os.path.isdir(d):
                for f in sorted(os.listdir(d)):
                    if not f.endswith("-" + name + ".json"):
                        continue
                    try:
                        with open(os.path.join(d, f)) as fh:
                            cfg_job = json.load(fh)
                    except (OSError, ValueError):
                        continue
                    labels = cfg_job.get("labels") or {}
                    if labels:
                        merged = dict(obj.get("labels") or {})
                        merged.update(labels)
                        obj["labels"] = merged
            sys.stdout.write(json.dumps(obj) + "\n")
            sys.exit(0)
        if "uid" in fmt:
            print(lookup("uid.map", name, "uid-" + name))
            sys.exit(0)
        if "status.state" in fmt:
            print(lookup("state.map", name, "QUEUED"))
            sys.exit(0)
        if "createTime" in fmt:
            print("2026-11-20T12:00:00Z")
            sys.exit(0)
        die("fake gcloud: unhandled describe format: %s" % fmt)
    if op == "list":
        # A job whose delete the platform has accepted is still listed until it
        # has finished with it, so with `delete_lists` set the delete below
        # leaves the name here for that many more list calls — the state a Batch
        # job is in as DELETION_IN_PROGRESS, and the reason the sweep cannot
        # read one list and call what it finds left behind.
        held = counts("deleting")
        for job in list(held):
            held[job] -= 1
            if held[job] <= 0:
                del held[job]
                write_list("jobs", [j for j in read_list("jobs") if j != job])
        write_counts("deleting", held)
        for job in read_list("jobs"):
            print(job.split("/")[-1])
        sys.exit(0)
    if op == "delete":
        if cfg("delete_fail") == "yes":
            die("ERROR: PERMISSION_DENIED: this account cannot delete jobs")
        linger = int(cfg("delete_lists", "0") or "0")
        if linger > 0:
            held = counts("deleting")
            held[name] = linger
            write_counts("deleting", held)
        else:
            write_list("jobs", [j for j in read_list("jobs") if j != name])
        if name == cfg("job_a"):
            open(os.path.join(state, "deleted_a"), "w").write("yes\n")
        print("Job %s deleted." % name)
        sys.exit(0)
    die("fake gcloud: unhandled batch call: %s" % " ".join(argv))

if argv[:2] == ["logging", "read"]:
    f = argv[2] if len(argv) > 2 else ""
    m = re.search(r'labels\.job_uid="([^"]*)"', f)
    uid = m.group(1) if m else ""
    if "CreateJob" in f:
        print(canned("audit.json"))
    elif "resource.type=" in f:
        # The broader reads probe-diag makes when no line of the container's own
        # arrived: the same query under the job's own resource type, by the
        # runner's label and then by the uid alone. They answer out of their own
        # files so a case can model lines that landed under a log name the
        # runner does not read, or under a label that is not labels.job_uid.
        if "labels.job_uid=" in f:
            print(canned("log-res-%s.json" % uid))
        else:
            quoted = re.findall(r'"([^"]+)"', f)
            print(canned("log-any-%s.json" % (quoted[-1] if quoted else "")))
    elif "batch_task_logs" in f or "labels.job_uid" in f:
        # log_late=N is the platform where a job's lines land after a delay: the
        # first N reads of that job's task logs answer with nothing and the
        # reads after them answer with the lines. The wait is what this models,
        # so a case that sets it gives the check a non-zero bound.
        late = int(cfg("log_late", "0") or "0")
        seen = 0
        path = os.path.join(state, "loglate-%s" % uid)
        if late > 0 and os.path.exists(path):
            seen = int(open(path).read().strip() or "0")
        if late > 0 and seen < late:
            with open(path, "w") as fh:
                fh.write(str(seen + 1) + "\n")
            print("[]")
        else:
            print(canned("log-%s.json" % uid))
    elif f.startswith('"'):
        print(canned("log-value.json"))
    else:
        die("fake gcloud: unhandled log filter: %s" % f)
    sys.exit(0)

die("fake gcloud: unexpected call: %s" % " ".join(argv))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}
# --------------------------------------------------------------- the fixture
# write_tree: a checkout the script believes it lives in. It carries exactly what
# the check reads out of the real one, and nothing else: the log filter
# (internal/backend/batch/logs.go), the label vocabulary
# (internal/backend/gcp/names.go) and the wrapped variable's name
# (docs/batch-run-identity/). A case bends one of the three to prove the read is
# a read rather than a copy.
write_tree() { # write_tree DIR [bend]
  local dir=$1 bend=${2:-}
  mkdir -p "$dir/scripts/live-checks" "$dir/internal/backend/batch" \
    "$dir/internal/backend/gcp" "$dir/docs/batch-run-identity"
  cat >"$dir/internal/backend/batch/logs.go" <<'GO_EOF'
package batch

// The filter lib/google documents for one Batch job's task logs. Its two
// halves are what Check 830 reads out of the tree.
func jobFilter(uid string) string {
	return `labels.job_uid=` + uid + ` AND logName="projects/p/logs/batch_task_logs"`
}
GO_EOF
  if [ "$bend" = no-filter ]; then
    cat >"$dir/internal/backend/batch/logs.go" <<'GO_EOF'
package batch

func jobFilter(uid string) string { return `resource.labels.job_id=` + uid }
GO_EOF
  fi
  cat >"$dir/internal/backend/gcp/names.go" <<'GO_EOF'
package gcp

const (
	LabelManaged = "fugaro"
	ManagedValue = "managed"
	LabelRepo    = "fugaro_repo"
)
GO_EOF
  cat >"$dir/docs/batch-run-identity/implementation-plan.md" <<'MD_EOF'
# A run on Cloud Batch gets its identity: implementation plan

The blob travels as FUGARO_BUDGET_TOKEN_WRAPPED, and FUGARO_BUDGET_TOKEN is
never set on a Batch job (R5).
MD_EOF
  if [ "$bend" = no-docs ]; then
    rm -f "$dir/docs/batch-run-identity/implementation-plan.md"
  fi
}

# write_log: the entries one job's task logs answer with — the container's own
# C830 lines, which is where the check reads the metadata, the token's claims and
# the marker's control. MODE bends the token: full, noclaims (the token arrives
# without the instance claims) or notoken (the metadata server gave no token).
write_log() { # write_log FILE UID VM ZONE IID CONTROL ATTRS MODE [EXTRA]
  python3 - "$@" <<'PY'
import json, sys

out, uid, vm, zone, iid, control, attrs, mode = sys.argv[1:9]
extra = sys.argv[9] if len(sys.argv) > 9 else ""
md = "projects/123456789/zones/%s" % zone
lines = [
    "C830 start host=%s" % vm,
    "C830 metadata instance/name=%s" % vm,
    "C830 metadata instance/id=%s" % iid,
    "C830 metadata instance/zone=%s" % md,
    "C830 metadata instance/hostname=%s" % vm,
    "C830 project fugaro-dev",
    "C830 attrs %s" % attrs,
    "C830 sa 1234-compute@developer.gserviceaccount.com",
]
if mode != "notoken":
    lines += [
        "C830 token len=987",
        "C830 token aud=https://fugaro-chk830.invalid/",
        "C830 token iss=https://accounts.google.com",
        "C830 token claims=aud,exp,google.compute_engine,iat,iss,sub",
    ]
    if mode == "full":
        lines += [
            "C830 token instance_id=%s" % iid,
            "C830 token instance_name=%s" % vm,
            "C830 token project_id=fugaro-dev",
            "C830 token zone=%s" % zone,
            "C830 token life=3600s",
        ]
    else:
        for claim in ("instance_id", "instance_name", "project_id", "zone"):
            lines.append("C830 token %s=<absent>" % claim)
lines.append("C830 env-marker %s" % control)
lines.append("C830 read")
if extra:
    lines.append(extra)
with open(out, "w") as fh:
    json.dump([{"textPayload": line} for line in lines], fh, indent=1)
    fh.write("\n")
PY
}

# write_vm: one instance object as `compute instances describe --format=json`
# answers it. LABELS and META are comma-separated key=value pairs, which is how
# a case writes the fields the link is judged on.
write_vm() { # write_vm FILE NAME IID ZONE LABELS META
  python3 - "$@" <<'PY'
import json, sys

out, name, iid, zone, labels, meta = sys.argv[1:7]


def pairs(s):
    out = {}
    for part in s.split(","):
        if "=" in part:
            k, v = part.split("=", 1)
            out[k] = v
    return out


obj = {
    "name": name,
    "id": iid,
    "zone": "https://www.googleapis.com/compute/v1/projects/fugaro-dev/zones/%s" % zone,
    "machineType": "https://www.googleapis.com/compute/v1/projects/fugaro-dev/zones/%s/machineTypes/e2-standard-4" % zone,
    "labels": pairs(labels),
    "metadata": {
        "kind": "compute#metadata",
        "items": [{"key": k, "value": v} for k, v in pairs(meta).items()],
    },
    "serviceAccounts": [{"email": "1234-compute@developer.gserviceaccount.com"}],
}
with open(out, "w") as fh:
    json.dump(obj, fh, indent=1)
    fh.write("\n")
PY
}

# write_diag: what the platform reports about a job that ran — the job's own
# state and its status events, and its task's state with the events it carries.
# A task-level event of a failed task is where the container's exit code lives
# (TaskExecution.ExitCode), which is the second channel probe-exit reads when no
# line of the container's own came back. It is the `diag/<job>.json` the fake
# merges into that job's own object.
write_diag() { # write_diag DIR JOB STATE TASK_STATE EXIT
  local d=$1 job=$2 st=$3 tstate=$4 code=$5
  mkdir -p "$d/state/diag"
  python3 - "$d/state/diag/$job.json" "$st" "$tstate" "$code" <<'PY'
import json, sys

out, state, tstate, code = sys.argv[1:5]
ev = {"type": "TASK_STATE_CHANGED", "taskState": tstate, "description": "task %s" % tstate}
if code:
    ev["taskExecution"] = {"exitCode": int(code)}
obj = {
    "status": {
        "state": state,
        "statusEvents": [{"type": "JOB_STATE_CHANGED", "description": "job %s" % state}],
    },
    "taskGroups": [{
        "tasks": [{
            "name": "projects/p/locations/us-east5/jobs/x/taskGroups/0/tasks/0",
            "status": {"state": tstate, "statusEvents": [ev]},
        }]
    }],
}
with open(out, "w") as fh:
    json.dump(obj, fh, indent=1)
    fh.write("\n")
PY
}

write_cfg() { # write_cfg DIR key=value ...
  local dir=$1
  shift
  : >"$dir/state/cfg"
  local kv
  for kv in "$@"; do
    printf '%s\n' "$kv" >>"$dir/state/cfg"
  done
}

# vm_a_is: the instance the probe job ran on is named as some other platform
# would name it (a plain name, when the probe's VM is not named after the job at
# all). The maps are what the fake answers `describe` and `instances list` with;
# both have to say the same thing or the check would read a VM that is not there.
vm_a_is() { # vm_a_is DIR NAME FILE
  local d=$1 name=$2 file=$3
  printf '%s\t%s\n' "$job_a" "$name" >"$d/state/vmjob.map"
  printf '%s\t%s\n' "$job_b" "$vm_b" >>"$d/state/vmjob.map"
  printf '%s\t%s\n' "$name" "$file" >"$d/state/vm.map"
  printf '%s\t%s\n' "$vm_b" vm-b.json >>"$d/state/vm.map"
}

# ---------------------------------------------------------------- the harness
dir=""
setup_case() { # setup_case NAME [bend]
  dir=$work/$1
  rm -rf "$dir"
  mkdir -p "$dir/state" "$dir/tmp" "$dir/home"
  write_fake_gcloud "$dir"
  write_tree "$dir/tree" "${2:-}"
  cp "$script" "$dir/tree/scripts/live-checks/check-830.sh"
  write_cfg "$dir" "job_a=$job_a" "zone=$zone"
  out=""
  err=""
  status=0
  RUN_TMPDIR=""
  RUN_OWNER=yes
  BASH_FLAGS=""
}

out=""
err=""
status=0
# The knobs a case turns. RUN_OWNER=no is the owner guard's case and
# BASH_FLAGS=-x the xtrace guard's; every waiting bound is zero, so a case that
# models an absence models it at once rather than in real minutes — except the
# sweep's, which is two seconds: a case whose job the platform keeps listing
# after the delete (delete_lists) is waiting that out within the bound, and two
# seconds is the shortest bound a case can tell a wait from a single read in.
RUN_OWNER=yes
BASH_FLAGS=""
run_check() { # run_check [K=V ...] [-- ARGS...]
  local envs=() args=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  [ "${1:-}" = "--" ] && shift
  args=("$@")
  local base=(
    PATH="$dir/bin:$PATH"
    HOME="$dir/home"
    TMPDIR="${RUN_TMPDIR:-$dir/tmp}"
    FAKE830_DIR="$dir/state"
    CHK830_WAIT_LINES=0
    # probe-exit waits for the task to end before its exit status exists, and a
    # case models a platform whose job has ended by handing the check the job's
    # own object (state and task status), so the bound is zero: the case that
    # models a job that has NOT ended sets it itself where it wants the wait.
    CHK830_WAIT_EXIT=0
    CHK830_LINGER_A=0
    CHK830_LINGER_B=0
    CHK830_WAIT_AUDIT=0
    CHK830_REUSE_BOUND=0
    CHK830_REUSE_POLL=0
    CHK830_SWEEP_BOUND=2
    CHK830_SWEEP_POLL=0
  )
  [ "$RUN_OWNER" = yes ] && base+=(CHK830_OWNER_RUN=yes)
  set +e
  out=$(cd "$dir" && env -i "${base[@]}" "${envs[@]}" \
    bash $BASH_FLAGS "$dir/tree/scripts/live-checks/check-830.sh" ${args[@]+"${args[@]}"} 2>"$dir/stderr")
  status=$?
  set -e
  err=$(cat "$dir/stderr" 2>/dev/null || true)
}

block_line() { # block_line N — one line of the RESULT block, 1-based
  printf '%s\n' "$out" | sed -n "$1p"
}
last_line() { printf '%s\n' "$out" | grep . | tail -n 1; }

calls() { cat "$dir/state/gcloud.calls" 2>/dev/null || true; }
expect_call() { # what
  if ! grep -qF -- "$1" "$dir/state/gcloud.calls" 2>/dev/null; then
    fail "the check never called 'gcloud $1'; calls were: $(head -c 600 "$dir/state/gcloud.calls" 2>/dev/null || echo none)"
  fi
}
expect_no_call() { # what
  if grep -qF -- "$1" "$dir/state/gcloud.calls" 2>/dev/null; then
    fail "the check called 'gcloud $1' where it must not"
  fi
}
expect_no_cloud() { # what
  if [ -e "$dir/state/gcloud.calls" ]; then
    fail "$1: a gcloud call was made where none may be: $(head -c 300 "$dir/state/gcloud.calls")"
  fi
}
# marker_sent: the value the check put in the job's own configuration. A case
# never has to know it; it reads it out of the body the check submitted, which is
# also the only place it may be.
marker_sent() {
  python3 - "$dir/state/submits" <<'PY' 2>/dev/null || true
import os, re, sys
d = sys.argv[1]
for f in sorted(os.listdir(d)):
    body = open(os.path.join(d, f)).read()
    m = re.search(r'"FUGARO_BUDGET_TOKEN_WRAPPED"\s*:\s*"([^"]+)"', body)
    if m:
        print(m.group(1))
        break
PY
}
# submitted_entrypoints: the interpreter every container the check submitted is
# launched with, one per line, read out of the configurations the fake kept. The
# probe's body is POSIX and the image need not carry bash, so the entrypoint and
# the shebang have to agree: a body launched with an interpreter the image does
# not have dies at start with nothing on stdout, which is the failure the first
# run could not tell apart from a line that never reached Cloud Logging.
submitted_entrypoints() {
  python3 - "$dir/state/submits" <<'PY' 2>/dev/null || true
import json, os, sys
d = sys.argv[1]
seen = set()
for f in sorted(os.listdir(d)):
    obj = json.load(open(os.path.join(d, f)))
    for tg in obj.get("taskGroups", []):
        for r in tg.get("taskSpec", {}).get("runnables", []):
            c = r.get("container", {})
            if c.get("commands"):
                seen.add(c.get("entrypoint") or "<none>")
for e in sorted(seen):
    print(e)
PY
}
# submitted_env: one variable of one submitted job's task, read out of the
# configuration the fake kept — the check's own write, and the only place the
# life it gave a container is visible.
submitted_env() { # submitted_env JOB NAME
  python3 - "$dir/state/submits" "$1" "$2" <<'PY' 2>/dev/null || true
import json, os, sys
d, job, name = sys.argv[1:4]
for f in sorted(os.listdir(d)):
    if not f.endswith("-" + job + ".json"):
        continue
    ts = json.load(open(os.path.join(d, f)))["taskGroups"][0]["taskSpec"]
    var = ((ts.get("environment") or {}).get("variables") or {}).get(name)
    if var is not None:
        print(var)
        break
PY
}
# submitted_max_run: the same job's task maxRunDuration, which is computed from
# the container's life rather than from the linger.
submitted_max_run() { # submitted_max_run JOB
  python3 - "$dir/state/submits" "$1" <<'PY' 2>/dev/null || true
import json, os, sys
d, job = sys.argv[1:3]
for f in sorted(os.listdir(d)):
    if not f.endswith("-" + job + ".json"):
        continue
    print(json.load(open(os.path.join(d, f)))["taskGroups"][0]["taskSpec"]["maxRunDuration"])
    break
PY
}
# write_default_case: the platform answers most cases start from. Both jobs ran;
# the probe's VM carries Batch's OWN job-uid label, which is the Google-made fact
# u1-link is about and the field a forge would have to reproduce, and its own
# name carries no part of the job's identity — the `name-forges` bend is the
# platform where Batch names the VM after the job, and where the name is
# therefore a candidate field too. The identity sits in the label alone, and not
# also in the same-named metadata item: Compute refuses that label key on a VM
# made by hand, so a platform whose link lives there alone is the one where O8's
# row can be answered at all, and where it is answered about every field the
# check did write — the `metadata-identity` bend is the other platform, where the
# identity also sits in a metadata item a hand-made VM can carry, and where the
# row must fail on it. The forging job's VM carries its OWN uid in the same
# field, so the forge does not reproduce the probe's identity but the field is
# live (that is what the control counts). The hand-made VM carries the fields the
# check wrote to it, as Compute would give them back. The CreateJob audit record
# carries the probe job's own uid, as the real one does.
write_default_case() { # write_default_case DIR [bend...]
  local d=$1
  shift
  local bend
  write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_a" "$zone" "$instance_id" present "job_uid=$uid_a" full
  write_log "$d/state/log-$uid_b.json" "$uid_b" "$vm_b" "$zone" 9900112234 absent "job_uid=$uid_b" full
  printf '%s\n' other-vm-1 >"$d/state/instances.base"
  printf '%s\t%s\n' "$job_a" "$uid_a" >"$d/state/uid.map"
  printf '%s\t%s\n' "$job_b" "$uid_b" >>"$d/state/uid.map"
  printf '%s\t%s\n' "$job_a" "$vm_a" >"$d/state/vmjob.map"
  printf '%s\t%s\n' "$job_b" "$vm_b" >>"$d/state/vmjob.map"
  printf '%s\t%s\n' "$job_a" RUNNING >"$d/state/state.map"
  mkdir -p "$d/state/jobjson"
  printf '{"name": "%s", "uid": "%s", "status": {"state": "RUNNING"}}\n' "$job_a" "$uid_a" \
    >"$d/state/jobjson/$job_a.json"
  printf '{"name": "%s", "uid": "%s", "status": {"state": "RUNNING"}}\n' "$job_b" "$uid_b" \
    >"$d/state/jobjson/$job_b.json"
  printf '[{"protoPayload": {"methodName": "google.cloud.batch.v1.BatchService.CreateJob", "resourceName": "projects/p/locations/%s/jobs/%s", "request": {"job": {"name": "projects/p/locations/%s/jobs/%s", "uid": "%s"}}}}]\n' \
    us-east5 "$job_a" us-east5 "$job_a" "$uid_a" >"$d/state/audit.json"
  write_vm "$d/state/vm-a.json" "$vm_a" "$instance_id" "$zone" \
    "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_a" ""
  write_vm "$d/state/vm-b.json" "$vm_b" 9900112234 "$zone" \
    "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_b,job_uid=$uid_a,job_name=$job_a,instance_name=$vm_a,instance_id=$instance_id" ""
  printf '%s\t%s\n' "$vm_a" vm-a.json >"$d/state/vm.map"
  printf '%s\t%s\n' "$vm_b" vm-b.json >>"$d/state/vm.map"
  # Compute refuses the label key Google's own control plane writes on a Batch
  # VM, so the plain VM for O8 comes out of the check's second attempt carrying
  # every field but that one — which is the shape u1-handmade answers about. The
  # `handmade-accepted` bend below takes that refusal away, and a platform that
  # accepts the key is exactly what that row exists to catch.
  printf 'create_refuse_reserved=yes\nreserved_key=batch-job-uid\n' >>"$d/state/cfg"
  for bend in "$@"; do
    case "$bend" in
    forge-succeeds)
      write_vm "$d/state/vm-b.json" "$vm_b" 9900112234 "$zone" \
        "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_a,job_uid=$uid_a,job_name=$job_a,instance_name=$vm_a,instance_id=$instance_id" ""
      ;;
    handmade-accepted)
      write_cfg "$d" "job_a=$job_a" "zone=$zone"
      ;;
    handmade-forges)
      # The probe's VM carries its identity in two labels: the reserved one
      # Compute refuses, and one a hand-made VM will happily take. The first
      # attempt is turned away for the reserved key, and the VM that comes out of
      # the check's second attempt — carrying every label but that one — already
      # carries the field the link is on.
      write_vm "$d/state/vm-a.json" "$vm_a" "$instance_id" "$zone" \
        "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_a,job_uid=$uid_a" ""
      write_vm "$d/state/vm-b.json" "$vm_b" 9900112234 "$zone" \
        "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_b,job_uid=$uid_b" ""
      ;;
    metadata-identity)
      # The platform where the link also lives in a metadata item on the VM: a
      # field Compute does not refuse a hand — the label key beside it is still
      # refused, so the plain VM is built by the second attempt, and that
      # attempt has to carry the whole metadata set for this field to be tried
      # at all. It is, so the plain VM reproduces it and the row FAILS here.
      write_vm "$d/state/vm-a.json" "$vm_a" "$instance_id" "$zone" \
        "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_a" "batch-job-uid=$uid_a"
      ;;
    delete-is-slow)
      # A Batch delete is accepted at once and the job stays listable, in
      # DELETION_IN_PROGRESS, until the platform has finished with it: the sweep
      # at the end of the run must wait that out rather than read one list and
      # call the job it just deleted left behind.
      printf 'delete_lists=3\n' >>"$d/state/cfg"
      ;;
    name-forges)
      # The platform where Batch names the VM after the job: the probe's VM
      # carries the job's name in its own name, so the name is a candidate field
      # — and O8's plain VM, which this check names after the probe job, carries
      # it. The row that must find this is u1-handmade, and it finds it on the
      # name rather than being saved from it by the name the check chose.
      write_vm "$d/state/vm-a.json" "$vm_job_named" "$instance_id" "$zone" \
        "fugaro=managed,fugaro_chk=830,batch-job-uid=$uid_a" "batch-job-uid=$uid_a"
      write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_job_named" "$zone" "$instance_id" present "job_uid=$uid_a" full
      vm_a_is "$d" "$vm_job_named" vm-a.json
      ;;
    no-link)
      write_vm "$d/state/vm-a.json" "vm-plain01" "$instance_id" "$zone" "fugaro_chk=830" ""
      write_log "$d/state/log-$uid_a.json" "$uid_a" "vm-plain01" "$zone" "$instance_id" present "nothing=here" full
      vm_a_is "$d" vm-plain01 vm-a.json
      ;;
    link-inside-only)
      write_vm "$d/state/vm-a.json" "vm-plain01" "$instance_id" "$zone" "fugaro_chk=830" ""
      write_log "$d/state/log-$uid_a.json" "$uid_a" "vm-plain01" "$zone" "$instance_id" present "batch-job-uid=$uid_a" full
      vm_a_is "$d" vm-plain01 vm-a.json
      ;;
    link-on-the-job)
      write_vm "$d/state/vm-a.json" "vm-plain01" "$instance_id" "$zone" "fugaro_chk=830" ""
      write_log "$d/state/log-$uid_a.json" "$uid_a" "vm-plain01" "$zone" "$instance_id" present "nothing=here" full
      vm_a_is "$d" vm-plain01 vm-a.json
      printf '{"name": "%s", "uid": "%s", "status": {"instances": [{"name": "vm-plain01"}]}}\n' \
        "$job_a" "$uid_a" >"$d/state/jobjson/$job_a.json"
      ;;
    job-forges)
      # The link sits in a label of the job's own object — a field the launcher
      # writes — and the forging job's own object ends up carrying it, because
      # Batch reports back the labels a submit carried. Only a comparison that
      # reads that object can see it.
      write_vm "$d/state/vm-a.json" "vm-plain01" "$instance_id" "$zone" "fugaro_chk=830" ""
      write_log "$d/state/log-$uid_a.json" "$uid_a" "vm-plain01" "$zone" "$instance_id" present "nothing=here" full
      vm_a_is "$d" vm-plain01 vm-a.json
      printf '{"name": "%s", "uid": "%s", "labels": {"vm_name": "vm-plain01"}, "status": {"state": "RUNNING"}}\n' \
        "$job_a" "$uid_a" >"$d/state/jobjson/$job_a.json"
      ;;
    noclaims)
      write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_a" "$zone" "$instance_id" present "job_uid=$uid_a" noclaims
      ;;
    notoken)
      write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_a" "$zone" "$instance_id" present "job_uid=$uid_a" notoken
      ;;
    metadata-unreachable)
      write_log "$d/state/log-$uid_a.json" "$uid_a" "<unreachable>" "$zone" "$instance_id" present "job_uid=$uid_a" full
      ;;
    control-absent)
      write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_a" "$zone" "$instance_id" absent "job_uid=$uid_a" full
      ;;
    marker-in-logs)
      write_log "$d/state/log-$uid_a.json" "$uid_a" "$vm_a" "$zone" "$instance_id" present "job_uid=$uid_a" full "C830 marker @MARKER@"
      ;;
    marker-in-the-project)
      printf '[{"textPayload": "the task started with @MARKER@ in its variables"}]\n' >"$d/state/log-value.json"
      ;;
    audit-stale)
      # A record from an earlier run of this check: the job's name is the same —
      # it is fixed — and the uid is not this run's. A row that took a record by
      # its name would pass here before this run's own record had arrived, and
      # would have claimed absence for a record nobody read.
      printf '[{"protoPayload": {"methodName": "google.cloud.batch.v1.BatchService.CreateJob", "resourceName": "projects/p/locations/us-east5/jobs/%s", "request": {"job": {"name": "projects/p/locations/us-east5/jobs/%s", "uid": "77099"}}}}]\n' \
        "$job_a" "$job_a" >"$d/state/audit.json"
      ;;
    refuse-unattributed)
      # Compute turns the first attempt away with a message that names no field
      # of the request, so there is nothing to leave out of a second attempt
      # that would still carry the rest.
      printf 'create_refuse_blind=yes\n' >>"$d/state/cfg"
      ;;
    audit-leaks)
      printf '[{"protoPayload": {"methodName": "google.cloud.batch.v1.BatchService.CreateJob", "resourceName": "projects/p/locations/us-east5/jobs/%s", "request": {"job": {"name": "%s", "taskGroups": [{"taskSpec": {"environment": {"variables": {"FUGARO_BUDGET_TOKEN_WRAPPED": "@MARKER@"}}}}]}}}}]\n' \
        "$job_a" "$job_a" >"$d/state/audit.json"
      ;;
    audit-missing)
      printf '[]\n' >"$d/state/audit.json"
      ;;
    delete-fails)
      printf 'delete_fail=yes\n' >>"$d/state/cfg"
      ;;
    reuse-slow)
      # The name is refused the first three times, as a deletion that is still
      # being registered would have it: the check's loop, and the first refusal's
      # own message, are what this models.
      printf 'refuse_reuse=3\n' >>"$d/state/cfg"
      ;;
    submit-fails)
      printf 'submit_fail=yes\n' >>"$d/state/cfg"
      ;;
    hide-vms)
      printf 'hide_vms=yes\n' >>"$d/state/cfg"
      ;;
    probe-ran-no-lines)
      # The case the first real run of this check hit: the container ran, took
      # every one of its readings, and NOT ONE line of its own came back through
      # the filter the runner reads. Its second channel still answers — 100 plus
      # a bit per reading: reached the end of its reads (1), the metadata server
      # answered (2), a token was obtained (4), all four claims were in it (8),
      # the marker was in its own environment (16) — so u2-token and u3-marker
      # have an answer that does not rest on the log channel, and both say which
      # channel it is. The broader read under the job's own resource type is
      # empty here too, which is the reading that tells "its lines do not land in
      # the log the runner reads" apart from "nothing landed at all". The serial
      # console carries a token-shaped value on purpose: a diagnostic that prints
      # the platform's own output must redact it.
      printf '[]\n' >"$d/state/log-$uid_a.json"
      write_diag "$d" "$job_a" FAILED FAILED 131
      mkdir -p "$d/state/serial"
      printf 'Nov 25 03:51:28 batch-vm systemd[1]: Started Batch task container.\nNov 25 03:51:29 batch-vm kernel: metadata token ya29.fake-not-a-credential-000000\n' \
        >"$d/state/serial/$vm_a.txt"
      ;;
    probe-never-ran)
      # Nothing of the container's own came back and the task failed at Batch's
      # own level: an exit code above 50000 is reserved for Batch, so it is not
      # this container's own reading, and the second channel does not answer
      # either. The rows must say UNKNOWN rather than absent.
      printf '[]\n' >"$d/state/log-$uid_a.json"
      write_diag "$d" "$job_a" FAILED FAILED 50000
      ;;
    probe-never-ends)
      # The job that never gets to run: nothing of the container's own came back
      # and, when the wait ends, the job's own state is still not a terminal one,
      # so no task exit status was ever written. probe-exit has to say THAT; a row
      # read from this state once asserted "the task ended (job state ...)" about
      # a job that had not ended, which is the opposite of what happened.
      printf '[]\n' >"$d/state/log-$uid_a.json"
      write_diag "$d" "$job_a" QUEUED RUNNING ""
      ;;
    log-late)
      # The platform where a job's lines land late: the first read of that job's
      # task logs answers with nothing and the one after it answers with the
      # lines, which is the delay the wait exists for. A case that sets this
      # gives the check a non-zero CHK830_WAIT_LINES.
      printf 'log_late=1\n' >>"$d/state/cfg"
      ;;
    esac
  done
}
# expect_no_unreached: a row printed as "not reached" is a row this run did NOT
# measure, and the rows a case is not about still have to have been reached.
expect_no_unreached() { # what
  if grep -q 'not reached (the run stopped before this step)' <<<"$out"; then
    fail "$1: a row was not reached: $(printf '%s\n' "$out" | grep -m2 'not reached' | tr '\n' ' ')"
  fi
}
# expect_last_line: the block's last line, which the register's paste-back rule
# and this suite both read.
expect_last_line() { # want what
  local l
  l=$(last_line)
  [ "$l" = "$1" ] || fail "$2: the block's last line is '$l'; expected '$1'"
}
count_calls() { grep -c -F -- "$1" "$dir/state/gcloud.calls" 2>/dev/null || true; }
line_of_call() { grep -n -F -m1 -- "$1" "$dir/state/gcloud.calls" 2>/dev/null | cut -d: -f1 || true; }
# nothing_left: the fake's own record of what exists. The acceptance says the
# check deletes the job it made, and a job the check forgot is a job the fake
# still holds.
nothing_left() { # what
  if [ -s "$dir/state/jobs" ] && grep -q . "$dir/state/jobs"; then
    fail "$1: the check left job(s) behind: $(tr '\n' ' ' <"$dir/state/jobs")"
  fi
  if [ -f "$dir/state/created" ] && grep -q . "$dir/state/created"; then
    fail "$1: the check left instance(s) behind: $(tr '\n' ' ' <"$dir/state/created")"
  fi
}

# ------------------------------------------------------------------- the cases
test_help() {
  setup_case help
  run_check -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check-830.sh [--dry-run]" "help: the usage line"
  expect_absent "$out" "CHECK 830 RESULT" "help: no block for a usage question"
  expect_no_cloud "help"
}

test_dry_run() {
  setup_case dry-run
  run_check -- --dry-run
  expect_status "$status" 0 "dry run"
  expect_contains "$err" "NOTHING below was run" "dry run: it says so"
  expect_contains "$out" "FACT tree: PASS" "dry run: the tree reads are made"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row that says what it was"
  expect_contains "$out" "FACT probe-submitted: INFO" "dry run: a row that was not measured is INFO, not FAIL"
  expect_no_unreached "dry run"
  expect_last_line "CHECK 830 RESULT: PASS" "dry run"
  expect_contains "$err" "gcloud batch jobs submit ${job_a}" "dry run: the plan names the job it would submit"
  expect_contains "$err" "gcloud compute instances create ${handmade}" "dry run: the plan names the plain VM for O8"
  expect_no_cloud "dry run"
}

# The happy path: the platform answers as the model of it does, so every row is
# measured and every one passes. It is also the assertion the red step below is
# about: u1-forge's PASS here is what fails when the forge attempt is dropped.
test_happy_path() {
  setup_case happy
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "happy"
  expect_no_unreached "happy"
  expect_contains "$out" "FACT xtrace: PASS" "happy: xtrace"
  expect_contains "$out" "FACT owner: PASS" "happy: the owner marker"
  expect_contains "$out" "FACT belong: PASS" "happy: belong is refused by name"
  expect_row tree "read out of the tree" "happy: the tree fact"
  expect_row tree "labels.job_uid=" "happy: the tree fact names the filter"
  expect_row tree "FUGARO_BUDGET_TOKEN_WRAPPED" "happy: the tree fact names the wrapped variable"
  expect_verdict probe-submitted PASS "happy: the probe job"
  expect_row probe-submitted "${job_a}" "happy: the row names the job"
  expect_verdict probe-ran PASS "happy: the container ran"
  expect_verdict vm-a PASS "happy: the VM the container named"
  expect_row vm-a "${vm_a}" "happy: the row names the VM"
  expect_verdict u1-link PASS "happy: the link"
  expect_row u1-link "the link is the instance's own object" "happy: the link is a field of the instance"
  expect_row u1-link "labels.batch-job-uid=${uid_a}" "happy: the field the link rests on"
  # The O8 VM the check makes is named after the probe job, but on this platform
  # the probe's own VM carries no part of that name, so the name is not one of
  # the fields the row judges: the `name-forges` case below is the platform
  # where it is, and where u1-handmade must fail rather than be saved by the
  # name the check gave its own VM.
  expect_absent "$(printf '%s\n' "$out" | grep -m1 "^FACT u1-link: " || true)" "name=${vm_a}" \
    "happy: the instance's own name is not a candidate field when it carries no part of the job's identity"
  expect_row u1-link "compute.instances.get" "happy: the read the field costs (O1)"
  expect_verdict u1-forge PASS "happy: a second job cannot forge the link"
  expect_row u1-forge "Control: 5/5 of those keys" "happy: the attempt was live — the keys did reach that VM"
  expect_row u1-forge "fields compared" "happy: the attempt was judged, not passed over"
  expect_verdict u1-handmade PASS "happy: O8's plain VM is judged, not only reported"
  expect_row u1-handmade "refused by Compute" "happy: the refusal that led to the second attempt is inside the row"
  expect_row u1-handmade "labels.batch-job-uid is the only field the second attempt left out" \
    "happy: and so is exactly which field it left out — the one field the refusal named, and no other"
  expect_verdict u2-token PASS "happy: the identity token"
  expect_row u2-token "instance_id=${instance_id}, instance_name=${vm_a}, project_id=fugaro-dev, zone=${zone}" "happy: the four claims"
  expect_verdict u3-marker PASS "happy: where a job's variables reach"
  expect_row u3-marker "the CreateJob audit record of this run (the one carrying the uid ${uid_a}, which was found)" \
    "happy: the audit record was read, and read as this run's own — by a uid a stale record cannot carry"
  expect_verdict u8-name PASS "happy: the reuse of a deleted name"
  expect_row u8-name "attempt 1 of this loop" "happy: it came back at once"
  expect_verdict restore PASS "happy: the restore row"
  expect_contains "$out" "MANUAL: paste this block" "happy: the paste-back line"
  expect_last_line "CHECK 830 RESULT: PASS" "happy"
  # The job it made is gone, and the calls say how: the probe twice (the
  # measurement, then the re-created job), the forging job, and the plain VM.
  expect_call "batch jobs submit ${job_a}"
  expect_call "batch jobs submit ${job_b}"
  expect_call "compute instances create ${handmade}"
  expect_call "compute instances describe ${vm_a}"
  expect_call "logging read"
  nothing_left "happy"
  local dels
  dels=$(count_calls "batch jobs delete ${job_a}")
  [ "$dels" = 2 ] || fail "happy: the probe job should be deleted twice (once for the measurement, once a name that was only holding a name); it was deleted $dels time(s)"
  local first_a first_b
  first_a=$(line_of_call "batch jobs submit ${job_a}")
  first_b=$(line_of_call "batch jobs submit ${job_b}")
  [ -n "$first_a" ] && [ -n "$first_b" ] && [ "$first_a" -lt "$first_b" ] \
    || fail "happy: the probe was submitted first and the forging job is read against it (lines ${first_a:-?} and ${first_b:-?})"
  # The marker's value is a stand-in, and the block is a thing an owner pastes
  # into a document: it must not carry it, in the block or in the progress log.
  local marker
  marker=$(marker_sent)
  [ -n "$marker" ] || fail "happy: no marker value was submitted at all, so u3-measured nothing"
  expect_absent "$out" "$marker" "happy: the block"
  expect_absent "$err" "$marker" "happy: the progress log"
  # The body the check submitted is POSIX and its shebang is /bin/sh, and the
  # image it runs in need not carry bash: a container launched with /bin/bash
  # there dies at start with nothing on stdout, so the entrypoint the job names
  # has to be the one the body can be read by.
  local eps
  eps=$(submitted_entrypoints)
  [ "$eps" = "/bin/sh" ] \
    || fail "happy: the submitted containers launch $(printf '%s' "$eps" | tr '\n' ' ')— the body is POSIX and the shebang is /bin/sh"
}

# The red step of BR0, as the plan states it: "the self-test fails when the
# script omits the forge attempt". The copy below calls step_forge out of the
# run (its line becomes a no-op, so the copy still parses), and the same
# platform answers are then no longer a PASS — which is exactly what the happy
# case above asserts, so a script that dropped the attempt fails this suite.
test_the_forge_attempt_is_what_makes_the_row() {
  setup_case forge-mutation
  write_default_case "$dir"
  local copy=$dir/tree/scripts/live-checks/check-830.sh
  sed 's|^\( *\)step_forge$|\1: # the forge attempt, omitted on purpose|' "$script" >"$copy" || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the line that runs the forge attempt was not found to neutralise"
    return 0
  fi
  run_check
  expect_verdict u1-forge FAIL "red step: without the attempt the row cannot pass"
  expect_contains "$out" "not reached (the run stopped before this step)" "red step: the row says it was not measured"
  expect_no_call "batch jobs submit ${job_b}"
  # Everything else the platform answered for is still a PASS: it is the forge
  # attempt, and only it, that the row rests on.
  expect_verdict u1-link PASS "red step: the link itself is still measured"
  expect_verdict probe-submitted PASS "red step: the probe still ran"
}

# The forging job's container has to be alive when the fields are read off its
# VM, and the check waits WAIT_LINES_B for its lines first: the life it submits
# is the linger RAISED past that wait, and the job's maxRunDuration is computed
# from that life rather than from the linger. The raw linger submitted is the
# defect this case pins — a container that ended while the check still waited
# for its lines would have taken the reading with it, which is what the owner's
# first run of this check cost it.
test_the_forging_containers_life_outlasts_the_wait_for_its_lines() {
  setup_case forge-life
  write_default_case "$dir"
  run_check CHK830_LINGER_B=30 CHK830_WAIT_LINES_B=120
  expect_status "$status" 0 "a linger under the wait for the lines"
  expect_call "batch jobs submit ${job_b}"
  expect_verdict u1-forge PASS "a linger under the wait: the row is still measured"
  local life maxrun
  life=$(submitted_env "$job_b" FUGARO_CHK830_LINGER)
  maxrun=$(submitted_max_run "$job_b")
  [ "$life" = 180 ] \
    || fail "a linger under the wait: the forging container's life should be the 120s wait for its lines raised to 180s, not the raw 30s linger; the job carried ${life:-<nothing>}"
  [ "$maxrun" = 480s ] \
    || fail "a linger under the wait: the forging job's maxRunDuration should be that life plus its own margin (480s), so a container Batch kills on time cannot be mistaken for one that failed; it was ${maxrun:-<nothing>}"
}

test_a_second_job_that_reproduces_the_field_fails_u1_forge() {
  setup_case forgeable
  write_default_case "$dir" forge-succeeds
  run_check
  expect_status "$status" 1 "a forgeable link"
  expect_verdict u1-forge FAIL "a forgeable link: the row"
  expect_row u1-forge "CAN forge the link" "a forgeable link: it says which side won"
  expect_row u1-forge "instance.labels.batch-job-uid" "a forgeable link: the field it was forged through, named with the object it is on"
  expect_row u1-forge "BR3 cannot be built on it" "a forgeable link: what that means for the work"
  # The link itself is a PASS: a Google-made fact exists, and the finding is
  # that a launcher can write it too.
  expect_verdict u1-link PASS "a forgeable link: the link exists"
  nothing_left "a forgeable link"
}

# A platform that accepts the label a Batch VM's own identity sits in, on a VM
# someone makes by hand: the field is then indistinguishable from the probe's own
# VM in it, which is a FAIL and the finding this row exists for.
test_a_handmade_vm_that_carries_the_field_fails_u1_handmade() {
  setup_case handmade-carries
  write_default_case "$dir" handmade-accepted
  run_check
  expect_status "$status" 1 "a hand-made VM"
  expect_verdict u1-handmade FAIL "a hand-made VM: the row"
  expect_row u1-handmade "instance.labels.batch-job-uid" "a hand-made VM: the field it was written into, named with the object it is on"
  expect_row u1-handmade "compute.instances.create" "a hand-made VM: who could do it (O8)"
  expect_row u1-handmade "whatever the job it names" "a hand-made VM: what that means for the link"
  expect_verdict u1-forge PASS "a hand-made VM: the jobs' own avenues still hold"
  nothing_left "a hand-made VM"
}

# The refusal and the finding at once: Compute turns the first attempt away for
# the reserved key, and the second attempt — carrying every other field, the
# metadata set included — reproduces a field the link rests on, so the row is a
# FAIL. The refusal is inside that FAIL rather than a row of its own before it,
# which is what used to hide the finding.
test_a_handmade_vm_that_carries_the_fallback_key_fails_the_row() {
  setup_case handmade-forges
  write_default_case "$dir" handmade-forges
  run_check
  expect_status "$status" 1 "a hand-made VM that carries the fallback key"
  expect_verdict u1-handmade FAIL "a hand-made VM that carries the fallback key: the row"
  expect_row u1-handmade "refused by Compute" "a hand-made VM that carries the fallback key: the refusal is inside the verdict"
  expect_row u1-handmade "the only field the second attempt left out" "a hand-made VM that carries the fallback key: which attempt made it"
  expect_row u1-handmade "instance.labels.job_uid" "a hand-made VM that carries the fallback key: the field it carries"
  expect_verdict u1-link PASS "a hand-made VM that carries the fallback key: the link itself exists"
  expect_last_line "CHECK 830 RESULT: FAIL" "a hand-made VM that carries the fallback key"
  nothing_left "a hand-made VM that carries the fallback key"
}

# The platform where the link also lives in a metadata item on the VM. The label
# key beside it is still reserved, so the plain VM comes out of the check's
# second attempt — and that attempt carries the whole metadata set, so the field
# IS written, the plain VM reproduces it, and the row FAILS naming it. This is
# the case a fallback that sent one key alone would pass on a field it never
# tried to write: drop the metadata set from the second attempt and the row
# passes here instead.
test_a_handmade_vm_that_can_carry_the_metadata_field_fails_the_row() {
  setup_case handmade-metadata
  write_default_case "$dir" metadata-identity
  run_check
  expect_status "$status" 1 "a hand-made VM that can carry the metadata field"
  expect_verdict u1-handmade FAIL "a hand-made VM that can carry the metadata field: the row"
  expect_row u1-handmade "instance.metadata.items.batch-job-uid" \
    "a hand-made VM that can carry the metadata field: the field reproduced, named with the object it is on"
  expect_row u1-handmade "refused by Compute" "a hand-made VM that can carry the metadata field: the refusal is still inside the verdict"
  expect_verdict u1-forge PASS "a hand-made VM that can carry the metadata field: the forging job's own fields still hold"
  expect_last_line "CHECK 830 RESULT: FAIL" "a hand-made VM that can carry the metadata field"
  nothing_left "a hand-made VM that can carry the metadata field"
  # The finding was that the second attempt dropped the metadata set, so the
  # field this row is about was never written and a "not reproduced" was read
  # off a field that was never tried. The create that made the VM is the proof
  # that it was: its metadata carries the key the link rests on.
  local last_create
  last_create=$(grep -F -- "compute instances create ${handmade}" "$dir/state/gcloud.calls" | tail -n 1)
  grep -q -- "--metadata=[^ ]*batch-job-uid=${uid_a}" <<<"$last_create" \
    || fail "a hand-made VM that can carry the metadata field: the second attempt did not carry the metadata set, so the field was never written: $last_create"
  grep -q -- "--metadata=[^ ]*job_uid=${uid_a}" <<<"$last_create" \
    || fail "a hand-made VM that can carry the metadata field: the second attempt left a metadata key out of the set it wrote: $last_create"
}

# A refusal that names no field: there is nothing to leave out of a second
# attempt that would still carry every other field, so the row ENDS there rather
# than making a second attempt with a guessed key dropped. A guessed drop is a
# field never tried, which is the pass this whole row must not be able to earn.
test_a_refusal_that_names_no_field_ends_the_row() {
  setup_case refuse-unattributed
  write_default_case "$dir" refuse-unattributed
  run_check
  expect_status "$status" 1 "a refusal that names no field"
  expect_verdict u1-handmade FAIL "a refusal that names no field: the row is a failure, not a guess"
  expect_row u1-handmade "names no field this check wrote" "a refusal that names no field: what was missing from the message"
  expect_row u1-handmade "refused by Compute" "a refusal that names no field: the platform's message is still shown"
  [ "$(count_calls "compute instances create ${handmade}")" = 1 ] \
    || fail "a refusal that names no field: a second attempt was made with a field dropped for no reason the platform gave"
  expect_last_line "CHECK 830 RESULT: FAIL" "a refusal that names no field"
  nothing_left "a refusal that names no field"
}

# O8's plain VM is named after the probe job, so on a platform whose Batch VMs
# carry the job's name in their own name (the `name-forges` bend) the VM's name
# is a candidate field like any other — and the hand-made VM reproduces it. The
# row must be a FAIL that names instance.name; a name the check picks freely must
# not be what makes this row pass, since that is exactly the field a launcher
# writes by naming their VM after another job. This case is what pins the name
# the check gives its plain VM: rename it to something that is not the job's and
# the row stops finding the field, so the case fails.
test_a_plain_vm_named_after_the_job_forges_the_name_field() {
  setup_case name-forges
  write_default_case "$dir" name-forges
  run_check
  expect_status "$status" 1 "a VM named after the job"
  expect_verdict u1-link PASS "a VM named after the job: the link itself exists"
  expect_row u1-link "name=${vm_job_named}" "a VM named after the job: the VM's own name is one of the candidate fields"
  expect_verdict u1-forge PASS "a VM named after the job: the second job's own avenues still hold"
  expect_verdict u1-handmade FAIL "a VM named after the job: O8's row, and it is a finding rather than a pass"
  expect_row u1-handmade "instance.name" "a VM named after the job: the field the plain VM reproduced"
  expect_row u1-handmade "compute.instances.create" "a VM named after the job: who could do it (O8)"
  expect_last_line "CHECK 830 RESULT: FAIL" "a VM named after the job"
  nothing_left "a VM named after the job"
}

# The same attempt against a platform that refuses the key: the row is a verdict
# like any other — the refusal is not a row of its own beside it, where a reader
# would see it instead of the verdict — and it says which attempt was refused and
# which field the second attempt left out.
test_a_plain_vm_compute_refuses_the_key_on_is_recorded() {
  setup_case handmade-refused
  write_default_case "$dir"
  run_check
  expect_verdict u1-handmade PASS "a refused key: the row is a verdict, and the refusal is inside it"
  expect_row u1-handmade "refused by Compute" "a refused key: the refusal is in the row"
  expect_row u1-handmade "the key is reserved" "a refused key: the platform's own words"
  expect_row u1-handmade "labels.batch-job-uid is the only field the second attempt left out" \
    "a refused key: the field the refusal named is the only one the second attempt dropped"
  [ "$(count_calls "compute instances create ${handmade}")" = 2 ] \
    || fail "a refused key: the plain VM is tried once with every key and once with every key but the refused one"
  nothing_left "a refused key"
}

test_no_google_made_link_at_all_fails_u1_link() {
  setup_case no-link
  write_default_case "$dir" no-link
  run_check
  expect_status "$status" 1 "no link"
  expect_verdict u1-link FAIL "no link: the row"
  expect_row u1-link "no Google-made fact links this VM to its job" "no link: what was not found"
  expect_row u1-link "R4 says the work returns to you" "no link: what happens next"
  # There is no link for a second job to reproduce and none for a plain VM to
  # carry, and no forge job is submitted against nothing.
  expect_verdict u1-forge INFO "no link: the forge attempt is not made against nothing"
  expect_row u1-forge "there is no link a second job would have to reproduce" "no link: why the attempt is not made"
  expect_verdict u1-handmade INFO "no link: there is no field for a plain VM to carry"
  expect_no_call "batch jobs submit ${job_b}"
  nothing_left "no link"
}

# The container's own readings are what this link lives on, and the forging
# job's container reports a different set of keys: nothing of the field u1-link
# named can be read off that avenue, so neither row may pass — the run says the
# attempt was not judged rather than printing a PASS the comparison never earned.
test_a_link_inside_the_vm_only_is_reported_as_such() {
  setup_case inside-only
  write_default_case "$dir" link-inside-only
  run_check
  expect_status "$status" 1 "a link inside the VM"
  expect_verdict u1-link PASS "a link inside the VM"
  expect_row u1-link "the link is inside the VM only" "a link inside the VM: where it is"
  expect_row u1-link "not in the instance's object" "a link inside the VM: what it costs a reader outside"
  expect_row u1-link "the token's claims" "a link inside the VM: what it would have to rest on instead"
  expect_verdict u1-forge FAIL "a link inside the VM: the forge row does not pass on a comparison that did not run"
  expect_row u1-forge "the forge attempt was not judged" "a link inside the VM: the forge row says so"
  expect_verdict u1-handmade FAIL "a link inside the VM: nor does the plain VM's row"
  expect_row u1-handmade "was made and read, but the attempt was not judged" "a link inside the VM: the plain VM's row says so"
}

test_a_link_on_the_job_object_is_reported_as_such() {
  setup_case job-home
  write_default_case "$dir" link-on-the-job
  run_check
  expect_verdict u1-link PASS "a link on the job"
  expect_row u1-link "the link is the job's own object" "a link on the job: where it is"
  expect_row u1-link "batch.jobs.get" "a link on the job: the read the service already holds (F26)"
  expect_row u1-link "O1's question does not arise" "a link on the job: no second read is needed"
  # The field is a label of the job's object, so it is the forging job's own
  # object that has to be read for it — a comparison against its VM alone would
  # leave the candidate unjudged.
  expect_verdict u1-forge PASS "a link on the job: the forge row"
  expect_row u1-forge "1 of 1 fields compared" "a link on the job: the forging job's own object was the artifact read"
}

# A link that lives on the job's own object is a field the launcher writes, and
# the forging job's own object reproduces it — Batch reports back the labels a
# submit carried. Reading only the VM would report the link unforgeable.
test_a_second_job_that_reproduces_a_job_home_link_fails_u1_forge() {
  setup_case job-forges
  write_default_case "$dir" job-forges
  run_check
  expect_status "$status" 1 "a job-home link that is forgeable"
  expect_verdict u1-link PASS "a job-home link that is forgeable: the link exists"
  expect_row u1-link "the link is the job's own object" "a job-home link that is forgeable: where it is"
  expect_verdict u1-forge FAIL "a job-home link that is forgeable: the row"
  expect_row u1-forge "job.labels.vm_name" "a job-home link that is forgeable: the forging job's own object is where it reproduced it"
  expect_row u1-forge "BR3 cannot be built on it" "a job-home link that is forgeable: what that means for the work"
  nothing_left "a job-home link that is forgeable"
}

test_a_token_without_the_instance_claims_fails_u2() {
  setup_case token-noclaims
  write_default_case "$dir" noclaims
  run_check
  expect_status "$status" 1 "a token without the claims"
  expect_verdict u2-token FAIL "a token without the claims: the row"
  expect_row u2-token "google.compute_engine.instance_id" "a token without the claims: the claims that were missing"
  expect_row u2-token "google.compute_engine.zone" "a token without the claims: all four are named"
  expect_row u2-token "D3101" "a token without the claims: what the claims are for"
}

test_a_token_that_could_not_be_minted_fails_u2() {
  setup_case token-unreachable
  write_default_case "$dir" notoken
  run_check
  expect_status "$status" 1 "no token"
  expect_verdict u2-token FAIL "no token: the row"
  expect_row u2-token "could not obtain an identity token" "no token: what failed"
  expect_row u2-token "R4 applies" "no token: the work returns to the owner"
}

test_a_container_that_cannot_name_its_vm_falls_back_to_the_list() {
  setup_case vm-fallback
  write_default_case "$dir" metadata-unreachable
  run_check
  expect_verdict vm-a PASS "the fallback: the VM is still found"
  expect_row vm-a "${vm_a}" "the fallback: the instance list's one new instance is it"
  expect_verdict u1-link PASS "the fallback: the link is judged on it"
}

test_a_vm_that_cannot_be_resolved_is_a_failure() {
  setup_case vm-ambiguous
  write_default_case "$dir" metadata-unreachable hide-vms
  run_check
  expect_status "$status" 1 "an unresolved VM"
  expect_verdict vm-a FAIL "an unresolved VM: the row"
  expect_row vm-a "0 new instances where exactly one was expected" "an unresolved VM: why it is not guessed at"
  expect_verdict u1-forge INFO "an unresolved VM: the forge is not tried against nothing"
  expect_row u1-forge "no instance carrying the probe's identity" "an unresolved VM: why not"
  expect_verdict u1-handmade INFO "an unresolved VM: there is no zone to make a plain VM in"
}

test_a_marker_that_reaches_the_jobs_own_logs_fails_u3() {
  setup_case marker-logs
  write_default_case "$dir" marker-in-logs
  run_check
  expect_status "$status" 1 "the marker in the logs"
  expect_verdict u3-marker FAIL "the marker in the logs: the row"
  expect_row u3-marker "APPEARS in" "the marker in the logs: what was found"
  expect_row u3-marker "the probe job's own log entries" "the marker in the logs: the reading that found it"
  expect_row u3-marker "not printed in this block on purpose" "the marker in the logs: the value stays out of the block"
  local marker
  marker=$(marker_sent)
  [ -n "$marker" ] || fail "the marker in the logs: no marker value was submitted"
  expect_absent "$out" "$marker" "the marker in the logs: the block"
  expect_absent "$err" "$marker" "the marker in the logs: the progress log"
}

test_a_marker_that_reaches_the_project_logs_fails_u3() {
  setup_case marker-project
  write_default_case "$dir" marker-in-the-project
  run_check
  expect_verdict u3-marker FAIL "the marker in the project logs: the row"
  expect_row u3-marker "Cloud Logging anywhere in fugaro-dev" "the marker in the project logs: the reading that found it"
  expect_absent "$out" "$(marker_sent)" "the marker in the project logs: the block"
}

test_a_marker_in_the_createjob_record_fails_u3() {
  setup_case marker-audit
  write_default_case "$dir" audit-leaks
  run_check
  expect_verdict u3-marker FAIL "the marker in the audit record: the row"
  expect_row u3-marker "the CreateJob audit record" "the marker in the audit record: the reading that found it"
  expect_absent "$out" "$(marker_sent)" "the marker in the audit record: the block"
}

test_an_audit_record_that_was_never_read_fails_u3() {
  setup_case audit-missing
  write_default_case "$dir" audit-missing
  run_check
  expect_verdict u3-marker FAIL "an audit record never read: the row is fail-closed"
  expect_row u3-marker "could NOT be read within" "an audit record never read: what was not taken"
  expect_row u3-marker "CHK830_WAIT_AUDIT=" "an audit record never read: the knob that gives it longer"
}

# The record the FAIL texts above ask the owner to run again for: a re-run within
# the hour sees the PREVIOUS run's CreateJob record, which carries the job's name
# (fixed) and a uid that is not this run's. Accepting it would be a PASS taken
# before this run's own record had arrived — absence claimed for a record nobody
# had read — so the record is told apart by the one thing on it that is this
# run's alone.
test_an_audit_record_from_another_run_is_not_this_runs() {
  setup_case audit-stale
  write_default_case "$dir" audit-stale
  run_check
  expect_status "$status" 1 "an audit record from another run"
  expect_verdict u3-marker FAIL "an audit record from another run: the row is fail-closed"
  expect_row u3-marker "could NOT be read within" "an audit record from another run: this run's own record was not taken"
  expect_row u3-marker "another run's" "an audit record from another run: why the record that was found is not this run's"
  expect_absent "$out" "appears in none of the readings" "an audit record from another run: no PASS was recorded beside the FAIL on a record from another run"
}

test_a_marker_that_never_reached_the_container_is_void() {
  setup_case control-absent
  write_default_case "$dir" control-absent
  run_check
  expect_verdict u3-marker FAIL "the control: the row"
  expect_row u3-marker "did not reach the probe's container" "the control: what the positive control found"
  expect_row u3-marker "void on this row" "the control: the run measured nothing here"
}

test_a_name_that_comes_back_slowly_is_measured() {
  setup_case reuse-slow
  write_default_case "$dir" reuse-slow
  run_check CHK830_REUSE_BOUND=2
  expect_verdict u8-name PASS "a slow name: the row"
  expect_row u8-name "attempt 4 of this loop" "a slow name: the refusals before acceptance"
  expect_row u8-name "ALREADY_EXISTS" "a slow name: the first refusal's own message"
  expect_row u8-name "the re-created job (uid ${uid_a}) was deleted at once" "a slow name: the re-created job does not run"
  expect_row u8-name "D3108 does not depend on this answer" "a slow name: what it is and is not for"
  nothing_left "a slow name"
}

test_a_name_that_never_comes_back_is_a_failure() {
  setup_case reuse-refused
  write_default_case "$dir" reuse-slow
  run_check CHK830_REUSE_BOUND=0
  expect_status "$status" 1 "a name that never comes back"
  expect_verdict u8-name FAIL "a name that never comes back: the row"
  expect_row u8-name "still refused" "a name that never comes back: what happened"
  expect_row u8-name "CHK830_REUSE_BOUND=" "a name that never comes back: the knob"
}

# Both a job and an instance the check cannot delete: each is NAMED in the
# restore row with the command that removes it, rather than left silent.
test_resources_that_cannot_be_deleted_are_named() {
  setup_case delete-fails
  write_default_case "$dir" delete-fails
  run_check
  expect_status "$status" 1 "a resource that cannot be deleted"
  expect_verdict u8-name FAIL "a resource that cannot be deleted: the row"
  expect_row u8-name "STILL THERE" "a resource that cannot be deleted: the job is named as left behind"
  expect_verdict restore FAIL "a resource that cannot be deleted: the restore row"
  expect_row restore "the job(s) ${job_a} ${job_b}" "a resource that cannot be deleted: the jobs"
  expect_row restore "the instance ${handmade}" "a resource that cannot be deleted: the instance"
  expect_row restore "gcloud batch jobs delete <name>" "a resource that cannot be deleted: the command that removes a job"
  expect_row restore "gcloud compute instances delete <name>" "a resource that cannot be deleted: the command that removes an instance"
}

# Deleting a Batch job is asynchronous: the delete is accepted at once and the
# job stays listable, in DELETION_IN_PROGRESS, until the platform has finished
# with it (internal/backend/batch/reap.go lists that state). The sweep that runs
# at the end of the check has just deleted the job it made, so it must wait that
# out: a single list taken one call too early records a job the check has already
# deleted as left behind and fails the restore row — and the whole check — on a
# run that left nothing behind at all.
test_a_slow_delete_is_not_a_leftover() {
  setup_case slow-delete
  write_default_case "$dir" delete-is-slow
  run_check CHK830_SWEEP_BOUND=30
  expect_status "$status" 0 "a slow delete"
  expect_verdict restore PASS "a slow delete: the restore row"
  expect_verdict u8-name PASS "a slow delete: the reuse row"
  expect_last_line "CHECK 830 RESULT: PASS" "a slow delete"
  nothing_left "a slow delete"
  local lists
  lists=$(count_calls "batch jobs list")
  [ "${lists:-0}" -ge 3 ] \
    || fail "a slow delete: the job list was read ${lists:-0} time(s) after the deletes, so the sweep judged what was left behind on a list the platform was still finishing the delete for"
}

test_a_probe_that_cannot_be_submitted_stops_the_run() {
  setup_case submit-fails
  write_default_case "$dir" submit-fails
  run_check
  expect_status "$status" 2 "a probe that cannot be submitted"
  expect_verdict probe-submitted FAIL "a probe that cannot be submitted: the row"
  expect_row probe-submitted "nothing was created" "a probe that cannot be submitted: nothing is left to clean up"
  expect_contains "$out" "FACT u1-link: FAIL — not reached" "a probe that cannot be submitted: the rows below it were not measured"
  expect_verdict restore PASS "a probe that cannot be submitted: nothing was made"
}

test_the_owner_marker_is_required() {
  setup_case no-owner
  RUN_OWNER=no run_check
  expect_status "$status" 2 "the owner guard"
  expect_verdict owner FAIL "the owner guard: the row"
  expect_row owner "CHK830_OWNER_RUN is not exported" "the owner guard: what is missing"
  expect_row xtrace PASS "the owner guard: the guard before it was reached"
  expect_no_cloud "the owner guard"
}

test_belong_is_refused_by_name() {
  setup_case belong
  run_check CLOUDSDK_CORE_PROJECT=belong-dev
  expect_status "$status" 2 "belong"
  expect_verdict belong FAIL "belong: the row"
  expect_row belong "belong-dev" "belong: the ambient project it read"
  expect_no_cloud "belong"
}

test_a_scratch_inside_a_work_tree_is_refused() {
  setup_case worktree
  git -c init.defaultBranch=main init -q "$dir/repo" 2>/dev/null || true
  [ -d "$dir/repo/.git" ] || { fail "a scratch inside a work tree: the case could not make a repository to test with"; return 0; }
  RUN_TMPDIR=$dir/repo run_check
  expect_status "$status" 2 "a scratch inside a work tree"
  expect_contains "$err" "inside a git work tree" "a scratch inside a work tree: the refusal"
  expect_no_cloud "a scratch inside a work tree"
}

test_xtrace_is_refused() {
  setup_case xtrace
  BASH_FLAGS=-x run_check
  expect_status "$status" 2 "xtrace"
  expect_verdict xtrace FAIL "xtrace: the row"
  expect_no_cloud "xtrace"
}

test_an_unknown_argument_is_refused() {
  setup_case unknown-arg
  run_check -- --bogus
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--bogus'" "an unknown argument: the refusal names it"
  expect_contains "$out" "usage: check-830.sh" "an unknown argument: the usage"
  expect_no_cloud "an unknown argument"
}

test_a_tree_without_the_filters_label_stops_before_anything_is_called() {
  setup_case tree-no-filter no-filter
  run_check
  expect_status "$status" 2 "a tree without the filter"
  expect_contains "$err" "no longer builds its filter with labels.job_uid" "a tree without the filter: the refusal"
  expect_contains "$out" "FACT aborted: FAIL" "a tree without the filter: the block says it was stopped"
  expect_no_cloud "a tree without the filter"
}

test_a_tree_without_the_plans_name_falls_back() {
  setup_case tree-no-docs no-docs
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "a tree without the plan's name"
  expect_row tree "could not be read out of docs/batch-run-identity/" "a tree without the plan's name: the fallback is reported"
  expect_verdict u3-marker PASS "a tree without the plan's name: the row is still measured"
}

# The case the first real run of this check hit, and the two the fix owes it: no
# line of the container's own arrives, the lines arrive late, and a container
# that never ran at all. probe-ran already says the log channel did not report
# the run; what these cases are about is that the block says WHICH of the three
# it was, and answers u2 and u3 from the container's second channel when the log
# channel is empty — rather than reading "not measured" as "measured against the
# design", which is what the first run's block did.
test_no_line_arrives_and_the_exit_status_answers() {
  setup_case no-lines
  write_default_case "$dir" probe-ran-no-lines
  run_check
  expect_status "$status" 1 "no lines"
  expect_no_unreached "no lines"
  expect_verdict probe-ran FAIL "no lines: the row"
  expect_row probe-ran "not one line of the container's own came back at all" "no lines: how far the container got"
  expect_row probe-ran "probe-diag" "no lines: the readings taken next are named"
  expect_row probe-ran "probe-exit" "no lines: and the second channel is named"
  # The evidence the row owes the reader, taken while the job and the VM still
  # exist: the job's own state, each task's state, their status events and the
  # serial console — and, under the job's own resource type, the reading that
  # tells "its lines land in another log" apart from "nothing landed".
  expect_verdict probe-diag INFO "no lines: the diagnostics"
  expect_row probe-diag "the job's own state is FAILED" "no lines: the job's own state"
  expect_row probe-diag "its task's state(s) 0=FAILED" "no lines: each task's state"
  expect_row probe-diag "exit=131" "no lines: the task's own status events, and the code on them"
  expect_row probe-diag "serial console" "no lines: the serial console is a reading, not a skip"
  expect_row probe-diag "Started Batch task container" "no lines: and its tail is printed"
  expect_row probe-diag 'resource.type="batch.googleapis.com/Job"' "no lines: the broader read"
  expect_row probe-diag "found 0 line(s) by the runner's label" "no lines: by the runner's label"
  expect_row probe-diag "0 line(s) by the uid alone" "no lines: and by the uid alone"
  # A diagnostic prints the platform's own output, and these lines carry a value
  # shaped like a credential: it must never reach the block.
  expect_row probe-diag "<redacted-token>" "no lines: a credential-shaped value in the serial output is redacted"
  expect_absent "$out" "ya29.fake-not-a-credential-000000" "no lines: the serial output's credential"
  # The second channel, and the two rows that answer from it.
  expect_verdict probe-exit INFO "no lines: the exit status"
  expect_row probe-exit "reported an exit status of its own: 131" "no lines: the code, and that it is this container's own"
  expect_row probe-exit "it reached the end of its reads" "no lines: what the code says"
  expect_row probe-exit "it obtained an identity token" "no lines: the token read"
  expect_row probe-exit "all four claims" "no lines: the claims read"
  expect_row probe-exit "marker variable was in its own environment" "no lines: the marker read"
  expect_verdict u2-token PASS "no lines: U2 is measured without the log channel"
  expect_row u2-token "answered by the task's exit status, not by the log channel" "no lines: and the row says which channel answered"
  expect_row u2-token "all four claims" "no lines: what it answers"
  expect_verdict u3-marker PASS "no lines: U3 too, and not read as void"
  expect_row u3-marker "answered by the task's exit status, not by the log channel" "no lines: naming the channel that answered"
  expect_verdict vm-a PASS "no lines: the VM is still found"
  expect_row vm-a "the container did NOT name it" "no lines: and the row says this reading is the weaker one"
  expect_last_line "CHECK 830 RESULT: FAIL" "no lines"
  # The value the check generated is in the job's own configuration and nowhere
  # else: not in the block, and not in the progress log.
  local marker
  marker=$(marker_sent)
  [ -n "$marker" ] || fail "no lines: no marker value was submitted at all"
  expect_absent "$out" "$marker" "no lines: the block"
  expect_absent "$err" "$marker" "no lines: the progress log"
}

test_no_line_arrives_and_the_task_reported_nothing_either() {
  setup_case never-ran
  write_default_case "$dir" probe-never-ran
  run_check
  expect_status "$status" 1 "never ran"
  expect_verdict probe-ran FAIL "never ran: the row"
  expect_row probe-diag "the job's own state is FAILED" "never ran: the job's state is one of the readings"
  expect_verdict probe-exit FAIL "never ran: the second channel did not answer"
  expect_row probe-exit "not this container's own" "never ran: the code is Batch's, above the reserved 50000"
  expect_row probe-exit "UNKNOWN rather than absent" "never ran: what that means for the rows below"
  expect_verdict u2-token FAIL "never ran: U2 is not answered"
  expect_row u2-token "neither channel answered" "never ran: and the row says so"
  expect_row u2-token "UNKNOWN rather than absent" "never ran: a container that never ran measures nothing"
  expect_last_line "CHECK 830 RESULT: FAIL" "never ran"
}

test_a_job_that_never_ended_says_so() {
  setup_case never-ends
  write_default_case "$dir" probe-never-ends
  run_check CHK830_WAIT_EXIT=10
  expect_status "$status" 1 "never ends"
  expect_verdict probe-ran FAIL "never ends: the row"
  expect_row probe-diag "the job's own state is QUEUED" "never ends: the job's own state is read and it is not a terminal one"
  expect_verdict probe-exit FAIL "never ends: the second channel did not answer"
  expect_row probe-exit "had not ended within 10s" "never ends: the wait expired, and the row says that rather than the opposite"
  expect_row probe-exit "state read QUEUED at the last poll" "never ends: with the state the last poll saw"
  expect_row probe-exit "UNKNOWN rather than absent" "never ends: and what that means for the rows below"
  # The row this state used to print said the task HAD ended. A job that never
  # scheduled is the one reading where that sentence is the opposite of what the
  # platform reported, so it must not be there — the row says what it does NOT
  # say, which is why the needle is the old sentence and not its words.
  expect_absent "$out" "the task ended (job state" "never ends: the row does not claim the task ended"
  expect_verdict u2-token FAIL "never ends: U2 is not answered"
  expect_row u2-token "neither channel answered" "never ends: and the row says so"
  expect_last_line "CHECK 830 RESULT: FAIL" "never ends"
}

test_lines_that_arrive_late_are_waited_for() {
  setup_case late-lines
  write_default_case "$dir" log-late
  run_check CHK830_WAIT_LINES=10
  expect_status "$status" 0 "late lines"
  expect_verdict probe-ran PASS "late lines: the row"
  expect_row probe-ran "the probe container ran and reported" "late lines: what it reads"
  # The first read of that job's logs answered with nothing and the lines came on
  # the one after it, so the row must have been read after a poll rather than
  # concluded from a single empty read.
  expect_contains "$err" "waiting for the probe's first line (10s of 10s)" "late lines: the poll"
  expect_verdict probe-diag INFO "late lines: nothing to diagnose"
  expect_row probe-diag "not taken" "late lines: and the row says so rather than inventing evidence"
  expect_verdict probe-exit INFO "late lines: the second channel was not needed"
  expect_row probe-exit "not taken" "late lines: and the row says so"
  expect_verdict u2-token PASS "late lines: U2 from the log channel"
  expect_verdict u3-marker PASS "late lines: U3 from the log channel"
  expect_last_line "CHECK 830 RESULT: PASS" "late lines"
}

# ------------------------------------------------------------------- the run
# CHECK_TEST_ONLY names one case; CHECK_TEST_SHARD=i/n runs every case whose
# index is congruent to i modulo n. scripts/live_checks_test.go splits the suite
# across workers that way, so a case added above needs its `t` line below or CI
# never runs it at all.
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
t test_the_forge_attempt_is_what_makes_the_row
t test_the_forging_containers_life_outlasts_the_wait_for_its_lines
t test_a_second_job_that_reproduces_the_field_fails_u1_forge
t test_a_handmade_vm_that_carries_the_field_fails_u1_handmade
t test_a_handmade_vm_that_carries_the_fallback_key_fails_the_row
t test_a_handmade_vm_that_can_carry_the_metadata_field_fails_the_row
t test_a_refusal_that_names_no_field_ends_the_row
t test_a_plain_vm_named_after_the_job_forges_the_name_field
t test_a_plain_vm_compute_refuses_the_key_on_is_recorded
t test_no_google_made_link_at_all_fails_u1_link
t test_a_link_inside_the_vm_only_is_reported_as_such
t test_a_link_on_the_job_object_is_reported_as_such
t test_a_second_job_that_reproduces_a_job_home_link_fails_u1_forge
t test_a_token_without_the_instance_claims_fails_u2
t test_a_token_that_could_not_be_minted_fails_u2
t test_a_container_that_cannot_name_its_vm_falls_back_to_the_list
t test_a_vm_that_cannot_be_resolved_is_a_failure
t test_a_marker_that_reaches_the_jobs_own_logs_fails_u3
t test_a_marker_that_reaches_the_project_logs_fails_u3
t test_a_marker_in_the_createjob_record_fails_u3
t test_an_audit_record_that_was_never_read_fails_u3
t test_an_audit_record_from_another_run_is_not_this_runs
t test_a_marker_that_never_reached_the_container_is_void
t test_no_line_arrives_and_the_exit_status_answers
t test_no_line_arrives_and_the_task_reported_nothing_either
t test_a_job_that_never_ended_says_so
t test_lines_that_arrive_late_are_waited_for
t test_a_name_that_comes_back_slowly_is_measured
t test_a_name_that_never_comes_back_is_a_failure
t test_resources_that_cannot_be_deleted_are_named
t test_a_slow_delete_is_not_a_leftover
t test_a_probe_that_cannot_be_submitted_stops_the_run
t test_the_owner_marker_is_required
t test_belong_is_refused_by_name
t test_a_scratch_inside_a_work_tree_is_refused
t test_xtrace_is_refused
t test_an_unknown_argument_is_refused
t test_a_tree_without_the_filters_label_stops_before_anything_is_called
t test_a_tree_without_the_plans_name_falls_back

if [ "$failures" -ne 0 ]; then
  echo "check-830_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-830_test.sh: ok"
