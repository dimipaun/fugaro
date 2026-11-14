#!/usr/bin/env bash
# Check 57b — the Batch reaper's role, its invocation, and the job it sweeps,
# as ONE script YOU run.
#
# It records the two steps the compute-backend plan adds to Check 57b and no
# hermetic test in this tree can take:
#
#   57b-10, the reaper's role, measured. The custom role `fugaroReaper` holds
#          exactly the five permissions the compute module grants it and no
#          sixth -- `batch.jobs.list`, `get`, `delete` and
#          `compute.instances.list`, `delete` -- which is the whole of what a
#          job stuck in `QUEUED` past the queue deadline is cleared with:
#          `jobs.delete`, not a cancel (`batch.jobs.cancel` is not a permission
#          Google has, which is why the role holds five and not six).
#
#   57b-11, the invocation, measured. The Scheduler job fires the Cloud Run job,
#          the Cloud Run job runs as the reaper account, the report it prints
#          reaches Cloud Logging under that job's own labels, and a job the
#          check deliberately strands is swept by it.
#
# **What one sitting can and cannot show, stated up front.** A pass deletes
# nothing younger than its own deadlines, and on this tree the queue deadline is
# `max(the installation's longest task timeout, backend.MaxRunTime) + slack` --
# at least 24 hours, whatever an installation configures (`batch.task_timeout`
# is refused above that ceiling for exactly this reason). So the deletion of a
# freshly stranded job is not observable in the sitting that creates it. This
# script is idempotent over that fact rather than pretending otherwise: it
# strands one job under its own labels, judges the pass on that job (the
# deadlines it printed, that it left a job inside them alone), and -- on a
# LATER run of this same script -- it reads the sweep out of the reaper's own
# log lines (`deleted job <name> (queued for N minutes and never left QUEUED,
# past the queue deadline of M)`) and records it. A job of this check's that is
# still here after its own deadline, with no such line, is the FAIL.
#
# Usage:      bash scripts/live-checks/check57b.sh [--dry-run]
#             Run it from the SANDBOX checkout (the repository the sandbox
#             installation runs tasks against), because `fugaro doctor` and
#             `fugaro reaper` resolve the installation from that checkout's
#             project and refuse a checkout that belongs to another one. The
#             script's own tree is the checkout the script lives in, read for
#             the role's permission list and the Scheduler region -- never a
#             second copy of either.
#             --dry-run runs the local guards and the two tree reads, prints
#             every command a real run would run in order, and touches NO cloud
#             project: no gcloud, no fugaro, no job, nothing.
# Needs:      bash, mktemp, git, python3 and gcloud on PATH, plus the installed
#             `fugaro` CLI. Nothing is installed and nothing is fetched.
# Where:      GCP project `fugaro-dev` (hard-coded, never read from gcloud and
#             never `belong`, EdgeWeb or EdgeServer) and region `us-east5`, the
#             installation `fugaro`. The one resource it creates is the
#             stranded job, in that project and region, carrying this check's
#             own `fugaro_chk=57b` label.
# Costs:      one Scheduler run, one Cloud Run job execution, a handful of
#             `gcloud` reads, and one Batch job that never leaves `QUEUED` --
#             which holds no VM and therefore bills nothing. No model call, no
#             build, no run of the backend.
# Runs:       by YOU. No agent runs any step of it. The `fugaro setup` an
#             unprovisioned installation needs is PRINTED, never run: its
#             confirmation is typed and a coding agent's session is refused it.
# Cleanup:    the scratch directory always goes, including on a Ctrl-C. The
#             stranded job is left in place ON PURPOSE -- it is the subject of
#             the sweep this check measures, and removing it would end the
#             measurement -- and the `restore` row names it and the one command
#             that removes it by hand. It holds no VM, so it costs nothing while
#             it waits.
# Output:     progress on stderr, ONE block on stdout headed `CHECK 57B RESULT`,
#             whose last line is `CHECK 57B RESULT: PASS|FAIL`. Exit 0 when
#             every row this script measures passed, 1 when one FAILed, 2 when
#             the run could not tell (a guard stopped it, a tool is missing, a
#             read did not answer). An interrupt exits 130.
#
# **The whole block's rows are facts to paste into the register**, and nothing
# here is written from memory: the five permissions are read out of
# `deploy/terraform/gcp/modules/compute/iam.tf`, the Scheduler region out of
# `internal/backend/gcp/names.go`, the deadlines out of the report the pass
# printed, and the sweep out of that pass's own log lines.
#
# There is no `--project` flag and no project variable: the project is
# hard-coded, every `gcloud` command carries `--project=${P:?}`, and every
# `fugaro` command carries `--project fugaro --gcp-project ${P:?}`. The
# script's own test (scripts/live-checks/check57b_test.sh) drives it entirely
# through fakes on PATH, with no network and no cloud project at all.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace would print
# the commands into the log an owner pastes back, and this script's commands
# carry the project and the job names. Check 130 spells the three parts out;
# this is the same guard.
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

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the reaper" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# oneline collapses a command's output into one line, because a fact is one
# line of the block and a multi-line detail would read as facts that are not
# there.
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 57b needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 57b needs bash"
  need mktemp
  need git "the sandbox checkout is the guard that says which installation and which repository this run is about"
  need python3 "the API answers are parsed by one python3 program each, which prints the fields the block carries rather than the document"
  need gcloud "the role, the reaper job, the Scheduler job, the execution and the report are all read with it"
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk57b-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""
STRANDED_NAME=""
STRANDED_CREATED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk57b.XXXXXXXX") || die "Check 57b cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 57b stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
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
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the reaper"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS note
  if [ -z "$SCRATCH" ]; then
    note="nothing was created: the run stopped before it made a scratch directory, so nothing anywhere was enabled, disabled, granted or revoked"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    note="the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"), and nothing was enabled, disabled, granted or revoked anywhere"
    if [ -n "$STRANDED_NAME" ]; then
      note="${note}. The stranded job ${STRANDED_NAME} is LEFT IN PLACE on purpose: it is the subject of the sweep this check measures (the reaper is what removes it, once its queue deadline passes), it holds no VM and bills nothing, and ${STRANDED_CREATED} of it — remove it by hand with: gcloud batch jobs delete ${STRANDED_NAME} --project=${P:-fugaro-dev} --location=${L:-us-east5} (that ends this check's sweep measurement)"
    fi
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
# The project and the region are literals: an installation whose jobs are
# destroyed by a reaper pointed at the wrong project is the failure the
# reaper's own out-of-bounds floor exists for, and this check does not add a
# second way to point one. Nothing here reads gcloud's ambient configuration.
P=fugaro-dev
L=us-east5
INSTALL=fugaro
REAPER_ROLE=fugaroReaper
REAPER_JOB=fugaroreap
REAPER_SCHED=fugaro-reaper-sweep
CHK_LABEL=fugaro_chk
CHK_VALUE=57b

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check57b.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_owner: the register's owner-run marker. It is not a security boundary —
# an agent's shell can export anything — but it is the one line that stops a
# session from running a check whose whole premise is that a person reads what
# it printed, and the register's other owner-run checks carry the same one.
guard_owner() {
  if [ "${CHK57B_OWNER_RUN:-}" != yes ]; then
    fact owner FAIL "this is an owner-run check and CHK57B_OWNER_RUN is not exported as yes; no step of Check 57b is performed by an agent"
    stop "Check 57b is owner-run. Run it at your own terminal, after: export CHK57B_OWNER_RUN=yes — no agent performs any step of it (the register's Check 57 row says so)"
  fi
  fact owner PASS "CHK57B_OWNER_RUN=yes: this run was started by you at your own terminal, and no agent performs any step of Check 57b"
}

