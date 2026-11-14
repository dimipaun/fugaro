#!/usr/bin/env bash
# Check 440 — a short run ending resolves to the one run it names (SR1, D1700,
# D1701, D1702, D1705, D1709, D1710), as ONE script YOU run.
#
# It settles the short-form rules of docs/short-run-ids on the dev installation,
# at the places a person meets them:
#   - `fugaro logs <ending>` and `fugaro diagnose <ending>` read the run the
#     ending names, and the diagnosis prints the run's FULL `<slug>/<run-id>`
#     (D1705) — the ending is a way to say a run, never what a report carries;
#   - an ending two runs share is answered at YOUR terminal by the numbered pick
#     list and one question (D1700), and anywhere else — a pipe, a script, an
#     agent's session — by a refusal that shows the same rows and the two ways to
#     name one exactly: a longer ending, or the full run ID;
#   - an ending shorter than four characters is refused as a user error, before
#     any listing (D1701);
#   - `fugaro cancel <ending>` cancels NOTHING, ever (D1710): it reads the
#     ending against the active runs alone and answers the list of full run IDs
#     to paste. An ending naming no active run is refused with `no active run`;
#   - an ending older than the seven-day window still resolves: the lookup
#     widens to the runs the bucket still holds on a miss alone (D1702);
#   - and the cost of that widening is MEASURED here, as the seconds it takes —
#     F5 of the plan's reconciliation leaves it UNVERIFIED.
#
# Two runs launched with `--run-id` values chosen to share their last four
# characters are what make the collision certain rather than hoped for. They are
# real runs of YOUR checkout: **billable**, and each one may open a pull request.
# The check cancels both in its Restore and leaves their records where they are —
# a run's history is the installation's — so a pull request one of them opened is
# YOURS to close. That is also why the check refuses to start without `--yes`.
#
# The pick list is a reading of a TERMINAL, so the two cases that need one run
# under a pty (scripts/live-checks/check111.sh's runner, the same forkpty child):
# a pty is a real terminal, and the child is given no coding-agent marker, which
# is the other half of the same decision inside the CLI. Every other read is
# piped, which is the "anywhere else" half of D1700.
#
# Nothing here is a claim about a run's own logs: `fugaro logs` prints the run's
# log lines and the runner's `run_id` is a Cloud Logging field the view does not
# print (internal/backend/gcp/logs.go), so this check reads the run's identity
# from `--url` — the execution's console link, which is the launch record's own
# `log_url`, compared against the row `fugaro runs ls --json` carries for the run
# the ending must have named. A log line proves nothing about which run was read.
#
# Usage:      bash scripts/live-checks/check440.sh --installation <name> --yes
#             --installation is the dev installation (`FUGARO_PROJECT`) this
#             check launches its two runs against; it is required, and the
#             project named `belong` is refused. Run it from the sandbox
#             repository's own checkout: the two runs are runs OF that
#             repository. `--bucket <bucket>` adds the runs bucket's own reading
#             (the two runs' records, and that no cancel marker was written);
#             without it those rows say so and read nothing. `--old-run
#             <slug>/<run-id>` names the run older than the window by hand, for
#             a bucket that holds none the discovery finds. `--dry-run` prints
#             the guards and the plan and makes NO fugaro and NO gcloud call.
# Needs:      bash, mktemp, sed, python3 and `fugaro` on PATH. `gcloud` is needed
#             only with --bucket. Nothing is installed and nothing is fetched.
# Where:      a SANDBOX installation and a sandbox repository checkout, and NEVER
#             the installation named `belong`: that name is refused in the
#             installation and in the bucket before anything is launched, and a
#             coding agent's session is refused too — this check is yours to run
#             at your own terminal, and no agent runs any step of it.
# Creates:    two runs of this checkout, whose IDs this run mints itself (they
#             share their last four characters by construction), and one scratch
#             directory (0700, outside every git work tree, removed at the end).
#             Each launched run may open a pull request; the Restore cancels both
#             runs and leaves their records and any pull request to you.
# Costs:      two sandbox runs and whatever their tasks spend, on the sandbox's
#             own budget. Every other call is a read. `--dry-run` costs nothing.
# Runs:       only when YOU run it. No agent runs any step of it, and its own
#             test drives it against a fake `fugaro` and a fake `gcloud` only.
# Cleanup:    the Restore cancels both launched runs and reads back that each one
#             is terminal or carries the cancel marker, then removes the scratch
#             directory. It deletes no record: a run's history is the
#             installation's, and a pull request is yours to close.
# Output:     progress on stderr and one `CHECK 440 RESULT` block on stdout at
#             the end, whose lines are `FACT <key>: PASS|FAIL|INFO — <detail>`.
#             Paste that block back and nothing else. Exit 0 is every criterion
#             measured and holding; 1 is a criterion that did not hold; 2 is the
#             check not being able to measure — a guard that stopped it (no
#             `--yes`, `belong`, an agent's session, a missing tool, no run older
#             than the window to read), or an error of its own.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 440".
set -Eeuo pipefail

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes once every step has run, so the exit trap can tell a finished run
# from one that ended on an error of its own.
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
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as a measurement of the resolver for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the resolver"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 440 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 440 needs bash"
  need mktemp
  need sed
  need python3 "the JSON of three fugaro reads is parsed by a reader this check writes into its own scratch directory, and the pty runner is a python3 one"
  need "$FUGARO_BIN" "every read this check makes is a fugaro one: the two launches, the listings, the endings and the cancels"
}

# ------------------------------------------------------------- knobs and names
# The installation is the one thing that must be typed: the runs this check
# launches are runs of it, and no config is read to guess which one.
INSTALLATION=""
BUCKET=""
OLD_RUN=""
DRY_RUN=${CHECK440_DRY_RUN:-}
YES=${CHECK440_YES:-}
FUGARO_BIN=${CHECK440_FUGARO:-fugaro}
TASK=${CHECK440_TASK:-"Report today's date in UTC and change nothing in the repository."}
CLI_TIMEOUT=${CHECK440_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK440_LAUNCH_TIMEOUT_SECONDS:-600}
ACTIVE_TIMEOUT=${CHECK440_ACTIVE_TIMEOUT_SECONDS:-420}
POLL_SECONDS=${CHECK440_POLL_SECONDS:-10}
OLD_DAYS=${CHECK440_OLD_DAYS:-8}
KEEP_SCRATCH=${CHECK440_KEEP_SCRATCH:-}
# The window the resolver lists before it widens (D1702), which is `runs ls`'s
# default of seven days: the check reads it to say the old run was NOT in it,
# which is what makes the row a reading of the widening rather than of the
# window. Change it only if the CLI's own default moves.
WINDOW=${CHECK440_WINDOW:-7d}
OLD_WINDOW=${CHECK440_OLD_WINDOW:-90d}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk440-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
PY=""
RUNNER=""
# This run's own names and the records the Restore reads rather than assumes.
ID_A=""
ID_B=""
SHARED4=""
SLUG=""
LAUNCHED=""
LAUNCH_ATTEMPTED=""
LAUNCH_DOC_A=""
LAUNCH_DOC_B=""
ROW_A=""
ROW_B=""
ENDING_A=""
ENDING_B=""
OLD_ID=""
OLD_ENDING=""
LOG_URL_A=""
# Whether the two runs were seen in the live feed at all, which is what makes
# `cancel <ending>`'s own reading of them possible.
ACTIVE_OK=no
ACTIVE_LAST=""
HELPED=no
# Timings for the latency row, in milliseconds.
MS_LIST_OLD=""
MS_WIDEN=""
MS_LIST_WINDOW=""

