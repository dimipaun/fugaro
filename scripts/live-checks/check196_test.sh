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
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$here/lib}
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
. "$LIVE_CHECK_LIB/strict.sh"
strict_fugaro_verify "$@" || exit $?
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
# The variable the check job's own mount of the git credential lands in
# (config.ReservedSecrets, read off the provider's git secret).
GIT_ENV = {"github": "FUGARO_GITHUB_APP_PRIVATE_KEY", "bitbucket": "FUGARO_BITBUCKET_TOKEN"}
# A CATALOG build is not a repository's build at all: it runs as the
# installation's ONE project-wide build identity (gcp.BuildIdentityAccountID,
# B6/D2301), and it clones with the provider git credential its own request
# mounts as availableSecrets, under the env names internal/backend/gcp/build.go
# gives (`credentialSecretEnv`, a BUILD's names and not a job's).
BUILD_IDENTITY = "fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com"
MINT_ENV = {"github": "GITHUB_APP_KEY", "bitbucket": "GIT_TOKEN"}
CATALOG_ENTRY = {"github": "sandbox-web", "bitbucket": "sandbox-bb"}
CATALOG_IMAGE = "us-east5-docker.pkg.dev/fugaro-dev/fugaro-shared-images/catalog-%s"


def flag(name, default=""):
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


# Like the real CLI, a flag a subcommand does not have is an error: the
# subcommands this check may call, and the flags each may pass, are the short
# allow-list kept beside this test (scripts/live-checks/lib/allow/
# check196.gcloud), so a misspelled or invented flag cannot hide in any call.
BASE_FLAGS = {"--help", "-h", "--quiet", "--project", "--format", "--account",
              "--verbosity", "--log-http"}
ALLOWED = {}
ALLOWED_FLAGS = set()
with open(os.path.join(os.environ["LIVE_CHECK_LIB"], "allow", "check196.gcloud")) as fh:
    for line in fh:
        line = line.rstrip("\n")
        if not line or line.startswith("#"):
            continue
        kind, _, rest = line.partition("\t")
        if kind == "cmd":
            ALLOWED[tuple(rest.split())] = True
        elif kind == "flag":
            ALLOWED_FLAGS.add(rest.strip())
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
    if a.startswith("--") and a.split("=")[0] not in ALLOWED_FLAGS | BASE_FLAGS:
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
    """The CATALOG-ENTRY build of one repository, as it really is since D1900
    (repository-level derived images are disabled): the image the repository's
    image_map names, submitted by `fugaro image build <entry>` or by `fugaro
    apply`'s owed catalog step, run as the installation's build identity, with
    the provider git credential the request itself mounts, and with
    `_CREDENTIALS_SERVICE_URL` EMPTY (B4.5: a catalog build's identity is not
    one the build-credentials route admits, so it fetches nothing from the
    service). No `_SLUG`, no `_WORKFLOW`, no `_SECRET_ENVS` -- a catalog request
    carries none of them."""
    entry = CATALOG_ENTRY[provider]
    subm = {"_REPO_URL": clone_url(repo, provider), "_IMAGE": CATALOG_IMAGE % entry,
            "_CATALOG_TAG": "9f2c" + provider, "_WORKFLOW": "", "_SECRET_ENVS": "",
            "_CREDENTIALS_SERVICE_URL": ""}
    b = {"id": "b-" + repo.split("/")[-1], "status": "SUCCESS",
         "startTime": "2026-11-20T10:00:00Z", "finishTime": "2026-11-20T10:05:00Z",
         "serviceAccount": "projects/fugaro-dev/serviceAccounts/" + BUILD_IDENTITY,
         "substitutions": subm,
         "steps": [{"name": "gcr.io/cloud-builders/docker"}]}
    mounts = 1
    if mode == "catalog-build-no-mount" and provider == "github":
        mounts = 0
    if mounts:
        b["availableSecrets"] = {"secretManager": [
            {"versionName": "projects/fugaro-dev/secrets/%s/versions/latest" % slug(provider),
             "env": MINT_ENV[provider]}]}
        b["steps"][0]["secretEnv"] = [MINT_ENV[provider]]
    if mode in ("build-failed", "flip-build-failed") and provider == "github":
        b["status"] = "FAILURE"
    if mode == "build-other-account" and provider == "github":
        b["serviceAccount"] = "projects/fugaro-dev/serviceAccounts/other-builder@fugaro-dev.iam.gserviceaccount.com"
    return b


