#!/usr/bin/env bash
# Tests for scripts/live-checks/check573.sh, with a fake `curl` on PATH: NOTHING
# here reaches a cloud, a project or the portal, no agent runs a live command, and
# the fake records every call it is given — the method, the route, whether a
# config was carried and with it the signature the check computed — so a case can
# assert what the check sent and what it did NOT send.
#
# The fake `curl` is a small model of the two halves this check meets: the
# webhook route and the operations queue. Its intake VERIFIES the delivery the way
# the service does — HMAC-SHA256 of `ts:<body>` under the case's own secret, and
# the five-minute window — stores what verified under the event's own id with the
# instant it was received, parks it (this check's transaction is one no checkout
# created, which is F15), and answers 200 with no body either way; its queue
# answers the records that are parked, which is what the route filters. A case
# bends that model with one line per delivery in `$dir/plan` — `drop`,
# `state:<s>`, `freeze`, `dup`, `code:<n>`, `body:<text>`, `store-stale` — and
# with `queue.code`/`queue.body` or `queue.garbage` for the queue's own answer.
#
# Usage: bash scripts/live-checks/check573_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check573_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check573_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check573.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK573_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK573_TEST_HEAD:-1600}")" ;;
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
# no_webhook: the fake's intake was given nothing at all, which is how a case says
# the check stopped before it delivered anything.
no_webhook() { # what
  local log="$dir/state/logs/webhook.calls"
  if [ -s "$log" ]; then
    fail "$1: a delivery was made: $(cat "$log")"
  fi
}

project=chk573-staging-proj
api=https://portal.chk573.test
# The two values the check carries: neither is a credential — the secret is a
# marker word the fake's own HMAC is computed under, and the token opens nothing —
# and the cases assert that neither reaches the check's output, and that the
# secret reaches no file the check leaves behind.
secret_value=WEBHOOK-SECRET-CHK573-CANARY
token_value=IDTOKEN-CHK573-CANARY

# --------------------------------------------------------------------- fakes
write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check573_test.sh only. See the
# test file's header for what it models; every reading it takes is a file under
# $FAKE573_DIR, and nothing here is a real endpoint.
set -euo pipefail
dir=${FAKE573_DIR:?}
mkdir -p "$dir/logs" "$dir/ledger"
conf=""
out=""
url=""
method=GET
explicit=no
data=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config)
    conf=${2:-}
    shift 2
    ;;
  -o)
    out=${2:-}
    shift 2
    ;;
  -X)
    method=${2:-GET}
    explicit=yes
    shift 2
    ;;
  --data-binary)
    data=${2:-}
    shift 2
    ;;
  -w | --max-time | -H)
    shift 2
    ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
