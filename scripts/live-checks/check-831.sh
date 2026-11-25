#!/usr/bin/env bash
# Check 831 — a Batch run's identity, end to end, as ONE script YOU run. It is
# BR13 of docs/batch-run-identity: the proof the plan's Check 831 row asks for
# (R11, D3108, D3115, F30).
#
# It launches a real sandbox run on Batch, reads that the run OPENED ITS SESSION
# and was METERED, and then — as a SECOND LAUNCHER, an account that may read and
# write Batch jobs in the project but is not the run's own job — tries to obtain
# that run's identity FOUR WAYS. It launches a SECOND run for the third of them,
# and deletes that one's job while it is still queued: that run never comes up,
# so it never claims its ticket and never spends. It prints one PASS or FAIL per
# reading:
#
#   launch, job-found, session, metered
#          One `fugaro run --repo <sandbox> --task-file … --json` in `fugaro-dev`,
#          the Batch job it created, the ledger node that says the run opened its
#          session (reserved, exp) and the one that says it was metered (spent,
#          tokens). The job is found by the name the launch answered, and held to
#          the run by its own FUGARO_RUN variable — a name is a launcher's word
#          and the check does not rest on one.
#
#   launch-ticket
#          Whether the launcher put the run's identity ticket on the job
#          (identitywire.TicketEnv, read out of the tree). Nothing can be
#          replayed from a job that carries no ticket, so this row is the one
#          the three attacks below stand on.
#
#   ticket-printed
#          Where Batch PRINTS that ticket: in the job's own document, in the
#          job's task logs and in the `CreateJob` audit record. D3115's rule is
#          that the ticket is a handle and not a secret — it is a job variable,
#          so it is readable — and this row measures the rule rather than
#          assuming it: it PASSES only when every reading was taken, and it names
#          which of them carried the ticket. A ticket in the logs is expected
#          and is not a FAIL.
#
#   attack-replay
#          The second launcher READS the run's job, takes the ticket out of it,
#          and runs a job of its own carrying that ticket, which claims from its
#          own VM. The attestation is the attack's own VM's identity token in
#          full format, so what refuses it can only be the VM-to-job link
#          (D3114, F4). PASS: no identity came back.
#
#   attack-noticket
#          The same job with no ticket at all: the claim must be refused on the
#          ticket and not on the VM.
#
#   attack-rename
#          The second launcher DELETES a job while it is still QUEUED and
#          RE-CREATES one under the same name carrying that run's ticket, which
#          claims from its own VM. The job is a SECOND LAUNCH's — a run launched
#          for this attack and read before its VM ever came up — because the
#          queued window is the whole attack: the run whose identity the other
#          rows read has been metered by the time this check reaches here, and
#          its own bootstrap has spent its ticket by then, so a re-creation
#          carrying THAT ticket would be refused for being spent and prove
#          nothing about the record. This one's ticket is unspent and the name is
#          the same, so nothing but the record's job id is left to refuse it: the
#          re-created job is another job with another Google id (D3108, F30). The
#          row FAILS when the job was not QUEUED when it was deleted, because the
#          window had passed and the attack, as the plan asks it, was not made.
#
#   attack-record
#          The second launcher reads the issuance record DIRECTLY, at the
#          Realtime Database path the tree builds for it
#          (identitywire.PathRecord). The database's own rules are what must
#          refuse that read.
#
#   cost, restore
#          The run's own spend and tokens out of its ledger, and the fact that
#          every job this check made is deleted: its own attack jobs by their
#          label, the two runs' jobs by the label the backend writes on them, and
#          the re-created one by the name it holds.
#
# **What one sitting can and cannot show, stated up front.** This is a proof of
# the mechanism as the tree under test implements it, not of the design: every
# row reports what the service, the database and the platform DECIDED, and a
# refusal is only counted when the answer carries the contract's own refusal code
# (`identity_refused`, `caller_not_admitted`, `no_launch_scope`, `not_served`) or
# the database's own refusal status. An attack whose claim was answered 5xx, or
# not answered at all, was NOT judged and FAILS as unmeasured rather than passing
# on an outage. The check is written for the world the plan's Check 831 row names
# — after the release, on an updated `fugaro-dev`, where the launcher writes the
# ticket and the claim route checks the attestation — so on a tree that has not
# landed those, `launch-ticket`, `attack-replay` and `attack-rename` FAIL, and
# that is the check doing its job: a row that says the mechanism was not there
# when it was measured.
#
# **The ticket never reaches the block.** It is a handle rather than a secret
# (D3115), but a block that printed one would be a block that had to be treated
# as secret: it is written to a file in the 0700 scratch directory (mode 0600),
# compared with it, and printed nowhere — not in a fact, not in the progress log,
# not on a command line. The readings report where it was FOUND, never its value.
#
# Usage:      CHK831_OWNER_RUN=yes bash scripts/live-checks/check-831.sh \
#               --second-launcher <account> [--repo owner/name]
#             From the Fugaro checkout this script lives in: it is the tree the
#             ticket's name, the claim's route and the record's path are read out
#             of. It does not need the sandbox checkout and it touches no
#             repository of yours by hand.
#             --second-launcher is REQUIRED: the account the four attacks are
#             made as, impersonated with `gcloud --impersonate-service-account`
#             (it must hold roles/batch.jobsEditor on the project and
#             iam.serviceAccountUser on the run accounts, F4; your own account
#             must be allowed to impersonate it).
#             --repo names the sandbox repository the run is launched in, as
#             owner/name; FUGARO_LIVE_REPO is read as the fallback. One of the
#             two is REQUIRED: this check never guesses a repository to launch a
#             run in.
#             --dry-run runs the guards and the tree reads, prints every command
#             a real run would run, and touches NO cloud project.
# Needs:      bash, mktemp, git, python3, curl and gcloud on PATH, an
#             authenticated gcloud that may impersonate the second launcher, and
#             `fugaro` installed and configured for the installation. Nothing is
#             installed and nothing is fetched.
# Where:      GCP project `fugaro-dev` (hard-coded, never read from gcloud and
#             never `belong`, EdgeWeb or EdgeServer) and region `us-east5`. The
#             resources it makes are all labelled `fugaro_chk=831`: three attack
#             jobs, plus the run's own job and the one re-created under the
#             second launch's name.
# Costs:      TWO real sandbox runs — the run whose identity the four attacks are
#             about (small: its job is deleted once the attacks that read it have
#             read it, and `CHK831_MAX_USD` caps it at $1 by default) and the
#             second launch the third attack deletes while its job is still
#             queued, which never comes up and so spends nothing — and three
#             small Batch jobs on e2-standard-4 (2000m/4096MiB), each alive for
#             its linger (60s by default). No image is built, and no model call is
#             made by this check itself.
# Runs:       by YOU. No agent runs any step of it: it launches two runs and it
#             makes four attacks on a live service with a second account.
# Cleanup:    every job it made is deleted — the run's, the second launch's, the
#             three attacks' and the re-created one — and the scratch directory
#             always goes, including on a Ctrl-C. The `restore` row names anything
#             an interrupted run left behind, with the one command that removes
#             it. The runs' own records in the runs bucket are the runs' and stay:
#             they are what `fugaro runs ls` reads.
# Output:     progress on stderr, ONE block on stdout headed `CHECK 831 RESULT`,
#             whose last line is `CHECK 831 RESULT: PASS|FAIL`. Exit 0 when
#             every row passed, 1 when one FAILed, 2 when the run could not tell
#             (a guard stopped it, a tool is missing, a read did not answer). An
#             interrupt exits 130.
#
# **Paste the whole block into the register**, `docs/gcp-live-checklist.md`'s
# Check 831 section, with the date and `fugaro version`.
#
# There is no `--project` flag and no project variable: the project is
# hard-coded, every `gcloud` command carries `--project=${P:?}`, and a container
# never reads the cloud. The script's own test
# (scripts/live-checks/check-831_test.sh) drives it entirely through fakes on
# PATH, with no network and no cloud project at all.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace would print the
# commands into the log an owner pastes back, and this script's commands carry
# the project, the job names and the ticket. Check 130 spells the three parts
# out; this is the same guard.
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
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the run's identity or the four attacks" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# oneline collapses a command's output into one line, because a fact is one line
# of the block and a multi-line detail would read as facts that are not there.
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }

# ----------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 831 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 831 needs bash"
  need mktemp
  need git "the checkout this script lives in is read for the ticket's variable, the claim's route and the record's path"
  need python3 "the job objects, the attack job configurations, the ledger node and the claim's body are built and parsed by one python3 program each, so the block carries fields rather than documents"
  need curl "the run's ledger and the issuance record are read out of the Realtime Database with it"
  need gcloud "the launch, the jobs, the logs, the audit record and the impersonated reads are all made with it"
  need fugaro "the run under test is launched with it"
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk831-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""
# What this run made, so the exit trap can delete it and the restore row can
# name it. CREATED_ANYTHING gates the sweep: a run a guard stopped made nothing
# and must not call gcloud at all.
CREATED_ANYTHING=no
LEFT_BEHIND=""
# The run: its id, the repository, the job's name, uid, state and the account it
# runs as, and the ticket (in a file of its own, never in a variable that could
# reach a fact). All of them are empty until a step reads them, and every row
# that reads one says "unread" or reports the row as not measured rather than
# stopping the whole run.
RUN_ID=""
RUN_JOB=""
RUN_SLUG=""
RUN_UID=""
RUN_STATE=""
RUN_ACCOUNT=""
RUN_RTDB=""
RUN_SERVICE=""
TICKET_FILE=""
TICKET_PRESENT=no
# The second launch of this check: the run whose job the third attack deletes
# while it is still queued. Its ticket lives in a file of its own in the scratch
# directory and never in a variable that could reach a fact.
RENAME_RUN_ID=""
RENAME_JOB=""
RENAME_UID=""
# The second launcher, and the jobs the attacks made.
SECOND=""
SUBMITS=0
LEDGER_READ=no
LEDGER_RESERVED=""
LEDGER_EXP=""
LEDGER_SPENT=""
LEDGER_TOKENS=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk831.XXXXXXXX") || die "Check 831 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  TICKET_FILE="$SCRATCH/ticket"
  : >"$TICKET_FILE"
  chmod 600 "$TICKET_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 831 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  CLEANUP_RUNNING=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  sweep_leftovers
  remove_scratch
}

# jobs_of_mine: this check's own jobs, by its own label. Every delete and every
# leftover read goes through it, so a resource made by a run that died is found
# again by the next one.
jobs_of_mine() {
  gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
    --filter="labels.${CHK_LABEL}=${CHK_VALUE}" --format='value(name.basename())' 2>/dev/null || true
}