# guard_tree: the checkout this script lives in, which is where the two facts
# the live reads are judged against are read from rather than remembered.
guard_tree() {
  TREE=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd) \
    || stop "Check 57b stopped: the script's own path ($0) does not lead to a checkout, so the role's permission list and the Scheduler region cannot be read out of the tree"
  local iam="$TREE/deploy/terraform/gcp/modules/compute/iam.tf"
  local names="$TREE/internal/backend/gcp/names.go"
  [ -f "$iam" ] && [ -f "$names" ] \
    || stop "Check 57b stopped: $TREE does not carry deploy/terraform/gcp/modules/compute/iam.tf and internal/backend/gcp/names.go, so this is not the Fugaro tree the script belongs to"
  # The expected permissions, out of the resource block that grants them: the
  # module is their authority, and a list kept in this script would be a second
  # home for a security-sensitive fact.
  EXPECTED_PERMS=$(awk '/resource "google_project_iam_custom_role" "reaper"/,/^}/' "$iam" \
    | awk '/permissions = \[/,/\]/' | grep -oE '"[a-z.]+"' | tr -d '"' | sort)
  [ -n "$EXPECTED_PERMS" ] \
    || stop "Check 57b stopped: the reaper role's permission list could not be read out of $iam (the module's block has moved)"
  # The Scheduler location for this check's region, read out of the map rather
  # than typed: us-east5 is a region Cloud Scheduler does not serve, and the
  # job lives in the nearest one it does.
  SREG=$(sed -n "s/^[[:space:]]*\"$L\":[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$names" | head -n 1)
  if [ -z "$SREG" ]; then
    if grep -qE "\"$L\"" <<<"$(sed -n '/^var SchedulerRegions = \[\]string{/,/^}/p' "$names")"; then
      SREG=$L
    else
      stop "Check 57b stopped: region $L has no Cloud Scheduler location this tree knows of (neither the served list nor the nearest-region map in $names names it), so the Scheduler job's location cannot be read"
    fi
  fi
  local sha
  sha=$(git -C "$TREE" rev-parse HEAD 2>/dev/null || echo unknown)
  TREE_SHA=$sha
  fact tree PASS "the role's five permissions and the Scheduler location ${SREG} were read out of the tree at ${TREE} (commit ${sha}), never from a copy in this script: $(printf '%s ' $EXPECTED_PERMS | oneline)"
}

# guard_checkout: the repository this run is about. `fugaro doctor` and
# `fugaro reaper` resolve the installation from the checkout's project and
# refuse a checkout that belongs to another one, so the sandbox checkout is the
# only place a run of this check means what it says.
guard_checkout() {
  CHECKOUT=$(git rev-parse --show-toplevel 2>/dev/null) \
    || stop "Check 57b stopped: this is not a git checkout (git rev-parse --show-toplevel failed). Run it from the sandbox checkout: cd <sandbox checkout> && bash <fugaro checkout>/scripts/live-checks/check57b.sh"
  ORIGIN=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) \
    || stop "Check 57b stopped: this checkout has no origin remote, so which repository this run is about is unknown"
  ORIGIN=$(printf '%s' "$ORIGIN" | sed -E 's#//[^@/]*@#//#')
  case "$ORIGIN" in
  *edgeappinc/fugarosandbox | *edgeappinc/fugarosandbox.git) : ;;
  *) stop "Check 57b stopped: this checkout's origin ('${ORIGIN}') is not the sandbox (github.com/edgeappinc/fugarosandbox), which is the only repository Check 57b runs against" ;;
  esac
  fact checkout PASS "run from the sandbox checkout ${CHECKOUT} (origin ${ORIGIN}): the installation every fugaro command resolves is the sandbox's own, and no command in this check names another repository"
}

