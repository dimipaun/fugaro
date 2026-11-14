#!/usr/bin/env bash
# Check 520 — a text-only coder reads a screenshot.
#
# Plan task V8 writes this check; what it settles is docs/vision-model/
# implementation-plan.md's "Checks 520–521" and the work's own reason to exist
# (the issue's case, run 20261107-104613-b2af): a repository whose coder is a
# model priced WITHOUT an image rate, with `agent.models.vision` set to a model
# that can read one, runs a stage that reads a picture on the vision model
# instead of dying on the gateway's image refusal with the stage's work paid
# for and thrown away.
#
# The plan's sentence reads "a provider model with an image rate"; the state the
# work is about — and the state doctor names "the coder model X is priced
# without an image rate, and agent.models.vision is Y, which a stage that reads
# a picture runs instead" — is the coder priced WITHOUT one (D2016: a coder
# whose own price row carries an image rate reads the picture itself and keeps
# its own model, so the slot is never reached). This check measures the state
# V7 rewrites, and the `premise` row says so in as many words when the sandbox
# is configured the other way.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     feature itself (`fugaro models use --help` names the `vision` slot). The
#     work is unreleased until 0.24.0, so a build without it stops here with the
#     release named rather than measuring a run that cannot carry the feature;
#   * the premise, from `fugaro doctor --json`'s `vision-coder` check, read in
#     the sandbox checkout: the coder's own price row carries no image rate and
#     the vision slot is set;
#   * the coder and vision model IDs, from `fugaro models ls --json` — the row
#     the project's configuration keeps for this repository. The vision model's
#     ID is what every row below looks for in the run's own record;
#   * one sandbox run whose task names a `.png` (a console `__screenshots__`
#     golden is the issue's own case), launched with `fugaro run --json` and
#     followed to its end;
#   * `result.json`: the run ended succeeded, every coder-role stage it ran
#     (`implement`, and `fix` where the recipe ran one) names the VISION model,
#     and `cost.model_by` carries that model's ID — which is the split the issue
#     is about, read from the record rather than from the config;
#   * `fugaro diagnose <run> --json`: the same split, through the command the
#     owner reads a run with.
#
# It creates nothing of its own choosing: one sandbox run, which is billable,
# and the pull request that run opens. Every other call is a read.
#
# Usage:      bash scripts/live-checks/check520.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check520.sh --sandbox <path> --yes --task "<the task>"
#             bash scripts/live-checks/check520.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check520.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, and able to read the runs
#             bucket) and fugaro (the installed release: the whole point is the
#             build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    one sandbox run and the pull request it opens. On disk: one
#             scratch directory, removed at the end, holding the commands'
#             output.
# Costs:      ONE sandbox run and what its task costs — the model spend of the
#             implement stage and, where the recipe reviews, its review rounds,
#             on the sandbox's own budget. Nothing else here is billable: the
#             doctor, the models list and the diagnose are reads. `--yes` is
#             required, and CHECK520_MAX_MODEL_USD turns the figure into a
#             criterion when you set it.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check520_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and its branch are
#             NOT closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 520 RESULT,
#             then CHECK 520 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or the run did not
#             end. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry model
# IDs, stage names, statuses and dollars.
#
# Knobs (all CHECK520_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, REPO (the project
# configuration's key for the sandbox repository; the checkout directory's name
# by default), TASK and RECIPE (what the run is launched with), RELEASE (0.24.0:
# the release this work ships in), MAX_MODEL_USD (unset: the run's figure is
# read and not graded), REGION (us-east5), RUN_TIMEOUT_SECONDS (3600),
# POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600),
# DOCTOR_TIMEOUT_SECONDS (300).
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
  command -v bash >/dev/null 2>&1 || die "Check 520 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 520 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk520-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
