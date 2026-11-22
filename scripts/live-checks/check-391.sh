#!/usr/bin/env bash
# Check 391 — three `--no-apply` changes, then ONE `fugaro apply`: the `policy`
# and `repository` stages run once each, as ONE script YOU run.
#
# It is AP10 of docs/composite-commands/implementation-plan.md, run by YOU on
# the sandbox installation after the release that ships the composite commands
# is installed. What it measures is the collapse the pending record exists for
# (D1562, D1570): `--no-apply` saves a change and stops (D1565), and the steps
# it owes are recorded ONE PER STEP NAME — so three changes that all owe the
# `policy` stage leave ONE entry for it, and the single `fugaro apply` that
# finishes them runs that stage once and not three times.
#
# What it reads, in this order:
#
#   1. The checkout, and the installation: the current directory is a git work
#      tree whose `origin` remote names the repository the `repository` stage is
#      for (D1574), and `fugaro doctor --json` names the project, its GCP
#      project and its region. Held to `--project` where you typed one, and an
#      installation named `belong` is refused.
#   2. The entry, before anything is written: `fugaro image ls --json` must not
#      already hold the name this check adds; a leftover of an earlier attempt
#      is removed first, so running this check twice works.
#   3. The two settings keys this check writes, and what they hold now:
#      `fugaro settings get <key> --json` says whether each is set, to what, and
#      whether the generic write path carries it. What is read here is what the
#      teardown puts back, and a key that cannot be read or cannot be written is
#      a reason not to write it at all.
#   4. **The three changes, and they are the fact this check is**: each written
#      with `--no-apply`, each answering its own "saved, not live yet" line with
#      the steps it owes. The first is `fugaro image add <entry> --dockerfile
#      <owner/name:path> --no-apply` (which owes the build, the redeploy and the
#      publication); the other two are `fugaro settings set <key> <value>
#      --layer local --no-apply` on two keys whose write owes the `policy` stage
#      alone. So all three owe `policy`, and one of them owes `repository` too.
#   5. What is owed: `fugaro apply --status --json`. It must name exactly
#      `catalog-build`, `repository` and `policy`, each ONCE — a second `policy`
#      row is the failure this check exists to catch.
#   6. **The one `fugaro apply`**: it must exit 0, and its own account must name
#      each of the three steps once ("applied catalog-build, repository,
#      policy").
#   7. The stages it really ran: the run's own stream, where every stage that
#      reached a result prints one line — internal/report's shape, a glyph, the
#      stage's name and its detail (`✔ policy published`). The `policy` line and
#      the `repository` line must appear exactly once each — the stage ran once,
#      and not once per change. The plan-view table (a mark, a state word and
#      then the name) is not read here: an applying run points that table at the
#      log, and what its own stream carries is these lines.
#   8. What is owed afterwards: `fugaro apply --status --json` again, which must
#      be nothing.
#   9. The teardown: `fugaro image rm <entry> --no-apply` and the two keys put
#      back the way they were found (the value they held, or unset), then ONE
#      `fugaro apply` to make those live, and the record read once more. This
#      check leaves the installation as it found it.
#
# **What a PASS means, and what it does not.** A PASS means the pending record
# collapses by step name and one `fugaro apply` runs each step once: the three
# changes were saved, the record named three steps, the single run answered
# them, and the record owes nothing. It does NOT mean the steps themselves did
# their work on the cloud — check 390 reads the `image add` chain's own effect
# — and it does not measure the failure path: a step left owed is check 392's
# subject.
#
# What a real run does to your installation: it adds a catalog entry (and, at
# the one `fugaro apply`, builds it, redeploys the repository's jobs and
# publishes the catalog), writes two of the machine's own settings, then
# removes the entry and puts the two keys back. Run it on the sandbox
# installation.
#
# Usage:      bash scripts/live-checks/check-391.sh --dockerfile <owner/name:path>
#                                                 [--entry <catalog entry>]
#                                                 [--project <fugaro project>]
#                                                 [--fugaro-flags '<flags>']
#                                                 [--deadline <seconds>] [--dry-run]
#             --dockerfile is the source of the entry the first change adds: a
#             Dockerfile in one of the project's repositories, as
#             `owner/name:path`. It is required, and it is the one flag a coding
#             agent's session may use of `image add` (D1573).
#             --entry is the name of the catalog entry this check owns (default
#             `chk391-catalog`). Give it a name nothing else uses.
#             --project is the Fugaro project the installation is, and
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on
#             whitespace, so a value with a space in it cannot be passed.
#             --deadline is how long the one `fugaro apply` may take, in seconds
#             (default 1800; CHECK391_DEADLINE does the same): it builds an
#             image, so it is minutes and not seconds.
#             --dry-run prints the plan and makes no call at all.
# Needs:      bash, git, mktemp, grep, sed, head, tail, cut, awk, python3, and a
#             `fugaro` on PATH with `doctor --json`, `image ls --json`,
#             `image add`, `image rm`, `settings get --json`, `settings set`,
#             `settings unset`, `apply` and `apply --status --json`. A missing
#             tool stops the run with one line naming it. No `gcloud` is needed:
#             every reading here is the CLI's own record.
# Where:      your own machine, in a checkout of a repository the project
#             onboards, against an installation whose project is NOT `belong`:
#             that name is refused before anything is run, in what you typed and
#             in what doctor answers. A coding agent's session is refused too.
# Costs:      one Cloud Build (at the one `fugaro apply`), two repository
#             redeploys (that apply's and the teardown apply's) and the policy
#             publications those two runs make. No model call and no run is
#             launched.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 391 RESULT.
#             Exit 0 when the claim held, 1 when it did not (a row says which),
#             and 2 when this run could not tell (a guard stopped it, a tool or
#             a document could not be read, an internal error ended it).
#
# CHECK391_FUGARO names the binary (default `fugaro`), CHECK391_DEADLINE the
# default deadline, CHECK391_DRY_RUN makes every run a dry one,
# CHECK391_KEEP_SCRATCH keeps the scratch directory (with the whole transcript
# of the one `fugaro apply`) instead of removing it, and CHECK391_KEY_A,
# CHECK391_VALUE_A, CHECK391_KEY_B and CHECK391_VALUE_B replace the two
# settings this check writes.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all.
set -E

