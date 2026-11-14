#!/usr/bin/env bash
# Check LC4 — the account's job mounts its own token and no other account's.
#
# docs/accounts-add/spec.md §9, LC4, is the fourth of the four live checks the
# accounts-add plan defers to the owner (D870, the plan's `LC1–LC4` row). LC1,
# LC2 and LC3 are human-terminal procedures and are steps in
# docs/gcp-live-checklist.md's own section; LC4 is the one that is a reading of
# the cloud, and this is its script. The design behind it is
# docs/design/named-oauth-tokens.md, option B / T3: one job variant per account.
#
# What it asserts, after LC3's `fugaro accounts add <name> --claude` and the
# `fugaro update` that deploys it (or after any installation that lists a named
# account, which is why this check is not tied to one installation):
#   - exactly ONE job of the installation wears this account's own labels
#     (`fugaro=managed`, `fugaro_profile=<profile>`, `fugaro_account=<account>`);
#   - that job's environment carries exactly ONE secret-backed entry named
#     CLAUDE_CODE_OAUTH_TOKEN, and the secret it mounts is the account's own
#     derived secret (its `fugaro_secret` label is `claude-oauth-token-<name>`,
#     internal/config's ClaudeAccountSecret);
#   - the profile's own mounts are on the same job (the GitHub App key, as
#     FUGARO_GITHUB_APP_PRIVATE_KEY, out of the secret labelled
#     `github-app-key`) — "beside the profile's own mounts" is half of LC4's
#     sentence, and a job that had lost them would still pass the two rows above;
#   - no OTHER account's token secret is mounted on that job;
#   - the job's own service account holds roles/secretmanager.secretAccessor on
#     that secret (deploy/terraform/gcp/modules/project/sa.tf grants it, one
#     member per secret the job's secret_env names) …
#   - … and on NO other account's: every other secret in the project whose
#     `fugaro_secret` label starts with `claude-oauth-token` is read back, and
#     the job's account must appear in none of their policies. That is the
#     isolation T3 is for — one account's job can read one account's token;
#   - `fugaro doctor --json` is green.
#
# The job's NAME and the secret's ID are hash-derived (gcp.ProjectJobName,
# gcp.ProjectSecretID: a readable part plus hex of a hash of the tuple), so this
# check never computes either. It finds the job by its labels and the secret by
# the label on it, which is also what makes it read whatever the installation
# actually deployed rather than what this tree would deploy today.
#
# A project-wide reader of an account's secret is NOT what the last two rows
# read: they read each secret's own IAM policy, and a `roles/owner` or a
# project-level secretAccessor binding is not in one. `fugaro doctor`'s
# secret-reader sweep is where that shows up (internal/cli/doctor_secret_readers.go
# reads the project's basic roles beside each container's own grants), which is
# one of the reasons the `doctor` row is part of this check and not an
# afterthought.
#
# No secret VALUE is read, asked for or printed anywhere in it: every gcloud
# call here is a describe, a list or a get-iam-policy, and the reader prints
# secret NAMES and LABELS alone. The one credential-shaped thing this check
# touches is the token's own secret ID, which is a name.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 600".
#
# Usage:      bash scripts/live-checks/check-lc4.sh --project <project> --account <name>
#             [--profile <profile>] [--region <region>] [--dry-run]
#             --project is the GCP project of the installation whose account you
#             are reading (internal/cli's --gcp-project, not Fugaro's own project
#             name), and it must be the project `fugaro doctor --json` names: the
#             check stops rather than read another installation's job. --account
#             is the name you gave `fugaro accounts add`. --profile defaults to
#             `standard` (config.DefaultJobProfile, the profile a workflow that
#             names none runs on). --region defaults to the region doctor
#             records. `--dry-run` prints every command a real run would make and
#             runs none of them: no gcloud call, no fugaro call, no verdict.
# Needs:      bash, mktemp, sed, python3 (the reader that parses the four
#             documents), gcloud and fugaro on PATH. Nothing is installed and
#             nothing is fetched.
# Where:      any installation, sandbox or not — it writes nothing — except the
#             owner's own `belong` (and the other names refused below), which is
#             refused before the first call.
# Creates:    NOTHING. Every call this check makes is a read; its Restore has
#             nothing to undo and says so.
#
# CHECK_LC4_DRY_RUN=1 is --dry-run by another name, for this script's own test.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the block this check
# prints is the thing you paste back, and a trace of the run would be pasted
# with it. This check reads secret NAMES and never a value, so a trace would
# carry no secret — the guard is here so that the one paste-back you keep holds
# this check's own words and nothing else.
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
# Set to yes by step_verdict once every step has run, so the exit trap can tell
# a finished run from one that ended on an error of its own.
REACHED_END=no
# HELP_SHOWN=yes is the one ending that prints no RESULT block: `--help` is not
# a run of the check.
HELP_SHOWN=no
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
# all, and the block would then read as an answer about the account's mounts for
# a run that measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the account's job, its mounts or its readers"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check LC4 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check LC4 needs bash"
  need mktemp
  need sed
  need python3 "the reader that parses the job, the secrets and the two IAM policies is one python3 file"
  need gcloud "every reading this check makes of the cloud is a gcloud one: the job list, the job, the secrets and their policies"
  need fugaro "the installation's own project, region and verdict are read with fugaro doctor --json"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk-lc4-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# The readings the steps fill in, so the Restore and the rows can name what was
