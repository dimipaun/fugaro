#!/usr/bin/env bash
# Check 310 — the default output on a real terminal, as ONE script YOU run.
#
# It settles the live half of CL5b (the converge's own account) and of CL1/CL3
# (the log) on a machine with a REAL terminal, which is the one thing the
# package's own tests cannot have: report's tests hand the reporter a writer and
# say whether it is a terminal, so "a pipe gets no escape byte" and "a terminal
# gets colour and a line in flight" are both read there off a fake. Here they
# are read off a real pty (the command is run under `script`, so the child's own
# stdout is a pty), a real pipe, and the log files a real converge leaves in the
# state directory.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 310".
#
# What it reads, in this order:
#
#   1. `fugaro update --project <name>` at a real terminal (under `script`,
#      which is also what makes it visible to you): the step lines carry ANSI
#      colour, the line in flight is a spinner drawn with \r and cleared with
#      \r\x1b[K before the step's own result line, and the run closes with the
#      summary line ("Done in 3.2s, 5 steps", or Failed / Left for you). The
#      spinner is the one thing a script cannot judge — whether it LOOKED right
#      is printed as one yes/no question you answer.
#   2. `fugaro setup --plan-only --project <name>` the same way: the plan view's
#      own markers and its colour. A plan-only run prints no closing line at all
#      (the loop's account is the execution's: initflow's `!l.o.PlanOnly`), so
#      this reading is the rows and not a summary, and the check says so rather
#      than failing the row for a line the plan view is not supposed to have.
#   3. `NO_COLOR=1 fugaro update --project <name>` at a real terminal: NO_COLOR
#      is the no-color.org rule (present and not empty), and it has to turn
#      colour off AT A TERMINAL — read off a pipe it would be off anyway, which
#      is a reading of nothing (T4(a)).
#   4. `fugaro update --project <name> | cat`: stdout is a pipe, so no escape
#      byte may appear on it at all (CL5b, T4(a)).
#   5. `fugaro update --project <name> --json`: stdout is ONE JSON document
#      (so nothing human was printed there), it parses, it is an object, it
#      names the project that was typed, and it carries the run's stages; the
#      human lines are on stderr, which is where --json sends them (G4).
#   6. A FORCED failure, so that there is a failure block to read at all: this
#      check puts a `terraform` of its own first on PATH that answers
#      `version -json` (the read every converge makes first) and refuses every
#      other subcommand, so the run fails inside a stage with the reporter open.
#      It reads the failed step's line, the context under it, the
#      `full output: <path>` line and the `Something went wrong. Run: fugaro
#      troubleshoot <id>` line under that (G8), and then the log file itself:
#      0600 in a 0700 directory, and holding the run's own lines whole (D1352).
#   7. The log directory's retention: a log stamped twenty days ago is removed
#      by the next converge (14 days), a log of another command name beside it
#      is left alone (the pruning is per command name), and after 51 runs
#      exactly 50 `update-*.log` files are left (the newest 50 per command
#      name). The runs are the same forced failure: a second or two each,
#      creating nothing.
#
# What it NEVER does. It never touches the project named `belong`: a name
# containing it (any case) is refused before a single command is run. It writes
# exactly two files outside its own scratch directory — the two planted logs of
# reading 7, in the log directory, and it removes the one the CLI itself does
# not prune — and it removes nothing else of yours: the logs your own runs left
# there are read, never deleted. The forced-failure runs create nothing, because
# the only terraform on their PATH is this check's stub, which refuses every
# plan and every apply. The five commands of the look phase are REAL `fugaro
# update` and `fugaro setup --plan-only` runs against the project you name:
# they do what those two commands do (an update applies whatever the
# installation is behind on; --plan-only creates nothing), which is the row's
# own subject. No model call is made, no run is launched and nothing is
# billable.
#
# Usage:      bash scripts/live-checks/check310.sh --project <fugaro project>
#                [--phase all|look|failure|logs] [--runs N]
#                [--fugaro-flags '<flags>'] [--dry-run]
#             --project is the Fugaro project the commands act on, and it is
#             required: the check never lets the CLI fall back to "the only
#             project config" on your machine, because the reading would then be
#             of a project you did not name. It must be a Fugaro project name
#             (the config's own rule: 1 to 40 of a-z, 0-9 and '-', starting and
#             ending with a letter or digit).
#             --phase look is readings 1 to 5, --phase failure is 6, --phase
#             logs is 7, and all (the default) is every one of them in that
#             order. A phase that was not selected is recorded as NOT MEASURED
#             rather than as a failure.
#             --runs is how many failing updates the retention half makes
#             (default 51). Fewer than 51 cannot read the newest-50 rule, and
#             the row then says so instead of guessing.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (it is split on whitespace, so a value with a space in it cannot
#             be passed).
#             --dry-run prints the plan and runs nothing at all.
# Needs:      bash, mktemp, sed, grep, head, cut, cat, tr, wc, ls, tee, python3
#             (the JSON document, the file modes, the stamp of the planted log
#             and the escape bytes are read with it), `script` (util-linux's or
#             BSD's — both are detected) for the three terminal readings, and a
#             `fugaro` on PATH. Nothing is installed and nothing is fetched; a
#             missing tool stops the run with one line naming it.
# Where:      your own machine (macOS or Linux), at a real terminal — the
#             terminal is the subject, so a run inside a pipe is not this
#             check. It is run against a project you name and never `belong`.
# Costs:      five real converge runs (an update three times, a
#             `setup --plan-only`, and the piped and --json updates), plus 52
#             forced-failure runs of a second or two each. No model call, no
#             run, no image build, nothing billable.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 310 RESULT.
#             Exit 0 when every fact held, 1 when one did not (each row says
#             which), and 2 when the run could not tell (a guard stopped it, a
#             tool is missing, the forced failure produced no failure block at
#             all, the log directory is not there, an internal error ended it).
#
# Nothing this check reads is a credential — a log holds a run's own lines, and
# the lines it reads here are the stub's — so there is no trace guard: a trace
# of this run prints a project name and paths, which is what the block prints
# anyway.
#
# CHECK310_RUNS is --runs' default (51), and CHECK310_SCRIPT/CHECK310_TEE/
# CHECK310_FUGARO name the tools the readings use (this script's own test
# replaces them). Nothing here is on a timer: a converge on a slow link is
# waited for, and an interrupt (Ctrl-C) prints the block with what was read
# before it.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by the_verdict once the reading has been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and it is read by the exit
# trap, which has to see both of these whatever path ended the run.
HELPED=no
DRY_RUN=""
USAGE="check310.sh --project <fugaro project> [--phase all|look|failure|logs] [--runs N] [--fugaro-flags '<flags>'] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something
# to paste back.
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

