#!/usr/bin/env bash
# Check 871 — the run index's three reads at 100x, on a SCRATCH project of
# yours, as ONE script YOU run. It is the second half of the pair 870/871 the
# plan docs/console-fixes/implementation-plan.md takes (D3205): 870 times the
# reads on your own project, and this one times them where the index carries
# 350,000 runs rather than yours.
#
# It seeds a scratch installation with 350,000 synthetic runs through the
# product's own path — three objects per run in the runs bucket, then
# `fugaro runs reindex`, which is the history job's reindex mode (RI5: the reset
# that marks the walk owed, then one execution of the history job that walks the
# bucket into the index) — waits for the walk to reach the end of history, times
# the same three reads Check 870 times, and then removes everything it wrote.
#
# The objects it writes are what a real run leaves: task.json, launch.json and
# result.json under runs/<slug>/<run id>/. The run ids are minted from one base
# moment, one second apart, so the walk lists them the way it lists history. The
# plan task each launch records is one of the tasks THIS CHECKOUT's plans carry
# (read from `fugaro plan status --json --all` itself), so the plan join reads
# entries that really join rather than entries that match nothing.
#
# WHAT IT WRITES, AND WHAT IT REMOVES. Only the scratch project it is pointed
# at, and only under names of its own: runs/<slug>/<run id>/ with those three
# objects, and runIndex/<run id> in that project's index. Its cleanup deletes
# the objects, deletes the index documents it seeded (the index has no prune:
# a fold never removes an entry, so a walk alone would leave them) and runs
# `fugaro runs reindex` once more, so the summary is recomputed over an index
# that no longer holds them. It refuses to start unless the project holds no
# run at all, which is what makes the slug it seeds under and the index it
# writes into its own to remove.
#
# Usage:      bash scripts/check-871-run-index-100x.sh --scratch <project>
#             bash scripts/check-871-run-index-100x.sh --scratch <project> --checkout <dir>
#             bash scripts/check-871-run-index-100x.sh --dry-run --scratch <project>
#             bash scripts/check-871-run-index-100x.sh --help
# Environment CHECK871_RUNS      how many synthetic runs are seeded (default
#                                350000: the plan's 100x, against the 3,500 runs
#                                of a real installation)
#             CHECK871_CHECKOUT  the checkout the plan join is run in (default
#                                `.`)
#             CHECK871_SLUG      the storage slug to seed under, when it is not
#                                read from `fugaro image status --json`
#             CHECK871_FUGARO    the binary to read (default `fugaro`)
#             CHECK871_BOUND_MS  the bound each median is judged by, in
#                                milliseconds (default 1000)
#             CHECK871_REPEATS   how many times each read is timed (default 3)
#             CHECK871_PARALLEL  how many uploads and deletes run at once
#                                (default 32)
#             CHECK871_TASKS     how many of the checkout's plan tasks the
#                                seeded launches claim (default 8); they are
#                                what the plan join asks the index about
#             CHECK871_WAIT_SECONDS   how long the walk is waited for (default
#                                10800); the walk is resumable, so a walk that
#                                has not finished is reported, never failed
#             CHECK871_POLL_SECONDS   how often the summary is read while
#                                waiting (default 15)
#             CHECK871_CLEAN_WAIT_SECONDS   how long each cleanup round waits
#                                for the summary to be recomputed (default 1800)
#             CHECK871_CLEAN_ROUNDS   how many times the cleanup may remove the
#                                index documents and ask for the recompute
#                                (default 3; a walk that is still going when
#                                the cleanup starts can fold an entry back, and
#                                one more round is what removes it)
#             CHECK871_CALL_SECONDS   the deadline for one `fugaro` call
#                                (default 1800)
#             CHECK871_MERGES=yes     call `plan status` without --no-merges
#             CHECK871_KEEP_CLOUD=yes leave the seeded runs and index documents
#                                in place and say so (they are then YOURS to
#                                remove)
#             CHECK871_KEEP_SCRATCH=yes   keep the scratch directory
# Needs:      bash, mktemp, python3 (the clock, the seeder and the reader of the
#             documents), perl (the deadline), gcloud, curl, and a fugaro on
#             PATH.
# Calls:      gcloud auth print-access-token --quiet; one curl GET of the runs
#             bucket's listing, the seed's own uploads and deletes over the GCS
#             JSON API and the index documents' own deletes over the Firestore
#             REST API (all with the caller's own token, from python3); `fugaro
#             runs ls --json`; `fugaro image status --json`; `fugaro plan status
#             --json --all` in the checkout; `fugaro runs reindex`; `fugaro
#             graph --json`. Every call runs with stdin closed and nothing is
#             written outside this run's scratch directory.
# Where:      the scratch project you name, and ONLY it. Never `belong`: a
#             project whose name, and a config whose own members, carry it are
#             refused by name before a single call. Point it at a THROWAWAY
#             installation: it writes hundreds of thousands of objects and index
#             documents there, and removes them on the way out.
# Runs:       by YOU, at your own terminal, after Check 870. It refuses to start
#             in a coding agent's session (its own test,
#             check-871-run-index-100x_test.sh, drives it against fakes on PATH
#             and a local HTTP server, and touches no cloud). No agent runs it.
# Costs:      the scratch project's own, and it is not small: about 1.05M
#             object writes and as many deletes in the runs bucket, 350,000
#             Firestore documents written and deleted, and the history job's
#             executions that walk them — roughly ten dollars of GCS and
#             Firestore operations, and hours of wall clock, not minutes.
# Token:      the access token goes into a 0600 curl config file and into the
#             seeder's own environment in a 0700 scratch directory that the
#             exit trap removes; it is never printed and never on an argv.
# Output:     progress on stderr, ONE block on stdout headed CHECK 871 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        the walk reached the end of history over
#                                    350,000 entries, all three medians are
#                                    under the bound, and everything the check
#                                    wrote was removed
#               FAIL (exit 1)        a median is at or over the bound, a read
#                                    did not answer, the seed failed or the
#                                    cleanup did not land
#               INCOMPLETE (exit 2)  the run stopped before it could judge
#               INCOMPLETE (exit 3)  it judged, and something it needed was not
#                                    there to measure: a walk that had not
#                                    reached the end of history, a build with
#                                    no graph, or an index that does not carry
#                                    the seeded runs
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
PROJECT=""
CHECKOUT=""
SLUG=""
DRY_RUN=no
SEEDED=no
CLEANED=no
TOKEN=""
BUCKET=""
GCP=""
REPO=""
WORKFLOW=""
PLAN_TASKS=""
BASE_EPOCH=0

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY STATE "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 871 — the run index's three reads at 100x, on a scratch project of yours.
It seeds the project with 350,000 synthetic runs through `fugaro runs reindex`,
times the listing, the plan join and the graph, and removes everything it wrote.

