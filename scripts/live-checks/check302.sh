#!/usr/bin/env bash
# Check 302 — the cutover to the orchestrator daemon, and the manual rollback
# that is the whole of going back, as ONE script YOU run.
#
# It settles the live half of O11 of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; T1 ruled 2026-11-04 —
# the rollback is manual, the scripts are kept 14 days, no rollback command is
# built — and D1235, no shadow week). The register's section "Check 302" in
# docs/gcp-live-checklist.md is the source of truth: this script implements
# exactly its four readings — the preconditions (the caps and the kill switch
# tested first, the scripts' launching stopped), the cutover (the daemon up,
# one finished run, one dependency-driven launch, one notification), the 14
# days, and the manual rollback — and nothing else. `docs/how-to/
# orchestrator-cutover.md` is where the steps it names are written down.
#
# Usage:      bash scripts/live-checks/check302.sh before   --installation <name> --scripts <dir> [--dry-run]
#             bash scripts/live-checks/check302.sh cutover  --dump <file> --daemon-log <file> [--dry-run]
#             bash scripts/live-checks/check302.sh rollback [--launched <run-id>] [--dry-run]
#             bash scripts/live-checks/check302.sh forget
#             --installation is read from FUGARO_PROJECT when it is not given;
#             every phase but `forget` needs it. Every phase takes --dry-run,
#             which prints its plan and changes nothing.
#
# Why it is phased. The cutover is four things in order, and none of them can
# be measured in one sitting: the preconditions come first and are yours (the
# caps and the kill switch tested, the scripts' launching stopped — a machine
# cannot read what starts runs from your queue.txt), the daemon then has to
# run long enough to finish one run and launch one more, and the rollback is
# allowed at any point inside the 14 days that follow. So each phase records
# its readings in one small state file and the next phase compares against
# them, the way Check 301's phases do.
#
#   before     reads the guardrails (the mode, the caps, the kill switch) and
#              the old scripts on disk, prints the two things only you can do
#              with their exact commands, and records the window's dates once
#              you have done them.
#   cutover    runs `fugaro orchestrator import` — the one-time session dump —
#              and reads the three facts the register names: one finished run,
#              one dependency-driven launch, one notification, each with the
#              run it was about.
#   rollback   the manual rollback, which is three steps and no command of its
#              own: `fugaro orchestrator uninstall`, the sweep, the watcher
#              and the queue scripts re-armed, one launch seen through them.
#   forget     removes the one thing this check leaves behind: its state file.
#
# What is a FACT and what is yours. Every reading is a
# `FACT <name>: PASS|FAIL|INFO — <what>` line in the block, and the run ends
# with `CHECK 302 RESULT ... END (exit <code>)`. Each phase prints its plan
# first and then asks for ONE confirmation: the phase's own word, typed at your
# terminal (`before`, `cutover`, `rollback` — a word rather than `--yes`, the
# same door `fugaro orchestrator install` has, because the steps that follow
# are yours). The human-only steps are printed once with their exact command
# and recorded in the block as their own row: the guardrail test
# (`human-guardrails`) and stopping the scripts' launching (`human-launching`)
# in `before`, and re-arming the scripts (`rearm`) in `rollback` — the launch
# it is proved by is read back from the installation's own listing rather than
# taken on your word. `human-next` is the step each phase leaves you, and
# `--dry-run` prints the plan and asks for nothing.
#
# Needs:      bash, mktemp, awk, sed, find and python3 on PATH, a real
#             terminal (the confirmation), and `fugaro` — every reading of the
#             installation goes through your own fugaro, which carries your own
#             credential itself. No credential is read, printed or needed, and
#             no secret value is ever echoed. A missing tool stops the run with
#             one line naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused
#             whether you typed it or the local config in force carries it.
#             `--scripts` names the directory the sweep, the Monitor watcher
#             and the queue scripts live in: it is where the rollback path is,
#             and this check reads it and never writes it.
# Costs:      one `fugaro orchestrator import` of your own dump (the cutover),
#             one `fugaro orchestrator uninstall` (the rollback), and reads:
#             budget show, orchestrator ls/state/status, runs ls. No
#             model call, and no run this check starts: the cutover's runs are
#             the daemon's own and the rollback's one launch is the scripts'.
# Runs:       by YOU, in your own terminal. No agent runs any step of it, and a
#             shell that arrived with a coding agent's marker is refused rather
#             than measured.
# Cleanup:    each phase removes its own scratch directory. The state file it
#             keeps between the phases is removed by the rollback that finishes
#             the check, and `forget` removes it at any time. Nothing else is
#             restored, and the `restore` row says what that means: the daemon
#             and the scripts are the point of this check and the register's
#             own Restore, which is a rollback and not an undo.
# Output:     progress on stderr, ONE block on stdout headed CHECK 302 RESULT,
#             to copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is
#             missing, a phase it needs has not run, the step it asked you for
#             has not happened).
#
# The release gate. `fugaro orchestrator import` is O11's command and it is
# released with 0.24.0: this check reads `fugaro version` and the command
# itself first, and stops with one line naming the release it needs rather than
# failing obscurely on a binary that has neither.
#
# The state file. The phases are resumable through one small file under this
# check's own state directory — ${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/
# check302/state, mode 0600, carrying the machine's label, the installation,
# the window's dates, where the old scripts are and what the guardrails read —
# and nothing else. It holds no credential and no document a project owns.
set -Eeuo pipefail

# No xtrace: a trace of a run that reads the budget's own document is a trace
# nobody can read as this check's block, and the sibling checks refuse one for
# the same reason. Check 145 spells the three parts out.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block goes to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
USAGE="bash scripts/live-checks/check302.sh before|cutover|rollback|forget [--installation <name>] [--scripts <dir>] [--dump <file>] [--daemon-log <file>] [--launched <run-id>] [--dry-run]"
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

# oneline is how a command's own output reaches a FACT: one line, so a detail
# cannot break the block's shape, and bounded, so a whole log cannot arrive in
# one row.
oneline() { tr '\n' ' ' | tr -s ' ' | tr '|' '/' | cut -c1-400; }

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as "the cutover is fine" for a run that
# measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the cutover" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 302 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 302 needs bash"
  need mktemp
  need sed
  need awk
  need find
  need python3 "the readers of fugaro's own --json documents are one python3 file"
  need fugaro "every reading of the installation and every command this check is about is fugaro's"
}

# ------------------------------------------------------------- scratch + state
SCRATCH=""
SCRATCH_MARKER=".chk302-mine"
SCRATCH_REMOVED=no
STATE_DIR=""
STATE_FILE=""
STATE_MARKER=".check302-mine"
PRINTED=""
NOT_REACHED_TEXT="FAIL — not reached (the run stopped before this step)"
PHASE=""
RELEASE_NEEDED=0.24.0
# T1's answer, ruled 2026-11-04: the scripts stay on disk for 14 days and the
# rollback is reachable inside that window. It is a constant of the ruling, not
# a preference of this run.
WINDOW_DAYS=14
INSTALLATION=""
MACHINE=""
SCRIPTS_DIR=""
DUMP=""
DAEMON_LOG=""
LAUNCHED=""
DRY_RUN=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk302.XXXXXXXX") || die "Check 302 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 302 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

state_paths() {
  STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/check302"
  STATE_FILE="$STATE_DIR/state"
}

# state_dir_up creates this check's own state directory, or refuses to touch a
# directory it did not write: a phase that records something must not clobber a
# path a person happens to have made, and the marker is what tells the two
# apart.
state_dir_up() {
  state_paths
  # A dry run prints the plan and changes nothing, so it does not make the
  # directory either: the paths above are all it needs to name what it would do.
  if [ "$DRY_RUN" = yes ]; then return 0; fi
  if [ ! -e "$STATE_DIR" ]; then
    (umask 077 && mkdir -p "$STATE_DIR") || die "Check 302 cannot make its state directory $STATE_DIR"
  fi
  if [ ! -f "$STATE_DIR/$STATE_MARKER" ] && [ -n "$(find "$STATE_DIR" -mindepth 1 -maxdepth 1 2>/dev/null | head -1)" ]; then
    die "Check 302 stopped: $STATE_DIR holds files and carries no marker from this check — it is not this check's directory, so nothing here is written or removed. Move it aside and run again"
  fi
  : >"$STATE_DIR/$STATE_MARKER"
  chmod 700 "$STATE_DIR" 2>/dev/null || true
}

# state_write KEY=VALUE ...: the whole state file is rewritten each time, so a
# half-written file cannot be read as a state. It is written under umask 077,
# moved into place, and holds readings and no credential.
state_write() {
  local tmp="$STATE_FILE.tmp.$$"
  (umask 077 && printf '%s\n' "$@" >"$tmp") || die "Check 302 cannot write its state file $STATE_FILE"
  mv "$tmp" "$STATE_FILE" || die "Check 302 cannot move its state file into place at $STATE_FILE"
  chmod 600 "$STATE_FILE" 2>/dev/null || true
}

state_get() { # state_get KEY
  [ -f "$STATE_FILE" ] || return 0
  sed -n "s/^$1=//p" "$STATE_FILE" | head -1
}

state_present() { [ -f "$STATE_FILE" ]; }

state_remove() {
  [ -n "$STATE_FILE" ] || return 0
  rm -f "$STATE_FILE" 2>/dev/null || true
  if [ -e "$STATE_FILE" ]; then
    fact state FAIL "the state file $STATE_FILE could not be removed — remove it yourself (or run this check's 'forget')"
    return 0
  fi
  fact state PASS "the state file this check kept between the phases ($STATE_FILE) is removed; nothing else of it is left"
}