# jobs_of_run: the jobs the backend labels as this run's, by the run id. This is
# the net under a run whose job the check never found: the label is the
# backend's own (internal/backend/batch/names.go), so it is there whether or not
# this check read the launch's answer. It takes one or more run ids and defaults
# to the run this check is about, so the sweep covers the second launch as well:
# a check that died between the two has two runs' jobs to remove, and the second
# launch's job is one of them.
jobs_of_run() { # jobs_of_run [RUN ...]
  [ $# -gt 0 ] || set -- "$RUN_ID"
  local listed=""
  listed=$(gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
    --filter="labels.${RUN_LABEL}:*" --format='value(name.basename(),labels.'"${RUN_LABEL}"')' 2>/dev/null || true)
  local run
  for run in "$@"; do
    [ -n "$run" ] || continue
    printf '%s\n' "$listed" | awk -v run="$run" 'index($2, run) > 0 { print $1 }'
  done
}

delete_job() {
  gcloud batch jobs delete "$1" --project="${P:?}" --location="${L:?}" --quiet >/dev/null 2>&1 || true
}

# sweep_leftovers is the acceptance's "nothing left to delete": every resource is
# deleted by the step that made it, and this is the net under a run that ended
# early. It is bounded (one list, then a delete per job), it is quiet (no prompt,
# ever), and a resource it cannot delete is NAMED in the restore row with the
# command that removes it rather than left silent.
sweep_leftovers() {
  [ -n "$DRY_RUN" ] && return 0
  [ "$CREATED_ANYTHING" = yes ] || return 0
  local names n
  names=$(jobs_of_mine 2>/dev/null || true)
  for n in $names; do delete_job "$n"; done
  names=$(jobs_of_run "$RUN_ID" "$RENAME_RUN_ID" 2>/dev/null || true)
  for n in $names; do delete_job "$n"; done
  # A job whose delete has been accepted is still listed until the platform has
  # finished with it, so the list is read again, bounded, rather than once: a job
  # the step before this one deleted is not a job this check left behind. A poll
  # of zero counts as a second of the bound, so the bound still bounds the wait
  # whatever the poll is set to.
  local waited=0 tick="$SWEEP_POLL"
  [ "$tick" -gt 0 ] || tick=1
  names=$(jobs_of_mine 2>/dev/null || true)
  names="$names$(jobs_of_run "$RUN_ID" "$RENAME_RUN_ID" 2>/dev/null || true)"
  while [ -n "$names" ] && [ "$waited" -lt "$SWEEP_BOUND" ]; do
    sleep "$tick"
    waited=$((waited + tick))
    names=$(jobs_of_mine 2>/dev/null || true)
    names="$names$(jobs_of_run "$RUN_ID" "$RENAME_RUN_ID" 2>/dev/null || true)"
  done
  if [ -n "$names" ]; then
    LEFT_BEHIND="${LEFT_BEHIND}${LEFT_BEHIND:+, }the job(s) $(printf '%s' "$names" | oneline)"
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
  if [ -n "$ABORT_REASON" ] && ! grep -q '^aborted|' <<<"$facts"; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! grep -q '^aborted|' <<<"$facts"; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the run's identity"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS note
  if [ -z "$SCRATCH" ]; then
    note="nothing was created: the run stopped before it made a scratch directory, so no run was launched and no job was made"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    note="the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
    if [ "$CREATED_ANYTHING" = no ]; then
      note="${note}, and nothing was created anywhere: no run and no job"
    elif [ -n "$LEFT_BEHIND" ]; then
      restore=FAIL
      note="${note}. STILL IN ${P:-fugaro-dev}: ${LEFT_BEHIND}. Remove each by hand — a job with: gcloud batch jobs delete <name> --project=${P:-fugaro-dev} --location=${L:-us-east5} --quiet"
    else
      note="${note}, and every job this check made is deleted: nothing of this check's is left in ${P:-fugaro-dev}. The runs' own records in the runs bucket are the runs' and stay — they are what \`fugaro runs ls\` reads"
      if [ -n "$RUN_ID" ]; then note="${note}, and this run's id is ${RUN_ID}"; fi
      if [ -n "$RENAME_RUN_ID" ]; then note="${note}, and the second launch's id is ${RENAME_RUN_ID}"; fi
    fi
  fi
  # A row that no step reached is a FAIL of its own, recorded before the verdict
  # is read: a block whose last line said PASS because the step that would have
  # found the defect never ran is the one outcome this check must not produce.
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    [ "$key" = restore ] && continue
    grep -q "^$key|" <<<"$facts" && continue
    facts="${facts}"$'\n'"$key|FAIL|not reached (the run stopped before this step)"
  done
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
# The project and the region are literals, as they are in every live check here:
# nothing reads gcloud's ambient configuration, and there is no flag that could
# point one of them elsewhere.
P=fugaro-dev
L=us-east5
# The three attack jobs this check makes, all named `fugaro-chk831-*` and all
# labelled here. The third attack's job is the run's own name and is made by the
# same function.
CHK_LABEL=fugaro_chk
CHK_VALUE=831
JOB_REPLAY=fugaro-chk831-replay
JOB_NOTICKET=fugaro-chk831-noticket
# A container with curl and python3, so each attack job reads the metadata server
# itself and builds the claim body the run's own runner builds. It is a
# Google-hosted public image: the attacks are not about the installation's own
# image, and naming one that a project may not have would make this check depend
# on a build.
IMAGE=${CHK831_IMAGE:-gcr.io/google.com/cloudsdktool/google-cloud-cli:slim}
MT=e2-standard-4
CPU_MILLI=2000
MEMORY_MIB=4096
# How long each attack container stays alive after its claim, so its line can be
# read out of the logs while it exists. The check does not wait for it to elapse:
# it deletes each job once it has what it needs.
LINGER=${CHK831_LINGER:-60}
# The bounds on the five waits: the run's job appearing, the ledger saying the
# session opened, the ledger saying the run was metered, an attack's own line,
# and the CreateJob audit record. Each is a knob because a slow project should be
# given longer rather than read as a FAIL.
WAIT_JOB=${CHK831_WAIT_JOB:-300}
WAIT_SESSION=${CHK831_WAIT_SESSION:-600}
WAIT_METERED=${CHK831_WAIT_METERED:-900}
WAIT_ATTACK=${CHK831_WAIT_ATTACK:-600}
WAIT_AUDIT=${CHK831_WAIT_AUDIT:-180}
# The bound on the re-creation: a Batch delete is accepted at once and the name
# is the platform's until it has finished with the job — a submit under it
# before then is refused ALREADY_EXISTS (the reaper's own DELETION_IN_PROGRESS,
# the state the SWEEP_BOUND comment below is about). The third attack retries
# within this bound rather than reading the platform's own answer as the
# attack's refusal, and it is a knob for the same reason the others are.
WAIT_RENAME=${CHK831_WAIT_RENAME:-180}
RENAME_POLL=${CHK831_RENAME_POLL:-5}
# The third attack's own window: the second launch's job must be caught while it
# is still QUEUED — before its VM exists and its bootstrap spends the ticket —
# so the job is read as the second launcher as soon as the platform publishes it,
# and this is how long that reading is retried. It is short on purpose: every
# second spent here is a second the job may leave the queued window in, and a job
# that has already left it is the row's FAIL rather than a silent pass.
WAIT_QUEUED=${CHK831_WAIT_QUEUED:-30}
QUEUE_POLL=${CHK831_QUEUE_POLL:-2}
# Deleting a Batch job is asynchronous: the delete is accepted at once and the
# job stays listable, in DELETION_IN_PROGRESS, until the platform has finished
# with it (the engine's own reaper lists that state: internal/backend/batch/
# reap.go). The sweep at the end waits for its own deletes to land within this
# bound, because a list taken one call too early is not a list of what is left.
SWEEP_BOUND=${CHK831_SWEEP_BOUND:-120}
SWEEP_POLL=${CHK831_SWEEP_POLL:-5}
# The cap this check puts on the run's own spend, as `fugaro run --max-usd`. It
# may only TIGHTEN the ceiling the launch resolves, so a refusal of it is the
# launch row's to report; set it empty (CHK831_MAX_USD=) to pass no cap and let
# the installation's own stand.
MAX_USD=${CHK831_MAX_USD-1}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-831.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_owner: the register's owner-run marker, the same one the other owner-run
# checks carry. It is not a security boundary — an agent's shell can export
# anything — but it is the line that stops a session from launching a real run
# and making four attacks with a second account.
guard_owner() {
  if [ "${CHK831_OWNER_RUN:-}" != yes ]; then
    fact owner FAIL "this is an owner-run check and CHK831_OWNER_RUN is not exported as yes; no agent runs any step of it, and the plan's proof of the run's identity (R11) is what it prints"
    stop "Check 831 is owner-run. Run it at your own terminal, after: export CHK831_OWNER_RUN=yes — no agent performs any step of it. It launches a real run and makes four attacks with a second account"
  fi
  fact owner PASS "CHK831_OWNER_RUN=yes: this run was started by you at your own terminal, and no agent performs any step of Check 831"
}

# read_const pulls one quoted Go constant out of a file: the tree is the
# authority for the ticket's variable, the claim's route and the record's path,
# and a copy of any of them in this script would be a second home for a fact the
# check exists to test.
read_const() { # read_const FILE CONSTNAME
  # The declaration may be its own `const Name = "value"` line or a line inside a
  # `const (...)` block, and the two shapes are both used by the tree this reads:
  # the prefix is allowed rather than the two files being read differently.
  sed -n "s/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?$2[[:space:]]*=[[:space:]]*\"\([^\"]*\)\".*/\2/p" "$1" 2>/dev/null | head -n 1
}

# guard_tree: the checkout this script lives in, read for the things the live
# readings are judged against rather than remembering: the variable a launcher
# puts the ticket in and the route the claim is made to
# (internal/identitywire/wire.go), the record's path family and the escaping its
# two names go through (internal/identitywire/wire.go's PathRecord and
# internal/budget/model.go's Key), the variable a job says its run is
# (internal/backend/batch/env.go), and the label the backend writes on a run's
# job (internal/backend/batch/names.go).
guard_tree() {
  TREE=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd) \
    || stop "Check 831 stopped: the script's own path ($0) does not lead to a checkout, so the ticket's variable, the claim's route and the record's path cannot be read out of the tree"
  local wire="$TREE/internal/identitywire/wire.go"
  local budget="$TREE/internal/budget/model.go"
  local envgo="$TREE/internal/backend/batch/env.go"
  local names="$TREE/internal/backend/batch/names.go"
  local logs="$TREE/internal/backend/batch/logs.go"
  local budgetenv="$TREE/internal/runner/budget_session.go"
  for f in "$wire" "$budget" "$envgo" "$names" "$logs" "$budgetenv"; do
    [ -f "$f" ] || stop "Check 831 stopped: $TREE does not carry ${f#"$TREE"/}, so this is not the Fugaro tree the script belongs to"
  done
  TICKET_ENV=$(read_const "$wire" TicketEnv)
  CLAIM_PATH=$(read_const "$wire" ClaimPath)
  RECORDS_PATH=$(read_const "$wire" pathRecords)
  RUN_LABEL=$(read_const "$names" RunLabel)
  RTDB_ENV=$(read_const "$budgetenv" RTDBURLEnv)
  SERVICE_ENV=$(read_const "$envgo" credentialsServiceEnv)
  [ -n "$TICKET_ENV" ] && [ -n "$CLAIM_PATH" ] && [ -n "$RECORDS_PATH" ] && [ -n "$RUN_LABEL" ] \
    || stop "Check 831 stopped: ${wire#"$TREE"/} no longer carries TicketEnv, ClaimPath and pathRecords, or ${names#"$TREE"/} no longer carries RunLabel, so the ticket's variable, the claim's route, the record's path and the label a job carries its run in would have to be remembered here instead of read"
  [ -n "$RTDB_ENV" ] && [ -n "$SERVICE_ENV" ] \
    || stop "Check 831 stopped: the Realtime Database URL's variable (internal/runner/budget_session.go's RTDBURLEnv) or the credentials service's URL's variable (internal/backend/batch/env.go's credentialsServiceEnv) is gone from the tree, so the run's ledger and the claim's address could not be read out of its job"
  # The run's own variable is not a constant but the one assignment that writes
  # it, and its SHAPE is the point: "<slug>/<run id>", which is what this check
  # parses to hold a job to a run and to build the ledger's and the record's
  # paths. A tree that wrote something else there would make both reads wrong.
  local runassign
  runassign=$(grep -m1 'vars\["FUGARO_RUN"\]' "$envgo" 2>/dev/null || true)
  RUN_ENV=$(printf '%s' "$runassign" | sed -n 's/.*vars\["\([^"]*\)"\].*/\1/p')
  [ -n "$RUN_ENV" ] || stop "Check 831 stopped: ${envgo#"$TREE"/} no longer writes the run's own variable into a job's environment (vars[\"FUGARO_RUN\"]), so a job could not be held to a run"
  case "$runassign" in
  *'spec.Repo.Slug + "/" + spec.RunID'*) ;;
  *) stop "Check 831 stopped: ${envgo#"$TREE"/} writes ${RUN_ENV} as something other than the repository's slug and the run id joined by a slash, and this check parses that value to hold a job to a run and to build the ledger's and the record's paths" ;;
  esac
  # The escaping the record's two names go through: the check builds the record's
  # path itself, so it refuses to run against a tree whose Key escapes something
  # else rather than reading a path the service never writes.
  grep -qF "c == '%' || c == '.' || c == '\$' || c == '#' || c == '[' || c == ']' || c == '/'" "$budget" \
    || stop "Check 831 stopped: ${budget#"$TREE"/}'s Key no longer escapes the set this script escapes when it builds the issuance record's path, so the path it would read is not the path the service writes"
  grep -qF 'pathRecords + "/" + budget.Key(slug) + "/" + budget.Key(run)' "$wire" \
    || stop "Check 831 stopped: ${wire#"$TREE"/}'s PathRecord no longer joins the record's path the way this script builds it, so the path it would read is not the service's"
  grep -q 'labels.job_uid=' "$logs" || stop "Check 831 stopped: ${logs#"$TREE"/} no longer builds its filter with labels.job_uid, so the read this check makes of an attack's own logs would not be the runner's read"
  TASK_LOG="projects/${P}/logs/batch_task_logs"
  local sha
  sha=$(git -C "$TREE" rev-parse HEAD 2>/dev/null || echo unknown)
  TREE_SHA=$sha
  fact tree PASS "the tree at ${TREE} (commit ${sha}) was read, never remembered: the ticket's variable ${TICKET_ENV}, the claim's route ${CLAIM_PATH}, the record's path family ${RECORDS_PATH} (its two names escaped as budget.Key does), the run's own variable ${RUN_ENV}, written as the repository's slug and the run id joined by a slash, the label ${RUN_LABEL} the backend writes on a run's job, the Realtime Database URL's variable ${RTDB_ENV} and the credentials service's ${SERVICE_ENV}, and the log filter labels.job_uid=\"<uid>\" AND logName=\"${TASK_LOG}\""
}

