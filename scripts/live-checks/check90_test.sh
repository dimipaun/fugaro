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
# The PHYSICAL path: on macOS the temp directory is under /var, a symlink to
# /private/var, and a child's getcwd() reports the resolved path, so a test that
# compares a child's cwd with the scratch directory the script announces needs
# both spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
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

# Every variable the script's environment guard refuses, unset before each run:
# this machine may have one of them set (a runner usually has NO_PROXY), and a
# run that stopped at the guard would measure the guard instead of the check.
# The list is the script's own -- a test below sets each one in turn and expects
# the refusal, so the two cannot drift apart silently.
GUARD_VARS=(ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL
  ANTHROPIC_VERTEX_BASE_URL ANTHROPIC_BEDROCK_BASE_URL CLAUDE_CODE_SKIP_VERTEX_AUTH
  CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_BEDROCK CLAUDE_CODE_USE_FOUNDRY
  HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY)
GUARD_UNSET=()
for v in "${GUARD_VARS[@]}"; do GUARD_UNSET+=(-u "$v"); done

# The real tools the fakes below hand over to, resolved before any test puts a
# directory of fakes at the front of PATH.
REAL_ENV=$(command -v env || true)
REAL_PS=$(command -v ps || true)
REAL_CHMOD=$(command -v chmod || true)
REAL_RM=$(command -v rm || true)

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
import http.client, json, os, subprocess, sys, time

mode = os.environ.get("FAKE_CLAUDE_MODE", "ok")
args = sys.argv[1:]

# The flags this client documents, which is what the script may pass it. The
# list is the installed client's own --help, restricted to the options the
# check uses: --tools takes "" to disable every built-in tool and
# --no-session-persistence needs --print, which every call here passes. A flag
# that is NOT here is refused below, so the check cannot come to rely on an
# option the real client does not have.
HELP_FLAGS = {"-p": "Print response and exit (non-interactive)",
              "--settings": "Path to a settings JSON file or a JSON string",
              "--strict-mcp-config": "Only use MCP servers from --mcp-config",
              "--tools": 'List of available tools (use "" to disable all tools)',
              "--no-session-persistence": "Disable session persistence (with --print)",
              "--version": "Output the version number"}
VALUE_FLAGS = {"-p", "--settings", "--tools"}

if "--help" in args:
    print("Usage: claude [options] [command] [prompt]\n")
    print("Options:")
    for flag in sorted(HELP_FLAGS):
        print("  %-28s %s" % (flag, HELP_FLAGS[flag]))
    sys.exit(0)

unknown = []
i = 0
while i < len(args):
    a = args[i]
    if a in HELP_FLAGS:
        i += 2 if a in VALUE_FLAGS else 1
        continue
    if a.startswith("-"):
        unknown.append(a)
    i += 1
if unknown:
    sys.stderr.write("error: unknown option '%s'\n" % unknown[0])
    sys.exit(2)

if "--version" in args:              # the version probe is not a call
    # The probe is recorded so a test can say it was made ONCE, and it can be
    # made to hang so a test can say the probe is capped.
    if os.environ.get("FAKE_CLAUDE_VERSION_LOG"):
        with open(os.environ["FAKE_CLAUDE_VERSION_LOG"], "a") as fh:
            fh.write("version\n")
    if os.environ.get("FAKE_CLAUDE_VERSION_HANG"):
        time.sleep(float(os.environ.get("FAKE_CLAUDE_HANG", "30")))
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

# Two recordings of what a process's ARGV holds while a call is live, for the
# test that the token is never on a command line: this call's own argv, and a
# snapshot of the argv of every process of this run, taken by the fake `ps`.
if os.environ.get("FAKE_CLAUDE_ARGV"):
    with open(os.environ["FAKE_CLAUDE_ARGV"], "a") as fh:
        fh.write(" ".join(sys.argv) + "\n")
