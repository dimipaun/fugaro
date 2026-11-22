#!/usr/bin/env bash
# Check 883 — a run pauses and the resident orchestrator resumes it on the
# original launcher's pool. Run by YOU, after release 1 is installed.
#
# The `pause-resume` slice makes a run that the credentials service cannot serve
# suspend itself: the service answers that every source is exhausted, decides
# (its own answer, never the job's) that the right action is `pause`, computes a
# resume time and writes the node under `paused/` with the launcher and the
# named pool out of its own record of the run's grant (R8, R19, D3185). The
# resident orchestrator reissues it when its time comes, as a new run in the
# same chain whose task names `resumes` the paused one, and D3191 binds that
# resumed run to the ORIGINAL launcher's pool. Nothing offline settles any of
# that: the service's decision, the orchestrator's tick and the pool a real
# grant resolves to are the live installation's. So this check does its own
# setup — ONE real run, launched on the pool YOU name — and reads, row by row:
#
#   premise     the installation has a resident orchestrator (`fugaro doctor
#               --json`, its own `orchestrator-identity` row): a project without
#               one never pauses at all (D3186), so nothing below could be
#               measured. Its published policy carries the `pause` block (the
#               wait in the job, the minimum and the maximum pause — the
#               settings that decide how long this check waits), and the pool
#               the launch will name is one the installation defines. A premise
#               that does not hold is a STOP (exit 2), not a finding: the rows
#               below would be about something else.
#   launch      ONE real run of the repository, with the task this check writes,
#               launched with `fugaro run --pool <name> --project <name>`, whose
#               document names the run. It is a real run on a real repository: it
#               spends model money under the repository's and the installation's
#               own caps and may open a pull request. A dry run commits none of
#               it.
#   pause       that run's row in `fugaro runs ls --json` reads `paused` within
#               the bound this check waits (the policy's own minimum pause plus
#               room): the run suspended itself rather than failing or running
#               to its end. A run that ended another way, or that was still
#               running when the bound passed, leaves this row UNVERIFIED naming
#               what it read — a source nothing exhausted never pauses, and that
#               is the state of the pool, not a defect of the release.
#   resume      a member of the PAUSED run's own row names the run that resumed
#               it (the design's place for it, plan C3), or a row that names the
#               paused run in a member about continuing (`previous_run`,
#               `continues`, `resumes`) is the resumed run itself — the
#               orchestrator's reissue under either spelling. UNVERIFIED when the
#               bound passes with no such row, naming what the rows do carry. The
#               resume TIME is a member of the same row and its name mentions
#               resuming too (`resume_at`), so a name that mentions a time is the
#               time, never the resumer; and a member of some other row that
#               names a third run (a follow-up's own `previous_run`) is not read
#               either: the rows are anchored on the paused run, not on the word.
#   pool        the RESUMED run's own record — read with `gcloud storage cat`
#               from the runs bucket, off its stages' `source` — was served from
#               the pool the launch named and of the kind `launcher-pool`:
#               D3191's binding. A resumed run whose source is another pool, or
#               a plain pool, is a FAIL; a record that carries no stage source
#               at all is UNVERIFIED.
#   restore     the check's own scratch directory is gone.
#
# Every row is a read except the launch, and the launch is ONE run: no other
# call is billable, and none changes anything. Nothing is written outside the
# check's own scratch directory, and it removes it.
#
# Usage:      bash scripts/live-checks/check-883-pause-resume.sh \
#               --repo <owner/name|path> --pool <name> [--installation <name>] \
#               [--bucket <name>] [--task-file <file>] [--poll-seconds N] \
#               [--pause-wait N] [--resume-wait N] [--dry-run]
#             --dry-run        prints the plan and calls NOTHING: no fugaro, no
#                              gcloud, no run is launched.
#             --repo           the repository to run in: owner/name
#                              (`fugaro run --repo` takes that from anywhere) or
#                              a path inside a checkout.
#             --pool           the pool the launch names, and the pool the
#                              resumed run must have been served from. Use one
#                              you are entitled to and whose members are out of
#                              room, or the run finishes instead of pausing.
#             --installation   the installation's Fugaro project name; read from
#                              `fugaro doctor --json` when it is not given. When
#                              it IS given, doctor is asked for that project and
#                              the launch and every listing name it too, so the
#                              whole check is about that installation and not
#                              about whichever one the checkout resolves.
#             --bucket         the runs bucket; `fugaro-runs-<gcp_project>` when
#                              it is not given.
#             --task-file      the task to launch with; this check writes one in
#                              its own scratch directory when it is not given.
#             --poll-seconds   how often to read `fugaro runs ls` (default 20).
#             --pause-wait     seconds to wait for the run to read `paused`
#                              (default 1800).
#             --resume-wait    seconds to wait for a row naming the resumed run
#                              (default: the policy's minimum pause plus 900).
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), fugaro, gcloud (only for the resumed run's own record).
# Costs:      ONE real run of the repository you name — its model spend, and a
#             branch and a pull request if its task asks for one — under the
#             repository's and the installation's own caps. Every other call is
#             a read. A dry run commits none of it.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-883-pause-resume_test.sh) drives it
#             against fake `fugaro` and `gcloud` on PATH, so no case of the test
#             launches anything.
# Cleanup:    its scratch directory (the task file included) is removed from the
#             exit trap, and the `restore` row says whether it really went. The
#             run it launched is NOT cancelled: it is a real run of your
#             repository and it ends on its own, and the row names it so you can
#             cancel it yourself if you want to.
# Output:     progress on stderr, ONE block on stdout headed CHECK 883 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 883 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 883 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 883 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 883 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# NO secret is read at all, and none may be printed: the check reads documents
# that name runs, pools, settings and states, it prints run ids, pool names,
# statuses and bucket paths, and it never prints a token, a key or an account's
# value.
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
GCP_PROJECT=""
BUCKET_GIVEN=""
RUN_REF=""
RUN_ID=""
RUN_SLUG=""
RESUMED_ID=""
RESUMED_REF=""
MIN_PAUSE_SECONDS=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real installation with it"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 883: it launches a REAL run of your repository. Run it yourself: bash scripts/live-checks/check-883-pause-resume.sh --repo <owner/name> --pool <name>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-883-pause-resume.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 883 — a run pauses and the resident orchestrator resumes it on the
original launcher's pool. Run by YOU, after release 1 is installed.

Usage:
  bash scripts/live-checks/check-883-pause-resume.sh \
    --repo <owner/name|path> --pool <name> [--installation <name>] \
    [--bucket <name>] [--task-file <file>] [--poll-seconds N] \
    [--pause-wait N] [--resume-wait N] [--dry-run]
  bash scripts/live-checks/check-883-pause-resume.sh --help

It launches ONE real run of the repository on the pool you name, waits for its
row in `fugaro runs ls --json` to read `paused`, waits for a row naming the run
that resumed it, and reads that resumed run's own record out of the runs bucket
to see which pool served it. Every row it prints is one criterion of the
register's Check 883; the last line of the block is the verdict and the exit
status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

REPO=${CHECK883_REPO:-}
POOL=${CHECK883_POOL:-}
INSTALLATION=${CHECK883_INSTALLATION:-}
BUCKET=${CHECK883_BUCKET:-}
TASK_FILE=${CHECK883_TASK_FILE:-}
POLL_SECONDS=${CHECK883_POLL_SECONDS:-20}
PAUSE_WAIT=${CHECK883_PAUSE_WAIT:-1800}
RESUME_WAIT=${CHECK883_RESUME_WAIT:-}
DRY_RUN=no
if [ "${CHECK883_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs the repository to run in (owner/name, or a path inside a checkout)"
    REPO=$2
    shift 2
    ;;
  --pool)
    [ $# -ge 2 ] || stop "--pool needs the pool the launch names"
    POOL=$2
    shift 2
    ;;
  --installation)
    [ $# -ge 2 ] || stop "--installation needs the installation's Fugaro project name"
    INSTALLATION=$2
    shift 2
    ;;
  --bucket)
    [ $# -ge 2 ] || stop "--bucket needs the runs bucket's name"
    BUCKET=$2
    BUCKET_GIVEN=$2
    shift 2
    ;;
  --task-file)
    [ $# -ge 2 ] || stop "--task-file needs the task to launch with"
    TASK_FILE=$2
    shift 2
    ;;
  --poll-seconds)
    [ $# -ge 2 ] || stop "--poll-seconds needs a number of seconds"
    POLL_SECONDS=$2
    shift 2
    ;;
  --pause-wait)
    [ $# -ge 2 ] || stop "--pause-wait needs a number of seconds"
    PAUSE_WAIT=$2
    shift 2
    ;;
  --resume-wait)
    [ $# -ge 2 ] || stop "--resume-wait needs a number of seconds"
    RESUME_WAIT=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check-883-pause-resume.sh --repo <owner/name> --pool <name>, --help, or --dry-run)"
    ;;
  esac
done

# A pool name is a name: the flag takes one the installation defines, and a
# value that could be a shell word, a path or an empty string is refused before
# anything is called.
case "$POOL" in
'') : ;;
*[!A-Za-z0-9._-]* | [!A-Za-z0-9]*) stop "\"$POOL\" is not a pool name: --pool takes the name of a pool the installation defines (letters, digits, dot, dash, underscore, starting with a letter or a digit). Read them with fugaro accounts pool ls" ;;
esac

