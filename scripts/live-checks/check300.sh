#!/usr/bin/env bash
# Check 300 — the orchestrator subtree on a real installation, as ONE script YOU
# run.
#
# It settles the live half of O2 of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; D1220 and D1241, answered
# 2026-11-04): the one command that puts the subtree's rules and their
# generation marker on a real database is `fugaro update`, and afterwards the
# person's own credential (D1241's answer) writes the state node while a client
# the rules evaluate is refused it. The register's row and section are
# docs/gcp-live-checklist.md, "Check 300".
#
# Usage:      bash scripts/live-checks/check300.sh --installation <name>
#             [--skip-update]
#             <name> is the Fugaro project name the local config in force
#             carries (`fugaro doctor --json`'s project.name), and the answer is
#             held to it: a name that is not the one doctor reports is a STOP
#             before anything is run. --skip-update reads the installation as it
#             stands and runs no command that changes it — the re-run of a check
#             whose update already ran.
# Needs:      bash, mktemp, python3, curl, gcloud and fugaro on PATH. Nothing is
#             installed and nothing is fetched; a missing tool stops the run
#             with one line naming it.
# Where:      YOUR OWN installation, and never `belong`: the name is refused,
#             whether you typed it as the installation or doctor answers it. It
#             runs `fugaro update` (unless --skip-update), which is the whole
#             upgrade and the point of the check, and it writes exactly one
#             node of its own — orchestrator/check300, an id no daemon uses —
#             which its Restore deletes again.
# Costs:      one `fugaro update` run on your installation (Terraform plan and
#             apply, the rules deploy, the images it rebuilds) — the same run
#             you would make anyway — plus a handful of RTDB REST calls, which
#             cost nothing. No model call, no run, no Cloud Build of its own.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the node this check wrote
#             (orchestrator/check300) is deleted and read back as gone, and the
#             scratch directory is removed. The `restore` row says both.
# Output:     progress on stderr, ONE block on stdout headed CHECK 300 RESULT,
#             to copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is
#             missing, the installation did not answer).
#
# The credential is the operator's own: `gcloud auth print-access-token`, read
# once into a variable only long enough to write it into a 0600 curl config file
# in the scratch directory, named by every request with curl's --config and
# never spelled out on a command line — the argv of a running process is
# readable by every user of the machine, and the header would sit there for the
# length of the call. It is never printed, never pasted, and never an argument
# of anything. IAM bypasses the security rules, which is exactly why the
# write below is the person's: D1241's answer is that the person's own
# credential writes this node, and this script's job is to say whether that is
# true of the installation in front of it. What it can NOT do live is mint the
# two tokens the rules are supposed to refuse — a run-scoped job token and a
# custom token from the launcher's `signJwt` minter — because minting either
# needs credentials this check does not hold; that half is pinned by the
# emulator test `TestOrchestratorRefusesEveryTokenWriter`
# (internal/budget/rules/emulator_orchestrator_test.go), and what the live run
# adds is the deployed document the test was written against (the `subtree`
# row) and that a client with no credential at all is refused at the same node
# the person just wrote (the `token-refusal` row). No trace: `bash -x` prints
# the Authorization header this script builds, so guard_xtrace refuses a shell
# that arrived tracing.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the trace prints the
# token this check carries in a header. Check 145 spells the three parts out.
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
USAGE="bash scripts/live-checks/check300.sh --installation <name> [--skip-update]"
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
# all, and the block would then read as "the subtree is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the subtree" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 300 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 300 needs bash"
  need mktemp
  need python3 "the reader that parses the deployed rules document is one python3 file"
  need curl "every read and write below is the RTDB REST API"
  need gcloud "the operator's own access token is what writes the state node"
  need fugaro "doctor names the installation and update is the command this check is about"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk300-mine"
