#!/usr/bin/env bash
# Tests for scripts/live-checks/check305.sh, against a fake front door on the
# loopback and a fake fugaro: nothing here reaches the network, there is no
# cloud state, no real installation, no token and no model call. The fake door
# speaks the surface the check signs in against (its own metadata documents,
# RFC 7591 registration, the authorization-code flow with PKCE, the token and
# revocation endpoints, /mcp with initialize, tools/list, tools/call and the two
# refusals), so every row of the block is driven by an answer this test
# controls: the tool list, the two documents, the cut, the scope refusals and
# the role.
#
# The second person of the check's two sign-ins is played here by a browser
# helper that reads the URL the check PRINTS on its stderr, opens it, and follows
# the door's redirect back to the client's loopback listener — which is what the
# owner and their paired device do, and what makes the printed URL itself part of
# what is asserted.
#
# Every run is bounded by `timeout`: a check that hangs must fail the suite
# rather than hang it (an earlier check340_test.sh hung for 25 minutes, which is
# what this rule is for), and the whole suite finishes in well under a minute.
#
# Usage: bash scripts/live-checks/check305_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=${CHECK305_SCRIPT:-$here/check305.sh}
# By absolute path, because the missing-tools case replaces PATH with a
# directory that holds nothing, and `env` would then find no bash at all.
BASH_BIN=$(command -v bash)
TIMEOUT_BIN=$(command -v timeout || true)
PYTHON_BIN=$(command -v python3 || true)
work=$(cd "$(mktemp -d)" && pwd -P)
DOOR_PID=""
current_case=""
suite_done=no
cleanup_all() {
  local status=$?
  if [ "$suite_done" = no ]; then
    echo "check305_test: the suite ended inside case '${current_case:-none}' (exit $status) before it finished — a command in that case failed, and the failure above it is the reason" >&2
  fi
  if [ -n "$DOOR_PID" ]; then kill "$DOOR_PID" 2>/dev/null || true; fi
  if [ -n "${CHECK305_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "$3 (FACT $1)"
}
expect_file() { # path what
  [ -f "$1" ] || fail "$2: $1 is not there"
}

INSTALLATION=sandbox305
AGENT=sandbox305
GCP_PROJECT=prj-sandbox305
FB_PROJECT=prj-sandbox305
MCP_ACCOUNT="fugaro-mcp@$FB_PROJECT.iam.gserviceaccount.com"
SIGNIN_WAIT=25
BROWSER_WANT=0
BROWSER_FETCHED=0

BLOCK_KEYS=(phase installation version edition door agent signin-state tools state state-refusal events events-cut signin-tasks tasks-token role database-reach catalogue save restore xtrace)

# --------------------------------------------------------------------- the fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check305_test.sh: every answer comes out of $FAKE305_DIR,
# and every call is recorded, so a case can assert what the check did NOT run.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE305_DIR/calls"
status_of() { # FILE — a case turns a read into a refusal with this file
  if [ -f "$FAKE305_DIR/$1.status" ]; then
    printf 'fake fugaro: %s failed\n' "$1" >&2
    exit "$(cat "$FAKE305_DIR/$1.status")"
  fi
}
case "${1:-}" in
doctor)
  status_of doctor
  cat "$FAKE305_DIR/doctor.json"
  ;;
version)
  status_of version
  printf '%s\n' "$(cat "$FAKE305_DIR/version")"
  ;;
coordinator | report | ab)
  # The paid names: the Subscription Edition answers with its own help, a core
  # build answers with D522's stub, and a third state answers neither.
  case "$(cat "$FAKE305_DIR/edition" 2>/dev/null || echo ee)" in
  core)
    printf '%s is part of Fugaro Subscription Edition; this build does not include it\n' "$1" >&2
    exit 1
    ;;
  silent)
    printf 'fake fugaro: %s is not a command this build answers\n' "$1" >&2
    exit 1
    ;;
  *)
    printf 'Usage:\n  fugaro %s [flags]\n\nFlags:\n  -h, --help   help for %s\n' "$1" "$1"
    ;;
  esac
  ;;
orchestrator)
  case "${2:-}" in
  --help)
    printf 'Usage:\n  fugaro orchestrator [command]\n\nAvailable Commands:\n  events      Read the orchestrator audit log\n  ls          List the orchestrators of this installation\n  patch       Apply one patch document to the state\n  run         Run the project'"'"'s orchestrator daemon\n  state       Print the orchestrator'"'"'s state\n'
    ;;
  ls)
    status_of ls
    cat "$FAKE305_DIR/ls.json"
    ;;
  state)
    status_of state
    cat "$FAKE305_DIR/state.json"
    ;;
  events)
    status_of events
    since=no
    for a in "$@"; do
      case "$a" in --since=* | --since) since=yes ;; esac
    done
    if [ "$since" = yes ]; then
      cat "$FAKE305_DIR/events-cut.json"
    else
      cat "$FAKE305_DIR/events.json"
    fi
    ;;
  patch | import | run | install | uninstall)
    # Writing commands: the check writes nothing on the installation, so a case
    # that sees one of these has caught it doing what its header promises not to.
    printf 'fake fugaro: the check ran the writing command orchestrator %s\n' "$2" >&2
    exit 64
    ;;
  *)
    printf 'fake fugaro: unexpected orchestrator verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check305_test.sh: the three reads the role and the front
# door's URL are made of, and nothing else.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE305_DIR/calls"
status_of() {
  if [ -f "$FAKE305_DIR/$1.status" ]; then
    printf 'fake gcloud: %s failed\n' "$1" >&2
    exit "$(cat "$FAKE305_DIR/$1.status")"
  fi
}
case "${1:-} ${2:-}" in
"run services")
  status_of services
  cat "$FAKE305_DIR/services.txt"
  ;;
"iam roles")
  status_of role
  cat "$FAKE305_DIR/role.json"
  ;;
