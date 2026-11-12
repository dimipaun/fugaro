#!/usr/bin/env bash
# Check 501 — the slim base, reached the way an owner reaches it: `fugaro update`
# on the sandbox, answered by you at your own terminal, and then one run that has
# to reach its record.
#
# Plan task IS13 writes this check; the steps are
# docs/image-setup-skill/implementation-plan.md's "Checks (reserved 500–509)".
# What it settles is read from the outside and nowhere else: the base pin the one
# upgrade command leaves behind (IS4's slim base), and whether a run really
# starts on an image built after it. The update moving a pin is a claim about a
# config file; a job that starts on the image is the reading that proves it.
#
# What it measures:
#
#   * the installation as it stands, from `fugaro doctor --json` in the sandbox
#     checkout: the project it belongs to and the base pin the local config
#     records (`project.base_images.base`). Doctor's own `local-config-base` check
#     fails on a config that has none, so a missing pin is a finding here too;
#   * **`fugaro update`, run by you at your own terminal and not by this script**:
#     it is launched with no `--yes`, so the review of the run's steps and the
#     billable rebuilds the upgrade asks for are answered by you in the session
#     you are sitting in. Its transcript is echoed to the terminal and kept in
#     the scratch directory, and the check reads its exit status — nothing here
#     answers a question of it;
#   * the pin **after** the update, read the same way: the one command is the
#     whole upgrade, so what it leaves is what the installation is on — and the
#     row says whether the pin moved or was already this release's;
#   * one run launched from the sandbox checkout and followed with `fugaro runs
#     ls --repo <slug> --json` until it is **terminal**, then its `result.json`
#     read from the runs bucket at the run's own key and checked against the run
#     id this check launched (a record read by listing alone could be another
#     run's);
#   * the record's `image.built_at`, against the moment the update ended: a run
#     whose image was built after the upgrade started on what the upgrade put
#     there, and one whose image predates it did not. A record that carries no
#     build time (an image built before that field existed, or a job running the
#     base image directly) is reported as a row this check could not date rather
#     than as a finding.
#
# What it creates: nothing of its own choosing. The update is the upgrade you
# would run anyway (its billable image rebuilds are among the steps you answer),
# and the run is a real sandbox run with a pull request the Restore names.
#
# Usage:      bash scripts/live-checks/check-501.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check-501.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check-501.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, and able to read the runs
#             bucket) and fugaro (the installed release, and the one whose base
#             the update pins).
# Where:      your own machine and the **sandbox project** (`fugaro-dev`) and the
#             **sandbox repository** only, at your own terminal: the update is
#             interactive and a coding agent's session is refused by name, as is
#             a project or region naming belong.
# Creates:    nothing the upgrade does not: `fugaro update` converges the
#             installation (and its billable image rebuilds, which it asks you
#             about), and the check then launches one sandbox run. On disk: one
#             scratch directory, removed at the end, holding the transcript.
# Costs:      what `fugaro update` costs on your installation — among its steps,
#             the rebuild of the checkout's workflow images, billable and asked
#             for on its own — plus one sandbox run and what its task costs.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-501_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and its branch are
#             NOT closed by this script: the Restore row names them.
# Output:     progress on stderr, the update's own output echoed to the terminal,
#             ONE block on stdout headed CHECK 501 RESULT, then CHECK 501 RESULT
#             END (exit N). Exit 0: every criterion held. 1: a criterion did not
#             hold, or a guard stopped the run. 2: the check could not measure —
#             a tool is missing, the checkout is not one, the update or a read
#             did not answer, or the run never ended. A key the run never reached
#             reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value, no Terraform variable and
# no file the update read reaches the block or the scratch directory. The rows
# carry names, refs, counts, timestamps and exit statuses.
#
# Knobs (all CHECK501_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), TASK and RECIPE (what the run is
# launched with), SKIP_UPDATE (1: measure the pin and the run without running the
# upgrade — what you want on a machine that has just been updated),
# DOCTOR_TIMEOUT_SECONDS (180), RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15: how
# often the run is looked for), CLI_TIMEOUT_SECONDS (180),
# LAUNCH_TIMEOUT_SECONDS (600) and REGION (us-east5). The update itself has no
# bound: it runs in the foreground so that its questions reach you and Ctrl-C
# reaches it, which is the bound an interactive command should have.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a real
# `fugaro update` at a real terminal, and a trace of that is a second copy of the
# upgrade's own output.
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
  command -v bash >/dev/null 2>&1 || die "Check 501 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 501 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk501-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REGION_USED=""
BASE_BEFORE=""
BASE_AFTER=""
UPDATE_STARTED=""
UPDATE_ENDED=""
UPDATE_ASKED=""
PICKED_RUN=""
RUN_ID=""
RUN_SLUG=""
SETTLED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk501.XXXXXXXX") || die "Check 501 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  mkdir -p "$SCRATCH/out"
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
    say "CHECK501_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a ref the update did not write the day the document is re-wrapped.