# curl's own rule: a body makes it a POST unless -X says otherwise.
if [ -n "$data" ] && [ "$explicit" = no ]; then method=POST; fi
path=${url#*://}
path=/${path#*/}
printf 'curl %s %s (config %s) (in %s)\n' "$method" "$path" "${conf:+yes}" "$PWD" >>"$dir/logs/curl.calls"

simulate_intake() { # simulate_intake — one delivery, as the service decides it
  local n plan header body_file verified store code=200 answer="" drop=no freeze=no dup=no force_state=""
  n=$(cat "$dir/logs/delivery.count" 2>/dev/null || printf '0')
  n=$((n + 1))
  printf '%s\n' "$n" >"$dir/logs/delivery.count"
  plan=$(sed -n "${n}p" "$dir/plan" 2>/dev/null || true)
  local tok
  for tok in $plan; do
    case "$tok" in
    drop) drop=yes ;;
    freeze) freeze=yes ;;
    dup) dup=yes ;;
    store-stale) : ;;
    state:*) force_state=${tok#state:} ;;
    code:*) code=${tok#code:} ;;
    body:*) answer=${tok#body:} ;;
    esac
  done
  header=$(sed -n 's/^header = "Paddle-Signature: \(.*\)"$/\1/p' "$conf" 2>/dev/null | head -n 1)
  printf '%s\n' "$header" >"$dir/logs/sig.$n"
  body_file=${data#@}
  [ -n "$body_file" ] && cp "$body_file" "$dir/logs/body.$n" 2>/dev/null || true
  verified=no
  if [ -n "$header" ] && [ -n "$body_file" ]; then
    verified=$(python3 - "$dir/fake-secret" "$header" "$body_file" <<'PY'
import hashlib, hmac, sys, time

secret = open(sys.argv[1], "rb").read().strip()
header, body = sys.argv[2], open(sys.argv[3], "rb").read()
stamp = signature = ""
for part in header.split(";"):
    name, _, value = part.strip().partition("=")
    if name == "ts" and not stamp:
        stamp = value
    if name == "h1" and not signature:
        signature = value
if not stamp or not signature:
    print("no")
    raise SystemExit(0)
try:
    at = int(stamp)
except Exception:
    print("no")
    raise SystemExit(0)
mac = hmac.new(secret, ("%d:" % at).encode() + body, hashlib.sha256).hexdigest()
if mac != signature:
    print("no")
    raise SystemExit(0)
print("stale" if abs(time.time() - at) > 300 else "ok")
PY
)
  fi
  store=no
  case "$verified" in
  ok) store=yes ;;
  stale) case " $plan " in *" store-stale "*) store=yes ;; esac ;;
  esac
  if [ "$drop" = yes ]; then store=no; fi
  if [ "$code" != 200 ]; then store=no; fi
  if [ "$store" = yes ]; then
    local id now state
    id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("event_id",""))' "$body_file")
    state=${force_state:-parked}
    now=$(date -u +%s)
    if [ -f "$dir/ledger/$id.received" ] && [ "$freeze" = yes ]; then
      now=$(cat "$dir/ledger/$id.received")
    fi
    printf '%s\n' "$state" >"$dir/ledger/$id.state"
    printf '%s\n' "$now" >"$dir/ledger/$id.received"
    if [ "$dup" = yes ]; then printf 'dup\n' >"$dir/ledger/$id.dup"; fi
  fi
  printf 'delivery %s: verified %s, stored %s, plan [%s]\n' "$n" "$verified" "$store" "$plan" >>"$dir/logs/webhook.calls"
  if [ -n "$out" ] && [ -n "$answer" ]; then printf '%s' "$answer" >"$out"; fi
  printf '%s' "$code"
}

case "$method $path" in
"POST /api/paddle/webhook")
  simulate_intake
  ;;
"GET /api/ops/events?state=parked")
  if [ -f "$dir/queue.code" ]; then
    code=$(cat "$dir/queue.code")
    [ -n "$out" ] && cp "$dir/queue.body" "$out"
  elif [ -f "$dir/queue.garbage" ]; then
    code=200
    [ -n "$out" ] && printf 'not-a-queue\n' >"$out"
  else
    code=200
    if [ -n "$out" ]; then
      python3 - "$dir/ledger" >"$out" <<'PY'
import json, os, sys, datetime

records = []
for name in sorted(os.listdir(sys.argv[1])):
    if not name.endswith(".state"):
        continue
    id = name[: -len(".state")]
    state = open(os.path.join(sys.argv[1], name)).read().strip()
    if state != "parked":
        continue
    received = open(os.path.join(sys.argv[1], id + ".received")).read().strip()
    at = datetime.datetime.fromtimestamp(int(received), datetime.timezone.utc)
    records.append({"id": id, "state": state,
                    "received_at": at.strftime("%Y-%m-%dT%H:%M:%SZ"),
                    "notification": "transaction.completed", "signature": "kept",
                    "body": "kept", "entitlement_id": None})
print(json.dumps(records))
PY
    fi
  fi
  printf '%s' "$code"
  ;;
"GET /api/workspaces")
  if [ -f "$dir/anonymous.code" ]; then
    code=$(cat "$dir/anonymous.code")
    [ -n "$out" ] && cp "$dir/anonymous.body" "$out"
  else
    code=200
    [ -n "$out" ] && printf '{}\n' >"$out"
  fi
  printf '%s' "$code"
  ;;
*)
  printf 'fake curl: no route for %s %s\n' "$method" "$path" >&2
  exit 7
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------ fixtures
fault() { # fault FILE CODE MESSAGE
  python3 - "$2" "$3" >"$1" <<'PY'
import json, sys
print(json.dumps({"error": {"code": sys.argv[1], "message": sys.argv[2]}}))
PY
}

