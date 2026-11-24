#!/usr/bin/env bash
# Tests for scripts/live-checks/check-880-token-utilisation.sh. NOTHING here is
# a cloud, a project or a database: every case writes the installation's local
# config in a temporary directory and drives the check against fake `gcloud`,
# `curl` and `fugaro` on PATH, whose answers the case owns and which record
# their whole argv and what their stdin turned out to be.
#
# The reading is the point. Check 880 judges, per source, the NAMES of the
# rate-limit headers the credentials service recorded for that source's last
# answer, and answers one question: is one of them a utilisation figure (U1,
# D3178 — the gate on PL8). So the cases bend exactly that: a utilisation figure
# recorded, rate-limit headers without one, a source with no names at all, a
# source with no record, a name the service has never served, a named source
# that narrows the judgement, a record that is not the shape the service writes,
# and a record that cannot be read at all.
#
# Two properties are held in every case, because this check's block is a
# terminal's reading of the service's own state: no value the record holds is
# ever printed (the record is names, and a case plants a value to prove the
# block does not carry it), and the access token never reaches an argv or any
# output — the check makes exactly two calls, `gcloud auth print-access-token`
# and one curl GET of the record.
#
# Every run has its stdin closed and a timeout, so a check that waited for input
# would end the case as a failure rather than hang it.
#
# Usage: bash scripts/live-checks/check-880-token-utilisation_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-880-token-utilisation.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK880_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK880_TEST_HEAD:-2400}")" ;;
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
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c "${CHECK880_TEST_HEAD:-2400}")"
}
expect_no_row_state() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    fail "$4: expected no row '$2' reading $3"
  fi
}

TOKEN_MARKER=chk880-fake-access-token-marker
CONFIG_MARKER=chk880-config-value-marker
RECORD_MARKER=chk880-record-value-marker

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
printf 'gcloud %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE880_LOG:?}"
case "$1 $2" in
"auth print-access-token")
  printf '%s\n' "${FAKE880_TOKEN:?}"
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
# reached the argv instead is answered 401. It answers ONE url — the record —
# and anything else is a 404, so a case says the check reads nothing more.
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
printf 'curl %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE880_LOG:?}"
dir=${FAKE880_DIR:?}
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
if [ -n "$conf" ] && grep -q "Authorization: Bearer ${FAKE880_TOKEN:?}" "$conf" 2>/dev/null; then
  perm=$(stat -c '%a' "$conf" 2>/dev/null || stat -f '%Lp' "$conf" 2>/dev/null)
  printf 'curl-config-mode %s\n' "$perm" >>"$FAKE880_LOG"
  case "$url" in
  */creds/observed.json)
    code=$(cat "$dir/observed-status" 2>/dev/null || printf 200)
    body=$(cat "$dir/observed.json")
    ;;
  *) code=404 body=null ;;
  esac
fi
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; else printf '%s' "$body"; fi
if [ -n "$fmt" ]; then printf '%s' "${fmt//%\{http_code\}/$code}"; fi
FAKE
chmod +x "$fakebin/curl"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"${FAKE880_LOG:?}"
printf 'fake fugaro: Check 880 calls no fugaro command\n' >&2
exit 64
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
  write_config
  write_observed
}

# The installation's own config as `fugaro update` leaves it, in the indented
# shape yaml.v3 writes. The API key's value stands in for anything the check
# must read past without printing.
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

# The record as the service writes it: one source, whose last answer carried a
# utilisation figure and the reset beside it, plus two members holding values —
# a time and a marker — which are not a shape the service writes, and which the
# block must not print.
write_observed() { # write_observed [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/observed.json"
    return
  fi
  cat >"$answers/observed.json" <<JSON
{"tool-alpha":{"headers":["Anthropic-Ratelimit-Unified-5h-Utilization","Anthropic-Ratelimit-Unified-5h-Reset"],
 "until":"2026-11-25T04:00:00Z","note":"${RECORD_MARKER}"}}
JSON
}

# ------------------------------------------------------------------ the runs

