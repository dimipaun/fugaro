#!/usr/bin/env bash
# Tests for scripts/live-checks/check570.sh, with fake `fugaro` and `curl` on
# PATH: NOTHING here reaches a cloud, a project or the portal, no agent runs a
# live command, and the fakes record every call they are given — with the
# directory it was made in — so a case can assert what the check did and what it
# did NOT do. The check binds a licence, mints a key and renews one, all of which
# are writes to a real installation, which is why every reading and every refusal
# below is measured here rather than by running the check for real.
#
# The fakes are a small model of one installation and its portal: the fake
# `fugaro` answers `version`, `doctor --json` and `licence status --json`, and its
# `licence login --device` REALLY applies the state a binding leaves behind (the
# entitlement bound, the workspace's project id, the key the login minted), the
# way the real command writes it; its `licence refresh` applies whatever the case
# put under `refresh/`. The fake `curl` serves the portal's own shapes out of the
# case's files — the anonymous 401, the entitlement list, the workspace list and
# the binding's 422 `no_installation` — and a case that says the refusal bound the
# licence anyway makes the fake `curl` apply `bind-after/` when it answers.
#
# Every document a case bends is a file the case wrote: a doctor whose project is
# another installation; an entitlement list with no purchase, an already-bound
# one, an air-gapped one or an inactive one; a binding that answers something
# other than 422 `no_installation`, a sentence that does not name `fugaro setup`,
# or a refusal that wrote anyway; a login that exits non-zero or leaves the
# licence unbound; a refresh that refuses; and a licence status carrying another
# seat count than the entitlement's.
#
# Usage: bash scripts/live-checks/check570_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check570_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check570_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check570.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK570_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK570_TEST_HEAD:-1600}")" ;;
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
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

# One installation the cases read and bend: the names are what the rows quote.
install=chk570-test
gcp_project=chk570-test-proj
region=us-east5
api=https://portal.chk570.test
entitlement=ent-chk570-0001
workspace=ws-chk570-0001
# no_installation_project is a project of the owner's that holds no Fugaro
# installation: the binding that D2205 refuses is for it, and it is deliberately
# NOT the installation's own project (which the check refuses by name).
no_installation=chk570-empty-proj
# token_value is what the case's token file holds. It is not a credential and
# opens nothing: it is a sentinel, and the cases assert it never reaches the
# check's own output, its stderr, or the scratch directory it leaves behind.
token_value=IDTOKEN-CHK570-CANARY
token_words='IDTOKEN-CHK570-CANARY'

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check570_test.sh only: it
# answers the three reads the check makes and performs the two writes it drives,
# as writes on the case's own files. `licence login --device` REALLY applies the
# state a binding leaves behind (the overlay the case wrote under login/), the way
# the real command writes the key and the portal writes the binding, so a case
# can bend what a login does to the licence rather than only what it prints. It
# touches no cloud and no key: what it "mints" is a marker file.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE570_DIR/logs/fugaro.calls"
state=${FAKE570_DIR:-}
apply() { # apply DIR — copy a case's overlay onto the live state
  local from=$1 f
  [ -d "$from" ] || return 0
  for f in "$from"/*; do
    [ -f "$f" ] || continue
    cp "$f" "$state/$(basename "$f")"
  done
}
while [ $# -gt 0 ]; do
  case "$1" in
  -*) shift ;;
  *) break ;;
  esac
done
case "${1:-}" in
version)
  echo "fugaro v0.0.0-chk570 (test)"
  ;;
doctor)
  [ -f "$state/doctor.exit" ] && exit "$(cat "$state/doctor.exit")"
  cat "$state/doctor.json"
  ;;
licence)
  case "${2:-}" in
  login)
    [ -f "$state/login.exit" ] && exit "$(cat "$state/login.exit")"
    apply "$state/login"
    if [ -f "$state/login.out" ]; then cat "$state/login.out"; fi
    ;;
  refresh)
    [ -f "$state/refresh.exit" ] && exit "$(cat "$state/refresh.exit")"
    apply "$state/refresh"
    if [ -f "$state/refresh.out" ]; then cat "$state/refresh.out"; fi
    ;;
  status)
    [ -f "$state/status.exit" ] && exit "$(cat "$state/status.exit")"
    cat "$state/status.json"
    ;;
  *)
    echo "fugaro: unknown licence subcommand ${2:-<none>}" >&2
    exit 2
    ;;
  esac
  ;;
*)
  echo "fugaro: unknown command ${1:-<none>}" >&2
  exit 2
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check570_test.sh only: the
# portal's routes are answered out of $FAKE570_DIR, and every request is recorded
# there — the method, the URL, whether an authorization config was carried, and
# the token that config holds, so a case can assert BOTH what the check read and
# that its sign-in never travelled on a command line. Nothing here is a real
# endpoint: the bodies are the case's own files.
set -euo pipefail
dir=${FAKE570_DIR:?}
mkdir -p "$dir/logs"
conf=""
out=""
url=""
method=GET
body=""
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
  --data-binary)
    body=${2:-}
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
path=${url#*://}
path=/${path#*/}
printf 'curl %s %s (config %s) (in %s)\n' "$method" "$path" "${conf:+yes}" "$PWD" >>"$dir/logs/curl.calls"
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >"$dir/logs/auth" 2>/dev/null || true
  python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$dir/logs/auth.mode" 2>/dev/null || printf 'unknown\n' >"$dir/logs/auth.mode"
