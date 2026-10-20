# Check 90 — the step-by-step text (appendix)

This is the step-by-step text the register's Check 90 section used to carry, kept
because it is what explains a result. The check itself is run as one script,
`scripts/live-checks/check90.sh`, which does every step below and prints a
`CHECK 90 RESULT` block; read this when a fact in that block is not `PASS` and you
want to know what the step it names did.

The register's section is `docs/gcp-live-checklist.md`, "Check 90".

---

## Check 90: the OAuth header swap — a throwaway loopback proxy (your own machine; no cloud, no installation, never `belong`; run by you; NOT RUN)

**NOT RUN.** Written by Task C0.3 of [credentials-service/implementation-plan.md](credentials-service/implementation-plan.md) (its Check 90, D151); **no step of it has been run, and nothing here is a claim that it works.** It is the check the credentials service's tier A waits on: if Claude Code's subscription traffic does not survive a proxy that strips the incoming credential and attaches the real one, tier A is wasted work, and that plan's TIER A tasks are dropped before any product task starts (its Global Constraint 4). Its `FACT` lines settle one of [reconciliation.md](credentials-service/reconciliation.md)'s open items outright — **U1** (does the swap work at all, and which headers must pass through) — and settle **U4**'s client timeout and retry count; they contribute to the rest without settling them: **U4**'s third part, Claude Code's own wording when it meets a limit, is recorded only if step 7 can meet one, and **stays UNVERIFIED** when none can be met, as **U3** does; and it records the token's stated expiry, which is **U2**'s evidence but not its answer (**U2** is settled by Anthropic's documentation, read by you), and it observes a usage limit's status, error type and reset headers (**U3**) — the type from the error object's own `type`, through the proxy's opt-in `--error-fields`, which reads those two keys and no other — only if a limit can be met in the session — step 7 records it either way, and **U3 stays open** when none is observable, with the plan's 15-minute default back-off applying until Check 99 meets one. **The header list it records is what that plan's Task C1.7 — the Anthropic-OAuth upstream — is written against** (the plan's "What must be sequential", item 1: "C1.7's header list *is* Check 90's FACT lines"), and nothing else in this check is. **No agent runs any step of it**, on either side: you type every command, and you alone hold the token.

**Where it runs, and what it never touches.** On **your own machine**, in a **scratch directory the check makes for itself with `mktemp -d`** (mode 0700, under `$TMPDIR`, and checked to be outside every git work tree) — not a checkout, nothing that can be committed, and **never a path you type**: step 9 removes that directory recursively, so its name is the check's own and carries a marker file the check writes, which is what the delete re-checks. **No cloud project is named or touched, no Fugaro installation is involved, no `fugaro` command runs, and `belong`, EdgeWeb and EdgeServer appear nowhere in it.** The guard also **clears `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_VERTEX` and `CLAUDE_CODE_USE_BEDROCK` from the shell and stops on a configured `apiKeyHelper`** — the other ways `claude` can be given a credential — saying so without printing a value, so the control below can only succeed on the setup-token, and the proxy records **by name** which credential header the client sent so that the record says which credential the control used. The only credential is **your own** `claude setup-token` value, in a file you create; the program below reads it, attaches it, and never prints, logs or stores it. The environment carries a **placeholder** instead, and the placeholder is what would otherwise leave your machine. **Never paste the token — or any header value — into the register, into a pull request, or into a chat with an agent:** every `FACT` line below records header **names**, statuses and timings, never a header value, and the only body content anywhere in it is step 7's error `type` and `message`, which the proxy's opt-in `--error-fields` reads and nothing else in the program does.

### Check 90 step 1: setup and guard

