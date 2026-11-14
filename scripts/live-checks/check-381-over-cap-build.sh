#!/usr/bin/env bash
# Check 381 — an image build over the cap is refused, with the estimate, the cap,
# the day's figure and the one command that raises it, as ONE script YOU run.
#
# The no-prompt policy removed the typed confirmation from `fugaro image build`
# (T7): a Cloud Build is billable and nothing is typed in front of it any more.
# What stands in front of it instead is the build lease (T8, D1543): the build's
# cost is estimated from the Cloud Billing catalog and judged against the
# project's caps BEFORE the submission, and a build no cap has room for is
# refused, non-zero, with the four things an operator needs — what it reserves,
# the cap it met, the figure that cap compared, and the one command that raises
# it (BuildOverCapError). That refusal is the whole of what replaced the
# confirmation, so a release that lost it would have replaced a question with
# nothing at all. This check reads it on a real installation.
#
# What it reads, in this order:
#
#   1. The installation: `fugaro doctor --json`'s project, held to what you typed
#      when you typed `--project`, so the block says WHICH installation was
#      measured and the `belong` guard can refuse the owner's own before
#      anything is touched.
#   2. The checkout: its origin remote's repository. The build runs from here and
#      is charged to this repository, and it is this repository's own cap the
#      check lowers. Nothing is taken from a flag, so the cap and the build
#      cannot end up on two different repositories.
#   3. The budget mode (`fugaro budget show --json`). A cap only REFUSES a build
#      in `enforce` (D1548): in observe the over-cap decision comes back as an
#      advisory and the build is submitted anyway, which is money. So a mode that
#      is not enforce is a row this check does not measure, and a refusal to run
#      that names the one command — the mode is your own choice to make, never
#      this check's to set.
#   4. The repository's own caps node, out of `fugaro project show --json` (the
#      raw nodes), plus the effective caps and where each comes from
#      (`budget show`). What the node holds is what the check puts back. A node
#      it cannot read is a refusal to run: a cap this check cannot put back is a
#      change to your project, not a reading.
#   5. The over-cap state, made by TIGHTENING: the repository's own caps are
#      lowered to $0.00. Lowering a cap is no raise (D1542), so this is one
#      `budget set` and nothing is asked — and since a mid-air project is not
#      worth a reading, the lowering is read back before anything is built. The
#      per-run cap goes to $0.00 too where the repository keeps one of its own,
#      because a node's per-run may not sit above its daily: that is the state
#      the CLI itself refuses to leave behind. Which of the two caps then refuses
#      the build is the node's own business, and the rows say which.
#   6. **The build, which is the fact this check is**: `fugaro image build
#      --no-wait` from the checkout, its stdin on /dev/null, under a deadline.
#      --no-wait is there because the refusal comes before the submission: a
#      build that WAS submitted must not hold this check open, and the row says
#      so rather than pretending otherwise. On a healthy installation nothing is
#      built at all: the lease refuses first.
#   7. The refusal itself: exit 1, and one sentence carrying the estimate and
#      where its price came from ("reserves $X at most (the N s build timeout at
#      the machine's rate, read from the Cloud Billing catalog)"), the cap it met
#      ($0.00 — the one this check just set), the figure that cap compared (the
#      day's counted figure for the repository's daily cap, what the build holds
#      for its per-run one) and what the build needed, and the one command that
#      raises that cap. A refusal missing any of them is a FAIL: an operator
#      handed half that sentence cannot act on it.
#
# **What a PASS means, and what it does not.** A PASS means the build was refused
# before it was submitted, with all four parts, and nothing was built. It does
# NOT mean your installation is well-off: a FAIL row names the part that was
# missing, or says the build was NOT refused — in which case it may have been
# submitted, and the build row carries the transcript's own last line.
#
# **What it does to your project**: it lowers ONE repository's own caps for the
# few seconds the build takes and puts them back from the exit trap, on every way
# out, reading them back afterwards to say so. No image is pushed, no run is
# launched, and no lease is taken for a build that is refused — the day counts
# nothing for it.
#
# Usage:      bash scripts/live-checks/check-381-over-cap-build.sh [--checkout <path>]
#                                             [--project <fugaro project>]
#                                             [--fugaro-flags '<flags>'] [--deadline <seconds>]
#                                             [--dry-run]
#             --checkout is the checkout to build from (default `.`). The build
#             runs from it and its origin remote is the repository whose caps are
#             lowered.
#             --project is the Fugaro project. Leave it out where a project
#             config is in force: `fugaro doctor --json` answers with it and this
#             check holds the two to each other.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on whitespace,
#             so a value with a space in it cannot be passed.
#             --deadline is how long the build command may take, in seconds
#             (default 900; CHECK381_DEADLINE does the same). The refusal is
#             immediate; the bound is for a command that hangs or that really did
#             submit a build.
#             --dry-run prints the plan and makes no call at all: it lowers no
#             cap and starts no build.
# Needs:      bash, mktemp, git, grep, sed, head, tail, cut, awk, python3, and a
#             `fugaro` on PATH with `doctor`, `project show`, `budget show`,
#             `budget set` and `image build`. Nothing is installed and nothing is
#             fetched; a missing tool stops the run with one line naming it.
# Where:      your own machine, in the checkout of a SANDBOX repository whose
#             project is NOT `belong` — that name is refused before anything is
#             run, in what you typed and in what doctor answers. A coding agent's
#             session is refused too: this one lowers a real cap, so it is yours
#             to run at your own terminal.
# Costs:      `fugaro doctor --json`, `fugaro project show --json`, two or three
#             `fugaro budget show --json`, two `fugaro budget set` (the lowering
#             and the putting back) and one `fugaro image build --no-wait` that
#             is refused before it is submitted. No model call, no image and no
#             Cloud Build, unless the build row says a submission happened.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 381 RESULT.
#             Exit 0 when the claim held (the build was refused, non-zero, with
#             all four parts), 1 when it did not, and 2 when this run could not
#             tell (a guard stopped it, the mode is not enforce, the caps could
#             not be read, a tool is missing).
#
# CHECK381_FUGARO names the binary (default `fugaro`), CHECK381_DEADLINE the
# default deadline, CHECK381_DRY_RUN makes every run a dry one and
# CHECK381_KEEP_SCRATCH keeps the scratch directory (with every call's own
# output) instead of removing it, so this script's own test can drive the same
# code and you can read what a failing run printed.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them would end the run with no fact recorded at
# all, and the block would then report the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had: a shell that arrived
# tracing prints every line of this run into the log you would then paste back,
# and this check reads your project's caps and writes one of them. So the trace
# is turned off here, BASH_XTRACEFD is unset so a trace redirected to a file this
# script knows nothing about cannot be reopened, and guard_xtrace refuses a shell
# that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then, and every child
# would start tracing on its own).
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# Your own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
HELPED=no
DRY_RUN=""
USAGE="check-381-over-cap-build.sh [--checkout <path>] [--project <fugaro project>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap puts the caps
# back, prints the block and removes the scratch directory, so a stop still gives
# you something to paste back, and 2 says this run could not tell rather than that
# the claim does not hold.
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
# and every caller stops the run after it, because what comes next would write to
# the project; the over-cap row is filled in here rather than left to read "not
# reached", so the block's own headline says why this run has no answer.
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  fact overcap INFO "the over-cap refusal is NOT MEASURED by this run: the $1 row above could not be settled, so nothing here is an answer about whether a build over the cap is refused"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the first
# command that fails outside a guard would end the run with no fact at all, and
# the block would then read as an answer about the refusal for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the over-cap refusal"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 381 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 381 needs bash"
  need mktemp
  need git "the checkout's origin remote is how this check knows which repository's caps it lowers"
  need grep
  need sed
  need head
  need tail
  need cut
  need python3 "the doctor, project, budget and build output are read with it"
  need "$FUGARO_BIN" "every read and the build itself are this program's"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk381-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK381_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk381.XXXXXXXX") || die "Check 381 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 381 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: restore_caps records what it
