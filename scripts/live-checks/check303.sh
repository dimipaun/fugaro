#!/usr/bin/env bash
# Check 303 — the bounded decision agent, and the cost of one cycle, as ONE
# script YOU run.
#
# It settles the live half of O12 of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; D1228, answered
# 2026-11-04), and it is the gate Q4 puts under slice 2: one real decision call
# through the bounded agent, the two count caps, the breaker at three, and the
# cost of one cycle measured — which is what turns reconciliation U4's target
# into a number. The register's row and section are docs/gcp-live-checklist.md,
# "Check 303".
#
# Usage:      bash scripts/live-checks/check303.sh --installation <name> --yes
#             [--window <duration>] [--breaker-seconds <n>] [--interval <duration>]
#             [--calls-per-hour <n>] [--calls-per-day <n>] [--usd-per-call <d>]
#             [--repo <checkout>] [--dry-run]
#             <name> is the Fugaro project name the local config in force
#             carries (`fugaro doctor --json`'s project.name), and the answer is
#             held to it: a name that is not the one doctor reports is a STOP
#             before anything is run.
# Needs:      bash, mktemp, timeout and fugaro on PATH, and YOUR OWN Claude
#             login (a decision call runs `claude -p` on it). Nothing is
#             installed and nothing is fetched; a missing tool stops the run
#             with one line naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused,
#             whether you typed it as the installation or doctor answers it.
#             The node this check reads is the installation's own — a daemon has
#             one orchestrator per project — so run it where the loop's own work
#             is what you want measured.
# Costs:      THE ONE THING IN THIS REGISTER THAT IS GENUINELY BILLABLE. Every
#             decision call runs `claude -p` on YOUR OWN Claude login: the
#             subscription's money, not Fugaro's gateway budget, which is why
#             D1228's counts are the only dollar control a local daemon has. The
#             run states its own bound before it spends anything and refuses
#             without --yes. It starts no cloud run, no job and no image build:
#             the daemon's own cycles are the whole of what it drives.
# Runs:       by YOU, with your own login. No agent runs any step of it, and no
#             step prints the login's credential or any token: the daemon's
#             child argv is never echoed, and --yes is the only confirmation.
# Cleanup:    it writes NOTHING of its own — no node, no fact, no work item: it
#             runs your own daemon on your own node and reads what the node
#             recorded. The one thing it cannot undo is the breaker: the pause a
#             trip earns is an unanswered decision, and closing one is a
#             person's own op at a real terminal (`fugaro orchestrator patch`
#             asks you to type the project name and takes no --yes), so the
#             Restore row says whether the daemon is paused and names the
#             command that lifts it. That is the register's own wording:
#             "unpause it, or say that it is paused".
# Output:     progress on stderr, ONE block on stdout headed CHECK 303 RESULT,
#             to copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is
#             missing, the build carries no decision agent, the owner did not
#             say --yes).
#
# THE COST BOUND IS PRINTED BEFORE ANYTHING IS SPENT. What a run can spend is
# bounded twice over and both are stated: the daemon's own caps (--calls-per-hour
# and --calls-per-day, the proposals D1228 carries until this check's number
# replaces them) and the window itself, because a decision call happens only
# when the loop meets a judgement case inside it. At --usd-per-call — a
# placeholder you correct for the model your policy picks — the bound is a fact
# and printed on stderr before the daemon starts.
#
# THE CAPABILITY GATE COMES FIRST, and it is a STOP and not a FAIL. A decision
# call has no command of its own in the build this was written against: O12
# landed the agent (internal/orchestrator/decide) and nothing calls it yet —
# O13 is the task that wires it into the daemon behind `--decide`, off by
# default. So the check asks the build for that surface first and, when it is
# not there, records the fact that names the task rather than running a daemon
# that would call no model.
#
# THE JUDGEMENT CASE IS THE LOOP'S, NOT THIS CHECK'S. The spec's table wakes the
# agent for a task whose retries are exhausted, one stuck past its timeout, and
# an ambiguous review result — and every field that makes one of those (the
# phase, the attempt count, the run id, the intent) is the daemon's to write, so
# no script can seed one. This check does not pretend to: it runs the loop and
# reads the calls the loop itself made. A window in which the loop met no
# judgement case makes no call, and the rows below say exactly that rather than
# passing on a reading nobody made. Run it on an installation whose loop has
# been running (the register's own gate: slice 1 has run 14 days before this
# number is asked for), not on a node with nothing waiting.
#
# No trace: `bash -x` prints the child's argv and the daemon's own output, so
# guard_xtrace refuses a shell that arrived tracing.
set -Eeuo pipefail

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
USAGE="bash scripts/live-checks/check303.sh --installation <name> --yes"
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as a clean cycle for a run that measured
# nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the decision agent" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 303 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  need bash "every child this check starts runs on it"
  need mktemp
  need timeout "every daemon this check starts is bounded, so a loop that never returns cannot hang the check"
  need fugaro "doctor names the installation and every step below is a fugaro command"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk303-mine"
