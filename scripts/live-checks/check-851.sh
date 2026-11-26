#!/usr/bin/env bash
# Check 851 — `fugaro update` on a workspace and on a member staged at the
# PREVIOUS release, with a run in flight on each: both converge with no other
# step, both runs finish, and a member that never turns the switch on sees no
# change.
#
# Plan task WC12 writes it (docs/workspace-credentials/implementation-plan.md §8);
# no task of that plan runs it. What it settles is R11 of the plan and the HARD
# RULE it comes from (CLAUDE.md): `fugaro update` is the whole upgrade and it
# works perfectly, and it is safe while runs are in flight, always — and the
# release that carries the personal half adds two steps that can still reach a
# run (WC11 names them, Check 773's own subject is the workspace-service half).
# The third thing this check reads is the half the release must NOT touch: a
# member that never sets `personal_workspace_tokens` is served exactly what it
# was served before, and its published policy carries no such key.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. It runs no update: an update is a
# real installation's, and nothing here may write to a cloud. The owner stages
# both sides at the previous release, launches a run on each, runs `fugaro
# update` on each with its output kept, and passes the paths in. What the script
# measures is what those updates left:
#
#   * both sides CONVERGED: `fugaro doctor --all --json` on the workspace and on
#     the member reports no warning afterwards, which is what "with no other
#     step" means — every step the update owed the installation, it took;
#   * the update's own line about the runs in flight: the sentence the HARD RULE
#     asks for (internal/cli/update_in_flight.go: "<N> in flight: update waits
#     for none and changes nothing they hold"), present in both logs, with no
#     failure line beside it;
#   * the two runs themselves, as `fugaro runs ls --json` states them: terminal,
#     with no halt that fell inside the update's own window;
#   * the member that never turned the switch on: its PUBLISHED policy node
#     carries no `personal_workspace_tokens` member (`fugaro project policy show
#     --json`, the node `settings set` publishes and the workspace's service
#     reads), and that member's own run was served no personal container — its
#     credentials service's `credsvc: audit` lines for the run name no
#     `fugaro_u_…_claude-oauth-token` credential. A release that published the
#     key for a member that never asked for it, or served one a token, is D2972
#     read backwards.
#
# Usage:      bash scripts/live-checks/check-851.sh
#             bash scripts/live-checks/check-851.sh --dry-run
#             bash scripts/live-checks/check-851.sh --help
# Needs:      bash, mktemp, python3, gcloud and fugaro (doctor, the run listing,
#             the published policy, the version).
# Where:      run from anywhere; the two updates' logs are files you name. The
#             installation the guards read is ${CHECK851_INSTALLATION}.
# Creates:    nothing anywhere. The update logs are files you wrote; everything
#             this check reads it reads into a scratch directory (mode 0700)
#             removed from the exit trap even on a failure or Ctrl-C.
# Costs:      nothing billable by the check: it calls no model, launches no run
#             and runs no update. The two runs it reads are the owner's own.
# Runs:       by YOU, after the two updates — and no agent runs any step of it.
#             Its own test (check-851_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 851 RESULT,
#             then CHECK 851 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure — a read that did not answer, or a value it
#             was not given. In the block, a key the run never reached reads
#             `FAIL — not reached`.
#
# Knobs (all CHECK851_*): DRY_RUN, KEEP_SCRATCH, WORKSPACE_PROJECT (the
# workspace's Fugaro project, for its doctor read), MEMBER_PROJECT (the member's,
# `fugaro` by default), INSTALLATION (the installation the guard reads),
# PREVIOUS_RELEASE (the release both sides were staged at — the version the
# updates were run FROM, which this CLI has to be newer than), WORKSPACE_UPDATE_LOG
# and MEMBER_UPDATE_LOG (the two updates' own output, saved by you — unset, the
# rows about them are not measured), WORKSPACE_RUN and MEMBER_RUN (the run in
# flight on each side, by run id or its last four characters — unset, the two run
# rows are not measured) and WORKSPACE_UPDATE_AT (when the workspace's update ran,
# epoch seconds or RFC 3339). The rest are the reads' own knobs: LOG_FRESHNESS,
# LOG_LIMIT and LOG_POLL (how long to wait between the service's log reads).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run reads an
# installation's doctor document and its service's log, and the shell it is
# started from may be a login shell whose trace ends up in a log.
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

