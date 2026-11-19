#!/usr/bin/env bash
# Check 711 — the wall time and size of every read the console spawns.
#
# Plan task P1 of docs/console-improvements/implementation-plan.md writes this
# file and YOU run it; the plan's "Checks the plan takes" section is where the
# number is registered. What it measures is the half of the console-performance
# pass no hermetic test can take: the reads the console spawns are slow because
# the CLI queries its stores and assembles a whole document before it answers
# and nothing is cached (reconciliation F16, F19), so which of them are slow,
# and by how much, is a fact about your own installation. What the reading
# decides is which commands besides `runs ls` get an opt-in `--stream` (D2906;
# O3, answered 2026-11-18).
#
# The reads are the rows of the SPAWN_COMMANDS table in
# console/src/main/spawn.ts, and the argv of each is read out of that table: the
# `words` column is the command, and --project (and --config, when the
# installation declares one) go on every row that takes one, exactly as the
# console's own argvFor puts them there. Each row is run twice: once cold (its
# first run in this pass) and once warm, in the table's own order.
#
# THIS SCRIPT READS. The table's three write rows (`settings.edit`,
# `roadmap.move`, `run.launch`) are skipped by name, and the set of rows it runs
# is closed: a row neither list names stops the script with the fix rather than
# being run, so the write rows the plan's Manage tasks add to the table later
# can never be spawned here by accident.
#
# --self-test runs the whole pass against a stub `fugaro` this script builds in
# a temporary directory. The stub sleeps a declared time per command and prints
# a payload of a declared size, and the test fails unless the ranking the script
# prints puts the slowest stub first -- so "the three slowest reads" is a tested
# claim about the ranking rather than one read by eye. It touches no
# installation and needs no CLI.
#
# Every read is bounded, because a read that hangs is exactly the kind of
# finding this check is for: an ordinary one is killed at --limit-seconds (90 by
# default, the READ_TIMEOUT_MS the console itself gives a read), and a feed at
# --watch-seconds (5), the window its first line is waited for in. A row the
# bound ended, and a feed that said nothing inside its window, are printed as
# the bound they are with the note that says so, never as a time.
#
# Usage:      bash scripts/console-read-timings.sh [--project <name>] [--config <path>]
#                 [--fugaro <path>] [--watch-seconds N] [--limit-seconds N]
#                 [--top N] [--only <text>] [--set "<words>"]
#             bash scripts/console-read-timings.sh --self-test
#             bash scripts/console-read-timings.sh --help
#             With no --project the CLI resolves its own default (the
#             checkout's fugaro.yaml project:, else $FUGARO_PROJECT, else the
#             only project config on the machine), which is what the console's
#             reads resolve to on an installation it has not named either.
# Needs:      bash, mktemp, sed, awk, sort, wc, head, cut, tr and sleep, plus the
#             `fugaro` binary on PATH or named by --fugaro -- except under
#             --self-test, which uses the stub it writes itself.
# Where:      your own installation, READ ONLY. Everything a read writes is
#             captured into a temporary directory that is removed at the end;
#             nothing outside it is written, and no read is given a stdin (a
#             child that asked a question would fail on a closed stdin rather
#             than wait for an answer nobody is there to give).
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, and on stdout the table -- one line per read
#             (command, cold seconds, warm seconds, bytes) -- followed by the
#             slowest reads by cold time. docs/console-improvements/check-711.md
#             says what to type, what each column means and what to do with the
#             answer.
# Exit:       0 the pass measured (a row that refused is reported on its own
#               line, with the CLI's sentence);
#             1 --self-test measured a ranking other than the one its stub
#               declares;
#             2 stopped before it measured anything: a guard, a missing tool, a
#               scratch directory it could not make, or a table it could not
#               read.
set -Eeuo pipefail

# The seconds bash's own `time` prints carry the locale's decimal point, and the
# ranking sorts those numbers: the C locale makes it a dot.
export LC_ALL=C

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
die() { say "$*"; exit 2; }

# ------------------------------------------------------------------ the table
# The table's write rows, skipped by name: each changes your installation rather
# than reading it (`settings.edit` writes the key it edits, `roadmap.move`
# writes the roadmap record, `run.launch` starts a run), and this check measures
# reads.
WRITE_OPS="settings.edit roadmap.move run.launch"