```bash
# === Check 90: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every
# step: it is idempotent, and nothing below the hard stops runs. ===
# Hard stop 1: every other way `claude` can authenticate is cleared from this
# shell, because Claude Code reads them BEFORE CLAUDE_CODE_OAUTH_TOKEN. Two
# groups, and both would make the control a measurement of something else:
#   * ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN — an api-key credential, whose
#     header is x-api-key and which the proxy's swap does not touch at all;
#   * CLAUDE_CODE_USE_VERTEX / CLAUDE_CODE_USE_BEDROCK — the two cloud
#     providers, which authenticate through the cloud SDK and the ambient
#     cloud identity rather than through either header, so a call that goes
#     that way never carries the credential this check is about (the owner
#     uses Vertex, which is exactly why this stop is here);
#   * ANTHROPIC_BASE_URL — which would send the call somewhere other than
#     where the step thinks it goes.
# Values are never printed: only that a variable was set, and is now unset.
for v in ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL \
         CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_BEDROCK; do
  if printenv "$v" >/dev/null 2>&1; then
    unset "$v"
    echo "Check 90: $v was set in this shell and is now unset (its value is not printed)"
  fi
done
# Hard stop 1b: a settings file is the other way these reach `claude`, and
# nothing above touches it. Claude Code merges the settings files it reads and
# applies their `env` block to its own environment, so a file carrying
# ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL, a CLAUDE_CODE_USE_* provider switch or
# an `apiKeyHelper` has exactly the effect the `unset` above just removed.
# `env` is not a corner case: it is the channel the repository's own runner
# polices — `checkSettingsRouting`, internal/runner/agentenv.go, over
# `agent.SettingsFiles` and `agent.RoutingKeys`, internal/agent/settings.go:
# `apiKeyHelper`, and in `env` a base URL, a credential, a proxy variable or a
# CLAUDE_CODE_USE_* switch. A file that sets one STOPS the check; only the
# file's path and the KEY NAMES are printed, never a value.
#
# Which files: the two in the directory you run from, your user settings, the
# same file under $CLAUDE_CONFIG_DIR when that is set, and every regular file
# in Claude Code's managed settings directory — /etc/claude-code, the directory
# `internal/agent/settings.go`'s ManagedSettingsPath names. That is the list
# `agent.SettingsFiles` builds, which is what the runner itself reads. On a
# machine where Claude Code keeps its managed settings somewhere else (a macOS
# install, for instance), this guard does not read that file: say so in the
# FACT rather than assuming it was checked.
routing_keys() {   # $1: a settings file. Prints the routing key NAMES it sets.
  jq -r '
    (["ANTHROPIC_BASE_URL","ANTHROPIC_VERTEX_BASE_URL","ANTHROPIC_BEDROCK_BASE_URL",
      "ANTHROPIC_API_KEY","ANTHROPIC_AUTH_TOKEN","CLAUDE_CODE_SKIP_VERTEX_AUTH",
      "HTTP_PROXY","HTTPS_PROXY","ALL_PROXY","NO_PROXY"]) as $want
    | ([to_entries[] | select(.key | ascii_downcase == "apikeyhelper") | "apiKeyHelper"]
       + [(.env? // {}) | objects | keys[] | ascii_upcase
          | select(. as $u | ($want | index($u)) != null
                          or ($u | startswith("CLAUDE_CODE_USE_")))]
      ) | unique | .[]
  ' "$1" 2>/dev/null
}
check_settings() {  # $1: a settings file. Stops this check on a routing key.
  local keys
  keys=$(routing_keys "$1") \
    || { echo "Check 90 stopped: $1 is not settings JSON this guard can read, so it cannot say what Claude Code makes of it. Fix or remove it and re-run this block. Its value is not printed." >&2; exit 1; }
  [ -n "$keys" ] || return 0
  printf 'Check 90 stopped: %s sets %s, so claude may answer the control call with something other than the setup-token, and this check would then measure the wrong credential. Remove that key (or run the check where it is not set) and re-run this block. Only the key names are printed, never a value.\n' \
    "$1" "$(printf '%s' "$keys" | tr '\n' ' ')" >&2
  exit 1
}
for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
  [ -f "$f" ] && check_settings "$f"
done
[ -n "${CLAUDE_CONFIG_DIR:-}" ] && [ -f "$CLAUDE_CONFIG_DIR/settings.json" ] \
  && check_settings "$CLAUDE_CONFIG_DIR/settings.json"
# The managed settings directory, one level deep — what the runner reads. A
# machine that has none prints the line below instead, which is a FACT.
if [ -d /etc/claude-code ]; then
  while IFS= read -r f; do
    check_settings "$f"
  done < <(find /etc/claude-code -maxdepth 1 -type f 2>/dev/null)
else
  echo "Check 90: no /etc/claude-code on this machine, so this guard read no managed settings file. If Claude Code keeps one elsewhere here, it was NOT read: record that in the FACT."
fi
echo "Check 90: no settings file this guard reads sets apiKeyHelper or an env key that would send the call or its credential elsewhere"
# Hard stop 2: the scratch directory is one THIS CHECK makes, and it is the
# only thing step 9's `rm -rf` is ever allowed to name — never a path you
# typed. `mktemp -d` gives it the shape that makes that safe (absolute, under
# $TMPDIR, not / and not $HOME) and a marker file this check writes; the path
# is remembered in $SCRATCH_RECORD so every later step reuses this same
# directory instead of making another one.
SCRATCH_RECORD=${SCRATCH_RECORD:-$HOME/.chk90-scratch-dir}
if [ -s "$SCRATCH_RECORD" ]; then
  SCRATCH=$(cat "$SCRATCH_RECORD")
  [ -f "$SCRATCH/.chk90-mine" ] \
    || { echo "Check 90 stopped: $SCRATCH_RECORD names $SCRATCH, which carries no marker from this check — delete $SCRATCH_RECORD and re-run" >&2; exit 1; }
else
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk90.XXXXXXXX") || exit 1
  : > "$SCRATCH/.chk90-mine"
  printf '%s\n' "$SCRATCH" > "$SCRATCH_RECORD"
fi
mkdir -p "$SCRATCH" && cd "$SCRATCH" || exit 1
chmod 700 "$SCRATCH"
# ... and it is not inside a git work tree, so nothing written here can be
# committed by accident.
git rev-parse --show-toplevel >/dev/null 2>&1 \
  && { echo "Check 90 stopped: $SCRATCH is inside a git work tree — use a scratch directory outside every repository" >&2; exit 1; }
# Hard stop 3: the token file is non-empty, yours, and outside a work tree too.
TOKEN_FILE=${TOKEN_FILE:-$SCRATCH/token}
[ -s "$TOKEN_FILE" ] || { echo "Check 90 stopped: no token file at $TOKEN_FILE yet — do step 2, then re-run this block" >&2; exit 1; }
git -C "$(dirname "$TOKEN_FILE")" rev-parse --show-toplevel >/dev/null 2>&1 \
  && { echo "Check 90 stopped: the token file is inside a git work tree" >&2; exit 1; }
chmod 600 "$TOKEN_FILE"
# Hard stop 4: the client this check measures, recorded by version.
claude --version || { echo "Check 90 stopped: no claude on PATH" >&2; exit 1; }
PORT=${PORT:-8787}                        # any free loopback port
```

