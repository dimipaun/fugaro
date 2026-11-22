#!/usr/bin/env bash
# Tests for scripts/live-checks/check564.sh, with a fake `curl`, a fake `fugaro`
# and a fake `gcloud` on PATH: NOTHING here reaches a cloud, a Paddle account, a
# portal or an installation, and no live command runs. Each fake records every
# call it is given — the method, the route, whether an authorization config was
# carried and which file it was, with the directory it was made in — so a case can
# assert BOTH what the check read and what it did NOT read.
#
# The fake `curl` serves both ends out of the case's files: the portal's routes
# (`/api/...`) and the Paddle SANDBOX's (`/prices`, `/transactions/{id}`,
# `/subscriptions/{id}`). The entitlement and the workspace mirror are served by
# ROTATION, so a case can give the check a purchase that is active at the first
# look, renewed at the second and revoked at the third — the three states K2.5
# moves a licence through. No body here is a real Paddle shape; each is the
# case's own file.
#
# The fake `gcloud` serves the renewal Scheduler job and the licence Cloud Run
# job: the job's own location and document (`scheduler.served` says which locations
# Scheduler serves, so a case can put the job somewhere OTHER than the
# installation's Cloud Run region, which is the state K4.2's module works around),
# the two `scheduler jobs run` fires, and the job's executions — an older one that
# ran before the fire and the one the fire starts, so a case can say which of them
# the tick row read. The fake `fugaro` whose `licence login` prints an authorize
# address, and whose `licence status --json` resolves the `at` the fixture carries
# into a check recorded after this run's refund or an hour before it.
#
# Usage: bash scripts/live-checks/check564_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check564_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check564_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check564.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK564_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK564_TEST_HEAD:-1600}")" ;;
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
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}
# no_calls: no fake was given anything at all, which is how a case says a guard or
# a setup row stopped the check before it reached the portal, the sandbox or an
# installation.
no_calls() { # what
  local l
  for l in curl.calls fugaro.calls gcloud.calls; do
    if [ -s "$dir/state/logs/$l" ]; then
      fail "$1: a call was made ($l): $(cat "$dir/state/logs/$l")"
    fi
  done
}

project=chk564-proj
api=https://portal.chk564.test
sandbox_api=https://sandbox-api.paddle.com
workspace=ws-chk564
txn=txn_sandbox_1
sub=sub_sandbox_1
# The two executions of the licence Cloud Run job the fixture holds: the one an
# earlier tick started, and the one the fire this run makes starts.
exec_older=fugarolicence-old99
exec_fresh=fugarolicence-abc12
# The two values a case writes into a file and asserts the check never prints:
# marker words and not credentials, so a file here is a key's SHAPE and nothing
# anybody could present.
token_marker=CHK564-NOT-A-TOKEN
paddle_marker=CHK564-NOT-A-PADDLE-KEY

# --------------------------------------------------------------------- fakes
write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check564_test.sh only: the
# portal's routes and the Paddle sandbox's are answered out of $FAKE564_DIR, and
# every request is recorded there — the method, the route, whether a config was
# carried, which file it was and what bearer it holds, so a case can assert BOTH
# what the check read and that neither secret ever travelled on a command line.
# Nothing here is a real endpoint.
set -euo pipefail
dir=${FAKE564_DIR:?}
mkdir -p "$dir/logs"
conf="" out="" url="" method=GET
while [ $# -gt 0 ]; do
  case "$1" in
  --config) conf=${2:-}; shift 2 ;;
  -o) out=${2:-}; shift 2 ;;
  -X) method=${2:-GET}; shift 2 ;;
  -w | --max-time | -H | --data-binary) shift 2 ;;
  -*) shift ;;
  *) url=$1; shift ;;
  esac
