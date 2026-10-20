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
  case "${FAKE_FUGARO_MODE:-ok}" in
  ok) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
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
import os, sys

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


if mode == "hang":
    import time
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
    if state("service"):
        print("fugaro-chk91")
    sys.exit(0)

if args[:2] == ["run", "services"] and args[2:3] == ["delete"]:
    rm("service")
    sys.exit(0)

if args[:2] == ["run", "services"] and args[2:3] == ["update"]:
    touch("service")
    sys.exit(0)

if args[:2] == ["run", "deploy"]:
    touch("service")
    print("Deploying container to Cloud Run service [fugaro-chk91]... done.")
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["describe"]:
    sys.exit(0 if (state("repo") or os.environ.get("FAKE_GCLOUD_REPO_EXISTS") == "yes") else 1)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["create"]:
    touch("repo")
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["delete"]:
    rm("repo")
    sys.exit(0)

if args[:2] == ["artifacts", "repositories"] and args[2:3] == ["list"]:
    if state("repo"):
        print("projects/fugaro-dev/locations/us-east5/repositories/fugaro-chk91")
    sys.exit(0)

if args[:2] == ["artifacts", "docker"] and args[2:3] == ["images"]:
    if args[3:4] == ["delete"]:
        rm("image")
        sys.exit(0)
    if args[3:4] == ["list"] and state("image"):
        print("us-east5-docker.pkg.dev/fugaro-dev/fugaro-chk91/fugaro-chk91:chk91")
    sys.exit(0)

if args[:1] == ["builds"]:
    touch("image")
    print("BUILD SUCCESS")
    sys.exit(0)

if args[:2] == ["auth", "print-identity-token"]:
    print("FAKE-IDENTITY-TOKEN-FOR-TESTS-NOT-A-CREDENTIAL")
    sys.exit(0)

sys.exit(1)
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
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
authed = any(h.lower().startswith("authorization:") for h in headers)
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
    # A real SSE endpoint writes each event as it happens: the fake does too,
    # one a second, and all at once when the test asks for a burst.
    count = 3
    status, version = 200, "1.1"
    streamed = []
    for i in range(count):
        line = "data: 0.%d\n" % i
        streamed.append(line)
        if out != "/dev/null" and out:
            with open(out, "a") as fh:
                fh.write(line)
        else:
            sys.stdout.write(line)
        sys.stdout.flush()
        if mode != "burst":
            time.sleep(1)
    body = "".join(streamed)
    if fmt:
        emit()
    # `timeout` mode ends the way a real curl does: 28 when its own --max-time
    # cut the probe, 18 when the front end cut the stream mid-body.
    if mode == "timeout":
        sys.exit(28 if "--max-time" in args else 18)
    sys.exit(0)

# GET / -- liveness, and the started= value that makes a cold start checkable.
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

test_service_taken() {
  local dir=$work/svc
  prepare "$dir"
  mkdir -p "$dir/state" && : >"$dir/state/service"
  run91 "$dir"
  expect_status "$status" 1 "service taken"
  expect_contains "$err" "already exists in fugaro-dev/us-east5, and it is not this check's" "service taken: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "service taken: nothing deployed"
}

test_repo_taken() {
  local dir=$work/repo
  prepare "$dir"
  run91 "$dir" FAKE_GCLOUD_REPO_EXISTS=yes
  expect_status "$status" 1 "repository taken"
  expect_contains "$err" "never adopts, writes into or deletes a repository it did not create" "repository taken: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "repositories create" "repository taken: nothing created"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "repository taken: nothing pushed"
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

test_no_http2_curl() {
  local dir=$work/nohttp2
  prepare "$dir"
  run91 "$dir" FAKE_CURL_MODE=no-http2
  expect_status "$status" 1 "no HTTP/2 curl"
  expect_contains "$err" "this curl has no HTTP/2 support" "no HTTP/2 curl: the stop"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete fugaro-chk91" "no HTTP/2 curl: still restored"
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

test_never_belong() {
  # This check is the sandbox installation's and nobody else's: the project is
  # hard-coded, the doctor stop names it, and no executed command may ever name
  # another installation's project. `belong` appears in the script only in the
  # two places that say it is never used.
  local text calls
  text=$(cat "$script")
  expect_contains "$text" "P=fugaro-dev" "never belong: the project is hard-coded"
  expect_absent "$text" "gcloud config get-value project" "never belong: the project is never read from a config"
  local outside
  outside=$(printf '%s\n' "$text" | grep -iv -e "never \`belong\`" -e "never belong" | grep -c "belong" || true)
  [ "$outside" = 0 ] || fail "never belong: 'belong' appears outside the two lines that refuse it ($outside)"
  calls=$(cat "$work"/happy/logs/gcloud.calls "$work"/happy/logs/curl.calls "$work"/happy/logs/fugaro.calls 2>/dev/null)
  expect_absent "$calls" "belong" "never belong: no command named it"
  expect_absent "$calls" "another-project" "never belong: no command named another project"
}

test_curl_cutting_the_stream
test_dry_run
test_project_guard
test_service_taken
test_repo_taken
test_happy_path
test_warm_cold_start
test_burst_stream
test_liveness_probe_failure_is_recorded
test_no_http2_curl
test_restore_leftover
test_sigint
test_never_belong

if [ "$failures" -ne 0 ]; then
  echo "check91_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check91_test.sh: ok"
