#!/usr/bin/env bash
# Check 27 — `fugaro setup` end to end on a THROWAWAY project, as ONE script YOU
# run.
#
# `docs/gcp-live-checklist.md`'s "Check 27" section is the authority for this
# check and stays the place the whole check is written down, steps 1 to 22 with
# the recorded seventh live run below them. This script is how you run the part
# of it a script can: the wizard's create path with the typed questions fed
# through a real terminal, the negative create that must be refused, the
# read-backs that say what the run really built, and the Restore that deletes
# the throwaway project again. Everything the section says about the check holds
# here; what this script adds is only which of its steps are run for you.
#
# WHAT IT AUTOMATES (Check 27's own numbers):
#   - step 1 and step 2, the wizard's create path, in a directory that is NOT a
#     checkout: `fugaro setup --name fugaro --region us-east5 --budget-mode
#     observe --firebase-project <project> --link-billing <account>
#     --bake-repos`, at a real terminal, with the ONE question a person has to
#     answer — the GCP project's ID — typed at the pty this script gives it
#     (readValueIn's "Type the GCP project's ID: "). The project's own
#     confirmation banner, the billing banner and the Firestore location's
#     banner are read and nothing is typed under them (T7, D1540: being at the
#     terminal is what confirms those), which is exactly what a person sees.
#     `--link-billing` is the flag path to the same typed banner step 3's picker
#     ends at (step 12 of the section), and `--bake-repos` answers the one
#     input-phase question this check does not need an answer to: this run is
#     deliberately outside a checkout, so D5 skips the repository stage and
#     nothing is baked either way.
#   - step 2's deliberate NEGATIVE, on a second drawn id and with no terminal at
#     all (`fugaro setup --gcp-project <other-id> --name fugaro --yes` with
#     stdin at /dev/null): the refusal's exit code and text, and that the id
#     still does not exist, over a config root of its own so nothing it wrote
#     can reach the installation this check is building.
#   - step 11's `--plan-only`: the stage list prints, nothing is created, exit 0.
#   - step 4's converge and the two `fugaro update --project fugaro` runs: the
#     second must print `No changes` and exit 0.
#   - the read-backs the steps state as FACTs, each as its own row: the project
#     exists and is ACTIVE (step 2), the billing link names the account (step
#     12), the Firestore database's location is the region (step 3), the runs
#     bucket and the history job are there (step 4), Artifact Registry holds the
#     release's history image (step 14), and `fugaro doctor --project fugaro
#     --all` resolves THIS installation (the block's sixth stop, step 5 on).
#   - the Restore, from the exit trap as well: the jobs, the two buckets, the
#     secrets listed by name as EVIDENCE and never counted, and the project's
#     own `gcloud projects delete`.
#
# WHAT IT NEVER RUNS, because it is a person's or a browser's, and this script
# never fakes a step of it: step 3's interactive PICKER (the success path that
# lists your open accounts by number and picks one — the script takes the flag
# path to the same banner with `--link-billing`, and the picker's own answer is
# on the list the script prints for you); steps 5 to 10, the plugin wiring and
# the four Claude Code spikes (a browser, a folder trust dialog and the base
# image); step 14's digest comparison and image build; step 15's default
# accounts and the fix you run yourself; step 16's hidden secret pastes (a
# value is never supplied by a flag, a pipe or this script); step 17's hostile
# clone; step 18's `doctor` breakages; step 19's teammate and second machine;
# step 20's `/fugaro:setup`, the repository stage's billable typed build and the
# first cloud run; step 21's injection probe; step 22's dogfood. The script
# prints them as a list when it finishes, and records one `human` row saying so
# — a step it did not run is not a step it passed.
#
# The throwaway project is the whole of this check, so `--project` must NOT
# exist yet: a project that is already there is refused (with the
# `rm -rf`-and-draw-a-new-id answer), and that refusal is also what makes the
# Restore safe — everything inside the project is this run's, which is why the
# Restore may delete every job it finds rather than only the ones a repository's
# enrolment made. Running it twice is safe for the same reason: the second run
# stops at that guard and creates nothing.
#
# Usage:      bash scripts/live-checks/check27.sh --project <throwaway-id>
#               --billing-account <ID> [--name fugaro] [--region us-east5]
#               [--repo <owner>/<name>] [--dir /tmp/chk27] [--dry-run]
#             --project is the throwaway project this check creates and then
#             deletes: it must not exist when the run starts, and it must not
#             name the owner's own installations (see Where). --billing-account
#             is the account the project is linked to, an ID such as
#             0123AB-4567CD-89EF01; it is checked for shape and never printed
#             back into the block. --repo is the sandbox repository step 20 and
#             the Restore's own "delete the App" line are about: it is required
#             because the Restore's checklist needs to name it, and this script
#             never touches it (no step of this check clones or enrols it).
#             `--dry-run` prints every command a real run would make, runs the
#             guards that need no cloud call and runs nothing at all: no gcloud
#             call and no `fugaro` call is made.
# Needs:      bash, mktemp, grep, sed, python3 (the terminal the wizard is given
#             is a pty, and `python3` is what makes one), `gcloud` and a RELEASE
#             `fugaro` on PATH. The release is not decoration: the `images`
#             stage mirrors `ghcr.io/dimipaun/fugaro-history:<version>`, and a
#             `dev` build has no such tag, so a dev binary is refused before
#             anything is created. A missing tool stops the run with one line
#             naming it.
# Where:      a SANDBOX-only, throwaway GCP project of YOUR OWN, and NEVER an
#             installation named `belong` (the owner's own, on edge-devel-dimi),
#             `fugaro-dev` or `edge-devel-dimi`: `--project`, `--name` and
#             `--repo` are refused when they name one of those, case-insensitive
#             and as a substring, before a single call is made. The
#             installation's config root is this check's own
#             (`/tmp/chk27/config` unless `--dir` says otherwise), it is
#             refused before `XDG_CONFIG_HOME` is exported if the shell already
#             points somewhere else, and the machine's own installation is never
#             read or written.
# Creates:    one GCP project (`--project`), its Firebase and Firestore
#             database, the installation named `fugaro` on it (buckets, secret
#             containers, the history job) and the release's images in Artifact
#             Registry — all of it created by `fugaro setup`, none of it created
#             by this script, which only runs it and reads the result back. The
#             config root under `--dir` stays when the run ends, on purpose: the
#             section's own remaining steps (5 to 22) run against that
#             installation in the very shell you are in, and the one line to
#             remove it is printed with them.
# Costs:      a real project, and what a real installation costs (the images,
#             the job, the buckets): this is step 22's own acceptance path and
#             the section records its price. No model call is made: this script
#             never runs `fugaro run`.
# Runs:       by YOU, at your own terminal, in your own shell. NO AGENT RUNS ANY
#             STEP OF IT, and a coding agent's session is refused before
#             anything happens.
# Cleanup:    the Restore runs whenever the run ends, Ctrl-C and a failed
#             assertion included, and it deletes what THIS check's create run
#             made: every run job in the project, the two buckets the
#             installation made, and the project itself (`gcloud projects
#             delete --quiet`, which leaves it DELETE_REQUESTED for 30 days: a
#             project ID is never free again, so draw a new one for the next
#             run). It reads the jobs and the buckets back first and only an
#             empty answer counts. One case it cannot undo for you: a run
#             interrupted WHILE the create was running, before the create run
#             returned — the project may exist and nothing recorded creating it,
#             so the Restore reads it back and records FAIL with the one command
#             that deletes it (`gcloud projects delete <P> --quiet`), and running
#             that line is yours. The secret containers are listed and never
#             counted — they are the installation's own by design and die with
#             the project (the section's 3a) — and the scratch directory the
#             run's transcripts live in is removed with the run.
# Output:     progress on stderr, ONE block on stdout headed CHECK 27 RESULT with
#     `PASS` or `FAIL` on every fact, and the list of steps left for you above
#     it. Exit 0 when every criterion held AND the Restore confirmed what it
#     deleted, 1 when a criterion failed, 2 when the run could not tell (a guard
#     stopped it, a tool is missing, an internal error ended it, a run never
#     reached its verdict).
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports an abort without the line that caused it.
set -E

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
# redact TEXT: TEXT with the billing account's ID written as <ACCOUNT_ID>, the
# way the section writes the argument. This script quotes tool output — a
# `--plan-only` row, a billing read-back, a refusal — into its own lines and into
# the block, so ONE filter under each of those three printers is what makes "the
# ID is in the wizard's output and in nothing this script writes" true by
# construction rather than by hoping. The account is shape-checked before
# anything runs, so what reaches sed holds no metacharacter.
redact() {
  if [ -z "${A:-}" ]; then printf '%s' "$1"; return 0; fi
  printf '%s' "$1" | sed "s/$A/<ACCOUNT_ID>/g"
}
say() { printf '%s: %s\n' "$PROG" "$(redact "$*")" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$(redact "$*")" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
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
  printf '%s|%s|%s\n' "$1" "$2" "$(one_line "$(redact "$3")")" >>"$FACTS_FILE"
}

