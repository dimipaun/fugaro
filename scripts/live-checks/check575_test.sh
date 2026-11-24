#!/usr/bin/env bash
# Tests for scripts/live-checks/check575.sh, with a fake `curl` and a fake
# `gcloud` on PATH: NOTHING here reaches a cloud, a project, an API or a
# scheduler, no agent runs a live command, and both fakes record every call they
# are given — the method, the path, the directory it was made in, and, for a call
# carrying a sign-in, the MODE of the curl config the token travelled in and
# whether that config held the Authorization header (never the header itself).
#
# Check 575 measures a month of a live reseller's portal, and neither the portal
# nor a reseller exists anywhere but in a deployment: the only way to measure its
# rows is against fakes that answer the way those do, including the refusal a sale
# beyond the credit limit earns. The documents the fakes serve are the shapes
# ee/keyservice writes (resellers.reseller, resellers.customer[], keys.key[],
# statements.statement[], the Cloud Scheduler job and the API's own revision), and
# the run's line is the sentence api_statements.go logs.
#
# The cases that matter most are the ones about what this check must NOT do: no
# ID token in its output, its logs or a command line; no row measured in an
# agent's session or against fugaro-prod by accident; no month read from an origin
# that is not the API's; and a sale that the credit limit cannot admit — a build
# that admits it is the finding the whole check exists for.
#
# Usage: bash scripts/live-checks/check575_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check575_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check575_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check575.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK575_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK575_TEST_HEAD:-1600}")" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

project=fugaro-staging
api=https://portal-staging.chk575.example
limit=5000000
rate=20000
days=29

# --------------------------------------------------------------------- fakes
write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check575_test.sh only: it routes
# on the method and the path, serves the document a case wrote into $FAKE575_DIR,
# and records what it was asked for. What it records of a call carrying a sign-in
# is the CONFIG's path, its mode and whether it held an Authorization header —
# never the header, because a test that printed it would be the leak this check is
# written to avoid.
set -euo pipefail
dir=${FAKE575_DIR:?}
mkdir -p "$dir/logs"
method=GET
out="" cfg="" url="" data=""
while [ $# -gt 0 ]; do
  case "$1" in
  -sS | -s | --silent) shift ;;
  --max-time) shift 2 ;;
  --config) cfg=$2; shift 2 ;;
  -o) out=$2; shift 2 ;;
  -w) shift 2 ;;
  -X) method=$2; shift 2 ;;
  -H) shift 2 ;;
  --data-binary) data=$2; shift 2 ;;
  *)
    url=$1
    shift
    ;;
  esac
done
rest=${url#*://}
path="/${rest#*/}"
cmod=none
auth=no
if [ -n "$cfg" ]; then
  cmod=$(stat -c %a "$cfg" 2>/dev/null || echo unknown)
  if grep -q '^header = "Authorization: Bearer ' "$cfg" 2>/dev/null; then auth=yes; fi
fi
printf 'curl %s %s (in %s config=%s mode=%s auth=%s)\n' "$method" "$path" "$PWD" "${cfg:-none}" "$cmod" "$auth" >>"$dir/logs/curl.calls"
case "$method $path" in
"GET /api/workspaces") name=anonymous ;;
# The operations area's own read of one organisation's workspaces, which is the
# only route that answers a portal workspace id (a key's own workspace_id is the
# Firebase project id, TN2): the query names the organisation and is recorded with
# the call, so a case says which customer was asked about.
"GET /api/ops/workspaces"*) name=opsworkspaces ;;
"GET /api/resellers/terms") name=terms ;;
"GET /api/resellers/customers") name=customers ;;
"GET /api/statements") name=statements ;;
"POST /api/resellers/customers/"*"/keys")
  name=mint
  printf '%s' "$data" >"$dir/logs/mint.request.json"
  ;;
"GET /api/resellers/customers/"*"/keys")
  n=0
  [ -f "$dir/logs/keys.gets" ] && n=$(cat "$dir/logs/keys.gets")
  n=$((n + 1))
  printf '%s' "$n" >"$dir/logs/keys.gets"
  if [ "$n" -gt 1 ] && [ -f "$dir/keys-after.body" ]; then name=keys-after; else name=keys; fi
  ;;
*)
  echo "fake curl: no route for '$method $url'" >&2
  exit 7
  ;;
esac
if [ -f "$dir/$name.exit" ]; then
  [ -f "$dir/$name.err" ] && cat "$dir/$name.err" >&2
  exit "$(cat "$dir/$name.exit")"
fi
code=200
[ -f "$dir/$name.code" ] && code=$(cat "$dir/$name.code")
if [ -n "$out" ] && [ -f "$dir/$name.body" ]; then cp "$dir/$name.body" "$out"; fi
printf '%s' "$code"
CURL_EOF
  chmod +x "$1/bin/curl"
}

write_fake_gcloud() { # dir
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check575_test.sh only: it
# answers each read out of $FAKE575_DIR and records the call. A document that is
# not there exits 1 with an error the way a missing resource does; `<name>.exit`
# makes the call fail with the status a case picked. `<name>.err` is what that
# failure says. Nothing here talks to a cloud.
set -euo pipefail
dir=${FAKE575_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/gcloud.calls"

# The flags that are not part of a document's identity are dropped, so which file
# answers a call cannot depend on the order a flag was passed in.
args=()
while [ $# -gt 0 ]; do
  case "$1" in
  --format=*) shift ;;
  --project=* | --location=* | --region=* | --freshness=* | --limit=*) shift ;;
  --project | --location | --region | --freshness | --limit | --format) shift 2 ;;
  *)
    args+=("$1")
    shift
    ;;
  esac
done
case "${args[0]:-} ${args[1]:-}" in
"run services") name=service ;;
"scheduler jobs")
  case "${args[2]:-}" in
  describe) name=job ;;
  run) name=jobrun ;;
  *)
    echo "fake gcloud: no route for '$*'" >&2
    exit 7
    ;;
  esac
  ;;
"logging read") name=log ;;
*)
  echo "fake gcloud: no route for '$*'" >&2
  exit 7
  ;;
esac
if [ -f "$dir/$name.exit" ]; then
  [ -f "$dir/$name.err" ] && cat "$dir/$name.err" >&2
  exit "$(cat "$dir/$name.exit")"
