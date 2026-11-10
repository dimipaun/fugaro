#!/usr/bin/env bash
# Check 96 — GitHub tokens through the credentials service, as ONE script YOU
# run. The register's row and section are docs/gcp-live-checklist.md, "Check 96".
#
# It settles the live half of the credentials-service work's D145 on the owner's
# sandbox installation fugaro (GCP project fugaro-dev): the job holds no App key
# (or holds it because C2.3's drop is held, which is the state this check is run
# against and says so), the run's installation token is minted by the service
# for the calling account's own repository and with the run's permissions, and a
# run identity for repository X cannot mint for Y. The three claims of the
# plan's row 79, read on a real installation.
#
# Usage:      bash scripts/live-checks/check96.sh [--dry-run] [<owner/name>]
#             The repository is the SANDBOX one. It is the one to name, and it
#             may be left out only where the installation's own state is
#             unambiguous: with exactly one repository holding state that one is
#             the sandbox and the fact says the check derived it, and with more
#             than one the check stops (exit 2) and lists them rather than
#             picking — what it mints below is a real GitHub installation token
#             for whichever repository it names.
#             --dry-run runs the guards and every read, prints the plan of the
#             only things that write — the calls to the mint endpoint and the
#             two Firebase Auth user deletions — and runs none of them.
# Needs:      bash, mktemp, python3, gcloud, curl and fugaro on PATH. Nothing is
#             installed and nothing is fetched; a missing tool stops the run
#             with one line naming it.
#             All five things this check used to ask you to export are read
#             instead: the installation, its project and region out of
#             `fugaro doctor --json`; the Firebase project, the restricted web
#             API key and the token signer out of the installation's own config
#             file; the service out of `gcloud run services list` by its
#             `fugaro_role=credentials` label; the sandbox repository and the
#             second repository the pairing case needs out of
#             `fugaro budget show --all --json` (the second one always, the
#             sandbox one only where that state names a single repository, which
#             the fact says it did); and the repository's job out of
#             `gcloud run jobs list` by its `fugaro_repo` label. The key and the
#             signer are read from the config file and never printed.
# Where:      installation fugaro on GCP project fugaro-dev ONLY, and never
#             `belong`: both the project the config in force describes and the
#             repository's name are refused when they are anything else. It
#             mints two Firebase Auth users (deleted again in the same run, and
#             read back absent), two run identities and ONE real GitHub
#             installation token, for the sandbox repository.
# Costs:      four POSTs to the service — three authenticated (the mint, the
#             pairing case's and the outsider's) and one with no identity at all,
#             which Cloud Run's own front end refuses before the container runs
#             (and which the service's header parse refuses instead, if IAM is
#             off for it) — a handful of read-only GitHub API calls and one probe
#             against a workflow name that does not exist, which GitHub refuses
#             before anything could dispatch. No model call, no run, no job
#             execution.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed,
#             and the two Firebase Auth users the run created are deleted in the
#             last step and read back absent. A run interrupted before that
#             step says in its `restore` line which uids are still there and the
#             one call that deletes them, because they are this check's own.
# Output:     progress on stderr, ONE block on stdout headed CHECK 96 RESULT, to
#             copy back to the coordinator. Exit 0 when every fact passed, 1
#             when one FAILed, 2 when the run could not tell (a guard stopped it,
#             a tool is missing, the service is not deployed).
#
# Nothing this check prints is a credential. The two tokens of each mint call —
# the caller's Google ID token and the run identity — are shell variables, and
# they reach curl through `--config -` read from stdin, so no token is in any
# argv (`ps` shows a process's argv to every user of the machine for as long as
# the process lives) and no token is in any file: the signed custom token is the
# one credential that touches the scratch directory, 0600 and removed the moment
# it is read. The minted installation token is never printed, never used for a
# write and never written down. And the block greps its own text for `ghs_` —
# the fixed prefix of every GitHub installation token — dropping any line that
# carries one and reporting a FAIL rather than printing it.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: `bash -x` prints every
# variable a line assigns, which is the identity tokens and the minted token,
# into a log the owner would then paste back. So the trace is turned off here,
# BASH_XTRACEFD is unset so a trace redirected to a file this script knows
# nothing about cannot be reopened, and guard_xtrace refuses a shell that
# arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then and every child
# would start tracing on its own).
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
DRY_RUN=${CHECK96_DRY_RUN:-}
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# HELP_SHOWN=yes is the one ending that prints no RESULT block at all: `--help`
# is not a run of the check, so a block with every row "not reached" would be a
# verdict about a mint that was never attempted.
HELP_SHOWN=no
# SECRET_SCAN=yes is set by print_block's own scan, which runs after main has
# already counted the facts: the block is the last thing to know a line carried
# a token's prefix, so it is also the last thing that can say the run failed.
SECRET_SCAN=no
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
# all, and the block would then read as "the mint is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the mint" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 96 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 96 needs bash"
  need mktemp
  need python3 "the reader that parses every document this check reads, and signs no token itself, is one python3 file"
  need gcloud
  need curl "the mint, the two refusals and the two user deletions are curl calls: the service admits nobody else"
  need fugaro "the installation's facts are read with fugaro doctor --json and fugaro budget show --all --json"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk96-mine"
SCRATCH_REMOVED=no
PRINTED=""
# The uids this run really created — one per run identity it exchanged — so the
# `restore` line can name the ones a stopped run left behind. A uid is added
# only after the exchange answered an ID token: before that there is no user to
# delete.
UIDS_CREATED=()

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk96.XXXXXXXX") || die "Check 96 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 96 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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

# restore_detail is what the `restore` line says: the scratch directory's own
# outcome, whether the two credentials this check minted are gone (the two
# Firebase Auth users), and the one thing it cannot take back (the installation
# token, which GitHub expires on its own and this check keeps no copy of).
restore_detail() {
  local dir
  if [ -z "$SCRATCH" ]; then
    dir="nothing was created: the run stopped before it made a scratch directory"
  elif [ "$SCRATCH_REMOVED" = yes ]; then
    dir="the scratch directory is gone"
  else
    dir="the scratch directory is STILL THERE, at $SCRATCH — remove it yourself"
  fi
  local users left
  if [ "${#UIDS_CREATED[@]}" -eq 0 ]; then
    users="no run identity was exchanged, so no Firebase Auth user of this check's was created"
  elif [ "${SURVIVORS_DONE:-no}" = yes ]; then
    users="the run identities it expired were deleted and read back absent"
  else
    left=${UIDS_CREATED[*]}
    users="the run identity users STILL EXIST (uids: $left) — the run stopped before the last step, and this is the call that deletes each: curl -sS --config - -X POST \"https://identitytoolkit.googleapis.com/v1/projects/${FB:-<firebase-project>}/accounts:delete\" with an Authorization header from 'gcloud auth print-access-token' and the data {\"localId\":\"<uid>\"}"
  fi
  printf '%s; %s; the one thing it cannot take back is the installation token the mint answered: GitHub expires it inside the hour, this check never wrote it anywhere, and a second run mints a second one' "$dir" "$users"
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
  # would offer a row of FAILs as a verdict about the mint when the steps that
  # would have filled them never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the mint"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ "$SCRATCH_REMOVED" != yes ] && [ -n "$SCRATCH" ]; then restore=FAIL; fi
  facts="${facts}"$'\n'"restore|$restore|$(restore_detail)"
  # The verdict is taken from the facts, not from the exit path: a scratch
  # directory this check could not remove is a FAIL, and a block that printed
  # `restore: FAIL` beside `END (exit 0)` would read as a pass (check91.sh reads
  # its own rows the same way). A run that stopped in a guard is exit 2 already.
  # The pattern is anchored on the verdict field, so a `|FAIL|` inside a detail
  # (an echoed message, a repository name) is not a fact that failed.
  if printf '%s\n' "$facts" | grep -qE '^[^|]+\|FAIL\|' && [ "$status" = 0 ]; then
    status=1
  fi
  if [ "$HELP_SHOWN" = yes ]; then
    exit 0
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
    if [ "$SECRET_SCAN" = yes ]; then status=1; fi
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the identity tokens and the minted installation token this check reads into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check96.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a token it read"
}

# guard_repository: the sandbox repository is named by you or read from the
# installation's state, and either way a name that belongs to another
# installation is refused before a single call is made. This runs before the
# scratch directory exists, so it writes no fact of its own; `stop` records the
# aborted row the block needs.
guard_repository() {
  case "${REPO_ARG:-}" in
  "")
    ;;
  *belong* | *edge-devel-dimi* | *edgeappinc*)
    stop "$REPO_ARG names an installation this check never touches (never belong, edge-devel-dimi or edgeappinc): name the sandbox repository of the fugaro-dev installation"
    ;;
  */*)
    ;;
  *)
    stop "'$REPO_ARG' is not a repository name: it has to be <owner>/<name>, the sandbox repository of the installation this config describes"
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
# `region=`, `repo=`, `slug=`, `service=`, `url=`, `job=`, `sa=`, `appid=`,
# `present`, `absent`, `yes`, `no`, `lines=3`, and the like. Its one subcommand
# that hands out a credential is `identity`, which prints the run's ID token on
# stdout for a shell variable and nowhere else.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# field_of NAME TEXT — the value of a NAME= line the reader printed.
field_of() {
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# yaml_scalar FILE KEY — one top-level-or-nested scalar out of the installation's
# config, by its key alone. The config is the document fugaro itself writes, and
# this reads three of its values; the restricted web API key never leaves this
# shell variable and is never printed.
yaml_scalar() {
  sed -n "s/^ *$2: *//p" "$1" | tr -d "\"'" | head -1
}

# config_path INSTALLATION — the config file in force: FUGARO_CONFIG when it is
# set, else the project's own file under the config home (localcfg.ProjectPath's
# convention: $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, and on a machine
# with no XDG_CONFIG_HOME the two places fugaro itself looks).
config_path() {
  if [ -n "${FUGARO_CONFIG:-}" ]; then
    printf '%s' "$FUGARO_CONFIG"
    return 0
  fi
  local name=$1 base
  for base in "${XDG_CONFIG_HOME:-}" "$HOME/.config" "$HOME/Library/Application Support"; do
    [ -n "$base" ] || continue
    if [ -r "$base/fugaro/projects/$name.yaml" ]; then
      printf '%s' "$base/fugaro/projects/$name.yaml"
      return 0
    fi
  done
  return 1
}