# rand_hex: four hex digits from bash's own $RANDOM, so this run's names are its
# own without another tool.
rand_hex() { printf '%04x' "$RANDOM"; }

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk440.XXXXXXXX") || die "Check 440 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  PY="$SCRATCH/py440.py"
  RUNNER="$SCRATCH/run.py"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 440 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  # The two IDs are this run's own, and they share their last four characters by
  # construction: the collision is what the check is about, so it is certain
  # rather than waited for. The stamps differ by one second because two runs
  # cannot carry one ID, and one second apart is the smallest difference whose
  # endings the display rule can still tell apart.
  SHARED4=$(rand_hex)
  local stamps
  stamps=$(python3 - "$SHARED4" <<'STAMP_EOF'
import datetime, sys
base = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
print(base.strftime("%Y%m%d-%H%M%S") + "-" + sys.argv[1])
print((base + datetime.timedelta(seconds=1)).strftime("%Y%m%d-%H%M%S") + "-" + sys.argv[1])
STAMP_EOF
  ) || die "Check 440 cannot mint its two run IDs"
  ID_A=$(printf '%s\n' "$stamps" | sed -n 1p)
  ID_B=$(printf '%s\n' "$stamps" | sed -n 2p)
  [ -n "$ID_A" ] && [ -n "$ID_B" ] || die "Check 440 cannot mint its two run IDs"
  write_reader
  write_runner
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  say "this run's own IDs: $ID_A and $ID_B, which share the ending $SHARED4"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  restore_run
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
  if [ -n "$KEEP_SCRATCH" ]; then
    say "kept: $SCRATCH (CHECK440_KEEP_SCRATCH is set)"
    SCRATCH_REMOVED=kept
    return 0
  fi
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before the
  # block is printed.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# --------------------------------------------------------------- the Restore
# The Restore is the cancel of both launched runs and the read-back that says so.
# It runs from the exit trap, so it has to tolerate a run that stopped anywhere:
# every command is guarded, and a cancel that fails is a FAILed row rather than
# the end of the shell before the block is printed.
restore_run() {
  if [ -z "$LAUNCHED" ]; then
    if [ -n "$LAUNCH_ATTEMPTED" ]; then
      fact restore FAIL "a launch was attempted ($LAUNCH_ATTEMPTED) and this run did not get its result back, so a run of yours may still be going: check 'fugaro runs ls' and cancel what is there by hand"
    else
      fact restore PASS "nothing of this check's was created: no run was launched, so there is nothing to cancel"
    fi
    return 0
  fi
  local id key bad=0 detail=""
  for id in $LAUNCHED; do
    # Both runs are still active here by design (no step cancels them), so this
    # is the graceful cancel's slow path: the CLI waits the run's finalize
    # reserve plus 40s for the runner to notice the marker — 340s by default —
    # before it cancels anything, and CLI_TIMEOUT would kill a healthy cancel
    # and leave the run going, which is the one thing this trap exists to
    # prevent. ACTIVE_TIMEOUT is the bound that clears that floor, and the
    # `--now` retry below is what stops the run when even that is not enough
    # (a repository whose own reserve is longer, or a CLI that never answers).
    key="restore-${id}"
    bounded "$ACTIVE_TIMEOUT" "$key" "$SCRATCH" env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" cancel "$SLUG/$id" --json || true
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then
      say "restore: 'cancel $SLUG/$id' did not finish within ${ACTIVE_TIMEOUT}s (exit $BOUNDED_STATUS); cancelling the execution at once with --now, which skips the draft PR the graceful cancel waits for"
      key="restore-now-${id}"
      bounded "$CLI_TIMEOUT" "$key" "$SCRATCH" env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" cancel "$SLUG/$id" --now --json || true
    fi
    if [ "$BOUNDED_STATUS" -ne 0 ]; then
      bad=1
      detail="$detail$id: cancel exited $BOUNDED_STATUS ($(one_line "$SCRATCH/$key.err")); "
    else
      detail="$detail$id: $(json_field "$SCRATCH/$key.out" status) (marker $(json_field "$SCRATCH/$key.out" marker)); "
    fi
  done
  if [ "$bad" -ne 0 ]; then
    fact restore FAIL "a cancel of this check's own runs failed: $detail"
    return 0
  fi
  fact restore PASS "both launched runs were cancelled ($detail) — a run stops at its next lease, so a row still reading active right after this is one on its way out. Their records stay where they are (a run's history is the installation's) and any pull request they opened is YOURS to close"
}

# ---------------------------------------------------------------- the guards
guard_belong() {
  local name
  for name in "$INSTALLATION" "$BUCKET"; do
    [ -n "$name" ] || continue
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 440 launches two real runs, and it is a SANDBOX check"
      stop "the name '$name' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  fact belong PASS "neither the installation ($INSTALLATION) nor the bucket (${BUCKET:-not given}) names the owner's own installation (nothing typed contains 'belong')"
}

# guard_agent_session: the check is yours, and it drives commands whose writes a
# coding agent's session is refused — and whose pick list an agent's session
# never sees. Refusing here rather than letting each command refuse one at a time
# is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 440 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for, and it is the session whose terminal the pick list is read at"
}

# guard_checkout: the two runs are runs of the repository this shell is in, so
# the checkout is a reading of the check's own premise rather than a detail.
guard_checkout() {
  [ -f "$PWD/fugaro.yaml" ] || stop "$PWD is not a fugaro repository checkout (no fugaro.yaml): run this check from the sandbox repository's own checkout, because the two runs it launches are runs OF that repository"
  fact checkout PASS "$PWD carries fugaro.yaml, so the two runs launched below are runs of this repository"
}

# ------------------------------------------------------------------ the tools
# one_line FILE: the first line of a file, cut to something a row can carry.
one_line() {
  sed -n '1p' "$1" 2>/dev/null | tr -d '\r' | cut -c1-400
}

# json_field FILE NAME: one top-level field of a JSON document, as a string. A
# document this reader cannot read is an empty string, which every caller reads
# as "not there" rather than as a value.
json_field() {
  [ -s "$1" ] || return 0
  python3 "$PY" field "$1" "$2" 2>/dev/null || true
}

