#!/usr/bin/env bash
# Tests for scripts/live-checks/check920.sh (Checks 920 and 921). NOTHING here
# is a cloud, a project, a bucket or a database: every case writes the
# installation's local config and a set of canned answers in a temporary
# directory and drives the check against fake `gcloud`, `curl`, `git` and
# `fugaro` on PATH, which record their whole argv and the state of their stdin.
#
# The readings are the point, and each case bends exactly one of them:
#
#   * a job of the installation that mounts a workflow secret — in the delivery
#     grammar's `fugaro_p_<name>` spelling AND in the project copy a job mounted
#     before the flip (`fugaro-project-…-<name>-<16 hex>`, G4/U4), which a
#     prefix-only reading would pass;
#   * the run's OWN job or execution fed from either spelling, or running as the
#     build identity (U1);
#   * a run of the repository under test that did not succeed, or succeeded
#     with no pull request;
#   * a run whose record holds no verify, or a verify that failed, or one that
#     passed on a dirty tree (the 0400-file row's own evidence);
#   * the service's log with no line for one of the two containers, with two
#     lines for one fetch, or with a line naming a container this check cannot
#     account for (one line per fetch, G18);
#   * a review run that carries a container, or that the service served;
#   * a run of a repository the allow-list does not admit that SUCCEEDED, and
#     one refused without the fix in its sentence;
#   * a project record that cannot be read or does not name the two secrets;
#   * for Check 921: an identity with a container's accessor, with a
#     project-level one, a build that carries an available secret, and no build
#     that ran as the identity at all.
#
# And the dry run — the mode the steps file has the owner try FIRST, on a
# machine that may have no credentials at all — is held to calling NOTHING: no
# gcloud, no fugaro, no curl, and the check's own log is empty afterwards. That
# is the case that fails first if `--dry-run` is ever answered by a real read.
#
# Every run has its stdin closed and a timeout, so a check that waited for input
# would end the case as a failure rather than hang it.
#
# Usage: bash scripts/live-checks/check920_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check920.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK920_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK920_TEST_HEAD:-3000}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; out: $(printf '%s' "$out" | head -c 1800); err: $(printf '%s' "$err" | head -c 900)"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | grep -q "^FACT $2: $3"; then return 0; fi
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c 2400)"
}

TOKEN_MARKER=chk920-fake-access-token-marker
VERSION_MARKER=chk920-version-marker

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

# Each fake logs its argv and its stdin: a read on /dev/null returns EOF at once
# (status 1), where an open pipe blocks past the bound (a status above 128).
cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'gcloud %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE920_LOG:?}"
dir=${FAKE920_DIR:?}
emit() { # emit FILE [fallback]
  if [ -f "$1" ]; then cat "$1"; else printf '%s' "${2:-{\}}"; fi
}
case "$1 $2" in
"auth print-access-token")
  printf '%s\n' "${FAKE920_TOKEN:?}"
  ;;
"run jobs")
  case "$3" in
  list) emit "$dir/jobs.json" '[]' ;;
  describe) emit "$dir/describe-$4.json" "$(emit "$dir/job.json" '{}')" ;;
  executions)
    emit "$dir/execution-$5.json" "$(emit "$dir/execution.json" '{}')"
    ;;
  *)
    printf 'fake gcloud: unexpected verb %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
"logging read")
  if [ -f "$dir/log-fails" ]; then
    printf 'ERROR: (gcloud.logging.read) DEADLINE_EXCEEDED\n' >&2
    exit 1
  fi
  f=""
  case "$3" in
  *jsonPayload.run=*) f=$dir/audit-$(printf '%s' "$3" | sed -n 's/.*jsonPayload\.run="\([^"]*\)".*/\1/p').json ;;
  *cloud_run_job*) f=$dir/runlog-$(printf '%s' "$3" | sed -n 's/.*execution_name="\([^"]*\)".*/\1/p').json ;;
  esac
  if [ -n "$f" ] && [ -f "$f" ]; then cat "$f"; else printf '%s' "$(emit "$dir/log.json" '[]')"; fi
  ;;
"storage cat")
  if [ -f "$dir/no-result" ]; then
    printf 'ERROR: (gcloud.storage.cat) gs://%s not found: 404\n' "$3" >&2
    exit 1
  fi
  emit "$dir/result.json" '{}'
  ;;
"iam service-accounts")
  emit "$dir/sa.json" '[]'
  ;;
"projects get-iam-policy")
  if [ -f "$dir/no-project-iam" ]; then
    printf 'ERROR: (gcloud.projects.get-iam-policy) Permission denied\n' >&2
    exit 1
  fi
  emit "$dir/iam-project.json" '{"bindings":[]}'
  ;;
"secrets get-iam-policy")
  if [ ! -f "$dir/iam-secret-$3.json" ]; then
    printf 'ERROR: (gcloud.secrets.get-iam-policy) NOT_FOUND: %s\n' "$3" >&2
    exit 1
  fi
  cat "$dir/iam-secret-$3.json"
  ;;
"secrets list")
  emit "$dir/secrets.json" '[]'
  ;;
"builds list")
  emit "$dir/builds.json" '[]'
  ;;
"builds describe")
  emit "$dir/build.json" '{}'
  ;;
*)
  printf 'fake gcloud: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# The fake curl answers only through the config file the check hands it: the
# token must be in that file and nowhere else, so an Authorization header that
# reached the argv instead is answered 401.
cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'curl %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE920_LOG:?}"
dir=${FAKE920_DIR:?}
conf="" out="" fmt="" url=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config | -K) conf=$2; shift 2 ;;
  -o | --output) out=$2; shift 2 ;;
  -w | --write-out) fmt=$2; shift 2 ;;
  --max-time | -m) shift 2 ;;
  -q | -s | -S | -sS | --silent | --show-error | --fail) shift ;;
  -*) printf 'fake curl: unexpected flag %s\n' "$1" >&2; exit 64 ;;
  *) url=$1; shift ;;
  esac
done
code=401
body='{"error":"Permission denied"}'
if [ -n "$conf" ] && grep -q "Authorization: Bearer ${FAKE920_TOKEN:?}" "$conf" 2>/dev/null; then
  perm=$(stat -c '%a' "$conf" 2>/dev/null || stat -f '%Lp' "$conf" 2>/dev/null)
  printf 'curl-config-mode %s\n' "$perm" >>"$FAKE920_LOG"
  case "$url" in
  */creds/projectsecrets.json)
    code=$(cat "$dir/rtdb-status" 2>/dev/null || printf 200)
    body=$(cat "$dir/projectsecrets.json")
    ;;
  *) code=404 body=null ;;
  esac
fi
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; else printf '%s' "$body"; fi
if [ -n "$fmt" ]; then printf '%s' "${fmt//%\{http_code\}/$code}"; fi
FAKE
chmod +x "$fakebin/curl"

# The fake fugaro answers the four verbs this check uses and records every call.
# A `runs ls` answers the rows fixture of the repository it was asked about, so a
# case can give the two repositories different answers.
cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
printf 'fugaro %s\n' "$*" >>"${FAKE920_LOG:?}"
dir=${FAKE920_DIR:?}
case "$1" in
version)
  printf '%s\n' "${FAKE920_VERSION:-fugaro v1.2.3 (chk920-version-marker)}"
  ;;
