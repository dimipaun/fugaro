#!/usr/bin/env bash
# Check 210 — a sampled run's shadow publishes nothing.
#
# Plan task A2.2 writes this check; what it settles is docs/model-ab-testing/
# implementation-plan.md's "A2.2 Check 210" and the plan's Global constraints
# table row — "A challenger never pushes, never opens, updates or comments on a
# PR, never pushes a checkpoint, and its agent holds no git credential"
# (A2.1's tests, and Check 210) — plus the sentence the shipped release notes
# promise the owner (docs/releases/v0.24.0.md): a sampled task still leaves one
# branch and one pull request.
#
# What it measures, on the sandbox installation with the project layer's
# `ab.rate` at the plan's own 0.10 and at least one challenger named:
#
#   * small sandbox tasks launched until `fugaro runs ls` shows a pair. About
#     ten launches are expected at 0.10, and the owner stops at thirty. Nothing
#     here forces a sample: the plan's sentence says no flag forces one, and
#     that is the design — a sample is the sampler's own hash of the run ID, so
#     a flag that steered it would measure a different program. `--max-launches`
#     widens the bound and never forces the roll;
#   * the pair the launch sampled, read from `fugaro run --json`'s own `pair`
#     block (its id, its `ab-pair-<the champion's run ID>` batch label, and the
#     challenger's run ID), and then from `fugaro runs ls --batch <label>` —
#     the command the plan's sentence names — as exactly the two arms, both
#     ended, under the one label;
#   * both records, read from the runs bucket as the owner reads them: the
#     champion's carries `ab.arm: champion` and the pair's id, and the
#     challenger's carries `ab.arm: challenger`, `ab.publish: none`,
#     `ab.challenger` (the model the owner named) with
#     `ab.challenger_source: owner`, `outcome: none`, and NO branch and NO pull
#     request — which is the whole claim the release makes about a shadow;
#   * the forge itself: `git ls-remote` shows `fugaro/<the champion's run ID>`
#     and shows no branch at all for the challenger's run ID, so "one branch"
#     is read from the remote rather than from the record that claims it;
#   * both arms' patch objects, listed in the bucket at the `patch_object` key
#     each record carries (A2.1: both arms upload one, the champion included);
#   * `fugaro ab --json`, the command the owner reads pairs with: the pair is
#     in its rows, so a pair the launcher made is a pair the report can score.
#
# It creates what the plan's sentence says it creates: up to thirty sandbox
# runs (each billable, and each champion among them pushes its branch and opens
# its pull request, as an ordinary sandbox run does), and nothing else. Its
# teardown is part of the check rather than a list of commands for the owner:
# a launch that has not ended is cancelled, every pull request this check's
# launches opened is closed and every branch they pushed is deleted, each read
# back afterwards.
#
# Usage:      bash scripts/live-checks/check210.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check210.sh --sandbox <path> --yes --max-launches 12
#             bash scripts/live-checks/check210.sh --sandbox <path> --yes --task "<the task>"
#             bash scripts/live-checks/check210.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check210.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents and the
#             published layer's `ab:` block are read with it), git (the remote
#             branches), gcloud (authenticated as YOU, and able to read the runs
#             bucket), fugaro (the installed release: the whole point is the
#             build you have) and, for the teardown only, gh — the sandbox
#             repository is a GitHub one, and closing its pull requests is the
#             one step no other tool here makes. A machine without gh still
#             deletes the branches and says, in the Restore row, which pull
#             requests are left for you to close.
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    up to thirty sandbox runs and the pull requests their champions
#             open — every one of them closed and every branch deleted again by
#             the Restore. On disk: one scratch directory, removed at the end,
#             holding the commands' output.
# Costs:      up to THIRTY sandbox runs and what their tasks cost — the model
#             spend of each champion's implement stage and its review rounds, on
#             the sandbox's own budget — plus each challenger's own spend where
#             a pair is sampled. About ten launches is the expectation at 0.10,
#             and thirty is the bound. `--yes` is required, and the launches
#             stop at the first pair, so a sandbox already at 0.10 usually
#             costs a fraction of the bound.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check210_test.sh) drives it against fake tools only.
# Cleanup:    the branches this check's launches pushed and the pull requests
#             their champions opened, closed and deleted by the Restore, which
#             reads each one back; a launch that has not ended, stopped with
#             `fugaro cancel --now` and read back as ended, so that no run of
#             this check's own outlives it; and the scratch directory, removed
#             from the exit trap even on a failed assertion or a Ctrl-C.
# Output:     progress on stderr, ONE block on stdout headed CHECK 210 RESULT,
#             then CHECK 210 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold — the layer samples nothing, no pair
#             appeared in the bound, an arm is not what the release promises, or
#             the teardown could not close what it opened. 2: the check could
#             not measure — a tool is missing, the checkout is not one, a
#             `--json` document could not be read, or an arm did not end. A key
#             the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry run
# IDs, branch names, pull request numbers, model IDs and statuses.
#
# Knobs (all CHECK210_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, TASK (what each launch is
# launched with), MAX_LAUNCHES (30), RELEASE (0.24.0: the release the pair
# launch ships in), SINCE (1d: the window `fugaro ab` is read over), REGION
# (us-east5), RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS
# (180), CANCEL_TIMEOUT_SECONDS (300), LAUNCH_TIMEOUT_SECONDS (600),
# DOCTOR_TIMEOUT_SECONDS (300). --sandbox,
# --task and --max-launches are the same three values as flags, and a value you
# type as a flag beats the same value in the environment: the environment is how
# a script sets a knob, and a run that quietly used the other one would spend
# the sandbox's money at a bound you did not choose.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a
# real `fugaro` at a real terminal, and a trace of that is a second copy of what
# its own output holds.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 210 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro git; do
    command -v "$t" >/dev/null 2>&1 || die "Check 210 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk210-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REPO_NAME=""
RUN_ID=""
CHALLENGER_ID=""
PAIR_ID=""
BATCH=""
CHAMPION_REF=""
CHALLENGER_REF=""
LAUNCHED=0
# LAYER_OK and PAIR_OK are set by the one branch of the step that establishes
# the state the launches below are a reading of. Every step that launches a run
# reads them and nothing else: launches made after a failed premise or with
# nothing to sample would spend the sandbox's money on a reading nobody could
# grade, and the FAIL row above is already the finding.
LAYER_OK=no
PAIR_OK=no
# ARMS_OK is the same for the pair's two rows: the records below are read at the
# refs those rows carry, so without them there is no key to read.
ARMS_OK=no
CHALLENGER_MODEL=""
REASONS_FILE=""
LAUNCHES_FILE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk210.XXXXXXXX") || die "Check 210 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  REASONS_FILE="$SCRATCH/reasons"
  : >"$REASONS_FILE"
  LAUNCHES_FILE="$SCRATCH/launched"
  : >"$LAUNCHES_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK210_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a run ID the release did not write the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so a
# shell can hold them without a second parser.
#
# The one document that is not JSON is the published project layer, which
# `fugaro config layer --json` hands over as YAML TEXT and no command
# resolves to a value: nothing in the release prints an effective `ab.rate`
# (`policy.Layer.ABRate` is filled by the launcher and read by the sampler), so
# the check reads the layer's own `ab:` block here, as the owner reads it in the
# file they published. It is a text read and the row says so.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def text(path):
    with open(path, "r", encoding="utf-8") as fh:
        return fh.read()


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def strip_comment(s):
    out = []
    quote = None
    i = 0
    while i < len(s):
        c = s[i]
        if quote:
            if c == quote:
                quote = None
            out.append(c)
        elif c in "\"'":
            quote = c
            out.append(c)
        elif c == "#" and (not out or out[-1] in " \t"):
            break
        else:
            out.append(c)
        i += 1
    return "".join(out).strip()