# bounded SECONDS KEY DIR CMD...: one command, in DIR, with its output kept in
# $SCRATCH/$KEY.out and $SCRATCH/$KEY.err and a bound of SECONDS. `timeout` is
# not on every macOS, so the bound is a background child and a kill — the same
# shape Check 520 uses. BOUNDED_STATUS and BOUNDED_TIMED_OUT carry the answer.
bounded() {
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  (trap - ERR; cd "$dir" || exit 1; exec "$@") >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  local pid=$! ticks=0 max=$((secs * 10))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$ticks" -lt "$max" ] || break
    sleep 0.1
    ticks=$((ticks + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

# fugaro_call KEY DIR ARGS...: one fugaro read or write against the installation
# this check was given. Every call goes through here so that no call can forget
# which installation it is about.
fugaro_call() {
  local secs=$1 key=$2 dir=$3
  shift 3
  bounded "$secs" "$key" "$dir" env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" "$@"
}

# piped_run KEY ARGS...: one fugaro call with NO terminal on either stream — the
# "anywhere else" half of D1700, and the shape every reading below that is not a
# pick list uses. The exit status is the command's own, not a pipe's.
piped_run() {
  local secs=$1 key=$2
  shift 2
  bounded "$secs" "$key" "$SCRATCH" env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" "$@" </dev/null
}

# now_ms: a clock python3 keeps, because `date +%s%N` is GNU's and the owner's
# machine is a Mac.
now_ms() { python3 "$PY" now; }

# ---------------------------------------------------------------- the reader
write_reader() {
  cat >"$PY" <<'PY_EOF'
#!/usr/bin/env python3
"""Check 440's reader: the little JSON this check reads, and its clock.

  py440.py now                 the current epoch, in milliseconds
  py440.py field FILE NAME     one top-level field of a JSON document
  py440.py rows FILE           one line per run of a `runs ls --json` document:
                               run_id|created|terminal|status|log_url|execution|run|title
  py440.py old FILE DAYS       the runs older than DAYS days, newest first:
                               created|run_id

A document it cannot read is an error and an empty answer, never a guess.
"""
import datetime
import json
import sys
import time


def load(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def parse_time(text):
    s = str(text or "").strip()
    if not s:
        return None
    s = s.replace("Z", "+00:00")
    if "." in s:
        # fromisoformat wants 3 or 6 fractional digits before 3.11, and Go
        # writes nanoseconds: the fraction is cut to microseconds.
        head, rest = s.split(".", 1)
        digits = ""
        i = 0
        while i < len(rest) and rest[i].isdigit():
            digits += rest[i]
            i += 1
        s = head + "." + (digits + "000000")[:6] + rest[i:]
    try:
        return datetime.datetime.fromisoformat(s)
    except ValueError:
        return None


def main(argv):
    if not argv:
        sys.stderr.write("py440.py: a mode is required\n")
        return 2
    mode = argv[0]
    if mode == "now":
        print(int(time.time() * 1000))
        return 0
    if mode == "field":
        doc = load(argv[1])
        value = doc.get(argv[2], "")
        print("" if value is None else value)
        return 0
    if mode == "rows":
        doc = load(argv[1])
        for r in doc.get("runs") or []:
            print("|".join([
                str(r.get("run_id", "")),
                str(r.get("created", "")),
                "true" if r.get("terminal") else "false",
                str(r.get("status", "")),
                str(r.get("log_url", "")),
                str(r.get("execution", "")),
                str(r.get("run", "")),
                str(r.get("title", "")),
            ]))
        return 0
    if mode == "old":
        doc = load(argv[1])
        days = float(argv[2])
        cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=days)
        out = []
        for r in doc.get("runs") or []:
            when = parse_time(r.get("created"))
            if when is None:
                continue
            if when.tzinfo is None:
                when = when.replace(tzinfo=datetime.timezone.utc)
            if when < cutoff:
                out.append((when, str(r.get("run_id", ""))))
        for when, run_id in sorted(out, reverse=True):
            print("%s|%s" % (when.isoformat(), run_id))
        return 0
    sys.stderr.write("py440.py: unknown mode %r\n" % mode)
    return 2


sys.exit(main(sys.argv[1:]))
PY_EOF
  chmod 700 "$PY"
}

# ---------------------------------------------------------------- the runner
# run.py is this check's own pty runner: it runs ONE child at a REAL terminal, so
# the CLI's pick list is reached (stdin and stdout are both a tty), and answers
# the questions the case expects — each answer written only once its own prompt
# has appeared, so an answer can never be taken by another question. A run that
# hits the cap is killed and reads 124, which is how "the command blocked waiting
# for an answer" becomes a FAILed row rather than a hang nobody can report.
write_runner() {
  cat >"$RUNNER" <<'RUNNER_EOF'
#!/usr/bin/env python3
"""Check 440's runner: one child on a pty, answered where it asks.

  run.py --out FILE [--echo FD] [--timeout SECONDS]
         [--expect TEXT --answer TEXT]... -- CMD [ARG...]

The transcript is echoed to --echo (the shell's fd 4, the owner's stderr) as it
arrives, so the run is watched live while the RESULT block stays clean. On the
timeout the child's process group is killed, a CHECK440-RUNNER line is appended
to the transcript and the exit status is 124.
"""
import os
import select
import signal
import sys
import time

KEEP = 65536


def parse(argv):
    o = {"out": None, "echo": None, "timeout": 600.0, "expect": [], "answer": [], "cmd": []}
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--":
            o["cmd"] = argv[i + 1:]
            break
        if a in ("--out", "--echo", "--timeout", "--expect", "--answer"):
            if i + 1 >= len(argv):
                sys.stderr.write("run.py: %s needs a value\n" % a)
                sys.exit(2)
            v = argv[i + 1]
            i += 2
            if a == "--expect":
                o["expect"].append(v)
            elif a == "--answer":
                o["answer"].append(v)
            elif a == "--timeout":
                o["timeout"] = float(v)
            elif a == "--echo":
                o["echo"] = int(v)
            else:
                o["out"] = v
        else:
            sys.stderr.write("run.py: unknown argument %r\n" % a)
            sys.exit(2)
    return o


def write_out(log, echo, data):
    log.write(data)
    if echo is not None:
        try:
            os.write(echo, data)
        except OSError:
            pass


def wait_status(pid):
    _, st = os.waitpid(pid, 0)
    if os.WIFEXITED(st):
        return os.WEXITSTATUS(st)
    if os.WIFSIGNALED(st):
        return 128 + os.WTERMSIG(st)
    return 2


def drain(fd, log, echo, until):
    while True:
        left = until - time.monotonic()
        if left <= 0:
            return
        try:
            r, _, _ = select.select([fd], [], [], min(left, 0.2))
        except InterruptedError:
            continue
        if not r:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            return
        if not chunk:
            return
        write_out(log, echo, chunk)


def main():
    o = parse(sys.argv[1:])
    if not o["out"] or not o["cmd"]:
        sys.stderr.write("run.py: --out and a command are required\n")
        return 2
    if len(o["expect"]) != len(o["answer"]):
        sys.stderr.write("run.py: every --expect needs an --answer\n")
        return 2
    echo = None
    if o["echo"] is not None:
        try:
            os.fstat(o["echo"])
            echo = o["echo"]
        except OSError:
            echo = None
    log = open(o["out"], "wb", buffering=0)
    pid, fd = os.forkpty()
    if pid == 0:
        try:
            os.execvp(o["cmd"][0], o["cmd"])
        except OSError as e:
            sys.stderr.write("run.py: cannot run %s: %s\n" % (o["cmd"][0], e))
            os._exit(127)
    deadline = time.monotonic() + o["timeout"]
    pending = list(zip(o["expect"], o["answer"]))
    buf = b""
    timed_out = False
    while True:
        left = deadline - time.monotonic()
        if left <= 0:
            timed_out = True
            break
        try:
            r, _, _ = select.select([fd], [], [], min(left, 0.2))
        except InterruptedError:
            continue
        if not r:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        buf += chunk
        write_out(log, echo, chunk)
        if len(buf) > KEEP:
            buf = buf[-KEEP:]
        while pending and pending[0][0].encode() in buf:
            _, text = pending.pop(0)
            # The answer is a LINE, so it is written with its Enter: a terminal
            # in canonical mode hands the child nothing until the newline
            # arrives, and an answer missing it would look exactly like a
            # prompt nobody answered.
            try:
                os.write(fd, (text + "\n").encode())
            except OSError:
                pass
    if timed_out:
        try:
            os.killpg(pid, signal.SIGKILL)
        except OSError:
            pass
        drain(fd, log, echo, time.monotonic() + 0.5)
    status = wait_status(pid)
    if timed_out:
        write_out(log, echo, ("\nCHECK440-RUNNER: TIMEOUT: the run was killed after %d s\n"
                              % int(o["timeout"])).encode())
        log.close()
        return 124
    if pending:
        write_out(log, echo, ("\nCHECK440-RUNNER: the prompt this run was to answer "
                              "never appeared: %s\n" % "; ".join(p for p, _ in pending)).encode())
    log.close()
    return status


sys.exit(main())
RUNNER_EOF
  chmod 700 "$RUNNER"
}

# run_pty KEY [PROMPT ANSWER]... -- CMD...: one command at a REAL terminal, its
# transcript in $SCRATCH/$KEY.log and echoed to you live. PTY_STATUS is the
# child's own exit status.
run_pty() {
  local key=$1
  shift
  local answers=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    answers+=(--expect "$1" --answer "$2")
    shift 2
  done
  [ "${1:-}" = "--" ] && shift
  say "at a real terminal (a pty): $*"
  PTY_STATUS=0
  python3 "$RUNNER" --out "$SCRATCH/$key.log" --echo 4 --timeout "$CLI_TIMEOUT" \
    ${answers[@]+"${answers[@]}"} -- "$@" || PTY_STATUS=$?
}

# ------------------------------------------------------------------ the reads
# list_runs KEY WINDOW: `fugaro runs ls --json` over a window, as the rows this
# check reads, and the milliseconds it took.
list_runs() {
  local key=$1 window=$2 started ended
  started=$(now_ms)
  fugaro_call "$CLI_TIMEOUT" "$key" "$SCRATCH" runs ls --json --since "$window"
  ended=$(now_ms)
  MS_LAST=$((ended - started))
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    return 3
  fi
  [ "$BOUNDED_STATUS" -eq 0 ] || return "$BOUNDED_STATUS"
  python3 "$PY" rows "$SCRATCH/$key.out" >"$SCRATCH/$key.rows" 2>"$SCRATCH/$key.pyerr" || return 4
  return 0
}

# row_of KEY RUN_ID: the `runs ls` row of one run, as
# run_id|created|terminal|status|log_url|execution|run|title, or nothing.
row_of() {
  grep -m1 "^$2|" "$SCRATCH/$1.rows" 2>/dev/null || true
}

# cell ROW N: the Nth `|`-separated cell of a row.
cell() { printf '%s' "$1" | cut -d'|' -f"$2"; }

# shortest_ending ID IDS_FILE: the shortest ending of ID that no other ID in the
# file also ends with — task.DisplayRuns's own rule (D1706), one character at a
# time from the end, never shorter than the CLI's minimum of four.
shortest_ending() {
  local id=$1 file=$2 n=4 other len=${#1}
  while [ "$n" -lt "$len" ]; do
    local clash=""
    while IFS= read -r other; do
      [ -n "$other" ] || continue
      [ "$other" = "$id" ] && continue
      if [ "${other:${#other}-n:n}" = "${id:${len}-n:n}" ]; then clash=$other; break; fi
    done <"$file"
    [ -n "$clash" ] || break
    n=$((n + 1))
  done
  printf '%s' "${id:${len}-n:n}"
}

# pick_row FILE ID: the number of the pick list's row whose ending is an ending
# of ID, or nothing. The rule guarantees each row's ending is an ending of
# exactly one candidate, so this names the row of the run without assuming the
# order the list is in.
pick_row() {
  local file=$1 id=$2 n cellending
  local rows
  rows=$(grep -c '^  [0-9]*) ' "$file" 2>/dev/null || true)
  n=1
  while [ "$n" -le "${rows:-0}" ]; do
    cellending=$(sed -n "s/^  $n) \([^ ]*\) .*/\1/p" "$file" | head -n1)
    case "$cellending" in
    */)
      n=$((n + 1))
      continue
      ;;
    esac
    case "$cellending" in
    */*) cellending=${cellending##*/} ;;
    esac
    if [ -n "$cellending" ] && [ "${#cellending}" -le "${#id}" ]; then
      case "$id" in
      *"$cellending") printf '%s' "$n"; return 0 ;;
      esac
    fi
    n=$((n + 1))
  done
  return 1
}

# pick_cell FILE N: the `slug/ending` cell of the pick list's Nth row.
pick_cell() { sed -n "s/^  $2) \([^ ]*\) .*/\1/p" "$1" | head -n1; }

# --------------------------------------------------------------------- steps
step_launch() {
  local i=0 id
  for id in "$ID_A" "$ID_B"; do
    i=$((i + 1))
    LAUNCH_ATTEMPTED="$LAUNCH_ATTEMPTED $id"
    fugaro_call "$LAUNCH_TIMEOUT" "launch$i" "$PWD" run --run-id "$id" --json "$TASK"
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      stop "the launch of $id did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch$i.out and $SCRATCH/launch$i.err"
    fi
    if [ "$BOUNDED_STATUS" -ne 0 ]; then
      stop "the launch of $id failed (exit $BOUNDED_STATUS): $(one_line "$SCRATCH/launch$i.err")"
    fi
    LAUNCHED="$LAUNCHED $id"
    if [ "$i" = 1 ]; then LAUNCH_DOC_A="$SCRATCH/launch1.out"; else LAUNCH_DOC_B="$SCRATCH/launch2.out"; fi
    say "launched $id: $(json_field "$SCRATCH/launch$i.out" status)"
  done
  # The slug is the launch's own answer rather than a guess from the directory
  # name: one run ID can exist in two repositories, and every read below names
  # the run as `<slug>/<run-id>`.
  SLUG=$(json_field "$LAUNCH_DOC_A" run)
  SLUG=${SLUG%%/*}
  [ -n "$SLUG" ] || stop "the launch's own document named no run: $(one_line "$LAUNCH_DOC_A")"
  LOG_URL_A=$(json_field "$LAUNCH_DOC_A" log_url)

  local status
  status=0
  list_runs listing "$WINDOW" || status=$?
  case "$status" in
  0) ;;
  3) stop "fugaro runs ls did not answer inside ${CLI_TIMEOUT}s: the listing this check reads its two runs from is the one thing it cannot do without" ;;
  4) stop "the runs listing's JSON could not be read: $(one_line "$SCRATCH/listing.pyerr")" ;;
  *) stop "fugaro runs ls --json --since $WINDOW failed (exit $status): $(one_line "$SCRATCH/listing.err")" ;;
  esac
  local row_a row_b
  row_a=$(row_of listing "$ID_A")
  row_b=$(row_of listing "$ID_B")
  local detail=""
  [ -n "$row_a" ] || detail="$detail$ID_A is NOT in the listing of the last $WINDOW; "
  [ -n "$row_b" ] || detail="$detail$ID_B is NOT in the listing of the last $WINDOW; "
  if [ -n "$detail" ]; then
    fact runs-launched FAIL "both runs were launched but the listing does not hold them: $detail"
    return 0
  fi
  local term_a term_b
  term_a=$(cell "$row_a" 3)
  term_b=$(cell "$row_b" 3)
  if [ "$term_a" = true ] || [ "$term_b" = true ]; then
    fact runs-launched FAIL "$ID_A (terminal=$term_a) and $ID_B (terminal=$term_b): a run that ended this fast cannot carry the active-run readings below, and it ended before the check could read it — look at its own record before reading anything into this row"
    return 0
  fi
  fact runs-launched PASS "$ID_A and $ID_B were launched against $INSTALLATION and are both in the last $WINDOW as active runs (terminal=false), each named $SLUG/<run-id>; they share the ending $SHARED4, which is the collision every reading below is about"
}

# step_cancel_active: D1710. `cancel` takes no ending and cancels nothing: the
# ending is read against the ACTIVE runs alone and the answer is the list of full
# run IDs to paste. The runs have to be in the live feed for that read to see
# them, and the feed is written by the runner inside the container rather than by
# the launch, so this is the one step that waits.
step_cancel_active() {
  local waited=0 out
  while :; do
    fugaro_call "$CLI_TIMEOUT" cancel-active "$SCRATCH" cancel "$SHARED4"
    out=$(cat "$SCRATCH/cancel-active.err" 2>/dev/null || true)
    ACTIVE_LAST=$out
    if [ "$BOUNDED_STATUS" -eq 1 ] &&
      grep -q "names [0-9][0-9]* active runs" <<<"$out" &&
      grep -q "^  $SLUG/$ID_A  " <<<"$out" &&
      grep -q "^  $SLUG/$ID_B  " <<<"$out"; then
      ACTIVE_OK=yes
      break
    fi
    # An ending that EXITS 0 has cancelled a run, which is the one thing D1710
    # forbids: that is read here rather than waited out, because the run it acted
    # on is the finding and the seconds until the feed catches up would only
    # delay it.
    if [ "$BOUNDED_STATUS" -eq 0 ]; then
      fact cancel-active-refuses FAIL "fugaro cancel $SHARED4 exited 0: the ending CANCELLED a run, and D1710 says an ending cancels nothing — the run it acted on carries a cancel marker in the bucket and any pull request it opened is yours. It answered: $(one_line "$SCRATCH/cancel-active.out"); it said: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
      return 0
    fi
    [ "$waited" -lt "$ACTIVE_TIMEOUT" ] || break
    say "waiting for both runs to reach the live feed (${waited}s of ${ACTIVE_TIMEOUT}s): fugaro cancel $SHARED4 said: $(printf '%s' "$out" | head -n1)"
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ACTIVE_OK" != yes ]; then
    fact cancel-active-refuses FAIL "after ${ACTIVE_TIMEOUT}s the live feed did not hold both runs, so D1710's active-only list was NOT measured; the last answer was: $(printf '%s' "$ACTIVE_LAST" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q '^pass one of the full run IDs above' <<<"$ACTIVE_LAST"; then
    fact cancel-active-refuses FAIL "the refusal names both runs but does not carry 'pass one of the full run IDs above': $(printf '%s' "$ACTIVE_LAST" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  fact cancel-active-refuses PASS "fugaro cancel $SHARED4 exited 1 with \"$SHARED4\" names 2 active runs, listed both as $SLUG/$ID_A and $SLUG/$ID_B with 'pass one of the full run IDs above' — the ending cancelled nothing, and the two rows a person pastes are the full IDs (D1710)"
}

# step_cancel_marker: the runs bucket's own reading of the same thing — the
# cancel marker `fugaro cancel <ending>` must never have written. It is the one
# reading here that does not come from fugaro, which is why it is a row of its
# own; without --bucket the check has no bucket to read and says so.
step_cancel_marker() {
  if [ -z "$BUCKET" ]; then
    fact cancel-marker INFO "not measured: pass --bucket <the installation's runs bucket> to read the runs bucket's own record of the two runs and of the cancel marker the ending must never have written (the name is in 'fugaro doctor'\\''s runs-bucket-iam line, as gs://<bucket>)"
    return 0
  fi
  local id detail="" bad=0
  for id in $LAUNCHED; do
    bounded "$CLI_TIMEOUT" "bucket-$id" "$SCRATCH" gcloud storage ls "gs://$BUCKET/runs/$SLUG/$id/"
    if [ "$BOUNDED_STATUS" -ne 0 ]; then
      bad=1
      detail="$detail$id: gcloud storage ls exited $BOUNDED_STATUS ($(one_line "$SCRATCH/bucket-$id.err")); "
      continue
    fi
    if grep -q "launch.json" "$SCRATCH/bucket-$id.out"; then
      detail="$detail$id: its record is in the bucket; "
    else
      detail="$detail$id: the listing names no launch.json; "
    fi
    if grep -qE "/cancel$" "$SCRATCH/bucket-$id.out"; then
      bad=1
      detail="$detail$id: a cancel marker IS there, which 'fugaro cancel <ending>' must never write; "
    fi
  done
  if [ "$bad" -ne 0 ]; then
    fact cancel-marker FAIL "$detail"
    return 0
  fi
  fact cancel-marker PASS "the runs bucket's own listing of both runs ($BUCKET) names each run's launch.json and NO cancel marker under either: the ending cancelled nothing in the bucket's record either. $detail"
}

# step_too_short: D1701's minimum, refused before any listing.
step_too_short() {
  piped_run "$CLI_TIMEOUT" too-short diagnose abc
  local out
  out=$(cat "$SCRATCH/too-short.err" 2>/dev/null || true)
  if [ "$BOUNDED_STATUS" -ne 1 ]; then
    fact diagnose-too-short FAIL "fugaro diagnose abc exited $BOUNDED_STATUS (want 1): $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q 'is 3 characters; an ending needs at least 4, the shortest form a run is shown in' <<<"$out"; then
    fact diagnose-too-short FAIL "fugaro diagnose abc was refused, but not with the minimum's own sentence: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  fact diagnose-too-short PASS "fugaro diagnose abc exited 1 with \"abc\" is 3 characters; an ending needs at least 4, the shortest form a run is shown in — the minimum is checked before any listing (D1701)"
}

# step_refuses_piped: D1700's other half, and the reading that gives the check
# the two rows it answers the pick list with. The refusal carries the same
# numbered rows the pick list shows, so the numbers below are the list's own.
step_refuses_piped() {
  piped_run "$CLI_TIMEOUT" piped diagnose "$SHARED4"
  local out
  out=$(cat "$SCRATCH/piped.err" 2>/dev/null || true)
  if [ "$BOUNDED_STATUS" -ne 1 ]; then
    fact diagnose-refuses-piped FAIL "fugaro diagnose $SHARED4 < /dev/null exited $BOUNDED_STATUS (want 1): the refusal is what an ending gets away from a person's terminal: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q "\"$SHARED4\" names [0-9][0-9]* runs" <<<"$out"; then
    fact diagnose-refuses-piped FAIL "the piped refusal does not name the runs the ending collides with: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q '^pass a longer ending or the full run ID' <<<"$out"; then
    fact diagnose-refuses-piped FAIL "the piped refusal does not carry 'pass a longer ending or the full run ID': $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  ROW_A=$(pick_row "$SCRATCH/piped.err" "$ID_A" || true)
  ROW_B=$(pick_row "$SCRATCH/piped.err" "$ID_B" || true)
  if [ -z "$ROW_A" ] || [ -z "$ROW_B" ] || [ "$ROW_A" = "$ROW_B" ]; then
    fact diagnose-refuses-piped FAIL "the piped refusal's rows do not tell the two runs apart (row of $ID_A: ${ROW_A:-none}, of $ID_B: ${ROW_B:-none}): $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  ENDING_A=$(pick_cell "$SCRATCH/piped.err" "$ROW_A")
  ENDING_A=${ENDING_A##*/}
  ENDING_B=$(pick_cell "$SCRATCH/piped.err" "$ROW_B")
  ENDING_B=${ENDING_B##*/}
  fact diagnose-refuses-piped PASS "piped, fugaro diagnose $SHARED4 exited 1 with \"$SHARED4\" names 2 runs, the numbered rows (row $ROW_A is $SLUG/$ENDING_A, row $ROW_B is $SLUG/$ENDING_B) and 'pass a longer ending or the full run ID': nothing that cannot answer is handed a run it did not choose (D1700)"
}

# step_pick_terminal: the same ending at a person's terminal is the numbered list
# and one question (D1700), and the diagnosis it answers with prints the run's
# FULL ID (D1705) rather than the ending that was typed.
step_pick_terminal() {
  if [ -z "$ROW_A" ]; then
    return 0
  fi
  run_pty diagnose-pick "which run? [1-" "$ROW_A" -- env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" diagnose "$SHARED4"
  # A pty's output is CRLF, and two of the readings below are anchored to the
  # end of a line: the transcript is read with the carriage returns taken out.
  local log="$SCRATCH/diagnose-pick.log" out
  out=$(tr -d '\r' <"$log" 2>/dev/null || true)
  if [ "$PTY_STATUS" -ne 0 ]; then
    fact diagnose-pick-terminal FAIL "the typed diagnosis exited $PTY_STATUS: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  # The question itself, digits and all: the pty runner names the prompt it was
  # to answer in its own "never appeared" line, and a reading that took that line
  # for the question would call a command that never asked a pass.
  if ! grep -qE 'which run\? \[1-[0-9]+\] ' <<<"$out"; then
    fact diagnose-pick-terminal FAIL "the terminal was never asked which run the ending means: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q "^Run:      $SLUG/$ID_A\$" <<<"$out"; then
    fact diagnose-pick-terminal FAIL "answering row $ROW_A did not diagnose $SLUG/$ID_A: the diagnosis has to carry the full <slug>/<run-id> and not the ending (D1705): $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  fact diagnose-pick-terminal PASS "at a terminal fugaro diagnose $SHARED4 asked 'which run? [1-2]', row $ROW_A answered for $SLUG/$ID_A, and the diagnosis prints 'Run:      $SLUG/$ID_A' in full (D1700, D1705)"
}

# step_logs: `fugaro logs` takes an ending too, and the run it read is the one
# the ending names. The identity is read from --url: the launch record's own
# console link, compared against the listing's log_url for that run, because a
# log line names no run (the runner's run_id is a Cloud Logging field the view
# does not print).
step_logs() {
  # A longer ending needs no list and no terminal: it names the one run.
  piped_run "$CLI_TIMEOUT" logs-unique logs --url "$ENDING_A"
  local out
  out=$(tr -d '\r' <"$SCRATCH/logs-unique.out" 2>/dev/null | sed -n '1p')
  local want
  want=$(cell "$(row_of listing "$ID_A")" 5)
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    fact logs-ending FAIL "fugaro logs --url $ENDING_A exited $BOUNDED_STATUS: $(one_line "$SCRATCH/logs-unique.err")"
  elif [ -z "$out" ]; then
    fact logs-ending FAIL "fugaro logs --url $ENDING_A printed no link"
  elif [ "$out" != "$want" ]; then
    fact logs-ending FAIL "fugaro logs --url $ENDING_A printed '$out', and the listing's own log_url for $ID_A is '$want': the ending did not read the run it names"
  else
    fact logs-ending PASS "fugaro logs --url $ENDING_A exited 0 and printed $ID_A's own console link, the listing's log_url for that run: a longer ending reads the one run it names with no list and no question (D1701)"
  fi

  # The shared four at a terminal: the pick list, then the log of the run the
  # answer named.
  if [ -z "$ROW_A" ]; then
    return 0
  fi
  run_pty logs-collision "which run? [1-" "$ROW_A" -- env FUGARO_PROJECT="$INSTALLATION" "$FUGARO_BIN" logs --url "$SHARED4"
  local log="$SCRATCH/logs-collision.log" cout
  cout=$(tr -d '\r' <"$log" 2>/dev/null || true)
  local link
  link=$(grep -m1 '^https\?://' <<<"$cout" || true)
  if [ "$PTY_STATUS" -ne 0 ]; then
    fact logs-collision FAIL "the typed logs run exited $PTY_STATUS: $(printf '%s' "$cout" | tr '\n' ' ' | cut -c1-400)"
  elif ! grep -q "^  $ROW_A) $SLUG/$ENDING_A " <<<"$cout"; then
    fact logs-collision FAIL "the pick list does not carry row $ROW_A as $SLUG/$ENDING_A: $(printf '%s' "$cout" | tr '\n' ' ' | cut -c1-400)"
  elif [ -z "$link" ]; then
    fact logs-collision FAIL "the answered run printed no console link: $(printf '%s' "$cout" | tr '\n' ' ' | cut -c1-400)"
  elif [ "$link" != "$want" ]; then
    fact logs-collision FAIL "answering row $ROW_A printed '$link', and the listing's own log_url for $ID_A is '$want': the answer did not read the run the row named"
  else
    fact logs-collision PASS "at a terminal fugaro logs --url $SHARED4 showed the numbered pick list, row $ROW_A answered for $SLUG/$ID_A, and the link printed is that run's own log_url: the list and the answer both name the run exactly (D1700)"
  fi
}

# step_old_run: the run older than the window, which is what D1702's widening is
# read with. It is found by listing the bucket's own window (90 days, the
# retention the plan's F5 is about) and taking the newest run older than eight
# days whose ending is unique among everything listed — unique, because the two
# readings below are refusals and a resolution, and an ending another run also
# ends with would answer for the wrong run.
step_old_run() {
  local status=0
  list_runs old-listing "$OLD_WINDOW" || status=$?
  case "$status" in
  0) ;;
  3) stop "fugaro runs ls --since $OLD_WINDOW did not answer inside ${CLI_TIMEOUT}s: the run older than the window is found by listing, and without it the two D1702 rows cannot be read" ;;
  4) stop "the $OLD_WINDOW listing's JSON could not be read: $(one_line "$SCRATCH/old-listing.pyerr")" ;;
  *) stop "fugaro runs ls --json --since $OLD_WINDOW failed (exit $status): $(one_line "$SCRATCH/old-listing.err")" ;;
  esac
  MS_LIST_OLD=$MS_LAST
  cut -d'|' -f1 "$SCRATCH/old-listing.rows" >"$SCRATCH/old-ids"
  if [ -n "$OLD_RUN" ]; then
    OLD_ID=${OLD_RUN##*/}
  else
    local line
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      OLD_ID=$(printf '%s' "$line" | cut -d'|' -f2)
      [ -n "$OLD_ID" ] || continue
      OLD_ENDING=$(shortest_ending "$OLD_ID" "$SCRATCH/old-ids")
      break
    done < <(python3 "$PY" old "$SCRATCH/old-listing.out" "$OLD_DAYS")
  fi
  if [ -z "$OLD_ID" ]; then
    stop "no run older than ${OLD_DAYS} days is in the last $OLD_WINDOW of this installation's bucket, so there is nothing for the widening to find: pass --old-run <slug>/<run-id> naming a run the bucket holds and that the last $WINDOW does not, or run this check again once a run has aged past ${OLD_DAYS} days"
  fi
  [ -n "$OLD_ENDING" ] || OLD_ENDING=$(shortest_ending "$OLD_ID" "$SCRATCH/old-ids")

  # The window is what the widening is measured against: a run the last seven
  # days already hold is found by the ordinary lookup, and its row would say
  # nothing about D1702.
  local wstatus=0
  list_runs window-listing "$WINDOW" || wstatus=$?
  case "$wstatus" in
  0) ;;
  3) stop "fugaro runs ls --since $WINDOW did not answer inside ${CLI_TIMEOUT}s: the window the old run must be outside of could not be listed" ;;
  4) stop "the $WINDOW listing's JSON could not be read: $(one_line "$SCRATCH/window-listing.pyerr")" ;;
  *) stop "fugaro runs ls --json --since $WINDOW failed (exit $wstatus): $(one_line "$SCRATCH/window-listing.err")" ;;
  esac
  MS_LIST_WINDOW=$MS_LAST
  if [ -n "$(row_of window-listing "$OLD_ID")" ]; then
    fact old-run FAIL "$OLD_ID is in the last $WINDOW, so the lookup that finds it is the ordinary one and the D1702 rows below would read the window rather than the widening: pass --old-run naming a run the last $WINDOW does not hold"
    OLD_ID=""
    return 0
  fi
  fact old-run PASS "the run $SLUG/$OLD_ID is older than ${OLD_DAYS} days, is NOT in the last $WINDOW, and is in the bucket's last $OLD_WINDOW; its ending $OLD_ENDING is unique among the $(wc -l <"$SCRATCH/old-ids" | tr -d ' ') runs that listing holds, so it names that one run"
}