doctor)
  printf '%s\n' "${FAKE920_DOCTOR:-project: fugaro (GCP fugaro-dev, us-east5)}"
  ;;
runs)
  repo=""
  prev=""
  for a in "$@"; do
    [ "$prev" = --repo ] && repo=$a
    prev=$a
  done
  f=$dir/rows-$(printf '%s' "$repo" | tr '/.' '--').json
  if [ -f "$f" ]; then cat "$f"; else printf '{"runs":[]}'; fi
  ;;
run)
  exit "${FAKE920_LAUNCH_EXIT:-0}"
  ;;
review)
  # `fugaro review` has no flag that sets its run id: it mints one and prints it
  # in the launch document, which is where the check reads it. FAKE920_REVIEW_RID
  # is that id, and setting it EMPTY is a launch that names no run.
  rc=${FAKE920_REVIEW_EXIT:-0}
  if [ "$rc" -eq 0 ]; then
    rid=${FAKE920_REVIEW_RID-2026-11-24-0400-0921}
    if [ -n "$rid" ]; then
      printf '{\n "project": "fugaro",\n "run": "Check 920 review",\n "run_id": "%s",\n "status": "launched"\n}\n' "$rid"
    else
      printf '{\n "project": "fugaro",\n "run": "Check 920 review",\n "status": "launched"\n}\n'
    fi
  fi
  exit "$rc"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

# The installation's own config as `fugaro update` leaves it.
write_config() {
  cat >"$confighome/fugaro/projects/fugaro.yaml" <<'YAML'
version: 3
name: fugaro
gcp_project: fugaro-dev
region: us-east5
runs_bucket: fugaro-dev-runs
budget:
    rtdb_url: https://fugaro-fb-default-rtdb.firebaseio.com
    firebase_project: fugaro-fb
repos:
    acme/sandbox:
        workflows:
            - default
YAML
}

# The project record IS3 writes: the workflow-secret names are its keys, minus
# the members its own shape uses.
write_record() {
  cat >"$answers/projectsecrets.json" <<'JSON'
{"version":2,"all_repos":false,
 "is12-env":{"repos":["acme/sandbox"]},
 "is12-file":{"repos":["acme/sandbox"]}}
JSON
}

JOB=chk920-r-acme-sandbox-1111222233334444
EXEC=${JOB}-abcde
JOB_OTHER=chk920-r-acme-elsewhere-5555666677778888
EXEC_OTHER=${JOB_OTHER}-fghij
REEXEC=${JOB}-klmno
ACCOUNT=${JOB}@fugaro-dev.iam.gserviceaccount.com
BUILD_IDENTITY=fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com
ONE_RID=2026-11-24-0400-0920
REVIEW_RID=2026-11-24-0400-0921
UNLISTED_RID=2026-11-24-0400-0922

# One job of the installation, mounting nothing.
write_jobs() {
  cat >"$answers/jobs.json" <<JSON
[{"metadata":{"name":"$JOB","labels":{"fugaro_repo":"acme-sandbox-1111222233334444"}},
  "spec":{"template":{"spec":{"template":{"spec":{"serviceAccountName":"$ACCOUNT",
    "containers":[{"image":"img","env":[
      {"name":"FUGARO_REMOTE","value":"https://github.com/acme/sandbox.git"},
      {"name":"HOME","value":"/home/user"}]}]}}}}}},
 {"metadata":{"name":"$JOB_OTHER","labels":{"fugaro_repo":"acme-elsewhere-5555666677778888"}},
  "spec":{"template":{"spec":{"template":{"spec":{"serviceAccountName":"$JOB_OTHER@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"img","env":[
      {"name":"FUGARO_REMOTE","value":"https://github.com/acme/elsewhere.git"}]}]}}}}}}]
JSON
}

write_execution() { # write_execution FILE JOB EXEC
  cat >"$1" <<JSON
{"metadata":{"name":"$3"},
 "spec":{"template":{"spec":{"serviceAccountName":"$2@fugaro-dev.iam.gserviceaccount.com",
   "containers":[{"image":"img","env":[
     {"name":"FUGARO_REMOTE","value":"https://github.com/acme/sandbox.git"},
     {"name":"HOME","value":"/home/user"}]}]}}}}
JSON
}

# A JOB's document, which is one level deeper than an execution's (the API
# spells a job's own template at spec.template.spec.template.spec and one
# execution of it at spec.template.spec), so both shapes the check must read are
# exercised.
write_job_doc() { # write_job_doc FILE JOB
  cat >"$1" <<JSON
{"metadata":{"name":"$2","labels":{"fugaro_repo":"acme-sandbox-1111222233334444"}},
 "spec":{"template":{"spec":{"template":{"spec":{"serviceAccountName":"$2@fugaro-dev.iam.gserviceaccount.com",
   "containers":[{"image":"img","env":[
     {"name":"FUGARO_REMOTE","value":"https://github.com/acme/sandbox.git"},
     {"name":"HOME","value":"/home/user"}]}]}}}}}}
JSON
}

write_rows() { # write_rows FILE RID EXEC STATUS PR REASON
  cat >"$1" <<JSON
{"runs":[{"run_id":"$2","repo":"acme/sandbox","status":"$4","terminal":true,
  "outcome":"ready","pr":$5,"started_at":"2026-11-24T03:50:00Z",
  "execution":"projects/fugaro-dev/locations/us-east5/jobs/${3%-*}/executions/$3"$6}]}
JSON
}

# One run's own record, with one verify that passed on a clean tree.
write_result() {
  cat >"$answers/result.json" <<'JSON'
{"head_sha":"9f10abcdef0123456789","verify":[
 {"n":1,"kind":"repo","head_sha":"9f10abcdef0123456789","clean_tree":true,
  "exit_code":0,"passed":true,"tests":3,"failures":[],"flaky":[],
  "started_at":"2026-11-24T03:55:00Z","duration_s":1.5}]}
JSON
}

# The service's own lines for one run: exactly one workflow-secret act per
# fetch, each naming one container, and never a value.
write_audit() { # write_audit FILE RID [purpose-one] [purpose-two]
  cat >"$1" <<JSON
[{"jsonPayload":{"run":"$2","msg":"credsvc: audit","status":"served",
  "credential":"projects/123/secrets/fugaro_p_is12-env/versions/3",
  "purpose":"${3:-workflow-secret}","caller":"$ACCOUNT"}},
 {"jsonPayload":{"run":"$2","msg":"credsvc: audit","status":"served",
  "credential":"fugaro_p_is12-file:1",
  "purpose":"${4:-workflow-secret}","caller":"$ACCOUNT"}}]
JSON
}

# The service refusing one run, with the fix in its sentence.
write_refusal() { # write_refusal FILE RID SENTENCE
  cat >"$1" <<JSON
[{"jsonPayload":{"run":"$2","msg":"credsvc: refused","status":"refused",
  "message":"$3","caller":"$ACCOUNT"}}]
JSON
}

REFUSAL='the repository acme/elsewhere is not allowed the workflow secret is12-env; run: fugaro secrets allow is12-env acme/elsewhere'

