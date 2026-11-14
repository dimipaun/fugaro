#!/usr/bin/env bash
# Check 304 — the handoff between two people, as ONE script YOU run in TWO
# phases, one per person.
#
# It settles the live half of O17 of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; D1222 answered
# 2026-11-04), and the register's row and section are docs/gcp-live-checklist.md,
# "Check 304": an agent handed from one developer to another by an owner or an
# admin, on a real installation, with the two people in different sessions — the
# receiving person holding the agent, the giving person no longer able to act as
# it, and the project-owned agent that belongs to no person at all.
#
# Usage:      bash scripts/live-checks/check304.sh first-person \
#               --installation <name> --from <user:address> --to <user:address> \
#               [--agent <id>] [--handoff <path>] [--repo <checkout>] [--dry-run]
#             bash scripts/live-checks/check304.sh second-person \
#               --installation <name> --as <user:address> --handoff <path> \
#               [--repo <checkout>] [--dry-run]
#             <name> is the Fugaro project name the local config in force carries
#             (`fugaro doctor --json`'s project.name), and the answer is held to
#             it: a name that is not the one doctor reports is a STOP before
#             anything is run. A member is spelled the way the tree spells one
#             ("user:<address>", access.Grant.User), and a value that is not
#             spelled that way is refused rather than written.
# Two people: FIRST PERSON — the one who holds the agent today, on the machine
#             whose credentials the agent is driven with. It reads the build, the
#             agent and the node, writes the handoff record and prints exactly
#             what the second person runs.
#             SECOND PERSON — the one who receives it, on their own machine and
#             with their own credentials, with the record sent to them. It reads
#             the same node and the lease from that session and reports both
#             readings.
# Needs:      bash, mktemp, timeout, python3 and fugaro on PATH, the Subscription
#             Edition build (see below), and TWO PEOPLE with their own
#             credentials against the installation. Nothing is installed and
#             nothing is fetched; a missing tool stops the run with one line
#             naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused,
#             whether you typed it as the installation or doctor answers it.
#             The agent is the installation's own orchestrator node
#             (`orchestrator/<project>`, the id `fugaro orchestrator ls --json`
#             lists), so run both phases against the installation whose agent
#             you mean.
# Costs:      nothing billable and no model call: this check starts no daemon
#             and runs no cycle, so it spends no money on your login and none of
#             Fugaro's. The register's own row says so: "no model call unless the
#             agent is asked to work, and nothing billable in the handoff
#             itself". The receiving person may start their own daemon — the
#             check names the command and does not run it.
# Runs:       by the TWO OF YOU, each in their own session. No agent runs any
#             step of it, and no step prints a credential or a token: nothing it
#             reads carries one, and the members it takes are refused unless they
#             are spelled "user:<address>".
# Cleanup:    it writes NOTHING on the installation — no node, no grant, no work
#             item. The one thing it writes is the handoff record on the first
#             person's machine (mode 0600, the path it prints), which the second
#             person needs and the first person keeps until the result is pasted
#             back; both phases say so as facts. The scratch directory each phase
#             makes is removed at the end.
# Output:     progress on stderr, ONE block on stdout headed CHECK 304 RESULT,
#             to copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is missing,
#             the build is not the Subscription Edition, the build carries no
#             command that performs a handoff).
#
# THE SUBSCRIPTION EDITION IS CHECKED FIRST, and it is a STOP and not a FAIL. The
# handoff's rule — an owner or a project-scope admin, over the project's own
# governance — is the build's shared half (D1222, F14): ee/orchestrator's
# Transfer, installed into internal/orchestrator.SetHandoff by ee/register, with
# the core carrying a seam that refuses every handoff. A core build has nothing
# to exercise, so the check reads the edition from the build's own paid surface
# before it reads anything else.
#
# THE HANDOFF SURFACE IS THE SECOND GATE, AND IT IS THE FINDING THIS RELEASE
# CARRIES. A handoff is a command a person runs, and this build has none: the
# register's row says "the `ee/` command O17 builds", the plan's own O17 file
# list carries no command file, ee/register installs the rule into the core's
# seam, and internal/orchestrator.HandoffAgent is called from nowhere in the
# tree. So the check asks the build for the verb (`fugaro orchestrator --help`)
# and, when there is none, records the fact that says exactly that and stops:
# Check 304 needs the release that gives the handoff a command, and until then
# there is nothing for the two of you to run. The steps below the gate are
# written for that release and are exercised against a fake in
# scripts/live-checks/check304_test.sh.
#
# No trace: `bash -x` prints every member and every document this check reads, so
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
USAGE="bash scripts/live-checks/check304.sh <first-person|second-person> --installation <name> …"
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
# all, and the block would then read as a clean handoff for a run that read
# nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the handoff" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 304 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  need bash "every child this check starts runs on it"
  need mktemp
  need timeout "every fugaro call this check makes is bounded, so a call that never returns cannot hang the check"
  need python3 "the handoff record and the two documents are read and written with it"
  need fugaro "doctor names the installation and every step below is a fugaro command"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk304-mine"
