#!/usr/bin/env bash
# Check 830 — what links a Batch VM to its job, and what a launcher can put on
# one, as ONE script YOU run. It is BR0 of docs/batch-run-identity: the probe
# the plan blocks BR3 (the VM-to-job link), BR5 and BR12 on (R4, O1).
#
# It measures four things, on a throwaway Batch job in `fugaro-dev`, and prints
# one PASS or FAIL for each:
#
#   U1, the link and its forge. Which Google-made fact ties a Batch VM to its
#          job — the instance's own name, a label or metadata item Batch sets,
#          a field on the job object, or nothing — and whether a second job can
#          put another job's identity into that same field through
#          `AllocationPolicy.labels`, a job's own labels, its task's variables,
#          or an instance's labels and metadata. Two rows: `u1-link`
#          (a Google-made fact exists at all) and `u1-forge` (a second job
#          cannot forge it), plus `u1-handmade` for O8's plain Compute Engine
#          VM, which the same measurement answers and which names the second
#          read D3102 asks about (O1).
#
#   U2, the identity token. Whether a task's container can reach the metadata
#          server, and whether the token in FULL format carries the instance's
#          id, name, project and zone there. `u2-token`.
#
#   U3, where a job's variables reach. Whether the value of a variable the job
#          carries — the wrapped key's own variable, R5, with a stand-in value
#          — appears in Cloud Logging, in the instance's metadata, or in the
#          `CreateJob` audit record. `u3-marker`, fail-closed: it passes only
#          when the value reached the container's own environment (the positive
#          control) AND every one of those readings was taken and saw nothing.
#
#   U8, how soon a deleted job's name can be used again. `u8-name`, with the
#          seconds it took printed.
#
# **What one sitting can and cannot show, stated up front.** This is a
# measurement, not a proof: it reports what a job shaped like a real run does
# on `fugaro-dev` today. The forge attempt is only as strong as the fields it
# tries — a launcher-controlled field this script does not know about would be
# tried by nobody — which is why the attempt writes every candidate key it
# found on the probe's own VM, and reports the ones it could not write rather
# than dropping them. The `CreateJob` audit record is written by Google, so the
# reading waits for it, bounded; a run that cannot read it FAILS rather than
# concluding absence from a record it never saw.
#
# Usage:      bash scripts/live-checks/check-830.sh [--dry-run]
#             From the Fugaro checkout this script lives in: it is the tree the
#             link is judged against (the log filter out of
#             internal/backend/batch/logs.go, the marker's name out of
#             docs/batch-run-identity/). It does not need the sandbox checkout
#             and it touches no repository of yours.
#             --dry-run runs the guards and the tree reads, prints every
#             command a real run would run, and touches NO cloud project.
# Needs:      bash, mktemp, git, python3 and gcloud on PATH. Nothing is
#             installed and nothing is fetched.
# Where:      GCP project `fugaro-dev` (hard-coded, never read from gcloud and
#             never `belong`, EdgeWeb or EdgeServer) and region `us-east5`. The
#             resources it makes are all labelled `fugaro_chk=830`: the probe
#             job, a second job that tries to forge the link, and — for O8 —
#             one plain VM.
# Costs:      two small Batch jobs on e2-standard-4 (2000m/4096MiB, the share
#             of the type the design measured 57a on; the link is not a
#             function of the machine type, which is why this is not the
#             default e2-standard-8), each alive for its linger (300s and 120s
#             by default), one plain VM alive for seconds, one instance
#             and a dozen `gcloud` reads. No model call, no build, no
#             run of the backend, no image built.
# Runs:       by YOU. No agent runs any step of it: the check is the probe the
#             plan blocks other work on, and its whole value is that a person
#             reads what it printed before the link is written.
# Cleanup:    everything it made is deleted — the probe job, the forging job,
#             the re-created job and the plain VM — and
#             the scratch directory always goes, including on a Ctrl-C. The
#             `restore` row names anything an interrupted run left behind, with
#             the one command that removes it.
# Output:     progress on stderr, ONE block on stdout headed `CHECK 830 RESULT`,
#             whose last line is `CHECK 830 RESULT: PASS|FAIL`. Exit 0 when
#             every row passed, 1 when one FAILed, 2 when the run could not
#             tell (a guard stopped it, a tool is missing, a read did not
#             answer). An interrupt exits 130.
#
# **Paste the whole block into the register**, `docs/gcp-live-checklist.md`'s
# Check 830 section, with the date and `fugaro version`. The owner's answer to
# O1 — the read the link needs — is read out of it: `u1-link` names the field
# and the permission that reads it, `u1-forge` and `u1-handmade` say whether a
# launcher can put another job's identity there.
#
# **The block never prints the marker's value.** It is a stand-in, not a key,
# but a block that printed it would be a block that had to be treated as
# secret; the readings are reported by name, and the value is only ever
# compared, never echoed.
#
# There is no `--project` flag and no project variable: the project is
# hard-coded, every `gcloud` command carries `--project=${P:?}`, and a
# container never reads the cloud. The script's own test
# (scripts/live-checks/check-830_test.sh) drives it entirely through fakes on
# PATH, with no network and no cloud project at all.
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
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the link, the token, the variables or the name" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# oneline collapses a command's output into one line, because a fact is one
# line of the block and a multi-line detail would read as facts that are not
# there.
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }
# count_lines: how many non-empty lines a file has, and 0 when it is empty or
# not there. It is not `grep -c . f || echo 0`: grep prints its 0 and exits 1 on
# a file with no match, so the fallback runs too and the count comes out as two
# lines — which put a newline in the middle of a row of the block.
count_lines() { # count_lines FILE
  local n
  n=$(grep -c . "$1" 2>/dev/null) || n=0
  printf '%s' "${n:-0}"
}
# count_matching: the same, for the lines of a file that match a pattern.
count_matching() { # count_matching FILE PATTERN
  local n
  n=$(grep -c -E "$2" "$1" 2>/dev/null) || n=0
  printf '%s' "${n:-0}"
}

# ----------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 830 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 830 needs bash"
  need mktemp
  need git "the checkout this script lives in is read for the log filter and the marker's name"
  need python3 "the job configurations, the instance objects and the job objects are built and parsed by one python3 program each, so the block carries fields rather than documents"
  need gcloud "the jobs, the instances, the audit record and the logs are all read and written with it"
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk830-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""
# What this run made, so the exit trap can delete it and the restore row can
# name it. CREATED_ANYTHING gates the sweep: a run a guard stopped made nothing
# and must not call gcloud at all.
CREATED_ANYTHING=no
LEFT_BEHIND=""
HANDMADE_MADE=""
# The probe job, its UID, and the VM the container named for itself. All of them
# are empty until a step reads them, and every row that reads one says "unread"
# or reports the row as not measured rather than stopping the whole run.
A_UID=""
A_STATE=""
A_CREATED=""
A_VM_NAME=""
A_VM_ID=""
A_VM_ZONE=""
B_UID=""
# Whether the container's own lines arrived as far as its last read, where the
# VM's name came from, and what the task's exit status said — the second channel
# the rows below fall back to when the log channel is empty.
LINES_A=""
VM_FROM=""
EXIT_READ=no
EXIT_STATE=""
EXIT_RC=""
EXIT_REACHED=no
EXIT_MD=no
EXIT_TOKEN=no
EXIT_CLAIMS=no
EXIT_MARKER=no
# The marker's value: generated here, compared everywhere, printed nowhere.
MARKER_VALUE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk830.XXXXXXXX") || die "Check 830 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 830 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  CLEANUP_RUNNING=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  sweep_leftovers
  remove_scratch
}

