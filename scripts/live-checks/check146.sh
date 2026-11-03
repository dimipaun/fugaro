#!/usr/bin/env bash
# Check 146 — the coordinator sweep reads a Bitbucket repository through the
# credentials service, as ONE script YOU run.
#
# It settles the live half of the Bitbucket-credentials work's BB5 on a sandbox
# installation: the coordinator's own job carries the read mint's variable and
# not the run path's (F23), a tick reconciles a Bitbucket repository instead of
# marking it unsupported, and the installation's GitHub views are exactly what
# they were before the tick. The register's row and section are
# docs/gcp-live-checklist.md, "Check 146".
#
# Usage:      bash scripts/live-checks/check146.sh --installation <sandbox>
#             The name is a GCP project ID, and it has to be the project the
#             local config in force names (FUGARO_CONFIG, else the default):
#             `fugaro doctor --json` is asked first, and a project that is not
#             the one you typed is a STOP before anything is run — this check
#             never decides for itself which installation it is looking at.
# Needs:      bash, mktemp, python3, gcloud and fugaro on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      SANDBOX installations only, and never `belong`: the name is
#             refused, whether you typed it as the installation or the config
#             answers it. It creates nothing, deletes nothing and changes no
#             cloud state of its own: the one thing it causes is a sweep, which
#             is the coordinator job doing its ordinary work on a sandbox.
# Costs:      one Cloud Run job execution (a sweep, bounded by the job's own
#             timeout) and the reads around it. No model call, no run.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed.
#             Nothing else was created, so the `restore` row says that and
#             whether the directory really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 146 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, the sweep did not run).
#
# The sweep's own log is read as a fixed vocabulary and never quoted: a
# Bitbucket repository's line is one of reconciled, stale or absent, and the
# sentence behind a stale line is looked for by its own words rather than
# pasted into the block. What the block prints is this check's words about the
# run — counts, yes/no, and the names of the repositories you already named in
# the job's own environment — so nothing a sweep or a pull request wrote
# reaches it. And no trace: `bash -x` prints every command and every document
# this check reads as it reads it, so guard_xtrace REFUSES a run whose shell
# arrived with tracing on.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. The documents this
# check reads carry pull request titles and branch names written by whoever
# opened them, and a trace would print them in full into the log the owner
# would then paste back. So the trace is turned off here, BASH_XTRACEFD is
# unset so a trace redirected to a file this script knows nothing about cannot
# be reopened, and guard_xtrace refuses a shell that arrived tracing (the flag
# sits in SHELLOPTS/BASHOPTS then, and every child would start tracing on its
# own). Check 145 spells the three parts out; this is the same guard.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
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

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as "the sweep is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the sweep" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 146 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 146 needs bash"
  need mktemp
  need python3 "the reader that parses doctor's answer, the job's environment and the stored views is one python3 file"
  need gcloud
  need fugaro "the stored view is read with fugaro coordinator status --from-store, which is the read a console makes"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk146-mine"
SCRATCH_REMOVED=no
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk146.XXXXXXXX") || die "Check 146 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 146 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
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
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the sweep when the steps that
  # would have filled them never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the sweep"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|this check created nothing and changed no cloud state of its own — the one thing it caused is the sweep, which is the coordinator job doing its ordinary work — and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the documents this check reads — pull request titles and branch names other people wrote — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check146.sh --installation <sandbox> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a document it read"
}

# guard_sandbox: `belong` is the owner's own installation and this check runs a
# job on whatever it is pointed at, so the name is refused here — before doctor
# is asked, before a job is looked for — and doctor's own answer is refused the
# same way a moment later, because the name you type and the config in force are
# two things and the check has to see the same one twice.
guard_sandbox() {
  case "$INSTALLATION" in
  belong | belong-*)
    fact installation FAIL "the installation named is $INSTALLATION, which is the owner's own: Check 146 runs a job and reads the coordinator's store, and it is a sandbox check"
    stop "Check 146 never runs against 'belong'. Name a sandbox installation: bash scripts/live-checks/check146.sh --installation <sandbox>"
    ;;
  esac
}

# --------------------------------------------------------------- the requests
# run_capture OUTFILE CMD... — runs a read, keeps stdout in OUTFILE, and leaves
# its exit status in $?. stderr is shown to the owner and never parsed: it is
# where gcloud and fugaro put their progress, and a warning on it is not a
# failed read.
run_capture() {
  local out=$1
  shift
  "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    return "$status"
  }
}