SCRATCH_REMOVED=no
PRINTED=""
HELPED=no
PHASE=""
INSTALLATION=""
AGENT=""
AGENT_NAMED=no
FROM_MEMBER=""
TO_MEMBER=""
AS_MEMBER=""
HANDOFF=""
REPO=""
DRY_RUN=no
EDITION=""
HANDOFF_VERB=""
VERSION_LINE=""
LEASE_BEFORE=""
LEASE_AFTER=""
LEASE_BEAT=""
NODE_ID=""
NODE_SOURCE=""
NODE_REV=""
NODE_BRIEF=""
RECORD_TO=""
RECORD_FROM=""
RECORD_AGENT=""
RECORD_LEASE=""
RECORD_NODE_ID=""
RECORD_NODE_REV=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk304.XXXXXXXX") || die "Check 304 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 304 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
  # finished handoff would offer the rows that were never read as a verdict
  # about it.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if ! printf '%s\n' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|INFO|nothing of this check's was written on the installation, so there is nothing there to undo"
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which prints the documents this check reads and the members you typed"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the members you typed or the documents it read"
}

# guard_belong: `belong` is the owner's own installation wherever it is named —
# the name you typed, or the one doctor answers.
guard_belong() { # guard_belong NAME WHERE
  case "$1" in
  belong | belong-*)
    case "$2" in
    typed) fact installation FAIL "the installation named on the command line is $1, which is the owner's own: Check 304 hands an agent between two people, so it is not run against belong" ;;
    *) fact installation FAIL "the local config in force answers $1, which is the owner's own installation" ;;
    esac
    stop "Check 304 never runs against 'belong'. Name another installation: $USAGE"
    ;;
  esac
}

# guard_member VALUE: a member is spelled the way access.Grant.User spells one,
# and the spelling is also what keeps a credential out of the record: a value
# that is not "user:<address>" is refused rather than written down and pasted
# back, so a token pasted here by accident never reaches a file or the block.
guard_member() { # guard_member WHAT VALUE
  local what=$1 value=$2
  case "$value" in
  user:*) ;;
  *) die "$what is not a member: a member is spelled 'user:<address>' (access.Grant.User). This check refuses it rather than print it, so nothing of the value reaches this message, the record or the block — a credential pasted here by accident is never echoed and never written" ;;
  esac
  if printf '%s' "$value" | grep -q '[^A-Za-z0-9._%+@:-]'; then
    die "$what carries a character a member cannot carry, so it is not written into the handoff record and not printed here. A member is 'user:<address>'; a credential or a token is never one and is never written by this check"
  fi
}

# guard_agent_id: the id names a node under orchestrator/ and is put into a path
# by every read below, so it is held to the characters an id can carry
# (internal/orchestrator's own checkAgentID) rather than passed on.
guard_agent_id() { # guard_agent_id WHAT VALUE
  case "$1:$2" in
  *:"" | *:*[!A-Za-z0-9._-]*) die "$1 is not an orchestrator id: an id is letters, digits, '.', '_' and '-' only, because it names the node under orchestrator/. Nothing was read, and the value is not printed here — an id is put into a path and a query, never echoed" ;;
  esac
}

parse_args() {
  [ $# -gt 0 ] || die "the phase is required: first-person or second-person. usage: $USAGE"
  case "$1" in
  first-person | second-person)
    PHASE=$1
    shift
    ;;
  -h | --help)
    printf 'usage: %s\n' "$USAGE"
    printf '  first-person  --installation <name> --from <user:address> --to <user:address> [--agent <id>] [--handoff <path>] [--repo <checkout>] [--dry-run]\n'
    printf '  second-person --installation <name> --as <user:address> --handoff <path> [--repo <checkout>] [--dry-run]\n'
    HELPED=yes
    PRINTED=1
    exit 0
    ;;
  -*)
    die "the phase is required and '$1' is a flag: the first argument is first-person or second-person. usage: $USAGE"
    ;;
  *)
    die "unknown phase '$1': the first argument is first-person or second-person. usage: $USAGE"
    ;;
  esac
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --agent)
      [ $# -ge 2 ] || die "--agent needs an orchestrator id. usage: $USAGE"
      AGENT=$2
      AGENT_NAMED=yes
      shift 2
      ;;
    --from)
      [ $# -ge 2 ] || die "--from needs a member ('user:<address>'). usage: $USAGE"
      FROM_MEMBER=$2
      shift 2
      ;;
    --to)
      [ $# -ge 2 ] || die "--to needs a member ('user:<address>'). usage: $USAGE"
      TO_MEMBER=$2
      shift 2
      ;;
    --as)
      [ $# -ge 2 ] || die "--as needs the member running this phase ('user:<address>'). usage: $USAGE"
      AS_MEMBER=$2
      shift 2
      ;;
    --handoff)
      [ $# -ge 2 ] || die "--handoff needs a path. usage: $USAGE"
      HANDOFF=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a checkout path. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  if [ "$AGENT_NAMED" = yes ]; then guard_agent_id "--agent" "$AGENT"; fi
  [ -n "$INSTALLATION" ] || die "--installation is required, and it must not be belong. usage: $USAGE"
  case "$PHASE" in
  first-person)
    [ -n "$FROM_MEMBER" ] || die "first-person needs --from <user:address>: the member handing the agent over. usage: $USAGE"
    [ -n "$TO_MEMBER" ] || die "first-person needs --to <user:address>: the member receiving it. usage: $USAGE"
    guard_member "--from" "$FROM_MEMBER"
    guard_member "--to" "$TO_MEMBER"
    [ "$FROM_MEMBER" != "$TO_MEMBER" ] || die "--from and --to are the same member ($FROM_MEMBER): a handoff moves an agent between two people, so this is refused rather than recorded"
    ;;
  second-person)
    [ -n "$AS_MEMBER" ] || die "second-person needs --as <user:address>: the member running this phase. usage: $USAGE"
    guard_member "--as" "$AS_MEMBER"
    [ -n "$HANDOFF" ] || die "second-person needs --handoff <path>: the record the first person wrote and sent you. usage: $USAGE"
    ;;
  esac
  [ -n "$HANDOFF" ] || HANDOFF="${TMPDIR:-/tmp}/fugaro-check304-handoff.json"
}

