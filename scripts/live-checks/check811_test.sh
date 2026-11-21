#!/usr/bin/env bash
# Tests for scripts/live-checks/check811.sh. NOTHING here is a cloud, a project,
# an installation or a run: every case builds THREE REAL local git checkouts in a
# temporary directory and drives the check against a fake `gcloud`, a fake
# `fugaro` and a fake `curl` on PATH whose answers the case owns — the database's
# three reads, the job's own document, the two updates, the runs listings, the
# run's own record out of the runs bucket, the service's own log lines, the
# validation of a file that still writes a retired key and the launch that must be
# refused. No case can spend anything, because the only thing that spends is
# `fugaro run`, and here that is a script in $work.
#
# The readings are the point. Check 811 watches ONE release's second half against
# a repository on a subscription: a run whose agent must hold no token, an update
# that must keep a record while that run is in flight and delete it after, a file
# that still writes a retired key and a launch on an image too old. The cases that
# matter are the ones where a reading is bent: a file that still writes a retired
# key (a premise that must STOP the check), a job that still mounts the token, a
# stage that names a subscription credential, an update that writes a record the
# installation never had, a record that goes while a run is in flight, a record
# that stays once none is, a validation that accepts the file, a refusal that
# names no fix, a launch that is not refused.
#
# Usage: bash scripts/live-checks/check811_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check811.sh
work=$(mktemp -d)
# The installation's own local project config lives outside the case's own
# directory tree ($work/config unless a case says otherwise), because the check
# finds it through XDG_CONFIG_HOME and a case must never be able to read the
# config of the machine the test runs on.
confighome=$work/config
cleanup_all() {
  if [ -n "${CHECK811_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK811_TEST_HEAD:-1600}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK811_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fakes
fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"${FAKE811_LOG:?}"
dir=${FAKE811_DIR:?}
case "${1:-} ${2:-}" in
"auth print-access-token")
  printf '%s\n' 'fake811-access-token'
  ;;
"logging read")
  status=$(cat "$dir/log-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake gcloud: the log read was refused\n' >&2
    exit "$status"
  fi
  # The filter this read was given, so a case can pin that the row read the lines
  # of the run it is about rather than every line of the service.
  printf '%s\n' "${3:-}" >>"$dir/log-filter"
  python3 - "$dir/log.json" "${3:-}" <<'PY'
import json, re, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
m = re.search(r'jsonPayload\.run="([^"]*)"', sys.argv[2])
want = m.group(1) if m else ""
rows = doc if isinstance(doc, list) else []
print(json.dumps([r for r in rows if (r.get("jsonPayload") or {}).get("run") == want]))
PY
  ;;
"run jobs")
  case "${3:-}" in
  describe)
    status=$(cat "$dir/job-status" 2>/dev/null || printf 0)
    if [ "$status" != 0 ]; then
      printf 'fake gcloud: the job read was refused\n' >&2
      exit "$status"
    fi
    cat "$dir/job.json"
    ;;
  *)
    printf 'fake gcloud: unexpected run jobs verb %s\n' "${3:-}" >&2
    exit 64
    ;;
  esac
  ;;
"storage cat")
  status=$(cat "$dir/record-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake gcloud: the record read was refused\n' >&2
    exit "$status"
  fi
  key=${3:-}
  ref=${key#gs://fugaro-runs-test-project/runs/}
  ref=${ref%/result.json}
  f="$dir/record_$(printf '%s' "$ref" | tr '/' '_').json"
  if [ ! -f "$f" ]; then
    printf 'fake gcloud: no record was written for %s\n' "$key" >&2
    exit 64
  fi
  cat "$f"
  ;;
*)
  printf 'fake gcloud: unexpected verb %s %s\n' "${1:-<none>}" "${2:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE811_LOG:?}"
dir=${FAKE811_DIR:?}
name=$(basename "$PWD")
case "${1:-}" in
run)
  status=$(cat "$dir/launch-status-$name" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/launch-refusal-$name" ]; then
      cat "$dir/launch-refusal-$name" >&2
    else
      printf 'fake fugaro: the launch was refused\n' >&2
    fi
    exit "$status"
  fi
  cat "$dir/launch-$name.json"
  ;;
runs)
  status=$(cat "$dir/runs-status-$name" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake fugaro: the listing of runs was refused\n' >&2
    exit "$status"
  fi
  # The FIRST listing is the one the update's own row is read from, with the run
  # in flight; every listing after it is the one the run is followed to its end
  # with. A case that wants them to differ writes `runs-<name>.2.json`.
  n=$(cat "$dir/runs-calls-$name" 2>/dev/null || printf 0)
  printf '%s' "$((n + 1))" >"$dir/runs-calls-$name"
  f="$dir/runs-$name.json"
  if [ "$n" -gt 0 ] && [ -f "$dir/runs-$name.2.json" ]; then f="$dir/runs-$name.2.json"; fi
  cat "$f"
  ;;
update)
  # The check runs `--repo` twice in the same checkout: once with the run in
  # flight and once after it. A case that wants the two to differ writes
  # `update-status-<name>.2` and `update-repo-<name>.2.out`.
  n=$(cat "$dir/update-calls-$name" 2>/dev/null || printf 0)
  printf '%s' "$((n + 1))" >"$dir/update-calls-$name"
  status=$(cat "$dir/update-status-$name" 2>/dev/null || printf 0)
  f="$dir/update-repo-$name.out"
  if [ "$n" -gt 0 ]; then
    status=$(cat "$dir/update-status-$name.2" 2>/dev/null || printf '%s' "$status")
    if [ -f "$dir/update-repo-$name.2.out" ]; then f="$dir/update-repo-$name.2.out"; fi
  fi
  if [ "$status" != 0 ]; then
    printf 'fake fugaro: the repository update was refused\n' >&2
    exit "$status"
  fi
  cat "$f" 2>/dev/null || true
  ;;