# reader SUBCOMMAND [ARGS] — the python3 reader that parses what the tools
# print. It prints a fixed vocabulary and never a document: `name=`, `project=`,
# `region=`, `repo=`, `present`, `absent`, `stored=yes`, `stale=no`, `records=3`,
# `same`, `different`, `bitbucket=reconciled`, and the like.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# ------------------------------------------------------------------ the steps
# step_installation asks the local config which installation it is looking at
# and holds it to the name that was typed. doctor is the only thing that reads
# the config here, and its --json answer is the whole of what this step wants:
# the project and the region the rest of the run addresses.
step_installation() {
  if ! run_capture "$SCRATCH/doctor.json" fugaro doctor --json; then
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local answer name project region
  answer=$(reader doctor <"$SCRATCH/doctor.json") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  name=$(field_of name "$answer")
  project=$(field_of project "$answer")
  region=$(field_of region "$answer")
  [ -n "$region" ] || stop "fugaro doctor --json named no region, so this check cannot address the job: the local config has to carry one"
  PROJECT=$project
  REGION=$region
  NAME=$name
  # The name typed is held to the config twice: a config naming another
  # installation is the same refusal as naming `belong` by hand, because the
  # check would otherwise read one installation's job and compare another's
  # store — which is what this equality is here for, and why guard_sandbox's
  # name guard above is enough for the owner's own.
  if [ "$PROJECT" != "$INSTALLATION" ]; then
    fact installation FAIL "you named $INSTALLATION and the config in force is the $PROJECT installation: set FUGARO_CONFIG to the sandbox's own config, or name $PROJECT — this check never decides for itself which installation it reads"
    stop "the installation named ($INSTALLATION) is not the one the local config in force describes ($PROJECT)"
  fi
  fact installation PASS "the config in force is the $NAME installation: project $PROJECT, region $REGION"
}

# step_job finds the coordinator's job by its own label rather than by a name
# this script assumes: the job's name is the installation's to choose
# (coordinator.job), and the label is what fugaro's own Terraform writes on it.
step_job() {
  if ! run_capture "$SCRATCH/jobs.json" gcloud run jobs list --project "$PROJECT" --region "$REGION" --format=json; then
    stop "gcloud run jobs list did not answer for $PROJECT/$REGION: the check needs an account that may read Cloud Run there"
  fi
  local answer
  answer=$(reader joblist <"$SCRATCH/jobs.json") || stop "no coordinator job is deployed in $PROJECT/$REGION (the job carries the label fugaro_role=coordinator). Turn the coordinator on first: fugaro update --firebase-project <id> --coordinator"
  JOB=$(field_of job "$answer")
  [ -n "$JOB" ] || stop "the coordinator job could not be named from the project's job list"
  fact job PASS "the installation's coordinator job is $JOB"
}

# step_job_env reads the job's own environment: which repositories it
# reconciles, where its Bitbucket reads are minted, and whether the run path's
# variable is there — the last of which is F23, the fact that a run's
# environment cannot move the root a sweep's token is minted at.
step_job_env() {
  if ! run_capture "$SCRATCH/job.json" gcloud run jobs describe "$JOB" --project "$PROJECT" --region "$REGION" --format=json; then
    stop "gcloud run jobs describe did not answer for $JOB"
  fi
  local answer
  answer=$(reader job <"$SCRATCH/job.json") || stop "the coordinator job's environment is not the document this check reads: FUGARO_COORDINATOR_REPOS has to be the JSON list of repositories the job reconciles"
  BITBUCKET_REPOS=()
  GITHUB_REPOS=()
  # The repository lines are the reader's only two-field ones, so they are cut
  # apart here rather than by field_of, which returns everything after the key.
  local line rest kind repo
  while IFS= read -r line; do
    case "$line" in
    kind=*) ;;
    *) continue ;;
    esac
    rest=${line#kind=}
    kind=${rest%% *}
    repo=${rest#* repo=}
    case "$kind" in
    bitbucket) BITBUCKET_REPOS+=("$repo") ;;
    github) GITHUB_REPOS+=("$repo") ;;
    esac
  done <<<"$(printf '%s\n' "$answer" | grep '^kind=' || true)"
  local service_url run_url
  service_url=$(field_of service_url "$answer")
  run_url=$(field_of run_url "$answer")
  if [ "${#BITBUCKET_REPOS[@]}" -eq 0 ]; then
    fact repositories FAIL "the job reconciles ${#GITHUB_REPOS[@]} repositories and none of them is on Bitbucket: this check measures the Bitbucket path, so a sandbox installation without one is not a run of it (onboard one with fugaro setup --repo <owner/name> in a Bitbucket checkout)"
    stop "Check 146 needs a Bitbucket repository in the coordinator job's own repository list (FUGARO_COORDINATOR_REPOS)"
  fi
  fact repositories PASS "the sweep reconciles ${#GITHUB_REPOS[@]} github and ${#BITBUCKET_REPOS[@]} bitbucket repositories; the bitbucket one is ${BITBUCKET_REPOS[0]}"
  # The run path's variable is looked for first: a job carrying both would
  # otherwise be reported as one carrying the right mint, which is the reading
  # that hides the thing F23 exists for.
  if [ "$run_url" = present ]; then
    fact read-mint FAIL "the job carries FUGARO_CREDENTIALS_SERVICE_URL, which is the RUN path's variable: a run's environment must not be able to move the root a sweep's token is minted at (F23). Its own FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL is $service_url"
    stop "the coordinator job carries the run path's credentials service variable"
  elif [ "$service_url" = absent ]; then
    fact read-mint FAIL "the job carries no FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL, so its Bitbucket repositories stay StaleUnsupported: the credentials service is off, or this job was deployed before BB5 — run fugaro update --firebase-project <id> and read the sweep's own log"
    stop "the coordinator job carries no read service URL, so there is no read mint to exercise (G8: without the variable a Bitbucket repository stays StaleUnsupported)"
  else
    fact read-mint PASS "the job carries FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL and not FUGARO_CREDENTIALS_SERVICE_URL: its Bitbucket reads are minted at the service's own URL, which no run's environment names (F23)"
  fi
}

