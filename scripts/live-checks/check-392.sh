#!/usr/bin/env bash
# Check 392 — a FORCED `policy` stage failure leaves the change saved and the
# step owed with a non-zero exit, and `fugaro apply` finishes it.
#
# It is AP10 of docs/composite-commands/implementation-plan.md, run by YOU on
# the sandbox installation after the release that ships the composite commands
# is installed. What it measures is D1563's failure half: a management command
# writes its change and runs the steps that make it live (D1561), and a step
# that does not finish leaves the change SAVED, the step OWED, and the command
# non-zero — with the one sentence that names the command which finishes it.
# Check 391 measures the success half (three changes, one apply, each step
# once); this one measures the failure half and the recovery.
#
# **How the failure is forced.** A live check may not break the installation to
# make a step fail, so the failure is the one the product itself makes on a
# sandbox that is working: the step is run from a CODING AGENT'S SESSION, whose
# marker (`CLAUDECODE=1`) is set for that one child process and for nothing
# else. The loop plans a stage read-only in such a session and leaves it for the
# person (D1571), so the `policy` stage does not run, its step stays owed, and
# the command exits non-zero. That is the same lever the AP3 test uses
# (internal/cli/apply_test.go's TestApplyUnderAgentMarker), and it is the only
# one a check can pull: a write that fails at the database cannot be produced
# without breaking the policy database, and the confirmations that do refuse
# without a terminal — the typed name on a loosening, the empty allow-list of a
# first publish — are exactly the ones a second run cannot finish without a
# person typing, so a check that pulled one would hang or would widen your
# policy on a script's say-so. What this check does NOT measure is therefore a
# REMOTE failure of the write (the database refusing it): nothing here says what
# `fugaro apply` does when the publish itself fails, only what it does when a
# step does not run.
#
# **The one installation this cannot run on.** A MANAGED project (one Fugaro
# converges on its owner's behalf, D1016) applies the policy stage under its
# gate in a coding agent's session instead of refusing it — the stage asks the
# gate first, and an admitted step proceeds — so the marker above is no refusal
# there and the forced run would publish the policy and exit 0. This check
# therefore reads `fugaro managed status --json` before it writes anything and
# STOPS, as a run that could not tell, when the marks answer managed. It is a
# reading about the installation and not about the release: run it on an
# installation whose marks answer managed=false.
#
# What it reads, in this order:
#
#   1. The installation: `fugaro doctor --json` names the project, its GCP
#      project and its region, held to `--project` where you typed one, and an
#      installation named `belong` is refused.
#   2. The marks: `fugaro managed status --json` must answer, and must answer
#      that the project is NOT managed, for the reason in the paragraph above.
#   3. The policy database, before anything is written: `fugaro settings --json`
#      must answer a `policy` source that was READ (`status=ok`) and must carry
#      no key the read marks `unpublished`. Both halves matter: an unread source
#      is a stage that skips its work and answers the step without doing it (so
#      the failure below could not be forced at all), and an unpublished key is
#      a change of YOURS that is saved and not live, which the applies below
#      would publish. A run that cannot read both is a run that does not write.
#   4. The one setting this check writes, and what it holds now:
#      `fugaro settings get <key> --json` must answer a row the generic write
#      path carries (`writable=yes`), SET at a layer (`set=yes`) and decided by
#      the local layer (`wins=local`) — and its value must be a boolean. `set`
#      is the load-bearing one: a key nothing states is not compared with the
#      published node at all (policyConfigDrift's own guard), so a value written
#      over an unset key cannot be put back — unsetting the file would leave the
#      member in the node for ever. What is read here is what the teardown puts
#      back, and a key that cannot be read, cannot be written, or is not set is
#      a reason not to write it at all.
#   5. The change, saved and not live: `fugaro settings set <key> <the other
#      answer> --layer local --no-apply` must exit 0 and answer its own "saved,
#      not live yet" line naming the `policy` step alone (D1565).
#   6. That the change really owes work: `fugaro settings get <key> --json` again
#      must read the value just written with `state=unpublished` — the local
#      file and the published node differ, which is what makes the `policy`
#      stage's own check say it has something to do. Without it the stage would
#      answer "No changes" and the forced run would exit 0 for a reason that has
#      nothing to do with this check.
#   7. **The forced failure, and it is the fact this check is**: `fugaro apply`
#      with `CLAUDECODE=1` on the child alone, its stdin on /dev/null and a
#      deadline. It must exit NON-ZERO, its stdout must carry D1563's sentence
#      ("saved, not live yet: policy still owed" — the failing variant, which
#      says "again"), and its own account must carry the session's refusal. An
#      apply that exited 0 is the loudest FAIL here: the step ran in a coding
#      agent's session, which is the one thing that must never happen.
#   8. What is owed after it: `fugaro apply --status --json` must name exactly
#      one step, `policy` (D1562: one entry per step name), and the setting must
#      still read the value this check wrote with `state=unpublished` — the
#      change is saved, the node did not move, and the step is still owed.
#   9. **The recovery**: `fugaro apply`, no marker, its stdin on /dev/null, under
#      the same deadline. It must exit 0 and its own account must say it applied
#      `policy`.
#  10. That the change is LIVE now: `fugaro apply --status --json` owes nothing,
#      and `fugaro settings get <key> --json` reads the value this check wrote
#      with `state=ok` — the published node carries it.
#  11. The teardown: the key is put back the way it was found (its own value at
#      the local layer), then ONE `fugaro apply` makes that live, and both reads
#      are made again. This check leaves the installation as it found it.
#
# **What a PASS means, and what it does not.** A PASS means a `policy` step that
# did not run left the change saved, the step owed and the command non-zero,
# that the sentence naming `fugaro apply` was printed, and that the same command
# finished the job with nothing typed. It does NOT mean the write itself was
# refused by the cloud (see "How the failure is forced" above), and it does not
# measure the collapse of several changes into one step: that is check 391's
# subject.
#
# What a real run does to your installation: it writes ONE setting of the
# machine's own config (the other answer of a boolean the published policy
# carries), publishes it at the recovery apply, and puts the original answer
# back and publishes that again at the teardown. The node carries the check's
# answer for the few seconds between the two applies, so a run launched in that
# window reads it. Run it on the sandbox installation.
#
# Usage:      bash scripts/live-checks/check-392.sh [--project <fugaro project>]
#                                                 [--key <settings key>]
#                                                 [--value <true|false>]
#                                                 [--fugaro-flags '<flags>']
#                                                 [--deadline <seconds>] [--dry-run]
#             --key is the setting this check writes (default `bake_repos`, the
#             bake switch the setup wizard answers): it must be a boolean the
#             machine's own config states at the local layer and the published
#             policy carries, so that writing the other answer is a change the
#             `policy` stage has to publish. `triage` and `review_against_task`
#             are the other two of that shape.
#             --value is the answer to write (default: the other one). It must
#             differ from what the key holds, or there is nothing to publish.
#             --project is the Fugaro project the installation is, and
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on
#             whitespace, so a value with a space in it cannot be passed.
#             --deadline is how long EACH of the two applies may take, in
#             seconds (default 600; CHECK392_DEADLINE does the same): both are a
#             policy publish and not a build, so seconds are enough and minutes
#             are room.
#             --dry-run prints the plan and makes no call at all.
# Needs:      bash, mktemp, grep, sed, head, tail, cut, awk, python3, and a
#             `fugaro` on PATH with `doctor --json`, `managed status --json`,
#             `settings --json`, `settings get --json`, `settings set`, `apply`
#             and `apply --status --json`. A missing tool stops the run with one
#             line naming it. No `gcloud` is needed: every reading here is the
#             CLI's own record.
# Where:      your own machine, in a directory where the installation's project
#             config is in force (or with --project), against an installation
#             whose project is NOT `belong` — that name is refused before
#             anything is run, in what you typed and in what doctor answers —
#             and whose marks answer that it is NOT managed, for the reason
#             above. A coding agent's session is refused too; the marker below
#             is set on one child's own command line and nowhere else.
# Costs:      two policy publications (the recovery's and the teardown's) and
#             the settings reads around them. No model call, no build and no run
#             is launched.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 392 RESULT.
#             Exit 0 when the claim held, 1 when it did not (a row says which),
#             and 2 when this run could not tell (a guard stopped it, a tool or
#             a document could not be read, an internal error ended it).
#
# CHECK392_FUGARO names the binary (default `fugaro`), CHECK392_DEADLINE the
# default deadline, CHECK392_DRY_RUN makes every run a dry one,
# CHECK392_KEEP_SCRATCH keeps the scratch directory (with both applies'
# transcripts) instead of removing it, and CHECK392_KEY and CHECK392_VALUE
# replace the setting this check writes and the answer it writes.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all.
set -E

