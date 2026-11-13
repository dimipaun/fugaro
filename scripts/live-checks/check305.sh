#!/usr/bin/env bash
# Check 305 — the MCP state group served, as ONE script YOU run against your own
# installation with the front door ON (never belong).
#
# It settles the live half of O16b of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; D2020 (a) and D2021 (a)
# answered 2026-11-10), and the register's row and section are
# docs/gcp-live-checklist.md, "Check 305": a real MCP client, signed in against
# the front door's own issuer, reads one project's orchestrator node through
# /mcp — the tools the door lists, get_state and get_events beside the CLI's own
# documents, the duration cut on the database's clock, a tasks-only token
# refused both, and the one role the read is granted through.
#
# Usage:      bash scripts/live-checks/check305.sh --installation <name> \
#               [--door <url>] [--agent <id>] [--firebase-project <id>] \
#               [--repo <checkout>] [--save <dir>] [--dry-run]
#             <name> is the Fugaro project name the local config in force carries
#             (`fugaro doctor --json`'s project.name), and the answer is held to
#             it: a name that is not the one doctor reports is a STOP before
#             anything is run.
#             --door is the front door's own URL. Without it the check finds the
#             service the way a client would: it asks gcloud for the project's
#             Cloud Run URLs and takes the one whose protected-resource metadata
#             answers. A front door that answers 404 on every path is the
#             installation that never turned the switch on, and the run stops.
#             --agent is the orchestrator to read (default: the installation's
#             own id, `fugaro orchestrator ls --json`).
#             --firebase-project is the project the role is declared in. It
#             defaults to the installation's own project: one Fugaro project has
#             one Firebase project (D3), and the check says which one it used.
#             --save <dir> keeps the documents this check read (mode 0700) so you
#             can paste them back with the block. It is a directory of YOURS:
#             nothing removes it.
# Two people: no. One person, with the device this installation paired with you
#             (the console or the phone) in their hand: each sign-in is the two
#             acts the front door's own page asks — you read the flow's number off
#             the page and approve that number on the device, then answer Allow.
# Needs:      bash, mktemp, timeout, python3 and fugaro on PATH, gcloud
#             authenticated as YOU (the role and the policy are read through
#             it), a browser, and the paired device. Nothing is installed and
#             nothing is fetched; a missing tool stops the run with one line
#             naming it.
# Where:      YOUR OWN installation with the front door on, and never `belong`:
#             the name is refused, whether you typed it as the installation or
#             doctor answers it. An installation that never asked for the front
#             door serves a 404 on every path of it and has nothing to read —
#             the register's own precondition.
# Costs:      nothing billable and no model call: the check reads the door, the
#             node and two gcloud reads, and starts nothing. The state group is
#             two read tools; its write half is served by nothing.
# Runs:       by YOU, in one terminal, with a browser and the paired device. No
#             agent runs any step of it.
# Two sign-ins: the check is a real OAuth client of the installation's own issuer
#             (it registers itself, uses PKCE with a loopback redirect and
#             exchanges the code — the shape `fugaro coordinator login` runs), so
#             it needs a token of its own TWICE: one carrying `state:read` (the
#             tools, the two documents, the two refusals there) and one carrying
#             only `tasks:read tasks:write` (the refusal the register's fourth
#             reading asks for). Each is the page's number and the device's
#             approval, and the second one is not asked for until the first has
#             read the tool list.
# Cleanup:    it writes NOTHING on the installation: no node, no state, no
#             work item, no role and no grant. What it makes at the door is two
#             client registrations (which expire on their own store's lifetime,
#             M12) and two token families, and both families are REVOKED at the
#             end (`POST /oauth/revoke`) with the fact that says so. The two
#             tokens live in the scratch directory (mode 0700, token files mode
#             0600), are never printed and are removed with it. --save is a
#             directory of yours and the check only copies documents into it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 305 RESULT,
#             to copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is missing,
#             the front door is off, the installation's door serves no state
#             group).
#
# THE RELEASE AND THE CAPABILITY ARE CHECKED FIRST. The state group is O16b's,
# and O16b is in release 0.24.0 "Uakari" (docs/releases/v0.24.0.md), which is
# written and not yet cut: `fugaro version` names what this build is, and the
# door's own answer is what settles it — a `tools/list` that carries neither
# `get_state` nor `get_events` is a door serving a build before that release, and
# the run stops with that fact rather than reading a node through tools that are
# not there. The version alone is not the gate: a build of main reports the
# release behind it and still carries O16b's work, which is why the fact says how
# old the build is and the tool list decides.
#
# One reading the register asks for cannot be produced live, and the fact says so
# rather than claiming it: "one another loop wrote is refused rather than answered
# as this project's state" needs a node under `orchestrator/<id>` whose document
# carries another role (internal/orchestrator/store.go's notThisNode), and no
# second loop writes one today — RoleCoordinator is a constant with no writer in
# the tree. So the check reads the two refusals that ARE there (an id that is not
# a name is refused; an id this project's node does not hold is answered as this
# project's own empty state and never as another project's document) and reports
# the third as the reading it did not make.
#
# No trace: `bash -x` prints every document this check reads and every line of
# its client, so guard_xtrace refuses a shell that arrived tracing. No token, no
# code and no verifier is ever printed: the client holds them in the scratch
# directory and prints a URL, a number and the answers it read.
set -Eeuo pipefail

XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
USAGE="bash scripts/live-checks/check305.sh --installation <name> [--door <url>] [--dry-run]"
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as a working state group for a run that read
# nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the state group" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 305 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  need bash "every child this check starts runs on it"
  need mktemp
  need timeout "every fugaro, gcloud and client call this check makes is bounded, so one that never returns cannot hang the check"
  need python3 "the check's own MCP client, and every document it reads, run on it"
  need fugaro "doctor names the installation and the two documents are read through it"
  need gcloud "the front door's URL and the one role the read is granted through are read with it, as YOU"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk305-mine"
