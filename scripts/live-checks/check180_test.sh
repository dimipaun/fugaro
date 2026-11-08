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
# expect_no_request DIR WHAT — the fake Bitbucket answered nothing for that run.
# Every request it answers is appended to the run's fake.log, so this is the
# reading of "the token was never sent anywhere": a guard that refused only after
# a read would leave a line here, and one that is checked by a file nothing
# writes proves nothing at all.
expect_no_request() { # dir what
  [ ! -s "$1/fake.log" ] || fail "$2: a request reached the API: $(cat "$1/fake.log" | tr '\n' ' ')"
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}
# expect_last_line OUT WANT WHAT — the block's own last line, exactly. It is the
# line a reader takes as the answer, and the one the exit status has to agree
# with: a block whose last line is a verdict but whose status is another is two
# stories about one run, which is the whole point of the verdict being written
# from the status rather than beside it.
expect_last_line() { # out want what
  local last
  last=$(printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1)
  [ "$last" = "$2" ] || fail "$3: the block's last line reads '$last', want '$2'"
}

# The sentinels are deliberately not credential shapes: nothing here is a real
# token, and the assertions below hold that they never survive a run.
TOKEN_SENTINEL='FAKE-PROJECT-TOKEN-FOR-CHECK-180-TESTS-NOT-A-CREDENTIAL'
TOKEN2_SENTINEL='FAKE-ROTATED-TOKEN-FOR-CHECK-180-TESTS-NOT-A-CREDENTIAL'

# What the fake installation's own doctor reports (the fake `fugaro` below), and
# the project-wide container's id as the CLI writes it — the name carries a hash
# suffix, so the check has to find the container rather than compose it. The
# fakes read both out of the run's state directory, so the secrets and versions
# they answer for are that project's and that container's and no other's.
GCP_PROJECT_NAME='sandbox-p'
SECRET_ID='fugaro-project-sandbox-bitbucket-token-9f3c1a'

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
  # A store is a NEW VERSION of the container, not a call: the fake `gcloud`
  # below answers `versions list <container>` from this file, so the check's own
  # "N version(s) before, M after" is a reading of what the store did. Without
  # it the count would be of how often the listing was called, and a check that
  # listed the wrong container — or stored into one — would read the same.
  id=$(cat "$state/secret-id" 2>/dev/null || echo fugaro-project-sandbox-bitbucket-token-9f3c1a)
  n=$(cat "$state/versions-$id" 2>/dev/null || echo 0)
  printf '%s' "$((n + 1))" >"$state/versions-$id"
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
    # Which credentials the seeded machine was handed, and from where. The
    # check replaces HOME with a scratch home, so an ADC the check did not name
    # would be looked for inside that empty home and found nowhere; the fake
    # records the two variables the CLI's own order reads
    # (internal/cli/adcpath.go), so the test can pin that the owner's own
    # directory came through BY PATH and that the scratch home's is never used.
    printf 'config-only-cloudsdk=%s config-only-adc=%s\n' \
      "${CLOUDSDK_CONFIG:-}" "${GOOGLE_APPLICATION_CREDENTIALS:-}" >>"$log"
    if [ "$mode" = second-seed-fails ]; then
      echo "the installation's outputs could not be read" >&2
      exit 1
    fi
    echo "wrote the local config from the installation's outputs"
    exit 0
    ;;
  esac
  [ "$mode" = update-fails ] && { echo "update: the apply failed" >&2; exit 1; }
  if [ "$mode" = update-leaks ]; then
    # A command that says a credential in its own last line, which the check
    # reads into the block with last_line. Short on purpose: the reading row cuts
    # the line at 140 characters, and a longer one would be cut before its
    # credential, leaving the test's assertion about it true for free.
    echo "Authorization: Bearer FAKE-NOT-A-REAL-BEARER-4d17 refresh_token: FAKE-NOT-A-REAL-REFRESH-TOKEN-7c05" >&2
    exit 1
  fi
  if [ "$mode" = update-asks ]; then
    echo "Still missing for sandbox/one: run \`fugaro secrets set bitbucket-token --repo sandbox/one\`"
    exit 0
  fi
  echo "update: nothing to do"
  exit 0
  ;;
run)
  # The launch result: `fugaro run` RETURNS AT LAUNCH, and what it names is the
  # run the check then waits for. Every launch mints its own run_id and resets
  # the poll counter the listing below reads.
  [ "$mode" = run-fails ] && { echo "run: the execution ended before finalizing" >&2; exit 1; }
  n=$(cat "$state/launches" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s' "$n" >"$state/launches"
  printf '0' >"$state/polls"
  printf '{"run_id":"run-%04d","status":"launched","repo":"sandbox/one"}\n' "$n"
  exit 0
  ;;
runs)
  # The listing the check polls. Two things about it are the regressions this
  # fake exists to catch: the FIRST poll after a launch answers an in-flight row
  # for the run just launched (status running, terminal=false, nothing pushed),
  # which is what `fugaro runs ls` really says while a run is going — and the
  # row for the launched run is never first in the list, an older unrelated run
  # is. So a check that read the listing once would read an in-flight row, and
  # one that read rows[0] or asked for --limit 1 would read another run's row or
  # no row at all, instead of picking this run's row out by run_id.
  launches=$(cat "$state/launches" 2>/dev/null || echo 0)
  n=$(cat "$state/polls" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s' "$n" >"$state/polls"
  ours=$(printf 'run-%04d' "${launches:-0}")
  status=running
  terminal=false
  pushed=false
  pr=0
  if [ "$n" -gt 1 ]; then
    status=succeeded
    terminal=true
    pushed=true
    pr=7
    case "$mode" in
    no-push) pushed=false; pr=0 ;;
    rotation-no-push) if [ "${launches:-0}" -ge 3 ]; then pushed=false; pr=0; fi ;;
    esac
  fi
  printf '{"project":"sandbox","runs":[{"run_id":"run-9000","status":"running","terminal":false,"settled":false,"pushed":false,"pr":0},{"run_id":"%s","status":"%s","terminal":%s,"settled":%s,"pushed":%s,"pr":%d,"base_branch":"fugaro/check180-run"}]}\n' \
    "$ours" "$status" "$terminal" "$terminal" "$pushed" "$pr"
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
# rotation makes. It answers the way gcloud does, which is the point of it: a
# secret's `name` is the FULL resource path projects/<project>/secrets/<id>,
# --filter is applied client-side to the resource as a whole (so a
# `labels.X=Y` term matches the labels and a `name~` term is a regex against the
# path, not against the id), and --format decides what is printed. A fake that
# answered a bare id to `value(name)` would hide a check whose filter can never
# match a name — which is exactly the bug this fake was made faithful for.
set -u
state=${FAKE180_STATE:-/tmp}
mode=${FAKE180_MODE:-ok}
id=$(cat "$state/secret-id" 2>/dev/null || echo fugaro-project-sandbox-bitbucket-token-9f3c1a)
# The GCP project the installation's doctor named: every read has to be of THAT
# project, and a read of any other one is refused the way gcloud would answer
# nothing for it (the check then stops rather than measuring another project's
# secret).
project_want=$(cat "$state/gcp-project" 2>/dev/null || echo "")
full=projects/${project_want:-sandbox-p}/secrets/$id
labels="fugaro=managed fugaro_project=sandbox fugaro_secret=bitbucket-token"