# step_cancel_finished: `cancel` reads an ending against the ACTIVE runs alone,
# so an ending of a finished run names none of them and is refused with the way
# out — never with a list, and never with a cancel.
step_cancel_finished() {
  [ -n "$OLD_ID" ] || return 0
  fugaro_call "$CLI_TIMEOUT" cancel-finished "$SCRATCH" cancel "$OLD_ENDING"
  local out
  out=$(cat "$SCRATCH/cancel-finished.err" 2>/dev/null || true)
  if [ "$BOUNDED_STATUS" -ne 1 ]; then
    fact cancel-finished-refuses FAIL "fugaro cancel $OLD_ENDING exited $BOUNDED_STATUS (want 1): $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  if ! grep -q "^no active run ending \"$OLD_ENDING\"; pass the full run ID\$" <<<"$out"; then
    fact cancel-finished-refuses FAIL "fugaro cancel $OLD_ENDING was refused, but not with the finished run's own sentence: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  fact cancel-finished-refuses PASS "fugaro cancel $OLD_ENDING ($SLUG/$OLD_ID, finished long ago) exited 1 with 'no active run ending \"$OLD_ENDING\"; pass the full run ID': the active-only reading names no run to act on and cancels nothing (D1710)"
}

# step_widen: D1702. The window has no match, so the lookup lists the bucket's
# own runs once more and finds the old one — and the seconds that cost are the
# figure F5 leaves UNVERIFIED.
step_widen() {
  [ -n "$OLD_ID" ] || return 0
  local started ended
  started=$(now_ms)
  piped_run "$CLI_TIMEOUT" widen diagnose "$OLD_ENDING"
  ended=$(now_ms)
  MS_WIDEN=$((ended - started))
  local out
  out=$(tr -d '\r' <"$SCRATCH/widen.out" 2>/dev/null || true)
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    fact window-widens FAIL "fugaro diagnose $OLD_ENDING exited $BOUNDED_STATUS: the ending of a run the bucket still holds is refused rather than looked up past the window: $(one_line "$SCRATCH/widen.err")"
    return 0
  fi
  if ! grep -q "^Run:      $SLUG/$OLD_ID\$" <<<"$out"; then
    fact window-widens FAIL "fugaro diagnose $OLD_ENDING did not diagnose $SLUG/$OLD_ID (the run is not in the last $WINDOW, so only the widening can have found it): $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
    return 0
  fi
  fact window-widens PASS "fugaro diagnose $OLD_ENDING exited 0 and printed 'Run:      $SLUG/$OLD_ID', a run the last $WINDOW does not hold: the lookup widened past the window on the miss alone (D1702), and the diagnosis carries the full ID (D1705)"
}

