#!/usr/bin/env bash
# Tests for scripts/live-checks/check195.sh, with fake `gcloud`, `fugaro` and
# `curl` on PATH: NOTHING here reaches a cloud, a project or the network, no
# agent runs a live command, and the fake tools record every argument they are
# given so the test can assert that no command ever named anything but the
# sandbox installation's project, that the build was submitted with no source at
# all, and that no token-shaped thing ever reaches the block.
#
# The fakes are a small project: a service and an account that appear when they
# are created and are gone when they are deleted, an IAM policy that names what
# was granted to it, a build that ends the way its step's own line says, and a
# fake `curl` that refuses an unauthenticated request the way a Cloud Run service
# with IAM required does.
#
# Usage: bash scripts/live-checks/check195_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check195.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK195_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
# A stand-in for `fugaro`, for scripts/live-checks/check195.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
case "${1:-}" in
version) echo "fugaro 0.13.0 (test)" ;;
doctor)
  # Faithful to internal/cli/doctor.go's emitDoctor: the checks first, then the
  # project: line -- on stdout -- and exit 1 as soon as any check of its own
  # failed, whatever that line said. The check must read the line and not the
  # status, which this mode is what proves.
  case "${FAKE_FUGARO_MODE:-ok}" in
  ok) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  failing)
    echo "check: credentials FAIL (no usable credentials)"
    echo "project: fugaro (GCP fugaro-dev, us-east5)"
    exit 1
    ;;
  *) exit 1 ;;
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
"""A stand-in for `gcloud`, for scripts/live-checks/check195.sh's test only.

It keeps a little state in $FAKE_GCLOUD_STATE -- whether the service and the
account exist, what the service's and the project's IAM policies name, and how
the build ended -- so the guards, the grants, the build and the Restore see a
project that changes the way a real one would. It never talks to anything.
"""
import os, sys

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
CALLER = "fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com"


def flag(name, default=""):
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


def has(name):
    return name in args


def state_get(k):
    path = os.path.join(STATE, "gcloud." + k)
    if os.path.exists(path):
        return open(path).read().strip()
    return "0"


def state_set(k, v):
    with open(os.path.join(STATE, "gcloud." + k), "w") as fh:
        fh.write(v)


