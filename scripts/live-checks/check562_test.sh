#!/usr/bin/env bash
# Test for check562.sh — hermetic: no network beyond the loopback the check
# itself dials. `curl` is faked (any http://127.0.0.1 URL is delegated to the
# real curl, so the check's wrong-state callback reaches a REAL loopback
# listener the fake `fugaro licence login` holds; everything else is served from
# case files). `fugaro` is faked: `doctor --json` and `licence status --json`
# print fixtures, and `licence login` runs a small python listener that prints an
# authorize address on 127.0.0.1 and plays the given mode.
#
# Shardable: CHECK_TEST_SHARD=0/3 runs one third, the shard numbers 0-based as the
# Go test passes them (scripts/live_checks_test.go); CHECK_TEST_ONLY=<name> runs one
# case. Ends with `check562_test.sh: ok` (exit 0) or `check562_test.sh: N
# failure(s)` (exit 1). Every fake process is killed from the exit trap.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script="$here/check562.sh"
REAL_CURL=$(command -v curl || true)

work=$(mktemp -d "${TMPDIR:-/tmp}/check562test.XXXXXX")
failures=0
ran=0
test_index=0
last_out=""; last_err=""; last_status=0
bin=""; case_dir=""; case_site="https://keys.fugaro.dev"
BASHX=""

cleanup_all() {
  pkill -f "$work" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup_all EXIT

fail() { failures=$((failures + 1)); printf '\n    FAIL: %s\n' "$*" >&2; }

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n" runs
# every n-th case starting at i (0-based, as scripts/live_checks_test.go passes it),
# CHECK_TEST_ONLY=name runs that one alone.
t() { # t NAME — filter by shard/only, run case_NAME, print ok when it added no failure
  local name=$1 before i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$CHECK_TEST_ONLY" = "$name" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  ran=$((ran + 1))
  printf '  %-30s ' "$name"
  before=$failures
  "case_$name"
  [ "$failures" -eq "$before" ] && printf 'ok\n'
  return 0
}

# ------------------------------------------------------------------ fake tools
write_fake_curl() { # write_fake_curl DIR
  cat >"$1/curl" <<'SH'
#!/usr/bin/env bash
set -u
out=""; fmt=""; url=""; args=("$@"); i=0
while [ "$i" -lt "$#" ]; do
  a=${args[$i]}
  case "$a" in
    -o) out=${args[$((i + 1))]}; i=$((i + 2)); continue ;;
    -w) fmt=${args[$((i + 1))]}; i=$((i + 2)); continue ;;
    --max-time) i=$((i + 2)); continue ;;
    -*) i=$((i + 1)); continue ;;
    *) url=$a; i=$((i + 1)) ;;
  esac
done
printf '%s\n' "$url" >>"$FAKE562_DIR/calls"
case "$url" in
  http://127.0.0.1:* | http://localhost:*) exec "$FAKE562_REAL_CURL" "$@" ;;
esac
code=$(cat "$FAKE562_DIR/site.code" 2>/dev/null || echo 000)
body=$(cat "$FAKE562_DIR/site.body" 2>/dev/null || true)
[ -n "$out" ] && printf '%s' "$body" >"$out"
printf '%s' "${fmt//\%\{http_code\}/$code}"
SH
  chmod +x "$1/curl"
}

write_fake_fugaro() { # write_fake_fugaro DIR
  cat >"$1/fugaro" <<'SH'
#!/usr/bin/env bash
set -u
printf '%s\n' "$*" >>"$FAKE562_DIR/cli"
case "$1 $2" in
  "doctor --json") cat "$FAKE562_DIR/doctor.json"; exit 0 ;;
  "licence status") cat "$FAKE562_DIR/status.json"; exit 0 ;;
  "licence login")
    mode=$(cat "$FAKE562_DIR/login.mode" 2>/dev/null || echo happy)
    if [ "$mode" = no-workspace ]; then
      printf '%s\n' "project acme has no Firebase project (budget.firebase_project is not set in its project config), so a licence key, which is bound to that project's id, cannot be read or installed here: an operator runs \`fugaro update --firebase-project fugaro-staging --name acme\`" >&2
      exit 1
    fi
    FAKE562_LOGIN_MODE=$mode exec python3 "$FAKE562_DIR/login.py" ;;
esac
exit 0
SH
  chmod +x "$1/fugaro"
}

