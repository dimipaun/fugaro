#!/usr/bin/env bash
# Tests for scripts/live-checks/check196.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project or the network. The fake gcloud
# answers the four reads the check makes — the project's recent builds, the
# installation's Cloud Run jobs and one execution, the service's own log lines
# and the service's own IAM policy — out of a small project built from the mode
# it is given, and it records every argument it is handed, so the tests can
# assert that the check asked for the sandbox installation's project and nothing
# else, that a row it could not measure is never read as a pass, and that
# nothing secret-shaped ever reaches the block.
#
# Usage: bash scripts/live-checks/check196_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check196.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK196_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
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

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check196.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
case "${1:-}" in
version)
  case "${FAKE_FUGARO_MODE:-ok}" in
  dev-version) echo "fugaro 0.13.0-dev (test)" ;;
  *) echo "fugaro 0.13.0 (test)" ;;
  esac
  ;;
doctor)
  # Faithful to internal/cli/doctor.go's emitDoctor: the checks first, then the
  # project: line -- on stdout -- and exit 1 as soon as any check of its own
  # failed, whatever that line said. The check must read the line and not the
  # status, which is what the failing mode proves.
  case "${FAKE_FUGARO_MODE:-ok}" in
  other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  failing)
    echo "check: credentials FAIL (no usable credentials)"
    echo "project: fugaro (GCP fugaro-dev, us-east5)"
    exit 1
    ;;
  *) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  esac
  ;;
*) exit 1 ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check196.sh's test only: it
answers the check's reads out of a project built from FAKE_GCLOUD_MODE and never
talks to anything.