# No xtrace, whatever the shell it was started from had: this run writes one of
# the machine's own settings and publishes it.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
HELPED=no
DRY_RUN=""
USAGE="check-392.sh [--project <fugaro project>] [--key <settings key>] [--value <true|false>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

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

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the composite commands"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 392 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 392 needs bash"
  need mktemp
  need grep
  need sed
  need head
  need tail
  need cut
  need awk "the rows of a run's own transcript are joined into one line with it"
  need python3 "the JSON documents this check reads are parsed with it"
  need "$FUGARO_BIN" "the change, the forced failure and the recovery are the commands this check measures"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk392-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK392_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk392.XXXXXXXX") || die "Check 392 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 392 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
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
    say "CHECK392_KEEP_SCRATCH is set: $SCRATCH is kept (both applies' transcripts are in forced.out/err and finish.out/err)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check reads and writes your installation and prints the block you would paste back. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: no trace of this run can copy a document it read or a value in one"
}

# guard_agent_session: this check is the owner's, and the commands it runs
# publish your project's policy. The loop is the five markers that say an agent,
# and not a person, is driving this shell: agentenv.AgentMarkers.
# CLAUDE_CODE_SSE_PORT is not one of them (it says this shell sits in an
# editor's terminal) and is dropped from this script's environment instead.
#
# The marker the FORCED run below carries is this check's own, set on that one
# child's command line: it is the failure being forced and not a session this
# check is running in, which is what this guard is about.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 392 writes one of the machine's own settings and publishes your policy, and it is yours to run at your own terminal, on the sandbox installation"
    stop "${marker} is set, so this is a coding agent's session: Check 392 is yours to run at your own terminal, on the sandbox installation"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for. The marker the forced run carries is set on that child's own command line and nowhere else. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused: it says this shell sits in an editor's terminal, not that an agent is driving it"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

guard_belong() { # guard_belong NAME WHERE
  if is_belong "$1"; then
    fact belong FAIL "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write one of the owner's own settings and publish the owner's own policy"
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write one of the owner's own settings and publish the owner's own policy"
  fi
}

# ------------------------------------------------------------- the jsons
# Every document this check reads, in one program: a live check on the owner's
# machine has python3 and no jq. Each query prints `key=value` lines, so the
# shell holds them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def load(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return None


def doctor(path):
    """The installation `fugaro doctor --json` names. A document with no project
    in it answers nothing: the run below would be about an installation this
    check cannot name, and its rows would be about whichever one is ambient."""
    doc = load(path)
    p = (doc or {}).get("project") if isinstance(doc, dict) else None
    if not isinstance(p, dict):
        return
    print("name=%s" % (p.get("name") or ""))
    print("gcp=%s" % (p.get("gcp_project") or ""))
    print("region=%s" % (p.get("region") or ""))


def sources(path):
    """The policy layer of `fugaro settings --json`: whether the published node
    was READ at all (its status is a state word, `ok` when it was), and every
    key the read marks `unpublished` -- the local file and the published node
    differ for it, so a change of the operator's is saved and not live. The two
    are one query because they are one question: is there anything for the
    applies below to publish that this check did not write."""
    doc = load(path)
    if not isinstance(doc, dict) or not isinstance(doc.get("sources"), list):
        print("read=no")
        return
    print("read=yes")
    status = ""
    for row in doc["sources"]:
        if isinstance(row, dict) and row.get("layer") == "policy":
            status = row.get("status") or ""
    print("policy-status=%s" % status)
    rows = doc.get("settings")
    if not isinstance(rows, list):
        print("settings=no")
        return
    print("settings=yes")
    unpublished = [r.get("key") or "" for r in rows
                   if isinstance(r, dict) and r.get("state") == "unpublished"]
    print("unpublished=%d" % len(unpublished))
    for key in unpublished:
        print("unpublished-key=%s" % key)


def managed(path):
    """The marks' own answer about the project (`fugaro managed status --json`,
    the one reader of the marker, managed.ReadMarkFacts): whether this
    installation is one Fugaro converges on its owner's behalf. It matters here
    because on such a project a coding agent's session APPLIES the policy stage
    under the gate (D1016), so the marker this check forces the failure with is
    not a refusal there at all."""
    doc = load(path)
    if not isinstance(doc, dict) or "managed" not in doc:
        print("read=no")
        return
    print("read=yes")
    print("managed=%s" % ("yes" if doc.get("managed") else "no"))
    print("reason=%s" % (doc.get("reason") or ""))


def setting(path, key):
    """The row `fugaro settings get <key> --json` answers for one key: whether
    the generic write path carries it at all (`writable`), what decides it now
    (`wins`), whether any layer states it (`set`), the value that decides it and
    the state the read gives it -- `unpublished` is the local file differing
    from the published node, which is a change saved and not live."""
    doc = load(path)
    rows = (doc or {}).get("settings") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        print("read=no")
        return
    for r in rows:
        if isinstance(r, dict) and r.get("key") == key:
            value = r.get("value")
            print("read=yes")
            print("writable=%s" % ("yes" if r.get("writable") else "no"))
            print("wins=%s" % (r.get("wins") or ""))
            print("state=%s" % (r.get("state") or ""))
            print("set=%s" % ("yes" if isinstance(value, str) and value != "" else "no"))
            print("value=%s" % (value if isinstance(value, str) else ""))
            return
    print("read=no")


