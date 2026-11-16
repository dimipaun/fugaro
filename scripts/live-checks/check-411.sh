#!/usr/bin/env bash
# Check 411 — a machine with no git on PATH and no GitHub account still gets the
# source of the version it is running.
#
# Plan task T-DG4 writes the two transports and T-DG10 writes this check; the
# steps are docs/diagnose-agent/implementation-plan.md's "Checks 410-412 taken".
# What it settles is D1611 read from the outside: a machine without git is not a
# machine without a diagnosis — the source comes down as the archive of the
# version installed, and what the cache records is that version rather than
# whatever the newest release happens to be.
#
# What it measures:
#
#   * a REAL failed `fugaro update`, provoked the way Check 410 provokes it and
#     creating nothing: a scratch state, cache and config home, and a GCP project
#     this check makes up and then proves is not there. The diagnosis needs a run
#     to read, and this is the cheapest one that cannot create anything;
#   * the child's PATH: the one you have with GIT REMOVED — every other tool in
#     your PATH directories is symlinked into one scratch directory, and no `git`
#     is; the check then asks that PATH for git itself and reports what it said;
#   * no GitHub account: GH_TOKEN, GITHUB_TOKEN, GH_CONFIG_DIR and the enterprise
#     spelling are unset for the child, and the source comes from the public
#     archive URL, which needs no account at all;
#   * `fugaro troubleshoot <id>` with that PATH and a scratch source cache — a
#     cache that holds nothing, so the tree has to be fetched now;
#   * the record the fetch left: `kind` is `tarball`, `version` is the version you
#     are running (the check refuses a build it cannot name a release for, D1611's
#     own case), `may_not_match` is false, and the tree is on disk holding the
#     code.
#
# It creates NOTHING in the cloud, and it cannot: the project it names does not
# exist. It does spend ONE model call on your own credential, which is what a
# diagnosis is.
#
# Usage:      bash scripts/live-checks/check-411.sh
#             bash scripts/live-checks/check-411.sh --dry-run   # print the plan
#             bash scripts/live-checks/check-411.sh --help
# Needs:      bash, mktemp, date, ln, python3 (the reader for the cache's record),
#             gcloud (authenticated as YOU: the check describes the project it
#             made up, which must come back NOT FOUND) and fugaro (the installed
#             release, and the version the fetched source has to be of). The
#             diagnosis itself needs a credential and a network, and it says so
#             itself when it has neither.
# Where:      your own machine. No installation is converged, configured or read:
#             the probe's config, state, cache and workdirs are all inside one
#             scratch directory, and the source cache it fills is a scratch one
#             too, so the tree this check reads is the one it just fetched. A
#             project, region or account naming belong is refused by name.
# Creates:    nothing in the cloud. On disk: one scratch directory, removed at the
#             end, holding the probe's config, state, cache, the git-less PATH and
#             the source tree it fetched.
# Costs:      one model call on your own credential, bounded by `fugaro
#             troubleshoot`'s own --max-turns and --max-budget-usd (the default
#             cap), plus one tarball download and the probe's handful of failing
#             reads against a project that does not exist.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-411_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory and nothing else, removed from the exit trap
#             even on a failed assertion or a Ctrl-C.
# Output:     progress on stderr, ONE block on stdout headed CHECK 411 RESULT,
#             then CHECK 411 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a probe whose own `fugaro update` SUCCEEDED, since a real
#             failure is what this check reads. 2: the check could not measure —
#             a tool is missing, the project it made up turned out to exist, the
#             build is one this check cannot name a release for, a git it did not
#             see is reachable with the PATH it built, or the diagnosis did not
#             run. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, stderr, the block or any file it writes. The diagnosis's own output is
# passed through as the command printed it, which is already redacted by the
# command (F20).
#
# Knobs (all CHECK411_*): DRY_RUN, KEEP_SCRATCH, STAMP (the suffix of the project
# name this run makes up; the clock by default), PROJECT (the project ID to name,
# when you want to pin one), UPDATE_TIMEOUT_SECONDS (300),
# DIAGNOSIS_TIMEOUT_SECONDS (900).
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

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 411 needs bash"
  local t
  for t in mktemp date ln python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 411 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk411-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