# managed to do in the facts captured below, and a `$(cleanup)` would throw that
# answer away with the subshell. The caps go back FIRST, so the teardown row the
# block carries is about the project as you will find it.
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
    say "CHECK381_KEEP_SCRATCH is set: $SCRATCH is kept (every call's own output is in it)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing prints every line of this run into
# the log an owner pastes back, and this run reads the project's caps and writes
# one of them. A tracing shell is refused rather than trusted.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check writes to your project's budget and prints the block you would paste back. Run it without -x"
    stop "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check writes to your project's budget and prints the block you would paste back. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: no trace of this run can copy the caps it read or the block it prints"
}

# guard_agent_session: this check is the owner's, and the write it makes is one an
# agent's session must not make. Refusing here rather than letting `budget set`
# refuse the agent's way is what makes the block say why: a session that ran this
# far would report an agent's own refusal as a finding about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "$marker is set, so this is a coding agent's session: Check 381 lowers a real cap on a real project and is yours to run at your own terminal, on the sandbox installation. No agent runs any step of it"
    stop "$marker is set, so this is a coding agent's session: Check 381 lowers a real cap on a real project and is yours to run at your own terminal, on the sandbox installation. No agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — the cap it lowers is yours to lower"
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
guard_belong() { # guard_belong NAME WHERE
  if is_belong "$1"; then
    fact belong FAIL "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would lower the owner's own caps"
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would lower the owner's own caps"
  fi
}

# ------------------------------------------------------------------- the jsons
# The four documents and the build's transcript, in one program: a live check on
# the owner's machine has python3 and not necessarily jq, and a row that read a
# cap with grep would read a number that is not there as "not set". Every query
# prints `key=value` lines, so the shell holds them without a second parser.
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
    """The repository's OWN caps node, out of `project show --json`: the raw
    nodes (caps.repos), matched by the repository the node names or by the key.
    A number is the node's own value; null is a node that does not set that cap;
    an absent node is no node at all, which is a different thing."""
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
    """`budget show --json`: the mode, the document's global and default caps, and
    this repository's own row -- the effective caps in force, where each comes
    from, and what the day counts for it."""
    doc = load(path)
    out = [("mode", doc.get("mode") or "")]
    mr = doc.get("max_reserve_usd")
    out.append(("max_reserve", money(mr) if mr is not None else ""))
    defaults = doc.get("defaults") or {}
    global_ = doc.get("global") or {}
    for key, src in (("default_daily", defaults.get("repo_daily_cap_usd")),
                     ("default_perrun", defaults.get("repo_per_run_cap_usd")),
                     ("global_daily", global_.get("daily_cap_usd")),
                     ("global_perrun", global_.get("per_run_cap_usd"))):
        out.append((key, money(src) if src is not None else ""))
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