def pending(path):
    """What `fugaro apply --status --json` says is owed, and what the same
    document says ran: `step=` lines in the record's own order, one `count=`
    line per step name, and `applied=` for a document that carries the list.
    `read=no` is a document this check could not read at all, which is not the
    same answer as an empty record."""
    doc = load(path)
    if not isinstance(doc, dict) or not isinstance(doc.get("pending"), list):
        print("read=no")
        return
    print("read=yes")
    print("recorded=%s" % ("yes" if doc.get("recorded") else "no"))
    print("current=%s" % ("yes" if doc.get("current") else "no"))
    counts = {}
    for row in doc["pending"]:
        if not isinstance(row, dict):
            continue
        step = row.get("step") or ""
        print("step=%s kind=%s key=%s" % (step, row.get("kind") or "", row.get("key") or ""))
        counts[step] = counts.get(step, 0) + 1
    for step in sorted(counts):
        print("count=%s n=%d" % (step, counts[step]))
    print("owed=%d" % len(doc["pending"]))
    applied = doc.get("applied")
    if isinstance(applied, list):
        for a in applied:
            print("applied=%s" % a)


# A stage's own line in a run's transcript, as internal/report prints it: the
# result's glyph, a space, the stage's name, and then the detail that result
# carries — `sep` is " " for a tick and a skip and ": " for a failure or a step
# left for you (report.go's end). The glyphs are the ones a step's own result
# can carry (style.go: a tick, a skip, a cross, the left-for-you marker), and
# NOT the waiting arrow: `↳ <text>` carries no stage name. The plan-view table —
# a mark, a state word, then the name — is deliberately not matched here: an
# applying run points that table at the log (Options.Detail) and what the run's
# own stream carries is these step lines, which is what a check reading the
# command's stderr sees. This is check 391's reading, and the two have to agree:
# a reader matching the other shape finds no row at all on a real run.
ANSI = re.compile(r"\x1b\[[0-9;]*m")
STEP_GLYPHS = ("✔", "+", "–", "-", "✖", "x", "!")
STEP_ROW = re.compile(r"^(?:%s) +([^\s:]+)(?:$|[ \t:])"
                      % "|".join(re.escape(g) for g in STEP_GLYPHS))


def transcript(*paths):
    """The stages of a run's own transcript, one `row=<name> count=<n>` line per
    stage it printed a result for, and the `applied <names>` line the command
    ends on. The count is the whole reading: a stage the loop reached once
    prints one line, and a record that queued the same step per change would
    reach it more than once.

    Both of the run's streams are read, because the two lines live on different
    ones: the stages are the step lines the reporter writes to stderr for the
    length of a run, and `applied <names>` is the account the command ends on,
    which goes to stdout. `read=no` is a reading that could open no file at all,
    which is not the same answer as a run whose stages printed nothing."""
    text = ""
    got = False
    for path in paths:
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                text += fh.read() + "\n"
            got = True
        except OSError:
            continue
    if not got:
        print("read=no")
        return
    print("read=yes")
    counts = {}
    for line in ANSI.sub("", text).splitlines():
        m = STEP_ROW.match(line)
        if m:
            counts[m.group(1)] = counts.get(m.group(1), 0) + 1
            continue
        if line.startswith("applied "):
            print("applied=%s" % line[len("applied "):].strip())
    for name in sorted(counts):
        print("row=%s count=%d" % (name, counts[name]))
    print("rows=%d" % sum(counts.values()))


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "doctor":
        doctor(sys.argv[2])
    elif cmd == "managed":
        managed(sys.argv[2])
    elif cmd == "sources":
        sources(sys.argv[2])
    elif cmd == "setting":
        setting(sys.argv[2], sys.argv[3])
    elif cmd == "pending":
        pending(sys.argv[2])
    elif cmd == "transcript":
        transcript(*sys.argv[2:])
    else:
        sys.exit(64)
PY
}

read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err" || : >"$dest"
}

field() { # field FILE KEY
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# last_line FILE: the last non-blank line of a file, bounded.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-300 || true
  fi
}

# fugaro_call KEY ARGS...: one `fugaro` call, its two streams in the scratch
# directory, its status in FUGARO_STATUS. The ERR trap is dropped inside it,
# because a failing exit status is one of this check's own readings.
FUGARO_STATUS=0
fugaro_call() { # fugaro_call KEY ARGS...
  local key=$1
  shift
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  FUGARO_STATUS=0
  (trap - ERR; exec "$FUGARO_BIN" "$@") </dev/null >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" || FUGARO_STATUS=$?
}

# run_bounded SECONDS KEY CMD...: run one command with its stdin on /dev/null
# and its two streams in the scratch directory, and end it at the deadline. The
# child execs, so the pid the deadline kills IS the command.
RUN_STATUS=0
RUN_TIMED_OUT=no
RUN_SECONDS=0
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  RUN_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  (trap - ERR; exec "$@") </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RUN_TIMED_OUT=yes
      say "the run is still going after ${secs}s: killing it (TERM, then KILL)"
      kill -TERM "$pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 1
    waited=$((waited + 1))
    if [ $((waited % 60)) -eq 0 ]; then
      say "still running after ${waited}s of ${secs}s ..."
    fi
  done
  RUN_STATUS=0
  wait "$pid" 2>/dev/null || RUN_STATUS=$?
  RUN_SECONDS=$((SECONDS - started))
}

