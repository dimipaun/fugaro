#!/usr/bin/env bash
# Check 382 — a `budget set` that RAISES a cap is refused in a coding agent's
# session, and the refusal carries the command the owner can run instead, as ONE
# script YOU run.
#
# The no-prompt policy removed the typed confirmation from every raise (T7), and
# what replaced it is the fence (D1542, raiseOwnerOnly): the owner's own terminal
# asks nothing, and a coding agent's session is refused — because a cap the session
# it bounds can lift is not a limit (F8). The refusal is a courtesy and as strong
# as the markers themselves, no stronger (F20); what this check reads is that the
# fence is there at all, that it fires on the RAISE and not on the command, and
# that an agent's report carries a command its owner can act on without
# reconstructing the flags from a transcript.
#
# What it reads, in this order:
#
#   1. The installation: `fugaro doctor --json`'s project, held to what you typed
#      when you typed `--project`, with a project named `belong` refused in what
#      you typed and in what doctor answers, before anything is run.
#   2. The checkout, whose origin remote names the repository this raise is about.
#      The command below carries that repository explicitly, so the check and
#      `fugaro budget set` agree on which caps the raise would move.
#   3. The caps before: the repository's own caps node out of
#      `fugaro project show --json` (what a raise would move and what gets put
#      back) and the effective caps out of `fugaro budget show --json`. A node the
#      check cannot read is a refusal to run, because a cap it cannot put back is a
#      change to your project rather than a reading.
#   4. **The raise, which is the fact this check is**: `fugaro budget set --repo
#      <this repository> --daily <more than it holds now>`, run from the checkout,
#      with its stdin on /dev/null and `CLAUDECODE=1` set FOR THE CHILD ALONE. The
#      value is chosen so the call is always a raise and never a no-op: setting a
#      cap where none existed is a raise as much as an increase is
#      (editMicroNode), and a no-op answers "nothing changed" instead of asking the
#      fence. The marker goes on the child's own command line, so this check's own
#      environment is untouched -- and the environment it was started in must
#      carry no marker either, which is the agent-session guard above.
#   5. The refusal: exit 1 (the code a user error carries), and one sentence with
#      all of it -- the act in its own words ("raising a cap is the owner's own
#      step"), the marker it read, that nothing was applied, and the same
#      invocation you would type at your own terminal. A refusal missing any part
#      is a FAIL: an agent handed half that sentence has nothing to hand over.
#   6. The command it printed: parsed out of the sentence, it has to name the same
#      repository, the same value and your project, start with the program this
#      check ran, and carry no flag that `fugaro budget set --help` does not name.
#      A command with an invented flag is one the owner cannot run.
#   7. The caps after: read out of `fugaro budget show --json` and held to what was
#      there before. A raise that LANDED is a FAIL of the loudest kind (the fence
#      did not hold, and a cap moved), and the putting back below runs in that case
#      and in the case where the caps could not be read back at all.
#
# **What a PASS means, and what it does not.** A PASS means: the raise was refused
# with exit 1 before anything was applied, the sentence carried all four parts and
# a runnable command, and the caps read back exactly as they were. It does NOT mean
# the fence is a barrier: it is a marker test, and a session that unsets CLAUDECODE
# is outside it (F20) — this check reads the marker it sets itself, and nothing
# here says anything about a session that clears its own.
#
# **What it does to your project**: nothing at all, on a PASS — the refusal comes
# before the write, and the read-back row says so. It raises a cap deliberately, so
# a FAIL where the raise LANDED leaves your project with the raised cap and the
# teardown puts the caps back from the exit trap, on every way out, reading them
# back afterwards to say so. No build, no run and no model call is made.
#
# Usage:      bash scripts/live-checks/check-382-agent-cannot-raise-a-cap.sh [--checkout <path>]
#                                             [--project <fugaro project>]
#                                             [--fugaro-flags '<flags>'] [--deadline <seconds>]
#                                             [--dry-run]
#             --checkout is the checkout the raise runs from (default `.`); its
#             origin remote names the repository whose caps the raise is about.
#             --project is the Fugaro project. Leave it out where a project config
#             is in force: `fugaro doctor --json` answers with it and this check
#             holds the two to each other.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on whitespace, so
#             a value with a space in it cannot be passed.
#             --deadline is how long the raise may take, in seconds (default 300;
#             CHECK382_DEADLINE does the same). The refusal is immediate; the bound
#             is for a command that hangs.
#             --dry-run prints the plan and makes no call at all: it raises nothing.
# Needs:      bash, mktemp, git, grep, sed, head, tail, cut, python3, and a `fugaro`
#             on PATH with `doctor`, `project show`, `budget show`, `budget set` and
#             `budget set --help`. Nothing is installed and nothing is fetched; a
#             missing tool stops the run with one line naming it.
# Where:      your own machine, in the checkout of a SANDBOX repository whose
#             project is NOT `belong` — that name is refused before anything is
#             run, in what you typed and in what doctor answers. A coding agent's
#             session is refused too: this check raises a real cap on a real
#             project, so it is yours to run at your own terminal.
# Costs:      `fugaro doctor --json`, `fugaro project show --json`, three
#             `fugaro budget show --json`, one `fugaro budget set --help` and ONE
#             `fugaro budget set` that is refused before it is applied. No model
#             call, no build and no run, and no cloud write at all unless a row
#             says the raise landed.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 382 RESULT.
#             Exit 0 when the claim held (the raise was refused, non-zero, with the
#             whole sentence and a runnable command, and nothing moved), 1 when it
#             did not, and 2 when this run could not tell (a guard stopped it, the
#             caps could not be read, a tool is missing).
#
# CHECK382_FUGARO names the binary (default `fugaro`), CHECK382_DEADLINE the
# default deadline, CHECK382_DRY_RUN makes every run a dry one and
# CHECK382_KEEP_SCRATCH keeps the scratch directory (with every call's own output)
# instead of removing it, so this script's own test can drive the same code and you
# can read what a failing run printed.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them would end the run with no fact recorded at
# all, and the block would then report the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had: a shell that arrived
# tracing prints every line of this run into the log you would then paste back, and
# this check prints a command carrying your project's name and reads your project's
# caps. So the trace is turned off here, BASH_XTRACEFD is unset so a trace
# redirected to a file this script knows nothing about cannot be reopened, and
# guard_xtrace refuses a shell that arrived tracing (the flag sits in
# SHELLOPTS/BASHOPTS then, and every child would start tracing on its own).
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# Your own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is written
# to the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
HELPED=no
DRY_RUN=""
USAGE="check-382-agent-cannot-raise-a-cap.sh [--checkout <path>] [--project <fugaro project>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap puts the caps
# back where they need it, prints the block and removes the scratch directory, so a
# stop still gives you something to paste back, and 2 says this run could not tell
# rather than that the claim does not hold.
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
# finding about the release. The row reads FAIL in the block -- it is not a pass --
# and every caller stops the run after it, because what comes next raises a real
# cap; the verdict row is filled in here rather than left to read "not reached", so
# the block's own headline says why this run has no answer.
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  fact fence INFO "the fence on a raise is NOT MEASURED by this run: the $1 row above could not be settled, so nothing here is an answer about whether a coding agent's session can raise a cap"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the first
# command that fails outside a guard would end the run with no fact at all, and the
# block would then read as an answer about the fence for a run that measured
# nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the fence on a raise"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 382 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 382 needs bash"
  need mktemp
  need git "the checkout's origin remote is how this check knows which repository's cap it raises"
  need grep
  need sed
  need head
  need tail
  need cut
  need python3 "the doctor, project, budget and help output are read with it"
  need "$FUGARO_BIN" "every read, the raise and its help are this program's"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk382-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK382_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk382.XXXXXXXX") || die "Check 382 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 382 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: restore_caps records what it