filter=""
format=""
project=""
read_args() { # read_args [args...] — the options the check passes
  while [ $# -gt 0 ]; do
    case "$1" in
    --filter=*) filter=${1#--filter=} ;;
    --format=*) format=${1#--format=} ;;
    --project=*) project=${1#--project=} ;;
    --project) project=${2:-} ;;
    --filter | --format | --project) shift ;;
    esac
    shift
  done
}

check_project() {
  [ -z "$project_want" ] && return 0
  [ "${project:-}" = "$project_want" ] && return 0
  echo "fake gcloud: this read names --project '${project:-none}', and the installation is in '$project_want'" >&2
  exit 1
}

if [ "${1:-}" = "secrets" ] && [ "${2:-}" = "list" ]; then
  shift 2
  read_args "$@"
  check_project
  [ "$mode" = no-container ] && exit 0
  # Every whitespace-separated term of a gcloud filter has to hold (that is
  # AND), against the resource as a whole. A term this fake does not know
  # matches nothing, so a filter the real gcloud would not match either.
  match=yes
  for term in $filter; do
    case "$term" in
    labels.*=*)
      key=${term%%=*}
      value=${term#*=}
      key=${key#labels.}
      case " $labels " in *" $key=$value "*) ;; *) match=no ;; esac
      ;;
    "name~"*)
      printf '%s\n' "$full" | grep -E -- "${term#name~}" >/dev/null || match=no
      ;;
    "name="*)
      [ "$full" = "${term#name=}" ] || match=no
      ;;
    *) match=no ;;
    esac
  done
  [ "$match" = yes ] || exit 0
  case "$format" in
  *name.basename*) printf '%s\n' "$id" ;;
  *) printf '%s\n' "$full" ;;
  esac
  exit 0
fi
if [ "${1:-}" = "secrets" ] && [ "${2:-}" = "versions" ]; then
  shift 2
  # The container the versions are listed FROM — `gcloud secrets versions list
  # <container>` — and a secret's own versions are what this answers: a listing
  # of another container (or of the full path where the check means the id) has
  # no versions, which is what the check's "1 version(s) before" reads as `0`
  # instead of as the store's own result.
  shift # the `list` verb of `secrets versions list`
  container=${1:-}
  shift || true
  read_args "$@"
  check_project
  n=$(cat "$state/versions-$container" 2>/dev/null || echo 0)
  one_version() { # one_version NUMBER STATE
    case "$format" in
    *name.basename*) printf '%s %s\n' "$1" "$2" ;;
    *) printf '%s/versions/%s %s\n' "$full" "$1" "$2" ;;
    esac
  }
  i=1
  while [ "$i" -le "$n" ]; do
    one_version "$i" ENABLED
    i=$((i + 1))
  done
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

  # The real curl, wrapped. The check reads curl's own last words into the block
  # (`curl_note`), and the block is pasted into a PR — so the filter over them is
  # a guard, and the real curl never prints a credential, which is why a test
  # cannot reach that guard through curl alone. With FAKE180_CURL_MODE=leaks this
  # wrapper makes curl say one credential-shaped line (to stderr, which the check
  # redirects into its curl log); with the mode unset it only runs the real curl,
  # so every other test in this suite is unaffected. The line stays under the 200
  # characters curl_note keeps: a longer one would have its tail cut off, and the
  # assertion that the tail's credential never reaches the block would then hold
  # for free.
  local real_curl
  real_curl=$(command -v curl)
  cat >"$dir/curl" <<CURL_EOF
#!/usr/bin/env bash
# A fake \`curl\`, for check180.sh's test only: the real one, plus a line the
# test asked for. Never a real credential — the shapes are spelled as what they
# are, and the suite asserts they do not survive.
set -u
"$real_curl" "\$@"
rc=\$?
if [ "\${FAKE180_CURL_MODE:-}" = leaks ]; then
  printf '%s\n' 'curl: (22) The requested URL returned error: 401 with Authorization: Basic RkFLRS1OT1QtQS1SRUFMLUJFQVJFUg== and client_secret=FAKE-NOT-A-REAL-CLIENT-SECRET-4b21' >&2