# read rather than assume it.
P=""
ACCOUNT=""
ACCOUNT_LABEL=""
ACCOUNT_SECRET=""
PROFILE=""
R=""
DRY_RUN=${CHECK_LC4_DRY_RUN:-}
DOC_PROJECT=""
DOC_REGION=""
JOB=""
JOBSA=""
JOB_MOUNTS=""
TOKEN_SECRET=""
# Set by step_token once the account's own mount was read and held to its label:
# the two rows under it are about that secret, so they are read only when there
# is one, and the block says so for the rest.
TOKEN_READ=no
# Whether every criterion held, and whether any of them FAILED — two different
# readings the verdict has to tell apart: a FAIL is a reading about the account,
# while a row that could not be read at all is no reading, and the two end the
# run with different answers.
CRITERIA_FAILED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk-lc4.XXXXXXXX") || die "Check LC4 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check LC4 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  restore_run
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

# --------------------------------------------------------------- the Restore
# This check makes no write, so its Restore has nothing to undo — and it says
# that in the past tense it can stand behind: every call the run made was a
# describe, a list or a get-iam-policy. It runs from the exit trap, so it also
# has to be honest about a run that stopped before it made one.
restore_run() {
  if [ -z "$SCRATCH" ]; then
    fact restore PASS "this check reads only (fugaro doctor --json, gcloud run jobs list/describe, gcloud secrets list/describe/get-iam-policy), and it stopped before it made a single call: nothing was created, granted, changed or deleted, and nothing was left behind"
    return 0
  fi
  fact restore PASS "this check reads only: it created, granted, changed and deleted nothing, so there is nothing to undo and nothing of its own to remove. Every call it made was a read (fugaro doctor --json, gcloud run jobs list/describe, gcloud secrets list/describe/get-iam-policy), and no secret value is read by any of them"
}

# ------------------------------------------------------------------ the guards
# guard_xtrace: the one guard this check shares with Check 96 rather than Check
# 340. The shell it was started from may be tracing, and a trace prints every
# variable a line assigns; this check assigns no credential, but its block is
# the paste-back and a traced run would carry the trace too.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), so the trace would be pasted back with the block. This check reads secret names and never a value, so the trace carries no secret — but the block is meant to hold this check's own words and nothing else"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-lc4.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so what you paste back is this check's own rows"
}

# guard_belong: the owner's own installation is named `belong`, and the register
# refuses it in every check that touches an installation — this one reads the
# account of a job, and the names refused are the three Check 96 refuses beside
# it (belong, edge-devel-dimi, edgeappinc). It is a substring test on purpose: a
# project that merely contains one of the names is refused too, and the message
# says which name matched. It runs before the first call, so a mistyped project
# is a stop and never a reading.
guard_belong() {
  local name
  for name in "$P" "$ACCOUNT"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong* | *edge-devel-dimi* | *edgeappinc*)
      fact belong FAIL "'$name' names an installation this check never reads (never belong, edge-devel-dimi or edgeappinc): name the sandbox installation's project and its account"
      stop "the name '$name' matches an installation this check must never read"
      ;;
    esac
  done
  fact belong PASS "neither the project ($P) nor the account ($ACCOUNT) names an installation this check never reads (nothing typed contains 'belong', 'edge-devel-dimi' or 'edgeappinc')"
}

# --------------------------------------------------------------- the readings
# run_capture OUTFILE CMD... — runs a read, keeps stdout in OUTFILE, and leaves
# its exit status in $?. stderr is shown to the owner and never parsed: it is
# where gcloud puts its progress and its impersonation warning.
run_capture() {
  local out=$1
  shift
  "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    return "$status"
  }
}

# gcloud_said TEXT — gcloud's own words, with its impersonation WARNING dropped:
# that warning is the first line of every call made as another account, and
# quoting it alone is how a refused call gets recorded with no reason in the row.
gcloud_said() {
  printf '%s' "$1" | grep -viE '^(WARNING|NOTICE):' | tr '\n' ' ' | sed 's/  */ /g; s/^ //; s/ $//'
}

# reader SUBCOMMAND [ARGS] — the python3 reader that parses the four documents
# this check reads. It prints a fixed vocabulary and never a document, and it
# prints secret NAMES and LABELS only: no value of any secret is asked for.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# field_of NAME TEXT — the value of a NAME= line the reader printed.
field_of() {
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# account_label_of NAME — the value of the job's `fugaro_account` label, written
# by deploy/terraform/gcp/modules/project/job.tf: a label cannot be empty, so the
# profile's no-account job wears `none`.
account_label_of() {
  case "$1" in "" | default) printf 'none' ;; *) printf '%s' "$1" ;; esac
}

# account_secret_of NAME — the account's derived secret name,
# internal/config's ClaudeAccountSecret: `claude-oauth-token` for the default
# account and `claude-oauth-token-<name>` for every other.
account_secret_of() {
  case "$1" in "" | default) printf 'claude-oauth-token' ;; *) printf 'claude-oauth-token-%s' "$1" ;; esac
}