def derived_build(repo, provider):
    """A repository-level DERIVED build: the shape this check read until
    2026-11-24, and one nothing builds any more (D1900). It must never be read
    as the repository's image build: its `_IMAGE` names the repository and the
    workflow, not a catalog entry, so it is not the catalog entry the
    repository's image_map names and this check cannot measure it."""
    return {"id": "old-" + repo.split("/")[-1], "status": "SUCCESS",
            "startTime": "2026-11-19T10:00:00Z", "finishTime": "2026-11-19T10:05:00Z",
            "serviceAccount": "projects/fugaro-dev/serviceAccounts/" + account(provider),
            "substitutions": {"_REPO_URL": clone_url(repo, provider), "_WORKFLOW": "image",
                              "_SECRET_ENVS": "GITHUB_TOKEN", "_SLUG": slug(provider),
                              "_IMAGE": "us-east5-docker.pkg.dev/fugaro-dev/fugaro-registry/img-%s" % slug(provider),
                              "_CREDENTIALS_SERVICE_URL": SVC_URL},
            "steps": [{"name": "gcr.io/cloud-builders/docker"}]}


def builds():
    if mode == "no-builds":
        return []
    if mode == "derived-only":
        return [derived_build(GH_REPO, "github"), derived_build(BB_REPO, "bitbucket")]
    out = []
    if mode != "no-gh-build":
        out.append(build(GH_REPO, "github"))
    if mode != "no-bb-build":
        out.append(build(BB_REPO, "bitbucket"))
    return out


def job(repo, provider):
    """The repository's daily check job, as `gcloud run jobs list --format=json`
    reports it. Its own credential shape is the one internal/infra/spec.go writes
    (checkMountServed): a repository the service does not yet serve for its BUILDS
    keeps the mount of its git credential and carries NO service address, and one
    it serves mounts nothing and is handed the address instead. The two never
    appear together — the address is written in the same breath as the mount's
    removal — so the default shapes are those two and nothing else."""
    env = [] if mode == "check-job-label-only" else [{"name": "FUGARO_REMOTE", "value": clone_url(repo, provider)}]
    mounts, url = (0, True) if FLIP else (1, False)
    if mode == "flip-job-mounts" and provider == "github":
        mounts = 1
    if mode == "check-job-url" and provider == "github":
        mounts, url = 0, True
    if mode == "check-job-mount-and-url" and provider == "github":
        mounts, url = 1, True
    if mode in ("check-job-no-credential", "flip-job-no-credential") and provider == "github":
        mounts, url = 0, False
    if url:
        env.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL", "value": SVC_URL})
    if mounts:
        env.append({"name": GIT_ENV[provider],
                    "valueSource": {"secretKeyRef": {"secret": slug(provider), "name": GIT_ENV[provider]}}})
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


def service():
    """The credentials service ITSELF, as `gcloud run services describe` reports
    it: the row is read off the service and off no build, so an installation
    whose project holds no catalog build at all still measures it."""
    if mode == "service-missing":
        return None
    doc = {"metadata": {"name": "fugaro-credentials"},
           "spec": {"template": {"spec": {"containers": [{"name": "svc"}]}}}}
    if mode != "service-no-url":
        doc["status"] = {"url": SVC_URL}
    return doc


