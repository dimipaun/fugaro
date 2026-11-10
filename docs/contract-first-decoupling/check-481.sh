#!/usr/bin/env bash
# Check 481 — the gain is real (owner-run, optional).
#
# Plan task CD5 of docs/contract-first-decoupling/implementation-plan.md writes
# this file, and YOU run it; the plan's own "Checks (480-489 reserved; 480 and 481
# used)" section is where this number is registered. It puts the pass's own figure
# beside the clock: the plan's gain as
# `fugaro plan graph` computes it, and the wall clock of the same work run both
# ways — one batch in the shape the pass wrote (an interface-only dependency
# broken with a contract) and one in the shape the plan had before it (the
# dependency kept). The weighted figure is only as honest as the `size` the
# planner wrote, and this is how that is learnt.
#
# The wall clock is OBSERVED, not read: no listing carries a run's end time, so
# this check watches each batch's own rows until every one of them is terminal
# and takes the span from the earliest start it saw to the poll that saw the last
# one settle. That is why it is started while the batches are running: a batch
# that is already over when the check first looks cannot be measured after the
# fact, and its row says so (UNVERIFIED) rather than reporting a number nobody
# measured. The accuracy is the poll interval, which the row prints.
#
# The check launches NOTHING and spends NOTHING: it reads the plan through the
# CLI, lists runs, and prints figures. The two batches are yours to launch, with
# the batch names you pass here.
#
# Usage:      bash docs/contract-first-decoupling/check-481.sh <plan> [--decoupled-batch NAME]
#                 [--sequential-batch NAME] [--poll SECONDS] [--wait SECONDS]
#             Run it from the checkout whose plan is being measured, right after
#             you launch the two batches. -h/--help prints this block.
# Needs:      bash, mktemp, jq and fugaro on PATH.
# Where:      nothing is written anywhere but a scratch directory this script
#             removes; no repository, project or installation is touched.
# Costs:      nothing: no job, no build, no model call. The wall clock it reports
#             is the cost of the batches YOU launched.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 481 RESULT,
#             whose last line is the verdict and the exit status:
#
#               CHECK 481 RESULT: PASS (exit 0)        the plan's break pays and
#                                                      both batches' wall clock
#                                                      was observed;
#               CHECK 481 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed (the read
#                                                      reports no gain, or a
#                                                      batch ended in runs that
#                                                      did not finish);
#               CHECK 481 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 481 RESULT: INCOMPLETE (exit 3)  it ran to the end with a
#                                                      batch whose wall clock it
#                                                      could not observe (a
#                                                      batch already over, or one
#                                                      that never appeared).
#
#             A row reading `FAIL — not reached` was NOT measured.
# Cleanup:    always: the scratch directory goes, and the `restore` row says
#             whether it really did.
#
# The one thing this check cannot do for you is the planning: the plan has to be
# a real interface-only dependency written with the `parallelism` skill, and the
# two batches have to run the same work both ways. A plan that broke nothing has
# no gain to report and no before graph to weigh, and the check stops there.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace prints the rows
# this check reads, into the log you would then paste.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; ABORT_REASON=$*; exit 2; }

ABORT_REASON=""
REACHED_END=no
HELP=""
CLEANUP_RUNNING=no
FACTS_FILE=""

fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the gain" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 481 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 481 needs bash"
  need mktemp
  need jq "the gain and the run rows are read by jq, so no figure in the block is read by eye"
  need fugaro "the plan is read through 'fugaro plan graph' and the batches through 'fugaro runs ls'"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk481-mine"
SCRATCH_REMOVED=no
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk481.XXXXXXXX") || die "Check 481 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  CLEANUP_RUNNING=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
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
  cleanup
  if [ -n "$HELP" ]; then exit 0; fi
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the gain"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ] || [ "$status" -eq 3 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"). This check launched nothing, spent nothing and wrote nothing outside it"
  fi
  # The restore row exists only now, so the status the steps decided is re-decided
  # here: a FAIL row is 1, a row nobody could measure (UNVERIFIED) is 3 — a block
  # that measured most of its criteria and left one unmeasured is not a pass.
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=1
    elif printf '%s\n' "$facts" | grep -q '|UNVERIFIED|'; then
      status=3
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The word the block ends on follows the exit status, not the rows: a run that
# stopped early has rows reading FAIL — not reached, and printing FAIL there
# would offer a row nobody measured as a verdict about the gain.
verdict_word() {
  case "$1" in
  0) printf 'PASS\n' ;;
  1) printf 'FAIL\n' ;;
  *) printf 'INCOMPLETE\n' ;;
  esac
}

