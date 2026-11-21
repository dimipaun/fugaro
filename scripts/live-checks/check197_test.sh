#!/usr/bin/env bash
# Tests for scripts/live-checks/check197.sh, with fake `fugaro` and `gcloud` on
# PATH: NOTHING here reaches a cloud, a project, a repository or the network, no
# agent runs a live command, and the fake tools record every argument they are
# given so the test can assert that no command ever named anything but the
# sandbox installation's project and that nothing was written anywhere (Check
# 197 creates no cloud resource, writes no record and touches no repository).
#
# The fakes are a small project of their own: one published policy that defines a
# single GitHub profile admitting one sandbox repository, the installation's jobs
# — one SHARED job of that profile (`fugaro-p-sandbox-github…`) and the
# per-repository job a run lands on when its repository selects no profile —
# throwaway runs that are launched, listed and then answered by the service's own
# `credsvc: audit` lines, and a Cloud Logging read whose filter carries the run
# id. Every row of the check is read off those, and the cases below turn each one
# the way a live installation would have to for the row to fail, to be
# unmeasured or to be not applicable.
#
# Usage: bash scripts/live-checks/check197_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check197.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK197_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c ${CHECK197_TEST_TAIL:-700})" ;;
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

# The two sandbox repositories every case runs against: one admitted to the
# profile this check reads (so a run of it lands on the SHARED job) and one the
# project admits while the profile does not (so the launcher refuses it — the
# outside-profile row).
REPOS_DEFAULT="acme/app,acme/other"
REPO_ONE="acme/app"
REPO_OUTSIDE="acme/other"
BB_REPO="acme/bb"
# The same two plus the Bitbucket one: the cases that take the Bitbucket half
# all the way hand the fake `fugaro` a launchable Bitbucket sandbox repository.
BB_REPOS_ALL="$REPOS_DEFAULT,$BB_REPO"
# The shared job of the profile, spelled as gcp.ProjectJobName spells it:
# `fugaro-p-<profile>-<provider>` plus a 16-hex hash, with no account segment
# for the profile's default account.
SHARED_JOB="fugaro-p-sandbox-github-0123456789abcdef"
BB_JOB="fugaro-p-sandbox-bb-bitbucket-0123456789abcdef"
# The start of a run that was in flight ACROSS the re-apply: an in-flight run is
# one that started BEFORE the moment the job was last rewritten, which the fakes
# answer as 2026-11-16T09:00:00Z.
INFLIGHT_STARTED="2026-11-16T08:30:00Z"

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
"""A stand-in for `fugaro`, for scripts/live-checks/check197.sh's test only.

It answers the four things the check asks it: which installation this is
(`doctor`), its own version, a launch (`run`, which only RECORDS the run — no
task, no repository and no cloud is touched) and the listing (`runs ls`) whose
rows the check polls and reads, plus the published policy (`project policy
show`), which is where the profile that admits the repository is read from.
"""
import json, os, sys

LOG = os.environ["FAKE_LOG_DIR"]
STATE = os.environ["FAKE_GCLOUD_STATE"]
mode = os.environ.get("FAKE_FUGARO_MODE", "ok")
repos = [r for r in os.environ.get("FAKE_REPOS", "").split(",") if r]
outside = repos[1] if len(repos) > 1 else ""
profile = os.environ.get("FAKE_PROFILE", "sandbox")
shared_job = os.environ.get("FAKE_SHARED_JOB", "fugaro-p-sandbox-github-0123456789abcdef")
perrepo_job = os.environ.get("FAKE_PERREPO_JOB", "acme-app-0123456789abcdef")
# The rest of the profile's own allow_repos list, beyond the repository this
# check runs in: the real refusal prints the WHOLE list, joined with ", "
# (projectpolicy.AdmissionRefusal), on the launcher's ONE stderr line, and a
# sandbox profile admits several repositories. The length is the point: with
# this list the stop sentence sits past the DISPLAY_CUT characters the check
# shows of a refusal, so the row tells a reading of the line's head from a
# reading of the whole line (see the launch branch below).
DISPLAY_CUT = 400
PROFILE_ALLOW_MORE = ["github/acme/fugaro-sandbox-daemon",
                      "github/acme/fugaro-sandbox-images",
                      "github/acme/fugaro-sandbox-mobile",
                      "github/acme/fugaro-sandbox-website"]


def profile_allow(widened=False):
    """The profile's own allow_repos list, in the ONE spelling both halves of
    this fake print: the policy document (`project policy show`) and the
    refusal a launch outside the profile gets. They are the same list on a real
    installation — profileAdmissionRefusal reads it off the policy it publishes
    — so a fake whose refusal named another list would test a refusal no
    installation makes."""
    allow = ["github/%s" % (repos[0] if repos else "")] + PROFILE_ALLOW_MORE
    if widened:
        allow = allow + ["github/%s" % outside]
    return allow

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
    """The runs launched so far, one `repo rid job` line each: the JOB is
    recorded at launch and not guessed at read time, because it is what the
    check reads out of the run's execution path."""
    path = os.path.join(STATE, "fugaro.runs")
    if not os.path.exists(path):
        return []
    return [l.split(" ") for l in open(path).read().splitlines() if l.strip()]


def record_run(repo, rid, job):
    with open(os.path.join(STATE, "fugaro.runs"), "a") as fh:
        fh.write("%s %s %s\n" % (repo, rid, job))


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
        sys.stderr.write("fugaro: the repository %s is not one of the sandbox repositories this test names\n" % repo)
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
    if repo == outside:
        # The launcher's own profile gate (internal/cli/run_allowlist.go,
        # profileAdmissionRefusal): a repository the profile does not admit is
        # refused before any execution exists, with the sentence the check reads.
        if mode == "outside-other-refusal":
            sys.stderr.write("fugaro: the project's policy does not admit %s: allow_repos is github/acme/*. The project's owner adds the repository with 'fugaro project repos add %s'\n" % (repo, repo))
            sys.exit(1)
        if mode == "outside-launched":
            record_run(repo, rid, shared_job)
            print(json.dumps({"run_id": rid, "repo": repo, "status": "queued"}))
            sys.exit(0)
        # The launcher's own line, spelled as internal/cli prints it
        # (profileAdmissionRefusal -> projectpolicy.AdmissionRefusal, then
        # safetext.PrintFailure's "fugaro:" prefix): ONE line carrying the
        # profile's whole allow_repos list, the remedy and, last, the sentence
        # that says no execution exists.
        stop = "" if mode == "outside-no-stop-line" else ". The launch stops before any execution exists"
        line = ('fugaro: the job profile "%s" does not admit github/%s: its allow_repos is %s, '
                "and a profile's own list can only narrow the project's enrollment, never widen it. "
                "The project's owner adds the repository to the profile's list, or removes the "
                "profile's list so the project's applies, and publishes the policy again%s"
                % (profile, repo, ", ".join(profile_allow()), stop))
        # The check reads the refusal from the WHOLE stderr and shows only its
        # first DISPLAY_CUT characters (check197.sh's last_lines), so a fake
        # whose refusal fits inside the cut — or that put the stop sentence on a
        # line of its own — would let a reading of the head pass as a reading of
        # the whole line, which is the defect this fake exists to catch.
        if len(line) <= DISPLAY_CUT:
            sys.stderr.write(
                "fugaro: this fake's refusal is %d characters, no longer than the check's "
                "%d-character display cut: lengthen the profile's allow_repos list, or the test "
                "can no longer tell a reading of the whole line from a reading of its head\n"
                % (len(line), DISPLAY_CUT))
            sys.exit(3)
        sys.stderr.write(line + "\n")
        sys.exit(1)
    job = perrepo_job if mode == "run-on-per-repo-job" else shared_job
    record_run(repo, rid, job)
    print(json.dumps({"run_id": rid, "repo": repo, "status": "queued"}))