**The first time through, hard stop 3 stops you on purpose:** the token file does not exist until step 2 has made it, so run step 2, come back, and re-run this block — after that it passes unchanged at the start of every step.

**Why hard stop 1 is first, and why it clears five variables *and* the routing keys of every settings file it reads.** The control in step 4 is only a control if it runs on the setup-token and nothing else, and `claude` reaches for four other things before it: with `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` in the shell, `claude` authenticates with that instead; with `CLAUDE_CODE_USE_VERTEX` or `CLAUDE_CODE_USE_BEDROCK` set, it runs against that cloud provider and authenticates through the cloud SDK's own identity, sending neither credential header at all; and a configured `apiKeyHelper` is a command Claude Code runs for a credential, which no environment variable clears. Any of the four makes the swap "work" with the proxy not being what made it work, and U1 would be recorded as answered from a call that never used the credential this check is about — on a machine with Vertex configured (the owner's), a call with `CLAUDE_CODE_USE_VERTEX` left set measures Vertex. So the shell's five variables are cleared, which is why the control can only succeed on the setup-token; each step's commands set `ANTHROPIC_BASE_URL` and the placeholder themselves, for that one command, so clearing them here costs nothing. **A settings file is a fifth way in, and the `unset` above does not touch it:** Claude Code applies a settings file's `env` block to its own environment, so a file that carries `ANTHROPIC_API_KEY`, a `CLAUDE_CODE_USE_*` switch or any other `RoutingKeys` entry (`internal/agent/settings.go`; the runner's own `checkSettingsRouting`, `internal/runner/agentenv.go`) supplies exactly what the `unset` removed, and an `apiKeyHelper` supplies a credential no variable holds. With the shell cleared, such a file is the *effective* source, not a theoretical one — which is why hard stop 1b reads the settings files rather than only the shell, and stops on any of those keys rather than only on the helper. **Which header the client actually sent is not left to be inferred:** the proxy records the name (never a value) of the credential header each request carried, and step 4 runs the **observed control** — the same real token, through the proxy in `--no-swap` mode, which replaces nothing — precisely so that the control call carries that line; a line reading `x-api-key`, or `(neither)`, is a control that ran on something other than the setup-token, whatever the environment looks like. The direct control in the same step runs with no proxy in its path and therefore produces no `FACT credential-header` line at all: what it settles is the token, and the observed control is what says the client's own choice of credential matched what the swap assumes.

