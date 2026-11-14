#!/usr/bin/env bash
# Check 380 — `fugaro update` with stdin closed finishes without a prompt, as ONE
# script YOU run.
#
# The no-prompt policy's first rule is that no command waits on stdin
# (docs/no-prompt-policy/implementation-plan.md, T7): the typed confirmations
# are gone, and where a step would have asked, a run that cannot confirm it
# refuses or leaves the step for you rather than printing a question and
# waiting on an answer nobody may be there to type. `fugaro update` is the
# command that rule is worth most on: this repository's HARD RULE is that
# `fugaro update` is the whole upgrade, so a run that stopped to ask would stop
# a person's upgrade on a screen they may never be looking at.
#
# No unit test can take this reading. internal/cli/noprompt_harness_test.go
# runs every leaf of the tree with a closed stdin in a hermetic world, and
# `fugaro update` there refuses on that world's own runs bucket name before its
# first stage — the harness says so in closedStdinUnreached. What remains is the
# reading only a real installation can give, and B4 (the scheduling row) puts it
# in your hands: **T7 is UNVERIFIED on an installation until you run it.**
#
# What it reads, in this order:
#
#   1. The installation: `fugaro doctor --json`'s `project.name` and
#      `project.gcp_project`, held to what you typed when you typed `--project`.
#      It is read so the block says WHICH installation was converged, and so the
#      `belong` guard can refuse the owner's own before anything runs.
#   2. **The run itself, which is the fact this check is**: `fugaro update` with
#      its stdin on `/dev/null` and its stdout and stderr in files in a scratch
#      directory of this check's own, under a deadline (1800 seconds by
#      default). `/dev/null` is not a terminal and delivers EOF to the first
#      read, so a prompt site that refuses when it cannot see a terminal returns
#      at once — and a site that reads, gets EOF and asks again never returns at
#      all, which is what the deadline is for. A run still going when the
#      deadline fires is killed (TERM, then KILL): a run waiting on a terminal
#      looks exactly like that, and that is the failure this check exists to
#      catch.
#   3. The question, which is what the run's own transcript cannot contain: no
#      line may ask for an answer. The shapes this check refuses are the ones a
#      prompt leaves behind — `[y/N]` in any casing, `(y/n)` and `(yes/no)`,
#      a `to confirm:` left open, an enter-prompt, and a question mark followed
#      by a blank (a question left waiting), which is not the shape this CLI's
#      own text has. The `⚠ CONFIRM` banner is NOT one of them: the policy keeps
#      the banner, which is the note of a step and not a read (askBanner prints
#      no `[y/N]`), and the runs that print it are fine here.
#
# **What a PASS means, and what it does not.** A PASS means the run ended by
# itself and asked nothing: nothing waited, and no question was put to a person
# who was not there. It does NOT mean the converge finished. `fugaro update`
# with no terminal leaves a step it cannot confirm for you and exits 1 with its
# own sentence naming what is left — that is the policy's answer, not a prompt
# (T7: removing a prompt is not the same as applying a step nobody asked for) —
# and this check reports that exit status and quotes the run's own last line in
# the `update` row so you can read which of the two you got. An exit 2 (a cloud
# failure) is a FAIL row: the run stopped on the cloud and read little of its
# steps. A run that printed nothing at all is a row this check could not
# measure, and it says so rather than passing on an empty transcript.
#
# What a real run does to your installation: it converges it, or refuses to,
# exactly as `fugaro update` would if you typed it. This check rolls nothing
# back — a converge is not something to undo, and it is what you asked the
# command for. Run it on the sandbox installation, and know that the release
# under test is the one installed (that is what makes the reading worth having).
#
# Usage:      bash scripts/live-checks/check-380-no-prompt-update.sh [--project <fugaro project>]
#                                                 [--fugaro-flags '<flags>'] [--deadline <seconds>]
#                                                 [--dry-run]
#             --project is the Fugaro project the run is about. Leave it out
#             where a project config is in force: `fugaro doctor --json` answers
#             with it and this check holds the two to each other.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on
#             whitespace, so a value with a space in it cannot be passed.
#             --deadline is how long the run may take, in seconds (default 1800;
#             CHECK380_DEADLINE does the same). It is a bound on waiting, not a
#             build timeout: a real converge of a new release may take minutes.
#             --dry-run prints the plan and makes no call at all, so it asks
#             nothing of your installation and starts no run.
# Needs:      bash, mktemp, grep, head, tail, cut, python3, and a `fugaro` on
#             PATH with `doctor --json` and `update`. Nothing is installed and
#             nothing is fetched; a missing tool stops the run with one line
#             naming it.
# Where:      your own machine, against an installation whose project is NOT
#             `belong`: that name is refused before anything is run, in what you
#             typed and in what doctor answers, because the owner's own
#             installation is never the subject of an owner-run check. The
#             natural place is the sandbox installation (the project the
#             `edgeappinc/fugarosandbox` checkout belongs to). A coding agent's
#             session is refused too: this is yours to run, at your own
#             terminal, and a run inside an agent's session would measure the
#             agent's path and not yours.
# Costs:      one `fugaro doctor --json` (read-only) and one `fugaro update`,
#             which is the command itself: it may build an image, apply
#             Terraform and spend what a converge spends. No model call of this
#             check's own and no run is launched.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 380 RESULT.
#             Exit 0 when the claim held (the run ended by itself and asked
#             nothing), 1 when it did not (the run was still going at the
#             deadline, or a line asked for an answer, or it ended on a cloud
#             failure — each row says which), and 2 when this run could not tell
#             (a guard stopped it, the transcript was empty, a tool is missing,
#             an internal error ended it).
#
# CHECK380_FUGARO names the binary (default `fugaro`), CHECK380_DEADLINE the
# default deadline, CHECK380_DRY_RUN makes every run a dry one and
# CHECK380_KEEP_SCRATCH keeps the scratch directory (with the run's whole
# transcript in update.out and update.err) instead of removing it, so this
# script's own test can drive the same code and you can read what a failing run
# printed.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had: a shell that arrived
# tracing prints every line of this run into the log you would then paste back,
# and this check starts the converge that carries your own credentials. So the
# trace is turned off here, BASH_XTRACEFD is unset so a trace redirected to a
# file this script knows nothing about cannot be reopened, and guard_xtrace
# refuses a shell that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS
# then, and every child would start tracing on its own).
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

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
# --help printed the usage itself: it is not a run, and the exit trap has to see
# this whatever path ended the run.
HELPED=no
DRY_RUN=""
USAGE="check-380-no-prompt-update.sh [--project <fugaro project>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something
# to paste back, and 2 says this run could not tell rather than that the claim
# does not hold.
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

