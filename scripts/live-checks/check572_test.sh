#!/usr/bin/env bash
# Tests for scripts/live-checks/check572.sh, with a fake `curl` on PATH:
# NOTHING here reaches a cloud, a project or the portal, no agent runs a live
# command, and the fake records every call it is given — the method, the route,
# whether an authorization config was carried and what that config holds, with
# the directory it was made in — so a case can assert what the check read and
# what it did NOT read.
#
# The fake `curl` serves the portal's own shapes out of the case's files: the
# anonymous 401, the admitted `/api/entitlements`, and the two refusals this
# check is about (`/api/ops/organisations` and `/api/resellers/customers`). The
# ID token a case hands the check is BUILT here — a three-part string whose
# payload the case writes and whose signature segment is a marker word, not a
# credential — so a case can bend the provider, the `aud` and the expiry
# independently, and assert that the check's own output, error stream and
# scratch directory never carry the token's text.
#
# Usage: bash scripts/live-checks/check572_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check572_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check572_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check572.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK572_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK572_TEST_HEAD:-1600}")" ;;
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
# no_calls: the fake `curl` was given nothing at all, which is how a case says a
# guard or a setup row stopped the check before it read the portal.
no_calls() { # what
  local log="$dir/state/logs/curl.calls"
  if [ -s "$log" ]; then
    fail "$1: a call was made: $(cat "$log")"
  fi
}
# no_auth_calls: no call CARRIED THE SIGN-IN, which is how a case says a row about
# the token stopped the run. The check's own anonymous read of the origin is the
# one call that can precede it, and this says so rather than forbidding it.
no_auth_calls() { # what
  if [ -e "$dir/state/logs/auth" ]; then
    fail "$1: a call carried the sign-in: $(cat "$dir/state/logs/auth")"
  fi
}

project=chk572-staging-proj
api=https://portal.chk572.test
# signature_marker is the token's third part: a marker word and not a credential,
# so the file a case writes is a readable ID token's SHAPE and nothing anybody
# could present. The cases assert the check never prints it.
signature_marker=CHK572-NOT-A-SIGNATURE

# --------------------------------------------------------------------- fakes
write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check572_test.sh only: the
# portal's routes are answered out of $FAKE572_DIR, and every request is recorded
# there — the method, the route, whether an authorization config was carried, and
# the token that config holds, so a case can assert BOTH what the check read and
# that its sign-in never travelled on a command line. Nothing here is a real
# endpoint: the bodies are the case's own files.
set -euo pipefail
dir=${FAKE572_DIR:?}
mkdir -p "$dir/logs"
conf=""
out=""
url=""
method=GET
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
    shift 2
    ;;
  -w | --max-time | -H | --data-binary)
    shift 2
    ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
path=${url#*://}
path=/${path#*/}
printf 'curl %s %s (config %s) (in %s)\n' "$method" "$path" "${conf:+yes}" "$PWD" >>"$dir/logs/curl.calls"
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >"$dir/logs/auth" 2>/dev/null || true
  python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$dir/logs/auth.mode" 2>/dev/null || printf 'unknown\n' >"$dir/logs/auth.mode"
fi
case "$method $path" in
"GET /api/ops/organisations")
  code=$(cat "$dir/ops.code")
  [ -n "$out" ] && cp "$dir/ops.body" "$out"
  ;;
"GET /api/resellers/customers")
  code=$(cat "$dir/reseller.code")
  [ -n "$out" ] && cp "$dir/reseller.body" "$out"
  ;;
"GET /api/entitlements")
  code=$(cat "$dir/entitlements.code")
  [ -n "$out" ] && cp "$dir/entitlements.body" "$out"
  ;;
"GET /api/workspaces")
  if [ -n "$conf" ]; then
    code=$(cat "$dir/workspaces.code")
    [ -n "$out" ] && cp "$dir/workspaces.body" "$out"
  else
    code=$(cat "$dir/anonymous.code")
    [ -n "$out" ] && cp "$dir/anonymous.body" "$out"
  fi
  ;;
