#!/usr/bin/env bash
# Check 870 — the run index's three reads, timed WARM on one project of yours,
# as ONE script YOU run. It is the "before" half of the pair 870/871 the plan
# docs/console-fixes/implementation-plan.md takes (D3205).
#
# The index (internal/runindex) is what makes a listing cost a handful of
# requests however much history an installation has (I-INDEX, RI1 to RI6): the
# three reads that used to walk the runs bucket read `runIndex/<run id>` and
# `runIndexMeta/summary` instead. This check measures what that costs on a real
# project, and nothing else.
#
# It reads, and only reads:
#
#   - `fugaro runs ls --json` — the listing, its rows and totals from the index;
#   - `fugaro plan status --json --all` IN YOUR CHECKOUT (the command reads the
#     directory it is run in and refuses one named elsewhere, so this check
#     cds into the one you name and runs it there), once per task of the plans
#     under docs/ it finds;
#   - `fugaro graph --json` — the whole dependency graph, the coordinator's
#     scope and one per orchestrator.
#
# Each read is taken once to warm it and then CHECK870_REPEATS times (3), and
# the FIGURE JUDGED IS THE MEDIAN, in milliseconds, against CHECK870_BOUND_MS
# (1000). `plan status` is called with --no-merges: the plan's own reading is
# `plan status --json --all`, and --no-merges leaves out only the gh lookups,
# which cost the network rather than the index and would put a figure this
# check is not about into the reading (CHECK870_MERGES=yes takes it the plan's
# literal way).
#
# It prints PASS when all three medians are under the bound, and the run is
# WARM in the sense the plan means: each read has answered once before it is
# timed, so what is measured is the index's own cost rather than a cold start.
#
# Usage:      bash scripts/check-870-run-index-timings.sh <fugaro-project>
#             bash scripts/check-870-run-index-timings.sh --checkout <dir> <fugaro-project>
#             bash scripts/check-870-run-index-timings.sh --dry-run <fugaro-project>
#             bash scripts/check-870-run-index-timings.sh --help
# Environment CHECK870_CHECKOUT  the checkout `plan status` is run in (default
#                                `.`: the directory you run this from)
#             CHECK870_FUGARO    the binary to read (default `fugaro`)
#             CHECK870_BOUND_MS  the bound each median is judged by, in
#                                milliseconds (default 1000)
#             CHECK870_REPEATS   how many times each read is timed (default 3)
#             CHECK870_CALL_SECONDS  the deadline for one `fugaro` call
#                                (default 600)
#             CHECK870_MERGES=yes    call `plan status --json --all` with no
#                                --no-merges, the plan's literal reading
#             CHECK870_KEEP_SCRATCH=yes  keep the scratch directory and say so
# Needs:      bash, mktemp, python3 (the clock and the reader of the documents),
#             perl (the deadline), and a fugaro on PATH.
# Calls:      `fugaro runs ls --json`, `fugaro plan status --json --all` (in the
#             checkout) and `fugaro graph --json`, each once warm and then
#             CHECK870_REPEATS times. Every call runs with stdin closed and
#             nothing is written outside this run's scratch directory.
# Where:      the project you name, and nothing else. Never `belong`: a project
#             whose name, and a config whose own members, carry it are refused
#             by name before a single call.
# Runs:       by YOU, at your own terminal, on the project you name. It refuses
#             to start in a coding agent's session (its own test,
#             check-870-run-index-timings_test.sh, drives it against fakes on
#             PATH and touches no cloud). No agent runs it.
# Costs:      reads only: three `fugaro` reads per sample, a few requests each,
#             and no cloud write of any kind — no run is launched, no model is
#             called, no bucket object is written, no credential is read. Wall
#             clock: seconds.
# Output:     progress on stderr, ONE block on stdout headed CHECK 870 RESULT.
#             Its rows are `key state detail`, and its last line is the verdict,
#             which is the exit status:
#               PASS (exit 0)        all three medians are under the bound
#               FAIL (exit 1)        a median is at or over the bound, or a read
#                                    did not answer
#               INCOMPLETE (exit 2)  the run stopped before it could judge
#               INCOMPLETE (exit 3)  it judged, and a read it needed could not
#                                    be measured here (this build carries no
#                                    graph, no run is joined by the checkout, or
#                                    the index holds no run to measure)
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
DRY_RUN=no

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
Check 870 — the run index's three reads, timed warm on one project of yours: the
listing, the plan join and the graph. It asks nothing and writes nothing outside
its own scratch directory.

Usage:
  bash scripts/check-870-run-index-timings.sh <fugaro-project>
  bash scripts/check-870-run-index-timings.sh --checkout <dir> <fugaro-project>
  bash scripts/check-870-run-index-timings.sh --dry-run <fugaro-project>
  bash scripts/check-870-run-index-timings.sh --help