SCRATCH_REMOVED=no
PRINTED=""
HELPED=no
INSTALLATION=""
DRY_RUN=no
YES=no
WINDOW=20m
BREAKER_SECONDS=180
INTERVAL=30s
CALLS_PER_HOUR=6
CALLS_PER_DAY=40
USD_PER_CALL=0.30
REPO=""
BOUND_CALLS=0
BOUND_USD=""
PAUSED_BEFORE=no
BREAKER_TRIPPED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk303.XXXXXXXX") || die "Check 303 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 303 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
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
  local facts=$FACTS_CAPTURED
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly: a block that read as a
  # finished run would offer the rows that were never measured as a verdict
  # about the decision agent.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if ! printf '%s\n' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|INFO|nothing of this check's was written, so there is nothing to undo"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"scratch|PASS|the scratch directory is gone"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which prints the decision child's argv and the daemon's own output — the child holds your Claude login"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the child's environment or its output"
}

# guard_belong: `belong` is the owner's own installation wherever it is named —
# the name you typed, or the one doctor answers — and this check spends model
# money and drives a daemon there.
guard_belong() { # guard_belong NAME WHERE
  case "$1" in
  belong | belong-*)
    case "$2" in
    typed) fact installation FAIL "the installation named on the command line is $1, which is the owner's own: Check 303 spends model money and drives a daemon, so it is not run against belong" ;;
    *) fact installation FAIL "the local config in force answers $1, which is the owner's own installation" ;;
    esac
    stop "Check 303 never runs against 'belong'. Name another installation: $USAGE"
    ;;
  esac
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --window)
      [ $# -ge 2 ] || die "--window needs a duration such as 20m. usage: $USAGE"
      WINDOW=$2
      shift 2
      ;;
    --breaker-seconds)
      [ $# -ge 2 ] || die "--breaker-seconds needs a number. usage: $USAGE"
      BREAKER_SECONDS=$2
      shift 2
      ;;
    --interval)
      [ $# -ge 2 ] || die "--interval needs a duration such as 30s. usage: $USAGE"
      INTERVAL=$2
      shift 2
      ;;
    --calls-per-hour)
      [ $# -ge 2 ] || die "--calls-per-hour needs a number. usage: $USAGE"
      CALLS_PER_HOUR=$2
      shift 2
      ;;
    --calls-per-day)
      [ $# -ge 2 ] || die "--calls-per-day needs a number. usage: $USAGE"
      CALLS_PER_DAY=$2
      shift 2
      ;;
    --usd-per-call)
      [ $# -ge 2 ] || die "--usd-per-call needs a number. usage: $USAGE"
      USD_PER_CALL=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a checkout path. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --yes)
      YES=yes
      shift
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation is required, and it must not be belong: this check spends model money. usage: $USAGE"
  case "$WINDOW" in
  '' | *[!0-9hms.]*) die "--window takes a duration such as 20m or 45m, not '$WINDOW'" ;;
  esac
  case "$INTERVAL" in
  '' | *[!0-9hms.]*) die "--interval takes a duration such as 30s, not '$INTERVAL'" ;;
  esac
  case "$BREAKER_SECONDS" in
  '' | *[!0-9]*) die "--breaker-seconds takes a whole number of seconds, not '$BREAKER_SECONDS'" ;;
  esac
  for n in "$CALLS_PER_HOUR" "$CALLS_PER_DAY"; do
    case "$n" in
    '' | *[!0-9]*) die "--calls-per-hour and --calls-per-day take whole numbers, not '$n'" ;;
    esac
  done
  case "$USD_PER_CALL" in
  '' | *[!0-9.]*) die "--usd-per-call takes a number like 0.30, not '$USD_PER_CALL'" ;;
  esac
}