dry_run_plan() {
  local where_repo where_pool where_install where_bucket where_task where_resume
  where_repo=$REPO
  [ -n "$where_repo" ] || where_repo='<not given>'
  where_pool=$POOL
  [ -n "$where_pool" ] || where_pool='<not given>'
  where_install=$INSTALLATION
  [ -n "$where_install" ] || where_install='<the name fugaro doctor --json prints>'
  where_bucket=$BUCKET
  [ -n "$where_bucket" ] || where_bucket='<fugaro-runs-<gcp_project>>'
  where_task=$TASK_FILE
  [ -n "$where_task" ] || where_task='<a task this check writes in its own scratch directory>'
  where_resume=$RESUME_WAIT
  [ -n "$where_resume" ] || where_resume='<the policy minimum pause plus 900>'
  cat <<PLAN
Check 883 plan (nothing is called: no fugaro, no gcloud, no run is launched)

  repository:    $where_repo
  pool:          $where_pool
  installation:  $where_install
  runs bucket:   $where_bucket
  task file:     $where_task
  poll seconds:  $POLL_SECONDS
  pause wait:    $PAUSE_WAIT seconds
  resume wait:   $where_resume seconds

What it would do, in order:

  premise   fugaro doctor --json [--project <installation>]   -> the installation must have a resident
            fugaro project policy show --json --project <name>   orchestrator (its orchestrator-identity row)
            fugaro accounts pool ls --project <name>             and a policy carrying the pause block,
                                                                 and the pool must be defined. Otherwise STOP.
  launch    fugaro run --repo $where_repo --pool $where_pool --task-file <file> --project <installation> --json
            -> ONE real run, on a real repository: it spends model money under the repository's and the
               installation's own caps and may open a pull request.
  pause     fugaro runs ls --repo $where_repo --project <installation> --json, polled -> the run's row must read 'paused'
  resume    fugaro runs ls --repo $where_repo --project <installation> --json, polled -> a row must name the run that resumed it
  pool      gcloud storage cat gs://<bucket>/runs/<slug>/<resumed run>/result.json
            -> the resumed run's stages must carry a source of kind launcher-pool on $where_pool
  restore   the check's own scratch directory is removed.

Nothing but the ONE launch is billable, no secret is read, and nothing outside the check's own scratch
directory is written.
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$REPO" ] || stop "no --repo was given (or --help for the usage)"
  [ -n "$POOL" ] || stop "no --pool was given (or --help for the usage)"
fi

guard_xtrace
guard_agent

if [ "$DRY_RUN" = yes ]; then
  dry_run_plan
  exit 0
fi

need bash
need mktemp
need python3
need fugaro
need gcloud

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk883.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk883-mine"
: >"$MARKER"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
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
  local facts=$1 status=$2
  printf '\nCHECK 883 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-10s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 883 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the release"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: the task this check wrote, every read's own output). The run this check launched was NOT cancelled — it is a real run of ${REPO:-the repository you named} and it ends on its own"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ---------------------------------------------------------------- the readers