def flat(paths):
    """The text of every file, whitespace collapsed: a refusal is one long
    sentence a terminal wraps, and this check looks for its parts in the text
    rather than for where the line breaks happened to fall."""
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
# thing it printed, and for a refused build that is the refusal itself.
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
    unmeasured project "fugaro doctor --json did not answer about a project (exit $status: $(last_line "$SCRATCH/doctor.err")), and no --project was typed, so this check cannot tell which installation it is about to lower a cap on. Run it where a project config is in force, or pass --project <name>"
    stop "fugaro doctor --json named no project and no --project was typed: this check cannot tell which installation's caps it would lower"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    stop "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
  fi
  fact project PASS "the local config in force is project $PROJECT_NAME${PROJECT_GCP:+ on GCP project $PROJECT_GCP} (fugaro doctor --json): the build below is that installation's"
}

# The checkout is where the build runs and whose repository it is charged to, so
# it is read from the disk rather than asked of the CLI: the origin remote is what
# `fugaro image build` itself resolves, and a check that took a name from
# elsewhere could lower one repository's caps and build another's.
step_checkout() {
  if [ ! -d "$CHECKOUT" ]; then
    fact checkout FAIL "--checkout '$CHECKOUT' is not a directory, so there is no checkout to build from and no origin to read"
    stop "--checkout '$CHECKOUT' is not a directory"
  fi
  if ! git -C "$CHECKOUT" rev-parse --show-toplevel >/dev/null 2>&1; then
    fact checkout FAIL "$CHECKOUT is not a git work tree, and the repository this check lowers the caps of is the one its origin remote names"
    stop "$CHECKOUT is not a git work tree"
  fi
  local url status=0
  url=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) || status=$?
  if [ "$status" -ne 0 ] || [ -z "$url" ]; then
    fact checkout FAIL "$CHECKOUT has no 'origin' remote, so this check cannot name the repository the build is charged to (fugaro image build resolves it from the same remote). Run it in the checkout of a SANDBOX repository whose origin names it"
    stop "$CHECKOUT has no origin remote that names a repository"
  fi
  REPO_NAME=$(python3 "$SCRATCH/read.py" origin "$url" 2>/dev/null | sed -n 's/^repo=//p' | head -1) || true
  if [ -z "$REPO_NAME" ]; then
    fact checkout FAIL "the origin remote of $CHECKOUT ($url) does not name a repository this check can read as owner/name"
    stop "the origin remote of $CHECKOUT does not name a repository this check can read"
  fi
  fact checkout PASS "the checkout $CHECKOUT is a git work tree whose origin names $REPO_NAME: the build runs from here and is charged to this repository, whose own caps are lowered below"
}

