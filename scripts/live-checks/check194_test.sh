#!/usr/bin/env bash
# Tests for scripts/live-checks/check194.sh, with fake `fugaro`, `gcloud` and
# `curl` on PATH: NOTHING here reaches a cloud, a project, a repository or the
# network, no agent runs a live command, and the fake tools record every
# argument they are given so the test can assert that no command ever named
# anything but the sandbox installation's project and that nothing was written
# anywhere (Check 194 creates no cloud resource and writes no record).
#
# The fakes are a small project of their own: two sandbox repositories, each
# with a Cloud Run job, one `creds/workflowsecrets` record per repository that
# the fake `curl` serves out of the installation's Realtime Database, two
# Secret Manager containers of the installation's own that the record and the
# jobs both name, throwaway runs that are launched, listed and answered by the
# service's own `credsvc: audit` and `credsvc: refused` lines, and one secret's
# IAM policy — which is where every row of the check comes from.
#
# The two containers are PROJECT containers, and that is not a detail of the
# fixture: under D2313 a repository-level secret is accepted and ignored, so the
# container a workflow secret's value is fetched from is always the
# installation's project-wide copy (`fugaro-project-<installation>-<logical>-
# <16 hex>`, gcp.ProjectSecretID) and never the repository's own
# (internal/cli/update_workflowsecrets.go's workflowSecretMountID). A fixture
# built out of repository containers would test the shape the ruling took away.
#
# Usage: bash scripts/live-checks/check194_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check194.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK194_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c ${CHECK194_TEST_TAIL:-700})" ;;
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

# The two sandbox repositories every case runs against: the one under test —
# whose record names both containers — and the second one, which the
# per-repository workflow_secrets entry does not list and the workspace one does.
REPOS_DEFAULT="acme/sandbox-one,acme/sandbox-two"
# The variables of the workflow under test: one per shape of the operator's
# allow-list (check194-steps.md).
ENVS_DEFAULT="DEPLOY_TOKEN,PROJECT_TOKEN"
# The moment `fugaro update --repo` last reached the repositories' jobs, and the
# moment the run that was in flight across it started: an in-flight run is one
# that started BEFORE the update, which is the order the check's own arithmetic
# reads.
REAPPLY="2026-11-17T09:00:00.000000Z"
INFLIGHT_STARTED="2026-11-17T08:30:00Z"

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
"""A stand-in for `fugaro`, for scripts/live-checks/check194.sh's test only.

It answers the four things the check asks it: which installation this is
(`doctor`), its own version, a launch (`run`, which only RECORDS the run — no
task, no repository and no cloud is touched) and the listing (`runs ls`) whose
rows the check polls and reads.
"""
import json, os, sys

LOG = os.environ["FAKE_LOG_DIR"]
STATE = os.environ["FAKE_GCLOUD_STATE"]
mode = os.environ.get("FAKE_FUGARO_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]

with open(os.path.join(LOG, "fugaro.calls"), "a") as fh:
    fh.write("fugaro " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]


def flag(name, default=""):
    """Both the --name=value and the --name value spellings: the check passes
    flags the way an owner would on a command line."""
    for i, a in enumerate(args):
        if a.startswith(name + "="):
            return a[len(name) + 1:]
        if a == name:
            return args[i + 1] if len(args) > i + 1 else ""
    return default


def runs_state():
    path = os.path.join(STATE, "fugaro.runs")
    if not os.path.exists(path):
        return []
    return [l.split(" ", 1) for l in open(path).read().splitlines() if l.strip()]


def record_run(repo, rid):
    with open(os.path.join(STATE, "fugaro.runs"), "a") as fh:
        fh.write("%s %s\n" % (repo, rid))


def execution_path(repo, rid):
    """The execution a run's own row names: the resource path
    `projects/<project>/locations/<region>/jobs/<job>/executions/<name>`, which
    is what names the job the run ran on — and therefore, by the job's own
    template, the account its containers ran as. A row whose record carries none
    is the state FAKE_ROW_NO_EXECUTION writes, and the row the check must say
    unmeasured about."""
    return "projects/fugaro-dev/locations/us-east5/jobs/%s-0123456789abcdef/executions/%s" % (
        repo.lower().replace("/", "-"), rid)


if args[:1] == ["version"]:
    # `fugaro version` prints the version and nothing else: `dev` on a checkout
    # build, the release's number on a released one.
    print("dev" if mode == "dev" else "0.13.0")
elif args[:1] == ["doctor"]:
    # Faithful to the real doctor: its checks first, then the project: line, and
    # exit 1 as soon as a check of its own failed — which has nothing to do with
    # which installation this is, and is why the guard must read the line.
    if mode == "failing":
        print("check: credentials FAIL (no usable credentials)")
        print("project: fugaro (GCP fugaro-dev, us-east5)")
        sys.exit(1)
    if mode == "other":
        print("project: fugaro (GCP someone-else-prod, us-east5)")
    else:
        print("project: fugaro (GCP fugaro-dev, us-east5)")
elif args[:1] == ["run"]:
    repo = flag("--repo")
    rid = flag("--run-id")
    task = flag("--task-file")
    if repo not in repos:
        sys.stderr.write("fugaro: the repository %s is not one of the two sandbox repositories this test names\n" % repo)
        sys.exit(1)
    if not rid or not task or not os.path.exists(task):
        sys.stderr.write("fugaro: a launch needs --run-id and a task file that exists\n")
        sys.exit(1)
    text = open(task).read()
    if not text.startswith("# Task: "):
        sys.stderr.write("fugaro: the task's first line must be the '# Task: <title>' line\n")
        sys.exit(1)
    if mode == "launch-fails":
        sys.stderr.write("fugaro: the launch was refused (test)\n")
        sys.exit(1)
    record_run(repo, rid)
    print(json.dumps({"run_id": rid, "repo": repo, "status": "queued"}))
elif args[:1] == ["runs"]:
    repo = flag("--repo")
    if mode == "listing-fails":
        sys.stderr.write("fugaro: the listing could not be read (test)\n")
        sys.exit(1)
    rows = []
    inflight = os.environ.get("FAKE_INFLIGHT", "")
    if inflight and repo == os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else ""):
        rows.append(inflight)
    for r, rid in runs_state():
        if r != repo or rid == inflight:
            continue
        rows.append(rid)
    out = []
    for rid in rows:
        started = os.environ.get("FAKE_ROW_STARTED", "2026-11-17T08:30:00Z")
        if rid == inflight:
            started = os.environ.get("FAKE_INFLIGHT_STARTED", started)
        row = {"run_id": rid, "repo": repo, "status": os.environ.get("FAKE_ROW_STATUS", "succeeded"),
               "terminal": True, "outcome": "ready", "pushed": True, "pr": 7,
               "started_at": started}
        if not os.environ.get("FAKE_ROW_NO_EXECUTION"):
            row["execution"] = execution_path(repo, rid)
        if os.environ.get("FAKE_ROW_INFLIGHT"):
            row["terminal"] = False
            row["status"] = "running"
            row["outcome"] = ""
            row["pushed"] = False
            row["pr"] = 0
        if row["status"] != "succeeded":
            row["reason"] = "the run failed (test)"
            row["pr"] = 0
            row["pushed"] = False
            row["halt"] = {"reason": "agent",
                           "detail": "the agent stopped before opening a pull request (test)"}
        out.append(row)
    print(json.dumps({"runs": out}))