validate)
  status=$(cat "$dir/validate-status" 2>/dev/null || printf 1)
  cat "$dir/validate.out" 2>/dev/null || true
  exit "$status"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE811_DIR:?}
log=${FAKE811_LOG:?}
out=""
url=""
conf=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o)
    out=$2
    shift 2
    ;;
  --config)
    conf=$2
    shift 2
    ;;
  --max-time)
    shift 2
    ;;
  -sS | -s | -S)
    shift
    ;;
  *)
    url=$1
    shift
    ;;
  esac
done
printf 'curl %s\n' "$url" >>"$log"
# The curl config file carries the owner's own access token, which this check
# writes there rather than passing as an argument: logging the config's KEYS is
# how a case pins that the read really presented one, and the token here is a
# literal the fake gcloud wrote.
if [ -n "$conf" ]; then
  printf 'config\n' >>"$log"
  sed 's/^\(header = "Authorization: Bearer\) .*/\1 <token>"/' "$conf" | sed 's/^/  /' >>"$log"
fi
case "$url" in
*"/creds.json")
  n=$(cat "$dir/creds-calls" 2>/dev/null || printf 0)
  n=$((n + 1))
  printf '%s' "$n" >"$dir/creds-calls"
  status=$(cat "$dir/creds-status-$n" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake curl: the database read failed\n' >&2
    exit "$status"
  fi
  case "$n" in
  1) f=creds-before.json ;;
  2) f=creds-mid.json ;;
  *) f=creds-after.json ;;
  esac
  if [ -n "$out" ]; then cat "$dir/$f" >"$out"; else cat "$dir/$f"; fi
  ;;
*)
  printf 'fake curl: unexpected URL %s\n' "$url" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/curl"

# --------------------------------------------------------------- the fixtures

OID=20261121-120001-aaaa

slug_of() { # slug_of <owner/name>
  printf '%s-0011223344556677' "$(printf '%s' "$1" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9' '-')"
}
SLUG=$(slug_of acme/sub-app)
REF="$SLUG/$OID"

# make_sandbox writes a real git checkout with a committed fugaro.yaml, an
# origin of its own and one commit. The file's body is the case's, because Check
# 811's premise is exactly about which keys that file writes.
make_sandbox() { # make_sandbox <dir> <name> <fugaro.yaml body>
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email check811@example.invalid
  git -C "$1" config user.name check811
  printf '%b' "$3" >"$1/fugaro.yaml"
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm sandbox
  git -C "$1" remote remove origin 2>/dev/null || true
  git -C "$1" remote add origin "git@github.com:acme/$2.git"
}