# No xtrace, whatever the shell it was started from had: this run writes two of
# the machine's own settings and the project's catalog.
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
USAGE="check-391.sh --dockerfile <owner/name:path> [--entry <catalog entry>] [--project <fugaro project>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
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
need() { command -v "$1" >/dev/null 2>&1 || die "Check 391 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 391 needs bash"
  need mktemp
  need grep
  need sed
  need head
  need tail
  need cut
  need awk "the rows of the run's own transcript are joined into one line with it"
  need python3 "the JSON documents this check reads are parsed with it"
  need git "the checkout's origin remote names the repository whose redeploy is owed"
  need "$FUGARO_BIN" "the three changes and the one apply are the commands this check measures"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk391-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK391_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk391.XXXXXXXX") || die "Check 391 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 391 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
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
    say "CHECK391_KEEP_SCRATCH is set: $SCRATCH is kept (the one apply's transcript is in apply.out and apply.err)"
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

# guard_agent_session: this check is the owner's, and the commands it runs write
# the project's catalog and the machine's own settings. The loop is the five
# markers that say an agent, and not a person, is driving this shell:
# agentenv.AgentMarkers. CLAUDE_CODE_SSE_PORT is not one of them (it says this
# shell sits in an editor's terminal) and is dropped from this script's
# environment instead.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 391 writes a real catalog entry and two real settings, and it is yours to run at your own terminal, on the sandbox installation"
    stop "${marker} is set, so this is a coding agent's session: Check 391 is yours to run at your own terminal, on the sandbox installation"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused: it says this shell sits in an editor's terminal, not that an agent is driving it"
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
    fact belong FAIL "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write the owner's own catalog and the owner's own settings"
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write the owner's own catalog and the owner's own settings"
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


def entry(path, name):
    """The catalog entry `name` as `fugaro image ls --json` holds it: whether it
    is there at all. This check never reads the entry's digest — check 390 is
    the one that does — only whether the name it owns is free."""
    doc = load(path)
    rows = (doc or {}).get("entries") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        print("read=no")
        return
    print("read=yes")
    print("count=%d" % len(rows))
    for r in rows:
        if isinstance(r, dict) and r.get("name") == name:
            print("present=yes")
            return
    print("present=no")


def setting(path, key):
    """The row `fugaro settings get <key> --json` answers for one key: whether
    the generic write path carries it at all (`writable`), what decides it now
    (`wins`), and the value that decides it. An absent `value` is a key nothing
    sets — the row's own default is what answers — which is a reading of its own
    and not an empty string."""
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
# command's stderr sees.
ANSI = re.compile(r"\x1b\[[0-9;]*m")
STEP_GLYPHS = ("✔", "+", "–", "-", "✖", "x", "!")
STEP_ROW = re.compile(r"^(?:%s) +([^\s:]+)(?:$|[ \t:])"
                      % "|".join(re.escape(g) for g in STEP_GLYPHS))


def transcript(path):
    """The stages of a run's own transcript: one `row=<name> count=<n>` line per
    stage it reported a result for, and the `applied <names>` line the command
    ends on. The count is the whole reading: a stage the loop reached once
    prints one line, and a record that queued the same step per change would
    reach it more than once."""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
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
    elif cmd == "entry":
        entry(sys.argv[2], sys.argv[3])
    elif cmd == "setting":
        setting(sys.argv[2], sys.argv[3])
    elif cmd == "pending":
        pending(sys.argv[2])
    elif cmd == "transcript":
        transcript(sys.argv[2])
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

# ---------------------------------------------------------------- the checkout
# The repository stage is per repository (D1574): the redeploy the entry's
# change owes is the checkout's own, so this check has to know which repository
# it stands in.
judge_checkout() {
  local top rc=0 url
  top=$(git rev-parse --show-toplevel 2>/dev/null) || rc=$?
  if [ "$rc" != 0 ] || [ -z "$top" ]; then
    stop "this directory is not a git work tree: the redeploy the changes owe is the checkout's own repository (D1574), so run this check in a checkout of a repository the project onboards"
  fi
  url=$(git -C "$top" remote get-url origin 2>/dev/null) || rc=$?
  if [ "$rc" != 0 ] || [ -z "$url" ]; then
    stop "$top has no 'origin' remote, so this check cannot name the repository whose redeploy is owed"
  fi
  REPO_NAME=$(repo_of "$url") || stop "the origin remote of $top ($url) does not name a repository this check can read as owner/name"
  CHECKOUT=$top
  fact checkout PASS "$CHECKOUT is a git work tree and its origin remote names ${REPO_NAME}: the 'repository' step of the entry's change is that repository's, and it is the one stage here that is per repository (D1574)"
}

# repo_of URL prints `owner/name` out of an origin remote, in the shapes git
# prints them. A remote that is a path on this machine names no repository, so it
# is a refusal (exit 1) rather than a made-up owner/name.
repo_of() { # repo_of <url>
  python3 - "$1" <<'PY'
import re, sys
u = sys.argv[1].strip()
u = re.sub(r"\.git$", "", u)
u = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", u)
if u.startswith(("/", "./", "../", "~")):
    sys.exit(1)
head = u.split("/", 1)[0]
if "@" in head:
    u = u.split("@", 1)[1]
u = re.sub(r"^[^/:]+:", "", u)
parts = [p for p in re.split(r"[/:]", u) if p]
if len(parts) < 2:
    sys.exit(1)
print("%s/%s" % (parts[-2], parts[-1]))
PY
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
  fact installation PASS "the installation is project $PROJECT_NAME on GCP project $PROJECT_GCP in region $PROJECT_REGION (fugaro doctor --json): the entry and the two settings below are written there, and the record the one apply reads is that installation's"
}

# ---------------------------------------------------------------- the entry
# image_ls writes the catalog to $SCRATCH/entries.json and the entry's row to
# $SCRATCH/entry.txt.
image_ls() {
  local flags=()
  flags_into flags
  fugaro_call entries image ls --json ${flags[@]+"${flags[@]}"}
  IMAGE_LS_STATUS=$FUGARO_STATUS
  read_json "$SCRATCH/entry.txt" entry "$SCRATCH/entries.out" "$ENTRY"
}

step_entry() {
  image_ls
  if [ "$(field "$SCRATCH/entry.txt" read)" != yes ]; then
    unmeasured entry "fugaro image ls --json did not answer a catalog (exit $IMAGE_LS_STATUS: $(last_line "$SCRATCH/entries.err")), so this check cannot tell whether the entry ${ENTRY} is already there -- and an entry this check cannot read is one it must not add. The catalog is the project's own document; fix what the line names and run the check again"
    SKIP_REST=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/entry.txt" present)" != yes ]; then
    fact entry PASS "the project's catalog holds no entry named ${ENTRY} ($(field "$SCRATCH/entry.txt" count) entr(ies) read): the first change below is a new entry, and the teardown removes exactly it"
    return 0
  fi
  # A leftover of an earlier attempt: this check owns the name it adds, so it
  # takes its own leftover away rather than refusing to run again.
  say "the entry ${ENTRY} is already in the catalog: removing it first, because this check owns that name (a leftover of an earlier attempt)"
  local flags=()
  flags_into flags
  fugaro_call rm-leftover image rm "$ENTRY" ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    unmeasured entry "the catalog already holds an entry named ${ENTRY}, which this check adds and removes, and removing that leftover failed (exit $FUGARO_STATUS: $(last_line "$SCRATCH/rm-leftover.err")). Remove it yourself (fugaro image rm ${ENTRY}) or pass --entry with a name nothing else uses, and run the check again"
    SKIP_REST=yes
    return 0
  fi
  fact entry PASS "the catalog already held an entry named ${ENTRY} (a leftover of an earlier attempt) and this check removed it with its own fugaro image rm: the first change below is a new entry again, and running this check twice in a row works"
}