# state_keep is state_remove's other half: a phase that has to be run again says
# where its readings are instead of dropping them.
state_keep() { # why
  fact state INFO "the state file $STATE_FILE is KEPT ($1): run this phase again as the fact above says, with no new '$PROG before' — '$PROG forget' drops it"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
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
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" != yes ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local note="nothing is restored: the state this check leaves behind is the register's own Restore — a rollback and not an undo — and the last phase's rows say which state the machine is in"
  if [ "$SCRATCH_REMOVED" = yes ]; then
    note="$note; the run's scratch directory is gone"
  elif [ -n "$SCRATCH" ]; then
    note="$note; the scratch directory is STILL THERE, at $SCRATCH — remove it yourself"
  fi
  if [ "$SCRATCH_REMOVED" != yes ] && [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|$note"
    if [ "$status" -eq 0 ]; then status=1; fi
  else
    facts="${facts}"$'\n'"restore|PASS|$note"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# run_bounded SECONDS KEY CMD...: run one command with its output in
# $SCRATCH/KEY.out and .err, and end it after SECONDS. `timeout` is not used: it
# is GNU coreutils, and this check runs on the owner's machine, where the tool is
# absent on macOS. Sets BOUNDED_STATUS and BOUNDED_TIMED_OUT.
#
# The poll is a twentieth of a second rather than a whole one: nearly every
# command this wraps answers in milliseconds, so a one-second poll would add a
# second to each of them, which is why the hermetic test is seconds long rather
# than minutes. Both platforms' sleep take a fraction.
run_bounded() {
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  # The two files start empty: one key is used twice in a run ('before' reads
  # the budget before its human step and again after), and a second reading
  # appended to the first is not a document either of them is.
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  "$@" >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0 limit=$((secs * 20))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$limit" ] || break
    sleep 0.05
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 0.2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then BOUNDED_STATUS=124; fi
  return 0
}

# run_bounded's own tail always returns 0 — the status is in BOUNDED_STATUS —
# so a caller that wants a test uses this rather than `if ! run_bounded ...`,
# which would never fire.
bounded_ok() {
  run_bounded "$@"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then return 1; fi
  return 0
}

# bounded_why SECONDS WHAT: the phrase a FACT uses for why a command did not
# answer — a timeout and an exit are different readings, and a row that said
# "failed" for both would hide the one that means the machine is stuck. It reads
# the status the last run_bounded left behind, so it is called straight after it.
bounded_why() { # bounded_why SECONDS WHAT
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    printf '%s did not answer within %ss' "$2" "$1"
  else
    printf '%s exited %s' "$2" "$BOUNDED_STATUS"
  fi
}

version_at_least() { # version_at_least HAVE WANT
  awk -v a="${1#v}" -v b="${2#v}" '
  function num(s, f) { f = s; gsub(/[^0-9.].*$/, "", f); return f }
  function part(s, i) { return (i in s && s[i] != "") ? s[i] + 0 : 0 }
  BEGIN {
    n = split(num(a), x, ".")
    m = split(num(b), y, ".")
    for (i = 1; i <= (n > m ? n : m); i++) {
      if (part(x, i) > part(y, i)) exit 0
      if (part(x, i) < part(y, i)) exit 1
    }
    exit 0
  }'
}

# ------------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell arrived with tracing on (bash -x): a trace prints the credential every fugaro command carries, so this check refuses to run under one — start it again without -x"
    stop "this shell is tracing (bash -x), and a trace of a run that reads the budget and the node is not a block anyone can paste back. Run it again with no -x"
  fi
  fact xtrace PASS "this shell is not tracing, and this check reads no credential at all: every reading of the installation goes through your own fugaro, which carries your own credential itself"
}

# guard_agent mirrors internal/agentenv.Session: a marker any coding agent sets
# is always refused, and the IDE marker is refused only where no person is at
# the terminal — which is the same door the CLI's own commands use.
guard_agent() {
  local name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      fact agent FAIL "this session carries the coding agent's marker $name, so this is an agent's session and not yours: Check 302 starts and stops your daemon, hands over a dump and drives a cutover, and no agent runs any step of it"
      stop "this is a coding agent's session ($name is set). Check 302 is OWNER-RUN: run it in your own terminal"
    fi
  done
  if [ -n "$(printenv CLAUDE_CODE_SSE_PORT 2>/dev/null || true)" ] && [ ! -t 0 ]; then
    fact agent FAIL "this session carries CLAUDE_CODE_SSE_PORT and has no terminal: that is an IDE session's shape, and Check 302 is owner-run"
    stop "this looks like an IDE session (CLAUDE_CODE_SSE_PORT is set and there is no terminal). Run Check 302 in your own terminal"
  fi
  fact agent PASS "this is not a coding agent's session (no agentenv marker is set), so the rows below measure your machine and not a session's own environment"
}

# guard_owner: `belong` is the installation the register refuses by name, and
# this check runs `fugaro orchestrator import` and `uninstall` against whatever
# the local config in force selects.
guard_owner() {
  if [ "$PHASE" = forget ]; then
    # `forget` removes this check's own state file and nothing else, so the
    # installation in force is not part of what it does and is not asked for.
    return 0
  fi
  if [ -z "$INSTALLATION" ]; then
    stop "no installation is selected: the local config in force names none, and this check must know which installation it is cutting over. Name it: $USAGE (--installation <name>, the name 'fugaro doctor --json' reports as project.name)"
  fi
  case "$INSTALLATION" in
  belong | belong-*)
    fact installation FAIL "the installation in force is $INSTALLATION, which the register refuses by name: Check 302 hands over a dump, starts and stops the daemon and re-arms your scripts, and it is never run against belong"
    stop "Check 302 never runs against 'belong'. Point FUGARO_PROJECT at another installation, or name one: $USAGE"
    ;;
  esac
}

# guard_release: O11's import is released with 0.24.0. A check that ran against
# an older binary would fail obscurely — an unknown command, a rollback that
# never existed — so the version and the two commands are read first and the run
# stops with one sentence naming the release it needs.
guard_release() {
  if ! bounded_ok 30 release fugaro version; then
    fact release FAIL "fugaro version did not answer (exit $BOUNDED_STATUS): this check needs release $RELEASE_NEEDED"
    stop "fugaro version did not answer, so this check cannot tell which release it is looking at: needs release $RELEASE_NEEDED"
  fi
  local v
  v=$(tr -d '\r\n' <"$SCRATCH/release.out")
  case "$v" in
  "" | dev)
    fact release FAIL "this fugaro reports '$v', which is a build from a checkout and not a release: needs release $RELEASE_NEEDED"
    stop "this fugaro is '$v' (a checkout build). Check 302 needs the released $RELEASE_NEEDED: $USAGE"
    ;;
  esac
  if ! version_at_least "$v" "$RELEASE_NEEDED"; then
    fact release FAIL "this fugaro is $v: needs release $RELEASE_NEEDED"
    stop "this fugaro is $v and Check 302 needs release $RELEASE_NEEDED: run 'fugaro update' (the whole upgrade) or 'brew upgrade fugaro' first"
  fi
  local verb missing=""
  for verb in import uninstall; do
    if ! bounded_ok 30 "verb-$verb" fugaro orchestrator "$verb" --help; then missing="$missing $verb"; fi
  done
  if [ -n "$missing" ]; then
    fact release FAIL "this fugaro ($v) carries no 'fugaro orchestrator$missing': needs release $RELEASE_NEEDED"
    stop "this fugaro ($v) is missing 'fugaro orchestrator$missing', the commands O11 and the rollback are: needs release $RELEASE_NEEDED"
  fi
  fact release PASS "this fugaro is $v (release $RELEASE_NEEDED or newer) and it carries the cutover's commands: 'fugaro orchestrator import|uninstall' answer"
}

# guard_terminal: every phase but `forget` ends on ONE confirmation typed at
# your terminal, and a phase that cannot ask for it has nothing to ask about.
guard_terminal() {
  if [ ! -t 0 ]; then
    fact terminal FAIL "this check's standard input is not a terminal, and the phase's one confirmation is typed there: a step that starts a daemon or stops one is never taken by a piped answer, and 'fugaro orchestrator import' refuses a run with no person at the terminal for the same reason"
    stop "standard input is not a terminal: run '$PROG $PHASE' in your own terminal (the confirmation is one word, typed)"
  fi
  fact terminal PASS "this check's standard input is a real terminal, so the one confirmation each phase asks for reaches a person, and nothing below it happens before you type the word"
}

# ---------------------------------------------------------------- the readers
# pick KEY FILE: the value the reader printed for KEY, empty when it printed
# none. The readers write `key|value` lines (the block's own separator), so a
# value cannot be mistaken for a second key.
pick() { # pick KEY FILE
  sed -n "s/^$1|//p" "$2" 2>/dev/null | head -1
}

