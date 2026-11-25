#!/usr/bin/env bash
# Tests for scripts/live-checks/check193.sh, with fake `fugaro`, `gcloud` and
# `curl` on PATH: NOTHING here reaches a cloud, a project, a repository or the
# network, no agent runs a live command, and the fake tools record every
# argument they are given so the test can assert that no command ever named
# anything but the sandbox installation's project and that nothing was written
# anywhere (Check 193 creates no cloud resource and writes no record).
#
# The fakes are a small project of their own: two sandbox repositories with a
# Cloud Run job each, a credentials-service switch the test turns on and off, a
# published policy that proxies one of them, one `creds/repoauth` record per
# repository that the fake `curl` serves out of the installation's Realtime
# Database, throwaway runs that are launched, listed and then answered by the
# service's own `credsvc: audit` lines — which is where every row of the check
# comes from.
#
# Usage: bash scripts/live-checks/check193_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check193.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK193_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c ${CHECK193_TEST_TAIL:-700})" ;;
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

# The two sandbox repositories every case runs against: one whose record says
# api-key and one whose says oauth, which is the pair the check needs.
REPOS_DEFAULT="acme/sandbox-api,acme/sandbox-oauth"

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
"""A stand-in for `fugaro`, for scripts/live-checks/check193.sh's test only.

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


def positional(n):
    ps = [a for a in args if not a.startswith("-")]
    return ps[n] if len(ps) > n else ""


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
    `projects/<project>/locations/<region>/jobs/<job>/executions/<name>`
    (internal/backend's Execution.Name, the shape check139 validates), which is
    what the flip's U9 row describes. A row whose record carries none — a run
    from before the launcher recorded one — is the state FAKE_ROW_NO_EXECUTION
    writes, and the row the check must say unmeasured about."""
    return "projects/fugaro-dev/locations/us-east5/jobs/%s-0123456789abcdef/executions/%s" % (
        repo.lower().replace("/", "-"), rid)


def account_for(repo):
    """The account a run of this repository declares, as the run's OWN row
    spells it: FAKE_OAUTH_ACCOUNT, which is EMPTY for the default account —
    a task spec may not say "default", so a run on the repository's default
    account declares none and its row carries no `oauth_account` at all
    (internal/task's OAuthToken rule, internal/runview's row) — and the bare
    account name for a named one. An api-key repository's run declares none
    either: its credential is the paid key."""
    if repo == (repos[1] if len(repos) > 1 else ""):
        return os.environ.get("FAKE_OAUTH_ACCOUNT", "")
    return ""


if args[:1] == ["version"]:
    # `fugaro version` prints the version and nothing else (cli.root's
    # versionTemplate): `dev` on a checkout build, the release's number on a
    # released one.
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
    if os.environ.get("FAKE_INFLIGHT", "") and repo == os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else ""):
        rows.append(os.environ["FAKE_INFLIGHT"])
    for r, rid in runs_state():
        if r != repo:
            continue
        rows.append(rid)
    out = []
    for rid in rows:
        row = {"run_id": rid, "repo": repo, "status": os.environ.get("FAKE_ROW_STATUS", "succeeded"),
               "terminal": True, "outcome": "ready", "pushed": True, "pr": 7,
               "started_at": os.environ.get("FAKE_ROW_STARTED", "2026-11-16T08:30:00Z")}
        if not os.environ.get("FAKE_ROW_NO_EXECUTION"):
            row["execution"] = execution_path(repo, rid)
        acct = account_for(repo)
        if acct:
            row["oauth_account"] = acct
        if row["status"] != "succeeded":
            row["reason"] = "the run failed (test)"
            row["pr"] = 0
            row["pushed"] = False
            halt = {"reason": "agent", "detail": "the agent stopped before opening a pull request (test)"}
            row["halt"] = halt
        out.append(row)
    print(json.dumps({"runs": out}))
elif args[:1] == ["project"] and args[1:2] == ["policy"]:
    if mode == "policy-fails":
        sys.stderr.write("fugaro: the published policy could not be read (test)\n")
        sys.exit(1)
    proxy_repo = os.environ.get("FAKE_PROXY_REPO", repos[1] if len(repos) > 1 else "")
    allow = ["bitbucket/" + proxy_repo] if proxy_repo else []
    policy = {"version": 9,
              "allow_repos": ["bitbucket/%s/*" % (repos[0].split("/")[0] if repos else "acme")],
              "bitbucket_token": {"allow_repos": allow,
                                  "proxy": mode != "no-proxy"}}
    # A policy that carries no budget block at all: what a `show` on an
    # installation with nothing published answers, and the shape whose absent
    # mode must never read as a mode the gateway exists in (D2071's third leg).
    if mode != "policy-no-mode":
        policy["budget"] = {"mode": "observe"}
    print(json.dumps({"policy": policy}))
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
"""A stand-in for `gcloud`, for scripts/live-checks/check193.sh's test only: no
socket is opened and nothing is created.

It answers four reads: the installation's job list (and one job's own
specification, and one execution's), the access token the installation's
Realtime Database record is read with, and Cloud Logging — whose lines for a run
are the service's own `credsvc: audit` lines, built here from the mode the case
names.
"""
import hashlib, json, os, re, sys

LOG = os.environ["FAKE_LOG_DIR"]
STATE = os.environ["FAKE_GCLOUD_STATE"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
positionals = [a for a in args if not a.startswith("-")]
# Like the real CLI, a subcommand or a flag that does not exist is an error: a
# misspelled one therefore cannot hide in any call this check makes.
ALLOWED = {
    ("auth", "print-access-token"): (),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("run", "jobs", "describe"): ("--project", "--region", "--format"),
    ("run", "jobs", "executions", "describe"): ("--project", "--region", "--format"),
    ("run", "services", "get-iam-policy"): ("--project", "--region", "--format"),
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
    """Both the --name=value and the --name value spellings: the check passes
    flags the way an owner would on a command line."""
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

# The moment `fugaro update --repo` last reached the repositories' jobs, and the
# moment the runs of the cases below started: an in-flight run is one that
# started BEFORE the update, which is the order the check's own arithmetic reads.
UPDATE = "2026-11-16T09:00:00.000000Z"


def job_name(repo):
    return "%s-0123456789abcdef" % repo.lower().replace("/", "-")


def secret_id(slug, logical):
    """gcp.SecretID: the DERIVED Secret Manager ID a job's env is fed from —
    `fugaro-<readable slug>-<logical>-<16 hex of sha256(slug NUL logical)>`
    (internal/backend/gcp/names.go: derive, readableSlug and secretHashHex).
    The logical name is the LAST readable segment and never the first, and the
    readable part drops the slug's own 16-hex tail, so this is the shape a
    deployment writes and the only one a case can use to test the check's
    mount reading."""
    readable = re.sub(r"-[0-9a-f]{16}$", "", slug.lower())
    h = hashlib.sha256(("%s\x00%s" % (slug, logical)).encode()).hexdigest()[:16]
    return "fugaro-%s-%s-%s" % (readable, logical, h)


def job_account(repo):
    """The account a repository's job runs as, as the deployment writes it: the
    service account's own EMAIL, which is the member the credentials service's
    invoker grant names (modules/workflow/sa.tf: google_service_account.job.member
    is `serviceAccount:<email>` and the job template carries the bare email)."""
    return "fugaro-%s@fugaro-dev.iam.gserviceaccount.com" % job_name(repo)


def job_doc(repo):
    """One repository's own job, as `gcloud run jobs list` answers it. A
    PER-REPOSITORY job template carries no FUGARO_CREDENTIALS_SERVICE_URL: the
    launcher writes that on each run's execution (internal/cli/run.go's
    credentialsServiceLaunchEnv), which is why the switch row can never be read
    off it — `job-url` is the other shape, a SHARED project job's, where
    internal/infra's projectJobEnv bakes the address in the template."""
    envs = [{"name": "FUGARO_REMOTE", "value": "https://bitbucket.org/%s.git" % repo}]
    if mode == "job-url":
        envs.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL",
                     "value": "https://fugaro-credentials-fake-ue.a.run.app"})
    if mode in ("mounted", "unswitched") and repo == (repos[1] if len(repos) > 1 else ""):
        # The mount the flip exists to take off: the container the paid key is
        # served from, as the deployment mounts it (config.ModelSecretEnvs and
        # the derived Secret Manager ID behind it — never the logical name,
        # which is what a check reading the ID by prefix would need to see).
        envs.append({"name": "ANTHROPIC_API_KEY",
                     "valueSource": {"secretKeyRef": {
                         "secret": secret_id(job_name(repo), "anthropic-api-key"),
                         "version": "latest"}}})
    if mode == "mounted-account" and repo == (repos[1] if len(repos) > 1 else ""):
        # The other shape a real job is fed from: a NAMED Claude account's own
        # container, whose logical name carries the account after the token's
        # (config.ClaudeOAuthTokenPrefix, `claude-oauth-token-<account>`) — the
        # mount the oauth sandbox repository's job holds before the flip.
        envs.append({"name": "CLAUDE_CODE_OAUTH_TOKEN",
                     "valueSource": {"secretKeyRef": {
                         "secret": secret_id(job_name(repo), "claude-oauth-token-work"),
                         "version": "latest"}}})
    if mode == "stage-account" and repo == (repos[0] if repos else ""):
        # A MIXED stage account's own job, which is the one mount a switched job
        # keeps (D2072's is_stage_account_token): the stage's own Claude token,
        # and nothing else — and on the API-KEY repository, which is the only
        # auth that builds such a job at all (mixedStageAccountJob reads
        # cfg.Agent.Auth). The fixture carries NO FUGARO_STAGE_ACCOUNT_SERVED on
        # purpose: that is whether the SERVICE selects the stage's account
        # (local.StageAccountServedOn), a fact of its own, and the mount stays
        # whether it is set or not — a check that asked the job for the flag
        # would call this switched-and-correct job off the switch.
        envs.append({"name": "CLAUDE_CODE_OAUTH_TOKEN",
                     "valueSource": {"secretKeyRef": {
                         "secret": secret_id(job_name(repo), "claude-oauth-token-work"),
                         "version": "latest"}}})
    if mode == "provider-key":
        envs.append({"name": "FUGARO_PROVIDER_KEY_OPENROUTER",
                     "valueSource": {"secretKeyRef": {"secret": "openrouter-key-abcdef", "key": "latest"}}})
    name = job_name(repo)
    # The label the record row reads the record's key out of: a job list whose
    # jobs carry none is the state a check that spelled the key out of the
    # repository's own name would paper over.
    labels = {} if mode == "no-repo-label" else {"fugaro_repo": name}
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Job",
            "metadata": {"name": name, "labels": labels, "updateTime": UPDATE},
            "spec": {"template": {"spec": {"template": {"spec": {
                "containers": [{"name": "workflow", "env": envs}],
                "serviceAccountName": job_account(repo)}}}}}}


