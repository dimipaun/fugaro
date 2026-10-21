#!/usr/bin/env bash
# Tests for scripts/live-checks/check91.sh, with fake `gcloud`, `fugaro` and
# `curl` on PATH: NOTHING here reaches a cloud, a project or the network, no
# agent runs a live command, and the fake tools record every argument they are
# given so the test can assert that no command ever named anything but the
# sandbox installation's project.
#
# Usage: bash scripts/live-checks/check91_test.sh   (also run by scripts/live_checks_test.go)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check91.sh
# The real `rm`, for the one test that puts a stand-in for it on PATH: everything that is
# not that test's scratch directory is still removed by the real one.
REAL_RM=$(command -v rm || true)
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK91_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check91.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
case "${1:-}" in
version) echo "fugaro 0.9.6 (test)" ;;
doctor)
  # Faithful to internal/cli/doctor.go's emitDoctor: the checks first, then the
  # project: line, then secrets: — all on stdout — and exit 1 (ExitUserError) as
  # soon as any check of its own failed, whatever the project: line said. A
  # stand-in that only ever printed the project line and exited 0 would not be
  # the command this guard depends on.
  case "${FAKE_FUGARO_MODE:-ok}" in
  ok) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  checkfail)
    echo "billing: FAIL (a check that has nothing to do with which installation this is)"
    echo "project: fugaro (GCP fugaro-dev, us-east5)"
    echo "secrets: none stored"
    exit 1
    ;;
  noisy)
    echo "project: fugaro (GCP fugaro-dev, us-east5)"
    echo "secrets: none stored"
    # Everything after the project: line, which is what a `grep -q` at the far
    # end of a pipe stops reading at: enough of it to fill a pipe buffer, so a
    # reader that stops at the match breaks the pipe rather than racing it.
    pad=$(printf 'x%.0s' $(seq 1 300))
    i=0
    while [ "$i" -lt 4000 ]; do
      printf 'check: %s\n' "$pad" || exit 141
      i=$((i + 1))
    done
    ;;
  *) exit 1 ;;
  esac
  ;;
*) exit 1 ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check91.sh's test only.

It keeps a little state in $FAKE_GCLOUD_STATE -- whether the service and the
repository exist -- so the guard, the baseline, the deploy and the Restore see
a project that changes the way a real one would. It never talks to anything.
"""
import os, sys, time

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]


def flag(name):
    if name in args:
        return args[args.index(name) + 1]
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return ""


def has(name):
    return name in args or any(a.startswith(name + "=") for a in args)


def state(name):
    return os.path.exists(os.path.join(STATE, name))


def touch(name):
    open(os.path.join(STATE, name), "w").close()


def rm(name):
    if os.environ.get("FAKE_GCLOUD_DELETE_FAILS") == "yes":
        return
    try:
        os.remove(os.path.join(STATE, name))
    except FileNotFoundError:
        pass


def listfails(kind):
    """Whether this run says the list QUERY itself fails, as a real one does when the
    session's credentials have expired or the API answers with a transient error. It is
    not the same answer as an empty list: an empty list is exit 0 and prints nothing,
    which is what a count of 0 is read from."""
    want = os.environ.get("FAKE_GCLOUD_LIST_FAILS", "")
    return want == "all" or kind in want.split(",")


LIST_FAILED = "ERROR: There was a problem refreshing your current auth tokens: the fake gcloud was told to fail this list\n"


# FAKE_GCLOUD_HANG_ON names one call that touches its state and THEN hangs, the
# way a real create/deploy does: the resource is there while the command is
# still running. It is what lets the test look at the ownership records on disk
# while a create is in flight -- the one moment that says whether a record is
# written BEFORE the thing it records.
HANG_ON = os.environ.get("FAKE_GCLOUD_HANG_ON", "")
HANG_SECONDS = 8

if mode == "hang":
    time.sleep(600)

if args[:2] == ["run", "services"] and args[2:3] == ["describe"]:
    if not state("service"):
        sys.exit(1)
    if has("--format"):
        fmt = flag("--format")
        if "status.url" in fmt:
            print("https://fugaro-chk91-fake.a.run.app")
        else:
            print("fugaro-chk91-image-digest")
    sys.exit(0)

if args[:2] == ["run", "services"] and args[2:3] == ["list"]:
    if listfails("services"):
        sys.stderr.write(LIST_FAILED)
        sys.exit(1)
    if state("service"):
        print("fugaro-chk91")
    sys.exit(0)

if args[:2] == ["run", "services"] and args[2:3] == ["delete"]:
    # FAKE_GCLOUD_MARK names a file this call touches as it starts, and
    # FAKE_GCLOUD_DELETE_SLEEP makes it take its time over the delete: both are
    # how the test finds the run in the middle of its CLEANUP and sends the
    # second signal there. The state is removed after the sleep, so a test that
    # interrupts the deletion sees a delete that really did happen.
    mark = os.environ.get("FAKE_GCLOUD_MARK")
    if mark:
        # A LINE, not an empty file: the test waits for this file to be
        # non-empty, so a zero-byte touch would never be seen and the wait would
        # run to its full budget instead of breaking the moment the delete
        # starts.
        with open(mark, "a") as fh:
            fh.write("delete\n")
    if os.environ.get("FAKE_GCLOUD_DELETE_SLEEP"):
        time.sleep(float(os.environ["FAKE_GCLOUD_DELETE_SLEEP"]))
    rm("service")
    sys.exit(0)

if args[:2] == ["run", "services"] and args[2:3] == ["update"]:
    touch("service")
    sys.exit(0)

if args[:2] == ["run", "deploy"]:
    if os.environ.get("FAKE_GCLOUD_DEPLOY_FAILS") == "yes":
        sys.stderr.write("ERROR: (gcloud.run.deploy) the fake gcloud was told to fail this deploy\n")
        sys.exit(1)
    touch("service")
    print("Deploying container to Cloud Run service [fugaro-chk91]... done.")
    if HANG_ON == "deploy":
        time.sleep(HANG_SECONDS)
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["describe"]:
    sys.exit(0 if (state("repo") or os.environ.get("FAKE_GCLOUD_REPO_EXISTS") == "yes") else 1)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["create"]:
    if os.environ.get("FAKE_GCLOUD_CREATE_FAILS") == "yes":
        sys.stderr.write("ERROR: (gcloud.artifacts.repositories.create) the fake gcloud was told to fail this create\n")
        sys.exit(1)
    touch("repo")
    if HANG_ON == "repo-create":
        time.sleep(HANG_SECONDS)
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["delete"]:
    rm("repo")
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["list"]:
    if listfails("repositories"):
        sys.stderr.write(LIST_FAILED)
        sys.exit(1)
    if state("repo"):
        print("projects/fugaro-dev/locations/us-east5/repositories/fugaro-chk91")
    sys.exit(0)

if args[:2] == ["artifacts", "docker"] and args[2:3] == ["images"]:
    if args[3:4] == ["delete"]:
        rm("image")
        sys.exit(0)
    if args[3:4] == ["list"]:
        if listfails("images"):
            sys.stderr.write(LIST_FAILED)
            sys.exit(1)
        # A list against a repository that is not there is NOT_FOUND for the real CLI: it
        # exits non-zero with the error on stderr and prints nothing. The Restore's image
        # count is the one query that meets this -- the repository is deleted just before
        # it -- so this is the fake's way of saying the run has to survive it.
        if not state("repo") and os.environ.get("FAKE_GCLOUD_REPO_EXISTS") != "yes":
            sys.stderr.write("ERROR: (gcloud.artifacts.docker.images.list) NOT_FOUND: Repository not found\n")
            sys.exit(1)
        if state("image"):
            print("us-east5-docker.pkg.dev/fugaro-dev/fugaro-chk91/fugaro-chk91:chk91")
    sys.exit(0)

if args[:1] == ["builds"]:
    touch("image")
    # The build uploads the source as a tar into the staging bucket before it
    # runs, so an object is there from that moment on: that is what the
    # Restore's staged-source removal has to clean up.
    touch("staged")
    print("BUILD SUCCESS")
    sys.exit(0)

if args[:1] == ["storage"]:
    op = args[1] if len(args) > 1 else ""
    target = args[-1]
    if op == "ls" and target.rstrip("/") == "gs://fugaro-dev_cloudbuild":
        # The installation's own staging bucket, which this check never creates:
        # the probe decides whether the build can be given a prefix of its own.
        if os.environ.get("FAKE_GCLOUD_NO_STAGING_BUCKET") == "yes":
            sys.stderr.write("ERROR: One or more URLs matched no objects.\n")
            sys.exit(1)
        print("gs://fugaro-dev_cloudbuild/source/")
        sys.exit(0)
    if op == "ls":
        if state("staged"):
            print(target + "chk91-source.tar.gz")
            sys.exit(0)
        # What the real CLI does for a prefix with nothing under it: a non-zero
        # exit and this line on stderr. The script reads the MESSAGE, not the
        # status, to tell "gone" from "could not be read".
        sys.stderr.write("ERROR: One or more URLs matched no objects.\n")
        sys.exit(1)
    if op == "rm":
        rm("staged")
        sys.exit(0)
    sys.exit(1)

if args[:2] == ["auth", "print-identity-token"]:
    print("FAKE-IDENTITY-TOKEN-FOR-TESTS-NOT-A-CREDENTIAL")
    sys.exit(0)

sys.exit(1)
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_rm() { # dir
  # A stand-in for `rm` that refuses to remove a DIRECTORY, the way a real `rm -rf` fails
  # on a scratch directory it cannot remove (a read-only parent, a file its owner cannot
  # unlink). Only the scratch directory is a directory here, so every other removal the run
  # makes still happens.
  mkdir -p "$1/bin"
  cat >"$1/bin/rm" <<RM_EOF
#!/usr/bin/env bash
# A stand-in for \`rm\`, for scripts/live-checks/check91_test.sh only: it never removes a
# directory. Everything else is the real \`rm\`.
set -euo pipefail
for a in "\$@"; do
  if [ -d "\$a" ]; then
    echo "rm: cannot remove '\$a': Operation not permitted" >&2
    exit 1
  fi
done
exec "$REAL_RM" "\$@"
RM_EOF
  chmod +x "$1/bin/rm"
}

# A stand-in for `rm` that refuses to remove the credential FILE the run writes
# its minted identity token into, and removes everything else. The run's own
# `rm -f "$AUTH_CONF" || true` records nothing by itself, so this is the case
# where a live bearer token is left behind — inside the scratch directory, which
# a Restore count that is not 0 keeps on purpose.
write_fake_rm_conf() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/rm" <<RM_EOF
#!/usr/bin/env bash
# A stand-in for \`rm\`, for scripts/live-checks/check91_test.sh only: it refuses
# the 0600 curl config holding the minted identity token. Everything else is the
# real \`rm\`.
set -euo pipefail
for a in "\$@"; do
  case "\$a" in
  */idt.conf)
    echo "rm: cannot remove '\$a': Operation not permitted" >&2
    exit 1
    ;;
  esac
done
exec "$REAL_RM" "\$@"
RM_EOF
  chmod +x "$1/bin/rm"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
"""A stand-in for `curl` (and for the Cloud Run front end), for
scripts/live-checks/check91.sh's test only: no socket is opened."""
import os, sys, time