SCRATCH_REMOVED=no
PRINTED=""
# The token is read once, written into a 0600 curl config file inside the
# scratch directory, and never held in a variable longer than it takes to write
# it there. It is not on a command line either: `ps` shows a process's argv to
# every user on the machine for as long as the process lives, and a token there
# is a token already read. Every request names the file with curl's --config,
# the way check91.sh's and check190.sh's requests name theirs.
AUTH_CONF=""
AUTH_READ=no
DB=""
FP=""
INSTALLATION=""
SKIP_UPDATE=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk300.XXXXXXXX") || die "Check 300 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 300 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  restore_node || true
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

# restore_node deletes the one node this check writes and reads it back as
# gone. It runs from the exit trap on every ending, so it uses no `set -e`
# behaviour it cannot survive: every call is guarded, and the outcome is
# recorded in RESTORE_* for the fact finish prints.
RESTORE_STATE=untouched
RESTORE_DETAIL=""
# Set by step_person_write once its PUT answered 200, and read here so the row
# says what this run actually did: the delete below is made either way, because
# the id is this check's own and a leftover from an earlier failed run belongs
# to it too, but a run that stopped before its write did not write this node,
# and a row claiming it did would report a write that never happened.
WROTE=no
restore_node() {
  # Nothing to delete when the token or the database was never read: the run
  # stopped before it could have written anything.
  if [ "$AUTH_READ" = no ] || [ -z "$DB" ]; then
    RESTORE_STATE=untouched
    RESTORE_DETAIL="the run stopped before it read the token and the database, so it wrote nothing and there is nothing to delete"
    return 0
  fi
  local status
  status=$(api DELETE "/$ORCH_ID.json" "$SCRATCH/restore.delete" 2>/dev/null) || status=000
  local back
  back=$(api GET "/$ORCH_ID.json" "$SCRATCH/restore.get" 2>/dev/null) || back=000
  local body=""
  [ -f "$SCRATCH/restore.get" ] && body=$(cat "$SCRATCH/restore.get" 2>/dev/null || true)
  if [ "$status" = 200 ] && [ "$back" = 200 ] && [ "$body" = "null" ]; then
    RESTORE_STATE=gone
    if [ "$WROTE" = yes ]; then
      RESTORE_DETAIL="the node this check wrote (/$ORCH_ID) is deleted and reads back as null"
    else
      RESTORE_DETAIL="this run stopped before it wrote anything and /$ORCH_ID reads back as null — the delete is made either way, because that id is this check's own"
    fi
  else
    RESTORE_STATE=STILL_THERE
    RESTORE_DETAIL="the delete of /$ORCH_ID answered $status and the read-back answered $back with '$body' (want 200 and null): /$ORCH_ID, the node this check writes and the id it owns, is STILL THERE, delete it yourself with curl -X DELETE against https://\$DB/$ORCH_ID.json and your own token"
  fi
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
  # a block that read as a finished run would offer a row of FAILs as a verdict
  # about the subtree when the steps that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local verdict=PASS
  case "$RESTORE_STATE" in
  gone | untouched) ;;
  *) verdict=FAIL ;;
  esac
  # The scratch sentence is composed HERE and not in restore_node: that runs
  # before cleanup, so a sentence composed there would report the directory as
  # present on every run, including the ones whose cleanup then removes it.
  if [ "$SCRATCH_REMOVED" = yes ]; then
    RESTORE_DETAIL="$RESTORE_DETAIL; the scratch directory is gone"
  elif [ -n "$SCRATCH" ]; then
    RESTORE_DETAIL="$RESTORE_DETAIL; the scratch directory is STILL THERE, at $SCRATCH — remove it yourself"
  fi
  # The verdict above is composed after main returned, so it is not in the count
  # main made: a run whose only FAIL is the Restore still has to exit 1, or a
  # node this check left in your database would read as a clean run.
  if [ "$verdict" = FAIL ] && [ "$status" -eq 0 ]; then
    status=1
  fi
  facts="${facts}"$'\n'"restore|$verdict|$RESTORE_DETAIL"
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which prints the Authorization header this check builds — that header is your own access token"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the token it read"
}

