#!/usr/bin/env bash
# Check 760 — how long `fugaro runs ls` takes on a REAL installation, as ONE
# script YOU run.
#
# The release behind tasks RL1, RL2, RL2B and RL3 of docs/runs-ls-speed/ has
# `runs ls` read a pull request through its own index instead of scanning every
# run of the window, and read the two live states (`running`, `launching`)
# through the set the backend is actually executing. What no offline test can
# answer is whether that is FAST ENOUGH on a real bucket, and the only figures
# anybody has are the owner's own, taken on 2026-11-19 with the run cache warm:
#
#   runs ls --state running    20 s     (0 rows: nothing was in flight)
#   runs ls --pr 3253          23 s     (1 row)   [32 s with --no-cache]
#   runs ls --since 7d         15 s     (991 rows)
#
# This check re-takes those reads (the pull-request one with the cache and
# without it) three times each and prints the median beside each baseline, with
# the plan's targets: `--state running` and `--pr N` under 5 s. The default
# listing has no target, and its figure is the one that decides RL4 — the
# optional task that would make the listing itself faster — under the plan's own
# gate (D2966): UNDER 8 SECONDS MEANS NEVER, so a listing that comes back under
# 8 s is the reading that says RL4 is not built at all.
#
# A SLOW READING IS A READING, NOT A FAILURE. Nothing here fails on a figure,
# whatever it is: the whole check is the numbers, and it exits 0 having printed
# them. Its exit 2 is for a run that could not measure — a guard stopped it, a
# tool is missing, `fugaro runs ls` refused the call or never answered within
# CHECK760_CALL_SECONDS. That is why there is no exit 1 in this check: the only
# word a figure could fail is a target the plan sets for the owner to read, not
# a criterion this check can settle.
#
# It also takes one sanity reading, because the fallback path is what a bad index
# would silently hand the owner: two pull requests' row sets are compared by
# COUNT and RUN IDS only — never by content — and the `pr-rows` row says which of
# the two reads went through the index and which fell back to the scan, read off
# the CLI's own note on stderr (`no pull-request index for …`, or the index could
# not be read).
#
# Nothing is written anywhere: every call this check makes is a read (`fugaro
# doctor`, `fugaro runs ls`), it never launches a run, never calls a model and
# never touches the bucket, the project or the installation's configuration. The
# one thing it writes is its own scratch directory, which its exit trap removes.
#
# Usage:      bash scripts/live-checks/check-760-runs-ls-speed.sh \
#               --installation <installation> [--pr <N>] [--repo <owner/name>] \
#               [--old-pr <N>]
#             bash scripts/live-checks/check-760-runs-ls-speed.sh --dry-run
#             --installation  the installation to time (default `fugaro`); it is
#             read with `fugaro doctor` and timed with `fugaro runs ls`, and
#             `belong` is refused by name.
#             --pr N          the pull request to time. Without it the check
#             takes the newest pull request the window's own listing names; for
#             a pull request that window does not hold (an old one, which is the
#             interesting case for the sanity reading) give --repo as well — a
#             pull request number means something only in one repository.
#             --repo OWNER/NAME  the repository the pull-request reads are
#             scoped to; without it the check takes the one the timed pull
#             request's own rows name.
#             --old-pr N      a SECOND pull request for the sanity reading. Give
#             one whose runs are older than the backfill (`fugaro update` fills
#             90 days of index) to see the fallback scan answer; without it the
#             check takes the oldest OTHER pull request its window names.
#             --dry-run       prints the plan and touches nothing: no call, no
#             read, no file of yours opened.
# Needs:      bash, mktemp, python3 (the reader and the clock), sort, awk and a
#             `fugaro` on PATH that is the version under test. Nothing is
#             installed and nothing is fetched; a missing tool stops the run in
#             one line naming it.
# Where:      the installation you name, and ONLY it. Never `belong`: an
#             installation or a repository that names it is refused before the
#             first call, and refused again if `fugaro doctor` answers with it.
#             The figures are of `fugaro runs ls` against that installation's
#             real runs bucket, which is what makes them comparable with the
#             baselines above and with nothing else.
# Costs:      NOTHING billable: no run is launched, no model is called, no image
#             is built, no write of any kind is made. Wall clock only — four
#             warm-up reads and twelve timed reads, so minutes on an installation
#             whose listing is slow (the baselines are 15 s to 32 s a read).
# Runs:       by YOU, after the release and `fugaro update` (which is what
#             backfills the pull-request index). No agent runs any step of it;
#             its own test (scripts/live-checks/check-760-runs-ls-speed_test.sh)
#             drives every step against a fake `fugaro` and never touches a
#             cloud, a bucket or an installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory holds the
#             readings, the documents and the reader, and the exit trap removes
#             it; the `restore` row says whether it really went. Nothing else was
#             created, changed or deleted, so there is nothing else to undo.
#             CHECK760_KEEP_SCRATCH=yes keeps the directory and says where.
# Output:     progress on stderr, ONE block on stdout headed CHECK 760 RESULT,
#             to copy back to the coordinator. Its last line is the verdict, and
#             the verdict is the exit status:
#
#               CHECK 760 RESULT: PASS (exit 0)        the readings were taken
#                                                      and are printed, whatever
#                                                      the figures are;
#               CHECK 760 RESULT: INCOMPLETE (exit 2)  the run could not measure
#                                                      everything: a guard
#                                                      stopped it, a tool is
#                                                      missing, a call was
#                                                      refused or never answered,
#                                                      or a query's row reads
#                                                      FAIL.
#
#             The figures are the answer, not the verdict: read the
#             `query-state-running`, `query-pr`, `query-pr-no-cache` and
#             `query-since-7d` rows beside their 2026-11-19 baselines, and read
#             the `rl4-gate` row for the one decision they carry.
#
# No value is read from any credential: this check reads no token, no key and no
# secret of any kind — `fugaro` uses the credentials already on the machine,
# inside its own process, and what comes back here is a listing, a count and a
# duration. The reader prints a fixed vocabulary (a count, a run id, a
# pull-request number, a repository name, a note kind), each matched against the
# characters that vocabulary allows, so text a document echoed back cannot reach
# the block. And no trace: `bash -x` would print every command as it runs, so
# guard_xtrace REFUSES a run whose shell arrived with tracing on.
#
# CHECK760_FUGARO, CHECK760_INSTALLATION, CHECK760_REPO, CHECK760_PR,
# CHECK760_OLD_PR, CHECK760_RUNS, CHECK760_CALL_SECONDS, CHECK760_TARGET_MS,
# CHECK760_GATE_MS, CHECK760_BASELINE_STATE_RUNNING_MS, CHECK760_BASELINE_PR_MS,
# CHECK760_BASELINE_PR_NO_CACHE_MS, CHECK760_BASELINE_SINCE_7D_MS,
# CHECK760_KEEP_SCRATCH and CHECK760_DRY_RUN exist for this script's own test
# and for an owner's own patience (a shorter deadline, a different number of
# timed runs). None of them can point the check at another installation: that is
# the one `--installation` (or CHECK760_INSTALLATION) names, and `belong` is
# refused whatever a knob says. The four baseline knobs move the figure a
# reading is compared AGAINST, never the reading itself, and the two threshold
# knobs move the words MET/OVER — the `rl4-gate` row's own sentence is the
# plan's gate and always says which way the reading went.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. `bash -x
# check-760-runs-ls-speed.sh` is a natural thing to reach for, and a traced run
# prints every command — the installation's own name and the run ids of a real
# project's runs among them — into the log the owner would paste back. So the
# trace is turned off here, BASH_XTRACEFD is unset so a trace redirected to a
# file this script knows nothing about cannot be reopened, and guard_xtrace
# REFUSES the run when the shell arrived with tracing on in SHELLOPTS/BASHOPTS,
# where every child would start tracing on its own.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured KEY REASON — a row that could not be taken. This check has no FAIL
# that means "the figure is bad": a FAIL row here means the reading is MISSING,
# which is what makes the run INCOMPLETE (exit 2) rather than a pass.
unmeasured() {
  fact "$1" FAIL "$2"
  owner "could not measure $1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; no figure is concluded from it" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 760 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 760 needs bash"
  need mktemp
  need python3 "the reader that parses the listings, and the clock every timed run is measured with, are one python3 file"
  need sort
  need awk
  need "$FUGARO_BIN" "the executable under test: the whole check is how long it takes"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk760-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
CLEANUP_RUNNING=no
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk760.XXXXXXXX") || die "Check 760 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 760 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  stop_call
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  if [ "$SCRATCH_KEPT" = yes ]; then
    say "CHECK760_KEEP_SCRATCH is set: $SCRATCH is kept (the readings, the window listing, every call's own output and the reader are in it)"
  else
    remove_scratch
  fi
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than the readings the check is for"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The scratch directory holds this check's own readings: the doctor's answer,
  # the listings, the per-query stdout and stderr of every timed read, and the
  # reader. No credential is read on any path, so none can be in one of them;
  # their removal is READ, not assumed.
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
  elif [ "$SCRATCH_KEPT" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is KEPT at $SCRATCH, because CHECK760_KEEP_SCRATCH=yes asked for that: nothing was put back BY REQUEST, and removing the directory is yours (its files: the doctor's answer, the window listing, one stdout and one stderr per timed read, and the reader). This check wrote nothing else anywhere: it creates, changes and deletes nothing on the installation, in the project or in the runs bucket"
  else
    facts="${facts}"$'\n'"restore|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself") (its files: the doctor's answer, the window listing, one stdout and one stderr per timed read, and the reader). This check wrote nothing else anywhere: it creates, changes and deletes nothing on the installation, in the project or in the runs bucket"
  fi
  # The verdict is taken from the facts, not from the exit path: a run that
  # recorded a FAIL row has a reading missing, and a run that reached its end
  # with every row taken is a PASS whatever the figures are. Exit 1 is never used
  # — this check has no criterion a slow figure could fail.
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=2
    elif [ "$status" -ne 0 ]; then
      status=2
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs — a real installation's name and its runs' own ids among them — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-760-runs-ls-speed.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the installation's name or a run id"
}

# guard_agent_session: this check is the owner's. Its figures are about a real
# installation's own bucket, and they are meant to be pasted back by the person
# who ran them, on the machine whose cache they warmed; a coding agent's session
# has no terminal of the owner's and no standing to answer for the installation,
# and the register's row says no agent runs any step of it. Refusing here is what
# makes the block say that.
#
# The loop is the five markers that say an agent, and not a person, is driving
# this shell. The sixth, CLAUDE_CODE_SSE_PORT, says only that this shell sits in
# an editor's terminal, where the person is still the one typing, so it is
# DROPPED rather than refused: refusing on it would lock the owner out of this
# check in their own editor.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 760 is yours to run at your own terminal, against an installation your own credentials reach, and no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 760 is yours to run at your own terminal, against an installation your own credentials reach, and no agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like), so this is your own session — the one this check is written for. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused, because it says this shell sits in an editor's terminal and not that an agent is driving it"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test, in either case, because the refusal has to hold for the spellings a
# person and a config use (`belong`, `Belong`, `belong-dev`).
is_belong() {
  case "$(lower "$1")" in
  *belong*) return 0 ;;
  esac
  return 1
}

# guard_belong: `belong` is the installation this check must never touch — its
# figures are the owner's own work — and a script that can be pointed at it by a
# typo has no business reading anything there. Any value GIVEN to this check that
# names it is refused before the first call is made (die, so nothing is read at
# all); the value it READS (doctor's own answer) is checked in the installation
# step, which is where it first exists, and that step writes the `belong` row.
guard_belong() {
  local what value
  for what in installation repository; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    repository) value=$REPO ;;
    esac
    [ -n "$value" ] || continue
    if is_belong "$value"; then
      die "Check 760 refused: the $what '$value' names 'belong', the installation this check must never touch. Nothing was read anywhere"
    fi
  done
}