**Hard stops end the shell they are run in, and that is the intended outcome.** Every stop above and below is `exit 1`; pasted into an interactive shell it closes the window, and `#` comments are honoured by a non-interactive shell and not by an interactive `zsh` without `setopt interactive_comments`. Both are Check 43's paragraphs, which apply here word for word ("Paste the block into a file, not straight into an interactive zsh"): save this block to a file and source it — `. ./chk90-env.sh`, in the shell you will run the steps in — so the variables are in that shell *and* the stop lands in it. Nothing after a stop may run, so a stop that closes the window is the fail-safe end: to keep the window, run the sourced block in a subshell (`( . ./chk90-env.sh )`), read the message, fix what it names, and source it again in the shell the steps run in — the subshell's variables are gone with it, so a step run in it would reach `claude` with them unset.

**FACT:** `claude --version`; which of the five variables the guard reported as set and now unset (or that there was none of them to clear), whether the managed settings directory `/etc/claude-code` was there to be read, and that no settings file the guard reads sets `apiKeyHelper` or an `env` key that would route the call — the environment the control ran in, without any of their values and without any settings key's value; and the token file's length and mode (`wc -c`, `ls -l`) — a length and a mode, never the value.

### Check 90 step 2: the token file — yours, typed by you

`claude setup-token` prints the value once. Put it in `$TOKEN_FILE` **yourself**, from your own terminal, with the value on stdin rather than on a command line: `read -rs` keeps it out of your shell's history and out of `ps`, and the redirection is the only place it is written.

```bash
read -rs TOKEN && printf '%s' "$TOKEN" > "$TOKEN_FILE" && unset TOKEN
chmod 600 "$TOKEN_FILE"
```

**FACT (U2):** what `claude setup-token` said about the token's validity — its stated expiry, or that it has none — **verbatim and without the value**, plus the file's length and mode. That sentence is U2's **evidence, not its answer** — U2 is settled by **Anthropic's documentation**, which the owner reads, and [reconciliation.md](credentials-service/reconciliation.md)'s U2 row is where that answer is recorded, with this check's stated expiry as the evidence beside it.

### Check 90 step 3: the proxy, in full

Write this into `$SCRATCH/proxy.py`. It listens on loopback only, deletes the client's `Authorization` and `x-api-key`, attaches the token read from `--token-file`, forwards every other header as it arrived, streams the response back as it arrives, and prints **header names and statuses only** — never a header value. Its one other mode is `--no-swap`, which replaces nothing and attaches nothing: the client's own credential is forwarded unchanged, so the credential the client sent is the one the API sees. Step 4's **observed control** — the same real token in the environment, through this proxy — runs under it, because the point of that call is to record *which* credential header the client sent, and a mode that replaced the header would destroy the thing being observed. A request whose `Host` is not this loopback address and port is **refused with 403 and not forwarded**: loopback is reachable from any page a browser loads, so a proxy that answered any `Host` could be made to spend your quota, and a DNS-rebinding page could read what it relays. It also records **by name** which credential header the client sent (`authorization`, `x-api-key`, both, or neither), which is what says whether a call was made with the credential this check swaps at all. It prints nothing out of a body either, with the one opt-in exception step 7 turns on: `--error-fields` prints the error object's own `type` and `message` of an **error** response (status ≥ 400), reads no other key of the body, and prints a fixed placeholder when the body carries no error object or is not JSON. The option is off by default, and every other flag leaves a body unread: `--drop` and `--delay` are what steps 6 and 8 use.