SCRATCH_REMOVED=no
PRINTED=""
HELPED=no
INSTALLATION=""
DOOR=""
DOOR_NAMED=no
AGENT=""
AGENT_NAMED=no
FB_PROJECT=""
FB_NAMED=no
REPO=""
SAVE=""
DRY_RUN=no
PROJECT=""
REGION=""
VERSION_LINE=""
RELEASE="0.24.0"
RELEASE_NAME=Uakari
ISSUER=""
PROTOCOL=""
TOOL_NAMES=""
STATE_TOKEN=""
TASKS_TOKEN=""
STATE_SCOPES=""
SIGNIN_WAIT=${CHECK305_SIGNIN_WAIT:-300}

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk305.XXXXXXXX") || die "Check 305 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 305 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  write_client
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# write_client writes the check's own MCP client. It is a real client of the
# installation's own issuer and of /mcp: it registers itself, runs the
# authorization-code flow with PKCE and a loopback redirect, and keeps the tokens
# it gets in files only this scratch directory holds. Nothing it can print is a
# token: the lines it answers are a URL, the tool names and the answers it read.
write_client() {
  cat >"$SCRATCH/mcp.py" <<'MCP_PY'
#!/usr/bin/env python3
"""The MCP client Check 305 signs in with and reads the front door through.

Subcommands, every value of the check's own:
  discover --door URL --out FILE
  signin   --door URL --scope S --token-file FILE --wait SECS
  list     --door URL --token-file FILE --out FILE
  call     --door URL --token-file FILE --tool NAME [--id ID] [--since D] --out FILE
  revoke   --door URL --token-file FILE

stdout carries one fact per line (RESOURCE, ISSUER, AUTHORIZE, SIGNED-IN,
PROTOCOL, TOOLS, CALL-OK, CALL-REFUSED, HTTP, CHALLENGE, REVOKED); a failure is
a line on stderr and a non-zero exit. No token, code or verifier is printed.
"""
import base64
import hashlib
import http.server
import json
import os
import secrets
import sys
import threading
import urllib.error
import urllib.parse
import urllib.request

UA = "fugaro-live-check-305"
TIMEOUT = 30


def _request(url, data=None, content_type=None, token=None, timeout=TIMEOUT):
    headers = {"User-Agent": UA, "Accept": "application/json"}
    if content_type:
        headers["Content-Type"] = content_type
    if token:
        headers["Authorization"] = "Bearer " + token
    method = "POST" if data is not None else "GET"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read(), r.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read(), e.headers
    except Exception as e:
        fail("the front door at %s could not be reached: %s" % (url, e))
    return 0, b"", {}


def fail(message):
    print("ERROR " + message, file=sys.stderr)
    sys.exit(4)


def header(headers, name):
    for key, value in headers.items():
        if key.lower() == name.lower():
            return value
    return ""


def body_object(body):
    try:
        return json.loads(body.decode("utf-8", "replace"))
    except Exception:
        return {}


def post_json(url, obj, token=None, content_type="application/json"):
    return _request(url, data=json.dumps(obj).encode("utf-8"), content_type=content_type, token=token)


def post_form(url, form, token=None):
    data = urllib.parse.urlencode(form).encode("utf-8")
    return _request(url, data=data, content_type="application/x-www-form-urlencoded", token=token)


def discover(door):
    base = door.rstrip("/")
    status, body, _ = _request(base + "/.well-known/oauth-protected-resource")
    if status == 404:
        return None
    if status != 200:
        fail("the protected-resource metadata at %s answered HTTP %d" % (base, status))
    doc = body_object(body)
    servers = doc.get("authorization_servers") or []
    issuer = servers[0] if servers else (doc.get("resource") or base)
    status, body, _ = _request(issuer.rstrip("/") + "/.well-known/oauth-authorization-server")
    if status != 200:
        fail("the authorization-server metadata at %s answered HTTP %d" % (issuer, status))
    return {"resource": doc.get("resource") or "", "issuer": issuer, "meta": body_object(body)}


def endpoint(meta, name):
    url = meta.get(name)
    if not url:
        fail("the authorization-server metadata names no %s" % name)
    return url


class Loopback:
    """The client's own redirect listener: one GET, and whatever it carried."""

    def __init__(self):
        self.query = {}
        self.done = threading.Event()
        self.server = None

    def start(self):
        query, done = self.query, self.done

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                parsed = urllib.parse.urlparse(self.path)
                query.update({k: v[0] for k, v in urllib.parse.parse_qs(parsed.query).items()})
                page = b"<html><body><p>The check has this flow's answer. You can close this window.</p></body></html>"
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Content-Length", str(len(page)))
                self.end_headers()
                self.wfile.write(page)
                done.set()

            def log_message(self, *_args):
                return

        self.server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self.server.server_address[1]

    def wait(self, seconds):
        if not self.done.wait(seconds):
            return None
        self.server.shutdown()
        return dict(self.query)


def token_doc(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except Exception as e:
        fail("the token file %s could not be read: %s" % (path, e))
    return {}


def write_token(path, doc):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        json.dump(doc, fh, sort_keys=True)
        fh.write("\n")


def cmd_discover(args):
    found = discover(args["door"])
    if found is None:
        print("DOOR-OFF")
        return 3
    with open(args["out"], "w", encoding="utf-8") as fh:
        json.dump({"resource": found["resource"], "issuer": found["issuer"], "meta": found["meta"]}, fh, indent=2, sort_keys=True)
    print("RESOURCE " + (found["resource"] or ""))
    print("ISSUER " + found["issuer"])
    return 0


def cmd_signin(args):
    found = discover(args["door"])
    if found is None:
        fail("the front door serves a 404 on every path, so it is off for this installation")
    meta = found["meta"]
    scope = args["scope"]
    loop = Loopback()
    port = loop.start()
    redirect = "http://127.0.0.1:%d/callback" % port
    status, body, _ = post_json(
        endpoint(meta, "registration_endpoint"),
        {"client_name": "Fugaro live check 305", "redirect_uris": [redirect]},
    )
    if status not in (200, 201):
        fail("the registration endpoint answered HTTP %d: %s" % (status, body_object(body).get("error_description", "")))
    client_id = body_object(body).get("client_id") or ""
    if not client_id:
        fail("the registration answered no client_id")
    verifier = base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b"=").decode("ascii")
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode("ascii")).digest()).rstrip(b"=").decode("ascii")
    state = secrets.token_urlsafe(16)
    query = urllib.parse.urlencode(
        {
            "response_type": "code",
            "client_id": client_id,
            "redirect_uri": redirect,
            "scope": scope,
            "state": state,
            "code_challenge": challenge,
            "code_challenge_method": "S256",
        }
    )
    print("AUTHORIZE " + endpoint(meta, "authorization_endpoint") + "?" + query, flush=True)
    answer = loop.wait(float(args["wait"]))
    if answer is None:
        fail("no answer reached the loopback redirect within %ss: the flow's number is approved on the device this installation paired with you, and Allow on the page answers the client" % args["wait"])
    if answer.get("error"):
        fail("the door refused the authorization: %s %s" % (answer.get("error"), answer.get("error_description", "")))
    if answer.get("state") != state:
        fail("the redirect carried a state this client did not send")
    code = answer.get("code")
    if not code:
        fail("the redirect carried no code")
    status, body, _ = post_form(
        endpoint(meta, "token_endpoint"),
        {
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": redirect,
            "client_id": client_id,
            "code_verifier": verifier,
        },
    )
    if status != 200:
        fail("the token endpoint answered HTTP %d: %s" % (status, body_object(body).get("error_description", "")))
    doc = body_object(body)
    if not doc.get("access_token"):
        fail("the token endpoint answered no access_token")
    write_token(
        args["token_file"],
        {
            "access": doc["access_token"],
            "refresh": doc.get("refresh_token", ""),
            "client_id": client_id,
            "issuer": found["issuer"],
            "scope": doc.get("scope", scope),
        },
    )
    print("SIGNED-IN")
    return 0


def cmd_list(args):
    token = token_doc(args["token_file"])
    door = args["door"].rstrip("/")
    status, body, _ = post_json(
        door + "/mcp",
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2026-07-28", "capabilities": {}, "clientInfo": {"name": UA, "version": "1"}}},
        token=token.get("access", ""),
    )
    if status == 200:
        result = body_object(body).get("result") or {}
        if result.get("protocolVersion"):
            print("PROTOCOL " + str(result["protocolVersion"]))
    status, body, headers = post_json(door + "/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, token=token.get("access", ""))
    if status != 200:
        print("HTTP %d" % status)
        challenge = header(headers, "WWW-Authenticate")
        if challenge:
            print("CHALLENGE " + challenge)
        print(body_object(body).get("error_description", ""), file=sys.stderr)
        return 4
    doc = body_object(body)
    if doc.get("error"):
        print("RPC-ERROR %s" % (doc["error"] or {}).get("code", ""))
        print(str((doc["error"] or {}).get("message", "")), file=sys.stderr)
        return 4
    with open(args["out"], "w", encoding="utf-8") as fh:
        json.dump(doc, fh, indent=2, sort_keys=True)
    tools = (doc.get("result") or {}).get("tools") or []
    print("TOOLS %d" % len(tools))
    for tool in tools:
        if isinstance(tool, dict) and tool.get("name"):
            print("TOOL " + str(tool["name"]))
    return 0


def cmd_call(args):
    token = token_doc(args["token_file"])
    arguments = {}
    if args.get("id"):
        arguments["id"] = args["id"]
    if args.get("since"):
        arguments["since"] = args["since"]
    status, body, headers = post_json(
        args["door"].rstrip("/") + "/mcp",
        {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": args["tool"], "arguments": arguments}},
        token=token.get("access", ""),
    )
    if status != 200:
        print("HTTP %d" % status)
        challenge = header(headers, "WWW-Authenticate")
        if challenge:
            print("CHALLENGE " + challenge)
        print(body_object(body).get("error_description", ""), file=sys.stderr)
        return 4
    doc = body_object(body)
    if doc.get("error"):
        print("RPC-ERROR %s" % (doc["error"] or {}).get("code", ""))
        print(str((doc["error"] or {}).get("message", "")), file=sys.stderr)
        return 4
    result = doc.get("result") or {}
    text = ""
    for part in result.get("content") or []:
        if isinstance(part, dict) and part.get("type") == "text":
            text = part.get("text") or ""
            break
    with open(args["out"], "w", encoding="utf-8") as fh:
        fh.write(text)
        if text and not text.endswith("\n"):
            fh.write("\n")
    print("CALL-REFUSED" if result.get("isError") else "CALL-OK")
    return 0


def cmd_revoke(args):
    token = token_doc(args["token_file"])
    found = discover(args["door"])
    if found is None:
        print("NOT-REVOKED the front door is off")
        return 0
    value = token.get("refresh") or token.get("access") or ""
    if not value:
        print("NOT-REVOKED no token was minted")
        return 0
    status, body, _ = post_form(
        endpoint(found["meta"], "revocation_endpoint"),
        {"client_id": token.get("client_id", ""), "token": value},
    )
    if status == 200:
        print("REVOKED")
        return 0
    print("NOT-REVOKED HTTP %d %s" % (status, body_object(body).get("error_description", "")))
    return 0


def parse(argv):
    if len(argv) < 2:
        fail("a subcommand is required")
    command = argv[1]
    args = {}
    rest = argv[2:]
    while rest:
        flag = rest[0]
        if not flag.startswith("--") or len(rest) < 2:
            fail("unexpected argument %r" % (flag,))
        args[flag[2:].replace("-", "_")] = rest[1]
        rest = rest[2:]
    commands = {"discover": cmd_discover, "signin": cmd_signin, "list": cmd_list, "call": cmd_call, "revoke": cmd_revoke}
    if command not in commands:
        fail("unknown subcommand %r" % (command,))
    return commands[command](args)


if __name__ == "__main__":
    try:
        sys.exit(parse(sys.argv))
    except KeyboardInterrupt:
        sys.exit(3)
MCP_PY
  chmod 700 "$SCRATCH/mcp.py"
}

cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly: a block that read as a
  # finished reading would offer the rows that were never read as a verdict about
  # the state group.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if ! printf '%s\n' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|INFO|nothing of this check's was written on the installation, so there is nothing there to undo; the two token families it minted at the door were not revoked, because the run did not reach the door again — they expire on their own"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"scratch|PASS|the scratch directory is gone, and the two tokens it held (mode 0600) with it"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself: it holds the two tokens this check minted"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which prints the documents this check reads and the lines its client answers with"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a token, a document or a value this check read"
}

