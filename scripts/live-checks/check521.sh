#!/usr/bin/env bash
# Check 521 — the early refusal.
#
# Plan task V8 writes this check; what it settles is docs/vision-model/
# implementation-plan.md's "Checks 520–521" and V5's own reason to exist
# (D2005, D2006): a repository whose coder is priced WITHOUT an image rate, with
# **no** `agent.models.vision`, and a task whose text names a `.png`, is refused
# at bootstrap — before any stage spends — with a sentence that names the slot
# and the price member that fix it. The failure the refusal removes is the
# issue's own: the run otherwise reaches implement, reads the picture, and dies
# on the gateway's image refusal with the stage's work paid for and thrown away.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     feature itself (`fugaro models use --help` names the `vision` slot). The
#     work is unreleased until 0.24.0, so a build without it stops here with the
#     release named rather than measuring a run that cannot carry the refusal;
#   * the premise, from `fugaro doctor --json`'s `vision-coder` check, read in
#     the sandbox checkout: the coder's own price row carries no image rate and
#     `agent.models.vision` is unset. The task must name an image file — the
#     check refuses a `--task` that names none, because the refusal it measures
#     is the one the task's own text triggers;
#   * one sandbox run whose task names a `.png`, launched with `fugaro run
#     --json`. **Nothing it launches can outlive it**: the run refuses at
#     bootstrap within seconds, and a run that has not ended by
#     CHECK521_RUN_TIMEOUT_SECONDS is cancelled with `fugaro cancel --now` —
#     from the step that gives up on it AND from the exit trap, so a Ctrl-C in
#     the middle cancels it too;
#   * `result.json`: the run did not succeed, its `failure_class` is
#     `config_invalid` (the class a bootstrap refusal is recorded under), its
#     `reason` is the refusal sentence, it holds NO stage at all, and its cost
#     is zero — no model call was made and no budget was spent.
#
# It creates nothing: no job of its own, no build, no binding and no account.
# The one thing it starts is the sandbox run, which is free because it refuses
# before any stage spends, and which the check cancels if it does not end.
#
# Usage:      bash scripts/live-checks/check521.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check521.sh --sandbox <path> --task "<the task>"
#             bash scripts/live-checks/check521.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check521.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, and able to read the runs
#             bucket) and fugaro (the installed release: the whole point is the
#             build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    nothing but the run itself, which the check cancels if it has not
#             ended. On disk: one scratch directory, removed at the end, holding
#             the commands' output.
# Costs:      NOTHING billable and no model call: the run is refused at
#             bootstrap, before its first stage, so no stage's work is paid for
#             and the budget is not touched. `--yes` is not needed for that
#             reason — the check asks for nothing but a terminal and a few
#             minutes.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check521_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the run, cancelled from that same trap
#             if it has not ended. Nothing else is created, so nothing else is
#             restored.
# Output:     progress on stderr, ONE block on stdout headed CHECK 521 RESULT,
#             then CHECK 521 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or the run never
#             ended and had to be cancelled. A key the run never reached reads
#             `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry the
# refusal's own sentence, statuses, stage counts and dollars.
#
# Knobs (all CHECK521_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), TASK (what the run is launched with;
# it must name an image file), RECIPE, RELEASE (0.24.0: the release this work
# ships in), REGION (us-east5), RUN_TIMEOUT_SECONDS (900), POLL_SECONDS (10),
# CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600),
# CANCEL_TIMEOUT_SECONDS (300), DOCTOR_TIMEOUT_SECONDS (300).
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
  command -v bash >/dev/null 2>&1 || die "Check 521 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 521 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk521-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