def service_iam_doc():
    """The credentials service's own IAM policy, as `gcloud run services
    get-iam-policy fugaro-credentials --format=json` answers it: the members of
    its roles/run.invoker binding are the job accounts of the repositories whose
    root was applied with the credentials service on (modules/workflow/sa.tf's
    google_cloud_run_v2_service_iam_member.credentials, D135) — the half of the
    per-repository switch the job template cannot show.

    `no-invoker` is the state the switch row exists to catch: the root was
    applied with the service off, so no grant was written. `svciam-invoker-other`
    grants the invoker role to an account that is nobody's job — a row that read
    any member of any binding as the grant would pass on it. The
    secretAccessor binding is the service's own account reading the containers it
    serves: a member of a binding that is NOT roles/run.invoker is never a
    caller."""
    members = ["serviceAccount:" + job_account(r) for r in repos]
    if mode in ("no-invoker", "unswitched"):
        members = []
    if mode == "svciam-invoker-other":
        members = ["serviceAccount:some-other-job@fugaro-dev.iam.gserviceaccount.com"]
    return {"etag": "BwYXfake", "version": 3,
            "bindings": [
                {"role": "roles/run.invoker", "members": members},
                {"role": "roles/secretmanager.secretAccessor",
                 "members": ["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com"]}]}


def execution_doc(repo, name):
    """One execution of a repository's job, as `gcloud run jobs executions
    describe --format=json` answers it: `kind: Execution` with its containers at
    spec.template.spec.containers (the other depth a Job's live at, which the
    check's reader reads both of).

    Its containers are the PRE-flip template's — the model mount and the
    service's URL both on them — because that is the spec an execution created
    before the re-apply was created with and keeps for its whole life (U9), and
    it is the mount the flip's row reads. `execution-no-mount` is the other
    shape: a spec with no model credential on it at all, which is what a run
    launched by the FLIPPED template was created with, and the one the row must
    never read as a mount a run in flight kept.
    """
    envs = [{"name": "FUGARO_REMOTE", "value": "https://bitbucket.org/%s.git" % repo},
            {"name": "FUGARO_CREDENTIALS_SERVICE_URL",
             "value": "https://fugaro-credentials-fake-ue.a.run.app"}]
    if mode != "execution-no-mount":
        envs.append({"name": "ANTHROPIC_API_KEY",
                     "valueSource": {"secretKeyRef": {
                         "secret": secret_id(job_name(repo), "anthropic-api-key"),
                         "version": "latest"}}})
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Execution",
            "metadata": {"name": name, "labels": {"run.googleapis.com/job": job_name(repo)},
                         "updateTime": UPDATE},
            "spec": {"template": {"spec": {
                "containers": [{"name": "workflow", "env": envs}]}}}}


if sub == ("auth", "print-access-token"):
    print("fake-access-token-for-the-check-test")
elif sub == ("run", "jobs", "list"):
    if mode == "jobs-list-fails":
        sys.stderr.write("ERROR: (gcloud) run.jobs.list was refused: the session has expired\n")
        sys.exit(1)
    docs = [] if mode == "no-jobs" else [job_doc(r) for r in repos]
    print(json.dumps(docs))
elif sub == ("run", "services", "get-iam-policy"):
    # The credentials service's own policy, which is the per-repository half of
    # the switch the job template cannot show (the job account's
    # roles/run.invoker). A read that did not answer is NOT a policy without the
    # grant: `svciam-refused` is the refusal an owner without
    # run.services.getIamPolicy meets, and `svciam-null` a `get-iam-policy`
    # whose output carries no policy at all.
    if mode == "svciam-refused":
        sys.stderr.write("ERROR: (gcloud) PERMISSION_DENIED: Permission 'run.services.getIamPolicy' denied on resource 'namespaces/fugaro-dev/services/fugaro-credentials'\n")
        sys.exit(1)
    if mode == "svciam-null":
        print("null")
    else:
        print(json.dumps(service_iam_doc()))
elif sub == ("run", "jobs", "describe"):
    want = positionals[3] if len(positionals) > 3 else ""
    for r in repos:
        if job_name(r) == want:
            print(json.dumps(job_doc(r)))
            break
    else:
        sys.stderr.write("ERROR: (gcloud) run.jobs.get: the job %s was not found\n" % want)
        sys.exit(1)
elif sub == ("run", "jobs", "executions", "describe"):
    if mode == "executions-describe-fails":
        # A read that does not answer at all: the execution's own spec is not in
        # the file it writes, which is NOT an execution with no mount.
        sys.stderr.write("ERROR: (gcloud) run.jobs.executions.get was refused: the caller does not have permission to read the execution\n")
        sys.exit(1)
    want = positionals[4] if len(positionals) > 4 else ""
    print(json.dumps(execution_doc(os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else ""), want)))