# one_line: a fact's detail is one line of the block, so whatever a command
# printed as several lines is squeezed into one before it is recorded. The full
# output stays in the script's own transcript, and the row says where.
one_line() { printf '%s' "$1" | tr '\n\r\t' '   ' | tr -s ' ' | sed 's/^ *//; s/ *$//' | cut -c1-500; }

# shown ARGS...: a command line as this script prints it. The billing account's
# ID stays out of it — the section's own text writes the same argument as
# <ACCOUNT_ID> — and redact() above is what keeps it out of every other line this
# script writes, its terminal lines and its block included. What the WIZARD
# prints is the wizard's own and is not redacted: a real `--plan-only` run draws
# the project stage's row as `link billing account <ID> (its own confirmation;
# may incur charges)` (init_project.go's plan detail), and the transcript keeps
# it and the runner echoes it to you exactly as a person at the terminal sees it.
shown() {
  local a prev="" out=""
  for a in "$@"; do
    if [ "$prev" = --link-billing ]; then a="<ACCOUNT_ID>"; fi
    out="$out $a"
    prev=$a
  done
  printf '%s' "${out# }"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the wizard for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the wizard"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 27 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 27 needs bash"
  need mktemp
  need grep
  need sed
  need python3 "the wizard's typed questions are answered at a real terminal, and the pty this script gives it is python3's"
  need gcloud "every read-back and the Restore are gcloud calls"
  need fugaro "the check is a run of the real CLI: nothing here works around it"
}

# The region and the Firestore location are one value in this check (the section
# says so: the two happen to hold the same value and are never derived from one
# another), and the third live run's own throwaway used us-east5.
REGION_DEFAULT=us-east5
# The installation's name: the plan's own allocation for this check, and the
# name every later --project flag resolves. Nothing else is accepted.
NAME_FIXED=fugaro
# The names this check never touches, as a case-insensitive substring (check340's
# own rule) and as the exact list the section's guard block refuses.
PROTECTED="belong fugaro-dev edge-devel-dimi fugaro-belong"
# The agent markers internal/agentenv reads. A shell carrying one is a coding
# agent's session and is refused: this check creates a real GCP project.
AGENT_MARKERS="CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT"

# How long each run is given before it is killed. The create path is the long one
# (a whole installation: Terraform, the images, the job), the converge runs are
# shorter, and the two runs that answer or refuse at once are shortest. They are
# knobs for this script's own test, and none of them can point the check at
# another project.
PLAN_SECONDS=${CHECK27_PLAN_SECONDS:-900}
CREATE_SECONDS=${CHECK27_CREATE_SECONDS:-3600}
CONVERGE_SECONDS=${CHECK27_CONVERGE_SECONDS:-1800}
QUICK_SECONDS=${CHECK27_QUICK_SECONDS:-600}

XTRACE_AT_START=no
case "$-" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk27-mine"
SCRATCH_REMOVED=no
PRINTED=""
# --help prints the usage and makes nothing: it is not a run, so the exit trap
# gives it no block and no verdict. Declared here because the trap reads it
# whether or not the arguments ever got as far as parsing.
HELPED=no
FACTS_CAPTURED=""
RUNNER=""
# The wizard's own run of the CLI, its two transcripts and the runs around it.
WIZARD_DIR=""
NEG_DIR=""
PLAN_LOG=""
CREATE_LOG=""
NEG_LOG=""
PLAN_STATUS=0
NEG_STATUS=0
CREATE_STATUS=0
UPDATE_STATUS=0
UPDATE2_STATUS=0
UPDATE_LOG=""
UPDATE2_LOG=""
DOCTOR_LOG=""
DOCTOR_STATUS=0
VERSION_STATUS=0
VERSION_LOG=""
# Which transcript the run currently being made writes to (the runner reads it).
LOG_FOR_MODE=""
# Whether the run got as far as creating, which is what the Restore keys on.
CREATED=no
LIFECYCLE=""
# Whether the run made ANY call at all: a run a guard refused before its first
# call has no cloud state to undo, and the Restore then makes no call either —
# which is also what keeps `--project belong` from reading the owner's own
# project while it is being refused.
TOUCHED=no

rand_hex() { printf '%04x' "$RANDOM"; }