# prepare DIR: the happy path's state — an origin that answers as the portal's
# API, a secret and a token of the check's own, an empty queue, and an intake that
# verifies what the check signs.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/state/ledger" "$dir/tmp" "$dir/home"
  write_fake_curl "$dir"
  printf '%s\n' "$secret_value" >"$dir/state/secret"
  # The fake verifies under its OWN copy of the deployment's secret, so a case can
  # hand the check a different one and watch the intake refuse what it signs.
  cp "$dir/state/secret" "$dir/state/fake-secret"
  printf '%s\n' "$token_value" >"$dir/state/token"
  chmod 600 "$dir/state/secret" "$dir/state/token"
  fault "$dir/state/anonymous.body" unauthenticated "This request carries no sign-in."
  printf '401\n' >"$dir/state/anonymous.code"
  : >"$dir/state/plan"
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
  : >"$dir/state/logs/webhook.calls"
  rm -f "$dir/state/logs/delivery.count" "$dir/state/logs/sig."*
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
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
    FAKE573_DIR="$dir/state" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --project "$project" \
    --token-file "$dir/state/token" \
    --webhook-secret-file "$dir/state/secret" \
    --api "$api" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check573\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  # CHECK573_TEST_DUMP=1 prints the block a case just read: a case that fails on
  # a row is much easier to read with the whole block in front of it.
  if [ -n "${CHECK573_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
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
  expect_contains "$out" "bash scripts/live-checks/check573.sh --project" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "two parked events per run" "--help: what it creates"
  expect_contains "$out" "not the webhook secret" "--help: what is never printed"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$err" "POST $api/api/paddle/webhook with body A" "a dry run: the delivery is planned"
  expect_contains "$err" "stamped 1800 seconds ago" "a dry run: the stale delivery is planned"
  no_webhook "a dry run"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row api "401 and code 'unauthenticated'" "the happy path: the origin is the portal's API"
  expect_row queue "none under this run's own ids" "the happy path: the ledger is empty of this check"
  expect_row delivery "state 'parked'" "the happy path: the delivery is parked"
  expect_row delivery "F15" "the happy path: the row says why it parks"
  expect_row redelivery "leave the ledger unchanged" "the happy path: the redelivery row"
  expect_row redelivery "received instant moved" "the happy path: the redelivery was received and applied nothing"
  expect_row window "delivery window" "the happy path: the stale delivery was refused"
  expect_row window "holds NOTHING" "the happy path: nothing was stored for it"
  expect_row replay "the SAME body B" "the happy path: the control delivery"
  expect_row restore "dismiss" "the happy path: the two ids are named for dismissal"
  expect_contains "$out" "evt-chk573-a-" "the happy path: the first event's id is in the block"
  expect_contains "$out" "evt-chk573-b-" "the happy path: the second event's id is in the block"
  expect_contains "$out" "CHECK 573 RESULT END (exit 0)" "the happy path: the block's end"
  # The intake saw four deliveries: A, A again, B stale, B fresh — and it refused
  # exactly the third of them.
  local calls
  calls=$(cat "$dir/state/logs/webhook.calls")
  expect_contains "$calls" "delivery 1: verified ok, stored yes" "the happy path: the first delivery verified and stored"
  expect_contains "$calls" "delivery 2: verified ok, stored yes" "the happy path: the redelivery verified and stored"
  expect_contains "$calls" "delivery 3: verified stale, stored no" "the happy path: the stale delivery was refused and stored by nothing"
  expect_contains "$calls" "delivery 4: verified ok, stored yes" "the happy path: the control delivery verified and stored"
  assert_scratch_removed "the happy path"
}

test_neither_the_secret_nor_the_token_is_printed() {
  local dir=$work/canary
  prepare "$dir"
  run_check "$dir" CHECK573_KEEP_SCRATCH=1
  expect_status "$status" 0 "the canaries"
  expect_absent "$out$err" "$secret_value" "the canaries: the secret is not printed"
  expect_absent "$out$err" "$token_value" "the canaries: the token is not printed"
  expect_absent "$out$err" "h1=" "the canaries: no signature is printed"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    local f found=""
    while IFS= read -r f; do
      [ -n "$f" ] && found="$found $f"
    done <<<"$(grep -rl "$secret_value" "$scratch" 2>/dev/null || true)"
    if [ -n "$found" ]; then fail "the canaries: the secret reaches the scratch:$found"; fi
    # The token travels in the curl configs, which is the transport the check
    # documents; anything else in the scratch carrying it would not be.
    found=""
    while IFS= read -r f; do
      case "$f" in
      "" | *.curlrc) ;;
      *) found="$found $f" ;;
      esac
    done <<<"$(grep -rl "$token_value" "$scratch" 2>/dev/null || true)"
    if [ -n "$found" ]; then fail "the canaries: the token is in the scratch outside a curl config:$found"; fi
  fi
  expect_row scratch "was kept at" "the canaries: the kept directory is named"
  rm -rf "$scratch" 2>/dev/null || true
}