# ------------------------------------------------------------------ the steps
# step_installation reads the installation's own facts and holds the project to
# fugaro-dev. Nothing here is typed by hand: the project, the region, the
# Firebase project, the API key and the token signer, and the sandbox repository
# with the second repository the pairing case needs.
step_installation() {
  if ! run_capture "$SCRATCH/doctor.json" fugaro doctor --json; then
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local answer name
  answer=$(reader doctor <"$SCRATCH/doctor.json") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  NAME=$(field_of name "$answer")
  PROJECT=$(field_of project "$answer")
  REGION=$(field_of region "$answer")
  [ -n "$REGION" ] || stop "fugaro doctor --json named no region, so this check cannot address the job: the local config has to carry one"
  [ "$PROJECT" = "$P" ] || stop "the config in force is the $PROJECT installation, and Check 96 runs on $P only (never belong): set FUGARO_CONFIG to the fugaro-dev installation's own config, or run it from a machine whose config names that project"
  case "$NAME" in
  belong | belong-*)
    stop "the config in force describes the installation '$NAME', which is the owner's own one: Check 96 mints GitHub installation tokens and creates Firebase users, and it is a sandbox check"
    ;;
  esac

  # The repositories, before the config: which repository is the sandbox one and
  # whether the config records its root as applied with the service on are two
  # halves of the same read, and the second half needs the first one's answer.
  if ! run_capture "$SCRATCH/budget.json" fugaro budget show --all --json; then
    stop "'fugaro budget show --all --json' did not answer, so this check cannot tell which repositories this installation has state for. Run it yourself from a checkout of a repository this installation manages"
  fi
  # The reader is handed the repository only when the owner named one, and it
  # refuses a derivation among several of them: what it mints below is a REAL
  # GitHub installation token for whichever repository this names, so a pick the
  # owner did not make is a token spent on a repository that may not be the
  # sandbox. Its refusal lists the candidates, which is the one word to add.
  answer=$(reader budget "$REPO_ARG" <"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err") \
    || stop "the sandbox repository could not be named: $(sed -n '1,3p' "$SCRATCH/budget.err" | tr '\n' ' ')"
  FPROJ=$(field_of project "$answer")
  REPO=$(field_of repo "$answer")
  SLUG=$(field_of slug "$answer")
  OTHER_REPO=$(field_of other_repo "$answer")
  OTHER_SLUG=$(field_of other_slug "$answer")
  REPO_COUNT=$(field_of repos "$answer")
  REPO_CHOSEN=$(field_of chosen "$answer")
  [ -n "$REPO" ] && [ -n "$SLUG" ] || stop "the sandbox repository could not be named from this installation's own state"
  [ -n "$OTHER_SLUG" ] || stop "the installation has one repository with state, and the pairing case needs a second one: Check 96's third claim is that a run identity for repository X cannot mint for Y, and there is no Y here"
  case "$REPO" in
  *belong* | *edge-devel-dimi* | *edgeappinc*)
    stop "$REPO names an installation this check never touches (never belong, edge-devel-dimi or edgeappinc)"
    ;;
  esac
  case "$FPROJ" in
  "") stop "fugaro budget show --all --json named no Fugaro project, so the run identity's fp claim cannot be minted for it" ;;
  esac

  # The Firebase project, the signer and the restricted web API key, from the
  # installation's own file. The signer's domain has to be the Firebase project
  # (it is token_signer = fugaro-token-signer@<fp>.iam.gserviceaccount.com, and
  # the custom token it signs is exchanged at that project): a mismatch is a
  # config that would exchange at another project's Identity Toolkit.
  config_path "$NAME" >"$SCRATCH/cfg.path" || stop "no project config for '$NAME' was found. Looked at \$FUGARO_CONFIG, \$XDG_CONFIG_HOME/fugaro/projects/$NAME.yaml and \$HOME/.config/fugaro/projects/$NAME.yaml: the installation's config is where its Firebase project, its signer and its restricted web API key live"
  CFG=$(cat "$SCRATCH/cfg.path")
  FB=$(yaml_scalar "$CFG" firebase_project)
  SIGNER=$(yaml_scalar "$CFG" token_signer)
  FIREKEY=$(yaml_scalar "$CFG" firebase_api_key)
  [ -n "$FB" ] || stop "$CFG carries no budget.firebase_project, so this check cannot address the installation's Firebase project: run 'fugaro update --firebase-project <id>'"
  [ -n "$SIGNER" ] || stop "$CFG carries no budget.token_signer, so this check cannot mint a run identity: run 'fugaro update --firebase-project <id>'"
  [ -n "$FIREKEY" ] || stop "$CFG carries no budget.firebase_api_key, so this check cannot exchange the run identity: run 'fugaro update --firebase-project <id>'"
  case "$SIGNER" in
  *@"$FB".iam.gserviceaccount.com) ;;
  *) stop "$CFG's token signer ($SIGNER) is not an account of its own Firebase project ($FB): the custom token would be exchanged at another project's Identity Toolkit, so nothing below would mean anything" ;;
  esac
  # The installation's own credentials-service block: the URL the launch reads to
  # decide whether a repository's job is handed the address. It is read, never
  # typed, and compared with the service's own URL in step_service.
  local cfgans
  cfgans=$(reader config "$CFG" "$REPO" 2>"$SCRATCH/cfg.err") || stop "$CFG is not the document this check reads (a config fugaro wrote, with a credentials_service block and a repos list): $(sed -n '1,3p' "$SCRATCH/cfg.err" | tr '\n' ' ')"
  CFG_URL=$(field_of service_url "$cfgans")
  CFG_READY=$(field_of repo_ready "$cfgans")
  fact installation PASS "the config in force is the $NAME installation: project $PROJECT, region $REGION, Fugaro project $FPROJ; its own file ($CFG) carries the Firebase project $FB and the signer $SIGNER (whose domain is that project) and a restricted web API key (present, never printed); the sandbox repository is $REPO (slug $SLUG), $([ "$REPO_CHOSEN" = named ] && printf 'the one you named' || printf 'DERIVED as the only repository this installation has state for'), and the second repository the pairing case needs is $OTHER_REPO (slug $OTHER_SLUG)$([ -n "$CFG_URL" ] && printf '; the config records the credentials service at %s' "$CFG_URL" || printf '; the config records NO credentials service (credentials_service.url is absent), so no job of this installation is handed an address')"
}

# step_service finds the credentials service by its own label rather than by a
# name this script assumes, and reads the two facts the mint needs: its URL
# (which is also the audience every caller's ID token is minted for) and the
# Fugaro project its container names. The label is what fugaro's own Terraform
# writes on it (deploy/terraform/gcp/modules/installation/credentials.tf).
step_service() {
  if ! run_capture "$SCRATCH/services.json" gcloud run services list --platform=managed \
    --project "$P" --region "$REGION" --filter='metadata.labels.fugaro_role=credentials' --format=json; then
    stop "gcloud run services list did not answer for $P/$REGION: the check needs an account that may read Cloud Run there"
  fi
  local answer
  answer=$(reader service <"$SCRATCH/services.json") \
    || stop "no single credentials service is deployed in $P/$REGION (the service carries the label fugaro_role=credentials). Apply the installation first: 'fugaro update' deploys it on every installation"
  SVC=$(field_of service "$answer")
  [ -n "$SVC" ] || stop "the credentials service could not be named from the project's service list"
  if ! run_capture "$SCRATCH/service.json" gcloud run services describe "$SVC" --project "$P" --region "$REGION" --format=json; then
    stop "gcloud run services describe did not answer for $SVC"
  fi
  answer=$(reader svc <"$SCRATCH/service.json") || stop "the credentials service's own document is not the shape this check reads"
  SVCURL=$(field_of url "$answer")
  SVC_FPROJ=$(field_of fproj "$answer")
  SVCSA=$(field_of sa "$answer")
  [ -n "$SVCURL" ] || stop "'$SVC' has no URL: it is not serving, and every caller's ID token would be minted for an audience no revision answers to"
  [ -n "$SVCSA" ] || stop "'$SVC' runs as no service account, so this check cannot tell whose is the bundle's one accessor (Q6)"
  [ "$SVC_FPROJ" = "$FPROJ" ] || stop "the service's container names FUGARO_PROJECT=$SVC_FPROJ and the installation's own state names the Fugaro project $FPROJ: the run identity's fp claim and the service it is presented to are two different projects, so the mint could only be refused"
  if [ -n "$CFG_URL" ] && [ "$CFG_URL" != "$SVCURL" ]; then
    stop "the config in force records the credentials service at $CFG_URL and the deployed service answers at $SVCURL: a launch would hand its repository jobs an address that is not this service, and every call would be answered 401"
  fi
  fact service PASS "the credentials service is $SVC, deployed in $P/$REGION, serving at $SVCURL as $SVCSA, and its container names FUGARO_PROJECT=$SVC_FPROJ — the Fugaro project the installation's own state names$([ -n "$CFG_URL" ] && printf ', and the same URL the config in force records' || printf '; the config in force records no credentials service URL, so what the launch hands a job is judged in the job-env row')"
}

# step_job finds the sandbox repository's job by its own label: the job's name
# is the installation's to choose, and the label is what fugaro's own Terraform
# writes on it. A repository's jobs carry `fugaro_repo` and `fugaro_workflow`
# (one per workflow in its fugaro.yaml); the installation's support jobs carry
# `fugaro_role` instead, so they are not the job a run lands on.
step_job() {
  LABEL=$(reader label "$SLUG") || stop "the repository's own label could not be derived from its slug"
  if ! run_capture "$SCRATCH/jobs.json" gcloud run jobs list --project "$P" --region "$REGION" \
    --filter="metadata.labels.fugaro_repo=$LABEL" --format=json; then
    stop "gcloud run jobs list did not answer for $P/$REGION: the check needs an account that may read Cloud Run there"
  fi
  local answer
  if ! answer=$(reader jobs <"$SCRATCH/jobs.json"); then
    # No job of the repository's own. That is a finding about the launch (the
    # address goes on a repository's own job) rather than a reason to stop --
    # unless the installation has no shared project job either, which is the one
    # reading where this check has no job to speak about at all. The fallback is
    # a second list, by the mark a shared project job wears and a repository's
    # own does not: it carries no fugaro_repo, so the first filter cannot see it.
    if ! run_capture "$SCRATCH/projectjobs.json" gcloud run jobs list --project "$P" --region "$REGION" \
      --filter="metadata.labels.fugaro_profile:*" --format=json; then
      stop "gcloud run jobs list did not answer for $P/$REGION: the check needs an account that may read Cloud Run there"
    fi
    answer=$(reader projectjob <"$SCRATCH/projectjobs.json") \
      || stop "the sandbox repository $REPO ($REPO_COUNT of this installation's repositories have state) has no single job of its own in $P/$REGION: the job carries the label fugaro_repo=$LABEL, and its repositories are set up ('fugaro setup --repo $REPO' in a checkout of it) and applied ('fugaro update --repo') first"
  fi
  JOB=$(field_of job "$answer")
  JOB_KIND=$(field_of kind "$answer")
  [ -n "$JOB" ] || stop "the sandbox repository's job could not be named from the project's job list"
  if ! run_capture "$SCRATCH/job.json" gcloud run jobs describe "$JOB" --project "$P" --region "$REGION" --format=json; then
    stop "gcloud run jobs describe did not answer for $JOB"
  fi
  answer=$(reader job <"$SCRATCH/job.json") || stop "the job's own document is not the shape this check reads"
  JOBSA=$(field_of sa "$answer")
  APPID=$(field_of appid "$answer")
  [ -n "$JOBSA" ] || stop "the job $JOB names no service account, so there is no job account to impersonate"
  [ -n "$APPID" ] || stop "the job $JOB carries no FUGARO_GITHUB_APP_ID, so this check cannot make a mint call the service would admit (the request carries the App's ID and the ID alone)"
  local kindsaid="a repository job of $P/$REGION"
  local namesaid="the sandbox repository $REPO's own job is $JOB"
  if [ "$JOB_KIND" = project ]; then
    kindsaid="the shared project job of $P/$REGION (fugaro_profile)"
    namesaid="the sandbox repository $REPO has NO job of its own: the job this check then judges is $JOB, the installation's shared project job"
  fi
  fact job PASS "$namesaid, $kindsaid: it runs as $JOBSA and carries FUGARO_GITHUB_APP_ID=$APPID"
}