else:
    sys.stderr.write("fugaro: unknown command: %s\n" % " ".join(args))
    sys.exit(2)
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check194.sh's test only: no
socket is opened and nothing is created.

It answers four reads: the installation's job list (jobs and their containers),
the spec of one execution of a job, the access token the installation's
Realtime Database record is read with, one secret's own IAM policy, and Cloud
Logging — whose lines for a run are the service's own `credsvc: audit` and
`credsvc: refused` lines, built here from the mode the case names.
"""
import hashlib, json, os, re, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]
envs_of_interest = [e for e in os.environ.get("FAKE_ENVS", "").split(",") if e]
flipped = os.environ.get("FAKE_FLIPPED", "")
inflight = os.environ.get("FAKE_INFLIGHT", "")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
positionals = [a for a in args if not a.startswith("-")]
# Like the real CLI, a subcommand or a flag that does not exist is an error: a
# misspelled one therefore cannot hide in any call this check makes. `run jobs
# describe` is deliberately NOT here: the check reads the account a run's
# containers run as off the job list, and a call it never makes is one this
# fake must refuse rather than answer.
ALLOWED = {
    ("auth", "print-access-token"): (),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("run", "jobs", "executions", "describe"): ("--project", "--region", "--format"),
    ("secrets", "get-iam-policy"): ("--project", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
}
sub = None
for n in (4, 3, 2):
    if tuple(positionals[:n]) in ALLOWED:
        sub = tuple(positionals[:n])
        break
if sub is None:
    sys.stderr.write("ERROR: (gcloud) unknown command: %s\n" % " ".join(args))
    sys.exit(2)
for a in args:
    if a.startswith("--") and a.split("=")[0] not in ALLOWED[sub]:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)


def flag(name, default=""):
    for i, a in enumerate(args):
        if a.startswith(name + "="):
            return a[len(name) + 1:]
        if a == name:
            return args[i + 1] if len(args) > i + 1 else ""
    return default


PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)


def job_name(repo):
    return "%s-0123456789abcdef" % repo.lower().replace("/", "-")


def project_secret_id(logical):
    """gcp.ProjectSecretID: the DERIVED Secret Manager ID of the
    installation's project-wide copy of a logical secret —
    `fugaro-project-<installation>-<logical>-<16 hex of sha256("project" NUL
    <installation> NUL <logical>)>` — which is the ONLY container a workflow
    secret's value comes from under D2313."""
    h = hashlib.sha256(("project\x00%s\x00%s" % ("fugaro", logical)).encode()).hexdigest()[:16]
    return "fugaro-project-fugaro-%s-%s" % (logical, h)


def repo_secret_id(slug, logical):
    """gcp.SecretID: the repository's OWN container, which the record may never
    name under D2313 — the shape the wrong-container case is built out of."""
    readable = re.sub(r"-[0-9a-f]{16}$", "", slug.lower())
    h = hashlib.sha256(("%s\x00%s" % (slug, logical)).encode()).hexdigest()[:16]
    return "fugaro-%s-%s-%s" % (readable, logical, h)


def account(repo):
    """The account a repository's job runs as. Nothing about it is a value: it
    is the member an accessor binding is compared with."""
    return "fugaro-%s@fugaro-dev.iam.gserviceaccount.com" % job_name(repo)


def mount(name, secret):
    return {"name": name,
            "valueSource": {"secretKeyRef": {"secret": secret, "version": "latest"}}}


def job_envs(repo):
    """A job's own environment: its remote, the credentials service's URL (kept
    across the flip, B3.8) and the mounts the flip takes away."""
    out = [{"name": "FUGARO_REMOTE", "value": "https://bitbucket.org/%s.git" % repo}]
    if mode != "no-url":
        out.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL",
                    "value": "https://fugaro-credentials-fake-ue.a.run.app"})
    one, two = envs_of_interest[0], envs_of_interest[1]
    still = mode == "still-mounts"
    if mode == "flipped-off":
        still = False
    elif flipped and not still:
        return out
    if repo == (repos[1] if len(repos) > 1 else ""):
        out.append(mount(two, project_secret_id("pool-token")))
        if mode == "other-mounts":
            # The second repository's job fed the FIRST one's per-repository
            # secret: the boundary this check exists to catch.
            out.append(mount(one, project_secret_id("deploy-token")))
        return out
    out.append(mount(one, project_secret_id("deploy-token")))
    if mode == "plain-value":
        out[-1] = {"name": one, "value": "a-plain-and-not-a-container"}
        out.append(mount(two, project_secret_id("pool-token")))
        return out
    if mode == "wrong-mount":
        out[-1] = mount(one, project_secret_id("another-logical-name"))
        out.append(mount(two, project_secret_id("pool-token")))
        return out
    out.append(mount(two, project_secret_id("pool-token")))
    return out


def job_doc(repo):
    name = job_name(repo)
    labels = {} if mode == "no-repo-label" else {"fugaro_repo": name}
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Job",
            "metadata": {"name": name, "labels": labels, "updateTime": os.environ.get("FAKE_REAPPLY", "")},
            "spec": {"template": {"spec": {"template": {"spec": {
                "containers": [{"name": "workflow", "env": job_envs(repo)}],
                "serviceAccountName": account(repo)}}}}}}


def execution_doc(repo, name):
    """One execution of a repository's job, as `gcloud run jobs executions
    describe` answers it: `kind: Execution` with its containers at
    spec.template.spec.containers — and NO serviceAccountName, which is what a
    real execution carries too (the account sits above the revision spec), and
    the reason the check reads it off the job list.

    Its containers are the template's of the moment it was CREATED: an execution
    created before the re-apply keeps the mounts the flip takes away (U9), which
    is why the run in flight across it still holds both containers while the job
    template that run was launched by now holds none."""
    envs = [{"name": "FUGARO_REMOTE", "value": "https://bitbucket.org/%s.git" % repo},
            {"name": "FUGARO_CREDENTIALS_SERVICE_URL",
             "value": "https://fugaro-credentials-fake-ue.a.run.app"}]
    one, two = envs_of_interest[0], envs_of_interest[1]
    keep = True
    if mode == "execution-no-mount":
        keep = False
    elif flipped and name != inflight:
        keep = False
    if keep:
        envs.append(mount(one, project_secret_id("deploy-token")))
        envs.append(mount(two, project_secret_id("pool-token")))
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Execution",
            "metadata": {"name": name, "labels": {"run.googleapis.com/job": job_name(repo)},
                         "updateTime": os.environ.get("FAKE_REAPPLY", "")},
            "spec": {"template": {"spec": {
                "containers": [{"name": "workflow", "env": envs}]}}}}


def iam_doc(secret):
    """One container's own IAM policy: a member that can read a version is an
    accessor, and nothing here is a value."""
    bindings = [{"role": "roles/secretmanager.secretVersionManager",
                 "members": ["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com"]}]
    if mode == "accessor-present":
        bindings.append({"role": "roles/secretmanager.secretAccessor",
                         "members": ["serviceAccount:%s" % account(repos[0] if repos else "")]})
    if mode == "accessor-other":
        bindings.append({"role": "roles/secretmanager.secretAccessor",
                         "members": ["serviceAccount:someone-else@fugaro-dev.iam.gserviceaccount.com"]})
    return {"bindings": bindings, "etag": "fake"}


