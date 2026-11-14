#!/usr/bin/env bash
# Check 490 — a trivial finding is deferred, and the run still ends ready.
#
# Plan task T-TR13 writes this check; what it settles is
# docs/post-review-triage/implementation-plan.md's "Checks" block and the two
# tasks it builds on: T-TR8 (a Startup run whose reviewer reports only a trivial
# finding finishes ready, with the finding recorded as a follow-up) and T-TR7b
# (the triage block in `result.json` and the Deferred section in the pull
# request). The failure it removes is the one the work exists for: a run that
# pays for a fix round over a one-word documentation nit, or that ends a draft
# because a reviewer would not let it go.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     feature itself (`fugaro followups --help` answers, which is the command the
#     same work ships, and `fugaro runs ls --help` names `--debt`). The work is
#     unreleased until 0.24.0, so a build without it stops here with the release
#     named rather than measuring a run that cannot carry a triage block;
#   * the premise, read from the sandbox checkout: the repository does not turn
#     the step off (`agent.triage: false` in its own `fugaro.yaml`). A repository
#     that does carries no triage block whatever the run does, so the check stops
#     before spending;
#   * ONE sandbox run, launched with `fugaro run --mode startup --json` over a
#     task this check writes: it asks for exactly one word of one sentence in one
#     documentation file to be changed so the sentence no longer matches the code
#     — the plan's own trivial finding. Nothing it launches can outlive it: a run
#     that has not ended by CHECK490_RUN_TIMEOUT_SECONDS is cancelled with
#     `fugaro cancel --now`, from the step that gives up on it AND from the exit
#     trap;
#   * `result.json`, read from the runs bucket at the key the listing's row
#     gives: the run ended **ready** after **one** review round that reported a
#     finding, the mode it resolved is one the triage step governs, and the
#     record's `triage` block holds a round whose entry for that finding is
#     `defer` from a source outside the floor;
#   * the deferral that took effect: `deferred.findings` names the same key the
#     triage round deferred, so the verdict and the follow-up are one finding and
#     not two;
#   * the pull request's `Deferred (follow-ups)` section, read with `gh pr view`:
#     it carries the key and the reviewer's own summary, which is what an owner
#     reads before `fugaro followups` — Check 491 files from exactly this.
#
# It creates nothing: no job of its own, no build, no binding and no account.
# The one thing it starts is the sandbox run, and it leaves nothing behind but
# the run, its pull request and its branch, which are yours to close.
#
# Usage:      bash scripts/live-checks/check490.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check490.sh --sandbox <path> --yes --task "<the task>"
#             bash scripts/live-checks/check490.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check490.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents and the pull
#             request are read with it), gcloud (authenticated as YOU, and able
#             to read the runs bucket), fugaro (the installed release: the whole
#             point is the build you have) and gh (the pull request's own body,
#             which no command of this CLI prints).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    one sandbox run, its branch and its pull request — the run is the
#             check's own and it cancels a run that will not end; the pull
#             request is left OPEN, because Check 491 files from it after you
#             merge it. On disk: one scratch directory, removed at the end.
# Costs:      ONE sandbox run and what its task costs — the model spend of a
#             documentation change and its review rounds, on the sandbox's own
#             budget — plus the pull request that run opens. Every other call is
#             a read (doctor, the runs list, the record, `gh pr view`), and
#             `--yes` is required before anything is launched.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check490_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the run, cancelled from that same trap
#             if it has not ended. The pull request and its branch are left for
#             you: Check 491 needs them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 490 RESULT,
#             then CHECK 490 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or the run never ended
#             and had to be cancelled. A key the run never reached reads
#             `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry finding
# keys, one-line summaries the reviewer wrote, statuses and dollars.
#
# Knobs (all CHECK490_*): DRY_RUN, YES, KEEP_SCRATCH, SANDBOX (the checkout, when
# you would rather set it in the environment), DOC (the documentation file the
# task names, README.md by default), TASK (what the run is launched with),
# RECIPE, RELEASE (0.24.0: the release this work ships in), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180),
# LAUNCH_TIMEOUT_SECONDS (600), CANCEL_TIMEOUT_SECONDS (300),
# DOCTOR_TIMEOUT_SECONDS (300), MAX_MODEL_USD (unset: the run's figure is
# reported, not graded).
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
  command -v bash >/dev/null 2>&1 || die "Check 490 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro gh; do
    command -v "$t" >/dev/null 2>&1 || die "Check 490 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk490-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