fugaro_flags() { # fugaro_flags -- extra flags...
  local out=()
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then out+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then out+=(--project "$PROJECT"); fi
  if [ ${#out[@]} -gt 0 ]; then printf '%s\n' "${out[@]}"; fi
}

# flags_into NAME: read fugaro_flags into the named array.
flags_into() { # flags_into ARRAY
  local -n dest=$1
  dest=()
  local line
  while IFS= read -r line; do dest+=("$line"); done < <(fugaro_flags)
}

# ------------------------------------------------------------ the installation
judge_project() {
  local flags=()
  flags_into flags
  fugaro_call doctor doctor --json ${flags[@]+"${flags[@]}"}
  local status=$FUGARO_STATUS
  read_json "$SCRATCH/project.txt" doctor "$SCRATCH/doctor.out"
  PROJECT_NAME=$(field "$SCRATCH/project.txt" name)
  PROJECT_GCP=$(field "$SCRATCH/project.txt" gcp)
  PROJECT_REGION=$(field "$SCRATCH/project.txt" region)
  if [ -z "$PROJECT_NAME" ] || [ -z "$PROJECT_GCP" ] || [ -z "$PROJECT_REGION" ]; then
    stop "fugaro doctor --json named no project (exit $status: $(last_line "$SCRATCH/doctor.err")), so this check cannot tell which installation it would write: run it where a project config is in force, or pass --project <name>"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    stop "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
  fi
  fact installation PASS "the installation is project $PROJECT_NAME on GCP project $PROJECT_GCP in region $PROJECT_REGION (fugaro doctor --json): the one setting below is written there, and the record the applies read is that installation's"
}

# --------------------------------------------------------------- the managed gate
# step_managed is why this check cannot run on every installation, and it is a
# guard and not a finding. The failure below is forced with a coding agent's
# marker, and on a MANAGED project that marker is not a refusal for the policy
# stage at all: the stage asks the managed gate first and an admitted step
# proceeds (M1, D1016 — a project Fugaro converges on its owner's behalf applies
# in a session exactly as it does for the owner). So on such a project the
# forced run would publish the policy and exit 0, and this check would report
# the loudest FAIL it has about the one thing that did not happen.
#
# It is read from the marks themselves (`fugaro managed status --json`, the one
# reader of the marker) rather than from a sentence, and an installation whose
# marks could not be read is refused too: a run that went on would be a run that
# cannot tell whether the marker below stops the step, which is the one thing
# this check's premise rests on. `fugaro apply` itself reads the same marks
# before it runs a step, so an installation this row cannot read is one the
# command under test is reading as unmanaged — and this check refuses rather
# than bet on which way that fell.
step_managed() {
  local flags=()
  flags_into flags
  fugaro_call managed managed status --json ${flags[@]+"${flags[@]}"}
  local status=$FUGARO_STATUS
  read_json "$SCRATCH/managed.txt" managed "$SCRATCH/managed.out"
  if [ "$(field "$SCRATCH/managed.txt" read)" != yes ]; then
    unmeasured managed "fugaro managed status --json did not answer whether this project is managed (exit $status: $(last_line "$SCRATCH/managed.err")). The failure this check forces is a coding agent's session's refusal, and a managed project's gate applies the policy stage in such a session instead of refusing it (D1016) -- so without this read there is no telling whether the marker below stops the step, which is this check's whole premise. Settle what the line names and run the check again"
    SKIP_REST=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/managed.txt" managed)" = yes ]; then
    unmeasured managed "this project IS managed (fugaro managed status --json answers managed=true): its gate applies the policy stage in a coding agent's session rather than refusing it (D1016), so the marker this check forces the failure with is not a refusal here and the forced run would publish the policy and exit 0. Check 392 measures the failure on an installation that is NOT managed; run it on that one (the sandbox), or run it against the same installation from a session the gate does not open"
    SKIP_REST=yes
    return 0
  fi
  local why
  why=$(field "$SCRATCH/managed.txt" reason)
  fact managed PASS "this project is not managed (fugaro managed status --json answers managed=false${why:+ -- $why}): nothing opens the gate, so the coding agent's marker below really is the refusal that stops the policy stage, which is the failure this check measures"
}

# ------------------------------------------------------------------- the policy
# read_sources writes the `fugaro settings --json` document's policy layer to
# $SCRATCH/sources.txt.
read_sources() {
  local flags=()
  flags_into flags
  fugaro_call sources settings --json ${flags[@]+"${flags[@]}"}
  SOURCES_STATUS=$FUGARO_STATUS
  read_json "$SCRATCH/sources.txt" sources "$SCRATCH/sources.out"
}

step_sync() {
  read_sources
  if [ "$(field "$SCRATCH/sources.txt" read)" != yes ] || [ "$(field "$SCRATCH/sources.txt" settings)" != yes ]; then
    unmeasured sync "fugaro settings --json did not answer its document (exit $SOURCES_STATUS: $(last_line "$SCRATCH/sources.err")), so this check cannot tell whether the published policy was read, nor whether a change of yours is waiting to be published -- and an apply it cannot read is one it must not run. Settle what the line names and run the check again"
    SKIP_REST=yes
    return 0
  fi
  local status
  status=$(field "$SCRATCH/sources.txt" policy-status)
  if [ "$status" != ok ]; then
    unmeasured sync "the policy layer of fugaro settings --json reads '${status:-<nothing>}' and not ok, so the published node was not read: the policy stage skips its work when the database cannot be read and answers the step without doing it, which would make the forced failure below meaningless. Settle what that status names and run the check again"
    SKIP_REST=yes
    return 0
  fi
  local n
  n=$(field "$SCRATCH/sources.txt" unpublished)
  if [ "${n:-0}" != 0 ]; then
    local keys
    keys=$(sed -n 's/^unpublished-key=//p' "$SCRATCH/sources.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    unmeasured sync "the read marks ${n} key(s) unpublished: ${keys}. A key in that state is a change of yours that is saved and not live, and the applies below would publish it -- this check publishes its own setting and nothing else. Run 'fugaro apply' to make those live (or settle the drift), then run the check again"
    SKIP_REST=yes
    return 0
  fi
  fact sync PASS "the published policy was READ (the policy layer of fugaro settings --json reads ok) and no key of the project reads unpublished: the node is in step with this machine's config, so the one setting below is the only thing an apply here would publish"
}

# read_setting KEY DEST: what `fugaro settings get <key> --json` answers, into
# DEST, with the call's status in FUGARO_STATUS.
read_setting() { # read_setting KEY DEST
  local flags=()
  flags_into flags
  fugaro_call "get-$1" settings get "$1" --json ${flags[@]+"${flags[@]}"}
  read_json "$2" setting "$SCRATCH/get-$1.out" "$1"
}

step_key() {
  read_setting "$KEY" "$SCRATCH/key.txt"
  local status=$FUGARO_STATUS
  if [ "$(field "$SCRATCH/key.txt" read)" != yes ]; then
    unmeasured key "fugaro settings get ${KEY} --json answered no row for that key (exit $status: $(last_line "$SCRATCH/get-${KEY}.err")). This check writes the key and puts it back, so a key it cannot read is one it must not write: pass --key with a key of your own and run the check again"
    SKIP_REST=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/key.txt" writable)" != yes ]; then
    unmeasured key "the generic write path does not carry ${KEY} (fugaro settings get --json answers writable=no): this check writes the key with 'fugaro settings set', so a key the write refuses is one it cannot use. Pass --key with a key the write carries (bake_repos, triage and review_against_task are the three of that shape)"
    SKIP_REST=yes
    return 0
  fi
  KEY_WINS=$(field "$SCRATCH/key.txt" wins)
  KEY_SET=$(field "$SCRATCH/key.txt" set)
  KEY_OLD=$(field "$SCRATCH/key.txt" value)
  KEY_STATE=$(field "$SCRATCH/key.txt" state)
  if [ "$KEY_SET" != yes ] || [ "$KEY_WINS" != local ]; then
    unmeasured key "${KEY} is not stated by this machine's own config (fugaro settings get --json answers set=${KEY_SET:-no}, wins=${KEY_WINS:-<nothing>}): a key nothing states is not compared with the published node at all, so the value this check wrote could not be put back -- unsetting the file would leave the member in the node for ever. Pass --key with a key this installation states at the local layer (bake_repos, triage and review_against_task are the three of that shape) and run the check again"
    SKIP_REST=yes
    return 0
  fi
  case "$KEY_OLD" in
  true | false) ;;
  *)
    unmeasured key "${KEY} reads '${KEY_OLD}' and not a boolean: this check writes the other answer of a boolean the published policy carries (its default is the machine's own config stated at the local layer), so a value it cannot invert is one it must not write. Pass --key with a boolean key of that shape and run the check again"
    SKIP_REST=yes
    return 0
    ;;
  esac
  if [ -n "$VALUE" ] && [ "$VALUE" = "$KEY_OLD" ]; then
    unmeasured key "--value $VALUE is the answer ${KEY} already holds (fugaro settings get --json reads ${KEY_OLD}): a write of the same value leaves the local file and the published node equal, so there would be nothing for the policy stage to publish and nothing to force. Pass the other answer, or pass no --value at all and this check writes it"
    SKIP_REST=yes
    return 0
  fi
  if [ -z "$VALUE" ]; then
    if [ "$KEY_OLD" = true ]; then VALUE=false; else VALUE=true; fi
  fi
  if [ "$KEY_STATE" != ok ]; then
    unmeasured key "${KEY} reads state=${KEY_STATE:-<nothing>} before anything was written: the key is already not in step with the published node, and the change below would be one of two this check's applies would carry. Run 'fugaro apply' to make what is saved live, then run the check again"
    SKIP_REST=yes
    return 0
  fi
  fact key PASS "${KEY} is ${KEY_OLD} (${KEY_WINS}, stated by this machine's own config) and the generic write path carries it: this check writes ${VALUE} at the local layer -- the other answer of a boolean the published policy carries -- and puts ${KEY_OLD} back at the end"
}