# --------------------------------------------------------------- the commands
# fg TIMEOUT ARGS... — every fugaro call this check makes is bounded, so a call
# that never returns ends the step instead of hanging the check.
fg() {
  local secs=$1
  shift
  timeout --kill-after=10 "$secs" "$@"
}

# safe_field cleans a value that came out of the database before it reaches the
# block: one line, and no character a terminal cannot take. The block is pasted
# back, and an id or a reason can carry anything.
safe_field() {
  printf '%s' "$1" | tr -d '\000-\010\013\014\016-\037' | tr '\n\r\t' '   ' | cut -c1-200
}

# ls_query FILE MODE [ARGS…] — what `fugaro orchestrator ls --json` says, read
# with the ids as VALUES rather than built into an expression: an id is typed by
# a person, and one that carried a quote would otherwise be parsed as code.
#   field FILE ID KEY  — the value of KEY for the orchestrator ID ("" if absent)
#   named FILE ID      — ID itself when the listing holds it, "" otherwise
#   local-count FILE   — how many orchestrators this machine runs
#   one-local FILE     — the id of the one orchestrator this machine runs, ""
#                        unless exactly one is listed
ls_query() { # ls_query FILE MODE [ARGS…]
  python3 - "$@" <<'PY' 2>/dev/null || true
import json, sys
mode = sys.argv[2]
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
entries = [o for o in doc.get("orchestrators", []) if isinstance(o, dict)]
if mode == "field":
    for o in entries:
        if o.get("project") == sys.argv[3]:
            value = o.get(sys.argv[4])
            if value:
                print(value)
            break
elif mode == "named":
    for o in entries:
        if o.get("project") == sys.argv[3]:
            print(sys.argv[3])
            break
elif mode == "local-count":
    print(len([o for o in entries if o.get("kind") == "local"]))
elif mode == "one-local":
    local = [o.get("project") for o in entries if o.get("kind") == "local"]
    if len(local) == 1:
        print(local[0])
PY
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
  fact installation PASS "the local config in force is installation $name, it is not belong, and the agent this check hands over is that installation's own orchestrator node (orchestrator/<project>)"
}

step_version() {
  local v
  v=$(fg 30 fugaro version 2>/dev/null | head -n 1 || true)
  VERSION_LINE=${v:-unknown}
  fact version PASS "${v:-fugaro version printed nothing} (the build both of you run: the rule the handoff is held to is this binary's own, and so is the listing the two sessions read)"
}

# probe_paid NAME: what this build answers for a paid command name. 0 — the
# build carries the name and answers with its own help (the Subscription
# Edition); 1 — the build carries the core stub and refuses it by name; 2 —
# neither, so this build's edition cannot be read. The stub's sentence is D522's
# and only a build without the ee build tag renders it (internal/cli/extend.go).
probe_paid() { # probe_paid NAME
  local out
  out=$(fg 30 fugaro "$1" --help 2>&1 || true)
  case "$out" in
  *"Subscription Edition; this build does not include it"*) return 1 ;;
  *"Usage:"*) return 0 ;;
  esac
  return 2
}

# step_edition is the register's own precondition, first: the handoff's rule is
# the build's shared half (D1222, F14), so a core build has nothing to exercise
# and the run stops before it reads a node.
step_edition() {
  local name rc carried=no stubbed=no
  for name in coordinator report ab; do
    rc=0
    probe_paid "$name" || rc=$?
    case "$rc" in
    0) carried=yes ;;
    1) stubbed=yes ;;
    esac
  done
  if [ "$carried" = yes ]; then
    EDITION=ee
    fact edition PASS "this is the Subscription Edition build: it carries the paid commands this probe asked for, so the handoff's rule (ee/orchestrator.Transfer, installed into internal/orchestrator.SetHandoff by ee/register) is the one both sessions run"
    return 0
  fi
  if [ "$stubbed" = yes ]; then
    EDITION=core
    fact edition FAIL "this is a core build: it answers 'coordinator', 'report' and 'ab' with D522's stub — 'is part of Fugaro Subscription Edition; this build does not include it' — so it carries the core's handoff seam and not the rule behind it (internal/orchestrator/hooks.go's coreHandoffFuncs refuses every handoff)"
    stop "Check 304 needs the Subscription Edition build: the handoff is the build's shared half (ee/orchestrator.Transfer, installed by ee/register, D1222/F14), and a core build refuses every handoff rather than performing one. Install the Subscription Edition build on both machines and run this again. Nothing was read and nothing was written"
  fi
  fact edition FAIL "this build answered none of 'coordinator', 'report' and 'ab' with either its own help or D522's stub, so its edition cannot be read here and this check cannot tell whether it carries the handoff's rule"
  stop "the build's edition could not be read: Check 304 needs the Subscription Edition build, and this one answers neither the paid surface nor the stub. Nothing was read and nothing was written"
}

