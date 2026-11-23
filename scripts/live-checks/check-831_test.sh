#!/usr/bin/env bash
# Tests for scripts/live-checks/check-831.sh, with a fake `gcloud`, a fake
# `curl`, a fake `fugaro` and a fake checkout on PATH: NOTHING here reaches a
# cloud, a project, a job, a database or a log, no agent runs a live command, and
# every fake records what it was given so a case can assert what the check did
# AND what it did not do. Check 831 launches a real Batch run and makes four
# attacks on a live credentials service with a second account, which is why every
# reading it takes is measured here against a model of the platform rather than
# by running it.
#
# The fake `gcloud` is a small model of Batch, Logging and the auth surface,
# driven by files a case writes under FAKE831_DIR:
#
#   account        the account `gcloud config get-value account` answers with
#   cfg            key=value knobs — impersonate_fail, impersonate_fail.<what>
#                  (what: describe, submit, delete, token), submit_fail,
#                  delete_fail, delete_fail.<job>, delete_lists, delete_submits,
#                  claim_status, claim_error, claim_message, claim_token, and the
#                  same four per job as claim_status.<job> etc.,
#                  second_ticket.<job> (what the SECOND LAUNCHER's own describe
#                  of that job answers with: `none` takes the ticket out of the
#                  document, any other value puts that value there, and no
#                  value at all leaves the platform's own job as it is),
#                  no_lines.<job>, run_fail
#   jobs/<name>/job.json  the job document: `describe --format=json` answers with
#                  it, `list` reads its labels, `delete` removes it
#   deleting/<name>/lists, deleting/<name>/submits  a job whose delete the
#                  platform has accepted and not finished: it stays listed for
#                  that many more `list` calls and its name stays un-submittable
#                  for that many more `submit` calls (ALREADY_EXISTS), which is
#                  how a Batch delete behaves (DELETION_IN_PROGRESS); a
#                  `describe` of it answers DELETION_IN_PROGRESS while either
#                  has anything left
#   logs/<uid>.json  the entries a job's task logs answer with, selected by the
#                  tree's own filter (labels.job_uid="<uid>")
#   audit.json     the entries the CreateJob audit filter answers with
#   launch.json, launch2.json  the answers `fugaro run --json` gives, one per
#                  call: the run under test's, then the second launch's (a
#                  counter under runs.next is what tells the two calls apart)
#   rtdb/<path>.json and .code  the body and the status one Realtime Database
#                  read answers with; a `.then.json`/`.then.code` beside them is
#                  served on the SECOND and later reads of that path, which is
#                  how a ledger that first says "session opened" and only later
#                  says "metered" is modelled
#   gcloud.calls, curl.calls, fugaro.calls  every invocation, one line each
#
# A submitted attack job is not answered from a file: the fake builds its log
# lines out of the knobs (claim_status/claim_error/claim_message/claim_token),
# because what an attack's own VM is told IS the reading the check reports — and
# the refusal's SENTENCE is what tells the refusal of the VM from one the route
# answers before it asks the link. `@TICKET@` in a fixture is not needed: the
# ticket is a fixture value the test writes and knows.
#
# The tree is a fixture too: the check reads the ticket's variable, the claim's
# route and the record's path family out of internal/identitywire/wire.go, the
# escaping out of internal/budget/model.go, the run's own variable out of
# internal/backend/batch/env.go, the run label out of
# internal/backend/batch/names.go, the log filter out of
# internal/backend/batch/logs.go and the two URL variables out of
# internal/runner/budget_session.go — and four cases bend those files to prove
# the reads are reads.
#
# Usage: bash scripts/live-checks/check-831_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check-831_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check-831_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-831.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK831_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK831_TEST_HEAD:-1800}")" ;;
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
expect_verdict() { # key want what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  case "$row" in
  "FACT $1: $2 "*) ;;
  *) fail "$3: expected FACT $1 to be $2; got: ${row:-<no row>}" ;;
  esac
}

# The values the check's own rows are read against, spelled out here so a case
# can name the job it means rather than matching a stem.
repo=acme/widget
slug=acme-widget-0123456789abcdef
runid=20261122-120000-abcd
runjob=fugaro-acme-widget-implement-abc123def456
uid=55001
# The SECOND launch of the check: the run the third attack deletes while its job
# is still queued. It is another run of the same repository, with its own id, its
# own job and its own ticket.
runid2=20261122-120500-beef
runjob2=fugaro-acme-widget-implement-def456abc123
uid2=55002
ticket2=chk831-ticket-4b7e15
runacct=fugaro-acme-widget-implement@fugaro-dev.iam.gserviceaccount.com
owner=owner@example.com
second=second-launcher@fugaro-dev.iam.gserviceaccount.com
ticket=chk831-ticket-9f2c41
rtdb=https://fugaro-dev-default-rtdb.firebaseio.com
service=https://fugaro-credsvc-abc123-ue.a.run.app
job_replay=fugaro-chk831-replay
job_noticket=fugaro-chk831-noticket
P_CHK=fugaro-dev

# --------------------------------------------------------------------- the fake
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
import json, os, re, shutil, sys

D = os.environ.get("FAKE831_DIR", "")


def load_cfg():
    out = {}
    try:
        with open(os.path.join(D, "cfg")) as fh:
            for line in fh:
                line = line.strip()
                if line and "=" in line:
                    k, v = line.split("=", 1)
                    out[k] = v
    except OSError:
        pass
    return out


C = load_cfg()
args = sys.argv[1:]
try:
    with open(os.path.join(D, "gcloud.calls"), "a") as fh:
        fh.write("gcloud " + " ".join(args) + "\n")
except OSError:
    pass


def out(s):
    sys.stdout.write(str(s) + "\n")


def flag(name):
    if name in args:
        i = args.index(name)
        if i + 1 < len(args):
            return args[i + 1]
    for a in args:
        if a.startswith(name + "="):
            return a.split("=", 1)[1]
    return ""


def fail(msg):
    sys.stderr.write("ERROR: (gcloud) " + msg + "\n")
    sys.exit(1)


def impersonated(what):
    who = flag("--impersonate-service-account")
    if not who:
        return
    if C.get("impersonate_fail") == "yes" or C.get("impersonate_fail." + what) == "yes":
        fail("Permission denied while impersonating " + who)
    allowed = C.get("impersonable", "second-launcher@fugaro-dev.iam.gserviceaccount.com")
    if who != allowed:
        fail("cannot impersonate " + who)


def jobs():
    names = []
    live = os.path.join(D, "jobs")
    try:
        names += [n for n in os.listdir(live) if os.path.isdir(os.path.join(live, n))]
    except OSError:
        pass
    going = os.path.join(D, "deleting")
    try:
        for n in sorted(os.listdir(going)):
            if reaping(n, "lists"):
                names.append(n)
    except OSError:
        pass
    return sorted(names)


def reaping(name, kind):
    """Is the platform still finishing with a deleted job of this name?

    A Batch delete is accepted at once and the job stays the platform's until it
    has finished with it: it is still listed, still described (DELETION_IN_PROGRESS)
    and its name is not free — a submit under it is refused ALREADY_EXISTS. The
    fixture says how long that lasts for each kind of read of its own
    (deleting/<name>/lists, deleting/<name>/submits), so a case can make a name
    slow to be listed without also making it slow to be submitted, and the other
    way round: the check makes both reads, and one must not spend the other's.
    """
    counter = os.path.join(D, "deleting", name, kind)
    try:
        left = int(open(counter).read().strip() or "0")
    except OSError:
        return False
    if left <= 0:
        forget(name)
        return False
    with open(counter, "w") as fh:
        fh.write(str(left - 1))
    return True


def forget(name):
    """The platform is done with a deleted job of this name: nothing is left to
    read, so a later describe is NOT_FOUND and a submit under the name creates."""
    going = os.path.join(D, "deleting", name)
    try:
        kinds = os.listdir(going)
    except OSError:
        return
    for kind in kinds:
        try:
            if int(open(os.path.join(going, kind)).read().strip() or "0") > 0:
                return
        except OSError:
            pass
    shutil.rmtree(going, ignore_errors=True)


def held(name):
    """Is a deleted job of this name still the platform's? A describe answers the
    job in DELETION_IN_PROGRESS until it is done, which is what makes NOT_FOUND a
    reading of a job that is gone rather than the first answer every delete gets.
    """
    going = os.path.join(D, "deleting", name)
    try:
        kinds = os.listdir(going)
    except OSError:
        return False
    for kind in kinds:
        try:
            if int(open(os.path.join(going, kind)).read().strip() or "0") > 0:
                return True
        except OSError:
            pass
    forget(name)
    return False


def doc(name):
    try:
        with open(os.path.join(D, "jobs", name, "job.json")) as fh:
            return json.load(fh)
    except Exception:
        return None


def next_uid():
    path = os.path.join(D, "uid.next")
    try:
        n = int(open(path).read().strip() or "60001")
    except OSError:
        n = 60001
    with open(path, "w") as fh:
        fh.write(str(n + 1))
    return n


