#!/usr/bin/env bash
# Tests for scripts/live-checks/check590.sh, against fake tools on PATH and a
# fake Bitbucket Cloud on loopback: nothing here reaches the network, no real
# credential exists, no Fugaro installation is read, and the three tokens the
# test feeds in are sentinels asserted absent from everything the run prints or
# leaves behind.
#
# What the fakes are: `fugaro` (doctor and creds set), `git` (the scratch dir is
# not a work tree, and `ls-remote`), each a few lines of bash that answer from
# FAKE590_MODE and write what they were asked to a log — and a python3 HTTP
# server that answers the API the way Bitbucket Cloud does, with a different
# answer for each of the three token types, a scope header the modes can drop or
# narrow, and a hostile mode whose bodies echo back the bearer token they were
# sent.
#
# Usage: bash scripts/live-checks/check590_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check590.sh
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
pids=()
cleanup_all() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  if [ -n "${CHECK590_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
# EXPECT_FAIL is set only by the last case, whose whole point is to be REFUSED by
# the assertions above: its failures are the reading, and they are counted and
# then undone, so the suite's footer still means what it says.
fail() {
  if [ -n "${EXPECT_FAIL:-}" ]; then echo "expected: $*" >&2; else echo "FAIL: $*" >&2; fi
  failures=$((failures + 1))
}
# rows_of_block TEXT — the block's own rows, one short line each, so a failure
# names what the run recorded instead of pasting the whole block into the log.
rows_of_block() {
  printf '%s\n' "$1" | grep '^FACT ' | cut -c1-90 | tr '\n' '|' | head -c 900
}

expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; the rows read: $(rows_of_block "$1")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
# expect_no_request DIR WHAT — the fake Bitbucket answered nothing for that run.
# Every request it answers is appended to the run's fake.log, so this is the
# reading of "no token was ever sent anywhere": a guard that refused only after a
# read would leave a line here, and one that is checked by a file nothing writes
# proves nothing at all.
expect_no_request() { # dir what
  [ ! -s "$1/fake.log" ] || fail "$2: a request reached the API: $(tr '\n' ' ' <"$1/fake.log")"
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}
# expect_last_line OUT WANT WHAT — the block's own last line, exactly. It is the
# line a reader takes as the answer, and the one the exit status has to agree
# with.
expect_last_line() { # out want what
  local last
  last=$(printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1)
  [ "$last" = "$2" ] || fail "$3: the block's last line reads '$last', want '$2'"
}

# The sentinels are deliberately not credential shapes: nothing here is a real
# token, and the assertions below hold that they never survive a run.
TOKEN_SENTINEL='FAKE-REPO-TOKEN-FOR-CHECK-590-TESTS-NOT-A-CREDENTIAL'
PROJECT_SENTINEL='FAKE-PROJECT-TOKEN-FOR-CHECK-590-TESTS-NOT-A-CREDENTIAL'
WORKSPACE_SENTINEL='FAKE-WORKSPACE-TOKEN-FOR-CHECK-590-TESTS-NOT-A-CREDENTIAL'
# What the fake installation's own doctor reports, and the project UUID the test
# hands the check for the project token's record.
GCP_PROJECT_NAME='fugaro-dev'
PROJECT_UUID='{a1b2c3d4-0000-0000-0000-00000000c590}'

# Every fact key the run's block must carry, whatever it measured.
# The project's UUID or key for the run about to happen, as an ARRAY (see
# run590): empty means the test's own default, --project-uuid $PROJECT_UUID.
RUN590_PROJECT_ARGS=()

BLOCK_KEYS=(installation inputs xtrace U1-repository U2-repository verifier-repo
  U1-project U2-project U5-project verifier-project
  U1-workspace U2-workspace verifier-workspace restore)

# --------------------------------------------------------------------- the fake
write_fakes() { # dir
  local dir=$1
  cat >"$dir/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check590.sh's test only: it answers the two calls the
# check makes and records what it was asked. It never reads a token: the store's
# value arrives on stdin and goes to /dev/null, so nothing it does can print one.
set -u
mode=${FAKE590_MODE:-ok}
log=${FAKE590_LOG:-/dev/null}
printf '%s\n' "$*" >>"$log"
case "${1:-}" in
doctor)
  [ "$mode" = doctor-fails ] && { echo "doctor: no usable credentials on this machine" >&2; exit 2; }
  name=fugaro
  gcp=fugaro-dev
  [ "$mode" = doctor-wrong-name ] && name=otherproject
  [ "$mode" = doctor-wrong-gcp ] && gcp=another-project
  printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"us-east5"}}\n' "$name" "$gcp"
  # exits 1 with its document, as the real doctor does when a check is a warning
  [ "$mode" = doctor-warns ] && exit 1
  exit 0
  ;;
