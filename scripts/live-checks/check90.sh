#!/usr/bin/env bash
# Check 90 — the OAuth header swap, as ONE script you run.
#
# It answers one question: does Claude Code's subscription traffic survive a
# proxy that strips the incoming credential and attaches your real one? If it
# does not, the credentials service's TIER A tasks are dropped (the plan's
# Global Constraint 4). The register's section is
# docs/gcp-live-checklist.md, "Check 90"; the step-by-step text it used to
# carry is docs/credentials-service/check90-steps.md.
#
# Usage:      bash scripts/live-checks/check90.sh
# Needs:      bash, claude, python3. Nothing is installed, nothing is fetched,
#             no cloud project is touched, and no Fugaro installation is
#             involved. If one of those is missing the script stops with one
#             line naming it.
# Costs:      a handful of tiny prompts — a control, a swap, one long-ish
#             prompt, one prompt per optional header (at most 8) and one on a
#             delayed proxy. All of them on YOUR subscription; there is no
#             API-key spend, and step 9's wait is the only slow part (default
#             90s, CHECK90_DELAY).
# Duration:   about two minutes, more if the client waits longer than the
#             delayed proxy does.
# You type:   the value `claude setup-token` printed, once, at a hidden prompt.
#             NO AGENT EVER SEES IT: the script is run by you, the prompt does
#             not echo, the value is never on a command line, and it reaches
#             nothing but the 0600 file the proxy reads and the one `claude`
#             child that needs it. Nothing the script prints — the RESULT
#             block above all — can carry a token or a header value: it prints
#             header NAMES and statuses only, and a test enforces that.
# Cleanup:    always, including on failure and on Ctrl-C: the proxy is killed,
#             the token file is removed, the scratch directory goes. The trap
#             re-checks its own marker file before rm -rf, as the section's
#             step 9 did.
# Output:     progress on stderr, ONE block on stdout headed CHECK 90 RESULT,
#             to copy back to the coordinator. Exit 0 when tier A is viable,
#             1 when it is not, 2 when the run could not tell (a control that
#             failed says nothing about the swap).
#
# CHECK90_UPSTREAM, CHECK90_DELAY, CHECK90_CALL_TIMEOUT, CHECK90_TOTAL_SECONDS,
# CHECK90_MAX_DROPS and CHECK90_MANAGED_SETTINGS_DIR exist for this script's own
# test: CHECK90_UPSTREAM may only name a loopback address (the test's fake
# upstream), so no environment can send the token anywhere but Anthropic's
# endpoint, and CHECK90_MANAGED_SETTINGS_DIR names a directory the guard still
# READS, never one it skips.
set -euo pipefail

# The owner's own stdout and stderr, kept on 3 and 4. A trap runs with whatever
# redirection is in effect at the moment the signal arrives -- and this script
# redirects a call's output for the whole of that call -- so the RESULT block
# and the messages around it are written to the SAVED descriptors, never to
# whatever fd 1 happens to be pointing at when Ctrl-C lands.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 1; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact "aborted" FAIL "$1"
  owner "stopped: $1"
  exit 1
}