test_a_signature_that_does_not_verify_is_not_stored() {
  # The check signs with the secret it was given: a case whose deployment secret
  # is another one is a delivery the intake refuses, and the row that follows is
  # about a ledger nothing landed in.
  local dir=$work/wrongsecret
  prepare "$dir"
  printf 'a-different-webhook-secret\n' >"$dir/state/secret"
  chmod 600 "$dir/state/secret"
  run_check "$dir"
  expect_status "$status" 2 "a signature that does not verify"
  expect_row delivery FAIL "a signature that does not verify: the row is not a pass"
  expect_row delivery "was signed with the secret" "a signature that does not verify: the row says what it depends on"
  expect_contains "$(cat "$dir/state/logs/webhook.calls")" "verified no" "a signature that does not verify: the intake refused it"
}

test_a_ledger_the_redelivery_moved_is_a_finding() {
  local dir=$work/moved
  prepare "$dir"
  # A redelivery that leaves the event processed: the queue answers parked events
  # alone, so the record the check read a moment ago is gone from it.
  printf '\nstate:processed\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 1 "a ledger the redelivery moved"
  expect_row delivery PASS "a ledger the redelivery moved: the delivery row held"
  expect_row redelivery FAIL "a ledger the redelivery moved: the row is a finding"
  expect_row redelivery "unmade what the delivery before it did" "a ledger the redelivery moved: the row says what moved"
}

test_a_redelivery_that_left_no_trace_is_a_finding() {
  local dir=$work/notrace
  prepare "$dir"
  printf '\nfreeze\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 1 "a redelivery that left no trace"
  expect_row redelivery FAIL "a redelivery that left no trace: the row is a finding"
  expect_row redelivery "no trace of itself" "a redelivery that left no trace: the row says why it cannot be read"
}

test_a_stale_delivery_that_was_stored_is_a_finding() {
  local dir=$work/stalestored
  prepare "$dir"
  printf '\n\nstore-stale\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 1 "a stale delivery that was stored"
  expect_row window FAIL "a stale delivery that was stored: the row is a finding"
  expect_row window "more than five minutes" "a stale delivery that was stored: the row names the window"
}

test_a_control_delivery_that_was_not_stored_is_a_finding() {
  local dir=$work/nocontrol
  prepare "$dir"
  # The stale delivery is refused by the window (as it is), and the SAME body with
  # a stamp of now is not stored either: without it the row above could be a body
  # the route refuses for any reason at all.
  printf '\n\n\ndrop\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 1 "a control delivery that was not stored"
  expect_row window PASS "a control delivery that was not stored: the window row held"
  expect_row replay FAIL "a control delivery that was not stored: the row is a finding"
  expect_row replay "is not one the delivery window refused" "a control delivery that was not stored: the row says what the control proves"
}

test_a_delivery_that_was_not_stored_is_not_measured() {
  local dir=$work/dropped
  prepare "$dir"
  printf 'drop\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 2 "a delivery that was not stored"
  expect_row delivery FAIL "a delivery that was not stored: the row is not a pass"
  expect_row delivery "secret is not this deployment's" "a delivery that was not stored: the row names what it depends on"
  expect_row redelivery FAIL "a delivery that was not stored: the run stopped before the redelivery"
}

test_a_delivery_answered_something_else_is_not_measured() {
  local dir=$work/othecode
  prepare "$dir"
  printf 'code:500 body:{"error":"boom"}\n' >"$dir/state/plan"
  run_check "$dir"
  expect_status "$status" 2 "a delivery answered with a 500"
  expect_row delivery "rather than 200 with no body" "a delivery answered with a 500: the row says so"
}

test_a_queue_that_refuses_the_token_is_not_measured() {
  local dir=$work/queuerefused
  prepare "$dir"
  fault "$dir/state/queue.body" not_operations "These screens are the operations team's, and this sign-in is not one of them."
  printf '403\n' >"$dir/state/queue.code"
  run_check "$dir"
  expect_status "$status" 2 "a queue that refuses the token"
  expect_row queue FAIL "a queue that refuses the token: the row is not a pass"
  expect_row queue "is not one" "a queue that refuses the token: the row names the membership"
  no_webhook "a queue that refuses the token"
}