run880_raw() { # run880_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE880_LOG="${CALLS:-/dev/null}" FAKE880_DIR="${answers:-$work}" \
    FAKE880_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="${confighome:-$work/none}" HOME="$work/home" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run880() { # run880 [sources...]
  run880_raw fugaro "$@" </dev/null
}

# ------------------------------------------------------------------- the cases

test_a_utilisation_figure_is_found() {
  case_dir utilisation
  run880
  expect_status "$status" 0 "a utilisation figure"
  expect_contains "$out" "CHECK 880 RESULT: PASS (exit 0)" "a utilisation figure"
  expect_row "$out" source PASS "a utilisation figure"
  expect_row "$out" utilisation PASS "a utilisation figure"
  expect_row "$out" record PASS "a utilisation figure"
  expect_contains "$out" "tool-alpha records Anthropic-Ratelimit-Unified-5h-Utilization" "the utilisation header is named"
  expect_contains "$out" "a utilisation figure is carried (Anthropic-Ratelimit-Unified-5h-Utilization)" "the figure is named as the answer U1 asks for"
  expect_contains "$out" "utilisation=Anthropic-Ratelimit-Unified-5h-Utilization" "the source table names the figure"
}

test_rate_limit_headers_without_a_figure_fail() {
  case_dir no_figure
  write_observed '{"tool-alpha":{"headers":["Retry-After","Anthropic-Ratelimit-Unified-5h-Reset"]}}'
  run880
  expect_status "$status" 1 "rate-limit headers without a figure"
  expect_contains "$out" "CHECK 880 RESULT: FAIL (exit 1)" "rate-limit headers without a figure"
  expect_row "$out" utilisation FAIL "rate-limit headers without a figure"
  expect_row "$out" source INFO "the source's own record is information, not a finding about the check"
  expect_no_row_state "$out" source FAIL "a source without a figure is never a finding of its own"
  expect_contains "$out" "no source's record names a utilisation figure" "the finding says what was read"
  expect_contains "$out" "Retry-After" "the recorded names are printed"
}

test_no_record_at_all_is_unverified() {
  case_dir absent
  write_observed null
  run880
  expect_status "$status" 3 "an absent record"
  expect_contains "$out" "CHECK 880 RESULT: INCOMPLETE (exit 3)" "an absent record"
  expect_row "$out" record UNVERIFIED "an absent record"
  expect_no_row_state "$out" source FAIL "an absent record judges no source"
}

# An answer that carried no rate-limit header is recorded as recorded and names
# nothing, which is not the same as a source no release has recorded: both are
# UNVERIFIED, and neither is ever a positive.
test_a_source_whose_answer_named_nothing_is_unverified() {
  case_dir no_names
  write_observed '{"tool-alpha":{"headers":[]}}'
  run880
  expect_status "$status" 3 "a source whose answer named nothing"
  expect_row "$out" record PASS "the source's answer WAS recorded"
  expect_row "$out" source UNVERIFIED "a source whose answer named nothing"
  expect_contains "$out" "tool-alpha has no names recorded" "the source is named"
  expect_no_row_state "$out" source PASS "no names is never a positive"
}

# A node with no names member at all is not a shape this release's service
# writes, and nothing can be read from it.
test_a_node_with_no_names_member_is_unverified() {
  case_dir no_member
  write_observed '{"tool-alpha":{}}'
  run880
  expect_status "$status" 3 "a node with no names member"
  expect_row "$out" source UNVERIFIED "a node with no names member"
  expect_contains "$out" "tool-alpha holds a node with no names in it" "the row says what was read"
  expect_no_row_state "$out" source PASS "a node that is not the service's shape is never a positive"
}

test_only_the_sources_named_are_judged() {
  case_dir named
  write_observed '{"tool-alpha":{"headers":["Anthropic-Ratelimit-Unified-5h-Utilization"]},
                "tool-beta":{"headers":["Retry-After"]}}'
  run880 tool-beta
  expect_status "$status" 1 "a named source without a figure"
  expect_contains "$out" "tool-beta records Retry-After" "the named source is judged"
  expect_not_contains "$out" "tool-alpha records" "an unnamed source is not judged"
  expect_contains "$out" "2 sources recorded in creds/observed" "the record's own size is still read"
}

test_a_named_source_the_service_never_served_is_unverified() {
  case_dir unknown_source
  run880 tool-gamma
  expect_status "$status" 3 "a source that is not in the record"
  expect_row "$out" source UNVERIFIED "a source that is not in the record"
  expect_contains "$out" "tool-gamma has no record" "the unknown source is named"
  expect_not_contains "$out" "tool-alpha records" "a name narrows the judgement, so the record's own source is not judged"
}

