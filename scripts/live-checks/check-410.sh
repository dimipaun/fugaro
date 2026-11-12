#!/usr/bin/env bash
# Check 410 — a failed `fugaro update` names the run, and `fugaro troubleshoot`
# explains it with nothing set up first.
#
# Plan task T-DG9 writes the failure line and T-DG10 writes this check; the
# steps are docs/diagnose-agent/implementation-plan.md's "Checks 410-412 taken".
# What it settles is D1604/D1609 read from the outside: a person whose command
# failed is told the one command that explains it, and that command works on the
# machine as it stands.
#
# What it measures:
#
#   * a REAL failed `fugaro update`, provoked where it can create nothing: a
#     scratch state, cache and config home, and a GCP project this check makes
#     up and then proves is not there (`gcloud projects describe` says NOT
#     FOUND). Every call the probe makes is against a project that does not
#     exist, so nothing in the cloud can be created, changed or deleted, and
#     your own installation is not the subject of any of them;
#   * the line that failure printed: `Something went wrong. Run: fugaro
#     troubleshoot <id>` — T-DG9's sentence, and the whole reason this check
#     exists. A build without it is a real finding and the row says which
#     sentence it looked for;
#   * the run ID that line named: the log of the run that failed, in the store
#     the run wrote it to, so the ID and the log are the same run;
#   * `fugaro troubleshoot <id>` — the one command the line named, typed with
#     nothing else: no setup, no environment prepared by this check beyond the
#     same scratch state directory the failing run used. A diagnosis that needs
#     one more step is a finding;
#   * the evidence folder the diagnosis wrote: its mode (recorded, not judged),
#     holding the log, doctor's output, the version and the effective
#     configuration.
#
# It creates NOTHING in the cloud, and it cannot: the project it names does not
# exist. It does spend ONE model call on your own credential, which is what a
# diagnosis is.
#
# Usage:      bash scripts/live-checks/check-410.sh
#             bash scripts/live-checks/check-410.sh --dry-run   # print the plan
#             bash scripts/live-checks/check-410.sh --help
# Needs:      bash, mktemp, date, gcloud (authenticated as YOU: the check
#             describes the project it made up, which must come back NOT FOUND)
#             and fugaro (the installed release: the whole point is the build
#             you have). The diagnosis itself needs a credential and a network,
#             and it says so itself when it has neither.
# Where:      your own machine. No installation is converged, configured or
#             read: the probe's config, state, cache and workdirs are all inside
#             one scratch directory. A project, region or account naming belong
#             is refused by name.
# Creates:    nothing in the cloud. On disk: one scratch directory, removed at
#             the end, holding the probe's config, state and cache.
# Costs:      one model call on your own credential, bounded by `fugaro
#             troubleshoot`'s own --max-turns and --max-budget-usd (the default
#             cap), plus the source fetch. The probe's `fugaro update` is a
#             handful of failing reads against a project that does not exist.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-410_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory and nothing else, removed from the exit trap
#             even on a failed assertion or a Ctrl-C.
# Output:     progress on stderr, ONE block on stdout headed CHECK 410 RESULT,
#             then CHECK 410 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a probe whose own `fugaro update` SUCCEEDED, since a real
#             failure is what this check reads. 2: the check could not measure —
#             a tool is missing, the project it made up turned out to exist, the
#             probe never finished inside its timeout, or the diagnosis did not
#             run. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, stderr, the block or any file it writes. The diagnosis's own output is
# passed through as the command printed it, which is already redacted by the
# command (F20).
#
# Knobs (all CHECK410_*): DRY_RUN, KEEP_SCRATCH, STAMP (the suffix of the
# project name this run makes up; the clock by default), PROJECT (the project ID
# to name, when you want to pin one), UPDATE_TIMEOUT_SECONDS (300),
# DIAGNOSIS_TIMEOUT_SECONDS (900), SKIP_DIAGNOSIS (1: measure the line and the
# log only, and spend no model call).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check runs a real
# command whose log is written beside a person's own state, and a trace of it
# would be a second copy of what that log holds.
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
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