# step_doctor reads the installation's own facts. The project is the one thing
# this check must hold to: the job name and the secret ID are hash-derived, so a
# reading taken in the wrong project would be a reading of another installation's
# account and nothing in it would say so.
step_doctor() {
  local status=0 out
  fugaro doctor --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || status=$?
  if [ ! -s "$SCRATCH/doctor.json" ]; then
    stop "fugaro doctor --json printed nothing (exit $status): $(sed -n '1,3p' "$SCRATCH/doctor.err" | tr '\n' ' ')"
  fi
  out=$(reader doctor <"$SCRATCH/doctor.json" 2>"$SCRATCH/reader.err") \
    || stop "fugaro doctor --json is not the document this check reads: $(sed -n '1,3p' "$SCRATCH/reader.err" | tr '\n' ' ')"
  DOC_PROJECT=$(field_of project "$out")
  DOC_REGION=$(field_of region "$out")
  local ok bad docerr
  ok=$(field_of ok "$out")
  bad=$(field_of bad "$out")
  docerr=$(field_of error "$out")
  if [ "$ok" = yes ]; then
    fact doctor PASS "fugaro doctor --json is green (ok: true) for the installation $DOC_PROJECT${DOC_REGION:+ in $DOC_REGION}: every check it ran passed"
  else
    CRITERIA_FAILED=yes
    fact doctor FAIL "fugaro doctor --json is NOT green${docerr:+ ($docerr)}: the checks that did not pass are ${bad:-<none named, and doctor answered ok: false>} — run fugaro doctor and fix what it reports before reading this as an account's own finding"
  fi
  [ -n "$DOC_PROJECT" ] || stop "fugaro doctor --json named no project, so this check cannot hold --project $P to the installation's own record: run fugaro setup (or fugaro update) in the installation first"
  if [ "$DOC_PROJECT" != "$P" ]; then
    stop "--project $P is not the project this installation is on: fugaro doctor --json names $DOC_PROJECT. The job's name and the secret's ID are hash-derived, so reading $P would read another installation's account and no row would say so. Re-run with --project $DOC_PROJECT"
  fi
  fact project PASS "$P (typed by the owner, and the project fugaro doctor --json names as this installation's)"
  if [ -z "$R" ]; then
    [ -n "$DOC_REGION" ] || stop "the installation's region could not be read from fugaro doctor --json, so the Cloud Run job cannot be looked up: pass --region <region>"
    R=$DOC_REGION
    fact region PASS "$R (the region fugaro doctor --json records; the job and the account live in it)"
  elif [ "$DOC_REGION" = "$R" ]; then
    fact region PASS "$R (typed by the owner, and the region fugaro doctor --json records)"
  else
    stop "--region $R is not the installation's region: fugaro doctor --json records $DOC_REGION. The job is deployed in one region and a list of another answers no job at all"
  fi
  fact profile PASS "$PROFILE (the job profile; a workflow that names none runs on config.DefaultJobProfile, standard)"
  fact account PASS "$ACCOUNT (the named Claude account; its job wears fugaro_account=$ACCOUNT_LABEL and its derived secret is $ACCOUNT_SECRET, config.ClaudeAccountSecret)"
}