# ------------------------------------------------------------------- the caps
# read_caps reads the mode, the effective caps and the repository's own caps
# node. All of it is recorded as rows whether it is what the check needs or not:
# the block is where a reader looks for what this project held before the build.
read_caps() {
  local args=(budget show --json --repo "$REPO_NAME")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err" || status=$?
  read_json "$SCRATCH/budget.txt" caps "$SCRATCH/budget.json" "$REPO_NAME" || true
  if [ "$(field "$SCRATCH/budget.txt" match)" != "0" ]; then
    unmeasured caps "fugaro budget show --json --repo $REPO_NAME did not answer with this repository's row (exit $status: $(last_line "$SCRATCH/budget.err")), so this check cannot see the caps it would lower or the mode they are judged in. Its output is in $SCRATCH/budget.json (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    stop "fugaro budget show --json --repo $REPO_NAME answered no row for it"
  fi
  CAPS_MODE=$(field "$SCRATCH/budget.txt" mode)
  CAPS_DAILY_SOURCE=$(field "$SCRATCH/budget.txt" daily_source)
  CAPS_PERRUN_SOURCE=$(field "$SCRATCH/budget.txt" perrun_source)
  CAPS_COUNTED=$(field "$SCRATCH/budget.txt" counted)
  CAPS_MAX_RESERVE=$(field "$SCRATCH/budget.txt" max_reserve)
  CAPS_DAILY=$(field "$SCRATCH/budget.txt" daily)
  CAPS_PERRUN=$(field "$SCRATCH/budget.txt" perrun)

  # The caps node's own fields, out of `project show --json` (the raw nodes).
  local pargs=(project show --json)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then pargs+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then pargs+=(--project "$PROJECT"); fi
  status=0
  "$FUGARO_BIN" "${pargs[@]}" >"$SCRATCH/project.json" 2>"$SCRATCH/project.err" || status=$?
  read_json "$SCRATCH/node.txt" node "$SCRATCH/project.json" "$REPO_NAME" || true
  local node
  node=$(field "$SCRATCH/node.txt" node)
  OWN_DAILY=""
  OWN_PERRUN=""
  OWN_DAILY_SET=no
  OWN_PERRUN_SET=no
  case "$node" in
  found)
    OWN_DAILY=$(field "$SCRATCH/node.txt" daily)
    OWN_PERRUN=$(field "$SCRATCH/node.txt" perrun)
    [ -n "$OWN_DAILY" ] && OWN_DAILY_SET=yes
    [ -n "$OWN_PERRUN" ] && OWN_PERRUN_SET=yes
    ;;
  absent)
    # No node of this repository's own. That is only safe to write to because
    # `budget set --clear` then puts back exactly nothing -- but it must be TRUE:
    # a source of "repo" on either cap says a node exists that this check could
    # not find, and clearing one that exists is a change, not a putting back.
    if [ "$CAPS_DAILY_SOURCE" = repo ] || [ "$CAPS_PERRUN_SOURCE" = repo ]; then
      unmeasured caps "the repository's own caps node holds a cap this check cannot read: fugaro budget show --json says its daily comes from the repository ($CAPS_DAILY_SOURCE) and its per-run from $CAPS_PERRUN_SOURCE, while fugaro project show --json lists no caps node naming $REPO_NAME ($(last_line "$SCRATCH/project.err")). This check will not write a cap it cannot put back. Its two documents are in $SCRATCH/project.json and $SCRATCH/budget.json (set CHECK381_KEEP_SCRATCH=1 to keep them)"
      stop "the repository's own caps node could not be read, and this check will not write a cap it cannot put back"
    fi
    ;;
  *)
    unmeasured caps "fugaro project show --json did not answer (exit $status: $(last_line "$SCRATCH/project.err")), so this check cannot read the repository's own caps node and cannot promise to put back what it would write. The command's own output is in $SCRATCH/project.json (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    stop "fugaro project show --json did not answer: the repository's own caps node could not be read"
    ;;
  esac

  if [ "$CAPS_MODE" != enforce ]; then
    unmeasured mode "the project's budget mode is '${CAPS_MODE:-unset}' (fugaro budget show --json), and a cap only REFUSES a build in enforce (D1548): in observe the over-cap decision comes back as an advisory and the build is submitted anyway, so a build here would spend money rather than be refused, and this run would measure nothing. Raise it yourself with: ${FUGARO_BIN} budget set --global --mode enforce${PROJECT:+ --project $PROJECT}${FUGARO_FLAGS[*]:+ ${FUGARO_FLAGS[*]}}. This check does not set the mode: that write records your own choice, and fugaro update is what raises it on a project whose owner has not chosen one"
    stop "the project's budget mode is '${CAPS_MODE:-unset}', not enforce: a build would be submitted rather than refused"
  fi
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
  fact mode PASS "the project's budget mode is enforce (fugaro budget show --json): a cap the build meets REFUSES it rather than advising (D1548). A build's reservation is taken before its submission, so the refusal below comes before anything is built"
  fact caps PASS "the repository $REPO_NAME's own caps node holds $node_words, and the effective caps in force are daily \$$CAPS_DAILY (from ${CAPS_DAILY_SOURCE:-none}) and per-run \$$CAPS_PERRUN (from ${CAPS_PERRUN_SOURCE:-none}), with \$$CAPS_COUNTED counted today and a max reserve of \$${CAPS_MAX_RESERVE:-unset} (fugaro budget show --json). The write below lowers this node to \$0.00 and the teardown row says what goes back"
}

# step_write lowers the node to $0.00: a tightening, which asks nothing and is no
# raise (D1542), so it is one call and no question anywhere. The per-run cap is
# lowered with the daily where the node keeps one of its own, because a node whose
# per-run sits above its daily is a state `budget set` itself refuses to leave.
step_write() {
  WRITE_ARGS=(--daily 0)
  RESTORE_ARGS=()
  RESTORE_CLEAR=no
  if [ "$OWN_PERRUN_SET" = yes ]; then
    WRITE_ARGS+=(--per-run 0)
    if [ "$OWN_DAILY_SET" = yes ]; then
      RESTORE_ARGS=(--daily "$OWN_DAILY" --per-run "$OWN_PERRUN")
    else
      RESTORE_CLEAR=yes
      RESTORE_ARGS=(--per-run "$OWN_PERRUN")
    fi
  else
    if [ "$OWN_DAILY_SET" = yes ]; then
      RESTORE_ARGS=(--daily "$OWN_DAILY")
    else
      RESTORE_CLEAR=yes
    fi
  fi
  local args=(budget set --repo "$REPO_NAME" "${WRITE_ARGS[@]}")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  say "lowering $REPO_NAME's own caps to \$0.00 for the build below: ${FUGARO_BIN} ${args[*]}"
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/set.out" 2>"$SCRATCH/set.err" || status=$?
  # A write was ATTEMPTED from here on, failed or not: a `budget set` that exited
  # non-zero may still have applied part of what it was handed, so the putting
  # back runs either way. WRITE_LANDED is separate, because a putting back that
  # then fails has to say whether there was anything to put back.
  WRITE_DONE=yes
  if [ "$status" -ne 0 ]; then
    unmeasured write "fugaro budget set --repo $REPO_NAME ${WRITE_ARGS[*]} exited $status: $(last_line "$SCRATCH/set.err"). The over-cap state this check measures was not made, so no build was run. The lowering may still have applied part of what it was handed, so the exit trap tries to put the caps back anyway and the teardown row says what that did"
    stop "the lowering write exited $status"
  fi
  WRITE_LANDED=yes
  fact write PASS "the repository's own caps were lowered to \$0.00 (fugaro budget set --repo $REPO_NAME ${WRITE_ARGS[*]}: $(last_line "$SCRATCH/set.out")), which is a tightening and asked nothing. The teardown row carries the values it puts back"
}

