#!/usr/bin/env bash
# Tests for scripts/live-checks/check591.sh, against fake tools on PATH and a
# fake credentials service on loopback: nothing here reaches the network, no real
# credential exists, no Fugaro installation is read, and the two tokens and the
# identity token the test feeds in are sentinels asserted absent from everything
# the run prints or leaves behind.
#
# What the fakes are: `fugaro` (doctor, creds ls, run, runs ls), `gcloud`
# (logging read, run services describe, auth print-identity-token) and `git`
# (which fails every call, so a check that needed a checkout or ran git of its
# own would show up here), each a few lines of bash that answer from a FAKE591_*
# mode and write what they were asked to a log — and one python3 server that
# answers BOTH faces of the real thing on loopback: Bitbucket Cloud's API (with a
# different answer for each token type, and a hostile mode whose bodies echo back
# the token they were sent) and the credentials service's front door on the proxy
# route, whose 403 `unverified_run_identity` is the one refusal the check forces.
#
# Usage: bash scripts/live-checks/check591_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check591.sh
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
pids=()
cleanup_all() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  if [ -n "${CHECK591_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
# expect_no_request DIR WHAT — the fake service answered nothing for that run.
# Every request it answers is appended to the run's fake.log, so this is the
# reading of "no token was ever sent anywhere": a guard that refused only after a
# read would leave a line here, and one that is checked by a file nothing writes
# proves nothing at all.
expect_no_request() { # dir what
  [ ! -s "$1/fake.log" ] || fail "$2: a request reached the service: $(tr '\n' ' ' <"$1/fake.log")"
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
TOKEN_SENTINEL='FAKE-REPO-TOKEN-FOR-CHECK-591-TESTS-NOT-A-CREDENTIAL'
PROJECT_SENTINEL='FAKE-PROJECT-TOKEN-FOR-CHECK-591-TESTS-NOT-A-CREDENTIAL'
IDENTITY_SENTINEL='FAKE-IDENTITY-TOKEN-FOR-CHECK-591-TESTS-NOT-A-CREDENTIAL'
# What the fake installation's own doctor reports, and the two records the check
# expects the installation to hold (Check 590's own names, which are this check's
# defaults).
GCP_PROJECT_NAME='fugaro-dev'
PROJECT_UUID='{a1b2c3d4-0000-0000-0000-00000000c591}'
REPO='dimi_bmb/sandbox-a'
OTHER_REPO='dimi_bmb/sandbox-b'
REPO_TOKEN_NAME='bitbucket-token--ck590-repo'
PROJECT_TOKEN_NAME='bitbucket-token--ck590-project'
RUN_A_ID='run-591-sandbox-a'
RUN_B_ID='run-591-sandbox-b'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation inputs bundle xtrace premise-repo premise-project
  run-a run-b audit-a audit-b swap refusal restore)

# The knobs a case sets before it calls run591; every one of them is reset after
# the run, so a case that forgets to clear one cannot leak into the next.
RUN591_ARGS=()
RUN591_ENV=()
RUN591_MODE=ok
RUN591_GCLOUD_MODE=ok
RUN591_API_MODE=ok
RUN591_FRONTDOOR_MODE=ok
RUN591_AUDIT_MODE=ok
RUN591_LAG=""
RUN591_REPO=""
RUN591_OTHER_REPO=""
RUN591_TOKEN_FILE=""
RUN591_PROJECT_TOKEN_FILE=""
RUN591_PROJECT_ARGS=()
RUN591_RUN_SECONDS=""
RUN591_AUDIT_WAIT_SECONDS=""
RUN591_UNSET_HOME=""
RUN591_SHELL=""
RUN591_FAKE_REPO_TOKEN=""

# --------------------------------------------------------------------- the fake
write_fakes() { # dir
  local dir=$1
  cat >"$dir/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check591.sh's test only: it answers the four calls the
# check makes and records what it was asked. It writes nothing and reads no
# token: `creds ls` names rows and never values, and no call here takes one.
set -u
mode=${FAKE591_MODE:-ok}
log=${FAKE591_LOG:-/dev/null}
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
  shift
  [ "${1:-}" = set ] && { echo "fake fugaro: this check writes nothing to the installation" >&2; exit 2; }
  [ "${1:-}" = ls ] || { echo "fake fugaro: creds ${1:-}" >&2; exit 2; }
  [ "$mode" = ls-fails ] && { echo "ERROR: the installation's bundle could not be read" >&2; exit 1; }
  repo_row='{"name":"bitbucket-token--ck590-repo","present":true,"type":"repo","coverage":["dimi_bmb/sandbox-a"],"state":"ok"}'
  project_row='{"name":"bitbucket-token--ck590-project","present":true,"type":"project","coverage":["dimi_bmb"],"state":"ok"}'
  case "$mode" in
  ls-missing-repo) repo_row= ;;
  ls-absent) repo_row='{"name":"bitbucket-token--ck590-repo","present":false,"type":"repo","coverage":["dimi_bmb/sandbox-a"],"state":"ok"}' ;;
  ls-wrong-type) repo_row='{"name":"bitbucket-token--ck590-repo","present":true,"type":"workspace","coverage":["dimi_bmb/sandbox-a"],"state":"ok"}' ;;
  ls-both-coverage) repo_row='{"name":"bitbucket-token--ck590-repo","present":true,"type":"repo","coverage":["dimi_bmb/sandbox-a","dimi_bmb/sandbox-b"],"state":"ok"}' ;;
  ls-other-covered) repo_row='{"name":"bitbucket-token--ck590-repo","present":true,"type":"repo","coverage":["dimi_bmb/*"],"state":"ok"}' ;;
  esac
  case "$mode" in
  ls-missing-project) project_row= ;;
  ls-project-narrow) project_row='{"name":"bitbucket-token--ck590-project","present":true,"type":"project","coverage":["dimi_bmb/sandbox-a"],"state":"ok"}' ;;
  esac
  if [ -n "$repo_row" ] && [ -n "$project_row" ]; then
    printf '{"project":"fugaro","secret":"fugaro-bitbucket-tokens","version":"4","changed":false,"credentials":[%s,%s]}\n' "$repo_row" "$project_row"
  elif [ -n "$repo_row" ]; then
    printf '{"project":"fugaro","secret":"fugaro-bitbucket-tokens","version":"4","changed":false,"credentials":[%s]}\n' "$repo_row"
  else
    printf '{"project":"fugaro","secret":"fugaro-bitbucket-tokens","version":"4","changed":false,"credentials":[%s]}\n' "$project_row"
  fi
  exit 0
  ;;
run)
  repo=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo) repo=$2; shift 2 ;;
    *) shift ;;
    esac
  done
  [ "$mode" = run-fails ] && { echo "ERROR: the run could not be launched" >&2; exit 1; }
  [ "$mode" = run-no-id ] && { printf '{"status":"launched","project":"fugaro"}\n'; exit 0; }
  printf '{"status":"launched","run_id":"run-591-%s","project":"fugaro"}\n' "${repo##*/}"
  exit 0
  ;;