# step_job finds the account's job by its labels and reads it. The name is
# hash-derived (gcp.ProjectJobName), so the labels are the only way to find it —
# and that is also why the labels are what makes it the account's job: the
# account axis is what the deployer writes them from (config.ProjectJobAccounts).
#
# The labels alone are two jobs on any installation that deploys the triple's
# review-only variant: the variant is a module instance of the same project
# module (roots/installation's module.project_review), so it wears the same
# fugaro, fugaro_profile, fugaro_provider and fugaro_account labels as the coding
# job it is the twin of. What tells them apart is the one thing the deployer
# gives one and withholds from the other: a secret. The coding job mounts the
# account's token and the profile's own secrets; the variant's module is handed
# an empty secret set, so a variant offered no container can be granted one
# (internal/infra's reviewVariant, D892(a), the owner's ruling O1). The job this
# check reads is therefore the one that mounts a secret, and the row says which
# mountless jobs beside it were left alone.
step_job() {
  local answer found carriers job variants
  if ! run_capture "$SCRATCH/jobs.json" gcloud run jobs list --project="$P" --region="$R" \
    --filter="metadata.labels.fugaro=managed AND metadata.labels.fugaro_profile=$PROFILE AND metadata.labels.fugaro_account=$ACCOUNT_LABEL" \
    --format=json; then
    stop "gcloud run jobs list did not answer for $P/$R: this check needs an identity that may read Cloud Run there"
  fi
  answer=$(reader joblist <"$SCRATCH/jobs.json" 2>"$SCRATCH/reader.err") \
    || stop "the project's job list is not the document this check reads (gcloud run jobs list --format=json): $(sed -n '1,3p' "$SCRATCH/reader.err" | tr '\n' ' ')"
  found=$(field_of count "$answer")
  carriers=$(field_of carriers "$answer")
  case "$found" in
  "" | *[!0-9]*) stop "the job list did not say how many jobs the account $ACCOUNT has, so this check cannot tell whether one is there" ;;
  0)
    # A reading, not a stop: the account's job not being there is exactly the
    # finding LC4 is about, and the rows under it are left "not reached" rather
    # than answered off a job that does not exist.
    CRITERIA_FAILED=yes
    REACHED_END=yes
    fact job FAIL "no job in $P/$R wears fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL, so this account has no job of its own: either fugaro accounts add $ACCOUNT has not been run on this installation, or the fugaro update that deploys the account's job has not been applied. Every row below is unread"
    owner "the account $ACCOUNT has no job in $P/$R: the rows below were not measured"
    return 0
    ;;
  esac
  case "$carriers" in
  "" | *[!0-9]*) stop "the job list did not say how many of the account $ACCOUNT's $found jobs mount a secret, so this check cannot tell which of them is the account's own" ;;
  esac
  job=""
  variants=""
  case "$carriers" in
  1)
    if [ "$((found - carriers))" -gt 1 ]; then
      stop "the account $ACCOUNT has $found jobs in $P/$R wearing fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL ($(listed_job_names "$answer" | tr '\n' ' ')), $((found - carriers)) of which mount no secret at all and one of which does: the triple deploys one coding job and one review-only variant, so a second mountless job is a leftover of an earlier release. Delete the one that is not in the installation's plan and run again"
    fi
    job=$(printf '%s\n' "$answer" | sed -n 's/^job=\([^ ]*\) secrets=[1-9][0-9]*$/\1/p')
    variants=$(printf '%s\n' "$answer" | sed -n 's/^job=\([^ ]*\) secrets=0$/\1/p' | tr '\n' ' ')
    ;;
  0)
    # Nothing wearing the labels mounts a secret. One such job is still the
    # account's -- a coding job that lost every mount is the finding the rows
    # below report -- but two of them cannot be told apart, and neither is a
    # variant: a variant is the mountless one.
    if [ "$found" != 1 ]; then
      stop "the account $ACCOUNT has $found jobs in $P/$R wearing fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL ($(listed_job_names "$answer" | tr '\n' ' ')) and not one of them mounts a secret: the account's own coding job mounts its token and the profile's own secrets, and its review-only variant mounts nothing at all, so this check cannot tell which of these is which. Converge the installation (fugaro update) and run again"
    fi
    job=$(listed_job_names "$answer" | head -n 1)
    ;;
  *)
    stop "the account $ACCOUNT has $carriers jobs in $P/$R wearing fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL that each mount a secret ($(listed_job_names "$answer" | tr '\n' ' ')): the review-only variant of the triple mounts none and is not among them, so the deployment holds more than one job of the same (profile, provider, account), which it builds exactly one of (infra.ProjectJobs) — a leftover of an earlier release. This check will not read one of them and call it the account's: delete the one that is not in the installation's plan and run again"
    ;;
  esac
  JOB=$job
  [ -n "$JOB" ] || stop "the account's job could not be named from the project's job list"
  if ! run_capture "$SCRATCH/job.json" gcloud run jobs describe "$JOB" --project="$P" --region="$R" --format=json; then
    stop "gcloud run jobs describe did not answer for $JOB in $P/$R"
  fi
  answer=$(reader job <"$SCRATCH/job.json" 2>"$SCRATCH/reader.err") \
    || stop "the job's own document is not the shape this check reads: $(sed -n '1,3p' "$SCRATCH/reader.err" | tr '\n' ' ')"
  JOB_MOUNTS="$answer"
  JOBSA=$(field_of sa "$answer")
  [ -n "$JOBSA" ] || stop "the job $JOB names no service account, so there is no job account to hold to the account's secret"
  if [ -n "$variants" ]; then
    fact job PASS "$JOB, the one job in $P/$R wearing fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL that mounts a secret — read beside ${variants% }, the review-only variant the same triple deploys, which mounts no secret of the installation's at all — runs as $JOBSA"
  else
    fact job PASS "$JOB, the only job in $P/$R wearing fugaro_profile=$PROFILE and fugaro_account=$ACCOUNT_LABEL, runs as $JOBSA"
  fi
}

# listed_job_names ANSWER — the job names a joblist answer carried, one per
# line: a stop sentence names the listing this check will not read, and where
# exactly one job is left the caller reads its name from here.
listed_job_names() { printf '%s\n' "$1" | sed -n 's/^job=\([^ ]*\) secrets=[0-9][0-9]*$/\1/p'; }

# mount_lines ENVVAR — the secret IDs the job mounts as that environment
# variable, one per line (a name twice is a job the deployer would not build, and
# the count below is what says so).
mount_lines() { printf '%s\n' "$JOB_MOUNTS" | sed -n "s/^mount=$1 //p"; }

# mounted_secrets — every secret ID the job mounts, deduplicated, one per line.
mounted_secrets() { printf '%s\n' "$JOB_MOUNTS" | sed -n 's/^mount=[^ ]* //p' | sort -u; }

# secret_label SECRET_ID — the secret's own fugaro_secret label. Read into a
# variable, never piped into a test: a describe that failed must not be
# indistinguishable from a label that is not there.
SECRET_LABEL=""
SECRET_SAID=""
secret_label() { # secret_label SECRET_ID — sets SECRET_LABEL, "" when unread
  local out
  SECRET_LABEL=""
  if ! run_capture "$SCRATCH/secret.json" gcloud secrets describe "$1" --project="$P" --format=json; then
    SECRET_SAID=$(sed -n '1,5p' "$SCRATCH/cmd.err" | tr '\n' ' ')
    return 1
  fi
  out=$(reader secret <"$SCRATCH/secret.json" 2>/dev/null) || {
    SECRET_SAID="the describe is not the document this check reads"
    return 1
  }
  SECRET_LABEL=$(field_of label "$out")
  return 0
}

