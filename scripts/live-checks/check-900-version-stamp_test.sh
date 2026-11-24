#!/usr/bin/env bash
# Tests for scripts/live-checks/check-900-version-stamp.sh. NOTHING here is a
# cloud, a project or a database: every case writes the project's local config,
# the node and doctor's document in a temporary directory and drives the check
# against fake `gcloud`, `curl` and `fugaro` on PATH, whose answers the case owns
# and which record their whole argv and what their stdin turned out to be.
#
# The readings are the point. Check 900 judges four things: the node's shape
# against the schema's rules, whether its `min` is the installed release,
# whether doctor's `cli-below-required` row agrees with that release, and
# whether a cold read of the stamp fits versionstamp.ReadTimeout. So the cases
# bend exactly those: a well-formed stamp, a malformed one of each kind the
# schema refuses, a stamp below and above this release, a row that disagrees, a
# row that was skipped, and a cold read that is too slow. The fake `fugaro`
# models the one thing the timing rests on: a `runs ls` whose stamp cache is
# absent does the cold read — it takes FAKE900_COLD_MS milliseconds and lands the
# cache — and one whose cache is there does not.
#
# Every run has its stdin closed and a timeout, so a check that waited for input
# would end the case as a failure rather than hang it.
#
# Usage: bash scripts/live-checks/check-900-version-stamp_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-900-version-stamp.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK900_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK900_TEST_HEAD:-2400}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; out: $(printf '%s' "$out" | head -c 1600); err: $(printf '%s' "$err" | head -c 800)"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c "${CHECK900_TEST_HEAD:-2400}")"
}
expect_no_row_state() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    fail "$4: expected no row '$2' reading $3"
  fi
}
expect_row_count() { # log prefix want what
  local got
  got=$(grep -c -- "$2" "$1" || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 calls matching '$2', got $got"
}

TOKEN_MARKER=chk900-fake-access-token-marker
CONFIG_MARKER=chk900-config-value-marker

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
printf 'gcloud %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE900_LOG:?}"
case "$1 $2" in
"auth print-access-token") printf '%s\n' "${FAKE900_TOKEN:?}" ;;
*)
  printf 'fake gcloud: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# The fake curl answers only through the config file the check hands it: the
# token must be in that file and nowhere else, so an Authorization header that
# reached the argv instead is answered 401. The node's own status code and body
# come from the case.
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
printf 'curl %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE900_LOG:?}"
dir=${FAKE900_DIR:?}
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
if [ -n "$conf" ] && grep -q "Authorization: Bearer ${FAKE900_TOKEN:?}" "$conf" 2>/dev/null; then
  perm=$(stat -c '%a' "$conf" 2>/dev/null || stat -f '%Lp' "$conf" 2>/dev/null)
  printf 'curl-config-mode %s\n' "$perm" >>"$FAKE900_LOG"
  case "$url" in
  */config/policy/required_cli.json)
    code=$(cat "$dir/node-status" 2>/dev/null || printf 200)
    body=$(cat "$dir/node.json")
    ;;
  *) code=404 body=null ;;
  esac
fi
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; else printf '%s' "$body"; fi
if [ -n "$fmt" ]; then printf '%s' "${fmt//%\{http_code\}/$code}"; fi
FAKE
chmod +x "$fakebin/curl"

# The fake fugaro answers the three commands Check 900 calls, and models the one
# thing its timing rests on: `runs ls` with no stamp cache under the cache home
# it was handed does the cold read — FAKE900_COLD_MS milliseconds, then the cache
# file a real cold read writes — and with the cache there it does not. So the
# check's `stamp` reading is the cold one and its `warm` reading is not, which is
# what the criterion is computed from. FAKE900_STAMP_READ says how those cold
# reads go: `ok` lands the cache, `fail` never does (the read timed out and the
# fence fell open), and `once` lands it on the first only.
cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'fugaro %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE900_LOG:?}"
dir=${FAKE900_DIR:?}
case "$1" in
version)
  printf '%s\n' "${FAKE900_VERSION:-0.30.0}"
  ;;
doctor)
  cat "$dir/doctor.json"
  exit "${FAKE900_DOCTOR_STATUS:-0}"
  ;;