test_one_source_with_a_figure_passes_beside_one_without() {
  case_dir one_of_two
  write_observed '{"tool-alpha":{"headers":["Anthropic-Ratelimit-Unified-5h-Utilization"]},
                "tool-beta":{"headers":["Retry-After"]}}'
  run880
  expect_status "$status" 0 "one source with a figure beside one without"
  expect_contains "$out" "CHECK 880 RESULT: PASS (exit 0)" "the verdict is existential"
  expect_row "$out" utilisation PASS "the question is answered by one source naming a figure"
  expect_row "$out" source PASS "the source with a figure"
  expect_row "$out" source INFO "the source without one"
  expect_contains "$out" "which does not answer this question" "a key's own rate-limit headers do not withdraw the gate"
  expect_contains "$out" "2 sources recorded in creds/observed" "both sources are read"
}

test_an_empty_record_is_unverified() {
  case_dir empty_record
  write_observed '{}'
  run880
  expect_status "$status" 3 "an empty record"
  expect_contains "$out" "CHECK 880 RESULT: INCOMPLETE (exit 3)" "an empty record"
  expect_row "$out" record UNVERIFIED "an empty record"
  expect_row "$out" utilisation UNVERIFIED "an empty record"
  expect_no_row_state "$out" record PASS "an empty record is never a pass"
  expect_contains "$out" "the record holds no source at all" "the row says what was read"
}

test_a_record_that_is_not_the_shapes_fails() {
  case_dir malformed
  write_observed '[1,2,3]'
  run880
  expect_status "$status" 1 "a record that is not the service's shape"
  expect_row "$out" record FAIL "a record that is not the service's shape"
  expect_no_row_state "$out" record PASS "a malformed record is never a pass"
}

test_a_source_that_is_not_an_observation_fails() {
  case_dir not_an_observation
  write_observed '{"tool-alpha":"exhausted"}'
  run880
  expect_status "$status" 1 "a source that is not an observation"
  expect_row "$out" source FAIL "a source that is not an observation"
  expect_contains "$out" "tool-alpha holds something that is not an observation" "the source is named"
}

test_a_record_that_is_not_a_list_is_unverified() {
  case_dir not_a_list
  write_observed '{"tool-alpha":{"headers":"Retry-After"}}'
  run880
  expect_status "$status" 3 "a record that is not a list of names"
  expect_row "$out" source UNVERIFIED "a record that is not a list of names"
  expect_contains "$out" "holds a record that is not a list of names" "the source is named"
}

test_a_value_in_the_record_is_never_printed() {
  case_dir value
  run880
  expect_status "$status" 0 "a value in the record"
  expect_not_contains "$out" "$RECORD_MARKER" "a field of the record reached the block"
  expect_not_contains "$err" "$RECORD_MARKER" "a field of the record reached stderr"
  expect_not_contains "$out" "2026-11-25T04:00:00Z" "a time beside the names reached the block"
  expect_contains "$out" "Anthropic-Ratelimit-Unified-5h-Utilization" "the names are what the block carries"
}

test_a_hostile_source_name_is_sanitized() {
  case_dir hostile
  write_observed '{"tool\u001b[31mred":{"headers":["Retry-After"]}}'
  run880
  expect_status "$status" 1 "a hostile source name"
  expect_not_contains "$out" "$(printf '\033')" "a control byte reached the block"
  expect_contains "$out" "tool?[31mred records Retry-After" "the name is printed with its control byte replaced"
}

test_an_unreadable_record_is_incomplete() {
  case_dir unreadable
  printf 403 >"$answers/observed-status"
  run880
  expect_status "$status" 2 "an unreadable record"
  expect_contains "$out" "CHECK 880 RESULT: INCOMPLETE (exit 2)" "an unreadable record"
}