# step_view REPO FILE — reads one repository's stored view and writes the view
# document itself, canonically, to FILE. Only the fixed vocabulary the reader
# prints is kept in the shell.
step_view() {
  local repo=$1 file=$2
  if ! run_capture "$SCRATCH/view.json" fugaro coordinator status --from-store --repo "$repo" --json; then
    return 1
  fi
  reader view "$file" <"$SCRATCH/view.json"
}

# step_before reads every repository's stored view as the sweep left it before
# this run: the GitHub half is the baseline the sweep must not move, and the
# Bitbucket half is the state the sweep is here to change.
step_before() {
  local repo
  BEFORE_OK=yes
  for repo in "${GITHUB_REPOS[@]}" "${BITBUCKET_REPOS[@]}"; do
    local slug
    slug=$(slug_of "$repo")
    if ! step_view "$repo" "$SCRATCH/before-$slug.json" >"$SCRATCH/before-$slug.facts"; then
      BEFORE_OK=no
      say "warning: $repo's stored view could not be read before the sweep; this run cannot compare it"
      continue
    fi
    say "$repo before: $(tr '\n' ' ' <"$SCRATCH/before-$slug.facts")"
  done
}

# step_sweep runs the tick. It is the one thing this check causes, and it is the
# coordinator job's ordinary work: one execution, waited for, on the sandbox.
step_sweep() {
  local status=0
  run_capture "$SCRATCH/exec.txt" gcloud run jobs execute "$JOB" --project "$PROJECT" --region "$REGION" --wait --format='value(metadata.name)' || status=$?
  if [ "$status" -ne 0 ]; then
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    stop "the sweep did not run: gcloud run jobs execute exited $status. Read the execution's log in the console (the job is $JOB in $PROJECT/$REGION) and fix what it reports"
  fi
  EXEC=$(head -n 1 "$SCRATCH/exec.txt" | tr -d '[:space:]')
  [ -n "$EXEC" ] || stop "the execution answered no name, so this run cannot read its log: gcloud run jobs execute --wait has to print the execution's name"
  fact sweep PASS "the sweep ran: execution $EXEC of $JOB finished with exit 0"
}