# managed to do in the facts captured below, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  restore_caps
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
    say "CHECK382_KEEP_SCRATCH is set: $SCRATCH is kept (every call's own output is in it)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing prints every line of this run into the
# log an owner pastes back, and this run prints a command carrying the project's
# name and reads the project's caps. A tracing shell is refused rather than trusted.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check prints the command the refusal carries and the block you would paste back. Run it without -x"
    stop "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check prints the command the refusal carries and the block you would paste back. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: no trace of this run can copy the caps it read, the command it prints or the block it prints"
}

# guard_agent_session: this check is the owner's, and the one read it makes is of a
# fence against exactly this session. Refusing here is not belt and braces: a
# session that ran it would have CLAUDECODE set in its own environment, and the
# child below would then refuse because of the session rather than because of the
# marker the check put there -- the reading would be of nothing.
#
# The loop is the five markers that say an agent, and not a person, is driving
# this shell: agentenv.AgentMarkers. agentenv.Markers holds a sixth,
# CLAUDE_CODE_SSE_PORT, and it is not one of those -- it says this shell sits in
# an editor's terminal, where the person is still the one typing, so refusing on
# it would lock you out of this check in your own editor. It is DROPPED from this
# script's environment instead: the child below has no terminal (its stdin is on
# /dev/null), and the marker the fence has to read is the one this check puts on
# that child's command line, `${MARKER}` in the loop's own order, never a marker
# this shell walked in with.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "$marker is set, so this is a coding agent's session: this check raises a real cap to read the fence, and it has to set the marker itself for its own child to be a reading of anything. Check 382 is yours to run at your own terminal, on the sandbox installation. No agent runs any step of it"
    stop "$marker is set, so this is a coding agent's session: this check raises a real cap to read the fence, and it has to set the marker itself for its own child to be a reading of anything. Check 382 is yours to run at your own terminal, on the sandbox installation. No agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set in THIS check's environment (CLAUDECODE and the like), so the marker the raise below carries is the one this check put there and nothing else: the refusal it reads is the fence's, not this session's. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused: it says this shell sits in an editor's terminal, not that an agent is driving it, and the child below has no terminal, where it would be read as this session's own marker"
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
    fact belong FAIL "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would raise the owner's own caps"
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would raise the owner's own caps"
  fi
}

# ------------------------------------------------------------------- the jsons
# The four documents and the refusal's text, in one program: a live check on the
# owner's machine has python3 and not necessarily jq, and a row that read a cap with
# grep would read a number that is not there as "not set". Every query prints
# `key=value` lines, so the shell holds them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def money(v):
    """A USD figure as a flag that wrote it would take it back: no trailing
    zeros, and never an empty string."""
    s = "%.6f" % float(v)
    s = s.rstrip("0").rstrip(".")
    return s or "0"


def load(path):
    if path == "/dev/stdin":
        return json.load(sys.stdin)
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def unkey(key):
    """budget.Key's inverse: the caps nodes are keyed by escaped slugs, so a name
    this check knows has to be read back out of the key to be matched."""
    out = []
    i = 0
    while i < len(key):
        if key[i] == "%" and i + 3 <= len(key):
            try:
                out.append(chr(int(key[i + 1:i + 3], 16)))
                i += 3
                continue
            except ValueError:
                pass
        out.append(key[i])
        i += 1
    return "".join(out)


def repo_of(url):
    """owner/name out of an origin remote, in the shapes git prints them:
    https://github.com/owner/name.git, git@github.com:owner/name.git,
    ssh://git@github.com/owner/name, and the same on bitbucket."""
    u = url.strip()
    u = re.sub(r"\.git$", "", u)
    u = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", u)
    head, _, rest = u.partition("/")
    u = rest if "@" in head else u
    parts = [p for p in re.split(r"[/:]", u) if p]
    if len(parts) < 2:
        return ""
    return "%s/%s" % (parts[-2], parts[-1])


def doctor(path):
    doc = load(path)
    project = doc.get("project") if isinstance(doc, dict) else None
    if not isinstance(project, dict):
        return None
    return project.get("name") or "", project.get("gcp_project") or ""


def node(path, name):
    """The repository's OWN caps node, out of `project show --json`: the raw nodes
    (caps.repos), matched by the repository the node names or by the key. A number
    is the node's own value; null is a node that does not set that cap; an absent
    node is no node at all, which is a different thing."""
    doc = load(path)
    caps = (doc.get("caps") or {}) if isinstance(doc, dict) else {}
    repos = caps.get("repos") or {}
    low = name.lower()
    found = None
    for key, pair in repos.items():
        if not isinstance(pair, dict):
            continue
        if (pair.get("repo") or "").lower() == low or unkey(key).lower() == low or key.lower() == low:
            found = pair
            break
    if found is None:
        return None
    return found.get("daily_cap_usd"), found.get("per_run_cap_usd")


def caps(path, name):
    """`budget show --json`: this repository's row -- the effective caps in force,
    where each comes from, and what the day counts for it."""
    doc = load(path)
    out = [("mode", doc.get("mode") or "")]
    rows = doc.get("repos") or []
    out.append(("rows", str(len(rows))))
    match = -1
    low = name.lower()
    for i, row in enumerate(rows):
        if not isinstance(row, dict):
            continue
        if (row.get("repo") or "").lower() == low or (row.get("slug") or "").lower() == low or len(rows) == 1:
            match = i
            break
    out.append(("match", str(match)))
    if match >= 0:
        row = rows[match]
        for key, src in (("repo", row.get("repo")),
                         ("slug", row.get("slug")),
                         ("daily", row.get("daily_cap_usd")),
                         ("daily_source", row.get("daily_cap_source")),
                         ("perrun", row.get("per_run_cap_usd")),
                         ("perrun_source", row.get("per_run_cap_source")),
                         ("counted", row.get("counted_usd"))):
            if src is None:
                out.append((key, ""))
            elif isinstance(src, str):
                out.append((key, src))
            else:
                out.append((key, money(src)))
    return out


def raise_to(daily, perrun):
    """The value this check raises the repository's daily cap to: above everything
    the node holds of its own, so the call is a raise whatever the node is and a
    no-op never. A node that holds nothing raises from nothing, which is the
    smallest value the flag takes."""
    top = 0.0
    for v in (daily, perrun):
        if v not in (None, ""):
            top = max(top, float(v))
    return money(round(top + 0.01, 6))