write_fake_login() { # write_fake_login DIR — the "browser sign-in" a person would do
  cat >"$1/login.py" <<'PY'
import http.server, os, sys, threading, time, urllib.request
from urllib.parse import urlsplit, parse_qsl

mode = os.environ.get("FAKE562_LOGIN_MODE", "happy")
site = os.environ.get("FAKE562_SITE", "https://keys.fugaro.dev").rstrip("/")
STATE = "fake562-flow-state"
CHALLENGE = "fake562-challenge-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"


class H(http.server.BaseHTTPRequestHandler):
    def _reply(self, code, html):
        body = html.encode()
        self.send_response(code)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        q = dict(parse_qsl(urlsplit(self.path).query))
        if q.get("state") == STATE:
            self._reply(200, "<h1>Signed in</h1>")
            if mode != "never-finishes":
                threading.Thread(target=self.server.shutdown, daemon=True).start()
            return
        if mode == "accept-wrong-state":
            self._reply(200, "<h1>ok</h1>")
            return
        self._reply(400, "<h1>This is not the sign-in's own callback</h1><p>Nothing was changed.</p>")
        if mode == "ends-on-wrong-state":
            threading.Thread(target=self.server.shutdown, daemon=True).start()

    def log_message(self, *a):
        pass


srv = http.server.HTTPServer(("127.0.0.1", 0), H)
port = srv.server_address[1]
addr = "%s/cli/authorize?port=%d&state=%s&code_challenge=%s&code_challenge_method=S256&installation=chk562" % (
    site, port, STATE, CHALLENGE)
sys.stdout.write(
    "Opening the licence site in your browser. Sign in there, and pick the licence this installation should run on.\n\n  %s\n\nIf the browser did not open, visit that address yourself.\n" % addr)
sys.stdout.flush()

if mode in ("happy", "key-on-stdout", "key-on-stderr"):
    def browser():
        time.sleep(2.0)
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/callback?state=%s&code=fake562-code" % (port, STATE), timeout=5).read()
        except Exception:
            pass
    threading.Thread(target=browser, daemon=True).start()

srv.serve_forever()

# The two key modes finish the sign-in and then say what no line of the command's
# own output may say, one on each stream: the check reads both.
if mode == "key-on-stdout":
    sys.stdout.write("the licence key fgk_fake562keyvalue is installed\n")
    sys.stdout.flush()
elif mode == "key-on-stderr":
    sys.stderr.write("the licence key fgk_fake562keyvalue is installed\n")
    sys.stderr.flush()
PY
}

# ------------------------------------------------------------------ the harness
new_case() { # new_case NAME — a fresh sandbox, fixtures and fake tools
  case_dir="$work/$1"; bin="$case_dir/bin"
  mkdir -p "$case_dir/home" "$case_dir/tmp" "$case_dir/sandbox" "$bin"
  : >"$case_dir/sandbox/fugaro.yaml"
  printf '403' >"$bin/site.code"
  printf '%s\n' "This sign-in could not be verified. Return to your terminal and try again." >"$bin/site.body"
  printf '%s\n' '{"project":{"name":"acme-checkout","gcp_project":"fugaro-staging","region":"us-central1"}}' >"$bin/doctor.json"
  printf '%s\n' '{"project":"acme-checkout","source":"project","state":"active","tier":"starter","seats":1,"renewal_mode":"card"}' >"$bin/status.json"
  printf 'happy\n' >"$bin/login.mode"
  write_fake_curl "$bin"; write_fake_fugaro "$bin"; write_fake_login "$bin"
  case_site="https://keys.fugaro.dev"
}

run_check() { # run_check NAME [ENV=VAL ...] -- [check args...]
  local name=$1; shift
  local -a envs=() cmd=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do envs+=("$1"); shift; done
  [ "${1:-}" = "--" ] && shift
  last_out="$case_dir/$name.out"; last_err="$case_dir/$name.err"; last_status=0
  cmd=(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT
    -u SHELLOPTS -u BASHOPTS "${envs[@]}"
    PATH="$bin:$PATH" HOME="$case_dir/home" TMPDIR="$case_dir/tmp"
    FAKE562_DIR="$bin" FAKE562_REAL_CURL="$REAL_CURL" FAKE562_SITE="$case_site"
    bash ${BASHX:+-x} "$script" "$@")
  if command -v timeout >/dev/null 2>&1; then
    timeout 60 "${cmd[@]}" >"$last_out" 2>"$last_err" || last_status=$?
  else
    "${cmd[@]}" >"$last_out" 2>"$last_err" || last_status=$?
  fi
}