# step_surface asks the build for the command a person runs to hand an agent
# over. It is the gate the register's row assumes ("the `ee/` command O17
# builds") and the finding this release carries: O17 built the rule and the
# seam, and nothing in the tree calls HandoffAgent, so there is no verb for the
# two people to run. The probe reads the build's own help and never guesses at a
# spelling it did not find.
step_surface() {
  local help verb
  help=$(fg 30 fugaro orchestrator --help 2>&1 || true)
  case "$help" in
  *Usage:*) ;;
  *) fact handoff-command FAIL "fugaro orchestrator --help answered nothing this check can read as a command list, so the handoff verb cannot be found in this build"; stop "the build's orchestrator command list could not be read, so Check 304 cannot tell whether a handoff verb is there. Nothing was read and nothing was written" ;;
  esac
  verb=$(printf '%s\n' "$help" | sed -n 's/^[[:space:]]*\(handoff\|hand-off\|handover\|transfer\)[[:space:]].*/\1/p' | head -n 1)
  case "$verb" in
  handoff | hand-off | handover | transfer)
    HANDOFF_VERB=$verb
    fg 30 fugaro orchestrator "$verb" --help >"$SCRATCH/verb.help" 2>&1 || true
    fact handoff-command PASS "this build carries a handoff verb: 'fugaro orchestrator $verb'. Its own help is printed above — the two of you run that command, and this check never spells its flags for you"
    ;;
  *)
    fact handoff-command FAIL "this build carries no command that hands an agent over. 'fugaro orchestrator --help' lists none, and neither does the tree: O17 built the rule and the seam (ee/orchestrator.Transfer, installed by ee/register/orchestrator.go into internal/orchestrator.SetHandoff) and internal/orchestrator.HandoffAgent is called from nowhere in the tree, so a real installation has nothing for the two of you to run. The register's own row says 'the ee/ command O17 builds' and the plan's O17 file list carries no command file. Check 304 needs the release that gives the handoff a command; until then this check reads nothing and nobody should paste back a pass"
    stop "this build has no handoff command, so Check 304 has nothing for the two of you to run: the seam is built (internal/orchestrator.HandoffAgent) and no command calls it, and the register's row expects one from O17. It needs the release that gives the handoff a command. Nothing was read and nothing was written"
    ;;
  esac
}

# step_agent picks the agent the handoff moves: the id a person names, or the one
# this machine runs when the listing holds exactly one. `ls --json` is the
# project's own node listing, so the id it names is the node under orchestrator/
# — the register's "an agent handed from one developer to another".
step_agent() {
  if ! fg 60 fugaro orchestrator ls --json >"$SCRATCH/ls.json" 2>"$SCRATCH/ls.err"; then
    sed -n '1,5p' "$SCRATCH/ls.err" >&4 || true
    stop "fugaro orchestrator ls --json did not answer, so this run cannot name the agent to hand over. Run it yourself: it needs your own credentials and the installation's database"
  fi
  if [ "$AGENT_NAMED" = yes ]; then
    local known
    known=$(ls_query "$SCRATCH/ls.json" named "$AGENT")
    [ -n "$known" ] || stop "the listing names no orchestrator '$AGENT' in this installation, so there is no node to hand over. Run 'fugaro orchestrator ls' and name one of the ids it prints"
  else
    AGENT=$(ls_query "$SCRATCH/ls.json" named "$INSTALLATION")
    if [ -z "$AGENT" ]; then
      local localcount
      localcount=$(ls_query "$SCRATCH/ls.json" local-count)
      case "$localcount" in
      1) AGENT=$(ls_query "$SCRATCH/ls.json" one-local) ;;
      *)
        stop "this installation's listing names no orchestrator for '$INSTALLATION' and this machine runs ${localcount:-no} of them, so the check cannot tell which agent you mean. Name it: --agent <id>, one of the ids 'fugaro orchestrator ls' prints"
        ;;
      esac
    fi
  fi
  [ -n "$AGENT" ] || stop "the listing answered with no agent id, so this run cannot tell what it is handing over"
  AGENT=$(safe_field "$AGENT")
  guard_agent_id "the agent the listing answered with" "$AGENT"
  LEASE_BEFORE=$(ls_query "$SCRATCH/ls.json" field "$AGENT" lease_holder)
  LEASE_BEAT=$(ls_query "$SCRATCH/ls.json" field "$AGENT" updated_at)
  local beat=""
  if [ -n "$LEASE_BEAT" ]; then beat=", last beat $LEASE_BEAT on the database's own clock"; fi
  fact agent PASS "the agent is $AGENT, the node under orchestrator/ this installation holds; its lease holder right now is ${LEASE_BEFORE:-nobody}$beat"
}