# The read rows this script times, by operation name -- every row of the table
# but the three above. The *argv* of each is read from spawn.ts and never
# restated here; what this list holds is that a row is a read, because the table
# has no column that says so. It is closed on purpose: read_table stops on a row
# neither list names, which is what keeps "this script reads" true after the
# table gains the Manage tasks' write rows.
READ_OPS="live.watch budget.show spend.report runs.list runs.stats runs.summary
plan.status graph.read graph.watch run.diagnose run.logs run.logLink
secrets.inventory accounts.inventory models.list settings.read cli.version
environment.report installation.list coordinator.status coordinator.show
coordinator.intents managed.status policy.show repos.ls repos.status seats.read
members.list apply.pending"
# The two lists are matched with spaces around a name, so a list wrapped across
# lines has to be one line of spaces by the time anything looks at it.
READ_OPS=$(printf '%s' "$READ_OPS" | tr '\n' ' ')

# The rows whose child does not end by itself: `watch --json` and
# `graph --watch --json` are feeds, so what is measured for them is the time to
# the first line and the child is killed after --watch-seconds. A feed that says
# nothing inside that window is reported as a lower bound rather than a time.
STREAM_OPS="live.watch graph.watch"

# The floor the table's read count is held to: the table has always named more
# reads than this, so a parse that finds fewer has stopped reading the table
# rather than found a smaller console, and it says so instead of ranking the
# handful of rows it did see.
MIN_READS=20

# The stub `fugaro` of --self-test declares its sleep and its payload size here,
# and the self-test's expectations are these same variables: the stub and the
# assertion read one set of numbers, so they cannot drift into agreeing on an
# order the stub does not sleep in.
STUB_SLEEP_SLOWEST=${CHECK711_STUB_SLEEP_SLOWEST:-0.30}
STUB_SLEEP_SECOND=${CHECK711_STUB_SLEEP_SECOND:-0.25}
STUB_SLEEP_THIRD=${CHECK711_STUB_SLEEP_THIRD:-0.20}
STUB_SLEEP_REST=${CHECK711_STUB_SLEEP_REST:-0.02}
STUB_BYTES_SLOWEST=${CHECK711_STUB_BYTES_SLOWEST:-3000}
STUB_BYTES_REST=${CHECK711_STUB_BYTES_REST:-200}
STUB_DISPLAY_SLOWEST="runs ls --json"
STUB_DISPLAY_SECOND="report --json"
STUB_DISPLAY_THIRD="budget show --json"

# --------------------------------------------------------------------- state
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
SPAWN_TS="$ROOT/console/src/main/spawn.ts"

# What a read that never answered shows in place of a time: a dash, because
# there is no number there, and the note beside it says which refusal it was.
NO_SECONDS_SHOWN="--"

SELF_TEST_STUB_ONLY="Check 711: --self-test runs against the stub it writes, so it takes no"

SCRATCH=""
HELP=""
SELF_TEST=""
PROJECT=${CHECK711_PROJECT:-}
CONFIG=${CHECK711_CONFIG:-}
FUGARO_BIN=${CHECK711_FUGARO:-fugaro}
WATCH_SECONDS=${CHECK711_WATCH_SECONDS:-5}
LIMIT_SECONDS=${CHECK711_LIMIT_SECONDS:-90}
TOP=${CHECK711_TOP:-3}
ONLY=${CHECK711_ONLY:-}
SET_WORDS=${CHECK711_SET:-}
VERSION_LINE=""
INSTALL_DISPLAY=""

T_SECONDS=""
T_BYTES=""
T_STATUS=0
T_SENTENCE=""
T_NOTE=""
RUN_KILLED=""

SELF_FAILURES=""
self_fail() {
  SELF_FAILURES="${SELF_FAILURES}
  - $1"
}

usage() { sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

cleanup() {
  local status=$?
  trap - EXIT INT TERM HUP
  case "$SCRATCH" in
  "" | / | "${HOME:-}" | "${HOME:-}"/) : ;;
  *)
    if [ -d "$SCRATCH" ]; then rm -rf "$SCRATCH" 2>/dev/null || true; fi
    ;;
  esac
  exit "$status"
}