"projects get-iam-policy")
  status_of policy
  project=${3:-}
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--project" ]; then project=$a; fi
    case "$a" in --project=*) project=${a#--project=} ;; esac
    prev=$a
  done
  sed -E "s#projects/[^/]+/roles/fugaroStateRead#projects/$project/roles/fugaroStateRead#" "$FAKE305_DIR/policy.json"
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "$*" >&2
  exit 64
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# write_door DIR — the fake front door. It binds the loopback on a free port,
# writes the port to $DIR/door-port and appends every request it serves to
# $DIR/state/door-calls, so a case can assert what was called and that nothing
# was written.
write_door() {
  cat >"$1/door.py" <<'DOOR_EOF'
#!/usr/bin/env python3
"""A fake front door for check305_test.sh.

It speaks the surface the check signs in against: the two metadata documents,
RFC 7591 registration, GET /oauth/authorize (which redirects the code straight
to the loopback, standing in for the page's number and the paired device's
approval), POST /oauth/token with PKCE verified, POST /oauth/revoke, and /mcp
with initialize, tools/list, tools/call and the two refusals. The mode file
$FAKE305_DIR/door-mode moves one answer at a time, which is how a case drives a
row of the check's block.
"""
import base64
import hashlib
import json
import os
import re
import sys
import threading
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STATE = os.environ.get("FAKE305_DIR", "")
AGENT = os.environ.get("FAKE305_AGENT", "")
ACCOUNT = os.environ.get("FAKE305_ACCOUNT", "")
NAME_RE = re.compile(r"^[A-Za-z0-9._-]+$")


def read(name, default=""):
    try:
        with open(os.path.join(STATE, name), encoding="utf-8") as fh:
            return fh.read().strip()
    except Exception:
        return default


def read_json(name, default):
    try:
        with open(os.path.join(STATE, name), encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return default


def b64(raw):
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def tools(mode):
    full = [
        {"name": "get_state", "description": "Read one orchestrator's state", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]}},
        {"name": "get_events", "description": "Read one orchestrator's audit log", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}, "since": {"type": "string"}}, "required": ["id"]}},
        {"name": "claim_item", "description": "Claim a work item", "inputSchema": {"type": "object", "properties": {}}},
        {"name": "list_items", "description": "List the work items", "inputSchema": {"type": "object", "properties": {}}},
    ]
    if mode == "old":
        return full[2:]
    if mode == "patch":
        return full + [{"name": "apply_patch", "description": "Apply a patch document", "inputSchema": {"type": "object"}}]
    if mode == "spawn":
        return full + [{"name": "spawn_task", "description": "Spawn a task", "inputSchema": {"type": "object"}}]
    return full


def state_doc(agent, rev, brief):
    return {"version": 1, "kind": "state", "id": agent, "source": "node", "rev": rev, "schema_version": 1, "brief": brief}