# write_files puts the one python3 reader in the scratch directory: it answers
# the four documents this check reads (budget show, orchestrator state, runs ls,
# and the clock) and nothing else.
write_files() {
  cat >"$SCRATCH/readers.py" <<'PY'
import json, sys, time, datetime

def load(path):
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        return json.load(fh)

def one(v):
    # One line, and never the block's separator: a value a project wrote must
    # not be able to break the row it arrives in.
    if v is None:
        return "-"
    s = str(v).replace("|", "/").replace("\n", " ").replace("\r", " ")
    return s if s else "-"

def usd(v):
    if v is None:
        return "-"
    return "%.4f" % float(v)

def budget(path):
    d = load(path)
    g = d.get("global") or {}
    kill = g.get("kill") or {}
    out = [
        ("mode", one(d.get("mode"))),
        ("day", one(d.get("day"))),
        ("daily", usd(g.get("daily_cap_usd"))),
        ("perrun", usd(g.get("per_run_cap_usd"))),
        ("counted", usd(g.get("counted_usd"))),
        ("spent", usd(g.get("spent_usd"))),
        ("headroom", usd(g.get("headroom_usd"))),
        ("kill", "on" if kill else "off"),
        ("killby", one(kill.get("by"))),
        ("killat", one(kill.get("at"))),
        ("killreason", one(kill.get("reason"))),
        ("warnings", one("; ".join(d.get("warnings") or []))),
    ]
    for k, v in out:
        print("%s|%s" % (k, v))

def brief(path):
    # The state's own summary, fenced and capped by the CLI (internal/orchestrator
    # Summarize): the goal, one block per work item and the questions.
    d = load(path)
    text = d.get("brief") or ""
    item = None
    count = 0
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        if raw.startswith("  ") and not raw.startswith("    "):
            head = line.split(" ", 1)
            if len(head) < 2 or not head[1].startswith("["):
                continue
            ident = head[0]
            rest = head[1]
            phase = rest[1:rest.find("]")] if "]" in rest else "-"
            item = {"id": ident, "phase": phase, "run": "-", "deps": "-", "by": "-"}
            count += 1
            print("item|%s|%s|-|-|-" % (one(ident), one(phase)))
            continue
        if item is None or not raw.startswith("    "):
            continue
        if line.startswith("run "):
            item["run"] = line[4:].strip()
            print("item-update|%s|run|%s" % (one(item["id"]), one(item["run"])))
        elif line.startswith("after "):
            item["deps"] = line[6:].strip()
            print("item-update|%s|deps|%s" % (one(item["id"]), one(item["deps"])))
        elif line.startswith("approved by "):
            # "approved by <who> at <when>, attempt n of m": the FACT names who,
            # so the two halves are split here rather than in the shell.
            item["by"] = line[len("approved by "):].strip().split(" at ")[0]
            print("item-update|%s|by|%s" % (one(item["id"]), one(item["by"])))
    print("items|%d" % count)
    print("goal|%s" % one(text.splitlines()[0][len("goal:"):].strip() if text.startswith("goal:") else "-"))

def runs(path):
    d = load(path)
    rows = d.get("runs") or []
    for r in rows:
        print("run|%s|%s|%s|%s|%s" % (
            one(r.get("run_id")), one(r.get("status")), one(r.get("repo")),
            one(r.get("pr_url")), one(r.get("launched_by"))))
    print("runs|%d" % len(rows))

def clock():
    now = time.time()
    print("now|%d|%s" % (int(now), iso(int(now))))

def iso(epoch):
    return datetime.datetime.fromtimestamp(int(epoch), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

cmd = sys.argv[1]
if cmd == "budget":
    budget(sys.argv[2])
elif cmd == "brief":
    brief(sys.argv[2])
elif cmd == "runs":
    runs(sys.argv[2])
elif cmd == "clock":
    clock()
elif cmd == "iso":
    print(iso(sys.argv[2]))
else:
    sys.stderr.write("readers.py: unknown command %s\n" % cmd)
    sys.exit(2)
PY
}

# reader CMD ARGS...: run one reader, bounded, and answer whether it answered.
# Its output is left at $SCRATCH/reader.out for the caller to pick from.
reader() {
  local cmd=$1
  shift
  if ! bounded_ok 60 "reader-$cmd" python3 "$SCRATCH/readers.py" "$cmd" "$@"; then return 1; fi
  cp "$SCRATCH/reader-$cmd.out" "$SCRATCH/reader.out"
  return 0
}

# budget_read: the guardrails, in $SCRATCH/budget.tsv. BUDGET_ERR carries why it
# failed, the same way BRIEF_ERR does.
BUDGET_ERR=""
budget_read() {
  if ! bounded_ok 120 budget fugaro budget show --json; then
    BUDGET_ERR=$(bounded_why 120 'fugaro budget show --json')
    return 1
  fi
  if ! python3 "$SCRATCH/readers.py" budget "$SCRATCH/budget.out" >"$SCRATCH/budget.tsv" 2>"$SCRATCH/budget-parse.err"; then
    BUDGET_ERR="'fugaro budget show --json' printed a document this check cannot read"
    return 1
  fi
  return 0
}

# brief_read: the node's own summary, as items one per line, in
# $SCRATCH/brief.tsv. The reader's `item-update` lines fold into the item lines
# here, so a caller reads one row per item. BRIEF_ERR carries why it failed, in
# the words the FACT that reports it uses.
BRIEF_ERR=""
brief_read() {
  if ! bounded_ok 120 brief fugaro orchestrator state --json; then
    BRIEF_ERR=$(bounded_why 120 'fugaro orchestrator state --json')
    return 1
  fi
  if ! python3 "$SCRATCH/readers.py" brief "$SCRATCH/brief.out" >"$SCRATCH/brief.raw" 2>"$SCRATCH/brief-parse.err"; then
    BRIEF_ERR="'fugaro orchestrator state --json' printed a document this check cannot read"
    return 1
  fi
  awk -F'|' '
    $1=="item" { run[$2]="-"; deps[$2]="-"; by[$2]="-"; id[++n]=$2; phase[$2]=$3 }
    $1=="item-update" {
      if ($3=="run") run[$2]=$4
      else if ($3=="deps") deps[$2]=$4
      else if ($3=="by") by[$2]=$4
    }
    END {
      for (i=1;i<=n;i++) printf "item|%s|%s|%s|%s|%s\n", id[i], phase[id[i]], run[id[i]], deps[id[i]], by[id[i]]
      print "items|" n
    }' "$SCRATCH/brief.raw" >"$SCRATCH/brief.tsv"
  return 0
}

# runs_read: the installation's own run listing, in $SCRATCH/runs.tsv. RUNS_ERR
# carries why it failed, the same way BRIEF_ERR does.
RUNS_ERR=""
runs_read() {
  if ! bounded_ok 180 runs fugaro runs ls --all --limit 50 --json; then
    RUNS_ERR=$(bounded_why 180 'fugaro runs ls --all --limit 50 --json')
    return 1
  fi
  if ! python3 "$SCRATCH/readers.py" runs "$SCRATCH/runs.out" >"$SCRATCH/runs.tsv" 2>"$SCRATCH/runs-parse.err"; then
    RUNS_ERR="'fugaro runs ls --json' printed a document this check cannot read"
    return 1
  fi
  return 0
}

# scripts_count DIR: the files the rollback path is, on disk. Prints the count
# and the first names, one line.
scripts_list() { # scripts_list DIR
  [ -d "$1" ] || return 1
  find "$1" -mindepth 1 -maxdepth 1 -type f 2>/dev/null | sort
}

# daemon_lines: the notification lines a foreground daemon prints, one per line
# (internal/orchestrator's LogSink: `<kind> <subject>: <text>`), and the cycles
# that carried something.
log_notifications() { # log_notifications FILE
  [ -r "$1" ] || return 1
  grep -E '^(run-finished|decision-needed) ' "$1" 2>/dev/null || true
}

log_carried() { # log_carried FILE
  [ -r "$1" ] || return 1
  grep -E '\([0-9]+ folded, [1-9][0-9]* carried\)' "$1" 2>/dev/null || true
}

# ------------------------------------------------------------------- confirm
# confirm WORD WHAT: the phase's ONE confirmation, typed at your terminal. The
# word rather than a key or `--yes`, because what follows it is not this
# check's to consent to on your behalf (the same door `fugaro orchestrator
# install` has).
confirm() { # confirm WORD WHAT
  local word=$1 what=$2 answer=""
  owner ""
  owner "this phase goes ahead on the word '$word' and on nothing else."
  owner "typing it confirms: $what"
  printf '%s> type %s to go ahead: ' "$PROG" "$word" >&4
  if ! IFS= read -r answer; then
    printf '\n' >&4
    stop "nothing was read from the terminal, so this phase cannot ask for its one confirmation: run '$PROG $PHASE' in your own terminal"
  fi
  printf '\n' >&4
  if [ "$answer" != "$word" ]; then
    stop "you typed '$answer' and this phase goes ahead only on the word '$word': nothing was run, and the state file is where it was"
  fi
}

# ---------------------------------------------------------------- the plans
plan_before() {
  owner ""
  owner "the plan, for installation $INSTALLATION, in this order:"
  owner "  1. read the budget (the mode, the caps, the kill switch, today's counters) — a read."
  owner "  2. read the directory the sweep, the Monitor watcher and the queue scripts live in ($SCRIPTS_DIR) — a read."
  owner "  3. stop their LAUNCHING (their watching may stay): what starts runs from queue.txt is on your"
  owner "     machine and this check cannot read it. The two share no launch key — the claim is per run id —"
  owner "     so a script and the daemon launching the same work would launch it twice, which is the one"
  owner "     thing this check must not get wrong."
  owner "  4. test the caps and the kill switch FIRST, so that what the daemon does next is bounded:"
  owner "       fugaro budget show                                    # what the row above read"
  owner "       fugaro budget kill --all --reason check302            # the project's kill switch on"
  owner "       <start one run, or the daemon> -> refused            # the refusal this step is for"
  owner "       fugaro budget resume --all --reason check302          # and clear it again"
  owner "       fugaro budget set --global --daily <a cap under today's spend>   # so the next launch is refused"
  owner "       fugaro budget set --global --daily <the value the row above read>  # and put it back"
  owner "     The check reads the guardrails again after this and fails the row if the caps or the switch"
  owner "     are not where they were, naming the command that puts them back."
  owner "  5. if this installation's caps are not in force yet, the daemon's launches are bounded by the"
  owner "     kill switch and the reserve limit alone: the guardrails row below says which."
  owner "  6. then nothing else: this phase records the readings and the window's dates and stops."
  owner ""
  owner "changes nothing it read: no cap, no switch, no script and no node is written by this phase."
}

plan_cutover() {
  owner ""
  owner "the plan, for installation $INSTALLATION, in this order:"
  owner "  1. read the window ($STATE_FILE) and confirm the 14 days have not passed."
  owner "  2. run: fugaro orchestrator import -f $DUMP"
  owner "     the one-time session dump (O11). It lands as the person you are, it lands nothing a session"
  owner "     may not land, and every work item it declares arrives unapproved — the daemon launches"
  owner "     approved work only, so approve what should run with 'fugaro orchestrator patch'."
  owner "  3. read the daemon: 'fugaro orchestrator ls --json' (is one beating this installation's lease?)"
  owner "     and 'fugaro orchestrator state --json' (the node's own summary: the items, their runs, the"
  owner "     questions). No command that starts a run is run by this phase."
  owner "  4. read the three facts the register names, each with the run it was about:"
  owner "       one finished run — the node carries the item's run and the installation's listing names it"
  owner "       one dependency-driven launch — an item the daemon launched whose dependencies are done"
  owner "       one notification — the daemon's own line, from its output at $DAEMON_LOG"
  owner "  5. read the old scripts again: they are still on disk, and nothing here deletes them."
  owner ""
  owner "the one thing you do: start the daemon, in its own window, and leave it running:"
  owner "  fugaro orchestrator run > $DAEMON_LOG 2>&1"
  owner "  (or the login unit the machine already has: 'fugaro orchestrator status' says which, and its own"
  owner "   output path is what --daemon-log should name.) Nothing else is typed: the daemon launches what"
  owner "   the node's approvals call for, folds what finished and says so on that output."
}

plan_rollback() {
  owner ""
  owner "the plan, for installation $INSTALLATION, in this order:"
  owner "  1. read the window ($STATE_FILE) and confirm the rollback is inside the 14 days."
  owner "  2. run: fugaro orchestrator uninstall   — the daemon stops and the login unit is removed."
  owner "  3. prove what the register's own reading is: no 'fugaro orchestrator rollback' exists, so the"
  owner "     three steps you do by hand ARE the whole rollback (T1, 2026-11-04)."
  owner "  4. read the old scripts in $SCRIPTS_DIR: still on disk, which is what makes the way back real."
  owner "  5. the one thing you do: re-arm the sweep, the Monitor watcher and the queue scripts, and see"
  owner "     ONE launch through them."
  owner ""
  owner "nothing here deletes the scripts and nothing here re-arms one for you: which entry starts the"
  owner "sweep is on your machine and not in the installation, and the launch you watch is the proof that"
  owner "the way back works. Run this phase again with --launched <run-id> once it has happened, and the"
  owner "check reads that run back from the installation's own listing."
}

plan_forget() {
  owner ""
  owner "the plan: remove $STATE_FILE, this check's own state file, and nothing else."
  owner "No installation, no daemon and no script is read or touched by this phase."
}

print_plan() {
  case "$PHASE" in
  before) plan_before ;;
  cutover) plan_cutover ;;
  rollback) plan_rollback ;;
  forget) plan_forget ;;
  esac
  owner ""
}