# step_token reads the account's own mount: exactly one entry named
# CLAUDE_CODE_OAUTH_TOKEN (config.ReservedSecrets' variable for
# config.ClaudeOAuthToken), pointing at a secret whose fugaro_secret label is the
# account's derived name.
step_token() {
  local ids count
  ids=$(mount_lines CLAUDE_CODE_OAUTH_TOKEN)
  count=$(printf '%s\n' "$ids" | grep -c . || true)
  if [ "$count" -eq 0 ]; then
    CRITERIA_FAILED=yes
    fact token-mount FAIL "the job $JOB mounts no secret as CLAUDE_CODE_OAUTH_TOKEN (config.ReservedSecrets' variable for the claude-oauth-token mount), so this account's token is not on it: the job cannot run as the account, and every row below is unread"
    owner "no CLAUDE_CODE_OAUTH_TOKEN mount on $JOB"
    return 0
  fi
  if [ "$count" -ne 1 ]; then
    CRITERIA_FAILED=yes
    fact token-mount FAIL "the job $JOB mounts $count secrets as CLAUDE_CODE_OAUTH_TOKEN ($(printf '%s' "$ids" | tr '\n' ' ')): exactly one account-token entry is what LC4 asks for, and a job carrying two would have one of them silently win"
    return 0
  fi
  TOKEN_SECRET=$ids
  TOKEN_READ=yes
  fact token-mount PASS "the job $JOB mounts exactly one secret as CLAUDE_CODE_OAUTH_TOKEN: $TOKEN_SECRET"
  if ! secret_label "$TOKEN_SECRET"; then
    stop "the secret $TOKEN_SECRET could not be described in $P ($(gcloud_said "$SECRET_SAID")): this check needs an identity that may read Secret Manager there"
  fi
  if [ "$SECRET_LABEL" = "$ACCOUNT_SECRET" ]; then
    fact token-secret PASS "that secret is labelled fugaro_secret=$ACCOUNT_SECRET, which is the account $ACCOUNT's own derived secret (config.ClaudeAccountSecret) — the token mounted on this job is this account's"
  else
    CRITERIA_FAILED=yes
    fact token-secret FAIL "the secret mounted as CLAUDE_CODE_OAUTH_TOKEN ($TOKEN_SECRET) is labelled fugaro_secret=${SECRET_LABEL:-<none>}, and the account $ACCOUNT's derived secret is $ACCOUNT_SECRET (config.ClaudeAccountSecret). A job mounting another account's token, or a secret the installation did not label, is the wrong credential under the right variable name"
  fi
}

# step_profile_mounts holds the job to the other half of LC4's sentence: the
# profile's own mounts are on it too. The GitHub App key is the one the section
# names (config.GitHubAppKeySecret, mounted as FUGARO_GITHUB_APP_PRIVATE_KEY by
# every profile whose provider is GitHub — the only provider a shared project job
# is deployed for at all, infra.ProjectJobs).
step_profile_mounts() {
  local ids id label said="" missing=no
  ids=$(mount_lines FUGARO_GITHUB_APP_PRIVATE_KEY)
  if [ -z "$ids" ]; then
    CRITERIA_FAILED=yes
    fact profile-mounts FAIL "the job $JOB mounts no secret as FUGARO_GITHUB_APP_PRIVATE_KEY: the account's job carries its own copy of the profile's mounts (infra.ProjectJobs copies secretEnv per account), and LC4 asks for the account's token BESIDE them. Its secret-backed entries are: $(printf '%s\n' "$JOB_MOUNTS" | sed -n 's/^mount=//p' | tr '\n' ' ')"
    return 0
  fi
  if ! secret_label "$ids"; then
    stop "the secret $ids mounted as FUGARO_GITHUB_APP_PRIVATE_KEY could not be described in $P ($(gcloud_said "$SECRET_SAID"))"
  fi
  if [ "$SECRET_LABEL" = "github-app-key" ]; then
    fact profile-mounts PASS "the profile's own mount is on the same job: FUGARO_GITHUB_APP_PRIVATE_KEY comes from the secret $ids, labelled fugaro_secret=github-app-key (config.GitHubAppKeySecret)"
  else
    CRITERIA_FAILED=yes
    fact profile-mounts FAIL "the job $JOB mounts FUGARO_GITHUB_APP_PRIVATE_KEY from the secret $ids, whose fugaro_secret label is ${SECRET_LABEL:-<none>} and not github-app-key: the variable the profile's own mount is under is carrying another secret"
  fi
  # Every other secret-backed entry, by its label: no other account's token may
  # ride on this job (the account axis exists so that one account's job can read
  # one account's token, design §6).
  for id in $(mounted_secrets); do
    [ "$id" != "$TOKEN_SECRET" ] || continue
    if ! secret_label "$id"; then
      stop "the secret $id, mounted on $JOB, could not be described in $P ($(gcloud_said "$SECRET_SAID"))"
    fi
    label=$SECRET_LABEL
    said="$said $id (fugaro_secret=${label:-<none>})"
    case "$label" in
    claude-oauth-token | claude-oauth-token-*)
      if [ "$label" != "$ACCOUNT_SECRET" ]; then
        CRITERIA_FAILED=yes
        fact other-mounts FAIL "the job $JOB mounts the secret $id, labelled fugaro_secret=$label — another account's token secret — beside its own. One account's job must mount one account's token (design §6), and this job would run with two of them"
        return 0
      fi
      ;;
    esac
  done
  fact other-mounts PASS "no other account's token is mounted on it: its other secret-backed entries are${said:- none}, and none of them is labelled claude-oauth-token*"
}