*)
  printf 'fake curl: no route for %s %s\n' "$method" "$path" >&2
  exit 7
  ;;
esac
printf '%s' "$code"
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------ fixtures
# token_doc FILE PROVIDER AUD EXP_OFFSET: an ID token's SHAPE — three
# dot-separated, base64url parts — with the payload the case names. The check
# decodes the payload locally and verifies no signature (it is not the API), so
# the signature segment is the marker word above and nothing signs anything here.
token_doc() { # token_doc FILE PROVIDER AUD EXP_OFFSET
  python3 - "$1" "$2" "$3" "$4" "$signature_marker" <<'PY'
import base64, json, sys, time

def part(doc):
    raw = json.dumps(doc, separators=(",", ":")).encode()
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")

provider, aud, offset = sys.argv[2], sys.argv[3], int(sys.argv[4])
now = int(time.time())
payload = {
    "iss": "https://securetoken.google.com/%s" % aud,
    "aud": aud,
    "iat": now,
    "auth_time": now,
    "exp": now + offset,
    "sub": "uid-chk572",
    "email": "chk572@example.invalid",
    "firebase": {"sign_in_provider": provider, "identities": {}},
}
token = "%s.%s.%s" % (part({"alg": "none", "typ": "JWT"}), part(payload), sys.argv[5])
open(sys.argv[1], "w").write(token + "\n")
PY
}

fault() { # fault FILE CODE MESSAGE
  python3 - "$2" "$3" >"$1" <<'PY'
import json, sys
print(json.dumps({"error": {"code": sys.argv[1], "message": sys.argv[2]}}))
PY
}

# prepare DIR: the happy path's state — an email-link token minted for the
# project under test, an origin that answers as the portal's API, an admitted
# ordinary read, and the two refusals D2207's rule is.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home"
  write_fake_curl "$dir"
  token_doc "$dir/state/token" password "$project" 3600
  chmod 600 "$dir/state/token"
  fault "$dir/state/anonymous.body" unauthenticated "This request carries no sign-in."
  printf '401\n' >"$dir/state/anonymous.code"
  printf '{"entitlements":[]}\n' >"$dir/state/entitlements.body"
  printf '200\n' >"$dir/state/entitlements.code"
  printf '{"workspaces":[]}\n' >"$dir/state/workspaces.body"
  printf '200\n' >"$dir/state/workspaces.code"
  fault "$dir/state/ops.body" sso_required \
    "The operations organisation's members sign in with Google or Microsoft. Sign in that way to reach these screens."
  printf '403\n' >"$dir/state/ops.code"
  fault "$dir/state/reseller.body" sso_required \
    "A reseller organisation's members sign in with Google or Microsoft. Sign in that way to use it."
  printf '403\n' >"$dir/state/reseller.code"
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
  rm -f "$dir/state/logs/auth" "$dir/state/logs/auth.mode"
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
    FAKE572_DIR="$dir/state" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --project "$project" \
    --token-file "$dir/state/token" \
    --api "$api" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check572\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  # CHECK572_TEST_DUMP=1 prints the block a case just read: a case that fails on
  # a row is much easier to read with the whole block in front of it.
  if [ -n "${CHECK572_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
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
  expect_contains "$out" "bash scripts/live-checks/check572.sh --project" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, who runs it and what it never prints.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "no ID token, in whole or in part" "--help: what is never printed"
  expect_contains "$out" "\`fugaro-prod\` is refused unless \`--production" "--help: the production guard"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$err" "GET $api/api/ops/organisations" "a dry run: the operations read is planned"
  expect_contains "$err" "GET $api/api/resellers/customers" "a dry run: the reseller read is planned"
  expect_contains "$err" "MANUAL line" "a dry run: the browser half is planned"
  no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row api "401 and code 'unauthenticated'" "the happy path: the origin is the portal's API"
  expect_row signin "firebase.sign_in_provider is 'password'" "the happy path: the provider was read"
  expect_row signin "$project" "the happy path: the aud is the project under test"
  expect_row signin "P1.3's UNVERIFIED item" "the happy path: the reading is tied to the item it answers"
  expect_row verified "answers 200" "the happy path: the control row"
  expect_row operations "403 'sso_required'" "the happy path: the operations refusal"
  expect_row operations "sign in with Google or Microsoft" "the happy path: the operations sentence"
  expect_row reseller "403 'sso_required'" "the happy path: the reseller refusal"
  expect_row reseller "A reseller organisation's members sign in with Google or Microsoft" "the happy path: the reseller sentence"
  expect_row manual "MANUAL line" "the happy path: the browser half is printed as a line"
  expect_row restore PASS "the happy path: the restore row"
  expect_contains "$out" "MANUAL: open $api" "the happy path: the MANUAL line itself"
  expect_contains "$out" "390x844" "the happy path: the phone width is named"
  expect_contains "$out" "CHECK 572 RESULT END (exit 0)" "the happy path: the block's end"
  # The check's calls, in the order the rows are: the anonymous read, the
  # control, and then the two refusals.
  expect_contains "$(cat "$dir/state/logs/curl.calls")" "curl GET /api/ops/organisations (config yes)" "the happy path: the operations read carried the sign-in"
  expect_contains "$(cat "$dir/state/logs/curl.calls")" "curl GET /api/resellers/customers (config yes)" "the happy path: the reseller read carried the sign-in"
  expect_contains "$(cat "$dir/state/logs/auth.mode")" "600" "the happy path: the token travelled in a 0600 config"
  assert_scratch_removed "the happy path"
}