case_dir() { # case_dir <name>
  cdir=$work/$1
  answers=$cdir/answers
  confighome=$cdir/config
  CHECKOUT=$cdir/checkout
  mkdir -p "$answers" "$confighome/fugaro/projects" "$CHECKOUT"
  CALLS=$cdir/calls
  : >"$CALLS"
  write_config
  write_record
  write_jobs
  write_job_doc "$answers/describe-$JOB.json" "$JOB"
  write_job_doc "$answers/describe-$JOB_OTHER.json" "$JOB_OTHER"
  write_execution "$answers/execution-$EXEC.json" "$JOB" "$EXEC"
  write_execution "$answers/execution-$EXEC_OTHER.json" "$JOB_OTHER" "$EXEC_OTHER"
  write_execution "$answers/execution-$REEXEC.json" "$JOB" "$REEXEC"
  write_result
  write_audit "$answers/audit-$ONE_RID.json" "$ONE_RID"
  write_audit "$answers/audit-$REVIEW_RID.json" "$REVIEW_RID" git-credential git-credential
  write_refusal "$answers/audit-$UNLISTED_RID.json" "$UNLISTED_RID" "$REFUSAL"
  write_rows "$answers/rows-acme-sandbox.json" "$ONE_RID" "$EXEC" succeeded 77 ""
  write_rows "$answers/rows-acme-elsewhere.json" "$UNLISTED_RID" "$EXEC_OTHER" failed 0 ',"reason":"halted: the bootstrap was refused"'
  # The review is launched from a checkout of the repository under test, and
  # `fugaro review --pr N` resolves the repository from that checkout's origin,
  # so the fixture is a real one-file checkout.
  (
    cd "$CHECKOUT"
    git init -q .
    git remote add origin https://github.com/acme/sandbox.git
  ) >/dev/null 2>&1
}

# One `fugaro review` appends a second row: the review's own run, on the same
# job as the run it reviews.
write_review_row() { # write_review_row [FILE]
  python3 - "${1:-$answers/rows-acme-sandbox.json}" "$REVIEW_RID" "$REEXEC" "$JOB" <<'PY'
import json, sys
path, rid, ex, job = sys.argv[1:5]
doc = json.load(open(path))
doc["runs"].append({"run_id": rid, "repo": "acme/sandbox", "status": "succeeded", "terminal": True,
                    "outcome": "reviewed", "pr": 77, "started_at": "2026-11-24T04:10:00Z",
                    "mode": "review",
                    "execution": "projects/fugaro-dev/locations/us-east5/jobs/%s/executions/%s" % (job, ex)})
json.dump(doc, open(path, "w"))
PY
}

# ---------------------------------------------------------------- the runner

run920_raw() { # run920_raw <args...>
  status=0
  out=$(
    # The knobs a case bends are prefix assignments on the call (`FAKE920_DOCTOR=…
    # run920`), which bash exports into this function and on to the check; the
    # defaults here fill in only what a case left unset, so `CHECK920_REPO=` (an
    # empty value, on purpose) is not quietly replaced by the default.
    FAKE920_DOCTOR=${FAKE920_DOCTOR-project: fugaro (GCP fugaro-dev, us-east5)}
    FAKE920_VERSION=${FAKE920_VERSION-fugaro v1.2.3 ($VERSION_MARKER)}
    FAKE920_LAUNCH_EXIT=${FAKE920_LAUNCH_EXIT-0}
    FAKE920_REVIEW_EXIT=${FAKE920_REVIEW_EXIT-0}
    FAKE920_REVIEW_RID=${FAKE920_REVIEW_RID-$REVIEW_RID}
    CHECK920_RUN_ID=${CHECK920_RUN_ID-2026-11-24-0400}
    CHECK920_REPO=${CHECK920_REPO-acme/sandbox}
    CHECK920_CHECKOUT=${CHECK920_CHECKOUT-${CHECKOUT:-$work/none}}
    CHECK920_UNLISTED_REPO=${CHECK920_UNLISTED_REPO-acme/elsewhere}
    CHECK920_SECRET=${CHECK920_SECRET-is12-env}
    CHECK920_FILE_SECRET=${CHECK920_FILE_SECRET-is12-file}
    CHECK920_RUN_SECONDS=${CHECK920_RUN_SECONDS-2400}
    export FAKE920_DOCTOR FAKE920_VERSION FAKE920_LAUNCH_EXIT FAKE920_REVIEW_EXIT \
      FAKE920_REVIEW_RID CHECK920_RUN_ID \
      CHECK920_REPO CHECK920_CHECKOUT CHECK920_UNLISTED_REPO CHECK920_SECRET \
      CHECK920_FILE_SECRET CHECK920_RUN_SECONDS
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
      -u CURSOR_AGENT -u AI_AGENT \
      PATH="$fakebin:$PATH" FAKE920_LOG="${CALLS:-/dev/null}" FAKE920_DIR="${answers:-$work}" \
      FAKE920_TOKEN="$TOKEN_MARKER" \
      XDG_CONFIG_HOME="${confighome:-$work/none}" HOME="$work/home" \
      CHECK920_LOG_WAIT_SECONDS=1 CHECK920_LOG_POLL_SECONDS=1 CHECK920_POLL_SECONDS=1 \
      perl -e 'alarm shift; exec @ARGV or die' 120 bash "$script" "$@" 2>"$work/err" </dev/null
  ) || status=$?
  err=$(cat "$work/err")
}

run920() { run920_raw; }

# ------------------------------------------------------------------- the cases