# The one sentence the HARD RULE asks the update to say about the runs in flight
# (internal/cli/update_in_flight.go, announceRunsInFlight). It is grepped, not
# paraphrased: a check that read a differently worded line as the same promise
# would read a release that says something else as this one.
IN_FLIGHT_PHRASE='in flight: update waits for none and changes nothing they hold'
# A line of an update that says a step it owed did not land. An update that
# failed a step and printed it is the release that ships an operator step, which
# the HARD RULE forbids.
FAILURE_WORDS='refused|failed|could not|not applied|permission denied'
# The switch one member turns on and the other never does (C1:
# creds.PersonSwitchKey, the top level of the member's published policy).
SWITCH_KEY='personal_workspace_tokens'
# The one personal container this plan serves, under its owner's own prefix:
# secretname.User(sub, creds.PersonTokenName). It is read as a SHAPE and never
# as a name: what the rows below need is whether a call was served a container of
# that shape, and no line of this check's output carries a person's own id.
PERSONAL_NAME_RE='fugaro_u_[0-9]+_claude-oauth-token'

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 851 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 851 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk851-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
RUNS_OUT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk851.XXXXXXXX") || die "Check 851 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  write_reader
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK851_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted by this check: no update ran, no installation was written and no run was touched. The two update logs are yours and stay where you put them, and this check reads them from there"
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

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }
field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }
lines_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p"; }
last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}
# mask_user_names replaces every `fugaro_u_<id>` with `fugaro_u_…` in what this
# check prints: the id is a person's own Google `sub`, the rows below need the
# name's SHAPE and never whose it is, and no line of this output may carry one.
mask_user_names() { sed -E "s/fugaro_u_[0-9]+/fugaro_u_…/g"; }

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 851's reader: an installation's doctor document, an update's own
output, the run listing, a member's published policy, and the credentials
service's own audit lines. None of them carries a credential value.

doctor   argv: `fugaro doctor --all --json`'s file. Prints `ok=` and one
         `warning=<id>: <problem>` line per check whose severity is a warning —
         a problem doctor leaves for the operator — plus `checks=` and
         `warnings=`. A warning is what "converged with no other step" is not.
update   argv: a file of the update's own output, the phrase that names the runs
         in flight, and the words that mean a step did not land. Prints
         `lines=`, `inflight=yes|no`, and one `failure=<line>` line per line
         matching those words, so the caller can read a step that did not land
         rather than only its count.
runs     argv: `fugaro runs ls --json`'s file and the run id asked about (or its
         last characters). Prints the row's status, terminal, outcome and its
         halt's reason, moment, detail and workspace marker; `found=no` when no
         row matches.
switch   argv: `fugaro project policy show --project <member> --json`'s file and
         the key. Prints `policy=yes|no` (whether a node is published at all)
         and `value=true|false|absent` for the switch at the node's top level,
         which is where the workspace's service reads it (C1: the switch's one
         enforcer, R9). Read off the node and not off `settings get`: that
         command's row for this key is a local-config row, so its `wins` is a
         file or the default and never the published layer — a reading that
         could not see the node would pass every member.