def unquote(s):
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "\"'":
        return s[1:-1]
    return s


def split_flow(inner):
    """Split a flow sequence or mapping body on its own top-level commas."""
    parts = []
    depth = 0
    quote = None
    cur = []
    for c in inner:
        if quote:
            if c == quote:
                quote = None
            cur.append(c)
            continue
        if c in "\"'":
            quote = c
            cur.append(c)
            continue
        if c in "[{":
            depth += 1
        elif c in "]}":
            depth -= 1
        if c == "," and depth == 0:
            parts.append("".join(cur))
            cur = []
            continue
        cur.append(c)
    if "".join(cur).strip():
        parts.append("".join(cur))
    return [p.strip() for p in parts if p.strip()]


def flow_mapping(body):
    out = {}
    for part in split_flow(body):
        key, sep, val = part.partition(":")
        if not sep:
            continue
        val = val.strip()
        if val.startswith("[") and val.endswith("]"):
            out[key.strip()] = [unquote(v) for v in split_flow(val[1:-1])]
        else:
            out[key.strip()] = unquote(val)
    return out


def block_mapping(lines):
    """A YAML mapping from its own lines, stripped of their indentation: the
    block is already known to be the one under `ab:`, so the nesting left to
    read is a scalar, a flow collection, or a sequence of plain items."""
    out = {}
    i = 0
    while i < len(lines):
        ln = lines[i]
        if ln.startswith("-"):
            i += 1
            continue
        key, sep, val = ln.partition(":")
        if not sep:
            i += 1
            continue
        key = key.strip()
        val = strip_comment(val)
        if val.startswith("[") and val.endswith("]"):
            out[key] = [unquote(v) for v in split_flow(val[1:-1])]
            i += 1
            continue
        if val:
            out[key] = unquote(val)
            i += 1
            continue
        items = []
        j = i + 1
        while j < len(lines) and lines[j].startswith("-"):
            items.append(unquote(strip_comment(lines[j][1:])))
            j += 1
        out[key] = items if j > i + 1 else ""
        i = j
    return out


def ab_block(doc):
    """The top-level `ab:` block of a layer or a fugaro.yaml, as
    (present, mapping). The key's own indentation is what ends the block: a
    mapping nested under another top-level key is not this one."""
    lines = doc.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        if re.match(r"^ab\s*:", line):
            rest = strip_comment(line.split(":", 1)[1])
            if rest:
                if rest.startswith("{") and rest.endswith("}"):
                    return True, flow_mapping(rest[1:-1])
                return True, {}
            block = []
            i += 1
            while i < len(lines):
                ln = lines[i]
                if not ln.strip() or ln.lstrip().startswith("#"):
                    i += 1
                    continue
                if not ln[:1].isspace():
                    break
                block.append(ln.strip())
                i += 1
            return True, block_mapping(block)
        i += 1
    return False, {}


def top_keys(doc):
    """The layer's own top-level keys: what a project that publishes no `ab:`
    block DOES carry, which is what tells its owner where the block goes."""
    keys = []
    for line in doc.split("\n"):
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_.-]*)\s*:", line)
        if m and m.group(1) not in keys:
            keys.append(m.group(1))
    return keys


def listed(value):
    if isinstance(value, list):
        return ",".join(str(v) for v in value)
    return flat(value)


def ab_lines(mapping):
    out = []
    out.append("rate=%s" % flat(mapping.get("rate")))
    out.append("challengers=%s" % listed(mapping.get("challengers")))
    out.append("challenger_count=%d" % (
        len(mapping["challengers"]) if isinstance(mapping.get("challengers"), list)
        else (1 if flat(mapping.get("challengers")) else 0)))
    out.append("max_pairs_per_day=%s" % flat(mapping.get("max_pairs_per_day")))
    out.append("judge_model=%s" % flat(mapping.get("judge_model")))
    out.append("keys=%s" % ",".join(sorted(mapping.keys())))
    return out


what = sys.argv[1]

if what == "doctor":
    doc = load(sys.argv[2])
    project = doc.get("project") or {}
    print("gcp_project=%s" % flat(project.get("gcp_project")))
    print("name=%s" % flat(project.get("name")))
elif what == "layer":
    doc = load(sys.argv[2])
    print("where=%s" % flat(doc.get("where")))
    print("generation=%s" % (doc.get("generation") or 0))
    print("sha256=%s" % flat(doc.get("sha256")))
    present, mapping = ab_block(doc.get("yaml") or "")
    print("has_ab=%s" % yes(present))
    print("top_keys=%s" % ",".join(top_keys(doc.get("yaml") or "")))
    for line in ab_lines(mapping):
        print(line)
elif what == "abfile":
    present, mapping = ab_block(text(sys.argv[2]))
    print("has_ab=%s" % yes(present))
    for line in ab_lines(mapping):
        print(line)
elif what == "launch":
    doc = load(sys.argv[2])
    pair = doc.get("pair") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("pair_id=%s" % flat(pair.get("id")))
    print("pair_batch=%s" % flat(pair.get("batch")))
    print("pair_challenger=%s" % flat(pair.get("challenger")))
    print("pair_reason=%s" % flat(pair.get("reason")))
elif what == "rows":
    doc = load(sys.argv[2])
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("batch") or "",
            row.get("run") or "",
            row.get("branch") or "",
            row.get("pr") or 0,
            row.get("outcome") or "",
            row.get("repo") or "",
        ))