elif args[:1] == ["runs"]:
    repo = flag("--repo")
    if mode == "listing-fails":
        sys.stderr.write("fugaro: the listing could not be read (test)\n")
        sys.exit(1)
    rows = []
    inflight = os.environ.get("FAKE_INFLIGHT", "")
    if inflight and repo == os.environ.get("FAKE_INFLIGHT_REPO", repos[0] if repos else ""):
        rows.append((inflight, os.environ.get("FAKE_INFLIGHT_JOB", shared_job)))
    for parts in runs_state():
        if len(parts) < 3 or parts[0] != repo:
            continue
        rows.append((parts[1], parts[2]))
    out = []
    for rid, job in rows:
        row = {"run_id": rid, "repo": repo,
               "status": os.environ.get("FAKE_ROW_STATUS", "succeeded"),
               "terminal": True, "outcome": "ready", "pushed": True, "pr": 7,
               "started_at": os.environ.get("FAKE_ROW_STARTED", "2026-11-16T08:30:00Z")}
        if rid == inflight:
            row["started_at"] = os.environ.get("FAKE_INFLIGHT_STARTED", "2026-11-16T08:30:00Z")
        if not os.environ.get("FAKE_ROW_NO_EXECUTION"):
            row["execution"] = "projects/fugaro-dev/locations/us-east5/jobs/%s/executions/%s" % (job, rid)
        if row["status"] != "succeeded":
            row["reason"] = "the run failed (test)"
            row["pr"] = 0
            row["pushed"] = False
            row["halt"] = {"reason": "agent",
                           "detail": "the agent stopped before opening a pull request (test)"}
        out.append(row)
    print(json.dumps({"runs": out}))
elif args[:1] == ["project"] and args[1:2] == ["policy"]:
    if mode == "policy-fails":
        sys.stderr.write("fugaro: the published policy could not be read (test)\n")
        sys.exit(1)
    project_allow = ["github/%s/*" % (repos[0].split("/")[0] if repos else "acme")]
    if mode == "not-proj-admitted":
        project_allow = ["github/someone-else/*"]
    if mode == "outside-admitted":
        project_allow = ["github/%s/*" % (repos[0].split("/")[0] if repos else "acme")]
    prof = {"provider": "bitbucket" if mode == "other-provider" else "github",
            "cpu": "2", "memory": "4Gi", "timeout_class": "standard",
            "secrets": ["anthropic-api-key"],
            "allow_repos": profile_allow(mode == "profile-widens"),
            "claude_accounts": []}
    profiles = {profile: prof}
    if mode == "no-profiles":
        profiles = {}
    if mode == "two-profiles":
        profiles = {profile: prof, "second": dict(prof)}
    block = {"allow_repos": [], "proxy": False}
    if mode in ("bitbucket-job", "bitbucket-token-mount"):
        # The Bitbucket half's own profile: a profile of that provider whose
        # every repository the published block proxies, which is what D2079's
        # T2.2 deploys a shared job for. It admits only the Bitbucket sandbox
        # repository, so the GitHub half's "exactly one profile admits it" still
        # reads one.
        bb = os.environ.get("FAKE_BB_REPO", "acme/bb")
        block = {"allow_repos": ["bitbucket/%s" % bb], "proxy": True}
        profiles["sandbox-bb"] = {"provider": "bitbucket", "cpu": "2", "memory": "4Gi",
                                  "timeout_class": "standard", "secrets": ["bitbucket-token"],
                                  "allow_repos": ["bitbucket/%s" % bb], "claude_accounts": []}
    policy = {"version": 10, "allow_repos": project_allow, "profiles": profiles}
    if mode != "policy-no-mode":
        policy["budget"] = {"mode": "observe"}
    policy["bitbucket_token"] = block
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
"""A stand-in for `gcloud`, for scripts/live-checks/check197.sh's test only: no
socket is opened and nothing is created.

It answers three reads: the installation's job list (and one job's own
specification, and one execution's), and Cloud Logging — whose lines for a run
are the service's own `credsvc: audit` lines, built here from the mode the case
names. It mints no access token and reads no other project: this check holds no
credential of its own at all.
"""
import hashlib, json, os, re, sys

LOG = os.environ["FAKE_LOG_DIR"]
STATE = os.environ["FAKE_GCLOUD_STATE"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")
shared_job = os.environ.get("FAKE_SHARED_JOB", "fugaro-p-sandbox-github-0123456789abcdef")
perrepo_job = os.environ.get("FAKE_PERREPO_JOB", "acme-app-0123456789abcdef")
bb_job = os.environ.get("FAKE_BB_JOB", "fugaro-p-sandbox-bb-bitbucket-0123456789abcdef")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
positionals = [a for a in args if not a.startswith("-")]
# Like the real CLI, a subcommand or a flag that does not exist is an error: a
# misspelled one therefore cannot hide in any call this check makes.
ALLOWED = {
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("run", "jobs", "describe"): ("--project", "--region", "--format"),
    ("run", "jobs", "executions", "describe"): ("--project", "--region", "--format"),
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


def secret_id(slug, logical):
    """gcp.SecretID: the DERIVED Secret Manager ID a job's env is fed from —
    `fugaro-<readable slug>-<logical>-<16 hex of sha256(slug NUL logical)>`
    (internal/backend/gcp/names.go: derive). The logical name is the LAST
    readable segment and never the first, and the readable part drops the slug's
    own 16-hex tail, so this is the shape a deployment writes and the only one a
    case can use to test the check's mount reading."""
    readable = re.sub(r"-[0-9a-f]{16}$", "", slug.lower())
    h = hashlib.sha256(("%s\x00%s" % (slug, logical)).encode()).hexdigest()[:16]
    return "fugaro-%s-%s-%s" % (readable, logical, h)


def job_doc(name, kind="shared"):
    envs = [{"name": "FUGARO_INSTALLATION", "value": "fugaro"},
            {"name": "FUGARO_REPOSITORY_PROFILE", "value": "sandbox"}]
    if not (mode == "no-url" and kind == "shared") and not (mode == "bb-no-url" and kind == "bitbucket"):
        envs.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL",
                     "value": "https://fugaro-credentials-fake-ue.a.run.app"})
    if mode == "mounted" and kind == "shared":
        # The mount the flip exists to take off: the container the paid key is
        # served from, as the deployment mounts it (config.ModelSecretEnvs and
        # the derived Secret Manager ID behind it — never the logical name,
        # which is what a reading that matched by prefix would need to see).
        # The reference is written the way `gcloud run jobs list` answers it,
        # run/v1's valueFrom.secretKeyRef.name: a fake in the v2 spelling would
        # let a reader that knows only that spelling pass every case here while
        # reading no mount of a real job.
        envs.append({"name": "ANTHROPIC_API_KEY",
                     "valueFrom": {"secretKeyRef": {
                         "name": secret_id(name, "anthropic-api-key"),
                         "key": "latest"}}})
    if mode == "mounted-account" and kind == "shared":
        # The other shape a real job is fed from: a NAMED Claude account's own
        # container, whose logical name carries the account after the token's
        # (config.ClaudeOAuthTokenPrefix, `claude-oauth-token-<account>`) — and
        # the one case left in run/v2's spelling (valueSource.secretKeyRef
        # .secret), so both spellings the reader knows are exercised.
        envs.append({"name": "CLAUDE_CODE_OAUTH_TOKEN",
                     "valueSource": {"secretKeyRef": {
                         "secret": secret_id(name, "claude-oauth-token-work"),
                         "version": "latest"}}})
    if mode == "mounted-plain-value" and kind == "shared":
        # A job whose ANTHROPIC_API_KEY carries a plain value instead of a
        # mount: the flip's row is about the credential being on the job at all,
        # so a plain value counts too.
        envs.append({"name": "ANTHROPIC_API_KEY", "value": "not-a-real-key"})
    if mode == "provider-key" and kind == "shared":
        envs.append({"name": "FUGARO_PROVIDER_KEY_OPENROUTER",
                     "valueFrom": {"secretKeyRef": {"name": "openrouter-key-abcdef", "key": "latest"}}})
    if mode == "bitbucket-token-mount" and kind == "bitbucket":
        envs.append({"name": "FUGARO_BITBUCKET_TOKEN", "value": "not-a-real-token"})
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Job",
            "metadata": {"name": name, "updateTime": os.environ.get("FAKE_JOB_UPDATE", "2026-11-16T09:00:00Z")},
            "spec": {"template": {"spec": {"template": {"spec": {
                "containers": [{"name": "workflow", "env": envs}],
                "serviceAccountName": "fugaro-%s@fugaro-dev.iam.gserviceaccount.com" % name}}}}}}