# guard_belong: `belong` is the owner's own installation wherever it is named —
# the name you typed, or the one doctor answers.
guard_belong() { # guard_belong NAME WHERE
  case "$1" in
  belong | belong-*)
    case "$2" in
    typed) fact installation FAIL "the installation named on the command line is $1, which is the owner's own: Check 305 reads a state node through the front door, so it is not run against belong" ;;
    *) fact installation FAIL "the local config in force answers $1, which is the owner's own installation" ;;
    esac
    stop "Check 305 never runs against 'belong'. Name another installation: $USAGE"
    ;;
  esac
}

# guard_agent_id: the id names a node under orchestrator/ and is put into a path
# by the CLI read and into a tool argument here, so it is held to the characters
# an id can carry (internal/orchestrator's own checkAgentID) rather than passed
# on.
guard_agent_id() { # guard_agent_id WHAT VALUE
  case "$1:$2" in
  *:"" | *:*[!A-Za-z0-9._-]*) die "$1 is not an orchestrator id: an id is letters, digits, '.', '_' and '-' only, because it names the node under orchestrator/. Nothing was read, and the value is not printed here — an id is put into a path and a tool argument, never echoed" ;;
  esac
}

parse_args() {
  [ $# -gt 0 ] || die "--installation is required. usage: $USAGE"
  while [ $# -gt 0 ]; do
    case "$1" in
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      printf '  --installation <name>       the Fugaro project the local config in force carries (never belong)\n'
      printf '  --door <url>                the front door URL (default: found through gcloud, by its metadata)\n'
      printf '  --agent <id>                the orchestrator to read (default: the installation\x27s own id)\n'
      printf '  --firebase-project <id>     the project the role is declared in (default: the installation\x27s own)\n'
      printf '  --repo <checkout>           read the tree half of the catalogue reading from this checkout\n'
      printf '  --save <dir>                keep the documents this check read, for pasting back\n'
      printf '  --dry-run                   print the plan and change nothing\n'
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --door)
      [ $# -ge 2 ] || die "--door needs a URL. usage: $USAGE"
      DOOR=$2
      DOOR_NAMED=yes
      shift 2
      ;;
    --agent)
      [ $# -ge 2 ] || die "--agent needs an orchestrator id. usage: $USAGE"
      AGENT=$2
      AGENT_NAMED=yes
      shift 2
      ;;
    --firebase-project)
      [ $# -ge 2 ] || die "--firebase-project needs a project id. usage: $USAGE"
      FB_PROJECT=$2
      FB_NAMED=yes
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a checkout path. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --save)
      [ $# -ge 2 ] || die "--save needs a directory. usage: $USAGE"
      SAVE=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  if [ "$AGENT_NAMED" = yes ]; then guard_agent_id "--agent" "$AGENT"; fi
  [ -n "$INSTALLATION" ] || die "--installation is required, and it must not be belong. usage: $USAGE"
  case "$DOOR" in
  "") ;;
  http://127.0.0.1:* | http://localhost:* | https://*) ;;
  *)
    die "--door is not a URL this check calls: the front door is an https address (a loopback http one is accepted so the check can be driven against a fake), and nothing else is called. Nothing was read"
    ;;
  esac
  case "$SIGNIN_WAIT" in
  "" | *[!0-9]*) die "CHECK305_SIGNIN_WAIT is not a number of seconds" ;;
  esac
  [ "$SIGNIN_WAIT" -ge 1 ] || die "CHECK305_SIGNIN_WAIT has to be at least one second"
}

# --------------------------------------------------------------- the commands
# fg TIMEOUT ARGS... — every fugaro call this check makes is bounded, so a call
# that never returns ends the step instead of hanging the check.
fg() {
  local secs=$1
  shift
  timeout --kill-after=10 "$secs" "$@"
}

# gc TIMEOUT ARGS... — the same bound for gcloud, which is the other network the
# check touches (two reads, both as YOU).
gc() {
  local secs=$1
  shift
  timeout --kill-after=10 "$secs" "$@"
}

# client TIMEOUT ARGS... — the check's own MCP client, bounded like everything
# else: a door that never answers must not hang the run.
client() {
  local secs=$1
  shift
  timeout --kill-after=10 "$secs" python3 "$SCRATCH/mcp.py" "$@"
  }

# safe_field cleans a value that came out of a document before it reaches the
# block: one line, and no character a terminal cannot take. The block is pasted
# back, and a brief or a reason can carry anything.
safe_field() {
  printf '%s' "$1" | tr -d '\000-\010\013\014\016-\037' | tr '\n\r\t' '   ' | cut -c1-200
}

# json_get FILE PYTHON-EXPRESSION — one value out of a document this check read,
# with python3 because the block is composed by hand and a jq that is not
# installed would be a check that silently reads nothing.
json_get() { # json_get FILE PYTHON-EXPRESSION
  python3 - "$1" "$2" <<'PY' 2>/dev/null || true
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
try:
    value = eval(sys.argv[2], {"doc": doc, "len": len, "sorted": sorted, "str": str, "int": int, "next": next})
except Exception:
    sys.exit(0)
if value is None:
    sys.exit(0)
if isinstance(value, (dict, list)):
    print(json.dumps(value, sort_keys=True))
else:
    print(value)
PY
}

# version_ge HAVE WANT — 0 when HAVE is a version at or past WANT, 1 when it is
# older, 2 when this build's own answer is not a version this check can compare
# (a build from source prints something else, and the tool list is what settles
# the reading then).
version_ge() { # version_ge HAVE WANT
  python3 - "$1" "$2" <<'PY'
import re, sys
def parts(v):
    m = re.match(r"^v?(\d+)\.(\d+)\.(\d+)", v.strip())
    return None if not m else tuple(int(g) for g in m.groups())
have, want = parts(sys.argv[1]), parts(sys.argv[2])
if have is None:
    sys.exit(2)
sys.exit(0 if have >= want else 1)
PY
}