def attack_log(name, uid):
    if C.get("no_lines." + name) == "yes":
        return
    status = C.get("claim_status." + name, C.get("claim_status", "403"))
    err = C.get("claim_error." + name, C.get("claim_error", "identity_refused"))
    tok = C.get("claim_token." + name, C.get("claim_token", "no"))
    # The refusal's own sentence is what tells the VM's refusal from one the
    # route answers before it asks the link at all (the ticket already spent),
    # so the fake prints one on every claim it refuses, exactly as credsvc's
    # refuse does (WireError.Message). The default is the link's own refusal.
    words = C.get("claim_message." + name, C.get("claim_message",
        "the VM this claim came from is not a VM of the job the record names"))
    lines = [
        "C831 start host=attack-vm-%d" % uid,
        "C831 claim status=%s error=%s token=%s" % (status, err, tok),
        "C831 refusal %s" % words,
        "C831 read",
    ]
    os.makedirs(os.path.join(D, "logs"), exist_ok=True)
    with open(os.path.join(D, "logs", str(uid) + ".json"), "w") as fh:
        json.dump([{"textPayload": l} for l in lines], fh)


if not args:
    sys.exit(0)

if args[0] == "config":
    try:
        out(open(os.path.join(D, "account")).read().strip())
    except OSError:
        out("")

elif args[0] == "auth":
    impersonated("token")
    out("token-second" if flag("--impersonate-service-account") else "token-owner")

elif args[0] == "logging":
    # `gcloud logging read <FILTER> ...`: the filter is the first argument after
    # the two words of the subcommand, not the second argument of the call.
    filt = args[2] if len(args) > 2 else ""
    if "CreateJob" in filt:
        path = os.path.join(D, "audit.json")
    else:
        m = re.search(r'labels\.job_uid="([^"]*)"', filt)
        path = os.path.join(D, "logs", (m.group(1) if m else "none") + ".json")
    try:
        sys.stdout.write(open(path).read())
    except OSError:
        sys.stdout.write("[]\n")

elif args[0] == "batch":
    sub = ""
    for a in ("list", "describe", "submit", "delete"):
        if a in args:
            sub = a
            break
    if sub == "list":
        filt = flag("--filter")
        for n in jobs():
            labels = (doc(n) or {}).get("labels") or {}
            if "labels.fugaro_chk=" in filt:
                want = filt.split("labels.fugaro_chk=", 1)[1].split()[0]
                if labels.get("fugaro_chk") == want:
                    out(n)
            elif filt.endswith(":*") and "labels." in filt:
                key = filt.split("labels.", 1)[1].split(":", 1)[0]
                if key in labels:
                    out(n + "\t" + labels[key])
    elif sub == "describe":
        name = args[args.index("describe") + 1]
        impersonated("describe")
        d = doc(name)
        if d is None and held(name):
            # The job is gone from the live set and the platform has not finished
            # with its name: it is still described, in DELETION_IN_PROGRESS, and
            # NOT_FOUND is the answer only once it is done.
            d = {"name": "projects/fugaro-dev/locations/us-east5/jobs/" + name,
                 "uid": "", "status": {"state": "DELETION_IN_PROGRESS"}}
        if d is None:
            fail("NOT_FOUND: job %s" % name)
        # What the SECOND LAUNCHER's own read answers, when a case bends it: the
        # two accounts read the same document on the platform, so the only way a
        # case can show that the check took the ticket it replays out of the
        # impersonated read — and not out of its own — is to make that read
        # answer another document.
        bend = C.get("second_ticket." + name)
        if flag("--impersonate-service-account") and bend is not None:
            vars_ = {}
            for tg in d.get("taskGroups") or []:
                env = ((tg or {}).get("taskSpec") or {}).setdefault("environment", {})
                vars_ = env.setdefault("variables", {})
            if bend == "none":
                vars_.pop("FUGARO_IDENTITY_TICKET", None)
            else:
                vars_["FUGARO_IDENTITY_TICKET"] = bend
        fmt = flag("--format")
        if fmt == "json":
            sys.stdout.write(json.dumps(d, indent=2) + "\n")
        elif fmt.startswith("value("):
            key = fmt[len("value("):-1]
            if key == "name.basename()":
                out(name)
            elif key == "uid":
                out(d.get("uid", ""))
            elif key == "status.state":
                out((d.get("status") or {}).get("state", ""))
            else:
                out("")
        else:
            sys.stdout.write(json.dumps(d) + "\n")
    elif sub == "submit":
        name = args[args.index("submit") + 1]
        impersonated("submit")
        if C.get("submit_fail") == "yes":
            fail("the job could not be created")
        if reaping(name, "submits"):
            # The platform has not finished with a deleted job of this name, so
            # the name is not free: the real Batch refuses the create with
            # ALREADY_EXISTS, and a check that read that as the attack's refusal
            # would never make the attack.
            fail("ALREADY_EXISTS: job %s already exists" % name)
        try:
            with open(flag("--config")) as fh:
                d = json.load(fh)
        except Exception:
            fail("no job configuration at %s" % flag("--config"))
        n = next_uid()
        d["name"] = "projects/fugaro-dev/locations/us-east5/jobs/" + name
        d["uid"] = str(n)
        d["status"] = {"state": "QUEUED"}
        os.makedirs(os.path.join(D, "jobs", name), exist_ok=True)
        with open(os.path.join(D, "jobs", name, "job.json"), "w") as fh:
            json.dump(d, fh, indent=2)
        os.makedirs(os.path.join(D, "submits"), exist_ok=True)
        with open(os.path.join(D, "submits", name + ".json"), "w") as fh:
            json.dump(d, fh, indent=2)
        attack_log(name, n)
        out(name)
    elif sub == "delete":
        name = args[args.index("delete") + 1]
        impersonated("delete")
        if C.get("delete_fail") == "yes" or C.get("delete_fail." + name) == "yes":
            fail("the job could not be deleted")
        live = os.path.join(D, "jobs", name)
        going = os.path.join(D, "deleting", name)
        if not os.path.isdir(live):
            # A job whose delete was accepted is still listed while the platform
            # reaps it, so a second delete of the SAME job is a no-op — but a job
            # re-created under a name whose earlier delete is still being reaped
            # is a new job, and its delete must move it like any other.
            if os.path.isdir(going):
                sys.exit(0)
            fail("NOT_FOUND: job %s" % name)
        # The name stays the platform's for as long as the fixture says, per kind
        # of read: `delete_lists` keeps it listed, `delete_submits` keeps it
        # un-submittable (ALREADY_EXISTS), and a describe answers it in
        # DELETION_IN_PROGRESS while either of them has anything left.
        for kind in ("lists", "submits"):
            left = C.get("delete_" + kind, "0")
            if left != "0":
                os.makedirs(going, exist_ok=True)
                with open(os.path.join(going, kind), "w") as fh:
                    fh.write(left)
        shutil.rmtree(live)
        out("")
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
import os, sys

D = os.environ.get("FAKE831_DIR", "")
args = sys.argv[1:]
out = None
url = ""
i = 0
while i < len(args):
    a = args[i]
    if a == "-o" and i + 1 < len(args):
        i += 1
        out = args[i]
    elif a.startswith("http"):
        url = a
    i += 1
try:
    with open(os.path.join(D, "curl.calls"), "a") as fh:
        fh.write("curl " + " ".join(args) + "\n")
except OSError:
    pass

rest = url.split("://", 1)[1] if "://" in url else url
_, _, path = rest.partition("/")
if path.endswith(".json"):
    path = path[:-5]
base = os.path.join(D, "rtdb", path)
reads = base + ".reads"
n = 0
try:
    n = int(open(reads).read().strip() or "0")
except OSError:
    n = 0
os.makedirs(os.path.dirname(reads), exist_ok=True)
with open(reads, "w") as fh:
    fh.write(str(n + 1))

body = None
status = "200"
if n >= 1 and os.path.exists(base + ".then.json"):
    body = base + ".then.json"
    if os.path.exists(base + ".then.code"):
        status = open(base + ".then.code").read().strip()
else:
    if os.path.exists(base + ".json"):
        body = base + ".json"
    if os.path.exists(base + ".code"):
        status = open(base + ".code").read().strip()
if body is None and status == "200":
    status = "404"
text = open(body).read() if body else ""
if out:
    with open(out, "w") as fh:
        fh.write(text)
sys.stdout.write(status)
CURL_EOF
  chmod +x "$1/bin/curl"

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
import os, sys

D = os.environ.get("FAKE831_DIR", "")
args = sys.argv[1:]
try:
    with open(os.path.join(D, "fugaro.calls"), "a") as fh:
        fh.write("fugaro " + " ".join(args) + "\n")
except OSError:
    pass
knobs = {}
try:
    with open(os.path.join(D, "cfg")) as fh:
        for line in fh:
            line = line.strip()
            if line and "=" in line:
                k, v = line.split("=", 1)
                knobs[k] = v
except OSError:
    pass
if args and args[0] == "version":
    sys.stdout.write("fugaro 0.99.0 (check-831 fixture)\n")
    sys.exit(0)
