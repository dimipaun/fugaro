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
# only: `doctor --json` and `licence status --json` answer out of $FAKE564_DIR, and
# every call is recorded so a case can assert which reads the check made and that
# none of them was a write.
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
  cat "$dir/status.json"
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
# renewal row's `scheduler jobs run` is recorded and answers out of $FAKE564_DIR.
set -euo pipefail
dir=${FAKE564_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s\n' "$*" >>"$dir/logs/gcloud.calls"
if [ -f "$dir/renew.status" ]; then exit "$(cat "$dir/renew.status")"; fi
printf '{"name":"fugaro-licence-renew"}\n'
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

  printf '{"state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":true,"last_check":{"status":"revoked"}}\n' >"$dir/state/status.json"
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
  expect_row checkout "the sandbox merchant minted the transaction" "the happy path: the sandbox minted it"
  expect_row checkout "$txn" "the happy path: the transaction is named"
  expect_row entitlement "source 'purchase', status 'active'" "the happy path: the purchase is active"
  expect_row entitlement "names Paddle's subscription $sub" "the happy path: the subscription is named"
  expect_row renewal "the entitlement's term moved" "the happy path: the term moved"
  expect_row refund "reads 'revoked' after the refund" "the happy path: the whole licence is withdrawn"
  expect_row refund "D2147" "the happy path: D2147 is cited"
  expect_row install "revoked=true" "the happy path: the installation reads revoked"
  expect_row install "D436" "the happy path: nothing was deleted"
  expect_row restore "$txn" "the happy path: the restore names the transaction"
  expect_row restore "its refund" "the happy path: the restore names the refund"
  expect_contains "$out" "MANUAL: " "the happy path: a MANUAL line is printed"
  expect_contains "$out" "sandbox overlay" "the happy path: the pay MANUAL names the overlay"
  expect_contains "$out" "refund the transaction $txn" "the happy path: the refund MANUAL names the transaction"
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
  expect_contains "$(cat "$dir/state/logs/gcloud.calls")" "scheduler jobs run fugaro-licence-renew" "the happy path: the renewal job ran"
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
  printf '{"state":"licensed","tier":"team","kind":"paid","seats":1,"revoked":false,"last_check":{"status":"ok"}}\n' >"$dir/state/status.json"
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

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --api "https://portal.fugaro-prod.test"
  expect_status "$status" 2 "fugaro-prod without --production"
  expect_contains "$err" "fugaro-prod" "fugaro-prod without --production: the refusal names it"
  no_calls "fugaro-prod without --production"
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
t test_fugaro_prod_is_refused_without_production
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check564_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check564_test.sh: ok"