```python
#!/usr/bin/env python3
"""Check 90's proxy. Loopback only.

It does one thing to the request: it deletes the client's Authorization and
x-api-key and attaches the token read from --token-file -- unless --no-swap is
given, in which case it changes no header and attaches no credential at all, so
the credential the client sent is the one the API sees. --no-swap is step 4's
observed control: that call exists to record WHICH credential header the client
sent, so it must run under a mode that does not replace it, and --token-file is
not read at all. Everything else is forwarded as it arrived, the response is
streamed back as it arrives, and the only things this program ever prints are
header NAMES, statuses, and -- with --error-fields, which is off by default --
an error response's own `type` and `message`.

Two things it refuses or records on the way, both of them about headers it
never prints a value of:

* A request whose Host header is not this loopback listener is refused with
  403 and never forwarded. Loopback is reachable from any page the browser
  loads, so a proxy that answered any Host could be made to spend the owner's
  subscription quota, and a DNS-rebinding page could read responses through
  this port. The client's own host is the only one that is served.
* Which CREDENTIAL header the client sent is recorded by NAME -- authorization,
  x-api-key, both, or neither -- because that is what says whether the control
  call was made with the credential this check is about at all.

--error-fields is what step 7 uses: its FACT (U3) is the status, the error type
and the reset headers of a usage-limit response, and an error body is a small
JSON object. error_fields() below reads exactly two keys of it -- the error
object's `type` and `message` -- and every other key is never indexed, so no
other value a body carries (a credential above all) can reach the output,
whatever the body holds.
"""
import argparse, http.client, http.server, json, time

ARGS = None
TOKEN = None            # the credential this program attaches; None: --no-swap
HOP = {"authorization", "x-api-key", "host", "content-length", "connection",
       "transfer-encoding", "accept-encoding", "expect", "proxy-connection",
       "keep-alive", "te", "upgrade"}
DROP = set()


def fact(*parts):
    print("FACT", *parts, flush=True)


def read_capped(resp, limit=65536):
    """At most `limit` bytes of the response body, and no more: the cap is what
    keeps a program that streams a response from buffering one, and an error
    body is a small object."""
    out = b""
    while len(out) < limit:
        chunk = resp.read1(min(65536, limit - len(out)))
        if not chunk:
            break
        out += chunk
    return out


def loopback_host(host, port):
    """True when Host is this check's own listener and nothing else. Loopback is
    reachable from any page a browser loads: with no Host check, a page could
    make this proxy spend the owner's subscription quota, and a DNS-rebinding
    page could read the responses it relays. Only the client's own address and
    port are served; the alternatives accepted are the two spellings of it."""
    return (host or "").strip().lower() in {"127.0.0.1:%d" % port,
                                            "localhost:%d" % port,
                                            "[::1]:%d" % port}


def credential_headers(headers):
    """The NAME of the credential header the request carried -- never a value.
    The control call is only a control if it carried the credential this check
    swaps, and the client's own choice is what says whether it did: an API-key
    or Vertex run sends x-api-key or neither, however the environment looks."""
    return [n for n in ("authorization", "x-api-key") if headers.get(n)]


def forward_names(headers, no_swap):
    """The header NAMES forwarded upstream, other than the ones this program
    writes itself. The client's Authorization and x-api-key are NOT among them
    -- this program replaces them with the token -- unless --no-swap is on, in
    which case nothing is replaced and they are forwarded exactly as they
    arrived: the credential the client sent is then the one the API sees, which
    is what makes step 4's observed control a control rather than a call the
    proxy answered for."""
    names = [n for n in sorted({k.lower() for k in headers.keys()})
             if n not in HOP and n not in DROP]
    if no_swap:
        names += [n for n in ("authorization", "x-api-key") if headers.get(n)]
    return names


def error_fields(body):
    """The only two keys of a body this program ever reads: the error object's
    own `type` and `message`. Nothing else is indexed, so nothing else can be
    printed; a body that is not JSON, or carries no error object, or carries
    those two keys as something other than strings, prints a placeholder."""
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

    def log_message(self, *a):     # BaseHTTPRequestHandler logs the request line
        pass                       # and the client's address; we do not.

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
        if not loopback_host(self.headers.get("Host"), ARGS.port):
            # The value is not printed, only that the request was refused: the
            # address a rebinding page used is the page's, not this check's.
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
        conn = http.client.HTTPSConnection(ARGS.upstream, timeout=ARGS.timeout)
        try:
            conn.putrequest(self.command, self.path, skip_host=True,
                            skip_accept_encoding=True)
            conn.putheader("Host", ARGS.upstream)
            if TOKEN is not None:          # --no-swap: the client's own, above
                conn.putheader("Authorization", "Bearer " + TOKEN)
            for n in sent:
                conn.putheader(n, self.headers[n])
            conn.putheader("Content-Length", str(len(body)))
            conn.endheaders(body)
            resp = conn.getresponse()
        except Exception as e:
            fact("upstream-error", type(e).__name__)
            self.send_error(502, "chk90proxy: upstream error")
            return
        fact("response", resp.status, resp.reason, "headers:",
             ",".join(n.lower() for n, _ in resp.getheaders()))
        head = b""
        if ARGS.error_fields and resp.status >= 400:
            head = read_capped(resp)       # an error body: small, and read here
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
                chunk = resp.read1(65536)   # as soon as there is one: SSE
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
    ap.add_argument("--token-file",
                    help="the token attached to every request in place of the "
                         "client's own credential; required unless --no-swap")
    ap.add_argument("--no-swap", action="store_true",
                    help="replace no header and attach no credential: forward "
                         "the client's own credential unchanged, and only "
                         "record its name (step 4's observed control)")
    ap.add_argument("--upstream", default="api.anthropic.com")
    ap.add_argument("--delay", type=float, default=0.0)
    ap.add_argument("--drop", action="append", default=[])
    ap.add_argument("--timeout", type=float, default=3600.0)
    ap.add_argument("--error-fields", action="store_true",
                    help="print the error object's own type and message of an "
                         "error response (status >= 400), and nothing else of "
                         "any body")
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
```