fi
if [ "$name" = jobrun ]; then exit 0; fi
if [ ! -f "$dir/$name.json" ]; then
  echo "ERROR: (gcloud) NOT_FOUND: $name was not found" >&2
  exit 1
fi
cat "$dir/$name.json"
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# write_token DIR [PROVIDER] [AUD] [EXP-OFFSET]: the ID token a browser holds. The
# payload carries an address and a uid that nothing may print — they are there so
# a case can hold the check to reading neither.
write_token() { # write_token DIR [provider] [aud] [exp offset]
  local dir=$1 provider=${2:-google.com} aud=${3:-$project} offset=${4:-3600}
  python3 - "$dir/token.txt" "$provider" "$aud" "$offset" <<'PY'
import base64, json, sys, time

path, provider, aud, offset = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])


def b64(doc):
    return base64.urlsafe_b64encode(json.dumps(doc, separators=(",", ":")).encode()).decode().rstrip("=")


head = b64({"alg": "RS256", "kid": "chk575key", "typ": "JWT"})
payload = {
    "iss": "https://securetoken.google.com/" + aud,
    "aud": aud,
    "sub": "uid-chk575-person",
    "email": "chk575-person@example.invalid",
    "exp": int(time.time()) + offset,
    "firebase": {"sign_in_provider": provider},
}
open(path, "w").write("%s.%s.%s\n" % (head, b64(payload), b64({"sig": "chk575"})))
PY
  chmod 600 "$dir/token.txt"
}

# prepare DIR [PROJECT]: the state every row holds — a reseller with a credit
# limit, two customers, one licenced key, a month already stated, an API revision
# configured with the scheduler pair, and a job that carries that pair and is
# enabled. Each case bends one of these files.
prepare() { # dir [project]
  local dir=$1 proj=${2:-$project}
  mkdir -p "$dir/bin" "$dir/logs" "$dir/tmp" "$dir/home"
  write_fake_curl "$dir"
  write_fake_gcloud "$dir"
  write_token "$dir" google.com "$proj" 3600
  python3 - "$dir" "$proj" "$limit" "$rate" <<'PY'
import datetime, json, os, sys

dir, project, limit, rate = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
account = "fugaro-portal-scheduler@%s.iam.gserviceaccount.com" % project
audience = "https://fugaro-portal-api-575575575575.us-east5.run.app"
assigned = "https://fugaro-portal-api-abc123-uc.a.run.app"


def write(name, doc):
    with open(os.path.join(dir, name), "w") as out:
        json.dump(doc, out)


def write_text(name, text):
    with open(os.path.join(dir, name), "w") as out:
        out.write(text)


def fault(code, message):
    return {"error": {"code": code, "message": message}}


# The origin probe: an unauthenticated read, and the API's own refusal.
write("anonymous.body", fault("unauthenticated", "the request carries no sign-in"))
write_text("anonymous.code", "401")

write("terms.body", {
    "organisation_id": "org-reseller",
    "credit_limit_micros": limit,
    "rates": [
        {"row": "team-annual", "daily_rate_micros": rate},
        {"row": "team-monthly", "daily_rate_micros": 30000},
    ],
    "created_at": "2026-08-01T00:00:00Z",
})
write("customers.body", [
    {"organisation_id": "org-alpha", "name": "Alpha Ltd",
     "reseller_organisation_id": "org-reseller", "linked_at": "2026-09-01T00:00:00Z"},
    {"organisation_id": "org-beta", "name": "Beta Ltd",
     "reseller_organisation_id": "org-reseller", "linked_at": "2026-08-01T00:00:00Z"},
])
write("keys.body", [
    {"key_id": "key-two", "entitlement_id": "ent-two", "workspace_id": "ws-beta", "tier": "team",
     "kind": "paid", "seats": 3, "issued_at": "2026-08-02T00:00:00Z",
     "paid_through": "2026-09-01T00:00:00Z", "last_seen": "2026-09-02T00:00:00Z"},
    {"key_id": "key-one", "entitlement_id": "ent-one", "workspace_id": "ws-alpha", "tier": "team",
     "kind": "paid", "seats": 5, "issued_at": "2026-09-02T00:00:00Z",
     "paid_through": "2026-10-02T00:00:00Z", "last_seen": "2026-10-02T00:00:00Z"},
])
# The workspaces the operations area answers for one organisation, which is where
# the check resolves the portal workspace id its sale names from the key's own
# Firebase project id (ws-alpha, ws-beta): a key's workspace_id is the project id
# (TN2) and the mint reads the sale's workspace_id as a workspace DOCUMENT id, so
# the two spellings are held to each other here. The file holds both customers'
# workspaces because the fake `curl` serves one document for the read and the check
# holds it to the key's own project id; a case that bends it is a case about a
# workspace the customer's key is not bound to, or about a token that may not read
# the operations area at all.
write("opsworkspaces.body", [
    {"id": "wsalpha-portal", "organisation_id": "org-alpha", "name": "Alpha",
     "project_id": "ws-alpha", "entitlement_id": "ent-alpha",
     "created_at": "2026-08-01T00:00:00Z", "bound_at": "2026-08-02T00:00:00Z"},
    {"id": "wsbeta-portal", "organisation_id": "org-beta", "name": "Beta",
     "project_id": "ws-beta", "entitlement_id": "ent-beta",
     "created_at": "2026-08-01T00:00:00Z", "bound_at": "2026-08-02T00:00:00Z"},
])

# The mint: the refusal D2189's ceiling gives, and the status the screen branches
# on. A case that bends it is a case about a build that admitted the sale.
write("mint.body", fault("over_credit_limit",
                         "This sale would pass the credit limit operations set for this reseller. "
                         "The licences it already holds are unchanged."))
write_text("mint.code", "409")

write("statements.body", [{
    "id": "org-reseller:2026-10",
    "reseller_organisation_id": "org-reseller",
    "month": "2026-10",
    "accrued_micros": 400000,
    "paid_micros": 0,
    "state": "open",
    "lines": [
        {"organisation_id": "org-alpha", "amount_micros": 250000},
        {"organisation_id": "org-beta", "amount_micros": 150000},
    ],
    "issued_at": "2026-11-01T03:00:00Z",
    "paid_at": None,
}])

# The API's own revision, with the pair portalauth.go's verifier compares a
# scheduled call against (main.tf's FUGARO_SCHEDULER_ACCOUNT and AUDIENCE).
write("service.json", {
    "metadata": {"name": "fugaro-portal-api"},
    "status": {"latestReadyRevisionName": "fugaro-portal-api-00007-chk"},
    "spec": {"template": {"spec": {"containers": [{
        "image": "us-east5-docker.pkg.dev/%s/portal/api:chk" % project,
        "env": [
            {"name": "FUGARO_SCHEDULER_ACCOUNT", "value": account},
            {"name": "FUGARO_SCHEDULER_AUDIENCE", "value": audience},
            {"name": "FUGARO_PADDLE_ENVIRONMENT", "value": "sandbox"},
        ],
    }]}}},
})

# The job P1.5 creates: a POST to the statement route, its token minted for the
# scheduler's account with the API's own URL as the audience, and enabled.
write("job.json", {
    "name": "projects/%s/locations/us-east5/jobs/fugaro-portal-statements" % project,
    "state": "ENABLED",
    "schedule": "0 3 1 * *",
    "timeZone": "Etc/UTC",
    "httpTarget": {
        "httpMethod": "POST",
        "uri": assigned + "/api/statements/run",
        "oidcToken": {"serviceAccountEmail": account, "audience": audience},
    },
})

# The API's own line for a run, which is the whole of what an operator reads of
# one (api_statements.go logs exactly this sentence).
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")
write("log.json", [{
    "timestamp": stamp,
    "textProto": "",
    "textPayload": "keyservice: the statement run as %s issued 1 statement(s) and mailed 2 message(s)" % account,
    "resource": {"type": "cloud_run_revision", "labels": {"service_name": "fugaro-portal-api"}},
}])
PY
}

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

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/tmp" "$dir/home" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/curl.calls"
  rm -f "$dir/logs/keys.gets"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and only a traced interpreter reaches the guard.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the suite that
  # runs this file may itself be started from an agent's session, and the case
  # that measures that refusal sets the marker again. CHECK575_RUN_SECONDS is 0 so
  # the fired run is read once and no case waits on a poll.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE575_DIR="$dir" CHECK575_RUN_SECONDS=0 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --project "$project" --token-file "$dir/token.txt" --api "$api" \
    ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check575\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "${CHECK575_TEST_DUMP:-}" ]; then printf -- '--- %s (exit %s)\n%s\n%s\n' "$dir" "$status" "$out" "$err" >&2; fi
}