# step_log reads that execution's own log and reports, in three words, what it
# says about the repositories: the Bitbucket one's line (reconciled, stale, or
# absent), whether the unsupported marker's sentence is anywhere in it, and
# whether the tick's own summary line is there.
#
# This is the register's red step. A sweep that still marks the Bitbucket
# repository unsupported prints `<repo>: stale: <reason>` with that sentence
# behind it, and the check FAILs on exactly that — the failure the work exists
# to remove.
step_log() {
  local filter
  filter=$(printf 'resource.type=cloud_run_job AND resource.labels.job_name="%s" AND labels."run.googleapis.com/execution_name"="%s"' "$JOB" "$EXEC")
  if ! run_capture "$SCRATCH/log.json" gcloud logging read "$filter" --project "$PROJECT" --freshness="${CHECK146_LOG_FRESHNESS:-30m}" --limit=1000 --format=json; then
    stop "gcloud logging read did not answer for $EXEC's log: the check needs an account that may read Cloud Logging in $PROJECT"
  fi
  local answer
  LOCAL_LOG_LINES=0
  answer=$(reader log "${BITBUCKET_REPOS[0]}" <"$SCRATCH/log.json") || stop "the execution's log is not the document this check reads"
  LOCAL_LOG_LINES=$(field_of lines "$answer")
  local bitbucket unsupported summary
  bitbucket=$(field_of bitbucket "$answer")
  unsupported=$(field_of unsupported "$answer")
  summary=$(field_of summary "$answer")
  if [ "$bitbucket" = stale ]; then
    fact log FAIL "the sweep's log shows ${BITBUCKET_REPOS[0]} as stale: it was NOT read, and the unsupported marker's own sentence is ${unsupported} in the log. The tick's summary line is ${summary}: read the job's environment (the read mint's variable) and the credentials service's log"
    return 1
  fi
  if [ "$bitbucket" != reconciled ]; then
    fact log FAIL "the sweep's log carries no line for ${BITBUCKET_REPOS[0]} at all (${LOCAL_LOG_LINES} lines read): the tick did not reconcile it, whatever it did with the rest"
    return 1
  fi
  fact log PASS "the sweep's log reports ${BITBUCKET_REPOS[0]} as reconciled and names no unsupported marker (${LOCAL_LOG_LINES} lines read, the tick's summary line ${summary})"
}

# step_after reads the stored views again: the Bitbucket repository's marker is
# gone and its view is stored, and every GitHub repository's view is the one
# read before the sweep, byte for byte.
step_after() {
  local slug
  slug=$(slug_of "${BITBUCKET_REPOS[0]}")
  local answer stored stale records
  answer=$(step_view "${BITBUCKET_REPOS[0]}" "$SCRATCH/after-$slug.json") \
    || stop "the stored view of ${BITBUCKET_REPOS[0]} could not be read after the sweep"
  stored=$(field_of stored "$answer")
  stale=$(field_of stale "$answer")
  records=$(field_of records "$answer")
  if [ "$stored" = yes ] && [ "$stale" = no ]; then
    fact bitbucket-view PASS "the sweep stored ${BITBUCKET_REPOS[0]}'s view ($records records) and left no stale marker: the repository was read through the read mint"
  else
    fact bitbucket-view FAIL "the sweep left ${BITBUCKET_REPOS[0]} unstored or marked stale (stored=$stored, stale=$stale), so the repository was not read: read the sweep's log and the credentials service's own log"
    return 1
  fi

  if [ "$BEFORE_OK" != yes ]; then
    fact github-views FAIL "the GitHub views could not be compared: a stored view was unreadable before the sweep, and a run that measures nothing is not a pass"
    return 1
  fi
  local repo changed=""
  for repo in ${GITHUB_REPOS[@]+"${GITHUB_REPOS[@]}"}; do
    slug=$(slug_of "$repo")
    answer=$(step_view "$repo" "$SCRATCH/after-$slug.json") \
      || stop "the stored view of $repo could not be read after the sweep"
    if [ "$(reader same "$SCRATCH/before-$slug.json" "$SCRATCH/after-$slug.json")" != same ]; then
      changed="$repo"
      break
    fi
  done
  if [ -n "$changed" ]; then
    fact github-views FAIL "$changed's stored view is not the one the sweep found: the Bitbucket path must be an addition to the sweep and not a change to what it already read (G8). A run that finished in the window would do this honestly — read the two views and check whether the installation had work in flight"
    return 1
  fi
  fact github-views PASS "all ${#GITHUB_REPOS[@]} github repositories' stored views are what they were before the tick, byte for byte"
}