def printed_command(command):
    """The invocation the refusal hands over, out of the sentence: the flags it
    carries and the value of each of the three this check asked for. A value that
    was quoted is unquoted, because the sentence is meant to be pasted and the
    quotes are the sentence's business, not the value's."""
    out = [("flags", " ".join(sorted(set(re.findall(r"--[a-z][a-z-]*", command)))))]
    for name in ("repo", "daily", "project"):
        m = re.search(r"--" + name + r"=(\S+)", command)
        out.append((name, m.group(1).strip("'\"") if m else ""))
    first = command.split(" ", 1)[0] if command.split(" ", 1) else ""
    out.append(("prog", first.strip("'\"")))
    out.append(("set", "yes" if re.search(r"\bbudget set\b", command) else "no"))
    return out


def flags_in_help(path):
    """The flags `budget set --help` names, as one string to search."""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        return ""
    return " ".join(sorted(set(re.findall(r"--[a-z][a-z-]*", text))))


def flat(paths):
    """The text of every file, whitespace collapsed: a refusal is one long sentence
    a terminal wraps, and this check looks for its parts in the text rather than for
    where the line breaks happened to fall."""
    text = []
    for path in paths:
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                text.append(fh.read())
        except OSError:
            continue
    return re.sub(r"\s+", " ", " ".join(text)).strip()


what = sys.argv[1]
if what == "doctor":
    got = doctor(sys.argv[2])
    if got is None:
        sys.exit(1)
    print("name=%s" % got[0])
    print("gcp=%s" % got[1])
elif what == "origin":
    repo = repo_of(sys.argv[2])
    if not repo:
        sys.exit(1)
    print("repo=%s" % repo)
elif what == "node":
    got = node(sys.argv[2], sys.argv[3])
    if got is None:
        print("node=absent")
    else:
        print("node=found")
        print("daily=%s" % ("" if got[0] is None else money(got[0])))
        print("perrun=%s" % ("" if got[1] is None else money(got[1])))
elif what == "caps":
    for key, value in caps(sys.argv[2], sys.argv[3]):
        print("%s=%s" % (key, value))
elif what == "raise":
    print("daily=%s" % raise_to(sys.argv[2], sys.argv[3]))
elif what == "command":
    for key, value in printed_command(sys.argv[2]):
        print("%s=%s" % (key, value))
elif what == "help":
    print("flags=%s" % flags_in_help(sys.argv[2]))
elif what == "flat":
    print(flat(sys.argv[2:]))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json DEST QUERY ARGS...: one query into a file, `key=value` lines.
read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY -- the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# last_line FILE: the last non-blank line of a file, bounded, for a row a reader
# has to be able to scan. The CLI's own account of how its run ended is the last
# thing it printed, and for a refused raise that is the refusal itself.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-400 || true
  fi
}

# ------------------------------------------------------------------ the install
judge_project() {
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
      PROJECT_GCP=""
      fact project INFO "fugaro doctor --json did not answer about a project (exit $status: $(last_line "$SCRATCH/doctor.err")), so the installation this check is about is the one you typed, $PROJECT. Every call below carries --project $PROJECT, so a config that is not that project's is refused by the CLI rather than read here"
      return 0
    fi
    unmeasured project "fugaro doctor --json did not answer about a project (exit $status: $(last_line "$SCRATCH/doctor.err")), and no --project was typed, so this check cannot tell which installation it is about to raise a cap on. Run it where a project config is in force, or pass --project <name>"
    stop "fugaro doctor --json named no project and no --project was typed: this check cannot tell which installation's cap it would raise"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    stop "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
  fi
  fact project PASS "the local config in force is project $PROJECT_NAME${PROJECT_GCP:+ on GCP project $PROJECT_GCP} (fugaro doctor --json): the raise below is that installation's, and the command the refusal carries names it"
}

# The checkout is where the raise runs and whose repository it is about, so it is
# read from the disk rather than asked of the CLI: the origin remote is what the
# CLI itself resolves a repository from, and a check that took a name from elsewhere
# could raise one repository's cap while reading another's.
step_checkout() {
  if [ ! -d "$CHECKOUT" ]; then
    fact checkout FAIL "--checkout '$CHECKOUT' is not a directory, so there is no checkout to raise from and no origin to read"
    stop "--checkout '$CHECKOUT' is not a directory"
  fi
  if ! git -C "$CHECKOUT" rev-parse --show-toplevel >/dev/null 2>&1; then
    fact checkout FAIL "$CHECKOUT is not a git work tree, and the repository whose cap this check raises is the one its origin remote names"
    stop "$CHECKOUT is not a git work tree"
  fi
  local url status=0
  url=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) || status=$?
  if [ "$status" -ne 0 ] || [ -z "$url" ]; then
    fact checkout FAIL "$CHECKOUT has no 'origin' remote, so this check cannot name the repository whose cap the raise is about. Run it in the checkout of a SANDBOX repository whose origin names it"
    stop "$CHECKOUT has no origin remote that names a repository"
  fi
  REPO_NAME=$(python3 "$SCRATCH/read.py" origin "$url" 2>/dev/null | sed -n 's/^repo=//p' | head -1) || true
  if [ -z "$REPO_NAME" ]; then
    fact checkout FAIL "the origin remote of $CHECKOUT ($url) does not name a repository this check can read as owner/name"
    stop "the origin remote of $CHECKOUT does not name a repository this check can read"
  fi
  fact checkout PASS "the checkout $CHECKOUT is a git work tree whose origin names $REPO_NAME: the raise below carries that repository, so the cap it is about and the cap this check reads are the same one"
}

# ------------------------------------------------------------------- the caps
# read_caps_state reads the repository's own caps node and its effective caps into
# the scratch. It is called twice: once before the raise (this is the value the
# raise would move, and what gets put back) and once after it (the reading that says
# whether anything moved).
RC_DAILY=""
RC_PERRUN=""
RC_WHY=""
read_caps_state() { # read_caps_state TAG -> RC_DAILY, RC_PERRUN, or RC_WHY and 1
  local tag=$1
  local args=(budget show --json --repo "$REPO_NAME")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/$tag.json" 2>"$SCRATCH/$tag.err" || status=$?
  read_json "$SCRATCH/$tag.txt" caps "$SCRATCH/$tag.json" "$REPO_NAME" || true
  if [ "$(field "$SCRATCH/$tag.txt" match)" != "0" ]; then
    RC_WHY="exit $status: $(last_line "$SCRATCH/$tag.err")"
    return 1
  fi
  RC_DAILY=$(field "$SCRATCH/$tag.txt" daily)
  RC_PERRUN=$(field "$SCRATCH/$tag.txt" perrun)
  return 0
}