PENDING = {}
TOKENS = {}
CLIENTS = {}
LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        return

    def record(self, what):
        with open(os.path.join(STATE, "door-calls"), "a", encoding="utf-8") as fh:
            fh.write(what + "\n")

    def send_json(self, status, obj, headers=None):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def base(self):
        return "http://127.0.0.1:%d" % self.server.server_address[1]

    def scope_refusal(self):
        named = "state:write" if read("door-mode") == "wrong-scope-name" else "state:read"
        self.send_json(
            403,
            {"error": "insufficient_scope", "error_description": "this token does not hold %s" % named},
            {"WWW-Authenticate": 'Bearer error="insufficient_scope", scope="%s", resource_metadata="%s/.well-known/oauth-protected-resource"' % (named, self.base())},
        )

    def call_json(self, request, obj):
        self.send_json(200, {"jsonrpc": "2.0", "id": request.get("id"), "result": {"content": [{"type": "text", "text": json.dumps(obj)}], "isError": False}})

    def call_refused(self, request, reason):
        self.send_json(200, {"jsonrpc": "2.0", "id": request.get("id"), "result": {"content": [{"type": "text", "text": reason}], "isError": True}})

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        self.record("GET " + parsed.path)
        mode = read("door-mode", "full")
        if parsed.path == "/.well-known/oauth-protected-resource":
            if mode == "off":
                self.send_json(404, {"error": "not_found"})
                return
            self.send_json(200, {"resource": self.base(), "authorization_servers": [self.base()]})
            return
        if parsed.path == "/.well-known/oauth-authorization-server":
            self.send_json(200, {
                "issuer": self.base(),
                "authorization_endpoint": self.base() + "/oauth/authorize",
                "token_endpoint": self.base() + "/oauth/token",
                "registration_endpoint": self.base() + "/oauth/register",
                "revocation_endpoint": self.base() + "/oauth/revoke",
                "response_types_supported": ["code"],
                "grant_types_supported": ["authorization_code", "refresh_token"],
                "code_challenge_methods_supported": ["S256"],
                "scopes_supported": ["tasks:read", "tasks:write", "state:read", "state:write", "roadmap:write"],
            })
            return
        if parsed.path == "/oauth/authorize":
            query = {k: v[0] for k, v in urllib.parse.parse_qs(parsed.query).items()}
            if query.get("code_challenge_method") != "S256" or not query.get("code_challenge"):
                self.send_json(400, {"error": "invalid_request", "error_description": "PKCE S256 is required"})
                return
            code = "code-%d" % (len(PENDING) + 1)
            with LOCK:
                PENDING[code] = query
            # The redirect's state is the client's own anti-forgery value: the
            # wrong-state mode answers with one it never sent, which the client
            # must refuse rather than redeem the code with.
            state = query.get("state", "")
            if read("door-mode") == "wrong-state":
                state = "a-state-this-client-never-sent"
            location = query["redirect_uri"] + "?" + urllib.parse.urlencode({"code": code, "state": state})
            self.send_response(302)
            self.send_header("Location", location)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        self.send_json(404, {"error": "not_found"})

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        self.record("POST " + parsed.path)
        if parsed.path == "/oauth/register":
            doc = json.loads(raw or b"{}")
            client_id = "client-%d" % (len(CLIENTS) + 1)
            with LOCK:
                CLIENTS[client_id] = doc
            self.send_json(201, {
                "client_id": client_id,
                "client_secret": "secret-" + client_id,
                "token_endpoint_auth_method": "none",
                "grant_types": ["authorization_code", "refresh_token"],
                "response_types": ["code"],
                "scope": "tasks:read tasks:write state:read",
                "redirect_uris": doc.get("redirect_uris") or [],
            })
            return
        if parsed.path == "/oauth/token":
            form = {k: v[0] for k, v in urllib.parse.parse_qs(raw.decode("utf-8")).items()}
            if form.get("grant_type") != "authorization_code":
                self.send_json(400, {"error": "unsupported_grant_type", "error_description": "only the code"})
                return
            with LOCK:
                pending = PENDING.get(form.get("code", ""))
            if pending is None:
                self.send_json(400, {"error": "invalid_grant", "error_description": "the code is not one this door issued"})
                return
            if form.get("redirect_uri") != pending.get("redirect_uri") or form.get("client_id") != pending.get("client_id"):
                self.send_json(400, {"error": "invalid_grant", "error_description": "the client or the redirect is not the one the code was issued to"})
                return
            verifier = form.get("code_verifier", "")
            if b64(hashlib.sha256(verifier.encode("utf-8")).digest()) != pending.get("code_challenge"):
                self.send_json(400, {"error": "invalid_grant", "error_description": "the verifier does not answer the challenge"})
                return
            scope = pending.get("scope") or ""
            kind = "state" if "state:read" in scope else "tasks"
            with LOCK:
                PENDING.pop(form["code"], None)
                token = "%s-token-%d" % (kind, len(TOKENS) + 1)
                TOKENS[token] = {"scope": scope, "kind": kind, "client_id": pending.get("client_id")}
            self.send_json(200, {
                "access_token": token,
                "token_type": "Bearer",
                "expires_in": 3600,
                "refresh_token": "refresh-" + token,
                "scope": scope,
            })
            return
        if parsed.path == "/oauth/revoke":
            form = {k: v[0] for k, v in urllib.parse.parse_qs(raw.decode("utf-8")).items()}
            with LOCK:
                for token, entry in list(TOKENS.items()):
                    if entry.get("client_id") == form.get("client_id"):
                        TOKENS.pop(token, None)
            self.send_json(200, {})
            return
        if parsed.path == "/mcp":
            self.mcp(raw)
            return
        self.send_json(404, {"error": "not_found"})

    def do_PUT(self):
        self.record("PUT " + urllib.parse.urlparse(self.path).path)
        self.send_json(405, {"error": "the check writes nothing here"})

    def mcp(self, raw):
        authorization = self.headers.get("Authorization") or ""
        token = authorization[7:] if authorization.startswith("Bearer ") else ""
        with LOCK:
            entry = TOKENS.get(token)
        if entry is None:
            self.send_json(401, {"error": "invalid_token", "error_description": "the token is not one this door minted"}, {"WWW-Authenticate": 'Bearer error="invalid_token"'})
            return
        mode = read("door-mode", "full")
        scope = entry["scope"]
        request = json.loads(raw or b"{}")
        method = request.get("method")
        if method == "initialize":
            self.send_json(200, {"jsonrpc": "2.0", "id": request.get("id"), "result": {
                "protocolVersion": "2026-07-28",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "fugaro-coordinator", "version": "0.24.0"},
            }})
            return
        if method == "tools/list":
            if "state:read" not in scope:
                self.scope_refusal()
                return
            self.send_json(200, {"jsonrpc": "2.0", "id": request.get("id"), "result": {"tools": tools(mode)}})
            return
        if method == "tools/call":
            params = request.get("params") or {}
            name = params.get("name")
            arguments = params.get("arguments") or {}
            if name in ("get_state", "get_events") and "state:read" not in scope and mode != "tasks-allowed":
                self.scope_refusal()
                return
            agent = str(arguments.get("id") or "")
            if name not in ("get_state", "get_events"):
                self.call_refused(request, "there is no tool named %s" % name)
                return
            if not NAME_RE.match(agent):
                if mode == "answer-notname":
                    self.call_json(request, state_doc(agent, 0, ""))
                    return
                self.call_refused(request, "invalid: the agent id is not a name")
                return
            if name == "get_state":
                if agent == AGENT:
                    node = read_json("state.json", {})
                    rev = int(node.get("rev", 7))
                    if mode == "state-mismatch":
                        rev += 1
                    self.call_json(request, state_doc(agent, rev, node.get("brief", "")))
                    return
                if mode == "other-loop":
                    self.call_json(request, state_doc("another-loop", 3, "a document another loop wrote"))
                    return
                self.call_json(request, state_doc(agent, 0, ""))
                return
            entries = (read_json("events.json", {}) or {}).get("events", [])
            since = arguments.get("since")
            if since and mode != "no-cut":
                entries = entries[:1]
            doc = {"version": 1, "kind": "events", "id": agent, "events": entries}
            if mode == "cursor":
                doc["cursor"] = "entry-9"
            self.call_json(request, doc)
            return
        self.send_json(200, {"jsonrpc": "2.0", "id": request.get("id"), "error": {"code": -32601, "message": "no such method"}})


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 0
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    with open(os.path.join(STATE, "door-port"), "w", encoding="utf-8") as fh:
        fh.write("%d\n" % server.server_address[1])
    server.serve_forever()


if __name__ == "__main__":
    main()
DOOR_EOF
  chmod +x "$1/door.py"
}

# write_browser DIR — the second person, played mechanically: it reads the URL
# the check prints on its stderr, opens it and follows the redirect to the
# client's loopback listener. It fetches exactly WANT of them and gives up on its
# own, so a case whose check never signs in does not wait for anything.
write_browser() {
  cat >"$1/browser.py" <<'BROWSER_EOF'
#!/usr/bin/env python3
"""Reads the URL the check printed and opens it, once per distinct URL."""
import re
import sys
import time
import urllib.error
import urllib.request

path, want, out = sys.argv[1], int(sys.argv[2]), sys.argv[3]
pattern = re.compile(r"https?://[^\s]+/oauth/authorize\?[^\s]+")
seen = []
lines = []
start = time.time()
last = start
while len(lines) < want and time.time() < start + 15 and time.time() - last < 4:
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except Exception:
        text = ""
    for url in pattern.findall(text):
        if url in seen:
            continue
        seen.append(url)
        try:
            with urllib.request.urlopen(url, timeout=5) as response:
                lines.append("200 %s" % response.status)
        except urllib.error.HTTPError as e:
            lines.append("HTTP %d %s" % (e.code, e.reason))
        except Exception as e:
            lines.append("ERR %s" % e)
        last = time.time()
        break
    time.sleep(0.05)
with open(out, "a", encoding="utf-8") as fh:
    fh.write("\n".join(lines) + ("\n" if lines else ""))
BROWSER_EOF
  chmod +x "$1/browser.py"
}