# step_sa reads the job account's readers. The account's own secret must name it
# under roles/secretmanager.secretAccessor (sa.tf grants one member per secret the
# job's secret_env names), and no other account's secret in the project may name
# it at all.
step_sa() {
  local out accessor ids id others="" count=0
  if ! run_capture "$SCRATCH/policy.json" gcloud secrets get-iam-policy "$TOKEN_SECRET" --project="$P" --format=json; then
    stop "gcloud secrets get-iam-policy did not answer for $TOKEN_SECRET in $P: this check needs an identity that may read a secret's policy there"
  fi
  out=$(reader iampolicy "serviceAccount:$JOBSA" <"$SCRATCH/policy.json" 2>"$SCRATCH/reader.err") \
    || stop "the secret's policy is not the document this check reads: $(sed -n '1,3p' "$SCRATCH/reader.err" | tr '\n' ' ')"
  accessor=$(field_of accessor "$out")
  if [ "$accessor" = yes ]; then
    fact sa-accessor PASS "$JOBSA holds roles/secretmanager.secretAccessor on $TOKEN_SECRET, the account $ACCOUNT's own token secret — so this job, and only this job, can read the credential it runs as"
  else
    CRITERIA_FAILED=yes
    fact sa-accessor FAIL "$JOBSA does NOT hold roles/secretmanager.secretAccessor on $TOKEN_SECRET (the policy's own bindings were read and none of them names it under that role): the job cannot read the account's token, and every run launched onto it fails at startup rather than here. deploy/terraform/gcp/modules/project/sa.tf grants it — re-run the fugaro update that applies this account's job"
  fi
  # Every other account token secret in the project, by its label. They are found
  # by label and never by name: the ID is hash-derived (gcp.ProjectSecretID).
  if ! run_capture "$SCRATCH/secrets.json" gcloud secrets list --project="$P" \
    --filter="labels.fugaro_secret~^claude-oauth-token" --format=json; then
    stop "gcloud secrets list did not answer for $P: this check needs an identity that may list Secret Manager there"
  fi
  out=$(reader secrets <"$SCRATCH/secrets.json" 2>"$SCRATCH/reader.err") \
    || stop "the project's secret list is not the document this check reads (gcloud secrets list --format=json): $(sed -n '1,3p' "$SCRATCH/reader.err" | tr '\n' ' ')"
  ids=$(printf '%s\n' "$out" | sed -n 's/^secret=//p')
  for id in $ids; do
    [ "$id" != "$TOKEN_SECRET" ] || continue
    count=$((count + 1))
    if ! run_capture "$SCRATCH/policy.json" gcloud secrets get-iam-policy "$id" --project="$P" --format=json; then
      stop "gcloud secrets get-iam-policy did not answer for $id in $P, and it is another account's token secret: this check cannot say whether $JOBSA may read it"
    fi
    out=$(reader iampolicy "serviceAccount:$JOBSA" <"$SCRATCH/policy.json" 2>/dev/null) \
      || stop "the policy of $id is not the document this check reads"
    if [ "$(field_of accessor "$out")" = yes ]; then
      others="$others $id"
    fi
  done
  if [ -n "$others" ]; then
    CRITERIA_FAILED=yes
    fact sa-other-account FAIL "$JOBSA holds roles/secretmanager.secretAccessor on another account's token secret:$others. One account's job must be able to read one account's token (design §6, and the point of the account axis in the job's labels); a job that can read two has the isolation the whole option is for"
    return 0
  fi
  fact sa-other-account PASS "$JOBSA holds roles/secretmanager.secretAccessor on no other account's token secret: the project has $count other secret(s) labelled claude-oauth-token*, each one's policy was read, and none of them names it"
}

step_verdict() {
  REACHED_END=yes
}

# ------------------------------------------------------------------ the reader
# One python3 file parses every document this check reads, and it prints a fixed
# vocabulary rather than a document: `project=`, `region=`, `ok=yes`,
# `bad=<id (severity), ...>`, `count=2`, `job=<name>`, `sa=<email>`,
# `mount=<ENVVAR> <secret id>`, `plain=<ENVVAR>`, `secret=<id>`, `label=<value>`,
# `accessor=yes`. So what reaches the block is this check's own words about what
# it read. No secret value is anywhere in it: gcloud is never asked for one, and
# the only fields read off a secret are its own name and its labels.
write_files() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check LC4's reader: every document this check reads is parsed here.