# unmeasured: a row this check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the verdict ends the run as one that could not tell (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as an answer about T7 for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about T7"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 380 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 380 needs bash"
  need mktemp
  need grep
  need sed
  need head
  need tail
  need cut
  need awk "the lines a prompt left behind are joined into the row's own one line with it"
  need python3 "the doctor document and the run's transcript are read with it"
  need "$FUGARO_BIN" "doctor names the installation and update is the command this check measures"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk380-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK380_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk380.XXXXXXXX") || die "Check 380 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 380 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
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
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK380_KEEP_SCRATCH is set: $SCRATCH is kept (the run's transcript is in update.out and update.err)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing prints every line of this run into
# the log an owner pastes back, and this run starts the converge that carries
# their own credentials. A tracing shell is refused rather than trusted, the way
# every scripted check in this directory does it.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check starts the converge that carries your credentials and prints the block you would paste back. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: no trace of this run can copy the converge's output or a credential it reads"
}

# guard_agent_session: this check is the owner's, and the command it runs is the
# one a coding agent's session may not converge. Refusing here rather than
# letting the run refuse the agent's way is what makes the block say why: a
# session that ran this far would report an agent's refusal as a finding about
# the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 380 is yours to run at your own terminal, on the sandbox installation, and no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 380 is yours to run at your own terminal, on the sandbox installation, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — the run below is the run you would have typed"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

# guard_belong NAME WHERE: called twice, on what was typed and on what doctor
# answers, because the check may be pointed at the owner's own installation by
# either. Nothing is run before the typed one is refused.
guard_belong() { # name where
  if is_belong "$1"; then
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would converge the owner's own installation"
  fi
}

# ------------------------------------------------------------- the jsons
# The doctor document and the run's transcript, in one program: a live check on
# the owner's machine has python3 and no jq, and a row that read a transcript
# with grep would miss a prompt the day one is printed in another shape. Every
# query prints `key=value` lines or `\t`-separated rows, so the shell holds them
# without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import sys