# ------------------------------------------------------------------ the fixtures
write_doctor() { # dir [NAME]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "europe-west1"}}\n' \
    "${2:-$INSTALLATION}" "$GCP_PROJECT" >"$1/state/doctor.json"
}

write_ls() { # dir [ID]
  printf '{"version": 1, "kind": "ls", "orchestrators": [{"kind": "local", "project": "%s", "lease_holder": "user:me@example.com", "updated_at": "2026-11-13T17:00:00Z", "cached_at": "2026-11-13T17:00:00Z"}]}\n' \
    "${2:-$AGENT}" >"$1/state/ls.json"
}

write_state() { # dir [REV]
  printf '{"version": 1, "kind": "state", "id": "%s", "source": "node", "rev": %s, "schema_version": 1, "brief": "goal: read this project'"'"'s state through the front door\\n\\nwork items: 1, 1 still open\\n  work-item-1 [ready] a piece of work\\n\\ndecisions: 0, 0 waiting\\n\\nfacts: 0"}\n' \
    "$AGENT" "${2:-7}" >"$1/state/state.json"
}

# write_events DIR [DIRTY] — the log both surfaces read: three entries, the
# newest first in the document's own order (SortEntries sorts by the stamp, and
# these stamps are what the door's copy is held to). DIRTY writes the first
# entry's text with a newline in it and longer than the log's own cap, which is
# what the door's cleaning exists to remove.
write_events() { # dir [dirty]
  local dir=$1 dirty=${2:-}
  local first="a cycle open"
  if [ "$dirty" = dirty ]; then
    first="two\\nlines\\tand a tab $(printf 'x%.0s' $(seq 1 320))"
  fi
  printf '{"version": 1, "kind": "events", "id": "%s", "events": [{"id": "entry-1", "at": "2026-11-13T16:59:00Z", "kind": "cycle", "text": "%s"}, {"id": "entry-2", "at": "2026-11-13T16:00:00Z", "kind": "claim", "text": "one item claimed"}, {"id": "entry-3", "at": "2026-11-13T15:00:00Z", "kind": "merge", "text": "a merge landed"}]}\n' \
    "$AGENT" "$first" >"$dir/state/events.json"
  printf '{"version": 1, "kind": "events", "id": "%s", "since": "2026-11-13T16:30:00Z", "events": [{"id": "entry-1", "at": "2026-11-13T16:59:00Z", "kind": "cycle", "text": "%s"}]}\n' \
    "$AGENT" "$first" >"$dir/state/events-cut.json"
}

write_role() { # dir [PERMISSIONS-JSON]
  printf '{"name": "projects/%s/roles/fugaroStateRead", "title": "Fugaro state read", "description": "Read one project'"'"'s orchestrator state", "stage": "GA", "includedPermissions": %s}\n' \
    "$FB_PROJECT" "${2:-[\"firebasedatabase.instances.get\"]}" >"$1/state/role.json"
}

write_policy() { # dir [MEMBERS…]
  local dir=$1
  shift
  local members="" m
  for m in "$@"; do
    members="$members\"$m\", "
  done
  [ -n "$members" ] || members="\"serviceAccount:fugaro-mcp@$FB_PROJECT.iam.gserviceaccount.com\", "
  printf '{"bindings": [{"role": "projects/%s/roles/fugaroStateRead", "members": [%s]}], "etag": "BwY="}\n' \
    "$FB_PROJECT" "${members%, }" >"$dir/state/policy.json"
}

write_services() { # dir [URL]
  printf '%s\n' "${2:-}" >"$1/state/services.txt"
}

write_door_mode() { # dir MODE
  printf '%s\n' "$2" >"$1/state/door-mode"
}