read_node() {
  local pargs=(project show --json)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then pargs+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then pargs+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${pargs[@]}" >"$SCRATCH/project.json" 2>"$SCRATCH/project.err" || status=$?
  read_json "$SCRATCH/node.txt" node "$SCRATCH/project.json" "$REPO_NAME" || true
  NODE_STATE=$(field "$SCRATCH/node.txt" node) || NODE_STATE=""
  OWN_DAILY=""
  OWN_PERRUN=""
  OWN_DAILY_SET=no
  OWN_PERRUN_SET=no
  case "$NODE_STATE" in
  found)
    OWN_DAILY=$(field "$SCRATCH/node.txt" daily)
    OWN_PERRUN=$(field "$SCRATCH/node.txt" perrun)
    if [ -n "$OWN_DAILY" ]; then OWN_DAILY_SET=yes; fi
    if [ -n "$OWN_PERRUN" ]; then OWN_PERRUN_SET=yes; fi
    ;;
  absent)
    # No node of this repository's own. That is only safe to write to because
    # `budget set --clear` then puts back exactly nothing -- but it must be TRUE:
    # a source of "repo" on either cap says a node exists that this check could not
    # find, and clearing one that exists is a change, not a putting back.
    if [ "$CAPS_DAILY_SOURCE" = repo ] || [ "$CAPS_PERRUN_SOURCE" = repo ]; then
      NODE_STATE=unmatched
    fi
    ;;
  esac
  return 0
}

step_caps() {
  if ! read_caps_state before; then
    unmeasured caps "fugaro budget show --json --repo $REPO_NAME did not answer with this repository's row (${RC_WHY}), so this check cannot see the cap the raise would move or read it back afterwards. Its output is in $SCRATCH/before.json (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    stop "fugaro budget show --json --repo $REPO_NAME answered no row for it"
  fi
  BEFORE_DAILY=$RC_DAILY
  BEFORE_PERRUN=$RC_PERRUN
  CAPS_DAILY_SOURCE=$(field "$SCRATCH/before.txt" daily_source)
  CAPS_PERRUN_SOURCE=$(field "$SCRATCH/before.txt" perrun_source)
  CAPS_MODE=$(field "$SCRATCH/before.txt" mode)
  CAPS_COUNTED=$(field "$SCRATCH/before.txt" counted)
  read_node
  case "$NODE_STATE" in
  # Both are readable: a node of this repository's own, or none at all, which the
  # putting back answers with --clear.
  found | absent) ;;
  unmatched)
    unmeasured caps "the repository's own caps node holds a cap this check cannot read: fugaro budget show --json says its daily comes from the repository ($CAPS_DAILY_SOURCE) and its per-run from $CAPS_PERRUN_SOURCE, while fugaro project show --json lists no caps node naming $REPO_NAME ($(last_line "$SCRATCH/project.err")). This check will not raise a cap it cannot put back. Its two documents are in $SCRATCH/project.json and $SCRATCH/before.json (set CHECK382_KEEP_SCRATCH=1 to keep them)"
    stop "the repository's own caps node could not be read, and this check will not raise a cap it cannot put back"
    ;;
  *)
    unmeasured caps "fugaro project show --json did not answer (its output is in $SCRATCH/project.json, and set CHECK382_KEEP_SCRATCH=1 to keep it), so this check cannot read the repository's own caps node: it will not raise a cap it cannot put back"
    stop "fugaro project show --json did not answer: the repository's own caps node could not be read"
    ;;
  esac
  local node_words
  if [ "$OWN_DAILY_SET" = yes ] && [ "$OWN_PERRUN_SET" = yes ]; then
    node_words="daily \$$OWN_DAILY and per-run \$$OWN_PERRUN"
  elif [ "$OWN_DAILY_SET" = yes ]; then
    node_words="daily \$$OWN_DAILY (no per-run of its own)"
  elif [ "$OWN_PERRUN_SET" = yes ]; then
    node_words="per-run \$$OWN_PERRUN (no daily of its own)"
  else
    node_words="no cap at all (the repository falls back to the defaults)"
  fi
  fact caps PASS "the repository $REPO_NAME's own caps node holds $node_words, and the effective caps in force are daily \$$BEFORE_DAILY (from ${CAPS_DAILY_SOURCE:-none}) and per-run \$$BEFORE_PERRUN (from ${CAPS_PERRUN_SOURCE:-none}), with \$$CAPS_COUNTED counted today in the '${CAPS_MODE:-unset}' mode (fugaro budget show --json). This is what the raise below would move, and what the unchanged row reads back"
}

# ------------------------------------------------------------------ the raise
# The value is above everything the node holds of its own, so the call is a raise
# whatever the node is; a value the node already held would be a no-op, and the CLI
# answers "nothing changed" without asking the fence at all.
step_raise_value() {
  RAISE_DAILY=$(python3 "$SCRATCH/read.py" raise "$OWN_DAILY" "$OWN_PERRUN" 2>/dev/null | sed -n 's/^daily=//p' | head -1) || RAISE_DAILY=""
  if [ -z "$RAISE_DAILY" ]; then
    unmeasured caps "the value to raise the cap to could not be worked out from the node's own caps ('$OWN_DAILY'/'$OWN_PERRUN'), so there is no raise to run"
    stop "the raise value could not be worked out"
  fi
  RAISE_ARGS=(budget set --repo "$REPO_NAME" --daily "$RAISE_DAILY")
  fact raise PASS "the raise this check runs is ${FUGARO_BIN} ${RAISE_ARGS[*]}${PROJECT:+ --project $PROJECT}: \$$RAISE_DAILY against a node that holds $([ "$OWN_DAILY_SET" = yes ] && printf '$%s' "$OWN_DAILY" || printf 'no daily of its own'), which is a raise (setting a cap where none existed is one) and never a no-op, so the fence is asked rather than answered with 'nothing changed'"
}

# run_raise SECONDS: the raise from the checkout, its stdin on /dev/null, under a
# deadline, with the marker set FOR THE CHILD ALONE. The child execs, so the pid the
# deadline kills IS the command; the ERR trap is dropped in it, because this check
# reads the command's failing exit status as one of its own readings and a trap
# inherited there would turn it into an internal error before the row could report
# it.
RAISE_STATUS=0
RAISE_TIMED_OUT=no
RAISE_SECONDS=0
run_raise() { # run_raise SECONDS
  local secs=$1
  RAISE_TIMED_OUT=no
  # The raise is ATTEMPTED from here on. Whether it landed is not known until
  # step_unchanged reads the caps back, and a run that ends in between (a Ctrl-C
  # during the child, a stop in a step after it) never learns: RAISE_ATTEMPTED is
  # what tells the exit trap to read them back itself instead of reporting a
  # raise that was refused.
  RAISE_ATTEMPTED=yes
  local started=$SECONDS
  : >"$SCRATCH/raise.out"
  : >"$SCRATCH/raise.err"
  (cd "$CHECKOUT" && trap - ERR; exec env "$MARKER=1" "$FUGARO_BIN" "${RAISE_ARGS[@]}") </dev/null >>"$SCRATCH/raise.out" 2>>"$SCRATCH/raise.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RAISE_TIMED_OUT=yes
      say "the raise command is still going after ${secs}s: killing it (TERM, then KILL)"
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
  RAISE_STATUS=0
  wait "$pid" 2>/dev/null || RAISE_STATUS=$?
  RAISE_SECONDS=$((SECONDS - started))
}