# doc_diff CLI_FILE TOOL_FILE [DROP…] — how the CLI's document and the door's
# answer differ, member for member, which is the register's own reading ("one
# node, two surfaces"). Prints "same", or one line per differing member; the
# members named as DROP… are the surface's own (the CLI's `since` and its
# `cached_at`), which the register's reading does not hold the door to.
doc_diff() { # doc_diff CLI_FILE TOOL_FILE [DROP…]
  python3 - "$@" <<'PY'
import json, sys
def load(path):
    try:
        doc = json.load(open(path, encoding="utf-8"))
    except Exception as e:
        print("unreadable|%s|%s" % (path, e))
        sys.exit(0)
    return doc if isinstance(doc, dict) else {"document": doc}
cli, tool = load(sys.argv[1]), load(sys.argv[2])
drop = set(sys.argv[3:])
cli = {k: v for k, v in cli.items() if k not in drop}
tool = {k: v for k, v in tool.items() if k not in drop}
diffs = []
for key in sorted(set(cli) | set(tool)):
    if cli.get(key) != tool.get(key):
        diffs.append("%s: the CLI says %s and the door says %s" % (
            key, json.dumps(cli.get(key), sort_keys=True)[:160], json.dumps(tool.get(key), sort_keys=True)[:160]))
if not diffs:
    print("same")
for line in diffs:
    print(line)
PY
}

# events_shape FILE — what the log's cleaning looks like on the way out: one
# plain line per entry's free text, cut at the log's own cap (300 runes). A
# cursor is not read for here: it is a member the CLI's own document does not
# carry, so doc_diff catches it by name (D1229). Prints "clean <n>" or one line
# per entry that is not.
events_shape() { # events_shape FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print("unreadable: %s" % e)
    sys.exit(0)
entries = doc.get("events") or []
problems = []
for e in entries:
    text = (e or {}).get("text") or ""
    if "\n" in text or "\r" in text or "\t" in text:
        problems.append("an entry's text is not one line")
    if len(text) > 300:
        problems.append("an entry's text is longer than the log's own cap (300)")
for p in problems[:3]:
    print(p)
if not problems:
    print("clean %d" % len(entries))
PY
}

# ------------------------------------------------------------------ the steps
step_installation() {
  if ! fg 60 fugaro doctor --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err"; then
    sed -n '1,5p' "$SCRATCH/doctor.err" >&4 || true
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local name
  name=$(json_get "$SCRATCH/doctor.json" 'doc.get("project", {}).get("name")')
  [ -n "$name" ] || stop "fugaro doctor --json named no project, so this check cannot tell which installation it is looking at"
  guard_belong "$name" doctor
  if [ "$name" != "$INSTALLATION" ]; then
    fact installation FAIL "the installation named on the command line is $INSTALLATION and the local config in force answers $name"
    stop "the name you typed and the config in force are two different installations: run with --installation $name, or point FUGARO_PROJECT/FUGARO_CONFIG at the one you meant"
  fi
  PROJECT=$(json_get "$SCRATCH/doctor.json" 'doc.get("project", {}).get("gcp_project")')
  REGION=$(json_get "$SCRATCH/doctor.json" 'doc.get("project", {}).get("region")')
  if [ "$FB_NAMED" = no ]; then
    FB_PROJECT=${PROJECT:-}
  fi
  [ -n "$FB_PROJECT" ] || stop "this check could not tell which Firebase project the state role is declared in: doctor named no gcp_project, so pass --firebase-project <the installation's Firebase project>"
  fact installation PASS "the local config in force is installation $name (project ${PROJECT:-unknown}, region ${REGION:-unknown}), it is not belong, and the state role is read in the Firebase project $FB_PROJECT${FB_NAMED:+ as you named it}"
}

# step_version names the release that carries O16b. It does not stop on its own:
# a build of main reports the release behind it and still serves the state group,
# so the fact says what this build is and the tool list below decides.
step_version() {
  local v rc=0
  v=$(fg 30 fugaro version 2>/dev/null | head -n 1 || true)
  VERSION_LINE=${v:-unknown}
  version_ge "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0) fact version PASS "$v is at or past ${RELEASE} (\"${RELEASE_NAME}\"), the release O16b's state group is written into (docs/releases/v0.24.0.md), so this build carries the two read tools" ;;
  1) fact version INFO "$v is older than ${RELEASE} (\"${RELEASE_NAME}\"), the release O16b's state group ships in — but the release a build reports and the work it carries are two things: a build of main reports the release behind it. If the tools below are not served, this is the release you need" ;;
  2) fact version INFO "fugaro version answered '${VERSION_LINE}', which is not a version this check can compare against ${RELEASE} (\"${RELEASE_NAME}\", the release O16b's state group ships in): a build from source prints its own string. The tool list below is what settles this reading" ;;
  esac
}

# probe_paid NAME: what this build answers for a paid command name. 0 — the
# build carries the name and answers with its own help (the Subscription
# Edition); 1 — the build carries the core stub and refuses it by name; 2 —
# neither, so this build's edition cannot be read. The stub's sentence is D522's
# and only a build without the ee build tag renders it (internal/cli/extend.go).
probe_paid() { # probe_paid NAME
  local out
  out=$(fg 30 fugaro "$1" --help 2>&1 || true)
  case "$out" in
  *"Subscription Edition; this build does not include it"*) return 1 ;;
  *"Usage:"*) return 0 ;;
  esac
  return 2
}

# step_edition is a fact and not a gate. The state group is EE code
# (ee/coordinator/mcpauth/tool_state.go) and the front door is the coordinator's
# own service, but this check does not need the LOCAL build to be the paid one:
# it signs in and calls the door itself, and the two documents it compares are
# read through core commands. A local build that is not the edition is said so
# rather than turned into a stop that would refuse a readable installation.
step_edition() {
  local rc=0
  probe_paid coordinator || rc=$?
  case "$rc" in
  0)
    fact edition PASS "this is the Subscription Edition build: it carries 'coordinator', which is the front door's own command family, and the state group this check reads is that edition's (ee/coordinator/mcpauth, O16b)"
    ;;
  1)
    fact edition INFO "this is a core build: it answers 'coordinator' with D522's stub. The check still reads the door itself — it signs in with its own OAuth client — and the two documents it compares are core commands (orchestrator state/events --json), so this is said rather than treated as a stop"
    ;;
  *)
    fact edition INFO "this build answered 'coordinator' with neither its own help nor D522's stub, so its edition cannot be read here. It does not change what this check reads: the door is called by the check's own client, not by the CLI's paid surface"
    ;;
  esac
}

# step_door resolves the front door and reads its own metadata: the register's
# precondition is the installation's switch (`var.mcp_server`, D934), and an
# installation that never asked serves a 404 on every path of the door. Without
# --door the check finds the service the way a client would — it lists the
# project's Cloud Run URLs and takes the one whose protected-resource metadata
# answers — because the URL carries the project number and nothing derives it
# offline (outputs.tf).
step_door() {
  if [ "$DOOR_NAMED" = no ]; then
    if [ -z "$PROJECT" ]; then
      stop "the front door's URL cannot be found: doctor named no gcp_project, so gcloud has no project to list services in. Pass --door <url>"
    fi
    local args=() urls
    args=(run services list --project "$PROJECT" '--format=value(status.url)' --quiet)
    if [ -n "$REGION" ]; then args+=(--region "$REGION"); fi
    if ! gc 90 gcloud "${args[@]}" >"$SCRATCH/services.txt" 2>"$SCRATCH/services.err"; then
      sed -n '1,5p' "$SCRATCH/services.err" >&4 || true
      stop "gcloud could not list the project's Cloud Run services, so the front door's URL cannot be found. Run 'gcloud run services list --project $PROJECT' yourself, or pass --door <url>"
    fi
    urls=$(grep -E '^https://' "$SCRATCH/services.txt" | head -n 10 || true)
    if [ -z "$urls" ]; then
      fact door FAIL "project $PROJECT publishes no Cloud Run service URL, so this installation has no front door: the register's own precondition is the installation's switch ($(printf '%s' 'var.mcp_server, D934')), and an installation that never asked for one serves a 404 on every path of it"
      stop "the front door is OFF for this installation: no Cloud Run service answers in $PROJECT. Turn it on (the release that carries the front door's own switch writes var.mcp_server and applies it) and run this check again. Nothing was read and nothing was written"
    fi
    local url found=""
    while IFS= read -r url; do
      [ -n "$url" ] || continue
      if client 60 discover --door "$url" --out "$SCRATCH/discover.json" >"$SCRATCH/discover.txt" 2>"$SCRATCH/discover.err"; then
        case "$(head -n 1 "$SCRATCH/discover.txt")" in
        RESOURCE*) found=$url; break ;;
        esac
      fi
    done <<EOF