Usage:
  bash scripts/check-871-run-index-100x.sh --scratch <project>
  bash scripts/check-871-run-index-100x.sh --scratch <project> --checkout <dir>
  bash scripts/check-871-run-index-100x.sh --dry-run --scratch <project>
  bash scripts/check-871-run-index-100x.sh --help

Point it at a THROWAWAY installation that holds no run: it refuses to seed one
that does, and its cleanup removes the objects, the index documents and the
summary's figure back to where they were.

  PASS (exit 0)  FAIL (exit 1)  INCOMPLETE (exit 2 stopped, exit 3 unverified)
USAGE
}

# The owner's own installation is never touched: `belong` is its name, and
# `edge-devel-dimi` and `edgeappinc` are its GCP projects (scripts/belong.sh).
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong* | *edge-devel-dimi* | *edgeappinc*) return 0 ;;
  esac
  return 1
}

guard_belong() { # guard_belong WHAT VALUE...
  local what=$1 value
  shift
  for value in "$@"; do
    if is_belong "$value"; then
      stop "the $what '$value' names the owner's own installation, and this check is never run against it. Nothing was read anywhere"
    fi
  done
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 871: it writes hundreds of thousands of objects into a project and needs a person's own token. Run it yourself, at your terminal: bash scripts/check-871-run-index-100x.sh --scratch <project>"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the access token this check mints. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check seeds and reads a project with it"
}

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --dry-run) DRY_RUN=yes ;;
  --scratch)
    [ $# -ge 2 ] || stop "--scratch takes a project (usage: check-871-run-index-100x.sh --scratch <project>)"
    if [ -z "$PROJECT" ]; then PROJECT=$2; else stop "one scratch project at a time (got '$PROJECT' and '$2')"; fi
    shift
    ;;
  --checkout)
    [ $# -ge 2 ] || stop "--checkout takes a directory (usage: check-871-run-index-100x.sh --scratch <project> --checkout <dir>)"
    CHECKOUT=$2
    shift
    ;;
  --slug)
    [ $# -ge 2 ] || stop "--slug takes the storage slug to seed under"
    SLUG=$2
    shift
    ;;
  -*) stop "unknown argument $1 (usage: check-871-run-index-100x.sh --scratch <project>, or --help)" ;;
  *) stop "'$1' is not an argument of this check: the scratch project is named with --scratch <project>" ;;
  esac
  shift
done

guard_xtrace
guard_agent

[ -n "$PROJECT" ] || stop "no scratch project was named (usage: check-871-run-index-100x.sh --scratch <project>)"