UPDATE_STATUS=0
RUN_ID=""
NOPATH=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk411.XXXXXXXX") || die "Check 411 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  mkdir -p "$SCRATCH/home" "$SCRATCH/state" "$SCRATCH/cache" "$SCRATCH/config" "$SCRATCH/run" "$SCRATCH/path"
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
    say "CHECK411_KEEP_SCRATCH is set: $SCRATCH is kept"
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
  RESTORE_NOTE="nothing was created in the cloud: the probe's update ran against ${P}, a project this check made up and read back as NOT FOUND, so no call it made could create, change or delete anything; your own installation was not its subject, and the source cache it filled was a scratch one. One model call was spent on your own credential (the diagnosis)"
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

# probe_env: the environment the probe gets. The three XDG directories are the
# scratch ones, so the probe's config, its log store and its source cache are all
# this run's; HOME is left alone, so the client the diagnosis starts finds the
# same login it would on this machine.
probe_env() { # probe_env CMD...
  env \
    HOME="$HOME" \
    XDG_STATE_HOME="$SCRATCH/state" \
    XDG_CACHE_HOME="$SCRATCH/cache" \
    XDG_CONFIG_HOME="$SCRATCH/config" \
    TMPDIR="$SCRATCH/run" \
    "$@"
}

# diag_env: the environment the diagnosis gets, which is probe_env with the two
# differences this check is about — the git-less PATH, and no GitHub account.
# `env -u` is used rather than an unset in this shell: the variables stay where
# they were for every other command this check runs.
diag_env() { # diag_env CMD...
  env -u GH_TOKEN -u GITHUB_TOKEN -u GH_CONFIG_DIR -u GITHUB_ENTERPRISE_TOKEN \
    HOME="$HOME" \
    XDG_STATE_HOME="$SCRATCH/state" \
    XDG_CACHE_HOME="$SCRATCH/cache" \
    XDG_CONFIG_HOME="$SCRATCH/config" \
    TMPDIR="$SCRATCH/run" \
    PATH="$NOPATH" \
    "$@"
}

log_dir() { printf '%s' "$SCRATCH/state/fugaro/logs"; }

# record_file is what the source fetch writes about the tree it left: the version
# it is of, the transport it came by, and whether the two could be tied together
# at all (D1611).
record_file() { printf '%s' "$SCRATCH/cache/fugaro/source/record.json"; }
tree_dir() { printf '%s' "$SCRATCH/cache/fugaro/source/tree"; }

json_field() { # json_field FILE KEY
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2" 2>/dev/null || true
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-411.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the probe's own log to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the probe's log or the diagnosis's output"
}

guard_belong() {
  case "${ACCOUNT:-}${P}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check names is one it made up (${P}) and the account it runs as is yours; nothing here names belong"
}

# guard_release: the version the source has to be of. A build that names no
# release — a development build, or one this check cannot read a release out of —
# is D1611's own case rather than a failure of the fetch: the tree then comes from
# the newest release tag and says so, which is not what this check measures. The
# row is unmeasured and the check does not run the diagnosis: a person on such a
# build learns it from the sentence below, which is the one the command prints.
guard_release() {
  V=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$V" in
  '' | dev | unknown)
    unmeasured release-build "fugaro version printed '${V:-nothing}' and this check needs a release to fetch the source of: a ${V:-unknown} build of fugaro with no release tag takes the newest release instead, so there is no version of its own for the record to match (D1611, and its own sentence is 'this is a ${V:-dev} build of fugaro with no release tag, so the source tree is the newest release ... and may not be the code that failed'). Run this check against an installed release"
    return 0
    ;;
  esac
  RELEASE=${V#v}
  case "$RELEASE" in
  [0-9]*.[0-9]*.[0-9]* | [0-9]*.[0-9]*.[0-9]*-*) ;;
  *)
    unmeasured release-build "fugaro version printed '${V}', which this check cannot read as a release (vX.Y.Z): the source a machine without git fetches is named by the version, and this check cannot say which one that is. Run it against an installed release"
    RELEASE=""
    return 0
    ;;
  esac
  fact release-build PASS "the build is release ${V}, so the source it fetches is the source of a version this check can name: the record has to hold that version"
}

# ---------------------------------------------------------------- the steps
# step_project: the premise of the probe. The project it names must NOT exist —
# that is what makes an update against it fail while creating nothing.
step_project() {
  local out
  out=$(gcloud projects describe "$P" 2>&1 || true)
  if printf '%s\n' "$out" | grep -Eq 'NOT_FOUND|does not exist|was not found'; then
    fact project-absent PASS "${P} is not there ($(printf '%s' "$out" | head -n 1 | head -c 160)): the probe's update can create nothing, because every call it makes is against a project that does not exist"
    return 0
  fi
  die "this check needs a project that does not exist and '${P}' is not that: gcloud said '$(printf '%s' "$out" | head -n 1 | head -c 200)'. It names one it makes up (CHECK411_PROJECT pins one); if that name is taken, run it again, or pick another with CHECK411_PROJECT=<an id nobody has>"
}