Each read is taken once warm and then three times, and the median of the three
is judged against 1000 ms: PASS when all three are under it. The checkout named
with --checkout (default `.`) is where `plan status` is run.

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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 870: it times a real project's reads with the credentials on your machine. Run it yourself, at your terminal: bash scripts/check-870-run-index-timings.sh <fugaro-project>"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and a trace of this check is noise a figure is read out of. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check times the reads with it"
}

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --dry-run) DRY_RUN=yes ;;
  --checkout)
    [ $# -ge 2 ] || stop "--checkout takes a directory (usage: check-870-run-index-timings.sh --checkout <dir> <fugaro-project>)"
    CHECKOUT=$2
    shift
    ;;
  -*) stop "unknown argument $1 (usage: check-870-run-index-timings.sh <fugaro-project>, or --help)" ;;
  *)
    if [ -z "$PROJECT" ]; then PROJECT=$1; else stop "one project is timed at a time (got '$PROJECT' and '$1')"; fi
    ;;
  esac
  shift
done

guard_xtrace
guard_agent

[ -n "$PROJECT" ] || stop "no Fugaro project was named (usage: check-870-run-index-timings.sh <fugaro-project>)"

FUGARO=${CHECK870_FUGARO:-fugaro}
CHECKOUT=${CHECKOUT:-${CHECK870_CHECKOUT:-.}}
BOUND_MS=${CHECK870_BOUND_MS:-1000}
REPEATS=${CHECK870_REPEATS:-3}
CALL_SECONDS=${CHECK870_CALL_SECONDS:-600}
for pair in "CHECK870_BOUND_MS:$BOUND_MS" "CHECK870_REPEATS:$REPEATS" "CHECK870_CALL_SECONDS:$CALL_SECONDS"; do
  case "${pair#*:}" in '' | *[!0-9]*) stop "${pair%%:*} '${pair#*:}' is not a number of milliseconds, repeats or seconds" ;; esac
done
[ "$BOUND_MS" -ge 1 ] || stop "CHECK870_BOUND_MS must be at least 1"
[ "$REPEATS" -ge 1 ] || stop "CHECK870_REPEATS must be at least 1"
[ "$CALL_SECONDS" -ge 1 ] || stop "CHECK870_CALL_SECONDS must be at least 1"

# `belong` is refused before the name is judged any other way: a name built
# around the owner's own installation is the one refusal this check never
# softens, whatever else is wrong with it.
guard_belong "project" "$PROJECT"
guard_belong "checkout" "$CHECKOUT"

printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' ||
  stop "'$PROJECT' is not a Fugaro project name"

need mktemp
need python3
need perl
command -v "$FUGARO" >/dev/null 2>&1 || stop "$FUGARO is not on PATH, and this check times the reads with it"

[ -d "$CHECKOUT" ] || stop "the checkout '$CHECKOUT' is not a directory: name the repository whose plans are joined with CHECK870_CHECKOUT or --checkout"

PLAN_ARGS=(--json --all)
if [ "${CHECK870_MERGES:-no}" != yes ]; then PLAN_ARGS=(--json --all --no-merges); fi
READS=(runs-ls plan-status graph)

if [ "$DRY_RUN" = yes ]; then
  printf '\nCHECK 870 PLAN\n'
  printf 'PROJECT   %s\n' "$PROJECT"
  printf 'CHECKOUT  %s\n' "$CHECKOUT"
  printf 'BOUND     %d ms, the median of %d timed samples per read\n' "$BOUND_MS" "$REPEATS"
  printf 'CALLS\n'
  printf '  warm then %d x  %s runs ls --json --project %s\n' "$REPEATS" "$FUGARO" "$PROJECT"
  printf '  warm then %d x  (cd %s && %s plan status %s --project %s)\n' "$REPEATS" "$CHECKOUT" "$FUGARO" "${PLAN_ARGS[*]}" "$PROJECT"
  printf '  warm then %d x  %s graph --json --project %s\n' "$REPEATS" "$FUGARO" "$PROJECT"
  printf 'Nothing was called and nothing was judged (--dry-run)\n'
  exit 0
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk870.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
: >"$FACTS"
: >"$SCRATCH/timing"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "${CHECK870_KEEP_SCRATCH:-}" ]; then
    say "kept the scratch directory at $SCRATCH (CHECK870_KEEP_SCRATCH is set)"
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
  printf '\nCHECK 870 RESULT\n'
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-12s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 870 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone; nothing outside it was written"
  elif [ -n "${CHECK870_KEEP_SCRATCH:-}" ]; then
    facts="${facts}"$'\n'"restore|INFO|the scratch directory is kept at ${SCRATCH}, by CHECK870_KEEP_SCRATCH"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}: remove it yourself"
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