# guard_belong: the owner's own installation and its repositories, refused by
# name. This check names its project itself and has no flag that could name
# another, so the guard is about what the environment could supply instead — and
# about the one argument that would make the attacks pointless.
guard_belong() {
  local ambient=${CLOUDSDK_CORE_PROJECT:-}${GOOGLE_CLOUD_PROJECT:-}${GCLOUD_PROJECT:-}
  local low
  low=$(printf '%s' "$ambient" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "the ambient gcloud configuration names a project matching '${ambient}', and the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch"
    stop "Check 831 never runs against 'belong', EdgeWeb or EdgeServer. Unset CLOUDSDK_CORE_PROJECT/GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and run it again: every command here names --project=${P} itself"
    ;;
  esac
  if [ -z "$SECOND" ]; then
    fact belong FAIL "no second launcher was named, and the four attacks are what this check is: without one they would be made by the account the check runs as, which is the launcher of the run and not a second one"
    stop "Check 831 needs --second-launcher <account>: the account the four attacks are made as (roles/batch.jobsEditor on ${P} and iam.serviceAccountUser on the run accounts, F4). Run: CHK831_OWNER_RUN=yes bash scripts/live-checks/check-831.sh --second-launcher <account> --repo <owner/name>"
  fi
  case "$SECOND" in
  *@*) ;;
  *)
    fact belong FAIL "the second launcher '${SECOND}' is not an account address, so nothing can be impersonated as it"
    stop "Check 831's --second-launcher must be the account's address, as gcloud --impersonate-service-account takes it"
    ;;
  esac
  local mine
  mine=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
  if [ -n "$mine" ] && [ "$(printf '%s' "$mine" | tr 'A-Z' 'a-z')" = "$(printf '%s' "$SECOND" | tr 'A-Z' 'a-z')" ]; then
    fact belong FAIL "the second launcher '${SECOND}' is the account this check is authenticated as (${mine}), so the four attacks would be made by the run's own launcher rather than by a second one, and a refusal could not be told from the launcher's own access"
    stop "Check 831's --second-launcher must be another account than the one gcloud is authenticated as (${mine}): the attacks are about what a second launcher can and cannot obtain"
  fi
  fact belong PASS "the project this check names is ${P} and the region ${L}, both hard-coded; neither 'belong' nor EdgeWeb nor EdgeServer is named by any step, the four attacks are made as the second launcher ${SECOND} (impersonated), and no flag or variable of this check can name another project"
}

# ------------------------------------------------------------------ the cloud
# job_field: one value out of a job object, with the disclaimer that an empty
# answer is an answer (a job with no UID is a job whose logs cannot be read).
job_field() { # job_field NAME FORMAT
  gcloud batch jobs describe "$1" --project="${P:?}" --location="${L:?}" \
    --format="value($2)" 2>/dev/null || true
}

job_uid() { job_field "$1" uid; }

# log_lines: the entries of one Batch job's task logs, as the tree's own filter
# selects them, flattened to text. This is where an attack's own readings come
# from, so it is also what proves the attack container ran at all.
log_lines() { # log_lines UID OUTFILE
  gcloud logging read "labels.job_uid=\"$1\" AND logName=\"${TASK_LOG}\"" --project="${P:?}" \
    --freshness="${CHK831_LOG_AGE:-1h}" --limit=200 --format=json 2>/dev/null \
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

# project_log_json: everything Cloud Logging holds for a filter, as one JSON
# document. The ticket-print readings use it.
project_log_json() { # project_log_json FILTER OUTFILE
  gcloud logging read "$1" --project="${P:?}" --freshness="${CHK831_LOG_AGE:-1h}" \
    --limit=200 --format=json 2>/dev/null >"$2" || : >"$2"
}

# wait_for_lines: polls a job's task logs until a line matching a pattern
# arrives, bounded. It prints nothing and returns non-zero on a spent bound, so
# the caller decides what a missing line means.
wait_for_lines() { # wait_for_lines UID PATTERN OUTFILE SECONDS
  local waited=0
  while :; do
    log_lines "$1" "$3"
    if grep -qE "$2" "$3" 2>/dev/null; then return 0; fi
    [ "$waited" -ge "$4" ] && return 1
    sleep 10
    waited=$((waited + 10))
    say "waiting for the attack's own line (${waited}s of ${4}s)"
  done
}

# rtdb_get: one read of the installation's Realtime Database, with the owner's
# own access token or, when an account is named, with that account's. It prints
# the HTTP status and writes the body to a file: the caller reads the status,
# because a refused read is an answer and a body that is not there is not.
rtdb_get() { # rtdb_get PATH OUTFILE [IMPERSONATE]
  local tok code
  if [ -n "${3:-}" ]; then
    tok=$(gcloud auth print-access-token --impersonate-service-account="$3" 2>/dev/null || true)
  else
    tok=$(gcloud auth print-access-token 2>/dev/null || true)
  fi
  if [ -z "$tok" ]; then
    : >"$2"
    printf '000'
    return 0
  fi
  code=$(curl -sS -o "$2" -w '%{http_code}' --max-time 20 \
    -H "Authorization: Bearer ${tok}" -H 'Accept: application/json' \
    "${RTDB%/}/${1}.json" 2>/dev/null) || code=000
  printf '%s' "${code:-000}"
}

# key_of escapes one path segment the way the tree does (internal/budget/model.go's
# Key): RTDB forbids . $ # [ ] / and ASCII control characters, each becomes %XX in
# uppercase hex, and % itself is escaped. guard_tree refuses a tree whose Key
# escapes another set, so this is a re-spelling of a read fact, not a remembered
# one.
key_of() { # key_of SEGMENT
  python3 -c 'import sys
out = []
for ch in sys.argv[1].encode():
    if ch in b"%$#[]/" or ch < 0x20 or ch == 0x7f:
        out.append("%%%02X" % ch)
    else:
        out.append(chr(ch))
sys.stdout.write("".join(out))
' "$1"
}

# record_path and ledger_path: the two paths this check reads, built the way the
# tree builds them (identitywire.PathRecord and budget.PathRun).
record_path() { # record_path SLUG RUN
  printf '%s/%s/%s' "$RECORDS_PATH" "$(key_of "$1")" "$(key_of "$2")"
}

ledger_path() { # ledger_path SLUG RUN
  printf 'runs/%s/%s' "$(key_of "$1")" "$(key_of "$2")"
}

# ledger_field: one number out of the run's ledger node. A node that is not
# there, or a value that is not a number, reads as empty rather than as zero: the
# caller has to be able to tell "the run reserved nothing" from "there was no
# ledger to read".
ledger_field() { # ledger_field FILE NAME
  python3 -c 'import json,sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = None
if not isinstance(d, dict):
    print("")
else:
    v = d.get(sys.argv[2])
    print("" if v is None else v)
' "$1" "$2"
}
# --------------------------------------------------------------- the fixtures
# read_job.py turns one Batch job object into the fields the block reports, and
# the ticket into a file of its own: the ticket is a handle rather than a secret
# (D3115), but a block that printed one would be a block that had to be treated
# as secret, so it never enters the facts file, the progress log or a command
# line.
write_read_job_py() {
  cat >"$SCRATCH/read_job.py" <<'C831READJOB'
import json, sys

path, facts, ticketpath, ticket_env = sys.argv[1:5]
try:
    j = json.load(open(path))
except Exception:
    j = {}
if not isinstance(j, dict):
    j = {}


def leaf(*keys):
    cur = j
    for k in keys:
        if not isinstance(cur, dict):
            return ""
        cur = cur.get(k)
        if cur is None:
            return ""
    return str(cur)


labels = j.get("labels") if isinstance(j.get("labels"), dict) else {}
variables = {}
for tg in j.get("taskGroups") or []:
    spec = (tg or {}).get("taskSpec") or {}
    env = spec.get("environment") or {}
    v = env.get("variables") or {}
    if isinstance(v, dict):
        variables.update(v)

with open(facts, "w") as fh:
    fh.write("name\t%s\n" % leaf("name"))
    fh.write("uid\t%s\n" % leaf("uid"))
    fh.write("state\t%s\n" % leaf("status", "state"))
    fh.write("created\t%s\n" % leaf("createTime"))
    fh.write("account\t%s\n" % leaf("allocationPolicy", "serviceAccount", "email"))
    for k in sorted(labels):
        fh.write("label\t%s\t%s\n" % (k, labels[k]))
    for k in sorted(variables):
        if k == ticket_env:
            # The ticket is one of the job's variables and this row names it as
            # such (D3115's fact), but its VALUE goes to the file of its own and
            # never into the facts: a block that printed a ticket would be a
            # block that had to be treated as a secret.
            fh.write("var\t%s\t<the ticket, written to its own file>\n" % k)
            continue
        fh.write("var\t%s\t%s\n" % (k, variables[k]))
with open(ticketpath, "w") as fh:
    fh.write(str(variables.get(ticket_env, "")))
C831READJOB
}

job_fact() { # job_fact FILE KEY
  awk -F'\t' -v k="$2" '$1 == k { print $2; exit }' "$1" 2>/dev/null
}

job_vars() { # job_vars FILE — the variable names, never their values
  awk -F'\t' '$1 == "var" { printf "%s ", $2 }' "$1" 2>/dev/null
}

job_labels() { # job_labels FILE
  awk -F'\t' '$1 == "label" { printf "%s=%s ", $2, $3 }' "$1" 2>/dev/null
}

# The container every attack job runs. It is written once, quoted, so nothing in
# it is expanded by this script: every parameter reaches the container as one of
# the job's own variables. It makes the claim the run's own runner makes — the
# ticket, a nonce it draws, and its OWN VM's identity token in full format as the
# attestation and as the bearer (internal/runner/identity_claim.go,
# batch_attest.go) — and it prints one `C831 claim` line (the status, the
# contract's refusal code and whether a token came back) and one `C831 refusal`
# line carrying the refusal's own sentence, which is what tells the VM's
# refusal from one the route answers before the link is asked. It never prints
# the ticket or the token.
write_attack_container() {
  cat >"$SCRATCH/attack.sh" <<'C831CONTAINER'
#!/bin/bash
set -u
MD=http://metadata.google.internal/computeMetadata/v1
md() { curl -s --max-time 8 -H 'Metadata-Flavor: Google' "$MD/$1" 2>/dev/null || true; }
echo "C831 start host=$(hostname)"
svc=${FUGARO_CHK831_SERVICE:-}
path=${FUGARO_CHK831_CLAIM_PATH:-}
if [ -z "$svc" ] || [ -z "$path" ]; then
  echo "C831 claim <no-service-url>"
  echo "C831 read"
  exit 0
fi
tok=$(md "instance/service-accounts/default/identity?audience=${svc}&format=full")
if [ -z "$tok" ]; then
  echo "C831 claim <no-token>"
  echo "C831 read"
  exit 0
fi
nonce=$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')
body=$(python3 - "$FUGARO_CHK831_TICKET" "$nonce" "$tok" <<'PY'
import json, sys
ticket, nonce, tok = sys.argv[1:4]
print(json.dumps({"ticket": ticket, "nonce": nonce, "attestation": tok}))
PY
)
code=$(curl -sS -o "$HOME/claim.json" -w '%{http_code}' --max-time 20 \
  -X POST -H "Authorization: Bearer ${tok}" -H 'Content-Type: application/json' \
  -H 'Accept: application/json' --data "$body" "${svc%/}${path}" 2>/dev/null) || code=000
IFS=$(printf '\t') read -r err has words <<EOF
$(python3 - "$HOME/claim.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = {}
if not isinstance(d, dict):
    d = {}
# The refusal's own words, collapsed to one line and bounded: they are the
# service's prose (credsvc's refuse writes WireError.Message), and the CODE
# alone cannot tell the refusal of a claim on the VM that made it from a
# refusal the route answers before the link is ever asked.
msg = " ".join(str(d.get("message", "")).split())[:400]
print("%s\t%s\t%s" % (d.get("error", "none"), "yes" if d.get("token") else "no", msg))
PY
)
EOF
echo "C831 claim status=${code} error=${err} token=${has}"
# The service's own sentence, on a line of its own because it has spaces in it.
# It names the field the refusal is about and never what the call sent (R7):
# the ticket and the attestation reach no refusal, no log line and no audit
# line, so this line carries no credential of this run's.
echo "C831 refusal ${words}"
echo "C831 read"
sleep "${FUGARO_CHK831_LINGER:-60}"
echo "C831 done"
C831CONTAINER
  chmod +x "$SCRATCH/attack.sh"
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
  cat >"$SCRATCH/mkconfig.py" <<'C831MKCONFIG'
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
            "entrypoint": "/bin/bash",
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

allocation = {"instances": [instance], "labels": obj("alabels")}
if params.get("account", ""):
    allocation["serviceAccount"] = {"email": params["account"]}

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
    "allocationPolicy": allocation,
    "logsPolicy": {"destination": "CLOUD_LOGGING"},
    "labels": obj("labels"),
}
with open(params["out"], "w") as fh:
    json.dump(job, fh, indent=2)
    fh.write("\n")
C831MKCONFIG
  python3 "$SCRATCH/mkconfig.py" "$1"
}