fi
case "$method $path" in
"POST /api/workspaces/bind")
  printf '%s\n' "$body" >"$dir/logs/bind.body"
  if [ -d "$dir/bind-after" ]; then
    for f in "$dir/bind-after"/*; do
      [ -f "$f" ] || continue
      cp "$f" "$dir/$(basename "$f")"
    done
  fi
  code=$(cat "$dir/bind.code")
  [ -n "$out" ] && cp "$dir/bind.body" "$out"
  ;;
"GET /api/entitlements")
  code=$(cat "$dir/entitlements.code")
  [ -n "$out" ] && cp "$dir/entitlements.json" "$out"
  ;;
"GET /api/workspaces")
  if [ -n "$conf" ]; then
    code=$(cat "$dir/workspaces.code")
    [ -n "$out" ] && cp "$dir/workspaces.json" "$out"
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
entitlements_doc() { # entitlements_doc BOUND SEATS SOURCE STATUS AIRGAPPED
  python3 - "$1" "$2" "$3" "$4" "$5" <<'PY'
import json, sys
bound, seats, source, status, air = sys.argv[1:6]
doc = {"entitlements": [{
    "id": "ent-chk570-0001",
    "organisation_id": "org-chk570",
    "workspace_id": "ws-chk570-0001",
    "source": source,
    "row": "team-monthly",
    "tier": "team",
    "kind": "paid",
    "seats": int(seats),
    "starts_at": "2026-11-20T10:00:00Z",
    "ends_at": "2026-12-20T10:00:00Z",
    "status": status,
    "air_gapped": air == "yes",
    "created_at": "2026-11-20T10:00:00Z",
    "bound_at": None if bound == "-" else "2026-11-21T10:00:00Z",
    "rebindable_at": None if bound == "-" else "2026-12-21T10:00:00Z",
}]}
print(json.dumps(doc))
PY
}

workspaces_doc() { # workspaces_doc BOUND
  python3 - "$1" <<'PY'
import json, sys
bound = sys.argv[1]
doc = {"workspaces": [{
    "id": "ws-chk570-0001",
    "organisation_id": "org-chk570",
    "name": "sandbox",
    "project_id": None if bound == "-" else "chk570-test-proj",
    "entitlement_id": None if bound == "-" else "ent-chk570-0001",
    "created_at": "2026-11-20T09:00:00Z",
    "bound_at": None if bound == "-" else "2026-11-21T10:00:00Z",
}]}
print(json.dumps(doc))
PY
}

status_doc() { # status_doc SEATS
  python3 - "$1" <<'PY'
import json, sys
print(json.dumps({
    "project": "chk570-test-proj",
    "state": "ok",
    "tier": "team",
    "seats": int(sys.argv[1]),
    "paid_through": "2026-12-20T10:00:00Z",
    "kind": "paid",
    "renewal_mode": "auto",
    "revoked": False,
    "last_check": None,
    "warning": None,
}))
PY
}

# prepare DIR: the happy path's state — an unbound sandbox purchase, a portal
# that answers it, and the overlay a successful login applies (the licence bound
# to the installation's own project, and the key it minted).
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home" "$dir/sandbox" "$dir/state/login" "$dir/state/refresh"
  write_fake_fugaro "$dir"
  write_fake_curl "$dir"
  printf 'name: chk570\n' >"$dir/sandbox/fugaro.yaml"
  printf '%s\n' "$token_value" >"$dir/state/token"
  chmod 600 "$dir/state/token"
  python3 - "$install" "$gcp_project" "$region" >"$dir/state/doctor.json" <<'PY'
import json, sys
print(json.dumps({"ok": True, "checks": [],
                  "project": {"name": sys.argv[1], "gcp_project": sys.argv[2], "region": sys.argv[3]}}))
PY
  entitlements_doc - 1 purchase active no >"$dir/state/entitlements.json"
  printf '200\n' >"$dir/state/entitlements.code"
  workspaces_doc - >"$dir/state/workspaces.json"
  printf '200\n' >"$dir/state/workspaces.code"
  printf '{"error":{"code":"unauthenticated","message":"This request carries no sign-in."}}\n' >"$dir/state/anonymous.body"
  printf '401\n' >"$dir/state/anonymous.code"
  printf '{"error":{"code":"no_installation","message":"No Fugaro installation answers in that project. Run `fugaro setup` there first, then bind it again."}}\n' >"$dir/state/bind.body"
  printf '422\n' >"$dir/state/bind.code"
  # What a successful `licence login --device` leaves behind: the portal's two
  # records bound, and the key this installation now holds.
  entitlements_doc 2026-11-21T10:00:00Z 1 purchase active no >"$dir/state/login/entitlements.json"
  workspaces_doc 2026-11-21T10:00:00Z >"$dir/state/login/workspaces.json"
  status_doc 1 >"$dir/state/login/status.json"
  status_doc 1 >"$dir/state/status.json"
  # A refresh of a one-seat licence changes no number: the overlay is empty, and
  # it is there so that a case which needs the refresh to move the seats can put
  # its own status.json in it.
  printf 'nothing to renew yet\n' >"$dir/state/refresh.out"
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
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/curl.calls"
  rm -f "$dir/state/logs/auth" "$dir/state/logs/auth.mode" "$dir/state/logs/bind.body"
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
    FAKE570_DIR="$dir/state" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" --project "$gcp_project" \
    --token-file "$dir/state/token" --no-installation-project "$no_installation" \
    --api "$api" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check570\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  # CHECK570_TEST_DUMP=1 prints the block a case just read: a case that fails on
  # a row is much easier to read with the whole block in front of it.
  if [ -n "${CHECK570_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
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
  expect_contains "$out" "bash scripts/live-checks/check570.sh --sandbox" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "--help: nothing is read"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the check binds and mints, and who runs it.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "one binding and the key it mints" "--help: what it creates"
  expect_contains "$out" "fugaro-prod\` is refused unless \`--production" "--help: the production guard"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK570_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 570 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT project: PASS" "dry run: the project guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "CHECK 570 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "fugaro licence login --device" "dry run: the plan names the login"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "dry run: no doctor is run"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "dry run: no call is made"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_row installation "${install} in ${gcp_project}" "happy path: the installation row"
  expect_row api "401 and code 'unauthenticated'" "happy path: the origin row"
  expect_row token "mode 0600" "happy path: the token row"
  expect_row purchase "bound_at null" "happy path: the unbound purchase row"
  expect_row purchase "F25" "happy path: the row names the finding it is about"
  expect_row binding "422 'no_installation'" "happy path: the refusal row"
  expect_row binding "still unbound" "happy path: the refusal wrote nothing"
  expect_row login "bound ${entitlement}" "happy path: the binding is read back"
  expect_row login "binding is the act that mints" "happy path: the row names D2196"
  expect_row seats "the same 1 after 'fugaro licence refresh'" "happy path: the seats row"
  expect_row restore "nothing of this check's own is left behind" "happy path: the restore row"
  expect_contains "$out" "CHECK 570 RESULT END (exit 0)" "happy path: the end line"
  # What the check RAN, in the order it ran it: the doctor, the login, the two
  # licence reads and the refresh — and each from the checkout.
  local calls
  calls=$(cat "$dir/state/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: the doctor is read in the checkout"
  expect_contains "$calls" "licence login --device (in $dir/sandbox)" "happy path: the login is run in the checkout"
  expect_contains "$calls" "licence refresh (in $dir/sandbox)" "happy path: the refresh is run in the checkout"
  expect_contains "$calls" "licence status --json (in $dir/sandbox)" "happy path: the installation's licence is read"
  # The sign-in: it reached the API in a 0600 config, and never this check's
  # output, its stderr, or the scratch directory it left behind.
  expect_contains "$(cat "$dir/state/logs/curl.calls")" "GET /api/entitlements (config yes)" "happy path: the entitlement read carries the sign-in"
  expect_contains "$(cat "$dir/state/logs/auth.mode")" "600" "happy path: the curl config is 0600"
  expect_contains "$(cat "$dir/state/logs/auth")" "$token_value" "happy path: the token travels in the config"
  expect_absent "$out$err" "$token_words" "happy path: the token is never printed"
  assert_scratch_removed "happy path"
}

test_the_token_never_reaches_the_scratch_directory() {
  # KEEP_SCRATCH is how a person reads back what a run did. What it must never
  # hold is the sign-in: the config the curl calls are made with lives in the
  # scratch directory and holds the token, so this case is about the ONE file
  # that is allowed to — and about nothing else in there carrying it.
  local dir=$work/scratch-token
  prepare "$dir"
  run_check "$dir" CHECK570_KEEP_SCRATCH=1
  expect_contains "$out" "FACT scratch: INFO" "kept scratch: the row"
  [ -n "$scratch" ] || {
    fail "kept scratch: the check named no scratch directory"
    return 0
  }
  local others
  others=$(grep -rl "$token_value" "$scratch" 2>/dev/null | grep -v '\.curlrc$' || true)
  [ -z "$others" ] || fail "kept scratch: the token is in $(printf '%s' "$others" | tr '\n' ' ') and the only file allowed to hold it is a .curlrc"
  rm -rf "$scratch" 2>/dev/null || true
}

test_a_purchase_the_portal_does_not_hold_is_not_measured() {
  local dir=$work/no-purchase
  prepare "$dir"
  entitlements_doc - 1 trial active no >"$dir/state/entitlements.json"
  run_check "$dir"
  expect_status "$status" 2 "no purchase"
  expect_row purchase "Buy one in the portal" "no purchase: the row says what to do first"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "no purchase: nothing is bound"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "workspaces/bind" "no purchase: no binding is attempted"
}

test_an_already_bound_purchase_is_not_measured() {
  local dir=$work/bound
  prepare "$dir"
  entitlements_doc 2026-11-21T10:00:00Z 1 purchase active no >"$dir/state/entitlements.json"
  run_check "$dir"
  expect_status "$status" 2 "an already-bound purchase"
  expect_row purchase "is already bound" "an already-bound purchase: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "an already-bound purchase: nothing is bound again"
}

test_an_air_gapped_purchase_is_not_measured() {
  local dir=$work/airgapped
  prepare "$dir"
  entitlements_doc - 1 purchase active yes >"$dir/state/entitlements.json"
  run_check "$dir"
  expect_status "$status" 2 "an air-gapped purchase"
  expect_row purchase "is air-gapped" "an air-gapped purchase: the row"
  expect_row purchase "D2206" "an air-gapped purchase: the row names the decision"
}

test_a_refusal_that_does_not_name_setup_is_a_finding() {
  # A refusal with the right code and a sentence that does not send the person
  # anywhere is a refusal D2205's row exists for: the code is a client's, and
  # the sentence is what a person acts on.
  local dir=$work/bind-sentence
  prepare "$dir"
  printf '{"error":{"code":"no_installation","message":"That project cannot be bound."}}\n' >"$dir/state/bind.body"
  run_check "$dir"
  expect_status "$status" 1 "a refusal without the sentence"
  expect_row binding "does not name 'fugaro setup'" "a refusal without the sentence: the finding"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "a refusal without the sentence: nothing is bound"
}

test_a_binding_that_answers_another_code_is_a_finding() {
  local dir=$work/bind-code
  prepare "$dir"
  printf '{"error":{"code":"unreachable","message":"That project'"'"'s database could not be reached. Try again."}}\n' >"$dir/state/bind.body"
  printf '503\n' >"$dir/state/bind.code"
  run_check "$dir"
  expect_status "$status" 1 "a binding answered with another code"
  expect_row binding "answered 503 with code 'unreachable'" "a binding answered with another code: the finding"
}

test_a_refusal_that_bound_the_licence_anyway_is_a_finding() {
  # The worst of both: the person is told to run `fugaro setup` and the licence
  # is bound all the same. The check re-reads the entitlement after the refusal
  # for exactly this case.
  local dir=$work/bind-wrote
  prepare "$dir"
  mkdir -p "$dir/state/bind-after"
  entitlements_doc 2026-11-21T10:00:00Z 1 purchase active no >"$dir/state/bind-after/entitlements.json"
  run_check "$dir"
  expect_status "$status" 1 "a refusal that wrote"
  expect_row binding "left ${entitlement} bound" "a refusal that wrote: the finding"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "a refusal that wrote: the run stops there"
}

test_an_origin_that_is_not_the_api_is_not_measured() {
  local dir=$work/not-api
  prepare "$dir"
  printf '<html>the marketing site</html>\n' >"$dir/state/anonymous.body"
  printf '200\n' >"$dir/state/anonymous.code"
  run_check "$dir"
  expect_status "$status" 2 "an origin that is not the API"
  expect_row api "rather than 401" "an origin that is not the API: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "an origin that is not the API: nothing is bound"
}

test_an_origin_that_answers_another_code_is_not_measured() {
  local dir=$work/other-code
  prepare "$dir"
  printf '{"error":{"code":"not_found","message":"No such route."}}\n' >"$dir/state/anonymous.body"
  printf '401\n' >"$dir/state/anonymous.code"
  run_check "$dir"
  expect_status "$status" 2 "an origin whose 401 carries another code"
  expect_row api "code 'not_found'" "an origin whose 401 carries another code: the row"
}

test_a_checkout_that_is_another_installation_is_not_measured() {
  local dir=$work/other-project
  prepare "$dir"
  python3 - >"$dir/state/doctor.json" <<'PY'
import json, sys
print(json.dumps({"ok": True, "checks": [],
                  "project": {"name": "other-install", "gcp_project": "other-project", "region": "us-east5"}}))
PY
  run_check "$dir"
  expect_status "$status" 2 "a checkout of another installation"
  expect_row installation "and --project names" "a checkout of another installation: the row"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "a checkout of another installation: no call is made"
}

test_a_doctor_that_does_not_answer_is_not_measured() {
  local dir=$work/doctor-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/doctor.exit"
  run_check "$dir"
  expect_status "$status" 2 "a doctor that exits 1"
  expect_row installation "exited 1" "a doctor that exits 1: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "a doctor that exits 1: nothing is bound"
}

test_a_doctor_with_no_project_is_not_measured() {
  local dir=$work/doctor-empty
  prepare "$dir"
  printf '{"ok":true,"checks":[]}\n' >"$dir/state/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor with no project"
  expect_row installation "answers no project" "a doctor with no project: the row"
}

test_a_login_that_leaves_the_licence_unbound_is_a_finding() {
  local dir=$work/login-unbound
  prepare "$dir"
  rm -rf "$dir/state/login"
  run_check "$dir"
  expect_status "$status" 1 "a login that bound nothing"
  expect_row login "is still unbound" "a login that bound nothing: the finding"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence refresh" "a login that bound nothing: nothing is refreshed"
}

test_a_login_that_refuses_is_not_measured() {
  local dir=$work/login-refused
  prepare "$dir"
  printf '1\n' >"$dir/state/login.exit"
  printf 'fugaro: this installation has no workspace\n' >"$dir/state/login.err"
  run_check "$dir"
  expect_status "$status" 2 "a refused login"
  expect_row login "exited 1" "a refused login: the row"
}

test_a_login_that_mints_no_key_is_a_finding() {
  # The portal binds and the installation holds nothing: D2196 is that binding
  # IS the mint, so that pair is the finding.
  local dir=$work/no-key
  prepare "$dir"
  printf '{"project":"chk570-test-proj","state":"unlicensed"}\n' >"$dir/state/login/status.json"
  run_check "$dir"
  expect_status "$status" 1 "a login that minted no key"
  expect_row login "carries tier 'none'" "a login that minted no key: the finding"
}

test_a_binding_with_no_project_id_is_a_finding() {
  local dir=$work/no-project-id
  prepare "$dir"
  workspaces_doc - >"$dir/state/login/workspaces.json"
  run_check "$dir"
  expect_status "$status" 1 "a binding with no project id"
  expect_row login "names no project_id" "a binding with no project id: the finding"
}

test_seats_that_do_not_reach_the_installation_are_a_finding() {
  local dir=$work/seats-stale
  prepare "$dir"
  entitlements_doc - 3 purchase active no >"$dir/state/entitlements.json"
  entitlements_doc 2026-11-21T10:00:00Z 3 purchase active no >"$dir/state/login/entitlements.json"
  run_check "$dir" -- --expect-seats 3
  expect_status "$status" 1 "a seat change the refresh did not carry"
  expect_row seats "carries 3 seat(s) and the installation's own licence carries 1" "a seat change the refresh did not carry: the finding"
  expect_row seats "K3.4" "a seat change the refresh did not carry: the row names the decision"
}

test_a_seat_change_that_reaches_the_installation_passes() {
  local dir=$work/seats-fresh
  prepare "$dir"
  entitlements_doc - 2 purchase active no >"$dir/state/entitlements.json"
  entitlements_doc 2026-11-21T10:00:00Z 2 purchase active no >"$dir/state/login/entitlements.json"
  status_doc 2 >"$dir/state/login/status.json"
  status_doc 2 >"$dir/state/status.json"
  run_check "$dir" -- --expect-seats 2
  expect_status "$status" 0 "a seat change that arrived"
  expect_row seats "the same 2 after 'fugaro licence refresh'" "a seat change that arrived: the row"
}

test_expect_seats_the_portal_does_not_hold_is_not_measured() {
  local dir=$work/seats-expect
  prepare "$dir"
  run_check "$dir" -- --expect-seats 5
  expect_status "$status" 2 "an expectation the portal does not meet"
  expect_row seats "carries 1 seat(s) and this check was told to expect 5" "an expectation the portal does not meet: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence refresh" "an expectation the portal does not meet: nothing is refreshed"
}

test_a_token_file_that_is_not_0600_is_not_measured() {
  local dir=$work/token-mode
  prepare "$dir"
  chmod 644 "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a world-readable token file"
  expect_row token "mode 644" "a world-readable token file: the row"
  expect_absent "$out$err" "$token_words" "a world-readable token file: the token is still never printed"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "a world-readable token file: no call is made"
}

test_a_token_file_that_is_not_there_is_not_measured() {
  local dir=$work/token-missing
  prepare "$dir"
  rm -f "$dir/state/token"
  run_check "$dir"
  expect_status "$status" 2 "a token file that is not there"
  expect_row token "is not there" "a token file that is not there: the row"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_contains "$err" "fugaro-prod, which is refused unless --production is given" "fugaro-prod: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "fugaro-prod: nothing is read"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "fugaro-prod: no call is made"
}

test_fugaro_prod_with_production_is_measured() {
  # The guard is a fence, not a prohibition: an owner who says --production gets
  # a run, and the row says which project it is about.
  local dir=$work/prod-by-choice
  prepare "$dir"
  python3 - >"$dir/state/doctor.json" <<'PY'
import json, sys
print(json.dumps({"ok": True, "checks": [],
                  "project": {"name": "fugaro", "gcp_project": "fugaro-prod", "region": "us-east5"}}))
PY
  run_check "$dir" -- --project fugaro-prod --production --dry-run
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project "production, named with --production" "fugaro-prod: the row"
  expect_row project "fugaro-staging first" "fugaro-prod: the row quotes the register's own rule"
}

test_a_bind_target_that_is_the_installation_is_refused() {
  local dir=$work/bind-self
  prepare "$dir"
  run_check "$dir" -- --no-installation-project "$gcp_project"
  expect_status "$status" 2 "a bind target that is the installation"
  expect_contains "$err" "which is the installation this check reads" "a bind target that is the installation: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "a bind target that is the installation: nothing is read"
}

test_a_bind_target_that_is_not_a_project_id_is_refused() {
  local dir=$work/bind-shape
  prepare "$dir"
  run_check "$dir" -- --no-installation-project "Not_A_Project"
  expect_status "$status" 2 "a bind target that is not a project id"
  expect_contains "$err" "is not a Firebase project id" "a bind target that is not a project id: the refusal"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project belong
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "names belong" "belong: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "belong: nothing is read"
}

test_a_coding_agents_session_is_refused() {
  # The check runs `fugaro licence login` and `licence refresh`, which the CLI
  # itself refuses inside an agent's session: refusing here is what keeps the
  # block from reporting those refusals as findings about the release.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the refusal"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "a coding agent's session: nothing is read"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "a coding agent's session: no call is made"
}

test_xtrace_is_refused() {
  # A traced run prints every command, and this check carries a sign-in: the
  # config file's path is fine, its contents would not be.
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  expect_absent "$out$err" "$token_words" "a traced shell: no token is printed"
  expect_absent "$(cat "$dir/state/logs/curl.calls")" "curl" "a traced shell: no call is made"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK570_KEEP_SCRATCH=1
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
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "an unknown argument: nothing is read"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_token_never_reaches_the_scratch_directory
t test_a_purchase_the_portal_does_not_hold_is_not_measured
t test_an_already_bound_purchase_is_not_measured
t test_an_air_gapped_purchase_is_not_measured
t test_a_refusal_that_does_not_name_setup_is_a_finding
t test_a_binding_that_answers_another_code_is_a_finding
t test_a_refusal_that_bound_the_licence_anyway_is_a_finding
t test_an_origin_that_is_not_the_api_is_not_measured
t test_an_origin_that_answers_another_code_is_not_measured
t test_a_checkout_that_is_another_installation_is_not_measured
t test_a_doctor_that_does_not_answer_is_not_measured
t test_a_doctor_with_no_project_is_not_measured
t test_a_login_that_leaves_the_licence_unbound_is_a_finding
t test_a_login_that_refuses_is_not_measured
t test_a_login_that_mints_no_key_is_a_finding
t test_a_binding_with_no_project_id_is_a_finding
t test_seats_that_do_not_reach_the_installation_are_a_finding
t test_a_seat_change_that_reaches_the_installation_passes
t test_expect_seats_the_portal_does_not_hold_is_not_measured
t test_a_token_file_that_is_not_0600_is_not_measured
t test_a_token_file_that_is_not_there_is_not_measured
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_a_bind_target_that_is_the_installation_is_refused
t test_a_bind_target_that_is_not_a_project_id_is_refused
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check570_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check570_test.sh: ok"
