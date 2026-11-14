#!/usr/bin/env bash
# Check 124 — the opt-in Cloud Batch backend, as ONE script YOU run.
#
# The register spreads this check over four parts, and this script is those four
# parts with their reading and their teardown in one place. Each part is one
# run of the script:
#
#   --part a  124a: the plan-only form, the typed apply, and the two read-backs
#                 (fugaro doctor's batch- lines and fugaro validate), against
#                 the before-state 124d diffs with.
#   --part b  124b: one ordinary sandbox task on Batch, from its launch to its
#                 draft pull request, and then the decline and the branch delete
#                 that are the check's own litter.
#   --part c  124c: a forced preemption -- the run's own VM taken away by hand,
#                 selected by the label the job itself records -- and the exit
#                 code the job's report carries, read out of the tree.
#   --part d  124d: what the check spent, the budget lease it did or did not
#                 leave, and the diff against 124a's before-state.
#
# **Owner-run, and no agent performs any step.** The whole block is handed to
# you: `fugaro setup --plan-only` (which applies nothing and asks nothing) is
# the only `fugaro setup` this script runs, and the APPLYING form --
# `fugaro setup --provision-batch --project fugaro`, whose confirmation is typed
# at a real terminal and which a coding agent's session is refused by the CLI
# itself (`internal/cli/init_compute.go`) -- is printed for you and never run.
# The same goes for the two Bitbucket calls: this script makes them only when
# you point it at the sandbox token's file, and the decline and the branch
# delete are 124b's own restore, run in the same sitting and in that order.
#
# Usage:      bash scripts/live-checks/check124.sh --part a|b|c|d [--dry-run]
#             Run it from the SANDBOX checkout (origin edgeappinc/fugarosandbox):
#             `fugaro doctor`, `fugaro validate`, `fugaro runs ls`, `fugaro
#             budget show` and `fugaro reaper` all resolve the installation from
#             that checkout's project, and a checkout belonging to another one
#             is refused rather than resolved.
#             --dry-run runs the local guards and reads the local tree only,
#             prints every command a real run would run, in order, and touches
#             NO cloud project: no gcloud, no fugaro, no run, nothing.
# Needs:      bash, mktemp, git, python3, gcloud and curl on PATH, the installed
#             `fugaro` CLI, and -- for part b -- the sandbox Bitbucket token in
#             a file (CHK124_TOKEN_FILE).
# Where:      GCP project `fugaro-dev` (hard-coded, never read from gcloud and
#             never `belong`, EdgeWeb or EdgeServer) and region `us-east5`. The
#             only thing it deletes anywhere is a VM carrying the run's OWN
#             `fugaro_run` label value, read back off the run's job, and it
#             refuses to delete when that count is anything but one.
# Costs:      TWO real runs of the installation's agent (one per parts b and c),
#             each on a Batch VM: a few minutes of an `e2-standard-4` each, and
#             the model spend of an ordinary sandbox task. 124d is where the
#             dollars are read. Nothing here is Spot and nothing here is free.
# Runs:       by YOU, one part at a time. b must run before d (d diffs against
#             the before-state a writes and reads the runs b and c made), and a
#             must run before d.
# Cleanup:    the per-run scratch directory always goes. The work directory
#             (${CHK124_WORK:-/tmp/chk124}) is KEPT on purpose: 124d's whole
#             diff is against the three before-state files in it, and the
#             restore row names it. Nothing else is removed: the check's own
#             litter is 124b's declined pull request and its deleted branch, and
#             every resource the installation's own apply created stays.
# Output:     progress on stderr, ONE block on stdout headed
#             `CHECK 124a RESULT` (or 124b, 124c, 124d), whose last line is
#             `CHECK 124a RESULT: PASS|FAIL`. Exit 0 when every row this part
#             measures passed, 1 when one FAILed, 2 when the run could not tell
#             (a guard stopped it, a tool is missing, a read did not answer) and
#             130 on an interrupt.
#
# **A row that reads INFO is a reading, not a verdict**: a job somebody else
# left behind, a run that had already ended when the first row was read, a pass
# that refused something that is not this check's -- those are facts to paste
# into the register with the FAILs. Where the register names an expected output,
# the row is PASS or FAIL against it.
#
# There is no `--project` flag and no project variable: the project is
# hard-coded, every `gcloud` command carries `--project=${P:?}`, and every
# `fugaro` command carries `--project fugaro` (`fugaro validate`, which takes no
# such flag, is run bare and resolves the project the way `doctor` does). The
# script's own test (scripts/live-checks/check124_test.sh) drives all four parts
# entirely through fakes on PATH, with no network and no cloud project at all.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace would print the
# commands into the log an owner pastes back, and this script's commands carry
# the project, the job names and -- through the token file -- the header a
# destructive call is made with. Check 130 spells the three parts out; this is
# the same guard.
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
ABORT_REASON=""
REACHED_END=no
HELP=""
CLEANUP_RUNNING=no
DRY_RUN=""
PART=""
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
  # A fact is ONE line of the block: a detail that carried a newline would read
  # as a fact that is not there, with a key nobody wrote. The callers collapse a
  # command's output with oneline; this is the belt to that pair of braces.
  local detail=${3//$'\n'/ }
  printf '%s|%s|%s\n' "$1" "$2" "$detail" >>"$FACTS_FILE"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the Batch backend" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# oneline collapses a command's output into one line, because a fact is one line
# of the block and a multi-line detail would read as facts that are not there.
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 124 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 124 needs bash"
  need mktemp
  need git "the sandbox checkout is the guard that says which installation and which repository this run is about"
  need python3 "the API and CLI answers are parsed by one python3 program each, which prints the fields the block carries rather than the document"
  [ -n "$DRY_RUN" ] && return 0
  need gcloud "the jobs, the VMs, the images and the before-state are all read with it"
  [ "$PART" = b ] && need curl "124b's restore declines the pull request and deletes the branch with it"
  return 0
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk124-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""
BLOCK_TITLE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk124.XXXXXXXX") || die "Check 124 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 124 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# The work directory is NOT scratch: 124a writes the before-state into it and
# 124d diffs against those files in a later sitting, so it outlives every run.
# Its marker is what says the two are the same check's, and its path is the one
# the register's own block uses.
work_up() {
  W=${CHK124_WORK:-/tmp/chk124}
  if [ -n "$DRY_RUN" ]; then
    fact workdir INFO "not created: this is a dry run. A real run writes ${W}/jobs-before.txt, instances-before.txt and templates-before.txt into it (124a) and diffs against them (124d), which is why it is kept between parts and is not this run's scratch"
    return 0
  fi
  mkdir -p "$W" || die "Check 124 cannot make the work directory $W"
  if [ ! -f "$W/.chk124-work" ]; then
    : >"$W/.chk124-work" || die "Check 124 cannot write a marker into $W"
  fi
}

cleanup() {
  CLEANUP_RUNNING=yes
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
  [ -n "$HELP" ] && exit 0
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the backend"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS note
  if [ -z "$SCRATCH" ]; then
    note="nothing was created: the run stopped before it made a scratch directory, so nothing anywhere was enabled, disabled, granted or revoked"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    note="the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"), and the work directory ${W:-<none>} is KEPT on purpose: 124a's three before-state files live in it and 124d's whole diff is against them, so removing it would end this check's cleanup reading. Nothing else was removed anywhere — not one job, not one image, not one account, not one role, not the network, not a scheduler job — and no VM was deleted by any part but 124c, whose delete is one VM carrying the run's own fugaro_run label"
  fi
  facts="${facts}"$'\n'"restore|$restore|${note}"
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ] && [ "$(verdict_of "$facts")" = FAIL ]; then
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
# The project and the region are literals: a check that destroyed a VM in the
# owner's own installation is the failure the register's every stop exists for,
# and this script does not add a second way to point one. Nothing here reads
# gcloud's ambient configuration.
P=fugaro-dev
L=us-east5
INSTALL=fugaro
R=fugaro-ghcr
U=dimipaun/fugaro-base
REPO=edgeappinc/fugarosandbox

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste — the token file's use included"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check124.sh --part ${PART} — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_owner: the register's owner-run marker, the same one its own setup block
# opens with. It is not a security boundary — an agent's shell can export
# anything — but it is the one line that stops a session from running a check
# whose whole premise is that a person reads what it printed.
guard_owner() {
  if [ "${CHK124_OWNER_RUN:-}" != yes ]; then
    fact owner FAIL "this is an owner-run check and CHK124_OWNER_RUN is not exported as yes; no agent performs any step of Check 124"
    stop "Check 124 is owner-run. Run it at your own terminal, after: export CHK124_OWNER_RUN=yes — no agent performs any step of it (the register's Check 124 sections say so, and the apply's confirmation is refused to a coding agent's session anyway)"
  fi
  fact owner PASS "CHK124_OWNER_RUN=yes: this run was started by you at your own terminal, and no agent performs any step of Check 124"
}

guard_checkout() {
  CHECKOUT=$(git rev-parse --show-toplevel 2>/dev/null) \
    || stop "Check 124 stopped: this is not a git checkout (git rev-parse --show-toplevel failed). Run it from the sandbox checkout: cd <sandbox checkout> && bash <fugaro checkout>/scripts/live-checks/check124.sh --part ${PART}"
  ORIGIN=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) \
    || stop "Check 124 stopped: this checkout has no origin remote, so which repository this run is about is unknown"
  ORIGIN=$(printf '%s' "$ORIGIN" | sed -E 's#//[^@/]*@#//#')
  case "$ORIGIN" in
  *"$REPO" | *"$REPO".git) : ;;
  *) stop "Check 124 stopped: this checkout's origin ('${ORIGIN}') is not the sandbox (${REPO}), which is the only repository Check 124 runs against" ;;
  esac
  CHECKOUT_SHA=$(git -C "$CHECKOUT" rev-parse HEAD 2>/dev/null | head -n 1)
  [ -n "$CHECKOUT_SHA" ] || CHECKOUT_SHA=unknown
  fact checkout PASS "run from the sandbox checkout ${CHECKOUT} (origin ${ORIGIN}, commit ${CHECKOUT_SHA}): the installation every fugaro command resolves is the sandbox's own, and no command in this check names another repository"
}