args = sys.argv[1:]
mode = os.environ.get("FAKE_CURL_MODE", "ok")
STATE = os.environ["FAKE_CURL_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]

with open(os.path.join(LOG, "curl.calls"), "a") as fh:
    fh.write("curl " + " ".join(args) + "\n")

if "-V" in args:
    print("curl 8.5.0 (x86_64) libcurl/8.5.0" + ("" if mode == "no-http2" else " HTTP2"))
    sys.exit(0)


def flag(name, default=""):
    if name in args:
        return args[args.index(name) + 1]
    for a in args:
        if a.startswith(name):
            return a[len(name):]
    return default


def has(name):
    return name in args


out = flag("-o")
fmt = flag("-w")
headers = [args[i + 1] for i, a in enumerate(args) if a == "-H"]
# The credential arrives in an 0600 curl config file, named with --config: a
# token on a command line is visible in `ps` to every user on the machine for
# as long as the process lives, and the hour-long stream would hold it there
# for an hour. The mode is written into the log so the test can assert the file
# really is 0600 as well as out of argv.
conf = flag("--config")
conf_text = ""
if conf:
    try:
        conf_text = open(conf).read()
        with open(os.path.join(LOG, "curl.calls"), "a") as fh:
            fh.write("curl config %s mode=%04o\n" % (conf, os.stat(conf).st_mode & 0o777))
    except OSError as exc:
        with open(os.path.join(LOG, "curl.calls"), "a") as fh:
            fh.write("curl config %s unreadable: %s\n" % (conf, exc))
authed = any(h.lower().startswith("authorization:") for h in headers) \
    or "authorization:" in conf_text.lower()
url = [a for a in args if not a.startswith("-")][-1]
method = flag("-X", "GET")
data = flag("--data-binary", "")
path = url.split("chk91-fake.a.run.app", 1)[-1] or "/"

status, version, uploaded, ttfb, total, body, header_lines = 0, "1.1", 0, 0.11, 0.22, "", []


def emit():
    if "-D" in args and flag("-D") == "-":
        sys.stdout.write("".join(l + "\r\n" for l in header_lines))
    if out and out != "/dev/null":
        with open(out, "w") as fh:
            fh.write(body)
    if fmt:
        text = (fmt.replace("%{http_code}", str(status))
                   .replace("%{size_upload}", str(uploaded))
                   .replace("%{http_version}", version)
                   .replace("%{time_starttransfer}", "%.6f" % ttfb)
                   .replace("%{time_total}", "%.6f" % total))
        sys.stdout.write(text)
    elif not fmt:
        sys.stdout.write(body)


if "-D" in args and flag("-D") == "-":
    if not authed:
        status, header_lines, body = 403, ["HTTP/1.1 403 Forbidden"], ""
        emit()
        sys.exit(0)
    header_lines = ["HTTP/1.1 200 OK", "Content-Type: text/event-stream", "X-Relay: chk91"]
    emit()
    # `timeout` mode: the headers probe is cut by its own --max-time exactly as
    # the body probe is, and the headers it printed on the way are still read.
    sys.exit(28 if mode == "timeout" and "--max-time" in args else 0)

if not authed:
    status, body = 403, ""
    emit()
    sys.exit(0)

if method == "POST" and path.startswith("/echo"):
    n = 0
    if data.startswith("@"):
        n = os.path.getsize(data[1:])
    status, uploaded, version = 200, n, ("1.1" if "--http1.1" in args else "2")
    body = "read %d bytes\n" % n
    # A front end that refuses the HTTP/2 request outright while the HTTP/1.1
    # one is accepted: the two halves of one table, so a row that answered 403
    # must not be printed as a PASS that says "nothing accepted" in it.
    if mode == "h2-refused" and "--http2" in args:
        status, body = 403, "forbidden\n"
        emit()
        sys.exit(0)
    # `timeout` mode: the size above the first one is refused the way a front
    # end refuses it -- a status with a curl that also exits non-zero, which is
    # the row this step exists to record.
    if mode == "timeout" and n >= 2097152:
        status, body = 413, "Request Entity Too Large\n"
        emit()
        sys.exit(56)
    emit()
    sys.exit(0)

if path.startswith("/sse"):
    # A front end that answers the long stream with an error page, at once, and
    # with curl EXITING 0: without --fail an HTTP status is not a failure to
    # curl, so the row this step records has to read the status to tell a
    # completed stream from an error page. The ` after ` in the -w format is the
    # long stream's own, so the headers probe and the unbuffered probe are
    # answered normally.
    if mode == "long-error-page" and fmt and " after " in fmt:
        status, version, body = 403, "1.1", "forbidden\n"
        emit()
        sys.exit(0)
    # A real SSE endpoint writes each event as it happens: the fake does too,
    # one a second, and all at once when the test asks for a burst.
    count = 3
    status, version = 200, "1.1"
    streamed = []
    for i in range(count):
        line = "data: 0.%d\n" % i
        streamed.append(line)
        if out and out != "/dev/null":
            with open(out, "a") as fh:
                fh.write(line)
        elif not out:
            # No -o at all: the body goes to stdout, which is what the
            # unbuffered probe reads. With -o /dev/null the body is DISCARDED,
            # as real curl discards it -- writing it to stdout instead would put
            # the events in front of the -w text the long stream is read from.
            sys.stdout.write(line)
        sys.stdout.flush()
        if mode != "burst":
            time.sleep(1)
    body = "".join(streamed)
    if fmt:
        emit()
    # `timeout` mode ends the way a real curl does: the probe is cut by this
    # check's own --max-time (28), the long stream is cut by the FRONT END (18)
    # however long the cap above it is. The two are told apart by the -w format
    # only the long stream carries.
    if mode == "timeout":
        sys.exit(18 if fmt else 28)
    sys.exit(0)

# GET / -- liveness, and the started= value that makes a cold start checkable.
if mode == "refuse-pre-liveness" and not fmt:
    # The liveness request BEFORE the idle wait: it carries no -w format, and it
    # is the call whose failure is a connection-level one (DNS, refused, its own
    # --max-time) -- a non-zero curl with no output at all. Under `set -e` with
    # no ERR trap that used to end the whole run with curl's exit code and no
    # `cold-start` fact at all.
    sys.stderr.write("curl: (7) Failed to connect\n")
    sys.exit(7)
if mode == "refuse-liveness" and fmt:
    # The request that fails AFTER the idle wait: only the two probes that carry
    # a -w format do, and a front end can answer one with a non-zero curl (a 504,
    # a reset connection) while / answers the earlier calls normally.
    sys.exit(7)
counter = os.path.join(STATE, "instance-calls")
n = 0
try:
    n = int(open(counter).read())
except Exception:
    pass
n += 1
open(counter, "w").write(str(n))
same = mode == "warm" or n == 1
if mode == "error-page" and n > 1:
    # The cold probe answered with an error page, and curl EXITS 0: without
    # --fail an HTTP error is not a failure to curl, so cold.out holds the error
    # page's body and no started= line at all. The first probe (n == 1) is
    # answered normally, so the run gets past its own "is this my image" check
    # and the error lands where it did in the wild: on the measurement.
    status, body = 503, "<html><head><title>503 Service Unavailable</title></head><body>the front end could not reach an instance</body></html>\n"
    emit()
    sys.exit(0)
status, body = 200, "started=2026-10-20T01:00:00Z\n" if same else "started=2026-10-20T02:00:00Z\n"
emit()
sys.exit(0)
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
run91() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    CHECK91_IDLE_SECONDS=0 CHECK91_PROBE_SECONDS=3 \
    CHECK91_BODY_SIZES="1048576 2097152" \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# start91: the same run as run91, but in the BACKGROUND, for the two tests that
# have to look at a run while it is in the middle of something (the ownership
# record and the interrupt). start_pid is the script's own pid.
start91() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    CHECK91_IDLE_SECONDS=0 CHECK91_PROBE_SECONDS=3 \
    CHECK91_BODY_SIZES="1048576 2097152" \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" &
  start_pid=$!
}