step_raise() {
  local args=("${RAISE_ARGS[@]}")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  RAISE_ARGS=("${args[@]}")
  say "running: cd $CHECKOUT && $MARKER=1 ${FUGARO_BIN} ${args[*]} < /dev/null  (deadline ${DEADLINE}s; the fence refuses it before anything is applied)"
  run_raise "$DEADLINE"
  RAISE_LAST=$(last_line "$SCRATCH/raise.err")
  RAISE_LAST_FROM=stderr
  if [ -z "$RAISE_LAST" ]; then
    RAISE_LAST=$(last_line "$SCRATCH/raise.out")
    RAISE_LAST_FROM=stdout
  fi
  if [ "$RAISE_TIMED_OUT" = yes ]; then
    fact marker FAIL "the raise was still running after ${DEADLINE}s and this check killed it (TERM, then KILL): nothing in budget set waits on a terminal, so a command that hangs here is its own finding, and the fence was never reached. Its last line ($RAISE_LAST_FROM) was: ${RAISE_LAST:-<nothing>}. The whole transcript is in $SCRATCH/raise.out and raise.err (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  case "$RAISE_STATUS" in
  1)
    fact marker PASS "\$MARKER=1 was set on the child's own command line and NOWHERE else -- this check's own environment carries no coding agent's marker (the agent-session row) -- and the command ended after ${RAISE_SECONDS}s with exit 1, the code a refusal carries. The refusal row reads whether the sentence is really there, and the unchanged row reads whether anything was applied"
    ;;
  0)
    fact marker FAIL "the raise ended after ${RAISE_SECONDS}s with exit 0, so it was NOT refused: the fence let a coding agent's session raise this repository's cap, and the cap has almost certainly moved. The unchanged row reads it back and the teardown puts it back. Its last line ($RAISE_LAST_FROM): ${RAISE_LAST:-<nothing>}. The whole transcript is in $SCRATCH/raise.out and raise.err (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    ;;
  *)
    fact marker FAIL "the raise ended after ${RAISE_SECONDS}s with exit $RAISE_STATUS, which is not the code a refused raise carries (1): the fence was never reached, because something before it refused or failed. Its last line ($RAISE_LAST_FROM): ${RAISE_LAST:-<nothing>}. The whole transcript is in $SCRATCH/raise.out and raise.err (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    ;;
  esac
}

# ---------------------------------------------------------------- the refusal
# The parts the sentence has to carry, in the words raiseOwnerOnly and
# AgentRefusal use. The refusal is the whole of what replaced the typed
# confirmation in front of a raise, so a part it lost is a part an operator cannot
# act on. The parts are looked for in the whitespace-collapsed text of BOTH
# streams: where a terminal wraps that sentence is not something a check may
# depend on.
RAISE_FLAT=""
MISSING=""
require() { # require "part" -- records a part the refusal is missing
  local part=${1# }
  part=${part% }
  case "$RAISE_FLAT" in
  *"$1"*) ;;
  *) MISSING="${MISSING:+$MISSING; }$part" ;;
  esac
}

step_refusal() {
  if [ "$RAISE_TIMED_OUT" = yes ] || [ "$RAISE_STATUS" != 1 ]; then
    RAISE_FLAT=""
    if [ "$RAISE_TIMED_OUT" = yes ]; then
      fact refusal FAIL "there is no refusal to read: the raise never ended (the marker row says so), so this check read no sentence at all"
    else
      fact refusal FAIL "there is no refusal to read: the raise exited $RAISE_STATUS, not the refusal's own code (1) (the marker row says what the command printed)"
    fi
    return 0
  fi
  read_json "$SCRATCH/flat.txt" flat "$SCRATCH/raise.err" "$SCRATCH/raise.out" || true
  RAISE_FLAT=$(cat "$SCRATCH/flat.txt" 2>/dev/null || true)
  MISSING=""
  require "raising a cap is the owner's own step: "
  require "a coding agent's session is present ($MARKER is set), so nothing is applied: "
  require "run this in your own terminal window, not through a coding agent"
  require "; nothing was changed. "
  require "Run this yourself at your own terminal: "
  require "budget set "
  if [ -n "$MISSING" ]; then
    fact refusal FAIL "the refusal is missing: $MISSING. The refusal is the whole of what replaced the typed confirmation in front of a raise, so a part it lost is a part an agent cannot hand to its owner (F8, D1542). What the command printed: ${RAISE_FLAT:-<nothing>}"
    return 0
  fi
  fact refusal PASS "the refusal carries all of it: the act in the fence's own words ('raising a cap is the owner's own step'), the marker it read ($MARKER, the one this check set), that nothing is applied, AgentAdvice's own sentence about the terminal window, that nothing was changed, and the invocation the owner can run instead. What the command printed: $RAISE_FLAT"
}

# ---------------------------------------------------------------- the command
step_command() {
  if [ "$RAISE_TIMED_OUT" = yes ] || [ "$RAISE_STATUS" != 1 ] || [ -z "$RAISE_FLAT" ]; then
    fact command FAIL "there is no printed command to read: the raise produced no refusal (the marker and refusal rows say what it did)"
    return 0
  fi
  local command
  command=$(printf '%s' "$RAISE_FLAT" | sed -n 's/.*Run this yourself at your own terminal: //p' | head -1)
  if [ -z "$command" ]; then
    fact command FAIL "the refusal names no command after 'Run this yourself at your own terminal: ', which is the whole of what makes an agent's report actionable (F8). What the command printed: $RAISE_FLAT"
    return 0
  fi
  read_json "$SCRATCH/command.txt" command "$command" || true
  read_json "$SCRATCH/help.txt" help "$SCRATCH/help.out" || true
  local prog set flags repo daily project help_flags
  prog=$(field "$SCRATCH/command.txt" prog)
  set=$(field "$SCRATCH/command.txt" set)
  flags=$(field "$SCRATCH/command.txt" flags)
  repo=$(field "$SCRATCH/command.txt" repo)
  daily=$(field "$SCRATCH/command.txt" daily)
  project=$(field "$SCRATCH/command.txt" project)
  help_flags=$(field "$SCRATCH/help.txt" flags)
  print_command_findings "$prog" "$set" "$flags" "$repo" "$daily" "$project" "$help_flags" "$command"
}