if os.environ.get("FAKE_CLAUDE_PS"):
    snap = subprocess.run(["ps", os.environ.get("FAKE_PS_PATTERN", "claude")],
                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    with open(os.environ["FAKE_CLAUDE_PS"], "ab") as fh:
        fh.write(snap.stdout)

# What this child was started WITH: its working directory, its argv, and the
# names -- never the values -- of the environment variables it can see. That is
# what a test needs to say the credential child runs in the scratch directory,
# with hooks off and MCP servers refused, and with no routing variable in its
# environment (the review's M1).
if os.environ.get("FAKE_CLAUDE_CONTEXT"):
    # ANTHROPIC_LOG and DEBUG are watched with the routing names: neither routes
    # the call, but both change what the client prints or writes, and the child
    # that carries the token must not be started with either.
    watched = ("CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
               "ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL",
               "ANTHROPIC_BEDROCK_BASE_URL", "CLAUDE_CODE_USE_VERTEX",
               "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY", "HTTP_PROXY",
               "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "ANTHROPIC_LOG", "DEBUG",
               "CHK90_CRED_SOURCE", "CHK90_CWD")
    seen = ",".join(sorted(k for k in os.environ if k in watched))
    # The tools value is recorded separately, and as a repr: `--tools ""` and a
    # missing --tools both leave the argv joined by spaces looking the same, and
    # the difference is the whole point of the flag.
    ti = args.index("--tools") if "--tools" in args else -1
    tools = repr(args[ti + 1]) if 0 <= ti < len(args) - 1 else "(none)"
    with open(os.environ["FAKE_CLAUDE_CONTEXT"], "a") as fh:
        fh.write("cwd=%s kind=%s env=%s tools=%s argv=%s\n"
                 % (os.getcwd(), kind, seen, tools, " ".join(args)))

# A client that stalls on ONE prompt and answers every other: the streaming
# call is the only one this test can make the cap cut off without losing the
# steps before it.
if os.environ.get("FAKE_CLAUDE_HANG_PROMPT", "") and prompt.startswith(os.environ["FAKE_CLAUDE_HANG_PROMPT"]):
    time.sleep(float(os.environ.get("FAKE_CLAUDE_HANG", "300")))
    sys.exit(0)

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

# A client that answers WITHOUT ever contacting the proxy, and still exits 0:
# the swap call "succeeds" while the proxy saw no request at all. Nothing was
# swapped, so there is no swap to judge -- and this is the shape the review's M3
# was about, where the run called it VIABLE.
if mode == "no-proxy" and kind == "placeholder":
    print("ok")
    sys.exit(0)

host, _, port = base[len("http://"):].partition(":")
body = json.dumps({"model": "fake", "messages": [{"role": "user", "content": prompt}]})
# A credential that is not this run's placeholder, sent on a swapped call: the
# shape of a request from something else on this machine (a page the browser
# loaded). The proxy must refuse it rather than attach the real token to it
# (the review's m1).
auth = "Bearer " + cred
if mode == "foreign" and kind == "placeholder":
    auth = "Bearer foreign-not-this-run"
conn = http.client.HTTPConnection(host, int(port), timeout=120)
conn.request("POST", "/v1/messages", body, {
    "Authorization": auth,
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

# write_fake_env DIR — a stand-in for `env` that writes down the argv it was
# given and then runs the real one. This is the recording that catches a
# credential handed over as an argument: `env VAR=value` puts the value in
# env's OWN argv, and env execs the client, so that argv exists for
# microseconds -- far too short for a snapshot of the process table to be
# relied on, and no reason not to be sure.
write_fake_env() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/env" <<ENV_EOF
#!/bin/sh
# fake env: record the argv, then hand over to the real env.
printf '%s\n' "\$@" >>"$1/env.argv"
exec "$REAL_ENV" "\$@"
ENV_EOF
  chmod +x "$1/bin/env"
}

# write_fake_ps DIR — a stand-in for `ps` that snapshots the argv of the
# processes of THIS run (the pattern in $1 names them), so an unrelated process
# elsewhere on the machine cannot make the test flaky.
write_fake_ps() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/ps" <<PS_EOF
#!/bin/sh
# fake ps: \$1 is a grep -E pattern naming this run's processes.
"$REAL_PS" -A -ww -o pid,ppid,args 2>/dev/null | grep -Ei "\$1" || true
PS_EOF
  chmod +x "$1/bin/ps"
}

# write_fake_chmod DIR — a `chmod` that fails on the token file and hands every
# other call to the real one. It plants ONE internal error, on the token path,
# to say what such a failure has to look like: an ABORTED fact and "could not
# tell", never a verdict on the swap (the review's M3).
write_fake_chmod() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/chmod" <<CHMOD_EOF
#!/bin/sh
case "\$*" in
*token*) echo "fake chmod: failing on purpose" >&2; exit 1 ;;
esac
exec "$REAL_CHMOD" "\$@"
CHMOD_EOF
  chmod +x "$1/bin/chmod"
}

# write_fake_rm DIR — an `rm` that takes its time and says when it started, so a
# test can send a signal while the CLEANUP is running. It hands every removal to
# the real `rm`; only the moment is the fake's.
write_fake_rm() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/rm" <<RM_EOF
#!/usr/bin/env bash
# A stand-in for \`rm\`, for scripts/live-checks/check90_test.sh only.
set -euo pipefail
if [ -n "\${FAKE_RM_MARK:-}" ]; then printf 'rm\n' >>"\$FAKE_RM_MARK"; fi
sleep "\${FAKE_RM_SLEEP:-2}"
exec "$REAL_RM" "\$@"
RM_EOF
  chmod +x "$1/bin/rm"
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
  write_fake_env "$dir"
  write_fake_ps "$dir"
  write_fake_upstream "$dir"
  start_upstream "$dir" "$mode" "$require" || return 1
  : >"$dir/claude.calls"
  status=0
  # RUN90_STDIN is what the run reads from its input; a test that wants to see
  # what happens to a SECOND line on that input sets it (the review's B1).
  printf '%s\n' "${RUN90_STDIN:-$SENTINEL}" | env \
    ${GUARD_UNSET[@]} \
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

# Every name in GUARD_VARS is set in turn, and the run must refuse each one:
# the guard has to cover the variables a settings file's keys route (the
# review's m2) exactly as it covers CLAUDE_CODE_USE_VERTEX.
test_env_guards() {
  local v
  for v in "${GUARD_VARS[@]}"; do
    local dir=$work/env-$v
    mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
    write_fake_claude "$dir"
    : >"$dir/claude.calls"
    local st=0
    printf '%s\n' "$SENTINEL" | env \
      ${GUARD_UNSET[@]} \
      PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
      "$v=some-value-that-must-not-be-printed" \
      FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
      bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
    expect_status "$st" 2 "guard $v"
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
      ${GUARD_UNSET[@]} \
      PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
      CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
      FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
      bash "$script" >"$dir/out" 2>"$dir/err") || st=$?
    expect_status "$st" 2 "settings guard $case_name"
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
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err") || st=$?
  expect_status "$st" 2 "settings guard managed"
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
    ${GUARD_UNSET[@]} \
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
    ${GUARD_UNSET[@]} \
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