# sweep_leftovers is the acceptance's "it deletes the job it made": every
# resource is deleted by the step that made it, and this is the net under a run
# that ended early. It is bounded (one list, then a delete per job), it is
# quiet (no prompt, ever), and a resource it cannot delete is NAMED in the
# restore row with the command that removes it rather than left silent.
sweep_leftovers() {
  [ -n "$DRY_RUN" ] && return 0
  [ "$CREATED_ANYTHING" = yes ] || return 0
  local names
  names=$(jobs_of_mine 2>/dev/null || true)
  local n
  for n in $names; do
    gcloud batch jobs delete "$n" --project="${P:?}" --location="${L:?}" --quiet >/dev/null 2>&1 || true
  done
  # A job whose delete has been accepted is still listed until the platform has
  # finished with it, so the list is read again, bounded, rather than once: a
  # job the step before this one deleted is not a job this check left behind.
  # A poll of zero counts as a second of the bound, so the bound still bounds
  # the wait whatever the poll is set to.
  local waited=0 tick="$SWEEP_POLL"
  [ "$tick" -gt 0 ] || tick=1
  names=$(jobs_of_mine 2>/dev/null || true)
  while [ -n "$names" ] && [ "$waited" -lt "$SWEEP_BOUND" ]; do
    sleep "$tick"
    waited=$((waited + tick))
    names=$(jobs_of_mine 2>/dev/null || true)
  done
  if [ -n "$names" ]; then
    LEFT_BEHIND="${LEFT_BEHIND}${LEFT_BEHIND:+, }the job(s) $(printf '%s' "$names" | oneline)"
  fi
  if [ -n "$HANDMADE_MADE" ]; then
    gcloud compute instances delete "$HANDMADE_MADE" --project="${P:?}" --zone="${A_VM_ZONE:?}" --quiet >/dev/null 2>&1 || true
    if gcloud compute instances describe "$HANDMADE_MADE" --project="${P:?}" --zone="${A_VM_ZONE:?}" >/dev/null 2>&1; then
      LEFT_BEHIND="${LEFT_BEHIND}${LEFT_BEHIND:+, }the instance $HANDMADE_MADE"
    fi
  fi
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
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the link"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS note
  if [ -z "$SCRATCH" ]; then
    note="nothing was created: the run stopped before it made a scratch directory, so no job and no instance was made"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    note="the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
    if [ "$CREATED_ANYTHING" = no ]; then
      note="${note}, and nothing was created anywhere: no job, no instance"
    elif [ -n "$LEFT_BEHIND" ]; then
      restore=FAIL
      note="${note}. STILL IN ${P:-fugaro-dev}: ${LEFT_BEHIND}. Remove each by hand — a job with: gcloud batch jobs delete <name> --project=${P:-fugaro-dev} --location=${L:-us-east5} --quiet; an instance with: gcloud compute instances delete <name> --project=${P:-fugaro-dev} --zone=${A_VM_ZONE:-<zone>} --quiet"
    else
      note="${note}, and every job and every instance this run made are deleted: nothing of this check's is left in ${P:-fugaro-dev}"
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
# The project and the region are literals, as they are in every live check
# here: nothing reads gcloud's ambient configuration, and there is no flag that
# could point one of them elsewhere.
P=fugaro-dev
L=us-east5
# The three resources this check makes, all named `fugaro-chk830-*` and all
# labelled here. The probe job's name is fixed rather than unique-per-run
# because U8 is the measurement of using it again after deleting it.
CHK_LABEL=fugaro_chk
CHK_VALUE=830
JOB_A=fugaro-chk830-probe
JOB_B=fugaro-chk830-forge
# O8's plain VM is named after the probe job on purpose, and not `…-handmade`:
# the name is the one field a launcher picks freely, so a VM named after another
# job is the best a hand can do at passing for it. If the probe's own VM carries
# the job's identity in its name, this name reproduces that field and
# u1-handmade FAILS — which is the finding, not a false pass: what a launcher
# chooses cannot be what the link rests on.
HANDMADE_VM=${JOB_A}-handmade
# A container with curl and python3, so the probe reads the metadata server
# itself. It is a Google-hosted public image: the probe is not about the
# installation's own image, and naming one that a project may not have would
# make this check depend on a build.
IMAGE=${CHK830_IMAGE:-gcr.io/google.com/cloudsdktool/google-cloud-cli:slim}
MT=e2-standard-4
CPU_MILLI=2000
MEMORY_MIB=4096
# How long each probe container stays alive after its reads, so the VM can be
# read from outside while it exists. The check does not wait for these to
# elapse: it deletes each job once it has what it needs.
LINGER_A=${CHK830_LINGER_A:-300}
LINGER_B=${CHK830_LINGER_B:-120}
# The bounds on the waits: the probe's first line, the audit record, the reuse
# of a deleted job's name, and — when no line arrived — the task's own end, so
# its exit status can be read. Each is a knob because a slow project should be
# given longer rather than read as a FAIL.
WAIT_LINES=${CHK830_WAIT_LINES:-600}
WAIT_LINES_B=${CHK830_WAIT_LINES_B:-180}
WAIT_AUDIT=${CHK830_WAIT_AUDIT:-180}
WAIT_EXIT=${CHK830_WAIT_EXIT:-420}
REUSE_BOUND=${CHK830_REUSE_BOUND:-180}
REUSE_POLL=${CHK830_REUSE_POLL:-5}
# How long the container itself lives, which is the linger RAISED to outlast the
# wait for its lines. A run whose lines never arrive still has to read the VM
# they never came from — its serial console, its own object — and the task's
# exit status is only written once the task has ended, so a container that ended
# before its lines were waited out would have taken both readings with it. The
# owner's first run of this check could read neither for exactly that reason.
life_of() { # life_of LINGER WAIT
  local life=$1
  [ "$life" -ge "$(( $2 + 60 ))" ] || life=$(( $2 + 60 ))
  printf '%s' "$life"
}
# A unit of the bound arithmetic: each is read once, and the job's own
# maxRunDuration is computed from the life so a container Batch kills on time
# cannot be mistaken for one that failed.
probe_life_a() { life_of "$LINGER_A" "$WAIT_LINES"; }
probe_life_b() { life_of "$LINGER_B" "$WAIT_LINES_B"; }
# Deleting a Batch job is asynchronous: the delete is accepted at once and the
# job stays listable, in DELETION_IN_PROGRESS, until the platform has finished
# with it (the engine's own reaper lists that state: internal/backend/batch/
# reap.go). The sweep at the end waits for its own deletes to land within this
# bound, because a list taken one call too early is not a list of what is left.
SWEEP_BOUND=${CHK830_SWEEP_BOUND:-120}
SWEEP_POLL=${CHK830_SWEEP_POLL:-5}
# The audience the probe asks the metadata server for. A real service's URL is
# not this check's to know; the claims are the measurement, and the audience is
# printed so the block says which one was asked for.
AUD=${CHK830_AUDIENCE:-https://fugaro-chk830.invalid/}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-830.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_owner: the register's owner-run marker, the same one the other
# owner-run checks carry. It is not a security boundary — an agent's shell can
# export anything — but it is the line that stops a session from running a check
# whose whole value is that a person read what it printed.
guard_owner() {
  if [ "${CHK830_OWNER_RUN:-}" != yes ]; then
    fact owner FAIL "this is an owner-run check and CHK830_OWNER_RUN is not exported as yes; no agent runs any step of it, and the plan blocks BR3, BR5 and BR12 on what it prints"
    stop "Check 830 is owner-run. Run it at your own terminal, after: export CHK830_OWNER_RUN=yes — no agent performs any step of it. BR3 waits on this result (R4)"
  fi
  fact owner PASS "CHK830_OWNER_RUN=yes: this run was started by you at your own terminal, and no agent performs any step of Check 830"
}

# read_const pulls one quoted Go constant out of a file: the tree is the
# authority for the log filter and the label vocabulary, and a copy of either
# in this script would be a second home for a fact the check exists to test.
read_const() { # read_const FILE CONSTNAME
  sed -n "s/^[[:space:]]*$2[[:space:]]*=[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$1" 2>/dev/null | head -n 1
}

# guard_tree: the checkout this script lives in, read for the things the live
# readings are judged against rather than remembering: the filter the runner
# itself reads a Batch job's logs with (internal/backend/batch/logs.go), the
# label vocabulary a real run's job carries (internal/backend/gcp/names.go and
# internal/backend/batch/names.go), and the wrapped variable's name the plan
# names (docs/batch-run-identity/, R5) — the variable U3 is about.
guard_tree() {
  TREE=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd) \
    || stop "Check 830 stopped: the script's own path ($0) does not lead to a checkout, so the log filter and the marker's name cannot be read out of the tree"
  local logs="$TREE/internal/backend/batch/logs.go"
  local gcpnames="$TREE/internal/backend/gcp/names.go"
  [ -f "$logs" ] && [ -f "$gcpnames" ] \
    || stop "Check 830 stopped: $TREE does not carry internal/backend/batch/logs.go and internal/backend/gcp/names.go, so this is not the Fugaro tree the script belongs to"
  # The filter the tree reads a job's task logs with, both halves: the label it
  # selects by and the log the lines go to. A read that cannot find them means
  # the file moved, which is a reason to stop rather than to guess a filter.
  grep -q 'labels.job_uid=' "$logs" || stop "Check 830 stopped: $logs no longer builds its filter with labels.job_uid, so the read this check makes of the probe's own logs would not be the runner's read"
  grep -q 'batch_task_logs' "$logs" || stop "Check 830 stopped: $logs no longer names batch_task_logs, so the log the probe's lines land in cannot be read out of the tree"
  TASK_LOG="projects/${P}/logs/batch_task_logs"
  # The labelled-job vocabulary a real run carries: the managed pair, so the
  # probe's job is shaped like one a reaper and a doctor would recognise.
  MANAGED_KEY=$(read_const "$gcpnames" LabelManaged)
  MANAGED_VAL=$(read_const "$gcpnames" ManagedValue)
  LABEL_NOTE=""
  if [ -z "$MANAGED_KEY" ] || [ -z "$MANAGED_VAL" ]; then
    MANAGED_KEY=fugaro
    MANAGED_VAL=managed
    LABEL_NOTE=", and the managed label pair could not be read out of $gcpnames so fugaro=managed is the fallback — the probe's job carries one label less than a real run's, which no row here turns on"
  fi
  # The wrapped variable's name, out of the plan: U3 is about the variable the
  # blob will travel in (R5), and the documents are where that name is decided
  # until BR1 writes it into the code.
  MARKER_NAME=$(grep -rhoE 'FUGARO_BUDGET_TOKEN_WRAPPED' "$TREE/docs/batch-run-identity/" 2>/dev/null | head -n 1) || MARKER_NAME=""
  MARKER_NOTE=""
  if [ -z "$MARKER_NAME" ]; then
    MARKER_NAME=FUGARO_BUDGET_TOKEN_WRAPPED
    MARKER_NOTE=", and the marker's name could not be read out of docs/batch-run-identity/ so the plan's own name is used as a fallback"
  fi
  local sha
  sha=$(git -C "$TREE" rev-parse HEAD 2>/dev/null || echo unknown)
  TREE_SHA=$sha
  fact tree PASS "the log filter (labels.job_uid=\"<uid>\" AND logName=\"${TASK_LOG}\"), the managed label pair ${MANAGED_KEY}=${MANAGED_VAL} and the marker's name ${MARKER_NAME} were all read out of the tree at ${TREE} (commit ${sha}), never from copies in this script${LABEL_NOTE}${MARKER_NOTE}"
}

# guard_belong: the owner's own installation and its repositories, refused by
# name. This check names its project itself and has no flag that could name
# another, so the guard is about what the environment could supply instead.
guard_belong() {
  local ambient=${CLOUDSDK_CORE_PROJECT:-}${GOOGLE_CLOUD_PROJECT:-}${GCLOUD_PROJECT:-}
  local low
  low=$(printf '%s' "$ambient" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "the ambient gcloud configuration names a project matching '${ambient}', and the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch"
    stop "Check 830 never runs against 'belong', EdgeWeb or EdgeServer. Unset CLOUDSDK_CORE_PROJECT/GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and run it again: every command here names --project=${P} itself"
    ;;
  esac
  fact belong PASS "the project this check names is ${P} and the region ${L}, both hard-coded; neither 'belong' nor EdgeWeb nor EdgeServer is named by any step, and this check has no flag or variable that could name another project"
}

# ------------------------------------------------------------------ the cloud
# jobs_of_mine: this check's own jobs, by its own label. Every delete and every
# leftover read goes through it, so a resource made by a run that died is found
# again by the next one.
jobs_of_mine() {
  gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
    --filter="labels.${CHK_LABEL}=${CHK_VALUE}" --format='value(name.basename())' 2>/dev/null || true
}

# job_field: one value out of a job object, with the disclaimer that an empty
# answer is an answer (a job with no UID is a job whose logs cannot be read).
job_field() { # job_field NAME FORMAT
  gcloud batch jobs describe "$1" --project="${P:?}" --location="${L:?}" --format="value($2)" 2>/dev/null || true
}

job_uid() { job_field "$1" uid; }

# instance_field: one value out of an instance object.
instance_field() { # instance_field NAME ZONE FORMAT
  gcloud compute instances describe "$1" --project="${P:?}" --zone="$2" --format="value($3)" 2>/dev/null || true
}

# log_lines: the entries of one Batch job's task logs, as the tree's own filter
# selects them, flattened to text. This is where the probe's own readings come
# from, so it is also what proves the container ran at all.
log_lines() { # log_lines UID OUTFILE
  log_text "labels.job_uid=\"$1\" AND logName=\"${TASK_LOG}\"" "$2"
}

# project_log_text: everything Cloud Logging holds for a filter, flattened.
# The U3 readings use it: one for the job's own lines, and one for a search of
# the whole project for the marker's value.
project_log_text() { # project_log_text FILTER OUTFILE
  gcloud logging read "$1" --project="${P:?}" --freshness="${CHK830_LOG_AGE:-1h}" \
    --limit=500 --format=json 2>/dev/null | python3 -c 'import json,sys
try:
    entries = json.load(sys.stdin)
except Exception:
    entries = []
print(json.dumps(entries))
' >"$2" 2>/dev/null || true
}

# wait_for_lines: polls a job's task logs until a line matching a pattern
# arrives, bounded. It prints nothing and returns the number of lines it saw,
# so the caller decides what a missing line means.
wait_for_lines() { # wait_for_lines UID PATTERN OUTFILE SECONDS
  local waited=0
  while :; do
    log_lines "$1" "$3"
    if grep -qE "$2" "$3" 2>/dev/null; then return 0; fi
    [ "$waited" -ge "$4" ] && return 1
    sleep 10
    waited=$((waited + 10))
    say "waiting for the probe's first line (${waited}s of ${4}s)"
  done
}
# --------------------------------------------------------------- the fixtures
# The container both probe jobs run. It is written once, quoted, so nothing in
# it is expanded by this script: every parameter reaches the container as one of
# the job's own variables, which is also how the marker variable gets there.
# It prints one `C830` line per reading and never prints the marker's value.
# It runs under /bin/sh — the shebang and the job's own entrypoint both name it,
# and the suite pins that they agree — and sets no `-e`: the image's own shell is
# the one thing this check must not have to assume. The first run of this check reported no
# line of the container's own at all, and a `#!/bin/bash` container in an image
# that ships no bash fails at start with nothing on stdout — the check could not
# tell that from a line that never reached Cloud Logging. /bin/sh is in every
# image a Windows-free container can be built on, and the reads below are POSIX.
#
# The container's readings also leave through the task's EXIT STATUS, which is
# the second channel: 100 plus one bit per reading (1 it reached the end of its
# reads, 2 the metadata server answered the instance's own identity, 4 an
# identity token was obtained, 8 the token carried all four claims, 16 the
# marker variable was in its own environment). Batch's own failures are reserved
# above 50000 (batch/docs/troubleshooting#reserved-exit-codes), so a code in the
# 100s is this container's and nothing else's — and the exit status arrives
# through `batch.jobs.get`, the call the check already makes. A container that
# never starts reports no code at all, which is the third answer and the one the
# first run could not tell apart from the other two. The code carries no value:
# a bit says a reading happened, never what it read.
write_container() {
  cat >"$SCRATCH/container.sh" <<'C830CONTAINER'
#!/bin/sh
set -u
MD=http://metadata.google.internal/computeMetadata/v1
md() { curl -s --max-time 8 -H 'Metadata-Flavor: Google' "$MD/$1" 2>/dev/null || true; }
bits=0
echo "C830 start host=$(hostname)"
name=''
for p in instance/name instance/id instance/zone instance/hostname; do
  v=$(md "$p")
  [ -n "$v" ] || v='<unreachable>'
  case "$p" in instance/name) name=$v ;; esac
  echo "C830 metadata $p=$v"
done
case "$name" in '' | '<unreachable>') ;; *) bits=$((bits + 2)) ;; esac
pj=$(md project/project-id)
[ -n "$pj" ] || pj='<unreachable>'
echo "C830 project $pj"
at=$(md 'instance/attributes/?recursive=true')
[ -n "$at" ] || at='<unreachable>'
echo "C830 attrs $(printf '%s' "$at" | tr '\n' ' ')"
sa=$(md instance/service-accounts/default/email)
[ -n "$sa" ] || sa='<unreachable>'
echo "C830 sa $sa"
tok=$(md "instance/service-accounts/default/identity?audience=${FUGARO_CHK830_AUD}&format=full")
if [ -z "$tok" ]; then
  echo "C830 token <unreachable>"
else
  bits=$((bits + 4))
  echo "C830 token len=${#tok}"
  # The decoder's own status is the fifth reading: it exits 0 only when the
  # token carries all four claims, so the shell needs no second decode to set
  # the bit. It prints no part of the token.
  if printf '%s' "$tok" | python3 -c '
import base64, json, sys
t = sys.stdin.read().strip()
try:
    p = t.split(".")[1]
    p += "=" * (-len(p) % 4)
    d = json.loads(base64.urlsafe_b64decode(p))
except Exception:
    print("C830 token <undecodable>")
    sys.exit(3)
print("C830 token aud=%s" % d.get("aud", "<absent>"))
print("C830 token iss=%s" % d.get("iss", "<absent>"))
print("C830 token claims=%s" % ",".join(sorted(str(k) for k in d)))
ce = d.get("google.compute_engine") or {}
for k in ("instance_id", "instance_name", "project_id", "zone"):
    print("C830 token %s=%s" % (k, ce.get(k, "<absent>")))
if "exp" in d and "iat" in d:
    print("C830 token life=%ss" % (int(d["exp"]) - int(d["iat"])))
sys.exit(0 if all(ce.get(k) for k in ("instance_id", "instance_name", "project_id", "zone")) else 3)
'; then
    bits=$((bits + 8))
  else
    case "$?" in 127) echo "C830 token <no-decoder>" ;; esac
  fi
fi
mname=${FUGARO_CHK830_MARKER:-}
mvalue=''
case "$mname" in
'' | [!A-Za-z_]*) ;;
*) eval "mvalue=\${$mname:-}" ;;
esac
if [ -n "$mvalue" ]; then
  bits=$((bits + 16))
  echo "C830 env-marker present"
else
  echo "C830 env-marker absent"
fi
echo "C830 read"
bits=$((bits + 1))
echo "C830 exit $((100 + bits))"
sleep "${FUGARO_CHK830_LINGER:-120}"
echo "C830 done"
exit $((100 + bits))
C830CONTAINER
  chmod +x "$SCRATCH/container.sh"
}