def all_jobs():
    jobs = [("shared", shared_job), ("perrepo", perrepo_job)]
    if mode in ("bitbucket-job", "bitbucket-token-mount", "bb-no-url"):
        jobs.append(("bitbucket", bb_job))
    return jobs


def job_docs():
    if mode == "no-jobs":
        return []
    return [job_doc(name, kind) for kind, name in all_jobs()]


def execution_doc(name):
    """One execution of a job, as `gcloud run jobs executions describe
    --format=json` answers it: `kind: Execution` with its containers at
    spec.template.spec.containers (the other depth a Job's live at, which the
    check's reader reads both of).

    Its containers are the PRE-flip template's — the model mount and the
    service's URL both on them — because that is the spec an execution created
    before the re-apply was created with and keeps for its whole life (U9).
    `execution-no-mount` is the other shape: a spec with no model credential on
    it at all, which is what a run launched by the FLIPPED template was created
    with, and the one the row must never read as a mount a run in flight kept.
    """
    envs = [{"name": "FUGARO_INSTALLATION", "value": "fugaro"},
            {"name": "FUGARO_CREDENTIALS_SERVICE_URL",
             "value": "https://fugaro-credentials-fake-ue.a.run.app"}]
    if mode != "execution-no-mount":
        # run/v1's spelling again (`gcloud run jobs executions describe`
        # answers the v1 Execution): the mount a run created before the
        # re-apply kept, which the U9 row has to find on this execution.
        envs.append({"name": "ANTHROPIC_API_KEY",
                     "valueFrom": {"secretKeyRef": {
                         "name": secret_id(shared_job, "anthropic-api-key"),
                         "key": "latest"}}})
    return {"apiVersion": "run.googleapis.com/v1", "kind": "Execution",
            "metadata": {"name": name, "updateTime": os.environ.get("FAKE_JOB_UPDATE", "2026-11-16T09:00:00Z")},
            "spec": {"template": {"spec": {"containers": [{"name": "workflow", "env": envs}]}}}}


def run_of(rid):
    """`repo rid job` for a run this test launched, or None: the check reads the
    job out of the execution path its row names, and this is the same fact the
    fake `fugaro` wrote down at launch."""
    path = os.path.join(STATE, "fugaro.runs")
    if not os.path.exists(path):
        return None
    for line in open(path).read().splitlines():
        parts = line.split(" ")
        if len(parts) >= 3 and parts[1] == rid:
            return parts
    return None


if sub == ("run", "jobs", "list"):
    if mode == "jobs-list-fails":
        sys.stderr.write("ERROR: (gcloud) run.jobs.list was refused: the session has expired\n")
        sys.exit(1)
    print(json.dumps(job_docs()))
elif sub == ("run", "jobs", "describe"):
    if mode == "describe-fails":
        sys.stderr.write("ERROR: (gcloud) run.jobs.get was refused: the caller does not have permission to read the job\n")
        sys.exit(1)
    want = positionals[3] if len(positionals) > 3 else ""
    for kind, name in all_jobs():
        if name == want:
            print(json.dumps(job_doc(name, kind)))
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
    print(json.dumps(execution_doc(want)))
elif sub == ("logging", "read"):
    if mode == "log-fails":
        # A log read that does not answer at all: no line of any run's is in the
        # file it writes, which is NOT the same thing as the service having
        # written none — the distinction the rows read off a run's own lines are
        # read against.
        sys.stderr.write("ERROR: (gcloud) logging.read was refused: the caller does not have permission to read the log\n")
        sys.exit(1)
    filt = positionals[2] if len(positionals) > 2 else ""
    m = re.search(r'jsonPayload\.run="([^"]*)"', filt)
    rid = m.group(1) if m else ""
    parts = run_of(rid)
    job = parts[2] if parts else shared_job
    repo = parts[0] if parts else ""
    lines = []
    if mode != "no-audit":
        p = {"message": "credsvc: audit", "run": rid, "repository": repo.replace("/", "-"),
             "job": job,
             "caller": ("fugaro-someone-elses-job@fugaro-dev.iam.gserviceaccount.com"
                        if mode == "wrong-caller" else
                        "fugaro-%s@fugaro-dev.iam.gserviceaccount.com" % job),
             "status": "ok", "credential": "anthropic-api-key", "tier": "B",
             "purpose": "forward"}
        if mode == "no-forward":
            # A run the service served for its GIT traffic only: a shared job's
            # model path reaches the service and nothing else does, so a run
            # whose lines hold no proxied model call is the finding.
            p["credential"] = "bitbucket-token"
            p.pop("tier")
            p["purpose"] = "git"
        lines.append({"jsonPayload": p})
    print(json.dumps(lines))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
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
  # CHECK197_TEST_XTRACE starts the shell the way an owner might by habit: with
  # tracing on, which is the one leak this check's own redaction cannot catch.
  local -a envs=() cli=() runner=(bash) a
  for a in "$@"; do
    case "$a" in
    CHECK197_TEST_XTRACE=*) runner=(bash -x) ;;
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
    CHECK197_REPO="$REPO_ONE" CHECK197_OUTSIDE="$REPO_OUTSIDE" \
    CHECK197_RUN_SECONDS=0 CHECK197_POLL_SECONDS=0 CHECK197_LOG_WAIT_SECONDS=0 \
    CHECK197_LOG_POLL_SECONDS=0 \
    ${envs[@]+"${envs[@]}"} \
    "${runner[@]}" "$sh" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run197() { # dir arg...: the check itself
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
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  # The installation's own local config, as an owner's machine holds it for
  # installation fugaro. This check reads nothing out of it (a shared job's
  # credential is the SERVICE's own bundle, and the one database record it could
  # read belongs to a per-repository job), and the file is here because a real
  # run happens on a machine that has one.
  cat >"$1/home/.config/fugaro/projects/fugaro.yaml" <<'CONFIG_EOF'
name: fugaro
gcp_project: fugaro-dev
endpoints:
  no_auth: false
CONFIG_EOF
}