# Every reader prints `key=value` lines or `\t`-separated rows, so a shell can
# hold them without a second parser, and a timestamp is printed both as written
# and as epoch seconds, because the image row compares it with a time this script
# took.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def epoch(text):
    if not text:
        return ""
    stamp = text.strip().replace("Z", "+00:00")
    try:
        return "%d" % datetime.datetime.fromisoformat(stamp).timestamp()
    except ValueError:
        return ""


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    bases = project.get("base_images") or {}
    print("gcp_project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))
    print("base=%s" % (bases.get("base") or ""))
    print("base_kinds=%s" % ";".join(sorted(bases.keys())))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
        ))
elif what == "record":
    image = doc.get("image") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("pushed=%s" % yes(doc.get("pushed_head")))
    print("built_at=%s" % (image.get("built_at") or ""))
    print("built_at_epoch=%s" % epoch(image.get("built_at")))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file, and fail the step when
# the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# bound_loop SECONDS PID: watch one background job to its bound and end it when
# the bound is reached. A hung cloud call would otherwise outlive the check that
# gave up on it, which is the one thing the bound exists to prevent — so the
# child of both helpers below execs, and the pid watched here IS the command.
bound_loop() { # bound_loop SECONDS PID
  local secs=$1 pid=$2 waited=0
  BOUNDED_TIMED_OUT=no
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

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. The ERR trap is dropped in the child: this
# check runs commands whose failing exit status is one of its own readings (an
# update that fails, a record that is not there), and a trap inherited here would
# turn each of them into "an internal error" and a status of 2 before the step
# could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# run_tee KEY DIR CMD...: run a command that talks to the person in front of it,
# in the FOREGROUND — its stdin is this shell's, so the questions `fugaro update`
# asks are answered by YOU and never by this script, and Ctrl-C reaches the
# update itself rather than only the check wrapping it. Its output goes to the
# terminal as it is written and to the scratch directory as a transcript; the
# child's own status is what comes back, because pipefail is on.
run_tee() { # run_tee KEY DIR CMD...
  local key=$1 dir=$2
  shift 2
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) 2>&1 | tee "$SCRATCH/$key.out"
}

# human_time EPOCH: an epoch in the form the run records write, for a row that
# has to name the moment it compared against.
human_time() { # human_time EPOCH
  date -u -r "$1" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
    date -u -d "@$1" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
    printf '%s' "$1"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-501.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the update's output to the terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the update's output"
}

# guard_agent_session: the check is the owner's, and the update it drives asks
# the person in front of it questions a coding agent's session must not answer.
# Refusing here rather than letting the update refuse one step at a time is what
# makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 501 runs \`fugaro update\` and then \`fugaro run\` on the sandbox, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — the update below asks you its questions"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release this check updates to: the base the update pins and the job images it rebuilds are this build's)"
}

# ---------------------------------------------------------------- the steps
# step_installation: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and both the update and the run are that project's.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the update this check runs converges the checkout it is run in"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation this checkout belongs to, and every row below is about it. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to, and the update, the run and its record are all that installation's. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there, and neither is \`fugaro update\`" ;;
  esac
  fact installation PASS "${SANDBOX} is a Fugaro repository's checkout of the installation ${PROJECT} (region ${REGION_USED:-unknown}): the update below converges this installation and this checkout, and the run is launched from here"
}

# step_base_pin: the pin before the update, read rather than assumed. It is the
# base every image of this installation is built FROM, and doctor's own
# `local-config-base` check fails on a config that has none — so a missing pin is
# the same finding here, and the comparison the row below makes needs this read
# to exist.
step_base_pin() {
  if [ -z "$PROJECT" ]; then
    unmeasured base-pin "there is no installation whose pin could be read: the row above says why"
    return 0
  fi
  BASE_BEFORE=$(field "$SCRATCH/doctor.txt" base)
  if [ -z "$BASE_BEFORE" ]; then
    fact base-pin FAIL "the local config of ${SANDBOX} records no base_images.base (base_images holds: $(field "$SCRATCH/doctor.txt" base_kinds)), which is the check fugaro doctor's own local-config-base makes too: every workflow of the repository builds FROM that pin, and the update below has nothing to move. Run \`fugaro update\` (or \`fugaro doctor\`) and read what it says about the pin before running this check again"
    return 0
  fi
  fact base-pin PASS "before the update the installation's base pin is ${BASE_BEFORE} (project.base_images.base, read with fugaro doctor --json): this is the ref the update converges from, and the row below reads what it leaves"
}

