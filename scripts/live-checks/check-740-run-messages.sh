#!/usr/bin/env bash
# Check 740 — messaging a run that is going, on a REAL installation, as ONE
# script YOU run.
#
# It settles the live half of the run-messages work (docs/run-messages;
# D2940-D2947, the release in progress 0.27.0). A person may message a run that
# is already going: `fugaro tell RUN "text"` writes one object under
# `tokens/<slug>/<run>/messages/NNNN.msg.json` — the one prefix a job account
# may only read (D2941) — and the run's runner reads it at its next coder stage.
# With `--now` the message stops the coder stage at the agent's next quiet
# point and the stage goes on in the SAME session (D2945); `--wrap-up` asks it
# to commit what it has and stop (D2946).
#
# Three things the reconciliation leaves open are settled by this check and by
# nothing else in the repository (docs/run-messages/reconciliation.md):
#
#   U2  whether a session stopped at a quiet point resumes cleanly, keeping the
#       turns before it. RM3's tests against `fakeclaude` prove only the
#       runner's side; the real binary is what this check reads.
#   U3  whether the real binary's `stream-json` events mark a quiet point as the
#       rule reads it, so a stop actually lands between two tool calls.
#   U4  whether the bucket's lifecycle rule really selects the message objects
#       and never a `budget-token` beside them (RM6's `terraform test` is the
#       offline half; this is the applied bucket).
#
# So the check LAUNCHES ONE REAL RUN of a repository you name, sends it a plain
# message and a `--now` message while its implement stage is going, and reads
# what the run's own record says became of them: the `--now` message's fate
# carries NO reason exactly where the stop found a quiet point AND the turn
# after it ran in the same session — the runner writes `no quiet point in 10m`
# instead when its ten-minute bound was reached, and `session lost at forced
# stop` when a forced stop left a session that could not be resumed
# (internal/runner/yield.go). A reason on that fate is U2's or U3's own answer,
# and the row quotes it.
#
# The last two readings are about the fence rather than the delivery: the check
# takes the REAL account of the job the run landed on (the record's own
# `execution`, then `gcloud run jobs describe`) — and, where the installation
# deploys one, the account of a shared project job — and asks the IAM Policy
# Troubleshooter whether that account holds `storage.objects.create` on the runs
# bucket. It must be denied (D2941): both accounts hold
# `roles/storage.objectViewer` on `tokens/`, which carries no write at all. The
# question is at BUCKET level because the Troubleshooter refuses an object-level
# resource name, so the object-name fence is read off the condition the answer
# explains rather than off the decision. A throwaway account given only
# `objectViewer` would prove nothing — the role cannot write by definition —
# which is why the accounts this check asks about are the installation's own.
#
# Usage:      bash scripts/live-checks/check-740-run-messages.sh \
#               --installation <name> --repo <owner/name> [--recipe <name>] \
#               [--dry-run]
#             --dry-run  prints the plan and touches nothing.
#             --installation is the Fugaro project name the local config in
#             force carries (`fugaro doctor --json`'s project.name), and the
#             answer is held to it: a name that is not the one doctor reports is
#             a STOP before anything is run.
#             --repo is the repository the small run is launched in, as
#             owner/name (the form `fugaro run --repo` takes from anywhere, so
#             this check needs no checkout of it). It is a REAL launch of that
#             repository: the run pushes a branch and opens a pull request, and
#             the task is a two-line file, so pick a repository whose pull
#             requests you do not mind seeing.
# Needs:      bash, mktemp, python3, gcloud and fugaro on PATH. Nothing is
#             installed and nothing is fetched; a missing tool stops the run
#             with one line naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused,
#             whether you typed it as the installation, as the repository or as
#             the GCP project, and doctor's own answer is refused the same way.
# Costs:      ONE real run of the repository you name. Its spend is the run's
#             own — the model spend of its implement stage, its review rounds
#             and whatever fixes follow, under the repository's and the
#             installation's own caps — and one branch and pull request in that
#             repository. Everything else this check does is a read (doctor,
#             version, runs ls, gcloud run jobs list/describe, gcloud storage
#             cat/describe) or the two small objects `fugaro tell` writes under
#             tokens/, which expire by the bucket's own lifecycle rule.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check-740-run-messages_test.sh) runs it
#             against fakes and never touches gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: its scratch directory (holding the
#             task file and every document it read) is removed from the exit
#             trap and the `restore` row says whether it really went. It writes
#             nothing else: the run it launched, its branch and its pull request
#             stay — they are what it is a reading of — and the two message
#             objects go when the bucket's message rule reaches them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 740 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 740 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 740 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 740 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, a tool is
#                                                      missing, an error ended it);
#               CHECK 740 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured is INCOMPLETE, not a pass, and the
#             register's section is read against that word.
#
# NO SECRET IS READ, so none can be printed: `gcloud` and `fugaro` use the
# credentials already on your machine, inside their own processes, and every
# answer this check reads is a run's row, a record, a job's account, a policy
# decision, a lifecycle configuration or the tiny JSON `fugaro tell` prints. The
# reader prints a fixed vocabulary — a name, an id, a stage, a status word, a
# yes/no, a number, and the record's own reason for a fate — and every value is
# clipped to that vocabulary, so a document that echoed back something else (a
# token in an error body, a message a service wrote) cannot reach the block. No
# message TEXT is printed either: what is written is this check's own sentence.
# And no trace: `bash -x` prints every command as it runs, so guard_xtrace
# REFUSES a run whose shell arrived with tracing on.
#
# CHECK740_INSTALLATION, CHECK740_REPO, CHECK740_RECIPE, CHECK740_DRY_RUN,
# CHECK740_CALL_SECONDS, CHECK740_LAUNCH_SECONDS, CHECK740_RUN_SECONDS,
# CHECK740_POLL_SECONDS, CHECK740_IMPLEMENT_SECONDS, CHECK740_SEND_GAP_SECONDS,
# CHECK740_RUNS_LIMIT and CHECK740_JOBS_LIMIT exist for this script's own test
# and for an owner's own patience; none of them can point a call at another
# project or another host.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had.
XTRACE_AT_START=no
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
USAGE="check-740-run-messages.sh --installation <name> --repo <owner/name> [--recipe <name>] [--dry-run]"
# The release the run-messages work ships in: `fugaro tell`, the record's
# messages block and the runner's yield all arrive in it, and a run launched on
# a job image built from an older release reads no message at all (F9).
RELEASE=0.27.0
# The stages a message may be delivered into (rule 1): a first run's implement,
# a follow-up's implement, or a fix. Every other stage — review, first-line
# review, adversarial, triage, merge judge, plan, digest, diagnose — carries
# none of them by construction.
CODER_STAGES=" implement fix "
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
CLEANUP_RUNNING=no
# Everything the steps hand each other, empty until the step that reads it. A
# step that ends early (a launch that refused, a run that never ended) leaves the
# ones after it with nothing to read, and they say so; with `set -u` an
# uninitialised name would end the run instead, with no block for the owner.
RUN_ID=""
RUN_REF=""
SLUG=""
LAUNCH_REPO=""
LAUNCH_STATUS=""
MESSAGE_KEY=""
SEQ_PLAIN=""
SEQ_NOW=""
RECORD_READ=no
RUN_ENDED=no
PROJECT=""
REGION=""
BUCKET=""
EXECUTION=""
EXEC_PROJECT=""
EXEC_LOCATION=""
EXEC_JOB=""
ACCEPTING=""
RECORD_STAGE=""
RECORD_STATUS=""
ROW_STAGE=""
ROW_STATUS=""
ROW_TERMINAL=""
TELL_SEQ=""
TELL_ERR=""
FUGARO_BIN=""
TS_DECISION=""
TS_NOTE=""
PROJECT_JOBS=""
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
fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured KEY REASON — a row this run could NOT settle, for a reason that is
# not a finding about the installation. It reads UNVERIFIED in the block (it is
# not a pass) and the check ends INCOMPLETE.
unmeasured() {
  fact "$1" UNVERIFIED "$2"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as "the channel is fine" for a run that
# measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  # Before the scratch directory exists there are no facts at all, so the reason
  # is carried out in ABORT_REASON and finish() writes the one row.
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE" 2>/dev/null; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the message channel"
  fi
  ABORT_REASON="an internal error ended the run at line $line of the script"
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 740 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 740 needs bash"
  need mktemp
  need python3 "the reader that parses every document this check reads is one python3 file"
  need gcloud "the run's job account, its IAM and the bucket's lifecycle rule are read with gcloud"
  need fugaro "the run this check launches and messages is launched and messaged with fugaro"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk740-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk740.XXXXXXXX") || die "Check 740 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 740 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  stop_call
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
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the message channel"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ] || [ "$status" -eq 3 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
  else
    # Every file this check writes holds a document it read — a doctor's answer,
    # a row of `runs ls`, the run's record, a job's description, a policy
    # decision, a bucket's configuration — and the task file it launched. No
    # credential is read on any path, so none can be in one of them. Their
    # removal is READ, not assumed.
    facts="${facts}"$'\n'"restore|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself") (the task file this check launched, the doctor's answer, the run's rows and its record, the jobs it described, the policy decisions and the bucket's lifecycle configuration). This check wrote nothing else anywhere: the run it launched, its branch and its pull request are the reading it came for and stay, and the two message objects under tokens/ go when the bucket's own lifecycle rule reaches them"
  fi
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=1
    elif [ -n "$(criteria_unmet "$facts")" ]; then
      status=3
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs — including the run it launches — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's own output"
}