test_only_reads_are_called() {
  case_dir reads
  run880
  expect_status "$status" 0 "the call set"
  [ -s "$CALLS" ] || fail "the call set: nothing was called"
  local count=0 line bad=""
  while IFS= read -r line; do
    case "$line" in
    "gcloud auth print-access-token --quiet "*) count=$((count + 1)) ;;
    "curl -q -sS --max-time 30 --config "*" -o "*" -w %{http_code} https://fugaro-fb-default-rtdb.firebaseio.com/creds/observed.json "*) count=$((count + 1)) ;;
    "curl-config-mode 600") ;;
    *) bad="${bad}${line}"$'\n' ;;
    esac
  done <"$CALLS"
  [ -z "$bad" ] || fail "the call set: calls outside the read set: $bad"
  [ "$count" = 2 ] || fail "the call set: expected the token mint and the one read of the record, got $count calls: $(cat "$CALLS")"
  ! grep -q '^fugaro ' "$CALLS" || fail "the call set: a fugaro command was called: $(grep '^fugaro ' "$CALLS")"
  # The check makes no model call of its own: the record is the whole of what it
  # reads, and no other URL is asked for.
  ! grep -q '^curl .*v1/messages' "$CALLS" || fail "the call set: a model route was called"
}

test_the_token_is_never_on_argv() {
  case_dir token
  run880
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
  run880_raw fugaro < <(sleep 20)
  expect_status "$status" 0 "stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "a call inherited the check's stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=line)" "a call read a line of the check's stdin"
  expect_contains "$(cat "$CALLS")" "(stdin=closed)" "the calls ran with stdin closed"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE880_LOG="$CALLS" FAKE880_DIR="$answers" \
    FAKE880_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
  expect_contains "$err" "no agent runs Check 880" "the refusal says why"
}

test_a_traced_shell_is_refused() {
  case_dir traced
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE880_LOG="$CALLS" FAKE880_DIR="$answers" FAKE880_TOKEN="$TOKEN_MARKER" \
    XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash -x "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "a traced shell"
  expect_contains "$err" "this shell is tracing" "the refusal says why"
  [ ! -s "$CALLS" ] || fail "a traced shell: it called something: $(head -c 400 "$CALLS")"
}

test_no_project_is_refused() {
  case_dir no_project
  run880_raw </dev/null
  expect_status "$status" 2 "no project named"
  expect_contains "$err" "no Fugaro project was named" "the refusal says what is missing"
  [ ! -s "$CALLS" ] || fail "no project named: it called something: $(head -c 400 "$CALLS")"
}

test_a_source_name_that_cannot_be_one_is_refused() {
  case_dir bad_name
  run880 'tool alpha'
  expect_status "$status" 2 "a source name that cannot be one"
  expect_contains "$err" "is not a source name" "the refusal says what is wrong"
  [ ! -s "$CALLS" ] || fail "a bad source name: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_config_is_refused() {
  case_dir no_config
  rm -f "$confighome/fugaro/projects/fugaro.yaml"
  run880
  expect_status "$status" 2 "a missing config"
  expect_contains "$err" "no local config for fugaro" "the refusal names the file"
  [ ! -s "$CALLS" ] || fail "a missing config: it called something: $(head -c 400 "$CALLS")"
}

test_help() {
  CALLS=
  run880_raw --help </dev/null
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help"
  expect_contains "$out" "bash scripts/live-checks/check-880-token-utilisation.sh <fugaro-project>" "--help names the run line"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK880_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK880_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_a_utilisation_figure_is_found
t test_rate_limit_headers_without_a_figure_fail
t test_no_record_at_all_is_unverified
t test_a_source_whose_answer_named_nothing_is_unverified
t test_a_node_with_no_names_member_is_unverified
t test_only_the_sources_named_are_judged
t test_a_named_source_the_service_never_served_is_unverified
t test_one_source_with_a_figure_passes_beside_one_without
t test_an_empty_record_is_unverified
t test_a_record_that_is_not_the_shapes_fails
t test_a_source_that_is_not_an_observation_fails
t test_a_record_that_is_not_a_list_is_unverified
t test_a_value_in_the_record_is_never_printed
t test_a_hostile_source_name_is_sanitized
t test_an_unreadable_record_is_incomplete
t test_only_reads_are_called
t test_the_token_is_never_on_argv
t test_stdin_is_closed
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_no_project_is_refused
t test_a_source_name_that_cannot_be_one_is_refused
t test_a_missing_config_is_refused
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check-880-token-utilisation_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-880-token-utilisation_test.sh: ok"