print_command_findings() { # print_command_findings PROG SET FLAGS REPO DAILY PROJECT HELP_FLAGS COMMAND
  local prog=$1 set=$2 flags=$3 repo=$4 daily=$5 project=$6 help_flags=$7 command=$8
  local missing=""
  # The program is compared by its name and not by the word: this check may have
  # been handed a path (CHECK382_FUGARO=/usr/local/bin/fugaro) while the CLI prints
  # what it was invoked as, and both are the same program to an owner's shell.
  case "${prog##*/}" in
  "${FUGARO_BIN##*/}") ;;
  *) missing="${missing:+$missing; }the program this check ran (${FUGARO_BIN}): it printed '$prog', which the owner may not have" ;;
  esac
  if [ "$set" != yes ]; then
    missing="${missing:+$missing; }the command itself (\`budget set\`)"
  fi
  local f
  for f in --repo --daily --project; do
    case " $flags " in
    *" $f "*) ;;
    *) missing="${missing:+$missing; }$f" ;;
    esac
  done
  if [ -z "$repo" ]; then
    missing="${missing:+$missing; }a --repo value"
  elif [ "$(printf '%s' "$repo" | tr '[:upper:]' '[:lower:]')" != "$(printf '%s' "$REPO_NAME" | tr '[:upper:]' '[:lower:]')" ]; then
    missing="${missing:+$missing; }the repository this check raised the cap of ($REPO_NAME), not '$repo'"
  fi
  if [ -z "$daily" ]; then
    missing="${missing:+$missing; }a --daily value"
  elif ! python3 -c 'import sys; sys.exit(0 if abs(float(sys.argv[1]) - float(sys.argv[2])) < 1e-9 else 1)' "$daily" "$RAISE_DAILY" 2>/dev/null; then
    missing="${missing:+$missing; }the value this check raised it to (\$$RAISE_DAILY), not '$daily'"
  fi
  if [ -n "$PROJECT_NAME" ]; then
    if [ -z "$project" ]; then
      missing="${missing:+$missing; }--project (the fence prints the project this run is about: a command without it would act on whatever project the owner's terminal happens to be set to)"
    elif [ "$project" != "$PROJECT_NAME" ]; then
      missing="${missing:+$missing; }--project $PROJECT_NAME, not '$project'"
    fi
  fi
  for f in $flags; do
    case " $help_flags " in
    *" $f "*) ;;
    *) missing="${missing:+$missing; }$f, which \`${FUGARO_BIN} budget set --help\` does not name -- a command an owner cannot run" ;;
    esac
  done
  if [ -n "$missing" ]; then
    COMMAND_BAD=yes
    fact command FAIL "the printed command is not one the owner can run: it is missing $missing. The command was: $command. What \`${FUGARO_BIN} budget set --help\` names: ${help_flags:-<nothing readable>}"
    return 0
  fi
  fact command PASS "the printed command is one the owner can run: \`$command\` starts with the program this check ran, runs \`budget set\`, and carries --repo=$repo (the repository this check raised the cap of), --daily=$daily (the value it raised it to) and --project=$project (this installation's own project, so it cannot act on another). Every flag it names is one \`${FUGARO_BIN} budget set --help\` lists, so nothing here has to be reconstructed from a transcript"
}

step_help() {
  local args=(budget set --help)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/help.out" 2>"$SCRATCH/help.err" || status=$?
  if [ "$status" -ne 0 ] || ! grep -q -- '--daily' "$SCRATCH/help.out" 2>/dev/null; then
    unmeasured command "fugaro budget set --help (exit $status: $(last_line "$SCRATCH/help.err")) did not print the flags of \`budget set\`, so this check cannot say whether the flag the refusal printed is one this CLI takes. Its output is in $SCRATCH/help.out (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    stop "fugaro budget set --help did not print the command's flags"
  fi
  fact help PASS "\`${FUGARO_BIN} budget set --help\` printed the command's own flags, so the printed command below is read against the CLI this check actually ran rather than against a list this script remembers"
}

# ----------------------------------------------------------------- unchanged
step_unchanged() {
  if ! read_caps_state after; then
    RESTORE_NEEDED=yes
    unmeasured unchanged "fugaro budget show --json --repo $REPO_NAME did not answer with this repository's row after the raise (${RC_WHY}), so this check cannot say whether the raise was applied. A raise may have landed, so the teardown puts the caps back, and its row says what that did. Its output is in $SCRATCH/after.json (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    stop "the caps could not be read back after the raise"
  fi
  AFTER_DAILY=$RC_DAILY
  AFTER_PERRUN=$RC_PERRUN
  CAPS_READ_AFTER=yes
  if [ "$AFTER_DAILY" = "$BEFORE_DAILY" ] && [ "$AFTER_PERRUN" = "$BEFORE_PERRUN" ]; then
    fact unchanged PASS "the caps read back exactly as they were before the raise: daily \$$BEFORE_DAILY and per-run \$$BEFORE_PERRUN then, daily \$$AFTER_DAILY and per-run \$$AFTER_PERRUN now (fugaro budget show --json --repo $REPO_NAME). Nothing of your project was changed, which is what the refusal's own 'nothing was changed' claims"
    return 0
  fi
  RESTORE_NEEDED=yes
  fact unchanged FAIL "the caps MOVED: they were daily \$$BEFORE_DAILY and per-run \$$BEFORE_PERRUN before the raise and are daily \$$AFTER_DAILY and per-run \$$AFTER_PERRUN now (fugaro budget show --json --repo $REPO_NAME), so the raise was APPLIED in a coding agent's session and the fence did not hold (F8). The teardown puts them back and its row says what that did"
}