need_tools() {
  command -v mktemp >/dev/null 2>&1 || die "Check 711 needs mktemp and it is not on PATH"
  command -v sort >/dev/null 2>&1 || die "Check 711 needs sort and it is not on PATH"
  command -v awk >/dev/null 2>&1 || die "Check 711 needs awk and it is not on PATH"
  if ! command -v "$FUGARO_BIN" >/dev/null 2>&1; then
    case "$FUGARO_BIN" in
    */*) die "Check 711 needs the CLI at '$FUGARO_BIN' and there is nothing to run there: the reads it times are that CLI's, and a pass without it would measure nothing" ;;
    *) die "Check 711 needs '$FUGARO_BIN' on PATH (or --fugaro <path>): the reads it times are that CLI's, and a pass without it would measure nothing" ;;
    esac
  fi
}

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk711.XXXXXXXX") || die "Check 711 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/reads"
  : >"$SCRATCH/results"
  : >"$SCRATCH/ranking"
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "Check 711: --project needs a name"
      value_ok "$2" "--project"
      PROJECT=$2
      shift 2
      ;;
    --config)
      [ $# -ge 2 ] || die "Check 711: --config needs a path"
      value_ok "$2" "--config"
      CONFIG=$2
      shift 2
      ;;
    --fugaro)
      [ $# -ge 2 ] || die "Check 711: --fugaro needs a path"
      value_ok "$2" "--fugaro"
      FUGARO_BIN=$2
      shift 2
      ;;
    --watch-seconds)
      [ $# -ge 2 ] || die "Check 711: --watch-seconds needs a whole number of seconds"
      WATCH_SECONDS=$2
      shift 2
      ;;
    --limit-seconds)
      [ $# -ge 2 ] || die "Check 711: --limit-seconds needs a whole number of seconds"
      LIMIT_SECONDS=$2
      shift 2
      ;;
    --top)
      [ $# -ge 2 ] || die "Check 711: --top needs a whole number"
      TOP=$2
      shift 2
      ;;
    --only)
      [ $# -ge 2 ] || die "Check 711: --only needs a piece of an operation or of a command"
      ONLY=$2
      shift 2
      ;;
    --set)
      [ $# -ge 2 ] || die "Check 711: --set needs the words to append"
      SET_WORDS=$2
      shift 2
      ;;
    --self-test) SELF_TEST=1; shift ;;
    -h | --help) HELP=1; shift ;;
    *) die "Check 711: unknown argument '$1' (usage: $USAGE)" ;;
    esac
  done
  whole_number "$WATCH_SECONDS" --watch-seconds
  whole_number "$LIMIT_SECONDS" --limit-seconds
  whole_number "$TOP" --top
  if [ -n "$SELF_TEST" ]; then
    # The stub answers whatever it is asked, so a knob that would aim a read at
    # an installation, widen one or append an argument would be silently
    # ignored: refused instead, with the run that does apply.
    [ -z "$PROJECT" ] || die "$SELF_TEST_STUB_ONLY --project. Run the pass without --self-test to measure your own installation"
    [ -z "$CONFIG" ] || die "$SELF_TEST_STUB_ONLY --config. Run the pass without --self-test to measure your own installation"
    [ -z "$ONLY" ] || die "$SELF_TEST_STUB_ONLY --only. Run the pass without --self-test to measure your own installation"
    [ -z "$SET_WORDS" ] || die "$SELF_TEST_STUB_ONLY --set. Run the pass without --self-test to measure your own installation"
  fi
}

# whole_number VALUE FLAG: a count or a number of seconds, refused by the flag
# that carries it rather than read as zero.
whole_number() {
  case "$1" in
  '' | *[!0-9]*) die "Check 711: $2 takes a whole number, not '$1'" ;;
  esac
  [ "$1" -ge 1 ] || die "Check 711: $2 has to be at least 1"
}

# value_ok VALUE FLAG: an argument of this script's own that begins with `-`
# would be read by the CLI as the next flag of the command line rather than as
# the value it is, so it is refused here, where the fix is still obvious.
value_ok() {
  case "$1" in
  "") die "Check 711: $2 needs a value" ;;
  -*) die "Check 711: $2 got '$1', which begins with '-': the CLI would read it as a flag rather than as a value. Quote the value or pick another" ;;
  esac
}

# ------------------------------------------------------------------ the parse
# table_rows: one line per row of the table in spawn.ts, as operation|words|
# takes -- the three columns this pass needs, read out of the table's own text.
# The range is the table, the columns are the lines each row writes them on, and
# a row that stops writing one of them stops being a row here, which is what
# makes the drift checks in read_table the thing that notices a shape change.
table_rows() {
  awk -v q="'" '
    /^export const SPAWN_COMMANDS/, /^\] as const satisfies/ {
      if ($0 ~ /^[[:space:]]*operation:/) {
        op = $0
        sub(/^[[:space:]]*operation:[[:space:]]*/, "", op)
        gsub(q, "", op)
        sub(/,[[:space:]]*$/, "", op)
        next
      }
      if ($0 ~ /^[[:space:]]*words:/) {
        words = $0
        sub(/^[[:space:]]*words:[[:space:]]*/, "", words)
        gsub(q, "", words)
        gsub(/[][]/, "", words)
        gsub(/,/, "", words)
        next
      }
      if ($0 ~ /^[[:space:]]*takesProject:[[:space:]]*true/) {
        print op "|" words "|true"
        next
      }
      if ($0 ~ /^[[:space:]]*takesProject:[[:space:]]*false/) {
        print op "|" words "|false"
      }
    }
  ' "$SPAWN_TS"
}