# jget FILE PATH prints the value at a dotted PATH of a JSON document, or nothing
# when the document, the path or the key is not there. A path segment of the
# form NAME[] walks every element of an array named NAME.
jget() { # jget <file> <dotted.path>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
cur = doc
for seg in sys.argv[2].split("."):
    many = seg.endswith("[]")
    key = seg[:-2] if many else seg
    if key:
        if not isinstance(cur, dict) or key not in cur:
            sys.exit(0)
        cur = cur[key]
    if many:
        if not isinstance(cur, list):
            sys.exit(0)
        for item in cur:
            if isinstance(item, (str, int, float)) and not isinstance(item, bool):
                print(item)
        sys.exit(0)
if cur is None or isinstance(cur, bool):
    sys.exit(0)
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
else:
    print(cur)
PY
}

# json_ok FILE prints `yes` when the file is a JSON document this python3 can
# read, `no` otherwise.
json_ok() {
  python3 -c 'import json,sys
try:
    json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)' "$1" 2>/dev/null && printf 'yes' || printf 'no'
}

# check_row FILE ID prints one check of a `fugaro doctor --json` document as a
# single line: severity, problem and fix, tab separated. Nothing when the
# document carries no check with that id.
check_row() { # check_row <file> <id>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for c in doc.get("checks") or []:
    if c.get("id") == sys.argv[2]:
        print("%s\t%s\t%s" % (c.get("severity", ""), c.get("problem", ""), c.get("fix", "")))
        break
PY
}

# row_json FILE RUNID prints the `runs ls --json` row of one run as a single
# JSON line, and nothing when the document names no such row.
row_json() { # row_json <file> <run id>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for r in doc.get("runs") or []:
    if r.get("run_id") == sys.argv[2] or r.get("run") == sys.argv[2]:
        print(json.dumps(r))
        break
PY
}

# row_status FILE RUNID prints the status of that row, `none` when the document
# names no such run and `?` when it names one without a status.
row_status() { # row_status <file> <run id>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("?")
    sys.exit(0)
for r in doc.get("runs") or []:
    if r.get("run_id") == sys.argv[2] or r.get("run") == sys.argv[2]:
        print(r.get("status") or "?")
        sys.exit(0)
print("none")
PY
}

# resumed_by FILE [the paused run id] prints, as `member=value`, the run that
# resumed the paused one; the value is the RESUMED run's own id, because that is
# what the caller reads it as. The design fixes the member's place — "the run
# that resumed it" is a member of the PAUSED run's own row (plan C3) — and not
# its name, so the reading is anchored on the row and not on the word:
#
#   * on the paused run's own row, a member whose name mentions resuming
#     (`resumed_by`, `resumed_run`, `resumer`, ...) holds the run that resumed
#     it. The resume TIME is a member of the same row and its name mentions
#     resuming too — `resume_at` is the plan's own name for it (C3, the
#     exhausted answer's wire member) — so a name that mentions a time
#     (`_at`, `_time`, `_until`, ...) is the time, never the resumer: reading it
#     would PASS the resume row naming a clock reading and send the pool row
#     after a record no run has.
#   * a member about CONTINUING names the run this row's own run continues, the
#     opposite direction: `previous_run`, `continues`, and `resumes` — the plan
#     names the back-link `resumes` (C3's task member; PR4's reissue names the
#     paused run with it, VS3's reader takes it back to `pause.json`). On the
#     paused row such a member is the paused run's own predecessor and is not
#     read as the resumer.
#   * on any other row, only a member about continuing that is EQUAL to the
#     paused id links the two: that row IS the resumed run (`previous_run` or
#     `resumes` pointing back at the pause), and the run that resumed it is that
#     row's own run. A follow-up run's `previous_run` naming some third run says
#     nothing about this pause and is not read.
resumed_by() { # resumed_by <file> [the paused run id]
  python3 - "$1" "${2:-}" <<'PY'
import json, re, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)