# verify_write reads the lowering back before the build: a write that did not land
# would leave the next step submitting a real build.
verify_write() {
  local args=(budget show --json --repo "$REPO_NAME")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/after.json" 2>"$SCRATCH/after.err" || status=$?
  read_json "$SCRATCH/after.txt" caps "$SCRATCH/after.json" "$REPO_NAME" || true
  local daily after_perrun
  daily=$(field "$SCRATCH/after.txt" daily)
  after_perrun=$(field "$SCRATCH/after.txt" perrun)
  if [ "$(field "$SCRATCH/after.txt" match)" != "0" ] || [ "$daily" != 0 ]; then
    WRITE_LANDED=no
    unmeasured write "the lowering did not read back: fugaro budget show --json --repo $REPO_NAME (exit $status) says this repository's daily cap is \$$daily and its per-run \$$after_perrun, where this check had just set the daily to \$0.00. The build below was NOT run, because running it would have submitted a real build. Nothing else was changed"
    stop "the lowering write did not read back as the repository's daily cap of \$0.00"
  fi
  fact write PASS "the lowering read back: fugaro budget show --json --repo $REPO_NAME now says this repository's daily cap is \$0.00 (per-run \$$after_perrun), so a build of this repository cannot be submitted while this check runs, and the build below can only be refused"
}

# --------------------------------------------------------------------- the build
# run_build SECONDS KEY CMD...: run one command from the checkout with its stdin
# on /dev/null and its two streams in the scratch directory, and end it at the
# deadline. The child execs, so the pid the deadline kills IS the command; the ERR
# trap is dropped in it, because this check reads the build's failing exit status
# as one of its own readings and a trap inherited there would turn it into an
# internal error before the row could report it.
BUILD_STATUS=0
BUILD_TIMED_OUT=no
BUILD_SECONDS=0
run_build() { # run_build SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BUILD_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  (cd "$CHECKOUT" && trap - ERR; exec "$@") </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      BUILD_TIMED_OUT=yes
      say "the build command is still going after ${secs}s: killing it (TERM, then KILL)"
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
  BUILD_STATUS=0
  wait "$pid" 2>/dev/null || BUILD_STATUS=$?
  BUILD_SECONDS=$((SECONDS - started))
}

step_build() {
  # No --repo: `fugaro image build` resolves the repository from this checkout's
  # own origin, which is the one whose caps were just lowered. A --repo spelled
  # even slightly differently would be refused as another checkout's repository,
  # and this check would then be reading that refusal instead.
  local args=(image build --no-wait)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  say "running: cd $CHECKOUT && ${FUGARO_BIN} ${args[*]} < /dev/null  (deadline ${DEADLINE}s; the refusal comes before the submission)"
  run_build "$DEADLINE" build "$FUGARO_BIN" "${args[@]}"
  BUILD_LAST=$(last_line "$SCRATCH/build.err")
  BUILD_LAST_FROM=stderr
  if [ -z "$BUILD_LAST" ]; then
    BUILD_LAST=$(last_line "$SCRATCH/build.out")
    BUILD_LAST_FROM=stdout
  fi
  if [ "$BUILD_TIMED_OUT" = yes ]; then
    fact build FAIL "fugaro image build was still running after ${DEADLINE}s and this check killed it (TERM, then KILL). Nothing in this command waits on a terminal any more, so a hang here is its own finding; a build that was NOT refused may also have been submitted and still be running in Cloud Build. Its last line ($BUILD_LAST_FROM) was: ${BUILD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/build.out and build.err (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  case "$BUILD_STATUS" in
  1)
    fact build PASS "fugaro image build ended after ${BUILD_SECONDS}s with exit 1, the code the refusal of a build no cap has room for carries, and nothing of this build was submitted. Its last line ($BUILD_LAST_FROM): ${BUILD_LAST:-<nothing>}"
    ;;
  0)
    fact build FAIL "fugaro image build ended after ${BUILD_SECONDS}s with exit 0, so it was NOT refused: a build of this repository was submitted (--no-wait means this check did not wait for it) while its daily cap read \$0.00. Money may be being spent right now. Two states let a build through with the cap at \$0.00 -- an installation with no budget backend for the lease to write into, and a price the Cloud Billing catalog could not answer -- and the transcript says which of them this was. Its last line ($BUILD_LAST_FROM): ${BUILD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/build.out and build.err (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    ;;
  *)
    fact build FAIL "fugaro image build ended after ${BUILD_SECONDS}s with exit $BUILD_STATUS, which is not the refusal's own code (1): the over-cap refusal was never reached, because something before it refused or failed. Its last line ($BUILD_LAST_FROM): ${BUILD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/build.out and build.err (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    ;;
  esac
}

# --------------------------------------------------------------- the refusal
# The parts the refusal has to carry, in the words the error itself uses
# (BuildOverCapError.Error and the predicate's own detail). Two of the caps a
# build can meet are this check's own doing, and which one refuses is the
# predicate's business, not this check's: the per-run cap where this repository
# keeps one of its own (a build holds nothing of its run, so a per-run cap of
# $0.00 is met by any reservation at all) and the repository's daily cap
# otherwise. The rest is shared, and so is required of every refusal; the
# compared figure is required in whichever form the sentence took, read from the
# detail that names the cap rather than from the flags of the command. The parts
# are looked for in the whitespace-collapsed text of BOTH streams: where a
# terminal wraps that sentence is not something a check may depend on.
BUILD_FLAT=""
MISSING=""
SHAPE=""
REFUSAL_SHAPE=""
require() { # require "part" -- records a part the refusal is missing
  local part=${1# }
  part=${part% }
  case "$BUILD_FLAT" in
  *"$1"*) ;;
  *) MISSING="${MISSING:+$MISSING; }$part" ;;
  esac
}