# step_update: the one command that is the whole upgrade, run by the person this
# check belongs to. No --yes is passed — that is the point of the row: the review
# of the run's steps and the billable rebuilds it asks for are answered by you in
# your own session, and the transcript below is what it said.
step_update() {
  if [ -z "$PROJECT" ]; then
    unmeasured update "there is no installation to update: the row above says why"
    return 0
  fi
  if [ -n "$SKIP_UPDATE" ]; then
    fact update INFO "not asked for (CHECK501_SKIP_UPDATE): the upgrade was not run, so this check read the pin and launched the run on the installation as it stands. The row below still says what the pin is, and the image row has no moment to date against"
    return 0
  fi
  UPDATE_STARTED=$(date +%s)
  say "running: fugaro update, in ${SANDBOX} — it asks YOU its questions (no --yes), and its output is echoed below and kept as a transcript"
  # `|| BOUNDED_STATUS=$?` and not a bare call: the update's failing status is one
  # of this check's own readings, and a bare call under `set -e` would end the
  # check here with an internal error instead of recording the row below.
  BOUNDED_STATUS=0
  run_tee update "$SANDBOX" "$FUGARO_BIN" update || BOUNDED_STATUS=$?
  UPDATE_ENDED=$(date +%s)
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact update FAIL "fugaro update exited ${BOUNDED_STATUS} in ${SANDBOX}: the one command that is the whole upgrade has to work for the installation to be on this release at all, and everything below is about the installation it left. Its transcript is in $SCRATCH/update.out, and \`fugaro doctor\` is what reads what is left behind"
    return 0
  fi
  if grep -qi 'rebuild\|build' "$SCRATCH/update.out" 2>/dev/null; then
    UPDATE_ASKED="and it named a build (the checkout's workflow images are rebuilt behind their own confirmation, which you answered)"
  else
    UPDATE_ASKED="and it named no build, so no workflow image of this checkout needed rebuilding — its transcript is in $SCRATCH/update.out"
  fi
  fact update PASS "fugaro update ran to its end in ${SANDBOX} (exit 0, $((${UPDATE_ENDED} - ${UPDATE_STARTED}))s) at your own terminal and with no --yes, so its review and its billable rebuilds were answered by you ${UPDATE_ASKED}: it is the one command that is the whole upgrade, and the one that moves the base pin the row below reads"
}

# step_pin_after: what the update left. The pin is read again with the same
# command: the row reports the ref, whether it moved, and whether anything at all
# is recorded, which is the state every image of the installation is built FROM.
step_pin_after() {
  if [ -z "$PROJECT" ]; then
    unmeasured pin-after "there is no installation whose pin could be read: the row above says why"
    return 0
  fi
  say "reading the pin the update left: fugaro doctor --json"
  run_bounded "$DOCTOR_TIMEOUT" pin "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pin-after "fugaro doctor --json did not answer after the update (exit ${BOUNDED_STATUS}), so this check cannot read the pin the update left. Its output is in $SCRATCH/pin.out and $SCRATCH/pin.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/pin.out" "$SCRATCH/pin.txt"; then
    unmeasured pin-after "fugaro doctor --json printed a document this check could not read after the update: what it printed is in $SCRATCH/pin.out"
    return 0
  fi
  BASE_AFTER=$(field "$SCRATCH/pin.txt" base)
  if [ -z "$BASE_AFTER" ]; then
    fact pin-after FAIL "the local config records no base_images.base after the update (base_images holds: $(field "$SCRATCH/pin.txt" base_kinds)): the pin the upgrade pins is what every workflow of this repository builds FROM, and an installation that has none is running images nothing names. What fugaro doctor --json printed is in $SCRATCH/pin.out"
    return 0
  fi
  if [ "$BASE_AFTER" = "$BASE_BEFORE" ]; then
    fact pin-after PASS "the base pin after the update is ${BASE_AFTER}, the same ref the row above read: this installation was already on this release's base, so the update had nothing to move (project.base_images.base)"
  else
    fact pin-after PASS "the base pin after the update is ${BASE_AFTER}, moved from ${BASE_BEFORE:-none}: this is the slim base this release pins, and the row below is what says a job really starts on an image built from it"
  fi
}

# step_run: one sandbox run, launched from the checkout the update converged.
# `--json` names the run this check then follows.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no installation to launch a run in: the row above says why"
    return 0
  fi
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the update above is only half the reading — a run that refuses says the installation it left cannot start a job. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  RUN_SLUG=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${RUN_SLUG:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${RUN_SLUG} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)): the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${RUN_SLUG} --json --limit 1)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$RUN_SLUG" --json --limit 1
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_result_json: the record, read from the runs bucket at the run's own key
# and checked against the run this check launched.
step_result_json() {
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  local key="runs/${RUN_SLUG}/${RUN_ID}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the image it started on is written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  PICKED_RUN=$RUN_ID
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), pushed=$(field "$SCRATCH/record.txt" pushed)): the run this check launched reached its own record after the upgrade, read from the bucket as you would read it"
}