runs)
  repo=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo) repo=$2; shift 2 ;;
    *) shift ;;
    esac
  done
  name=${repo##*/}
  terminal=true
  settled=true
  pushed=true
  status=finished
  outcome=ready
  pr=101
  case "$mode" in
  runs-empty) printf '{"runs":[]}\n'; exit 0 ;;
  runs-hang) terminal=false; settled=false; pushed=false; status=running; outcome=""; pr=0 ;;
  runs-nopush) pushed=false; status=finished; outcome=draft; pr=0 ;;
  runs-nopr) pr=0; status=finished; outcome=draft ;;
  esac
  printf '{"runs":[{"run":"%s","repo":"%s","run_id":"run-591-%s","status":"%s","terminal":%s,"settled":%s,"branch":"fugaro/check591","outcome":"%s","pushed":%s,"pr":%s}]}\n' \
    "$name" "$repo" "$name" "$status" "$terminal" "$settled" "$outcome" "$pushed" "$pr"
  exit 0
  ;;
esac
echo "fake fugaro: unknown subcommand ${1:-}" >&2
exit 2
FUGARO_EOF
  chmod +x "$dir/fugaro"

  cat >"$dir/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake `gcloud`, for check591.sh's test only: the service's own URL, the
# service's own lines for a run, and the identity token the check mints for its
# one refused call. The lines are read from a JSON file the test writes, filtered
# by the run id the check put in its filter — so what the check sees is a
# function of what it asked for.
set -u
mode=${FAKE591_GCLOUD_MODE:-ok}
log=${FAKE591_GCLOUD_LOG:-/dev/null}
printf '%s\n' "$*" >>"$log"
case "${1:-}" in
auth)
  shift
  [ "${1:-}" = print-identity-token ] && shift
  [ "$mode" = id-fails ] && { echo "ERROR: (gcloud.auth.print-identity-token) There is no active account" >&2; exit 1; }
  printf '%s\n' "${FAKE591_IDENTITY:-}"
  exit 0
  ;;
run)
  shift
  [ "${1:-}" = services ] && shift
  [ "${1:-}" = describe ] && shift
  case "$mode" in
  service-fails)
    echo "ERROR: (gcloud.run.services.describe) The service fugaro-credentials does not exist" >&2
    exit 1
    ;;
  service-no-url)
    printf '{"status":{},"spec":{}}\n'
    exit 0
    ;;
  esac
  printf '{"status":{"url":"%s"},"spec":{}}\n' "${FAKE591_SERVICE_URL:-}"
  exit 0
  ;;
logging)
  shift
  [ "${1:-}" = read ] && shift
  filter=${1:-}
  [ "$mode" = logging-refused ] && { echo "ERROR: (gcloud.logging.read) ResponseError: status=[403]" >&2; exit 1; }
  rid=$(printf '%s' "$filter" | sed -n 's/.*jsonPayload\.run="\([^"]*\)".*/\1/p')
  count=0
  if [ -n "${FAKE591_COUNT_FILE:-}" ]; then
    count=$(cat "$FAKE591_COUNT_FILE" 2>/dev/null || true)
    case "${count:-}" in '' | *[!0-9]*) count=0 ;; esac
    count=$((count + 1))
    printf '%s\n' "$count" >"$FAKE591_COUNT_FILE"
  fi
  python3 - "$FAKE591_AUDIT_FILE" "$rid" "${FAKE591_LAG:-}" "$count" <<'PY'
import json
import sys

path, rid, lag, count = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
if lag == "lagging" and count <= 1:
    # What Cloud Logging answers the instant a run's row turns terminal: nothing
    # has been ingested yet. The check has to keep asking.
    print("[]")
    raise SystemExit(0)
try:
    with open(path, "r", encoding="utf-8") as fh:
        entries = json.load(fh)
except (OSError, ValueError):
    entries = []
print(json.dumps([e for e in entries if (e.get("jsonPayload") or {}).get("run") == rid]))
PY
  exit 0
  ;;
esac
echo "fake gcloud: unknown subcommand ${1:-}" >&2
exit 2
GCLOUD_EOF
  chmod +x "$dir/gcloud"

  cat >"$dir/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git`, for check591.sh's test only: every call fails and is logged. The
# check asks whether its scratch directory is inside a work tree and is expected
# to run nothing else — a check that started needing a checkout, or ran git
# itself, shows up as an extra line in this log.
set -u
printf '%s\n' "$*" >>"${FAKE591_GIT_LOG:-/dev/null}"
exit 1
GIT_EOF
  chmod +x "$dir/git"

  cat >"$dir/fake.py" <<'FAKE_EOF'
#!/usr/bin/env python3
"""A fake Bitbucket Cloud AND a fake credentials service, for check591.sh's test
only, on one loopback port.

It never reaches the network and holds no credential of its own: the tokens it
echoes back are the ones it read out of the requests it was just sent. The
Bitbucket face answers `/2.0/repositories/<ws>/<name>` the way each token type
does — a repository token reads the repository it was created for and is refused
at the other, a project token reads both — and the service face answers the proxy
route, whose 403 `unverified_run_identity` is the refusal the check forces.

Modes change one answer at a time so the check's rows can be read apart.
"""
import json
import os
import sys
import http.server

REPO = os.environ.get("FAKE591_REPO_TOKEN", "")
PROJECT = os.environ.get("FAKE591_PROJECT_TOKEN", "")
IDENTITY = os.environ.get("FAKE591_IDENTITY", "")
APILOG = os.environ.get("FAKE591_API_LOG", "")
MODE = os.environ.get("FAKE591_API_MODE", "ok")
FRONT = os.environ.get("FAKE591_FRONTDOOR_MODE", "ok")
COVERED = "dimi_bmb/sandbox-a"
OTHER = "dimi_bmb/sandbox-b"
PROXY = "/v1/bitbucket/proxy/"


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

    def reply_raw(self, status, body, content_type="text/html"):
        raw = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def note(self, what):
        # One line per request, with what the request carried and nothing it can
        # choose: the token is written as the NAME of the sentinel it matched, and
        # an unrecognised one is written as `other`. A log is where a token would
        # survive a run if a fake ever wrote a value into it.
        if not APILOG:
            return
        name = "none"
        if self.token == REPO:
            name = "repo"
        elif self.token == PROJECT:
            name = "project"
        elif self.token == IDENTITY:
            name = "identity"
        elif self.token:
            name = "other"
        with open(APILOG, "a", encoding="utf-8") as fh:
            if what == "front":
                run = "runid=yes" if self.headers.get("X-Fugaro-Run") else "runid=no"
                job = "job=yes" if self.headers.get("X-Fugaro-Job") else "job=no"
                fh.write("FRONTDOOR %s token=%s %s %s\n" % (self.path, name, run, job))
            else:
                fh.write("API GET %s token=%s\n" % (self.path.split("?", 1)[0], name))

    def frontdoor(self):
        self.note("front")
        if FRONT == "open":
            self.reply(200, {"ok": True})
            return
        if FRONT == "iam":
            # What Cloud Run's own front end answers a caller without run.invoker:
            # not the service's wire document at all.
            self.reply_raw(403, "<html><title>403 Forbidden</title></html>")
            return
        if FRONT == "unauthorized":
            self.reply_raw(401, "Unauthorized")
            return
        if FRONT == "other-type":
            self.reply(403, {"error": "not_covered", "message": "the token does not cover this repository"})
            return
        if FRONT == "badrequest":
            self.reply(400, {"error": "not_covered", "message": "the token does not cover this repository"})
            return
        if FRONT == "hostile":
            # A body that echoes the identity token it was sent, in the very field
            # the check reads the type out of.
            self.reply(403, {"error": self.token, "message": self.token, "tried": [self.token]})
            return
        self.reply(403, {"error": "unverified_run_identity",
                         "message": "this call carries no run identity"})

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        head = self.headers.get("Authorization") or ""
        self.token = head[7:] if head.startswith("Bearer ") else ""
        if path.startswith(PROXY):
            self.frontdoor()
            return
        path = path.rstrip("/")
        self.note("api")
        if self.token not in (REPO, PROJECT):
            self.reply(401, {"error": {"code": "invalid_token"}})
            return
        if path.startswith("/2.0/repositories/"):
            rest = path[len("/2.0/repositories/"):]
            parts = [p for p in rest.split("/") if p]
            if len(parts) == 2:
                full = "%s/%s" % (parts[0], parts[1])
                if self.token == REPO and full != COVERED and MODE != "repo-reads-b":
                    # The design's premise (F7/F9): the repository token is refused
                    # at the other repository, which is what makes a run there
                    # served by the project token a real choice.
                    self.reply(403, {"error": {"code": self.token if MODE == "hostile" else "forbidden",
                                               "message": self.token}})
                    return
                code = self.token if MODE == "hostile" else "not_found"
                self.reply(200, {"full_name": full, "project": {"key": "SANDBOX"},
                                 "is_private": True, "error": {"code": code}})
                return
            self.reply(404, {"error": {"code": "not_found"}})
            return
        self.reply(404, {"error": {"code": "not_found"}})


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print("fake591 listening on 127.0.0.1:%d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
FAKE_EOF
}