# ------------------------------------------------------------ the clock + calls
# now_ms: the epoch in milliseconds. The shell has no portable sub-second clock
# (macOS's /bin/bash has no EPOCHREALTIME and BSD `date` has no %N), so the clock
# is python3's — the interpreter this check already needs for its reader.
now_ms() { python3 -c 'import time; print(int(time.time() * 1000))'; }

ELAPSED_MS=0
CALL_PID=""

# run_call SECONDS OUT ERR CMD... — one command with a deadline of its own, in a
# process group of its own so the deadline reaches whatever the call started, its
# two streams in files, and ELAPSED_MS set to the milliseconds it took.
# `timeout(1)` is not used: it is not on macOS, where this check is also run. The
# loop waits in tenths of a second rather than whole ones — this check's whole
# subject is how long a call takes, and a poll that woke only on the second would
# add up to a second to every reading — while the figure itself is taken from
# `wait`, so it is the command's own runtime and not a tick's.
run_call() { # run_call SECONDS OUT ERR CMD...
  local secs=$1 out=$2 err=$3
  shift 3
  local started ticks waited=0 rc=0
  ticks=$((secs * 10))
  started=$(now_ms)
  set -m
  "$@" </dev/null >"$out" 2>"$err" &
  CALL_PID=$!
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$ticks" ]; then
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      ELAPSED_MS=$(( $(now_ms) - started ))
      set +m
      return 124
    fi
    sleep 0.1
    waited=$((waited + 1))
  done
  wait "$CALL_PID" || rc=$?
  CALL_PID=""
  ELAPSED_MS=$(( $(now_ms) - started ))
  set +m
  return "$rc"
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

# ------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/read760.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# last_line FILE — what a refused call said, for a row that has to explain
# itself. Clipped, and never a value of this check's own: this is the CLI's text.
last_line() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | tr -d '\r' || true)
  [ -n "$text" ] || text="the call printed nothing"
  printf '%s' "$(printf '%s' "$text" | cut -c1-200)"
}