# guard_owner: `belong` is the owner's own installation and this check runs
# `fugaro update` and writes a node, so the name is refused here — before doctor
# is asked — and doctor's own answer is refused the same way a moment later,
# because the name you type and the config in force are two things and the
# check has to see the same one twice.
guard_owner() {
  case "$INSTALLATION" in
  belong | belong-*)
    fact installation FAIL "the installation named is $INSTALLATION, which is the owner's own: Check 300 runs fugaro update and writes a node, and this check is for an installation that is not belong"
    stop "Check 300 never runs against 'belong'. Name another installation: $USAGE"
    ;;
  esac
}

parse_args() {
  INSTALLATION=""
  SKIP_UPDATE=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --skip-update)
      SKIP_UPDATE=yes
      shift
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

# ------------------------------------------------------------ the RTDB calls
# api METHOD PATH OUTFILE — one RTDB REST call as the OPERATOR, whose access
# token IAM accepts and which therefore bypasses the security rules: this is the
# person's own credential D1241's answer is about. The path is relative to the
# database root and carries its .json suffix. The status is printed and the body
# lands in OUTFILE; the URL is built from $DB, which step_config read out of the
# project's own config, so no address is remembered here. The credential is the
# one `step_config` wrote into $AUTH_CONF, and it is named rather than spelled
# out: the header is never an argument of this call, so it is never in the argv
# another user can read.
api() {
  local method=$1 path=$2 out=$3
  curl -sS -X "$method" -o "$out" -w '%{http_code}' \
    --config "$AUTH_CONF" \
    ${API_ARGS[@]+"${API_ARGS[@]}"} \
    "https://$DB$path"
}

# anon METHOD PATH OUTFILE — the same call with NO credential at all: a client
# the security rules evaluate, and the only such client this check can be
# without minting a token. It is refused with 401 or 403 Permission denied.
anon() {
  local method=$1 path=$2 out=$3
  curl -sS -X "$method" -o "$out" -w '%{http_code}' \
    ${ANON_ARGS[@]+"${ANON_ARGS[@]}"} \
    "https://$DB$path"
}

# API_ARGS and ANON_ARGS are the body of a write and nothing else: an empty
# array for a read, and `-H Content-Type -d <json>` for a PUT. They are arrays
# because the body is JSON and an unquoted expansion would split it.
API_ARGS=()
ANON_ARGS=()

# The one node this check writes, and its id: `check300` is this check's own and
# no daemon uses it, so the Restore's delete can never take a live
# orchestrator's state with it.
ORCH_ID="orchestrator/check300"

# ------------------------------------------------------------------ the steps
# step_installation asks the local config which installation it is looking at
# and holds it to the name that was typed.
step_installation() {
  if ! fugaro doctor --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err"; then
    sed -n '1,5p' "$SCRATCH/doctor.err" >&4 || true
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local answer name project
  answer=$(python3 "$SCRATCH/doctor_reader.py" <"$SCRATCH/doctor.json") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  name=$(printf '%s\n' "$answer" | sed -n 's/^name=//p')
  project=$(printf '%s\n' "$answer" | sed -n 's/^project=//p')
  [ -n "$name" ] || stop "fugaro doctor --json named no project, so this check cannot tell which installation it is looking at"
  if [ "$name" != "$INSTALLATION" ]; then
    fact installation FAIL "the installation named on the command line is $INSTALLATION and the local config in force answers $name"
    stop "the name you typed and the config in force are two different installations: run with --installation $name, or point FUGARO_PROJECT/FUGARO_CONFIG at the one you meant"
  fi
  case "$name" in
  belong | belong-*)
    fact installation FAIL "the local config in force answers $name, which is the owner's own installation"
    stop "Check 300 never runs against 'belong'"
    ;;
  esac
  fact installation PASS "the local config in force is installation $name on GCP project $project, and it is not belong"
}