# guard_agent_session: this check is the owner's. It launches a real run and
# writes messages into it, and the block it prints is meant to be pasted back by
# the person who ran it — the register's row says no agent runs any step of it.
# Refusing here is what makes the block say that, rather than an agent's run
# reading as the installation's answer.
#
# The loop is the five markers that say an agent, and not a person, is driving
# this shell. The sixth, CLAUDE_CODE_SSE_PORT, says only that this shell sits in
# an editor's terminal, where the person is still the one typing, so it is
# DROPPED rather than refused: refusing on it would lock you out of this check in
# your own editor.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 740 is yours to run at your own terminal, against an installation you own, and no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 740 is yours to run at your own terminal, against an installation you own, and no agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like), so this is your own session — the one this check is written for: the run it messages is the run you launched. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused, because it says this shell sits in an editor's terminal and not that an agent is driving it"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# guard_belong: `belong` is the owner's own installation, and this check
# launches a real run, opens a pull request in a repository of it and writes
# objects under tokens/. Any value GIVEN to this check that names it is refused
# before the first call is made, and the answer doctor gives a moment later is
# refused in step_installation, which is where the name the config in force
# carries first exists — the name you type and the config in force are two
# things, and this check has to see the same one twice.
guard_belong() {
  local what value
  for what in installation repo; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    repo) value=$REPO ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    *belong*)
      fact belong FAIL "'$value' (the $what) names 'belong', the owner's own development installation: Check 740 launches a real run and opens a pull request, and it is for an installation that is not belong"
      stop "Check 740 never runs against 'belong'. Name another installation: $USAGE"
      ;;
    esac
  done
}

# version_at_least HAVE NEED: 0 when HAVE is a release number at or above NEED,
# 1 when it is a release number below it, 2 when HAVE is not a release number at
# all (a dev build, a commit) — the third answer is why this is not a comparison
# alone: a build that names no version is decided by the feature probe and never
# by a guess at what "dev" sorts as.
version_at_least() { # version_at_least HAVE NEED
  awk -v have="$1" -v need="$2" 'BEGIN {
    n = split(have, h, ".")
    m = split(need, k, ".")
    for (i = 1; i <= n; i++) {
      if (h[i] !~ /^[0-9]+$/) exit 2
    }
    for (i = 1; i <= (n > m ? n : m); i++) {
      a = (i <= n ? h[i] + 0 : 0)
      b = (i <= m ? k[i] + 0 : 0)
      if (a > b) exit 0
      if (a < b) exit 1
    }
    exit 0
  }'
}

# --------------------------------------------------------------- the calls
# last_line FILE: one bounded line of a command's own output, for a block that
# has to say why a step failed without pasting a log.
last_line() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | tr -d '\r' | tr '\n' ' ' | head -c 240 || true)
  [ -n "$text" ] || text="the command printed nothing"
  printf '%s' "$text"
}

# run_call SECONDS OUT LOG cmd... — one long command with a deadline of its own,
# in a process group of its own so the deadline reaches whatever the call
# started. `timeout(1)` is not used: it is not on macOS, where this check is also
# run.
#
# The status comes from a file the call's own subshell writes before it exits,
# and never from `wait`, and there is no second background job watching the
# deadline. Both of those are load-bearing. With job control on bash reaps a
# finished child by itself, and a `wait` for a pid whose record bash has let go
# answers "No record of process" instead of the status; under `set -e` that
# failing wait runs this script's ERR trap, and the harness that runs every live
# check's test side by side lost that race often enough to fail this check's own
# suite with an "internal error" that had not happened. A deadline watchdog
# beside the call is the same hazard from the other side: the second background
# job is one more record for bash to lose, and it lost it just as often. One
# background job, a status file for the status, and `SECONDS` for the deadline.
CALL_PID=""
run_call() { # run_call SECONDS OUT LOG cmd...
  local secs=$1 out=$2 log=$3
  shift 3
  # job control, so the call is a process group of its own and the deadline
  # reaches whatever the call started rather than only the call itself.
  set -m
  local st="$SCRATCH/.status.$$" start=$SECONDS status=""
  rm -f "$st"
  # The subshell's own sides of the traps are cut off from this script's: a call
  # that refuses exits non-zero, which is the ordinary case this check reads, and
  # it must neither run the check's ERR handler nor end the subshell before it
  # has written the status down. Its descriptors are cut off too, so an output
  # descriptor left open in it cannot hold a pipe open for a caller capturing
  # this script's output.
  (
    trap - ERR
    set +e
    "$@" </dev/null >"$out" 2>"$log"
    printf '%s' "$?" >"$st"
  ) </dev/null >/dev/null 2>&1 &
  CALL_PID=$!
  # The file is what says the call has finished -- not `kill -0`, which cannot
  # tell a running child from one that has exited and not been reaped. The write
  # is one short write to a file that has to be non-empty to count, and it
  # happens before the subshell exits, so a finished call is always seen as
  # finished within the tick.
  while [ ! -s "$st" ] && [ $((SECONDS - start)) -lt "$secs" ]; do sleep 0.05; done
  if [ -s "$st" ]; then
    status=$(cat "$st" 2>/dev/null || true)
  else
    # The deadline, and nothing else reaches here: a call that was signalled
    # before it did write its status, as 128 + the signal.
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    sleep 0.2
    kill -KILL -- "-$CALL_PID" 2>/dev/null || kill -KILL "$CALL_PID" 2>/dev/null || true
    status=124
  fi
  # Collecting the child is bookkeeping only: the status is already in hand, so a
  # `wait` that finds no record has nothing left to spoil.
  wait "$CALL_PID" 2>/dev/null || true
  rm -f "$st"
  CALL_PID=""
  case "$status" in '' | *[!0-9]*) status=1 ;; esac
  return "$status"
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
    wait "$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

# ------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# msg_field SEQ FIELD FILE — one field of the message the reader printed as
# `m<seq>|<state>|<stage>|<flags>|<reason>|<object>`: 2 state, 3 stage, 4 flags,
# 5 the runner's own reason, 6 the object the message is. No field is ever a
# whole line, so no field can carry a separator into the block: every value is
# clipped to that vocabulary by the reader, which drops '|' outright.
msg_field() { # msg_field SEQ FIELD FILE
  local line
  line=$(grep -m1 "^m$1|" "$3" 2>/dev/null || true)
  [ -n "$line" ] || return 0
  case "$2" in
  2) printf '%s' "$line" | cut -d'|' -f2 ;;
  3) printf '%s' "$line" | cut -d'|' -f3 ;;
  4) printf '%s' "$line" | cut -d'|' -f4 ;;
  5) printf '%s' "$line" | cut -d'|' -f5 ;;
  6) printf '%s' "$line" | cut -d'|' -f6 ;;
  esac
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 740's reader: the one place the documents this check reads are parsed.

The shell never parses JSON and never prints a field it did not choose. This
program prints a FIXED vocabulary -- a name, an id, a stage, a status word, a
yes/no, a number, a permission decision, and the record's own reason for a
message's fate -- and every value is clipped to the characters that vocabulary
allows, so a document that echoed back something else (a token in an error
body, a message a service wrote) cannot reach the block. Message TEXT is never
printed: the record carries none, and this reader never looks at an object under
tokens/.