# The row vocabulary: seconds with two decimals, the median of the samples, and
# the two sentences a figure is read with. All three are arithmetic on THIS
# check's own integers — no text of a document travels through them.
secs() { awk -v ms="$1" 'BEGIN { printf "%.2f", ms / 1000 }'; }
# limit_text MS — a threshold in the unit that reads: the plan's figures are
# seconds, and a knob the owner (or this check's own test) set below a second is
# clearer as milliseconds than as "0.00 s".
limit_text() {
  if [ "$1" -lt 1000 ]; then printf '%sms' "$1"; else printf '%s s' "$(secs "$1")"; fi
}
runs_word() { if [ "$1" -eq 1 ]; then printf 'run'; else printf 'runs'; fi; }
median_ms() { # median_ms "ms ms ms" — the middle sample, the lower one of two
  printf '%s\n' $1 | sort -n | awk '{ a[NR] = $1 } END { if (NR > 0) print a[int((NR + 1) / 2)] }'
}
baseline_note() { # baseline_note MEDIAN_MS BASELINE_MS
  local ratio
  ratio=$(awk -v m="$1" -v b="$2" 'BEGIN { if (b > 0) printf "%.2f", m / b; else printf "0.00" }')
  if [ "$1" -lt "$2" ]; then
    printf 'beside the 2026-11-19 baseline %s: FASTER, %sx it' "$(limit_text "$2")" "$ratio"
  elif [ "$1" -eq "$2" ]; then
    printf 'beside the 2026-11-19 baseline %s: the same figure' "$(limit_text "$2")"
  else
    printf 'beside the 2026-11-19 baseline %s: SLOWER, %sx it' "$(limit_text "$2")" "$ratio"
  fi
}
target_note() { # target_note MEDIAN_MS TARGET_MS
  local over
  if [ "$2" -le 0 ]; then
    printf 'the plan sets no target for this read: the figure is read against its own baseline and nothing else'
    return 0
  fi
  if [ "$1" -le "$2" ]; then
    printf 'TARGET under %s: MET (median %s s)' "$(limit_text "$2")" "$(secs "$1")"
    return 0
  fi
  over=$(secs "$(( $1 - $2 ))")
  printf 'TARGET under %s: OVER by %s s — over is a READING and not a failure: this check never fails on a slow figure, and what the plan asks for here is the number' "$(limit_text "$2")" "$over"
}