no_launch() { # dir what — no `fugaro run` was recorded
  expect_absent "$(cat "$1/logs/fugaro.calls")" "fugaro run --repo" "$2: no run was launched"
}

# --------------------------------------------------------------- the tests
# --dry-run is the mode the checklist has the owner try FIRST, on a machine that
# may have no credentials at all: it must print the plan and call NOTHING. Not
# even the installation's project is read, which is why the fugaro log is empty
# too — the guard that reads the cloud is not reached.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run197 "$dir" CHECK197_DRY_RUN=1
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 197 RESULT" "dry run: the block"
  expect_contains "$out" "FACT repos: PASS" "dry run: the two repositories"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the shell is not tracing"
  expect_contains "$out" "FACT policy: FAIL — not reached" "dry run: the policy was not read"
  expect_contains "$out" "FACT project-region: FAIL — not reached" "dry run: the cloud guard is not run"
  expect_contains "$out" "not reached (the run stopped before this step)" "dry run: the row says it was not reached"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS" "dry run: nothing was created"
  expect_contains "$out" "FACT scratch-removed: PASS" "dry run: the scratch directory"
  expect_contains "$out" "CHECK 197 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "gcloud run jobs list --project=fugaro-dev --region=us-east5" "dry run: the plan carries the hard-coded project and region"
  expect_contains "$err" "fugaro run --repo acme/app" "dry run: the plan names the run it would launch"
  expect_contains "$err" "fugaro-credentials" "dry run: the plan names the log filter it would read"
  expect_contains "$err" "REFUSED before any execution exists" "dry run: the plan names the refusal it expects"
  expect_contains "$err" "CHECK197_BITBUCKET is empty" "dry run: the plan says the Bitbucket half is skipped"
  # The flip's plan is the other half's: one more run, the mounts and the
  # in-flight run it reads.
  # The flip's plan needs the flip's own knob as much as the real run does: an
  # input is not a credential, and the dry run promises only that nothing is
  # CALLED (which the empty call logs above hold it to).
  run197 "$dir" CHECK197_DRY_RUN=1 CHECK197_INFLIGHT=2026-11-16-0830-0001 --after-flip
  expect_status "$status" 0 "dry run --after-flip: the verdict"
  expect_contains "$out" "MODE: flip" "dry run --after-flip: the mode"
  expect_contains "$err" "FUGARO_BITBUCKET_TOKEN" "dry run --after-flip: the plan names the token row"
  expect_contains "$err" "updateTime" "dry run --after-flip: the plan names the in-flight reading"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls")
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
  run197 "$dir" FAKE_FUGARO_MODE=failing
  expect_status "$status" 0 "doctor exit 1: the run goes on"
  expect_absent "$err" "not the fugaro-dev installation" "doctor exit 1: no stop"
  expect_contains "$out" "FACT served-one: PASS" "doctor exit 1: the check ran to the end"
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=other
  expect_status "$status" 1 "project guard: the verdict"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run" "project guard: nothing was read"
  no_launch "$dir" "project guard"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# A trace would print every command this check runs into the log the owner
# pastes: the check prints its own RESULT block, so a trace is never needed.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run197 "$dir" CHECK197_TEST_XTRACE=1
  expect_status "$status" 1 "xtrace: the verdict"
  expect_contains "$err" "is tracing" "xtrace: the stop"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  no_launch "$dir" "xtrace"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "xtrace: no log was read"
  if [ -d "$scratch" ]; then fail "xtrace: the scratch directory survived"; fi
}