SETTLED=no
CANCEL_DONE=no
CANCEL_OK=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk521.XXXXXXXX") || die "Check 521 cannot make a scratch directory"
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
    say "CHECK521_KEEP_SCRATCH is set: $SCRATCH is kept"
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
    found = None
    for check in doc.get("checks") or []:
        if check.get("id") == "vision-coder":
            found = check
            break
    if found is None:
        print("present=false")
    else:
        print("present=true")
        print("ok=%s" % yes(found.get("ok")))
        print("severity=%s" % flat(found.get("severity")))
        print("problem=%s" % flat(found.get("problem")))
        print("fix=%s" % flat(found.get("fix")))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("failure_class=%s" % (doc.get("failure_class") or ""))
    print("reason=%s" % flat(doc.get("reason")))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
    stages = doc.get("stages") or []
    print("stage_count=%d" % len(stages))
    for stage in stages:
        print("stage\t%s\t%s\t%s" % (
            stage.get("name") or "",
            flat(stage.get("model")),
            stage.get("model_usd") or 0,
        ))
    for model, usd in sorted((cost.get("model_by") or {}).items()):
        print("model\t%s\t%s" % (model, usd))
elif what == "cancel":
    print("run_id=%s" % (doc.get("run_id") or ""))
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
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# task_image TASK: the first image file name the task's text carries, "" when it
# names none. The kinds are the ones the run's own check refuses on
# (agent.ImageBlockExtensions, without the document kinds), and the row that
# grades the refusal's sentence looks for the name this returns.
task_image() { # task_image TASK
  printf '%s\n' "$1" | grep -Eio '[A-Za-z0-9._/-]+\.(png|jpg|jpeg|gif|webp|bmp|ico|heic|tiff)' | head -n 1 || true
}

# names_an_image TASK: whether the task names an image at all, by file name or
# by the kind's own word — the two spellings the run's check matches
# (imageNamesInTask), and the reason a task that names neither measures nothing.
names_an_image() { # names_an_image TASK
  printf '%s\n' "$1" | grep -Eqi '\.(png|jpg|jpeg|gif|webp|bmp|ico|heic|tiff)\b|(^|[^a-z])(png|jpeg|heic|tiff)([^a-z]|$)'
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
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check521.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
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
    stop "${marker} is set, so this is a coding agent's session: Check 521 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
# ships in 0.24.0, and a build that predates it has neither the slot nor the
# refusal, so a run launched here would measure the state the work removes. Both
# probes are offline: `fugaro version` and the usage line `models use` prints.
step_release() {
  local v feature=no
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" models use --help 2>/dev/null | grep -q 'vision'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no agent.models.vision: 'fugaro models use --help' does not name the vision slot, so neither the slot nor the bootstrap refusal the run below measures is in this build. Check 521 needs release ${RELEASE} (the vision-model work) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no agent.models.vision: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'models use' names the vision slot: this build carries the work the run below measures"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the vision-model work ships in: a run measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro models use --help' names the vision slot, so this build carries the work"
    ;;
  esac
}

# step_bound: what this check is about to start, said before it starts it.
step_bound() {
  fact bound PASS "NOTHING billable and no model call: the run below is refused at bootstrap, before its first stage, so no stage's work is paid for and the budget is not touched. It does start one Cloud Run job execution, and nothing this check launches outlives it: a run that has not ended within ${RUN_TIMEOUT}s is cancelled with 'fugaro cancel --now', from the step that gives up on it and from this script's exit trap"
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

# step_premise: the state the refusal is triggered by. `doctor --json`'s
# `vision-coder` check is the one place this program resolves the checkout's own
# repository against the price table, so its sentence is the premise rather than
# a config key read by hand — a coder whose price row carries an image rate
# reads the picture itself (D2016), and a run of it would never be refused.
step_premise() {
  if [ ! -s "$SCRATCH/doctor.out" ]; then
    unmeasured premise "there is no doctor document to read the premise from: the row above says why"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/premise.txt"; then
    unmeasured premise "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/premise.txt" present)" != true ]; then
    fact premise FAIL "fugaro doctor --json in ${SANDBOX} carries no vision-coder check: this build's doctor does not know the vision slot, which is the same fact the release row above measures. Run 'fugaro update' and run this check again"
    return 0
  fi
  local problem fix
  problem=$(field "$SCRATCH/premise.txt" problem)
  fix=$(field "$SCRATCH/premise.txt" fix)
  case "$problem" in
  *"is priced without an image rate"*"agent.models.vision is unset"*)
    fact premise PASS "doctor says: ${problem} — the coder cannot read an image and the slot is UNSET, which is the state the refusal below is for. The task names $(task_image "$TASK"), so the run's bootstrap refuses it"
    return 0
    ;;
  *"is priced with an image rate"*)
    fact premise FAIL "doctor says: ${problem} — the coder model reads a picture ITSELF (D2016), so no stage of this repository ever needs the vision slot and the run below would read the picture rather than being refused. Price the coder without image_tokens, or point --sandbox at a repository whose coder is text-only"
    return 0
    ;;
  *"is priced without an image rate"*)
    fact premise FAIL "doctor says: ${problem} — agent.models.vision is SET in ${SANDBOX}, so the run below would run the picture on that model (Check 520's state) rather than being refused. Unset the slot (fugaro models use <repo> vision <the model agent.model names>, or edit the repository's fugaro.yaml) and run this check again. Its own remedy line reads: ${fix}"
    return 0
    ;;
  esac
  fact premise FAIL "doctor's vision-coder sentence is not one this check knows (${problem}): the premise above is read from that sentence, and a sentence it cannot place is a reading nobody can grade. Run 'fugaro doctor --all' in ${SANDBOX} yourself and read the vision-coder line"
}