# json_obj builds a JSON object out of key=value pairs, so a label set or a
# variable set can be handed to the builder as one argument. Values are our own
# (a job name, an account, a URL), so no escaping is needed beyond the JSON
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

# json_field: one field out of a JSON document, or empty.
json_field() { # json_field FILE KEY
  python3 -c 'import json,sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = None
print(d.get(sys.argv[2], "") if isinstance(d, dict) else "")
' "$1" "$2"
}
# ----------------------------------------------------------------- the steps
# installation: the installation the check is about, out of the local project
# configuration rather than out of gcloud's ambient one. The project and the
# region are what every cloud call here names, so an installation that is not
# `fugaro-dev`/`us-east5` is a stop and not a FAIL: nothing below could be read
# against it.
step_installation() {
  local cfg="${CHK831_CONFIG:-${HOME:-}/.config/fugaro/projects/fugaro.yaml}"
  local ver project region backend
  if [ ! -f "$cfg" ]; then
    fact installation FAIL "no installation configuration is at ${cfg}, so there is nothing that says which project and region this installation is (fugaro writes it at fugaro setup: \`fugaro setup\` on the ${P} installation, or point CHK831_CONFIG at the file)"
    stop "Check 831 needs the installation's own configuration at ${cfg}: run it on the ${P} installation"
  fi
  project=$(grep -m1 -E '^[[:space:]]*gcp_project:[[:space:]]*' "$cfg" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"' ' || true)
  region=$(grep -m1 -E '^[[:space:]]*region:[[:space:]]*' "$cfg" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"' ' || true)
  backend=$(grep -m1 -E '^[[:space:]]*backend:[[:space:]]*' "$cfg" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"' ' || true)
  RTDB=$(grep -m1 -E '^[[:space:]]*rtdb_url:[[:space:]]*' "$cfg" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"' ' || true)
  if [ "$project" != "$P" ] || [ "$region" != "$L" ]; then
    fact installation FAIL "the installation at ${cfg} is project '${project:-<unset>}' in region '${region:-<unset>}', and every cloud call of this check names ${P}/${L} itself: this is not the installation Check 831 is about, and no reading below could be made against it"
    stop "Check 831 runs on the ${P} installation (region ${L}) and on no other. Run it there, or point CHK831_CONFIG at that installation's own configuration file"
  fi
  if [ -n "$backend" ] && [ "$backend" != cloud-batch ]; then
    fact installation FAIL "the installation at ${cfg} says backend '${backend}', and this check is about a BATCH run: a run launched here would not be a Batch job at all, so none of the four attacks would be about anything"
    stop "Check 831 is about a Batch run: put the installation on the Batch backend (backend: cloud-batch, which fugaro setup --provision-batch writes) or run this check on one that is"
  fi
  ver=$(fugaro version 2>/dev/null | head -n 1 | oneline || true)
  fact installation PASS "the installation at ${cfg} is ${P}/${L}$([ -n "$backend" ] && echo ", backend ${backend}" || echo ", backend not stated in the file — the run's own job in the \`job-found\` row is what says whether it was Batch")$([ -n "$RTDB" ] && echo ", its Realtime Database ${RTDB}" || echo ", and it states no Realtime Database URL (the ledger is then read at the URL the run's own job carries)"). \`fugaro version\` says: ${ver:-<unread>}"
}

# launch_run: one sandbox run of ${REPO} on Batch, its answer written to the
# named file. Both launches of this check are made with this one command: the run
# whose identity the four attacks are about, and the second launch whose still
# queued job the third attack deletes and re-creates.
launch_run() { # launch_run OUTFILE
  local args=(run --repo "$REPO" --task-file "$SCRATCH/task.md" --json)
  [ -n "$MAX_USD" ] && args+=(--max-usd "$MAX_USD")
  fugaro "${args[@]}" >"$1" 2>"$SCRATCH/err"
}

# execution_job: the job's own name out of a launch's answer. The answer's
# execution field names the job (…/jobs/<name>), and a launch that named none
# leaves this empty: the row that needs the name says so rather than guessing.
execution_job() { # execution_job JSONFILE
  local execution name=""
  execution=$(json_field "$1" execution)
  case "$execution" in
  */jobs/*) name=${execution##*/} ;;
  esac
  printf '%s\n' "$name"
}

# launch: one real sandbox run. The task is a file this check writes, and it says
# what it is: its job is deleted once the attacks have read it, so it must not do
# any work in the repository it is launched in.
step_launch() {
  say "launching one sandbox run of ${REPO} on Batch"
  cat >"$SCRATCH/task.md" <<'C831TASK'
# Task: check 831 sandbox probe — do not change anything

This run is a probe of the run's own identity, launched by
`scripts/live-checks/check-831.sh`, which deletes its job once the attacks have
read it. It is not asked to do any work.

Change nothing in this repository, push nothing, and open no pull request. Read
this file and stop.
C831TASK
  if ! launch_run "$SCRATCH/launch.json"; then
    local msg
    msg=$(oneline <"$SCRATCH/err")
    fact launch FAIL "the run could not be launched in ${REPO} (${msg}); nothing was created, so the run's identity and the four attacks are all unmeasured"
    stop "the sandbox run could not be launched: ${msg}"
  fi
  CREATED_ANYTHING=yes
  RUN_ID=$(json_field "$SCRATCH/launch.json" run_id)
  local status branch
  status=$(json_field "$SCRATCH/launch.json" status)
  branch=$(json_field "$SCRATCH/launch.json" branch)
  RUN_JOB=$(execution_job "$SCRATCH/launch.json")
  if [ -z "$RUN_ID" ]; then
    fact launch FAIL "fugaro run answered with no run_id, so the run this check is about has no name: its ledger, its job and its record are all unreachable (the answer was: $(oneline <"$SCRATCH/launch.json"))"
    stop "the launch answered with no run id, so nothing about the run's identity can be read"
  fi
  fact launch PASS "one run of ${REPO} was launched in ${P} on Batch: run ${RUN_ID} (status ${status:-<unread>}, branch ${branch:-<unread>}$([ -n "$MAX_USD" ] && echo ", capped at \$$MAX_USD by --max-usd" || echo ", with no cap of this check's — the installation's own stand")). The task file asks the run to change nothing, and this check deletes the run's job as soon as the attack that reads it has read it. The launch answered the job's own name as ${RUN_JOB:-<none>}, which the \`job-found\` row holds to the run rather than trusting"
}

# job-found: the run's Batch job, out of the launch's answer when it named one
# and out of the backend's own run label when it did not. A name is a launcher's
# word, so the job is held to the run by its own FUGARO_RUN variable, and the
# row reports the uid, the state and the account the job runs as — the account is
# what the attacks must run as too, or a refusal would be about the account
# rather than about the VM (F4).
step_job_found() {
  write_read_job_py
  local waited=0 name=""
  while :; do
    if [ -z "$name" ]; then
      if [ -n "$RUN_JOB" ] && gcloud batch jobs describe "$RUN_JOB" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job.json" 2>/dev/null; then
        name=$RUN_JOB
      else
        name=$(jobs_of_run | head -n 1)
        [ -n "$name" ] && gcloud batch jobs describe "$name" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job.json" 2>/dev/null || name=""
      fi
    else
      gcloud batch jobs describe "$name" --project="${P:?}" --location="${L:?}" --format=json >"$SCRATCH/job.json" 2>/dev/null || name=""
    fi
    if [ -n "$name" ]; then
      python3 "$SCRATCH/read_job.py" "$SCRATCH/job.json" "$SCRATCH/job-facts" "$TICKET_FILE" "$TICKET_ENV" 2>/dev/null || true
      local runsays
      runsays=$(job_fact "$SCRATCH/job-facts" name)
      RUN_UID=$(job_fact "$SCRATCH/job-facts" uid)
      RUN_STATE=$(job_fact "$SCRATCH/job-facts" state)
      RUN_ACCOUNT=$(job_fact "$SCRATCH/job-facts" account)
      local envrun runslug runofjob
      envrun=$(awk -F'\t' '$1 == "var" && $2 == "'"$RUN_ENV"'" { print $3; exit }' "$SCRATCH/job-facts" 2>/dev/null)
      case "$envrun" in
      */*)
        runslug=${envrun%%/*}
        runofjob=${envrun#*/}
        ;;
      *) runslug=""; runofjob="" ;;
      esac
      if [ -n "$RUN_UID" ] && [ -n "$runslug" ] && [ "$runofjob" = "$RUN_ID" ]; then
        RUN_JOB=$name
        RUN_SLUG=$runslug
        RUN_RTDB=$(awk -F'\t' '$1 == "var" && $2 == "'"$RTDB_ENV"'" { print $3; exit }' "$SCRATCH/job-facts" 2>/dev/null)
        RUN_SERVICE=$(awk -F'\t' '$1 == "var" && $2 == "'"$SERVICE_ENV"'" { print $3; exit }' "$SCRATCH/job-facts" 2>/dev/null)
        if [ -s "$TICKET_FILE" ]; then TICKET_PRESENT=yes; fi
        fact job-found PASS "the run's Batch job is ${RUN_JOB} (uid ${RUN_UID}, state ${RUN_STATE:-<unread>}, created $(job_fact "$SCRATCH/job-facts" created), running as ${RUN_ACCOUNT:-<unread>}, labels: $(job_labels "$SCRATCH/job-facts" | oneline)), and it is held to the run by its own ${RUN_ENV} variable, which reads ${envrun} — a name is a launcher's word and this row does not rest on one. The run's slug is therefore ${RUN_SLUG}, which is what its ledger and its issuance record are keyed by. Its variables are: $(job_vars "$SCRATCH/job-facts" | oneline)"
        return 0
      fi
      # A job whose name matched but whose own variable names another run is not
      # this run's: the search goes on rather than reading another run's job.
      name=""
    fi
    [ "$waited" -ge "$WAIT_JOB" ] && break
    sleep 10
    waited=$((waited + 10))
    say "waiting for the run's Batch job (${waited}s of ${WAIT_JOB}s)"
  done
  fact job-found FAIL "no Batch job whose own ${RUN_ENV} variable names run ${RUN_ID} was found in ${P}/${L} within ${WAIT_JOB}s: either the run was launched on another backend (this installation's backend, and the launch's own answer, are named above), or its job is not in ${L}. Without the job there is nothing to read the ticket out of, nothing to attack, and no reading of the ledger or the record, which are both keyed by the slug that only the job states: gcloud batch jobs list --project=${P} --location=${L} --filter='labels.${RUN_LABEL}:*'"
}