if sub == ("auth", "print-access-token"):
    print("fake-access-token-for-the-check-test")
elif sub == ("run", "jobs", "list"):
    if mode == "jobs-list-fails":
        sys.stderr.write("ERROR: (gcloud) run.jobs.list was refused: the session has expired\n")
        sys.exit(1)
    docs = [] if mode == "no-jobs" else [job_doc(r) for r in repos]
    print(json.dumps(docs))
elif sub == ("run", "jobs", "executions", "describe"):
    if mode == "executions-describe-fails":
        # A read that does not answer at all: the execution's own spec is not in
        # the file it writes, which is NOT an execution with no mount.
        sys.stderr.write("ERROR: (gcloud) run.jobs.executions.get was refused: the caller does not have permission to read the execution\n")
        sys.exit(1)
    want = positionals[4] if len(positionals) > 4 else ""
    repo = os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else "")
    if want != inflight:
        for r in repos:
            if want.endswith("-0197"):
                repo = r
                break
        else:
            repo = os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else "")
    print(json.dumps(execution_doc(repo, want)))
elif sub == ("secrets", "get-iam-policy"):
    if mode == "iam-fails":
        sys.stderr.write("ERROR: (gcloud) secrets.getIamPolicy was refused: the caller does not have permission\n")
        sys.exit(1)
    print(json.dumps(iam_doc(positionals[2] if len(positionals) > 2 else "")))
elif sub == ("logging", "read"):
    if mode == "log-fails":
        # A log read that does not answer at all: no line of any run's is in the
        # file it writes, which is NOT the same thing as the service having
        # written none.
        sys.stderr.write("ERROR: (gcloud) logging.read was refused: the caller does not have permission to read the log\n")
        sys.exit(1)
    filt = positionals[2] if len(positionals) > 2 else ""
    m = re.search(r'jsonPayload\.run="([^"]*)"', filt)
    rid = m.group(1) if m else ""
    repo = os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else "")

    def line(credential, purpose, status="ok", tier="", message="credsvc: audit"):
        p = {"message": message, "run": rid,
             "repository": repo.replace("/", "-"), "job": job_name(repo),
             "caller": account(repo), "status": status,
             "credential": credential, "purpose": purpose}
        if tier:
            p["tier"] = tier
        if message == "credsvc: refused":
            p = {"message": message, "run": rid, "status": status,
                 "message_text": "the service refused this call (test)"}
        return {"jsonPayload": p}

    lines = []
    if rid and mode != "no-audit":
        if mode == "refused-only":
            # A refusal and NO act: the read answered, and the run was served
            # nothing — which is a FAIL for the served row and not a failed read.
            lines.append({"jsonPayload": {"message": "credsvc: refused", "run": rid,
                                          "status": "403",
                                          "message": "the service refused this call (test)"}})
        else:
            # The run's own git and model traffic, which the row must NOT count:
            # only the workflow-secret purpose names a container the record names.
            lines.append(line("bitbucket-token", "git"))
            lines.append(line("anthropic-api-key", "forward", tier="B"))
            if flipped and rid != inflight:
                lines.append(line(project_secret_id("deploy-token"), "workflow-secret"))
                if mode != "one-workflow-line":
                    lines.append(line(project_secret_id("pool-token"), "workflow-secret"))
    print(json.dumps(lines))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
"""A stand-in for `curl`, for scripts/live-checks/check194.sh's test only: no
socket is opened.

It answers the one GET the check makes — the installation's own Realtime
Database, at the path `fugaro update --repo` writes a repository's
workflow-secrets record to (creds/workflowsecrets/<slug>) — out of the records
the case names, and it refuses the GET the way the real database does when the
node is not there (404). The record it hands back is READ by the check and
never written by it, which is the only thing this fake has to get right:
nothing here accepts a write.
"""
import hashlib
import json
import os, re, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_CURL_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]
envs_of_interest = [e for e in os.environ.get("FAKE_ENVS", "").split(",") if e]
args = sys.argv[1:]

with open(os.path.join(LOG, "curl.calls"), "a") as fh:
    fh.write("curl " + " ".join(args) + "\n")

if "-X" in args or "--request" in args or any(a.startswith("-d") or a.startswith("--data") for a in args):
    sys.stderr.write("curl: this check READS the record and must never write one\n")
    sys.exit(3)


def value_of(name):
    for i, a in enumerate(args):
        if a == name:
            return args[i + 1] if len(args) > i + 1 else ""
        if a.startswith(name):
            return a[len(name):].lstrip("=")
    return ""


out = value_of("-o")
url = ""
for a in args:
    if a.startswith("http"):
        url = a
    elif a.startswith("--url"):
        url = a.split("=", 1)[-1]
fmt = ""
for i, a in enumerate(args):
    if a == "-w" or a == "--write-out":
        fmt = args[i + 1] if len(args) > i + 1 else ""
    elif a.startswith("--write-out=") or a.startswith("-w="):
        fmt = a.split("=", 1)[1]


def job_key(repo):
    """The slug a repository's record is keyed by: the readable path, "-" and 16
    hex of the hash (task.Slug), which is the same spelling the fake `gcloud`
    writes as its jobs' `fugaro_repo` label — one key, written once."""
    return repo.lower().replace("/", "-") + "-0123456789abcdef"


def project_secret_id(logical):
    h = hashlib.sha256(("project\x00%s\x00%s" % ("fugaro", logical)).encode()).hexdigest()[:16]
    return "fugaro-project-fugaro-%s-%s" % (logical, h)


def repo_secret_id(slug, logical):
    readable = re.sub(r"-[0-9a-f]{16}$", "", slug.lower())
    h = hashlib.sha256(("%s\x00%s" % (slug, logical)).encode()).hexdigest()[:16]
    return "fugaro-%s-%s-%s" % (readable, logical, h)


def body_for(slug):
    """The two records: the repository under test's, which names both containers
    of the installation's project, and the second repository's, which the
    per-repository entry of the allow-list does not admit and the workspace one
    does — so it names the project-wide container alone.

    A record is keyed by the repository's SLUG and not by its name, which is why
    every branch below compares against job_key(): the node `fugaro update
    --repo` writes is creds/workflowsecrets/<readable>-<16 hex>, and a GET at
    creds/workflowsecrets/<owner>-<name>.json is the node nobody writes — the
    real database answers it 404, and this fake answers it 404 too, so a check
    that spelled the key out of the repository's name cannot pass here.
    """
    one, two = envs_of_interest[0], envs_of_interest[1]
    first = repos[0] if repos else ""
    second = repos[1] if len(repos) > 1 else ""
    if slug == job_key(second):
        rows = [{"env": two, "id": project_secret_id("pool-token")}]
        if mode == "other-names-one":
            rows.append({"env": one, "id": project_secret_id("deploy-token")})
        return {"version": 1, "workflows": {"default": rows}}
    if slug != job_key(first):
        return None
    rows = [{"env": one, "id": project_secret_id("deploy-token")},
            {"env": two, "id": project_secret_id("pool-token")}]
    if mode == "stray-key":
        # A member this release does not model, at the top of the record: a
        # record whose shape is closed is the only one that cannot carry a
        # workflow secret's value, so this is a FAIL and never a shrug (G18).
        return {"version": 1, "workflows": {"default": rows}, "containers": ["not-a-container"]}
    if mode == "stray-entry":
        rows.append({"env": "THIRD_TOKEN", "id": project_secret_id("pool-token"),
                     "value": "example"})
        return {"version": 1, "workflows": {"default": rows}}
    if mode == "repo-container":
        # The repository's OWN container: the shape D2313 took away, and a
        # record naming it promises a value no mount carries.
        return {"version": 1, "workflows": {"default": [
            {"env": one, "id": repo_secret_id(slug, "deploy-token")},
            {"env": two, "id": project_secret_id("pool-token")}]}}
    if mode == "one-env":
        return {"version": 1, "workflows": {"default": [
            {"env": two, "id": project_secret_id("pool-token")}]}}
    if mode == "same-container":
        return {"version": 1, "workflows": {"default": [
            {"env": one, "id": project_secret_id("deploy-token")},
            {"env": two, "id": project_secret_id("deploy-token")}]}}
    return {"version": 1, "workflows": {"default": rows}}