PR_URL=""
SETTLED=no
CANCEL_DONE=no
CANCEL_OK=""
MODE_OK=no
# PREMISE_OK is set by the one branch of step_premise that establishes the state
# the run is a reading of, and step_run reads it and nothing else: a run launched
# after a premise that did not hold would spend the sandbox's money on a reading
# nobody could grade.
PREMISE_OK=no
# RECORD_OK is set by the one branch of step_record that read THIS run's record,
# and every row below it reads it and nothing else: a record that is another
# run's is not a reading of this run, and a step that went on anyway would report
# fields no run of this check ever wrote.
RECORD_OK=no
# DEFER_KEY is the key the triage round deferred: the record's deferrals and the
# pull request's section are both read against it, so the three readings are the
# same finding rather than three that happen to be there.
DEFER_KEY=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk490.XXXXXXXX") || die "Check 490 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
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
    say "CHECK490_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a field the release did not write the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so a
# shell can hold them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("project=%s" % (project.get("gcp_project") or ""))
    print("ok=%s" % yes(doc.get("ok")))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
            row.get("pr") or 0,
            row.get("pr_url") or "",
            row.get("outcome") or "",
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    rigor = doc.get("rigor") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("failure_class=%s" % (doc.get("failure_class") or ""))
    print("reason=%s" % flat(doc.get("reason")))
    print("mode=%s" % (rigor.get("mode") or ""))
    print("mode_from=%s" % (rigor.get("from") or ""))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
    print("pr=%s" % (doc.get("pr") or 0))
    stages = doc.get("stages") or []
    print("stage_count=%d" % len(stages))
    for stage in stages:
        print("stage\t%s\t%s" % (stage.get("name") or "", flat(stage.get("model"))))
    reviews = doc.get("reviews") or []
    print("review_count=%d" % len(reviews))
    for review in reviews:
        print("review\t%s\t%s\t%s\t%s\t%s" % (
            review.get("round") or 0,
            flat(review.get("tier")),
            flat(review.get("verdict")),
            review.get("findings") or 0,
            flat(review.get("head")),
        ))
    triage = doc.get("triage") or {}
    rounds = triage.get("rounds") or []
    print("triage_rounds=%d" % len(rounds))
    for rnd in rounds:
        print("triage\t%s\t%s\t%s\t%s\t%s" % (
            rnd.get("round") or 0,
            flat(rnd.get("mode")),
            flat(rnd.get("level")),
            yes(rnd.get("failed")),
            rnd.get("total") or 0,
        ))
        for entry in rnd.get("entries") or []:
            print("triage-entry\t%s\t%s\t%s\t%s\t%s\t%s" % (
                rnd.get("round") or 0,
                entry.get("index") or 0,
                entry.get("key") or "",
                flat(entry.get("verdict")),
                flat(entry.get("source")),
                flat(entry.get("reason")),
            ))
    deferred = doc.get("deferred") or {}
    findings = deferred.get("findings") or []
    print("deferred_count=%d" % len(findings))
    print("deferred_total=%s" % (deferred.get("total") or 0))
    for f in findings:
        print("deferred\t%s\t%s\t%s\t%s\t%s" % (
            f.get("key") or "",
            flat(f.get("summary")),
            flat(f.get("reason")),
            flat(f.get("group")),
            flat(f.get("source")),
        ))
elif what == "prdoc":
    print("number=%s" % (doc.get("number") or 0))
    print("state=%s" % flat(doc.get("state")))
    print("is_draft=%s" % yes(doc.get("isDraft")))
    print("body=%s" % flat(doc.get("body")))
elif what == "cancel":
    print("status=%s" % (doc.get("status") or ""))
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
  # `|| true`: a reader that found nothing is an empty answer rather than a
  # failure, and under `set -o pipefail` the `head` that closes the pipe early is
  # enough to make this pipeline non-zero.
  { sed -n "s/^$2=//p" "$1" 2>/dev/null || true; } | head -n 1 || true
}

