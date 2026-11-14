#!/usr/bin/env bash
# Check 310 — the CLI's output at a real terminal, as ONE script YOU run.
#
# docs/cli-output-console-graph/implementation-plan-output.md (CL5b, CL9, CL10)
# replaced what `fugaro update` prints with an account: one line per step and a
# closing line, colour and a line in flight where a person is watching, nothing
# of either where nobody is, the run's full output in a log, and a failure that
# prints its own context and the log's path. Its guarantees are G1 to G11 of
# that plan, and this check is the only place they are read off a real terminal:
#
#   G1  a step in flight never hides a step's result, and the result of every
#       step is printed;
#   G2  a --json run's stdout is one JSON document and nothing else;
#   G3  the closing line names the outcome (done, left for you, failed) and the
#       exit status is 0, 1 or 2 to match;
#   G4  no colour, no carriage return and no escape sequence where stdout is
#       not a terminal, with NO_COLOR set to anything, or with TERM=dumb — and
#       no colour in --json either;
#   G5  a log is 0600 inside a 0700 directory, and a log that cannot be written
#       never fails the command;
#   G6  retention is 14 days or the newest 50 per command name, and never
#       another command's file, a link or a directory;
#   G7  a read writes no log at all (`fugaro doctor --json` below);
#   G8  a failed step prints its name, the error, the last lines of context on
#       the screen and the log's path;
#   G9  `doctor`'s default view is unchanged (that one is not read here: it is
#       a screen, and this check does not compare screens);
#   G10 `fugaro -v` alone is the version and `fugaro update -v` is verbose
#       (not read here either: the plan's CL12 table names the five commands
#       below and no other);
#   G11 an agent session sees the same default output (not read here: this
#       check refuses an agent's session outright).
#
# What it runs, in this order, against the installation you name:
#
#   1. `fugaro version` and `fugaro doctor --json` — the binary and the
#      installation in force, and the one read that must file no log (G7).
#   2. `fugaro update` on a real terminal — colour, the line in flight, one
#      line per step, the closing line and the exit status (G1, G3, G4).
#   3. `fugaro setup --plan-only` on a real terminal — the plan view: a header,
#      one row per stage, no closing line, and the screen's rows in the log,
#      whole (CL5a).
#   4. `NO_COLOR=1 fugaro update` on a real terminal — no escape sequence and
#      no carriage return, with the steps still printed (G1, G4).
#   5. `fugaro update | cat` — stdout a pipe: no escape sequence, no carriage
#      return, the steps still printed (G1, G4).
#   6. `fugaro update --json` on a real terminal — stdout one document with no
#      escape sequence, the account on stderr, and the document's own outcome
#      matching the exit status (G2, G3, G4).
#   7. `fugaro update` with a fake `terraform` first on PATH — a forced failure
#      inside the converge: the step line, the context under it, the log's path
#      and `fugaro troubleshoot`'s line (G8), the log's mode (G5), and the log
#      holding what the screen did not show.
#   8. retention: 54 seeded logs stand in for 51 runs (retention reads the
#      directory, never a history), and the failure run's own open is what
#      prunes them (G6). The logs of YOURS the rule removes on the way are
#      copied aside before step 2, since the runs of steps 2 to 7 each prune the
#      directory as they open their own log.
#   9. `ls -l` of the log directory, printed for you.
#
# It creates nothing, deletes nothing and writes nothing outside the log
# directory and its own scratch directory: the runs are ordinary `fugaro
# update`s of an installation that is already converged (so they apply nothing
# on a converged machine), the seeds are this check's own and are removed
# again, and any of YOUR logs the retention rule removes while this check runs
# are copied aside before its first fugaro call and put back byte for byte in
# the teardown (the rule would remove them on your next update anyway, and the
# row names them).
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 310".
#
# Usage:      bash scripts/live-checks/check310.sh [--installation <name>] [--dry-run]
#             --installation is the installation whose `fugaro update` this
#             check drives; without it, the installation in force is read from
#             `fugaro doctor --json`. It is never `belong` (the register's own
#             installation), and the terminal readings do not depend on which
#             installation it is — run it against any other one you have.
#             `--dry-run` prints the commands a real run would make and runs
#             nothing at all: no `fugaro` call, no seed, no write.
# Needs:      bash, mktemp, sed, python3 (the terminal, the captures and the
#             retention rule are read by one python3 file) and `fugaro` on
#             PATH. Nothing is installed and nothing is fetched; if one of
#             those is missing the script stops with one line naming it.
# Where:      your own machine, in your own terminal, and NOT from inside a
#             Fugaro checkout whose `fugaro.yaml` names another project (the
#             CLI refuses that pairing itself; the row would say so). The
#             installation is refused by name when it is `belong`.
# Costs:      four `fugaro update` runs and one `fugaro setup --plan-only`, all
#             of them re-converges of an installation that exists: on a
#             converged one each applies nothing and costs nothing billable.
#             The forced-failure run writes nothing at all — its `terraform` is
#             a fake that answers the version question and refuses the rest.
#             No model call is made by any step of this check.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 310 RESULT.
#             Exit 0 when every fact below holds, 1 when one of them does not
#             (the CLI's output drifted from G1 to G8), and 2 when the run
#             could not tell (a guard stopped it, a command never printed the
#             account this check reads, a tool is missing, an internal error
#             ended it).
#
# Every `fugaro` call this check makes gets a real terminal on stdout (a pty of
# the check's own) and its standard input is /dev/null: a converge that reaches
# a question it would ask a person reads end-of-file instead of blocking, and
# the step it cannot confirm is reported as one left for you — which is itself a
# reading of the closing line and the exit status, and not a failure of this
# check. Standard error is kept in a file of its own, so the two streams are
# judged apart.
#
# CHECK310_RUN_TIMEOUT bounds each `fugaro` call in seconds (default 1800, 0
# for none): a converge that hangs must not hang the check that watches it.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had. The captures below hold
# the whole of four `fugaro update`s, and a trace of the machinery around them
# is noise in the one thing the owner pastes back — so the trace is turned off
# here, BASH_XTRACEFD is unset so a trace redirected to a file this script knows
# nothing about cannot be reopened, and guard_xtrace refuses a shell that
# arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then, and every child
# would start tracing on its own). Check 145 spells the three parts out; this is
# the same guard.
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
# Set to yes once the readings have been judged, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and it is read by the exit
# trap, which has to see both of these whatever path ended the run.
HELPED=no
DRY_RUN=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something
# to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
# fact KEY PASS|FAIL|INFO "detail": one row of the block. The separator is a
# pipe and a detail a command printed may hold one, so both it and any newline
# are replaced: the block is read line by line, and a row split in two would be
# a row nobody could read.
fact() {
  [ -n "$FACTS_FILE" ] || return 0
  local detail=${3//|//}
  detail=${detail//$'\n'/ }
  printf '%s|%s|%s\n' "$1" "$2" "$detail" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as an answer about the CLI's output for a
# run that measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the CLI's output"
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
  need python3 "the terminal each command runs on, the captures it leaves and the retention rule are one python3 file"
  need fugaro "every reading below is a fugaro command of yours"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk310-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# The reader and the terminal, written into the scratch directory when the run
# reaches them.
READER=""
PTY=""
# The seeds and the guard entries this run put in the log directory, one name to
# a line, and the logs of the owner's own that the rule removed and this run put
# back. All three are read by the teardown.
SEEDS_FILE=""
GUARDS_FILE=""
RESTORED_FILE=""
# The log directory, and the log the failure run wrote in it.
LOGDIR=""
FAIL_LOG=""
# What the reader made of the last capture it was given.
READ=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk310.XXXXXXXX") || die "Check 310 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  SEEDS_FILE="$SCRATCH/seeds"
  GUARDS_FILE="$SCRATCH/guards"
  RESTORED_FILE="$SCRATCH/restored"
  : >"$SEEDS_FILE"
  : >"$GUARDS_FILE"
  : >"$RESTORED_FILE"
  READER="$SCRATCH/reader310.py"
  PTY="$SCRATCH/pty310.py"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 310 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: unseed records what it
# managed to do, and a `$(cleanup)` would throw those answers away with the
# subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  unseed
  remove_scratch
}

# unseed takes this run's own files out of the log directory and puts back the
# owner's own logs the retention rule removed while they were there. It is
# guarded from end to end because it runs from the exit trap, where a failing
# `rm` under `set -e` would end the shell before the block is printed.
unseed() {
  unseed_mine
  restore_owner_logs
}

unseed_mine() {
  [ -n "$LOGDIR" ] || return 0
  [ -d "$LOGDIR" ] || return 0
  local name
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    case "$name" in
    */* | . | ..) continue ;;
    esac
    if [ -d "$LOGDIR/$name" ] && [ ! -L "$LOGDIR/$name" ]; then
      rmdir "$LOGDIR/$name" 2>/dev/null || true
    else
      rm -f "$LOGDIR/$name" 2>/dev/null || true
    fi
  done <"$GUARDS_FILE"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    case "$name" in
    */* | . | ..) continue ;;
    esac
    rm -f "$LOGDIR/$name" 2>/dev/null || true
  done <"$SEEDS_FILE"
  return 0
}

# restore_owner_logs puts back, byte for byte and mode 0600, every log of the
# owner's own that the rule removed while this check ran. The list was computed
# from the rule itself, over the directory as it stood before the first fugaro
# call (plan_owner_logs), so nothing of theirs is lost to a reading that only
# needed the directory to look full.
restore_owner_logs() {
  [ -n "$LOGDIR" ] || return 0
  [ -d "$LOGDIR" ] || return 0
  [ -f "$SCRATCH/removed-expected" ] || return 0
  local name backup
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    case "$name" in
    */* | . | ..) continue ;;
    esac
    backup="$SCRATCH/backup/$name"
    if [ -f "$backup" ] && [ ! -e "$LOGDIR/$name" ]; then
      cp "$backup" "$LOGDIR/$name" 2>/dev/null || true
      chmod 600 "$LOGDIR/$name" 2>/dev/null || true
      printf '%s\n' "$name" >>"$RESTORED_FILE"
    fi
  done <"$SCRATCH/removed-expected"
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
# guard_xtrace: a traced run of four converges is not the block anybody can read,
# so the run is refused rather than read: the trace is already off (the top of
# this file), and what is left to catch is the shell that started with it, whose
# flag sits in SHELLOPTS/BASHOPTS and would be inherited by every child.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would trace every fugaro command this check runs and the block you would then paste back. Run it without -x"
    stop "the shell this check was started from is tracing (-x). Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the four converges below"
}

# guard_agent mirrors internal/agentenv.Session, which is the same door the CLI's
# own commands use: a coding agent's session is refused, and the IDE marker is
# refused only where no person is at the terminal. It is not a formality here —
# a converge in an agent's session changes nothing outside the machine, so the
# readings below would be of a run that is not the one this check is about.
guard_agent() {
  local name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      fact agent FAIL "this session carries the coding agent's marker $name, so this is an agent's session and not yours: Check 310 drives four converges and reads what they print at a person's terminal, and no agent runs any step of it"
      stop "this is a coding agent's session ($name is set). Check 310 is OWNER-RUN: run it in your own terminal"
    fi
  done
  if [ -n "$(printenv CLAUDE_CODE_SSE_PORT 2>/dev/null || true)" ] && [ ! -t 0 ]; then
    fact agent FAIL "this session carries CLAUDE_CODE_SSE_PORT and has no terminal: that is an IDE session's shape, and Check 310 is owner-run"
    stop "this looks like an IDE session (CLAUDE_CODE_SSE_PORT is set and there is no terminal). Run Check 310 in your own terminal"
  fi
  fact agent PASS "this is not a coding agent's session (no agentenv marker is set), so the four converges below are the ones a person runs and not a session's own"
}

# guard_belong: `belong` is the installation the register refuses by name, and
# this check runs `fugaro update` against whatever is selected. The terminal
# readings do not depend on which installation it is, so any other one will do —
# and pointing this at the owner's own installation by accident is what the
# refusal is for. GCP may be empty: the name alone is checked before the first
# `fugaro` call, and the GCP project is added once doctor has read it.
guard_belong() {
  if grep -q '^belong|' "$FACTS_FILE" 2>/dev/null; then return 0; fi
  case "$INSTALLATION" in
  belong | belong-* | Belong | BELONG)
    fact belong FAIL "the installation is $INSTALLATION, which the register refuses by name: Check 310 re-converges it four times and reads the log directory it writes, and it is never pointed at belong. Name another installation (--installation <name>): the terminal readings do not depend on which one it is"
    stop "Check 310 never runs against 'belong'. Name another installation: --installation <name>"
    ;;
  esac
  case "$(printf '%s' "$GCP" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact belong FAIL "the GCP project of $INSTALLATION is $GCP, which contains the name 'belong': Check 310 is never pointed at the owner's own installation. Name another one (--installation <name>)"
    stop "the GCP project '$GCP' matches the installation 'belong', which this check must never touch"
    ;;
  esac
  if [ -n "$INSTALLATION" ]; then
    fact belong PASS "the installation ($INSTALLATION${GCP:+, GCP project $GCP}) is not the owner's own installation (nothing typed or read contains 'belong')"
  else
    fact belong INFO "not read: no installation is named yet (a real run reads it from fugaro doctor --json and refuses it here when it is 'belong')"
  fi
}

# ------------------------------------------------------------- the two helpers
# write_pty: the terminal every `fugaro` call below runs on. The child's stdout
# is a real pty, so the CLI's own "is this a terminal" test sees a terminal and
# draws what it draws there; every byte of it — carriage returns, escape
# sequences, the line in flight — is written to the capture unread, which is
# what makes the colour and the spinner readable afterwards. Standard input is
# /dev/null and standard error is a file of its own, so a question nobody can
# answer never blocks the check and the two streams are judged apart.
write_pty() {
  cat >"$PTY" <<'PTY_EOF'
#!/usr/bin/env python3
"""Run a command with a real terminal on stdout and its stderr in a file.

usage: pty310.py CAPTURE ERRFILE -- CMD [ARG...]

The child's standard input is /dev/null: a converge that reaches a question it
would ask a person reads end-of-file instead of blocking, and the step it
cannot confirm is reported as one left for you, which is a reading of its own.
The exit status is the child's own (128 plus the signal where it was killed),
so the caller can compare it with the closing line the run printed.
"""
import fcntl
import os
import pty
import struct
import sys
import termios

capture, errfile = sys.argv[1], sys.argv[2]
rest = sys.argv[3:]
if rest and rest[0] == "--":
    rest = rest[1:]
if not rest:
    sys.stderr.write("pty310.py: no command\n")
    sys.exit(2)

master, slave = pty.openpty()
try:
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
except OSError:
    pass
pid = os.fork()
if pid == 0:
    os.close(master)
    os.setsid()
    try:
        os.dup2(slave, 1)
        devnull = os.open(os.devnull, os.O_RDONLY)
        os.dup2(devnull, 0)
        fe = os.open(errfile, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        os.dup2(fe, 2)
    except OSError:
        os._exit(3)
    os.execvp(rest[0], rest)
    os._exit(127)
os.close(slave)
with open(capture, "wb") as out:
    while True:
        try:
            data = os.read(master, 65536)
        except OSError:
            break
        if not data:
            break
        out.write(data)
os.close(master)
_, status = os.waitpid(pid, 0)
if os.WIFEXITED(status):
    sys.exit(os.WEXITSTATUS(status))
sys.exit(128 + os.WTERMSIG(status))
PTY_EOF
}

# write_reader: the one python3 file that reads a capture, a JSON document, a
# log's mode and the retention rule. It prints one `key=value` line per question
# and decides nothing: the shell judges what it printed, so the parsing and the
# verdict stay in one place each.
write_reader() {
  cat >"$READER" <<'READER_EOF'
#!/usr/bin/env python3
"""Read a capture, a document, a mode or a directory, and print what it says.

One `key=value` line per question. Nothing here decides anything.
"""
import calendar
import json
import os
import re
import stat
import sys
import time

# The alphabet a step line starts with (internal/report/style.go): the symbols
# and their ASCII fallbacks. A plan view's row is indented two spaces and a
# line under a step four, so neither can be mistaken for one of these.
STEP_GLYPHS = "✔○–↳✖!+o>x-"
WAIT_GLYPHS = "↳>"
CSI = re.compile(rb"\x1b\[[0-9;?]*[ -/]*[@-~]")
# A frame of the line in flight is a carriage return, the painted frame and a
# space (internal/report/spinner.go drawRunning). The clear sequence stopSpinner
# writes is a carriage return and `ESC [ K`, which is what keeps a step's own
# line from being counted as a frame.
FRAME = re.compile(rb"\r(?:\x1b\[[0-9;]*m)*[|/\\-](?:\x1b\[[0-9;]*m)* ")
LOG_NAME = re.compile(r"^([a-z][a-z0-9-]{0,40})-(\d{14})-(\d+)\.log$")
RETENTION = 14 * 24 * 3600
KEEP_PER_COMMAND = 50


def strip_ansi(raw):
    return CSI.sub(b"", raw).decode("utf-8", "replace")


def real_lines(text):
    """The lines a person read, without the frames drawn over them.

    The terminal writes a carriage return before every redraw, so everything in
    one newline-delimited chunk before its last carriage return is a frame of
    the line in flight, and what follows it is the line itself.
    """
    out = []
    for chunk in text.split("\n"):
        # The terminal's own ONLCR writes a carriage return before every newline
        # the child prints, so a chunk that ends with one ends with the
        # terminal's and not with the spinner's: it goes before the frames are
        # peeled off, or every line of a pty capture would read as empty.
        if chunk.endswith("\r"):
            chunk = chunk[:-1]
        if "\r" in chunk:
            chunk = chunk.rsplit("\r", 1)[1]
        chunk = chunk.rstrip()
        if chunk != "":
            out.append(chunk)
    return out


def lines_cmd(path):
    raw = open(path, "rb").read()
    text = strip_ansi(raw)
    fields = {
        "escapes": raw.count(b"\x1b"),
        "barecr": len(re.findall(rb"\r(?!\n)", raw)),
        "frames": len(FRAME.findall(raw)),
        "clears": raw.count(b"\r\x1b[K"),
    }
    steps = waits = plan_rows = context = 0
    closing = header = full_output = diagnose = odd = last = ""
    for line in real_lines(text):
        last = line
        if re.match(r"^(Done|Left for you|Failed) in ", line):
            closing = line
        elif re.match(r"^fugaro (setup|update): project ", line):
            header = line
        elif re.match(r"^ {4}full output: ", line):
            full_output = line[4 + len("full output: "):]
        elif re.match(r"^ {4}Something went wrong\. Run: fugaro troubleshoot ", line):
            diagnose = line.split("troubleshoot ", 1)[1]
        elif re.match(r"^ {4,}\S", line):
            # The indent is four spaces and the line under it keeps its own, so
            # a context line an error already indented reads as six (report.go
            # writes "    " + the line).
            context += 1
        elif re.match(r"^ {2}\S", line):
            plan_rows += 1
        elif len(line) > 1 and line[0] in STEP_GLYPHS and line[1] == " ":
            if line[0] in WAIT_GLYPHS:
                waits += 1
            else:
                steps += 1
        elif odd == "":
            odd = line[:200]
    kind = ""
    if closing.startswith("Done in "):
        kind = "done"
    elif closing.startswith("Left for you in "):
        kind = "left"
    elif closing.startswith("Failed in "):
        kind = "failed"
    count = ""
    m = re.search(r"(\d+) steps?$", closing)
    if m:
        count = m.group(1)
    fields.update({
        "lines": len(real_lines(text)),
        "steps": steps,
        "waits": waits,
        "plan_rows": plan_rows,
        "context": context,
        "closing": closing,
        "closing_kind": kind,
        "closing_count": count,
        "header": header,
        "full_output": full_output,
        "diagnose": diagnose,
        "odd": odd,
        "last": last,
    })
    for key in ("closing", "header", "full_output", "diagnose", "odd", "last"):
        fields[key] = fields[key].replace("\n", " ")
    print_lines(fields)


def doc_cmd(path):
    raw = open(path, "rb").read()
    fields = {"ok": "0", "why": "", "failed": "", "left": "", "stages": "",
              "error": "", "project": "", "plan_only": "",
              "escapes": raw.count(b"\x1b"), "barecr": len(re.findall(rb"\r(?!\n)", raw))}
    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
    except Exception as exc:  # noqa: BLE001 - the reader reports, it never raises
        fields["why"] = "%s" % (exc,)
        print_lines(fields)
        return
    if not isinstance(doc, dict):
        fields["why"] = "the document is a %s, not an object" % type(doc).__name__
        print_lines(fields)
        return
    fields["ok"] = "1"
    fields["project"] = str(doc.get("project") or "")
    fields["plan_only"] = "1" if doc.get("plan_only") else "0"
    failed = doc.get("failed")
    if isinstance(failed, dict):
        fields["failed"] = str(failed.get("stage") or "?")
    left = doc.get("left_for_you")
    fields["left"] = str(len(left)) if isinstance(left, list) else ""
    stages = doc.get("stages")
    fields["stages"] = str(len(stages)) if isinstance(stages, list) else ""
    fields["error"] = "1" if doc.get("error") else "0"
    print_lines(fields)


def doctor_cmd(path, key):
    try:
        doc = json.load(open(path))
    except Exception:  # noqa: BLE001
        print_lines({key: ""})
        return
    proj = doc.get("project") if isinstance(doc, dict) else None
    value = proj.get(key) if isinstance(proj, dict) else ""
    print_lines({key: value or ""})


def mode_cmd(path, dirpath):
    fields = {"file_mode": "", "dir_mode": "", "file_owner": "", "dir_owner": "", "file_size": ""}
    try:
        st = os.lstat(path)
        fields["file_mode"] = "%o" % (stat.S_IMODE(st.st_mode),)
        fields["file_owner"] = "yes" if st.st_uid == os.geteuid() else "no"
        fields["file_size"] = str(st.st_size)
    except OSError:
        pass
    try:
        st = os.lstat(dirpath)
        fields["dir_mode"] = "%o" % (stat.S_IMODE(st.st_mode),)
        fields["dir_owner"] = "yes" if st.st_uid == os.geteuid() else "no"
    except OSError:
        pass
    print_lines(fields)


def snapshot(dirpath):
    """The directory as `<kind> <name>` lines: what the rule can see."""
    out = []
    try:
        names = sorted(os.listdir(dirpath))
    except OSError:
        return out
    for name in names:
        try:
            st = os.lstat(os.path.join(dirpath, name))
        except OSError:
            continue
        if stat.S_ISLNK(st.st_mode):
            kind = "link"
        elif stat.S_ISDIR(st.st_mode):
            kind = "dir"
        elif stat.S_ISREG(st.st_mode):
            kind = "file"
        else:
            kind = "other"
        out.append("%s %s" % (kind, name))
    return out


def read_snapshot(path):
    entries = []
    try:
        with open(path) as fh:
            for line in fh:
                line = line.rstrip("\n")
                if not line:
                    continue
                kind, _, name = line.partition(" ")
                entries.append((kind, name))
    except OSError:
        pass
    return entries


def ls_cmd(dirpath):
    for entry in snapshot(dirpath):
        sys.stdout.write(entry + "\n")


def candidates(entries, command):
    """The names the rule considers: `<command>-<14 digits>-<pid>.log`, a
    regular file, and nothing else (pruneLogs)."""
    out = []
    for kind, name in entries:
        if kind != "file":
            continue
        m = LOG_NAME.match(name)
        if not m or m.group(1) != command:
            continue
        out.append((name, m.group(2)))
    return out


def stamp_at(stamp):
    """A log name's own time, read as UTC, which is what the CLI writes there."""
    return calendar.timegm(time.strptime(stamp, "%Y%m%d%H%M%S"))


def retention_survivors(entries, command, keep, now):
    """The rule of internal/report/logsink.go pruneLogs, written again here.

    The log just created counts as the newest whatever its name says, the others
    sort by time then name, and a log is kept when it is inside the newest 50 AND
    no older than 14 days.
    """
    mine = candidates(entries, command)
    keep_first = [e for e in mine if e[0] == keep]
    rest = [e for e in mine if e[0] != keep]
    rest.sort(key=lambda e: (-stamp_at(e[1]), e[0]))
    ordered = keep_first + rest
    survivors, removed = [], []
    for i, (name, stamp) in enumerate(ordered):
        if i < KEEP_PER_COMMAND and (now - stamp_at(stamp)) <= RETENTION:
            survivors.append(name)
        else:
            removed.append(name)
    return survivors, removed


def seed_cmd(new, old):
    """The names this check seeds: the shape the rule reads back."""
    now = int(time.time())
    for i in range(int(new)):
        stamp = time.strftime("%Y%m%d%H%M%S", time.gmtime(now - 12 * 24 * 3600 + i))
        sys.stdout.write("update-%s-%d.log\n" % (stamp, 700000 + i))
    for i in range(int(old)):
        stamp = time.strftime("%Y%m%d%H%M%S", time.gmtime(now - (15 + i) * 24 * 3600))
        sys.stdout.write("update-%s-%d.log\n" % (stamp, 800000 + i))


def judge_cmd(before, after, keep, command, guards):
    """What the rule did, against what it says it does."""
    now = time.time()
    before_entries = read_snapshot(before)
    after_entries = read_snapshot(after)
    with_keep = before_entries + [("file", keep)]
    survivors, removed = retention_survivors(with_keep, command, keep, now)
    after_names = {name for _, name in after_entries}
    after_candidates = {name for name, _ in candidates(after_entries, command)}
    survivor_set, removed_set = set(survivors), set(removed)
    wrong_kept = sorted(name for name in removed_set if name in after_candidates)
    wrong_gone = sorted(name for name in survivor_set if name not in after_candidates)
    guard_names = [name for name in guards.split(",") if name]
    guards_missing = sorted(name for name in guard_names if name not in after_names)
    print_lines({
        "candidates": len(with_keep),
        "survivors": len(survivors),
        "removed": len(removed),
        "wrong_kept": ",".join(wrong_kept[:6]),
        "wrong_kept_count": len(wrong_kept),
        "wrong_gone": ",".join(wrong_gone[:6]),
        "wrong_gone_count": len(wrong_gone),
        "guards_missing": ",".join(guards_missing[:6]),
        "guards_missing_count": len(guards_missing),
        "keep_present": "1" if keep in after_names else "0",
    })


def plan_cmd(before, command, new_logs, out_path):
    """The names the rule will remove from this directory, so the owner's own
    can be copied aside and put back.

    The logs the runs being planned are about to file are part of the reading:
    the rule keeps the newest 50 of a command's logs, so a new one is what
    pushes the oldest out, and the log a run is writing is the newest of them
    whatever its name says (pruneLogs' keep) — which is why the newest of the
    planned logs is handed to retention_survivors as the keep.
    """
    entries = read_snapshot(before)
    now = time.time()
    stamp = time.strftime("%Y%m%d%H%M%S", time.gmtime(now))
    added = [("file", "%s-%s-%d.log" % (command, stamp, 1 + i)) for i in range(int(new_logs))]
    keep = added[-1][0] if added else ""
    survivors, removed = retention_survivors(entries + added, command, keep, now)
    with open(out_path, "w") as fh:
        for name in removed:
            fh.write(name + "\n")
    print_lines({"removed": len(removed), "survivors": len(survivors)})


def new_cmd(before, after, command):
    """The log the run filed: a name that was not there before, its own
    command's where there is one."""
    before_names = {name for _, name in read_snapshot(before)}
    after_entries = read_snapshot(after)
    found = ""
    for name, _ in candidates(after_entries, command):
        if name not in before_names:
            found = name
            break
    if not found:
        for kind, name in after_entries:
            if kind == "file" and name not in before_names:
                found = name
                break
    print_lines({"new": found})


def restore_cmd(original, final, mine):
    """Whether the directory holds what it held before this run touched it."""
    original_names = [name for _, name in read_snapshot(original)]
    final_names = {name for _, name in read_snapshot(final)}
    missing = sorted(name for name in original_names if name not in final_names)
    mine_names = [name for name in mine.split(",") if name]
    left = sorted(name for name in mine_names if name in final_names)
    print_lines({
        "missing": ",".join(missing[:6]),
        "missing_count": len(missing),
        "left": ",".join(left[:6]),
        "left_count": len(left),
    })


def print_lines(fields):
    for key in sorted(fields):
        value = str(fields[key]).replace("\n", " ")
        sys.stdout.write("%s=%s\n" % (key, value))


def main(argv):
    if len(argv) < 2:
        sys.stderr.write("reader310.py: a subcommand is required\n")
        return 2
    cmd, rest = argv[1], argv[2:]
    if cmd == "lines":
        lines_cmd(rest[0])
    elif cmd == "doc":
        doc_cmd(rest[0])
    elif cmd == "doctor":
        doctor_cmd(rest[0], rest[1])
    elif cmd == "mode":
        mode_cmd(rest[0], rest[1])
    elif cmd == "ls":
        ls_cmd(rest[0])
    elif cmd == "seed":
        seed_cmd(rest[0], rest[1])
    elif cmd == "judge":
        judge_cmd(rest[0], rest[1], rest[2], rest[3], rest[4])
    elif cmd == "plan":
        plan_cmd(rest[0], rest[1], rest[2], rest[3])
    elif cmd == "new":
        new_cmd(rest[0], rest[1], rest[2])
    elif cmd == "restore":
        restore_cmd(rest[0], rest[1], rest[2])
    else:
        sys.stderr.write("reader310.py: unknown subcommand %s\n" % cmd)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
READER_EOF
}

# read_cmd WHAT FILE [ARG...]: runs the reader over FILE and keeps its lines in
# READ. A reader that cannot run at all is a stop, never a set of zeroes.
read_cmd() {
  local what=$1 file=$2
  shift 2
  if ! READ=$(python3 "$READER" "$what" "$file" "$@" 2>"$SCRATCH/reader.err"); then
    fact aborted FAIL "this check's own reader did not run ($(first_line "$SCRATCH/reader.err")), so nothing about $file was judged"
    stop "the reader of this check did not run (its own line is above), so nothing was measured"
  fi
}

# field KEY: the reader's own line for KEY, empty when it printed none.
field() { printf '%s\n' "$READ" | sed -n "s/^$1=//p" | head -1; }

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# ------------------------------------------------------------------ the commands
RUN_TIMEOUT=${CHECK310_RUN_TIMEOUT:-1800}

# bounded CMD...: every `fugaro` call below goes through this, so the bound is
# one rule and a converge that hangs is a stop rather than a hang of the check.
bounded() {
  local status=0
  if [ "$RUN_TIMEOUT" -gt 0 ] && command -v timeout >/dev/null 2>&1; then
    timeout "$RUN_TIMEOUT" "$@" || status=$?
  else
    "$@" || status=$?
  fi
  return "$status"
}

# run_tty TAG CMD...: the command on a real terminal. Sets TTY_STATUS, TTY_OUT
# (the capture of its stdout) and TTY_ERR.
TTY_STATUS=0
TTY_OUT=""
TTY_ERR=""
run_tty() {
  local tag=$1
  shift
  TTY_OUT="$SCRATCH/$tag.out"
  TTY_ERR="$SCRATCH/$tag.err"
  TTY_STATUS=0
  say "running (on a real terminal): $*"
  bounded python3 "$PTY" "$TTY_OUT" "$TTY_ERR" -- "$@" || TTY_STATUS=$?
  return 0
}

# run_pipe TAG CMD...: the command with its stdout on a pipe, as a script or a
# CI job would read it. Sets PIPE_STATUS (the command's own status, not cat's).
PIPE_STATUS=0
PIPE_OUT=""
PIPE_ERR=""
run_pipe() {
  local tag=$1
  shift
  PIPE_OUT="$SCRATCH/$tag.out"
  PIPE_ERR="$SCRATCH/$tag.err"
  PIPE_STATUS=0
  say "running (stdout a pipe): $*"
  # A command that exits 1 or 2 here is a reading and not an internal error, and
  # the ERR trap would otherwise end the run on it: inside a pipeline the left
  # side is a subshell of its own, so it cannot be kept out of the trap's way
  # with a `||` the way run_tty does. The trap goes back on straight after.
  set +e
  trap - ERR
  bounded "$@" </dev/null 2>"$PIPE_ERR" | cat >"$PIPE_OUT"
  PIPE_STATUS=${PIPESTATUS[0]}
  trap on_error ERR
  set -e
  return 0
}

# status_note STATUS: a clause for a status that is not one of the three
# outcomes, naming the bound when it is the one that fired.
status_note() {
  case "$1" in
  124) printf ' (the CHECK310_RUN_TIMEOUT bound of %ss fired)' "$RUN_TIMEOUT" ;;
  127) printf ' (the command could not be found on PATH)' ;;
  *) printf '' ;;
  esac
}

# ---------------------------------------------------------------- the readings
VERSION_OUT=""
INSTALLATION=""
GCP=""
REGION=""
# fact_platform: which machine this is. It is read before the dry run's own
# branch, so a dry run's block says which terminal the plan is written for
# rather than reading 'not reached' for the one fact a dry run does settle.
fact_platform() {
  local platform
  platform=$(uname -s 2>/dev/null || echo unknown)
  case "$platform" in
  Darwin)
    fact platform PASS "this is macOS ($platform $(uname -r 2>/dev/null || echo '?')), the terminal the plan's Check 310 names"
    ;;
  *)
    fact platform INFO "this is $platform and not macOS: every reading below is the CLI's own output, which is the same on both, but the plan's Check 310 is written for a macOS terminal and Check 311 for Windows"
    ;;
  esac
}

# step_environment: the binary, the installation in force and the read that must
# file no log (G7). Nothing below is judged without these.
step_environment() {
  local vstatus=0
  VERSION_OUT=$(bounded fugaro version 2>&1) || vstatus=$?
  if [ "$vstatus" != 0 ]; then
    fact fugaro FAIL "fugaro version did not answer (exit $vstatus): $(printf '%s' "$VERSION_OUT" | head -1 | cut -c1-200)"
    stop "the fugaro on PATH did not answer 'fugaro version', so this check cannot tell which release it is looking at"
  fi
  fact fugaro PASS "the fugaro on PATH is $(printf '%s' "$VERSION_OUT" | head -1 | cut -c1-120); every reading below is that binary's own output"

  # The one read of this check: a listing, so it files no log at all (G7, the
  # exemptListings table of internal/cli/report_exempt.go). The directory is
  # listed before and after, and a file that appeared is the finding.
  python3 "$READER" ls "$LOGDIR" >"$SCRATCH/doctor.before" 2>/dev/null || true
  local dstatus=0
  bounded fugaro doctor --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || dstatus=$?
  python3 "$READER" ls "$LOGDIR" >"$SCRATCH/doctor.after" 2>/dev/null || true
  read_cmd doc "$SCRATCH/doctor.json"
  local ok why project
  ok=$(field ok)
  why=$(field why)
  # doctor's own `project` is an object, not the run document's string: the
  # installation's name is the `name` inside it.
  read_cmd doctor "$SCRATCH/doctor.json" name
  project=$(field name)
  read_cmd new "$SCRATCH/doctor.before" "$SCRATCH/doctor.after" doctor
  local filed
  filed=$(field new)
  if [ "$ok" != 1 ]; then
    fact doctor FAIL "fugaro doctor --json did not print one JSON document ($why), so neither the installation in force nor the read's own log rule was read. Its exit status was $dstatus: $(first_line "$SCRATCH/doctor.err")"
    stop "fugaro doctor --json did not print a document (the reader's own line is in the row above), so the installation in force could not be read: name one with --installation <name> and run again"
  fi
  if [ -n "$filed" ]; then
    fact doctor FAIL "fugaro doctor --json filed the log $filed: doctor is a listing and a read, and a read writes no log (G7, the exemptListings table). Delete it if you like; nothing else below is affected"
  else
    fact doctor PASS "fugaro doctor --json printed one JSON document and filed no log at all (the log directory $LOGDIR holds the same names before and after it), which is G7 for a read"
  fi

  if [ -n "$INSTALLATION" ]; then
    if [ -n "$project" ] && [ "$project" != "$INSTALLATION" ]; then
      fact installation FAIL "--installation names $INSTALLATION and fugaro doctor --json reports the project $project: the two disagree, which is what a checkout whose fugaro.yaml names another project does to a named installation (FUGARO_PROJECT). Run this check from outside that checkout, or name the project it names"
    fi
  elif [ -z "$project" ]; then
    stop "no installation is selected: --installation was not given and fugaro doctor --json names no project. Name one: --installation <name>"
  else
    INSTALLATION=$project
  fi
  read_cmd doctor "$SCRATCH/doctor.json" gcp_project
  GCP=$(field gcp_project)
  read_cmd doctor "$SCRATCH/doctor.json" region
  REGION=$(field region)
  if [ -n "$INSTALLATION" ]; then
    export FUGARO_PROJECT="$INSTALLATION"
  fi
  say "the installation this run drives: $INSTALLATION${GCP:+ (GCP $GCP)}"
}

# step_tty: `fugaro update` on a real terminal — G1 (every step printed, a line
# in flight over none of them), G3 (the closing line and the exit status agree)
# and G4 (colour and a line in flight here, where a person is watching).
step_tty() {
  run_tty tty env ${ENVV[@]+"${ENVV[@]}"} fugaro update
  read_cmd lines "$TTY_OUT"
  local steps waits frames escapes closing kind count
  steps=$(field steps)
  waits=$(field waits)
  frames=$(field frames)
  escapes=$(field escapes)
  closing=$(field closing)
  kind=$(field closing_kind)
  count=$(field closing_count)

  if [ "$steps" = 0 ] && [ -z "$closing" ]; then
    fact update-tty FAIL "fugaro update printed no step line and no closing line at all, so this run measured nothing about the default view. Its exit status was $TTY_STATUS$(status_note "$TTY_STATUS") and its last line was: $(field last)"
    stop "fugaro update printed neither a step line nor a closing line (its own output is above and in the block), so the default view was never measured. Fix what it says and run this check again"
  fi

  local want="" verdict="" detail=""
  case "$TTY_STATUS" in
  0) want="done" ;;
  1) want=left ;;
  2) want=failed ;;
  *) want="" ;;
  esac
  if [ -z "$closing" ]; then
    verdict=FAIL
    detail="$steps step line(s) were printed and no closing line closed the run: every step-based command ends with one (Done, Left for you or Failed, in seconds and steps)"
  elif [ -z "$want" ]; then
    verdict=FAIL
    detail="the run exited $TTY_STATUS$(status_note "$TTY_STATUS"), which is none of the three outcomes (0 done or only planned, 1 a step left for you, 2 a failed stage), and its closing line read '$closing'"
  elif [ "$kind" != "$want" ]; then
    verdict=FAIL
    detail="the closing line says '$kind' and the run exited $TTY_STATUS: the account and the exit status disagree (G3 — 0 done, 1 left for you, 2 failed)"
  elif [ -z "$count" ]; then
    verdict=FAIL
    detail="the closing line carries no step count: '$closing'"
  elif [ "$count" -lt "$steps" ]; then
    verdict=FAIL
    detail="the closing line counts $count steps and $steps step line(s) were printed, so the account is short of what a person watched"
  else
    verdict=PASS
    detail="$steps step line(s) and a closing line that names the outcome ($closing) and agrees with the exit status $TTY_STATUS, which is G3; ${waits} line(s) stood for the stages that waited"
  fi
  fact update-tty "$verdict" "$detail"

  # G4 where a person is watching: colour, and the line in flight drawn over no
  # step. The rule turns both off with NO_COLOR set to anything and with
  # TERM=dumb, so a machine that carries either of those is a reading of its own
  # rather than a failure of the CLI.
  local expect_colour=yes why_no=""
  if [ -n "${NO_COLOR:-}" ]; then
    expect_colour=no
    why_no="this shell carries NO_COLOR=$NO_COLOR"
  elif [ "${TERM:-}" = dumb ]; then
    expect_colour=no
    why_no="this shell carries TERM=dumb"
  elif [ -z "${TERM:-}" ]; then
    expect_colour=no
    why_no="this shell carries no TERM at all"
  fi
  if [ "$escapes" -gt 0 ]; then
    fact tty-colour PASS "the run drew colour at the terminal ($escapes escape sequence(s) in its output), which is G4's rule the other way round: a terminal gets the colour and a pipe does not"
  elif [ "$expect_colour" = no ]; then
    fact tty-colour INFO "no escape sequence was drawn, and $why_no — which turns colour off by the CLI's own rule, so none was expected here"
  else
    fact tty-colour FAIL "fugaro update drew no colour on a real terminal (0 escape sequences), and neither NO_COLOR nor TERM=dumb is set in this shell (TERM=${TERM:-unset}): a person watching the converge should see the marks and the words in their colours (G4, D1355)"
  fi
  if [ "$frames" -gt 0 ]; then
    fact tty-spin PASS "the line in flight was drawn ($frames frame(s) of the step in flight in the capture), and each step's own result line took its place: no step's line is hidden behind it (G1)"
  elif [ "$expect_colour" = no ]; then
    fact tty-spin INFO "no line in flight was drawn, and $why_no: the line in flight follows the colour rule exactly (internal/report/report.go: r.spin = colour and not --verbose), so none was expected"
  elif [ "$steps" = 0 ]; then
    fact tty-spin INFO "no line in flight was drawn and the run printed no step line, so there was no step for one to be drawn over"
  else
    fact tty-spin FAIL "fugaro update printed $steps step line(s) on a real terminal and drew no line in flight over any of them (0 frames): a step in flight is drawn the moment the step starts (internal/report/spinner.go startSpinner draws frame 0 synchronously), so this is the spinner never drawing (G1, D1354)"
  fi
}

# step_plan_only: `fugaro setup --plan-only` on a real terminal — the plan view
# (CL5a): a header, one row per stage, no closing line (a plan-only run opens no
# step, so there is no run to close), and the screen's rows in the log, whole.
step_plan_only() {
  run_tty plan env ${ENVV[@]+"${ENVV[@]}"} fugaro setup --plan-only
  # The listing is taken here and not by the caller: the log this run filed has
  # to be read out of the directory as it stands the moment the run returned.
  python3 "$READER" ls "$LOGDIR" >"$PLAN_AFTER" 2>/dev/null || true
  read_cmd lines "$TTY_OUT"
  local rows header closing
  rows=$(field plan_rows)
  header=$(field header)
  closing=$(field closing)
  if [ "$rows" = 0 ]; then
    fact plan-only FAIL "fugaro setup --plan-only printed no plan row: the plan view is one row per stage with the status in its column, and this run printed none (its exit status was $TTY_STATUS$(status_note "$TTY_STATUS") and its last line was: $(field last))"
    stop "fugaro setup --plan-only printed no plan view (its own output is above), so the plan view was never measured. Fix what it says and run this check again"
  fi
  local verdict=PASS detail=""
  if [ -z "$header" ]; then
    verdict=FAIL
    detail="$rows plan row(s) were printed with no header above them: the view heads itself 'fugaro setup: project <name> (GCP <project>, <region>)'"
  elif [ -n "$closing" ]; then
    verdict=FAIL
    detail="the plan view was followed by a closing line ('$closing'): a plan-only run opens no step, so there is no run to close and the plan is not one (CL5a)"
  else
    detail="the plan view: '$header' and $rows row(s), one per stage, and no closing line — a plan-only run opens no step, so there is no run to close (CL5a). Its exit status was $TTY_STATUS"
  fi
  fact plan-only "$verdict" "$detail"

  # The log's copy of the plan: under --plan-only no step is printed at all, so
  # the log is the only place a row's reason is whole (initflow's logRow). The
  # reading is "no fewer", not "the same count": the log is written through a
  # MultiWriter that also takes Terraform's own output, and the plan is written
  # there whole (tf.Summary) where the screen is handed the brief one, so a
  # non-empty plan adds a row to the log alone. A row the screen showed and the
  # log does not is the finding.
  read_cmd new "$PLAN_BEFORE" "$PLAN_AFTER" setup
  local newlog logrows
  newlog=$(field new)
  if [ -z "$newlog" ]; then
    fact plan-log FAIL "fugaro setup --plan-only filed no log in $LOGDIR, and under --plan-only the log is the only place a row's whole reason is readable"
  else
    read_cmd lines "$LOGDIR/$newlog"
    logrows=$(field plan_rows)
    if [ "$logrows" -ge "$rows" ]; then
      fact plan-log PASS "the log $newlog holds the $rows plan row(s) the screen showed, whole and uncoloured, and no fewer ($logrows in all): under --plan-only no step is printed, so the log is the only copy of a reason the screen cut, and it carries what the run wrote besides the plan"
    else
      fact plan-log FAIL "the screen showed $rows plan row(s) and the log $newlog holds $logrows: the log is the whole of what the run printed (D1352), and a row the screen showed is missing from it"
    fi
  fi
}

# judge_stream KEY FILE STATUS WHAT: one command's output judged for G1 and G4
# together — no escape sequence and no carriage return where the rules forbid
# them, the steps still printed, and the closing line agreeing with the exit
# status.
judge_stream() {
  local key=$1 file=$2 status=$3 what=$4
  read_cmd lines "$file"
  local steps escapes barecr closing kind count
  steps=$(field steps)
  escapes=$(field escapes)
  barecr=$(field barecr)
  closing=$(field closing)
  kind=$(field closing_kind)
  count=$(field closing_count)
  if [ "$steps" = 0 ]; then
    fact "$key" FAIL "fugaro update $what printed no step line at all: the default view is one line per step wherever the run's output goes, and a run whose output is not a terminal is not a run with nothing to say (G1). Its exit status was $status$(status_note "$status")"
    return 0
  fi
  if [ "$escapes" != 0 ] || [ "$barecr" != 0 ]; then
    fact "$key" FAIL "fugaro update $what drew $escapes escape sequence(s) and $barecr carriage return(s): off a terminal, with NO_COLOR set and in --json there is no colour, no line in flight and no cursor movement at all (G4, D1355). CLICOLOR_FORCE and FORCE_COLOR are deliberately not read, so an environment that forces colour cannot put ANSI here"
    return 0
  fi
  if [ -z "$closing" ]; then
    fact "$key" FAIL "fugaro update $what printed $steps step line(s) and no closing line: the account ends with Done, Left for you or Failed, in seconds and steps (G3)"
    return 0
  fi
  local want=""
  case "$status" in
  0) want="done" ;;
  1) want=left ;;
  2) want=failed ;;
  esac
  if [ -z "$want" ] || [ "$kind" != "$want" ]; then
    fact "$key" FAIL "fugaro update $what exited $status$(status_note "$status") and its closing line says '$kind' ('$closing'): the account and the exit status disagree (G3)"
    return 0
  fi
  if [ -n "$count" ] && [ "$count" -lt "$steps" ]; then
    fact "$key" FAIL "fugaro update $what closed with '$closing' and printed $steps step line(s): the count is short of what was printed"
    return 0
  fi
  fact "$key" PASS "fugaro update $what printed $steps step line(s) and closed with '$closing', which agrees with the exit status $status (G3), and drew no escape sequence and no carriage return at all (G4)"
}

# step_no_color: `NO_COLOR=1 fugaro update` on a real terminal — G4's first
# rule, with the steps still printed (G1: nothing is suppressed by turning the
# colour off).
step_no_color() {
  run_tty nocolor env ${ENVV[@]+"${ENVV[@]}"} NO_COLOR=1 fugaro update
  judge_stream no-color "$TTY_OUT" "$TTY_STATUS" "with NO_COLOR=1 on a real terminal"
}

# step_piped: `fugaro update | cat` — G4's second rule, stdout a pipe.
step_piped() {
  run_pipe piped env ${ENVV[@]+"${ENVV[@]}"} fugaro update
  judge_stream piped "$PIPE_OUT" "$PIPE_STATUS" "with stdout on a pipe (piped to cat)"
}

# step_json: `fugaro update --json` on a real terminal — G2 (stdout is one
# document and nothing else, even where a person is watching), G3 (the document's
# own outcome matches the exit status) and G4 (no escape sequence in it).
step_json() {
  run_tty json env ${ENVV[@]+"${ENVV[@]}"} fugaro update --json
  read_cmd doc "$TTY_OUT"
  local ok why project failed left stages err escapes barecr
  ok=$(field ok)
  why=$(field why)
  project=$(field project)
  failed=$(field failed)
  left=$(field left)
  stages=$(field stages)
  err=$(field error)
  escapes=$(field escapes)
  barecr=$(field barecr)
  local verdict=PASS detail=""
  if [ "$ok" != 1 ]; then
    verdict=FAIL
    detail="the run's stdout is not one JSON document ($why): under --json stdout is the document and nothing else, even at a terminal (G2). Its exit status was $TTY_STATUS$(status_note "$TTY_STATUS")"
  elif [ "$escapes" != 0 ] || [ "$barecr" != 0 ]; then
    verdict=FAIL
    detail="the document carries $escapes escape sequence(s) and $barecr carriage return(s): --json is never coloured and never drawn over (G2, G4)"
  elif [ -n "$failed" ] && [ "$TTY_STATUS" != 2 ]; then
    verdict=FAIL
    detail="the document names the failed stage $failed and the run exited $TTY_STATUS (G3: a failed stage is 2)"
  elif [ -z "$failed" ] && [ "$TTY_STATUS" = 2 ]; then
    verdict=FAIL
    detail="the run exited 2 and the document names no failed stage, so the account and the exit status disagree (G3)"
  elif [ -n "$left" ] && [ "$left" != 0 ] && [ "$TTY_STATUS" != 1 ] && [ "$TTY_STATUS" != 2 ]; then
    verdict=FAIL
    detail="the document carries $left step(s) left for you and the run exited $TTY_STATUS (G3: a step left for you is 1)"
  elif [ -z "$failed" ] && [ "$left" = 0 ] && [ "$TTY_STATUS" != 0 ] && [ "$err" != 1 ]; then
    verdict=FAIL
    detail="the run exited $TTY_STATUS and the document carries no failed stage, nothing left for you and no error (G3)"
  else
    detail="stdout is one JSON document (project $project, $stages stage(s), $left left for you${failed:+, failed at $failed}), it carries no escape sequence and no carriage return, and its own outcome agrees with the exit status $TTY_STATUS (G2, G3, G4)"
  fi
  fact json "$verdict" "$detail"

  # The account a person reads, beside the document: under --json the human
  # lines go to stderr (report.Options.Err), and they are the same steps and
  # closing line (G1, G2).
  read_cmd lines "$TTY_ERR"
  local steps closing kind count
  steps=$(field steps)
  closing=$(field closing)
  kind=$(field closing_kind)
  count=$(field closing_count)
  local want=""
  case "$TTY_STATUS" in
  0) want="done" ;;
  1) want=left ;;
  2) want=failed ;;
  esac
  if [ "$steps" = 0 ] || [ -z "$closing" ]; then
    fact json-stderr FAIL "the run printed $steps step line(s) and no closing line on stderr: under --json the document is stdout's and the account a person watches is stderr's, and a run that printed neither has no account at all (G2)"
  elif [ "$kind" != "$want" ]; then
    fact json-stderr FAIL "the account on stderr closed with '$closing' while the run exited $TTY_STATUS: the two disagree (G3)"
  elif [ -n "$count" ] && [ "$count" -lt "$steps" ]; then
    fact json-stderr FAIL "the account on stderr closed with '$closing' and printed $steps step line(s): the count is short of what was printed"
  else
    fact json-stderr PASS "the account went to stderr while the document went to stdout: $steps step line(s) and '$closing', which agrees with the exit status $TTY_STATUS (G2's rule that --json leaves stdout to the document alone, G3)"
  fi
}

# step_failure: the forced failure. A fake `terraform` first on PATH answers the
# one question the CLI asks before it converges (its version) and refuses every
# other subcommand, so the failure lands inside a stage — which is the only path
# that prints the step line, the context under it, the log's path and the
# command that explains the run (G8). The fake writes nothing anywhere.
step_failure() {
  local shim=$SCRATCH/bin
  mkdir -p "$shim"
  cat >"$shim/terraform" <<'SHIM_EOF'
#!/usr/bin/env bash
# A fake terraform for Check 310's forced failure: it answers the version
# question the CLI asks before it converges, and refuses every other subcommand
# with the shape of a real terraform error. It writes nothing and changes
# nothing anywhere, so the failure it forces costs nothing but the reading.
set -euo pipefail
for a in "$@"; do
  case "$a" in
  version | -version | --version)
    case " $* " in
    *" -json "*)
      printf '{"terraform_version":"1.9.9","platform":"check310","provider_selections":{},"terraform_outdated":false}\n'
      ;;
    *)
      printf 'Terraform v1.9.9\n'
      ;;
    esac
    exit 0
    ;;
  esac
done
printf 'check310 fake terraform: reading the installation root\n'
printf 'check310-shim-marker: this line is in the log and was never on the screen\n'
printf 'Error: could not read the installation root state\n\n' >&2
printf '  Check 310 forced this failure: the fake terraform first on PATH refuses to run %s.\n' "${1:-?}" >&2
printf '  Nothing was read and nothing was written; the block above is the reading.\n' >&2
exit 1
SHIM_EOF
  chmod +x "$shim/terraform"

  run_tty failure env ${ENVV[@]+"${ENVV[@]}"} PATH="$shim:$PATH" fugaro update
  # Taken here, for the same reason as the plan view's: the log this run filed
  # is what the failure's own path has to point at, and it is read from the
  # directory as it stands the moment the run returned.
  python3 "$READER" ls "$LOGDIR" >"$FAIL_AFTER" 2>/dev/null || true
  read_cmd lines "$TTY_OUT"
  local steps closing kind full diagnose context tf_ran=no
  steps=$(field steps)
  closing=$(field closing)
  kind=$(field closing_kind)
  full=$(field full_output)
  diagnose=$(field diagnose)
  context=$(field context)
  if grep -q terraform "$TTY_OUT" 2>/dev/null; then tf_ran=yes; fi

  # The log the run filed, read out of the directory rather than out of its own
  # line: the two have to be the same file for the path under the failure to
  # mean anything.
  read_cmd new "$FAIL_BEFORE" "$FAIL_AFTER" update
  FAIL_LOG=$(field new)

  local verdict=PASS detail=""
  if [ "$steps" = 0 ]; then
    verdict=FAIL
    detail="the update printed no step line at all, so the failure never reached a stage (its exit status was $TTY_STATUS$(status_note "$TTY_STATUS")). Its last line was: $(field last)"
  elif [ "$TTY_STATUS" != 2 ]; then
    verdict=FAIL
    detail="the update exited $TTY_STATUS$(status_note "$TTY_STATUS") with a stage's failure in it: a failed stage is exit 2 (G3), and the closing line read '$closing'"
  elif [ "$kind" != failed ]; then
    verdict=FAIL
    detail="the update closed with '$closing' while a stage had failed: the closing line has to say Failed (G3)"
  elif [ "$tf_ran" = no ]; then
    verdict=INFO
    detail="the step line, the closing line and the exit status are as above, but the fake terraform never ran, so the forced failure landed before the installation stage and the context, the log's path and the diagnose line were not read this way: $(field last)"
  elif [ "$context" = 0 ]; then
    verdict=FAIL
    detail="the failed step printed no context line under it: the error's own lines after the first are printed on the screen beside it, because a session that cannot open the log still has to see them (G8)"
  elif [ -z "$full" ]; then
    verdict=FAIL
    detail="the failed step printed no 'full output: <path>' line under it, so nothing sends a person to the log that holds the whole of what went wrong (G8, D1352)"
  elif [ ! -f "$full" ]; then
    verdict=FAIL
    detail="the failure names the log $full and there is no such file: the path under a failure has to be the log this run wrote (G5, G8)"
  elif [ -n "$FAIL_LOG" ] && [ "$full" != "$LOGDIR/$FAIL_LOG" ]; then
    verdict=FAIL
    detail="the failure names $full and the log this run filed is $LOGDIR/$FAIL_LOG: the path under the failure is not the run's own log"
  elif [ -z "$diagnose" ]; then
    verdict=FAIL
    detail="the failed step printed no 'Something went wrong. Run: fugaro troubleshoot <id>' line: that line is how a person hands the run on without a path (T-DG9, G8)"
  else
    detail="the stage's failure printed its own step line, $context line(s) of context under it, the log's path and the command that explains the run (fugaro troubleshoot $diagnose), and the run exited 2 with a closing line that says Failed (G3, G8)"
  fi
  fact failure "$verdict" "$detail"

  # The log's own id: the stem of the file the path points at, which is what
  # `fugaro troubleshoot` routes by. A diagnose line naming anything else is a
  # person sent to the wrong run.
  if [ -n "$FAIL_LOG" ] && [ -n "$diagnose" ]; then
    if [ "$diagnose" = "${FAIL_LOG%.log}" ]; then
      fact failure-id PASS "the diagnose line names ${FAIL_LOG%.log}, which is the stem of the log the failure points at: one word is what a person passes on, and it names the run that failed (T-DG9)"
    else
      fact failure-id FAIL "the diagnose line names '$diagnose' and the log this run wrote is '${FAIL_LOG%.log}': the id a person would pass to fugaro troubleshoot is not this run's"
    fi
  elif [ -n "$FAIL_LOG" ]; then
    fact failure-id FAIL "no diagnose line was printed beside the log path, so the run has no id to hand on"
  fi

  # What the log holds that the screen did not: a stage's own output goes to the
  # log and reaches the terminal only under --verbose (D1352), so the fake
  # terraform's marker line is in the log and nowhere on the screen.
  if [ -z "$FAIL_LOG" ]; then
    fact failure-log FAIL "the forced failure filed no log in $LOGDIR, so there is nothing that holds the run's own output"
  elif [ "$tf_ran" = no ]; then
    fact failure-log INFO "the fake terraform never ran (the failure landed before the installation stage), so the log's copy of a stage's own output was not read this way; the log $FAIL_LOG is there all the same"
  else
    local marker=check310-shim-marker
    local inlog=0 nocapture=0
    if grep -q "$marker" "$LOGDIR/$FAIL_LOG" 2>/dev/null; then inlog=1; fi
    if grep -q "$marker" "$TTY_OUT" 2>/dev/null; then nocapture=1; fi
    if [ "$inlog" = 1 ] && [ "$nocapture" = 0 ]; then
      fact failure-log PASS "the log $FAIL_LOG holds the stage's own output (the fake terraform's marker line) and the screen did not: the default view is one line per step, and the run's full output is in the log the failure points at (D1352, G8)"
    elif [ "$inlog" = 0 ] && [ "$nocapture" = 1 ]; then
      fact failure-log FAIL "the stage's own output was on the screen and is not in the log: the log is the whole of what a run printed, and the path under a failure has to hold it (D1352)"
    elif [ "$inlog" = 0 ]; then
      fact failure-log FAIL "the stage's own output is neither in the log nor on the screen: the run swallowed it, which is the one thing the output that explains a failure may not do (G8)"
    else
      fact failure-log FAIL "the stage's own output is in the log AND on the screen: without --verbose the default view is one line per step, and the run's own output belongs to the log alone (D1352, D1354)"
    fi
    # And the log is uncoloured: a log is read with a pager, where the colour
    # that helps a person watching is noise.
    read_cmd lines "$LOGDIR/$FAIL_LOG"
    local logesc
    logesc=$(field escapes)
    if [ "$logesc" = 0 ]; then
      fact log-plain PASS "the log holds no escape sequence at all: the step lines in it are plain (PlainStatus), which is what a log read with a pager needs (D1352)"
    else
      fact log-plain FAIL "the log holds $logesc escape sequence(s): the log's copy of a line is the plain one, and colour in it is noise in the file a person reads with a pager (D1352)"
    fi
  fi
}

# step_log_mode: the log's mode and the directory's (G5, F6a): 0600 inside
# 0700, both this user's.
step_log_mode() {
  local name=$TTY_LOG
  if [ -z "$name" ] || [ ! -f "$LOGDIR/$name" ]; then name=$FAIL_LOG; fi
  if [ -z "$name" ] || [ ! -f "$LOGDIR/$name" ]; then
    fact log-mode FAIL "neither the terminal run nor the forced-failure run filed a log in $LOGDIR, so there is no log to read the mode of"
    return 0
  fi
  read_cmd mode "$LOGDIR/$name" "$LOGDIR"
  local fm dm fo do_
  fm=$(field file_mode)
  dm=$(field dir_mode)
  fo=$(field file_owner)
  do_=$(field dir_owner)
  local verdict=PASS detail=""
  if [ "$fm" != 600 ]; then
    verdict=FAIL
    detail="the log $name has mode $fm and not 0600: a log holds a run's full output, and nobody but its owner has any business in it (F6a, G5)"
  elif [ "$fo" != yes ]; then
    verdict=FAIL
    detail="the log $name is not this user's file"
  elif [ "$dm" != 700 ]; then
    verdict=FAIL
    detail="$LOGDIR has mode $dm and not 0700: the directory the logs live in is 0700, since a log holds a run's full output (F6a, G5)"
  elif [ "$do_" != yes ]; then
    verdict=FAIL
    detail="$LOGDIR is not this user's directory"
  else
    detail="the log $name is mode 0600 inside $LOGDIR, which is mode 0700 and this user's: a log holds a run's full output and its neighbours have no business in it (F6a, G5)"
  fi
  fact log-mode "$verdict" "$detail"
}

# plan_owner_logs: the owner's own logs the rule will remove while this check
# runs, copied aside so the teardown can put them back byte for byte.
#
# It is called before the FIRST fugaro call and not from the retention step:
# every command this check drives opens a log as it starts and prunes the
# directory as it opens it, so by the time the retention step seeds its 51 logs
# a log one of those runs removed is already gone and cannot be copied aside —
# and the teardown's own reading would then report the owner's log lost. The
# rule keeps the newest 50 of a command's logs, so the logs the check's own runs
# file are part of the reading: each command is planned with the number of logs
# the runs this check makes will file — the five `fugaro update`s (the terminal,
# `NO_COLOR`, the pipe, `--json` and the forced failure) and the one `fugaro
# setup --plan-only`. The count only has to be an upper bound: a name planned
# and not removed is a file copied aside for nothing.
plan_owner_logs() {
  [ -n "$LOGDIR" ] || return 0
  [ -d "$LOGDIR" ] || return 0
  mkdir -p "$SCRATCH/backup"
  local pair cmd n
  for pair in update:5 setup:1; do
    cmd=${pair%%:*}
    n=${pair##*:}
    python3 "$READER" plan "$ORIGINAL_SNAPSHOT" "$cmd" "$n" "$SCRATCH/plan-$cmd" >/dev/null 2>&1 || true
    cat "$SCRATCH/plan-$cmd" >>"$SCRATCH/removed-expected" 2>/dev/null || true
  done
  copy_aside_owner_logs
}

# copy_aside_owner_logs: every name on the removal list that is one of the
# owner's own and is still in the directory, byte for byte. The logs this check
# seeded and its guard entries are its own and are skipped, and a name already
# copied aside is not copied again (the list grows as the check runs).
copy_aside_owner_logs() {
  [ -n "$LOGDIR" ] || return 0
  [ -f "$SCRATCH/removed-expected" ] || return 0
  local name kept=0
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    case "$name" in
    */* | . | ..) continue ;;
    esac
    if [ -f "$SCRATCH/backup/$name" ]; then continue; fi
    if grep -qxF "$name" "$SEEDS_FILE" 2>/dev/null; then continue; fi
    if grep -qxF "$name" "$GUARDS_FILE" 2>/dev/null; then continue; fi
    if [ -f "$LOGDIR/$name" ]; then
      cp "$LOGDIR/$name" "$SCRATCH/backup/$name" 2>/dev/null || true
      kept=$((kept + 1))
    fi
  done <"$SCRATCH/removed-expected"
  if [ "$kept" -gt 0 ]; then
    say "$kept of your own log(s) fall inside what the rule removes: they are copied aside and put back at the end"
  fi
}