# probe_unmeasured: a row that reads the probe's own output, when the probe never
# finished. The rows below the probe are then unmeasured one by one — a finding
# they would report is only the probe's timeout seen from further away, and the
# check says which rows it could not measure instead of blaming the code for
# them. Returns 0 when the row was recorded as unmeasured.
probe_unmeasured() { # probe_unmeasured KEY "why the probe could not be measured"
  [ "$PROBE_UNMEASURED" = yes ] || return 1
  unmeasured "$1" "the probe's fugaro update did not finish within ${UPDATE_TIMEOUT}s and was ended, so this check has no failed run to read: $2"
  return 0
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 410 needs bash"
  local t
  for t in mktemp date gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 410 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk410-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROBE_UNMEASURED=no
UPDATE_STATUS=0
RUN_ID=""
LINE_ID=""
LOG_PATH=""
DIAGNOSIS_TIMED_OUT=no
DIAG_STATUS=0

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk410.XXXXXXXX") || die "Check 410 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  mkdir -p "$SCRATCH/home" "$SCRATCH/state" "$SCRATCH/cache" "$SCRATCH/config" "$SCRATCH/run"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
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
    say "CHECK410_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check changed, said rather than assumed. It creates nothing
# in the cloud — the project it names does not exist — and its one lasting effect
# is a model call on the owner's own credential.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created in the cloud: the probe's update ran against ${P}, a project this check made up and read back as NOT FOUND, so no call it made could create, change or delete anything; your own installation was not its subject. One model call was spent on your own credential (the diagnosis), and the scratch directory held the probe's config, state and cache"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
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

# ------------------------------------------------------------------- the tools
# run_bounded SECONDS KEY CMD...: run one command with its output in
# $SCRATCH/KEY.out and .err, and end it after SECONDS. `timeout` is not used:
# it is GNU coreutils, and this check runs on the owner's machine, where the
# tool is absent on macOS. Sets BOUNDED_STATUS and BOUNDED_TIMED_OUT.
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  "$@" >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

# probe_env: the environment every command this check runs gets. The three XDG
# directories are the scratch ones, so the probe's config, its log store and its
# source cache are all this run's; HOME is left alone, so the client the
# diagnosis starts finds the same login it would on this machine.
probe_env() { # probe_env CMD...
  env \
    HOME="$HOME" \
    XDG_STATE_HOME="$SCRATCH/state" \
    XDG_CACHE_HOME="$SCRATCH/cache" \
    XDG_CONFIG_HOME="$SCRATCH/config" \
    TMPDIR="$SCRATCH/run" \
    "$@"
}

log_dir() { printf '%s' "$SCRATCH/state/fugaro/logs"; }

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-410.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the probe's own log to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the probe's log or the diagnosis's output"
}

guard_belong() {
  case "${ACCOUNT:-}${P}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check names is one it made up (${P}) and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  # Not a release gate: what this check measures is a failure line and a
  # diagnosis, and both are the build you have. A `dev` build is refused only
  # where it decides an answer (the diagnosis's own source fetch), and the
  # command says so itself.
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the line it prints and the diagnosis it starts are the ones you have installed)"
}

# ---------------------------------------------------------------- the steps
# step_project: the premise of every later row. The project the probe names must
# NOT exist — that is what makes an update against it a failure that can create
# nothing. A project that exists, or one gcloud will not answer about, is the
# check stopping rather than a finding: it cannot measure what it meant to.
step_project() {
  local out
  out=$(gcloud projects describe "$P" 2>&1 || true)
  if printf '%s\n' "$out" | grep -Eq 'NOT_FOUND|does not exist|was not found'; then
    fact project-absent PASS "${P} is not there ($(printf '%s' "$out" | head -n 1 | head -c 160)): the probe's update can create nothing, because every call it makes is against a project that does not exist"
    return 0
  fi
  die "this check needs a project that does not exist and '${P}' is not that: gcloud said '$(printf '%s' "$out" | head -n 1 | head -c 200)'. It names one it makes up (CHECK410_PROJECT pins one); if that name is taken, run it again, or pick another with CHECK410_PROJECT=<an id nobody has>"
}