# step_run: one sandbox run whose task names a `.png`. `--json` is what names the
# run this check then follows, and — because the run is cancelled rather than
# left going — a launch that prints no run ID is a finding about the launcher
# rather than a row this check could not measure.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  say "launching one run from ${SANDBOX} (refused at bootstrap, so nothing billable)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: this check measures a run the JOB refuses at bootstrap, and a launcher that refuses before starting one says something else is wrong with the sandbox. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
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
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)), with a task naming $(task_image "$TASK"): the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end — and it ends in seconds, because the
# refusal is at bootstrap and no stage starts. A run still going after
# CHECK521_RUN_TIMEOUT_SECONDS is cancelled here: this check may not leave
# running what it cannot stop.
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
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      RUN_REF=$(row_field 6)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    cancel_run "it had not ended after ${RUN_TIMEOUT}s"
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): a bootstrap refusal ends in seconds, so a run still going is one this check could not measure — and it was cancelled rather than left behind. What the cancel printed is in $SCRATCH/cancel.out and $SCRATCH/cancel.err. Read the run with 'fugaro runs show ${RUN_ID}' before running this check again"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end on its own, and nothing had to be cancelled"
  if [ -z "$CANCEL_DONE" ]; then
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
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up and this check cancelled it, so there is no terminal record to read: a refusal that never reached its end is not the reading this check is after. Read the run with 'fugaro runs show ${RUN_ID}' and run this check again"
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
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the refusal's own sentence and the run's cost are written. What gcloud printed is in $SCRATCH/record.err"
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
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), failure_class=$(field "$SCRATCH/record.txt" failure_class), $(field "$SCRATCH/record.txt" stage_count) stage(s), model_usd=$(field "$SCRATCH/record.txt" cost_usd)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_refused: the run must have been refused at bootstrap, and the class is
# what says which ending it was: a bootstrap refusal of the resolved
# configuration is recorded config_invalid (runner.go's own reading of a
# modelRefusal), so a run that failed some other way is a different ending and
# this check may not read it as the refusal.
step_refused() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured refused "there is no record to read the ending from: the row above says why"
    return 0
  fi
  local status class reason
  status=$(field "$SCRATCH/record.txt" status)
  class=$(field "$SCRATCH/record.txt" failure_class)
  reason=$(field "$SCRATCH/record.txt" reason)
  if [ "$status" = succeeded ]; then
    fact refused FAIL "the run ended SUCCEEDED with a task naming $(task_image "$TASK") and no agent.models.vision: it was not refused at bootstrap, so whatever it did instead is not the early refusal this check measures — and if it read the picture, its stage's work was paid for and thrown away. Its reason reads: ${reason:-none}. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$class" != config_invalid ]; then
    fact refused FAIL "the run ended ${status} with failure_class '${class:-none}' rather than config_invalid: the refusal this check measures is the resolved configuration's own (the coder's price row and the task's text), which is the class that ending is recorded under — so this run ended for another reason. Its reason reads: ${reason:-none}. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact refused PASS "the run ended ${status} with failure_class config_invalid (outcome=$(field "$SCRATCH/record.txt" outcome)): it was refused before any stage ran, which is V5's early refusal and not the mid-run death the issue reports"
}