$urls
EOF
    if [ -z "$found" ]; then
      fact door FAIL "none of the ${PROJECT} Cloud Run services answers the front door's own metadata document (/.well-known/oauth-protected-resource), so this installation serves no front door: the register's own precondition is the switch that renders the service, its account and the door's entry onto the revision (var.mcp_server, D934)"
      stop "no front door answered in $PROJECT: either the installation never turned the switch on, or the service is not the one you meant. Name it yourself with --door <url>. Nothing was read and nothing was written"
    fi
    DOOR=$found
    cp "$SCRATCH/discover.json" "$SCRATCH/discover.used.json" 2>/dev/null || true
  else
    if ! client 60 discover --door "$DOOR" --out "$SCRATCH/discover.json" >"$SCRATCH/discover.txt" 2>"$SCRATCH/discover.err"; then
      case "$(head -n 1 "$SCRATCH/discover.txt" 2>/dev/null || true)" in
      DOOR-OFF)
        fact door FAIL "$DOOR serves a 404 on /.well-known/oauth-protected-resource, which is what an installation whose front door is OFF answers on every path of it (the register's own precondition: the switch that renders the service, its account and the door's entry onto the revision — var.mcp_server, D934)"
        stop "the front door is off at $DOOR: it serves a 404 on every path. Turn the installation's switch on and apply it, then run this check again. Nothing was read and nothing was written"
        ;;
      *)
        sed -n '1,5p' "$SCRATCH/discover.err" >&4 || true
        fact door FAIL "$DOOR did not answer as a front door: its metadata document could not be read"
        stop "the door's own metadata could not be read at $DOOR. Check the URL (it is the Cloud Run service the installation's outputs publish) and run this check again"
        ;;
      esac
    fi
    cp "$SCRATCH/discover.json" "$SCRATCH/discover.used.json" 2>/dev/null || true
  fi
  ISSUER=$(json_get "$SCRATCH/discover.used.json" 'doc.get("issuer", "")')
  [ -n "$ISSUER" ] || stop "$DOOR answered a protected-resource document that names no authorization server, so this is not a front door this check can sign in against"
  owner "the front door's issuer is $ISSUER — the token this check mints is that issuer's own, and nothing else is called"
  fact door PASS "the front door answers at $DOOR and its protected-resource metadata names its own authorization server, $ISSUER: the register's precondition is met (the installation's switch is on) and the door is not belong"
}

# step_agent picks the orchestrator the register's second and third readings are
# about: the id a person names, or the installation's own id.
step_agent() {
  if ! fg 60 fugaro orchestrator ls --json >"$SCRATCH/ls.json" 2>"$SCRATCH/ls.err"; then
    sed -n '1,5p' "$SCRATCH/ls.err" >&4 || true
    stop "fugaro orchestrator ls --json did not answer, so this run cannot name the orchestrator to read. Run it yourself: it needs your own credentials and the installation's database"
  fi
  # An orchestrator id IS the project's own name (internal/cli/orchestrator_cmd.go,
  # orchestratorID), so this installation's node is the one the register's second
  # and third readings are about, and a name typed here has to be one the listing
  # holds: a node nothing holds would compare two empty documents and read as a
  # pass, which is the reading this check exists to not make.
  if [ "$AGENT_NAMED" = no ]; then
    AGENT=$INSTALLATION
  fi
  AGENT=$(safe_field "$AGENT")
  guard_agent_id "--agent" "$AGENT"
  local listed kind holder
  listed=$(json_get "$SCRATCH/ls.json" 'any(o.get("project") == "'"$AGENT"'" for o in doc.get("orchestrators", []))')
  if [ "$listed" != "True" ]; then
    fact agent FAIL "the listing holds no orchestrator whose id is $AGENT, and an orchestrator id is the project's own name (internal/cli/orchestrator_cmd.go: the id the daemon drives is the project's name). Reading a node nothing holds would compare two empty documents and read as a pass"
    stop "this installation has no orchestrator node for $AGENT, so 'one node, two surfaces' would be read over an empty state and prove nothing. Start the installation's own daemon ('fugaro orchestrator run') at least once, or name an id the listing holds: --agent <id>"
  fi
  kind=$(json_get "$SCRATCH/ls.json" 'next((o.get("kind") for o in doc.get("orchestrators", []) if o.get("project") == "'"$AGENT"'"), "")')
  holder=$(json_get "$SCRATCH/ls.json" 'next((o.get("lease_holder") for o in doc.get("orchestrators", []) if o.get("project") == "'"$AGENT"' and o.get("lease_holder")), "")')
  fact agent PASS "the orchestrator this check reads is $AGENT — an id is the project's own name (internal/cli/orchestrator_cmd.go), and the listing holds it (kind ${kind:-unknown}${holder:+, lease held by $holder}). The CLI's own documents for it are read below and the door's answers are held to them, member for member"
}

# step_signin runs the check's own OAuth client for one scope and keeps the token
# in the scratch directory (mode 0600). The URL is printed to you WHILE the client
# waits, because the second act is yours: the page's number is approved on the
# device this installation paired with you. Nothing the client prints is a token.
step_signin() { # step_signin WHAT SCOPE TOKEN_FILE
  local what=$1 scope=$2 file=$3
  local out="$SCRATCH/signin.$what.out"
  local rc=0
  owner ""
  owner "SIGN IN ($what) — this check is a real client of the installation's own issuer: it registers itself,"
  owner "  runs the authorization-code flow with PKCE and a loopback redirect (the shape 'fugaro coordinator login' runs),"
  owner "  and keeps the token in its scratch directory (mode 0600), which is removed at the end. It waits ${SIGNIN_WAIT}s."
  client "$((SIGNIN_WAIT + 60))" signin --door "$DOOR" --scope "$scope" --token-file "$file" --wait "$SIGNIN_WAIT" 2>"$SCRATCH/signin.$what.err" |
    while IFS= read -r line; do
      printf '%s\n' "$line" >>"$out"
      case "$line" in
      AUTHORIZE\ *)
        owner ""
        owner "  OPEN THIS URL in your browser:"
        owner "    ${line#AUTHORIZE }"
        owner "  Read the flow's number off that page, approve THAT number on the device this installation paired"
        owner "  with you (the console or the phone you signed in on), then press Allow on the page."
        owner ""
        ;;
      esac
    done || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^SIGNED-IN$' "$out" 2>/dev/null; then
    sed -n '1,5p' "$SCRATCH/signin.$what.err" >&4 || true
    fact "signin-$what" FAIL "the sign-in for $scope did not finish: the client exited $rc without a token. What the door and the client said is above"
    stop "Check 305 could not mint a token of its own for '$scope': the flow needs the page's number approved on the device this installation paired with you, and Allow pressed on the page, inside ${SIGNIN_WAIT}s. Nothing on the installation was written — the client registered itself and no token was kept. Run it again when you have that device in your hand"
  fi
  fact "signin-$what" PASS "a client of the check's own registered at the door and holds a token whose scopes are '$scope' (the token is in the scratch directory, mode 0600, and no token, code or verifier is ever printed). The register's reading is made with THIS token, not with your own credential"
}

