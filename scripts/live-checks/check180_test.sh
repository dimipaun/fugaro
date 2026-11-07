#!/usr/bin/env bash
# Tests for scripts/live-checks/check180.sh, against fake tools on PATH and a
# fake Bitbucket Cloud on loopback: nothing here reaches the network, no real
# credential exists, no Fugaro installation is read, no run is launched, and the
# token the test feeds in is a sentinel asserted absent from everything the run
# prints or leaves behind.
#
# What the fakes are: `fugaro` (doctor, secrets set, update, run, runs ls),
# `gcloud` (the two secret reads), `git` (the scratch dir is not a work tree,
# and `ls-remote`), `go` (the live test) — each a few lines of bash that answer
# from FAKE180_MODE and write what they were asked to a log — and a python3
# HTTP server that answers the API the way Bitbucket Cloud does, including a
# hostile mode whose bodies echo back the bearer token they were sent.
#
# Usage: bash scripts/live-checks/check180_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check180.sh
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
pids=()
cleanup_all() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  if [ -n "${CHECK180_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
# rows_of_block TEXT — the block's own rows, one short line each, so a failure
# names what the run recorded instead of pasting the whole block into the log.
rows_of_block() {
  printf '%s\n' "$1" | grep '^FACT ' | grep -v 'not reached' | cut -c1-90 | tr '\n' '|' | head -c 900
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
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}

# The sentinels are deliberately not credential shapes: nothing here is a real
# token, and the assertions below hold that they never survive a run.
TOKEN_SENTINEL='FAKE-PROJECT-TOKEN-FOR-CHECK-180-TESTS-NOT-A-CREDENTIAL'
TOKEN2_SENTINEL='FAKE-ROTATED-TOKEN-FOR-CHECK-180-TESTS-NOT-A-CREDENTIAL'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation inputs xtrace U1 U2 store update run-a run-b author-a author-b
  U4-approve-a U4-merge-a U4-approve-b U4-merge-b U3 live-test rotation second-machine
  restore)

# --------------------------------------------------------------------- the fake
write_fakes() { # dir
  local dir=$1
  cat >"$dir/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check180.sh's test only: it answers the five calls the
# check makes and records what it was asked. It never reads a token: the store's
# value arrives on stdin and goes to /dev/null, so nothing it does can print one.
set -u
mode=${FAKE180_MODE:-ok}
log=${FAKE180_LOG:-/dev/null}
state=${FAKE180_STATE:-/tmp}
printf '%s\n' "$*" >>"$log"
case "${1:-}" in
doctor)
  [ "$mode" = doctor-fails ] && { echo "doctor: no usable credentials on this machine" >&2; exit 1; }
  name=sandbox
  [ "$mode" = doctor-wrong-name ] && name=otherproject
  printf '{"ok":true,"project":{"name":"%s","gcp_project":"sandbox-p","region":"us-east5"}}\n' "$name"
  exit 0
  ;;
secrets)
  cat >/dev/null
  if [ "$mode" = store-refused ]; then
    echo "bitbucket-token has no project_secrets.bitbucket-token.allow_data_to entry in this local config" >&2
    exit 1
  fi
  echo "stored bitbucket-token project-wide"
  exit 0
  ;;
update)
  case "$*" in
  *--plan-only*)
    # The plan is about the checkout it was given: a plan taken against
    # whatever directory the check happened to start in is the bug this log is
    # here to catch.
    printf 'plan-cwd=%s\n' "$PWD" >>"$log"
    if [ "$mode" = second-plan-hides-the-token ]; then
      echo "plan: nothing is missing for this repository"
    else
      echo "plan: Still missing for sandbox/one: fugaro secrets set bitbucket-token --repo sandbox/one"
    fi
    exit 0
    ;;
  *--config-only*)
    if [ "$mode" = second-seed-fails ]; then
      echo "the installation's outputs could not be read" >&2
      exit 1
    fi
    echo "wrote the local config from the installation's outputs"
    exit 0
    ;;
  esac
  [ "$mode" = update-fails ] && { echo "update: the apply failed" >&2; exit 1; }
  if [ "$mode" = update-asks ]; then
    echo "Still missing for sandbox/one: run \`fugaro secrets set bitbucket-token --repo sandbox/one\`"
    exit 0
  fi
  echo "update: nothing to do"
  exit 0
  ;;