# guard_belong: the owner's own installation and its repositories, refused by
# name. This check has no argument that could name a project, so the guard is
# about what the environment could silently supply instead: gcloud's ambient
# project is the owner's, and a project named `belong` is the one installation
# no check here touches.
guard_belong() {
  local ambient=${CLOUDSDK_CORE_PROJECT:-}${GOOGLE_CLOUD_PROJECT:-}${GCLOUD_PROJECT:-}
  low=$(printf '%s' "$ambient" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "the ambient gcloud configuration names a project matching '${ambient}', and the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch"
    stop "Check 57b never runs against 'belong', EdgeWeb or EdgeServer. Unset CLOUDSDK_CORE_PROJECT/GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and run it again: every command here names --project=${P} itself"
    ;;
  esac
  fact belong PASS "the project this check names is ${P}, hard-coded and never read from gcloud; neither 'belong' nor EdgeWeb nor EdgeServer is named by any step, and this check has no flag or variable that could name another project"
}

# guard_plan: --dry-run touches nothing, and the live reads are the ones that
# need gcloud. This is where the two are told apart, before a single command
# leaves the machine.
guard_tools_for_real() {
  need gcloud "this is a real run: the role, the job, the Scheduler job and the report are read with it"
}

# ----------------------------------------------------------------- the steps
# role: the live custom role, against the module's own list. The comparison is
# set equality and not containment: a sixth permission on a sweeper that
# deletes machines is the finding, and a missing `delete` is the other one.
step_role() {
  gcloud iam roles describe "$REAPER_ROLE" --project="${P:?}" --format=json >"$SCRATCH/role.json" 2>"$SCRATCH/err" \
    || {
      local msg
      msg=$(oneline <"$SCRATCH/err")
      fact role FAIL "the custom role ${REAPER_ROLE} could not be read in ${P} (${msg}). If it is not there, the installation's Batch provisioning has not been applied — that is YOUR step and this check will not run it: fugaro setup --provision-batch --project ${INSTALL} prints the plan and asks you to type the installation's name"
      return 0
    }
  python3 - "$SCRATCH/role.json" <<'PY' >"$SCRATCH/role.fields"
import json, sys
d = json.load(open(sys.argv[1]))
perms = sorted(d.get("permissions") or [])
print("name|" + str(d.get("name") or ""))
print("title|" + str(d.get("title") or ""))
print("perms|" + " ".join(perms))
PY
  ROLE_NAME=$(sed -n 's/^name|//p' "$SCRATCH/role.fields")
  ROLE_PERMS=$(sed -n 's/^perms|//p' "$SCRATCH/role.fields")
  [ -n "$ROLE_PERMS" ] || {
    fact role FAIL "the custom role ${REAPER_ROLE} exists in ${P} but answers with no permissions at all, so the sweeper can read and delete nothing"
    return 0
  }
  WANT=$(printf '%s\n' "$EXPECTED_PERMS" | oneline)
  HAVE=$(printf '%s\n' "$ROLE_PERMS" | tr ' ' '\n' | sort | oneline)
  if [ "$HAVE" = "$WANT" ]; then
    fact role PASS "${ROLE_NAME} holds exactly the five permissions the compute module grants it (${HAVE}), and no sixth: batch.jobs.list/get/delete and compute.instances.list/delete. A job stuck in QUEUED past the queue deadline is cleared with jobs.delete — the role holds no cancel, and batch.jobs.cancel is not a permission Google has"
  else
    fact role FAIL "${ROLE_NAME} holds '${HAVE}' where ${P}'s module grants exactly '${WANT}': a permission the module does not grant is a widening on a role that deletes machines, and one below the list is a sweeper that cannot do its job. The module (deploy/terraform/gcp/modules/compute/iam.tf) is the authority — reconcile one against the other and record which"
  fi
}

# reaper-job: the Cloud Run job, which is the process the role exists for. Its
# account, its argument and its three environment variables are read rather
# than assumed: a job that runs as anything but the reaper account holds the
# five permissions' worth of reach under another identity, and one without the
# marker variable is not the reaper's own invocation at all.
step_reaper_job() {
  gcloud run jobs describe "$REAPER_JOB" --project="${P:?}" --region="${L:?}" --format=json >"$SCRATCH/job.json" 2>"$SCRATCH/err" \
    || {
      local msg
      msg=$(oneline <"$SCRATCH/err")
      fact reaper-job FAIL "the Cloud Run job ${REAPER_JOB} could not be read in ${P}/${L} (${msg}). If it is not there, the installation's Batch provisioning has not been applied — that is YOUR step and this check will not run it: fugaro setup --provision-batch --project ${INSTALL} prints the plan and asks you to type the installation's name"
      return 0
    }
  python3 - "$SCRATCH/job.json" <<'PY' >"$SCRATCH/job.fields"
import json, sys
d = json.load(open(sys.argv[1]))
spec = d.get("spec", {}).get("template", {}).get("spec", {}).get("template", {}).get("spec", {})
containers = spec.get("containers") or [{}]
c = containers[0]
env = {e.get("name"): e.get("value") for e in (c.get("env") or [])}
labels = d.get("labels") or {}
print("account|" + str(spec.get("serviceAccountName") or ""))
print("args|" + " ".join(c.get("args") or []))
print("env|" + " ".join("%s=%s" % (k, env[k]) for k in sorted(env)))
print("labels|" + " ".join("%s=%s" % (k, labels[k]) for k in sorted(labels)))
PY
  JOB_ACCOUNT=$(sed -n 's/^account|//p' "$SCRATCH/job.fields")
  JOB_ARGS=$(sed -n 's/^args|//p' "$SCRATCH/job.fields")
  JOB_ENV=$(sed -n 's/^env|//p' "$SCRATCH/job.fields")
  JOB_LABELS=$(sed -n 's/^labels|//p' "$SCRATCH/job.fields")
  WANT_ACCOUNT="fugaro-reaper@${P}.iam.gserviceaccount.com"
  local bad=""
  [ "$JOB_ACCOUNT" = "$WANT_ACCOUNT" ] || bad="${bad}it runs as '${JOB_ACCOUNT}' and not as ${WANT_ACCOUNT} (the reaper account, the only identity the five permissions are granted to); "
  case " $JOB_ARGS " in *" reaper "*) : ;; *) bad="${bad}its container's args ('${JOB_ARGS}') do not carry the 'reaper' argument, so it is not the sweeper's own invocation; " ;; esac
  case " $JOB_ENV " in *" FUGARO_REAPER_JOB=1 "*) : ;; *) bad="${bad}its environment ('${JOB_ENV}') does not carry FUGARO_REAPER_JOB=1, which is the marker that says this pass is the reaper's own job and not a dry run; " ;; esac
  case " $JOB_ENV " in *" FUGARO_GCP_PROJECT=${P} "*) : ;; *) bad="${bad}its environment ('${JOB_ENV}') does not carry FUGARO_GCP_PROJECT=${P}; " ;; esac
  case " $JOB_ENV " in *" FUGARO_REGION=${L} "*) : ;; *) bad="${bad}its environment ('${JOB_ENV}') does not carry FUGARO_REGION=${L}; " ;; esac
  if [ -n "$bad" ]; then
    fact reaper-job FAIL "the Cloud Run job ${REAPER_JOB} is there but ${bad}the fields verbatim: account '${JOB_ACCOUNT}', args '${JOB_ARGS}', env '${JOB_ENV}', labels '${JOB_LABELS}'"
  else
    fact reaper-job PASS "the Cloud Run job ${REAPER_JOB} runs as ${JOB_ACCOUNT} with args '${JOB_ARGS}' and env '${JOB_ENV}' (labels '${JOB_LABELS}'): the account is the reaper's own, the argument is the sweeper's, and the marker variable says the pass this job runs is a real one"
  fi
}

