#!/usr/bin/env bash
# Tests for scripts/live-checks/check563.sh, with a fake `fugaro` on PATH:
# NOTHING here reaches a cloud, a licence site or a real installation, no agent
# runs a live command, and the fake records every call it is given — the
# arguments and the directory it was made in — so a case can assert what the
# check ran and what it did NOT run.
#
# NO FAKE `curl` IS NEEDED, and the check has no `curl` on its PATH either: the
# device flow's two calls (the start and the poll) are the CLI's own, made inside
# `fugaro licence login --device`, so this check opens no socket of its own. What
# it reads is the CLI's OUTPUT — the address and the user code it prints — so the
# fake answers the three commands the check runs (`doctor --json`, `licence
# status --json` and `licence login --device`) and nothing else, out of the
# case's own files: the device block it prints, a fresh `licence status` document
# per call (so the `--expiry` half's before and after can differ), and a bounded
# sleep before the exit status a case names.
#
# Usage: bash scripts/live-checks/check563_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check563_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check563_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check563.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK563_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK563_TEST_HEAD:-1600}")" ;;
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
# no_calls: the fake `fugaro` was given nothing at all, which is how a case says
# a guard or a setup row stopped the check before it ran a command.
no_calls() { # what
  local log="$dir/state/logs/fugaro.calls"
  if [ -s "$log" ]; then
    fail "$1: a command was run: $(cat "$log")"
  fi
}

site=https://keys.fugaro.dev
project=chk563-staging
# device_marker is a device code a case puts in the CLI's output to prove the
# check never surfaces it. A device code is never the user code a person types,
# and the check reads the user code and MUST NOT read or print this.
device_marker=CHK563-DEVICE-CODE-CANARY

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check563_test.sh only: it
# answers `doctor --json`, `licence status --json` (a fresh document per call)
# and `licence login --device` (the device block a case wrote, then a bounded
# sleep and an exit status) out of $FAKE563_DIR, and records every call — with
# the directory it was made in — so a case can assert what the check ran and, to
# the point, what it did NOT. Nothing here reaches a cloud or a licence site.
set -euo pipefail
dir=${FAKE563_DIR:?}
mkdir -p "$dir/logs"
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/fugaro.calls"
case "${1:-}" in
doctor)
  if [ -f "$dir/doctor.json" ]; then cat "$dir/doctor.json"; fi
  exit "$(cat "$dir/doctor.code" 2>/dev/null || echo 0)"
  ;;
licence)
  case "${2:-}" in
  status)
    n=$(( $(cat "$dir/status.count" 2>/dev/null || echo 0) + 1 ))
    printf '%s\n' "$n" >"$dir/status.count"
    printf '%s\n' "$n" >>"$dir/logs/status.seq"
    f="$dir/status-$n.json"
    if [ ! -f "$f" ]; then f="$dir/status-1.json"; fi
    if [ -f "$f" ]; then cat "$f"; fi
    exit "$(cat "$dir/status.code" 2>/dev/null || echo 0)"
    ;;
  login)
    if [ -f "$dir/device.block" ]; then cat "$dir/device.block"; fi
    if [ -f "$dir/login.err" ]; then cat "$dir/login.err" >&2; fi
    s=$(cat "$dir/login.sleep" 2>/dev/null || echo 0)
    if [ "$s" -gt 0 ] 2>/dev/null; then
      sleep "$s" &
      sp=$!
      # The check kills the login when its bound runs out, so the sleep goes with
      # it: a case that measures a poll that did not stop leaves nothing behind.
      trap 'kill "$sp" 2>/dev/null || true' TERM INT
      wait "$sp" 2>/dev/null || true
    fi
    exit "$(cat "$dir/login.code" 2>/dev/null || echo 0)"
    ;;
  esac
  ;;
