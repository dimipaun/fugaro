#!/usr/bin/env bash
# Tests for scripts/live-checks/check145.sh, against a fake Bitbucket Cloud on
# loopback: nothing here reaches the network, no real credential exists, and the
# consumer key and secret the test feeds in are sentinels asserted absent from
# everything the run prints or leaves behind.
#
# The fake answers as a hostile endpoint as well as an ordinary one: every body
# it sends carries back the consumer secret it was given in the request's
# Authorization header, and in its "hostile" mode the fields the check reads --
# the granted scopes and an error code -- carry that secret too. An
# endpoint (or anything between this machine and it) that prints back what it
# was given is exactly the case a check that handles a credential has to
# survive, so the ordinary run exercises it too.
#
# Usage: bash scripts/live-checks/check145_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check145.sh
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
pids=()
cleanup_all() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  if [ -n "${CHECK145_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 400)$(printf '%s' "$1" | grep -E '^FACT (aborted|U[0-9]+|write-refused):' | sed 's/^/ ... /')" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|U[0-9]+):' | sed 's/^/ ... last run: /')"
}

# The sentinels are deliberately not credential shapes: nothing here is a real
# key or secret, and the assertions below hold that they never survive a run.
KEY_SENTINEL='fake-consumer-key-for-check-145-tests'
SECRET_SENTINEL='FAKE-CONSUMER-SECRET-FOR-CHECK-145-TESTS-NOT-A-CREDENTIAL'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(U1 U2 U3 U4 U5 U8 U11 U12 U14 U15 consumer-file workspaces write-refused restore)

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  cat >"$1/fake.py" <<'FAKE_EOF'
#!/usr/bin/env python3
"""A fake Bitbucket Cloud on loopback, for check145.sh's test only.

It never reaches the network and it holds no credential of its own: the
consumer secret it echoes back is one it decoded from the request it was just
sent. It records every access token it mints, so the test can assert that a
minted token reaches nothing the check prints.
"""
import base64, json, os, secrets, sys, http.server

MODE = os.environ.get("FAKE145_MODE", "ok")
TOKENS = os.environ.get("FAKE145_TOKENS", "")
STATE = {"secret": "", "grants": 0, "revoked": set()}


def note(token):
    if TOKENS:
        with open(TOKENS, "a", encoding="utf-8") as fh:
            fh.write(token + "\n")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, status, doc):
        # Every body carries back the consumer secret this fake was given: an
        # endpoint that prints what it was handed must not be able to reach the
        # check's output through any field it sends.
        doc = dict(doc)
        doc["echo"] = STATE["secret"]
        raw = json.dumps(doc).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def basic(self):
        head = self.headers.get("Authorization") or ""
        if not head.startswith("Basic "):
            return ""
        try:
            return base64.b64decode(head[6:]).decode("utf-8", "replace")
        except Exception:
            return ""

    def bearer(self):
        head = self.headers.get("Authorization") or ""
        return head[7:] if head.startswith("Bearer ") else ""

    def do_POST(self):
        self.route("POST")

    def do_GET(self):
        self.route("GET")

    def route(self, method):
        path = self.path.split("?", 1)[0].rstrip("/")
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""

        if path == "/site/oauth2/access_token":
            given = self.basic()
            if ":" in given:
                STATE["secret"] = given.split(":", 1)[1]
            if MODE == "refused":
                self.reply(401, {"error": {"code": STATE["secret"], "message": STATE["secret"]}})
                return
            STATE["grants"] += 1
            token = "chk145tok-%d-%s" % (STATE["grants"], secrets.token_hex(8))
            note(token)
            doc = {"access_token": token, "token_type": "bearer",
                   "expires_in": 3600 if MODE == "short-life" else 7200,
                   "scopes": "repository pullrequest"}
            if MODE == "hostile":
                doc["scopes"] = STATE["secret"]
            if MODE != "no-refresh":
                doc["refresh_token"] = "chk145refresh-%d" % STATE["grants"]
            self.reply(200, doc)
            return

        if path == "/site/oauth2/revoke":
            if MODE == "revoke-404":
                self.reply(404, {"error": {"code": "not_found"}})
                return
            for part in raw.decode("utf-8", "replace").split("&"):
                if part.startswith("token="):
                    STATE["revoked"].add(part[len("token="):])
            self.reply(200, {})
            return

        token = self.bearer()
        if not token or token in STATE["revoked"]:
            self.reply(401, {"error": {"code": "invalid_token"}})
            return

        if path == "/2.0/user/permissions/workspaces":
            self.reply(200, {"values": [{"workspace": {"slug": "sandbox-a"}, "permission": "admin"}]})
            return

        if path.startswith("/2.0/workspaces/"):
            rest = path[len("/2.0/workspaces/"):]
            slug, _, sub = rest.partition("/")
            if sub == "":
                self.reply(200, {"slug": slug, "type": "workspace", "is_private": True})
            elif sub == "members":
                if MODE == "members-forbidden":
                    self.reply(403, {"error": {"code": "insufficient_scope"}})
                else:
                    self.reply(200, {"size": 3, "values": []})
            elif sub == "ip-allowlist":
                if MODE == "allowlist-offered":
                    self.reply(200, {"size": 1, "values": []})
                else:
                    self.reply(404, {"error": {"code": "not_found"}})
            else:
                self.reply(404, {"error": {"code": "not_found"}})
            return

        if path.startswith("/2.0/repositories/"):
            slug = path[len("/2.0/repositories/"):]
            if "/" in slug:
                self.reply(404, {"error": {"code": "not_found"}})
            elif method == "POST":
                if MODE == "write-allowed":
                    self.reply(201, {"slug": "a-write-that-must-never-happen"})
                elif MODE == "write-unsettled":
                    self.reply(400, {"error": {"code": "bad_request"}})
                else:
                    self.reply(403, {"error": {"code": "insufficient_scope"}})
            elif slug == "sandbox-b" and MODE == "no-reach":
                self.reply(403, {"error": {"code": "insufficient_scope"}})
            else:
                self.reply(200, {"size": 2 if slug == "sandbox-a" else 1, "values": []})
            return

        self.reply(404, {"error": {"code": "not_found"}})


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print("fake145 listening on 127.0.0.1:%d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
FAKE_EOF
}