run)
  [ "$mode" = run-fails ] && { echo "run: the execution ended before finalizing" >&2; exit 1; }
  echo '{"run_id":"run-0001","status":"succeeded","repo":"sandbox/one"}'
  exit 0
  ;;
runs)
  n=$(cat "$state/ls-calls" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s' "$n" >"$state/ls-calls"
  if [ "$mode" = no-push ]; then
    echo '{"project":"sandbox","runs":[{"run_id":"run-0001","status":"succeeded","pushed":false,"pr":0}]}'
    exit 0
  fi
  if [ "$mode" = rotation-no-push ] && [ "$n" -ge 3 ]; then
    echo '{"project":"sandbox","runs":[{"run_id":"run-0002","status":"succeeded","pushed":false,"pr":0}]}'
    exit 0
  fi
  echo '{"project":"sandbox","runs":[{"run_id":"run-0001","status":"succeeded","pushed":true,"pr":7,"base_branch":"fugaro/check180-run"}]}'
  exit 0
  ;;
esac
echo "fake fugaro: unknown subcommand ${1:-}" >&2
exit 2
FUGARO_EOF
  chmod +x "$dir/fugaro"

  cat >"$dir/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake `gcloud`, for check180.sh's test only: the two Secret Manager reads the
# rotation makes. The container list answers with the hash-suffixed name the real
# ProjectSecretID builds, and the version list answers one version the first time
# and two after the new value was stored.
set -u
state=${FAKE180_STATE:-/tmp}
mode=${FAKE180_MODE:-ok}
if [ "${1:-}" = "secrets" ] && [ "${2:-}" = "list" ]; then
  [ "$mode" = no-container ] && exit 0
  echo "fugaro-project-sandbox-bitbucket-token-9f3c1a"
  exit 0
fi
if [ "${1:-}" = "secrets" ] && [ "${2:-}" = "versions" ]; then
  n=$(cat "$state/version-calls" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s' "$n" >"$state/version-calls"
  if [ "$n" -eq 1 ]; then
    printf '1 ENABLED\n'
  else
    printf '1 ENABLED\n2 ENABLED\n'
  fi
  exit 0
fi
echo "fake gcloud: unknown call $*" >&2
exit 2
GCLOUD_EOF
  chmod +x "$dir/gcloud"

  cat >"$dir/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git`, for check180.sh's test only. `rev-parse --show-toplevel` fails,
# because the scratch directory is not a work tree (which is what the check asks
# it), and `ls-remote --heads` answers like a repository the token can read. The
# credential is in a config file the check writes, so nothing here sees it.
set -u
case "${1:-}" in
rev-parse)
  exit 128
  ;;
ls-remote)
  if [ "${FAKE180_GIT_MODE:-ok}" = refused ]; then
    echo "fatal: Authentication failed for 'https://bitbucket.org/sandbox/one.git/'" >&2
    exit 128
  fi
  printf '%s\trefs/heads/main\n' 2f4a1c9d2f4a1c9d2f4a1c9d2f4a1c9d2f4a1c9d
  printf '%s\trefs/heads/fugaro/check180\n' 9d1c4a2f9d1c4a2f9d1c4a2f9d1c4a2f9d1c4a2f
  exit 0
  ;;
esac
exit 0
GIT_EOF
  chmod +x "$dir/git"

  cat >"$dir/go" <<'GO_EOF'
#!/usr/bin/env bash
# A fake `go`, for check180.sh's test only. The live test's contract is the
# token in FUGARO_BITBUCKET_TOKEN, so this fake checks that the check put the
# REAL value there — and records only the verdict, never the value: a log with a
# token in it is exactly what this suite asserts cannot exist.
set -u
if [ "${FUGARO_BITBUCKET_TOKEN:-}" = "${FAKE180_TOKEN:-unset}" ] && [ -n "${FUGARO_BITBUCKET_TOKEN:-}" ]; then
  echo "token=matches" >>"${FAKE180_LOG:-/dev/null}"