slug = url.rsplit("/creds/workflowsecrets/", 1)[-1].replace(".json", "") if "/creds/workflowsecrets/" in url else ""
body = body_for(slug) if slug else None
code = "200"
if mode == "record-missing":
    code = "404"
elif mode == "record-unreadable":
    code = "500"
elif body is None:
    # No node at that path: what the real database answers for a key nobody
    # wrote — including a name-shaped key, which is the whole finding this
    # fake's strictness holds the check to.
    code = "404"
if code == "200" and out:
    with open(out, "w") as fh:
        fh.write(json.dumps(body))
if code != "200" and out:
    with open(out, "w") as fh:
        fh.write("null")
sys.stdout.write(fmt.replace("%{http_code}", code) if fmt else "")
sys.stdout.write("\n")
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- harness
status=0
out=""
err=""
scratch=""

# run_it DIR SCRIPT ARG...: one run of the check (or of a copy of it) with the
# fakes on PATH and the installation's own config under an empty $HOME. An
# argument carrying an `=` is an environment assignment for that run; anything
# else is one of the check's own command-line flags. Every case reads back
# `status`, `out` (the owner's RESULT block, which the check writes to fd 3),
# `err` (what it said while it ran) and `scratch`.
run_it() {
  local dir=$1 sh=$2
  shift 2
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  # CHECK194_TEST_XTRACE starts the shell the way an owner might by habit: with
  # tracing on, which is the one leak this check's own redaction cannot catch.
  local -a envs=() cli=() runner=(bash) a
  for a in "$@"; do
    case "$a" in
    CHECK194_TEST_XTRACE=*) runner=(bash -x) ;;
    esac
    case "$a" in
    *=*) envs+=("$a") ;;
    *) cli+=("$a") ;;
    esac
  done
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_REPOS="$REPOS_DEFAULT" FAKE_ENVS="$ENVS_DEFAULT" \
    FAKE_REAPPLY="$REAPPLY" FAKE_INFLIGHT_STARTED="$INFLIGHT_STARTED" \
    CHECK194_REPO="${REPOS_DEFAULT%%,*}" \
    CHECK194_OTHER_REPO="${REPOS_DEFAULT##*,}" \
    CHECK194_RUN_SECONDS=0 CHECK194_POLL_SECONDS=0 CHECK194_LOG_WAIT_SECONDS=0 \
    CHECK194_LOG_POLL_SECONDS=0 \
    ${envs[@]+"${envs[@]}"} \
    "${runner[@]}" "$sh" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run194() { # dir arg...: the check itself
  local dir=$1
  shift
  run_it "$dir" "$script" "$@"
}

run_copy() { # dir copy arg...: the same run against a mutated copy of the check
  local dir=$1 copy=$2
  shift 2
  run_it "$dir" "$copy" "$@"
}

prepare() { # dir
  mkdir -p "$1/home/.config/fugaro/projects" "$1/tmp" "$1/state" "$1/logs"
  : >"$1/logs/gcloud.calls"
  : >"$1/logs/fugaro.calls"
  : >"$1/logs/curl.calls"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
  # The installation's own local config, which is where the check reads the
  # Realtime Database's URL from (budget.rtdb_url) — the same file an owner's
  # machine holds for installation fugaro.
  cat >"$1/home/.config/fugaro/projects/fugaro.yaml" <<'CONFIG_EOF'
name: fugaro
gcp_project: fugaro-dev
budget:
  rtdb_url: https://fugaro-dev-fake.firebaseio.com
endpoints:
  no_auth: false
CONFIG_EOF
}

# --------------------------------------------------------------- the tests
# --dry-run is the mode the checklist has the owner try FIRST, on a machine that
# may have no credentials at all: it must print the plan and call NOTHING. Not
# even the installation's project is read, which is why both logs are empty too
# — the two guards that read the cloud are not reached.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run194 "$dir" --dry-run
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row"
  expect_contains "$out" "CHECK 194 RESULT END (exit 0)" "dry run: the end line"
  # The plan is progress and goes to stderr; the block on stdout carries its own
  # row and nothing of the plan.
  expect_contains "$err" "gcloud run jobs list" "dry run: the plan names the job list"
  # The plan must name what a run would cost, and it must NOT be the plan of the
  # flip when the flip was not asked for.
  expect_contains "$err" "fugaro run --repo acme/sandbox-one" "dry run: the throwaway run"
  expect_contains "$err" "as a plain value" "dry run: the switch mode's own mounts row"
  expect_absent "$err" "the mount goes, the URL stays" "dry run: the flip's rows are not the switch mode's plan"
  expect_absent "$err" "gcloud logging read" "dry run: the service's lines are not read before the flip"
  expect_absent "$out" "fugaro run --repo" "dry run: the plan itself is not the block"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "dry run: gcloud was called"; fi
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "dry run: fugaro was called"; fi
  if [ -s "$dir/logs/curl.calls" ]; then fail "dry run: curl was called"; fi
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory survived"; fi
}

# --after-flip's plan is a DIFFERENT plan: the rows it reads are the mounts that
# are gone, the account's accessors, the service's own lines and the run that was
# in flight — none of which the switch mode reads at all.
test_dry_run_flip() {
  local dir=$work/dryflip
  prepare "$dir"
  # The run in flight is required in this mode too, and the guard that asks for
  # it is about the inputs rather than the cloud: a dry run without it stops
  # before it prints a plan, which is the mode's own contract (§test_the_inputs_guard).
  run194 "$dir" --dry-run --after-flip CHECK194_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 0 "dry run flip: the verdict"
  expect_contains "$out" "MODE: flip" "dry run flip: the mode"
  expect_contains "$err" "gcloud secrets get-iam-policy" "dry run flip: the accessor read"
  expect_contains "$err" "gcloud logging read" "dry run flip: the service's own lines"
  expect_contains "$err" "the mount goes, the URL stays" "dry run flip: the URL is kept"
  expect_contains "$err" "for CHECK194_INFLIGHT" "dry run flip: the run in flight"
  expect_contains "$err" "started before the job's own updateTime" "dry run flip: U9's arithmetic"
  expect_contains "$err" "-0198" "dry run flip: the flip's own run suffix"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "dry run flip: gcloud was called"; fi
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "dry run flip: fugaro was called"; fi
  if [ -s "$dir/logs/curl.calls" ]; then fail "dry run flip: curl was called"; fi
}