# ------------------------------------------------------------------ the phases
phase_before() {
  state_dir_up
  if state_present; then
    local half stage
    half=$(state_get half)
    stage=$(state_get stage)
    fact state INFO "the readings 'before' records are already at $STATE_FILE (a $half/$stage run, window $(state_get window_start) to $(state_get window_end)): run the next phase — '$PROG cutover --dump <file> --daemon-log <file>' — or drop them ('$PROG forget') and run this one again"
    stop "$STATE_FILE already holds a $half/$stage run: 'before' starts the window and running it twice would move the 14 days. Finish that run or drop it ('$PROG forget')"
  fi
  fact phase PASS "before on $MACHINE: the preconditions of the cutover of installation $INSTALLATION — the guardrails tested and the scripts' launching stopped — and the window the rollback is allowed inside"

  local rv=ok
  budget_read || rv=no
  if [ "$rv" = no ]; then
    fact guardrails FAIL "$BUDGET_ERR, so the mode, the caps and the kill switch this phase must read before anything starts are not measured. Its last lines: $(tail -n 3 "$SCRATCH/budget.out" "$SCRATCH/budget.err" 2>/dev/null | tr '\n' ' ')"
    stop "the guardrails could not be read ($BUDGET_ERR): this check will not look at a cutover whose caps it cannot describe. Read it yourself and see why"
  fi
  local gmode gdaily gperrun gcounted gheadroom gkill gkillby gkillat gkillreason gwarnings
  gmode=$(pick mode "$SCRATCH/budget.tsv")
  gdaily=$(pick daily "$SCRATCH/budget.tsv")
  gperrun=$(pick perrun "$SCRATCH/budget.tsv")
  gcounted=$(pick counted "$SCRATCH/budget.tsv")
  gheadroom=$(pick headroom "$SCRATCH/budget.tsv")
  gkill=$(pick kill "$SCRATCH/budget.tsv")
  gkillby=$(pick killby "$SCRATCH/budget.tsv")
  gkillat=$(pick killat "$SCRATCH/budget.tsv")
  gkillreason=$(pick killreason "$SCRATCH/budget.tsv")
  gwarnings=$(pick warnings "$SCRATCH/budget.tsv")
  # The reading the guardrails-kept row below compares against: the caps and the
  # switch, and nothing of the day's counters (which move by themselves).
  GUARDRAIL_SIGNATURE="mode=$gmode daily=$gdaily per-run=$gperrun kill=$gkill"
  local advisory="the mode is $gmode, so the caps are in force"
  case "$gmode" in
  enforce) ;;
  *) advisory="the mode is $gmode, so the caps are advisory (observe): the kill switch and the reserve limit are what refuse, and 'fugaro budget set --global --mode enforce' is what makes a cap do it" ;;
  esac
  local killreason_suffix=""
  if [ -n "$gkillreason" ]; then killreason_suffix=", \"$gkillreason\""; fi
  if [ "$gkill" = on ]; then
    fact guardrails FAIL "the project's kill switch is ON (set by $gkillby at $gkillat$killreason_suffix): no run starts while it is on, so this cutover could show none of the three facts it is for. The row above is what the test of it should leave behind: clear it ('fugaro budget resume --all --reason check302') and run this phase again"
    stop "the project's kill switch is on (by $gkillby at $gkillat): a cutover under it launches nothing. Clear it with 'fugaro budget resume --all --reason check302' and run this phase again"
  fi
  fact guardrails PASS "the guardrails this phase read before anything started: the daily cap is \$$gdaily, the per-run cap is \$$gperrun, today's counted is \$$gcounted with \$$gheadroom of headroom, and the project's kill switch is off — $advisory${gwarnings:+; fugaro also warned: $gwarnings}"

  local files count names
  files=$(scripts_list "$SCRIPTS_DIR" 2>/dev/null || true)
  count=$(printf '%s\n' "$files" | grep -c . || true)
  if [ ! -d "$SCRIPTS_DIR" ]; then
    fact scripts FAIL "there is no directory at $SCRIPTS_DIR, which is where the sweep, the Monitor watcher and the queue scripts are meant to be: the rollback re-arms exactly those, and without them the way back is not there. Name the directory that holds them (--scripts <dir>)"
    stop "no directory at $SCRIPTS_DIR: name the one the sweep, the watcher and the queue scripts live in (--scripts <dir>) and run this phase again"
  fi
  if [ "$count" -eq 0 ]; then
    fact scripts FAIL "$SCRIPTS_DIR holds no file: the rollback's second step is re-arming the sweep, the watcher and the queue scripts, and there is nothing there to re-arm"
    stop "$SCRIPTS_DIR holds no file: name the directory the sweep, the watcher and the queue scripts live in (--scripts <dir>) and run this phase again"
  fi
  names=$(printf '%s\n' "$files" | sed -n '1,6p' | while IFS= read -r f; do printf '%s ' "${f##*/}"; done)
  fact scripts PASS "$count file(s) are in $SCRIPTS_DIR, and the first of them are $names— the rollback re-arms these and nothing here writes or deletes one. T1 keeps them on disk for the window this phase starts"

  if [ "$DRY_RUN" != yes ]; then
    guard_terminal
    print_plan
    confirm before "the caps and the kill switch have been tested as the plan above names, and the launching of the sweep, the Monitor watcher and the queue scripts is stopped (their watching may stay). Nothing is started by this half: the daemon comes in the next phase"

    fact human-guardrails PASS "you confirmed, at this terminal, that the caps and the kill switch were tested first: a cap under today's spend and the project's kill switch each refused a launch, and both were put back. This is the reading a check cannot make — a cap that refuses is a cap that refuses before it has spent anything, and the only way to see it is to make it refuse"
    fact human-launching PASS "you confirmed, at this terminal, that the launching of the sweep, the Monitor watcher and the queue scripts is stopped, their watching left as it was: the two share no launch key (the claim is per run id), so a script and the daemon launching the same work would launch it twice. What starts runs from queue.txt is on your machine and this check cannot read it"

    # The guardrails after the test, against the reading above: the step that
    # puts a cap back is the step people forget, and a forgotten tiny cap would
    # halt the owner's own work tomorrow.
    local rv2=ok
    budget_read || rv2=no
    if [ "$rv2" = no ]; then
      fact guardrails-kept FAIL "the guardrails could not be read again after the test ($BUDGET_ERR): whether the caps and the kill switch are where the reading above found them is not measured — read 'fugaro budget show' yourself"
      stop "the guardrails could not be read again after the test ($BUDGET_ERR): the window is not opened on a reading this check cannot stand behind"
    else
      local n2 n2daily n2perrun n2kill
      n2=$(pick mode "$SCRATCH/budget.tsv")
      n2daily=$(pick daily "$SCRATCH/budget.tsv")
      n2perrun=$(pick perrun "$SCRATCH/budget.tsv")
      n2kill=$(pick kill "$SCRATCH/budget.tsv")
      if [ "$n2" != "$gmode" ] || [ "$n2daily" != "$gdaily" ] || [ "$n2perrun" != "$gperrun" ] || [ "$n2kill" != "$gkill" ]; then
        fact guardrails-kept FAIL "the guardrails are not where they were before the test: the mode was '$gmode' and is '$n2', the daily cap was \$$gdaily and is \$$n2daily, the per-run cap was \$$gperrun and is \$$n2perrun, and the kill switch was '$gkill' and is '$n2kill'. Put them back — fugaro budget set --global --daily <the value above> (and --per-run, --mode) and fugaro budget resume --all — or the daemon's launches stop being the ones you meant"
        stop "the test's own cap or switch was not put back (the daily cap is \$$n2daily, not \$$gdaily): this check will not open the window with the guardrails moved. Put them back and run this phase again"
      else
        fact guardrails-kept PASS "the guardrails are exactly where the reading above found them after the test: mode '$n2', daily cap \$$n2daily, per-run cap \$$n2perrun, kill switch '$n2kill' — the test's own writes were put back, and the daemon the next phase starts launches under the caps you chose"
      fi
    fi
  fi

  local now end
  now=$(python3 "$SCRATCH/readers.py" clock 2>/dev/null | sed -n 's/^now|\([0-9]*\)|.*$/\1/p' || true)
  if ! printf '%s' "$now" | grep -qE '^[0-9]+$'; then
    fact window FAIL "this machine's clock could not be read, so the window the rollback is allowed inside cannot be dated"
    stop "the clock could not be read: the 14 days T1 allows are dated from this run"
  fi
  end=$((now + WINDOW_DAYS * 86400))
  local start_iso end_iso
  start_iso=$(python3 "$SCRATCH/readers.py" iso "$now")
  end_iso=$(python3 "$SCRATCH/readers.py" iso "$end")
  if [ "$DRY_RUN" = yes ]; then
    fact window PASS "the window this phase would start: $WINDOW_DAYS days from the run's own clock ($start_iso), so the rollback would be allowed until $end_iso (T1, 2026-11-04: the scripts stay on disk and are not deleted by any step of the cutover)"
    fact state INFO "nothing is written: this is a dry run, so no state file is made at $STATE_FILE"
    fact human-next INFO "the real run of this phase writes the state file and stops; the next command is '$PROG cutover --dump <the session dump> --daemon-log <the daemon's output file>'"
    return 0
  fi
  state_write \
    "half=cutover" \
    "stage=pending-cutover" \
    "machine=$MACHINE" \
    "installation=$INSTALLATION" \
    "scripts_dir=$SCRIPTS_DIR" \
    "guardrails=$GUARDRAIL_SIGNATURE" \
    "window_start=$start_iso" \
    "window_end=$end_iso" \
    "window_start_epoch=$now" \
    "window_end_epoch=$end" \
    "at=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)"
  fact window PASS "the window the rollback is allowed inside is open: it runs $WINDOW_DAYS days from this run to $end_iso (it opened $start_iso, T1's answer of 2026-11-04), and the scripts stay on disk and unlaunched for all of it. The state file below is what dates it"
  fact state INFO "the readings are recorded at $STATE_FILE (mode 0600): the machine, the installation, $SCRIPTS_DIR, the guardrails the row above read, and the window's two dates. Nothing else is written and no credential is in it"
  fact human-next INFO "next: start the daemon in its own window and leave it running — 'fugaro orchestrator run > <file> 2>&1' — and once it has finished one run and launched one more, run: bash scripts/live-checks/check302.sh cutover --dump <session dump> --daemon-log <that file>"
  say "next: start the daemon (fugaro orchestrator run > <file> 2>&1), then: bash scripts/live-checks/check302.sh cutover --dump <dump> --daemon-log <file>"
}