# --------------------------------------------------------------- the commands
# fg TIMEOUT ARGS... — every fugaro call this check makes is bounded, so a
# daemon that never returns ends the step instead of hanging the check. The
# timeout names the process group, so a `claude -p` child the daemon started is
# taken with it rather than left running and spending.
fg() {
  local secs=$1
  shift
  timeout --kill-after=10 "$secs" "$@"
}

# safe_field cleans a value that came out of the database before it reaches the
# block: one line, and no character a terminal cannot take. The block is pasted
# back, and a question or a title can carry anything.
safe_field() {
  printf '%s' "$1" | tr -d '\000-\010\013\014\016-\037' | tr '\n\r\t' '   ' | cut -c1-200
}

# json_get FILE EXPRESSION — one value out of a document this check read, with
# python3 because the block is composed by hand and a jq that is not installed
# would be a check that silently reads nothing.
json_get() { # json_get FILE PYTHON-EXPRESSION
  python3 - "$1" "$2" <<'PY' 2>/dev/null || true
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
try:
    value = eval(sys.argv[2], {"doc": doc, "len": len, "sorted": sorted, "str": str, "int": int, "next": next})
except Exception:
    sys.exit(0)
if value is None:
    sys.exit(0)
print(value)
PY
}

# -------------------------------------------------------------- the bound
# bound_calls: the day cap is the most one window can spend, and a call never
# happens more than once a cycle — so the day's count is the bound the run
# states, and the hour cap is named beside it because it bites first.
bound_calls() { printf '%s' "$CALLS_PER_DAY"; }

bound_usd() {
  awk -v n="$BOUND_CALLS" -v p="$USD_PER_CALL" 'BEGIN { printf "%.2f", n * p }'
}

print_bound() {
  owner "the bound this run will not pass: at most $BOUND_CALLS decision calls (the day cap) inside the ${WINDOW} window,"
  owner "  about \$$BOUND_USD at \$$USD_PER_CALL a call — and the hour cap ($CALLS_PER_HOUR) bites first."
  owner "  every one of them runs on YOUR OWN Claude login; --usd-per-call is a placeholder you correct for the model"
  owner "  your policy picks (orchestrator.decision_model_per_class.<class>). A call happens only when the loop meets a"
  owner "  judgement case inside the window, so the real spend is normally far under this."
}

# ------------------------------------------------------------------ the steps
step_installation() {
  if ! fg 60 fugaro doctor --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err"; then
    sed -n '1,5p' "$SCRATCH/doctor.err" >&4 || true
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local name
  name=$(json_get "$SCRATCH/doctor.json" 'doc.get("project", {}).get("name")')
  [ -n "$name" ] || stop "fugaro doctor --json named no project, so this check cannot tell which installation it is looking at"
  if [ "$name" != "$INSTALLATION" ]; then
    fact installation FAIL "the installation named on the command line is $INSTALLATION and the local config in force answers $name"
    stop "the name you typed and the config in force are two different installations: run with --installation $name, or point FUGARO_PROJECT/FUGARO_CONFIG at the one you meant"
  fi
  guard_belong "$name" doctor
  fact installation PASS "the local config in force is installation $name, it is not belong, and the node this check drives and reads is that installation's own orchestrator — a daemon has one per project"
}

step_version() {
  local v
  v=$(fg 30 fugaro version 2>/dev/null | head -n 1 || true)
  fact version PASS "${v:-fugaro version printed nothing} (the build this check drives: the bounds and the caps are this binary's own, and the numbers it prints are its proposals until this check's number replaces them)"
}

# step_capability asks the build for the surface a decision call needs before
# anything is spent. `--decide` is O13's flag on the daemon (the plan names the
# file it lands in), and it is off by default there: the check turns it on for
# the runs below. A build without it answers the fact that says so rather than a
# daemon that would run every cycle and call no model.
step_capability() {
  local help
  help=$(fg 30 fugaro orchestrator run --help 2>&1 || true)
  case "$help" in
  *--decide*)
    fact decision-agent PASS "this build carries the bounded decision agent: the daemon takes --decide, so the cycles this check drives may make a real decision call"
    ;;
  *)
    fact decision-agent FAIL "this build carries no command that makes a decision call: 'fugaro orchestrator run --help' does not name --decide, which is the flag task O13 wires the agent into the daemon behind (the agent itself — internal/orchestrator/decide — landed with O12, and nothing calls it). Check 303 needs the release that carries O13; nothing was started and nothing was spent"
    stop "this build cannot make a decision call, so Check 303 has nothing to measure. It needs the release that wires the decision agent into the daemon (plan task O13). Nothing was spent"
    ;;
  esac
}