# read_table fills $SCRATCH/reads with one line per read: operation|words|takes.
# The rows come from spawn.ts and the classification comes from the two lists
# above, and both halves are checked: a table row neither list names stops the
# script, and a read the table no longer holds stops it too, because a list that
# has drifted from the table would time the wrong thing quietly.
read_table() {
  [ -f "$SPAWN_TS" ] \
    || die "Check 711 stopped: $SPAWN_TS is not there, and the reads it times are the rows of its SPAWN_COMMANDS table. Run this script from a checkout of this repository"
  local op words takes kind name rows=0
  while IFS='|' read -r op words takes; do
    [ -n "$op" ] || continue
    [ -n "$words" ] || die "Check 711 stopped: the table's row for $op carries no command words, so there is nothing to run"
    kind=unknown
    case " $READ_OPS " in *" $op "*) kind=read ;; esac
    if [ "$kind" = unknown ]; then
      case " $WRITE_OPS " in *" $op "*) kind=write ;; esac
    fi
    case "$kind" in
    read)
      printf '%s|%s|%s\n' "$op" "$words" "$takes" >>"$SCRATCH/reads"
      rows=$((rows + 1))
      ;;
    write) say "skipping $op: it is a write row of the table, and this check reads only" ;;
    *)
      die "Check 711 stopped: the table has a row this script does not know ($op). The reads it times are a closed list: add the row to READ_OPS if it changes nothing, or to WRITE_OPS if it writes, and run it again -- a new row is a new command, and this check must never spawn a write by accident"
      ;;
    esac
  done < <(table_rows)
  [ "$rows" -ge "$MIN_READS" ] \
    || die "Check 711 stopped: the SPAWN_COMMANDS table in $SPAWN_TS named only $rows reads and this script has always found at least $MIN_READS, so the parse above is no longer reading the whole table and a shape change is what stopped it"
  for name in $READ_OPS; do
    grep -q "^$name|" "$SCRATCH/reads" \
      || die "Check 711 stopped: $name is on the list of reads this script times and the table has no such row. The two have drifted, and the read would be timed as nothing: fix the list, or the table"
  done
  say "reads: $rows rows of $SPAWN_TS (the writes $(printf '%s' "$WRITE_OPS" | tr ' ' ',') are skipped by name)"
}

# ------------------------------------------------------------------ the runner
is_stream() { case " $STREAM_OPS " in *" $1 "*) return 0 ;; esac; return 1; }