# facts_from_the_brief: the register's first two facts out of the node's own
# summary — one finished run and one dependency-driven launch, each named with
# the run it was about. The first two arguments say whether the summary and the
# installation's run listing could be read at all: a row this check cannot read
# is a FAIL that names why, never a pass on an assumption.
facts_from_the_brief() { # facts_from_the_brief BRIEF_OK BRIEF_ERR RUNS_OK RUNS_ERR
  local brief_ok=$1 brief_err=$2 runs_ok=$3 runs_err=$4
  if [ "$brief_ok" != ok ]; then
    fact finished FAIL "the register's first fact, one finished run, is not measured: $brief_err. The daemon's own brief is what names the item a finished run belongs to"
    fact launch FAIL "the register's second fact, one dependency-driven launch, is not measured: $brief_err"
    return 0
  fi

  # The item of the first finished run: a terminal phase with a run behind it.
  # A launch the daemon made on its own is one whose item waited on other items
  # and went once every one of them was done — the brief carries `after <deps>`
  # and the phases of those items are on the same summary, so the second pass
  # reads the first one's phases.
  local done_line launch_line
  done_line=$(awk -F'|' '
    $1=="item" && ($3=="done" || $3=="cancelled" || $3=="failed" || $3=="blocked") && $4!="-" { print; exit }' "$SCRATCH/brief.tsv")
  launch_line=$(awk -F'|' '
    NR==FNR { if ($1=="item") phase[$2]=$3; next }
    $1=="item" && $4!="-" && $5!="-" {
      n=split($5, d, ",")
      ok=1
      for (i=1;i<=n;i++) { gsub(/^ +| +$/, "", d[i]); if (phase[d[i]] != "done" && phase[d[i]] != "cancelled") ok=0 }
      if (ok) { print; exit }
    }' "$SCRATCH/brief.tsv" "$SCRATCH/brief.tsv")

  if [ -z "$done_line" ]; then
    fact finished FAIL "the node's own summary names no work item that is finished (done, failed, blocked or cancelled) with a run behind it: the register's first fact is one finished run, and none is on the node yet. What the summary lists is $(cut -d'|' -f2,3,4 "$SCRATCH/brief.tsv" | tr '\n' ' ')"
  else
    local dit dit_run dit_phase status
    dit=$(printf '%s' "$done_line" | cut -d'|' -f2)
    dit_phase=$(printf '%s' "$done_line" | cut -d'|' -f3)
    dit_run=$(printf '%s' "$done_line" | cut -d'|' -f4)
    if [ "$runs_ok" != ok ]; then
      fact finished FAIL "the node names the finished item $dit [$dit_phase] with run $dit_run, and the installation's own run listing could not be read ($runs_err), so the run's own outcome is not measured"
    else
      status=$(awk -F'|' -v r="$dit_run" '$1=="run" && $2==r { print $3; exit }' "$SCRATCH/runs.tsv")
      if [ -z "$status" ]; then
        fact finished FAIL "the node names the finished item $dit [$dit_phase] with run $dit_run, and the installation's own listing does not name that run ('fugaro runs ls --all --limit 50'): the run's outcome cannot be read back, so whether it finished cannot be told"
      else
        fact finished PASS "one finished run: the daemon folded run $dit_run into item $dit, which is [$dit_phase] on the node, and the installation's own listing names the run as '$status' — the first fact the register names, with the run it was about"
      fi
    fi
  fi

  if [ -z "$launch_line" ]; then
    fact launch FAIL "the node's own summary names no item the daemon launched whose dependencies are done: the register's second fact is one dependency-driven launch — an item that waited on another and went once it was done — and the node does not carry one yet. What the summary lists is $(cut -d'|' -f2,3,4,5 "$SCRATCH/brief.tsv" | tr '\n' ' ')"
  else
    local lit lit_run lit_deps lit_by carried carried_note=""
    lit=$(printf '%s' "$launch_line" | cut -d'|' -f2)
    lit_run=$(printf '%s' "$launch_line" | cut -d'|' -f4)
    lit_deps=$(printf '%s' "$launch_line" | cut -d'|' -f5)
    lit_by=$(printf '%s' "$launch_line" | cut -d'|' -f6)
    carried=$(log_carried "$DAEMON_LOG" | head -1)
    if [ -n "$carried" ]; then carried_note=", and the daemon's own output carries the cycle that carried it ($(printf '%s' "$carried" | oneline))"; fi
    fact launch PASS "one dependency-driven launch: item $lit waits on $lit_deps, every one of which is done on the node, and the daemon started it (run $lit_run) with no command typed${lit_by:+, under the approval the node records ($lit_by)}$carried_note — that is the launch the register names: the daemon taking the next step when the dependency it waited for landed"
  fi
}

phase_cutover() {
  state_dir_up
  if ! state_present; then
    fact state FAIL "there is no state file at $STATE_FILE: 'cutover' reads the window and the scripts' directory from what 'before' recorded, and it will not start a cutover this check never prepared"
    stop "no state from 'before' is at $STATE_FILE: run '$PROG before --installation <name> --scripts <dir>' first ($USAGE)"
  fi
  local half stage was_machine
  half=$(state_get half)
  stage=$(state_get stage)
  was_machine=$(state_get machine)
  if [ "$half" != cutover ]; then
    fact state FAIL "the state file at $STATE_FILE is a $half/$stage run, not the cutover this phase reads"
    stop "$STATE_FILE holds a $half/$stage run: run '$PROG before' for this check, or drop it ('$PROG forget')"
  fi
  if [ "$was_machine" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $was_machine and this machine is $MACHINE: the window and the scripts' directory are that machine's"
    stop "the recorded run is $was_machine's and this is $MACHINE: run this phase on that machine, or drop the state ('$PROG forget')"
  fi
  [ -n "$SCRIPTS_DIR" ] || SCRIPTS_DIR=$(state_get scripts_dir)
  fact phase PASS "cutover on $MACHINE: the one-time import of installation $INSTALLATION and the three facts the register names — one finished run, one dependency-driven launch, one notification"

  local wstart wend wend_epoch now
  wstart=$(state_get window_start)
  wend=$(state_get window_end)
  wend_epoch=$(state_get window_end_epoch)
  now=$(date -u '+%s' 2>/dev/null || echo "")
  case "$now" in "" | *[!0-9]*) now=$(python3 "$SCRATCH/readers.py" clock | sed -n 's/^now|\([0-9]*\)|.*$/\1/p') ;; esac
  if [ -n "$wend_epoch" ] && [ -n "$now" ] && [ "$now" -gt "$wend_epoch" ] 2>/dev/null; then
    fact window FAIL "the window 'before' opened ($wstart) ended $wend, and this run is past it: the cutover this check is for is the one that runs while the scripts are still on disk, so a cutover started now is a different call than the one T1's 14 days are about"
    stop "the $WINDOW_DAYS days T1 allows ended $wend: run '$PROG forget' and start again with '$PROG before' if you mean to cut over now"
  fi
  fact window PASS "the rollback is still allowed inside the window 'before' opened: it runs to $wend (it opened $wstart, $WINDOW_DAYS days, T1), and the old scripts are on disk for all of it"

  if [ "$DRY_RUN" = yes ]; then
    fact terminal INFO "this is a dry run, so nothing below is asked for and nothing is run: the real run prints this same plan and then asks for the word '$PHASE' at your terminal"
    fact import INFO "not run: --dry-run, so the dump at $DUMP is untouched and the node is not read"
    fact daemon INFO "not run: --dry-run, so no daemon is read and no lease is looked at"
    fact finished INFO "not measured: --dry-run"
    fact launch INFO "not measured: --dry-run"
    fact notification INFO "not measured: --dry-run"
    fact scripts INFO "not read: --dry-run"
    fact state INFO "nothing is written: the state file at $STATE_FILE is left as it is"
    fact human-next INFO "the real run of this phase imports the dump, reads the three facts, and prints where the rollback stands"
    return 0
  fi
  if [ ! -r "$DUMP" ]; then
    fact import FAIL "there is no readable session dump at '$DUMP': this phase imports exactly that document ('fugaro orchestrator import -f <file>'), and it reads it before it asks for anything"
    stop "no readable dump at '$DUMP': name the session dump O11 imports (--dump <file>) and run this phase again"
  fi
  if [ ! -r "$DAEMON_LOG" ]; then
    fact notification FAIL "there is no readable daemon output at '$DAEMON_LOG': the notification the register names is a line the daemon prints, and a check cannot see a notification it has no channel for"
    stop "no readable daemon output at '$DAEMON_LOG': start the daemon as the plan names ('fugaro orchestrator run > $DAEMON_LOG 2>&1') and run this phase again with --daemon-log <that file>"
  fi
  guard_terminal
  print_plan
  confirm cutover "this check runs 'fugaro orchestrator import -f $DUMP' for you (the one-time session dump; it lands as you, approves nothing and writes that node and nothing else), then reads the daemon, the node and the installation's run listing. No run is started by this phase"

  local already
  already=$(state_get imported)
  if [ "$already" = yes ]; then
    fact import INFO "the dump was imported by an earlier run of this phase (recorded at $STATE_FILE): it is not imported twice — a dump carries the revision it was built from, so a second import of the same document is a refusal and not a second write"
  elif ! bounded_ok 600 import fugaro orchestrator import -f "$DUMP"; then
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      fact import FAIL "fugaro orchestrator import -f $DUMP did not answer within 600s: the dump may have landed and the node may have moved, and this check will not guess which. Read 'fugaro orchestrator state' and the command's own output below"
    else
      fact import FAIL "fugaro orchestrator import -f $DUMP exited $BOUNDED_STATUS: the dump did not land, so the node the daemon drives carries none of what the session tracked. Its last lines: $(tail -n 4 "$SCRATCH/import.out" "$SCRATCH/import.err" 2>/dev/null | tr '\n' ' ')"
    fi
    stop "the import failed (exit $BOUNDED_STATUS): read $(printf '%s' "$SCRATCH/import.err" | tail -n 3 | tr '\n' ' ') and run this phase again. Nothing else was run"
  else
    state_write \
      "half=cutover" \
      "stage=pending-facts" \
      "imported=yes" \
      "machine=$MACHINE" \
      "installation=$INSTALLATION" \
      "scripts_dir=$SCRIPTS_DIR" \
      "guardrails=$(state_get guardrails)" \
      "window_start=$(state_get window_start)" \
      "window_start_epoch=$(state_get window_start_epoch)" \
      "window_end=$(state_get window_end)" \
      "window_end_epoch=$(state_get window_end_epoch)" \
      "at=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)"
    local answered
    answered=$(grep -m1 '^imported ' "$SCRATCH/import.out" 2>/dev/null | oneline || true)
    fact import PASS "the dump landed: ${answered:-'fugaro orchestrator import' exited 0}. Every work item it declared arrives unapproved — the daemon launches approved work only, so approve what should run with 'fugaro orchestrator patch -f <file>' before the daemon is left to it"
  fi

  local rv=ok
  brief_read || rv=no
  local brief_err=$BRIEF_ERR
  if ! bounded_ok 120 ls fugaro orchestrator ls --json; then
    fact daemon FAIL "fugaro orchestrator ls --json exited $BOUNDED_STATUS, so whether a daemon is beating this installation's lease is not measured. Its last lines: $(tail -n 3 "$SCRATCH/ls.out" "$SCRATCH/ls.err" 2>/dev/null | tr '\n' ' ')"
  else
    local holder beat
    holder=$(python3 - "$SCRATCH/ls.out" "$INSTALLATION" <<'PY' | sed -n '1p'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8", errors="replace"))
except Exception:
    sys.exit(0)
for o in (d.get("orchestrators") or []):
    if o.get("kind") == "local" and str(o.get("project") or "") == sys.argv[2]:
        print("%s|%s" % (o.get("lease_holder") or "", o.get("updated_at") or "-"))
        break
PY
)
    holder=${holder%%|*}
    beat=${holder#*|}
    if [ -z "${holder:-}" ]; then
      fact daemon FAIL "the installation's own listing names no daemon holding this installation's lease ('fugaro orchestrator ls' has no local orchestrator for $INSTALLATION with a holder): the daemon is not running, so the cutover has not happened yet. Start it as the plan names ('fugaro orchestrator run > $DAEMON_LOG 2>&1') and run this phase again"
      state_keep "the cutover is waiting for the daemon to be started and left running"
      stop "no daemon is beating $INSTALLATION's lease: start it ('fugaro orchestrator run > $DAEMON_LOG 2>&1') and run this phase again"
    fi
    fact daemon PASS "a daemon is driving installation $INSTALLATION: the listing names it as holder $holder, whose last beat the database stamped ${beat:-unknown} — one daemon, and the scripts' launching is stopped, so nothing else starts this installation's work"
  fi

  local rv2=ok
  runs_read || rv2=no
  facts_from_the_brief "$rv" "$brief_err" "$rv2" "$RUNS_ERR"

  local note_line
  note_line=$(log_notifications "$DAEMON_LOG" | head -1)
  if [ -z "$note_line" ]; then
    fact notification FAIL "the daemon's own output at $DAEMON_LOG carries no notification line: the register's third fact is one notification, and a notification is a channel and not a record — the daemon prints 'run-finished <run-id>: <text>' (and 'decision-needed <id>: <text>') when the write behind it has landed, and this file has none. Is the daemon you started the one writing this file?"
  else
    fact notification PASS "one notification: the daemon's own output carries '$(printf '%s' "$note_line" | oneline)' — its log sink's line, printed once, after the write that made it true landed. $(log_notifications "$DAEMON_LOG" | wc -l | tr -d ' ') notification line(s) are in this file"
  fi

  local files count names
  files=$(scripts_list "$SCRIPTS_DIR" 2>/dev/null || true)
  count=$(printf '%s\n' "$files" | grep -c . || true)
  if [ "${count:-0}" -eq 0 ]; then
    fact scripts FAIL "the directory 'before' recorded ($SCRIPTS_DIR) holds no file now: the rollback re-arms the sweep, the watcher and the queue scripts, and something removed them — the way back T1 keeps for $WINDOW_DAYS days is gone"
  else
    names=$(printf '%s\n' "$files" | sed -n '1,6p' | while IFS= read -r f; do printf '%s ' "${f##*/}"; done)
    fact scripts PASS "the old scripts are still on disk at $SCRIPTS_DIR ($count file(s): $names— nothing this check ran writes or deletes one, and they stay for the whole window, which ends $(state_get window_end))"
  fi

  state_keep "the rollback reads the window, the scripts' directory and the guardrails from it, and it is allowed at any point inside the $WINDOW_DAYS days"
  fact human-next INFO "the cutover is done and the scripts are still on disk: at any point inside the window (to $(state_get window_end)) the rollback is '$PROG rollback', and once one launch has come through the re-armed scripts run '$PROG rollback --launched <run-id>' — then 'fugaro orchestrator uninstall' + the re-arm is the whole way back (no rollback command exists, T1)"
  say "next, at any point inside the window: bash scripts/live-checks/check302.sh rollback"
}

phase_rollback() {
  state_dir_up
  if ! state_present; then
    fact state FAIL "there is no state file at $STATE_FILE: 'rollback' reads the window and the scripts' directory from what 'before' recorded, and the rollback's 14 days are dated from that run"
    stop "no state from 'before' is at $STATE_FILE: the rollback belongs to a cutover this check prepared ($USAGE)"
  fi
  local half stage was_machine
  half=$(state_get half)
  stage=$(state_get stage)
  was_machine=$(state_get machine)
  if [ "$was_machine" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $was_machine and this machine is $MACHINE"
    stop "the recorded run is $was_machine's and this is $MACHINE: run this phase on that machine, or drop the state ('$PROG forget')"
  fi
  [ -n "$SCRIPTS_DIR" ] || SCRIPTS_DIR=$(state_get scripts_dir)
  local pending_launch=no
  if [ "$stage" = pending-launch ]; then pending_launch=yes; fi
  if [ "$half" != cutover ]; then
    fact state FAIL "the state file at $STATE_FILE is a $half/$stage run: the rollback belongs to the cutover this check prepared ('$PROG before' then '$PROG cutover')"
    stop "$STATE_FILE holds a $half/$stage run: run the cutover first, or drop the state ('$PROG forget')"
  fi
  fact phase PASS "rollback on $MACHINE: the manual rollback of installation $INSTALLATION — the daemon stopped, the sweep, the watcher and the queue scripts re-armed, and one launch seen through them"

  local wstart wend wend_epoch now
  wstart=$(state_get window_start)
  wend=$(state_get window_end)
  wend_epoch=$(state_get window_end_epoch)
  now=$(date -u '+%s' 2>/dev/null || echo "")
  case "$now" in "" | *[!0-9]*) now=$(python3 "$SCRATCH/readers.py" clock | sed -n 's/^now|\([0-9]*\)|.*$/\1/p') ;; esac
  if [ -z "$wend_epoch" ] || [ -z "$now" ]; then
    fact window FAIL "the window's end or this machine's clock could not be read ($wend / $now), so whether the rollback is still inside the $WINDOW_DAYS days is not measured"
    stop "the window's end or the clock could not be read: the rollback T1 allows is the one inside the $WINDOW_DAYS days"
  fi
  if [ "$now" -gt "$wend_epoch" ] 2>/dev/null; then
    fact window FAIL "the $WINDOW_DAYS days are over: the window 'before' opened ($wstart) ended $wend. The register's own reading is that the rollback is reachable 'at any point inside the 14 days', so what runs now is not that rollback — the scripts are still on disk and re-arming them is yours to decide, but this check will not call it the window's way back"
    stop "the window ended $wend: the rollback Check 302 reads is the one inside the $WINDOW_DAYS days, and this run is past them"
  fi
  fact window PASS "the rollback is inside the window the cutover opened: it runs to $wend (it opened $wstart, $WINDOW_DAYS days, T1's answer of 2026-11-04), and the scripts are on disk to be re-armed"

  if [ "$DRY_RUN" = yes ]; then
    fact terminal INFO "this is a dry run, so nothing below is asked for and nothing is run: the real run prints this same plan and then asks for the word '$PHASE' at your terminal"
    fact uninstall INFO "not run: --dry-run, so the daemon is left exactly as it is"
    fact no-rollback-command INFO "not read: --dry-run"
    fact scripts INFO "not read: --dry-run"
    fact rearm INFO "not asked: --dry-run"
    fact launch-scripts INFO "not measured: --dry-run"
    fact state INFO "nothing is written: the state file at $STATE_FILE is left as it is"
    fact human-next INFO "the real run stops the daemon with 'fugaro orchestrator uninstall', reads the scripts and asks you to re-arm them"
    return 0
  fi
  guard_terminal
  print_plan
  confirm rollback "this check runs 'fugaro orchestrator uninstall' for you (the daemon stops and the login unit is removed), reads the old scripts and confirms there is no rollback command, and you re-arm the sweep, the Monitor watcher and the queue scripts and see one launch through them. Nothing here deletes a script"

  if [ "$pending_launch" = yes ]; then
    fact uninstall INFO "the daemon was stopped by an earlier run of this phase (recorded at $STATE_FILE, 'fugaro orchestrator uninstall' already ran): it is not stopped twice — what is left is the launch through the scripts"
  elif ! bounded_ok 300 uninstall fugaro orchestrator uninstall; then
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      fact uninstall FAIL "fugaro orchestrator uninstall did not answer within 300s: the login unit may or may not be gone, and this check will not guess. Read 'fugaro orchestrator status' and 'fugaro orchestrator ls' yourself"
    else
      fact uninstall FAIL "fugaro orchestrator uninstall exited $BOUNDED_STATUS, so the daemon is still the one the login unit starts: the rollback's first step did not happen. Its last lines: $(tail -n 4 "$SCRATCH/uninstall.out" "$SCRATCH/uninstall.err" 2>/dev/null | tr '\n' ' ')"
    fi
    stop "the daemon could not be stopped (fugaro orchestrator uninstall exited $BOUNDED_STATUS): read its own output and run this phase again. The scripts are untouched"
  else
    local answered
    answered=$(oneline <"$SCRATCH/uninstall.out")
    case "$answered" in
    *"nothing to remove"*)
      fact uninstall PASS "'fugaro orchestrator uninstall' exited 0 and found no login unit to remove ('$answered'): nothing was starting this installation's daemon at login, so the rollback's first step was already the state of the machine — the rows below read whether a daemon is running anyway"
      ;;
    *)
      fact uninstall PASS "the daemon is stopped: 'fugaro orchestrator uninstall' exited 0 (${answered:-no output}) and the login unit is removed. This is the first of the rollback's three steps and the only one a command exists for"
      ;;
    esac
  fi

  if [ "$pending_launch" != yes ]; then
    if ! bounded_ok 120 status-after fugaro orchestrator status; then
      fact daemon-stopped FAIL "fugaro orchestrator status exited $BOUNDED_STATUS after the uninstall: whether the login unit is really gone is not measured"
    else
      local stext
      stext=$(oneline <"$SCRATCH/status-after.out")
      case "$stext" in
      *"is not installed"*) fact daemon-stopped PASS "the login unit is gone: 'fugaro orchestrator status' answers '$stext', so nothing starts this installation's daemon at the next login" ;;
      *) fact daemon-stopped FAIL "'fugaro orchestrator status' answers '$stext' after the uninstall: the unit the daemon was started from is still there, so it comes back at the next login and the rollback did not stop it" ;;
      esac
    fi
    if ! bounded_ok 120 ls-after fugaro orchestrator ls --json; then
      fact lease-after FAIL "fugaro orchestrator ls --json exited $BOUNDED_STATUS after the uninstall, so whether the stopped daemon still holds the installation's lease is not measured"
    else
      local lholder
      lholder=$(python3 - "$SCRATCH/ls-after.out" "$INSTALLATION" <<'PY' | sed -n '1p'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8", errors="replace"))
except Exception:
    sys.exit(0)
for o in (d.get("orchestrators") or []):
    if o.get("kind") == "local" and str(o.get("project") or "") == sys.argv[2]:
        print(o.get("lease_holder") or "")
        break
PY
)
      if [ -n "${lholder:-}" ]; then
        fact lease-after FAIL "the installation's lease is still held ($lholder) after the daemon was uninstalled: a daemon is still beating it — is one running on another machine, or did the uninstall leave this one up? The rollback means the daemon is stopped"
      else
        fact lease-after PASS "the installation's lease is free after the uninstall (the listing names no holder), so the daemon really stopped: what starts this installation's work from here is the scripts alone"
      fi
    fi
    state_write \
      "half=cutover" \
      "stage=pending-launch" \
      "imported=yes" \
      "machine=$MACHINE" \
      "installation=$INSTALLATION" \
      "scripts_dir=$SCRIPTS_DIR" \
      "window_start=$wstart" \
      "window_end=$wend" \
      "window_end_epoch=$wend_epoch" \
      "at=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)"
  fi

  # T1's reading, as a fact: the way back is three steps by hand because no
  # command exists, and this is what makes that true rather than assumed.
  if bounded_ok 60 no-rollback fugaro orchestrator rollback --help; then
    fact no-rollback-command FAIL "this release answers 'fugaro orchestrator rollback --help' (exit 0): the register's own reading is that NO rollback command is built (T1, 2026-11-04) and that the three steps below are the whole of the way back. A rollback command that exists is a finding for the register, not a step of this check"
  else
    fact no-rollback-command PASS "there is no 'fugaro orchestrator rollback' (it exited $BOUNDED_STATUS: '$(oneline <"$SCRATCH/no-rollback.err")'), exactly as T1 ruled: the rollback is the three steps by hand — this uninstall, the re-armed scripts, and one launch seen through them — and no command stands in for them"
  fi

  local files count names
  files=$(scripts_list "$SCRIPTS_DIR" 2>/dev/null || true)
  count=$(printf '%s\n' "$files" | grep -c . || true)
  if [ "${count:-0}" -eq 0 ]; then
    fact scripts FAIL "the directory the cutover recorded ($SCRIPTS_DIR) holds no file: there is nothing to re-arm, so the way back is not there and the rollback cannot finish"
    stop "there is nothing to re-arm in $SCRIPTS_DIR: restore the sweep, the watcher and the queue scripts from wherever you keep them, then run this phase again"
  fi
  names=$(printf '%s\n' "$files" | sed -n '1,6p' | while IFS= read -r f; do printf '%s ' "${f##*/}"; done)
  fact scripts PASS "the old scripts are on disk at $SCRIPTS_DIR ($count file(s): $names— every one of them untouched by this check, which is what makes the way back real)"

  fact rearm PASS "you confirmed, at this terminal, that the sweep, the Monitor watcher and the queue scripts are re-armed and their watching is back: from here the machine's work starts the way it did before the daemon, and the daemon is stopped (the rows above). Which entry starts the sweep is on your machine, so this reading is yours and the check records it as yours"

  if [ -n "$LAUNCHED" ]; then
    local rv=ok
    runs_read || rv=no
    if [ "$rv" = no ]; then
      fact launch-scripts FAIL "the installation's run listing could not be read (fugaro runs ls --json exited $BOUNDED_STATUS), so the launch through the scripts cannot be read back. Its last lines: $(tail -n 3 "$SCRATCH/runs.err" 2>/dev/null | tr '\n' ' ')"
    else
      local lstatus lrepo
      lstatus=$(awk -F'|' -v r="$LAUNCHED" '$1=="run" && $2==r { print $3; exit }' "$SCRATCH/runs.tsv")
      lrepo=$(awk -F'|' -v r="$LAUNCHED" '$1=="run" && $2==r { print $4; exit }' "$SCRATCH/runs.tsv")
      if [ -z "$lstatus" ]; then
        fact launch-scripts FAIL "the installation's own listing does not name run '$LAUNCHED' (fugaro runs ls --all --limit 50, newest first): the launch through the scripts was named to this check and the installation does not carry it, so it is not the proof the register's fourth reading is for"
      else
        fact launch-scripts PASS "one launch was seen through the scripts: run $LAUNCHED is in the installation's own listing ($lstatus${lrepo:+, $lrepo}), started after the daemon was stopped and while the scripts were re-armed — the fourth reading the register names, and the end of the rollback"
        state_remove
        fact human-next INFO "the rollback is complete and the state file is gone: the daemon is stopped, the scripts are re-armed and on disk, and T1's window has $(awk -v a="$wend_epoch" -v n="$now" 'BEGIN { d = int((a - n) / 86400); if (d < 0) d = 0; printf "%d", d }') day(s) left of its $WINDOW_DAYS — after it, removing the scripts is your decision and no step of anything else does it"
        say "the rollback is complete: the daemon is stopped and the scripts are re-armed"
        return 0
      fi
    fi
    state_keep "the launch through the scripts was named but could not be read back"
    stop "run '$LAUNCHED' is not in the installation's listing: check the id, or the repository the scripts launched into, and run this phase again with --launched <run-id>"
  fi

  state_keep "the rollback's last reading is one launch seen through the re-armed scripts, and it has not been named yet"
  fact launch-scripts INFO "not measured yet: re-arm the sweep, the Monitor watcher and the queue scripts, see ONE launch through them, and then run this phase again with the run id that launch started — '$PROG rollback --launched <run-id>' — and this check reads it back from the installation's own listing"
  stop "the rollback's one launch through the scripts has not happened: re-arm them, watch one launch, and run '$PROG rollback --launched <run-id>'"
}