# rows_field COLUMN: one column of one `tab`-separated row, "" when the kind of
# row or the value is not there. The readers print values that hold a `|` or a
# `=` unmangled, so a row is cut by tab and never by a separator a value could
# carry.
rows_field() { # rows_field KIND COLUMN [MATCH]
  awk -F'\t' -v kind="$1" -v col="$2" -v want="${3:-}" '
    $1 == kind && (want == "" || $2 == want) { print $col; exit }
  ' "$SCRATCH/record.txt" 2>/dev/null || true
}

# run_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
run_field() { # run_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null || true
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a cancel that
# had nothing to do, a record that is not there), and a trap inherited here
# would turn each of them into "an internal error" and a status of 2 before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

# cancel_run WHY: stop the run this check started, once, and say whether it
# worked. It is what makes "launch nothing it cannot cancel" true rather than
# intended: the step that gives up waiting calls it, and so does the exit trap,
# so a Ctrl-C in the middle cancels the run too. A run that had already ended is
# not cancelled — the record below is read from the ending it reached.
cancel_run() { # cancel_run WHY
  [ -n "$RUN_ID" ] || return 0
  [ "$SETTLED" = yes ] && return 0
  [ "$CANCEL_DONE" = yes ] && return 0
  CANCEL_DONE=yes
  say "cancelling ${RUN_ID} ($1): fugaro cancel ${RUN_ID} --now"
  run_bounded "$CANCEL_TIMEOUT" cancel "$SANDBOX" "$FUGARO_BIN" cancel "$RUN_ID" --now --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    CANCEL_OK=no
    fact cancelled FAIL "the run ${RUN_ID} was cancelled with 'fugaro cancel ${RUN_ID} --now' and the command did not answer (exit ${BOUNDED_STATUS}): the run may still be going. Read it with 'fugaro runs show ${RUN_ID}' and cancel it yourself. What the command printed is in $SCRATCH/cancel.out and $SCRATCH/cancel.err"
    return 0
  fi
  CANCEL_OK=yes
  fact cancelled PASS "the run ${RUN_ID} did not end on its own, so this check cancelled it with 'fugaro cancel ${RUN_ID} --now' (status $(field "$SCRATCH/cancel.txt" status)): nothing this check launched outlives it"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check490.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused. Refusing here rather than letting
# each command refuse one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 490 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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

# step_release: the gate every check of an unreleased feature carries. The work
# ships in 0.24.0, and a build that predates it has no triage step, no triage
# block and no `followups` command, so a run launched here would be a reading of
# a build nobody shipped this for. Both probes are offline: `fugaro version`, the
# usage line `fugaro followups --help` prints, and the `--debt` line of
# `fugaro runs ls --help`.
step_release() {
  local v feature=yes
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if ! "$FUGARO_BIN" followups --help >/dev/null 2>&1; then feature=no; fi
  if ! "$FUGARO_BIN" runs ls --help 2>/dev/null | grep -q -- '--debt'; then feature=no; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no follow-ups: either 'fugaro followups --help' does not answer or 'fugaro runs ls --help' does not name --debt, so neither the triage block this run is read through nor the command Check 491 runs is in this build. Check 490 needs release ${RELEASE} (the post-review triage work) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro does not carry the triage work: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'followups' and 'runs ls --debt' are there: this build carries the work the run below measures"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the post-review triage work ships in: a run measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and this build answers 'followups --help' and names 'runs ls --debt', so it carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  local extra=""
  if [ -n "$MAX_MODEL_USD" ]; then
    extra=" The run's own figure is graded against CHECK490_MAX_MODEL_USD=${MAX_MODEL_USD}: a run that spends more is a FAIL row, and the money is spent either way."
  fi
  fact bound PASS "ONE sandbox run and what its task costs — a one-word documentation change, its review round and its triage call, on the sandbox's own budget — plus the pull request that run opens and leaves OPEN for Check 491. Every other call this check makes is a read (doctor, the runs list, the record, gh pr view).${extra} --yes is required to launch it"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the run and its record are that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the run this check launches is launched from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get("gcp_project") or "")' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches from this checkout and reads its record in this project's bucket"
}

# step_premise: the state the deferral needs. The triage step is a repository
# switch (`agent.triage: false` turns it off), and a repository that turned it
# off records no triage block whatever the run does — so the check reads the
# switch before it spends rather than reading an empty block afterwards as a
# finding about the release.
step_premise() {
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    unmeasured premise "there is no fugaro.yaml in ${SANDBOX} to read the switch from: the row above says why"
    return 0
  fi
  local off
  off=$(grep -nE '^[[:space:]]*triage:[[:space:]]*false[[:space:]]*(#.*)?$' "$SANDBOX/fugaro.yaml" 2>/dev/null | head -n 1 || true)
  if [ -n "$off" ]; then
    fact premise FAIL "${SANDBOX}/fugaro.yaml turns the step off (${off}): a repository that writes agent.triage: false records no triage block at all, so a run launched here could not answer this check either way. Point --sandbox at a repository whose checkouts leave triage on, or drop the key from this one and run the check again — the switch is the repository's own, and nothing here changes it"
    return 0
  fi
  PREMISE_OK=yes
  fact premise PASS "${SANDBOX}/fugaro.yaml does not turn the step off (no agent.triage: false), so the run below records a triage block when its review reports a finding"
}

# step_run: one sandbox run over the trivial finding. `--json` is what names the
# run this check then follows, and — because the run is cancelled rather than
# left going — a launch that prints no run ID is a finding about the launcher
# rather than a row this check could not measure.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  # A run is launched only once the premise has named the state it is a reading
  # of. The check spends the sandbox's money, and a run launched after a premise
  # that did not hold would spend it on a reading nobody could grade.
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured run-launched "nothing was launched: the premise row above did not establish the state this run is a reading of, and a run launched without it would spend the sandbox's money on a reading nobody could grade. Settle the premise row and run this check again"
    return 0
  fi
  say "launching one sandbox run from ${SANDBOX} (a real run: implement, review, triage)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" --mode startup ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the check needs a run to read a record from, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named — and how this check would cancel it. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched and cancels it if it does not end, and a launch that does not name it can be neither. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout in mode startup (status $(field "$SCRATCH/launch.txt" status)), over a task asking for one word of ${DOC}: the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's, and the record of a run that is
# still working is not the reading this check is after.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 1)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 1
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(run_field 3)
      ended=$(run_field 4)
      stage=$(run_field 5)
      RUN_REF=$(run_field 6)
      PR_URL=$(run_field 8)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    cancel_run "it had not ended after ${RUN_TIMEOUT}s"
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): a documentation run with one review round ends well inside that, so a run still going is one this check could not measure — and it was cancelled rather than left behind. What the cancel printed is in $SCRATCH/cancel.out and $SCRATCH/cancel.err. Read the run with 'fugaro runs show ${RUN_ID}' before running this check again"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end on its own, and nothing had to be cancelled"
  if [ "$CANCEL_DONE" != yes ]; then
    fact cancelled INFO "not needed: the run ended by itself in ${waited}s, so this check cancelled nothing. It would have cancelled it with 'fugaro cancel --now' from the step that gives up waiting and from its own exit trap"
  fi
}