# step_node1 reads the agent's node from the GIVING session before the handoff:
# the id, where the answer came from and the revision, which is what the second
# person's own session is held against afterwards.
step_node1() {
  if ! fg 60 fugaro orchestrator state --json --project "$AGENT" >"$SCRATCH/state.before.json" 2>"$SCRATCH/state.before.err"; then
    sed -n '1,5p' "$SCRATCH/state.before.err" >&4 || true
    stop "fugaro orchestrator state --json --project $AGENT did not answer from the giving session, so this check cannot read the node it is about to hand over. This session's own credentials are what reads it — fix that first"
  fi
  NODE_ID=$(json_get "$SCRATCH/state.before.json" 'doc.get("id", "")')
  NODE_SOURCE=$(json_get "$SCRATCH/state.before.json" 'doc.get("source", "")')
  NODE_REV=$(json_get "$SCRATCH/state.before.json" 'doc.get("rev")')
  NODE_BRIEF=$(safe_field "$(json_get "$SCRATCH/state.before.json" 'doc.get("brief", "")')" | cut -c1-90)
  [ "$NODE_ID" = "$AGENT" ] || stop "state --json answered for '${NODE_ID:-nothing}' where this check asked for '$AGENT': the giving session is not reading the node it means to hand over"
  fact node PASS "the giving session reads the node as $NODE_ID (source ${NODE_SOURCE:-unknown}, revision ${NODE_REV:-none}): '${NODE_BRIEF}'"
}