test_the_token_never_reaches_the_output_or_the_scratch() {
  local dir=$work/canary
  prepare "$dir"
  run_check "$dir" CHECK572_KEEP_SCRATCH=1
  expect_status "$status" 0 "the token canary"
  expect_absent "$out$err" "$signature_marker" "the token canary: the token is not printed"
  expect_absent "$out$err" "uid-chk572" "the token canary: no claim that names the account is printed"
  expect_contains "$(cat "$dir/state/logs/auth")" "$signature_marker" "the token canary: the fake saw the token in the config"
  # The scratch directory is where the payload was decoded and the curl configs
  # are written, so what a kept one holds is checked here: the token's own text
  # is in a 0600 curl config (the transport the check documents) and in nothing
  # else the run left behind.
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    local f found=""
    while IFS= read -r f; do
      case "$f" in
      *.curlrc) ;;
      *) found="$found $f" ;;
      esac
    done <<<"$(grep -rl "$signature_marker" "$scratch" 2>/dev/null || true)"
    if [ -n "$found" ]; then fail "the token canary: the token is in the scratch outside a curl config:$found"; fi
  fi
  expect_row scratch "was kept at" "the token canary: the kept directory is named"
  rm -rf "$scratch" 2>/dev/null || true
}

test_the_other_spelling_of_an_email_link_sign_in_is_read() {
  local dir=$work/emaillink
  prepare "$dir"
  token_doc "$dir/state/token" emailLink "$project" 3600
  run_check "$dir"
  expect_status "$status" 0 "an emailLink provider"
  expect_row signin "'emailLink'" "an emailLink provider: the row names what it read"
  expect_row signin "the other spelling" "an emailLink provider: the row says which spelling this project uses"
}

test_an_sso_sign_in_is_not_measured() {
  local dir=$work/sso
  prepare "$dir"
  token_doc "$dir/state/token" google.com "$project" 3600
  run_check "$dir"
  expect_status "$status" 2 "a Google sign-in"
  expect_row signin FAIL "a Google sign-in: the row is not a pass"
  expect_row signin "SSO sign-in" "a Google sign-in: the refusal says why"
  no_auth_calls "a Google sign-in"
}

test_a_provider_that_is_neither_is_not_measured() {
  local dir=$work/custom
  prepare "$dir"
  token_doc "$dir/state/token" custom "$project" 3600
  run_check "$dir"
  expect_status "$status" 2 "a custom provider"
  expect_row signin "neither an email link" "a custom provider: the refusal says why"
  no_auth_calls "a custom provider"
}