def audit_line(provider):
    cred = "github-app-key" if provider == "github" else "bitbucket-token--ci"
    if mode == "audit-wrong-credential" and provider == "github":
        cred = "bitbucket-token"
    # A line under this repository's slug can only have been written by the
    # identity the service verified (G20): with the repository's image now a
    # catalog entry's, that identity is the installation's build identity.
    caller = BUILD_IDENTITY
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
    if mode == "audit-refused":
        # A refusal, with a credential-shaped sentence in it: the block must
        # carry the service's own words redacted, never the token.
        emit([{"jsonPayload": {"msg": "credsvc: audit event refused", "field": "repository",
                               "reason": "Authorization: Bearer FAKE-SERVICE-LOG-TOKEN",
                               "purpose": "build"}}])
    elif mode in ("audit-other-caller", "audit-wrong-credential"):
        emit([audit_line("bitbucket" if BB_SLUG in " ".join(positionals) else "github")])
    else:
        # The default project's builds are CATALOG entries' and ask the service
        # for nothing (B4.5), so the service wrote no line of purpose `build`
        # for either repository: that is the shape the served row reads as INFO.
        emit([])
elif sub == ("run", "services", "get-iam-policy"):
    if mode == "svc-iam-fails":
        die("run.services.getIamPolicy was refused")
    emit(service_policy())
elif sub == ("run", "services", "describe"):
    if mode == "service-list-fails":
        die("run.services.get was refused: the session has expired")
    doc = service()
    if doc is None:
        die("NOT_FOUND: service fugaro-credentials was not found in region us-east5")
    emit(doc)
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
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" \
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