# session and metered: the run's own ledger, read with the owner's access token —
# the token of the account the installation belongs to, which the database treats
# as an administrator, where the second launcher's is a caller the rules answer.
step_session() {
  local url="${RUN_RTDB:-${RTDB:-}}"
  if [ -z "$RUN_SLUG" ]; then
    fact session FAIL "the run's job was never found (see \`job-found\`), so the slug its ledger is keyed by is unread: nothing says the run opened its session"
    fact metered FAIL "the run's slug is unread (see \`session\`), so the run's own spend and tokens could not be read"
    return 0
  fi
  if [ -z "$url" ]; then
    fact session FAIL "neither the run's job nor the installation's configuration names the Realtime Database URL, so the run's ledger cannot be read: without it there is no reading that says the run opened its session. Point CHK831_CONFIG at the installation's configuration file (fugaro writes budget.rtdb_url into it)"
    fact metered FAIL "the Realtime Database URL is unknown (see the \`session\` row), so the run's own spend and tokens could not be read"
    return 0
  fi
  local waited=0
  local ledger
  ledger=$(ledger_path "$RUN_SLUG" "$RUN_ID")
  local code
  while :; do
    code=$(rtdb_get "$ledger" "$SCRATCH/ledger.json")
    LEDGER_RESERVED=$(ledger_field "$SCRATCH/ledger.json" reserved)
    LEDGER_EXP=$(ledger_field "$SCRATCH/ledger.json" exp)
    LEDGER_SPENT=$(ledger_field "$SCRATCH/ledger.json" spent)
    LEDGER_TOKENS=$(ledger_field "$SCRATCH/ledger.json" tokens)
    [ "$code" = 200 ] && [ -n "$LEDGER_RESERVED" ] && LEDGER_READ=yes
    [ "$LEDGER_READ" = yes ] && break
    [ "$waited" -ge "$WAIT_SESSION" ] && break
    sleep 15
    waited=$((waited + 15))
    say "waiting for the run's ledger to say it opened its session (${waited}s of ${WAIT_SESSION}s)"
  done
  if [ "$LEDGER_READ" != yes ]; then
    fact session FAIL "the run's ledger could not be read at ${url%/}/${ledger}.json within ${WAIT_SESSION}s (HTTP ${code}): either the run never took its lease — a run that halted at bootstrap, before its first claim, reserves nothing — or the database refused the read (a token that is not the installation owner's is answered by the rules, and ${url} may not be this installation's). Read it by hand: curl -H \"Authorization: Bearer \$(gcloud auth print-access-token)\" '${url%/}/${ledger}.json'"
    fact metered FAIL "the ledger could not be read (see the \`session\` row), so the run's own spend and tokens are unknown"
    return 0
  fi
  fact session PASS "the run opened its session: its ledger at ${ledger} holds reserved ${LEDGER_RESERVED} micros and a lease expiring at ${LEDGER_EXP}, read with your own access token — the account the installation belongs to, which the database treats as an administrator. The job's own state beside it is ${RUN_STATE:-<unread>}"
  local spent_dollars
  spent_dollars=$(python3 -c 'import sys
try:
    print("%.4f" % (int(sys.argv[1]) / 1000000.0))
except Exception:
    print("<unread>")' "${LEDGER_SPENT:-0}" 2>/dev/null || echo "<unread>")
  # The meter is read once at once and then again on every poll: a run that has
  # already been metered when the session's own read happened must not wait a
  # single poll to be seen, and a bound of zero must still leave one reading
  # taken rather than none.
  local waited2=0
  while :; do
    rtdb_get "$ledger" "$SCRATCH/ledger.json" >/dev/null
    LEDGER_SPENT=$(ledger_field "$SCRATCH/ledger.json" spent)
    LEDGER_TOKENS=$(ledger_field "$SCRATCH/ledger.json" tokens)
    if [ "${LEDGER_TOKENS:-0}" -gt 0 ] 2>/dev/null || [ "${LEDGER_SPENT:-0}" -gt 0 ] 2>/dev/null; then break; fi
    [ "$waited2" -ge "$WAIT_METERED" ] && break
    sleep 15
    waited2=$((waited2 + 15))
    say "waiting for the run to be metered (${waited2}s of ${WAIT_METERED}s)"
  done
  spent_dollars=$(python3 -c 'import sys
try:
    print("%.4f" % (int(sys.argv[1]) / 1000000.0))
except Exception:
    print("<unread>")' "${LEDGER_SPENT:-0}" 2>/dev/null || echo "<unread>")
  if [ "${LEDGER_TOKENS:-0}" -gt 0 ] || [ "${LEDGER_SPENT:-0}" -gt 0 ]; then
    fact metered PASS "the run was metered: its ledger holds spent ${LEDGER_SPENT} micros (\$${spent_dollars}) and ${LEDGER_TOKENS} tokens. The run's job is what the attacks read next and is deleted by this check once they have, so these are its figures as they stood when the meter first moved"
  else
    fact metered FAIL "the run opened its session (reserved ${LEDGER_RESERVED} micros) but no spend and no tokens appeared in its ledger within ${WAIT_METERED}s: nothing says the run was metered, so the reading the acceptance asks for is NOT taken. Read the ledger by hand, and the run's own job: gcloud batch jobs describe ${RUN_JOB:-<job>} --project=${P} --location=${L}"
  fi
}

# launch-ticket: whether the launcher put the run's identity ticket on the job.
# It is the row the three attacks stand on: a job that carries no ticket has
# nothing to replay, so the replay and the re-creation could not be the attacks
# the plan asks for.
step_launch_ticket() {
  if [ "$TICKET_PRESENT" = yes ]; then
    fact launch-ticket PASS "the launcher put the run's identity ticket on the job: ${TICKET_ENV} is one of its variables (read out of the job's own document, and written to a file of this check's rather than into a fact). The ticket's value is printed nowhere in this block: it is a handle rather than a secret (D3115), but a block that printed one would be a block that had to be treated as secret"
    return 0
  fi
  fact launch-ticket FAIL "the run's job carries no ${TICKET_ENV} variable, so the launcher of this installation does not put an identity ticket on the job it creates: there is nothing for the second launcher to replay, and the \`attack-replay\` and \`attack-rename\` rows are NOT measured as the attacks the plan asks for. This is the launcher's half of the mechanism (identitywire.TicketEnv, the plan's LM6), and it is absent from the tree under test: check \`fugaro version\` against the release that carries it (the plan's Check 831 row says this check runs after that release, on an updated ${P})"
}

# ticket-printed: where Batch PRINTS the ticket. D3115's rule is that the ticket
# is a handle and not a secret — it is a job variable, so it is readable by anyone
# who can read the job (F3, F35) — and this row measures that rule rather than
# assuming it. The verdict is about the READINGS, not about the leakage: a ticket
# in the logs is expected and accepted. FAIL only when a reading could not be
# taken, because absence is not established by a record nobody read.
step_ticket_printed() {
  if [ "$TICKET_PRESENT" != yes ]; then
    fact ticket-printed FAIL "there is no ticket to find: the run's job carries no ${TICKET_ENV} (see \`launch-ticket\`), so where Batch prints one was not measured at all"
    return 0
  fi
  local seen=""
  # (1) the job's own document: the reading the ticket was taken out of.
  seen="the job's own document (that is where this check read it, and where a second launcher that may read the job reads it too — the read the first attack makes), "
  # (2) the job's task logs.
  local logs_read=no
  if [ -n "$RUN_UID" ]; then
    log_lines "$RUN_UID" "$SCRATCH/run-lines"
    logs_read=yes
    grep -qF -f "$TICKET_FILE" "$SCRATCH/run-lines" 2>/dev/null && seen="${seen}the run's task logs (labels.job_uid=\"${RUN_UID}\"), "
  fi
  # (3) the CreateJob audit record, waited for within a bound and told apart by
  # the job's own uid: the name is fixed per repository and a record of another
  # run of the same repository carries the same name.
  local audit=not waited=0
  while :; do
    project_log_json 'protoPayload.methodName="google.cloud.batch.v1.BatchService.CreateJob"' "$SCRATCH/audit.json"
    if grep -qF "$RUN_UID" "$SCRATCH/audit.json" 2>/dev/null; then
      audit=yes
      break
    fi
    [ "$waited" -ge "$WAIT_AUDIT" ] && break
    sleep 10
    waited=$((waited + 10))
    say "waiting for this run's CreateJob audit record (uid ${RUN_UID}) (${waited}s of ${WAIT_AUDIT}s)"
  done
  local audit_says=no
  if [ "$audit" = yes ]; then
    grep -qF -f "$TICKET_FILE" "$SCRATCH/audit.json" 2>/dev/null && { audit_says=yes; seen="${seen}the CreateJob audit record of this run (the one carrying uid ${RUN_UID}), "; }
  fi
  local logs_says=no
  [ "$logs_read" = yes ] && grep -qF -f "$TICKET_FILE" "$SCRATCH/run-lines" 2>/dev/null && logs_says=yes
  if [ "$logs_read" != yes ] || [ "$audit" != yes ]; then
    fact ticket-printed FAIL "the ticket is in the job's own document (the reading this check took it from), but not every other reading could be taken: the run's task logs were $([ "$logs_read" = yes ] && echo "read" || echo "NOT read (the job's uid is unread)") and this run's CreateJob audit record was $([ "$audit" = yes ] && echo "found" || echo "NOT found within ${WAIT_AUDIT}s"). Absence is not established by a record nobody read, so this row is fail-closed: give the audit record longer (CHK831_WAIT_AUDIT=<seconds>) and run again, and check that Admin Activity records for batch.googleapis.com are on in ${P}"
    return 0
  fi
  fact ticket-printed PASS "the ticket's value was found in ${seen}and in $([ "$logs_says" = yes ] && echo "the run's task logs" || echo "neither the run's task logs")$([ "$audit_says" = yes ] && echo ", and in this run's CreateJob audit record" || echo ""). D3115's rule is measured rather than assumed: a job's variables are part of the job's own document and Batch publishes them (F3), so the ticket is readable there by anyone who may read the job, and the audit record and the logs carry it or not as the platform writes them. That is accepted — the ticket alone claims nothing: the claim must come from a VM of the job the record names (D3114), the identity is served once (R5), and a job re-created under a deleted job's name is refused by the record's id (D3108, F30, the third attack). The value itself is printed nowhere in this block"
}

# The three attack jobs. Each is submitted AS the second launcher
# (--impersonate-service-account), runs as the account the run's own job runs as
# (or a refusal would be about the account rather than about the VM, F4), carries
# the ticket the attack is about, and claims from its own VM.
submit_attack() { # submit_attack NAME TICKET [ACCOUNT]
  local name=$1 ticket=$2 account=${3:-$RUN_ACCOUNT}
  mk_params "$SCRATCH/params-attack" \
    out="$SCRATCH/attack-job.json" \
    image="$IMAGE" \
    script="$SCRATCH/attack.sh" \
    machine="$MT" cpu="$CPU_MILLI" mem="$MEMORY_MIB" \
    account="$account" \
    max_run="$((LINGER + 300))s" \
    labels="$(json_obj "$CHK_LABEL=$CHK_VALUE")" \
    alabels="$(json_obj "$CHK_LABEL=$CHK_VALUE")" \
    env="$(json_obj "FUGARO_CHK831_SERVICE=$RUN_SERVICE" "FUGARO_CHK831_CLAIM_PATH=$CLAIM_PATH" "FUGARO_CHK831_TICKET=$ticket" "FUGARO_CHK831_LINGER=$LINGER")"
  write_config "$SCRATCH/params-attack"
  if ! gcloud batch jobs submit "$name" --project="${P:?}" --location="${L:?}" \
    --impersonate-service-account="$SECOND" --config="$SCRATCH/attack-job.json" >/dev/null 2>"$SCRATCH/err"; then
    oneline <"$SCRATCH/err"
    return 1
  fi
  SUBMITS=$((SUBMITS + 1))
  return 0
}

# claim_row: one attack's row, from the lines its own container printed. PASS is
# the absence of an identity AND a decision about the VM the claim came from: a
# claim answered 5xx, or not answered at all, was not judged; an answer that
# carried a token is the finding these rows exist for; and a refusal the route
# can make WITHOUT asking the link is not this row's refusal either.
#
# The last of those is the one this function has to work for. These attacks run
# after the ledger says the run opened its session and was metered, and by then
# the run's own bootstrap has already claimed the ticket — so a service that
# does not ask the link (this tree, until BR2 and BR3 merge: the route checks
# the account, the ticket's digest and life, the record's claims and the
# once-only fence, and never reads the attestation) refuses the replay with the
# FENCE's refusal, "the ticket was already claimed". A row that read that as a
# refusal of the attack would record a PASS proving nothing: a tree with no
# VM-to-job link at all would pass both replay rows. What is read here is the
# refusal's own sentence, which the container prints beside its code, and the
# sentence is what tells the two apart.
claim_row() { # claim_row KEY LINES WHAT-REFUSED
  local key=$1 lines=$2 what=$3
  if ! grep -qx 'C831 read' "$lines" 2>/dev/null; then
    fact "$key" FAIL "the attack's container reported no reading at all within the bound (no 'C831 read' line came back through the tree's own filter): the attack was NOT judged, and an attack nobody judged is not a refusal. Read the job's own status and its logs: gcloud batch jobs describe <the attack job> --project=${P} --location=${L}"
    return 0
  fi
  local line status refusal token words
  line=$(grep -m1 '^C831 claim ' "$lines" 2>/dev/null || true)
  case "$line" in
  "")
    fact "$key" FAIL "the attack's container reported a reading but no claim line, so nothing says what the service answered"
    return 0
    ;;
  *"<no-token>"* | *"<no-service-url>"*)
    fact "$key" FAIL "the attack's own VM could not mint its identity token (the container reported '${line#C831 claim }'), so the claim was never made: what refuses a claim on the VM's identity cannot be measured from a VM that has none. This is U2's fact (the metadata server's full-format token, Check 830's own row) and not this attack's"
    return 0
    ;;
  esac
  status=$(printf '%s' "$line" | sed -n 's/.*status=\([^ ]*\).*/\1/p')
  refusal=$(printf '%s' "$line" | sed -n 's/.*error=\([^ ]*\).*/\1/p')
  token=$(printf '%s' "$line" | sed -n 's/.*token=\([^ ]*\).*/\1/p')
  words=$(grep -m1 '^C831 refusal ' "$lines" 2>/dev/null | sed 's/^C831 refusal //' || true)
  if [ "$refusal" = not_served ]; then
    fact "$key" FAIL "the service answered 'not_served', so it did NOT decide the claim: its link from a VM to the job the record names is not wired (identitywire.VMIdentity, BR3 — a nil link is answered this way, D3114), and a claim it cannot judge is served to nobody and refused to everybody. Nothing about the VM was measured, so this row is NOT a refusal of the attack. Its own words: '${words:-<none>}' (HTTP ${status}). An owner run on an installation whose link is not wired reports exactly this, and the answer is the plan's work and not a defect in the check"
    return 0
  fi
  case "$status" in
  000 | 5*)
    fact "$key" FAIL "the claim was made from the attack's own VM and nothing decided it (HTTP ${status}): a service that is down, or one that answered its own failure, did not refuse anything, so the attack was NOT judged. The container's own line reads '${line#C831 claim }'. Read the service's logs and run again"
    return 0
    ;;
  esac
  if [ "$token" = yes ]; then
    fact "$key" FAIL "the attack OBTAINED the run's identity: the claim was made from the attack's own VM (${what}) and the service answered it a token (HTTP ${status}, refusal ${refusal}). This is the finding these rows exist for — the identity of a Batch run was served to a VM that is not the run's job's, and the mechanism the plan rests on (D3114, D3108) is not doing its work in the tree under test"
    return 0
  fi
  if [ "$refusal" != identity_refused ]; then
    fact "$key" FAIL "the claim was refused and not by the claim route's own decision about it: the service answered the code '${refusal}' (HTTP ${status}), which is not the refusal a claim the route READ is refused with (identity_refused), and its words are '${words:-<none>}'. Whether the VM was ever judged is not said by this answer, so the row is NOT measured: read the service's logs for this run and run again"
    return 0
  fi
  # The two refusals this route can answer before it asks the link, in this
  # attack's own situation (the account is the run's, the ticket is the record's
  # and was minted minutes ago): the once-only fence, when the ticket is already
  # spent — the run's own bootstrap spends it, which is what a replay of a
  # metered run's ticket meets — and the ticket's own life. Both are the route's
  # words in internal/credsvc/identity_claim.go, both are answered WITHOUT the
  # link, and either of them read as this row's refusal would be the PASS this
  # row must not print. Any other refusal of a claim the route read is one the
  # link made (D3114: the VM is not the recorded job's, or the job is not the
  # registered one — D3108 for the re-created job), which is what these rows
  # measure.
  case "$words" in
  *"already claimed"* | *"has expired"*)
    fact "$key" FAIL "the claim was refused, and the refusal is one the service answers WITHOUT asking whether the VM is the job's: '${words}'. The attack's ticket was already spent — a run's own bootstrap claimed it, and the two rows meet that differently: the replay carries the run-under-test's ticket, which the check's own order guarantees is spent, because it waits for the ledger to say the run opened its session and was metered and that claim is the run's; the re-creation carries the second launch's ticket, which is spent only if that run's VM came up between this check's read of its job and the delete, so the window was not kept open — so this refusal is the ticket being spent and not the VM being another job's. The tree under test does not ask the link on this route (BR2, BR3), and this row is the measurement of that, not a PASS: the identity was not served to the attack's VM by any decision about the VM, and a tree with no link at all answers exactly this"
    return 0
    ;;
  esac
  if [ -z "$words" ]; then
    fact "$key" FAIL "the service refused the claim (HTTP ${status}, ${refusal}) and its refusal carried no sentence this check could read, so the refusal cannot be told from one the route answers before the link is asked — the ticket already spent, or expired — and this row is NOT measured. A refusal of this service carries WireError.Message (credsvc's refuse); read the service's logs and run again"
    return 0
  fi
  fact "$key" PASS "the attack was refused: the claim was made from the attack's own VM (${what}), the service decided it, and no identity came back (HTTP ${status}, the contract's refusal ${refusal}). The refusal is a decision about the VM and not the ticket: the service's own words are '${words}', and the refusals this route answers before it asks the link — the ticket already spent, and the ticket expired — are this row's FAIL. A claim answered 5xx or not answered at all is this row's FAIL too, so this PASS is a decision read once"
}