FUGARO=${CHECK871_FUGARO:-fugaro}
CHECKOUT=${CHECKOUT:-${CHECK871_CHECKOUT:-.}}
SLUG=${SLUG:-${CHECK871_SLUG:-}}
RUNS=${CHECK871_RUNS:-350000}
BOUND_MS=${CHECK871_BOUND_MS:-1000}
REPEATS=${CHECK871_REPEATS:-3}
PARALLEL=${CHECK871_PARALLEL:-32}
WAIT_SECONDS=${CHECK871_WAIT_SECONDS:-10800}
POLL_SECONDS=${CHECK871_POLL_SECONDS:-15}
CALL_SECONDS=${CHECK871_CALL_SECONDS:-1800}
CLEAN_WAIT_SECONDS=${CHECK871_CLEAN_WAIT_SECONDS:-1800}
CLEAN_ROUNDS=${CHECK871_CLEAN_ROUNDS:-3}
TASK_FANOUT=${CHECK871_TASKS:-8}
GCS_URL=${CHECK871_GCS_URL:-https://storage.googleapis.com}
FIRESTORE_URL=${CHECK871_FIRESTORE_URL:-https://firestore.googleapis.com}
NUMBERS="CHECK871_RUNS:$RUNS CHECK871_BOUND_MS:$BOUND_MS CHECK871_REPEATS:$REPEATS CHECK871_PARALLEL:$PARALLEL CHECK871_WAIT_SECONDS:$WAIT_SECONDS CHECK871_POLL_SECONDS:$POLL_SECONDS CHECK871_CALL_SECONDS:$CALL_SECONDS CHECK871_CLEAN_WAIT_SECONDS:$CLEAN_WAIT_SECONDS CHECK871_CLEAN_ROUNDS:$CLEAN_ROUNDS CHECK871_TASKS:$TASK_FANOUT"
for pair in $NUMBERS; do
  case "${pair#*:}" in '' | *[!0-9]*) stop "${pair%%:*} '${pair#*:}' is not a number" ;; esac
done
for pair in $NUMBERS; do
  [ "${pair#*:}" -ge 1 ] || stop "${pair%%:*} must be at least 1"
done

# `belong` is refused before anything is judged any other way.
guard_belong "scratch project" "$PROJECT"
guard_belong "checkout" "$CHECKOUT"

printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' ||
  stop "'$PROJECT' is not a Fugaro project name"
if [ -n "$SLUG" ]; then
  printf '%s' "$SLUG" | grep -Eq '^[a-z0-9-]+$' || stop "'$SLUG' is not a storage slug"
fi

need mktemp
need python3
need perl
need gcloud
need curl
command -v "$FUGARO" >/dev/null 2>&1 || stop "$FUGARO is not on PATH, and this check seeds and reads the index with it"

[ -d "$CHECKOUT" ] || stop "the checkout '$CHECKOUT' is not a directory: name the repository whose plans are joined with CHECK871_CHECKOUT or --checkout"

PLAN_ARGS=(--json --all)
if [ "${CHECK871_MERGES:-no}" != yes ]; then PLAN_ARGS=(--json --all --no-merges); fi
READS=(runs-ls plan-status graph)

if [ "$DRY_RUN" = yes ]; then
  printf '\nCHECK 871 PLAN\n'
  printf 'SCRATCH   %s (a throwaway installation that holds no run)\n' "$PROJECT"
  printf 'CHECKOUT  %s\n' "$CHECKOUT"
  printf 'RUNS      %d synthetic runs, %d objects, seeded through the history job reindex path\n' "$RUNS" "$((RUNS * 3))"
  printf 'STEPS\n'
  printf '  1  read %s'"'"'s config, mint a token, refuse a project that holds a run\n' "$PROJECT"
  printf '  2  read the storage slug%s, then the plan tasks from (cd %s && %s plan status %s --project %s)\n' \
    "$([ -n "$SLUG" ] && printf ' (%s, given)' "$SLUG" || printf ' from %s image status --json' "$FUGARO")" \
    "$CHECKOUT" "$FUGARO" "${PLAN_ARGS[*]}" "$PROJECT"
  printf '  3  seed %d runs, then %s runs reindex --project %s and wait up to %d s for the walk\n' "$RUNS" "$FUGARO" "$PROJECT" "$WAIT_SECONDS"
  printf '  4  warm then %d x  %s runs ls --json --project %s\n' "$REPEATS" "$FUGARO" "$PROJECT"
  printf '  5  warm then %d x  (cd %s && %s plan status %s --project %s)\n' "$REPEATS" "$CHECKOUT" "$FUGARO" "${PLAN_ARGS[*]}" "$PROJECT"
  printf '  6  warm then %d x  %s graph --json --project %s\n' "$REPEATS" "$FUGARO" "$PROJECT"
  printf '  7  remove every object and index document it wrote, and reindex once more\n'
  printf 'Nothing was called and nothing was judged (--dry-run)\n'
  exit 0
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk871.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
: >"$FACTS"
: >"$SCRATCH/timing"
AUTH_CONF=$SCRATCH/auth.conf
CONFIG=""

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "${CHECK871_KEEP_SCRATCH:-}" ]; then
    say "kept the scratch directory at $SCRATCH (CHECK871_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  printf '\nCHECK 871 RESULT\n'
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-12s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 871 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  # The seed is removed even when the run stops early: what this check wrote
  # into the scratch project is its own to take back, and a stop between the
  # seed and its own cleanup step is exactly when a person is not watching.
  if [ "$SEEDED" = yes ] && [ "$CLEANED" = no ] && [ -z "${CHECK871_KEEP_CLOUD:-}" ]; then
    say "removing what this run seeded (the run is ending before its cleanup step)"
    if cloud_clean; then
      facts="${facts}"$'\n'"clean|PASS|the runs, the objects and the index documents this run seeded were removed on the way out; the summary is recomputed by the reindex the cleanup asked for"
      CLEANED=yes
    else
      facts="${facts}"$'\n'"clean|FAIL|the seeded runs and index documents could NOT all be removed, and they are in ${PROJECT} under ${SLUG}: remove them yourself (the runs are runs/${SLUG}/<run id>/ and the index documents are runIndex/<run id>)"
    fi
  fi
  if [ -n "${CHECK871_KEEP_CLOUD:-}" ] && [ "$SEEDED" = yes ]; then
    facts="${facts}"$'\n'"clean|INFO|the seeded runs and index documents are LEFT in ${PROJECT} under ${SLUG}, by CHECK871_KEEP_CLOUD: remove them yourself"
  fi
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the token in it, is gone"
  elif [ -n "${CHECK871_KEEP_SCRATCH:-}" ]; then
    facts="${facts}"$'\n'"restore|INFO|the scratch directory is kept at ${SCRATCH}, by CHECK871_KEEP_SCRATCH, and it holds an access token: remove it yourself"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}, and it holds an access token: remove it yourself"
  fi
  local v
  v=$(verdict "$facts")
  if [ -n "$ABORT" ] || [ "$REACHED_END" = no ]; then
    status=2
  elif [ "$v" = FAIL ]; then
    status=1
  elif [ "$v" = INCOMPLETE ]; then
    status=3
  else
    status=0
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

first_line() { head -1 "$1" 2>/dev/null | cut -c1-300 || true; }

now_ms() { python3 -c 'import time; print(int(time.time() * 1000))' </dev/null; }

now_iso() { python3 -c 'import time; print(time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))' </dev/null; }

run_call() { # run_call SECONDS CMD...
  local secs=$1
  shift
  perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$secs" "$@" </dev/null
}

