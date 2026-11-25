#!/usr/bin/env bash
# Check 750 — a review judged against its task, on a REAL installation, as ONE
# script YOU run.
#
# It settles the live half of the review-against-task work (docs/review-against-
# task; D2930-D2939, the release 0.27.0 carries it). Every review stage checks
# the change against the task the pull request was written for, found through
# the runs bucket alone (`launchSpecForPR`); `fugaro review show RUN` prints what
# it judged against on one line (R8, `reviewAgainstLine`):
#
#   against: run <bare-run-id>            the task of the run that opened the PR
#   against: file <sha256>                a task a person supplied with --spec
#   against: none (--code-only)           no task at all, on purpose
#   against: none (<why nothing was found>)
#
# Offline tests prove the plumbing against fake providers and fakes of the
# store (RA10's two end-to-end files, RA2, RA3, RA5b, RA6a, RA6b, RA8a). What
# they cannot prove is the thing the plan's Check 750 is for: that the launcher
# on a real installation, reading a real runs bucket, resolves the run that
# opened a real pull request and that the REAL reviewer reports a change which
# does not do what its brief asked as an `unmet` issue rather than approving it.
#
# So the check does three reviews of pull requests a Fugaro run opened, and each
# is a billable review job of its own:
#
#   o `fugaro review --pr <N>` on the pull request you name. It passes when the
#     artifact's own against line is exactly `against: run <the run that opened
#     pull request N>` — that run's id, found independently from
#     `fugaro runs ls --pr N --json` (see step_pr_opened; the launcher's own
#     answer is never taken as the evidence for it).
#   o `fugaro review --pr <M>` — M named to THIS check as `--unmet-pr <M>`, which
#     is no flag of `fugaro review` — on a SECOND pull request, a Fugaro run
#     opened one whose head deliberately leaves out one item of its brief: that
#     task is YOURS to arrange, because a pull request that misses an item of
#     its brief is not something this script can make. Its review must list an
#     issue with scope `unmet` and its verdict must be `fix first`. Without
#     `--unmet-pr` this reading is not taken, the row reads UNVERIFIED and the
#     check ends INCOMPLETE (exit 3) — never a pass.
#   o `fugaro review --pr <N> --code-only` on the first pull request again: its
#     against line must be exactly `against: none (--code-only)`, which is the
#     launcher's own statement that the reviewer was handed no task.
#
# `fugaro review` is a LAUNCH: each of the three is a real review job on your
# installation, with the model spend of a review stage, bounded by the project's
# own per-run ceiling and the gateway's own accounting. Nothing here asks you to
# confirm it — Fugaro asks for nothing — so the check prints the ceiling it read
# from the project (and, before each launch, which review it is about to pay
# for) and then launches. The gateway's refusal is the only thing that stops it,
# and that refusal is written as the row's own reason rather than a guess.
#
# Usage:      bash scripts/live-checks/check-750-review-against-task.sh \
#               --installation <name> --pr <N> [--unmet-pr <M>] [--dry-run]
#             --dry-run  prints the plan and touches nothing.
#             --installation is the Fugaro project name the local config in
#             force carries (`fugaro doctor --json`'s project.name), and the
#             answer is held to it: a name that is not the one doctor reports is
#             a STOP before anything is launched.
#             --pr is the pull request a Fugaro run opened, in the repository of
#             the checkout this is run from. The check names that repository
#             explicitly — `--repo .`, which `fugaro` resolves from the
#             checkout's origin, the same repository `fugaro review --pr` takes
#             from it — because `runs ls --pr` with no `--repo` reads the local
#             config's repositories instead and refuses the call when that lists
#             more than one. So run this from a checkout of the repository the
#             pull request is in. A pull request no Fugaro run opened is refused
#             before the first launch: `fugaro runs ls --pr N --json` must name
#             the run that opened it, and zero such rows (or several) is a STOP.
#             --unmet-pr is the second pull request: a Fugaro run opened one
#             whose head deliberately leaves out one item of its own brief.
# Needs:      bash, mktemp, python3 and fugaro on PATH. No cloud tool is needed:
#             every reading comes from `fugaro` (doctor, version, review,
#             runs ls, project show) and nothing is installed or fetched. A
#             missing tool stops the run with one line naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused,
#             whether you typed it as the installation or as the GCP project,
#             and doctor's own answer is refused the same way.
# Costs:      TWO real review jobs (the review of your pull request, and the
#             --code-only one), or THREE when --unmet-pr is given: each is a
#             launch of its own with a review stage's model spend, under the
#             project's and the repository's own per-run ceilings. Everything
#             else this check does is a read (`fugaro doctor`, `fugaro
#             version`, `fugaro review show`, `fugaro runs ls`, `fugaro project
#             show`) and no review posts a comment or a review on the pull
#             request.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check-750-review-against-task_test.sh) runs
#             it against a fake fugaro and never touches a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: its scratch directory (holding
#             every document it read) is removed from the exit trap and the
#             `restore` row says whether it really went. It writes nothing else
#             anywhere: the review runs it launched stay, as every run does.
# Output:     progress on stderr, ONE block on stdout headed CHECK 750 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 750 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 750 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 750 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, a tool is
#                                                      missing, an error ended it);
#               CHECK 750 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured (--unmet-pr not given, a review
#             that never ended) is INCOMPLETE, not a pass, and the register's
#             section is read against that word.
#
# NO SECRET IS READ, so none can be printed: this check makes no `gcloud` call
# at all, and every value it reads is a run's row, an artifact's own fields, a
# verdict word, a count, or a number the project publishes. The reader prints a
# fixed vocabulary — a name, an id, a word, a count, a number and one bounded
# line of a command's own error — and every value is clipped to that vocabulary,
# so a document that echoed back something else (a token in an error body, a
# repository's own text) cannot reach the block. No task TEXT is printed either:
# what is written is this check's own sentence and the ids it compared. And no
# trace: `bash -x` prints every command as it runs, so guard_xtrace REFUSES a
# run whose shell arrived with tracing on.
#
# CHECK750_INSTALLATION, CHECK750_PR, CHECK750_UNMET_PR, CHECK750_DRY_RUN,
# CHECK750_CALL_SECONDS, CHECK750_LAUNCH_SECONDS, CHECK750_RUN_SECONDS,
# CHECK750_POLL_SECONDS and CHECK750_RUNS_LIMIT exist for this script's own test
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
USAGE="check-750-review-against-task.sh --installation <name> --pr <N> [--unmet-pr <M>] [--dry-run]"
# The release the review-against-task work ships in: the against line, the two
# flags and the launcher's own resolution arrive in it, and a review launched on
# a job image built from an older release runs code-only whatever the launcher
# wrote (D2933). `reviewSpecSince` in internal/cli's recipes_skew.go is the same
# number.
RELEASE=0.27.0
# The three sources `against` can name (runstore.AgainstSources), spelled as the
# artifact spells them.
AGAINST_RUN=run
AGAINST_NONE=none
# The two artifact words the second half of the pass condition is read on
# (RQ's schema: verdict "ship" | "fix first" | "reject", an issue's scope
# "unmet" | "beyond" | "differs").
VERDICT_FIX_FIRST="fix first"
SCOPE_UNMET=unmet
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
CLEANUP_RUNNING=no
# Everything the steps hand each other, empty until the step that reads it. A
# step that ends early (a launch that refused, a review that never ended) leaves
# the ones after it with nothing to read, and they say so; with `set -u` an
# uninitialised name would end the run instead, with no block for the owner.
INSTALLATION=""
PR=""
UNMET_PR=""
DRY_RUN=no
PROJECT=""
REGION=""
REPO_SLUG=""
OPENING_RUN=""
OPENING_REF=""
OPENING_STATUS=""
CANDIDATES=""
CAP_POLICY=no
CAP_MODE=""
CAP_PER_RUN=""
CAP_GLOBAL=""
CAP_DEFAULT=""
CAP_REPO=""
CAP_READ=no
REVIEW_REF=""
REVIEW_RUN_ID=""
REVIEW_PROBLEM=""
REVIEW_TERMINAL=no
REVIEW_AGAINST_SOURCE=""
REVIEW_AGAINST_RUN=""
REVIEW_AGAINST_REASON=""
REVIEW_AGAINST_LINE=""
REVIEW_VERDICT=""
REVIEW_ISSUES=""
REVIEW_UNMET=""
REVIEW_UNMET_SEVERITY=""
FUGARO_BIN=""
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
# all, and the block would then read as "reviews check the task" for a run that
# measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  # Before the scratch directory exists there are no facts at all, so the reason
  # is carried out in ABORT_REASON and finish() writes the one row.
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE" 2>/dev/null; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about reviews reading their task"
  fi
  ABORT_REASON="an internal error ended the run at line $line of the script"
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 750 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 750 needs bash"
  need mktemp
  need python3 "the reader that parses every document this check reads is one python3 file"
  need fugaro "the reviews this check launches, and every row it reads them back with, are fugaro's"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk750-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk750.XXXXXXXX") || die "Check 750 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 750 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
  if [ -n "$ABORT_REASON" ] && ! grep -q '^aborted|' <<<"$facts"; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! grep -q '^aborted|' <<<"$facts"; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about reviews reading their task"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ] || [ "$status" -eq 3 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
  else
    # Every file this check writes holds a document it read — a doctor's answer,
    # a row of `runs ls`, a review's launch result, an artifact's own bytes, a
    # project's published numbers — or a command's own error. No credential is
    # read on any path, so none can be in one of them. Their removal is READ,
    # not assumed.
    facts="${facts}"$'\n'"restore|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself") (the doctor's answer, the version and the usage line, the rows of every listing, the three reviews' launch results and artifacts and the project's own numbers). This check wrote nothing else anywhere, and nothing of yours on disk was opened: the review runs it launched stay, as every run does"
  fi
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if grep -q '|FAIL|' <<<"$facts"; then
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs — including the reviews it launches — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a review's own output"
}