# ticket_as_second: the ticket out of a job's own document AS THE SECOND LAUNCHER
# reads it. The row that says the second launcher "carried the ticket it found
# there" has to measure that it could read it: this describes the job under its
# impersonation with --format=json — the whole document, not one field — and
# takes the ticket out of THAT document with read_job.py, into a file of its own
# in the scratch directory. It prints the ticket and returns 0 when the
# second launcher's own read carried one, and returns non-zero when the describe
# answered nothing or the document carried no ticket, which is the row's FAIL.
ticket_as_second() { # ticket_as_second JOB STEM — reads as $SECOND, writes $SCRATCH/STEM-ticket
  local job=$1 stem=$2
  rm -f "$SCRATCH/$stem-ticket"
  gcloud batch jobs describe "$job" --project="${P:?}" --location="${L:?}" \
    --impersonate-service-account="${SECOND:?}" --format=json >"$SCRATCH/$stem-job.json" 2>/dev/null || return 1
  python3 "$SCRATCH/read_job.py" "$SCRATCH/$stem-job.json" "$SCRATCH/$stem-job-facts" "$SCRATCH/$stem-ticket" "$TICKET_ENV" 2>/dev/null || true
  [ -s "$SCRATCH/$stem-ticket" ] || return 1
  cat "$SCRATCH/$stem-ticket"
}