BLOCK_KEYS=(setup gain pays decoupled-batch sequential-batch comparison restore)

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 481 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 481 RESULT: %s (exit %s)\n' "$(verdict_word "$status")" "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on, which prints the plan's figures and the run rows into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash docs/contract-first-decoupling/check-481.sh <plan>"
  fi
  fact setup PASS "bash, jq and fugaro are on PATH; this shell is not tracing; the plan is $PLAN and the batches watched are '$DECOUPLED_BATCH' and '$SEQUENTIAL_BATCH'"
}

# ------------------------------------------------------------------ the gain
# read_gain: the plan's gain, computed by the read from the document's own
# record of the break and the sizes — never from a figure a planner wrote, since
# `gain` is not a key the artifact may carry (D1811, D1816). A graph that broke
# no edge has no gain: there is no before graph to weigh and nothing to compare.
read_gain() {
  GRAPH="$SCRATCH/graph.json"
  if ! fugaro plan graph --plan "$PLAN" --json >"$GRAPH" 2>"$SCRATCH/graph.err"; then
    stop "Check 481 stopped: 'fugaro plan graph --plan $PLAN --json' refused the plan — $(tr '\n' ' ' <"$SCRATCH/graph.err")"
  fi
  if [ "$(jq -r 'has("gain") and (.gain != null)' "$GRAPH")" != true ]; then
    fact gain FAIL "the plan records no broken edge, so the read computes no gain: this check compares the estimate of a break with the wall clock of the two shapes, and a plan that broke nothing has neither. Plan the dependency with the pass first"
    stop "the plan records no break, so there is no gain to measure"
  fi
  BROKEN=$(jq -r '.gain.broken' "$GRAPH")
  KEPT=$(jq -r '.gain.kept' "$GRAPH")
  ADDED=$(jq -r '.gain.added_tasks' "$GRAPH")
  BEFORE_TASKS=$(jq -r '.gain.before.tasks' "$GRAPH")
  BEFORE_WEIGHT=$(jq -r '.gain.before.weight' "$GRAPH")
  AFTER_TASKS=$(jq -r '.gain.after.tasks' "$GRAPH")
  AFTER_WEIGHT=$(jq -r '.gain.after.weight' "$GRAPH")
  IMPROVES=$(jq -r '.gain.improves' "$GRAPH")
  local stubs
  stubs=$(jq -r '[.nodes[] | (.stubs_present // []) | length] | add // 0' "$GRAPH")
  fact gain PASS "computed by the read: $BROKEN edge(s) broken, $KEPT kept, $ADDED task(s) added; the critical path before $BEFORE_TASKS task(s) weighing $BEFORE_WEIGHT, after $AFTER_TASKS task(s) weighing $AFTER_WEIGHT$([ "$stubs" -gt 0 ] && printf '; %s stub(s) still in the checkout' "$stubs")"
  if [ "$IMPROVES" = true ]; then
    fact pays PASS "the read reports the break pays: the weighted path after ($AFTER_WEIGHT) is below the one before ($BEFORE_WEIGHT). Whether the plan got faster is what the two batches below say"
  else
    fact pays FAIL "the read reports no gain: the weighted path after ($AFTER_WEIGHT) is not below the one before ($BEFORE_WEIGHT), so this break lengthens the plan — F5's case, and one to undo whatever the task count says. The two batches below are then two shapes of a break the plan should not have taken"
  fi
}

# --------------------------------------------------------------- the batches
fmt_duration() {
  local s=$1
  printf '%dh%02dm%02ds' $((s / 3600)) $(((s % 3600) / 60)) $((s % 60))
}