# step_job_env reads the job's own environment, mounts and secret references —
# names only, never a value — and holds what it finds to the installation's own
# record of the switch: the keyless path is opened by FUGARO_CREDENTIALS_SERVICE_URL
# alone (internal/gitprov/providers/providers.go), the launch is what puts it on
# a repository's job (internal/cli/run.go's credentialsServiceLaunchEnv), and
# the address it writes is the credential_service.url of the config in force.
step_job_env() {
  local answer envs mounts refs appkey url
  answer=$(reader job <"$SCRATCH/job.json")
  envs=$(field_of env "$answer")
  mounts=$(field_of mounts "$answer")
  refs=$(field_of refs "$answer")
  appkey=$(field_of appkey "$answer")
  url=$(field_of url "$answer")
  JOB_KEY_IN_SPEC=no
  if [ "$appkey" = present ]; then JOB_KEY_IN_SPEC=yes; fi
  local key_names=""
  [ -n "$envs" ] && key_names="$envs" || key_names="(none)"
  say "the job's container environment: $key_names"
  say "its mounts: $mounts; its secret environment references: $refs"

  if [ "$JOB_KIND" = project ]; then
    if [ "$url" = present ]; then
      fact job-env FAIL "the job $JOB is a SHARED PROJECT job (fugaro_profile) and its environment carries FUGARO_CREDENTIALS_SERVICE_URL: the variable is what opens the github provider keyless, and a shared project job's account is the one caller the service refuses, so telling it would break every GitHub run on it (Global Constraint 6). The launch puts the address on a repository's own job and on no other"
      return 0
    fi
    fact job-env PASS "the job $JOB is a shared project job: its environment does not carry FUGARO_CREDENTIALS_SERVICE_URL, which is the expected answer there (the service refuses its account as a caller, so the launch deliberately does not tell it), and FUGARO_GITHUB_APP_PRIVATE_KEY is $appkey in its environment"
    return 0
  fi
  if [ -z "$CFG_URL" ]; then
    fact job-env PASS "the job $JOB is the repository's own job, and the config in force records no credentials service (credentials_service.url is absent), so the launch hands no job an address: FUGARO_CREDENTIALS_SERVICE_URL is $url on it — which is what a repository whose root has not been re-applied with the switch on reads — and FUGARO_GITHUB_APP_PRIVATE_KEY is $appkey there"
    return 0
  fi
  if [ "$CFG_READY" = yes ]; then
    if [ "$url" = present ]; then
      fact job-env PASS "the job $JOB is the repository's own job and the config in force records this repository's root as applied with the credentials service on, and its environment carries FUGARO_CREDENTIALS_SERVICE_URL: that is the wiring, and its runner opens the github provider keyless (FUGARO_GITHUB_APP_PRIVATE_KEY is $appkey in its environment)"
    else
      fact job-env FAIL "the config in force records $REPO's root as applied with the credentials service on, and the repository's own job $JOB carries no FUGARO_CREDENTIALS_SERVICE_URL: a runner with no service to ask, which is a GitHub run that halts rather than one that falls back. Run 'fugaro update --repo' in a checkout of $REPO and re-apply its root"
    fi
    return 0
  fi
  fact job-env PASS "the job $JOB is the repository's own job, FUGARO_CREDENTIALS_SERVICE_URL is $url on it and FUGARO_GITHUB_APP_PRIVATE_KEY is $appkey in its environment: the config in force records this repository as NOT (yet) applied with the service on (credentials_service_ready reads $CFG_READY), which is byte for byte the job of a repository whose root has not been re-applied with the switch on — it reads the key and mints in-job exactly as before, and the mint below is made as the job's own account calls it, which the service admits on the account and not on the variable"
}

# step_app_key is the first claim: the job holds no App key. Three reads, in the
# one order that is honest — the checkout's own switch (the script reads the file
# beside itself, which is the code this run is a run of), the deployed job's
# spec, and Secret Manager — and the verdict is the CONSISTENCY of the two:
#
#   - the list names no App key and the job holds one -> the drop is HELD, which
#     is the state this check is written for and the expected answer today;
#   - the list names it and the job holds none -> the drop landed;
#   - the list names it and the job still holds one -> the deployment has not
#     been re-applied: the code decides the drop and the deployment does not
#     carry it;
#   - the list names nothing and the job holds none -> the drop landed without
#     the code that decides it (the two moved apart, and the code is the one
#     that decides).
step_app_key() {
  local providers=${CHECK96_PROVIDERS:-$SCRIPT_DIR/../../internal/config/providers.go}
  # The hook exists for this script's own hermetic test, which has to be able to
  # stand the switch both ways without touching the checkout. It reads a file on
  # this machine and reaches nothing, and it is held to a providers.go so a
  # mistyped value cannot quietly become a different fact.
  case "${providers##*/}" in
  providers.go) ;;
  *) stop "CHECK96_PROVIDERS=$providers does not name a providers.go: this hook exists for the script's own test and may only name the file that declares the switch" ;;
  esac
  if [ ! -r "$providers" ]; then
    stop "the switch this claim is read against is not there: $providers. Check 96 reads internal/config/providers.go beside the script it is run from, so run it out of a checkout of the fugaro repository"
  fi
  local answer
  answer=$(reader switch "$providers") || stop "$providers is not the file that declares ServiceHeldSecretNames"
  HELD_BY_CODE=$(field_of held "$answer")
  HELD_LINE=$(field_of line "$answer")
  HELD_NAMES=$(field_of names "$answer")

  if ! run_capture "$SCRATCH/secrets.json" gcloud secrets list --project "$P" --format=json; then
    stop "gcloud secrets list did not answer for $P: the check needs an account that may read Secret Manager there"
  fi
  answer=$(reader secrets <"$SCRATCH/secrets.json") \
    || stop "the project's secret list is not the shape this check reads, or it holds no single container labelled fugaro_secret=github-app-key and no single bundle container (fugaro_project and no fugaro_secret). Q6: the App key's container and the bundle are one each"
  APPKEY_SECRET=$(field_of appkey "$answer")
  BUNDLE_SECRET=$(field_of bundle "$answer")
  if ! run_capture "$SCRATCH/policy-bundle.json" gcloud secrets get-iam-policy "$BUNDLE_SECRET" --project "$P" --format=json; then
    stop "the IAM policy of the bundle's container ($BUNDLE_SECRET) could not be read: Q6's ruling is about exactly what is bound on it"
  fi
  local appans bundleans
  # No App key container at all is a possible reading of the two: nothing can
  # mount what does not exist, so no job holds it and there is no policy to
  # read. Which of the four pairs that is still depends on the switch.
  local job_member_on_key=no
  if [ -n "$APPKEY_SECRET" ]; then
    if ! run_capture "$SCRATCH/policy-app.json" gcloud secrets get-iam-policy "$APPKEY_SECRET" --project "$P" --format=json; then
      stop "the IAM policy of the App key's container ($APPKEY_SECRET) could not be read: this claim is about who holds an accessor on it"
    fi
    appans=$(reader policy "$JOBSA" "$SVCSA" <"$SCRATCH/policy-app.json") || stop "the App key's IAM policy is not the document this check reads"
    job_member_on_key=$(field_of job_member "$appans")
  fi
  bundleans=$(reader policy "$JOBSA" "$SVCSA" <"$SCRATCH/policy-bundle.json") || stop "the bundle's IAM policy is not the document this check reads"
  local job_member_on_bundle
  job_member_on_bundle=$(field_of job_member "$bundleans")

  if [ "$job_member_on_key" = yes ]; then JOB_HOLDS_KEY=yes; elif [ "$JOB_KEY_IN_SPEC" = yes ]; then JOB_HOLDS_KEY=yes; else JOB_HOLDS_KEY=no; fi

  local appkey_secret_said="no container labelled fugaro_secret=github-app-key exists in $P at all"
  if [ -n "$APPKEY_SECRET" ]; then appkey_secret_said="its container is $APPKEY_SECRET"; fi
  local detail
  detail="the checkout's switch (internal/config/providers.go, line $HELD_LINE) is ServiceHeldSecretNames = [$HELD_NAMES], so the code says the job holds $([ "$HELD_BY_CODE" = yes ] && echo "no App key" || echo "the App key"); the deployed job's environment names FUGARO_GITHUB_APP_PRIVATE_KEY $([ "$JOB_KEY_IN_SPEC" = yes ] && echo yes || echo no), the job account $JOBSA is a member of the App key container's policy ($job_member_on_key) and $appkey_secret_said"
  if [ "$HELD_BY_CODE" = yes ] && [ "$JOB_HOLDS_KEY" = yes ]; then
    fact app-key FAIL "$detail. That pair is the drop landing without the deployment: the code decides it (ServiceHeldSecretNames is what turns C2.3's drop on) and this installation has not been re-applied with the release that carries it — run 'fugaro update' and re-apply the repository's root"
  elif [ "$HELD_BY_CODE" = no ] && [ "$JOB_HOLDS_KEY" = yes ]; then
    fact app-key PASS "$detail. That pair is C2.3's drop HELD: the list names no App key and the deployed job still holds one, which is the expected state on this release and not a failure — the flip waits on this check's own live result (#907), and what the flip will have to read is the two rows below"
  elif [ "$HELD_BY_CODE" = yes ] && [ "$JOB_HOLDS_KEY" = no ]; then
    fact app-key PASS "$detail. That pair is C2.3's drop LANDED: the list names the App key and the deployed job holds none of it — no environment name, no mount and no accessor — which is the state the release behind this one has to reach"
  else
    fact app-key FAIL "$detail. That pair is the drop landing WITHOUT the code that decides it: the deployed job holds no App key while this checkout's ServiceHeldSecretNames names none, and the two have moved apart — the code is the one that decides which job gets the mount, so a job without it is a job whose next apply puts it back"
  fi
  # The bundle is the other half of Q6, and it is not about the job at all: the
  # service mints from it, exactly one account may read it, and a job account
  # with an accessor on it is the finding the mint's whole design rules out.
  local bindings role members cond svcmember
  bindings=$(field_of bindings "$bundleans")
  role=$(field_of role "$bundleans")
  members=$(field_of members "$bundleans")
  cond=$(field_of cond "$bundleans")
  # The member is part of the claim, not just its count: one binding whose one
  # member is not the service's own account is a bundle the service cannot mint
  # from, and a row that said "the service's own account" over it would be
  # asserting what it did not read.
  svcmember=$(field_of svc_member "$bundleans")
  if [ "$bindings" = 1 ] && [ "$role" = "roles/secretmanager.secretAccessor" ] && [ "$members" = 1 ] && [ "$svcmember" = yes ] && [ "$cond" = no ] && [ "$job_member_on_bundle" = no ]; then
    fact bundle PASS "the bundle's container is $BUNDLE_SECRET and its policy is exactly one binding — roles/secretmanager.secretAccessor on $SVCSA, the service's own account, with no condition and no second member (Q6, Global Constraint 12) — and the job account $JOBSA is a member of it: no"
  else
    fact bundle FAIL "the bundle's container is $BUNDLE_SECRET and its policy is NOT Q6's one grant: $bindings binding(s), roles $role, $members member(s), the service's own account among them: $svcmember, a condition: $cond, and the job account $JOBSA a member of it: $job_member_on_bundle. Exactly one 'roles/secretmanager.secretAccessor' on the service's own account ($SVCSA), no condition and no second member is what the service mints from, and a job account with an accessor on the bundle is a job that could read every credential in it"
  fi
}