# rewrite_fact replaces the row a key already has, or writes it when there is
# none. The block prints the FIRST row a key carries, so a reading that turns out
# to be stale has to be replaced in place: appending a second one would leave the
# stale row in the block and hide the correction.
rewrite_fact() { # rewrite_fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  python3 - "$FACTS_FILE" "$1" "$2" "$3" <<'PY'
import sys

path, key, verdict, detail = sys.argv[1:5]
row = "%s|%s|%s" % (key, verdict, detail)
with open(path, encoding="utf-8") as fh:
    rows = fh.read().split("\n")
out, replaced = [], False
for line in rows:
    if not replaced and line.startswith(key + "|"):
        out.append(row)
        replaced = True
    else:
        out.append(line)
if not replaced:
    out.append(row)
with open(path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(out))
PY
}

has_fact() { # has_fact KEY
  [ -n "$FACTS_FILE" ] && grep -q "^$1|" "$FACTS_FILE" 2>/dev/null
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the output for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the output"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 310 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 310 needs bash"
  need mktemp
  need sed "the block's rows are read back out of this run's own fact file with it"
  need grep
  need head
  need cut
  need cat
  need tr
  need wc
  need ls "the log directory's retention is a count of the files in it"
  need tee "the terminal readings are captured while you watch them"
  need python3 "the JSON document, the file modes, the planted log's stamp and the escape bytes are read with it"
  need "$FUGARO_BIN" "every reading is a command of the build's own"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk310-mine"
SCRATCH_REMOVED=no
PLANTED=()
STUB_DIR=""
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk310.XXXXXXXX") || die "Check 310 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 310 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_planted records what it
# managed to do, and a `$(cleanup)` would throw that answer away with the
# subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_planted
  remove_scratch
}