Spellings: the fields it reads are read in more than one spelling where the
APIs disagree. A v1 Cloud Run job nests its execution template one level below a
service's, so a job's account sits at spec.template.spec.template.spec
(internal/backend/gcp's LabelProfile over a job), and `gcloud storage buckets
describe` prints the lifecycle block as `lifecycleConfig` on the `gcloud
storage` surface and as `lifecycle_config` on the older one. A reader that knew
one spelling only would see every real job as account-less, or every real bucket
as having no rule at all -- and would report the fence's absence as a finding.
"""
import json
import os
import re
import sys

SAFE = re.compile(r"[^A-Za-z0-9 ._/@:+-]")


def clip(value, limit=200):
    text = value if isinstance(value, str) else str(value)
    text = SAFE.sub(" ", text)
    text = " ".join(text.split())
    return text[:limit]


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def one(doc, *spellings):
    for name in spellings:
        if isinstance(doc, dict) and name in doc and doc[name] is not None:
            return doc[name]
    return None


def job_spec(doc):
    spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}
    return (spec.get("template") or {}).get("spec") or {}


def labels_of(doc):
    return (doc.get("metadata") or {}).get("labels") or {}


def cmd_doctor(path):
    doc = load(path)
    project = doc.get("project") or {}
    print("name=%s" % clip(project.get("name") or ""))
    print("project=%s" % clip(project.get("gcp_project") or ""))
    print("region=%s" % clip(project.get("region") or ""))


def cmd_row(path):
    """`fugaro runs ls --json` -- the row of the run this check launched, by its
    run_id. Every field is the CLI's own view of the run's record."""
    doc = load(path)
    rows = doc.get("runs") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        fail("not a runs document")
    want = sys.argv[3] if len(sys.argv) > 3 else ""
    for row in rows:
        if (row.get("run_id") or "") != want:
            continue
        print("found=yes")
        print("stage=%s" % clip(row.get("stage") or ""))
        print("status=%s" % clip(row.get("status") or ""))
        print("terminal=%s" % ("yes" if row.get("terminal") else "no"))
        print("execution=%s" % clip(row.get("execution") or "", 400))
        print("run=%s" % clip(row.get("run") or ""))
        print("repo=%s" % clip(row.get("repo") or ""))
        return
    print("found=no")


def cmd_record(path):
    """The run's own record, read from the bucket
    (runs/<slug>/<run>/result.json). What it carries that this check reads: the
    run it is, its `execution` (the job the run landed on, which is how the real
    job account is found), its own `stage` and `status` (the stage the run was
    in when it ended, and how it ended) and the messages block the runner writes
    -- one entry per message, with the state, the stage it reached, its flags and
    the runner's own reason where a stop carried one. The block is the ONLY
    statement about delivery this check trusts: `fugaro runs show --json` prints
    the same block as the CLI states it, and this command reads the record
    itself. The stage and the status are read for the one thing a fate cannot
    say on its own: whether the run went on past the stage a stop delivered into
    (see the --now row), which is why they are read from the record and not from
    `fugaro runs ls`, whose row is the CLI's own view of it."""
    doc = load(path)
    print("run_id=%s" % clip(doc.get("run_id") or ""))
    print("stage=%s" % clip(doc.get("stage") or ""))
    print("status=%s" % clip(doc.get("status") or ""))
    print("execution=%s" % clip(doc.get("execution") or "", 400))
    msgs = doc.get("messages") or {}
    print("accepting=%s" % ("yes" if msgs.get("accepting") else "no"))
    print("format=%s" % clip(msgs.get("format") if msgs.get("format") is not None else ""))
    flags = msgs.get("flags") or []
    print("flags=%s" % clip(",".join(f for f in flags if isinstance(f, str))))
    entries = msgs.get("messages") or []
    print("messages=%d" % len(entries))
    for e in entries:
        seq = e.get("seq")
        if not isinstance(seq, int):
            continue
        print("m%d|%s|%s|%s|%s|%s" % (
            seq,
            clip(e.get("state") or ""),
            clip(e.get("stage") or ""),
            clip(",".join(f for f in (e.get("flags") or []) if isinstance(f, str))),
            # The runner's own reason, and only where a stop carried one. This
            # is the field the --now row is decided on: it is written where a
            # quiet point was NOT found, and where a forced stop left a session
            # that could not be resumed.
            clip(e.get("reason") or ""),
            # The object the message is, as the store names it. The check names
            # it in the rows as the object the fence is about; the question posed
            # to the Policy Troubleshooter is the bucket, because an object-level
            # resource name is refused.
            clip(e.get("object") or "", 300),
        ))


def cmd_job(path):
    """`gcloud run jobs describe --format=json`: the job's own service account.
    That account, and never a throwaway one, is the principal the Policy
    Troubleshooter is asked about: the question is whether the account a run of
    this installation really runs as holds storage.objects.create on the runs
    bucket (the object-name fence beside it is read from the answer's explained
    condition)."""
    doc = load(path)
    spec = job_spec(doc)
    print("name=%s" % clip((doc.get("metadata") or {}).get("name") or ""))
    print("sa=%s" % clip(spec.get("serviceAccountName") or ""))


def cmd_jobs(path):
    """`gcloud run jobs list --format=json`: the shared project jobs the
    installation deploys, found by the profile label the deployer writes
    (gcp.LabelProfile / job.profile_label), each with its own service
    account."""
    doc = load(path)
    if not isinstance(doc, list):
        fail("not a list of jobs")
    jobs = []
    for j in doc:
        if not (labels_of(j).get("fugaro_profile") or ""):
            continue
        name = (j.get("metadata") or {}).get("name") or ""
        if not name:
            continue
        jobs.append((name, job_spec(j).get("serviceAccountName") or ""))
    jobs.sort()
    print("count=%d" % len(jobs))
    for name, sa in jobs:
        print("job=%s|%s" % (clip(name), clip(sa)))


def cmd_troubleshoot(path):
    """The Policy Troubleshooter's answer. The decision is read in both
    spellings the API has carried it in -- v3 answers `accessDecision` and v1
    answers `access` -- and anything that is not one of the answers this check
    knows is reported as it stands, so a run that could not measure prints what
    it got rather than a guess.

    The `fence` line is the other half, and the half the question is asked about
    here: the resource is the BUCKET (the API refuses an object-level full
    resource name), so the condition on the object name -- the tokens/ prefix
    fence D2941 pins -- is not evaluated by the decision. What the answer does
    explain is the binding's own condition, wherever the payload carries one
    (v3 `explainedPolicies[].explainedBindings[]`, v1 `policy.bindings[]`, and
    any nested `bindings`), and the rows report that text instead of a verdict
    about an object."""
    doc = load(path)
    decision = one(doc, "accessDecision", "access")
    if isinstance(decision, dict):
        decision = decision.get("accessDecision") or decision.get("access") or decision.get("state")
    print("decision=%s" % clip(decision if decision is not None else "none"))
    errors = doc.get("errors") or []
    print("errors=%d" % len(errors))
    if errors:
        first = errors[0] if isinstance(errors[0], dict) else {}
        print("problem=%s" % clip(first.get("message") or first.get("reason") or ""))
    conditions = []
    walk_conditions(doc, conditions)
    seen = []
    for text in conditions:
        text = " ".join(text.split())
        if text and text not in seen:
            seen.append(text)
    print("conditions=%d" % len(seen))
    print("fence=%s" % clip(" ; ".join(seen), 600))


def walk_conditions(node, found):
    """Every binding condition's text in a troubleshooter payload, in the order
    the payload carries it. The walk is shape-tolerant on purpose: the payload's
    binding lists sit under different keys across the v1 and v3 spellings, and a
    reader that only knew one of them would report no fence on the other."""
    if isinstance(node, dict):
        cond = node.get("condition")
        if isinstance(cond, dict):
            for key in ("expression", "title", "description"):
                text = cond.get(key)
                if isinstance(text, str):
                    found.append(text)
        for value in node.values():
            walk_conditions(value, found)
    elif isinstance(node, list):
        for value in node:
            walk_conditions(value, found)


def cmd_lifecycle(path):
    """`gcloud storage buckets describe --format=json`: the bucket's lifecycle
    configuration, read as the two things U4 asks -- that a Delete rule selects a
    message object (`tokens/<slug>/<run>/messages/NNNN.msg.json`), and that it
    does NOT select the sealed budget token beside it
    (`tokens/<slug>/<run>/budget-token`). The match is computed here on the two
    names' own segments rather than on the rule's shape, because GCS matches a
    prefix and a suffix and never a middle segment (modules/installation/
    bucket.tf)."""
    doc = load(path)
    rules = ((one(doc, "lifecycleConfig", "lifecycle_config") or {}).get("rule")) or []
    if not isinstance(rules, list):
        fail("not a lifecycle configuration")
    message = "tokens/check740-slug/check740-run/messages/0001.msg.json"
    token = "tokens/check740-slug/check740-run/budget-token"

    def matches(cond, name):
        prefixes = one(cond, "matchesPrefix", "matches_prefix") or []
        suffixes = one(cond, "matchesSuffix", "matches_suffix") or []
        if isinstance(prefixes, str):
            prefixes = [prefixes]
        if isinstance(suffixes, str):
            suffixes = [suffixes]
        if prefixes and not any(name.startswith(p) for p in prefixes):
            return False
        if suffixes and not any(name.endswith(s) for s in suffixes):
            return False
        return bool(prefixes or suffixes)

    found = None
    for rule in rules:
        cond = rule.get("condition") or {}
        action = (rule.get("action") or {}).get("type") or ""
        if matches(cond, message):
            found = (rule, cond, action)
            break
    print("rules=%d" % len(rules))
    if found is None:
        print("msg_rule=no")
        print("msg_selects_token=%s" % ("yes" if any(matches(r.get("condition") or {}, token) for r in rules) else "no"))
        return
    rule, cond, action = found
    print("msg_rule=yes")
    print("msg_action=%s" % clip(action))
    age = one(cond, "age")
    print("msg_age=%s" % clip(age if age is not None else "unrecorded"))
    print("msg_selects_message=yes")
    print("msg_selects_token=%s" % ("yes" if matches(cond, token) else "no"))
    print("token_rule=%s" % ("yes" if any(matches(r.get("condition") or {}, token) for r in rules) else "no"))


def fail(message):
    sys.stderr.write("check 740 reader: %s\n" % message)
    sys.exit(1)


CMDS = {
    "doctor": cmd_doctor,
    "row": cmd_row,
    "record": cmd_record,
    "job": cmd_job,
    "jobs": cmd_jobs,
    "troubleshoot": cmd_troubleshoot,
    "lifecycle": cmd_lifecycle,
}

if len(sys.argv) < 3 or sys.argv[1] not in CMDS:
    fail("usage: reader.py <%s> FILE [ARG]" % "|".join(sorted(CMDS)))
try:
    CMDS[sys.argv[1]](sys.argv[2])
except SystemExit:
    raise
except Exception as exc:  # noqa: BLE001 - the caller reports it as an unread answer
    fail("%s" % clip(exc))
READER_EOF
}

# ------------------------------------------------------------------ the steps
# step_installation asks the local config which installation it is looking at and
# holds it to the name that was typed.
step_installation() {
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/doctor.out" "$SCRATCH/doctor.err" \
    "$FUGARO_BIN" doctor --json || status=$?
  if [ "$status" = 124 ] || { [ "$status" != 0 ] && [ "$status" != 1 ]; }; then
    sed -n '1,5p' "$SCRATCH/doctor.err" >&4 || true
    stop "fugaro doctor --json did not answer (exit ${status}), so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local answer name
  answer=$(reader doctor "$SCRATCH/doctor.out") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  name=$(field_of name "$answer")
  PROJECT=$(field_of project "$answer")
  REGION=$(field_of region "$answer")
  [ -n "$name" ] || stop "fugaro doctor --json named no project, so this check cannot tell which installation it is looking at"
  if [ "$name" != "$INSTALLATION" ]; then
    fact installation FAIL "the installation named on the command line is $INSTALLATION and the local config in force answers $name"
    stop "the name you typed and the config in force are two different installations: run with --installation $name, or point FUGARO_PROJECT/FUGARO_CONFIG at the one you meant"
  fi
  [ -n "$PROJECT" ] || stop "fugaro doctor --json answered without a gcp_project, so this check cannot say which project's runs bucket it reads"
  case "$(lower "$PROJECT")" in
  *belong*)
    fact belong FAIL "the local config in force answers the installation $name on GCP project $PROJECT, which names the owner's own development installation"
    stop "Check 740 never runs against 'belong'"
    ;;
  esac
  BUCKET="fugaro-runs-$PROJECT"
  fact belong PASS "neither the installation ($name) nor the repository ($REPO) nor the GCP project ($PROJECT) names the owner's own development installation (nothing typed contains 'belong')"
  fact installation PASS "the local config in force is installation $name on GCP project $PROJECT (region ${REGION:-unrecorded}), and the runs bucket this check reads is gs://$BUCKET"
}

# step_release: the gate every check of an unreleased feature carries. The work
# ships in 0.27.0, and a build that predates it has no `fugaro tell` at all, so
# the messages below could not be sent. Both probes are offline: `fugaro version`
# and the usage line `tell --help` prints.
step_release() {
  local v feature=no status=0
  run_call "$CALL_SECONDS" "$SCRATCH/version.out" "$SCRATCH/version.err" "$FUGARO_BIN" version || status=$?
  v=$(head -n 1 "$SCRATCH/version.out" 2>/dev/null | head -c 60 || true)
  # A release build prints its number bare (internal/cli's versionTemplate:
  # exactly the Version variable and a newline); a leading v is dropped if one
  # is there, so `v0.27.0` is read as the release it is rather than as a build
  # that names no version.
  v=${v#v}
  fact fugaro-version PASS "${v:-fugaro version printed nothing}"
  local help=""
  status=0
  run_call "$CALL_SECONDS" "$SCRATCH/tellhelp.out" "$SCRATCH/tellhelp.err" "$FUGARO_BIN" tell --help || status=$?
  help=$(cat "$SCRATCH/tellhelp.out" 2>/dev/null || true)
  if printf '%s' "$help" | grep -q -- '--now' && printf '%s' "$help" | grep -q -- '--wrap-up'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no run-messages channel: 'fugaro tell --help' does not name --now and --wrap-up, so neither the command nor the yield the run below is messaged through is in this build. Check 740 needs release ${RELEASE} or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no run-messages channel: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'tell --help' names --now and --wrap-up: this build carries the channel the run below is messaged through"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the run-messages channel ships in: the usage line above names the flags only because a build that reports no release number may still carry the work, and a run measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro tell --help' names --now and --wrap-up, so this build carries the channel"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. Nothing asks for a confirmation — `fugaro run` is the ordinary launch and
# the repository's own caps bound it — and a dry run prints the same sentences
# and touches nothing.
step_bound() {
  fact bound PASS "ONE real run of ${REPO} and what its task costs — the model spend of its implement stage, its review rounds and any fixes that follow, under the repository's and the installation's own caps — plus a branch and a pull request in that repository. Every other call this check makes is a read (doctor, version, runs ls, gcloud run jobs list/describe, gcloud storage cat/describe, the Policy Troubleshooter) or the two small objects 'fugaro tell' writes under tokens/, which the bucket's own lifecycle rule expires. A dry run commits none of it"
}

# step_launch: the run everything below is a reading of. It is launched by name
# (`--repo owner/name`), so this check needs no checkout of the repository and
# nothing of yours is read from disk; `--json` is what names the run it then
# follows, and a launch that prints no run ID is a finding about the launcher.
#
# The brief goes over as --task-file and never as the positional argument: that
# argument is the task TEXT (`fugaro run [TEXT]`), so handing it a path launches
# a run whose task is the path itself, with the brief this check wrote never
# reaching it — and a run doing that on a one-line sentence is not the run these
# rows are readings of.
step_launch() {
  cat >"$SCRATCH/task.md" <<'TASK_EOF'
# Task: check 740 — one small file for the run-messages check

Create a file named CHECK740.md at the repository root whose whole content is
the single line:

    check 740

Commit it and open the pull request. Change nothing else.
TASK_EOF
  say "launching one real run of ${REPO} (the run this check messages)"
  local status=0
  run_call "$LAUNCH_SECONDS" "$SCRATCH/launch.out" "$SCRATCH/launch.err" \
    "$FUGARO_BIN" run --repo "$REPO" ${RECIPE:+--recipe "$RECIPE"} --json \
    --task-file "$SCRATCH/task.md" || status=$?
  if [ "$status" = 124 ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_SECONDS}s and was ended: this check has no run to message. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$status" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${status} for ${REPO}: the check needs a run to message, and a launch that refuses says the repository or the installation is not one a run starts on. Its last line: $(last_line "$SCRATCH/launch.err")"
    return 0
  fi
  local launched_repo
  launched_repo=$(python3 - "$SCRATCH/launch.out" <<'PY' 2>/dev/null || true
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
for key in ("run_id", "repo", "run", "status"):
    value = doc.get(key)
    if isinstance(value, str):
        print("%s=%s" % (key, value))
PY
)
  RUN_ID=$(field_of run_id "$launched_repo")
  RUN_REF=$(field_of run "$launched_repo")
  LAUNCH_REPO=$(field_of repo "$launched_repo")
  LAUNCH_STATUS=$(field_of status "$launched_repo")
  if [ -z "$RUN_ID" ] || [ -z "$RUN_REF" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, run=${RUN_REF:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  SLUG=${RUN_REF%%/*}
  LAUNCH_REPO=${LAUNCH_REPO:-$REPO}
  # The object the fence is about, for the rows to NAME: the key the store gives
  # a run's first message (internal/runmsg.Path). It never reaches a command the
  # owner copies — the plan's Never list forbids one built from result.json — and
  # the Troubleshooter itself is asked about the BUCKET, which refuses an
  # object-level resource name; this is the object the fence is about, not a
  # resource the check passes.
  MESSAGE_KEY="tokens/${SLUG}/${RUN_ID}/messages/0001.msg.json"
  fact run-launched PASS "fugaro run launched ${RUN_REF} (${RUN_ID}) on ${LAUNCH_REPO}, status ${LAUNCH_STATUS:-unknown}: the run is this check's own, and every row below is that one run and no other"
}

# step_watch: the two messages, sent while implement is going, which is the only
# window in which they can be delivered INTO implement.
#
# A plain message delivered at a boundary reaches the NEXT coder stage, so a run
# that passes its review first time would leave it undelivered; sending it a few
# seconds before the `--now` message makes it ride the same stop, and the stop is
# what carries both into the same resumed turn (internal/runner/yield.go's
# claimYield: every pending message goes, not just the flagged one). So the plain
# message's own reading is rule 1's delivery into a coder stage, and the `--now`
# message's is the quiet point itself.
step_watch() {
  if [ -z "$RUN_ID" ]; then
    unmeasured message-plain "nothing was launched, so no message could be sent: the row above says why"
    unmeasured message-now "nothing was launched, so no message could be sent: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to reach its implement stage (up to ${IMPLEMENT_SECONDS}s)"
  local waited=0 stage="" status=""
  while [ "$waited" -lt "$IMPLEMENT_SECONDS" ]; do
    if read_row; then
      stage=$ROW_STAGE
      status=$ROW_STATUS
      [ "$stage" = implement ] && break
      [ "$ROW_TERMINAL" = yes ] && break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$stage" != implement ]; then
    unmeasured message-plain "the run never reached an implement stage this check could see (last row: stage=${stage:-unknown}, status=${status:-unknown}, after ${waited}s): the plain message is read at a coder stage's boundary and the check sends it into the one that is running, so a run that ended, or that was already past implement, leaves this row NOT measured. The run it launched is a real one and is not cancelled by stopping here — read its record yourself with 'fugaro runs show ${RUN_ID} --json'"
    unmeasured message-now "the run never reached an implement stage this check could see (last row: stage=${stage:-unknown}, status=${status:-unknown}, after ${waited}s): a --now message asks the agent for its next quiet point INSIDE the coder stage that is running, and there was none to ask. The run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  say "sending the plain message (fugaro tell ${RUN_ID} \"…\")"
  if ! send_message ""; then
    unmeasured message-plain "the plain message could not be sent: $(tell_error). That refusal is the whole reading — a run whose runner predates the channel, a run past finalize, a recipe with no coder stage and a text over 4 KiB are each refused with their own reason"
    unmeasured message-now "the plain message could not be sent, and the --now message was not attempted: $(tell_error)"
    return 0
  fi
  SEQ_PLAIN=$TELL_SEQ
  # The gap is what makes the plain message pending when the stop fires. It is
  # not a wait for delivery: the plain message may only be delivered at a
  # boundary, and no boundary of implement is left.
  say "waiting ${SEND_GAP_SECONDS}s so both messages are pending at the same stop"
  sleep "$SEND_GAP_SECONDS"
  say "sending the --now message (fugaro tell ${RUN_ID} --now \"…\")"
  if ! send_message "--now"; then
    unmeasured message-now "the --now message could not be sent: $(tell_error). That refusal is the whole reading — a run whose runner predates the channel, a run past finalize, a recipe with no coder stage and a 21st message are each refused with their own reason"
    return 0
  fi
  SEQ_NOW=$TELL_SEQ
  say "both messages are written; waiting for ${RUN_ID} to end (up to ${RUN_SECONDS}s)"
}

read_row() {
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/rows.out" "$SCRATCH/rows.err" \
    "$FUGARO_BIN" runs ls --repo "${LAUNCH_REPO:-$REPO}" --json --limit "$RUNS_LIMIT" || status=$?
  [ "$status" = 0 ] || return 1
  local answer
  answer=$(reader row "$SCRATCH/rows.out" "$RUN_ID") || return 1
  ROW_STAGE=$(field_of stage "$answer")
  ROW_STATUS=$(field_of status "$answer")
  ROW_TERMINAL=$(field_of terminal "$answer")
  [ "$(field_of found "$answer")" = yes ] || return 1
  return 0
}

# send_message FLAG — one `fugaro tell`, as the person: the text is this check's
# own sentence and never anything of yours, and --json is how the number the
# message took is read.
TELL_SEQ=""
TELL_ERR=""
send_message() { # send_message FLAG
  local flag=$1 status=0
  TELL_SEQ=""
  run_call "$CALL_SECONDS" "$SCRATCH/tell.out" "$SCRATCH/tell.err" \
    "$FUGARO_BIN" tell "$RUN_ID" ${flag:+"$flag"} --json "check 740: this message is part of a live check; ignore it and carry on with your task" || status=$?
  if [ "$status" != 0 ]; then
    TELL_ERR="$(last_line "$SCRATCH/tell.err")"
    return 1
  fi
  local answer
  answer=$(python3 - "$SCRATCH/tell.out" <<'PY' 2>/dev/null || true
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
seq = doc.get("seq")
if isinstance(seq, int):
    print("seq=%d" % seq)
PY
)
  TELL_SEQ=$(field_of seq "$answer")
  if [ -z "$TELL_SEQ" ]; then
    TELL_ERR="fugaro tell --json printed no number for the message (is this fugaro too old to print --json?)"
    return 1
  fi
  return 0
}

tell_error() {
  if [ -n "$TELL_ERR" ]; then printf '%s' "$TELL_ERR"; else printf 'fugaro tell printed no reason'; fi
}

# step_end: the run has to end. Every fate below is read from a record the run
# has finished writing — a record read while the run is still working is
# whatever was there before it, and the delivery of a message sent a moment ago
# is not in it yet.
step_end() {
  if [ -z "$RUN_ID" ]; then
    unmeasured run-ended "nothing was launched, so there is no run to follow: the row above says why"
    return 0
  fi
  local waited=0
  while [ "$waited" -lt "$RUN_SECONDS" ]; do
    if read_row && [ "$ROW_TERMINAL" = yes ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if ! read_row || [ "$ROW_TERMINAL" != yes ]; then
    unmeasured run-ended "the run had not ended after ${RUN_SECONDS}s (last row: stage=${ROW_STAGE:-unknown}, status=${ROW_STATUS:-unknown}), so its record was NOT read: a record read while a run is still working is whatever was there before. This is not a finding about the message channel — the rows below are the ones it leaves unmeasured. Watch it with 'fugaro watch ${RUN_ID}'"
    return 0
  fi
  RUN_ENDED=yes
  fact run-ended PASS "${RUN_REF} ended in ${waited}s, status ${ROW_STATUS:-unknown} — read from 'fugaro runs ls --json'. Every fate below is this run's own record whatever its ending: what the messages became is the runner's statement, and a run that failed after delivering them still delivered them"
}

# step_record: the run's record, read from the runs bucket as the owner would
# read it. Its own run_id is what ties it to the run this check launched, and its
# `execution` is what names the job whose account the last two rows ask about.
step_record() {
  if [ -z "$RUN_ID" ]; then
    unmeasured record "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$RUN_ENDED" != yes ]; then
    unmeasured record "the run had not ended when this check's own bound ran out, and a record read while a run is still working is whatever was there before: the record was NOT read, so no fate below it was either. The run is not cancelled by this check stopping — watch it with 'fugaro watch ${RUN_ID}' and run this check again, or read its record yourself with 'gcloud storage cat gs://${BUCKET}/runs/${SLUG}/${RUN_ID}/result.json'"
    return 0
  fi
  local key="runs/${SLUG}/${RUN_ID}/result.json"
  say "reading the record: gcloud storage cat gs://${BUCKET}/${key}"
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/record.out" "$SCRATCH/record.err" \
    gcloud storage cat "gs://${BUCKET}/${key}" --project="$PROJECT" || status=$?
  if [ "$status" != 0 ]; then
    fact record FAIL "gs://${BUCKET}/${key} could not be read (exit ${status}: $(last_line "$SCRATCH/record.err")): a run that ended has a record, and the record is where the runner writes what became of every message. This check needs an account that may read the runs bucket"
    return 0
  fi
  if ! reader record "$SCRATCH/record.out" >"$SCRATCH/record.txt" 2>"$SCRATCH/record.readerr"; then
    fact record FAIL "${key} is not a document this check could read ($(last_line "$SCRATCH/record.readerr")): what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  REC=$(cat "$SCRATCH/record.txt")
  local recorded
  recorded=$(field_of run_id "$REC")
  if [ "$recorded" != "$RUN_ID" ]; then
    fact record FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  EXECUTION=$(field_of execution "$REC")
  ACCEPTING=$(field_of accepting "$REC")
  # The stage the run was in when it ended and how it ended: the one reading a
  # fate cannot give on its own, because an entry is written when a stop claims
  # a message and before the turn it resumes runs (the --now row).
  RECORD_STAGE=$(field_of stage "$REC")
  RECORD_STATUS=$(field_of status "$REC")
  # The object the plain message really is, when the record names it. It is read
  # here to NAME the object in the rows and never to build a command from: the
  # plan's Never list forbids a copy-paste command built from result.json, and
  # the resource the Troubleshooter is asked about is the bucket anyway.
  if [ -n "${SEQ_PLAIN:-}" ]; then
    local object
    object=$(msg_field "$SEQ_PLAIN" 6 "$SCRATCH/record.txt")
    if [ -n "$object" ]; then MESSAGE_KEY=$object; fi
  fi
  if [ "$ACCEPTING" != yes ]; then
    fact record FAIL "${key} carries no accepting messages block (accepting=${ACCEPTING:-none}), so this run's runner reads no message at all: it is a runner from before ${RELEASE}, or a recipe with no coder stage, and the two messages this check sent were refused or will never be read. The run's job image is built from the release the repository is on — 'fugaro update' rebuilds it"
    return 0
  fi
  RECORD_READ=yes
  fact record PASS "${key} records ${RUN_ID} (accepting=${ACCEPTING}, format=$(field_of format "$REC"), flags=$(field_of flags "$REC"), $(field_of messages "$REC") message(s)), read from the bucket as you would read it. The runner is on ${RELEASE} or later and reads messages"
}

# step_messages: what became of the two messages, from the run's own record.
#
# The plain message's criterion is rule 1: written while the run was going, it
# reaches a coder stage. The `--now` message's criterion is the quiet point
# (U2/U3): its fate carries NO reason exactly where the stop found a quiet point
# inside implement — the runner writes `no quiet point in 10m` when its bound was
# reached, and `session lost at forced stop` when a forced stop left a session
# that could not be resumed and the stage went on in a fresh one — and each of
# those is quoted as it stands.
#
# An empty reason is NOT on its own the statement that the session resumed. The
# entry is written by claimYield BEFORE the turn it resumes runs, and the
# session-lost reason is written afterwards by retagYield, for a FORCED stop
# only (internal/runner/yield.go). A quiet-point stop whose resume failed with
# agent.ErrNoSession — a failure a first run's implement has no recovery for —
# therefore leaves delivered/implement/reason="" and ends the run there: the
# record is exactly that of a resume that went on. What the row needs besides
# the fate is the run's own ending, which is why the record's stage and status
# are read with it: a run that was still in implement when it ended cannot be
# read as U2 and U3 holding, and the row says so rather than claiming them.
step_messages() {
  if [ "$RECORD_READ" != yes ]; then
    unmeasured message-plain "the run's own record was NOT read — the record row above says why — and a fate can be read only from the record the runner wrote. Nothing about this message is concluded here"
    unmeasured message-now "the run's own record was NOT read — the record row above says why — so whether a quiet point was found (U2, U3) was NOT measured"
    return 0
  fi
  if [ -z "${SEQ_PLAIN:-}" ]; then
    unmeasured message-plain "no plain message was sent, so the record holds no fate for one: the rows above say why"
  else
    local state stage
    state=$(msg_field "$SEQ_PLAIN" 2 "$SCRATCH/record.txt")
    stage=$(msg_field "$SEQ_PLAIN" 3 "$SCRATCH/record.txt")
    if [ -z "$state" ]; then
      fact message-plain FAIL "the record carries no entry for message ${SEQ_PLAIN}, which this check sent: every message the run's runner read has an entry, so the block it wrote is not this run's own. Read it yourself with 'fugaro runs show ${RUN_ID} --json'"
    else
      case "$state:$stage" in
      delivered:*)
        if [ "${CODER_STAGES#* $stage }" != "$CODER_STAGES" ]; then
          fact message-plain PASS "message ${SEQ_PLAIN} (no flag) is delivered into the ${stage} stage — a coder stage, which is what rule 1 allows a message to reach. The fate the record carries: seq=${SEQ_PLAIN}, state=${state}, stage=${stage}, flags=$(msg_field "$SEQ_PLAIN" 4 "$SCRATCH/record.txt")"
        else
          fact message-plain FAIL "message ${SEQ_PLAIN} (no flag) reached the '${stage}' stage: a message may be delivered only into a coder stage — a first run's implement, a follow-up's implement, or a fix (rule 1, D2942) — so a reviewer read the person's words, which is what the fence exists to prevent"
        fi
        ;;
      undelivered:*)
        fact message-plain FAIL "message ${SEQ_PLAIN} (no flag) was never delivered: the record's reason is '$(msg_field "$SEQ_PLAIN" 5 "$SCRATCH/record.txt")', which is the runner saying no coder stage was left to carry it. A message sent into a running implement stage rides the same stop as the --now message beside it, so this is the channel losing a message that was pending while a coder stage was going — read the record yourself with 'fugaro runs show ${RUN_ID} --json'"
        ;;
      refused:* | missing:*)
        fact message-plain FAIL "message ${SEQ_PLAIN} (no flag) reads ${state} in the record: the runner refused it or could not read the object at all. The reason it wrote: '$(msg_field "$SEQ_PLAIN" 5 "$SCRATCH/record.txt")'"
        ;;
      *)
        fact message-plain FAIL "message ${SEQ_PLAIN} (no flag) reads state='${state}' in the record, which is not one of the channel's states (delivered, undelivered, refused, missing): the block this check read is not the block ${RELEASE}'s runner writes"
        ;;
      esac
    fi
  fi

  if [ -z "${SEQ_NOW:-}" ]; then
    unmeasured message-now "no --now message was sent, so the record holds no fate for one: the rows above say why"
    return 0
  fi
  local state stage reason
  state=$(msg_field "$SEQ_NOW" 2 "$SCRATCH/record.txt")
  stage=$(msg_field "$SEQ_NOW" 3 "$SCRATCH/record.txt")
  reason=$(msg_field "$SEQ_NOW" 5 "$SCRATCH/record.txt")
  if [ -z "$state" ]; then
    fact message-now FAIL "the record carries no entry for message ${SEQ_NOW}, which this check sent with --now: every message the run's runner read has an entry, so the block it wrote is not this run's own. Read it yourself with 'fugaro runs show ${RUN_ID} --json'"
    return 0
  fi
  if [ "$state" != delivered ]; then
    if [ "$state" = undelivered ]; then
      fact message-now FAIL "the --now message ${SEQ_NOW} was never delivered: the record's reason is '${reason}'. A --now message asks for the agent's next quiet point INSIDE the coder stage that is running, so a run that had already left its coder stages by the time the message was read leaves the quiet point unexercised. Whether U2 and U3 hold is NOT settled by this run — read the record yourself with 'fugaro runs show ${RUN_ID} --json'"
    else
      fact message-now FAIL "the --now message ${SEQ_NOW} reads ${state} in the record: the runner refused it or could not read the object at all. The reason it wrote: '${reason}'"
    fi
    return 0
  fi
  if [ -n "$reason" ]; then
    case "$reason" in
    "no quiet point in 10m")
      fact message-now FAIL "the --now message ${SEQ_NOW} reached ${stage}, and the record's own reason is '${reason}': the runner waited its bound for a quiet point the rule reads (a turn that ends between two tool calls) and never saw one, so it stopped the agent at its bound instead. That is U3's NEGATIVE — the real binary's stream-json events did not mark a quiet point where the rule expects one — and the session it left may not be resumable (rule 2's fallback). Whether a quiet point is marked is settled only here, against the real binary (docs/run-messages/reconciliation.md U3)"
      ;;
    "session lost at forced stop")
      fact message-now FAIL "the --now message ${SEQ_NOW} reached ${stage}, and the record's own reason is '${reason}': the turn after the stop could NOT continue in the session the stage was running in, and the run wrote that it went on in a fresh one. That is U2's NEGATIVE — a session stopped at a quiet point did not resume cleanly on this escalation to the real binary — and the message still reached the stage, in a session that had lost the turns before it"
      ;;
    *)
      fact message-now FAIL "the --now message ${SEQ_NOW} reached ${stage} with the record's reason '${reason}': the runner writes a reason on a fate exactly where a stop did NOT find a quiet point, and this check does not know this one. Read the runner's own words in $RELEASE (internal/runner/yield.go) against it"
      ;;
    esac
    return 0
  fi
  case "$stage" in
  implement)
    # What the fate cannot say on its own, and what the row therefore reads with
    # it: the run's ending. The row is measured only where the record shows the
    # run going PAST the stage the message reached — implement behind it, which
    # is the runner's own statement that the turn after the stop ran. `ended_in`
    # names why it did not, in the two shapes that leave the record unable to say
    # so: a record that still names implement as its own stage (the run stopped
    # while the stage the stop delivered into was running — the one ending an
    # empty reason can hide a failed resume behind, because the entry is written
    # by claimYield before that turn runs and retagYield's session-lost reason
    # only ever follows a FORCED stop), and a record with no stage to read.
    local ended_in=""
    if [ -z "$RECORD_STAGE" ]; then
      ended_in="it carries no stage this check can read"
    elif [ "$RECORD_STAGE" = implement ]; then
      ended_in="it ends at stage=implement, status=${RECORD_STATUS:-unreadable}"
    fi
    if [ -n "$ended_in" ]; then
      unmeasured message-now "the --now message ${SEQ_NOW} reached implement with NO reason on its fate, but this run's own record does not show the run going past that stage: ${ended_in}. The record therefore cannot say whether the turn after the quiet point ran in the SAME session or the resume never happened at all — an empty reason is written BEFORE that turn runs (internal/runner/yield.go, claimYield), and the session-lost reason is written after it fails, for a FORCED stop only (retagYield), so a quiet-point stop whose resume failed with agent.ErrNoSession leaves exactly this fate: delivered into implement with no reason, with the run ending failed there. U2 and U3 are NOT settled by this run, and nothing here reads as them holding: read the record yourself with 'fugaro runs show ${RUN_ID} --json', and run the check again"
    else
      fact message-now PASS "the --now message ${SEQ_NOW} is delivered into implement with NO reason on its fate, and the run went on PAST that stage — its record ends at stage=${RECORD_STAGE}, status=${RECORD_STATUS} — which is the runner's own statement that the turn after the stop really ran. The reason is written only where a quiet point was missed (no quiet point in 10m) or a FORCED stop's session could not be resumed (session lost at forced stop); the fate alone cannot say the turn after it ran, because the entry is written before that turn does (internal/runner/yield.go, claimYield), which is why the run's own ending is read with it. U2 and U3 hold on this escalation: the stop found a quiet point inside implement as the rule reads it, and the turn after it ran in the SAME session, which is what the ending past implement says"
    fi
    ;;
  *)
    if [ "${CODER_STAGES#* $stage }" != "$CODER_STAGES" ]; then
      unmeasured message-now "the --now message ${SEQ_NOW} reached the ${stage} stage with no reason on its fate — a boundary delivery, which is the next coder stage's own delivery and NOT the quiet point this row asks about (the flag was read after implement had already ended). The quiet point is a stop INSIDE the coder stage that is running, so this run did not exercise it and U2/U3 are NOT settled here. Send it earlier: run the check again, or read the record yourself with 'fugaro runs show ${RUN_ID} --json'"
    else
      fact message-now FAIL "the --now message ${SEQ_NOW} reached the '${stage}' stage with no reason on its fate, and that stage is not a coder stage: a message may be delivered only into implement or a fix (rule 1, D2942), so a reviewer read the person's words"
    fi
    ;;
  esac
}

# step_workflow_iam: D2941's fence, asked about the account the run REALLY ran
# as. The record's own `execution` names the job and the location it ran in, and
# `gcloud run jobs describe` names that job's service account; the Policy
# Troubleshooter is then asked, for that account and not for a throwaway one,
# whether it holds `storage.objects.create` on the runs bucket. It must be
# denied: a job account holds `roles/storage.objectViewer` on tokens/ and nothing
# more (D2941), and that role carries no create at all. The question is at BUCKET
# level because the Troubleshooter refuses an object-level name; the object-name
# fence is read off the answer's explained binding condition, which the row
# reports as it stands.
step_workflow_iam() {
  if [ -z "${EXECUTION:-}" ]; then
    unmeasured iam-workflow-job "the record carries no execution, so this check cannot name the job the run landed on and cannot ask about its account. Read it yourself with 'gcloud storage cat gs://${BUCKET}/runs/${SLUG:-<slug>}/${RUN_ID:-<run>}/result.json'"
    return 0
  fi
  EXEC_PROJECT=$(sed -n 's|^projects/\([^/]*\)/.*|\1|p' <<<"$EXECUTION")
  EXEC_LOCATION=$(sed -n 's|^projects/[^/]*/locations/\([^/]*\)/.*|\1|p' <<<"$EXECUTION")
  EXEC_JOB=$(sed -n 's|^projects/[^/]*/locations/[^/]*/jobs/\([^/]*\)/.*|\1|p' <<<"$EXECUTION")
  if [ -z "$EXEC_JOB" ]; then
    unmeasured iam-workflow-job "the record's execution ('${EXECUTION}') is not the canonical name this check reads (projects/<project>/locations/<location>/jobs/<job>/executions/<execution>), so the job the run landed on cannot be named"
    return 0
  fi
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/job.out" "$SCRATCH/job.err" \
    gcloud run jobs describe "$EXEC_JOB" --project="${EXEC_PROJECT:-$PROJECT}" --region="${EXEC_LOCATION:-$REGION}" --format=json || status=$?
  if [ "$status" != 0 ]; then
    unmeasured iam-workflow-job "'gcloud run jobs describe ${EXEC_JOB}' did not answer (exit ${status}: $(last_line "$SCRATCH/job.err")), so the account the run really ran as was NOT read and D2941's fence was NOT asked about it. This check needs an account that may read Cloud Run jobs on ${EXEC_PROJECT:-$PROJECT} (roles/run.viewer or better). Run it yourself: gcloud run jobs describe ${EXEC_JOB} --project=${EXEC_PROJECT:-$PROJECT} --region=${EXEC_LOCATION:-$REGION} --format=json"
    return 0
  fi
  WORKFLOW_SA=$(field_of sa "$(reader job "$SCRATCH/job.out" 2>/dev/null || true)")
  [[ "$WORKFLOW_SA" == *@*".iam.gserviceaccount.com" ]] || {
    unmeasured iam-workflow-job "the job ${EXEC_JOB} answered without a service account this check can ask about (sa='${WORKFLOW_SA:-none}'), so whether the account the run really ran as may write a message object was NOT measured"
    return 0
  }
  ask_troubleshooter "$WORKFLOW_SA" "$SCRATCH/ts-job"
  if [ -z "$TS_DECISION" ]; then
    unmeasured iam-workflow-job "the Policy Troubleshooter did not answer for the job's own account ${WORKFLOW_SA} (${TS_NOTE:-no answer}), so D2941's fence was NOT measured. ${TS_ASK}"
    return 0
  fi
  case "$TS_DECISION" in
  NOT_GRANTED)
    fact iam-workflow-job PASS "the IAM Policy Troubleshooter says NOT_GRANTED for storage.objects.create by ${WORKFLOW_SA} — the job's OWN service account, the one the run this check launched really ran as (${EXEC_JOB}) — on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}. That is D2941's fence read off the installation's own account and not off a throwaway one: a job account holds roles/storage.objectViewer on tokens/, which carries no write at all. The question is asked at BUCKET level — the Troubleshooter refuses an object-level resource name (INVALID_RESOURCE_NAME), so the object-name condition is not evaluated by this answer — and ${TS_FENCE}${TS_NOTE:+ (${TS_NOTE})}. The object the fence is about is ${MESSAGE_KEY}"
    ;;
  GRANTED)
    fact iam-workflow-job FAIL "the IAM Policy Troubleshooter says GRANTED for storage.objects.create by ${WORKFLOW_SA} — the account the run this check launched really ran as — on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}: an agent running as its job account can write an object on the bucket the messages live in, and, on a shared project job, into another repository's run. D2941's whole fence is that this is denied (its grant on tokens/ is objectViewer, gcp.TokenBucketCondition, which carries no create at all); the question is at BUCKET level, so ${TS_FENCE}. Read the bucket's IAM with 'gcloud storage buckets get-iam-policy gs://${BUCKET} --project=${PROJECT}'${TS_NOTE:+ (${TS_NOTE})}"
    ;;
  *)
    unmeasured iam-workflow-job "the IAM Policy Troubleshooter answered '${TS_DECISION}' for storage.objects.create by ${WORKFLOW_SA} on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}, which is neither GRANTED nor NOT_GRANTED: the answer this check reads did not settle D2941's fence. Asked at BUCKET level, ${TS_FENCE}${TS_NOTE:+ (${TS_NOTE})}"
    ;;
  esac
}

# step_project_iam: the same question about a shared PROJECT job's account,
# where the installation deploys one. Its reach is wider — roles/
# storage.objectViewer on the whole of tokens/, every repository's — so it is
# worth asking about separately, and its absence is a fact of the installation
# rather than a row this check failed to measure.
step_project_iam() {
  if [ -z "${PROJECT:-}" ]; then
    unmeasured iam-project-job "the installation's GCP project was not read, so its jobs could not be listed: the installation row says why"
    return 0
  fi
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/jobs.out" "$SCRATCH/jobs.err" \
    gcloud run jobs list --project="$PROJECT" --region="${REGION:-}" --limit="$JOBS_LIMIT" --format=json || status=$?
  if [ "$status" != 0 ]; then
    unmeasured iam-project-job "'gcloud run jobs list' did not answer (exit ${status}: $(last_line "$SCRATCH/jobs.err")), so whether this installation deploys a shared project job was NOT read. Run it yourself: gcloud run jobs list --project=${PROJECT} --region=${REGION:-<the region doctor records>} --format=json"
    return 0
  fi
  local answer
  answer=$(reader jobs "$SCRATCH/jobs.out" 2>/dev/null || true)
  PROJECT_JOBS=$(field_of count "$answer")
  if [ "${PROJECT_JOBS:-0}" = 0 ]; then
    fact iam-project-job PASS "this installation deploys NO shared project job ('gcloud run jobs list' answered no job carrying the profile label), so there is no project job account to ask about. D2941's wider case — a project job account holds roles/storage.objectViewer on the whole of tokens/, every repository's messages — is not reachable on this installation, which is the fact the row states rather than a question left unasked"
    return 0
  fi
  local line name sa
  line=$(printf '%s\n' "$answer" | grep -m1 '^job=' || true)
  name=$(printf '%s' "$line" | cut -d'|' -f1 | sed 's/^job=//')
  sa=$(printf '%s' "$line" | cut -d'|' -f2)
  case "$sa" in
  *@*.iam.gserviceaccount.com) ;;
  *)
    unmeasured iam-project-job "the installation deploys the shared project job ${name:-unknown}, and it answered without a service account this check can ask about (sa='${sa:-none}'): D2941's wider case (${PROJECT_JOBS} project job(s) deployed) was NOT measured"
    return 0
    ;;
  esac
  ask_troubleshooter "$sa" "$SCRATCH/ts-project"
  if [ -z "$TS_DECISION" ]; then
    unmeasured iam-project-job "the Policy Troubleshooter did not answer for the shared project job's account ${sa} (${TS_NOTE:-no answer}), so D2941's wider case was NOT measured. ${TS_ASK}"
    return 0
  fi
  case "$TS_DECISION" in
  NOT_GRANTED)
    fact iam-project-job PASS "the IAM Policy Troubleshooter says NOT_GRANTED for storage.objects.create by ${sa} — the account of ${name}, the installation's own shared project job (of ${PROJECT_JOBS} deployed) — on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}. A project job's grant on tokens/ is roles/storage.objectViewer with no slug (gcp.ProjectTokenBucketCondition), so the wider reach it carries is over reads and never over writes. The question is asked at BUCKET level — the Troubleshooter refuses an object-level resource name (INVALID_RESOURCE_NAME), so the object-name condition is not evaluated by this answer — and ${TS_FENCE}${TS_NOTE:+ (${TS_NOTE})}"
    ;;
  GRANTED)
    fact iam-project-job FAIL "the IAM Policy Troubleshooter says GRANTED for storage.objects.create by ${sa}, the account of the shared project job ${name}, on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}: an agent on a shared project job can write an object into ANOTHER repository's running run — the reach D2941 exists to bound. The question is at BUCKET level, so ${TS_FENCE}. Read the bucket's IAM with 'gcloud storage buckets get-iam-policy gs://${BUCKET} --project=${PROJECT}'${TS_NOTE:+ (${TS_NOTE})}"
    ;;
  *)
    unmeasured iam-project-job "the IAM Policy Troubleshooter answered '${TS_DECISION}' for storage.objects.create by ${sa} (the shared project job ${name}) on the runs bucket //storage.googleapis.com/projects/_/buckets/${BUCKET}, which is neither GRANTED nor NOT_GRANTED: the answer did not settle D2941's wider case. Asked at BUCKET level, ${TS_FENCE}${TS_NOTE:+ (${TS_NOTE})}"
    ;;
  esac
}

# ask_troubleshooter PRINCIPAL PREFIX — the one call this check makes that is not
# a plain read, and it reads only: the IAM Policy Troubleshooter answers whether
# a principal holds a permission on a resource, and changes nothing. The command
# group is `policy-troubleshoot` (`gcloud policy-troubleshoot iam RESOURCE`), the
# RESOURCE is a POSITIONAL argument — `--resource=` is not a flag gcloud has, and
# a call that passes one fails with 'unrecognized arguments' — and it is the
# BUCKET, because the Troubleshooter refuses an object-level full resource name
# (INVALID_ARGUMENT INVALID_RESOURCE_NAME, accessTuple.fullResourceName) and
# accepts `//storage.googleapis.com/projects/_/buckets/<bucket>`. The object-name
# fence the plan pins (the tokens/ prefix) is therefore not evaluated at the
# level asked: what the answer explains is the binding's own condition, and the
# rows report that text rather than a verdict about an object. The call is tried
# on gcloud's GA surface first and on its alpha surface after, because the verb
# has moved between them across gcloud releases: a gcloud that does not know the
# GA verb says so at once, and an answer that is not a document is reported as
# the note rather than as a fence that is absent. `--quiet` is passed on both
# surfaces: without it the call stops at a prompt a session with no terminal
# cannot answer, which is how every row here came out UNVERIFIED before the flag
# was added.
TS_DECISION=""
TS_NOTE=""
TS_ASK=""
TS_FENCE=""
ask_troubleshooter() { # ask_troubleshooter PRINCIPAL PREFIX
  local principal=$1 prefix=$2 status=0 verb
  local resource="//storage.googleapis.com/projects/_/buckets/${BUCKET}"
  TS_DECISION=""
  TS_NOTE=""
  TS_FENCE=""
  # What the owner is handed when the answer does not come. It is built from the
  # bucket the installation records and the account the documents name, never
  # from a value read out of the run's record.
  TS_ASK="Ask it yourself: gcloud policy-troubleshoot iam ${resource} --permission=storage.objects.create --principal-email=${principal} --project=${PROJECT} --format=json --quiet"
  for verb in "" "alpha "; do
    status=0
    run_call "$CALL_SECONDS" "$prefix.out" "$prefix.err" \
      gcloud ${verb}policy-troubleshoot iam "$resource" \
      --permission=storage.objects.create \
      --principal-email="$principal" \
      --project="$PROJECT" --format=json --quiet || status=$?
    [ "$status" = 0 ] && break
    # A gcloud that does not know the verb at all says so on its own first line;
    # only then is the alpha surface tried, so a real refusal is not retried.
    grep -qiE 'unknown command|Invalid choice|not found|no such group' "$prefix.err" || break
  done
  if [ "$status" != 0 ]; then
    # An API that was never enabled on the project is the one refusal whose fix
    # is another command rather than another permission: the row says which one.
    if grep -qiE 'SERVICE_DISABLED|has not been used in project|is not enabled|is disabled' "$prefix.err" 2>/dev/null; then
      local reason=""
      grep -qi 'SERVICE_DISABLED' "$prefix.err" 2>/dev/null && reason=", reason SERVICE_DISABLED"
      TS_NOTE="the Policy Troubleshooter API (policytroubleshooter.googleapis.com) is NOT enabled on ${PROJECT}${reason} (gcloud exited ${status}: $(last_line "$prefix.err"))"
      TS_ASK="Enable it first: gcloud services enable policytroubleshooter.googleapis.com --project=${PROJECT} — then ask: gcloud policy-troubleshoot iam ${resource} --permission=storage.objects.create --principal-email=${principal} --project=${PROJECT} --format=json --quiet"
    else
      TS_NOTE="gcloud exited ${status}: $(last_line "$prefix.err")"
    fi
    return 0
  fi
  local answer
  answer=$(reader troubleshoot "$prefix.out" 2>/dev/null || true)
  TS_DECISION=$(field_of decision "$answer")
  TS_FENCE=$(field_of fence "$answer")
  case "$TS_DECISION" in
  "" | none)
    TS_DECISION=""
    TS_NOTE="the answer carried no decision this check knows (errors=$(field_of errors "$answer"), $(field_of problem "$answer"))"
    ;;
  *)
    TS_NOTE="errors=$(field_of errors "$answer")"
    ;;
  esac
  # The object-name fence is not evaluated at bucket level, so what the row can
  # honestly report is the condition text the answer explains; where it explains
  # none, the row says the fence was not read off it rather than implying a
  # verdict about the object.
  if [ -z "$TS_FENCE" ]; then
    TS_FENCE="the answer explained no binding condition whose text this check can read, so the tokens/ prefix fence was NOT read off it"
  else
    TS_FENCE="the explained binding's own condition reads: ${TS_FENCE}"
  fi
}