# step_nogit: the PATH the diagnosis gets, built rather than assumed. Every name
# of every directory in your PATH is symlinked into one scratch directory except
# `git`, from the first directory that holds it, so the child has the tools you
# have — resolved as your own shell resolves them — and cannot find git at all,
# which is the whole premise of this check, and why it is built instead of hoped
# for.
step_nogit() {
  local dirs=() d entry name linked=0
  NOPATH="$SCRATCH/path"
  local IFS=:
  read -r -a dirs <<<"$PATH"
  for d in "${dirs[@]}"; do
    [ -n "$d" ] && [ -d "$d" ] || continue
    for entry in "$d"/*; do
      [ -f "$entry" ] || [ -L "$entry" ] || continue
      name=${entry##*/}
      [ "$name" = git ] && continue
      # The FIRST directory of your PATH that holds a name is the one that
      # resolves, so the mirror keeps that one and skips the name after it: a
      # later link written over it (ln -sfn) would hand the diagnosis a tool your
      # own shell never runs — a second claude, a second node — and the no-git
      # row's whole claim is that this PATH is yours minus git.
      if [ -e "$NOPATH/$name" ] || [ -L "$NOPATH/$name" ]; then continue; fi
      if ln -sn "$entry" "$NOPATH/$name" 2>/dev/null; then linked=$((linked + 1)); fi
    done
  done
  if [ -x "$NOPATH/fugaro" ]; then
    fact fugaro-in-path PASS "the git-less PATH holds a fugaro ($NOPATH/fugaro), so the check runs the installed build with it rather than falling back to this script's own PATH"
  else
    die "the git-less PATH this check built holds no fugaro: every directory of your PATH was mirrored into $NOPATH with git left out, and fugaro is not in any of them, so the diagnosis could not run against the installed build. Run the check from a shell where 'fugaro' is on PATH"
  fi
  run_bounded 30 nogit env PATH="$NOPATH" sh -c 'command -v git || true'
  local found
  found=$(head -n 1 "$SCRATCH/nogit.out" 2>/dev/null || true)
  if [ -n "$found" ]; then
    die "git is still reachable with the PATH this check built ('$found' came back from 'command -v git' with PATH=${NOPATH}), so the diagnosis would take the git transport and this check would measure the other one. Nothing else in your PATH moved — ${linked} tools were symlinked and git alone was left out — so this is a git this check did not see, and it stops rather than reporting on a machine it did not build"
  fi
  fact no-git PASS "PATH=${NOPATH} holds ${linked} tools mirrored from your own PATH — every name from the first directory of it that holds one, so the child resolves tools as your own shell does — and no git at all: 'command -v git' answers nothing with it, so the source fetch has to take the tarball (a machine without git, D1611)"
}

# step_noaccount: the other half of the premise, said as what the check did. The
# source comes from the public archive URL, which is anonymous; the token
# variables are removed for the child so that a fetch that succeeded cannot have
# used one.
step_noaccount() {
  fact no-account PASS "GH_TOKEN, GITHUB_TOKEN, GH_CONFIG_DIR and GITHUB_ENTERPRISE_TOKEN are unset for the diagnosis (env -u), and the source it fetches is the public archive of the Fugaro repository, which needs no account: a tree fetched under this environment was fetched anonymously"
}

# step_update: the real failed run, with the normal PATH — git has nothing to do
# with an update failing against a project that does not exist, and the premise
# this check is about is the diagnosis's, not the probe's.
step_update() {
  say "the probe: fugaro update --name $NAME --gcp-project $P --region $REGION --yes, in a scratch home"
  run_bounded "$UPDATE_TIMEOUT" probe probe_env "$FUGARO_BIN" update \
    --name "$NAME" --gcp-project "$P" --region "$REGION" --yes
  UPDATE_STATUS=$BOUNDED_STATUS
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured update-failed "the probe's fugaro update did not finish within ${UPDATE_TIMEOUT}s and was ended: this check cannot say what a failed update printed. Nothing it reached could create anything (the project does not exist), and its output is in $SCRATCH/probe.err"
    return 0
  fi
  if [ "$UPDATE_STATUS" = 0 ]; then
    stop "the probe's fugaro update SUCCEEDED against ${P}, a project that does not exist: this check needs a real failed run for the diagnosis to read, and a success means the machine it ran on is not the one this check thinks it is. Its output is in $SCRATCH/probe.err"
  fi
  fact update-failed PASS "fugaro update exited ${UPDATE_STATUS} against ${P} (a project that does not exist): a real failed run, logged, which is the run the diagnosis reads"
}