creds)
  # The typed add path. The value comes in on stdin and is DRAINED, never read
  # into a variable: this fake must not be able to print one.
  cat >/dev/null
  shift
  [ "${1:-}" = set ] && shift
  name=${1:-}
  type=""
  coverage=0
  while [ $# -gt 0 ]; do
    case "$1" in
    --type) type=$2; shift 2 ;;
    --coverage) coverage=$((coverage + 1)); shift 2 ;;
    --project-uuid | --project-key | --project) shift 2 ;;
    *) shift ;;
    esac
  done
  case "$mode" in
  set-refused*)
    echo "nothing was written: the token's own read of its coverage answered 403 (the check is run before anything is written, D2352)" >&2
    exit 1
    ;;
  set-bad-type)
    printf '{"project":"fugaro","secret":"fugaro-bitbucket-token--x","version":"3","bitbucket":{"name":"%s","type":"workspace","coverage":["dimi_bmb/*"],"identity":"a1b2c3d4-0000-0000-0000-000000000001","write_verified":false}}\n' "$name"
    exit 0
    ;;
  set-write-verified)
    printf '{"project":"fugaro","secret":"fugaro-%s","version":"2","bitbucket":{"name":"%s","type":"%s","coverage":["dimi_bmb/sandbox-a"],"identity":"a1b2c3d4-0000-0000-0000-000000000001","write_verified":true}}\n' "$name" "$name" "$type"
    exit 0
    ;;
  esac
  # The ordinary answer, and the one that matters: a REPOSITORY token has no
  # identity at GET /2.0/user (R14), and the other two do. Mode
  # `set-no-identity` is the regression where a project or workspace token's
  # identity is not read either: the write is real and the row is UNVERIFIED.
  identity=""
  case "$type" in
  repo) [ "$mode" = set-repo-identity ] && identity="${FAKE590_IDENTITY:-a1b2c3d4-0000-0000-0000-000000000001}" ;;
  *) [ "$mode" = set-no-identity ] || identity="${FAKE590_IDENTITY:-a1b2c3d4-0000-0000-0000-000000000001}" ;;
  esac
  printf '{"project":"fugaro","secret":"fugaro-%s","version":"2","bitbucket":{"name":"%s","type":"%s","coverage":["dimi_bmb/%s"],"project_uuid":"{a1b2c3d4-0000-0000-0000-00000000c590}"%s,"write_verified":false}}\n' \
    "$name" "$name" "$type" "$coverage" "${identity:+,\"identity\":\"$identity\"}"
  exit 0
  ;;
esac
echo "fake fugaro: unknown subcommand ${1:-}" >&2
exit 2
FUGARO_EOF
  chmod +x "$dir/fugaro"

  cat >"$dir/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git`, for check590.sh's test only. `rev-parse --show-toplevel` fails,
# because the scratch directory is not a work tree (which is what the check asks
# it), and `ls-remote --heads` answers like a repository the project token can
# read. The credential is in a config file the check writes, so nothing here sees
# it.
set -u
case "${1:-}" in
rev-parse)
  exit 128
  ;;
ls-remote)
  if [ "${FAKE590_GIT_MODE:-ok}" = refused ]; then
    echo "fatal: Authentication failed for 'https://bitbucket.org/dimi_bmb/sandbox-a.git/'" >&2
    exit 128
  fi
  printf '%s\trefs/heads/main\n' 2f4a1c9d2f4a1c9d2f4a1c9d2f4a1c9d2f4a1c9d
  printf '%s\trefs/heads/fugaro/check590\n' 9d1c4a2f9d1c4a2f9d1c4a2f9d1c4a2f9d1c4a2f
  exit 0
  ;;
esac
exit 0
GIT_EOF
  chmod +x "$dir/git"

  cat >"$dir/fake.py" <<'FAKE_EOF'
#!/usr/bin/env python3
"""A fake Bitbucket Cloud on loopback, for check590.sh's test only.

It never reaches the network and holds no credential of its own: the token it
echoes back is one it read out of the request it was just sent. Every body
carries that echo back, so a check that printed a response field as it arrived
would fail this suite rather than pass it.

The three tokens are told apart by their VALUE, and each answers the way its own
type does: a repository token is refused at /user (R14), a project and a
workspace token answer with their own `kind` and an identity. Modes change one
answer at a time so the check's rows can be read apart.
"""
import json, os, sys, http.server