# mk_params writes the parameters one job configuration is built from, as
# key<TAB>value lines: nothing in them can be read as a shell word, so a value
# with spaces or quotes in it reaches the builder whole.
mk_params() { # mk_params OUT key=value ...
  local out=$1
  shift
  : >"$out"
  local kv
  for kv in "$@"; do
    printf '%s\t%s\n' "${kv%%=*}" "${kv#*=}" >>"$out"
  done
}

# write_config turns one params file into one Batch job configuration. It is one
# program because the container script goes into the JSON as a string, and a
# second escaping written by hand would be a second thing to get wrong.
write_config() {
  cat >"$SCRATCH/mkconfig.py" <<'C830MKCONFIG'
import json, sys

params = {}
with open(sys.argv[1]) as fh:
    for line in fh:
        line = line.rstrip("\n")
        if line and "\t" in line:
            k, v = line.split("\t", 1)
            params[k] = v

body = params.get("script", "")
if body:
    with open(body) as fh:
        container = {
            "imageUri": params["image"],
            "entrypoint": "/bin/sh",
            "commands": ["-c", fh.read()],
        }
else:
    container = {"imageUri": params["image"], "entrypoint": "/bin/sh", "commands": ["-c", "true"]}


def obj(name):
    raw = params.get(name, "")
    return json.loads(raw) if raw else {}


instance = {
    "policy": {"machineType": params["machine"], "provisioningModel": "STANDARD"},
    "blockProjectSshKeys": True,
}

job = {
    "taskGroups": [
        {
            "taskCount": 1,
            "parallelism": 1,
            "taskSpec": {
                "runnables": [{"container": container}],
                "computeResource": {
                    "cpuMilli": int(params["cpu"]),
                    "memoryMib": int(params["mem"]),
                },
                "maxRetryCount": 0,
                "maxRunDuration": params.get("max_run", "900s"),
                "environment": {"variables": obj("env")},
            },
        }
    ],
    "allocationPolicy": {
        "instances": [instance],
        "labels": obj("alabels"),
    },
    "logsPolicy": {"destination": "CLOUD_LOGGING"},
    "labels": obj("labels"),
}
with open(params["out"], "w") as fh:
    json.dump(job, fh, indent=2)
    fh.write("\n")
C830MKCONFIG
  python3 "$SCRATCH/mkconfig.py" "$1"
}

# json_obj builds a JSON object out of key=value pairs, so a label set or a
# variable set can be handed to the builder as one argument. Values are our
# own (a job name, a UID, a marker), so no escaping is needed beyond the JSON
# itself, which python does.
json_obj() { # json_obj key=value ...
  python3 -c 'import json,sys
d = {}
for a in sys.argv[1:]:
    if "=" in a:
        k, v = a.split("=", 1)
        d[k] = v
print(json.dumps(d))
' "$@"
}

# ----------------------------------------------------------------- the steps
# submit-a: the probe job, shaped like a real run: the same label keys the
# backend writes, the same `blockProjectSshKeys`, no retry, its logs to Cloud
# Logging, and a task share the type holds. It is the job every row below is
# about, so the fact says what was submitted rather than what was intended.
step_submit_a() {
  say "submitting the probe job ${JOB_A}"
  PROBE_LIFE_A=$(probe_life_a)
  mk_params "$SCRATCH/params-a" \
    out="$SCRATCH/job-a.json" \
    image="$IMAGE" \
    script="$SCRATCH/container.sh" \
    machine="$MT" cpu="$CPU_MILLI" mem="$MEMORY_MIB" \
    max_run="$((PROBE_LIFE_A + 300))s" \
    labels="$(json_obj "$MANAGED_KEY=$MANAGED_VAL" "$CHK_LABEL=$CHK_VALUE")" \
    alabels="$(json_obj "$MANAGED_KEY=$MANAGED_VAL" "$CHK_LABEL=$CHK_VALUE")" \
    env="$(json_obj "FUGARO_CHK830_AUD=$AUD" "FUGARO_CHK830_LINGER=$PROBE_LIFE_A" "FUGARO_CHK830_MARKER=$MARKER_NAME" "$MARKER_NAME=$MARKER_VALUE")"
  write_config "$SCRATCH/params-a"
  gcloud compute instances list --project="${P:?}" --format='value(name)' >"$SCRATCH/instances.before" 2>/dev/null || true
  if ! gcloud batch jobs submit "$JOB_A" --project="${P:?}" --location="${L:?}" --config="$SCRATCH/job-a.json" >"$SCRATCH/submit-a.out" 2>"$SCRATCH/err"; then
    local msg
    msg=$(oneline <"$SCRATCH/err")
    fact probe-submitted FAIL "the probe job ${JOB_A} could not be submitted in ${P}/${L} (${msg}); nothing was created, so the link, the token, where variables reach and the reuse of a name are all unmeasured"
    stop "the probe job could not be submitted: ${msg}"
  fi
  CREATED_ANYTHING=yes
  A_UID=$(job_uid "$JOB_A")
  A_STATE=$(job_field "$JOB_A" status.state)
  A_CREATED=$(job_field "$JOB_A" createTime)
  if [ -z "$A_UID" ]; then
    fact probe-submitted FAIL "the probe job ${JOB_A} was created but answers with no UID, and a Batch job's task logs are selected by that UID (internal/backend/batch/logs.go refuses one without it): nothing can be read about this job"
    stop "the probe job answered with no UID, so its logs cannot be read"
  fi
  fact probe-submitted PASS "the probe job ${JOB_A} was created in ${P}/${L} (uid ${A_UID}, state ${A_STATE:-<unread>}, created ${A_CREATED:-<unread>}; ${MT}, ${CPU_MILLI}m/${MEMORY_MIB}MiB, container alive ${PROBE_LIFE_A}s after its reads — the ${LINGER_A}s linger raised to outlast the ${WAIT_LINES}s wait for its lines, so a run whose lines never arrive can still read the VM they never came from and the task's exit status). It carries ${CHK_LABEL}=${CHK_VALUE} and ${MANAGED_KEY}=${MANAGED_VAL} in Job.labels and in AllocationPolicy.labels, the marker variable ${MARKER_NAME} with a stand-in value, logs to CLOUD_LOGGING, and no retry — the shape a real run's job has. Its container reports each of its readings twice, in its lines and in the task's exit status"
}

# observe-a: the container's own readings, then the VM it ran on. The
# container's lines come back through the same filter the runner reads a job's
# logs with, so a run that cannot find them is also a run that could not read a
# real job's logs — which is worth knowing on its own.
step_observe_a() {
  local pattern='^C830 read$'
  if wait_for_lines "$A_UID" "$pattern" "$SCRATCH/lines-a" "$WAIT_LINES"; then
    LINES_A=yes
    fact probe-ran PASS "the probe container ran and reported: $(count_matching "$SCRATCH/lines-a" '^C830 ') C830 lines came back through the tree's own filter (labels.job_uid=\"${A_UID}\" AND logName=\"${TASK_LOG}\"), the last of them 'C830 read'. The container then stays alive ${PROBE_LIFE_A}s so its VM can be read from outside while it exists"
  else
    LINES_A=no
    # How far the container got is the reading that tells the three cases apart:
    # nothing at all is a container that never ran or a line that never landed,
    # and a prefix ending early is a container that stopped there. The number and
    # the last line are read out of the same file the runner's own filter filled.
    local got last
    got=$(count_matching "$SCRATCH/lines-a" '^C830 ')
    last=$(grep '^C830 ' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1 || true)
    local prefix=""
    if [ "${got:-0}" -gt 0 ]; then
      prefix="${got} C830 line(s) DID come back and the last was '$(printf '%s' "$last" | oneline)': the container ran and stopped (or was stopped) before its last read, so its lines do land in the log the runner reads"
    else
      prefix="not one line of the container's own came back at all, so either the container never ran (image pull, the interpreter its entrypoint names, quota, a task that failed at start) or its lines do not land in the log the runner reads"
    fi
    fact probe-ran FAIL "no 'C830 read' line arrived in ${WAIT_LINES}s through the tree's own filter (labels.job_uid=\"${A_UID}\" AND logName=\"${TASK_LOG}\"): ${prefix}. The rows below do not conclude from this: the job's own state and events, its task's state and events, the VM's serial console and the same query under the job's own resource type are read next (probe-diag), and the container's task exit status — its second channel — is read once the task has ended (probe-exit)"
  fi
  if ! vm_of_job "$SCRATCH/lines-a"; then
    fact vm-a FAIL "${VM_PROBLEM}: which instance is this job's is unknown, so nothing is read off it. A run of another job in ${P} at the same time is the innocent reading — run this check when the project is quiet"
    return 0
  fi
  A_VM_NAME=$VM_NAME
  A_VM_ID=$VM_ID
  A_VM_ZONE=$VM_ZONE
  gcloud compute instances describe "$A_VM_NAME" --project="${P:?}" --zone="${A_VM_ZONE:?}" --format=json >"$SCRATCH/vm-a.json" 2>"$SCRATCH/err" \
    || {
      local msg
      msg=$(oneline <"$SCRATCH/err")
      fact vm-a FAIL "the probe's VM ${A_VM_NAME} in ${A_VM_ZONE} could not be read (${msg}): the link cannot be judged without the instance's own object"
      return 0
    }
  gcloud batch jobs describe "$JOB_A" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job-a.json" 2>/dev/null || true
  A_VM_MT=$(instance_field "$A_VM_NAME" "$A_VM_ZONE" 'machineType.basename()')
  A_VM_SA=$(instance_field "$A_VM_NAME" "$A_VM_ZONE" 'serviceAccounts[0].email')
  # WHERE the name came from is part of this row's answer, and it is the one
  # thing the first run of this check got wrong: the row said the container had
  # named its own VM while the lines it would have named it in had never
  # arrived, and the name had come from the instance list instead. The two reads
  # are not equally strong — the container's own metadata read is the VM saying
  # what it is, and a new instance in a list is a VM this project made while the
  # probe was the only thing being submitted — so the row says which one it was.
  local from
  case "$VM_FROM" in
  container) from="the container named it itself out of the metadata server, and its own line is where this row's name comes from" ;;
  list) from="the container did NOT name it (its metadata read answered nothing): the name comes from the instance list taken around the submit, where exactly one instance appeared, and that is a weaker reading — a VM another job of ${P} made in the same window would be this one's by this reading" ;;
  *) from="where this row's name came from was not recorded" ;;
  esac
  fact vm-a PASS "the probe's VM is ${A_VM_NAME} in ${A_VM_ZONE} (id ${A_VM_ID:-<unread>}, ${A_VM_MT:-<unread>}, running as ${A_VM_SA:-<unread>}): ${from}. Its whole object is what the link is judged on; the job's own object is read beside it"
}
# redact_stream: nothing that looks like a credential is printed from a reading
# this check did not write — the serial console, the job's own events and the
# broader log query are written by the platform and by programs this check does
# not own. The probe prints no token, but a diagnostic that leaked a bearer
# credential would be worse than the failure it was diagnosing, so a line
# carrying one is redacted and the marker's stand-in value with it.
redact_stream() {
  sed -E \
    -e 's/ey[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+/<redacted-token>/g' \
    -e 's/ya29\.[A-Za-z0-9_.-]+/<redacted-token>/g' \
    -e 's/AIza[A-Za-z0-9_-]+/<redacted-token>/g' \
    -e "s/${MARKER_VALUE}/<marker-value>/g"
}

# diag.py reads the job's own object for the three things the block has to say
# when no line arrived: the job's state, the job's own status events, and each
# task's state with the status events it carries. The task's exit code is among
# them — `TaskExecution.ExitCode`, defined only for a task-level event of a task
# that failed — and it is the second channel the probe's rows fall back to. Only
# these fields are printed: the object itself carries the job's variables, and
# one of them is the marker's value.
write_diag_py() {
  cat >"$SCRATCH/diag.py" <<'C830DIAG'
import json, sys

try:
    with open(sys.argv[1]) as fh:
        job = json.load(fh)
except Exception:
    job = {}


def flat(s, n=240):
    return " ".join(str(s or "").split())[:n]


def events(evs, where):
    for e in evs or []:
        if not isinstance(e, dict):
            continue
        te = e.get("taskExecution") or {}
        code = te.get("exitCode")
        print("event\t%s\t%s\t%s\t%s\t%s" % (
            where, e.get("type") or "?", e.get("taskState") or "-",
            "-" if code is None else code, flat(e.get("description"))))
        if code is not None:
            print("exit\t%s\t%s" % (e.get("taskState") or "-", code))


status = job.get("status") or {}
print("state\t%s" % (status.get("state") or "<unread>"))
events(status.get("statusEvents"), "job")
tasks = []
for group in job.get("taskGroups") or []:
    for task in (group or {}).get("tasks") or []:
        if not isinstance(task, dict):
            continue
        ts = task.get("status") or {}
        tasks.append("%s=%s" % ((task.get("name") or "?").split("/")[-1], ts.get("state") or "<unread>"))
        events(ts.get("statusEvents"), "task")
print("tasks\t%s" % (",".join(tasks) or "none reported"))
C830DIAG
}

diag_read() { # diag_read JOBJSON
  python3 "$SCRATCH/diag.py" "$1" >"$SCRATCH/diag.out" 2>/dev/null || : >"$SCRATCH/diag.out"
}

diag_field() { # diag_field KEY
  awk -F'\t' -v k="$1" '$1 == k { print $2; exit }' "$SCRATCH/diag.out" 2>/dev/null || true
}