step_logged() {
  local dir newest
  dir=$(log_dir)
  newest=""
  if [ -d "$dir" ]; then
    newest=$(find "$dir" -maxdepth 1 -type f -name '*.log' -print 2>/dev/null | sort | tail -n 1 || true)
  fi
  if [ -z "$newest" ]; then
    unmeasured run-logged "the probe's update exited ${UPDATE_STATUS} and left no log under ${dir}: there is no run ID to hand the diagnosis, and this check measures what the diagnosis does. Check 410's own row says the same thing about the same run"
    return 0
  fi
  RUN_ID=$(basename "$newest" .log)
  fact run-logged PASS "the failing run left ${RUN_ID} in the state directory the probe was given: the run ID this check hands the diagnosis"
}

# step_diagnosis: the one command, on the machine this check built. The source
# fetch runs before the diagnosis decides anything about credentials — the
# offline path fetches too — so the rows below are measured even from a run that
# ends without an answer.
step_diagnosis() {
  say "the diagnosis: PATH without git, env -u GH_TOKEN ..., fugaro troubleshoot ${RUN_ID} (one model call on your own credential)"
  run_bounded "$DIAGNOSIS_TIMEOUT" diag diag_env "$FUGARO_BIN" troubleshoot "$RUN_ID"
  local timed_out=$BOUNDED_TIMED_OUT
  DIAG_STATUS=$BOUNDED_STATUS
  if [ "$timed_out" = yes ]; then
    DIAGNOSIS_TIMED_OUT=yes
    return 0
  fi
  if [ "$DIAG_STATUS" != 0 ]; then return 0; fi
  if grep -q '^Outcome:  ' "$SCRATCH/diag.out"; then DIAGNOSIS_OUTCOME=yes; fi
}

# step_source: the record the fetch left. This is the check's answer: which
# transport the machine without git took, what version it fetched, and whether the
# tree could be tied to the running build.
step_source() {
  local rec kind version anchored tree
  rec=$(record_file)
  if [ ! -f "$rec" ]; then
    fact source-kind FAIL "the diagnosis left no record at ${rec}: a source fetch writes down which version and which transport the tree it cached is of (D1611), and nothing is there. What the diagnosis printed is in $SCRATCH/diag.out and $SCRATCH/diag.err"
    return 0
  fi
  kind=$(json_field "$rec" kind)
  version=$(json_field "$rec" version)
  anchored=$(json_field "$rec" may_not_match)
  tree=$(tree_dir)
  if [ "$kind" = tarball ]; then
    fact source-kind PASS "the record says kind=tarball: on a machine with no git the source of the version comes down as its archive, and this is that transport"
  else
    fact source-kind FAIL "the record says kind=${kind:-nothing} and this machine has no git on PATH: the tarball transport is the one a machine without git takes (D1611), so either git was reachable after all — the no-git row above says what the check built — or the transport choice did not follow it"
  fi
  case "$version" in
  "$V" | "v$RELEASE")
    fact source-version PASS "the record says version=${version}, the version this machine is running (fugaro version ${V}): the tree the diagnosis read is the source of the installed version"
    ;;
  *)
    fact source-version FAIL "the record says version=${version:-nothing} and this machine runs ${V}: the cache holds one source tree, and a version recorded as another is a tree that would be served to the next diagnosis as if it were this build's"
    ;;
  esac
  if [ "$anchored" = False ]; then
    fact source-anchored PASS "the record says may_not_match=false: the tree is the source of the installed version rather than a newer release fetched because the build could not name one"
  else
    fact source-anchored FAIL "the record says may_not_match=${anchored:-nothing}: the tree may be another version's, which for a machine running ${V} means the diagnosis reads code that is not this build's (D1611's own sentence, and the release-build row above says why this build should not be in that case)"
  fi
  if [ -f "$tree/go.mod" ]; then
    fact source-tree PASS "${tree} holds the source (its go.mod is there, $(find "$tree" -type f 2>/dev/null | wc -l | tr -d ' ') files): the agent reads a tree rather than a note about one"
  else
    fact source-tree FAIL "${tree} is not a source tree: the fetch recorded version ${version:-nothing} but left no go.mod at ${tree}/go.mod, so the agent has nothing to read the failure against"
  fi
}