phase_forget() {
  state_dir_up
  fact phase PASS "forget on $MACHINE: remove this check's own state file and nothing else"
  if ! state_present; then
    fact state PASS "there is no state file at $STATE_FILE to remove, so nothing this check kept between the phases is left behind"
    return 0
  fi
  local half stage
  half=$(state_get half)
  stage=$(state_get stage)
  fact state PASS "the state file at $STATE_FILE (a $half/$stage run, installation $(state_get installation), window $(state_get window_start) to $(state_get window_end)) is removed, and nothing else is: no script, no daemon and no node is touched by this phase"
  rm -f "$STATE_FILE" 2>/dev/null || true
  if [ -e "$STATE_FILE" ]; then
    fact state FAIL "the state file $STATE_FILE could not be removed — remove it yourself"
  fi
}

# ---------------------------------------------------------------- the block
print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 302 RESULT\n' >&3
  for key in $(block_keys); do
    local line
    # The LAST fact under a key, not the first: a phase that reads a thing twice
    # records the later reading, and a row that passed and then failed must read
    # as the failure.
    line=$(printf '%s\n' "$facts" | grep "^$key|" | tail -1 || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: %s\n' "$key" "$NOT_REACHED_TEXT" >&3
    fi
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
    printed="$printed"$'\n'"$key"
  done
  printf 'CHECK 302 RESULT END (exit %s)\n' "$status" >&3
}