elif what == "record":
    doc = load(sys.argv[2])
    ab = doc.get("ab") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("stage=%s" % flat(doc.get("stage")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("reason=%s" % flat(doc.get("reason")))
    print("branch=%s" % flat(doc.get("branch")))
    # runstore.Record.PR is an object, not a number (result.schema.json's `pr`:
    # {number,url}), so the field is read for what it holds: a reader that took
    # it for a number would paste a Python repr into the row.
    print("pr=%s" % ((doc.get("pr") or {}).get("number") or 0))
    print("patch_object=%s" % flat(doc.get("patch_object")))
    print("patch_truncated=%s" % yes(doc.get("patch_truncated")))
    print("ab_pair_id=%s" % flat(ab.get("pair_id")))
    print("ab_arm=%s" % flat(ab.get("arm")))
    print("ab_publish=%s" % flat(ab.get("publish")))
    print("ab_challenger=%s" % flat(ab.get("challenger")))
    print("ab_challenger_source=%s" % flat(ab.get("challenger_source")))
    print("ab_slot=%s" % flat(ab.get("slot")))
elif what == "ab":
    doc = load(sys.argv[2])
    rows = doc.get("rows") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("ab_row\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("mode") or "",
            row.get("tier") or "",
            row.get("champion") or "",
            row.get("challenger") or "",
            row.get("pairs") or 0,
            row.get("wins") or 0,
            row.get("losses") or 0,
        ))
    for w in doc.get("warnings") or []:
        print("warning\t%s" % flat(w))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file, and fail the step
# when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# rows_row FILE RUNID — the `fugaro runs ls --json` row for one run, as the tab
# row it is. The run's own id is the only row that counts: a list read by
# position alone could be another run's.
rows_row() { # rows_row FILE RUNID
  awk -F'\t' -v id="$2" '$1 == "row" && $2 == id { print; exit }' "$1" 2>/dev/null
}

rcol() { # rcol ROW N — one column of a tab row, "" when there is none
  [ -n "$1" ] || return 0
  printf '%s\n' "$1" | awk -F'\t' -v n="$2" '{ print $n }'
}

# The two facts run_bounded reports are declared here, before any step: a step
# that names them in a message after a read that never started (a ref that is
# not this check's, an argument that did not match) would otherwise abort under
# `set -u` instead of reporting what it found.
BOUNDED_STATUS=0
BOUNDED_TIMED_OUT=no

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the record and patch reads are the exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a record that
# is not there, a branch that is not on the remote), and a trap inherited here
# would turn each of them into "an internal error" and a status of 2 before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: the launch loop and the arms poll call the same key
  # again and again, and a `--json` read takes the call's own document — two
  # documents in one file is a parse error, not a second reading.
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  # The poll is a tenth of a second and the bound is counted in tenths: a read
  # that answers at once — every one of them, when the installation is healthy —
  # would otherwise cost this check a whole second each, and the owner watches
  # the check rather than the clock.
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

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check210.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused. Refusing here rather than letting
# each command refuse one at a time is what makes the block say why: a session
# that ran this far would report the refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 210 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

# version_at_least HAVE NEED: 0 when HAVE is a release number at or above NEED,
# 1 when it is a release number below it, 2 when HAVE is not a release number at
# all (a dev build, a commit) — the third answer is why this is not a comparison
# alone: a build that names no version is decided by the feature probe below and
# never by a guess at what "dev" sorts as.
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

# step_release: the gate every check of a released feature carries. The pair
# launch ships in 0.24.0 (docs/releases/v0.24.0.md), and a build before it
# samples nothing at all: its launch path has no planner, or one whose `ab`
# support the job image does not honour. Both probes are offline: `fugaro
# version`, and the usage line `fugaro ab` prints — the A/B results command is
# the Subscription's, so a core build answers with D522's stub instead of the
# command, and a stub names no flag.
step_release() {
  local v feature=no
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" ab --help 2>/dev/null | grep -q -- '--since'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro carries no A/B: 'fugaro ab --help' does not name the command's own --since flag, so this build is either older than release ${RELEASE} — the release the pair launch ships in — or a core build, whose 'ab' is the D522 stub rather than the command. Check 210 needs release ${RELEASE} (docs/releases/v0.24.0.md) or later, built with the features it ships. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro carries no A/B: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'ab' command answers: this build carries the sampler, the pair launch and the report the rows below read"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, the release the pair launch ships in: a launch of it samples nothing, and the launches below would measure a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro ab --help' names the command, so this build carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  fact bound PASS "up to ${MAX_LAUNCHES} sandbox runs and what their tasks cost — each champion's implement stage and its review rounds, plus each challenger's own spend — on the sandbox's own budget, plus every branch and pull request those champions leave, which the Restore closes and deletes again. The launches stop at the FIRST pair, so a sandbox already sampling at ${RATE_WANTED} costs a fraction of the bound. Nothing here forces a sample: a sample is the sampler's own hash of the run ID, and --max-launches widens the bound rather than steering the roll. --yes is required to launch"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and every launch and record below is that
# project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the runs this check launches are launched from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured sandbox "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a GCP project: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches from this checkout and reads the pair's records in this project's bucket"
}

# step_layer: the state every launch below is a reading of. The plan's sentence
# is "on the sandbox installation with ab.rate: 0.10 and one challenger", and
# the published project layer is the only place that rate and that list live
# (D556): nothing in the release prints an effective ab.rate, so the check reads
# the layer's own `ab:` block out of the text `fugaro config layer --json`
# hands over, and says in the row that it is a text read.
step_layer() {
  if [ -z "$PROJECT" ]; then
    unmeasured layer "there is no project to read the layer for: the row above says why"
    return 0
  fi
  if ! read_json abfile "$SANDBOX/fugaro.yaml" "$SCRATCH/repoab.txt"; then
    unmeasured layer "the sandbox checkout's fugaro.yaml could not be read: it is where a repository's own ab.rate: 0 opt-out lives (D556), and without it this check cannot say whether the repository has opted out. The file is at $SANDBOX/fugaro.yaml"
    return 0
  fi
  say "reading the published project layer: fugaro config layer --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" layer "$SANDBOX" "$FUGARO_BIN" config layer --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured layer "fugaro config layer --json did not answer (exit ${BOUNDED_STATUS}): the published project layer is where ab.rate and the challenger list live, and without it there is nothing to launch against. Its output is in $SCRATCH/layer.out and $SCRATCH/layer.err — a project with no published layer answers here too, and then this check's premise is one 'fugaro config publish' away"
    return 0
  fi
  if ! read_json layer "$SCRATCH/layer.out" "$SCRATCH/layer.txt"; then
    unmeasured layer "fugaro config layer --json printed a document this check could not read: what it printed is in $SCRATCH/layer.out"
    return 0
  fi
  local where rate challengers count keys
  where=$(field "$SCRATCH/layer.txt" where)
  rate=$(field "$SCRATCH/layer.txt" rate)
  challengers=$(field "$SCRATCH/layer.txt" challengers)
  count=$(field "$SCRATCH/layer.txt" challenger_count)
  keys=$(field "$SCRATCH/layer.txt" top_keys)
  if [ "$(field "$SCRATCH/layer.txt" has_ab)" != true ]; then
    fact layer FAIL "the published project layer carries no top-level ab: block (its where is ${where:-unknown}); its top-level keys are ${keys:-none}, and this check read the whole block out of the layer's own text. A project with no ab: block samples nothing at all (D550, D557), so no launch below could ever be the pair this check measures. Add the block to the project's layer file — ab.rate: ${RATE_WANTED} and the challengers you allow, which is the shape docs/releases/v0.24.0.md's For operators paragraph gives — and publish it with 'fugaro config publish'"
    return 0
  fi
  if ! awk -v r="${rate:-}" 'BEGIN { exit (r + 0 > 0) ? 0 : 1 }'; then
    fact layer FAIL "the published project layer sets ab.rate: ${rate:-unset} (where ${where:-unknown}); A/B is off, so no launch below is ever sampled and the loop could spend its whole bound without a pair — which is not a reading about the shadow but about a switch nobody turned on. Set ab.rate to ${RATE_WANTED} in the project's layer file and publish it with 'fugaro config publish'"
    return 0
  fi
  if ! awk -v r="$rate" -v want="$RATE_WANTED" 'BEGIN { exit (r + 0 == want + 0) ? 0 : 1 }'; then
    fact layer FAIL "the published project layer sets ab.rate: ${rate} and this check's premise is ${RATE_WANTED} (D557's ceiling, which is the figure the plan's own sentence names and the rate the loop's bound is sized for). A lower rate needs more launches than ${MAX_LAUNCHES} to expect a pair, and a higher one is not open to a project. Set ab.rate to ${RATE_WANTED}, publish it, and run this check again — or widen the bound with --max-launches if you mean to measure a lower rate"
    return 0
  fi
  if [ "${count:-0}" -lt 1 ]; then
    fact layer FAIL "the published project layer sets ab.rate: ${RATE_WANTED} and names no challenger (ab.challengers is empty); a launch that samples then skips with the closed reason 'no-challenger' (D556), so no pair is ever started. Name the model you allow as a challenger in the layer's challengers list and publish the layer again"
    return 0
  fi
  if [ "$(field "$SCRATCH/repoab.txt" has_ab)" = true ]; then
    local repo_rate
    repo_rate=$(field "$SCRATCH/repoab.txt" rate)
    if awk -v r="${repo_rate:-}" 'BEGIN { exit (r + 0 > 0) ? 0 : 1 }'; then
      :
    else
      fact layer FAIL "the sandbox repository's own fugaro.yaml carries an ab: block with rate ${repo_rate:-unset}, which is the opt-out (D556, D557): the repository lowers the project's rate to zero, so no launch of it is ever sampled whatever the layer says. Remove the ab: block from $SANDBOX/fugaro.yaml (or raise its rate, which a repository may only do down to zero) and run this check again"
      return 0
    fi
  fi
  LAYER_OK=yes
  fact layer PASS "the published project layer (${where:-unknown}, generation $(field "$SCRATCH/layer.txt" generation), sha256 $(field "$SCRATCH/layer.txt" sha256)) sets ab.rate: ${rate} and names ${count} challenger(s): ${challengers}. This is a text read of the layer's own ab: block — no command in this release prints an effective ab.rate — and the sandbox repository's fugaro.yaml carries no opt-out"
}

# step_pair: the launches. The plan's sentence is small sandbox tasks launched
# until `fugaro runs ls` shows a pair, about ten expected at 0.10, the owner
# stopping at thirty; and there is deliberately no flag that forces a sample, so
# this step can only spend launches and read what they answered. The launch's
# own `--json` names the pair, and the batch label it carries is what the row
# below reads `fugaro runs ls` with.
step_pair() {
  if [ -z "$PROJECT" ]; then
    unmeasured pair "there is no project to launch a run in: the row above says why"
    return 0
  fi
  # The launches happen only once the row above has established the state they
  # are a reading of — a published layer that samples at the plan's rate with a
  # challenger named. Launches made after it failed would spend the sandbox's
  # money on a reading nobody could grade, and the FAIL row above is already the
  # finding.
  if [ "$LAYER_OK" != yes ]; then
    unmeasured pair "nothing was launched: the layer row above did not establish the state these launches are a reading of, and up to ${MAX_LAUNCHES} runs launched without it would spend the sandbox's money on a reading nobody could grade. Settle that row and run this check again"
    return 0
  fi
  local n pair_id pair_batch pair_challenger pair_reason run_id repo status
  for n in $(seq 1 "$MAX_LAUNCHES"); do
    say "launching ${n} of at most ${MAX_LAUNCHES} from ${SANDBOX} (a real sandbox run, with what its task costs)"
    run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --json "$TASK"
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      unmeasured pair "launch ${n} did not finish inside ${LAUNCH_TIMEOUT}s and was ended: the launches before it are real sandbox runs, and this check's teardown cancels the ones that have not ended with 'fugaro cancel <run> --now' when it ends. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
      return 0
    fi
    if [ "$BOUNDED_STATUS" != 0 ]; then
      fact pair FAIL "fugaro run exited ${BOUNDED_STATUS} on launch ${n} of at most ${MAX_LAUNCHES} from ${SANDBOX}: the check needs a launch to sample a pair, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
      return 0
    fi
    if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
      fact pair FAIL "fugaro run --json printed a document this check could not read on launch ${n}: the launch is the step that samples, and its own --json view is how the pair is named. What it printed is in $SCRATCH/launch.out"
      return 0
    fi
    run_id=$(field "$SCRATCH/launch.txt" run_id)
    repo=$(field "$SCRATCH/launch.txt" repo)
    status=$(field "$SCRATCH/launch.txt" status)
    if [ -z "$run_id" ]; then
      fact pair FAIL "the launch ${n} named no run (status=${status:-unknown}): this check follows and tears down the runs it launched, and a launch that does not name its own cannot be followed. What it printed is in $SCRATCH/launch.out"
      return 0
    fi
    LAUNCHED=$n
    RUN_ID=$run_id
    [ -n "$repo" ] && REPO_NAME=$repo
    printf '%s\n' "$run_id" >>"$LAUNCHES_FILE"
    pair_id=$(field "$SCRATCH/launch.txt" pair_id)
    pair_batch=$(field "$SCRATCH/launch.txt" pair_batch)
    pair_challenger=$(field "$SCRATCH/launch.txt" pair_challenger)
    pair_reason=$(field "$SCRATCH/launch.txt" pair_reason)
    if [ -n "$pair_id" ]; then
      if [ -z "$pair_challenger" ]; then
        fact pair FAIL "launch ${n} sampled a pair (${pair_id}) and the challenger did not start: fugaro run --json names the pair and no second run in it. The champion ${run_id} is a real sandbox run: this check's teardown cancels it with 'fugaro cancel ${run_id} --now' when it ends, unless it has ended by then. Its stderr names why the arm did not start and is in $SCRATCH/launch.err"
        return 0
      fi
      PAIR_ID=$pair_id
      BATCH=$pair_batch
      CHALLENGER_ID=$pair_challenger
      printf '%s\n' "$pair_challenger" >>"$LAUNCHES_FILE"
      PAIR_OK=yes
      fact pair PASS "launch ${n} of at most ${MAX_LAUNCHES} was sampled: fugaro run --json names pair ${PAIR_ID} on batch ${BATCH} with challenger ${CHALLENGER_ID}. The champion is ${run_id} on ${REPO_NAME:-the sandbox repository}; both arms are torn down by the Restore below"
      return 0
    fi
    printf '%s\t%s\n' "${n}" "${pair_reason:--}" >>"$REASONS_FILE"
  done
  local skipped=0 silent=0 detail="" reason
  while IFS=$'\t' read -r _ reason; do
    [ -n "$reason" ] || continue
    if [ "$reason" = "-" ]; then
      silent=$((silent + 1))
    else
      skipped=$((skipped + 1))
      detail="${detail}${detail:+, }${reason}"
    fi
  done <"$REASONS_FILE"
  fact pair FAIL "${MAX_LAUNCHES} launches and none sampled a pair. ${silent} of them answered with no pair block at all — the rate's own roll, or a launch D558's eligibility excludes, and at 0.10 about a tenth of them sampling is what the plan expects — and ${skipped} were skipped with a closed reason: ${detail:-none}. Nothing here forces a sample, so the bound is the only lever: widen it with --max-launches (each launch is a real sandbox run with what its task costs) or read the reasons above, which name what the sampler refused and what to change"
}

# step_arms: the pair, through the command the plan's sentence names. `fugaro
# runs ls --batch <label>` is what a reader uses to see the two arms, and the
# label is the one the launch's own --json carried, so this row reads the pair
# back rather than restating it. Both arms have to have ENDED: a record read
# while a run is still working is whatever was there before.
step_arms() {
  if [ "$PAIR_OK" != yes ]; then
    unmeasured arms "there is no pair to read back: the row above says why"
    return 0
  fi
  say "waiting for both arms of ${PAIR_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${SANDBOX} --batch ${BATCH} --json)"
  local waited=0 count="" champion_row="" challenger_row="" both=no
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" arms "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --batch "$BATCH" --json
    count=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/arms.out" "$SCRATCH/arms.txt"; then
      count=$(field "$SCRATCH/arms.txt" count)
      champion_row=$(rows_row "$SCRATCH/arms.txt" "$RUN_ID")
      challenger_row=$(rows_row "$SCRATCH/arms.txt" "$CHALLENGER_ID")
      if [ "$(rcol "$champion_row" 4)" = true ] && [ "$(rcol "$challenger_row" 4)" = true ]; then
        both=yes
        break
      fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$both" != yes ]; then
    unmeasured arms "${PAIR_ID}'s two arms had not both ended after ${RUN_TIMEOUT}s (fugaro runs ls --batch ${BATCH} answered ${count:-no} row(s): champion ${RUN_ID} $([ -n "$champion_row" ] && echo "status=$(rcol "$champion_row" 3)" || echo 'is not in the list'), challenger ${CHALLENGER_ID} $([ -n "$challenger_row" ] && echo "status=$(rcol "$challenger_row" 3)" || echo 'is not in the list')). The two arms are real sandbox runs: this check's teardown cancels the ones that have not ended with 'fugaro cancel <run> --now' when it ends, and 'fugaro runs show ${RUN_ID}' or 'fugaro runs show ${CHALLENGER_ID}' reads what they did"
    return 0
  fi
  if [ "$count" != 2 ]; then
    fact arms FAIL "fugaro runs ls --batch ${BATCH} answered ${count} row(s) and the pair is two: the batch label is what both arms carry and what joins them (A3.0), so a label that names ${count} runs is a pairing a reader cannot take the two arms from. The rows are in $SCRATCH/arms.out"
    return 0
  fi
  if [ "$(rcol "$champion_row" 5)" != "$BATCH" ] || [ "$(rcol "$challenger_row" 5)" != "$BATCH" ]; then
    fact arms FAIL "the two rows fugaro runs ls --batch ${BATCH} answered do not both carry the label '${BATCH}': champion '$(rcol "$champion_row" 5)', challenger '$(rcol "$challenger_row" 5)'. The label is what makes the two rows a pair, and one that reads differently is a pair nobody can join. The rows are in $SCRATCH/arms.out"
    return 0
  fi
  CHAMPION_REF=$(rcol "$champion_row" 6)
  CHALLENGER_REF=$(rcol "$challenger_row" 6)
  case "$CHAMPION_REF/$CHALLENGER_REF" in
  */"$RUN_ID"/*/"$CHALLENGER_ID") ;;
  *)
    fact arms FAIL "the two rows under batch ${BATCH} are keyed ${CHAMPION_REF:-none} and ${CHALLENGER_REF:-none}, and this check's arms are ${RUN_ID} and ${CHALLENGER_ID}: a row keyed elsewhere is another run's, and the records below are read at the keys those rows carry. The rows are in $SCRATCH/arms.out"
    return 0
    ;;
  esac
  ARMS_OK=yes
  fact arms PASS "fugaro runs ls --repo ${SANDBOX} --batch ${BATCH} answers exactly the pair's two rows — champion ${RUN_ID} and challenger ${CHALLENGER_ID} — and both have ended (champion $(rcol "$champion_row" 3), challenger $(rcol "$challenger_row" 3)) after ${waited}s. The plan's own sentence is read this way: one label, two rows, a pair"
}

# record_of REF DEST: one run's record out of the runs bucket, as the owner
# reads it. The ref is the row's own `<slug>/<run id>`, which is why it is taken
# from `fugaro runs ls` rather than built here.
record_of() { # record_of REF DEST
  local ref=$1 dest=$2
  case "$ref" in
  */"$RUN_ID" | */"$CHALLENGER_ID") ;;
  *)
    return 1
    ;;
  esac
  run_bounded "$CLI_TIMEOUT" "$dest" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/runs/${ref}/result.json" --project="${PROJECT}"
  [ "$BOUNDED_TIMED_OUT" = no ] && [ "$BOUNDED_STATUS" = 0 ] || return 1
  read_json record "$SCRATCH/$dest.out" "$SCRATCH/$dest.txt"
}