# run_bounded MODE WINDOW OUT ERR BIN ARGS...: runs one read with a bound on how
# long it may take, and leaves RUN_KILLED=yes when the bound is what ended it.
# MODE=exit lets the child end by itself (an ordinary read: the console bounds
# its own at READ_TIMEOUT_MS and a read that outlives that is a finding, not a
# reason to sit here); MODE=line also ends it as soon as it has written a line
# (a feed, whose first line is what the pane waits for). The wait is a tick loop
# rather than `timeout`, which is GNU's and not on your machine. The exit status
# is the child's own when it ended by itself, so a read that died on a refusal
# is reported as one.
run_bounded() {
  local mode=$1 window=$2 out=$3 err=$4
  shift 4
  RUN_KILLED=no
  local marker="$out.killed"
  rm -f "$marker"
  "$@" >"$out" 2>"$err" </dev/null &
  local pid=$!
  # The bound is a watchdog rather than a poll of the read: the read is then
  # waited for exactly, so the seconds this function is timed at are the read's
  # own and not a poll's overshoot. The watchdog naps on a background `sleep` it
  # can end the moment the read is done -- a traps-and-wait watchdog, so that
  # killing it here takes effect at once and leaves no `sleep` behind, and so
  # that it holds no copy of the script's own traps, which must not clean up the
  # scratch directory out from under the pass.
  (
    trap 'kill "$nap" 2>/dev/null; exit 0' TERM INT HUP
    sleep "$window" &
    nap=$!
    wait "$nap" 2>/dev/null
    if kill -0 "$pid" 2>/dev/null; then
      : >"$marker"
      kill "$pid" 2>/dev/null
    fi
  ) &
  local watchdog=$!
  if [ "$mode" = line ]; then
    local ticks=$((window * 50)) i=0
    while [ "$i" -lt "$ticks" ]; do
      if [ -s "$out" ]; then
        break
      fi
      if ! kill -0 "$pid" 2>/dev/null; then
        break
      fi
      sleep 0.02
      i=$((i + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null
    fi
  fi
  local status=0
  wait "$pid" 2>/dev/null || status=$?
  kill "$watchdog" 2>/dev/null || true
  wait "$watchdog" 2>/dev/null || true
  if [ -f "$marker" ]; then
    RUN_KILLED=yes
  fi
  rm -f "$marker"
  return "$status"
}

# normalise_seconds TEXT: bash's `time` answer as a number, or nothing when the
# shell answered with something that is not one (a cwd that is not there, a
# child that never started).
normalise_seconds() {
  local text=${1/,/.}
  case "$text" in
  '' | *[!0-9.]*) printf '' ;;
  *) printf '%s' "$text" ;;
  esac
}

# is_seconds TEXT: whether TEXT is a number bash's `time` could have printed
# and not a dash or a sentence (the self-test's own check on its ranking).
is_seconds() {
  awk -v value="$1" 'BEGIN { exit !(value ~ /^[0-9]+(\.[0-9]+)?$/ && value + 0 > 0) }'
}

bytes_of() {
  if [ -f "$1" ]; then
    wc -c <"$1" | tr -d ' '
  else
    printf '0'
  fi
}

first_line() {
  if [ -s "$1" ]; then
    head -n 1 "$1" | cut -c1-200
  fi
}

# time_one PHASE OPERATION WORDS TAKES: runs one read once and leaves the
# outcome in T_SECONDS, T_BYTES, T_STATUS, T_NOTE. The seconds are bash's own
# `time` (TIMEFORMAT %R) around the spawn, which is what the console's read
# costs the person waiting for it.
time_one() {
  local phase=$1 op=$2 words=$3 takes=$4
  local out="$SCRATCH/$phase.$op.out" err="$SCRATCH/$phase.$op.err" clock="$SCRATCH/$phase.$op.time"
  local -a argv=()
  local w
  for w in $words; do argv+=("$w"); done
  if [ "$takes" = true ]; then
    if [ -n "$PROJECT" ]; then argv+=(--project "$PROJECT"); fi
    if [ -n "$CONFIG" ]; then argv+=(--config "$CONFIG"); fi
  fi
  for w in $SET_WORDS; do argv+=("$w"); done
  T_STATUS=0
  TIMEFORMAT='%R'
  if is_stream "$op"; then
    { time run_bounded line "$WATCH_SECONDS" "$out" "$err" "$FUGARO_BIN" "${argv[@]}"; } 2>"$clock" || T_STATUS=$?
  else
    { time run_bounded exit "$LIMIT_SECONDS" "$out" "$err" "$FUGARO_BIN" "${argv[@]}"; } 2>"$clock" || T_STATUS=$?
  fi
  T_SECONDS=$(normalise_seconds "$(head -n 1 "$clock" 2>/dev/null || true)")
  T_BYTES=$(bytes_of "$out")
  T_SENTENCE=$(first_line "$err")
  T_NOTE=""
  if is_stream "$op"; then
    if [ -s "$out" ]; then
      T_NOTE="first line, killed after ${WATCH_SECONDS}s"
    elif [ "$T_STATUS" -ne 0 ]; then
      T_SECONDS=""
      T_NOTE="refused: ${T_SENTENCE:-the feed ended on exit $T_STATUS without a line}"
    else
      T_NOTE="no line within ${WATCH_SECONDS}s: the seconds are that bound and not a time"
    fi
  elif [ "$T_STATUS" -ne 0 ] && [ "$RUN_KILLED" = yes ]; then
    T_NOTE="no answer within ${LIMIT_SECONDS}s (killed): the seconds are that bound and not a time"
  elif [ "$T_STATUS" -ne 0 ]; then
    T_SECONDS=""
    T_NOTE="refused (exit $T_STATUS): ${T_SENTENCE:-no sentence on stderr}"
  fi
}