audit    argv: the service's log lines' file, a run id, and the regex a personal
         container's name matches. Prints `audit=<status>|<credential>|tier|
         <purpose>` for each `credsvc: audit` line of that run — the credential
         is the NAME the service used and never a value — and `personal=<n>`,
         how many of those lines named a credential of that shape.
"""
import json
import re
import sys


def dig(doc, *path):
    cur = doc
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            return None
        cur = cur[step]
    return cur


def text(v, cap=300):
    s = re.sub(r"\s+", " ", str(v or "")).strip()
    return s[:cap]


def doctor(path):
    with open(path) as fh:
        doc = json.load(fh)
    checks = dig(doc, "checks")
    if not isinstance(checks, list):
        checks = []
    warnings = 0
    for one in checks:
        if not isinstance(one, dict):
            continue
        if str(one.get("severity") or "") != "warning":
            continue
        warnings += 1
        print("warning=%s: %s" % (str(one.get("id") or "?"), text(one.get("problem"))))
    print("ok=" + ("yes" if doc.get("ok") else "no"))
    print("checks=%d" % len(checks))
    print("warnings=%d" % warnings)


def update(path, phrase, words):
    with open(path, "r", errors="replace") as fh:
        raw = fh.read()
    lines = raw.splitlines()
    print("lines=%d" % len(lines))
    print("inflight=" + ("yes" if any(phrase in one for one in lines) else "no"))
    pattern = re.compile(words, re.IGNORECASE)
    for one in lines:
        body = one.strip()
        if not body or not pattern.search(body):
            continue
        print("failure=" + text(body))


def runs(path, asked):
    with open(path) as fh:
        doc = json.load(fh)
    rows = dig(doc, "runs")
    if not isinstance(rows, list):
        rows = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        run_id = str(row.get("run_id") or "")
        if not asked or not (run_id == asked or run_id.endswith(asked)):
            continue
        halt = row.get("halt") if isinstance(row.get("halt"), dict) else {}
        print("found=yes")
        print("run_id=" + run_id)
        print("status=" + str(row.get("status") or ""))
        print("terminal=" + ("yes" if row.get("terminal") else "no"))
        print("outcome=" + str(row.get("outcome") or ""))
        print("reason=" + str(row.get("reason") or ""))
        print("halt_reason=" + str(halt.get("reason") or ""))
        print("halt_at=" + str(halt.get("at") or ""))
        print("halt_detail=" + text(halt.get("detail")))
        print("halt_workspace_refusal=" + str(halt.get("workspace_refusal") or ""))
        return
    print("found=no")


def switch(path, key):
    with open(path) as fh:
        doc = json.load(fh)
    policy = doc.get("policy") if isinstance(doc, dict) else None
    if not isinstance(policy, dict):
        print("policy=no")
        print("value=absent")
        return
    print("policy=yes")
    value = policy.get(key)
    print("value=" + ("absent" if value is None else ("true" if value else "false")))


def audit(path, run, pattern):
    want = re.compile(pattern)
    with open(path) as fh:
        doc = json.load(fh)
    if not isinstance(doc, list):
        doc = []
    personal = 0
    for entry in doc:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        if not isinstance(payload, dict):
            continue
        if str(payload.get("run") or "") != run:
            continue
        msg = entry.get("textPayload") or payload.get("msg") or payload.get("message") or ""
        if msg != "credsvc: audit":
            continue
        credential = str(payload.get("credential") or "")
        # The id is a person's own and never leaves this reader: the caller reads
        # the shape, and what it prints is the masked name.
        shown = re.sub(r"fugaro_u_[0-9]+", "fugaro_u_…", credential)
        if want.search(credential):
            personal += 1
        print("audit=%s|%s|%s|%s" % (
            text(payload.get("status"), 40), text(shown, 80),
            text(payload.get("tier"), 8), text(payload.get("purpose"), 40)))
    print("personal=%d" % personal)


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "doctor":
        doctor(argv[2])
        return 0
    if argv[1] == "update":
        update(argv[2], argv[3], argv[4])
        return 0
    if argv[1] == "runs":
        runs(argv[2], argv[3] if len(argv) > 3 else "")
        return 0
    if argv[1] == "switch":
        switch(argv[2], argv[3])
        return 0
    if argv[1] == "audit":
        audit(argv[2], argv[3], argv[4])
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
  chmod 700 "$SCRATCH/reader.py"
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 851 (the plan says so): the two updates it
# reads are the owner's, and a coding agent's session is refused by the markers
# it carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 851. Run it yourself: bash scripts/live-checks/check-851.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-851.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the doctor documents and the service's lines it reads"
}

guard_project() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check's doctor reads are about ${MEMBER_PROJECT}${WORKSPACE_PROJECT:+, and about ${WORKSPACE_PROJECT},} and never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$v" in
  *belong*) stop "the version this check printed names belong: this check is never run there" ;;
  esac
  if [ -n "$PREVIOUS_RELEASE" ]; then
    case "$v" in
    *"${PREVIOUS_RELEASE}"*)
      fact fugaro-version FAIL "the CLI is ${v:-unknown}, which names the previous release (${PREVIOUS_RELEASE}) itself: the two logs were written by an update run AT that release, so a check read with the same CLI reads the old promises about the old release and cannot see whether the upgrade converged. Upgrade this machine ('brew upgrade fugaro'), then run the check again: fugaro version"
      return 0
      ;;
    esac
  fi
  fact fugaro-version PASS "${v:-fugaro version printed nothing}${PREVIOUS_RELEASE:+ — newer than the staged release ${PREVIOUS_RELEASE}} — this is the CLI the two updates' logs are read against: R11 is about 'fugaro update' on the release the CLI is, and a check run with the wrong release reads the wrong release's promises"
}

# ------------------------------------------------------------------- the steps
# step_converged: doctor after the update, on each side. This is "with no other
# step": every step the update owed the installation, it took.
step_converged() { # step_converged KEY PROJECT WHERE
  local key=$1 project=$2 where=$3
  fugaro doctor --project "$project" --all --json >"$SCRATCH/doctor-$key.json" 2>"$SCRATCH/doctor-$key.err" || true
  if [ ! -s "$SCRATCH/doctor-$key.json" ]; then
    unmeasured "$key" "the doctor document for ${where} (${project}) could not be read ($(last_line "$(cat "$SCRATCH/doctor-$key.err" 2>/dev/null || true)")): 'fugaro doctor --project ${project} --all --json' failed. Run it here yourself and read its output"
    return 0
  fi
  local out warnings checks
  out=$(reader doctor "$SCRATCH/doctor-$key.json") || die "the doctor document could not be parsed ($(last_line "$out"))"
  warnings=$(field_of warnings "$out")
  checks=$(field_of checks "$out")
  if [ "${warnings:-0}" -eq 0 ]; then
    fact "$key" PASS "doctor on ${where} (${project}) reports ${checks:-?} checks and NO warning after the update: the installation is where the release the CLI is says it should be, with no step left for a person to take — which is what R11 asks the update to converge to, and what the HARD RULE calls 'the whole upgrade, and it works perfectly'. Run it yourself: fugaro doctor --project ${project} --all"
    return 0
  fi
  fact "$key" FAIL "doctor on ${where} (${project}) reports ${warnings} warning(s) after the update: $(printf '%s' "$(lines_of warning "$out" | head -n 3 | tr '\n' '; ')" | head -c 500). An update that leaves a warning is an update with another step behind it, which is exactly what the HARD RULE forbids — the fix belongs IN the update of the same release, not in a runbook"
}

# step_update_log: the update's own line about the runs in flight, and no step
# that did not land.
step_update_log() { # step_update_log KEY LOG ENVVAR WHERE
  local key=$1 log=$2 envvar=$3 where=$4
  if [ -z "$log" ]; then
    unmeasured "$key" "no path was given for ${where}'s update output (${envvar} is unset). Save it when you run the update — 'fugaro update 2>&1 | tee /tmp/${key}.log' on ${where} — and pass the path as ${envvar}"
    return 0
  fi
  if [ ! -r "$log" ]; then
    unmeasured "$key" "the file you named for ${where}'s update output (${log}) cannot be read: check the path, and that the update's output was saved rather than typed into a terminal that scrolled"
    return 0
  fi
  local out inflight failures
  out=$(reader update "$log" "$IN_FLIGHT_PHRASE" "$FAILURE_WORDS") || die "the update output could not be parsed ($(last_line "$out"))"
  inflight=$(field_of inflight "$out")
  failures=$(lines_of failure "$out")
  if [ -n "$failures" ]; then
    fact "$key" FAIL "the update on ${where} printed $(printf '%s\n' "$failures" | grep -c .) line(s) that name a step which did not land: $(printf '%s' "$(printf '%s\n' "$failures" | head -n 3 | tr '\n' '; ')" | head -c 500). R11 and the HARD RULE both say 'fugaro update' is the whole upgrade and it works perfectly: a step that failed and was printed is an operator step the release ships, and the next release has to take it inside the update"
    return 0
  fi
  if [ "$inflight" != yes ]; then
    fact "$key" FAIL "the update on ${where} printed no '${IN_FLIGHT_PHRASE}' line, and a run was in flight on it: the HARD RULE says the update says so in one line — the count, and that the update waits for none of them and changes nothing they hold. Either the run started after the update, or this release does not print the line. Read the log yourself: grep 'in flight' ${log}"
    return 0
  fi
  fact "$key" PASS "the update on ${where} printed the line the HARD RULE asks for ('${IN_FLIGHT_PHRASE}', $(field_of lines "$out") lines of output) and no line naming a step that did not land: it named the runs in flight rather than denying them, and it left nothing for a person to take"
}

# step_run: the run in flight, as its own record states it. It has to be over,
# and no halt of it may fall inside the update's window: a run the update stopped
# is what R11 forbids.
step_run() { # step_run KEY RUN ENVVAR AT WHERE
  local key=$1 run=$2 envvar=$3 at=$4 where=$5
  if [ -z "$run" ]; then
    unmeasured "$key" "no run id was given for ${where} (${envvar} is unset). Launch the run BEFORE the update, keep its run id, and pass it as ${envvar} (or its last four characters)"
    return 0
  fi
  if [ -z "$RUNS_OUT" ]; then
    unmeasured "$key" "not read: the run listing could not be read. Read it yourself: fugaro runs ls --json | grep -A8 ${run}"
    return 0
  fi
  local out
  out=$(reader runs "$SCRATCH/runs.json" "$run") || die "the run listing could not be parsed ($(last_line "$out"))"
  if [ "$(field_of found "$out")" != yes ]; then
    unmeasured "$key" "no run in the listing has an id ending in ${run}. List them: fugaro runs ls --json | grep run_id"
    return 0
  fi
  local rid status terminal halt_reason halt_at detail
  rid=$(field_of run_id "$out")
  status=$(field_of status "$out")
  terminal=$(field_of terminal "$out")
  halt_reason=$(field_of halt_reason "$out")
  halt_at=$(field_of halt_at "$out")
  detail=$(field_of halt_detail "$out")
  if [ "$terminal" != yes ]; then
    fact "$key" FAIL "the run ${rid} on ${where} is not terminal after the update (status ${status:-unknown}): R11 says a run in flight keeps the environment and the route it started with, so the update leaves it alone — a run still going hours after an update is a run the update is holding, or one that died without its record being written. Read it: fugaro runs show ${rid}"
    return 0
  fi
  if [ -z "$halt_reason" ]; then
    fact "$key" PASS "the run ${rid} in flight on ${where} finished across the update (status ${status}, outcome $(field_of outcome "$out"), no halt): the update reached neither its environment nor its route, which is what R11 promises and what the HARD RULE calls 'safe while runs are in flight, always'"
    return 0
  fi
  if [ -n "$at" ] && [ -n "$halt_at" ] && in_window "$at" "$halt_at" "$UPDATE_WINDOW_SECONDS"; then
    fact "$key" FAIL "the run ${rid} in flight on ${where} halted ${halt_reason} at ${halt_at}, $(gap_after "$at" "$halt_at" || true), with the update running at ${at} and marker '$(field_of halt_workspace_refusal "$out")'$(detail_note "$detail"): a run whose halt falls inside the update's own window is a run the update stopped, which R11 forbids. The step that reached it has to be named in the update's own line (the budget mode's raise to enforce, the policy a cascade project re-reads, a retirement) — and a run it stopped is not that line's promise being kept"
    return 0
  fi
  fact "$key" FAIL "the run ${rid} in flight on ${where} halted after the update: ${halt_reason} at ${halt_at}$(detail_note "$detail"). Whether the update caused it needs the moment the update ran (CHECK851_WORKSPACE_UPDATE_AT): with it, this row says whether the halt fell inside the update's window. A halt elsewhere is a finding of its own for that run — 'fugaro runs show ${rid}' and 'fugaro diagnose ${rid}' are the two that read it"
}

# in_window FROM TO SECONDS: whether TO falls in [FROM, FROM+SECONDS).
in_window() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    start, end = at(sys.argv[1]), at(sys.argv[2])
except ValueError:
    sys.exit(1)
sys.exit(0 if 0 <= (end - start).total_seconds() < int(sys.argv[3]) else 1)
' "$1" "$2" "$3"
}

# detail_note says ", detail '<the halt's own words>'" — or nothing at all when
# the halt carries none. It is a function and not `${detail:+...}` because a
# quoted word inside a parameter expansion is a syntax error in bash, not a
# quote: the row has to carry the halt's own words whatever they are.
detail_note() {
  [ -n "${1:-}" ] || return 0
  printf ", detail '%s'" "$(printf '%s' "$1" | head -c 200)"
}

gap_after() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    seconds = (at(sys.argv[2]) - at(sys.argv[1])).total_seconds()
except ValueError:
    sys.exit(1)
print("%dm%02ds after the update began" % (seconds // 60, seconds % 60))
' "$1" "$2"
}

# step_switch: the member that never turns the switch on was left alone by the
# release that carries the personal half (D2972 read forwards). Its published
# policy carries no such member, and the switch is read where the workspace's
# service reads it (C1) rather than from the member's file.
step_switch() { # step_switch KEY MEMBER
  local key=$1 member=$2
  fugaro project policy show --project "$member" --json >"$SCRATCH/switch-$member.json" 2>"$SCRATCH/switch-$member.err" || true
  if [ ! -s "$SCRATCH/switch-$member.json" ]; then
    unmeasured "$key" "the member's published policy did not answer: 'fugaro project policy show --project ${member} --json' printed nothing ($(last_line "$(cat "$SCRATCH/switch-$member.err" 2>/dev/null || true)")) — and a read that did not answer is not a member with no switch. Run it here yourself and read its output"
    return 0
  fi
  local out policy value
  out=$(reader switch "$SCRATCH/switch-$member.json" "$SWITCH_KEY") || die "the published policy could not be parsed ($(last_line "$out"))"
  policy=$(field_of policy "$out")
  value=$(field_of value "$out")
  if [ "$policy" != yes ]; then
    fact "$key" PASS "the member (${member}) never turned the switch on and holds no published policy node at all: the release published no ${SWITCH_KEY} for a member that never asked for it, which is D2972 read forwards"
    return 0
  fi
  if [ "$value" = true ]; then
    fact "$key" FAIL "the member (${member}) never turned the switch on, and its PUBLISHED policy node carries ${SWITCH_KEY}=true: the release published a key the member never set, and the workspace's service — the switch's only enforcer (C1, R9) — serves every run of that member under it. D2972 is 'absent reads as off': a member that never sets the key is served exactly what it was served before"
    return 0
  fi
  fact "$key" PASS "the member (${member}) never turned the switch on and its published policy node reads ${SWITCH_KEY}=${value}: the release that carries the personal half left a member that never asked for it alone, which is D2972 read forwards"
}

# step_not_served: that member's own run was served no personal container. The
# reading is the service's own audit lines for the run: the credential NAME it
# used (never a value), matched against the one personal shape this plan serves.
step_not_served() { # step_not_served KEY RUN ENVVAR MEMBER_PROJECT
  local key=$1 run=$2 envvar=$3 member=$4
  if [ -z "$run" ]; then
    unmeasured "$key" "no run id was given for ${member} (${envvar} is unset). Launch a run in that member after the update and pass its id as ${envvar}"
    return 0
  fi
  run=$(run_id_of "$run")
  local log="$SCRATCH/audit-$key.json"
  if ! audit_read "$member" "$run" "$log"; then
    unmeasured "$key" "$(audit_unread "$log")"
    return 0
  fi
  local out personal lines
  out=$(reader audit "$log" "$run" "$PERSONAL_NAME_RE") || die "the service's lines could not be parsed ($(last_line "$out"))"
  personal=$(field_of personal "$out")
  lines=$(printf '%s\n' "$out" | grep -c '^audit=' || true)
  if [ "${lines:-0}" -eq 0 ]; then
    unmeasured "$key" "the service's lines in ${member} hold no 'credsvc: audit' entry for the run ${run}, so 'this run was served no personal container' is not measured: an unread log is not an empty one. Check the run id, and that the read reached the member's own service: gcloud logging read 'jsonPayload.msg=\"credsvc: audit\"' --project ${member} --freshness=${LOG_FRESHNESS} --format=json"
    return 0
  fi
  if [ "${personal:-0}" -eq 0 ]; then
    fact "$key" PASS "the run ${run} in ${member} — a member that never turned the switch on — was served no personal container: none of its ${lines} 'credsvc: audit' line(s) named a credential of the one personal shape this plan serves. The run was served what it was served before the release, which is what 'a member that never turns the switch on sees no change' means"
    return 0
  fi
  fact "$key" FAIL "the run ${run} in ${member} was served a personal container ${personal} time(s) in its 'credsvc: audit' lines, and that member never turned the switch on: $(printf '%s' "$(printf '%s\n' "$out" | grep '^audit=' | grep 'fugaro_u_' | head -n 3 | tr '\n' '; ')" | head -c 400). A member with the switch off must serve no personal token at all (D2972), and its own admin is the only one who can turn it on"
}

# run_id_of RUN: the full id of the run a knob names, off the listing this run
# already read. The service's lines are filtered by that id, and a knob holding
# the run's last four characters would filter on those four and read nothing —
# so a suffix is resolved before the read, and the rows about the run quote the
# id the record holds. A listing that could not be read, or a knob no row
# matches, leaves the knob as the owner wrote it: the rows that use it say what
# they found instead.
run_id_of() { # run_id_of RUN
  local run=$1 out
  [ -n "$RUNS_OUT" ] || {
    printf '%s' "$run"
    return 0
  }
  out=$(reader runs "$SCRATCH/runs.json" "$run" 2>/dev/null || true)
  if [ "$(field_of found "$out")" = yes ]; then
    printf '%s' "$(field_of run_id "$out")"
    return 0
  fi
  printf '%s' "$run"
}

# audit_read PROJECT RUN OUT: the workspace's or a member's service lines for one
# run, retried while Cloud Logging ingests them. The service verifies the run's
# identity, so its own line carries the run id and the read is by it.
audit_read() { # audit_read PROJECT RUN OUT
  local project=$1 run=$2 out=$3 rc=0 i
  # Each retry is a second of the bound however the poll is set, so a poll of
  # zero still bounds the wait rather than spinning through it.
  local tick=$LOG_POLL
  [ "$tick" -gt 0 ] || tick=1
  : >"$out.unread"
  for i in 1 2 3 4 5 6; do
    rc=0
    gcloud logging read \
      "resource.type=\"cloud_run_revision\" AND jsonPayload.msg=\"credsvc: audit\" AND jsonPayload.run=\"${run}\"" \
      --project="$project" --freshness="${LOG_FRESHNESS:-2d}" \
      --limit="${LOG_LIMIT:-200}" --order=asc --format=json >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    if [ "$rc" -eq 0 ] && grep -q '"credsvc: audit"' "$out" 2>/dev/null; then return 0; fi
    if [ "$rc" -eq 0 ] && [ "$i" -ge 2 ]; then return 0; fi
    sleep "$tick"
  done
  printf '%s' "the service's own lines for the run (${run}) in ${project} could not be read: 'gcloud logging read' exited ${rc} and printed: $(last_line "$(cat "$SCRATCH/audit.err" 2>/dev/null || true)"). Every row read off those lines is read off what the service wrote for that run, and a read that did not answer is NOT the service having written no line: nothing here says whether that run was served a personal token or not. Read the log yourself ('gcloud logging read' with that run's id) and run this check again" >"$out.unread"
  return 1
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# step_prompt: what only the owner can do, said out loud.
step_prompt() {
  fact update-prompt INFO "not run by this check, and no agent may run any of them: (1) stage the WORKSPACE's installation at the previous release and the MEMBER's too — a checkout at the previous tag, or the installed older CLI — and launch one run on each with 'fugaro run' so it is in flight; (2) leave the member that will be watched at its default: do NOT run 'fugaro settings set personal_workspace_tokens true' on it; (3) run 'fugaro update' on each side, keeping the output ('fugaro update 2>&1 | tee /tmp/ws-update.log'); (4) write down the moment the update began (date -u +%s); (5) run this check: CHECK851_WORKSPACE_PROJECT=<project> CHECK851_WORKSPACE_UPDATE_LOG=/tmp/ws-update.log CHECK851_MEMBER_UPDATE_LOG=/tmp/member-update.log CHECK851_WORKSPACE_RUN=<id> CHECK851_MEMBER_RUN=<id> CHECK851_PREVIOUS_RELEASE=<the staged release> CHECK851_WORKSPACE_UPDATE_AT=<that> bash scripts/live-checks/check-851.sh"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 851 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guard: fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] fugaro doctor --project ${WORKSPACE_PROJECT:-<workspace project>} --all --json — the workspace, converged?"
  say "  [network, read] fugaro doctor --project ${MEMBER_PROJECT} --all --json — the member, converged?"
  say "  [local, read] the two update logs you saved — the in-flight line and any step that did not land"
  say "  [network, read] fugaro runs ls --json — the run in flight on each side, terminal and unhalted"
  say "  [network, read] fugaro project policy show --project ${MEMBER_PROJECT} --json — the node the member never set the switch on"
  say "  [network, read] gcloud logging read over ${MEMBER_PROJECT} for the member's run — no personal container served"
  say "  [not run] the two updates and the two runs: the owner's, never a script's"
  say "  [nothing created] no update, no installation write, no run; the scratch directory is removed at the end"
  for key in fugaro-version xtrace workspace-converged member-converged workspace-update-line member-update-line workspace-run member-run switch-untouched run-untouched update-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no installation was read and no update log was opened"
}

BLOCK_KEYS=(fugaro-version xtrace dry-run workspace-converged member-converged
  workspace-update-line member-update-line workspace-run member-run switch-untouched
  run-untouched update-prompt restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 851 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3- | mask_user_names)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$(printf '%s' "$detail" | mask_user_names)" >&3
  done
  printf 'CHECK 851 RESULT END (exit %s)\n' "$status" >&3
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

INSTALLATION=${CHECK851_INSTALLATION:-fugaro}
WORKSPACE_PROJECT=${CHECK851_WORKSPACE_PROJECT:-}
MEMBER_PROJECT=${CHECK851_MEMBER_PROJECT:-fugaro}
PREVIOUS_RELEASE=${CHECK851_PREVIOUS_RELEASE:-}
WORKSPACE_UPDATE_LOG=${CHECK851_WORKSPACE_UPDATE_LOG:-}
MEMBER_UPDATE_LOG=${CHECK851_MEMBER_UPDATE_LOG:-}
WORKSPACE_RUN=${CHECK851_WORKSPACE_RUN:-}
MEMBER_RUN=${CHECK851_MEMBER_RUN:-}
WORKSPACE_UPDATE_AT=${CHECK851_WORKSPACE_UPDATE_AT:-}
UPDATE_WINDOW_SECONDS=${CHECK851_UPDATE_WINDOW_SECONDS:-3600}
LOG_FRESHNESS=${CHECK851_LOG_FRESHNESS:-2d}
LOG_LIMIT=${CHECK851_LOG_LIMIT:-200}
LOG_POLL=${CHECK851_LOG_POLL:-2}
DRY_RUN=${CHECK851_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK851_KEEP_SCRATCH:-}
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check reads the run listing and the service's log with that account's own roles. Run 'gcloud auth login' first"
  [ "$UPDATE_WINDOW_SECONDS" -gt 0 ] || die "CHECK851_UPDATE_WINDOW_SECONDS is ${UPDATE_WINDOW_SECONDS}: the window is seconds an update may take, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  [ -n "$WORKSPACE_PROJECT" ] || unmeasured workspace-converged "CHECK851_WORKSPACE_PROJECT was not set, so this run does not know the workspace's Fugaro project to read doctor from. Name it and run the check again: CHECK851_WORKSPACE_PROJECT=<project> bash scripts/live-checks/check-851.sh"
  guard_agent
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "checking the two updates and the runs that were in flight across them"
  [ -n "$WORKSPACE_PROJECT" ] && step_converged workspace-converged "$WORKSPACE_PROJECT" "the workspace"
  step_converged member-converged "$MEMBER_PROJECT" "the member"
  step_update_log workspace-update-line "$WORKSPACE_UPDATE_LOG" CHECK851_WORKSPACE_UPDATE_LOG "the workspace"
  step_update_log member-update-line "$MEMBER_UPDATE_LOG" CHECK851_MEMBER_UPDATE_LOG "the member"
  fugaro runs ls --json >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err" || true
  if [ -s "$SCRATCH/runs.json" ]; then RUNS_OUT=yes; else say "the run listing could not be read ($(last_line "$(cat "$SCRATCH/runs.err" 2>/dev/null || true)"))"; fi
  step_run workspace-run "$WORKSPACE_RUN" CHECK851_WORKSPACE_RUN "$WORKSPACE_UPDATE_AT" "the workspace"
  step_run member-run "$MEMBER_RUN" CHECK851_MEMBER_RUN "$WORKSPACE_UPDATE_AT" "the member"
  step_switch switch-untouched "$MEMBER_PROJECT"
  step_not_served run-untouched "$MEMBER_RUN" CHECK851_MEMBER_RUN "$MEMBER_PROJECT"
  step_prompt
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
