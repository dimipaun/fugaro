#!/usr/bin/env bash
# Tests for scripts/live-checks/check90.sh, with a fake `claude` and a fake
# upstream on loopback: nothing here reaches the network, no real credential
# exists, and the token the test feeds in is a sentinel that is asserted absent
# from everything the run prints or leaves behind.
#
# Usage: bash scripts/live-checks/check90_test.sh   (also run by scripts/live_checks_test.go)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check90.sh
work=$(mktemp -d)
pids=()
cleanup_all() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  if [ -n "${CHECK90_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 400)" ;;
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

# The sentinel is deliberately not a credential shape: nothing here is a real
# token, and a test asserts this string never survives the run.
SENTINEL='FAKE-TOKEN-FOR-CHECK-90-TESTS-NOT-A-CREDENTIAL-0123456789'

# --------------------------------------------------------------------- fakes
write_fake_claude() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/claude" <<'CLAUDE_EOF'
#!/usr/bin/env python3
"""A stand-in for `claude`, for scripts/live-checks/check90.sh's test.

It never touches the network: the direct control's https://api.anthropic.com
base is answered from here, and every other base is a loopback proxy this run
started. It records which credential it was given only as a KIND -- sentinel,
placeholder or empty -- never a value.
"""
import http.client, json, os, sys, time

mode = os.environ.get("FAKE_CLAUDE_MODE", "ok")
args = sys.argv[1:]
if "--version" in args:              # the version probe is not a call
    print("fake-claude 1.0 (check90_test.sh)")
    sys.exit(0)
prompt = ""
for i, a in enumerate(args):
    if a == "-p" and i + 1 < len(args):
        prompt = args[i + 1]

cred = os.environ.get("CLAUDE_CODE_OAUTH_TOKEN", "")
sentinel = os.environ["FAKE_CLAUDE_SENTINEL"]
kind = "sentinel" if cred == sentinel else ("placeholder" if cred else "empty")
if "--version" not in args:
    with open(os.environ["FAKE_CLAUDE_CALLS"], "a") as fh:
        fh.write("%s %s %s\n" % (os.environ.get("ANTHROPIC_BASE_URL", ""), kind, prompt))

if mode == "fail":
    sys.exit("fake claude: failing on purpose")
if mode == "hang":
    time.sleep(float(os.environ.get("FAKE_CLAUDE_HANG", "30")))
    sys.exit(0)
if os.environ.get("FAKE_CLAUDE_FAIL_ON", "") == kind:
    sys.exit("fake claude: the credential this call was given is not one it can use")

base = os.environ.get("ANTHROPIC_BASE_URL", "")
if not base.startswith("http://127.0.0.1:"):
    print("ok")                     # the control: no proxy, and no network
    sys.exit(0)

host, _, port = base[len("http://"):].partition(":")
body = json.dumps({"model": "fake", "messages": [{"role": "user", "content": prompt}]})
conn = http.client.HTTPConnection(host, int(port), timeout=120)
conn.request("POST", "/v1/messages", body, {
    "Authorization": "Bearer " + cred,
    "anthropic-beta": "fake-beta-2026-01-01",
    "user-agent": "fake-claude/1.0",
    "x-fake-credential-kind": kind,
    "content-type": "application/json",
})
resp = conn.getresponse()
resp.read()
if resp.status != 200:
    sys.stderr.write("fake claude: HTTP %d\n" % resp.status)
    sys.exit(1)
if prompt.startswith("Write the numbers"):
    for i in (1, 2, 3):
        print(i, flush=True)
        time.sleep(1)
else:
    print("ok")
CLAUDE_EOF
  chmod +x "$1/bin/claude"
}