# ------------------------------------------------------------- the two settings
# read_setting KEY DEST: what `fugaro settings get <key> --json` answers, into
# DEST, with the call's status in FUGARO_STATUS.
read_setting() { # read_setting KEY DEST
  local flags=()
  flags_into flags
  fugaro_call "get-$1" settings get "$1" --json ${flags[@]+"${flags[@]}"}
  read_json "$2" setting "$SCRATCH/get-$1.out" "$1"
}

step_keys() {
  read_setting "$KEY_A" "$SCRATCH/key-a.txt"
  local status_a=$FUGARO_STATUS
  read_setting "$KEY_B" "$SCRATCH/key-b.txt"
  local status_b=$FUGARO_STATUS
  if [ "$(field "$SCRATCH/key-a.txt" read)" != yes ]; then
    unmeasured keys "fugaro settings get ${KEY_A} --json answered no row for that key (exit $status_a: $(last_line "$SCRATCH/get-${KEY_A}.err")). This check writes the key and puts it back, so a key it cannot read is one it must not write: settle it (or pass CHECK391_KEY_A with a key of your own) and run the check again"
    SKIP_REST=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/key-b.txt" read)" != yes ]; then
    unmeasured keys "fugaro settings get ${KEY_B} --json answered no row for that key (exit $status_b: $(last_line "$SCRATCH/get-${KEY_B}.err")). This check writes the key and puts it back, so a key it cannot read is one it must not write: settle it (or pass CHECK391_KEY_B with a key of your own) and run the check again"
    SKIP_REST=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/key-a.txt" writable)" != yes ] || [ "$(field "$SCRATCH/key-b.txt" writable)" != yes ]; then
    local which=$KEY_A
    [ "$(field "$SCRATCH/key-a.txt" writable)" = yes ] && which=$KEY_B
    unmeasured keys "the generic write path does not carry ${which} (fugaro settings get --json answers writable=no): this check writes both keys with 'fugaro settings set', so a key the write refuses is one it cannot use. Pass CHECK391_KEY_A/CHECK391_KEY_B with keys the write carries"
    SKIP_REST=yes
    return 0
  fi
  KEY_A_WINS=$(field "$SCRATCH/key-a.txt" wins)
  KEY_A_SET=$(field "$SCRATCH/key-a.txt" set)
  KEY_A_OLD=$(field "$SCRATCH/key-a.txt" value)
  KEY_B_WINS=$(field "$SCRATCH/key-b.txt" wins)
  KEY_B_SET=$(field "$SCRATCH/key-b.txt" set)
  KEY_B_OLD=$(field "$SCRATCH/key-b.txt" value)
  fact keys PASS "${KEY_A} (${KEY_A_WINS}, $( [ "$KEY_A_SET" = yes ] && printf 'set to %s' "$KEY_A_OLD" || printf 'unset')) and ${KEY_B} (${KEY_B_WINS}, $( [ "$KEY_B_SET" = yes ] && printf 'set to %s' "$KEY_B_OLD" || printf 'unset')) are both carried by the generic write path: this check writes them to ${VALUE_A} and ${VALUE_B} at the local layer and puts them back at the end (a key that was unset is unset again)"
}