REPO = os.environ.get("FAKE590_REPO_TOKEN", "")
PROJECT = os.environ.get("FAKE590_PROJECT_TOKEN", "")
WORKSPACE = os.environ.get("FAKE590_WORKSPACE_TOKEN", "")
APILOG = os.environ.get("FAKE590_API_LOG", "")
MODE = os.environ.get("FAKE590_API_MODE", "ok")
KINDS = {REPO: "repository_access_token", PROJECT: "project_access_token", WORKSPACE: "workspace_access_token"}
SCOPES = "repository:read,repository:write,pullrequest:read,pullrequest:write"
UUID = "{9f1d0c2a-0000-0000-0000-000000000001}"


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, status, doc, scopes=None):
        doc = dict(doc)
        doc["echo"] = self.token
        raw = json.dumps(doc).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        if scopes:
            self.send_header("X-OAuth-Scopes", scopes)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def refuse(self, status, code):
        # The code itself is the token in "hostile" mode: an endpoint that echoes
        # what it was given must not be able to reach the block through a field
        # it chooses.
        self.reply(status, {"error": {"code": self.token if MODE == "hostile" else code,
                                      "message": self.token}})

    def scopes(self):
        if MODE == "no-scope-header":
            return None
        if MODE == "partial-scopes":
            return "repository:read"
        return SCOPES

    def do_GET(self):
        path = self.path.split("?", 1)[0].rstrip("/")
        if APILOG:
            with open(APILOG, "a", encoding="utf-8") as fh:
                fh.write("GET %s\n" % path)
        head = self.headers.get("Authorization") or ""
        self.token = head[7:] if head.startswith("Bearer ") else ""
        if self.token not in KINDS:
            self.refuse(401, "invalid_token")
            return

        if path == "/2.0/user":
            if self.token == REPO and MODE != "user-200-repo":
                # The repository token's own answer, and the design's accepted
                # one (R14, O7): the endpoint refuses the mechanism.
                self.refuse(403, "forbidden")
                return
            if MODE == "user-200-no-identity":
                self.reply(200, {"uuid": "", "display_name": "", "nickname": ""}, self.scopes())
                return
            if MODE == "user-unknown-kind":
                self.reply(200, {"uuid": UUID, "display_name": "Fugaro sandbox", "kind": "personal_access_token"}, self.scopes())
                return
            kind = KINDS[self.token] if self.token != REPO else "repository_access_token"
            self.reply(200, {"uuid": UUID, "display_name": "Fugaro sandbox", "kind": kind}, self.scopes())
            return

        if path.startswith("/2.0/repositories/"):
            rest = path[len("/2.0/repositories/"):]
            parts = [p for p in rest.split("/") if p]
            if len(parts) == 2:
                if self.token == WORKSPACE and MODE == "workspace-refused":
                    self.refuse(403, "forbidden")
                    return
                # Bitbucket carries the scope header on every authenticated
                # answer, not only on /user: the check reads it off the
                # repository where the /user answer carried none (a repository
                # token's own /user is a 403).
                self.reply(200, {"full_name": "%s/%s" % (parts[0], parts[1]),
                                 "project": {"key": "SANDBOX"}, "is_private": True}, self.scopes())
                return
            self.reply(404, {"error": {"code": "not_found"}})
            return

        self.reply(404, {"error": {"code": "not_found"}})


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print("fake590 listening on 127.0.0.1:%d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
FAKE_EOF
}

FAKE_PORT=""
# start_fake DIR — starts the fake Bitbucket and waits, bounded, for the port it
# reports on its own startup line.
#
# The wait is a poll and not a read taken the moment the server goes to the
# background: the redirect that makes fake.out belongs to the child, so a read
# that lands before the child has opened it finds NO file at all — and sed
# failing on a missing file (this suite runs with `set -e` and `pipefail`) ends
# the suite where it stands, with sed's own complaint and no case named. The
# bound is 20s of 0.1s probes, far above a python start on a loaded machine; a
# fake that died is reported at once with what it printed rather than waited out.
start_fake() { # start_fake DIR — the fakes go in DIR/bin, which is put on PATH
  write_fakes "$1/bin"
  : >"$1/fake.log"
  FAKE590_API_MODE=${RUN590_API_MODE:-ok} FAKE590_REPO_TOKEN="$TOKEN_SENTINEL" \
    FAKE590_PROJECT_TOKEN="$PROJECT_SENTINEL" FAKE590_WORKSPACE_TOKEN="$WORKSPACE_SENTINEL" \
    FAKE590_API_LOG="$1/fake.log" \
    python3 "$1/bin/fake.py" >"$1/fake.out" 2>"$1/fake.err" &
  local pid=$!
  pids+=("$pid")
  local out=$1/fake.out i=0
  FAKE_PORT=""
  while [ "$i" -lt 200 ]; do
    if [ -e "$out" ]; then
      FAKE_PORT=$(sed -n 's/^fake590 listening on 127\.0\.0\.1:\([0-9]\{1,\}\)$/\1/p' "$out" | head -n 1)
      [ -n "$FAKE_PORT" ] && return 0
    fi
    kill -0 "$pid" 2>/dev/null || { fail "the fake Bitbucket died: $(tail -n 3 "$1/fake.err" | tr '\n' ' ')"; return 1; }
    sleep 0.1
    i=$((i + 1))
  done
  fail "the fake Bitbucket never reported 'fake590 listening on 127.0.0.1:<port>' in $out within 20s"
  return 1
}

# ---------------------------------------------------------------- running it
write_token() { # write_token FILE [value]
  printf '%s\n' "${2:-$TOKEN_SENTINEL}" >"$1"
  chmod 600 "$1"
}