if args and args[0] == "run":
    if knobs.get("run_fail") == "yes":
        sys.stderr.write("fugaro: the launch was refused\n")
        sys.exit(1)
    # The check launches twice — the run under test, and the second launch whose
    # still-queued job the third attack deletes — and the two are different runs
    # with different ids, jobs and tickets. The answer is served per call, so the
    # second launch is not the first one's answer read again.
    nth = 0
    path = os.path.join(D, "runs.next")
    try:
        nth = int(open(path).read().strip() or "0")
    except OSError:
        nth = 0
    try:
        with open(path, "w") as fh:
            fh.write(str(nth + 1))
    except OSError:
        pass
    answer = "launch.json" if nth == 0 else "launch2.json"
    try:
        sys.stdout.write(open(os.path.join(D, answer)).read())
    except OSError:
        sys.stdout.write("{}\n")
    sys.exit(0)
sys.exit(0)
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ the fixtures
write_tree() { # write_tree DIR [bend]
  local dir=$1 bend=${2:-}
  mkdir -p "$dir/scripts/live-checks" "$dir/internal/identitywire" \
    "$dir/internal/budget" "$dir/internal/backend/batch" "$dir/internal/runner"
  cat >"$dir/internal/identitywire/wire.go" <<'GO_EOF'
package identitywire

// Where the launcher asks for a ticket and where the run claims it.
const (
	TicketPath = "/v1/identity/ticket"
	ClaimPath  = "/v1/identity/claim"
)

// TicketEnv is the name of the execution variable the ticket travels in.
const TicketEnv = "FUGARO_IDENTITY_TICKET"

// pathRecords is the family the issuance records live in.
const pathRecords = "creds/issuance"

func PathRecord(slug, run string) string {
	return pathRecords + "/" + budget.Key(slug) + "/" + budget.Key(run)
}
GO_EOF
  cat >"$dir/internal/budget/model.go" <<'GO_EOF'
package budget

// Key escapes one path segment for RTDB.
func Key(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '.' || c == '$' || c == '#' || c == '[' || c == ']' || c == '/' || c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
GO_EOF
  cat >"$dir/internal/backend/batch/env.go" <<'GO_EOF'
package batch

// The credentials service's URL travels to the job in this variable.
const credentialsServiceEnv = "FUGARO_CREDENTIALS_SERVICE_URL"

func envFor(spec Spec, full, deployedJobName string) map[string]string {
	vars := map[string]string{}
	vars["FUGARO_RUN"] = spec.Repo.Slug + "/" + spec.RunID
	vars["FUGARO_BACKEND"] = "cloud-batch"
	return vars
}
GO_EOF
  cat >"$dir/internal/backend/batch/names.go" <<'GO_EOF'
package batch

// RunLabel is the per-run label the reaper and a follow-up read a job by.
const RunLabel = "fugaro_run"
GO_EOF
  cat >"$dir/internal/backend/batch/logs.go" <<'GO_EOF'
package batch

// The filter lib/google documents for one Batch job's task logs.
func jobFilter(uid string) string {
	return `labels.job_uid=` + uid + ` AND logName="projects/p/logs/batch_task_logs"`
}
GO_EOF
  cat >"$dir/internal/runner/budget_session.go" <<'GO_EOF'
package runner

const (
	RTDBURLEnv        = "FUGARO_RTDB_URL"
	FirebaseAPIKeyEnv = "FUGARO_FIREBASE_API_KEY"
)
GO_EOF
  case "$bend" in
  no-ticket-env) sed -i 's|^const TicketEnv = "FUGARO_IDENTITY_TICKET"$|const TicketEnvGone = "FUGARO_IDENTITY_TICKET"|' "$dir/internal/identitywire/wire.go" ;;
  key-other-set) sed -i "s|c == '/' ||" "$dir/internal/budget/model.go" ;;
  no-run-var) sed -i 's|^	vars\["FUGARO_RUN"\] = spec.Repo.Slug + "/" + spec.RunID$|	_ = full|' "$dir/internal/backend/batch/env.go" ;;
  run-var-other) sed -i 's|vars\["FUGARO_RUN"\] = spec.Repo.Slug + "/" + spec.RunID|vars["FUGARO_RUN"] = spec.Repo.Slug + "-" + spec.RunID|' "$dir/internal/backend/batch/env.go" ;;
  esac
}

write_install_config() { # write_install_config DIR [bend]
  local dir=$1 bend=${2:-}
  mkdir -p "$dir/home/.config/fugaro/projects"
  local project=$P_CHK region=us-east5 backend=cloud-batch
  case "$bend" in
  other-project) project=some-other-project ;;
  other-region) region=europe-west1 ;;
  no-backend) backend="" ;;
  other-backend) backend=cloud-run ;;
  esac
  {
    printf 'name: fugaro-dev\n'
    printf 'gcp_project: %s\n' "$project"
    printf 'region: %s\n' "$region"
    [ -n "$backend" ] && printf 'backend: %s\n' "$backend"
    printf 'runs_bucket: fugaro-dev-runs\n'
    printf 'budget:\n'
    printf '  rtdb_url: %s\n' "$rtdb"
  } >"$dir/home/.config/fugaro/projects/fugaro.yaml"
}