# The token is never on a command line. The calls run for real, and three
# recordings are taken while they are live:
#   env.argv      the argv of every `env` the script runs (see write_fake_env:
#                 this is the one that would carry a credential passed as an
#                 argument, and the one no process snapshot can be relied on to
#                 catch, because env execs the client microseconds later)
#   claude.argv   the argv the client itself was run with
#   ps.snapshot   the argv of every process of the run, as the client saw it
# The same run also has to show the token DID reach the client -- in the
# environment of the one child that needs it, and nowhere else.
test_token_is_never_on_a_command_line() {
  run90 argv ok "" \
    FAKE_CLAUDE_ARGV="$work/argv/claude.argv" \
    FAKE_CLAUDE_PS="$work/argv/ps.snapshot" \
    FAKE_PS_PATTERN='check90|chk90|claude'
  expect_status "$status" 0 "argv: the run"
  expect_contains "$(cat "$work/argv/claude.calls")" "sentinel" \
    "argv: the control was given the token (in its environment, which is where it belongs)"
  local what
  for what in claude.argv ps.snapshot env.argv; do
    [ -s "$work/argv/$what" ] || fail "argv: nothing was recorded in $what, so it proves nothing"
    expect_absent "$(cat "$work/argv/$what" 2>/dev/null)" "$SENTINEL" "argv: $what"
  done
  # The recording that would carry it is the env one: it must have seen the
  # calls, so that its silence about the token means something.
  expect_contains "$(cat "$work/argv/env.argv")" "check90-claude" "argv: env.argv saw the calls"
  expect_contains "$(cat "$work/argv/env.argv")" "CHK90_CRED_SOURCE" "argv: env.argv saw where the credential comes from"
  # And nothing the run left behind carries it either.
  assert_no_sentinel "$work/argv" "argv"
  return 0
}

# The streaming call is under the same cap as every other call, and the FAIL
# line says the cap is what ended it. It is also the one scenario where the
# call is cut off before it prints a single line, so the arrival-time reading
# below it sees an empty file: the run must still reach the block -- the last
# three steps' facts are worthless if it does not.
test_streaming_call_is_capped() {
  local started elapsed
  started=$(date +%s)
  run90 streamcap ok "" \
    FAKE_CLAUDE_HANG_PROMPT="Write the numbers" FAKE_CLAUDE_HANG=60 \
    CHECK90_CALL_TIMEOUT=5
  elapsed=$(($(date +%s) - started))
  expect_status "$status" 0 "stream cap"
  expect_contains "$out" "FACT stream: FAIL — did not complete: this check's own 5s cap cut the call off" \
    "stream cap: the fact names the cap"
  expect_contains "$out" "CHECK 90 RESULT END (exit 0)" "stream cap: the run reached the block"
  # The steps after it ran, so the cap ended the call and not the run.
  expect_contains "$out" "FACT headers-required: PASS" "stream cap: the header steps ran"
  expect_contains "$out" "FACT timeout-retry: PASS" "stream cap: the delayed call ran"
  [ "$elapsed" -lt 60 ] || fail "stream cap: the run took ${elapsed}s, so the call was not cut off at the cap"
  # The cap killed the whole call -- the pipeline's client too, not just the
  # shell in front of it -- so nothing of this run is left sleeping behind it.
  if command -v pgrep >/dev/null 2>&1 && pgrep -f "$work/streamcap/bin/claude" >/dev/null 2>&1; then
    fail "stream cap: the client outlived the cap"
  fi
  return 0
}

# The script wipes the token file explicitly and then removes the whole 0700
# scratch directory, so "the token file was left behind" is only observable
# together with "the scratch directory survived" -- and that pair is what the
# tests below catch: the token file lives inside the scratch directory, and
# every scenario asserts the scratch directory is gone.
assert_no_sentinel() { # dir what — no file under dir, and nothing printed, carries the token
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null)" "$SENTINEL" "$what: the output"
  local hits
  hits=$(grep -rl -- "$SENTINEL" "$dir" 2>/dev/null || true)
  [ -z "$hits" ] || fail "$what: the token is in $hits"
}

# B1: the check asks for the token and NOTHING else. A second line on the same
# input -- what pasting `claude setup-token`'s whole output leaves behind -- must
# never be read, printed or stored. That line sits next to the token, so a prompt
# for it is a prompt that carries the token into the RESULT block, into the
# terminal's history and into whatever the block is pasted to.
CANARY='CANARY-SETUP-TOKEN-LINE-VALID-FOR-ONE-YEAR-0123456789'
test_no_second_paste() {
  RUN90_STDIN="$SENTINEL
$CANARY"
  run90 nopaste ok ""
  RUN90_STDIN=""
  expect_status "$status" 0 "no second paste: the run"
  expect_contains "$out" "CHECK 90 RESULT END (exit 0)" "no second paste: the run reached the block"
  expect_contains "$out" "FACT token-validity: PASS" \
    "no second paste: validity is derived from the control call, not asked for"
  expect_absent "$out$err" "$CANARY" "no second paste: the text that was left on the input"
  local hits
  hits=$(grep -rl -- "$CANARY" "$work/nopaste" 2>/dev/null || true)
  [ -z "$hits" ] || fail "no second paste: the text left on the input is in $hits"
  assert_no_sentinel "$work/nopaste" "no second paste"
  return 0
}