# step_tools is the capability gate, and it is the one that stops: a door that
# serves neither get_state nor get_events is a build before the release O16b
# ships in, and nothing below can be read through tools that are not there.
step_tools() {
  local out="$SCRATCH/tools.out" rc=0
  client 90 list --door "$DOOR" --token-file "$STATE_TOKEN" --out "$SCRATCH/tools.json" >"$out" 2>"$SCRATCH/tools.err" || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^TOOLS ' "$out" 2>/dev/null; then
    sed -n '1,5p' "$SCRATCH/tools.err" >&4 || true
    fact tools FAIL "the door answered no tool list to a token holding state:read (client exit $rc). The lines above name what it answered instead"
    stop "the front door did not answer tools/list to a token holding state:read, so Check 305 cannot read the state group. What it answered is printed above — a 401/403 names the scope or the token, and a JSON-RPC error names the method"
  fi
  PROTOCOL=$(sed -n 's/^PROTOCOL //p' "$out" | head -n 1)
  TOOL_NAMES=$(sed -n 's/^TOOL //p' "$out" | tr '\n' ' ' | sed 's/ $//')
  local has_state=no has_events=no has_patch=no has_spawn=no
  case " $TOOL_NAMES " in *" get_state "*) has_state=yes ;; esac
  case " $TOOL_NAMES " in *" get_events "*) has_events=yes ;; esac
  if [ "$has_state" = no ] || [ "$has_events" = no ]; then
    fact tools FAIL "the door lists no state group: 'get_state' is ${has_state} and 'get_events' is ${has_events} in the ${TOOL_NAMES:-empty} list"
    stop "Check 305 needs release ${RELEASE} (\"${RELEASE_NAME}\"): O16b's state group (ee/coordinator/mcpauth/tool_state.go, registered by ee/coordinator/cli/coordinator_serve.go) is in that release, and this door serves a build without it — the tools it lists are: ${TOOL_NAMES:-none}. This build of fugaro reports ${VERSION_LINE}. Apply the release to the installation (its image is what the door runs) and run this check again. Nothing was read beyond the tool list and nothing was written"
  fi
  case " $TOOL_NAMES " in *" apply_patch "*) has_patch=yes ;; esac
  case " $TOOL_NAMES " in *spawn*) has_spawn=yes ;; esac
  if [ "$has_patch" = yes ]; then
    fact tools FAIL "the door lists apply_patch, which D2021 (a) leaves registered by nothing: the state group's write half is catalogued under state:write so that nothing can register it under another scope, and the person's own credential stays the write (fugaro orchestrator patch, D1223)"
    return 0
  fi
  if [ "$has_spawn" = yes ]; then
    fact tools FAIL "the door lists a launch: '${TOOL_NAMES}' carries a spawn name, and T2's rule is that no task is spawned by anything this door serves"
    return 0
  fi
  local protocol=""
  if [ -n "$PROTOCOL" ]; then protocol=", and it answered the MCP revision ${PROTOCOL} to the client's initialize"; fi
  fact tools PASS "the door lists ${TOOL_NAMES}${protocol}: get_state and get_events are in it, apply_patch is NOT (D2021 (a): catalogued under state:write and registered by nothing — fugaro orchestrator patch and the person's own credential are the write, D1223) and no spawn_task is anywhere in the list (T2)"
}

# step_state is the register's second reading: the tool's document and the CLI's
# own, member for member (one contract, schemas/orchestrator-output.schema.json,
# stateOutputVersion shared by both surfaces — R3).
step_state() {
  if ! fg 60 fugaro orchestrator state --json --project "$AGENT" >"$SCRATCH/state.cli.json" 2>"$SCRATCH/state.cli.err"; then
    sed -n '1,5p' "$SCRATCH/state.cli.err" >&4 || true
    fact state FAIL "fugaro orchestrator state --json --project $AGENT did not answer, so the door's document has nothing to be held to"
    stop "the CLI could not read the node this check is about, so the register's 'one node, two surfaces' cannot be read. Fix that read first (your own credentials and the installation's database) and run this check again"
  fi
  local out="$SCRATCH/state.tool.out" rc=0
  client 90 call --door "$DOOR" --token-file "$STATE_TOKEN" --tool get_state --id "$AGENT" --out "$SCRATCH/state.tool.json" >"$out" 2>"$SCRATCH/state.tool.err" || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^CALL-OK$' "$out" 2>/dev/null; then
    sed -n '1,5p' "$SCRATCH/state.tool.err" >&4 || true
    local said
    said=$(safe_field "$(head -c 300 "$SCRATCH/state.tool.json" 2>/dev/null || true)")
    fact state FAIL "the door did not answer get_state for $AGENT (client exit $rc${said:+: $said})"
    stop "the door refused or failed the get_state call this reading is made of, and what it said is above. Nothing about the state group is concluded"
  fi
  local diff
  diff=$(doc_diff "$SCRATCH/state.cli.json" "$SCRATCH/state.tool.json" cached_at | head -n 4)
  local brief
  brief=$(safe_field "$(json_get "$SCRATCH/state.tool.json" 'doc.get("brief", "")')" | cut -c1-90)
  if [ "$diff" != "same" ]; then
    fact state FAIL "the door's get_state and the CLI's state --json are two documents, member for member: $(safe_field "$diff") — one node, two surfaces is not met. The readings after this one do not rest on it and are still made (the CLI's cached_at is the cache source's own member and is not held against the door)"
    return 0
  fi
  fact state PASS "one node, two surfaces: get_state for $AGENT answers what 'fugaro orchestrator state --json --project $AGENT' answers, member for member (version, kind, id, source, rev, schema_version, brief — the stateOutputVersion both surfaces are held to, R3). This node's own, as the door read it: revision $(json_get "$SCRATCH/state.tool.json" 'doc.get("rev")'), schema_version $(json_get "$SCRATCH/state.tool.json" 'doc.get("schema_version")'), brief '${brief}'"
}

# step_state_refusal reads what the door does with an id that is not this
# project's own, which is the register's "refused rather than answered as this
# project's state". Two of the three shapes can be produced without writing a
# node: an id that is not a name (refused by orchestrator.PathState before any
# read) and an id this project's node does not hold (answered as this project's
# own empty state, never as another project's document). The third — a node
# another LOOP wrote — needs a document carrying another role, and no second loop
# writes one today (RoleCoordinator is a constant with no writer in the tree), so
# the fact says that rather than claiming the reading.
step_state_refusal() {
  local out="$SCRATCH/refuse.out" rc=0
  client 90 call --door "$DOOR" --token-file "$STATE_TOKEN" --tool get_state --id '../not-a-name' --out "$SCRATCH/refuse.notname.json" >"$out" 2>"$SCRATCH/refuse.notname.err" || rc=$?
  if ! grep -q '^CALL-REFUSED$' "$out" 2>/dev/null; then
    fact state-refusal FAIL "get_state with an id that is not a name ('../not-a-name') was answered rather than refused (client exit $rc): the id is what orchestrator.PathState builds a path out of, and a value the database would refuse must never reach a reach"
    return 0
  fi
  local said
  said=$(safe_field "$(head -c 300 "$SCRATCH/refuse.notname.json" 2>/dev/null || true)")
  local unheld="check305-not-this-installations-loop"
  rc=0
  client 90 call --door "$DOOR" --token-file "$STATE_TOKEN" --tool get_state --id "$unheld" --out "$SCRATCH/state.unheld.json" >"$SCRATCH/unheld.out" 2>"$SCRATCH/unheld.err" || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^CALL-OK$' "$SCRATCH/unheld.out" 2>/dev/null; then
    fact state-refusal FAIL "get_state for an id this project's node does not hold ($unheld) was not answered at all (client exit $rc), so which loop's document the door answers could not be read"
    return 0
  fi
  local got_id got_rev
  got_id=$(json_get "$SCRATCH/state.unheld.json" 'doc.get("id", "")')
  got_rev=$(json_get "$SCRATCH/state.unheld.json" 'doc.get("rev")')
  if [ "$got_id" != "$unheld" ] || [ "$got_rev" != "0" ]; then
    fact state-refusal FAIL "an id this project's node does not hold was answered as '${got_id:-nothing}' at revision ${got_rev:-none}: it is not that id's own empty state, so the door answered something else as this project's state"
    return 0
  fi
  fact state-refusal PASS "an id that is not a name is refused by the door ('$said'), and an id this project's node does not hold ($unheld) is answered as this project's own empty state (id $got_id, rev $got_rev) and never as another project's or another loop's document. The third shape the register names — a node another LOOP wrote — is NOT read here: it needs an orchestrator/<id> document carrying another role (internal/orchestrator/store.go's notThisNode), and no second loop writes one today (RoleCoordinator is a constant with no writer in the tree). Reported as not read rather than claimed"
}