# write_default_case: the platform as Check 831 expects to find it — one running
# run of acme/widget on Batch, its ledger saying it opened its session and then
# that it was metered, a ticket on the job, the ticket in the job's logs and in
# the CreateJob audit record, a second launch whose job is still QUEUED and
# carries that run's own ticket (the one the third attack deletes and re-creates
# under its name), and a database that refuses the second launcher the issuance
# record. MODE bends one thing at a time.
write_default_case() { # write_default_case DIR [mode]
  python3 - "$1/state" "${2:-}" "$slug" "$runid" "$runjob" "$uid" "$runacct" "$repo" \
    "$ticket" "$rtdb" "$service" "$runid2" "$runjob2" "$uid2" "$ticket2" <<'PY'
import json, os, sys

(d, mode, slug, runid, runjob, uid, runacct, repo, ticket, rtdb, service,
 runid2, runjob2, uid2, ticket2) = sys.argv[1:16]

os.makedirs(os.path.join(d, "jobs", runjob), exist_ok=True)
os.makedirs(os.path.join(d, "logs"), exist_ok=True)
os.makedirs(os.path.join(d, "rtdb", "runs", slug), exist_ok=True)
os.makedirs(os.path.join(d, "rtdb", "creds", "issuance", slug), exist_ok=True)
open(os.path.join(d, "cfg"), "w").write("")
open(os.path.join(d, "account"), "w").write("owner@example.com\n")

variables = {
    "FUGARO_RUN": slug + "/" + runid,
    "FUGARO_RTDB_URL": rtdb,
    "FUGARO_CREDENTIALS_SERVICE_URL": service,
    "FUGARO_BACKEND": "cloud-batch",
    "FUGARO_DEPLOYED_JOB": "fugaro-acme-widget-implement",
}
if mode != "no-ticket":
    variables["FUGARO_IDENTITY_TICKET"] = ticket

doc = {
    "name": "projects/fugaro-dev/locations/us-east5/jobs/" + runjob,
    "uid": uid,
    "createTime": "2026-11-22T12:00:00Z",
    "status": {"state": "RUNNING"},
    "labels": {"fugaro_run": slug + "-" + runid, "fugaro_repo": slug},
    "allocationPolicy": {"serviceAccount": {"email": runacct}},
    "taskGroups": [{"taskSpec": {"environment": {"variables": variables}}}],
}
if mode != "no-job":
    with open(os.path.join(d, "jobs", runjob, "job.json"), "w") as fh:
        json.dump(doc, fh, indent=2)

# The second launch of the check: another run of the same repository, whose job
# the third attack deletes. Its state is that attack's own window — QUEUED, and
# the run's VM has therefore never existed, so its ticket is never claimed — and
# a case that needs the window already gone writes mode `second-running`, which
# is the state that makes the row FAIL rather than pass on a re-creation whose
# ticket the run's own bootstrap has spent.
second_variables = {
    "FUGARO_RUN": slug + "/" + runid2,
    "FUGARO_RTDB_URL": rtdb,
    "FUGARO_CREDENTIALS_SERVICE_URL": service,
    "FUGARO_BACKEND": "cloud-batch",
    "FUGARO_DEPLOYED_JOB": "fugaro-acme-widget-implement",
    "FUGARO_IDENTITY_TICKET": ticket2,
}
second = {
    "name": "projects/fugaro-dev/locations/us-east5/jobs/" + runjob2,
    "uid": uid2,
    "createTime": "2026-11-22T12:05:00Z",
    "status": {"state": "RUNNING" if mode == "second-running" else "QUEUED"},
    "labels": {"fugaro_run": slug + "-" + runid2, "fugaro_repo": slug},
    "allocationPolicy": {"serviceAccount": {"email": runacct}},
    "taskGroups": [{"taskSpec": {"environment": {"variables": second_variables}}}],
}
if mode != "no-job":
    os.makedirs(os.path.join(d, "jobs", runjob2), exist_ok=True)
    with open(os.path.join(d, "jobs", runjob2, "job.json"), "w") as fh:
        json.dump(second, fh, indent=2)

with open(os.path.join(d, "launch2.json"), "w") as fh:
    json.dump(
        {
            "run_id": runid2,
            "repo": repo,
            "execution": "projects/fugaro-dev/locations/us-east5/jobs/" + runjob2,
            "status": "launched",
            "log_url": "https://console.cloud.google.com/batch/jobs/" + runjob2,
            "branch": "fugaro/20261122-120500-beef",
        },
        fh,
    )

# The run's own task logs: the container's own lines, one of which carries the
# ticket. That is the model of what Batch prints, and the check measures it.
with open(os.path.join(d, "logs", uid + ".json"), "w") as fh:
    json.dump(
        [
            {"textPayload": "fugaro: run %s starting" % runid},
            {"textPayload": "fugaro: identity ticket %s" % ticket},
            {"textPayload": "fugaro: budget session opened"},
        ],
        fh,
    )

# The CreateJob audit record of this run, told apart by the job's own uid.
with open(os.path.join(d, "audit.json"), "w") as fh:
    json.dump(
        [
            {
                "protoPayload": {
                    "methodName": "google.cloud.batch.v1.BatchService.CreateJob",
                    "resourceName": "projects/fugaro-dev/locations/us-east5/jobs/" + runjob,
                },
                "textPayload": "CreateJob %s uid=%s variables FUGARO_IDENTITY_TICKET=%s"
                % (runjob, uid, ticket),
            }
        ],
        fh,
    )
if mode == "no-audit":
    open(os.path.join(d, "audit.json"), "w").write("[]")

with open(os.path.join(d, "launch.json"), "w") as fh:
    json.dump(
        {
            "run_id": runid,
            "repo": repo,
            "execution": "projects/fugaro-dev/locations/us-east5/jobs/" + runjob,
            "status": "launched",
            "log_url": "https://console.cloud.google.com/batch/jobs/" + runjob,
            "branch": "fugaro/20261122-120000-abcd",
        },
        fh,
    )

# The run's ledger: it opens its session first, and the meter moves after.
ledger = os.path.join(d, "rtdb", "runs", slug, runid)
with open(ledger + ".json", "w") as fh:
    json.dump({"reserved": 250000, "released": 0, "exp": 1795900000000}, fh)
with open(ledger + ".then.json", "w") as fh:
    json.dump(
        {"reserved": 250000, "released": 0, "exp": 1795900000000, "spent": 12000, "tokens": 4321},
        fh,
    )
if mode == "ledger-403":
    with open(ledger + ".code", "w") as fh:
        fh.write("403")
    with open(ledger + ".then.code", "w") as fh:
        fh.write("403")
if mode == "never-metered":
    with open(ledger + ".then.json", "w") as fh:
        json.dump({"reserved": 250000, "released": 0, "exp": 1795900000000}, fh)

record = os.path.join(d, "rtdb", "creds", "issuance", slug, runid)
with open(record + ".json", "w") as fh:
    fh.write('{"error":"Permission denied"}')
with open(record + ".code", "w") as fh:
    fh.write("403")
if mode == "record-200":
    with open(record + ".json", "w") as fh:
        fh.write(
            '{"ticket_digest":"deadbeef","account":"%s","job":"%s","job_id":"%s"}'
            % (runacct, runjob, uid)
        )
    with open(record + ".code", "w") as fh:
        fh.write("200")
if mode == "record-404":
    with open(record + ".code", "w") as fh:
        fh.write("404")
if mode == "record-500":
    with open(record + ".code", "w") as fh:
        fh.write("500")
PY
  case "${2:-}" in
  claim-token) printf 'claim_token=yes\n' >>"$1/state/cfg" ;;
  claim-500) printf 'claim_status=500\nclaim_error=none\n' >>"$1/state/cfg" ;;
  no-replay-lines) printf 'no_lines.%s=yes\n' "$job_replay" >>"$1/state/cfg" ;;
  submit-fail) printf 'submit_fail=yes\n' >>"$1/state/cfg" ;;
  describe-fail) printf 'impersonate_fail.describe=yes\n' >>"$1/state/cfg" ;;
  delete-fail) printf 'impersonate_fail.delete=yes\n' >>"$1/state/cfg" ;;
  delete-always) printf 'delete_fail.%s=yes\n' "$job_replay" >>"$1/state/cfg" ;;
  delete-lists) printf 'delete_lists=2\n' >>"$1/state/cfg" ;;
  # A name the platform is still reaping, for the re-creation's own read: the
  # submit under it is refused ALREADY_EXISTS for the first two attempts, which
  # is the platform's answer and not the third attack's refusal.
  rename-slow-submit) printf 'delete_submits=2\n' >>"$1/state/cfg" ;;
  # The refusal the route answers WITHOUT asking the link: the ticket is already
  # spent, and the run's own bootstrap is what spent it (the check waits for the
  # ledger to say the run opened its session and was metered, which is after the
  # run's claim). A tree with no VM-to-job link at all answers exactly this, so
  # a row that read it as its own refusal would record a PASS proving nothing.
  fence) printf 'claim_error=identity_refused\nclaim_message=the ticket was already claimed: a ticket is claimed once, and this claim is not the one that claimed it\nclaim_message.%s=this record names a job and the claim carries no attestation field\n' "$job_noticket" >>"$1/state/cfg"
    # The re-created job carries the SECOND launch's ticket, and that run's VM
    # never existed — its job was deleted while it was queued — so the ticket is
    # unspent: a tree that reads no attestation serves the claim, which is the
    # hole the third attack is for (the record's own job id is the only thing
    # left, D3108, F30, and nothing reads it). The fence is the first attack's.
    printf 'claim_token.%s=yes\n' "$runjob2" >>"$1/state/cfg" ;;
  # The link is not wired at all: a claim the service cannot judge is answered
  # 'not served' and nothing falls back (D3114).
  not-served) printf 'claim_status=503\nclaim_error=not_served\nclaim_message=this service does not issue a run identity: no issuer is configured on this installation\n' >>"$1/state/cfg" ;;
  # A refusal that is not the claim route's own decision about a claim it read:
  # the caller's class, in the shape refuseIdentityCaller writes it.
  claim-other) printf 'claim_error=caller_not_admitted\nclaim_message=this route admits a run own job service account\n' >>"$1/state/cfg" ;;
  run-fail) printf 'run_fail=yes\n' >>"$1/state/cfg" ;;
  # The second launcher's OWN read of the run's job is what the first attack has
  # to take the ticket it replays out of: a read that carries no ticket, and one
  # that carries a different ticket from the one this check read as the owner,
  # are both the row's FAIL rather than a replay of the check's own reading.
  second-read-no-ticket) printf 'second_ticket.%s=none\n' "$runjob" >>"$1/state/cfg" ;;
  second-read-other-ticket) printf 'second_ticket.%s=%s\n' "$runjob" "$ticket2" >>"$1/state/cfg" ;;
  esac
}

setup_case() { # setup_case NAME [tree-bend] [mode]
  dir=$work/$1
  rm -rf "$dir"
  mkdir -p "$dir/state" "$dir/tmp" "$dir/home"
  write_fakes "$dir"
  write_tree "$dir/tree" "${2:-}"
  cp "$script" "$dir/tree/scripts/live-checks/check-831.sh"
  write_install_config "$dir" "${3:-}"
  out=""
  err=""
  status=0
  RUN_TMPDIR=""
  RUN_OWNER=yes
  BASH_FLAGS=""
  EXTRA_ENV=()
}

out=""
err=""
status=0
# The knobs a case turns. RUN_OWNER=no is the owner guard's case and
# BASH_FLAGS=-x the xtrace guard's; every waiting bound is zero, so a case that
# models an absence models it at once rather than in real minutes — except the
# sweep's, which is two seconds: a case whose job the platform keeps listing
# after the delete is waiting that out within the bound, and two seconds is the
# shortest bound a case can tell a wait from a single read in.
RUN_OWNER=yes
BASH_FLAGS=""
EXTRA_ENV=()
run_check() { # run_check [K=V ...] [-- ARGS...]
  local envs=() args=() have_args=no
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    have_args=yes
    shift
    args=("$@")
  fi
  if [ "$have_args" = no ]; then
    args=(--second-launcher "$second" --repo "$repo")
  fi
  local base=(
    PATH="$dir/bin:$PATH"
    HOME="$dir/home"
    TMPDIR="${RUN_TMPDIR:-$dir/tmp}"
    FAKE831_DIR="$dir/state"
    CHK831_CONFIG="$dir/home/.config/fugaro/projects/fugaro.yaml"
    CHK831_WAIT_JOB=0
    CHK831_WAIT_SESSION=0
    CHK831_WAIT_METERED=0
    CHK831_WAIT_ATTACK=0
    CHK831_WAIT_AUDIT=0
    # A case that makes the platform hold a deleted job's name must give the
    # re-creation its own bound, as the check's own comment says: zero is the
    # fail-closed default here, so a case that forgot it reports the FAIL rather
    # than spinning.
    CHK831_WAIT_RENAME=0
    CHK831_RENAME_POLL=0
    CHK831_WAIT_QUEUED=0
    CHK831_QUEUE_POLL=0
    CHK831_SWEEP_BOUND=2
    CHK831_SWEEP_POLL=0
  )
  [ "$RUN_OWNER" = yes ] && base+=(CHK831_OWNER_RUN=yes)
  set +e
  out=$(cd "$dir" && env -i "${base[@]}" ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    "${envs[@]}" bash $BASH_FLAGS "$dir/tree/scripts/live-checks/check-831.sh" \
    ${args[@]+"${args[@]}"} 2>"$dir/stderr")
  status=$?
  set -e
  err=$(cat "$dir/stderr" 2>/dev/null || true)
}