fi
exit \$rc
CURL_EOF
  chmod +x "$dir/curl"

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
# The rotated value: the check never sends it (the new version is written to the
# secret and the API is not asked with it), so a request that carries it is a
# request the check built from the wrong file — and it is accepted here rather
# than refused, so such a check reads as a check that measured nothing.
TOKEN2 = os.environ.get("FAKE180_TOKEN2", "")
# Every request this fake answers, appended as `<method> <path>`. The test's
# "no request was sent" cases (the `belong` guards, the dry run) read this file:
# a guard that refused only after reading something would leave a line here.
APILOG = os.environ.get("FAKE180_API_LOG", "")
MODE = os.environ.get("FAKE180_API_MODE", "ok")
UUID = "{9f1d0c2a-0000-0000-0000-000000000001}"
# Where the rotation's revocation is read: the read of the FIRST of the two
# --repo pairs, the request step_inputs requires to answer 200 with the token
# before anything else is a reading. The check re-reads exactly this path to see
# the OLD value refused, so its reads — and no other repository's — are counted
# here. The harness derives the path from the --repo it hands the check.
REVOKE_PATH = "/2.0/repositories/" + os.environ.get("FAKE180_REVOKE_REPO", "sandbox/one")
READS = {}


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
        if APILOG:
            with open(APILOG, "a", encoding="utf-8") as fh:
                fh.write("%s %s\n" % (method, path))

        bearer = self.bearer()
        # The rotated value is valid at Bitbucket and the check never sends it:
        # the new version goes into the secret, and the polling below asks with
        # the OLD config. So a request carrying it is a check asking with the
        # wrong value — one that would loop against the real API until its cap —
        # and it is ANSWERED here rather than refused, so the wait runs out the
        # way the real one would instead of the test reading a pass for it.
        if TOKEN2 and bearer == TOKEN2:
            self.reply(200, {"uuid": UUID, "display_name": "Fugaro sandbox", "nickname": "fugaro-sandbox"})
            return

        if MODE == "wrong-token" or bearer != TOKEN:
            self.refuse(401, "invalid_token")
            return

        if path == "/2.0/user":
            # U2's identity read, and U7's open question: whether Bitbucket
            # answers this endpoint at all for a project access token. Mode
            # `user-forbidden` is the answer "no" — the token is scoped to
            # repository:write and pullrequest:write and is refused here from the
            # start — and the rotation must not mistake that standing refusal for
            # a revocation, which is why it polls the repository read instead.
            # In this mode the old value stays live everywhere else, so a check
            # that read a /user refusal as the revocation would launch the
            # post-rotation run and call the rotation PASS; this fake is the case
            # that tells the two readings apart.
            if MODE == "user-forbidden":
                self.refuse(403, "forbidden")
                return
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
                # The rotation's revocation, on the request the check waits on.
                # The register's step 8 has the owner revoke the OLD token before
                # the run after the rotation, and this fake stands in for that
                # state: the first read of this path is step_inputs', with a live
                # token, and a later one is the rotation's poll, REFUSED as a
                # revoked value is. In modes `old-token-live` (the owner has not
                # revoked) and `user-forbidden` (which is about /2.0/user, not
                # about this read) every read is accepted: the check waits out its
                # cap, the rotation's row reads UNVERIFIED and no run is launched
                # after it.
                if path == REVOKE_PATH and MODE not in ("old-token-live", "user-forbidden"):
                    READS["n"] = READS.get("n", 0) + 1
                    if READS["n"] > 1:
                        self.refuse(401, "invalid_token")
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
    FAKE180_TOKEN2="$TOKEN2_SENTINEL" FAKE180_API_LOG="$1/fake.log" \
    FAKE180_REVOKE_REPO="${RUN180_NAME_A:-sandbox/one}" \
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
  mkdir -p "$dir/tmp" "$dir/checkout-a/.git" "$dir/checkout-b/.git" "$dir/bin" "$dir/state" "$dir/gcloudconfig"
  mkdir -p "$dir/source/internal/gitprov/bitbucket"
  # What the fakes are agreeing about: the id the project-wide container has
  # (the CLI's own, hash suffix and all) and the GCP project the installation's
  # doctor names. The fake `gcloud` reads the secrets and their versions of that
  # project and no other, and the fake `fugaro`'s store adds a version to that
  # container — so a check that listed or stored under any other name reads a
  # listing with no versions in it rather than a convenient one.
  printf '%s\n' "$SECRET_ID" >"$dir/state/secret-id"
  printf '%s\n' "$GCP_PROJECT_NAME" >"$dir/state/gcp-project"
  write_token "$dir/token"
  write_token "$dir/token2" "$TOKEN2_SENTINEL"
  start_fake "$dir" || return 1
  status=0
  out=""
  # RUN180_UNSET_HOME=yes runs the check with HOME gone, the one environment in
  # which its cleanup has to still work (see test_cleanup_survives_an_unset_home).
  env ${RUN180_UNSET_HOME:+-u HOME} PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    CHECK180_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK180_CURL_SECONDS=20 CHECK180_RUN_SECONDS=30 CHECK180_RUN_POLL_SECONDS=1 \
    CHECK180_ROTATION_SECONDS=2 CHECK180_ROTATION_POLL_SECONDS=1 \
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
  RUN180_UNSET_HOME=""
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