# ---------------------------------------------------------------- putting back
# restore_caps is called from the exit trap on EVERY way out, and writes the
# teardown row itself: that row is about the project as you will find it, so it
# belongs to the code that puts it back and not to a step that ran earlier. On a
# PASS there is nothing to put back, and the row says why rather than claiming a
# write that never happened.
RESTORED=no
RESTORE_NEEDED=no
# RAISE_ATTEMPTED is set before the raise runs and CAPS_READ_AFTER when its
# read-back has happened (step_unchanged), and the two together are what a run
# that ended in between -- a Ctrl-C during the raise, a stop in a step after it --
# is caught by: the trap reads the caps back itself, because a raise that landed
# and was never read is the one thing this check must not leave behind.
RAISE_ATTEMPTED=no
CAPS_READ_AFTER=no
restore_caps() {
  [ "$RESTORED" = yes ] && return 0
  RESTORED=yes
  if [ "$RESTORE_NEEDED" != yes ] && [ "$RAISE_ATTEMPTED" = yes ] && [ "$CAPS_READ_AFTER" != yes ]; then
    if read_caps_state trapped && [ "$RC_DAILY" = "$BEFORE_DAILY" ] && [ "$RC_PERRUN" = "$BEFORE_PERRUN" ]; then
      fact teardown PASS "nothing of your project was changed and nothing was put back: the run ended after the raise and before the row that grades it, so this trap read the caps back itself and they are exactly what they were, daily \$$RC_DAILY and per-run \$$RC_PERRUN (fugaro budget show --json --repo $REPO_NAME) — the raise was refused before it was applied, and only a read-back that said they MOVED would have been written back"
      return 0
    fi
    RESTORE_NEEDED=yes
  fi
  if [ "$RESTORE_NEEDED" != yes ]; then
    fact teardown PASS "nothing of your project was changed and nothing was put back: the raise was refused before it was applied, and the unchanged row's own read-back says the caps are exactly what they were"
    return 0
  fi
  local args=(budget set --repo "$REPO_NAME")
  RESET_OWN=no
  if [ "$OWN_DAILY_SET" = yes ] && [ "$OWN_PERRUN_SET" = yes ]; then
    args+=(--daily "$OWN_DAILY" --per-run "$OWN_PERRUN")
  elif [ "$OWN_DAILY_SET" = yes ]; then
    args+=(--daily "$OWN_DAILY")
  elif [ "$OWN_PERRUN_SET" = yes ]; then
    args+=(--per-run "$OWN_PERRUN")
  else
    args+=(--clear)
    RESET_OWN=yes
  fi
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  if [ "$RESET_OWN" = yes ]; then
    say "putting $REPO_NAME's own caps back: ${FUGARO_BIN} ${args[*]} (the node held no cap of its own)"
  else
    say "putting $REPO_NAME's own caps back: ${FUGARO_BIN} ${args[*]}"
  fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/restore.out" 2>"$SCRATCH/restore.err" || status=$?
  if [ "$status" -ne 0 ]; then
    fact teardown FAIL "the repository's own caps were NOT put back: ${FUGARO_BIN} ${args[*]} exited $status ($(last_line "$SCRATCH/restore.err")). This check's raise LANDED, so your project is holding a raised cap. Run this yourself: ${FUGARO_BIN} ${args[*]}"
    return 0
  fi
  # Read back, because a write that printed no error is not a write that landed:
  # this row is the only thing you have to trust about your own caps.
  local check=(budget show --json --repo "$REPO_NAME")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then check+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then check+=(--project "$PROJECT"); fi
  status=0
  "$FUGARO_BIN" "${check[@]}" >"$SCRATCH/restored.json" 2>"$SCRATCH/restored.err" || status=$?
  read_json "$SCRATCH/restored.txt" caps "$SCRATCH/restored.json" "$REPO_NAME" || true
  local daily perrun
  daily=$(field "$SCRATCH/restored.txt" daily)
  perrun=$(field "$SCRATCH/restored.txt" perrun)
  if [ "$(field "$SCRATCH/restored.txt" match)" != "0" ]; then
    fact teardown FAIL "the putting back could not be read: fugaro budget show --json --repo $REPO_NAME (exit $status: $(last_line "$SCRATCH/restored.err")) answered no row for this repository, so this check cannot say whether your caps are back. What it wrote was ${args[*]}. Its output is in $SCRATCH/restored.json (set CHECK382_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  if [ "$RESET_OWN" = yes ]; then
    fact teardown PASS "the caps this check's raise landed on are back: the node held no cap of its own, so this check cleared it, and fugaro budget show --json now reads the defaults again for this repository (daily \$$daily, per-run \$$perrun). It reads rather than trusts, because a putting back that printed no error is not one that landed"
  else
    fact teardown PASS "the caps this check's raise landed on are back: the node held $(own_caps_words), this check wrote them back, and fugaro budget show --json --repo $REPO_NAME now reads daily \$$daily and per-run \$$perrun. It reads rather than trusts, because a putting back that printed no error is not one that landed"
  fi
}

# own_caps_words names what the repository's own node held, in the wording the caps
# row uses, so a row never reads a cap as an empty figure.
own_caps_words() {
  if [ "$OWN_DAILY_SET" = yes ] && [ "$OWN_PERRUN_SET" = yes ]; then
    printf 'daily $%s and per-run $%s' "$OWN_DAILY" "$OWN_PERRUN"
  elif [ "$OWN_DAILY_SET" = yes ]; then
    printf 'a daily of $%s (and no per-run of its own)' "$OWN_DAILY"
  elif [ "$OWN_PERRUN_SET" = yes ]; then
    printf 'a per-run of $%s (and no daily of its own)' "$OWN_PERRUN"
  else
    printf 'no cap of its own'
  fi
}