done
path=${url#*://}
path=/${path#*/}
cfg=no
[ -n "$conf" ] && cfg=yes
printf 'curl %s %s (config %s) (file %s) (in %s)\n' "$method" "$path" "$cfg" "${conf:-none}" "$PWD" >>"$dir/logs/curl.calls"
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >>"$dir/logs/bearers" 2>/dev/null || true
  python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$dir/logs/config.mode" 2>/dev/null || printf 'unknown\n' >"$dir/logs/config.mode"
fi
# rot BASE: the body to serve, rotating BASE.1, BASE.2, ... so a case can move the
# entitlement (or the mirror) between the check's looks. The counter lives in the
# case's directory because each curl is a process of its own.
rot() {
  local base=$1 name n
  name=$(basename "$base")
  n=$(cat "$dir/counter.$name" 2>/dev/null || printf '0')
  n=$((n + 1))
  printf '%s' "$n" >"$dir/counter.$name"
  if [ -f "$base.$n" ]; then printf '%s' "$base.$n"; else printf '%s' "$base"; fi
}
serve() { # serve BASE — the body is BASE.body (rotating BASE.body.1, ...), the code BASE.code
  local base=$1 file code
  file=$(rot "$base.body")
  if [ -n "$out" ]; then cp "$file" "$out"; fi
  code="$base.code"
  [ -f "$code" ] || code=/dev/null
  tr -d '\n' <"$code"
}
case "$path" in
/api/workspaces)
  if [ -n "$conf" ]; then serve "$dir/workspaces"; else serve "$dir/anonymous"; fi
  ;;
/api/entitlements) serve "$dir/entitlements" ;;
/api/workspaces/*/subscription) serve "$dir/mirror" ;;
/api/checkout) serve "$dir/checkout" ;;
/prices*) serve "$dir/prices" ;;
/transactions/*) serve "$dir/txn" ;;
/subscriptions/*) serve "$dir/sub" ;;
*)
  printf 'fake curl: no route for %s %s\n' "$method" "$path" >&2
  exit 7
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for the released `fugaro`, for scripts/live-checks/check564_test.sh
# only: `doctor --json`, `licence login` and `licence status --json` answer out of
# $FAKE564_DIR, and every call is recorded so a case can assert which reads the
# check made and that none of them was a write.
set -euo pipefail
dir=${FAKE564_DIR:?}
mkdir -p "$dir/logs"
printf 'fugaro %s\n' "$*" >>"$dir/logs/fugaro.calls"
case "${1:-}" in
doctor)
  cat "$dir/doctor.json"
  exit "$(cat "$dir/doctor.status" 2>/dev/null || printf '0')"
  ;;
licence)
  if [ "${2:-}" = login ]; then
    printf 'open this address to finish the sign-in: https://licence.chk564.example/cli/authorize?state=chk564&code_challenge=chk564\n'
    printf 'waiting for the sign-in to come back\n' >&2
    exit "$(cat "$dir/login.status" 2>/dev/null || printf '0')"
  fi
  # `licence status --json`. The `at` the fixture carries is a PLACEHOLDER and is
  # resolved here, at the moment of this read: `after` is a second PAST the read
  # and `before` an hour before it, so a case can give the installation a check
  # recorded after this run's own refund instant — or one recorded long before it —
  # without either side racing a clock. (The check compares whole seconds, and its
  # own refund instant is the second it saw the refund land, so "after" is a second
  # past rather than the same second.)
  python3 - "$dir/status.json" <<'PY'
import datetime, json, sys
doc = json.load(open(sys.argv[1]))
last = doc.get("last_check")
if isinstance(last, dict):
    now = datetime.datetime.now(datetime.timezone.utc)
    if last.get("at") == "after":
        last["at"] = (now + datetime.timedelta(seconds=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
    elif last.get("at") == "before":
        last["at"] = (now - datetime.timedelta(seconds=3600)).strftime("%Y-%m-%dT%H:%M:%SZ")
print(json.dumps(doc))
PY
  exit "$(cat "$dir/status.status" 2>/dev/null || printf '0')"
  ;;
*)
  printf 'fake fugaro: no route for %s\n' "$*" >&2
  exit 7
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check564_test.sh only: the
# Scheduler job's own location and document, the licence Cloud Run job's
# executions, and the two `scheduler jobs run` fires all answer out of
# $FAKE564_DIR, and every call is recorded, so a case reads the location each fire
# was aimed at and which executions were looked at. Scheduler is NOT served in the
# installation's own Cloud Run region here (list `$dir/scheduler.served`), which is
# the state K4.2's module works around by placing the job in the nearest location
# it can.
set -euo pipefail
dir=${FAKE564_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s\n' "$*" >>"$dir/logs/gcloud.calls"
served() { # served LOCATION — 0 when the fixture says Scheduler serves it
  [ -f "$dir/scheduler.served" ] || return 1
  local l
  for l in $(cat "$dir/scheduler.served"); do
    [ "$l" = "$1" ] && return 0
  done
  return 1
}
resolve() { # resolve FILE — the `after`/`before` placeholders of a fixture
  python3 - "$1" <<'PY'
import datetime, json, sys
now = datetime.datetime.now(datetime.timezone.utc)

def at(v):
    if v == "after":
        return now.strftime("%Y-%m-%dT%H:%M:%SZ")
    if v == "before":
        return (now - datetime.timedelta(seconds=3600)).strftime("%Y-%m-%dT%H:%M:%SZ")
    return v

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
for e in doc if isinstance(doc, list) else []:
    if not isinstance(e, dict):
        continue
    md = e.get("metadata")
    if isinstance(md, dict) and "creationTimestamp" in md:
        md["creationTimestamp"] = at(md["creationTimestamp"])
    st = e.get("status")
    if isinstance(st, dict) and st.get("completionTime"):
        st["completionTime"] = at(st["completionTime"])
print(json.dumps(doc))
PY
}
case "${1:-} ${2:-}" in
"scheduler locations")
  if [ -f "$dir/locations.status" ]; then exit "$(cat "$dir/locations.status")"; fi
  cat "$dir/locations.json"
  ;;
"scheduler jobs")
  case "${3:-}" in
  run)
    if [ -f "$dir/renew.status" ]; then exit "$(cat "$dir/renew.status")"; fi
    printf '{"name":"fugaro-licence-renew"}\n'
    ;;
  describe)
    where=""
    while [ $# -gt 0 ]; do
      case "$1" in
      --location) where=${2:-} ;;
      esac
      shift
    done
    served "$where" || exit 1
    cat "$dir/scheduler.json"
    ;;
  *)
    printf 'fake gcloud: no route for %s\n' "$*" >&2
    exit 7
    ;;
  esac
  ;;
"run jobs")
  resolve "$dir/execs.json"
  ;;
*)
  printf 'fake gcloud: no route for %s\n' "$*" >&2
  exit 7
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ------------------------------------------------------------------ fixtures
fault() { # fault FILE CODE MESSAGE
  python3 - "$2" "$3" >"$1" <<'PY'
import json, sys
print(json.dumps({"error": {"code": sys.argv[1], "message": sys.argv[2]}}))
PY
}

paddle_fault() { # paddle_fault FILE CODE DETAIL
  python3 - "$2" "$3" >"$1" <<'PY'
import json, sys
print(json.dumps({"error": {"code": sys.argv[1], "detail": sys.argv[2]}}))
PY
}

entitlement_doc() { # entitlement_doc FILE STATUS ENDS_AT
  python3 - "$1" "$2" "$3" "$workspace" >"$1" <<'PY'
import json, sys
print(json.dumps({"entitlements": [{
    "id": "ent-chk564-1", "organisation_id": "org-chk564",
    "workspace_id": sys.argv[4], "source": "purchase", "row": "team-monthly",
    "tier": "team", "kind": "paid", "seats": 1,
    "starts_at": "2026-12-01T00:00:00Z", "ends_at": sys.argv[3],
    "status": sys.argv[2], "air_gapped": False,
    "created_at": "2026-12-01T00:00:00Z",
}]}))
PY
}

# prepare DIR: the happy path's state. The portal answers as itself; the Paddle
# sandbox is reachable and priced; the checkout mints a transaction the sandbox
# holds; the purchase is active at the first look, renewed at the second and
# revoked at the third; and the installation reads a revoked key.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home" "$dir/sandbox"
  write_fake_curl "$dir"
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  printf '%s\n' "$token_marker" >"$dir/state/token"
  chmod 600 "$dir/state/token"
  printf '%s\n' "$paddle_marker" >"$dir/state/paddle.key"
  chmod 600 "$dir/state/paddle.key"

  fault "$dir/state/anonymous.body" unauthenticated "This request carries no sign-in."
  printf '401\n' >"$dir/state/anonymous.code"
  printf '{"workspaces":[]}\n' >"$dir/state/workspaces.body"
  printf '200\n' >"$dir/state/workspaces.code"

  printf '{"project":{"name":"chk564-sandbox","gcp_project":"%s","region":"us-east5"}}\n' "$project" >"$dir/state/doctor.json"

  printf '{"data":[{"id":"pri_sandbox_1","unit_price":{"currency_code":"USD"}}]}\n' >"$dir/state/prices.body"
  printf '200\n' >"$dir/state/prices.code"

  printf '{"transaction_id":"%s"}\n' "$txn" >"$dir/state/checkout.body"
  printf '201\n' >"$dir/state/checkout.code"
  printf '{"data":{"id":"%s","status":"ready","custom_data":{"workspace_id":"%s"}}}\n' "$txn" "$workspace" >"$dir/state/txn.body"
  printf '200\n' >"$dir/state/txn.code"

  printf '{"data":{"id":"%s","status":"active","current_billing_period":{"ends_at":"2027-01-01T00:00:00Z"},"next_billed_at":"2027-01-01T00:00:00Z"}}\n' "$sub" >"$dir/state/sub.body"
  printf '200\n' >"$dir/state/sub.code"

  entitlement_doc "$dir/state/entitlements.body.1" active "2027-01-01T00:00:00Z"
  entitlement_doc "$dir/state/entitlements.body.2" active "2027-02-01T00:00:00Z"
  entitlement_doc "$dir/state/entitlements.body" revoked "2027-02-01T00:00:00Z"
  printf '200\n' >"$dir/state/entitlements.code"

  printf '{"id":"%s","workspace_id":"%s","state":"active","seats":1,"renews_at":"2027-01-01T00:00:00Z"}\n' "$sub" "$workspace" >"$dir/state/mirror.body.1"
  cp "$dir/state/mirror.body.1" "$dir/state/mirror.body.2"
  cp "$dir/state/mirror.body.1" "$dir/state/mirror.body"
  printf '200\n' >"$dir/state/mirror.code"

  printf '{"source":"project","state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":true,"last_check":{"status":"revoked","at":"after"}}\n' >"$dir/state/status.json"

  # The renewal job: Scheduler serves europe-west1 and us-central1, and NOT
  # us-east5, which is the installation's own Cloud Run region in this fixture —
  # the state K4.2's module works around by placing the job in the nearest served
  # location. The job's own document names the schedule and the state.
  printf '[{"locationId":"europe-west1"},{"locationId":"us-central1"}]\n' >"$dir/state/locations.json"
  printf 'europe-west1 us-central1\n' >"$dir/state/scheduler.served"
  printf '{"name":"projects/%s/locations/europe-west1/jobs/fugaro-licence-renew","state":"ENABLED","schedule":"23 4 * * *","timeZone":"UTC"}\n' "$project" >"$dir/state/scheduler.json"

  # The licence Cloud Run job's executions: an execution that ran LONG BEFORE this
  # run's fire and completed, and the one the fire itself starts. The tick row has
  # to name the fire's own (`after`) and never the older one.
  printf '[{"metadata":{"name":"fugarolicence-old99","creationTimestamp":"before"},"status":{"conditions":[{"type":"Completed","status":"True"}]}},\n {"metadata":{"name":"fugarolicence-abc12","creationTimestamp":"after"},"status":{"conditions":[{"type":"Completed","status":"True"}]}}]\n' >"$dir/state/execs.json"
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
  mkdir -p "$dir/tmp" "$dir/home" "$dir/state/logs"
  : >"$dir/state/logs/curl.calls"
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/gcloud.calls"
  rm -f "$dir/state/logs/bearers" "$dir/state/logs/config.mode" "$dir"/state/counter.*
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the only
  # way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE564_DIR="$dir/state" \
    CHECK564_WAIT=1 CHECK564_POLL_SECONDS=0.1 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" \
    --token-file "$dir/state/token" \
    --paddle-key-file "$dir/state/paddle.key" \
    --workspace-id "$workspace" \
    --api "$api" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check564\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "${CHECK564_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n" runs
# every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
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

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check564.sh --sandbox" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "no Paddle API key, no ID token, no licence key" "--help: what is never printed"
  expect_contains "$out" "LIVE Paddle host is refused by name" "--help: the live-host guard"
  expect_contains "$out" "S8.5" "--help: the task the sandbox account comes from"
  no_calls "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$err" "GET $api/api/workspaces" "a dry run: the origin read is planned"
  expect_contains "$err" "POST $api/api/checkout" "a dry run: the purchase is planned"
  expect_contains "$err" "GET $sandbox_api/prices?status=active" "a dry run: the sandbox read is planned"
  expect_contains "$err" "D2147" "a dry run: the refund row's rule is planned"
  no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row xtrace "not tracing" "the happy path: the xtrace guard"
  expect_row agent-session "no coding agent's marker" "the happy path: the session guard"
  expect_row belong "nothing this check was given names belong" "the happy path: the belong guard"
  expect_row paddle-sandbox "the sandbox account subscriptions S8.5 creates is reachable and priced" "the happy path: the sandbox account"
  expect_row paddle-sandbox "count" "the happy path: the sandbox row reads a count"
  expect_row api "401 and code 'unauthenticated'" "the happy path: the origin is the portal's API"
  expect_row token "one line at mode 0600" "the happy path: the token file"
  expect_row sandbox "chk564-sandbox in $project" "the happy path: the installation"
  expect_row scheduler "Scheduler location europe-west1" "the happy path: the renewal job's own location"
  expect_row scheduler "not offered in every Cloud Run region" "the happy path: why the location is read"
  expect_row checkout "the sandbox merchant minted the transaction" "the happy path: the sandbox minted it"
  expect_row checkout "$txn" "the happy path: the transaction is named"
  expect_row entitlement "source 'purchase', status 'active'" "the happy path: the purchase is active"
  expect_row entitlement "names Paddle's subscription $sub" "the happy path: the subscription is named"
  expect_row bind "bound to the purchase" "the happy path: the purchase is bound to the installation"
  expect_row bind "source 'project'" "the happy path: the key came from the sign-in"
  expect_row bind "never read by this check" "the happy path: the key was never read"
  expect_row renewal "the entitlement's term moved" "the happy path: the term moved"
  expect_row refund "reads 'revoked' after the refund" "the happy path: the whole licence is withdrawn"
  expect_row refund "D2147" "the happy path: D2147 is cited"
  expect_row tick "$exec_fresh" "the happy path: the tick names the fire's own execution"
  expect_row tick "COMPLETED" "the happy path: the tick ran to completion"
  expect_row install "revoked=true" "the happy path: the installation reads revoked"
  expect_row install "D436" "the happy path: nothing was deleted"
  expect_row restore "$txn" "the happy path: the restore names the transaction"
  expect_row restore "its refund" "the happy path: the restore names the refund"
  expect_contains "$out" "MANUAL: " "the happy path: a MANUAL line is printed"
  expect_contains "$out" "sandbox overlay" "the happy path: the pay MANUAL names the overlay"
  expect_contains "$out" "refund the transaction $txn" "the happy path: the refund MANUAL names the transaction"
  expect_contains "$out" "sign in as the account that bought this licence" "the happy path: the bind MANUAL names the account"
  expect_contains "$out" "https://licence.chk564.example/cli/authorize" "the happy path: the bind MANUAL names the address the login printed"
  expect_contains "$out" "CHECK 564 RESULT END (exit 0)" "the happy path: the block's end"
  # The calls, in the order the rows are: the anonymous read, the sandbox read,
  # the checkout and its read-back, then the entitlement, the mirror, Paddle's
  # subscription, the renewal job and the two later looks.
  local calls
  calls=$(cat "$dir/state/logs/curl.calls")
  expect_contains "$calls" "curl GET /api/workspaces (config no)" "the happy path: the origin read carried no sign-in"
  expect_contains "$calls" "curl POST /api/checkout (config yes)" "the happy path: the checkout carried the sign-in"
  expect_contains "$calls" "curl GET /prices?status=active (config yes)" "the happy path: the sandbox read carried a config"
  expect_contains "$(cat "$dir/state/logs/config.mode")" "600" "the happy path: the secrets travelled in 0600 configs"
  local gcloudcalls
  gcloudcalls=$(cat "$dir/state/logs/gcloud.calls")
  expect_contains "$gcloudcalls" "scheduler jobs run fugaro-licence-renew" "the happy path: the renewal job ran"
  expect_contains "$gcloudcalls" "run jobs executions list --job fugarolicence" "the happy path: the Cloud Run job's executions were read"
  # Both fires, each aimed at the job's OWN location and neither at the
  # installation's Cloud Run region, which Scheduler does not serve.
  local fires
  fires=$(grep 'scheduler jobs run' "$dir/state/logs/gcloud.calls")
  expect_absent "$fires" "us-east5" "the happy path: no fire is aimed at the Cloud Run region"
  expect_contains "$fires" "--location europe-west1" "the happy path: the fires are aimed at the job's own location"
  expect_contains "$(grep -c 'scheduler jobs run' "$dir/state/logs/gcloud.calls")" "2" "the happy path: the job is fired twice"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "the happy path: the installation signed in once"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "licence status --json" "the happy path: the installation was read"
  assert_scratch_removed "the happy path"
}

test_the_paddle_key_never_reaches_the_output_or_the_scratch() {
  local dir=$work/canary
  prepare "$dir"
  run_check "$dir" CHECK564_KEEP_SCRATCH=1
  expect_status "$status" 0 "the Paddle key canary"
  expect_absent "$out$err" "$paddle_marker" "the Paddle key canary: the key is not printed"
  expect_absent "$out$err" "$token_marker" "the Paddle key canary: the token is not printed"
  expect_contains "$(cat "$dir/state/logs/bearers")" "$paddle_marker" "the Paddle key canary: the fake saw the key in a config"
  expect_contains "$(cat "$dir/state/logs/bearers")" "$token_marker" "the Paddle key canary: the fake saw the token in a config"
  # The scratch directory is where the curl configs are written, so what a kept
  # one holds is checked here: each secret is in a 0600 curl config (the transport
  # the check documents) and in nothing else the run left behind.
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    local f found="" hits
    hits=$(grep -rl -e "$paddle_marker" -e "$token_marker" "$scratch" 2>/dev/null || true)
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      case "$f" in
      *.curlrc) ;;
      *) found="$found $f" ;;
      esac
    done <<<"$hits"
    if [ -n "$found" ]; then fail "the Paddle key canary: a secret is in the scratch outside a curl config:$found"; fi
  fi
  expect_row scratch "was kept at" "the Paddle key canary: the kept directory is named"
  rm -rf "$scratch" 2>/dev/null || true
}

test_a_missing_paddle_key_file_is_refused() {
  local dir=$work/nokey
  prepare "$dir"
  run_check "$dir" -- --paddle-key-file "$dir/state/nope.key"
  expect_status "$status" 2 "a missing Paddle key file"
  expect_row paddle-sandbox "S8.5" "a missing Paddle key file: the refusal names the task"
  expect_row paddle-sandbox "Check 564 buys through the Paddle SANDBOX" "a missing Paddle key file: the exact sentence"
  no_calls "a missing Paddle key file"
}

test_a_paddle_key_file_that_is_not_0600_is_refused() {
  local dir=$work/keymode
  prepare "$dir"
  chmod 644 "$dir/state/paddle.key"
  run_check "$dir"
  expect_status "$status" 2 "a world-readable Paddle key file"
  expect_row paddle-sandbox "mode 644" "a world-readable Paddle key file: the mode is named"
  expect_row paddle-sandbox "S8.5" "a world-readable Paddle key file: the task is named"
  no_calls "a world-readable Paddle key file"
}

test_an_empty_paddle_key_file_is_refused() {
  local dir=$work/keyempty
  prepare "$dir"
  : >"$dir/state/paddle.key"
  run_check "$dir"
  expect_status "$status" 2 "an empty Paddle key file"
  expect_row paddle-sandbox "S8.5" "an empty Paddle key file: the task is named"
  no_calls "an empty Paddle key file"
}

test_a_paddle_host_that_is_not_the_sandbox_is_refused_by_name() {
  local dir=$work/livehost
  prepare "$dir"
  run_check "$dir" CHECK564_PADDLE_API=https://api.paddle.com
  expect_status "$status" 2 "a live Paddle host"
  expect_row paddle-sandbox "refused BY NAME" "a live Paddle host: the refusal says so"
  expect_row paddle-sandbox "https://api.paddle.com" "a live Paddle host: the host is named"
  no_calls "a live Paddle host"
}

test_the_sandbox_refusing_the_key_is_not_measured() {
  local dir=$work/keyrefused
  prepare "$dir"
  paddle_fault "$dir/state/prices.body" not_authenticated "Authentication failed"
  printf '401\n' >"$dir/state/prices.code"
  run_check "$dir"
  expect_status "$status" 2 "a sandbox that refuses the key"
  expect_row paddle-sandbox FAIL "a sandbox that refuses the key: the row is not a pass"
  expect_row paddle-sandbox "S8.5" "a sandbox that refuses the key: the task is named"
  expect_row paddle-sandbox "401" "a sandbox that refuses the key: the refusal is quoted"
}

test_an_api_origin_that_is_not_the_portal_is_not_measured() {
  local dir=$work/notorigin
  prepare "$dir"
  printf '200\n' >"$dir/state/anonymous.code"
  printf '{"ok":true}\n' >"$dir/state/anonymous.body"
  run_check "$dir"
  expect_status "$status" 2 "an origin that is not the portal's API"
  expect_row api FAIL "an origin that is not the portal's API: the row is not a pass"
  expect_row api "rather than 401" "an origin that is not the portal's API: the refusal says why"
}

test_a_token_file_that_is_not_there_is_not_measured() {
  local dir=$work/notoken
  prepare "$dir"
  rm -f "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a token file that is not there"
  expect_row token FAIL "a token file that is not there: the row is not a pass"
  no_calls "a token file that is not there"
}

test_a_live_merchant_is_a_finding() {
  local dir=$work/livem
  prepare "$dir"
  # The sandbox does not hold the transaction the checkout minted: it was minted
  # by a merchant other than the sandbox.
  paddle_fault "$dir/state/txn.body" not_found "Transaction not found"
  printf '404\n' >"$dir/state/txn.code"
  run_check "$dir"
  expect_status "$status" 1 "a live merchant"
  expect_row checkout FAIL "a live merchant: the row is a finding"
  expect_row checkout "LIVE merchant" "a live merchant: the row says which merchant"
}

test_a_purchase_whose_workspace_names_no_subscription_is_a_finding() {
  local dir=$work/nosub
  prepare "$dir"
  fault "$dir/state/mirror.body" no_subscription "That workspace has no subscription."
  printf '404\n' >"$dir/state/mirror.code"
  run_check "$dir"
  expect_status "$status" 1 "a purchase naming no subscription"
  expect_row entitlement FAIL "a purchase naming no subscription: the row is a finding"
  expect_row entitlement "names no subscription" "a purchase naming no subscription: the row says so"
}

test_a_refund_that_left_a_key_serving_is_a_finding() {
  local dir=$work/serving
  prepare "$dir"
  # A check recorded AFTER the refund, and it read ok: the installation has
  # heard about the withdrawal and its key still serves, which is the FAIL.
  printf '{"source":"project","state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":false,"last_check":{"status":"ok","at":"after"}}\n' >"$dir/state/status.json"
  run_check "$dir"
  expect_status "$status" 1 "a refund that left a key serving"
  expect_row install FAIL "a refund that left a key serving: the row is a finding"
  expect_row install "STILL SERVES" "a refund that left a key serving: the row says so"
  expect_row install "D2147" "a refund that left a key serving: the rule is cited"
}

test_a_renewal_that_did_not_move_the_term_is_not_measured() {
  local dir=$work/norenew
  prepare "$dir"
  # Every look answers the same term: the renewal never moved it.
  entitlement_doc "$dir/state/entitlements.body.2" active "2027-01-01T00:00:00Z"
  entitlement_doc "$dir/state/entitlements.body" active "2027-01-01T00:00:00Z"
  run_check "$dir"
  expect_status "$status" 2 "a renewal that did not move the term"
  expect_row renewal FAIL "a renewal that did not move the term: the row is not a pass"
  expect_row renewal "the entitlement's term did not move" "a renewal that did not move the term: the row says so"
}

# The Scheduler location is a fact about the installation and NOT its Cloud Run
# region: Scheduler is not offered in us-east5, and K4.2's module puts the job in
# the nearest location it can. A fire aimed at the Cloud Run region would name a
# location the job is not in.
test_the_scheduler_location_is_read_not_assumed() {
  local dir=$work/schedloc
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the Scheduler location"
  expect_row scheduler "Scheduler location europe-west1" "the Scheduler location: the job's own location, not the region"
  expect_row scheduler "Cloud Run region us-east5" "the Scheduler location: the installation's own region is named as the one it is not"
  local calls fires
  calls=$(cat "$dir/state/logs/gcloud.calls")
  fires=$(grep 'scheduler jobs run' "$dir/state/logs/gcloud.calls")
  expect_contains "$calls" "scheduler jobs describe fugaro-licence-renew --location us-east5" "the Scheduler location: the Cloud Run region is tried first and is not served"
  expect_contains "$calls" "scheduler jobs describe fugaro-licence-renew --location europe-west1" "the Scheduler location: the job was found where it is"
  expect_absent "$fires" "us-east5" "the Scheduler location: no fire at the Cloud Run region"
  expect_contains "$fires" "--project $project --location europe-west1" "the Scheduler location: every fire is aimed at the job's own location"
}

# The job in no location this project serves (or no location list at all): the
# renewal this check fires does not exist here, and that is NO finding about the
# release — and it is said BEFORE anything is bought, so the run leaves no
# transaction behind.
test_a_renewal_job_in_no_served_location_is_not_measured() {
  local dir=$work/nosched
  prepare "$dir"
  printf '[]\n' >"$dir/state/locations.json"
  : >"$dir/state/scheduler.served"
  run_check "$dir"
  expect_status "$status" 2 "a job in no served location"
  expect_row scheduler "in none of this project's Scheduler locations" "a job in no served location: the row"
  expect_row scheduler "TN5" "a job in no served location: renewal by hand is named"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "/api/checkout" "a job in no served location: nothing is bought"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "/prices" "a job in no served location: the sandbox is not read"
  expect_absent "$(cat "$dir/state/logs/gcloud.calls")" "scheduler jobs run" "a job in no served location: nothing is fired"
  assert_scratch_removed "a job in no served location"
}

# A fire that exits non-zero triggers nothing: the term's move is only ever read
# after that fire, so there is no measured row below it. Not a finding.
test_a_renewal_fire_that_fails_is_not_measured() {
  local dir=$work/firefail
  prepare "$dir"
  printf '1\n' >"$dir/state/renew.status"
  run_check "$dir"
  expect_status "$status" 2 "a fire that failed"
  expect_row renewal "exited 1" "a fire that failed: the exit is named"
  expect_row renewal "was NOT triggered" "a fire that failed: nothing was measured"
  assert_scratch_removed "a fire that failed"
}

# The installation hears about a withdrawal only from its OWN check (K4.1): an
# installation whose last check was recorded before the refund has not heard about
# it, so the install row could NOT be measured — never a FAIL, however the local
# store reads. The tick row above it is what shows the fire itself worked.
test_no_check_since_the_refund_is_not_measured() {
  local dir=$work/stalecheck
  prepare "$dir"
  printf '{"source":"project","state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":true,"last_check":{"status":"revoked","at":"before"}}\n' >"$dir/state/status.json"
  run_check "$dir"
  expect_status "$status" 2 "no check since the refund"
  expect_row tick "$exec_fresh" "no check since the refund: the tick still names the fire's own execution"
  expect_row install "not after the instant this run saw the refund land at the portal" "no check since the refund: the row says why"
  expect_row install "no check has been recorded since the refund" "no check since the refund: the row says what is missing"
  expect_absent "$out" "FACT install: PASS" "no check since the refund: the install row is not a pass"
  assert_scratch_removed "no check since the refund"
}

# The per-machine key answers only where the project holds none: an installation
# whose `licence status --json` reads source 'machine' after the sign-in holds a key
# that is not this purchase's, so every row below would be about somebody else's
# licence. A FAIL, and nothing is renewed.
test_a_machine_key_after_the_sign_in_is_a_finding() {
  local dir=$work/machinekey
  prepare "$dir"
  printf '{"source":"machine","state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":true,"last_check":{"status":"revoked","at":"after"}}\n' >"$dir/state/status.json"
  run_check "$dir"
  expect_status "$status" 1 "a machine key after the sign-in"
  expect_row bind "reads source 'machine' rather than 'project'" "a machine key after the sign-in: the row says what it read"
  expect_row bind "somebody else's key" "a machine key after the sign-in: the finding is named"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "/subscriptions/" "a machine key after the sign-in: nothing is renewed"
  assert_scratch_removed "a machine key after the sign-in"
}

# The account signed in for a licence whose tier is not the purchase's: the rows
# below would not be about this purchase, and that is a state of the sandbox rather
# than a finding about the release.
test_a_sign_in_for_another_licence_is_not_measured() {
  local dir=$work/othertier
  prepare "$dir"
  printf '{"source":"project","state":"licensed","tier":"business","kind":"paid","seats":1,"revoked":true,"last_check":{"status":"revoked","at":"after"}}\n' >"$dir/state/status.json"
  run_check "$dir"
  expect_status "$status" 2 "a sign-in for another licence"
  expect_row bind "the purchase is tier 'team'" "a sign-in for another licence: the purchase's own terms are named"
  expect_row bind "tier 'business'" "a sign-in for another licence: the licence actually held is named"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "/subscriptions/" "a sign-in for another licence: nothing is renewed"
  assert_scratch_removed "a sign-in for another licence"
}

# The tick fires the job and no execution of it was CREATED by that fire: the
# newest execution the list holds ran long before it (last night's tick), and its
# own terminal line is not this fire's answer. A row this check could NOT measure,
# and never a PASS read off yesterday's run.
test_a_tick_that_started_no_execution_is_not_measured() {
  local dir=$work/notick
  prepare "$dir"
  printf '[{"metadata":{"name":"fugarolicence-old99","creationTimestamp":"before"},"status":{"conditions":[{"type":"Completed","status":"True"}]}}]\n' >"$dir/state/execs.json"
  run_check "$dir"
  expect_status "$status" 2 "a tick that started no execution"
  expect_row tick "no execution of the Cloud Run job fugarolicence created after this run fired" "a tick that started no execution: the row"
  expect_row tick "$exec_older" "a tick that started no execution: the older run is named as what it saw"
  expect_row tick "created " "a tick that started no execution: the older run's own instant is named"
  expect_absent "$out" "FACT install: PASS" "a tick that started no execution: the install row is not a pass"
  assert_scratch_removed "a tick that started no execution"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --api "https://portal.fugaro-prod.test"
  expect_status "$status" 2 "fugaro-prod without --production"
  expect_contains "$err" "fugaro-prod" "fugaro-prod without --production: the refusal names it"
  no_calls "fugaro-prod without --production"
}

test_a_fugaro_prod_installation_is_refused_without_production() {
  local dir=$work/prodinstall
  prepare "$dir"
  # The project comes from the installation's own doctor, not from a flag: this is
  # the one the check would fire the renewal job of.
  printf '{"project":{"name":"chk564-prod","gcp_project":"fugaro-prod","region":"us-east5"}}\n' >"$dir/state/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "an installation in fugaro-prod without --production"
  expect_row sandbox "fugaro-prod" "an installation in fugaro-prod: the row names the project doctor read"
  expect_row sandbox "refused unless --production" "an installation in fugaro-prod: the refusal"
  expect_contains "$err" "fugaro-prod" "an installation in fugaro-prod: the refusal names it"
  # The purchase and the renewal are not made: the fence is read before either.
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "/prices" "an installation in fugaro-prod: the sandbox is not read"
  expect_absent "$(cat "$dir/state/logs/gcloud.calls")" "scheduler jobs run" "an installation in fugaro-prod: nothing is fired"
  assert_scratch_removed "an installation in fugaro-prod"
}

test_a_fugaro_prod_installation_named_production_is_measured() {
  local dir=$work/prodinstall-ok
  prepare "$dir"
  printf '{"project":{"name":"chk564-prod","gcp_project":"fugaro-prod","region":"us-east5"}}\n' >"$dir/state/doctor.json"
  run_check "$dir" -- --production
  expect_status "$status" 0 "an installation in fugaro-prod named --production"
  expect_row sandbox "fugaro-prod" "an installation in fugaro-prod named --production: the row names it"
  expect_contains "$out" "production" "an installation in fugaro-prod named --production: the block says so"
  assert_scratch_removed "an installation in fugaro-prod named --production"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --workspace-id belong-ws
  expect_status "$status" 2 "belong"
  expect_contains "$err" "names belong" "belong: the refusal names it"
  no_calls "belong"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 2 "a coding agent's session"
  expect_row agent-session FAIL "a coding agent's session: the row is not a pass"
  expect_row agent-session "coding agent's session" "a coding agent's session: the refusal says so"
  no_calls "a coding agent's session"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 2 "a traced shell"
  expect_row xtrace FAIL "a traced shell: the row is not a pass"
  expect_row xtrace "tracing" "a traced shell: the refusal says so"
  no_calls "a traced shell"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  no_calls "an unknown argument"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_paddle_key_never_reaches_the_output_or_the_scratch
t test_a_missing_paddle_key_file_is_refused
t test_a_paddle_key_file_that_is_not_0600_is_refused
t test_an_empty_paddle_key_file_is_refused
t test_a_paddle_host_that_is_not_the_sandbox_is_refused_by_name
t test_the_sandbox_refusing_the_key_is_not_measured
t test_an_api_origin_that_is_not_the_portal_is_not_measured
t test_a_token_file_that_is_not_there_is_not_measured
t test_a_live_merchant_is_a_finding
t test_a_purchase_whose_workspace_names_no_subscription_is_a_finding
t test_a_refund_that_left_a_key_serving_is_a_finding
t test_a_renewal_that_did_not_move_the_term_is_not_measured
t test_the_scheduler_location_is_read_not_assumed
t test_a_renewal_job_in_no_served_location_is_not_measured
t test_a_renewal_fire_that_fails_is_not_measured
t test_no_check_since_the_refund_is_not_measured
t test_a_machine_key_after_the_sign_in_is_a_finding
t test_a_sign_in_for_another_licence_is_not_measured
t test_a_tick_that_started_no_execution_is_not_measured
t test_fugaro_prod_is_refused_without_production
t test_a_fugaro_prod_installation_is_refused_without_production
t test_a_fugaro_prod_installation_named_production_is_measured
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check564_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check564_test.sh: ok"