# step_champion: the champion's own record. It publishes as an unsampled run
# does (A2.1) and adds its `ab` block and its patch object; the arm and the pair
# id are what join it to the shadow.
step_champion() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured champion "there is no ended pair to read the champion's record from: the row above says why"
    return 0
  fi
  if ! record_of "$CHAMPION_REF" champion; then
    unmeasured champion "gs://fugaro-runs-${PROJECT}/runs/${CHAMPION_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the arm, the pair and the patch object are written. What gcloud printed is in $SCRATCH/champion.err"
    return 0
  fi
  local recorded arm pair_id
  recorded=$(field "$SCRATCH/champion.txt" run_id)
  arm=$(field "$SCRATCH/champion.txt" ab_arm)
  pair_id=$(field "$SCRATCH/champion.txt" ab_pair_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact champion FAIL "the record at runs/${CHAMPION_REF}/result.json records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this arm's"
    return 0
  fi
  if [ "$arm" != champion ]; then
    fact champion FAIL "the champion's record carries ab.arm '${arm:-absent}' and this arm is 'champion': the champion is the arm of a pair that publishes as it always did (A2.1), and a champion's own block saying otherwise is a pair a reader cannot join. It carries pair_id '${pair_id:-absent}'. The record is in $SCRATCH/champion.out"
    return 0
  fi
  if [ "$pair_id" != "$PAIR_ID" ]; then
    fact champion FAIL "the champion's record carries ab.pair_id '${pair_id:-absent}' and this check's pair is ${PAIR_ID}: the pair id is the one value both arms carry and the only way the two records are joined. The record is in $SCRATCH/champion.out"
    return 0
  fi
  fact champion PASS "the champion's record at runs/${CHAMPION_REF}/result.json carries ab.arm champion and the pair's id ${PAIR_ID} (status=$(field "$SCRATCH/champion.txt" status), outcome=$(field "$SCRATCH/champion.txt" outcome), branch=$(field "$SCRATCH/champion.txt" branch), pr=$(field "$SCRATCH/champion.txt" pr)): it is the arm that publishes, and the rows below read what it left"
}