# time_query KEY LABEL BASELINE_MS TARGET_MS WARM CMD... — one `runs ls` query
# timed CHECK760_RUNS times, its row written as the median beside the 2026-11-19
# baseline and the plan's target (TARGET_MS of 0 is a read the plan gives no
# target). WARM=yes runs the query once untimed first, which is what "with the
# run cache warm" means and what makes the timed runs comparable with the
# baselines — the `--no-cache` query is the one read nothing can warm, and its
# row says so. A query that is refused, or that never answers within
# CHECK760_CALL_SECONDS, writes a FAIL row naming the call's own last line of
# stderr and does not stop the other queries: the readings that could be taken
# are taken. The median is left in $SCRATCH/<key>.median for the gate row.
time_query() {
  local key=$1 label=$2 baseline=$3 target=$4 warm=$5
  shift 5
  local shown
  shown=$(printf '%s ' "$@")
  shown=${shown% }
  local rc=0
  if [ "$warm" = yes ]; then
    say "warming the cache for ${label}: ${FUGARO_BIN} ${shown}"
    run_call "$CALL_SECONDS" "$SCRATCH/$key.warm.out" "$SCRATCH/$key.warm.err" "$FUGARO_BIN" "$@" || rc=$?
    if [ "$rc" -eq 124 ]; then
      unmeasured "$key" "${label} could not be measured: the untimed read that warms the cache never answered within ${CALL_SECONDS}s, so nothing of this query was timed. The call is '${FUGARO_BIN} ${shown}'; run it yourself to see what it does"
      return 0
    fi
    if [ "$rc" -ne 0 ]; then
      unmeasured "$key" "${label} could not be measured: the untimed read that warms the cache was refused (exit ${rc}): $(last_line "$SCRATCH/$key.warm.err"). Nothing of this query was timed"
      return 0
    fi
  fi
  local i=0 samples="" text="" ms
  while [ "$i" -lt "$RUNS" ]; do
    say "timing ${label}, run $((i + 1)) of ${RUNS}"
    rc=0
    run_call "$CALL_SECONDS" "$SCRATCH/$key.$i.out" "$SCRATCH/$key.$i.err" "$FUGARO_BIN" "$@" || rc=$?
    ms=$ELAPSED_MS
    if [ "$rc" -eq 124 ]; then
      unmeasured "$key" "${label} could not be measured: run $((i + 1)) never answered within ${CALL_SECONDS}s and was killed with its process group, so the median of ${RUNS} is not a reading. The call is '${FUGARO_BIN} ${shown}'; raise CHECK760_CALL_SECONDS only if you believe that read is genuinely that slow"
      return 0
    fi
    if [ "$rc" -ne 0 ]; then
      unmeasured "$key" "${label} could not be measured: run $((i + 1)) was refused (exit ${rc}): $(last_line "$SCRATCH/$key.$i.err"). No figure for this query"
      return 0
    fi
    samples="${samples}${samples:+ }${ms}"
    text="${text}${text:+, }$(secs "$ms") s"
    i=$((i + 1))
  done
  local median
  median=$(median_ms "$samples")
  [ -n "$median" ] || median=0
  printf '%s\n' "$median" >"$SCRATCH/$key.median"
  fact "$key" PASS "median $(secs "$median") s over ${RUNS} $(runs_word "$RUNS") (${text}) for '${FUGARO_BIN} ${shown}'; $(baseline_note "$median" "$baseline"); $(target_note "$median" "$target")"
}

# --------------------------------------------------------------- the steps
step_installation() {
  # doctor's own exit status is not the answer to which installation this is: it
  # exits 1 whenever any of its checks failed (billing, IAM, the update check),
  # none of which has anything to do with the name. Its document is read instead,
  # and only for three names.
  run_call "$CALL_SECONDS" "$SCRATCH/doctor.json" "$SCRATCH/doctor.err" \
    "$FUGARO_BIN" doctor --project "$INSTALLATION" --json || true
  local rc=0 out
  out=$(reader doctor "$SCRATCH/doctor.json") || rc=$?
  if [ "$rc" -ne 0 ]; then
    local why="the reader could not read the document"
    [ "$rc" -eq 3 ] && why="the document is not the JSON the reader expects"
    fact installation FAIL "'${FUGARO_BIN} doctor --project ${INSTALLATION} --json' answered something this check cannot read (${why}): $(last_line "$SCRATCH/doctor.err"). Nothing was timed"
    stop "the installation '${INSTALLATION}' could not be read: the call is '${FUGARO_BIN} doctor --project ${INSTALLATION} --json'. Check the name, that this machine's credentials reach that installation, and that '${FUGARO_BIN}' is the version under test"
  fi
  NAME_SEEN=$(field_of name "$out")
  GCP_SEEN=$(field_of gcp "$out")
  REGION_SEEN=$(field_of region "$out")
  case "$NAME_SEEN" in
  "" | -)
    fact installation FAIL "'${FUGARO_BIN} doctor --project ${INSTALLATION} --json' answered no installation name this check can read (the reader printed 'name=${NAME_SEEN}'). Nothing was timed"
    stop "doctor answered no installation name for --project ${INSTALLATION}"
    ;;
  esac
  if is_belong "$NAME_SEEN" || is_belong "$GCP_SEEN"; then
    fact belong FAIL "the installation this check READ from '${FUGARO_BIN} doctor --project ${INSTALLATION} --json' names belong (installation '${NAME_SEEN}', GCP project '${GCP_SEEN}'): this check is never run against belong, whatever it was asked for. Nothing was timed"
    stop "the installation doctor reports names belong, which this check never touches"
  fi
  fact belong PASS "neither the installation this check was given (${INSTALLATION}) nor the repository (${REPO:-none given}) nor the one '${FUGARO_BIN} doctor --project ${INSTALLATION} --json' reports (${NAME_SEEN}, GCP project ${GCP_SEEN}, region ${REGION_SEEN}) names belong: nothing this check was given or has read touches the owner's own installation"
  fact installation PASS "the installation '${NAME_SEEN}' in GCP project ${GCP_SEEN} (region ${REGION_SEEN}), read from '${FUGARO_BIN} doctor --project ${INSTALLATION} --json'; every read below is '${FUGARO_BIN} runs ls' against that installation, and this check writes nothing there"
}

# no_pr_rows REASON — the two pull-request rows, when no pull request could be
# settled. Written as its own helper because the two rows always fail together:
# they are the same read with and without --no-cache.
no_pr_rows() {
  unmeasured query-pr "$1"
  unmeasured query-pr-no-cache "$1"
}