# read_config prints the scratch project's own members as shell assignments.
# fugaro writes this file with yaml.v3's block style, so an indentation reader
# is enough and python3 needs no YAML module. The repositories are
# `repos:`, then one key per repository, then its own `workflows:` list; the
# first repository and its first workflow are what a synthetic run's own task
# names, because the storage slug and the plan join are keyed by both.
read_config() { # read_config <file>
  python3 - "$1" <<'PY'
import re, shlex, sys

def unquote(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"":
        return v[1:-1]
    return v

scalars = {}
repos = {}
order = []
in_repos = False
repos_indent = None
repo = None
last_key = ""
for raw in open(sys.argv[1], encoding="utf-8"):
    line = raw.rstrip("\n")
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    line = re.sub(r"\s+#.*$", "", line)
    ind = len(line) - len(line.lstrip(" "))
    body = line.strip()
    if body.startswith("- "):
        if in_repos and repo is not None and last_key == "workflows":
            repos[repo].append(unquote(body[2:].strip()))
        continue
    m = re.match(r"^([A-Za-z0-9_./-]+):(?:[ \t]+(.*))?$", body)
    if not m:
        continue
    key, value = m.group(1), (m.group(2) or "").strip()
    if in_repos:
        if value == "":
            if repo is None or ind == repos_indent:
                repo = unquote(key)
                if repo not in repos:
                    repos[repo] = []
                    order.append(repo)
                repos_indent = ind
            last_key = key
            continue
        last_key = key
        continue
    if value == "":
        if key == "repos":
            in_repos = True
            last_key = key
            continue
        last_key = key
        continue
    if ind == 0:
        scalars[key] = unquote(value)

name = scalars.get("name", "")
gcp = scalars.get("gcp_project", "")
bucket = scalars.get("runs_bucket", "")
bucket_url = scalars.get("bucket_url", "")
if not bucket and bucket_url:
    bucket = bucket_url.rstrip("/").rsplit("/", 1)[-1]
repo = order[0] if order else ""
workflows = repos.get(repo) or []
workflow = workflows[0] if workflows else ""
out = {
    "cfg_name": name,
    "cfg_gcp": gcp,
    "cfg_bucket": bucket,
    "cfg_repo": repo,
    "cfg_workflow": workflow,
    "cfg_repos": str(len(order)),
}
for key, value in out.items():
    print("%s=%s" % (key, shlex.quote(value)))
PY
}

fs_doc_url() { printf '%s/v1/projects/%s/databases/(default)/documents/%s' "$FIRESTORE_URL" "$GCP" "$1"; }

# summary_read reads the installation's one summary document into the named
# file and prints the two fields this check waits on as shell assignments.
# A project with no summary at all answers zero runs, which is what the
# freshness fence below expects of a scratch installation.
summary_read() { # summary_read <file>
  curl -sS -K "$AUTH_CONF" --max-time 120 "$(fs_doc_url runIndexMeta/summary)" >"$1" 2>"$1.err" || true
  python3 - "$1" <<'PY'
import json, shlex, sys

def value(v):
    if not isinstance(v, dict):
        return None
    for key in ("integerValue", "booleanValue", "stringValue", "doubleValue", "timestampValue"):
        if key in v:
            return v[key]
    if "mapValue" in v:
        return {k: value(x) for k, x in (v["mapValue"].get("fields") or {}).items()}
    return None

def emit(runs, complete, why):
    print("summary_runs=%d" % runs)
    print("summary_complete=%s" % ("yes" if complete else "no"))
    print("summary_why=%s" % shlex.quote(why))

try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except (OSError, ValueError):
    emit(0, False, "the summary document could not be read as JSON")
    raise SystemExit(0)
fields = doc.get("fields")
if fields is None:
    err = doc.get("error") if isinstance(doc.get("error"), dict) else {}
    code = err.get("code", doc.get("code"))
    if code == 404:
        emit(0, False, "")
    else:
        emit(0, False, str(err.get("message") or doc.get("message") or "the read answered no document"))
    raise SystemExit(0)
try:
    runs = int(value(fields.get("runs")) or 0)
except (TypeError, ValueError):
    runs = 0
complete = str(value(fields.get("complete"))).lower() == "true"
emit(runs, complete, "")
PY
}

# seed_python drives the seeder and the cleaner: one python3 process uploads
# every synthetic run's three objects, or removes them and their index
# documents. The access token and the whole of what it writes go in its own
# environment, never on an argv, and its threads are what make a million
# objects land in minutes rather than hours.
seed_python() { # seed_python seed|clean
  CHECK871_MODE=$1 \
    CHECK871_TOKEN=$TOKEN \
    CHECK871_GCS_URL=$GCS_URL \
    CHECK871_FIRESTORE_URL=$FIRESTORE_URL \
    CHECK871_GCP=$GCP \
    CHECK871_BUCKET=$BUCKET \
    CHECK871_SLUG=$SLUG \
    CHECK871_REPO=$REPO \
    CHECK871_WORKFLOW=$WORKFLOW \
    CHECK871_PLAN_TASKS=$PLAN_TASKS \
    CHECK871_RUNS=$RUNS \
    CHECK871_PARALLEL=$PARALLEL \
    CHECK871_BASE_EPOCH=$BASE_EPOCH \
    python3 - <<'PY'
import json, os, queue, sys, threading, time, urllib.error, urllib.parse, urllib.request

mode = os.environ["CHECK871_MODE"]
token = os.environ["CHECK871_TOKEN"]
gcs = os.environ["CHECK871_GCS_URL"].rstrip("/")
fs = os.environ["CHECK871_FIRESTORE_URL"].rstrip("/")
gcp = os.environ["CHECK871_GCP"]
bucket = os.environ["CHECK871_BUCKET"]
slug = os.environ["CHECK871_SLUG"]
repo = os.environ["CHECK871_REPO"]
workflow = os.environ["CHECK871_WORKFLOW"]
runs = int(os.environ["CHECK871_RUNS"])
workers = int(os.environ["CHECK871_PARALLEL"])
base = int(os.environ["CHECK871_BASE_EPOCH"])
plan_tasks = [t for t in os.environ.get("CHECK871_PLAN_TASKS", "").split(",") if t]
index_root = "projects/%s/databases/(default)/documents/runIndex" % gcp

def call(method, url, body=None, tolerate=()):
    data = body if body is None or isinstance(body, bytes) else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", "Bearer " + token)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=180) as resp:
            return resp.read()
    except urllib.error.HTTPError as exc:
        if exc.code in tolerate:
            return b""
        raise RuntimeError("%s %s: HTTP %d %s" % (
            method, url.split("?")[0][:160], exc.code, exc.read()[:200].decode("utf-8", "replace")))

def object_url(rid, name):
    quoted = urllib.parse.quote("runs/%s/%s/%s" % (slug, rid, name), safe="")
    return "%s/storage/v1/b/%s/o/%s" % (gcs, urllib.parse.quote(bucket, safe=""), quoted)

def upload(rid, name, doc):
    quoted = urllib.parse.quote("runs/%s/%s/%s" % (slug, rid, name), safe="")
    url = "%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s" % (
        gcs, urllib.parse.quote(bucket, safe=""), quoted)
    call("POST", url, json.dumps(doc).encode())

def run_id(i):
    stamp = time.strftime("%Y%m%d-%H%M%S", time.gmtime(base + i))
    return "%s-%04x" % (stamp, i & 0xFFFF)

def documents(i):
    rid = run_id(i)
    started = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(base + i))
    finished = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(base + i + 60))
    task = {"version": 1, "run_id": rid, "repo": repo, "ref": "main",
            "title": "Synthetic run %d of the run index scale check" % i,
            "workflow": workflow}
    launch = {"version": 1, "run_id": rid, "backend": "cloud-run", "launched_at": started,
              "execution": "projects/%s/locations/scale/executions/synthetic-%s" % (gcp, rid),
              "job": "%s-history" % gcp}
    if plan_tasks:
        launch["plan_task"] = plan_tasks[i % len(plan_tasks)]
    record = {"version": 1, "run_id": rid, "repo": repo, "workflow": workflow,
              "status": "succeeded", "stage": "finalize", "outcome": "none",
              "cost_usd": 0.0, "started_at": started, "finished_at": finished}
    return rid, [("task.json", task), ("launch.json", launch), ("result.json", record)]