guard_workdir() {
  work_up
  fact workdir PASS "the work directory is ${W} (the register's own ${CHK124_WORK:+CHK124_WORK-overridden }path): 124a writes its three before-state files there and 124d diffs against them, so it is kept between parts and is not the check's scratch"
}

# guard_belong: the owner's own installation and its repositories, refused by
# name. This check has no argument that could name a project, so the guard is
# about what the environment could silently supply instead.
guard_belong() {
  local ambient=${CLOUDSDK_CORE_PROJECT:-}${GOOGLE_CLOUD_PROJECT:-}${GCLOUD_PROJECT:-}
  low=$(printf '%s' "$ambient" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "the ambient gcloud configuration names a project matching '${ambient}', and the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch"
    stop "Check 124 never runs against 'belong', EdgeWeb or EdgeServer. Unset CLOUDSDK_CORE_PROJECT/GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and run it again: every command here names --project=${P} itself"
    ;;
  esac
  fact belong PASS "the project this check names is ${P}, hard-coded and never read from gcloud; neither 'belong' nor EdgeWeb nor EdgeServer is named by any step, this check's VM delete included (it names the run's own label value and nothing else), and this check has no flag or variable that could name another project"
}

# guard_installation: the register's own stop, and the only guard that reads the
# cloud. It is where "this run is about the sandbox installation" stops being an
# assumption: doctor resolves the checkout's project, and the line is compared.
guard_installation() {
  if [ -n "$DRY_RUN" ]; then
    fact installation INFO "not taken: this is a dry run, so fugaro doctor --all was not run. A real run asserts its 'project:' line names the GCP project fugaro-dev, and stops the whole check if it does not"
    return 0
  fi
  local out
  out=$(fugaro doctor --project "$INSTALL" --all 2>&1) || {
    fact installation FAIL "fugaro doctor --project ${INSTALL} --all refused, so this checkout is not the ${INSTALL} installation's: $(printf '%s' "$out" | oneline)"
    stop "Check 124 stopped: this is not the ${INSTALL} installation (fugaro doctor --all refused the checkout, or --project disagrees with the checkout's own project:, which is refused rather than resolved)"
  }
  line=$(printf '%s\n' "$out" | grep -m1 '^project: ' || true)
  case "$line" in
  *"(GCP ${P},"*)
    fact installation PASS "the installation this run is about is confirmed by its own read-back: '${line}' — doctor resolved the checkout's project to ${P}, which is the sandbox project this check's every gcloud command names"
    ;;
  *)
    fact installation FAIL "fugaro doctor's 'project:' line is '${line}', which does not name the GCP project ${P}: this is not the sandbox installation, and every step of Check 124 would be about another one"
    stop "Check 124 stopped: this is not the ${P} installation (fugaro doctor's project: line is not ${P}, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)"
    ;;
  esac
}

# ------------------------------------------------------------------- helpers
# run_capture OUT ERR CMD... — runs a command, records it for the log, and
# leaves its status in $RUN_STATUS. Every cloud command of this check goes
# through here, so a case can say exactly what was run, and the failure rows
# quote the tool's own words rather than a reconstruction of them.
RUN_STATUS=0
run_capture() { # OUT ERR CMD...
  local out=$1 err=$2
  shift 2
  say "+ $*"
  RUN_STATUS=0
  "$@" >"$out" 2>"$err" || RUN_STATUS=$?
  return 0
}

# sleep_for SECONDS — the waits. A check that follows a run has to wait, and a
# wait that never ends is a check nobody runs, so every wait here is bounded and
# says so in the row when it gives up.
sleep_for() { sleep "$1" 2>/dev/null || true; }

runs_json() { # the sandbox installation's runs, as JSON
  fugaro runs ls --project "$INSTALL" --json >"$1" 2>"$2"
}

# run_row FIELD: one field of the check's run in the runs JSON, by title prefix.
# The rows are selected by the TITLE the register's own launch gives them
# ("check 124b: …", "check 124c: …"), which is how a later part finds this
# check's run without a file of its own to get out of step.
run_field() { # JSON TITLE-PREFIX FIELD
  python3 - "$1" "$2" "$3" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
def get(o, path):
    cur = o
    for k in path.split("."):
        if isinstance(cur, dict):
            cur = cur.get(k)
        else:
            return ""
    return "" if cur is None else cur
for r in d.get("runs") or []:
    if str(r.get("title", "")).startswith(sys.argv[2]):
        print(get(r, sys.argv[3]))
        sys.exit(0)
PY
}

# job_of_run RUN_ID [STATE-FILTER] — the Batch job of one run, by the label the
# job itself records. `fugaro run --json` names the run and not the job, so the
# job is found by reading the labels back: `fugaro_run` is `<slug>-<run id>`, so
# the value ENDS with the run id, and this is the selector 124c-2's delete is
# scoped by.
job_of_run() { # RUN_ID
  gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
    --filter="labels.fugaro=managed" --format=json 2>/dev/null >"$SCRATCH/jobs.json" || true
  python3 - "$SCRATCH/jobs.json" "$1" <<'PY'
import json, sys
run = sys.argv[2]
try:
    jobs = json.load(open(sys.argv[1]))
except Exception:
    jobs = []
found = []
for j in jobs if isinstance(jobs, list) else []:
    labels = j.get("labels") or {}
    if str(labels.get("fugaro_run", "")).endswith(run) and labels.get("fugaro") == "managed":
        found.append((j.get("name", ""), labels.get("fugaro_run", "")))
for name, value in found:
    print(name)
    print(value)
    break
PY
}

# ---------------------------------------------------------------------- 124a
step_before() {
  run_capture "$W/jobs-before.txt" "$SCRATCH/err" \
    gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter="labels.fugaro=managed" --format='value(name,status.state)'
  [ "$RUN_STATUS" = 0 ] || {
    fact before FAIL "the before-state's job list could not be read: $(oneline <"$SCRATCH/err")"
    return 0
  }
  sort -o "$W/jobs-before.txt" "$W/jobs-before.txt"
  run_capture "$W/instances-before.txt" "$SCRATCH/err" \
    gcloud compute instances list --project="${P:?}" --filter="labels.fugaro=managed" --format='value(name)'
  [ "$RUN_STATUS" = 0 ] || {
    fact before FAIL "the before-state's instance list could not be read: $(oneline <"$SCRATCH/err")"
    return 0
  }
  sort -o "$W/instances-before.txt" "$W/instances-before.txt"
  run_capture "$W/templates-before.txt" "$SCRATCH/err" \
    gcloud compute instance-templates list --project="${P:?}" --format='value(name)'
  [ "$RUN_STATUS" = 0 ] || {
    fact before FAIL "the before-state's instance-template list could not be read: $(oneline <"$SCRATCH/err")"
    return 0
  }
  sort -o "$W/templates-before.txt" "$W/templates-before.txt"
  local nj ni nt
  nj=$(wc -l <"$W/jobs-before.txt" | tr -d ' ')
  ni=$(wc -l <"$W/instances-before.txt" | tr -d ' ')
  nt=$(wc -l <"$W/templates-before.txt" | tr -d ' ')
  # A non-empty list is not this check's failure: whatever is here was here
  # before it, and 124d's diff is exactly the reading that says so. What it is
  # NOT is deletable by this check.
  fact before INFO "the before-state is written to $W (jobs-before.txt ${nj}, instances-before.txt ${ni}, templates-before.txt ${nt}): jobs [$(oneline <"$W/jobs-before.txt")], instances [$(oneline <"$W/instances-before.txt")], templates [$(oneline <"$W/templates-before.txt")]. On a sandbox whose Batch backend has never run a job this is 0, 0 and 0 — the installation provisions no instance template at all (what the preload builds is an image family) — and a non-empty jobs or instances list is a run still in flight or one left behind, which is NOT this check's to delete: 124d diffs against these three files, so what is here now is what the check is measured from"
}

step_plan_only() {
  run_capture "$SCRATCH/plan-only.out" "$SCRATCH/plan-only.err" \
    fugaro setup --plan-only --provision-batch --project "$INSTALL"
  local out
  out=$(cat "$SCRATCH/plan-only.out" "$SCRATCH/plan-only.err")
  if [ "$RUN_STATUS" != 0 ]; then
    fact plan-only FAIL "fugaro setup --plan-only refused (exit ${RUN_STATUS}): $(printf '%s' "$out" | oneline). This form applies nothing and asks nothing, so a refusal here is the installation's or the tree's, and the Batch plan below cannot be read at all"
    return 0
  fi
  local computes planline
  computes=$(printf '%s\n' "$out" | grep -c 'module\.compute\.' || true)
  planline=$(printf '%s\n' "$out" | grep -m1 -- '--plan-only: nothing applied; the plan is' || true)
  if [ "$computes" != 0 ]; then
    fact plan-only FAIL "the plan-only terminal output carries ${computes} 'module.compute.' address(es), which the installation stage's plan-only stop cannot have reached (the compute stage is registered after the repository stage, and plan-only stops at the installation root): $(printf '%s' "$out" | oneline)"
    return 0
  fi
  fact plan-only PASS "fugaro setup --plan-only --provision-batch ran, applied nothing, asked nothing, and printed no 'module.compute.' address on the terminal — '${planline:-<no --plan-only line>}' — which is the reading 124a-2 exists for: this form shows the INSTALLATION root's plan and cannot show the Batch plan, and an owner who reads it as 'the Batch resources are not planned' has read a stop as a result. The Batch plan is printed by the apply, which is yours and which this script does not run. Whole output: $(printf '%s' "$out" | oneline)"
}