# step_front_end: Cloud Run IAM is the outer fence, not the whole gate. A call
# with no token at all must be refused by the platform, before the container
# runs, so the answer is Google's and the service writes no audit line for it.
# The line count is read before and after the call: the call carries no identity
# to name it by, so a count that does not move is what "no line for it" means.
step_front_end() {
  local before after status refused error
  before=$(audit_lines "jsonPayload.purpose=\"mint\"") || stop "the service's own log could not be read (the fugaro-dev project's Cloud Logging): this row is the difference between the front end refusing the call and the service answering it"
  status=$(no_identity_call) || stop "the unauthenticated call did not get an answer from $SVCURL: is the URL right (it is the deployed service's own), and is there a network path to it?"
  after=$(audit_lines "jsonPayload.purpose=\"mint\"") || stop "the service's own log could not be read a second time"
  # The status alone cannot tell the two 403s apart, and the line count cannot
  # either: a service deployed WITHOUT IAM lets the call reach the container,
  # which refuses it at the shared header parse -- a call with no Authorization
  # header at all fails RequestFromHTTP, and server.go answers that through
  # refuse with unverified_run_identity and writes no line for it -- the same
  # status and the same unmoved count the front end's own refusal leaves. The
  # body separates them: Google's refusal is the platform's HTML, and the
  # service's is its typed JSON. So the body is read first, and the count
  # decides only what it leaves.
  refused=$(reader refusal <"$SCRATCH/noident.json" 2>/dev/null) || refused=""
  error=$(field_of error "$refused")
  case "$status" in
  403)
    if [ -n "$error" ]; then
      fact front-end FAIL "the call with no identity at all was refused $status $error BY THE SERVICE ($before purpose=mint line(s) before it and $after after it): a typed answer to a call with no identity is a call that reached the container, so Cloud Run IAM is not on for this service. Read 'gcloud run services get-iam-policy $SVC' — the invoker grant is the outer fence this whole check is layered on, and without it the account table is the only gate"
    elif [ "$before" = "$after" ]; then
      fact front-end PASS "the call with no identity at all was refused $status by Cloud Run's own front end, and the service's log carries $before line(s) with purpose=mint before it and $after after it: an unauthenticated request never reaches the container (the answer is the platform's own, not the service's typed refusal), so the service's own account check is layered on top of IAM rather than instead of it"
    else
      fact front-end FAIL "the call with no identity was refused $status, and the service's log carries $after purpose=mint line(s) where it carried $before before the call: a line for a call with no identity is a line this service cannot write, so read the two dumps before concluding anything (another run's mint can land in the window and is the honest cause)"
    fi
    ;;
  200)
    fact front-end FAIL "the call with no identity at all was ANSWERED $status: the service is deployed without IAM, and every claim below would be about a service anyone can call"
    stop "the credentials service answers an unauthenticated call"
    ;;
  *)
    fact front-end FAIL "the call with no identity at all was answered $status, and this check expects the front end's 403: read the body in $SCRATCH/noident.json and the service's own revision"
    ;;
  esac
}

# identity SLUG RUN — mints a run identity and exchanges it for a live Firebase
# ID token, printing the token on stdout and nothing else. It is the launcher's
# own path: a custom token whose claims are internal/budget/token's MintAt (uid
# r~<slug>~<run>, fs/fr/fx/fp/rb), signed by the installation's signer, traded
# at accounts:signInWithCustomToken with the restricted web API key. Neither the
# signed custom token nor the answer is ever written down: the signed token is
# the one credential that touches the disk (0600 in the scratch directory, and
# removed the moment it is read into a variable), and the exchange's answer is a
# shell variable.
identity() { # identity VARNAME SLUG RUN — sets VARNAME in THIS shell
  local outvar=$1 slug=$2 run=$3 now jwt resp uid tok
  now=$(date -u +%s)
  reader claims "$SIGNER" "$slug" "$run" "$now" "$FPROJ" "$SCRATCH/claims.json" \
    || { say "the custom token's claims could not be written"; return 1; }
  gcloud iam service-accounts sign-jwt --iam-account="$SIGNER" --project="$FB" \
    "$SCRATCH/claims.json" "$SCRATCH/custom.jwt" >/dev/null 2>"$SCRATCH/sign.err" || {
    say "the signer would not sign: it needs iam.serviceAccounts.signJwt for your identity (the fugaroTokenMinter grant on $SIGNER)"
    sed -n '1,3p' "$SCRATCH/sign.err" >&2 || true
    return 1
  }
  chmod 600 "$SCRATCH/custom.jwt" 2>/dev/null || true
  jwt=$(cat "$SCRATCH/custom.jwt")
  rm -f "$SCRATCH/custom.jwt" "$SCRATCH/claims.json"
  resp=$(printf 'url = "%s"\nheader = "Content-Type: application/json"\ndata = "{\\"token\\":\\"%s\\",\\"returnSecureToken\\":true}"\n' \
    "https://identitytoolkit.googleapis.com/v1/accounts:signInWithCustomToken?key=$FIREKEY" "$jwt" |
    curl -sS --config - 2>"$SCRATCH/exchange.err") || {
    say "the exchange of the run identity did not get an answer from Identity Toolkit"
    sed -n '1,3p' "$SCRATCH/exchange.err" >&2 || true
    return 1
  }
  jwt=""
  tok=$(printf '%s' "$resp" | reader identity) || {
    say "the run identity was not exchanged: the web API key has to be the $FB project's own restricted one, and the signer an account of it"
    return 1
  }
  resp=""
  [ -n "$tok" ] || { say "the exchange answered no ID token"; return 1; }
  uid=$(reader uid "$slug" "$run")
  UIDS_CREATED+=("$uid")
  # Written into the caller's own variable, never echoed: a command substitution
  # would run this function in a subshell and the uid just recorded -- the one
  # that says which users the `restore` line has to name if the run stops -- with
  # it.
  printf -v "$outvar" '%s' "$tok"
}