# The scratch directory holds every transcript and the two config roots the runs
# that must not touch the installation are given. It is NEVER inside a git work
# tree: this check runs the wizard in a directory that is not a checkout (D5),
# and a scratch inside one would make the run a checkout of somebody's
# repository and change what the wizard asks.
scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk27.XXXXXXXX") || die "Check 27 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 27 stopped: $SCRATCH is inside a git work tree, and the wizard must run in a directory that is not a checkout — set TMPDIR outside every repository and run again"
  fi
  WIZARD_DIR="$SCRATCH/wizard"
  NEG_DIR="$SCRATCH/negative"
  mkdir -p "$WIZARD_DIR" "$NEG_DIR"
  RUNNER="$SCRATCH/run.py"
  PLAN_LOG="$SCRATCH/plan-only.log"
  CREATE_LOG="$SCRATCH/create.log"
  NEG_LOG="$SCRATCH/negative.log"
  UPDATE_LOG="$SCRATCH/update-1.log"
  UPDATE2_LOG="$SCRATCH/update-2.log"
  DOCTOR_LOG="$SCRATCH/doctor.log"
  VERSION_LOG="$SCRATCH/version.log"
  # The second id the negative is drawn on, so a refusal can never land on the
  # project this check is about. It is drawn from the project's own name, which
  # keeps it recognisable in the account's logs, and cut to the 30 characters a
  # project ID holds.
  OTHER="$(printf '%s' "$P" | cut -c1-18)-neg-$(rand_hex)"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  say "this check's own config root: $C (kept when the run ends, for the section's remaining steps)"
  say "the negative create is drawn on the unused id $OTHER"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell. The config root is deliberately NOT part of it (see
# Creates in the header).
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
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before the
  # block is printed.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------ the terminal of a run
# The wizard's typed questions are read only where a person can type, so the
# runs that have to answer one are given a REAL pty — the same helper
# Check 111's script uses, with this check's own names in it. `--kind plain` is
# the other half: an untyped run with stdin at /dev/null, which is what the
# negative and the converge runs are.
write_runner() {
  cat >"$RUNNER" <<'RUNNER_EOF'
#!/usr/bin/env python3
"""Check 27's runner: one child, a pty or no terminal, its own transcript.

  run.py --mode NAME --kind pty --out FILE [--echo FD] [--timeout SECONDS]
         [--expect TEXT --answer TEXT]... -- CMD [ARG...]

--kind pty runs CMD on a pty, so the child sees a real terminal on stdin, stdout
and stderr; --kind plain runs it with stdin at /dev/null, no pty at all. Every
answer is written only after its own --expect text has appeared in the child's
output, in order, so the run is answered where the mode expects to be asked and
never earlier — each answer as a line of its own, the text plus its Enter. On the
timeout the child's process group is killed, a CHECK27-RUNNER line is appended to
the transcript and the exit status is 124. The transcript is echoed to --echo (the
shell's fd 4, the owner's stderr) as it arrives, so the run is watched live while
the RESULT block stays clean.
"""
import os
import select
import signal
import subprocess
import sys
import time

KEEP = 65536


def parse(argv):
    o = {"mode": None, "kind": None, "out": None, "echo": None,
         "timeout": 600.0, "expect": [], "answer": [], "cmd": []}
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--":
            o["cmd"] = argv[i + 1:]
            break
        if a in ("--mode", "--kind", "--out", "--echo", "--timeout",
                 "--expect", "--answer"):
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
                o[a[2:]] = v
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


def run_pty(o, echo):
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
            # The answer is a LINE, so it is written with its Enter: a prompt
            # reads that way, and a terminal in canonical mode hands the child
            # nothing until the newline arrives — an answer missing it would look
            # exactly like a prompt nobody answered. An EMPTY answer is the empty
            # line, which is what the wizard's suggestion asks for where a
            # question has one.
            try:
                os.write(fd, (text + "\n").encode())
            except OSError:
                pass
    if timed_out:
        try:
            os.killpg(pid, signal.SIGKILL)
        except OSError:
            pass
    status = wait_status(pid)
    if timed_out:
        note = ("\nCHECK27-RUNNER: TIMEOUT: the run was killed after %d s\n"
                % int(o["timeout"]))
        write_out(log, echo, note.encode())
        log.close()
        return 124
    if pending:
        note = ("\nCHECK27-RUNNER: the prompt this run was to answer never "
                "appeared: %s\n" % "; ".join(p for p, _ in pending))
        write_out(log, echo, note.encode())
    log.close()
    return status


def run_plain(o, echo):
    out = open(o["out"], "wb", buffering=0)
    with open(os.devnull, "rb") as devnull:
        proc = subprocess.Popen(o["cmd"], stdin=devnull, stdout=out,
                                stderr=out, start_new_session=True)
    try:
        status = proc.wait(timeout=o["timeout"])
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except OSError:
            pass
        proc.wait()
        note = ("\nCHECK27-RUNNER: TIMEOUT: the run was killed after %d s\n"
                % int(o["timeout"])).encode()
        write_out(out, echo, note)
        status = 124
    out.close()
    return status


def main():
    o = parse(sys.argv[1:])
    if not o["mode"] or not o["kind"] or not o["out"] or not o["cmd"]:
        sys.stderr.write("run.py: --mode, --kind, --out and a command are required\n")
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
    if o["kind"] == "pty":
        return run_pty(o, echo)
    if o["kind"] == "plain":
        return run_plain(o, echo)
    sys.stderr.write("run.py: --kind is pty or plain, not %r\n" % o["kind"])
    return 2


sys.exit(main())
RUNNER_EOF
  chmod 700 "$RUNNER"
}

# run_pty MODE SECONDS DIR [PROMPT ANSWER]... -- CMD...: a TYPED run, so the
# wizard sees a real terminal (canType) and its question is answered where the
# mode expects it. The transcript carries the prompts, the answers and the echo,
# and is echoed to you live.
run_pty() {
  local m=$1 secs=$2 dir=$3
  shift 3
  local answers=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    answers+=(--expect "$1" --answer "$2")
    shift 2
  done
  if [ $# -gt 0 ]; then shift; fi
  say "mode $m: at a real terminal (a pty), in $dir. running: $(shown "$@")"
  LAST_STATUS=0
  (cd "$dir" && python3 "$RUNNER" --mode "$m" --kind pty --echo 4 --timeout "$secs" \
    --out "$LOG_FOR_MODE" ${answers[@]+"${answers[@]}"} -- "$@") || LAST_STATUS=$?
}

# run_plain MODE SECONDS DIR LOG -- CMD...: an UNTYPED run — no pty at all,
# standard input at /dev/null, both streams into LOG — which is what the
# negative and the converge runs are.
run_plain() {
  local m=$1 secs=$2 dir=$3 log=$4
  shift 4
  [ "${1:-}" = "--" ] && shift
  say "mode $m: with NO terminal (standard input at /dev/null), in $dir. running: $(shown "$@")"
  LAST_STATUS=0
  (cd "$dir" && python3 "$RUNNER" --mode "$m" --kind plain --timeout "$secs" \
    --out "$log" -- "$@") || LAST_STATUS=$?
}

# -------------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print this run's command lines into a log you might paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check27.sh --project <throwaway> --billing-account <ID> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing"
}

# guard_agent: the check creates a real GCP project and answers the wizard's
# typed questions, so it is a person's run and nothing else. The markers are the
# ones internal/agentenv reads; CLAUDE_CODE_SSE_PORT alone is NOT one of them
# (the IDE extension sets it in a person's own terminal too), and it is recorded
# rather than refused — the pty runs then look to the CLI like a person at an
# IDE terminal, which is what they are.
guard_agent() {
  local marker found=""
  for marker in $AGENT_MARKERS; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  if [ -n "$found" ]; then
    fact agent-session FAIL "this shell carries $found, so it is a coding agent's session: this check creates a real GCP project and answers the wizard's typed questions, and an agent never runs it"
    stop "Check 27 refuses to run in a coding agent's session ($found is set). Run it yourself at your own terminal: bash scripts/live-checks/check27.sh --project <throwaway> --billing-account <ID>"
  fi
  if [ -n "${CLAUDE_CODE_SSE_PORT:-}" ]; then
    fact agent-session PASS "no agent marker is set; CLAUDE_CODE_SSE_PORT is (the IDE extension sets it in a person's own terminal too), which the CLI reads as a person's terminal when stdin is one — and a pty is what every typed run here is given"
  else
    fact agent-session PASS "no agent marker is set, so this is a person's shell"
  fi
}

# guard_protected NAME VAR: the names this check never touches. `belong` is the
# owner's own installation (on edge-devel-dimi) and the others are the
# installations the register's rows run on; the section's own guard block
# refuses them in the project, and this check creates a project, a Firebase
# installation and a billing link, so nothing may be named one. The test is a
# case-insensitive substring, as Check 340's is: a name that merely contains one
# is refused too.
guard_protected() { # guard_protected VALUE VAR
  local value=$1 var=$2 low
  [ -n "$value" ] || stop "$var is empty: $var must name something of your own. Nothing was run"
  low=$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')
  local name
  for name in $PROTECTED; do
    case "$low" in
    *"$name"*)
      fact belong FAIL "'$value' ($var) names a project or installation this check must never touch: $PROTECTED"
      stop "$var=$value matches the protected name '$name', which this check never touches. Name your own throwaway project. Nothing was run"
      ;;
    esac
  done
}

guard_names() {
  guard_protected "$P" "--project"
  guard_protected "$N" "--name"
  guard_protected "$R" "--repo"
  [ "$N" = "$NAME_FIXED" ] || stop "--name must be $NAME_FIXED: this check sets up the installation the plan allocates to it, and every later --project flag resolves that name (it was $N). Nothing was run"
  [ "$P" != "$OTHER" ] || stop "the negative's own id ($OTHER) must not be --project. Nothing was run"
  fact belong PASS "none of the typed names ($P, $N, $R) names a protected installation (nothing contains: $PROTECTED)"
  fact identity PASS "--name $N (the plan's own allocation for this check) and --repo $R (the sandbox the section's step 20 and its Restore's App line are about; this script never touches it)"
}

# guard_account: the billing account's shape, before anything is created. An ID
# a person would recognise is 6-6-6 of uppercase letters and digits; it is
# checked here and NEVER recorded in the block, which is the one thing a reader
# of the block should not have to redact.
guard_account() {
  case "$A" in
  [0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z]-[0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z]-[0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z][0-9A-Z]) ;;
  *) stop "--billing-account must be an account ID such as 0123AB-4567CD-89EF01 (it was refused: nothing of that shape). Nothing was run" ;;
  esac
  fact billing-account PASS "the account ID you gave has the shape of one, and it is NOT repeated in this block: the link the run makes is read back from the project itself"
}