# step_challenger: the reading the plan names first. A challenger's record is
# the whole claim the release makes about a shadow: the arm, the publish mode,
# the model the OWNER named, and no branch and no pull request.
step_challenger() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured challenger "there is no ended pair to read the challenger's record from: the row above says why"
    return 0
  fi
  if ! record_of "$CHALLENGER_REF" challenger; then
    unmeasured challenger "gs://fugaro-runs-${PROJECT}/runs/${CHALLENGER_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): the challenger's own record is where ab.arm, ab.publish and its outcome are written. What gcloud printed is in $SCRATCH/challenger.err"
    return 0
  fi
  local recorded arm publish source outcome branch pr
  recorded=$(field "$SCRATCH/challenger.txt" run_id)
  arm=$(field "$SCRATCH/challenger.txt" ab_arm)
  publish=$(field "$SCRATCH/challenger.txt" ab_publish)
  source=$(field "$SCRATCH/challenger.txt" ab_challenger_source)
  outcome=$(field "$SCRATCH/challenger.txt" outcome)
  branch=$(field "$SCRATCH/challenger.txt" branch)
  pr=$(field "$SCRATCH/challenger.txt" pr)
  CHALLENGER_MODEL=$(field "$SCRATCH/challenger.txt" ab_challenger)
  if [ "$recorded" != "$CHALLENGER_ID" ]; then
    fact challenger FAIL "the record at runs/${CHALLENGER_REF}/result.json records run_id=${recorded} and this check's challenger is ${CHALLENGER_ID}: the record at that key is another run's, and nothing below can be read as this arm's"
    return 0
  fi
  if [ "$arm" != challenger ]; then
    fact challenger FAIL "the challenger's record carries ab.arm '${arm:-absent}' and this arm is 'challenger': a second arm without the block is a run that publishes, which is the one thing the shadow may never be (D554, A2.1). The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$publish" != none ]; then
    fact challenger FAIL "the challenger's record carries ab.publish '${publish:-absent}' and the arm's own block is 'none' (D554): the publish mode is what the runner reads to push nothing and open nothing, and a challenger whose own record does not carry it is one whose publishes are a question rather than a reading. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ -z "$CHALLENGER_MODEL" ]; then
    fact challenger FAIL "the challenger's record names no model in ab.challenger: the arm runs the coder role on the model the OWNER named in the project layer (D556), and a record that names none is one the pair's report cannot score against the champion. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$source" != owner ]; then
    fact challenger FAIL "the challenger's record carries ab.challenger_source '${source:-absent}' and a challenger's is 'owner' (D556): the source says the model came from the project layer rather than the repository's own file, which is the boundary a repository author may not cross. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$outcome" != none ]; then
    fact challenger FAIL "the challenger's record carries outcome '${outcome:-absent}' and a challenger's is 'none' (A2.1): the outcome is what a reader takes the arm's pull request from, and an arm that records one is an arm that opened something. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ -n "$branch" ] || [ "${pr:-0}" != 0 ]; then
    fact challenger FAIL "the challenger's record carries branch '${branch:-none}' and pr ${pr:-0}: a challenger pushes nothing and opens nothing (D554, A2.1), so a record that names either is the shadow having published. The record is in $SCRATCH/challenger.out, and the branch and pull request it names are yours to remove"
    return 0
  fi
  fact challenger PASS "the challenger's record at runs/${CHALLENGER_REF}/result.json carries ab.arm challenger, ab.publish none, ab.challenger ${CHALLENGER_MODEL} with challenger_source owner, outcome none, and NO branch and NO pull request (status=$(field "$SCRATCH/challenger.txt" status)): the same task, at the same commit, on another coder model — and it published nothing"
}