CODER=""
VISION=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
SETTLED=no
MODEL_USD=""
# PREMISE_OK is set by the one branch of step_premise that establishes the state
# the run is a reading of. step_run reads it and nothing else: a run launched
# after a failed premise would spend the sandbox's money measuring a state this
# check could not name.
PREMISE_OK=no
# MODELS_OK is the same for step_models: two IDs that name two different models
# are what makes a stage timing naming the vision model a reading of the rewrite
# rather than of the coder's own model.
MODELS_OK=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk520.XXXXXXXX") || die "Check 520 cannot make a scratch directory"
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
    say "CHECK520_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a model ID the release did not write the day the document is
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
elif what == "models":
    want = sys.argv[3]
    rows = doc.get("repositories") or []
    print("names=%s" % ",".join(row.get("name") or "" for row in rows))
    found = None
    for row in rows:
        if row.get("name") == want:
            found = row
            break
    if found is None:
        print("found=false")
    else:
        print("found=true")
        print("coder=%s" % flat(found.get("coder")))
        print("vision=%s" % flat(found.get("vision")))
        print("reviewer=%s" % flat(found.get("reviewer")))
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
elif what == "diagnose":
    print("failure_class=%s" % (doc.get("failure_class") or ""))
    for model, usd in sorted((doc.get("model_by") or {}).items()):
        print("model\t%s\t%s" % (model, usd))
    for stage in doc.get("stages") or []:
        print("stage\t%s\t%s\t%s" % (
            stage.get("name") or "",
            flat(stage.get("model")),
            stage.get("model_usd") or 0,
        ))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST [ARG]: answer one query into a file, and fail the
# step when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARG]
  if [ $# -ge 4 ]; then
    python3 "$SCRATCH/read.py" "$1" "$2" "$4" >"$3" 2>"$3.err"
  else
    python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
  fi
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# stage_field COLUMN NAME — one column of a `stage\t<name>\t...` row.
stage_field() { # stage_field FILE NAME COLUMN
  awk -F'\t' -v name="$2" -v col="$3" '$1 == "stage" && $2 == name { print $col; exit }' "$1" 2>/dev/null || true
}

# stage_names FILE — every stage the record ran, comma-separated.
stage_names() { # stage_names FILE
  awk -F'\t' '$1 == "stage" { print $2 }' "$1" 2>/dev/null | paste -sd, - 2>/dev/null || true
}

# model_usd FILE MODEL — the dollars `cost.model_by` holds for one model, or ""
# when the split names none. An absent key and a zero are different readings.
model_usd() { # model_usd FILE MODEL
  awk -F'\t' -v model="$2" '$1 == "model" && $2 == model { print $3; exit }' "$1" 2>/dev/null || true
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the record read is the one exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a run that
# failed, a record that is not there), and a trap inherited here would turn each
# of them into "an internal error" and a status of 2 before the step could
# report it.
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

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check520.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
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
    stop "${marker} is set, so this is a coding agent's session: Check 520 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
# rewrite, so a run launched here would measure the state the work removes. Both
# probes are offline: `fugaro version` and the usage line `models use` prints.
step_release() {
  local v feature=no
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" models use --help 2>/dev/null | grep -q 'vision'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no agent.models.vision: 'fugaro models use --help' does not name the vision slot, so neither the slot nor the rewrite the run below measures is in this build. Check 520 needs release ${RELEASE} (the vision-model work) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no agent.models.vision: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'models use' names the vision slot: this build carries the work the run below measures"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the vision-model work ships in: the slot exists in this build only if the feature probe above is right about a version string that says otherwise, and a run measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro models use --help' names the vision slot, so this build carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  local extra=""
  if [ -n "$MAX_MODEL_USD" ]; then
    extra=" The run's own figure is graded against CHECK520_MAX_MODEL_USD=${MAX_MODEL_USD}: a run that spends more is a FAIL row, and the money is spent either way."
  fi
  fact bound PASS "ONE sandbox run and what its task costs — the model spend of the implement stage and, where the recipe reviews, its review rounds, on the sandbox's own budget. Every other call this check makes is a read (doctor, models ls, diagnose) or the run's own record.${extra} --yes is required to launch it"
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

# step_premise: the state the run below is a reading of. `doctor --json`'s
# `vision-coder` check is the one place this program resolves the checkout's own
# repository against the price table, so its sentence is the premise rather than
# a config key read by hand — a coder whose price row carries an image rate
# reads the picture itself and keeps its own model (D2016), and a run of it
# would measure nothing about the vision slot.
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
    fact premise FAIL "doctor says: ${problem} — the vision slot is UNSET in ${SANDBOX}, so the run below would be refused at bootstrap before any stage (V5) rather than reading the picture, and nothing about the rewrite would be measured. Set the slot to a model that can read images and price that model's images with model_prices' image_tokens (fugaro models use <repo> vision <id>), then run this check again"
    return 0
    ;;
  *"is priced with an image rate"*)
    fact premise FAIL "doctor says: ${problem} — the coder model reads a picture ITSELF, so no stage of this repository ever reaches the vision slot (D2016) and a run here measures the coder's own model rather than the rewrite. This is the state the plan's own sentence reads as ('a provider model with an image rate'), and it is not the state the work is about: the check needs a coder priced WITHOUT an image rate and agent.models.vision set to a model that can. Price the coder without image_tokens, or read the plan's sentence as the slip it is"
    return 0
    ;;
  *"is priced without an image rate"*)
    PREMISE_OK=yes
    fact premise PASS "doctor says: ${problem} — the coder cannot read an image and the vision slot covers it, which is the state V7 rewrites. Its own remedy line reads: ${fix}"
    return 0
    ;;
  esac
  fact premise FAIL "doctor's vision-coder sentence is not one this check knows (${problem}): the premise above is read from that sentence, and a sentence it cannot place is a reading nobody can grade. Run 'fugaro doctor --all' in ${SANDBOX} yourself and read the vision-coder line"
}