elif sub == ("logging", "read"):
    if mode == "log-fails":
        # A log read that does not answer at all: no line of any run's is in the
        # file it writes, which is NOT the same thing as the service having
        # written none — the distinction the rows that read a run's lines (the
        # audit, served and declared-account rows) are read against.
        sys.stderr.write("ERROR: (gcloud) logging.read was refused: the caller does not have permission to read the log\n")
        sys.exit(1)
    filt = positionals[2] if len(positionals) > 2 else ""
    m = re.search(r'jsonPayload\.run="([^"]*)"', filt)
    rid = m.group(1) if m else ""
    inflight = os.environ.get("FAKE_INFLIGHT", "")
    repo_of_run = ""
    path = os.path.join(STATE, "fugaro.runs")
    if os.path.exists(path):
        for line in open(path).read().splitlines():
            parts = line.split(" ", 1)
            if len(parts) == 2 and parts[1] == rid:
                repo_of_run = parts[0]
    if rid == inflight:
        repo_of_run = os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else "")
    proxy_repo = os.environ.get("FAKE_PROXY_REPO", repos[1] if len(repos) > 1 else "")
    oauth_repo = repos[1] if len(repos) > 1 else ""
    # The credential a tier-A line names is the ACCOUNT and never the secret
    # container: internal/credsvc's bundle loader names a Claude account
    # credential after the account — `default` for the unsuffixed
    # claude-oauth-token, the bare name for claude-oauth-token-<account> — and
    # the audit line writes that name (bundle.go's credential names, and
    # narrowToRepoAuth's `tier A && Name == the declared account`).
    account = os.environ.get("FAKE_OAUTH_ACCOUNT", "")
    want = account or "default"

    def line(credential, tier, purpose):
        p = {"message": "credsvc: audit", "run": rid, "repository": repo_of_run.replace("/", "-"),
             "job": job_name(repo_of_run or (repos[0] if repos else "")),
             "caller": "fugaro-%s@fugaro-dev.iam.gserviceaccount.com" % job_name(repo_of_run or "x"),
             "status": "ok", "credential": credential, "purpose": purpose}
        if tier:
            p["tier"] = tier
        return {"jsonPayload": p}

    lines = []
    if rid and rid != inflight and mode != "no-audit":
        if repo_of_run == oauth_repo:
            if mode == "oauth-not-recorded":
                # A tier-A line naming an account this repository's own record
                # does not name: not a credential the service could have served
                # its run out of.
                lines.append(line("someone-else", "A", "forward"))
            else:
                lines.append(line(want, "A", "forward"))
                if mode == "second-account":
                    lines.append(line("other", "A", "forward"))
        else:
            lines.append(line("default" if mode == "wrong-credential" else "anthropic-api-key",
                              "A" if mode == "wrong-credential" else "B", "forward"))
    if mode == "inflight-served" and rid == inflight:
        lines.append(line("anthropic-api-key", "B", "forward"))
    if rid and repo_of_run == proxy_repo and mode != "no-git" and mode != "no-audit" and rid != inflight:
        # The run's own git traffic, forwarded to bitbucket.org: the purpose is
        # `git` and the credential is the bundle's Bitbucket token, which — like
        # every credential that is not a model credential — carries no tier.
        lines.append(line("bitbucket-token", "", "git"))
    print(json.dumps(lines))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
"""A stand-in for `curl`, for scripts/live-checks/check193.sh's test only: no
socket is opened.

It answers the one GET the check makes — the installation's own Realtime
Database, at the path `fugaro update` writes a repository's record to
(creds/repoauth/<slug>) — out of the two records the case names, and it refuses
the GET the way the real database does when the node is not there (404). The
record it hands back is READ by the check and never written by it, which is the
only thing this fake has to get right: nothing here accepts a write.
"""
import json
import os, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_CURL_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]
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


def body_for(slug):
    """The two records: the api-key repository's and the oauth one's. The oauth
    record names TWO accounts (the default and one other), which is what makes
    the declared-account row measurable at all — a record naming one account has
    nothing the run could have been served besides it.

    A record is keyed by the repository's SLUG and not by its name, which is why
    every branch below compares against job_key(): the node `fugaro update
    --repo` writes is creds/repoauth/<readable>-<16 hex>, and a GET at
    creds/repoauth/<owner>-<name>.json is the node nobody writes — the real
    database answers it 404, and this fake answers it 404 too, so a check that
    spelled the key out of the repository's name cannot pass here.
    """
    if mode == "record-cascade":
        return {"version": 1, "auth": "cascade", "accounts": []}
    if mode == "record-both-api-key":
        return {"version": 1, "auth": "api-key", "accounts": []}
    if slug == job_key(repos[1] if len(repos) > 1 else ""):
        if mode == "record-one-account":
            return {"version": 1, "auth": "oauth", "accounts": ["default"]}
        if mode == "record-named-account":
            return {"version": 1, "auth": "oauth", "accounts": ["default", "work"]}
        return {"version": 1, "auth": "oauth", "accounts": ["default", "other"]}
    if slug == job_key(repos[0] if repos else ""):
        return {"version": 1, "auth": "api-key", "accounts": []}
    return None


def job_key(repo):
    """The slug a repository's record is keyed by: the readable path, "-" and 16
    hex of the hash (task.Slug), which is the same spelling the fake `gcloud`
    writes as its jobs' `fugaro_repo` label — one key, written once."""
    return repo.lower().replace("/", "-") + "-0123456789abcdef"