def spread(work, label):
    q = queue.Queue()
    for item in range(runs):
        q.put(item)
    lock = threading.Lock()
    state = {"done": 0, "error": None}

    def worker():
        while True:
            if state["error"] is not None:
                return
            try:
                i = q.get_nowait()
            except queue.Empty:
                return
            try:
                work(i)
            except Exception as exc:
                with lock:
                    if state["error"] is None:
                        state["error"] = "run %d: %s" % (i, exc)
                return
            with lock:
                state["done"] += 1
                if state["done"] % 25000 == 0:
                    print("  %s %d of %d runs" % (label, state["done"], runs), file=sys.stderr)

    threads = [threading.Thread(target=worker) for _ in range(workers)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    if state["error"] is not None:
        raise SystemExit("the %s stopped: %s" % (label, state["error"]))

def seed(i):
    rid, docs = documents(i)
    for name, doc in docs:
        upload(rid, name, doc)

def clean(i):
    rid = run_id(i)
    for name in ("task.json", "launch.json", "result.json"):
        call("DELETE", object_url(rid, name), tolerate=(404,))

def commit_deletes():
    url = "%s/v1/projects/%s/databases/(default)/documents:commit" % (fs, gcp)
    for start in range(0, runs, 200):
        writes = [{"delete": "%s/%s" % (index_root, run_id(i))}
                  for i in range(start, min(start + 200, runs))]
        call("POST", url, {"writes": writes})

started = time.time()
if mode == "seed":
    spread(seed, "seeded")
    print("seeded %d runs (%d objects) into %s under runs/%s/ in %.0f s"
          % (runs, runs * 3, bucket, slug, time.time() - started))
else:
    spread(clean, "removed")
    commit_deletes()
    print("removed %d objects and %d index documents from %s in %.0f s"
          % (runs * 3, runs, bucket, time.time() - started))
PY
}

# cloud_clean takes back everything this check wrote. The index has no prune --
# a fold never removes an entry -- so removing the runs is not enough: the
# entries are deleted too, and `fugaro runs reindex` is asked once more so the
# summary is recomputed over an index that no longer holds them. The summary's
# own figure is the check that it landed: a walk that reached the end of
# history and recomputed zero runs is an index this check left as it found it.
# It is called from the last step and from the exit trap, so it never stops the
# run: it answers whether it removed what it wrote.
cloud_clean() {
  local round=1 rc left
  while [ "$round" -le "$CLEAN_ROUNDS" ]; do
    rc=0
    if [ "$round" -eq 1 ]; then
      say "removing the seeded objects and index documents from ${PROJECT}"
    else
      say "removing them again (round ${round} of ${CLEAN_ROUNDS})"
    fi
    seed_python clean >>"$SCRATCH/clean.out" 2>>"$SCRATCH/clean.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
      say "  the cleanup did not finish: $(first_line "$SCRATCH/clean.err")"
      return 1
    fi
    run_call "$CALL_SECONDS" "$FUGARO" runs reindex --project "$PROJECT" \
      >>"$SCRATCH/clean.out" 2>>"$SCRATCH/clean.err" || say "  the reindex the cleanup asks for did not answer"
    local deadline=$(( $(date +%s) + CLEAN_WAIT_SECONDS ))
    while :; do
      eval "$(summary_read "$SCRATCH/summary.json")" || true
      if [ "${summary_complete:-no}" = yes ] && [ "${summary_runs:-1}" = 0 ]; then break; fi
      if [ "$(date +%s)" -ge "$deadline" ]; then break; fi
      sleep "$POLL_SECONDS"
    done
    if [ "${summary_complete:-no}" = yes ] && [ "${summary_runs:-1}" = 0 ]; then
      printf '%s\n' "$(first_line "$SCRATCH/clean.out")" >"$SCRATCH/clean.removed"
      return 0
    fi
    left=${summary_runs:-unknown}
    say "  the index still reports ${left} run(s) after the cleanup"
    round=$((round + 1))
  done
  return 1
}