# step_image: the dated evidence that the run started on what the upgrade put
# there. The record's `image.built_at` is when the image the job ran was built,
# so one at or after the moment the update ended is a job on an image built after
# the base pin moved — and one before it is a job on the image the installation
# had before the upgrade. A record that carries no build time is not a finding:
# an image built before the field existed, and a job running the base image
# directly, both leave it empty by design.
step_image() {
  if [ "$SETTLED" != yes ] || [ -z "$PICKED_RUN" ]; then
    unmeasured image "there is no record to read the image from: the row above says why"
    return 0
  fi
  local built_at built_epoch
  built_at=$(field "$SCRATCH/record.txt" built_at)
  built_epoch=$(field "$SCRATCH/record.txt" built_at_epoch)
  if [ -z "$built_at" ]; then
    fact image INFO "not measured: the record of ${RUN_ID} carries no image.built_at, so this check cannot date the image the job started on. An image built before that field existed, and a workflow running the base image directly, both leave it out; the base pin the update left, above, is the reading this run gives"
    return 0
  fi
  if [ "$SKIP_UPDATE" != "" ]; then
    fact image INFO "not dated: CHECK501_SKIP_UPDATE was set, so no update ran and there is no moment to compare the image's build time (${built_at}) against. The pin above is what this installation is on"
    return 0
  fi
  if [ -z "$built_epoch" ]; then
    fact image INFO "not dated: the record's image.built_at (${built_at}) is not a timestamp this check could read, so it cannot say whether the image was built after the update"
    return 0
  fi
  if [ "$built_epoch" -lt "$UPDATE_ENDED" ]; then
    fact image FAIL "the run ${RUN_ID} started on an image built at ${built_at}, before the update ended ($(human_time "$UPDATE_ENDED")): the job ran the image the installation already had, so the base the update pinned is not what this run started on. If you declined the rebuild the update offered, that is this row and the run proves nothing about the new base — walk the rebuild steps in the update's own transcript ($SCRATCH/update.out) and run this check again"
    return 0
  fi
  fact image PASS "the run ${RUN_ID} started on an image built at ${built_at}, at or after the moment the update ended (image.built_at): the job's image was built by the upgrade this check ran, which is the outside evidence that the base pin the update left is what a run really starts on"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check wrote no pin, no config and no policy of its own: the only write was \`fugaro update\`, run by you at your own terminal, whose steps you answered. It then launched the sandbox run ${RUN_ID:-none} and left its pull request and its branch open: close that pull request and delete its branch yourself once you have read the run. The update's transcript was in the scratch directory"
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

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 501 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, and the base pin it records (project.base_images.base)"
  if [ -n "$SKIP_UPDATE" ]; then
    say "  [not asked for] fugaro update: CHECK501_SKIP_UPDATE is set, and this check only reads the pin and launches the run"
  else
    say "  [network, BILLABLE, asks YOU] fugaro update in ${SANDBOX} — the one command that is the whole upgrade, run with no --yes at your own terminal: its review, its Terraform and the rebuild of the checkout's workflow images are answered by you"
  fi
  say "  [read] fugaro doctor --json again — the base pin after the update"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<repo>/<run>/result.json — the record, and the image.built_at it was built at"
  for key in installation base-pin update pin-after run-launched terminal result-json image; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no update, no run and no bucket read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation base-pin update
  pin-after run-launched terminal result-json image restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 501 RESULT\n' >&3
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
  printf 'CHECK 501 RESULT END (exit %s)\n' "$status" >&3
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
SANDBOX_CLI=""
TASK_CLI=""
RECIPE_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the update converges and the run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK501_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 501 needs --sandbox: the checkout of a repository of the sandbox project. The update is run in it, and the run is launched from it. Usage: bash scripts/live-checks/check-501.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${CHECK501_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK501_RECIPE:-${RECIPE_CLI:-}}
SKIP_UPDATE=${CHECK501_SKIP_UPDATE:-}
DRY_RUN=${CHECK501_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK501_KEEP_SCRATCH:-}
REGION=${CHECK501_REGION:-us-east5}
DOCTOR_TIMEOUT=${CHECK501_DOCTOR_TIMEOUT_SECONDS:-180}
RUN_TIMEOUT=${CHECK501_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK501_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK501_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK501_LAUNCH_TIMEOUT_SECONDS:-600}
# Resolved before anything runs, and by absolute path: this check drives a real
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
  [ -n "$FUGARO_BIN" ] || die "Check 501 needs 'fugaro' on PATH: the check drives the upgrade you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_installation
  step_base_pin
  step_update
  step_pin_after
  step_run
  step_terminal
  step_result_json
  step_image
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