esac
printf 'fake fugaro: no route for %s\n' "$*" >&2
exit 7
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ fixtures
# device_block FILE ADDRESS CODE TTL: the block `fugaro licence login --device`
# prints (ee/licence/login's `Client.Device`), exactly as the CLI lays it out —
# the address, the user code, and the sentence naming the code's life. No device
# code is here, because the CLI never prints one.
device_block() { # device_block FILE ADDRESS CODE TTL
  {
    printf 'Open this address in a browser, on any machine:\n\n  %s\n\n' "$2"
    printf 'and type this code when it asks:\n\n  %s\n\n' "$3"
    printf 'The code is worth %s. Waiting for the sign-in to be approved.\n' "$4"
  } >"$1"
}
doctor_json() { # doctor_json FILE NAME PROJECT REGION
  printf '{"project":{"name":"%s","gcp_project":"%s","region":"%s"}}\n' "$2" "$3" "$4" >"$1"
}
# status_json FILE SOURCE STATE TIER PAID: the shape `fugaro licence status
# --json` answers (ee/register/licence.go). It carries no key, which is why the
# check reads the installed licence through it and nothing else.
status_json() { # status_json FILE SOURCE STATE TIER PAID
  printf '{"project":"%s","state":"%s","tier":"%s","seats":1,"paid_through":"%s","kind":"subscription","renewal_mode":"auto","source":"%s","revoked":false,"last_check":"2026-11-21T00:00:00Z","warning":""}\n' \
    "$project" "$3" "$4" "$5" "$2" >"$1"
}

# prepare DIR: the happy path's state — a real installation named by `doctor`,
# a device block whose address and code hold, an approved sign-in (the fake ends
# zero), and an installation whose licence reads a project key.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/checkout" "$dir/tmp" "$dir/home" "$dir/logs" "$dir/state"
  write_fake_fugaro "$dir"
  doctor_json "$dir/state/doctor.json" chk563-install "$project" europe-west1
  status_json "$dir/state/status-1.json" project active pro 2027-01-01
  status_json "$dir/state/status-2.json" project active pro 2027-01-01
  device_block "$dir/state/device.block" "$site/cli/device" BCDF-GHJK 10m0s
  printf '1\n' >"$dir/state/login.sleep"
  printf '0\n' >"$dir/state/login.code"
  rm -f "$dir/state/login.err"
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
  mkdir -p "$dir/tmp" "$dir/home" "$dir/checkout" "$dir/state/logs"
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/status.seq"
  rm -f "$dir/state/status.count"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures the trace refusal is
  # the only way to trace a run that is refusing to be traced.
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
  # stdin is /dev/null: the check is written to ask nothing, so a run that tried
  # to read would see EOF rather than hang the suite.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE563_DIR="$dir/state" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/checkout" ${cli[@]+"${cli[@]}"} </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check563\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1 || true)
  if [ -n "${CHECK563_TEST_DUMP:-}" ]; then printf -- "--- %s (exit %s)\n%s\n" "$dir" "$status" "$out" >&2; fi
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
  expect_contains "$out" "bash scripts/live-checks/check563.sh --sandbox" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, who runs it and what it never prints.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "A NOTE ON WHAT IS NEVER PRINTED:" "--help: the never-printed note"
  expect_contains "$out" "never read and never printed" "--help: what is never printed"
  expect_contains "$out" "\`fugaro-prod\` is refused unless \`--production" "--help: the production guard"
  expect_contains "$out" "CHECK563_EXPIRY" "--help: a knob"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_row address "planned:" "a dry run: the address the flow would print is planned, not read"
  expect_row code "planned:" "a dry run: the code's shape is planned"
  expect_contains "$err" "/cli/device" "a dry run: the approve route is planned"
  expect_contains "$err" "10m0s" "a dry run: the ten-minute life is planned"
  no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row xtrace PASS "the happy path: no trace"
  expect_row site "$site" "the happy path: the site is the licence site"
  expect_row project "$project" "the happy path: the installation's project"
  expect_row sandbox "chk563-install" "the happy path: the installation is named"
  expect_row address "no browser was opened on this machine" "the happy path: the address row names the no-browser promise"
  expect_row code "the ten-minute life" "the happy path: the code row names its life"
  expect_row signin "source 'project'" "the happy path: the sign-in installed a project key"
  expect_row expiry INFO "not this run's half" "the happy path: the expiry half is not this run's"
  expect_row restore PASS "the happy path: the restore row"
  expect_contains "$out" "MANUAL: open $site/cli/device" "the happy path: the MANUAL line itself"
  expect_contains "$out" "CHECK 563 RESULT END (exit 0)" "the happy path: the block's end"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "fugaro licence login --device (in $dir/checkout)" "the happy path: the login ran in the checkout"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "fugaro licence status --json (in $dir/checkout)" "the happy path: the licence was read in the checkout"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "fugaro doctor --json (in $dir/checkout)" "the happy path: the installation was read in the checkout"
  assert_scratch_removed "the happy path"
}