write_fake_upstream() { # dir
  cat >"$1/upstream.py" <<'UPSTREAM_EOF'
#!/usr/bin/env python3
"""A fake api.anthropic.com on loopback, for check90.sh's test only."""
import http.server, json, os, sys

TOKEN = os.environ["FAKE_UPSTREAM_TOKEN"]
MODE = os.environ.get("FAKE_UPSTREAM_MODE", "ok")
REQUIRE = [h for h in os.environ.get("FAKE_UPSTREAM_REQUIRE_HEADERS", "").split() if h]
PORT = int(os.environ["FAKE_UPSTREAM_PORT"])


def error(handler, status, etype, message, extra=None):
    body = json.dumps({"error": {"type": etype, "message": message}}).encode()
    handler.send_response(status)
    handler.send_header("Content-Type", "application/json")
    handler.send_header("Content-Length", str(len(body)))
    for k, v in (extra or {}).items():
        handler.send_header(k, v)
    handler.end_headers()
    handler.wfile.write(body)


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        self.rfile.read(n)
        auth = self.headers.get("Authorization") or ""
        kind = self.headers.get("x-fake-credential-kind") or ""
        if MODE == "reject-all" or auth != "Bearer " + TOKEN:
            return error(self, 401, "authentication_error", "fake upstream: bad credential")
        if MODE == "limit" and kind == "placeholder":
            return error(self, 429, "rate_limit_error",
                         "fake upstream: usage limit", {"retry-after": "60"})
        missing = [h for h in REQUIRE if not self.headers.get(h)]
        if missing:
            return error(self, 400, "invalid_request_error",
                         "fake upstream: missing " + ",".join(missing))
        body = b'{"ok": true}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


srv = http.server.ThreadingHTTPServer(("127.0.0.1", PORT), H)
print("fake upstream listening on %d" % PORT, flush=True)
srv.serve_forever()
UPSTREAM_EOF
}

free_port() {
  python3 - <<'PORT_EOF'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PORT_EOF
}

start_upstream() { # dir mode [require headers]
  local dir=$1 mode=${2:-ok} require=${3:-}
  UPSTREAM_PORT=$(free_port)
  FAKE_UPSTREAM_TOKEN=$SENTINEL FAKE_UPSTREAM_MODE=$mode \
    FAKE_UPSTREAM_REQUIRE_HEADERS="$require" FAKE_UPSTREAM_PORT=$UPSTREAM_PORT \
    python3 "$dir/upstream.py" >"$dir/upstream.log" 2>&1 &
  pids+=("$!")
  local i=0
  while [ "$i" -lt 50 ]; do
    grep -q 'listening on' "$dir/upstream.log" 2>/dev/null && break
    sleep 0.1
    i=$((i + 1))
  done
  grep -q 'listening on' "$dir/upstream.log" || {
    fail "the fake upstream did not start: $(cat "$dir/upstream.log")"
    return 1
  }
  FAKE_UPSTREAM_ADDR="127.0.0.1:$UPSTREAM_PORT"
}