# step_lifecycle: U4, on the applied bucket. The message objects live under
# tokens/, which every job account may only read, so they are kept by a rule of
# their own: a Delete rule whose prefix is tokens/ and whose suffix is the
# store's own `.msg.json`. GCS matches a prefix and a suffix and never a middle
# segment, which is the whole of what keeps that rule off the sealed budget
# token beside it — so the reading is both halves: the rule selects a message
# object, and it does not select a budget-token.
step_lifecycle() {
  if [ -z "${BUCKET:-}" ]; then
    unmeasured lifecycle "the runs bucket was not read, so its lifecycle configuration could not be: the installation row says why"
    return 0
  fi
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/bucket.out" "$SCRATCH/bucket.err" \
    gcloud storage buckets describe "gs://${BUCKET}" --project="$PROJECT" --format=json || status=$?
  if [ "$status" != 0 ]; then
    unmeasured lifecycle "'gcloud storage buckets describe gs://${BUCKET}' did not answer (exit ${status}: $(last_line "$SCRATCH/bucket.err")), so the bucket's lifecycle configuration was NOT read and U4 was NOT measured. This check needs an account that may read the bucket's metadata (roles/storage.objectViewer on gs://${BUCKET}, or the bucket's own describe permission). Run it yourself: gcloud storage buckets describe gs://${BUCKET} --project=${PROJECT} --format=json"
    return 0
  fi
  local answer
  answer=$(reader lifecycle "$SCRATCH/bucket.out" 2>/dev/null || true)
  local rules msg_rule action age selects_token
  rules=$(field_of rules "$answer")
  msg_rule=$(field_of msg_rule "$answer")
  if [ "$msg_rule" != yes ]; then
    unmeasured lifecycle "gs://${BUCKET} carries ${rules:-no} lifecycle rule(s) and none of them selects a message object (tokens/<slug>/<run>/messages/NNNN.msg.json): U4 is NOT settled by this bucket — a message object is plaintext, a person's own words, and a bucket with no rule for it keeps it until something deletes it. RM6's terraform test pins the rule this release deploys, and 'fugaro update' is what applies it. Read the bucket yourself: gcloud storage buckets describe gs://${BUCKET} --project=${PROJECT} --format=json"
    return 0
  fi
  action=$(field_of msg_action "$answer")
  age=$(field_of msg_age "$answer")
  selects_token=$(field_of msg_selects_token "$answer")
  local token_rule
  token_rule=$(field_of token_rule "$answer")
  if [ "$selects_token" = yes ]; then
    fact lifecycle FAIL "gs://${BUCKET}'s message rule (action ${action}, age ${age}, prefix tokens/, suffix .msg.json) ALSO selects the sealed budget token beside it at tokens/<slug>/<run>/budget-token: the same rule would delete a queued run's token, which is that run's Realtime Database identity. GCS matches a prefix and a suffix and never a middle segment (modules/installation/bucket.tf), so this is the suffix having been widened to something a budget-token name also ends with"
    return 0
  fi
  if [ "$action" != Delete ]; then
    fact lifecycle FAIL "gs://${BUCKET}'s message rule selects a message object and its action is '${action}', not Delete (age ${age}): a message object is worth keeping while the run it steered is recent and no longer, and U4's rule is the Delete rule this release deploys (docs/run-messages D2941, F11)"
    return 0
  fi
  fact lifecycle PASS "gs://${BUCKET}'s lifecycle rule that selects tokens/<slug>/<run>/messages/NNNN.msg.json is a Delete rule (age ${age} days, prefix tokens/, suffix .msg.json) and it does NOT select the sealed budget token beside it (the bucket also carries a budget-token rule: ${token_rule:-no}). U4 holds on the applied bucket: a message object goes at the message age and the token rule reaches its own object only"
}