test_the_device_code_never_reaches_the_output() {
  local dir=$work/canary
  prepare "$dir"
  printf 'device_code: %s\n' "$device_marker" >>"$dir/state/device.block"
  run_check "$dir"
  expect_status "$status" 0 "the device-code canary"
  # The CLI's output carries the device code here; the check must never surface
  # it, in a row, on stderr or in the MANUAL line.
  if ! grep -q "$device_marker" "$dir/state/device.block"; then
    fail "the device-code canary: the case did not put the canary in the fake's output"
  fi
  expect_absent "$out$err" "$device_marker" "the device-code canary: the device code is not printed"
  expect_row code "BCDF-GHJK" "the device-code canary: the USER code is what the row prints"
}

test_a_code_that_is_not_the_K2_4_shape_is_a_finding() {
  local dir=$work/shape
  prepare "$dir"
  device_block "$dir/state/device.block" "$site/cli/device" "ABCD-EFG0" 10m0s
  run_check "$dir"
  expect_status "$status" 1 "a code of the wrong shape"
  expect_row address PASS "a code of the wrong shape: the address still held"
  expect_row code FAIL "a code of the wrong shape: the row is a finding"
  expect_row code "eight characters of its alphabet" "a code of the wrong shape: the row says what it read"
  expect_row signin FAIL "a code of the wrong shape: the sign-in was not reached"
}

test_a_code_that_is_not_ten_minutes_is_a_finding() {
  local dir=$work/ttl
  prepare "$dir"
  device_block "$dir/state/device.block" "$site/cli/device" BCDF-GHJK 30m0s
  run_check "$dir"
  expect_status "$status" 1 "a code worth 30m0s"
  expect_row code FAIL "a code worth 30m0s: the row is a finding"
  expect_row code "not the ten-minute code" "a code worth 30m0s: the row says what it read"
}

test_an_address_that_is_not_the_device_page_is_a_finding() {
  local dir=$work/addrpath
  prepare "$dir"
  device_block "$dir/state/device.block" "$site/cli/other" BCDF-GHJK 10m0s
  run_check "$dir"
  expect_status "$status" 1 "an address on another route"
  expect_row address FAIL "an address on another route: the row is a finding"
  expect_row address "not the site's own device page" "an address on another route: the row names what it expected"
}

test_an_address_that_is_not_https_is_a_finding() {
  local dir=$work/addrscheme
  prepare "$dir"
  device_block "$dir/state/device.block" "http://keys.fugaro.dev/cli/device" BCDF-GHJK 10m0s
  run_check "$dir"
  expect_status "$status" 1 "an address that is not https"
  expect_row address FAIL "an address that is not https: the row is a finding"
}