# run590 DIR — runs the check once against the fakes. RUN590_ARGS appends
# arguments, RUN590_MODE picks the fake fugaro's behaviour, RUN590_API_MODE the
# fake Bitbucket's, RUN590_GIT_MODE the fake git's, RUN590_ENV names environment
# variables (NAME=value) to set, and RUN590_SHELL replaces the interpreter the
# check is started with (the one place a tracing shell can be arranged). Sets
# $status, $out and $err.
run590() {
  local dir=$work/$1
  # The project's UUID or key, as an ARRAY: a quoted word nested inside a
  # `${VAR:-default}` expansion is one argument, not two, and the check would see
  # `--project-uuid {…}` as a single unknown flag.
  local project_args=(--project-uuid "$PROJECT_UUID")
  if [ ${#RUN590_PROJECT_ARGS[@]} -gt 0 ]; then project_args=("${RUN590_PROJECT_ARGS[@]}"); fi
  mkdir -p "$dir/tmp" "$dir/bin" "$dir/state"
  write_token "$dir/token" "$TOKEN_SENTINEL"
  write_token "$dir/token-project" "$PROJECT_SENTINEL"
  write_token "$dir/token-workspace" "$WORKSPACE_SENTINEL"
  start_fake "$dir" || return 1
  status=0
  out=""
  env ${RUN590_UNSET_HOME:+-u HOME} PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    CHECK590_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK590_CURL_SECONDS=20 \
    FAKE590_MODE="${RUN590_MODE:-ok}" FAKE590_LOG="$dir/fugaro.log" \
    FAKE590_GIT_MODE="${RUN590_GIT_MODE:-ok}" \
    ${RUN590_ENV[@]+"${RUN590_ENV[@]}"} \
    ${RUN590_SHELL:-bash} "$script" \
    --repo "${RUN590_REPO:-dimi_bmb/sandbox-a}" \
    --token-file "${RUN590_TOKEN_FILE:-$dir/token}" \
    --project-token-file "${RUN590_PROJECT_TOKEN_FILE:-$dir/token-project}" \
    --workspace-token-file "${RUN590_WORKSPACE_TOKEN_FILE:-$dir/token-workspace}" \
    "${project_args[@]}" \
    ${RUN590_ARGS[@]+"${RUN590_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  RUN590_ARGS=()
  RUN590_ENV=()
  RUN590_MODE=ok
  RUN590_API_MODE=ok
  RUN590_GIT_MODE=""
  RUN590_UNSET_HOME=""
  RUN590_SHELL=""
  RUN590_REPO=""
  RUN590_TOKEN_FILE=""
  RUN590_PROJECT_TOKEN_FILE=""
  RUN590_WORKSPACE_TOKEN_FILE=""
  RUN590_PROJECT_ARGS=()
}

# assert_no_secret DIR WHAT — nothing the run printed, and no file it left
# behind, carries any of the three sentinels. The three files this TEST writes
# with a sentinel in them on purpose are its own fixtures, and they are the only
# ones any of them may be in.
assert_no_secret() {
  local dir=$1 what=$2 hits sentinel
  for sentinel in "$TOKEN_SENTINEL" "$PROJECT_SENTINEL" "$WORKSPACE_SENTINEL"; do
    expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$sentinel" "$what: the token in the output"
  done
  hits=$(grep -rl -e "$TOKEN_SENTINEL" -e "$PROJECT_SENTINEL" -e "$WORKSPACE_SENTINEL" "$dir" 2>/dev/null \
    | grep -v -F -x -e "$dir/token" -e "$dir/token-project" -e "$dir/token-workspace" || true)
  [ -z "$hits" ] || fail "$what: a token is in $hits"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 590 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

# expect_store_argv DIR WHAT — every `creds set` call the run made, pinned to the
# flags the register documents. The fake `fugaro` logs its own argv, so this
# reads what the check actually ran rather than a string the check printed: the
# type, the coverage form of that type, the project's UUID or key and --json (a
# --json write asks nothing at a terminal, and this check has none), with the
# value on stdin and never on the command line.
expect_store_argv() {
  local dir=$1 what=$2 bad count
  count=$(grep -c '^creds set ' "$dir/fugaro.log" 2>/dev/null || true)
  [ "${count:-0}" -eq 3 ] || fail "$what: the run made ${count:-0} 'creds set' call(s), want 3 (one per type)"
  # Every call takes --json (a write that asks nothing at a terminal), and no
  # call carries any of the three values in its argv: the value goes in on stdin.
  bad=$(grep '^creds set ' "$dir/fugaro.log" 2>/dev/null | grep -v -- '--json' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "$what: a store call reads '$bad': every one takes --json"
  local line
  for line in "$TOKEN_SENTINEL" "$PROJECT_SENTINEL" "$WORKSPACE_SENTINEL"; do
    expect_absent "$(cat "$dir/fugaro.log")" "$line" "$what: a value in a store call's argv"
  done
  expect_contains "$(cat "$dir/fugaro.log")" "creds set bitbucket-token--ck590-repo --type repo --project fugaro --json --coverage dimi_bmb/sandbox-a" "$what: the repository token's argv"
  expect_contains "$(cat "$dir/fugaro.log")" "creds set bitbucket-token--ck590-project --type project --project fugaro --json --coverage dimi_bmb --project-uuid $PROJECT_UUID" "$what: the project token's argv"
  expect_contains "$(cat "$dir/fugaro.log")" "creds set bitbucket-token--ck590-workspace --type workspace --project fugaro --json --coverage dimi_bmb/*" "$what: the workspace token's argv"
}

# --------------------------------------------------------------- the tests
# The all-PASS run: EVERY criterion is measured, which is what makes the block's
# last line PASS and the exit status 0.
test_happy_path() {
  run590 happy
  expect_status "$status" 0 "the happy path"
  expect_last_line "$out" "CHECK 590 RESULT: PASS (exit 0)" "the happy path"
  expect_block_keys "$out" "the happy path"
  expect_contains "$out" "FACT U1-repository: PASS" "the repository token's /user answer is a 403, accepted (R14)"
  expect_contains "$out" "FACT U2-repository: PASS" "the repository token's scope header"
  expect_contains "$out" "FACT U1-project: PASS" "the project token's /user answer"
  expect_contains "$out" "kind 'project_access_token'" "the project token's own kind is named"
  expect_contains "$out" "FACT U5-project: PASS" "the project token is accepted as x-token-auth and as Bearer"
  expect_contains "$out" "FACT verifier-repo: PASS" "the add path accepted the repository token"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory and the credential files are removed"
  expect_store_argv "$work/happy" "the happy path"
  assert_no_secret "$work/happy" "the happy path"
  local scratch
  scratch=$(scratch_of "$work/happy")
  [ -n "$scratch" ] || fail "the happy path: the run announced no scratch directory"
  [ ! -e "$scratch" ] || fail "the happy path: the scratch directory is still there: $scratch"
}

# The three types are three rows per question, and the type is in the row's own
# name: a check that read ONE token three times, or read one type's answer for
# another's row, would pass a single-token suite.
test_each_type_is_read_itself() {
  run590 types
  expect_status "$status" 0 "the three types"
  expect_contains "$out" "kind 'workspace_access_token'" "the workspace token's own kind is named"
  expect_contains "$out" "kind 'project_access_token'" "the project token's own kind is named"
  expect_contains "$out" "FACT U1-repository: PASS" "the repository token's row"
  local calls
  calls=$(grep -c '^GET /2.0/user' "$work/types/fake.log" || true)
  [ "${calls:-0}" -eq 3 ] || fail "the three types: the check read /user ${calls:-0} time(s), want 3 (one per token)"
  local repo_reads
  repo_reads=$(grep -c "^GET /2.0/repositories/dimi_bmb/sandbox-a$" "$work/types/fake.log" || true)
  # Three premise reads, U5's REST half, and the FALLBACK that reads the scope
  # header off the repository when the /user answer carried none -- which it does
  # for a repository token, because that answer is a 403.
  [ "${repo_reads:-0}" -eq 5 ] || fail "the three types: the repository was read ${repo_reads:-0} time(s), want 5 (three premise reads, U5's REST half and the repository token's scope fallback)"
}

# U1's other reading: a repository token that DOES answer /user with a kind. The
# row is a PASS and says so (it is a fact about Bitbucket, not a failure), and
# the verifier then records an identity for a repository token.
test_a_repository_token_with_an_identity() {
  RUN590_API_MODE=user-200-repo RUN590_MODE=set-repo-identity run590 repo-identity
  expect_status "$status" 0 "the repository token with an identity"
  expect_contains "$out" "FACT U1-repository: PASS" "the 200 answer is a reading, not a failure"
  expect_contains "$out" "kind 'repository_access_token'" "the repository token's own kind"
  expect_contains "$out" "FACT verifier-repo: PASS" "the verifier row"
  expect_contains "$out" "does NOT happen" "the verifier row says this is the case R14 says does not happen"
}

# A row that could not be measured is UNVERIFIED, and a block with one is
# INCOMPLETE rather than a pass (exit 3): a scope header Bitbucket did not send,
# and an identity a project token's check did not read.
test_no_scope_header_is_incomplete() {
  RUN590_API_MODE=no-scope-header run590 no-scopes
  expect_status "$status" 3 "no scope header"
  expect_last_line "$out" "CHECK 590 RESULT: INCOMPLETE (exit 3)" "no scope header"
  expect_contains "$out" "FACT U2-project: UNVERIFIED" "the scope row is unmeasured"
  expect_contains "$out" "carries NO scope header" "the row says what was not read"
  expect_contains "$out" "FACT U1-project: PASS" "the other rows are measured"
}

test_no_identity_for_a_project_token_is_incomplete() {
  RUN590_MODE=set-no-identity run590 no-identity
  expect_status "$status" 3 "a project token with no identity"
  expect_contains "$out" "FACT verifier-project: UNVERIFIED" "the verifier row is unmeasured"
  expect_contains "$out" "the identity a run's own set would trust is empty" "the row says why"
  expect_contains "$out" "FACT verifier-repo: PASS" "the repository token's row is unaffected"
}

# A narrowed scope header is a FAIL: a token with no write scope cannot push.
test_narrow_scopes_are_a_failure() {
  RUN590_API_MODE=partial-scopes run590 scopes
  expect_status "$status" 1 "narrow scopes"
  expect_last_line "$out" "CHECK 590 RESULT: FAIL (exit 1)" "narrow scopes"
  expect_contains "$out" "FACT U2-repository: FAIL" "the scope row failed"
}

# The verifier's own answer, read three ways: a document naming another type than
# the one asked for, write_verified=true (D2353 says it is never proven), and a
# refusal from the add-time check, which stops the run before anything is
# written.
test_verifier_document_is_checked() {
  RUN590_MODE=set-bad-type run590 bad-type
  expect_status "$status" 1 "a document naming another type"
  expect_contains "$out" "FACT verifier-repo: FAIL" "the record is not the one asked for"
}

test_write_verified_is_a_failure() {
  RUN590_MODE=set-write-verified run590 write-verified
  expect_status "$status" 1 "write_verified=true"
  expect_contains "$out" "cannot have been verified" "the row says why it is a failure"
}

test_a_refused_add_path_stops_the_run() {
  RUN590_MODE=set-refused run590 refused
  expect_status "$status" 2 "a refused add-time check"
  expect_last_line "$out" "CHECK 590 RESULT: INCOMPLETE (exit 2)" "a refused add-time check"
  expect_contains "$out" "FACT aborted: FAIL" "the block says the run stopped"
  expect_contains "$out" "nothing was written" "the refusal is carried in the reason"
  # The workspace token's rows were never reached, and the block says so rather
  # than reading them as measured.
  expect_contains "$out" "FACT verifier-workspace: FAIL — not reached" "the rows after the stop are not reached"
}

# The premise: a token that cannot read the repository stops the run before any
# /user answer is read, because a row about a token that does not reach the
# repository measures nothing.
test_a_token_that_does_not_reach_the_repository_stops_the_run() {
  RUN590_API_MODE=workspace-refused run590 reach
  expect_status "$status" 2 "the workspace token refused"
  expect_contains "$out" "the workspace token answered 403" "the reason names the token"
  expect_contains "$out" "FACT inputs: FAIL — not reached" "no row was measured off a premise that failed"
  expect_contains "$out" "FACT aborted: FAIL" "the block says the run stopped"
}

# U5's git half: a project token git refuses is a FAIL, and it is the row that
# says so rather than the run.
test_git_refusal_is_a_failure() {
  RUN590_GIT_MODE=refused run590 git-refused
  expect_status "$status" 1 "git refuses the project token"
  expect_last_line "$out" "CHECK 590 RESULT: FAIL (exit 1)" "git refuses the project token"
  expect_contains "$out" "FACT U5-project: FAIL" "the U5 row failed"
  expect_contains "$out" "NOT accepted as 'x-token-auth'" "the row says what was read"
}

# The guards, each of which must refuse WITHOUT sending a token anywhere: the
# installation, the workspace, the repository and the GCP project.
test_belong_is_refused_everywhere() {
  # The workspace and a repository of it, together: the consistency check between
  # --repo and --workspace passes (the pair agrees), so it is guard_belong that
  # refuses this run, and the refusal names belong rather than talking about a
  # mismatch.
  RUN590_ARGS=(--workspace belong)
  RUN590_REPO=belong/sandbox-a
  run590 belong-1
  expect_status "$status" 2 "the workspace named belong"
  expect_no_request "$work/belong-1" "the workspace named belong"
  expect_contains "$err" "must never touch" "the refusal says why"

  RUN590_ARGS=(--project belong)
  run590 belong-2
  expect_status "$status" 2 "the installation named belong"
  expect_no_request "$work/belong-2" "the installation named belong"
  expect_contains "$err" "must never touch" "the refusal says why"

  RUN590_ARGS=(--gcp-project belong)
  run590 belong-3
  expect_status "$status" 2 "the GCP project named belong"
  expect_no_request "$work/belong-3" "the GCP project named belong"
  expect_contains "$err" "must never touch" "the refusal says why"
}

test_the_installation_and_the_gcp_project_are_held() {
  RUN590_MODE=doctor-fails run590 doctor-fails
  expect_status "$status" 2 "a doctor that cannot read the installation"
  expect_no_request "$work/doctor-fails" "a doctor that cannot read the installation"
  expect_contains "$err" "needs the installation named on this machine" "the refusal says what is wrong"
  expect_contains "$(cat "$work/doctor-fails/fugaro.log")" "doctor --project fugaro --json" "the doctor was read by name, not from the environment"

  RUN590_MODE=doctor-wrong-name run590 doctor-name
  expect_status "$status" 2 "a doctor naming another installation"
  expect_no_request "$work/doctor-name" "a doctor naming another installation"
  expect_contains "$err" "otherproject" "the refusal names what doctor said"

  RUN590_MODE=doctor-wrong-gcp run590 doctor-gcp
  expect_status "$status" 2 "an installation in another GCP project"
  expect_no_request "$work/doctor-gcp" "an installation in another GCP project"
  expect_contains "$err" "another-project" "the refusal names the GCP project doctor read"
}

# Doctor exits 1 whenever any of its checks is a warning, and still prints its
# document: that is an answer and not a failure.
test_doctor_exit_1_is_an_answer() {
  RUN590_MODE=doctor-warns run590 doctor-warns
  expect_status "$status" 0 "a doctor that warned"
  expect_contains "$out" "FACT installation: PASS" "a warning is not a refusal"
}

# The dry run and the usage touch nothing at all, and neither needs `fugaro`,
# `git` or `curl` on PATH: the one output the owner can have without an
# installation must not be denied them for a missing tool.
test_dry_run_touches_nothing() {
  RUN590_ARGS=(--dry-run)
  run590 dry-run
  expect_status "$status" 0 "the dry run"
  expect_contains "$out" "This is the PLAN" "the dry run prints the plan"
  expect_no_request "$work/dry-run" "the dry run"
  [ ! -s "$work/dry-run/fugaro.log" ] || fail "the dry run: fugaro was called: $(cat "$work/dry-run/fugaro.log")"
  expect_absent "$out" "CHECK 590 RESULT" "the dry run prints no block (a plan is not a run whose criteria were left unmeasured)"
}

test_the_plan_and_the_usage_need_no_cloud_tools() {
  local empty=$work/no-tools
  mkdir -p "$empty/bin" "$empty/tmp"
  local status=0 out=""
  PATH="$empty/bin:/usr/bin:/bin" TMPDIR="$empty/tmp" \
    bash "$script" --repo dimi_bmb/sandbox-a --token-file /nonexistent/repo \
    --project-token-file /nonexistent/project --workspace-token-file /nonexistent/workspace \
    --project-uuid "$PROJECT_UUID" --dry-run >"$empty/plan" 2>"$empty/plan.err" || status=$?
  [ "$status" -eq 0 ] || fail "the plan without cloud tools: expected exit 0, got $status: $(head -n 2 "$empty/plan.err")"
  out=$(cat "$empty/plan")
  expect_contains "$out" "This is the PLAN" "the plan without cloud tools"
  status=0
  PATH="$empty/bin:/usr/bin:/bin" TMPDIR="$empty/tmp" bash "$script" --help >"$empty/usage" 2>&1 || status=$?
  [ "$status" -eq 0 ] || fail "the usage without cloud tools: expected exit 0, got $status"
  expect_contains "$(cat "$empty/usage")" "usage: check590.sh" "the usage without cloud tools"
}

# The endpoint override may only name loopback, and only in one shape: the
# requests carry a token.
test_endpoint_override_shapes_are_refused() {
  local value
  for value in "https://api.bitbucket.org/2.0" "http://127.0.0.1:8080@evil.example/2.0" \
    "http://evil.example:8080/2.0" "http://127.0.0.1:80abc/2.0" "http://127.0.0.1:8080/2.0?x=1"; do
    local status=0
    mkdir -p "$work/endpoint/tmp"
    CHECK590_API_URL="$value" TMPDIR="$work/endpoint/tmp" PATH="$work/endpoint/bin:$PATH" \
      bash "$script" --repo dimi_bmb/sandbox-a --token-file /nonexistent/repo \
      --project-token-file /nonexistent/project --workspace-token-file /nonexistent/workspace \
      --project-uuid "$PROJECT_UUID" >"$work/endpoint/out" 2>"$work/endpoint/err" || status=$?
    [ "$status" -eq 2 ] || fail "the endpoint override '$value': expected exit 2, got $status"
    expect_contains "$(cat "$work/endpoint/out" "$work/endpoint/err")" "CHECK590_API_URL may only name a loopback address" "the endpoint override '$value'"
  done
}

# One token file per type, and each at mode 0600: a token any other user of the
# machine can read is not one to store in the installation's bundle.
test_token_file_complaints() {
  # The fixture files live OUTSIDE the run's own directory: run590 writes the
  # three files it is pointed at, so a fixture inside it would be rewritten to
  # mode 0600 with the sentinel in it and the case would test nothing.
  local fixtures=$work/fixtures
  mkdir -p "$fixtures"
  printf '%s\n' "$TOKEN_SENTINEL" >"$fixtures/not-private"
  chmod 644 "$fixtures/not-private"
  RUN590_TOKEN_FILE="$fixtures/not-private" run590 badmode
  expect_status "$status" 2 "a token file at mode 0644"
  expect_contains "$err" "is mode 644" "the refusal names the mode"
  expect_no_request "$work/badmode" "a token file at mode 0644"

  : >"$fixtures/empty"
  chmod 600 "$fixtures/empty"
  RUN590_TOKEN_FILE="$fixtures/empty" run590 empty
  expect_status "$status" 2 "an empty token file"
  expect_contains "$err" "could not be read" "the refusal names what was read"
  expect_contains "$err" "the token file is empty" "the refusal says the file is empty"
  expect_no_request "$work/empty" "an empty token file"

  printf 'FAKE-FIRST-LINE-ONLY\nFAKE-SECOND-LINE-ONLY\n' >"$fixtures/twoline"
  chmod 600 "$fixtures/twoline"
  RUN590_TOKEN_FILE="$fixtures/twoline" run590 twoline
  expect_status "$status" 2 "a token file holding two lines"
  expect_contains "$err" "one line of printable text" "the refusal says what a token file holds"
  expect_no_request "$work/twoline" "a token file holding two lines"
  expect_absent "$(cat "$work/twoline/out" "$work/twoline/err")" "FAKE-FIRST-LINE-ONLY" "a token file whose line was refused"
}

# The one file per type rule, and the two spellings of one project.
test_argument_complaints() {
  RUN590_PROJECT_ARGS=(--project-uuid "$PROJECT_UUID" --project-key SANDBOX)
  run590 both-flags
  expect_status "$status" 2 "both project flags"
  expect_contains "$err" "takes ONE of them" "the refusal says why"

  RUN590_PROJECT_ARGS=(--project-key SANDBOX)
  run590 key-only
  expect_status "$status" 0 "the project's key instead of its UUID"
  expect_contains "$(cat "$work/key-only/fugaro.log")" "--project-key SANDBOX" "the key reaches the store"

  RUN590_TOKEN_FILE=/nonexistent/token
  run590 missing-file
  expect_status "$status" 2 "a token file that is not there"
  expect_contains "$err" "could not be read" "a missing file is refused before anything is sent"
}

test_one_file_for_two_types_is_refused() {
  RUN590_PROJECT_TOKEN_FILE="$work/shared/token"
  run590 shared
  expect_status "$status" 2 "one file for two types"
  expect_contains "$err" "the same path" "the refusal says why"
  expect_no_request "$work/shared" "one file for two types"
}

# A tracing shell prints every command, and the commands here pass file names
# that hold a token.
test_xtrace_is_refused() {
  RUN590_SHELL='bash -x' run590 xtrace
  expect_status "$status" 2 "a tracing shell"
  expect_no_request "$work/xtrace" "a tracing shell"
  expect_contains "$out" "FACT xtrace: FAIL" "the block names the trace"
  expect_contains "$err" "is tracing" "the refusal says what to do"
}

# An endpoint that echoes back what it was given must not be able to reach the
# block through a field it chooses.
test_hostile_endpoint_prints_nothing_it_was_given() {
  RUN590_API_MODE=hostile RUN590_MODE=set-refused run590 hostile
  assert_no_secret "$work/hostile" "the hostile endpoint"
  expect_contains "$out" "FACT aborted: FAIL" "the run stopped on the refusal"
}

# The cleanup has to work with HOME unset, which is the one environment a
# `set -u` script gets wrong.
test_cleanup_survives_an_unset_home() {
  RUN590_UNSET_HOME=yes run590 no-home
  expect_status "$status" 0 "HOME unset"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory was removed with HOME unset"
  local scratch
  scratch=$(scratch_of "$work/no-home")
  [ -n "$scratch" ] && [ ! -e "$scratch" ] || fail "HOME unset: the scratch directory is still there"
}

# ------------------------------------- the assertions have to be able to fail
# Each of the assertions above is only worth something if it can fail: this case
# runs a script that prints a token it was given and one that leaves a file
# behind, and checks that the suite's own assertions catch both.
test_the_secret_assertions_catch_an_echoing_script() {
  local dir=$work/echoing
  mkdir -p "$dir"
  cat >"$dir/echo.sh" <<'ECHO_EOF'
#!/usr/bin/env bash
echo "the token is $TOKEN_SENTINEL"
printf '%s\n' "$TOKEN_SENTINEL" >"$0.leak"
ECHO_EOF
  chmod +x "$dir/echo.sh"
  # It is RUN first: an assertion only ever handed a clean run has not been read.
  # The value arrives in the environment, which is the one way a token can be in
  # a process without being in its argv.
  TOKEN_SENTINEL="$TOKEN_SENTINEL" "$dir/echo.sh" >"$dir/out" 2>"$dir/err" || true
  local before=$failures
  EXPECT_FAIL=yes assert_no_secret "$dir" "the echoing script"
  [ "$failures" -gt "$before" ] || fail "the secret assertions did not catch a script that prints the token and leaves it in a file"
  # The assertions are back to their real jobs now: the failures above were the
  # point of this case.
  failures=$before
  rm -f "$dir/echo.sh.leak"
}

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

test_index=0
t test_happy_path
t test_each_type_is_read_itself
t test_a_repository_token_with_an_identity
t test_no_scope_header_is_incomplete
t test_no_identity_for_a_project_token_is_incomplete
t test_narrow_scopes_are_a_failure
t test_verifier_document_is_checked
t test_write_verified_is_a_failure
t test_a_refused_add_path_stops_the_run
t test_a_token_that_does_not_reach_the_repository_stops_the_run
t test_git_refusal_is_a_failure
t test_belong_is_refused_everywhere
t test_the_installation_and_the_gcp_project_are_held
t test_doctor_exit_1_is_an_answer
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_cloud_tools
t test_endpoint_override_shapes_are_refused
t test_token_file_complaints
t test_argument_complaints
t test_one_file_for_two_types_is_refused
t test_xtrace_is_refused
t test_hostile_endpoint_prints_nothing_it_was_given
t test_cleanup_survives_an_unset_home
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check590_test: $failures failure(s)" >&2
  exit 1
fi
echo "check590_test: all tests passed" >&2