# step_config reads the installation's own budget database address out of the
# project's config file rather than from memory: the RTDB URL and the Firebase
# project id are two fields of that file, and a check that remembered them
# would be addressing whatever it remembered.
step_config() {
  local cfgdir=${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects
  local cfg=$cfgdir/$INSTALLATION.yaml
  [ -r "$cfg" ] || stop "no project config at $cfg: this check reads the budget database's address out of the installation's own file (run 'fugaro doctor --project $INSTALLATION' and read its project line for the path)"
  local url
  url=$(sed -n 's|^ *rtdb_url: *||p' "$cfg" | tr -d "\"'" | head -1)
  FP=$(sed -n 's|^ *firebase_project: *||p' "$cfg" | tr -d "\"'" | head -1)
  [ -n "$url" ] || stop "$cfg carries no budget.rtdb_url, so this check has no database to read: the installation's rules live in the Firebase Realtime Database that field names"
  [ -n "$FP" ] || stop "$cfg carries no budget.firebase_project"
  DB=$(printf '%s' "$url" | sed -e 's|^https://||' -e 's|/*$||')
  local tok
  tok=$(gcloud auth print-access-token 2>"$SCRATCH/token.err") || {
    sed -n '1,3p' "$SCRATCH/token.err" >&4 || true
    stop "gcloud auth print-access-token did not answer: this check writes as you (project owner, editor or budget admin), which is D1241's answer — run 'gcloud auth login' and try again"
  }
  [ -n "$tok" ] || stop "gcloud auth print-access-token answered nothing"
  AUTH_CONF="$SCRATCH/auth.conf"
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") \
    || stop "the access token cannot be written into $AUTH_CONF, and this check makes no request carrying it on a command line — where another user could read it out of ps"
  chmod 600 "$AUTH_CONF" || stop "$AUTH_CONF cannot be made mode 0600, and a token file another user can read is a token that has already leaked"
  unset tok
  AUTH_READ=yes
  fact database PASS "the installation's own database is $DB (Firebase project $FP) and your access token was read, written into a 0600 config file no request spells out on its command line, and never printed"
}

# step_update runs the one command the check is about. The plan lines are the
# only thing read out of its output; the token is in no argument of it.
step_update() {
  if [ "$SKIP_UPDATE" = yes ]; then
    fact update INFO "--skip-update: fugaro update was NOT run, so this run reads the installation as it stands and proves nothing about the convergence"
    return 0
  fi
  local status=0
  fugaro update --yes >"$SCRATCH/update.out" 2>"$SCRATCH/update.err" || status=$?
  if [ "$status" -ne 0 ]; then
    owner "fugaro update --yes exited $status; its last lines:"
    tail -n 12 "$SCRATCH/update.out" >&4 || true
    tail -n 12 "$SCRATCH/update.err" >&4 || true
    fact update FAIL "fugaro update --yes exited $status: the one command did not finish on this installation. Its last lines are above; the rows below still read what the database holds"
    return 0
  fi
  local planned=no
  if grep -q 'config/limits/orchestratorRules' "$SCRATCH/update.out"; then planned=yes; fi
  if [ "$planned" = yes ]; then
    fact update PASS "fugaro update --yes exited 0 and its plan named config/limits/orchestratorRules, so this run was the one that converged the subtree's generation"
  else
    fact update PASS "fugaro update --yes exited 0; its plan named no config/limits/orchestratorRules line, which is what a database already on this generation prints (a settled run plans nothing). Whether the generation is there is the marker row's to say"
  fi
}

# step_marker reads the generation marker the update writes after the rules: it
# is the statement about the document underneath it, and the daemon's own gate
# reads the same node before it writes any state.
GEN=1 # rules.OrchestratorRulesVersion (internal/budget/rules/marker.go); a bump rewrites this line and the section that pins it
step_marker() {
  local status body
  status=$(api GET "/config/limits/orchestratorRules.json" "$SCRATCH/marker.json") || status=000
  body=$(cat "$SCRATCH/marker.json" 2>/dev/null || true)
  if [ "$status" != 200 ]; then
    fact marker FAIL "reading config/limits/orchestratorRules as you answered $status, so the deployed generation cannot be told"
    return 0
  fi
  if [ "$body" = "$GEN" ]; then
    fact marker PASS "config/limits/orchestratorRules = $body: the deployed rules are generation $GEN, the one this Fugaro deploys"
  else
    fact marker FAIL "config/limits/orchestratorRules = $body, want $GEN: the subtree's rules are not the generation this binary deploys, so a daemon's own gate would refuse to start on this database (run fugaro update, which is the command that writes both the document and this marker)"
  fi
}

# step_subtree reads the deployed document and asks it the three questions the
# subtree's fence is: is the node there, is its write false for every identity
# the rules evaluate, and does its read carry the device conjunct the person's
# own account needs (D166).
step_subtree() {
  local status
  status=$(api GET "/.settings/rules.json" "$SCRATCH/rules.json") || status=000
  if [ "$status" != 200 ]; then
    fact subtree FAIL "reading the deployed rules as you answered $status, so the document cannot be read"
    return 0
  fi
  cp "$SCRATCH/rules.json" "$SCRATCH/rules.before.json" 2>/dev/null || true
  local answer
  answer=$(python3 "$SCRATCH/rules_reader.py" "$SCRATCH/rules.json") || stop "the deployed rules are not the JSON document this check reads"
  case "$answer" in
  "subtree=present write=false read=user children=0")
    fact subtree PASS "the deployed document carries /orchestrator with an explicit .write of false, a .read carrying the paired phone's mu claim and the device conjunct, and no grant of its own anywhere under it"
    ;;
  *)
    fact subtree FAIL "the deployed document reads: $answer — want subtree=present write=false read=user children=0, which is the fence O2 deploys (write=false for every identity the rules evaluate, read=user for the signed-in person, and no child rule to widen it)"
    ;;
  esac
}