# The knobs are the check's whole input, and every one of them is read before
# anything is called: a run that named no repository, a malformed one, the same
# repository twice, the owner's own workspace, or a flip with no run in flight
# must stop with nothing launched and nothing read.
test_the_knobs_guard() {
  local dir=$work/knobs case_ label rest var val
  prepare "$dir"
  # One `label|VAR|VALUE` line each: the knob the case turns, and what it turns
  # it to. The two knobs the check requires are always named, so each case
  # changes exactly one thing.
  for case_ in \
    "norepo|CHECK197_REPO|" \
    "malformed|CHECK197_REPO|acme" \
    "deep|CHECK197_REPO|acme/app/extra" \
    "trailing|CHECK197_REPO|acme/app/" \
    "belong|CHECK197_REPO|belong/app" \
    "same|CHECK197_OUTSIDE|acme/app" \
    "outside-norepo|CHECK197_OUTSIDE|" \
    "outside-belong|CHECK197_OUTSIDE|belong/other"; do
    label=${case_%%|*}
    rest=${case_#*|}
    var=${rest%%|*}
    val=${rest#*|}
    status=0
    env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_REPOS="$REPOS_DEFAULT" \
      CHECK197_REPO="$REPO_ONE" CHECK197_OUTSIDE="$REPO_OUTSIDE" \
      "$var=$val" \
      bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
    out=$(cat "$dir/out")
    err=$(cat "$dir/err")
    expect_status "$status" 1 "${label}: the verdict"
    expect_contains "$out" "FACT aborted: FAIL" "${label}: the block says it stopped"
    expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "${label}: no run was launched"
    expect_absent "$(cat "$dir/logs/gcloud.calls")" "jobs list" "${label}: nothing was read"
    : >"$dir/logs/fugaro.calls"
    : >"$dir/logs/gcloud.calls"
  done
  # The flip's own required knob: a run this check picks itself could be one that
  # ended hours before the re-apply, and U9's reading is about THAT run.
  run197 "$dir" --after-flip
  expect_status "$status" 1 "flip with no run in flight: the verdict"
  expect_contains "$err" "CHECK197_INFLIGHT must name the run" "flip with no run in flight: the stop"
  no_launch "$dir" "flip with no run in flight"
}

# The policy is where the profile that admits the repository is read from. A read
# that did not answer, a repository the project does not admit, a repository no
# profile admits, one two profiles admit, a profile of another provider, and a
# budget mode the gateway does not exist in: each is settled before any run is
# launched, because a run commits to a sandbox repository and opens a pull
# request.
test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/policyfail
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=policy-fails
  expect_status "$status" 2 "policy unreadable: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "policy unreadable: the row"
  expect_contains "$out" "could not be read" "policy unreadable: the row says so"
  expect_contains "$out" "FACT switch: FAIL — not reached" "policy unreadable: nothing below was read"
  no_launch "$dir" "policy unreadable"
}

test_a_repository_the_project_does_not_admit_is_a_failure() {
  local dir=$work/notproj
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=not-proj-admitted
  expect_status "$status" 1 "not admitted to the project: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "not admitted to the project: the row"
  expect_contains "$out" "fugaro project repos add" "not admitted to the project: the remedy"
  no_launch "$dir" "not admitted to the project"
}

test_a_repository_no_profile_admits_is_a_failure() {
  local dir=$work/noprofile
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=no-profiles
  expect_status "$status" 1 "no profile admits it: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "no profile admits it: the row"
  expect_contains "$out" "to NO profile" "no profile admits it: the row says which"
  no_launch "$dir" "no profile admits it"
}

test_a_repository_two_profiles_admit_leaves_the_rows_unmeasured() {
  local dir=$work/twoprofiles
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=two-profiles
  expect_status "$status" 2 "two profiles admit it: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "two profiles admit it: the row"
  expect_contains "$out" "2 profiles of the published policy admit" "two profiles admit it: the row says why"
  no_launch "$dir" "two profiles admit it"
}

test_a_profile_of_another_provider_is_a_failure() {
  local dir=$work/otherprovider
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=other-provider
  expect_status "$status" 1 "other provider: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "other provider: the row"
  expect_contains "$out" "serves provider 'bitbucket'" "other provider: the row says which"
  no_launch "$dir" "other provider"
}

test_a_policy_with_no_budget_mode_leaves_the_rows_unmeasured() {
  local dir=$work/nomode
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=policy-no-mode
  expect_status "$status" 2 "no budget mode: the verdict"
  expect_contains "$out" "FACT policy: FAIL" "no budget mode: the row"
  expect_contains "$out" "carries no budget mode" "no budget mode: the row says so"
  expect_contains "$out" "a mode the document does not carry is neither" "no budget mode: why it is not a finding"
  no_launch "$dir" "no budget mode"
}

test_no_shared_job_of_the_profile_leaves_the_rows_unmeasured() {
  local dir=$work/nojobs
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=no-jobs
  expect_status "$status" 2 "no shared job: the verdict"
  expect_contains "$out" "FACT policy: PASS" "no shared job: the policy was read"
  expect_contains "$out" "FACT switch: FAIL" "no shared job: the row"
  expect_contains "$out" "serves no shared project job" "no shared job: the row says so"
  no_launch "$dir" "no shared job"
}

test_a_shared_job_without_the_service_url_is_a_failure() {
  local dir=$work/nourl
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=no-url
  expect_status "$status" 1 "no service URL: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "no service URL: the row"
  expect_contains "$out" "FUGARO_CREDENTIALS_SERVICE_URL" "no service URL: the row names the variable"
  no_launch "$dir" "no service URL"
}

test_a_jobs_list_that_could_not_be_read_is_a_failure() {
  local dir=$work/jobsfail
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=jobs-list-fails
  expect_status "$status" 1 "job list refused: the verdict"
  expect_contains "$out" "FACT switch: FAIL" "job list refused: the row"
  expect_contains "$out" "jobs list exited non-zero" "job list refused: the row says so"
  no_launch "$dir" "job list refused"
}

# The whole first half, on a sound installation: the policy admits the
# repository to exactly one profile, the profile's shared job names the service,
# the run finishes on that job, the service's own lines carry a proxied model
# call whose verified caller is the job's own service account, and a launch of
# the repository outside the profile is refused before any execution exists.
# The Bitbucket rows read INFO — the half the plan puts after D2079 — and the
# block ends 0.
test_the_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run197 "$dir"
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT policy: PASS" "happy path: the policy"
  expect_contains "$out" "admitted to the profile 'sandbox'" "happy path: the profile it read"
  expect_contains "$out" "FACT switch: PASS" "happy path: the switch"
  expect_contains "$out" "$SHARED_JOB" "happy path: the shared job"
  expect_contains "$out" "FACT job-one: PASS" "happy path: the run landed on the shared job"
  expect_contains "$out" "ran on the shared project job" "happy path: the job row says which"
  expect_contains "$out" "FACT run-one: PASS" "happy path: the run"
  expect_contains "$out" "FACT audited-one: PASS" "happy path: the service wrote lines for it"
  expect_contains "$out" "anthropic-api-key|B|forward" "happy path: the credential, tier and purpose"
  expect_contains "$out" "FACT served-one: PASS" "happy path: the service served it"
  expect_contains "$out" "the verified caller is the shared job's own service account" "happy path: the caller reading"
  expect_contains "$out" "FACT outside-profile: PASS" "happy path: the refusal"
  expect_contains "$out" "left no row in that repository's listing" "happy path: no execution exists"
  expect_contains "$out" "**The SERVICE's own half of this gate is NOT measured here**" "happy path: the limitation is in the block"
  expect_contains "$out" "FACT job-bitbucket: INFO" "happy path: the Bitbucket half is not applicable"
  expect_contains "$out" "CHECK197_BITBUCKET is empty" "happy path: the Bitbucket rows say why"
  expect_contains "$out" "CHECK 197 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch directory went"
  # The policy is read once and the launch is the only write.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "project policy show --project fugaro --json" "happy path: the policy read"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "logging read" "happy path: the log read"
}

# The same run with the scratch directory kept: the row says it was KEPT and not
# that the check failed to remove it, and the verdict is unchanged.
test_the_happy_path_with_the_scratch_directory_kept() {
  local dir=$work/kept
  prepare "$dir"
  run197 "$dir" CHECK197_KEEP_SCRATCH=1
  expect_status "$status" 0 "kept scratch: the verdict"
  expect_contains "$out" "FACT scratch-removed: PASS" "kept scratch: the row"
  expect_contains "$out" "kept on request" "kept scratch: the row says it was kept"
  [ -n "$scratch" ] || fail "kept scratch: the script did not say where its scratch directory is"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    kept+=("$scratch")
  else
    fail "kept scratch: the directory was removed although CHECK197_KEEP_SCRATCH is set"
  fi
}

# The Bitbucket half, on an installation that has the shared job (D2079's T2.2):
# the job exists, names the service, and the run of the proxied repository is
# served on it.
test_the_bitbucket_half_when_the_shared_job_is_there() {
  local dir=$work/bitbucket
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=bitbucket-job FAKE_GCLOUD_MODE=bitbucket-job \
    CHECK197_BITBUCKET="$BB_REPO" FAKE_REPOS="$BB_REPOS_ALL"
  expect_status "$status" 0 "bitbucket half: the verdict"
  expect_contains "$out" "FACT job-bitbucket: PASS" "bitbucket half: the job"
  expect_contains "$out" "$BB_JOB" "bitbucket half: the job's name"
  expect_contains "$out" "FACT run-bitbucket: PASS" "bitbucket half: the run"
  expect_contains "$out" "FACT served-bitbucket: PASS" "bitbucket half: the service served it"
}

# A Bitbucket repository the published block does not proxy: D2079's T2.2
# deploys a shared Bitbucket job only for a profile whose every repository the
# block proxies, so nothing about this installation is measured and the block
# says so rather than failing.
test_a_bitbucket_repository_the_block_does_not_proxy_is_info() {
  local dir=$work/bbnotproxied
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=bitbucket-job FAKE_GCLOUD_MODE=bitbucket-job \
    CHECK197_BITBUCKET="$BB_REPO" FAKE_REPOS="$BB_REPOS_ALL" FAKE_BB_REPO=someoneelse/other
  expect_status "$status" 0 "bitbucket not proxied: the verdict"
  expect_contains "$out" "FACT job-bitbucket: INFO" "bitbucket not proxied: the row"
  expect_contains "$out" "not in the published Bitbucket block's allow_repos" "bitbucket not proxied: the row says why"
}

# A run that did not finish on the shared job: every credential of its path is
# the service's, so a run that could not open a pull request is the finding.
test_a_run_that_did_not_finish_is_a_failure() {
  local dir=$work/runfailed
  prepare "$dir"
  run197 "$dir" FAKE_ROW_STATUS=failed
  expect_status "$status" 1 "run failed: the verdict"
  expect_contains "$out" "FACT run-one: FAIL" "run failed: the row"
  expect_contains "$out" "ended 'failed' and no pull request was opened" "run failed: the row says so"
  expect_contains "$out" "FACT job-one: PASS" "run failed: the job is still read"
  expect_contains "$out" "FACT served-one: PASS" "run failed: the service was still read"
}