# wait_for FILE PATTERN TRIES: up to TRIES fifths of a second for PATTERN to
# appear in FILE.
wait_for() {
  local tries=0
  while [ "$tries" -lt "$3" ]; do
    grep -q "$2" "$1" 2>/dev/null && return 0
    sleep 0.2
    tries=$((tries + 1))
  done
  return 1
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
}

# --------------------------------------------------------------- the tests
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run91 "$dir" CHECK91_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 91 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded project"
  expect_contains "$out" "FACT baseline: PASS" "dry run: the baseline"
  expect_contains "$out" "FACT relay: PASS" "dry run: the relay files"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  expect_contains "$out" "CHECK 91 RESULT END (exit 0)" "dry run: the end line"

  # Nothing was created, built, deployed or deleted: only describes and the
  # version were called.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "repositories create" "dry run: no repository created"
  expect_absent "$calls" "builds submit" "dry run: no build"
  expect_absent "$calls" "run deploy" "dry run: no deploy"
  expect_absent "$calls" "delete" "dry run: nothing deleted"
  # Cleanup.
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run91 "$dir" FAKE_FUGARO_MODE=other CHECK91_DRY_RUN=1
  expect_status "$status" 1 "project guard"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "create" "project guard: nothing created"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# A knob the header advertises and nothing reads is worse than no knob at all:
# a reader — or a test — sets it, gets no effect, and gets no error either. The
# SSE probe's duration is CHECK91_PROBE_SECONDS and the long stream's is the
# relay's SSE_MINUTES, and the header once named a CHECK91_SSE_SECONDS that
# neither the script nor this test ever mentions.
test_every_advertised_knob_is_read() {
  local name reads
  local knobs
  knobs=$(sed -n '1,60p' "$script" | grep -o 'CHECK91_[A-Z_]*' | sort -u)
  if [ -z "$knobs" ]; then
    fail "the header advertises no CHECK91_ knob at all, so this test reads nothing"
    return
  fi
  for name in $knobs; do
    # The READ, not a mention: every knob the header names appears in that
    # comment three or four times over, so counting plain mentions could never
    # fire for a knob nothing reads — the assertion was vacuous by
    # construction. `${NAME:-` is an expansion, and an expansion cannot be a
    # comment, so a knob with no expansion is a knob with no effect.
    reads=$(grep -cF "\${$name:-" "$script" || true)
    if [ "$reads" -lt 1 ]; then
      fail "$name is advertised in the header and read nowhere in the script (no \${$name:-...} expansion in it)"
    fi
  done
}

# The guard asks one question — does doctor's project: line name fugaro-dev —
# and doctor's exit status is not the answer to it. doctor exits 1 whenever any
# check of its own failed (emitDoctor returns ExitUserError, doctor.go), and
# those checks (billing, IAM, the update check) are unrelated to which
# installation this is, so the guard must read the line doctor printed rather
# than the code it exited with.
test_doctor_failing_its_own_checks_is_not_a_stop() {
  local dir=$work/docfail
  prepare "$dir"
  run91 "$dir" FAKE_FUGARO_MODE=checkfail CHECK91_DRY_RUN=1
  expect_absent "$err" "not the fugaro-dev installation" "doctor failed a check of its own: no false stop on the installation guard"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "doctor failed a check of its own: the run went past the guard"
  expect_status "$status" 0 "doctor failed a check of its own: the dry run"
}

# doctor prints its secrets: line right after the project: line, so a guard that
# pipes doctor into `grep -q` closes the pipe on the match: doctor's next write
# fails, it dies on the broken pipe (141), and `set -o pipefail` makes the
# pipeline 141 — a matched line read as a failed check. doctor's output is
# captured here, so nothing closes under it.
test_doctor_output_after_the_project_line_is_not_a_stop() {
  local dir=$work/docpipe
  prepare "$dir"
  run91 "$dir" FAKE_FUGARO_MODE=noisy CHECK91_DRY_RUN=1
  expect_absent "$err" "not the fugaro-dev installation" "doctor still printing after the project line: no false stop"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "doctor still printing after the project line: the run went past the guard"
  expect_status "$status" 0 "doctor still printing after the project line: the dry run"
}

test_service_taken() {
  local dir=$work/svc
  prepare "$dir"
  mkdir -p "$dir/state" && : >"$dir/state/service"
  run91 "$dir"
  expect_status "$status" 1 "service taken"
  expect_contains "$err" "already exists in fugaro-dev/us-east5, and it is not this check's" "service taken: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "service taken: nothing deployed"
  # Every stop() records WHY in the block, not only on stderr: the block is what
  # the owner pastes back, and a guard stop that left every row reading "FAIL —
  # not reached" with no reason in it is the one thing the block cannot say.
  expect_contains "$out" "FACT aborted: FAIL — fugaro-chk91 already exists" "service taken: the block names what stopped the run"
}