# step_consent is the register's own rule about money: a run that spends on your
# login is not one a script starts by accident, and the bound above is what your
# word is given against.
step_consent() {
  if [ "$YES" = yes ]; then
    fact consent PASS "you passed --yes, and this is the whole of what it covers: your own daemon on your own node may spend up to $BOUND_CALLS decision calls (about \$$BOUND_USD) on your own Claude login inside the ${WINDOW} window. Nothing else is spent — no cloud run, no job, no image build"
    return 0
  fi
  fact consent FAIL "no --yes: Check 303 spends model money on your own Claude login (up to $BOUND_CALLS calls, about \$$BOUND_USD) and it starts no daemon without your explicit word"
  stop "Check 303 spends model money and needs --yes. Re-run: $USAGE"
}

# paused_reading FILE — writes the state's brief to FILE and reports whether the
# node carries the breaker's question. The pause is exactly that: an unanswered
# question whose id carries the breaker's own prefix (orchestrator.BreakerOpen),
# and the summary prints every open question with its id.
paused_reading() {
  local brief
  fg 60 fugaro orchestrator state --json --project "$INSTALLATION" >"$SCRATCH/state.json" 2>"$SCRATCH/state.err" || return 1
  brief=$(json_get "$SCRATCH/state.json" 'doc.get("brief", "")')
  printf '%s' "$brief" >"$1"
  case "$brief" in
  *breaker-tripped-*) return 0 ;;
  *) return 1 ;;
  esac
}

# step_daemon drives the loop for the window: the cycles themselves, and the
# calls the loop makes on its own judgement cases.
step_daemon() {
  local status=0
  owner "running: fugaro orchestrator run --decide --interval $INTERVAL --project $INSTALLATION for at most $WINDOW"
  owner "  (Ctrl-C stops it early: what the node recorded by then is still read and printed)"
  fg "$WINDOW" fugaro orchestrator run --decide --interval "$INTERVAL" --project "$INSTALLATION" \
    >"$SCRATCH/daemon.out" 2>"$SCRATCH/daemon.err" || status=$?
  case "$status" in
  0)
    owner "the daemon exited 0 before the window was out; its last lines:"
    tail -n 6 "$SCRATCH/daemon.out" >&4 || true
    ;;
  124 | 137)
    owner "the window ended and the daemon was stopped"
    ;;
  *)
    owner "the daemon exited $status; its last lines:"
    tail -n 8 "$SCRATCH/daemon.out" >&4 || true
    tail -n 8 "$SCRATCH/daemon.err" >&4 || true
    ;;
  esac
}

# step_calls reads the calls the loop made out of its own audit log: one
# decision writes one entry whose transition carries the decision's stamp
# (internal/orchestrator/decide's audit), and the entry's subject is the item it
# decided about. That is the reading of "one real decision call" that outlives
# the process, because the log is the node's.
step_calls() {
  local status=0 n subjects
  : >"$SCRATCH/calls"
  fg 90 fugaro orchestrator events --json --since "$WINDOW" --project "$INSTALLATION" >"$SCRATCH/events.json" 2>"$SCRATCH/events.err" || status=$?
  if [ "$status" -ne 0 ]; then
    fact calls FAIL "the audit log could not be read (fugaro orchestrator events --json --since $WINDOW exited $status), so the calls this window made cannot be told"
    return 0
  fi
  n=$(json_get "$SCRATCH/events.json" 'sum(1 for e in doc.get("events", []) if str(e.get("transition", "")).startswith("decision-"))')
  subjects=$(json_get "$SCRATCH/events.json" '", ".join(sorted({str(e.get("subject")) for e in doc.get("events", []) if str(e.get("transition", "")).startswith("decision-")})[:6])')
  n=${n:-0}
  printf '%s' "$n" >"$SCRATCH/calls"
  if [ "$n" -gt 0 ]; then
    fact calls PASS "$n decision call(s) are in the audit log for this window; the items they were made about: $(safe_field "${subjects:-unnamed}")"
    return 0
  fi
  if grep -q 'all the calls its caps allow' "$SCRATCH/daemon.out" "$SCRATCH/daemon.err" 2>/dev/null; then
    fact calls PASS "no decision call was made in this window, and the daemon's own refusal says why: this node's caps were already spent. That is the caps refusing rather than the loop finding nothing to decide, and it is what the two cap rows below read"
    return 0
  fi
  fact calls FAIL "the loop made no decision call in this window: it met no judgement case (a task whose retries are exhausted, one stuck past its timeout, an ambiguous review result). Nothing about a real call can be read from a window like this — run the check where the loop has work waiting, or widen --window"
}