# The red step: --dry-run is the mode the steps file has the owner try FIRST, on
# a machine that may have no credentials at all. It must print the plan and call
# NOTHING — not gcloud, not fugaro, not curl — which is what an empty call log
# after it holds it to. A plan answered by one real read would show up here.
test_dry_run_calls_nothing() {
  case_dir dry
  CHECK920_REPO= CHECK920_CHECKOUT= CHECK920_UNLISTED_REPO= CHECK920_SECRET= CHECK920_FILE_SECRET= \
    run920_raw --dry-run
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 920 RESULT" "dry run: the block"
  expect_contains "$out" "FACT scratch: PASS" "dry run: the scratch directory"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the shell is not tracing"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT record: FAIL — not reached" "dry run: the record was not read"
  expect_contains "$out" "FACT run-one: FAIL — not reached" "dry run: no run was launched"
  expect_contains "$out" "FACT restore: PASS" "dry run: nothing was created"
  expect_contains "$out" "CHECK 920 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "gcloud run jobs list --project=fugaro-dev --region=us-east5" "dry run: the plan carries the hard-coded project and region"
  expect_contains "$err" "fugaro run --repo" "dry run: the plan names the run it would launch"
  expect_contains "$err" "fugaro-credentials" "dry run: the plan names the log filter it would read"
  expect_contains "$err" "fugaro secrets allow" "dry run: the plan names the refusal it expects"
  expect_contains "$err" "CHECK920_REPO" "dry run: the plan says which input is unset"
  local calls
  calls=$(cat "$CALLS")
  if [ -n "$calls" ]; then fail "dry run: a tool was called: $(printf '%s' "$calls" | head -c 300)"; fi
  local scratch
  scratch=$(printf '%s' "$err" | sed -n 's/^check920\.sh: scratch directory: \([^ ]*\).*/\1/p' | head -n 1)
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_dry_run_catalog_calls_nothing() {
  case_dir drycat
  run920_raw --catalog --dry-run
  expect_status "$status" 0 "dry run --catalog: the verdict"
  expect_contains "$out" "MODE: catalog" "dry run --catalog: the mode"
  expect_contains "$out" "CHECK 921 RESULT" "dry run --catalog: the block"
  expect_contains "$out" "FACT build-identity: FAIL — not reached" "dry run --catalog: no identity was read"
  expect_contains "$out" "FACT identity-accessors: FAIL — not reached" "dry run --catalog: no policy was read"
  expect_contains "$out" "FACT catalog-build: FAIL — not reached" "dry run --catalog: no build was read"
  expect_contains "$out" "CHECK 921 RESULT END (exit 0)" "dry run --catalog: the end line"
  expect_contains "$err" "gcloud secrets get-iam-policy" "dry run --catalog: the plan names the container policies"
  expect_contains "$err" "gcloud builds list --project=fugaro-dev --region=us-east5" "dry run --catalog: the plan names the build list"
  expect_contains "$err" "gcloud iam service-accounts list --project=fugaro-dev" "dry run --catalog: the plan names the identity"
  local calls
  calls=$(cat "$CALLS")
  if [ -n "$calls" ]; then fail "dry run --catalog: a tool was called: $(printf '%s' "$calls" | head -c 300)"; fi
}

test_the_happy_path_passes() {
  case_dir happy
  write_review_row
  run920
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 920 RESULT" "the happy path: the block"
  expect_row "$out" fugaro-version PASS "the happy path: the binary"
  expect_row "$out" project-region PASS "the happy path: the installation"
  expect_row "$out" record PASS "the happy path: the record names both secrets"
  expect_row "$out" jobs PASS "the happy path: no job mounts one"
  expect_row "$out" run-one PASS "the happy path: the run succeeded with a pull request"
  expect_row "$out" run-mounts PASS "the happy path: neither the job nor the execution is fed from one"
  expect_row "$out" run-verify PASS "the happy path: the run's own verify passed on a clean tree"
  expect_row "$out" audited PASS "the happy path: one workflow-secret line per fetch"
  expect_row "$out" review PASS "the happy path: the review carried nothing and was served nothing"
  expect_row "$out" unlisted PASS "the happy path: the unlisted repository was refused with the fix"
  expect_row "$out" restore PASS "the happy path: nothing was left behind"
  expect_contains "$out" "CHECK 920 RESULT END (exit 0)" "the happy path: the end line"
  expect_contains "$out" "fugaro_p_is12-env" "the happy path: the rows name the containers"
}

test_a_job_that_mounts_a_workflow_secret_fails() {
  case_dir mounting
  python3 - "$answers/jobs.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "DEPLOY_TOKEN",
  "valueSource": {"secretKeyRef": {"secret": "fugaro_p_is12-env", "key": "1"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  [ "$status" -ne 0 ] || fail "a mounted workflow secret: passed"
  expect_row "$out" jobs FAIL "a mounted workflow secret: the jobs row"
  expect_contains "$out" "$JOB mounts fugaro_p_is12-env as DEPLOY_TOKEN" "a mounted workflow secret: the job is named"
  expect_contains "$out" "A mount left behind is the flip not reached" "a mounted workflow secret: the row names the constraint"
}

# The other spelling of the same class: a job that still mounts the PROJECT copy
# (`gcp.ProjectSecretID`, what a job mounted before the flip) reads PASS under a
# check keyed on the `fugaro_p_` prefix alone — and that is exactly the state the
# plan's G4 keeps alive after the release and U4 asks about.
test_a_job_mounting_the_project_copy_fails() {
  case_dir projectcopy
  write_review_row
  python3 - "$answers/jobs.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "DEPLOY_TOKEN",
  "valueSource": {"secretKeyRef": {"secret": "fugaro-project-fugaro-is12-env-0123456789abcdef", "key": "1"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  [ "$status" -ne 0 ] || fail "a job mounting the project copy: passed"
  expect_row "$out" jobs FAIL "a job mounting the project copy: the jobs row"
  expect_contains "$out" "$JOB mounts fugaro-project-fugaro-is12-env-0123456789abcdef as DEPLOY_TOKEN (the workflow secret is12-env)" "a job mounting the project copy: the job and the secret are named"
}

test_a_job_mounting_another_secret_is_not_this_row() {
  case_dir othermount
  python3 - "$answers/jobs.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "GIT_TOKEN",
  "valueSource": {"secretKeyRef": {"secret": "fugaro_p_git-token", "key": "1"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  write_review_row
  run920
  expect_row "$out" jobs PASS "another secret's mount: no fugaro_p_ container is a workflow secret of this class"
  expect_status "$status" 0 "another secret's mount: the verdict"
}

test_the_runs_own_job_that_mounts_one_fails() {
  case_dir runmount
  write_review_row
  python3 - "$answers/describe-$JOB.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "FILE_SECRET",
  "valueSource": {"secretKeyRef": {"secret": "projects/123/secrets/fugaro_p_is12-file", "key": "1"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  [ "$status" -ne 0 ] || fail "the run's own mount: passed"
  expect_row "$out" run-mounts FAIL "the run's own mount: the row"
  expect_contains "$out" "job-one feeds FILE_SECRET from fugaro_p_is12-file" "the run's own mount: the job is named"
}

# The same row over the project copy, on the EXECUTION rather than the job — the
# document Cloud Run overrides the job template with, and the one a prefix-only
# reading would pass.
test_the_runs_own_execution_mounting_the_project_copy_fails() {
  case_dir runprojectcopy
  write_review_row
  python3 - "$answers/execution-$EXEC.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "DEPLOY_CERT",
  "valueSource": {"secretKeyRef": {"secret": "fugaro-project-fugaro-is12-file-0123456789abcdef"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  [ "$status" -ne 0 ] || fail "the run's execution on the project copy: passed"
  expect_row "$out" run-mounts FAIL "the run's execution on the project copy: the row"
  expect_contains "$out" "exec-one feeds DEPLOY_CERT from fugaro-project-fugaro-is12-file-0123456789abcdef (the workflow secret is12-file)" "the run's execution on the project copy: the row names it"
}

# The runs bucket key is the repository's SLUG, and the `fugaro_repo` label is
# it: `internal/task.Slug` turns every RUN of characters outside [a-z0-9] into
# one "-", so `dimi/bmb_app` labels its jobs `dimi-bmb-app-<16 hex>`. A reading
# that replaced only the "/" matched no job, fell back to a name-derived path
# nobody writes, and left run-verify — the D3327/G3 evidence for the 0400 file —
# UNVERIFIED on an ordinary class of repository names.
test_a_repository_name_with_punctuation_finds_its_slug() {
  case_dir slugged
  python3 - "$answers/jobs.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc[0]["metadata"]["labels"]["fugaro_repo"] = "dimi-bmb-app-1111222233334444"
json.dump(doc, open(sys.argv[1], "w"))
PY
  write_rows "$answers/rows-dimi-bmb_app.json" "$ONE_RID" "$EXEC" succeeded 77 ""
  write_review_row "$answers/rows-dimi-bmb_app.json"
  git -C "$CHECKOUT" remote set-url origin https://github.com/dimi/bmb_app.git
  CHECK920_REPO=dimi/bmb_app run920
  expect_status "$status" 0 "a repository name with punctuation: the verdict"
  expect_row "$out" run-verify PASS "a repository name with punctuation: the verify record is read"
  expect_contains "$(cat "$CALLS")" "gs://fugaro-dev-runs/runs/dimi-bmb-app-1111222233334444/$ONE_RID/result.json" "a repository name with punctuation: the slug is the label's, not the name's"
}

test_a_run_that_ran_as_the_build_identity_fails() {
  case_dir buildid
  write_review_row
  python3 - "$answers/execution-$EXEC.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["spec"]["template"]["spec"]["serviceAccountName"] = "fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com"
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  [ "$status" -ne 0 ] || fail "the build identity: passed"
  expect_row "$out" run-mounts FAIL "the build identity: the row"
  expect_contains "$out" "exec-one runs as the BUILD identity" "the build identity: the row names it"
}

test_a_run_that_did_not_succeed_fails() {
  case_dir runfailed
  write_review_row
  write_rows "$answers/rows-acme-sandbox.json" "$ONE_RID" "$EXEC" failed 0 ',"reason":"halted: the verify failed"'
  run920
  expect_row "$out" run-one FAIL "a run that failed: the row"
  expect_contains "$out" "ended 'failed'" "a run that failed: the status is named"
  expect_row "$out" run-verify UNVERIFIED "a run that failed: nothing of its verify is read"
}

test_a_run_with_no_pull_request_fails() {
  case_dir nopr
  write_review_row
  write_rows "$answers/rows-acme-sandbox.json" "$ONE_RID" "$EXEC" succeeded 0 ""
  run920
  expect_row "$out" run-one FAIL "a run with no pull request: the row"
  expect_contains "$out" "succeeded with no pull request" "a run with no pull request: the row says why"
  expect_row "$out" review UNVERIFIED "a run with no pull request: there is nothing to review"
}

test_a_run_that_never_finished_is_not_measured() {
  case_dir inflight
  write_review_row
  python3 - "$answers/rows-acme-sandbox.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["runs"][0]["terminal"] = False
doc["runs"][0]["status"] = "running"
json.dump(doc, open(sys.argv[1], "w"))
PY
  CHECK920_RUN_SECONDS=2 run920
  expect_status "$status" 3 "a run still going: the verdict"
  expect_row "$out" run-one UNVERIFIED "a run still going: the row"
  expect_contains "$out" "still in flight is not an answer" "a run still going: the row says why"
}

test_no_verify_record_at_all_fails() {
  case_dir noverify
  write_review_row
  printf '{"head_sha":"9f10abcdef"}' >"$answers/result.json"
  run920
  expect_row "$out" run-verify FAIL "no verify record: the row"
  expect_contains "$out" "holds no verify record at all" "no verify record: the row says why"
}

test_a_verify_that_failed_fails() {
  case_dir verifyfailed
  write_review_row
  python3 - "$answers/result.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["verify"][0]["passed"] = False
doc["verify"][0]["exit_code"] = 1
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  expect_row "$out" run-verify FAIL "a failed verify: the row"
  expect_contains "$out" "reads 'failed'" "a failed verify: the row says which"
}

test_a_verify_on_a_dirty_tree_fails() {
  case_dir dirtytree
  write_review_row
  python3 - "$answers/result.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["verify"][0]["clean_tree"] = False
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  expect_row "$out" run-verify FAIL "a dirty tree: the row"
  expect_contains "$out" "passed on a DIRTY tree" "a dirty tree: the row says why"
}

test_a_record_that_cannot_be_read_is_unmeasured() {
  case_dir norecord
  write_review_row
  printf 404 >"$answers/rtdb-status"
  run920
  expect_status "$status" 3 "an unreadable record: the verdict"
  expect_row "$out" record UNVERIFIED "an unreadable record: the row"
  expect_contains "$out" "HTTP 404" "an unreadable record: the code is named"
  expect_row "$out" jobs PASS "an unreadable record: the jobs row is still measured"
}

test_a_record_that_does_not_name_the_secrets_is_unmeasured() {
  case_dir notnamed
  write_review_row
  printf '{"version":2,"all_repos":false,"some-other":{"repos":["acme/sandbox"]}}' >"$answers/projectsecrets.json"
  run920
  expect_status "$status" 3 "a record without the secrets: the verdict"
  expect_row "$out" record UNVERIFIED "a record without the secrets: the row"
  expect_contains "$out" "does NOT name is12-env, is12-file" "a record without the secrets: the row names them"
}

test_a_record_in_the_secrets_object_shape_is_read() {
  case_dir objectshape
  write_review_row
  printf '{"version":2,"all_repos":false,"secrets":{"is12-env":{"repos":["acme/sandbox"]},"is12-file":{"repos":["acme/sandbox"]}}}' >"$answers/projectsecrets.json"
  run920
  expect_row "$out" record PASS "the secrets-object shape: the row"
  expect_status "$status" 0 "the secrets-object shape: the verdict"
}

test_a_missing_audit_line_fails() {
  case_dir oneline
  write_review_row
  printf '[{"jsonPayload":{"run":"%s","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-env","purpose":"workflow-secret","caller":"%s"}}]' \
    "$ONE_RID" "$ACCOUNT" >"$answers/audit-$ONE_RID.json"
  run920
  [ "$status" -ne 0 ] || fail "a missing audit line: passed"
  expect_row "$out" audited FAIL "a missing audit line: the row"
  expect_contains "$out" "no line for fugaro_p_is12-file" "a missing audit line: the container is named"
}

test_two_lines_for_one_fetch_fail() {
  case_dir twolines
  write_review_row
  cat >"$answers/audit-$ONE_RID.json" <<JSON
[{"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-env","purpose":"workflow-secret","caller":"$ACCOUNT"}},
 {"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-env","purpose":"workflow-secret","caller":"$ACCOUNT"}},
 {"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-file","purpose":"workflow-secret","caller":"$ACCOUNT"}}]
JSON
  run920
  expect_row "$out" audited FAIL "two lines for one fetch: the row"
  expect_contains "$out" "2 lines for fugaro_p_is12-env (one fetch, one line)" "two lines for one fetch: the row says why"
}

test_a_line_naming_another_container_fails() {
  case_dir othercontainer
  write_review_row
  cat >"$answers/audit-$ONE_RID.json" <<JSON
[{"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-env","purpose":"workflow-secret","caller":"$ACCOUNT"}},
 {"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_is12-file","purpose":"workflow-secret","caller":"$ACCOUNT"}},
 {"jsonPayload":{"run":"$ONE_RID","msg":"credsvc: audit","status":"served","credential":"fugaro_p_git-token","purpose":"workflow-secret","caller":"$ACCOUNT"}}]
JSON
  run920
  expect_row "$out" audited FAIL "an unaccounted line: the row"
  expect_contains "$out" "1 workflow-secret act(s) naming a container that is neither" "an unaccounted line: the row says why"
}

test_a_service_log_that_could_not_be_read_is_not_measured() {
  case_dir unreadlog
  write_review_row
  printf 1 >"$answers/log-fails"
  run920
  expect_status "$status" 3 "an unreadable service log: the verdict"
  expect_row "$out" audited UNVERIFIED "an unreadable service log: the row"
  expect_contains "$out" "A read that did not answer is NOT the service having written no line" "an unreadable service log: the row says why"
}

test_a_service_log_with_no_line_for_the_run_fails() {
  case_dir noline
  write_review_row
  printf '[]' >"$answers/audit-$ONE_RID.json"
  run920
  [ "$status" -ne 0 ] || fail "a service log with no line for the run: passed"
  expect_row "$out" audited FAIL "a service log with no line for the run: the row"
}

test_a_review_that_was_served_the_secret_fails() {
  case_dir reviewserved
  write_review_row
  write_audit "$answers/audit-$REVIEW_RID.json" "$REVIEW_RID" workflow-secret workflow-secret
  run920
  expect_row "$out" review FAIL "a review that was served: the row"
  expect_contains "$out" "served the review run" "a review that was served: the row says so"
}

test_a_review_that_carries_the_container_fails() {
  case_dir reviewmount
  write_review_row
  write_audit "$answers/audit-$REVIEW_RID.json" "$REVIEW_RID" "git-credential" "git-credential"
  python3 - "$answers/execution-$REEXEC.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "FILE_SECRET",
  "valueSource": {"secretKeyRef": {"secret": "fugaro_p_is12-file"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  expect_row "$out" review FAIL "a review carrying the container: the row"
  expect_contains "$out" "exec-review feeds FILE_SECRET from fugaro_p_is12-file" "a review carrying the container: the row names it"
}

test_a_review_carrying_the_project_copy_fails() {
  case_dir reviewprojectcopy
  write_review_row
  write_audit "$answers/audit-$REVIEW_RID.json" "$REVIEW_RID" "git-credential" "git-credential"
  python3 - "$answers/execution-$REEXEC.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "DEPLOY_TOKEN",
  "valueSource": {"secretKeyRef": {"secret": "fugaro-project-fugaro-is12-env-0123456789abcdef"}}})
json.dump(doc, open(sys.argv[1], "w"))
PY
  run920
  expect_row "$out" review FAIL "a review carrying the project copy: the row"
  expect_contains "$out" "exec-review feeds DEPLOY_TOKEN from fugaro-project-fugaro-is12-env-0123456789abcdef (the workflow secret is12-env)" "a review carrying the project copy: the row names it"
}

# `fugaro review` has no `--run-id`: the id is the CLI's own and the launch
# document is where it is read. A launch that names none leaves the row
# unmeasured — the alternative is polling an id no run ever had.
test_a_review_whose_launch_names_no_run_is_not_measured() {
  case_dir reviewnorun
  write_review_row
  FAKE920_REVIEW_RID= run920
  expect_status "$status" 3 "a review launch naming no run: the verdict"
  expect_row "$out" review UNVERIFIED "a review launch naming no run: the row"
  expect_contains "$out" "names no run" "a review launch naming no run: the row says why"
}

test_a_review_launch_that_failed_is_not_measured() {
  case_dir reviewnolaunch
  write_review_row
  FAKE920_REVIEW_EXIT=1 run920
  expect_status "$status" 3 "a review launch that failed: the verdict"
  expect_row "$out" review UNVERIFIED "a review launch that failed: the row"
  expect_contains "$out" "launched no review" "a review launch that failed: the row says why"
}

test_an_unlisted_run_that_succeeded_fails() {
  case_dir unlistedok
  write_review_row
  write_rows "$answers/rows-acme-elsewhere.json" "$UNLISTED_RID" "$EXEC_OTHER" succeeded 9 ""
  run920
  [ "$status" -ne 0 ] || fail "an unlisted run that succeeded: passed"
  expect_row "$out" unlisted FAIL "an unlisted run that succeeded: the row"
  expect_contains "$out" "SUCCEEDED" "an unlisted run that succeeded: the row says so"
}

test_a_refusal_without_the_fix_fails() {
  case_dir nofix
  write_review_row
  printf '[{"jsonPayload":{"run":"%s","msg":"credsvc: refused","status":"refused","message":"not allowed","caller":"%s"}}]' \
    "$UNLISTED_RID" "$ACCOUNT" >"$answers/audit-$UNLISTED_RID.json"
  run920
  expect_row "$out" unlisted FAIL "a refusal without the fix: the row"
  expect_contains "$out" "refused, but with no fix in the sentence" "a refusal without the fix: the row says why"
}

test_a_run_that_failed_with_no_refusal_is_not_measured() {
  case_dir nosentence
  write_review_row
  printf '[]' >"$answers/audit-$UNLISTED_RID.json"
  run920
  expect_status "$status" 3 "a failure with no refusal: the verdict"
  expect_row "$out" unlisted UNVERIFIED "a failure with no refusal: the row"
  expect_contains "$out" "holds no refusal for it" "a failure with no refusal: the row says why"
}

test_the_installation_guard_stops_another_project() {
  case_dir otherinstall
  FAKE920_DOCTOR='project: belong (GCP belong-prod, us-east5)' run920
  expect_status "$status" 2 "another installation: the verdict"
  expect_row "$out" project-region FAIL "another installation: the row"
  expect_contains "$out" "never belong" "another installation: the row says so"
  local calls
  calls=$(cat "$CALLS")
  expect_not_contains "$calls" "gcloud auth print-access-token" "another installation: no token was minted"
  expect_not_contains "$calls" "run jobs list" "another installation: no job was read"
}

test_a_development_build_fails_the_version_row() {
  case_dir devbuild
  write_review_row
  FAKE920_VERSION='fugaro v0.0.0-dev (abc123-dirty)' run920
  expect_row "$out" fugaro-version FAIL "a development build: the row"
  expect_contains "$out" "a checkout build resolves no released credentials-service image" "a development build: the row says why"
}

test_a_launch_that_never_started_is_not_measured() {
  case_dir nolaunch
  write_review_row
  FAKE920_LAUNCH_EXIT=1 run920
  expect_status "$status" 3 "a launch that failed: the verdict"
  expect_row "$out" run-one UNVERIFIED "a launch that failed: the row"
  expect_contains "$out" "never launched one" "a launch that failed: the row says why"
}

# Check 921 launches nothing and owns no review row, and the steps file's own
# bare command (`check920.sh --catalog`) names no checkout. Run from a directory
# that is not a git checkout at all — `git -C ""` reads the caller's cwd — the
# block must still be 921's own, with no `review` row riding its verdict.
test_the_catalog_mode_needs_no_checkout() {
  case_dir catnocheckout
  write_catalog_answers ok
  local back; back=$(pwd)
  mkdir -p "$work/not-a-repo"
  cd "$work/not-a-repo"
  CHECK920_CHECKOUT= run920_raw --catalog
  cd "$back"
  expect_status "$status" 0 "the catalog mode from a directory that is not a checkout: the verdict"
  expect_contains "$out" "CHECK 921 RESULT END (exit 0)" "the catalog mode from a directory that is not a checkout: the end line"
  expect_not_contains "$out" "FACT review:" "the catalog mode from a directory that is not a checkout: no review row"
}

test_the_catalog_mode_passes() {
  case_dir catalog
  write_catalog_answers ok
  run920_raw --catalog
  expect_status "$status" 0 "the catalog happy path"
  expect_contains "$out" "MODE: catalog" "the catalog happy path: the mode"
  expect_row "$out" build-identity PASS "the catalog happy path: the identity"
  expect_row "$out" identity-accessors PASS "the catalog happy path: no accessor"
  expect_row "$out" catalog-build PASS "the catalog happy path: the build"
  expect_contains "$out" "CHECK 921 RESULT END (exit 0)" "the catalog happy path: the end line"
}

test_an_identity_with_a_containers_accessor_fails() {
  case_dir cataccessor
  write_catalog_answers accessor
  run920_raw --catalog
  [ "$status" -ne 0 ] || fail "an accessor: passed"
  expect_row "$out" identity-accessors FAIL "an accessor: the row"
  expect_contains "$out" "the container fugaro_p_is12-env grants it roles/secretmanager.secretAccessor" "an accessor: the container is named"
}

test_an_identity_with_a_project_level_accessor_fails() {
  case_dir catproject
  write_catalog_answers project
  run920_raw --catalog
  expect_row "$out" identity-accessors FAIL "a project-level accessor: the row"
  expect_contains "$out" "the project grants it roles/owner" "a project-level accessor: the role is named"
}

test_a_catalog_build_with_an_available_secret_fails() {
  case_dir catbuild
  write_catalog_answers buildsecret
  run920_raw --catalog
  expect_row "$out" catalog-build FAIL "a build with an available secret: the row"
  expect_contains "$out" "its request carries 1 available secret(s)" "a build with an available secret: the row says so"
}

test_no_build_as_the_identity_is_unverified() {
  case_dir catnobuild
  write_catalog_answers nobuild
  run920_raw --catalog
  expect_status "$status" 3 "no build as the identity: the verdict"
  expect_row "$out" catalog-build UNVERIFIED "no build as the identity: the row"
  expect_contains "$out" "none of the last 50 builds" "no build as the identity: the row says why"
}

test_a_catalog_build_that_did_not_succeed_fails() {
  case_dir catfailedbuild
  write_catalog_answers failedbuild
  run920_raw --catalog
  expect_row "$out" catalog-build FAIL "a failed build: the row"
  expect_contains "$out" "its status is FAILURE" "a failed build: the status is named"
}

# A read that did not answer is a row this check could not measure, not an
# accessor it found: the register's own row for 921 says exit 3 is a row
# UNVERIFIED (a read that did not answer). The containers' policies read fine
# here, so the row must NOT claim D3330 was violated.
test_a_project_policy_that_could_not_be_read_is_not_measured() {
  case_dir catnopolicy
  write_catalog_answers ok
  : >"$answers/no-project-iam"
  run920_raw --catalog
  expect_status "$status" 3 "an unreadable project policy: the verdict"
  expect_row "$out" identity-accessors UNVERIFIED "an unreadable project policy: the row"
  expect_contains "$out" "the project's IAM policy could not be read" "an unreadable project policy: the row says why"
  expect_row "$out" build-identity PASS "an unreadable project policy: the identity is still read"
}

test_an_absent_build_identity_is_unverified() {
  case_dir catnoidentity
  write_catalog_answers noidentity
  run920_raw --catalog
  expect_status "$status" 3 "an absent identity: the verdict"
  expect_row "$out" build-identity UNVERIFIED "an absent identity: the row"
  expect_row "$out" identity-accessors UNVERIFIED "an absent identity: nothing it holds is read"
  expect_row "$out" catalog-build UNVERIFIED "an absent identity: no build of its is read"
  expect_contains "$out" "is not an account in fugaro-dev" "an absent identity: the row says so"
}

# ------------------------------------------------------------------- the guards

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE920_LOG="$CALLS" FAKE920_DIR="$answers" \
    FAKE920_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "carries CLAUDECODE" "an agent session: the row names the marker"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_an_xtrace_shell_is_refused() {
  case_dir xtrace
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE920_LOG="$CALLS" FAKE920_DIR="$answers" FAKE920_TOKEN="$TOKEN_MARKER" \
    XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash -x "$script" </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an xtrace shell"
  expect_contains "$err" "tracing" "an xtrace shell: the stop says so"
  [ ! -s "$CALLS" ] || fail "an xtrace shell: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_input_stops_before_the_first_read() {
  case_dir noinput
  CHECK920_REPO= run920
  expect_status "$status" 2 "no repository"
  expect_contains "$err" "CHECK920_REPO must name the sandbox repository under test" "no repository: the stop names it"
  [ ! -s "$CALLS" ] || fail "no repository: it read something: $(head -c 400 "$CALLS")"
}

test_the_same_repository_twice_stops() {
  case_dir same
  CHECK920_UNLISTED_REPO=acme/sandbox run920
  expect_status "$status" 2 "the same repository twice"
  expect_contains "$err" "must not be CHECK920_REPO" "the same repository twice: the stop says so"
}

# The review half runs `fugaro review --pr N` FROM the checkout, and the CLI
# resolves the repository from that checkout's origin: a checkout of another
# repository would launch a REAL review — a cloud run, the installation's budget
# — of that repository's pull request #N, and the review row would be about a
# repository this check never read. The origin here is `acme/sandbox-prod`, a
# name that CONTAINS `acme/sandbox`, so a substring reading would have gone on
# and launched it.
test_a_checkout_of_another_repository_stops_before_the_launch() {
  case_dir wrongcheckout
  write_review_row
  git -C "$CHECKOUT" remote set-url origin https://github.com/acme/sandbox-prod.git
  run920
  expect_status "$status" 2 "a checkout of another repository: the verdict"
  expect_contains "$out" "CHECK 920 RESULT END (exit 2)" "a checkout of another repository: the end line"
  expect_contains "$err" "is a checkout of acme/sandbox-prod, not acme/sandbox" "a checkout of another repository: the stop names both"
  local calls
  calls=$(cat "$CALLS")
  if [ -n "$calls" ]; then fail "a checkout of another repository: a tool was called: $(printf '%s' "$calls" | head -c 300)"; fi
  expect_not_contains "$out" "FACT review: PASS" "a checkout of another repository: no review row passed"
}

test_a_checkout_with_no_readable_origin_stops() {
  case_dir noorigin
  mkdir -p "$cdir/plain"
  CHECK920_CHECKOUT=$cdir/plain run920
  expect_status "$status" 2 "a checkout with no origin: the verdict"
  expect_contains "$err" "is not a checkout with a readable origin remote" "a checkout with no origin: the stop says so"
  [ ! -s "$CALLS" ] || fail "a checkout with no origin: it called something: $(head -c 400 "$CALLS")"
}

# `git remote get-url origin` answers in whatever shape the remote is spelled,
# and the CLI resolves the repository the same way: the ssh spelling must be read
# as the repository it names rather than stop the check.
test_an_ssh_origin_names_the_repository_the_same_way() {
  case_dir sshorigin
  write_review_row
  git -C "$CHECKOUT" remote set-url origin git@github.com:acme/sandbox.git
  run920
  expect_status "$status" 0 "an ssh origin: the verdict"
  expect_row "$out" review PASS "an ssh origin: the review ran"
}

test_an_unknown_argument_stops() {
  case_dir unknown
  run920_raw --catalouge
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --catalouge" "an unknown argument: the stop names it"
}

test_the_token_is_never_on_argv_and_the_config_is_0600() {
  case_dir token
  write_review_row
  run920
  local calls
  calls=$(cat "$CALLS")
  expect_not_contains "$calls" "$TOKEN_MARKER" "the token: never on an argv"
  expect_contains "$calls" "curl-config-mode 600" "the token: the curl config is 0600"
  expect_not_contains "$out" "$TOKEN_MARKER" "the token: never in the block"
  expect_not_contains "$err" "$TOKEN_MARKER" "the token: never on stderr"
  expect_not_contains "$out" "Bearer" "the token: no header in the block"
}

test_stdin_is_closed() {
  case_dir stdin
  write_review_row
  run920
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "stdin: no tool was left on an open pipe"
}

test_the_scratch_directory_is_removed() {
  case_dir scratch
  write_review_row
  run920
  local scratch
  scratch=$(printf '%s' "$err" | sed -n 's/^check920\.sh: scratch directory: \([^ ]*\).*/\1/p' | head -n 1)
  [ -n "$scratch" ] || fail "the scratch directory: the script did not say where it is"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then fail "the scratch directory $scratch survived"; fi
  expect_row "$out" scratch-removed PASS "the scratch directory: the row"
}

test_help() {
  CALLS=
  run920_raw --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help"
  expect_contains "$out" "a row could not be measured" "--help: the exit codes"
}

# The catalog fixtures, one scenario at a time: `ok` is the clean installation,
# and every other name bends exactly one reading.
write_catalog_answers() {
  local mode=$1
  cat >"$answers/sa.json" <<JSON
[{"email":"$BUILD_IDENTITY","displayName":"Fugaro build identity"}]
JSON
  printf '{"bindings":[]}' >"$answers/iam-project.json"
  printf '[{"name":"projects/fugaro-dev/secrets/fugaro_p_is12-env"},{"name":"projects/fugaro-dev/secrets/fugaro_p_is12-file"},{"name":"projects/fugaro-dev/secrets/fugaro_dev_git"}]' >"$answers/secrets.json"
  printf '{"bindings":[]}' >"$answers/iam-secret-fugaro_p_is12-env.json"
  printf '{"bindings":[]}' >"$answers/iam-secret-fugaro_p_is12-file.json"
  printf '{"bindings":[]}' >"$answers/iam-secret-fugaro_dev_git.json"
  cat >"$answers/builds.json" <<JSON
[{"id":"b-1111","status":"SUCCESS","serviceAccount":"$BUILD_IDENTITY","createTime":"2026-11-24T03:00:00Z"},
 {"id":"b-0000","status":"SUCCESS","serviceAccount":"projects/fugaro-dev/serviceAccounts/$JOB@fugaro-dev.iam.gserviceaccount.com","createTime":"2026-11-23T03:00:00Z"}]
JSON
  cat >"$answers/build.json" <<'JSON'
{"id":"b-1111","status":"SUCCESS","serviceAccount":"fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com",
 "availableSecrets":{"secretManager":[]},
 "steps":[{"name":"gcr.io/cloud-builders/git","args":["clone"]}]}
JSON
  case "$mode" in
  accessor)
    printf '{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:%s"]}]}' "$BUILD_IDENTITY" \
      >"$answers/iam-secret-fugaro_p_is12-env.json"
    ;;
  project)
    printf '{"bindings":[{"role":"roles/owner","members":["serviceAccount:%s","user:ada@example.com"]}]}' "$BUILD_IDENTITY" \
      >"$answers/iam-project.json"
    ;;
  buildsecret)
    python3 - "$answers/build.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["availableSecrets"]["secretManager"].append({"versionName": "projects/1/secrets/x/versions/1", "env": "T"})
json.dump(doc, open(sys.argv[1], "w"))
PY
    ;;
  failedbuild)
    python3 - "$answers/build.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["status"] = "FAILURE"
json.dump(doc, open(sys.argv[1], "w"))
PY
    ;;
  nobuild)
    printf '[]' >"$answers/builds.json"
    ;;
  noidentity)
    printf '[]' >"$answers/sa.json"
    ;;
  esac
  printf '%s' "$mode" >"$answers/catalog-mode"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK920_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK920_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  unset FAKE920_DOCTOR FAKE920_VERSION FAKE920_LAUNCH_EXIT FAKE920_REVIEW_EXIT \
    FAKE920_REVIEW_RID CHECK920_REPO CHECK920_UNLISTED_REPO \
    CHECK920_RUN_SECONDS 2>/dev/null || true
  "$1"
}