# M2: `bash -x` prints every command as it runs, and the commands around the
# token carry its value -- that was the review's proof. The check refuses to run
# in a tracing shell rather than trusting a `set +x` to hold for its children
# too, and the trace that IS there (before the refusal) must carry no token.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
  write_fake_claude "$dir"
  : >"$dir/claude.calls"
  local st=0
  printf '%s\n' "$SENTINEL" | env \
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash -x "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 2 "xtrace"
  expect_contains "$(cat "$dir/err")" "+ " "xtrace: the trace really was on"
  expect_contains "$(cat "$dir/err")" "tracing" "xtrace: the message names it"
  expect_contains "$(cat "$dir/out")" "FACT xtrace: FAIL" "xtrace: the fact"
  expect_absent "$(cat "$dir/out")" "CHECK 90 RESULT END (exit 0)" "xtrace: the run did not continue"
  if [ -s "$dir/claude.calls" ]; then fail "xtrace: claude was run anyway"; fi
  assert_no_sentinel "$dir" "xtrace"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -d "$sc" ]; then fail "xtrace: the scratch directory survived the stop"; fi
  return 0
}

# need_tools is the FIRST thing main does, and the ordering is the point:
# free_port and random_placeholder are python3 programs, so `need python3` is
# the only place a missing interpreter is named. Checked after them, the run
# died inside free_port's command substitution -- "an internal error ended the
# run (line N)", which names a line and not the tool, and reads as a defect in
# the check rather than as the one thing the owner has to install. The PATH
# here has bash, mktemp and claude and no python3 at all.
test_missing_python3_is_named_not_an_internal_error() {
  local dir=$work/notools
  mkdir -p "$dir/bin" "$dir/home" "$dir/tmp"
  write_fake_claude "$dir"
  : >"$dir/claude.calls"
  ln -sf "$(command -v bash)" "$dir/bin/bash"
  ln -sf "$(command -v mktemp)" "$dir/bin/mktemp"
  local st=0
  printf '%s\n' "$SENTINEL" | env \
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 2 "no python3: could not tell"
  expect_contains "$(cat "$dir/err")" "needs 'python3' on PATH" "no python3: the missing tool is named"
  expect_absent "$(cat "$dir/err")" "internal error" "no python3: the run reported an internal error instead of naming the missing tool"
  if [ -s "$dir/claude.calls" ]; then fail "no python3: claude was run anyway"; fi
  # Nothing was set up either: the guard is the first thing that runs, so there
  # is no scratch directory to have made and none to remove.
  expect_absent "$(cat "$dir/err")" "scratch directory:" "no python3: a scratch directory was made before the tools were checked"
  assert_no_sentinel "$dir" "no python3"
  return 0
}

# M1: every settings source Claude Code loads is discovered from its working
# directory, and every hook, plugin hook and MCP server it starts inherits its
# environment -- the CLAUDE_CODE_OAUTH_TOKEN in this one included. So the
# credential child has to run in the scratch directory with hooks off and MCP
# servers refused, and it must be the only child that sees the token.
test_child_is_confined() {
  run90 confined ok "" FAKE_CLAUDE_CONTEXT="$work/confined/context"
  expect_status "$status" 0 "confined: the run"
  local ctx=$work/confined/context
  [ -s "$ctx" ] || {
    fail "confined: no child recorded its context, so this test proves nothing"
    return 0
  }
  [ -n "$scratch" ] || fail "confined: the run never said where its scratch directory is"
  local line n=0
  while IFS= read -r line; do
    n=$((n + 1))
    expect_contains "$line" "cwd=$scratch " "confined: the child's working directory (line $n)"
    expect_contains "$line" '--strict-mcp-config' "confined: no MCP server is started (line $n)"
    expect_contains "$line" '--settings {"disableAllHooks":true}' "confined: no hook is started (line $n)"
    expect_contains "$line" "CLAUDE_CODE_OAUTH_TOKEN" \
      "confined: the token does reach the child (line $n) — without this the rest proves nothing"
    local bad
    for bad in ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN CLAUDE_CODE_USE_VERTEX \
      CLAUDE_CODE_USE_BEDROCK HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY \
      CHK90_CWD CHK90_CRED_SOURCE; do
      expect_absent "$line" "$bad" "confined: $bad in the child's environment (line $n)"
    done
  done <"$ctx"
  [ "$n" -ge 3 ] || fail "confined: only $n child(ren) recorded a context, the run changed shape"
  assert_no_sentinel "$work/confined" "confined"
  return 0
}