# step_latency: F5's cost, as a figure and never a criterion. What it measures
# is the widening itself — the extra listing a lookup of an old run pays — beside
# the two ordinary listings of the same bucket, so the owner can say what the
# widening costs on an installation with real history in it.
step_latency() {
  local detail="the bucket's own listings: ${MS_LIST_OLD}ms over $OLD_WINDOW and ${MS_LIST_WINDOW}ms over $WINDOW"
  if [ -n "$MS_WIDEN" ]; then
    detail="$detail; the widening lookup of $OLD_ENDING (a diagnose that had to go past the window): ${MS_WIDEN}ms"
  fi
  fact latency INFO "$detail — measured here and graded by nothing: F5 of docs/short-run-ids/reconciliation.md leaves the cost of the wider listing UNVERIFIED, and this is one installation's figure rather than a bound"
}

# ---------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(agent-session belong installation checkout runs-launched
  cancel-active-refuses cancel-marker diagnose-too-short diagnose-refuses-piped
  diagnose-pick-terminal logs-collision logs-ending old-run
  cancel-finished-refuses window-widens latency restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 440 RESULT\n' >&3
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
  printf 'CHECK 440 RESULT END (exit %s)\n' "$status" >&3
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
  # exits by itself for an error the ERR trap does not see, and a block that then
  # read as a finished run would offer FAILed rows as readings of the resolver
  # when the steps that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the resolver"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no run of this check's to cancel"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so the runs this check launched may still be going: cancel them by hand with 'fugaro cancel $SLUG/<run-id>' for$LAUNCHED"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed row would read as a pass. The latency row is INFO and can never do
  # this, which is deliberate — it is a figure, not a criterion.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -q '|FAIL|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" = no ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: cancelling this check's own runs and printing what was measured"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check440.sh --installation <name> --yes [--bucket <bucket>] [--old-run <slug>/<run-id>] [--dry-run]"

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --bucket)
      [ $# -ge 2 ] || die "--bucket needs a bucket name (with or without gs://). usage: $USAGE"
      BUCKET=$2
      shift 2
      ;;
    --old-run)
      [ $# -ge 2 ] || die "--old-run needs <slug>/<run-id>. usage: $USAGE"
      OLD_RUN=$2
      shift 2
      ;;
    --yes)
      YES=1
      shift
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
  case "$BUCKET" in gs://*) BUCKET=${BUCKET#gs://} ;; esac
  case "$BUCKET" in */) BUCKET=${BUCKET%/} ;; esac
  [ -n "$INSTALLATION" ] || die "--installation is required, and it has to be a SANDBOX installation: this check launches two real runs of the checkout it is run from. usage: $USAGE"
}