# mint CALLER RUNTOK — the one mint call, used by the positive case and by the
# pairing case. Both identities go into the curl config read from stdin, so
# neither is ever in argv, and the answer stays in ANSWER: minted tokens are
# never written to a file.
mint() { # mint CALLER RUNTOK — sets ANSWER and MINT_STATUS in THIS shell
  local out
  out=$(printf 'url = "%s"\nheader = "Authorization: Bearer %s"\nheader = "X-Fugaro-Run-Identity: %s"\nheader = "X-Fugaro-Run-Job: %s"\nheader = "Content-Type: application/json"\ndata = "{\\"app_id\\":\\"%s\\"}"\n' \
    "$SVCURL$MINT" "$1" "$2" "$JOB" "$APPID" |
    curl -sS --config - -w '\n%{http_code}' 2>"$SCRATCH/mint.err") || {
    say "the mint call did not get an answer from $SVCURL$MINT"
    sed -n '1,3p' "$SCRATCH/mint.err" >&2 || true
    return 1
  }
  ANSWER=${out%$'\n'*}
  MINT_STATUS=${out##*$'\n'}
}

# step_mint: the claim itself. The caller is the sandbox job's own account (an
# impersonated Google ID token for the service's URL, which needs
# iam.serviceAccounts.getOpenIdToken on it — a permission the installation's own
# grants deliberately do not give operators), the run identity is the sandbox
# repository's own, and the answer has to be the service's.
step_mint() {
  JOBTOK=$(gcloud auth print-identity-token --impersonate-service-account="$JOBSA" \
    --audiences="$SVCURL" --include-email 2>"$SCRATCH/impersonate.err") || {
    stop "no Google ID token for the job's own account $JOBSA — this step needs iam.serviceAccounts.getOpenIdToken on it (the installation's own grants give operators actAs only, and hold no such permission: modules/workflow/sa.tf). $(sed -n '1,3p' "$SCRATCH/impersonate.err" | tr '\n' ' ')"
  }
  [ -n "$JOBTOK" ] || stop "the impersonation of $JOBSA answered no token"
  RUN="$(date -u +%Y%m%d-%H%M%S)-c96a"
  identity RUNTOK "$SLUG" "$RUN" || stop "the run identity of $SLUG could not be minted and exchanged (the lines above name which half failed)"
  say "the caller is $JOBSA and the run identity is the sandbox repository's own (uid r~$SLUG~$RUN): both are read by shape, and neither token is printed"
  mint "$JOBTOK" "$RUNTOK" || stop "the mint call did not answer"
  local ans
  # The status first: a refusal carries no token, so parsing the answer as an
  # issuance before reading the status would report a refusal as a document this
  # check cannot read — and the refusal's own error is the useful fact.
  if [ "$MINT_STATUS" != 200 ]; then
    local refusal
    refusal=$(printf '%s' "$ANSWER" | reader refusal 2>/dev/null) || refusal=""
    MINT_REFUSED=yes
    fact mint FAIL "the mint answered $MINT_STATUS, not 200: $(field_of error "$refusal") — $(field_of message "$refusal"). Read the audit row: the service records a refusal like an issuance, which is what tells a refusal at the bundle (the operator has not run 'fugaro creds set github-app-key') from one at the identity or the pairing. The rows below still ran: the pairing refusal, the outsider's answer and the audit line are about identities and accounts, and none of them needs a token"
    return 0
  fi
  ans=$(printf '%s' "$ANSWER" | reader mint) || stop "the mint answered 200 with no token in it: the answer is in the shell variable it was read into and is not written down, so all there is to report is that the service answered 200 without one"
  MINT_REPO=$(field_of repository "$ans")
  MINT_EXPIRES=$(field_of expires "$ans")
  MINT_IN_HOUR=$(field_of in_hour "$ans")
  TOK=$(field_of token "$ans")
  [ -n "$TOK" ] || stop "the mint answered 200 with no token in it"
  # The answer names the repository in the CANONICAL spelling —
  # "<provider>/<owner>/<name>", projectpolicy.Canonical's, which is what
  # internal/credsvc's MintResponse carries — while this check's own $REPO is the
  # "<owner>/<name>" the config and the budget document use. The two are the same
  # repository in two spellings, so the answer is held to the run's repository by
  # its tail: ONE segment stripped, and the rest exactly $REPO. Stripping one
  # rather than matching "*/$REPO" is what makes the comparison the canonical
  # form's own: "github/acme/acme/sandbox" is a repository called acme/sandbox
  # under an owner called acme, not this run's, and a glob would take it.
  MINT_TAIL=${MINT_REPO#*/}
  if [ "$MINT_TAIL" = "$MINT_REPO" ] || [ "$MINT_TAIL" != "$REPO" ]; then
    fact mint FAIL "the mint answered 200 with a token for $MINT_REPO while the run identity is a run of $REPO (uid r~$SLUG~$RUN): the service minted for a repository the run is not of, and the run identity — with the job's own account — is the whole of what decides which repository a mint is for. The token is in a shell variable and is not printed"
    return 0
  fi
  # The expiry is part of the claim, not decoration: GitHub's installation tokens
  # live an hour and the service mints them with that expiry, so a token that
  # outlives it (or arrives already expired) is a finding about the mint itself.
  if [ "$MINT_IN_HOUR" != yes ]; then
    fact mint FAIL "the mint answered 200 for $REPO ($MINT_REPO, the canonical spelling of the run's own repository) and its expiry is '$MINT_EXPIRES', inside the hour: $MINT_IN_HOUR — GitHub's installation tokens live an hour and the service mints them with that expiry, so a token outside it is the service's own expiry being wrong. The token is in a shell variable and is not printed"
    return 0
  fi
  fact mint PASS "the mint answered 200 for the calling account $JOBSA and the run identity of $SLUG (uid r~$SLUG~$RUN): repository $MINT_REPO, the service's canonical spelling of the run's OWN repository $REPO — the caller named nothing (the request has no repository field to fill in) and the account and the identity decided it — expires $MINT_EXPIRES, inside the hour: $MINT_IN_HOUR. The token is in a shell variable, is never printed, and is used for three reads and one probe below and for no write"
}

# step_reach reads the token's reach AT GITHUB, which is the only place it can be
# read: the permission set is the service's own (github.RunTokenPermissions) and
# what the token can really do is GitHub's answer.
step_reach() {
  # No token was minted, so there is nothing to read at GitHub: the row says so
  # rather than reporting a reach it never measured (the mint's own row above is
  # the finding, and an INFO here is what keeps the block from reading as a
  # second, identical one).
  if [ "${MINT_REFUSED:-no}" = yes ]; then
    fact reach INFO "not measured — the mint answered $MINT_STATUS and issued no token, so there is nothing to read at GitHub with. The mint row above is the finding"
    return 0
  fi
  local bodies ans contents actions
  bodies=$(github_get "https://api.github.com/installation/repositories") || stop "GitHub did not answer the installation's repository list: the token the service answered with is not one GitHub issued, or this machine has no path to api.github.com"
  ans=$(printf '%s' "$bodies" | reader repos) || stop "GitHub's repository list is not the document this check reads (a 401 or a 403 stands in its place, and a token GitHub refuses is a token it did not issue)"
  REACH_REPOS=$(field_of repos "$ans")
  REACH_ONLY=$(field_of only "$ans")
  contents=$(github_status "https://api.github.com/repos/$REPO/contents/") || contents="no-answer"
  actions=$(github_dispatch) || actions="no-answer"
  if [ "$REACH_REPOS" = 1 ] && [ "$REACH_ONLY" = "$REPO" ] && [ "$contents" = 200 ] && [ "$actions" = 403 ]; then
    fact reach PASS "the minted token reaches exactly one repository at GitHub — $REACH_ONLY, the account's own — reads contents ($contents), and is refused the actions dispatch the run's set does not grant ($actions, a 403 from GitHub itself: the probe asks for a workflow name that does not exist, so a 404 here would mean the permission was granted and the probe found nothing to dispatch, and a 200 would mean it started something)"
  else
    fact reach FAIL "the minted token's reach is not the run's own set: the installation's repository list holds $REACH_REPOS entr(y|ies)$([ -n "$REACH_ONLY" ] && printf ' (%s)' "$REACH_ONLY"), the contents read answered $contents, and the actions probe answered $actions. Expected exactly 1 entry, that repository, contents 200 and actions 403 (github.RunTokenPermissions): a 404 on the probe is a granted permission, and a 200 is a dispatch that started"
  fi
}

# step_audit reads the service's own line for the mint: the one place a mint is
# recorded, and it records the credential's NAME.
step_audit() {
  local ans
  ans=$(audit_for "$RUN") || stop "the service's own audit lines could not be read from the fugaro-dev project's Cloud Logging"
  local lines statuses credential purpose tier repository ghs
  lines=$(field_of lines "$ans")
  statuses=$(field_of statuses "$ans")
  credential=$(field_of credential "$ans")
  purpose=$(field_of purpose "$ans")
  tier=$(field_of tier "$ans")
  repository=$(field_of repository "$ans")
  ghs=$(field_of ghs "$ans")
  if [ "$lines" = 1 ] && [ "$statuses" = ok ] && [ "$credential" = github-app-key ] && [ "$purpose" = mint ] && [ -z "$tier" ] && [ "$repository" = "$SLUG" ] && [ "$ghs" = 0 ]; then
    fact audit PASS "the service's log carries exactly one audit line for this run: credential github-app-key (the name, never a value), purpose mint, no tier (the App key is not a model credential, and a tier on a mint is refused rather than written), status ok, repository $repository, caller the verified job account, and no line anywhere in it carrying the installation token's own prefix"
  else
    fact audit FAIL "the service's audit line for this run is not the one this claim is about: $lines line(s), status $statuses, credential $credential, purpose $purpose, tier '$tier', repository $repository, token-prefix occurrences $ghs. Expected exactly 1 line, status ok, credential github-app-key, purpose mint, no tier, repository $SLUG and no token in it anywhere: the service's log is where a mint is recorded, and it records the credential's name"
  fi
}

# step_pairing is the third claim, first half: a run identity for repository X
# cannot mint for Y. The run identity is another repository's of the same
# installation and the caller is the SANDBOX job's own account, so the pair is X's
# run and Y's job: the pairing question is what refuses it, and the refusal is
# answered after both identities were verified, which is why it is audited.
step_pairing() {
  RUNX="$(date -u +%Y%m%d-%H%M%S)-c96b"
  local status ans
  identity RUNTOKX "$OTHER_SLUG" "$RUNX" || stop "the second run identity ($OTHER_SLUG's) could not be minted and exchanged"
  mint "$JOBTOK" "$RUNTOKX" || stop "the pairing call did not answer"
  status=$MINT_STATUS
  ans=$(printf '%s' "$ANSWER" | reader refusal 2>/dev/null) || ans=""
  local error message
  error=$(field_of error "$ans")
  message=$(field_of message "$ans")
  if [ "$status" = 403 ] && [ "$error" = unverified_run_identity ] && [ "$message" = "the run identity is not a run of the calling job's repository" ]; then
    fact pairing PASS "a run identity of $OTHER_REPO with the sandbox job's own account is refused 403 unverified_run_identity — \"$message\" — with nothing minted: both identities were verified first (the refusal would name another cause otherwise) and the pairing is what refused it"
  else
    fact pairing FAIL "the pairing call answered $status with error '$error' and message '$message', and this claim needs 403 unverified_run_identity. A 200 is the finding this case exists to prevent — the sandbox job's account minted for another repository — and any other error is a different fact (an expired identity or a caller whose account is no job of this installation reads as unverified_job_identity)"
  fi
  # The refusal's own audit line: exactly one, because it was answered after
  # Auth.Verify returned a whole identity, and it names the RUN's repository and
  # not the calling job's.
  local lines statuses credential purpose tier repository ghs
  ans=$(audit_for "$RUNX") || stop "the service's audit lines for the second run could not be read"
  lines=$(field_of lines "$ans")
  statuses=$(field_of statuses "$ans")
  credential=$(field_of credential "$ans")
  purpose=$(field_of purpose "$ans")
  tier=$(field_of tier "$ans")
  repository=$(field_of repository "$ans")
  ghs=$(field_of ghs "$ans")
  if [ "$lines" = 1 ] && [ "$statuses" = unverified_run_identity ] && [ "$credential" = github-app-key ] && [ "$purpose" = mint ] && [ -z "$tier" ] && [ "$repository" = "$OTHER_SLUG" ] && [ "$ghs" = 0 ]; then
    fact pairing PASS "the refusal is recorded and is the ROUTE's line, not the branch's: exactly 1 audit line for the second run — purpose mint, credential github-app-key, no tier, status unverified_run_identity, repository $repository (the identity's own repository, which is what the refusal is about, and not the calling job's), and no token in it anywhere. It is written before the typed refusal goes out, which is what tells an answer on refuseCall's path from one on refuse's"
  else
    fact pairing FAIL "the pairing refusal's audit line is not the route's: $lines line(s), status $statuses, credential $credential, purpose $purpose, tier '$tier', repository $repository, token-prefix occurrences $ghs. Exactly 1 line with status unverified_run_identity, purpose mint, credential github-app-key, no tier and repository $OTHER_SLUG is expected: a 0 here would be the finding — a refusal this service made and left unrecorded, which means it took refuse's path instead of refuseCall's — and any other reading says the refusal was made for a different reason"
  fi
}

# step_outsider is the second half of the third claim: a caller that is no job
# account at all. It has to be a service account (gcloud refuses --audiences for
# a user account, and the mint route's class keeps the service's URL as the
# token's audience), and the installation's own grants give run.invoker to job
# accounts alone, so a stock installation refuses this at the front end before
# the container runs — the same fact the front-end row read — while one that has
# been granted an invoker reaches the container and is refused there.
step_outsider() {
  NONJOBTOK=$(gcloud auth print-identity-token --impersonate-service-account="$SIGNER" \
    --audiences="$SVCURL" --include-email 2>"$SCRATCH/nonjob.err") || {
    stop "no Google ID token for $SIGNER — this step needs iam.serviceAccounts.getOpenIdToken on an account that is no job account of this installation (the installation's own grants give operators actAs only, and hold no such permission: modules/workflow/sa.tf). $(sed -n '1,3p' "$SCRATCH/nonjob.err" | tr '\n' ' ')"
  }
  [ -n "$NONJOBTOK" ] || stop "the impersonation of $SIGNER answered no token"
  local status ans
  mint "$NONJOBTOK" "$RUNTOK" || stop "the call from an account that is no job account did not answer"
  status=$MINT_STATUS
  ans=$(printf '%s' "$ANSWER" | reader refusal 2>/dev/null) || ans=""
  local error message
  error=$(field_of error "$ans")
  message=$(field_of message "$ans")
  local lines statuses
  ans=$(audit_for "$RUN") || stop "the service's audit lines for the mint's run could not be read a second time"
  lines=$(field_of lines "$ans")
  statuses=$(field_of statuses "$ans")
  if [ "$status" = 200 ]; then
    fact outsider FAIL "a caller that is no job account of this installation — $SIGNER, the installation's own token signer — was ANSWERED 200 by the mint: the account table is the gate and the invoker grant only a fence, so a 200 here means any account that can reach the service can mint with any run identity it can mint for"
  elif [ "$status" = 403 ] && [ "$error" = unverified_job_identity ]; then
    fact outsider PASS "the call from $SIGNER was refused 403 unverified_job_identity by the service itself (the log carries $statuses for the mint's run): this account holds run.invoker on the service — a debugging grant nothing in the installation makes for you — so the front end admitted it and the account table refused it, which is the claim that the table is the gate and the invoker grant only a fence"
  elif [ "$status" = 403 ] && [ -z "$error" ] && [ "$lines" = 1 ]; then
    # A 403 with no parsed body is the front end's own answer: Google's refusal
    # is HTML, so the service never saw the call and wrote no line for it. This
    # is judged second because a body that names an error is the service's, and
    # that answer says more than the count of lines does.
    fact outsider PASS "the call from $SIGNER, an account that is no job account of this installation, was refused 403 by Cloud Run's own front end: the service's log carries $lines line for the mint's run (its own issuance) and none for this call, so the request never reached the container — which is the stock installation, whose only invoker grants are the job accounts' own (modules/workflow/sa.tf)"
  else
    fact outsider FAIL "the call from $SIGNER answered $status with error '$error' and message '$message', and the mint's run has $lines audit line(s) ($statuses). Expected a 403 either way: from the front end with no line for the call, or from the service as unverified_job_identity"
  fi
}

# step_survivors is the final assertion: the identities this check minted are
# Firebase Auth users (r~<slug>~<run>, one per exchange) and they are deleted the
# way they were created — as the owner, with the project's own API — and the
# user list is read back. The access token is passed in a curl config read from
# stdin, so it is never in argv either.
step_survivors() {
  local at uid deleted=0 code
  at=$(gcloud auth print-access-token 2>"$SCRATCH/at.err") || stop "no access token for the project's own Identity Toolkit API: the two users this check created cannot be deleted"
  [ -n "$at" ] || stop "gcloud answered no access token"
  for uid in ${UIDS_CREATED[@]+"${UIDS_CREATED[@]}"}; do
    code=$(printf 'url = "https://identitytoolkit.googleapis.com/v1/projects/%s/accounts:delete"\nheader = "Authorization: Bearer %s"\nheader = "Content-Type: application/json"\ndata = "{\\"localId\\":\\"%s\\"}"\n' \
      "$FB" "$at" "$uid" | curl -sS --config - -o /dev/null -w '%{http_code}') || code="curl-failed"
    if [ "$code" = 200 ]; then deleted=$((deleted + 1)); fi
    say "deleted the run identity user $uid: status $code"
  done
  at=""
  local answer read_back left
  answer=$(printf 'url = "https://identitytoolkit.googleapis.com/v1/projects/%s/accounts:batchGet?maxResults=1000"\nheader = "Authorization: Bearer %s"\n' "$FB" "$(gcloud auth print-access-token 2>/dev/null)" | curl -sS --config -) \
    || stop "the project's user list could not be read back, so this run cannot say whether what it created is gone"
  # The reader's status, not sed's: the count it prints is the whole assertion of
  # this row, and a list this check cannot parse would otherwise read back as the
  # empty string — a document that was never read scored as a count. It fails
  # closed (an empty count is not zero, so the row FAILs), and the FAIL's own
  # sentence would name no number at all, which is why the read stops the run
  # instead: a list that cannot be read is a fact this check cannot tell.
  read_back=$(printf '%s' "$answer" | reader uids "${UIDS_CREATED[@]+"${UIDS_CREATED[@]}"}") \
    || stop "the project's user list is not the document this check reads, so whether what this run created is gone cannot be read back"
  left=$(field_of left "$read_back")
  [ -n "$left" ] || stop "the project's user list was read back and carries no count: this row's assertion is that count, and it cannot be taken from a document that does not carry one"
  SURVIVORS_DONE=yes
  if [ "$left" = 0 ]; then
    fact survivors PASS "the two run identity users this check exchanged were deleted ($deleted of ${#UIDS_CREATED[@]} answered 200) and the project's own user list holds 0 of their uids: nothing this check created survives it"
  else
    fact survivors FAIL "the project's user list still holds $left of the uids this check created ($deleted of ${#UIDS_CREATED[@]} deletions answered 200): the read-back IS the assertion, and a survivor is a failure this check reports rather than leaves"
  fi
}

# ------------------------------------------------------- the calls, in one place
# audit_lines FILTER — the number of audit lines the service's log carries for a
# filter. It is a count and never a document: the log is filtered by the
# service's own resource labels.
audit_lines() {
  if ! run_capture "$SCRATCH/audit-count.json" gcloud logging read --project "$P" \
    --limit=100 --freshness="${CHECK96_LOG_FRESHNESS:-15m}" --format=json \
    "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"$SVC\" $1"; then
    return 1
  fi
  # The reader's own status is part of the read. `gcloud logging read` can exit 0
  # with something this check cannot parse -- a notice on stdout, a shape that is
  # not a list -- and a count taken from that document is the empty string. The
  # counts this row hands out are compared with each other for equality, and two
  # empty strings are equal: that is the shape in which a row asserts "the service
  # wrote no line for it" from a log it never read. So the reader is read as a
  # value whose status is checked, and a count that is not there fails the read
  # rather than reading as zero.
  local ans lines
  ans=$(reader count <"$SCRATCH/audit-count.json") || return 1
  lines=$(field_of lines "$ans")
  [ -n "$lines" ] || return 1
  printf '%s' "$lines"
}

# audit_for RUN — the service's audit lines for one run id, as the reader's
# vocabulary: how many, their statuses, and the first line's fields.
audit_for() {
  if ! run_capture "$SCRATCH/audit-$1.json" gcloud logging read --project "$P" \
    --limit=50 --freshness="${CHECK96_LOG_FRESHNESS:-15m}" --format=json \
    "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"$SVC\" jsonPayload.run=\"$1\""; then
    return 1
  fi
  reader audit <"$SCRATCH/audit-$1.json"
}

# no_identity_call — the mint endpoint with no Authorization header at all. Its
# body is written to a file because it is Google's, not this service's, and it is
# printed nowhere; what the row reads is the status.
no_identity_call() {
  printf 'url = "%s"\nheader = "Content-Type: application/json"\ndata = "{\\"app_id\\":\\"%s\\"}"\n' \
    "$SVCURL$MINT" "$APPID" |
    curl -sS --config - -o "$SCRATCH/noident.json" -w '%{http_code}'
}

# github_get URL / github_status URL / github_dispatch — the three GitHub reads,
# all of them with the minted token in a config read from stdin.
github_get() {
  printf 'url = "%s"\nheader = "Authorization: Bearer %s"\nheader = "Accept: application/vnd.github+json"\nheader = "X-Github-Api-Version: 2022-11-28"\n' \
    "$1" "$TOK" | curl -sS --config -
}
github_status() {
  printf 'url = "%s"\nheader = "Authorization: Bearer %s"\nheader = "Accept: application/vnd.github+json"\nheader = "X-Github-Api-Version: 2022-11-28"\n' \
    "$1" "$TOK" | curl -sS --config - -o /dev/null -w '%{http_code}'
}
github_dispatch() {
  printf 'url = "%s"\nheader = "Authorization: Bearer %s"\nheader = "Accept: application/vnd.github+json"\nheader = "X-Github-Api-Version: 2022-11-28"\nheader = "Content-Type: application/json"\ndata = "{}"\n' \
    "https://api.github.com/repos/$REPO/actions/workflows/chk96-nonexistent.yml/dispatches" "$TOK" |
    curl -sS --config - -X POST -o /dev/null -w '%{http_code}'
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(installation service job job-env app-key bundle front-end mint reach audit pairing outsider survivors xtrace restore)
NOT_REACHED_TEXT="FAIL — not reached (the run stopped before this row)"

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  # The check's own secret scan: a `ghs_` string is the fixed prefix of every
  # GitHub installation token, and a block whose lines could carry one would be
  # the leak this check exists to rule out. A line that carries one is dropped
  # and the block says so rather than printing it.
  if printf '%s\n' "$facts" | grep -q 'ghs_'; then
    facts=$(printf '%s\n' "$facts" | grep -v 'ghs_' || true)
    SECRET_SCAN=yes
    facts="${facts}"$'\n'"secret-scan|FAIL|a line of this run carried the fixed prefix of a GitHub installation token, so the block prints the run without it: the value is nowhere in what you paste back, and its presence is itself the finding"
    status=1
  fi
  local key printed=""
  printf '\nCHECK 96 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line all
    all=$(printf '%s\n' "$facts" | grep "^$key|" || true)
    # A step may record two facts under one key -- the pairing row says both
    # "the refusal was made" and "the refusal is recorded" -- and one row is
    # what the block has. The one that FAILED is the one it prints: a block
    # that showed a PASS over the finding under it would read as an answer
    # about the mint when the run's own verdict is a finding.
    line=$(printf '%s\n' "$all" | grep -m1 '|FAIL|' || true)
    if [ -z "$line" ]; then line=$(printf '%s\n' "$all" | head -n 1); fi
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
  printf 'CHECK 96 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
USAGE="check96.sh [--dry-run] [<owner/name>]"

parse_args() {
  REPO_ARG=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --dry-run) DRY_RUN=1 ;;
    -h | --help)
      HELP_SHOWN=yes
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    -*) die "unknown argument '$1'. usage: $USAGE" ;;
    *)
      [ -z "$REPO_ARG" ] || die "only one repository may be named. usage: $USAGE"
      REPO_ARG=$1
      ;;
    esac
    shift
  done
}