# M3: the swap's PASS needs the proxy to have SEEN the swap. A client that
# answers without ever contacting the proxy leaves its log empty, and an empty
# log is not a swap: the run must say it could not tell, never "NOT VIABLE".
test_swap_the_proxy_never_saw() {
  run90 unswapped ok "" FAKE_CLAUDE_MODE=no-proxy
  expect_status "$status" 2 "unswapped: could not tell"
  expect_contains "$out" "FACT swap: INFO" "unswapped: no verdict on the swap"
  expect_absent "$out" "FACT swap: PASS" "unswapped: nothing was swapped, so nothing passed"
  expect_absent "$out" "NOT VIABLE" "unswapped: no verdict either way"
  expect_contains "$out" "FACT tier-a: INFO — INCONCLUSIVE" "unswapped: tier A is not judged"
  expect_contains "$out" "FACT aborted: FAIL" "unswapped: the abort is a fact of the run"
  expect_contains "$out" "CHECK 90 RESULT END (exit 2)" "unswapped: the end line"
  expect_absent "$(cat "$work/unswapped/logs/proxy-swap.log" 2>/dev/null)" "FACT request " \
    "unswapped: the proxy saw no request"
  [ -d "$(sed -n 's/^check90: scratch directory: \([^ ]*\) .*/\1/p' "$work/unswapped/err" | head -n 1)" ] \
    && fail "unswapped: the scratch directory survived"
  return 0
}

# m1: the proxy takes requests from its own run only. A request that carries
# some other credential -- the shape of a page the browser loaded posting to
# loopback -- is refused, so the real token is never attached to it.
test_foreign_request_is_refused() {
  run90 foreign ok "" FAKE_CLAUDE_MODE=foreign
  expect_status "$status" 1 "foreign: the swap call fails"
  expect_contains "$out" "FACT swap: FAIL" "foreign: the fact"
  local log=$work/foreign/logs/proxy-swap.log
  [ -s "$log" ] || {
    fail "foreign: the swap phase left no proxy log, so this test proves nothing"
    return 0
  }
  expect_contains "$(cat "$log")" "refused not-this-run" "foreign: the refusal is recorded"
  expect_absent "$(cat "$log")" "FACT request " "foreign: nothing was forwarded to the upstream"
  assert_no_sentinel "$work/foreign" "foreign"
  return 0
}

# m3: the total budget is read at every step, not once before the last three, so
# a step that is slow for reasons of its own stops the run where the budget ran
# out. Here the streaming call is cut off at its own 5s cap, which puts the run
# past its 5s budget: the header drops and the delayed proxy must not run.
test_budget_is_read_at_every_step() {
  # The budget is wide enough for the three calls before the stream on a loaded
  # machine (a 5s budget ran out before step_streaming when the two bash tests
  # ran side by side), and the stream's hang is held by a cap longer than the
  # budget, so the budget is spent INSIDE step_streaming: only a check made
  # between steps can then stop step_drops.
  run90 budget ok "" CHECK90_TOTAL_SECONDS=20 CHECK90_CALL_TIMEOUT=25 \
    FAKE_CLAUDE_HANG_PROMPT="Write the numbers" FAKE_CLAUDE_HANG=60
  expect_status "$status" 0 "budget: the swap's verdict still stands"
  expect_contains "$out" "FACT tier-a: PASS — VIABLE" "budget: tier A"
  expect_contains "$out" "FACT budget: FAIL" "budget: the fact"
  expect_contains "$out" "before step_drops" "budget: the step that did not run is named"
  expect_contains "$out" "FACT headers-required: FAIL — not reached" "budget: the drops did not run"
  expect_contains "$out" "FACT timeout-retry: FAIL — not reached" "budget: the delayed proxy did not run"
  return 0
}

# The header lists are a MEASUREMENT, and a run that dropped no header did not
# make it. `CHECK90_MAX_DROPS=0` is the documented knob that ends the loop
# before its first drop (the budget can end it there too), and the step used to
# record the fallback text -- "none: every header observed is optional" -- as a
# PASS: a claim about what the client sends made without testing one header, in
# the list Task C1.7 is written against. It reads INFO now, and the detail says
# which stop ended the loop.
test_no_drops_measured_is_not_a_pass() {
  run90 nodrops ok "" CHECK90_MAX_DROPS=0
  expect_status "$status" 0 "no drops: the run"
  expect_contains "$out" "FACT headers-required: INFO — no header was dropped, so this list was NOT measured: the loop was stopped before its first drop by CHECK90_MAX_DROPS=0" \
    "no drops: the untested list is not claimed"
  expect_contains "$out" "FACT headers-optional: INFO — no header was dropped" \
    "no drops: neither list is claimed"
  expect_contains "$out" "FACT header-drop-skipped" "no drops: the knob is recorded as having stopped it"
  expect_absent "$out" "every header observed is optional" \
    "no drops: the run recorded the fallback text for a list it never tested"
  return 0
}

# m4: the version probe is ONE call, and it is capped: a client that hangs on
# `claude --version` must not hold the run that is measuring it.
test_version_probe_is_capped_and_once() {
  run90 version ok "" FAKE_CLAUDE_VERSION_LOG="$work/version/versions" \
    CHECK90_VERSION_TIMEOUT=20
  expect_status "$status" 0 "version: the run"
  local n
  n=$(wc -l <"$work/version/versions" | tr -d ' ')
  [ "$n" = 1 ] || fail "version: 'claude --version' was run $n time(s), expected exactly one"
  expect_contains "$out" "FACT claude-version: PASS — fake-claude 1.0" "version: the version is recorded"
  run90 version-hang ok "" FAKE_CLAUDE_VERSION_HANG=1 CHECK90_VERSION_TIMEOUT=2
  expect_status "$status" 0 "version hang: the run finishes anyway"
  expect_contains "$out" "FACT claude-version: FAIL — claude --version did not answer within 2s" \
    "version hang: the cap is named"
  expect_contains "$out" "CHECK 90 RESULT END (exit 0)" "version hang: the run reached the block"
  return 0
}