# The project this check reads now is one whose builds are CATALOG ENTRIES': the
# repository's own derived image is disabled (D1900), so the image each sandbox
# repository builds is the entry its image_map names, submitted as the project's
# build identity with the provider git credential its request mounts. Every row
# of the switch mode is measured, and the whole run ends 0.
test_happy_path() {
  local dir=$work/ok
  prepare "$dir"
  run196 "$dir" ok
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT service: PASS" "happy path: the service itself"
  expect_contains "$out" "FACT build-github: PASS" "happy path: the GitHub build"
  expect_contains "$out" "FACT build-bitbucket: PASS" "happy path: the Bitbucket build"
  # The build row says WHICH catalog entry the image is and which account it ran
  # as -- the two facts that tell a catalog build from a repository's own.
  expect_contains "$out" "CATALOG ENTRY sandbox-web" "happy path: the entry is named"
  expect_contains "$out" "CATALOG ENTRY sandbox-bb" "happy path: the Bitbucket entry"
  expect_contains "$out" "ran as fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com" "happy path: the build identity"
  expect_contains "$out" "FACT mounts-github: PASS" "happy path: the mounted credential"
  expect_contains "$out" "fed to the build step as GITHUB_APP_KEY" "happy path: the mount's env"
  expect_contains "$out" "fed to the build step as GIT_TOKEN" "happy path: the Bitbucket mount's env"
  # A catalog build asks the service for nothing, so the service wrote it no
  # line: the row reads that as INFO and says which shape it is.
  expect_contains "$out" "FACT served-github: INFO" "happy path: the service's own lines"
  expect_contains "$out" "FACT served-bitbucket: INFO" "happy path: the Bitbucket lines"
  expect_contains "$out" "catalog entry" "happy path: the reason"
  expect_contains "$out" "FACT check-github: PASS" "happy path: the daily check"
  expect_contains "$out" "FACT check-bitbucket: PASS" "happy path: the Bitbucket daily check"
  expect_contains "$out" "FACT boundary: PASS" "happy path: the front door"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: cleanup"
  expect_absent "$out" "FAIL" "happy path: no row failed"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

# The three project shapes the catalog path has to survive. (1) The project's
# only builds are catalog-entry builds: every build row is measured (asserted
# above). (2) The project holds no build at all. (3) The project's builds are
# the DERIVED ones nothing builds any more. The service row is measured in all
# three, because it is read off the service and off no build.
test_a_project_with_no_build_at_all() {
  local dir=$work/nobuilds
  prepare "$dir"
  run196 "$dir" no-builds
  expect_status "$status" 2 "no builds: the verdict"
  expect_contains "$out" "FACT service: PASS" "no builds: the service is still measured"
  expect_contains "$out" "FACT build-github: FAIL — no CATALOG-ENTRY build of acme/sandbox" "no builds: the row"
  expect_contains "$out" "FACT mounts-github: FAIL" "no builds: the mount row is not a pass"
  # The remedy names a command that works: 'fugaro image refresh' is refused
  # (repo-level images are disabled), so the row must not send the owner there.
  expect_contains "$out" "fugaro image build <entry>" "no builds: the remedy"
  expect_absent "$out" "Submit one with 'fugaro image refresh'" "no builds: the refused command is not the remedy"
  # The daily check is found by the repository itself, so it is measured too.
  expect_contains "$out" "FACT check-github: PASS" "no builds: the daily check"
}

test_a_project_whose_builds_are_derived_only() {
  # The shape this check read until 2026-11-24: builds that carry a `_SLUG` and
  # a service address and belong to a repository-level image no installation
  # builds any more. They are not the catalog entry the image_map names, so the
  # build rows are unmeasured rather than read as this repository's image.
  local dir=$work/derived
  prepare "$dir"
  run196 "$dir" derived-only
  expect_status "$status" 2 "derived only: the verdict"
  expect_contains "$out" "FACT build-github: FAIL — no CATALOG-ENTRY build of acme/sandbox" "derived only: the row"
  expect_contains "$out" "FACT service: PASS" "derived only: the service is still measured"
  expect_contains "$out" "fugaro image build <entry>" "derived only: the remedy"
}

test_a_service_that_cannot_be_read_is_unmeasured() {
  local dir=$work/svcmissing
  prepare "$dir"
  run196 "$dir" service-missing
  expect_status "$status" 2 "missing service: the verdict"
  expect_contains "$out" "FACT service: FAIL" "missing service: the row"
  expect_contains "$out" "credentials service" "missing service: the reason"
}

test_a_service_with_no_url_is_a_failure() {
  local dir=$work/svcnourl
  prepare "$dir"
  run196 "$dir" service-no-url
  expect_status "$status" 1 "service with no url: the verdict"
  expect_contains "$out" "FACT service: FAIL" "service with no url: the row"
  expect_contains "$out" "no URL" "service with no url: the reason"
}

test_the_check_job_is_found_by_its_label() {
  # The repository's slug is read off the `fugaro_repo` label the installation
  # writes, because a catalog build carries no `_SLUG` substitution to read one
  # out of: a job whose containers name no FUGARO_REMOTE is still found.
  local dir=$work/joblabel
  prepare "$dir"
  run196 "$dir" check-job-label-only
  expect_status "$status" 0 "job by label: the verdict"
  expect_contains "$out" "FACT check-github: PASS" "job by label: the row"
  expect_contains "$out" "slug acme-sandbox-0123456789abcdef" "job by label: the slug"
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
  expect_contains "$out" "FACT build-bitbucket: FAIL — no CATALOG-ENTRY build of acme/bb-sandbox" "no build: the row"
  expect_contains "$out" "FACT mounts-bitbucket: FAIL" "no build: the mount row"
  expect_contains "$out" "FACT served-bitbucket: FAIL" "no build: the served row"
  # And the GitHub half is still read, and so is the Bitbucket daily check: the
  # check job is found by the repository itself and not through a build.
  expect_contains "$out" "FACT build-github: PASS" "no build: the other repository"
  expect_contains "$out" "FACT check-bitbucket: PASS" "no build: the daily check is still read"
}

test_a_build_that_failed_is_a_failure() {
  local dir=$work/buildfailed
  prepare "$dir"
  run196 "$dir" build-failed
  expect_status "$status" 1 "failed build: the verdict"
  expect_contains "$out" "FACT build-github: FAIL" "failed build: the row"
  expect_contains "$out" "ended FAILURE" "failed build: the status is named"
}

# A catalog build clones with the credential its request MOUNTS, and a request
# that mounts none names no service either: it no credential at all and
# could only fail. That is the one shape the mount row calls a failure.
test_a_catalog_build_with_no_mount_is_a_failure() {
  local dir=$work/nomount
  prepare "$dir"
  run196 "$dir" catalog-build-no-mount
  expect_status "$status" 1 "no mount: the verdict"
  expect_contains "$out" "FACT mounts-github: FAIL" "no mount: the row"
  expect_contains "$out" "no credential at all" "no mount: the reason"
}

test_a_build_that_ran_as_another_account_is_a_failure() {
  local dir=$work/otheracct
  prepare "$dir"
  run196 "$dir" build-other-account
  expect_status "$status" 1 "another account: the verdict"
  expect_contains "$out" "FACT build-github: FAIL" "another account: the row"
  expect_contains "$out" "not as the installation's build identity" "another account: the reason"
}

test_a_log_read_that_fails_is_unmeasured() {
  local dir=$work/logfails
  prepare "$dir"
  run196 "$dir" log-fails
  expect_status "$status" 2 "unreadable log: the verdict"
  expect_contains "$out" "FACT served-github: FAIL" "unreadable log: the row"
  expect_contains "$out" "could not be read" "unreadable log: the reason"
}

# A CATALOG build asks the service for nothing, so the service writes it no
# line: the row says so and names it as the shape this installation plans rather
# than failing a correct installation (the same run is the happy path above).
test_a_catalog_build_with_no_service_line_reads_info() {
  local dir=$work/noline
  prepare "$dir"
  run196 "$dir" ok
  expect_status "$status" 0 "no line: the verdict"
  expect_contains "$out" "FACT served-github: INFO" "no line: the row"
  expect_contains "$out" "wrote no 'credsvc: audit' line" "no line: the reason"
  expect_absent "$out" "FACT served-github: FAIL" "no line: not a failure"
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

# Before the flip the check job keeps its own mount and carries NO service
# address (internal/infra/spec.go writes the address only where the mount has
# gone), so a job with neither the mount nor the address holds no credential at
# all and can only fail.
test_a_check_job_that_holds_no_credential_is_a_failure() {
  local dir=$work/jobnocred
  prepare "$dir"
  run196 "$dir" check-job-no-credential
  expect_status "$status" 1 "check job with no credential: the verdict"
  expect_contains "$out" "FACT check-github: FAIL" "check job with no credential: the row"
  expect_contains "$out" "neither a mount nor a service" "check job with no credential: the reason"
}

# The switch mode reads the pre-flip shape, so a job that already carries the
# address reads INFO — the flip has landed and --after-flip is the mode for it —
# and never as a failure of a correct installation.
test_a_check_job_that_already_names_the_service_reads_info() {
  local dir=$work/joburl
  prepare "$dir"
  run196 "$dir" check-job-url
  expect_status "$status" 0 "check job naming the service: the verdict"
  expect_contains "$out" "FACT check-github: INFO" "check job naming the service: the row"
  expect_contains "$out" "post-flip installation" "check job naming the service: the reason"
  expect_contains "$out" "FACT check-bitbucket: PASS" "check job naming the service: the other repository"
  expect_absent "$out" "FACT check-github: FAIL" "check job naming the service: not a failure"
}

# A mount beside the address is a shape internal/infra/spec.go never writes: the
# address arrives with the mount's removal, so the two together are the
# deployment and the spec having drifted apart.
test_a_check_job_that_mounts_beside_the_address_is_a_failure() {
  local dir=$work/jobboth
  prepare "$dir"
  run196 "$dir" check-job-mount-and-url
  expect_status "$status" 1 "check job with both: the verdict"
  expect_contains "$out" "FACT check-github: FAIL" "check job with both: the row"
  expect_contains "$out" "AND mounts 1 Secret Manager container" "check job with both: the reason"
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
  # The flip does not reach a catalog entry's build (it runs as the project's
  # build identity, which the build-credentials route does not admit, D2301), so
  # the mount is still there and the row says why that is right.
  expect_contains "$out" "FACT noavailable-github: PASS" "flip: the build's mount"
  expect_contains "$out" "B4.9's flip does not reach a catalog entry's build" "flip: the reason"
  expect_contains "$out" "FACT build-flip-github: PASS" "flip: the build still succeeds"
  expect_contains "$out" "FACT served-github-flip: INFO" "flip: the service's own line"
  expect_contains "$out" "FACT noaccessor-github: PASS" "flip: the check job's mounts are gone"
  expect_contains "$out" "FACT noavailable-bitbucket: PASS" "flip: the Bitbucket half"
  expect_absent "$out" "FAIL" "flip: no row failed"
  # The flip's block is its own shape: the switch mode's rows are not printed as
  # `not reached`, which would read as a reading this mode never takes.
  expect_absent "$out" "FACT mounts-github" "flip: no switch-mode row"
}

# A catalog build that carries no mount requests no secret and names no service
# either: it no credential at all, and no submitter of this installation
# writes such a request.
test_the_flip_with_a_build_that_holds_no_credential_is_a_failure() {
  local dir=$work/flipnocred
  prepare "$dir"
  run_args=(--after-flip)
  run196 "$dir" catalog-build-no-mount
  expect_status "$status" 1 "flip with no build credential: the verdict"
  expect_contains "$out" "FACT noavailable-github: FAIL" "flip with no build credential: the row"
  expect_contains "$out" "no credential at all" "flip with no build credential: the reason"
  expect_contains "$out" "fugaro image build sandbox-web" "flip with no build credential: the remedy names the entry"
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

# The flip drops the mount AND hands the job the address in the same breath
# (internal/infra/spec.go): a job that mounts nothing and names no service holds
# no credential at all, so the flip's own half has not landed on it.
test_the_flip_with_a_check_job_that_names_no_service_is_a_failure() {
  local dir=$work/flipjobnocred
  prepare "$dir"
  run_args=(--after-flip)
  run196 "$dir" flip-job-no-credential
  expect_status "$status" 1 "flip with no credential: the verdict"
  expect_contains "$out" "FACT noaccessor-github: FAIL" "flip with no credential: the row"
  expect_contains "$out" "mounts no Secret Manager container but carries no FUGARO_CREDENTIALS_SERVICE_URL" "flip with no credential: the reason"
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
t test_a_project_with_no_build_at_all
t test_a_project_whose_builds_are_derived_only
t test_a_service_that_cannot_be_read_is_unmeasured
t test_a_service_with_no_url_is_a_failure
t test_the_check_job_is_found_by_its_label
t test_a_guard_refuses_a_missing_knob
t test_a_guard_refuses_belong
t test_a_guard_refuses_one_repository_twice
t test_the_project_guard_stops_on_another_project
t test_the_project_guard_reads_the_line_not_the_status
t test_a_development_version_is_a_failure
t test_xtrace_is_refused
t test_a_repository_with_no_build_is_unmeasured
t test_a_build_that_failed_is_a_failure
t test_a_catalog_build_with_no_mount_is_a_failure
t test_a_build_that_ran_as_another_account_is_a_failure
t test_a_log_read_that_fails_is_unmeasured
t test_a_catalog_build_with_no_service_line_reads_info
t test_an_audit_line_for_another_caller_is_a_failure
t test_the_other_providers_credential_is_a_failure
t test_a_refusal_line_is_reported_redacted
t test_a_missing_check_job_is_unmeasured
t test_a_check_job_that_holds_no_credential_is_a_failure
t test_a_check_job_that_already_names_the_service_reads_info
t test_a_check_job_that_mounts_beside_the_address_is_a_failure
t test_a_check_execution_that_failed_is_a_failure
t test_a_public_service_policy_is_a_failure
t test_a_builds_list_that_cannot_be_read_stops_the_run
t test_a_jobs_list_that_cannot_be_read_stops_the_run
t test_the_flip_happy_path
t test_the_flip_with_a_build_that_holds_no_credential_is_a_failure
t test_the_flip_with_the_check_job_mount_left_is_a_failure
t test_the_flip_with_a_check_job_that_names_no_service_is_a_failure
t test_keep_scratch_keeps_the_directory
t test_every_advertised_knob_is_read
t test_never_belong
t test_nothing_secret_shaped_reaches_the_block

if [ "$failures" -ne 0 ]; then
  echo "check196_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check196_test.sh: ok"