# block_keys is each phase's rows in the order they are read. A row that a
# phase cannot reach reads 'not reached' rather than being dropped, so a block
# is the whole of what that phase could have said.
block_keys() {
  local list=""
  case "$PHASE" in
  before)
    list="phase installation agent release xtrace guardrails scripts terminal human-guardrails human-launching guardrails-kept window state human-next restore"
    ;;
  cutover)
    list="phase installation release window import daemon finished launch notification scripts terminal state human-next restore"
    ;;
  rollback)
    list="phase installation release window uninstall daemon-stopped lease-after no-rollback-command scripts terminal rearm launch-scripts state human-next restore"
    ;;
  forget)
    list="phase installation state restore"
    ;;
  esac
  if [ "$DRY_RUN" = yes ]; then list="$list dry-run"; fi
  local key
  for key in $list; do printf '%s\n' "$key"; done
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

parse_args() {
  PHASE=""
  INSTALLATION=${FUGARO_PROJECT:-}
  MACHINE=""
  SCRIPTS_DIR=""
  DUMP=""
  DAEMON_LOG=""
  LAUNCHED=""
  DRY_RUN=no
  while [ $# -gt 0 ]; do
    case "$1" in
    before | cutover | rollback | forget)
      [ -z "$PHASE" ] || die "one phase per run, and '$PHASE' was already given. usage: $USAGE"
      PHASE=$1
      shift
      ;;
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --scripts)
      [ $# -ge 2 ] || die "--scripts needs a directory. usage: $USAGE"
      SCRIPTS_DIR=$2
      shift 2
      ;;
    --dump)
      [ $# -ge 2 ] || die "--dump needs a file. usage: $USAGE"
      DUMP=$2
      shift 2
      ;;
    --daemon-log)
      [ $# -ge 2 ] || die "--daemon-log needs a file. usage: $USAGE"
      DAEMON_LOG=$2
      shift 2
      ;;
    --launched)
      [ $# -ge 2 ] || die "--launched needs a run id. usage: $USAGE"
      LAUNCHED=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      # The usage is the whole answer, so there is no block: a CHECK 302 RESULT
      # for a run that ran nothing would be a verdict about a cutover this run
      # never looked at.
      REACHED_END=yes
      PRINTED=1
      printf 'usage: %s\n' "$USAGE"
      printf 'phases: before (the preconditions and the window), cutover (the import and the three facts),\n'
      printf '        rollback (the manual rollback), forget (remove this check%s state file)\n' "'s"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$PHASE" ] || die "name the phase to run. usage: $USAGE"
  if [ -z "$INSTALLATION" ] && [ "$PHASE" != forget ]; then
    INSTALLATION=$(only_installation)
  fi
  if [ "$PHASE" = before ] && [ -z "$SCRIPTS_DIR" ]; then
    SCRIPTS_DIR=$(state_get scripts_dir)
  fi
  if [ -z "$MACHINE" ]; then
    MACHINE="$(uname -s 2>/dev/null || echo unknown):$(uname -n 2>/dev/null || echo unknown)"
    MACHINE=$(printf '%s' "$MACHINE" | tr '[:upper:]' '[:lower:]')
  fi
}