# Like the real CLI, a flag a subcommand does not have is an error. EVERY
# subcommand this script calls is listed with the flags it may pass; a call to a
# subcommand that is not here, or with a flag that is not in its list, fails
# with exit 2. A misspelled or invented flag therefore cannot hide in any call.
ALLOWED = {
    ("iam", "service-accounts", "list"): ("--project", "--filter", "--format"),
    ("iam", "service-accounts", "create"): ("--project", "--display-name", "--quiet"),
    ("iam", "service-accounts", "delete"): ("--project", "--quiet"),
    ("projects", "get-iam-policy"): ("--format",),
    ("projects", "add-iam-policy-binding"): ("--member", "--role", "--condition", "--quiet"),
    ("projects", "remove-iam-policy-binding"): ("--member", "--role", "--condition", "--quiet"),
    ("builds", "submit"): ("--project", "--no-source", "--config", "--service-account", "--async", "--format"),
    ("builds", "describe"): ("--project", "--format"),
    ("builds", "cancel"): ("--project", "--quiet"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
    ("run", "deploy"): ("--project", "--region", "--image", "--no-allow-unauthenticated", "--quiet"),
    ("run", "services", "list"): ("--project", "--region", "--filter", "--format"),
    ("run", "services", "describe"): ("--project", "--region", "--format"),
    ("run", "services", "delete"): ("--project", "--region", "--quiet"),
    ("run", "services", "get-iam-policy"): ("--project", "--region", "--format"),
    ("run", "services", "add-iam-policy-binding"): ("--project", "--region", "--member", "--role", "--quiet"),
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

SVC = "fugaro-chk195"


def emit(*lines):
    sys.stdout.write("\n".join(lines) + ("\n" if lines else ""))


def die(msg):
    sys.stderr.write("ERROR: (gcloud) %s\n" % msg)
    sys.exit(1)


# ---------------- the service and its own IAM policy
def service_members():
    if mode == "public-invoker":
        return ["allUsers"]
    member = state_get("invokergrant")
    return [member] if member and member != "0" else []


def policy_json(members):
    if not members:
        return '{"bindings": [], "etag": "BwYfake="}'
    body = ", ".join('"%s"' % m for m in members)
    return '{"bindings": [{"role": "roles/run.invoker", "members": [%s]}], "etag": "BwYfake="}' % body


if sub == ("run", "deploy"):
    if mode == "deploy-fails":
        die("run.services.create denied on resource")
    if not has("--no-allow-unauthenticated"):
        die("this check's service must be IAM-only")
    state_set("svc", "1")
elif sub == ("run", "services", "list"):
    if mode == "service-there" or state_get("svc") == "1":
        emit(SVC)
elif sub == ("run", "services", "describe"):
    if "status.url" in flag("--format"):
        emit("https://fugaro-chk195-fake-ue.a.run.app")
    else:
        die("unknown --format")
elif sub == ("run", "services", "get-iam-policy"):
    if state_get("svc") != "1":
        die("service not found")
    emit(policy_json(service_members()))
elif sub == ("run", "services", "add-iam-policy-binding"):
    if mode == "grant-fails":
        die("run.services.setIamPolicy denied on resource")
    if flag("--role") != "roles/run.invoker":
        die("this check grants roles/run.invoker on its own service and nothing else")
    if mode != "grant-ignored":
        state_set("invokergrant", flag("--member"))
elif sub == ("run", "services", "delete"):
    if mode == "service-delete-fails":
        die("run.services.delete failed")
    state_set("svc", "0")
    state_set("invokergrant", "0")

# ---------------- the throwaway account and the project's own IAM policy
elif sub == ("iam", "service-accounts", "list"):
    if mode == "account-there" or state_get("account") == "1":
        emit(CALLER)
elif sub == ("iam", "service-accounts", "create"):
    if mode == "account-create-fails":
        die("iam.serviceAccounts.create denied on resource")
    state_set("account", "1")
elif sub == ("iam", "service-accounts", "delete"):
    if mode == "account-delete-fails":
        die("iam.serviceAccounts.delete failed")
    state_set("account", "0")
elif sub == ("projects", "get-iam-policy"):
    member = state_get("loggrant")
    emit(policy_json(["serviceAccount:" + member] if member and member != "0" else []))
elif sub == ("projects", "add-iam-policy-binding"):
    if mode == "logwriter-fails":
        die("resourcemanager.projects.setIamPolicy denied on resource")
    if flag("--role") != "roles/logging.logWriter":
        die("this check adds roles/logging.logWriter on the project and nothing else")
    if mode != "logwriter-ignored":
        state_set("loggrant", flag("--member").split("serviceAccount:")[-1])
elif sub == ("projects", "remove-iam-policy-binding"):
    state_set("loggrant", "0")

# ---------------- the build
elif sub == ("builds", "submit"):
    if not has("--no-source"):
        # Without it gcloud stages a source directory, and the source directory
        # here would be the installation's own gs://<project>_cloudbuild.
        die("this check's build has no source: --no-source")
    cfg = flag("--config")
    if not cfg or not os.path.exists(cfg):
        die("Unable to read file [%s]" % cfg)
    if os.path.isdir(cfg):
        die("the build config must be a file")
    text = open(cfg).read()
    with open(os.path.join(LOG, "cloudbuild.yaml"), "w") as fh:
        fh.write(text)
    if "logging: CLOUD_LOGGING_ONLY" not in text:
        # The real API's refusal: a build that names its own service account
        # must say where its logs go, or it is rejected outright.
        die("INVALID_ARGUMENT: if 'build.service_account' is specified, the build must either "
            "(a) specify 'build.logs_bucket', (b) use the REGIONAL_USER_OWNED_BUCKET default logs "
            "bucket, or (c) use CLOUD_LOGGING_ONLY / NONE logging.")
    if flag("--service-account").split("/serviceAccounts/")[-1] != CALLER:
        die("this check's build runs as its own throwaway account")
    if mode == "build-submit-fails":
        die("the submit was refused")
    if mode in ("build-fails", "call-refused"):
        state_set("build", "FAILURE\tStep #0: the step exited with non-zero status: 1")
    else:
        state_set("build", "SUCCESS\t")
    state_set("buildsa", flag("--service-account").split("/serviceAccounts/")[-1])
    if has("--async"):
        emit("fake-build-1")
elif sub == ("builds", "describe"):
    if "serviceAccount" in flag("--format"):
        emit("somebody-else@fugaro-dev.iam.gserviceaccount.com" if mode == "build-as-another-account"
             else state_get("buildsa"))
    else:
        emit(state_get("build"))
elif sub == ("builds", "cancel"):
    pass
elif sub == ("logging", "read"):
    # The build's own lines, newest first, as Cloud Logging returns them. The
    # step prints ONE line -- the status the service answered and the token's
    # email claim -- and never the token; the second line here is one no step of
    # this check's writes, there so the test can see the check's excerpt redacts
    # a credential whatever its source.
    if mode == "log-missing":
        pass
    elif mode == "call-refused":
        emit("Step #0: CHK195 status=403 email=%s" % CALLER,
             "Step #0: Authorization: Bearer FAKE-BUILD-LOG-TOKEN")
    elif mode == "wrong-email":
        emit("Step #0: CHK195 status=200 email=somebody-else@fugaro-dev.iam.gserviceaccount.com")
    else:
        emit("Step #0: CHK195 status=200 email=%s" % CALLER)
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
"""A stand-in for `curl` (and for the Cloud Run front end behind it), for
scripts/live-checks/check195.sh's test only: no socket is opened.

It answers the check's one unauthenticated GET, and it refuses it the way a
service with IAM required does -- unless the mode says a project-level binding
made the service public, which is the case the service's own IAM policy cannot
show.
"""
import os, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_CURL_MODE", "ok")
args = sys.argv[1:]

with open(os.path.join(LOG, "curl.calls"), "a") as fh:
    fh.write("curl " + " ".join(args) + "\n")

def write_out():
    fmt = ""
    for i, a in enumerate(args):
        if a.startswith("-w=") or a.startswith("--write-out="):
            fmt = a.split("=", 1)[1]
        elif a in ("-w", "--write-out"):
            fmt = args[i + 1]
    return fmt


code = "200" if mode == "public" else "401"
fmt = write_out()
# curl substitutes %{http_code} in the format with the status it got; the check
# reads exactly that, so the fake substitutes it too rather than printing the
# format string back.
if fmt:
    sys.stdout.write(fmt.replace("%{http_code}", code))
sys.stdout.write("\n")
sys.exit(0)
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- harness
status=0
out=""
err=""
scratch=""

run195() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    CHECK195_GRANT_SECONDS=0 CHECK195_BUILD_WAIT_SECONDS=0 CHECK195_LOG_WAIT_SECONDS=0 \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run_copy() { # dir script extra-env...: the same run against a mutated copy
  local dir=$1 copy=$2
  shift 2
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    CHECK195_GRANT_SECONDS=0 CHECK195_BUILD_WAIT_SECONDS=0 CHECK195_LOG_WAIT_SECONDS=0 \
    ${1+"$@"} \
    bash "$copy" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
}

# --------------------------------------------------------------- the tests
# --dry-run is the mode the checklist has the owner try FIRST, on a machine that
# may have no credentials at all: it must print the plan and call NOTHING. Not
# even the installation's project is read, which is why the fugaro log is empty
# too -- guard_project is not reached.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run195 "$dir" CHECK195_DRY_RUN=1
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 195 RESULT" "dry run: the block"
  expect_contains "$out" "FACT image: PASS" "dry run: the container the service would run"
  expect_contains "$out" "FACT deploy-iam-only: PASS" "dry run: the deploy line's flag"
  # The guards that read the cloud are NOT run in this mode -- that is what makes
  # it safe on a machine with no credentials -- so their rows say the run did not
  # reach them rather than claiming a project nobody asked about. The project and
  # region are printed by the plan, in the commands it shows.
  expect_contains "$out" "FACT project-region: FAIL — not reached" "dry run: the cloud guards are not run"
  expect_contains "$err" "gcloud run deploy fugaro-chk195 --project=fugaro-dev --region=us-east5" "dry run: the plan carries the hard-coded project and region"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  expect_contains "$out" "FACT scratch-removed: PASS" "dry run: the scratch directory"
  expect_contains "$out" "CHECK 195 RESULT END (exit 0)" "dry run: the end line"
  # The plan has to say what the build's step does, because that step is the
  # thing the check exists for and the owner reads the plan to know what they are
  # about to run.
  expect_contains "$err" "metadata.google.internal" "dry run: the plan names the metadata server"
  expect_contains "$err" "--no-source" "dry run: the plan says the build has no source"
  expect_contains "$err" "roles/run.invoker" "dry run: the plan says what is granted"

  local calls
  calls=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls" "$dir/logs/curl.calls")
  if [ -n "$calls" ]; then
    fail "dry run: a tool was called: $(printf '%s' "$calls" | head -c 300)"
  fi
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

# doctor exits 1 whenever any check of its own failed -- billing, credentials,
# the update check -- and none of those has anything to do with which
# installation this is. The guard must read the project: LINE and not the status,
# or a check that failed for an unrelated reason would stop a run that should go.
test_the_project_guard_reads_the_line_not_doctors_status() {
  local dir=$work/doctorfails
  prepare "$dir"
  run195 "$dir" FAKE_FUGARO_MODE=failing
  expect_status "$status" 0 "doctor exit 1: the run goes on"
  expect_absent "$err" "not the fugaro-dev installation" "doctor exit 1: no stop"
  expect_contains "$out" "FACT call: PASS" "doctor exit 1: the check ran to the end"
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run195 "$dir" FAKE_FUGARO_MODE=other
  expect_status "$status" 1 "project guard: the verdict"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "create" "project guard: nothing created"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# THE RED STEP. The deploy line is the whole of the throwaway service's access
# control: a service deployed without --no-allow-unauthenticated answers anyone
# who finds its URL, and a 200 from it in the build's step would prove nothing
# about the grant this check is about. The guard must read the argv a real run
# would use, so the case is a COPY of the script with that one line taken out.
test_a_deploy_line_without_no_allow_unauthenticated_is_refused() {
  local dir=$work/publicinv
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed '/^  --no-allow-unauthenticated$/d' "$script" >"$dir/copy/check195.sh"
  run_copy "$dir" "$dir/copy/check195.sh"
  expect_status "$status" 1 "public invoker: the verdict"
  expect_contains "$err" "--no-allow-unauthenticated" "public invoker: the stop names the missing flag"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "run deploy" "public invoker: nothing was deployed"
  expect_absent "$calls" "create" "public invoker: nothing was created"
  expect_absent "$calls" "run services delete" "public invoker: nothing was deleted"
}

# The plan's own red step, and its wording: the service's IAM policy is read
# with get-iam-policy BEFORE anything is called, and a public member in it stops
# the run -- not a warning, and not something the check discovers from a 200.
test_a_public_iam_policy_stops_before_any_request() {
  local dir=$work/publiciam
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=public-invoker
  expect_status "$status" 1 "public IAM: the verdict"
  expect_contains "$err" "names a public member" "public IAM: the stop"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services get-iam-policy" "public IAM: the policy was read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "run.app" "public IAM: no request was sent"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "public IAM: no build was submitted"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "service-accounts create" "public IAM: no account was created"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete" "public IAM: the service is still deleted"
  expect_contains "$out" "FACT iam-policy:" "public IAM: the row is in the block"
}

# The other half of the same assertion, and the one the service's own policy
# cannot make: a project-level allUsers/run.invoker binding does not appear in
# get-iam-policy on the service, so an unauthenticated GET is the only thing that
# sees it. It must stop the run BEFORE the throwaway account's token is sent.
test_a_service_that_answers_unauthenticated_calls_stops_the_run() {
  local dir=$work/publiccurl
  prepare "$dir"
  run195 "$dir" FAKE_CURL_MODE=public
  expect_status "$status" 1 "public service: the verdict"
  expect_contains "$err" "answers unauthenticated calls" "public service: the stop"
  expect_contains "$out" "FACT unauthenticated-request: FAIL" "public service: the row"
  local curl_calls
  curl_calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$curl_calls" "-o /dev/null" "public service: the probe is the one request"
  expect_absent "$curl_calls" "-H " "public service: the probe carried no header at all"
  expect_absent "$curl_calls" "Authorization" "public service: the probe carried no credential"
  expect_contains "$curl_calls" "fugaro-chk195-fake-ue.a.run.app" "public service: the probe went to the service's own URL"
  # Nothing was created and no token was ever minted for this run.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "service-accounts create" "public service: no account was created"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "public service: no build was submitted"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete" "public service: the service is still deleted"
}

# A knob the header advertises and nothing reads is worse than no knob at all: a
# reader -- or a test -- sets it, gets no effect, and gets no error either.
test_every_advertised_knob_is_read() {
  local name reads knobs
  knobs=$(sed -n '1,/^set -euo pipefail/p' "$script" | grep -o 'CHECK195_[A-Z_]*' | sort -u)
  if [ -z "$knobs" ]; then
    fail "the header advertises no CHECK195_ knob at all, so this test reads nothing"
    return 0
  fi
  for name in $knobs; do
    # The READ, not a mention: every knob the header names appears in prose as
    # well, so counting mentions could never fire. `${NAME:-` is an expansion,
    # and an expansion cannot be a comment.
    reads=$(grep -cF "\${$name:-" "$script" || true)
    if [ "$reads" -lt 1 ]; then
      fail "$name is advertised in the header and read nowhere in the script (no \${$name:-...} expansion in it)"
    fi
  done
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run195 "$dir"
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT fugaro-version: PASS" "happy path: the version"
  expect_contains "$out" "FACT project-region: PASS" "happy path: the project"
  expect_contains "$out" "FACT baseline: PASS" "happy path: neither name was there"
  expect_contains "$out" "FACT service: PASS" "happy path: the deploy"
  expect_contains "$out" "FACT iam-policy: PASS" "happy path: the service's own policy"
  expect_contains "$out" "FACT unauthenticated-request: PASS — an unauthenticated GET on the service's own URL was refused with 401" "happy path: the probe"
  expect_contains "$out" "FACT invoker-grant: PASS" "happy path: the grant, read back"
  expect_contains "$out" "FACT build: PASS" "happy path: the build"
  expect_contains "$out" "FACT call: PASS" "happy path: the call"
  expect_contains "$out" "fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com" "happy path: the token's email claim is printed"
  expect_contains "$out" "FACT service-deleted: PASS" "happy path: the service is gone"
  expect_contains "$out" "FACT account-deleted: PASS" "happy path: the account is gone"
  expect_contains "$out" "FACT binding-removed: PASS" "happy path: the project binding is gone"
  expect_contains "$out" "FACT restore: PASS" "happy path: the Restore"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch directory"
  expect_contains "$out" "CHECK 195 RESULT END (exit 0)" "happy path: the end line"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi

  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  # The service is deployed IAM-only, from an image the check does not build.
  local deploy
  deploy=$(grep 'run deploy' "$dir/logs/gcloud.calls")
  expect_contains "$deploy" "--no-allow-unauthenticated" "happy path: the deploy requires IAM"
  expect_contains "$deploy" "--project=fugaro-dev --region=us-east5" "happy path: the deploy is in the installation's project and region"
  expect_absent "$calls" "artifacts" "happy path: no registry, so no image of the check's own"
  expect_absent "$calls" "storage" "happy path: no bucket is created or touched"
  # The one grant this check is about, and it is on the service, not the project.
  expect_contains "$calls" "run services add-iam-policy-binding fugaro-chk195 --project=fugaro-dev --region=us-east5 --member=serviceAccount:fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com --role=roles/run.invoker" "happy path: invoker on the check's own service"
  expect_contains "$calls" "projects add-iam-policy-binding fugaro-dev --member=serviceAccount:fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com --role=roles/logging.logWriter --condition=None" "happy path: logWriter on the project, which Cloud Build requires of a build's own account"
  expect_absent "$calls" "run services remove-iam-policy-binding" "happy path: the invoker binding is never removed from the project"
  # The build: no source at all, its own account, and where its logs go.
  local build
  build=$(grep 'builds submit' "$dir/logs/gcloud.calls")
  expect_contains "$build" "--no-source" "happy path: the build has no source, so nothing is staged anywhere"
  expect_contains "$build" "--service-account=projects/fugaro-dev/serviceAccounts/fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com" "happy path: the build runs as the throwaway account"
  expect_contains "$build" "--async" "happy path: the build is read back by its status, not by streaming its log"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "logging: CLOUD_LOGGING_ONLY" "happy path: the build says where its logs go"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "metadata.google.internal" "happy path: the step mints its token from the build's own metadata server"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "audience=https://fugaro-chk195-fake-ue.a.run.app&format=full" "happy path: the token's audience is the deployed service's own URL"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com" "happy path: the step names the throwaway account"
  expect_contains "$calls" "builds describe fake-build-1" "happy path: the build's status was read back"
  expect_contains "$calls" "logging read resource.type=\"build\" resource.labels.build_id=\"fake-build-1\"" "happy path: the build's own lines were read"
  # Everything this run created was deleted again.
  expect_contains "$calls" "run services delete fugaro-chk195 --project=fugaro-dev --region=us-east5" "happy path: the service deleted"
  expect_contains "$calls" "projects remove-iam-policy-binding fugaro-dev --member=serviceAccount:fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com --role=roles/logging.logWriter --condition=None" "happy path: the project binding removed"
  expect_contains "$calls" "iam service-accounts delete fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com --project=fugaro-dev" "happy path: the account deleted"
  # The binding is removed BEFORE the account: a binding that outlives its
  # member reads back as deleted:serviceAccount:... and can never be removed.
  local remove_line delete_line
  remove_line=$(grep -n 'projects remove-iam-policy-binding' "$dir/logs/gcloud.calls" | head -n 1 | cut -d: -f1)
  delete_line=$(grep -n 'iam service-accounts delete' "$dir/logs/gcloud.calls" | head -n 1 | cut -d: -f1)
  [ -n "$remove_line" ] && [ -n "$delete_line" ] && [ "$remove_line" -lt "$delete_line" ] \
    || fail "happy path: the project binding must be removed before the account is deleted (remove at $remove_line, delete at $delete_line)"
  # A token and anything token-shaped are in no argument and in no line of the
  # block. The build's config does name the header it sends -- the step has to --
  # but what follows it there is a shell variable holding the token the step
  # minted a moment earlier, never a value: there is no credential in any file
  # this check writes.
  local all
  all=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls" "$dir/logs/curl.calls" "$dir/out" "$dir/err")
  expect_absent "$all" "FAKE-BUILD-LOG-TOKEN" "happy path: no token-shaped line is printed"
  expect_absent "$all" "Authorization" "happy path: no authorization header is printed"
  expect_absent "$all" "Bearer" "happy path: no bearer value is printed"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" 'Authorization: Bearer $${tok}' "happy path: the config sends a token the step itself minted, held in a variable"
}

test_a_service_already_there_is_a_hard_stop() {
  local dir=$work/svcthere
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=service-there
  expect_status "$status" 1 "service there: the verdict"
  expect_contains "$err" "already exists in fugaro-dev/us-east5" "service there: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "service there: nothing was deployed"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run services delete" "service there: the service is never deleted -- it is not this check's"
}

test_an_account_already_there_is_a_hard_stop() {
  local dir=$work/sacthere
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=account-there
  expect_status "$status" 1 "account there: the verdict"
  expect_contains "$err" "already in fugaro-dev, and it is not this check's" "account there: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "iam service-accounts delete" "account there: the account is never deleted"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "account there: nothing was deployed"
}

# The grant is read back from the service's OWN policy before the build: a
# binding that did not land would otherwise turn into a 403 the block would
# report as the account having been refused, which is a different fact.
test_a_grant_that_does_not_land_stops_before_the_build() {
  local dir=$work/nogrant
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=grant-ignored
  expect_status "$status" 1 "grant ignored: the verdict"
  expect_contains "$out" "FACT invoker-grant: FAIL" "grant ignored: the row"
  expect_contains "$out" "does not name fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com" "grant ignored: the row says what the policy names"
  expect_contains "$err" "the invoker grant is not in fugaro-chk195's own IAM policy" "grant ignored: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "grant ignored: no build was submitted"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete" "grant ignored: the service is still deleted"
}

# A 403 in the build's step is a FAIL and not an INFO: it is the reading S4's
# build exception would rest on, and a refusal is the opposite of it.
test_a_call_the_service_refuses_is_a_failure() {
  local dir=$work/refused
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=call-refused
  expect_status "$status" 1 "refused call: the verdict"
  expect_contains "$out" "FACT call: FAIL" "refused call: the row"
  expect_contains "$out" "was answered 403" "refused call: the status the step got"
  expect_contains "$out" "FACT build: FAIL" "refused call: the build ended FAILURE"
  expect_contains "$out" "the step exited with non-zero status" "refused call: the failure detail"
  # The excerpt of a failed build's own lines goes through the redaction: a
  # credential-shaped line the step never writes must not reach the block.
  expect_absent "$out" "FAKE-BUILD-LOG-TOKEN" "refused call: the credential line is redacted"
  expect_absent "$out" "Authorization: Bearer" "refused call: no authorization header is printed"
  expect_contains "$out" "FACT account-deleted: PASS" "refused call: the account is still deleted"
}

# The token was minted for somebody else: a 200 earned by another identity says
# nothing about the grant this check made, so it is a FAIL however green it looks.
test_a_token_for_another_account_is_a_failure() {
  local dir=$work/otheremail
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=wrong-email
  expect_status "$status" 1 "another account's token: the verdict"
  expect_contains "$out" "FACT call: FAIL" "another account's token: the row"
  expect_contains "$out" "minted for another identity" "another account's token: the reason"
}

# A build that ended SUCCESS as some other identity is the same problem from the
# other end: the build's own record is read, not assumed.
test_a_build_that_ran_as_another_account_is_a_failure() {
  local dir=$work/otherbuildsa
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=build-as-another-account
  expect_status "$status" 1 "another build account: the verdict"
  expect_contains "$out" "FACT build: FAIL" "another build account: the row"
  expect_contains "$out" "ran as somebody-else@fugaro-dev.iam.gserviceaccount.com" "another build account: the account it ran as"
}

# The build's own line is where the email claim is read from. If it cannot be
# read, nothing about the grant was measured, and a row that said PASS would be
# the worst outcome of this check.
test_a_build_whose_line_was_not_read_is_a_failure() {
  local dir=$work/nolog
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=log-missing
  expect_status "$status" 1 "no log: the verdict"
  expect_contains "$out" "FACT call: FAIL" "no log: the row"
  expect_contains "$out" "the build's own line was not read" "no log: the reason"
  expect_absent "$out" "FACT call: PASS" "no log: the row is not a pass"
}

# A refusal on the way in is a stop with the reason named, not a run that goes on
# and reports a call it never made.
test_a_grant_that_fails_stops_before_the_build() {
  local dir=$work/grantfails
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=grant-fails
  expect_status "$status" 1 "grant fails: the verdict"
  expect_contains "$out" "FACT invoker-grant: FAIL" "grant fails: the row"
  expect_contains "$err" "roles/run.invoker" "grant fails: the stop names the role"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "grant fails: no build was submitted"
}

test_a_service_that_cannot_be_deleted_is_a_failure() {
  local dir=$work/leftover
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=service-delete-fails
  expect_status "$status" 1 "leftover: the verdict"
  expect_contains "$out" "FACT service-deleted: FAIL" "leftover: the row"
  expect_contains "$out" "gcloud run services delete fugaro-chk195" "leftover: how to delete it by hand"
  expect_contains "$out" "FACT restore: FAIL" "leftover: the Restore row"
  expect_contains "$out" "FACT account-deleted: PASS" "leftover: the account is still gone"
  expect_contains "$out" "FACT binding-removed: PASS" "leftover: the binding is still gone"
  if [ -d "$scratch" ]; then fail "leftover: the scratch directory survived"; fi
}

# The other half of the same rule: the project-level binding has to be removed
# BEFORE the account is deleted, because a binding that outlives its member reads
# back as deleted:serviceAccount:... and can never be removed again.
test_an_account_that_cannot_be_deleted_is_a_failure() {
  local dir=$work/acleftover
  prepare "$dir"
  run195 "$dir" FAKE_GCLOUD_MODE=account-delete-fails
  expect_status "$status" 1 "account leftover: the verdict"
  expect_contains "$out" "FACT account-deleted: FAIL" "account leftover: the row"
  expect_contains "$out" "gcloud iam service-accounts delete fugaro-chk195-caller@fugaro-dev.iam.gserviceaccount.com" "account leftover: how to delete it by hand"
  expect_contains "$out" "FACT binding-removed: PASS" "account leftover: the binding was removed first"
  expect_contains "$out" "FACT restore: FAIL" "account leftover: the Restore row"
}

# The rule the whole family is built on: this check runs on the installation's
# own project and on nothing else. `belong` is another installation, and the
# check's constants must never be read from an environment that could point it
# there.
test_never_belong() {
  # The project is a constant in the script and not an input: `P=${...}` would be
  # the shape that lets an environment point this check at another installation.
  local assignment
  assignment=$(grep -c '^P=fugaro-dev$' "$script" || true)
  [ "$assignment" = 1 ] || fail "never belong: the project must be the one constant assignment P=fugaro-dev, and the script has $assignment of them"
  expect_absent "$(cat "$script")" 'P=${' "never belong: the project is not read from the environment"

  # And a whole run's own record: every --project it passed, and no call that
  # named another installation anywhere in its arguments.
  local dir=$work/oneproj
  prepare "$dir"
  run195 "$dir"
  local projects p all
  all=$(cat "$dir/logs/gcloud.calls" "$dir/logs/fugaro.calls" "$dir/logs/curl.calls")
  expect_absent "$all" "belong" "never belong: no command named it"
  projects=$(grep -o -- '--project=[^ ]*' "$dir/logs/gcloud.calls" | sort -u || true)
  [ -n "$projects" ] || fail "never belong: no gcloud --project argument was recorded, so this test checks nothing"
  for p in $projects; do
    [ "$p" = "--project=fugaro-dev" ] || fail "never belong: a gcloud command named $p"
  done
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one test alone (for a solo rerun of a
# flaky-looking one). Neither set: all run.
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
t test_a_deploy_line_without_no_allow_unauthenticated_is_refused
t test_a_public_iam_policy_stops_before_any_request
t test_a_service_that_answers_unauthenticated_calls_stops_the_run
t test_every_advertised_knob_is_read
t test_happy_path
t test_a_service_already_there_is_a_hard_stop
t test_an_account_already_there_is_a_hard_stop
t test_a_grant_that_does_not_land_stops_before_the_build
t test_a_call_the_service_refuses_is_a_failure
t test_a_token_for_another_account_is_a_failure
t test_a_build_that_ran_as_another_account_is_a_failure
t test_a_build_whose_line_was_not_read_is_a_failure
t test_a_grant_that_fails_stops_before_the_build
t test_a_service_that_cannot_be_deleted_is_a_failure
t test_an_account_that_cannot_be_deleted_is_a_failure
t test_never_belong

if [ "$failures" -ne 0 ]; then
  echo "check195_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check195_test.sh: ok"