print_plan() {
  say "dry run: NOTHING below was run. A real run would, in this order:"
  say "  [network] a POST to $SVCURL$MINT with no identity at all, expecting the front end's 403 (step 2 of the register's check: the outer fence), and two reads of the service's own log around it"
  say "  [network] gcloud auth print-identity-token --impersonate-service-account=$JOBSA --audiences=$SVCURL --include-email (the caller's ID token)"
  say "  [network] gcloud iam service-accounts sign-jwt as $SIGNER, and the custom token traded at accounts:signInWithCustomToken with $FB's restricted web API key (the run identity)"
  say "  [network, WRITES] the mint: POST $SVCURL$MINT with both identities, which MINTS a real GitHub installation token for $REPO and creates a Firebase Auth user (uid r~$SLUG~<run>)"
  say "  [network] three read-only GitHub calls with that token: the installation's repository list, a contents read and one probe against a workflow name that does not exist"
  say "  [network] two more run identities and two more calls: the pairing refusal ($OTHER_SLUG's run identity with $JOB's own account) and a caller that is no job account ($SIGNER)"
  say "  [network] the service's own log, read for the two runs' audit lines"
  say "and its Cleanup (from the exit trap, and the last step) would run:"
  say "  [network, DELETES] the two Firebase Auth users this run created, at Identity Toolkit's accounts:delete, then the project's user list read back"
  say "  [local] rm -rf of the scratch directory $SCRATCH, which holds the log dumps and the one credential that touches the disk (the custom token gcloud signs, 0600 and removed the moment it is read)"
  say "The one thing it cannot take back: the installation token the mint answers with is real at GitHub, this check never writes it anywhere, and GitHub expires it inside the hour."
  say "Reads this dry run made: fugaro doctor --json, the installation's own config, fugaro budget show --all --json, gcloud run services list/describe, gcloud run jobs list/describe, gcloud secrets list and the two IAM policies. Nothing above was run."
}