# measure_all runs every read twice -- the cold pass first for every row, then
# the warm one -- so "cold" is every read's first run in this script rather than
# the run that happened to follow another row's own warming. The rows are the
# table's own order, and the ranking is written as seconds|bytes|display.
measure_all() {
  local op words takes display cold_s cold_b cold_status warm_s warm_b size
  local cold_note warm_note
  while IFS='|' read -r op words takes; do
    if [ -n "$ONLY" ]; then
      case "$op $words" in
      *"$ONLY"*) ;;
      *) continue ;;
      esac
    fi
    display=$words
    if [ "$takes" = true ]; then
      if [ -n "$PROJECT" ]; then display="$display --project $PROJECT"; fi
      if [ -n "$CONFIG" ]; then display="$display --config $CONFIG"; fi
    fi
    if [ -n "$SET_WORDS" ]; then display="$display $SET_WORDS"; fi

    time_one cold "$op" "$words" "$takes"
    cold_s=$T_SECONDS
    cold_b=$T_BYTES
    cold_status=$T_STATUS
    cold_note=$T_NOTE
    time_one warm "$op" "$words" "$takes"
    warm_s=$T_SECONDS
    warm_b=$T_BYTES
    warm_note=$T_NOTE
    size=$cold_b
    if [ "$T_STATUS" -eq 0 ]; then size=$warm_b; fi
    say "$display: cold ${cold_s:-($cold_status)}s, warm ${warm_s:-($T_STATUS)}s, ${size} bytes"
    printf '%s|%s|%s|%s|%s|%s\n' "$display" "${cold_s:--}" "${warm_s:--}" "$size" "$cold_note" "$warm_note" >>"$SCRATCH/results"
    if [ -n "$cold_s" ]; then
      printf '%s|%s|%s\n' "$cold_s" "$size" "$display" >>"$SCRATCH/ranking"
    fi
  done <"$SCRATCH/reads"
  sort -t'|' -k1,1nr -k3,3 "$SCRATCH/ranking" >"$SCRATCH/ranking.sorted"
}

# ------------------------------------------------------------------ the report
read_version() {
  local out="$SCRATCH/version.out" err="$SCRATCH/version.err" answer=""
  run_bounded exit "$LIMIT_SECONDS" "$out" "$err" "$FUGARO_BIN" version >/dev/null 2>&1 || true
  if [ -s "$out" ]; then
    answer=$(head -n 1 "$out")
  fi
  VERSION_LINE=${answer:-"the CLI answered nothing to 'version'"}
}

print_report() {
  local reads ranked display cold warm size cold_note warm_note
  reads=$(wc -l <"$SCRATCH/results" | tr -d ' ')
  ranked=$(wc -l <"$SCRATCH/ranking.sorted" | tr -d ' ')
  printf '\nCHECK 711 — the wall time and size of every read the console spawns (reads only: nothing is written)\n'
  printf 'binary:       %s (%s)\n' "$FUGARO_BIN" "$VERSION_LINE"
  if [ -n "$PROJECT" ]; then
    printf 'installation: %s\n' "$PROJECT"
  else
    printf 'installation: the CLI'"'"'s own default (the checkout'"'"'s fugaro.yaml project:, else $FUGARO_PROJECT, else the only project config)\n'
  fi
  printf 'reads run:    %s (cold, then warm, in the table'"'"'s own order)\n' "$reads"
  printf '\n%-56s %9s %9s %10s  %s\n' READ COLD WARM BYTES NOTE
  while IFS='|' read -r display cold warm size cold_note warm_note; do
    [ -n "$display" ] || continue
    case "$cold" in -) cold=$NO_SECONDS_SHOWN ;; *) cold="${cold}s" ;; esac
    case "$warm" in -) warm=$NO_SECONDS_SHOWN ;; *) warm="${warm}s" ;; esac
    printf '%-56s %9s %9s %10s  %s\n' "$display" "$cold" "$warm" "$size" "$cold_note"
  done <"$SCRATCH/results"
  printf '\nTHREE SLOWEST BY COLD TIME (--top changes the count; the first one or two decide which commands want --stream)\n'
  if [ "$ranked" -eq 0 ]; then
    printf '  nothing was ranked: no read answered here, so the note column above is the whole answer\n'
  else
    local i=0 seconds bytes
    head -n "$TOP" "$SCRATCH/ranking.sorted" | while IFS='|' read -r seconds bytes display; do
      i=$((i + 1))
      printf '  %s. %-56s %9ss  %10s bytes\n' "$i" "$display" "$seconds" "$bytes"
    done
  fi
  printf '\nWhat each column means, and what to do with the answer: docs/console-improvements/check-711.md\n'
}