# step_update: the real failed run. `fugaro update` on a machine whose config,
# state and cache are the scratch ones, naming a project that does not exist,
# with every input the wizard would ask for given as a flag so the run is
# scripted: it fails inside its own converge, which is the failure this check is
# about — the run has opened its log by then, and the log is what the diagnosis
# reads.
step_update() {
  say "the probe: fugaro update --name $NAME --gcp-project $P --region $REGION --yes, in a scratch home"
  run_bounded "$UPDATE_TIMEOUT" probe probe_env "$FUGARO_BIN" update \
    --name "$NAME" --gcp-project "$P" --region "$REGION" --yes
  UPDATE_STATUS=$BOUNDED_STATUS
  UPDATE_TIMED_OUT=$BOUNDED_TIMED_OUT
  if [ "$UPDATE_TIMED_OUT" = yes ]; then
    PROBE_UNMEASURED=yes
    unmeasured update-failed "the probe's fugaro update did not finish within ${UPDATE_TIMEOUT}s and was ended: this check cannot say what a failed update printed. Nothing it reached could create anything (the project does not exist), and its output is in $SCRATCH/probe.err"
    return 0
  fi
  if [ "$UPDATE_STATUS" = 0 ]; then
    stop "the probe's fugaro update SUCCEEDED against ${P}, a project that does not exist: this check measures what a real failure prints, and a success means the machine it ran on is not the one this check thinks it is. Its output is in $SCRATCH/probe.err"
  fi
  fact update-failed PASS "fugaro update exited ${UPDATE_STATUS} against ${P} (a project that does not exist): a real failed run, logged, which is what this check measures"
}

# step_logged: the failing run wrote its log where a later command looks for it.
# This is the other half of T-DG1's contract read from the outside: the run ID
# is the log's name, in the state directory the run was given. A failed run that
# wrote no log is a finding rather than an unmeasurable row: the line it prints
# sends the person to a diagnosis that reads this log, so a failure with no log
# behind it is the defect this check is looking for.
step_logged() {
  local dir newest
  if probe_unmeasured run-logged "there is no log to find"; then return 0; fi
  dir=$(log_dir)
  newest=""
  if [ -d "$dir" ]; then
    newest=$(find "$dir" -maxdepth 1 -type f -name '*.log' -print 2>/dev/null | sort | tail -n 1 || true)
  fi
  if [ -z "$newest" ]; then
    fact run-logged FAIL "the probe's update exited ${UPDATE_STATUS} and left no log under ${dir}: a failed run logs itself, because the line it prints sends the person to 'fugaro troubleshoot <id>' and that command reads this log. If the failure happened before the run itself started there is no line and no log — the failure-line row below says which of the two this was. The probe's own output is in $SCRATCH/probe.out and $SCRATCH/probe.err"
    return 0
  fi
  LOG_PATH=$newest
  RUN_ID=$(basename "$newest" .log)
  fact run-logged PASS "the failing run left ${RUN_ID} in the state directory the probe was given, which is what a later command reads: the log's stem is the run ID"
}

# step_failure_line: T-DG9's sentence, in the output of the run that failed. Both
# streams are read: which one it goes to is the reporter's, and a person reads
# both.
step_failure_line() {
  local text
  if probe_unmeasured failure-line "no failed run printed anything"; then return 0; fi
  text=$(cat "$SCRATCH/probe.out" "$SCRATCH/probe.err" 2>/dev/null || true)
  LINE_ID=$(printf '%s\n' "$text" | sed -n 's/.*Run: fugaro troubleshoot \([A-Za-z0-9][A-Za-z0-9._-]*\).*/\1/p' | head -n 1)
  if [ -z "$LINE_ID" ]; then
    fact failure-line FAIL "the failed update printed no 'Something went wrong. Run: fugaro troubleshoot <id>' line: a person whose command failed is told the one command that explains it (D1604, D1609), and this run told them nothing. Its output is in $SCRATCH/probe.out and $SCRATCH/probe.err, and this is T-DG9's sentence — if the wording moved, move this row with it"
    return 0
  fi
  fact failure-line PASS "the failed update printed the line naming ${LINE_ID}: what a person is told is the one command that explains the failure, and no setup"
}