step_refusal() {
  if [ "$BUILD_TIMED_OUT" = yes ] || [ "$BUILD_STATUS" != 1 ]; then
    BUILD_FLAT=""
    unmeasured refusal "there is no refusal to read: the build row says the command exited $BUILD_STATUS (timed out: $BUILD_TIMED_OUT). This check only reads the sentence of an exit 1"
    return 0
  fi
  read_json "$SCRATCH/flat.txt" flat "$SCRATCH/build.err" "$SCRATCH/build.out" || true
  BUILD_FLAT=$(cat "$SCRATCH/flat.txt" 2>/dev/null || true)
  MISSING=""
  SHAPE=""
  require "reserves \$"
  require " at most (the "
  require " s build timeout at the machine's rate, read from the Cloud Billing catalog), and "
  require "cap \$0.00 reached ("
  require " needed)"
  require "raise the cap with \`${FUGARO_BIN##*/} budget set "
  # Which cap refused the build is read from the predicate's own detail, and not
  # from the command: a command is a flag string, and `--global --daily <usd>`
  # also ends in `--daily <usd>`. The detail names the cap, so a refusal whose
  # detail is about a cap this check did NOT set is a finding of its own rather
  # than a shape to be bent into one of the two below.
  case "$BUILD_FLAT" in
  *"this repository's daily cap \$0.00 reached ("*) SHAPE=daily ;;
  *"run cap \$0.00 reached ("*) SHAPE=run ;;
  esac
  case "$SHAPE" in
  daily)
    require " counted, "
    require " counted today by this repository's jobs and its settled builds"
    require " --daily <usd>\`"
    ;;
  run)
    require " held, "
    require " --per-run <usd>\`"
    ;;
  *)
    MISSING="${MISSING:+$MISSING; }the cap this check set (\$0.00 on this repository's own daily or per-run cap) named as the one the build met, with the one command that raises THAT cap"
    ;;
  esac
  REFUSAL_SHAPE=$SHAPE
  if [ -n "$MISSING" ]; then
    fact refusal FAIL "the refusal is missing: $MISSING. An operator handed a sentence without them cannot act on it, and this sentence is the whole of what replaced the confirmation in front of a billable build (T7). What the command printed: ${BUILD_FLAT:-<nothing>}"
    return 0
  fi
  if [ "$SHAPE" = daily ]; then
    fact refusal PASS "the refusal carries all four: what the build reserves at most and that its price came from the Cloud Billing catalog, the repository's daily cap it met (the \$0.00 this check set), the day's counted figure and what the build needed, and the one command that raises that cap. What the command printed: $BUILD_FLAT"
  else
    fact refusal PASS "the refusal carries all four: what the build reserves at most and that its price came from the Cloud Billing catalog, the per-run cap it met (the \$0.00 this check set on this repository's own per-run cap), what the build holds and what it needed, and the one command that raises that cap. This repository keeps a per-run cap of its own and a build holds nothing of its run, so that is the cap any reservation meets first; the daily cap this check lowered is met by the same build either way. What the command printed: $BUILD_FLAT"
  fi
}