# The planted logs are the only files this check writes outside its scratch
# directory. Each one is removed by name, and only when it is still there: the
# twenty-day-old one is normally pruned by the CLI itself, and the other
# command's is removed here because nothing else would. Guarded, because this
# runs from the exit trap, where a failing `rm` under `set -e` would end the
# shell before the block is printed.
remove_planted() {
  local name path
  for name in ${PLANTED[@]+"${PLANTED[@]}"}; do
    path="$LOG_DIR/$name"
    case "$path" in
    "$LOG_DIR"/*.log) rm -f "$path" 2>/dev/null || true ;;
    esac
  done
  return 0
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

# ------------------------------------------------------------------- the guards
# guard_project: the project is required and it has to be a project name, which
# is the config's own rule (ProjectNameRE). The name also travels inside the
# command string the terminal readings hand to `script`, so holding it to that
# pattern is what keeps the string one command rather than several.
guard_project() {
  if [ -z "$PROJECT" ]; then
    fact project FAIL "no project was named: this check never lets the CLI fall back to 'the only project config' on this machine, because the reading would then be of a project you did not name"
    stop "no --project was given. usage: $USAGE"
  fi
  if ! printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'; then
    fact project FAIL "'$PROJECT' is not a Fugaro project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)"
    stop "the project '$PROJECT' is not a Fugaro project name"
  fi
  fact project PASS "$PROJECT is a Fugaro project name, and every command this check runs carries it: the CLI is never left to fall back to 'the only project config' on this machine, which would make the reading one of a project you did not name"
}

# guard_belong: the installation named `belong` is the owner's own. The look
# phase runs real converges and the failure phase writes into the log directory,
# so a mistyped name pointing at it would be work nobody asked for; the name is
# refused before anything is run, and the test is a substring one on purpose, so
# a name that merely contains it is refused too and the message says which one
# matched.
guard_belong() {
  case "$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact belong FAIL "'$PROJECT' names the owner's own installation: Check 310 runs real converges and writes into the log directory, and it is never pointed at 'belong'"
    stop "the project '$PROJECT' matches the installation 'belong', which this check must never touch"
    ;;
  esac
  fact belong PASS "the project ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
}

# guard_session: a coding agent's session is not who this check is for. The
# converge still runs there — it applies nothing and reports the applying steps
# as left-for-you — so the readings below still hold and this is a note rather
# than a stop: what a marker does change is that a stage needing a typed
# confirmation is left for you instead of applied, which is a reading of the
# summary's own word rather than of the output. The list is agentenv's.
guard_session() {
  local marker="" k
  for k in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!k:-}" ]; then
      marker=$k
      break
    fi
  done
  if [ -z "$marker" ] && [ -n "${CLAUDE_CODE_SSE_PORT:-}" ] && [ ! -t 0 ]; then
    marker=CLAUDE_CODE_SSE_PORT
  fi
  if [ -n "$marker" ]; then
    fact session INFO "a coding agent's session is present ($marker is set): the converge applies nothing there and reports the applying steps as left for you, so the readings below hold and a stage that needs a typed confirmation is left for you rather than applied. This check is the owner's, at the owner's own terminal"
    return 0
  fi
  fact session PASS "no coding agent's marker is set, so the converge this check runs is not in an agent's session (agentenv's list)"
}

# ------------------------------------------------------ where the log lives
# resolve_state_dir mirrors localcfg.UserDir for DirState, which is the rule the
# converge places its log by: $XDG_STATE_HOME where it is set to an ABSOLUTE
# path, and $HOME/.local/state where it is not (the XDG spec ignores a relative
# value, and UserDir ignores it in the same words). macOS is the second case --
# and the reason the plan's row spells the expansion out, because
# `${XDG_STATE_HOME:-$HOME/.local/state}` would expand an unset variable to the
# empty string and the path would read `/fugaro/logs`.
STATE_BASE=""
LOG_DIR=""
resolve_state_dir() {
  local xdg=${XDG_STATE_HOME:-}
  case "$xdg" in
  "") ;;
  /*) STATE_BASE=$xdg ;;
  *) say "XDG_STATE_HOME is set to '$xdg', which is not absolute: the XDG rule ignores such a value and localcfg.UserDir falls back to \$HOME, so this check does the same. fugaro is given the same environment, so the two agree on the directory either way" ;;
  esac
  if [ -z "$STATE_BASE" ]; then
    local home=${HOME:-${USERPROFILE:-}}
    [ -n "$home" ] || die "Check 310 cannot place the state directory: neither XDG_STATE_HOME nor HOME is set. usage: $USAGE"
    case "$home" in
    /*) ;;
    *) die "Check 310 cannot place the state directory: HOME is '$home', which is not absolute. usage: $USAGE" ;;
    esac
    STATE_BASE="$home/.local/state"
  fi
  LOG_DIR="$STATE_BASE/fugaro/logs"
  fact state-dir PASS "the state directory is $STATE_BASE and the logs are read at $LOG_DIR (the same rule localcfg.UserDir uses: \$XDG_STATE_HOME where it is set and absolute, else \$HOME/.local/state)"
}

# --------------------------------------------------------------- the terminal
# The three terminal readings need a real pty, because the reporter decides
# colour on the OUTPUT writer: `script` gives the child a pty on stdout while
# this script keeps its own stdout for the RESULT block. Both spellings are
# probed rather than assumed -- util-linux takes `-e -c CMD` and BSD (macOS)
# takes the command as arguments -- and the probe is what decides which one this
# machine has.
SCRIPT_FLAVOUR=none
detect_script() {
  # Both probes take their input from /dev/null: `script` copies its own stdin
  # into the pty it hands the child, so a probe left on the terminal would eat
  # the answer to the question below before the question is asked.
  if "$SCRIPT_BIN" -q -e -c true /dev/null </dev/null >/dev/null 2>&1; then
    SCRIPT_FLAVOUR=util
  elif "$SCRIPT_BIN" -q /dev/null /bin/sh -c true </dev/null >/dev/null 2>&1; then
    SCRIPT_FLAVOUR=bsd
  else
    SCRIPT_FLAVOUR=none
  fi
}

# pty_run CAPTURE CMD-STRING: CMD-STRING with a pty on its stdout, its output
# captured in CAPTURE and, through tee, still on your terminal -- the spinner is
# only judgeable if you watched it. tee writes to this script's own STDERR, so
# the run is watched while stdout stays the RESULT block and nothing else: an
# owner who pipes this check's stdout somewhere gets the block, not a second
# copy of five converge runs. The command string carries its own
# `< /dev/null`, so the child's INPUT is not a terminal: a stage that would ask
# a question refuses instead of waiting for a line nobody is going to type. The
# same redirection is on `script` itself, so it never reads this run's own input
# (the answer to the question below is read by this script and by nobody else).
# The pipeline's status is `script`'s own (pipefail), and `script -e` passes the
# child's exit code through, so PTY_STATUS is the converge's.
PTY_STATUS=0
pty_run() {
  PTY_STATUS=0
  case "$SCRIPT_FLAVOUR" in
  util) "$SCRIPT_BIN" -q -e -c "$2" /dev/null </dev/null | "$TEE_BIN" "$1" >&2 || PTY_STATUS=$? ;;
  bsd) "$SCRIPT_BIN" -q /dev/null /bin/sh -c "$2" </dev/null | "$TEE_BIN" "$1" >&2 || PTY_STATUS=$? ;;
  *) return 3 ;;
  esac
}

# ------------------------------------------------------------- running fugaro
# FUGARO_ARGS is built once: the binary, the project you named, and any extra
# flags. It is never empty, so expanding it under `set -u` is safe on every bash
# this script can meet.
FUGARO_ARGS=()
fugaro_args() {
  FUGARO_ARGS=("$FUGARO_BIN" --project "$PROJECT")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then
    FUGARO_ARGS+=("${FUGARO_FLAGS[@]}")
  fi
}

# The command string the terminal readings hand to `script`, built from the same
# words the piped ones use, so the two cannot read different commands. The
# project name is held to the config's pattern by guard_project, which is what
# makes this one command rather than several; `< /dev/null` is the input half
# described at pty_run.
pty_command() { # pty_command [args...]
  printf '%s < /dev/null' "${FUGARO_ARGS[*]} $*"
}

# run_fugaro OUT ERR [args...]: the CLI with its output captured and its input
# /dev/null. EXTRA_PATH is prepended to PATH when it is set (the forced-failure
# stub) and NO_COLOR_RUN adds NO_COLOR=1 for the run that reads that rule.
FUGARO_STATUS=0
run_fugaro() {
  local out=$1 err=$2
  shift 2
  local path=$PATH
  [ -z "$EXTRA_PATH" ] || path="$EXTRA_PATH:$PATH"
  FUGARO_STATUS=0
  if [ "$NO_COLOR_RUN" = yes ]; then
    PATH="$path" NO_COLOR=1 "${FUGARO_ARGS[@]}" "$@" </dev/null >"$out" 2>"$err" || FUGARO_STATUS=$?
  else
    PATH="$path" "${FUGARO_ARGS[@]}" "$@" </dev/null >"$out" 2>"$err" || FUGARO_STATUS=$?
  fi
}

# ------------------------------------------------------------- reading output
# esc_count FILE: how many escape bytes the file holds. CL5b's rule is about the
# escape byte, so this is the reading itself rather than a proxy for it.
esc_count() {
  python3 - "$1" <<'PY'
import sys
with open(sys.argv[1], 'rb') as fh:
    print(fh.read().count(b'\x1b'))
PY
}

# summary_from FILE: the last line that is the run's closing line, or nothing.
# It is read as the last one that matches and not as the file's last line,
# because a failed run prints its error line after it and a nag about a newer
# release may follow that. The carriage return a pty puts before every newline
# is taken off first: without that the `$` anchor never matches a terminal run's
# own closing line, and the reading would be of this check's capture rather than
# of the run.
summary_from() {
  sed 's/\r$//' "$1" 2>/dev/null | grep -E '^(Done|Failed|Left for you) in [0-9]+\.[0-9]s, ([0-9]+ steps|1 step)$' | tail -1 || true
}

# last_line FILE: the file's last non-empty line, with the carriage returns a
# pty adds to every newline taken off it.
last_line() {
  python3 - "$1" <<'PY'
import sys
with open(sys.argv[1], 'rb') as fh:
    text = fh.read().decode('utf-8', 'replace')
lines = [ln.rstrip('\r') for ln in text.split('\n')]
lines = [ln for ln in lines if ln.strip()]
print(lines[-1] if lines else '')
PY
}

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# has_any FILE STR...: whether the file carries any of the strings, byte for
# byte (the marks the reporter draws are not ASCII).
has_any() {
  local f=$1
  shift
  local s
  for s in "$@"; do
    if grep -qF -- "$s" "$f" 2>/dev/null; then return 0; fi
  done
  return 1
}

file_mode() {
  python3 -c 'import os, sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$1" 2>/dev/null || printf 'unknown'
}

# ------------------------------------------------------------- phase: look
# read_look_run NAME CAPTURE [args...]: one run of the CLI whose output is read
# for the three things CL5b says about a terminal -- colour, the line in flight,
# and the closing line -- with the two numbers the reading is about (escape
# bytes, and whether the clear sequence the spinner ends with is there).
LOOK_ESC=0
LOOK_SUMMARY=""
LOOK_SPIN=no
look_capture() { # look_capture CAPTURE [args...]
  local capture=$1
  shift
  pty_run "$capture" "$(pty_command "$@")"
  LOOK_ESC=$(esc_count "$capture")
  LOOK_SUMMARY=$(summary_from "$capture")
  LOOK_SPIN=no
  if python3 - "$capture" <<'PY'
import sys
with open(sys.argv[1], 'rb') as fh:
    sys.exit(0 if b'\r\x1b[K' in fh.read() else 1)
PY
  then
    LOOK_SPIN=yes
  fi
}

phase_look() {
  need "$SCRIPT_BIN" "the terminal readings need it to give the child a pty, and it is neither util-linux's nor BSD's here"
  detect_script
  if [ "$SCRIPT_FLAVOUR" = none ]; then
    stop "the terminal readings need a 'script' this check recognises (util-linux's '-e -c CMD' or BSD's 'FILE CMD'), and $SCRIPT_BIN is neither"
  fi
  say "terminal readings run under $SCRIPT_BIN ($SCRIPT_FLAVOUR spelling); each one is also printed here, so you can watch it"

  # 1. `fugaro update` at a real terminal, which you watch.
  local cap="$SCRATCH/tty-update"
  say "1/5: ${FUGARO_ARGS[*]} update (real terminal, apply)"
  look_capture "$cap" update
  local glyphs="✔ ✖ ○ – ↳"
  if [ "$LOOK_ESC" -gt 0 ] && has_any "$cap" '✔' '✖' '○' '–' '↳' '!'; then
    fact tty-colour PASS "at a terminal the run is painted: $LOOK_ESC escape bytes, and the step lines carry the reporter's own marks (✔ ✖ ○ – ↳)"
  elif [ "$LOOK_ESC" -eq 0 ]; then
    fact tty-colour FAIL "at a terminal the run carried no escape byte at all: colour is off where it must be on (CL5b, G4), and the marks are then drawn plain. First line: $(first_line "$cap")"
  else
    fact tty-colour FAIL "the run carried $LOOK_ESC escape bytes but none of the reporter's marks ($glyphs): the account is not the one the reporter draws. First line: $(first_line "$cap")"
  fi
  if [ "$LOOK_SPIN" = yes ]; then
    fact tty-spinner PASS "the line in flight was drawn and taken down again: the capture carries \\r\\x1b[K, which is the clear the spinner ends every frame with"
  else
    fact tty-spinner FAIL "no \\r\\x1b[K in the capture: either the line in flight was never drawn at a terminal, or it was drawn and never cleared -- which leaves its frame on the line the step's own result prints over"
  fi
  if [ -n "$LOOK_SUMMARY" ]; then
    fact tty-summary PASS "the run closes with its own line: '$LOOK_SUMMARY'"
  else
    fact tty-summary FAIL "the run printed no closing line (the shape is 'Done in 3.2s, 5 steps', or Failed / Left for you): a run whose account has no last line cannot be read at a glance. Last line: $(last_line "$cap")"
  fi

  # The one thing no script can judge. The question is asked only where there is
  # a terminal to answer on: a run whose stdin is a pipe records the row as NOT
  # MEASURED rather than reading an EOF as an answer.
  local answer=""
  if [ -t 0 ]; then
    owner "QUESTION -- in the run above, did the line in flight look right? It should be a spinner (| / - \\ turning in place) with the step's name beside it, taken down before the step's own result line, leaving nothing behind it on the line. Answer y or n:"
    if IFS= read -r answer; then :; else answer=""; fi
  fi
  case "$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')" in
  y | yes) fact spinner PASS "you watched the line in flight and said it looked right: a spinner with the step's name beside it, taken down before the step's own line (this is the one reading a script cannot take, and it is yours)" ;;
  n | no) fact spinner FAIL "you watched the line in flight and said it does NOT look right: the spinner is the reading no script can take, so what you saw is the reading" ;;
  "") fact spinner INFO "not measured: this run has no terminal on its input to ask the question at, so whether the spinner LOOKED right is unread here. Run it at a real terminal to answer it" ;;
  *) fact spinner INFO "not measured: the answer was '$answer', which is neither yes nor no" ;;
  esac

  # 2. The plan view: the same reporter, the rows of a run that is not a run.
  cap="$SCRATCH/tty-plan"
  say "2/5: ${FUGARO_ARGS[*]} setup --plan-only (real terminal, creates nothing)"
  look_capture "$cap" setup --plan-only
  if [ "$LOOK_ESC" -gt 0 ] && has_any "$cap" '○' '–' '↳' 'o'; then
    fact setup-plan PASS "the plan view is painted at a terminal ($LOOK_ESC escape bytes) and its rows carry the plan's own marks (○ will do, – skipped, ↳ waiting). A plan-only run prints no closing line by design (the loop's account is the execution's), so this row is the rows and not a summary: last line '$(last_line "$cap")'"
  elif [ "$LOOK_ESC" -eq 0 ]; then
    fact setup-plan FAIL "the plan view carried no escape byte at a terminal: the same reporter draws it, so colour is off where it must be on. First line: $(first_line "$cap")"
  else
    fact setup-plan FAIL "the plan view carried $LOOK_ESC escape bytes but none of its own marks (○ – ↳): last line '$(last_line "$cap")'"
  fi

  # 3. NO_COLOR at a terminal, where the rule has something to turn off.
  cap="$SCRATCH/tty-nocolor"
  say "3/5: NO_COLOR=1 ${FUGARO_ARGS[*]} update (real terminal)"
  pty_run "$cap" "NO_COLOR=1 $(pty_command update)"
  local nocolor_esc
  nocolor_esc=$(esc_count "$cap")
  if [ "$nocolor_esc" -eq 0 ]; then
    fact no-color PASS "NO_COLOR=1 at a terminal turned colour off: no escape byte in the capture, and the same run without it carried $(esc_count "$SCRATCH/tty-update") (the no-color.org rule is 'present and not empty')"
  else
    fact no-color FAIL "NO_COLOR=1 at a terminal still painted the run: $nocolor_esc escape bytes. NO_COLOR is the rule that has to win at a terminal, where colour would otherwise be on"
  fi

  # 4. A pipe, where no escape byte may appear at all.
  cap="$SCRATCH/piped"
  local piped_err="$SCRATCH/piped.err"
  say "4/5: ${FUGARO_ARGS[*]} update | cat (stdout is a pipe)"
  local piped_status=0
  "${FUGARO_ARGS[@]}" update </dev/null 2>"$piped_err" | cat >"$cap" || piped_status=$?
  local piped_esc
  piped_esc=$(esc_count "$cap")
  if [ "$piped_esc" -eq 0 ]; then
    fact piped PASS "a pipe is not a terminal and the run wrote no escape byte to it (exit $piped_status): the account is plain text, which is what a log, a CI job and a `| grep` all need (CL5b, T4(a))"
  else
    fact piped FAIL "the run wrote $piped_esc escape bytes to a pipe: colour has to be off wherever the output is not a terminal, whatever CLICOLOR_FORCE says (T4(a))"
  fi

  # 5. --json: one document on stdout, the human lines on stderr.
  cap="$SCRATCH/json"
  local json_err="$SCRATCH/json.err"
  say "5/5: ${FUGARO_ARGS[*]} update --json"
  run_fugaro "$cap" "$json_err" update --json
  local reading
  reading=$(json_reading "$cap" "$json_err")
  case "$reading" in
  ok:*) fact json PASS "${reading#ok: }" ;;
  *) fact json FAIL "${reading#bad: }" ;;
  esac

  # The log directory is where every run above filed its own log, so this is
  # read after them rather than guessed: the newest update log's stamp has to be
  # from this run.
  if [ ! -d "$LOG_DIR" ]; then
    fact log-dir FAIL "the log directory $LOG_DIR is not there after five converges: a converge that keeps no log leaves a failure with no path to print (CL3, D1352)"
  else
    local newest
    newest=$(newest_log update)
    if [ -n "$newest" ]; then
      fact log-dir PASS "the runs above filed their logs in $LOG_DIR (the newest is $newest), which is where a failure's path points"
    else
      fact log-dir FAIL "$LOG_DIR holds no update-*.log after five converges: the log is what a failure's 'full output:' line names, and there is none"
    fi
  fi
}

# json_reading FILE ERRFILE: the --json reading as one line, "ok: ..." or
# "bad: ...". A document that parses on the WHOLE of stdout is the reading that
# nothing human was printed there; the project it names is the half that proves
# it is this run's result and not another run's.
json_reading() {
  python3 - "$1" "$2" "$PROJECT" <<'PY'
import json
import sys

path, errpath, want = sys.argv[1:4]
with open(path, 'rb') as fh:
    raw = fh.read()
if b'\x1b' in raw:
    print('bad: the --json run wrote an escape byte to stdout, which is a document and not a screen (G4)')
    raise SystemExit
try:
    doc = json.loads(raw.decode('utf-8', 'replace'))
except Exception as exc:
    print('bad: stdout is not one JSON document (%s): first line %r' % (exc, raw.decode('utf-8', 'replace').split('\n')[0][:200]))
    raise SystemExit
if not isinstance(doc, dict):
    print('bad: stdout is a JSON %s, and --json prints the run\'s result object' % type(doc).__name__)
    raise SystemExit
if doc.get('project') != want:
    print('bad: the document names project %r, and %r was typed' % (doc.get('project'), want))
    raise SystemExit
stages = doc.get('stages')
if not isinstance(stages, list) or not stages:
    print('bad: the document carries no stages, and a converge always runs some')
    raise SystemExit
try:
    with open(errpath, encoding='utf-8', errors='replace') as fh:
        human = fh.read()
except OSError:
    human = ''
marks = ['✔', '✖', '○', '–', '↳', '!']
if not any(m in human for m in marks):
    print('bad: the human step lines did not go to stderr, so a reader of the document has nowhere to watch the run (G4)')
    raise SystemExit
print('ok: stdout is one JSON document naming project %s, with %d stages, %d left for you and no failure recorded; the step lines are on stderr, where --json sends them'
      % (doc.get('project'), len(stages), len(doc.get('left_for_you') or [])))
PY
}

# ------------------------------------------------------------- phase: failure
# write_stub: the terraform this check forces its failure with. It answers the
# one read every converge makes before it plans anything (`version -json`, which
# tf.New parses) and refuses every other subcommand, so the run fails inside a
# stage with the reporter open and prints the block G8 is about. The two lines
# it writes to stderr are the context that block has to carry: the first is the
# step's own reason (one line, shortened on the screen and whole in the log) and
# the second is what the reader gets under it.
STUB_MARK="check310's terraform stub refused every subcommand but 'version -json'"
STUB_LINE_ONE="Error: check310 forced this failure: the terraform on PATH refused to plan or apply anything, so nothing was created and nothing was changed"
STUB_LINE_TWO="check310's own context line: this failure was forced by scripts/live-checks/check310.sh, not by your installation"
write_stub() {
  STUB_DIR="$SCRATCH/stub"
  mkdir -p "$STUB_DIR"
  cat >"$STUB_DIR/terraform" <<'STUB_EOF'
#!/bin/sh
# Check 310's forced failure. The version read is answered, because every
# converge makes it before it plans anything and a run that failed there would
# fail before the stage this reading is about.
case "${1:-}" in
version) printf '{"terraform_version":"1.9.5"}\n'; exit 0 ;;
esac
printf '%s\n' 'Error: check310 forced this failure: the terraform on PATH refused to plan or apply anything, so nothing was created and nothing was changed' >&2
printf '%s\n' "check310's own context line: this failure was forced by scripts/live-checks/check310.sh, not by your installation" >&2
exit 1
STUB_EOF
  chmod 755 "$STUB_DIR/terraform"
}

phase_failure() {
  write_stub
  local cap="$SCRATCH/failure"
  local err="$SCRATCH/failure.err"
  say "the forced failure: ${FUGARO_ARGS[*]} update, with this check's own terraform first on PATH"
  EXTRA_PATH="$STUB_DIR"
  run_fugaro "$cap" "$err" update
  EXTRA_PATH=""
  say "  (exit $FUGARO_STATUS; nothing was applied: the only terraform on that run's PATH refuses every plan and apply)"

  # The block has to be there at all: a run that refused at the door (a missing
  # project config, an unknown flag) prints no step line and no log path, and
  # reading four FAILs off that would be an answer about the output for a run
  # that never reached it.
  local step
  step=$(grep -m1 -E '^✖ ' "$cap" 2>/dev/null || true)
  if [ -z "$step" ] && ! grep -q 'full output: ' "$cap" 2>/dev/null; then
    fact fail-step FAIL "the forced failure printed no failed step line and no log path: the run refused before it reached a stage. First line: $(first_line "$cap")"
    stop "the forced failure produced no failure block at all, so there is nothing to read: $(first_line "$cap")"
  fi
  if [ -n "$step" ]; then
    fact fail-step PASS "the failure is one step's own line, with the reporter's failed mark and the reason after it: '$(printf '%s' "$step" | cut -c1-240)'"
  else
    fact fail-step FAIL "no line carries the failed step's mark (✖): the run failed without saying which step it failed in. First line: $(first_line "$cap")"
  fi

  # The context under the step: the error's own lines, four spaces in.
  local context=""
  context=$(grep -E '^    [^ ]' "$cap" 2>/dev/null | grep -v '^    full output: ' | grep -v '^    Something went wrong\.' | head -3 || true)
  if [ -n "$context" ]; then
    fact fail-context PASS "the error's context is printed under the step's line, four spaces in: '$(printf '%s' "$context" | head -1 | cut -c1-240)'"
  else
    fact fail-context FAIL "nothing is printed under the failed step's line: the reader gets the reason and none of what went wrong behind it (G8). Capture: $(first_line "$cap")"
  fi

  # The log's path, and the id the diagnose line routes by: the path's own stem,
  # so `fugaro troubleshoot <id>` reads the file the failure already sent you to.
  local logpath="" id="" want_id=""
  logpath=$(grep -m1 -E '^    full output: .+' "$cap" 2>/dev/null | sed 's/^    full output: //' || true)
  id=$(grep -m1 -E '^    Something went wrong\. Run: fugaro troubleshoot .+' "$cap" 2>/dev/null | sed 's/^    Something went wrong\. Run: fugaro troubleshoot //' || true)
  if [ -n "$logpath" ]; then
    want_id=${logpath##*/}
    want_id=${want_id%.log}
  fi
  if [ -n "$logpath" ] && [ -f "$logpath" ]; then
    if [ -n "$id" ] && [ "$id" = "$want_id" ]; then
      fact fail-log-path PASS "the failure names its log ($logpath) and the id 'fugaro troubleshoot $id' routes by, which is that file's own stem, so one word finds the whole output of the run that failed"
    elif [ -n "$id" ]; then
      fact fail-log-path FAIL "the failure names its log ($logpath) and a troubleshoot id ('$id') that is not that file's stem ('$want_id'): the id would route a person to another run's log"
    else
      fact fail-log-path FAIL "the failure names its log ($logpath) and prints no 'Something went wrong. Run: fugaro troubleshoot <id>' line under it: the whole output is one command away and the command is not named"
    fi
  elif [ -n "$logpath" ]; then
    fact fail-log-path FAIL "the failure names a log at $logpath and no such file is there: the path it prints is the one thing that saves a rerun with --verbose"
  else
    fact fail-log-path FAIL "the failure prints no 'full output: <path>' line: the reason was shortened on the screen and there is nothing to read the rest in (D1352)"
  fi

  # The file the path names: 0600 in a 0700 directory, and holding the run's own
  # lines whole.
  if [ -n "$logpath" ] && [ -f "$logpath" ]; then
    local fmode dmode
    fmode=$(file_mode "$logpath")
    dmode=$(file_mode "${logpath%/*}")
    if [ "$fmode" = 600 ] && [ "$dmode" = 700 ]; then
      fact fail-log-mode PASS "the log the failure named is mode 0600 in a directory of mode 0700 ($logpath): a log holds a run's own output, which can carry a value, so nobody else on the machine reads it"
    else
      fact fail-log-mode FAIL "the log the failure named is mode $fmode in a directory of mode $dmode ($logpath): CL3 asks for 0600 in a 0700 directory"
    fi
    if has_any "$logpath" "$STUB_LINE_TWO" && grep -q 'full output: ' "$logpath" 2>/dev/null; then
      fact fail-log-context PASS "the log holds the run's own lines whole: the context the screen carried, the failure block's own lines and the closing line are all in $logpath (D1352)"
    else
      fact fail-log-context FAIL "the log at $logpath does not hold what the run printed (the failure block's lines and the closing line): the path the failure prints has to lead to the whole of it (D1352). First line: $(first_line "$logpath")"
    fi
  else
    fact fail-log-mode FAIL "there is no log file to read a mode off, because the failure named no path that exists"
    fact fail-log-context FAIL "there is no log file to read, because the failure named no path that exists"
  fi
}