# start_epoch: the moment a batch's first run started, in seconds since the
# epoch, from the earliest `started_at` its rows carry (or their `created` for a
# run that never started). The listing is the only place a run's start is
# published — no read carries its end — which is why the rest of the span is
# measured by watching rather than read afterwards.
start_epoch() {
  jq -r '[.runs[] | ((.started_at // .created) | sub("\\.[0-9]+$"; "") | sub("\\+00:00$"; "Z") | fromdateiso8601)] | min' "$1" 2>/dev/null
}

# watch_batch KEY NAME ESTIMATE: the batch's wall clock, observed. It polls the
# batch's rows until every one of them is terminal and takes the span from the
# earliest start it saw to the poll that saw the last one settle. A batch whose
# rows are already terminal on the first poll cannot be measured this way, and
# saying what it did instead would be a figure nobody took.
watch_batch() {
  local key=$1 name=$2 estimate=$3
  local batch_json="$SCRATCH/batch-$key.json"
  local first=yes start="" start_raw="" n settled now deadline
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while :; do
    if ! fugaro runs ls --batch "$name" --since 0 --json >"$batch_json" 2>"$SCRATCH/batch-$key.err"; then
      # A listing that cannot be read is not a finding about the plan: the wall
      # clock is the only thing this row measures, and a check that could not
      # take it has nothing to say about the gain either way. It is left
      # unmeasured — the row says which figure is missing and why — rather than
      # failed, which would read as a break the read reported and the clock
      # refused.
      fact "$key" UNVERIFIED "'fugaro runs ls --batch $name' could not list the batch — $(tr '\n' ' ' <"$SCRATCH/batch-$key.err") The wall clock is the figure this row measures and the listing is the only place it comes from, so nothing is concluded about the plan here: check that the installation can read the runs bucket (fugaro doctor) and run the check again"
      return 0
    fi
    n=$(jq -r '.runs | length' "$batch_json")
    settled=$(jq -r '[.runs[] | select(.terminal == true)] | length' "$batch_json")
    if [ "$n" -gt 0 ] && [ -z "$start" ]; then
      start=$(start_epoch "$batch_json" || true)
      start_raw=$(jq -r '[.runs[] | (.started_at // .created)] | min' "$batch_json")
      owner "batch '$name': $n run(s), the first started $start_raw"
    fi
    now=$(date +%s)
    if [ "$n" -gt 0 ] && [ "$settled" -eq "$n" ]; then
      if [ "$first" = yes ]; then
        fact "$key" UNVERIFIED "batch '$name' was already over when this check first looked ($n run(s), all terminal): the span is observable only while the batch runs, so this figure is not taken after the fact. Run the check right after launching the batches and it measures them"
        return 0
      fi
      if [ -z "$start" ]; then
        fact "$key" UNVERIFIED "batch '$name' has $n run(s) and none of them carries a readable start time ($start_raw), so the span from the first start cannot be taken"
        return 0
      fi
      local wall=$((now - start))
      local statuses
      statuses=$(jq -r '[.runs[].status] | group_by(.) | map("\(length) \(.[0])") | join(", ")' "$batch_json")
      local cost
      cost=$(jq -r '.totals.total_usd' "$batch_json")
      fact "$key" PASS "batch '$name': $n run(s) ($statuses), wall clock $(fmt_duration "$wall") observed live (starts read from the rows, the end the poll at ${POLL_SECONDS}s), $cost USD, $estimate"
      if [ "$key" = decoupled-batch ]; then
        DECOUPLED_WALL=$wall
      else
        SEQUENTIAL_WALL=$wall
      fi
      return 0
    fi
    if [ "$now" -ge "$deadline" ]; then
      if [ "$n" -eq 0 ]; then
        fact "$key" UNVERIFIED "no run carries the batch name '$name' after ${WAIT_SECONDS}s: the check cannot measure a batch nothing was launched in. Launch it with --batch $name, then run the check again"
      else
        fact "$key" FAIL "batch '$name' still had $((n - settled)) of $n run(s) going after ${WAIT_SECONDS}s: the wall clock is not measured for a batch that has not settled. Watch it with 'fugaro runs ls --watch --batch $name' and re-run the check while it is going"
      fi
      return 0
    fi
    if [ "$first" = yes ]; then
      owner "batch '$name': waiting for its runs to settle (re-reading every ${POLL_SECONDS}s, up to ${WAIT_SECONDS}s)"
    fi
    first=no
    sleep "$POLL_SECONDS"
  done
}