step_apply() {
  fact apply INFO "left for you, deliberately: ${INSTALL}'s Batch provisioning is applied by 'fugaro setup --provision-batch --project ${INSTALL}', which prints the compute root's plan and then asks you to TYPE the installation's name. This script never runs it — a coding agent's session is refused that stage by the CLI itself, and the flag that skips confirmations (--yes) does not cover it — and it is not a step of 124a's reading: whether the apply is already done is what the doctor and validate rows below read. If it is done, the same command prints 'No changes: the compute backend's provisioning matches the plan.' and nothing is applied. What it provisions is permanent and is NOT this check's litter: the two APIs, the run accounts' IAM, the launcher's, reaper's and preload accounts and roles, the Cloud NAT and its router, the preload's image family and the reaper's invocation"
}

step_doctor() {
  run_capture "$SCRATCH/doctor.out" "$SCRATCH/doctor.err" fugaro doctor --project "$INSTALL"
  local out lines warnings strict_status
  out=$(cat "$SCRATCH/doctor.out" "$SCRATCH/doctor.err")
  lines=$(printf '%s\n' "$out" | grep -E '^[a-z]+ batch-' || true)
  warnings=$(printf '%s\n' "$out" | grep -E '^warning batch-' || true)
  if [ -n "$warnings" ]; then
    fact doctor FAIL "fugaro doctor reports the Batch setup as NOT ok: $(printf '%s' "$warnings" | oneline). Each line carries the value the project answered that makes it not OK (an API that is not ENABLED, a family that is not there, a name that resolves to nothing, a sink whose destination or filter is not the installation's, a launcher whose conditioned grant is missing or unconditioned), or the read it could not make. Every batch- line, whole: $(printf '%s' "$lines" | oneline)"
    return 0
  fi
  if [ -z "$lines" ]; then
    fact doctor FAIL "fugaro doctor printed no 'batch-' line at all, not even the info one — an installation with no batch: block in its project config is one doctorBatchChecks returns nothing for, and this check is about the Batch backend: this is the first thing to fix, and the rest of 124a-5 says nothing about it. doctor's whole output: $(printf '%s' "$out" | oneline)"
    return 0
  fi
  local summary
  summary=$(printf '%s\n' "$out" | grep -m1 '^fugaro doctor:' || true)
  run_capture "$SCRATCH/strict.out" "$SCRATCH/strict.err" fugaro doctor --strict --project "$INSTALL"
  strict_status=$RUN_STATUS
  if [ "$strict_status" = 0 ]; then
    fact doctor PASS "fugaro doctor's Batch half is a pass, and it is read the way the register says it must be read: the only batch- line is the design's own boundary ('$(printf '%s' "$lines" | oneline)'), the other seven ids print nothing while the read behind each is OK, and --strict exits 0 ('${summary}'), so no warning batch- line is hiding in it. A second, still-flying run of this backend is the one thing that would print one"
  elif printf '%s\n' "$out" | grep -qE '^warning batch-'; then
    fact doctor FAIL "fugaro doctor --strict exits ${strict_status} and a 'warning batch-' line is why: $(printf '%s' "$warnings" | oneline)"
  else
    fact doctor INFO "fugaro doctor --strict exits ${strict_status} and NO batch- line is a warning: every batch- line is '$(printf '%s' "$lines" | oneline)' and doctor's summary is '${summary}'. A warning in another part of doctor is not this check's finding, but the exit code is the register's 'exit: 0', so record both readings: the batch- half is OK and doctor as a whole is not. Its whole output: $(printf '%s' "$out" | oneline)"
  fi
}

step_validate() {
  run_capture "$SCRATCH/validate.out" "$SCRATCH/validate.err" fugaro validate
  local out
  out=$(cat "$SCRATCH/validate.out" "$SCRATCH/validate.err")
  if [ "$RUN_STATUS" = 0 ] && printf '%s\n' "$out" | grep -q ' is valid'; then
    fact validate PASS "fugaro validate accepts the sandbox checkout's config — '$(printf '%s' "$out" | oneline)' — which is the offline half of the enable: the backend name reaches the offline checks, and what they judged is the machine type priced from the price table, batch.task_timeout under Batch's own RUNNING ceiling, and the artifact boundary"
    return 0
  fi
  case "$out" in
  *"is not enabled yet"*)
    fact validate FAIL "fugaro validate still refuses the backend name as one this build does not run: $(printf '%s' "$out" | oneline). The enable task has landed, so a refusal that names it is the finding now — it means this build predates it"
    ;;
  *)
    fact validate FAIL "fugaro validate refused (exit ${RUN_STATUS}): $(printf '%s' "$out" | oneline)"
    ;;
  esac
}

part_a() {
  BLOCK_TITLE="CHECK 124A RESULT"
  BLOCK_KEYS=(owner installation checkout belong workdir xtrace before plan-only apply doctor validate restore)
  if [ -n "$DRY_RUN" ]; then
    print_plan_a
    mark_not_taken before plan-only apply doctor validate
  else
    step_before
    step_plan_only
    step_apply
    step_doctor
    step_validate
  fi
  step_manual "paste this block into the register's Check 124a section: the three before-state counts, the plan-only output, the two read-backs (every batch- line whole with its severity word, the --strict exit code, and whether any batch- line appeared at all), and whether fugaro validate said '<path> is valid' — with the date, the commit, and whether you had to type the apply's confirmation in this sitting"
}

print_plan_a() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. --part a would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the sandbox checkout's origin, the work directory, and the project refusal"
  say "  [cloud]  fugaro doctor --project ${INSTALL} --all — the installation's own project: line, which is the stop that says this is ${P}"
  say "  [cloud]  gcloud batch jobs list / compute instances list / compute instance-templates list --project=${P} — the before-state, written to ${W:-<the work directory>}/jobs-before.txt, instances-before.txt and templates-before.txt"
  say "  [cloud]  fugaro setup --plan-only --provision-batch --project ${INSTALL} — the installation root's plan, applying nothing and asking nothing"
  say "  [yours]  fugaro setup --provision-batch --project ${INSTALL} — NOT run by this script: it asks you to type the installation's name, and a coding agent's session is refused that stage"
  say "  [cloud]  fugaro doctor --project ${INSTALL} and fugaro doctor --strict --project ${INSTALL} — every batch- line whole, and the strict exit code"
  say "  [local]  fugaro validate — the offline half: <path> is valid, and not the old 'is not enabled yet' refusal"
  say "and its Restore removes the scratch directory ${SCRATCH} and nothing else: the work directory is kept (124d diffs against it) and the installation's own provisioning stays"
}

# ---------------------------------------------------------------------- 124b
step_backend() {
  run_capture "$SCRATCH/backends.json" "$SCRATCH/backends.err" fugaro backends ls --project "$INSTALL" --json
  [ "$RUN_STATUS" = 0 ] || {
    fact backend FAIL "fugaro backends ls refused: $(oneline <"$SCRATCH/backends.err")"
    return 0
  }
  python3 - "$SCRATCH/backends.json" >"$SCRATCH/backends.fields" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print("selected|" + str(d.get("backend") or ""))
print("project|" + str(d.get("project") or ""))
print("complete|" + ("yes" if d.get("complete") else "no"))
print("note|" + str(d.get("note") or ""))
print("names|" + " ".join("%s%s" % (b.get("name"), "(in use)" if b.get("in_use") else "") for b in (d.get("backends") or [])))
PY
  local selected names complete note
  selected=$(sed -n 's/^selected|//p' "$SCRATCH/backends.fields")
  names=$(sed -n 's/^names|//p' "$SCRATCH/backends.fields")
  complete=$(sed -n 's/^complete|//p' "$SCRATCH/backends.fields")
  note=$(sed -n 's/^note|//p' "$SCRATCH/backends.fields")
  if [ "$complete" != yes ]; then
    fact backend FAIL "fugaro backends ls could select no project config ('complete' is false, note '${note}'), so the backend it prints is not authoritative and this check cannot say which one the installation runs on: $(oneline <"$SCRATCH/backends.json")"
    return 0
  fi
  case "$names" in
  *cloud-run*cloud-batch*) : ;;
  *) fact backend FAIL "fugaro backends ls lists '${names}', which does not hold both names the seam defines: $(oneline <"$SCRATCH/backends.json")" && return 0 ;;
  esac
  if [ "$selected" = cloud-batch ]; then
    fact backend PASS "the installation selects the opt-in backend: fugaro backends ls reports '${names}' with the selected one marked, and the project config's own backend: value is '${selected}' — which is what turns the name from known into selectable, and what 124b-2's launch runs on"
  else
    fact backend FAIL "the installation's backend: value is '${selected}' and not 'cloud-batch': the list is '${names}'. 124b is the task on the Batch backend, so a run launched here would be a run on the other one — set the project config's backend: to cloud-batch (it is the PROJECT config's key, not a repository fugaro.yaml's) and record that you did"
  fi
}

step_repo_yaml() {
  local file="$CHECKOUT/fugaro.yaml"
  if [ ! -f "$file" ]; then
    fact repo-yaml FAIL "the sandbox checkout has no fugaro.yaml at ${file}, so which configuration the run will resolve is unknown"
    return 0
  fi
  local hits
  hits=$(grep -nE '^[[:space:]]*backend:' "$file" || true)
  if [ -n "$hits" ]; then
    fact repo-yaml FAIL "the checkout's fugaro.yaml carries a backend: key — $(printf '%s' "$hits" | oneline) — and the repository config's decoder refuses unknown fields: it is the PROJECT config's key (internal/localcfg's BackendName), so this is a config error rather than a selection, and the run below would fail on it"
  else
    fact repo-yaml PASS "the checkout's fugaro.yaml carries no backend: key at all (the repository decoder refuses one), so the backend this run uses is the project config's, read by the row above"
  fi
}