# guard_agent_session: this check is the owner's. Each of its three readings is a
# real review job on a real installation, and the block it prints is meant to be
# pasted back by the person who ran it — the register's row says no agent runs
# any step of it. Refusing here is what makes the block say that, rather than an
# agent's run reading as the installation's answer.
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
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 750 is yours to run at your own terminal, against an installation you own, and no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 750 is yours to run at your own terminal, against an installation you own, and no agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like), so this is your own session — the one this check is written for: the reviews it launches are the reviews you asked for. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused, because it says this shell sits in an editor's terminal and not that an agent is driving it"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# guard_belong: `belong` is the owner's own installation, and this check launches
# real review jobs on the installation it is pointed at. Any value GIVEN to this
# check that names it is refused before the first call is made, and the answer
# doctor gives a moment later is refused in step_installation, which is where the
# name the config in force carries first exists — the name you type and the
# config in force are two things, and this check has to see the same one twice.
guard_belong() {
  [ -n "$INSTALLATION" ] || return 0
  case "$(lower "$INSTALLATION")" in
  *belong*)
    fact belong FAIL "'$INSTALLATION' (the installation) names 'belong', the owner's own development installation: Check 750 launches real review jobs, and it is for an installation that is not belong"
    stop "Check 750 never runs against 'belong'. Name another installation: $USAGE"
    ;;
  esac
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
# failing wait ran this script's ERR trap, and a dozen copies of the shape this
# replaced, running side by side as the harness runs every live check's test,
# ended the check with an "internal error" that had not happened. A deadline
# watchdog beside the call is the same hazard from the other side: the second
# background job is one more record for bash to lose, and it lost it just as
# often. One background job, a status file for the status, and `SECONDS` for the
# deadline.
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