# ------------------------------------------------------------------- the change
# saved_names FILE: the names of a "saved, not live yet" line, whichever of the
# two doors printed it. The two spell the sentence with two different tails
# ("owed" for --no-apply, "still owed" for a step that did not finish), so this
# reads the names between the colon and the tail rather than pinning either
# wording.
saved_names() { # saved_names FILE
  grep -m1 '^saved, not live yet: ' "$1" 2>/dev/null |
    sed -e 's/^saved, not live yet: //' -e 's/ still owed$//' -e 's/ owed$//' || true
}

step_saved() {
  local flags=()
  flags_into flags
  SAVED_OK=no
  say "the change: ${FUGARO_BIN} settings set ${KEY} ${VALUE} --layer local --no-apply (saved, not live)"
  fugaro_call save settings set "$KEY" "$VALUE" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    fact saved FAIL "the change (fugaro settings set ${KEY} ${VALUE} --layer local --no-apply) exited $FUGARO_STATUS, so nothing was saved and there is nothing to apply: $(last_line "$SCRATCH/save.err")"
    return 0
  fi
  KEY_WRITTEN=yes
  local line
  line=$(saved_names "$SCRATCH/save.out")
  case "$line" in
  policy) ;;
  *)
    fact saved FAIL "the change says it owes '${line:-<nothing>}', and a write of ${KEY} at the local layer owes the policy stage alone (the machine's own config is what the published node is derived from, D2615). The whole line is in $SCRATCH/save.out"
    return 0
    ;;
  esac
  fact saved PASS "the change is saved and not live: ${KEY} is ${VALUE} in this machine's own config, the command exited 0 without running a step (D1565), and its own line says the policy step is owed"
  SAVED_OK=yes
}

# step_drift is what makes the forced failure mean something: the policy stage
# only has work when the local file and the published node differ, and a stage
# with nothing to do answers its step without running it -- so a forced run
# against a config already in step would exit 0 for a reason that has nothing to
# do with the session's marker.
step_drift() {
  read_setting "$KEY" "$SCRATCH/drift.txt"
  if [ "$(field "$SCRATCH/drift.txt" read)" != yes ]; then
    unmeasured drift "fugaro settings get ${KEY} --json did not answer after the write (exit $FUGARO_STATUS: $(last_line "$SCRATCH/get-${KEY}.err")), so whether the change owes the policy stage could not be read"
    return 0
  fi
  local value state
  value=$(field "$SCRATCH/drift.txt" value)
  state=$(field "$SCRATCH/drift.txt" state)
  if [ "$value" != "$VALUE" ]; then
    unmeasured drift "fugaro settings get ${KEY} --json reads '${value}' after the write of ${VALUE}, so the write did not land the way this check reads it and the change below would be about a value this check did not write"
    return 0
  fi
  if [ "$state" != unpublished ]; then
    unmeasured drift "${KEY} reads '${VALUE}' with state=${state:-<nothing>} after the write, and not unpublished: the local file and the published node do not differ, so the policy stage has nothing to do (its own check would answer 'No changes') and the forced failure below would be forced against a step with no work in it. Pass --key with a boolean the published policy carries and this installation states at the local layer (bake_repos, triage and review_against_task are the three of that shape), or pass --value with the other answer, and run the check again"
    return 0
  fi
  fact drift PASS "${KEY} now reads ${VALUE} with state=unpublished (fugaro settings get --json): the change is saved and not live -- the local file and the published node differ -- which is what gives the policy stage work to do and what the forced run below must leave exactly where it is"
}

# ------------------------------------------------------------------ the failure
# step_forced is the fact this check is: the step is run from a coding agent's
# session (the marker on the child alone), so the policy stage does not run, and
# what D1563 promises is read off the command's own answer.
step_forced() {
  local flags=()
  flags_into flags
  say "the forced failure: CLAUDECODE=1 ${FUGARO_BIN} apply (a coding agent's session may not publish the policy: D1571)"
  run_bounded "$DEADLINE" forced env CLAUDECODE=1 "$FUGARO_BIN" apply ${flags[@]+"${flags[@]}"}
  FORCED_STATUS=$RUN_STATUS
  FORCED_TIMED_OUT=$RUN_TIMED_OUT
  FORCED_SECONDS=$RUN_SECONDS
  if [ "$FORCED_TIMED_OUT" = yes ]; then
    fact forced FAIL "the forced apply was still running after ${DEADLINE}s and this check killed it. A policy publication is seconds; a run that never ends is not the failure this check forces, and what it left is in $SCRATCH/forced.err"
    return 0
  fi
  if [ "$FORCED_STATUS" = 0 ]; then
    fact forced FAIL "the forced apply exited 0 with CLAUDECODE=1 set on its own command line: the policy step RAN in a coding agent's session, which is the one thing that must never happen (D1571). Read $SCRATCH/forced.out and $SCRATCH/forced.err, and check what the published policy now carries"
    return 0
  fi
  local line
  line=$(saved_names "$SCRATCH/forced.out")
  case "$line" in
  policy) ;;
  *)
    fact forced FAIL "the forced apply exited ${FORCED_STATUS} without D1563's sentence: its stdout reads 'saved, not live yet: ${line:-<no such line>}' and the one this door prints for a step it could not answer is 'saved, not live yet: policy still owed'. The whole stdout is in $SCRATCH/forced.out"
    return 0
    ;;
  esac
  # The sentence word for word, because it is the door's own: a chain that saved
  # a change with --no-apply says "owed" and this door says "still owed"
  # (chainSavedLine, applyKeptLine), and a reader has to be able to tell which of
  # the two left the change behind. The line under it is the promise the step is
  # measured against, so it is held to its wording too.
  if ! grep -q '^saved, not live yet: policy still owed$' "$SCRATCH/forced.out"; then
    fact forced FAIL "the forced apply printed a 'saved, not live yet' line naming policy, and the one this door prints for a step it could not answer is 'saved, not live yet: policy still owed', word for word. Its stdout is in $SCRATCH/forced.out"
    return 0
  fi
  if ! grep -q '^Run `fugaro apply` again to finish the steps it can answer' "$SCRATCH/forced.out"; then
    fact forced FAIL "the forced apply said the policy step is still owed without saying what finishes it: the line under the sentence is 'Run \`fugaro apply\` again to finish the steps it can answer: every step checks the state first, so the ones that already ran cost nothing.', and the command it names is the one this check runs next. Its stdout is in $SCRATCH/forced.out"
    return 0
  fi
  local got
  got=$(cat "$SCRATCH/forced.out" "$SCRATCH/forced.err" 2>/dev/null || true)
  case "$got" in
  *"a coding agent's session is present (CLAUDECODE is set)"*) ;;
  *)
    fact forced FAIL "the forced apply exited ${FORCED_STATUS} and said the policy step is still owed, and its own account does not carry the session's refusal ('a coding agent's session is present (CLAUDECODE is set) ...'). What stopped the step has to be readable: the refusal is what says the step did not run rather than the write failing. The two streams are in $SCRATCH/forced.out and $SCRATCH/forced.err"
    return 0
    ;;
  esac
  case "$got" in
  *"run this in your own terminal window, not through a coding agent"*) ;;
  *)
    fact forced FAIL "the session's refusal does not carry what the person can do about it ('run this in your own terminal window, not through a coding agent'): an agent handed half that sentence has nothing to hand over. The two streams are in $SCRATCH/forced.out and $SCRATCH/forced.err"
    return 0
    ;;
  esac
  FORCED_OK=yes
  fact forced PASS "the policy step did not run from a coding agent's session and the change was KEPT: the forced apply exited ${FORCED_STATUS} in ${FORCED_SECONDS}s, its stdout carries D1563's sentence ('saved, not live yet: policy still owed' and the line naming 'fugaro apply' to finish it), and its own account carries the session's refusal (D1571). The failure cost the run and not the work"
}