# field_of NAME TEXT — the value of a NAME= line the reader printed.
field_of() {
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# slug_of REPO — the file-name spelling this check uses for a repository. It is
# the check's own and not the coordinator's (that one is a hash of the provider
# and the name, task.Slug): it only has to be a name two repositories cannot
# share, and the separator is the owner's, so `owner/name` stays distinct.
slug_of() { printf '%s' "$1" | sed 's|/|__|g'; }

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(installation job repositories read-mint sweep log bitbucket-view github-views xtrace restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 146 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 146 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
USAGE="check146.sh --installation <sandbox>"

parse_args() {
  INSTALLATION=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a project ID. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation is required. usage: $USAGE"
}

main() {
  need_tools
  parse_args "$@"
  guard_sandbox

  PROJECT=""
  REGION=""
  NAME=""
  JOB=""
  EXEC=""
  BEFORE_OK=no
  BITBUCKET_REPOS=()
  GITHUB_REPOS=()

  scratch_up
  write_files
  guard_xtrace
  step_installation
  step_job
  step_job_env
  step_before
  step_sweep
  step_log || true
  step_after || true

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

write_files() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 146's reader: every document this check reads is parsed here.

It prints a fixed vocabulary and never a document. doctor's answer becomes
name=, project= and region=; the job's environment becomes one `kind=<provider>
repo=<owner/name>` line per repository plus present/absent for the two service
URLs; a stored view becomes stored=, stale= and records=; the sweep's log
becomes three classified words; and `same` compares two view documents it has
already canonicalised.

The stored view the check keeps is the `view` document alone, written with its
keys sorted, so two reads of one repository can be compared byte for byte
without the check ever printing a pull request's title.
"""
import json, sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def load(path=None):
    raw = open(path, encoding="utf-8").read() if path else sys.stdin.read()
    try:
        return json.loads(raw)
    except ValueError as e:
        fail("not JSON: %s" % e)


def env_of(job):
    """The job's container environment, as a dict: a job with no containers is
    an empty one rather than an error, so the caller reports what it found."""
    out = {}
    spec = (job.get("spec") or {}).get("template") or {}
    spec = (spec.get("template") or {}).get("containers") or []
    for container in spec:
        for entry in container.get("env") or []:
            value = entry.get("value")
            if value is None:
                value = ""  # a value_source entry: a mounted secret, not a URL
            out[entry.get("name") or ""] = value
    return out


def cmd_doctor():
    doc = load()
    project = doc.get("project") or {}
    print("name=%s" % (project.get("name") or ""))
    print("project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))


def cmd_joblist():
    found = []
    for job in load():
        labels = (job.get("metadata") or {}).get("labels") or {}
        if labels.get("fugaro_role") == "coordinator":
            found.append(job)
    if len(found) != 1:
        fail("the project lists %d coordinator jobs" % len(found))
    print("job=%s" % ((found[0].get("metadata") or {}).get("name") or ""))


def cmd_job():
    job = load()
    env = env_of(job)
    raw = env.get("FUGARO_COORDINATOR_REPOS") or ""
    if not raw:
        fail("the job carries no FUGARO_COORDINATOR_REPOS")
    try:
        repos = json.loads(raw)
    except ValueError as e:
        fail("FUGARO_COORDINATOR_REPOS is not JSON: %s" % e)
    for r in repos:
        print("kind=%s repo=%s" % (r.get("provider") or "", r.get("repo") or ""))
    print("service_url=%s" % ("present" if env.get("FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL") else "absent"))
    print("run_url=%s" % ("present" if env.get("FUGARO_CREDENTIALS_SERVICE_URL") else "absent"))


def cmd_view(path):
    doc = load()
    view = doc.get("view") or {}
    open(path, "w", encoding="utf-8").write(json.dumps(view, sort_keys=True, separators=(",", ":")))
    stale = doc.get("stale") or {}
    print("stored=%s" % ("yes" if doc.get("stored") else "no"))
    print("stale=%s" % ("yes" if stale else "no"))
    print("records=%d" % len(view.get("records") or []))


def cmd_same(a, b):
    print("same" if open(a, "rb").read() == open(b, "rb").read() else "different")


def cmd_log(repo):
    entries = load()
    lines = []
    for entry in entries:
        text = entry.get("textPayload")
        if text is None:
            text = ((entry.get("jsonPayload") or {}).get("message")) or ""
        for line in str(text).splitlines():
            lines.append(line.strip())
    # The marker's own sentence, as the sweep's package writes it
    # (coordinator.StaleUnsupported). It is looked for by its words rather than
    # printed: what reaches the block is one of the three words below.
    unsupported = "present" if any("the project-wide read serves github only, so this repository was not read" in l for l in lines) else "absent"
    mine = [l for l in lines if l.startswith(repo + ": ")]
    verdict = "absent"
    for line in mine:
        verdict = "stale" if line.startswith(repo + ": stale:") else "reconciled"
        break
    summary = "present" if any(l.startswith("swept ") for l in lines) else "absent"
    print("bitbucket=%s" % verdict)
    print("unsupported=%s" % unsupported)
    print("summary=%s" % summary)
    print("lines=%d" % len(lines))


def main(argv):
    if not argv:
        fail("no subcommand")
    cmd = argv[0]
    if cmd == "doctor":
        cmd_doctor()
    elif cmd == "joblist":
        cmd_joblist()
    elif cmd == "job":
        cmd_job()
    elif cmd == "view":
        cmd_view(argv[1])
    elif cmd == "same":
        cmd_same(argv[1], argv[2])
    elif cmd == "log":
        cmd_log(argv[1])
    else:
        fail("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}
trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