# job_json writes the job's own document in the shape `gcloud run jobs describe
# --format=json` answers with (the v1 API nests it at
# spec.template.spec.template.spec). `token` writes one that still carries the
# subscription token in its container's environment, which is the FAIL the
# `token` row is about; `empty` writes one that names no container at all.
job_json() { # job_json <out> [token|empty]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
env = [{"name": "FUGARO_BUCKET", "value": "b"}]
if sys.argv[2] == "token":
    env.append({"name": "CLAUDE_CODE_OAUTH_TOKEN", "valueSource": {
        "secretKeyRef": {"secret": "fugaro-oauth-token", "key": "latest"}}})
doc = {"spec": {"template": {"spec": {"template": {"spec": {
    "serviceAccountName": "job-sub@test-project.iam.gserviceaccount.com",
    "containers": [{"image": "us-docker.pkg.dev/p/jobs/app:1.0.0", "env": env}],
}}}}}}
if sys.argv[2] == "empty":
    doc["spec"]["template"]["spec"]["template"]["spec"]["containers"] = []
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# creds_json writes the database's creds node: the repository's ACCESS record
# (creds/repoaccess/<slug>, the one release 1 writes) and the OLD one
# (creds/repoauth/<slug>, the closed shape internal/creds/repoauth.go pins). The
# three phases are the three reads the check makes: before the update, between
# the two updates, and after the second. An argument drops or bends one member:
# `drop-old`, `drop-access`, `old-auth=<auth>` and `access-cred=<credential>`.
creds_json() { # creds_json <out> <before|mid|after> [drop-old|drop-access|old-auth=<a>|access-cred=<c>]
  local out=$1
  shift
  python3 - "$out" "$SLUG" "$@" <<'PY'
import json, sys
out, slug = sys.argv[1], sys.argv[2]
args = sys.argv[3:]
phase = args[0]
bend = args[1:]
old_auth = "oauth"
access_cred = "oauth"
drop_old = "drop-old" in bend
drop_access = "drop-access" in bend
for raw in bend:
    key, _, value = raw.partition("=")
    if key == "old-auth":
        old_auth = value
    elif key == "access-cred":
        access_cred = value
doc = {}
if not drop_old and phase != "after":
    doc["repoauth"] = {slug: {"version": 1, "auth": old_auth, "accounts": ["default"]}}
if not drop_access:
    doc["repoaccess"] = {slug: {"version": 1, "crafts": {"code": {"access": access_cred}}}}
open(out, "w").write(json.dumps(doc))
PY
}

# runs_json writes a listing of runs, one `<ref>|<outcome|->` argument per row:
# `-` is a row with no outcome yet, which is a run that is going.
runs_json() { # runs_json <out> <ref> <outcome|-> ...
  local out=$1
  shift
  python3 - "$out" "$@" <<'PY'
import json, sys
rows = []
args = sys.argv[2:]
for i in range(0, len(args), 2):
    ref, outcome = args[i], args[i + 1]
    row = {"run": ref, "run_id": ref.rsplit("/", 1)[-1], "status": "running"}
    if outcome and outcome != "-":
        row["outcome"] = outcome
        row["status"] = "done"
    rows.append(row)
open(sys.argv[1], "w").write(json.dumps({"runs": rows}))
PY
}

# record_json writes the run's own record. `clean` is a run whose every stage the
# service served (no `credential` and no `served_by`), `oauth` one whose first
# stage names a subscription credential the job itself would hold, `none` one
# that names no stage at all and `garbage` one that is no document.
record_json() { # record_json <out> <clean|oauth|none|garbage>
  python3 - "$1" "$2" <<'PY'
import json, sys
what = sys.argv[2]
if what == "garbage":
    open(sys.argv[1], "w").write("this is not a document\n")
    raise SystemExit(0)
doc = {"run_id": "x", "status": "succeeded"}
if what != "none":
    first = {"name": "implement", "credential": "", "served_by": ""}
    if what == "oauth":
        first["served_by"] = "oauth"
    doc["stages"] = [first, {"name": "review", "credential": "", "served_by": ""}]
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# log_json writes the credentials service's own lines, one entry per
# `<run-id>|<msg>|<status>` argument. `credsvc: audit` with status `ok` is the
# line that says the service served a call through its gateway.
log_json() { # log_json <out> <run>|<msg>|<status> ...
  local out=$1
  shift
  python3 - "$out" "$@" <<'PY'
import json, sys
rows = []
for raw in sys.argv[2:]:
    parts = raw.split("|")
    rows.append({"jsonPayload": {
        "msg": parts[1], "status": parts[2], "run": parts[0],
        "repository": "acme/sub-app", "credential": "oauth",
        "purpose": "forward", "type": ""}})
open(sys.argv[1], "w").write(json.dumps(rows))
PY
}

# case_dir builds one case's own world: the three checkouts, the installation's
# own documents and every answer the fakes give, all in the state the happy path
# needs. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  sub=$root/sub
  retired=$root/retired
  oldimage=$root/oldimage
  # The repository on a subscription: its committed file writes no retired key,
  # because release 2 refuses a launch on one that does.
  make_sandbox "$sub" sub-app 'agent:\n  model: claude-sonnet-5\ncrafts:\n  code:\n    model: claude-sonnet-5\n'
  # A checkout whose file still writes a retired key: the `retired-key` row runs
  # `fugaro validate` in it.
  make_sandbox "$retired" retired-app 'agent:\n  auth: oauth\n'
  # A checkout whose repository's job image is older than release 1: the
  # `old-image` row launches in it and reads the refusal.
  mkdir -p "$oldimage"
  # The installation's own local project config, which is where the check reads
  # the database's URL from (`budget.rtdb_url`): `fugaro doctor --json` carries no
  # such member, so a case that invented one in a doctor document would be
  # testing a source the check does not have.
  confighome=$root/config
  mkdir -p "$confighome/fugaro/projects"
  printf 'project: test\nbudget:\n  rtdb_url: https://db.invalid\n' \
    >"$confighome/fugaro/projects/test.yaml"
  job_json "$answers/job.json"
  creds_json "$answers/creds-before.json" before
  creds_json "$answers/creds-mid.json" mid
  creds_json "$answers/creds-after.json" after
  runs_json "$answers/runs-sub.json" "$REF" -
  runs_json "$answers/runs-sub.2.json" "$REF" succeeded
  printf '{"run_id":"%s","run":"%s"}\n' "$OID" "$REF" >"$answers/launch-sub.json"
  record_json "$answers/record_${SLUG}_$OID.json" clean
  log_json "$answers/log.json" "$OID|credsvc: audit|ok"
  {
    printf 'fugaro update: acme/sub-app re-applied\n'
    printf 'fugaro update: creds/repoauth/%s kept: a run of acme/sub-app is in flight\n' "$SLUG"
  } >"$answers/update-repo-sub.out"
  printf 1 >"$answers/validate-status"
  {
    printf 'fugaro.yaml: agent.auth is retired (credentials-belong-to-models): a repository\n'
    printf "no longer writes its own model credential. Run 'fugaro update' in this checkout\n"
    printf 'to be offered the removal as a diff you commit yourself.\n'
  } >"$answers/validate.out"
  printf 1 >"$answers/launch-status-oldimage"
  printf 'fugaro: a run of a repository whose model credential is brokered needs a job image whose runner knows the credentials service (fugaro 0.25.0 or later), but the job image of acme/sub-app workflow default was built from base image ghcr.io/dimipaun/fugaro-base:0.24.0, release 0.24.0. Run fugaro update in its checkout, in your own terminal window\n' \
    >"$answers/launch-refusal-oldimage"
}

# ------------------------------------------------------------------ the runs

run811_raw() { # run811_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE811_LOG="${CALLS:-/dev/null}" FAKE811_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" CHECK811_CONFIG= \
    CHECK811_LIVE_TRIES="${CHECK811_LIVE_TRIES:-1}" CHECK811_POLLS="${CHECK811_POLLS:-1}" \
    CHECK811_POLL_SECONDS=0 CHECK811_LOG_POLL_SECONDS=0 CHECK811_LOG_WAIT_SECONDS=0 \
    CHECK811_CURL_SECONDS=5 \
    ${RUN811_ENV[@]+"${RUN811_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN811_ENV=()
}

run811() { # run811 [extra args...]
  run811_raw --installation test --gcp-project test-project --region us-test1 \
    --subscription "$sub" --job test-job \
    --retired-key "$retired" --old-image "$oldimage" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run811
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 811 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" launch PASS "the happy path"
  expect_row "$out" old-record PASS "the happy path"
  expect_row "$out" token PASS "the happy path"
  expect_row "$out" record-gone PASS "the happy path"
  expect_row "$out" retired-key PASS "the happy path"
  expect_row "$out" old-image PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  # The rows really read what they say they read: the job's own document, the
  # service's lines for THIS run, the run's own record out of the runs bucket and
  # the database node, and the owner's own token presented as a curl config
  # rather than as an argument.
  expect_contains "$(cat "$CALLS")" "gcloud run jobs describe test-job" "the token row read the job"
  expect_contains "$(cat "$answers/log-filter")" "$OID" "the token row read the run's own lines"
  expect_contains "$(cat "$CALLS")" "gs://fugaro-runs-test-project/runs/$REF/result.json" "the token row read the run's own record"
  expect_contains "$(cat "$CALLS")" "curl https://db.invalid/creds.json" "the premise read the database"
  expect_contains "$(cat "$CALLS")" "header = \"Authorization: Bearer <token>\"" "the database read presented the owner's token"
  expect_contains "$(cat "$CALLS")" "fugaro validate (in $retired)" "the retired-key row validated the checkout it was pointed at"
  # One launch, one launch that must be refused, and two updates.
  local launches updates
  launches=$(grep -c '^fugaro run ' "$CALLS" || true)
  updates=$(grep -c '^fugaro update ' "$CALLS" || true)
  [ "$launches" = 2 ] || fail "the happy path: expected 2 launches, saw $launches"
  [ "$updates" = 2 ] || fail "the happy path: expected 2 updates, saw $updates"
}

test_help() {
  CALLS=
  run811_raw --help
  expect_status "$status" 0 "check811.sh --help"
  expect_contains "$out" "Usage:" "check811.sh --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run811_raw --dry-run --installation test --gcp-project test-project --region us-test1 \
    --subscription "$sub" --job test-job --retired-key "$retired" --old-image "$oldimage"
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 811 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_flag_is_refused() {
  CALLS=
  run811_raw --installation test --gcp-project test-project --region us-test1
  expect_status "$status" 2 "a missing flag"
  expect_contains "$err" "no --subscription was given" "a missing flag"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE811_LOG="$CALLS" FAKE811_DIR="$answers" \
    bash "$script" --installation test --gcp-project test-project --region us-test1 \
    --subscription "$sub" --job test-job 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE811_LOG="$CALLS" FAKE811_DIR="$answers" \
    bash -x "$script" --installation test --gcp-project test-project --region us-test1 \
    --subscription "$sub" --job test-job 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_a_subscription_that_is_not_a_checkout_is_refused() {
  case_dir not_a_checkout
  run811 --subscription "$work/not-a-repo"
  expect_status "$status" 2 "a subscription that is not a checkout"
  expect_contains "$err" "is not a directory" "a subscription that is not a checkout"
}

test_a_committed_file_that_still_writes_a_retired_key_stops_the_check() {
  case_dir committed_retired_key
  # The file a run of the repository reads is the committed one, so a repository
  # whose default branch still writes agent.auth cannot be launched on at all.
  make_sandbox "$sub" sub-app 'agent:\n  auth: oauth\n'
  run811
  expect_status "$status" 2 "a committed file writing a retired key"
  expect_row "$out" premise FAIL "a committed file writing a retired key"
  expect_contains "$out" "still writes a retired key (agent.auth)" "a committed file writing a retired key"
  [ ! -s "$CALLS" ] || case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "a committed file writing a retired key: it launched a run" ;;
  esac
}

test_a_working_file_that_still_writes_a_retired_key_stops_the_check() {
  case_dir working_retired_key
  printf 'agent:\n  auth: oauth\n' >"$sub/fugaro.yaml"
  run811
  expect_status "$status" 2 "a working file writing a retired key"
  expect_row "$out" premise FAIL "a working file writing a retired key"
  expect_contains "$out" "working fugaro.yaml of acme/sub-app still writes a retired key" "a working file writing a retired key"
}

test_a_craft_access_is_a_retired_key_too() {
  case_dir craft_access
  make_sandbox "$sub" sub-app 'agent:\n  crafts:\n    code:\n      access: oauth\n'
  run811
  expect_status "$status" 2 "a craft's access"
  expect_row "$out" premise FAIL "a craft's access"
  expect_contains "$out" "agent.crafts.code.access" "a craft's access"
}

test_a_providers_own_auth_is_not_a_retired_key() {
  case_dir provider_auth
  # `providers.<name>.auth` is a provider's own key and is NOT retired: a scan
  # that read the key's name alone would stop the check on a file release 2
  # accepts.
  make_sandbox "$sub" sub-app 'providers:\n  openrouter:\n    auth: api-key\n    base_url: https://openrouter.invalid\n'
  run811
  expect_status "$status" 0 "a provider's own auth"
  expect_row "$out" premise PASS "a provider's own auth"
}

test_a_local_config_with_no_database_url_stops_the_check() {
  case_dir no_rtdb
  printf 'project: test\n' >"$confighome/fugaro/projects/test.yaml"
  run811
  expect_status "$status" 2 "a local config with no database URL"
  expect_row "$out" premise UNVERIFIED "a local config with no database URL"
}

test_an_rtdb_url_that_is_given_is_used() {
  case_dir rtdb_given
  printf 'project: test\n' >"$confighome/fugaro/projects/test.yaml"
  run811 --rtdb-url https://db.invalid
  expect_status "$status" 0 "an rtdb url that is given"
  expect_contains "$(cat "$CALLS")" "curl https://db.invalid/creds.json" "an rtdb url that is given"
}

test_a_creds_node_that_cannot_be_read_stops_the_check() {
  case_dir creds_unreadable
  printf 7 >"$answers/creds-status-1"
  run811
  expect_status "$status" 2 "a creds node that cannot be read"
  expect_row "$out" premise UNVERIFIED "a creds node that cannot be read"
  [ ! -s "$CALLS" ] || case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "a creds node that cannot be read: it launched a run" ;;
  esac
}

test_a_repository_with_no_access_record_stops_the_check() {
  case_dir no_access_record
  creds_json "$answers/creds-before.json" before drop-access
  run811
  expect_status "$status" 2 "a repository with no access record"
  expect_row "$out" premise FAIL "a repository with no access record"
  expect_contains "$out" "carries no access record" "a repository with no access record"
}

test_an_old_record_that_is_not_a_subscription_stops_the_check() {
  case_dir old_record_not_oauth
  creds_json "$answers/creds-before.json" before old-auth=api-key
  run811
  expect_status "$status" 2 "an old record that is not a subscription"
  expect_row "$out" premise FAIL "an old record that is not a subscription"
  expect_contains "$out" "names 'auth: api-key', not 'oauth'" "an old record that is not a subscription"
}

test_a_repository_whose_records_name_no_subscription_stops_the_check() {
  case_dir no_subscription_at_all
  creds_json "$answers/creds-before.json" before drop-old access-cred=vertex
  run811
  expect_status "$status" 2 "a repository whose records name no subscription"
  expect_row "$out" premise FAIL "a repository whose records name no subscription"
  expect_contains "$out" "names nothing this check reads as a subscription" "a repository whose records name no subscription"
}

test_a_launch_that_fails_is_a_finding() {
  case_dir launch_fails
  printf 1 >"$answers/launch-status-sub"
  run811
  expect_status "$status" 1 "a launch that fails"
  expect_row "$out" launch FAIL "a launch that fails"
  expect_row "$out" old-record UNVERIFIED "a launch that fails"
  expect_row "$out" token UNVERIFIED "a launch that fails"
  expect_row "$out" retired-key PASS "a launch that fails"
}

test_a_launch_whose_answer_carries_no_run_id_leaves_the_row_unverified() {
  case_dir launch_no_id
  printf '{}\n' >"$answers/launch-sub.json"
  run811
  expect_status "$status" 3 "a launch whose answer carries no run id"
  expect_row "$out" launch UNVERIFIED "a launch whose answer carries no run id"
}

test_an_update_that_fails_is_a_finding() {
  case_dir update_fails
  printf 1 >"$answers/update-status-sub"
  run811
  expect_status "$status" 1 "an update that fails"
  expect_row "$out" old-record FAIL "an update that fails"
  expect_row "$out" token PASS "an update that fails"
}

test_a_run_that_had_already_ended_leaves_the_record_row_unverified() {
  case_dir run_already_ended
  runs_json "$answers/runs-sub.json" "$REF" succeeded
  # A run that had already ended makes the row read UNVERIFIED, so the check
  # never reads the database between the two updates: the second read is the
  # `record-gone` row's own.
  creds_json "$answers/creds-mid.json" after
  run811
  expect_status "$status" 3 "a run that had already ended"
  expect_row "$out" old-record UNVERIFIED "a run that had already ended"
  expect_contains "$out" "had already ended ('succeeded')" "a run that had already ended"
  expect_row "$out" record-gone PASS "a run that had already ended"
}

test_a_listing_that_never_answers_leaves_the_record_row_unverified() {
  case_dir listing_unreadable
  printf 1 >"$answers/runs-status-sub"
  run811
  expect_status "$status" 3 "a listing that never answers"
  expect_row "$out" old-record UNVERIFIED "a listing that never answers"
  expect_row "$out" token UNVERIFIED "a listing that never answers"
}

test_an_update_that_writes_a_record_the_installation_never_had_is_a_finding() {
  case_dir update_writes_a_record
  # Release 2's update no longer writes creds/repoauth at all: a record that
  # appears where there was none is the release-1 behaviour coming back.
  creds_json "$answers/creds-before.json" before drop-old
  run811
  expect_status "$status" 1 "an update that writes a record"
  expect_row "$out" old-record FAIL "an update that writes a record"
  expect_contains "$out" "WROTE an old record" "an update that writes a record"
}

test_a_repository_whose_old_record_is_already_gone_leaves_both_record_rows_unverified() {
  case_dir old_record_gone
  creds_json "$answers/creds-before.json" before drop-old
  creds_json "$answers/creds-mid.json" mid drop-old
  run811
  expect_status "$status" 3 "an old record already gone"
  expect_row "$out" premise PASS "an old record already gone"
  expect_row "$out" old-record UNVERIFIED "an old record already gone"
  expect_row "$out" record-gone UNVERIFIED "an old record already gone"
  expect_contains "$out" "already deleted it (CM31)" "an old record already gone"
}

test_a_creds_node_that_cannot_be_read_after_the_update_leaves_the_row_unverified() {
  case_dir creds_mid_unreadable
  printf 7 >"$answers/creds-status-2"
  run811
  expect_status "$status" 3 "a creds node that cannot be read after the update"
  expect_row "$out" old-record UNVERIFIED "a creds node that cannot be read after the update"
}

test_a_record_that_goes_while_a_run_is_in_flight_is_a_finding() {
  case_dir record_goes_in_flight
  creds_json "$answers/creds-mid.json" mid drop-old
  run811
  expect_status "$status" 1 "a record that goes while a run is in flight"
  expect_row "$out" old-record FAIL "a record that goes while a run is in flight"
  expect_contains "$out" "is GONE after 'fugaro update --repo' was run while the run" "a record that goes while a run is in flight"
}

test_a_job_that_still_carries_the_token_is_a_finding() {
  case_dir job_still_has_token
  job_json "$answers/job.json" token
  # The listing of runs is bent too, so the run itself cannot be followed: the
  # job's own document is read FIRST, so the finding is reported anyway.
  printf 1 >"$answers/runs-status-sub"
  run811
  expect_status "$status" 1 "a job that still carries the token"
  expect_row "$out" token FAIL "a job that still carries the token"
  expect_contains "$out" "carries CLAUDE_CODE_OAUTH_TOKEN in its container's environment" "a job that still carries the token"
  expect_row "$out" record-gone UNVERIFIED "a job that still carries the token"
}

test_a_job_document_that_cannot_be_read_leaves_the_row_unverified() {
  case_dir job_unreadable
  printf 1 >"$answers/job-status"
  run811
  expect_status "$status" 3 "a job document that cannot be read"
  expect_row "$out" token UNVERIFIED "a job document that cannot be read"
  expect_contains "$out" "own document could not be read" "a job document that cannot be read"
}

test_a_job_that_names_no_environment_leaves_the_row_unverified() {
  case_dir job_without_containers
  job_json "$answers/job.json" empty
  run811
  expect_status "$status" 3 "a job that names no environment"
  expect_row "$out" token UNVERIFIED "a job that names no environment"
  expect_contains "$out" "names no container environment at all" "a job that names no environment"
}

test_a_job_that_carries_another_key_says_so_and_still_passes() {
  case_dir job_with_api_key
  python3 - "$answers/job.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["spec"]["template"]["spec"]["template"]["spec"]["containers"][0]["env"].append(
    {"name": "ANTHROPIC_API_KEY", "valueSource": {"secretKeyRef": {"secret": "k", "key": "latest"}}})
open(sys.argv[1], "w").write(json.dumps(doc))
PY
  run811
  expect_status "$status" 0 "a job carrying another key"
  expect_row "$out" token PASS "a job carrying another key"
  expect_contains "$out" "does carry ANTHROPIC_API_KEY, which this row does not judge" "a job carrying another key"
}

test_a_run_that_never_ends_leaves_the_row_unverified() {
  case_dir run_never_ends
  runs_json "$answers/runs-sub.2.json" "$REF" -
  run811
  expect_status "$status" 3 "a run that never ends"
  expect_row "$out" token UNVERIFIED "a run that never ends"
  expect_row "$out" record-gone UNVERIFIED "a run that never ends"
  expect_row "$out" old-record PASS "a run that never ends"
}

test_a_run_with_no_served_line_leaves_the_row_unverified() {
  case_dir no_served_line
  log_json "$answers/log.json" "$OID|credsvc: audit|refused"
  run811
  expect_status "$status" 3 "a run with no served line"
  expect_row "$out" token UNVERIFIED "a run with no served line"
  expect_contains "$out" "carry no served line" "a run with no served line"
}

test_a_log_read_that_does_not_answer_leaves_the_row_unverified() {
  case_dir log_unreadable
  printf 1 >"$answers/log-status"
  run811
  expect_status "$status" 3 "a log read that does not answer"
  expect_row "$out" token UNVERIFIED "a log read that does not answer"
  expect_contains "$out" "could not be read" "a log read that does not answer"
}

test_a_record_that_cannot_be_read_leaves_the_row_unverified() {
  case_dir record_unreadable
  printf 1 >"$answers/record-status"
  run811
  expect_status "$status" 3 "a record that cannot be read"
  expect_row "$out" token UNVERIFIED "a record that cannot be read"
  expect_contains "$out" "could not be read out of the runs bucket" "a record that cannot be read"
}

test_a_record_with_no_stages_leaves_the_row_unverified() {
  case_dir record_without_stages
  record_json "$answers/record_${SLUG}_$OID.json" none
  run811
  expect_status "$status" 3 "a record with no stages"
  expect_row "$out" token UNVERIFIED "a record with no stages"
  expect_contains "$out" "names no stage" "a record with no stages"
}

test_a_record_that_is_no_document_leaves_the_row_unverified() {
  case_dir record_garbage
  record_json "$answers/record_${SLUG}_$OID.json" garbage
  run811
  expect_status "$status" 3 "a record that is no document"
  expect_row "$out" token UNVERIFIED "a record that is no document"
  expect_contains "$out" "no document this check can read" "a record that is no document"
}

test_a_stage_that_names_a_subscription_credential_is_a_finding() {
  case_dir stage_holds_oauth
  record_json "$answers/record_${SLUG}_$OID.json" oauth
  run811
  expect_status "$status" 1 "a stage naming a subscription credential"
  expect_row "$out" token FAIL "a stage naming a subscription credential"
  expect_contains "$out" "names a subscription credential (oauth) for the stage(s) implement" "a stage naming a subscription credential"
}

test_a_record_that_stays_once_none_is_going_is_a_finding() {
  case_dir record_stays
  creds_json "$answers/creds-after.json" mid
  run811
  expect_status "$status" 1 "a record that stays"
  expect_row "$out" record-gone FAIL "a record that stays"
  expect_contains "$out" "is STILL there after 'fugaro update --repo' was run with no run" "a record that stays"
}

test_another_run_going_leaves_the_deletion_unverified() {
  case_dir another_run_going
  runs_json "$answers/runs-sub.2.json" "$REF" succeeded "acme-other-app-0011223344556677/20261121-130001-bbbb" -
  run811
  expect_status "$status" 3 "another run going"
  expect_row "$out" record-gone UNVERIFIED "another run going"
  expect_row "$out" token PASS "another run going"
}

test_an_update_that_fails_to_delete_is_a_finding() {
  case_dir delete_update_fails
  printf 1 >"$answers/update-status-sub.2"
  run811
  expect_status "$status" 1 "an update that fails to delete"
  expect_row "$out" record-gone FAIL "an update that fails to delete"
  expect_row "$out" old-record PASS "an update that fails to delete"
}

test_a_validate_that_accepts_a_retired_key_is_a_finding() {
  case_dir validate_accepts
  printf 0 >"$answers/validate-status"
  : >"$answers/validate.out"
  run811
  expect_status "$status" 1 "a validate that accepts a retired key"
  expect_row "$out" retired-key FAIL "a validate that accepts a retired key"
  expect_contains "$out" "ACCEPTED it (exit 0)" "a validate that accepts a retired key"
}

test_a_validate_that_names_no_fix_is_a_finding() {
  case_dir validate_without_fix
  printf 'fugaro.yaml: agent.auth is retired\n' >"$answers/validate.out"
  run811
  expect_status "$status" 1 "a validate that names no fix"
  expect_row "$out" retired-key FAIL "a validate that names no fix"
  expect_contains "$out" "names no fix" "a validate that names no fix"
}

test_a_validate_whose_refusal_names_no_retired_key_leaves_the_row_unverified() {
  case_dir validate_another_reason
  printf "fugaro.yaml: the file is not readable by this release\nRun 'fugaro update' to see the fix\n" \
    >"$answers/validate.out"
  run811
  expect_status "$status" 3 "a refusal for another reason"
  expect_row "$out" retired-key UNVERIFIED "a refusal for another reason"
  expect_contains "$out" "names none of the retired keys the file writes" "a refusal for another reason"
}

test_a_retired_key_checkout_whose_file_writes_none_leaves_the_row_unverified() {
  case_dir retired_key_clean
  make_sandbox "$retired" retired-app 'agent:\n  model: claude-sonnet-5\n'
  run811
  expect_status "$status" 3 "a retired-key checkout whose file writes none"
  expect_row "$out" retired-key UNVERIFIED "a retired-key checkout whose file writes none"
}

test_no_retired_key_flag_leaves_the_row_unverified() {
  case_dir no_retired_key_flag
  run811 --retired-key ""
  expect_status "$status" 3 "no --retired-key"
  expect_row "$out" retired-key UNVERIFIED "no --retired-key"
  expect_contains "$out" "no --retired-key was given" "no --retired-key"
}

test_a_launch_on_an_old_image_that_is_not_refused_is_a_finding() {
  case_dir old_image_served
  rm -f "$answers/launch-status-oldimage"
  printf '{"run_id":"%s","run":"%s"}\n' "$OID" "$REF" >"$answers/launch-oldimage.json"
  run811
  expect_status "$status" 1 "a launch on an old image that is not refused"
  expect_row "$out" old-image FAIL "a launch on an old image that is not refused"
  expect_contains "$out" "was NOT refused (exit 0)" "a launch on an old image that is not refused"
}

test_a_refusal_that_names_no_rebuild_command_leaves_the_row_unverified() {
  case_dir old_image_no_command
  printf 'fugaro: the launch was refused for another reason\n' >"$answers/launch-refusal-oldimage"
  run811
  expect_status "$status" 3 "a refusal that names no rebuild command"
  expect_row "$out" old-image UNVERIFIED "a refusal that names no rebuild command"
  expect_contains "$out" "names no command that rebuilds the image" "a refusal that names no rebuild command"
}

test_a_refusal_that_names_no_release_leaves_the_row_unverified() {
  case_dir old_image_no_release
  printf 'fugaro: the image is too old: run fugaro update in its checkout\n' \
    >"$answers/launch-refusal-oldimage"
  run811
  expect_status "$status" 3 "a refusal that names no release"
  expect_row "$out" old-image UNVERIFIED "a refusal that names no release"
  expect_contains "$out" "names no fugaro release the image has to be at or past" "a refusal that names no release"
}

test_no_old_image_flag_leaves_the_row_unverified() {
  case_dir no_old_image_flag
  run811 --old-image ""
  expect_status "$status" 3 "no --old-image"
  expect_row "$out" old-image UNVERIFIED "no --old-image"
  expect_contains "$out" "no --old-image was given" "no --old-image"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run811_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK811_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK811_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_flag_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_subscription_that_is_not_a_checkout_is_refused
t test_a_committed_file_that_still_writes_a_retired_key_stops_the_check
t test_a_working_file_that_still_writes_a_retired_key_stops_the_check
t test_a_craft_access_is_a_retired_key_too
t test_a_providers_own_auth_is_not_a_retired_key
t test_a_local_config_with_no_database_url_stops_the_check
t test_an_rtdb_url_that_is_given_is_used
t test_a_creds_node_that_cannot_be_read_stops_the_check
t test_a_repository_with_no_access_record_stops_the_check
t test_an_old_record_that_is_not_a_subscription_stops_the_check
t test_a_repository_whose_records_name_no_subscription_stops_the_check
t test_a_launch_that_fails_is_a_finding
t test_a_launch_whose_answer_carries_no_run_id_leaves_the_row_unverified
t test_an_update_that_fails_is_a_finding
t test_a_run_that_had_already_ended_leaves_the_record_row_unverified
t test_a_listing_that_never_answers_leaves_the_record_row_unverified
t test_an_update_that_writes_a_record_the_installation_never_had_is_a_finding
t test_a_repository_whose_old_record_is_already_gone_leaves_both_record_rows_unverified
t test_a_creds_node_that_cannot_be_read_after_the_update_leaves_the_row_unverified
t test_a_record_that_goes_while_a_run_is_in_flight_is_a_finding
t test_a_job_that_still_carries_the_token_is_a_finding
t test_a_job_document_that_cannot_be_read_leaves_the_row_unverified
t test_a_job_that_names_no_environment_leaves_the_row_unverified
t test_a_job_that_carries_another_key_says_so_and_still_passes
t test_a_run_that_never_ends_leaves_the_row_unverified
t test_a_run_with_no_served_line_leaves_the_row_unverified
t test_a_log_read_that_does_not_answer_leaves_the_row_unverified
t test_a_record_that_cannot_be_read_leaves_the_row_unverified
t test_a_record_with_no_stages_leaves_the_row_unverified
t test_a_record_that_is_no_document_leaves_the_row_unverified
t test_a_stage_that_names_a_subscription_credential_is_a_finding
t test_a_record_that_stays_once_none_is_going_is_a_finding
t test_another_run_going_leaves_the_deletion_unverified
t test_an_update_that_fails_to_delete_is_a_finding
t test_a_validate_that_accepts_a_retired_key_is_a_finding
t test_a_validate_that_names_no_fix_is_a_finding
t test_a_validate_whose_refusal_names_no_retired_key_leaves_the_row_unverified
t test_a_retired_key_checkout_whose_file_writes_none_leaves_the_row_unverified
t test_no_retired_key_flag_leaves_the_row_unverified
t test_a_launch_on_an_old_image_that_is_not_refused_is_a_finding
t test_a_refusal_that_names_no_rebuild_command_leaves_the_row_unverified
t test_a_refusal_that_names_no_release_leaves_the_row_unverified
t test_no_old_image_flag_leaves_the_row_unverified
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check811_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check811_test.sh: ok"