diag_events() { # diag_events
  awk -F'\t' '$1 == "event" {
    printf "%s[%s %s%s]%s ", $2, $3, $4, ($5 == "-" ? "" : " exit=" $5), ($6 == "" ? "" : " \"" $6 "\"")
  }' "$SCRATCH/diag.out" 2>/dev/null | oneline
}

diag_exit() { # diag_exit — the task's own exit code, or empty
  awk -F'\t' '$1 == "exit" { print $3; exit }' "$SCRATCH/diag.out" 2>/dev/null || true
}

# log_text: everything Cloud Logging holds for a filter, flattened to text. It is
# log_lines' one implementation: the runner's own filter is one filter, and the
# broader reads probe-diag makes are others.
log_text() { # log_text FILTER OUTFILE
  gcloud logging read "$1" --project="${P:?}" --freshness="${CHK830_LOG_AGE:-1h}" \
    --limit=500 --format=json 2>/dev/null \
    | python3 -c 'import json,sys
try:
    entries = json.load(sys.stdin)
except Exception:
    entries = []
for e in entries if isinstance(entries, list) else []:
    p = e.get("textPayload")
    if p is None:
        p = (e.get("jsonPayload") or {}).get("message") or ""
    for line in str(p).splitlines():
        print(line.rstrip())
' >"$2" 2>/dev/null || true
}

# first_lines: the first few lines of a file, redacted, as one short string. A
# diagnostic names what it found rather than counting it.
first_lines() { # first_lines FILE LIMIT
  local out
  out=$(head -n "$2" "$1" 2>/dev/null | grep . | redact_stream | oneline | cut -c1-400 || true)
  printf '%s' "$out"
}

# probe-diag: what the block owes a run whose lines did not arrive. It is taken
# BEFORE the job is deleted and while the VM is still alive (which is why the
# container's own life is the linger raised to outlast the wait for its lines),
# and it reads four things: the job's state and its own status events, each
# task's state and its status events, the VM's serial console tail, and the same
# log query under the job's own resource type — both by the runner's label and by
# the uid alone, because "the label is not the one the lines carry" and "the log
# name is not the one the runner reads" are two of the ways the first run's lines
# could have gone missing. Nothing here is a verdict on the design: probe-ran has
# already answered that. It is the evidence that says WHICH of the two it was.
step_probe_diag() {
  if [ "$LINES_A" = yes ]; then
    fact probe-diag INFO "not taken: the container's own lines came back through the runner's filter, so there is nothing to diagnose. This row is the one a run with no line of its own owes the reader — the job's own state and events, its task's state and events, the VM's serial console tail, and the same query under the job's own resource type (resource.type=\"batch.googleapis.com/Job\") by the runner's label and by the uid alone"
    return 0
  fi
  local job_state events task_states tasks_shown
  gcloud batch jobs describe "$JOB_A" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job-a-diag.json" 2>"$SCRATCH/err" || true
  diag_read "$SCRATCH/job-a-diag.json"
  job_state=$(diag_field state)
  task_states=$(diag_field tasks)
  # The status events are written by the platform, not by this check: an event's
  # description is free text, so it goes through the same redaction the serial
  # console does before any of it reaches a row that promises no credential-shaped
  # value is printed.
  events=$(diag_events | redact_stream 2>/dev/null | oneline)
  tasks_shown=$(printf '%s' "${task_states:-none reported}" | oneline)
  local serial="not taken"
  if [ -n "$A_VM_NAME" ] && [ -n "$A_VM_ZONE" ]; then
    if gcloud compute instances get-serial-port-output "$A_VM_NAME" --project="${P:?}" --zone="${A_VM_ZONE:?}" >"$SCRATCH/serial-a.txt" 2>"$SCRATCH/err"; then
      tail -n 40 "$SCRATCH/serial-a.txt" | redact_stream >"$SCRATCH/serial-a.tail" 2>/dev/null || : >"$SCRATCH/serial-a.tail"
      serial="its last $(count_lines "$SCRATCH/serial-a.tail") non-empty line(s) of the last 40, redacted for anything that looks like a credential: $(first_lines "$SCRATCH/serial-a.tail" 40)"
    else
      serial="the read failed ($(oneline <"$SCRATCH/err")) — the VM had been destroyed by then, which is what the container's own life ($PROBE_LIFE_A}s here) governs"
    fi
  else
    serial="not taken: which instance is this job's is unknown (see vm-a)"
  fi
  log_text "resource.type=\"batch.googleapis.com/Job\" AND labels.job_uid=\"${A_UID}\"" "$SCRATCH/job-res-lines.txt"
  log_text "resource.type=\"batch.googleapis.com/Job\" AND \"${A_UID}\"" "$SCRATCH/job-any-lines.txt"
  local by_label by_any found_label found_any
  by_label=$(count_lines "$SCRATCH/job-res-lines.txt")
  by_any=$(count_lines "$SCRATCH/job-any-lines.txt")
  found_label=""
  [ "${by_label:-0}" -gt 0 ] && found_label=", the first of them \"$(first_lines "$SCRATCH/job-res-lines.txt" 2)\""
  found_any=""
  [ "${by_any:-0}" -gt 0 ] && found_any=", the first of them \"$(first_lines "$SCRATCH/job-any-lines.txt" 2)\""
  fact probe-diag INFO "no line of the container's own arrived, so this run owes the reader the evidence that says which of the three it was — and reads it before the job is deleted: the job's own state is ${job_state:-<unread>} and its task's state(s) ${task_states:-<unread>}; the job's and the task's own status events read: ${events:-<none>}. The VM's serial console: ${serial}. The same query under the job's own resource type (resource.type=\"batch.googleapis.com/Job\") found ${by_label} line(s) by the runner's label (labels.job_uid=\"${A_UID}\" — the read that catches the container's lines under a log name the runner does not read)${found_label}, and ${by_any} line(s) by the uid alone (any field carrying it — the read that catches a label that is not labels.job_uid)${found_any}. Nothing is printed from the job's own object but these fields, and nothing that looks like a credential is printed at all"
}
# probe-exit: the probe's second channel. A container that never starts reports
# no exit status at all; one that starts reports 100 plus a bit per reading it
# took (the encoding is in the container at the top of this script), and Batch's
# own failures are reserved above 50000, so a code in the 100s is this
# container's and nothing else's. It arrives through the job's own object — the
# `batch.jobs.get` the credentials service already holds (F26) — so the channel
# costs no new permission and no new resource. The task has to END for it to be
# written, which is why this row waits for it, bounded, and why the container's
# life is not shortened to make the wait cheaper: the readings probe-diag takes
# need the VM alive.
step_probe_exit() {
  if [ "$LINES_A" = yes ]; then
    fact probe-exit INFO "not taken: the container's own lines came back, so its second channel was not needed — and reading it would mean waiting ${WAIT_EXIT}s for the container to end for a reading its lines already gave. This row is read when probe-ran fails: the task's exit status is 100 plus one bit per reading (reached the end of its reads, the metadata server answered, a token was obtained, all four claims present, the marker reached its environment), and it is the one channel that survives a broken log channel"
    return 0
  fi
  local waited=0 ended=no
  while :; do
    gcloud batch jobs describe "$JOB_A" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job-a-exit.json" 2>/dev/null || true
    diag_read "$SCRATCH/job-a-exit.json"
    EXIT_STATE=$(diag_field state)
    EXIT_RC=$(diag_exit)
    [ -n "$EXIT_RC" ] && break
    case "$EXIT_STATE" in
    SUCCEEDED | FAILED | CANCELLED | DELETION_IN_PROGRESS) ended=yes; break ;;
    esac
    [ "$waited" -ge "$WAIT_EXIT" ] && break
    sleep 10
    waited=$((waited + 10))
    say "waiting for the probe's task to end so its exit status can be read (${waited}s of ${WAIT_EXIT}s)"
  done
  if [ -n "$EXIT_RC" ] && [ "$EXIT_RC" -ge 100 ] && [ "$EXIT_RC" -le 131 ]; then
    EXIT_READ=yes
    local bits=$((EXIT_RC - 100))
    [ $((bits & 1)) -eq 0 ] || EXIT_REACHED=yes
    [ $((bits & 2)) -eq 0 ] || EXIT_MD=yes
    [ $((bits & 4)) -eq 0 ] || EXIT_TOKEN=yes
    [ $((bits & 8)) -eq 0 ] || EXIT_CLAIMS=yes
    [ $((bits & 16)) -eq 0 ] || EXIT_MARKER=yes
    local said=""
    [ "$EXIT_REACHED" = yes ] && said="${said}it reached the end of its reads, "
    [ "$EXIT_MD" = yes ] && said="${said}the metadata server answered the instance's own identity, "
    [ "$EXIT_TOKEN" = yes ] && said="${said}it obtained an identity token, "
    [ "$EXIT_CLAIMS" = yes ] && said="${said}the token carried all four claims, "
    [ "$EXIT_MARKER" = yes ] && said="${said}the marker variable was in its own environment, "
    fact probe-exit INFO "the probe's task reported an exit status of its own: ${EXIT_RC} (job state ${EXIT_STATE:-<unread>}), which reads as ${said:-nothing: none of its reads were taken}. It is the container's own code, not Batch's — Batch's failures are reserved above 50000. This is the second channel, read with the batch.jobs.get this service already makes, and the u2-token and u3-marker rows below answer from it where the log channel is empty"
    return 0
  fi
  # Whether the job ENDED is what tells the two failure rows apart: a task that
  # ended without a readable code is a different reading from a job that had not
  # ended at all when this row stopped waiting, and this is the row the first run
  # of this check needed and did not have.
  if [ -z "$EXIT_RC" ] && [ "$ended" != yes ]; then
    fact probe-exit FAIL "neither channel answered: the container's lines never arrived and the job had not ended within ${WAIT_EXIT}s (its own state read ${EXIT_STATE:-<unread>} at the last poll), so no task exit status was written and the container's own readings are UNKNOWN rather than absent — this run cannot tell a container that never ran from one whose lines did not land, and it does NOT say the task ended. Raise CHK830_WAIT_EXIT (the container's own life is CHK830_LINGER_A raised to outlast CHK830_WAIT_LINES, so the two bounds move together) and run again"
    return 0
  fi
  # The code is spelled out in a variable rather than inside a `:+` expansion:
  # bash reads an apostrophe inside `${var:+...}` as an opening quote, and the
  # sentences here carry "container's" and "Batch's". (The same trap cost this
  # script a syntax error, so the rows below keep their nouns in variables.)
  local code_note=""
  if [ -n "$EXIT_RC" ]; then
    code_note=" with a code of ${EXIT_RC}, which is not this container's own — Batch's own codes are reserved above 50000, so the task was failed or killed by Batch before the container's own status was written"
  fi
  fact probe-exit FAIL "the second channel did not answer either: the container's lines never arrived and the task ended (job state ${EXIT_STATE:-<unread>})${code_note} without any task exit status in the job's own object. Read it by hand: gcloud batch jobs describe ${JOB_A} --project=${P} --location=${L} --format=json, and look at status.statusEvents[].taskExecution.exitCode and at each task's own status events. Until then the container's own readings are UNKNOWN rather than absent"
}

