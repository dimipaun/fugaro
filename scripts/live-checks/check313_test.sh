#!/usr/bin/env bash
# Tests for scripts/live-checks/check313.sh, against a fake `fugaro`, a fake
# `curl` and a fake daemon on PATH: nothing here reaches the network beyond
# loopback, there is no cloud project and no credential, and no live step of the
# check is run.
#
# The fake is what makes this check testable at all, because the check's whole
# subject is a real process, a real lock and a real listener: the fake daemon
# this test starts is a python process that binds 127.0.0.1:0, takes an exclusive
# flock on `<id>.lock` and writes `<id>.json` at 0600 in a 0700 run directory,
# exactly as endpoint.Hold does, and its listener answers the check's handshake
# from the token and the nonce the file carries. The fake `fugaro`'s `graph` is
# endpoint.Probe: it handshakes, and removes the file only where nothing answers
# and nobody holds the lock -- with modes that break that in the ways the check
# exists to catch (a reader that keeps the dead file, one that trusts the pid,
# one that kills the pid, one that fails).
#
# The daemon's token and nonce are MINTED per case and never written as a literal
# in this file: the cases that hold the token out of the block read the value the
# fake daemon wrote, so nothing here is secret-shaped and the assertion is about
# a value rather than about a string this test chose.
#
# Usage: bash scripts/live-checks/check313_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check313.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
KILL_ON_EXIT=""
cleanup_all() {
  local pid
  for pid in $KILL_ON_EXIT; do
    kill -9 "$pid" 2>/dev/null || true
  done
  if [ -n "${CHECK313_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|id|belong|xtrace|endpoint):' | sed 's/^/ ... last run: /')"
}
expect_pass_row() { # key what
  expect_contains "$out" "FACT $1: PASS" "$2"
}
expect_fail_row() { # key what
  expect_contains "$out" "FACT $1: FAIL" "$2"
}
expect_info_row() { # key what
  expect_contains "$out" "FACT $1: INFO" "$2"
}

# The orchestrator every case reads: a project's name, and never the owner's.
ID=workspace-alpha-1234
# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(id belong xtrace state-dir registry-file registry-fields handshake
  lock-held daemon-pid graph-live crash crash-half graph-after-crash dead-file
  lock-free pid-reuse reboot-nonce endpoint teardown)
# The keys that have to hold in the run where everything is right. crash-half and
# reboot-nonce are the two that carry an INFO there by design: the first says
# which reading the crash half took, the second says that no previous boot's
# nonce was carried in.
PASS_KEYS=(id belong xtrace state-dir registry-file registry-fields handshake
  lock-held daemon-pid graph-live crash graph-after-crash dead-file lock-free
  pid-reuse endpoint teardown)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# The fake fugaro for check313.sh's test: every call is recorded in the case's
# own directory, so a case can say what the check asked of the build, and the
# `graph` read is answered by fake_graph.py, which is endpoint.Probe.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE313_DIR/fugaro.calls"
exec python3 "$FAKE313_FAKES/fake_graph.py" "$@"
FUGARO_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# The fake curl for check313.sh's test. It records the argv and the credential
# the check handed over in its `--config` file, and then answers the request out
# of fake_curl.py -- so a case says how the token travelled, and the check's own
# output is never consulted for it.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE313_DIR/curl.calls"
conf=""
want=0
for a in "$@"; do
  if [ "$want" = 1 ]; then
    conf=$a
    want=0
    continue
  fi
  case "$a" in
  --config) want=1 ;;
  esac
done
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >"$FAKE313_DIR/curl.auth" 2>/dev/null || true
  # The file's mode, so a case can say it is 0600 and not merely present. Read
  # with python3 rather than stat, whose -c is GNU's: this test runs on both.
  python3 -c 'import os, sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$FAKE313_DIR/curl.conf.mode" 2>/dev/null || printf 'unknown\n' >"$FAKE313_DIR/curl.conf.mode"
fi
exec python3 "$FAKE313_FAKES/fake_curl.py" "$@"
CURL_EOF

  cat >"$1/bin/fake_curl.py" <<'FAKECURL_EOF'
#!/usr/bin/env python3
"""The handshake the check makes, answered from a loopback listener.

It is a fake curl rather than the real one so that what the check handed over is
recorded -- the argv, and the Authorization header of the `--config` file, which
must never be a command-line header -- while the request itself is a real HTTP
request to the fake daemon, so the check's handshake reading is a reading of an
answer and not of a stub.
"""
import sys
import urllib.error
import urllib.request