# step_models: the two model IDs, from the project's own view of this
# repository. The vision model's ID is what every row below looks for in the
# run's record, so a row that read it from the config file alone would miss the
# layer that actually decided it.
step_models() {
  if [ -z "$PROJECT" ]; then
    unmeasured models "there is no project to read the models for: the row above says why"
    return 0
  fi
  say "reading the models: fugaro models ls --json"
  run_bounded "$CLI_TIMEOUT" models "$SANDBOX" "$FUGARO_BIN" models ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured models "fugaro models ls --json did not answer (exit ${BOUNDED_STATUS}): the vision model's ID is what the record below is read against. Its output is in $SCRATCH/models.out and $SCRATCH/models.err"
    return 0
  fi
  if ! read_json models "$SCRATCH/models.out" "$SCRATCH/models.txt" "$REPO"; then
    unmeasured models "fugaro models ls --json printed a document this check could not read: what it printed is in $SCRATCH/models.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/models.txt" found)" != true ]; then
    unmeasured models "the project's models list holds no repository named '${REPO}' (it holds: $(field "$SCRATCH/models.txt" names)): that name is the project configuration's key for the sandbox repository, and the default is the checkout directory's own name. Name it with CHECK520_REPO=<key> — read the keys with 'fugaro models ls'"
    return 0
  fi
  CODER=$(field "$SCRATCH/models.txt" coder)
  VISION=$(field "$SCRATCH/models.txt" vision)
  if [ -z "$CODER" ]; then
    fact models FAIL "the row for ${REPO} names no coder model: with no coder pinned the stage runs Claude Code's own default, an Anthropic model with an image rate in every configuration this program ships, so no stage reaches the vision slot and the rows below would measure nothing about the rewrite"
    return 0
  fi
  if [ -z "$VISION" ]; then
    fact models FAIL "the row for ${REPO} names no vision model (agent.models.vision is unset): the run below would be refused at bootstrap before any stage. Set it with 'fugaro models use ${REPO} vision <id>' and price that model's images with model_prices' image_tokens"
    return 0
  fi
  if [ "$VISION" = "$CODER" ]; then
    fact models FAIL "the row for ${REPO} names the coder and the vision slot the same model (${VISION}): the record's stage timings would name it whether the rewrite happened or not, so nothing below could tell the rewrite from the coder's own model. Name a different model for images"
    return 0
  fi
  MODELS_OK=yes
  fact models PASS "${REPO} runs coder ${CODER} and agent.models.vision ${VISION}: the two are different models, so a stage timing that names ${VISION} is the rewrite and not the coder"
}