else
  echo "token=MISSING" >>"${FAKE180_LOG:-/dev/null}"
fi
if [ "${FAKE180_GO_MODE:-ok}" = fail ]; then
  echo "--- FAIL: TestLiveBitbucket (0.31s)" >&2
  echo "    live_test.go:311: the sandbox repository refused the token" >&2
  exit 1
fi
echo "ok  	github.com/dimipaun/fugaro/internal/gitprov/bitbucket	0.31s"
exit 0
GO_EOF
  chmod +x "$dir/go"

  cat >"$dir/fake.py" <<'FAKE_EOF'
#!/usr/bin/env python3
"""A fake Bitbucket Cloud on loopback, for check180.sh's test only.

It never reaches the network and holds no credential of its own: the token it
echoes back is one it read out of the request it was just sent. Every body
carries that echo back, so a check that printed a response field as it arrived
would fail this suite rather than pass it.
"""
import json, os, sys, http.server

TOKEN = os.environ.get("FAKE180_TOKEN", "")
MODE = os.environ.get("FAKE180_API_MODE", "ok")
UUID = "{9f1d0c2a-0000-0000-0000-000000000001}"


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, status, doc):
        doc = dict(doc)
        doc["echo"] = TOKEN
        raw = json.dumps(doc).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def refuse(self, status, code):
        # The code itself is the token in "hostile" mode: an endpoint that
        # echoes what it was given must not be able to reach the block through a
        # field it chooses.
        self.reply(status, {"error": {"code": TOKEN if MODE == "hostile" else code, "message": TOKEN}})

    def bearer(self):
        head = self.headers.get("Authorization") or ""
        return head[7:] if head.startswith("Bearer ") else ""

    def do_GET(self):
        self.route("GET")

    def do_POST(self):
        self.route("POST")

    def route(self, method):
        path = self.path.split("?", 1)[0].rstrip("/")
        n = int(self.headers.get("Content-Length") or 0)
        if n:
            self.rfile.read(n)

        if MODE == "wrong-token" or self.bearer() != TOKEN:
            self.refuse(401, "invalid_token")
            return

        if path == "/2.0/user":
            if MODE == "no-identity":
                self.reply(200, {"uuid": UUID, "display_name": "", "nickname": ""})
            else:
                self.reply(200, {"uuid": UUID, "display_name": "Fugaro sandbox", "nickname": "fugaro-sandbox"})
            return

        if path.startswith("/2.0/repositories/"):
            rest = path[len("/2.0/repositories/"):]
            parts = [p for p in rest.split("/") if p]
            if len(parts) == 2:
                if parts[1] == "probe" and MODE == "probe-refused":
                    self.refuse(403, "forbidden")
                    return
                self.reply(200, {"full_name": "%s/%s" % (parts[0], parts[1]),
                                 "project": {"key": "SANDBOX"}, "is_private": True})
                return
            if len(parts) == 4 and parts[2] == "pullrequests":
                if method == "GET":
                    self.reply(200, {"id": int(parts[3]) if parts[3].isdigit() else 0, "state": "OPEN",
                                     "author": {"display_name": "Fugaro sandbox", "uuid": UUID}})
                    return
                self.reply(404, {"error": {"code": "not_found"}})
                return
            if len(parts) == 5 and parts[2] == "pullrequests":
                if parts[4] == "approve":
                    if MODE == "approve-refused":
                        self.refuse(403, "insufficient_scope")
                        return
                    self.reply(200, {})
                    return
                if parts[4] == "merge":
                    if MODE == "merge-refused":
                        self.refuse(403, "insufficient_scope")
                        return
                    self.reply(200, {"state": "MERGED"})
                    return
                self.reply(404, {"error": {"code": "not_found"}})
                return
            self.reply(404, {"error": {"code": "not_found"}})
            return

        self.reply(404, {"error": {"code": "not_found"}})


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print("fake180 listening on 127.0.0.1:%d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
FAKE_EOF
}