# step_retention: the rule of G6, read off the directory rather than reasoned
# about. Retention reads the directory, never a history, so 54 seeded logs stand
# in for 51 runs: 51 inside the 14 days and three past them. The failure run's
# own open is what prunes them, which is why this step comes after it.
SEEDED=0
step_retention() {
  local dir=$LOGDIR
  [ -d "$dir" ] || stop "there is no log directory at $dir, so the retention rule has no directory to be read off"
  # The seeds: names the rule reads back as its own (a regular file named
  # <command>-<14 digits UTC>-<pid>.log). 51 of them inside the 14 days and
  # three past it, so both halves of the rule are exercised.
  python3 "$READER" seed 51 3 >"$SCRATCH/seednames" 2>/dev/null || stop "this check could not build the seeded log names"
  SEEDED=$(grep -c . "$SCRATCH/seednames" || true)
  local name
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    : >"$dir/$name"
    chmod 600 "$dir/$name" 2>/dev/null || true
    printf '%s\n' "$name" >>"$SEEDS_FILE"
  done <"$SCRATCH/seednames"
  # The guards: entries the rule must never touch, whatever they are named — a
  # symlink, a directory, a name that is not the shape, another command's log
  # and a file that is not a log at all. Every one of them is stamped PAST the
  # 14 days, so a pruner that reached one of them would remove it and be caught
  # by the reading below.
  local old
  old=$(python3 - <<'PY'
import time
print(time.strftime("%Y%m%d%H%M%S", time.gmtime(time.time() - 20 * 24 * 3600)))
PY
)
  : >"$SCRATCH/guard-target"
  ln -s "$SCRATCH/guard-target" "$dir/update-$old-900001.log" 2>/dev/null || true
  mkdir -p "$dir/update-$old-900002.log" 2>/dev/null || true
  : >"$dir/setup-$old-900003.log"
  : >"$dir/update-notalogno.log"
  : >"$dir/update-$old-900004.log.bak"
  local guard
  for guard in "update-$old-900001.log" "update-$old-900002.log" "setup-$old-900003.log" "update-notalogno.log" "update-$old-900004.log.bak"; do
    printf '%s\n' "$guard" >>"$GUARDS_FILE"
  done
  say "seeded $SEEDED logs and 5 guard entries in $dir (they stand in for 51 runs: retention reads the directory, not a history)"
  python3 "$READER" ls "$dir" >"$SCRATCH/retention.before" 2>/dev/null || stop "this check could not read the log directory $dir"
  # Which of the owner's own logs the failure run's own open will remove, so
  # they can be put back: the log that run files is the one it adds.
  python3 "$READER" plan "$SCRATCH/retention.before" update 1 "$SCRATCH/plan-seeds" >/dev/null 2>&1 || true
  cat "$SCRATCH/plan-seeds" >>"$SCRATCH/removed-expected" 2>/dev/null || true
  copy_aside_owner_logs
}