# step_run: one sandbox run whose task names a `.png`. `--json` is what names
# the run this check then follows; a launch that prints no run ID is a finding
# about the launcher, not a row this check could not measure.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  # A run is launched only once the two rows above have named the state it is a
  # reading of — a coder that cannot read an image, a slot that covers it, and
  # the two model IDs the record is read against. A run launched after either
  # failed would spend the sandbox's money on a reading nobody could grade, and
  # the FAIL row above is already the finding.
  if [ "$PREMISE_OK" != yes ] || [ "$MODELS_OK" != yes ]; then
    unmeasured run-launched "nothing was launched: the premise or the models row above did not establish the state this run is a reading of, and a run launched without it would spend the sandbox's money on a reading nobody could grade. Settle the row(s) above and run this check again"
    return 0
  fi
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the check needs a run to read a record from, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)), with a task that names a .png: the run is the check's own, and the rows below follow this one and no other"
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
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_record: the record, read from the runs bucket as the owner would read it.
# The record's own run_id is what ties it to the run this check launched.
step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
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
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the run's cost split and its stage timings are written. What gcloud printed is in $SCRATCH/record.err"
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
  MODEL_USD=$(field "$SCRATCH/record.txt" cost_usd)
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), $(field "$SCRATCH/record.txt" stage_count) stage(s), model_usd=${MODEL_USD:-0}): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_succeeded: the run has to have passed. The issue's failure is a run that
# dies at the first stage to read a picture — the stage's work paid for and
# thrown away — so a run that did not pass is the reading this check exists to
# catch, and its reason is printed with the row.
step_succeeded() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured run-succeeded "there is no record to read the ending from: the row above says why"
    return 0
  fi
  local status outcome reason
  status=$(field "$SCRATCH/record.txt" status)
  outcome=$(field "$SCRATCH/record.txt" outcome)
  reason=$(field "$SCRATCH/record.txt" reason)
  if [ "$status" != succeeded ]; then
    fact run-succeeded FAIL "the run ended ${status} (outcome=${outcome:-none}) rather than succeeded, and it was launched with a task that names a .png: a run that dies on the picture is the failure this work removes. Its reason reads: ${reason:-none}. The record is in $SCRATCH/record.out"
    return 0
  fi
  if [ -n "$MAX_MODEL_USD" ] && awk -v got="${MODEL_USD:-0}" -v max="$MAX_MODEL_USD" 'BEGIN { exit (got > max) ? 0 : 1 }'; then
    fact run-succeeded FAIL "the run succeeded but spent \$${MODEL_USD:-0} of model money, over CHECK520_MAX_MODEL_USD=${MAX_MODEL_USD}: the bound you set is the one this row grades, and the money is spent either way. Raise the knob if the task is simply dearer than you expected"
    return 0
  fi
  fact run-succeeded PASS "the run ended succeeded (outcome=${outcome:-none}, model_usd=${MODEL_USD:-0}): it was launched with a task naming a .png and it was not refused by the gateway for the picture, which is the failure the issue reports"
}