# step_branch: the forge itself, rather than the record that claims it. The
# plan's sentence is "exactly one fugaro/<run-id> branch", and for a pair that
# is the champion's and not the challenger's: a read of the record alone would
# report what the runner wrote, and this one reports what the remote holds.
step_branch() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured branch "there is no pair to look for branches of: the row above says why"
    return 0
  fi
  say "reading the sandbox repository's branches: git ls-remote --heads origin 'refs/heads/fugaro/*', in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" remote "$SANDBOX" git ls-remote --heads origin "refs/heads/fugaro/*"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured branch "git ls-remote did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): without the remote's own branch list this check cannot tell a shadow that published from one that did not, which is the reading it exists for. What git printed is in $SCRATCH/remote.err"
    return 0
  fi
  local champion_branch="fugaro/${RUN_ID}" challenger_branch="fugaro/${CHALLENGER_ID}" has_champion=no has_challenger=no
  if grep -q "refs/heads/${champion_branch}\$" "$SCRATCH/remote.out"; then has_champion=yes; fi
  if grep -q "refs/heads/${challenger_branch}\$" "$SCRATCH/remote.out"; then has_challenger=yes; fi
  if [ "$has_champion" != yes ]; then
    fact branch FAIL "the sandbox repository holds no refs/heads/${champion_branch} (git ls-remote --heads origin 'refs/heads/fugaro/*' is in $SCRATCH/remote.out): the champion publishes exactly as an unsampled run does (A2.1), so a pair whose champion left no branch is a launch that did not reach its push rather than a reading about the shadow. Its record's own status is $(field "$SCRATCH/champion.txt" status)"
    return 0
  fi
  if [ "$has_challenger" = yes ]; then
    fact branch FAIL "the sandbox repository holds refs/heads/${challenger_branch}: the challenger PUSHED, and a shadow pushes nothing (D554, A2.1). This is the finding this check exists for — the arm is not the run the release describes — and the branch is left for you to delete. git ls-remote --heads origin 'refs/heads/fugaro/*' is in $SCRATCH/remote.out"
    return 0
  fi
  fact branch PASS "git ls-remote on the sandbox repository holds refs/heads/${champion_branch} and no branch at all for the challenger (${challenger_branch}): of the pair's two arms exactly one has a branch, and it is the champion's — read from the remote, not from the record that claims it"
}

# step_pr: the other half of the plan's sentence, read where the runner writes
# it. The champion's record carries its pull request number at finalize and the
# challenger's carries none, which is what "one pull request" means for a pair:
# the shadow opened nothing, whatever else the run did.
step_pr() {
  if [ ! -s "$SCRATCH/champion.txt" ] || [ ! -s "$SCRATCH/challenger.txt" ]; then
    unmeasured pr "there are no two records to read the pull requests from: a row above says why"
    return 0
  fi
  local champion_pr challenger_pr
  champion_pr=$(field "$SCRATCH/champion.txt" pr)
  challenger_pr=$(field "$SCRATCH/challenger.txt" pr)
  if [ "${champion_pr:-0}" = 0 ]; then
    fact pr FAIL "the champion's record carries no pull request number (status=$(field "$SCRATCH/champion.txt" status), outcome=$(field "$SCRATCH/champion.txt" outcome), reason=$(field "$SCRATCH/champion.txt" reason)): the champion opens one exactly as an unsampled run does, so a pair whose champion opened none did not reach its publish, and the release's promise — a sampled task still leaves one pull request — has nothing to be read from here. The record is in $SCRATCH/champion.out"
    return 0
  fi
  if [ "${challenger_pr:-0}" != 0 ]; then
    fact pr FAIL "the challenger's record carries pull request ${challenger_pr}: a shadow opens nothing and comments on nothing (D554, A2.1). This is the finding this check exists for, and the pull request is left for you to close"
    return 0
  fi
  fact pr PASS "exactly one pull request came out of the pair: the champion's record carries pr ${champion_pr} and the challenger's carries none. Read from the two records' own pr fields — the teardown below closes the ${champion_pr} and every other pull request these launches opened"
}

# step_patches: A2.1's other half — BOTH arms upload their patch object, the
# champion included, and the record names the key. The key is what a later
# judge reads (D728), so an arm without one cannot be scored.
step_patches() {
  if [ ! -s "$SCRATCH/champion.txt" ] || [ ! -s "$SCRATCH/challenger.txt" ]; then
    unmeasured patch-objects "there are no two records to read the patch keys from: a row above says why"
    return 0
  fi
  local arm key missing="" found="" truncated=""
  for arm in champion challenger; do
    key=$(field "$SCRATCH/$arm.txt" patch_object)
    if [ -z "$key" ]; then
      missing="${missing}${missing:+, }${arm}"
      continue
    fi
    run_bounded "$CLI_TIMEOUT" "patch-$arm" "$SANDBOX" gcloud storage ls "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      missing="${missing}${missing:+, }${arm} (${key} is not in the bucket)"
      continue
    fi
    found="${found}${found:+, }${arm}=${key}"
    if [ "$(field "$SCRATCH/$arm.txt" patch_truncated)" = true ]; then
      truncated="${truncated}${truncated:+, }${arm}"
    fi
  done
  if [ -n "$missing" ]; then
    fact patch-objects FAIL "the bucket does not hold a patch object for ${missing}: both arms upload runs/<slug>/<run id>/ab/patch.diff and record its key in patch_object (A2.1) — a judge reads the pair's two patches from those keys (D728), so an arm without one is an arm no pair can be scored from. The keys this check looked for are in $SCRATCH/patch-*.err, and the records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  fact patch-objects PASS "both arms' patch objects are in the bucket — ${found} — so the pair carries the two tree diffs the judge reads (A2.1, D728)${truncated:+, with ${truncated} truncated at the 2 MiB cap, which the record says in patch_truncated}"
}