# The token as it exists on disk: a case holds the check to never putting any of
# it — or any claim naming the person — into the output, the error stream or a
# command line.
token_line() { head -n 1 "$1/token.txt"; }
token_marker() { head -c 60 "$1/token.txt"; }

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check575.sh --project" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "--help: gcloud was called"
  [ ! -s "$dir/logs/curl.calls" ] || fail "--help: the API was called"
  expect_contains "$out" "the credit limit" "--help: what it settles"
  expect_contains "$out" "NEVER PRINTED" "--help: what it never prints"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK575_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$err" "CHECK 575 plan" "a dry run: the plan"
  expect_contains "$err" "THE ONE ACT" "a dry run: the one act it names"
  expect_contains "$err" "scheduler jobs run fugaro-portal-statements" "a dry run: the fire"
  expect_row dry-run "nothing was run" "a dry run: the row"
  expect_row terms "not reached" "a dry run: the rows it did not measure read as unreached"
  [ ! -s "$dir/logs/curl.calls" ] || fail "a dry run: the API was called"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "a dry run: gcloud was called"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row xtrace "not tracing" "the happy path: the trace guard"
  expect_row agent-session "no coding agent" "the happy path: the session guard"
  expect_row project "fugaro-staging" "the happy path: the project"
  expect_row belong "belong" "the happy path: the belong guard"
  expect_row api "401" "the happy path: the origin probe"
  expect_row token "one line at mode 0600" "the happy path: the token file"
  expect_row signin "google.com" "the happy path: the provider"
  expect_row terms "5000000 micros" "the happy path: the credit limit"
  expect_row customers "org-alpha" "the happy path: the customer read"
  expect_row keys "ws-alpha" "the happy path: the Firebase project the key is bound to"
  expect_row keys "digest" "the happy path: the before digest"
  expect_row workspace "wsalpha-portal" "the happy path: the workspace the sale names"
  expect_row workspace "ws-alpha" "the happy path: the project it was resolved from"
  expect_row service "FUGARO_SCHEDULER_AUDIENCE" "the happy path: the API's own pair"
  expect_row service "fugaro-portal-api-00007-chk" "the happy path: the revision"
  expect_row job "ENABLED" "the happy path: the job"
  expect_row job "POST /api/statements/run" "the happy path: the route it posts"
  expect_row run "issued 1 statement(s) and mailed 2 message(s)" "the happy path: the run's own line"
  expect_row run "fugaro-portal-scheduler@fugaro-staging.iam.gserviceaccount.com" "the happy path: the account the run was as"
  expect_row statements "2026-10" "the happy path: the month"
  expect_row statements "400000 micros" "the happy path: the accrual"
  expect_row limit "409" "the happy path: the refusal"
  expect_row limit "over_credit_limit" "the happy path: the refusal's code"
  expect_row limit "the licences it already holds are unchanged" "the happy path: the refusal's own sentence"
  expect_row limit "the same" "the happy path: the keys compared around it"
  expect_row restore "nothing this check wrote" "the happy path: the restore"
  expect_row scratch "removed from the exit trap" "the happy path: the scratch"
  assert_scratch_removed "the happy path"
  # The one act: the job was described and then fired, and the run read from the
  # log with the filter the API's own sentence answers.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs describe fugaro-portal-statements" "the happy path: the job described"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run fugaro-portal-statements" "the happy path: the job fired"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" 'the statement run as' "the happy path: the log filter"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services describe fugaro-portal-api" "the happy path: the API revision read"
  # The composed sale: seats read off the limit and the rate, so that its cost is
  # strictly greater than the whole credit limit.
  expect_contains "$(cat "$dir/logs/curl.calls")" "/api/ops/workspaces?organisation_id=org-alpha" "the happy path: the customer's workspaces read"
  expect_contains "$(cat "$dir/logs/mint.request.json")" '"seats":9' "the happy path: the seats the limit forces"
  # The sale names the portal workspace DOCUMENT id, never the key's own Firebase
  # project id: the mint resolves that field as a document and 404s the project id,
  # which would measure no ceiling at all.
  expect_contains "$(cat "$dir/logs/mint.request.json")" '"workspace_id":"wsalpha-portal"' "the happy path: the workspace"
  expect_absent "$(cat "$dir/logs/mint.request.json")" '"workspace_id":"ws-alpha"' "the happy path: the project id is never posted as the workspace"
  expect_contains "$(cat "$dir/logs/mint.request.json")" '"row":"team-annual"' "the happy path: the row"
  expect_contains "$(cat "$dir/logs/mint.request.json")" '"check_interval":"P1D"' "the happy path: the interval"
  python3 - "$dir/logs/mint.request.json" "$limit" "$days" "$rate" <<'PY' || fail "the happy path: the composed sale is not beyond the limit"