# guard_config_root: the installation's config root is this check's own, and the
# machine's own installations are never read or written. The point is the FIRST
# test, which runs BEFORE XDG_CONFIG_HOME is exported: a shell that already
# points somewhere else — the owner's real ~/.config, or another check's root —
# is refused, and after the export the same stop could never fire. That is the
# section's own guard block, and this script records the same C in the same
# vars.env so the steps you run by hand afterwards resolve the same root.
guard_config_root() {
  case "$C" in
  "$DIR"/*) ;;
  *) stop "the config root ($C) is not under this check's own directory ($DIR): this check never writes the machine's own config root. Nothing was run" ;;
  esac
  # A vars.env left from an earlier attempt is READ BACK rather than overwritten:
  # the section's own third stop is a hand-edited or tampered file that would
  # point the check at the machine's own ~/.config, and a silent overwrite would
  # take that stop's meaning away.
  if [ -f "$DIR/vars.env" ]; then
    local recorded
    recorded=$(sed -n 's/^C=//p' "$DIR/vars.env" | head -1)
    if [ -n "$recorded" ] && [ "$recorded" != "$C" ]; then
      stop "$DIR/vars.env records C=$recorded, which is not this check's own root ($C): delete $DIR (rm -rf $DIR) and start again. Nothing was run"
    fi
  fi
  if [ -n "${XDG_CONFIG_HOME:-}" ] && [ "$XDG_CONFIG_HOME" != "$C" ]; then
    stop "this shell's XDG_CONFIG_HOME ($XDG_CONFIG_HOME) is not this check's root ($C). Unset it, or start the check in a fresh shell: the machine's own installations must never be read. Nothing was run"
  fi
  mkdir -p "$DIR" "$C"
  chmod 700 "$DIR"
  printf 'C=%s\n' "$C" >"$DIR/vars.env"
  . "$DIR/vars.env"
  [ "${C:-}" = "$DIR/config" ] || stop "the config root recorded in $DIR/vars.env is not this check's own ($C): delete $DIR and start again. Nothing was run"
  export XDG_CONFIG_HOME="$C"
  [ "$XDG_CONFIG_HOME" = "$C" ] || stop "the run did not leave XDG_CONFIG_HOME at this check's own root (it is $XDG_CONFIG_HOME)"
  fact config-root PASS "$C (this check's own, recorded in $DIR/vars.env so the section's later steps resolve it: set CHK27=$DIR in the shell you run them in). The machine's own installations are never read"
}

# guard_version: a RELEASE binary is part of the check's premises, because the
# `images` stage mirrors `ghcr.io/dimipaun/fugaro-history:<version>` and a `dev`
# build has no such tag to mirror. It is read with `fugaro version`, which prints
# the version and nothing else.
guard_version() {
  VERSION_STATUS=0
  V=$(fugaro version 2>&1) || VERSION_STATUS=$?
  printf '%s\n' "$V" >"$VERSION_LOG"
  if [ "$VERSION_STATUS" -ne 0 ]; then
    fact version FAIL "fugaro version exited $VERSION_STATUS: a release fugaro on PATH is what this check runs"
    stop "fugaro version exited $VERSION_STATUS: $(one_line "$V"). Install the release (brew upgrade fugaro) and run again"
  fi
  case "$(one_line "$V")" in
  "" | dev* | *-dev | *dirty*)
    fact version FAIL "fugaro version printed '$(one_line "$V")': a dev build has no ghcr.io/dimipaun/fugaro-history:<version> tag, so the images stage has nothing to mirror"
    stop "this fugaro is not a release build (fugaro version printed '$(one_line "$V")'). Install the release and run again: the check mirrors a release's images"
    ;;
  esac
  fact version PASS "fugaro version $(one_line "$V") — a release build, which is what the images stage mirrors"
}

# guard_project_absent: the throwaway project must NOT exist yet. This check is
# the create path; a project that is already there is either an earlier run's
# (delete it, or draw a new id — a deleted project's ID is never free again) or
# somebody's installation, and neither is this check's to converge into. The read
# is a read: gcloud answers a project this identity cannot see the same way it
# answers one that is not there, and both mean this script may not create it.
guard_project_absent() {
  local out status=0
  if out=$(gcloud projects describe "$P" --format='value(projectId,lifecycleState)' 2>&1); then
    fact project-absent FAIL "the project $P already exists ($(one_line "$out")): this check creates its project and then deletes it, and it never adopts one"
    stop "$P already exists. This check is the create path: delete it first (the Restore's own 'gcloud projects delete $P --quiet'), or point --project at an id that has never been used — a deleted project's ID is never free again"
  else
    status=$?
  fi
  fact project-absent PASS "$P does not exist yet (gcloud projects describe answered $(one_line "$out")): the project this run creates is its own"
}

# --------------------------------------------------------------- the readings
# read_out: one guarded command whose stdout and stderr go to READ_OUT and whose
# status goes to READ_STATUS. Nothing here dies on a failing read — that is what
# every row below records — and no call is made outside this shape.
READ_OUT=""
READ_STATUS=0
read_out() {
  READ_STATUS=0
  if READ_OUT=$("$@" 2>&1); then
    READ_STATUS=0
  else
    READ_STATUS=$?
  fi
}

# rows_of TEXT: TEXT's non-empty lines.
rows_of() { printf '%s\n' "$1" | grep -v '^[[:space:]]*$' || true; }

# --------------------------------------------------------------- the steps
# step_plan_only: Check 27's step 11 — the same create path with --plan-only: the
# stage list prints, nothing is created, exit 0. Every input is a flag, so the
# plan is printed without a question; the pty is what --plan-only itself needs
# (it never takes --yes, since it creates nothing on a flag's word).
step_plan_only() {
  LOG_FOR_MODE=$PLAN_LOG
  run_pty plan-only "$PLAN_SECONDS" "$WIZARD_DIR" -- \
    fugaro setup --create-project --gcp-project "$P" --name "$N" --region "$REGION" \
    --firebase-project "$P" --budget-mode observe --link-billing "$A" --bake-repos --plan-only
  PLAN_STATUS=$LAST_STATUS
  local named=0 s
  for s in installation firebase images installation-2 secrets plugin; do
    grep -q "$s" "$PLAN_LOG" 2>/dev/null && named=$((named + 1))
  done
  if [ "$PLAN_STATUS" -eq 124 ] || grep -q 'CHECK27-RUNNER: TIMEOUT' "$PLAN_LOG" 2>/dev/null; then
    fact plan-only FAIL "the --plan-only run did not finish in ${PLAN_SECONDS}s and was killed: it blocked waiting for an answer a plan-only run must not need. Transcript: $PLAN_LOG"
    stop "the --plan-only run blocked (transcript $PLAN_LOG): a plan-only run takes every input from its flags and creates nothing, so a wait in it is a finding"
  fi
  if [ "$PLAN_STATUS" -ne 0 ]; then
    fact plan-only FAIL "--plan-only exited $PLAN_STATUS (step 11 expects 0): $(one_line "$(tail -n 5 "$PLAN_LOG" 2>/dev/null)")"
    stop "--plan-only exited $PLAN_STATUS, which step 11 says must be 0. Transcript: $PLAN_LOG"
  fi
  read_out gcloud projects describe "$P" --format='value(projectId)'
  if [ "$READ_STATUS" -eq 0 ]; then
    fact plan-only FAIL "--plan-only exited 0 and the project exists anyway ($(one_line "$READ_OUT")): a plan-only run creates nothing"
    stop "the --plan-only run created the project, which it must never do (transcript $PLAN_LOG)"
  fi
  if [ "$named" -lt 3 ]; then
    fact plan-only FAIL "exit 0 and nothing created, but the transcript names only $named of the six stages a plan prints (installation, firebase, images, installation-2, secrets, plugin): $PLAN_LOG"
    stop "the --plan-only run printed no stage list (transcript $PLAN_LOG): step 11's whole subject is the stages it prints"
  fi
  local cfg=""
  cfg=$(find "$C" -name '*.yaml' -print 2>/dev/null | head -5 || true)
  fact plan-only PASS "exit 0, $named of the six stages named, and the project still does not exist. A config file left behind: ${cfg:-none} (the create run resumes from one when it is there; a plan-only run that wrote one is worth reading)"
}

# step_negative: Check 27's step 2 deliberate negative — a second unused id, no
# terminal at all, and --yes: `fugaro setup --gcp-project <other> --name fugaro
# --yes` must refuse (project creation is typed and --yes never covers it),
# create nothing and link nothing. Its config root is its own, so whatever it
# wrote cannot reach the installation this check is building.
step_negative() {
  local negroot="$SCRATCH/negative-config"
  mkdir -p "$negroot"
  step_negative_run "$negroot"
  read_out gcloud projects describe "$OTHER" --format='value(projectId)'
  if [ "$READ_STATUS" -eq 0 ]; then
    fact negative FAIL "the negative's own id $OTHER EXISTS ($(one_line "$READ_OUT")): the run that must create nothing created a project"
    stop "$OTHER exists after a run that was to create nothing (transcript $NEG_LOG). Delete it yourself: gcloud projects delete $OTHER --quiet"
  fi
  if [ "$NEG_STATUS" -eq 0 ]; then
    fact negative FAIL "the run exited 0: nothing was created, but a create with no terminal must be REFUSED rather than pass silently ($NEG_LOG)"
    stop "the negative run exited 0 (transcript $NEG_LOG): step 2 expects a refusal, since the project's creation is typed and --yes never covers it"
  fi
  local left=""
  left=$(find "$negroot" -type f -print 2>/dev/null | head -3 | tr '\n' ' ' || true)
  fact negative PASS "exit $NEG_STATUS (a refusal), $OTHER does not exist, and it linked nothing. Its own config root holds: ${left:-nothing} — $(one_line "$(grep -m1 -iE 'refus|error|terminal|type' "$NEG_LOG" 2>/dev/null)")"
}

step_negative_run() {
  local negroot=$1
  LOG_FOR_MODE=$NEG_LOG
  run_plain negative "$QUICK_SECONDS" "$NEG_DIR" "$NEG_LOG" -- \
    env "XDG_CONFIG_HOME=$negroot" fugaro setup --gcp-project "$OTHER" --name "$N" --yes
  NEG_STATUS=$LAST_STATUS
  if [ "$NEG_STATUS" -eq 124 ] || grep -q 'CHECK27-RUNNER: TIMEOUT' "$NEG_LOG" 2>/dev/null; then
    fact negative FAIL "the negative run did not finish in ${QUICK_SECONDS}s and was killed: with no terminal it must refuse at once rather than wait for a person ($NEG_LOG)"
    stop "the negative run blocked instead of refusing (transcript $NEG_LOG): with stdin at /dev/null there is no one to answer it"
  fi
}

# step_create: Check 27's steps 1 to 4 — the wizard, at a real terminal, in a
# directory that is NOT a checkout. ONE question is asked of a person (the GCP
# project's ID, readValueIn's typed read) and this script's pty answers it with
# the same id the block's --project is; the confirmation banners are read and
# nothing is typed under them, which is what a person at the terminal does too.
# `--link-billing` is step 12's flag path to the banner step 3's picker ends at,
# and `--bake-repos` answers the one input-phase question this run does not need
# (no checkout, so D5 skips the repository stage and nothing is baked).
step_create() {
  LOG_FOR_MODE=$CREATE_LOG
  run_pty create "$CREATE_SECONDS" "$WIZARD_DIR" \
    "Type the GCP project's ID" "$P" -- \
    fugaro setup --name "$N" --region "$REGION" --budget-mode observe \
    --firebase-project "$P" --link-billing "$A" --bake-repos
  CREATE_STATUS=$LAST_STATUS
  if [ "$CREATE_STATUS" -eq 124 ] || grep -q 'CHECK27-RUNNER: TIMEOUT' "$CREATE_LOG" 2>/dev/null; then
    fact wizard FAIL "the create run did not finish in ${CREATE_SECONDS}s and was killed: something in it waited for an answer this check cannot give. Transcript: $CREATE_LOG"
    stop "the create run blocked and was killed (transcript $CREATE_LOG): read it for the prompt that was waiting, and report it — the wizard asks one question of a person and this script answers that one"
  fi
  if grep -q 'CHECK27-RUNNER: the prompt this run was to answer never appeared' "$CREATE_LOG" 2>/dev/null; then
    fact wizard FAIL "the create run finished without ever asking for the GCP project's ID: the question this check's step 1 is about was not asked ($CREATE_LOG)"
    stop "the create run never asked for the project ID (transcript $CREATE_LOG): step 1's subject is that question"
  fi
  if [ "$CREATE_STATUS" -ne 0 ]; then
    local first
    first=$(grep -m1 -E 'ERROR|error|refus|CONFIRM' "$CREATE_LOG" 2>/dev/null || true)
    fact wizard FAIL "the create run exited $CREATE_STATUS: $(one_line "${first:-$(tail -n 3 "$CREATE_LOG" 2>/dev/null)}")"
    stop "the create run exited $CREATE_STATUS (transcript $CREATE_LOG). The project may be half-created: the Restore below deletes what is there"
  fi
  CREATED=yes
  local suggested
  suggested=$(grep -m1 -o "the GCP project's ID \[[^]]*\]" "$CREATE_LOG" 2>/dev/null || true)
  fact wizard PASS "exit 0, with the project's ID typed at a real terminal and the id it proposed recorded: $(one_line "${suggested:-the run printed no suggestion line — read $CREATE_LOG}")"
}

# step_created: the project itself, read back from gcloud (step 2's FACT).
step_created() {
  read_out gcloud projects describe "$P" --format='value(projectId,lifecycleState)'
  [ "$READ_STATUS" -eq 0 ] || {
    fact created FAIL "gcloud projects describe $P failed ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the project the create run says it made cannot be read back: $(one_line "$READ_OUT"). Transcript: $CREATE_LOG"
  }
  local id state
  id=$(printf '%s\n' "$READ_OUT" | head -1 | cut -f1)
  state=$(printf '%s\n' "$READ_OUT" | head -1 | cut -f2)
  [ "$id" = "$P" ] || {
    fact created FAIL "gcloud names the project '$id' and not $P"
    stop "the project read back is not the one this check asked for ('$id' vs $P)"
  }
  case "$state" in
  ACTIVE) ;;
  *)
    fact created FAIL "the project's lifecycleState is '${state:-empty}' and not ACTIVE"
    stop "the project $P is not ACTIVE (lifecycleState ${state:-empty}): the converge wrote into something that cannot be used"
    ;;
  esac
  fact created PASS "$P exists and reads ACTIVE"
}

# step_billing: the billing link, read from the project (step 12's FACT). The
# account ID is not printed into the block; the account the link NAMES is
# compared with the one you gave, so a wrong link is a FAIL here.
step_billing() {
  read_out gcloud billing projects describe "$P" --format='value(billingAccountName,billingEnabled)'
  [ "$READ_STATUS" -eq 0 ] || {
    fact billing FAIL "gcloud billing projects describe $P failed ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the billing link cannot be read back: $(one_line "$READ_OUT")"
  }
  local named enabled
  named=$(printf '%s\n' "$READ_OUT" | head -1 | cut -f1)
  enabled=$(printf '%s\n' "$READ_OUT" | head -1 | cut -f2)
  if [ "$named" = "billingAccounts/$A" ] && [ "$enabled" = "True" ]; then
    fact billing PASS "the project's billing link is the account you named on the command line, and billingEnabled reads True"
    return 0
  fi
  fact billing FAIL "the project's billing link reads '$named' and billingEnabled '$enabled': the flag path did not link the account it was given"
  stop "the billing link is not the account --billing-account named ('$named'): read the create run's transcript at $CREATE_LOG"
}

# step_firestore: the Firestore database and its PERMANENT location (step 3's
# last input-phase answer, and the one that can never be changed).
step_firestore() {
  read_out gcloud firestore databases describe --database='(default)' --project="$P" --format='value(locationId)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact firestore FAIL "the (default) Firestore database cannot be read ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the Firestore database the run was to create is not there or cannot be read: $(one_line "$READ_OUT")"
  fi
  local loc
  loc=$(printf '%s\n' "$READ_OUT" | head -1)
  if [ "$loc" = "$REGION" ]; then
    fact firestore PASS "the (default) database's locationId is $REGION, the region this run converged"
    return 0
  fi
  fact firestore FAIL "the (default) database's locationId reads '${loc:-empty}' and not $REGION: the location is permanent, so this is a finding for the section, not a retry"
  stop "the Firestore location is '${loc:-empty}' and not $REGION"
}

# step_installation: the runs bucket and the history job the converge was to
# make, plus the doctor read that says THIS machine resolves THIS installation
# (the section's sixth stop, from step 5 on).
step_installation() {
  read_out gcloud storage buckets list --project="$P" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact installation FAIL "the project's buckets cannot be listed ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the installation's buckets cannot be read back: $(one_line "$READ_OUT")"
  fi
  if ! grep -qxF "fugaro-runs-$P" <<<"$READ_OUT"; then
    fact installation FAIL "no fugaro-runs-$P bucket in the project: $(one_line "$(rows_of "$READ_OUT" | tr '\n' ' ')")"
    stop "the installation's runs bucket (fugaro-runs-$P) is not in the project: the converge did not install"
  fi
  read_out gcloud run jobs list --project="$P" --region="$REGION" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact installation FAIL "the project's run jobs cannot be listed in $REGION ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the installation's jobs cannot be read back: $(one_line "$READ_OUT")"
  fi
  if ! grep -qxF 'fugarohist' <<<"$READ_OUT"; then
    fact installation FAIL "no fugarohist job in $REGION: $(one_line "$(rows_of "$READ_OUT" | tr '\n' ' ')")"
    stop "the history job (fugarohist) is not deployed in $REGION: the installation-2 stage did not finish"
  fi
  fact installation PASS "fugaro-runs-$P is in the project and the fugarohist job is deployed in $REGION"
}

# step_doctor: `fugaro doctor --project fugaro --all`, which is the section's
# sixth stop and the check's own read that the installation resolves to THIS
# project. An exit that is not 0 fails it.
step_doctor() {
  LOG_FOR_MODE=$DOCTOR_LOG
  run_plain doctor "$QUICK_SECONDS" "$WIZARD_DIR" "$DOCTOR_LOG" -- \
    fugaro doctor --project "$N" --all
  DOCTOR_STATUS=$LAST_STATUS
  if [ "$DOCTOR_STATUS" -eq 124 ] || grep -q 'CHECK27-RUNNER: TIMEOUT' "$DOCTOR_LOG" 2>/dev/null; then
    fact doctor FAIL "fugaro doctor did not finish in ${QUICK_SECONDS}s and was killed ($DOCTOR_LOG)"
    stop "fugaro doctor blocked (transcript $DOCTOR_LOG): it asks nothing, so a wait in it is a finding"
  fi
  local line
  line=$(grep -m1 "(GCP $P," "$DOCTOR_LOG" 2>/dev/null || true)
  if [ -z "$line" ]; then
    fact doctor FAIL "doctor's project line does not name (GCP $P,: $(one_line "$(grep -m1 -i 'project' "$DOCTOR_LOG" 2>/dev/null)")"
    stop "fugaro doctor --project $N does not resolve the throwaway project ($P): the section's sixth stop is exactly this read. Transcript: $DOCTOR_LOG"
  fi
  if [ "$DOCTOR_STATUS" -ne 0 ]; then
    fact doctor FAIL "doctor exits $DOCTOR_STATUS on a fresh installation, and the project line is $(one_line "$line"): $(one_line "$(grep -m1 -iE 'fail|warn|error' "$DOCTOR_LOG" 2>/dev/null)")"
    stop "fugaro doctor exits $DOCTOR_STATUS on the installation this run just converged (transcript $DOCTOR_LOG): step 15's own subject is the fix line it prints, and this one is not a pass"
  fi
  fact doctor PASS "exit 0, and its project line names the throwaway one: $(one_line "$line")"
}

# step_images: Artifact Registry holds the release's history image (step 14's
# mirror, read from the registry rather than from docker).
step_images() {
  read_out gcloud artifacts docker images list "$REGION-docker.pkg.dev/$P/fugaro-base" \
    --include-tags --format='value(package,tags)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact images FAIL "the installation's Artifact Registry repository cannot be listed ($READ_STATUS): $(one_line "$READ_OUT")"
    stop "the fugaro-base repository cannot be read back: $(one_line "$READ_OUT")"
  fi
  if ! grep -q 'history' <<<"$READ_OUT"; then
    fact images FAIL "no history image in $REGION-docker.pkg.dev/$P/fugaro-base: $(one_line "$(rows_of "$READ_OUT" | tr '\n' ' ')")"
    stop "the images stage mirrored no history image into the project's own registry: the history job has nothing to pull"
  fi
  fact images PASS "the release's history image is in $REGION-docker.pkg.dev/$P/fugaro-base: $(one_line "$(grep 'history' <<<"$READ_OUT" | tr '\n' ' ')")"
}

# step_update: Check 27's step 4 — `fugaro update --project fugaro`, twice. The
# first converges whatever is left; the SECOND is the assertion: every stage
# `No changes` and exit 0. Both run with no terminal at all, which is also the
# read that nothing is left to ask (a secret is the one thing a converge may
# leave for you, and this run has no checkout, so the section's step 16 is not
# reached here).
step_update() {
  LOG_FOR_MODE=$UPDATE_LOG
  run_plain update-1 "$CONVERGE_SECONDS" "$WIZARD_DIR" "$UPDATE_LOG" -- \
    fugaro update --project "$N"
  UPDATE_STATUS=$LAST_STATUS
  LOG_FOR_MODE=$UPDATE2_LOG
  run_plain update-2 "$CONVERGE_SECONDS" "$WIZARD_DIR" "$UPDATE2_LOG" -- \
    fugaro update --project "$N"
  UPDATE2_STATUS=$LAST_STATUS
  local t
  for t in "$UPDATE_STATUS" "$UPDATE2_STATUS"; do
    if [ "$t" -eq 124 ]; then
      fact update FAIL "a fugaro update run did not finish in ${CONVERGE_SECONDS}s and was killed"
      stop "a fugaro update run blocked and was killed (transcripts $UPDATE_LOG and $UPDATE2_LOG): with no terminal it must refuse or converge, never wait"
    fi
  done
  if [ "$UPDATE_STATUS" -ne 0 ]; then
    fact update FAIL "the first fugaro update exited $UPDATE_STATUS: $(one_line "$(tail -n 3 "$UPDATE_LOG" 2>/dev/null)")"
    stop "the first fugaro update exited $UPDATE_STATUS (transcript $UPDATE_LOG)"
  fi
  if [ "$UPDATE2_STATUS" -ne 0 ]; then
    fact update FAIL "the second fugaro update exited $UPDATE2_STATUS: $(one_line "$(tail -n 3 "$UPDATE2_LOG" 2>/dev/null)")"
    stop "the second fugaro update exited $UPDATE2_STATUS (transcript $UPDATE2_LOG): step 4's assertion is that it prints No changes for every stage and exits 0"
  fi
  local changes
  changes=$(grep -c 'No changes' "$UPDATE2_LOG" 2>/dev/null || true)
  if [ "${changes:-0}" -eq 0 ]; then
    fact update FAIL "the second fugaro update printed no 'No changes' line at all: this is the one assertion step 4 makes, and a converge that finds work on a converged installation has not converged it ($UPDATE2_LOG)"
    stop "the second fugaro update printed no 'No changes' ($UPDATE2_LOG): step 4's assertion did not hold"
  fi
  if grep -qE 'will do|applying|Creating |Updating ' "$UPDATE2_LOG" 2>/dev/null; then
    fact update FAIL "the second fugaro update still names work it would do ($(one_line "$(grep -m1 -E 'will do|applying|Creating |Updating ' "$UPDATE2_LOG" 2>/dev/null)")): the first run did not converge everything"
    stop "the second fugaro update still has work to do ($UPDATE2_LOG): step 4's 'No changes for every stage' did not hold"
  fi
  fact update PASS "the first run exits 0 and converges what was left; the second exits 0 with $(one_line "${changes}") 'No changes' line(s) and no work named ($UPDATE2_LOG)"
}

# step_secrets: the installation's secret containers, LISTED and never counted —
# the section's 3a: they are the installation's own by design, they die with the
# project, and a "must read 0" line here could never pass. Names only, never a
# value, and this run stores none (step 16's pastes are yours).
step_secrets() {
  read_out gcloud secrets list --project="$P" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact secrets INFO "the project's secrets could not be listed ($READ_STATUS): $(one_line "$READ_OUT") — informational only, and the run's own rows are unaffected"
    return 0
  fi
  local names count
  names=$(rows_of "$READ_OUT" | tr '\n' ' ')
  count=$(rows_of "$READ_OUT" | wc -l | tr -d ' ')
  owner "the installation's secret containers, by name (no value is read, and the section's step 16 is where values are stored):"
  rows_of "$READ_OUT" | while read -r n; do owner "  $n"; done
  fact secrets INFO "$count container(s), listed by name and never counted as a criterion (they are the installation's own and go with the project): $(one_line "$names")"
}

# step_human: the steps that stay yours, printed where you can read them and
# recorded as ONE row so the block never reads as if this script did them.
step_human() {
  owner ""
  owner "the steps this script did NOT run, in Check 27's own numbering — they are a person's, a browser's or a second machine's, and this script never fakes one:"
  owner "  step 3  the billing PICKER's own success path: a run that creates a project with NO --link-billing, at your terminal, where it lists your open accounts by number and says which source answered (the API, or the gcloud fallback). Draw a fresh id for it: rm -rf $DIR"
  owner "  step 5  fugaro update-skills --check, then update-skills: the plugin at user scope, and the checkout left byte for byte alone"
  owner "  steps 6-10  V1 to V4 in Claude Code and in the base image (a folder trust dialog, a Marketplace refresh, and docker run for the container)"
  owner "  step 14 the mirror's DIGESTS: docker buildx imagetools inspect against the source, and the rerun that sends nothing"
  owner "  step 15 gcloud services enable compute.googleapis.com, then fugaro doctor: the default Compute account's roles/editor and the fix it prints"
  owner "  step 16 the hidden secret pastes in the sandbox checkout (fugaro update: the App key and claude setup-token's output), and the refusals beside them"
  owner "  step 17 the hostile clone: your own scratch repository, --yes --non-interactive, and the url.<x>.insteadOf rewrite"
  owner "  step 18 doctor's breakages, one at a time, and the one fix line each prints"
  owner "  step 19 a teammate (a second machine, or XDG_CONFIG_HOME=\$(mktemp -d)): the fresh-only refusal, then the update that writes the config"
  owner "  step 20 /fugaro:setup on the sandbox, the repository stage's billable TYPED first build, and a first fugaro run that ends in a pull request"
  owner "  step 21 the prompt-injection probe over plugin/testdata/repos/hostile/repo"
  owner "  step 22 dogfood: Fugaro's own repository, created by fugaro setup and re-converged by fugaro update"
  owner "  and the Restore's own lines that are not a cloud call: the sandbox repository's App, the PRs the runs opened (decline them), the OAuth token, and rm -rf $DIR"
  owner ""
  fact human INFO "12 steps stay yours (3, 5-10, 14, 15, 16, 17, 18, 19, 20, 21, 22, and the Restore's non-cloud lines); they are listed above and in docs/gcp-live-checklist.md's Check 27, which is the authority for all of them. Nothing here was run or faked: a step this script did not run is not a step it passed"
}

step_verdict() {
  REACHED_END=yes
  trap - ERR
  # The verdict is taken from the facts, not from the exit path: every row that
  # FAILed is a failure, and an INFO row is a reading and never a criterion.
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# --------------------------------------------------------------- the Restore
# The Restore deletes what this check created, and the project is the last of it.
# It runs from the exit trap, so it tolerates a run that stopped anywhere, and
# every command is guarded: a delete that fails must not end the shell before the
# block is printed. EVERY job in the project is this run's — guard_project_absent
# is what makes that true — so the jobs are listed and all of them deleted, and
# only an empty read counts as gone. The secrets are listed and never counted
# (the section's 3a), and the project's own delete is what takes them.
restore_run() {
  if [ -z "$FACTS_FILE" ]; then return 0; fi
  if [ "$TOUCHED" != yes ]; then
    fact restore PASS "nothing was created and nothing was read: the run stopped before its first call (a guard, or --dry-run), so there is no cloud state of this check's to undo and nothing was deleted"
    return 0
  fi
  if [ "$CREATED" != yes ]; then
    if [ -n "${P:-}" ] && [ -n "${C:-}" ]; then
      read_out gcloud projects describe "$P" --format='value(projectId)'
      if [ "$READ_STATUS" -eq 0 ]; then
        fact restore FAIL "no create run finished, yet $P exists: gcloud projects describe answers $(one_line "$READ_OUT"). Delete it yourself: gcloud projects delete $P --quiet"
        return 0
      fi
    fi
    fact restore PASS "nothing of this check's was created (no project, no job, no bucket), so there is nothing to delete"
    return 0
  fi
  say "Restore: deleting the throwaway project's jobs, its buckets, and the project itself"
  local bad="" unknown="" left="" jobs left_count=0
  # 1. every job in the project. The project is this run's own, so a job left in
  #    it would make the "jobs left" read non-zero for a project that is about to
  #    stop existing.
  read_out gcloud run jobs list --project="$P" --region="$REGION" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    unknown="$unknown (the job list could not be read: $(one_line "$READ_OUT"))"
  else
    for jobs in $(rows_of "$READ_OUT"); do
      gcloud run jobs delete "$jobs" --project="$P" --region="$REGION" --quiet >&2 || bad="$bad job:$jobs"
    done
    read_out gcloud run jobs list --project="$P" --region="$REGION" --format='value(name)'
    if [ "$READ_STATUS" -ne 0 ]; then
      unknown="$unknown (the job list could not be read back: $(one_line "$READ_OUT"))"
    elif [ -n "$(rows_of "$READ_OUT")" ]; then
      bad="$bad jobs-left:$(rows_of "$READ_OUT" | tr '\n' ',')"
    fi
  fi
  # 2. the two buckets the installation makes, BY NAME: the project also holds a
  #    Firebase default bucket and Artifact Registry's own, and those are the
  #    project's, not a prefix of anything this check may remove.
  local bucket
  for bucket in "fugaro-runs-$P" "fugaro-state-$P"; do
    gcloud storage rm --recursive "gs://$bucket" --project="$P" >&2 || true
  done
  read_out gcloud storage buckets list --project="$P" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    unknown="$unknown (the bucket list could not be read: $(one_line "$READ_OUT"))"
  else
    for bucket in "fugaro-runs-$P" "fugaro-state-$P"; do
      if grep -qxF "$bucket" <<<"$READ_OUT"; then bad="$bad bucket:$bucket"; fi
    done
    left=$(rows_of "$READ_OUT" | tr '\n' ' ')
    left_count=$(rows_of "$READ_OUT" | wc -l | tr -d ' ')
  fi
  # 3. the secrets, listed as EVIDENCE and never counted (the section's 3a):
  #    names only, never a value, and they go with the project's own delete.
  read_out gcloud secrets list --project="$P" --format='value(name)'
  local secrets="could not be listed: $(one_line "$READ_OUT")" scount=0
  if [ "$READ_STATUS" -eq 0 ]; then
    secrets=$(rows_of "$READ_OUT" | tr '\n' ' ')
    scount=$(rows_of "$READ_OUT" | wc -l | tr -d ' ')
    owner "the secret containers this Restore lists (names only, no value; they are the installation's own and the project's delete takes them):"
    rows_of "$READ_OUT" | while read -r n; do owner "  $n"; done
  fi
  # 4. and only then the project itself.
  gcloud projects delete "$P" --quiet >&2 || bad="$bad project-delete"
  read_out gcloud projects describe "$P" --format='value(lifecycleState)'
  if [ "$READ_STATUS" -ne 0 ] && ! grep -qiE 'not found|deleted|404' <<<"$READ_OUT"; then
    unknown="$unknown (the project's state could not be read: $(one_line "$READ_OUT"))"
  else
    LIFECYCLE=$(rows_of "$READ_OUT" | head -1)
  fi
  if [ -n "$bad" ]; then
    fact restore FAIL "these would not go:$bad — finish by hand: gcloud run jobs delete <each> --region=$REGION --project=$P, gcloud storage rm -r gs://fugaro-runs-$P, and gcloud projects delete $P --quiet"
    return 0
  fi
  if [ -n "$unknown" ]; then
    fact restore FAIL "the Restore could not read something back:$unknown — read it yourself before you trust the delete: gcloud projects describe $P --format=value(lifecycleState)"
    return 0
  fi
  fact restore PASS "every job in the project is gone (none left), the two buckets are gone (the $left_count bucket(s) left are the installation's own and go with the project: ${left:-none}), the $scount secret container(s) were listed and never counted (names above, no value: $secrets), and the project reads ${LIFECYCLE:-missing} — its delete is pending for 30 days"
}

# ------------------------------------------------------------------ the plan
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [local, reads] fugaro version — a RELEASE binary, because the images stage mirrors ghcr.io/dimipaun/fugaro-history:<version> and a dev build has no such tag"
  say "  [network, reads] gcloud projects describe $P — the guard that says the throwaway project does not exist yet, and again in the Restore and after each create"
  say "  [network, reads+writes] the whole create path, run at a REAL TERMINAL (a pty) in a directory that is NOT a checkout, with ONE answer typed by this script:"
  say "      fugaro setup --name $N --region $REGION --budget-mode observe --firebase-project $P --link-billing <the account you gave> --bake-repos"
  say "    which asks 'Type the GCP project's ID: ' and is answered '$P'; the project's confirmation banner, the billing banner and the Firestore location's banner are read and nothing is typed under them"
  say "  [network, reads] gcloud projects describe $P --format='value(projectId,lifecycleState)' (must read $P ACTIVE)"
  say "  [network, reads] gcloud billing projects describe $P --format='value(billingAccountName,billingEnabled)' (must name the account you gave, enabled True)"
  say "  [network, reads] gcloud firestore databases describe --database='(default)' --project=$P --format='value(locationId)' (must read $REGION)"
  say "  [network, reads] gcloud storage buckets list --project=$P (must name fugaro-runs-$P), gcloud run jobs list --project=$P --region=$REGION (must name fugarohist)"
  say "  [network, reads] gcloud artifacts docker images list $REGION-docker.pkg.dev/$P/fugaro-base --include-tags (must name the history image)"
  say "  [network+local, reads] fugaro doctor --project $N --all, with no terminal: exit 0, and its project line must name (GCP $P,"
  say "  [network+local, reads] fugaro update --project $N twice, with no terminal (the second must print No changes for every stage and exit 0)"
  say "  [local, writes] before any of that: fugaro setup --create-project --gcp-project $P --name $N --region $REGION --firebase-project $P --budget-mode observe --link-billing <account> --bake-repos --plan-only at a pty (the stage list prints, nothing is created, exit 0)"
  say "  [network, reads] gcloud projects describe $P again after the plan-only run (it must still not exist)"
  say "  [network+local, reads] fugaro setup --gcp-project $OTHER --name $N --yes with stdin at /dev/null, over a config root of its own: it must be REFUSED (project creation is typed, and --yes never covers it), and gcloud projects describe $OTHER must still answer not-found"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, reads+deletes] gcloud run jobs list --project=$P --region=$REGION, then gcloud run jobs delete <each> --project=$P --region=$REGION --quiet, then the list again (it must be empty)"
  say "  [network, deletes] gcloud storage rm --recursive gs://fugaro-runs-$P and gs://fugaro-state-$P, then gcloud storage buckets list (neither may be named; the Firebase default bucket and Artifact Registry's are the project's own)"
  say "  [network, reads] gcloud secrets list --project=$P --format='value(name)' — LISTED as evidence and never counted: they are the installation's own containers and the project's delete takes them"
  say "  [network, deletes] gcloud projects delete $P --quiet, then gcloud projects describe $P --format='value(lifecycleState)' (DELETE_REQUESTED; the record stays for 30 days and the ID is never free again)"
  say "  [local, leaves in place] the config root $C stays on purpose: the section's steps 5 to 22 run against that installation, and removing it is one line this script prints"
  say "Nothing was created, granted, written or deleted: this dry run made no gcloud call and no fugaro call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project billing-account belong identity config-root
  project-absent version plan-only negative wizard
  created billing firestore installation images doctor update
  secrets human restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 27 RESULT\n' >&3
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
  printf 'CHECK 27 RESULT END (exit %s)\n' "$status" >&3
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
    facts="${facts}"$'\n'"aborted|FAIL|$(redact "$ABORT_REASON")"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer a
  # FAILed wizard row as an answer about the setup path when the steps that would
  # have filled it never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the wizard"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so the throwaway project may still be there. Delete it by hand: gcloud run jobs list --project=$P --region=$REGION, gcloud storage rm --recursive gs://fugaro-runs-$P, and gcloud projects delete $P --quiet"
  fi
  # The verdict is taken from the facts, not from the exit path: any FAILed row
  # is a failure, and an INFO row is a reading the check's criteria do not rest
  # on.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -q '|FAIL|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself (it holds the transcripts; the config root $C is the one that stays)"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# on_signal: a Ctrl-C still prints the block and still runs the Restore, which
# is what deletes the throwaway project — except where the interrupt landed on
# the create run itself, which is the one case the Restore reports instead of
# undoing (see Cleanup in the header).
on_signal() {
  owner "interrupted: printing what was measured, then running the Restore"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check27.sh --project <throwaway-id> --billing-account <ID> [--name fugaro] [--region us-east5] [--repo <owner>/<name>] [--dir /tmp/chk27] [--dry-run]"

parse_args() {
  P=""
  A=""
  N=$NAME_FIXED
  REGION=$REGION_DEFAULT
  R=""
  DIR=${CHECK27_DIR:-/tmp/chk27}
  DRY_RUN=${CHECK27_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --billing-account)
      [ $# -ge 2 ] || die "--billing-account needs an account ID. usage: $USAGE"
      A=$2
      shift 2
      ;;
    --name)
      [ $# -ge 2 ] || die "--name needs a value. usage: $USAGE"
      N=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a region. usage: $USAGE"
      REGION=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs an owner/name. usage: $USAGE"
      R=$2
      shift 2
      ;;
    --dir)
      [ $# -ge 2 ] || die "--dir needs a path. usage: $USAGE"
      DIR=$2
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
  case "$DIR" in */) DIR=${DIR%/} ;; esac
  C="$DIR/config"
  [ -n "$P" ] || die "--project is required, and it has to be a THROWAWAY project that does not exist yet: this check creates it and then deletes it. usage: $USAGE"
  [ -n "$A" ] || die "--billing-account is required, and it has to be YOUR account: the create path links the project to it, at a real terminal. usage: $USAGE"
  [ -n "$R" ] || die "--repo is required (an owner/name): the section's step 20 and its Restore's App line are about it, and this script never touches it. usage: $USAGE"
  printf '%s' "$REGION" | grep -qE '^[a-z]+-[a-z]+[0-9]+$' \
    || die "--region '$REGION' does not look like a region (us-east5, europe-west4). usage: $USAGE"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  write_runner
  fact project PASS "$P (typed by you; it must not exist yet, and the Restore deletes it again)"
  guard_xtrace
  guard_agent
  guard_names
  guard_account
  guard_config_root

  if [ -n "$DRY_RUN" ]; then
    # A dry run reaches its own end: it is the whole of what was asked of it, so
    # it exits 0 and its block carries no verdict — never REACHED_END=no, which
    # would report this as a run that died before its last step.
    REACHED_END=yes
    print_plan
    fact dry-run PASS "the guards that need no cloud call only: no gcloud call and no fugaro call was made, and the commands a real run would run and the ones its Restore would run were printed, not run"
    BLOCK_KEYS=(project billing-account belong identity config-root dry-run)
    return 0
  fi

  guard_version
  TOUCHED=yes
  guard_project_absent
  step_plan_only
  step_negative
  step_create
  step_created
  step_billing
  step_firestore
  step_installation
  step_images
  step_doctor
  step_update
  step_secrets
  step_human
  step_verdict
}

trap on_error ERR
trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