test_a_queue_that_answers_an_unreadable_document_is_not_measured() {
  local dir=$work/queuegarbage
  prepare "$dir"
  : >"$dir/state/queue.garbage"
  run_check "$dir"
  expect_status "$status" 2 "a queue that answers an unreadable document"
  expect_row queue "answered 200 with a document this check cannot read" "a queue that answers an unreadable document: the row says so"
  no_webhook "a queue that answers an unreadable document"
}

test_an_origin_that_is_not_the_api_is_not_measured() {
  local dir=$work/origin
  prepare "$dir"
  printf '200\n' >"$dir/state/anonymous.code"
  printf '{"marketing":true}\n' >"$dir/state/anonymous.body"
  run_check "$dir"
  expect_status "$status" 2 "an origin that is not the API"
  expect_row api "rather than 401" "an origin that is not the API: the refusal"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "api/ops/events" "an origin that is not the API: no ledger was read"
}

test_a_secret_file_that_is_not_0600_is_not_measured() {
  local dir=$work/secretmode
  prepare "$dir"
  chmod 644 "$dir/state/secret"
  run_check "$dir"
  expect_status "$status" 2 "a secret file at mode 0644"
  expect_row secret FAIL "a secret file at mode 0644: the row is not a pass"
  expect_row secret "not 600" "a secret file at mode 0644: the row names the mode"
  no_webhook "a secret file at mode 0644"
}

test_a_secret_file_that_is_not_there_is_not_measured() {
  local dir=$work/secretmissing
  prepare "$dir"
  rm -f "$dir/state/secret"
  run_check "$dir"
  expect_status "$status" 2 "a secret file that is not there"
  expect_row secret "is not there" "a secret file that is not there: the row says so"
  no_webhook "a secret file that is not there"
}

test_a_token_file_that_is_not_0600_is_not_measured() {
  local dir=$work/tokenmode
  prepare "$dir"
  chmod 644 "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a token file at mode 0644"
  expect_row token "not 600" "a token file at mode 0644: the row names the mode"
  no_webhook "a token file at mode 0644"
}

test_a_stale_seconds_inside_the_window_is_refused() {
  # The window is 300 seconds: a check whose replay is inside it would prove
  # nothing about the window, so the value is refused before anything runs.
  local dir=$work/staleknob
  prepare "$dir"
  run_check "$dir" CHECK573_STALE_SECONDS=60
  expect_status "$status" 2 "a stale window inside the window"
  expect_contains "$err" "the delivery window is 300 seconds" "a stale window inside the window: the refusal"
  no_webhook "a stale window inside the window"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_row project FAIL "fugaro-prod without --production: the row is a refusal"
  expect_contains "$err" "refused unless --production" "fugaro-prod without --production: the reason"
  no_webhook "fugaro-prod without --production"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prodok
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod --production
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project "production, named with --production" "fugaro-prod with --production: the row says so"
  expect_row redelivery "leave the ledger unchanged" "fugaro-prod with --production: the rows ran"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project "belong-staging"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "names belong" "a project naming belong: the refusal"
  no_webhook "a project naming belong"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_row agent-session FAIL "a coding agent's session: the row is a refusal"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal names the marker"
  no_webhook "a coding agent's session"
}

test_xtrace_is_refused() {
  # A traced run prints every command, and this check signs with the deployment's
  # webhook secret: the config file's path is fine, what it holds is not.
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  expect_absent "$out$err" "$secret_value" "a traced shell: no secret is printed"
  no_webhook "a traced shell"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK573_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch: INFO" "a kept scratch directory: the row"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  no_webhook "an unknown argument"
}

t test_help
t test_dry_run
t test_happy_path
t test_neither_the_secret_nor_the_token_is_printed
t test_a_signature_that_does_not_verify_is_not_stored
t test_a_ledger_the_redelivery_moved_is_a_finding
t test_a_redelivery_that_left_no_trace_is_a_finding
t test_a_stale_delivery_that_was_stored_is_a_finding
t test_a_control_delivery_that_was_not_stored_is_a_finding
t test_a_delivery_that_was_not_stored_is_not_measured
t test_a_delivery_answered_something_else_is_not_measured
t test_a_queue_that_refuses_the_token_is_not_measured
t test_a_queue_that_answers_an_unreadable_document_is_not_measured
t test_an_origin_that_is_not_the_api_is_not_measured
t test_a_secret_file_that_is_not_0600_is_not_measured
t test_a_secret_file_that_is_not_there_is_not_measured
t test_a_token_file_that_is_not_0600_is_not_measured
t test_a_stale_seconds_inside_the_window_is_refused
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check573_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check573_test.sh: ok"