# step_events is the register's third reading: the log, one sort and one stamp
# (D1230), the CLI's `since` member excepted because the door cannot state the
# instant it cut at (it is handed a duration).
step_events() {
  if ! fg 60 fugaro orchestrator events --json --project "$AGENT" >"$SCRATCH/events.cli.json" 2>"$SCRATCH/events.cli.err"; then
    sed -n '1,5p' "$SCRATCH/events.cli.err" >&4 || true
    fact events FAIL "fugaro orchestrator events --json --project $AGENT did not answer, so the door's log has nothing to be held to"
    return 0
  fi
  local out="$SCRATCH/events.tool.out" rc=0
  client 90 call --door "$DOOR" --token-file "$STATE_TOKEN" --tool get_events --id "$AGENT" --out "$SCRATCH/events.tool.json" >"$out" 2>"$SCRATCH/events.tool.err" || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^CALL-OK$' "$out" 2>/dev/null; then
    sed -n '1,5p' "$SCRATCH/events.tool.err" >&4 || true
    fact events FAIL "the door did not answer get_events for $AGENT (client exit $rc)"
    return 0
  fi
  local diff shape
  diff=$(doc_diff "$SCRATCH/events.cli.json" "$SCRATCH/events.tool.json" since | head -n 4)
  shape=$(events_shape "$SCRATCH/events.tool.json" | head -n 4)
  if [ "$diff" != "same" ]; then
    fact events FAIL "the door's get_events and the CLI's events --json are two documents: $(safe_field "$diff")"
    return 0
  fi
  case "$shape" in
  "clean "*)
    fact events PASS "one log, one sort, the server's stamp's: get_events for $AGENT answers the entries 'fugaro orchestrator events --json --project $AGENT' prints, in the same order and member for member, and the door's document carries no cursor (D1229 — a client asks again with a since). The CLI's own 'since' member is deliberately not held against it: the door is handed a duration and cannot state the instant it cut at (StateReach.Events). ${shape#clean } entries, each entry's free text one plain line, cut at the log's own cap (300) — the cleaning the register names"
    ;;
  *)
    fact events FAIL "the door's log is not the shape the register reads: $(safe_field "$shape")"
    ;;
  esac
}

# step_events_cut reads the duration half of the same reading: the cut is the
# database's own clock's (D1230), so the CLI and the door have to answer the same
# entries for the same duration.
step_events_cut() {
  if ! fg 60 fugaro orchestrator events --json --project "$AGENT" --since 1h >"$SCRATCH/events.cut.cli.json" 2>"$SCRATCH/events.cut.cli.err"; then
    sed -n '1,5p' "$SCRATCH/events.cut.cli.err" >&4 || true
    fact events-cut FAIL "fugaro orchestrator events --json --project $AGENT --since 1h did not answer, so the door's cut has nothing to be held to"
    return 0
  fi
  local out="$SCRATCH/events.cut.tool.out" rc=0
  client 90 call --door "$DOOR" --token-file "$STATE_TOKEN" --tool get_events --id "$AGENT" --since 1h --out "$SCRATCH/events.cut.tool.json" >"$out" 2>"$SCRATCH/events.cut.tool.err" || rc=$?
  if [ "$rc" -ne 0 ] || ! grep -q '^CALL-OK$' "$out" 2>/dev/null; then
    sed -n '1,5p' "$SCRATCH/events.cut.tool.err" >&4 || true
    fact events-cut FAIL "the door did not answer get_events with a duration for $AGENT (client exit $rc)"
    return 0
  fi
  local diff cut
  diff=$(doc_diff "$SCRATCH/events.cut.cli.json" "$SCRATCH/events.cut.tool.json" since | head -n 4)
  cut=$(json_get "$SCRATCH/events.cut.cli.json" 'doc.get("since", "")')
  if [ "$diff" != "same" ]; then
    fact events-cut FAIL "the same duration cuts the two surfaces differently: $(safe_field "$diff")"
    return 0
  fi
  fact events-cut PASS "since=1h cuts one log one way: the door answers exactly the entries 'fugaro orchestrator events --json --project $AGENT --since 1h' answers, for a duration and not a stamp — the cut is taken inside orchestrator.ReadAudit on the database's own clock (D1230), which is the instant the CLI prints as since=${cut:-none} and which the door cannot carry (StateReach.Events is handed a duration and answers entries). There is no cursor (D1229): a client asks again with a since"
}

# step_tasks_token is the register's fourth reading, and it is what the second
# sign-in is for: the vocabulary's default pair is not a state reader, so a token
# holding only it must be refused both tools — by the scope, not by the identity.
step_tasks_token() {
  local refusals=0 detail="" out rc name
  for name in get_state get_events; do
    out="$SCRATCH/tasks.$name.out"
    rc=0
    client 90 call --door "$DOOR" --token-file "$TASKS_TOKEN" --tool "$name" --id "$AGENT" --out "$SCRATCH/tasks.$name.json" >"$out" 2>"$SCRATCH/tasks.$name.err" || rc=$?
    if grep -q '^HTTP 403$' "$out" 2>/dev/null && grep -q 'insufficient_scope' "$out" 2>/dev/null && grep -q 'state:read' "$out" 2>/dev/null; then
      refusals=$((refusals + 1))
      detail="$detail $name(403 insufficient_scope scope=\"state:read\")"
    else
      detail="$detail $name($(head -n 2 "$out" 2>/dev/null | tr '\n' ' '))"
    fi
  done
  if [ "$refusals" -eq 2 ]; then
    fact tasks-token PASS "both state tools are refused to a token holding only the vocabulary's default pair (${STATE_SCOPES:-tasks:read tasks:write}):$detail — the refusal is the scope's (RFC 6750 §3.1's insufficient_scope with the scope named), so a tasks token is never silently a state reader (F26's R1: a scope is held only when it was asked for by name)"
    return 0
  fi
  fact tasks-token FAIL "a token holding only the task scopes was not refused both state tools:$detail — a state scope is held only when it was asked for by name, and a tasks:write token must never be one"
}

# step_role is the register's fifth reading: the one role the read is granted
# through, and the one binding it has. Realtime Database IAM has no path-level
# scope (F27), so this is the grant that reaches the WHOLE database, and the fact
# below says so as the register does.
step_role() {
  local rc=0
  gc 60 gcloud iam roles describe fugaroStateRead --project "$FB_PROJECT" --format=json >"$SCRATCH/role.json" 2>"$SCRATCH/role.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    sed -n '1,5p' "$SCRATCH/role.err" >&4 || true
    fact role FAIL "gcloud could not describe the role fugaroStateRead in $FB_PROJECT (exit $rc): the role is declared by the installation's own terraform (modules/installation/state_read.tf) and arrives with the front door's opt-in"
    return 0
  fi
  local perms
  perms=$(json_get "$SCRATCH/role.json" 'doc.get("includedPermissions")')
  case "$perms" in
  '["firebasedatabase.instances.get"]') ;;
  *)
    fact role FAIL "the role fugaroStateRead in $FB_PROJECT holds ${perms:-nothing}, and the register's reading is exactly [\"firebasedatabase.instances.get\"]: firebasedatabase.instances.update (the write) is deliberately NOT bound, because the state group's writes stay with fugaro orchestrator patch and the person's own credential (D1223, D1241, D2021)"
    return 0
    ;;
  esac
  rc=0
  gc 90 gcloud projects get-iam-policy "$FB_PROJECT" --format=json >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    sed -n '1,5p' "$SCRATCH/policy.err" >&4 || true
    fact role FAIL "the role holds exactly firebasedatabase.instances.get, and the policy of $FB_PROJECT could not be read back (exit $rc), so the binding was not read"
    return 0
  fi
  local members count
  members=$(python3 - "$SCRATCH/policy.json" "$FB_PROJECT" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
project = sys.argv[2]
wanted = {"roles/fugaroStateRead", "projects/%s/roles/fugaroStateRead" % project}
out = []
for b in doc.get("bindings") or []:
    if (b or {}).get("role") in wanted:
        for m in b.get("members") or []:
            out.append(m)
print(" ".join(sorted(out)))
PY
)
  count=$(printf '%s' "$members" | wc -w | tr -d ' ')
  case "$count:$members" in
  1:serviceAccount:*)
    local domain=${members#*@}
    domain=${domain%.iam.gserviceaccount.com}
    fact role PASS "the role fugaroStateRead in $FB_PROJECT holds ${perms} — firebasedatabase.instances.get and nothing else, with no instances.update (the write is not bound at all) — and its one binding names one member, ${members}, a service account of ${domain} and nobody else: no operator, no group, no allUsers and no actAs on it"
    ;;
  0:)
    fact role FAIL "no binding in $FB_PROJECT's IAM policy names the role fugaroStateRead: the role is declared and the state group's read is not granted through it, so a turned-on front door would serve get_state and read nothing"
    ;;
  *)
    fact role FAIL "the role fugaroStateRead in $FB_PROJECT names ${count} members (${members:-none}), and the register's reading is the front door's own account and nothing else"
    ;;
  esac
}

# step_database_reach is the register's own cost, said rather than read: the
# reading this check just made is a read of the whole database.
step_database_reach() {
  fact database-reach INFO "how far this one grant reaches is the reconciliation's U11 and no step here settles it: Realtime Database IAM has no path-level scope (F27), so firebasedatabase.instances.get on $FB_PROJECT is a read of the WHOLE database — pairing/, mailbox/, config/, spend/, runs/ and the rest of the rules' top-level nodes — and what that exposes node by node, and whether any of it sits in the clear, is D2020's answer's own cost. Only the front door's account holds it, and the reach the state tools make of it is the two paths StateReach names"
}