step_launch_b() {
  local title="check 124b: one task on the Batch backend"
  run_capture "$SCRATCH/launch.json" "$SCRATCH/launch.err" \
    fugaro run --json --project "$INSTALL" --title "$title" \
    "Add one sentence to README.md saying which check last ran against this repository, and commit it."
  [ "$RUN_STATUS" = 0 ] || {
    fact launch FAIL "fugaro run refused (exit ${RUN_STATUS}): $(oneline <"$SCRATCH/launch.err"). Nothing was launched, so the rows below have nothing to read — and a refusal here is worth recording whole: it is the launcher's own words about this backend"
    return 0
  }
  RUN_ID=$(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
print(d.get("run_id") or "")' "$SCRATCH/launch.json" 2>/dev/null || true)
  RUN_BRANCH=$(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
print(d.get("branch") or "")' "$SCRATCH/launch.json" 2>/dev/null || true)
  local url
  url=$(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
print(d.get("log_url") or "")' "$SCRATCH/launch.json" 2>/dev/null || true)
  if [ -z "$RUN_ID" ]; then
    fact launch FAIL "the launch printed no run_id, so the run cannot be followed: $(oneline <"$SCRATCH/launch.json")"
    return 0
  fi
  fact launch PASS "one ordinary sandbox task is launched on the installation: run ${RUN_ID} on branch ${RUN_BRANCH:-<none in the launch JSON>} (log: ${url:-<none>}), from the register's own title and prompt — nothing about the task is Batch-specific, which is the point: a task written for the backend would test the task. The launch's whole JSON: $(oneline <"$SCRATCH/launch.json")"
}

step_job_spec() {
  [ -n "$RUN_ID" ] || {
    fact job PASS "not reached: nothing was launched, so there is no job to read — the launch row above says why"
    return 0
  }
  local pair job
  pair=$(job_of_run "$RUN_ID")
  job=$(printf '%s\n' "$pair" | sed -n '1p')
  JOB_LABEL_VALUE=$(printf '%s\n' "$pair" | sed -n '2p')
  if [ -z "$job" ]; then
    fact job FAIL "no Batch job in ${P}/${L} carries a fugaro_run value ending with this run's id (${RUN_ID}) beside the fugaro=managed marker, so the run either was not launched on Batch or its job is not attributable — read the run's own record and the log above before going on"
    return 0
  fi
  JOB=$job
  run_capture "$SCRATCH/job.json" "$SCRATCH/err" gcloud batch jobs describe "$JOB" --project="${P:?}" --location="${L:?}" --format=json
  [ "$RUN_STATUS" = 0 ] || {
    fact job FAIL "the job ${JOB} could not be described: $(oneline <"$SCRATCH/err")"
    return 0
  }
  python3 - "$SCRATCH/job.json" >"$SCRATCH/job.fields" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
alloc = d.get("allocationPolicy") or {}
inst = (alloc.get("instances") or [{}])[0]
pol = inst.get("policy") or {}
boot = ((pol.get("bootDisk") or {}).get("image") or "")
nets = ((alloc.get("network") or {}).get("networkInterfaces") or [{}])
net = nets[0] if nets else {}
print("jobid|" + str(d.get("name", "").split("/")[-1]))
print("machine|" + str(pol.get("machineType") or ""))
print("boot|" + str(boot))
print("network|" + str(net.get("network") or ""))
print("subnetwork|" + str(net.get("subnetwork") or ""))
print("labels|" + " ".join("%s=%s" % (k, (d.get("labels") or {})[k]) for k in sorted(d.get("labels") or {})))
spot = "yes" if "spot" in json.dumps(alloc).lower() else "no"
print("spot|" + spot)
PY
  JOB_ID=$(sed -n 's/^jobid|//p' "$SCRATCH/job.fields")
  local machine boot network subnetwork labels spot bad=""
  machine=$(sed -n 's/^machine|//p' "$SCRATCH/job.fields")
  boot=$(sed -n 's/^boot|//p' "$SCRATCH/job.fields")
  network=$(sed -n 's/^network|//p' "$SCRATCH/job.fields")
  subnetwork=$(sed -n 's/^subnetwork|//p' "$SCRATCH/job.fields")
  labels=$(sed -n 's/^labels|//p' "$SCRATCH/job.fields")
  spot=$(sed -n 's/^spot|//p' "$SCRATCH/job.fields")
  case "$boot" in
  *fugaro-preload-*) : ;;
  *) bad="${bad}its boot image is '${boot}' and not the installation's preloaded image family (fugaro-preload-<8 hex>, the family 124a's apply builds); " ;;
  esac
  [ -n "$network" ] || bad="${bad}its allocation policy names no network (a default network is not the design's explicit one); "
  [ -n "$subnetwork" ] || bad="${bad}its allocation policy names no subnetwork; "
  [ "$spot" = no ] || bad="${bad}its allocation policy names a Spot provisioning model, and nothing in this check runs Spot; "
  if [ -n "$bad" ]; then
    fact job FAIL "the run's Batch job ${JOB_ID} is there but ${bad}the fields verbatim: machine type '${machine}', boot image '${boot}', network '${network}', subnetwork '${subnetwork}', labels '${labels}'"
  else
    fact job PASS "the run's Batch job is ${JOB_ID}, found by its own labels ('${labels}', whose fugaro_run value is the run's own ${JOB_LABEL_VALUE:-${RUN_ID}}) rather than by a name typed here: it boots the installation's preloaded image family ('${boot}'), on the explicit network '${network}' and subnetwork '${subnetwork}', on a '${machine}', with no Spot provisioning model anywhere in its allocation policy"
  fi
}

# step_run_b: the draft in flight, and the end. A Fugaro run opens its pull
# request as a draft at its first checkpoint push and flips it at finalize, so
# the draft is a reading taken in flight and the end is a reading taken after.
step_run_b() {
  [ -n "$RUN_ID" ] || {
    fact run FAIL "not taken: nothing was launched (the launch row above says why), so there is no run to follow"
    return 0
  }
  local waited=0 bound=${CHK124_RUN_WAIT_SECONDS:-2400} seen_draft="" text
  run_capture "$SCRATCH/runs.txt" "$SCRATCH/err" fugaro runs ls --project "$INSTALL"
  text=$(grep -F "$RUN_ID" "$SCRATCH/runs.txt" || true)
  while :; do
    runs_json "$SCRATCH/runs.json" "$SCRATCH/err" || true
    local status outcome pr prurl prstate
    status=$(run_field "$SCRATCH/runs.json" "check 124b:" "status")
    outcome=$(run_field "$SCRATCH/runs.json" "check 124b:" "outcome")
    pr=$(run_field "$SCRATCH/runs.json" "check 124b:" "pr")
    prurl=$(run_field "$SCRATCH/runs.json" "check 124b:" "pr_url")
    prstate=$(run_field "$SCRATCH/runs.json" "check 124b:" "pr_state")
    if [ -z "$seen_draft" ]; then
      run_capture "$SCRATCH/runs.txt" "$SCRATCH/err" fugaro runs ls --project "$INSTALL"
      text=$(grep -F "$RUN_ID" "$SCRATCH/runs.txt" || true)
      if [ -n "$text" ]; then
        seen_draft=1
        fact draft INFO "the row while the run was in flight, read at ${waited}s: '$(printf '%s' "$text" | oneline)' (status '${status}', the pull request ${pr:-none}${prurl:+ at ${prurl}}, its state '${prstate:-none}', the row's own outcome '${outcome:-none}') — a Fugaro run opens its pull request as a draft at its first checkpoint push, so this is the reading that says the draft was really opened, not that it was assumed"
      fi
    fi
    case "$status" in
    running | pending | launching | unlaunched | "") : ;;
    *)
      if [ "$status" = running ]; then :; fi
      if [ -n "$pr" ] && [ "$pr" != 0 ] && [ "$outcome" = ready ] && [ "$status" = succeeded ]; then
        fact run PASS "the run ended: fugaro runs ls reads it 'succeeded' with its pull request ready — ${pr} at ${prurl:-<no url>} (pr_state '${prstate:-none}', outcome '${outcome}') — after ${waited}s. The last row read: '$(printf '%s' "$text" | oneline)'. So the whole path ran on this backend: the launcher, the runner, the gateway, the git provider and finalize"
      else
        fact run FAIL "the run ended without a ready pull request: status '${status}', outcome '${outcome:-none}', pull request '${pr:-none}' at '${prurl:-none}' (pr_state '${prstate:-none}'), reason '$(run_field "$SCRATCH/runs.json" "check 124b:" "reason")', derived '$(run_field "$SCRATCH/runs.json" "check 124b:" "derived")'. The last row read: '$(printf '%s' "$text" | oneline)'. A run that ended a draft, or was left stalled, is the finding 124b-3's 'the pull request ready' is about — record the row whole"
      fi
      RUN_B_PR=$(printf '%s' "$pr")
      RUN_B_PR_URL=$(printf '%s' "$prurl")
      RUN_B_OUTCOME=$(printf '%s' "$outcome")
      return 0
      ;;
    esac
    if [ "$waited" -ge "$bound" ]; then
      fact run FAIL "the run was still going after ${bound}s (status '${status}', stage '$(run_field "$SCRATCH/runs.json" "check 124b:" "stage")'), so this sitting could not read its end: '$(printf '%s' "$text" | oneline)'. Re-run --part b later with CHK124_RUN_WAIT_SECONDS raised (a first Batch run on a fresh installation pays the preload and the checkout), or read the row yourself and paste it — nothing below it was measured"
      return 0
    fi
    sleep_for "${CHK124_POLL_SECONDS:-30}"
    waited=$((waited + ${CHK124_POLL_SECONDS:-30}))
  done
}