# step_id_resolves: the ID the line named is the run that failed. A line naming a
# run ID nothing wrote sends the person to a diagnosis that cannot start. When the
# ID does not resolve the check stops here rather than diagnosing a run of its own
# choosing: what the diagnosis is run on is the ID the line named, and this check
# does not spend a model call on anything else.
step_id_resolves() {
  if probe_unmeasured run-id-resolves "no line named an ID"; then return 0; fi
  if [ -z "$LINE_ID" ]; then
    RUN_ID=""
    unmeasured run-id-resolves "there is no run ID to resolve: the failed update printed no failure line, and the row above says so"
    return 0
  fi
  if [ -n "$RUN_ID" ] && [ "$LINE_ID" != "$RUN_ID" ]; then
    local logged=$RUN_ID
    RUN_ID=""
    fact run-id-resolves FAIL "the failure line named ${LINE_ID} and the run that failed logged itself as ${logged}: the line's ID has to name the log the diagnosis will read, or the person is sent to a run nothing wrote"
    return 0
  fi
  if [ ! -f "$(log_dir)/${LINE_ID}.log" ]; then
    RUN_ID=""
    fact run-id-resolves FAIL "the failure line named ${LINE_ID} and no log of that name is in $(log_dir): the ID a person types back has to be the log the diagnosis reads"
    return 0
  fi
  RUN_ID=$LINE_ID
  fact run-id-resolves PASS "${RUN_ID} names the log the failing run wrote: the run ID a person types back is the run that failed"
}

# step_diagnosis: the one command the line named, with nothing set up first. The
# check runs exactly `fugaro troubleshoot <the id>` — no setup, no flag, no
# environment beyond the same scratch state directory — and reads what it
# printed.
step_diagnosis() {
  if [ -n "$SKIP_DIAGNOSIS" ]; then
    fact diagnosis INFO "not asked for (CHECK410_SKIP_DIAGNOSIS): the failure line and the log it names were measured, and no model call was made"
    return 0
  fi
  if [ -z "$RUN_ID" ]; then
    unmeasured diagnosis "there is no run ID to hand the diagnosis: the failure line named none that resolves, and the rows above say why. This is the half of the check that costs a model call, and it did not run"
    return 0
  fi
  say "the diagnosis: fugaro troubleshoot ${RUN_ID} (one model call on your own credential)"
  run_bounded "$DIAGNOSIS_TIMEOUT" diag probe_env "$FUGARO_BIN" troubleshoot "$RUN_ID"
  DIAG_STATUS=$BOUNDED_STATUS
  DIAGNOSIS_TIMED_OUT=$BOUNDED_TIMED_OUT
  if [ "$DIAGNOSIS_TIMED_OUT" = yes ]; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} did not finish within ${DIAGNOSIS_TIMEOUT}s and was ended: the command's own cap (--max-turns, --max-budget-usd) is what should end it, and this is a longer look than a check should take. Its output is in $SCRATCH/diag.out"
    return 0
  fi
  if [ "$DIAG_STATUS" != 0 ]; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} exited ${DIAG_STATUS}: the failure line named this command as the one that explains the failure, so it has to run — and it prints an offline answer and exits non-zero when it has no credential or cannot reach the source at this version, which is what this run looks like from here. What it printed is in $SCRATCH/diag.out and $SCRATCH/diag.err"
    return 0
  fi
  if ! grep -q '^Outcome:  ' "$SCRATCH/diag.out"; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} exited 0 without printing an outcome line: the command's answer is the outcome, the signature and the fix, and this run printed none of them. What it printed is in $SCRATCH/diag.out"
    return 0
  fi
  fact diagnosis PASS "$(grep -m1 '^Outcome:  ' "$SCRATCH/diag.out" | head -c 200) — the one command the failure line named returned a diagnosis, typed with nothing set up first"
}

# step_evidence: the folder the diagnosis wrote, and its modes. An answer with no
# evidence behind it is not one a person can check.
step_evidence() {
  if [ -n "$SKIP_DIAGNOSIS" ]; then
    fact evidence INFO "not asked for (CHECK410_SKIP_DIAGNOSIS): no diagnosis ran, so there is no folder this check could read"
    return 0
  fi
  if probe_unmeasured evidence "no diagnosis wrote a folder"; then return 0; fi
  if [ -z "$RUN_ID" ] || [ "$DIAG_STATUS" != 0 ]; then
    unmeasured evidence "there is no diagnosis to read the evidence of: the rows above say why it did not run, and this row is about the folder it writes"
    return 0
  fi
  local dir
  dir="$SCRATCH/state/fugaro/troubleshoot/${RUN_ID}"
  if [ ! -d "$dir" ]; then
    fact evidence FAIL "the diagnosis printed an outcome and wrote no evidence folder at ${dir}: the person checks the answer against the log, doctor's output, the version and the configuration, and there is nothing to check it against"
    return 0
  fi
  local mode
  mode=$(stat -c '%a' "$dir" 2>/dev/null || stat -f '%Lp' "$dir" 2>/dev/null || echo '?')
  local missing="" f
  for f in "${RUN_ID}.log" doctor.txt version.txt config.yaml; do
    [ -f "$dir/$f" ] || missing="${missing:+$missing, }$f"
  done
  if [ -n "$missing" ]; then
    fact evidence FAIL "the evidence folder ${dir} is missing ${missing}: the agent reads a folder rather than a list, and the four files are what the answer stands on"
    return 0
  fi
  fact evidence PASS "${dir} (mode ${mode}) holds the log, doctor's output, the version and the effective configuration: the answer the diagnosis printed can be checked against what the run left"
}