# The scratch project's config is read from the installation's own config
# directory, the file `fugaro setup` wrote: this check is pointed at a
# throwaway installation of the owner's, so its name, GCP project and runs
# bucket are read from there rather than typed again on the command line.
CONFIG_DIR=${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects
case "$CONFIG_DIR" in
/*) ;;
*) stop "XDG_CONFIG_HOME is '${XDG_CONFIG_HOME:-}', which is not an absolute path, so this check cannot find the installation's config: unset it, or give it an absolute path" ;;
esac
CONFIG=$CONFIG_DIR/$PROJECT.yaml
[ -f "$CONFIG" ] || stop "the project ${PROJECT} has no config at ${CONFIG}: this check reads the project's own GCP project and runs bucket from the installation's own config, so it is pointed at an installation of yours"
cfg=$(read_config "$CONFIG") || stop "the config at ${CONFIG} could not be read as YAML"
eval "$cfg"
GCP=$cfg_gcp
BUCKET=$cfg_bucket
REPO=$cfg_repo
WORKFLOW=$cfg_workflow
[ -n "$GCP" ] || stop "the config at ${CONFIG} names no gcp_project"
[ -n "$BUCKET" ] || stop "the config at ${CONFIG} names no runs_bucket"
[ -n "$REPO" ] || stop "the config at ${CONFIG} names no repository under repos:, and every synthetic run's own task names one: the slug the runs are seeded under and the plan join are both keyed by it"
[ -n "$WORKFLOW" ] || WORKFLOW=default
guard_belong "project's own config" "$cfg_name" "$GCP" "$BUCKET" "$REPO"
fact belong PASS "neither the scratch project (${PROJECT}) nor its own config's members (${cfg_name}, ${GCP}, ${BUCKET}, ${REPO}) name the owner's own installation: nothing this check was given touches belong"
fact config PASS "${PROJECT} is ${cfg_name}, its GCP project is ${GCP}, its runs bucket is ${BUCKET}, and its config names ${cfg_repos} repository(ies): the first, ${REPO}, with the workflow ${WORKFLOW}"

say "minting an access token for ${GCP}"
token_rc=0
TOKEN=$(run_call "$CALL_SECONDS" gcloud auth print-access-token --quiet 2>"$SCRATCH/token.err") || token_rc=$?
if [ "$token_rc" -ne 0 ]; then
  say "gcloud auth print-access-token failed: $(first_line "$SCRATCH/token.err")"
  stop "no access token could be minted for ${GCP}: run gcloud auth login, and make sure the account can read ${BUCKET}"
fi
TOKEN=$(printf '%s' "$TOKEN" | tr -d '[:space:]')
[ -n "$TOKEN" ] || stop "gcloud answered no access token for ${GCP}: run gcloud auth login, and make sure the account can read ${BUCKET}"
umask 077
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" >"$AUTH_CONF"
chmod 600 "$AUTH_CONF"
fact token PASS "an access token for ${GCP} was minted into the scratch directory (0600) and is passed to python3 in its own environment: it is on no argv, in no log and in no output of this check"

say "checking that ${BUCKET} holds no run"
curl -sS -K "$AUTH_CONF" --max-time 120 \
  "$GCS_URL/storage/v1/b/${BUCKET}/o?delimiter=%2F&prefix=runs%2F&maxResults=1000&fields=prefixes%2Citems%2Fname" \
  >"$SCRATCH/fresh.json" 2>"$SCRATCH/fresh.err" ||
  stop "the runs bucket ${BUCKET} could not be listed: $(first_line "$SCRATCH/fresh.err")"
eval "$(python3 - "$SCRATCH/fresh.json" <<'PY'
import json, shlex, sys

try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except (OSError, ValueError):
    print("fresh_runs=1")
    print("fresh_why=" + shlex.quote("the listing could not be read as JSON"))
    raise SystemExit(0)
if isinstance(doc.get("error"), dict):
    print("fresh_runs=1")
    print("fresh_why=" + shlex.quote("the listing answered: " + str(doc["error"].get("message"))[:200]))
    raise SystemExit(0)
print("fresh_runs=%d" % (len(doc.get("prefixes") or []) + len(doc.get("items") or [])))
print("fresh_why=")
PY
)"
[ -z "${fresh_why:-}" ] || stop "the runs bucket ${BUCKET} could not be listed: ${fresh_why}"
[ "${fresh_runs:-0}" = 0 ] ||
  stop "${BUCKET} holds ${fresh_runs} run(s), and this check seeds, times and removes a project that holds none: point it at a throwaway installation"
eval "$(summary_read "$SCRATCH/summary.json")" || true
[ -z "${summary_why:-}" ] || stop "the index of ${PROJECT} could not be read: ${summary_why}"
[ "${summary_runs:-0}" = 0 ] ||
  stop "the index of ${PROJECT} already carries ${summary_runs} run(s), and this check removes exactly what it seeds: point it at a throwaway installation"
fact fresh PASS "the runs bucket ${BUCKET} holds no run and the index carries none: what this check writes into them is its own to remove, and nothing else is in its way"

if [ -z "$SLUG" ]; then
  say "reading the storage slug the product computes for ${REPO}"
  image_rc=0
  run_call "$CALL_SECONDS" "$FUGARO" image status --json --project "$PROJECT" >"$SCRATCH/image.out" 2>"$SCRATCH/image.err" || image_rc=$?
  if [ "$image_rc" -ne 0 ]; then
    say "fugaro image status --json failed: $(first_line "$SCRATCH/image.err")"
    stop "the storage slug of ${REPO} could not be read from the product, and this check does not compute it itself: run ${FUGARO} image status --json --project ${PROJECT} and give the slug it prints with --slug"
  fi
  SLUG=$(python3 - "$SCRATCH/image.out" "$REPO" "$WORKFLOW" <<'PY'
import json, sys

try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except (OSError, ValueError):
    raise SystemExit(0)
repo, workflow = sys.argv[2], sys.argv[3]
for row in doc.get("workflows") or []:
    if row.get("repo") == repo and row.get("workflow") == workflow and row.get("slug"):
        print(row["slug"])
        break
PY
)
  [ -n "$SLUG" ] || stop "fugaro image status --json named no slug for ${REPO} ${WORKFLOW}, and this check does not compute it itself: run it yourself and give the slug with --slug"
  fact slug PASS "the seeded runs go under runs/${SLUG}/, the slug the product itself computed for ${REPO} (${FUGARO} image status --json): this check never builds one of its own"
else
  fact slug PASS "the seeded runs go under runs/${SLUG}/, the slug this check was given with --slug"
fi
printf '%s' "$SLUG" | grep -Eq '^[a-z0-9-]+$' || stop "'${SLUG}' is not a storage slug"

plan_status_call() { # plan_status_call <stdout> <stderr>
  (cd "$CHECKOUT" && exec perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$CALL_SECONDS" \
    "$FUGARO" plan status "${PLAN_ARGS[@]}" --project "$PROJECT") >"$1" 2>"$2"
}

# sample TAG reads TAG once: the read itself, its stdout into the scratch and
# its wall clock into the timing file, under the sample name in $SAMPLE.
# `plan status` is run inside the checkout, because it reads the directory it is
# run in.
sample() { # sample TAG
  local tag=$1 rc=0 started
  started=$(now_ms)
  case "$tag" in
  runs-ls)
    run_call "$CALL_SECONDS" "$FUGARO" runs ls --json --project "$PROJECT" \
      >"$SCRATCH/$tag-$SAMPLE.out" 2>"$SCRATCH/$tag-$SAMPLE.err" || rc=$?
    ;;
  plan-status)
    plan_status_call "$SCRATCH/$tag-$SAMPLE.out" "$SCRATCH/$tag-$SAMPLE.err" || rc=$?
    ;;
  graph)
    run_call "$CALL_SECONDS" "$FUGARO" graph --json --project "$PROJECT" \
      >"$SCRATCH/$tag-$SAMPLE.out" 2>"$SCRATCH/$tag-$SAMPLE.err" || rc=$?
    ;;
  esac
  printf '%s %s %s %s\n' "$tag" "$SAMPLE" "$(( $(now_ms) - started ))" "$rc" >>"$SCRATCH/timing"
  return 0
}

say "reading the plan tasks of ${CHECKOUT}"
plan_status_call "$SCRATCH/plan.out" "$SCRATCH/plan.err" || true
PLAN_TASKS=$(python3 - "$SCRATCH/plan.out" "$TASK_FANOUT" <<'PY'
import json, sys

try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except (OSError, ValueError):
    raise SystemExit(0)
limit = int(sys.argv[2])
tasks = []
for plan in doc.get("plans") or []:
    pid = plan.get("id") or ""
    for node in plan.get("nodes") or []:
        if pid and node.get("id"):
            tasks.append("%s/%s" % (pid, node["id"]))
        if len(tasks) >= limit:
            break
    if len(tasks) >= limit:
        break
print(",".join(tasks))
PY
)
[ -n "$PLAN_TASKS" ] ||
  stop "the checkout ${CHECKOUT} carries no plan task (${FUGARO} plan status named none), and every seeded launch claims one of this checkout's own so that the join reads entries that really join: run this check from a checkout with plans, or name one with --checkout"
fact plans PASS "the plan join asks the index about $(printf '%s' "$PLAN_TASKS" | tr ',' '\n' | grep -c .) of ${CHECKOUT}'s own plan tasks (${PLAN_TASKS}), and every seeded launch claims one of them, so the join reads the seeded entries rather than answering nothing"

BASE_EPOCH=$(( $(date +%s) - RUNS ))
say "seeding ${RUNS} runs (${RUNS} x 3 objects) into ${BUCKET} under runs/${SLUG}/: this is the long step"
SEEDED=yes
seed_rc=0
seed_python seed >"$SCRATCH/seed.out" 2>"$SCRATCH/seed.err" || seed_rc=$?
if [ "$seed_rc" -ne 0 ]; then
  fact seed FAIL "the seed stopped: $(first_line "$SCRATCH/seed.err")"
  stop "the seed did not land in ${BUCKET}, so there is nothing at 100x to time"
fi
fact seed PASS "$(first_line "$SCRATCH/seed.out")"

say "marking the index for a rebuild, so the history job walks the seed into it"
reindex_rc=0
run_call "$CALL_SECONDS" "$FUGARO" runs reindex --project "$PROJECT" >"$SCRATCH/reindex.out" 2>"$SCRATCH/reindex.err" || reindex_rc=$?
if [ "$reindex_rc" -ne 0 ]; then
  fact reindex FAIL "fugaro runs reindex exited ${reindex_rc}: $(first_line "$SCRATCH/reindex.err")"
  stop "the index could not be marked for a rebuild, so nothing would walk the ${RUNS} seeded runs into it"
fi
fact reindex PASS "$(first_line "$SCRATCH/reindex.out")"
[ -s "$SCRATCH/reindex.err" ] && say "  $(first_line "$SCRATCH/reindex.err")"

walk_started=$(date +%s)
say "waiting up to ${WAIT_SECONDS} s for the walk to reach the end of history"
deadline=$(( walk_started + WAIT_SECONDS ))
walked=no
while :; do
  eval "$(summary_read "$SCRATCH/summary.json")" || true
  if [ "${summary_complete:-no}" = yes ]; then
    walked=yes
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then break; fi
  say "  the walk has folded ${summary_runs:-0} of ${RUNS} runs"
  sleep "$POLL_SECONDS"
done
if [ "$walked" = yes ]; then
  fact walk PASS "the walk reached the end of history after $(( $(date +%s) - walk_started )) s, and the summary carries ${summary_runs:-0} runs"
else
  fact walk UNVERIFIED "the walk had not reached the end of history after ${WAIT_SECONDS} s (the summary carries ${summary_runs:-0} of ${RUNS} runs), so the figures below would be of an index that is still building. The walk is resumable and every later sweep of the history job continues it: wait for the end of history, then run this check again"
fi

say "timing each read once warm, then ${REPEATS} times, against ${BOUND_MS} ms"
for tag in "${READS[@]}"; do
  SAMPLE=warm
  say "  warming $tag"
  sample "$tag"
  i=1
  while [ "$i" -le "$REPEATS" ]; do
    SAMPLE=$i
    say "  timing $tag ($i/$REPEATS)"
    sample "$tag"
    i=$((i + 1))
  done
done

say "reading the documents the timed calls answered"
python3 - "$SCRATCH" "$BOUND_MS" "$REPEATS" "$CHECKOUT" "$RUNS" "$walked" >"$SCRATCH/analysis" 2>"$SCRATCH/analysis.err" <<'PY' ||
import json, os, sys

scratch, bound_ms, repeats, checkout = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]
expected, walked = int(sys.argv[5]), sys.argv[6] == "yes"

def fact(key, state, detail):
    detail = " ".join(str(detail).split()).replace("|", "/")
    print("fact|%s|%s|%s" % (key, state, detail))

def read(name):
    try:
        with open(os.path.join(scratch, name), encoding="utf-8") as f:
            return f.read()
    except OSError:
        return ""

def first_line(name):
    lines = read(name).strip().splitlines()
    return lines[0][:300] if lines else ""

def doc(name):
    try:
        return json.loads(read(name))
    except ValueError:
        return None

timing = {}
for line in read("timing").splitlines():
    parts = line.split()
    if len(parts) == 4:
        timing.setdefault(parts[0], {})[parts[1]] = (int(parts[2]), int(parts[3]))

def median(values):
    ordered = sorted(values)
    middle = len(ordered) // 2
    if len(ordered) % 2:
        return ordered[middle]
    return (ordered[middle - 1] + ordered[middle]) // 2

def failure(tag, sample):
    ms, rc = timing[tag][sample]
    return "the %s read exited %d after %d ms (CHECK871_CALL_SECONDS bounds it): %s" % (
        sample, rc, ms, first_line("%s-%s.err" % (tag, sample)))

def judged(name, tag):
    samples = timing.get(tag) or {}
    warm = samples.get("warm")
    timed = [samples[str(i)] for i in range(1, repeats + 1) if str(i) in samples]
    if warm is None or len(timed) != repeats:
        fact(name, "UNVERIFIED", "the read was not taken enough times to judge (%d of %d samples): nothing was measured" % (len(timed), repeats))
        return None
    for sample in ["warm"] + [str(i) for i in range(1, repeats + 1)]:
        if samples[sample][1] != 0:
            fact(name, "FAIL", failure(tag, sample))
            return None
    ms = [s[0] for s in timed]
    figure = median(ms)
    detail = "warm median %d ms, samples %s (warm-up %d ms)" % (figure, ", ".join("%d" % m for m in ms), warm[0])
    if figure >= bound_ms:
        fact(name, "FAIL", "at or over the %d ms bound: %s" % (bound_ms, detail))
    else:
        fact(name, "PASS", "under the %d ms bound: %s" % (bound_ms, detail))
    return figure

runs = doc("runs-ls-warm.out")
scale = None
if runs is None:
    fact("runs-ls", "FAIL", "`fugaro runs ls --json` answered no document this check could read: %s"
         % (first_line("runs-ls-warm.err") or first_line("runs-ls-warm.out")))
else:
    totals = runs.get("totals") or {}
    scale = totals.get("runs")
    rows = runs.get("runs")
    if scale is None:
        scale = len(rows) if isinstance(rows, list) else None
    judged("runs-ls", "runs-ls")
    if isinstance(rows, list):
        fact("listing", "INFO", "%d rows in the page, %s runs in the read's own totals, from %s"
             % (len(rows), scale if scale is not None else "an unknown number of",
                "the index" if "index_complete" in runs else "the window (this listing read no index)"))

plan = doc("plan-status-warm.out")
if plan is None:
    fact("plan-status", "FAIL", "`fugaro plan status --json --all` answered no document this check could read: %s"
         % (first_line("plan-status-warm.err") or first_line("plan-status-warm.out")))
elif not plan.get("repo"):
    fact("plan-status", "UNVERIFIED", "`fugaro plan status --json --all` read %s and answered no repository for it, so it joined no run at all and its figure would measure nothing. Run this check from a checkout of the repository, or name it with --checkout" % checkout)
    fact("join", "UNVERIFIED", "no run was joined, so the plan join's figure is not a reading of the index")
else:
    totals = plan.get("totals") or {}
    fact("join", "INFO", "%s: %s plans, %s tasks (%s open, %s closed) read from %s, and one query per task asks the index for its runs"
         % (plan.get("repo"), totals.get("plans", 0), totals.get("tasks", 0),
            totals.get("open", 0), totals.get("closed", 0), checkout))
    judged("plan-status", "plan-status")

graph = doc("graph-warm.out")
graph_err = (first_line("graph-warm.err") or first_line("graph-warm.out"))
if graph is None and "Subscription Edition" in graph_err:
    fact("graph", "UNVERIFIED", "this build answers `graph` with the Subscription Edition stub (%s), so nothing here can time it: run this check with the released binary" % graph_err)
elif graph is None:
    fact("graph", "FAIL", "`fugaro graph --json` answered no document this check could read: %s" % graph_err)
else:
    sources = graph.get("sources")
    warnings = graph.get("warnings") or []
    fact("scopes", "INFO", "%d scope(s) in the graph document%s"
         % (len(sources) if isinstance(sources, list) else 0,
            ", and %d warning(s), the first: %s" % (len(warnings), str(warnings[0])[:160]) if warnings else ""))
    judged("graph", "graph")

if runs is not None:
    if "index_complete" not in runs:
        fact("index", "FAIL", "the listing read the window rather than the index, so the seeded runs are not in an index at all")
    elif runs.get("index_complete"):
        fact("index", "PASS", "the index has reached the end of history, folded through %s"
             % (runs.get("folded_through") or "an unrecorded moment"))
    else:
        fact("index", "INFO", "the index is still building (index_complete false%s): a figure taken now is of what it has folded"
             % (", folded through %s" % runs["folded_through"] if runs.get("folded_through") else ""))
if plan is not None and not plan.get("index_complete", True):
    fact("plan-index", "INFO", "the plan join read an index that is still building, so its figure is of what the index has folded")

if not walked:
    fact("scale", "UNVERIFIED", "the walk had not reached the end of history, so what these figures cover is not the whole seed")
elif scale is None:
    fact("scale", "UNVERIFIED", "the reads carried no figure for the index, so nothing here says what they cover")
elif scale != expected:
    fact("scale", "FAIL", "the index carries %s runs where %s were seeded, so the figures are not a reading of the seed" % (scale, expected))
else:
    fact("scale", "PASS", "the index carries the %s runs this check seeded, and no other run: the figures are of an installation at that scale" % scale)
PY
  stop "the documents could not be read: $(first_line "$SCRATCH/analysis.err")"

while IFS='|' read -r tag k state detail; do
  case "$tag" in
  fact) fact "$k" "$state" "$detail" ;;
  esac
done <"$SCRATCH/analysis"

if [ -n "${CHECK871_KEEP_CLOUD:-}" ]; then
  fact clean INFO "the seeded runs and index documents are LEFT in ${PROJECT} under ${SLUG}, by CHECK871_KEEP_CLOUD: remove them yourself, the runs are runs/${SLUG}/<run id>/ and the index documents are runIndex/<run id>"
  CLEANED=yes
elif cloud_clean; then
  fact clean PASS "$(first_line "$SCRATCH/clean.removed")"
  CLEANED=yes
else
  fact clean FAIL "the seeded runs and index documents could NOT all be removed, and they are in ${PROJECT} under ${SLUG}: remove them yourself, the runs are runs/${SLUG}/<run id>/ and the index documents are runIndex/<run id>"
fi

REACHED_END=yes