block_line() { # block_line N — one line of the RESULT block, 1-based
  printf '%s\n' "$out" | sed -n "$1p"
}
last_line() { printf '%s\n' "$out" | grep . | tail -n 1; }
calls() { cat "$dir/state/gcloud.calls" 2>/dev/null || true; }
curl_calls() { cat "$dir/state/curl.calls" 2>/dev/null || true; }
fugaro_calls() { cat "$dir/state/fugaro.calls" 2>/dev/null || true; }
expect_call() { # what
  if ! grep -qF -- "$1" "$dir/state/gcloud.calls" 2>/dev/null; then
    fail "the check never called 'gcloud $1'; calls were: $(head -c 900 "$dir/state/gcloud.calls" 2>/dev/null || echo none)"
  fi
}
expect_no_call() {
  if grep -qF -- "$1" "$dir/state/gcloud.calls" 2>/dev/null; then
    fail "the check called 'gcloud $1' and it must not have"
  fi
}
# expect_no_cloud: the check reached no project. `gcloud config get-value
# account` is the one call a guard may make — it reads the local configuration to
# refuse the second launcher being the owner — so what is refused here is every
# call that would touch a project: Batch, Logging, an access token, curl and
# fugaro.
expect_no_cloud() { # what
  local hit
  hit=$(grep -E 'batch |logging |auth print-access-token' "$dir/state/gcloud.calls" 2>/dev/null || true)
  if [ -n "$hit" ]; then
    fail "$1: the check reached the cloud anyway (gcloud: $(printf '%s' "$hit" | head -c 300))"
  fi
  local f
  for f in curl.calls fugaro.calls; do
    if [ -s "$dir/state/$f" ]; then
      fail "$1: the check reached the cloud anyway ($f: $(head -c 300 "$dir/state/$f"))"
    fi
  done
}
expect_last_line() { # want what
  [ "$(last_line)" = "$1" ] || fail "$2: expected the last line '$1', got '$(last_line)'"
}
expect_no_unreached() { # what
  expect_absent "$out" "not reached" "$1: a row that was not reached"
}
# nothing_left: every job the check made is gone — none is listed, none is still
# being deleted, and no directory is left under either tree.
nothing_left() { # what
  local names
  names=$(find "$dir/state/jobs" -mindepth 1 -maxdepth 1 -type d 2>/dev/null || true)
  if [ -n "$names" ]; then
    fail "$1: jobs left behind: $(printf '%s' "$names" | tr '\n' ' ')"
  fi
  # A `deleting/<name>` entry is the platform's own state and not a job this
  # check left: the job is already gone from `jobs/`, because a delete MOVES it
  # there. What is asserted is that no job is left, and the cases that are about
  # a name the platform is still reaping assert the `restore` row's reading
  # beside this one.
}
# submitted: the job configuration the check sent for one job, as JSON.
submitted() { cat "$dir/state/submits/$1.json" 2>/dev/null || true; }
expect_submitted_field() { # job jq-ish-path needle what
  local body
  body=$(submitted "$1")
  if [ -z "$body" ]; then
    fail "$3: the check never submitted $1"
    return 0
  fi
  expect_contains "$body" "$2" "$3"
}
expect_curl_token() { # which-url-fragment token what
  local line
  line=$(grep -F -- "$1" "$dir/state/curl.calls" 2>/dev/null | head -n 1 || true)
  if [ -z "$line" ]; then
    fail "$3: the check never read $1"
    return 0
  fi
  expect_contains "$line" "Bearer $2" "$3"
}
# A ticket is a handle and not a secret (D3115), and the check reads two of them
# — the run-under-test's out of its job, and the second launch's out of the job
# the third attack deletes — so no line of the block, the progress log, a gcloud
# command line or a fugaro command line may carry either value.
expect_no_ticket_anywhere() { # what
  local t
  for t in "$ticket" "$ticket2"; do
    expect_absent "$out" "$t" "$1: the block"
    expect_absent "$err" "$t" "$1: the progress log"
    expect_absent "$(calls)" "$t" "$1: a gcloud command line"
    expect_absent "$(fugaro_calls)" "$t" "$1: a fugaro command line"
  done
}
# --------------------------------------------------------------- the run, end to end
test_help() {
  setup_case help
  run_check -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check-831.sh" "help: the usage"
  expect_no_cloud "help"
}

test_dry_run() {
  setup_case dry-run
  run_check -- --dry-run --second-launcher "$second" --repo "$repo"
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row"
  expect_contains "$err" "fugaro run --repo $repo" "dry run: the launch it would make"
  expect_contains "$err" "batch jobs submit $job_replay" "dry run: the replay it would make"
  expect_contains "$out" "CHECK 831 RESULT: PASS" "dry run: the block"
  expect_no_cloud "dry run"
}

test_happy_path() {
  setup_case happy
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "happy"
  expect_last_line "CHECK 831 RESULT: PASS" "happy"
  expect_no_unreached "happy"
  expect_verdict xtrace PASS "happy: no trace"
  expect_verdict owner PASS "happy: the owner marker"
  expect_row tree "the ticket's variable FUGARO_IDENTITY_TICKET" "happy: the ticket's variable is read out of the tree"
  expect_row tree "the claim's route /v1/identity/claim" "happy: the claim's route is read out of the tree"
  expect_row tree "the record's path family creds/issuance" "happy: the record's path family is read out of the tree"
  expect_verdict installation PASS "happy: the installation"
  expect_verdict launch PASS "happy: the launch"
  expect_verdict job-found PASS "happy: the job"
  expect_row job-found "$slug/$runid" "happy: the job is held to the run by its own variable"
  expect_row job-found "running as $runacct" "happy: the account the job runs as"
  expect_verdict session PASS "happy: the session opened"
  expect_row session "reserved 250000 micros" "happy: the reserved figure"
  expect_verdict metered PASS "happy: metered"
  expect_row metered "12000 micros" "happy: the spend"
  expect_row metered "4321 tokens" "happy: the tokens"
  expect_verdict launch-ticket PASS "happy: the ticket is on the job"
  expect_verdict ticket-printed PASS "happy: where Batch printed it"
  expect_row ticket-printed "the run's task logs" "happy: the logs are named as a place it was found"
  expect_row ticket-printed "CreateJob audit record" "happy: the audit record is named too"
  expect_verdict attack-replay PASS "happy: the replay is refused"
  expect_row attack-replay "refusal identity_refused" "happy: the refusal is the service's own code"
  expect_verdict attack-noticket PASS "happy: the control"
  expect_verdict attack-rename PASS "happy: the re-creation is refused"
  expect_row attack-rename "a second run's job was deleted while it was QUEUED" "happy: the queued window the plan asks to be measured, and the run it belongs to"
  expect_verdict attack-record PASS "happy: the record read is refused"
  expect_verdict cost PASS "happy: the cost"
  expect_verdict restore PASS "happy: nothing left"
  expect_row restore "every job this check made is deleted" "happy: the sweep"
  expect_no_ticket_anywhere "happy"
  # The four attacks were made as the second launcher, and the reading that
  # matters was made AS it: the run's ticket is taken out of the document the
  # second launcher's own read of the job answered (--format=json, the whole
  # document), the second launch's job is read the same way, and the three
  # submits and the record's read all carry the impersonation.
  expect_call "batch jobs describe $runjob --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second --format=json"
  expect_call "batch jobs describe $runjob2 --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second --format=json"
  expect_call "batch jobs submit $job_replay --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second"
  expect_call "batch jobs submit $job_noticket --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second"
  expect_call "batch jobs delete $runjob2 --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second --quiet"
  expect_call "batch jobs submit $runjob2 --project=fugaro-dev --location=us-east5 --impersonate-service-account=$second"
  # Two runs, and neither is launched twice.
  local launches
  launches=$(grep -c "^fugaro run " <<<"$(fugaro_calls)")
  [ "$launches" = 2 ] || fail "happy: the check launched $launches runs, want 2 (the run under test and the second launch)"
  # The ledger is read with your own token — the installation owner's, which the
  # database treats as an administrator — and the record with the second
  # launcher's, which is the caller the rules answer.
  expect_curl_token "runs/$slug/$runid.json" token-owner "happy: the ledger is read as the owner"
  expect_curl_token "creds/issuance/$slug/$runid.json" token-second "happy: the record is read as the second launcher"
  nothing_left "happy"
  # The two runs' jobs and the three attack jobs are all deleted by the steps
  # that made them: the sweep has nothing left to do.
  expect_row restore "nothing of this check's is left in fugaro-dev" "happy: the sweep found nothing"
}

# The red step of BR13, as the plan states it: "the self-test fails when the
# replay step is removed". The copy below calls step_attack_replay out of the run
# (its line becomes a no-op, so the copy still parses), and the same platform
# answers are then no longer a PASS — which is exactly what the happy case above
# asserts, so a script that dropped the replay fails this suite.
test_the_replay_is_what_makes_the_row() {
  setup_case replay-mutation
  write_default_case "$dir"
  local copy=$dir/tree/scripts/live-checks/check-831.sh
  sed 's|^\( *\)step_attack_replay$|\1: # the replay, omitted on purpose|' "$script" >"$copy" || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the line that runs the replay was not found to neutralise"
    return 0
  fi
  run_check
  expect_status "$status" 1 "red step"
  expect_verdict attack-replay FAIL "red step: without the attack the row cannot pass"
  expect_contains "$out" "not reached (the run stopped before this step)" "red step: the row says it was not measured"
  expect_no_call "batch jobs submit $job_replay"
  # Everything else the platform answered for is still a PASS: it is the replay
  # attempt, and only it, that the row rests on.
  expect_verdict attack-noticket PASS "red step: the control is still made"
  expect_verdict attack-rename PASS "red step: the re-creation is still made"
  expect_verdict attack-record PASS "red step: the record read is still made"
}