CONTINUING = ("continues", "previous_run", "resumes")
TIME_WORDS = ("at", "time", "ts", "timestamp", "until", "since", "after",
              "before", "not_before", "deadline", "expires", "expiry", "eta",
              "date", "clock", "seconds", "minutes", "duration", "when")


def is_time(low):
    return any(w in TIME_WORDS for w in re.split(r"[^a-z0-9]+", low))


paused = sys.argv[2]
for r in doc.get("runs") or []:
    rid = r.get("run_id") or r.get("run") or ""
    on_paused_row = bool(paused) and paused in (r.get("run_id"), r.get("run"))
    for name, value in r.items():
        low = name.lower()
        if not value or isinstance(value, (dict, list)):
            continue
        continuing = low in CONTINUING
        resuming = "resum" in low and not continuing and not is_time(low)
        if not (resuming or continuing):
            continue
        if on_paused_row:
            if resuming and str(value) != paused:
                print("%s=%s" % (name, value))
                sys.exit(0)
            continue
        if continuing and paused and str(value) == paused and rid and rid != paused:
            print("run=%s" % rid)
            sys.exit(0)
PY
}

# source_of FILE prints, for a run record, the first stage source that names a
# source, as `kind=<kind> pool=<pool>`. It prints `none` when the record carries
# no stage source at all, and `no-pool` when every source it carries names a
# kind whose source is not a pool (a key, a provider key or Vertex).
#
# The name is read from `pool` first and from `name` second: C1 fixes the kind
# words (`launcher-pool`, `pool`, `anthropic`, `provider`, `vertex`), and the run
# record carries a stage's source as "kind and name, never a value" — a reader
# that insisted on one spelling of the name would call a record written with the
# other an unreadable one, and this row is about which pool served the run, not
# about which member holds its name.
source_of() { # source_of <record file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("unreadable")
    sys.exit(0)
seen = False
for st in doc.get("stages") or []:
    src = st.get("source")
    if isinstance(src, dict) and src:
        seen = True
        named = src.get("pool") or src.get("name")
        if named:
            print("kind=%s pool=%s" % (src.get("kind", "?"), named))
            sys.exit(0)
if seen:
    print("no-pool")
else:
    print("none")
PY
}

# seconds_of DURATION prints a Go duration ("30m", "6h", "1h30m", "90s") as
# whole seconds, and nothing when it is not one.
seconds_of() { # seconds_of <duration>
  python3 - "$1" <<'PY'
import re, sys
s = sys.argv[1].strip()
if not s:
    sys.exit(0)
total = 0.0
for num, unit in re.findall(r"([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h)", s):
    total += float(num) * {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3,
                           "s": 1, "m": 60, "h": 3600}[unit]
if total <= 0 or re.sub(r"([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h)", "", s).strip():
    sys.exit(0)
print(int(total))
PY
}

# pool_defined FILE NAME prints `yes` when the pool listing has a NAME column
# entry for NAME, `no` otherwise. `fugaro accounts pool ls` prints a table, and
# this reads the table's own first column.
pool_defined() { # pool_defined <file> <name>
  if awk -v want="$2" '$1 == want { found = 1 } END { exit !found }' "$1" 2>/dev/null; then
    printf 'yes'
  else
    printf 'no'
  fi
}

# ------------------------------------------------------------------- the check