FAKE_PORT=""
# start_fake DIR — starts the fake service and waits, bounded, for the port it
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
  FAKE591_API_MODE=${RUN591_API_MODE:-ok} FAKE591_FRONTDOOR_MODE=${RUN591_FRONTDOOR_MODE:-ok} \
    FAKE591_REPO_TOKEN="${RUN591_FAKE_REPO_TOKEN:-$TOKEN_SENTINEL}" \
    FAKE591_PROJECT_TOKEN="$PROJECT_SENTINEL" \
    FAKE591_IDENTITY="$IDENTITY_SENTINEL" FAKE591_API_LOG="$1/fake.log" \
    python3 "$1/bin/fake.py" >"$1/fake.out" 2>"$1/fake.err" &
  local pid=$!
  pids+=("$pid")
  local out=$1/fake.out i=0
  FAKE_PORT=""
  while [ "$i" -lt 200 ]; do
    if [ -e "$out" ]; then
      FAKE_PORT=$(sed -n 's/^fake591 listening on 127\.0\.0\.1:\([0-9]\{1,\}\)$/\1/p' "$out" | head -n 1)
      [ -n "$FAKE_PORT" ] && return 0
    fi
    kill -0 "$pid" 2>/dev/null || { fail "the fake service died: $(tail -n 3 "$1/fake.err" | tr '\n' ' ')"; return 1; }
    sleep 0.1
    i=$((i + 1))
  done
  fail "the fake service never reported 'fake591 listening on 127.0.0.1:<port>' in $out within 20s"
  return 1
}

# ------------------------------------------------------------------ the lines
# write_audit FILE MODE — the credentials service's own lines for the two runs,
# as `gcloud logging read` would answer them. The credential NAMES are the
# check's defaults, the repositories are the two the check runs on, and each
# entry carries the fields the service writes (R9's own shape: message, run,
# repository, credential, purpose, status).
#
# A model call is a line of the same run (purpose `forward`), and the default
# mode gives run A one naming the OTHER token: a check that read the credential
# off every line of the run instead of off the two purposes a Bitbucket call has
# would read two tokens for one run and fail this suite.
write_audit() { # write_audit FILE MODE
  python3 - "$1" "$2" "$RUN_A_ID" "$RUN_B_ID" "$REPO" "$OTHER_REPO" "$REPO_TOKEN_NAME" "$PROJECT_TOKEN_NAME" <<'PY'
import json
import sys

out, mode, run_a, run_b, repo, other, repo_token, project_token = sys.argv[1:9]


def line(run, repository, purpose, credential, status="ok"):
    return {"resource": {"type": "cloud_run_revision"},
            "jsonPayload": {"message": "credsvc: audit", "run": run, "repository": repository,
                            "job": run + "-job", "caller": "runner", "credential": credential,
                            "purpose": purpose, "tier": "-", "status": status, "slip": "-",
                            "owner": "fugaro"}}


def refused(run, repository, cls):
    return {"resource": {"type": "cloud_run_revision"},
            "jsonPayload": {"message": "credsvc: a Bitbucket call was refused", "run": run,
                            "repository": repository, "class": cls}}


entries = []
if mode == "ok":
    # Run A: the repository token serves it, on both a git and a Bitbucket-API
    # call, and the run's model calls name another credential entirely.
    entries += [line(run_a, repo, "git", repo_token),
                line(run_a, repo, "bitbucket-api", repo_token),
                line(run_a, repo, "forward", project_token),
                line(run_a, repo, "forward", project_token)]
    # Run B: the project token serves it, because the repository token is not
    # recorded for it.
    entries += [line(run_b, other, "git", project_token),
                line(run_b, other, "bitbucket-api", project_token)]
elif mode == "swapped-repo":
    entries += [line(run_a, repo, "git", repo_token),
                line(run_b, other, "git", repo_token),
                line(run_b, other, "bitbucket-api", repo_token)]
elif mode == "swapped-project":
    entries += [line(run_a, repo, "git", project_token),
                line(run_b, other, "git", project_token)]
elif mode == "a-project":
    entries += [line(run_a, repo, "git", project_token),
                line(run_b, other, "git", project_token),
                line(run_b, other, "bitbucket-api", project_token)]
elif mode == "empty":
    entries = []
elif mode == "noisy":
    entries += [line(run_a, repo, "git", repo_token),
                refused(run_a, repo, "not_covered"),
                line(run_a, repo, "bitbucket-api", repo_token)]
    entries += [line(run_b, other, "git", project_token)]
elif mode == "unknown-repo":
    entries += [line(run_a, "dimi_bmb/somewhere-else", "git", repo_token),
                line(run_a, "dimi_bmb/somewhere-else", "bitbucket-api", repo_token)]
    entries += [line(run_b, other, "git", project_token)]
elif mode == "fallback":
    # A token the store holds is skipped for a wider one (R12): both names on the
    # run's lines, which is a reading rather than a failure.
    entries += [line(run_a, repo, "git", repo_token),
                line(run_a, repo, "git", project_token)]
    entries += [line(run_b, other, "git", project_token)]
with open(out, "w", encoding="utf-8") as fh:
    json.dump(entries, fh)
PY
}

# ---------------------------------------------------------------- running it
write_token() { # write_token FILE [value]
  printf '%s\n' "${2:-$TOKEN_SENTINEL}" >"$1"
  chmod 600 "$1"
}