### Check 90 step 4: the control, then the swap (U1)

The control first — the same call with **no proxy** and the real token in the environment — so that a token which does not work at all is never mistaken for a swap that failed:

```bash
CLAUDE_CODE_OAUTH_TOKEN="$(cat "$TOKEN_FILE")" claude -p "say ok"
```

**That call produces no `FACT credential-header` line, because no proxy is in its path** — nothing observed it, and a line cannot be read off a call nothing observed. What it settles is the token: it works or it does not. **Which credential the client sends with this environment is the next call's business**, and it is the same token and the same environment: the **observed control**, through the proxy in `--no-swap` mode, which replaces no header and attaches no credential, so the client's own credential is the one the API sees and the proxy's `FACT credential-header` line names what the client sent.

```bash
[ -e "$SCRATCH/proxy.pid" ] && kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null   # a rerun of this step
python3 "$SCRATCH/proxy.py" --no-swap --port "$PORT" > "$SCRATCH/proxy.log" 2>&1 &
echo $! > "$SCRATCH/proxy.pid"
sleep 1
ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="$(cat "$TOKEN_FILE")" \
  claude -p "say ok"
```

Then the swap. The placeholder below is a literal that is not a credential, it is what Claude Code puts in the `Authorization` header, and the proxy replaces it before anything leaves your machine:

```bash
kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null
python3 "$SCRATCH/proxy.py" --token-file "$TOKEN_FILE" --port "$PORT" > "$SCRATCH/proxy.log" 2>&1 &
echo $! > "$SCRATCH/proxy.pid"
sleep 1
ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="placeholder-not-a-credential" \
  claude -p "say ok"
```

**FACT:** the `FACT credential-header` line of the **observed control** and of the swap — the **name** of the credential header the client actually sent, never a value — which is the check that these calls ran on the credential this check is about: the observed control's line reading `x-api-key`, or `(neither)` (a Vertex or Bedrock run, or one on an `apiKeyHelper`), means the environment still routes the client somewhere else, so the control measured something other than the setup-token and its outcome says nothing about the swap — record that and stop rather than reading U1 out of it, and the swap's own success in that case would be the token the proxy attached rather than the client's credential. The **direct control** above has no such line: no proxy is in its path, nothing observed that call, and its evidence is its own outcome in an environment hard stops 1 and 1b have cleared of every other credential. Whether each call succeeded, the exact text either printed, and the header names on the `FACT request` line in `$SCRATCH/proxy.log`. (`ANTHROPIC_API_KEY="placeholder-not-a-credential"` runs the other client path — the API-key one, whose header is `x-api-key` — and is worth recording separately if you want the two side by side; the subscription path, `CLAUDE_CODE_OAUTH_TOKEN`, is the one tier A is built on.)