# ---------------------------------------------------------------- running it
# run90 DIR MODE [require] [extra env...]; stdin is the token. Sets $status,
# $out, $err, $scratch.
run90() {
  local dir=$work/$1 mode=$2 require=$3
  shift 3
  mkdir -p "$dir/home" "$dir/tmp" "$dir/logs" "$dir/managed"
  write_fake_claude "$dir"
  write_fake_upstream "$dir"
  start_upstream "$dir" "$mode" "$require" || return 1
  : >"$dir/claude.calls"
  status=0
  printf '%s\n' "$SENTINEL" | env \
    -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    CHECK90_UPSTREAM="$FAKE_UPSTREAM_ADDR" \
    CHECK90_DELAY=1 CHECK90_CALL_TIMEOUT=20 CHECK90_TOTAL_SECONDS=180 \
    CHECK90_KEEP_LOGS="$dir/logs" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# --------------------------------------------------------------- the tests
test_happy_path() {
  run90 happy ok "anthropic-beta"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "CHECK 90 RESULT" "happy path: the block"
  expect_contains "$out" "FACT control: PASS" "happy path: the control"
  expect_contains "$out" "FACT observed-control: PASS" "happy path: the observed control"
  expect_contains "$out" "FACT swap: PASS" "happy path: the swap"
  expect_contains "$out" "FACT stream: PASS" "happy path: the stream"
  expect_contains "$out" "FACT header-drop-anthropic-beta: FAIL" "happy path: a required header"
  expect_contains "$out" "FACT header-drop-user-agent: PASS" "happy path: an optional header"
  expect_contains "$out" "FACT headers-required: PASS — anthropic-beta" "happy path: the C1.7 list"
  expect_contains "$out" "FACT timeout-retry: PASS" "happy path: the timeout record"
  expect_contains "$out" "FACT tier-a: PASS — VIABLE" "happy path: tier A"
  expect_contains "$out" "FACT restore: PASS" "happy path: the Restore"
  expect_contains "$out" "CHECK 90 RESULT END (exit 0)" "happy path: the end line"

  # The swap call is the one the proxy supplies the credential for: the fake
  # claude records what it was given, and the swap must have been given the
  # placeholder, not the token.
  expect_contains "$(cat "$work/happy/claude.calls")" "127.0.0.1" "happy path: a proxied call"
  expect_absent "$(cat "$work/happy/claude.calls")" "$SENTINEL" "happy path: the calls record"

  # Nothing that survives carries the token, and the logs carry the header
  # NAMES the register wants, not values.
  assert_no_sentinel "$work/happy" "happy path"
  expect_contains "$(cat "$work/happy/logs/"*.log)" "credential-header authorization" \
    "happy path: the header name is recorded"

  # Cleanup on success.
  [ -n "$scratch" ] || fail "happy path: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory $scratch survived the run"; fi
  return 0
}

test_block_format() {
  run90 format ok ""
  local first last line n=0
  first=$(printf '%s\n' "$out" | sed -n '/^CHECK 90 RESULT$/=' | head -n 1)
  last=$(printf '%s\n' "$out" | sed -n '/^CHECK 90 RESULT END (exit 0)$/=' | head -n 1)
  [ -n "$first" ] || fail "format: no block header"
  [ -n "$last" ] || fail "format: no end line"
  while IFS= read -r line; do
    case "$line" in
    "CHECK 90 RESULT" | "CHECK 90 RESULT END"*) ;;
    "FACT "*)
      n=$((n + 1))
      if ! printf '%s' "$line" | grep -qE '^FACT [a-z0-9-]+: (PASS|FAIL|INFO) — .+$'; then
        fail "format: this line is not a FACT line: $line"
      fi
      ;;
    "") ;;
    *) fail "format: a line outside the block's grammar: $line" ;;
    esac
  done < <(printf '%s\n' "$out" | sed -n '/^CHECK 90 RESULT$/,/^CHECK 90 RESULT END/p')
  [ "$n" -ge 20 ] || fail "format: only $n FACT lines, the shape changed"
  # The keys the register and Task C1.7 name must all be there, whatever the run did.
  local key
  for key in claude-version env-cleared managed-settings-dir settings-routing \
    token-file token-validity control observed-control swap swap-credential-header \
    stream headers-observed headers-required headers-optional usage-limit \
    timeout-retry restore tier-a; do
    printf '%s' "$out" | grep -q "^FACT $key: " || fail "format: the block has no '$key' line"
  done
}

test_env_guards() {
  local v
  for v in ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL \
    CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_BEDROCK; do
    local dir=$work/env-$v
    mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
    write_fake_claude "$dir"
    : >"$dir/claude.calls"
    local st=0
    printf '%s\n' "$SENTINEL" | env \
      -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
      -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
      PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
      "$v=some-value-that-must-not-be-printed" \
      FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
      bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
    expect_status "$st" 1 "guard $v"
    expect_contains "$(cat "$dir/err")" "$v" "guard $v: the message names it"
    expect_absent "$(cat "$dir/err")" "some-value-that-must-not-be-printed" "guard $v: no value"
    expect_contains "$(cat "$dir/out")" "FACT env-cleared: FAIL" "guard $v: the fact"
    if [ -s "$dir/claude.calls" ]; then fail "guard $v: claude was run anyway"; fi
    local sc
    sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
    if [ -d "$sc" ]; then fail "guard $v: the scratch directory survived the stop"; fi
  done
}