# ------------------------------------------------------------ the three changes
# saved_line KEY: the "saved, not live yet" line of a change, whichever of the
# two doors printed it. The two management doors spell the sentence with two
# different tails ("owed" and "still owed"), so this reads the names between the
# colon and the tail rather than pinning either wording.
saved_names() { # saved_names FILE
  grep -m1 '^saved, not live yet: ' "$1" 2>/dev/null |
    sed -e 's/^saved, not live yet: //' -e 's/ still owed$//' -e 's/ owed$//' || true
}

step_saved() {
  local flags=() names line
  flags_into flags
  SAVED_OK=no

  say "change 1 of 3: ${FUGARO_BIN} image add ${ENTRY} --dockerfile ${DOCKERFILE} --no-apply (saved, not live)"
  run_bounded "$DEADLINE" save-add "$FUGARO_BIN" image add "$ENTRY" --dockerfile "$DOCKERFILE" --no-apply ${flags[@]+"${flags[@]}"}
  SAVE_ADD_STATUS=$RUN_STATUS
  SAVE_ADD_TIMED_OUT=$RUN_TIMED_OUT
  if [ "$SAVE_ADD_TIMED_OUT" = yes ]; then
    fact saved FAIL "the first change (fugaro image add --no-apply) was still running after ${DEADLINE}s and this check killed it. A --no-apply change runs no step, so this is a command that did not answer; its last line: $(last_line "$SCRATCH/save-add.err")"
    return 0
  fi
  if [ "$SAVE_ADD_STATUS" != 0 ]; then
    fact saved FAIL "the first change (fugaro image add ${ENTRY} --dockerfile ${DOCKERFILE} --no-apply) exited ${SAVE_ADD_STATUS}, so nothing was saved and there is nothing to apply: $(last_line "$SCRATCH/save-add.err")"
    return 0
  fi
  ENTRY_ADDED=yes
  line=$(saved_names "$SCRATCH/save-add.out")
  if [ -z "$line" ]; then
    fact saved FAIL "fugaro image add --no-apply exited 0 without its own 'saved, not live yet' line, so this check cannot read the steps the change owes: $(last_line "$SCRATCH/save-add.out"). The whole transcript is in $SCRATCH/save-add.out (set CHECK391_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  case "$line" in
  *catalog-build*repository*policy* | *catalog-build*policy*repository*) ;;
  *)
    fact saved FAIL "the entry's change says it owes '$line', and the steps an image add owes are the build, the repository's redeploy and the publication (catalog-build, repository, policy: apply.Order, D1577). The whole line is in $SCRATCH/save-add.out"
    return 0
    ;;
  esac

  say "change 2 of 3: ${FUGARO_BIN} settings set ${KEY_A} ${VALUE_A} --layer local --no-apply"
  fugaro_call save-a settings set "$KEY_A" "$VALUE_A" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    fact saved FAIL "the second change (fugaro settings set ${KEY_A} ${VALUE_A} --layer local --no-apply) exited $FUGARO_STATUS, so the three changes this check needs are not all saved: $(last_line "$SCRATCH/save-a.err")"
    return 0
  fi
  KEY_A_WRITTEN=yes
  line=$(saved_names "$SCRATCH/save-a.out")
  case "$line" in
  *policy*) ;;
  *)
    fact saved FAIL "the second change says it owes '${line:-<nothing>}', and a write of ${KEY_A} owes the policy stage alone (the machine's own config is what the published node is derived from, D2615). The whole line is in $SCRATCH/save-a.out"
    return 0
    ;;
  esac

  say "change 3 of 3: ${FUGARO_BIN} settings set ${KEY_B} ${VALUE_B} --layer local --no-apply"
  fugaro_call save-b settings set "$KEY_B" "$VALUE_B" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    fact saved FAIL "the third change (fugaro settings set ${KEY_B} ${VALUE_B} --layer local --no-apply) exited $FUGARO_STATUS, so the three changes this check needs are not all saved: $(last_line "$SCRATCH/save-b.err")"
    return 0
  fi
  KEY_B_WRITTEN=yes
  line=$(saved_names "$SCRATCH/save-b.out")
  case "$line" in
  *policy*) ;;
  *)
    fact saved FAIL "the third change says it owes '${line:-<nothing>}', and a write of ${KEY_B} owes the policy stage alone. The whole line is in $SCRATCH/save-b.out"
    return 0
    ;;
  esac

  fact saved PASS "three changes are saved and not live: the catalog entry ${ENTRY} (owing catalog-build, repository and policy), ${KEY_A} at the local layer and ${KEY_B} at the local layer (each owing policy). Every one of them exited 0 without running a step, which is what --no-apply is (D1565)"
  SAVED_OK=yes
}

# ---------------------------------------------------------- what is owed
# read_pending DEST: `fugaro apply --status --json`, its document read into DEST.
read_pending() { # read_pending DEST
  local flags=()
  flags_into flags
  fugaro_call status apply --status --json ${flags[@]+"${flags[@]}"}
  PENDING_STATUS=$FUGARO_STATUS
  read_json "$1" pending "$SCRATCH/status.out"
}