# scheduler: the invocation itself. The location is the tree's answer for this
# region, the cadence and the target are the module's, and the token's account
# is what makes the Cloud Run job's own IAM the only thing between the schedule
# and a deletion.
step_scheduler() {
  gcloud scheduler jobs describe "$REAPER_SCHED" --project="${P:?}" --location="${SREG:?}" --format=json >"$SCRATCH/sched.json" 2>"$SCRATCH/err" \
    || {
      local msg
      msg=$(oneline <"$SCRATCH/err")
      fact scheduler FAIL "the Scheduler job ${REAPER_SCHED} could not be read in ${P}/${SREG} (${msg}). If it is not there, the installation's Batch provisioning has not been applied — that is YOUR step and this check will not run it: fugaro setup --provision-batch --project ${INSTALL} prints the plan and asks you to type the installation's name"
      return 0
    }
  python3 - "$SCRATCH/sched.json" <<'PY' >"$SCRATCH/sched.fields"
import json, sys
d = json.load(open(sys.argv[1]))
http = d.get("httpTarget") or {}
tok = http.get("oauthToken") or http.get("oidcToken") or {}
print("schedule|" + str(d.get("schedule") or ""))
print("state|" + str(d.get("state") or ""))
print("uri|" + str(http.get("uri") or ""))
print("account|" + str(tok.get("serviceAccountEmail") or ""))
print("retries|" + str(d.get("retryConfig", {}).get("retryCount", "")))
PY
  SCHED_CRON=$(sed -n 's/^schedule|//p' "$SCRATCH/sched.fields")
  SCHED_STATE=$(sed -n 's/^state|//p' "$SCRATCH/sched.fields")
  SCHED_URI=$(sed -n 's/^uri|//p' "$SCRATCH/sched.fields")
  SCHED_ACCOUNT=$(sed -n 's/^account|//p' "$SCRATCH/sched.fields")
  local bad=""
  [ "$SCHED_CRON" = "*/15 * * * *" ] || bad="${bad}its cadence is '${SCHED_CRON}' and not '*/15 * * * *' (the module's, and the interval the reaper's deadlines are read against); "
  case "$SCHED_URI" in
  */jobs/${REAPER_JOB}:run*) : ;;
  *) bad="${bad}its target URI ('${SCHED_URI}') does not name ${REAPER_JOB}'s run endpoint; " ;;
  esac
  [ "$SCHED_ACCOUNT" = "fugaro-reaper@${P}.iam.gserviceaccount.com" ] \
    || bad="${bad}the token it fires with belongs to '${SCHED_ACCOUNT}' and not to the reaper account, so the Cloud Run job is invoked by an identity other than the one its IAM was written for; "
  if [ -n "$bad" ]; then
    fact scheduler FAIL "the Scheduler job ${REAPER_SCHED} is there in ${SREG} but ${bad}the fields verbatim: schedule '${SCHED_CRON}', state '${SCHED_STATE}', URI '${SCHED_URI}', token account '${SCHED_ACCOUNT}'"
  else
    fact scheduler PASS "the Scheduler job ${REAPER_SCHED} lives in ${SREG} (the location this tree maps ${L} to, since Cloud Scheduler does not serve ${L}), fires on '${SCHED_CRON}', targets ${REAPER_JOB}'s own run endpoint, and fires as ${SCHED_ACCOUNT} — the reaper account. State: ${SCHED_STATE}"
  fi
}