test_a_run_whose_job_never_appears_fails_job_found() {
  setup_case no-job
  write_default_case "$dir" no-job
  run_check
  expect_status "$status" 1 "no job"
  expect_verdict job-found FAIL "no job: the row"
  expect_row job-found "no Batch job whose own FUGARO_RUN variable names run" "no job: what was looked for"
  expect_verdict session FAIL "no job: the ledger's key is the job's to state"
  expect_verdict metered FAIL "no job: nothing was metered"
  expect_verdict launch-ticket FAIL "no job: no ticket was read"
  expect_verdict attack-replay FAIL "no job: nothing to replay"
  expect_verdict attack-rename FAIL "no job: no name to re-create under"
  expect_verdict attack-record FAIL "no job: no path to read"
  expect_verdict cost FAIL "no job: the cost is unread"
  expect_no_call "batch jobs submit $job_replay"
}

test_a_job_carrying_no_ticket_fails_the_replay_and_the_rename() {
  setup_case no-ticket
  write_default_case "$dir" no-ticket
  run_check
  expect_status "$status" 1 "no ticket"
  expect_verdict launch-ticket FAIL "no ticket: the row"
  expect_row launch-ticket "does not put an identity ticket on the job" "no ticket: the launcher's half is named as the cause"
  expect_verdict ticket-printed FAIL "no ticket: nothing to find"
  expect_verdict attack-replay FAIL "no ticket: nothing to replay"
  expect_verdict attack-rename FAIL "no ticket: nothing to carry into a re-created job"
  # The control is still made: it is what the other attacks are read beside.
  expect_verdict attack-noticket PASS "no ticket: the control"
  expect_call "batch jobs submit $job_noticket"
  expect_no_call "batch jobs submit $job_replay"
}

test_a_claim_that_comes_back_with_a_token_is_the_finding() {
  setup_case claim-token
  write_default_case "$dir" claim-token
  run_check
  expect_status "$status" 1 "a token"
  expect_verdict attack-replay FAIL "a token: the row"
  expect_row attack-replay "OBTAINED the run's identity" "a token: the finding is named"
  expect_verdict attack-rename FAIL "a token: the re-creation obtained it too"
  expect_verdict attack-record PASS "a token: the record is still refused"
}

test_a_claim_that_nobody_decided_is_not_a_refusal() {
  setup_case claim-500
  write_default_case "$dir" claim-500
  run_check
  expect_status "$status" 1 "a 500"
  expect_verdict attack-replay FAIL "a 500: the row"
  expect_row attack-replay "nothing decided it (HTTP 500)" "a 500: an outage is not a refusal"
}

# The finding the two claim rows were fixed for. The check waits for the ledger
# to say the run opened its session and was metered, and the run's own bootstrap
# has claimed the ticket by then — so a service that never asks whether the VM is
# the job's (this tree, until BR2 and BR3 merge) refuses the replay with the
# once-only fence, and a row that read any non-5xx refusal as its own would print
# a PASS on a tree with no VM-to-job link at all. The re-creation is the other
# half of the same tree: it carries the SECOND launch's ticket, which no VM ever
# spent, so a route that reads no attestation SERVES it — the identity of a run
# whose job was deleted while it was still queued, handed to a job the second
# launcher re-created under that name.
test_a_refusal_the_route_answers_without_the_link_is_not_the_rows() {
  setup_case fence
  write_default_case "$dir" fence
  run_check
  expect_status "$status" 1 "the fence"
  expect_verdict attack-replay FAIL "the fence: the row"
  expect_row attack-replay "the ticket was already claimed" "the fence: the service's own sentence is named"
  expect_row attack-replay "WITHOUT asking whether the VM is the job's" "the fence: the row says what the refusal is not"
  expect_verdict attack-rename FAIL "the fence: the re-created job's claim is served"
  expect_row attack-rename "OBTAINED the run's identity" "the fence: the third attack's finding is named"
  expect_verdict attack-noticket PASS "the fence: the control's own refusal is its own"
}

test_a_claim_the_service_cannot_judge_is_not_a_refusal() {
  setup_case not-served
  write_default_case "$dir" not-served
  run_check
  expect_status "$status" 1 "not served"
  expect_verdict attack-replay FAIL "not served: the row"
  expect_row attack-replay "answered 'not_served'" "not served: the code is named"
  expect_row attack-replay "link from a VM to the job the record names is not wired" "not served: the row says the link is the missing thing"
  expect_verdict attack-rename FAIL "not served: the re-creation's claim too"
}

test_a_refusal_that_is_not_the_routes_own_is_not_measured() {
  setup_case claim-other
  write_default_case "$dir" claim-other
  run_check
  expect_status "$status" 1 "another refusal"
  expect_verdict attack-replay FAIL "another refusal: the row"
  expect_row attack-replay "not by the claim route's own decision" "another refusal: the row says so"
  expect_row attack-replay "caller_not_admitted" "another refusal: the code is named"
}

test_the_refusal_the_row_passes_on_is_the_services_own_sentence() {
  setup_case words
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "the words"
  expect_verdict attack-replay PASS "the words: the row"
  expect_row attack-replay "not a VM of the job the record names" "the words: the service's sentence is recorded"
  expect_row attack-replay "the refusals this route answers before it asks the link" "the words: the row says what a PASS excludes"
}

# The third attack deletes the run's job and re-creates one under the same name,
# and a Batch delete is asynchronous: the name stays the platform's until it has
# finished with the job, and a submit under it is refused ALREADY_EXISTS. That
# refusal is the platform's, and reading it as the attack's would report the
# attack refused without ever making it — on the owner's one paying run.
test_a_name_the_platform_is_still_reaping_is_waited_for() {
  setup_case rename-slow-submit
  write_default_case "$dir" rename-slow-submit
  run_check CHK831_WAIT_RENAME=8
  expect_status "$status" 0 "a slow submit"
  expect_verdict attack-rename PASS "a slow submit: the attack is made once the name is free"
  expect_row attack-rename "re-created under the same name" "a slow submit: the re-creation happened"
  expect_contains "$err" "still the platform's to reap" "a slow submit: the wait is reported"
  expect_contains "$err" "waiting, 0s of 8s" "a slow submit: the bound is named"
  # The re-creation of the SECOND run's job was attempted three times: the
  # platform refused the first two, and the third was made.
  local tries
  tries=$(printf '%s\n' "$(calls)" | grep -c -- "batch jobs submit $runjob2 ")
  [ "$tries" = 3 ] || fail "a slow submit: the re-creation was submitted $tries times, want 3 (two refusals, then the create)"
  expect_verdict restore PASS "a slow submit: nothing left"
  nothing_left "a slow submit"
}

test_a_name_the_platform_never_frees_is_not_the_attacks_refusal() {
  setup_case rename-held
  write_default_case "$dir" rename-slow-submit
  run_check CHK831_WAIT_RENAME=0
  expect_status "$status" 1 "a name the platform keeps"
  expect_verdict attack-rename FAIL "a name the platform keeps: the row"
  expect_row attack-rename "the name is still the platform's after" "a name the platform keeps: the bound is named"
  expect_row attack-rename "ALREADY_EXISTS" "a name the platform keeps: the platform's own answer is quoted"
  expect_verdict attack-replay PASS "a name the platform keeps: the replay is unaffected"
}

test_an_attack_that_reported_nothing_is_not_judged() {
  setup_case no-lines
  write_default_case "$dir" no-replay-lines
  run_check
  expect_status "$status" 1 "no lines"
  expect_verdict attack-replay FAIL "no lines: the row"
  expect_row attack-replay "no line of its own came back" "no lines: the attack was not judged"
  expect_verdict attack-noticket PASS "no lines: the control still reported"
}

test_the_second_launcher_must_be_able_to_read_the_job() {
  setup_case describe-fail
  write_default_case "$dir" describe-fail
  run_check
  expect_status "$status" 1 "a describe the second launcher cannot make"
  expect_verdict attack-replay FAIL "describe: the row"
  expect_row attack-replay "could not read the run's ticket out of its job" "describe: the attack was not made"
  expect_no_call "batch jobs submit $job_replay"
  # The third attack reads the second run's job the same way, so a describe the
  # second launcher cannot make leaves it with nothing to delete and nothing to
  # carry into a re-creation: it is not made either.
  expect_verdict attack-rename FAIL "describe: the third attack's row"
  expect_row attack-rename "could not be read as the second launcher" "describe: the third attack was not made"
  expect_no_call "batch jobs delete $runjob2 --project=$P_CHK --location=us-east5 --impersonate-service-account=$second"
}