# step_warmup: the window's own listing, untimed. It is the read that warms the
# run cache for the timed `--since 7d` runs, and it is also where the pull
# requests this check times come from: the newest the window names when --pr was
# not given, and the oldest OTHER one for the sanity reading. Its own row is
# `warmup`.
step_warmup() {
  local rc=0 out why=""
  say "reading the window untimed, to warm the cache and to find the pull requests: ${FUGARO_BIN} ${LS[*]} --since ${SINCE} --json"
  run_call "$CALL_SECONDS" "$SCRATCH/window.json" "$SCRATCH/window.err" \
    "$FUGARO_BIN" "${LS[@]}" --since "$SINCE" --json || rc=$?
  if [ "$rc" -eq 124 ]; then
    why="the window listing never answered within ${CALL_SECONDS}s, so no query could be warmed and no pull request could be found. The call is '${FUGARO_BIN} ${LS[*]} --since ${SINCE} --json'"
  elif [ "$rc" -ne 0 ]; then
    why="the window listing was refused (exit ${rc}): $(last_line "$SCRATCH/window.err"). Nothing was warmed and no pull request was found"
  else
    rc=0
    out=$(reader rows "$SCRATCH/window.json") || rc=$?
    if [ "$rc" -ne 0 ]; then
      why="the window listing could not be read as the JSON this check knows (reader exit ${rc}), so nothing was warmed and no pull request was found: $(last_line "$SCRATCH/window.err")"
    fi
  fi
  if [ -n "$why" ]; then
    unmeasured warmup "$why"
    no_pr_rows "$why No pull request could be settled from it, so the two pull-request queries were not timed at all. Name one yourself and the repository it is in: bash scripts/live-checks/check-760-runs-ls-speed.sh --installation ${INSTALLATION} --pr <N> --repo <owner/name>"
    return 0
  fi
  ROWS_SEEN=$(field_of rows "$out")
  # Every pull request the window names, in the listing's own order (newest
  # first, so the first is the newest and the last is the oldest), with the
  # repository that pull request's rows carry. The reader has already matched
  # every value against its own vocabulary, so nothing here is a document's text.
  local tag pr rid repo entry
  CAND_PRS=""
  CAND_REPOS=""
  while read -r tag pr rid repo; do
    [ "$tag" = ROW ] || continue
    case "$pr" in "" | 0) continue ;; esac
    case " $CAND_PRS " in *" $pr "*) continue ;; esac
    CAND_PRS="${CAND_PRS}${CAND_PRS:+ }${pr}"
    case "$repo" in "" | -) ;; *) CAND_REPOS="${CAND_REPOS}${CAND_REPOS:+ }${pr}=${repo}" ;; esac
  done <<EOF
$out
EOF
  local newest="" p
  [ -n "$CAND_PRS" ] && newest=${CAND_PRS%% *}
  if [ -n "$PR_WANT" ]; then PR_TIMED=$PR_WANT; else PR_TIMED=$newest; fi
  if [ -z "$PR_TIMED" ]; then
    no_pr_rows "there is no pull request to time: the window (--since ${SINCE}) names none, and --pr was not given. Give one: bash scripts/live-checks/check-760-runs-ls-speed.sh --installation ${INSTALLATION} --pr <N> [--repo <owner/name>]"
  elif [ -n "$REPO" ]; then
    REPO_TIMED=$REPO
  else
    REPO_TIMED=$(repo_of_pr "$PR_TIMED" || true)
    if [ -z "$REPO_TIMED" ]; then
      no_pr_rows "the pull request ${PR_TIMED} is not among the ones the window (--since ${SINCE}) just named, so this check does not know its repository and 'runs ls --pr' needs one: give --repo <owner/name> and run it again. No figure for this pull request"
      PR_TIMED=""
    fi
  fi
  # The sanity reading's second pull request: the one the owner named, or the
  # oldest other the window holds. It is a reading, never a criterion, and a
  # window that names one pull request only leaves its row saying so.
  if [ -n "$OLD_PR_WANT" ]; then
    PR_OLD=$OLD_PR_WANT
    REPO_OLD=$REPO
    [ -n "$REPO_OLD" ] || REPO_OLD=$(repo_of_pr "$PR_OLD" || true)
  else
    PR_OLD=""
    REPO_OLD=""
    for p in $CAND_PRS; do
      [ "$p" = "$PR_TIMED" ] && continue
      PR_OLD=$p
      REPO_OLD=$(repo_of_pr "$p" || true)
    done
  fi
  # The base command for the pull-request reads: --repo is given when it is
  # known, and left out when it is not (the CLI resolves a config that names
  # exactly one repository itself, and says what to do when it cannot).
  LSPR=("${LS[@]}")
  [ -n "${REPO_TIMED:-}" ] && LSPR+=("--repo" "$REPO_TIMED")
  local newest_text="the window names no pull request" old_text="and no second pull request is named either" repo_text
  [ -n "$newest" ] && newest_text="the newest pull request it names is #${newest}"
  if [ -n "$OLD_PR_WANT" ]; then
    old_text="the second pull request is #${OLD_PR_WANT}, the one you named"
  elif [ -n "${PR_OLD:-}" ]; then
    old_text="the oldest other one it names is #${PR_OLD}"
  fi
  if [ -n "${REPO_TIMED:-}" ]; then
    repo_text="read --repo ${REPO_TIMED}, the repository the rows of the timed pull request name"
  else
    repo_text="read whatever repository the CLI resolves from the config, there being no rows of the timed pull request here to name one"
  fi
  fact warmup PASS "the run cache was warmed before the timed runs: '${FUGARO_BIN} ${LS[*]} --since ${SINCE} --json' ran once untimed and answered ${ROWS_SEEN:-?} row(s) — ${newest_text}, ${old_text} — and every query below is then run once untimed before its ${RUNS} timed runs, except '--pr N --no-cache', which reads the bucket by definition and is the one read nothing can warm. The pull-request queries ${repo_text}"
}

repo_of_pr() { # repo_of_pr PR — the repository that pull request's rows carry
  local want=$1 entry
  for entry in ${CAND_REPOS:-}; do
    case "$entry" in
    "$want"=*) printf '%s' "${entry#*=}"; return 0 ;;
    esac
  done
  return 1
}

step_timing() {
  time_query query-state-running "runs ls --state running" \
    "$BASELINE_STATE_RUNNING" "$TARGET" yes "${LS[@]}" --state running
  if [ -n "${PR_TIMED:-}" ]; then
    time_query query-pr "runs ls --pr ${PR_TIMED}" \
      "$BASELINE_PR" "$TARGET" yes ${LSPR[@]+"${LSPR[@]}"} --pr "$PR_TIMED"
    time_query query-pr-no-cache "runs ls --pr ${PR_TIMED} --no-cache" \
      "$BASELINE_PR_NO_CACHE" 0 no ${LSPR[@]+"${LSPR[@]}"} --pr "$PR_TIMED" --no-cache
  fi
  time_query query-since-7d "the default listing, runs ls --since ${SINCE}" \
    "$BASELINE_SINCE_7D" 0 yes "${LS[@]}" --since "$SINCE"
}