# setup_case NAME — a fresh case: the two fakes, a healthy Subscription Edition
# installation, the fake door in its full mode, an orchestrator the listing
# holds and the fixtures both the CLI's reads and the door's answers come from.
setup_case() {
  if [ -n "$DOOR_PID" ]; then kill "$DOOR_PID" 2>/dev/null || true; fi
  DOOR_PID=""
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  write_door "$dir"
  write_browser "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/door-calls"
  rm -f "$dir"/state/*.status
  printf '0.24.0\n' >"$dir/state/version"
  printf 'ee\n' >"$dir/state/edition"
  printf '7\n' >"$dir/state/rev"
  write_doctor "$dir"
  write_ls "$dir"
  write_state "$dir"
  write_events "$dir"
  write_role "$dir"
  write_policy "$dir"
  write_services "$dir"
  write_door_mode "$dir" full
  BROWSER_WANT=0
  DOOR=http://127.0.0.1:1
  start_door
}

# start_door — the door binds a free port and says which one. Bounded: a door
# that never writes its port fails the case rather than hanging the suite.
start_door() {
  rm -f "$dir/state/door-port"
  env FAKE305_DIR="$dir/state" FAKE305_AGENT="$AGENT" FAKE305_ACCOUNT="$MCP_ACCOUNT" \
    "$TIMEOUT_BIN" 120 "$PYTHON_BIN" "$dir/door.py" >>"$dir/door.log" 2>&1 &
  DOOR_PID=$!
  local i=0
  while [ ! -s "$dir/state/door-port" ] && [ "$i" -lt 100 ]; do
    sleep 0.05
    i=$((i + 1))
  done
  [ -s "$dir/state/door-port" ] || die_case "the fake door did not come up: $(head -c 400 "$dir/door.log" 2>/dev/null)"
  DOOR="http://127.0.0.1:$(head -n 1 "$dir/state/door-port")"
}

die_case() {
  fail "$1"
  printf 'FACT the-case: FAIL — %s\n' "$1"
  exit 1
}

# run305 [ARGS...] — runs the check on the case at hand. RUN305_PATH replaces
# PATH and RUN305_BASH adds flags to the bash that runs it. Sets $status, $out,
# $errtext and $dir/err. Every run is bounded, and the browser helper the case
# asked for is started before it and joined after.
run305() {
  status=0
  BROWSER_FETCHED=0
  local browser_pid=""
  if [ "$BROWSER_WANT" -gt 0 ]; then
    : >"$dir/browser.out"
    "$TIMEOUT_BIN" 30 "$PYTHON_BIN" "$dir/browser.py" "$dir/err" "$BROWSER_WANT" "$dir/browser.out" &
    browser_pid=$!
  fi
  out=$(env TMPDIR="$dir/tmp" FAKE305_DIR="$dir/state" CHECK305_SIGNIN_WAIT="$SIGNIN_WAIT" \
    PATH="${RUN305_PATH:-$dir/bin:$PATH}" \
    "$TIMEOUT_BIN" 60 "$BASH_BIN" ${RUN305_BASH[@]+"${RUN305_BASH[@]}"} "${RUN305_SCRIPT:-$script}" ${RUN305_ARGS[@]+"${RUN305_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN305_ARGS=()
  RUN305_PATH=""
  RUN305_BASH=()
  RUN305_SCRIPT=""
  errtext=$(cat "$dir/err" 2>/dev/null || true)
  # The scratch directory is this check's own litter and the cleanup is a
  # promise its header makes: every case asserts it, whatever it is about.
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" 2>/dev/null | head -n 1)
  if [ -n "$sc" ] && [ -d "$sc" ]; then
    fail "case $dir: the scratch directory survived the run: $sc"
  fi
  if [ -n "$browser_pid" ]; then
    wait "$browser_pid" 2>/dev/null || true
    BROWSER_FETCHED=$(grep -c '^200 ' "$dir/browser.out" 2>/dev/null || true)
  fi
}

# expect_browser WANT WHAT — the sign-in URL the check printed was one a browser
# could open and follow: WANT of them, and every one of them answered by the
# door rather than a dead link. This is what makes the printed URL itself part of
# what is asserted.
expect_browser() { # want what
  [ "$BROWSER_FETCHED" -eq "$1" ] || fail "$2: the check printed $BROWSER_FETCHED of the $1 sign-in URLs a browser could open and follow: $(head -c 300 "$dir/browser.out" 2>/dev/null)"
}

expect_block_keys() { # out what
  local text=$1 what=$2
  expect_contains "$text" "CHECK 305 RESULT" "$what: the block"
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$text" "FACT $key:" "$what: the $key row"
  done
  expect_contains "$text" "CHECK 305 RESULT END (exit " "$what: the block's end"
}

# expect_no_writes WHAT — the check writes nothing on the installation: no
# writing fugaro command, and no request to the door that is not a read of a
# surface or the client's own registration, token and revocation.
expect_no_writes() { # what
  local wrote
  wrote=$(grep -cE 'orchestrator (patch|import|run|install|uninstall)' "$dir/state/calls" 2>/dev/null || true)
  [ "${wrote:-0}" -eq 0 ] || fail "$1: the check ran a writing fugaro command: $(grep -E 'orchestrator (patch|import|run|install|uninstall)' "$dir/state/calls" | tr '\n' ' ')"
  wrote=$(grep -cE '^(PUT|DELETE)' "$dir/state/door-calls" 2>/dev/null || true)
  [ "${wrote:-0}" -eq 0 ] || fail "$1: the check wrote at the door: $(grep -E '^(PUT|DELETE)' "$dir/state/door-calls" | tr '\n' ' ')"
  local post
  post=$(grep -E '^POST ' "$dir/state/door-calls" 2>/dev/null | grep -vcE '/oauth/(register|token|revoke)$|/mcp$' || true)
  [ "${post:-0}" -eq 0 ] || fail "$1: the check posted to a path that is not the door's own: $(grep -E '^POST ' "$dir/state/door-calls" | tr '\n' ' ')"
}

# ------------------------------------------------------------------- the cases
case_full_pass() {
  setup_case full
  BROWSER_WANT=2
  mkdir -p "$dir/saved"
  run305 --installation "$INSTALLATION" --door "$DOOR" --firebase-project "$FB_PROJECT" --save "$dir/saved"
  expect_status "$status" 0 "full: the whole check over a working state group"
  expect_block_keys "$out" "full"
  local key
  for key in phase installation version edition door agent signin-state tools state state-refusal events events-cut signin-tasks tasks-token role save restore xtrace; do
    expect_fact "$key" PASS "full"
  done
  expect_fact catalogue INFO "full"
  expect_fact database-reach INFO "full"
  expect_contains "$out" "F27" "full: the reach of the one grant is said (U11), not read"
  expect_contains "$out" "get_state and get_events are in it" "full: the tool list names the two read tools"
  expect_contains "$out" "apply_patch is NOT" "full: the list carries no apply_patch"
  expect_contains "$out" "one node, two surfaces" "full: the state reading"
  expect_contains "$out" "no cursor" "full: the log reading"
  expect_contains "$out" "insufficient_scope" "full: the refusal names the scope"
  expect_contains "$out" "firebasedatabase.instances.get" "full: the role's permission"
  expect_contains "$out" "a service account of $GCP_PROJECT and nobody else" "full: the one member the role names"
  expect_contains "$out" "is NOT read here" "full: the reading that cannot be produced is reported"
  expect_contains "$errtext" "OPEN THIS URL" "full: the check hands the owner the URL to open"
  expect_browser 2 "full: both sign-in URLs the check printed were openable and followed"
  # The documents the owner pastes back are kept where they asked, and the
  # tokens are not among them.
  expect_file "$dir/saved/tools.json" "full: the tool list is kept"
  expect_file "$dir/saved/state.tool.json" "full: the door's state document is kept"
  expect_file "$dir/saved/role.json" "full: the role is kept"
  local leaked
  leaked=$(grep -rl 'state-token-\|tasks-token-\|refresh-' "$dir/saved" 2>/dev/null || true)
  [ -z "$leaked" ] || fail "full: a token is in the kept documents: $leaked"
  expect_absent "$out" "state-token-" "full: the block carries no token"
  expect_absent "$out" "refresh-" "full: the block carries no refresh token"
  # The two families this check minted were revoked, at the door, before it left.
  expect_contains "$(cat "$dir/state/door-calls")" "POST /oauth/revoke" "full: the tokens are revoked at the door"
  local revokes
  revokes=$(grep -c '^POST /oauth/revoke$' "$dir/state/door-calls" || true)
  [ "${revokes:-0}" -eq 2 ] || fail "full: expected two revocations (one per family), got ${revokes:-0}"
  expect_no_writes "full"
}

case_door_off() {
  setup_case off
  write_door_mode "$dir" off
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "off: a door that serves 404 on every path is off"
  expect_fact door FAIL "off"
  expect_contains "$out" "serves a 404 on every path" "off: the fact names what was read"
  expect_contains "$errtext" "the front door is off at" "off: the stop says it"
  expect_fact tools FAIL "off: the run stopped before the tool list"
  expect_contains "$out" "not reached" "off: the rows that were not measured say so"
  expect_no_writes "off"
}

case_no_door_published() {
  setup_case nodoor
  write_services "$dir" ""
  run305 --installation "$INSTALLATION"
  expect_status "$status" 2 "nodoor: an installation that publishes no front door"
  expect_fact door FAIL "nodoor"
  expect_contains "$out" "publishes no Cloud Run service URL" "nodoor: the fact names what was read"
  expect_contains "$(cat "$dir/state/calls")" "gcloud run services list" "nodoor: the door was looked for the way a client looks"
}

# The redirect's state is the client's own anti-forgery value, and the fake door
# echoes back the one it was handed in every other case: a client that stopped
# comparing it would pass the whole suite, and would redeem a code that answers
# somebody else's authorization request. The wrong-state mode answers with a
# value the client never sent, which it must refuse BEFORE it redeems anything.
case_signin_wrong_state() {
  setup_case wrongstate
  write_door_mode "$dir" wrong-state
  BROWSER_WANT=1
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "wrongstate: the run stops when the redirect carries a state the client never sent"
  expect_fact signin-state FAIL "wrongstate: the one sign-in it made did not finish"
  expect_contains "$errtext" "the redirect carried a state this client did not send" "wrongstate: the client's own line is what stops it"
  expect_absent "$out" "FACT tools: PASS" "wrongstate: nothing past the sign-in was read"
  expect_absent "$(cat "$dir/state/door-calls")" "POST /oauth/token" "wrongstate: the code was never redeemed"
}

case_release_gate() {
  setup_case gate
  write_door_mode "$dir" old
  BROWSER_WANT=1
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "gate: a door serving a build before the release stops the run"
  expect_fact tools FAIL "gate"
  expect_contains "$out" "needs release 0.24.0" "gate: the stop names the release"
  expect_contains "$out" "Uakari" "gate: and its name"
  expect_contains "$errtext" "Apply the release to the installation" "gate: the stop says what to do"
  expect_fact version PASS "gate: this build reports the release the work is in"
  expect_fact signin-state PASS "gate: the sign-in came before the tool list"
  expect_fact signin-tasks FAIL "gate: the second sign-in was not asked for"
  expect_browser 1 "gate: the gate stops the run after the first sign-in, so only one URL is printed"
  expect_no_writes "gate"
}

case_apply_patch_listed() {
  setup_case patch
  write_door_mode "$dir" patch
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "patch: a listed apply_patch is a FAILed row"
  expect_fact tools FAIL "patch"
  expect_contains "$out" "D2021" "patch: the fact names the decision the write half rests on"
  expect_contains "$out" "apply_patch" "patch: and the tool"
  expect_fact state PASS "patch: the readings after it are still made"
  expect_no_writes "patch"
}

case_state_mismatch() {
  setup_case mismatch
  write_door_mode "$dir" state-mismatch
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "mismatch: two documents for one node is a FAILed row"
  expect_fact state FAIL "mismatch"
  expect_contains "$out" "rev:" "mismatch: the fact names the member that differs"
  expect_fact tools PASS "mismatch: the tool list was read"
  expect_no_writes "mismatch"
}

case_state_cli_refused() {
  setup_case staterefused
  printf '1\n' >"$dir/state/state.status"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "staterefused: the CLI's own read failing stops the reading"
  expect_fact state FAIL "staterefused"
  expect_contains "$errtext" "the CLI could not read the node" "staterefused: the stop says so"
  expect_no_writes "staterefused"
}

case_state_refusal_answered() {
  setup_case notname
  write_door_mode "$dir" answer-notname
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "notname: an id that is not a name answered is a FAILed row"
  expect_fact state-refusal FAIL "notname"
  expect_contains "$out" "not a name" "notname: the fact names the id"
  expect_fact state PASS "notname: the state reading itself was made"
  expect_no_writes "notname"
}

case_state_refusal_other_loop() {
  setup_case otherloop
  write_door_mode "$dir" other-loop
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "otherloop: another document answered as this project's state is a FAILed row"
  expect_fact state-refusal FAIL "otherloop"
  expect_contains "$out" "not that id's own empty state" "otherloop: the fact says what came back"
  expect_no_writes "otherloop"
}

case_events_cursor() {
  setup_case cursor
  write_door_mode "$dir" cursor
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "cursor: a cursor is a FAILed row (D1229)"
  expect_fact events FAIL "cursor"
  expect_contains "$out" 'cursor: the CLI says' "cursor: the fact names the member the CLI's document does not carry"
  expect_fact state PASS "cursor: the state reading was made"
  expect_no_writes "cursor"
}

case_events_dirty_text() {
  setup_case dirty
  write_events "$dir" dirty
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "dirty: an uncleaned entry's text is a FAILed row"
  expect_fact events FAIL "dirty"
  expect_contains "$out" "not one line" "dirty: the fact names the cleaning that did not happen"
  expect_contains "$out" "longer than the log's own cap" "dirty: and the cap"
  expect_no_writes "dirty"
}

case_events_cut_ignored() {
  setup_case cut
  write_door_mode "$dir" no-cut
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "cut: a duration the door ignores is a FAILed row"
  expect_fact events-cut FAIL "cut"
  expect_contains "$out" "the same duration cuts the two surfaces differently" "cut: the fact says so"
  expect_fact events PASS "cut: the uncut reading was made"
  expect_no_writes "cut"
}

case_tasks_token_allowed() {
  setup_case tasksok
  write_door_mode "$dir" tasks-allowed
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "tasksok: a tasks-only token reading state is a FAILed row"
  expect_fact tasks-token FAIL "tasksok"
  expect_contains "$out" "never be one" "tasksok: the fact says what the rule is"
  expect_fact state PASS "tasksok: the state:read readings were made"
  expect_no_writes "tasksok"
}

case_tasks_token_wrong_scope() {
  setup_case taskscope
  write_door_mode "$dir" wrong-scope-name
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "taskscope: a refusal that does not name state:read is a FAILed row"
  expect_fact tasks-token FAIL "taskscope"
  expect_no_writes "taskscope"
}

case_role_extra_permission() {
  setup_case roleperm
  write_role "$dir" '["firebasedatabase.instances.get", "firebasedatabase.instances.update"]'
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "roleperm: a write in the read role is a FAILed row"
  expect_fact role FAIL "roleperm"
  expect_contains "$out" "instances.update" "roleperm: the fact names the permission"
  expect_contains "$out" "D1223" "roleperm: and the decision the write stays with"
  expect_no_writes "roleperm"
}

case_role_two_members() {
  setup_case rolemembers
  write_policy "$dir" "serviceAccount:$MCP_ACCOUNT" "user:someone@example.com"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "rolemembers: a second member on the role is a FAILed row"
  expect_fact role FAIL "rolemembers"
  expect_contains "$out" "names 2 members" "rolemembers: the fact counts them"
  expect_contains "$out" "the front door's own account and nothing else" "rolemembers: and says the reading"
  expect_no_writes "rolemembers"
}

case_role_not_described() {
  setup_case rolenone
  printf '1\n' >"$dir/state/role.status"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "rolenone: a role that cannot be described is a FAILed row"
  expect_fact role FAIL "rolenone"
  expect_contains "$out" "state_read.tf" "rolenone: the fact names where the role comes from"
  expect_no_writes "rolenone"
}

case_belong() {
  setup_case belong
  run305 --installation belong --door "$DOOR"
  expect_status "$status" 2 "belong: the owner's own installation is refused"
  expect_fact installation FAIL "belong"
  expect_contains "$errtext" "never runs against 'belong'" "belong: the stop says so"
  expect_absent "$out" "FACT door: PASS" "belong: nothing was read"
  expect_browser 0 "belong: nothing was signed in against"
  if [ -s "$dir/state/door-calls" ]; then fail "belong: the door was called"; fi
  if [ -s "$dir/state/calls" ]; then fail "belong: fugaro was called"; fi
}

case_belong_from_doctor() {
  setup_case belongdoctor
  write_doctor "$dir" belong
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "belongdoctor: the name the config in force answers is held to belong too"
  expect_fact installation FAIL "belongdoctor"
  expect_contains "$out" "the owner's own installation" "belongdoctor: the fact says which name"
  if [ -s "$dir/state/door-calls" ]; then fail "belongdoctor: the door was called"; fi
}

case_name_mismatch() {
  setup_case nameclash
  write_doctor "$dir" another-sandbox
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "nameclash: the name you typed is held to the config in force"
  expect_fact installation FAIL "nameclash"
  expect_contains "$out" "the local config in force answers another-sandbox" "nameclash: the fact names both"
  if [ -s "$dir/state/door-calls" ]; then fail "nameclash: the door was called"; fi
}

case_doctor_refused() {
  setup_case doctorout
  printf '1\n' >"$dir/state/doctor.status"
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "doctorout: doctor not answering stops the run"
  expect_contains "$errtext" "fugaro doctor --json did not answer" "doctorout: the stop says so"
  expect_fact aborted FAIL "doctorout"
}

case_no_orchestrator() {
  setup_case noloop
  write_ls "$dir" "some-other-loop"
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "noloop: a node nothing holds stops the reading"
  expect_fact agent FAIL "noloop"
  expect_contains "$out" "would compare two empty documents" "noloop: the fact says why it is a stop"
  expect_absent "$out" "FACT state: PASS" "noloop: no document was compared"
}

case_firebase_project_named() {
  setup_case fbnamed
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR" --firebase-project fb-other
  expect_status "$status" 0 "fbnamed: the role is read in the project you name"
  expect_contains "$out" "fb-other" "fbnamed: the fact names it"
  expect_contains "$(cat "$dir/state/calls")" "iam roles describe fugaroStateRead --project fb-other" "fbnamed: gcloud was asked for that project"
}

case_core_build() {
  setup_case core
  printf 'core\n' >"$dir/state/edition"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 0 "core: a core build still reads the door itself"
  expect_fact edition INFO "core"
  expect_contains "$out" "core build" "core: the fact says which build this is"
  expect_fact tools PASS "core"
}

case_old_version() {
  setup_case oldver
  printf '0.23.0\n' >"$dir/state/version"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 0 "oldver: the version is a fact and not a gate"
  expect_fact version INFO "oldver"
  expect_contains "$out" "the release a build reports and the work it carries are two things" "oldver: the fact says why"
  expect_fact tools PASS "oldver: the tool list is what settles it"
}

case_dev_version() {
  setup_case devver
  printf 'dev\n' >"$dir/state/version"
  BROWSER_WANT=2
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 0 "devver: a build from source is a fact, not a gate (a dev build of main carries O16b)"
  expect_fact version INFO "devver"
  expect_contains "$out" "a build from source prints its own string" "devver: the fact says so"
  expect_fact tools PASS "devver"
}

case_no_installation() {
  setup_case noinstall
  run305 --door "$DOOR"
  expect_status "$status" 2 "noinstall: --installation is required"
  expect_contains "$errtext" "--installation is required" "noinstall: the usage says so"
}

case_unknown_arg() {
  setup_case unknown
  run305 --installation "$INSTALLATION" --wat
  expect_status "$status" 2 "unknown: an unknown argument is refused"
  expect_contains "$errtext" "unknown argument '--wat'" "unknown: the message names it"
}

case_agent_id_path() {
  setup_case agentid
  run305 --installation "$INSTALLATION" --agent '../../etc/passwd' --door "$DOOR"
  expect_status "$status" 2 "agentid: an id that is not a name is refused before anything is read"
  expect_contains "$errtext" "not an orchestrator id" "agentid: the message says so"
  if [ -s "$dir/state/calls" ]; then fail "agentid: fugaro was called"; fi
}

case_xtrace() {
  setup_case xtrace
  RUN305_BASH=(-x)
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "xtrace: a tracing shell is refused"
  expect_fact xtrace FAIL "xtrace"
  expect_contains "$errtext" "this shell is tracing" "xtrace: the stop says so"
  if [ -s "$dir/state/calls" ]; then fail "xtrace: fugaro was called"; fi
  if [ -s "$dir/state/door-calls" ]; then fail "xtrace: the door was called"; fi
}

case_missing_python() {
  setup_case nopython
  mkdir -p "$dir/notools"
  local t
  for t in bash env timeout mktemp sed grep head tr cut wc cp mkdir cat rm; do
    ln -sf "$(command -v "$t")" "$dir/notools/$t"
  done
  RUN305_PATH="$dir/notools:$dir/bin"
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "nopython: a missing tool is one line and a stop"
  expect_contains "$errtext" "needs 'python3' on PATH" "nopython: the message names it"
  expect_contains "$errtext" "the check's own MCP client" "nopython: and why the check needs it"
  if [ -s "$dir/state/door-calls" ]; then fail "nopython: the door was called"; fi
}

case_missing_tools() {
  setup_case notools
  mkdir -p "$dir/empty"
  RUN305_PATH="$dir/empty"
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 2 "notools: an empty PATH stops before anything is read"
  expect_contains "$errtext" "needs 'bash' on PATH" "notools: the line names the first tool that is missing"
}

case_dry_run() {
  setup_case dry
  run305 --installation "$INSTALLATION" --door "$DOOR" --dry-run
  expect_status "$status" 0 "dry: a dry run plans and changes nothing"
  expect_fact phase PASS "dry: the guards and the plan"
  expect_fact dry-run PASS "dry"
  expect_contains "$errtext" "sign-in 1 of 2" "dry: the plan names the owner's own act"
  expect_contains "$errtext" "release 0.24.0" "dry: the plan names the gate"
  expect_contains "$errtext" "NOTHING below was run" "dry: the plan says so up front"
  if [ -s "$dir/state/calls" ]; then fail "dry: fugaro was called"; fi
  if [ -s "$dir/state/door-calls" ]; then fail "dry: the door was called"; fi
}

# --------------------------------------------------------------- the mutation
# The mutation breaks the row the fourth reading rests on: a check whose
# tasks-token row could be made to pass while the door serves the state tools to
# a tasks-only token would report a scope refusal that never happened, and this
# suite would not catch it.
case_mutation_tasks_row_always_passes() {
  setup_case mutation
  write_door_mode "$dir" tasks-allowed
  BROWSER_WANT=2
  local copy=$dir/mutated305.sh
  sed 's|  fact tasks-token FAIL "a token holding only|  fact tasks-token PASS "a token holding only|' "$script" >"$copy"
  if ! grep -q 'fact tasks-token PASS "a token holding only' "$copy"; then
    fail "mutation: the sed found no place to break the tasks-token row"
    return 0
  fi
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_status "$status" 1 "mutation: the unbroken check FAILs the row the door's answer breaks"
  expect_fact tasks-token FAIL "mutation"
  RUN305_SCRIPT="$copy"
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_contains "$out" "FACT tasks-token: PASS" \
    "mutation: the broken row reports PASS, so the assertion in case_tasks_token_allowed is not vacuous"
  expect_status "$status" 0 "mutation: the broken copy exits 0 where the real one exits 1"
}

# The mutation: a copy of the check whose client mints the token file
# world-readable. The mode the block reports is a reading of that file (stat), so
# widening it must FAIL the sign-in row and stop the run before it reads the door
# with that token — a check that printed the mode it promised instead of the one
# the file has would pass this copy and report "mode 0600" over a 0644 file.
case_mutation_token_mode_world_readable() {
  setup_case mutmode
  BROWSER_WANT=2
  local copy=$dir/mutated305.sh old_umask
  sed 's|os.O_TRUNC, 0o600)|os.O_TRUNC, 0o644)|' "$script" >"$copy"
  if ! grep -q 'os.O_TRUNC, 0o644)' "$copy"; then
    fail "mutation: the sed found no place to widen the token file's mode"
    return 0
  fi
  run305 --installation "$INSTALLATION" --door "$DOOR"
  expect_fact signin-state PASS "mutation: the unbroken check reads the file's mode and passes it"
  expect_contains "$out" "is mode 600" "mutation: the row carries the mode stat read, so it is a reading"
  # The mode the client asks for is the file's mode only under a umask that does
  # not take bits away, and this case asserts the two differ.
  old_umask=$(umask)
  umask 022
  RUN305_SCRIPT="$copy"
  BROWSER_WANT=1
  run305 --installation "$INSTALLATION" --door "$DOOR"
  umask "$old_umask"
  expect_fact signin-state FAIL "mutation: a world-readable token file FAILs the sign-in row"
  expect_contains "$out" "is mode 644" "mutation: and the row carries the mode it read"
  expect_status "$status" 2 "mutation: the copy stops there instead of reading the door with that token"
}

# --------------------------------------------------------------- the suite
# CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
# CHECK_TEST_ONLY=name runs that one case alone. Neither set: all run.
case_index=0
t() {
  local i=$case_index
  case_index=$((case_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  current_case=$1
  "$1"
}

t case_full_pass
t case_door_off
t case_no_door_published
t case_signin_wrong_state
t case_release_gate
t case_apply_patch_listed
t case_state_mismatch
t case_state_cli_refused
t case_state_refusal_answered
t case_state_refusal_other_loop
t case_events_cursor
t case_events_dirty_text
t case_events_cut_ignored
t case_tasks_token_allowed
t case_tasks_token_wrong_scope
t case_role_extra_permission
t case_role_two_members
t case_role_not_described
t case_belong
t case_belong_from_doctor
t case_name_mismatch
t case_doctor_refused
t case_no_orchestrator
t case_firebase_project_named
t case_core_build
t case_old_version
t case_dev_version
t case_no_installation
t case_unknown_arg
t case_agent_id_path
t case_xtrace
t case_missing_python
t case_missing_tools
t case_dry_run
t case_mutation_tasks_row_always_passes
t case_mutation_token_mode_world_readable

suite_done=yes
if [ "$failures" -ne 0 ]; then
  echo "check305_test: $failures failure(s)" >&2
  exit 1
fi
echo "check305_test: all cases passed"