# read_policy_key prints one resolved key's value out of `fugaro config show
# --json`, which resolves a checkout's fugaro.yaml over the project layer and
# prints where every value came from. Nothing is written and no network of its
# own is reached beyond the layer the command already reads.
read_policy_key() { # read_policy_key KEY
  local dir key=${1:-}
  dir=${REPO:-$PWD}
  [ -f "$dir/fugaro.yaml" ] || return 0
  (cd "$dir" && fg 60 fugaro config show --json >"$SCRATCH/config.json" 2>/dev/null) || return 0
  python3 - "$SCRATCH/config.json" "$key" <<'PY' 2>/dev/null || true
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
want = sys.argv[2]
for v in doc.get("values", []):
    if v.get("path") == want and v.get("value") is not None:
        print(v["value"])
        break
PY
}

# decision_line_field FILE KEY — the value of KEY= on the daemon's own line about
# the decision agent. That line is the only place either half is written down:
# nothing in the state carries the model, and the log's entry carries the
# decision rather than the model. Both files are read, because a daemon may
# print its progress on either.
decision_line_field() { # decision_line_field KEY
  local f
  for f in "$SCRATCH/daemon.out" "$SCRATCH/daemon.err"; do
    [ -f "$f" ] || continue
    python3 - "$f" "$1" <<'PY' 2>/dev/null && return 0
import re, sys
try:
    text = open(sys.argv[1], encoding="utf-8", errors="replace").read()
except OSError:
    sys.exit(1)
line = re.search(r"decision agent:[^\n]*", text)
if not line:
    sys.exit(1)
field = re.search(r"(?:^|\s)" + re.escape(sys.argv[2]) + r"=([A-Za-z0-9._:@/-]+)", line.group(0))
if not field:
    sys.exit(1)
print(field.group(1))
PY
  done
}

# step_model holds the model that answered to the class the call was made under.
step_model() {
  local ran class value key
  ran=$(decision_line_field model || true)
  class=$(decision_line_field class || true)
  if [ -z "$ran" ]; then
    fact model FAIL "the daemon named no model on its own output, so which model answered the one real call cannot be told. What the register asks for is that the model that answered is the classifier's and not the one the output names, and that reading needs the daemon to name it"
    return 0
  fi
  if [ -z "$class" ]; then
    fact model FAIL "the call ran on $ran and the daemon named no class on its own output, so the model cannot be held to O4's key for the class the classifier picked"
    return 0
  fi
  key="orchestrator.decision_model_per_class.$class"
  value=$(read_policy_key "$key")
  if [ -z "$value" ]; then
    fact model FAIL "the call ran on $ran under class $class, and '$key' could not be read on this machine to hold it to: pass --repo <the checkout whose fugaro.yaml carries the policy in force> and run again"
    return 0
  fi
  case "$ran" in
  *"$value"*)
    fact model PASS "the call ran on $ran, which is what '$key' holds for the $class the classifier picked: the model that answered is the classifier's (O1's class, O4's key) and not one the answer named for itself"
    ;;
  *)
    fact model FAIL "the call ran on $ran and '$key' holds $value for class $class: the model that answered is not the one the classifier chose, which is the whole of D1234's rule"
    ;;
  esac
}