The two sandbox repositories, their slugs, their build accounts and the
service's address are the constants below, so a test names the MODE it wants and
gets a whole project in that state.
"""
import json, os, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")
FLIP = mode.startswith("flip")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
GH_REPO = "acme/sandbox"
BB_REPO = "acme/bb-sandbox"
GH_SLUG = "acme-sandbox-0123456789abcdef"
BB_SLUG = "acme-bb-sandbox-fedcba9876543210"
GH_ACCT = "fugaro-b-0123456789abcdef@fugaro-dev.iam.gserviceaccount.com"
BB_ACCT = "fugaro-b-fedcba9876543210@fugaro-dev.iam.gserviceaccount.com"
SVC_URL = "https://fugaro-credentials-fake-ue.a.run.app"


def flag(name, default=""):
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


# Like the real CLI, a flag a subcommand does not have is an error: every
# subcommand this check calls is listed with the flags it may pass, so a
# misspelled or invented flag cannot hide in any call.
ALLOWED = {
    ("builds", "list"): ("--project", "--limit", "--format"),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("run", "jobs", "executions", "list"): ("--project", "--region", "--job", "--limit", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
    ("run", "services", "get-iam-policy"): ("--project", "--region", "--format"),
}
positionals = [a for a in args if not a.startswith("-")]
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

PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)
REGION = flag("--region")
if REGION and REGION != "us-east5":
    sys.stderr.write("ERROR: (gcloud) this check runs in us-east5 only\n")
    sys.exit(1)


def die(msg):
    sys.stderr.write("ERROR: (gcloud) %s\n" % msg)
    sys.exit(1)


def emit(doc):
    sys.stdout.write(json.dumps(doc) + "\n")


def clone_url(repo, provider):
    return "https://%s/%s.git" % ("bitbucket.org" if provider == "bitbucket" else "github.com", repo)


def account(provider):
    return GH_ACCT if provider == "github" else BB_ACCT


def slug(provider):
    return GH_SLUG if provider == "github" else BB_SLUG


def build(repo, provider):
    """The derived-image build of one repository, as `gcloud builds list
    --format=json` reports it: its substitutions, the account it ran as (a
    RESOURCE NAME, the shape the API uses), and the Secret Manager containers
    its request mounts -- none of them after the flip, which is the state the
    --after-flip mode reads."""
    subm = {"_REPO_URL": clone_url(repo, provider), "_WORKFLOW": "image",
            "_SECRET_ENVS": "GITHUB_TOKEN", "_SLUG": slug(provider)}
    b = {"id": "b-" + repo.split("/")[-1], "status": "SUCCESS",
         "startTime": "2026-11-20T10:00:00Z", "finishTime": "2026-11-20T10:05:00Z",
         "serviceAccount": "projects/fugaro-dev/serviceAccounts/" + account(provider),
         "substitutions": subm,
         "steps": [{"name": "gcr.io/cloud-builders/docker"}]}
    if mode != "build-no-url":
        subm["_CREDENTIALS_SERVICE_URL"] = SVC_URL
    mounts = 0 if FLIP else 1
    if mode == "flip-build-mounts" and provider == "github":
        mounts = 1
    if mounts:
        b["availableSecrets"] = {"secretManager": [
            {"versionName": "projects/fugaro-dev/secrets/%s/versions/latest" % slug(provider),
             "env": "GITHUB_TOKEN"}]}
        b["steps"][0]["secretEnv"] = ["GITHUB_TOKEN"]
    if mode in ("build-failed", "flip-build-failed") and provider == "github":
        b["status"] = "FAILURE"
    if mode == "build-other-account" and provider == "github":
        b["serviceAccount"] = "projects/fugaro-dev/serviceAccounts/other-builder@fugaro-dev.iam.gserviceaccount.com"
    return b


def builds():
    out = []
    if mode != "no-gh-build":
        out.append(build(GH_REPO, "github"))
    if mode != "no-bb-build":
        out.append(build(BB_REPO, "bitbucket"))
    return out


def job(repo, provider):
    env = [{"name": "FUGARO_REMOTE", "value": clone_url(repo, provider)}]
    if mode != "check-job-no-url":
        env.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL", "value": SVC_URL})
    mounts = 0 if FLIP else 1
    if mode == "flip-job-mounts" and provider == "github":
        mounts = 1
    if mounts:
        env.append({"name": "GITHUB_TOKEN",
                    "valueSource": {"secretKeyRef": {"secret": slug(provider), "name": "GITHUB_TOKEN"}}})
    return {
        "metadata": {"name": "fugarochk-%s-%s" % (slug(provider), provider),
                     "labels": {"fugaro_repo": slug(provider)}},
        "spec": {"template": {"spec": {"template": {"spec": {
            "serviceAccountName": account(provider),
            "containers": [{"name": "check", "env": env}],
        }}}}} ,
    }


def jobs():
    out = []
    if mode != "no-check-job":
        out.append(job(GH_REPO, "github"))
    out.append(job(BB_REPO, "bitbucket"))
    return out


def audit_line(provider):
    cred = "github-app-key" if provider == "github" else "bitbucket-token--ci"
    if mode == "audit-wrong-credential" and provider == "github":
        cred = "bitbucket-token"
    caller = account(provider)
    if mode == "audit-other-caller" and provider == "github":
        caller = "somebody-else@fugaro-dev.iam.gserviceaccount.com"
    return {"jsonPayload": {"msg": "credsvc: audit", "status": "ok", "credential": cred,
                            "purpose": "build", "caller": caller, "repository": slug(provider),
                            "run": "", "job": "", "tier": "", "slip": "", "owner": ""}}


def service_policy():
    members = ["serviceAccount:" + GH_ACCT, "serviceAccount:" + BB_ACCT]
    if mode == "public-invoker":
        members.append("allUsers")
    return {"bindings": [{"role": "roles/run.invoker", "members": members}], "etag": "BwYfake="}


if sub == ("builds", "list"):
    if mode == "builds-list-fails":
        die("cloudbuild.builds.list was refused: the session has expired")
    emit(builds())
elif sub == ("run", "jobs", "list"):
    if mode == "jobs-list-fails":
        die("run.jobs.list was refused: the session has expired")
    emit(jobs())
elif sub == ("run", "jobs", "executions", "list"):
    if mode == "exec-fails":
        die("run.jobs.executions.list was refused")
    failed = mode == "check-exec-failed" and GH_SLUG in flag("--job")
    emit([{"metadata": {"name": flag("--job") + "-abc", "creationTimestamp": "2026-11-20T11:00:00Z"},
          "status": {"conditions": [{"type": "Completed", "status": "False" if failed else "True"}]}}])
elif sub == ("logging", "read"):
    if mode == "log-fails":
        die("logging.logEntries.list was refused: the session has expired")
    if mode == "log-missing":
        emit([])
    elif mode == "audit-refused":
        # A refusal, with a credential-shaped sentence in it: the block must
        # carry the service's own words redacted, never the token.
        emit([{"jsonPayload": {"msg": "credsvc: audit event refused", "field": "repository",
                               "reason": "Authorization: Bearer FAKE-SERVICE-LOG-TOKEN",
                               "purpose": "build"}}])
    elif BB_SLUG in " ".join(positionals):
        emit([audit_line("bitbucket")])
    else:
        emit([audit_line("github")])
elif sub == ("run", "services", "get-iam-policy"):
    if mode == "svc-iam-fails":
        die("run.services.getIamPolicy was refused")
    emit(service_policy())
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ------------------------------------------------------------------- harness
status=0
out=""
err=""
scratch=""
run_args=()
run_env=()

run196() { # dir mode — the script's own arguments are in run_args, extra env in run_env
  local dir=$1 mode=$2
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  # CHECK196_TEST_XTRACE starts the shell the way an owner might by habit: with
  # tracing on, which is the one leak this check's own redaction cannot catch.
  local -a runner=(bash)
  if [ -n "${CHECK196_TEST_XTRACE:-}" ]; then runner=(bash -x); fi
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_MODE="$mode" \
    CHECK196_REPO=acme/sandbox CHECK196_BITBUCKET=acme/bb-sandbox \
    CHECK196_LOG_WAIT_SECONDS=0 CHECK196_LOG_POLL_SECONDS=0 \
    ${run_env[@]+"${run_env[@]}"} \
    "${runner[@]}" "$script" ${run_args[@]+"${run_args[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  run_args=()
  run_env=()
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
}

# --------------------------------------------------------------- the tests
# --dry-run is the mode the checklist has the owner try FIRST, on a machine that
# may have no credentials at all: it must print the plan and call NOTHING.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_env=(CHECK196_DRY_RUN=1)
  run196 "$dir" ok
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 196 RESULT" "dry run: the block"
  expect_contains "$err" "who may invoke the service" "dry run: the plan"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row"
  # The guards that read the cloud are NOT run in this mode -- that is what makes
  # it safe on a machine with no credentials -- so their rows say the run did not
  # reach them rather than claiming a state nobody read.
  expect_contains "$out" "FACT service: FAIL — not reached" "dry run: a cloud row is not reached"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "dry run: gcloud was called"; fi
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "dry run: fugaro was called"; fi
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory survived"; fi
}

test_happy_path() {
  local dir=$work/ok
  prepare "$dir"
  run196 "$dir" ok
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT build-github: PASS" "happy path: the GitHub build"
  expect_contains "$out" "FACT build-bitbucket: PASS" "happy path: the Bitbucket build"
  expect_contains "$out" "FACT mounts-github: PASS" "happy path: the pre-flip mounts"
  expect_contains "$out" "FACT served-github: PASS" "happy path: the service's own line"
  expect_contains "$out" "FACT served-bitbucket: PASS" "happy path: the Bitbucket line"
  expect_contains "$out" "FACT check-github: PASS" "happy path: the daily check"
  expect_contains "$out" "FACT check-bitbucket: PASS" "happy path: the Bitbucket daily check"
  expect_contains "$out" "FACT boundary: PASS" "happy path: the front door"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: cleanup"
  expect_absent "$out" "FAIL" "happy path: no row failed"
  # The Bitbucket line names a token of the family, not the legacy spelling: the
  # reading takes the family, so a typed token is not read as a wrong one.
  expect_contains "$out" "bitbucket-token--ci" "happy path: the token family"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

test_a_guard_refuses_a_missing_knob() {
  local dir=$work/noknob
  prepare "$dir"
  run_env=(CHECK196_BITBUCKET=)
  run196 "$dir" ok
  expect_status "$status" 1 "missing knob: the verdict"
  expect_contains "$out" "FACT aborted: FAIL" "missing knob: the row"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "missing knob: gcloud was called before the guard"; fi
}

test_a_guard_refuses_belong() {
  local dir=$work/belong
  prepare "$dir"
  run_env=(CHECK196_REPO=belong/sandbox)
  run196 "$dir" ok
  expect_status "$status" 1 "belong: the verdict"
  expect_contains "$out" "never belong" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "belong" "belong: no command named it"
}

test_a_guard_refuses_one_repository_twice() {
  local dir=$work/twice
  prepare "$dir"
  run_env=(CHECK196_BITBUCKET=acme/sandbox)
  run196 "$dir" ok
  expect_status "$status" 1 "twice: the verdict"
  expect_contains "$out" "name the same repository" "twice: the refusal"
}

test_the_project_guard_stops_on_another_project() {
  local dir=$work/otherproj
  prepare "$dir"
  run_env=(FAKE_FUGARO_MODE=other)
  run196 "$dir" ok
  expect_status "$status" 1 "another project: the verdict"
  expect_contains "$out" "FACT aborted: FAIL" "another project: the row"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "another project: gcloud was called before the guard"; fi
}

test_the_project_guard_reads_the_line_not_the_status() {
  # doctor exits 1 with a failed check of its own and still prints the project
  # line: the check must read the line, or a machine with any failing doctor
  # check would stop every run of this one.
  local dir=$work/doctorfail
  prepare "$dir"
  run_env=(FAKE_FUGARO_MODE=failing)
  run196 "$dir" ok
  expect_status "$status" 0 "doctor's status: the verdict"
  expect_contains "$out" "FACT project-region: PASS" "doctor's status: the row"
}

test_a_development_version_is_a_failure() {
  local dir=$work/devver
  prepare "$dir"
  run_env=(FAKE_FUGARO_MODE=dev-version)
  run196 "$dir" ok
  expect_status "$status" 1 "dev version: the verdict"
  expect_contains "$out" "FACT fugaro-version: FAIL" "dev version: the row"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  CHECK196_TEST_XTRACE=1 run196 "$dir" ok
  expect_status "$status" 1 "xtrace: the verdict"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_absent "$out" "gcloud builds list" "xtrace: no command was traced into the block"
  if [ -d "$scratch" ]; then fail "xtrace: the scratch directory survived"; fi
}

test_a_repository_with_no_build_is_unmeasured() {
  local dir=$work/nobuild
  prepare "$dir"
  run196 "$dir" no-bb-build
  expect_status "$status" 2 "no build: the verdict"
  expect_contains "$out" "FACT build-bitbucket: FAIL — no build of acme/bb-sandbox" "no build: the row"
  expect_contains "$out" "FACT served-bitbucket: FAIL" "no build: the served row"
  expect_contains "$out" "FACT check-bitbucket: FAIL" "no build: the check row"
  # And the GitHub half is still read: one repository's silence does not stop
  # the other's rows.
  expect_contains "$out" "FACT build-github: PASS" "no build: the other repository"
}

test_a_build_that_failed_is_a_failure() {
  local dir=$work/buildfailed
  prepare "$dir"
  run196 "$dir" build-failed
  expect_status "$status" 1 "failed build: the verdict"
  expect_contains "$out" "FACT build-github: FAIL" "failed build: the row"
  expect_contains "$out" "ended FAILURE" "failed build: the status is named"
}

test_a_build_that_names_no_service_is_a_failure() {
  local dir=$work/nourl
  prepare "$dir"
  run196 "$dir" build-no-url
  expect_status "$status" 1 "no service address: the verdict"
  expect_contains "$out" "FACT service: FAIL" "no service address: the row"
  expect_contains "$out" "FACT build-github: FAIL" "no service address: the build row"
  expect_contains "$out" "FACT mounts-github: FAIL" "no service address: the mount row is not read as a pass"
}

test_a_build_that_ran_as_another_account_is_a_failure() {
  local dir=$work/otheracct
  prepare "$dir"
  run196 "$dir" build-other-account
  expect_status "$status" 1 "another account: the verdict"
  expect_contains "$out" "FACT build-github: FAIL" "another account: the row"
  expect_contains "$out" "not as a build account of fugaro-dev" "another account: the reason"
}

test_a_log_read_that_fails_is_unmeasured() {
  local dir=$work/logfails
  prepare "$dir"
  run196 "$dir" log-fails
  expect_status "$status" 2 "unreadable log: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "unreadable log: the row"
  expect_contains "$out" "could not be read" "unreadable log: the reason"
}

test_a_missing_audit_line_is_a_failure() {
  local dir=$work/nolog
  prepare "$dir"
  run196 "$dir" log-missing
  expect_status "$status" 1 "missing line: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "missing line: the row"
  expect_contains "$out" "wrote no 'credsvc: audit' line" "missing line: the reason"
}

test_an_audit_line_for_another_caller_is_a_failure() {
  local dir=$work/othercaller
  prepare "$dir"
  run196 "$dir" audit-other-caller
  expect_status "$status" 1 "another caller: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "another caller: the row"
  expect_contains "$out" "names the caller" "another caller: the reason"
}

test_the_other_providers_credential_is_a_failure() {
  local dir=$work/wrongcred
  prepare "$dir"
  run196 "$dir" audit-wrong-credential
  expect_status "$status" 1 "wrong credential: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "wrong credential: the row"
  expect_contains "$out" "not the github build's own git credential" "wrong credential: the reason"
}

test_a_refusal_line_is_reported_redacted() {
  local dir=$work/refused
  prepare "$dir"
  run196 "$dir" audit-refused
  expect_status "$status" 1 "refusal: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "refusal: the row"
  expect_contains "$out" "audit event refused" "refusal: the service's own words"
  expect_absent "$out" "FAKE-SERVICE-LOG-TOKEN" "refusal: the token never reaches the block"
  expect_contains "$out" "<redacted>" "refusal: it is redacted instead"
}

test_a_missing_check_job_is_unmeasured() {
  local dir=$work/nojob
  prepare "$dir"
  run196 "$dir" no-check-job
  expect_status "$status" 2 "no check job: the verdict"
  expect_contains "$out" "FACT check-github: FAIL — this installation deploys no daily check job" "no check job: the row"
  expect_contains "$out" "FACT check-bitbucket: PASS" "no check job: the other repository"
}

test_a_check_job_without_the_service_is_a_failure() {
  local dir=$work/jobnourl
  prepare "$dir"
  run196 "$dir" check-job-no-url
  expect_status "$status" 1 "check job without the service: the verdict"
  expect_contains "$out" "FACT check-github: FAIL" "check job without the service: the row"
  expect_contains "$out" "carries no FUGARO_CREDENTIALS_SERVICE_URL" "check job without the service: the reason"
}

test_a_check_execution_that_failed_is_a_failure() {
  local dir=$work/execfailed
  prepare "$dir"
  run196 "$dir" check-exec-failed
  expect_status "$status" 1 "failed execution: the verdict"
  expect_contains "$out" "FACT check-github: FAIL" "failed execution: the row"
  expect_contains "$out" "did not succeed" "failed execution: the reason"
}

test_a_public_service_policy_is_a_failure() {
  local dir=$work/public
  prepare "$dir"
  run196 "$dir" public-invoker
  expect_status "$status" 1 "public invoker: the verdict"
  expect_contains "$out" "FACT boundary: FAIL" "public invoker: the row"
  expect_contains "$out" "allUsers" "public invoker: it is named"
}

test_a_builds_list_that_cannot_be_read_stops_the_run() {
  local dir=$work/nobuildlist
  prepare "$dir"
  run196 "$dir" builds-list-fails
  expect_status "$status" 1 "unreadable builds: the verdict"
  expect_contains "$out" "FACT aborted: FAIL" "unreadable builds: the row"
  expect_contains "$out" "cannot be read" "unreadable builds: the reason"
}

test_a_jobs_list_that_cannot_be_read_stops_the_run() {
  local dir=$work/nojoblist
  prepare "$dir"
  run196 "$dir" jobs-list-fails
  expect_status "$status" 1 "unreadable jobs: the verdict"
  expect_contains "$out" "FACT aborted: FAIL" "unreadable jobs: the row"
  expect_contains "$out" "Cloud Run jobs cannot be read" "unreadable jobs: the reason"
}

test_the_flip_happy_path() {
  local dir=$work/flip
  prepare "$dir"
  run_args=(--after-flip)
  run196 "$dir" flip
  expect_status "$status" 0 "flip: the verdict"
  expect_contains "$out" "FACT noavailable-github: PASS" "flip: no availableSecrets"
  expect_contains "$out" "FACT build-flip-github: PASS" "flip: the build still succeeds"
  expect_contains "$out" "FACT served-github-flip: PASS" "flip: the service's own line"
  expect_contains "$out" "FACT noaccessor-github: PASS" "flip: the check job's mounts are gone"
  expect_contains "$out" "FACT noavailable-bitbucket: PASS" "flip: the Bitbucket half"
  expect_absent "$out" "FAIL" "flip: no row failed"
  # The flip's block is its own shape: the switch mode's rows are not printed as
  # `not reached`, which would read as a reading this mode never takes.
  expect_absent "$out" "FACT mounts-github" "flip: no switch-mode row"
}

test_the_flip_with_a_mount_left_is_a_failure() {
  local dir=$work/flipmount
  prepare "$dir"
  run_args=(--after-flip)
  run196 "$dir" flip-build-mounts
  expect_status "$status" 1 "flip with a mount: the verdict"
  expect_contains "$out" "FACT noavailable-github: FAIL" "flip with a mount: the row"
  expect_contains "$out" "still carries 1 availableSecrets" "flip with a mount: the reason"
}

test_the_flip_with_the_check_job_mount_left_is_a_failure() {
  local dir=$work/flipjobmount
  prepare "$dir"
  run_args=(--after-flip)
  run196 "$dir" flip-job-mounts
  expect_status "$status" 1 "flip with a job mount: the verdict"
  expect_contains "$out" "FACT noaccessor-github: FAIL" "flip with a job mount: the row"
  expect_contains "$out" "still mounts 1 Secret Manager container" "flip with a job mount: the reason"
}

test_keep_scratch_keeps_the_directory() {
  local dir=$work/keep
  prepare "$dir"
  run_env=(CHECK196_KEEP_SCRATCH=1)
  run196 "$dir" ok
  expect_status "$status" 0 "keep scratch: the verdict"
  expect_contains "$out" "FACT scratch-removed: PASS — kept on request" "keep scratch: the row"
  if [ -z "$scratch" ] || [ ! -d "$scratch" ]; then
    fail "keep scratch: the directory the knob asks for is not there"
  else
    kept+=("$scratch")
  fi
}

test_every_advertised_knob_is_read() {
  local knob
  for knob in CHECK196_REPO CHECK196_BITBUCKET CHECK196_BUILDS CHECK196_LOG_AGE \
    CHECK196_LOG_LIMIT CHECK196_LOG_WAIT_SECONDS CHECK196_LOG_POLL_SECONDS \
    CHECK196_DRY_RUN CHECK196_KEEP_SCRATCH; do
    grep -q "\${${knob}:-" "$script" ||
      fail "knobs: $knob is documented in the header and not read"
  done
  # And the header documents every one of them, so a knob cannot be read without
  # being named where the owner reads how to set it.
  for knob in CHECK196_REPO CHECK196_BITBUCKET CHECK196_BUILDS CHECK196_DRY_RUN CHECK196_KEEP_SCRATCH; do
    expect_contains "$(sed -n '1,120p' "$script")" "$knob" "knobs: $knob is documented"
  done
}

test_never_belong() {
  # The project and the region are constants in the script and not inputs:
  # `P=${...}` would be the shape that lets an environment point this check at
  # another installation.
  local assignment
  assignment=$(grep -c '^P=fugaro-dev$' "$script" || true)
  [ "$assignment" = 1 ] || fail "never belong: the project must be the one constant assignment P=fugaro-dev, and the script has $assignment of them"
  expect_absent "$(cat "$script")" 'P=${' "never belong: the project is not read from the environment"
  expect_absent "$(cat "$script")" 'L=${' "never belong: the region is not read from the environment"

  local dir=$work/oneproj
  prepare "$dir"
  run196 "$dir" ok
  local projects p all
  all=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls")
  expect_absent "$all" "belong" "never belong: no command named it"
  projects=$(grep -o -- '--project=[^ ]*' "$dir/logs/gcloud.calls" | sort -u || true)
  [ -n "$projects" ] || fail "never belong: no gcloud --project argument was recorded, so this test checks nothing"
  for p in $projects; do
    [ "$p" = "--project=fugaro-dev" ] || fail "never belong: a gcloud command named $p"
  done
}

test_nothing_secret_shaped_reaches_the_block() {
  local dir=$work/redact
  prepare "$dir"
  run196 "$dir" audit-refused
  # The check holds no credential of its own and writes nothing secret into its
  # block: the only lines it prints from a cloud are the service's own, redacted.
  expect_absent "$out" "Bearer FAKE" "redaction: a bearer value never reaches the block"
  expect_contains "$out" "RESULT END (exit" "redaction: the block closed"
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
t test_happy_path
t test_a_guard_refuses_a_missing_knob
t test_a_guard_refuses_belong
t test_a_guard_refuses_one_repository_twice
t test_the_project_guard_stops_on_another_project
t test_the_project_guard_reads_the_line_not_the_status
t test_a_development_version_is_a_failure
t test_xtrace_is_refused
t test_a_repository_with_no_build_is_unmeasured
t test_a_build_that_failed_is_a_failure
t test_a_build_that_names_no_service_is_a_failure
t test_a_build_that_ran_as_another_account_is_a_failure
t test_a_log_read_that_fails_is_unmeasured
t test_a_missing_audit_line_is_a_failure
t test_an_audit_line_for_another_caller_is_a_failure
t test_the_other_providers_credential_is_a_failure
t test_a_refusal_line_is_reported_redacted
t test_a_missing_check_job_is_unmeasured
t test_a_check_job_without_the_service_is_a_failure
t test_a_check_execution_that_failed_is_a_failure
t test_a_public_service_policy_is_a_failure
t test_a_builds_list_that_cannot_be_read_stops_the_run
t test_a_jobs_list_that_cannot_be_read_stops_the_run
t test_the_flip_happy_path
t test_the_flip_with_a_mount_left_is_a_failure
t test_the_flip_with_the_check_job_mount_left_is_a_failure
t test_keep_scratch_keeps_the_directory
t test_every_advertised_knob_is_read
t test_never_belong
t test_nothing_secret_shaped_reaches_the_block

if [ "$failures" -ne 0 ]; then
  echo "check196_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check196_test.sh: ok"