# step_decline: 124b's own restore. The pull request and the branch are the
# check's litter and nothing else is; the two calls are the register's own, the
# token stays in a file and never on a command line, and the repository is
# spelled out rather than built from a variable.
step_decline() {
  local pr=${RUN_B_PR:-0}
  if [ -z "${CHK124_TOKEN_FILE:-}" ]; then
    fact decline FAIL "the run's pull request${RUN_B_PR:+ (${RUN_B_PR}${RUN_B_PR_URL:+ at ${RUN_B_PR_URL}}) }is still open: the decline and the branch delete are made with the sandbox token, so export CHK124_TOKEN_FILE=<the file holding the sandbox token> and re-run --part b (or make the two calls by hand from the register's own block). Nothing was declined and nothing was deleted, and the pull request is left for you"
    return 0
  fi
  if [ ! -f "$CHK124_TOKEN_FILE" ]; then
    fact decline FAIL "CHK124_TOKEN_FILE is '${CHK124_TOKEN_FILE}' and there is no such file: nothing was declined and the branch was not deleted"
    return 0
  fi
  if [ -z "${RUN_B_PR:-}" ] || [ "${RUN_B_PR:-0}" = 0 ]; then
    fact decline INFO "the run's row names no pull request number, so the decline has nothing to decline: read the runs row above and make the two calls by hand if a pull request was opened — a run that ended without one is the reading 124b-3 is about, and nothing here was deleted"
    return 0
  fi
  local code
  run_capture "$SCRATCH/decline.json" "$SCRATCH/decline.err" \
    curl -fsS --config <(printf 'header = "Authorization: Bearer %s"\n' "$(cat "$CHK124_TOKEN_FILE")") -X POST \
    "https://api.bitbucket.org/2.0/repositories/${REPO}/pullrequests/${RUN_B_PR}/decline"
  if [ "$RUN_STATUS" != 0 ]; then
    fact decline FAIL "the decline of pull request ${RUN_B_PR} failed (exit ${RUN_STATUS}): $(oneline <"$SCRATCH/decline.err"). Nothing was deleted: the branch delete below it is deliberately not attempted, because a pull request left open over a deleted branch is worse than one left open"
    return 0
  fi
  code=$(python3 -c 'import json,sys
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    d={}
print(d.get("state") or "")' "$SCRATCH/decline.json" 2>/dev/null || true)
  run_capture "$SCRATCH/branch.err" "$SCRATCH/branch.out" \
    curl -fsS --config <(printf 'header = "Authorization: Bearer %s"\n' "$(cat "$CHK124_TOKEN_FILE")") -X DELETE \
    "https://api.bitbucket.org/2.0/repositories/${REPO}/refs/branches/fugaro/${RUN_ID}"
  local branch_status=$RUN_STATUS
  run_capture "$SCRATCH/runs-after.txt" "$SCRATCH/err" fugaro runs ls --project "$INSTALL"
  local history
  history=$(grep -F "$RUN_ID" "$SCRATCH/runs-after.txt" || true)
  local branch_note
  if [ "$branch_status" = 0 ]; then
    branch_note="the branch refs/branches/fugaro/${RUN_ID} was deleted"
  else
    branch_note="the branch delete failed (exit ${branch_status}): $(oneline <"$SCRATCH/branch.err") — delete refs/branches/fugaro/${RUN_ID} by hand"
  fi
  if [ "$code" = DECLINED ] && [ "$branch_status" = 0 ]; then
    fact decline PASS "the check's own litter is cleared: the pull request ${RUN_B_PR} is DECLINED (the API's own answer: $(oneline <"$SCRATCH/decline.json")) and ${branch_note}. The run's history is still there — '$(printf '%s' "$history" | oneline)' — which is the read-back: a run's record is history and is not restored by declining its pull request. Nothing was merged, and nothing but this pull request and this branch was touched"
  elif [ "$code" = DECLINED ]; then
    fact decline FAIL "the pull request ${RUN_B_PR} is DECLINED, but ${branch_note}. The RESTORE is not finished: the branch is the check's own litter and it is still there"
  else
    fact decline FAIL "the decline answered '${code}' where the register's expected output is \"state\": \"DECLINED\" ($(oneline <"$SCRATCH/decline.json")). ${branch_note} — read the answer whole: a token without write access to ${REPO} answers 403 here"
  fi
}

part_b() {
  BLOCK_TITLE="CHECK 124B RESULT"
  BLOCK_KEYS=(owner installation checkout belong workdir xtrace backend repo-yaml launch job draft run decline restore)
  if [ -n "$DRY_RUN" ]; then
    print_plan_b
    mark_not_taken backend repo-yaml launch job draft run decline
  else
    step_backend
    step_repo_yaml
    step_launch_b
    step_job_spec
    step_run_b
    step_decline
  fi
  step_manual "paste this block into the register's Check 124b section: fugaro backends ls's two names and which one is selected, whether the checkout's fugaro.yaml carries a backend: key, the run_id and the job's own name and labels, the launch's JSON, the row read while the run was in flight and again after it ended, the draft-or-ready reading, the pull request's number and URL, the decline's JSON and the branch delete, and the run's own record of which backend it ran on — with the date and the commit"
}

print_plan_b() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. --part b would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the sandbox checkout's origin, the work directory, and the project refusal"
  say "  [cloud]  fugaro doctor --project ${INSTALL} --all — the installation's own project: line"
  say "  [cloud]  fugaro backends ls --project ${INSTALL} --json — the two names and the selected backend:"
  say "  [local]  the checkout's fugaro.yaml, read for a backend: key the repository decoder would refuse"
  say "  [cloud]  fugaro run --json --project ${INSTALL} --title 'check 124b: one task on the Batch backend' '<the register's prompt>' — ONE real run of the installation's agent, on a Batch VM, billed"
  say "  [cloud]  gcloud batch jobs list --filter=labels.fugaro=managed, then describe the job whose fugaro_run value ends with this run's id — its labels, its machine type, its boot image family, its network and subnetwork, and that no Spot provisioning model is anywhere in its allocation policy"
  say "  [cloud]  fugaro runs ls --project ${INSTALL} while the run is in flight and again until it ends — the draft row, and the ready one"
  say "  [cloud]  curl -X POST …/pullrequests/<id>/decline and curl -X DELETE …/refs/branches/fugaro/<run-id> — the check's own restore, with the sandbox token from \${CHK124_TOKEN_FILE} in a --config file, never on a command line"
  say "  [cloud]  fugaro runs ls --project ${INSTALL} — the read-back that the run's history is still there"
  say "and its Restore removes the scratch directory ${SCRATCH} and nothing else: the pull request and the branch ARE the restore (above), and the run's record and the job's history are deliberately not restored"
}

# ---------------------------------------------------------------------- 124c
step_exit_code() {
  # The number is read, never remembered: it is a fact about the tree this
  # checkout is at, and the register refuses to name it for exactly that reason.
  local tree=$CHECKOUT file hits
  file="$CHECKOUT/internal/backend/batch/run.go"
  if [ ! -f "$file" ]; then
    # The sandbox is this check's own checkout, but a sandbox without the Fugaro
    # tree still has to produce the fact rather than skip it: the tree the
    # script lives in is the second, named source.
    tree=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd || true)
    file="$tree/internal/backend/batch/run.go"
    [ -f "$file" ] || {
      fact exit-code FAIL "neither this checkout (${CHECKOUT}) nor the tree this script lives in (${tree:-<none>}) carries internal/backend/batch/run.go, so the exit code this part's assertion is about cannot be read — and it is not written into this script on purpose"
      return 0
    }
    EXIT_CODE_SOURCE="${tree} (the tree this script lives in; the checkout does not carry the Fugaro tree)"
    EXIT_CODE_SHA=$(git -C "$tree" rev-parse HEAD 2>/dev/null | head -n 1)
    [ -n "$EXIT_CODE_SHA" ] || EXIT_CODE_SHA=unknown
  else
    EXIT_CODE_SOURCE="${CHECKOUT}"
    EXIT_CODE_SHA=$CHECKOUT_SHA
  fi
  hits=$(grep -n 'preemptExitCode =' "$file" || true)
  PREEMPT_EXIT_CODE=$(sed -n 's/.*preemptExitCode *= *\([0-9]\+\).*/\1/p' <<<"$hits" | head -n 1)
  if [ -z "$PREEMPT_EXIT_CODE" ]; then
    fact exit-code FAIL "internal/backend/batch/run.go carries no readable 'preemptExitCode =' declaration: $(printf '%s' "$hits" | oneline). The whole of 124c-3 is the reading that the job's own report carries this number, so nothing below can be concluded"
    return 0
  fi
  fact exit-code INFO "read out of the tree, not remembered: '$(printf '%s' "$hits" | oneline)' in ${EXIT_CODE_SOURCE} at commit ${EXIT_CODE_SHA}. Record both verbatim in the register and do NOT write the number into it — the register's own note says why: the value is a fact about the commit the check ran at, and a document that named it would be a second home for it. The assertion below is that the failed job's own report carries exactly this value"
}