# step_caps reads the two count caps out of the daemon's own refusals: the
# refusal names the count and the cap that produced it (decide's ErrCallCap),
# which is exactly the reading the register asks for. A cap the window never
# reached is a FAIL and not a pass: the numbers are D1228's proposals, and a run
# that never reached one has proved nothing about it.
step_caps() {
  local hour day
  hour=$(grep -ho 'decide: the decision agent has made all the calls its caps allow[^"]*' "$SCRATCH/daemon.out" "$SCRATCH/daemon.err" 2>/dev/null | grep -m1 'calls in the last hour' || true)
  day=$(grep -ho 'decide: the decision agent has made all the calls its caps allow[^"]*' "$SCRATCH/daemon.out" "$SCRATCH/daemon.err" 2>/dev/null | grep -m1 'calls today' || true)
  if [ -n "$hour" ]; then
    fact cap-hour PASS "the $CALLS_PER_HOUR-an-hour cap was reached and the next call was refused, by the cap's own words: $(safe_field "$hour")"
  else
    fact cap-hour FAIL "the $CALLS_PER_HOUR-an-hour cap was not reached in this window, so it is not proved: spending it takes a window in which the loop meets at least $CALLS_PER_HOUR judgement cases, and the calls row says how many it met"
  fi
  if [ -n "$day" ]; then
    fact cap-day PASS "the $CALLS_PER_DAY-a-day cap was reached and the next call was refused, by the cap's own words: $(safe_field "$day")"
  else
    fact cap-day FAIL "the $CALLS_PER_DAY-a-day cap was not reached in this window — the hour's cap bites first and the day's count is the node's own calendar day, so proving the day cap takes a node whose counts are already near it — and the day cap is not proved by this run"
  fi
}

# step_breaker proves the breaker at three: three cycles in a row that ended
# with nothing done raise a decision and stop every spawn. The count is the
# daemon process's own (orchestrator.Breaker), so this is the one step that
# needs the loop to keep running rather than one cycle — and the pause is read
# off the node, which is what outlives the process.
step_breaker() {
  if [ "$PAUSED_BEFORE" = yes ]; then
    fact breaker INFO "the node already carried a breaker question before this run started, so a pause read now is not this window's: lift the one that is there (the restore row prints the command) and run the check again to prove the trip"
    return 0
  fi
  local status=0 paused_id
  owner "running: fugaro orchestrator run --decide --interval $INTERVAL --project $INSTALLATION for at most ${BREAKER_SECONDS}s, to reach the breaker at 3"
  fg "$BREAKER_SECONDS" fugaro orchestrator run --decide --interval "$INTERVAL" --project "$INSTALLATION" \
    >"$SCRATCH/breaker.out" 2>"$SCRATCH/breaker.err" || status=$?
  if paused_reading "$SCRATCH/paused.after"; then
    paused_id=$(grep -ho 'breaker-tripped-[0-9A-Za-z:_-]*' "$SCRATCH/paused.after" | head -n 1 || true)
    fact breaker PASS "three cycles in a row ended with nothing done and the node now carries the breaker's own question (${paused_id:-breaker-tripped-<stamp>}), so the daemon is PAUSED and starts nothing until a person answers (G10)"
    BREAKER_TRIPPED=yes
    return 0
  fi
  fact breaker FAIL "the ${BREAKER_SECONDS}s window ended and the node carries no breaker question: the daemon did not reach three cycles in a row with nothing done, so the trip is not proved by this run"
}

# step_cycle_cost is the number U4 is waiting for and Q4's gate reads: the calls
# one cycle makes, and the dollars they move, end to end.
step_cycle_cost() {
  local n=${1:-0} usd
  case "$n" in
  '' | *[!0-9]*) n=0 ;;
  esac
  usd=$(awk -v n="$n" -v p="$USD_PER_CALL" 'BEGIN { printf "%.4f", n * p }')
  if [ "$n" -eq 0 ]; then
    fact cycle-cost FAIL "no call was made in this window, so the cost of one cycle cannot be measured: a cycle that meets a judgement case spends one decision call and a cycle that meets none spends nothing at all, and this window read only the second half"
    return 0
  fi
  fact cycle-cost PASS "$n call(s) in this window at \$$USD_PER_CALL a call, about \$$usd: one judgement cycle spends one call (a call is bounded by its own turns and output pin), and a cycle that meets no judgement case spends nothing — which is the shape U4's target is written against. These dollars are your own Claude login's and not Fugaro's gateway budget's"
}