# write_handoff writes the record the second person runs against — mode 0600, in
# plain JSON, carrying the members the two of you typed and nothing else. No
# credential, no token and no node body is in it: it is what a person can paste
# back, written by python3's argv so no value is ever re-parsed by a shell.
write_handoff() {
  local at members
  at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  members=$(python3 -c 'import json,sys; print(json.dumps({"id": sys.argv[1], "source": sys.argv[2], "rev": sys.argv[3]}))' "$NODE_ID" "$NODE_SOURCE" "$NODE_REV")
  if ! python3 - "$HANDOFF" "$INSTALLATION" "$AGENT" "$FROM_MEMBER" "$TO_MEMBER" "$EDITION" "$VERSION_LINE" "$LEASE_BEFORE" "$members" "$HANDOFF_VERB" "$at" <<'PY'
import json, os, sys
path, installation, agent, frm, to, edition, version, lease, node, verb, at = sys.argv[1:12]
doc = {
    "version": 1,
    "check": "304",
    "at": at,
    "installation": installation,
    "agent": agent,
    "from": frm,
    "to": to,
    "edition": edition,
    "fugaro_version": version,
    "lease_holder_before": lease,
    "node_before": json.loads(node),
    "handoff_verb": verb,
}
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as fh:
    json.dump(doc, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY
  then
    fact handoff FAIL "the handoff record could not be written to $HANDOFF"
    stop "the handoff record could not be written to $HANDOFF: the second person has nothing to run against, so this phase is not finished. Check the path and its permissions (the file is written mode 0600) and run it again"
  fi
  chmod 600 "$HANDOFF" 2>/dev/null || true
}

# step_handoff writes the record and prints, for each person, what to do. The
# handoff itself is the build's own verb and this check never spells its flags:
# it prints the build's own help for it, so the syntax a person runs is the one
# their release documents rather than one this script guessed.
step_handoff() {
  local verb_help
  verb_help=$(sed -n '1,12p' "$SCRATCH/verb.help" 2>/dev/null || true)
  write_handoff
  owner ""
  owner "WHAT THE TWO OF YOU RUN. The handoff itself is this build's own command; its help, as this build prints it:"
  printf '%s\n' "$verb_help" | sed 's/^/  | /' >&4
  owner ""
  owner "FIRST PERSON (you, $FROM_MEMBER), on this machine: run the handoff verb above — hand agent $AGENT to $TO_MEMBER, as yourself."
  owner "  Then send the second person this file (mode 0600, it names the agent and the two of you, and no credential):"
  owner "    $HANDOFF"
  owner ""
  owner "SECOND PERSON, on THEIR machine, in THEIR OWN session:"
  owner "  bash scripts/live-checks/check304.sh second-person --installation $INSTALLATION --as $TO_MEMBER --handoff <the file above>${REPO:+ --repo $REPO}"
  owner "  and, so the lease says their own session holds the agent rather than nobody:"
  owner "    fugaro orchestrator run $AGENT      # in the foreground; stop it when the second phase is done"
  owner ""
  owner "THEN BOTH OF YOU, for the refusal half of the register's first reading (a credential this script does not hold, so it cannot make the call):"
  owner "  run the same handoff verb once as an owner or a project-scope admin — it lands — and once as a member who holds neither — it is refused."
  owner "  Paste both lines back with this block."
  fact handoff PASS "the record is written to $HANDOFF (mode 0600): agent $AGENT, from $FROM_MEMBER to $TO_MEMBER, edition $EDITION, version $VERSION_LINE, lease holder before ${LEASE_BEFORE:-nobody}. It is yours to keep until the result is pasted back, and the second person needs it — nothing of it is a credential"
}

# step_refusals: the half of the register's first reading no script can make.
# The rule is ee/orchestrator.Transfer's, asked of the caller's own resolved
# access, so a refusal needs a second person's credential in a second terminal.
step_refusals() {
  fact refusals INFO "not read by this script: whether the handoff is refused for anyone but an owner or a project-scope admin is decided by ee/orchestrator.Transfer over the CALLER's own grants (access.Allowed(ActionHandoffAgent), project scope), and one script holds one credential. The two of you read it, as the lines above print: the verb lands for an owner or a project-scope admin and is refused for anyone else. Paste both lines back with this block — the register's Check 304 asks for them, and this check will not claim a reading it did not make"
}

step_restore1() {
  fact restore PASS "this check wrote NOTHING on the installation: no node, no grant, no work item, no daemon, and no model call. The one file it wrote is $HANDOFF (mode 0600) on this machine, and the second person needs it — keep it until the result is pasted back, then remove it yourself. The scratch directory is removed at the end"
}

# ---------------------------------------------------------------------------
# The second person's phase. Every step below is a read with THEIR credentials:
# the record, the same node, and the lease the agent is held by.
step_record() {
  if [ ! -f "$HANDOFF" ]; then
    fact record FAIL "there is no handoff record at $HANDOFF"
    stop "Check 304's second phase has nothing to verify: $HANDOFF is not there. The first person runs 'first-person' and sends you the file it writes (it prints the path), and you run this phase with --handoff <that file>"
  fi
  local v
  v=$(json_get "$HANDOFF" 'doc.get("check", "")')
  [ "$v" = "304" ] || stop "$HANDOFF is not a Check 304 record (it says check='${v:-nothing}'). Use the file the first person's phase printed"
  v=$(json_get "$HANDOFF" 'doc.get("version", "")')
  [ "$v" = "1" ] || stop "$HANDOFF is a Check 304 record of version '${v:-nothing}' and this build reads version 1. Run both phases with the same build"
  v=$(json_get "$HANDOFF" 'doc.get("installation", "")')
  [ "$v" = "$INSTALLATION" ] || stop "the record is one handoff of installation '$v' and this phase was run for '$INSTALLATION': the two of you are looking at different installations"
  RECORD_TO=$(json_get "$HANDOFF" 'doc.get("to", "")')
  RECORD_FROM=$(json_get "$HANDOFF" 'doc.get("from", "")')
  RECORD_AGENT=$(json_get "$HANDOFF" 'doc.get("agent", "")')
  RECORD_LEASE=$(json_get "$HANDOFF" 'doc.get("lease_holder_before", "")')
  RECORD_NODE_ID=$(json_get "$HANDOFF" 'doc.get("node_before", {}).get("id", "")')
  RECORD_NODE_REV=$(json_get "$HANDOFF" 'doc.get("node_before", {}).get("rev", "")')
  case "$RECORD_TO" in
  "$AS_MEMBER") ;;
  *) stop "the record hands this agent to '$RECORD_TO' and you ran this phase as '$AS_MEMBER': the receiving person runs the second phase, and a handoff read by a third member proves nothing" ;;
  esac
  [ -n "$RECORD_AGENT" ] || stop "the record names no agent, so there is nothing to verify. Ask the first person to run their phase again with this build"
  AGENT=$(safe_field "$RECORD_AGENT")
  guard_agent_id "the agent the record names" "$AGENT"
  fact record PASS "the record is $HANDOFF: agent $AGENT, from $RECORD_FROM to $RECORD_TO, taken at $(json_get "$HANDOFF" 'doc.get("at", "")'), by $(json_get "$HANDOFF" 'doc.get("fugaro_version", "")'), and you are the receiving member ($AS_MEMBER)"
}

step_node2() {
  if ! fg 60 fugaro orchestrator state --json --project "$AGENT" >"$SCRATCH/state.after.json" 2>"$SCRATCH/state.after.err"; then
    sed -n '1,5p' "$SCRATCH/state.after.err" >&4 || true
    fact node FAIL "fugaro orchestrator state --json --project $AGENT did not answer from the receiving session"
    stop "the receiving session cannot read the node it has been handed: Check 304's second reading is that the receiving person holds the agent and can act as it, and this session's own credentials did not read it. Fix the access, then run this phase again"
  fi
  NODE_ID=$(json_get "$SCRATCH/state.after.json" 'doc.get("id", "")')
  NODE_SOURCE=$(json_get "$SCRATCH/state.after.json" 'doc.get("source", "")')
  NODE_REV=$(json_get "$SCRATCH/state.after.json" 'doc.get("rev")')
  NODE_BRIEF=$(safe_field "$(json_get "$SCRATCH/state.after.json" 'doc.get("brief", "")')" | cut -c1-90)
  if [ "$NODE_ID" != "$RECORD_NODE_ID" ] || [ "$NODE_ID" != "$AGENT" ]; then
    fact node FAIL "the receiving session read '${NODE_ID:-nothing}' where the record names '$RECORD_NODE_ID' and the check asked for '$AGENT'"
    stop "the two sessions are not looking at the same node, so nothing about this handoff is read: run both phases against the installation whose orchestrator you mean"
  fi
  fact node PASS "the receiving session reads the same node as the giving one did — $NODE_ID — one agent, two sessions: source ${NODE_SOURCE:-unknown}, revision ${NODE_REV:-none} now against ${RECORD_NODE_REV:-none} when the record was written. '${NODE_BRIEF}'"
}