step_launch_c() {
  run_capture "$SCRATCH/launch.json" "$SCRATCH/launch.err" \
    fugaro run --json --project "$INSTALL" --title "check 124c: the preemption" \
    "Add one sentence to README.md saying which check last ran against this repository, and commit it."
  [ "$RUN_STATUS" = 0 ] || {
    fact launch FAIL "fugaro run refused (exit ${RUN_STATUS}): $(oneline <"$SCRATCH/launch.err"). Nothing was launched and no VM will be deleted by this part"
    return 0
  }
  RUN_ID=$(python3 -c 'import json,sys
print((json.load(open(sys.argv[1])) or {}).get("run_id") or "")' "$SCRATCH/launch.json" 2>/dev/null || true)
  RUN_BRANCH=$(python3 -c 'import json,sys
print((json.load(open(sys.argv[1])) or {}).get("branch") or "")' "$SCRATCH/launch.json" 2>/dev/null || true)
  [ -n "$RUN_ID" ] || {
    fact launch FAIL "the launch printed no run_id: $(oneline <"$SCRATCH/launch.json")"
    return 0
  }
  fact launch PASS "one ordinary sandbox task is launched and caught in flight: run ${RUN_ID} on branch ${RUN_BRANCH:-<none>} — the same task the register's other run checks use, long enough to be caught with its job RUNNING and its VM alive. Nothing here is Spot: there is no config key that asks for a Spot provisioning model on this tree, so the preemption below is SIMULATED by taking the VM away by hand (the design's own Spot method, §7.2 step 7, remains unrun). Launch JSON: $(oneline <"$SCRATCH/launch.json")"
}

# step_in_flight: the job RUNNING and its VM alive, which is what the delete
# below needs. Both are reads, and both are bounded: a job that never leaves
# QUEUED is a reading (and the reaper's business), not something to wait out.
step_in_flight() {
  [ -n "$RUN_ID" ] || {
    fact in-flight FAIL "not taken: nothing was launched, so there is no job and no VM to catch (the launch row above says why)"
    return 0
  }
  local waited=0 bound=${CHK124_INFLIGHT_WAIT_SECONDS:-900}
  while :; do
    local pair
    pair=$(job_of_run "$RUN_ID")
    JOB=$(printf '%s\n' "$pair" | sed -n '1p')
    JOB_LABEL_VALUE=$(printf '%s\n' "$pair" | sed -n '2p')
    if [ -n "$JOB" ]; then
      run_capture "$SCRATCH/state.txt" "$SCRATCH/err" gcloud batch jobs describe "$JOB" --project="${P:?}" --location="${L:?}" --format='value(status.state)'
      JOB_STATE=$(oneline <"$SCRATCH/state.txt")
      if [ "$JOB_STATE" = RUNNING ]; then
        run_capture "$SCRATCH/vms.txt" "$SCRATCH/err" gcloud compute instances list --project="${P:?}" \
          --filter="labels.fugaro_run=${JOB_LABEL_VALUE:?} AND labels.fugaro=managed" --format='value(name,zone.basename())'
        local n
        n=$(grep -c . "$SCRATCH/vms.txt" || true)
        if [ "$n" = 1 ]; then
          fact in-flight PASS "the run is on a VM of its own: job ${JOB} (whose own fugaro_run label value is ${JOB_LABEL_VALUE}) reads ${JOB_STATE}, and exactly one VM carries that value beside the installation's marker — '$(oneline <"$SCRATCH/vms.txt")' — after ${waited}s. This is the VM 124c-2's delete is scoped to, and the count is the reason it is scoped: a second one would mean the selection is not the check's job's"
          return 0
        fi
        if [ "$waited" -ge "$bound" ]; then
          fact in-flight FAIL "the job ${JOB} reads ${JOB_STATE} but ${n} VM(s) carry its label value (${JOB_LABEL_VALUE}) where the delete below needs exactly one: $(oneline <"$SCRATCH/vms.txt"). Nothing was deleted"
          return 0
        fi
      fi
    fi
    if [ "$waited" -ge "$bound" ]; then
      fact in-flight FAIL "the job of run ${RUN_ID} did not reach RUNNING with a VM of its own within ${bound}s (the job is '${JOB:-<not found by its label>}', its state '${JOB_STATE:-<none>}'). Nothing was deleted: a job that never leaves QUEUED is the reaper's business, not a preemption to stage, and this sitting cannot take a VM that does not exist. Re-run --part c with CHK124_INFLIGHT_WAIT_SECONDS raised if the queue was simply slow"
      return 0
    fi
    sleep_for "${CHK124_POLL_SECONDS:-30}"
    waited=$((waited + ${CHK124_POLL_SECONDS:-30}))
  done
}

# step_reclaim: the four reads and the one delete. The VM is selected by the
# label value the JOB records, read back off the job, never by a name typed into
# this check, and the guard refuses anything but exactly one such VM.
step_reclaim() {
  if [ -z "${JOB:-}" ] || [ -z "${JOB_LABEL_VALUE:-}" ]; then
    fact reclaim FAIL "not taken: no job of this run's was caught in flight, so which VM is the run's own is unknown and NOTHING was deleted"
    return 0
  fi
  run_capture "$SCRATCH/vms.txt" "$SCRATCH/err" gcloud compute instances list --project="${P:?}" \
    --filter="labels.fugaro_run=${JOB_LABEL_VALUE:?} AND labels.fugaro=managed" --format='value(name,zone.basename())'
  mapfile -t VMS < <(grep . "$SCRATCH/vms.txt" || true)
  if [ "${#VMS[@]}" != 1 ]; then
    fact reclaim FAIL "the selection found ${#VMS[@]} VM(s) carrying the job's own label value (${JOB_LABEL_VALUE}) where it must find exactly one: '$(oneline <"$SCRATCH/vms.txt")'. Nothing was deleted — the register's own stop is that a second line means the selection is not this job's, and the filter's labels.fugaro=managed half is what refuses a VM that is not the installation's"
    return 0
  fi
  read -r VM ZONE <<<"${VMS[0]}"
  if [ -z "${VM:-}" ] || [ -z "${ZONE:-}" ]; then
    fact reclaim FAIL "no VM carries the job's own label value (${JOB_LABEL_VALUE}): '$(oneline <"$SCRATCH/vms.txt")'. Nothing was deleted"
    return 0
  fi
  run_capture "$SCRATCH/delete.out" "$SCRATCH/err" gcloud compute instances delete "${VM:?}" --project="${P:?}" --zone="${ZONE:?}" --quiet
  if [ "$RUN_STATUS" != 0 ]; then
    fact reclaim FAIL "the delete of ${VM} (zone ${ZONE}) failed (exit ${RUN_STATUS}): $(oneline <"$SCRATCH/err"). This is the check's own run's VM, selected by the job's own label value ${JOB_LABEL_VALUE}; the run is still flying and nothing below can be read from it"
    return 0
  fi
  fact reclaim PASS "the run's own VM was taken away by hand, which is what a Spot reclaim does to a run: job ${JOB} → its own label value ${JOB_LABEL_VALUE} → exactly one VM, ${VM} in ${ZONE}, deleted ('$(oneline <"$SCRATCH/delete.out")'). The four reads and the delete, in that order, and the delete names the run's own label value and nothing else: this is the only delete anywhere in Check 124, and the register's step 7 note is why it is a simulation — no Fugaro run is Spot on this tree, so the platform's own reclaim stays unrun"
}

# step_terminal: the job FAILED, the retry count really zero, the exit code the
# tree carries, and the row the read side derives.
step_terminal() {
  if [ -z "${JOB:-}" ]; then
    fact terminal FAIL "not taken: no job of this run's was found, so there is nothing to read the ending off"
    return 0
  fi
  local waited=0 bound=${CHK124_TERMINAL_WAIT_SECONDS:-900} state=""
  while :; do
    run_capture "$SCRATCH/state.txt" "$SCRATCH/err" gcloud batch jobs describe "$JOB" --project="${P:?}" --location="${L:?}" --format='value(status.state)'
    state=$(oneline <"$SCRATCH/state.txt")
    case "$state" in
    FAILED | SUCCEEDED | CANCELLED) break ;;
    esac
    if [ "$waited" -ge "$bound" ]; then
      fact terminal FAIL "the job ${JOB} had not reached a terminal state within ${bound}s of its VM being taken away (it reads '${state}'). Nothing below was measured: record the state and the job's own description"
      return 0
    fi
    sleep_for "${CHK124_POLL_SECONDS:-30}"
    waited=$((waited + ${CHK124_POLL_SECONDS:-30}))
  done
  run_capture "$SCRATCH/job.json" "$SCRATCH/err" gcloud batch jobs describe "$JOB" --project="${P:?}" --location="${L:?}" --format=json
  python3 - "$SCRATCH/job.json" >"$SCRATCH/terminal.fields" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
st = d.get("status") or {}
tg = (d.get("taskGroups") or [{}])[0]
ts = tg.get("taskSpec") or {}
res = tg.get("taskGroups") if False else None
code = ""
execs = st.get("taskGroups") or {}
for name, g in (execs.items() if isinstance(execs, dict) else []):
    for t in (g.get("tasks") or []):
        if "exitCode" in t:
            code = t.get("exitCode")
print("state|" + str(st.get("state") or ""))
print("retry|" + ("present:%s" % ts.get("maxRetryCount") if "maxRetryCount" in ts else "absent"))
print("code|" + str(code))
print("events|" + " | ".join(str(e.get("description") or e.get("type") or "") for e in (st.get("statusEvents") or [])))
PY
  local retry code events
  retry=$(sed -n 's/^retry|//p' "$SCRATCH/terminal.fields")
  code=$(sed -n 's/^code|//p' "$SCRATCH/terminal.fields")
  events=$(sed -n 's/^events|//p' "$SCRATCH/terminal.fields")
  runs_json "$SCRATCH/runs.json" "$SCRATCH/err" || true
  local row_status row_derived row_reason
  row_status=$(run_field "$SCRATCH/runs.json" "check 124c:" "status")
  row_derived=$(run_field "$SCRATCH/runs.json" "check 124c:" "derived")
  row_reason=$(run_field "$SCRATCH/runs.json" "check 124c:" "reason")
  if [ "$state" != FAILED ]; then
    fact terminal FAIL "the job of the preempted run ended '${state}' and not FAILED: the whole of 124c-3 is that taking the run's VM away ends it FAILED with the task's own exit code in its report. Status events: ${events}. The run's row: status '${row_status}', derived '${row_derived:-none}', reason '${row_reason:-none}' — record all of it as the finding, and do not adjust the check to it"
    return 0
  fi
  local bad=""
  case "$retry" in
  "present:0") : ;;
  absent) bad="${bad}its task spec carries no maxRetryCount at all, and the field is written with ForceSendFields precisely so that a zero survives (an int64 with omitempty drops it) — without it Batch may attempt the task again and the ending below is a retry's, not the preemption's; " ;;
  *) bad="${bad}its task spec's maxRetryCount is ${retry#present:} and not 0, so this ending may be a retry's rather than the preemption's; " ;;
  esac
  if [ "$row_status" != failed ]; then
    bad="${bad}the run's row reads '${row_status}' and not 'failed'; "
  fi
  if [ "$row_derived" != preempted ]; then
    bad="${bad}the run's row carries no 'preempted' derivation (derived '${row_derived:-none}', reason '${row_reason:-none}'), and the read side derives exactly that from a job that ended FAILED because its VM was reclaimed; "
  fi
  if [ -z "$PREEMPT_EXIT_CODE" ]; then
    bad="${bad}the tree's own value could not be read (the exit-code row above says why), so the job's ${code:-<none>} cannot be compared with it; "
  elif [ "$code" != "$PREEMPT_EXIT_CODE" ]; then
    bad="${bad}the task's own exit code is '${code}' where the tree's preemptExitCode is '${PREEMPT_EXIT_CODE}' — if no status event says the VM was preempted, then the derivation's exit-code arm does not cover a hand-deleted VM and the job reads only through its description. That is exactly the fact this step was written to settle: RECORD IT, do not adjust the check to it; "
  fi
  if [ -n "$bad" ]; then
    fact terminal FAIL "the preempted run ended the way the register expects in its state (${state}) but ${bad}the readings verbatim: exit code '${code:-<none>}', retry '${retry}', run row status '${row_status}', derived '${row_derived:-none}', reason '${row_reason:-none}'; the job's status events: ${events}"
  else
    fact terminal PASS "the preemption reads the way the read side is built on it: the job ended ${state} (not CANCELLED), its task spec's maxRetryCount is present and 0 (so nothing was retried), the task's own exit code '${code}' equals the value read out of the tree at ${EXIT_CODE_SHA}, and the run's row reads '${row_status}' with the derivation '${row_derived}' — which is the second thing no unit test can see: a row read after such an ending says it while the record itself stays unfinished. Status events: ${events}"
  fi
}