# step_stages: the reading the plan names first. Every stage that runs in the
# coder's slot — implement, and the fix rounds a recipe adds — is pinned to the
# vision model (D2002), so a stage timing that names it is the rewrite having
# happened. A reviewer's stage is deliberately NOT rewritten (D2004) and is
# never graded here.
step_stages() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured vision-stages "there is no record to read the stage timings from: the row above says why"
    return 0
  fi
  local names
  names=$(stage_names "$SCRATCH/record.txt")
  if [ "$(field "$SCRATCH/record.txt" stage_count)" = 0 ]; then
    fact vision-stages FAIL "the record holds no stage at all (status=$(field "$SCRATCH/record.txt" status), reason=$(field "$SCRATCH/record.txt" reason)): the run never reached implement, so the stage that would have read the picture never ran and the rewrite is not measured by this run"
    return 0
  fi
  local name model wrong="" seen=""
  while IFS= read -r name; do
    case "$name" in implement | fix) ;; *) continue ;; esac
    model=$(stage_field "$SCRATCH/record.txt" "$name" 3)
    seen="${seen:+$seen, }${name}=${model:-none}"
    if [ "$model" != "$VISION" ]; then
      wrong="${wrong:+$wrong, }${name}(=${model:-none})"
    fi
  done < <(awk -F'\t' '$1 == "stage" { print $2 }' "$SCRATCH/record.txt")
  if [ -z "$seen" ]; then
    fact vision-stages FAIL "the record ran ${names} and none of them is implement or a fix round: the coder-role stages are the ones the rewrite moves (D2002), and this run has none, so nothing here is a reading of the vision slot"
    return 0
  fi
  if [ -n "$wrong" ]; then
    fact vision-stages FAIL "the record's coder-role stages do not all name the vision model ${VISION}: ${seen}, and the ones on another model are ${wrong} — a coder-role stage on another model is either the rewrite not having happened or the stage having been pinned by hand, and the run's stages are ${names}. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact vision-stages PASS "every coder-role stage the run ran is pinned to the vision model ${VISION} — ${seen} — read from the record's own stage timings (the run's stages are ${names}). A reviewer's stage is deliberately not rewritten (D2004) and is not graded here"
}

# step_cost: the split the issue is about. `cost.model_by` is the run's spend by
# model, and the vision model's ID being a key in it is the outside evidence
# that the picture's call was answered — and paid for — by that model.
step_cost() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured vision-cost "there is no record to read the cost split from: the row above says why"
    return 0
  fi
  if [ -z "$VISION" ]; then
    unmeasured vision-cost "this check does not know the vision model's ID (the models row above says why), so it cannot say whether the run's cost split carries it. Read it yourself in $SCRATCH/record.out"
    return 0
  fi
  local usd split
  usd=$(model_usd "$SCRATCH/record.txt" "$VISION")
  split=$(awk -F'\t' '$1 == "model" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt" 2>/dev/null || true)
  if [ -z "$usd" ]; then
    fact vision-cost FAIL "the run's cost.model_by does not carry the vision model ${VISION} (it carries: ${split:-nothing}): the picture's call was not answered — or not priced — by the model the configuration named for images, and the split the issue is about is not there to read. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact vision-cost PASS "the run's cost.model_by carries ${VISION} at \$${usd} — the split is ${split}— so the call that carried the picture was answered and priced on the model agent.models.vision names, read from the record rather than from the config"
}