test_the_second_launcher_must_be_able_to_submit() {
  setup_case submit-fail
  write_default_case "$dir" submit-fail
  run_check
  expect_status "$status" 1 "a submit that fails"
  expect_verdict attack-replay FAIL "submit: the row"
  expect_row attack-replay "could not submit its own job" "submit: the attack was not made"
  expect_verdict attack-noticket FAIL "submit: the control was not made either"
}

test_the_second_launcher_must_be_able_to_delete() {
  setup_case delete-fail
  write_default_case "$dir" delete-fail
  run_check
  expect_status "$status" 1 "a delete the second launcher cannot make"
  expect_verdict attack-rename FAIL "delete: the row"
  expect_row attack-rename "could not delete the second run's job" "delete: the attack was not made"
  # The job the second launcher could not delete is still there, and the sweep
  # (which deletes as the owner) is what removes it: nothing is left behind.
  nothing_left "delete: the sweep still cleared it"
  expect_verdict restore PASS "delete: nothing left"
}

# The replay's own premise: what the second launcher carries has to be the ticket
# IT found in the run's job. Two bends of the impersonated read show that the
# check measures that rather than replaying the launcher's own reading of it: a
# read that answers the job without a ticket, and one that answers a different
# ticket from the owner's read of it, are both the row's FAIL and neither is
# submitted.
test_the_replay_carries_the_ticket_the_second_launcher_read() {
  setup_case second-read-no-ticket
  write_default_case "$dir" second-read-no-ticket
  run_check
  expect_status "$status" 1 "no ticket as the second launcher"
  expect_verdict attack-replay FAIL "no ticket as the second launcher: the row"
  expect_row attack-replay "could not read the run's ticket out of its job" "no ticket as the second launcher: the reading is named as the missing thing"
  expect_no_call "batch jobs submit $job_replay"
  # The third attack reads the SECOND run's job, which the bend does not touch:
  # it is still made.
  expect_verdict attack-rename PASS "no ticket as the second launcher: the re-creation is unaffected"
}

test_a_ticket_the_second_launcher_reads_another_one_of_is_not_the_replay() {
  setup_case second-read-other-ticket
  write_default_case "$dir" second-read-other-ticket
  run_check
  expect_status "$status" 1 "another ticket as the second launcher"
  expect_verdict attack-replay FAIL "another ticket as the second launcher: the row"
  expect_row attack-replay "is not the ticket this check read out of that job as the owner" "another ticket as the second launcher: the two readings are held together"
  expect_no_call "batch jobs submit $job_replay"
  expect_verdict attack-rename PASS "another ticket as the second launcher: the re-creation is unaffected"
}

# The whole point of the third attack's window: the job it deletes must still be
# QUEUED, because a run whose VM has come up has claimed its ticket already — and
# the re-creation, carrying a ticket that is already spent, would be refused for
# the ticket rather than by the record's job id, which is the one thing this
# attack measures (D3108, F30). A job the read answers past QUEUED is therefore
# this row's FAIL, and the attack is not made at all.
test_the_rename_row_fails_when_the_window_has_gone() {
  setup_case second-running
  write_default_case "$dir" second-running
  run_check
  expect_status "$status" 1 "the window gone"
  expect_verdict attack-rename FAIL "the window gone: the row"
  expect_row attack-rename "was already RUNNING when this attack read it" "the window gone: the state the read answered is named"
  expect_row attack-rename "NOT deleted while it was queued" "the window gone: the row says the attack was not made"
  # Nothing was deleted as the second launcher and nothing was re-created: the
  # job the read answered past QUEUED is the owner's sweep's to remove.
  expect_no_call "batch jobs delete $runjob2 --project=$P_CHK --location=us-east5 --impersonate-service-account=$second"
  expect_no_call "batch jobs submit $runjob2"
  nothing_left "the window gone: the sweep still cleared it"
  expect_verdict restore PASS "the window gone: nothing left"
}

test_a_record_the_second_launcher_can_read_is_the_finding() {
  setup_case record-200
  write_default_case "$dir" record-200
  run_check
  expect_status "$status" 1 "a record the second launcher can read"
  expect_verdict attack-record FAIL "record 200: the row"
  expect_row attack-record "READ the issuance record" "record 200: the finding is named"
}

test_a_record_that_is_not_there_is_not_measured() {
  setup_case record-404
  write_default_case "$dir" record-404
  run_check
  expect_status "$status" 1 "a record that is not there"
  expect_verdict attack-record FAIL "record 404: the row"
  expect_row attack-record "answered HTTP 404" "record 404: the attack was not made"
}

test_a_record_read_nobody_decided_is_not_a_refusal() {
  setup_case record-500
  write_default_case "$dir" record-500
  run_check
  expect_status "$status" 1 "a record read nobody decided"
  expect_verdict attack-record FAIL "record 500: the row"
  expect_row attack-record "did not decide the second launcher's read" "record 500: a read nobody decided is not a refusal"
}

test_the_ledger_must_say_the_run_opened_its_session() {
  setup_case ledger-403
  write_default_case "$dir" ledger-403
  run_check
  expect_status "$status" 1 "a ledger the owner cannot read"
  expect_verdict session FAIL "ledger 403: the row"
  expect_row session "HTTP 403" "ledger 403: the status is named"
  expect_verdict metered FAIL "ledger 403: nothing was read"
  expect_verdict cost FAIL "ledger 403: the cost is unread"
  # The job and its ticket were still read: it is the ledger, and only it, that
  # could not be.
  expect_verdict job-found PASS "ledger 403: the job is still read"
  expect_verdict launch-ticket PASS "ledger 403: the ticket is still read"
}

test_a_run_that_is_never_metered_fails_the_metered_row() {
  setup_case never-metered
  write_default_case "$dir" never-metered
  run_check
  expect_status "$status" 1 "a run that is never metered"
  expect_verdict session PASS "never metered: the session opened"
  expect_verdict metered FAIL "never metered: the row"
  expect_row metered "nothing says the run was metered" "never metered: the reading was not taken"
  expect_verdict cost PASS "never metered: the cost is still readable"
}

test_the_audit_record_that_was_never_read_fails_the_print_row() {
  setup_case no-audit
  write_default_case "$dir" no-audit
  run_check
  expect_status "$status" 1 "an audit record that was never read"
  expect_verdict ticket-printed FAIL "no audit: the row"
  expect_row ticket-printed "Absence is not established by a record nobody read" "no audit: fail-closed, and said so"
}

test_the_installation_must_be_this_project() {
  setup_case other-project "" other-project
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "another project"
  expect_verdict installation FAIL "another project: the row"
  expect_row installation "some-other-project" "another project: the project it found is named"
  expect_no_cloud "another project"
}

test_the_installation_must_be_on_batch() {
  setup_case other-backend "" other-backend
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "another backend"
  expect_verdict installation FAIL "another backend: the row"
  expect_row installation "backend 'cloud-run'" "another backend: the backend it found is named"
  expect_no_cloud "another backend"
}

test_the_installation_that_states_no_backend_is_read_from_the_job() {
  setup_case no-backend "" no-backend
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "no backend stated"
  expect_row installation "backend not stated in the file" "no backend: the row says so"
  expect_verdict job-found PASS "no backend: the job is what says it was Batch"
}

test_the_owner_marker_is_required() {
  setup_case owner
  write_default_case "$dir"
  RUN_OWNER=no run_check
  expect_status "$status" 2 "the owner marker"
  expect_verdict owner FAIL "the owner marker: the row"
  expect_contains "$out" "FACT aborted: FAIL" "the owner marker: the block says it was stopped"
  expect_no_cloud "the owner marker"
}

test_belong_is_refused_by_name() {
  setup_case belong
  write_default_case "$dir"
  EXTRA_ENV=(CLOUDSDK_CORE_PROJECT=belong-prod)
  run_check
  EXTRA_ENV=()
  expect_status "$status" 2 "belong"
  expect_verdict belong FAIL "belong: the row"
  expect_contains "$out" "belong, EdgeWeb, EdgeServer" "belong: the refusal names them"
  expect_no_cloud "belong"
}

test_the_second_launcher_is_required() {
  setup_case no-second
  write_default_case "$dir"
  run_check -- --repo "$repo"
  expect_status "$status" 2 "no second launcher"
  expect_verdict belong FAIL "no second launcher: the row"
  expect_row belong "no second launcher was named" "no second launcher: the row says why"
  expect_no_cloud "no second launcher"
}

test_the_second_launcher_cannot_be_the_owner() {
  setup_case second-is-owner
  write_default_case "$dir"
  run_check -- --second-launcher "$owner" --repo "$repo"
  expect_status "$status" 2 "the second launcher is the owner"
  expect_verdict belong FAIL "the second launcher is the owner: the row"
  expect_row belong "is the account this check is authenticated as" "the second launcher is the owner: the row says why"
  expect_no_cloud "the second launcher is the owner"
}

test_the_repository_is_required() {
  setup_case no-repo
  write_default_case "$dir"
  run_check -- --second-launcher "$second"
  expect_status "$status" 2 "no repository"
  expect_contains "$err" "no sandbox repository was named" "no repository: the refusal"
  expect_no_cloud "no repository"
}

test_the_repository_must_be_owner_slash_name() {
  setup_case bad-repo
  write_default_case "$dir"
  run_check -- --second-launcher "$second" --repo widget
  expect_status "$status" 2 "a bare repository name"
  expect_contains "$err" "is not owner/name" "a bare repository name: the refusal"
  expect_no_cloud "a bare repository name"
}