# invocation: fire the schedule by hand and read the execution it made. The
# execution is what says the schedule really reaches the Cloud Run job; its
# completion is what says the pass really ran.
step_invocation() {
  local before after name state waited=0
  before=$(execution_names)
  gcloud scheduler jobs run "$REAPER_SCHED" --project="${P:?}" --location="${SREG:?}" >"$SCRATCH/run.out" 2>"$SCRATCH/err" \
    || {
      fact invocation FAIL "the Scheduler job ${REAPER_SCHED} could not be fired by hand (gcloud scheduler jobs run said: $(oneline <"$SCRATCH/err")). Nothing was swept by this run, and the report below is whatever an earlier pass left"
      return 0
    }
  while [ "$waited" -lt "${CHK57B_WAIT_SECONDS:-180}" ]; do
    after=$(execution_names)
    name=$(printf '%s\n' "$after" | grep -vxF "$before" | head -n 1 || true)
    if [ -n "$name" ]; then
      state=$(execution_state "$name")
      case "$state" in
      completed=True* | *failed=[1-9]*)
        fact invocation PASS "firing the schedule by hand (gcloud scheduler jobs run ${REAPER_SCHED} --location ${SREG}) made the Cloud Run execution ${name}, which ended after ${waited}s with ${state}: the schedule reaches the Cloud Run job and the job runs through it. gcloud said: $(oneline <"$SCRATCH/run.out")"
        EXECUTION=$name
        return 0
        ;;
      esac
    fi
    sleep 5
    waited=$((waited + 5))
  done
  fact invocation FAIL "the schedule was fired by hand but no execution of ${REAPER_JOB} that ends appeared within ${CHK57B_WAIT_SECONDS:-180}s (the executions listed, newest first: $(printf '%s\n' "$after" | oneline); the newest one reads '${state:-no new execution at all}'). The invocation is not reaching the Cloud Run job, or the pass is still running and the wait was too short — record which, and re-run with CHK57B_WAIT_SECONDS raised if it is simply slow. A report read now would be an earlier pass's, so nothing below is concluded from one"
}

# execution_names: every execution of the reaper job, newest first.
execution_names() {
  gcloud run jobs executions list --job="${REAPER_JOB}" --project="${P:?}" --region="${L:?}" --limit=5 --format=json 2>/dev/null \
    | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    d = []
for e in d if isinstance(d, list) else []:
    print((e.get("metadata") or {}).get("name", ""))
' || true
}

# execution_state NAME: the execution's own reading of whether it ended, in one
# line. A job execution reports terminality as the `Completed` condition, not as
# a word like SUCCEEDED, so the counts are printed beside it rather than
# guessed at.
execution_state() {
  gcloud run jobs executions describe "$1" --project="${P:?}" --region="${L:?}" --format=json 2>/dev/null \
    | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
st = d.get("status") or {}
conds = {c.get("type"): c.get("status") for c in (st.get("conditions") or [])}
print("completed=%s succeeded=%s failed=%s" % (conds.get("Completed", ""), st.get("succeededCount", ""), st.get("failedCount", "")))' 2>/dev/null | oneline || true
}

# report: the pass's own output, out of Cloud Logging, forwarded by the labels
# the Cloud Run job carries. This is the one line 57b-11 asks for -- the report
# arriving -- and the deadlines in it are what every judgement below is made
# against, so they are read out of it rather than computed here.
step_report() {
  LOGFILTER=${CHK57B_LOG_FILTER:-resource.type="cloud_run_job" AND resource.labels.job_name="${REAPER_JOB}"}
  local age=${CHK57B_LOG_AGE:-1h}
  gcloud logging read "$LOGFILTER" --project="${P:?}" --freshness="$age" --limit=100 --format=json >"$SCRATCH/log.json" 2>"$SCRATCH/err" \
    || {
      fact report FAIL "the report could not be read out of Cloud Logging (gcloud logging read said: $(oneline <"$SCRATCH/err")). The filter used was: ${LOGFILTER} — if this installation sends the job's logs elsewhere, re-run with CHK57B_LOG_FILTER set to the filter that finds them, and record which"
      return 0
    }
  python3 - "$SCRATCH/log.json" >"$SCRATCH/log.lines" <<'PY'
import json, sys
entries = json.load(open(sys.argv[1]))
out = []
for e in entries if isinstance(entries, list) else []:
    payload = e.get("textPayload")
    if payload is None:
        jp = e.get("jsonPayload") or {}
        payload = jp.get("message") or jp.get("textPayload") or json.dumps(jp)
    for line in str(payload).splitlines():
        line = line.strip()
        if line:
            out.append(line)
# oldest first: the API answers newest first, and the report reads in the order
# the pass printed it.
for line in reversed(out[-200:]):
    print(line)
PY
  local n
  n=$(wc -l <"$SCRATCH/log.lines" | tr -d ' ')
  [ "$n" != 0 ] || {
    fact report FAIL "the read found no entries at all for the Cloud Run job ${REAPER_JOB} in the last ${age} (filter: ${LOGFILTER}). Either the pass has not run yet or this installation's log sink does not carry the job's output — record which; the deadlines below cannot be read from a pass nobody can see"
    return 0
  }
  REPORT_LINE=$(grep -m1 '^reaped projects/' "$SCRATCH/log.lines" || true)
  if [ -z "$REPORT_LINE" ]; then
    fact report FAIL "the read found ${n} line(s) but none is a pass's headline: the report lines are $(tail -n 5 "$SCRATCH/log.lines" | oneline). The headline the reaper prints is 'reaped projects/<project>/locations/<region>: queue deadline <d>, run budget <d>, max age <d>' — a pass that did not print one is a pass that did not sweep"
    return 0
  fi
  QUEUE_DEADLINE=$(sed -n 's/.*queue deadline \([^,]*\),.*/\1/p' <<<"$REPORT_LINE")
  RUN_BUDGET=$(sed -n 's/.*run budget \([^,]*\),.*/\1/p' <<<"$REPORT_LINE")
  MAX_AGE=$(sed -n 's/.*max age \(.*\)$/\1/p' <<<"$REPORT_LINE")
  fact report PASS "the pass's own report reached Cloud Logging under the Cloud Run job's labels (filter: ${LOGFILTER}, ${n} line(s) in the last ${age}), and its headline is: ${REPORT_LINE} — the three deadlines every judgement below is made against are the installation's own, derived from its task timeouts (queue deadline ${QUEUE_DEADLINE}, run budget ${RUN_BUDGET}, max age ${MAX_AGE})"
}