# step_record: the record, read from the runs bucket as the owner would read it.
# The record's own run_id is what ties it to the run this check launched.
step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up and this check cancelled it, so there is no terminal record to read: a run that never reached its end is not the reading this check is after. Read the run with 'fugaro runs show ${RUN_ID}' and run this check again"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured result-json "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=${RUN_REF:-none}), so there was no key to read: the record of run ${RUN_ID} lives under the repository's slug, and the slug is what that row's ref carries"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where its outcome, its triage block and its deferrals are written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  if [ -z "$PR_URL" ]; then
    PR_URL=$(pr_url_from_record)
  fi
  RECORD_OK=yes
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), mode=$(field "$SCRATCH/record.txt" mode), $(field "$SCRATCH/record.txt" stage_count) stage(s), model_usd=$(field "$SCRATCH/record.txt" cost_usd)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# pr_url_from_record: the pull request's own address, built from the record when
# the runs list carried none. The slug is the repository the run was launched on
# and the number is the record's own, which is the pair `gh` needs.
pr_url_from_record() {
  local n repo
  n=$(field "$SCRATCH/record.txt" pr)
  repo=$LAUNCH_REPO
  case "$n" in "" | 0) return 0 ;; esac
  case "$repo" in */*) printf 'https://github.com/%s/pull/%s\n' "$repo" "$n" ;; esac
}

# step_mode: the mode the run resolved. The plan's reading is a Startup run, and
# the two modes the step governs are startup and pro: a mode outside them — a
# hotfix, a critical or a POC run — never triages, so a record from one carries
# no block to read. `--mode` raises a mode and never lowers one, so a sandbox
# repository whose own rules raise it is a finding about the checkout rather than
# about the release.
step_mode() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured mode "there is no record of this run to read the mode from: the row above says why"
    MODE_OK=no
    return 0
  fi
  local mode from
  mode=$(field "$SCRATCH/record.txt" mode)
  from=$(field "$SCRATCH/record.txt" mode_from)
  case "$mode" in
  startup | pro)
    MODE_OK=yes
    fact mode PASS "the run resolved mode ${mode} (from ${from:-unknown}), which the triage step governs: the deferral below is a reading of a mode that writes one"
    ;;
  "")
    fact mode FAIL "the record carries no rigor block at all, so this check cannot say which mode the run resolved: every row below is about a mode that defers findings, and a record without one is not that reading. The record is in $SCRATCH/record.out"
    MODE_OK=no
    ;;
  *)
    fact mode FAIL "the run resolved mode ${mode} (from ${from:-unknown}) rather than startup or pro, and the triage step governs neither: this checkout's own rules, or the mode it carries, raised the run out of the modes that defer. Point --sandbox at a checkout that resolves to startup, or run this check against a repository whose rules leave it there. The record is in $SCRATCH/record.out"
    MODE_OK=no
    ;;
  esac
}

# step_outcome: the claim Check 490 exists for. A run whose reviewer left one
# trivial finding finishes READY — not a draft waiting on it and not stopped —
# and it does so after ONE review round, because the finding was deferred rather
# than fixed.
step_outcome() {
  if [ "$RECORD_OK" != yes ] || [ "$MODE_OK" != yes ]; then
    unmeasured outcome "this run's reading is not graded: the record row or the mode row above did not establish the state the row is about, and a row read past them would report fields of a run nobody could grade"
    return 0
  fi
  local status outcome reason rounds findings
  status=$(field "$SCRATCH/record.txt" status)
  outcome=$(field "$SCRATCH/record.txt" outcome)
  reason=$(field "$SCRATCH/record.txt" reason)
  rounds=$(field "$SCRATCH/record.txt" review_count)
  findings=$(rows_field review 5 "1")
  if [ "$status" != succeeded ] || [ "$outcome" != ready ]; then
    fact outcome FAIL "the run ended status=${status:-unknown}, outcome=${outcome:-none}${reason:+, reason: ${reason}}: a Startup run whose review left one trivial finding finishes ready, and this one did not — either the reviewer reported something the mode blocks (and the run went to a fix or stopped) or it reported nothing at all. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "${rounds:-0}" != 1 ]; then
    fact outcome FAIL "the run ended ready but after ${rounds:-0} review rounds rather than one: the finding this check is about is deferred in the first round, and a second round means it was worked on instead — the fix round the triage step exists to avoid. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "${findings:-0}" -lt 1 ]; then
    fact outcome FAIL "the run ended ready after one review round that reported no finding at all: the task asks for a sentence that no longer matches the code, and the deferral this check measures needs the reviewer to report it. Re-run with CHECK490_TASK naming a change the reviewer must flag (the row's remedy is a task, not a release). The record is in $SCRATCH/record.out"
    return 0
  fi
  fact outcome PASS "the run ended ready after ONE review round that reported ${findings} finding(s), with no fix round in between: the trivial finding was written down rather than worked on, which is the outcome the triage step exists to produce"
}

# step_triage_block: the block the plan names beside the outcome. It has to be
# there, it has to be for the round the review ran, and it has to hold a `defer`
# for the finding: a run that ends ready with no triage block is a run whose
# finding was dropped, and one whose entry reads `block` is a finding the step
# kept for the fixer.
step_triage_block() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured triage-block "this run's reading is not graded: the record row above did not establish the state the row is about, and a block read past it would be one this run never wrote"
    return 0
  fi
  local rounds total
  rounds=$(field "$SCRATCH/record.txt" triage_rounds)
  if [ "${rounds:-0}" = 0 ]; then
    fact triage-block FAIL "the record carries no triage block at all (field triage is absent or empty): a run whose reviewer reported a finding records the round it judged, and this one has none — which is either the step being off (the premise row above reads the switch) or a build whose runner does not write the block. The record is in $SCRATCH/record.out"
    return 0
  fi
  total=$(rows_field triage 6 "1")
  local deferred reason source
  deferred=$(rows_field triage-entry 5 "1")
  reason=$(rows_field triage-entry 7 "1")
  source=$(rows_field triage-entry 6 "1")
  if [ "$total" -lt 1 ]; then
    fact triage-block FAIL "the triage block's first round holds no verdict for a finding, while the review round reported one: the block and the round are read together, so a round the judge read nothing from is a finding about the judge's call rather than about the deferral. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$deferred" != defer ]; then
    fact triage-block FAIL "the triage block's verdict for the round's finding is '${deferred:-none}' rather than defer (source=${source:-none}, reason: ${reason:-none}): a finding the mode's table would have blocked, which the step kept, is a finding the run must fix — and if it is the floor's, it may not be deferred at all. Read the round in $SCRATCH/record.out and decide which of the two this run is"
    return 0
  fi
  DEFER_KEY=$(rows_field triage-entry 4 "1")
  fact triage-block PASS "the record's triage block holds round 1 with ${total} verdict(s), and the verdict for the review's finding is defer (source=${source:-none}) with the judge's own reason: ${reason:-none} — the plan's triage block, read from result.json"
}

# step_deferred: the deferral that TOOK EFFECT. A verdict in the triage block is
# the judge's word; `deferred.findings` is what the run really left behind, and
# the two have to name the same key — a block that defers one finding while the
# record defers another is a run whose follow-ups do not match its decisions.
step_deferred() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured deferred "this run's reading is not graded: the record row above did not establish the state the row is about, and a deferral read past it would be one this run never wrote"
    return 0
  fi
  local count total
  count=$(field "$SCRATCH/record.txt" deferred_count)
  total=$(field "$SCRATCH/record.txt" deferred_total)
  if [ "${count:-0}" = 0 ]; then
    fact deferred FAIL "the record carries no deferrals at all (field deferred is absent or empty), while the triage block above deferred a finding: the verdict did not take effect, so nothing was written down and Check 491 would have nothing to file. The record is in $SCRATCH/record.out"
    return 0
  fi
  local key summary source sunk=no
  key=$(rows_field deferred 2)
  summary=$(rows_field deferred 3)
  source=$(rows_field deferred 6)
  case " ${DEFER_KEY:-} " in
  *" ${key:-} "*) sunk=yes ;;
  esac
  # The run's own key is opaque, so the check cannot recompute it here; what it
  # can say is whether the deferral it read is the one the block deferred. A
  # record with one verdict and one deferral whose keys differ is a run whose
  # two readings disagree, and that is the finding.
  if [ "$sunk" = no ] && [ "${count:-0}" = 1 ] && [ "${total:-0}" = 1 ]; then
    fact deferred FAIL "the triage block deferred key ${DEFER_KEY:-none} and the record's deferrals name key ${key:-none}: one finding each, and they are not the same finding — the verdict the run recorded and the deferral that took effect do not agree, which is a mismatch between what the judge decided and what the run left behind"
    return 0
  fi
  fact deferred PASS "the record's deferred block lists ${count} of ${total} finding(s): key ${key:-none} with source ${source:-none}, and the reason it was left — ${summary:-no summary}. That key is the one the triage block deferred, so the verdict and the follow-up are one finding"
}

# step_pr_deferred: the section an owner reads. `fugaro followups` files from
# the record, and the pull request is where the owner sees the deferral before
# they merge — the plan names both, so a run that recorded a deferral and wrote
# no section is a finding, not a pass.
step_pr_deferred() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured pr-deferred "this run's reading is not graded: the record row above did not establish the state the row is about, so there is no run whose pull request this check could read"
    return 0
  fi
  if [ -z "$PR_URL" ]; then
    fact pr-deferred FAIL "the run's record names no pull request (pr=${PR_URL:-none}) and its row carries no pr_url: a run that ended ready opens one, and this check reads the Deferred section of that pull request. Read the run with 'fugaro runs show ${RUN_ID}' yourself"
    return 0
  fi
  say "reading the pull request: gh pr view ${PR_URL} --json body,state,isDraft,number"
  run_bounded "$CLI_TIMEOUT" prdoc "$SANDBOX" gh pr view "$PR_URL" --json body,state,isDraft,number
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pr-deferred "gh pr view ${PR_URL} did not answer (exit ${BOUNDED_STATUS}): the Deferred section is the owner's copy of the deferral and no command of this CLI prints it, so this row cannot be measured without gh and a credential that can read the repository. What it printed is in $SCRATCH/prdoc.err"
    return 0
  fi
  if ! read_json prdoc "$SCRATCH/prdoc.out" "$SCRATCH/prdoc.txt"; then
    fact pr-deferred FAIL "gh pr view ${PR_URL} printed a document this check could not read: what it printed is in $SCRATCH/prdoc.out"
    return 0
  fi
  local state body
  state=$(field "$SCRATCH/prdoc.txt" state)
  body=$(field "$SCRATCH/prdoc.txt" body)
  case "$body" in
  *"Deferred (follow-ups)"*) ;;
  *)
    fact pr-deferred FAIL "pull request ${PR_URL} (state=${state:-unknown}) carries no 'Deferred (follow-ups)' section, while the record defers a finding: the owner merges the pull request, and the deferral has to be readable there rather than only in the bucket. The body gh printed is in $SCRATCH/prdoc.txt"
    return 0
    ;;
  esac
  if [ -n "$DEFER_KEY" ]; then
    case "$body" in
    *"$DEFER_KEY"*) ;;
    *)
      fact pr-deferred FAIL "pull request ${PR_URL} has a Deferred section that does not name the key ${DEFER_KEY} the record defers: the section and the record are read as the same finding, and a section naming different keys sends an owner to a follow-up the record does not carry. The body gh printed is in $SCRATCH/prdoc.txt"
      return 0
      ;;
    esac
  fi
  fact pr-deferred PASS "pull request ${PR_URL} (state=${state:-unknown}) carries the Deferred (follow-ups) section, and it names the key ${DEFER_KEY:-the record defers}: the deferral is in the pull request an owner merges, not only in the bucket"
}

# step_cost: what the run spent, reported and graded only when the owner set a
# bound. A one-word documentation change plus its review and its triage call is
# a small figure, and the row is the check's own account of what it cost.
step_cost() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured cost "this run's reading is not graded: the record row above did not establish the state the row is about, and a cost read past it would be another run's"
    return 0
  fi
  local usd
  usd=$(field "$SCRATCH/record.txt" cost_usd)
  if [ -n "$MAX_MODEL_USD" ] && awk -v got="${usd:-0}" -v max="$MAX_MODEL_USD" 'BEGIN { exit (got > max) ? 0 : 1 }'; then
    fact cost FAIL "the run spent \$${usd:-0} of model money, over CHECK490_MAX_MODEL_USD=${MAX_MODEL_USD}: the bound you set is the one this row grades, and the money is spent either way. Raise the knob if the task is simply dearer than you expected"
    return 0
  fi
  fact cost PASS "the run's own figure is \$usd=${usd:-0} (cost.model_usd in the record)$([ -n "$MAX_MODEL_USD" ] && echo ", at or under CHECK490_MAX_MODEL_USD=${MAX_MODEL_USD}"): ONE sandbox run's worth, spent on a documentation change, its review round and its triage call"
}

# restore: what this check changed, said rather than assumed. The run is the one
# thing it started, and a run that had not ended is cancelled here — from the
# exit trap, so a Ctrl-C or a failed assertion cancels it as surely as the
# ordinary way out. The pull request is deliberately NOT closed: Check 491 files
# from it once you merge it.
restore() {
  cancel_run "the check is ending"
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created: no job of this check's own, no build, no binding and no account. The one run it started (${RUN_ID:-none}) is a sandbox run, and $([ "$CANCEL_DONE" = yes ] && echo "it did not end on its own, so it was cancelled with 'fugaro cancel --now'" || echo "it ended on its own and nothing had to be cancelled"). Its pull request and branch are LEFT OPEN on purpose: Check 491 files the deferred finding from it once you merge it. The scratch directory held the commands' output"
  if [ "$CANCEL_OK" = no ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the run ${RUN_ID} did not end on its own and 'fugaro cancel --now' did not answer: it may still be going. Read it with 'fugaro runs show ${RUN_ID}' and cancel it yourself. Nothing else was created; the pull request is left open for Check 491"
  fi
}

cleanup() {
  # restore runs BEFORE the facts are captured: it is what cancels a run that
  # had not ended, and the row that says so has to be in the block.
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to cancel a run and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done — leaving the run it was about to cancel.
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
  say "CHECK 490 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version, 'fugaro followups --help' and the --debt line of 'fugaro runs ls --help' — the release ${RELEASE} gate: the build must carry the post-review triage work"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [local] ${SANDBOX}/fugaro.yaml — the premise: the repository does not write agent.triage: false"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --mode startup --json \"${TASK}\" — ONE sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [network] fugaro cancel <run> --now — only if the run has not ended within ${RUN_TIMEOUT}s, and again from the exit trap: nothing this check launches outlives it"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — the outcome, the mode, the triage block and the deferrals"
  say "  [read] gh pr view <the run's pull request> --json body — the Deferred (follow-ups) section an owner merges with"
  say "  [nothing created] no job of this check's own, no build, no binding, no account; the scratch directory is removed at the end, and the pull request is left OPEN for Check 491"
  for key in release sandbox premise run-launched terminal cancelled result-json \
    mode outcome triage-block deferred pr-deferred cost; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no run and no cancel"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  premise run-launched terminal cancelled result-json mode outcome triage-block
  deferred pr-deferred cost restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 490 RESULT\n' >&3
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
  printf 'CHECK 490 RESULT END (exit %s)\n' "$status" >&3
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
DOC_CLI=""
RECIPE_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with; it has to be a change a reviewer can report a finding about"
    TASK_CLI=$2
    shift 2
    ;;
  --doc)
    [ $# -ge 2 ] || die "--doc needs the documentation file the task names, relative to the checkout (README.md by default)"
    DOC_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
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

SANDBOX=${CHECK490_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 490 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from and what its configuration is read through. Usage: bash scripts/live-checks/check490.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
DOC=${CHECK490_DOC:-${DOC_CLI:-README.md}}
TASK=${CHECK490_TASK:-${TASK_CLI:-"Read ${DOC} and change exactly ONE word in exactly ONE sentence so that the sentence no longer matches what the code does — a count, a default, a name or a path that is off by one from the code. Change no code, no other sentence and no other file: this is a one-word documentation change, and the pull request should say what the sentence used to claim."}}
RECIPE=${CHECK490_RECIPE:-${RECIPE_CLI:-}}
RELEASE=${CHECK490_RELEASE:-0.24.0}
MAX_MODEL_USD=${CHECK490_MAX_MODEL_USD:-}
YES=${CHECK490_YES:-$YES_CLI}
DRY_RUN=${CHECK490_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK490_KEEP_SCRATCH:-}
REGION=${CHECK490_REGION:-us-east5}
RUN_TIMEOUT=${CHECK490_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK490_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK490_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK490_LAUNCH_TIMEOUT_SECONDS:-600}
CANCEL_TIMEOUT=${CHECK490_CANCEL_TIMEOUT_SECONDS:-300}
DOCTOR_TIMEOUT=${CHECK490_DOCTOR_TIMEOUT_SECONDS:-300}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 490 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
    die "Check 490 launches ONE sandbox run and spends what its task costs, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check490.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_premise
  step_run
  step_terminal
  step_record
  step_mode
  step_outcome
  step_triage_block
  step_deferred
  step_pr_deferred
  step_cost
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