step_reaper() {
  run_capture "$SCRATCH/reaper-dry.out" "$SCRATCH/reaper-dry.err" fugaro reaper --dry-run --project "$INSTALL"
  local dry_status=$RUN_STATUS dry
  dry=$(grep -m1 -E '^(would reap|reaped) projects/' "$SCRATCH/reaper-dry.out" || true)
  if [ "$dry_status" != 0 ] || [ -z "$dry" ]; then
    fact reaper FAIL "fugaro reaper --dry-run printed no sweep headline (exit ${dry_status}): $(oneline <"$SCRATCH/reaper-dry.out"; oneline <"$SCRATCH/reaper-dry.err")"
    return 0
  fi
  run_capture "$SCRATCH/reaper.out" "$SCRATCH/reaper.err" fugaro reaper --project "$INSTALL"
  local real_status=$RUN_STATUS real
  real=$(grep -m1 -E '^reaped projects/' "$SCRATCH/reaper.out" || true)
  local both
  both=$(cat "$SCRATCH/reaper-dry.out" "$SCRATCH/reaper.out")
  if [ "$real_status" != 0 ] || [ -z "$real" ]; then
    fact reaper FAIL "the real pass printed no sweep headline (exit ${real_status}): $(oneline <"$SCRATCH/reaper.out"; oneline <"$SCRATCH/reaper.err"). The dry run's headline was '${dry}'"
    return 0
  fi
  local refused named
  refused=$(printf '%s\n' "$both" | grep -E '^refused ' || true)
  named=$(printf '%s\n' "$both" | grep -F "$VM" || true)
  if [ -n "$named" ]; then
    fact reaper FAIL "the pass names the preempted run's VM (${VM}) in its report — '$(printf '%s' "$named" | oneline)' — and it is already gone: the delete above was the check's own step, so a report that still has it is the reaper reading a stale list, or a second VM that is not this check's. Record both reports whole"
    return 0
  fi
  fact reaper PASS "the sweeper's business is the VM and the job and not the record, read both ways: '${dry}' dry, and '${real}' as the real pass, then one line per action and one refusal per refusal — the preempted run's VM (${VM}) is in NEITHER report (it is already gone), nothing in either names the run's record, and the refusals are $(if [ -n "$refused" ]; then printf '%s' "$refused" | oneline; else printf 'none'; fi). A VM whose job is still running is refused, and that is a second, still-flying run of this backend rather than this check's to delete. The two reports whole: $(printf '%s' "$both" | oneline)"
}

part_c() {
  BLOCK_TITLE="CHECK 124C RESULT"
  BLOCK_KEYS=(owner installation checkout belong workdir xtrace exit-code launch in-flight reclaim terminal reaper restore)
  if [ -n "$DRY_RUN" ]; then
    # The exit code is a local read of the tree, so a dry run makes it too: it
    # is the one fact of this part that no cloud read could change.
    step_exit_code
    print_plan_c
    mark_not_taken launch in-flight reclaim terminal reaper
  else
    step_exit_code
    step_launch_c
    step_in_flight
    step_reclaim
    step_terminal
    step_reaper
  fi
  step_manual "paste this block into the register's Check 124c section: the preemptExitCode line and the commit it was read at (the LINE, never the number as a sentence of the document), the run_id and its job, the job's state and the one VM's name and zone, the delete's answer, the job's terminal state, the task's own exit code, the status events, maxRetryCount, the runs row with its derivation, and both reaper reports whole — with the date, and with the note that the preemption was simulated rather than a platform reclaim"
}

print_plan_c() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. --part c would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the sandbox checkout's origin, the work directory, and the project refusal"
  say "  [cloud]  fugaro doctor --project ${INSTALL} --all — the installation's own project: line"
  say "  [local]  grep -n 'preemptExitCode =' <the checkout>/internal/backend/batch/run.go — read by this dry run too, since it is a local read of the tree and no cloud read could change it; the value it carries is never written into any document, only into the block's own row"
  say "  [cloud]  fugaro run --json --project ${INSTALL} --title 'check 124c: the preemption' '<the register's prompt>' — ONE real run, billed, caught with its job RUNNING and its VM alive"
  say "  [cloud]  gcloud batch jobs list --filter=labels.fugaro=managed, then describe the job whose fugaro_run value ends with this run's id — the four reads 124c-2 makes"
  say "  [cloud]  gcloud compute instances list --filter='labels.fugaro_run=<the job's own value> AND labels.fugaro=managed', refusing anything but exactly one VM"
  say "  [cloud]  gcloud compute instances delete <that VM> --zone=<its zone> --quiet — the ONE delete in this whole check, and the preemption itself"
  say "  [cloud]  gcloud batch jobs describe (state, json(status)) and fugaro runs ls --project ${INSTALL} — FAILED, maxRetryCount present and 0, the task's exit code against the tree's, and the row's 'failed (preempted)'"
  say "  [cloud]  fugaro reaper --dry-run and fugaro reaper --project ${INSTALL} — both reports whole, and that the preempted VM is in neither"
  say "and its Restore removes the scratch directory ${SCRATCH} and nothing else: the VM is already gone (that was the step), the job keeps its record and the run's record stays unfinished on purpose — it is the evidence"
}

# ---------------------------------------------------------------------- 124d
step_spend() {
  runs_json "$SCRATCH/runs.json" "$SCRATCH/err" || true
  local rows total
  rows=$(python3 - "$SCRATCH/runs.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = {}
for r in d.get("runs") or []:
    if str(r.get("title", "")).startswith("check 124"):
        c = r.get("cost") or {}
        print("%s|%s|%s|%s|%s|%s" % (r.get("run_id"), r.get("title"), r.get("status"),
                                     c.get("total_usd"), c.get("compute_usd"), c.get("compute_estimated")))
PY
)
  if [ -z "$rows" ]; then
    fact spend FAIL "fugaro runs ls --json lists no run of this check's (no row's title begins 'check 124'), so the check's own spend has nothing to read: run --part b and --part c first, or read the rows by hand and record the dollars"
    return 0
  fi
  total=$(python3 - "$SCRATCH/runs.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print("%.4f" % sum(float(((r.get("cost") or {}).get("total_usd") or 0)) for r in (d.get("runs") or []) if str(r.get("title", "")).startswith("check 124")))
PY
)
  run_capture "$SCRATCH/budget.txt" "$SCRATCH/budget.err" fugaro budget show --project "$INSTALL"
  local budget running
  budget=$(cat "$SCRATCH/budget.txt" "$SCRATCH/budget.err")
  running=$(printf '%s\n' "$budget" | grep -cE '^[[:space:]]+[a-z0-9-]+/[0-9]{8}-' || true)
  local flight
  flight=$(printf '%s\n' "$budget" | grep -E 'running:' || true)
  if [ "$RUN_STATUS" != 0 ]; then
    fact spend FAIL "fugaro budget show refused: $(printf '%s' "$budget" | oneline)"
    return 0
  fi
  fact spend PASS "the check's own spend, one row per run and one total: $(printf '%s' "$rows" | tr '\n' ' ' | oneline) — ${total} USD for the whole check (run_id|title|status|total_usd|compute_usd|compute_estimated). The budget node reads '$(printf '%s' "$flight" | oneline)' with ${running} run(s) in flight: the register's expected reading once both runs have ended is NO run in flight and NO reserve left for either of them, and a reserve that outlives a run is the finding — its fix is the reaper's queue deadline or the budget node's own sweep. The whole budget output: $(printf '%s' "$budget" | oneline)"
}