# step_repo reads the tree half of the catalogue reading: apply_patch is
# catalogued under state:write and registered by nothing.
step_repo() {
  case "$REPO" in
  "") fact catalogue INFO "not read from the tree: pass --repo <checkout> to read the half that says apply_patch is catalogued under state:write and registered by nothing (ee/coordinator/mcpauth/tools.go and tool_state.go). The live half is above: the door's own list carries no apply_patch" ;;
  *)
    if [ ! -f "$REPO/ee/coordinator/mcpauth/tools.go" ] || [ ! -f "$REPO/ee/coordinator/mcpauth/tool_state.go" ]; then
      fact catalogue FAIL "--repo $REPO does not hold ee/coordinator/mcpauth/tools.go and tool_state.go, so the catalogue this reading names is not there"
      return 0
    fi
    local catalogued registered
    catalogued=$(grep -c '"apply_patch"' "$REPO/ee/coordinator/mcpauth/tools.go" 2>/dev/null || true)
    registered=$(grep -c '"apply_patch"' "$REPO/ee/coordinator/mcpauth/tool_state.go" 2>/dev/null || true)
    if [ "${catalogued:-0}" -lt 1 ]; then
      fact catalogue FAIL "ee/coordinator/mcpauth/tools.go does not catalogue apply_patch, so the write half the register's first reading is about is not where it says"
      return 0
    fi
    if [ "${registered:-0}" -gt 0 ]; then
      fact catalogue FAIL "ee/coordinator/mcpauth/tool_state.go names apply_patch, and the register's reading is that RegisterStateTools registers get_state and get_events and the third name by nothing (D2021 (a))"
      return 0
    fi
    fact catalogue PASS "the tree half of the first reading, from $REPO: ee/coordinator/mcpauth/tools.go catalogues apply_patch (under state:write, so nothing can register it under another scope — D936's shape) and ee/coordinator/mcpauth/tool_state.go, which registers the two read tools, names it nowhere. The live half is the door's own list, which carries no apply_patch"
    ;;
  esac
}

# step_save keeps the documents you paste back. It is a directory of YOURS: the
# check writes into it and removes nothing from it.
step_save() {
  case "$SAVE" in
  "") return 0 ;;
  esac
  if ! mkdir -p "$SAVE" 2>/dev/null; then
    fact save FAIL "the documents could not be kept: $SAVE could not be made"
    return 0
  fi
  chmod 700 "$SAVE" 2>/dev/null || true
  local name
  for name in tools.json state.cli.json state.tool.json events.cli.json events.tool.json events.cut.cli.json events.cut.tool.json state.unheld.json role.json policy.json discover.used.json; do
    if [ -f "$SCRATCH/$name" ]; then cp "$SCRATCH/$name" "$SAVE/$name" 2>/dev/null || true; fi
  done
  fact save PASS "the documents this check read are kept in $SAVE (mode 0700, yours — nothing removes it): the door's tool list and its two documents, the CLI's own two, the tool list's refusals' bodies, the role and the policy read-back. Paste them back with this block"
}

# step_restore revokes what the check minted at the door and says what is left.
# It writes nothing on the installation; the two client registrations expire on
# their own (M12's store lifetime).
step_restore() {
  local name out rc revoked=0 tried=0
  for name in state tasks; do
    local file="$SCRATCH/token.$name.json"
    [ -f "$file" ] || continue
    tried=$((tried + 1))
    out="$SCRATCH/revoke.$name.out"
    rc=0
    client 60 revoke --door "$DOOR" --token-file "$file" >"$out" 2>"$SCRATCH/revoke.$name.err" || rc=$?
    if grep -q '^REVOKED$' "$out" 2>/dev/null; then revoked=$((revoked + 1)); fi
  done
  if [ "$tried" -gt 0 ] && [ "$revoked" -eq "$tried" ]; then
    fact restore PASS "nothing on the installation was written — no node, no state, no work item, no role and no grant — and the two token families this check minted at the door are revoked (POST /oauth/revoke answered 200 for both; the two client registrations expire on their own, M12's store lifetime). The scratch directory holds the two tokens (mode 0600) and is removed at the end: nothing of this run is left at the door or on this machine"
    return 0
  fi
  if [ "$tried" -eq 0 ]; then
    fact restore INFO "no token was minted, so there was nothing of this check's to revoke; nothing was written on the installation"
    return 0
  fi
  fact restore FAIL "nothing was written on the installation, but the revocation answered for ${revoked} of ${tried} of this check's tokens: they expire on their own (the door's own store lifetime), and the clients it registered expire with theirs"
}

# ------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was run, no door was called, no sign-in was made, no document was read and nothing was written. A real run would run, in this order:"
  say "  [local] the guards: you typed $INSTALLATION and no belong, and no secret is ever printed"
  say "  [read] fugaro version; fugaro doctor --json — the installation you typed is held to doctor's project.name"
  say "  [read] fugaro coordinator --help — the edition, as a fact and not a gate (the check signs in and calls the door itself)"
  say "  [network, read] gcloud run services list (unless --door) and the door's own metadata documents — the register's precondition: a door that serves a 404 on every path is off, and the run stops"
  say "  [read] fugaro orchestrator ls --json — the orchestrator this check reads"
  say "  [network, read, YOURS] sign-in 1 of 2: the check's own OAuth client registers itself and prints a URL; you open it, approve the page's number on the device this installation paired with you, and press Allow. The token it gets carries state:read and is never printed"
  say "  [network, read] POST /mcp tools/list — the capability gate: no get_state and no get_events is a door serving a build before release ${RELEASE} (\"${RELEASE_NAME}\"), and the run stops there"
  say "  [read] fugaro orchestrator state --json --project <id>, and the door's get_state — member for member"
  say "  [network, read] get_state for an id that is not a name (refused) and for an id this project's node does not hold (this project's own empty state)"
  say "  [read] fugaro orchestrator events --json --project <id> and --since 1h, and the door's get_events — one log, one sort, one cut, no cursor"
  say "  [network, read, YOURS] sign-in 2 of 2: the same flow for a token holding only tasks:read tasks:write"
  say "  [network, read] get_state and get_events with that token — both refused by the scope"
  say "  [network, read] gcloud iam roles describe fugaroStateRead and gcloud projects get-iam-policy — the one permission and the one member"
  say "  [read, optional] --repo <checkout> — apply_patch catalogued under state:write and registered by nothing"
  say "and its Restore would run:"
  say "  [network, write] POST /oauth/revoke for both of the check's tokens — the two families it minted are ended"
  say "  [local] the scratch directory (and the two tokens in it) is removed; --save is yours and is left alone"
  say "Nothing was created, read, written or spent: this dry run called no door, minted no token and touched no node."
}

# ------------------------------------------------------------------- the block
BLOCK_KEYS=(phase installation version edition door agent signin-state tools state state-refusal events events-cut signin-tasks tasks-token role database-reach catalogue save restore xtrace)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 305 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 305 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  owner "interrupted: printing what was read and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
main() {
  parse_args "$@"
  need_tools

  scratch_up
  guard_xtrace
  guard_belong "$INSTALLATION" typed

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    fact phase PASS "dry run: the guards and the plan only"
    fact dry-run PASS "guards and the plan only: no door was called, no token was minted, no document was read and nothing was written"
    REACHED_END=yes
    BLOCK_KEYS=(phase installation dry-run xtrace)
    return 0
  fi

  fact phase PASS "check 305: the MCP state group, read through your installation's front door by the check's own signed-in client — two sign-ins and six readings"
  step_installation
  step_version
  step_edition
  step_door
  step_agent

  STATE_TOKEN="$SCRATCH/token.state.json"
  step_signin state "state:read" "$STATE_TOKEN"
  step_tools
  step_state
  step_state_refusal
  step_events
  step_events_cut

  TASKS_TOKEN="$SCRATCH/token.tasks.json"
  STATE_SCOPES="tasks:read tasks:write"
  step_signin tasks "$STATE_SCOPES" "$TASKS_TOKEN"
  step_tasks_token

  step_role
  step_database_reach
  step_repo
  step_save
  step_restore

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