# A run whose execution names the PER-REPOSITORY job: the repository's own
# fugaro.yaml selects no profile, so nothing below it is about the shared shape
# S5 is — and the row says which knob to look at instead of guessing.
test_a_run_that_landed_on_a_per_repository_job_is_a_failure() {
  local dir=$work/perrepo
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=run-on-per-repo-job
  expect_status "$status" 1 "per-repository job: the verdict"
  expect_contains "$out" "FACT job-one: FAIL" "per-repository job: the row"
  expect_contains "$out" "which is not a shared project job of the profile 'sandbox'" "per-repository job: the row says which"
  expect_contains "$out" "fugaro.yaml" "per-repository job: the remedy"
  expect_contains "$out" "FACT run-one: PASS" "per-repository job: the run itself still finished"
}

# A launch that never happened: the row says so and the rest of the runs' rows
# are unmeasured rather than read off a listing that holds no row.
test_a_launch_that_failed_leaves_the_rows_unmeasured() {
  local dir=$work/launchfail
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=launch-fails
  expect_status "$status" 2 "launch failed: the verdict"
  expect_contains "$out" "FACT run-one: FAIL" "launch failed: the row"
  expect_contains "$out" "never launched one" "launch failed: the row says so"
  expect_absent "$out" "FACT run-one: PASS" "launch failed: no pass"
}

test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/listingfail
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=listing-fails
  expect_status "$status" 2 "listing failed: the verdict"
  expect_contains "$out" "FACT run-one: FAIL" "listing failed: the row"
  expect_contains "$out" "does not appear in 'fugaro runs ls" "listing failed: the row says so"
}

# A log read that did not answer is NOT the service having written no line: a
# row read off it must say it could not measure, never FAIL the installation on
# a read that returned nothing.
test_a_log_read_that_failed_leaves_the_served_rows_unmeasured() {
  local dir=$work/logfail
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=log-fails
  expect_status "$status" 2 "log read failed: the verdict"
  expect_contains "$out" "FACT run-one: PASS" "log read failed: the run itself passed"
  expect_contains "$out" "FACT audited-one: FAIL" "log read failed: the audit row"
  expect_contains "$out" "could not be read" "log read failed: the row says so"
  expect_contains "$out" "is NOT the service having written no line" "log read failed: the row says what it is not"
  expect_absent "$out" "FACT served-one: PASS" "log read failed: no pass on a read that did not answer"
}

test_a_run_the_service_wrote_no_line_for_is_a_failure() {
  local dir=$work/noaudit
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=no-audit
  expect_status "$status" 1 "no audit line: the verdict"
  expect_contains "$out" "FACT audited-one: FAIL" "no audit line: the row"
  expect_contains "$out" "wrote NO line for the one run" "no audit line: the row says so"
  expect_absent "$out" "FACT served-one: PASS" "no audit line: no pass"
}

# A run the service answered for its git traffic only: a shared job's MODEL
# path reaches the service and nothing else does, so a run whose lines hold no
# proxied model call is the finding this check exists for.
test_a_run_with_no_proxied_model_call_is_a_failure() {
  local dir=$work/noforward
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=no-forward
  expect_status "$status" 1 "no forward line: the verdict"
  expect_contains "$out" "FACT audited-one: PASS" "no forward line: the run was served"
  expect_contains "$out" "FACT served-one: FAIL" "no forward line: the served row"
  expect_contains "$out" "NO line of it is a proxied MODEL call" "no forward line: the row says so"
}

# The caller on the service's line is the identity the service VERIFIED, and
# the row compares it with the shared job's own service account: a call served
# under another account is a credential served to something else.
test_a_caller_that_is_not_the_shared_jobs_account_is_a_failure() {
  local dir=$work/wrongcaller
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=wrong-caller
  expect_status "$status" 1 "wrong caller: the verdict"
  expect_contains "$out" "FACT served-one: FAIL" "wrong caller: the served row"
  expect_contains "$out" "is the identity the service VERIFIED" "wrong caller: the row says which field"
  expect_contains "$out" "fugaro-someone-elses-job@fugaro-dev.iam.gserviceaccount.com" "wrong caller: the caller it read"
}

# The shared job's own service account cannot be read: the row says it could not
# measure rather than comparing against nothing.
test_a_shared_job_that_could_not_be_read_leaves_the_served_row_unmeasured() {
  local dir=$work/described
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=describe-fails
  expect_status "$status" 2 "job unreadable: the verdict"
  expect_contains "$out" "FACT audited-one: PASS" "job unreadable: the audit row"
  expect_contains "$out" "FACT served-one: FAIL" "job unreadable: the served row"
  expect_contains "$out" "could not be read" "job unreadable: the row says so"
}

# The repository outside the profile: the launcher refuses it, the refusal
# carries the profile's own sentence, and the listing holds no row for it.
test_the_outside_repository_is_refused() {
  local dir=$work/outside
  prepare "$dir"
  run197 "$dir"
  expect_status "$status" 0 "outside refused: the verdict"
  expect_contains "$out" "FACT outside-profile: PASS" "outside refused: the row"
  expect_contains "$err" "the launcher is expected to refuse it" "outside refused: the check said what it expected"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "fugaro run --repo acme/other" "outside refused: the launch was attempted"
}

# The launcher LAUNCHED a repository outside the profile: the profile's own
# allow_repos is what says which repositories a shared job serves, so a run
# admitted to the job anyway is the finding.
test_the_outside_repository_launched_anyway_is_a_failure() {
  local dir=$work/outsidelaunched
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=outside-launched
  expect_status "$status" 1 "outside launched: the verdict"
  expect_contains "$out" "FACT outside-profile: FAIL" "outside launched: the row"
  expect_contains "$out" "LAUNCHED a run of acme/other" "outside launched: the row says so"
}

# A refusal by something else — the project's gate, another launch's claim — is
# not the profile's gate: which gate answered is not measured, and the row says
# so instead of passing on a refusal by another rule entirely.
test_another_refusal_is_not_the_profile_gate() {
  local dir=$work/otherrefusal
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=outside-other-refusal
  expect_status "$status" 2 "another refusal: the verdict"
  expect_contains "$out" "FACT outside-profile: FAIL" "another refusal: the row"
  expect_contains "$out" "does NOT carry the profile's own sentence" "another refusal: the row says so"
}

test_a_refusal_without_the_stop_line_is_unmeasured() {
  local dir=$work/nostopline
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=outside-no-stop-line
  expect_status "$status" 2 "no stop line: the verdict"
  expect_contains "$out" "FACT outside-profile: FAIL" "no stop line: the row"
  expect_contains "$out" "stops before any execution exists" "no stop line: the row names the line it wanted"
}

# A repository the PROJECT admits and the profile widens to cover: it is not
# outside the profile at all, and a row about it would be a row about nothing.
test_a_repository_the_profile_admits_is_not_the_outside_case() {
  local dir=$work/widened
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=profile-widens
  expect_status "$status" 1 "profile admits it: the verdict"
  expect_contains "$out" "FACT outside-profile: FAIL" "profile admits it: the row"
  expect_contains "$out" "is admitted to 1 profile(s)" "profile admits it: the row says so"
}

# A repository the published policy does not admit to the PROJECT at all: that
# refusal is the project's gate, which is another check's row.
test_a_repository_off_the_project_is_not_the_outside_case() {
  local dir=$work/offproject
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=not-proj-admitted
  expect_status "$status" 1 "off the project: the verdict"
  expect_contains "$out" "FACT outside-profile: FAIL — not reached" "off the project: the row was not reached"
}

