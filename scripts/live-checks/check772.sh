#!/usr/bin/env bash
# Check 772 — the workspace's service cold, with the beat on and off, and a
# run's behaviour across a forced outage shorter and longer than the bound.
#
# Plan task WS20 writes it (docs/workspace-service/implementation-plan.md §8);
# no task of that plan runs it. What it settles is U4 of
# docs/workspace-service/reconciliation.md — the real cold-start time of the
# workspace's service, and whether the beat is needed at all (the owner's own
# doubt in D2985, "I'm not sure we actually need it") — and D2983: a run whose
# workspace service cannot be reached waits and retries for a bounded time, then
# halts with a reason that names the workspace's service.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. It forces no outage, scales nothing,
# stops nothing and starts nothing: an outage and a 100-run load are the owner's
# acts, and the beat is a config setting whose value on a real installation only
# the owner turns. What the script measures is the evidence those acts leave:
#
#   * the beat's setting, as the project's own resolved config holds it
#     (`agent.workspace_beat`, the key C2 reserves; R10 says on is the default);
#   * the deployment's warm floor — the minimum instance count the platform
#     holds — against that setting: a beat that is on and a service whose floor
#     is zero is a beat that keeps nothing warm;
#   * the request log over the window, read for the cold starts in it: a request
#     that followed an idle gap of at least CHECK772_IDLE_GAP_SECONDS, its
#     latency, and the steady latency beside it, which is U4's figure;
#   * two runs the owner forced an outage across — one shorter than the bound,
#     one longer — as their own run records state them (`fugaro runs ls --json`):
#     the first rode it out, the second halted naming the credentials service.
#
# Usage:      bash scripts/live-checks/check772.sh
#             bash scripts/live-checks/check772.sh --dry-run
#             bash scripts/live-checks/check772.sh --help
# Needs:      bash, mktemp, python3, gcloud (authenticated as an OWNER of the
#             project: the check describes the service and reads its log) and
#             fugaro (the guards, the config read and the run listings).
# Where:      the workspace's own installation — for the dogfood installation
#             fugaro-dev in us-east5, both hard-coded in this file. A project,
#             region, account or service naming belong is refused by name.
# Creates:    nothing. The copies of the service document, the config document,
#             the run listing and the log entries this run reads sit in a scratch
#             directory (mode 0700) removed from the exit trap even on a failure
#             or Ctrl-C.
# Costs:      nothing billable by the check: it calls no model and launches no
#             run. The two runs this check reads are the owner's own.
# Runs:       by YOU, after the outage runs — and no agent runs any step of it.
#             Its own test (check772_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 772 RESULT,
#             then CHECK 772 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure. In the block, a key the run never reached
#             reads `FAIL — not reached`.
#
# Knobs (all CHECK772_*): DRY_RUN, KEEP_SCRATCH, SERVICE, INSTALLATION,
# WINDOW_MINUTES (how far back the log is read, 180 by default: a cold start
# needs an idle gap, and a window shorter than the gap holds none),
# BEAT_SETTING (the config key, `agent.workspace_beat`), IDLE_GAP_SECONDS (how
# long a silence counts as the service having gone cold, 600 by default),
# PROJECT (the project whose config the beat is read from, `fugaro` by
# default), SHORT_RUN and LONG_RUN (the two runs the outage was forced across —
# their run ids, or the last four characters of them; unset, the two outage rows
# are not measured) and OUTAGE_AT (when the outage began, epoch seconds or
# RFC 3339: the check reports the wait from it).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run reads the IAM
# policy, and the shell it is started from may be a login shell whose trace ends
# up in a log.
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

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 772 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 772 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk772-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
SERVICE_URL=""
REVISION=""
MIN_INSTANCES=""
BEAT_VALUE=""
BEAT_SOURCE=""
COLD_OUT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk772.XXXXXXXX") || die "Check 772 cannot make a scratch directory"
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
    say "CHECK772_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted by this check: no revision, no instance, no setting, no run. The beat is exactly where you left it — if you turned it off to read the cold start, turn it back on (R10: on is the default) — and the two runs this check read are untouched"
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
gcloud_admin() { gcloud --project="$P" --quiet "$@"; }

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }
field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }
lines_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p"; }
last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 772's reader: the service document, the resolved config, the run
listing and the service's request log. None of them carries a credential.