# The shapes a prompt leaves in a transcript, all of them in lower case: the
# answer a read waits for ([y/N] in any casing, (y/n), (yes/no)), the typed
# confirmation's own line left open, an enter-prompt, and a question mark
# followed by a blank -- a question left waiting for an answer. This CLI's own
# text has none of them: its refusals are one sentence ending in a full stop and
# the banner it keeps ends at the question mark itself (askBanner prints no
# [y/N], which is why the banner is not a finding here).
NEEDLES = ("[y/n]", "(y/n)", "(yes/no)", "to confirm:", "press enter", "hit enter", "press return", "hit return")


def prompts(paths):
    bad = []
    for path in paths:
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                text = fh.read()
        except OSError:
            continue
        for line in text.splitlines():
            low = line.lower()
            if any(n in low for n in NEEDLES) or line.endswith("? "):
                bad.append(line)
    return bad


what = sys.argv[1]

if what == "doctor":
    import json
    try:
        with open(sys.argv[2], "r", encoding="utf-8") as fh:
            doc = json.load(fh)
    except (OSError, ValueError):
        sys.exit(1)
    project = doc.get("project") if isinstance(doc, dict) else None
    if not isinstance(project, dict):
        sys.exit(1)
    print("name=%s" % (project.get("name") or ""))
    print("gcp=%s" % (project.get("gcp_project") or ""))
elif what == "prompts":
    bad = prompts(sys.argv[2:])
    print("count=%d" % len(bad))
    for line in bad[:20]:
        print("prompt\t%s" % line.strip()[:240])
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# last_line FILE: the last non-blank line of a file, bounded, for a row a reader
# has to be able to scan. The CLI's own account of how its run ended is the last
# thing it printed, and for a run that stopped it is either the sentence that
# names what is left for you or cobra's own "Error:" line.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-300 || true
  fi
}

# ---------------------------------------------------------- the installation
judge_project() {
  write_reader
  local args=(doctor --json)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || status=$?
  local answer
  answer=$(python3 "$SCRATCH/read.py" doctor "$SCRATCH/doctor.json" 2>/dev/null) || answer=""
  PROJECT_NAME=$(printf '%s\n' "$answer" | sed -n 's/^name=//p' | head -1)
  PROJECT_GCP=$(printf '%s\n' "$answer" | sed -n 's/^gcp=//p' | head -1)
  if [ -z "$PROJECT_NAME" ]; then
    if [ -n "$PROJECT" ]; then
      PROJECT_NAME=$PROJECT
      fact project INFO "fugaro doctor --json did not answer about a project (exit $status: $(last_line "$SCRATCH/doctor.err")), so the installation this run is about is the one you typed, $PROJECT. The update below carries --project $PROJECT, so a config that is not that project's is refused by the CLI rather than read here"
      return 0
    fi
    fact project FAIL "fugaro doctor --json did not answer about a project (exit $status: $(last_line "$SCRATCH/doctor.err")), and no --project was typed, so this check cannot tell which installation it is about to converge. Run it where a project config is in force, or pass --project <name>"
    stop "fugaro doctor --json named no project and no --project was typed: this check cannot tell which installation it would converge"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    stop "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
  fi
  fact project PASS "the local config in force is project $PROJECT_NAME on GCP project $PROJECT_GCP (fugaro doctor --json): the update below is that installation's"
}

# ------------------------------------------------------------------ the run
# run_closed_stdin SECONDS KEY CMD...: run one command with its stdin on
# /dev/null and its two streams in the scratch directory, and end it at the
# deadline. The child execs, so the pid the deadline kills IS the command: a
# converge waiting on a terminal would otherwise outlive the check that gave up
# on it, which is the one thing the deadline exists to prevent. The ERR trap is
# dropped in it too, because this check reads the run's failing exit status as
# one of its own readings and a trap inherited here would turn it into "an
# internal error" before the row could report it.
RUN_STATUS=0
RUN_TIMED_OUT=no
RUN_SECONDS=0
run_closed_stdin() { # run_closed_stdin SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  RUN_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  ( trap - ERR; exec "$@" ) </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RUN_TIMED_OUT=yes
      say "the run is still going after ${secs}s: killing it (TERM, then KILL). A run waiting on an answer looks exactly like this"
      kill -TERM "$pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 1
    waited=$((waited + 1))
    if [ $((waited % 30)) -eq 0 ]; then
      say "still running after ${waited}s of ${secs}s ..."
    fi
  done
  RUN_STATUS=0
  wait "$pid" 2>/dev/null || RUN_STATUS=$?
  RUN_SECONDS=$((SECONDS - started))
}