# step_person_write is D1241's answer, live: the person's own credential writes
# the state node and reads it back. It is also the positive control for the
# refusal below — the same URL, method and body, so a refusal there is this
# node's rule and not a malformed request.
step_person_write() {
  API_ARGS=(-H 'Content-Type: application/json' -d '{"schemaVersion":1,"phase":"check300"}')
  local status
  status=$(api PUT "/$ORCH_ID/state.json" "$SCRATCH/write.json") || status=000
  API_ARGS=()
  # Recorded here and not after the read-back: a write that answered 200 wrote
  # the node whatever the read-back then did, and the Restore's row is about
  # what is in the database.
  if [ "$status" = 200 ]; then WROTE=yes; fi
  if [ "$status" != 200 ]; then
    fact person-write FAIL "PUT /$ORCH_ID/state.json as you answered $status (want 200): your own credential cannot write the state node, which is D1241's answer and the half the daemon depends on"
    return 0
  fi
  local back
  back=$(api GET "/$ORCH_ID/state.json" "$SCRATCH/write.back.json") || back=000
  # The comparison is the document, not its bytes: the API is free to store and
  # return the value with other spacing, and a byte comparison would read that
  # as a failed write.
  if [ "$back" = 200 ] && python3 "$SCRATCH/written.py" "$SCRATCH/write.back.json"; then
    fact person-write PASS "PUT /$ORCH_ID/state.json as you answered 200 and the read-back is the document written: the person's own credential writes the state node over IAM, which is D1241's answer"
  else
    fact person-write FAIL "the write answered 200 but the read-back ($back) is '$(cat "$SCRATCH/write.back.json" 2>/dev/null || true)', which is not what was written"
  fi
}