# step_sessions is the register's second reading, and it is read off the lease
# node: one daemon per orchestrator id (O3, D1232), so the id's holder is who is
# driving it, and a session whose daemon cannot take the id is one that can no
# longer act as the agent (internal/orchestrator/lease.go). The holder is a
# daemon's run id and never a person's name: this release persists no owner for
# an agent (F28), and the fact says so rather than inventing one.
step_sessions() {
  if ! fg 60 fugaro orchestrator ls --json >"$SCRATCH/ls.after.json" 2>"$SCRATCH/ls.after.err"; then
    sed -n '1,5p' "$SCRATCH/ls.after.err" >&4 || true
    fact sessions FAIL "fugaro orchestrator ls --json did not answer from the receiving session"
    stop "the receiving session cannot read the installation's orchestrator listing, so which session holds the agent cannot be read. Fix the access and run this phase again"
  fi
  LEASE_AFTER=$(ls_query "$SCRATCH/ls.after.json" field "$AGENT" lease_holder)
  LEASE_BEAT=$(ls_query "$SCRATCH/ls.after.json" field "$AGENT" updated_at)
  if [ -z "$LEASE_AFTER" ]; then
    fact sessions FAIL "nobody holds $AGENT now: the lease names no holder, so the receiving session is not driving the agent and the register's second reading is not met — start your own daemon for a while (fugaro orchestrator run $AGENT) in this session and run this phase again"
    return 0
  fi
  if [ "$LEASE_AFTER" = "$RECORD_LEASE" ] && [ -n "$RECORD_LEASE" ]; then
    fact sessions FAIL "the lease still names the same holder as before the handoff ($LEASE_AFTER), so the GIVING session is still the one driving this agent: the handoff did not move it. Stop the giving session's daemon and start yours, then run this phase again"
    return 0
  fi
  local beat=""
  if [ -n "$LEASE_BEAT" ]; then beat=", last beat $LEASE_BEAT on the database's own clock"; fi
  fact sessions PASS "the lease names $LEASE_AFTER$beat where before the handoff it named ${RECORD_LEASE:-nobody}: the GIVING session no longer holds it and a second session does. The holder is a daemon's run id, not a person — this release persists no owner for an agent (F28) — so what the two readings say together is: the receiving session's daemon holds the id and the giving session's cannot take it (one daemon per orchestrator id, O3/D1232)"
}

# step_project_owned is the register's third reading. The live half of it is
# what the node does NOT carry — no owner, no member, no person identity (F28) —
# and the tree half is the registration the reading names: the core's own
# extension point, filled by ee/register, with no ee/ import in the core.
step_project_owned() {
  case "$REPO" in
  "") fact project-owned-agent INFO "not read from the tree: pass --repo <checkout> to read the extension-point half of this reading (that internal/orchestrator/hooks.go carries no ee/ import and that ee/register/orchestrator.go installs Transfer). The live half is the record's own node: the two sessions read it (FACT node), and an agent the project owns has no person identity because the node carries none — internal/orchestrator/state.go's agent_id and role, and the Owner of hooks.go is a value the handoff takes, not a field it persists (F28)" ;;
  *)
    if [ ! -f "$REPO/internal/orchestrator/hooks.go" ] || [ ! -f "$REPO/ee/register/orchestrator.go" ]; then
      fact project-owned-agent FAIL "--repo $REPO does not hold internal/orchestrator/hooks.go and ee/register/orchestrator.go, so the extension point this reading names is not there"
      return 0
    fi
    local leaked set
    leaked=$(grep -n 'dimipaun/fugaro/ee' "$REPO/internal/orchestrator/hooks.go" 2>/dev/null || true)
    set=$(grep -c 'orchestrator.SetHandoff(' "$REPO/ee/register/orchestrator.go" 2>/dev/null || true)
    if [ -n "$leaked" ]; then
      fact project-owned-agent FAIL "the core's extension point imports ee/ ($(safe_field "$leaked")), so the core does not build with ee/ deleted and this reading is not met"
      return 0
    fi
    if [ "${set:-0}" -lt 1 ]; then
      fact project-owned-agent FAIL "ee/register/orchestrator.go does not call orchestrator.SetHandoff, so the shared half this reading names is not registered the way the coordinator's CLI is"
      return 0
    fi
    fact project-owned-agent PASS "an agent the project owns has no person identity: the live half is the node both sessions read (no owner and no member on it — F28), and the registration half is read from $REPO — internal/orchestrator/hooks.go names no ee/ (the core builds and tests with ee/ deleted, internal/eeboundary's guard) and ee/register/orchestrator.go installs Transfer through the core's own SetHandoff, the way the coordinator's CLI is registered"
    ;;
  esac
}