test_repo_taken() {
  local dir=$work/repo
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_REPO_EXISTS=yes
  expect_status "$status" 1 "repository taken"
  expect_contains "$err" "never adopts, writes into or deletes a repository it did not create" "repository taken: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "repositories create" "repository taken: nothing created"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "repository taken: nothing pushed"
  # The other half of "never adopts, writes into or deletes": a repository that
  # was already there is not this check's, so the Restore must not name it in a
  # delete either. Asserting only that nothing was created would stay green for
  # a Restore that deletes a repository the run never made.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "delete" "repository taken: nothing deleted — the repository is not this check's to remove"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run91 "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT service-config: PASS" "happy path: the service configuration"
  expect_contains "$out" "FACT unauthenticated-request: PASS" "happy path: the unauthenticated refusal"
  expect_contains "$out" "FACT cold-start: PASS" "happy path: a cold start (a different instance answered)"
  expect_contains "$out" "FACT stream-unbuffered: PASS" "happy path: the stream is unbuffered"
  expect_contains "$out" "FACT stream-longest: PASS" "happy path: the long stream"
  expect_contains "$out" "FACT body-limit-http1: PASS" "happy path: the HTTP/1.1 body row"
  expect_contains "$out" "FACT body-limit-http2-front-end: PASS" "happy path: the HTTP/2-to-front-end row"
  expect_contains "$out" "FACT http2-end-to-end: INFO — NOT SETTLED" "happy path: U5's open half is stated"
  expect_contains "$out" "FACT restore: PASS" "happy path: the Restore"
  expect_contains "$out" "FACT restore-counts: PASS — services: 0, images: 0, repository: 0" "happy path: the three counts"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch directory went"
  # The credential file holds a minted identity token and lives inside the
  # scratch directory, so its removal is CHECKED rather than assumed from
  # `rm -f ... || true`: on the path where the directory is kept (a Restore
  # count that is not 0) a removal that failed would leave a live token behind.
  expect_contains "$out" "FACT restore-token-file: PASS — the credential file is gone" "happy path: the credential file went"
  expect_contains "$out" "CHECK 91 RESULT END (exit 0)" "happy path: the end line"
  # The deploy is the register's, flag for flag.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "--no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1" "happy path: the deploy flags"
  expect_contains "$calls" "--set-env-vars SSE_MINUTES=59" "happy path: the long stream raises SSE_MINUTES"
  expect_contains "$calls" "run services delete fugaro-chk91" "happy path: the service is deleted"
  expect_contains "$calls" "repositories delete fugaro-chk91" "happy path: the repository is deleted"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

test_curl_cutting_the_stream() {
  # curl exits 28 when its own --max-time cuts an SSE probe, 18 when the front
  # end cuts the long stream, and 56 on a refused body. All three are what this
  # check exists to MEASURE: unguarded, `set -e` would end the run at the first
  # probe and the last three of U5's numbers would be lost after the 35-minute
  # cold-start wait.
  local dir=$work/truncating
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=timeout
  expect_status "$status" 0 "curl cut the stream: the run completes"
  for fact in stream-unbuffered stream-longest body-limit-http1 body-limit-http2-front-end; do
    expect_absent "$out" "FACT $fact: FAIL — not reached" "curl cut the stream: $fact was reached"
  done
  expect_contains "$out" "FACT stream-unbuffered: PASS" "curl cut the stream: the arrival times are still read"
  # The headers probe is cut by its own --max-time too, and what it printed
  # before that is still what the fact reports.
  expect_contains "$out" "x-relay: chk91" "curl cut the stream: the headers the cut probe printed are read"
  expect_contains "$out" "curl exit 18" "curl cut the stream: the long stream records curl's own exit"
  expect_contains "$out" "curl exit 56" "curl cut the stream: a refused body row records curl's exit"
  expect_contains "$out" "413 2097152 over HTTP/1.1 (curl exit 56)" "curl cut the stream: the refusal is in the row"
  expect_contains "$out" "FACT stream-longest: PASS" "curl cut the stream: the long stream is recorded"
  # Only the refused size is missing from the accepted column: the smaller one
  # was accepted, so the fact still reads PASS with a limit to report.
  expect_contains "$out" "FACT body-limit-http1: PASS — largest accepted: 1048576" "curl cut the stream: the smaller size is the largest accepted"
  expect_contains "$out" "END (exit 0)" "curl cut the stream: the block ends at 0"
}

test_warm_cold_start() {
  local dir=$work/warm
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=warm
  expect_status "$status" 1 "warm cold start"
  expect_contains "$out" "FACT cold-start: FAIL" "warm cold start: the fact"
  expect_contains "$out" "NOT a cold start" "warm cold start: the words"
  # It is still restored: a FAILed fact must not leave the service running.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete fugaro-chk91" "warm cold start: still restored"
}

# A cold probe that produced no measurement must not be recorded as one. curl
# without --fail exits 0 on an HTTP error, so an error page from the front end
# leaves cold.out with no started= line in it; the step compared that empty
# `after` with `before`, found them different, and wrote "a DIFFERENT instance
# answered" -- a failed measurement recorded in U5 as the cold start this check
# exists to produce. It is a FAIL with the body quoted back instead.
test_error_page_is_not_a_cold_start() {
  local dir=$work/errpage
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=error-page
  expect_status "$status" 1 "error page: the run"
  expect_contains "$out" "FACT cold-start: FAIL — the cold request came back with no started= line" \
    "error page: the fact says nothing was measured"
  expect_contains "$out" "the front end could not reach an instance" \
    "error page: the body is quoted back rather than inferred"
  expect_absent "$out" "cold-start: PASS" "error page: a failed measurement is not a cold start"
  # The steps after the wait still run, and the service is still restored: the
  # measurement is missing, not the run.
  expect_contains "$out" "FACT stream-unbuffered: PASS" "error page: the stream still ran"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete fugaro-chk91" \
    "error page: still restored"
}

test_burst_stream() {
  local dir=$work/burst
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=burst
  expect_status "$status" 1 "burst stream"
  expect_contains "$out" "FACT stream-unbuffered: FAIL" "burst stream: the fact"
  expect_contains "$out" "a burst, not a stream" "burst stream: the words"
}

# A request that fails after the idle wait is a FACT, not the end of the run:
# the two cold-start probes are guarded, so the block still comes back with the
# curl exits in it -- and with the steps after the wait still to run.
test_liveness_probe_failure_is_recorded() {
  local dir=$work/refuse
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=refuse-liveness
  expect_status "$status" 1 "liveness refusal"
  expect_contains "$out" "FACT cold-start: FAIL — a request failed (cold curl exit 7" \
    "liveness refusal: the fact names the curl exits"
  expect_absent "$out" "cold-start: FAIL — not reached" "liveness refusal: the fact was written"
  expect_contains "$out" "END (exit 1)" "liveness refusal: the block still comes back"
}

# The HTTP/2 capability is settled FIRST, before anything is created or spent:
# the body table's second row is meaningless without it, and finding that out
# after the 35-minute cold-start wait and the hour-long stream would waste both.
test_no_http2_curl() {
  local dir=$work/nohttp2
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=no-http2
  expect_status "$status" 1 "no HTTP/2 curl"
  expect_contains "$err" "this curl has no HTTP/2 support" "no HTTP/2 curl: the stop"
  expect_contains "$out" "FACT curl-http2: FAIL" "no HTTP/2 curl: the fact"
  # ... and it is decided before anything was made: no repository, no build, no
  # deploy, so there is nothing for the Restore to delete and nothing spent.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "repositories create" "no HTTP/2 curl: no repository created"
  expect_absent "$calls" "builds submit" "no HTTP/2 curl: nothing built"
  expect_absent "$calls" "run deploy" "no HTTP/2 curl: nothing deployed"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" \
    "no HTTP/2 curl: there was nothing to delete"
  if [ -d "$scratch" ]; then fail "no HTTP/2 curl: the scratch directory survived"; fi
}

# `bash -x scripts/live-checks/check91.sh` is a natural thing to reach for on a
# two-hour run, and it is the one way this check's own commands can leak the
# identity token: `tok=$(gcloud auth print-identity-token)` and the printf that
# writes the token into the 0600 curl config file are both traced in full, so
# the trace carries the bearer token twice into stderr -- which is exactly the
# log an owner pastes back. The check turns the trace off at its first line and
# REFUSES a shell that arrived with it on (the flag is in SHELLOPTS/BASHOPTS
# then, and every child this script starts would trace on its own), before
# anything is created or spent.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  write_fake_curl "$dir"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  local st=0
  # The fast knobs are passed even though the guard should stop this run long
  # before it reaches the wait: a test whose only defence against a REGRESSION
  # is a guard that no longer fires must not then sit in the 35-minute idle wait
  # and the hour-long stream. With the guard gone this run goes the whole way
  # and the assertions below fail it, which is what a mutation should produce.
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    CHECK91_IDLE_SECONDS=0 CHECK91_PROBE_SECONDS=3 CHECK91_LONG_SECONDS=3 \
    CHECK91_BODY_SIZES="1048576 2097152" \
    bash -x "$script" >"$dir/out" 2>"$dir/err" || st=$?
  expect_status "$st" 1 "xtrace"
  expect_contains "$(cat "$dir/err")" "+ " "xtrace: the trace really was on"
  expect_contains "$(cat "$dir/err")" "tracing" "xtrace: the message names it"
  expect_contains "$(cat "$dir/out")" "FACT xtrace: FAIL" "xtrace: the fact"
  expect_absent "$(cat "$dir/out")" "CHECK 91 RESULT END (exit 0)" "xtrace: the run did not continue"
  expect_absent "$(cat "$dir/err")" "FAKE-IDENTITY-TOKEN" "xtrace: the trace carries no token"
  # Nothing was created, built or deployed: the guard is decided before the
  # first gcloud call that spends anything.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "repositories create" "xtrace: no repository created"
  expect_absent "$calls" "builds submit" "xtrace: nothing built"
  expect_absent "$calls" "run deploy" "xtrace: nothing deployed"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "$sc" ] && [ -d "$sc" ]; then fail "xtrace: the scratch directory survived the stop"; fi
}