test_a_token_of_another_project_is_not_measured() {
  local dir=$work/otherproj
  prepare "$dir"
  token_doc "$dir/state/token" password another-chk572-proj 3600
  run_check "$dir"
  expect_status "$status" 2 "a token of another project"
  expect_row signin "aud is 'another-chk572-proj'" "a token of another project: the row names both"
  no_auth_calls "a token of another project"
}

test_an_expired_token_is_not_measured() {
  local dir=$work/expired
  prepare "$dir"
  token_doc "$dir/state/token" password "$project" -60
  run_check "$dir"
  expect_status "$status" 2 "an expired token"
  expect_row signin "the token expired" "an expired token: the refusal says why"
  no_auth_calls "an expired token"
}

test_a_file_that_is_not_a_token_is_not_measured() {
  local dir=$work/nottoken
  prepare "$dir"
  printf 'not-a-token-at-all\n' >"$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a file that is not a token"
  expect_row signin "not an ID token this check can read" "a file that is not a token: the refusal"
  no_auth_calls "a file that is not a token"
}

test_a_token_file_that_is_not_0600_is_not_measured() {
  local dir=$work/mode
  prepare "$dir"
  chmod 644 "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a token file at mode 0644"
  expect_row token "not 600" "a token file at mode 0644: the refusal"
  no_calls "a token file at mode 0644"
}

test_a_token_file_that_is_not_there_is_not_measured() {
  local dir=$work/nothere
  prepare "$dir"
  rm -f "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a token file that is not there"
  expect_row token "is not there" "a token file that is not there: the refusal"
  no_calls "a token file that is not there"
}

test_an_origin_that_is_not_the_api_is_not_measured() {
  local dir=$work/origin
  prepare "$dir"
  printf '200\n' >"$dir/state/anonymous.code"
  printf '{"marketing":true}\n' >"$dir/state/anonymous.body"
  run_check "$dir"
  expect_status "$status" 2 "an origin that is not the API"
  expect_row api "rather than 401" "an origin that is not the API: the refusal"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "api/entitlements" "an origin that is not the API: nothing else was read"
}

test_an_origin_that_answers_another_code_is_not_measured() {
  local dir=$work/othercode
  prepare "$dir"
  fault "$dir/state/anonymous.body" forbidden "Not this one."
  run_check "$dir"
  expect_status "$status" 2 "a 401 with another code"
  expect_row api "rather than 'unauthenticated'" "a 401 with another code: the refusal"
}

test_a_token_the_api_refuses_is_not_measured() {
  local dir=$work/unverified
  prepare "$dir"
  fault "$dir/state/entitlements.body" unauthenticated "This request carries no sign-in."
  printf '401\n' >"$dir/state/entitlements.code"
  run_check "$dir"
  expect_status "$status" 2 "a token the API refuses"
  expect_row verified FAIL "a token the API refuses: the row is not a pass"
  expect_row verified "the sign-in's own" "a token the API refuses: the row says the refusals below cannot be attributed"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "api/ops/" "a token the API refuses: the operations screens were not read"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "api/resellers/" "a token the API refuses: the reseller screens were not read"
}

test_the_operations_screens_admitting_the_sign_in_is_a_finding() {
  local dir=$work/ops200
  prepare "$dir"
  printf '{"organisations":[]}\n' >"$dir/state/ops.body"
  printf '200\n' >"$dir/state/ops.code"
  run_check "$dir"
  expect_status "$status" 1 "the operations screens admitting the sign-in"
  expect_row operations FAIL "the operations screens admitting the sign-in: the row is a finding"
  expect_row operations "answered 200" "the operations screens admitting the sign-in: the row says what it read"
  expect_row reseller FAIL "the operations screens admitting the sign-in: the run stopped before the reseller row"
}

test_an_account_that_is_not_an_operations_member_is_not_measured() {
  local dir=$work/notops
  prepare "$dir"
  fault "$dir/state/ops.body" not_operations "These screens are the operations team's, and this sign-in is not one of them."
  run_check "$dir"
  expect_status "$status" 2 "an account that is not an operations member"
  expect_row operations "not a member of the staging operations organisation" "an account that is not an operations member: the refusal says why"
  expect_row operations "Add the email-link account" "an account that is not an operations member: the row names the fix"
}