# The project guard reads the `project:` LINE and not doctor's exit status:
# doctor exits 1 whenever a check of its own failed — no credentials, an expired
# session — and a guard that read the status would stop every run on a machine
# whose only trouble is a check it does not need.
test_the_project_guard_reads_the_line_not_doctors_status() {
  local dir=$work/doctorline
  prepare "$dir"
  run194 "$dir" FAKE_FUGARO_MODE=failing
  expect_status "$status" 0 "doctor's status: the guard reads the line"
  expect_contains "$out" "FACT project-region: PASS" "doctor's status: the installation is the right one"
}

test_project_guard() {
  local dir=$work/otherproject
  prepare "$dir"
  run194 "$dir" FAKE_FUGARO_MODE=other
  expect_status "$status" 1 "other installation: the verdict"
  expect_contains "$out" "FACT aborted: FAIL" "other installation: the guard stopped it"
  expect_contains "$out" "this is not the fugaro-dev installation" "other installation: the reason"
  expect_contains "$out" "never belong" "other installation: the rule"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run194 "$dir" CHECK194_TEST_XTRACE=1
  expect_status "$status" 1 "xtrace: the verdict"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: the reason"
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "xtrace: a tool was called after the refusal"; fi
}

# The inputs guard: both repositories are REQUIRED and neither may be the
# owner's own workspace, the two variables must be two, and the flip's run id is
# required with --after-flip. None of them is read from the cloud, so either
# spilling this is a mistake in how the owner launched the check.
test_the_inputs_guard() {
  local dir=$work/inputs
  prepare "$dir"
  run194 "$dir" CHECK194_REPO=
  expect_status "$status" 1 "no repository: the verdict"
  expect_contains "$out" "CHECK194_REPO must name the sandbox repository" "no repository: the reason"

  run194 "$dir" CHECK194_OTHER_REPO=
  expect_contains "$out" "CHECK194_OTHER_REPO must name a second sandbox repository" "no second repository: the reason"

  run194 "$dir" CHECK194_OTHER_REPO=acme/sandbox-one
  expect_contains "$out" "are the same repository" "one repository twice: the reason"

  run194 "$dir" CHECK194_REPO=belong/sandbox-one
  expect_contains "$out" "never belong" "the owner's workspace: the reason"

  run194 "$dir" CHECK194_ENV_TWO=DEPLOY_TOKEN
  expect_contains "$out" "are the same variable" "one variable twice: the reason"

  run194 "$dir" CHECK194_ENV_ONE=deploy-token
  expect_contains "$out" "must be an upper-case environment variable name" "a lower-case variable: the reason"

  # --after-flip without a run id is the one case the flip cannot do without:
  # a run this check picked itself could be one that ended hours before the
  # re-apply, which is not the run U9 is about.
  run194 "$dir" --after-flip
  expect_contains "$out" "CHECK194_INFLIGHT must name the run" "the flip without a run: the reason"
}

test_a_development_build_is_a_failure() {
  local dir=$work/dev
  prepare "$dir"
  run194 "$dir" FAKE_FUGARO_MODE=dev
  expect_status "$status" 1 "development build: the verdict"
  expect_contains "$out" "FACT fugaro-version: FAIL" "development build: the row"
  expect_contains "$out" "a development build is not the release" "development build: the reason"
}

# A repository whose own jobs could not be read at all: the record is keyed by
# the slug those jobs carry, so no record is read — and every row that needed it
# says so rather than reading mounts off a list nobody wrote.
test_a_job_list_that_could_not_be_read_leaves_the_record_unmeasured() {
  local dir=$work/nojobs
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=jobs-list-fails
  expect_status "$status" 2 "no job list: the verdict"
  expect_contains "$out" "FACT record: FAIL" "no job list: the record row"
  expect_contains "$out" "gcloud run jobs list exited non-zero" "no job list: the reason"
  expect_contains "$out" "FACT mounts: FAIL" "no job list: the mounts row was not measured either"
  if [ -s "$dir/logs/curl.calls" ]; then fail "no job list: a record was read anyway"; fi
}

test_a_job_without_the_repo_label_leaves_the_record_unmeasured() {
  local dir=$work/nolabel
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=no-repo-label
  expect_status "$status" 2 "no label: the verdict"
  expect_contains "$out" "FACT record: FAIL" "no label: the record row"
  expect_contains "$out" "carries its 'fugaro_repo' label" "no label: the reason"
  expect_contains "$out" "fugaro update --repo" "no label: the fix"
}

# The record is READ and never written, and it is read at the key `fugaro update
# --repo` wrote it under — the repository's own SLUG, which is the `fugaro_repo`
# label of its jobs — never at a path spelled out of the repository's name.
test_the_record_is_read_at_the_slug() {
  local dir=$work/slug
  prepare "$dir"
  run194 "$dir"
  local curls
  curls=$(cat "$dir/logs/curl.calls")
  expect_contains "$curls" "creds/workflowsecrets/acme-sandbox-one-0123456789abcdef.json" "the record: the repository under test, at its slug"
  expect_contains "$curls" "creds/workflowsecrets/acme-sandbox-two-0123456789abcdef.json" "the record: the second repository, at its slug"
  expect_absent "$curls" "creds/workflowsecrets/acme-sandbox-one.json" "the record: never at a name-shaped path"
  expect_absent "$curls" "-X " "the record: nothing was written to the database"
}

test_a_record_that_is_not_there_is_unmeasured() {
  local dir=$work/norecord
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=record-missing
  expect_status "$status" 2 "no record: the verdict"
  expect_contains "$out" "FACT record: FAIL" "no record: the row"
  expect_contains "$out" "the record is not there (HTTP 404)" "no record: the reason"
  expect_contains "$out" "fugaro update --repo" "no record: the fix"
  # Nothing is launched off a record nobody read: every run costs the sandbox
  # repository a commit and a pull request.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_absent "$calls" "fugaro run " "no record: no run was launched"
  expect_contains "$out" "FACT verify: FAIL" "no record: the verify row was not measured"
}

test_a_record_that_could_not_be_read_names_the_status_and_the_path() {
  local dir=$work/badrecord
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=record-unreadable
  expect_status "$status" 2 "unreadable record: the verdict"
  expect_contains "$out" "the record could not be read (HTTP 500)" "unreadable record: the status"
  expect_contains "$out" "creds/workflowsecrets/acme-sandbox-one-0123456789abcdef.json" "unreadable record: the path"
}

# The record's shape is CLOSED and that is the reading of G18: every member of
# it is a NAME, so a member this release does not model is one that could hold a
# workflow secret's value — a FAIL and never a shrug, at the top of the record
# and inside an entry alike.
test_a_record_with_a_key_this_release_does_not_model_is_a_failure() {
  local dir=$work/stray
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=stray-key
  expect_status "$status" 1 "stray key: the verdict"
  expect_contains "$out" "FACT record: FAIL" "stray key: the row"
  expect_contains "$out" "carries key(s) this release does not model: containers" "stray key: the key by name"
  expect_contains "$out" "no field of this record is a credential's value" "stray key: the rule"

  local dir2=$work/strayentry
  prepare "$dir2"
  run194 "$dir2" FAKE_CURL_MODE=stray-entry
  expect_status "$status" 1 "stray entry key: the verdict"
  expect_contains "$out" "carries key(s) this release does not model: value" "stray entry key: the key by name"
}