### Check 90 step 5: a long prompt, streaming through the proxy

```bash
ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="placeholder-not-a-credential" \
  claude -p "Write the numbers from 1 to 200, one per line, slowly."
```

**FACT:** whether it completed, roughly how long it took, and whether the text arrived progressively rather than in one burst at the end — a burst is the buffering a proxy in the path must not add.

### Check 90 step 6: dropping each optional header in turn (U1 — the list tier A is written against)

For each name the step-4 log printed, other than `authorization` and `x-api-key` (which the proxy removes by design), restart the proxy with `--drop <name>` and re-run step 4's **swap** call — the one whose credential the proxy supplies, since that is the path a dropped header has to survive:

```bash
kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null
python3 "$SCRATCH/proxy.py" --token-file "$TOKEN_FILE" --port "$PORT" --drop "anthropic-beta" > "$SCRATCH/proxy.log" 2>&1 &
echo $! > "$SCRATCH/proxy.pid"; sleep 1
ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="placeholder-not-a-credential" claude -p "say ok"
```

**FACT:** per header name, the call's outcome and — when it failed — the failure, so the recorded list separates the names that **must** pass through from the ones that are optional. **This list is the answer the credentials service's Task C1.7 (the Anthropic-OAuth upstream) is written against**; nothing else in this check is.

### Check 90 step 7: a usage or rate limit, if one can be observed (U3, and U4's wording for it if one is met)

Drive enough subscription traffic to meet the account's own limit — or wait for one — and record the response when it comes. Run that traffic through the proxy with `--error-fields` on, so the error object's `type` and `message` land in the log as a `FACT error-fields` line; a proxy started without the flag never reads a body, and one started with it still prints nothing of a body that is not an error response:

```bash
kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null
python3 "$SCRATCH/proxy.py" --token-file "$TOKEN_FILE" --port "$PORT" --error-fields > "$SCRATCH/proxy.log" 2>&1 &
echo $! > "$SCRATCH/proxy.pid"; sleep 1
ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="placeholder-not-a-credential" claude -p "say ok"
```

**The check passes without one:** if no limit can be observed in the session, record exactly that, and **U3 stays open** with the plan's 15-minute default back-off applying until the cascade's own check (Check 99) meets one.

**FACT (U3):** the HTTP status, the error's own `type` and `message` (the two fields `--error-fields` reads out of an error body, and the only two it can print — the `FACT error-fields` line, or one of its two placeholders, `(no error object in the body)` and `(unreadable body: not JSON)`, when a body carries neither), and every reset-carrying header verbatim — for example `retry-after` or an `anthropic-ratelimit-*-reset`. Those headers are what the mark-and-back-off rule has to read, and the error type is what tells a rate limit from an overload ([reconciliation.md](credentials-service/reconciliation.md)'s F15); a guess at either is what this step exists to replace.