step_update() {
  local args=(update)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  say "running: ${FUGARO_BIN} ${args[*]} < /dev/null  (deadline ${DEADLINE}s; this converges the installation, which is what update does)"
  run_closed_stdin "$DEADLINE" update "$FUGARO_BIN" "${args[@]}"
  UPDATE_STATUS=$RUN_STATUS
  UPDATE_TIMED_OUT=$RUN_TIMED_OUT
  UPDATE_SECONDS=$RUN_SECONDS
  UPDATE_LAST=$(last_line "$SCRATCH/update.err")
  UPDATE_LAST_FROM=stderr
  if [ -z "$UPDATE_LAST" ]; then
    UPDATE_LAST=$(last_line "$SCRATCH/update.out")
    UPDATE_LAST_FROM=stdout
  fi
  fact stdin PASS "the run's own streams: stdin was /dev/null (a read meets EOF at once and never a person's answer), stdout and stderr were files in $SCRATCH, and no pty was attached to any of them. A prompt site that refuses without a terminal therefore returns at once, and one that read and asked again would have hung — which is what the ${DEADLINE}s deadline measures"
  if [ "$UPDATE_TIMED_OUT" = yes ]; then
    fact update FAIL "fugaro update was still running after ${DEADLINE}s with its stdin on /dev/null, and this check killed it (TERM, then KILL): a run waiting on a terminal looks exactly like that. Its last line ($UPDATE_LAST_FROM) was: ${UPDATE_LAST:-<nothing>}. The whole transcript is in $SCRATCH/update.out and update.err (set CHECK380_KEEP_SCRATCH=1 to keep the directory)"
    return 0
  fi
  case "$UPDATE_STATUS" in
  0)
    fact update PASS "fugaro update ended by itself after ${UPDATE_SECONDS}s with exit 0: it converged what it had to do, or it had nothing to do. Its last line ($UPDATE_LAST_FROM): ${UPDATE_LAST:-<nothing>}"
    ;;
  1)
    fact update PASS "fugaro update ended by itself after ${UPDATE_SECONDS}s with exit 1 — a refusal, or a step left for you, which is the policy's own answer for a run that cannot confirm a step (T7): it asked nothing and it did not wait. Its last line ($UPDATE_LAST_FROM): ${UPDATE_LAST:-<nothing>}"
    ;;
  2)
    fact update FAIL "fugaro update ended after ${UPDATE_SECONDS}s with exit 2 — a cloud failure, so the converge did not finish and this run read little of its steps. Its last line ($UPDATE_LAST_FROM): ${UPDATE_LAST:-<nothing>}. Fix what the line names and run the check again"
    ;;
  *)
    fact update FAIL "fugaro update ended after ${UPDATE_SECONDS}s with exit $UPDATE_STATUS, which is not one of update's own codes (0 done, 1 a refusal or a step left for you, 2 a cloud failure). Its last line ($UPDATE_LAST_FROM): ${UPDATE_LAST:-<nothing>}"
    ;;
  esac
}