# step_restore: this check writes nothing of its own, so what it has to say is
# what the node is left as — and the breaker's pause is the one thing it cannot
# undo, because closing a decision is a person's own op at a terminal.
step_restore() {
  if [ "$BREAKER_TRIPPED" = yes ] || paused_reading "$SCRATCH/paused.now"; then
    local paused_id
    paused_id=$(grep -ho 'breaker-tripped-[0-9A-Za-z:_-]*' "$SCRATCH/paused.now" 2>/dev/null | head -n 1 || true)
    fact restore PASS "this check wrote nothing of its own, so nothing of its is left to undo — but the DAEMON IS PAUSED: the node carries the breaker's question ${paused_id:-breaker-tripped-<stamp>}, and the pause is an unanswered decision, which only a person closes. Lift it at your own terminal: read the id with 'fugaro orchestrator state --project $INSTALLATION', then close it with 'fugaro orchestrator patch -f <a document holding a decision_resolve op for that id>' — the typed confirmation takes the project's name and no --yes. Answering it is also what re-arms the breaker"
    return 0
  fi
  fact restore PASS "this check wrote nothing of its own and the daemon is not paused: the node carries no unanswered breaker question, so nothing is left to undo"
}

# ------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was run, no daemon was started and no model call was made. A real run would run, in this order:"
  say "  [local] the guards: the installation you typed is held to fugaro doctor --json's project.name, and 'belong' is refused by name"
  say "  [read] fugaro version — the build whose bounds and caps this check measures"
  say "  [read] fugaro orchestrator run --help — the capability gate: a build without --decide stops here, spending nothing"
  say "  [no spend] the bound, printed before anything: at most $BOUND_CALLS decision calls (the day cap) in the window, about \$$BOUND_USD"
  say "  [read] fugaro orchestrator state --json — the pause the node starts with, so a trip this run reads afterwards is attributable"
  say "  [SPENDS] fugaro orchestrator run --decide for at most $WINDOW — your own daemon on your own node, one cycle every $INTERVAL"
  say "  [read] fugaro orchestrator events --json --since $WINDOW — the audit log's own decision entries: the calls the loop made, and the items it made them about"
  say "  [read] the daemon's own output — the model and the class it named, held to orchestrator.decision_model_per_class.<class> from fugaro config show --json"
  say "  [read] the daemon's own output — the cap refusals, which name the count that produced them"
  say "  [SPENDS] fugaro orchestrator run --decide for at most ${BREAKER_SECONDS}s — the same daemon kept running, so the breaker's own count (three cycles in a row with nothing done) can reach three"
  say "  [read] fugaro orchestrator state --json — the pause, which is the breaker's question on the node"
  say "and its Restore (also from the exit trap) would run:"
  say "  [yours] nothing of this check's was written, so there is nothing to undo; if the breaker tripped it says the daemon is PAUSED and names the command that lifts it, because closing that decision is a person's own op at a terminal"
  say "  [local] the scratch directory is removed"
  say "Nothing was created, written or spent: this dry run started no daemon, made no fugaro call that writes and made no model call at all."
}

# ------------------------------------------------------------------- the block
BLOCK_KEYS=(installation version decision-agent cost-bound consent calls model cap-hour cap-day breaker cycle-cost restore xtrace)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 303 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 303 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
main() {
  need_tools
  parse_args "$@"
  BOUND_CALLS=$(bound_calls)
  BOUND_USD=$(bound_usd)

  scratch_up
  guard_xtrace
  guard_belong "$INSTALLATION" typed

  print_bound
  if [ "$DRY_RUN" = yes ]; then
    print_plan
    fact dry-run PASS "guards, the bound and the plan only: no daemon was started, nothing was written and no model call was made"
    REACHED_END=yes
    BLOCK_KEYS=(installation cost-bound consent dry-run xtrace)
    return 0
  fi

  fact cost-bound PASS "at most $BOUND_CALLS decision calls inside the ${WINDOW} window, about \$$BOUND_USD at \$$USD_PER_CALL a call, with the $CALLS_PER_HOUR-an-hour cap biting first: a call happens only when the loop meets a judgement case in the window"

  step_installation
  step_version
  step_capability
  step_consent
  # The baseline: a pause that is already there is not this run's to claim.
  if paused_reading "$SCRATCH/paused.before"; then PAUSED_BEFORE=yes; fi

  step_daemon
  step_calls
  step_model
  step_caps
  step_breaker
  step_cycle_cost "$(cat "$SCRATCH/calls" 2>/dev/null || echo 0)"
  step_restore

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