# cand_field INDEX FIELD TEXT — one field of a candidate the reader printed as
# `c<index>|<run_id>|<repo>|<run>|<created>|<status>`: 2 run_id, 3 repo, 4 run,
# 5 created, 6 status. No field is ever a whole line, so no field can carry a
# separator into the block: every value is clipped to that vocabulary by the
# reader, which drops '|' outright.
cand_field() { # cand_field INDEX FIELD TEXT
  local line
  line=$(grep -m1 "^c$1|" <<<"$3" 2>/dev/null || true)
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
"""Check 750's reader: the one place the documents this check reads are parsed.

The shell never parses JSON and never prints a field it did not choose. This
program prints a FIXED vocabulary -- a name, an id, a word, a count, a number,
and ONE bounded line of a command's own error -- and every value is clipped to
the characters that vocabulary allows, so a document that echoed back something
else (a token in an error body, a repository's own text) cannot reach the block.
A task's TEXT is never printed: the artifact carries only the `against` record
about it, and this reader never looks at a task file.

Spellings: the fields are read in more than one spelling where the CLI's
documents disagree. A review's launch result prints the run as both `run_id` and
`run` (the CLI's own launchResult); a row of `runs ls --json` carries the pull
request as `pr`, `task_pr` and `record_pr`, the branch the run recorded, and
`artifact_count` only for a run whose record says it delivered an artifact. The
three are read together in `cmd_rows`, and the rule is the one the launcher
itself uses (internal/cli's launchSpecForPR): a run is on a pull request when
its task continues it or when the branch it recorded names a run.
"""
import json
import re
import sys

SAFE = re.compile(r"[^A-Za-z0-9 ._/@:()+-]")


def clip(value, limit=200):
    text = value if isinstance(value, str) else str(value)
    text = SAFE.sub(" ", text)
    text = " ".join(text.split())
    return text[:limit]


def num(value):
    """A published ceiling, as a number a reader can compare; anything that is
    not a number is reported as unset rather than guessed at."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return "unset"
    return "%g" % value


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def one(doc, *spellings):
    for name in spellings:
        if isinstance(doc, dict) and name in doc and doc[name] is not None:
            return doc[name]
    return None


def cmd_doctor(path):
    """`fugaro doctor --json`: which installation the config in force names, and
    the GCP project behind it -- the two things the belong refusal is read on."""
    doc = load(path)
    project = doc.get("project") or {}
    print("name=%s" % clip(project.get("name") or ""))
    print("project=%s" % clip(project.get("gcp_project") or ""))
    print("region=%s" % clip(project.get("region") or ""))


def cmd_rows(path):
    """`fugaro runs ls --pr N --json`: the rows of the pull request, and which
    of them could be the run that opened it.

    The rule is the check's own and it is deliberately narrower than the
    launcher's: a candidate is a row that names this pull request, is not a
    follow-up (a follow-up continues a pull request; it did not open it), whose
    branch names the run itself (task.BranchRunID: `fugaro/<run-id>` -- the run
    that cut the branch, where a follow-up's row names the ROOT run's id and a
    review run's row names its own lock key), and whose record says it delivered
    no artifact. That last field is what tells a review run's row apart from an
    opening run's on the same pull request: a finished review's record carries
    the artifact it wrote (review.json, its markdown and its signature) beside
    the pull request it read, while the runview.Row comment states the opening
    run's record has none. A row that is a review run and somehow carries no
    artifact is not guessed at: it makes the count two, and the check stops with
    the rows it found rather than picking one.
    """
    doc = load(path)
    rows = doc.get("runs") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        fail("not a runs document")
    pr = sys.argv[3] if len(sys.argv) > 3 else ""
    cands = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        number = one(row, "pr", "record_pr", "task_pr")
        if clip(number if number is not None else "") != pr:
            continue
        if row.get("follow_up"):
            continue
        if row.get("artifact_count"):
            continue
        run_id = row.get("run_id") or ""
        branch = row.get("branch") or ""
        if branch != "fugaro/" + run_id:
            continue
        cands.append((
            run_id,
            row.get("repo") or "",
            row.get("run") or "",
            row.get("created") or "",
            row.get("status") or "",
        ))
    cands.sort()
    print("count=%d" % len(cands))
    for i, (run_id, repo, run, created, status) in enumerate(cands, start=1):
        print("c%d|%s|%s|%s|%s|%s" % (
            i, clip(run_id), clip(repo), clip(run), clip(created, 40), clip(status)))


def cmd_row(path):
    """`fugaro runs ls --json`: one run's own row, by its run id -- what the
    polling after a launch reads: whether the run is TERMINAL, which is when its
    artifact exists (the job writes review.json, its markdown and its signature
    together, at the end)."""
    doc = load(path)
    rows = doc.get("runs") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        fail("not a runs document")
    want = sys.argv[3] if len(sys.argv) > 3 else ""
    for row in rows:
        if (row.get("run_id") or "") != want:
            continue
        print("found=yes")
        print("terminal=%s" % ("yes" if row.get("terminal") else "no"))
        print("status=%s" % clip(row.get("status") or ""))
        print("stage=%s" % clip(row.get("stage") or ""))
        return
    print("found=no")


def cmd_launch(path):
    """`fugaro review --pr N --json`: the launch's own result -- the run it
    started, its id and its job. The account it names is deliberately not read:
    nothing in this check needs it."""
    doc = load(path)
    print("run_id=%s" % clip(doc.get("run_id") or ""))
    print("run=%s" % clip(doc.get("run") or ""))
    print("repo=%s" % clip(doc.get("repo") or ""))
    print("status=%s" % clip(doc.get("status") or ""))
    print("job=%s" % clip(doc.get("job") or "", 300))


def cmd_against(path):
    """The first line `fugaro review show RUN` prints: the against line R8 puts
    there, or nothing where the artifact carries no against this build knows
    (reviewAgainstLine prints no line at all then, and the artifact's own JSON
    follows).

    The line is read as the OWNER reads it -- the exact spelling is compared by
    the shell -- and it is broken into its three parts here so a row can say what
    it found instead: `source` is run, file or none; `run` is the bare run id;
    `reason` is why there was nothing to check against."""
    with open(path, encoding="utf-8") as fh:
        lines = [ln.rstrip("\n") for ln in fh]
    first = ""
    for ln in lines:
        if ln.strip():
            first = ln
            break
    has = first.startswith("against: ")
    print("present=%s" % ("yes" if has else "no"))
    print("line=%s" % clip(first, 300))
    if not has:
        return
    rest = first[len("against: "):]
    m = re.match(r"^(run|file)\s+(.+)$", rest)
    if m:
        print("source=%s" % clip(m.group(1)))
        if m.group(1) == "run":
            print("run=%s" % clip(m.group(2)))
        return
    m = re.match(r"^none\s+\((.*)\)$", rest)
    if m:
        print("source=%s" % clip("none"))
        print("reason=%s" % clip(m.group(1)))
        return
    print("source=%s" % clip("unknown"))


def cmd_artifact(path):
    """`fugaro review show RUN --json`: the artifact's own bytes, which is what
    the second and third readings are taken from -- the verdict, and the issues
    with their scope (`unmet` is the one the pass condition names). The issue's
    own text is never printed: what a row says is how many there were, what
    scope they carried and the worst severity among them."""
    doc = load(path)
    verdict = doc.get("verdict") or ""
    issues = doc.get("issues") or []
    if not isinstance(issues, list):
        issues = []
    unmet = [i for i in issues if isinstance(i, dict) and (i.get("scope") or "") == "unmet"]
    sev = [i.get("severity") or "" for i in unmet]
    print("verdict=%s" % clip(verdict))
    print("issues=%d" % len(issues))
    print("unmet=%d" % len(unmet))
    print("unmet_severity=%s" % clip(",".join(sorted(set(s for s in sev if s)))))
    against = doc.get("against") or {}
    if isinstance(against, dict):
        print("against_source=%s" % clip(against.get("source") or ""))
        print("against_run=%s" % clip(against.get("run") or ""))
        print("against_reason=%s" % clip(against.get("reason") or ""))


def cmd_project(path):
    """`fugaro project show --json`: the numbers that bound what a launch may
    spend -- the published policy's own per-run ceiling and mode, and the
    installation's caps (global, defaults, and this repository's where it has
    one). Nothing here is added up: the row quotes the three, because which of
    them binds a run is the gateway's own reading and not this check's."""
    doc = load(path)
    policy = doc.get("policy")
    caps = doc.get("caps") or {}
    repo = sys.argv[3] if len(sys.argv) > 3 else ""
    print("policy=%s" % ("yes" if isinstance(policy, dict) else "no"))
    if isinstance(policy, dict):
        budget = policy.get("budget") or {}
        if isinstance(budget, dict):
            print("mode=%s" % clip(budget.get("mode") or "unset"))
            print("per_run_usd=%s" % num(budget.get("per_run_usd")))
    glob = ((caps.get("global") or {}).get("per_run_cap_usd"))
    print("global_cap=%s" % num(glob))
    print("default_cap=%s" % num((caps.get("defaults") or {}).get("per_run_cap_usd")))
    found = "unset"
    repos = caps.get("repos") or {}
    if isinstance(repos, dict):
        for key, pair in repos.items():
            if not isinstance(pair, dict):
                continue
            if key == repo or (pair.get("repo") or "") == repo:
                found = num(pair.get("per_run_cap_usd"))
                break
    print("repo_cap=%s" % found)


def cmd_err(path):
    """The last non-empty line of a command's own error, clipped: what a row
    says when a call refused, without pasting a log into the block."""
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            lines = [ln.strip() for ln in fh if ln.strip()]
    except OSError:
        lines = []
    print("line=%s" % clip(lines[-1] if lines else "", 240))


def fail(message):
    sys.stderr.write("check 750 reader: %s\n" % message)
    sys.exit(1)


CMDS = {
    "doctor": cmd_doctor,
    "rows": cmd_rows,
    "row": cmd_row,
    "launch": cmd_launch,
    "against": cmd_against,
    "artifact": cmd_artifact,
    "project": cmd_project,
    "err": cmd_err,
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
  [ -n "$PROJECT" ] || stop "fugaro doctor --json answered without a gcp_project, so this check cannot say which project's runs bucket the launcher will read"
  case "$(lower "$PROJECT")" in
  *belong*)
    fact belong FAIL "the local config in force answers the installation $name on GCP project $PROJECT, which names the owner's own development installation"
    stop "Check 750 never runs against 'belong'"
    ;;
  esac
  fact belong PASS "neither the installation ($name) nor its GCP project ($PROJECT) names the owner's own development installation (nothing typed contains 'belong')"
  fact installation PASS "the local config in force is installation $name on GCP project $PROJECT (region ${REGION:-unrecorded}), and the reviews below are launched on it"
}

# step_release: the gate every check of an unreleased feature carries. The work
# ships in 0.27.0, and a launcher that predates it has no --code-only and no
# --spec at all, so the against line could not be asked for. Both probes are
# offline: `fugaro version` and the usage line `review --help` prints.
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
  run_call "$CALL_SECONDS" "$SCRATCH/reviewhelp.out" "$SCRATCH/reviewhelp.err" "$FUGARO_BIN" review --help || status=$?
  help=$(cat "$SCRATCH/reviewhelp.out" 2>/dev/null || true)
  if grep -q -- '--code-only' <<<"$help" && grep -q -- '--spec' <<<"$help"; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro does not review against a task: 'fugaro review --help' names neither --code-only nor --spec, so neither the against line nor the launcher's resolution of the opening run is in this build. Check 750 needs release ${RELEASE} or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro carries no review-against-task: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'review --help' names --code-only and --spec: this build carries the against line and the launcher that resolves a pull request's opening run"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release review-against-task ships in: the usage line above names the flags only because a build that reports no release number may still carry the work, and a review measured here would be one nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro review --help' names --code-only and --spec, so this build carries the work"
    ;;
  esac
}

# step_pr_opened: the premise the whole check rests on, and the one run the
# first reading compares the launcher's answer against. The call names the
# checkout's own repository with `--repo .` — the CLI resolves that from this
# checkout's origin, which is also where `fugaro review --pr` below takes it —
# so a `runs ls --pr` here cannot be refused for a local config that lists more
# than one repository, and cannot read rows out of another one. This is where a
# run from somewhere else stops — and it stops BEFORE the first launch, because
# a review of a pull request no Fugaro run opened is not this check's subject
# (the launcher would answer `against: none (no run found)`, which is a correct
# answer about a pull request that is not what Check 750 measures).
#
# The opening run is found from the rows alone, never from the launcher: a check
# that compared `fugaro review`'s answer with itself would pass whatever the
# launcher resolved.
step_pr_opened() {
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/prrows.out" "$SCRATCH/prrows.err" \
    "$FUGARO_BIN" runs ls --pr "$PR" --repo . --limit "$RUNS_LIMIT" --json || status=$?
  if [ "$status" != 0 ]; then
    fact pr-opened FAIL "fugaro runs ls --pr ${PR} --repo . --json exited ${status}, so this check cannot say which run opened pull request ${PR}: $(reader err "$SCRATCH/prrows.err" | sed -n 's/^line=//p' | head -n 1). Run it yourself from a checkout of the repository the pull request is in — the check passes '--repo .' so 'runs ls --pr' names the repository 'fugaro review --pr' takes from that checkout's origin — and look at what it says"
    stop "the rows of pull request ${PR} could not be read: $(reader err "$SCRATCH/prrows.err" | sed -n 's/^line=//p' | head -n 1)"
  fi
  local answer count
  answer=$(reader rows "$SCRATCH/prrows.out" "$PR") || stop "the answer to fugaro runs ls --pr --json is not the document this check reads (is this fugaro too old to print --json?)"
  count=$(field_of count "$answer")
  CANDIDATES=$count
  case "$count" in
  1) ;;
  0)
    fact pr-opened FAIL "fugaro runs ls --pr ${PR} --json names no run that opened pull request ${PR}: a candidate is a row whose branch names the run itself, which is not a follow-up and whose record delivered no artifact. Check 750 is about a pull request a Fugaro run opened — name one with --pr, or open one with 'fugaro run' and run this check on it"
    stop "pull request ${PR} is not one a Fugaro run opened, so there is no opening run for a review to be checked against"
    ;;
  *)
    local named="" i=1
    while [ "$i" -le "$count" ]; do
      named="${named}${named:+, }$(cand_field "$i" 2 "$answer")"
      i=$((i + 1))
    done
    fact pr-opened FAIL "fugaro runs ls --pr ${PR} --json names ${count} runs that could be the one that opened pull request ${PR} (${named}). This check compares the launcher's answer against exactly one opening run, and refuses rather than picking one"
    stop "pull request ${PR} has ${count} rows that could be its opening run, so this check cannot name the one a review must be checked against"
    ;;
  esac
  OPENING_RUN=$(cand_field 1 2 "$answer")
  OPENING_REF=$(cand_field 1 4 "$answer")
  OPENING_STATUS=$(cand_field 1 6 "$answer")
  REPO_SLUG=$(cand_field 1 3 "$answer")
  fact pr-opened PASS "pull request ${PR} is one a Fugaro run opened: its opening run is ${OPENING_RUN} (${OPENING_STATUS:-status unrecorded}) in ${REPO_SLUG}, read from the rows of 'fugaro runs ls --pr ${PR} --json' and never from the launcher this check is measuring"
}

# step_bound: the spend this check is about to commit, said before it commits
# it. Nothing asks for a confirmation — Fugaro asks for nothing, and a review is
# the ordinary launch — so what stands in its place is the ceiling read from the
# project, printed here and again in one line before each launch. A dry run
# prints the same sentences and touches nothing.
step_bound() {
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/project.out" "$SCRATCH/project.err" \
    "$FUGARO_BIN" project show --json || status=$?
  if [ "$status" != 0 ]; then
    unmeasured cap "fugaro project show --json exited ${status}, so this check cannot say what bounds the three launches: $(reader err "$SCRATCH/project.err" | sed -n 's/^line=//p' | head -n 1)"
  else
    local answer
    answer=$(reader project "$SCRATCH/project.out" "$REPO_SLUG" 2>/dev/null || true)
    if [ -z "$answer" ]; then
      unmeasured cap "the answer to fugaro project show --json is not the document this check reads, so the ceiling the launches are bounded by could not be read"
    else
      CAP_READ=yes
      CAP_POLICY=$(field_of policy "$answer")
      CAP_MODE=$(field_of mode "$answer")
      CAP_PER_RUN=$(field_of per_run_usd "$answer")
      CAP_GLOBAL=$(field_of global_cap "$answer")
      CAP_DEFAULT=$(field_of default_cap "$answer")
      CAP_REPO=$(field_of repo_cap "$answer")
      local policy_text="no policy is published, so a run uses the ceiling its job's environment carries"
      if [ "$CAP_POLICY" = yes ]; then
        policy_text="the published policy's budget is mode '${CAP_MODE:-unset}' with a per-run ceiling of \$${CAP_PER_RUN:-unset}"
      fi
      fact cap PASS "what bounds the launches, as the project publishes it: ${policy_text}; the installation's own caps are \$${CAP_GLOBAL:-unset} globally, \$${CAP_DEFAULT:-unset} by default and \$${CAP_REPO:-unset} for ${REPO_SLUG}. Nothing here is added up: the gateway holds the ceiling and its refusal is the only thing that stops a launch"
    fi
  fi
  local launches="TWO"
  if [ -n "$UNMET_PR" ]; then launches="THREE"; fi
  fact bound PASS "${launches} real review jobs, each launched by 'fugaro review' as a run of its own with a review stage's model spend, under the project's and the repository's own per-run ceilings. Nothing asks you to confirm them — Fugaro asks for nothing — and a launch the gateway refuses past the ceiling is written as the row's own reason rather than retried. Every other call this check makes is a read (doctor, version, review show, runs ls, project show), no review posts a comment or a review on the pull request, and a dry run commits none of it"
}

# run_one_review NAME PR FLAGS...: one review, from its launch to its artifact.
# It sets REVIEW_REF, REVIEW_RUN_ID and REVIEW_PROBLEM (empty when the review
# was launched and ended) and REVIEW_TERMINAL, and it prints the ceiling line
# before it spends anything. The polling is not decoration: `fugaro review show`
# has no artifact to print until the review job has finished writing it.
run_one_review() { # run_one_review NAME PR FLAGS...
  local name=$1 pr=$2
  shift 2
  REVIEW_REF=""
  REVIEW_RUN_ID=""
  REVIEW_PROBLEM=""
  REVIEW_TERMINAL=no
  local cap_line="no published policy is in force"
  if [ "$CAP_READ" = yes ]; then
    cap_line="the project's own numbers are in force (a published per-run ceiling of \$${CAP_PER_RUN:-unset}${CAP_MODE:+ under mode $CAP_MODE}, the installation's own \$${CAP_GLOBAL:-unset} global and \$${CAP_REPO:-unset} for ${REPO_SLUG})"
  fi
  owner "launching ${name}: 'fugaro review --pr ${pr} ${*}' is a billable review job of its own; ${cap_line}"
  say "launching ${name} (review of pull request ${pr}, a review job of its own)"
  local status=0
  run_call "$LAUNCH_SECONDS" "$SCRATCH/$name.launch.out" "$SCRATCH/$name.launch.err" \
    "$FUGARO_BIN" review --pr "$pr" "$@" --json || status=$?
  if [ "$status" = 124 ]; then
    REVIEW_PROBLEM="the launch did not finish inside ${LAUNCH_SECONDS}s and was ended"
    return 0
  fi
  if [ "$status" != 0 ]; then
    REVIEW_PROBLEM="fugaro review --pr ${pr} ${*} exited ${status}: $(reader err "$SCRATCH/$name.launch.err" | sed -n 's/^line=//p' | head -n 1)"
    return 0
  fi
  local answer
  answer=$(reader launch "$SCRATCH/$name.launch.out" 2>/dev/null || true)
  REVIEW_RUN_ID=$(field_of run_id "$answer")
  REVIEW_REF=$(field_of run "$answer")
  if [ -z "$REVIEW_RUN_ID" ] || [ -z "$REVIEW_REF" ]; then
    REVIEW_PROBLEM="fugaro review --pr ${pr} --json named no run (run_id=${REVIEW_RUN_ID:-none}, run=${REVIEW_REF:-none}), so this check has no review to read back"
    return 0
  fi
  # The wait is the run's own: a review is a job, and its artifact exists when it
  # has finished. The bound is the owner's patience (CHECK750_RUN_SECONDS, 90
  # minutes), and reaching it is a row left UNVERIFIED, never a pass.
  local waited=0 terminal=no rowstatus="" rowstage=""
  while :; do
    local ls_status=0
    run_call "$CALL_SECONDS" "$SCRATCH/$name.rows.out" "$SCRATCH/$name.rows.err" \
      "$FUGARO_BIN" runs ls --limit "$RUNS_LIMIT" --json || ls_status=$?
    if [ "$ls_status" = 0 ]; then
      local rows_answer
      rows_answer=$(reader row "$SCRATCH/$name.rows.out" "$REVIEW_RUN_ID" 2>/dev/null || true)
      if [ "$(field_of found "$rows_answer")" = yes ]; then
        terminal=$(field_of terminal "$rows_answer")
        rowstatus=$(field_of status "$rows_answer")
        rowstage=$(field_of stage "$rows_answer")
        if [ "$terminal" = yes ]; then break; fi
      fi
    fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$terminal" = yes ]; then
    REVIEW_TERMINAL=yes
    return 0
  fi
  REVIEW_PROBLEM="the review ${REVIEW_REF} had not finished after ${RUN_SECONDS}s (its row read status '${rowstatus:-unknown}', stage '${rowstage:-unknown}'), so its artifact does not exist yet and this reading was not taken. The run is still going on your installation: read it with 'fugaro runs show ${REVIEW_REF}'"
  return 0
}

# read_review_against NAME: what `fugaro review show` prints for the review the
# last launch started — the line the pass condition is written on, taken as the
# owner takes it (the exact spelling) and broken into its parts for the row.
read_review_against() { # read_review_against NAME
  local name=$1
  REVIEW_AGAINST_LINE=""
  REVIEW_AGAINST_PRESENT=""
  REVIEW_AGAINST_SOURCE=""
  REVIEW_AGAINST_RUN=""
  REVIEW_AGAINST_REASON=""
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/$name.show.out" "$SCRATCH/$name.show.err" \
    "$FUGARO_BIN" review show "$REVIEW_REF" || status=$?
  if [ "$status" != 0 ]; then
    return 1
  fi
  local answer
  answer=$(reader against "$SCRATCH/$name.show.out" 2>/dev/null || true)
  REVIEW_AGAINST_LINE=$(field_of line "$answer")
  REVIEW_AGAINST_PRESENT=$(field_of present "$answer")
  REVIEW_AGAINST_SOURCE=$(field_of source "$answer")
  REVIEW_AGAINST_RUN=$(field_of run "$answer")
  REVIEW_AGAINST_REASON=$(field_of reason "$answer")
  return 0
}

# step_against_run: the first half of the plan's pass condition — on a pull
# request a Fugaro run opened, the review's own against line names THAT run.
step_against_run() {
  run_one_review against-run "$PR"
  if [ -n "$REVIEW_PROBLEM" ]; then
    unmeasured against-run "${REVIEW_PROBLEM}"
    return 0
  fi
  say "reading the against line of ${REVIEW_REF}"
  if ! read_review_against against-run; then
    local why
    why=$(reader err "$SCRATCH/against-run.show.err" | sed -n 's/^line=//p' | head -n 1)
    fact against-run FAIL "fugaro review show ${REVIEW_REF} exited non-zero, so the review has no artifact to read an against line from: ${why}. A review that wrote none is a review whose verdict was never signed, and it is a finding about that run rather than about this check"
    return 0
  fi
  # What says there was no line is the reader's own reading of the first line,
  # not the emptiness of the value it printed: with no against line the first
  # line is the artifact's JSON, which is a value but not an against line.
  if [ "$REVIEW_AGAINST_PRESENT" != yes ]; then
    fact against-run FAIL "the review of pull request ${PR} (${REVIEW_REF}) printed no against line at all, so nothing says what it judged the change against: an artifact written before the field existed, or one whose against this build does not know, prints no line rather than a line nobody defined"
    return 0
  fi
  if [ "$REVIEW_AGAINST_SOURCE" = "$AGAINST_RUN" ] && [ "$REVIEW_AGAINST_RUN" = "$OPENING_RUN" ]; then
    fact against-run PASS "the review's own line is '${REVIEW_AGAINST_LINE}', naming run ${OPENING_RUN} — the run the rows say opened pull request ${PR} — so this review judged the change against that run's stored task, found through the runs bucket alone"
    return 0
  fi
  if [ "$REVIEW_AGAINST_SOURCE" = "$AGAINST_RUN" ]; then
    fact against-run FAIL "the review's own line is '${REVIEW_AGAINST_LINE}', naming run ${REVIEW_AGAINST_RUN}, and the rows of pull request ${PR} say the run that opened it is ${OPENING_RUN}: the launcher resolved a task from a run that is not this pull request's own, so the review judged the change against a brief it was not written for"
    return 0
  fi
  if [ "$REVIEW_AGAINST_SOURCE" = "$AGAINST_NONE" ]; then
    fact against-run FAIL "the review's own line is '${REVIEW_AGAINST_LINE}': the launcher found no task to check the change against (reason '${REVIEW_AGAINST_REASON:-unrecorded}') although pull request ${PR} is one a Fugaro run opened — ${OPENING_RUN} is in the rows — so its task should have been found in the runs bucket (this is D2933's older job image, or a launcher that lost the task)"
    return 0
  fi
  fact against-run FAIL "the review's own line is '${REVIEW_AGAINST_LINE}', which names a source this check does not read (${REVIEW_AGAINST_SOURCE:-none}), so it does not say that the change was judged against run ${OPENING_RUN}'s task"
}

# step_unmet: the second half of the pass condition, on the SECOND pull request
# — the one whose head deliberately leaves out one item of its brief. The
# artifact must call that out: an issue with scope `unmet` and a verdict of
# `fix first`. A reviewer that shipped a change which does not do what its brief
# asked is what this row exists to catch, and it is not something the offline
# tests can force out of a real model.
step_unmet() {
  if [ -z "$UNMET_PR" ]; then
    unmeasured unmet "no --unmet-pr was given, so the second reading was not taken: this half of the pass condition needs a SECOND pull request a Fugaro run opened whose head deliberately leaves out one item of its brief, and only you can name it. Run again with --unmet-pr <N>, after opening such a pull request, and this row is measured"
    return 0
  fi
  run_one_review unmet "$UNMET_PR"
  if [ -n "$REVIEW_PROBLEM" ]; then
    unmeasured unmet "${REVIEW_PROBLEM}"
    return 0
  fi
  say "reading the artifact of ${REVIEW_REF} (the pull request that misses an item of its brief)"
  local status=0
  run_call "$CALL_SECONDS" "$SCRATCH/unmet.show.out" "$SCRATCH/unmet.show.err" \
    "$FUGARO_BIN" review show "$REVIEW_REF" --json || status=$?
  if [ "$status" != 0 ]; then
    local why
    why=$(reader err "$SCRATCH/unmet.show.err" | sed -n 's/^line=//p' | head -n 1)
    fact unmet FAIL "fugaro review show ${REVIEW_REF} --json exited ${status}, so the artifact of the review of pull request ${UNMET_PR} could not be read: ${why}"
    return 0
  fi
  local answer verdict issues unmet severity
  answer=$(reader artifact "$SCRATCH/unmet.show.out") || stop "the artifact of ${REVIEW_REF} is not the document this check reads"
  verdict=$(field_of verdict "$answer")
  issues=$(field_of issues "$answer")
  unmet=$(field_of unmet "$answer")
  severity=$(field_of unmet_severity "$answer")
  # A count the reader did not print is no count: the comparisons below are on
  # numbers, and an empty value would make them an error rather than an answer.
  case "$unmet" in '' | *[!0-9]*) unmet=0 ;; esac
  case "$issues" in '' | *[!0-9]*) issues=0 ;; esac
  REVIEW_VERDICT=$verdict
  REVIEW_ISSUES=$issues
  REVIEW_UNMET=$unmet
  REVIEW_UNMET_SEVERITY=$severity
  REVIEW_AGAINST_SOURCE=$(field_of against_source "$answer")
  REVIEW_AGAINST_RUN=$(field_of against_run "$answer")
  local against_note="its against source is '${REVIEW_AGAINST_SOURCE:-none}'"
  if [ "$REVIEW_AGAINST_SOURCE" = "$AGAINST_RUN" ]; then
    against_note="it was judged against run ${REVIEW_AGAINST_RUN}"
  fi
  if [ "$verdict" = "$VERDICT_FIX_FIRST" ] && [ "$unmet" -ge 1 ] 2>/dev/null; then
    fact unmet PASS "the review of pull request ${UNMET_PR} (${REVIEW_REF}) is '${verdict}' with ${unmet} of its ${issues} issues scoped unmet (severity '${severity:-unrecorded}'), and ${against_note}: a change whose head leaves out an item of its brief is reported as unmet and held back rather than shipped"
    return 0
  fi
  if [ "$unmet" -ge 1 ] 2>/dev/null; then
    fact unmet FAIL "the review of pull request ${UNMET_PR} (${REVIEW_REF}) lists ${unmet} unmet issue(s) and its verdict is '${verdict:-unrecorded}', and the pass condition is '${VERDICT_FIX_FIRST}': an unmet item of the brief was seen and the change was not held back"
    return 0
  fi
  fact unmet FAIL "the review of pull request ${UNMET_PR} (${REVIEW_REF}) lists no issue with scope '${SCOPE_UNMET}' — ${issues} issue(s) in all, verdict '${verdict:-unrecorded}', ${against_note}: the change leaves out an item of its brief and the reviewer did not say so as unmet. Check the pull request you named: --unmet-pr must be one a Fugaro run opened whose head really misses one item of the brief the run was given"
}

# step_code_only: the plan's last line — `--code-only` must say so. The whole
# point of the flag is that a reader can tell a review that was handed no task
# from one whose launcher lost the task, so what is compared is the exact line,
# not merely that it says none.
step_code_only() {
  run_one_review code-only "$PR" --code-only
  if [ -n "$REVIEW_PROBLEM" ]; then
    unmeasured code-only "${REVIEW_PROBLEM}"
    return 0
  fi
  say "reading the against line of ${REVIEW_REF} (--code-only)"
  if ! read_review_against code-only; then
    local why
    why=$(reader err "$SCRATCH/code-only.show.err" | sed -n 's/^line=//p' | head -n 1)
    fact code-only FAIL "fugaro review show ${REVIEW_REF} exited non-zero, so the --code-only review has no artifact to read an against line from: ${why}"
    return 0
  fi
  local want="against: none (--code-only)"
  if [ "$REVIEW_AGAINST_LINE" = "$want" ]; then
    fact code-only PASS "with --code-only the review's own line is exactly '${want}': the reviewer was handed no task and the artifact says so, which is what lets a reader tell this apart from a launcher that lost the task"
    return 0
  fi
  fact code-only FAIL "with --code-only the review's line is '${REVIEW_AGAINST_LINE:-no line at all}', and the pass condition is exactly '${want}': a review asked for the code alone did not say so in the one line a reader has to tell the two cases apart"
}

BLOCK_KEYS=(installation belong release pr-opened bound against-run unmet code-only agent-session xtrace restore)

criteria_unmet() {
  local facts=$1 key line
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(grep -m1 "^$key|" <<<"$facts" || true)
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
  printf '\nCHECK 750 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(grep -m1 "^$key|" <<<"$facts" || true)
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
  printf 'CHECK 750 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  # A variable rather than a default inside the heredoc: the lines below name
  # the pull requests and the flags as the run would pass them.
  local unmet_shown="not given — that reading is left UNVERIFIED and the check ends INCOMPLETE (exit 3)"
  if [ -n "$UNMET_PR" ]; then unmet_shown="pull request ${UNMET_PR}, a Fugaro run opened one whose head deliberately leaves out one item of its brief"; fi
  cat <<PLAN_EOF
Check 750 — a review judged against its task. This is the PLAN. --dry-run
touches nothing: nothing is read, no call is made, no review is launched and no
file of yours is opened.

  installation  ${INSTALLATION}  (the local config in force must answer it; 'belong' is refused)
  pull request  ${PR}  (must be one a Fugaro run opened, read from 'fugaro runs ls --pr')
  unmet pull request  ${unmet_shown}
  release       ${RELEASE} or later  (the release review-against-task ships in)

What the check will do, in this order:
  1. read the installation with 'fugaro doctor --json' and refuse it if it is not
     ${INSTALLATION}, or if the installation or its GCP project names 'belong';
  2. probe the build offline — 'fugaro version' and 'fugaro review --help' — and stop
     if it carries neither --code-only nor --spec, because the against line could not
     be asked for at all;
  3. read the rows of pull request ${PR} with 'fugaro runs ls --pr ${PR} --repo . --json',
     where '--repo .' is this checkout's repository, resolved from its origin — the one
     'fugaro review --pr' takes from there — and REFUSE the pull request unless exactly
     one of them is the run that opened it: a
     row that names this pull request, is not a follow-up, whose branch names the run
     itself and whose record delivered no artifact. This happens BEFORE the first
     launch — a review of a pull request no run opened is not what this check
     measures;
  4. read the ceiling the launches are bounded by with 'fugaro project show --json'
     (the published policy's per-run ceiling and mode, and the installation's own
     caps), and print it: nothing here asks you to confirm a launch, because Fugaro
     asks for nothing;
  5. launch the FIRST review, 'fugaro review --pr ${PR} --json' — a billable review job
     of its own — wait for its row to read terminal ('fugaro runs ls --json'), and
     read the line 'fugaro review show <run>' prints: it must be exactly
     'against: run <the run step 3 found>';
  6. launch the SECOND review, 'fugaro review --pr <M> --json' — M is this check's
     --unmet-pr, no flag of 'fugaro review' — on the pull request whose head
     deliberately leaves out one item of its brief, and read its artifact
     ('fugaro review show <run> --json'): it must list an issue with
     scope 'unmet' and its verdict must be 'fix first'. Without --unmet-pr this
     reading is NOT taken and the row reads UNVERIFIED;
  7. launch the THIRD review, 'fugaro review --pr ${PR} --code-only --json', and read
     its against line: it must be exactly 'against: none (--code-only)';
  8. write nothing of its own: every other call above is a read, no review posts a
     comment or a review on the pull request, and the scratch directory — every
     document it read — is removed on the way out, however the run ends.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
usage() {
  printf 'usage: %s\n' "$USAGE"
  printf '\n'
  printf 'Check 750 — a review judged against its task, on a real installation. It does\n'
  printf 'three reviews on pull requests a Fugaro run opened: the review of your pull\n'
  printf 'request against the task of the run that opened it, a review of a second pull\n'
  printf 'request whose head deliberately misses one item of its brief (--unmet-pr), and\n'
  printf 'the same pull request again with --code-only. Each is a billable review job of\n'
  printf 'its own.\n'
  printf '\n'
  printf '  --installation NAME  the installation to read (the local config in force must answer it; belong is refused)\n'
  printf '  --pr N               the pull request a Fugaro run opened, in the repository of\n'
  printf '                       the checkout this is run from\n'
  printf '  --unmet-pr M         the second pull request: a Fugaro run opened one whose head\n'
  printf '                       deliberately leaves out one item of its brief\n'
  printf '  --dry-run            print the plan and touch nothing\n'
  printf '\n'
  printf 'The full description, the exit codes and every knob (CHECK750_*) are the comment\n'
  printf 'block at the top of this file: read it before you run the check for real.\n'
}

parse_args() {
  INSTALLATION=${CHECK750_INSTALLATION:-}
  PR=${CHECK750_PR:-}
  UNMET_PR=${CHECK750_UNMET_PR:-}
  DRY_RUN=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --pr)
      [ $# -ge 2 ] || die "--pr needs a pull request number. usage: $USAGE"
      PR=$2
      shift 2
      ;;
    --unmet-pr)
      [ $# -ge 2 ] || die "--unmet-pr needs a pull request number. usage: $USAGE"
      UNMET_PR=$2
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
  [ -n "$PR" ] || die "--pr is required: this check reads a review of a pull request a Fugaro run opened. usage: $USAGE"
  case "$PR" in
  *[!0-9]*) die "--pr '$PR' is not a pull request number. usage: $USAGE" ;;
  esac
  [ "$PR" -gt 0 ] || die "--pr '$PR': use a pull request number. usage: $USAGE"
  if [ -n "$UNMET_PR" ]; then
    case "$UNMET_PR" in
    *[!0-9]*) die "--unmet-pr '$UNMET_PR' is not a pull request number. usage: $USAGE" ;;
    esac
    [ "$UNMET_PR" -gt 0 ] || die "--unmet-pr '$UNMET_PR': use a pull request number. usage: $USAGE"
    if [ "$UNMET_PR" = "$PR" ]; then
      die "--unmet-pr $UNMET_PR is --pr: the second reading needs a DIFFERENT pull request, one whose head deliberately leaves out an item of its brief, because the review of a pull request that does its task reports no unmet issue by construction. usage: $USAGE"
    fi
  fi
}

main() {
  parse_args "$@"
  CALL_SECONDS=${CHECK750_CALL_SECONDS:-60}
  LAUNCH_SECONDS=${CHECK750_LAUNCH_SECONDS:-600}
  RUN_SECONDS=${CHECK750_RUN_SECONDS:-5400}
  POLL_SECONDS=${CHECK750_POLL_SECONDS:-20}
  RUNS_LIMIT=${CHECK750_RUNS_LIMIT:-50}
  if [ "${CHECK750_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
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
  step_pr_opened
  step_bound
  step_against_run
  step_unmet
  step_code_only

  REACHED_END=yes
  trap - ERR
  local failed unsettled
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  # A run that reached the end with a criterion it could not settle is
  # INCOMPLETE, never a pass: the register's rule is that ONLY an all-PASS block
  # is a PASS, and a row left UNVERIFIED (--unmet-pr not given, a review that
  # had not ended, a project that would not answer) is exactly that.
  unsettled=$(grep -c '|UNVERIFIED|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${unsettled:-0}" -eq 0 ] || return 3
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