# step_diagnosis_outcome: whether the diagnosis also answered. It is not what this
# check is for — the fetch above is — but a person reading the block wants to know
# whether the whole command worked on the machine this check built.
step_diagnosis_outcome() {
  if [ "$DIAGNOSIS_TIMED_OUT" = yes ]; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} did not finish within ${DIAGNOSIS_TIMEOUT}s and was ended: the command's own cap (--max-turns, --max-budget-usd) is what should end it, and this is a longer look than a check should take. The source it fetched is in $(tree_dir), and its output is in $SCRATCH/diag.out"
    return 0
  fi
  if [ "$DIAG_STATUS" != 0 ]; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} exited ${DIAG_STATUS} with the git-less PATH this check built: it prints an offline answer and exits non-zero when it has no credential or cannot reach the source at this version, which is what this run looks like from here — the source rows above are measured from the fetch either way. What it printed is in $SCRATCH/diag.out and $SCRATCH/diag.err"
    return 0
  fi
  if [ "$DIAGNOSIS_OUTCOME" != yes ]; then
    unmeasured diagnosis "fugaro troubleshoot ${RUN_ID} exited 0 without printing an outcome line: the command's answer is the outcome, the signature and the fix, and this run printed none of them. What it printed is in $SCRATCH/diag.out"
    return 0
  fi
  fact diagnosis PASS "$(grep -m1 '^Outcome:  ' "$SCRATCH/diag.out" | head -c 200) — the whole command ran on a machine with no git and no GitHub account, and the source it read came from the archive of this version"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 411 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] gcloud projects describe ${P} — it must come back NOT FOUND, which is what makes the probe harmless"
  say "  [network] fugaro update --name ${NAME} --gcp-project ${P} --region ${REGION} --yes — a real failed update against a project that does not exist; the run the diagnosis reads"
  say "  [local] mirror your PATH into one scratch directory with git left out, then ask that PATH for git"
  say "  [network, ONE MODEL CALL on your own credential] fugaro troubleshoot <id> with PATH=<the git-less PATH>, GH_TOKEN/GITHUB_TOKEN/GH_CONFIG_DIR unset, and a scratch source cache — so the tarball of the installed version is fetched now"
  say "  [read] <scratch>/cache/fugaro/source/record.json — kind=tarball, version=$(fugaro version 2>/dev/null | head -n 1 || true), may_not_match=false — and the tree beside it"
  say "  [nothing created] no job, no build, no container, no binding and no account: the project does not exist, and your own installation is not the subject of any call"
  for key in fugaro-version xtrace project-absent belong release-build fugaro-in-path no-git \
    no-account update-failed run-logged source-kind source-version source-anchored source-tree diagnosis; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no update, no diagnosis, no model call and no cloud call"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace project-absent belong release-build no-git no-account
  update-failed run-logged source-kind source-version source-anchored source-tree
  diagnosis restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 411 RESULT\n' >&3
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
  printf 'CHECK 411 RESULT END (exit %s)\n' "$status" >&3
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

STAMP=${CHECK411_STAMP:-$(date -u +%Y%m%d%H%M%S)}
P=${CHECK411_PROJECT:-fugaro-chk411-${STAMP}}
NAME=${CHECK411_NAME:-chk411-${STAMP}}
REGION=${CHECK411_REGION:-us-east5}
UPDATE_TIMEOUT=${CHECK411_UPDATE_TIMEOUT_SECONDS:-300}
DIAGNOSIS_TIMEOUT=${CHECK411_DIAGNOSIS_TIMEOUT_SECONDS:-900}
DRY_RUN=${CHECK411_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK411_KEEP_SCRATCH:-}
DIAG_STATUS=0
DIAGNOSIS_TIMED_OUT=no
DIAGNOSIS_OUTCOME=no
V=""
RELEASE=""
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
  [ -n "$FUGARO_BIN" ] || die "Check 411 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_belong
  version_row
  guard_release
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$RELEASE" ]; then return 0; fi
  step_project
  step_update
  step_logged
  step_nogit
  step_noaccount
  if [ -z "$RUN_ID" ] || [ "$NOPATH" = "" ]; then return 0; fi
  step_diagnosis
  step_source
  step_diagnosis_outcome
}

# version_row: the build this check drives, recorded rather than gated. What the
# check does with it is guard_release's business.
version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the version its source has to be of)"
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