FAKE_PORT=""
start_fake() { # start_fake DIR — the fakes go in DIR/bin, which is put on PATH
  write_fakes "$1/bin"
  : >"$1/fake.log"
  FAKE180_API_MODE=${RUN180_API_MODE:-ok} FAKE180_TOKEN="$TOKEN_SENTINEL" \
    python3 "$1/bin/fake.py" >"$1/fake.out" 2>"$1/fake.err" &
  local pid=$!
  pids+=("$pid")
  local i=0
  while [ "$i" -lt 200 ]; do
    FAKE_PORT=$(sed -n 's/^fake180 listening on 127\.0\.0\.1:\([0-9]\{1,\}\)$/\1/p' "$1/fake.out" | head -n 1)
    [ -n "$FAKE_PORT" ] && return 0
    kill -0 "$pid" 2>/dev/null || { fail "the fake Bitbucket died: $(tail -n 3 "$1/fake.err" | tr '\n' ' ')"; return 1; }
    sleep 0.05
    i=$((i + 1))
  done
  fail "the fake Bitbucket did not report that it is listening"
  return 1
}

# ---------------------------------------------------------------- running it
write_token() { # write_token FILE [value]
  printf '%s\n' "${2:-$TOKEN_SENTINEL}" >"$1"
  chmod 600 "$1"
}