# --------------------------------------------------------------- the verdict
the_verdict() {
  if [ "$RAISE_TIMED_OUT" = yes ]; then
    fact fence FAIL "the fence on a raise does NOT hold as measured: the raise was NOT refused -- it was still running after ${DEADLINE}s and this check killed it. A refusal is immediate (nothing in budget set waits on a terminal), so a command that hangs here is its own finding, and the marker row says what it printed"
    REACHED_END=yes
    exit 1
  fi
  if [ "$RAISE_STATUS" != 1 ]; then
    fact fence FAIL "the fence on a raise does NOT hold as measured: the raise exited $RAISE_STATUS, not the refusal's own code (1). An exit 0 is the loudest finding here -- a coding agent's session raised a cap, which is exactly what F8 says cannot be a limit"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$MISSING" ]; then
    fact fence FAIL "the fence on a raise does NOT hold as measured: the raise was refused (exit 1), but the sentence is missing $MISSING. That sentence is what an agent hands its owner, so a part it lost is a part the owner cannot act on"
    REACHED_END=yes
    exit 1
  fi
  if [ "${COMMAND_BAD:-no}" = yes ]; then
    fact fence FAIL "the fence on a raise does NOT hold as measured: the refusal's own command is not one the owner can run (the command row says what is missing)"
    REACHED_END=yes
    exit 1
  fi
  if [ "$RESTORE_NEEDED" = yes ]; then
    fact fence FAIL "the fence on a raise does NOT hold as measured: the caps moved, so the raise was applied in a coding agent's session (the unchanged row reads both sides). The teardown puts them back"
    REACHED_END=yes
    exit 1
  fi
  fact fence PASS "the fence on a raise HOLDS on this installation: a \`budget set\` raising this repository's daily cap to \$$RAISE_DAILY was refused after ${RAISE_SECONDS}s with exit 1 while $MARKER was set on its own command line, the sentence carried the act, the marker, 'nothing is applied', 'nothing was changed' and the invocation to run at your own terminal, that command names this repository, this value and your project and uses only flags \`fugaro budget set --help\` lists, and the caps read back exactly as they were. Nothing of your project was changed"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
#
# A key CAN be recorded twice -- the marker row is written once, but a step that
# re-reads what it already recorded (the command row after the help row) supersedes
# it -- and the block carries the LAST one, because that is the one the run stood
# behind when it ended.
BLOCK_KEYS=(xtrace agent-session project belong checkout caps raise marker refusal help command unchanged fence teardown)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=""
  printf '\nCHECK 382 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep "^$key|" | tail -n 1 || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed $key"
  done
  # The space at each end is what makes the membership test work: without it the
  # LAST key of the fixed list would match nothing and be printed twice.
  printed=" $printed "
  while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case "$printed" in *" $key "*) continue ;; esac
    printed="$printed $key "
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done <<<"$facts"
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    printf 'FACT scratch-removed: FAIL — the scratch directory is STILL THERE at %s -- remove it yourself\n' "$SCRATCH" >&3
  else
    printf 'FACT scratch-removed: PASS — the scratch directory is gone, and nothing of this check is left behind\n' >&3
  fi
  printf 'CHECK 382 RESULT END (exit %s)\n' "$status" >&3
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
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see, and a block that then
  # read as a finished run would offer the rows that read "not reached" as an
  # answer about the fence when the read that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the fence"
    fi
    status=2
  fi
  # A row that reads FAIL can never end in a pass. The verdict sets its own code,
  # but the putting back runs from this trap AFTER it -- so a verdict of "it holds"
  # whose caps did not go back is turned into a non-zero exit here, which is the one
  # reading an owner must not have to look for.
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    case "$verdict" in
    FAIL) failed=yes ;;
    esac
  done <<<"$facts"
  if [ "$status" = 0 ] && [ "$failed" = yes ]; then
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: putting your caps back where the raise landed, printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads] ${FUGARO_BIN} doctor --json${FUGARO_FLAGS[*]:+ ${FUGARO_FLAGS[*]}}${PROJECT:+ --project $PROJECT}: which installation the config in force names, held to --project where you typed one, with a project named belong refused here and in what doctor answers"
  say "  [reads] the checkout ${CHECKOUT}'s origin remote, which names the repository whose cap the raise below is about"
  say "  [reads] ${FUGARO_BIN} budget show --json --repo <that repository> and ${FUGARO_BIN} project show --json: the caps node a raise would move, which is what gets put back if the raise lands"
  say "  [runs] cd ${CHECKOUT} && CLAUDECODE=1 ${FUGARO_BIN} budget set --repo <that repository> --daily <more than it holds now>, its stdin on /dev/null, under a ${DEADLINE}s deadline: THE COMMAND THIS CHECK MEASURES. The fence refuses it before anything is applied, so nothing is written"
  say "  [reads the refusal] exit 1, and one sentence carrying the act, the marker, 'nothing is applied', 'nothing was changed' and the invocation to run at your own terminal"
  say "  [reads the command] that the invocation names this repository, this value and your project, starts with ${FUGARO_BIN} and uses only flags ${FUGARO_BIN} budget set --help lists"
  say "  [reads back] ${FUGARO_BIN} budget show --json --repo <that repository>, held to the caps read before: nothing may have moved. Only if it did is anything written back, from the exit trap, with the result read back afterwards"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  CHECKOUT_ARG=${CHECK382_CHECKOUT:-.}
  PROJECT=${CHECK382_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK382_DRY_RUN:-}
  DEADLINE=${CHECK382_DEADLINE:-300}
  while [ $# -gt 0 ]; do
    case "$1" in
    --checkout)
      [ $# -ge 2 ] || die "--checkout needs a path. usage: $USAGE"
      CHECKOUT_ARG=$2
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
      printf '\nRuns ONE `%s budget set` that RAISES this checkout\x27s repository\x27s daily\ncap, with CLAUDECODE=1 set for that child alone, and reads the refusal: the\nact in the fence\x27s own words, the marker, that nothing was applied, and the\ncommand to run at your own terminal. It then reads the caps back to say whether\nanything moved, and puts them back if it did. It prints one block headed\nCHECK 382 RESULT and exits 0 when the fence held, 1 when it did not, and 2 when\nthis run could not tell. It is yours to run: no agent runs any step of it.\n' "$FUGARO_BIN"
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

dry_rows() { # dry_rows ORIGIN REPO
  local origin=$1 dry_repo=$2
  if [ -n "$PROJECT" ]; then
    guard_belong "$PROJECT" "the project you typed"
    fact belong PASS "the project name you typed ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
  else
    fact belong INFO "no --project was typed, so the guard had nothing to judge: a real run refuses an installation named belong in what doctor answers too, before anything is run"
  fi
  fact project INFO "a dry run reads nothing: a real run asks fugaro doctor --json which installation the config in force names, holds it to the --project you typed where you typed one, and refuses an installation named belong in that answer too"
  if [ -n "$dry_repo" ]; then
    fact checkout INFO "planned: a real run reads the origin remote of $CHECKOUT ($origin) as $dry_repo, and raises THAT repository's cap by one cent"
  else
    fact checkout INFO "planned: a real run reads the origin remote of $CHECKOUT and refuses if it does not name a repository; this dry run could not read it as one"
  fi
  fact caps INFO "planned: a real run reads the repository's own caps node out of ${FUGARO_BIN} project show --json and its effective caps out of ${FUGARO_BIN} budget show --json, and refuses to raise anything at all when it cannot read the node, because a cap it cannot put back is a change to your project"
  fact raise INFO "planned: a real run raises that node's daily cap by one cent above everything it holds of its own, so the call is a raise and never a no-op"
  fact marker INFO "planned: a real run sets CLAUDECODE=1 on that child's own command line and nowhere else, so the refusal it reads is the fence's and not this session's"
  fact refusal INFO "planned: a real run reads the whitespace-collapsed text of both streams for the parts of the refusal and FAILs naming any part that is missing"
  fact help INFO "planned: a real run asks ${FUGARO_BIN} budget set --help, so the printed command is judged against the flags this CLI really takes"
  fact command INFO "planned: a real run checks the printed command names this repository, this value and your project, starts with ${FUGARO_BIN}, and carries no flag --help does not list"
  fact unchanged INFO "planned: a real run reads the caps back and FAILs if they moved -- a raise that landed is the finding this check exists for"
  fact fence INFO "planned: the verdict is a real run's; a dry run measured nothing"
  fact teardown INFO "planned: a real run writes nothing back unless the raise landed, and reads back what it wrote"
  fact dry-run PASS "the plan was printed and nothing was run: no cap was raised, no command was run and nothing was written outside the scratch directory"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  guard_agent_session
  write_reader
  # The checkout is resolved before the dry run too: the plan names the repository
  # the raise would be about, and a plan that could not say which one would be a
  # plan for some other repository's caps.
  CHECKOUT=$(cd "$CHECKOUT_ARG" 2>/dev/null && pwd) || stop "--checkout '$CHECKOUT_ARG' is not a directory this check can read"
  REPO_NAME=""
  if [ -n "$DRY_RUN" ]; then
    local origin dry_repo
    origin=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null || true)
    dry_repo=""
    if [ -n "$origin" ]; then
      dry_repo=$(printf '%s' "$origin" | python3 "$SCRATCH/read.py" origin /dev/stdin 2>/dev/null | sed -n 's/^repo=//p' | head -1) || true
    fi
    BLOCK_KEYS=(xtrace agent-session project belong checkout caps raise marker refusal help command unchanged fence teardown dry-run)
    dry_rows "$origin" "$dry_repo"
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  step_checkout
  step_caps
  step_raise_value
  step_help
  step_raise
  step_refusal
  step_command
  step_unchanged
  the_verdict
}

FUGARO_BIN=${CHECK382_FUGARO:-fugaro}
MARKER=${CHECK382_MARKER:-CLAUDECODE}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
CHECKOUT=""
CHECKOUT_ARG=.
REPO_NAME=""
DEADLINE=300
CAPS_MODE=""
CAPS_DAILY_SOURCE=""
CAPS_PERRUN_SOURCE=""
CAPS_COUNTED=""
NODE_STATE=""
BEFORE_DAILY=""
BEFORE_PERRUN=""
AFTER_DAILY=""
AFTER_PERRUN=""
OWN_DAILY=""
OWN_PERRUN=""
OWN_DAILY_SET=no
OWN_PERRUN_SET=no
RAISE_DAILY=""
RAISE_ARGS=()
RAISE_LAST=""
RAISE_LAST_FROM=stderr
RESET_OWN=no
COMMAND_BAD=no

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