t test_dry_run_calls_nothing
t test_dry_run_catalog_calls_nothing
t test_the_happy_path_passes
t test_a_job_that_mounts_a_workflow_secret_fails
t test_a_job_mounting_the_project_copy_fails
t test_a_job_mounting_another_secret_is_not_this_row
t test_the_runs_own_job_that_mounts_one_fails
t test_the_runs_own_execution_mounting_the_project_copy_fails
t test_a_repository_name_with_punctuation_finds_its_slug
t test_a_run_that_ran_as_the_build_identity_fails
t test_a_run_that_did_not_succeed_fails
t test_a_run_with_no_pull_request_fails
t test_a_run_that_never_finished_is_not_measured
t test_no_verify_record_at_all_fails
t test_a_verify_that_failed_fails
t test_a_verify_on_a_dirty_tree_fails
t test_a_record_that_cannot_be_read_is_unmeasured
t test_a_record_that_does_not_name_the_secrets_is_unmeasured
t test_a_record_in_the_secrets_object_shape_is_read
t test_a_missing_audit_line_fails
t test_two_lines_for_one_fetch_fail
t test_a_line_naming_another_container_fails
t test_a_service_log_that_could_not_be_read_is_not_measured
t test_a_service_log_with_no_line_for_the_run_fails
t test_a_review_that_was_served_the_secret_fails
t test_a_review_that_carries_the_container_fails
t test_a_review_carrying_the_project_copy_fails
t test_a_review_whose_launch_names_no_run_is_not_measured
t test_a_review_launch_that_failed_is_not_measured
t test_an_unlisted_run_that_succeeded_fails
t test_a_refusal_without_the_fix_fails
t test_a_run_that_failed_with_no_refusal_is_not_measured
t test_the_installation_guard_stops_another_project
t test_a_development_build_fails_the_version_row
t test_a_launch_that_never_started_is_not_measured
t test_the_catalog_mode_needs_no_checkout
t test_the_catalog_mode_passes
t test_an_identity_with_a_containers_accessor_fails
t test_an_identity_with_a_project_level_accessor_fails
t test_a_catalog_build_with_an_available_secret_fails
t test_no_build_as_the_identity_is_unverified
t test_a_catalog_build_that_did_not_succeed_fails
t test_a_project_policy_that_could_not_be_read_is_not_measured
t test_an_absent_build_identity_is_unverified
t test_an_agent_session_is_refused
t test_an_xtrace_shell_is_refused
t test_a_missing_input_stops_before_the_first_read
t test_the_same_repository_twice_stops
t test_a_checkout_of_another_repository_stops_before_the_launch
t test_a_checkout_with_no_readable_origin_stops
t test_an_ssh_origin_names_the_repository_the_same_way
t test_an_unknown_argument_stops
t test_the_token_is_never_on_argv_and_the_config_is_0600
t test_stdin_is_closed
t test_the_scratch_directory_is_removed
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check920_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check920_test.sh: ok"