FAKE_PORT=""
start_fake() { # start_fake DIR MODE
  write_fake "$1"
  : >"$1/tokens"
  FAKE145_MODE=$2 FAKE145_TOKENS="$1/tokens" python3 "$1/fake.py" \
    >"$1/fake.out" 2>"$1/fake.err" &
  local pid=$!
  pids+=("$pid")
  local i=0
  while [ "$i" -lt 200 ]; do
    FAKE_PORT=$(sed -n 's/^fake145 listening on 127\.0\.0\.1:\([0-9]\{1,\}\)$/\1/p' "$1/fake.out" | head -n 1)
    [ -n "$FAKE_PORT" ] && return 0
    kill -0 "$pid" 2>/dev/null || { fail "the fake upstream died: $(tail -n 3 "$1/fake.err" | tr '\n' ' ')"; return 1; }
    sleep 0.05
    i=$((i + 1))
  done
  fail "the fake upstream did not report that it is listening"
  return 1
}

# ---------------------------------------------------------------- running it
write_consumer() { # write_consumer FILE [key] [secret]
  local file=$1 key=${2:-$KEY_SENTINEL} secret=${3:-$SECRET_SENTINEL}
  printf '{"key": "%s", "secret": "%s", "workspace": "sandbox-a"}\n' "$key" "$secret" >"$file"
  chmod 600 "$file"
}