step_prompt() {
  if [ ! -s "$SCRATCH/update.out" ] && [ ! -s "$SCRATCH/update.err" ]; then
    unmeasured prompt "the run printed nothing at all (exit $UPDATE_STATUS after ${UPDATE_SECONDS}s), so there is no transcript to read for a question: an empty transcript is not evidence that the run asked nothing, and it is not evidence of a converge either. Its output files are in $SCRATCH/update.out and update.err (set CHECK380_KEEP_SCRATCH=1 to keep them), and what it printed is a finding of its own"
    PROMPT_COUNT=""
    return 0
  fi
  read_json "$SCRATCH/prompts.txt" prompts "$SCRATCH/update.out" "$SCRATCH/update.err"
  PROMPT_COUNT=$(field "$SCRATCH/prompts.txt" count)
  local listed
  listed=$(awk -F'\t' '$1 == "prompt" { print $2 }' "$SCRATCH/prompts.txt" 2>/dev/null | head -3 || true)
  PROMPT_LIST=$(printf '%s\n' "$listed" | awk 'NF { printf "%s%s", (n++ ? "; " : ""), $0 }')
  if [ -n "$PROMPT_LIST" ]; then
    fact prompt FAIL "${PROMPT_COUNT:-some} line(s) of the run's own output ask for an answer: ${PROMPT_LIST}. The policy's rule is that no command waits on stdin and no question is put to a person who is not there (T7); the whole transcript is in $SCRATCH/update.out and update.err (set CHECK380_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  fact prompt PASS "none of the run's output asks for an answer: no [y/N] in any casing, no (y/n) or (yes/no), no 'to confirm:' left open, no enter-prompt and no question left waiting on a blank (a line ending in '? '). The ⚠ CONFIRM banners the steps print are notes and not reads (askBanner prints no [y/N]), so they are not findings here"
}

# ------------------------------------------------------------------- teardown
report_teardown() {
  fact teardown PASS "this check wrote nothing outside its scratch directory, and it rolled nothing back: the run it started IS fugaro update, so converging this installation (or leaving a step for you) is what you asked for. The scratch directory (mode 0700, holding the transcript) is removed at the end; your installation, your project and your credentials are otherwise untouched"
}

# ------------------------------------------------------------------- the verdict
the_verdict() {
  if [ "$UPDATE_TIMED_OUT" = yes ]; then
    fact noprompt FAIL "T7 does NOT hold on this installation as measured: fugaro update with its stdin on /dev/null was still running after ${DEADLINE}s and had to be killed. Nothing about a converge justifies waiting that long on a closed stdin, and a run waiting for somebody to type is the failure this check exists to catch"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$PROMPT_LIST" ]; then
    fact noprompt FAIL "T7 does NOT hold on this installation: the run ended (exit $UPDATE_STATUS after ${UPDATE_SECONDS}s) but it asked: $PROMPT_LIST. A command that puts a question to a run with no terminal is the one prompt the policy removes (D1540)"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    fact noprompt INFO "T7 is NOT MEASURED by this run: the row(s) ${UNMEASURED_KEYS// /, } above could not be read, so nothing here is an answer about whether fugaro update asks. Settle what the row names and run the check again"
    REACHED_END=yes
    exit 2
  fi
  fact noprompt PASS "T7 HOLDS on this installation: fugaro update with its stdin on /dev/null ended by itself after ${UPDATE_SECONDS}s (exit $UPDATE_STATUS) and asked nothing — nothing waited, and no line asked for an answer. What the exit status means is the update row's: 0 is a converge, 1 is a refusal or a step left for you (the policy's answer for a run that cannot confirm a step, not a prompt), and the run's own last line is quoted there"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(xtrace agent-session project belong stdin update prompt noprompt teardown)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 380 RESULT\n' >&3
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
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    printf 'FACT scratch-removed: FAIL — the scratch directory is STILL THERE at %s -- remove it yourself\n' "$SCRATCH" >&3
  else
    printf 'FACT scratch-removed: PASS — the scratch directory is gone, and nothing of this check is left behind\n' >&3
  fi
  printf 'CHECK 380 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see, and a block that then
  # read as a finished run would offer the rows that read "not reached" as an
  # answer about T7 when the read that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about T7"
    fi
    status=2
  fi
  # The facts decide between "it does not hold" and "this run could not tell",
  # for the paths that record a row without reaching the verdict.
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) ;; *) failed=yes ;; esac
      ;;
    esac
  done <<<"$facts"
  if [ "$status" = 0 ]; then
    if [ "$failed" = yes ]; then
      status=1
    elif [ -n "$UNMEASURED_KEYS" ]; then
      status=2
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