# run591 DIR — runs the check once against the fakes. RUN591_ARGS appends
# arguments, RUN591_ENV names environment variables (NAME=value) to set,
# RUN591_MODE picks the fake fugaro's behaviour, RUN591_GCLOUD_MODE the fake
# gcloud's, RUN591_API_MODE and RUN591_FRONTDOOR_MODE the two faces of the fake
# service, RUN591_AUDIT_MODE what the service's lines say, and RUN591_SHELL
# replaces the interpreter the check is started with (the one place a tracing
# shell can be arranged). Sets $status, $out and $err.
run591() {
  local dir=$work/$1
  # The project's key or UUID, as an ARRAY: a quoted word nested inside a
  # `${VAR:-default}` expansion is one argument, not two, and the check would see
  # `--project-key SANDBOX` as a single unknown flag.
  local project_args=(--project-key SANDBOX)
  if [ ${#RUN591_PROJECT_ARGS[@]} -gt 0 ]; then project_args=("${RUN591_PROJECT_ARGS[@]}"); fi
  mkdir -p "$dir/tmp" "$dir/bin"
  write_token "$dir/token" "$TOKEN_SENTINEL"
  write_token "$dir/token-project" "$PROJECT_SENTINEL"
  write_audit "$dir/audit.json" "${RUN591_AUDIT_MODE:-ok}"
  : >"$dir/gcloud-count"
  # Set before the fake is started: a case whose server cannot come up leaves
  # these as its own answer rather than the previous case's.
  status=0
  out=""
  err=""
  start_fake "$dir" || return 1
  env ${RUN591_UNSET_HOME:+-u HOME} PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    CHECK591_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK591_CURL_SECONDS=20 CHECK591_LAUNCH_SECONDS=20 \
    CHECK591_RUN_SECONDS="${RUN591_RUN_SECONDS:-8}" CHECK591_RUN_POLL_SECONDS=1 \
    CHECK591_AUDIT_WAIT_SECONDS="${RUN591_AUDIT_WAIT_SECONDS:-2}" CHECK591_AUDIT_POLL_SECONDS=1 \
    FAKE591_MODE="${RUN591_MODE:-ok}" FAKE591_LOG="$dir/fugaro.log" \
    FAKE591_GCLOUD_MODE="${RUN591_GCLOUD_MODE:-ok}" FAKE591_GCLOUD_LOG="$dir/gcloud.log" \
    FAKE591_IDENTITY="$IDENTITY_SENTINEL" FAKE591_SERVICE_URL="http://127.0.0.1:$FAKE_PORT" \
    FAKE591_AUDIT_FILE="$dir/audit.json" FAKE591_COUNT_FILE="$dir/gcloud-count" \
    FAKE591_LAG="${RUN591_LAG:-}" FAKE591_GIT_LOG="$dir/git.log" \
    ${RUN591_ENV[@]+"${RUN591_ENV[@]}"} \
    ${RUN591_SHELL:-bash} "$script" \
    --repo "${RUN591_REPO:-$REPO}" \
    --other-repo "${RUN591_OTHER_REPO:-$OTHER_REPO}" \
    --token-file "${RUN591_TOKEN_FILE:-$dir/token}" \
    --project-token-file "${RUN591_PROJECT_TOKEN_FILE:-$dir/token-project}" \
    "${project_args[@]}" \
    ${RUN591_ARGS[@]+"${RUN591_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  RUN591_ARGS=()
  RUN591_ENV=()
  RUN591_MODE=ok
  RUN591_GCLOUD_MODE=ok
  RUN591_API_MODE=ok
  RUN591_FRONTDOOR_MODE=ok
  RUN591_AUDIT_MODE=ok
  RUN591_LAG=""
  RUN591_REPO=""
  RUN591_OTHER_REPO=""
  RUN591_TOKEN_FILE=""
  RUN591_PROJECT_TOKEN_FILE=""
  RUN591_PROJECT_ARGS=()
  RUN591_RUN_SECONDS=""
  RUN591_AUDIT_WAIT_SECONDS=""
  RUN591_UNSET_HOME=""
  RUN591_SHELL=""
  RUN591_FAKE_REPO_TOKEN=""
}

# assert_no_secret DIR WHAT — nothing the run printed, and no file it left
# behind, carries any of the three sentinels. The two files this TEST writes with
# a sentinel in them on purpose are its own fixtures, and they are the only ones
# any of them may be in.
assert_no_secret() {
  local dir=$1 what=$2 hits sentinel
  for sentinel in "$TOKEN_SENTINEL" "$PROJECT_SENTINEL" "$IDENTITY_SENTINEL"; do
    expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$sentinel" "$what: the token in the output"
  done
  hits=$(grep -rl -e "$TOKEN_SENTINEL" -e "$PROJECT_SENTINEL" -e "$IDENTITY_SENTINEL" "$dir" 2>/dev/null \
    | grep -v -F -x -e "$dir/token" -e "$dir/token-project" || true)
  [ -z "$hits" ] || fail "$what: a token is in $hits"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 591 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

# expect_nothing_written DIR WHAT — what the check must never do to the
# installation: no `creds set` and no `fugaro update` (a record's coverage has to
# reach the node, and that is the owner's own command, not a check's), and no
# write of any other kind through the fake.
expect_nothing_written() {
  local dir=$1 what=$2 calls
  calls=$(grep -c -e '^creds set' -e '^update' -e '^creds rm' "$dir/fugaro.log" 2>/dev/null || true)
  [ "${calls:-0}" -eq 0 ] || fail "$what: the check wrote to the installation ${calls:-0} time(s): $(grep -e '^creds set' -e '^update' -e '^creds rm' "$dir/fugaro.log" | tr '\n' ' ')"
}

# ------------------------------------------------------------------ the tests
# The all-PASS run: EVERY criterion is measured, which is what makes the block's
# last line PASS and the exit status 0.
test_happy_path() {
  run591 happy
  expect_status "$status" 0 "the happy path"
  expect_last_line "$out" "CHECK 591 RESULT: PASS (exit 0)" "the happy path"
  expect_block_keys "$out" "the happy path"
  expect_contains "$out" "FACT installation: PASS" "the installation is read before anything else"
  expect_contains "$out" "FACT bundle: PASS" "the two records are read"
  expect_contains "$out" "FACT premise-repo: PASS" "the repository token is refused at the other repository"
  expect_contains "$out" "is REFUSED 403 at dimi_bmb/sandbox-b" "the premise row says which repository it was refused at"
  expect_contains "$out" "FACT premise-project: PASS" "the project token reads both"
  expect_contains "$out" "FACT run-a: PASS" "run A pushed and opened a pull request"
  expect_contains "$out" "the run in dimi_bmb/sandbox-a (run-591-sandbox-a) pushed its branch" "the row names the run it waited for"
  expect_contains "$out" "FACT audit-a: PASS" "run A's token is read off the service's own lines"
  expect_contains "$out" "name bitbucket-token--ck590-repo — and nothing else — on all 2 of its git/Bitbucket-API line(s)" "run A was served by the repository token alone, with the run's own model calls left out of the reading"
  expect_contains "$out" "FACT audit-b: PASS" "run B's token is read off the service's own lines"
  expect_contains "$out" "name bitbucket-token--ck590-project" "run B was served by the project token"
  expect_contains "$out" "purposes bitbucket-api:1,forward:2,git:1" "the row says what the run's lines carried"
  expect_contains "$out" "FACT swap: PASS" "the two runs were served by two different tokens"
  expect_contains "$out" "dimi_bmb/sandbox-a by 'bitbucket-token--ck590-repo', dimi_bmb/sandbox-b by 'bitbucket-token--ck590-project'" "the swap row names both"
  expect_contains "$out" "FACT refusal: PASS" "the forced 403 is the service's own"
  expect_contains "$out" "unverified_run_identity" "the refusal row names the type"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory and the credential files are removed"
  assert_no_secret "$work/happy" "the happy path"
  expect_nothing_written "$work/happy" "the happy path"
  local scratch
  scratch=$(scratch_of "$work/happy")
  [ -n "$scratch" ] || fail "the happy path: the run announced no scratch directory"
  [ ! -e "$scratch" ] || fail "the happy path: the scratch directory is still there: $scratch"
}

# What the check asked the world for, read from the world's own logs: two runs,
# one per repository (the released CLI has no change-set launch), a premise read
# with each token at each repository, and exactly one call to the front door —
# with an identity token and NO run identity, which is what the refusal is about.
test_the_check_reads_and_launches_exactly_what_it_says() {
  run591 calls
  expect_status "$status" 0 "the calls"
  local runs
  runs=$(grep -c '^run ' "$work/calls/fugaro.log" || true)
  [ "${runs:-0}" -eq 2 ] || fail "the calls: the check launched ${runs:-0} run(s), want 2 (one per repository)"
  expect_contains "$(cat "$work/calls/fugaro.log")" "--repo dimi_bmb/sandbox-a" "run A's repository reaches the CLI"
  expect_contains "$(cat "$work/calls/fugaro.log")" "--repo dimi_bmb/sandbox-b" "run B's repository reaches the CLI"
  expect_contains "$(cat "$work/calls/fugaro.log")" "runs ls --repo dimi_bmb/sandbox-a" "the run is waited for in its own repository"
  local api front
  api=$(grep -c '^API GET /2.0/repositories/' "$work/calls/fake.log" || true)
  [ "${api:-0}" -eq 4 ] || fail "the calls: the API was read ${api:-0} time(s), want 4 (each token at each repository): $(tr '\n' ' ' <"$work/calls/fake.log")"
  front=$(grep -c '^FRONTDOOR ' "$work/calls/fake.log" || true)
  [ "${front:-0}" -eq 1 ] || fail "the calls: the front door was asked ${front:-0} time(s), want 1"
  expect_contains "$(cat "$work/calls/fake.log")" "FRONTDOOR /v1/bitbucket/proxy/dimi_bmb/sandbox-a.git/info/refs?service=git-upload-pack token=identity runid=no job=no" "the one forced call carries the identity token and no run identity"
  expect_contains "$(cat "$work/calls/fake.log")" "API GET /2.0/repositories/dimi_bmb/sandbox-b token=repo" "the premise read with the repository token at the other repository"
  expect_contains "$(cat "$work/calls/fake.log")" "API GET /2.0/repositories/dimi_bmb/sandbox-b token=project" "the premise read with the project token"
}

# The check needs no checkout: it never runs git of its own, and the only call it
# makes is the one that asks whether the scratch directory is a work tree.
test_the_check_runs_no_git_of_its_own() {
  run591 no-git
  expect_status "$status" 0 "no git"
  local calls
  calls=$(cat "$work/no-git/git.log" 2>/dev/null || true)
  [ "$calls" = "rev-parse --show-toplevel" ] || fail "no git: git was called with '$calls', want only 'rev-parse --show-toplevel'"
}

# The service's lines are ingested after the run finishes, so a reading taken the
# instant a run's row turns terminal is empty: the check has to keep asking.
test_the_service_lines_are_waited_for() {
  RUN591_LAG=lagging RUN591_AUDIT_WAIT_SECONDS=6 run591 lagging
  expect_status "$status" 0 "the lines that arrived late"
  expect_contains "$out" "FACT audit-a: PASS" "the row is read once the lines arrive"
  local reads
  reads=$(grep -c '^logging read' "$work/lagging/gcloud.log" || true)
  [ "${reads:-0}" -ge 2 ] || fail "the lines that arrived late: the service's lines were read ${reads:-0} time(s), want at least 2 (the first answer is empty)"
}

# Two repositories of one installation served by ONE token is the finding this
# check exists to catch: the selection is per request and narrowest first.
test_one_token_for_both_repositories_is_a_failure() {
  RUN591_AUDIT_MODE=swapped-repo run591 swapped-repo
  expect_status "$status" 1 "both runs served by the repository token"
  expect_last_line "$out" "CHECK 591 RESULT: FAIL (exit 1)" "both runs served by the repository token"
  expect_contains "$out" "FACT swap: FAIL" "the swap row failed"
  expect_contains "$out" "both runs were served by the SAME credential 'bitbucket-token--ck590-repo'" "the row says which token served both"
  expect_contains "$out" "FACT audit-a: PASS" "the per-run rows are still measured"

  RUN591_AUDIT_MODE=swapped-project run591 swapped-project
  expect_status "$status" 1 "both runs served by the project token"
  expect_contains "$out" "FACT swap: FAIL" "the swap row failed for the wider token too"
}

# A run served by a token this check's records say should not have been chosen
# for it is a failure of its own row, and it names the row's own reading.
test_a_run_served_by_the_other_token_is_a_failure() {
  RUN591_AUDIT_MODE=a-project run591 a-project
  expect_status "$status" 1 "run A served by the project token"
  expect_contains "$out" "FACT audit-a: FAIL" "run A's row failed"
  expect_contains "$out" "bitbucket-token--ck590-repo is NOT among them" "the row names the token it expected"
  expect_contains "$out" "FACT audit-b: PASS" "run B's row is unaffected"
}

# A run whose git traffic did not go through the service is UNVERIFIED rather
# than failed, and a block with one is INCOMPLETE (exit 3): the reading says the
# switch is off, not that the selection was wrong.
test_no_service_line_at_all_is_incomplete() {
  RUN591_AUDIT_MODE=empty run591 no-lines
  expect_status "$status" 3 "a run with no service line"
  expect_last_line "$out" "CHECK 591 RESULT: INCOMPLETE (exit 3)" "a run with no service line"
  expect_contains "$out" "FACT audit-a: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "carry NO git or Bitbucket-API line" "the row says what was not read"
  expect_contains "$out" "the credentials-service switch" "the row names what to look at"
  expect_contains "$out" "FACT swap: UNVERIFIED" "the swap cannot be measured from one reading"
}

# The service's own refusal lines are read too: a run served as it should be, with
# a refused call of its own beside it, is a PASS that carries the class.
test_a_refusal_line_is_carried_in_the_row() {
  RUN591_AUDIT_MODE=noisy run591 noisy
  expect_status "$status" 0 "a run with a refusal line"
  expect_contains "$out" "FACT audit-a: PASS" "the row is measured"
  expect_contains "$out" "1 refusal line(s), classes not_covered:1" "the row carries the class the service wrote"
  expect_contains "$out" "and no refusal line" "the other run's row says it had none"
}

# A run served by two tokens is the stated price of the swap (R12), not a
# failure: the narrowest usable token is chosen per request.
test_a_fallback_to_a_wider_token_is_read_as_such() {
  RUN591_AUDIT_MODE=fallback run591 fallback
  expect_status "$status" 0 "a run served by two tokens"
  expect_contains "$out" "FACT audit-a: PASS" "the row is measured"
  expect_contains "$out" "and also bitbucket-token--ck590-project" "the row names the wider token"
  expect_contains "$out" "the stated price of the swap and not a failure" "the row says why it is not a failure"
  expect_contains "$out" "FACT swap: PASS" "two different tokens were read"
}

# The repository the lines name is the pairing's own fixing of a run to its
# repository: lines naming another repository are said out loud.
test_lines_naming_another_repository_are_said_out_loud() {
  RUN591_AUDIT_MODE=unknown-repo run591 other-repo
  expect_status "$status" 0 "lines naming another repository"
  expect_contains "$out" "THE LINES NAME THE REPOSITORY dimi_bmb/somewhere-else" "the row carries the finding"
  expect_contains "$out" "AND THIS RUN WAS LAUNCHED ON dimi_bmb/sandbox-a" "the row names the repository it was launched on"
}

# The records are the premise the whole reading rests on: a repository token
# recorded for both repositories would make the swap impossible, and a project
# token that does not reach the second repository would leave it unserved.
test_the_records_have_to_make_the_selection_a_real_choice() {
  RUN591_MODE=ls-both-coverage run591 both-coverage
  expect_status "$status" 2 "a repository token covering both repositories"
  expect_contains "$out" "FACT bundle: FAIL" "the row failed"
  expect_contains "$out" "ALSO admits dimi_bmb/sandbox-b" "the row says what is wrong with the coverage"
  expect_contains "$out" "fugaro creds set" "the row names the command that fixes it"
  expect_no_request "$work/both-coverage" "a repository token covering both repositories"
  expect_absent "$(cat "$work/both-coverage/fugaro.log" 2>/dev/null || true)" "run --repo" "nothing is launched from a premise that does not hold"

  RUN591_MODE=ls-project-narrow run591 project-narrow
  expect_status "$status" 2 "a project token that does not reach the second repository"
  expect_contains "$out" "does not admit dimi_bmb/sandbox-b" "the row says which repository is not covered"

  RUN591_MODE=ls-missing-repo run591 missing-repo
  expect_status "$status" 2 "a repository token that is not in the installation's bundle"
  expect_contains "$out" "is not in the installation's bundle (present=missing)" "the row says the record is not there"

  RUN591_MODE=ls-absent run591 absent-repo
  expect_status "$status" 2 "a record the installation lists as not present"
  expect_contains "$out" "is not in the installation's bundle (present=no)" "the row reads the listing's own word"

  RUN591_MODE=ls-wrong-type run591 wrong-type
  expect_status "$status" 2 "a record typed another way"
  expect_contains "$out" "states type 'workspace' where this check needs a repository token" "the row says what the type is"

  RUN591_MODE=ls-fails run591 ls-fails
  expect_status "$status" 2 "a listing that could not be read"
  expect_contains "$out" "FACT bundle: FAIL" "the row failed"
  expect_no_request "$work/ls-fails" "a listing that could not be read"
}

# The premise rows are about Bitbucket's own answer, not about the records: a
# repository token that also reads the second repository makes the selection a
# reading of the design's order over the records rather than a necessity, and the
# row says so instead of claiming the stronger premise.
test_a_repository_token_that_reaches_both_is_read_as_the_weaker_premise() {
  RUN591_API_MODE=repo-reads-b run591 reads-both
  expect_status "$status" 0 "a repository token that reads both repositories"
  expect_contains "$out" "FACT premise-repo: PASS" "the row is measured"
  expect_contains "$out" "reads both dimi_bmb/sandbox-a and dimi_bmb/sandbox-b over REST" "the row says what was read"
  expect_contains "$out" "rather than a necessity at Bitbucket" "the row does not claim the stronger premise"
  expect_contains "$out" "FACT swap: PASS" "the swap is measured from the audit lines"
}

test_a_token_that_does_not_reach_its_repository_is_a_failure() {
  # A token the fake Bitbucket does not recognise at all: the answer is a 401, so
  # nothing about the repository token's own coverage was measured and the premise
  # this check rests on does not hold.
  RUN591_FAKE_REPO_TOKEN='FAKE-OTHER-TOKEN-FOR-CHECK-591-TESTS' run591 no-reach
  expect_status "$status" 1 "a repository token that does not read its own repository"
  expect_contains "$out" "FACT premise-repo: FAIL" "the premise row failed"
  expect_contains "$out" "so this token does not read the repository it is recorded for" "the row says why"
}

# A run that never reaches a final status, or one that finishes without pushing,
# settles nothing about which token would have served it.
test_a_run_that_does_not_finish_is_a_failure() {
  RUN591_MODE=runs-hang RUN591_RUN_SECONDS=3 run591 hang
  expect_status "$status" 1 "a run that never finishes"
  expect_contains "$out" "FACT run-a: FAIL" "the row failed"
  expect_contains "$out" "no row in a FINAL status" "the row says what was not read"
  # The service's lines are read whatever the run's own row said: which token
  # served what it DID do is a reading of its own, and the two rows are not
  # allowed to stand or fall together.
  expect_contains "$out" "FACT audit-a: PASS" "the audit row is written from the service's lines alone"

  RUN591_MODE=runs-nopush run591 nopush
  expect_status "$status" 1 "a run that did not push"
  expect_contains "$out" "did not push" "the row says the run never reached the proxy"

  RUN591_MODE=runs-nopr run591 nopr
  expect_status "$status" 1 "a run that pushed and opened no pull request"
  expect_contains "$out" "names no pull request" "the row says what is missing"

  RUN591_MODE=runs-empty run591 empty-listing
  expect_status "$status" 1 "a listing that holds no row for the run"
  expect_contains "$out" "no row in a FINAL status" "the row says the row was never there"
}

test_a_launch_that_names_no_run_id_is_a_failure() {
  RUN591_MODE=run-no-id run591 no-id
  expect_status "$status" 1 "a launch that named no run id"
  expect_contains "$out" "FACT run-a: FAIL" "the row failed"
  expect_contains "$out" "named NO run id" "the row says what was missing"
  expect_contains "$out" "FACT audit-a: UNVERIFIED" "the audit row says it cannot be read"
  expect_contains "$out" "has no run id on this side" "the audit row says why"

  RUN591_MODE=run-fails run591 launch-fails
  expect_status "$status" 1 "a launch that failed"
  expect_contains "$out" "FACT run-a: FAIL" "the row failed"
}

# The one 403 a check can force: the service's own typed refusal. Every other
# answer is UNVERIFIED or FAIL, and each names what to look at.
test_the_forced_403_is_the_services_own_answer() {
  RUN591_FRONTDOOR_MODE=iam run591 iam
  expect_status "$status" 3 "an IAM refusal at the front end"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "roles/run.invoker" "the row names the fix"

  RUN591_FRONTDOOR_MODE=open run591 open
  expect_status "$status" 1 "an answered call with no run identity"
  expect_contains "$out" "FACT refusal: FAIL" "the row failed"
  expect_contains "$out" "ANSWERED a call that carried no run identity" "the row says what happened"

  RUN591_FRONTDOOR_MODE=unauthorized run591 unauthorized
  expect_status "$status" 3 "a 401 from the front end"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"

  RUN591_FRONTDOOR_MODE=other-type run591 other-type
  expect_status "$status" 3 "the service's own document with another type"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "the type it wrote is 'not_covered'" "the row names the type it read"

  RUN591_FRONTDOOR_MODE=badrequest run591 badrequest
  expect_status "$status" 3 "a 400 from the proxy route"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "not the 403 this check measures" "the row says why"

  RUN591_GCLOUD_MODE=id-fails run591 id-fails
  expect_status "$status" 3 "an identity token that could not be minted"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "print-identity-token exited" "the row says what failed"

  RUN591_GCLOUD_MODE=service-fails run591 service-fails
  expect_status "$status" 3 "a service that could not be described"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "the row is unmeasured"
  expect_contains "$out" "could not be described in fugaro-dev/us-east5" "the row names what was read"

  RUN591_GCLOUD_MODE=service-no-url run591 service-no-url
  expect_status "$status" 3 "a service with no usable URL"
  expect_contains "$out" "no usable status.url" "the row says what was missing"

  RUN591_GCLOUD_MODE=logging-refused run591 logging-refused
  expect_status "$status" 3 "the service's lines that could not be read"
  expect_contains "$out" "FACT audit-a: UNVERIFIED" "the audit row is unmeasured"
}

# An endpoint that echoes back what it was given must not be able to reach the
# block through a field it chooses — on either face.
test_hostile_endpoints_print_nothing_they_were_given() {
  RUN591_API_MODE=hostile run591 hostile-api
  expect_status "$status" 0 "the hostile API"
  assert_no_secret "$work/hostile-api" "the hostile API"

  RUN591_FRONTDOOR_MODE=hostile run591 hostile-front
  expect_status "$status" 3 "the hostile front door"
  expect_contains "$out" "FACT refusal: UNVERIFIED" "a body it wrote is not the service's own set"
  assert_no_secret "$work/hostile-front" "the hostile front door"
}

test_belong_is_refused_everywhere() {
  local args
  for args in "--repo belong/x" "--other-repo belong/x" ; do
    RUN591_ARGS=($args)
    run591 belong
    expect_status "$status" 2 "belong in '$args'"
    expect_no_request "$work/belong" "belong in '$args'"
  done

  RUN591_ARGS=(--workspace belong --repo belong/x --other-repo belong/y)
  run591 belong-ws
  expect_status "$status" 2 "belong as the workspace"
  expect_contains "$err" "names 'belong'" "the refusal says which name it found"
  expect_no_request "$work/belong-ws" "belong as the workspace"

  RUN591_ARGS=(--project belong)
  run591 belong-project
  expect_status "$status" 2 "belong as the installation"
  expect_contains "$err" "names 'belong'" "the refusal says which name it found"

  RUN591_ENV=(CHECK591_GCP_PROJECT=belong)
  run591 belong-gcp
  expect_status "$status" 2 "belong as the GCP project"
  expect_contains "$err" "names 'belong'" "the refusal says which name it found"

  RUN591_ARGS=(--repo-token-name bitbucket-token--belong)
  run591 belong-name
  expect_status "$status" 2 "belong in a record name"
  expect_contains "$err" "names 'belong'" "the refusal says which name it found"
}

# The installation and its GCP project are read, and neither is taken from the
# environment's own default.
test_the_installation_and_the_gcp_project_are_held() {
  RUN591_MODE=doctor-wrong-name run591 wrong-name
  expect_status "$status" 2 "another installation"
  expect_contains "$err" "is the installation 'otherproject'" "the refusal names what it found"
  expect_no_request "$work/wrong-name" "another installation"

  RUN591_MODE=doctor-wrong-gcp run591 wrong-gcp
  expect_status "$status" 2 "another GCP project"
  expect_contains "$err" "lives in GCP project 'another-project'" "the refusal names what it found"

  RUN591_MODE=doctor-fails run591 doctor-fails
  expect_status "$status" 2 "a doctor that could not answer"
  expect_contains "$err" "fugaro doctor --project fugaro failed" "the refusal says what was run"

  # The real doctor exits 1 whenever a check is a warning AND still prints its
  # document: that is an answer, not a failure.
  RUN591_MODE=doctor-warns run591 doctor-warns
  expect_status "$status" 0 "a doctor that warns"
  expect_contains "$out" "FACT installation: PASS" "a warning is an answer"
}

test_dry_run_touches_nothing() {
  RUN591_ARGS=(--dry-run)
  run591 dry
  expect_status "$status" 0 "the dry run"
  expect_contains "$out" "This is the PLAN" "the plan is printed"
  expect_contains "$out" "Check 591 — which of the installation's Bitbucket tokens serves WHICH repository." "the plan names the check"
  expect_contains "$out" "dimi_bmb/sandbox-a" "the plan names the first repository"
  expect_contains "$out" "dimi_bmb/sandbox-b" "the plan names the second repository"
  expect_contains "$out" "bitbucket-token--ck590-repo" "the plan names the records"
  expect_absent "$out" "CHECK 591 RESULT" "a plan is not a result block"
  expect_no_request "$work/dry" "the dry run"
  [ ! -e "$work/dry/fugaro.log" ] || fail "the dry run: fugaro was called"
  [ ! -e "$work/dry/gcloud.log" ] || fail "the dry run: gcloud was called"
  [ ! -e "$work/dry/git.log" ] || fail "the dry run: git was called"
}

# The plan and the usage need no cloud tools at all: they are the one output the
# owner can have without an installation on the machine.
test_the_plan_and_the_usage_need_no_cloud_tools() {
  local status=0
  local sh
  # The interpreter is resolved HERE and named by its own path: the PATH below
  # holds no fugaro, gcloud or git, and it must not have to hold a bash either.
  sh=$(command -v bash)
  mkdir -p "$work/notools/tmp"
  env PATH=/usr/bin:/bin TMPDIR="$work/notools/tmp" "$sh" "$script" --dry-run \
    --repo "$REPO" --other-repo "$OTHER_REPO" \
    --token-file /nonexistent/repo --project-token-file /nonexistent/project \
    --project-key SANDBOX >"$work/notools/out" 2>"$work/notools/err" || status=$?
  [ "$status" -eq 0 ] || fail "the plan without the cloud tools: expected exit 0, got $status: $(cat "$work/notools/err")"
  expect_contains "$(cat "$work/notools/out")" "This is the PLAN" "the plan without the cloud tools"
  status=0
  env PATH=/usr/bin:/bin "$sh" "$script" --help >"$work/notools/help" 2>&1 || status=$?
  [ "$status" -eq 0 ] || fail "the usage without the cloud tools: expected exit 0, got $status"
  expect_contains "$(cat "$work/notools/help")" "usage: check591.sh" "the usage without the cloud tools"
}

test_endpoint_override_shapes_are_refused() {
  local value
  for value in "https://api.bitbucket.org/2.0" "http://127.0.0.1:8080@evil.example/2.0" \
    "http://evil.example:8080/2.0" "http://127.0.0.1:80abc/2.0" "http://127.0.0.1:8080/2.0?x=1"; do
    local status=0
    mkdir -p "$work/endpoint/tmp"
    CHECK591_API_URL="$value" TMPDIR="$work/endpoint/tmp" PATH="$work/endpoint/bin:$PATH" \
      bash "$script" --repo "$REPO" --other-repo "$OTHER_REPO" \
      --token-file /nonexistent/repo --project-token-file /nonexistent/project \
      --project-key SANDBOX >"$work/endpoint/out" 2>"$work/endpoint/err" || status=$?
    [ "$status" -eq 2 ] || fail "the endpoint override '$value': expected exit 2, got $status"
    expect_contains "$(cat "$work/endpoint/out" "$work/endpoint/err")" "CHECK591_API_URL may only name a loopback address" "the endpoint override '$value'"
  done
  for value in "http://127.0.0.1:8080/service@evil.example" "https://evil.example" "http://127.0.0.1:8080#x"; do
    local status=0
    mkdir -p "$work/endpoint/tmp"
    CHECK591_SERVICE_URL="$value" TMPDIR="$work/endpoint/tmp" PATH="$work/endpoint/bin:$PATH" \
      bash "$script" --repo "$REPO" --other-repo "$OTHER_REPO" \
      --token-file /nonexistent/repo --project-token-file /nonexistent/project \
      --project-key SANDBOX >"$work/endpoint/out" 2>"$work/endpoint/err" || status=$?
    [ "$status" -eq 2 ] || fail "the service override '$value': expected exit 2, got $status"
    expect_contains "$(cat "$work/endpoint/out" "$work/endpoint/err")" "CHECK591_SERVICE_URL may only name a loopback address" "the service override '$value'"
  done
  # A service URL the fake gcloud answers with that this check will not send a
  # run's path to reads as no URL at all.
  RUN591_ENV=(CHECK591_SERVICE_URL=)
  RUN591_GCLOUD_MODE=service-fails run591 no-service
  expect_status "$status" 3 "a service URL that is not usable"
}

# Each token file at mode 0600: a token any other user of the machine can read is
# not one to hold beside the installation's own.
test_token_file_complaints() {
  # The fixture files live OUTSIDE the run's own directory: run591 writes the two
  # files it is pointed at, so a fixture inside it would be rewritten to mode 0600
  # with the sentinel in it and the case would test nothing.
  local fixtures=$work/fixtures
  mkdir -p "$fixtures"
  printf '%s\n' "$TOKEN_SENTINEL" >"$fixtures/not-private"
  chmod 644 "$fixtures/not-private"
  RUN591_TOKEN_FILE="$fixtures/not-private" run591 badmode
  expect_status "$status" 2 "a token file at mode 0644"
  expect_contains "$err" "is mode 644" "the refusal names the mode"
  expect_no_request "$work/badmode" "a token file at mode 0644"

  : >"$fixtures/empty"
  chmod 600 "$fixtures/empty"
  RUN591_TOKEN_FILE="$fixtures/empty" run591 empty
  expect_status "$status" 2 "an empty token file"
  expect_contains "$err" "could not be read" "the refusal names what was read"
  expect_contains "$err" "the token file is empty" "the refusal says the file is empty"
  expect_no_request "$work/empty" "an empty token file"

  printf 'FAKE-FIRST-LINE-ONLY\nFAKE-SECOND-LINE-ONLY\n' >"$fixtures/twoline"
  chmod 600 "$fixtures/twoline"
  RUN591_TOKEN_FILE="$fixtures/twoline" run591 twoline
  expect_status "$status" 2 "a token file holding two lines"
  expect_contains "$err" "one line of printable text" "the refusal says what a token file holds"
  expect_no_request "$work/twoline" "a token file holding two lines"
  expect_absent "$(cat "$work/twoline/out" "$work/twoline/err")" "FAKE-FIRST-LINE-ONLY" "a token file whose line was refused"

  RUN591_TOKEN_FILE="$fixtures/missing" run591 missing
  expect_status "$status" 2 "a token file that is not there"
  expect_contains "$err" "could not be read" "the refusal names what was read"
  expect_no_request "$work/missing" "a token file that is not there"
}

test_argument_complaints() {
  RUN591_PROJECT_ARGS=(--project-uuid "$PROJECT_UUID" --project-key SANDBOX)
  run591 both-flags
  expect_status "$status" 2 "both project flags"
  expect_contains "$err" "takes ONE of them" "the refusal says why"

  RUN591_PROJECT_ARGS=(--project-uuid "$PROJECT_UUID")
  run591 uuid-only
  expect_status "$status" 0 "the project's UUID instead of its key"
  expect_contains "$out" "FACT bundle: PASS" "the UUID form is accepted"

  RUN591_ARGS=(--other-repo "$REPO")
  run591 same-repo
  expect_status "$status" 2 "one repository for both"
  expect_contains "$err" "the same repository" "the refusal says why"
  expect_no_request "$work/same-repo" "one repository for both"

  # The repository token file's own default for this case is "$work/shared-file/token",
  # so pointing the project token at that same path is one file for two tokens.
  RUN591_PROJECT_TOKEN_FILE="$work/shared-file/token" run591 shared-file
  expect_status "$status" 2 "one file for two tokens"
  expect_contains "$err" "the same path" "the refusal says why"
  expect_no_request "$work/shared-file" "one file for two tokens"

  RUN591_ARGS=(--project-token-name bitbucket-token--ck590-repo)
  run591 same-name
  expect_status "$status" 2 "one record name for both"
  expect_contains "$err" "the same name" "the refusal says why"

  RUN591_ARGS=(--repo "sandbox-a")
  run591 not-qualified
  expect_status "$status" 2 "a repository that is not workspace/name"
  expect_contains "$err" "is not workspace/name" "the refusal says what a repository looks like"

  RUN591_ARGS=(--other-workspace dimi_bmb)
  run591 unknown-flag
  expect_status "$status" 2 "an unknown flag"
  expect_contains "$err" "unknown argument" "the refusal says which argument"
}

# A tracing shell prints every command, and the commands here pass file names that
# hold a token.
test_xtrace_is_refused() {
  RUN591_SHELL='bash -x' run591 xtrace
  expect_status "$status" 2 "a tracing shell"
  expect_no_request "$work/xtrace" "a tracing shell"
  expect_contains "$out" "FACT xtrace: FAIL" "the block names the trace"
  expect_contains "$err" "is tracing" "the refusal says what to do"
}

# The cleanup has to work with HOME unset, which is the one environment a `set -u`
# script gets wrong.
test_cleanup_survives_an_unset_home() {
  RUN591_UNSET_HOME=yes run591 no-home
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
echo "the token is $TOKEN_SENTINEL and the identity is $IDENTITY_SENTINEL"
printf '%s\n' "$IDENTITY_SENTINEL" >"$0.leak"
ECHO_EOF
  chmod +x "$dir/echo.sh"
  # It is RUN first: an assertion only ever handed a clean run has not been read.
  # The values arrive in the environment, which is the one way a token can be in a
  # process without being in its argv.
  TOKEN_SENTINEL="$TOKEN_SENTINEL" IDENTITY_SENTINEL="$IDENTITY_SENTINEL" \
    "$dir/echo.sh" >"$dir/out" 2>"$dir/err" || true
  local before=$failures
  EXPECT_FAIL=yes assert_no_secret "$dir" "the echoing script"
  [ "$failures" -gt "$before" ] || fail "the secret assertions did not catch a script that prints the tokens and leaves one in a file"
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
t test_the_check_reads_and_launches_exactly_what_it_says
t test_the_check_runs_no_git_of_its_own
t test_the_service_lines_are_waited_for
t test_one_token_for_both_repositories_is_a_failure
t test_a_run_served_by_the_other_token_is_a_failure
t test_no_service_line_at_all_is_incomplete
t test_a_refusal_line_is_carried_in_the_row
t test_a_fallback_to_a_wider_token_is_read_as_such
t test_lines_naming_another_repository_are_said_out_loud
t test_the_records_have_to_make_the_selection_a_real_choice
t test_a_repository_token_that_reaches_both_is_read_as_the_weaker_premise
t test_a_token_that_does_not_reach_its_repository_is_a_failure
t test_a_run_that_does_not_finish_is_a_failure
t test_a_launch_that_names_no_run_id_is_a_failure
t test_the_forced_403_is_the_services_own_answer
t test_hostile_endpoints_print_nothing_they_were_given
t test_belong_is_refused_everywhere
t test_the_installation_and_the_gcp_project_are_held
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_cloud_tools
t test_endpoint_override_shapes_are_refused
t test_token_file_complaints
t test_argument_complaints
t test_xtrace_is_refused
t test_cleanup_survives_an_unset_home
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check591_test: $failures failure(s)" >&2
  exit 1
fi
echo "check591_test: all tests passed" >&2