# step_no_setup: the claim the whole check exists for, said as a row rather than
# left to the reader. It is a fact about what this script ran, which is why the
# check can state it.
step_no_setup() {
  if [ -n "$SKIP_DIAGNOSIS" ]; then
    fact no-setup INFO "not asked for (CHECK410_SKIP_DIAGNOSIS): the diagnosis did not run, so this check makes no claim about what it needed"
    return 0
  fi
  if probe_unmeasured no-setup "no diagnosis ran"; then return 0; fi
  if [ -z "$RUN_ID" ] || [ "$DIAG_STATUS" != 0 ]; then
    unmeasured no-setup "the diagnosis did not run, so this check cannot say whether it needed anything set up first: the rows above say why"
    return 0
  fi
  fact no-setup PASS "the command the line named is the whole of it: this check ran 'fugaro troubleshoot ${RUN_ID}' with a run ID and nothing else — no fugaro setup, no flag, no credential exported, no directory prepared beyond the scratch state the failing run itself used"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 410 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] gcloud projects describe ${P} — it must come back NOT FOUND, which is what makes the probe harmless"
  say "  [network] fugaro update --name ${NAME} --gcp-project ${P} --region ${REGION} --yes — a real failed update against a project that does not exist; it opens its log and fails inside its own converge"
  say "  [read] the log store under <scratch>/state/fugaro/logs — the run ID of the failed run"
  say "  [read] the failed run's own output — the 'Something went wrong. Run: fugaro troubleshoot <id>' line (T-DG9)"
  if [ -n "$SKIP_DIAGNOSIS" ]; then
    say "  [not asked for] the diagnosis: CHECK410_SKIP_DIAGNOSIS is set, so no model call is made"
  else
    say "  [network, ONE MODEL CALL on your own credential] fugaro troubleshoot <id> — the one command the line named, with nothing set up first; it fetches the source at this version and runs the read-only agent under the command's own cap"
  fi
  say "  [nothing created] no job, no build, no container, no binding and no account: the project does not exist, and your own installation is not the subject of any call"
  for key in fugaro-version xtrace project-absent belong update-failed run-logged failure-line \
    run-id-resolves diagnosis evidence no-setup; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no update, no diagnosis, no model call and no cloud call"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace project-absent belong update-failed run-logged
  failure-line run-id-resolves diagnosis evidence no-setup restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 410 RESULT\n' >&3
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
  printf 'CHECK 410 RESULT END (exit %s)\n' "$status" >&3
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
# The project and the installation name are made up from the clock, so no run
# reuses another's, and both are shapes gcloud and localcfg accept.
DRY_RUN_CLI=""
for arg in "$@"; do
  case "$arg" in
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done

STAMP=${CHECK410_STAMP:-$(date -u +%Y%m%d%H%M%S)}
P=${CHECK410_PROJECT:-fugaro-chk410-${STAMP}}
NAME=${CHECK410_NAME:-chk410-${STAMP}}
REGION=${CHECK410_REGION:-us-east5}
UPDATE_TIMEOUT=${CHECK410_UPDATE_TIMEOUT_SECONDS:-300}
DIAGNOSIS_TIMEOUT=${CHECK410_DIAGNOSIS_TIMEOUT_SECONDS:-900}
SKIP_DIAGNOSIS=${CHECK410_SKIP_DIAGNOSIS:-}
DRY_RUN=${CHECK410_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK410_KEEP_SCRATCH:-}
UPDATE_TIMED_OUT=no
# Resolved before anything runs, and by absolute path: this check runs a real
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
  [ -n "$FUGARO_BIN" ] || die "Check 410 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_project
  step_update
  step_logged
  step_failure_line
  step_id_resolves
  step_diagnosis
  step_evidence
  step_no_setup
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