# step_report: the pair through the command the owner reads pairs with. A pair
# the launcher made and the report does not show is a pair nobody can act on,
# and `fugaro ab` is the surface the release notes name.
step_report() {
  if [ -z "$CHALLENGER_MODEL" ]; then
    unmeasured ab-report "this check does not know the challenger's model (the challenger row above says why), so it cannot look for the pair in fugaro ab's rows"
    return 0
  fi
  say "reading the pairs report: fugaro ab --repo ${REPO_NAME:-<the sandbox repository>} --since ${SINCE} --json"
  run_bounded "$CLI_TIMEOUT" ab "$SANDBOX" "$FUGARO_BIN" ab --repo "$REPO_NAME" --since "$SINCE" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured ab-report "fugaro ab --json did not answer (exit ${BOUNDED_STATUS}): the report is how you read the pairs your installation sampled, and it could not be read here. Its output is in $SCRATCH/ab.out and $SCRATCH/ab.err"
    return 0
  fi
  if ! read_json ab "$SCRATCH/ab.out" "$SCRATCH/ab.txt"; then
    unmeasured ab-report "fugaro ab --json printed a document this check could not read: what it printed is in $SCRATCH/ab.out"
    return 0
  fi
  local line pairs="" seen=""
  while IFS= read -r line; do
    case "$line" in ab_row*) ;; *) continue ;; esac
    seen="${seen}${seen:+, }$(printf '%s' "$line" | awk -F'\t' '{ printf "%s in mode %s (%s pair(s))", $5, $4, $6 }')"
    if [ "$(printf '%s' "$line" | awk -F'\t' '{ print $5 }')" = "$CHALLENGER_MODEL" ] &&
      [ "$(printf '%s' "$line" | awk -F'\t' '{ print $6 }')" -ge 1 ]; then
      pairs="$line"
    fi
  done <"$SCRATCH/ab.txt"
  if [ -z "$pairs" ]; then
    fact ab-report FAIL "fugaro ab --repo ${REPO_NAME} --since ${SINCE} --json carries no row for the challenger model ${CHALLENGER_MODEL} with a pair in it (its rows name ${seen:-nothing}): the launcher sampled a pair and the command the owner reads pairs with does not show it, so the pair the pair launch exists for is one nobody can act on. Its output is in $SCRATCH/ab.out"
    return 0
  fi
  fact ab-report PASS "fugaro ab --repo ${REPO_NAME} --since ${SINCE} --json shows the pair — mode $(printf '%s' "$pairs" | awk -F'\t' '{ print $2 }'), tier $(printf '%s' "$pairs" | awk -F'\t' '{ print $3 }'), champion $(printf '%s' "$pairs" | awk -F'\t' '{ print $4 }'), challenger $(printf '%s' "$pairs" | awk -F'\t' '{ print $5 }'), pairs $(printf '%s' "$pairs" | awk -F'\t' '{ print $6 }') — so a pair the launcher made is a pair the report scores"
}

# restore_rows: the runs list as the teardown reads it, and whether the read
# worked. It is read again after every cancel below, because a run that was
# going can push its branch and open its pull request in the seconds before the
# cancel reaches it, and a teardown that read the list once would report what
# the run had left when it looked.
restore_rows() {
  run_bounded "$CLI_TIMEOUT" teardown "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then return 1; fi
  read_json rows "$SCRATCH/teardown.out" "$SCRATCH/teardown.txt"
}

# restore_wait_ended ID: wait, up to CANCEL_TIMEOUT, for a run this check
# cancelled to read as ended. `fugaro cancel --now` cancels the execution
# through the backend, and the run's own row is what says it stopped.
restore_wait_ended() {
  local waited=0 row
  while [ "$waited" -lt "$CANCEL_TIMEOUT" ]; do
    restore_rows || return 1
    row=$(rows_row "$SCRATCH/teardown.txt" "$1")
    if [ -n "$row" ] && [ "$(rcol "$row" 4)" = true ]; then return 0; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  return 1
}