# step_retention_judge: what the rule did, against what it says it does.
step_retention_judge() {
  local dir=$LOGDIR
  if [ -z "$FAIL_LOG" ]; then
    fact retention FAIL "the forced failure filed no log in $dir, so the rule's own open never happened and retention was not read: the log a command writes on opening is where the rule runs (G6)"
    fact retention-guards FAIL "not read: the run that prunes the directory never opened a log"
    return 0
  fi
  python3 "$READER" ls "$dir" >"$SCRATCH/retention.after" 2>/dev/null || true
  local guards
  guards=$(tr '\n' ',' <"$GUARDS_FILE")
  read_cmd judge "$SCRATCH/retention.before" "$SCRATCH/retention.after" "$FAIL_LOG" update "$guards"
  local cand surv removed wk wkc wg wgc gm gmc kp
  cand=$(field candidates)
  surv=$(field survivors)
  removed=$(field removed)
  wk=$(field wrong_kept)
  wkc=$(field wrong_kept_count)
  wg=$(field wrong_gone)
  wgc=$(field wrong_gone_count)
  gm=$(field guards_missing)
  gmc=$(field guards_missing_count)
  kp=$(field keep_present)
  local verdict=PASS detail=""
  if [ "$kp" != 1 ]; then
    verdict=FAIL
    detail="the log this run wrote ($FAIL_LOG) is gone from $dir: the run just created it, and the rule never removes the log it is writing to"
  elif [ "$wkc" != 0 ]; then
    verdict=FAIL
    detail="$wkc log(s) past the newest 50 or older than 14 days are still there ($wk): retention removes them when a command of that name opens its log (G6)"
  elif [ "$wgc" != 0 ]; then
    verdict=FAIL
    detail="$wgc log(s) the rule keeps are gone ($wg): the rule keeps the newest 50 of a command's logs inside 14 days, and one it removed is a log a person no longer has (G6)"
  else
    detail="with $SEEDED seeded logs standing in for 51 runs, the rule left $surv of $cand candidates and removed $removed, exactly the ones past the newest 50 or older than 14 days; the log this run wrote is among the survivors (G6). Retention reads the directory, never a history"
  fi
  fact retention "$verdict" "$detail"

  if [ "$gmc" = 0 ]; then
    fact retention-guards PASS "the rule reached none of the five entries it must never touch: another command's log, a symlink, a directory, a name that is not the shape and a file that is not a log — all five are still in $dir (G6: only this command's own regular files, never a link, a directory or another command's log)"
  else
    fact retention-guards FAIL "$gmc entry(ies) the rule must never touch are gone ($gm): retention removes only the regular files named <this command>-<14 digits UTC>-<pid>.log — never another command's log, a link, a directory or a name that is not that shape (G6)"
  fi
}