test_xtrace_is_refused() {
  setup_case xtrace
  write_default_case "$dir"
  BASH_FLAGS=-x run_check
  expect_status "$status" 2 "xtrace"
  expect_verdict xtrace FAIL "xtrace: the row"
  expect_no_cloud "xtrace"
}

test_a_scratch_inside_a_work_tree_is_refused() {
  setup_case scratch-in-tree
  write_default_case "$dir"
  git -C "$dir/tree" init -q 2>/dev/null || true
  mkdir -p "$dir/tree/tmp"
  RUN_TMPDIR="$dir/tree/tmp" run_check
  expect_status "$status" 2 "a scratch inside a work tree"
  expect_contains "$err" "is inside a git work tree" "a scratch inside a work tree: the refusal"
  expect_no_cloud "a scratch inside a work tree"
}

test_a_tree_without_the_ticket_variable_stops() {
  setup_case tree-no-ticket no-ticket-env
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "a tree without the ticket's variable"
  expect_contains "$err" "no longer carries TicketEnv" "a tree without the ticket's variable: the refusal"
  expect_contains "$out" "FACT aborted: FAIL" "a tree without the ticket's variable: the block says it was stopped"
  expect_no_cloud "a tree without the ticket's variable"
}

test_a_tree_whose_key_escapes_another_set_stops() {
  setup_case tree-key key-other-set
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "a tree whose Key escapes another set"
  expect_contains "$err" "no longer escapes the set" "a tree whose Key escapes another set: the refusal"
  expect_no_cloud "a tree whose Key escapes another set"
}

test_a_tree_without_the_runs_own_variable_stops() {
  setup_case tree-no-run no-run-var
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "a tree without the run's own variable"
  expect_contains "$err" "no longer writes the run's own variable" "a tree without the run's own variable: the refusal"
  expect_no_cloud "a tree without the run's own variable"
}

test_a_tree_whose_run_variable_is_not_slug_and_run_stops() {
  setup_case tree-run-shape run-var-other
  write_default_case "$dir"
  run_check
  expect_status "$status" 2 "a tree whose run variable is another shape"
  expect_contains "$err" "something other than the repository's slug and the run id joined by a slash" "a tree whose run variable is another shape: the refusal"
  expect_no_cloud "a tree whose run variable is another shape"
}

test_an_unknown_argument_is_refused() {
  setup_case unknown-arg
  write_default_case "$dir"
  run_check -- --bogus
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--bogus'" "an unknown argument: the refusal names it"
  expect_no_cloud "an unknown argument"
}

test_the_max_usd_cap_is_optional() {
  setup_case no-cap
  write_default_case "$dir"
  run_check CHK831_MAX_USD=
  expect_status "$status" 0 "no cap"
  expect_contains "$(fugaro_calls)" "run --repo $repo --task-file" "no cap: the launch is still made"
  expect_absent "$(fugaro_calls)" "--max-usd" "no cap: no cap was passed"
  expect_row launch "with no cap of this check's" "no cap: the row says the installation's own stands"
}

test_the_max_usd_cap_is_passed_when_it_is_set() {
  setup_case with-cap
  write_default_case "$dir"
  run_check CHK831_MAX_USD=3
  expect_status "$status" 0 "a cap"
  expect_contains "$(fugaro_calls)" "--max-usd 3" "a cap: it is passed to the launch"
  expect_row launch "capped at \$3 by --max-usd" "a cap: the row says so"
}

test_a_slow_delete_is_not_a_leftover() {
  setup_case slow-delete
  write_default_case "$dir" delete-lists
  run_check
  expect_status "$status" 0 "a slow delete"
  expect_verdict restore PASS "a slow delete: nothing is named as left behind"
  expect_absent "$out" "STILL IN fugaro-dev" "a slow delete: no leftover row"
  nothing_left "a slow delete"
}

test_a_job_that_cannot_be_deleted_is_named() {
  setup_case undeletable
  write_default_case "$dir" delete-always
  run_check
  expect_status "$status" 1 "a job that cannot be deleted"
  expect_verdict restore FAIL "a job that cannot be deleted: the row"
  expect_row restore "$job_replay" "a job that cannot be deleted: it is named"
  expect_row restore "batch jobs delete <name>" "a job that cannot be deleted: the command that removes it is given"
}

test_a_launch_that_fails_stops_the_run() {
  setup_case launch-fails
  write_default_case "$dir" run-fail
  run_check
  expect_status "$status" 2 "a launch that fails"
  expect_verdict launch FAIL "a launch that fails: the row"
  expect_row launch "could not be launched" "a launch that fails: the row says so"
  expect_verdict job-found FAIL "a launch that fails: no job was made"
  expect_no_call "batch jobs submit $job_replay"
}

test_the_four_attacks_run_as_the_runs_own_account() {
  setup_case attack-account
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "the attacks' account"
  # Each attack runs as the account the RUN's own job runs as: a refusal would
  # otherwise be about the account (caller_not_admitted) rather than about the
  # VM's identity, and the row would not have earned its PASS.
  expect_submitted_field "$job_replay" "\"email\": \"$runacct\"" "the attacks' account: the replay"
  expect_submitted_field "$job_noticket" "\"email\": \"$runacct\"" "the attacks' account: the control"
  expect_submitted_field "$runjob2" "\"email\": \"$runacct\"" "the attacks' account: the re-creation"
  expect_submitted_field "$job_replay" "$ticket" "the attacks' account: the replay carries the ticket the second launcher read"
  expect_submitted_field "$runjob2" "$ticket2" "the attacks' account: the re-creation carries the SECOND launch's ticket, the one it was deleted under"
}

test_the_launch_ticket_is_read_out_of_the_jobs_own_variables() {
  setup_case ticket-in-job
  write_default_case "$dir"
  run_check
  expect_status "$status" 0 "the ticket's variable"
  # The ticket is one of the job's variables — that is the reading D3115 accepts
  # — and the row names the variable rather than the value.
  expect_row job-found "FUGARO_IDENTITY_TICKET" "the ticket's variable: the job's variables are reported"
  expect_row job-found "FUGARO_RTDB_URL" "the ticket's variable: the ledger's URL is one of them"
  expect_row job-found "FUGARO_CREDENTIALS_SERVICE_URL" "the ticket's variable: the claim's address is one of them"
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
t test_the_replay_is_what_makes_the_row
t test_a_run_whose_job_never_appears_fails_job_found
t test_a_job_carrying_no_ticket_fails_the_replay_and_the_rename
t test_a_claim_that_comes_back_with_a_token_is_the_finding
t test_a_claim_that_nobody_decided_is_not_a_refusal
t test_a_refusal_the_route_answers_without_the_link_is_not_the_rows
t test_a_claim_the_service_cannot_judge_is_not_a_refusal
t test_a_refusal_that_is_not_the_routes_own_is_not_measured
t test_the_refusal_the_row_passes_on_is_the_services_own_sentence
t test_a_name_the_platform_is_still_reaping_is_waited_for
t test_a_name_the_platform_never_frees_is_not_the_attacks_refusal
t test_an_attack_that_reported_nothing_is_not_judged
t test_the_second_launcher_must_be_able_to_read_the_job
t test_the_second_launcher_must_be_able_to_submit
t test_the_second_launcher_must_be_able_to_delete
t test_the_replay_carries_the_ticket_the_second_launcher_read
t test_a_ticket_the_second_launcher_reads_another_one_of_is_not_the_replay
t test_the_rename_row_fails_when_the_window_has_gone
t test_a_record_the_second_launcher_can_read_is_the_finding
t test_a_record_that_is_not_there_is_not_measured
t test_a_record_read_nobody_decided_is_not_a_refusal
t test_the_ledger_must_say_the_run_opened_its_session
t test_a_run_that_is_never_metered_fails_the_metered_row
t test_the_audit_record_that_was_never_read_fails_the_print_row
t test_the_installation_must_be_this_project
t test_the_installation_must_be_on_batch
t test_the_installation_that_states_no_backend_is_read_from_the_job
t test_the_owner_marker_is_required
t test_belong_is_refused_by_name
t test_the_second_launcher_is_required
t test_the_second_launcher_cannot_be_the_owner
t test_the_repository_is_required
t test_the_repository_must_be_owner_slash_name
t test_xtrace_is_refused
t test_a_scratch_inside_a_work_tree_is_refused
t test_a_tree_without_the_ticket_variable_stops
t test_a_tree_whose_key_escapes_another_set_stops
t test_a_tree_without_the_runs_own_variable_stops
t test_a_tree_whose_run_variable_is_not_slug_and_run_stops
t test_an_unknown_argument_is_refused
t test_the_max_usd_cap_is_optional
t test_the_max_usd_cap_is_passed_when_it_is_set
t test_a_slow_delete_is_not_a_leftover
t test_a_job_that_cannot_be_deleted_is_named
t test_a_launch_that_fails_stops_the_run
t test_the_four_attacks_run_as_the_runs_own_account
t test_the_launch_ticket_is_read_out_of_the_jobs_own_variables

if [ "$failures" -ne 0 ]; then
  echo "check-831_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-831_test.sh: ok"