main() {
  need_tools
  parse_args "$@"
  guard_repository
  scratch_up
  write_files
  guard_xtrace
  step_installation
  step_service
  step_job
  step_job_env
  step_app_key

  if [ -n "$DRY_RUN" ]; then
    NOT_REACHED_TEXT="INFO — not measured (--dry-run: nothing live was run, so nothing below is a verdict)"
    print_plan
    fact dry-run PASS "guards and every read only; the calls that write — the mint, the pairing and outsider calls, and the two user deletions — and their cleanup were printed, not run. Exit 0 here means the guards and the reads passed, not that any claim is verified"
    REACHED_END=yes
    trap - ERR
    return 0
  fi

  step_front_end
  step_mint
  step_reach
  step_audit
  step_pairing
  step_outsider
  step_survivors

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
"""Check 96's reader: every document this check reads is parsed here.

It prints a fixed vocabulary and never a document -- `name=`, `project=`,
`region=`, `repo=`, `slug=`, `service=`, `url=`, `job=`, `sa=`, `appid=`,
`present`, `absent`, `yes`, `no`, `lines=3` -- so what reaches the block is the
check's own words about what it read. Two subcommands are exceptions, both of
them deliberate and both of them never printed by the script:

  - `identity` prints the run's ID token on stdout, for a shell variable;
  - `mint` prints the answer's `token=` field, for a shell variable.

Neither value is written to a file, and the block's own secret scan drops any
line carrying a GitHub installation token's prefix before it prints.
"""
import json, re, sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def parse_json(raw):
    try:
        return json.loads(raw)
    except ValueError as e:
        fail("not JSON: %s" % e)


def load(path=None):
    raw = open(path, encoding="utf-8").read() if path else sys.stdin.read()
    return parse_json(raw)


def env_of(container):
    """One container's environment as a dict. A value_source entry (a mounted
    secret) reads as the empty string, which is what makes `present` mean a
    plain value was set."""
    out = {}
    for entry in container.get("env") or []:
        value = entry.get("value")
        out[entry.get("name") or ""] = "" if value is None else value
    return out


def cmd_doctor():
    doc = load()
    project = doc.get("project") or {}
    print("name=%s" % (project.get("name") or ""))
    print("project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))


def cmd_budget(want):
    """The sandbox repository, and the second one the pairing case needs. Named
    when the owner named it, and otherwise only when exactly one repository of
    this installation has state -- never a pick among several: this check mints a
    REAL GitHub installation token for the repository it is handed and probes it
    at GitHub, so choosing one the owner did not name would spend a token on a
    repository that may not be the sandbox at all. The refusal names every
    candidate, so the owner has the one word to add."""
    doc = load()
    rows = doc.get("repos") or []
    with_state = [r for r in rows if r.get("repo") and r.get("slug")]
    print("project=%s" % (doc.get("project") or ""))
    print("repos=%d" % len(with_state))
    if want:
        mine = [r for r in with_state if r.get("repo") == want]
        if not mine:
            fail("%s is not one of this installation's repositories with state" % want)
        chosen = "named"
    else:
        if not with_state:
            fail("this installation has no repository with state")
        if len(with_state) > 1:
            fail("this installation has %d repositories with state and none was named, so this check cannot tell which one is the sandbox: name it, one of %s"
                 % (len(with_state), ", ".join(sorted(r.get("repo") or "" for r in with_state))))
        mine = with_state[:1]
        chosen = "derived"
    others = [r for r in with_state if r.get("repo") != mine[0].get("repo")]
    print("repo=%s" % (mine[0].get("repo") or ""))
    print("slug=%s" % (mine[0].get("slug") or ""))
    print("chosen=%s" % chosen)
    if others:
        print("other_repo=%s" % (others[0].get("repo") or ""))
        print("other_slug=%s" % (others[0].get("slug") or ""))


def cmd_config(path, repo):
    """The installation's own config: its credentials_service block and the
    repository's record of the switch. Line-based on purpose -- the file is
    fugaro's own output, and this reads two keys of it. The restricted web API
    key is NOT read here: it never enters this process."""
    text = open(path, encoding="utf-8").read()
    url, in_block = "", False
    for line in text.splitlines():
        if re.match(r"^credentials_service:", line):
            in_block = True
            continue
        if in_block:
            if re.match(r"^\S", line):
                in_block = False
            elif re.match(r"^\s+url:\s*\S", line):
                url = line.split("url:", 1)[1].strip().strip("\"'")
    # repos is a MAPPING keyed by owner/name (localcfg.Project.Repos), so the
    # repository is a key at the block's own indentation and the flag is one
    # level under it. That indentation is READ from the block's first key rather
    # than assumed to be two spaces: localcfg.Config.Marshal is yaml.Marshal
    # (yaml.v3, four spaces), and a config written by fugaro itself read at two
    # is a config in which no repository is ever found -- repo_ready "unset" for
    # every one of them, which is how the row this feeds would pass a job that
    # has lost the service's address.
    ready, in_repos, current, entry_pad = "unset", False, None, None
    for line in text.splitlines():
        if re.match(r"^repos:", line):
            in_repos, current, entry_pad = True, None, None
            continue
        if not in_repos:
            continue
        if re.match(r"^\S", line):
            in_repos = False
            continue
        m = re.match(r"^(\s+)([^\s:][^:]*):(.*)$", line)
        if not m:
            continue
        pad, key, rest = m.group(1), m.group(2).strip().strip("\"'"), m.group(3).strip()
        if entry_pad is None:
            entry_pad = len(pad)
        if len(pad) <= entry_pad:
            # An entry of the mapping: the value is a block, so it never carries
            # one here, and a nested key that does (`workflows:`) must not be
            # mistaken for one.
            current = key
            continue
        if current == repo and key == "credentials_service_ready":
            ready = "yes" if rest.strip("\"'") == "true" else "no"
    if repo == "":
        ready = "unset"
    print("service_url=%s" % url)
    print("repo_ready=%s" % ready)


def cmd_label(slug):
    print(re.sub(r"[^a-z0-9_-]", "_", slug))


def cmd_service():
    found = []
    for svc in load():
        labels = (svc.get("metadata") or {}).get("labels") or {}
        if labels.get("fugaro_role") == "credentials":
            found.append(svc)
    if len(found) != 1:
        fail("the project lists %d services labelled fugaro_role=credentials" % len(found))
    print("service=%s" % ((found[0].get("metadata") or {}).get("name") or ""))


def cmd_svc():
    svc = load()
    spec = ((svc.get("spec") or {}).get("template") or {}).get("spec") or {}
    containers = spec.get("containers") or [{}]
    env = env_of(containers[0])
    url = (svc.get("status") or {}).get("url") or ""
    print("url=%s" % url)
    print("fproj=%s" % env.get("FUGARO_PROJECT", ""))
    print("sa=%s" % (spec.get("serviceAccountName") or ""))


def cmd_jobs():
    """The repository's OWN jobs: those carrying its fugaro_repo label and no
    fugaro_role. A support job (a repository's image check) wears the role
    instead of the workflow label, so it is not a job a run lands on; a
    repository's jobs are one per workflow in its fugaro.yaml, and this check
    refuses to pick between two."""
    found = []
    for job in load():
        labels = (job.get("metadata") or {}).get("labels") or {}
        if labels.get("fugaro_repo") and not labels.get("fugaro_role"):
            found.append(job)
    if len(found) != 1:
        fail("the repository has %d jobs of its own" % len(found))
    labels = (found[0].get("metadata") or {}).get("labels") or {}
    print("job=%s" % ((found[0].get("metadata") or {}).get("name") or ""))
    print("kind=%s" % ("project" if labels.get("fugaro_profile") else "repository"))
    print("workflow=%s" % (labels.get("fugaro_workflow") or ""))


def cmd_projectjob():
    """The installation's shared project job: the one job a repository with no
    job of its own would run on. It carries nothing per-repository, so it wears
    fugaro_profile and no fugaro_repo -- which is why the repository's own list
    cannot see it and this is a second read. A shared project job is what this
    check then judges; a project that runs several profiles is a shape it
    refuses to pick between rather than guess."""
    found = []
    for job in load():
        labels = (job.get("metadata") or {}).get("labels") or {}
        if labels.get("fugaro_profile"):
            found.append(job)
    if len(found) != 1:
        fail("the project has %d shared project jobs" % len(found))
    labels = (found[0].get("metadata") or {}).get("labels") or {}
    print("job=%s" % ((found[0].get("metadata") or {}).get("name") or ""))
    print("kind=project")
    print("profile=%s" % (labels.get("fugaro_profile") or ""))


def cmd_job():
    job = load()
    spec = ((job.get("spec") or {}).get("template") or {}).get("spec") or {}
    containers = spec.get("containers") or [{}]
    container = containers[0]
    env = env_of(container)
    names = sorted(n for n in env if n)
    mounts = sorted({v.get("name") for v in spec.get("volumes") or [] if v.get("name")}
                    | {m.get("name") for m in container.get("volumeMounts") or [] if m.get("name")})
    refs = sorted({(e.get("valueSource") or {}).get("secretKeyRef", {}).get("secret") or ""
                   for e in container.get("env") or []} - {""})
    appkey = "FUGARO_GITHUB_APP_PRIVATE_KEY"
    print("sa=%s" % (spec.get("serviceAccountName") or ""))
    print("appid=%s" % env.get("FUGARO_GITHUB_APP_ID", ""))
    print("env=%s" % " ".join(names))
    print("mounts=%s" % " ".join(mounts))
    print("refs=%s" % " ".join(refs))
    print("appkey=%s" % ("present" if appkey in env else "absent"))
    print("url=%s" % ("present" if env.get("FUGARO_CREDENTIALS_SERVICE_URL") else "absent"))


def cmd_secrets():
    """The App key's container (fugaro_secret=github-app-key) and the bundle
    (fugaro_project and deliberately NO fugaro_secret, credentials.tf). The App
    key's container may be absent -- a release that drops it is one of the two
    readings this row is written for -- while the bundle cannot be, because the
    service that reads it exists (that is what step_service established)."""
    secrets = load()
    key = [s for s in secrets if (s.get("labels") or {}).get("fugaro_secret") == "github-app-key"]
    if len(key) > 1:
        fail("expected at most one container labelled fugaro_secret=github-app-key, found %d" % len(key))
    bundle = [s for s in secrets if (s.get("labels") or {}).get("fugaro_project")
              and not (s.get("labels") or {}).get("fugaro_secret")]
    if len(bundle) != 1:
        fail("expected exactly one container labelled fugaro_project and no fugaro_secret, found %d" % len(bundle))
    print("appkey=%s" % (key[0]["name"].split("/")[-1] if key else ""))
    print("bundle=%s" % bundle[0]["name"].split("/")[-1])


def cmd_policy(jobsa, svcsa):
    doc = load()
    bindings = doc.get("bindings") or []
    roles, members, job_member, cond = [], 0, "no", "no"
    for b in bindings:
        roles.append(b.get("role") or "")
        if b.get("condition"):
            cond = "yes"
        for m in b.get("members") or []:
            members += 1
            if jobsa and m.endswith(":" + jobsa):
                job_member = "yes"
    print("bindings=%d" % len(bindings))
    print("role=%s" % ",".join(sorted(roles)))
    print("members=%d" % members)
    print("job_member=%s" % job_member)
    print("svc_member=%s" % ("yes" if any(
        m.endswith(":" + svcsa)
        for b in bindings for m in (b.get("members") or [])) else "no"))
    print("cond=%s" % cond)


def cmd_switch(path):
    """The switch C2.3's drop reads, out of the checkout beside this script: the
    string literals of ServiceHeldSecretNames. Names, and never a value --
    the list holds secret NAMES."""
    text = open(path, encoding="utf-8").read()
    # The DECLARATION, not a mention: the name is written in the comments above
    # it and below it, and a find() of the first occurrence would read whichever
    # one came first and then the next brace anywhere in the file.
    m = re.search(r"(?m)^[ \t]*(?:var[ \t]+)?ServiceHeldSecretNames[ \t]*=[ \t]*\[\]string\{(.*?)\}",
                  text, re.S)
    if not m:
        fail("no `ServiceHeldSecretNames = []string{...}` declaration in %s" % path)
    line = text[:m.start()].count("\n") + 1
    names = re.findall(r'"([^"]*)"', m.group(1))
    print("line=%d" % line)
    print("held=%s" % ("yes" if names else "no"))
    print("names=%s" % ",".join(names))


def cmd_claims(signer, slug, run, now, fproj, out):
    now = int(now)
    json.dump({"iss": signer, "sub": signer,
               "aud": "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit",
               "iat": now, "exp": now + 600, "uid": "r~%s~%s" % (slug, run),
               "claims": {"fs": slug, "fr": run, "fx": (now + 3600) * 1000,
                          "fp": fproj, "rb": signer}},
              open(out, "w", encoding="utf-8"))


def cmd_uid(slug, run):
    print("r~%s~%s" % (slug, run))


def cmd_identity():
    doc = load()
    tok = doc.get("idToken")
    if not tok:
        fail("the exchange answered no ID token (%s)" % (doc.get("error", {}).get("message") or "no error field"))
    print(tok)


def cmd_mint():
    doc = load()
    tok = doc.get("token") or ""
    if not tok:
        fail("the answer carries no token")
    expires = doc.get("expires_at") or ""
    inside = "unknown"
    if expires:
        import datetime
        try:
            when = datetime.datetime.strptime(expires, "%Y-%m-%dT%H:%M:%SZ").replace(
                tzinfo=datetime.timezone.utc)
            delta = (when - datetime.datetime.now(datetime.timezone.utc)).total_seconds()
            inside = "yes" if 0 < delta <= 3600 else "no"
        except ValueError:
            inside = "unreadable"
    print("repository=%s" % (doc.get("repository") or ""))
    print("expires=%s" % expires)
    print("in_hour=%s" % inside)
    print("token=%s" % tok)


def cmd_refusal():
    doc = load()
    print("error=%s" % (doc.get("error") or ""))
    print("message=%s" % (doc.get("message") or ""))


def cmd_repos():
    doc = load()
    repos = doc.get("repositories") or []
    print("repos=%d" % len(repos))
    if repos:
        print("only=%s" % (repos[0].get("full_name") or ""))


def cmd_audit():
    """One run's audit lines: how many, their statuses, and the first line's
    fields. The raw text is scanned for the token prefix and only the COUNT
    leaves this function."""
    raw = sys.stdin.read()
    lines = json.loads(raw) if raw.strip() else []
    fields = {}
    statuses = []
    for entry in lines:
        payload = entry.get("jsonPayload") or {}
        statuses.append(payload.get("status") or "")
        for k in ("run", "repository", "job", "caller", "credential", "purpose", "tier", "status"):
            fields.setdefault(k, payload.get(k) or "")
    print("lines=%d" % len(lines))
    print("statuses=%s" % ",".join(statuses))
    for k, v in fields.items():
        print("%s=%s" % (k, v))
    print("ghs=%d" % raw.count("ghs_"))


def cmd_count():
    """The entry count of a log read, for the one row that compares two of them.

    It goes through the same parse as every other subcommand rather than a
    `json.loads` of its own: a document that does not parse has to fail here --
    and the caller has to see that failure -- because the count it would
    otherwise print is the empty string, and an empty string is what a
    comparison of two counts reads as equal. Nothing printed at all is no
    entries, which is a count of zero and not a failure."""
    raw = sys.stdin.read()
    entries = [] if not raw.strip() else parse_json(raw)
    if not isinstance(entries, list):
        fail("the log read is not a list of entries")
    print("lines=%d" % len(entries))


def cmd_uids(wanted):
    doc = load()
    users = doc.get("users") or []
    left = 0
    for u in users:
        uid = u.get("localId") or ""
        if any(uid.endswith(w) for w in wanted if w):
            left += 1
    print("left=%d" % left)


def main(argv):
    if not argv:
        fail("no subcommand")
    cmd, rest = argv[0], argv[1:]
    if cmd == "doctor":
        cmd_doctor()
    elif cmd == "budget":
        cmd_budget(rest[0] if rest else "")
    elif cmd == "config":
        cmd_config(rest[0], rest[1] if len(rest) > 1 else "")
    elif cmd == "label":
        cmd_label(rest[0])
    elif cmd == "service":
        cmd_service()
    elif cmd == "svc":
        cmd_svc()
    elif cmd == "jobs":
        cmd_jobs()
    elif cmd == "projectjob":
        cmd_projectjob()
    elif cmd == "job":
        cmd_job()
    elif cmd == "secrets":
        cmd_secrets()
    elif cmd == "policy":
        cmd_policy(rest[0], rest[1])
    elif cmd == "switch":
        cmd_switch(rest[0])
    elif cmd == "claims":
        cmd_claims(*rest)
    elif cmd == "uid":
        cmd_uid(rest[0], rest[1])
    elif cmd == "identity":
        cmd_identity()
    elif cmd == "mint":
        cmd_mint()
    elif cmd == "refusal":
        cmd_refusal()
    elif cmd == "repos":
        cmd_repos()
    elif cmd == "audit":
        cmd_audit()
    elif cmd == "count":
        cmd_count()
    elif cmd == "uids":
        cmd_uids(rest)
    else:
        fail("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}
# The installation this check runs on, and the one path it ever posts to:
# hard-coded, and the guard above refuses a config that describes any other
# project. The mint path is credsvc.MintPath (internal/credsvc/github.go).
P=fugaro-dev
MINT=/v1/github/installation-token
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