# --after-flip, on a sound flipped installation: nothing mounts a model
# credential, the provider keys are left and said so, and a run on the flipped
# shared job still finishes — the service is the only path its model calls have.
test_after_flip_passes_when_nothing_mounts() {
  local dir=$work/flip
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_INFLIGHT_STARTED="$INFLIGHT_STARTED" CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 0 "flip: the verdict"
  expect_contains "$out" "MODE: flip" "flip: the mode"
  expect_contains "$out" "FACT no-model-mounts: PASS" "flip: the mount row"
  expect_contains "$out" "none mounts a model credential" "flip: the mount row says so"
  expect_contains "$out" "FACT no-token-mounts: INFO" "flip: the token row is not applicable"
  expect_contains "$out" "FACT inflight-run: PASS" "flip: the in-flight run"
  expect_contains "$out" "still finished" "flip: the in-flight run finished"
  expect_contains "$out" "FACT inflight-mount: PASS" "flip: the mount it kept"
  expect_contains "$out" "kept the spec it was created with" "flip: the mount row says why"
  expect_contains "$out" "FACT run-flip-one: PASS" "flip: the run on the flipped job"
  expect_contains "$out" "FACT served-flip-one: PASS" "flip: the service served it"
  expect_contains "$out" "CHECK 197 RESULT END (exit 0)" "flip: the end line"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run jobs executions describe" "flip: the execution was read"
  # The block's own key set is the flip's: the first half's rows are not in it.
  expect_absent "$out" "FACT outside-profile" "flip: the first half's rows are not in the block"
}

# The providers keys R6 keeps: a job that still holds one is the design, and the
# row records the count without failing on it.
test_after_flip_reports_the_provider_keys() {
  local dir=$work/flipprovider
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_GCLOUD_MODE=provider-key CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 0 "flip provider keys: the verdict"
  expect_contains "$out" "FACT no-model-mounts: PASS" "flip provider keys: the mount row"
  expect_contains "$out" "FACT no-model-mounts-detail: INFO" "flip provider keys: the detail row"
  expect_contains "$out" "provider-key variable(s) are left" "flip provider keys: the detail says what it is"
}

test_after_flip_refuses_a_job_that_still_mounts_the_model_credential() {
  local dir=$work/flipmounted
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_GCLOUD_MODE=mounted CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 1 "flip mounted: the verdict"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "flip mounted: the row"
  expect_contains "$out" "still MOUNT the model credential" "flip mounted: the row says so"
  expect_contains "$out" "anthropic-api-key" "flip mounted: the container it read"
}

test_after_flip_refuses_a_job_that_still_mounts_an_accounts_token() {
  local dir=$work/flipaccount
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_GCLOUD_MODE=mounted-account CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 1 "flip account mount: the verdict"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "flip account mount: the row"
  expect_contains "$out" "claude-oauth-token-work" "flip account mount: the container it read"
}

test_after_flip_refuses_a_plain_model_value() {
  local dir=$work/flipplain
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_GCLOUD_MODE=mounted-plain-value CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 1 "flip plain value: the verdict"
  expect_contains "$out" "FACT no-model-mounts: FAIL" "flip plain value: the row"
  expect_contains "$out" "ANTHROPIC_API_KEY" "flip plain value: the variable it read"
}

# A shared Bitbucket job that carries the token: D2079's answer is that it
# mounts NONE, and a token on a job serving several repositories is what the
# proxy exists to end.
test_after_flip_refuses_a_bitbucket_token() {
  local dir=$work/fliptoken
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0001 \
    FAKE_GCLOUD_MODE=bitbucket-token-mount FAKE_FUGARO_MODE=bitbucket-job \
    CHECK197_BITBUCKET="$BB_REPO" FAKE_REPOS="$BB_REPOS_ALL" CHECK197_INFLIGHT=2026-11-16-0830-0001
  expect_status "$status" 1 "flip token: the verdict"
  expect_contains "$out" "FACT no-token-mounts: FAIL" "flip token: the row"
  expect_contains "$out" "FUGARO_BITBUCKET_TOKEN" "flip token: the variable it read"
  expect_contains "$out" "mounts NO token" "flip token: the row says what D2079 answers"
}

# A Bitbucket shared job whose environment does not name the service: a proxied
# job names it, and one that does not reaches nothing.
test_a_bitbucket_job_without_the_service_url_is_a_failure() {
  local dir=$work/bbnourl
  prepare "$dir"
  run197 "$dir" FAKE_GCLOUD_MODE=bb-no-url FAKE_FUGARO_MODE=bitbucket-job \
    CHECK197_BITBUCKET="$BB_REPO" FAKE_REPOS="$BB_REPOS_ALL"
  expect_status "$status" 1 "bitbucket no url: the verdict"
  expect_contains "$out" "FACT job-bitbucket: FAIL" "bitbucket no url: the row"
  expect_contains "$out" "shared Bitbucket job(s) of the profile 'sandbox-bb'" "bitbucket no url: the row says which"
}

# U9: the run was already going when the profile's job was re-applied, its
# execution keeps the spec it was created with, and it still finishes.
test_the_inflight_run_keeps_its_mount() {
  local dir=$work/inflight
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0002 \
    FAKE_INFLIGHT_STARTED="$INFLIGHT_STARTED" CHECK197_INFLIGHT=2026-11-16-0830-0002
  expect_status "$status" 0 "in-flight: the verdict"
  expect_contains "$out" "FACT inflight-run: PASS" "in-flight: the run"
  expect_contains "$out" "before the job 'fugaro-p-sandbox-github-0123456789abcdef' was last updated" "in-flight: the order it read"
  expect_contains "$out" "FACT inflight-mount: PASS" "in-flight: the mount"
  expect_contains "$out" "$(printf '%s' "$INFLIGHT_STARTED")" "in-flight: the run's own start"
}

# The execution's own spec carries no model mount although it was created before
# the re-apply: it is not a run the mount-dropping re-apply was in flight
# across, and the row says which run to name instead.
test_an_inflight_execution_with_no_mount_is_a_failure() {
  local dir=$work/inflightnomount
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0003 \
    FAKE_GCLOUD_MODE=execution-no-mount CHECK197_INFLIGHT=2026-11-16-0830-0003
  expect_status "$status" 1 "in-flight no mount: the verdict"
  expect_contains "$out" "FACT inflight-run: PASS" "in-flight no mount: the run"
  expect_contains "$out" "FACT inflight-mount: FAIL" "in-flight no mount: the mount row"
  expect_contains "$out" "carries NO model mount" "in-flight no mount: the row says so"
}

test_an_execution_that_could_not_be_read_leaves_the_row_unmeasured() {
  local dir=$work/inflightunreadable
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0004 \
    FAKE_GCLOUD_MODE=executions-describe-fails CHECK197_INFLIGHT=2026-11-16-0830-0004
  expect_status "$status" 2 "in-flight unreadable: the verdict"
  expect_contains "$out" "FACT inflight-run: PASS" "in-flight unreadable: the run"
  expect_contains "$out" "FACT inflight-mount: FAIL" "in-flight unreadable: the mount row"
  expect_contains "$out" "could not be read" "in-flight unreadable: the row says so"
}