# step_restore: the teardown of the log directory, read back rather than
# assumed: the seeds and the guards this check put there are gone, and every log
# of the owner's that the rule removed while they were there is back.
step_restore() {
  unseed_mine
  restore_owner_logs
  python3 "$READER" ls "$LOGDIR" >"$SCRATCH/final" 2>/dev/null || true
  local mine
  mine=$(cat "$SEEDS_FILE" "$GUARDS_FILE" 2>/dev/null | tr '\n' ',')
  read_cmd restore "$ORIGINAL_SNAPSHOT" "$SCRATCH/final" "$mine"
  local missing mc left lc restored
  missing=$(field missing)
  mc=$(field missing_count)
  left=$(field left)
  lc=$(field left_count)
  restored=$(grep -c . "$RESTORED_FILE" 2>/dev/null || true)
  local verdict=PASS detail=""
  if [ "$mc" != 0 ]; then
    verdict=FAIL
    detail="$mc name(s) that were in $LOGDIR before this check touched it are gone and were not put back ($missing)"
  elif [ "$lc" != 0 ]; then
    verdict=FAIL
    detail="$lc name(s) this check seeded are still in $LOGDIR ($left): they are not logs, and they are removed here or nowhere"
  else
    detail="$LOGDIR holds every name it held before this check touched it: the $SEEDED seeded logs and the five guard entries are gone, and $restored log(s) of your own that the rule removed while they were there were put back byte for byte with mode 0600. The rule would have removed those on your next fugaro update anyway; this check does not leave the directory changed"
  fi
  fact restore "$verdict" "$detail"
}