# ------------------------------------------------------------------- the block
BLOCK_KEYS=(installation belong release bound run-launched message-plain message-now run-ended record iam-workflow-job iam-project-job lifecycle agent-session xtrace restore)

criteria_unmet() {
  local facts=$1 key line
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    case "$line" in
    "$key|PASS|"*) ;;
    *) printf '%s\n' "$key" ;;
    esac
  done
}

# verdict_of FACTS STATUS — the block's own last word, and the one the exit
# status carries: 0 is PASS, 1 is FAIL, and both 2 (the run stopped before it
# could measure everything) and 3 (it finished with criteria left unsettled) are
# INCOMPLETE.
verdict_of() {
  case "$2" in
  0) printf 'PASS' ;;
  1) printf 'FAIL' ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 740 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached: nothing measured this criterion (the step that writes this row did not run, or the check stopped before it)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 740 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  # A variable rather than a ${RECIPE:-...} default: an apostrophe inside a
  # default is read as a quote by the shell's substitution parser, which then
  # never finds the closing brace.
  local recipe_shown="unset — the repository's own recipe is used"
  if [ -n "$RECIPE" ]; then recipe_shown=$RECIPE; fi
  cat <<PLAN_EOF
Check 740 — messaging a run that is going. This is the PLAN. --dry-run touches
nothing: nothing is read, no call is made, no run is launched and no file of
yours is opened.

  installation  ${INSTALLATION}  (the local config in force must answer it; 'belong' is refused)
  repository    ${REPO}  (launched by name, so no checkout of it is needed)
  release       ${RELEASE} or later  (the release the run-messages channel ships in)
  recipe        ${recipe_shown}

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --json' and refuse it if it is not
     ${INSTALLATION}, or if the installation, the repository or its GCP project names
     'belong';
  2. probe the build offline — 'fugaro version' and 'fugaro tell --help' — and stop
     if it carries no run-messages channel, because the messages below could not be
     sent at all;
  3. launch ONE real run of ${REPO} with 'fugaro run --repo ${REPO} --json', whose task asks
     for one small file: the run pushes a branch and opens a pull request, and its
     own spend is the money this check commits;
  4. wait for that run's implement stage ('fugaro runs ls --json'), send it a plain
     message with 'fugaro tell <run> "…"', wait a few seconds, and send it a second
     message with --now: the plain one is pending when the stop fires, so the stop
     carries both into the same resumed turn;
  5. wait for the run to end, then read its own record from the runs bucket
     (runs/<slug>/<run>/result.json) and say what became of each message: the plain
     one must be delivered into a coder stage, and the --now one must be delivered
     into implement with NO reason on its fate — which is the runner's own statement
     that the stop found a quiet point and the same session resumed (U2 and U3), and
     the row quotes the reason it wrote instead when it did not;
  6. read the record's 'execution', describe that job with 'gcloud run jobs
     describe' and take the service account it really ran as, then ask the IAM
     Policy Troubleshooter whether THAT account holds storage.objects.create on
     the runs bucket: it must be denied (D2941 — a job account's grant on tokens/ is
     objectViewer, which carries no write). The question is asked of the BUCKET
     (gcloud policy-troubleshoot iam <resource> takes the resource positionally,
     and the Troubleshooter refuses an object-level name), so the object-name
     fence is read off the condition the answer explains and the row reports it;
  7. ask the same question about the account of a shared project job, where
     'gcloud run jobs list' shows the installation deploys one (its reach on tokens/
     is wider and still read-only);
  8. read the bucket's lifecycle configuration ('gcloud storage buckets describe')
     and confirm the Delete rule that selects .msg.json under tokens/ — and that it
     does NOT select the sealed budget token beside it (U4);
  9. write nothing of its own: every call above is a read or the two messages, the
     run it launched is left as it is (its branch and pull request stay), and the
     scratch directory — the task file and every document it read — is removed on
     the way out, however the run ends.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