# A run whose record names no execution: the job it ran on — and the moment that
# job was re-applied — is not read, and the row says it could not measure.
test_a_row_that_names_no_execution_leaves_the_rows_unmeasured() {
  local dir=$work/inflightnoexec
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0005 \
    FAKE_ROW_NO_EXECUTION=1 CHECK197_INFLIGHT=2026-11-16-0830-0005
  expect_status "$status" 2 "no execution: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "no execution: the run row"
  expect_contains "$out" "names no execution" "no execution: the row says so"
  expect_contains "$out" "FACT inflight-mount: FAIL" "no execution: the mount row"
}

# A run that was launched by the template the re-apply wrote is not a run that
# was in flight ACROSS it: its mount says nothing about U9.
test_a_run_that_started_after_the_reapply_is_not_the_case() {
  local dir=$work/inflightafter
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0006 \
    FAKE_INFLIGHT_STARTED=2026-11-16T09:30:00Z CHECK197_INFLIGHT=2026-11-16-0830-0006
  expect_status "$status" 1 "in-flight after: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "in-flight after: the run row"
  expect_contains "$out" "not before the job" "in-flight after: the row says so"
  expect_contains "$out" "FACT inflight-mount: FAIL" "in-flight after: the mount row"
  expect_contains "$out" "started after the re-apply" "in-flight after: the mount row says so"
}

# A run that did not finish although it started before the re-apply: the
# re-apply reached INTO a run that was already going, which is exactly what the
# update's in-flight rule forbids.
test_a_run_in_flight_that_did_not_finish_is_a_failure() {
  local dir=$work/inflightfailed
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0007 \
    FAKE_INFLIGHT_STARTED="$INFLIGHT_STARTED" FAKE_ROW_STATUS=failed \
    CHECK197_INFLIGHT=2026-11-16-0830-0007
  expect_status "$status" 1 "in-flight failed: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "in-flight failed: the run row"
  expect_contains "$out" "could not finish" "in-flight failed: the row says so"
  expect_contains "$out" "reached INTO a run that was already going" "in-flight failed: the row says why it matters"
}

# A run the check cannot find at all: it says so rather than reading a mount off
# a run it never saw.
test_a_run_in_flight_that_is_not_in_the_listing_is_unmeasured() {
  local dir=$work/inflightmissing
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0008 \
    FAKE_INFLIGHT_REPO=acme/app CHECK197_INFLIGHT=2026-11-16-0899-0008
  expect_status "$status" 2 "in-flight missing: the verdict"
  expect_contains "$out" "FACT inflight-run: FAIL" "in-flight missing: the run row"
  expect_contains "$out" "is not in the listing of acme/app" "in-flight missing: the row says so"
}

# The flip's own run: the mounts are gone, so the service is the only path its
# model calls have, and a run that finishes is the flip having left the path
# intact. A shared job off the switch leaves it unmeasured — the switch row
# already carries why.
test_a_flipped_job_off_the_switch_leaves_the_run_unmeasured() {
  local dir=$work/flipoffswitch
  prepare "$dir"
  run197 "$dir" --after-flip FAKE_INFLIGHT=2026-11-16-0830-0009 \
    FAKE_GCLOUD_MODE=no-url CHECK197_INFLIGHT=2026-11-16-0830-0009
  expect_status "$status" 1 "flip off switch: the verdict"
  expect_contains "$out" "FACT no-model-mounts: PASS" "flip off switch: the mount row was still read"
  expect_contains "$out" "FACT switch: FAIL" "flip off switch: the switch row"
  expect_contains "$out" "FACT run-flip-one: FAIL" "flip off switch: the run row"
  expect_contains "$out" "not on the credentials-service switch" "flip off switch: the run row says why"
  expect_absent "$out" "FACT run-flip-one: PASS" "flip off switch: no pass"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run-id 2026" "flip off switch: no run of this check's was launched"
}

# A development build is not the release this check is about: the check runs on
# the release that carries B5.1–B5.5, and a checkout build reaches no released
# service image.
test_a_development_build_is_a_failure() {
  local dir=$work/devbuild
  prepare "$dir"
  run197 "$dir" FAKE_FUGARO_MODE=dev
  expect_status "$status" 1 "dev build: the verdict"
  expect_contains "$out" "FACT fugaro-version: FAIL" "dev build: the row"
  expect_contains "$out" "development build is not the release" "dev build: the row says so"
}

# The whole run, on a machine and an installation that are the sandbox one: no
# command may ever name the owner's own workspace, and every gcloud call names
# fugaro-dev.
test_never_belong() {
  local dir=$work/never
  prepare "$dir"
  run197 "$dir"
  expect_status "$status" 0 "never belong: the run went on"
  local projects p all
  all=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls")
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
    CHECK197_REPO="$REPO_ONE" CHECK197_OUTSIDE="$REPO_OUTSIDE" \
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
t test_the_knobs_guard
t test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_repository_the_project_does_not_admit_is_a_failure
t test_a_repository_no_profile_admits_is_a_failure
t test_a_repository_two_profiles_admit_leaves_the_rows_unmeasured
t test_a_profile_of_another_provider_is_a_failure
t test_a_policy_with_no_budget_mode_leaves_the_rows_unmeasured
t test_no_shared_job_of_the_profile_leaves_the_rows_unmeasured
t test_a_shared_job_without_the_service_url_is_a_failure
t test_a_jobs_list_that_could_not_be_read_is_a_failure
t test_the_happy_path
t test_the_happy_path_with_the_scratch_directory_kept
t test_the_bitbucket_half_when_the_shared_job_is_there
t test_a_bitbucket_repository_the_block_does_not_proxy_is_info
t test_a_run_that_did_not_finish_is_a_failure
t test_a_run_that_landed_on_a_per_repository_job_is_a_failure
t test_a_launch_that_failed_leaves_the_rows_unmeasured
t test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_log_read_that_failed_leaves_the_served_rows_unmeasured
t test_a_run_the_service_wrote_no_line_for_is_a_failure
t test_a_run_with_no_proxied_model_call_is_a_failure
t test_a_caller_that_is_not_the_shared_jobs_account_is_a_failure
t test_a_shared_job_that_could_not_be_read_leaves_the_served_row_unmeasured
t test_the_outside_repository_is_refused
t test_the_outside_repository_launched_anyway_is_a_failure
t test_another_refusal_is_not_the_profile_gate
t test_a_refusal_without_the_stop_line_is_unmeasured
t test_a_repository_the_profile_admits_is_not_the_outside_case
t test_a_repository_off_the_project_is_not_the_outside_case
t test_after_flip_passes_when_nothing_mounts
t test_after_flip_reports_the_provider_keys
t test_after_flip_refuses_a_job_that_still_mounts_the_model_credential
t test_after_flip_refuses_a_job_that_still_mounts_an_accounts_token
t test_after_flip_refuses_a_plain_model_value
t test_after_flip_refuses_a_bitbucket_token
t test_a_bitbucket_job_without_the_service_url_is_a_failure
t test_the_inflight_run_keeps_its_mount
t test_an_inflight_execution_with_no_mount_is_a_failure
t test_an_execution_that_could_not_be_read_leaves_the_row_unmeasured
t test_a_row_that_names_no_execution_leaves_the_rows_unmeasured
t test_a_run_that_started_after_the_reapply_is_not_the_case
t test_a_run_in_flight_that_did_not_finish_is_a_failure
t test_a_run_in_flight_that_is_not_in_the_listing_is_unmeasured
t test_a_flipped_job_off_the_switch_leaves_the_run_unmeasured
t test_a_development_build_is_a_failure
t test_never_belong
t test_a_missing_tool_stops_before_anything_is_called

if [ "$failures" -ne 0 ]; then
  echo "check197_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check197_test.sh: ok"