step_owed() {
  read_pending "$SCRATCH/pending-after-forced.txt"
  if [ "$(field "$SCRATCH/pending-after-forced.txt" read)" != yes ]; then
    unmeasured owed "fugaro apply --status --json did not answer its document after the forced apply (exit $PENDING_STATUS: $(last_line "$SCRATCH/status.err")), so what is still owed could not be read"
    return 0
  fi
  OWED_AFTER_FORCED=$(field "$SCRATCH/pending-after-forced.txt" owed)
  local c_policy
  c_policy=$(sed -n 's/^count=policy n=//p' "$SCRATCH/pending-after-forced.txt" | head -n 1)
  if [ "${OWED_AFTER_FORCED:-0}" != 1 ] || [ "${c_policy:-0}" != 1 ]; then
    local steps
    steps=$(sed -n 's/^step=\([^ ]*\).*/\1/p' "$SCRATCH/pending-after-forced.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    fact owed FAIL "the record after the forced apply owes ${OWED_AFTER_FORCED:-0} step(s): ${steps:-<none>}, and the step that did not run must still be owed exactly once (policy recorded ${c_policy:-0} time(s)). D1563 keeps the change and the step; a record that cleared it would make the sentence above a promise nothing can keep"
    return 0
  fi
  read_setting "$KEY" "$SCRATCH/still-saved.txt"
  if [ "$(field "$SCRATCH/still-saved.txt" read)" != yes ]; then
    unmeasured owed "fugaro settings get ${KEY} --json did not answer after the forced apply (exit $FUGARO_STATUS: $(last_line "$SCRATCH/get-${KEY}.err")), so whether the change is still saved could not be read"
    return 0
  fi
  local value state
  value=$(field "$SCRATCH/still-saved.txt" value)
  state=$(field "$SCRATCH/still-saved.txt" state)
  if [ "$value" != "$VALUE" ] || [ "$state" != unpublished ]; then
    fact owed FAIL "after the forced apply ${KEY} reads '${value}' with state=${state:-<nothing>}, and the change this check saved is ${VALUE} unpublished: a step that did not run must leave the change saved and the published node where it was, so either the value moved or something published it. The node the policy carries now is what 'fugaro settings --json' answers"
    return 0
  fi
  fact owed PASS "the record owes exactly one step, policy, once (fugaro apply --status --json, recorded=$(field "$SCRATCH/pending-after-forced.txt" recorded)), and ${KEY} still reads ${VALUE} unpublished: the change is saved, the published node did not move, and the step is owed -- which is the half of D1563 the sentence above promises"
}

# ----------------------------------------------------------------- the recovery
step_finish() {
  local flags=()
  flags_into flags
  say "the recovery: ${FUGARO_BIN} apply (no marker, nothing typed)"
  run_bounded "$DEADLINE" finish "$FUGARO_BIN" apply ${flags[@]+"${flags[@]}"}
  FINISH_STATUS=$RUN_STATUS
  FINISH_TIMED_OUT=$RUN_TIMED_OUT
  FINISH_SECONDS=$RUN_SECONDS
  if [ "$FINISH_TIMED_OUT" = yes ]; then
    fact finish FAIL "the recovery apply was still running after ${DEADLINE}s and this check killed it: $(last_line "$SCRATCH/finish.err")"
    return 0
  fi
  read_json "$SCRATCH/finish.txt" transcript "$SCRATCH/finish.out" "$SCRATCH/finish.err"
  APPLIED_LINE=$(field "$SCRATCH/finish.txt" applied)
  if [ "$FINISH_STATUS" != 0 ]; then
    fact finish FAIL "the recovery apply exited ${FINISH_STATUS} and did not finish the step the forced run left owed: $(last_line "$SCRATCH/finish.err"). Its whole stdout is in $SCRATCH/finish.out and the record still owes what 'fugaro apply --status' answers"
    return 0
  fi
  if [ "$APPLIED_LINE" != policy ]; then
    fact finish FAIL "the recovery apply exited 0 and its own account reads 'applied ${APPLIED_LINE:-<nothing>}', and the step it had to answer is policy: a run that answered something else, or nothing, did not finish this check's change. Its whole stdout is in $SCRATCH/finish.out"
    return 0
  fi
  fact finish PASS "the same command finished the job: 'fugaro apply' exited 0 in ${FINISH_SECONDS}s with no marker and its stdin on /dev/null, and its own account says it applied policy -- the step the forced run left owed, run once, with nothing typed"
}

step_closed() {
  read_pending "$SCRATCH/pending-after-finish.txt"
  if [ "$(field "$SCRATCH/pending-after-finish.txt" read)" != yes ]; then
    unmeasured closed "fugaro apply --status --json did not answer its document after the recovery (exit $PENDING_STATUS: $(last_line "$SCRATCH/status.err")), so what is still owed could not be read"
    return 0
  fi
  local owed
  owed=$(field "$SCRATCH/pending-after-finish.txt" owed)
  if [ "${owed:-0}" != 0 ]; then
    local steps
    steps=$(sed -n 's/^step=\([^ ]*\).*/\1/p' "$SCRATCH/pending-after-finish.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    fact closed FAIL "the record still owes ${owed} step(s) after the recovery apply: ${steps}. A run that applied the step clears the entry it answered (D1563, ClearUnchanged), and a record that keeps it is a step nobody will run again"
    return 0
  fi
  read_setting "$KEY" "$SCRATCH/live.txt"
  if [ "$(field "$SCRATCH/live.txt" read)" != yes ]; then
    unmeasured closed "fugaro settings get ${KEY} --json did not answer after the recovery (exit $FUGARO_STATUS: $(last_line "$SCRATCH/get-${KEY}.err")), so whether the change is live could not be read"
    return 0
  fi
  local value state
  value=$(field "$SCRATCH/live.txt" value)
  state=$(field "$SCRATCH/live.txt" state)
  if [ "$value" != "$VALUE" ] || [ "$state" != ok ]; then
    fact closed FAIL "after the recovery apply ${KEY} reads '${value}' with state=${state:-<nothing>}, and the change that was published is ${VALUE} ok: the published node has to carry the value the step was owed for. If it reads unpublished the publish did not reach the node; if it reads another value something else published"
    return 0
  fi
  fact closed PASS "the change is LIVE: fugaro apply --status --json owes nothing and ${KEY} reads ${VALUE} with state=ok -- the published node carries it. The step the forced run left owed is the change this machine now runs by"
}

# ------------------------------------------------------------------- teardown
step_teardown() {
  local flags=()
  TEARDOWN_OK=no
  flags_into flags
  say "teardown: undoing what this check wrote"
  if [ "${KEY_WRITTEN:-}" != yes ]; then
    TEARDOWN_STATUS=0
    TEARDOWN_TIMED_OUT=no
    fact teardown PASS "nothing to put back: this check wrote no setting (the row above says why it stopped before the write), so the installation is as it found it"
    TEARDOWN_OK=yes
    return 0
  fi
  fugaro_call undo settings set "$KEY" "$KEY_OLD" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    fact teardown FAIL "the setting ${KEY} could not be put back (exit $FUGARO_STATUS: $(last_line "$SCRATCH/undo.err")): this machine's config still carries the value this check wrote. Put it back yourself with 'fugaro settings set ${KEY} ${KEY_OLD} --layer local', then run 'fugaro apply'"
    return 0
  fi
  say "teardown: one ${FUGARO_BIN} apply to make the undoing live"
  run_bounded "$DEADLINE" undo-apply "$FUGARO_BIN" apply ${flags[@]+"${flags[@]}"}
  TEARDOWN_STATUS=$RUN_STATUS
  TEARDOWN_TIMED_OUT=$RUN_TIMED_OUT
  if [ "$TEARDOWN_TIMED_OUT" = yes ] || [ "$TEARDOWN_STATUS" != 0 ]; then
    fact teardown FAIL "the teardown's own fugaro apply exited ${TEARDOWN_STATUS} (timed out: ${TEARDOWN_TIMED_OUT}): $(last_line "$SCRATCH/undo-apply.err"). The undoing is saved and its step is still owed -- run 'fugaro apply' yourself and check 'fugaro apply --status' owes nothing"
    return 0
  fi
  read_pending "$SCRATCH/pending-end.txt"
  if [ "$(field "$SCRATCH/pending-end.txt" read)" != yes ]; then
    unmeasured teardown "the record could not be read after the teardown (exit $PENDING_STATUS: $(last_line "$SCRATCH/status.err"))"
    return 0
  fi
  local end
  end=$(field "$SCRATCH/pending-end.txt" owed)
  if [ "${end:-0}" != 0 ]; then
    local steps
    steps=$(sed -n 's/^step=\([^ ]*\).*/\1/p' "$SCRATCH/pending-end.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    fact teardown FAIL "the teardown left ${end} step(s) owed: ${steps}. Run 'fugaro apply' and check 'fugaro apply --status' owes nothing"
    return 0
  fi
  read_setting "$KEY" "$SCRATCH/back.txt"
  if [ "$(field "$SCRATCH/back.txt" read)" != yes ]; then
    unmeasured teardown "fugaro settings get ${KEY} --json did not answer after the teardown (exit $FUGARO_STATUS: $(last_line "$SCRATCH/get-${KEY}.err")), so whether the setting is back could not be read"
    return 0
  fi
  local value state
  value=$(field "$SCRATCH/back.txt" value)
  state=$(field "$SCRATCH/back.txt" state)
  if [ "$value" != "$KEY_OLD" ] || [ "$state" != ok ]; then
    fact teardown FAIL "the teardown left ${KEY} reading '${value}' with state=${state:-<nothing>}, and this check found it ${KEY_OLD} ok: the setting is not back the way it was found. Put it back yourself with 'fugaro settings set ${KEY} ${KEY_OLD} --layer local' and run 'fugaro apply'"
    return 0
  fi
  fact teardown PASS "${KEY} is back to ${KEY_OLD} with state=ok and the record owes nothing: one fugaro apply made the undoing live, the published node carries what it carried before this check ran, and the scratch directory is removed at the end"
  TEARDOWN_OK=yes
}

# read_pending DEST: `fugaro apply --status --json`, its document read into DEST.
read_pending() { # read_pending DEST
  local flags=()
  flags_into flags
  fugaro_call status apply --status --json ${flags[@]+"${flags[@]}"}
  PENDING_STATUS=$FUGARO_STATUS
  read_json "$1" pending "$SCRATCH/status.out"
}

# ------------------------------------------------------------------- the verdict
the_verdict() {
  if [ "${SAVED_OK:-}" = no ]; then
    fact claim FAIL "the claim does NOT hold as measured: the change this check writes was not saved (the saved row says how it ended), so there was no change for a step that does not run to leave behind"
    REACHED_END=yes
    exit 1
  fi
  if [ "${TEARDOWN_OK:-}" = no ]; then
    case " ${UNMEASURED_KEYS:-} " in
    *" teardown "*) ;;
    *)
      fact claim FAIL "the claim does NOT hold as measured: the teardown did NOT leave the installation as this check found it (the teardown row says what is still there), so the readings above are about an installation this check changed and did not put back"
      REACHED_END=yes
      exit 1
      ;;
    esac
  fi
  local key verdict failed=""
  while IFS='|' read -r key verdict _; do
    [ -n "$key" ] || continue
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) ;; *) failed="${failed:+$failed }$key" ;; esac
      ;;
    esac
  done <"$FACTS_FILE"
  if [ -n "$failed" ]; then
    fact claim FAIL "the claim does NOT hold as measured: the row(s) ${failed// /, } above did not hold, and each of them says why. Read them before the claim: the rows are the reading, this line is only their sum"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    fact claim INFO "the claim is NOT MEASURED by this run: the row(s) ${UNMEASURED_KEYS// /, } above could not be read, so nothing here is an answer about what a policy step that does not run leaves behind. Settle what the row names and run the check again"
    REACHED_END=yes
    exit 2
  fi
  fact claim PASS "a policy step that did not run left the change saved, the step owed and the command non-zero with D1563's sentence on it, and ONE fugaro apply finished the job with nothing typed and nothing published twice: the failure cost the run and not the work (D1563, D1571), and the installation is back as this check found it"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL -- not reached. That is what makes the block safe to diff between runs.