# M3: an INTERNAL error -- a command that fails where nothing is being judged --
# must be a fact of the run and "could not tell", never "NOT VIABLE". Before the
# ERR trap, `set -e` ended such a run with no fact at all and exit 1, which the
# block printed as a swap that was not viable.
test_internal_error_is_aborted_not_viable() {
  local dir=$work/internal
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
  write_fake_claude "$dir"
  write_fake_chmod "$dir"
  : >"$dir/claude.calls"
  local st=0
  printf '%s\n' "$SENTINEL" | env \
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 2 "internal error: could not tell"
  expect_contains "$(cat "$dir/out")" "FACT aborted: FAIL — an internal error" "internal error: the abort is a fact"
  expect_contains "$(cat "$dir/out")" "FACT tier-a: INFO — INCONCLUSIVE" "internal error: tier A is not judged"
  expect_absent "$(cat "$dir/out")" "NOT VIABLE" "internal error: no verdict either way"
  expect_contains "$(cat "$dir/out")" "FACT swap: FAIL — not reached" "internal error: the swap was not reached"
  expect_contains "$(cat "$dir/out")" "CHECK 90 RESULT END (exit 2)" "internal error: the end line"
  if [ -s "$dir/claude.calls" ]; then fail "internal error: a call was made anyway"; fi
  assert_no_sentinel "$dir" "internal error"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -d "$sc" ]; then fail "internal error: the scratch directory survived the stop"; fi
  return 0
}

# m5: the length of the token is a hint about the value, so it is not printed.
test_token_length_is_not_printed() {
  run90 length ok ""
  expect_contains "$out" "FACT token-file: PASS" "length: the fact"
  local line
  line=$(printf '%s\n' "$out" | grep -m1 '^FACT token-file: ' || true)
  expect_absent "$line" "${#SENTINEL}" "length: the token's length, in the fact about the token file"
  return 0
}

# The child that carries the real token is given the two flags the review asked
# for, and they are flags the client DOCUMENTS: the fake claude refuses any
# option its own --help does not list (it mirrors the installed client's set),
# so a flag that does not exist there fails this run rather than silently
# changing what the call does. --tools "" is what removes the built-in tools: a
# tool call is a process `claude` starts on its own, with the token in its
# environment. --no-session-persistence keeps the call from writing a session
# file into the owner's own store.
test_child_flags_disable_tools_and_are_documented() {
  local ctx=$work/flags/context
  run90 flags ok "" FAKE_CLAUDE_CONTEXT="$ctx"
  expect_status "$status" 0 "child flags: the run"
  [ -s "$ctx" ] || {
    fail "child flags: no child recorded its context, so this test proves nothing"
    return 0
  }
  local line n=0 flag help
  help=$("$work/flags/bin/claude" --help)
  while IFS= read -r line; do
    n=$((n + 1))
    expect_contains "$line" "tools=''" "child flags: the child is given NO tool at all (line $n)"
    expect_contains "$line" "--no-session-persistence" "child flags: no session file is written (line $n)"
    for flag in --settings --strict-mcp-config --tools --no-session-persistence; do
      expect_contains "$help" "$flag" "child flags: $flag is not in the client's own --help"
      expect_contains "$line" "$flag" "child flags: the child was not given $flag (line $n)"
    done
  done <"$ctx"
  [ "$n" -ge 3 ] || fail "child flags: only $n child(ren) recorded a context, the run changed shape"
}

# ANTHROPIC_LOG and DEBUG are not routing variables, but both change what the
# client prints or writes (ANTHROPIC_LOG writes request bodies to a file of its
# choosing), and this child's output is captured into a file the check reads and
# pastes back: a debug log of a call carrying CLAUDE_CODE_OAUTH_TOKEN is exactly
# the surface this check exists to keep the token off. Both are set on the RUN's
# environment here — the check does not refuse them in the owner's shell, it
# only removes them from the child — so the child's own environment is where
# their absence has to show.
test_child_is_not_started_with_debug_logging() {
  local ctx=$work/debugenv/context
  run90 debugenv ok "" FAKE_CLAUDE_CONTEXT="$ctx" ANTHROPIC_LOG=1 DEBUG=1
  expect_status "$status" 0 "debug logging: the run"
  [ -s "$ctx" ] || {
    fail "debug logging: no child recorded its context, so this test proves nothing"
    return 0
  }
  local line n=0
  while IFS= read -r line; do
    n=$((n + 1))
    expect_absent "$line" "ANTHROPIC_LOG" "debug logging: the child can see ANTHROPIC_LOG (line $n)"
    expect_absent "$line" "DEBUG" "debug logging: the child can see DEBUG (line $n)"
  done <"$ctx"
  [ "$n" -ge 3 ] || fail "debug logging: only $n child(ren) recorded a context, the run changed shape"
}