expect_status() { [ "$last_status" = "$1" ] || fail "expected exit $1, got $last_status (stderr: $(tail -n 3 "$last_err" | tr '\n' ' '))"; }
expect_contains() { grep -qF -- "$2" "$1" || fail "$1 does not contain: $2"; }
expect_absent() { if grep -qF -- "$2" "$1"; then fail "$1 contains (should not): $2"; fi; }
expect_row() { grep -q "^FACT $2: $3" "$1" || fail "$1 has no row '$2: $3' (got: $(grep "^FACT $2:" "$1" | head -n1))"; }
no_calls() {
  [ -s "$bin/calls" ] && fail "an HTTP call was made: $(cat "$bin/calls")"
  [ -s "$bin/cli" ] && fail "fugaro was run: $(cat "$bin/cli")"
  return 0
}
no_login_started() { grep -q 'licence login' "$bin/cli" 2>/dev/null && fail "the sign-in was started"; return 0; }
assert_scratch_removed() { [ -z "$(find "$case_dir/tmp" -maxdepth 1 -name 'check562.*' -print -quit)" ] || fail "the scratch directory was left in $case_dir/tmp"; }
assert_no_leak() { if pgrep -f "$bin/login.py" >/dev/null 2>&1; then fail "a fake login process leaked"; pkill -f "$bin/login.py" >/dev/null 2>&1 || true; fi; }

# --------------------------------------------------------------------- cases
case_help() {
  new_case help
  run_check help -- --help
  expect_status 0
  expect_contains "$last_out" "Usage:"
  expect_contains "$last_out" 'licenceServerURL = "https://keys.fugaro.dev"'
  expect_contains "$last_out" "CHECK562_SANDBOX"
  expect_contains "$last_out" "A NOTE ON WHAT IS NEVER PRINTED"
  expect_absent "$last_out" "FACT "
  no_calls
}

case_dry_run() {
  new_case dry
  run_check dry -- --sandbox "$case_dir/sandbox" --dry-run
  expect_status 0
  expect_row "$last_out" xtrace PASS
  expect_row "$last_out" dry-run PASS
  expect_contains "$last_out" "CHECK 562 RESULT END (exit 0)"
  expect_contains "$last_err" "plan"
  no_calls
  assert_scratch_removed
}

case_happy() {
  new_case happy
  run_check happy -- --sandbox "$case_dir/sandbox" --wait 30
  expect_status 0
  expect_row "$last_out" site PASS
  expect_row "$last_out" sandbox PASS
  expect_row "$last_out" address PASS
  expect_row "$last_out" wrong-state PASS
  expect_row "$last_out" signin PASS
  expect_contains "$last_out" "source 'project'"
  expect_contains "$last_out" "tier 'starter'"
  expect_contains "$last_out" "state 'active'"
  expect_contains "$last_out" "MANUAL:"
  expect_contains "$bin/calls" "/callback"
  expect_contains "$last_out" "CHECK 562 RESULT END (exit 0)"
  assert_scratch_removed
  assert_no_leak
}

case_site_not_licence() {
  new_case siteno
  printf '200' >"$bin/site.code"
  printf '%s\n' "<html><body>Welcome to a marketing page</body></html>" >"$bin/site.body"
  run_check siteno -- --sandbox "$case_dir/sandbox"
  expect_status 2
  expect_row "$last_out" site FAIL
  expect_contains "$last_out" "does not answer as the licence site"
  expect_contains "$last_out" "CHECK 562 RESULT END (exit 2)"
  no_login_started
  assert_scratch_removed
}

case_no_workspace() {
  new_case nows
  printf 'no-workspace\n' >"$bin/login.mode"
  run_check nows -- --sandbox "$case_dir/sandbox"
  expect_status 2
  expect_row "$last_out" address FAIL
  expect_contains "$last_out" "fugaro update --firebase-project"
  expect_contains "$last_err" "fugaro update --firebase-project"
  assert_scratch_removed
  assert_no_leak
}