# restore: what this check changed, said rather than assumed. It is the teardown
# the plan asks a check of this shape to carry, and nothing this check launches
# outlives it: a launched run that has not ended is cancelled with `fugaro cancel
# <run> --now` and read back as ended before its branch and its pull request are
# dealt with, because a run still going pushes and opens AFTER a teardown that
# read only what it had left so far — a Restore that skipped it would read PASS
# over a push that had not happened yet. Then every pull request these launches
# opened is closed and every branch they pushed is deleted, each read back. A
# pull request already closed and a branch already gone are the state it wants,
# not a failure; a launched run still going after the cancel, or one the runs
# list does not carry at all, IS one — the row names it, and names the commands
# that settle it by run id, because this check's list of its own launches goes
# with its scratch directory and running the check again does not reach them.
restore() {
  RESTORE_VERDICT=PASS
  local ids="" id row branch pr close_failed="" deleted="" kept="" note=""
  local going="" absent="" cancelled="" cancel_status="" cancel_note="" why=""
  if [ ! -s "$LAUNCHES_FILE" ] || [ -z "$PROJECT" ]; then
    RESTORE_NOTE="nothing was created: ${LAUNCHED:-no} launch(es) were made and no branch or pull request of this check's own is left. The scratch directory held the commands' output"
    return 0
  fi
  ids=$(sort -u "$LAUNCHES_FILE" | tr '\n' ' ')
  if ! restore_rows; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and their branches and pull requests could not be read back (fugaro runs ls --json exited ${BOUNDED_STATUS}; its output is in $SCRATCH/teardown.out and $SCRATCH/teardown.err), so nothing was closed or deleted. Settle them by run id: read one with 'fugaro runs show <run>', stop one that is still going with 'fugaro cancel <run> --now', then close its pull request with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' and delete its branch with 'git push origin --delete <branch>' in ${SANDBOX}"
    return 0
  fi
  [ -n "$REPO_NAME" ] || REPO_NAME=$(field "$SCRATCH/teardown.txt" repo)
  for id in $ids; do
    row=$(rows_row "$SCRATCH/teardown.txt" "$id")
    if [ -z "$row" ]; then
      absent="${absent}${absent:+, }${id}"
      continue
    fi
    if [ "$(rcol "$row" 4)" != true ]; then
      run_bounded "$CANCEL_TIMEOUT" "teardown-cancel" "$SANDBOX" "$FUGARO_BIN" cancel "$id" --now --json
      if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
        going="${going}${going:+, }${id} ('fugaro cancel ${id} --now' exited ${BOUNDED_STATUS})"
        continue
      fi
      cancel_status=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("status") or ""))' "$SCRATCH/teardown-cancel.out" 2>/dev/null || true)
      if ! restore_wait_ended "$id"; then
        going="${going}${going:+, }${id} (still going after 'fugaro cancel ${id} --now' answered '${cancel_status:-no status}')"
        continue
      fi
      cancelled="${cancelled}${cancelled:+, }${id}"
      row=$(rows_row "$SCRATCH/teardown.txt" "$id")
    fi
    branch=$(rcol "$row" 7)
    pr=$(rcol "$row" 8)
    if [ -n "$branch" ]; then
      run_bounded "$CLI_TIMEOUT" "teardown-ls" "$SANDBOX" git ls-remote --heads origin "refs/heads/${branch}"
      # A branch the remote no longer holds is the state this wants, not a
      # failure: the teardown may be the second one to run, and deleting a
      # branch twice is not a reading of anything.
      if [ "$BOUNDED_STATUS" = 0 ] && grep -q "refs/heads/${branch}\$" "$SCRATCH/teardown-ls.out"; then
        run_bounded "$CLI_TIMEOUT" "teardown-rm" "$SANDBOX" git push origin --delete "$branch"
        if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
          close_failed="${close_failed}${close_failed:+, }the branch ${branch} (git push --delete exited ${BOUNDED_STATUS})"
        else
          run_bounded "$CLI_TIMEOUT" "teardown-ls2" "$SANDBOX" git ls-remote --heads origin "refs/heads/${branch}"
          if [ "$BOUNDED_STATUS" = 0 ] && grep -q "refs/heads/${branch}\$" "$SCRATCH/teardown-ls2.out"; then
            close_failed="${close_failed}${close_failed:+, }the branch ${branch} (still on the remote after git push --delete)"
          else
            deleted="${deleted}${deleted:+, }${branch}"
          fi
        fi
      fi
    fi
    if [ "${pr:-0}" != 0 ]; then
      if [ -z "$GH_BIN" ]; then
        kept="${kept}${kept:+, }pull request ${pr}"
        continue
      fi
      run_bounded "$CLI_TIMEOUT" "teardown-pr" "$SANDBOX" "$GH_BIN" pr view "$pr" --repo "$REPO_NAME" --json state
      local state=""
      if [ "$BOUNDED_STATUS" = 0 ]; then
        state=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("state") or ""))' "$SCRATCH/teardown-pr.out" 2>/dev/null || true)
      fi
      if [ "$state" = OPEN ]; then
        run_bounded "$CLI_TIMEOUT" "teardown-close" "$SANDBOX" "$GH_BIN" pr close "$pr" --repo "$REPO_NAME"
        if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
          close_failed="${close_failed}${close_failed:+, }pull request ${pr} (gh pr close exited ${BOUNDED_STATUS})"
          continue
        fi
      fi
      run_bounded "$CLI_TIMEOUT" "teardown-check" "$SANDBOX" "$GH_BIN" pr view "$pr" --repo "$REPO_NAME" --json state
      state=""
      if [ "$BOUNDED_STATUS" = 0 ]; then
        state=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("state") or ""))' "$SCRATCH/teardown-check.out" 2>/dev/null || true)
      fi
      if [ "$state" != CLOSED ]; then
        close_failed="${close_failed}${close_failed:+, }pull request ${pr} (read back as '${state:-unreadable}')"
      fi
    fi
  done
  if [ -n "$going" ]; then
    why="these launches had not ended: 'fugaro cancel <run> --now' was run on each of them and the runs list still reads them as going — ${going}"
  fi
  if [ -n "$absent" ]; then
    why="${why}${why:+; }the runs list does not carry these launches at all: ${absent}"
  fi
  if [ -n "$why" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and ${why}. A run that is still going is settled by nothing this teardown reads — it can push its branch and open its pull request after this check is over — so settle each one by run id: read it with 'fugaro runs show <run>', stop it with 'fugaro cancel <run> --now', then close its pull request with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' and delete its branch with 'git push origin --delete <branch>' in ${SANDBOX}. Running this check again does not settle them: the list of the runs it launched is removed with its scratch directory"
    return 0
  fi
  if [ -n "$close_failed" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and ${close_failed} could NOT be closed or deleted. Close the pull request with 'gh pr close <n> --repo ${REPO_NAME}' and delete the branch with 'git push origin --delete <branch>' in ${SANDBOX}: closing a closed pull request and deleting a deleted branch are the state this wants, so both commands are safe to run again"
    return 0
  fi
  note="${deleted:+ deleted ${deleted};}"
  if [ -n "$kept" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}their branches were${note:-} deleted, and ${kept} could NOT be closed: 'gh' is not on PATH, and a pull request is the one thing no other tool here closes. Close each one with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' — they are sandbox pull requests and closing them is the whole of it"
    return 0
  fi
  if [ -n "$cancelled" ]; then
    cancel_note="the launches that had not ended (${cancelled}) were cancelled with 'fugaro cancel <run> --now' and read back as ended, and "
  fi
  RESTORE_NOTE="the runs this check launched are ${ids}${cancel_note}every branch they pushed was deleted${note:-} and every pull request their champions opened is closed, each read back from the forge. The scratch directory held the commands' output; the records stay in the bucket, which is what the rows above were read from"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 210 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro ab --help — the release ${RELEASE} gate: the build must carry the sampler, the pair launch and the pairs report"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [network, read] fugaro config layer --json in ${SANDBOX} — the published project layer, whose ab: block must set rate ${RATE_WANTED} and name a challenger (and ${SANDBOX}/fugaro.yaml must carry no opt-out)"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --json \"${TASK}\" — up to ${MAX_LAUNCHES} sandbox runs, stopped at the first pair. About ten are expected at ${RATE_WANTED}; nothing forces a sample"
  say "  [read] fugaro runs ls --repo ${SANDBOX} --batch <the pair's label> --json — the two arms, both ended"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the slug>/<run>/result.json for BOTH arms — the ab block, the outcome, the branch, the pull request and the patch key"
  say "  [network, read] git ls-remote --heads origin 'refs/heads/fugaro/*' in ${SANDBOX} — the remote's own branch list, which is where 'exactly one branch' is read"
  say "  [read] gcloud storage ls of both arms' patch_object keys"
  say "  [read] fugaro ab --repo <the sandbox repository> --since ${SINCE} --json — the pair through the command you read pairs with"
  say "  [WRITE, the teardown] a launch that has not ended is stopped with fugaro cancel --now and read back, then every branch these launches pushed is deleted and every pull request their champions opened is closed with gh, each read back"
  for key in release sandbox layer pair arms champion challenger branch pr patch-objects ab-report; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no layer read, no launch, no remote read and no teardown"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  layer pair arms champion challenger branch pr patch-objects ab-report
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 210 RESULT\n' >&3
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
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 210 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
TASK_CLI=""
MAX_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the runs are launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text each launch is launched with; it has to be small, because the check launches up to ${MAX_LAUNCHES:-30} of them"
    TASK_CLI=$2
    shift 2
    ;;
  --max-launches)
    [ $# -ge 2 ] || die "--max-launches needs a count: how many launches the check may spend looking for a pair (the default is 30, the bound the plan sets)"
    MAX_CLI=$2
    shift 2
    ;;
  --yes)
    YES_CLI=1
    shift
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

# A value given as a flag wins over the same value in the environment: the
# environment is how a script or a case sets a knob, and the flag is what the
# owner typed, so a run that quietly used the other one would be spending the
# sandbox's money at a bound its owner did not choose.
SANDBOX=${SANDBOX_CLI:-${CHECK210_SANDBOX:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 210 needs --sandbox: the checkout of a repository of the sandbox project. It is what the runs are launched from and what its configuration is read through. Usage: bash scripts/live-checks/check210.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${TASK_CLI:-${CHECK210_TASK:-"Add a line reading 'check 210' to CHECK210.md, and change nothing else."}}
MAX_LAUNCHES=${MAX_CLI:-${CHECK210_MAX_LAUNCHES:-30}}
case "$MAX_LAUNCHES" in
"" | *[!0-9]*) die "--max-launches needs a whole number, not '${MAX_LAUNCHES}'" ;;
esac
[ "$MAX_LAUNCHES" -ge 1 ] || die "--max-launches needs at least 1"
# The bound the teardown reads the runs list with: its own launches are the
# newest rows, and a pair is two rows, so the window is twice the bound plus
# room for the rows another run of the sandbox added beside them.
LS_LIMIT=$((MAX_LAUNCHES * 2 + 10))
RELEASE=${CHECK210_RELEASE:-0.24.0}
# The rate the plan's own sentence names: the check is written for it, and the
# sampler's ceiling is the same figure (D557), so a project cannot sample harder.
RATE_WANTED=${CHECK210_RATE:-0.1}
SINCE=${CHECK210_SINCE:-1d}
YES=${CHECK210_YES:-$YES_CLI}
DRY_RUN=${CHECK210_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK210_KEEP_SCRATCH:-}
REGION=${CHECK210_REGION:-us-east5}
RUN_TIMEOUT=${CHECK210_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK210_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK210_CLI_TIMEOUT_SECONDS:-180}
# The bound for both halves of the teardown's cancel: the `fugaro cancel --now`
# this check runs on a launched run that has not ended, and the wait for that
# run's row to read as ended afterwards.
CANCEL_TIMEOUT=${CHECK210_CANCEL_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK210_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK210_DOCTOR_TIMEOUT_SECONDS:-300}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there. `gh` is not required —
# the branches are deleted without it — so a missing one is left empty and the
# restore row says which pull requests are therefore yours to close.
FUGARO_BIN=$(command -v fugaro || true)
GH_BIN=$(command -v gh || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 210 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  step_release
  step_bound
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$YES" ]; then
    die "Check 210 launches up to ${MAX_LAUNCHES} sandbox runs and spends what their tasks cost, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check210.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_layer
  step_pair
  step_arms
  step_champion
  step_challenger
  step_branch
  step_pr
  step_patches
  step_report
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