# The placeholder is a per-run secret: the proxy refuses any request whose
# Authorization is not it, so it is what keeps a page the browser loaded from
# having the REAL token attached to its request. It reaches the proxy through a
# 0600 file the proxy is pointed at, never through that process's command line:
# `ps` shows a process's argv to every user of the machine for as long as the
# process lives, and the delayed phase's proxy lives for minutes.
#
# The calls that the proxy swaps the credential for are the other half of the
# same rule, and they are what this test is about: they are handed the
# placeholder by the same 0600 FILE. They used to be handed it as the literal
# text of the source variable (`CHK90_CRED_SOURCE=literal:<value>`), which put
# the value on `env`'s own argv -- the very thing the file form exists to
# prevent -- and this test did not notice, because it learned the value from
# that same recording and then looked for it only in a snapshot of the live
# process table, where `env`'s argv is already gone (it execs the child).
#
# The value is written nowhere this test can read afterwards -- that is the
# point of it being per-run -- so the search is for its SHAPE: the script's
# random_placeholder is "placeholder-" followed by hex, and the source the run
# hands over is a PATH (file:/.../placeholder), which no shape match can
# produce. Both recordings are searched, because either alone would miss it:
# env.argv is the argv of the process that hands the credential over, which is
# where a value passed as an argument appears, and the snapshot is the argv of
# every process of the run while a call is live.
test_placeholder_is_never_in_a_process_argv() {
  local dir=$work/placeholder
  run90 placeholder ok "" \
    FAKE_CLAUDE_PS="$dir/ps.snapshot" FAKE_PS_PATTERN='chk90|proxy'
  expect_status "$status" 0 "placeholder: the run"
  # The positive control, and it is the same check that would fail on the
  # literal form: the swapped calls must have been handed a source that IS the
  # placeholder FILE. If the run handed no placeholder over at all -- or handed
  # it over any other way -- there is nothing here for the silence below to be
  # about, and this test says so instead of passing on it.
  local handed
  handed=$(grep -o 'CHK90_CRED_SOURCE=file:[^ ]*/placeholder' "$dir/env.argv" 2>/dev/null | head -n 1 || true)
  [ -n "$handed" ] || {
    fail "placeholder: no swapped call was handed the placeholder by file, so this test proves nothing about where the value went; env.argv held: $(cat "$dir/env.argv" 2>/dev/null | grep -c CHK90_CRED_SOURCE) credential source(s)"
    return 0
  }
  [ -s "$dir/ps.snapshot" ] || {
    fail "placeholder: nothing was recorded in the process snapshot, so it proves nothing"
    return 0
  }
  expect_contains "$(cat "$dir/ps.snapshot")" "proxy.py" \
    "placeholder: the snapshot did not catch the proxy itself, so its silence about the placeholder means nothing"
  # ... and the proxy is pointed at the FILE, so its silence about the value is
  # not the silence of a proxy that was given no placeholder at all. (That the
  # file itself is 0600 and inside the 0700 scratch directory is the script's
  # own doing; what this run can see is that the value is not in any argv.)
  expect_contains "$(cat "$dir/ps.snapshot")" "--require-credential-file" \
    "placeholder: the proxy was not handed the placeholder file, so this proves nothing about how it was handed the value"
  local hits
  hits=$(grep -n 'placeholder-[0-9a-f]' "$dir/env.argv" "$dir/ps.snapshot" 2>/dev/null || true)
  [ -z "$hits" ] || fail "placeholder: a per-run placeholder is in a process's argv, where every user of this machine can read it: $hits"
}

# Every managed settings directory the guard names is read, not just the first:
# the Linux one the runner writes and the one a macOS install of the client
# reads. Reading only the Linux path left the guard blind on the machine this
# check is most likely to be run from, with its FACT claiming no managed settings
# had been read at all. Two directories are named here and the routing key is in
# the SECOND, so a guard that stops at the first is caught.
test_managed_settings_reads_every_directory_it_names() {
  local dir=$work/managedboth
  mkdir -p "$dir/home" "$dir/tmp" "$dir/first" "$dir/second" "$dir/logs"
  write_fake_claude "$dir"
  write_fake_env "$dir"
  write_fake_ps "$dir"
  write_fake_upstream "$dir"
  : >"$dir/claude.calls"
  start_upstream "$dir" ok "" || return 1
  printf '%s' '{"env":{"ANTHROPIC_BASE_URL":"https://example.invalid"}}' \
    >"$dir/second/managed-settings.json"
  local st=0
  printf '%s\n' "$SENTINEL" | env \
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIRS="$dir/first:$dir/second" \
    CHECK90_UPSTREAM="$FAKE_UPSTREAM_ADDR" \
    CHECK90_DELAY=1 CHECK90_CALL_TIMEOUT=20 CHECK90_TOTAL_SECONDS=180 \
    CHECK90_KEEP_LOGS="$dir/logs" \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 2 "managed settings: the run"
  local line
  line=$(grep -m1 '^FACT managed-settings-dir: ' "$dir/out" || true)
  expect_contains "$line" "$dir/first" "managed settings: the first directory is named"
  expect_contains "$line" "$dir/second" "managed settings: the second directory is named"
  expect_contains "$(cat "$dir/out")" "FACT settings-routing: FAIL" \
    "managed settings: a routing key in the SECOND directory is not read"
  expect_contains "$(cat "$dir/err")" "$dir/second/managed-settings.json" \
    "managed settings: the file in the second directory is named"
  if [ -s "$dir/claude.calls" ]; then fail "managed settings: claude was run anyway"; fi
}