# step_sentence: the refusal has to be actionable without the issue in hand. The
# run's `reason` is where the owner reads it, and it has to name the slot to set
# and the price member that gives that slot's model an image rate (D2006, D2001)
# — a sentence naming the slot alone would send an owner to a key whose model
# the gateway then refuses for the same reason.
step_sentence() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured sentence "there is no record to read the refusal's sentence from: the row above says why"
    return 0
  fi
  local reason image missing=""
  reason=$(field "$SCRATCH/record.txt" reason)
  image=$(task_image "$TASK")
  if [ -z "$reason" ]; then
    fact sentence FAIL "the run's record carries no reason at all: the refusal's own sentence is what an owner acts on, and a run that ended without one leaves them the failure and none of the remedy. The record is in $SCRATCH/record.out"
    return 0
  fi
  case "$reason" in
  *"can't read an image the task names"*) ;;
  *) missing="the refusal's own opening ('the coder can't read an image the task names')" ;;
  esac
  case "$reason" in
  *"agent.models.vision"*) ;;
  *) missing="${missing:+$missing, }agent.models.vision" ;;
  esac
  case "$reason" in
  *image_tokens*) ;;
  *) missing="${missing:+$missing, }model_prices' image_tokens" ;;
  esac
  if [ -n "$image" ]; then
    case "$reason" in
    *"$image"*) ;;
    *) missing="${missing:+$missing, }the file the task names ($image)" ;;
    esac
  fi
  if [ -n "$missing" ]; then
    fact sentence FAIL "the refusal's sentence does not name ${missing}: it reads '${reason}'. An owner who meets this needs the model that cannot read, the file that made the check refuse, the slot to set and the price member that gives that slot's model an image rate (D2006, D2001), and a sentence missing any of them is one they cannot act on"
    return 0
  fi
  fact sentence PASS "the refusal names everything an owner needs — the slot agent.models.vision, the price member model_prices' image_tokens${image:+, and the file the task names ($image)} — and it reads: ${reason}"
}

# step_no_stage: the refusal is EARLY. The whole point of V5 is that no stage's
# work is paid for and thrown away, so a record with a stage in it is the run
# having reached the place the refusal exists to keep it from.
step_no_stage() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured no-stage "there is no record to read the stage list from: the row above says why"
    return 0
  fi
  local count names
  count=$(field "$SCRATCH/record.txt" stage_count)
  names=$(awk -F'\t' '$1 == "stage" { print $2 }' "$SCRATCH/record.txt" | paste -sd, - 2>/dev/null || true)
  if [ "${count:-0}" != 0 ]; then
    fact no-stage FAIL "the record holds ${count} stage(s) — ${names} — for a run that was supposed to be refused at bootstrap: a stage that ran is a stage whose work was paid for, which is the failure V5 exists to remove. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact no-stage PASS "the record holds no stage at all: the run was refused before its first one, so no stage's work was paid for and thrown away — which is V5's whole claim"
}

# step_cost_zero: the record has to show the run spent nothing. This is the
# reading the plan names beside the refusal, and it is the one that says the
# refusal cost the owner nothing rather than only that it happened early.
step_cost_zero() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured cost-zero "there is no record to read the cost from: the row above says why"
    return 0
  fi
  local usd split
  usd=$(field "$SCRATCH/record.txt" cost_usd)
  split=$(awk -F'\t' '$1 == "model" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt" 2>/dev/null || true)
  if [ "${usd:-0}" != 0 ]; then
    fact cost-zero FAIL "the record's cost.model_usd is \$${usd} (model_by: ${split:-nothing}) for a run that was refused at bootstrap: a refusal that costs money is one the owner pays for to be told no. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ -n "$split" ]; then
    fact cost-zero FAIL "the record's cost.model_by is not empty (${split}) while its model_usd is \$${usd:-0}: a split naming a model is a call that was made, and no call may be made before the refusal"
    return 0
  fi
  fact cost-zero PASS "the record's cost.model_usd is \$0 and its cost.model_by is empty: the refusal made no model call and spent nothing, which is the reading the plan names beside it"
}