# run145 DIR MODE — runs the script once against the fake. RUN145_ARGS appends
# arguments, and RUN145_ENV names environment variables (NAME=value) to set, so
# a test can add a bad override or point the check at a name of its own.
# Sets $status, $out, $err.
run145() {
  local dir=$work/$1 mode=$2
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"
  start_fake "$dir" "$mode" || return 1
  status=0
  out=""
  env TMPDIR="$dir/tmp" \
    CHECK145_OAUTH_URL="http://127.0.0.1:$FAKE_PORT" \
    CHECK145_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK145_CURL_SECONDS=20 \
    ${RUN145_ENV[@]+"${RUN145_ENV[@]}"} \
    bash "$script" \
    --consumer-file "$dir/consumer.json" --workspace sandbox-a --second-workspace sandbox-b \
    ${RUN145_ARGS[@]+"${RUN145_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  RUN145_ARGS=()
  RUN145_ENV=()
}

# assert_no_secret DIR WHAT — nothing the run printed, and no file it left
# behind, carries the consumer's key, its secret or a token it minted. The two
# files this TEST writes with a sentinel in them on purpose are its input
# fixtures, and they are the only ones a sentinel may be in.
assert_no_secret() {
  local dir=$1 what=$2 hits token
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$SECRET_SENTINEL" "$what: the secret in the output"
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$KEY_SENTINEL" "$what: the key in the output"
  hits=$(grep -rl -- "$SECRET_SENTINEL" "$dir" 2>/dev/null | grep -v -F -x -e "$dir/consumer.json" -e "$dir/no-secret.json" || true)
  [ -z "$hits" ] || fail "$what: the consumer secret is in $hits"
  hits=$(grep -rl -- "$KEY_SENTINEL" "$dir" 2>/dev/null | grep -v -F -x -e "$dir/consumer.json" -e "$dir/no-secret.json" || true)
  [ -z "$hits" ] || fail "$what: the consumer key is in $hits"
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$token" "$what: a minted token in the output"
  done < <(cat "$dir/tokens" 2>/dev/null || true)
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 145 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

# --------------------------------------------------------------- the tests
test_happy_path() {
  run145 happy ok
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 145 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT U1: PASS — " "happy path: the lifetime is measured"
  expect_contains "$out" "7200" "happy path: the lifetime the endpoint reported"
  expect_contains "$out" "FACT U2: PASS — " "happy path: the refresh token and the revocation"
  expect_contains "$out" "refresh" "happy path: the refresh token's presence is recorded"
  expect_contains "$out" "FACT U3: PASS — " "happy path: the reach is measured"
  expect_contains "$out" "sandbox-b" "happy path: the second workspace is the one measured"
  expect_contains "$out" "FACT U4: PASS — " "happy path: the plan"
  expect_contains "$out" "FACT U5: PASS — " "happy path: the seat count"
  expect_contains "$out" "FACT U8: INFO — " "happy path: U8 is reported, not guessed"
  expect_contains "$out" "FACT U11: PASS — " "happy path: the scopes are listed"
  expect_contains "$out" "repository" "happy path: the scope words the endpoint listed"
  expect_contains "$out" "FACT U12: PASS — " "happy path: distinctness"
  expect_contains "$out" "FACT U14: PASS — " "happy path: the creator's rights"
  expect_contains "$out" "admin" "happy path: the permission the endpoint reported"
  expect_contains "$out" "FACT U15: INFO — " "happy path: U15 is reported, not guessed"
  expect_contains "$out" "FACT write-refused: PASS — " "happy path: a write with the read token is refused"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"

  # The check's promise about the consumer file: it names the file it read, and
  # nothing from that file reaches the block.
  expect_contains "$out" "consumer.json" "happy path: the file it read is named"

  assert_no_secret "$work/happy" "happy path"

  # Cleanup on success: the scratch directory and the files holding the
  # credential go with it.
  local scratch
  scratch=$(scratch_of "$work/happy")
  [ -n "$scratch" ] || fail "happy path: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory $scratch survived the run"; fi
  local left
  left=$(find "$work/happy/tmp" -mindepth 1 2>/dev/null || true)
  [ -z "$left" ] || fail "happy path: files were left in TMPDIR: $left"
}

# U3's other answer: the token does not reach the second workspace, and the
# block says so rather than reporting a reach it did not measure.
test_no_reach() {
  run145 no-reach no-reach
  expect_status "$status" 0 "no reach"
  expect_contains "$out" "FACT U3: PASS — " "no reach: the reach is measured"
  expect_contains "$out" "sandbox-b" "no reach: the second workspace is named"
  expect_contains "$out" "403" "no reach: the status the second workspace answered"
  expect_absent "$out" "reach beyond" "no reach: a reach that was not measured is not claimed"
  assert_no_secret "$work/no-reach" "no reach"
}

# The read-only guard is measured, never assumed: a token that CAN write is a
# FAIL, not a row that passes because the endpoint was reached.
test_write_allowed_is_a_fail() {
  run145 write-allowed write-allowed
  expect_status "$status" 1 "write allowed"
  expect_contains "$out" "FACT write-refused: FAIL — " "write allowed: the guard is reported as broken"
  expect_contains "$out" "CHECK 145 RESULT END (exit 1)" "write allowed: the block says the run failed"
  assert_no_secret "$work/write-allowed" "write allowed"
}

# A probe that does not settle -- neither the refusal the read mint depends on
# nor a completed write -- must not read as a pass: the row FAILs, says
# UNVERIFIED, and the whole run fails with it.
test_write_probe_unsettled_is_not_a_pass() {
  run145 write-unsettled write-unsettled
  expect_status "$status" 1 "write unsettled"
  expect_contains "$out" "FACT write-refused: FAIL — " "write unsettled: the row is not a pass"
  expect_contains "$out" "UNVERIFIED" "write unsettled: the row says the guard was not measured"
  expect_contains "$out" "400" "write unsettled: the status the endpoint answered"
  expect_contains "$out" "CHECK 145 RESULT END (exit 1)" "write unsettled: the block says the run failed"
  assert_no_secret "$work/write-unsettled" "write unsettled"
}

# A hostile endpoint: the granted scopes and an error code carry the consumer
# secret back, and neither may reach the block. (A member count cannot carry it:
# the members listing reports a number, never a string from the request.)
test_a_hostile_endpoint_prints_nothing_it_was_given() {
  run145 hostile hostile
  expect_status "$status" 0 "hostile"
  expect_block_keys "$out" "hostile"
  expect_contains "$out" "FACT U11: PASS — " "hostile: U11 is still answered"
  expect_absent "$out" "FACT U11: PASS — repository" "hostile: the echo is not read as a scope list"
  expect_contains "$out" "FACT U5: PASS — " "hostile: U5 is still answered"
  assert_no_secret "$work/hostile" "hostile"
}

# U1's other answer: a token the endpoint gives a shorter life, so the row
# reports the lifetime this endpoint gave rather than one baked into the script.
test_short_life() {
  run145 short-life short-life
  expect_status "$status" 0 "short life"
  expect_contains "$out" "FACT U1: PASS — " "short life: the lifetime is measured"
  expect_contains "$out" "3600" "short life: the lifetime the endpoint reported"
  assert_no_secret "$work/short-life" "short life"
}

# U2 without a refresh token coming back: the grant and the revocation are still
# measured, and the row states which of the two the endpoint gave.
test_no_refresh() {
  run145 no-refresh no-refresh
  expect_status "$status" 0 "no refresh"
  expect_contains "$out" "FACT U2: PASS — " "no refresh: the grant and the revocation are measured"
  expect_contains "$out" "carried no refresh token" "no refresh: the absent refresh token is stated"
  assert_no_secret "$work/no-refresh" "no refresh"
}

# U2's other answer: the revoke endpoint refuses, so the row must report that the
# token could not be revoked rather than a revocation this run never saw.
test_revoke_refused() {
  run145 revoke-404 revoke-404
  expect_status "$status" 0 "revoke refused"
  expect_contains "$out" "FACT U2: INFO — " "revoke refused: U2 stays unverified"
  expect_contains "$out" "UNVERIFIED" "revoke refused: the row says so"
  expect_contains "$out" "404" "revoke refused: the status the endpoint answered"
  assert_no_secret "$work/revoke-404" "revoke refused"
}

# U5's other answer: the members listing is refused, so the seat count was never
# read and the row is UNVERIFIED rather than a count nobody saw.
test_seats_not_read() {
  run145 members-forbidden members-forbidden
  expect_status "$status" 0 "seats not read"
  expect_contains "$out" "FACT U5: INFO — " "seats not read: U5 stays unverified"
  expect_contains "$out" "UNVERIFIED" "seats not read: the row says so"
  assert_no_secret "$work/members-forbidden" "seats not read"
}

# U8's other answer: the plan does expose an allowlist. That is still not what
# U8 asks -- whether a request from an address that is NOT allowlisted is
# refused -- so the row stays INFO.
test_allowlist_offered() {
  run145 allowlist-offered allowlist-offered
  expect_status "$status" 0 "allowlist offered"
  expect_contains "$out" "FACT U8: INFO — " "allowlist offered: U8 stays unverified"
  expect_contains "$out" "UNVERIFIED" "allowlist offered: the row says so"
  expect_contains "$out" "200" "allowlist offered: the status the endpoint answered"
  assert_no_secret "$work/allowlist-offered" "allowlist offered"
}

# An exchange the endpoint refuses, with a body that echoes the secret back.
# Nothing about the consumer can be measured with no token, so the run stops --
# and the stop, the block and the exit status carry no secret.
test_refused_exchange() {
  run145 refused refused
  expect_status "$status" 2 "refused exchange"
  expect_contains "$out" "FACT aborted: FAIL — " "refused exchange: the stop is recorded"
  expect_contains "$out" "401" "refused exchange: the status the endpoint answered"
  expect_contains "$out" "CHECK 145 RESULT END (exit 2)" "refused exchange: the block"
  assert_no_secret "$work/refused" "refused exchange"
}

# The two guards that keep this check on a sandbox: a URL that is not loopback
# (where a real run's token would go) and the workspace it must never name. Each
# case writes its stdout and stderr where assert_no_secret reads them, and is
# checked on its own, so a later case cannot overwrite the evidence of an
# earlier one.
test_guards() {
  local dir=$work/guards
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"

  status=0
  env TMPDIR="$dir/tmp" CHECK145_API_URL="https://api.bitbucket.org/2.0" \
    bash "$script" --consumer-file "$dir/consumer.json" --workspace sandbox-a \
    --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "non-loopback API URL"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "loopback" "non-loopback API URL: the guard names why"
  assert_no_secret "$dir" "non-loopback API URL"

  status=0
  env TMPDIR="$dir/tmp" \
    bash "$script" --consumer-file "$dir/consumer.json" --workspace belong \
    --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "belong"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "belong" "belong: the guard names the workspace"
  assert_no_secret "$dir" "belong"

  status=0
  env TMPDIR="$dir/tmp" \
    bash "$script" --consumer-file "$dir/consumer.json" --workspace sandbox-a \
    >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "missing --second-workspace"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "usage" "missing --second-workspace: the usage line"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "--second-workspace" "missing --second-workspace: the flag is named"
  assert_no_secret "$dir" "missing --second-workspace"
}

# Every override shape that is not exactly http://127.0.0.1:<port> is refused
# before a request is made. The '@' one is the one that matters: curl reads
# everything before it as userinfo and connects to the host AFTER it, so a guard
# that matched the address with a glob would hand the consumer's Basic credential
# to whatever host the value named.
#
# Both variables are tried, and the OTHER one is always the fake, so a guard that
# let a value through would send its request to the fake rather than anywhere
# real -- and the fake's token file is then the evidence that something was sent:
# every shape that can be read as an address does reach the fake under a looser
# guard, and the ones curl would choke on are caught by the message a refusal
# gives.
test_endpoint_override_shapes_are_refused() {
  local dir=$work/override-shapes host name other value
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"
  start_fake "$dir" ok || return 1
  host="127.0.0.1:$FAKE_PORT"
  for name in CHECK145_OAUTH_URL CHECK145_API_URL; do
    if [ "$name" = CHECK145_OAUTH_URL ]; then other=CHECK145_API_URL; else other=CHECK145_OAUTH_URL; fi
    for value in \
      "http://127.0.0.1:1@$host/2.0" \
      "http://127.0.0.1:1@evil.invalid/2.0" \
      "http://$host?x" \
      "http://$host#x" \
      "http://$host\\x" \
      "http://$host x"; do
      status=0
      env TMPDIR="$dir/tmp" "$name=$value" "$other=http://$host" \
        bash "$script" --consumer-file "$dir/consumer.json" --workspace sandbox-a \
        --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
      out=$(cat "$dir/out")
      expect_status "$status" 2 "$name='$value'"
      expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "loopback" "$name='$value': the guard names why"
      assert_no_secret "$dir" "$name='$value'"
      [ ! -s "$dir/tokens" ] \
        || fail "$name='$value': a request reached the fake, so the guard let the credential out"
    done
  done
}

# The dev installation is refused whatever case it is spelled in: the guard
# compares a lowercased value, so `bElong` cannot slip past a check that only
# knows the three spellings someone thought of.
test_belong_in_any_case_is_refused() {
  local dir=$work/belong-case ws
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"
  for ws in bElong BELONG BeLoNg; do
    status=0
    env TMPDIR="$dir/tmp" \
      bash "$script" --consumer-file "$dir/consumer.json" --workspace "$ws" \
      --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
    expect_status "$status" 2 "belong spelled '$ws'"
    expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "belong" "belong spelled '$ws': the guard names the workspace"
    assert_no_secret "$dir" "belong spelled '$ws'"
  done
}

# The file the owner names is the one place the consumer lives, and a run that
# cannot read it stops before it makes a request -- with no value in what it
# says about why. Each case is checked on its own, the two fixtures this test
# writes with a sentinel in them excepted (assert_no_secret knows them).
test_consumer_file_complaints() {
  local dir=$work/consumer
  mkdir -p "$dir/tmp"

  status=0
  env TMPDIR="$dir/tmp" bash "$script" --consumer-file "$dir/missing.json" \
    --workspace sandbox-a --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "missing consumer file"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "missing.json" "missing consumer file: the path is named"
  assert_no_secret "$dir" "missing consumer file"

  printf '{"key": "%s"}\n' "$KEY_SENTINEL" >"$dir/no-secret.json"
  status=0
  env TMPDIR="$dir/tmp" bash "$script" --consumer-file "$dir/no-secret.json" \
    --workspace sandbox-a --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "consumer file with no secret"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "secret" "consumer file with no secret: the missing field is named"
  assert_no_secret "$dir" "consumer file with no secret"

  # A file the check cannot read is refused rather than sent anywhere.
  printf 'not json at all\n' >"$dir/not-json.json"
  status=0
  env TMPDIR="$dir/tmp" bash "$script" --consumer-file "$dir/not-json.json" \
    --workspace sandbox-a --second-workspace sandbox-b >"$dir/out" 2>"$dir/err" || status=$?
  expect_status "$status" 2 "consumer file that is not JSON"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "not-json.json" "consumer file that is not JSON: the path is named"
  assert_no_secret "$dir" "consumer file that is not JSON"
}

# A run that arrived with tracing on is refused: `bash -x` prints every command
# as it runs, into a log the owner would then paste back.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"
  status=0
  out=$(env TMPDIR="$dir/tmp" CHECK145_OAUTH_URL="http://127.0.0.1:1" \
    CHECK145_API_URL="http://127.0.0.1:1/2.0" \
    bash -x "$script" --consumer-file "$dir/consumer.json" --workspace sandbox-a \
    --second-workspace sandbox-b 2>"$dir/err") || status=$?
  expect_status "$status" 2 "xtrace"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "+ " "xtrace: the trace really was on"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "tracing" "xtrace: the guard names why"
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$SECRET_SENTINEL" "xtrace: no secret"
}

# THE RED STEP, kept as a test: the assertions above are only worth anything if
# they catch a script that prints the consumer file. This one does, and this
# test fails if the assertions stop noticing.
test_the_secret_assertions_catch_an_echoing_script() {
  local dir=$work/detector caught
  mkdir -p "$dir/tmp"
  write_consumer "$dir/consumer.json"
  cat >"$dir/echoing.sh" <<'ECHO_EOF'
#!/usr/bin/env bash
# A script that prints the credential it was handed: the failure the check's own
# test exists to catch.
cat "$2"
ECHO_EOF
  bash "$dir/echoing.sh" --consumer-file "$dir/consumer.json" \
    >"$dir/out" 2>"$dir/err" || true
  # The assertions run in a subshell with a counter of their own, so the four
  # FAIL lines this provokes on purpose are COUNTED instead of printed: output
  # that reads 'FAIL' beside a suite that passed is output nobody trusts.
  caught=$(
    failures=0
    fail() { failures=$((failures + 1)); }
    assert_no_secret "$dir" "detector"
    printf '%s' "$failures"
  )
  [ "${caught:-0}" -eq 4 ] \
    || fail "detector: an echoing script raised $caught of the four secret assertions (the secret and the key in the output, the secret and the key in a file left behind), so at least one of them is vacuous"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i (each test
# works in its own directory under $work), CHECK_TEST_ONLY=name runs that one
# test alone. Neither set: all run.
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

t test_happy_path
t test_no_reach
t test_write_allowed_is_a_fail
t test_a_hostile_endpoint_prints_nothing_it_was_given
t test_write_probe_unsettled_is_not_a_pass
t test_short_life
t test_no_refresh
t test_revoke_refused
t test_seats_not_read
t test_allowlist_offered
t test_refused_exchange
t test_guards
t test_endpoint_override_shapes_are_refused
t test_belong_in_any_case_is_refused
t test_consumer_file_complaints
t test_xtrace_is_refused
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check145_test: $failures failure(s)" >&2
  exit 1
fi
echo "check145_test: all tests passed" >&2