import json, sys
body = json.load(open(sys.argv[1]))
limit, days, rate = int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
if body["seats"] * days * rate <= limit:
    raise SystemExit(1)
PY
}

test_the_sign_in_never_leaves_the_config_file() {
  local dir=$work/secret
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the sign-in"
  expect_absent "$out$err" "$(token_marker "$dir")" "the sign-in: the token is not in the output"
  expect_absent "$(cat "$dir/logs/curl.calls")" "$(token_marker "$dir")" "the sign-in: the token is not in a command line"
  expect_absent "$(cat "$dir/logs/curl.calls")" "Bearer" "the sign-in: not even the header is recorded"
  expect_absent "$out$err" "chk575-person@example.invalid" "the sign-in: the address is never printed"
  expect_absent "$out$err" "uid-chk575-person" "the sign-in: the uid is never printed"
  # Every portal call carried a config at mode 0600 holding the Authorization
  # header, and the anonymous probe carried none.
  expect_contains "$(cat "$dir/logs/curl.calls")" "auth=yes" "the sign-in: the header travelled in a config"
  expect_contains "$(cat "$dir/logs/curl.calls")" "mode=600" "the sign-in: the config is 0600"
  expect_absent "$(cat "$dir/logs/curl.calls")" "auth=yes (in" "the sign-in: the record is well formed"
  local anonymous
  anonymous=$(grep 'GET /api/workspaces' "$dir/logs/curl.calls" | head -n 1)
  expect_contains "$anonymous" "config=none" "the sign-in: the probe carries no sign-in"
}

test_a_token_file_that_is_not_0600_is_unmeasured() {
  local dir=$work/mode
  prepare "$dir"
  chmod 644 "$dir/token.txt"
  run_check "$dir"
  expect_status "$status" 2 "a token file at 0644"
  expect_row token "not 600" "a token file at 0644: the row"
  assert_scratch_removed "a token file at 0644"
  [ ! -s "$dir/logs/curl.calls" ] || fail "a token file at 0644: the API was called"
}

test_a_token_file_that_is_not_there_is_unmeasured() {
  local dir=$work/no-token
  prepare "$dir"
  rm -f "$dir/token.txt"
  run_check "$dir"
  expect_status "$status" 2 "a token file that is not there"
  expect_row token "is not there" "a token file that is not there: the row"
}

test_a_token_file_with_a_second_line_is_unmeasured() {
  local dir=$work/two-lines
  prepare "$dir"
  printf 'and-a-second-line\n' >>"$dir/token.txt"
  run_check "$dir"
  expect_status "$status" 2 "a token file with two lines"
  expect_row token "line(s)" "a token file with two lines: the row"
}

test_a_text_that_is_not_a_token_is_unmeasured() {
  local dir=$work/not-a-jwt
  prepare "$dir"
  printf 'not-a-token\n' >"$dir/token.txt"
  chmod 600 "$dir/token.txt"
  run_check "$dir"
  expect_status "$status" 2 "a file that holds no token"
  expect_row signin "not an ID token" "a file that holds no token: the row"
}

test_an_email_link_account_is_unmeasured() {
  local dir=$work/email-link
  prepare "$dir"
  write_token "$dir" emailLink "$project" 3600
  run_check "$dir"
  expect_status "$status" 2 "an email-link account"
  expect_row signin "email-link" "an email-link account: the row"
  expect_row signin "Check 572" "an email-link account: the check that owns that refusal"
  expect_absent "$(cat "$dir/logs/curl.calls")" "auth=yes" "an email-link account: no call carried the sign-in"
}

test_a_provider_that_is_neither_is_unmeasured() {
  local dir=$work/other-provider
  prepare "$dir"
  write_token "$dir" saml.acme "$project" 3600
  run_check "$dir"
  expect_status "$status" 2 "a provider that is neither"
  expect_row signin "saml.acme" "a provider that is neither: the row"
}

test_a_token_of_another_project_is_unmeasured() {
  local dir=$work/other-aud
  prepare "$dir"
  write_token "$dir" google.com fugaro-somewhere-else 3600
  run_check "$dir"
  expect_status "$status" 2 "a token of another project"
  expect_row signin "aud is 'fugaro-somewhere-else'" "a token of another project: the row"
}

test_an_expired_token_is_unmeasured() {
  local dir=$work/expired
  prepare "$dir"
  write_token "$dir" google.com "$project" -120
  run_check "$dir"
  expect_status "$status" 2 "an expired token"
  expect_row signin "expired" "an expired token: the row"
}

test_an_origin_that_is_not_the_api_is_unmeasured() {
  local dir=$work/not-the-api
  prepare "$dir"
  printf '200' >"$dir/anonymous.code"
  python3 -c 'open("'"$dir"'/anonymous.body","w").write("<html>a landing page</html>")'
  run_check "$dir"
  expect_status "$status" 2 "an origin that is not the API"
  expect_row api "rather than 401" "an origin that is not the API: the row"
  expect_row api "--api" "an origin that is not the API: the fix"
  [ ! -s "$dir/logs/mint.request.json" ] 2>/dev/null || fail "an origin that is not the API: a sale was composed"
}