# local-pass: the same sweep from the checkout, dry. It is the second half of
# the read-back: the pass the job runs and the pass an operator runs by hand
# must decide by the same three deadlines, because they are derived from the
# same installation. A local dry run deletes nothing.
step_local_pass() {
  fugaro reaper --dry-run --project "${INSTALL:?}" --gcp-project "${P:?}" >"$SCRATCH/local.out" 2>"$SCRATCH/local.err" \
    || {
      fact local-pass FAIL "fugaro reaper --dry-run refused in this checkout: $(oneline <"$SCRATCH/local.err" | oneline). A refusal here is a fact about the installation — no batch: block in its project config, or the checkout belongs to another project — and it means the pass the Cloud Run job runs cannot be reproduced by hand"
      return 0
    }
  local line
  line=$(grep -m1 'would reap projects/' "$SCRATCH/local.out" || true)
  [ -n "$line" ] || {
    fact local-pass FAIL "fugaro reaper --dry-run printed no headline at all (its output: $(oneline <"$SCRATCH/local.out")). A dry run prints 'would reap projects/<project>/locations/<region>: queue deadline …' before anything else"
    return 0
  }
  local qd
  qd=$(sed -n 's/.*queue deadline \([^,]*\),.*/\1/p' <<<"$line")
  if [ -n "${QUEUE_DEADLINE:-}" ] && [ "$qd" != "$QUEUE_DEADLINE" ]; then
    fact local-pass FAIL "the pass an operator runs by hand decides by a queue deadline of ${qd} while the pass the Cloud Run job ran decided by ${QUEUE_DEADLINE}: the two derive it from different sources (the config's task timeouts and the job environment's ceiling), and they must agree — a config whose longest task timeout is above backend.MaxRunTime is refused by validate for exactly this reason. The local headline: ${line}"
    return 0
  fi
  fact local-pass PASS "fugaro reaper --dry-run prints the same sweep from this checkout: ${line} — and a dry run deletes nothing. Its whole output: $(oneline <"$SCRATCH/local.out")"
}

# stranded: the deliberately stranded job, and what the pass does with it. The
# job is this check's own — labelled with the installation's marker and this
# check's own fugaro_chk=57b beside a well-formed per-run label, so the reaper
# attributes it to a run and judges it like any other job — and it asks for a
# share of a machine no machine type can hold, which is what keeps it in QUEUED
# and is exactly the shape the design's step 9 describes.
step_stranded() {
  batch_jobs_json >"$SCRATCH/jobs.json"
  python3 - "$SCRATCH/jobs.json" >"$SCRATCH/stranded.fields" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = []
for j in d if isinstance(d, list) else []:
    print("job|%s|%s|%s" % (j.get("name", "").split("/")[-1], (j.get("status") or {}).get("state", ""), j.get("createTime", "")))
PY
  local existing
  existing=$(grep -c '^job|' "$SCRATCH/stranded.fields" || true)

  # The sweep, if a pass has already made it: the reaper's own line, out of the
  # log, for a job of this check's. That line is the measurement 57b-10 asks
  # for, and it is only readable after the queue deadline has passed -- which
  # is why this script is worth running again a day after the first run.
  local sweep
  sweep=$(sweep_lines)
  if [ -n "$sweep" ]; then
    fact stranded PASS "a scheduled pass has swept this check's own stranded job, in the reaper's own words: $(printf '%s' "$sweep" | oneline) — that is a job stuck in QUEUED past the queue deadline deleted by jobs.delete, which is the half of 57b-10 a single sitting cannot show"
    if [ "$existing" != 0 ]; then
      fact stranded-note INFO "a job of this check's is still present as well ($(grep '^job|' "$SCRATCH/stranded.fields" | oneline)): it is one this run's own deadline has not reached yet, or the pass has not reached it since"
    fi
    return 0
  fi

  # Without the pass's own deadlines there is nothing to judge a job against: a
  # job that is old says nothing about the reaper if nobody read what the reaper
  # decides by, and the report row above says which reading failed.
  if [ "$existing" != 0 ] && [ -z "${QUEUE_DEADLINE:-}" ]; then
    STRANDED_NAME=$(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f2)
    STRANDED_CREATED=$(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f4)
    fact stranded INFO "this check's own job ${STRANDED_NAME} (state $(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f3), created ${STRANDED_CREATED}) is there, but the pass's own deadlines could not be read (the report row above says why), so whether it is due to be swept is unknown: record the job and the report, and do not read this row as either answer"
    return 0
  fi

  if [ "$existing" = 0 ]; then
    strand_one || return 0
    fact stranded INFO "no stranded job of this check's was there, so this run created one: ${STRANDED_NAME}, QUEUED and never scheduling (it asks for 64000m/1048576MiB of an e2-standard-4, whose whole VM is 4000m/16384MiB). The pass that judges it decides by a queue deadline of ${QUEUE_DEADLINE:-<not read>}, so the deletion this step measures is due at ${STRANDED_NAME}'s creation plus that deadline: run this script again after then and the line above this one will carry the reaper's own 'deleted job …' instead. Nothing is concluded today, and nothing was deleted"
  else
    # A job that is here after its own deadline is the finding: the pass has run
    # (this run fired it) and left it. Anything younger is simply not due yet.
    STRANDED_NAME=$(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f2)
    STRANDED_CREATED=$(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f4)
    local verdict due
    read -r verdict due <<<"$(python3 - "$SCRATCH/stranded.fields" "${QUEUE_DEADLINE:-0s}" <<'PY'
import datetime, json, re, sys

def parse_dur(s):
    m = re.fullmatch(r"(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?", s.strip() or "0s")
    if not m:
        return 0.0
    h, mi, se = (float(x) if x else 0.0 for x in m.groups())
    return h * 3600 + mi * 60 + se

def parse_rfc3339(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))