test_an_account_that_is_not_a_reseller_member_is_not_measured() {
  local dir=$work/notreseller
  prepare "$dir"
  fault "$dir/state/reseller.body" not_reseller "These screens are a reseller organisation's, and this sign-in is not one of its people."
  run_check "$dir"
  expect_status "$status" 2 "an account that is not a reseller member"
  expect_row operations PASS "an account that is not a reseller member: the operations row held"
  expect_row reseller "not a member of the staging reseller organisation" "an account that is not a reseller member: the refusal says why"
}

test_a_refusal_with_another_code_is_not_measured() {
  local dir=$work/otherrefusal
  prepare "$dir"
  fault "$dir/state/ops.body" not_permitted "Only the operations organisation's owners and admins may change anything here."
  run_check "$dir"
  expect_status "$status" 2 "a refusal with another code"
  expect_row operations "with code 'not_permitted'" "a refusal with another code: the row names what it read"
}

test_a_refusal_with_another_sentence_is_not_measured() {
  local dir=$work/othersentence
  prepare "$dir"
  fault "$dir/state/ops.body" sso_required "Sign in another way."
  run_check "$dir"
  expect_status "$status" 2 "a refusal with another sentence"
  expect_row operations "does not carry" "a refusal with another sentence: the row says the sentence is the SPA's"
}

test_a_read_that_answers_nothing_is_not_measured() {
  local dir=$work/noanswer
  prepare "$dir"
  rm -f "$dir/state/ops.code" "$dir/state/ops.body"
  run_check "$dir"
  expect_status "$status" 2 "a read that answers nothing"
  expect_row operations "answered nothing" "a read that answers nothing: the row says so"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_row project FAIL "fugaro-prod without --production: the row is a refusal"
  expect_contains "$err" "refused unless --production" "fugaro-prod without --production: the reason"
  no_calls "fugaro-prod without --production"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prodok
  prepare "$dir"
  token_doc "$dir/state/token" password fugaro-prod 3600
  run_check "$dir" -- --project fugaro-prod --production
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project "production, named with --production" "fugaro-prod with --production: the row says so"
  expect_row signin "live ID token of fugaro-prod" "fugaro-prod with --production: the token is read against it"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project "belong-staging"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "names belong" "a project naming belong: the refusal"
  no_calls "a project naming belong"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_row agent-session FAIL "a coding agent's session: the row is a refusal"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal names the marker"
  no_calls "a coding agent's session"
}

test_xtrace_is_refused() {
  # A traced run prints every command, and this check carries a sign-in: the
  # config file's path is fine, its contents would not be.
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  expect_absent "$out$err" "$signature_marker" "a traced shell: no token is printed"
  no_calls "a traced shell"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK572_KEEP_SCRATCH=1
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
  no_calls "an unknown argument"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_token_never_reaches_the_output_or_the_scratch
t test_the_other_spelling_of_an_email_link_sign_in_is_read
t test_an_sso_sign_in_is_not_measured
t test_a_provider_that_is_neither_is_not_measured
t test_a_token_of_another_project_is_not_measured
t test_an_expired_token_is_not_measured
t test_a_file_that_is_not_a_token_is_not_measured
t test_a_token_file_that_is_not_0600_is_not_measured
t test_a_token_file_that_is_not_there_is_not_measured
t test_an_origin_that_is_not_the_api_is_not_measured
t test_an_origin_that_answers_another_code_is_not_measured
t test_a_token_the_api_refuses_is_not_measured
t test_the_operations_screens_admitting_the_sign_in_is_a_finding
t test_an_account_that_is_not_an_operations_member_is_not_measured
t test_an_account_that_is_not_a_reseller_member_is_not_measured
t test_a_refusal_with_another_code_is_not_measured
t test_a_refusal_with_another_sentence_is_not_measured
t test_a_read_that_answers_nothing_is_not_measured
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check572_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check572_test.sh: ok"