# u2: what the identity token carries. The container asks the metadata server
# for a token in full format and decodes it itself; this row is about the
# claims, and the token — a bearer credential for that VM — is never printed,
# neither here nor in the container's lines.
step_u2_token() {
  local reachable=yes
  grep -q '^C830 token len=' "$SCRATCH/lines-a" 2>/dev/null || reachable=no
  local id name project zone
  id=$(sed -n 's|^C830 token instance_id=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  name=$(sed -n 's|^C830 token instance_name=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  project=$(sed -n 's|^C830 token project_id=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  zone=$(sed -n 's|^C830 token zone=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  zone=${zone##*/}
  local life iss aud missing=""
  life=$(sed -n 's|^C830 token life=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  iss=$(sed -n 's|^C830 token iss=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  aud=$(sed -n 's|^C830 token aud=||p' "$SCRATCH/lines-a" 2>/dev/null | tail -n 1)
  local got=""
  local claim
  for claim in "instance_id:${id:-}" "instance_name:${name:-}" "project_id:${project:-}" "zone:${zone:-}"; do
    local value=${claim#*:}
    case "$value" in
    "" | "<absent>" | "<unreachable>" | "<undecodable>") missing="${missing}${missing:+, }google.compute_engine.${claim%%:*}" ;;
    esac
  done
  got="instance_id=${id:-<none>}, instance_name=${name:-<none>}, project_id=${project:-<none>}, zone=${zone:-<none>}"
  if [ "$reachable" = yes ]; then
    if [ -n "$missing" ]; then
      fact u2-token FAIL "the identity token in full format was obtained but does not carry ${missing}: the claims found were ${got} (aud=${aud:-<none>}, iss=${iss:-<none>}${life:+, life=${life}}). The token's instance identity is what the credentials service matches the VM to its job with (D3101, U2), and a claim that is not there cannot be matched on"
    else
      fact u2-token PASS "the container reached the metadata server and its identity token in full format carries all four claims: ${got} (aud=${aud:-<none>}, iss=${iss:-<none>}${life:+, life=${life}}). The container's own metadata reads agree with them: instance name ${A_VM_NAME:-<unread>}, id ${A_VM_ID:-<unread>}, zone ${A_VM_ZONE:-<unread>}. The token itself is not printed anywhere — it is a bearer credential for that VM"
    fi
  elif [ "$LINES_A" = yes ]; then
    # The container's own lines came back through to 'C830 read', which it prints
    # after its token attempt: it reached that attempt, and no token line came
    # back with it. That is the log channel answering, and it is why this row is
    # U2 answered against the design rather than U2 unanswered.
    fact u2-token FAIL "the probe's container reached the end of its reads — its own lines came back through to 'C830 read' — and not one 'C830 token len=' line came with them: it could not obtain an identity token from the metadata server, or the token could not be minted. Which of the two it was is in the lines that did arrive (the C830 metadata lines are printed before the token). The token is the carrier of D3101, so this is U2 answered against the design and R4 applies — the work returns to you"
  elif [ "$EXIT_READ" = yes ]; then
    # The log channel is empty, so this row answers from the task's exit status
    # and says which claim it is answering: the channel carries a bit per claim
    # set, never a value — what each claim said is only in the log channel, which
    # is why the row names the channel rather than printing the claims.
    if [ "$EXIT_TOKEN" = yes ] && [ "$EXIT_CLAIMS" = yes ]; then
      # This branch is reached only with no line of the container's own, so the
      # instance this row can name is the one the LIST gave, not the one the
      # container named: the row says which reading it is, the way vm-a does.
      local u2_vmsaid
      if [ -n "${A_VM_NAME:-}" ]; then
        u2_vmsaid="the VM this row can name, ${A_VM_NAME} (id ${A_VM_ID:-<unread>}, zone ${A_VM_ZONE:-<unread>}), is the one the instance list gave — the container never named it, and no line of its own arrived to agree with anything here (see vm-a) — so that part of this reading is the weaker one"
      else
        u2_vmsaid="no instance is known for this job at all (see vm-a), so this row says nothing about which VM the token was minted for"
      fi
      fact u2-token PASS "answered by the task's exit status, not by the log channel: the container reached the metadata server and its identity token in full format carried all four claims — google.compute_engine.instance_id, instance_name, project_id and zone — as its own second channel reports (no 'C830 token len=' line came back, so the token's claims cannot be printed here; run the check again once the log channel answers and this row reads them out). For the VM: ${u2_vmsaid}. The token itself is printed nowhere — it is a bearer credential for that VM"
    elif [ "$EXIT_TOKEN" = yes ]; then
      fact u2-token FAIL "answered by the task's exit status, not by the log channel: an identity token in full format WAS obtained, but not all four claims it must carry for D3101 (instance_id, instance_name, project_id, zone) were found in it — the second channel reports that much and the log channel, which would name the missing claim, never answered. U2 is answered against the design and R4 applies: rerun after the log channel answers, or read the token's claims by hand in the job's own log"
    elif [ "$EXIT_REACHED" = yes ]; then
      fact u2-token FAIL "answered by the task's exit status, not by the log channel: the container reached the end of its reads and could NOT obtain an identity token from the metadata server — its second channel reports the missing read and not one 'C830 token len=' line came back through the log channel either, so both channels agree: a task's container on a Batch VM cannot get one, or the token could not be minted. The token is the carrier of D3101, so this is U2 answered against the design and R4 applies — the work returns to you"
    else
      fact u2-token FAIL "neither channel answered: no 'C830 token len=' line came back and the task's exit status says the container did not reach the end of its reads, so whether it could get an identity token is UNKNOWN rather than absent — see probe-exit above, and rerun with the log channel working. R4 applies as soon as it is answered"
    fi
  else
    fact u2-token FAIL "neither channel answered: no 'C830 token len=' line came back, and the task's exit status did not report this container's own readings either (see probe-exit above), so whether it could obtain an identity token is UNKNOWN rather than absent. The first run of this check read this row as answered against the design from exactly this state, and that reading was wrong: a container that never ran says nothing about what a container that runs can reach. The token is the carrier of D3101, so U2 stays open and R4 applies once it is answered — rerun with the log channel working, or read the job's own lines by hand"
  fi
}

# links.py reads the three objects the link question is about — the instance's
# own object, the job's object, and the container's own lines — and prints every
# field that carries one side's identity into the other: the candidates the link
# could be built on, and the keys a second job would have to write to forge
# them. The marker's value is redacted out of anything it prints, because a
# finding that a value leaked must not be the leak.
write_links_py() {
  cat >"$SCRATCH/links.py" <<'C830LINKS'
import json, re, sys

uid, jobname, vmname, vmid, marker = sys.argv[1:6]
vm_path, job_path, lines_path = sys.argv[6:9]
vm = json.load(open(vm_path)) if vm_path else {}
try:
    job = json.load(open(job_path))
except Exception:
    job = {}

needles = [n for n in (uid, jobname) if n]


def redact(v):
    return v.replace(marker, "<marker-value>") if marker else v


def walk(node, path=""):
    out = []
    if isinstance(node, dict):
        for k in sorted(node):
            if path == "" and k == "metadata":
                continue
            out += walk(node[k], path + "." + k if path else k)
    elif isinstance(node, list):
        for i, v in enumerate(node):
            out += walk(v, "%s[%d]" % (path, i))
    elif isinstance(node, str) and node:
        out.append((path, node))
    return out


def leaves(node):
    out = walk(node)
    meta = ((node or {}).get("metadata") or {}).get("items") or []
    for item in meta:
        if isinstance(item, dict) and item.get("value"):
            out.append(("metadata.items.%s" % item.get("key", "?"), str(item["value"])))
    return out


for path, value in leaves(vm):
    if any(n in value for n in needles):
        print("link\tinstance\t%s\t%s" % (path, redact(value)))

for path, value in leaves(job):
    if (vmname and vmname in value) or (vmid and vmid == value):
        print("link\tjob\t%s\t%s" % (path, redact(value)))

try:
    with open(lines_path) as fh:
        for line in fh:
            line = line.strip()
            if not line.startswith("C830 "):
                continue
            body = line[5:]
            if any(n in body for n in needles):
                print("link\tinside\t%s\t%s" % (body.split("=", 1)[0], redact(body)))
except OSError:
    pass

# The fields a second job can be made to carry: a launcher writes its own job's
# labels, the labels Batch puts on its VMs and the task's variables, so every
# such field that carries the other side's identity on either object is a key
# the forge attempt has to try — the job's own object included, because a link
# resting on a field there would rest on a field the launcher wrote too.
for path, value in leaves(vm):
    if not any(n in value for n in needles):
        continue
    if path.startswith("metadata.items."):
        print("forge\t%s\t%s" % (path[len("metadata.items."):], redact(value)))
    elif path.startswith("labels."):
        print("forge\t%s\t%s" % (path[len("labels."):], redact(value)))

for path, value in leaves(job):
    if not path.startswith("labels."):
        continue
    if not ((vmname and vmname in value) or (vmid and vmid == value)):
        continue
    print("forge\t%s\t%s" % (path[len("labels."):], redact(value)))
C830LINKS
}

links_of() { # links_of HOME
  awk -F'\t' -v h="$1" '$1 == "link" && $2 == h { printf "%s=%s ", $3, $4 }' "$SCRATCH/links" 2>/dev/null
}

# link: the Google-made fact itself, and what reading it costs. The row names
# the field, because BR3 is built on the field this prints and O1 is answered by
# where it lives: a field on the instance costs a second read
# (compute.instances.get, which cannot be scoped to Fugaro's VMs), one on the
# job is read with the batch.jobs.get the service already holds.
step_u1_link() {
  write_links_py
  python3 "$SCRATCH/links.py" "$A_UID" "$JOB_A" "${A_VM_NAME:-}" "${A_VM_ID:-}" "$MARKER_VALUE" \
    "$SCRATCH/vm-a.json" "$SCRATCH/job-a.json" "$SCRATCH/lines-a" >"$SCRATCH/links" 2>/dev/null || true
  local inst job inside
  inst=$(links_of instance)
  job=$(links_of job)
  inside=$(links_of inside)
  if [ -n "$inst" ]; then
    u1_link_pass "the instance's own object" "$inst" "an instance field, so reading it costs compute.instances.get on the VM — the second read D3102 asks about (O1), and one that cannot be scoped to Fugaro's VMs: whether it is safe is what u1-forge and u1-handmade below measure"
  elif [ -n "$inside" ]; then
    u1_link_pass "inside the VM only" "$inside" "visible to the container through the metadata server but not in the instance's object, so a service outside the VM cannot read it as it is: the link would have to rest on what the container itself reports (the token's claims) rather than on a read of the instance"
  elif [ -n "$job" ]; then
    u1_link_pass "the job's own object" "$job" "a job field, read with the batch.jobs.get the credentials service already holds (F26): no second read, and O1's question does not arise"
  else
    fact u1-link FAIL "no Google-made fact links this VM to its job: neither the instance's object nor the job's object nor the container's own metadata read carries the job's uid (${A_UID}) or its name (${JOB_A}) on the other side. What the VM and the job share is only the fields this check wrote itself. A link built on anything else — a naming convention, a guess, a launcher-supplied label — is what U1 exists to refuse, and R4 says the work returns to you rather than falling back to K1"
    return 0
  fi
}

u1_link_pass() { # u1_link_pass WHERE PATHS WHY
  fact u1-link PASS "the link is ${1}: $(printf '%s' "$2" | oneline). $(printf '%s' "$3"). The candidate fields are every field of that object whose value carries the other side's identity, which is the whole set the link could be built on"
}
# forge_args.py turns the candidates into the writes a second job would make:
# the label set, the metadata set and the variables, each carrying the probe
# job's identity. A candidate that cannot be written as a label is reported
# rather than dropped — the forge attempt is only as strong as what it tried —
# and the metadata set carries the key unchanged, because metadata keys have no
# label syntax to satisfy.
write_forge_py() {
  cat >"$SCRATCH/forge_args.py" <<'C830FORGEARGS'
import json, re, sys

keys_path, uid, jobname, vmname, vmid = sys.argv[1:6]

pairs = []
with open(keys_path) as fh:
    for line in fh:
        parts = line.rstrip("\n").split("\t")
        if len(parts) >= 2 and parts[0]:
            pairs.append((parts[0], parts[1]))


def safe_key(s):
    # A label KEY starts with a lowercase letter; a key this cannot be written
    # for is reported rather than dropped, because the forge attempt is only as
    # honest as what it tried.
    v = re.sub(r"[^a-z0-9_-]", "_", s.lower())
    v = re.sub(r"^[^a-z]+", "", v)
    return v[:63]


def safe_value(s):
    # A label VALUE may begin with a digit — a job's uid has to be tried as one,
    # and a check that dropped every numeric candidate would not be trying the
    # field the link most likely rests on.
    v = re.sub(r"[^a-z0-9_-]", "_", s.lower())
    v = re.sub(r"^[^a-z0-9]+", "", v)
    return v[:63]


def safe_env(s):
    v = re.sub(r"[^A-Za-z0-9_]", "_", s.upper())
    return v if v[:1].isalpha() else "K_" + v


labels, metadata, env, skipped = {}, {}, {}, []
for key, value in pairs:
    label_key, label_value = safe_key(key), safe_value(value)
    if not label_key:
        skipped.append(key)
    else:
        labels[label_key] = label_value
    if key and value:
        metadata[key] = value
        env[safe_env(key)] = value
for key, value in (("job_uid", uid), ("job_name", jobname), ("instance_name", vmname), ("instance_id", vmid)):
    if value:
        labels.setdefault(key, safe_value(value))
        metadata.setdefault(key, value)
        env.setdefault(safe_env(key), value)

print("labels_json\t" + json.dumps(labels))
print("labels_arg\t" + ",".join("%s=%s" % (k, labels[k]) for k in sorted(labels)))
print("metadata_arg\t" + ",".join("%s=%s" % (k, metadata[k]) for k in sorted(metadata)))
print("env_json\t" + json.dumps(env))
print("skipped\t" + ",".join(skipped))
C830FORGEARGS
}

# forge_compare.py is the judgement: for every field that carries one side's
# identity into the other, did this avenue end up carrying the probe's identity
# in the same place? Each candidate is compared against the artifact its own
# home belongs to — a field of the instance's object against that avenue's VM, a
# field of the job's object against its own job's object, a field the container
# reports against that avenue's container lines — so a link on the job's own
# object, or one only visible inside the VM, is judged rather than silently
# passing with nothing compared. It also reports, per avenue, how many of the
# keys this check wrote on the job reached the VM at all — the control that says
# whether the attempt was live or whether Batch copies a launcher's labels
# nowhere.
write_compare_py() {
  cat >"$SCRATCH/forge_compare.py" <<'C830COMPARE'
import json, re, sys

uid, jobname, vmname, vmid = sys.argv[1:5]
links_path, keys_path = sys.argv[5:7]
specs = sys.argv[7:]
needles = [n for n in (uid, jobname, vmname, vmid) if n]

candidates = []
with open(links_path) as fh:
    for line in fh:
        parts = line.rstrip("\n").split("\t")
        if len(parts) >= 4 and parts[0] == "link":
            candidates.append((parts[1], parts[2]))

keys = []
with open(keys_path) as fh:
    for line in fh:
        parts = line.rstrip("\n").split("\t")
        if len(parts) >= 2 and parts[0]:
            keys.append(parts[0])


def at(obj, path):
    """The value at a path like `status.instances[0].name`, or None when that
    object carries no such field at all."""
    cur = obj
    for part in path.split("."):
        m = re.match(r"^(.*)\[([0-9]+)\]$", part)
        if m:
            key, idx = m.group(1), int(m.group(2))
            if key:
                cur = cur.get(key) if isinstance(cur, dict) else None
            if not isinstance(cur, list) or idx >= len(cur):
                return None
            cur = cur[idx]
        else:
            if not isinstance(cur, dict):
                return None
            cur = cur.get(part)
        if cur is None:
            return None
    if isinstance(cur, str):
        return cur
    if isinstance(cur, (int, float)):
        return str(cur)
    return None


def carries(value):
    return any(n in value for n in needles)


def metadata_value(obj, key):
    for item in ((obj or {}).get("metadata") or {}).get("items") or []:
        if isinstance(item, dict) and item.get("key") == key:
            return str(item.get("value", ""))
    return None


def in_object(path, obj):
    """True: the probe's identity is in that field of this object. False: the
    field is not there, or is there without the identity — either way this
    avenue did not get the probe's identity into it."""
    if path.startswith("metadata.items."):
        value = metadata_value(obj, path[len("metadata.items."):])
    else:
        value = at(obj, path)
    return False if value is None else carries(value)


def in_lines(path, lines):
    """None when this avenue's container reported no such key: the report keys
    are the VM's own, so another VM's keys are a different set and there is
    nothing here to compare."""
    if path not in lines:
        return None
    return carries(lines[path])


def present(key, obj):
    if key in ((obj or {}).get("labels") or {}):
        return True
    return metadata_value(obj, key) is not None


for spec in specs:
    fields = spec.split("\t")
    avenue, artifacts = fields[0], {}
    for field in fields[1:]:
        kind, _, path = field.partition("=")
        if path:
            artifacts[kind] = path
    instances, jobs, lines, unreadable = None, None, {}, []
    for kind, path in sorted(artifacts.items()):
        try:
            with open(path) as fh:
                if kind == "lines":
                    for line in fh:
                        line = line.strip()
                        if line.startswith("C830 "):
                            body = line[5:]
                            lines[body.split("=", 1)[0]] = body
                else:
                    obj = json.load(fh)
                    if kind == "instance":
                        instances = obj
                    else:
                        jobs = obj
        except Exception:
            unreadable.append(path)
    for path in unreadable:
        print("result\t%s\t%s\t%s" % (avenue, path.split("/")[-1], "unreadable"))
    compared = 0
    for home, path in candidates:
        shown = "%s.%s" % (home, path)
        if home == "job":
            v = None if jobs is None else in_object(path, jobs)
        elif home == "inside":
            v = None if not lines else in_lines(path, lines)
        else:
            v = None if instances is None else in_object(path, instances)
        if v is None:
            print("result\t%s\t%s\t%s" % (avenue, shown, "n/a"))
            continue
        compared += 1
        print("result\t%s\t%s\t%s" % (avenue, shown, "reproduced" if v else "not"))
    print("judged\t%s\t%s\t%s" % (avenue, "yes" if compared else "no",
                                  "%d of %d fields compared" % (compared, len(candidates))))
    for key in keys:
        print("control\t%s\t%s\t%s" % (avenue, key, "present" if present(key, instances) else "absent"))
C830COMPARE
}

forge_rows() { # forge_rows AVENUE VERDICT
  awk -F'\t' -v a="$1" -v v="$2" '$1 == "result" && $2 == a && $4 == v { printf "%s ", $3 }' "$SCRATCH/compare" 2>/dev/null
}

# forge_judged: whether this avenue compared anything at all. A row that PASSes
# because its candidates were all unreadable or all belonged to an artifact the
# avenue does not have is a PASS it did not earn, so the caller requires this.
forge_judged() { # forge_judged AVENUE
  awk -F'\t' -v a="$1" '$1 == "judged" && $2 == a && $3 == "yes" { print "yes" }' "$SCRATCH/compare" 2>/dev/null | tail -n 1
}

forge_compared() { # forge_compared AVENUE — for the row's own text
  awk -F'\t' -v a="$1" '$1 == "judged" && $2 == a { print $4 }' "$SCRATCH/compare" 2>/dev/null | tail -n 1
}

forge_control() { # forge_control AVENUE
  printf '%s/%s' \
    "$(awk -F'\t' -v a="$1" '$1 == "control" && $2 == a && $4 == "present"' "$SCRATCH/compare" 2>/dev/null | grep -c . || true)" \
    "$(grep -c . "$SCRATCH/forge.keys" 2>/dev/null || true)"
}
# vm_of_job: the instance a job ran on. The container names it itself out of
# the metadata server, which is the reading that cannot pick the wrong VM; when
# it could not, the instance is the one that appeared between the instance list
# taken before that job's submit and the list now. A single new instance is that
# job's; anything else is ambiguous and is refused rather than guessed at, since
# reading the wrong VM would answer the link question about the wrong machine.
VM_NAME=""
VM_ZONE=""
VM_ID=""
VM_PROBLEM=""
vm_of_job() { # vm_of_job LINES_FILE
  VM_NAME=""
  VM_ZONE=""
  VM_ID=""
  VM_PROBLEM=""
  VM_FROM=""
  VM_NAME=$(sed -n 's|^C830 metadata instance/name=||p' "$1" 2>/dev/null | tail -n 1)
  VM_ID=$(sed -n 's|^C830 metadata instance/id=||p' "$1" 2>/dev/null | tail -n 1)
  VM_ZONE=$(sed -n 's|^C830 metadata instance/zone=||p' "$1" 2>/dev/null | tail -n 1)
  VM_ZONE=${VM_ZONE##*/}
  case "$VM_NAME" in "" | "<unreachable>") VM_NAME="" ;; esac
  if [ -n "$VM_NAME" ] && [ -n "$VM_ZONE" ]; then VM_FROM=container; return 0; fi
  gcloud compute instances list --project="${P:?}" --format='value(name,zone.basename())' >"$SCRATCH/instances.after" 2>/dev/null || true
  local found count
  found=$(python3 - "$SCRATCH/instances.before" "$SCRATCH/instances.after" <<'PY'
import sys
before = set()
with open(sys.argv[1]) as fh:
    for line in fh:
        if line.strip():
            before.add(line.split()[0])
with open(sys.argv[2]) as fh:
    for line in fh:
        parts = line.split()
        if parts and parts[0] not in before:
            print("%s\t%s" % (parts[0], parts[1] if len(parts) > 1 else ""))
PY
)
  count=$(printf '%s' "$found" | grep -c . || true)
  if [ "$count" = 1 ]; then
    VM_NAME=$(printf '%s' "$found" | cut -f1)
    VM_ZONE=$(printf '%s' "$found" | cut -f2)
    VM_FROM=list
    return 0
  fi
  VM_PROBLEM="the container could not name its own VM (its metadata read answered nothing) and the instance list around the submit showed ${count} new instances where exactly one was expected"
  return 1
}

# forge: the second job. It writes the probe job's identity into every field a
# launcher controls — the job's own labels, the labels Batch puts on the VMs,
# and the task's variables — and its VM is then read for any of the fields the
# probe's own VM carries. The red step of BR0 is this step's absence: a script
# that does not try the forge cannot fail this row.
step_forge() {
  if [ -z "$A_VM_NAME" ] && [ -z "$A_VM_ID" ]; then
    fact u1-forge INFO "not taken: the probe's VM was never read (see vm-a above), so there is no instance carrying the probe's identity to compare a forged field against — a second job tried against nothing would read as a PASS it did not earn. The row is answered when vm-a passes"
    fact u1-handmade INFO "not taken: the probe's VM was never read (see vm-a above), so there is no zone to make O8's plain VM in beside it"
    return 0
  fi
  # A forge attempt is only worth making against a link: with no candidate field
  # there is nothing for the comparison to compare, and a row printed from an
  # empty comparison is a PASS the attempt did not earn. u1-link has failed in
  # that case anyway.
  local candidates
  candidates=$(awk -F'\t' '$1 == "link"' "$SCRATCH/links" 2>/dev/null | wc -l | tr -d ' ')
  if [ "${candidates:-0}" = 0 ]; then
    fact u1-forge INFO "not taken: u1-link found no field of the probe's VM, the job's own object or the container's own readings that carries the other side's identity, so there is no link a second job would have to reproduce (see u1-link above)"
    fact u1-handmade INFO "not taken: no link means no field for a plain VM to carry (see u1-link above), and this row is answered when u1-link passes"
    return 0
  fi
  awk -F'\t' '$1 == "forge" { printf "%s\t%s\n", $2, $3 }' "$SCRATCH/links" >"$SCRATCH/forge.keys" 2>/dev/null || : >"$SCRATCH/forge.keys"
  printf 'job_uid\t%s\njob_name\t%s\ninstance_name\t%s\ninstance_id\t%s\n' "$A_UID" "$JOB_A" "$A_VM_NAME" "$A_VM_ID" >>"$SCRATCH/forge.keys"
  sort -u -o "$SCRATCH/forge.keys" "$SCRATCH/forge.keys"
  write_forge_py
  write_compare_py
  # The forging container's life is raised past the wait for its lines exactly as
  # the probe's is: it has to be alive when the fields are read off its VM, and a
  # container that ended before its lines were waited out would have taken the
  # reading with it — which is what the first run of this check cost it.
  PROBE_LIFE_B=$(probe_life_b)
  python3 "$SCRATCH/forge_args.py" "$SCRATCH/forge.keys" "$A_UID" "$JOB_A" "$A_VM_NAME" "$A_VM_ID" "$AUD" "$PROBE_LIFE_B" >"$SCRATCH/forge.args" 2>/dev/null || true
  local labels_arg metadata_arg labels_json env_json skipped labels_b keys_tried
  # Read with awk rather than sed: awk's -F'\t' is a tab on every awk the owner
  # may have, where BSD sed takes `\t` in a pattern for the two characters and
  # would leave every one of these empty, failing this row on the owner's Mac.
  labels_arg=$(awk -F'\t' '$1 == "labels_arg" { print $2 }' "$SCRATCH/forge.args")
  metadata_arg=$(awk -F'\t' '$1 == "metadata_arg" { print $2 }' "$SCRATCH/forge.args")
  labels_json=$(awk -F'\t' '$1 == "labels_json" { print $2 }' "$SCRATCH/forge.args")
  env_json=$(awk -F'\t' '$1 == "env_json" { print $2 }' "$SCRATCH/forge.args")
  skipped=$(awk -F'\t' '$1 == "skipped" { print $2 }' "$SCRATCH/forge.args")
  keys_tried=$(grep -c . "$SCRATCH/forge.keys" 2>/dev/null || true)
  if [ -z "$labels_arg" ]; then
    fact u1-forge FAIL "the forge attempt could not be built: not one candidate key (${keys_tried} of them) could be written as a label, so no second job was submitted and this run does not show that a launcher cannot forge the link"
    return 0
  fi
  labels_b=$(python3 -c 'import json,sys
d = json.loads(sys.argv[1])
d.update(json.loads(sys.argv[2]))
print(json.dumps(d))' "$labels_json" "$(json_obj "$CHK_LABEL=$CHK_VALUE")")
  # The forging job's container gets the life the probe's does, for the same
  # reason: it has to be alive when the fields are read off its VM, and the check
  # waits WAIT_LINES_B for its lines before it reads anything.
  env_json=$(python3 -c 'import json,sys
d = json.loads(sys.argv[1])
d.update(json.loads(sys.argv[2]))
print(json.dumps(d))' "$env_json" "$(json_obj "FUGARO_CHK830_LINGER=$PROBE_LIFE_B")")
  say "submitting the forging job ${JOB_B}: ${keys_tried} keys carrying the probe's identity, into Job.labels, AllocationPolicy.labels and the task's variables"
  mk_params "$SCRATCH/params-b" \
    out="$SCRATCH/job-b.json" \
    image="$IMAGE" \
    script="$SCRATCH/container.sh" \
    machine="$MT" cpu="$CPU_MILLI" mem="$MEMORY_MIB" \
    max_run="$((PROBE_LIFE_B + 300))s" \
    labels="$labels_b" \
    alabels="$labels_b" \
    env="$env_json"
  write_config "$SCRATCH/params-b"
  gcloud compute instances list --project="${P:?}" --format='value(name,zone.basename())' >"$SCRATCH/instances.before" 2>/dev/null || true
  if ! gcloud batch jobs submit "$JOB_B" --project="${P:?}" --location="${L:?}" --config="$SCRATCH/job-b.json" >"$SCRATCH/submit-b.out" 2>"$SCRATCH/err"; then
    local msg
    msg=$(oneline <"$SCRATCH/err")
    fact u1-forge FAIL "the forging job ${JOB_B} could not be submitted (${msg}), so nothing tried to put the probe's identity into a launcher-controlled field and this run does not show the link cannot be forged"
    return 0
  fi
  B_UID=$(job_uid "$JOB_B")
  [ -n "$B_UID" ] || B_UID="$JOB_B"
  wait_for_lines "$B_UID" '^C830 read$' "$SCRATCH/lines-b" "${CHK830_WAIT_LINES_B:-180}" || true
  # The job's own object is read beside its VM: a link that rests on a field of
  # the job's object is a field this launcher wrote too, and the candidate for it
  # is only judged if the object it would be forged on is read.
  gcloud batch jobs describe "$JOB_B" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job-b-own.json" 2>/dev/null || rm -f "$SCRATCH/job-b-own.json"
  if ! vm_of_job "$SCRATCH/lines-b"; then
    fact u1-forge FAIL "${VM_PROBLEM}, so the forging job's VM was not read and this run does not show the link cannot be forged. The forging job carried the probe's uid and name in ${keys_tried} keys; its own object is at $SCRATCH/job-b.json in this run's scratch, which is gone when the run ends — read gcloud batch jobs describe ${JOB_B} --project=${P} --location=${L} if you want to see it"
  elif ! gcloud compute instances describe "$VM_NAME" --project="${P:?}" --zone="${VM_ZONE:?}" --format=json >"$SCRATCH/vm-b.json" 2>"$SCRATCH/err"; then
    fact u1-forge FAIL "the forging job's VM ${VM_NAME} in ${VM_ZONE} could not be read ($(oneline <"$SCRATCH/err")), so the forge attempt was not judged"
  else
    python3 "$SCRATCH/forge_compare.py" "$A_UID" "$JOB_A" "$A_VM_NAME" "$A_VM_ID" "$SCRATCH/links" "$SCRATCH/forge.keys" \
      "job-b"$'\t'"instance=$SCRATCH/vm-b.json"$'\t'"job=$SCRATCH/job-b-own.json"$'\t'"lines=$SCRATCH/lines-b" >"$SCRATCH/compare" 2>/dev/null || true
    local forged control compared unjudged unreadable
    forged=$(forge_rows job-b reproduced)
    control=$(forge_control job-b)
    compared=$(forge_compared job-b)
    if [ -n "$forged" ]; then
      fact u1-forge FAIL "a second job CAN forge the link: ${JOB_B} carries the probe job's identity in $(printf '%s' "$forged" | oneline) — a field a launcher's own submit carries, so it is not Google's own record of which VM belongs to which job. The credentials service must not decide the link from it: a launcher who can name another job's UID there would be handed that job's release (R1, D3108). Record which field it was; BR3 cannot be built on it"
    elif [ -z "$(forge_judged job-b)" ]; then
      unjudged=$(forge_rows job-b n/a)
      unreadable=$(forge_rows job-b unreadable)
      fact u1-forge FAIL "the forge attempt was not judged: ${JOB_B} carried the probe job's uid (${A_UID}) and name (${JOB_A}) in ${keys_tried} launcher-controlled keys — Job.labels, AllocationPolicy.labels and the task's variables — and ${compared}, so none of the fields u1-link named was read off this avenue to compare with them${unjudged:+: $(printf '%s' "$unjudged" | oneline) could not be read off it}${unreadable:+, and $(printf '%s' "$unreadable" | oneline) could not be read at all}. Control: ${control} of those keys are on that VM at all. This sitting therefore does not show the link cannot be forged: give the forging job's container a longer life (CHK830_LINGER_B=<seconds>, which is raised to outlast the wait for its lines) and, if its lines are merely slow to land, a longer wait for them (CHK830_WAIT_LINES_B=<seconds>), or read the fields u1-link named and compare them by hand"
    else
      fact u1-forge PASS "a second job cannot forge the link: ${JOB_B} carried the probe job's uid (${A_UID}) and name (${JOB_A}) in ${keys_tried} launcher-controlled keys — Job.labels, AllocationPolicy.labels and the task's variables — and neither its VM nor its own job object carries the probe's identity in any of the fields the probe's own readings carry it in (${compared}). Control: ${control} of those keys are on that VM at all${skipped:+, and $(printf '%s' "$skipped" | oneline) could not be written as a label}"
    fi
  fi
  gcloud batch jobs delete "$JOB_B" --project="${P:?}" --location="${L:?}" --quiet >/dev/null 2>&1 || true
  if [ -n "${A_VM_ZONE:-}" ]; then
    step_handmade "$labels_arg" "$metadata_arg" "$keys_tried"
  else
    fact u1-handmade INFO "O8's plain VM was not tried: the probe's own VM was never read (see vm-a above), so there is no zone to make one in beside it. The run answers this row when the probe's VM reads"
  fi
}

# refused_field MESSAGE: the field a Compute refusal is about, as
# `<home><TAB><key>` with home `labels` or `metadata`, when the message names
# one of the fields this check wrote — `resource.labels.batch-job-uid`,
# `resource.metadata.job_uid`. Empty otherwise, a metadata *item*
# (`resource.metadata.items[0].key`) included: that names a position in a list,
# not a key, and a second attempt built on a guess at which key it was would
# drop a field for no reason the platform gave.
refused_field() { # refused_field MESSAGE
  printf '%s\n' "$1" | awk '
    {
      if (match($0, /resource[.]metadata[.]items\[/)) exit
      if (match($0, /resource[.]labels[.][A-Za-z0-9_-]+/)) {
        s = substr($0, RSTART, RLENGTH)
        sub(/^resource[.]labels[.]/, "", s)
        print "labels\t" s
        exit
      }
      if (match($0, /resource[.]metadata[.][A-Za-z0-9_-]+/)) {
        s = substr($0, RSTART, RLENGTH)
        sub(/^resource[.]metadata[.]/, "", s)
        print "metadata\t" s
        exit
      }
    }'
}

# kv_without LIST DROP: the `k=v` list without the pair whose key is DROP. The
# second attempt of O8's plain VM is built with this, so that the one field
# Compute named is the only one left out — every other field the first attempt
# carried is still written, and the VM is read with all of them.
kv_without() { # kv_without LIST DROP
  printf '%s\n' "$1" | awk -F, -v drop="$2" '
    {
      out = 0
      for (i = 1; i <= NF; i++) {
        eq = index($i, "=")
        if (eq < 2) continue
        if (substr($i, 1, eq - 1) == drop) continue
        printf "%s%s", (out++ ? "," : ""), $i
      }
    }'
}

# handmade: O8's plain Compute Engine VM — the one someone with
# compute.instances.create and actAs on a run account could make by hand. It is
# named after the probe job and labelled with the probe job's identity, which is
# the whole of what that pair buys: if a field the link could rest on can be put
# on a VM by hand, the link cannot rest on it. It is deleted as soon as it has
# been read, so it is alive for seconds.
step_handmade() { # step_handmade LABELS META KEYS_TRIED
  local labels=$1 metadata=$2 keys_tried=$3
  local note=""
  say "creating a plain VM named after the probe job (${HANDMADE_VM}) for O8"
  if ! gcloud compute instances create "$HANDMADE_VM" --project="${P:?}" --zone="${A_VM_ZONE:?}" \
    --machine-type="$MT" --image-family=debian-12 --image-project=debian-cloud --no-address \
    --labels="$labels" --metadata="$metadata" >"$SCRATCH/err" 2>&1; then
    local msg refused rhome rdrop refused_shown retry_labels retry_metadata
    msg=$(oneline <"$SCRATCH/err")
    refused=$(refused_field "$msg")
    rhome=""
    rdrop=""
    IFS=$'\t' read -r rhome rdrop <<<"$refused"
    if [ -z "$rhome" ]; then
      fact u1-handmade FAIL "O8's plain VM: the first attempt, carrying every one of the ${keys_tried} keys, was refused by Compute (${msg}) and the refusal names no field this check wrote, so there is nothing to leave out of a second attempt that would still carry the rest: this sitting does not say which of the fields u1-link named a hand-made VM can carry. Read the message against those fields by hand before BR3 rests on one"
      return 0
    fi
    refused_shown="${rhome}.${rdrop}"
    # Only the field Compute named is left out of the second attempt: the whole
    # metadata set is still written — Compute refuses label keys, and a metadata
    # item it did not name is a field a hand-made VM is free to carry, which is
    # what this row is asking about — and every label but the refused one. A
    # field that is never written is a field that was never tried, so nothing
    # else may be dropped, and a refusal that names no field ends the row above
    # rather than guessing at one.
    retry_labels="$labels"
    retry_metadata="$metadata"
    case "$rhome" in
    labels) retry_labels=$(kv_without "$labels" "$rdrop") ;;
    metadata) retry_metadata=$(kv_without "$metadata" "$rdrop") ;;
    esac
    if gcloud compute instances create "$HANDMADE_VM" --project="${P:?}" --zone="${A_VM_ZONE:?}" \
      --machine-type="$MT" --image-family=debian-12 --image-project=debian-cloud --no-address \
      --labels="${CHK_LABEL}=${CHK_VALUE}${retry_labels:+,$retry_labels}" --metadata="$retry_metadata" >"$SCRATCH/err" 2>&1; then
      # The refusal is part of this row's answer, so it belongs in the row's own
      # sentence: a fact recorded beside the verdict would be what a reader of
      # the block sees instead of the verdict.
      note="the first attempt, carrying every one of the ${keys_tried} keys, was refused by Compute (${msg}) — a field Compute will not let a hand-made VM carry cannot be forged that way — and ${refused_shown} is the only field the second attempt left out: every other field the first attempt carried was written to the VM this row is about, and is read below; "
    else
      fact u1-handmade INFO "O8's plain VM could not be created in ${A_VM_ZONE} (${msg}), so this sitting does not say whether a hand-made VM can carry the link's field. The keys it would have carried were ${keys_tried}; a quota or a default-network problem in ${A_VM_ZONE} is the innocent reading, and the run of this check on a quiet project answers it"
      return 0
    fi
  fi
  HANDMADE_MADE=$HANDMADE_VM
  if ! gcloud compute instances describe "$HANDMADE_VM" --project="${P:?}" --zone="${A_VM_ZONE:?}" --format=json >"$SCRATCH/vm-handmade.json" 2>"$SCRATCH/err"; then
    fact u1-handmade FAIL "${note}O8's plain VM was created but could not be read ($(oneline <"$SCRATCH/err")): the row the plan asked this check for is unmeasured"
  else
    python3 "$SCRATCH/forge_compare.py" "$A_UID" "$JOB_A" "$A_VM_NAME" "$A_VM_ID" "$SCRATCH/links" "$SCRATCH/forge.keys" \
      "handmade"$'\t'"instance=$SCRATCH/vm-handmade.json" >>"$SCRATCH/compare" 2>/dev/null || true
    local forged control compared unjudged
    forged=$(forge_rows handmade reproduced)
    control=$(forge_control handmade)
    compared=$(forge_compared handmade)
    if [ -n "$forged" ]; then
      fact u1-handmade FAIL "${note}a hand-made VM CAN carry the link's field: a plain instance named after the probe job carries the probe job's identity in $(printf '%s' "$forged" | oneline), a field the probe's own VM carries it in — so that field is written by whoever creates the machine and not by Batch's control plane. Anyone holding compute.instances.create and actAs on a run account (O8) makes one by hand, and the link must not rest on it, whatever the job it names"
    elif [ -z "$(forge_judged handmade)" ]; then
      unjudged=$(forge_rows handmade n/a)
      fact u1-handmade FAIL "${note}O8's plain VM was made and read, but the attempt was not judged: ${compared}${unjudged:+ — $(printf '%s' "$unjudged" | oneline) could not be read off a plain VM, since the link u1-link named lives on another side of it}. What a plain VM carries is its own object alone, so this sitting does not answer O8 for the fields u1-link named: read them against a VM made by hand if they are the fields BR3 is to rest on"
    else
      fact u1-handmade PASS "${note}a hand-made VM cannot carry the link's field: a plain instance named after the probe job carries none of the fields the probe's VM carries the probe's identity in (${compared}; control: ${control} of those keys are on it at all). What the link can rest on is therefore written by Batch's own control plane, not by whoever creates the machine"
    fi
  fi
  gcloud compute instances delete "$HANDMADE_VM" --project="${P:?}" --zone="${A_VM_ZONE:?}" --quiet >/dev/null 2>&1 || true
  if gcloud compute instances describe "$HANDMADE_VM" --project="${P:?}" --zone="${A_VM_ZONE:?}" >/dev/null 2>&1; then
    say "the hand-made VM ${HANDMADE_VM} is still there; the restore row names it"
  fi
}
# u3: where a job's variables reach. The value is a stand-in and is never
# printed; the readings are named, and each is either taken (and saw nothing) or
# not taken (and then this row FAILS rather than concluding absence). The
# positive control comes first: the container reports whether the variable
# reached its own environment, because a reading that found nothing is only
# worth something if the job carried the value at all.
step_u3() {
  local control=no control_from="the container's own line"
  grep -qx 'C830 env-marker present' "$SCRATCH/lines-a" 2>/dev/null && control=yes
  if [ "$control" = no ] && [ "$EXIT_MARKER" = yes ]; then
    control=yes
    control_from="the task's exit status, since no line of the container's own came back (see probe-exit above)"
  fi
  local inside_read=taken
  [ "$LINES_A" = yes ] || inside_read="NOT taken: the container's own readings never arrived (see probe-ran above)"
  project_log_text "labels.job_uid=\"$A_UID\"" "$SCRATCH/log-job.json"
  project_log_text "\"$MARKER_VALUE\"" "$SCRATCH/log-value.json"
  local waited=0 audit=not
  while :; do
    project_log_text 'protoPayload.methodName="google.cloud.batch.v1.BatchService.CreateJob"' "$SCRATCH/audit.json"
    # The record waited for here is this run's own, and the uid is what tells it
    # apart: the job's name is fixed, so a record from a run in the last hour —
    # the run the FAIL texts below ask the owner to make, on the very failure
    # this loop is waiting out — carries the same name and would be read as this
    # run's record before this run's has arrived. A Batch uid is new with every
    # job creation, so it is the one thing on the record that is this run's
    # alone; absence is established only for a record that was read.
    if grep -qF "$A_UID" "$SCRATCH/audit.json" 2>/dev/null; then
      audit=yes
      break
    fi
    [ "$waited" -ge "$WAIT_AUDIT" ] && break
    sleep 10
    waited=$((waited + 10))
    say "waiting for this run's CreateJob audit record (uid ${A_UID}) (${waited}s of ${WAIT_AUDIT}s)"
  done
  local seen=""
  grep -qF "$MARKER_VALUE" "$SCRATCH/log-job.json" 2>/dev/null && seen="${seen}the probe job's own log entries (all logs, selected by its uid), "
  grep -qF "$MARKER_VALUE" "$SCRATCH/log-value.json" 2>/dev/null && seen="${seen}Cloud Logging anywhere in ${P} in the last hour, "
  grep -qF "$MARKER_VALUE" "$SCRATCH/lines-a" 2>/dev/null && seen="${seen}the instance metadata as the container itself read it, "
  grep -qF "$MARKER_VALUE" "$SCRATCH/vm-a.json" 2>/dev/null && seen="${seen}the instance's own object, "
  grep -qF "$MARKER_VALUE" "$SCRATCH/audit.json" 2>/dev/null && seen="${seen}the CreateJob audit record read here (this run's own, selected by uid), "
  local entries
  entries=$(python3 -c 'import json,sys
try:
    print(len(json.load(open(sys.argv[1]))))
except Exception:
    print("unreadable")' "$SCRATCH/log-job.json" 2>/dev/null || echo unreadable)
  if [ "$control" != yes ]; then
    fact u3-marker FAIL "the marker variable ${MARKER_NAME} did not reach the probe's container — neither its own environment control nor the task's exit status reports it — so no reading here establishes where a job's variables go, and the value was not carried by the job at all: the run is void on this row rather than a measurement. Readings taken anyway: ${entries} log entries for the job, the project-wide search, the instance metadata (inside: ${inside_read}), and the CreateJob audit record ${audit}"
  elif [ -n "$seen" ]; then
    fact u3-marker FAIL "the marker variable ${MARKER_NAME}'s stand-in value APPEARS in ${seen}which is the finding this row exists for: a variable on a Batch job is published there, so the wrapped blob of R5 would be published too (F3, R7). Nothing may carry the key as a job variable while this holds, and the value is not printed in this block on purpose — read it out of the job's own object if you need to see it: gcloud batch jobs describe ${JOB_A} --project=${P} --location=${L}"
  elif [ "$audit" != yes ]; then
    fact u3-marker FAIL "the marker variable ${MARKER_NAME}'s stand-in value reached the container (the control, read from ${control_from}) and was not found in any reading that WAS taken — ${entries} log entries for the probe job, the project-wide search, the instance metadata (inside: ${inside_read}) — but this run's own CreateJob audit record (the one carrying the uid ${A_UID}) could NOT be read within ${WAIT_AUDIT}s, so absence was not established for it: a record this check never saw is not a record that says no. A record naming ${JOB_A} but not this run's uid is another run's and is not read as this one's — job names are reused, uids are not. If the CreateJob records in ${P} carry no uid at all, this row stays fail-closed on purpose: read the record for this run by hand (gcloud logging read 'protoPayload.methodName="google.cloud.batch.v1.BatchService.CreateJob"' --freshness=1h) and check its own timestamp against the job's createTime (${A_CREATED:-<unread>}). Give it longer (CHK830_WAIT_AUDIT=<seconds>) and run again, and check that Admin Activity records for batch.googleapis.com are on in ${P}. Nothing may rest on the marker until this is answered (R7, F3)"
  elif [ "$LINES_A" != yes ]; then
    fact u3-marker PASS "answered by the task's exit status, not by the log channel — the channel that failed is named here rather than hidden: the control reads present (the job did carry the marker variable, read from ${control_from}), and the value appears in none of the readings the check itself takes — not in the probe job's own log entries (${entries} of them, selected by its uid across every log), not in this run's CreateJob audit record (which was found), not in the instance metadata as the container read it (${inside_read}), and nowhere in ${P}'s Cloud Logging in the last hour — the project-wide search, which is the reading that would have caught the value in the job's own lines had there been any. The container's own lines never arrived (see probe-diag for why), so this row rests on the second channel for the control and on the check's own reads for the absence; rerun once the log channel answers and it is measured whole. R5 may not carry the key as a job variable until this row is PASS on the log channel alone (R7, F3)"
  else
    fact u3-marker PASS "the marker variable ${MARKER_NAME}'s stand-in value reached the container's own environment (the control, read from ${control_from}, so the job carried it) and appears in none of the readings: neither in the probe job's own log entries (${entries} of them, selected by its uid across every log), nor anywhere in ${P}'s Cloud Logging in the last hour, nor in the instance metadata as the container read it or in the instance's own object, nor in the CreateJob audit record of this run (the one carrying the uid ${A_UID}, which was found)"
  fi
}

# u8: how soon a deleted job's name can be used again. The job is deleted, the
# same name is submitted again, and the seconds to acceptance are measured —
# with the refusals' own message, since a name that comes back slowly comes back
# with something to say. The re-created job is deleted at once: it exists to
# hold a name, not to run.
step_u8() {
  say "deleting the probe job ${JOB_A} and measuring how soon its name can be used again"
  if ! gcloud batch jobs delete "$JOB_A" --project="${P:?}" --location="${L:?}" --quiet >/dev/null 2>"$SCRATCH/err"; then
    fact u8-name FAIL "the probe job ${JOB_A} could not be deleted ($(oneline <"$SCRATCH/err")), so the reuse of its name was not measured, and the job is STILL THERE: gcloud batch jobs delete ${JOB_A} --project=${P} --location=${L} --quiet"
    return 0
  fi
  mk_params "$SCRATCH/params-reuse" \
    out="$SCRATCH/job-reuse.json" \
    image="$IMAGE" \
    script="" \
    machine="$MT" cpu="$CPU_MILLI" mem="$MEMORY_MIB" \
    max_run="300s" \
    labels="$(json_obj "$CHK_LABEL=$CHK_VALUE")" \
    alabels="$(json_obj "$CHK_LABEL=$CHK_VALUE")"
  write_config "$SCRATCH/params-reuse"
  local start now elapsed=0 attempts=0 first_msg="" ok=no new_uid=""
  start=$(date +%s)
  while :; do
    attempts=$((attempts + 1))
    if gcloud batch jobs submit "$JOB_A" --project="${P:?}" --location="${L:?}" --config="$SCRATCH/job-reuse.json" >/dev/null 2>"$SCRATCH/err"; then
      ok=yes
      break
    fi
    [ -n "$first_msg" ] || first_msg=$(oneline <"$SCRATCH/err")
    now=$(date +%s)
    elapsed=$((now - start))
    [ "$elapsed" -ge "$REUSE_BOUND" ] && break
    say "the name ${JOB_A} is still refused after ${elapsed}s"
    sleep "$REUSE_POLL"
  done
  now=$(date +%s)
  elapsed=$((now - start))
  if [ "$ok" = yes ]; then
    new_uid=$(job_uid "$JOB_A")
    gcloud batch jobs delete "$JOB_A" --project="${P:?}" --location="${L:?}" --quiet >/dev/null 2>&1 || true
    fact u8-name PASS "a deleted job's name can be used again: after deleting ${JOB_A}, the same name was accepted again ${elapsed}s later (attempt ${attempts} of this loop; the first refusal read: ${first_msg:-none}), and the re-created job (uid ${new_uid:-<unread>}) was deleted at once — it existed to hold the name, not to run. The registration of D3108 does not depend on this answer"
  else
    fact u8-name FAIL "the name ${JOB_A} was still refused ${elapsed}s after the job was deleted, over ${attempts} attempts, so how soon it can be used again was NOT measured within the bound: increase it (CHK830_REUSE_BOUND=<seconds>) and run again, or record this as the answer for the sitting. The last refusal read: ${first_msg:-<none>}"
  fi
}
# ------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. A real run would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the tree this script lives in, and the project refusal"
  say "  [local]  the tree reads: the log filter out of internal/backend/batch/logs.go, the managed label pair out of internal/backend/gcp/names.go, and the wrapped variable's name out of docs/batch-run-identity/"
  say "  [cloud]  gcloud compute instances list — the instance list before the probe, which is how a VM is found if the container cannot name its own"
  say "  [cloud]  gcloud batch jobs submit ${JOB_A} — ONE job (${MT}, ${CPU_MILLI}m/${MEMORY_MIB}MiB, container alive $(probe_life_a)s after its reads) carrying ${CHK_LABEL}=${CHK_VALUE}, the managed label, the marker variable ${MARKER_NAME} with a generated stand-in value, logs to CLOUD_LOGGING and no retry"
  say "  [cloud]  gcloud batch jobs describe ${JOB_A} — its uid, state and create time"
  say "  [cloud]  gcloud logging read (labels.job_uid=\"<uid>\" AND logName=\"${TASK_LOG}\") until the container's own 'C830 read' line arrives — the container's metadata reads and its token claims (never the token itself), bounded at ${WAIT_LINES}s"
  say "  [cloud]  ONLY IF no line arrived: gcloud batch jobs describe ${JOB_A} (the job's own state and status events, each task's state and its events), gcloud compute instances get-serial-port-output <the VM> (its last 40 lines, scanned so no value that looks like a token is printed), the same log query under resource.type=\"batch.googleapis.com/Job\" by the uid, and a bounded wait for the task to end — its exit status is the container's second channel, read with the batch.jobs.get the service already makes (probe-diag, probe-exit)"
  say "  [cloud]  gcloud compute instances describe <the VM the container named> — its whole object, which is what the link is judged on"
  say "  [local]  the link walk: every field of the instance, of the job and of the container's own metadata read that carries the other side's identity (U1)"
  say "  [cloud]  gcloud batch jobs submit ${JOB_B} — a second job with the probe job's uid and name in every launcher-controlled field it has: Job.labels, AllocationPolicy.labels and the task's variables"
  say "  [cloud]  gcloud logging read + gcloud compute instances describe <the second job's VM> — the forge attempt, judged field by field against the first (U1)"
  say "  [cloud]  gcloud compute instances create ${HANDMADE_VM} — a plain VM named after the probe job, for O8, read and deleted at once"
  say "  [cloud]  gcloud logging read, three times — the job's own log entries, a search of the whole project for the marker's value, and the CreateJob audit record (waited for, bounded at ${WAIT_AUDIT}s). Its value is compared and never printed (U3)"
  say "  [cloud]  gcloud batch jobs delete ${JOB_A}, then gcloud batch jobs submit ${JOB_A} until the name is accepted — the seconds to reuse, bounded at ${REUSE_BOUND}s (U8), and the re-created job is deleted at once"
  say "  [local]  the exit trap deletes every job of this check's by its label, and the scratch directory"
  say "A dry run runs the guards and the tree reads only. It submits nothing, reads no cloud project, and creates no VM"
}

# ------------------------------------------------------------------- reporting
BLOCK_KEYS=(xtrace owner tree belong probe-submitted probe-ran probe-diag probe-exit vm-a u1-link u1-forge u1-handmade u2-token u3-marker u8-name restore)

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
  printf '\nCHECK 830 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    # A key's FAIL is what the block's own last line is read from, so a key that
    # recorded a FAIL beside anything else is printed as that FAIL: the first
    # fact of a key wins only where it is not hiding a FAIL behind it.
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|FAIL|" || true)
    [ -n "$line" ] || line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
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
  printf 'CHECK 830 RESULT: %s\n' "$(verdict_of "$facts")" >&3
}

step_manual() {
  MANUAL_LINE="paste this block into docs/gcp-live-checklist.md's Check 830 section, with the date and \`fugaro version\`: the link field \`u1-link\` named and the read it costs (that is the owner's answer to O1), whether a second job and a hand-made VM can put another job's identity there, the four claims the identity token carries, whether the marker value reached any of the three readings, and the seconds a deleted job's name took to come back. BR3 starts only after this is recorded (R4); if \`u1-link\` is FAIL, or \`u1-forge\` is FAIL, the work returns to you rather than falling back to K1"
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
USAGE="check-830.sh [--dry-run]"

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

# marker_value: the stand-in a job carries in the wrapped variable's place. It
# is generated per run so a reading that finds it cannot be a reading that found
# an older run's value, and it is a marker rather than a key — nothing here
# derives it from anything.
marker_value() {
  python3 -c 'import secrets
print("c830-" + secrets.token_hex(8))' 2>/dev/null || printf 'c830-%s' "$$"
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
  guard_belong
  MARKER_VALUE=${CHK830_MARKER_VALUE:-$(marker_value)}
  write_container
  write_diag_py

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "guards and the tree reads only: the commands above were printed, not run, and no cloud project, no credential and no resource was touched"
    fact probe-submitted INFO "not taken: this is a dry run, so no probe job was submitted and no instance list was taken"
    fact probe-ran INFO "not taken: this is a dry run, so no container ran and no line of its own was read"
    fact probe-diag INFO "not taken: this is a dry run, so nothing was read of the job's own state, its events or the VM's serial console"
    fact probe-exit INFO "not taken: this is a dry run, so no task end was waited for and no exit status was read"
    fact vm-a INFO "not taken: this is a dry run, so no instance was described"
    fact u1-link INFO "not taken: this is a dry run, so no field was searched for the other side's identity"
    fact u1-forge INFO "not taken: this is a dry run, so no second job was submitted and nothing was forged"
    fact u1-handmade INFO "not taken: this is a dry run, so no plain VM was created for O8"
    fact u2-token INFO "not taken: this is a dry run, so no identity token was asked for"
    fact u3-marker INFO "not taken: this is a dry run, so no log, no instance metadata and no audit record was read"
    fact u8-name INFO "not taken: this is a dry run, so no job was deleted and no name reused"
  else
    step_submit_a
    step_observe_a
    step_probe_diag
    step_probe_exit
    step_u2_token
    step_u1_link
    step_forge
    step_u3
    step_u8
  fi
  step_manual

  REACHED_END=yes
  if grep -qE '^[a-z0-9-]+\|FAIL\|' "$FACTS_FILE"; then
    exit 1
  fi
  return 0
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