slug = url.rsplit("/creds/repoauth/", 1)[-1].replace(".json", "") if "/creds/repoauth/" in url else ""
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
  # CHECK193_TEST_XTRACE starts the shell the way an owner might by habit: with
  # tracing on, which is the one leak this check's own redaction cannot catch.
  local -a envs=() cli=() runner=(bash) a
  for a in "$@"; do
    case "$a" in
    CHECK193_TEST_XTRACE=*) runner=(bash -x) ;;
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
    FAKE_REPOS="$REPOS_DEFAULT" \
    CHECK193_REPOS="$REPOS_DEFAULT" \
    CHECK193_RUN_SECONDS=0 CHECK193_POLL_SECONDS=0 CHECK193_LOG_WAIT_SECONDS=0 \
    CHECK193_LOG_POLL_SECONDS=0 \
    ${envs[@]+"${envs[@]}"} \
    "${runner[@]}" "$sh" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run193() { # dir arg...: the check itself
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
# even the installation's project is read, which is why the fugaro log is empty
# too — the two guards that read the cloud are not reached.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run193 "$dir" CHECK193_DRY_RUN=1
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 193 RESULT" "dry run: the block"
  expect_contains "$out" "FACT repos: PASS" "dry run: the two repositories"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the shell is not tracing"
  expect_contains "$out" "FACT project-region: FAIL" "dry run: the cloud guards are not run"
  expect_contains "$out" "FACT project-region: FAIL — not reached" "dry run: the row says it was not reached"
  expect_contains "$out" "FACT switch: FAIL" "dry run: nothing was read"
  expect_contains "$out" "not reached (the run stopped before this step)" "dry run: the row says it was not reached"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS" "dry run: nothing was created"
  expect_contains "$out" "FACT scratch-removed: PASS" "dry run: the scratch directory"
  expect_contains "$out" "CHECK 193 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "creds/repoauth/<slug>.json" "dry run: the plan names the record it would read"
  expect_contains "$err" "fugaro run --repo" "dry run: the plan names the runs it would launch"
  expect_contains "$err" "gcloud run jobs list --project=fugaro-dev --region=us-east5" "dry run: the plan carries the hard-coded project and region"
  expect_contains "$err" "gcloud run services get-iam-policy fugaro-credentials --project=fugaro-dev --region=us-east5 --format=json" \
    "dry run: the plan names the read that carries the invoker half of the switch"

  local calls
  calls=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls" "$dir/logs/curl.calls")
  if [ -n "$calls" ]; then
    fail "dry run: a tool was called: $(printf '%s' "$calls" | head -c 300)"
  fi
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

# doctor exits 1 whenever any check of its own failed — billing, credentials,
# the update check — and none of those says which installation this is. The
# guard must read the project: LINE and not the status, or a check that failed
# for an unrelated reason would stop a run that should go.
test_the_project_guard_reads_the_line_not_doctors_status() {
  local dir=$work/doctorfails
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=failing
  expect_status "$status" 0 "doctor exit 1: the run goes on"
  expect_absent "$err" "not the fugaro-dev installation" "doctor exit 1: no stop"
  expect_contains "$out" "FACT served-oauth: PASS" "doctor exit 1: the check ran to the end"
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=other
  expect_status "$status" 1 "project guard: the verdict"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run" "project guard: nothing was read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run --repo" "project guard: no run was launched"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# A trace would print every command this check runs into the log the owner
# pastes, including the `gcloud auth print-access-token` line it mints the
# record's read with — which is the one thing the check's own redaction cannot
# catch, because it is bash that writes those lines and not this script.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run193 "$dir" CHECK193_TEST_XTRACE=1
  expect_status "$status" 1 "xtrace: the verdict"
  expect_contains "$err" "is tracing" "xtrace: the stop"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "xtrace: no run was launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "print-access-token" "xtrace: no token was minted"
  if [ -d "$scratch" ]; then fail "xtrace: the scratch directory survived"; fi
}

# The two knobs are the check's whole input, and both are read before anything
# is called: a run that named one repository, three, the same one twice, or the
# owner's own workspace must stop with nothing launched and nothing read.
test_the_repos_guard() {
  local dir=$work/reposguard case_
  prepare "$dir"
  for case_ in "one:acme/sandbox-api" "three:acme/a,acme/b,acme/c" "twice:acme/a,acme/a" "belong:belong/app,acme/ok"; do
    status=0
    env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_REPOS="$REPOS_DEFAULT" \
      CHECK193_REPOS="${case_#*:}" \
      bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
    out=$(cat "$dir/out")
    err=$(cat "$dir/err")
    expect_status "$status" 1 "CHECK193_REPOS ${case_%%:*}: the verdict"
    expect_contains "$out" "FACT aborted: FAIL" "CHECK193_REPOS ${case_%%:*}: the block says it stopped"
    expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "CHECK193_REPOS ${case_%%:*}: no run was launched"
    expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "CHECK193_REPOS ${case_%%:*}: nothing was read"
    : >"$dir/logs/fugaro.calls"
    : >"$dir/logs/gcloud.calls"
  done
}

# The switch row is read off what the APPLY changes, per repository, and never
# off FUGARO_CREDENTIALS_SERVICE_URL: a per-repository job template never carries
# that variable at all (job_doc above writes none, and no module under
# deploy/terraform/gcp/modules/workflow puts one on the template — the launcher
# writes the address on each run's EXECUTION, internal/cli/run.go's
# credentialsServiceLaunchEnv), so a row read off the template's URL calls every
# switched per-repository job off the switch whatever the apply did. What a
# switched job really carries is the two facts the plan writes: no model
# credential on it (dropModelMounts, D2072/G17) and its own account granted
# roles/run.invoker on the credentials service (modules/workflow/sa.tf, D135 —
# the same switch, infra.CredentialsServiceServes, that makes the launcher write
# the address at all).
#
# The one mount a switched job KEEPS is a mixed stage account's own Claude token,
# and the mix is api-key's alone (mixedStageAccountJob reads cfg.Agent.Auth), so
# that mount is judged by the auth the repository's own RECORD names and never by
# a flag on the job: under `api-key` it is the stage's credential and the job is
# on the switch, under `oauth` the very same token is the run's own credential
# and the job is off it.
#
# A repository whose job still mounts the credential the service serves and
# whose account holds no invoker grant is OFF the switch: its runs are served by
# nothing, so every row below it would be a row about a repository this check is
# not about. It must stop before a run is launched — the runs are the expensive
# part, and a FAIL that costs two of them is worse than one that costs none —
# and it names BOTH causes, because an owner who read only one of them would
# take the row for a finding about the other.
test_a_repository_off_the_switch_is_a_failure() {
  local dir=$work/offswitch
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=unswitched
  expect_status "$status" 1 "off the switch: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "off the switch: the row"
  expect_contains "$out" "OFF the credentials-service switch" "off the switch: the row says why"
  expect_contains "$out" "on=no why=no-mount+no-invoker" "off the switch: the row names the job, why it is off, and no more than why"
  expect_contains "$out" "acme-sandbox-api-0123456789abcdef" "off the switch: the row names the job"
  expect_contains "$out" "fugaro-acme-sandbox-api-0123456789abcdef@fugaro-dev.iam.gserviceaccount.com" \
    "off the switch: and the account that holds no grant"
  expect_contains "$out" "fugaro update --repo" "off the switch: the row names the fix"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "off the switch: no run was launched"
}

# The mount half on its own, with the grant in place: the row is a FAIL all the
# same, and the whole of it is the credential the flip exists to take off the
# job — the URL is not asked for, because a per-repository job has none.
test_a_job_that_still_mounts_the_model_credential_is_off_the_switch() {
  local dir=$work/switchmount
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=mounted
  expect_status "$status" 1 "still mounted: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "still mounted: the row"
  expect_contains "$out" "on=no why=mounts:fugaro-acme-sandbox-oauth-anthropic-api-key-" \
    "still mounted: the job is named with the container its env is fed from"
  expect_contains "$out" "carries no FUGARO_CREDENTIALS_SERVICE_URL at all" \
    "still mounted: the row says where the address really is"
  expect_contains "$out" "fugaro update --repo" "still mounted: the row names the fix"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "still mounted: no run was launched"
}

# The grant half on its own: the jobs mount nothing, and their accounts were
# never granted the service — the state of a repository root applied with the
# service off, or one whose apply stopped between the mounts and the grant. Every
# run of it would halt at the service's door (D143), so it stops here.
test_a_job_account_without_the_invoker_grant_is_off_the_switch() {
  local dir=$work/switchgrant
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=no-invoker
  expect_status "$status" 1 "no grant: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "no grant: the row"
  expect_contains "$out" "on=no why=no-mount+no-invoker" "no grant: the unmounted job is off on the grant alone"
  expect_contains "$out" "roles/run.invoker on fugaro-credentials" "no grant: the row names the role and the service"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no grant: no run was launched"
}

# An invoker grant is a grant to THAT job's account and to no one else
# (modules/workflow/sa.tf writes one member): a binding whose members are other
# accounts is not this job's grant, and a row that read "any member of
# roles/run.invoker" as the grant would pass a repository whose runs cannot
# reach the service at all.
test_an_invoker_grant_to_another_account_is_not_this_jobs_grant() {
  local dir=$work/switchother
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=svciam-invoker-other
  expect_status "$status" 1 "another account's grant: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "another account's grant: the row"
  expect_contains "$out" "no-invoker" "another account's grant: the job's own account holds none"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "another account's grant: no run was launched"
}

# The one shape in which a job template DOES carry the address is the shared
# project job's (internal/infra's projectJobEnv bakes it, and modules/project's
# sa.tf grants that job's account the invoker role in the same plan), so the URL
# settles the unmounted half on its own there — and it stays the reading for that
# shape, which is Check 197's subject and not this one's.
test_a_job_whose_template_carries_the_url_is_on_the_switch() {
  local dir=$work/switchurl
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=job-url
  expect_status "$status" 0 "the baked URL: the verdict"
  expect_contains "$out" "FACT switch: PASS" "the baked URL: the row"
  expect_contains "$err" "acme/sandbox-oauth: 1 of 1 job(s) on the credentials-service switch" \
    "the baked URL: the job is read as switched, on the address its template carries"
}

# The one mount a switched job KEEPS: a mixed stage account's own Claude token,
# which is the STAGE's credential and not the run's (D2072's
# is_stage_account_token) — and which the row has to judge by the repository's
# AUTH, because the same single token is a correct stage variant on an api-key
# repository and the run's own credential on an oauth one. The fixture carries
# the mount and NOT R12's FUGARO_STAGE_ACCOUNT_SERVED (config.StageAccountServedEnv):
# that fact says whether the SERVICE selects the account and is planned from a
# record of its own, so an installation without it plans a switched job exactly
# like this one — a row that asked the job for the flag fails a repository that
# is switched and correct, which is the same false failure the URL reading was.
test_a_stage_accounts_own_token_is_not_a_reason_to_call_a_job_off_the_switch() {
  local dir=$work/switchstage
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=stage-account
  expect_status "$status" 0 "the stage account: the verdict"
  expect_contains "$out" "FACT switch: PASS" "the stage account: the row"
  expect_contains "$err" "acme/sandbox-api: 1 of 1 job(s) on the credentials-service switch" \
    "the stage account: the api-key repository's job is on the switch despite the token it keeps"
}

# The same single mount on the OTHER auth, which is the reading that must not be
# lost to the exception above: on an `oauth` repository the account's token IS
# the run's model credential — the credential the flip exists to take off the
# job — and no job of such a repository is a stage variant (mixedStageAccountJob
# answers false for every auth but api-key), so the job is OFF the switch.
test_an_oauth_jobs_token_is_the_mount_the_flip_takes_away() {
  local dir=$work/switchoauthtoken
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=mounted-account
  expect_status "$status" 1 "an oauth token mount: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "an oauth token mount: the row"
  expect_contains "$out" "on=no why=mounts:fugaro-acme-sandbox-oauth-claude-oauth-token-work-b83d639928880e02" \
    "an oauth token mount: the job is off it, with the container its env is fed from"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "an oauth token mount: no run was launched"
}

# The job list is the read the whole row stands on, and it is read FIRST because
# the auth the row judges its exception by comes out of the record row, which is
# keyed by the jobs themselves. A `gcloud run jobs list` that did not answer is
# therefore the switch row's own FAIL — a criterion that did not hold, exit 1 —
# and never one of the rows this check could not measure: a second entry for the
# switch key would put it among those and report a refused gcloud session (exit
# 2) as a setup problem of the check.
test_a_job_list_that_did_not_answer_is_the_switch_rows_own_failure() {
  local dir=$work/switchjobs
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=jobs-list-fails
  expect_status "$status" 1 "a job list that did not answer: the verdict"
  expect_contains "$out" "FACT switch: FAIL — gcloud run jobs list exited non-zero" \
    "a job list that did not answer: the row carries the gcloud failure itself"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "a job list that did not answer: no run was launched"
}

# A get-iam-policy that did not answer is not a policy without the grant: the
# half of the switch it carries is unmeasured, the runs wait, and the row names
# the permission the owner needs to read it themselves.
test_an_iam_read_that_failed_leaves_the_switch_unmeasured() {
  local dir=$work/switchiam
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=svciam-refused
  expect_status "$status" 2 "a refused IAM read: the check could not measure"
  expect_contains "$out" "FACT switch: FAIL — 'gcloud run services get-iam-policy fugaro-credentials" \
    "a refused IAM read: the row carries the reason itself"
  expect_contains "$out" "run.services.getIamPolicy" "a refused IAM read: the row names the permission"
  expect_absent "$out" "FACT switch: PASS" "a refused IAM read: the leg is never a pass"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "a refused IAM read: no run was launched"
}

# The same read one step further in: `get-iam-policy` answered, and the document
# carries no policy at all. That is not a policy without the grant either, and a
# repository called off the switch on it would stop two runs for nothing.
test_an_iam_answer_that_is_no_policy_leaves_the_switch_unmeasured() {
  local dir=$work/switchiamnull
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=svciam-null
  expect_status "$status" 2 "no policy at all: the check could not measure"
  expect_contains "$out" "FACT switch: FAIL" "no policy at all: the row is a FAIL and never a pass"
  expect_contains "$out" "no policy at all" "no policy at all: the row says what it read"
  expect_absent "$out" "FACT switch: PASS" "no policy at all: the leg is never a pass"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no policy at all: no run was launched"
}

# `fugaro project policy show --json` failing is not a policy that carries a
# mode the gateway exists in: the read leaves an empty document, the reader
# answers `mode=None` for it, and a row that read that as a published mode would
# PASS D2071's third leg on a mode nobody read — while the two throwaway runs
# below it would be launched on a switch nobody settled.
test_a_policy_that_could_not_be_read_leaves_the_switch_unmeasured() {
  local dir=$work/policyfail
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=policy-fails
  expect_status "$status" 2 "a failed policy read: the check could not measure"
  expect_contains "$out" "FACT switch: FAIL — both repositories are on the switch" \
    "a failed policy read: the row carries the reason itself"
  expect_contains "$out" "fugaro project policy show --project fugaro --json' exited 1" \
    "a failed policy read: the row names the command that failed"
  expect_contains "$out" "the published policy could not be read" \
    "a failed policy read: the row carries what fugaro printed"
  expect_absent "$out" "FACT switch: PASS" "a failed policy read: the leg is never a pass"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "a failed policy read: no run was launched"
}

# The same leg one step further in: the command answered, and the document it
# printed carries no budget block at all. `None` is not a mode the gateway
# exists in, so the leg is unmeasured and never a PASS on a value nobody
# published.
test_a_policy_with_no_budget_mode_leaves_the_switch_unmeasured() {
  local dir=$work/policynomode
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=policy-no-mode
  expect_status "$status" 2 "no budget mode: the check could not measure"
  expect_contains "$out" "FACT switch: FAIL" "no budget mode: the row is a FAIL and never a pass"
  expect_contains "$out" "carries no budget mode" "no budget mode: the row says why"
  expect_absent "$out" "the published budget mode is 'None'" "no budget mode: None never reaches a PASS"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no budget mode: no run was launched"
}

# The record is the boundary every row below is read against (D2070 (a)): a
# pair of repositories whose records are not one api-key and one oauth is a pair
# this check cannot read its rows against, and a repository recorded `cascade`
# is served the whole cascade and is not what this check is about.
test_a_pair_that_is_not_one_api_key_and_one_oauth_stops() {
  local dir=$work/pair
  prepare "$dir"
  run193 "$dir" FAKE_CURL_MODE=record-both-api-key
  expect_status "$status" 1 "the pair: the verdict"
  expect_contains "$out" "FACT record: FAIL" "the pair: the row"
  expect_contains "$out" "not one api-key and one oauth" "the pair: the row says why"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "the pair: no run was launched"

  local dir2=$work/cascade
  prepare "$dir2"
  run193 "$dir2" FAKE_CURL_MODE=record-cascade
  expect_status "$status" 1 "cascade: the verdict"
  expect_contains "$out" "FACT record: FAIL" "cascade: the row"
  expect_contains "$out" "is served the whole cascade" "cascade: the row says why"
  expect_absent "$(cat "$dir2/logs/fugaro.calls")" "run --repo" "cascade: no run was launched"
}

# A record that is not there is the state of an installation whose `fugaro
# update --repo` has not run since the release that writes one: no row below can
# be measured, and the check says so rather than reading an absent record as an
# answer. The REASON is the row's own text and not a line above the block: the
# body curl wrote is the database's answer (`null` for a node that is not there),
# so a row built from it would read `FAIL — null` and the diagnosis would land
# outside the one block the owner pastes.
test_a_record_that_is_not_there_is_unmeasured() {
  local dir=$work/norecord
  prepare "$dir"
  run193 "$dir" FAKE_CURL_MODE=record-missing
  expect_status "$status" 2 "no record: the check could not measure"
  expect_contains "$out" "FACT record: FAIL — the record is not there (HTTP 404)" "no record: the row carries the reason itself"
  expect_contains "$out" "update --repo" "no record: the row names the fix"
  expect_absent "$err" "HTTP 404" "no record: the diagnosis is inside the block, not above it"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no record: no run was launched"
}

# The other arm of the same read: an answer that is neither 200 nor 404 (here a
# 500) carries no diagnosis at all, so the row has to name the status and the
# path it asked for — the check has nothing else to go on.
test_a_record_that_could_not_be_read_names_the_status_and_the_path() {
  local dir=$work/badrecord
  prepare "$dir"
  run193 "$dir" FAKE_CURL_MODE=record-unreadable
  expect_status "$status" 2 "unreadable record: the check could not measure"
  expect_contains "$out" "FACT record: FAIL — the record could not be read (HTTP 500)" "unreadable record: the row carries the reason itself"
  expect_contains "$out" "creds/repoauth/" "unreadable record: the row names the path it asked for"
  expect_absent "$err" "the record could not be read" "unreadable record: the diagnosis is inside the block"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "unreadable record: no run was launched"
}

# THE HAPPY PATH, and the one the check exists for: both repositories on the
# switch, both records written, an api-key run served the paid key at tier B and
# an oauth run served an account's token at tier A, the oauth repository
# published with proxy: true and its run's git traffic forwarded by the service.
test_the_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run193 "$dir"
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT switch: PASS" "happy path: the switch"
  expect_contains "$out" "FACT record: PASS" "happy path: the records"
  expect_contains "$out" "FACT served-api-key: PASS" "happy path: the paid key served the api-key run"
  expect_contains "$out" "anthropic-api-key|B" "happy path: the credential and its tier"
  expect_contains "$out" "FACT served-oauth: PASS" "happy path: an account served the oauth run"
  expect_contains "$out" "default|A" "happy path: the account's own name and its tier"
  expect_contains "$out" "FACT declared-account: PASS" "happy path: the account it declared"
  expect_contains "$out" "FACT proxy: PASS" "happy path: the proxied repository"
  expect_contains "$out" "bitbucket-token" "happy path: the git line's credential"
  expect_contains "$out" "CHECK 193 RESULT END (exit 0)" "happy path: the end line"

  local calls launches
  calls=$(cat "$dir/logs/fugaro.calls")
  launches=$(printf '%s\n' "$calls" | grep -c '^fugaro run --repo' || true)
  expect_status "$launches" 2 "happy path: two throwaway runs and no more"
  expect_contains "$calls" "--repo acme/sandbox-api" "happy path: the api-key repository"
  expect_contains "$calls" "--repo acme/sandbox-oauth" "happy path: the oauth repository"
  expect_contains "$calls" "--project fugaro" "happy path: the installation"
  expect_absent "$calls" "--total-timeout" "happy path: no total timeout is ever passed"
  # The record is READ and never written: the check holds no write of its own to
  # the installation's database at all. And it is read at the key `fugaro update
  # --repo` wrote it under — the repository's own SLUG, which is the
  # `fugaro_repo` label of its jobs (task.Slug: <readable>-<16 hex>) — never at
  # a path spelled out of the repository's name, which is a node nobody writes.
  local curls
  curls=$(cat "$dir/logs/curl.calls")
  expect_contains "$curls" "creds/repoauth/acme-sandbox-api-0123456789abcdef.json" "happy path: the api-key repository's record, at its slug"
  expect_contains "$curls" "creds/repoauth/acme-sandbox-oauth-0123456789abcdef.json" "happy path: the oauth repository's record, at its slug"
  expect_absent "$curls" "creds/repoauth/acme-sandbox-api.json" "happy path: the record is never read at a name-shaped path"
  expect_absent "$curls" "creds/repoauth/acme-sandbox-oauth.json" "happy path: the record is never read at a name-shaped path"
  expect_absent "$curls" "-X " "happy path: nothing was written to the database"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

# CHECK193_KEEP_SCRATCH is the knob the steps file tells the owner to set to read
# what the check wrote before it goes. A directory kept because it was asked for
# is not one the check failed to remove — that is what the row's FAIL is for —
# so the row reads PASS and the verdict stays the reading's own: a run whose
# every other row passes still ends `(exit 0)`.
test_the_happy_path_with_the_scratch_directory_kept() {
  local dir=$work/keep
  prepare "$dir"
  run193 "$dir" CHECK193_KEEP_SCRATCH=1
  expect_status "$status" 0 "kept scratch: the verdict is the reading's own"
  expect_contains "$out" "FACT scratch-removed: PASS" "kept scratch: the row is not a FAIL"
  expect_contains "$out" "kept on request" "kept scratch: the row says the owner asked for it"
  expect_contains "$out" "CHECK 193 RESULT END (exit 0)" "kept scratch: the end line"
  [ -n "$scratch" ] && [ -d "$scratch" ] || fail "kept scratch: the directory was removed anyway"
}

# A run whose model calls were answered by something other than the service is
# exactly what this check is for: a switched repository's runs are served the
# credential its own RECORD names, and a run served another one is the boundary
# not holding.
test_a_run_served_the_wrong_credential_is_a_failure() {
  local dir=$work/wrongcred
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=wrong-credential
  expect_status "$status" 1 "wrong credential: the verdict"
  expect_contains "$out" "FACT served-api-key: FAIL" "wrong credential: the row"
  expect_contains "$out" "no line of this run names the paid key" "wrong credential: the row says why"
  expect_contains "$out" "FACT served-oauth: PASS" "wrong credential: the other repository is unaffected"
}

# A `gcloud logging read` that did not answer is not the service having written
# no line: the file the read writes is empty either way, and a row that took the
# empty file for the service's silence would FAIL a run whose lines it never
# read. The three rows that read those lines — the two the run is served from
# and the account it declared — are rows this check could not measure (exit 2),
# and never a report about the credentials service.
test_a_log_read_that_failed_leaves_the_served_rows_unmeasured() {
  local dir=$work/logfail
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=log-fails
  expect_status "$status" 2 "a failed log read: the check could not measure"
  expect_contains "$out" "FACT audited-api-key: FAIL — the service's own lines for the run" \
    "a failed log read: the audited row carries the reason itself"
  expect_contains "$out" "gcloud logging read' exited 1" "a failed log read: the row names the read that failed"
  expect_contains "$out" "the caller does not have permission to read the log" \
    "a failed log read: the row carries what gcloud printed"
  expect_contains "$out" "FACT served-api-key: FAIL — the service's own lines for the run" \
    "a failed log read: the row it would have decided is unmeasured too"
  expect_contains "$out" "FACT declared-account: FAIL — the service's own lines for the run" \
    "a failed log read: the account row is unmeasured and not the boundary reported as broken"
  expect_absent "$out" "no served line of its own names that account" \
    "a failed log read: nothing is read as the boundary not holding"
  expect_absent "$out" "FACT inflight-mount" "a failed log read: the rows of the other mode are not in this block"
}

# The oauth half of the boundary: the service narrows a call to the account that
# call declared, so a line naming a SECOND account of the repository's own
# record is a run served an account it did not declare.
test_a_second_account_served_is_a_failure() {
  local dir=$work/second
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=second-account
  expect_status "$status" 1 "second account: the verdict"
  expect_contains "$out" "FACT declared-account: FAIL" "second account: the row"
  expect_contains "$out" "served it ANOTHER account as well" "second account: the row says why"
  # The row names the lines it read, the way the service names a tier-A
  # credential — the ACCOUNT itself, `default` and `other` — and never the
  # secret container it is stored in (`claude-oauth-token-other`), which no
  # audit line ever carries.
  expect_contains "$out" "the accounts its lines name are default other" "second account: the row names them by their accounts"
  expect_absent "$out" "claude-oauth-token" "second account: no row speaks of a secret container"
}

# The declared-account row is measurable only where the record names more than
# one account: a record naming one has nothing the run could have been served
# besides it, and a row read there would be a row about nothing. This is a state
# the check cannot measure, not a criterion that failed — so the row reads FAIL
# and the verdict is the one its own contract names for it.
test_a_record_with_one_account_leaves_the_row_unmeasured() {
  local dir=$work/oneacct
  prepare "$dir"
  run193 "$dir" FAKE_CURL_MODE=record-one-account
  expect_status "$status" 2 "one account: the check could not measure"
  expect_contains "$out" "FACT declared-account: FAIL" "one account: the row is a FAIL and never a pass"
  expect_contains "$out" "names 1 account(s)" "one account: the row reads the record"
  expect_contains "$out" "a row about nothing" "one account: the row says why it measures nothing"
  expect_contains "$out" "FACT served-oauth: PASS" "one account: everything above it still holds"
}

# The declared-account row is read off the oauth run's OWN lines, so a run that
# never launched leaves it nothing to read: the file step_run writes them to is
# not there at all. A row built from a file nobody wrote reports the boundary as
# broken — a FAIL the verdict reads as a criterion that did not hold, exit 1 —
# on the strength of nothing, when what happened is that this check could not
# measure it (exit 2). Its sibling step_proxy guards the row it reads the same
# way, and the launch's own row is where the reason is.
test_a_launch_that_failed_leaves_the_declared_account_unmeasured() {
  local dir=$work/nolaunch
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=launch-fails
  expect_status "$status" 2 "no launch: the check could not measure"
  expect_contains "$out" "FACT declared-account: FAIL" "no launch: the row is a FAIL and never a pass"
  expect_contains "$out" "was never launched" "no launch: the row says what happened"
  expect_absent "$out" "no served line of its own names that account at tier A" "no launch: the row is not read off a file nobody wrote"
  expect_contains "$out" "FACT served-oauth: FAIL" "no launch: the launch's own row is the one that carries the reason"
  expect_contains "$out" "never launched one" "no launch: the reason is the launch's own"
  expect_contains "$out" "CHECK 193 RESULT END (exit 2)" "no launch: the end line"
}

# The record's key is the repository's own task slug, and the slug is read off
# the `fugaro_repo` label the engine that writes the record also writes on the
# jobs. A job list whose jobs carry no such label leaves the key unknown — so
# the row says it could not be measured, and it does not fall back to a path
# spelled out of the repository's name, which is a node nobody writes.
test_a_job_without_the_repo_label_leaves_the_record_unmeasured() {
  local dir=$work/nolabel
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=no-repo-label
  expect_status "$status" 2 "no repo label: the check could not measure"
  expect_contains "$out" "FACT record: FAIL" "no repo label: the row"
  expect_contains "$out" "carries its 'fugaro_repo' label" "no repo label: the row says why"
  expect_contains "$out" "update --repo" "no repo label: the row names the fix"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "no repo label: no run was launched"
  if [ -s "$dir/logs/curl.calls" ]; then
    fail "no repo label: the record was read at a key the check made up: $(head -n 1 "$dir/logs/curl.calls")"
  fi
}

# A NAMED account's line is read as an account and never as a container, and the
# expectation it is read against comes from the LAUNCH and never from the run's
# own row: `oauth_account` is the credential the service's answers carried — the
# account the run used last — so a row that took the declaration from there would
# compare the answers with themselves. The record here names `default` and
# `work`, the run's own row says `work` (what the service answered with) and its
# line names `work` alone; the check's launch named no account, so it declared
# `default`, and being served `work` is exactly the boundary not holding — the
# state the row's own FAIL text calls "a run served an account it did not
# declare: this is the row Check 193 exists for".
test_a_served_account_the_run_never_declared_is_a_failure() {
  local dir=$work/undeclared
  prepare "$dir"
  run193 "$dir" FAKE_OAUTH_ACCOUNT=work FAKE_CURL_MODE=record-named-account
  expect_status "$status" 1 "undeclared account: the verdict"
  expect_contains "$out" "FACT served-oauth: PASS" "undeclared account: the service did serve the run, out of an account of its record"
  expect_contains "$out" "work|A" "undeclared account: the account's own name and its tier"
  expect_contains "$out" "FACT declared-account: FAIL" "undeclared account: the row"
  expect_contains "$out" "served it ANOTHER account as well" "undeclared account: the row says why"
  expect_contains "$out" "the accounts its lines name are work" "undeclared account: the row names the account it was served"
  expect_contains "$out" "declared default" "undeclared account: the account comes from the launch, not from the row"
  expect_absent "$out" "claude-oauth-token" "undeclared account: no row speaks of a secret container"
}

# A tier-A line naming an account the repository's own record does NOT name is a
# run served a credential the service had no answer for: the record is the
# boundary every call of that repository is narrowed to (narrowToRepoAuth), so a
# line naming an account outside it is the boundary not holding.
test_a_line_naming_an_account_the_record_does_not_name_is_a_failure() {
  local dir=$work/notrecorded
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=oauth-not-recorded
  expect_status "$status" 1 "outside the record: the verdict"
  expect_contains "$out" "FACT served-oauth: FAIL" "outside the record: the row"
  expect_contains "$out" "no line of this run names an account its own record names" "outside the record: the row says why"
  expect_contains "$out" "someone-else" "outside the record: the row names the credential it was served"
}

# A repository published with proxy: true whose runs never reached the service
# is F29 exactly: the proxy switch is on and the repository is on the switch,
# and its push still went straight to the provider with the credential its job
# mounts.
test_a_proxied_repository_whose_run_did_not_reach_the_service_is_a_failure() {
  local dir=$work/nogit
  prepare "$dir"
  run193 "$dir" FAKE_GCLOUD_MODE=no-git
  expect_status "$status" 1 "no git line: the verdict"
  expect_contains "$out" "FACT proxy: FAIL" "no git line: the row"
  expect_contains "$out" "no git line for its run" "no git line: the row says why"
}

# A repository the published policy does not admit with proxy: true measures
# nothing about the proxy — its runs push straight to the provider by design —
# so the row says so instead of reading the direct path as the proxy.
test_a_policy_without_the_proxy_leaves_the_row_unmeasured() {
  local dir=$work/noproxy
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=no-proxy
  expect_status "$status" 2 "no proxy: the check could not measure"
  expect_contains "$out" "FACT proxy: FAIL" "no proxy: the row"
  expect_contains "$out" "with proxy: true" "no proxy: the row says why"
  expect_contains "$out" "publish proxy: true" "no proxy: the row names the fix"
}

# --after-flip's first half, and the one the plan names in as many words: no job
# of either repository mounts its model credential any more. A job that still
# carries one is the flip not having reached it.
test_after_flip_refuses_a_job_that_still_mounts_the_model_credential() {
  local dir=$work/flipmount
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=mounted \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 1 "after the flip: the verdict"
  expect_contains "$out" "MODE: flip" "after the flip: the block's mode"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "after the flip: the mount row"
  # The container the job is fed from, named as the deployment writes it: the
  # derived ID, `fugaro-<readable slug>-<logical>-<16 hex>`, and never the
  # logical name — an assertion on the logical spelling would pass against a
  # check that cannot see a real mount at all.
  expect_contains "$out" "fugaro-acme-sandbox-oauth-anthropic-api-key-71102c59f2b0e1e9" "after the flip: the row names the container the job is fed from"
  expect_contains "$out" "FACT inflight-mount: PASS" "after the flip: the untaken rows are still read"
  expect_absent "$out" "FACT declared-account" "after the flip: a row of the other mode is not in this block"
}

# The other container a real job is fed from, and the one the oauth sandbox
# repository's own job holds before the flip: a NAMED Claude account's token,
# whose derived ID carries the account after the logical name
# (`-claude-oauth-token-<account>-<hash>`). A reading that knew only the bare
# logical name would call this job unmounted.
test_after_flip_refuses_a_job_that_still_mounts_an_accounts_token() {
  local dir=$work/flipacct
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=mounted-account \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 1 "after the flip, an account's token: the verdict"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "after the flip, an account's token: the mount row"
  expect_contains "$out" "fugaro-acme-sandbox-oauth-claude-oauth-token-work-b83d639928880e02" "after the flip, an account's token: the row names the container the job is fed from"
}

# The same run on a repository whose jobs mount nothing: every flip row holds,
# and the runs still finish with no mount on their jobs.
test_after_flip_passes_when_no_job_mounts_a_model_credential() {
  local dir=$work/flipok
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=provider-key \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 0 "after the flip: the verdict"
  expect_contains "$out" "FACT no-model-mounts: PASS" "after the flip: the mount row"
  expect_contains "$out" "FACT no-model-mounts-detail: INFO" "after the flip: the provider keys left behind"
  expect_contains "$out" "2 provider-key variable(s)" "after the flip: R6's keys are counted and not a finding"
  expect_contains "$out" "FACT served-api-key: PASS" "after the flip: a run still succeeds"
  expect_contains "$out" "FACT served-oauth: PASS" "after the flip: and the other repository too"
  expect_contains "$out" "FACT inflight-run: PASS" "after the flip: the run that was in flight"
  expect_contains "$out" "FACT inflight-mount: PASS" "after the flip: it kept its own mount"
}

# The service's own lines for a run in flight across the re-apply are NOT the
# reading: the flip takes the MOUNTS (D2072) and a switched run reaches the
# service on the FUGARO_CREDENTIALS_SERVICE_URL the LAUNCHER writes on its
# execution (internal/cli/run.go's credentialsServiceLaunchEnv; no
# per-repository job template ever carries it), so a run that behaved correctly
# before the flip has lines of its own. The mount is the reading, and it is on
# the execution's own spec.
test_an_inflight_run_the_service_answered_still_keeps_its_mount() {
  local dir=$work/inflightserved
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=inflight-served \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 0 "in flight, served: the verdict"
  expect_contains "$out" "FACT inflight-mount: PASS" "in flight, served: the row"
  expect_contains "$out" "its own container spec still mounts the model credential" \
    "in flight, served: the row is read off the execution's own spec"
  expect_contains "$out" "not read here" "in flight, served: the service's lines are not this row's reading"
}

# An execution created before the re-apply keeps the container spec it was
# created with (U9), so a run in flight across a mount-dropping re-apply has a
# mount on its execution and no mount on the template. An execution with none is
# a run launched by the flipped template — a run the re-apply was not in flight
# across — and reading it as a retained mount would pass the row on nothing.
test_an_inflight_run_whose_execution_mounts_nothing_is_a_failure() {
  local dir=$work/inflightnomount
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=execution-no-mount \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 1 "in flight, no mount on the execution: the verdict"
  expect_contains "$out" "FACT inflight-mount: FAIL" "in flight, no mount on the execution: the row"
  expect_contains "$out" "has no mount to keep" "in flight, no mount on the execution: the row says why"
  expect_contains "$out" "the release that drops the mount" "in flight, no mount on the execution: the row names the fix"
}

# U9's reading is the container spec the execution was CREATED with, so a
# `gcloud run jobs executions describe` that did not answer must never be read as
# an execution with no mount: the row says unmeasured instead.
test_an_execution_that_could_not_be_read_leaves_the_u9_row_unmeasured() {
  local dir=$work/inflightexecread
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=executions-describe-fails \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api
  expect_status "$status" 2 "U9, a failed execution read: the check could not measure"
  expect_contains "$out" "FACT inflight-run: PASS" "U9, a failed execution read: the rows above it still hold"
  expect_contains "$out" "gcloud run jobs executions describe" "U9, a failed execution read: the row names the read that failed"
  expect_contains "$out" "is not an execution with no mount" "U9, a failed execution read: the row says what it could not show"
  expect_absent "$out" "FACT inflight-mount: PASS" "U9, a failed execution read: the failed read is never read as U9"
}

# A run whose own record names no execution — one from before the launcher
# recorded one — has no container spec to read at all: the row says so rather
# than reading the service's lines, or the absence of a mount, in its place.
test_a_row_that_names_no_execution_leaves_the_u9_row_unmeasured() {
  local dir=$work/inflightnoexec
  prepare "$dir"
  run193 "$dir" --after-flip \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-api \
    FAKE_ROW_NO_EXECUTION=1
  expect_status "$status" 2 "U9, no execution named: the check could not measure"
  expect_contains "$out" "FACT inflight-run: PASS" "U9, no execution named: the rows above it still hold"
  expect_contains "$out" "FACT inflight-mount: FAIL — the row of the run" "U9, no execution named: the row carries the reason itself"
  expect_contains "$out" "names no execution" "U9, no execution named: the row says what it could not read"
  expect_absent "$out" "FACT inflight-mount: PASS" "U9, no execution named: nothing is read in its place"
}

# With the repository's own jobs still mounting the model credential, the flip
# has not reached it: a mount on the execution is then not the RETENTION U9 is
# about — the template hands the same mount out to every run it launches — so
# the row says unmeasured and points at the row that reads the template.
test_an_inflight_run_whose_repository_still_mounts_is_unmeasured() {
  local dir=$work/inflighttemplatemounts
  prepare "$dir"
  run193 "$dir" --after-flip \
    FAKE_GCLOUD_MODE=mounted \
    CHECK193_INFLIGHT=20261116-070000-aaaa \
    FAKE_INFLIGHT=20261116-070000-aaaa FAKE_INFLIGHT_REPO=acme/sandbox-oauth
  expect_status "$status" 1 "in flight, the template still mounts: the verdict"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "in flight, the template still mounts: the row that reads it"
  expect_contains "$out" "FACT inflight-mount: FAIL — the execution" "in flight, the template still mounts: the row carries the reason itself"
  expect_contains "$out" "not the RETENTION U9 is about" "in flight, the template still mounts: the row says why"
  expect_absent "$out" "FACT inflight-mount: PASS" "in flight, the template still mounts: the template's own mount is never read as a retained one"
}

# A run that started AFTER the re-apply is not the U9 case at all — it was
# launched by the template the re-apply wrote — so its mount must not be read as
# the one a run in flight kept.
test_a_run_that_started_after_the_reapply_is_not_the_u9_case() {
  local dir=$work/inflightlate
  prepare "$dir"
  run193 "$dir" --after-flip \
    CHECK193_INFLIGHT=20261116-120000-bbbb \
    FAKE_INFLIGHT=20261116-120000-bbbb FAKE_INFLIGHT_REPO=acme/sandbox-api \
    FAKE_ROW_STARTED=2026-11-16T12:00:00Z
  expect_status "$status" 1 "started late: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "started late: the row"
  expect_contains "$out" "not before" "started late: the row says why"
}

# A run that did not finish is not an answer about the boundary: nothing was
# served to it that ended in a push and a pull request.
test_a_run_that_did_not_finish_is_a_failure() {
  local dir=$work/runfailed
  prepare "$dir"
  run193 "$dir" FAKE_ROW_STATUS=failed
  expect_status "$status" 1 "a failed run: the verdict"
  expect_contains "$out" "FACT run-api-key: FAIL" "a failed run: the row"
  expect_contains "$out" "no pull request was opened" "a failed run: the row says why"
  expect_contains "$out" "FACT run-oauth: FAIL" "a failed run: and the other repository's run too"
}

# A development build is not the release this check is about: it resolves no
# released credentials-service image to deploy, and the whole reading is about
# what that image does.
test_a_development_build_is_a_failure() {
  local dir=$work/devbuild
  prepare "$dir"
  run193 "$dir" FAKE_FUGARO_MODE=dev
  expect_status "$status" 1 "a dev build: the verdict"
  expect_contains "$out" "FACT fugaro-version: FAIL" "a dev build: the row"
  expect_contains "$out" "development build" "a dev build: the row says why"
}

# The check is about ONE installation, and the project and the region are
# constants in it: no gcloud command may name another project, and no command at
# all may name the owner's own workspace.
test_never_belong() {
  local dir=$work/never
  prepare "$dir"
  run193 "$dir"
  expect_status "$status" 0 "never belong: the run went on"
  local projects p all
  all=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls" "$dir/logs/curl.calls")
  expect_absent "$all" "belong" "never belong: no command named it"
  projects=$(grep -o -- '--project=[^ ]*' "$dir/logs/gcloud.calls" | sort -u || true)
  [ -n "$projects" ] || fail "never belong: no gcloud --project argument was recorded, so this test checks nothing"
  for p in $projects; do
    [ "$p" = "--project=fugaro-dev" ] || fail "never belong: a gcloud command named $p"
  done
}

# The check's own tools are looked for before anything is done, and a machine
# missing one is told which: nothing is called and nothing is created.
test_a_missing_tool_stops_before_anything_is_called() {
  local dir=$work/notools
  prepare "$dir"
  mkdir -p "$dir/empty"
  local tool
  for tool in bash mktemp python3 dirname cat rm mkdir date tr sed grep; do
    ln -s "$(command -v "$tool")" "$dir/empty/$tool" 2>/dev/null || true
  done
  status=0
  env PATH="$dir/empty" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    CHECK193_REPOS="$REPOS_DEFAULT" \
    "$dir/empty/bash" "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "no gcloud on PATH: the verdict"
  expect_contains "$err" "needs 'gcloud' on PATH" "no gcloud on PATH: the stop"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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
t test_the_project_guard_reads_the_line_not_doctors_status
t test_project_guard
t test_xtrace_is_refused
t test_the_repos_guard
t test_a_repository_off_the_switch_is_a_failure
t test_a_job_that_still_mounts_the_model_credential_is_off_the_switch
t test_a_job_account_without_the_invoker_grant_is_off_the_switch
t test_an_invoker_grant_to_another_account_is_not_this_jobs_grant
t test_a_job_whose_template_carries_the_url_is_on_the_switch
t test_a_stage_accounts_own_token_is_not_a_reason_to_call_a_job_off_the_switch
t test_an_oauth_jobs_token_is_the_mount_the_flip_takes_away
t test_a_job_list_that_did_not_answer_is_the_switch_rows_own_failure
t test_an_iam_read_that_failed_leaves_the_switch_unmeasured
t test_an_iam_answer_that_is_no_policy_leaves_the_switch_unmeasured
t test_a_policy_that_could_not_be_read_leaves_the_switch_unmeasured
t test_a_policy_with_no_budget_mode_leaves_the_switch_unmeasured
t test_a_pair_that_is_not_one_api_key_and_one_oauth_stops
t test_a_record_that_is_not_there_is_unmeasured
t test_a_record_that_could_not_be_read_names_the_status_and_the_path
t test_a_job_without_the_repo_label_leaves_the_record_unmeasured
t test_the_happy_path
t test_the_happy_path_with_the_scratch_directory_kept
t test_a_run_served_the_wrong_credential_is_a_failure
t test_a_log_read_that_failed_leaves_the_served_rows_unmeasured
t test_a_second_account_served_is_a_failure
t test_a_served_account_the_run_never_declared_is_a_failure
t test_a_line_naming_an_account_the_record_does_not_name_is_a_failure
t test_a_record_with_one_account_leaves_the_row_unmeasured
t test_a_launch_that_failed_leaves_the_declared_account_unmeasured
t test_a_proxied_repository_whose_run_did_not_reach_the_service_is_a_failure
t test_a_policy_without_the_proxy_leaves_the_row_unmeasured
t test_after_flip_refuses_a_job_that_still_mounts_the_model_credential
t test_after_flip_refuses_a_job_that_still_mounts_an_accounts_token
t test_after_flip_passes_when_no_job_mounts_a_model_credential
t test_an_inflight_run_the_service_answered_still_keeps_its_mount
t test_an_inflight_run_whose_execution_mounts_nothing_is_a_failure
t test_an_execution_that_could_not_be_read_leaves_the_u9_row_unmeasured
t test_a_row_that_names_no_execution_leaves_the_u9_row_unmeasured
t test_an_inflight_run_whose_repository_still_mounts_is_unmeasured
t test_a_run_that_started_after_the_reapply_is_not_the_u9_case
t test_a_run_that_did_not_finish_is_a_failure
t test_a_development_build_is_a_failure
t test_never_belong
t test_a_missing_tool_stops_before_anything_is_called

if [ "$failures" -ne 0 ]; then
  echo "check193_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check193_test.sh: ok"