# only_installation is the installation a run with no --installation drove: the
# configuration directory's only project config, and nothing when there is none
# or more than one. It is read rather than asked of fugaro because `fugaro
# doctor --json` reaches the cloud and this check reads nothing that way until it
# has to.
only_installation() {
  local dir="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects" f found="" n=0
  for f in "$dir"/*.yaml; do
    [ -f "$f" ] || continue
    n=$((n + 1))
    found=${f##*/}
    found=${found%.yaml}
  done
  if [ "$n" -eq 1 ]; then printf '%s' "$found"; fi
}

gcp_project_of() { # gcp_project_of NAME
  local cfg="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$1.yaml"
  [ -r "$cfg" ] || return 0
  sed -n 's/^ *gcp_project: *//p' "$cfg" | tr -d "\"'" | head -1
}

main() {
  state_paths
  parse_args "$@"
  scratch_up
  write_files
  guard_xtrace
  need_tools
  guard_agent
  guard_release
  guard_owner

  if [ "$PHASE" = forget ]; then
    # forget removes this check's own state file and nothing else, so which
    # installation the run was about is not part of what it does.
    fact installation PASS "forget names no installation: this phase removes this check's own state file and reads no installation, no daemon and no script"
  else
    local project
    project=$(gcp_project_of "$INSTALLATION")
    fact installation PASS "the installation in force is $INSTALLATION${project:+ on GCP project $project}, and it is not belong"
  fi

  if [ "$DRY_RUN" = yes ]; then
    NOT_REACHED_TEXT="INFO — not measured (--dry-run: nothing live was run, so nothing below it is a verdict)"
    print_plan
    case "$PHASE" in
    before) phase_before ;;
    cutover) phase_cutover ;;
    rollback) phase_rollback ;;
    forget) phase_forget ;;
    esac
    fact dry-run PASS "this was a dry run: the plan above is the whole of it, no command that changes anything was run, and no state file was written"
    REACHED_END=yes
    trap - ERR
    return 0
  fi

  case "$PHASE" in
  before) phase_before ;;
  cutover) phase_cutover ;;
  rollback) phase_rollback ;;
  forget) phase_forget ;;
  esac
  REACHED_END=yes
  trap - ERR
  # A run that reached its last step ends on its own rows: one FAIL is exit 1,
  # and only a run that could not tell is exit 2.
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap 'on_error' ERR
trap 'on_signal' INT TERM
trap 'finish' EXIT
main "$@"