# stop2 REASON: the same, for a stop that says nothing about the swap itself —
# the run reached the calls but the control failed, so the answer is "could not
# tell" rather than "no".
stop2() {
  fact "aborted" FAIL "$1"
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() {                       # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

DRY_RUN=${CHECK90_DRY_RUN:-}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 90 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 90 needs bash"
  need mktemp
  need claude "Claude Code, the client this check measures"
  need python3 "the throwaway proxy is one python3 file, written into the scratch directory"
  # Only so the message is one line when it is missing, not two.
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk90-mine"
PROXY_PID=""
TOKEN_FILE=""
SCRATCH_REMOVED=no
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk90.XXXXXXXX") || die "Check 90 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  TOKEN_FILE="$SCRATCH/token"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 90 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  stop_call
  stop_proxy
  rm -f "$TOKEN_FILE"
  keep_logs
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

# keep_logs copies the proxy logs — and nothing else — where the owner asked
# for them, so a failed run can be read after the scratch directory is gone.
# The token file is never among them: this is a copy of the .log files only.
keep_logs() {
  [ -n "${CHECK90_KEEP_LOGS:-}" ] || return 0
  [ -d "$SCRATCH" ] || return 0
  if (cd "$CHECK90_KEEP_LOGS" 2>/dev/null && git rev-parse --show-toplevel >/dev/null 2>&1); then
    say "not copying logs into $CHECK90_KEEP_LOGS: it is inside a git work tree"
    return 0
  fi
  mkdir -p "$CHECK90_KEEP_LOGS" 2>/dev/null || return 0
  cp "$SCRATCH"/proxy-*.log "$CHECK90_KEEP_LOGS"/ 2>/dev/null || true
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    # with_timeout puts every call in its own process group, so the signal
    # reaches the whole call -- the streaming one is a pipeline, and its reader
    # would otherwise outlive the client. The single-PID fallback is for a shell
    # where job control could not be turned on: there the call is one process.
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

remove_scratch() {
  case "$SCRATCH" in
  "" | / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH"
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

stop_proxy() {
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  PROXY_PID=""
}

finish() {
  local status=$?
  trap - EXIT INT TERM
  cleanup
  local facts=$FACTS_CAPTURED
  if ! printf '%s' "$facts" | grep -q '^tier-a|' && [ "$status" = 2 ]; then
    # A run that reached the calls and could not judge the swap says so here,
    # rather than in a "not reached" line that reads like a step nobody ran.
    facts="${facts}"$'\n'"tier-a|INFO|INCONCLUSIVE — the run reached the calls and could not judge the swap; read the FAILed fact above"
  fi
  local restore=PASS
  [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
  facts="${facts}"$'\n'"restore|$restore|the proxy is stopped, the token file is removed, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there")"
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
GUARD_VARS=(ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL
  CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_BEDROCK)

guard_env() {
  local set_vars=() v
  for v in "${GUARD_VARS[@]}"; do
    if printenv "$v" >/dev/null 2>&1; then set_vars+=("$v"); fi
  done
  if [ ${#set_vars[@]} -gt 0 ]; then
    fact "env-cleared" FAIL "set in your shell: ${set_vars[*]} — unset them and run again"
    stop "these are set in your shell: ${set_vars[*]} (their values are not printed, and none is read). Each one makes 'claude' answer with something other than the setup-token, so the control would measure the wrong credential. Run: unset ${set_vars[*]}"
  fi
  fact "env-cleared" PASS "none of ${GUARD_VARS[*]} is set in this shell"
}

# settings_keys FILE: the routing key NAMES a settings file sets, nothing else.
# Non-zero when the file is not settings JSON this check can read.
settings_keys() {
  python3 "$SCRATCH/settingskeys.py" "$1"
}

guard_settings() {
  local files=() f setkeys
  for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
    [ -f "$f" ] && files+=("$f")
  done
  if [ -n "${CLAUDE_CONFIG_DIR:-}" ] && [ -f "$CLAUDE_CONFIG_DIR/settings.json" ]; then
    files+=("$CLAUDE_CONFIG_DIR/settings.json")
  fi
  # The directory Claude Code keeps its managed settings in, one level deep.
  # CHECK90_MANAGED_SETTINGS_DIR exists so this script's own test can run on a
  # machine that has one; nothing else should set it, and a value names a
  # directory that is then READ, never one that is skipped.
  local managed=${CHECK90_MANAGED_SETTINGS_DIR:-/etc/claude-code}
  if [ -d "$managed" ]; then
    while IFS= read -r f; do files+=("$f"); done < <(find "$managed" -maxdepth 1 -type f 2>/dev/null)
    fact "managed-settings-dir" PASS "$managed is there and was read"
  else
    fact "managed-settings-dir" PASS \
      "no $managed on this machine, so no managed settings file was read — if Claude Code keeps one elsewhere here, this check did NOT look at it"
  fi
  local found=()
  for f in ${files[@]+"${files[@]}"}; do
    if ! setkeys=$(settings_keys "$f"); then
      found+=("$f (not settings JSON this check can read)")
      continue
    fi
    if [ -n "$setkeys" ]; then found+=("$f sets $setkeys"); fi
  done
  if [ ${#found[@]} -gt 0 ]; then
    fact "settings-routing" FAIL "${found[*]}"
    stop "a settings file would route 'claude' around this check: ${found[*]} — only key names are printed, never a value. Remove those keys (or run the check where they are not set) and run again"
  fi
  fact "settings-routing" PASS \
    "no settings file this check reads sets apiKeyHelper or an env key that would route the call or its credential elsewhere (${#files[@]} file(s) read)"
}

# ------------------------------------------------------------------ the token
ask_token() {
  say ""
  say "Paste the value from 'claude setup-token' below. It is not echoed, not put on a"
  say "command line and not exported to anything but the one 'claude' child that needs it."
  printf '%s: token (hidden, then Enter): ' "$PROG" >&2
  local token=""
  IFS= read -rs token || true
  printf '\n' >&2
  [ -n "$token" ] || stop "no token was typed, so there is nothing to swap in"
  (umask 077 && printf '%s' "$token" >"$TOKEN_FILE")
  chmod 600 "$TOKEN_FILE"
  unset token
  local mode
  mode=$(python3 -c 'import os,sys; print(oct(os.stat(sys.argv[1]).st_mode & 0o777))' "$TOKEN_FILE")
  fact "token-file" PASS "0600 written to the scratch directory; ${mode}; $(( $(wc -c <"$TOKEN_FILE") )) bytes"
  printf '%s: what did claude setup-token say about how long the token lasts? (paste it, or Enter to skip): ' "$PROG" >&2
  local validity=""
  IFS= read -r validity || true
  [ -n "$validity" ] || validity="(not recorded — paste it from claude setup-token yourself)"
  fact "token-validity" PASS "$validity"
}

# ------------------------------------------------------------------ the proxy
write_files() {
  cat >"$SCRATCH/proxy.py" <<'PROXY_EOF'
#!/usr/bin/env python3
"""Check 90's throwaway loopback proxy.

It does one thing to a request: it deletes the client's Authorization and
x-api-key and attaches the token read from --token-file -- unless --no-swap is
given, in which case it changes no header and attaches nothing, so the
credential the client sent is the one the API sees (that is the observed
control). Everything else is forwarded as it arrived, the response is streamed
back as it arrives, and the only things printed are header NAMES, statuses and
timings. A request whose Host is not this listener is refused with 403 and
never forwarded: loopback is reachable from any page a browser loads.
"""
import argparse, http.client, http.server, json, time

ARGS = None
TOKEN = None
HOP = {"authorization", "x-api-key", "host", "content-length", "connection",
       "transfer-encoding", "accept-encoding", "expect", "proxy-connection",
       "keep-alive", "te", "upgrade"}
DROP = set()


def fact(*parts):
    print("FACT", *parts, flush=True)


def read_capped(resp, limit=65536):
    out = b""
    while len(out) < limit:
        chunk = resp.read1(min(65536, limit - len(out)))
        if not chunk:
            break
        out += chunk
    return out


def loopback_host(host, port):
    return (host or "").strip().lower() in {
        "127.0.0.1:%d" % port, "localhost:%d" % port, "[::1]:%d" % port}


def credential_headers(headers):
    return [n for n in ("authorization", "x-api-key") if headers.get(n)]


def forward_names(headers, no_swap):
    names = [n for n in sorted({k.lower() for k in headers.keys()})
             if n not in HOP and n not in DROP]
    if no_swap:
        names += [n for n in ("authorization", "x-api-key") if headers.get(n)]
    return names


def error_fields(body):
    """The only two keys of a body that are ever read: the error object's own
    `type` and `message`. Nothing else is indexed, so nothing else can leak."""
    try:
        parsed = json.loads(body.decode("utf-8", "replace"))
    except Exception:
        return ("(unreadable body: not JSON)",)
    err = parsed.get("error") if isinstance(parsed, dict) else None
    if not isinstance(err, dict):
        return ("(no error object in the body)",)
    t, m = err.get("type"), err.get("message")
    return ("type: " + t if isinstance(t, str) else "type: (none)",
            "message: " + m.replace("\n", " ") if isinstance(m, str)
            else "message: (none)")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "chk90proxy"

    def log_message(self, *a):
        pass

    def read_body(self):
        if "chunked" in (self.headers.get("Transfer-Encoding") or "").lower():
            out = []
            while True:
                n = int(self.rfile.readline().split(b";")[0], 16)
                if n == 0:
                    self.rfile.readline()
                    return b"".join(out)
                out.append(self.rfile.read(n))
                self.rfile.readline()
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def forward(self):
        started = time.time()
        if not loopback_host(self.headers.get("Host"), ARGS.port):
            fact("refused", "host-not-loopback", "the request was not forwarded")
            self.send_error(403, "chk90proxy: Host must be this loopback address and port")
            return
        body = self.read_body()
        fact("credential-header",
             ",".join(credential_headers(self.headers)) or "(neither)")
        sent = forward_names(self.headers, ARGS.no_swap)
        fact("request", self.command, self.path, "headers:",
             ",".join(sent) or "(none)")
        if ARGS.delay:
            fact("delaying", ARGS.delay, "seconds before answering")
            time.sleep(ARGS.delay)
        cls = http.client.HTTPSConnection if ARGS.upstream_scheme == "https" else http.client.HTTPConnection
        conn = cls(ARGS.upstream, timeout=ARGS.timeout)
        try:
            conn.putrequest(self.command, self.path, skip_host=True,
                            skip_accept_encoding=True)
            conn.putheader("Host", ARGS.upstream)
            if TOKEN is not None:
                conn.putheader("Authorization", "Bearer " + TOKEN)
            for n in sent:
                conn.putheader(n, self.headers[n])
            conn.putheader("Content-Length", str(len(body)))
            conn.endheaders(body)
            resp = conn.getresponse()
        except Exception as e:
            fact("upstream-error", type(e).__name__, "after", "%.1fs" % (time.time() - started))
            self.send_error(502, "chk90proxy: upstream error")
            return
        fact("response", resp.status, resp.reason, "headers:",
             ",".join(n.lower() for n, _ in resp.getheaders()))
        head = b""
        if ARGS.error_fields and resp.status >= 400:
            head = read_capped(resp)
            fact("error-fields", *error_fields(head))
        self.send_response(resp.status)
        for n, v in resp.getheaders():
            if n.lower() in ("content-length", "transfer-encoding",
                             "connection", "keep-alive"):
                continue
            self.send_header(n, v)
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            if head:
                self.wfile.write(b"%x\r\n" % len(head) + head + b"\r\n")
                self.wfile.flush()
            while True:
                chunk = resp.read1(65536)
                if not chunk:
                    break
                self.wfile.write(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            fact("client-gone", "the client closed before the response ended")
        conn.close()

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = forward


def main():
    global ARGS, TOKEN, DROP
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8787)
    ap.add_argument("--token-file", help="the token attached in place of the client's own")
    ap.add_argument("--no-swap", action="store_true")
    ap.add_argument("--upstream", default="api.anthropic.com")
    ap.add_argument("--upstream-scheme", default="https", choices=("https", "http"))
    ap.add_argument("--delay", type=float, default=0.0)
    ap.add_argument("--drop", action="append", default=[])
    ap.add_argument("--timeout", type=float, default=3600.0)
    ap.add_argument("--error-fields", action="store_true")
    ARGS = ap.parse_args()
    if not ARGS.no_swap and not ARGS.token_file:
        ap.error("--token-file is required unless --no-swap is given")
    if not ARGS.no_swap:
        TOKEN = open(ARGS.token_file, "rb").read().strip().decode()
    DROP = {d.lower() for d in ARGS.drop}
    server = http.server.ThreadingHTTPServer(("127.0.0.1", ARGS.port), Handler)
    print("chk90proxy listening on 127.0.0.1:%d, upstream %s" % (ARGS.port, ARGS.upstream),
          flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
PROXY_EOF
  cat >"$SCRATCH/settingskeys.py" <<'KEYS_EOF'
#!/usr/bin/env python3
"""The routing key NAMES a Claude Code settings file sets. Never a value."""
import json, sys

WANT = {"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
        "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_SKIP_VERTEX_AUTH",
        "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

with open(sys.argv[1]) as fh:
    doc = json.load(fh)
if not isinstance(doc, dict):
    sys.exit(1)
keys = []
for k in doc:
    if k.lower() == "apikeyhelper":
        keys.append("apiKeyHelper")
env = doc.get("env") or {}
if isinstance(env, dict):
    for k in env:
        upper = k.upper()
        if upper in WANT or upper.startswith("CLAUDE_CODE_USE_"):
            keys.append(upper)
print(" ".join(sorted(set(keys))))
KEYS_EOF
}

start_proxy() { # start_proxy LOGFILE [extra args...]
  local log=$1
  shift
  stop_proxy
  python3 "$SCRATCH/proxy.py" --port "$PORT" --upstream "$UPSTREAM" \
    --upstream-scheme "$UPSTREAM_SCHEME" "$@" >"$log" 2>&1 &
  PROXY_PID=$!
  local i=0
  while [ "$i" -lt 50 ]; do
    if grep -q 'listening on' "$log" 2>/dev/null; then return 0; fi
    if ! kill -0 "$PROXY_PID" 2>/dev/null; then
      stop "the proxy did not start: $(tail -n 3 "$log" | tr '\n' ' ')"
    fi
    sleep 0.1
    i=$((i + 1))
  done
  stop "the proxy did not report that it is listening within 5s"
}

# --------------------------------------------------------------- calling claude
with_timeout() { # with_timeout SECONDS cmd...
  local secs=$1
  shift
  # Job control, so the call gets a process group of its own: without it the
  # deadline below could only kill the group's leader -- and a streaming call is
  # a pipeline, whose reader and client are separate processes. stdin stays
  # /dev/null, as it was before job control was on: `claude -p` takes its prompt
  # as an argument, and a client that decided to read the terminal instead would
  # hang.
  set -m
  "$@" </dev/null &
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      # The pid is kept here and cleared by stop_call: the wait below has to
      # name the call to reap it, and cleanup must not signal it twice.
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  local status=0
  wait "$CALL_PID" || status=$?
  CALL_PID=""
  return "$status"
}

# The credential the client is given. The control and the observed control run
# on the real token -- they are what says the token works and that the client
# sends it -- while the swap, the streaming prompt, the dropped headers and the
# delayed proxy all run on a PLACEHOLDER that is not a credential, because
# there the proxy is what attaches the real one, and a call that carried it
# anyway would prove nothing.
PLACEHOLDER="placeholder-not-a-credential"

# Where a call is told its credential is — a SOURCE, never the value:
#   file:PATH     the 0600 token file, the one the proxy reads too
#   literal:TEXT  the placeholder above, which is not a credential
# The child reads the source into CLAUDE_CODE_OAUTH_TOKEN itself. That is what
# keeps the token off every command line: `env CLAUDE_CODE_OAUTH_TOKEN=value`
# would put the value in `env`'s own argv, which /proc/<pid>/cmdline shows to
# every process on the machine for as long as it lives.
# shellcheck disable=SC2016 # the child's own text: nothing here expands in this shell
CRED_CHILD='
case "$CHK90_CRED_SOURCE" in
file:*) CLAUDE_CODE_OAUTH_TOKEN=$(cat "${CHK90_CRED_SOURCE#file:}") || exit 1 ;;
literal:*) CLAUDE_CODE_OAUTH_TOKEN=${CHK90_CRED_SOURCE#literal:} ;;
*) echo "check90: the call was given no credential source" >&2; exit 1 ;;
esac
export CLAUDE_CODE_OAUTH_TOKEN
unset CHK90_CRED_SOURCE
exec claude "$@"'

# claude_call BASE_URL CRED_SOURCE OUT CALL_TIMEOUT "$@" — runs claude with
# nothing in its environment but the credential and the base URL this check
# chose. The five variables the guards refused cannot reach it from here
# either, and no argument of the call carries a credential: its argv holds only
# the source — a path, or the placeholder — and the child is what reads it.
claude_call() {
  local base=$1 src=$2 out=$3 secs=$4
  shift 4
  set +e
  with_timeout "$secs" env \
    -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    ANTHROPIC_BASE_URL="$base" \
    CHK90_CRED_SOURCE="$src" \
    bash -c "$CRED_CHILD" check90-claude "$@" >"$out" 2>&1
  local status=$?
  set -e
  return "$status"
}

phase_log() { printf '%s/proxy-%s.log' "$SCRATCH" "$1"; }

# The observed control and the swap both go through the proxy; the direct
# control does not, and therefore produces no credential-header line.
header_of() { # header_of LOGFILE
  sed -n 's/^FACT credential-header //p' "$1" 2>/dev/null | tail -n 1
}
attempts_in() { # attempts_in LOGFILE
  local n
  n=$(grep -c '^FACT request ' "$1" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}
header_names_of() { # header_names_of LOGFILE — the names the swap call forwarded
  sed -n 's/^FACT request .* headers: //p' "$1" 2>/dev/null | tail -n 1 |
    tr ',' '\n' | sed '/^$/d' |
    grep -v -x -e host -e content-length -e authorization -e x-api-key \
      -e connection -e expect -e accept-encoding -e transfer-encoding | tr '\n' ' '
}

# ------------------------------------------------------------------- the steps
step_control() {
  say "step 1/7: the control (no proxy, your token in the environment)"
  if claude_call "https://api.anthropic.com" "file:$TOKEN_FILE" "$SCRATCH/control.out" "$CALL_TIMEOUT" -p "say ok"; then
    fact "control" PASS "the call succeeded with the setup-token and no proxy in its path"
  else
    fact "control" FAIL "the call failed ($(head -c 300 "$SCRATCH/control.out" | tr '\n' ' '))"
    stop2 "the control call failed, so nothing about the swap can be learned: the token itself is what is in question. A token that fails here says nothing about the header swap"
  fi
}

step_observed_control() {
  say "step 2/7: the observed control (the same token, through the proxy, swapping nothing)"
  local log
  log=$(phase_log observed)
  start_proxy "$log" --no-swap
  if claude_call "http://127.0.0.1:$PORT" "file:$TOKEN_FILE" "$SCRATCH/observed.out" "$CALL_TIMEOUT" -p "say ok"; then
    local h
    h=$(header_of "$log")
    if [ "$h" = "authorization" ]; then
      fact "observed-control" PASS "the client sent the credential this check swaps (credential-header: $h)"
    else
      fact "observed-control" FAIL "the client sent '${h:-nothing}', not authorization, so this environment is not on the setup-token path — the control measured something else"
      stop "the client sent '${h:-nothing}' rather than an Authorization header, so this environment routes 'claude' somewhere other than the subscription credential (a Vertex, Bedrock or API-key path). Nothing was learned about the swap"
    fi
  else
    fact "observed-control" FAIL "the call failed through the proxy with nothing swapped ($(head -c 300 "$SCRATCH/observed.out" | tr '\n' ' '))"
    stop2 "the observed control failed with the client's own credential untouched, so the proxy in the path is what failed — not the swap"
  fi
}

step_swap() {
  say "step 3/7: the swap (the proxy strips the client's credential and attaches yours)"
  local log
  log=$(phase_log swap)
  start_proxy "$log" --token-file "$TOKEN_FILE" --error-fields
  local started ended
  started=$(date +%s)
  if claude_call "http://127.0.0.1:$PORT" "literal:$PLACEHOLDER" "$SCRATCH/swap.out" "$CALL_TIMEOUT" -p "say ok"; then
    ended=$(date +%s)
    local h
    h=$(header_of "$log")
    fact "swap" PASS "the call succeeded with the proxy's own credential attached ($((ended - started))s)"
    fact "swap-credential-header" PASS "credential-header: ${h:-missing}"
  else
    fact "swap" FAIL "the call failed through the proxy while the control succeeded ($(head -c 300 "$SCRATCH/swap.out" | tr '\n' ' '))"
    fact "swap-credential-header" INFO "credential-header: $(header_of "$log")"
    fact "headers-required" FAIL "not recorded: the swap call did not complete"
    fact "tier-a" FAIL "NOT VIABLE — subscription traffic did not survive the header swap (Global Constraint 4: the TIER A tasks are dropped and the service ships the B-only cascade)"
    save_usage_limit "$log"
    return 0
  fi
  save_usage_limit "$log"
  save_optional_headers "$log"
}

# The usage limit is looked for, never provoked: whatever an error response
# happened to carry is recorded, and if none occurred the check passes and U3
# stays open.
save_usage_limit() { # save_usage_limit LOGFILE
  local log=$1 line
  line=$(grep -m1 '^FACT error-fields ' "$log" 2>/dev/null || true)
  if [ -z "$line" ]; then
    local status
    status=$(sed -n 's/^FACT response \([0-9]*\) .*/\1/p' "$log" 2>/dev/null | sort -u | grep -E '^(4|5)' | head -n 1 || true)
    if [ -n "$status" ]; then
      local reset
      reset=$(sed -n 's/^FACT response [0-9]* .*headers: //p' "$log" | tr ',' '\n' |
        grep -E 'retry-after|ratelimit|reset' | tr '\n' ' ' || true)
      fact "usage-limit" PASS "status $status, reset headers: ${reset:-none seen} (no error object came back; U3 stays open until a reset-carrying answer is seen)"
    else
      fact "usage-limit" PASS "none observed — none was provoked; U3 stays open and the plan's 15-minute default back-off applies"
    fi
    return 0
  fi
  fact "usage-limit" PASS "$line (the error object's own type and message, the only body fields this check ever reads)"
  local reset
  reset=$(sed -n 's/^FACT response [0-9]* .*headers: //p' "$log" | tr ',' '\n' |
    grep -E 'retry-after|ratelimit|reset' | tr '\n' ' ' || true)
  fact "usage-limit-reset-headers" PASS "${reset:-none seen}"
}

OPTIONAL_HEADERS=""
save_optional_headers() {
  OPTIONAL_HEADERS=$(header_names_of "$(phase_log swap)")
  fact "headers-observed" PASS "${OPTIONAL_HEADERS:-none}"
}

step_streaming() {
  say "step 4/7: a long-ish prompt, streamed through the proxy"
  local log times
  log=$(phase_log stream)
  times="$SCRATCH/stream.times"
  : >"$times"
  start_proxy "$log" --token-file "$TOKEN_FILE"
  local status=0
  set +e
  # Under the same cap as every other call. It is a pipeline, so the cap has to
  # reach the group, not one process: with_timeout and stop_call do that.
  with_timeout "$CALL_TIMEOUT" stream_call "$times"
  status=$?
  set -e
  local lines first last spread
  lines=$(wc -l <"$times" | tr -d ' ')
  first=$(head -n 1 "$times" 2>/dev/null || true)
  last=$(tail -n 1 "$times" 2>/dev/null || true)
  # A call the cap cut off before its first line leaves the file empty, and an
  # empty variable is 0 in an arithmetic context: this reads as a spread of 0,
  # which is only ever reported when the call produced lines.
  spread=$((last - first))
  if [ "$status" -eq 0 ] && [ "$lines" -gt 1 ]; then
    if [ "$spread" -ge 2 ]; then
      fact "stream" PASS "completed, $lines output lines over ${spread}s of arrival — the text arrived progressively, not in one burst"
    else
      fact "stream" FAIL "completed, but all $lines lines arrived within ${spread}s: this client buffers, or the answer was too short to tell"
    fi
  elif [ "$status" -eq 0 ]; then
    fact "stream" FAIL "completed but produced $lines line(s), too few to say whether it streamed"
  elif [ "$status" -eq 124 ]; then
    fact "stream" FAIL "did not complete: this check's own ${CALL_TIMEOUT}s cap cut the call off, after $lines line(s) of output"
  else
    fact "stream" FAIL "the call failed (status $status) after $lines line(s) of output"
  fi
}

stream_call() { # stream_call TIMESFILE — one prompt, its arrival times recorded
  local times=$1
  env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
    -u CLAUDE_CODE_USE_VERTEX -u CLAUDE_CODE_USE_BEDROCK \
    ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" \
    CHK90_CRED_SOURCE="literal:$PLACEHOLDER" \
    bash -c "$CRED_CHILD" check90-claude \
    -p "Write the numbers from 1 to 20, one per line." |
    while IFS= read -r _line; do date +%s >>"$times"; done
}

step_drops() {
  say "step 5/7: dropping each optional header in turn (this is the list Task C1.7 is written against)"
  local required=() optional=() name log status
  local n=0
  for name in $OPTIONAL_HEADERS; do
    n=$((n + 1))
    if [ "$n" -gt "$MAX_DROPS" ]; then
      fact "header-drop-skipped" PASS "more than $MAX_DROPS optional headers were seen; the rest were not dropped (CHECK90_MAX_DROPS)"
      break
    fi
    log=$(phase_log "drop-$name")
    start_proxy "$log" --token-file "$TOKEN_FILE" --drop "$name"
    if claude_call "http://127.0.0.1:$PORT" "literal:$PLACEHOLDER" "$SCRATCH/drop.out" "$CALL_TIMEOUT" -p "say ok"; then
      optional+=("$name")
      fact "header-drop-$name" PASS "the call survived dropping it (optional)"
    else
      required+=("$name")
      fact "header-drop-$name" FAIL "the call failed when it was dropped, so it MUST pass through"
    fi
  done
  fact "headers-required" PASS "${required[*]:-none: every header observed is optional}"
  fact "headers-optional" PASS "${optional[*]:-none}"
}

step_timeout() {
  say "step 6/7: the client's timeout and retries (a proxy that answers $DELAY s late)"
  local log started ended status attempts
  log=$(phase_log delay)
  start_proxy "$log" --token-file "$TOKEN_FILE" --delay "$DELAY"
  started=$(date +%s)
  set +e
  claude_call "http://127.0.0.1:$PORT" "literal:$PLACEHOLDER" "$SCRATCH/delay.out" "$CALL_TIMEOUT" -p "say ok"
  status=$?
  set -e
  ended=$(date +%s)
  attempts=$(attempts_in "$log")
  if [ "$status" -eq 124 ]; then
    fact "timeout-retry" PASS "the client was still waiting when this check's own ${CALL_TIMEOUT}s cap cut it off, after ${attempts} attempt(s) — so its timeout is longer than ${CALL_TIMEOUT}s or it has none (U4: the wait belongs in the runner)"
  elif [ "$status" -eq 0 ]; then
    fact "timeout-retry" PASS "the client waited $((ended - started))s for an answer delayed by ${DELAY}s, in ${attempts} attempt(s), and succeeded"
  else
    fact "timeout-retry" PASS "the client gave up after $((ended - started))s on an answer delayed by ${DELAY}s, in ${attempts} attempt(s), status $status ($(head -c 200 "$SCRATCH/delay.out" | tr '\n' ' '))"
  fi
}

# ------------------------------------------------------------------- reporting
tier_a() {
  local t
  t=$(grep -m1 '^swap|' "$FACTS_FILE" 2>/dev/null | cut -d'|' -f2 || true)
  case "$t" in
  PASS)
    fact "tier-a" PASS "VIABLE — the swap call succeeded where the control did"
    return 0
    ;;
  FAIL)
    # The FAIL fact was already recorded with its reason; nothing to add.
    return 1
    ;;
  *)
    fact "tier-a" INFO "INCONCLUSIVE — the run did not reach the swap call; read the FAILed fact above for which step stopped it"
    return 2
    ;;
  esac
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key (the per-header drops) follows,
# sorted. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(claude-version env-cleared managed-settings-dir settings-routing
  token-file token-validity control observed-control swap swap-credential-header
  stream headers-observed headers-required headers-optional usage-limit
  timeout-retry restore tier-a)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 90 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 90 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
free_port() {
  python3 - <<'PORT_EOF'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PORT_EOF
}

main() {
  PORT=${CHECK90_PORT:-}
  [ -n "$PORT" ] || PORT=$(free_port)
  DELAY=${CHECK90_DELAY:-90}
  CALL_TIMEOUT=${CHECK90_CALL_TIMEOUT:-240}
  TOTAL_SECONDS=${CHECK90_TOTAL_SECONDS:-1800}
  MAX_DROPS=${CHECK90_MAX_DROPS:-8}
  UPSTREAM=${CHECK90_UPSTREAM:-api.anthropic.com}
  UPSTREAM_SCHEME=https
  case "$UPSTREAM" in
  127.0.0.1:* | localhost:* | "[::1]:"*) UPSTREAM_SCHEME=http ;;
  *)
    if [ -n "${CHECK90_UPSTREAM:-}" ]; then
      die "Check 90 refused: CHECK90_UPSTREAM may only name a loopback address, so the token can never leave this machine"
    fi
    ;;
  esac

  need_tools
  scratch_up
  write_files
  case "$(claude --version 2>/dev/null || true)" in
  "") fact "claude-version" FAIL "claude --version printed nothing" ;;
  *) fact "claude-version" PASS "$(claude --version 2>/dev/null | head -n 1)" ;;
  esac
  guard_env
  guard_settings
  ask_token

  if [ -n "$DRY_RUN" ]; then
    say "dry run: guards and prompts done, no calls made"
    fact "dry-run" PASS "guards and token prompt only"
    return 0
  fi

  DEADLINE=$(($(date +%s) + TOTAL_SECONDS))
  step_control
  step_observed_control
  step_swap
  if ! grep -q '^swap|PASS' "$FACTS_FILE"; then
    # The swap call failed: tier A is decided, and the remaining prompts would
    # cost the owner more subscription traffic to learn nothing new.
    say "the swap call failed, so the remaining steps are skipped (they would spend prompts for nothing)"
  elif [ "$(date +%s)" -lt "$DEADLINE" ]; then
    step_streaming
    step_drops
    step_timeout
  else
    fact "budget" FAIL "the run reached its ${TOTAL_SECONDS}s budget; the last three steps did not run"
  fi

  local rc=0
  tier_a || rc=$?
  return "$rc"
}

trap finish EXIT
trap 'owner "interrupted"; exit 130' INT TERM
main "$@"