test_settings_guard() {
  local case_name body
  while IFS='|' read -r case_name body; do
    [ -n "$case_name" ] || continue
    local dir=$work/settings-$case_name
    mkdir -p "$dir/home" "$dir/tmp" "$dir/managed" "$dir/cwd/.claude"
    write_fake_claude "$dir"
    : >"$dir/claude.calls"
    printf '%s' "$body" >"$dir/cwd/.claude/settings.json"
    local st=0
    (cd "$dir/cwd" && printf '%s\n' "$SENTINEL" | env \
      -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
      -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
      PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
      FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
      bash "$script" >"$dir/out" 2>"$dir/err") || st=$?
    expect_status "$st" 1 "settings guard $case_name"
    expect_contains "$(cat "$dir/out")" "FACT settings-routing: FAIL" "settings guard $case_name: the fact"
    expect_contains "$(cat "$dir/err")" "settings.json" "settings guard $case_name: names the file"
    if [ -s "$dir/claude.calls" ]; then fail "settings guard $case_name: claude was run anyway"; fi
    local sc
    sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
    if [ -d "$sc" ]; then fail "settings guard $case_name: the scratch directory survived the stop"; fi
  done <<'CASES'
apikeyhelper|{"apiKeyHelper":"/usr/bin/true"}
env-baseurl|{"env":{"ANTHROPIC_BASE_URL":"https://example.invalid"}}
vertex-switch|{"env":{"CLAUDE_CODE_USE_VERTEX":"1"}}
unreadable|this is not json
CASES

  # ... and the managed settings directory is read too: a routing key in a file
  # there stops the check just as a repository's .claude file does.
  local dir=$work/settings-managed
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed" "$dir/cwd"
  write_fake_claude "$dir"
  : >"$dir/claude.calls"
  printf '%s' '{"env":{"ANTHROPIC_BASE_URL":"https://example.invalid"}}' >"$dir/managed/managed-settings.json"
  local st=0
  (cd "$dir/cwd" && printf '%s\n' "$SENTINEL" | env \
    -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err") || st=$?
  expect_status "$st" 1 "settings guard managed"
  expect_contains "$(cat "$dir/out")" "FACT settings-routing: FAIL" "settings guard managed: the fact"
  expect_contains "$(cat "$dir/err")" "managed-settings.json" "settings guard managed: names the file"
  if [ -s "$dir/claude.calls" ]; then fail "settings guard managed: claude was run anyway"; fi
}

test_swap_fails() {
  # The client cannot use the credential the swap path gives it: the swap call
  # fails while the control and the observed control succeeded.
  run90 swapfail ok "" FAKE_CLAUDE_FAIL_ON=placeholder
  expect_status "$status" 1 "swap fails"
  expect_contains "$out" "FACT swap: FAIL" "swap fails: the fact"
  expect_contains "$out" "FACT tier-a: FAIL — NOT VIABLE" "swap fails: tier A"
  expect_contains "$out" "Global Constraint 4" "swap fails: the consequence is named"
  expect_contains "$out" "CHECK 90 RESULT END (exit 1)" "swap fails: the end line"
  expect_contains "$err" "the remaining steps are skipped" "swap fails: no further prompts"
  [ -d "$(sed -n 's/^check90: scratch directory: \([^ ]*\) .*/\1/p' "$work/swapfail/err" | head -n 1)" ] \
    && fail "swap fails: the scratch directory survived"
  return 0
}

test_limit_is_recorded() {
  run90 limit limit ""
  expect_status "$status" 1 "usage limit"
  expect_contains "$out" "FACT usage-limit: PASS" "usage limit: the fact"
  expect_contains "$out" "rate_limit_error" "usage limit: the error type"
  expect_contains "$out" "retry-after" "usage limit: the reset header"
  expect_contains "$out" "FACT tier-a: FAIL" "usage limit: tier A"
  assert_no_sentinel "$work/limit" "usage limit"
  return 0
}

test_control_failure_is_inconclusive() {
  run90 control-fail reject-all ""
  expect_status "$status" 2 "control failure"
  expect_contains "$out" "FACT observed-control: FAIL" "control failure: the fact"
  expect_contains "$out" "FACT tier-a: INFO — INCONCLUSIVE" "control failure: tier A is not judged"
  expect_absent "$out" "NOT VIABLE" "control failure: nothing was concluded about the swap"
  [ -d "$(sed -n 's/^check90: scratch directory: \([^ ]*\) .*/\1/p' "$work/control-fail/err" | head -n 1)" ] \
    && fail "control failure: the scratch directory survived"
  return 0
}

test_dry_run_makes_no_call() {
  local dir=$work/dry
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
  write_fake_claude "$dir"
  : >"$dir/claude.calls"
  local st=0
  printf '%s\n' "$SENTINEL" | env \
    -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" CHECK90_DRY_RUN=1 \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 0 "dry run"
  if [ -s "$dir/claude.calls" ]; then fail "dry run: claude was run anyway"; fi
  expect_contains "$(cat "$dir/out")" "FACT dry-run: PASS" "dry run: the fact"
}

test_sigint_cleans_up() {
  local dir=$work/sigint
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
  write_fake_claude "$dir"
  write_fake_upstream "$dir"
  : >"$dir/claude.calls"
  start_upstream "$dir" ok "" || return 1
  local st=0
  printf '%s\n' "$SENTINEL" >"$dir/tokenin"
  # Job control: a background job of a NON-interactive shell starts with SIGINT
  # ignored, and bash cannot trap a signal that was ignored when it started --
  # without this, the script would never see the Ctrl-C this test sends.
  set -m
  env \
    -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    CHECK90_UPSTREAM="$FAKE_UPSTREAM_ADDR" \
    CHECK90_CALL_TIMEOUT=60 CHECK90_TOTAL_SECONDS=180 FAKE_CLAUDE_MODE=hang \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" <"$dir/tokenin" >"$dir/out" 2>"$dir/err" &
  local pid=$!
  local i=0
  while [ "$i" -lt 60 ]; do
    grep -q 'step 1/7' "$dir/err" 2>/dev/null && break
    sleep 0.2
    i=$((i + 1))
  done
  kill -INT "$pid" 2>/dev/null || true
  local j=0
  while kill -0 "$pid" 2>/dev/null && [ "$j" -lt 150 ]; do
    sleep 0.2
    j=$((j + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null || true
    fail "sigint: the script did not end after SIGINT"
  fi
  wait "$pid" || st=$?
  set +m
  expect_status "$st" 130 "sigint"
  expect_contains "$(cat "$dir/err")" "interrupted" "sigint: the message"
  expect_contains "$(cat "$dir/out")" "CHECK 90 RESULT END (exit 130)" "sigint: the block"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  [ -n "$sc" ] || fail "sigint: the scratch directory was never announced"
  if [ -d "$sc" ]; then fail "sigint: the scratch directory $sc survived"; fi
  return 0
}

assert_no_sentinel() { # dir what — no file under dir, and nothing printed, carries the token
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null)" "$SENTINEL" "$what: the output"
  local hits
  hits=$(grep -rl -- "$SENTINEL" "$dir" 2>/dev/null || true)
  [ -z "$hits" ] || fail "$what: the token is in $hits"
}

test_happy_path
test_block_format
test_env_guards
test_settings_guard
test_swap_fails
test_limit_is_recorded
test_control_failure_is_inconclusive
test_dry_run_makes_no_call
test_sigint_cleans_up

if [ "$failures" -ne 0 ]; then
  echo "check90_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check90_test.sh: ok"