# run_call SECONDS CMD... — one command with a deadline of its own and stdin
# closed. perl's alarm rather than timeout(1), which macOS does not have; the
# alarm is set on the process that then execs the command, so it is the command
# itself that the deadline kills.
run_call() { # run_call SECONDS CMD...
  local secs=$1
  shift
  perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$secs" "$@" </dev/null
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
    (cd "$CHECKOUT" && exec perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$CALL_SECONDS" \
      "$FUGARO" plan status "${PLAN_ARGS[@]}" --project "$PROJECT") \
      >"$SCRATCH/$tag-$SAMPLE.out" 2>"$SCRATCH/$tag-$SAMPLE.err" || rc=$?
    ;;
  graph)
    run_call "$CALL_SECONDS" "$FUGARO" graph --json --project "$PROJECT" \
      >"$SCRATCH/$tag-$SAMPLE.out" 2>"$SCRATCH/$tag-$SAMPLE.err" || rc=$?
    ;;
  esac
  printf '%s %s %s %s\n' "$tag" "$SAMPLE" "$(( $(now_ms) - started ))" "$rc" >>"$SCRATCH/timing"
  return 0
}

fact belong PASS "neither the project this check was given (${PROJECT}) nor the checkout (${CHECKOUT}) names the owner's own installation: nothing this check was given touches belong, and it reads no config of its own"

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
python3 - "$SCRATCH" "$BOUND_MS" "$REPEATS" "$CHECKOUT" >"$SCRATCH/analysis" 2>"$SCRATCH/analysis.err" <<'PY' ||
import json, os, sys

scratch, bound_ms, repeats, checkout = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]

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

# The timing file is one line per call: tag, sample ("warm" or the index), the
# wall clock in ms, and the exit status.
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
    return "the %s read exited %d after %d ms (CHECK870_CALL_SECONDS bounds it): %s" % (
        sample, rc, ms, first_line("%s-%s.err" % (tag, sample)))

# judged NAME TAG: the figures of one read, and the verdict the bound gives it.
# A read that did not answer is a FAIL: the criterion is about what these reads
# cost, and one that exited non-zero measured nothing. The warm-up is not
# judged: it is there so that what is timed is not a cold start.
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

# ------------------------------------------------------------- the listing
runs = doc("runs-ls-warm.out")
scale = None
if runs is None:
    fact("runs-ls", "FAIL", "`fugaro runs ls --json` answered no document this check could read: %s"
         % (first_line("runs-ls-warm.err") or first_line("runs-ls-warm.out")))
else:
    totals = runs.get("totals") or {}
    scale = totals.get("runs")
    rows = runs.get("runs")
    where = "the index" if "index_complete" in runs else "the window (this listing read no index)"
    if scale is None:
        scale = len(rows) if isinstance(rows, list) else None
    judged("runs-ls", "runs-ls")
    if isinstance(rows, list):
        fact("listing", "INFO", "%d rows in the page, %s runs in the read's own totals, from %s"
             % (len(rows), scale if scale is not None else "an unknown number of", where))

# ------------------------------------------------------------ the plan join
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

# ----------------------------------------------------------------- the graph
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

# ------------------------------------------------------------------- the index
if runs is not None:
    if "index_complete" not in runs:
        fact("index", "INFO", "the listing read the window rather than the index, so it says nothing about the index: an installation whose index has folded nothing is read this way (run `fugaro runs reindex`)")
    elif runs.get("index_complete"):
        fact("index", "PASS", "the index has reached the end of history, folded through %s"
             % (runs.get("folded_through") or "an unrecorded moment"))
    else:
        fact("index", "INFO", "the index is still building (index_complete false%s): a figure taken now is of what it has folded. Run `fugaro runs reindex`, wait for the walk to reach the end of history, and run this check again"
             % (", folded through %s" % runs["folded_through"] if runs.get("folded_through") else ""))
if plan is not None and not plan.get("index_complete", True):
    fact("plan-index", "INFO", "the plan join read an index that is still building, so its figure is of what the index has folded")

if scale in (0, None) and runs is not None:
    fact("scale", "UNVERIFIED", "the index holds no run, so a figure under the bound measures nothing: run this check on a project with history")
else:
    fact("scale", "PASS", "the read covers %s runs: the figure is of an installation with history" % scale)
PY
  stop "the documents could not be read: $(first_line "$SCRATCH/analysis.err")"

while IFS='|' read -r tag k state detail; do
  case "$tag" in
  fact) fact "$k" "$state" "$detail" ;;
  esac
done <"$SCRATCH/analysis"

REACHED_END=yes