# D2313 read off the wire: a repository-level secret is accepted and ignored, so
# the record may only ever name the installation's PROJECT container and never
# the repository's own — a record naming the repository's own promises a value
# no job mounts.
test_a_record_naming_a_repository_container_is_a_failure() {
  local dir=$work/repocont
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=repo-container
  expect_status "$status" 1 "repository container: the verdict"
  expect_contains "$out" "FACT record: FAIL" "repository container: the row"
  expect_contains "$out" "names a container that is not a project secret of this installation" "repository container: the reason"
  expect_contains "$out" "fugaro-project-" "repository container: the shape it must be"
}

test_a_record_naming_no_container_for_one_variable_is_a_failure() {
  local dir=$work/oneenv
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=one-env
  expect_status "$status" 1 "one variable: the verdict"
  expect_contains "$out" "FACT record: FAIL" "one variable: the row"
  expect_contains "$out" "names no container for DEPLOY_TOKEN" "one variable: the variable it lacks"
  expect_contains "$out" "it does name" "one variable: the one it does name"
  expect_contains "$out" "workflow_secrets" "one variable: the allow-list"
}

test_a_record_naming_one_container_for_both_variables_is_a_failure() {
  local dir=$work/samecont
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=same-container
  expect_status "$status" 1 "same container: the verdict"
  expect_contains "$out" "names the SAME container" "same container: the reason"
  expect_contains "$out" "which is not two secrets" "same container: the rule"
}

test_the_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run194 "$dir"
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT record: PASS" "happy path: the record"
  expect_contains "$out" "carries no key this release does not model" "happy path: the closed shape"
  expect_contains "$out" "FACT mounts: PASS" "happy path: the mounts"
  expect_contains "$out" "FACT verify: PASS" "happy path: the run's own verify"
  expect_contains "$out" "FACT other: PASS" "happy path: the second repository"
  expect_contains "$out" "is refused acme/sandbox-one's per-repository workflow secret" "happy path: what the second repository is refused"
  expect_contains "$out" "CHECK 194 RESULT END (exit 0)" "happy path: the end line"

  local calls launches curls
  calls=$(cat "$dir/logs/fugaro.calls")
  launches=$(printf '%s\n' "$calls" | grep -c '^fugaro run --repo' || true)
  expect_status "$launches" 1 "happy path: one throwaway run and no more"
  expect_contains "$calls" "--repo acme/sandbox-one" "happy path: the repository under test"
  expect_contains "$calls" "--run-id " "happy path: the run id"
  expect_contains "$calls" "-0197" "happy path: the switch mode's own suffix"
  expect_contains "$calls" "--project fugaro" "happy path: the installation"
  expect_absent "$calls" "--total-timeout" "happy path: no total timeout is ever passed"
  expect_absent "$calls" "--repo acme/sandbox-two" "happy path: the second repository is read and never launched"
  curls=$(cat "$dir/logs/curl.calls")
  expect_absent "$curls" "-X " "happy path: nothing was written to the database"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

# CHECK194_KEEP_SCRATCH is the knob the steps file tells the owner to set to read
# what the check wrote before it goes. A directory kept because it was asked for
# is not one the check failed to remove, so the row reads PASS and the verdict
# stays the reading's own.
test_the_happy_path_with_the_scratch_directory_kept() {
  local dir=$work/keep
  prepare "$dir"
  run194 "$dir" CHECK194_KEEP_SCRATCH=1
  expect_status "$status" 0 "kept scratch: the verdict is the reading's own"
  expect_contains "$out" "FACT scratch-removed: PASS" "kept scratch: the row is not a FAIL"
  expect_contains "$out" "kept on request" "kept scratch: the row says the owner asked for it"
  expect_contains "$out" "CHECK 194 RESULT END (exit 0)" "kept scratch: the end line"
  [ -n "$scratch" ] && [ -d "$scratch" ] || fail "kept scratch: the directory was removed anyway"
}

# F33 and D1267: the fetch reads the container the mount reads, so a job fed
# from another container than the record names is a run whose fetched value
# would not be the value it holds.
test_a_mount_from_another_container_is_a_failure() {
  local dir=$work/wrongmount
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=wrong-mount
  expect_status "$status" 1 "wrong mount: the verdict"
  expect_contains "$out" "FACT mounts: FAIL" "wrong mount: the row"
  expect_contains "$out" "and the record names" "wrong mount: the two containers"
  local mounts
  mounts=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$mounts" "executions describe" "wrong mount: the execution the run ran on is read"
  expect_contains "$out" "CHECK 194 RESULT END (exit 1)" "wrong mount: the end line"
}

# A value the job carries as plain text is one a fetch could never be seen to
# replace: the mount is what the plan wrote, and a plain one is a plan that wrote
# the secret into the job.
test_a_plain_value_on_a_job_is_a_failure() {
  local dir=$work/plain
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=plain-value
  expect_status "$status" 1 "plain value: the verdict"
  expect_contains "$out" "FACT mounts: FAIL" "plain value: the row"
  expect_contains "$out" "as a PLAIN value and not out of a container" "plain value: the reason"
}

# A read that does not answer is not an execution with no mount: the mounts row
# must say it could not measure rather than reading an empty file as a spec.
test_an_execution_that_could_not_be_read_leaves_the_mounts_row_unmeasured() {
  local dir=$work/noexec
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=executions-describe-fails
  expect_status "$status" 2 "no execution: the verdict"
  expect_contains "$out" "FACT mounts: FAIL" "no execution: the mounts row"
  expect_contains "$out" "executions describe" "no execution: the call that failed"
  expect_contains "$out" "FACT verify: PASS" "no execution: the run's own verify is still read"
}

test_a_run_that_did_not_finish_is_a_failure() {
  local dir=$work/failedrun
  prepare "$dir"
  run194 "$dir" FAKE_ROW_STATUS=failed
  expect_status "$status" 1 "failed run: the verdict"
  expect_contains "$out" "FACT verify: FAIL" "failed run: the verify row"
  expect_contains "$out" "ended 'failed' and opened no pull request" "failed run: the reason"
  expect_contains "$out" "a value that did not reach it" "failed run: what it means"
}

test_a_launch_that_failed_leaves_the_rows_unmeasured() {
  local dir=$work/nolaunch
  prepare "$dir"
  run194 "$dir" FAKE_FUGARO_MODE=launch-fails
  expect_status "$status" 2 "no launch: the verdict"
  expect_contains "$out" "FACT verify: FAIL" "no launch: the verify row"
  expect_contains "$out" "exited" "no launch: the launch's own failure"
  expect_contains "$out" "FACT mounts: FAIL" "no launch: the mounts row"
}

test_a_run_that_names_no_execution_leaves_the_mounts_row_unmeasured() {
  local dir=$work/noexecution
  prepare "$dir"
  run194 "$dir" FAKE_ROW_NO_EXECUTION=1
  expect_status "$status" 2 "no execution in the row: the verdict"
  expect_contains "$out" "FACT mounts: FAIL" "no execution in the row: the mounts row"
  expect_contains "$out" "names no execution" "no execution in the row: the reason"
}