# run180 DIR — runs the check once against the fakes. RUN180_ARGS appends
# arguments, RUN180_MODE picks the fake fugaro's behaviour, RUN180_API_MODE the
# fake Bitbucket's, RUN180_NAME_A/RUN180_NAME_B/RUN180_PROBE replace the
# repository names the check is pointed at, and RUN180_ENV names environment
# variables (NAME=value) to set, so a test can add a bad override. Sets $status,
# $out and $err.
run180() {
  local dir=$work/$1
  mkdir -p "$dir/tmp" "$dir/checkout-a/.git" "$dir/checkout-b/.git" "$dir/bin" "$dir/state"
  mkdir -p "$dir/source/internal/gitprov/bitbucket"
  write_token "$dir/token"
  write_token "$dir/token2" "$TOKEN2_SENTINEL"
  start_fake "$dir" || return 1
  status=0
  out=""
  env PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    CHECK180_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK180_CURL_SECONDS=20 CHECK180_RUN_SECONDS=30 \
    FAKE180_MODE="${RUN180_MODE:-ok}" FAKE180_TOKEN="$TOKEN_SENTINEL" \
    FAKE180_STATE="$dir/state" FAKE180_LOG="$dir/fugaro.log" \
    ${RUN180_ENV[@]+"${RUN180_ENV[@]}"} \
    bash "$script" \
    --installation sandbox --token-file "$dir/token" \
    --repo "${RUN180_NAME_A:-sandbox/one}=$dir/checkout-a" \
    --repo "${RUN180_NAME_B:-sandbox/two}=$dir/checkout-b" \
    --probe-repo "${RUN180_PROBE:-sandbox/probe}" --source "$dir/source" \
    ${RUN180_ARGS[@]+"${RUN180_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  RUN180_ARGS=()
  RUN180_ENV=()
  RUN180_MODE=ok
  RUN180_API_MODE=ok
  RUN180_NAME_A=""
  RUN180_NAME_B=""
  RUN180_PROBE=""
}

# assert_no_secret DIR WHAT — nothing the run printed, and no file it left
# behind, carries the token. The two files this TEST writes with a sentinel in
# them on purpose are its own fixtures, and they are the only ones it may be in.
assert_no_secret() {
  local dir=$1 what=$2 hits
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$what: the token in the output"
  hits=$(grep -rl -- "$TOKEN_SENTINEL" "$dir" 2>/dev/null \
    | grep -v -F -x -e "$dir/token" -e "$dir/token2" || true)
  [ -z "$hits" ] || fail "$what: the token is in $hits"
  hits=$(grep -rl -- "$TOKEN2_SENTINEL" "$dir" 2>/dev/null \
    | grep -v -F -x -e "$dir/token" -e "$dir/token2" || true)
  [ -z "$hits" ] || fail "$what: the rotated token is in $hits"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 180 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

# --------------------------------------------------------------- the tests
test_happy_path() {
  run180 happy
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 180 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "sandbox-p" "happy path: the GCP project it read"
  expect_contains "$out" "FACT inputs: PASS — " "happy path: the two repositories and the project they share"
  expect_contains "$out" "SANDBOX" "happy path: the Bitbucket project key"
  expect_contains "$out" "FACT U1: PASS — " "happy path: the clone and the push"
  expect_contains "$out" "x-token-auth" "happy path: the username U1 is about"
  expect_contains "$out" "FACT U2: PASS — " "happy path: the token's own identity"
  expect_contains "$out" "FACT store: PASS — " "happy path: the store"
  expect_contains "$out" "FACT update: PASS — " "happy path: the update asks nothing"
  expect_contains "$out" "FACT run-a: PASS — " "happy path: the first run"
  expect_contains "$out" "FACT run-b: PASS — " "happy path: the second run"
  expect_contains "$out" "FACT author-a: PASS — " "happy path: the pull request's author"
  expect_contains "$out" "FACT author-b: PASS — " "happy path: the second pull request's author"
  expect_contains "$out" "FACT U4-approve-a: PASS — " "happy path: the approve"
  expect_contains "$out" "FACT U4-merge-a: INFO — " "happy path: the merge is reported, not guessed"
  expect_contains "$out" "FACT U3: PASS — " "happy path: the probe repository"
  expect_contains "$out" "FACT live-test: PASS — " "happy path: the live test"
  expect_contains "$out" "FACT rotation: INFO — " "happy path without --new-token-file: the rotation says what to pass"
  expect_contains "$out" "FACT second-machine: PASS — " "happy path: the second machine"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"

  # The live test's own contract: the check put the REAL token in that one
  # command's environment.
  expect_contains "$(cat "$work/happy/fugaro.log")" "token=matches" "happy path: the token reached the live test"

  assert_no_secret "$work/happy" "happy path"

  # Cleanup on success: the scratch directory and the files holding the
  # credential go with it.
  local scratch
  scratch=$(scratch_of "$work/happy")
  [ -n "$scratch" ] || fail "happy path: the run never announced a scratch directory"
  [ ! -e "$scratch" ] || fail "happy path: the scratch directory survived the run: $scratch"
  [ ! -e "$work/happy/tmp" ] || [ -z "$(ls -A "$work/happy/tmp" 2>/dev/null)" ] \
    || fail "happy path: something is still under the run's TMPDIR"
}

test_dry_run_touches_nothing() {
  RUN180_ARGS=(--dry-run)
  run180 dry
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "This is the PLAN" "dry run: the plan"
  expect_contains "$out" "--dry-run touches nothing" "dry run: what it promises"
  expect_contains "$out" "sandbox/one" "dry run: the repositories it would use"
  expect_absent "$out" "CHECK 180 RESULT" "dry run: no block for a plan"
  expect_absent "$out" "FACT " "dry run: no facts"
  [ ! -s "$work/dry/fugaro.log" ] || fail "dry run: fugaro was called: $(cat "$work/dry/fugaro.log")"
  [ ! -d "$work/dry/tmp" ] || [ -z "$(ls -A "$work/dry/tmp" 2>/dev/null)" ] \
    || fail "dry run: something was written under the run's TMPDIR"
}

test_belong_is_refused_everywhere() {
  local as_installation as_repo as_probe
  RUN180_ARGS=(--installation belong)
  run180 belong-install
  as_installation=$status
  expect_status "$as_installation" 2 "belong as the installation"
  expect_contains "$out" "names 'belong'" "belong as the installation: the refusal"
  expect_status "$as_installation" 2 "belong as the installation"
  [ ! -s "$work/belong-install/fake.log" ] || fail "belong as the installation: a request was sent"

  RUN180_NAME_A=belong/one
  run180 belong-repo
  as_repo=$status
  expect_status "$as_repo" 2 "belong as a repository's workspace"
  expect_contains "$out" "names 'belong'" "belong as a repository's workspace: the refusal"

  RUN180_NAME_B=belong/two
  run180 belong-repo-b
  expect_status "$status" 2 "belong as the second repository's workspace"
  expect_contains "$out" "names 'belong'" "belong as the second repository's workspace: the refusal"

  RUN180_PROBE=belong/probe
  run180 belong-probe
  as_probe=$status
  expect_status "$as_probe" 2 "belong as the probe repository"
  expect_contains "$out" "names 'belong'" "belong as the probe repository: the refusal"

  # And in any case, including the mixed one a typo produces.
  RUN180_ARGS=(--installation BELONG)
  run180 belong-upper
  expect_status "$status" 2 "BELONG in upper case"
  expect_contains "$out" "names 'belong'" "BELONG in upper case: the refusal"
}

test_endpoint_override_shapes_are_refused() {
  local value
  for value in \
    "https://api.bitbucket.org/2.0" \
    "http://127.0.0.1:1@evil.example/2.0" \
    "http://localhost:8080/2.0" \
    "http://127.0.0.1:not-a-port/2.0" \
    "http://127.0.0.1:8080/2.0?x=1"; do
    RUN180_ENV=("CHECK180_API_URL=$value")
    run180 "endpoint-${value//[^a-zA-Z0-9]/_}"
    expect_status "$status" 2 "endpoint override '$value'"
    expect_contains "$out" "CHECK180_API_URL may only name a loopback address" "endpoint override '$value': the refusal"
  done
}

test_token_file_complaints() {
  # A file that is not there.
  RUN180_ARGS=(--token-file "$work/absent/token")
  run180 token-absent
  expect_status "$status" 2 "a token file that is not there"
  expect_contains "$out" "could not be read" "a token file that is not there: the reason"

  # A file any other user of the machine can read.
  local dir=$work/token-mode
  mkdir -p "$dir"
  printf '%s\n' "$TOKEN_SENTINEL" >"$dir/token"
  chmod 644 "$dir/token"
  RUN180_ARGS=(--token-file "$dir/token")
  run180 token-mode-missing
  expect_status "$status" 2 "a token file at mode 644"
  expect_contains "$out" "wants 0600" "a token file at mode 644: the fix"
  expect_absent "$out" "$TOKEN_SENTINEL" "a token file at mode 644: the value"
  assert_no_secret "$work/token-mode-missing" "a token file at mode 644"
}

test_store_refused() {
  RUN180_MODE=store-refused
  run180 store-refused
  expect_status "$status" 2 "a store that is refused"
  expect_contains "$out" "FACT store: FAIL" "a store that is refused: the fact"
  expect_contains "$out" "allow_data_to" "a store that is refused: the reason the CLI gave"
  expect_absent "$out" "$TOKEN_SENTINEL" "a store that is refused: the value"
  # Nothing after the store ran: no update, no run.
  expect_absent "$(cat "$work/store-refused/fugaro.log")" "run --repo" "a store that is refused: no run was launched"
  assert_no_secret "$work/store-refused" "a store that is refused"
}

test_update_asking_for_a_token_is_a_fail() {
  RUN180_MODE=update-asks
  run180 update-asks
  expect_status "$status" 1 "an update that asks for a token"
  expect_contains "$out" "FACT update: FAIL" "an update that asks for a token: the fact"
  expect_contains "$out" "ASK NOTHING" "an update that asks for a token: what the check expects of it"
  # The runs still happened: the check reports the reading it could not make
  # rather than stopping, because the rest of the block is still a measurement.
  expect_contains "$out" "FACT run-a: PASS" "an update that asks for a token: the run still ran"
}

test_a_run_that_does_not_push_is_a_fail() {
  RUN180_MODE=no-push
  run180 no-push
  expect_status "$status" 1 "a run that does not push"
  expect_contains "$out" "FACT run-a: FAIL" "a run that does not push: the first"
  expect_contains "$out" "FACT run-b: FAIL" "a run that does not push: the second"
  expect_contains "$out" "FACT author-a: FAIL — not reached" "a run that does not push: no author is read for a pull request that does not exist"
}

test_probe_reach() {
  RUN180_API_MODE=probe-refused
  run180 probe-refused
  expect_status "$status" 1 "a probe repository the token cannot read"
  expect_contains "$out" "FACT U3: FAIL" "a probe repository the token cannot read: the fact"
  expect_contains "$out" "added to the Bitbucket project afterwards" "a probe repository the token cannot read: the claim it contradicts"
}

test_approve_and_merge() {
  RUN180_API_MODE=approve-refused
  run180 approve-refused
  expect_status "$status" 1 "an approve the scopes refuse"
  expect_contains "$out" "FACT U4-approve-a: FAIL" "an approve the scopes refuse: the fact"

  RUN180_API_MODE=merge-refused
  RUN180_ARGS=(--merge)
  run180 merge-refused
  expect_status "$status" 1 "a merge the scopes refuse"
  expect_contains "$out" "FACT U4-approve-a: PASS" "a merge the scopes refuse: the approve was admitted"
  expect_contains "$out" "FACT U4-merge-a: FAIL" "a merge the scopes refuse: the fact"

  RUN180_ARGS=(--merge)
  run180 merge-allowed
  expect_status "$status" 0 "a merge the scopes admit"
  expect_contains "$out" "FACT U4-merge-a: PASS" "a merge the scopes admit: the fact"
  expect_contains "$out" "cannot be undone" "a merge the scopes admit: what the merge is"
}

test_rotation() {
  RUN180_ARGS=(--new-token-file "$work/rotation/token2" --merge)
  run180 rotation
  expect_status "$status" 0 "the rotation"
  expect_contains "$out" "FACT rotation: PASS" "the rotation: the fact"
  expect_contains "$out" "the next run uses the new version" "the rotation: the reading it makes"
  assert_no_secret "$work/rotation" "the rotation"

  RUN180_MODE=rotation-no-push
  RUN180_ARGS=(--new-token-file "$work/rotation-no-push/token2")
  run180 rotation-no-push
  expect_status "$status" 1 "a rotation the next run does not use"
  expect_contains "$out" "FACT rotation: FAIL" "a rotation the next run does not use: the fact"

  # The container is found by filter, never composed: a listing that names
  # several (or none) stops rather than picking one.
  RUN180_MODE=no-container
  RUN180_ARGS=(--new-token-file "$work/rotation-nocontainer/token2")
  run180 rotation-nocontainer
  expect_status "$status" 2 "a container listing that names none"
  expect_contains "$out" "was not found once" "a container listing that names none: the reason"
}

test_second_machine() {
  run180 second-machine
  expect_status "$status" 0 "the second machine"
  expect_contains "$out" "FACT second-machine: PASS" "the second machine: the fact"
  # `update --repo` is a bool whose positional PATH defaults to the current
  # directory: the plan has to be about the allow-listed checkout, not about
  # whatever directory the check was launched from.
  expect_contains "$(cat "$work/second-machine/fugaro.log")" "plan-cwd=$work/second-machine/checkout-a" \
    "the second machine: the plan's own directory"

  RUN180_MODE=second-plan-hides-the-token
  run180 second-hidden
  expect_status "$status" 1 "a second machine whose plan hides the per-repository token"
  expect_contains "$out" "FACT second-machine: FAIL" "a second machine whose plan hides the per-repository token: the fact"
  expect_contains "$out" "D467" "a second machine whose plan hides the per-repository token: the decision it contradicts"
  expect_contains "$(cat "$work/second-hidden/fugaro.log")" "plan-cwd=$work/second-hidden/checkout-a" \
    "a second machine whose plan hides the per-repository token: the plan's own directory"

  RUN180_MODE=second-seed-fails
  run180 second-seed-fails
  expect_status "$status" 0 "a second machine that could not be seeded"
  expect_contains "$out" "FACT second-machine: INFO" "a second machine that could not be seeded: the fact"
  expect_contains "$out" "UNVERIFIED" "a second machine that could not be seeded: what is left open"
}

test_live_test() {
  RUN180_ENV=("FAKE180_GO_MODE=fail")
  run180 live-fail
  expect_status "$status" 1 "a live test that fails"
  expect_contains "$out" "FACT live-test: FAIL" "a live test that fails: the fact"
  expect_contains "$out" "TestLiveBitbucket" "a live test that fails: the harness it names"

  RUN180_ARGS=(--source "$work/live-nosource/absent")
  run180 live-nosource
  expect_status "$status" 0 "no source tree"
  expect_contains "$out" "FACT live-test: INFO" "no source tree: the fact"
  expect_contains "$out" "go test -tags live -run TestLiveBitbucket" "no source tree: the command it hands over"
}

test_hostile_endpoint_prints_nothing_it_was_given() {
  RUN180_API_MODE=hostile
  RUN180_ENV=("FAKE180_GIT_MODE=refused")
  run180 hostile
  expect_contains "$out" "FACT U1: FAIL" "hostile: the git refusal is reported"
  assert_no_secret "$work/hostile" "hostile"
  # The API echoed the token in every body and in every error code; none of it
  # reached the block, and the rows that read FAIL read a word rather than a
  # value.
  expect_absent "$out" '"echo"' "hostile: no response field reached the block"
}

test_installation_mismatch_and_doctor_failure() {
  RUN180_MODE=doctor-wrong-name
  run180 doctor-wrong-name
  expect_status "$status" 2 "a doctor that selects another installation"
  expect_contains "$out" "is the installation 'otherproject'" "a doctor that selects another installation: the reason"

  RUN180_MODE=doctor-fails
  run180 doctor-fails
  expect_status "$status" 2 "a doctor that fails"
  expect_contains "$out" "doctor" "a doctor that fails: the command it names"
  [ ! -s "$work/doctor-fails/fugaro.log" ] || [ "$(wc -l <"$work/doctor-fails/fugaro.log")" -le 1 ] \
    || fail "a doctor that fails: the check went on to other fugaro calls"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  mkdir -p "$dir/tmp" "$dir/bin" "$dir/checkout-a/.git" "$dir/checkout-b/.git" "$dir/source/internal/gitprov/bitbucket"
  write_token "$dir/token"
  write_fakes "$dir/bin"
  status=0
  out=""
  env PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    bash -x "$script" --installation sandbox --token-file "$dir/token" \
    --repo sandbox/one="$dir/checkout-a" --repo sandbox/two="$dir/checkout-b" \
    --probe-repo sandbox/probe --source "$dir/source" \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace"
  expect_contains "$out" "tracing" "xtrace: the refusal"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the fact"
}

test_the_secret_assertions_catch_an_echoing_script() {
  local dir=$work/detector caught
  mkdir -p "$dir"
  write_token "$dir/token"
  write_token "$dir/token2" "$TOKEN2_SENTINEL"
  cat >"$dir/echoing.sh" <<'ECHO_EOF'
#!/usr/bin/env bash
# A script that prints the credential it was handed: the failure the check's own
# test exists to catch. It writes it where a credential file would be written as
# well, so both halves of the assertion have something to catch.
for arg in "$@"; do case "$arg" in /*) cat "$arg" ;; esac; done
ECHO_EOF
  bash "$dir/echoing.sh" --installation sandbox --token-file "$dir/token" \
    >"$dir/out" 2>"$dir/err" || true
  # The assertions run in a subshell with a counter of its own, so the FAIL
  # lines this provokes on purpose are COUNTED instead of printed.
  caught=$(
    caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    assert_no_secret "$dir" "detector"
    printf '%s' "$caught_failures"
  )
  # Two, and not fewer: the leak in what the run PRINTED and the leak in a file
  # it LEFT BEHIND are two assertions, and an assert_no_secret that lost either
  # half would report one here. (The third, the rotated token, cannot be raised
  # by this script: it was never handed one.)
  [ "${caught:-0}" -eq 2 ] \
    || fail "detector: an echoing script raised $caught of the two secret assertions this can provoke (the token in the output, the token in a file left behind), so one of them is vacuous"
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
t test_dry_run_touches_nothing
t test_belong_is_refused_everywhere
t test_endpoint_override_shapes_are_refused
t test_token_file_complaints
t test_store_refused
t test_update_asking_for_a_token_is_a_fail
t test_a_run_that_does_not_push_is_a_fail
t test_probe_reach
t test_approve_and_merge
t test_rotation
t test_second_machine
t test_live_test
t test_hostile_endpoint_prints_nothing_it_was_given
t test_installation_mismatch_and_doctor_failure
t test_xtrace_is_refused
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check180_test: $failures failure(s)" >&2
  exit 1
fi
echo "check180_test: all tests passed" >&2