# ------------------------------------------------------------------- the verdict
# step_verdict STATUS ends a judged run. REACHED_END is what keeps the exit trap
# from reading this as a run that died on an error, and it is set before the exit
# so the trap's own accounting is the same on both endings.
step_verdict() {
  REACHED_END=yes
  case "$1" in
  0) owner "every fact holds: the CLI's output at a terminal is what the plan says it is" ;;
  *) owner "the block's FAILed rows name what the CLI printed instead: each one says which guarantee of the plan it is measured against" ;;
  esac
  exit "$1"
}

# verdict_from_facts: 1 when a judged fact failed, 0 when none did. The rows are
# read back out of the run's own fact file rather than remembered, so the exit
# status cannot drift from the block.
verdict_from_facts() {
  local facts=$1 key
  for key in update-tty tty-colour tty-spin plan-only plan-log no-color piped json json-stderr failure failure-id failure-log log-plain log-mode retention retention-guards doctor installation restore; do
    if [ "$(printf '%s\n' "$facts" | sed -n "s/^$key|\([A-Z]*\)|.*/\1/p" | head -1)" = FAIL ]; then
      return 1
    fi
  done
  return 0
}

# print_plan: the commands a real run would make, printed by --dry-run INSTEAD of
# running anything. This check's own test pins that a dry run makes no `fugaro`
# call at all, so the plan names the commands and never runs one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [local, reads] fugaro version, and fugaro doctor --json — the binary and the installation in force, and the one read of the run (a read files no log at all, G7)"
  say "  [local, mutating] fugaro update on a real terminal — the default view, the colour, the line in flight, the closing line and the exit status (G1, G3, G4)"
  say "  [local, reads] fugaro setup --plan-only on a real terminal — the plan view and the log's own copy of it (CL5a)"
  say "  [local, mutating] NO_COLOR=1 fugaro update on a real terminal, then fugaro update piped to cat — no escape sequence and no carriage return, with the steps still printed (G1, G4)"
  say "  [local, mutating] fugaro update --json on a real terminal — stdout one document, the account on stderr (G2, G3)"
  say "  [local, mutating] 54 seeded logs in $LOGDIR and fugaro update with a fake terraform first on PATH — a forced failure inside a stage: the step line, the context, the log's path and fugaro troubleshoot's line (G8), the log's mode (G5) and the retention rule (G6)"
  say "  [local, reads] ls -l $LOGDIR, printed for you"
  say "Nothing is created, granted or written outside $LOGDIR and this run's own scratch directory, and the seeded logs (and any of yours the rule removes while they are there) are put back or removed again at the end."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(installation belong xtrace agent platform fugaro logdir doctor
  update-tty tty-colour tty-spin plan-only plan-log no-color piped json
  json-stderr failure failure-id failure-log log-plain log-mode retention
  retention-guards ls restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 310 RESULT\n' >&3
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
  printf 'CHECK 310 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable,
  # a bad substitution), and a block that then read as a finished run would offer
  # a FAILed row as an answer about the CLI's output when the command that would
  # have filled it never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the CLI's output"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The seeds and the guard entries this check put in the log directory are read
  # back rather than assumed: a run that leaves them behind has changed a
  # directory of yours and has to say so.
  if [ -n "$LOGDIR" ] && [ -s "$SEEDS_FILE" ]; then
    local left=""
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      if [ -e "$LOGDIR/$name" ]; then left="$left$name "; fi
    done <"$SEEDS_FILE"
    if [ -n "$left" ]; then
      facts="${facts}"$'\n'"scratch|FAIL|this check's seeded logs are STILL in $LOGDIR: ${left}— remove them yourself, they are not logs"
    else
      facts="${facts}"$'\n'"scratch|PASS|the $SEEDED seeded log(s) and the five guard entries are out of $LOGDIR again, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo 'is gone' || echo "IS STILL THERE at $SCRATCH — remove it yourself")"
    fi
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone ($SCRATCH)" || echo "IS STILL THERE at $SCRATCH — remove it yourself")"
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

# -------------------------------------------------------------------- main
USAGE="check310.sh [--installation <name>] [--dry-run]"

parse_args() {
  INSTALLATION=${CHECK310_INSTALLATION:-}
  DRY_RUN=${CHECK310_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
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
}

# ENVV is the environment every `fugaro` call below runs with: the installation
# this check was pointed at, when it was named on the command line. Without it
# the CLI's own selection stands, which is what the owner's own shell does. It is
# expanded as ${ENVV[@]+"${ENVV[@]}"} everywhere, because macOS's bash 3.2 reads
# an empty array under `set -u` as an unbound variable.
ENVV=()

# log_dir: the directory the CLI files a log in — `<state>/fugaro/logs`, with the
# state directory read exactly as localcfg.UserDir reads it (XDG_STATE_HOME when
# it is an absolute path, else HOME/.local/state). It is computed here rather
# than asked for, so a check that reads the wrong directory cannot pass.
is_abs() { case "$1" in /*) return 0 ;; *) return 1 ;; esac; }
log_dir() {
  local base=""
  if [ -n "${XDG_STATE_HOME:-}" ] && is_abs "$XDG_STATE_HOME"; then
    base=$XDG_STATE_HOME
  elif [ -n "${HOME:-}" ] && is_abs "$HOME"; then
    base=$HOME/.local/state
  else
    return 1
  fi
  printf '%s/fugaro/logs\n' "$base"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  guard_agent

  LOGDIR=$(log_dir) || stop "this machine has no state directory for the CLI's logs: neither XDG_STATE_HOME nor HOME is an absolute path, so there is no log directory to read"
  fact logdir INFO "the CLI's logs are kept in $LOGDIR (XDG_STATE_HOME where it is set, else ~/.local/state, then fugaro/logs); a real run reads it and writes nothing in it but its own runs' logs"
  ORIGINAL_SNAPSHOT="$SCRATCH/original"
  fact_platform

  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: no fugaro call was made, nothing was seeded and nothing was written"
    if [ -n "$INSTALLATION" ]; then
      fact installation PASS "$INSTALLATION (typed by you; a real run would drive its fugaro update four times)"
      guard_belong
    else
      fact installation INFO "not read: a dry run makes no fugaro call, so the installation in force is not asked for (a real run reads it from fugaro doctor --json, or takes --installation)"
      fact belong INFO "not read: no installation was named and a dry run reads none"
    fi
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle.
    # It reached its end — the plan IS the end of a dry run — so the exit trap
    # must not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(installation belong xtrace agent platform logdir dry-run scratch)
    return 0
  fi

  write_pty
  write_reader
  # The log directory as it stands before this run touches it, read with the
  # reader written just above: `restore` compares the end against this listing,
  # so it has to be a real one rather than an empty file the reader would have
  # written had it not existed yet.
  python3 "$READER" ls "$LOGDIR" >"$ORIGINAL_SNAPSHOT" 2>/dev/null || true
  # The owner's own logs the rule will remove are copied aside HERE, before the
  # first fugaro call: each of the runs below prunes the directory as it opens
  # its log, so a copy taken later is a copy of a directory the check itself has
  # already emptied.
  plan_owner_logs
  # A named installation is refused by name BEFORE the first fugaro call: the
  # converge below would otherwise be the thing that touched it.
  if [ -n "$INSTALLATION" ]; then
    guard_belong
    ENVV=(FUGARO_PROJECT="$INSTALLATION")
  fi

  step_environment
  guard_belong
  fact installation PASS "$INSTALLATION${GCP:+ (GCP $GCP)}${REGION:+, $REGION}: the installation this run drives, and never belong"
  fact logdir PASS "the CLI's logs are kept in $LOGDIR (XDG_STATE_HOME where it is set, else ~/.local/state, then fugaro/logs — the one directory rule the CLI has, F5)"

  # The log directory before and after each command, so the log a run filed is
  # read out of the directory rather than out of its own line.
  local before after
  before="$SCRATCH/ls.before"
  after="$SCRATCH/ls.after"
  python3 "$READER" ls "$LOGDIR" >"$before" 2>/dev/null || true
  step_tty
  python3 "$READER" ls "$LOGDIR" >"$after" 2>/dev/null || true
  read_cmd new "$before" "$after" update
  TTY_LOG=$(field new)

  PLAN_BEFORE="$SCRATCH/plan.before"
  PLAN_AFTER="$SCRATCH/plan.after"
  python3 "$READER" ls "$LOGDIR" >"$PLAN_BEFORE" 2>/dev/null || true
  step_plan_only

  step_no_color
  step_piped
  step_json

  step_retention
  FAIL_BEFORE="$SCRATCH/failure.before"
  FAIL_AFTER="$SCRATCH/failure.after"
  python3 "$READER" ls "$LOGDIR" >"$FAIL_BEFORE" 2>/dev/null || true
  step_failure
  step_log_mode
  step_retention_judge
  step_restore

  # The plan's own last line: `ls -l` of the log directory, for you to read.
  printf '\nThe log directory, as `ls -l %s` prints it:\n' "$LOGDIR" >&3
  ls -l "$LOGDIR" >&3 2>/dev/null || true
  fact ls INFO "$LOGDIR as ls -l prints it is above, before the block: $(python3 "$READER" ls "$LOGDIR" 2>/dev/null | grep -c . || echo 0) entries, and the CLI keeps 50 per command name for 14 days (G6)"

  # The verdict, out of the rows the block prints: a FAILed row is the CLI's
  # output drifting from the plan, and that is exit 1 rather than an abort.
  local facts
  facts=$(cat "$FACTS_FILE" 2>/dev/null || true)
  if verdict_from_facts "$facts"; then
    step_verdict 0
  fi
  step_verdict 1
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