service  argv: `gcloud run services describe --format=json`'s file. Prints url,
         revision and min_instances: the warm floor as the platform holds it
         (minInstanceCount, or the minScale annotation the Knative rendering
         carries). A document that holds neither prints `unknown`, which is not
         a floor of zero.
config   argv: `fugaro config show --json`'s file, the key to read. Prints
         `beat=<value>` and `beat_source=<source>` for the key, or
         `beat=<absent>` when the resolved config does not carry it — which is
         the default (R10: on), and a reading a caller must be able to tell from
         a failed read.
runs     argv: `fugaro runs ls --json`'s file, the run id asked about. Prints the
         row's status, terminal, outcome, halt_reason, halt_at, halt_detail and
         halt_workspace_refusal, or `found=no` when no row's run_id is that id
         (or ends with it, the console's short form).
cold     argv: a request-log listing's file, the idle gap in seconds. Prints
         requests=, the span the log covers, `cold=<timestamp> <ms>` for every
         request that followed a silence of at least the gap — the cold starts —
         and `steady_ms=<median>` of the requests that did not. A log with no
         such request prints no cold line, which is a reading: nothing went cold
         in this window.
"""

import datetime
import json
import sys


def dig(doc, *path):
    cur = doc
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            return None
        cur = cur[step]
    return cur


def service(path):
    with open(path) as fh:
        doc = json.load(fh)
    print("url=" + (dig(doc, "status", "url") or ""))
    print("revision=" + (dig(doc, "status", "latestReadyRevisionName") or ""))
    found = None
    for path_ in (
        ("spec", "template", "scaling", "minInstanceCount"),
        ("spec", "template", "spec", "scaling", "minInstanceCount"),
        ("spec", "template", "metadata", "annotations", "autoscaling.knative.dev/minScale"),
    ):
        got = dig(doc, *path_)
        if got is not None:
            found = got
            break
    if isinstance(found, bool) or found is None:
        print("min_instances=unknown")
    else:
        print("min_instances=" + str(found))


def config(path, key):
    with open(path) as fh:
        doc = json.load(fh)
    values = dig(doc, "values")
    if not isinstance(values, list):
        values = []
    for one in values:
        if not isinstance(one, dict):
            continue
        if str(one.get("path") or "") != key:
            continue
        print("beat=" + json.dumps(one.get("value")))
        print("beat_source=" + str(one.get("source") or ""))
        return
    print("beat=<absent>")


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
        print("halt_detail=" + str(halt.get("detail") or ""))
        print("halt_workspace_refusal=" + str(halt.get("workspace_refusal") or ""))
        return
    print("found=no")


def latency_ms(request):
    raw = str(request.get("latency") or "")
    if not raw.endswith("s"):
        return 0
    try:
        return int(float(raw[:-1]) * 1000)
    except ValueError:
        return 0


def moment(entry):
    for key in ("timestamp", "receiveTimestamp"):
        found = entry.get(key)
        if isinstance(found, str) and found:
            stamp = found.replace("Z", "+00:00")
            try:
                return datetime.datetime.fromisoformat(stamp).timestamp()
            except ValueError:
                return None
    return None


def utc(seconds):
    return datetime.datetime.fromtimestamp(seconds, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def cold(path, gap_seconds):
    with open(path) as fh:
        entries = json.load(fh)
    if not isinstance(entries, list):
        entries = []
    requests = []
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        request = entry.get("httpRequest") if isinstance(entry.get("httpRequest"), dict) else {}
        if not request:
            continue
        at = moment(entry)
        if at is None:
            continue
        requests.append((at, latency_ms(request)))
    requests.sort()
    print("requests=%d" % len(requests))
    if not requests:
        return
    print("span=%s..%s" % (utc(requests[0][0]), utc(requests[-1][0])))
    steady = []
    for index, (at, ms) in enumerate(requests):
        if index == 0:
            steady.append(ms)
            continue
        silence = at - requests[index - 1][0]
        if silence >= gap_seconds:
            print("cold=%s %d" % (utc(at), ms))
        else:
            steady.append(ms)
    steady.sort()
    if steady:
        print("steady_ms=%d" % steady[len(steady) // 2])


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "service":
        service(argv[2])
        return 0
    if argv[1] == "config":
        config(argv[2], argv[3])
        return 0
    if argv[1] == "runs":
        runs(argv[2], argv[3] if len(argv) > 3 else "")
        return 0
    if argv[1] == "cold":
        cold(argv[2], int(argv[3]))
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 772 (the plan says so): the outage it reads is
# the owner's, and a coding agent's session is refused by the markers it
# carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 772. Run it yourself: bash scripts/live-checks/check772.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check772.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the run records it reads"
}

guard_project() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check reads the service in ${P} and in no other project, and never belong"
  fi
  case "${P}${L}${ACCOUNT:-}${SERVICE}" in
  *belong*) stop "the project, region, account or service this check names is belong: this check is never run there" ;;
  esac
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads the platform and the run records, not the run it is in: any build will do)"
  fact "project-region" PASS "${P}/${L} (installation ${INSTALLATION}, sandbox only; never belong)"
}

# ------------------------------------------------------------------- the steps
step_service() {
  gcloud_admin run services describe "$SERVICE" --region="$L" --format=json \
    >"$SCRATCH/service.json" 2>"$SCRATCH/service.err" || true
  if [ ! -s "$SCRATCH/service.json" ]; then
    die "the Cloud Run service ${SERVICE} could not be described in ${P}/${L} ($(last_line "$(cat "$SCRATCH/service.err" 2>/dev/null || true)")): a cold start is what this service does when it is deployed and idle, and there is none to read. Run 'gcloud run services describe $SERVICE --project $P --region $L' here yourself, or name the service with CHECK772_SERVICE"
  fi
  local out
  out=$(reader service "$SCRATCH/service.json") || die "the service document could not be read ($(last_line "$out"))"
  SERVICE_URL=$(field_of url "$out")
  REVISION=$(field_of revision "$out")
  MIN_INSTANCES=$(field_of min_instances "$out")
  fact service PASS "${SERVICE} in ${P}/${L} at ${SERVICE_URL:-<no url>}, latest ready revision ${REVISION:-unknown}, warm floor ${MIN_INSTANCES} instance(s): every row below is about this deployment"
}

# step_beat: R10's setting. A key the resolved config does not carry is the
# default, which D2985 and R10 both say is on.
step_beat() {
  fugaro config show --project "$PROJECT" --json >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" || true
  if [ ! -s "$SCRATCH/config.json" ]; then
    unmeasured beat "the resolved config could not be read ($(last_line "$(cat "$SCRATCH/config.err" 2>/dev/null || true)")): 'fugaro config show --project ${PROJECT} --json' failed. Read the key yourself: fugaro config show --project ${PROJECT} --json | grep -A2 ${BEAT_SETTING}"
    return 0
  fi
  local out
  out=$(reader config "$SCRATCH/config.json" "$BEAT_SETTING") || die "the config document could not be parsed ($(last_line "$out"))"
  BEAT_VALUE=$(field_of beat "$out")
  BEAT_SOURCE=$(field_of beat_source "$out")
  if [ "$BEAT_VALUE" = "<absent>" ]; then
    BEAT_VALUE=true
    fact beat PASS "${BEAT_SETTING} is absent from the resolved config of ${PROJECT}, which is the default: the beat is ON (D2985, R10). Its source is then nothing rather than a layer that set it"
    return 0
  fi
  case "$BEAT_VALUE" in
  true | false)
    fact beat PASS "${BEAT_SETTING} is ${BEAT_VALUE} in ${PROJECT} (${BEAT_SOURCE:-no source}). R10's switch works: the beat is a setting and not a constant, which is the owner's own addition to D2985"
    ;;
  *)
    fact beat FAIL "${BEAT_SETTING} is $(printf '%s' "$BEAT_VALUE" | head -c 60) in ${PROJECT} (${BEAT_SOURCE:-no source}), which is neither true nor false: the key is the one C2 reserves (internal/creds/route.go, WorkspaceBeatSetting) and a value the config cannot read as a switch is a beat nobody can turn off"
    ;;
  esac
}

# step_warm: the beat's effect, as the platform holds it. A beat that is on and
# a floor of zero is a beat that keeps nothing warm — D2985's own doubt answered
# in the deployment rather than in the setting.
step_warm() {
  if [ "$MIN_INSTANCES" = unknown ]; then
    unmeasured warm "the service document carries no minimum instance count in the places the platform has held it, so this run cannot say whether the beat keeps the service warm. Read it yourself: gcloud run services describe $SERVICE --project $P --region $L --format=json | grep -iE 'minInstance|minScale'"
    return 0
  fi
  if [ "$BEAT_VALUE" = true ] && [ "$MIN_INSTANCES" -lt 1 ]; then
    fact warm FAIL "the beat is ON (${BEAT_SETTING}) and the deployment's warm floor is ${MIN_INSTANCES} instances: nothing keeps the service warm, so every run after an idle spell pays a cold start whatever the setting says. Either the beat is not applied to this deployment — read what 'fugaro update' applied to the revision — or the floor has to be raised to one instance, which is what the beat is for (F16, D2985)"
    return 0
  fi
  if [ "$BEAT_VALUE" = false ]; then
    fact warm PASS "the beat is OFF (${BEAT_SETTING}, ${BEAT_SOURCE:-no source}) and the floor is ${MIN_INSTANCES} instances: nothing keeps the service warm by choice, which is the state the cold-start row below measures the cost of. U4 asks for that number, and this is the configuration it has to be taken in"
    return 0
  fi
  fact warm PASS "the beat is ON and the floor is ${MIN_INSTANCES} instance(s): the platform holds the service above zero, so the service is warm when a run reaches it and a cold start is the exception rather than the rule"
}

# step_cold: U4's figure. The requests that followed an idle gap of at least
# IDLE_GAP_SECONDS, and the steady latency beside them.
step_cold() {
  gcloud_admin logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE}\"" \
    --freshness="${WINDOW_MINUTES}m" --limit=2000 --format=json >"$SCRATCH/logs.json" 2>"$SCRATCH/logs.err" || true
  if [ ! -s "$SCRATCH/logs.json" ]; then
    unmeasured cold-start "the service's log could not be read ($(last_line "$(cat "$SCRATCH/logs.err" 2>/dev/null || true)")): reading it needs roles/logging.viewer on ${P}. Read it yourself: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $P --freshness=${WINDOW_MINUTES}m --format=json"
    return 0
  fi
  COLD_OUT=$(reader cold "$SCRATCH/logs.json" "$IDLE_GAP_SECONDS") || die "the log entries could not be parsed ($(last_line "$COLD_OUT"))"
  local requests span colds steady count
  requests=$(field_of requests "$COLD_OUT")
  span=$(field_of span "$COLD_OUT")
  steady=$(field_of steady_ms "$COLD_OUT")
  colds=$(lines_of cold "$COLD_OUT")
  count=$(printf '%s\n' "$colds" | grep -c . || true)
  if [ "${requests:-0}" -eq 0 ]; then
    unmeasured cold-start "the last ${WINDOW_MINUTES}m of the service's log hold no request at all (span ${span:-none}): a cold start is measured from the requests around it, and there are none. Send the traffic — a run, or a single call — and run this check again"
    return 0
  fi
  if [ "$count" -eq 0 ]; then
    if [ "$BEAT_VALUE" = true ]; then
      fact cold-start PASS "the last ${WINDOW_MINUTES}m hold ${requests} requests and NOT ONE followed a silence of ${IDLE_GAP_SECONDS}s or more (steady median ${steady:-unreadable}ms): with the beat on the service never went cold in this window, which is the beat doing what D2985 asks of it. U4's figure is then the steady latency — there is no cold start to time"
      return 0
    fi
    unmeasured cold-start "the last ${WINDOW_MINUTES}m hold ${requests} requests and none followed a silence of ${IDLE_GAP_SECONDS}s or more, so this window holds no cold start to time (steady median ${steady:-unreadable}ms). Leave the service alone for at least ${IDLE_GAP_SECONDS}s — the beat is ${BEAT_SETTING}=${BEAT_VALUE} — then make ONE call and run the check inside CHECK772_WINDOW_MINUTES=${WINDOW_MINUTES}"
    return 0
  fi
  if [ "$BEAT_VALUE" = true ]; then
    fact cold-start FAIL "the beat is ON and the last ${WINDOW_MINUTES}m hold ${count} request(s) that followed a silence of ${IDLE_GAP_SECONDS}s or more: the service went cold anyway, so the beat is not holding it (see the warm row). The worst cold start was $(printf '%s\n' "$colds" | tail -n 1), against a steady median of ${steady:-unreadable}ms"
    return 0
  fi
  fact cold-start PASS "the beat is OFF and the last ${WINDOW_MINUTES}m hold ${count} cold start(s) after a silence of at least ${IDLE_GAP_SECONDS}s: $(printf '%s\n' "$colds" | tr '\n' '; ')— against a steady median of ${steady:-unreadable}ms. U4's figure is that difference, per call, and it is what says whether the beat is worth its instance: record it in docs/workspace-service/reconciliation.md (U4, D2985)"
}

# step_outage_short: a run that met an outage shorter than the bound. The bound
# is the runner's own retry of the stage plus the gateway's single retry of a
# call, so a short outage is ridden out and the run must not have stopped.
step_outage_short() {
  if [ -z "$SHORT_RUN" ]; then
    unmeasured outage-short "CHECK772_SHORT_RUN was not set, so this run does not know which run met the short outage. Run one — a member's run while the workspace's service is unreachable for less than the bound — and pass its run id, or the last four characters of it"
    return 0
  fi
  if [ -z "${RUNS_OUT:-}" ]; then
    unmeasured outage-short "not read: the run listing was not read (see the outage-long row). Read it yourself: fugaro runs ls --json | grep -A8 ${SHORT_RUN}"
    return 0
  fi
  local out
  out=$(reader runs "$SCRATCH/runs.json" "$SHORT_RUN") || die "the run listing could not be parsed ($(last_line "$out"))"
  if [ "$(field_of found "$out")" != yes ]; then
    unmeasured outage-short "no run in the listing has an id ending in ${SHORT_RUN}, so this run cannot read what the short outage did to it. List them: fugaro runs ls --json | grep run_id"
    return 0
  fi
  local status halt_reason halt_at detail
  status=$(field_of status "$out")
  halt_reason=$(field_of halt_reason "$out")
  halt_at=$(field_of halt_at "$out")
  detail=$(field_of halt_detail "$out")
  if [ -n "$halt_reason" ]; then
    fact outage-short FAIL "the run $(field_of run_id "$out") met the short outage and ${halt_reason} stopped it (${halt_at}: $(printf '%s' "$detail" | head -c 200)): an outage SHORTER than the bound ends a run here, so the bound is shorter than its name — the run should have waited it out and finished. D2983's bound is read from this: either it has to grow, or the two halves of the wait (the gateway's retry of a call and the runner's retry of the stage) do not add up to what was measured at Check 772"
    return 0
  fi
  fact outage-short PASS "the run $(field_of run_id "$out") met the short outage and finished (status ${status:-unknown}, outcome $(field_of outcome "$out"), no halt): a client that cannot reach the workspace's service for less than the bound rides the outage out rather than stopping, which is what D2983 promises"
}

# step_outage_long: a run that met an outage longer than the bound. It has to
# halt, and the halt has to name the workspace's service.
step_outage_long() {
  fugaro runs ls --json >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err" || true
  RUNS_OUT=""
  if [ -s "$SCRATCH/runs.json" ]; then RUNS_OUT=yes; fi
  if [ -z "$LONG_RUN" ]; then
    unmeasured outage-long "CHECK772_LONG_RUN was not set, so this run does not know which run met the long outage. Run one — a member's run while the workspace's service is unreachable for longer than the bound — and pass its run id, or the last four characters of it"
    return 0
  fi
  if [ -z "$RUNS_OUT" ]; then
    unmeasured outage-long "the run listing could not be read ($(last_line "$(cat "$SCRATCH/runs.err" 2>/dev/null || true)")): 'fugaro runs ls --json' failed. Read the record yourself: fugaro runs show ${LONG_RUN} --json"
    return 0
  fi
  local out
  out=$(reader runs "$SCRATCH/runs.json" "$LONG_RUN") || die "the run listing could not be parsed ($(last_line "$out"))"
  if [ "$(field_of found "$out")" != yes ]; then
    unmeasured outage-long "no run in the listing has an id ending in ${LONG_RUN}. List them: fugaro runs ls --json | grep run_id"
    return 0
  fi
  local halt_reason halt_at detail marker waited=""
  halt_reason=$(field_of halt_reason "$out")
  halt_at=$(field_of halt_at "$out")
  detail=$(field_of halt_detail "$out")
  marker=$(field_of halt_workspace_refusal "$out")
  if [ -n "$OUTAGE_AT" ] && [ -n "$halt_at" ]; then
    waited=$(wait_phrase "$OUTAGE_AT" "$halt_at" || true)
  fi
  if [ -z "$halt_reason" ]; then
    fact outage-long FAIL "the run $(field_of run_id "$out") met the long outage and nothing stopped it (status $(field_of status "$out"), terminal $(field_of terminal "$out")): D2983 promises a bounded wait that ends in a halt naming the workspace's service, and a run that is still going after an outage longer than the bound has no bound at all. Every call it makes to an unreachable service is a call it will make again"
    return 0
  fi
  if [ -n "$marker" ]; then
    fact outage-long FAIL "the run halted (${halt_reason} at ${halt_at}) carrying the workspace's own refusal marker '${marker}': a marker is written from the header the workspace's service puts on a refusal it ANSWERED (workspace_refusal.go: stop, cap or settle). An outage is the service not answering at all, so a marker here means the halt is about a refusal rather than about the outage, and this run is not the one that measures the bound"
    return 0
  fi
  fact outage-long PASS "the run $(field_of run_id "$out") met the long outage and halted: ${halt_reason} at ${halt_at}${waited:+ ($waited)}${detail:+, detail '$(printf '%s' "$detail" | head -c 200)'}${marker:+, marker '$marker'}, with no workspace marker, which is what unreachable means. D2983's bound is what the wait from the outage to this halt measured: Check 772 is where it is recorded"
}

# wait_phrase FROM TO: how long the run waited, from the outage to the halt.
wait_phrase() {
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
if seconds < 0:
    print("the halt precedes the outage you named")
else:
    print("waited %dm%02ds" % (seconds // 60, seconds % 60))
' "$1" "$2"
}

# step_outage: what only the owner can do, said out loud.
step_outage() {
  fact outage-prompt INFO "not run by this check, and no agent may run any of them: force the workspace's service unreachable — scale its revision to zero, remove the member's invoke grant, or block it — for a spell SHORTER than the bound (one run, CHECK772_SHORT_RUN) and for a spell LONGER (another run, CHECK772_LONG_RUN), then put the service back. Write the moment the second outage began as CHECK772_OUTAGE_AT and both run ids, and run this check"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 772 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] gcloud run services describe ${SERVICE} --project ${P} --region ${L} --format=json — the revision and the warm floor"
  say "  [network, read] fugaro config show --project ${PROJECT} --json — the beat (${BEAT_SETTING}) as the resolved config holds it"
  say "  [network, read] gcloud logging read — the last ${WINDOW_MINUTES}m of the service's requests, for the ones after a silence of ${IDLE_GAP_SECONDS}s"
  say "  [network, read] fugaro runs ls --json — the two runs the outage was forced across"
  say "  [not run] the outage itself and the two runs: the owner's, never a script's"
  say "  [nothing created] no revision, no instance, no setting, no run; the scratch directory is removed at the end"
  for key in fugaro-version project-region xtrace service beat warm cold-start outage-short outage-long outage-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no service was described, no config was read and no log was listed"
}

BLOCK_KEYS=(fugaro-version project-region xtrace dry-run service beat warm cold-start
  outage-short outage-long outage-prompt restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 772 RESULT\n' >&3
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
  printf 'CHECK 772 RESULT END (exit %s)\n' "$status" >&3
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
P=fugaro-dev
L=us-east5
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

SERVICE=${CHECK772_SERVICE:-fugaro-workspace-credentials}
INSTALLATION=${CHECK772_INSTALLATION:-fugaro}
WINDOW_MINUTES=${CHECK772_WINDOW_MINUTES:-180}
BEAT_SETTING=${CHECK772_BEAT_SETTING:-agent.workspace_beat}
IDLE_GAP_SECONDS=${CHECK772_IDLE_GAP_SECONDS:-600}
PROJECT=${CHECK772_PROJECT:-fugaro}
SHORT_RUN=${CHECK772_SHORT_RUN:-}
LONG_RUN=${CHECK772_LONG_RUN:-}
OUTAGE_AT=${CHECK772_OUTAGE_AT:-}
DRY_RUN=${CHECK772_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK772_KEEP_SCRATCH:-}
RUNS_OUT=""
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check describes the service and reads its log with that account's own roles. Run 'gcloud auth login' first"
  [ "$WINDOW_MINUTES" -gt 0 ] || die "CHECK772_WINDOW_MINUTES is ${WINDOW_MINUTES}: the window is minutes of the service's log, so it has to be a positive number"
  [ "$IDLE_GAP_SECONDS" -gt 0 ] || die "CHECK772_IDLE_GAP_SECONDS is ${IDLE_GAP_SECONDS}: the gap is a silence in seconds, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "checking the workspace's service cold, the beat's setting, and the outage runs"
  step_service
  step_beat
  step_warm
  step_cold
  step_outage_long
  step_outage_short
  step_outage
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