# ---------------------------------------------------------------- phase: logs
stamp_days_ago() {
  python3 -c 'import datetime, sys; print((datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=int(sys.argv[1]))).strftime("%Y%m%d%H%M%S"))' "$1"
}

# count_logs NAME: how many files in the log directory are that command's own
# logs, by the name rule OpenLog writes and its pruning reads back
# (`<command>-<14 digits>-<pid>.log`, and nothing else).
count_logs() {
  ls -1 "$LOG_DIR" 2>/dev/null | grep -Ec "^$1-[0-9]{14}-[0-9]+\.log$" || true
}

# newest_log NAME: the newest of them, by name -- the stamp is the first thing
# that differs, so sorting the names sorts them by time.
newest_log() {
  ls -1 "$LOG_DIR" 2>/dev/null | grep -E "^$1-[0-9]{14}-[0-9]+\.log$" | sort | tail -1 || true
}

phase_logs() {
  if [ ! -d "$LOG_DIR" ]; then
    stop "the log directory $LOG_DIR is not there, so there is no retention to read: run --phase look first (or the whole check), which makes it"
  fi
  local dmode
  dmode=$(file_mode "$LOG_DIR")
  if [ "$dmode" = 700 ]; then
    fact log-dir-mode PASS "the log directory is mode 0700 ($LOG_DIR): the logs are the owner's own"
  else
    fact log-dir-mode FAIL "the log directory is mode $dmode ($LOG_DIR): CL3 asks for 0700"
  fi

  write_stub
  local newest_before newest_after
  newest_before=$(newest_log update)

  # The two planted logs: one stamped twenty days ago, which the next converge
  # has to prune (14 days), and one of another command's name beside it, which
  # it must not touch (the pruning is per command name, and a log of another
  # command is not this command's to remove).
  local aged image
  aged="update-$(stamp_days_ago 20)-$$.log"
  image="image-$(stamp_days_ago 0)-$$.log"
  if [ -e "$LOG_DIR/$aged" ] || [ -e "$LOG_DIR/$image" ]; then
    stop "the log directory already holds $aged or $image, which is this check's own name for the logs it plants: run again in a moment"
  fi
  printf 'a log the check planted, stamped twenty days ago: the next converge has to prune it\n' >"$LOG_DIR/$aged"
  printf 'a log of another command name, which the next converge must not touch\n' >"$LOG_DIR/$image"
  chmod 600 "$LOG_DIR/$aged" "$LOG_DIR/$image"
  PLANTED+=("$aged" "$image")
  say "planted $aged (twenty days old) and $image (another command's) in $LOG_DIR"

  say "one failing update, to read what the retention does to them"
  EXTRA_PATH="$STUB_DIR"
  run_fugaro "$SCRATCH/logs-1.out" "$SCRATCH/logs-1.err" update
  EXTRA_PATH=""
  newest_after=$(newest_log update)
  # The converge has to have filed a log of its OWN: a newest log that is still
  # the one before it, or the twenty-day-old one this check planted (which is
  # the newest of its name only when nothing else was written), is a converge
  # that kept no log, and the retention below would then be read on this check's
  # own files.
  if [ "$newest_after" = "$newest_before" ] || [ "$newest_after" = "$aged" ]; then
    stop "the converge filed no log of its own (the newest update log is still '${newest_after:-none}'), so there is no retention to read"
  fi
  if [ -e "$LOG_DIR/$aged" ]; then
    fact log-old-pruned FAIL "the log stamped twenty days ago is still there after a converge ($LOG_DIR/$aged): CL3 keeps a log for 14 days, so the run that opened a new one had to remove it"
  else
    fact log-old-pruned PASS "a log stamped twenty days ago was pruned by the next converge's own open: CL3's 14 days, read on a file this check planted rather than on one of yours"
  fi
  if [ -e "$LOG_DIR/$image" ]; then
    fact log-other-command PASS "a log of another command's name beside it was left alone ($image): the pruning reads the name back and removes only this command's own logs, so no run of one command takes another's output away"
  else
    fact log-other-command FAIL "a converge removed a log of ANOTHER command's name ($LOG_DIR/$image): the pruning has to match the command's own name exactly, or a run takes away a log it does not own"
  fi

  # The newest 50 per command name: 51 failing updates, each of which opens a
  # log, so the rule's own count is what is left.
  local runs=$RUNS i=0
  if [ "$runs" -lt 51 ]; then
    say "--runs $runs is fewer than the 51 the newest-50 rule needs: the retention is left as not measured"
    fact log-keep INFO "not measured: --runs $runs is fewer than 51, and the newest-50-per-command-name rule only shows itself past 50 logs"
  else
    say "running $((runs - 1)) more failing updates, to pass 50 logs of this command's name"
    i=1
    while [ "$i" -lt "$runs" ]; do
      EXTRA_PATH="$STUB_DIR"
      run_fugaro "$SCRATCH/logs.out" "$SCRATCH/logs.err" update
      EXTRA_PATH=""
      i=$((i + 1))
    done
    local count kept newest
    count=$(count_logs update)
    kept=$(count_logs image)
    newest=$(newest_log update)
    if [ "$count" = 50 ]; then
      fact log-keep PASS "after $runs runs exactly 50 update-*.log files are left in $LOG_DIR (the newest 50 per command name), the newest is this run's own ($newest), and the image-*.log beside them is untouched"
    elif [ "$count" -gt 50 ]; then
      fact log-keep FAIL "$count update-*.log files are left after $runs runs: CL3 keeps the newest 50 per command name. (A converge running elsewhere on this machine while this check ran would also add one; the count above is what is there now)"
    else
      fact log-keep FAIL "only $count update-*.log files are left after $runs runs: the rule keeps the newest 50 per command name, so a run removed more than it had to"
    fi
    if [ "$kept" -ge 1 ]; then
      say "  ($count update logs and $kept image logs in $LOG_DIR)"
    fi
  fi

  local newest_mode
  newest_mode=$(file_mode "$LOG_DIR/$(newest_log update)")
  if [ "$newest_mode" = 600 ]; then
    fact log-file-mode PASS "the newest update log is mode 0600 ($LOG_DIR/$(newest_log update)): a log holds a run's own output, which can carry a value"
  else
    fact log-file-mode FAIL "the newest update log is mode $newest_mode, and CL3 asks for 0600"
  fi

  # The row's own step, printed where you can read it: the plan's expectation is
  # this listing.
  owner "ls -l $LOG_DIR:"
  ls -l "$LOG_DIR" >&4 2>/dev/null || true
}