# ------------------------------------------------------------------ self-test
# write_stub builds the stub `fugaro` of --self-test. It reads its declared
# sleeps and payload sizes from the environment this script exports, which are
# the same variables the assertions below use.
write_stub() {
  STUB="$SCRATCH/stub-fugaro"
  cat >"$STUB" <<'STUB_EOF'
#!/usr/bin/env bash
# The built-in stub `fugaro` of Check 711's --self-test: it sleeps a declared
# time per command and prints a payload of a declared size, so the ranking the
# script prints can be held to a known order. It reads nothing, writes nothing
# and always answers 0.
set -u
sleep_seconds="${CHECK711_STUB_SLEEP_REST:-0.02}"
payload_bytes="${CHECK711_STUB_BYTES_REST:-200}"
case "${1:-} ${2:-}" in
'runs ls')
  sleep_seconds="${CHECK711_STUB_SLEEP_SLOWEST:-0.30}"
  payload_bytes="${CHECK711_STUB_BYTES_SLOWEST:-3000}"
  ;;
'report --json') sleep_seconds="${CHECK711_STUB_SLEEP_SECOND:-0.25}" ;;
'budget show') sleep_seconds="${CHECK711_STUB_SLEEP_THIRD:-0.20}" ;;
esac
sleep "$sleep_seconds"
printf '%s' "$(head -c "$payload_bytes" /dev/zero | tr '\0' '#')"
printf '\n'
STUB_EOF
  chmod 700 "$STUB"
}

run_self_test() {
  local stub=$SCRATCH/stub-fugaro
  write_stub
  export CHECK711_STUB_SLEEP_SLOWEST="$STUB_SLEEP_SLOWEST"
  export CHECK711_STUB_SLEEP_SECOND="$STUB_SLEEP_SECOND"
  export CHECK711_STUB_SLEEP_THIRD="$STUB_SLEEP_THIRD"
  export CHECK711_STUB_SLEEP_REST="$STUB_SLEEP_REST"
  export CHECK711_STUB_BYTES_SLOWEST="$STUB_BYTES_SLOWEST"
  export CHECK711_STUB_BYTES_REST="$STUB_BYTES_REST"
  FUGARO_BIN=$stub
  PROJECT=""
  CONFIG=""
  ONLY=""
  SET_WORDS=""
  # Long enough that the stub's own sleeps are what the cold pass measures and
  # not this window: the stub answers a feed at once.
  WATCH_SECONDS=2
  say "self-test: the pass runs against the stub $stub (sleeps ${STUB_SLEEP_SLOWEST}s, ${STUB_SLEEP_SECOND}s, ${STUB_SLEEP_THIRD}s and ${STUB_SLEEP_REST}s; payloads ${STUB_BYTES_SLOWEST} and ${STUB_BYTES_REST} bytes), and the ranking it prints is held to that order"
  read_table
  measure_all
  read_version
  print_report
  self_test_verdict
}