# The plan's "another repository's job is refused them", read where it can be
# before the flip: the second repository's own record must name neither the
# variable the per-repository entry admits only to the repository under test,
# nor a container of it.
test_another_repository_whose_record_names_the_per_repository_secret_is_a_failure() {
  local dir=$work/otherrecord
  prepare "$dir"
  run194 "$dir" FAKE_CURL_MODE=other-names-one
  expect_status "$status" 1 "the second record: the verdict"
  expect_contains "$out" "FACT other: FAIL" "the second record: the row"
  expect_contains "$out" "its record names a row for DEPLOY_TOKEN" "the second record: what it names"
  expect_contains "$out" "it is not refused" "the second record: the boundary"
}

test_another_repository_whose_job_carries_the_per_repository_secret_is_a_failure() {
  local dir=$work/otherjob
  prepare "$dir"
  run194 "$dir" FAKE_GCLOUD_MODE=other-mounts
  expect_status "$status" 1 "the second job: the verdict"
  expect_contains "$out" "FACT other: FAIL" "the second job: the row"
  expect_contains "$out" "one of its jobs carries DEPLOY_TOKEN at all" "the second job: what it carries"
}

test_never_belong() {
  local dir=$work/belong
  prepare "$dir"
  run194 "$dir" CHECK194_REPO=belong/whatever
  expect_status "$status" 1 "never belong: the verdict"
  expect_contains "$out" "never belong" "never belong: the rule"
  expect_absent "$out" "curl " "never belong: nothing was read"
}

test_a_missing_tool_stops_before_anything_is_called() {
  local dir=$work/notool
  prepare "$dir"
  # Everything on this test's own PATH EXCEPT python3: the reader is what every
  # document is read with, so the check must stop before it calls any tool at
  # all. The whole PATH and not a handful of commands, because the check's own
  # inputs block runs before its main body: a PATH holding only bash and mktemp
  # would die on `date` in an assignment and never reach the requirement it is
  # being tested for.
  local bin=$dir/minbin d f b
  mkdir -p "$bin"
  for d in ${PATH//:/ }; do
    for f in "$d"/*; do
      b=${f##*/}
      case "$b" in python3 | python3.* | python) continue ;; esac
      if [ -x "$f" ] && [ ! -d "$f" ]; then ln -sf "$f" "$bin/$b" 2>/dev/null || true; fi
    done
  done
  status=0
  env PATH="$bin" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    CHECK194_REPO=acme/sandbox-one CHECK194_OTHER_REPO=acme/sandbox-two \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "no python3: the verdict"
  expect_contains "$err" "needs 'python3' on PATH" "no python3: the reason"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "no python3: gcloud was called"; fi
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "no python3: fugaro was called"; fi
}

# ------------------------------------------------------------ the flip, after
# --after-flip's happy path: the mounts and the URL's own reading, the service's
# own lines for the run it launches, the account's accessors, and the run that
# was in flight across the re-apply.
test_after_flip_happy_path() {
  local dir=$work/flip
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 0 "flip: the verdict"
  expect_contains "$out" "MODE: flip" "flip: the mode in the block"
  expect_contains "$out" "FACT no-mounts: PASS" "flip: the mounts are gone"
  expect_contains "$out" "the mount goes, the URL stays" "flip: the URL is kept"
  expect_contains "$out" "FACT served: PASS" "flip: the service's own lines"
  expect_contains "$out" "under purpose workflow-secret" "flip: the purpose"
  expect_contains "$out" "FACT no-accessors: PASS" "flip: the accessor row"
  expect_contains "$out" "FACT inflight-run: PASS" "flip: the run in flight"
  expect_contains "$out" "FACT inflight-mount: PASS" "flip: the spec it kept"
  expect_contains "$out" "kept the spec it was created with" "flip: U9's reading"
  expect_contains "$out" "CHECK 194 RESULT END (exit 0)" "flip: the end line"

  local calls curls gcloud
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "-0198" "flip: the flip mode's own suffix"
  curls=$(cat "$dir/logs/curl.calls")
  expect_absent "$curls" "-X " "flip: nothing was written to the database"
  gcloud=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$gcloud" "secrets get-iam-policy fugaro-project-fugaro-deploy-token-" "flip: the container's own policy was read"
  expect_contains "$gcloud" "logging read" "flip: the service's lines for the run were read"
  expect_contains "$gcloud" "jsonPayload.run=" "flip: the lines are read for the run by id"
  expect_absent "$gcloud" "run jobs describe" "flip: the account is read off the job list and not by a second call"
}

# A job that still hands a run either variable after the flip has not been
# reached by it — and a job with no mount AND no URL is a run that would fetch
# nothing and start with the variable empty.
test_after_flip_refuses_a_job_that_still_hands_out_a_variable() {
  local dir=$work/flipstill
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_GCLOUD_MODE=still-mounts CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "still mounted: the verdict"
  expect_contains "$out" "FACT no-mounts: FAIL" "still mounted: the row"
  expect_contains "$out" "still hand a run a variable the flip should have taken" "still mounted: the reason"
  expect_contains "$out" "B3.8" "still mounted: the release the flip belongs to"
}

test_after_flip_refuses_a_job_that_lost_the_service_url() {
  local dir=$work/flipnourl
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=no-url CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "no URL: the verdict"
  expect_contains "$out" "FACT no-mounts: FAIL" "no URL: the row"
  expect_contains "$out" "carry FUGARO_CREDENTIALS_SERVICE_URL" "no URL: the reason"
  expect_contains "$out" "takes the mounts and KEEPS the URL" "no URL: the rule"
}

# The served row is the service's OWN lines: a run with no workflow-secret line
# of its own, or one naming only one of the record's containers, is a value the
# run did not fetch from the other.
test_after_flip_fails_when_a_container_the_record_names_is_not_served() {
  local dir=$work/flipone
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=one-workflow-line CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "one line: the verdict"
  expect_contains "$out" "FACT served: FAIL" "one line: the row"
  expect_contains "$out" "do NOT name every container" "one line: the reason"
}

test_after_flip_fails_when_the_run_was_served_nothing() {
  local dir=$work/flipnoaudit
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=no-audit CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "no lines: the verdict"
  expect_contains "$out" "FACT served: FAIL" "no lines: the row"
  expect_contains "$out" "do NOT name every container" "no lines: the reason"
}

test_after_flip_fails_when_the_service_only_refused() {
  local dir=$work/fliprefused
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=refused-only CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "refused only: the verdict"
  expect_contains "$out" "FACT served: FAIL" "refused only: the row"
}

# A log read that does not answer is NOT the service having written no line: a
# row that took a failed read for silence would pass the check on nothing.
test_a_log_read_that_failed_leaves_the_served_row_unmeasured() {
  local dir=$work/fliplogfail
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=log-fails CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 2 "log read failed: the verdict"
  expect_contains "$out" "FACT served: FAIL" "log read failed: the row"
  expect_contains "$out" "could not be read" "log read failed: the reason"
  expect_contains "$out" "NOT the service having written no line" "log read failed: the distinction"
  expect_contains "$out" "FACT no-accessors: PASS" "log read failed: the accessor row is still read"
}

# D2074: after the flip the account the run's job runs as holds NO accessor on a
# workflow secret's container — the grants are the credentials service's.
test_after_flip_fails_when_the_job_account_still_holds_an_accessor() {
  local dir=$work/flipaccessor
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=accessor-present CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "accessor: the verdict"
  expect_contains "$out" "FACT no-accessors: FAIL" "accessor: the row"
  expect_contains "$out" "can still read a container" "accessor: the reason"
  expect_contains "$out" "secretAccessor" "accessor: the binding"
  expect_contains "$out" "FACT served: PASS" "accessor: the served row is read first and holds"
}