# step_gate: the one decision the figures carry. The plan's D2966 gates the
# optional RL4 on this single reading — a default listing that comes back under
# 8 s means RL4 is never built — so it is written as its own row rather than left
# to be read out of the query row above it.
step_gate() {
  local median=""
  [ -f "$SCRATCH/query-since-7d.median" ] && median=$(cat "$SCRATCH/query-since-7d.median" 2>/dev/null || true)
  case "$median" in '' | *[!0-9]*) median="" ;; esac
  if [ -z "$median" ]; then
    unmeasured rl4-gate "the RL4 gate cannot be read: the default listing (runs ls --since ${SINCE}) was not timed, so there is no figure to hold against the plan's $(limit_text "$GATE_MS") gate (D2966). The rows above stand on their own; this one is missing for the reason the query-since-7d row gives"
    return 0
  fi
  if [ "$median" -gt "$GATE_MS" ]; then
    fact rl4-gate PASS "the default listing's median is $(secs "$median") s, OVER the $(limit_text "$GATE_MS") the plan's D2966 makes the gate: RL4 IS WANTED — the optional task is built, and this reading, kept beside the query-since-7d row, is what asks for it"
  else
    fact rl4-gate PASS "the default listing's median is $(secs "$median") s, UNDER the $(limit_text "$GATE_MS") the plan's D2966 makes the gate: RL4 IS NOT WANTED — under 8 seconds means never, so the optional task is not built and this reading is the whole of the decision"
  fi
}

# step_rows: the sanity reading. Two pull requests' row sets are compared by
# count and run ids only, and each read says which path it took — the index, or
# the fallback scan the CLI's own note on stderr announces. Nothing here is a
# criterion: it is the reading that would catch a `--pr` answer coming from
# somewhere the owner did not expect, and a pull request whose runs predate the
# backfill (--old-pr N) is the one to give it.
step_rows() {
  if [ -z "${PR_TIMED:-}" ]; then
    unmeasured pr-rows "no pull request was timed, so there is no row set to read: the reason is the query-pr row above"
    return 0
  fi
  local ids count list note
  if ! ids=$(read_pr_rows "$PR_TIMED" "${REPO_TIMED:-}" "$SCRATCH/pr-new.json" "$SCRATCH/pr-new.err"); then
    unmeasured pr-rows "the rows of the pull request this check timed (#${PR_TIMED}) could not be read: $(last_line "$SCRATCH/pr-new.err")"
    return 0
  fi
  count=$(row_count "$ids")
  list=$(row_ids "$ids")
  note=$(note_of "$SCRATCH/pr-new.err")
  local second_text
  if [ -n "${PR_OLD:-}" ]; then
    local old_ids old_count old_list old_note old_repo
    old_repo=${REPO_OLD:-${REPO_TIMED:-}}
    if old_ids=$(read_pr_rows "$PR_OLD" "$old_repo" "$SCRATCH/pr-old.json" "$SCRATCH/pr-old.err"); then
      old_count=$(row_count "$old_ids")
      old_list=$(row_ids "$old_ids")
      old_note=$(note_of "$SCRATCH/pr-old.err")
      local which
      if [ -n "$OLD_PR_WANT" ]; then which="whose runs are the ones you named"; else which="the oldest other one the window holds"; fi
      second_text="The second pull request, #${PR_OLD}${old_repo:+ in ${old_repo}}, ${which}: ${old_count} row(s) — run ids ${old_list} — ${old_note}"
    else
      second_text="The second pull request, #${PR_OLD}, could not be read: $(last_line "$SCRATCH/pr-old.err")"
    fi
  else
    second_text="No second pull request was read: the window names one pull request only. Give --old-pr <N> — one whose runs are older than the backfill, so its rows come from the fallback scan — to take the comparison"
  fi
  fact pr-rows PASS "the pull request this check timed, #${PR_TIMED}${REPO_TIMED:+ in ${REPO_TIMED}}: ${count} row(s) — run ids ${list} — ${note}. ${second_text}. The counts and the run ids are the reading and the whole of it: they say each read answered with one pull request's own runs and which of the two paths (the pull-request index, or the fallback scan) each answer came from. Nothing of a row's content is read or compared"
}

read_pr_rows() { # read_pr_rows PR REPO JSON ERR — one `runs ls --pr` read, --json
  local want=$1 repo=$2 json=$3 err=$4
  local rc=0 args=("${LS[@]}")
  [ -n "$repo" ] && args+=("--repo" "$repo")
  run_call "$CALL_SECONDS" "$json" "$err" "$FUGARO_BIN" ${args[@]+"${args[@]}"} --pr "$want" --json || rc=$?
  [ "$rc" -eq 0 ] || return "$rc"
  rc=0
  reader rows "$json" || rc=$?
  return "$rc"
}

row_count() { printf '%s\n' "$1" | sed -n 's/^rows=//p' | head -n 1; }
row_ids() { # row_ids READER_OUTPUT — the run ids, sorted, or "none" for no rows
  local list
  list=$(printf '%s\n' "$1" | sed -n 's/^ROW [0-9]* //p' | awk '{ print $1 }' | sort | tr '\n' ' ')
  list=${list% }
  [ -n "$list" ] || list="none"
  printf '%s' "$list"
}

note_of() { # note_of ERR — which path the read took, in the CLI's own words
  local out
  out=$(reader note "$1" || true)
  case "$(field_of note "$out")" in
  no-entry) printf 'read through the FALLBACK SCAN (its stderr carried the CLI note that there is no pull-request index entry for that pull request)' ;;
  unreadable) printf 'read through the FALLBACK SCAN (its stderr carried the CLI note that the pull-request index could not be read)' ;;
  unread) printf 'the note on its stderr could not be read, so which path it took is not known' ;;
  *) printf 'read through the INDEX (no fallback note on its stderr)' ;;
  esac
}