# ---------------------------------------------------------------- the plan
# print_plan: what a real run would do, printed by --dry-run INSTEAD of doing any
# of it. This check's own test pins that a dry run runs nothing at all -- no
# fugaro, no script, no read of the log directory -- so the plan names the
# commands and never runs one.
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [runs] ${FUGARO_ARGS[*]} update, under \`script\` so its stdout is a real pty, its output captured and printed to you: colour on, the line in flight drawn with \\r and cleared with \\r\\x1b[K, and the closing line ('Done in 3.2s, 5 steps')"
  say "  [asks] ONE yes/no question, on your terminal: did the line in flight look right? A run with no terminal on its input records the row as not measured instead"
  say "  [runs] ${FUGARO_ARGS[*]} setup --plan-only, the same way: the plan view's own marks and its colour (a plan-only run prints no closing line, so the row is the rows)"
  say "  [runs] NO_COLOR=1 ${FUGARO_ARGS[*]} update, the same way: no escape byte may appear AT A TERMINAL"
  say "  [runs] ${FUGARO_ARGS[*]} update | cat: no escape byte may appear on a pipe"
  say "  [runs] ${FUGARO_ARGS[*]} update --json: stdout is one JSON document naming the project, the step lines are on stderr"
  say "  [writes] a \`terraform\` of this check's own, first on PATH for the runs that force a failure: it answers \`version -json\` and refuses every other subcommand, so nothing is planned and nothing is applied"
  say "  [runs] ${FUGARO_ARGS[*]} update with that stub: the failed step line, the context under it, 'full output: <path>' and 'Something went wrong. Run: fugaro troubleshoot <id>' -- then that file: 0600 in a 0700 directory, holding the run's own lines"
  say "  [reads] $LOG_DIR: its mode (0700) and the newest update log's (0600)"
  say "  [writes] two planted logs in $LOG_DIR (one stamped twenty days ago, one of another command's name), removed at the end; then ${RUNS} failing updates, and the count that is left (50 update-*.log, the newest 50 per command name)"
  say "  [reads] ls -l $LOG_DIR, printed for you"
  say "  [writes] the teardown: the planted logs removed, the scratch directory removed"
  say "Nothing is created in the cloud by the forced-failure runs (their only terraform refuses everything), no model call is made and nothing is billable."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project belong session state-dir log-dir tty-colour tty-spinner
  tty-summary setup-plan no-color piped json spinner fail-step fail-context
  fail-log-path fail-log-mode fail-log-context log-dir-mode log-file-mode
  log-old-pruned log-keep log-other-command teardown output)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 310 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
  while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done <<<"$facts"
  printf 'CHECK 310 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and ran nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer
  # the rows that read "not reached" as an answer about the output when the
  # commands that would have filled them never ran. Any such ending is an abort,
  # and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the output"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local name path
  for name in ${PLANTED[@]+"${PLANTED[@]}"}; do
    path="$LOG_DIR/$name"
    if [ -e "$path" ]; then
      facts="${facts}"$'\n'"planted|FAIL|the log this check planted is STILL THERE at $path -- remove it yourself"
    fi
  done
  if [ -n "$SCRATCH" ]; then
    local planted_note
    if [ ${#PLANTED[@]} -gt 0 ]; then
      planted_note="and every log this check planted in $LOG_DIR was removed (the twenty-day-old one by the CLI's own pruning, the other here); no log of yours was deleted"
    else
      planted_note="and this run planted no log (a phase that plants one was not selected)"
    fi
    if [ "$SCRATCH_REMOVED" = yes ]; then
      facts="${facts}"$'\n'"teardown|PASS|the scratch directory is gone, $planted_note"
    else
      facts="${facts}"$'\n'"teardown|FAIL|the scratch directory is STILL THERE at $SCRATCH -- remove it yourself"
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# ------------------------------------------------------------------- the verdict
row_verdict() {
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

# VERDICT_KEYS are the readings the verdict is about. A row that was never
# reached, or one recorded as NOT MEASURED, is not a failure: only a FAIL is,
# and the verdict then names it.
VERDICT_KEYS=(project belong session state-dir log-dir tty-colour tty-spinner
  tty-summary setup-plan no-color piped json spinner fail-step fail-context
  fail-log-path fail-log-mode fail-log-context log-dir-mode log-file-mode
  log-old-pruned log-keep log-other-command)

the_verdict() {
  local key drifted=() list
  for key in "${VERDICT_KEYS[@]}"; do
    if [ "$(row_verdict "$key")" = FAIL ]; then drifted+=("$key"); fi
  done
  if [ ${#drifted[@]} -eq 0 ]; then
    fact output PASS "the default output holds on this machine, as far as this run read it: colour and a line in flight at a real terminal, no escape byte on a pipe or with NO_COLOR, one JSON document for --json, a failure that carries its context and the path of the log holding the whole of it, and a log directory that keeps 0600 files inside a 0700 directory, prunes at 14 days and holds the newest 50 per command name (CL5b, CL1, CL3)"
    REACHED_END=yes
    exit 0
  fi
  list=$(printf '%s, ' "${drifted[@]}")
  list=${list%, }
  fact output FAIL "the default output does not hold on this machine: $list. Each row above says which reading it is and what it found"
  REACHED_END=yes
  exit 1
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK310_PROJECT:-}
  PHASE=all
  RUNS=${CHECK310_RUNS:-51}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK310_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a Fugaro project name. usage: $USAGE"
      PROJECT=$2
      shift 2
      ;;
    --phase)
      [ $# -ge 2 ] || die "--phase needs all, look, failure or logs. usage: $USAGE"
      case "$2" in
      all | look | failure | logs) PHASE=$2 ;;
      *) die "--phase takes all, look, failure or logs, and '$2' is none of them. usage: $USAGE" ;;
      esac
      shift 2
      ;;
    --runs)
      [ $# -ge 2 ] || die "--runs needs a number. usage: $USAGE"
      case "$2" in
      '' | *[!0-9]*) die "--runs takes a number, and '$2' is not one. usage: $USAGE" ;;
      esac
      RUNS=$2
      shift 2
      ;;
    --fugaro-flags)
      [ $# -ge 2 ] || die "--fugaro-flags needs a string of flags. usage: $USAGE"
      read -r -a FUGARO_FLAGS <<<"$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
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
  fugaro_args
}

# skip_missing_phases records the rows of a phase that was not selected. They are
# NOT MEASURED and not failures: the verdict reads FAILs only, so a run of one
# phase is an answer about that phase and says so about the others.
skip_missing_phases() {
  local key
  if [ "$PHASE" = look ] || [ "$PHASE" = failure ] || [ "$PHASE" = logs ]; then
    for key in "${VERDICT_KEYS[@]}"; do
      has_fact "$key" || fact "$key" INFO "not measured: --phase $PHASE was selected and this row belongs to another phase (--phase all runs every one of them)"
    done
  fi
}

main() {
  parse_args "$@"
  scratch_up
  # The state directory is resolved before the dry-run branch because resolving
  # it reads no file: it is $XDG_STATE_HOME or $HOME and a fixed suffix, so the
  # plan can name the log directory it would read without touching anything.
  resolve_state_dir
  if [ -n "$DRY_RUN" ]; then
    # A dry run reads nothing: the guards are judged on what was TYPED, and the
    # commands and the reads are the plan's business.
    if [ -n "$PROJECT" ]; then
      guard_project
      guard_belong
    else
      fact project INFO "no --project was typed, and a dry run runs nothing: a real run requires one"
      fact belong INFO "no project was typed, so the guard had nothing to judge: a real run refuses a name that matches the installation 'belong' before it runs anything"
    fi
    fact dry-run PASS "the guards and the plan only: no command was run, no file was read, no log was planted and nothing was applied"
    BLOCK_KEYS=(project belong state-dir dry-run)
    print_plan
    REACHED_END=yes
    return 0
  fi
  need_tools
  guard_project
  guard_belong
  guard_session
  case "$PHASE" in
  all | look) phase_look ;;
  esac
  case "$PHASE" in
  all | failure) phase_failure ;;
  esac
  case "$PHASE" in
  all | logs) phase_logs ;;
  esac
  skip_missing_phases
  the_verdict
}

RUNS=${CHECK310_RUNS:-51}
FUGARO_BIN=${CHECK310_FUGARO:-fugaro}
SCRIPT_BIN=${CHECK310_SCRIPT:-script}
TEE_BIN=${CHECK310_TEE:-tee}
PROJECT=""
PHASE=all
NO_COLOR_RUN=no
EXTRA_PATH=""
LOOK_ESC=0
LOOK_SUMMARY=""
LOOK_SPIN=no

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