test_an_origin_that_answers_another_refusal_is_unmeasured() {
  local dir=$work/other-fault
  prepare "$dir"
  python3 -c 'import json; json.dump({"error": {"code": "unauthorized", "message": "no"}}, open("'"$dir"'/anonymous.body", "w"))'
  run_check "$dir"
  expect_status "$status" 2 "an origin that answers another code"
  expect_row api "'unauthorized'" "an origin that answers another code: the row"
}

test_a_caller_who_is_not_a_reseller_is_unmeasured() {
  local dir=$work/not-reseller
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "not_reseller", "message": "this account is not a member of a reseller organisation"}},
          open(sys.argv[1] + "/terms.body", "w"))
PY
  printf '403' >"$dir/terms.code"
  run_check "$dir"
  expect_status "$status" 2 "a caller who is not a reseller"
  expect_row terms "not_reseller" "a caller who is not a reseller: the row"
  expect_row terms "membership" "a caller who is not a reseller: the fix"
}

test_a_caller_who_must_sign_in_with_sso_is_unmeasured() {
  local dir=$work/sso-required
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "sso_required", "message": "this organisation's members sign in with Google or Microsoft"}},
          open(sys.argv[1] + "/terms.body", "w"))
PY
  printf '403' >"$dir/terms.code"
  run_check "$dir"
  expect_status "$status" 2 "a caller the SSO rule refuses"
  expect_row terms "sso_required" "a caller the SSO rule refuses: the row"
}

test_a_terms_document_that_is_not_the_terms_is_unmeasured() {
  local dir=$work/odd-terms
  prepare "$dir"
  printf '{"organisation_id":"org-reseller"}\n' >"$dir/terms.body"
  run_check "$dir"
  expect_status "$status" 2 "a terms document this check cannot read"
  expect_row terms "cannot read as the terms" "a terms document this check cannot read: the row"
}

test_a_reseller_with_no_customer_is_unmeasured() {
  local dir=$work/no-customers
  prepare "$dir"
  printf '[]\n' >"$dir/customers.body"
  run_check "$dir"
  expect_status "$status" 2 "a reseller with no customer"
  expect_row customers "no customer at all" "a reseller with no customer: the row"
}

test_a_customer_the_check_was_not_given_is_unmeasured() {
  local dir=$work/other-customer
  prepare "$dir"
  run_check "$dir" -- --customer org-gamma
  expect_status "$status" 2 "a customer this reseller does not hold"
  expect_row customers "--customer org-gamma" "a customer this reseller does not hold: the row"
}

test_the_named_customer_is_the_one_read() {
  local dir=$work/named-customer
  prepare "$dir"
  run_check "$dir" -- --customer org-beta
  expect_status "$status" 0 "the customer this check was given"
  expect_row customers "this check reads org-beta" "the customer this check was given: the row"
}

test_a_customer_with_no_key_is_unmeasured() {
  local dir=$work/no-keys
  prepare "$dir"
  printf '[]\n' >"$dir/keys.body"
  run_check "$dir"
  expect_status "$status" 2 "a customer with no key"
  expect_row keys "holds no key at all" "a customer with no key: the row"
}

test_a_key_with_no_workspace_is_unmeasured() {
  local dir=$work/no-workspace
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/keys.body"))
for key in doc:
    key["workspace_id"] = ""
json.dump(doc, open(sys.argv[1] + "/keys.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a key with no workspace"
  expect_row keys "carry no workspace_id" "a key with no workspace: the row"
}

test_a_workspace_named_by_hand_is_used() {
  local dir=$work/workspace-by-hand
  prepare "$dir"
  run_check "$dir" -- --workspace-id wsalpha-by-hand
  expect_status "$status" 0 "a workspace named by hand"
  expect_row workspace "wsalpha-by-hand" "a workspace named by hand: the row"
  expect_absent "$(cat "$dir/logs/curl.calls")" "/api/ops/workspaces" "a workspace named by hand: nothing was read to resolve it"
  expect_contains "$(cat "$dir/logs/mint.request.json")" '"workspace_id":"wsalpha-by-hand"' "a workspace named by hand: the sale names it"
}

test_a_workspace_that_is_not_bound_to_the_key_is_unmeasured() {
  local dir=$work/no-binding
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/opsworkspaces.body"))
for row in doc:
    row["project_id"] = None
json.dump(doc, open(sys.argv[1] + "/opsworkspaces.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a workspace the key's project is not bound to"
  expect_row workspace "none of them is bound" "a workspace the key's project is not bound to: the row"
  expect_row workspace "--workspace-id" "a workspace the key's project is not bound to: what to do"
  expect_row limit "not reached" "a workspace the key's project is not bound to: no sale was composed"
  [ ! -f "$dir/logs/mint.request.json" ] || fail "a workspace the key's project is not bound to: the sale was posted"
}

test_a_token_that_may_not_read_the_operations_area_is_unmeasured() {
  local dir=$work/not-operations
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "not_operations",
                     "message": "These screens are the operations team's, and this sign-in is not one of them."}},
          open(sys.argv[1] + "/opsworkspaces.body", "w"))
open(sys.argv[1] + "/opsworkspaces.code", "w").write("403")
PY
  run_check "$dir"
  expect_status "$status" 2 "a token that may not read the operations area"
  expect_row workspace "not_operations" "a token that may not read the operations area: the row"
  expect_row workspace "--workspace-id" "a token that may not read the operations area: what to do"
  [ ! -f "$dir/logs/mint.request.json" ] || fail "a token that may not read the operations area: the sale was posted"
}

test_a_sale_whose_workspace_the_mint_cannot_resolve_is_unmeasured() {
  # The failure the workspace resolution exists for: the mint reads the sale's
  # workspace_id as a workspace DOCUMENT id, so a sale naming the key's own Firebase
  # project id is answered 404 'not_found' before the credit limit is compared —
  # which measures no ceiling at all. A run given the project id is here held to
  # reading that answer as its own row rather than as a refusal over the limit.
  local dir=$work/mint-unresolved
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "not_found", "message": "This customer does not exist."}},
          open(sys.argv[1] + "/mint.body", "w"))