runs)
  [ "${2:-}" = ls ] || { printf 'fake fugaro: unexpected verb %s\n' "$*" >&2; exit 64; }
  cache=${XDG_CACHE_HOME:?}/fugaro/required-cli
  n=0
  for f in "$cache"/*.json; do
    [ -e "$f" ] || continue
    n=$((n + 1))
  done
  if [ "$n" -eq 0 ]; then
    reads=$(cat "$dir/reads" 2>/dev/null || printf 0)
    reads=$((reads + 1))
    printf '%s' "$reads" >"$dir/reads"
    ms=${FAKE900_COLD_MS:-0}
    if [ "$ms" -gt 0 ]; then
      python3 -c 'import sys, time; time.sleep(int(sys.argv[1]) / 1000.0)' "$ms"
    fi
    lands=yes
    case "${FAKE900_STAMP_READ:-ok}" in
    fail) lands=no ;;
    once) [ "$reads" -eq 1 ] || lands=no ;;
    esac
    if [ "$lands" = yes ]; then
      mkdir -p "$cache"
      printf '{"schema":1,"min":"%s","set_at":"2026-11-24T00:00:00Z","read_at":"2026-11-24T00:00:00Z"}\n' \
        "${FAKE900_VERSION:-0.30.0}" >"$cache/01234567.json"
    fi
  fi
  printf '{"runs":[]}\n'
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

case_dir() { # case_dir <name>
  cdir=$work/$1
  answers=$cdir/answers
  confighome=$cdir/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS=$cdir/calls
  : >"$CALLS"
  FAKE900_VERSION=0.30.0
  FAKE900_DOCTOR_STATUS=0
  FAKE900_COLD_MS=0
  FAKE900_STAMP_READ=ok
  CHECK900_COLD_MS=2000
  CHECK900_REPEATS=3
  : >"$answers/reads"
  write_config
  write_node
  write_doctor
}

# The project's own config as `fugaro update` leaves it, in the indented shape
# yaml.v3 writes. The API key's value stands in for anything the check must read
# past without printing.
write_config() {
  {
    printf 'version: 3\n'
    printf 'name: fugaro\n'
    printf 'gcp_project: fugaro-gcp\n'
    printf 'region: us-east5\n'
    printf 'budget:\n'
    printf '    rtdb_url: https://fugaro-fb-default-rtdb.firebaseio.com\n'
    printf '    firebase_project: fugaro-fb\n'
    printf '    firebase_api_key: %s\n' "$CONFIG_MARKER"
    printf 'repos:\n'
    printf '    acme/app:\n'
    printf '        workflows:\n'
    printf '            - default\n'
  } >"$confighome/fugaro/projects/fugaro.yaml"
}

# The node as `fugaro update`'s version-stamp stage writes it.
write_node() { # write_node [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/node.json"
  else
    printf '%s' '{"schema":1,"min":"0.30.0","set_at":"2026-11-01T09:00:00Z"}' >"$answers/node.json"
  fi
  printf '%s' "${FAKE900_NODE_STATUS:-200}" >"$answers/node-status"
}

# doctor's document, as --json prints it: the cli-below-required row beside the
# other checks, ok when this binary is at or above the stamp.
write_doctor() { # write_doctor [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/doctor.json"
    return
  fi
  cat >"$answers/doctor.json" <<'JSON'
{"ok":false,"checks":[
 {"id":"cli-version","ok":true},
 {"id":"cli-below-required","ok":true,"class":"update"}
]}
JSON
}

# ------------------------------------------------------------------ the runs

run900_raw() { # run900_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE900_LOG="${CALLS:-/dev/null}" FAKE900_DIR="${answers:-$work}" \
    FAKE900_TOKEN="$TOKEN_MARKER" FAKE900_VERSION="${FAKE900_VERSION:-0.30.0}" \
    FAKE900_DOCTOR_STATUS="${FAKE900_DOCTOR_STATUS:-0}" FAKE900_COLD_MS="${FAKE900_COLD_MS:-0}" \
    FAKE900_STAMP_READ="${FAKE900_STAMP_READ:-ok}" \
    XDG_CONFIG_HOME="${confighome:-$work/none}" HOME="$work/home" \
    CHECK900_REPEATS="${CHECK900_REPEATS:-3}" CHECK900_COLD_MS="${CHECK900_COLD_MS:-2000}" \
    perl -e 'alarm shift; exec @ARGV or die' 300 bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run900() { # run900
  run900_raw fugaro </dev/null
}

# ------------------------------------------------------------------- the cases

test_a_well_formed_stamp_passes() {
  case_dir well_formed
  run900
  expect_status "$status" 0 "a well-formed stamp"
  expect_contains "$out" "CHECK 900 RESULT: PASS (exit 0)" "a well-formed stamp"
  expect_row "$out" node PASS "a well-formed stamp"
  expect_contains "$out" "schema=1 min=0.30.0 set_at=2026-11-01T09:00:00Z" "the node's three members are printed"
  expect_row "$out" version PASS "a well-formed stamp"
  expect_row "$out" stamp-min PASS "a well-formed stamp"
  expect_row "$out" doctor-row PASS "a well-formed stamp"
  expect_row "$out" cold-read PASS "a well-formed stamp"
  expect_row "$out" restore PASS "a well-formed stamp"
  expect_contains "$out" "config/policy/required_cli.json" "the call quoted is the node's path"
}

test_a_malformed_stamp_fails() {
  case_dir malformed_min
  write_node '{"schema":1,"min":"0.30","set_at":"2026-11-01T09:00:00Z"}'
  run900
  expect_status "$status" 1 "a min that is not a release version"
  expect_contains "$out" "CHECK 900 RESULT: FAIL (exit 1)" "a min that is not a release version"
  expect_row "$out" node FAIL "a min that is not a release version"
  expect_contains "$out" "not a release version" "a min that is not a release version is named"
  expect_row "$out" stamp-min UNVERIFIED "a malformed stamp leaves no min to compare"
  expect_no_row_state "$out" node PASS "a malformed stamp is never a pass"

  case_dir malformed_extra
  write_node '{"schema":1,"min":"0.30.0","set_at":"2026-11-01T09:00:00Z","note":"hand edit"}'
  run900
  expect_status "$status" 1 "a member outside the schema"
  expect_row "$out" node FAIL "a member outside the schema"
  expect_contains "$out" "it carries note" "the member outside the schema is named"

  case_dir malformed_schema
  write_node '{"schema":2,"min":"0.30.0","set_at":"2026-11-01T09:00:00Z"}'
  run900
  expect_status "$status" 1 "a schema this CLI does not know"
  expect_row "$out" node FAIL "a schema this CLI does not know"
  expect_contains "$out" "schema is 2" "the schema is named"

  case_dir malformed_set_at
  write_node '{"schema":1,"min":"0.30.0","set_at":"yesterday"}'
  run900
  expect_status "$status" 1 "a set_at that is not a date-time"
  expect_row "$out" node FAIL "a set_at that is not a date-time"

  case_dir malformed_missing
  write_node '{"schema":1,"min":"0.30.0"}'
  run900
  expect_status "$status" 1 "a member the schema requires and the node does not carry"
  expect_row "$out" node FAIL "a member the schema requires and the node does not carry"
  expect_contains "$out" "it is missing set_at" "the missing member is named"
}

test_an_absent_stamp_fails() {
  case_dir absent
  write_node 'null'
  run900
  expect_status "$status" 1 "an absent node"
  expect_row "$out" node FAIL "an absent node"
  expect_contains "$out" "holds no stamp" "an absent node is named"
  expect_row "$out" stamp-min UNVERIFIED "an absent node leaves no min to compare"
}

test_a_stamp_below_the_installed_release_fails() {
  case_dir below
  write_node '{"schema":1,"min":"0.29.0","set_at":"2026-11-23T09:00:00Z"}'
  run900
  expect_status "$status" 1 "a stamp below the installed release"
  expect_contains "$out" "CHECK 900 RESULT: FAIL (exit 1)" "a stamp below the installed release"
  expect_row "$out" stamp-min FAIL "a stamp below the installed release"
  expect_contains "$out" "min (0.29.0) is BELOW the installed release (0.30.0)" "both releases are named"
  expect_row "$out" doctor-row PASS "a row that says ok above the stamp agrees"
  expect_row "$out" cold-read PASS "the rest of the check still runs"
}

test_a_cli_below_the_stamp_is_reported() {
  case_dir above
  write_node '{"schema":1,"min":"0.31.0","set_at":"2026-11-01T09:00:00Z"}'
  write_doctor '{"ok":false,"checks":[{"id":"cli-below-required","ok":false,"severity":"warning","class":"update","problem":"this fugaro is below the project'"'"'s required release 0.31.0","fix":"brew upgrade fugaro, then fugaro update"}]}'
  run900
  expect_status "$status" 1 "a CLI below the stamp"
  expect_row "$out" stamp-min FAIL "a CLI below the stamp"
  expect_contains "$out" "is BELOW the project's required release (0.31.0)" "the required release is named"
  expect_contains "$out" "brew upgrade fugaro" "the fix names the brew command"
  expect_row "$out" doctor-row PASS "the warning row agrees with this binary"
}

test_a_doctor_row_that_disagrees_fails() {
  case_dir disagree
  write_doctor '{"ok":false,"checks":[{"id":"cli-below-required","ok":false,"severity":"warning","class":"update","problem":"this fugaro is below the project'"'"'s required release 0.30.0","fix":"brew upgrade fugaro, then fugaro update"}]}'
  run900
  expect_status "$status" 1 "a row that says below while the release is at the stamp"
  expect_row "$out" doctor-row FAIL "a row that says below while the release is at the stamp"
  expect_contains "$out" "cli-below-required row says ok=False" "the disagreement is named"
  expect_contains "$out" "brew upgrade fugaro" "the row's own fix is printed"
  expect_row "$out" stamp-min PASS "the stamp itself agrees"
}

test_a_skipped_doctor_row_is_unverified() {
  case_dir skipped_row
  write_doctor '{"ok":false,"checks":[{"id":"cli-below-required","ok":false,"skipped":true,"severity":"info","class":"update","problem":"the stamp could not be read"}]}'
  run900
  expect_status "$status" 3 "a skipped row"
  expect_contains "$out" "CHECK 900 RESULT: INCOMPLETE (exit 3)" "a skipped row"
  expect_row "$out" doctor-row UNVERIFIED "a skipped row agrees with nothing"
  expect_contains "$out" "was skipped" "the skip is named"
}

test_a_missing_doctor_row_fails() {
  case_dir missing_row
  write_doctor '{"ok":true,"checks":[{"id":"cli-version","ok":true}]}'
  run900
  expect_status "$status" 1 "doctor without the row"
  expect_row "$out" doctor-row FAIL "doctor without the row"
  expect_contains "$out" "no cli-below-required row" "the absent row is named"
}

test_an_unreadable_node_is_never_a_pass() {
  case_dir forbidden
  printf 403 >"$answers/node-status"
  run900
  expect_status "$status" 1 "a node the caller's token cannot read"
  expect_row "$out" node FAIL "a node the caller's token cannot read"
  expect_contains "$out" "U2 answered NO" "a denied read is named as U2's answer"

  case_dir absent_database
  printf 404 >"$answers/node-status"
  run900
  expect_status "$status" 1 "a database that does not hold the node"
  expect_row "$out" node FAIL "a database that does not hold the node"
}

test_a_slow_cold_read_fails() {
  case_dir slow
  FAKE900_COLD_MS=400
  CHECK900_COLD_MS=100
  CHECK900_REPEATS=1
  run900
  expect_status "$status" 1 "a cold read over the bound"
  expect_contains "$out" "CHECK 900 RESULT: FAIL (exit 1)" "a cold read over the bound"
  expect_row "$out" cold-read FAIL "a cold read over the bound"
  expect_contains "$out" "at or over the 100 ms bound" "the bound is named"
  expect_contains "$out" "versionstamp.ReadTimeout" "the timeout it stands for is named"
  expect_row "$out" node PASS "the node is judged on its own"
}

test_a_cold_read_that_lands_no_cache_fails() {
  case_dir no_cache
  FAKE900_STAMP_READ=once
  CHECK900_REPEATS=1
  run900
  expect_status "$status" 1 "a cold read that never completed"
  expect_row "$out" cold-read FAIL "a cold read that never completed"
  expect_contains "$out" "landed NO cache" "the missing cache is named"
}

test_a_binary_that_reads_no_stamp_is_unverified() {
  case_dir no_reader
  FAKE900_STAMP_READ=fail
  CHECK900_REPEATS=1
  # No run lands a cache at all: this is the release that carries no fence
  # rather than one read that timed out, and the check says so instead of
  # failing a project over it.
  run900
  expect_status "$status" 3 "a binary that reads no stamp"
  expect_row "$out" cold-read UNVERIFIED "a binary that reads no stamp"
  expect_contains "$out" "reads no stamp at all" "the reason is named"
}

test_only_reads_are_called() {
  case_dir reads
  run900
  expect_status "$status" 0 "the call set"
  [ -s "$CALLS" ] || fail "the call set: nothing was called"
  local line bad=""
  while IFS= read -r line; do
    case "$line" in
    "gcloud auth print-access-token --quiet (stdin=closed)") ;;
    "curl -q -sS --max-time 30 --config "*" -o "*" -w %{http_code} https://fugaro-fb-default-rtdb.firebaseio.com/config/policy/required_cli.json (stdin=closed)") ;;
    "curl-config-mode 600") ;;
    "fugaro version (stdin=closed)") ;;
    "fugaro doctor --json --project fugaro (stdin=closed)") ;;
    "fugaro runs ls --json --project fugaro (stdin=closed)") ;;
    *) bad="${bad}${line}"$'\n' ;;
    esac
  done <"$CALLS"
  [ -z "$bad" ] || fail "the call set: calls outside the read set: $bad"
  expect_row_count "$CALLS" "fugaro version " 1 "the version is read once"
  expect_row_count "$CALLS" "doctor --json" 1 "doctor is read once"
  expect_row_count "$CALLS" "runs ls --json" 7 "one cold reading and two per repeat"
  expect_row_count "$CALLS" "gcloud " 1 "one token is minted"
  expect_row_count "$CALLS" "curl " 1 "the node is read once"
  expect_not_contains "$(cat "$CALLS")" "runs launch" "nothing is launched"
}

test_the_token_is_never_on_argv() {
  case_dir token
  run900
  expect_status "$status" 0 "the token"
  expect_not_contains "$(cat "$CALLS")" "$TOKEN_MARKER" "the token reached an argv"
  expect_not_contains "$out" "$TOKEN_MARKER" "the token was printed on stdout"
  expect_not_contains "$err" "$TOKEN_MARKER" "the token was printed on stderr"
  expect_not_contains "$out$err" "$CONFIG_MARKER" "a config value the check does not need was printed"
  expect_contains "$(cat "$CALLS")" "curl-config-mode 600" "the curl config file is 0600"
  expect_row "$out" restore PASS "the scratch directory with the token file is removed"
}

test_stdin_is_closed() {
  case_dir stdin
  # An open pipe nobody writes to: a call that inherited it would read it as open.
  run900_raw fugaro < <(sleep 20)
  expect_status "$status" 0 "stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "a call inherited the check's stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=line)" "a call read a line of the check's stdin"
  expect_contains "$(cat "$CALLS")" "(stdin=closed)" "the calls ran with stdin closed"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE900_LOG="$CALLS" FAKE900_DIR="$answers" \
    FAKE900_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_help() {
  CALLS=
  run900_raw --help </dev/null
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK900_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK900_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_a_well_formed_stamp_passes
t test_a_malformed_stamp_fails
t test_an_absent_stamp_fails
t test_a_stamp_below_the_installed_release_fails
t test_a_cli_below_the_stamp_is_reported
t test_a_doctor_row_that_disagrees_fails
t test_a_skipped_doctor_row_is_unverified
t test_a_missing_doctor_row_fails
t test_an_unreadable_node_is_never_a_pass
t test_a_slow_cold_read_fails
t test_a_cold_read_that_lands_no_cache_fails
t test_a_binary_that_reads_no_stamp_is_unverified
t test_only_reads_are_called
t test_the_token_is_never_on_argv
t test_stdin_is_closed
t test_an_agent_session_is_refused
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check-900-version-stamp_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-900-version-stamp_test.sh: ok"
