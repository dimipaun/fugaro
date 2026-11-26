#!/usr/bin/env bash
# Tests for scripts/check-871-run-index-100x.sh. NOTHING here is a cloud, a
# project or a checkout: every case runs the check against a fake `fugaro`, a
# fake `gcloud` and a fake `curl` on PATH, and against a tiny HTTP server on the
# loopback that plays the runs bucket and the index's summary document. The
# server counts what the check's own seeder uploaded, what it deleted and which
# index documents its commits name, so a case can assert what the check really
# wrote and really removed.
#
# The seed, the walk and the cleanup are the point. A case drives the check
# through the whole of them with four synthetic runs, and the ones around it
# bend exactly the refusals the check rests on: a project that already holds a
# run, a config that names no repository, a checkout with no plan task, a
# `belong` name, a core build's graph stub, a read that did not answer, a
# project whose slug the product cannot name. Two more say what must never
# happen: the access token on an argv, and a dry run that calls anything at all.
#
# Usage: bash scripts/check-871-run-index-100x_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-871-run-index-100x.sh
work=$(mktemp -d)
server_pid=""
cleanup_all() {
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  if [ -n "${CHECK871_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK871_TEST_HEAD:-2400}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; out: $(printf '%s' "$out" | head -c 2000); err: $(printf '%s' "$err" | head -c 800)"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c "${CHECK871_TEST_HEAD:-2400}")"
}
expect_no_row() { # out key what
  if printf '%s\n' "$1" | awk -v k="$2" '$1 == k { found = 1 } END { exit !found }'; then
    fail "$3: expected no row '$2'"
  fi
}
expect_calls() { # log pattern want what
  local got
  got=$(grep -c -- "$2" "$1" || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 calls matching '$2', got $got: $(head -c 1200 "$1")"
}

# expect_median_is_middle OUT KEY WHAT: the figure a read's row prints is the
# middle of the samples that same row prints. The fake's per-sample sleeps are
# wide apart, so a figure that were the mean, the smallest or the largest would
# fail this.
expect_median_is_middle() { # out key what
  local line
  line=$(printf '%s\n' "$1" | awk -v k="$2" '$1 == k && ($2 == "PASS" || $2 == "FAIL") { print }')
  [ -n "$line" ] || {
    fail "$3: no row '$2' to read a figure from"
    return 0
  }
  local figure samples middle sorted
  figure=$(printf '%s' "$line" | sed -n 's/.*median \([0-9]*\) ms.*/\1/p')
  samples=$(printf '%s' "$line" | sed -n 's/.*samples \([0-9, ]*\) (warm-up.*/\1/p')
  [ -n "$figure" ] && [ -n "$samples" ] || {
    fail "$3: the row '$2' prints no figure and samples to compare: $line"
    return 0
  }
  sorted=$(printf '%s\n' $(printf '%s' "$samples" | tr ',' ' ') | sort -n)
  middle=$(printf '%s\n' "$sorted" | sed -n 2p)
  [ "$figure" = "$middle" ] ||
    fail "$3: the figure $figure ms of '$2' is not the middle of the samples ($samples), which is $middle ms"
}

# -------------------------------------------------------------- the fake cloud

# The server plays what the check writes and reads: the runs bucket's listing
# and objects, and the index's summary document. The summary counts the runs the
# bucket holds, which is what a reindex that reached the end of history would
# have computed, so the check's own cleanup is visible in the figure it waits
# on. `POST /__preload` puts one run in before a case and `POST /__reset` empties
# it between cases; `GET /__objects` and `GET /__commits` are what a case
# asserts against.
cat >"$work/cloud.py" <<'PY'
import http.server, json, re, sys, urllib.parse

port_file, log_file = sys.argv[1], sys.argv[2]
state = {"objects": {}, "commits": []}

def live_runs():
    return sorted({name.split("/")[2] for name in state["objects"] if name.startswith("runs/")})

def summary_document(project):
    return {
        "name": "projects/%s/databases/(default)/documents/runIndexMeta/summary" % project,
        "fields": {
            "version": {"integerValue": "1"},
            "complete": {"booleanValue": True},
            "runs": {"integerValue": str(len(live_runs()))},
        },
    }

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def answer(self, code, body):
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def note(self, method):
        with open(log_file, "a", encoding="utf-8") as f:
            f.write("%s %s\n" % (method, self.path))

    def body(self):
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length else b""

    def do_POST(self):
        parsed = urllib.parse.urlsplit(self.path)
        raw = self.body()
        self.note("POST")
        if parsed.path == "/__preload":
            state["objects"]["runs/owners-project-0123456789ab/20260101-000000-aaaa/task.json"] = b"{}"
            return self.answer(200, {})
        if parsed.path == "/__reset":
            state["objects"].clear()
            state["commits"] = []
            return self.answer(200, {})
        if parsed.path.startswith("/upload/storage/v1/b/"):
            name = urllib.parse.unquote(urllib.parse.parse_qs(parsed.query).get("name", [""])[0])
            state["objects"][name] = raw
            return self.answer(200, {"name": name})
        if parsed.path.endswith(":commit"):
            deletes = [w.get("delete", "") for w in json.loads(raw or b"{}").get("writes", [])]
            state["commits"].append(deletes)
            for target in deletes:
                state["objects"].pop("index/" + target.rsplit("/", 1)[-1], None)
            return self.answer(200, {})
        return self.answer(404, {"error": {"code": 404}})

    def do_DELETE(self):
        parsed = urllib.parse.urlsplit(self.path)
        self.note("DELETE")
        name = urllib.parse.unquote(parsed.path.split("/o/", 1)[-1])
        if name in state["objects"]:
            del state["objects"][name]
            return self.answer(200, {})
        return self.answer(404, {"error": {"code": 404}})

    def do_GET(self):
        parsed = urllib.parse.urlsplit(self.path)
        self.note("GET")
        if parsed.path == "/__objects":
            return self.answer(200, {"names": sorted(state["objects"])})
        if parsed.path == "/__commits":
            return self.answer(200, {"commits": state["commits"]})
        if "/o" in parsed.path and "/storage/v1/b/" in parsed.path:
            prefixes = ["runs/%s/" % name.split("/")[1] for name in state["objects"]]
            return self.answer(200, {"prefixes": sorted(set(prefixes))})
        match = re.search(r"/v1/projects/([^/]+)/databases/\(default\)/documents/runIndexMeta/summary$", parsed.path)
        if match:
            return self.answer(200, summary_document(match.group(1)))
        return self.answer(404, {"error": {"code": 404}})

class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True

with Server(("127.0.0.1", 0), Handler) as httpd:
    with open(port_file, "w", encoding="utf-8") as f:
        f.write(str(httpd.server_address[1]))
    httpd.serve_forever()
PY

python3 "$work/cloud.py" "$work/port" "$work/http.log" &
server_pid=$!
for _ in $(seq 1 100); do
  [ -s "$work/port" ] && break
  sleep 0.1
done
[ -s "$work/port" ] || {
  echo "the fake cloud did not start" >&2
  exit 1
}
port=$(cat "$work/port")
base=http://127.0.0.1:$port

cloud_control() { # cloud_control <path>
  python3 - "$base$1" <<'PY'
import sys, urllib.request
req = urllib.request.Request(sys.argv[1], data=b"", method="POST")
with urllib.request.urlopen(req, timeout=10) as r:
    r.read()
PY
}

cloud_objects() { # cloud_objects
  python3 - "$base/__objects" <<'PY'
import json, sys, urllib.request
with urllib.request.urlopen(sys.argv[1], timeout=10) as r:
    print(" ".join(json.load(r)["names"]))
PY
}

cloud_commits() { # cloud_commits — the number of index documents the commits named
  python3 - "$base/__commits" <<'PY'
import json, sys, urllib.request
with urllib.request.urlopen(sys.argv[1], timeout=10) as r:
    print(sum(len(c) for c in json.load(r)["commits"]))
PY
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

# The fake fugaro answers the commands Check 871 drives and models the one thing
# the timing rests on: how long each sample of a read takes. FAKE871_SLEEPS is a
# list of `<tag>=<ms>,<ms>` entries, one per call of that command in this case
# (the warm-up first), the last of which is repeated for any call past the list.
# It logs its whole argv, the directory it was run in, and its stdin.
cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'fugaro %s (stdin=%s) (cwd=%s)\n' "$*" "$kind" "$PWD" >>"${FAKE871_LOG:?}"
dir=${FAKE871_DIR:?}
case "$1 $2" in
"image status") tag=image ;;
"plan status") tag=plan ;;
"runs ls") tag=runs ;;
"runs reindex") tag=reindex ;;
"graph --json") tag=graph ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
count=$(cat "$dir/$tag.count" 2>/dev/null || printf 0)
count=$((count + 1))
printf '%s' "$count" >"$dir/$tag.count"
want=""
for entry in ${FAKE871_SLEEPS:-}; do
  case "$entry" in
  "$tag"=*)
    nth=$(printf '%s' "${entry#*=}" | tr ',' ' ')
    i=1
    for one in $nth; do
      want=$one
      [ "$i" -lt "$count" ] || break
      i=$((i + 1))
    done
    ;;
  esac