# step_token_refusal makes the same call with no credential at all: the one
# client the rules evaluate that this check can be without minting a token. It
# is a refusal of the node the person just wrote, so it is this subtree's rule
# and not an empty database's — and it is the live half of the fence, whose
# stronger halves (a run-scoped job token, a minter-minted custom token) are the
# emulator test's.
step_token_refusal() {
  local put get
  ANON_ARGS=(-H 'Content-Type: application/json' -d '{"schemaVersion":1,"phase":"check300-anon"}')
  put=$(anon PUT "/$ORCH_ID/state.json" "$SCRATCH/anon.put") || put=000
  ANON_ARGS=()
  get=$(anon GET "/$ORCH_ID/state.json" "$SCRATCH/anon.get") || get=000
  case "$put$get" in
  401401 | 401403 | 403401 | 403403)
    fact token-refusal PASS "a client with no credential at all is refused the write ($put) and the read ($get) of /$ORCH_ID/state, the node your own credential wrote a moment ago — the same URL, method and body, so the refusal is this node's rule"
    ;;
  *)
    fact token-refusal FAIL "the credential-less write answered $put and the read answered $get (want 401 or 403 for both): an identity the rules evaluate is NOT refused this subtree, which is the fence O2 deploys being absent or widened. The run-scoped job token and the minter-minted token are TestOrchestratorRefusesEveryTokenWriter's; this row is the client that needs no minting"
    ;;
  esac
}

# step_untouched says what this check did NOT write: the deployed document is
# byte-for-byte what it was before the writes above, because the only node this
# check writes is its own under /orchestrator/check300.
step_untouched() {
  local status
  status=$(api GET "/.settings/rules.json" "$SCRATCH/rules.after.json") || status=000
  if [ "$status" != 200 ]; then
    fact untouched FAIL "the second read of the deployed rules answered $status, so whether this check changed it cannot be told"
    return 0
  fi
  if cmp -s "$SCRATCH/rules.before.json" "$SCRATCH/rules.after.json"; then
    fact untouched PASS "the deployed document is byte-for-byte what it was before this check wrote: the only node written here is /$ORCH_ID, which the Restore deletes"
  else
    fact untouched FAIL "the deployed document changed while this check ran: something other than this check deployed rules, and the readings above may not describe the same document"
  fi
}

# ------------------------------------------------------------ the fixed files
write_files() {
  # doctor_reader.py: doctor's answer as a fixed vocabulary, never a document.
  cat >"$SCRATCH/doctor_reader.py" <<'PY'
import json, sys
try:
    doc = json.load(sys.stdin)
except Exception:
    sys.exit(1)
p = doc.get("project") or {}
print("name=%s" % (p.get("name") or ""))
print("project=%s" % (p.get("gcp_project") or ""))
PY
  # rules_reader.py: the deployed document as the three answers this check
  # asks, and never the document itself.
  cat >"$SCRATCH/rules_reader.py" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(1)
node = (doc.get("rules") or {}).get("orchestrator")
if not isinstance(node, dict):
    print("subtree=absent")
    sys.exit(0)
w = node.get(".write", "<none>")
r = node.get(".read", "<none>")
if w is False:
    write = "false"
else:
    write = "true" if w is True else "other"
if isinstance(r, str) and "auth.token.mu" in r and "config/admin/devices" in r:
    read = "user"
elif r is False:
    read = "false"
else:
    read = "other"
kids = sum(1 for k, v in node.items()
           if k not in (".read", ".write") and isinstance(v, dict) and (".read" in v or ".write" in v))
print("subtree=present write=%s read=%s children=%d" % (write, read, kids))
PY
  # written.py: whether a read-back is the document this check wrote, with the
  # formatting the API chose ignored.
  cat >"$SCRATCH/written.py" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(1)
sys.exit(0 if doc == {"schemaVersion": 1, "phase": "check300"} else 1)
PY
}

# ------------------------------------------------------------------- the block
BLOCK_KEYS=(installation database update marker subtree person-write token-refusal untouched xtrace restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 300 RESULT\n' >&3
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
  printf 'CHECK 300 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

main() {
  need_tools
  parse_args "$@"

  scratch_up
  write_files
  # After scratch_up, not before: a guard that stops the run records the fact
  # that says why, and a fact written before the facts file exists is dropped.
  guard_owner
  guard_xtrace
  step_installation
  step_config
  step_update
  step_marker
  step_subtree
  step_person_write
  step_token_refusal
  step_untouched

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