# The staged source is this check's litter in the installation's bucket, and the
# Restore removes it: the build uploads the scratch directory as a tar before it
# runs, and a check that left that tar behind would be leaving something of its
# own in a bucket it does not own.
test_staged_source_is_removed() {
  local dir=$work/staged
  prepare "$dir"
  run91 "$dir"
  expect_status "$status" 0 "staged source: the run"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "builds submit --project=fugaro-dev --tag=us-east5-docker.pkg.dev/fugaro-dev/fugaro-chk91/fugaro-chk91:chk91 --gcs-source-staging-dir=gs://fugaro-dev_cloudbuild/chk91-source/" \
    "staged source: the build is given a per-run staging prefix"
  expect_contains "$calls" "storage rm -r gs://fugaro-dev_cloudbuild/chk91-source/" \
    "staged source: the prefix is removed by the Restore"
  expect_contains "$out" "FACT restore-staged-source: PASS" "staged source: the fact"
  if [ -f "$dir/state/staged" ]; then fail "staged source: the fake bucket still holds this run's source"; fi
}

# The installation's staging bucket may not be readable here (it is not this
# check's, and it is not created by it). Then the run STOPS before the build:
# `gcloud builds submit` would CREATE that bucket to stage the source, and a
# bucket is the installation's — Terraform-managed, with no ownership record
# this check could write and no Restore that could delete it. The fallback to
# gcloud's default staging directory is gone for the same reason it was always
# wrong: the tarball then cannot be named afterwards either.
test_unreadable_staging_bucket_stops_before_the_build() {
  local dir=$work/nobucket
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_NO_STAGING_BUCKET=yes
  expect_status "$status" 1 "unreadable staging bucket: the run stops"
  expect_contains "$out" "FACT build-staging: FAIL" "unreadable staging bucket: the fact says the build did not start"
  expect_contains "$err" "would CREATE it to stage this run's source" "unreadable staging bucket: the stop says what gcloud would do"
  expect_contains "$err" "Nothing was created, built or deployed by this run" "unreadable staging bucket: the stop says nothing was spent"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "builds submit" "unreadable staging bucket: nothing is built"
  expect_absent "$calls" "run deploy" "unreadable staging bucket: nothing is deployed"
  expect_absent "$calls" "storage rm" "unreadable staging bucket: nothing is deleted from a bucket this run did not write to"
  expect_absent "$calls" "storage buckets create" "unreadable staging bucket: this check never creates the installation's bucket"
  # The repository it created before the stop is still deleted: a stop is not a
  # reason to leave something of this check's in the project.
  expect_contains "$calls" "repositories delete fugaro-chk91" "unreadable staging bucket: the repository this run made is deleted"
  expect_contains "$out" "FACT restore: PASS" "unreadable staging bucket: the Restore ran"
  if [ -d "$scratch" ]; then fail "unreadable staging bucket: the scratch directory survived"; fi
}

# Every request carries a cap of its own. A curl with none is waited on for as
# long as the front end keeps the socket open, so a request can outlive the run
# it belongs to and a Ctrl-C has nothing to end. The fake curl logs the argv it
# was handed, so this reads the whole run's requests.
test_every_curl_carries_a_cap() {
  local dir=$work/curlcaps
  prepare "$dir"
  run91 "$dir"
  expect_status "$status" 0 "curl caps: the run"
  local line n=0
  while IFS= read -r line; do
    case "$line" in
    "curl config "*) continue ;;
    "curl -V"*) continue ;; # the capability probe: no request, nothing to cap
    curl\ *) ;;
    *) continue ;;
    esac
    n=$((n + 1))
    expect_contains "$line" "--max-time " "curl caps: this request has no cap of its own"
  done <"$dir/logs/curl.calls"
  [ "$n" -ge 6 ] || fail "curl caps: only $n request(s) were logged, the run changed shape"
}