# compare: the two figures side by side. The estimate is a weighted path — a
# count of work — and the wall clock is time: their two ratios are read side by
# side and subtracted from nothing, which is what makes this the check on the
# `size` the planner wrote rather than a claim that one of them is the other.
compare() {
  local estimate="the plan's weighted path: $BEFORE_WEIGHT before, $AFTER_WEIGHT after"
  if [ -n "$DECOUPLED_WALL" ] && [ -n "$SEQUENTIAL_WALL" ]; then
    fact comparison INFO "decoupled $(fmt_duration "$DECOUPLED_WALL") against sequential $(fmt_duration "$SEQUENTIAL_WALL"), observed live; $estimate, and the estimate's own ratio $BEFORE_WEIGHT/$AFTER_WEIGHT. The two ratios are the reading: a wall-clock ratio far from the estimate's is what a size that does not describe the work looks like, and a weighted path shorter with a wall clock no shorter is a break that bought nothing"
  else
    fact comparison INFO "one of the two batches' wall clock was not observed, so there is nothing to put beside the estimate ($estimate). The figures above say which"
  fi
}

# ----------------------------------------------------------------------- main
USAGE="check-481.sh <plan> [--decoupled-batch NAME] [--sequential-batch NAME] [--poll SECONDS] [--wait SECONDS]"

usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}

PLAN=""
DECOUPLED_BATCH=${CHECK481_DECOUPLED_BATCH:-check481-decoupled}
SEQUENTIAL_BATCH=${CHECK481_SEQUENTIAL_BATCH:-check481-sequential}
POLL_SECONDS=${CHECK481_POLL_SECONDS:-30}
WAIT_SECONDS=${CHECK481_WAIT_SECONDS:-14400}
DECOUPLED_WALL=""
SEQUENTIAL_WALL=""
BROKEN=""
KEPT=""
ADDED=""
BEFORE_TASKS=""
BEFORE_WEIGHT=""
AFTER_TASKS=""
AFTER_WEIGHT=""
IMPROVES=""

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --decoupled-batch)
      [ $# -ge 2 ] || die "Check 481: --decoupled-batch needs a name"
      DECOUPLED_BATCH=$2
      shift 2
      ;;
    --sequential-batch)
      [ $# -ge 2 ] || die "Check 481: --sequential-batch needs a name"
      SEQUENTIAL_BATCH=$2
      shift 2
      ;;
    --poll)
      [ $# -ge 2 ] || die "Check 481: --poll needs seconds"
      POLL_SECONDS=$2
      shift 2
      ;;
    --wait)
      [ $# -ge 2 ] || die "Check 481: --wait needs seconds"
      WAIT_SECONDS=$2
      shift 2
      ;;
    -h | --help)
      HELP=1
      shift
      ;;
    -*)
      die "Check 481: unknown argument '$1' (usage: $USAGE)"
      ;;
    *)
      if [ -n "$PLAN" ]; then
        die "Check 481: one plan at a time, and '$1' is a second one (usage: $USAGE)"
      fi
      PLAN=$1
      shift
      ;;
    esac
  done
  case "$POLL_SECONDS$WAIT_SECONDS" in
  *[!0-9]*) die "Check 481: --poll and --wait take whole seconds" ;;
  esac
  [ "$POLL_SECONDS" -ge 1 ] || die "Check 481: --poll has to be at least a second"
}

main() {
  parse_args "$@"
  need_tools
  scratch_up
  trap on_error ERR
  if [ -n "$HELP" ]; then
    usage
    REACHED_END=yes
    return 0
  fi
  [ -n "$PLAN" ] || die "Check 481: name the plan to measure (usage: $USAGE)"
  guard_xtrace
  read_gain
  watch_batch decoupled-batch "$DECOUPLED_BATCH" "the shape the pass wrote"
  watch_batch sequential-batch "$SEQUENTIAL_BATCH" "the shape the plan had before the pass"
  compare
  REACHED_END=yes
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