# expect_store_argv DIR WHAT COUNT — every `secrets set bitbucket-token` call
# the run made, pinned to the value write the register documents. The fake
# `fugaro` logs its own argv, so this reads what the check actually ran rather
# than a string the check printed: `--yes` is the POLICY write's flag and the
# CLI refuses it on a value write (D1171), so a store carrying it stops the
# check at that step — and the rotation could then only ever read FAIL.
expect_store_argv() {
  local dir=$1 what=$2 want=$3 lines bad
  lines=$(grep -c '^secrets set bitbucket-token' "$dir/fugaro.log" 2>/dev/null || true)
  if [ "${lines:-0}" -lt 1 ]; then
    fail "$what: no 'secrets set bitbucket-token' call was made at all"
    return 0
  fi
  bad=$(grep '^secrets set bitbucket-token' "$dir/fugaro.log" \
    | grep -v -x -F 'secrets set bitbucket-token --project-wide --project sandbox' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "$what: a store call reads '$bad', want exactly 'secrets set bitbucket-token --project-wide --project sandbox': a value write refuses --yes and --dry-run (D1171), and the value goes in on stdin"
  [ "${lines:-0}" -eq "$want" ] || fail "$what: the run made $lines store call(s), want $want"
}

# expect_seed_argv DIR WHAT CHECKOUT — the second machine's TWO commands,
# argument for argument, from the fake `fugaro`'s own log.
#
# The seed writes the scratch home's local config, and the CLI CONFIRMS that
# write (internal/cli/init.go: writeLocalConfig -> confirmOrdinary): a terminal,
# or --yes. This step's stdin is /dev/null and it has no terminal, so a seed
# without --yes is refused on every run — the second-machine row could then never
# be anything but UNVERIFIED, criteria_unmet would force exit 3, and the all-PASS
# this check exists for (and U5's reading with it) would be unreachable. The fake
# cannot model that refusal, because it is the CLI that makes it; the argv is the
# only place the test can hold it, which is why it is pinned here. The second
# command is the opposite case: `--plan-only` creates nothing, so it never takes
# --yes, and the plan is about the allow-listed checkout rather than the current
# directory.
expect_seed_argv() { # dir what checkout
  local dir=$1 what=$2 checkout=$3 seed plan
  seed=$(grep '^update .*--config-only' "$dir/fugaro.log" 2>/dev/null || true)
  [ "$seed" = "update --gcp-project sandbox-p --region us-east5 --name sandbox --config-only --yes" ] \
    || fail "$what: the seed ran as '${seed:-nothing at all}', want exactly 'update --gcp-project sandbox-p --region us-east5 --name sandbox --config-only --yes': the seed WRITES the scratch home's local config, the CLI confirms that write, and this step has no terminal — without --yes the CLI refuses it (init.go's writeLocalConfig -> confirmOrdinary) and the second machine is never seeded"
  plan=$(grep '^update --repo .*--plan-only' "$dir/fugaro.log" 2>/dev/null || true)
  [ "$plan" = "update --repo $checkout --plan-only" ] \
    || fail "$what: the plan ran as '${plan:-nothing at all}', want exactly 'update --repo $checkout --plan-only': --plan-only creates nothing so it never takes --yes, and the plan has to be about the allow-listed checkout rather than the directory the check was launched from"
}

# expect_runs_polled DIR WHAT LAUNCHES — how the check read each run's row.
# `fugaro run` returns at launch (the fake's own answer says status
# "launched"), so the listing has to be polled until that run reached a final
# status: one read per run would be an in-flight row (terminal=false, nothing
# pushed), which is what the fake answers first on purpose. And the read has to
# be the window's own listing with the row picked out by run_id: --limit would
# leave the row out of the listing altogether, and the fake keeps an unrelated,
# newer run first in it so a positional read is another run's row.
expect_runs_polled() {
  local dir=$1 what=$2 launches=$3 calls bad
  calls=$(grep -c '^runs ls' "$dir/fugaro.log" 2>/dev/null || true)
  if [ "${calls:-0}" -lt $((launches * 2)) ]; then
    fail "$what: the check made ${calls:-0} 'runs ls' call(s) for ${launches} launch(es), fewer than the two per run a check that waits for the run to finish has to make: fugaro run returns at launch, and the first poll answers an in-flight row"
  fi
  bad=$(grep '^runs ls' "$dir/fugaro.log" | grep -e '-limit' -e '-after' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "$what: a 'runs ls' call reads '$bad': the row of the run being waited for has to be in the listing, so the check reads the window's own and picks the row out by run_id"
}

# --------------------------------------------------------------- the tests
# The all-PASS run: EVERY criterion is measured, which is what makes the block's
# last line PASS and the exit status 0. --merge measures U4's merge,
# --new-token-file measures the rotation, and --gcloud-config hands the second
# machine the owner's own ADC by path, so the seeded `update --config-only` has
# something to read the installation with. One criterion fewer and the run is
# INCOMPLETE (test_unmeasured_criteria_are_not_a_pass below).
test_happy_path() {
  RUN180_ARGS=(--merge --new-token-file "$work/happy/token2" --gcloud-config "$work/happy/gcloudconfig")
  run180 happy
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_last_line "$out" "CHECK 180 RESULT: PASS (exit 0)" "happy path: the block's last line is the verdict"
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
  expect_contains "$out" "FACT U4-merge-a: PASS — " "happy path with --merge: the merge is measured"
  expect_contains "$out" "FACT U3: PASS — " "happy path: the probe repository"
  expect_contains "$out" "FACT live-test: PASS — " "happy path: the live test"
  expect_contains "$out" "FACT rotation: PASS — " "happy path with --new-token-file: the rotation is measured"
  expect_contains "$out" "FACT second-machine: PASS — " "happy path: the second machine"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"

  # The live test's own contract: the check put the REAL token in that one
  # command's environment.
  expect_contains "$(cat "$work/happy/fugaro.log")" "token=matches" "happy path: the token reached the live test"
  # Two stores (the original and the rotation) and three launches (two runs and
  # the rotation's): every criterion was measured, so every step ran.
  expect_store_argv "$work/happy" "happy path: the store" 2
  expect_runs_polled "$work/happy" "happy path" 3
  # The run only reaches an all-PASS if the second machine really was seeded: the
  # seed's own argv is what says so, --yes included.
  expect_seed_argv "$work/happy" "happy path: the second machine's commands" "$work/happy/checkout-a"

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

# A step that could NOT verify its criterion must not read as a pass. Without
# --merge and --new-token-file there is nothing that can measure U4's merge or
# the rotation, and the same run also has to seed the second machine with an ADC
# it can actually read the installation with. An UNVERIFIED row inside a block
# whose last line said PASS, and whose status was 0, is the check reporting an
# answer it never got — so the block's last line says INCOMPLETE and the status
# is 3, and both are read here.
test_unmeasured_criteria_are_not_a_pass() {
  RUN180_MODE=second-seed-fails
  run180 unmeasured
  expect_status "$status" 3 "a run that finished with criteria left unmeasured"
  expect_block_keys "$out" "a run that finished with criteria left unmeasured"
  expect_contains "$out" "FACT U4-merge-a: UNVERIFIED — " "without --merge: the merge is not claimed as measured"
  expect_contains "$out" "FACT rotation: UNVERIFIED — " "without --new-token-file: the rotation says what to pass"
  expect_contains "$out" "FACT second-machine: UNVERIFIED — " "a seeded command that cannot read the installation: the row says what it could not measure"
  # The rows that WERE measured still read as they were measured: a criterion
  # left open does not turn the ones that passed into failures.
  expect_contains "$out" "FACT run-a: PASS — " "a run that finished with criteria left unmeasured: the runs still passed"
  expect_contains "$out" "FACT U1: PASS — " "a run that finished with criteria left unmeasured: U1 still passed"
  # An unsettled criterion is not a FAIL: nothing was measured to fail, so no
  # row reads FAIL and the status is 3 rather than 1.
  local failed
  failed=$(printf '%s\n' "$out" | grep '^FACT .*: FAIL' || true)
  [ -z "$failed" ] || fail "a run that finished with criteria left unmeasured: these rows read FAIL, and no criterion was measured and failed: $failed"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "a run that finished with criteria left unmeasured: the block's last line"
  assert_no_secret "$work/unmeasured" "a run that finished with criteria left unmeasured"
}

test_dry_run_touches_nothing() {
  RUN180_ARGS=(--dry-run --gcloud-config "$work/dry/gcloudconfig")
  run180 dry
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "This is the PLAN" "dry run: the plan"
  expect_contains "$out" "--dry-run touches nothing" "dry run: what it promises"
  expect_contains "$out" "sandbox/one" "dry run: the repositories it would use"
  # The plan says where the second machine's credentials come from, and the flag
  # is the one that names it: a plan that hid the ADC would leave the owner to
  # find out at the last step that the seeded machine had nothing to read the
  # installation with.
  expect_contains "$out" "gcloud config  --gcloud-config $work/dry/gcloudconfig" \
    "dry run: the second machine's credentials, by path"
  expect_absent "$out" "CHECK 180 RESULT" "dry run: no block for a plan"
  expect_absent "$out" "FACT " "dry run: no facts"
  [ ! -s "$work/dry/fugaro.log" ] || fail "dry run: fugaro was called: $(cat "$work/dry/fugaro.log")"
  expect_no_request "$work/dry" "dry run"
  [ ! -d "$work/dry/tmp" ] || [ -z "$(ls -A "$work/dry/tmp" 2>/dev/null)" ] \
    || fail "dry run: something was written under the run's TMPDIR"
}

# The plan and the usage are the two answers an owner can want before anything is
# installed, and neither reaches for a cloud tool: the tool lookup is after both
# endings. This runs the check with the shell's own tools on PATH and neither
# `fugaro` nor `gcloud` anywhere: with the lookup first, `--dry-run` on a machine
# that has nothing installed is refused with "Check 180 needs ... on PATH and it
# is not there" instead of the plan it promised to print, and the promise at the
# top of the script ("--dry-run prints the plan and touches nothing") would be
# the one thing the script could not do.
test_the_plan_and_the_usage_need_no_cloud_tools() {
  local tools=/usr/bin:/bin
  mkdir -p "$work/plan-no-tools/home"
  status=0
  out=$(env PATH="$tools" HOME="$work/plan-no-tools/home" GOOGLE_APPLICATION_CREDENTIALS= CLOUDSDK_CONFIG= \
    bash "$script" --dry-run \
    --installation sandbox --token-file "$work/plan-no-tools/token" \
    --repo "sandbox/one=$work/plan-no-tools/one" --repo "sandbox/two=$work/plan-no-tools/two" \
    --probe-repo sandbox/probe 2>"$work/plan-no-tools/err") || status=$?
  expect_status "$status" 0 "the plan without the cloud tools"
  expect_contains "$out" "This is the PLAN" "the plan without the cloud tools: the plan"
  expect_absent "$(cat "$work/plan-no-tools/err")" "on PATH and it is not there" \
    "the plan without the cloud tools: a refusal for a tool the plan never uses"

  status=0
  out=$(env PATH="$tools" HOME="$work/plan-no-tools/home" bash "$script" --help 2>"$work/plan-no-tools/err-help") || status=$?
  expect_status "$status" 0 "the usage without the cloud tools"
  expect_contains "$out" "usage: check180.sh --installation" "the usage without the cloud tools: the usage line"
  expect_absent "$(cat "$work/plan-no-tools/err-help")" "on PATH and it is not there" \
    "the usage without the cloud tools: a refusal for a tool the usage never uses"
}

test_belong_is_refused_everywhere() {
  local as_installation as_repo as_probe
  RUN180_ARGS=(--installation belong)
  run180 belong-install
  as_installation=$status
  expect_status "$as_installation" 2 "belong as the installation"
  expect_contains "$out" "names 'belong'" "belong as the installation: the refusal"
  # The refusal is BEFORE anything is sent: the token may never reach the API on
  # a run pointed at belong, and the fake's request log is what says so.
  expect_no_request "$work/belong-install" "belong as the installation"

  RUN180_NAME_A=belong/one
  run180 belong-repo
  as_repo=$status
  expect_status "$as_repo" 2 "belong as a repository's workspace"
  expect_contains "$out" "names 'belong'" "belong as a repository's workspace: the refusal"
  expect_no_request "$work/belong-repo" "belong as a repository's workspace"

  RUN180_NAME_B=belong/two
  run180 belong-repo-b
  expect_status "$status" 2 "belong as the second repository's workspace"
  expect_contains "$out" "names 'belong'" "belong as the second repository's workspace: the refusal"
  expect_no_request "$work/belong-repo-b" "belong as the second repository's workspace"

  RUN180_PROBE=belong/probe
  run180 belong-probe
  as_probe=$status
  expect_status "$as_probe" 2 "belong as the probe repository"
  expect_contains "$out" "names 'belong'" "belong as the probe repository: the refusal"
  expect_no_request "$work/belong-probe" "belong as the probe repository"

  # And in any case, including the mixed one a typo produces.
  RUN180_ARGS=(--installation BELONG)
  run180 belong-upper
  expect_status "$status" 2 "BELONG in upper case"
  expect_contains "$out" "names 'belong'" "BELONG in upper case: the refusal"
  expect_no_request "$work/belong-upper" "BELONG in upper case"
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

# HOME unset is the one environment where the cleanup still has to work: the
# removal guard compares the scratch directory against $HOME, and reading that
# unguarded aborts the shell under `set -u` — from inside cleanup, which the EXIT
# trap has already disarmed, so the scratch directory (where this run's files
# are) survives and no block is printed at all. The run below stops with a
# scratch directory made and a token file that is not there, so what is read is
# exactly what a leftover would hide: the block printed, and the scratch gone.
test_cleanup_survives_an_unset_home() {
  local scratch
  RUN180_UNSET_HOME=yes
  RUN180_ARGS=(--token-file "$work/no-home/token-absent")
  run180 no-home
  expect_status "$status" 2 "a run with HOME unset"
  expect_contains "$out" "could not be read" "a run with HOME unset: the reason it stopped"
  expect_contains "$out" "CHECK 180 RESULT:" "a run with HOME unset: the block it printed"
  scratch=$(scratch_of "$work/no-home")
  [ -n "$scratch" ] || fail "a run with HOME unset: the run never announced a scratch directory"
  [ ! -e "$scratch" ] || fail "a run with HOME unset: the scratch directory survived the run: $scratch"
}

test_store_refused() {
  RUN180_MODE=store-refused
  run180 store-refused
  expect_status "$status" 2 "a store that is refused"
  # The store is where the run stops, so the block says so in the row that names
  # what stopped it — and the store's own row is the not-reached one, because the
  # step wrote no fact. Asserting only "FACT store: FAIL" would be satisfied by
  # that placeholder, which reads the same whatever the run did.
  expect_contains "$out" "FACT aborted: FAIL — fugaro secrets set bitbucket-token --project-wide was REFUSED" \
    "a store that is refused: what stopped the run"
  expect_contains "$out" "FACT store: FAIL — not reached" \
    "a store that is refused: the row the store could not write"
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
  # No pull request exists, so no step wrote the author's row, and the block
  # reads that key the way it reads any unreached one: not a pass.
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

  # --merge measures U4's merge and nothing else: the rotation is still
  # unmeasured, so the block's verdict is INCOMPLETE (exit 3) even though every
  # row it does carry reads PASS. That is the reading the register takes: a
  # measured merge is not a finished check.
  RUN180_ARGS=(--merge)
  run180 merge-allowed
  expect_status "$status" 3 "a merge the scopes admit"
  expect_contains "$out" "FACT U4-merge-a: PASS" "a merge the scopes admit: the fact"
  expect_contains "$out" "cannot be undone" "a merge the scopes admit: what the merge is"
  expect_contains "$out" "FACT rotation: UNVERIFIED" "a merge the scopes admit: the rotation is still unmeasured"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "a merge the scopes admit: the block's last line"
}

test_rotation() {
  RUN180_ARGS=(--new-token-file "$work/rotation/token2" --merge --gcloud-config "$work/rotation/gcloudconfig")
  run180 rotation
  expect_status "$status" 0 "the rotation"
  expect_contains "$out" "FACT rotation: PASS" "the rotation: the fact"
  expect_contains "$out" "the next run uses the new version" "the rotation: the reading it makes"
  # What makes that reading a reading: the check asked GET /2.0/user with the
  # OLD value before it launched anything, and the row says Bitbucket refused it.
  # A run launched while the old value still worked would prove nothing.
  expect_contains "$out" "Bitbucket REFUSES the old value" "the rotation: the refusal the row turns on"
  # The version counts come from what gcloud really prints: the full resource
  # name for a version, turned into a count by the basename the check asks for
  # and the last path segment the reader accepts. A listing read as the full path
  # would count 0 and the row would read '? version(s)'.
  expect_contains "$out" "1 version(s) before, 2 after" "the rotation: the version list it read"
  # Two stores, the original and the rotated value, and both are the same value
  # write: the rotation's first half is a new version of the same secret.
  expect_store_argv "$work/rotation" "the rotation: the stores" 2
  # Three launches, and the rotation's reading is the row of the THIRD one: the
  # polling is what makes that run's row the one read.
  expect_runs_polled "$work/rotation" "the rotation" 3
  assert_no_secret "$work/rotation" "the rotation"

  RUN180_MODE=rotation-no-push
  RUN180_ARGS=(--new-token-file "$work/rotation-no-push/token2")
  run180 rotation-no-push
  expect_status "$status" 1 "a rotation the next run does not use"
  expect_contains "$out" "FACT rotation: FAIL" "a rotation the next run does not use: the fact"

  # The old value still accepted by Bitbucket: the criterion is NOT measured, and
  # the row has to say so rather than claim the new version is in use. With the
  # old token live, a run launched after the store pushes exactly the same way —
  # a job still mounting the old value, or a pinned version — so the check may
  # not launch one and call the result a rotation. The fake's `old-token-live`
  # mode is the owner who has not revoked yet, and the run is INCOMPLETE: the
  # rotation's row is the one thing this run measured wrongly before.
  RUN180_API_MODE=old-token-live
  RUN180_ARGS=(--new-token-file "$work/rotation-live/token2")
  run180 rotation-live
  expect_status "$status" 3 "a rotation whose old value is still accepted"
  expect_contains "$out" "FACT rotation: UNVERIFIED" "a rotation whose old value is still accepted: the fact"
  expect_contains "$out" "still ACCEPTED by Bitbucket" "a rotation whose old value is still accepted: the reading"
  expect_contains "$out" "Revoke it in the sandbox workspace's settings" "a rotation whose old value is still accepted: what to do about it"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "a rotation whose old value is still accepted: the block's last line"
  # Nothing was launched for the post-rotation run: the two runs of the two
  # repositories and no third.
  local launched
  launched=$(grep -c '^run --repo' "$work/rotation-live/fugaro.log" 2>/dev/null || true)
  [ "${launched:-0}" -eq 2 ] \
    || fail "a rotation whose old value is still accepted: the check launched ${launched:-0} run(s), want the two of the two repositories and none after the rotation — a run whose push could not settle the criterion must not be billed"

  # /2.0/user refused from the start, which is U7's open question answered "no"
  # for a token scoped to repository:write and pullrequest:write. That standing
  # refusal is NOT a revocation: the old value is still live on the repository
  # read the rotation waits on, so the row has to read UNVERIFIED and no run may
  # be launched after it. A rotation that probed /2.0/user would read this 403 as
  # the revocation, launch the run and call the criterion PASS while the old
  # value was still live — the reading this case exists to catch.
  RUN180_API_MODE=user-forbidden
  RUN180_ARGS=(--new-token-file "$work/rotation-forbidden/token2")
  run180 rotation-forbidden
  expect_status "$status" 3 "a rotation whose /2.0/user is refused"
  expect_contains "$out" "FACT rotation: UNVERIFIED" "a rotation whose /2.0/user is refused: the fact"
  expect_contains "$out" "still ACCEPTED by Bitbucket" "a rotation whose /2.0/user is refused: the reading"
  # Which request the wait read, and what it saw: the repository step_inputs had
  # required to answer 200 with this same value, still answering 200 — not the
  # /2.0/user refusal, which says nothing about this value.
  expect_contains "$out" "GET /2.0/repositories/sandbox/one with it answered 200" "a rotation whose /2.0/user is refused: the request it read instead"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "a rotation whose /2.0/user is refused: the block's last line"
  launched=$(grep -c '^run --repo' "$work/rotation-forbidden/fugaro.log" 2>/dev/null || true)
  [ "${launched:-0}" -eq 2 ] \
    || fail "a rotation whose /2.0/user is refused: the check launched ${launched:-0} run(s), want the two of the two repositories and none after the rotation — a /2.0/user refusal is not a revocation, and a run whose push could not settle the criterion must not be billed"

  # The container is found by filter, never composed: a listing that names
  # several (or none) stops rather than picking one.
  RUN180_MODE=no-container
  RUN180_ARGS=(--new-token-file "$work/rotation-nocontainer/token2")
  run180 rotation-nocontainer
  expect_status "$status" 2 "a container listing that names none"
  expect_contains "$out" "was not found once" "a container listing that names none: the reason"
}

# The second machine runs the CLI in a scratch home, so the ONE thing it has to
# borrow is the owner's Application Default Credentials — handed to it BY PATH.
# The step's own regression was to point CLOUDSDK_CONFIG at an empty directory
# inside that scratch home, where the CLI finds no ADC at all: the seeded command
# then cannot read the installation and the step measures nothing. The fake
# `fugaro` records the two variables the CLI's own order reads, so this pins
# where the credentials came from and, just as much, that they are not copied.
test_second_machine() {
  local log
  RUN180_ARGS=(--gcloud-config "$work/second-machine/gcloudconfig")
  run180 second-machine
  # The rotation and the merge are unmeasured here (no --new-token-file, no
  # --merge), so the run is INCOMPLETE: what this case is about is the row.
  expect_status "$status" 3 "the second machine"
  expect_contains "$out" "FACT second-machine: PASS" "the second machine: the fact"
  expect_seed_argv "$work/second-machine" "the second machine" "$work/second-machine/checkout-a"
  log=$(cat "$work/second-machine/fugaro.log")
  # `--gcloud-config` is the owner's own directory, named by path, and it is what
  # the seeded machine was run with.
  expect_contains "$log" "config-only-cloudsdk=$work/second-machine/gcloudconfig" \
    "the second machine: the credentials it was seeded with, by path"
  # And never the scratch home's own, which is empty: a check that pointed the
  # CLI there would measure nothing and could not tell. The needle is the path
  # the CLI's own order falls back to ($HOME/.config/gcloud, adcpath.go), with
  # HOME the scratch home — a needle near it rather than that path would hold
  # for free. Nothing was copied either: the owner's directory is the only one
  # the run ever names, and the scratch home it might have been copied into is
  # gone with the rest of the scratch directory.
  expect_absent "$log" "home/.config/gcloud" \
    "the second machine: a gcloud config inside the scratch home"
  # `update --repo` is a bool whose positional PATH defaults to the current
  # directory: the plan has to be about the allow-listed checkout, not about
  # whatever directory the check was launched from.
  expect_contains "$log" "plan-cwd=$work/second-machine/checkout-a" \
    "the second machine: the plan's own directory"

  # The flag has to WIN over an inherited $GOOGLE_APPLICATION_CREDENTIALS, not
  # merely be passed beside it: the CLI reads that variable BEFORE the gcloud
  # config directory (internal/cli/adcpath.go), so an owner who has one exported
  # — for another project, another machine — would otherwise be measuring with
  # credentials they did not name, and the flag would decide nothing. The fake
  # records both variables, so an empty one is a reading of that: the line ends
  # at the variable, which a value in it would not.
  RUN180_ARGS=(--gcloud-config "$work/second-shadowed/gcloudconfig")
  RUN180_ENV=("GOOGLE_APPLICATION_CREDENTIALS=$work/second-shadowed/elsewhere.json")
  run180 second-shadowed
  expect_contains "$(cat "$work/second-shadowed/fugaro.log")" \
    "config-only-cloudsdk=$work/second-shadowed/gcloudconfig" \
    "the second machine with an inherited GOOGLE_APPLICATION_CREDENTIALS: the flag's own directory"
  grep -q 'config-only-adc=$' "$work/second-shadowed/fugaro.log" \
    || fail "the second machine with an inherited GOOGLE_APPLICATION_CREDENTIALS: it shadowed --gcloud-config, so the credentials the seeded machine was handed are not the ones the owner named: $(cat "$work/second-shadowed/fugaro.log")"

  # Without the flag, CLOUDSDK_CONFIG is what the check passes through — the
  # order of internal/cli/adcpath.go, and the flag only exists for an ADC
  # somewhere else.
  RUN180_ENV=("CLOUDSDK_CONFIG=$work/second-machine-env/gcloudconfig" "GOOGLE_APPLICATION_CREDENTIALS=")
  run180 second-machine-env
  expect_contains "$(cat "$work/second-machine-env/fugaro.log")" \
    "config-only-cloudsdk=$work/second-machine-env/gcloudconfig" \
    "the second machine without --gcloud-config: the environment's own CLOUDSDK_CONFIG"

  RUN180_MODE=second-plan-hides-the-token
  run180 second-hidden
  expect_status "$status" 1 "a second machine whose plan hides the per-repository token"
  expect_contains "$out" "FACT second-machine: FAIL" "a second machine whose plan hides the per-repository token: the fact"
  expect_contains "$out" "D467" "a second machine whose plan hides the per-repository token: the decision it contradicts"
  expect_contains "$(cat "$work/second-hidden/fugaro.log")" "plan-cwd=$work/second-hidden/checkout-a" \
    "a second machine whose plan hides the per-repository token: the plan's own directory"

  RUN180_MODE=second-seed-fails
  RUN180_ARGS=(--gcloud-config "$work/second-seed-fails/gcloudconfig")
  run180 second-seed-fails
  expect_status "$status" 3 "a second machine that could not be seeded"
  expect_contains "$out" "FACT second-machine: UNVERIFIED" "a second machine that could not be seeded: the fact"
  expect_contains "$out" "U5 is NOT measured by this run" "a second machine that could not be seeded: what is left open"
  # The row names the flag to pass, so the owner can fix the one thing that
  # stopped it rather than reading an UNVERIFIED row with no way forward.
  expect_contains "$out" "--gcloud-config <dir>" "a second machine that could not be seeded: the way forward"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "a second machine that could not be seeded: the block's last line"
}

test_live_test() {
  RUN180_ENV=("FAKE180_GO_MODE=fail")
  run180 live-fail
  expect_status "$status" 1 "a live test that fails"
  expect_contains "$out" "FACT live-test: FAIL" "a live test that fails: the fact"
  expect_contains "$out" "TestLiveBitbucket" "a live test that fails: the harness it names"

  # A criterion the step could not verify makes the run INCOMPLETE, not a pass:
  # there is no checkout to run the live test from, so nothing measured the
  # token against the API itself.
  RUN180_ARGS=(--source "$work/live-nosource/absent")
  run180 live-nosource
  expect_status "$status" 3 "no source tree"
  expect_contains "$out" "FACT live-test: UNVERIFIED" "no source tree: the fact"
  expect_last_line "$out" "CHECK 180 RESULT: INCOMPLETE (exit 3)" "no source tree: the block's last line"
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

# A credential a command said in its OWN words is the one leak assert_no_secret
# cannot catch: that assertion's sentinel is deliberately not credential-shaped,
# so a redact_secrets that stopped filtering would leave it green. The two places
# a command's words reach the block are last_line (a command's log) and curl_note
# (curl's own stderr), and the block is a thing the check says to paste — so both
# paths are driven here with fakes made to say a credential, and both have to
# show <redacted> where a value was while the words AROUND it survive (a filter
# that dropped the whole line would leave the owner a row saying nothing about
# what failed).
test_a_credential_a_command_said_never_reaches_the_block() {
  # The run-log path: `fugaro update --repo` fails and its last line says a
  # Bearer value and a refresh_token, which last_line reads into the update row.
  RUN180_MODE=update-leaks
  run180 leaks-update
  expect_status "$status" 1 "a credential in a command's log"
  expect_contains "$out" "<redacted>" "a credential in a command's log: what was filtered is marked as filtered"
  expect_absent "$out" "FAKE-NOT-A-REAL-BEARER-4d17" "a credential in a command's log: a Bearer value never reaches the block"
  expect_absent "$out" "FAKE-NOT-A-REAL-REFRESH-TOKEN-7c05" "a credential in a command's log: a refresh_token never reaches the block"
  expect_contains "$out" "FAILED (exit 1)" "a credential in a command's log: the words that say what failed survive the filter"
  assert_no_secret "$work/leaks-update" "a credential in a command's log"

  # curl's own path: the token is refused, so the check stops with curl_note in
  # the aborted row, and the wrapper makes curl say a Basic value and a
  # client_secret. This is the only way to reach that guard: the real curl prints
  # no credential, so no other test in this suite can make this path say one.
  RUN180_API_MODE=wrong-token
  RUN180_ENV=("FAKE180_CURL_MODE=leaks")
  run180 leaks-curl
  expect_status "$status" 2 "a credential in curl's own words"
  expect_contains "$out" "<redacted>" "a credential in curl's own words: what was filtered is marked as filtered"
  expect_absent "$out" "RkFLRS1OT1QtQS1SRUFMLUJFQVJFUg==" "a credential in curl's own words: a Basic value never reaches the block"
  expect_absent "$out" "FAKE-NOT-A-REAL-CLIENT-SECRET-4b21" "a credential in curl's own words: a client_secret never reaches the block"
  expect_contains "$out" "curl: (22) The requested URL returned error: 401" "a credential in curl's own words: the words that say what failed survive the filter"
  assert_no_secret "$work/leaks-curl" "a credential in curl's own words"
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
t test_unmeasured_criteria_are_not_a_pass
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_cloud_tools
t test_belong_is_refused_everywhere
t test_endpoint_override_shapes_are_refused
t test_token_file_complaints
t test_cleanup_survives_an_unset_home
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
t test_a_credential_a_command_said_never_reaches_the_block
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check180_test: $failures failure(s)" >&2
  exit 1
fi
echo "check180_test: all tests passed" >&2