**FACT (U4, and only if a limit was met):** what `claude` itself printed for the limit — its own wording, verbatim, which is **U4's third part** and the text the skill's loose "usage limit" match would have read ([reconciliation.md](credentials-service/reconciliation.md)'s F15 keeps it UNVERIFIED until a run records it). If the client never reported one, because the limit was seen only on the wire, record exactly that and nothing else: **that part of U4 stays UNVERIFIED**, as U3's own answer does when no limit can be met.

### Check 90 step 8: the client's timeout and retries (U4)

Restart the proxy with `--delay 90` and re-run step 4's **swap** call — again the proxied one, since this measures the client against a proxy that answers slowly — timing it; raise the delay if the first was survived.

```bash
kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null
python3 "$SCRATCH/proxy.py" --token-file "$TOKEN_FILE" --port "$PORT" --delay 90 > "$SCRATCH/proxy.log" 2>&1 &
echo $! > "$SCRATCH/proxy.pid"; sleep 1
time ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" CLAUDE_CODE_OAUTH_TOKEN="placeholder-not-a-credential" claude -p "say ok"
```

**FACT (U4):** how long the client waited, whether it retried (the `FACT request` lines count the attempts) or gave up, the wall clock to each outcome, and the exact text of a give-up. This is the measurement the plan's D138 turns on: the wait belongs in the runner, and the service answers a typed "exhausted until T" rather than holding one call open.

### Check 90 step 9: Restore

```bash
kill "$(cat "$SCRATCH/proxy.pid")" 2>/dev/null
# The recursive delete names only the directory hard stop 2 made, and both
# guards are re-checked HERE, immediately before it: a variable that no longer
# holds what the check put there stops the check instead of being removed.
case "$SCRATCH" in
  ""|/|"$HOME"|"$HOME"/) echo "Check 90 stopped: refusing to remove a scratch directory of '$SCRATCH'" >&2; exit 1;;
esac
[ -f "$SCRATCH/.chk90-mine" ] \
  || { echo "Check 90 stopped: $SCRATCH carries no marker from this check — nothing was removed" >&2; exit 1; }
rm -rf "$SCRATCH"                     # the token file goes with it; copy it out first if you keep it
rm -f "$SCRATCH_RECORD"               # the check forgets the directory it made
pgrep -f proxy.py                     # nothing: the proxy is gone
```

The token file is yours to keep or delete, and revoking a `claude setup-token` value is done from the Claude account's own settings, not here. **If you want to keep it, copy it out before this step** — mode 600, outside every repository — because the delete above takes the scratch directory and everything in it. **Nothing was created in any cloud project, no repository was touched and no `fugaro` installation was involved** — the Restore's assertion is that the scratch directory and the proxy are gone.

**Passes when:** the guard's hard stops held — in particular that step 1 reported each of `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_VERTEX` and `CLAUDE_CODE_USE_BEDROCK` it cleared, or that there was none to clear, and that no settings file it reads sets `apiKeyHelper` or an `env` key that would route the call — because a control run with any of those in play proves nothing about the swap — every call of this check **that goes through the proxy** (step 4's observed control and its swap, and steps 5 to 8) has a `FACT credential-header` line reading `authorization` (the credential this check swaps; `x-api-key` or `(neither)` is a call made on something else, and it is recorded and stops the check rather than answering U1) — the direct control at the top of step 4 carries no such line at all, having no proxy in its path, and its own part is that it succeeded with the real token in the environment hard stops 1 and 1b cleared — the control, the observed control and the swapped call all succeed (step 4), the long prompt streams through the proxy (step 5), the header-name list is recorded (step 6), U2's stated validity and U3's answer — or the explicit record that no usage limit was observable — are recorded (steps 2 and 7), U4's timings and retry count are recorded (step 8) — with, when step 7 met a limit, Claude Code's own wording for it — and the scratch directory and the proxy are gone (step 9).

**Fails when:** the swapped call fails **and the control succeeded**. Separate the two before concluding anything: a 401 whose body says the credential is invalid, or a request the API never saw, is a bug in the little program above and not a finding about subscription traffic. A failure that survives that look is the plan's **Global Constraint 4**: the TIER A tasks are dropped, the service ships with a B-only cascade, and the plan is amended in a docs PR before any further task starts. A token that fails the **control** too says nothing about the swap — record it and stop. And a swapped call whose `FACT credential-header` line is not `authorization`, or an **observed control** whose line is not, is not a failure of the swap either: it is a call made on something else, recorded and stopped on, and U1 is not read out of it.

**Paste back:** `claude --version`; which of the five credential variables step 1 cleared (or that there was none), whether `/etc/claude-code` was there to read, and that no settings file it reads sets `apiKeyHelper` or a routing `env` key; the token's stated validity with the file's length and mode; the `FACT credential-header` lines — the observed control's and the swap's, since the direct control has none — and the control's, the observed control's and the swapped call's outcomes and texts; every `FACT request` and `FACT response` line (header **names** and statuses); the per-header drop results; the usage-limit status, the error object's own `type` and `message` and the reset headers, or the explicit record that none was observable; the U4 timings with the attempt count; and, when step 7 met a limit, what the client itself printed for it. **Never paste the token, a header value, or a body** — step 7's `type` and `message` are the only body fields the record carries. Until this check runs, tier A is **UNVERIFIED** and its row above stays **NOT RUN**.