# restore: what this check changed, said rather than assumed. The run is the one
# thing it started, and a run that had not ended is cancelled here — from the
# exit trap, so a Ctrl-C or a failed assertion cancels it as surely as the
# ordinary way out.
restore() {
  cancel_run "the check is ending"
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created: no job of this check's own, no build, no binding and no account. The one run it started (${RUN_ID:-none}) is a sandbox run that was refused at bootstrap, and $([ "$CANCEL_DONE" = yes ] && echo "it did not end on its own, so it was cancelled with 'fugaro cancel --now'" || echo "it ended on its own and nothing had to be cancelled"). The scratch directory held the commands' output"
  if [ "$CANCEL_OK" = no ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the run ${RUN_ID} did not end on its own and 'fugaro cancel --now' did not answer: it may still be going. Read it with 'fugaro runs show ${RUN_ID}' and cancel it yourself. Nothing else was created"
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
  say "CHECK 521 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro models use --help — the release ${RELEASE} gate: the build must carry agent.models.vision"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to, and the vision-coder sentence that is the premise (a text-only coder and no slot)"
  say "  [network, FREE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — ONE sandbox run, refused at bootstrap before its first stage, followed with fugaro runs ls --json until it is terminal"
  say "  [network] fugaro cancel <run> --now — only if the run has not ended within ${RUN_TIMEOUT}s, and again from the exit trap: nothing this check launches outlives it"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — the record: the refusal's own sentence, the failure class, the (empty) stage list and the cost"
  say "  [nothing created] no job of this check's own, no build, no binding, no account; the scratch directory is removed at the end"
  for key in release sandbox premise run-launched terminal cancelled result-json \
    refused sentence no-stage cost-zero; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no run and no cancel"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  premise run-launched terminal cancelled result-json refused sentence no-stage
  cost-zero restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 521 RESULT\n' >&3
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
  printf 'CHECK 521 RESULT END (exit %s)\n' "$status" >&3
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
RECIPE_CLI=""
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
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with; it has to name an image file, which is what makes the run a reading of the refusal"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
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

SANDBOX=${CHECK521_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 521 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from and what its configuration is read through. Usage: bash scripts/live-checks/check521.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${CHECK521_TASK:-${TASK_CLI:-"Read the screenshot docs/__screenshots__/settings.png and reply with one line saying what it shows. Do not change any file."}}
RECIPE=${CHECK521_RECIPE:-${RECIPE_CLI:-}}
RELEASE=${CHECK521_RELEASE:-0.24.0}
DRY_RUN=${CHECK521_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK521_KEEP_SCRATCH:-}
REGION=${CHECK521_REGION:-us-east5}
RUN_TIMEOUT=${CHECK521_RUN_TIMEOUT_SECONDS:-900}
POLL_SECONDS=${CHECK521_POLL_SECONDS:-10}
CLI_TIMEOUT=${CHECK521_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK521_LAUNCH_TIMEOUT_SECONDS:-600}
CANCEL_TIMEOUT=${CHECK521_CANCEL_TIMEOUT_SECONDS:-300}
DOCTOR_TIMEOUT=${CHECK521_DOCTOR_TIMEOUT_SECONDS:-300}
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
  [ -n "$FUGARO_BIN" ] || die "Check 521 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  names_an_image "$TASK" || die "the task this check would launch names no image file: the refusal it measures is triggered by the task's own text (imageNamesInTask), so a task naming no picture measures nothing. It would have been: $TASK"
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
  step_sandbox
  step_premise
  step_run
  step_terminal
  step_record
  step_refused
  step_sentence
  step_no_stage
  step_cost_zero
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