step_premise() {
  local rc=0 first
  # doctor's own exit status is NOT the answer to which installation this is: it
  # writes its whole document and then exits 1 whenever any of its checks failed
  # (doctor.go's emitDoctor returns an ExitError for !o.OK, any check of default
  # severity), which is a separate reading this check does not make. Only an
  # answer that is not a document this check can read is no answer.
  local doctor_args=()
  if [ -n "$INSTALLATION" ]; then doctor_args=(--project "$INSTALLATION"); fi
  say "reading the installation: fugaro doctor --json${INSTALLATION:+ --project ${INSTALLATION}}"
  fugaro doctor --json ${doctor_args[@]+"${doctor_args[@]}"} >"$SCRATCH/doctor.out" 2>"$SCRATCH/doctor.err" || rc=$?
  if [ "$(json_ok "$SCRATCH/doctor.out")" = no ]; then
    first=$(head -1 "$SCRATCH/doctor.err" 2>/dev/null || true)
    fact premise FAIL "fugaro doctor --json did not answer with a document this check reads (exit ${rc})${first:+: ${first}}. The check is about the pause of a real installation, and doctor is how it says which installation and whether that installation has a resident orchestrator at all"
    stop "fugaro doctor --json did not answer"
  fi
  GCP_PROJECT=$(jget "$SCRATCH/doctor.out" project.gcp_project)
  local named
  named=$(jget "$SCRATCH/doctor.out" project.name)
  if [ -n "$INSTALLATION" ]; then
    if [ -n "$named" ] && [ "$named" != "$INSTALLATION" ]; then
      fact premise FAIL "'fugaro doctor --json --project ${INSTALLATION}' answered about the project '${named}' and not the installation this check was pointed at. Every row below reads the policy, the pools and the runs of the project doctor names, so a check that was pointed at ${INSTALLATION} and reads ${named} is measuring another installation. Point --installation at ${named}, or run the check where ${INSTALLATION} is the project fugaro resolves"
      stop "doctor answered about another project"
    fi
    PROJECT=$INSTALLATION
  fi
  [ -n "$PROJECT" ] || PROJECT=$named
  [ -n "$BUCKET" ] || BUCKET="fugaro-runs-${GCP_PROJECT}"
  if [ -z "$PROJECT" ] || [ -z "$GCP_PROJECT" ]; then
    fact premise FAIL "fugaro doctor --json named no project (name=${PROJECT:-none}, gcp_project=${GCP_PROJECT:-none}), so this check cannot say which installation, which policy or which runs bucket it is reading"
    stop "fugaro doctor --json named no project"
  fi

  local row severity problem
  row=$(check_row "$SCRATCH/doctor.out" orchestrator-identity)
  if [ -z "$row" ]; then
    fact premise FAIL "fugaro doctor --json carries no orchestrator-identity row, so whether ${PROJECT} has the resident orchestrator Check 883 is about is NOT measured (a configuration that reads no IAM answers no such row). The pause this check measures is resumed by the resident orchestrator alone (D3186). Run 'fugaro doctor' in your own terminal and read its orchestrator-identity line"
    stop "the installation's resident orchestrator could not be read"
  fi
  severity=$(printf '%s' "$row" | cut -f1)
  problem=$(printf '%s' "$row" | cut -f2)
  case "$problem" in
  *"has no resident orchestrator"*)
    fact premise FAIL "${PROJECT} has no resident orchestrator: doctor's own orchestrator-identity row says '${problem}'. A project without one never pauses: the job that would be suspended ends instead (D3186), so there is no pause for this check to measure and no orchestrator to resume it. Opt the installation in (re-apply its root with the orchestrator block on, then 'fugaro update'), or run this check against an installation that has one"
    stop "the installation has no resident orchestrator"
    ;;
  esac

  say "reading the pause settings: fugaro project policy show --json --project ${PROJECT}"
  rc=0
  fugaro project policy show --json --project "$PROJECT" >"$SCRATCH/policy.out" 2>"$SCRATCH/policy.err" || rc=$?
  if [ "$rc" -ne 0 ] || [ "$(json_ok "$SCRATCH/policy.out")" = no ]; then
    first=$(head -1 "$SCRATCH/policy.err" 2>/dev/null || true)
    fact premise FAIL "the published policy of ${PROJECT} could not be read (exit ${rc})${first:+: ${first}}, so whether this installation carries the pause settings is NOT measured, and neither is how long a pause of it lasts. Read it yourself with 'fugaro project policy show --json --project ${PROJECT}'"
    stop "the published policy could not be read"
  fi
  local min_pause max_pause wait_in_job
  min_pause=$(jget "$SCRATCH/policy.out" policy.pause.min_pause)
  max_pause=$(jget "$SCRATCH/policy.out" policy.pause.max_pause)
  wait_in_job=$(jget "$SCRATCH/policy.out" policy.pause.wait_in_job)
  if [ -z "$min_pause" ] && [ -z "$max_pause" ] && [ -z "$wait_in_job" ]; then
    fact premise FAIL "the published policy of ${PROJECT} carries no pause block (no min_pause, max_pause or wait_in_job), so this installation's release does not decide pauses and no run of it will be suspended for this check to measure. Publish the policy of the release that carries the pause-resume slice ('fugaro update' publishes it), or run this check against an installation that has it"
    stop "the published policy carries no pause settings"
  fi
  MIN_PAUSE_SECONDS=$(seconds_of "$min_pause" || true)
  if [ -z "$RESUME_WAIT" ]; then
    if [ -n "$MIN_PAUSE_SECONDS" ]; then
      RESUME_WAIT=$((MIN_PAUSE_SECONDS + 900))
    else
      RESUME_WAIT=3600
    fi
  fi

  say "reading the pool the launch will name: fugaro accounts pool ls --project ${PROJECT}"
  rc=0
  fugaro accounts pool ls --project "$PROJECT" >"$SCRATCH/pools.out" 2>"$SCRATCH/pools.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    first=$(head -1 "$SCRATCH/pools.err" 2>/dev/null || true)
    fact premise FAIL "the pools of ${PROJECT} could not be read (exit ${rc})${first:+: ${first}}, so whether the pool ${POOL} this check names exists is NOT measured. Read them yourself with 'fugaro accounts pool ls --project ${PROJECT}'"
    stop "the pools could not be read"
  fi
  if [ "$(pool_defined "$SCRATCH/pools.out" "$POOL")" = no ]; then
    fact premise FAIL "${PROJECT} does not define the pool ${POOL}: 'fugaro accounts pool ls' names no such pool, and a launch naming a pool the installation has not got is refused before any chain of it resolves (D3176). Its pools are the listing's own NAME column. Declare it with 'fugaro accounts pool add ${POOL} <member...>', or name one of the pools the listing prints"
    stop "the installation does not define the pool"
  fi

  fact premise PASS "${PROJECT} (GCP project ${GCP_PROJECT}) has a resident orchestrator (doctor's orchestrator-identity row, severity '${severity}'), its published policy carries the pause settings (min_pause ${min_pause:-none}, max_pause ${max_pause:-none}, wait_in_job ${wait_in_job:-none}), and 'fugaro accounts pool ls' defines the pool ${POOL} this check launches on. Every row below is one real pause of this installation"
}