done
if [ -n "$want" ]; then
  python3 -c 'import sys, time; time.sleep(int(sys.argv[1]) / 1000.0)' "$want"
fi
case "$tag" in
image)
  cat "$dir/image.json"
  exit "${FAKE871_IMAGE_STATUS:-0}"
  ;;
plan)
  cat "$dir/plan.json"
  exit "${FAKE871_PLAN_STATUS:-0}"
  ;;
runs)
  cat "$dir/runs.json"
  exit "${FAKE871_RUNS_STATUS:-0}"
  ;;
reindex)
  printf 'index: marked incomplete, so every sweep of the history job walks it until one reaches the end of history\n'
  exit "${FAKE871_REINDEX_STATUS:-0}"
  ;;
graph)
  if [ -n "${FAKE871_GRAPH_STUB:-}" ]; then
    printf 'graph is part of Fugaro Subscription Edition; this build does not include it\n' >&2
    exit 1
  fi
  cat "$dir/graph.json"
  exit "${FAKE871_GRAPH_STATUS:-0}"
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# The fake gcloud answers the one call the check makes, and logs its argv: the
# token it prints must never be passed on as an argument of anything.
cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${FAKE871_LOG:?}"
case "$*" in
"auth print-access-token --quiet") printf 'ya29.fake-access-token\n' ;;
*) printf 'fake gcloud: unexpected call %s\n' "$*" >&2; exit 64 ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# The fake curl logs its argv and then reads the URL it was given from the fake
# cloud, so the check's own listing and summary reads go through the server. Its
# -K config file (where the token lives) is logged as the path it is, never read
# into the log.
cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
printf 'curl %s\n' "$*" >>"${FAKE871_LOG:?}"
url=""
for arg in "$@"; do
  case "$arg" in http*) url=$arg ;; esac