# The keys the block prints, in the order it prints them, whatever happened: a
# run that stopped early leaves the later ones reading FAIL — not reached, which
# is what makes the block safe to diff between two runs.
BLOCK_KEYS=(xtrace agent-session installation belong warmup
  query-state-running query-pr query-pr-no-cache query-since-7d
  rl4-gate pr-rows restore)

verdict_of() {
  case "$1" in
  0) printf 'PASS' ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 760 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached: nothing measured this row (the step that writes it did not run, or the check stopped before it)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 760 RESULT: %s (exit %s)\n' "$(verdict_of "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  cat <<PLAN_EOF
Check 760 — how long 'fugaro runs ls' takes on a REAL installation.
This is the PLAN. --dry-run touches nothing: no call is made, nothing is read,
no file of yours is opened.

  installation   ${INSTALLATION}   (read with 'fugaro doctor', timed with 'fugaro runs ls')
  pull request   ${PR_WANT:-the newest the 7-day window names}
  second one     ${OLD_PR_WANT:-the oldest other pull request the window names}
  repository     ${REPO:-whatever repository the rows of the timed pull request name}
  runs per query ${RUNS}   (the median of them is the figure)
  deadline       ${CALL_SECONDS}s per call

What the run will do, in this order:
  1. read the installation with '${FUGARO_BIN} doctor --project ${INSTALLATION} --json' and refuse it if it
     names belong;
  2. read the window ONCE, untimed — '${FUGARO_BIN} runs ls --project ${INSTALLATION} --since ${SINCE} --json' —
     which warms the run cache and names the pull requests this check times;
  3. warm each query once more, untimed, and time it ${RUNS} times:
       '${FUGARO_BIN} runs ls --project ${INSTALLATION} --state running'                   baseline 20 s, target 5 s
       '${FUGARO_BIN} runs ls --project ${INSTALLATION} --repo <repo> --pr <N>'            baseline 23 s, target 5 s
       '${FUGARO_BIN} runs ls --project ${INSTALLATION} --repo <repo> --pr <N> --no-cache' baseline 32 s, no target
       '${FUGARO_BIN} runs ls --project ${INSTALLATION} --since ${SINCE}'                   baseline 15 s, no target
     and print each median beside its 2026-11-19 baseline;
  4. read the RL4 gate off the default listing's median: over 8 s means the optional task is
     built, under it means never (the plan's D2966);
  5. read two pull requests' rows — the one timed and a second, older one — with '--pr N --json',
     and say for each whether it came from the index or from the fallback scan, by count and run
     ids only: a sanity reading, never a criterion;
  6. write NOTHING: every call above is a read — no run is launched, no model is called, no
     bucket object is written — and the scratch directory is removed on the way out, however the
     run ends.

Nothing here fails on a slow figure: the readings are the point, and the exit status says
whether they were taken (0) or not (2).
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check-760-runs-ls-speed.sh [--installation <installation>] [--pr <N>] [--repo <owner/name>] [--old-pr <N>] [--dry-run]"

usage() {
  printf 'usage: %s\n' "$USAGE"
  printf '\n'
  printf 'Check 760 — how long `fugaro runs ls` takes on a real installation, and the reading\n'
  printf 'that decides whether the optional follow-up (RL4) is ever built. Read-only: it\n'
  printf 'launches no run and costs nothing billable, only wall clock.\n'
  printf '\n'
  printf '  --installation NAME   the installation to time (default fugaro; belong is refused)\n'
  printf '  --pr N                the pull request to time (default: the newest the window names)\n'
  printf '  --repo OWNER/NAME     the repository those pull-request reads are scoped to\n'
  printf '  --old-pr N            a second, older pull request for the sanity reading\n'
  printf '  --dry-run             print the plan and touch nothing\n'
  printf '\n'
  printf 'The full description, the exit codes and every knob (CHECK760_*) are the comment\n'
  printf 'block at the top of this file: read it before you run the check for real.\n'
}

check_repo() { # check_repo VALUE — OWNER/NAME, and nothing that could be a flag
  case "$1" in
  "" | -* | *[!A-Za-z0-9._/-]*)
    die "--repo '$1' is not a repository name. usage: $USAGE"
    ;;
  esac
  case "$1" in
  */*/* | /* | */)
    die "--repo '$1' is not a repository name: it is OWNER/NAME, with one slash. usage: $USAGE"
    ;;
  esac
  case "$1" in
  */*) ;;
  *) die "--repo '$1' is not a repository name: it is OWNER/NAME, with one slash. usage: $USAGE" ;;
  esac
}

check_pr() { # check_pr FLAG VALUE
  case "$2" in
  "" | *[!0-9]*) die "$1 '$2' is not a pull request number. usage: $USAGE" ;;
  esac
  [ "$2" -gt 0 ] || die "$1 '$2' is not a pull request number: it must be positive. usage: $USAGE"
}

parse_args() {
  INSTALLATION=${CHECK760_INSTALLATION:-fugaro}
  FUGARO_BIN=${CHECK760_FUGARO:-fugaro}
  REPO=${CHECK760_REPO:-}
  PR_WANT=${CHECK760_PR:-}
  OLD_PR_WANT=${CHECK760_OLD_PR:-}
  DRY_RUN=no
  NAME_SEEN=""
  GCP_SEEN=""
  REGION_SEEN=""
  PR_TIMED=""
  PR_OLD=""
  REPO_TIMED=""
  REPO_OLD=""
  CAND_PRS=""
  CAND_REPOS=""
  ROWS_SEEN=""
  LSPR=()
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation | --project)
      [ $# -ge 2 ] || die "$1 needs an installation name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs OWNER/NAME. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --pr)
      [ $# -ge 2 ] || die "--pr needs a pull request number. usage: $USAGE"
      PR_WANT=$2
      shift 2
      ;;
    --old-pr)
      [ $# -ge 2 ] || die "--old-pr needs a pull request number. usage: $USAGE"
      OLD_PR_WANT=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      usage
      REACHED_END=yes
      PRINTED=1
      trap - ERR
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation needs a name. usage: $USAGE"
  case "$INSTALLATION" in
  *[!A-Za-z0-9._-]*) die "the installation name '${INSTALLATION}' holds a character an installation name may not have. usage: $USAGE" ;;
  esac
  if [ -n "$PR_WANT" ]; then check_pr --pr "$PR_WANT"; fi
  if [ -n "$OLD_PR_WANT" ]; then check_pr --old-pr "$OLD_PR_WANT"; fi
  if [ -n "$REPO" ]; then check_repo "$REPO"; fi
}

# write_reader: the one python3 file the check parses its documents with. It
# prints a fixed vocabulary and nothing of a document's own text, so no string a
# listing carries can reach the block.
write_reader() {
  cat >"$SCRATCH/read760.py" <<'READER_EOF'
"""Check 760's reader: `fugaro runs ls --json`, `fugaro doctor --json`, and the
one stderr note that says which path a `--pr` read took.

One subcommand per question:
  doctor FILE   name=, gcp=, region=     the installation doctor answered with
  rows FILE     rows=, then one `ROW <pr> <run id> <repo>` line per row, in the
                document's own order (the CLI's is newest first)
  note FILE     note=none|no-entry|unreadable|unread

Every value printed is matched against the characters its own vocabulary allows
(a run id, a repository, an installation name, a number) and replaced by `-` when
it is anything else, so a document's text never reaches the block. Exit 3 means
the document is not what this reader knows; 2 means the call itself is wrong.
"""
import json
import re
import sys

RUN_ID = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._-]{0,63}\Z")
REPO = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._-]{0,63}/[A-Za-z0-9][A-Za-z0-9._-]{0,63}\Z")
NAME = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._-]{0,63}\Z")


def clean(value, pattern):
    text = value if isinstance(value, str) else ""
    return text if pattern.match(text) else "-"


def load(path):
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        return json.load(fh)


def cmd_doctor(path):
    try:
        doc = load(path)
    except (OSError, ValueError):
        return 3
    if not isinstance(doc, dict):
        return 3
    project = doc.get("project")
    if not isinstance(project, dict):
        return 3
    print("name=" + clean(project.get("name"), NAME))
    print("gcp=" + clean(project.get("gcp_project"), NAME))
    print("region=" + clean(project.get("region"), NAME))
    return 0


def cmd_rows(path):
    try:
        doc = load(path)
    except (OSError, ValueError):
        return 3
    if not isinstance(doc, dict):
        return 3
    runs = doc.get("runs")
    if not isinstance(runs, list):
        return 3
    print("rows=%d" % len(runs))
    for row in runs:
        if not isinstance(row, dict):
            return 3
        pr = row.get("pr")
        if not isinstance(pr, int) or isinstance(pr, bool) or pr < 0:
            pr = 0
        print("ROW %d %s %s" % (pr, clean(row.get("run_id"), RUN_ID), clean(row.get("repo"), REPO)))
    return 0


def cmd_note(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        print("note=unread")
        return 0
    if "no pull-request index for" in text:
        print("note=no-entry")
    elif "pull-request index of" in text and "could not be read" in text:
        print("note=unreadable")
    else:
        print("note=none")
    return 0


COMMANDS = {"doctor": cmd_doctor, "rows": cmd_rows, "note": cmd_note}


def main(argv):
    if len(argv) != 2 or argv[0] not in COMMANDS:
        sys.stderr.write("read760.py: usage: read760.py doctor|rows|note FILE\n")
        return 2
    return COMMANDS[argv[0]](argv[1])


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
READER_EOF
}

main() {
  parse_args "$@"
  # The figures are the plan's own readings of 2026-11-19 (warm run cache), and
  # the target and the gate are the plan's: --state running and --pr N under 5 s,
  # and the default listing over 8 s as the reading that asks for the optional
  # RL4 (D2966).
  RUNS=${CHECK760_RUNS:-3}
  CALL_SECONDS=${CHECK760_CALL_SECONDS:-900}
  TARGET=${CHECK760_TARGET_MS:-5000}
  GATE_MS=${CHECK760_GATE_MS:-8000}
  BASELINE_STATE_RUNNING=${CHECK760_BASELINE_STATE_RUNNING_MS:-20000}
  BASELINE_PR=${CHECK760_BASELINE_PR_MS:-23000}
  BASELINE_PR_NO_CACHE=${CHECK760_BASELINE_PR_NO_CACHE_MS:-32000}
  BASELINE_SINCE_7D=${CHECK760_BASELINE_SINCE_7D_MS:-15000}
  SINCE=7d
  if [ "${CHECK760_KEEP_SCRATCH:-}" = yes ]; then SCRATCH_KEPT=yes; fi
  if [ "${CHECK760_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  case "$RUNS" in '' | *[!0-9]*) die "CHECK760_RUNS '$RUNS' is not a number of runs. usage: $USAGE" ;; esac
  [ "$RUNS" -ge 1 ] || die "CHECK760_RUNS must be at least 1: a median of no runs is not a reading"
  case "$CALL_SECONDS" in '' | *[!0-9]*) die "CHECK760_CALL_SECONDS '$CALL_SECONDS' is not a number of seconds. usage: $USAGE" ;; esac
  [ "$CALL_SECONDS" -ge 1 ] || die "CHECK760_CALL_SECONDS must be at least 1"
  guard_belong

  if [ "$DRY_RUN" = yes ]; then
    # Before need_tools and before the scratch directory: the plan is the one
    # output an owner can have without the version under test on PATH at all.
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  need_tools
  scratch_up
  write_reader
  guard_xtrace
  guard_agent_session

  # The base command every read is built on. --project is always given: the
  # installation is the one this check was told to time, never whatever the
  # machine's config happens to name.
  LS=(runs ls --project "$INSTALLATION")

  step_installation
  step_warmup
  step_timing
  step_gate
  step_rows

  REACHED_END=yes
  trap - ERR
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