case_wrong_state_accepted() {
  new_case wsacc
  printf 'accept-wrong-state\n' >"$bin/login.mode"
  run_check wsacc -- --sandbox "$case_dir/sandbox"
  expect_status 1
  expect_row "$last_out" wrong-state FAIL
  expect_contains "$last_out" "rather than 400"
  expect_absent "$last_out" "CHECK 562 RESULT END (exit 0)"
  assert_scratch_removed
  assert_no_leak
}

case_ended_by_wrong_state() {
  new_case wsends
  printf 'ends-on-wrong-state\n' >"$bin/login.mode"
  run_check wsends -- --sandbox "$case_dir/sandbox"
  expect_status 1
  expect_row "$last_out" wrong-state FAIL
  expect_contains "$last_out" "wrong-state callback ended the flow"
  expect_absent "$last_out" "CHECK 562 RESULT END (exit 0)"
  assert_scratch_removed
  assert_no_leak
}

case_never_finished() {
  new_case never
  printf 'never-finishes\n' >"$bin/login.mode"
  run_check never -- --sandbox "$case_dir/sandbox" --wait 3
  expect_status 2
  expect_row "$last_out" wrong-state PASS
  expect_row "$last_out" signin FAIL
  expect_contains "$last_out" "not finished within 3s"
  assert_scratch_removed
  assert_no_leak
}

case_key_on_stdout() {
  new_case keystdout
  printf 'key-on-stdout\n' >"$bin/login.mode"
  run_check keystdout -- --sandbox "$case_dir/sandbox" --wait 30
  expect_status 1
  expect_row "$last_out" signin FAIL
  expect_contains "$last_out" "carries a key-shaped string"
  expect_absent "$last_out" "fgk_fake562keyvalue"
  expect_absent "$last_out" "CHECK 562 RESULT END (exit 0)"
  assert_scratch_removed
  assert_no_leak
}

case_key_on_stderr() {
  new_case keystderr
  printf 'key-on-stderr\n' >"$bin/login.mode"
  run_check keystderr -- --sandbox "$case_dir/sandbox" --wait 30
  expect_status 1
  expect_row "$last_out" signin FAIL
  expect_contains "$last_out" "carries a key-shaped string"
  expect_absent "$last_out" "fgk_fake562keyvalue"
  expect_absent "$last_out" "CHECK 562 RESULT END (exit 0)"
  assert_scratch_removed
  assert_no_leak
}

case_prod_refused() {
  new_case prod
  run_check prod -- --sandbox "$case_dir/sandbox" --project fugaro-prod
  expect_status 1
  expect_row "$last_out" project FAIL
  expect_contains "$last_err" "refused unless --production"
  no_calls
  assert_scratch_removed
}

case_belong_refused() {
  new_case belong
  run_check belong -- --sandbox "$case_dir/sandbox" --site https://belong.example
  expect_status 1
  expect_contains "$last_err" "names belong"
  no_calls
  assert_scratch_removed
}

case_agent_session() {
  new_case agent
  run_check agent CLAUDECODE=1 -- --sandbox "$case_dir/sandbox"
  expect_status 1
  expect_row "$last_out" agent-session FAIL
  expect_contains "$last_err" "coding agent's session"
  no_calls
  assert_scratch_removed
}

case_xtrace() {
  new_case xtrace
  BASHX=1 run_check xtrace -- --sandbox "$case_dir/sandbox" --dry-run
  BASHX=""
  expect_status 1
  expect_row "$last_out" xtrace FAIL
  expect_contains "$last_err" "tracing"
  no_calls
}

case_unknown_arg() {
  new_case unknown
  run_check unknown -- --sandbox "$case_dir/sandbox" --bogus
  expect_status 2
  expect_contains "$last_err" "unknown argument"
  expect_absent "$last_out" "CHECK 562 RESULT"
  no_calls
}

# --------------------------------------------------------------------- driver
printf 'check562_test.sh: running\n'
t help
t dry_run
t happy
t site_not_licence
t no_workspace
t wrong_state_accepted
t ended_by_wrong_state
t never_finished
t key_on_stdout
t key_on_stderr
t prod_refused
t belong_refused
t agent_session
t xtrace
t unknown_arg

printf '\n%d case(s) run\n' "$ran"
if [ "$failures" -ne 0 ]; then
  printf 'check562_test.sh: %d failure(s)\n' "$failures"
  exit 1
fi
printf 'check562_test.sh: ok\n'
exit 0