done
[ -n "$url" ] || { printf 'fake curl: no URL\n' >&2; exit 64; }
exec python3 - "$url" <<'PY'
import sys, urllib.request
try:
    with urllib.request.urlopen(sys.argv[1], timeout=15) as r:
        sys.stdout.write(r.read().decode("utf-8", "replace"))
except Exception as exc:
    sys.stdout.write('{"error":{"code":500,"message":"%s"}}' % exc)
PY
FAKE
chmod +x "$fakebin/curl"

# --------------------------------------------------------------- the fixtures

case_dir() { # case_dir <name>
  cdir=$work/$1
  answers=$cdir/answers
  checkout=$cdir/checkout
  xdg=$cdir/xdg
  mkdir -p "$answers" "$checkout" "$xdg/fugaro/projects"
  CALLS=$cdir/calls
  : >"$CALLS"
  : >"$work/http.log"
  rm -f "$answers"/*.count
  FAKE871_SLEEPS=""
  FAKE871_RUNS_STATUS=0
  FAKE871_PLAN_STATUS=0
  FAKE871_IMAGE_STATUS=0
  FAKE871_REINDEX_STATUS=0
  FAKE871_GRAPH_STATUS=0
  FAKE871_GRAPH_STUB=
  CASE_RUNS=${CHECK871_RUNS_CASE:-4}
  CHECK871_BOUND_MS=1000
  CHECK871_REPEATS=3
  CHECK871_MERGES=no
  CHECK871_KEEP_CLOUD=
  CHECK871_KEEP_SCRATCH=
  write_config
  write_image
  write_plan
  write_graph
  write_runs
  cloud_control /__reset
  : >"$work/http.log"
}

# The installation's own config: what the check reads the GCP project, the runs
# bucket and the repository from.
write_config() { # write_config [yaml]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$xdg/fugaro/projects/scratch-check.yaml"
  else
    cat >"$xdg/fugaro/projects/scratch-check.yaml" <<'YAML'
version: 1
name: scratch-check
gcp_project: scratch-project-1
region: us-central1
runs_bucket: scratch-runs
repos:
  acme/app:
    provider: github
    workflows:
      - default
YAML
  fi
}

write_image() { # write_image [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/image.json"
  else
    cat >"$answers/image.json" <<'JSON'
{"project":"scratch-check","workflows":[{"repo":"acme/app","workflow":"default","slug":"acme-app-0123456789ab"}]}
JSON
  fi
}

write_plan() { # write_plan [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/plan.json"
  else
    cat >"$answers/plan.json" <<'JSON'
{"repo":"acme/app","plans":[{"id":"plans/thing","nodes":[{"id":"T-ONE"},{"id":"T-TWO"}]}],"totals":{"plans":1,"tasks":2,"closed":0,"open":2},"streams":[],"warnings":[],"index_complete":true,"folded_through":"2026-11-25T09:30:00Z"}
JSON
  fi
}

write_runs() { # write_runs [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/runs.json"
  else
    printf '{"runs":[{"run":"acme-app/20261120-101112-abcd","status":"succeeded"}],"totals":{"runs":%s},"index_complete":true,"folded_through":"2026-11-25T09:30:00Z"}\n' \
      "$CASE_RUNS" >"$answers/runs.json"
  fi
}

write_graph() { # write_graph [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/graph.json"
  else
    cat >"$answers/graph.json" <<'JSON'
{"version":1,"assembled_at":"2026-11-25T09:30:00Z","sources":[{"scope":"all","state":"up"}],"nodes":[],"edges":[],"warnings":[]}
JSON
  fi
}

# ------------------------------------------------------------------ the runs

run871_raw() { # run871_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" XDG_CONFIG_HOME="${xdg:-$work/none}" \
    FAKE871_LOG="${CALLS:-/dev/null}" FAKE871_DIR="${answers:-$work}" \
    FAKE871_SLEEPS="${FAKE871_SLEEPS:-}" \
    FAKE871_RUNS_STATUS="${FAKE871_RUNS_STATUS:-0}" \
    FAKE871_PLAN_STATUS="${FAKE871_PLAN_STATUS:-0}" \
    FAKE871_IMAGE_STATUS="${FAKE871_IMAGE_STATUS:-0}" \
    FAKE871_REINDEX_STATUS="${FAKE871_REINDEX_STATUS:-0}" \
    FAKE871_GRAPH_STATUS="${FAKE871_GRAPH_STATUS:-0}" \
    FAKE871_GRAPH_STUB="${FAKE871_GRAPH_STUB:-}" \
    CHECK871_RUNS="${CASE_RUNS:-4}" CHECK871_PARALLEL=2 CHECK871_POLL_SECONDS=1 \
    CHECK871_WAIT_SECONDS=30 CHECK871_CLEAN_WAIT_SECONDS=20 CHECK871_CLEAN_ROUNDS=1 \
    CHECK871_BOUND_MS="${CHECK871_BOUND_MS:-1000}" CHECK871_REPEATS="${CHECK871_REPEATS:-3}" \
    CHECK871_MERGES="${CHECK871_MERGES:-no}" \
    CHECK871_GCS_URL="$base" CHECK871_FIRESTORE_URL="$base" \
    CHECK871_KEEP_CLOUD="${CHECK871_KEEP_CLOUD:-}" \
    CHECK871_KEEP_SCRATCH="${CHECK871_KEEP_SCRATCH:-}" \
    perl -e 'alarm shift; exec @ARGV or die' 300 bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run871() { # run871 <args...> — the case's own checkout, then what the case passes
  run871_raw --checkout "${checkout:-$work/none}" "$@" </dev/null
}

# ------------------------------------------------------------------- the cases

test_a_seeded_project_passes() {
  case_dir pass
  run871 --scratch scratch-check
  expect_status "$status" 0 "a seeded project"
  expect_contains "$out" "CHECK 871 RESULT: PASS (exit 0)" "a seeded project"
  expect_row "$out" config PASS "a seeded project"
  expect_row "$out" token PASS "a seeded project"
  expect_row "$out" fresh PASS "a seeded project"
  expect_row "$out" slug PASS "a seeded project"
  expect_row "$out" plans PASS "a seeded project"
  expect_row "$out" seed PASS "a seeded project"
  expect_row "$out" reindex PASS "a seeded project"
  expect_row "$out" walk PASS "a seeded project"
  expect_row "$out" runs-ls PASS "a seeded project"
  expect_row "$out" plan-status PASS "a seeded project"
  expect_row "$out" graph PASS "a seeded project"
  expect_row "$out" index PASS "a seeded project"
  expect_row "$out" scale PASS "a seeded project"
  expect_row "$out" clean PASS "a seeded project"
  expect_row "$out" restore PASS "a seeded project"
  expect_contains "$out" "seeded 4 runs (12 objects)" "the seed says what it wrote"
  expect_contains "$out" "the index carries the 4 runs this check seeded" "the scale is the seed"
  expect_contains "$out" "removed 12 objects and 4 index documents" "the cleanup says what it removed"
  [ "$(cloud_objects)" = "" ] || fail "a seeded project: the check left $(cloud_objects) in the bucket"
  [ "$(cloud_commits)" = 4 ] || fail "a seeded project: expected a commit naming 4 index documents, got $(cloud_commits)"
}

test_the_figure_is_the_median_of_the_samples() {
  case_dir median
  FAKE871_SLEEPS="runs=10,220,60 plan=10,300,40 graph=10,20,400"
  run871 --scratch scratch-check
  expect_status "$status" 0 "the median"
  expect_median_is_middle "$out" runs-ls "the median: the listing"
  expect_median_is_middle "$out" plan-status "the median: the plan join"
  expect_median_is_middle "$out" graph "the median: the graph"
  expect_not_contains "$out" "median 400 ms" "the median: the largest sample is not the figure"
  expect_not_contains "$out" "median 10 ms" "the median: the warm-up is not the figure"
}

test_a_slow_read_fails() {
  case_dir slow
  FAKE871_SLEEPS="runs=10,1200,1200 graph=10,10,10"
  run871 --scratch scratch-check
  expect_status "$status" 1 "a read at or over the bound"
  expect_contains "$out" "CHECK 871 RESULT: FAIL (exit 1)" "a read at or over the bound"
  expect_row "$out" runs-ls FAIL "a read at or over the bound"
  expect_contains "$out" "at or over the 1000 ms bound" "the bound is named"
  expect_row "$out" plan-status PASS "the other reads are still judged"
  expect_row "$out" clean PASS "a slow read is still cleaned up after"
}

test_a_read_that_did_not_answer_fails() {
  case_dir no_answer
  FAKE871_RUNS_STATUS=7
  run871 --scratch scratch-check
  expect_status "$status" 1 "a read that exited non-zero"
  expect_row "$out" runs-ls FAIL "a read that exited non-zero"
  expect_contains "$out" "warm read exited 7" "the exit status is named"
  expect_row "$out" graph PASS "the other reads were measured"
}

test_a_core_build_s_graph_is_unverified() {
  case_dir no_graph
  FAKE871_GRAPH_STUB=1
  run871 --scratch scratch-check
  expect_status "$status" 3 "a core build"
  expect_contains "$out" "CHECK 871 RESULT: INCOMPLETE (exit 3)" "a core build"
  expect_row "$out" graph UNVERIFIED "a core build"
  expect_contains "$out" "released binary" "the fix is named"
  expect_row "$out" seed PASS "the seed still ran"
  expect_row "$out" clean PASS "the seed is still removed"
}

test_a_project_that_holds_a_run_is_refused() {
  case_dir occupied
  cloud_control /__preload
  run871 --scratch scratch-check
  expect_status "$status" 2 "a project that holds a run"
  expect_contains "$out" "CHECK 871 RESULT: INCOMPLETE (exit 2)" "a project that holds a run"
  expect_contains "$err" "holds 1 run(s)" "the reason is named"
  expect_row "$out" belong PASS "the guards ran first"
  expect_row "$out" config PASS "the config was read"
  expect_no_row "$out" seed "nothing is seeded into a project that holds a run"
  expect_contains "$(cloud_objects)" "owners-project-0123456789ab" "the run that was there is still there"
}

test_a_config_without_a_repository_is_refused() {
  case_dir no_repo
  write_config 'version: 1
name: scratch-check
gcp_project: scratch-project-1
region: us-central1
runs_bucket: scratch-runs
'
  run871 --scratch scratch-check
  expect_status "$status" 2 "a config that names no repository"
  expect_contains "$err" "names no repository" "the reason is named"
  expect_no_row "$out" seed "nothing is seeded without a repository to seed under"
}

test_a_missing_config_is_refused() {
  case_dir no_config
  rm -f "$xdg/fugaro/projects/scratch-check.yaml"
  run871 --scratch scratch-check
  expect_status "$status" 2 "a project with no config"
  expect_contains "$err" "has no config at" "the path is named"
  expect_contains "$err" "scratch-check.yaml" "the path is named"
}

test_the_slug_comes_from_the_product() {
  case_dir slug
  run871 --scratch scratch-check
  expect_status "$status" 0 "the slug the product computed"
  expect_calls "$CALLS" "fugaro image status --json --project scratch-check" 1 "the product is asked once"
  expect_contains "$out" "the slug the product itself computed" "the source is named"
  expect_contains "$(cat "$work/http.log")" "name=runs%2Facme-app-0123456789ab%2F" "the runs are under the product's own slug"
}

test_the_slug_can_be_given() {
  case_dir slug_given
  run871 --scratch scratch-check --slug given-slug-abcdef012345
  expect_status "$status" 0 "a slug given on the command line"
  expect_calls "$CALLS" "image status" 0 "the product is not asked when the slug is given"
  expect_contains "$out" "the slug this check was given" "the source is named"
  expect_contains "$(cat "$work/http.log")" "name=runs%2Fgiven-slug-abcdef012345%2F" "the runs are under the slug it was given"
}

test_a_checkout_with_no_plan_task_is_refused() {
  case_dir no_tasks
  write_plan '{"repo":"acme/app","plans":[{"id":"plans/thing","nodes":[]}],"totals":{"plans":1,"tasks":0},"streams":[],"warnings":[],"index_complete":true}'
  run871 --scratch scratch-check
  expect_status "$status" 2 "a checkout with no plan task"
  expect_contains "$err" "carries no plan task" "the reason is named"
  expect_no_row "$out" seed "nothing is seeded when the join would measure nothing"
}

test_only_the_commands_the_check_needs_are_called() {
  case_dir calls
  run871 --scratch scratch-check
  expect_status "$status" 0 "the calls"
  expect_calls "$CALLS" "gcloud auth print-access-token --quiet" 1 "one token"
  expect_calls "$CALLS" "fugaro image status --json --project scratch-check" 1 "one image status"
  expect_calls "$CALLS" "fugaro plan status --json --all --no-merges --project scratch-check" 5 "one plan status for the tasks, one warm-up and three timed samples"
  expect_calls "$CALLS" "fugaro runs reindex --project scratch-check" 2 "one reindex for the walk and one for the cleanup"
  expect_calls "$CALLS" "fugaro runs ls --json --project scratch-check" 4 "one warm-up and three timed samples"
  expect_calls "$CALLS" "fugaro graph --json --project scratch-check" 4 "one warm-up and three timed samples"
  expect_contains "$(cat "$CALLS")" "(cwd=$checkout)" "the plan join runs in the checkout"
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "every call is made with stdin closed"
}

test_the_token_is_never_an_argument() {
  case_dir token
  run871 --scratch scratch-check
  expect_status "$status" 0 "the token"
  expect_not_contains "$(cat "$CALLS")" "ya29.fake-access-token" "the token is on no argv"
  expect_not_contains "$out" "ya29.fake-access-token" "the token is in no output"
  expect_not_contains "$err" "ya29.fake-access-token" "the token is in no output"
  expect_contains "$(cat "$CALLS")" "curl -sS -K " "the token reaches curl in its own config file"
  expect_contains "$out" "on no argv" "the check says so itself"
}

test_the_cloud_can_be_kept() {
  case_dir keep_cloud
  CHECK871_KEEP_CLOUD=yes
  run871 --scratch scratch-check
  expect_status "$status" 0 "a kept cloud"
  expect_row "$out" clean INFO "a kept cloud says what it left"
  expect_contains "$out" "CHECK871_KEEP_CLOUD" "the reason is named"
  expect_calls "$CALLS" "runs reindex" 1 "no cleanup reindex is asked for"
  [ -n "$(cloud_objects)" ] || fail "a kept cloud: the seeded runs are still there"
}

test_a_dry_run_calls_nothing() {
  case_dir dry
  run871 --dry-run --scratch scratch-check
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "CHECK 871 PLAN" "a dry run prints its plan"
  expect_contains "$out" "Nothing was called and nothing was judged (--dry-run)" "a dry run says so"
  if [ -s "$CALLS" ]; then
    fail "a dry run called something: $(head -c 400 "$CALLS")"
  fi
  if [ -s "$work/http.log" ]; then
    fail "a dry run touched the fake cloud: $(head -c 400 "$work/http.log")"
  fi
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" bash "$script" --scratch scratch-check 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  expect_contains "$err" "no agent runs Check 871" "an agent session"
  if [ -s "$CALLS" ]; then
    fail "an agent session called something: $(head -c 400 "$CALLS")"
  fi
}

test_a_traced_shell_is_refused() {
  case_dir traced
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u AI_AGENT PATH="$fakebin:$PATH" \
    bash -x "$script" --scratch scratch-check 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "a traced shell"
  expect_contains "$err" "this shell is tracing" "a traced shell"
  expect_not_contains "$err" "ya29.fake-access-token" "the trace of a refused run mints no token"
}

test_the_owners_installation_is_refused() {
  local name
  for name in belong Belong belong-dev edge-devel-dimi edgeappinc; do
    case_dir "belong_$name"
    run871 --scratch "$name"
    expect_status "$status" 2 "the owner's installation as '$name'"
    expect_contains "$err" "names the owner's own installation" "the owner's installation as '$name'"
    if [ -s "$CALLS" ]; then
      fail "'$name' called something: $(head -c 400 "$CALLS")"
    fi
  done
}

test_bad_arguments_are_refused() {
  case_dir args
  run871 --scratch
  expect_status "$status" 2 "a flag with no value"
  expect_contains "$err" "--scratch takes a project" "a flag with no value"
  run871 --scratch scratch-check --scratch other
  expect_status "$status" 2 "two scratch projects"
  expect_contains "$err" "one scratch project at a time" "two scratch projects"
  run871 scratch-check
  expect_status "$status" 2 "a project with no flag"
  expect_contains "$err" "is not an argument of this check" "a project with no flag"
  run871 --scratch scratch-check --nope
  expect_status "$status" 2 "an unknown flag"
  expect_contains "$err" "unknown argument --nope" "an unknown flag"
  run871 --scratch scratch-check --checkout "$work/none"
  expect_status "$status" 2 "a checkout that is not there"
  expect_contains "$err" "is not a directory" "a checkout that is not there"
  run871 --scratch scratch-check --slug 'Not A Slug'
  expect_status "$status" 2 "a slug that is not one"
  expect_contains "$err" "is not a storage slug" "a slug that is not one"
  if [ -s "$CALLS" ]; then
    fail "a refused argument called something: $(head -c 400 "$CALLS")"
  fi
}

test_the_scratch_can_be_kept() {
  case_dir keep_scratch
  CHECK871_KEEP_SCRATCH=yes
  run871 --scratch scratch-check
  expect_status "$status" 0 "a kept scratch directory"
  expect_row "$out" restore INFO "a kept scratch directory"
  expect_contains "$out" "remove it yourself" "the token in it is named"
}

test_help() {
  case_dir help
  run871 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 871" "--help names the check"
  expect_contains "$out" "PASS (exit 0)" "--help names the verdicts"
  if [ -s "$CALLS" ]; then
    fail "--help called something"
  fi
}

# --------------------------------------------------------------------- driver

test_index=0
t() {
  test_index=$((test_index + 1))
  if [ -n "${CHECK871_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK871_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((test_index % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_a_seeded_project_passes
t test_the_figure_is_the_median_of_the_samples
t test_a_slow_read_fails
t test_a_read_that_did_not_answer_fails
t test_a_core_build_s_graph_is_unverified
t test_a_project_that_holds_a_run_is_refused
t test_a_config_without_a_repository_is_refused
t test_a_missing_config_is_refused
t test_the_slug_comes_from_the_product
t test_the_slug_can_be_given
t test_a_checkout_with_no_plan_task_is_refused
t test_only_the_commands_the_check_needs_are_called
t test_the_token_is_never_an_argument
t test_the_cloud_can_be_kept
t test_a_dry_run_calls_nothing
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_the_owners_installation_is_refused
t test_bad_arguments_are_refused
t test_the_scratch_can_be_kept
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check-871-run-index-100x_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-871-run-index-100x_test.sh: ok"