step_diff() {
  local missing=""
  for f in jobs-before.txt instances-before.txt templates-before.txt; do
    [ -f "$W/$f" ] || missing="${missing}${W}/${f} "
  done
  if [ -n "$missing" ]; then
    fact diff FAIL "124a's before-state is not there (missing: ${missing}), so this part's whole reading — that the check left nothing behind — cannot be made: run --part a first (it writes the three files into ${W}, which is why that directory is kept between parts)"
    return 0
  fi
  run_capture "$SCRATCH/jobs-now.txt" "$SCRATCH/err" gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter="labels.fugaro=managed" --format='value(name,status.state)'
  [ "$RUN_STATUS" = 0 ] || {
    fact diff FAIL "the jobs could not be listed now: $(oneline <"$SCRATCH/err")"
    return 0
  }
  sort -o "$SCRATCH/jobs-now.txt" "$SCRATCH/jobs-now.txt"
  run_capture "$SCRATCH/instances-now.txt" "$SCRATCH/err" gcloud compute instances list --project="${P:?}" --filter="labels.fugaro=managed" --format='value(name)'
  sort -o "$SCRATCH/instances-now.txt" "$SCRATCH/instances-now.txt"
  run_capture "$SCRATCH/templates-now.txt" "$SCRATCH/err" gcloud compute instance-templates list --project="${P:?}" --format='value(name)'
  sort -o "$SCRATCH/templates-now.txt" "$SCRATCH/templates-now.txt"
  local djobs dinst dtpl
  djobs=$(diff "$W/jobs-before.txt" "$SCRATCH/jobs-now.txt" || true)
  dinst=$(diff "$W/instances-before.txt" "$SCRATCH/instances-now.txt" || true)
  dtpl=$(diff "$W/templates-before.txt" "$SCRATCH/templates-now.txt" || true)
  local bad=""
  [ -z "$dinst" ] || bad="${bad}the INSTANCES diff is not empty — a VM is there now that was not there before, and Batch deletes a finished job's VM itself (the design's §3.4 and 57a-8's measured teardown), so this is either a run still in flight or a leaked VM: $(printf '%s' "$dinst" | oneline); "
  [ -z "$dtpl" ] || bad="${bad}the TEMPLATES diff is not empty, and neither the installation nor a run creates an instance template: $(printf '%s' "$dtpl" | oneline); "
  # A terminal job keeps its record on purpose, so the jobs' diff is EXPECTED to
  # hold the check's own two jobs — and nothing else, and nothing unfinished.
  local unfinished extra
  unfinished=$(grep -E 'QUEUED|RUNNING|SCHEDULED' "$SCRATCH/jobs-now.txt" || true)
  [ -z "$unfinished" ] || bad="${bad}a job is still QUEUED, SCHEDULED or RUNNING, which a completed check does not leave: $(printf '%s' "$unfinished" | oneline); "
  extra=$(printf '%s\n' "$djobs" | grep -E '^>' | grep -vE 'fugaro-' || true)
  [ -z "$extra" ] || bad="${bad}the jobs diff holds a line that is not a Fugaro job at all: $(printf '%s' "$extra" | oneline); "
  local labels
  labels=$(gcloud batch jobs list --project="${P:?}" --location="${L:?}" --format='value(name,labels.fugaro_run)' 2>/dev/null | grep . || true)
  if [ -n "$bad" ]; then
    fact diff FAIL "$bad the three diffs verbatim: jobs [$(printf '%s' "$djobs" | oneline)], instances [$(printf '%s' "$dinst" | oneline)], templates [$(printf '%s' "$dtpl" | oneline)]. The lists as they are now: jobs [$(oneline <"$SCRATCH/jobs-now.txt")], instances [$(oneline <"$SCRATCH/instances-now.txt")], templates [$(oneline <"$SCRATCH/templates-now.txt")]"
  else
    fact diff PASS "nothing is orphaned: the instances and the templates diff against 124a's before-state are both EMPTY, and the jobs diff holds only the check's own runs' jobs, which stay on purpose because a terminal job's record is history ('$(printf '%s' "$djobs" | oneline)'). Every job's own labels.fugaro_run value, so the VMs above are attributable to the runs that made them rather than to the installation: $(printf '%s' "$labels" | oneline). The before-state's three counts were $(wc -l <"$W/jobs-before.txt" | tr -d ' '), $(wc -l <"$W/instances-before.txt" | tr -d ' '), $(wc -l <"$W/templates-before.txt" | tr -d ' ')"
  fi
}

step_whole() {
  run_capture "$SCRATCH/doctor.out" "$SCRATCH/doctor.err" fugaro doctor --project "$INSTALL"
  local out warnings lines
  out=$(cat "$SCRATCH/doctor.out" "$SCRATCH/doctor.err")
  warnings=$(printf '%s\n' "$out" | grep -E '^warning batch-' || true)
  lines=$(printf '%s\n' "$out" | grep -E '^[a-z]+ batch-' || true)
  run_capture "$SCRATCH/images.txt" "$SCRATCH/images.err" gcloud compute images list --project="${P:?}" --filter='name~^fugaro-preload-' --format='value(name)'
  local images
  images=$(grep . "$SCRATCH/images.txt" || true)
  local bad=""
  [ -z "$warnings" ] || bad="${bad}a new 'warning batch-' line is there, which is a finding this check made: $(printf '%s' "$warnings" | oneline); "
  [ -n "$lines" ] || bad="${bad}doctor printed no batch- line at all, where 124a read the design's own info line; "
  [ -n "$images" ] || bad="${bad}no image in ${P} matches fugaro-preload-, so the preload half of the installation is not intact; "
  if [ -n "$bad" ]; then
    fact whole FAIL "${bad}the read-back verbatim: batch- lines [$(printf '%s' "$lines" | oneline)], doctor's summary [$(printf '%s\n' "$out" | grep -m1 '^fugaro doctor:' | oneline)], the preload images [$(printf '%s' "$images" | oneline)]"
  else
    fact whole PASS "the installation is whole after the whole check: the same batch- lines 124a-5 read ('$(printf '%s' "$lines" | oneline)'), doctor's summary '$(printf '%s\n' "$out" | grep -m1 '^fugaro doctor:' | oneline)', and the preload family still there — $(printf '%s' "$images" | oneline). NOTHING was torn down anywhere in this check: the two APIs are still on, the run accounts' IAM is untouched, the reaper's and the preload's accounts and their two custom roles stand, the Cloud NAT and its router stand, both scheduler jobs stand, the reaper job stands, and a teardown of the compute root is NOT this check's restore step — it would take the installation's backend apart, and the two custom roles would keep counting against the project's custom-role limit for seven days after it"
  fi
}

part_d() {
  BLOCK_TITLE="CHECK 124D RESULT"
  BLOCK_KEYS=(owner installation checkout belong workdir xtrace spend diff whole restore)
  if [ -n "$DRY_RUN" ]; then
    print_plan_d
    mark_not_taken spend diff whole
  else
    step_spend
    step_diff
    step_whole
  fi
  step_manual "paste this block into the register's Check 124d section: each run's cost out of its row and the check's total, the budget node's reading and whether any reserve outlived a run, the three diffs whole against 124a's before-state, every job's own fugaro_run label value, and the read-back that the installation is whole — plus a sentence saying that nothing was torn down anywhere, because the absence of a command is not visible in an output"
}

print_plan_d() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. --part d would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the sandbox checkout's origin, the work directory, and the project refusal"
  say "  [cloud]  fugaro doctor --project ${INSTALL} --all — the installation's own project: line"
  say "  [cloud]  fugaro runs ls --project ${INSTALL} --json — each of the check's runs with its cost, and the total"
  say "  [cloud]  fugaro budget show --project ${INSTALL} — the budget node: whether any run is in flight and whether a reserve outlived one"
  say "  [cloud]  gcloud batch jobs list / compute instances list / compute instance-templates list --project=${P} — diffed against 124a's three before-state files in ${W:-<the work directory>}"
  say "  [cloud]  gcloud batch jobs list --format='value(name,labels.fugaro_run)' — every job's own run label"
  say "  [cloud]  gcloud compute images list --project=${P} --filter='name~^fugaro-preload-' — the preload family, the read that says the installation is intact"
  say "  [local]  the read-back that NOTHING was torn down: the two APIs, the run accounts' IAM, both service accounts and their two custom roles, the Cloud NAT and its router, both scheduler jobs and the reaper job all stay"
  say "and its Restore removes the scratch directory ${SCRATCH} and nothing else: the check's own litter was 124b's declined pull request and its deleted branch, there is no VM to delete (Batch deleted it) and no job to delete (a terminal job's record is history)"
}

# ------------------------------------------------------------------- reporting
BLOCK_KEYS=()
verdict_of() {
  if printf '%s\n' "$1" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    printf 'FAIL\n'
  else
    printf 'PASS\n'
  fi
}

mark_not_taken() { # KEY... — a dry run's live rows, as readings
  local key
  for key in "$@"; do
    fact "$key" INFO "not taken: this is a dry run, so this step was printed and not run, and no cloud project, no credential and no resource was touched"
  done
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\n%s\n' "$BLOCK_TITLE" >&3
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
  if [ -n "$MANUAL_LINE" ]; then
    printf 'MANUAL: %s\n' "$MANUAL_LINE" >&3
  else
    printf 'MANUAL: not reached — this run stopped before the check’s paste-back line was printed\n' >&3
  fi
  printf '%s: %s\n' "$BLOCK_TITLE" "$(verdict_of "$facts")" >&3
}

step_manual() {
  MANUAL_LINE=$1
}

interrupted() {
  owner "interrupted"
  exit 130
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  interrupted
}

# ----------------------------------------------------------------------- main
USAGE="check124.sh --part a|b|c|d [--dry-run]"

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --part)
      [ $# -ge 2 ] || die "--part needs a value. usage: $USAGE"
      PART=$2
      shift
      ;;
    --part=*) PART=${1#--part=} ;;
    --dry-run) DRY_RUN=1 ;;
    -h | --help) HELP=1 ;;
    *) die "unknown argument '$1'. usage: $USAGE" ;;
    esac
    shift
  done
  # --help is answered before the part is required: an owner who does not yet
  # know which part they want is the reader the usage is for.
  [ -n "$HELP" ] && return 0
  case "$PART" in
  a | b | c | d) : ;;
  "") die "--part is required: this check is four parts and each is its own sitting. usage: $USAGE" ;;
  *) die "--part takes a, b, c or d and not '$PART'. usage: $USAGE" ;;
  esac
}
usage() {
  printf 'usage: %s\n' "$USAGE"
  printf '  --part a   the plan-only form, the typed apply, and the two read-backs (124a)\n'
  printf '  --part b   one ordinary sandbox task on Batch to its draft pull request, then the decline (124b)\n'
  printf '  --part c   a forced preemption, the exit code the tree carries, and the reaper (124c)\n'
  printf '  --part d   the check’s spend, the budget lease, and the cleanup diffed against 124a (124d)\n'
  printf '  --dry-run  print every command a part would run and touch no cloud project\n'
}

main() {
  parse_args "$@"
  if [ -n "$HELP" ]; then
    usage
    return 0
  fi
  need_tools
  scratch_up
  guard_xtrace
  guard_owner
  guard_checkout
  guard_workdir
  guard_belong
  guard_installation
  case "$PART" in
  a) part_a ;;
  b) part_b ;;
  c) part_c ;;
  d) part_d ;;
  esac
  REACHED_END=yes
  if printf '%s\n' "$(cat "$FACTS_FILE")" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    exit 1
  fi
  return 0
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