deadline = parse_dur(sys.argv[2])
now = datetime.datetime.now(datetime.timezone.utc)
for line in open(sys.argv[1]):
    if not line.startswith("job|"):
        continue
    _, name, state, created = line.rstrip("\n").split("|", 3)
    try:
        age = (now - parse_rfc3339(created)).total_seconds()
    except Exception:
        print("unknown -")
        sys.exit(0)
    due = parse_rfc3339(created) + datetime.timedelta(seconds=deadline)
    print(("overdue" if age > deadline else "waiting") + " " + due.strftime("%Y-%m-%dT%H:%M:%SZ"))
    sys.exit(0)
print("unknown -")
PY
)" || true
    case "${verdict:-unknown}" in
    overdue)
      fact stranded FAIL "this check's own job ${STRANDED_NAME} is still QUEUED past the queue deadline the pass decides by, and no pass has printed a line deleting it (the pass ran: this run fired the schedule). The queue deadline is ${QUEUE_DEADLINE} and the job was created ${STRANDED_CREATED}: a job stuck in QUEUED past that deadline is deleted, not cancelled, so this is the reaper not doing what 57b-10 says it does — record it, and do not adjust this check to it"
      ;;
    unknown)
      fact stranded INFO "this check's own job ${STRANDED_NAME} is there but its creation time ('${STRANDED_CREATED}') could not be read, so whether the pass should have swept it is unknown: record the job's own status and the pass's report, and do not read this row as either answer"
      ;;
    *)
      fact stranded INFO "this check's own job ${STRANDED_NAME} (state $(grep '^job|' "$SCRATCH/stranded.fields" | head -n 1 | cut -d'|' -f3), created ${STRANDED_CREATED}) is inside the queue deadline the pass decides by (${QUEUE_DEADLINE}), whose own line shows it left it alone: a job still waiting for capacity within the deadline is Batch's to manage, and the sweep is due at ${due}. Run this script again after that time and its 'stranded' row will carry the reaper's own 'deleted job …' line instead of this one"
      ;;
    esac
  fi
}

# strand_one: submit the job that never leaves QUEUED. The request is the one
# the design's step 9 names as the shape that never schedules, so no image is
# ever pulled and no VM is ever created: it asks for a share no machine type
# can hold. If the API refuses it, the check stops rather than inventing a
# second shape -- the request itself is the thing to record.
strand_one() {
  STRANDED_NAME="fugaro-chk57b-$(date -u +%Y%m%dt%H%M)"
  local label="chk57b-$(date -u +%Y%m%dt%H%M)"
  python3 - "$SCRATCH/stranded.json" "$STRANDED_NAME" "$label" <<'PY'
import json, sys
name, label = sys.argv[2], sys.argv[3]
labels = {"fugaro": "managed", "fugaro_chk": "57b", "fugaro_run": label}
job = {
    "taskGroups": [{
        "taskCount": "1",
        "parallelism": "1",
        "taskSpec": {
            "computeResource": {"cpuMilli": "64000", "memoryMib": "1048576"},
            "runnables": [{"container": {"imageUri": "gcr.io/cloud-batch/noop", "commands": ["/bin/true"]}}],
        },
    }],
    "allocationPolicy": {
        "instances": [{"policy": {"machineType": "e2-standard-4"}}],
        "labels": labels,
    },
    "labels": labels,
}
json.dump(job, open(sys.argv[1], "w"), indent=2)
PY
  gcloud batch jobs submit "$STRANDED_NAME" --project="${P:?}" --location="${L:?}" --config="$SCRATCH/stranded.json" >"$SCRATCH/submit.out" 2>"$SCRATCH/err" \
    || {
      fact stranded FAIL "the deliberately stranded job could not be created: $(oneline <"$SCRATCH/err"). The request asks for 64000m/1048576MiB of an e2-standard-4 (whose whole VM is 4000m/16384MiB), which the design's step 9 names as the shape that stays QUEUED — a refusal of THIS request is a fact about the Batch API to record, and the sweep this step measures is not measured by this run"
      STRANDED_NAME=""
      return 1
    }
  STRANDED_CREATED=$(gcloud batch jobs describe "$STRANDED_NAME" --project="${P:?}" --location="${L:?}" --format='value(createTime)' 2>/dev/null || true)
  return 0
}

# batch_jobs_json: this check's own stranded jobs, by its own label.
batch_jobs_json() {
  gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
    --filter="labels.${CHK_LABEL}=${CHK_VALUE}" --format=json 2>/dev/null || echo '[]'
}

# sweep_lines: the reaper's own deletion lines for this check's jobs, whenever
# they were printed. The window is long on purpose: the sweep happens more than
# a queue deadline after the job was created, which is at least a day.
sweep_lines() {
  gcloud logging read "$LOGFILTER" --project="${P:?}" \
    --freshness="${CHK57B_SWEEP_AGE:-30d}" --limit=200 --format=json 2>/dev/null \
    | python3 -c 'import json,sys
try:
    entries = json.load(sys.stdin)
except Exception:
    entries = []
for e in entries if isinstance(entries, list) else []:
    p = e.get("textPayload")
    if p is None:
        jp = e.get("jsonPayload") or {}
        p = jp.get("message") or ""
    for line in str(p).splitlines():
        line = line.strip()
        if line.startswith("deleted job fugaro-chk57b-") or line.startswith("would delete job fugaro-chk57b-"):
            print(line)
' 2>/dev/null || true
}