# ---------------------------------------------------------------- putting back
# restore_caps is called from the exit trap on EVERY way out, so the caps are back
# whatever ended the run, and it writes the teardown row itself: that row is about
# the project as you will find it, so it belongs to the code that puts it back and
# not to a step that ran earlier.
RESTORED=no
restore_caps() {
  [ "$RESTORED" = yes ] && return 0
  RESTORED=yes
  if [ "$WRITE_DONE" != yes ]; then
    fact teardown PASS "nothing of your project was changed: this run lowered no cap (it stopped before the write, or it was a dry run), so there is nothing to put back"
    return 0
  fi
  if [ "$WRITE_LANDED" = yes ]; then
    LANDED_NOTE="Your project is holding the \$0.00 this check wrote."
  else
    LANDED_NOTE="The lowering this check ran had already failed on its own (the write row above says so), so it may have written nothing at all: check with \`${FUGARO_BIN} budget show --repo $REPO_NAME\` before you run the command below."
  fi
  if [ "$RESTORE_CLEAR" = yes ]; then
    local clr=(budget set --repo "$REPO_NAME" --clear)
    if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then clr+=("${FUGARO_FLAGS[@]}"); fi
    if [ -n "$PROJECT" ]; then clr+=(--project "$PROJECT"); fi
    say "putting $REPO_NAME's own caps back: ${FUGARO_BIN} ${clr[*]}"
    local cstatus=0
    "$FUGARO_BIN" "${clr[@]}" >"$SCRATCH/restore.out" 2>"$SCRATCH/restore.err" || cstatus=$?
    if [ "$cstatus" -ne 0 ]; then
      fact teardown FAIL "the repository's own caps were NOT put back: fugaro budget set --repo $REPO_NAME --clear exited $cstatus ($(last_line "$SCRATCH/restore.err")). The node held no cap of its own before this check, so this is the command that removes the \$0.00 this check wrote: ${FUGARO_BIN} budget set --repo $REPO_NAME --clear${PROJECT:+ --project $PROJECT}. $LANDED_NOTE Run it yourself"
      return 0
    fi
  fi
  local args=(budget set --repo "$REPO_NAME")
  if [ ${#RESTORE_ARGS[@]} -gt 0 ]; then
    args+=("${RESTORE_ARGS[@]}")
    if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
    if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
    say "putting $REPO_NAME's own caps back: ${FUGARO_BIN} ${args[*]}"
    local status=0
    "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/restore.out" 2>"$SCRATCH/restore.err" || status=$?
    if [ "$status" -ne 0 ]; then
      fact teardown FAIL "the repository's own caps were NOT put back: ${FUGARO_BIN} ${args[*]} exited $status ($(last_line "$SCRATCH/restore.err")). $LANDED_NOTE Run this yourself: ${FUGARO_BIN} ${args[*]}"
      return 0
    fi
  fi
  # Read back, because a write that printed no error is not a write that landed:
  # this row is the only thing you have to trust about your own caps.
  local check=(budget show --json --repo "$REPO_NAME")
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then check+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then check+=(--project "$PROJECT"); fi
  local status=0
  "$FUGARO_BIN" "${check[@]}" >"$SCRATCH/restored.json" 2>"$SCRATCH/restored.err" || status=$?
  read_json "$SCRATCH/restored.txt" caps "$SCRATCH/restored.json" "$REPO_NAME" || true
  local daily perrun want
  daily=$(field "$SCRATCH/restored.txt" daily)
  perrun=$(field "$SCRATCH/restored.txt" perrun)
  if [ ${#RESTORE_ARGS[@]} -eq 0 ]; then
    want="the defaults again (this repository's node set no cap of its own)"
  else
    want="${RESTORE_ARGS[*]}"
  fi
  if [ "$(field "$SCRATCH/restored.txt" match)" != "0" ]; then
    fact teardown FAIL "the putting back could not be read: fugaro budget show --json --repo $REPO_NAME (exit $status: $(last_line "$SCRATCH/restored.err")) answered no row for this repository, so this check cannot say whether your caps are back. What it wrote was $want. Its output is in $SCRATCH/restored.json (set CHECK381_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  fact teardown PASS "the repository's own caps are back: this check lowered them to \$0.00 (${WRITE_ARGS[*]}) and put back $want, and fugaro budget show --json --repo $REPO_NAME now reads daily \$$daily and per-run \$$perrun. It reads rather than trusts, because a putting back that printed no error is not one that landed"
}

# --------------------------------------------------------------- the verdict
the_verdict() {
  if [ "$BUILD_TIMED_OUT" = yes ]; then
    fact overcap FAIL "the over-cap refusal is NOT MEASURED: the build command never ended, so this check read no refusal at all. A command that waits is its own finding (the build row says what it printed), and a build that was not refused may have been submitted"
    REACHED_END=yes
    exit 1
  fi
  if [ "$BUILD_STATUS" != 1 ]; then
    fact overcap FAIL "the over-cap refusal does NOT hold as measured: fugaro image build exited $BUILD_STATUS, not the refusal's own code (1), so a build this check had made impossible was not refused. Read the build row: an exit 0 means a build may have been submitted with the repository's daily cap at \$0.00"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$MISSING" ]; then
    fact overcap FAIL "the over-cap refusal does NOT hold as measured: the build was refused (exit 1), but the sentence is missing $MISSING. The refusal is what replaced the confirmation in front of this billable build, so a part it lost is a part an operator cannot act on"
    REACHED_END=yes
    exit 1
  fi
  fact overcap PASS "the over-cap refusal HOLDS on this installation: with this repository's own caps at \$0.00 (${REFUSAL_SHAPE:-daily}), fugaro image build was refused after ${BUILD_SECONDS}s with exit 1 and one sentence carrying the estimate and where its price came from, the cap it met, the figure that cap compared and what the build needed, and the one command that raises the cap. Nothing of the build was submitted"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
#
# A key CAN be recorded twice -- the write row is the lowering and then the read
# back of it -- and the block carries the LAST one, because that is the one the
# run stood behind when it ended: a lowering that printed no error and did not
# read back is a failure, and a block that kept the first row would report the
# write as a pass on the strength of the CLI's own word.
BLOCK_KEYS=(xtrace agent-session project belong checkout mode caps write build refusal overcap teardown)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=""
  printf '\nCHECK 381 RESULT\n' >&3
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
  printf 'CHECK 381 RESULT END (exit %s)\n' "$status" >&3
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
  # answer about the refusal when the read that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the over-cap refusal"
    fi
    status=2
  fi
  # A row that reads FAIL can never end in a pass. The verdict sets its own code,
  # but the putting back runs from this trap AFTER it -- so a verdict of "it holds"
  # whose caps did not go back is turned into a non-zero exit here, which is the
  # one reading an owner must not have to look for.
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
  owner "interrupted: putting your caps back, printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads] ${FUGARO_BIN} doctor --json${FUGARO_FLAGS[*]:+ ${FUGARO_FLAGS[*]}}${PROJECT:+ --project $PROJECT}: which installation the config in force names, held to --project where you typed one, with a project named belong refused here and in what doctor answers"
  say "  [reads] the checkout ${CHECKOUT}'s origin remote, which is the repository the build is charged to and the one whose caps are lowered"
  say "  [reads] ${FUGARO_BIN} budget show --json --repo <that repository> and ${FUGARO_BIN} project show --json: the budget mode (it must be enforce, or a cap only advises and the build is submitted anyway) and the repository's OWN caps node, which is what gets put back"
  say "  [writes] ${FUGARO_BIN} budget set --repo <that repository> --daily 0 (and --per-run 0 where the node keeps a per-run cap of its own): ONE tightening, which asks nothing and is no raise. The write is read back before anything is built"
  say "  [runs] cd ${CHECKOUT} && ${FUGARO_BIN} image build --no-wait, its stdin on /dev/null, under a ${DEADLINE}s deadline: THE COMMAND THIS CHECK MEASURES. The lease refuses it before the submission, so nothing is built"
  say "  [reads the refusal] exit 1, and one sentence carrying the estimate and where its price came from, the cap it met, the figure that cap compared and what the build needed, and the one command that raises the cap"
  say "  [writes back] ${FUGARO_BIN} budget set --repo <that repository> with the values the node held, or --clear where it held no cap of its own, and then reads the caps back to say so -- from the exit trap, on every way out"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  CHECKOUT_ARG=${CHECK381_CHECKOUT:-.}
  PROJECT=${CHECK381_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK381_DRY_RUN:-}
  DEADLINE=${CHECK381_DEADLINE:-900}
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
      printf '\nLowers ONE repository\x27s own caps to $0.00 (a tightening, which asks nothing),\nruns %s image build --no-wait from the checkout with its stdin closed, reads\nthe refusal and puts the caps back from the exit trap. It prints one block headed\nCHECK 381 RESULT and exits 0 when the build was refused, non-zero, with the\nestimate, the cap, the figure that cap compared and the one command that raises\nit; 1 when it was not refused or the sentence lost a part; and 2 when this run\ncould not tell. It is yours to run: no agent runs any step of it.\n' "$FUGARO_BIN"
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
    fact checkout INFO "planned: a real run reads the origin remote of $CHECKOUT ($origin) as $dry_repo, and lowers THAT repository's own caps"
  else
    fact checkout INFO "planned: a real run reads the origin remote of $CHECKOUT and refuses if it does not name a repository; this dry run could not read it as one"
  fi
  fact mode INFO "planned: a real run reads ${FUGARO_BIN} budget show --json and stops, unmeasured, unless the mode is enforce -- in observe a cap only advises and the build is submitted anyway, so a build here would spend money rather than be refused"
  fact caps INFO "planned: a real run reads the repository's own caps node out of ${FUGARO_BIN} project show --json and refuses to write at all when it cannot read it, because a cap it cannot put back is a change to your project"
  fact write INFO "planned: a real run lowers that node to \$0.00 with one fugaro budget set (a tightening, which asks nothing), then reads it back so a write that did not land cannot leave a real build to be submitted"
  fact build INFO "planned: a real run runs fugaro image build --no-wait from $CHECKOUT with its stdin on /dev/null under a ${DEADLINE}s deadline, and reports its exit status and its own last line"
  fact refusal INFO "planned: a real run reads the whitespace-collapsed text of both streams for the parts of the refusal and FAILs naming any part that is missing"
  fact overcap INFO "planned: the verdict is a real run's; a dry run measured nothing"
  fact teardown INFO "planned: a real run puts the repository's own caps back from the exit trap, on every way out, and reads them back afterwards to say so"
  fact dry-run PASS "the plan was printed and nothing was run: no cap was lowered, no build was started and nothing was written outside the scratch directory"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  guard_agent_session
  write_reader
  # The checkout is resolved before the dry run too: the plan names the repository
  # the caps would be lowered on, and a plan that could not say which one would be
  # a plan for some other repository's project.
  CHECKOUT=$(cd "$CHECKOUT_ARG" 2>/dev/null && pwd) || stop "--checkout '$CHECKOUT_ARG' is not a directory this check can read"
  REPO_NAME=""
  if [ -n "$DRY_RUN" ]; then
    local origin dry_repo
    origin=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null || true)
    dry_repo=""
    if [ -n "$origin" ]; then
      dry_repo=$(printf '%s' "$origin" | python3 "$SCRATCH/read.py" origin /dev/stdin 2>/dev/null | sed -n 's/^repo=//p' | head -1) || true
    fi
    BLOCK_KEYS=(xtrace agent-session belong project checkout mode caps write build refusal overcap teardown dry-run)
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
  read_caps
  step_write
  verify_write
  step_build
  step_refusal
  the_verdict
}

FUGARO_BIN=${CHECK381_FUGARO:-fugaro}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
CHECKOUT=""
CHECKOUT_ARG=.
REPO_NAME=""
DEADLINE=900
CAPS_MODE=""
CAPS_DAILY_SOURCE=""
CAPS_PERRUN_SOURCE=""
CAPS_DAILY=""
CAPS_PERRUN=""
CAPS_COUNTED=""
CAPS_MAX_RESERVE=""
OWN_DAILY=""
OWN_PERRUN=""
OWN_DAILY_SET=no
OWN_PERRUN_SET=no
WRITE_DONE=no
WRITE_LANDED=no
RESTORE_CLEAR=no
WRITE_ARGS=()
RESTORE_ARGS=()
BUILD_LAST=""
BUILD_LAST_FROM=stderr

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