# attack-replay: the second launcher reads the run's job, takes the ticket out of
# it, and runs a job of its own carrying that ticket.
step_attack_replay() {
  if [ "$TICKET_PRESENT" != yes ]; then
    fact attack-replay FAIL "there is nothing to replay: the run's job carries no ${TICKET_ENV} (see \`launch-ticket\`), so the ticket could not be read out of the job and this attack was NOT made as the plan asks it"
    return 0
  fi
  say "reading the run's job ${RUN_JOB} as the second launcher ${SECOND}"
  local ticket
  if ! ticket=$(ticket_as_second "$RUN_JOB" replay); then
    fact attack-replay FAIL "the second launcher ${SECOND} could not read the run's ticket out of its job ${RUN_JOB}: gcloud batch jobs describe --impersonate-service-account=${SECOND} --format=json either answered nothing — the impersonation is not allowed from your account (roles/iam.serviceAccountTokenCreator on ${SECOND}), or the job is not there — or the document it answered carries no ${TICKET_ENV} the second launcher could take. The reading this attack is about is the second launcher's OWN read of the ticket: replaying the launcher's reading of it would claim the second launcher could see it without measuring that, so this row is NOT measured"
    return 0
  fi
  if ! cmp -s "$SCRATCH/replay-ticket" "$TICKET_FILE"; then
    fact attack-replay FAIL "the ticket the second launcher read out of the run's job is not the ticket this check read out of that job as the owner (the two readings of one document differ), so what was replayed would not be the ticket the second launcher found there: the run's job may have been replaced under this check, and neither reading is the one this row is about. The attack was NOT made as the plan asks it"
    return 0
  fi
  say "submitting the replay job ${JOB_REPLAY} as ${SECOND}, running as ${RUN_ACCOUNT:-<unread>}"
  if ! submit_attack "$JOB_REPLAY" "$ticket"; then
    local msg
    msg=$(oneline <"$SCRATCH/err")
    fact attack-replay FAIL "the second launcher could not submit its own job carrying the run's ticket (${msg}): the attack was NOT made, so nothing is concluded. The account must hold roles/batch.jobsEditor on ${P} and iam.serviceAccountUser on the run account ${RUN_ACCOUNT:-<unread>} (F4)"
    return 0
  fi
  local uid
  uid=$(job_uid "$JOB_REPLAY")
  if ! wait_for_lines "$uid" '^C831 read$' "$SCRATCH/replay-lines" "$WAIT_ATTACK"; then
    fact attack-replay FAIL "the replay job ran as ${RUN_ACCOUNT:-<unread>} but no line of its own came back within ${WAIT_ATTACK}s, so the attack was NOT judged: the container may not have started, or its lines do not land in the log the runner reads"
    return 0
  fi
  claim_row attack-replay "$SCRATCH/replay-lines" "it read the run's job as the second launcher, took the ticket out of the document that read answered, and carried that ticket from its own VM"
  delete_job "$JOB_REPLAY"
}

# attack-noticket: the same job with no ticket at all. It is the control the
# other attacks are read beside: if this one is served, nothing below is about
# the ticket either.
step_attack_noticket() {
  say "submitting the no-ticket job ${JOB_NOTICKET} as ${SECOND}"
  if ! submit_attack "$JOB_NOTICKET" ""; then
    local msg
    msg=$(oneline <"$SCRATCH/err")
    fact attack-noticket FAIL "the second launcher could not submit its own job with no ticket (${msg}): the control was NOT made, so nothing is concluded"
    return 0
  fi
  local uid
  uid=$(job_uid "$JOB_NOTICKET")
  if ! wait_for_lines "$uid" '^C831 read$' "$SCRATCH/noticket-lines" "$WAIT_ATTACK"; then
    fact attack-noticket FAIL "the no-ticket job ran but no line of its own came back within ${WAIT_ATTACK}s, so the control was NOT judged"
    return 0
  fi
  claim_row attack-noticket "$SCRATCH/noticket-lines" "it carried no ticket at all and claimed from its own VM"
  delete_job "$JOB_NOTICKET"
}

# attack-rename: the second launcher deletes a SECOND LAUNCH's job while it is
# still QUEUED — before that run's VM exists, so before its bootstrap has spent
# its ticket — and re-creates a job under the same name carrying that run's
# ticket. Nothing but the record's job id is then left to refuse the re-created
# job: the name is the same and the ticket is unspent, so a service that judged
# the name would be satisfied, while the re-created job is another job with
# another Google id (D3108, F30). The row FAILS when the job was not QUEUED when
# it was read, because then the window had passed and the attack the plan asks
# for — the queued one — was not made. The re-creation waits, bounded, for the
# deleted name to be free again: a Batch delete is asynchronous, and a submit
# under the name while the platform is still finishing with the job is refused
# ALREADY_EXISTS — the platform's own answer, and not the attack's refusal.
step_attack_rename() {
  if [ "$TICKET_PRESENT" != yes ]; then
    fact attack-rename FAIL "there is nothing to carry into a re-created job: the run's job carries no ${TICKET_ENV} (see \`launch-ticket\`), so the re-creation could not be the attack the plan asks for and this row is NOT measured"
    return 0
  fi
  # The run under test has been read by the attack that reads it — the replay,
  # which takes its ticket out of that job as the second launcher — so its job is
  # deleted here, by this check as the installation owner. This is not the attack:
  # this attack used to delete that very job, and that is what made its row wrong.
  # A run that has been metered has already claimed its ticket with its own
  # bootstrap, so a re-creation carrying that ticket is refused for being spent,
  # and the record's own job id — the one thing the attack is about (D3108, F30)
  # — is never what refuses it.
  say "deleting the run's job ${RUN_JOB} as the owner: the attack that reads it has read it"
  delete_job "$RUN_JOB"
  # A second launch, because this attack's window is the one before a run's
  # bootstrap claims its ticket: the run whose identity every other row reads has
  # been metered by now, so its ticket is already spent and a re-creation carrying
  # it would be refused for being spent rather than by the record. This run's job
  # is deleted while it is still queued, so its VM never exists, its ticket is
  # never claimed, and the record's own job id is the only thing left that can
  # refuse the re-created job.
  say "launching a second sandbox run of ${REPO}: its job is the one this attack deletes while it is still queued"
  local msg
  if ! launch_run "$SCRATCH/launch2.json"; then
    msg=$(oneline <"$SCRATCH/err")
    fact attack-rename FAIL "the second sandbox run of ${REPO} could not be launched (${msg}), so this attack has no job that is still queued and it was NOT made as the plan asks it. Nothing was deleted and nothing was re-created: the launch's own failure is not the attack's refusal"
    return 0
  fi
  RENAME_RUN_ID=$(json_field "$SCRATCH/launch2.json" run_id)
  RENAME_JOB=$(execution_job "$SCRATCH/launch2.json")
  if [ -z "$RENAME_RUN_ID" ] || [ -z "$RENAME_JOB" ]; then
    fact attack-rename FAIL "the second launch answered without naming the run and its job (run_id '${RENAME_RUN_ID:-<none>}', execution job '${RENAME_JOB:-<none>}'), so the job this attack deletes cannot be found and the attack was NOT made. The second run was launched and this check's own sweep deletes its job as the owner"
    return 0
  fi
  # The job is read AS THE SECOND LAUNCHER as soon as the platform publishes it,
  # and the state that read answers is the state the attack acts on — the queued
  # window is short, so it is measured in the same call the attack is made from,
  # and the ticket comes out of that same document. A document that arrives
  # already past QUEUED means the window has gone: that is this row's FAIL and
  # never a silent pass.
  local waited=0 tick="$QUEUE_POLL" state="" ticket=""
  [ "$tick" -gt 0 ] || tick=1
  while :; do
    if ticket=$(ticket_as_second "$RENAME_JOB" queued); then
      state=$(job_fact "$SCRATCH/queued-job-facts" state)
      RENAME_UID=$(job_fact "$SCRATCH/queued-job-facts" uid)
      break
    fi
    [ "$waited" -ge "$WAIT_QUEUED" ] && break
    sleep "$tick"
    waited=$((waited + tick))
    say "waiting for the second run's job ${RENAME_JOB} to be readable (${waited}s of ${WAIT_QUEUED}s)"
  done
  if [ -z "$ticket" ]; then
    fact attack-rename FAIL "the second run's job ${RENAME_JOB} could not be read as the second launcher ${SECOND} within ${WAIT_QUEUED}s, so there is nothing to delete and nothing to carry into a re-creation: gcloud batch jobs describe --impersonate-service-account=${SECOND} --format=json either answered nothing — the impersonation is not allowed from your account (roles/iam.serviceAccountTokenCreator on ${SECOND}), or the job is not there yet — or the document it answered carries no ${TICKET_ENV}. The attack was NOT made. The second run's job is left to this check's own sweep, which deletes it as the installation owner"
    return 0
  fi
  if [ "$state" != QUEUED ]; then
    fact attack-rename FAIL "the second run's job ${RENAME_JOB} was already ${state:-<unread>} when this attack read it, so it was NOT deleted while it was queued and this attack was NOT made as the plan asks it: a run whose VM has come up has claimed its ticket already, and a re-creation carrying that ticket would be refused for being spent rather than by the record's job id (D3108, F30), which is the one thing this attack measures. Nothing was deleted and nothing was re-created. The second run is stopped here rather than left to spend, and this check's own sweep removes its job"
    delete_job "$RENAME_JOB"
    return 0
  fi
  say "deleting the second run's job ${RENAME_JOB} (state ${state}, uid ${RENAME_UID:-<unread>}) as the second launcher, before its VM has claimed its ticket"
  if ! gcloud batch jobs delete "$RENAME_JOB" --project="${P:?}" --location="${L:?}" \
    --impersonate-service-account="$SECOND" --quiet >/dev/null 2>"$SCRATCH/err"; then
    msg=$(oneline <"$SCRATCH/err")
    fact attack-rename FAIL "the second launcher could not delete the second run's job ${RENAME_JOB} (${msg}): the attack was NOT made, so nothing is concluded. The account must hold roles/batch.jobsEditor on ${P} (F4). The job is left to this check's own sweep, which deletes it as the installation owner"
    return 0
  fi
  # The re-creation is retried, bounded, and the reason is the platform's: the
  # delete above was accepted and the name is still the platform's until it has
  # finished with the job (DELETION_IN_PROGRESS — the state the sweep's own
  # comment is about), so a submit under it is refused ALREADY_EXISTS and that
  # refusal is the platform's and not the third attack's. A check that read it
  # as the attack would report the attack refused and never make it. Any other
  # refusal is the submit's own failure and stops the step at once, and the
  # platform's first refusal is kept to be named if the name never comes free.
  local first="" msg2 tick2="$RENAME_POLL" waited2=0
  [ "$tick2" -gt 0 ] || tick2=1
  while :; do
    if submit_attack "$RENAME_JOB" "$ticket"; then break; fi
    msg2=$(oneline <"$SCRATCH/err")
    case "$msg2" in
    *ALREADY_EXISTS* | *"already exists"*) ;;
    *)
      fact attack-rename FAIL "the second run's job ${RENAME_JOB} was deleted while it was queued, and the second launcher could not re-create a job under the same name (${msg2}): the attack was NOT made. That run stopped here — this check's own sweep deletes anything left of it"
      return 0
      ;;
    esac
    [ -n "$first" ] || first=$msg2
    if [ "$waited2" -ge "$WAIT_RENAME" ]; then
      fact attack-rename FAIL "the second run's job ${RENAME_JOB} was deleted while it was queued, and the name is still the platform's after ${waited2}s of waiting, so a job could not be re-created under it and the attack was NOT made. The platform's own answer was '${first}'. A Batch delete is asynchronous: the job stays the platform's — listed, and described as DELETION_IN_PROGRESS — until it has finished with it, and a submit under its name before then is refused ALREADY_EXISTS. Give it longer (CHK831_WAIT_RENAME=<seconds>) and run again: this check waits up to ${WAIT_RENAME}s. That run stopped here, and \`restore\` names what is left"
      return 0
    fi
    say "the name ${RENAME_JOB} is still the platform's to reap ('${msg2}'); waiting, ${waited2}s of ${WAIT_RENAME}s"
    sleep "$tick2"
    waited2=$((waited2 + tick2))
  done
  local uid
  uid=$(job_uid "$RENAME_JOB")
  if ! wait_for_lines "$uid" '^C831 read$' "$SCRATCH/rename-lines" "$WAIT_ATTACK"; then
    fact attack-rename FAIL "a job was re-created under ${RENAME_JOB} (uid ${uid:-<unread>}) carrying the second run's ticket, but no line of its own came back within ${WAIT_ATTACK}s, so the attack was NOT judged. That run's own job is deleted either way: it stopped here"
    return 0
  fi
  claim_row attack-rename "$SCRATCH/rename-lines" "a second run's job was deleted while it was ${state} (uid ${RENAME_UID:-<unread>}, before any VM of that run existed, so its ticket was never claimed) and a job was re-created under the same name (uid ${uid:-<unread>}) carrying that run's ticket, claiming from its own VM"
  delete_job "$RENAME_JOB"
}