args = sys.argv[1:]
out = dump = conf = ""
timeout = 10
url = ""
i = 0
while i < len(args):
    a = args[i]
    if a == "-o":
        out = args[i + 1]
        i += 2
    elif a == "-D":
        dump = args[i + 1]
        i += 2
    elif a == "--config":
        conf = args[i + 1]
        i += 2
    elif a == "--max-time":
        timeout = float(args[i + 1])
        i += 2
    elif a == "-w":
        i += 2
    elif a.startswith("-"):
        i += 1
    else:
        url = a
        i += 1

auth = ""
if conf:
    with open(conf, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line.lower().startswith("header") and "authorization:" in line.lower():
                auth = line.split(":", 1)[1].strip().strip('"')

request = urllib.request.Request(url)
if auth:
    request.add_header("Authorization", auth)
try:
    with urllib.request.urlopen(request, timeout=timeout) as response:
        status, headers, body = response.status, response.headers, response.read()
except urllib.error.HTTPError as e:
    status, headers, body = e.code, e.headers, e.read()
except Exception as e:  # noqa: BLE001 - any failure is this fake's exit 7
    print("fake curl: %s" % e, file=sys.stderr)
    raise SystemExit(7)

if out:
    with open(out, "wb") as fh:
        fh.write(body)
if dump:
    with open(dump, "w", encoding="utf-8") as fh:
        fh.write("HTTP/1.1 %d %s\n" % (status, "OK" if status == 200 else "Error"))
        for k, v in headers.items():
            fh.write("%s: %s\n" % (k, v))
print("%d" % status, end="")
FAKECURL_EOF

  cat >"$1/bin/fake_daemon.py" <<'DAEMON_EOF'
#!/usr/bin/env python3
"""The daemon check313.sh's test starts, in the daemon's own shape.

The check's subject is a real process with a real lock and a real listener, so
this fake writes exactly the three things endpoint.Hold writes -- the 0600
registry file in the 0700 run directory, the lock it holds for its life, and a
loopback listener that answers the handshake with the id and the nonce the file
carries -- and then keeps running until it is killed, which is what the check's
crash half does to it.
"""
import argparse
import fcntl
import http.server
import json
import os
import secrets
import sys
import time

parser = argparse.ArgumentParser()
parser.add_argument("--dir", required=True, help="the run directory the file goes in")
parser.add_argument("--log", required=True, help="where the case reads back what was minted")
parser.add_argument("--id", required=True)
parser.add_argument("--pid-claim", type=int, default=0, help="a pid to write into the file instead of this process's")
parser.add_argument("--addr-claim", default="", help="an address to write into the file instead of the one bound")
parser.add_argument("--no-lock", action="store_true")
parser.add_argument("--token-other", action="store_true", help="answer with a token the file does not carry")
parser.add_argument("--nonce-other", action="store_true", help="answer with a nonce the file does not carry")
parser.add_argument("--nonce-same-as-token", action="store_true")
# The two words `orchestrator run` on this fake's command line are there on
# purpose: the check signals a pid only when ps says the command line is the
# daemon's, and this fake has to be the daemon for those cases to measure
# anything.
args, _ = parser.parse_known_args()


def record(name, value):
    with open(os.path.join(args.log, name), "w", encoding="utf-8") as fh:
        fh.write(value)


token = secrets.token_hex(32)
nonce = token if args.nonce_same_as_token else secrets.token_hex(32)
answered_token = secrets.token_hex(32) if args.token_other else token
answered_nonce = secrets.token_hex(32) if args.nonce_other else nonce
record("token.txt", token)
record("nonce.txt", nonce)

os.makedirs(args.dir, mode=0o700, exist_ok=True)
if not args.no_lock:
    fd = os.open(os.path.join(args.dir, args.id + ".lock"), os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, status, body, extra=None):
        self.send_response(status)
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/state":
            self.reply(404, b'{"error":"no such route"}')
            return
        if self.headers.get("Authorization", "") != "Bearer " + answered_token:
            self.reply(401, b'{"error":"unauthorized"}')
            return
        self.reply(200, json.dumps({"id": args.id, "started_at": started_at}).encode(), {
            "X-Fugaro-Orchestrator": args.id,
            "X-Fugaro-Nonce": answered_nonce,
            "X-Fugaro-Tick": "1",
        })


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
addr = "%s:%d" % (server.server_address[0], server.server_address[1])
record("addr.txt", addr)
started_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
doc = {
    "id": args.id,
    "pid": args.pid_claim or os.getpid(),
    "addr": args.addr_claim or addr,
    "token": token,
    "nonce": nonce,
    "started_at": started_at,
}
tmp = os.path.join(args.dir, ".daemon.tmp")
wfd = os.open(tmp, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(wfd, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
os.rename(tmp, os.path.join(args.dir, args.id + ".json"))
record("daemon.pid", str(os.getpid()))
server.serve_forever()
DAEMON_EOF

  cat >"$1/bin/fake_graph.py" <<'GRAPH_EOF'
#!/usr/bin/env python3
"""The reader, `fugaro graph --source local:<id>`: endpoint.Probe.

The check's two removal facts hang on this read, so the fake is the real rule --
handshake, then the lock -- with a mode per way of breaking it, which is what
lets a case say the check notices a reader that keeps a dead file or one that
believes a pid.
"""
import fcntl
import json
import os
import secrets
import sys
import urllib.error
import urllib.request

argv = sys.argv[1:]
if not argv or argv[0] != "graph":
    print("the fake fugaro answers graph only: %r" % (argv,), file=sys.stderr)
    raise SystemExit(64)
mode = os.environ.get("FAKE313_MODE", "reader")
if mode == "fail":
    print("the fake fugaro cannot reach the graph store", file=sys.stderr)
    raise SystemExit(5)

run_dir = os.environ["FAKE313_RUN_DIR"]
id = os.environ["FAKE313_ID"]
path = os.path.join(run_dir, id + ".json")
lock = os.path.join(run_dir, id + ".lock")


def remove():
    try:
        os.remove(path)
    except FileNotFoundError:
        pass


def alive(pid):
    try:
        os.kill(int(pid), 0)
        return True
    except OSError:
        return False


def handshake(doc):
    request = urllib.request.Request(
        "http://%s/state" % doc.get("addr", ""),
        headers={"Authorization": "Bearer %s" % doc.get("token", "")})
    try:
        with urllib.request.urlopen(request, timeout=2) as response:
            return (response.headers.get("X-Fugaro-Orchestrator") == doc.get("id")
                    and response.headers.get("X-Fugaro-Nonce") == doc.get("nonce"))
    except Exception:  # noqa: BLE001 - nothing answering is the answer this fake wants
        return False


def lock_free():
    try:
        fd = os.open(lock, os.O_RDWR)
    except OSError:
        return True
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return True
    except OSError:
        return False


def write(doc):
    tmp = os.path.join(run_dir, ".graph.tmp")
    fd = os.open(tmp, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        json.dump(doc, fh)
    os.rename(tmp, path)


state = "node"
try:
    with open(path, encoding="utf-8") as fh:
        doc = json.load(fh)
except FileNotFoundError:
    doc = None
if doc is not None:
    if mode == "supervise":
        doc["pid"] = int(os.environ["FAKE313_SUPERVISE_PID"])
        doc["nonce"] = secrets.token_hex(32)
        write(doc)
        state = "endpoint"
    elif handshake(doc):
        state = "endpoint"
    elif mode == "keep":
        pass
    elif mode in ("pid-trust", "pid-kill"):
        if alive(doc.get("pid") or 0):
            state = "endpoint"
            if mode == "pid-kill":
                try:
                    os.kill(int(doc["pid"]), 9)
                except OSError:
                    pass
        else:
            remove()
    elif lock_free():
        remove()

print(json.dumps({
    "version": 1,
    "id": id,
    "sources": [{"source": "local:" + id, "state": state, "age_seconds": 0}],
    "nodes": [{"id": "task-1", "state": "done"}],
}))
GRAPH_EOF

  chmod +x "$1/bin/fugaro" "$1/bin/curl"
}

# ------------------------------------------------------------------ the harness
CASE=""
out=""
err=""
status=0
DAEMON=0

setup_case() { # setup_case NAME
  CASE=$work/$1
  rm -rf "$CASE"
  mkdir -p "$CASE/bin" "$CASE/log" "$CASE/home" "$CASE/tmp" "$CASE/state/fugaro/run"
  chmod 700 "$CASE/tmp" "$CASE/state" "$CASE/state/fugaro" "$CASE/state/fugaro/run"
  write_fakes "$CASE"
  : >"$CASE/log/fugaro.calls"
  : >"$CASE/log/curl.calls"
  out=""
  err=""
  status=0
  DAEMON=0
  MODE=reader
  SUPERVISE_PID=""
}

wait_for() { # wait_for FILE TENTHS
  local i=0
  while [ "$i" -lt "$2" ]; do
    [ -s "$1" ] && return 0
    sleep 0.1
    i=$((i + 1))
  done
  return 1
}

start_daemon() { # start_daemon [extra args...]
  python3 "$CASE/bin/fake_daemon.py" orchestrator run \
    --dir "$CASE/state/fugaro/run" --log "$CASE/log" --id "$ID" "$@" \
    >>"$CASE/log/daemon.out" 2>&1 &
  DAEMON=$!
  KILL_ON_EXIT="$KILL_ON_EXIT $DAEMON"
  # The registry file is what this wait is for, and not the daemon's pid record:
  # a case starts a second daemon under another id, and the first one's record is
  # already there by the time the second one starts.
  wait_for "$CASE/state/fugaro/run/$ID.json" 100 ||
    fail "the fake daemon did not come up: $(cat "$CASE/log/daemon.out" 2>/dev/null)"
}

start_bystander() { # start_bystander -- a live process that is not the daemon
  # Its output goes to /dev/null on purpose: a background process left holding a
  # caller's pipe keeps a command substitution open until it exits.
  sleep 60 >/dev/null 2>&1 &
  BYSTANDER=$!
  KILL_ON_EXIT="$KILL_ON_EXIT $BYSTANDER"
}

run313() { # run313 [args...]
  status=0
  out=""
  err=""
  : >"$CASE/log/fugaro.calls"
  : >"$CASE/log/curl.calls"
  local bash_args=(bash)
  [ -z "${WITH_XTRACE:-}" ] || bash_args=(bash -x)
  out=$(env PATH="$CASE/bin:$PATH" HOME="$CASE/home" \
    XDG_STATE_HOME="$CASE/state" TMPDIR="$CASE/tmp" \
    FAKE313_DIR="$CASE/log" FAKE313_FAKES="$CASE/bin" \
    FAKE313_RUN_DIR="$CASE/state/fugaro/run" FAKE313_ID="$ID" FAKE313_MODE="$MODE" \
    ${SUPERVISE_PID:+FAKE313_SUPERVISE_PID=$SUPERVISE_PID} \
    CHECK313_FUGARO="$CASE/bin/fugaro" CHECK313_CURL_SECONDS=5 \
    CHECK313_WAIT_SECONDS=10 CHECK313_DECOY_SECONDS=30 \
    "${bash_args[@]}" "$script" ${1+"$@"} 2>"$CASE/log/err") || status=$?
  err=$(cat "$CASE/log/err" 2>/dev/null || true)
  unset WITH_XTRACE
}

# The token the fake daemon minted this case, read from where the daemon wrote it
# rather than written here as a literal: what the block must never carry is a
# value, and this is where that value is known.
the_token() { cat "$CASE/log/token.txt" 2>/dev/null || true; }
the_nonce() { cat "$CASE/log/nonce.txt" 2>/dev/null || true; }
expect_the_token_never_leaks() { # what
  local token
  token=$(the_token)
  [ -n "$token" ] || return 0
  expect_absent "$out" "$token" "$1: the block never carries the endpoint's token"
  expect_absent "$err" "$token" "$1: the run's own output never carries the endpoint's token"
}

# --------------------------------------------------------------------- cases
test_the_pinned_set_passes() {
  setup_case pinned
  start_daemon
  run313 --id "$ID"
  expect_status "$status" 0 "everything right"
  local key
  for key in "${PASS_KEYS[@]}"; do
    expect_pass_row "$key" "everything right"
  done
  key=crash-half
  expect_info_row crash-half "everything right: the crash half says which reading it took"
  expect_info_row reboot-nonce "everything right: no previous boot's nonce is compared"
  expect_contains "$out" "CHECK 313 RESULT END (exit 0)" "everything right"
  expect_contains "$out" "$(the_nonce)" "the block prints the nonce, which --phase reboot needs on the command line"
  expect_contains "$out" "naming a LIVE pid" "the pid-reuse row says the file named a live pid"
  expect_the_token_never_leaks "everything right"
  # What the run did to the machine: the daemon is gone, the file the crash half
  # read is gone with it, and the token's 0600 file went with the scratch dir.
  if kill -0 "$DAEMON" 2>/dev/null; then fail "everything right: the daemon is still running after the crash half"; fi
  if [ -e "$CASE/state/fugaro/run/$ID.json" ]; then fail "everything right: the registry file is still there"; fi
  if [ -e "$CASE/state/fugaro/run/.check313-plant.tmp" ]; then fail "everything right: the check left its planting temp file behind"; fi
  if [ -n "$(ls -d "$CASE/tmp"/chk313.* 2>/dev/null)" ]; then fail "everything right: the scratch directory was not removed"; fi
  expect_absent "$err" "$(the_token)" "everything right: the token is in no line the run printed"
  # Both reads happened, and the crash read is the one that removed the file.
  expect_contains "$(cat "$CASE/log/fugaro.calls")" "graph --source local:$ID --json" "everything right: the scope was read"
  expect_contains "$(cat "$CASE/log/curl.calls")" "--config" "everything right: the handshake carried its credential in a file"
  expect_contains "$(cat "$CASE/log/curl.conf.mode")" "600" "everything right: the credential file was 0600"
}

test_a_registry_file_that_is_not_0600_fails() {
  setup_case filemode
  start_daemon
  chmod 644 "$CASE/state/fugaro/run/$ID.json"
  run313 --id "$ID"
  expect_fail_row registry-file "a file another user could read"
  expect_contains "$out" "is mode 0644" "the row says which mode it read"
  expect_pass_row handshake "the rest of the reading still happened"
  expect_status "$status" 1 "a file another user could read"
}

test_a_run_directory_that_is_not_0700_fails() {
  setup_case dirmode
  start_daemon
  chmod 755 "$CASE/state/fugaro/run"
  run313 --id "$ID"
  expect_fail_row state-dir "a directory another user can list"
  expect_contains "$out" "is mode 0755" "the row says which mode it read"
  expect_status "$status" 1 "a directory another user can list"
}

test_a_nonce_that_is_the_token_fails() {
  setup_case noncetoken
  start_daemon --nonce-same-as-token
  run313 --id "$ID"
  expect_fail_row registry-fields "a nonce that proves no identity of its own"
  expect_contains "$out" "its nonce is the token" "the row says what is wrong with the nonce"
  expect_status "$status" 1 "a nonce that is the token"
}

test_a_daemon_whose_listener_is_gone_fails() {
  setup_case deadaddr
  start_daemon --addr-claim 127.0.0.1:1
  run313 --id "$ID"
  expect_fail_row handshake "a file whose address nothing answers"
  expect_pass_row lock-held "the lock is what says the file is still owned"
  expect_status "$status" 1 "a file whose address nothing answers"
}

test_a_lock_that_is_not_held_fails() {
  setup_case nolock
  start_daemon --no-lock
  run313 --id "$ID"
  expect_fail_row lock-held "a file whose lock was never taken"
  expect_status "$status" 1 "a file whose lock was never taken"
}

test_a_handshake_answered_with_another_nonce_fails() {
  setup_case othernonce
  start_daemon --nonce-other
  run313 --id "$ID"
  expect_fail_row handshake "an answer that does not carry the file's nonce"
  expect_status "$status" 1 "an answer that does not carry the file's nonce"
}

test_a_handshake_answered_with_another_token_is_refused() {
  setup_case othertoken
  start_daemon --token-other
  run313 --id "$ID"
  expect_fail_row handshake "a request the endpoint refused"
  expect_contains "$out" "HTTP 401" "the row says what the endpoint answered"
  expect_status "$status" 1 "a request the endpoint refused"
}

test_a_file_left_by_a_daemon_that_is_gone_stops() {
  setup_case leftover
  start_daemon
  kill -9 "$DAEMON" 2>/dev/null || true
  wait "$DAEMON" 2>/dev/null || true
  run313 --id "$ID"
  expect_fail_row handshake "a daemon that is gone leaves a file nothing answers for"
  expect_fail_row lock-held "the kernel dropped the lock with the process"
  expect_fail_row daemon-pid "a pid that is not running is never signalled"
  expect_status "$status" 2 "a file left by a daemon that is gone"
  if [ ! -e "$CASE/state/fugaro/run/$ID.json" ]; then
    fail "a file left by a daemon that is gone: the check removed a file it was reading rather than leaving it to the reader"
  fi
}

test_a_pid_that_is_not_the_daemon_is_never_signalled() {
  setup_case strangerpid
  start_bystander
  start_daemon --pid-claim "$BYSTANDER"
  run313 --id "$ID"
  expect_fail_row daemon-pid "a file naming a process that is not the daemon"
  expect_contains "$out" "never signalled" "the row says the pid is not touched"
  expect_status "$status" 2 "a file naming a process that is not the daemon"
  if kill -0 "$BYSTANDER" 2>/dev/null; then :; else fail "a file naming a process that is not the daemon: that process was killed"; fi
  if kill -0 "$DAEMON" 2>/dev/null; then :; else fail "a file naming a process that is not the daemon: the daemon was killed before the run stopped"; fi
}

test_no_registry_file_at_all_stops() {
  setup_case nodaemon
  run313 --id "$ID"
  expect_fail_row registry-file "no daemon on this machine"
  expect_contains "$out" "does not exist" "the row says the file is not there"
  expect_status "$status" 2 "no daemon on this machine"
  if [ -s "$CASE/log/fugaro.calls" ]; then fail "no daemon on this machine: the check read a scope anyway"; fi
}

test_two_registry_files_stop_without_an_id() {
  setup_case twofiles
  start_daemon
  local first=$ID
  ID=workspace-beta-5678
  start_daemon
  ID=$first
  run313
  expect_fail_row id "two daemons and no --id"
  expect_contains "$out" "holds 2 registry files" "the row says how many it found"
  expect_status "$status" 2 "two daemons and no --id"
  if [ -e "$CASE/state/fugaro/run/.check313-plant.tmp" ]; then fail "two daemons and no --id: the check wrote something anyway"; fi
}

test_belong_is_refused() {
  setup_case belong
  start_daemon
  run313 --id belong
  expect_fail_row belong "the owner's own installation"
  expect_contains "$out" "never point" "the row says why"
  expect_status "$status" 2 "the owner's own installation"
  if [ -s "$CASE/log/fugaro.calls" ]; then fail "the owner's own installation: the check made a read anyway"; fi
  if kill -0 "$DAEMON" 2>/dev/null; then :; else fail "the owner's own installation: the daemon was killed"; fi
}

test_an_id_no_local_scope_names_is_refused() {
  setup_case badid
  run313 --id has_an_underscore
  expect_fail_row id "an id no local: scope names"
  expect_contains "$out" "not an id a local: scope names" "the row says why"
  expect_status "$status" 2 "an id no local: scope names"
}

test_a_dry_run_makes_no_call() {
  setup_case dryrun
  start_daemon
  run313 --id "$ID" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_pass_row dry-run "a dry run"
  expect_pass_row id "a dry run judges the id it was given"
  expect_absent "$out" "FACT handshake" "a dry run measures nothing, so its block carries no reading"
  expect_contains "$err" "dry run: NOTHING below was done" "the plan says nothing was done"
  expect_contains "$err" "[writes] kill -9" "the plan names the SIGKILL it would make"
  if [ -s "$CASE/log/fugaro.calls" ]; then fail "a dry run: the check read a scope"; fi
  if [ -s "$CASE/log/curl.calls" ]; then fail "a dry run: the check made the handshake"; fi
  if [ ! -e "$CASE/state/fugaro/run/$ID.json" ]; then fail "a dry run: the registry file was removed"; fi
  if kill -0 "$DAEMON" 2>/dev/null; then :; else fail "a dry run: the daemon was killed"; fi
}

test_xtrace_is_refused() {
  setup_case xtrace
  start_daemon
  WITH_XTRACE=1 run313 --id "$ID"
  expect_fail_row xtrace "a shell that arrived tracing"
  expect_status "$status" 2 "a shell that arrived tracing"
  if [ -s "$CASE/log/curl.calls" ]; then fail "a shell that arrived tracing: the handshake was made anyway"; fi
}

test_usage() {
  setup_case usage
  run313 --id "$ID" --phase sideways
  expect_status "$status" 2 "--phase sideways"
  expect_contains "$err" "--phase takes live or reboot" "--phase sideways"
  run313 --id "$ID" --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument" "an unknown argument"
  run313 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check313.sh" "--help prints the usage"
  expect_absent "$out" "CHECK 313 RESULT" "--help is not a run"
}

test_a_reader_that_keeps_the_dead_file_fails() {
  setup_case keeps
  start_daemon
  MODE=keep
  run313 --id "$ID"
  expect_fail_row dead-file "a reader that keeps the file it judged dead"
  expect_contains "$out" "STILL THERE" "the row says the file survived"
  expect_fail_row pid-reuse "the planted file survived too"
  expect_status "$status" 1 "a reader that keeps the file it judged dead"
  if [ -e "$CASE/state/fugaro/run/$ID.json" ]; then
    fail "a reader that keeps the file it judged dead: the check left the file it planted behind"
  fi
}

test_a_reader_that_trusts_the_pid_is_caught() {
  setup_case pidtrust
  start_daemon
  MODE=pid-trust
  run313 --id "$ID"
  expect_pass_row dead-file "a reader that trusts the pid removes the dead one"
  expect_fail_row pid-reuse "a reader that trusts the pid keeps the live one"
  expect_contains "$out" "the reader that read it had to remove it" "the row says what the reader failed to do"
  expect_status "$status" 1 "a reader that trusts the pid"
}

test_a_reader_that_kills_the_pid_is_caught() {
  setup_case pidkill
  start_daemon
  MODE=pid-kill
  run313 --id "$ID"
  expect_fail_row pid-reuse "a reader that signals the pid in the file"
  expect_contains "$out" "is gone: something signalled a process" "the row says the process was killed"
  expect_status "$status" 1 "a reader that signals the pid in the file"
}

test_a_read_that_fails_fails_the_live_row() {
  setup_case readfails
  start_daemon
  MODE=fail
  run313 --id "$ID"
  expect_fail_row graph-live "a read that does not answer while the endpoint is live"
  expect_contains "$out" "exited 5" "the row says what the read exited with"
  expect_status "$status" 1 "a read that does not answer"
}

test_a_daemon_that_comes_back_is_not_measured() {
  setup_case supervised
  start_daemon
  MODE=supervise
  start_bystander
  SUPERVISE_PID=$BYSTANDER
  run313 --id "$ID"
  expect_status "$status" 2 "a daemon a supervisor started again"
  expect_contains "$out" "FACT crash-half: INFO" "a daemon a supervisor started again"
  expect_contains "$out" "belongs to a NEW daemon" "the row says the file is another daemon's"
  expect_info_row endpoint "the live half holds and the crash half was not measured"
  expect_info_row dead-file "the removal was not measured"
  expect_info_row pid-reuse "nothing was planted over a live daemon's file"
  expect_contains "$out" "launchctl bootout" "the row names the one thing that fixes it"
  if [ -e "$CASE/state/fugaro/run/.check313-plant.tmp" ]; then
    fail "a daemon a supervisor started again: the check planted a file anyway"
  fi
  local nonce_now
  nonce_now=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["nonce"])' "$CASE/state/fugaro/run/$ID.json")
  [ "$nonce_now" != "$(the_nonce)" ] \
    || fail "a daemon a supervisor started again: the check overwrote the new daemon's file with the dead one's"
}

test_the_reboot_phase_compares_the_nonce() {
  setup_case rebootdiffers
  start_daemon
  # The value passed as the previous boot's is the token: another 32-byte hex
  # value from the same case, so nothing here is a literal and it cannot equal
  # this boot's nonce.
  run313 --id "$ID" --phase reboot --previous-nonce "$(the_token)"
  expect_pass_row reboot-nonce "a nonce minted per start"
  expect_contains "$out" "differs from the previous boot's" "the row says the two differ"
  expect_status "$status" 0 "a nonce minted per start"
}

test_a_nonce_reused_across_a_reboot_fails() {
  setup_case rebootreuse
  start_daemon
  run313 --id "$ID" --phase reboot --previous-nonce "$(the_nonce)"
  expect_fail_row reboot-nonce "a nonce the same across two starts"
  expect_contains "$out" "SAME nonce" "the row says the two are the same"
  expect_status "$status" 1 "a nonce the same across two starts"
}

test_the_reboot_phase_without_a_previous_nonce_is_not_measured() {
  setup_case rebootnonce
  start_daemon
  run313 --id "$ID" --phase reboot
  expect_info_row reboot-nonce "a reboot phase with nothing to compare"
  expect_contains "$out" "no --previous-nonce was given" "the row says why nothing was measured"
  expect_status "$status" 0 "a reboot phase with nothing to compare"
}

# ---------------------------------------------------------------- self-tests
# A case that asserts something about a run is only worth having if the thing it
# asserts about can fail: these two rewrite a copy of the script into the state
# the assertion is for, and require the assertion to catch it.
rewrite_script() { # rewrite_script DEST OLD NEW
  OLD=$2 NEW=$3 python3 - "$script" "$1" <<'PY'
import os
import sys

src, dst = sys.argv[1], sys.argv[2]
old, new = os.environ["OLD"], os.environ["NEW"]
with open(src, encoding="utf-8") as fh:
    text = fh.read()
hit = text.count(old)
if hit != 1:
    raise SystemExit("the line this self-test edits is not in the check exactly once (%d): it has moved" % hit)
with open(dst, "w", encoding="utf-8") as fh:
    fh.write(text.replace(old, new))
PY
  chmod +x "$1"
}

test_the_token_assertion_catches_an_echoing_script() {
  setup_case echoes
  start_daemon
  local copy=$CASE/check313-echoing.sh
  # The one line this puts in the copy is a fact no reader of the block should
  # ever see: the token, in the check's own output.
  rewrite_script "$copy" \
    "$(printf '  read_registry\n%s\n' '  judge_registry')" \
    "$(printf '  read_registry\n  say "self-test token: $(cat "$TOKEN_FILE")"\n%s\n' '  judge_registry')"
  local keep=$script
  script=$copy
  run313 --id "$ID"
  script=$keep
  local together="$out$err"
  expect_contains "$together" "$(the_token)" "the echoing copy prints the token, which is what this self-test needs"
  # The assertion is run in a subshell whose `fail` only counts, so that the
  # failure this self-test is looking for does not become a failure of the run.
  local caught
  caught=$(
    local count=0
    out="$together"
    err=""
    fail() { count=$((count + 1)); }
    expect_the_token_never_leaks "detector"
    printf '%s' "$count"
  )
  [ "${caught:-0}" -ge 1 ] \
    || fail "detector: an echoing script raised none of the assertion that holds the token out of the block, so that assertion is vacuous"
}

test_the_pid_guard_is_not_vacuous() {
  setup_case guard
  start_bystander
  start_daemon --pid-claim "$BYSTANDER"
  local copy=$CASE/check313-unguarded.sh
  rewrite_script "$copy" '  *orchestrator*run*)' '  *)'
  local keep=$script
  script=$copy
  run313 --id "$ID"
  script=$keep
  expect_pass_row daemon-pid "the unguarded copy accepts any pid"
  if kill -0 "$BYSTANDER" 2>/dev/null; then
    fail "the unguarded copy left the process it should have killed alone, so the guard this test measures is not the one that saves it"
  fi
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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

t test_the_pinned_set_passes
t test_a_registry_file_that_is_not_0600_fails
t test_a_run_directory_that_is_not_0700_fails
t test_a_nonce_that_is_the_token_fails
t test_a_daemon_whose_listener_is_gone_fails
t test_a_lock_that_is_not_held_fails
t test_a_handshake_answered_with_another_nonce_fails
t test_a_handshake_answered_with_another_token_is_refused
t test_a_file_left_by_a_daemon_that_is_gone_stops
t test_a_pid_that_is_not_the_daemon_is_never_signalled
t test_no_registry_file_at_all_stops
t test_two_registry_files_stop_without_an_id
t test_belong_is_refused
t test_an_id_no_local_scope_names_is_refused
t test_a_dry_run_makes_no_call
t test_xtrace_is_refused
t test_usage
t test_a_reader_that_keeps_the_dead_file_fails
t test_a_reader_that_trusts_the_pid_is_caught
t test_a_reader_that_kills_the_pid_is_caught
t test_a_read_that_fails_fails_the_live_row
t test_a_daemon_that_comes_back_is_not_measured
t test_the_reboot_phase_compares_the_nonce
t test_a_nonce_reused_across_a_reboot_fails
t test_the_reboot_phase_without_a_previous_nonce_is_not_measured
t test_the_token_assertion_catches_an_echoing_script
t test_the_pid_guard_is_not_vacuous

if [ "$failures" -ne 0 ]; then
  echo "check313_test: $failures failure(s)" >&2
  exit 1
fi
echo "check313_test: all tests passed" >&2