# ------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. A real run would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the tree this script lives in, the sandbox checkout's origin, and the project refusal"
  say "  [local]  the tree reads: the reaper role's permissions out of deploy/terraform/gcp/modules/compute/iam.tf, and ${L}'s Scheduler location out of internal/backend/gcp/names.go"
  say "  [cloud]  gcloud iam roles describe ${REAPER_ROLE} --project=${P} — the live role against the module's five permissions"
  say "  [cloud]  gcloud run jobs describe ${REAPER_JOB} --project=${P} --region=${L} — its account, its 'reaper' argument and its three FUGARO_* variables"
  say "  [cloud]  gcloud scheduler jobs describe ${REAPER_SCHED} --project=${P} --location=${SREG:-<the region the tree reads for ${L}>} — the cadence, the run endpoint and the token account"
  say "  [cloud]  gcloud scheduler jobs run ${REAPER_SCHED} --project=${P} --location=${SREG:-<the region the tree reads for ${L}>}, then gcloud run jobs executions list/describe until the execution it made ends — the invocation"
  say "  [cloud]  gcloud logging read (resource.type=cloud_run_job AND resource.labels.job_name=${REAPER_JOB}) — the pass's own report and its three deadlines"
  say "  [local]  fugaro reaper --dry-run --project ${INSTALL} --gcp-project ${P} — the same sweep from this checkout, deciding by the same deadlines (deletes nothing)"
  say "  [cloud]  gcloud batch jobs list --filter=labels.${CHK_LABEL}=${CHK_VALUE} — this check's own stranded job, if an earlier run made one"
  say "  [cloud]  gcloud batch jobs submit <name> (if there is none) — ONE job asking for 64000m/1048576MiB of an e2-standard-4, which never leaves QUEUED, carries fugaro=managed and a well-formed fugaro_run beside this check's fugaro_chk=57b, and holds no VM"
  say "  [cloud]  gcloud logging read (--freshness ${CHK57B_SWEEP_AGE:-30d}) — the reaper's own 'deleted job <a job of this check's> (queued for N minutes …)' line, which is the sweep 57b-10 asks for; it is only printable after the queue deadline, so a first run reads it as INFO and a later run reads it as PASS"
  say "  [yours]  an unprovisioned installation is NOT provisioned by this script: the plan, the typed confirmation and the apply are yours — fugaro setup --provision-batch --project ${INSTALL}"
  say "and its Restore (also from the exit trap) would remove: the scratch directory ${SCRATCH} and nothing else. The stranded job is left in place on purpose; it holds no VM and bills nothing, and the row names the one command that removes it"
}

# ------------------------------------------------------------------- reporting
BLOCK_KEYS=(owner checkout tree belong role reaper-job scheduler invocation report local-pass stranded restore)

verdict_of() {
  if printf '%s\n' "$1" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    printf 'FAIL\n'
  else
    printf 'PASS\n'
  fi
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 57B RESULT\n' >&3
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
  printf 'CHECK 57B RESULT: %s\n' "$(verdict_of "$facts")" >&3
}

step_manual() {
  MANUAL_LINE="paste this block into docs/gcp-live-checklist.md's Check 57 row and its 57b half, with the date and \`fugaro version\`: the role's five permissions verbatim, the reaper job's account and its three FUGARO_* variables, the Scheduler job's location, cadence and token account, the execution's name, the report's headline with its three deadlines, whether the local dry run decided by the same ones, and — a day or more after this run — the reaper's own 'deleted job <name> (queued for N minutes and never left QUEUED, past the queue deadline of M)' line, which is what makes 57b-10 a measurement rather than a reading of the code. Nothing here is inferred"
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
USAGE="check57b.sh [--dry-run]"

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --dry-run) DRY_RUN=1 ;;
    -h | --help) HELP=1 ;;
    *) die "unknown argument '$1'. usage: $USAGE" ;;
    esac
    shift
  done
}

main() {
  need_tools
  parse_args "$@"
  if [ -n "$HELP" ]; then
    printf 'usage: %s\n' "$USAGE"
    return 0
  fi
  scratch_up
  guard_xtrace
  guard_owner
  guard_tree
  guard_checkout
  guard_belong

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "guards and the two tree reads only: the commands above were printed, not run, and no cloud project, no credential and no resource was touched"
    fact role INFO "not taken: this is a dry run, so the live role was not read. A real run asserts it holds exactly the module's five permissions, which the tree row above prints"
    fact reaper-job INFO "not taken: this is a dry run, so the Cloud Run job was not read. A real run asserts it runs as fugaro-reaper@${P}.iam.gserviceaccount.com with the 'reaper' argument and FUGARO_REAPER_JOB=1"
    fact scheduler INFO "not taken: this is a dry run, so the Scheduler job was not read. A real run asserts '*/15 * * * *' in ${SREG}, ${REAPER_JOB}'s run endpoint, and the reaper account's token"
    fact invocation INFO "not taken: this is a dry run, so nothing was fired and no execution was waited for"
    fact report INFO "not taken: this is a dry run, so no report was read out of Cloud Logging and no deadline is known"
    fact local-pass INFO "not taken: this is a dry run, so fugaro reaper --dry-run was not run"
    fact stranded INFO "not taken: this is a dry run, so no stranded job was listed or created, and nothing was swept"
  else
    step_role
    step_reaper_job
    step_scheduler
    step_invocation
    step_report
    step_local_pass
    step_stranded
  fi
  step_manual

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