# A second Ctrl-C DURING the cleanup must not kill the shell that is running it.
# The first one is already accounted for; what cleanup is doing is deleting the
# service, the image and the repository this run created in fugaro-dev, and a
# shell killed halfway through that leaves a billed service (or an image, or a
# repository) behind with the RESULT block that would have named it never
# printed. The fake gcloud says when the delete starts and takes its time over
# it, so the signal lands inside the cleanup rather than before it.
test_second_interrupt_during_cleanup_does_not_kill_it() {
  local dir=$work/secondint
  prepare "$dir"
  # The fake curl logs its argv into $dir/logs and the fake gcloud keeps its
  # state in $dir/state: without them the fakes fail on their first call and the
  # run stops long before its cleanup (run91 makes these; this test does not
  # go through run91).
  mkdir -p "$dir/logs" "$dir/state"
  rm -f "$dir/delete.mark"
  local st=0
  set -m # a background job of a non-interactive shell starts with SIGINT ignored
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    FAKE_GCLOUD_MARK="$dir/delete.mark" FAKE_GCLOUD_DELETE_SLEEP=4 \
    CHECK91_IDLE_SECONDS=0 CHECK91_PROBE_SECONDS=3 \
    CHECK91_BODY_SIZES="1048576 2097152" \
    bash "$script" >"$dir/out" 2>"$dir/err" &
  local pid=$!
  # A run takes a while to reach its cleanup (the probes and the body sizes
  # above), so this waits past that; it stops early if the run is already gone,
  # which is a failure below rather than something to wait for.
  local i=0
  while [ "$i" -lt 900 ] && kill -0 "$pid" 2>/dev/null; do
    [ -s "$dir/delete.mark" ] && break
    sleep 0.2
    i=$((i + 1))
  done
  if [ ! -s "$dir/delete.mark" ]; then
    kill -KILL "$pid" 2>/dev/null || true
    set +m
    wait "$pid" 2>/dev/null || true
    fail "second interrupt: the run never reached its cleanup (no delete started), so this test proved nothing: $(tail -n 2 "$dir/err" | tr '\n' ' ')"
    return 0
  fi
  kill -INT "$pid" 2>/dev/null || true
  local j=0
  while kill -0 "$pid" 2>/dev/null && [ "$j" -lt 600 ]; do
    sleep 0.2
    j=$((j + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null || true
    set +m
    fail "second interrupt: the run did not end after the second signal"
    wait "$pid" 2>/dev/null || true
    return 0
  fi
  wait "$pid" || st=$?
  set +m
  # The verdict is the run's own: the happy path measured what it measured, and
  # the signal during the cleanup may not change that or the block.
  expect_status "$st" 0 "second interrupt: the run's own verdict survives the second signal"
  expect_contains "$(cat "$dir/out")" "CHECK 91 RESULT END (exit 0)" \
    "second interrupt: the block was still printed after the cleanup"
  expect_contains "$(cat "$dir/err")" "cleanup is running, do not press Ctrl-C again" \
    "second interrupt: the run says what the second Ctrl-C did"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "run services delete fugaro-chk91" "second interrupt: the service delete still ran"
  expect_contains "$calls" "repositories delete fugaro-chk91" "second interrupt: the cleanup went on past the service"
  expect_contains "$out" "FACT restore-counts: PASS" "second interrupt: the counts were still read"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  [ -n "$sc" ] || fail "second interrupt: the scratch directory was never announced"
  if [ -d "$sc" ]; then fail "second interrupt: the cleanup was cut short — $sc survived"; fi
}

# A token on a command line is a token in `ps`: every user on the machine sees
# a process's argv for as long as it lives, and the hour-long stream would hold
# one there for an hour. The requests carry it in an 0600 curl config file
# instead, and this is the assertion that keeps it that way -- the fake gcloud
# and the fake curl both log the argv they were handed.
test_identity_token_is_never_on_a_command_line() {
  local dir=$work/token
  prepare "$dir"
  run91 "$dir"
  expect_status "$status" 0 "identity token: the run"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls" "$dir/logs/curl.calls" "$dir/logs/fugaro.calls")
  expect_absent "$calls" "FAKE-IDENTITY-TOKEN" "identity token: it appears in no command line at all"
  expect_absent "$(cat "$dir/out" "$dir/err")" "FAKE-IDENTITY-TOKEN" "identity token: it is printed nowhere"
  expect_contains "$(cat "$dir/logs/curl.calls")" "--config " "identity token: the request names a config file"
  expect_contains "$(cat "$dir/logs/curl.calls")" "mode=0600" "identity token: the file it is in is 0600"
  expect_absent "$(cat "$dir/logs/curl.calls")" "Bearer" "identity token: no -H header carries it"
}

# The ownership records are written BEFORE the create they record, so a run that
# dies in between -- Ctrl-C, a kill, a dropped connection -- still leaves the
# Restore something that says the resource is this check's. The fake gcloud
# touches its state and THEN hangs, so the test can look at the scratch
# directory while the create is still in flight: with the record written after
# the create, a run killed at this moment would leave a repository or a service
# behind with nothing claiming it.
test_ownership_record_precedes_the_create() {
  local spec dir hang logpat record
  for spec in "repo-create:repositories create:repo-mine" "deploy:run deploy:service-mine"; do
    hang=${spec%%:*}
    logpat=${spec#*:}
    record=${logpat#*:}
    logpat=${logpat%%:*}
    dir=$work/order-$hang
    prepare "$dir"
    start91 "$dir" FAKE_GCLOUD_HANG_ON="$hang"
    local pid=$start_pid
    if ! wait_for "$dir/logs/gcloud.calls" "$logpat" 150; then
      fail "record order ($hang): the run never reached '$logpat'"
      wait "$pid" || true
      continue
    fi
    local sc
    sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
    if [ -z "$sc" ]; then
      fail "record order ($hang): the run never announced its scratch directory"
    elif [ -f "$sc/$record" ]; then
      : # the record is on disk while the command that creates the thing is still running
    else
      fail "record order ($hang): $record is not on disk while '$logpat' is still running, so a run killed at this moment would leave the resource to be billed with nothing saying it is this check's"
    fi
    wait "$pid" || true
    case "$sc" in "" | /) ;; *) rm -rf "$sc" 2>/dev/null || true ;; esac
  done
}

# The other half of the same rule: a create that FAILED and really made nothing
# takes its record back, so the Restore does not name -- and certainly does not
# delete -- a resource that is not there.
test_failed_create_takes_its_record_back() {
  local dir=$work/createfail
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_CREATE_FAILS=yes
  expect_status "$status" 1 "failed create: the verdict"
  expect_contains "$err" "the repository create failed and fugaro-chk91 is not there" "failed create: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "repositories delete" \
    "failed create: the Restore does not delete a repository this run never made"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" \
    "failed create: there was nothing to restore"
  if [ -d "$scratch" ]; then fail "failed create: the scratch directory survived"; fi
}

# A deploy that reported failure but left the service there is the dangerous
# half: the record must STAY, because the service is this check's and is being
# billed, and the Restore has to delete it.
test_failed_deploy_keeps_the_record_it_made() {
  local dir=$work/deployfail
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_DEPLOY_FAILS=yes
  expect_status "$status" 1 "failed deploy: the verdict"
  expect_contains "$err" "the deploy failed and fugaro-chk91 is not there" "failed deploy: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run services delete" \
    "failed deploy: a service that is not there is never named"
  # The BUILD succeeded, so the image is in the repository — and it is deleted
  # here even though there is no service: the failed deploy's own stop takes the
  # service record back, and with the image delete gated on that record the
  # image was left in the repository, the repository delete failing on a
  # repository that still holds it, and the stop's message promising the image
  # went with the service. The build is what puts an image there, and its record
  # (BUILD_ATTEMPTED) is what the delete is gated on.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "artifacts docker images delete" \
    "failed deploy: the image the successful build made is deleted"
  expect_contains "$calls" "artifacts repositories delete fugaro-chk91" \
    "failed deploy: the repository this run made is still deleted"
  expect_contains "$out" "FACT restore: PASS" "failed deploy: the Restore ran"
}

# A signal during the cold-start wait has to end the run AT ONCE, and take the
# wait with it: bash runs a signal's trap only when the foreground command is
# done, so a foreground `sleep 2100` would hold the Restore behind half an hour
# of waiting. The run waits 45s here and must be gone well before that.
test_interrupt_during_the_idle_wait_is_prompt() {
  local dir=$work/interrupt
  prepare "$dir"
  local started ended
  started=$(date +%s)
  start91 "$dir" CHECK91_IDLE_SECONDS=45
  local pid=$start_pid
  if ! wait_for "$dir/err" "waiting 45s" 150; then
    fail "interrupt: the run never reached the 45s idle wait"
  fi
  kill -TERM "$pid" 2>/dev/null || true
  local status=0
  wait "$pid" || status=$?
  ended=$(date +%s)
  expect_status "$status" 130 "interrupt during the idle wait"
  local elapsed=$((ended - started))
  if [ "$elapsed" -ge 40 ]; then
    fail "interrupt: the run took ${elapsed}s to end after SIGTERM while it was waiting 45s — the trap did not end the wait, so the Restore was held behind it"
  fi
  expect_contains "$(cat "$dir/err")" "interrupted" "interrupt: the message"
  expect_contains "$(cat "$dir/out")" "CHECK 91 RESULT" "interrupt: the block still comes back"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "$sc" ] && [ -d "$sc" ]; then fail "interrupt: the scratch directory $sc survived"; fi
}

# The long stream is the one request that could outlive the run it belongs to: a
# curl with no cap of its own runs for as long as the relay keeps the socket
# open, so a Ctrl-C has nothing to end and the Restore waits behind it.
test_long_stream_request_carries_its_own_cap() {
  local dir=$work/longcap
  prepare "$dir"
  run91 "$dir" CHECK91_LONG_SECONDS=7
  local line
  line=$(grep -m1 -- '--max-time 7' "$dir/logs/curl.calls" || true)
  if [ -z "$line" ]; then
    fail "the long-stream curl carries no cap of this check's own (CHECK91_LONG_SECONDS=7 was set and no curl call carried --max-time 7), so that request outlives the run"
  fi
  expect_contains "$out" "FACT stream-longest: PASS" "long stream cap: the step still records where the stream ended"
  expect_status "$status" 0 "long stream cap: the run"
}

# Every request mints its OWN identity token, and the body table is where that
# matters most: it runs after the long stream, whose own mint is up to 59
# minutes old by the time the first row is sent, and an identity token lives
# about an hour. A row sent with that stale token answers 401 — and a 401 is not
# matched by the `case ... in 200*` that decides "largest accepted", so
# body-limit-http1 would read PASS with a too-small number: U5's request-body
# figure silently wrong, in exactly the shape of the size limit the table is
# measuring. The fake gcloud never ages a token, so expiry itself cannot be
# staged here; what can be checked is the thing that prevents it — the mint
# count has to grow with the number of rows.
test_every_body_row_mints_its_own_token() {
  local dir1=$work/mint1 dir3=$work/mint3 n1 n3
  prepare "$dir1"
  run91 "$dir1" CHECK91_BODY_SIZES="1048576"
  expect_status "$status" 0 "mints: the one-row run"
  prepare "$dir3"
  run91 "$dir3" CHECK91_BODY_SIZES="1048576 2097152 4194304"
  expect_status "$status" 0 "mints: the three-row run"
  n1=$(grep -c 'auth print-identity-token' "$dir1/logs/gcloud.calls" || true)
  n3=$(grep -c 'auth print-identity-token' "$dir3/logs/gcloud.calls" || true)
  # A floor, so a count of 0 reads as "this test is looking at the wrong calls"
  # rather than as "the step mints nothing": the probes before the table mint
  # too (the unauthenticated one, the two cold-start ones, the two streams).
  [ "${n1:-0}" -ge 5 ] || {
    fail "mints: the one-row run minted ${n1:-0} token(s), which is fewer than the steps before the table alone, so this test is not counting the right calls"
    return 0
  }
  [ "$((n3 - n1))" = 2 ] || fail "mints: the three-row run minted $n3 tokens against the one-row run's $n1, so the body table does not mint one per row — the rows after the first go out with a token that can be an hour old, and the 401 that answers them reads as the size limit"
  return 0
}

test_restore_leftover() {
  local dir=$work/leftover
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_DELETE_FAILS=yes
  expect_status "$status" 1 "leftover restore"
  expect_contains "$out" "FACT restore: FAIL" "leftover restore: the fact"
  expect_contains "$out" "services: 1, images: 1, repository: 1" "leftover restore: the counts"
  # The scratch directory is KEPT, on purpose: it holds the records that say
  # what is this check's, which is what the owner deletes the survivors by hand
  # from. The test removes it itself.
  if [ ! -d "$scratch" ]; then
    fail "leftover restore: the scratch directory was removed and its records are gone"
  else
    kept+=("$scratch")
  fi
  expect_contains "$err" "the scratch directory is kept" "leftover restore: the message"
  # ... and the block must not send the owner to run the check again: a fresh
  # run mints a scratch directory of its own and stops in the guards while the
  # service or the repository is still in the project, so a count that is not 0
  # is finished by hand or not at all.
  expect_contains "$out" "does NOT take it over" "leftover restore: the block says a fresh run cannot finish this"
  expect_contains "$out" "remove it yourself" "leftover restore: the block says what to do with the kept directory"
  expect_absent "$out" "re-run this check" "leftover restore: the block promises no impossible re-run"
}

# A count QUERY that fails is not a count of 0. The deletes above are guarded the same way,
# so a run whose session has expired late in its two hours can have every gcloud call fail at
# once, and `... | wc -l` prints 0 for a query that printed nothing -- the same 0 an empty
# list prints. Read as a count, that is `restore: PASS`, exit 0, and the ownership records
# deleted: exactly the false PASS the scratch-removed row beside it exists to prevent.
test_restore_count_query_failure_is_not_zero() {
  local dir=$work/countfail
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_DELETE_FAILS=yes FAKE_GCLOUD_LIST_FAILS=all
  expect_status "$status" 1 "failing count queries: the verdict"
  expect_contains "$out" "FACT restore-counts: FAIL" "failing count queries: the counts are not read as 0"
  expect_contains "$out" "a list query failed" "failing count queries: the fact says why there is no count"
  expect_absent "$out" "FACT restore-counts: PASS" "failing count queries: no count is claimed"
  expect_absent "$out" "FACT restore: PASS" "failing count queries: the Restore does not claim a clean project"
  expect_contains "$out" "FACT restore: FAIL" "failing count queries: the Restore says it cannot tell"
  expect_contains "$out" "END (exit 1)" "failing count queries: the block still comes back"
  # The records are what the owner deletes the survivors by hand from, so they are KEPT --
  # a failed query is not a reason to throw away the only statement of what is this check's.
  if [ ! -d "$scratch" ]; then
    fail "failing count queries: the scratch directory was removed on a Restore that could not read its counts"
  else
    kept+=("$scratch")
  fi
  expect_contains "$err" "the scratch directory is kept" "failing count queries: the message"
}

# One failed query is enough: the other two counts reading 0 must not paper over the one that
# could not be read. Here the service list is the one that fails, and the still-existing
# repository means the run cannot say the service is gone.
test_restore_count_query_failure_is_not_papered_over() {
  local dir=$work/onefail
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_DELETE_FAILS=yes FAKE_GCLOUD_LIST_FAILS=services
  expect_status "$status" 1 "one failing count query: the verdict"
  expect_contains "$out" "FACT restore-counts: FAIL" "one failing count query: the fact"
  expect_absent "$out" "restore-counts: PASS — services: 0" "one failing count query: the failed service list is not reported as 0"
  expect_contains "$out" "FACT restore: FAIL" "one failing count query: the Restore says it cannot tell"
  if [ ! -d "$scratch" ]; then
    fail "one failing count query: the scratch directory was removed"
  else
    kept+=("$scratch")
  fi
}

# A scratch directory the run cannot remove is a FAIL, and a FAIL in the block is a
# non-zero exit: the row is added to the facts BEFORE the verdict is read out of them, so a
# block that reads `scratch-removed: FAIL` cannot end at `exit 0` and be taken for a pass.
test_scratch_removal_failure_is_a_failure() {
  local dir=$work/unremovable
  prepare "$dir"
  write_fake_rm "$dir"
  run91 "$dir"
  expect_status "$status" 1 "unremovable scratch: the verdict"
  expect_contains "$out" "FACT scratch-removed: FAIL — the scratch directory is still there, at " \
    "unremovable scratch: the fact says the directory stayed"
  expect_contains "$out" "CHECK 91 RESULT END (exit 1)" "unremovable scratch: the block's own end line"
  # Everything else still ran and still passed: this is the one row that failed.
  expect_contains "$out" "FACT restore: PASS" "unremovable scratch: the Restore still ran"
  expect_contains "$out" "FACT restore-counts: PASS — services: 0, images: 0, repository: 0" \
    "unremovable scratch: the counts are still read"
  expect_absent "$out" "FACT scratch-removed: PASS" "unremovable scratch: the row does not claim the directory went"
  if [ -d "$scratch" ]; then
    kept+=("$scratch")
  else
    fail "unremovable scratch: the directory went after all, so this case asserted nothing"
  fi
}

test_sigint() {
  local dir=$work/sigint
  prepare "$dir"
  mkdir -p "$dir/tmp" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  set -m # background jobs of a non-interactive shell start with SIGINT ignored
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    FAKE_GCLOUD_MODE=hang \
    CHECK91_IDLE_SECONDS=0 CHECK91_PROBE_SECONDS=3 CHECK91_BODY_SIZES=1048576 \
    bash "$script" >"$dir/out" 2>"$dir/err" &
  local pid=$!
  local waited=0
  while [ "$waited" -lt 100 ]; do
    grep -q 'scratch directory:' "$dir/err" 2>/dev/null && break
    sleep 0.2
    waited=$((waited + 1))
  done
  # A terminal's Ctrl-C signals the whole foreground process group, so the
  # in-flight `gcloud` dies with the script and the script's trap runs at once.
  # Signalling the script alone would leave the child sleeping and bash would
  # defer the trap until it finished.
  kill -INT -- -"$pid" 2>/dev/null || true
  local status=0
  wait "$pid" || status=$?
  set +m
  expect_status "$status" 130 "sigint"
  expect_contains "$(cat "$dir/err")" "interrupted" "sigint: the message"
  expect_contains "$(cat "$dir/out")" "CHECK 91 RESULT" "sigint: the block"
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  [ -n "$sc" ] || fail "sigint: the scratch directory was never announced"
  if [ -d "$sc" ]; then fail "sigint: the scratch directory $sc survived"; fi
}

# The pre-wait liveness request is the one call in this script with no `-w`
# format and no `|| rc=$?` guard around it, and check91 installs no ERR trap: a
# connection-level curl failure (DNS, refused, its own --max-time) ended the run
# with curl's own exit code and NO `cold-start` fact — against the header's own
# 0/1 contract. The two probes fourteen lines later are guarded; this is the same
# guard, and the fact it records says which request failed so a pre-wait failure
# is not read as a failed cold probe.
test_pre_wait_liveness_failure_is_a_fact() {
  local dir=$work/preliveness
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=refuse-pre-liveness
  expect_status "$status" 1 "pre-wait liveness: the verdict is this check's own"
  expect_contains "$out" "FACT cold-start: FAIL — the pre-wait liveness request failed (curl exit 7)" \
    "pre-wait liveness: the failure is a fact with curl's own status"
  expect_absent "$out" "FACT cold-start: PASS" "pre-wait liveness: nothing is claimed as measured"
  expect_contains "$out" "CHECK 91 RESULT END (exit 1)" "pre-wait liveness: the block still comes back"
  # Nothing was probed: the two probes carry the -w format and the failed call
  # does not, so their absence is what says the idle wait was not sat through.
  if grep -q 'time_starttransfer' "$dir/logs/curl.calls"; then
    fail "pre-wait liveness: the cold/warm probes were made although the pre-wait request failed"
  fi
}

# curl exits 0 for any HTTP status (there is no --fail in this script), so a
# front end answering the long stream with a 403 in a fraction of a second left
# `out` reading "403 after 0.2s" with rc=0 — and the row was an unconditional
# PASS. The status is read now, exactly as it is for cold-start.
test_long_stream_error_page_is_not_a_pass() {
  local dir=$work/longerr
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=long-error-page
  expect_status "$status" 1 "long stream error page: the verdict"
  expect_contains "$out" "FACT stream-longest: FAIL — the long stream never started" \
    "long stream error page: the row is a failure"
  expect_contains "$out" "403" "long stream error page: the status is named"
  expect_absent "$out" "FACT stream-longest: PASS" "long stream error page: no PASS for a stream that never started"
  expect_contains "$out" "CHECK 91 RESULT END (exit 1)" "long stream error page: the block still comes back"
}

# One table, two rows, one meaning for PASS: the HTTP/1.1 row only PASSes when a
# 200* row exists, and the HTTP/2-to-front-end row used to be an unconditional
# PASS that printed "nothing accepted" inside it — a row that answered 403 read
# as a measurement of the front end's limit.
test_h2_row_refused_is_not_a_pass() {
  local dir=$work/h2refused
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=h2-refused
  expect_status "$status" 1 "h2 row refused: the verdict"
  expect_contains "$out" "FACT body-limit-http2-front-end: FAIL — no size was accepted over TLS to the front end" \
    "h2 row refused: the row is a failure"
  expect_absent "$out" "FACT body-limit-http2-front-end: PASS" "h2 row refused: no PASS for a table with nothing accepted"
  # The HTTP/1.1 half of the same table is untouched: it answered 200, so this is
  # the one row that failed.
  expect_contains "$out" "FACT body-limit-http1: PASS — largest accepted" "h2 row refused: the sibling row still measures"
  expect_contains "$out" "403" "h2 row refused: the status is in the table"
}

# The credential file holds a minted identity token and lives inside the scratch
# directory, which is KEPT when a Restore count is not 0. `rm -f "$AUTH_CONF" ||
# true` recorded nothing, so a removal that failed left a live bearer token
# inside a surviving directory with no fact saying so. Check 90 reports its own
# token file this way (TOKEN_REMOVED); this is the same row for this script.
test_a_token_file_that_survives_is_a_failure() {
  local dir=$work/tokenleft
  prepare "$dir"
  write_fake_rm_conf "$dir"
  run91 "$dir" FAKE_GCLOUD_DELETE_FAILS=yes
  expect_status "$status" 1 "token file left: the verdict"
  expect_contains "$out" "FACT restore-token-file: FAIL — the credential file IS STILL THERE at " \
    "token file left: the row says it survived"
  expect_contains "$out" "holding a minted identity token" "token file left: what the file holds is named"
  expect_contains "$out" "remove it yourself" "token file left: the owner is told what to do"
  expect_absent "$out" "FACT restore-token-file: PASS" "token file left: the row does not claim it went"
  expect_contains "$out" "CHECK 91 RESULT END (exit 1)" "token file left: the block still comes back"
  # The file really is there, or this case asserted nothing: the scratch
  # directory is kept on this path and the credential file is inside it.
  if [ ! -f "$scratch/idt.conf" ]; then
    fail "token file left: the credential file went after all, so this case asserted nothing"
  fi
  if [ -d "$scratch" ]; then kept+=("$scratch"); fi
}

test_never_belong() {
  # This check is the sandbox installation's and nobody else's: the project is
  # hard-coded, the doctor stop names it, and no executed command may ever name
  # another installation's project. `belong` appears in the script only in the
  # two places that say it is never used.
  local text calls
  text=$(cat "$script")
  expect_contains "$text" "P=fugaro-dev" "never belong: the project is hard-coded"
  # The phrase "gcloud config get-value project" occurs in this script's comments
  # only — and wrapped across two lines there — so asserting its absence proved
  # nothing about the CALL. What must not exist is an executed `gcloud config`:
  # the project is the literal above and is never read from a config.
  local cfg
  cfg=$(printf '%s\n' "$text" | grep -v '^[[:space:]]*#' | grep -n 'gcloud config' || true)
  if [ -n "$cfg" ]; then
    fail "never belong: the script runs a gcloud config command: $cfg"
  fi
  local outside
  outside=$(printf '%s\n' "$text" | grep -iv -e "never \`belong\`" -e "never belong" | grep -c "belong" || true)
  [ "$outside" = 0 ] || fail "never belong: 'belong' appears outside the two lines that refuse it ($outside)"
  calls=$(cat "$work"/happy/logs/gcloud.calls "$work"/happy/logs/curl.calls "$work"/happy/logs/fugaro.calls 2>/dev/null)
  expect_absent "$calls" "belong" "never belong: no command named it"
  # Every project argument gcloud was given names the sandbox project. This was
  # `expect_absent "$calls" "another-project"`, which can never fail: nothing in
  # the script or the fakes ever writes that string, so the assertion held
  # whatever the run passed. (Only gcloud's own log is read: `fugaro doctor
  # --project fugaro` names the INSTALLATION, which is a different thing.)
  local projects p
  projects=$(grep -o -- '--project=[^ ]*' "$work/happy/logs/gcloud.calls" | sort -u || true)
  [ -n "$projects" ] || fail "never belong: no gcloud --project argument was recorded, so this test checks nothing"
  for p in $projects; do
    [ "$p" = "--project=fugaro-dev" ] || fail "never belong: a gcloud command named $p"
  done
}

test_curl_cutting_the_stream
test_dry_run
test_project_guard
test_every_advertised_knob_is_read
test_doctor_failing_its_own_checks_is_not_a_stop
test_doctor_output_after_the_project_line_is_not_a_stop
test_service_taken
test_repo_taken
test_happy_path
test_warm_cold_start
test_error_page_is_not_a_cold_start
test_burst_stream
test_liveness_probe_failure_is_recorded
test_no_http2_curl
test_xtrace_is_refused
test_staged_source_is_removed
test_unreadable_staging_bucket_stops_before_the_build
test_every_curl_carries_a_cap
test_second_interrupt_during_cleanup_does_not_kill_it
test_identity_token_is_never_on_a_command_line
test_ownership_record_precedes_the_create
test_failed_create_takes_its_record_back
test_failed_deploy_keeps_the_record_it_made
test_interrupt_during_the_idle_wait_is_prompt
test_long_stream_request_carries_its_own_cap
test_every_body_row_mints_its_own_token
test_restore_leftover
test_restore_count_query_failure_is_not_zero
test_restore_count_query_failure_is_not_papered_over
test_scratch_removal_failure_is_a_failure
test_a_token_file_that_survives_is_a_failure
test_pre_wait_liveness_failure_is_a_fact
test_long_stream_error_page_is_not_a_pass
test_h2_row_refused_is_not_a_pass
test_sigint
test_never_belong

if [ "$failures" -ne 0 ]; then
  echo "check91_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check91_test.sh: ok"