print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, writes, BILLABLE] in $PWD: FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN run --run-id $ID_A --json '$TASK'"
  say "  [network, writes, BILLABLE] the same with --run-id $ID_B — one second later, sharing the ending $SHARED4, which is what makes the collision certain rather than hoped for. Each launch may open a pull request"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN runs ls --json --since $WINDOW — the two runs, as the listing that every ending below is resolved against"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN cancel $SHARED4 — polled until the live feed holds both runs (up to ${ACTIVE_TIMEOUT}s), then read for its refusal: it names the active runs and the full IDs to paste, and cancels nothing (D1710)"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN diagnose abc — read for the minimum's own sentence (D1701)"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN diagnose $SHARED4 < /dev/null | cat — read for the refusal that shows the rows and the way out (D1700); its numbered rows are what the two pty runs below are answered with"
  say "  [network, reads, at a REAL TERMINAL] the check's own pty runner around FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN diagnose $SHARED4 — the numbered list and 'which run? [1-2]', answered with the row of $ID_A; the answer is the diagnosis of that run, whose 'Run:' line has to be the full <slug>/<run-id> (D1705)"
  say "  [network, reads, at a REAL TERMINAL] the same pty around FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN logs --url $SHARED4 — the same list, and the link printed has to be the run the answer named (the log lines themselves name no run, so --url is the identity)"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN logs --url <the ending the list gave for $ID_A> — a longer ending, with no list and no question"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN runs ls --json --since $OLD_WINDOW (timed) and --since $WINDOW, to find a run older than ${OLD_DAYS} days that the last $WINDOW does not hold, and to read that it is NOT in the window"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN cancel <that run's ending> — read for 'no active run ending ...; pass the full run ID'"
  say "  [network, reads] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN diagnose <that run's ending> (timed) — read for its 'Run:' line: the lookup widened past the window on the miss alone (D1702), and the seconds it took are F5's figure"
  if [ -n "$BUCKET" ]; then
    say "  [network, reads] gcloud storage ls gs://$BUCKET/runs/$SLUG/$ID_A/ and the same for $ID_B — the bucket's own record of both runs, and that NO cancel marker is under either. The bucket name is global, so no project is passed: the installation's name and its GCP project are two different things and this check is given only the first"
  else
    say "  [nothing] --bucket was not given, so the runs bucket's own record is NOT read and its row says so"
  fi
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, writes] FUGARO_PROJECT=$INSTALLATION $FUGARO_BIN cancel ${SLUG:-<slug>}/$ID_A --json and the same for $ID_B — the cancel marker, and the status read back from each command's own --json. The runs' records stay where they are (a run's history is the installation's) and any pull request they opened is YOURS to close"
  say "  [local, deletes] the scratch directory ($SCRATCH), which carries this run's marker file"
  say "Nothing was launched, read or cancelled: this dry run made no fugaro and no gcloud call at all."
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_belong
  guard_agent_session
  if [ -n "$DRY_RUN" ]; then
    fact installation PASS "$INSTALLATION (typed by the owner; FUGARO_PROJECT for every call this check makes)"
    fact checkout INFO "$PWD (a dry run reads nothing, so whether it is a fugaro checkout is not measured here)"
    fact dry-run PASS "guards and the plan only: no run was launched, nothing was read and nothing was cancelled, and no fugaro and no gcloud call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle.
    # It reached its end — the plan IS the end of a dry run — so the exit trap
    # must not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(installation checkout belong agent-session dry-run)
    return 0
  fi
  guard_checkout
  fact installation PASS "$INSTALLATION (typed by the owner; FUGARO_PROJECT for every call this check makes)"
  if [ -z "$YES" ]; then
    fact yes FAIL "the check was not given --yes: it launches two REAL runs of $PWD against $INSTALLATION, which are billable and may each open a pull request. Look at the plan with --dry-run, then run it again with --yes"
    stop "--yes was not given, and this check launches two real runs"
  fi
  step_launch
  step_cancel_active
  step_cancel_marker
  step_too_short
  step_refuses_piped
  step_pick_terminal
  step_logs
  step_old_run
  step_cancel_finished
  step_widen
  step_latency
  REACHED_END=yes
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