open(sys.argv[1] + "/mint.code", "w").write("404")
PY
  run_check "$dir" -- --workspace-id ws-alpha
  expect_status "$status" 2 "a sale the mint cannot resolve a workspace for"
  expect_row limit "404" "a sale the mint cannot resolve a workspace for: the row"
  expect_row limit "--workspace-id" "a sale the mint cannot resolve a workspace for: what to do"
}

test_a_revision_without_the_scheduler_pair_is_unmeasured() {
  local dir=$work/no-pair
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/service.json"))
doc["spec"]["template"]["spec"]["containers"][0]["env"] = [
    {"name": "FUGARO_PADDLE_ENVIRONMENT", "value": "sandbox"}]
json.dump(doc, open(sys.argv[1] + "/service.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a revision without the pair"
  expect_row service "no FUGARO_SCHEDULER_ACCOUNT" "a revision without the pair: the row"
  expect_row service "NewSchedulerVerifier" "a revision without the pair: why it matters"
  # The row it could not measure does not stop the one act: the run is still read.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run" "a revision without the pair: the job is still fired"
  expect_row run "issued 1 statement(s)" "a revision without the pair: the run is still measured"
}

test_a_revision_the_account_cannot_read_is_unmeasured() {
  local dir=$work/service-refused
  prepare "$dir"
  printf '1\n' >"$dir/service.exit"
  printf 'ERROR: (gcloud.run.services.describe) PERMISSION_DENIED\n' >"$dir/service.err"
  run_check "$dir"
  expect_status "$status" 2 "a revision this account cannot read"
  expect_row service "roles/run.viewer" "a revision this account cannot read: the row"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run" "a revision this account cannot read: the job is still fired"
}

test_a_job_that_is_not_there_is_unmeasured() {
  local dir=$work/no-job
  prepare "$dir"
  rm -f "$dir/job.json"
  run_check "$dir"
  expect_status "$status" 2 "a job the deployment does not have"
  expect_row job "NOT_FOUND" "a job the deployment does not have: the row"
  expect_row job "--statement-job" "a job the deployment does not have: the fix"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run" "a job the deployment does not have: nothing is fired"
}

test_a_job_that_posts_elsewhere_is_a_finding() {
  local dir=$work/wrong-path
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["httpTarget"]["uri"] = "https://fugaro-portal-api-abc123-uc.a.run.app/api/statement/run"
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a job that posts elsewhere"
  expect_row job "does not reach the route" "a job that posts elsewhere: the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run" "a job that posts elsewhere: nothing is fired"
}

test_a_job_token_from_another_project_is_a_finding() {
  local dir=$work/other-account
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["httpTarget"]["oidcToken"]["serviceAccountEmail"] = "scheduler@fugaro-other.iam.gserviceaccount.com"
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a token from another project's account"
  expect_row job "not an account in fugaro-staging" "a token from another project's account: the row"
}

test_a_job_whose_audience_is_the_assigned_url_is_a_finding() {
  local dir=$work/assigned-audience
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["httpTarget"]["oidcToken"]["audience"] = doc["httpTarget"]["uri"]
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a token minted for the assigned URL"
  expect_row job "the target's own assigned URL" "a token minted for the assigned URL: the row"
  expect_row job "scheduler.tf" "a token minted for the assigned URL: where the trap is written"
}

test_a_job_whose_audience_is_not_the_apis_is_a_finding() {
  local dir=$work/other-audience
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["httpTarget"]["oidcToken"]["audience"] = "https://somewhere-else.example"
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an audience the API does not compare against"
  expect_row job "FUGARO_SCHEDULER_AUDIENCE 'https://fugaro-portal-api-575575575575.us-east5.run.app'" "an audience the API does not compare against: the row"
}

test_a_job_whose_account_is_not_the_apis_is_a_finding() {
  local dir=$work/other-account-in-project
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["httpTarget"]["oidcToken"]["serviceAccountEmail"] = "another-job@fugaro-staging.iam.gserviceaccount.com"
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an account the API does not compare against"
  expect_row job "FUGARO_SCHEDULER_ACCOUNT" "an account the API does not compare against: the row"
}

test_a_paused_job_is_unmeasured_and_still_fired() {
  local dir=$work/paused
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/job.json"))
doc["state"] = "PAUSED"
json.dump(doc, open(sys.argv[1] + "/job.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a job the deployment left paused"
  expect_row job "PAUSED" "a paused job: the row"
  expect_row job "statements_paused=false" "a paused job: the fix"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "scheduler jobs run" "a paused job: the check fires it by hand"
  expect_row run "issued 1 statement(s)" "a paused job: the run is still measured"
}

test_a_run_the_log_does_not_carry_is_a_finding() {
  local dir=$work/no-run
  prepare "$dir"
  printf '[]\n' >"$dir/log.json"
  run_check "$dir"
  expect_status "$status" 1 "a fire that left no line"
  expect_row run "logged no statement run" "a fire that left no line: the row"
  expect_row run "scheduler jobs describe" "a fire that left no line: where to look"
}

test_a_run_line_from_another_account_is_unmeasured() {
  local dir=$work/other-run
  prepare "$dir"
  python3 - "$dir" <<'PY'
import datetime, json, sys
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")
json.dump([{"timestamp": stamp,
            "textPayload": "keyservice: the statement run as someone-else@fugaro-staging.iam.gserviceaccount.com issued 3 statement(s) and mailed 4 message(s)"}],
          open(sys.argv[1] + "/log.json", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a run line that is not this fire's"
  expect_row run "someone-else@fugaro-staging.iam.gserviceaccount.com" "a run line that is not this fire's: the row"
}

test_a_fire_the_account_may_not_make_is_unmeasured() {
  local dir=$work/no-runner
  prepare "$dir"
  printf '1\n' >"$dir/jobrun.exit"
  printf 'ERROR: (gcloud.scheduler.jobs.run) PERMISSION_DENIED\n' >"$dir/jobrun.err"
  run_check "$dir"
  expect_status "$status" 2 "an account that may not fire the job"
  expect_row run "cloudscheduler.jobRunner" "an account that may not fire the job: the fix"
  expect_row job "ENABLED" "an account that may not fire the job: the job row is still measured"
}

test_a_log_read_that_is_refused_is_unmeasured() {
  local dir=$work/no-log
  prepare "$dir"
  printf '1\n' >"$dir/log.exit"
  printf 'ERROR: (gcloud.logging.read) PERMISSION_DENIED\n' >"$dir/log.err"
  run_check "$dir"
  expect_status "$status" 2 "an account that may not read the log"
  expect_row run "logging.viewer" "an account that may not read the log: the fix"
}

test_a_reseller_with_no_statement_is_unmeasured() {
  local dir=$work/no-statement
  prepare "$dir"
  printf '[]\n' >"$dir/statements.body"
  run_check "$dir"
  expect_status "$status" 2 "a reseller with no statement"
  expect_row statements "no statement at all" "a reseller with no statement: the row"
}

test_a_month_that_is_not_over_is_a_finding() {
  local dir=$work/this-month
  prepare "$dir"
  python3 - "$dir" <<'PY'
import datetime, json, sys
doc = json.load(open(sys.argv[1] + "/statements.body"))
doc[0]["month"] = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m")
json.dump(doc, open(sys.argv[1] + "/statements.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a month still in progress"
  expect_row statements "not a whole UTC month that is over" "a month still in progress: the row"
}

test_lines_that_do_not_sum_is_a_finding() {
  local dir=$work/bad-sum
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/statements.body"))
doc[0]["lines"][0]["amount_micros"] = 100000
json.dump(doc, open(sys.argv[1] + "/statements.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "lines that do not sum to the accrual"
  expect_row statements "250000 micros and the statement says it accrued 400000" "lines that do not sum to the accrual: the row"
}

test_a_line_for_another_organisation_is_a_finding() {
  local dir=$work/foreign-line
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/statements.body"))
doc[0]["lines"][1]["organisation_id"] = "org-gamma"
json.dump(doc, open(sys.argv[1] + "/statements.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a line for an organisation this reseller does not hold"
  expect_row statements "org-gamma" "a line for another organisation: the row"
  expect_row statements "not customers of this reseller" "a line for another organisation: why it matters"
}

test_a_state_that_does_not_follow_the_amounts_is_a_finding() {
  local dir=$work/bad-state
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/statements.body"))
doc[0]["paid_micros"] = 100000
doc[0]["state"] = "paid"
json.dump(doc, open(sys.argv[1] + "/statements.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a state its figures do not support"
  expect_row statements "'paid' with 400000 accrued and 100000 paid" "a state its figures do not support: the row"
}

test_a_line_of_nothing_is_a_finding() {
  local dir=$work/zero-line
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/statements.body"))
doc[0]["lines"][0]["amount_micros"] = 0
doc[0]["accrued_micros"] = 150000
json.dump(doc, open(sys.argv[1] + "/statements.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a line of nothing"
  expect_row statements "include a line of nothing" "a line of nothing: the row"
}

test_a_sale_beyond_the_limit_that_is_admitted_is_a_finding() {
  local dir=$work/admitted
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"key_id": "key-three", "entitlement_id": "ent-three", "workspace_id": "ws-alpha",
           "tier": "team", "kind": "paid", "seats": 9, "issued_at": "2026-11-21T00:00:00Z",
           "paid_through": "2026-12-20T00:00:00Z", "last_seen": ""},
          open(sys.argv[1] + "/mint.body", "w"))
PY
  printf '201' >"$dir/mint.code"
  run_check "$dir"
  expect_status "$status" 1 "a sale beyond the limit that was admitted"
  expect_row limit "the sale was ADMITTED" "a sale beyond the limit that was admitted: the row"
  expect_row limit "D2189" "a sale beyond the limit that was admitted: what it breaks"
  expect_row limit "5220000 micros" "a sale beyond the limit that was admitted: the cost the check composed"
}

test_a_sale_refused_with_another_code_is_unmeasured() {
  local dir=$work/other-refusal
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "invalid_row", "message": "that row is not one this deployment sells"}},
          open(sys.argv[1] + "/mint.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a refusal that is not the credit limit's"
  expect_row limit "409 with code 'invalid_row'" "a refusal that is not the credit limit's: the row"
}

test_a_sale_refused_for_its_own_reason_is_unmeasured() {
  local dir=$work/row-itself
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "invalid_term", "message": "that term is longer than the row allows"}},
          open(sys.argv[1] + "/mint.body", "w"))
PY
  printf '422' >"$dir/mint.code"
  run_check "$dir"
  expect_status "$status" 2 "a sale the row itself refuses"
  expect_row limit "422 'invalid_term'" "a sale the row itself refuses: the row"
  expect_row limit "--days" "a sale the row itself refuses: the fix"
}

test_a_sale_refused_with_another_sentence_is_unmeasured() {
  local dir=$work/other-sentence
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
json.dump({"error": {"code": "over_credit_limit", "message": "no"}},
          open(sys.argv[1] + "/mint.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a refusal with another sentence"
  expect_row limit "not refuseResellerMint's own" "a refusal with another sentence: the row"
}

test_a_sale_the_limit_would_admit_is_unmeasured() {
  local dir=$work/seat-too-few
  prepare "$dir"
  run_check "$dir" -- --seats 1
  expect_status "$status" 2 "a sale the limit would admit"
  expect_row limit "does not clear it" "a sale the limit would admit: the row"
  expect_absent "$(cat "$dir/logs/curl.calls")" "POST" "a sale the limit would admit: nothing was composed"
}

test_a_row_the_reseller_is_not_priced_for_is_unmeasured() {
  local dir=$work/unpriced
  prepare "$dir"
  run_check "$dir" -- --row enterprise
  expect_status "$status" 2 "a row this reseller is not priced for"
  expect_row limit "priced for no 'enterprise' row" "a row this reseller is not priced for: the row"
}

test_a_row_priced_at_nothing_is_unmeasured() {
  local dir=$work/free-row
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/terms.body"))
for rate in doc["rates"]:
    if rate["row"] == "team-annual":
        rate["daily_rate_micros"] = 0
json.dump(doc, open(sys.argv[1] + "/terms.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 2 "a row that costs nothing"
  expect_row limit "a sale of it costs nothing" "a row that costs nothing: the row"
}

test_a_refusal_that_wrote_a_key_is_a_finding() {
  local dir=$work/wrote-a-key
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1] + "/keys.body"))
doc.append({"key_id": "key-three", "entitlement_id": "ent-three", "workspace_id": "ws-alpha",
            "tier": "team", "kind": "paid", "seats": 9, "issued_at": "2026-11-21T00:00:00Z",
            "paid_through": "2026-12-20T00:00:00Z", "last_seen": ""})
json.dump(doc, open(sys.argv[1] + "/keys-after.body", "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a refusal that wrote a licence"
  expect_row limit "changed the customer's keys" "a refusal that wrote a licence: the row"
  expect_row limit "key-three" "a refusal that wrote a licence: what it wrote"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir" fugaro-prod
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_row aborted "refused unless --production" "fugaro-prod without --production: the row"
  expect_row api "not reached" "fugaro-prod without --production: nothing was read"
  expect_contains "$err" "refused unless --production" "fugaro-prod without --production: the refusal"
  [ ! -s "$dir/logs/curl.calls" ] || fail "fugaro-prod without --production: the API was called"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "fugaro-prod without --production: gcloud was called"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prod-named
  prepare "$dir" fugaro-prod
  run_check "$dir" -- --project fugaro-prod --production
  expect_status "$status" 0 "fugaro-prod named with --production"
  expect_row project "production, named with --production" "fugaro-prod named with --production: the row"
  expect_row signin "fugaro-prod" "fugaro-prod named with --production: the token's own project"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project staging-belong-1
  expect_status "$status" 1 "a value naming belong"
  expect_contains "$err" "names belong" "a value naming belong: the refusal"
  [ ! -s "$dir/logs/curl.calls" ] || fail "a value naming belong: the API was called"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "an agent session"
  expect_row aborted "CLAUDECODE" "an agent session: the row"
  [ ! -s "$dir/logs/curl.calls" ] || fail "an agent session: the API was called"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "an agent session: gcloud was called"
}

test_a_traced_shell_is_refused() {
  local dir=$work/traced
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced shell"
  expect_row aborted "is tracing" "a traced shell: the row"
  expect_absent "$err" "$(token_marker "$dir")" "a traced shell: the trace never carries the token"
  [ ! -s "$dir/logs/curl.calls" ] || fail "a traced shell: the API was called"
}

test_a_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK575_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch"
  expect_row scratch "was kept" "a kept scratch: the row"
  [ -d "$scratch" ] || fail "a kept scratch: the directory is gone"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --not-a-flag
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--not-a-flag'" "an unknown argument: the refusal"
}

test_a_project_that_is_missing_is_refused() {
  local dir=$work/no-project
  prepare "$dir"
  run_check "$dir" -- --project ""
  expect_status "$status" 2 "a run with no project"
  expect_contains "$err" "needs --project" "a run with no project: the refusal"
}

test_index=0
t test_help
t test_dry_run
t test_happy_path
t test_the_sign_in_never_leaves_the_config_file
t test_a_token_file_that_is_not_0600_is_unmeasured
t test_a_token_file_that_is_not_there_is_unmeasured
t test_a_token_file_with_a_second_line_is_unmeasured
t test_a_text_that_is_not_a_token_is_unmeasured
t test_an_email_link_account_is_unmeasured
t test_a_provider_that_is_neither_is_unmeasured
t test_a_token_of_another_project_is_unmeasured
t test_an_expired_token_is_unmeasured
t test_an_origin_that_is_not_the_api_is_unmeasured
t test_an_origin_that_answers_another_refusal_is_unmeasured
t test_a_caller_who_is_not_a_reseller_is_unmeasured
t test_a_caller_who_must_sign_in_with_sso_is_unmeasured
t test_a_terms_document_that_is_not_the_terms_is_unmeasured
t test_a_reseller_with_no_customer_is_unmeasured
t test_a_customer_the_check_was_not_given_is_unmeasured
t test_the_named_customer_is_the_one_read
t test_a_customer_with_no_key_is_unmeasured
t test_a_key_with_no_workspace_is_unmeasured
t test_a_workspace_named_by_hand_is_used
t test_a_workspace_that_is_not_bound_to_the_key_is_unmeasured
t test_a_token_that_may_not_read_the_operations_area_is_unmeasured
t test_a_sale_whose_workspace_the_mint_cannot_resolve_is_unmeasured
t test_a_revision_without_the_scheduler_pair_is_unmeasured
t test_a_revision_the_account_cannot_read_is_unmeasured
t test_a_job_that_is_not_there_is_unmeasured
t test_a_job_that_posts_elsewhere_is_a_finding
t test_a_job_token_from_another_project_is_a_finding
t test_a_job_whose_audience_is_the_assigned_url_is_a_finding
t test_a_job_whose_audience_is_not_the_apis_is_a_finding
t test_a_job_whose_account_is_not_the_apis_is_a_finding
t test_a_paused_job_is_unmeasured_and_still_fired
t test_a_run_the_log_does_not_carry_is_a_finding
t test_a_run_line_from_another_account_is_unmeasured
t test_a_fire_the_account_may_not_make_is_unmeasured
t test_a_log_read_that_is_refused_is_unmeasured
t test_a_reseller_with_no_statement_is_unmeasured
t test_a_month_that_is_not_over_is_a_finding
t test_lines_that_do_not_sum_is_a_finding
t test_a_line_for_another_organisation_is_a_finding
t test_a_state_that_does_not_follow_the_amounts_is_a_finding
t test_a_line_of_nothing_is_a_finding
t test_a_sale_beyond_the_limit_that_is_admitted_is_a_finding
t test_a_sale_refused_with_another_code_is_unmeasured
t test_a_sale_refused_for_its_own_reason_is_unmeasured
t test_a_sale_refused_with_another_sentence_is_unmeasured
t test_a_sale_the_limit_would_admit_is_unmeasured
t test_a_row_the_reseller_is_not_priced_for_is_unmeasured
t test_a_row_priced_at_nothing_is_unmeasured
t test_a_refusal_that_wrote_a_key_is_a_finding
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_a_project_naming_belong_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_a_project_that_is_missing_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check575_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check575_test.sh: ok"