usage() {
  printf 'usage: %s\n' "$USAGE"
  printf '\n'
  printf 'Check 740 — messaging a run that is going, on a real installation. It launches\n'
  printf 'ONE real run of the repository you name, sends it a plain message and a --now\n'
  printf 'message while its implement stage is going, reads what the run'"'"'s own record\n'
  printf 'says became of them (U2, U3), and asks the IAM Policy Troubleshooter about the\n'
  printf 'account the run really ran as (D2941) and about the bucket'"'"'s message lifecycle\n'
  printf 'rule (U4).\n'
  printf '\n'
  printf '  --installation NAME  the installation to read (the local config in force must answer it; belong is refused)\n'
  printf '  --repo OWNER/NAME    the repository the small run is launched in, as owner/name (a REAL\n'
  printf '                       launch: it pushes a branch and opens a pull request there)\n'
  printf '  --recipe NAME        the recipe that run uses (default: the repository'"'"'s own)\n'
  printf '  --dry-run            print the plan and touch nothing\n'
  printf '\n'
  printf 'The full description, the exit codes and every knob (CHECK740_*) are the comment\n'
  printf 'block at the top of this file: read it before you run the check for real.\n'
}

parse_args() {
  INSTALLATION=${CHECK740_INSTALLATION:-}
  REPO=${CHECK740_REPO:-}
  RECIPE=${CHECK740_RECIPE:-}
  DRY_RUN=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a repository as owner/name. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --recipe)
      [ $# -ge 2 ] || die "--recipe needs a recipe name. usage: $USAGE"
      RECIPE=$2
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
  [ -n "$INSTALLATION" ] || die "--installation is required. usage: $USAGE"
  [ -n "$REPO" ] || die "--repo is required: this check launches a run, and a run is launched in a repository. usage: $USAGE"
  case "$REPO" in
  */*) ;;
  *) die "--repo '$REPO' is not a repository as owner/name (nor a path inside a checkout): this check launches by name, so it needs the owner/name form and knows nothing about your checkout. usage: $USAGE" ;;
  esac
}

main() {
  parse_args "$@"
  CALL_SECONDS=${CHECK740_CALL_SECONDS:-60}
  LAUNCH_SECONDS=${CHECK740_LAUNCH_SECONDS:-1800}
  RUN_SECONDS=${CHECK740_RUN_SECONDS:-5400}
  POLL_SECONDS=${CHECK740_POLL_SECONDS:-20}
  IMPLEMENT_SECONDS=${CHECK740_IMPLEMENT_SECONDS:-1800}
  SEND_GAP_SECONDS=${CHECK740_SEND_GAP_SECONDS:-20}
  RUNS_LIMIT=${CHECK740_RUNS_LIMIT:-50}
  JOBS_LIMIT=${CHECK740_JOBS_LIMIT:-200}
  if [ "${CHECK740_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  FUGARO_BIN=${FUGARO_BIN:-$(command -v fugaro || true)}

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  need_tools
  FUGARO_BIN=$(command -v fugaro)
  scratch_up
  write_reader
  # After scratch_up, not before: a guard that stops the run records the fact
  # that says why, and a fact written before the facts file exists is dropped.
  guard_belong
  guard_xtrace
  guard_agent_session

  step_installation
  step_release
  step_bound
  step_launch
  step_watch
  step_end
  step_record
  step_messages
  step_workflow_iam
  step_project_iam
  step_lifecycle

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# -- including through the cleanup, which is where its second answer matters.
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