# step_diagnose: the same split through the command the owner reads a run with.
# A split that exists in the bucket and not in `diagnose` is one the owner
# cannot see, and the plan names this command as part of the reading.
step_diagnose() {
  if [ -z "$RUN_ID" ] || [ "$SETTLED" != yes ]; then
    unmeasured diagnose-split "there is no ended run to diagnose: the row above says why"
    return 0
  fi
  say "reading the diagnosis: fugaro diagnose ${RUN_ID} --json"
  run_bounded "$CLI_TIMEOUT" diagnose "$SANDBOX" "$FUGARO_BIN" diagnose "$RUN_ID" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured diagnose-split "fugaro diagnose ${RUN_ID} --json did not answer (exit ${BOUNDED_STATUS}): the diagnosis is how you read a run's split, and it could not be read here. Its output is in $SCRATCH/diagnose.out and $SCRATCH/diagnose.err"
    return 0
  fi
  if ! read_json diagnose "$SCRATCH/diagnose.out" "$SCRATCH/diagnose.txt"; then
    unmeasured diagnose-split "fugaro diagnose --json printed a document this check could not read: what it printed is in $SCRATCH/diagnose.out"
    return 0
  fi
  local usd split
  usd=$(model_usd "$SCRATCH/diagnose.txt" "$VISION")
  split=$(awk -F'\t' '$1 == "model" { printf "%s=%s ", $2, $3 }' "$SCRATCH/diagnose.txt" 2>/dev/null || true)
  if [ -z "$usd" ]; then
    fact diagnose-split FAIL "fugaro diagnose ${RUN_ID} --json prints no ${VISION} in its model_by (it prints: ${split:-nothing}): the record's own split is one thing and the command the owner reads a run with is another, and a split the diagnosis drops is one nobody sees. Its output is in $SCRATCH/diagnose.out"
    return 0
  fi
  fact diagnose-split PASS "fugaro diagnose ${RUN_ID} --json prints the same split — ${VISION} at \$${usd} (${split}) — so the model that read the picture is visible through the command you read a run with, not only in the bucket"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check wrote no config, no price and no policy — it read the sandbox's configuration and launched one run. The run ${RUN_ID:-none} is a real sandbox run and it opened a pull request: close that pull request and delete its branch yourself once you have read the run. The scratch directory held the commands' output"
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
  say "CHECK 520 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro models use --help — the release ${RELEASE} gate: the build must carry agent.models.vision"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to, and the vision-coder sentence that is the premise"
  say "  [read] fugaro models ls --json — the coder and the vision model the project's configuration gives ${REPO}"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — ONE sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — the record: its stage timings and its cost.model_by"
  say "  [read] fugaro diagnose <run> --json — the same split through the command you read a run with"
  say "  [nothing else] no config, no price and no policy is written; the scratch directory is removed at the end"
  fact bound PASS "planned: ONE sandbox run and what its task costs. --dry-run runs nothing, and a real run requires --yes"
  for key in release sandbox premise models run-launched terminal result-json \
    run-succeeded vision-stages vision-cost diagnose-split; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no models read, no run and no diagnose"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  premise models run-launched terminal result-json run-succeeded vision-stages
  vision-cost diagnose-split restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 520 RESULT\n' >&3
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
  printf 'CHECK 520 RESULT END (exit %s)\n' "$status" >&3
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
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with; it has to name a .png, which is what makes the run a reading of the vision slot"
    TASK_CLI=$2
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

SANDBOX=${CHECK520_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 520 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from and what its configuration is read through. Usage: bash scripts/live-checks/check520.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
# The project configuration's key for this repository, and the name
# `fugaro models ls --json` lists it under: the checkout directory's own name by
# default, and the knob exists because the two need not agree.
REPO=${CHECK520_REPO:-${SANDBOX##*/}}
TASK=${CHECK520_TASK:-${TASK_CLI:-"Read the screenshot docs/__screenshots__/settings.png and reply with one line saying what it shows. Do not change any file."}}
RECIPE=${CHECK520_RECIPE:-${RECIPE_CLI:-}}
RELEASE=${CHECK520_RELEASE:-0.24.0}
MAX_MODEL_USD=${CHECK520_MAX_MODEL_USD:-}
YES=${CHECK520_YES:-$YES_CLI}
DRY_RUN=${CHECK520_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK520_KEEP_SCRATCH:-}
REGION=${CHECK520_REGION:-us-east5}
RUN_TIMEOUT=${CHECK520_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK520_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK520_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK520_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK520_DOCTOR_TIMEOUT_SECONDS:-300}
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
  [ -n "$FUGARO_BIN" ] || die "Check 520 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
    die "Check 520 launches ONE sandbox run and spends what its task costs, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check520.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_premise
  step_models
  step_run
  step_terminal
  step_record
  step_succeeded
  step_stages
  step_cost
  step_diagnose
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