test_a_site_that_is_not_the_licence_site_is_not_measured() {
  local dir=$work/site
  prepare "$dir"
  run_check "$dir" -- --site https://portal.chk563.test
  expect_status "$status" 2 "a site that is not the licence site"
  expect_row site FAIL "a site that is not the licence site: the row is not a pass"
  expect_row site "is not the licence site" "a site that is not the licence site: the refusal says why"
  no_calls "a site that is not the licence site"
}

test_an_installation_with_no_firebase_project_is_not_measured() {
  local dir=$work/noworkspace
  prepare "$dir"
  rm -f "$dir/state/device.block"
  printf 'fugaro: this installation has no Firebase project: run `fugaro update --firebase-project <project> --name <name>` first\n' >"$dir/state/login.err"
  printf '1\n' >"$dir/state/login.code"
  run_check "$dir"
  expect_status "$status" 2 "an installation with no Firebase project"
  expect_row sandbox PASS "an installation with no Firebase project: the premise still held"
  expect_row address FAIL "an installation with no Firebase project: the row is not a pass"
  expect_row address "no Firebase project" "an installation with no Firebase project: the row names the fix"
}

test_a_doctor_that_answers_no_installation_is_not_measured() {
  local dir=$work/noinstall
  prepare "$dir"
  doctor_json "$dir/state/doctor.json" "" "" ""
  run_check "$dir"
  expect_status "$status" 2 "a doctor that answers no installation"
  expect_row sandbox FAIL "a doctor that answers no installation: the row is not a pass"
  expect_row sandbox "not a real installation's" "a doctor that answers no installation: the refusal says why"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "a doctor that answers no installation: no login ran"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  doctor_json "$dir/state/doctor.json" chk563-install fugaro-prod europe-west1
  run_check "$dir"
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_row project FAIL "fugaro-prod without --production: the row is a refusal"
  expect_row project "refused unless --production" "fugaro-prod without --production: the reason"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "licence login" "fugaro-prod without --production: no login ran"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prodok
  prepare "$dir"
  doctor_json "$dir/state/doctor.json" chk563-install fugaro-prod europe-west1
  run_check "$dir" -- --production
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project PASS "fugaro-prod with --production: the row says so"
  expect_row project "production, named with --production" "fugaro-prod with --production: the row says it was named on purpose"
  expect_row signin "source 'project'" "fugaro-prod with --production: the sign-in was read against it"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --sandbox "$dir/belong-install"
  expect_status "$status" 1 "a sandbox naming belong"
  expect_row belong FAIL "a sandbox naming belong: the row is a refusal"
  expect_contains "$err" "names belong" "a sandbox naming belong: the refusal"
  no_calls "a sandbox naming belong"
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
  # device flow's output would be copied a second time by the trace.
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_row xtrace FAIL "a traced shell: the row is a refusal"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  expect_absent "$out$err" "$device_marker" "a traced shell: no device code is printed"
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

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK563_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch: INFO" "a kept scratch directory: the row"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

# approve the flow by hand, in the case that measures the expiry half: the file
# the CLI writes on expiry is the client's own sentence (ee/licence/login).
expiry_login() { # dir
  printf 'the sign-in was not finished before its code expired\n' >"$1/state/login.err"
  printf '1\n' >"$1/state/login.code"
}

test_the_expiry_half_holds() {
  local dir=$work/expiry
  prepare "$dir"
  expiry_login "$dir"
  run_check "$dir" -- --expiry --wait 5
  expect_status "$status" 0 "the expiry half"
  expect_row expiry PASS "the expiry half: the row holds"
  expect_row expiry "ran out at its own ten minutes" "the expiry half: the poll stopped at the code's life"
  expect_row expiry "UNCHANGED" "the expiry half: nothing was written"
  expect_row signin INFO "not this run's half" "the expiry half: the sign-in half is not this run's"
  # the before read and the after read are the two status calls.
  expect_contains "$(cat "$dir/state/logs/status.seq")" "2" "the expiry half: the licence was read before and after"
  assert_scratch_removed "the expiry half"
}

test_a_licence_that_moved_under_expiry_is_a_finding() {
  local dir=$work/expirymoved
  prepare "$dir"
  expiry_login "$dir"
  status_json "$dir/state/status-1.json" machine free "" ""
  status_json "$dir/state/status-2.json" project active pro 2027-01-01
  run_check "$dir" -- --expiry --wait 5
  expect_status "$status" 1 "a licence that moved under --expiry"
  expect_row expiry FAIL "a licence that moved under --expiry: the row is a finding"
  expect_row expiry "changed under --expiry" "a licence that moved under --expiry: the row says what it read"
}

test_a_poll_that_did_not_stop_at_the_expiry_is_a_finding() {
  local dir=$work/poll
  prepare "$dir"
  expiry_login "$dir"
  printf '30\n' >"$dir/state/login.sleep"
  run_check "$dir" -- --expiry --wait 1
  expect_status "$status" 1 "a poll that ran past the bound"
  expect_row expiry FAIL "a poll that ran past the bound: the row is a finding"
  expect_row expiry "did not stop at the code's own expiry" "a poll that ran past the bound: the row says so"
}

test_an_approval_that_never_came_is_not_measured() {
  local dir=$work/approval
  prepare "$dir"
  printf '30\n' >"$dir/state/login.sleep"
  run_check "$dir" -- --approve --wait 1
  expect_status "$status" 2 "an approval that never came"
  expect_row signin FAIL "an approval that never came: the row is not a pass"
  expect_row signin "was not approved within" "an approval that never came: the row says so"
}

test_a_login_that_ended_zero_without_a_key_is_a_finding() {
  local dir=$work/nokey
  prepare "$dir"
  status_json "$dir/state/status-1.json" machine free "" ""
  run_check "$dir"
  expect_status "$status" 1 "a login that ended zero without a project key"
  expect_row signin FAIL "a login that ended zero without a project key: the row is a finding"
  expect_row signin "ended 0 without a key" "a login that ended zero without a project key: the row says so"
}

test_a_key_that_is_not_active_is_a_finding() {
  local dir=$work/notactive
  prepare "$dir"
  status_json "$dir/state/status-1.json" project expired pro 2020-01-01
  run_check "$dir"
  expect_status "$status" 1 "a key in state expired"
  expect_row signin FAIL "a key in state expired: the row is a finding"
  expect_row signin "active (or in its own grace)" "a key in state expired: the row says what it expected"
}

test_a_status_that_cannot_be_read_is_not_measured() {
  local dir=$work/nostatus
  prepare "$dir"
  printf '1\n' >"$dir/state/status.code"
  run_check "$dir"
  expect_status "$status" 2 "a licence read that does not answer"
  expect_row signin FAIL "a licence read that does not answer: the row is not a pass"
  expect_row signin "did not answer" "a licence read that does not answer: the row says so"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_device_code_never_reaches_the_output
t test_a_code_that_is_not_the_K2_4_shape_is_a_finding
t test_a_code_that_is_not_ten_minutes_is_a_finding
t test_an_address_that_is_not_the_device_page_is_a_finding
t test_an_address_that_is_not_https_is_a_finding
t test_a_site_that_is_not_the_licence_site_is_not_measured
t test_an_installation_with_no_firebase_project_is_not_measured
t test_a_doctor_that_answers_no_installation_is_not_measured
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_an_unknown_argument_is_refused
t test_kept_scratch_is_reported
t test_the_expiry_half_holds
t test_a_licence_that_moved_under_expiry_is_a_finding
t test_a_poll_that_did_not_stop_at_the_expiry_is_a_finding
t test_an_approval_that_never_came_is_not_measured
t test_a_login_that_ended_zero_without_a_key_is_a_finding
t test_a_key_that_is_not_active_is_a_finding
t test_a_status_that_cannot_be_read_is_not_measured

if [ "$failures" -ne 0 ]; then
  echo "check563_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check563_test.sh: ok"