# The task the run is launched with. It is small and it changes nothing the
# repository needs: what this check measures is the pause, not the work.
write_task() {
  [ -n "$TASK_FILE" ] && return 0
  TASK_FILE="$SCRATCH/task.md"
  cat >"$TASK_FILE" <<'TASK'
# Task: check 883 pause/resume

Report the Go version this repository pins in its `go.mod`, and change no file.
TASK
}

step_launch() {
  say "launching ONE real run of ${REPO} on the pool ${POOL} (this is the one billable call of this check)"
  write_task
  [ -r "$TASK_FILE" ] || {
    fact launch FAIL "the task file ${TASK_FILE} cannot be read, so there is nothing to launch this check's run with"
    stop "the task file cannot be read"
  }
  local rc=0 first
  fugaro run --repo "$REPO" --pool "$POOL" --task-file "$TASK_FILE" --project "$PROJECT" --json \
    >"$SCRATCH/launch.out" 2>"$SCRATCH/launch.err" || rc=$?
  first=$(head -1 "$SCRATCH/launch.err" 2>/dev/null || true)
  if [ "$rc" -ne 0 ]; then
    fact launch FAIL "fugaro run exited ${rc} for ${REPO} on the pool ${POOL}${first:+: ${first}}. The check needs a run to pause, and a launch that refuses says the repository, the pool or the task is not one a run starts on. Its output is in the block below; nothing further is measured"
    stop "the launch was refused"
  fi
  RUN_ID=$(jget "$SCRATCH/launch.out" run_id)
  RUN_REF=$(jget "$SCRATCH/launch.out" run)
  [ -n "$RUN_ID" ] || RUN_ID=$RUN_REF
  if [ -z "$RUN_ID" ]; then
    fact launch FAIL "fugaro run --json named no run: this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed: $(head -1 "$SCRATCH/launch.out" 2>/dev/null || true)"
    stop "the launch named no run"
  fi
  RUN_SLUG=${RUN_REF%%/*}
  [ -n "$RUN_SLUG" ] && [ "$RUN_SLUG" != "$RUN_REF" ] || RUN_SLUG=""
  fact launch PASS "ONE real run of ${REPO} was launched on the pool ${POOL}: ${RUN_REF:-$RUN_ID} (${RUN_ID}). It spends model money under the repository's and the installation's own caps and it is NOT cancelled by this check; every row below is that one run"
}

# read_runs OUT runs `fugaro runs ls --repo <repo> --project <name> --json` into
# OUT and prints its exit status. It is the one listing every row below reads,
# and it names the installation this check resolved rather than letting the
# checkout decide: a listing of another project's runs is not a reading of this
# one.
read_runs() { # read_runs <out> [err]
  local out=$1 err=${2:-$SCRATCH/runs.err} rc=0
  fugaro runs ls --repo "$REPO" --project "$PROJECT" --json --limit 50 >"$out" 2>"$err" || rc=$?
  printf '%s' "$rc"
}

# wait_loop SECONDS BODY — runs BODY until it returns 0 or the deadline passes,
# sleeping between attempts. The deadline is wall clock, so a poll interval of
# zero (the tests use one) still ends.
deadline_for() { # deadline_for <seconds>
  printf '%s' "$(( $(date +%s) + $1 ))"
}

still_waiting() { # still_waiting <deadline>
  [ "$(date +%s)" -lt "$1" ]
}

elapsed_since() { # elapsed_since <start>
  printf '%s' "$(( $(date +%s) - $1 ))"
}

pause_between() { # pause_between <seconds>
  if [ "$1" -gt 0 ]; then sleep "$1"; else sleep 0.2; fi
}

step_pause() {
  say "waiting up to ${PAUSE_WAIT}s for ${RUN_ID} to read 'paused' (fugaro runs ls --json, every ${POLL_SECONDS}s)"
  local start deadline rc status last="none"
  start=$(date +%s)
  deadline=$(deadline_for "$PAUSE_WAIT")
  while :; do
    rc=$(read_runs "$SCRATCH/runs.out")
    if [ "$rc" -ne 0 ]; then
      last=$(head -1 "$SCRATCH/runs.err" 2>/dev/null || true)
      fact pause UNVERIFIED "fugaro runs ls --repo ${REPO} --json did not answer (exit ${rc})${last:+: ${last}}, so whether ${RUN_ID} paused is NOT measured. Read it yourself with 'fugaro runs ls --repo ${REPO} --json'"
      return 0
    fi
    if [ "$(json_ok "$SCRATCH/runs.out")" = no ]; then
      fact pause UNVERIFIED "fugaro runs ls --repo ${REPO} --json printed no document this check can read, so whether ${RUN_ID} paused is NOT measured. Read it yourself with 'fugaro runs ls --repo ${REPO} --json'"
      return 0
    fi
    status=$(row_status "$SCRATCH/runs.out" "$RUN_ID")
    case "$status" in
    none)
      last="the listing names no row for ${RUN_ID}"
      ;;
    paused)
      local resume_member
      resume_member=$(row_json "$SCRATCH/runs.out" "$RUN_ID" | python3 -c 'import json,sys
r=json.load(sys.stdin)
for n,v in r.items():
    if isinstance(v,str) and v and ("resum" in n.lower() or "until" in n.lower() or "paus" in n.lower()):
        print("%s=%s" % (n,v)); break' 2>/dev/null || true)
      fact pause PASS "${RUN_ID}'s own row of 'fugaro runs ls --json' reads paused after $(elapsed_since "$start")s (the run suspended itself rather than failing or ending)${resume_member:+, and the row carries ${resume_member}}. The run is ${RUN_REF:-$RUN_ID}, as its own row names it"
      return 0
      ;;
    running | launching | "")
      last="still ${status:-unknown}"
      ;;
    *)
      local reason
      reason=$(row_json "$SCRATCH/runs.out" "$RUN_ID" | python3 -c 'import json,sys
r=json.load(sys.stdin)
print(r.get("reason") or "")' 2>/dev/null || true)
      fact pause UNVERIFIED "${RUN_ID} ended ${status} after $(elapsed_since "$start")s and never read paused${reason:+, its own row saying '${reason}'}: a run pauses only where every source of its chain is exhausted and the service answers that a pause is the right action (rather than a wait in the job or a halt), so a run that ended another way is this check's pool still having room, not a release that fails to pause. Read its record yourself with 'fugaro runs show ${RUN_ID} --json', name a pool whose members are out of room with --pool, or raise the pause settings' wait_in_job"
      return 0
      ;;
    esac
    if ! still_waiting "$deadline"; then
      fact pause UNVERIFIED "${RUN_ID} had not read paused after $(elapsed_since "$start")s of waiting (${last}), so whether this installation pauses a run is NOT measured. A run pauses only when every source of its chain is exhausted and the service decides a pause is the right action; a pool with room never reaches it. Read the run yourself with 'fugaro runs ls --repo ${REPO} --json', name a pool whose members are out of room with --pool, or give this check more time with --pause-wait (it waited ${PAUSE_WAIT}s)"
      return 0
    fi
    pause_between "$POLL_SECONDS"
  done
}

step_resume() {
  say "waiting up to ${RESUME_WAIT}s for a row naming the run that resumed ${RUN_ID} (the policy's own minimum pause decides when it is due)"
  local start deadline rc found="" last="none"
  start=$(date +%s)
  deadline=$(deadline_for "$RESUME_WAIT")
  while :; do
    rc=$(read_runs "$SCRATCH/runs.out")
    if [ "$rc" -ne 0 ]; then
      last=$(head -1 "$SCRATCH/runs.err" 2>/dev/null || true)
      fact resume UNVERIFIED "fugaro runs ls --repo ${REPO} --json did not answer (exit ${rc})${last:+: ${last}}, so whether the resident orchestrator resumed ${RUN_ID} is NOT measured. Read the listing yourself"
      return 0
    fi
    if [ "$(json_ok "$SCRATCH/runs.out")" = no ]; then
      fact resume UNVERIFIED "fugaro runs ls --repo ${REPO} --json printed no document this check can read, so whether the resident orchestrator resumed ${RUN_ID} is NOT measured. Read the listing yourself"
      return 0
    fi
    found=$(resumed_by "$SCRATCH/runs.out" "$RUN_ID")
    if [ -n "$found" ]; then
      RESUMED_ID=${found#*=}
      local ref
      ref=$(python3 - "$SCRATCH/runs.out" "$RESUMED_ID" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for r in doc.get("runs") or []:
    if r.get("run_id") == sys.argv[2] or r.get("run") == sys.argv[2]:
        print(r.get("run") or "")
        break
PY
)
      RESUMED_REF=$ref
      [ -n "$RESUMED_REF" ] || RESUMED_REF=$RESUMED_ID
      fact resume PASS "a row of 'fugaro runs ls --json' names the run that resumed ${RUN_ID} after $(elapsed_since "$start")s: ${found}. The resident orchestrator reissued the paused run as a new run in the same chain (D3185), which is what makes the pause below worth anything — a pause nothing resumes is a run that never finishes"
      return 0
    fi
    last="no row names the run that resumed ${RUN_ID}"
    if ! still_waiting "$deadline"; then
      fact resume UNVERIFIED "no row of 'fugaro runs ls --json' names the run that resumed ${RUN_ID} after $(elapsed_since "$start")s of waiting (${last}), so whether the resident orchestrator resumes a pause is NOT measured. A pause is resumed when its time comes — never sooner than the policy's minimum pause (${MIN_PAUSE_SECONDS:-unknown}s), with the orchestrator ticking every minute — so this may be a check that waited too little: give it more with --resume-wait, or read 'fugaro runs ls --repo ${REPO} --json' yourself and record what the paused row carries. The two members this check reads are a member of ${RUN_ID}'s OWN row whose name mentions resuming and not a time (the row's resume TIME — ${RUN_ID}'s own row carries it too — is not the resumer), and, on any other row, a member about continuing (previous_run, continues, resumes) that names ${RUN_ID} (that row is then the resumed run)"
      return 0
    fi
    pause_between "$POLL_SECONDS"
  done
}

step_pool() {
  if [ -z "$RESUMED_ID" ]; then
    fact pool UNVERIFIED "no run was seen resuming ${RUN_ID}, so which pool the resumed run drew on is NOT measured (the row above says why). The binding this check is about — a resumed run draws on the ORIGINAL launcher's pool (D3191) — needs a resumed run to read"
    return 0
  fi
  local slug=${RESUMED_REF%%/*}
  if [ -z "$slug" ] || [ "$slug" = "$RESUMED_REF" ]; then slug=$RUN_SLUG; fi
  if [ -z "$slug" ]; then
    fact pool UNVERIFIED "the resumed run ${RESUMED_ID} was seen, but neither its row nor the paused run's own row names the <slug>/<run-id> this check builds the record's path from, so which pool it drew on is NOT measured. Read it yourself: 'fugaro runs ls --repo ${REPO} --json' names the run, and its record is gs://${BUCKET}/runs/<slug>/<run>/result.json"
    return 0
  fi
  local uri="gs://${BUCKET}/runs/${slug}/${RESUMED_ID}/result.json"
  say "reading the resumed run's own record: gcloud storage cat ${uri}"
  local rc=0 first
  gcloud storage cat "$uri" >"$SCRATCH/record.out" 2>"$SCRATCH/record.err" || rc=$?
  if [ "$rc" -ne 0 ] || [ "$(json_ok "$SCRATCH/record.out")" = no ]; then
    first=$(head -1 "$SCRATCH/record.err" 2>/dev/null || true)
    fact pool UNVERIFIED "the resumed run's own record could not be read (gcloud storage cat ${uri}, exit ${rc})${first:+: ${first}}, so which pool it drew on is NOT measured. The bucket ages a run's objects out, and a record the bucket no longer holds is unread rather than wrong — read it yourself with 'gcloud storage cat ${uri}', or point --bucket at the installation's own"
    return 0
  fi
  local src
  src=$(source_of "$SCRATCH/record.out")
  case "$src" in
  none)
    fact pool UNVERIFIED "the resumed run's own record carries no stage source at all, so which pool it drew on is NOT measured: the record's stages name no 'source', which is a record written before the release that puts the placement on it (or an image that did not). The record is ${uri}"
    return 0
    ;;
  no-pool)
    fact pool UNVERIFIED "the resumed run's own record carries stage sources that name no pool, so whether the resumed run drew on ${POOL} is NOT measured: the run may have been served by a key or a provider source rather than by a pool. The record is ${uri}"
    return 0
    ;;
  unreadable)
    fact pool UNVERIFIED "the resumed run's own record could not be read as a document, so which pool it drew on is NOT measured. The record is ${uri}"
    return 0
    ;;
  esac
  local kind pool
  kind=$(printf '%s' "$src" | tr ' ' '\n' | sed -n 's/^kind=//p')
  pool=$(printf '%s' "$src" | tr ' ' '\n' | sed -n 's/^pool=//p')
  if [ "$pool" != "$POOL" ]; then
    fact pool FAIL "the resumed run ${RESUMED_REF:-$RESUMED_ID} was NOT served from the pool the launch named: its own record's stage source reads kind=${kind} pool=${pool}, and the launch named ${POOL} (D3176). A run that resumes a person's paused run must draw on that person's pool (D3191), and a resumed run served from ${pool} is a resume that left the launcher's pool behind. Its record is ${uri}"
    return 0
  fi
  if [ "$kind" = "launcher-pool" ]; then
    fact pool PASS "the resumed run ${RESUMED_REF:-$RESUMED_ID} was served from the ORIGINAL launcher's pool: its own record's stage source reads kind=${kind} pool=${pool}, the pool the launch named (C1's kind word for it, D3191)"
    return 0
  fi
  fact pool UNVERIFIED "the resumed run ${RESUMED_REF:-$RESUMED_ID} was served from a source NAMED ${pool}, the pool the launch named, but its record calls the kind ${kind} and not launcher-pool, so whether that pool is the launcher's own is NOT measured: C1's kind words are launcher-pool, pool, anthropic, provider and vertex, and a run served from the plain 'pool' kind is a run served from a pool the SERVICE resolved rather than the one the launcher named (D3191). Read the run's own record and say which it is: gcloud storage cat ${uri}"
}

step_premise
step_launch
step_pause
step_resume
step_pool
REACHED_END=yes