It prints a fixed vocabulary and never a document, and it prints secret NAMES
and LABELS only -- no subcommand here asks gcloud for a secret's value, and the
documents handed to it carry none.
"""
import json
import sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def load():
    try:
        return json.loads(sys.stdin.read())
    except ValueError as e:
        fail("not JSON: %s" % e)


def last_segment(name):
    """A resource name's own ID: gcloud answers a job and a secret both ways --
    `projects/<p>/secrets/<id>` and the bare ID -- so the last segment is the
    only form two readings of the same secret compare equal in."""
    return (name or "").rstrip("/").rsplit("/", 1)[-1]


def labels_of(doc):
    """A secret's labels, from either shape gcloud prints: `describe` puts them
    at the top level beside `name`, and so does an entry of `secrets list`."""
    labels = doc.get("labels")
    if labels is None:
        labels = (doc.get("metadata") or {}).get("labels") or {}
    return labels


def cmd_doctor():
    """`fugaro doctor --json`: the installation's own project and region, its
    verdict (`ok`, internal/cli's doctorOutput.OK: false when a check of an
    error severity failed) and the ids of the checks that did not pass."""
    doc = load()
    project = doc.get("project") or {}
    print("name=%s" % (project.get("name") or ""))
    print("project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))
    print("ok=%s" % ("yes" if doc.get("ok") else "no"))
    print("error=%s" % (doc.get("error") or ""))
    bad = []
    for c in doc.get("checks") or []:
        if c.get("ok"):
            continue
        bad.append("%s (%s)" % (c.get("id") or "?", c.get("severity") or "error"))
    print("bad=%s" % ", ".join(bad))


def job_spec(doc):
    """The container spec of a job document, from `list` or `describe` alike:
    gcloud forces the v1 API, and a v1 Job nests its execution template one
    level below a service's, so the container and the job's account sit at
    spec.template.spec.template.spec (internal/backend/gcp's LabelProfile over
    a job a launcher may override nothing of)."""
    spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}
    return (spec.get("template") or {}).get("spec") or {}


def secret_mounts(doc):
    """How many secret-backed environment entries a job document mounts. It is
    what tells the account's coding job from the review-only variant deployed
    for the same (profile, provider, account): the variant wears the same four
    labels and mounts nothing at all -- the deployer hands its module an empty
    secret set, so a variant offered no container cannot be granted one
    (internal/infra's reviewVariant, D892(a))."""
    mounted = 0
    for container in job_spec(doc).get("containers") or []:
        for entry in container.get("env") or []:
            ref = (entry.get("valueSource") or {}).get("secretKeyRef") or {}
            if ref.get("secret"):
                mounted += 1
    return mounted


def cmd_joblist():
    """`gcloud run jobs list --format=json`: the managed jobs it answered, by
    name and secret-mount count. The label is checked here as well as in the
    filter, so a fake or a future gcloud that ignored the filter cannot make a
    job of somebody else's look like this installation's. The count is what the
    caller picks the account's own job by, and `carriers` is how many of the
    jobs it answered mount a secret at all."""
    doc = load()
    if not isinstance(doc, list):
        fail("not a list of jobs")
    jobs = []
    for j in doc:
        md = j.get("metadata") or {}
        if (md.get("labels") or {}).get("fugaro") != "managed":
            continue
        name = md.get("name") or ""
        if name:
            jobs.append((name, secret_mounts(j)))
    jobs.sort()
    print("count=%d" % len(jobs))
    print("carriers=%d" % len([1 for _, mounted in jobs if mounted]))
    for name, mounted in jobs:
        print("job=%s secrets=%d" % (name, mounted))


def cmd_job():
    """`gcloud run jobs describe --format=json`: the job's own service account
    and its container environment, one line per entry. A secret-backed entry is
    `mount=<ENVVAR> <secret id>` and a plain one is `plain=<ENVVAR>`: the split
    is what tells the account's token mount from a variable like FUGARO_PROJECT,
    and a mount prints the secret's ID and never its value."""
    doc = load()
    spec = job_spec(doc)
    print("sa=%s" % (spec.get("serviceAccountName") or ""))
    for container in spec.get("containers") or []:
        for entry in container.get("env") or []:
            name = entry.get("name") or ""
            ref = (entry.get("valueSource") or {}).get("secretKeyRef") or {}
            if ref.get("secret"):
                print("mount=%s %s" % (name, last_segment(ref.get("secret"))))
            else:
                print("plain=%s" % name)


def cmd_secret():
    """`gcloud secrets describe --format=json`: the secret's own ID and the two
    labels this check reads. The labels are what name a secret's owner here: the
    ID is hash-derived (gcp.ProjectSecretID) and means nothing to a person."""
    doc = load()
    labels = labels_of(doc)
    print("id=%s" % last_segment(doc.get("name") or ""))
    print("label=%s" % (labels.get("fugaro_secret") or ""))
    print("project=%s" % (labels.get("fugaro_project") or ""))


def cmd_secrets():
    """`gcloud secrets list --format=json`: every secret labelled with a
    claude-oauth-token* fugaro_secret, which is the account-token namespace
    internal/config reserves (ClaudeOAuthTokenPrefix) -- so a secret of another
    account's is found by its label and never by a name this check computes."""
    doc = load()
    if not isinstance(doc, list):
        fail("not a list of secrets")
    ids = []
    for s in doc:
        if (labels_of(s).get("fugaro_secret") or "").startswith("claude-oauth-token"):
            ids.append(last_segment(s.get("name") or ""))
    ids = [i for i in ids if i]
    print("count=%d" % len(ids))
    for i in sorted(ids):
        print("secret=%s" % i)


def cmd_iampolicy(member):
    """`gcloud secrets get-iam-policy --format=json`: whether MEMBER holds
    roles/secretmanager.secretAccessor, the one role that carries
    secretmanager.versions.access, which is the permission a job needs to read
    the token it mounts (deploy/terraform/gcp/modules/project/sa.tf)."""
    doc = load()
    hit = False
    for binding in doc.get("bindings") or []:
        if binding.get("role") != "roles/secretmanager.secretAccessor":
            continue
        for m in binding.get("members") or []:
            if m == member:
                hit = True
    print("accessor=%s" % ("yes" if hit else "no"))


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
    elif cmd == "secret":
        cmd_secret()
    elif cmd == "secrets":
        cmd_secrets()
    elif cmd == "iampolicy":
        if len(argv) < 2:
            fail("iampolicy needs a member")
        cmd_iampolicy(argv[1])
    else:
        fail("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

# print_plan: every command a real run would make, in order. Printed by --dry-run
# INSTEAD of running anything: this check's own test pins that a dry run makes no
# gcloud call and no fugaro call at all, so the plan names the shape of each
# command and never evaluates one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [local, reads] fugaro doctor --json — the installation's own project and region, and its verdict. The project it names must be the one you typed (--project $P), and the region it records is the default for --region (you typed ${R:-<none>}), because the job's name and the secret's ID are hash-derived and a reading taken in another project would be another installation's account"
  say "  [network, reads] gcloud run jobs list --project=$P --region=${R:-<the region doctor records>} --filter=metadata.labels.fugaro=managed AND metadata.labels.fugaro_profile=$PROFILE AND metadata.labels.fugaro_account=$ACCOUNT_LABEL --format=json — the account's job, found by the labels the deployer writes from the account axis (infra.ProjectJobs, gcp.ProjectJobName's name is hash-derived and never typed)"
  say "  [network, reads] gcloud run jobs describe <that job> --project=$P --region=<the region> --format=json — its service account and its secret-backed environment entries, names only: no secret value is asked for anywhere in this check"
  say "  [network, reads] gcloud secrets describe <each secret the job mounts> --project=$P --format=json — the fugaro_secret label on it, which is what says whose secret it is (the ID is hash-derived, gcp.ProjectSecretID)"
  say "  [network, reads] gcloud secrets list --project=$P --filter=labels.fugaro_secret~^claude-oauth-token --format=json — every other account's token secret in the project"
  say "  [network, reads] gcloud secrets get-iam-policy <the account's secret> --project=$P --format=json, and the same for each of those others — whether the job's own service account holds roles/secretmanager.secretAccessor on it"
  say "and its Restore would run: nothing. This check makes no write, so there is no teardown: every command above is a describe, a list or a get-iam-policy, and the scratch directory is the only thing it leaves to remove."
  say "Nothing was read, listed or described: this dry run made no gcloud call and no fugaro call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(xtrace belong project region profile account doctor job
  token-mount token-secret profile-mounts other-mounts sa-accessor sa-other-account restore)
NOT_REACHED_TEXT="FAIL — not reached (the run did not read this row)"

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK LC4 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: %s\n' "$key" "$NOT_REACHED_TEXT" >&3
    fi
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK LC4 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ "$HELP_SHOWN" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: a
  # block that then read as a finished run would offer a FAILed mount row as an
  # answer about the account when the steps that would have filled it never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the account's job"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, and this check has nothing to restore: it made no write at all. Every call it makes is a describe, a list or a get-iam-policy — read the transcript above rather than assume a teardown happened"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed row, or beside a Restore that did not finish, would read as a pass.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -qE '^(doctor|job|token-mount|token-secret|profile-mounts|other-mounts|sa-accessor|sa-other-account|restore)\|FAIL\|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was read and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check-lc4.sh --project <project> --account <name> [--profile <profile>] [--region <region>] [--dry-run]"

parse_args() {
  P=""
  ACCOUNT=""
  PROFILE="standard"
  R=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --account)
      [ $# -ge 2 ] || die "--account needs the name you gave 'fugaro accounts add'. usage: $USAGE"
      ACCOUNT=$2
      shift 2
      ;;
    --profile)
      [ $# -ge 2 ] || die "--profile needs a job profile name. usage: $USAGE"
      PROFILE=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a region such as us-central1. usage: $USAGE"
      R=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELP_SHOWN=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$P" ] || die "--project is required, and it has to be the GCP project of the installation whose account you are reading (fugaro doctor --json's own project, not Fugaro's project name). usage: $USAGE"
  [ -n "$ACCOUNT" ] || die "--account is required: it is the name you gave 'fugaro accounts add', and this check reads that account's job. usage: $USAGE"
  [ -n "$PROFILE" ] || die "--profile cannot be empty. usage: $USAGE"
  ACCOUNT_LABEL=$(account_label_of "$ACCOUNT")
  ACCOUNT_SECRET=$(account_secret_of "$ACCOUNT")
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  write_files
  guard_xtrace
  guard_belong
  if [ -n "$DRY_RUN" ]; then
    fact project PASS "$P (typed by the owner; a real run holds it to the project fugaro doctor --json names, and stops if they differ)"
    if [ -n "$R" ]; then
      fact region PASS "$R (typed by the owner; a real run holds it to the region fugaro doctor --json records)"
    else
      fact region PASS "not typed: a real run takes the region from fugaro doctor --json's own record"
    fi
    fact profile PASS "$PROFILE (the job profile; a workflow that names none runs on config.DefaultJobProfile, standard)"
    fact account PASS "$ACCOUNT (the named Claude account; its job wears fugaro_account=$ACCOUNT_LABEL and its derived secret is $ACCOUNT_SECRET, config.ClaudeAccountSecret)"
    fact dry-run PASS "guards and the plan only: no gcloud call and no fugaro call was made, nothing was read and no verdict is printed"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the guards and the arguments. It reached its end -- the plan IS the end of a
    # dry run -- so the exit trap must not read it as a run that stopped.
    REACHED_END=yes
    BLOCK_KEYS=(xtrace belong project region profile account dry-run)
    return 0
  fi
  step_doctor
  step_job
  if [ "$REACHED_END" != yes ]; then step_token; fi
  # The rows under the token are about the account's own secret, so they are read
  # only when there is one: with no job, or with no single account-token mount on
  # it, there is no secret to hold anything to, and the block says each of them
  # was not read rather than answering off nothing.
  if [ "$TOKEN_READ" = yes ]; then
    step_profile_mounts
    step_sa
  fi
  step_verdict
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