BLOCK_KEYS=(xtrace agent-session installation belong managed sync key saved drift forced owed finish closed teardown claim)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 392 RESULT\n' >&3
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
  printf 'CHECK 392 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured"
    fi
    status=2
  fi
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
  say "  [reads] ${FUGARO_BIN} doctor --json${PROJECT:+ --project $PROJECT}: the project, its GCP project and its region, with a project named belong refused here and in what doctor answers"
  say "  [reads] ${FUGARO_BIN} managed status --json: whether the project is managed. A MANAGED project stops the run here, because its gate applies the policy stage in a coding agent's session instead of refusing it (D1016), so the marker the failure is forced with is no refusal there"
  say "  [reads] ${FUGARO_BIN} settings --json: that the published policy was read (the policy layer's status is ok) and that no key reads unpublished -- a change of yours waiting to be published is a reason not to write"
  say "  [reads] ${FUGARO_BIN} settings get ${KEY} --json: that ${KEY} is stated by this machine's own config at the local layer, that the generic write path carries it, and that it is a boolean. This is what the teardown puts back"
  say "  [writes] ${FUGARO_BIN} settings set ${KEY} ${VALUE:-<the other answer>} --layer local --no-apply: the change, saved and not live (it owes policy)"
  say "  [reads] ${FUGARO_BIN} settings get ${KEY} --json: that the key now reads the written answer with state=unpublished, so the policy stage really has work to do"
  say "  [writes] CLAUDECODE=1 ${FUGARO_BIN} apply: THE FORCED FAILURE. A coding agent's session may not publish the policy (D1571), so the step does not run: it must exit non-zero, print 'saved, not live yet: policy still owed' and carry the session's refusal, under a ${DEADLINE}s deadline"
  say "  [reads] ${FUGARO_BIN} apply --status --json and ${FUGARO_BIN} settings get ${KEY} --json: the step still owed exactly once, and the change still saved with the node where it was"
  say "  [writes] ${FUGARO_BIN} apply: THE RECOVERY. No marker, nothing typed, under the same deadline: it must exit 0 and say it applied policy"
  say "  [reads] ${FUGARO_BIN} apply --status --json and ${FUGARO_BIN} settings get ${KEY} --json: nothing owed, and the written answer live (state=ok)"
  say "  [writes] ${FUGARO_BIN} settings set ${KEY} ${KEY_OLD:-<the answer it held>} --layer local --no-apply, then ONE ${FUGARO_BIN} apply: the teardown, which puts the setting back and makes that live"
  say "  [writes] nothing else: one scratch directory (mode 0700) holding the transcripts, removed at the end"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK392_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK392_DRY_RUN:-}
  DEADLINE=${CHECK392_DEADLINE:-600}
  KEY=${CHECK392_KEY:-bake_repos}
  VALUE=${CHECK392_VALUE:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --key)
      [ $# -ge 2 ] || die "--key needs a settings key. usage: $USAGE"
      KEY=$2
      shift 2
      ;;
    --value)
      [ $# -ge 2 ] || die "--value needs true or false. usage: $USAGE"
      VALUE=$2
      shift 2
      ;;
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
      printf '\nWrites ONE setting with --no-apply, forces the `policy` step to fail (a\ncoding agent\x27s session may not publish it), reads that the change is still saved\nand the step still owed with a non-zero exit, and then runs ONE `%s apply` to\nfinish it. It prints one block headed CHECK 392 RESULT and exits 0 when the\nclaim held, 1 when it did not, and 2 when this run could not tell. It is yours\nto run: no agent runs any step of it.\n' "$FUGARO_BIN"
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
  if [ -z "$KEY" ]; then
    die "Check 392 needs a settings key to write (--key, or CHECK392_KEY): it is the change that owes the policy stage alone. usage: $USAGE"
  fi
  case "$VALUE" in
  "" | true | false) ;;
  *) die "--value must be true or false (got '$VALUE'): the key this check writes is a boolean. usage: $USAGE" ;;
  esac
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  write_reader
  guard_xtrace
  guard_agent_session
  if [ -n "$DRY_RUN" ]; then
    if [ -n "$PROJECT" ]; then
      guard_belong "$PROJECT" "the project you typed"
      fact belong PASS "the project name you typed ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
    else
      fact belong INFO "no --project was typed, so the guard had nothing to judge: a real run refuses an installation named belong in what doctor answers too, before anything is run"
    fi
    fact installation INFO "planned: a real run asks fugaro doctor --json which installation the config in force names$( [ -n "$PROJECT" ] && printf ', holds it to the --project %s you typed' "$PROJECT" ) and refuses an installation named belong in that answer too"
    fact managed INFO "planned: a real run asks fugaro managed status --json whether the project is managed, and STOPS here when it is: a managed project's gate applies the policy stage in a coding agent's session rather than refusing it (D1016), so the marker the forced failure is made of is no refusal there"
    fact sync INFO "planned: a real run reads fugaro settings --json and holds the policy layer to a status of ok with no key reading unpublished, so the applies below carry this check's change and nothing else"
    fact key INFO "planned: a real run reads fugaro settings get ${KEY} --json and requires it stated at the local layer (set=yes, wins=local), carried by the generic write path (writable=yes) and a boolean: what it holds now is what the teardown puts back"
    fact saved INFO "planned: a real run writes the change with fugaro settings set ${KEY} ${VALUE:-<the other answer>} --layer local --no-apply, which owes the policy stage alone"
    fact drift INFO "planned: a real run reads the key back and requires state=unpublished, so the policy stage really has work to do rather than answering 'No changes'"
    fact forced INFO "planned: a real run runs CLAUDECODE=1 fugaro apply under a ${DEADLINE}s deadline, and requires exit non-zero, D1563's sentence ('saved, not live yet: policy still owed') and the session's refusal in the run's own account"
    fact owed INFO "planned: a real run reads fugaro apply --status --json and the key again, requiring exactly one step owed (policy) and the change still saved with the published node where it was"
    fact finish INFO "planned: a real run runs fugaro apply with no marker and its stdin on /dev/null, requiring exit 0 and an account saying it applied policy"
    fact closed INFO "planned: a real run reads fugaro apply --status --json again, which must owe nothing, and the key, which must read the written answer with state=ok"
    fact teardown INFO "planned: a real run puts the key back to the answer it held and runs ONE fugaro apply to make that live, then reads both back"
    fact claim INFO "planned: the verdict is a real run's, and it is the CLI's own record and documents that answer it"
    BLOCK_KEYS=(xtrace agent-session installation belong managed sync key saved drift forced owed finish closed teardown claim)
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  step_managed
  # A managed project's gate applies the policy stage in a coding agent's
  # session, so the marker this check forces the failure with is no refusal
  # there: without this row the forced run would exit 0 and this check would
  # report the one thing that did not happen.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_sync
  # A policy database that could not be read, or a change of yours waiting to be
  # published, is a reason NOT to run the commands: the first makes the forced
  # failure meaningless and the second would be published by this check's applies.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_key
  # A key this check cannot read, cannot write, is not stated at a layer, or is
  # not a boolean is one it must not write: it could not put it back.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_saved
  # The forced failure is only a reading if the change really was saved.
  if [ "${SAVED_OK:-}" = yes ]; then
    step_drift
  fi
  # And it is only a reading if the policy stage really has work: a config
  # already in step would answer the step without running it, and the forced run
  # would exit 0 for a reason that has nothing to do with the marker.
  if [ "${SAVED_OK:-}" = yes ] && [ -z "$UNMEASURED_KEYS" ]; then
    step_forced
    step_owed
    step_finish
    step_closed
  fi
  step_teardown
  the_verdict
}

FUGARO_BIN=${CHECK392_FUGARO:-fugaro}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
PROJECT_REGION=""
KEY=bake_repos
VALUE=""
DEADLINE=600
KEY_WINS=""
KEY_SET=""
KEY_OLD=""
KEY_STATE=""
SOURCES_STATUS=0
SKIP_REST=""
KEY_WRITTEN=""
SAVED_OK=""
FORCED_STATUS=0
FORCED_TIMED_OUT=no
FORCED_SECONDS=0
FORCED_OK=""
PENDING_STATUS=0
OWED_AFTER_FORCED=""
FINISH_STATUS=0
FINISH_TIMED_OUT=no
FINISH_SECONDS=0
APPLIED_LINE=""
TEARDOWN_OK=""
TEARDOWN_STATUS=0
TEARDOWN_TIMED_OUT=no

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