# An accessor held by SOMEONE ELSE is not the row's reading: the plan's question
# is the job's own account, and the credentials service's own grant on the
# container is exactly what D2074 puts there.
test_after_flip_passes_when_another_member_holds_the_accessor() {
  local dir=$work/flipother
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=accessor-other CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 0 "another accessor: the verdict"
  expect_contains "$out" "FACT no-accessors: PASS" "another accessor: the row"
  expect_contains "$err" "roles/secretmanager.secretVersionManager" "another accessor: the other roles are said out loud"
}

test_a_policy_that_could_not_be_read_leaves_the_accessor_row_unmeasured() {
  local dir=$work/flipiamfail
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=iam-fails CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 2 "iam read failed: the verdict"
  expect_contains "$out" "FACT no-accessors: FAIL" "iam read failed: the row"
  expect_contains "$out" "get-iam-policy" "iam read failed: the call that failed"
  expect_contains "$out" "is not a container nobody can read" "iam read failed: the distinction"
}

# U9: an execution created before the re-apply keeps the container spec it was
# created with, and the run still finishes.
test_an_inflight_run_that_kept_a_spec_with_no_mount_is_a_failure() {
  local dir=$work/flipinflightnomount
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 FAKE_GCLOUD_MODE=execution-no-mount CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "the spec it kept: the verdict"
  expect_contains "$out" "FACT inflight-mount: FAIL" "the spec it kept: the row"
  expect_contains "$out" "does not hold what the flip takes away" "the spec it kept: the reason"
  expect_contains "$out" "keeps the container spec it was created with (U9)" "the spec it kept: the rule"
}

# The template still handing a variable out is the no-mounts row's own FAIL, and
# the U9 row cannot read a retention off it: the execution's mount is not the
# mount the flip took away. The verdict is the hard FAIL's (exit 1) and the
# inflight row is the unmeasured one.
test_an_inflight_run_whose_repository_still_mounts_leaves_the_row_unmeasured() {
  local dir=$work/flipinflightstill
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_GCLOUD_MODE=still-mounts CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight
  expect_status "$status" 1 "the template still mounts: the verdict"
  expect_contains "$out" "FACT inflight-mount: FAIL" "the template still mounts: the row"
  expect_contains "$out" "the flip has not reached that repository's jobs" "the template still mounts: the reason"
  expect_contains "$out" "FACT no-mounts: FAIL" "the template still mounts: the no-mounts row"
}

test_an_inflight_run_that_started_after_the_reapply_is_not_the_u9_case() {
  local dir=$work/flipafter
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight \
    FAKE_INFLIGHT_STARTED=2026-11-17T10:00:00Z
  expect_status "$status" 1 "started after: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "started after: the row"
  expect_contains "$out" "not before" "started after: the reason"
  expect_contains "$out" "Name the run that was already going" "started after: the fix"
}

test_a_run_in_flight_that_had_not_finished_is_a_failure() {
  local dir=$work/fliprunning
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 CHECK194_INFLIGHT=acme-sandbox-one-inflight FAKE_INFLIGHT=acme-sandbox-one-inflight \
    FAKE_ROW_INFLIGHT=1
  expect_status "$status" 1 "still running: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "still running: the row"
  expect_contains "$out" "had not reached a final status" "still running: the reason"
}

test_a_named_run_that_is_not_in_the_listing_leaves_the_row_unmeasured() {
  local dir=$work/flipnolisting
  prepare "$dir"
  run194 "$dir" --after-flip FAKE_FLIPPED=1 CHECK194_INFLIGHT=acme-sandbox-one-absent
  expect_status "$status" 2 "not listed: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "not listed: the row"
  expect_contains "$out" "is not in acme/sandbox-one's listing" "not listed: the reason"
}

# The flip's own guard: --after-flip's run in flight is REQUIRED, and a check
# that picked one itself could pick one that ended hours before the re-apply.
test_the_flip_without_a_run_id_stops() {
  local dir=$work/flipnorun
  prepare "$dir"
  run194 "$dir" --after-flip
  expect_status "$status" 1 "no inflight run: the verdict"
  expect_contains "$out" "CHECK194_INFLIGHT must name the run" "no inflight run: the reason"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "no inflight run: gcloud was called after the refusal"; fi
}

# ------------------------------------------------------------------- the shard
# The suite runs in shards in CI (scripts/live_checks_test.go): CHECK_TEST_SHARD
# is "i/n" and CHECK_TEST_ONLY names one case. Without either, everything runs.
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

t test_dry_run
t test_dry_run_flip
t test_the_project_guard_reads_the_line_not_doctors_status
t test_project_guard
t test_xtrace_is_refused
t test_the_inputs_guard
t test_a_development_build_is_a_failure
t test_a_job_list_that_could_not_be_read_leaves_the_record_unmeasured
t test_a_job_without_the_repo_label_leaves_the_record_unmeasured
t test_the_record_is_read_at_the_slug
t test_a_record_that_is_not_there_is_unmeasured
t test_a_record_that_could_not_be_read_names_the_status_and_the_path
t test_a_record_with_a_key_this_release_does_not_model_is_a_failure
t test_a_record_naming_a_repository_container_is_a_failure
t test_a_record_naming_no_container_for_one_variable_is_a_failure
t test_a_record_naming_one_container_for_both_variables_is_a_failure
t test_the_happy_path
t test_the_happy_path_with_the_scratch_directory_kept
t test_a_mount_from_another_container_is_a_failure
t test_a_plain_value_on_a_job_is_a_failure
t test_an_execution_that_could_not_be_read_leaves_the_mounts_row_unmeasured
t test_a_run_that_did_not_finish_is_a_failure
t test_a_launch_that_failed_leaves_the_rows_unmeasured
t test_a_run_that_names_no_execution_leaves_the_mounts_row_unmeasured
t test_another_repository_whose_record_names_the_per_repository_secret_is_a_failure
t test_another_repository_whose_job_carries_the_per_repository_secret_is_a_failure
t test_never_belong
t test_a_missing_tool_stops_before_anything_is_called
t test_after_flip_happy_path
t test_after_flip_refuses_a_job_that_still_hands_out_a_variable
t test_after_flip_refuses_a_job_that_lost_the_service_url
t test_after_flip_fails_when_a_container_the_record_names_is_not_served
t test_after_flip_fails_when_the_run_was_served_nothing
t test_after_flip_fails_when_the_service_only_refused
t test_a_log_read_that_failed_leaves_the_served_row_unmeasured
t test_after_flip_fails_when_the_job_account_still_holds_an_accessor
t test_after_flip_passes_when_another_member_holds_the_accessor
t test_a_policy_that_could_not_be_read_leaves_the_accessor_row_unmeasured
t test_an_inflight_run_that_kept_a_spec_with_no_mount_is_a_failure
t test_an_inflight_run_whose_repository_still_mounts_leaves_the_row_unmeasured
t test_an_inflight_run_that_started_after_the_reapply_is_not_the_u9_case
t test_a_run_in_flight_that_had_not_finished_is_a_failure
t test_a_named_run_that_is_not_in_the_listing_leaves_the_row_unmeasured
t test_the_flip_without_a_run_id_stops

if [ "$failures" -ne 0 ]; then
  echo "check194_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check194_test.sh: ok"