step_status() {
  read_pending "$SCRATCH/pending-before.txt"
  if [ "$(field "$SCRATCH/pending-before.txt" read)" != yes ]; then
    unmeasured status "fugaro apply --status --json did not answer its document (exit $PENDING_STATUS: $(last_line "$SCRATCH/status.err")), so what the three changes owe could not be read"
    return 0
  fi
  OWED_BEFORE=$(field "$SCRATCH/pending-before.txt" owed)
  local steps
  steps=$(sed -n 's/^step=\([^ ]*\).*/\1/p' "$SCRATCH/pending-before.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
  local c_build c_repo c_policy
  c_build=$(sed -n 's/^count=catalog-build n=//p' "$SCRATCH/pending-before.txt" | head -n 1)
  c_repo=$(sed -n 's/^count=repository n=//p' "$SCRATCH/pending-before.txt" | head -n 1)
  c_policy=$(sed -n 's/^count=policy n=//p' "$SCRATCH/pending-before.txt" | head -n 1)
  if [ "${OWED_BEFORE:-0}" != 3 ] || [ "${c_build:-0}" != 1 ] || [ "${c_repo:-0}" != 1 ] || [ "${c_policy:-0}" != 1 ]; then
    fact status FAIL "the three changes owe ${OWED_BEFORE} step(s): ${steps} (policy recorded ${c_policy:-0} time(s), repository ${c_repo:-0}, catalog-build ${c_build:-0}). The record keeps ONE entry per step name (D1562, D1570), so three changes that all owe the policy stage leave exactly one policy entry and this run must find three steps: catalog-build, repository and policy, each once"
    return 0
  fi
  fact status PASS "the three changes owe exactly three steps and each of them once: ${steps} (fugaro apply --status --json, recorded=$(field "$SCRATCH/pending-before.txt" recorded)). The two settings writes and the entry all owe the policy stage, and the record collapsed them into one entry per step name (D1562, D1570) rather than one per change"
}

# ------------------------------------------------------------ the one apply
step_applied() {
  local flags=()
  flags_into flags
  say "the one apply: ${FUGARO_BIN} apply${PROJECT:+ --project $PROJECT} (deadline ${DEADLINE}s; this BUILDS the catalog image, redeploys the repository's jobs and publishes the catalog)"
  run_bounded "$DEADLINE" apply "$FUGARO_BIN" apply ${flags[@]+"${flags[@]}"}
  APPLY_STATUS=$RUN_STATUS
  APPLY_TIMED_OUT=$RUN_TIMED_OUT
  APPLY_SECONDS=$RUN_SECONDS
  if [ "$APPLY_TIMED_OUT" = yes ]; then
    fact applied FAIL "the one fugaro apply was still running after ${DEADLINE}s and this check killed it: the steps it had not finished are still owed, and 'fugaro apply' run again finishes them. Its last line: $(last_line "$SCRATCH/apply.err")"
    return 0
  fi
  if [ "$APPLY_STATUS" != 0 ]; then
    fact applied FAIL "the one fugaro apply ended after ${APPLY_SECONDS}s with exit ${APPLY_STATUS}: $(last_line "$SCRATCH/apply.err"). The steps it could not run are still owed; the whole transcript is in $SCRATCH/apply.out and apply.err (set CHECK391_KEEP_SCRATCH=1 to keep the directory)"
    return 0
  fi
  # The command's own answer is on stdout (`applied <names>`); every stage's own
  # result line is on stderr, because the command points itself there for the
  # length of the run so that a stage's own output can never land on the answer.
  # The line is internal/report's: a glyph, the stage's name, then its detail.
  read_json "$SCRATCH/apply-rows.txt" transcript "$SCRATCH/apply.err"
  if [ "$(field "$SCRATCH/apply-rows.txt" read)" != yes ]; then
    unmeasured applied "the one fugaro apply's own transcript could not be read ($SCRATCH/apply.err): its exit status was 0, but what it ran cannot be read"
    return 0
  fi
  local said
  said=$(grep -m1 '^applied ' "$SCRATCH/apply.out" 2>/dev/null | sed 's/^applied //' | cut -c1-300 || true)
  if [ -z "$said" ]; then
    fact applied FAIL "the one fugaro apply exited 0 after ${APPLY_SECONDS}s without its own 'applied' line, so this check cannot read the steps it answered: $(last_line "$SCRATCH/apply.out"). The whole transcript is in $SCRATCH/apply.out"
    return 0
  fi
  APPLIED_NAMES=$said
  local c_build c_repo c_policy
  c_build=$(printf '%s\n' "$said" | tr ',' '\n' | sed 's/^ *//; s/ *$//' | grep -c '^catalog-build$' || true)
  c_repo=$(printf '%s\n' "$said" | tr ',' '\n' | sed 's/^ *//; s/ *$//' | grep -c '^repository$' || true)
  c_policy=$(printf '%s\n' "$said" | tr ',' '\n' | sed 's/^ *//; s/ *$//' | grep -c '^policy$' || true)
  APPLIED_BUILD=${c_build:-0}
  APPLIED_REPO=${c_repo:-0}
  APPLIED_POLICY=${c_policy:-0}
  local total
  total=$(printf '%s\n' "$said" | tr ',' '\n' | sed 's/^ *//; s/ *$//' | grep -c . || true)
  if [ "${APPLIED_BUILD:-0}" != 1 ] || [ "${APPLIED_REPO:-0}" != 1 ] || [ "${APPLIED_POLICY:-0}" != 1 ] || [ "${total:-0}" != 3 ]; then
    fact applied FAIL "the one fugaro apply exited 0 after ${APPLY_SECONDS}s and answered '${said}', where the record owed catalog-build, repository and policy once each. A step answered twice or a step that is not the record's is the finding"
    return 0
  fi
  fact applied PASS "ONE fugaro apply ended after ${APPLY_SECONDS}s with exit 0 and answered exactly the three steps the record owed, each once: ${said} (D1561, D1563)"
}

step_stages() {
  if [ -z "${APPLIED_NAMES:-}" ]; then
    unmeasured stages "the one apply's own account was not read (the applied row says why), so the stage lines it printed cannot be held to anything"
    return 0
  fi
  local c_policy c_repo rows
  c_policy=$(sed -n 's/^row=policy count=//p' "$SCRATCH/apply-rows.txt" | head -n 1)
  c_repo=$(sed -n 's/^row=repository count=//p' "$SCRATCH/apply-rows.txt" | head -n 1)
  rows=$(sed -n 's/^row=\(.*\) count=\(.*\)$/\1 x\2/p' "$SCRATCH/apply-rows.txt" | awk 'NF { printf "%s%s", (n++ ? "; " : ""), $0 }')
  STAGE_POLICY=${c_policy:-0}
  STAGE_REPO=${c_repo:-0}
  if [ "${STAGE_POLICY:-0}" != 1 ] || [ "${STAGE_REPO:-0}" != 1 ]; then
    fact stages FAIL "the one apply printed the policy stage's own line ${STAGE_POLICY:-0} time(s) and the repository stage's ${STAGE_REPO:-0} time(s), and each of them must be exactly one: the record keeps one entry per step name (D1562), so the loop reaches each stage once whatever number of changes owed it. The stages it reported: ${rows:-<none>}"
    return 0
  fi
  fact stages PASS "the one apply printed one line for the policy stage and one for the repository stage (${rows:-<none>}): three changes that all owed policy reached that stage ONCE, which is the collapse the pending record exists for (D1562, D1570)"
}

step_owed() {
  read_pending "$SCRATCH/pending-after.txt"
  if [ "$(field "$SCRATCH/pending-after.txt" read)" != yes ]; then
    unmeasured owed "fugaro apply --status --json did not answer its document after the one apply (exit $PENDING_STATUS: $(last_line "$SCRATCH/status.err")), so what is still owed could not be read"
    return 0
  fi
  OWED_AFTER=$(field "$SCRATCH/pending-after.txt" owed)
  if [ "${OWED_AFTER:-0}" != 0 ]; then
    local steps
    steps=$(sed -n 's/^step=\([^ ]*\).*/\1/p' "$SCRATCH/pending-after.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    fact owed FAIL "${OWED_AFTER} step(s) are still owed after the one apply: ${steps}. The one apply answered the three steps it read, and a record that still owes one is a run that did fewer steps than it was asked for"
    return 0
  fi
  fact owed PASS "fugaro apply --status --json owes nothing after the one apply: the three changes are live and the record is clear (D1561, D1563)"
}

# restore_key KEY WRITTEN WINS SET OLD DEST: put one setting back the way this
# check found it -- the value it held at the local layer when that is what
# decided it, and nothing at all when nothing set it -- and answer non-zero (with
# the teardown row recorded) when the write refused. A key this check never
# wrote is left alone: there is nothing to put back.
restore_key() { # restore_key KEY WRITTEN WINS SET OLD DEST
  local key=$1 written=$2 wins=$3 set=$4 old=$5 dest=$6
  [ "$written" = yes ] || return 0
  local flags=() value=""
  flags_into flags
  if [ "$wins" = local ] && [ "$set" = yes ]; then value=$old; fi
  if [ -n "$value" ]; then
    fugaro_call "$dest" settings set "$key" "$value" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  else
    fugaro_call "$dest" settings unset "$key" --layer local --no-apply ${flags[@]+"${flags[@]}"}
  fi
  if [ "$FUGARO_STATUS" != 0 ]; then
    fact teardown FAIL "the setting ${key} could not be put back (exit $FUGARO_STATUS: $(last_line "$SCRATCH/$dest.err")): this machine's config still carries the value this check wrote. Put it back yourself with 'fugaro settings set ${key} <value> --layer local' or 'fugaro settings unset ${key} --layer local', then run 'fugaro apply'"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------------- teardown
step_teardown() {
  local flags=() wrote=no
  TEARDOWN_OK=no
  flags_into flags
  say "teardown: undoing what this check wrote"
  if [ "${ENTRY_ADDED:-}" = yes ]; then
    fugaro_call undo-add image rm "$ENTRY" --no-apply ${flags[@]+"${flags[@]}"}
    if [ "$FUGARO_STATUS" != 0 ]; then
      fact teardown FAIL "the entry this check added could not be removed (fugaro image rm ${ENTRY} --no-apply exited $FUGARO_STATUS: $(last_line "$SCRATCH/undo-add.err")): it is STILL in the project's catalog. Remove it yourself with 'fugaro image rm ${ENTRY}' and run 'fugaro apply'"
      return 0
    fi
    wrote=yes
  fi
  local rc=0
  restore_key "$KEY_A" "${KEY_A_WRITTEN:-}" "${KEY_A_WINS:-}" "${KEY_A_SET:-}" "$KEY_A_OLD" undo-a || rc=$?
  if [ "$rc" = 0 ] && [ "${KEY_A_WRITTEN:-}" = yes ]; then wrote=yes; fi
  if [ "$rc" != 0 ]; then return 0; fi
  restore_key "$KEY_B" "${KEY_B_WRITTEN:-}" "${KEY_B_WINS:-}" "${KEY_B_SET:-}" "$KEY_B_OLD" undo-b || rc=$?
  if [ "$rc" = 0 ] && [ "${KEY_B_WRITTEN:-}" = yes ]; then wrote=yes; fi
  if [ "$rc" != 0 ]; then return 0; fi
  if [ "$wrote" = yes ]; then
    say "teardown: one ${FUGARO_BIN} apply to make the undoing live"
    run_bounded "$DEADLINE" undo-apply "$FUGARO_BIN" apply ${flags[@]+"${flags[@]}"}
    TEARDOWN_STATUS=$RUN_STATUS
    TEARDOWN_TIMED_OUT=$RUN_TIMED_OUT
    if [ "$TEARDOWN_TIMED_OUT" = yes ] || [ "$TEARDOWN_STATUS" != 0 ]; then
      fact teardown FAIL "the teardown's own fugaro apply exited ${TEARDOWN_STATUS} (timed out: ${TEARDOWN_TIMED_OUT}): $(last_line "$SCRATCH/undo-apply.err"). The undoing is saved and its steps are still owed -- run 'fugaro apply' yourself and check 'fugaro apply --status' owes nothing"
      return 0
    fi
  else
    TEARDOWN_STATUS=0
    TEARDOWN_TIMED_OUT=no
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
  fact teardown PASS "the entry this check added is out of the project's catalog again and both settings are back the way this check found them (${KEY_A}: $( [ "${KEY_A_SET:-}" = yes ] && printf 'its own value' || printf 'unset again'); ${KEY_B}: $( [ "${KEY_B_SET:-}" = yes ] && printf 'its own value' || printf 'unset again')), with one fugaro apply making that live and the record owing nothing: the installation is as this check found it, and the scratch directory is removed at the end"
  TEARDOWN_OK=yes
}

# ------------------------------------------------------------------- the verdict
the_verdict() {
  if [ "${SAVE_ADD_TIMED_OUT:-no}" = yes ] || [ "${SAVE_ADD_STATUS:-0}" != 0 ] || [ "${SAVED_OK:-}" = no ]; then
    fact claim FAIL "the claim does NOT hold as measured: the three --no-apply changes were not all saved (the saved row says how it ended), so there was nothing for one fugaro apply to collapse"
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
  if [ "${APPLY_TIMED_OUT:-no}" = yes ] || [ "${APPLY_STATUS:-0}" != 0 ]; then
    fact claim FAIL "the claim does NOT hold as measured: the one fugaro apply did not finish the three steps (the applied row says how it ended)"
    REACHED_END=yes
    exit 1
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
    fact claim INFO "the claim is NOT MEASURED by this run: the row(s) ${UNMEASURED_KEYS// /, } above could not be read, so nothing here is an answer about whether three --no-apply changes collapse into one run of each stage. Settle what the row names and run the check again"
    REACHED_END=yes
    exit 2
  fi
  fact claim PASS "three --no-apply changes left three steps owed, one entry per step name, and ONE fugaro apply ran the policy stage once and the repository stage once: the record collapses by step (D1562, D1570), one command finishes the job (D1561, D1565), and the installation is back as this check found it"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL -- not reached. That is what makes the block safe to diff between runs.
BLOCK_KEYS=(xtrace agent-session checkout installation belong entry keys saved status applied stages owed teardown claim)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 391 RESULT\n' >&3
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
  printf 'CHECK 391 RESULT END (exit %s)\n' "$status" >&3
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
  say "  [reads] git rev-parse --show-toplevel and the checkout's origin remote: the repository whose redeploy the entry's change owes (D1574)"
  say "  [reads] ${FUGARO_BIN} doctor --json${PROJECT:+ --project $PROJECT}: the project, its GCP project and its region, with a project named belong refused here and in what doctor answers"
  say "  [reads] ${FUGARO_BIN} image ls --json: whether the catalog already holds ${ENTRY} (a leftover is removed first, so running this check twice works)"
  say "  [reads] ${FUGARO_BIN} settings get ${KEY_A} --json and ${KEY_B} --json: what each holds now and whether the generic write path carries it -- this is what the teardown puts back"
  say "  [writes] ${FUGARO_BIN} image add ${ENTRY} --dockerfile ${DOCKERFILE} --no-apply: change 1 of 3, saved and not live (it owes catalog-build, repository and policy)"
  say "  [writes] ${FUGARO_BIN} settings set ${KEY_A} ${VALUE_A} --layer local --no-apply: change 2 of 3, saved and not live (it owes policy)"
  say "  [writes] ${FUGARO_BIN} settings set ${KEY_B} ${VALUE_B} --layer local --no-apply: change 3 of 3, saved and not live (it owes policy)"
  say "  [reads] ${FUGARO_BIN} apply --status --json: the steps the three changes owe, which must be exactly catalog-build, repository and policy, each ONCE"
  say "  [writes] ${FUGARO_BIN} apply: THE ONE APPLY. It builds the catalog image, redeploys the repository's jobs and publishes the catalog once each, under a ${DEADLINE}s deadline"
  say "  [reads] the one apply's own stream: the policy stage's own line and the repository stage's own line (a glyph, the stage's name and its detail), each printed exactly once"
  say "  [reads] ${FUGARO_BIN} apply --status --json again: which must owe nothing"
  say "  [writes] ${FUGARO_BIN} image rm ${ENTRY} --no-apply, ${FUGARO_BIN} settings set|unset ${KEY_A} --layer local --no-apply and the same for ${KEY_B}, then ONE ${FUGARO_BIN} apply: the teardown, which takes the entry back out and puts both keys back"
  say "  [writes] nothing else: one scratch directory (mode 0700) holding the transcripts, removed at the end"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK391_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK391_DRY_RUN:-}
  DEADLINE=${CHECK391_DEADLINE:-1800}
  ENTRY=${CHECK391_ENTRY:-chk391-catalog}
  DOCKERFILE=${CHECK391_DOCKERFILE:-}
  KEY_A=${CHECK391_KEY_A:-issues.nudge_after_days}
  VALUE_A=${CHECK391_VALUE_A:-8}
  KEY_B=${CHECK391_KEY_B:-issues.nudge_recurrence}
  VALUE_B=${CHECK391_VALUE_B:-4}
  while [ $# -gt 0 ]; do
    case "$1" in
    --dockerfile)
      [ $# -ge 2 ] || die "--dockerfile needs an owner/name:path. usage: $USAGE"
      DOCKERFILE=$2
      shift 2
      ;;
    --entry)
      [ $# -ge 2 ] || die "--entry needs a catalog entry name. usage: $USAGE"
      ENTRY=$2
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
      printf '\nWrites three changes with --no-apply, then runs ONE `%s apply`, and reads\nfrom the CLI that the three collapsed into one entry per step name: the policy\nand repository stages each ran once. It prints one block headed CHECK 391\nRESULT and exits 0 when the claim held, 1 when it did not, and 2 when this run\ncould not tell. It is yours to run: no agent runs any step of it.\n' "$FUGARO_BIN"
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
  if [ -z "$DOCKERFILE" ]; then
    die "Check 391 needs --dockerfile: the entry its first change adds is built from a Dockerfile in one of the project's repositories, named as owner/name:path. usage: $USAGE"
  fi
  case "$DOCKERFILE" in
  */*:*) ;;
  *) die "--dockerfile must name a Dockerfile in one of the project's repositories, as owner/name:path (got '$DOCKERFILE'). usage: $USAGE" ;;
  esac
  case "$ENTRY" in
  '' | *[!a-z0-9-]*) die "--entry must be a lowercase catalog entry name (got '$ENTRY'). usage: $USAGE" ;;
  esac
  if [ -z "$KEY_A" ] || [ -z "$KEY_B" ]; then
    die "Check 391 needs two settings keys to write (CHECK391_KEY_A, CHECK391_KEY_B): they are the two changes that owe the policy stage alone. usage: $USAGE"
  fi
  if [ "$KEY_A" = "$KEY_B" ]; then
    die "Check 391 needs two DIFFERENT settings keys (CHECK391_KEY_A and CHECK391_KEY_B are both '$KEY_A'): the point of the check is that two different writes owing the same stage leave one entry for it"
  fi
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
    fact checkout INFO "planned: a real run reads git rev-parse --show-toplevel and the checkout's origin remote, which name the repository the entry's change owes a redeploy to (D1574)"
    fact installation INFO "planned: a real run asks fugaro doctor --json which installation the config in force names$( [ -n "$PROJECT" ] && printf ', holds it to the --project %s you typed' "$PROJECT" ) and refuses an installation named belong in that answer too"
    fact entry INFO "planned: a real run reads fugaro image ls --json and removes an entry named ${ENTRY} it finds there (a leftover of an earlier attempt), so running the check twice works"
    fact keys INFO "planned: a real run reads fugaro settings get ${KEY_A} --json and ${KEY_B} --json: what each holds now, and whether the generic write path carries it. A key it cannot read or cannot write is one it does not touch"
    fact saved INFO "planned: a real run writes three changes with --no-apply: fugaro image add ${ENTRY} --dockerfile ${DOCKERFILE} (owing catalog-build, repository, policy), and fugaro settings set ${KEY_A} ${VALUE_A} --layer local and ${KEY_B} ${VALUE_B} --layer local (each owing policy)"
    fact status INFO "planned: a real run reads fugaro apply --status --json and holds the record to exactly three steps, one entry per step name: catalog-build, repository and policy, each once (D1562, D1570)"
    fact applied INFO "planned: a real run runs ONE fugaro apply under a ${DEADLINE}s deadline -- it builds the catalog image, redeploys the repository's jobs and publishes the catalog -- and reads the steps it answered out of its own 'applied' line"
    fact stages INFO "planned: a real run counts the stages of that apply's own transcript: the policy stage's own line and the repository stage's must each appear exactly once"
    fact owed INFO "planned: a real run reads fugaro apply --status --json again, which must owe nothing"
    fact teardown INFO "planned: a real run removes the entry with fugaro image rm ${ENTRY} --no-apply and puts both settings back the way it found them (their own value at the local layer, or unset), then runs ONE fugaro apply"
    fact claim INFO "planned: the verdict is a real run's, and it is the CLI's own record that answers it"
    BLOCK_KEYS=(xtrace agent-session checkout installation belong entry keys saved status applied stages owed teardown claim)
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_checkout
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  step_entry
  # A catalog this check could not read, or a leftover it could not remove, is a
  # reason NOT to run the commands: the entry it adds is one it must be able to
  # read first, and it must not write over a name it does not own.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_keys
  # A key that cannot be read, or that the write path refuses, is one this check
  # must not write: it could not put it back.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_saved
  # The one apply is only a reading if all three changes really were saved: a
  # change that failed leaves nothing to collapse, and running the apply over a
  # half-saved record would measure something else entirely.
  if [ "${SAVED_OK:-}" = yes ]; then
    step_status
    step_applied
    step_stages
    step_owed
  fi
  step_teardown
  the_verdict
}

FUGARO_BIN=${CHECK391_FUGARO:-fugaro}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
PROJECT_REGION=""
CHECKOUT=""
REPO_NAME=""
ENTRY=chk391-catalog
DOCKERFILE=""
DEADLINE=1800
KEY_A=issues.nudge_after_days
VALUE_A=8
KEY_B=issues.nudge_recurrence
VALUE_B=4
KEY_A_WINS=""
KEY_A_SET=""
KEY_A_OLD=""
KEY_B_WINS=""
KEY_B_SET=""
KEY_B_OLD=""
IMAGE_LS_STATUS=0
SKIP_REST=""
ENTRY_ADDED=""
SAVED_OK=""
KEY_A_WRITTEN=""
KEY_B_WRITTEN=""
SAVE_ADD_STATUS=0
SAVE_ADD_TIMED_OUT=no
PENDING_STATUS=0
OWED_BEFORE=""
APPLY_STATUS=0
APPLY_TIMED_OUT=no
APPLY_SECONDS=0
APPLIED_NAMES=""
APPLIED_BUILD=""
APPLIED_REPO=""
APPLIED_POLICY=""
STAGE_POLICY=""
STAGE_REPO=""
OWED_AFTER=""
TEARDOWN_OK=""
TEARDOWN_STATUS=0
TEARDOWN_TIMED_OUT=no

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