# -------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads] ${FUGARO_BIN} doctor --json$( [ ${#FUGARO_FLAGS[@]} -gt 0 ] && printf ' %s' "${FUGARO_FLAGS[*]}" )$( [ -n "$PROJECT" ] && printf ' --project %s' "$PROJECT" ): which installation the config in force names, held to --project where you typed one, with a project named belong refused here and in what doctor answers"
  say "  [runs] ${FUGARO_BIN} update$( [ ${#FUGARO_FLAGS[@]} -gt 0 ] && printf ' %s' "${FUGARO_FLAGS[*]}" )$( [ -n "$PROJECT" ] && printf ' --project %s' "$PROJECT" ) with its stdin on /dev/null, its stdout and stderr in the scratch directory, under a ${DEADLINE}s deadline (TERM, then KILL): THE RUN THIS CHECK MEASURES. It converges this installation, or refuses to and leaves the step for you, exactly as it would if you typed it"
  say "  [reads the transcript] every line the run printed: a [y/N] in any casing, a (y/n) or (yes/no), a 'to confirm:' left open, an enter-prompt, or a question left waiting on a blank are what a prompt leaves behind. The ⚠ CONFIRM banners the steps note themselves under are not findings"
  say "  [writes] nothing of its own: one scratch directory (mode 0700) holding the transcript, removed at the end"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK380_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK380_DRY_RUN:-}
  DEADLINE=${CHECK380_DEADLINE:-1800}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a Fugaro project name. usage: $USAGE"
      PROJECT=$2
      shift 2
      ;;
    --fugaro-flags)
      [ $# -ge 2 ] || die "--fugaro-flags needs a string of flags. usage: $USAGE"
      read -r -a FUGARO_FLAGS <<<"$2"
      shift 2
      ;;
    --deadline)
      [ $# -ge 2 ] || die "--deadline needs a number of seconds. usage: $USAGE"
      DEADLINE=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      printf '\nRuns %s update with its stdin closed and reads what came back: the no-prompt\npolicy'"'"'s rule that no command waits on stdin (T7). It prints one block headed\nCHECK 380 RESULT and exits 0 when the run ended by itself and asked nothing, 1\nwhen it did not, and 2 when this run could not tell. It is yours to run: no agent\nruns any step of it.\n' "$FUGARO_BIN"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  case "$DEADLINE" in
  '' | *[!0-9]*) die "--deadline must be a whole number of seconds (got '$DEADLINE'). usage: $USAGE" ;;
  esac
  if [ "$DEADLINE" -lt 5 ]; then
    die "--deadline $DEADLINE is too short to measure anything: give it at least 5 seconds. usage: $USAGE"
  fi
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  guard_agent_session
  if [ -n "$DRY_RUN" ]; then
    # A dry run reads nothing and runs nothing: the project guard is judged on
    # what was TYPED (a string test, no filesystem and no call), and the reads
    # and the run are the plan's business.
    if [ -n "$PROJECT" ]; then
      guard_belong "$PROJECT" "the project you typed"
      fact belong PASS "the project name you typed ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
    else
      fact belong INFO "no --project was typed, so the guard had nothing to judge: a real run refuses an installation named belong in what doctor answers too, before anything is run"
    fi
    fact project INFO "a dry run reads nothing: a real run asks fugaro doctor --json which installation the config in force is$( [ -n "$PROJECT" ] && printf ', holds it to the --project %s you typed,' ), and refuses an installation named belong in that answer too"
    fact stdin INFO "planned: a real run starts fugaro update with its stdin on /dev/null and its two streams in the scratch directory, so no pty is attached to any of them"
    fact update INFO "planned: a real run runs fugaro update under a ${DEADLINE}s deadline and reports its exit status and its own last line; a run still going at the deadline is killed and the row FAILs"
    fact prompt INFO "planned: a real run reads every line the run printed for the shapes a prompt leaves behind (a [y/N], a 'to confirm:' left open, a question waiting on a blank); a dry run reads nothing"
    fact noprompt INFO "planned: the verdict is a real run's, and it is the run's own transcript that answers it"
    fact teardown INFO "planned: a real run writes nothing outside the scratch directory, which it removes at the end"
    fact dry-run PASS "the plan was printed and nothing was run: no doctor, no update, no read of any transcript, and nothing was written outside the scratch directory"
    BLOCK_KEYS=(xtrace agent-session belong project stdin update prompt noprompt teardown)
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  step_update
  step_prompt
  report_teardown
  the_verdict
}

FUGARO_BIN=${CHECK380_FUGARO:-fugaro}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
DEADLINE=1800
UPDATE_STATUS=0
UPDATE_TIMED_OUT=no
UPDATE_SECONDS=0
UPDATE_LAST=""
UPDATE_LAST_FROM=stderr
PROMPT_COUNT=""
PROMPT_LIST=""

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