# self_test_verdict holds the printed ranking to the order the stub declares.
# The ranking is the script's own answer, so this is what makes "the three
# slowest reads" a tested claim: a ranking that is not sorted, that counts a row
# twice, that loses a row, that reads no bytes or that ranks the stub's fastest
# command first all fail here.
self_test_verdict() {
  local ranked parsed top3 want biggest seconds bytes display
  ranked=$(wc -l <"$SCRATCH/ranking.sorted" | tr -d ' ')
  parsed=$(wc -l <"$SCRATCH/reads" | tr -d ' ')
  if [ "$ranked" -ne "$parsed" ] || [ "$ranked" -lt "$MIN_READS" ]; then
    self_fail "the ranking holds $ranked rows and the table names $parsed reads (and the table names $MIN_READS or more): a read that did not rank is a read the table has and this pass did not measure"
  fi

  awk -v a="$STUB_SLEEP_SLOWEST" -v b="$STUB_SLEEP_SECOND" -v c="$STUB_SLEEP_THIRD" \
    'BEGIN { exit !((a + 0) > (b + 0) && (b + 0) > (c + 0)) }' \
    || self_fail "the stub's own sleep order is not strictly slowest-to-fastest (${STUB_SLEEP_SLOWEST}s, ${STUB_SLEEP_SECOND}s, ${STUB_SLEEP_THIRD}s), so the expectation below could not tell a sorted ranking from an unsorted one"

  top3=$(head -n 3 "$SCRATCH/ranking.sorted" | cut -d'|' -f3 | tr '\n' '|')
  want="$STUB_DISPLAY_SLOWEST|$STUB_DISPLAY_SECOND|$STUB_DISPLAY_THIRD|"
  if [ "$top3" != "$want" ]; then
    self_fail "the three slowest are '${top3}' and the stub sleeps longest for '$want': the slowest stub has to be ranked first, or the ranking the owner reads is not the ranking the machine measured (--only, --set or an unsorted sort would each do this)"
  fi

  if ! awk -F'|' 'NR > 1 && ($1 + 0) > (previous + 0) { bad = 1 } { previous = $1 } END { exit(bad ? 1 : 0) }' "$SCRATCH/ranking.sorted"; then
    self_fail "the ranking is not sorted by cold time: a row is slower than the row above it, so the three at the top are not the three slowest"
  fi

  while IFS='|' read -r seconds bytes display; do
    [ -n "$display" ] || continue
    if ! is_seconds "$seconds"; then
      self_fail "the row for '$display' has no cold time ('$seconds'): the clock or the parse of it did not answer"
    fi
    case "$bytes" in
    '' | *[!0-9]*) self_fail "the row for '$display' has no byte count ('$bytes'): the bytes a read wrote were not read" ;;
    0) self_fail "the row for '$display' read 0 bytes and the stub always prints a payload" ;;
    esac
  done <"$SCRATCH/ranking.sorted"

  biggest=$(awk -F'|' 'NR == 1 { size = $2; display = $3 } ($2 + 0) > (size + 0) { size = $2; display = $3 } END { print display }' "$SCRATCH/ranking.sorted")
  if [ "$biggest" != "$STUB_DISPLAY_SLOWEST" ]; then
    self_fail "the biggest payload is '$biggest' and the stub prints it for '$STUB_DISPLAY_SLOWEST' (${STUB_BYTES_SLOWEST} bytes against ${STUB_BYTES_REST}): the byte column is not the size of what the read wrote"
  fi

  if [ -z "$SELF_FAILURES" ]; then
    printf '\nCHECK 711 SELF-TEST: PASS — the ranking printed above is the one the stub declares: %s first at %ss, then %s at %ss, then %s at %ss\n' \
      "$STUB_DISPLAY_SLOWEST" "$STUB_SLEEP_SLOWEST" "$STUB_DISPLAY_SECOND" "$STUB_SLEEP_SECOND" "$STUB_DISPLAY_THIRD" "$STUB_SLEEP_THIRD"
    return 0
  fi
  printf '\nCHECK 711 SELF-TEST: FAIL%s\n' "$SELF_FAILURES"
  return 1
}

# ----------------------------------------------------------------------- main
USAGE="console-read-timings.sh [--project <name>] [--config <path>] [--fugaro <path>] [--watch-seconds N] [--limit-seconds N] [--top N] [--only <text>] [--set \"<words>\"] [--self-test]"

main() {
  parse_args "$@"
  if [ -n "$HELP" ]; then
    usage
    return 0
  fi
  scratch_up
  if [ -n "$SELF_TEST" ]; then
    run_self_test
    return $?
  fi
  need_tools
  read_table
  read_version
  if [ -n "$ONLY" ]; then
    say "only the rows matching '$ONLY' are run"
  fi
  measure_all
  if [ "$(wc -l <"$SCRATCH/results" | tr -d ' ')" -eq 0 ]; then
    die "Check 711 stopped: --only '$ONLY' matched no row of the table, so nothing was measured"
  fi
  print_report
  return 0
}

trap cleanup EXIT
trap 'exit 130' INT TERM HUP
main "$@"