step_refusals2() {
  fact refusals INFO "not read by this script: whether the handoff is refused for anyone but an owner or a project-scope admin is decided by ee/orchestrator.Transfer over the CALLER's own grants (access.Allowed(ActionHandoffAgent), project scope), and one script holds one credential. The two of you read it: run the build's handoff verb once as an owner or a project-scope admin (it lands) and once as a member who holds neither (it is refused), and paste both lines back with this block"
}

step_restore2() {
  fact restore PASS "nothing was created, granted, deleted or written: the record at $HANDOFF is the first person's copy and yours to remove once the result is pasted back. To hand the agent back: stop the daemon you started in this session and let the giving session start its own (fugaro orchestrator run $AGENT) — the lease is what says who holds it, and the id is left at revision ${NODE_REV:-unknown}. The project-owned agent is left exactly as it was: no owner was written, because this release persists none (F28)"
}

# ------------------------------------------------------------------- the plan
print_plan1() {
  say "dry run: NOTHING below was run, no node was read, no record was written and no daemon was started. A real first-person run would run, in this order:"
  say "  [local] the guards: you typed $INSTALLATION and no belong, and the phase is first-person"
  say "  [read] fugaro version; fugaro doctor --json — the installation you typed is held to doctor's project.name"
  say "  [read] fugaro coordinator --help (and report, ab) — the Subscription Edition gate: a core build stops here, spending nothing"
  say "  [read] fugaro orchestrator --help — the handoff verb: a build with none stops here (the finding this release carries: O17's seam has no command)"
  say "  [read] fugaro orchestrator ls --json — the agent, and the lease holder before the handoff"
  say "  [read] fugaro orchestrator state --json --project <agent> — the node from the giving session"
  say "  [yours] the handoff itself, run by you with the build's own verb: the check prints that verb's help and never spells its flags"
  say "  [writes, local] the handoff record at $HANDOFF (mode 0600) — the file the second person runs against"
  say "  [prints] the second person's own command line, and the two refusal readings no script can make"
  say "and its Restore would run:"
  say "  [yours] nothing on the installation was written; the record is yours to keep until the result is pasted back"
  say "  [local] the scratch directory is removed"
  say "Nothing was created, read, written or spent: this dry run touched no node, no database and no record."
}

print_plan2() {
  say "dry run: NOTHING below was run and no node was read. A real second-person run would run, in this order:"
  say "  [local] the guards: you typed $INSTALLATION and no belong, and the phase is second-person"
  say "  [read] fugaro version; fugaro doctor --json — the installation you typed is held to doctor's project.name"
  say "  [read] fugaro coordinator --help (and report, ab) — the Subscription Edition gate"
  say "  [read] fugaro orchestrator --help — the handoff verb gate"
  say "  [local] the handoff record at ${HANDOFF:-<--handoff>}: its check, its version, its installation, and that it hands the agent to YOU"
  say "  [read] fugaro orchestrator state --json --project <agent> — the same node, from your own session, against the revision the record carries"
  say "  [read] fugaro orchestrator ls --json — the lease: whether your session holds the agent and the giving session no longer does"
  say "  [read, optional] --repo <checkout> — the extension point (the core names no ee/, ee/register installs the rule)"
  say "and its Restore would run:"
  say "  [yours] nothing on the installation was written; the record is yours to remove once the result is pasted back, and you hand the agent back by stopping your daemon"
  say "  [local] the scratch directory is removed"
  say "Nothing was created, read, written or spent: this dry run touched no node and no database."
}

# ------------------------------------------------------------------- the block
BLOCK_KEYS_1=(phase installation version edition handoff-command agent node handoff refusals restore xtrace)
BLOCK_KEYS_2=(phase installation version edition handoff-command record node sessions project-owned-agent refusals restore xtrace)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed="" keys=("${BLOCK_KEYS_1[@]}")
  case "$PHASE" in second-person) keys=("${BLOCK_KEYS_2[@]}") ;; esac
  printf '\nCHECK 304 RESULT\n' >&3
  for key in "${keys[@]}"; do
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
  printf 'CHECK 304 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  owner "interrupted: printing what was read and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
main() {
  parse_args "$@"
  need_tools

  scratch_up
  guard_xtrace
  guard_belong "$INSTALLATION" typed

  if [ "$DRY_RUN" = yes ]; then
    case "$PHASE" in
    first-person) print_plan1 ;;
    second-person) print_plan2 ;;
    esac
    fact phase PASS "dry run: $PHASE — the guards and the plan only"
    fact dry-run PASS "guards and the plan only: no node was read, no record was written, no daemon was started and no model call was made"
    REACHED_END=yes
    BLOCK_KEYS_1=(phase installation dry-run xtrace)
    BLOCK_KEYS_2=(phase installation dry-run xtrace)
    return 0
  fi

  fact phase PASS "${PHASE}: the ${PHASE%%-*} of the two people Check 304 needs — nobody else's credentials are read here"
  step_installation
  step_version
  step_edition
  step_surface

  case "$PHASE" in
  first-person)
    step_agent
    step_node1
    step_handoff
    step_refusals
    step_restore1
    ;;
  second-person)
    step_record
    step_node2
    step_sessions
    step_project_owned
    step_refusals2
    step_restore2
    ;;
  esac

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