# The two directories a real run looks at are the Linux one and the macOS one,
# as literals, because a run here cannot be used to see them: this machine has a
# real /etc/claude-code, so a run would measure it rather than the check.
test_default_managed_settings_names_both_platforms() {
  expect_contains "$(cat "$script")" '/etc/claude-code' \
    "default managed settings: the Linux directory"
  expect_contains "$(cat "$script")" '/Library/Application Support/ClaudeCode' \
    "default managed settings: the macOS directory"
}

# A second Ctrl-C DURING the cleanup must not kill the shell that is running it:
# the first one has already ended the run, and what cleanup is doing is wiping
# the token file and removing the scratch directory. Killed here, the token file
# stays on disk in a directory nobody is left to remove, and the RESULT block
# that would say so is never printed. The fake `rm` takes its time and says when
# it started, so the signal lands inside the cleanup rather than before it.
test_second_interrupt_during_cleanup_does_not_kill_it() {
  local dir=$work/secondint
  mkdir -p "$dir/home" "$dir/tmp" "$dir/managed"
  write_fake_claude "$dir"
  write_fake_env "$dir"
  write_fake_ps "$dir"
  write_fake_rm "$dir"
  write_fake_upstream "$dir"
  : >"$dir/claude.calls"
  start_upstream "$dir" ok "" || return 1
  local st=0
  printf '%s\n' "$SENTINEL" >"$dir/tokenin"
  rm -f "$dir/rm.mark"
  # Job control: a background job of a non-interactive shell starts with SIGINT
  # ignored, and bash cannot trap a signal that was ignored when it started.
  set -m
  env \
    ${GUARD_UNSET[@]} \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    CHECK90_MANAGED_SETTINGS_DIR="$dir/managed" \
    CHECK90_UPSTREAM="$FAKE_UPSTREAM_ADDR" \
    CHECK90_DELAY=1 CHECK90_CALL_TIMEOUT=20 CHECK90_TOTAL_SECONDS=180 \
    CHECK90_KEEP_LOGS="$dir/logs" \
    FAKE_RM_MARK="$dir/rm.mark" FAKE_RM_SLEEP=3 \
    FAKE_CLAUDE_SENTINEL="$SENTINEL" FAKE_CLAUDE_CALLS="$dir/claude.calls" \
    bash "$script" <"$dir/tokenin" >"$dir/out" 2>"$dir/err" &
  local pid=$!
  # The run takes about a minute to reach its cleanup (CHECK90_DELAY=1 over the
  # seven steps), so this waits well past that, and stops early if the run is
  # already gone — a run that died before its cleanup is a failure below, not
  # something to wait three minutes for.
  local i=0
  while [ "$i" -lt 900 ] && kill -0 "$pid" 2>/dev/null; do
    [ -s "$dir/rm.mark" ] && break
    sleep 0.2
    i=$((i + 1))
  done
  if [ ! -s "$dir/rm.mark" ]; then
    kill -KILL "$pid" 2>/dev/null || true
    set +m
    wait "$pid" 2>/dev/null || true
    fail "second interrupt: the run never reached its cleanup (no rm started), so this test proved nothing: $(tail -n 2 "$dir/err" | tr '\n' ' ')"
    return 0
  fi
  kill -INT "$pid" 2>/dev/null || true
  local j=0
  while kill -0 "$pid" 2>/dev/null && [ "$j" -lt 600 ]; do
    sleep 0.2
    j=$((j + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null || true
    set +m
    fail "second interrupt: the run did not end after the second signal"
    wait "$pid" 2>/dev/null || true
    return 0
  fi
  wait "$pid" || st=$?
  set +m
  # The verdict is the run's own: the happy path measured what it measured, and
  # the signal during the cleanup is not allowed to change either that or the
  # block that reports it.
  expect_status "$st" 0 "second interrupt: the run's own verdict survives the second signal"
  expect_contains "$(cat "$dir/out")" "CHECK 90 RESULT END (exit 0)" \
    "second interrupt: the block was still printed after the cleanup"
  expect_contains "$(cat "$dir/err")" "cleanup is running, do not press Ctrl-C again" \
    "second interrupt: the run says what the second Ctrl-C did"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  [ -n "$sc" ] || fail "second interrupt: the scratch directory was never announced"
  if [ -d "$sc" ]; then fail "second interrupt: the cleanup was cut short — $sc survived"; fi
  if [ -e "$sc/token" ]; then fail "second interrupt: the token file survived the cleanup"; fi
  return 0
}

test_happy_path
test_block_format
test_env_guards
test_settings_guard
test_swap_fails
test_limit_is_recorded
test_control_failure_is_inconclusive
test_dry_run_makes_no_call
test_token_is_never_on_a_command_line
test_streaming_call_is_capped
test_sigint_cleans_up
test_no_second_paste
test_xtrace_is_refused
test_missing_python3_is_named_not_an_internal_error
test_child_is_confined
test_swap_the_proxy_never_saw
test_foreign_request_is_refused
test_budget_is_read_at_every_step
test_no_drops_measured_is_not_a_pass
test_version_probe_is_capped_and_once
test_internal_error_is_aborted_not_viable
test_token_length_is_not_printed
test_child_flags_disable_tools_and_are_documented
test_child_is_not_started_with_debug_logging
test_placeholder_is_never_in_a_process_argv
test_managed_settings_reads_every_directory_it_names
test_default_managed_settings_names_both_platforms
test_second_interrupt_during_cleanup_does_not_kill_it

if [ "$failures" -ne 0 ]; then
  echo "check90_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check90_test.sh: ok"