# attack-record: the second launcher reads the issuance record directly, at the
# path the tree builds for it. The database's own rules are what must refuse it:
# the record family is unreadable to every caller but the service
# (internal/budget/rules/rules.json.tmpl's creds node).
step_attack_record() {
  if [ -z "$RUN_SLUG" ]; then
    fact attack-record FAIL "the run's job was never found (see \`job-found\`), so the slug the issuance record is keyed by is unread and the path could not be built: this attack was NOT made"
    return 0
  fi
  local path code
  path=$(record_path "$RUN_SLUG" "$RUN_ID")
  say "reading the issuance record ${path} as the second launcher ${SECOND}"
  code=$(rtdb_get "$path" "$SCRATCH/record.json" "$SECOND")
  case "$code" in
  401 | 403)
    fact attack-record PASS "the second launcher ${SECOND} read the issuance record directly at ${path} and the database refused it (HTTP ${code}): the record family is unreadable to a caller that is not the service, which is the rule this attack is about. The body the database answered is not printed here — a refusal's text names the path and never a value"
    ;;
  200)
    fact attack-record FAIL "the second launcher ${SECOND} READ the issuance record at ${path} (HTTP 200): the record holds the run's ticket digest, its expected account, the job and the job's id, and the database's rules are what must refuse this read (internal/budget/rules/rules.json.tmpl's creds node). A record a second launcher can read is the whole mechanism's premise gone"
    ;;
  404)
    fact attack-record FAIL "the database answered HTTP 404 at ${path}: the record is not where the tree says it is, so this attack was NOT made. Either the run was never issued a ticket (see \`launch-ticket\`) or the path family has moved"
    ;;
  *)
    fact attack-record FAIL "the database did not decide the second launcher's read of the issuance record at ${path} (HTTP ${code}): the token may not have been minted (gcloud auth print-access-token --impersonate-service-account=${SECOND}), or the database could not be reached. A read nobody decided is not a refusal, so this row is not measured"
    ;;
  esac
}

# cost: what this sitting cost, out of the run's own ledger, and how many jobs
# the check itself made.
step_cost() {
  if [ "$LEDGER_READ" != yes ]; then
    fact cost FAIL "the run's ledger could not be read (see the \`session\` row), so the cost of this sitting is unknown: this check's own jobs are ${SUBMITS} Batch job(s) of ${MT} (${CPU_MILLI}m/${MEMORY_MIB}MiB) alive for their linger (${LINGER}s), and the run's own spend is in its ledger, which is where it has to be read"
    return 0
  fi
  local dollars
  dollars=$(python3 -c 'import sys
try:
    print("%.4f" % (int(sys.argv[1]) / 1000000.0))
except Exception:
    print("<unread>")' "${LEDGER_SPENT:-0}" 2>/dev/null || echo "<unread>")
  fact cost PASS "the run's own ledger says it spent ${LEDGER_SPENT} micros (\$${dollars}) and ${LEDGER_TOKENS} tokens, with ${LEDGER_RESERVED} micros reserved for it — that run is the real money of this check, and its job was deleted once the attacks had read it. The second launch of this check (the third attack's, run ${RENAME_RUN_ID:-<unread>}) never came up: its job was deleted while it was queued, before its VM existed, so it was never metered and spent nothing. This check's own jobs were ${SUBMITS} Batch job(s) of ${MT} (${CPU_MILLI}m/${MEMORY_MIB}MiB), each alive for its linger (${LINGER}s) and each deleted once its line was read: no image was built and no model call was made by the check itself"
}
# ------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was run, and no command reached a cloud project. A real run would run, in this order:"
  say "  [local]  guards: no xtrace, the owner marker, the tree this script lives in, and the project refusal (with the second launcher's own shape and the account gcloud is authenticated as)"
  say "  [local]  the tree reads: the ticket's variable, the claim's route and the record's path family out of internal/identitywire/wire.go, the escaping out of internal/budget/model.go, the run's own variable out of internal/backend/batch/env.go, the run label out of internal/backend/batch/names.go and the two URL variables out of internal/runner/budget_session.go and internal/backend/batch/env.go"
  say "  [local]  the installation's own configuration: \${HOME}/.config/fugaro/projects/fugaro.yaml, which must say ${P}/${L}"
  say "  [cloud]  fugaro run --repo ${REPO:-<owner/name>} --task-file <scratch>/task.md --json$([ -n "$MAX_USD" ] && echo " --max-usd ${MAX_USD}") — ONE real sandbox run on Batch, the run whose identity the four attacks are about"
  say "  [cloud]  gcloud batch jobs describe — the job the launch answered, held to the run by its own ${RUN_ENV} variable (uid, state, account, variables)"
  say "  [cloud]  curl ${RTDB:-<rtdb_url>}/runs/<slug>/<run>.json with your own access token — until the ledger says the run opened its session, bounded at ${WAIT_SESSION}s, and then until it says the run was metered, bounded at ${WAIT_METERED}s"
  say "  [cloud]  gcloud logging read (labels.job_uid=\"<uid>\" AND logName=\"${TASK_LOG}\") and the CreateJob audit filter, waited for within ${WAIT_AUDIT}s — where Batch printed the ticket (the job's own document is the reading the check took it from)"
  say "  [cloud]  gcloud batch jobs describe ${RUN_JOB:-<the job of this run>} --impersonate-service-account=<second launcher> --format=json — the first attack's own read of the run's job, which is where it takes the ticket it replays from"
  say "  [cloud]  gcloud batch jobs submit ${JOB_REPLAY} --impersonate-service-account=<second launcher> — the replay: the second launcher's own job, running as the run's own account, carrying the ticket its own read of the job found, claiming from its own VM"
  say "  [cloud]  gcloud batch jobs submit ${JOB_NOTICKET} --impersonate-service-account=<second launcher> — the same job with no ticket"
  say "  [cloud]  gcloud batch jobs delete ${RUN_JOB:-<the job of this run>} --project=${P} --location=${L} --quiet — as the OWNER and not as an attack: the run under test has been read by the attack that reads it, and its job is deleted here so it stops"
  say "  [cloud]  fugaro run --repo ${REPO:-<owner/name>} --task-file <scratch>/task.md --json$([ -n "$MAX_USD" ] && echo " --max-usd ${MAX_USD}") — a SECOND real sandbox run, launched only for the third attack: its job is deleted while it is still QUEUED, before its VM exists, so it never comes up and never spends"
  say "  [cloud]  gcloud batch jobs delete <the second launch's job> --impersonate-service-account=<second launcher> — while it is QUEUED — then gcloud batch jobs submit under the SAME name carrying that run's unspent ticket — the re-creation, claiming from its own VM, retried within ${WAIT_RENAME}s because a Batch delete is asynchronous and the name stays the platform's (DELETION_IN_PROGRESS) until it has finished with the job. Nothing but the record's job id can refuse that claim (D3108, F30)"
  say "  [cloud]  curl ${RTDB:-<rtdb_url>}/${RECORDS_PATH}/<slug>/<run>.json with the second launcher's access token — the direct read of the issuance record, which the database's rules must refuse"
  say "  [local]  the exit trap deletes every job of this check's by its label, the two runs' jobs by the label the backend writes on them, and removes the scratch directory"
  say "A dry run runs the guards and the tree reads only. It launches no run, makes no attack, and touches no cloud project"
}

# ------------------------------------------------------------------- reporting
BLOCK_KEYS=(xtrace owner tree belong installation launch job-found session metered launch-ticket ticket-printed attack-replay attack-noticket attack-rename attack-record cost restore)

verdict_of() {
  if grep -qE '^[a-z0-9-]+\|FAIL\|' <<<"$1"; then
    printf 'FAIL\n'
  else
    printf 'PASS\n'
  fi
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 831 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    # A key's FAIL is what the block's own last line is read from, so a key that
    # recorded a FAIL beside anything else is printed as that FAIL: the first
    # fact of a key wins only where it is not hiding a FAIL behind it.
    line=$(grep -m1 "^$key|FAIL|" <<<"$facts" || true)
    [ -n "$line" ] || line=$(grep -m1 "^$key|" <<<"$facts" || true)
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
  printf 'CHECK 831 RESULT: %s\n' "$(verdict_of "$facts")" >&3
}

step_manual() {
  MANUAL_LINE="paste this block into docs/gcp-live-checklist.md's Check 831 section, with the date and \`fugaro version\`: whether the launcher put a ticket on the run's job, where Batch printed it (the job's document, the logs, the audit record — D3115's rule measured), and what each of the four attacks was answered. The four attacks are the proof R11 asks for: a FAIL on \`attack-replay\` says the identity of a Batch run was served to a VM that is not its job's, and a FAIL on \`attack-rename\` says a job re-created under a deleted job's name was not refused by the record's own job id (D3108, F30) — the identity is obtainable, and the work returns to the plan rather than being recorded as a pass"
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
USAGE="check-831.sh --second-launcher <account> [--repo owner/name] [--dry-run]"

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --second-launcher)
      shift
      [ $# -gt 0 ] || die "--second-launcher needs the account's address. usage: $USAGE"
      SECOND=$1
      ;;
    --second-launcher=*) SECOND=${1#*=} ;;
    --repo)
      shift
      [ $# -gt 0 ] || die "--repo needs owner/name. usage: $USAGE"
      REPO=$1
      ;;
    --repo=*) REPO=${1#*=} ;;
    --dry-run) DRY_RUN=1 ;;
    -h | --help) HELP=1 ;;
    *) die "unknown argument '$1'. usage: $USAGE" ;;
    esac
    shift
  done
  REPO=${REPO:-${FUGARO_LIVE_REPO:-}}
  if [ -z "$HELP" ]; then
    case "$REPO" in
    "") die "no sandbox repository was named, and this check never guesses one to launch a run in. usage: $USAGE — or export FUGARO_LIVE_REPO=<owner/name>" ;;
    */*)
      case "$REPO" in
      *" "* | /* | */ | */*/*) die "--repo '${REPO}' is not owner/name" ;;
      esac
      ;;
    *) die "--repo '${REPO}' is not owner/name (a bare name is not a repository this check can launch a run in)" ;;
    esac
  fi
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
  write_attack_container

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "guards and the tree reads only: the commands above were printed, not run, and no cloud project, no credential and no resource was touched"
    fact installation INFO "not taken: this is a dry run, so the installation's own configuration was not read"
    fact launch INFO "not taken: this is a dry run, so no run was launched"
    fact job-found INFO "not taken: this is a dry run, so no Batch job was read"
    fact session INFO "not taken: this is a dry run, so no ledger was read"
    fact metered INFO "not taken: this is a dry run, so no spend and no tokens were read"
    fact launch-ticket INFO "not taken: this is a dry run, so no job's variables were read"
    fact ticket-printed INFO "not taken: this is a dry run, so no log and no audit record was read"
    fact attack-replay INFO "not taken: this is a dry run, so no attack job was submitted"
    fact attack-noticket INFO "not taken: this is a dry run, so no control job was submitted"
    fact attack-rename INFO "not taken: this is a dry run, so no second run was launched, no job was deleted and none was re-created"
    fact attack-record INFO "not taken: this is a dry run, so the issuance record was not read"
    fact cost INFO "not taken: this is a dry run, so nothing was spent"
  else
    step_installation
    step_launch
    step_job_found
    step_session
    step_launch_ticket
    step_ticket_printed
    step_attack_replay
    step_attack_noticket
    step_attack_rename
    step_attack_record
    step_cost
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
