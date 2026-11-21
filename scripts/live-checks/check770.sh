#!/usr/bin/env bash
# Check 770 — the load: 100 concurrent runs across at least two members, on the
# workspace's credentials service.
#
# Plan task WS20 writes it (docs/workspace-service/implementation-plan.md §8);
# no task of that plan runs it. What it settles is the reconciliation's O1 and
# D2986: R9's first values — a maximum of 10 instances and a concurrency of 40
# per instance on the workspace's Cloud Run service — are a guess until this
# check measures the service at 100 concurrent runs, and git traffic rides the
# same service from the first release (D2984), so pushes and clones are measured
# beside the model calls.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. The load itself is the owner's: 100
# concurrent runs across two members, launched with `fugaro run` and watched, is
# a billable act that no script may start on its own. What this script measures
# is the evidence that load leaves, and the service it leaves it on:
#
#   * the deployed revision's own limits — `max_instance_count` and the
#     container concurrency as the PLATFORM holds them, which is what R9 states
#     as 10 and 40 and what nothing in this tree can read but the platform;
#   * the service's own request log over the window the load ran in: how many
#     requests, how many DISTINCT instances answered, the statuses (a 429 or a
#     503 is the platform having no capacity for the call, which is exactly what
#     the load is for) and the slowest one;
#   * the workspace's meter node — the reservations still held for calls that
#     never settled, which is the one way a load leaves a leak behind
#     (budget.WorkspaceReservationTTL, internal/budget/workspace.go).
#
# It creates NOTHING in the cloud and deletes NOTHING. Every read is a
# `describe`, a log listing, a config read or a Realtime Database GET, and it
# makes no request to the service at all — the load's own traffic is the
# reading.
#
# Usage:      bash scripts/live-checks/check770.sh
#             bash scripts/live-checks/check770.sh --dry-run   # print the plan
#             bash scripts/live-checks/check770.sh --help
# Needs:      bash, mktemp, python3 (the reader for the service document, the
#             log entries, the meter node and the config), curl (the database
#             reads, when a database URL is named) and gcloud (authenticated as
#             an OWNER of the project: the check describes the service, reads
#             its log and mints the database token) and fugaro (the guards and
#             the config read).
# Where:      the workspace's own installation, which for the dogfood
#             installation is fugaro-dev in us-east5, and both are hard-coded in
#             this file. A project, region, account or service naming belong is
#             refused by name.
# Creates:    nothing. The copies of the service document, the log entries and
#             the meter node this run reads sit in a scratch directory (mode
#             0700) removed from the exit trap even on a failure or Ctrl-C.
# Costs:      nothing billable. The load the owner already ran is the spend; the
#             check itself calls no model, launches no run and writes nothing.
# Runs:       by YOU, after the load — and no agent runs any step of it. Its own
#             test (check770_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 770 RESULT,
#             then CHECK 770 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure — a tool is missing, the service or its
#             log could not be read, or a row of the block has no reading. In
#             the block, a key the run never reached reads `FAIL — not reached`.
#
# Knobs (all CHECK770_*): DRY_RUN, KEEP_SCRATCH, SERVICE (the workspace's Cloud
# Run service's name), INSTALLATION (the installation the guards read),
# WINDOW_MINUTES (how far back the request log is read, 60 by default),
# RUNS (how many concurrent runs the load was, 100 by default), MEMBERS (how
# many members it spanned, 2 by default), MAX_INSTANCES and CONCURRENCY (R9's
# first values, 10 and 40), DATABASE_URL (the workspace's own Realtime Database;
# unset, the reservations row is not measured) and WORKSPACE (the workspace's
# name, for the meter's path).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run mints an
# access token for the database reads, and the shell it is started from may be a
# login shell whose trace ends up in a log.
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

# unmeasured: a row this run could NOT measure, for a reason that is not a
# finding about the platform. The row still reads FAIL in the block — it is not
# a pass and must not be read as one — and the check ends as one that could not
# measure (exit 2).
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
  command -v bash >/dev/null 2>&1 || die "Check 770 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 770 needs '$t' and it is not on PATH"
  done
  if [ -n "$DATABASE_URL" ]; then
    command -v curl >/dev/null 2>&1 || die "Check 770 needs 'curl' for the meter read (CHECK770_DATABASE_URL is set) and it is not on PATH"
  fi
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk770-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
TOKEN=""
SERVICE_URL=""
REVISION=""
MAX_INSTANCES_HELD=""
CONCURRENCY_HELD=""
MIN_INSTANCES=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk770.XXXXXXXX") || die "Check 770 cannot make a scratch directory"
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
    say "CHECK770_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: this check makes nothing, and the row says so rather than missing.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted: no job, no build, no container, no binding and no account. The load this check reads was the owner's own, and the platform's request log entries for it stay — they are not this check's to delete"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # Both rows are added before the verdict is taken, because the verdict is
  # taken from the facts: a directory this check could not remove is a FAIL, and
  # a block that printed `scratch-removed: FAIL` beside `END (exit 0)` would read
  # as a pass.
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

describe_service() {
  gcloud_admin run services describe "$SERVICE" --region="$L" --format=json >"$SCRATCH/service.json" 2>"$SCRATCH/service.err" || true
}

# database_get PATH DEST: one Realtime Database node, read with the owner's own
# token. The token is a header curl never prints, and it reaches no file.
database_get() { # database_get PATH DEST
  local path=$1 dest=$2
  curl -sS -o "$dest" -w '%{http_code}' \
    --max-time "$DATABASE_TIMEOUT" -H "Authorization: Bearer $TOKEN" \
    "${DATABASE_URL%/}/${path}.json" 2>"$SCRATCH/db.err" || true
}

# rtdb_key S: the spelling budget.Key (internal/budget/model.go) gives one path
# segment. A repository's route lives at config/policy/route/<slug> and a slug
# carries a `/`, which Key escapes as %2F — a check that spelled the raw slug
# would read a node of a fixed depth that no writer ever wrote.
rtdb_key() {
  local s=$1 out="" i c o
  for ((i = 0; i < ${#s}; i++)); do
    c=${s:i:1}
    case "$c" in
    % | . | '$' | '#' | '[' | ']' | /) printf -v o '%%%02X' "'$c"; out="${out}${o}" ;;
    *) out="${out}${c}" ;;
    esac
  done
  printf '%s' "$out"
}

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 770's reader: the service document, the service's request log and the
workspace's meter node. No credential and no token is in any of them.

service  argv: `gcloud run services describe --format=json`'s file. Prints the
         url, the latest ready revision, and the three limits as the platform
         holds them: minInstanceCount, maxInstanceCount and the container's
         concurrency. A limit the document does not carry prints `unknown`
         rather than a guess — the shapes differ between the Cloud Run one and
         the Knative one gcloud sometimes renders.
logs     argv: a log-entry listing's file. Prints requests= (the entries that
         carry an `httpRequest` — a plain application line the service wrote in
         the same window is NOT a request, and counting it as one would read a
         window with no load in it as a window with a load), entries= (how many
         entries the listing held, so a read that came back at the limit can be
         told from one that came back whole), instances= (how many DISTINCT
         instance ids answered a request), statuses= (one `status=NNN count`
         line per status), max_ms=, and a `slow=` line for the slowest request.
         An entry with no instance id contributes nothing to instances, which is
         a reading a reader has to be able to tell from "one instance answered".
meter    argv: the meter node's JSON, the month to read. Prints month=, settled
         (micros), reserved (micros), reservations= (how many are held) and one
         `stale=` line per reservation older than the TTL — a reservation no
         settle will ever find, which is the leak this row is about.
"""

import json
import sys
import time

WS_RESERVATION_TTL_MS = 6 * 60 * 60 * 1000  # budget.WorkspaceReservationTTL


def dig(doc, *path):
    cur = doc
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            return None
        cur = cur[step]
    return cur


def number_or_unknown(found):
    if isinstance(found, bool):
        return "unknown"
    if isinstance(found, int):
        return str(found)
    if isinstance(found, str) and found.strip().lstrip("-").isdigit():
        return found.strip()
    return "unknown"


def service(path):
    with open(path) as fh:
        doc = json.load(fh)
    print("url=" + (dig(doc, "status", "url") or ""))
    print("revision=" + (dig(doc, "status", "latestReadyRevisionName") or ""))
    min_count = None
    for path_ in (
        ("spec", "template", "scaling", "minInstanceCount"),
        ("spec", "template", "spec", "scaling", "minInstanceCount"),
        ("spec", "template", "metadata", "annotations", "autoscaling.knative.dev/minScale"),
    ):
        found = dig(doc, *path_)
        if found is not None:
            min_count = number_or_unknown(found)
            break
    max_count = None
    for path_ in (
        ("spec", "template", "scaling", "maxInstanceCount"),
        ("spec", "template", "spec", "scaling", "maxInstanceCount"),
        ("spec", "template", "metadata", "annotations", "autoscaling.knative.dev/maxScale"),
    ):
        found = dig(doc, *path_)
        if found is not None:
            max_count = number_or_unknown(found)
            break
    containers = dig(doc, "spec", "template", "spec", "containers") or []
    if not isinstance(containers, list):
        containers = []
    concurrency = "unknown"
    if containers and isinstance(containers[0], dict):
        found = containers[0].get("resources", {}).get("limits", {}).get("cpu")
        if found is not None:
            concurrency = "unknown"
        found = dig(containers[0], "concurrency")
        if found is None:
            found = dig(doc, "spec", "template", "metadata", "annotations", "run.googleapis.com/containerConcurrency")
        concurrency = number_or_unknown(found)
    print("min_instances=" + (min_count or "unknown"))
    print("max_instances=" + (max_count or "unknown"))
    print("concurrency=" + concurrency)


def logs(path):
    with open(path) as fh:
        entries = json.load(fh)
    if not isinstance(entries, list):
        entries = []
    requests = 0
    ids = []
    statuses = {}
    max_ms = 0
    slow = ""
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        request = entry.get("httpRequest")
        if not isinstance(request, dict):
            # Not a request: the service writes its own application lines into
            # the same revision log (credsvc's audit and refusal lines), and one
            # of those is not a call that was served.
            continue
        requests += 1
        status = str(request.get("status") or "")
        if status:
            statuses[status] = statuses.get(status, 0) + 1
        latency = str(request.get("latency") or "")
        ms = 0
        if latency.endswith("s"):
            try:
                ms = int(float(latency[:-1]) * 1000)
            except ValueError:
                ms = 0
        if ms > max_ms:
            max_ms = ms
            slow = "%s %s in %dms" % (status or "?", str(request.get("requestUrl") or ""), ms)
        for candidate in (
            dig(entry, "resource", "labels", "instance_id"),
            dig(entry, "resource", "labels", "instanceId"),
            dig(entry, "labels", "instanceId"),
            dig(entry, "labels", "instance_id"),
        ):
            if isinstance(candidate, str) and candidate and candidate not in ids:
                ids.append(candidate)
                break
    print("requests=%d" % requests)
    print("entries=%d" % len(entries))
    print("instances=%d" % len(ids))
    print("max_ms=%d" % max_ms)
    if slow:
        print("slow=" + slow)
    for status in sorted(statuses):
        print("status=%s count=%d" % (status, statuses[status]))


def meter(path, month, now_ms):
    with open(path) as fh:
        doc = json.load(fh)
    if not isinstance(doc, dict):
        doc = {}
    members = doc.get("members") if isinstance(doc.get("members"), dict) else {}
    reservations = doc.get("reservations") if isinstance(doc.get("reservations"), dict) else {}
    settled = 0
    for value in members.values():
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            settled += int(value)
    reserved = 0
    stale = []
    for call, one in reservations.items():
        if not isinstance(one, dict):
            continue
        amount = one.get("amount")
        if isinstance(amount, (int, float)) and not isinstance(amount, bool):
            reserved += int(amount)
        at = one.get("at")
        if not isinstance(at, (int, float)) or isinstance(at, bool):
            stale.append("%s holds no moment" % call)
            continue
        if now_ms - int(at) >= WS_RESERVATION_TTL_MS:
            age_h = (now_ms - int(at)) / 3600000.0
            member = one.get("member") or "no member"
            stale.append("%s held %.1fh ago (TTL 6h), for member %s" % (call, age_h, member))
    print("month=" + str(doc.get("month") or ""))
    print("settled=%d" % settled)
    print("reserved=%d" % reserved)
    print("reservations=%d" % len(reservations))
    for one in stale:
        print("stale=" + one)


def main(argv):
    if len(argv) < 2:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "service":
        service(argv[2])
        return 0
    if argv[1] == "logs":
        logs(argv[2])
        return 0
    if argv[1] == "meter":
        meter(argv[2], argv[3], int(argv[4]))
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 770 (the plan says so, and the block's rows
# are the owner's own reading of a real installation): a coding agent's session
# is refused by the markers it carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 770. Run it yourself: bash scripts/live-checks/check770.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check770.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would carry the access token it mints for the meter read"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token this check mints for the meter read"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`:
  # run anywhere else and this measures another installation's load, which is
  # why it stops rather than warns.
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check measures the service in ${P} and in no other project, and never belong"
  fi
  case "${P}${L}${ACCOUNT:-}${SERVICE}${WORKSPACE:-}" in
  *belong*) stop "the project, region, account, service or workspace this check names is belong: this check is never run there" ;;
  esac
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads the platform's service and its log, not a run: any build will do)"
  fact "project-region" PASS "${P}/${L} (installation ${INSTALLATION}, sandbox only; never belong)"
}

# ------------------------------------------------------------------- the steps
step_service() {
  describe_service
  if [ ! -s "$SCRATCH/service.json" ]; then
    die "the Cloud Run service ${SERVICE} could not be described in ${P}/${L} ($(last_line "$(cat "$SCRATCH/service.err" 2>/dev/null || true)")): this check measures the service the workspace's members call, which the workspace owner's 'fugaro update' deploys. Run 'gcloud run services describe $SERVICE --project $P --region $L' here yourself to read the error, or name the service with CHECK770_SERVICE"
  fi
  local out
  out=$(reader service "$SCRATCH/service.json") || die "the service document could not be read ($(last_line "$out"))"
  SERVICE_URL=$(field_of url "$out")
  REVISION=$(field_of revision "$out")
  MIN_INSTANCES=$(field_of min_instances "$out")
  MAX_INSTANCES_HELD=$(field_of max_instances "$out")
  CONCURRENCY_HELD=$(field_of concurrency "$out")
  fact service PASS "${SERVICE} in ${P}/${L} at ${SERVICE_URL:-<no url>}, latest ready revision ${REVISION:-unknown}; max instances ${MAX_INSTANCES_HELD}, concurrency ${CONCURRENCY_HELD}, min instances ${MIN_INSTANCES} — every row below is about this one service"
}

# step_limits: R9's first values, as the platform holds them. This is the row
# D2986 and O1 are about: the values are a guess until a load at 100 concurrent
# runs says whether they hold, and a platform holding something else means the
# guess was already corrected by hand and the design's sentence is what is stale.
step_limits() {
  if [ "$MAX_INSTANCES_HELD" = unknown ] || [ "$CONCURRENCY_HELD" = unknown ]; then
    unmeasured limits "the service document carries no max instance count or no container concurrency in the places the platform has held them, so this run cannot say whether R9's first values hold. Read them yourself: gcloud run services describe $SERVICE --project $P --region $L --format=json | python3 -m json.tool | grep -iE 'maxInstance|concurrency'"
    return 0
  fi
  if [ "$MAX_INSTANCES_HELD" = "$MAX_INSTANCES" ] && [ "$CONCURRENCY_HELD" = "$CONCURRENCY" ]; then
    fact limits PASS "the platform holds a maximum of ${MAX_INSTANCES_HELD} instances and a concurrency of ${CONCURRENCY_HELD} per instance, which is exactly R9's first values (10 and 40): a load that stays inside them does not correct them, and the window row below is what says whether it does"
    return 0
  fi
  fact limits FAIL "the platform holds a maximum of ${MAX_INSTANCES_HELD} instances and a concurrency of ${CONCURRENCY_HELD} per instance, and R9 states ${MAX_INSTANCES} and ${CONCURRENCY}: the values the design carries are not the ones deployed, so either the Terraform moved them and R9's sentence is stale, or this is another service. O1 says the owner settles it — correct R9 (docs/workspace-service/reconciliation.md) to the values the load needs, and move them in the same change"
}

# step_window: the load's own evidence. The request log over the window the
# owner ran the load in, read for the four things a load is measured for: how
# many calls there were, how many instances served them, what was refused, and
# how slow the worst one was.
step_window() {
  local since_ms waited=0 out="" cutoff entries
  # Filtered to the requests themselves: the service writes its own application
  # lines into the same revision log, and a window whose newest entries are all
  # application lines is a window with no load in it.
  gcloud_admin logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE}\" AND httpRequest.status>=0" \
    --freshness="${WINDOW_MINUTES}m" --limit="$LOG_LIMIT" --format=json >"$SCRATCH/logs.json" 2>"$SCRATCH/logs.err" || true
  if [ ! -s "$SCRATCH/logs.json" ]; then
    unmeasured load-window "the service's log entries could not be read ($(last_line "$(cat "$SCRATCH/logs.err" 2>/dev/null || true)")): reading them needs roles/logging.viewer on ${P}. Read them yourself: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $P --freshness=${WINDOW_MINUTES}m --format=json | head -50"
    return 0
  fi
  out=$(reader logs "$SCRATCH/logs.json") || die "the log entries could not be parsed ($(last_line "$out"))"
  entries=$(field_of entries "$out")
  if [ "${entries:-0}" -ge "$LOG_LIMIT" ]; then
    unmeasured load-window "the read came back at its own limit: ${entries} entries, which is the --limit=${LOG_LIMIT} this check asked for, so what the service logged OLDER than the newest ${LOG_LIMIT} of them was never read — and the refusals and the instance count a load leaves behind are exactly what the oldest of them would have shown. A row read from a truncated window is no reading at all. Narrow CHECK770_WINDOW_MINUTES to the minutes the load itself ran in, or read a shorter window by hand: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\" AND httpRequest.status>=0' --project $P --freshness=${WINDOW_MINUTES}m --limit=20000 --format=json | head -50"
    return 0
  fi
  local requests instances max_ms slow statuses=""
  requests=$(field_of requests "$out")
  instances=$(field_of instances "$out")
  max_ms=$(field_of max_ms "$out")
  slow=$(field_of slow "$out")
  statuses=$(printf '%s\n' "$out" | grep '^status=' | sed 's/^status=//' | tr '\n' ' ' || true)
  local refused=""
  case " $statuses " in
  *" 429 "*) refused="${refused}429 " ;;
  esac
  case " $statuses " in
  *" 503 "*) refused="${refused}503 " ;;
  esac
  if [ "${requests:-0}" -eq 0 ]; then
    unmeasured load-window "the last ${WINDOW_MINUTES}m of the service's log holds no request at all, so there is no load here to read: launch the load first, then run this check within the window. The load is CHECK770_RUNS=${RUNS} concurrent runs across CHECK770_MEMBERS=${MEMBERS} members, each a 'fugaro run' on a repository whose kind routes to the workspace, with the git pushes and clones of those runs riding the same service (D2984). Then: bash scripts/live-checks/check770.sh"
    return 0
  fi
  if [ -n "$refused" ]; then
    fact load-window FAIL "the last ${WINDOW_MINUTES}m hold ${requests} requests over ${instances} distinct instances (statuses: ${statuses}), and the platform REFUSED some of them with ${refused}: with ${MAX_INSTANCES_HELD} instances at a concurrency of ${CONCURRENCY_HELD} the service ran out of room, which is the correction D2986 and O1 ask for — raise the maximum instances or the concurrency (R9), or state the smaller load the values do hold. The slowest request was ${slow:-not readable} (max ${max_ms}ms)"
    return 0
  fi
  if [ "${requests:-0}" -lt "$RUNS" ]; then
    unmeasured load-window "the last ${WINDOW_MINUTES}m hold ${requests} requests over ${instances} distinct instances, which is fewer than the ${RUNS} a load at CHECK770_RUNS=${RUNS} concurrent runs leaves behind — a run makes many calls, so this window is not the load this check asks about. Run the load (or widen CHECK770_WINDOW_MINUTES) and run the check again"
    return 0
  fi
  if [ "$instances" -gt "$MAX_INSTANCES" ]; then
    fact load-window FAIL "the last ${WINDOW_MINUTES}m hold ${requests} requests over ${instances} distinct instances, and the platform states a maximum of ${MAX_INSTANCES_HELD}: more instances served than the service says it may run, which no reading of R9 can explain and which the load did not cause"
    return 0
  fi
  fact load-window PASS "the last ${WINDOW_MINUTES}m hold ${requests} requests over ${instances} distinct instances (statuses: ${statuses}), none refused: ${RUNS} concurrent calls across ${MEMBERS} members stayed inside ${MAX_INSTANCES_HELD} instances at a concurrency of ${CONCURRENCY_HELD}, so the load did not reach R9's limits. The slowest request was ${slow:-not readable} (max ${max_ms}ms)"
  # A window this wide is the load's, and the platform's own quota is where a
  # larger load would first show: it is a reading for the owner and no criterion.
  fact load-ceiling INFO "R9's limits are confirmed at CHECK770_MEMBERS members and CHECK770_RUNS runs. What the values would need at 200, at 500 or at the platform's instance quota is not measured here, and the project's Cloud Run quotas are: gcloud compute project-info describe --project $P (or the console's Quotas page) is where a larger load meets its first wall"
}

# step_reservations: the workspace's meter node. A reservation is taken before
# every paid call and settled after it, so reservations still held long after
# the load are calls whose settle never came — the one leak a load leaves.
step_reservations() {
  if [ -z "$DATABASE_URL" ] || [ -z "$WORKSPACE" ]; then
    unmeasured reservations "not read: CHECK770_DATABASE_URL and CHECK770_WORKSPACE have to name the workspace's own database and the workspace. The path is meter/workspace/<name>/<month> (budget.PathWorkspaceSpend). Read it by hand: gcloud auth print-access-token | curl -sS -H \"Authorization: Bearer \$(cat -)\" \"<database>/meter/workspace/<name>/<month>.json\""
    return 0
  fi
  local month path code out
  month=$(date -u +%Y-%m)
  path="meter/workspace/$(rtdb_key "$WORKSPACE")/${month}"
  TOKEN=$(gcloud auth print-access-token 2>"$SCRATCH/token.err" | head -n 1 || true)
  [ -n "$TOKEN" ] || die "gcloud could not mint an access token for the meter read ($(last_line "$(cat "$SCRATCH/token.err" 2>/dev/null || true)")): run 'gcloud auth login' here yourself and read the error"
  code=$(database_get "$path" "$SCRATCH/meter.json")
  case "$code" in
  200) ;;
  401 | 403)
    unmeasured reservations "the database refused the read with ${code}: reading another project's database needs the Firebase rules' own permission, which an operator of the workspace's project holds through its owner role. Read it from a shell signed in as the workspace's own owner, or with the check's own grant"
    return 0
    ;;
  *)
    unmeasured reservations "the meter node ${path} could not be read (HTTP ${code:-none}, $(last_line "$(cat "$SCRATCH/db.err" 2>/dev/null || true)")): CHECK770_DATABASE_URL names $(printf '%s' "$DATABASE_URL" | sed 's#^\(https://[^/]*\).*#\1#') and the node may simply be absent in a month with no settled call (a 404 is 'no node', which is not a failure). Read it by hand and settle which it is"
    return 0
    ;;
  esac
  out=$(reader meter "$SCRATCH/meter.json" "$month" "$(date -u +%s)000") || die "the meter node could not be parsed ($(last_line "$out"))"
  local settled reserved held stale
  settled=$(field_of settled "$out")
  reserved=$(field_of reserved "$out")
  held=$(field_of reservations "$out")
  stale=$(printf '%s\n' "$out" | sed -n 's/^stale=//p')
  if [ -n "$stale" ]; then
    fact reservations FAIL "${held} reservation(s) are held in ${month}: ${settled} micros settled and ${reserved} micros still reserved, and $(printf '%s' "$stale" | tr '\n' '; ')— a reservation past budget.WorkspaceReservationTTL is room no settle will ever find, so the cap is short by it until the month ends. Find the call: each reservation carries its member and the moment it was taken"
    return 0
  fi
  fact reservations PASS "${held} reservation(s) are held in ${month}, ${reserved} micros of room, none older than budget.WorkspaceReservationTTL (6h), and ${settled} micros have settled: every call the load paid for either settled or is still in flight"
}

# step_load: what only the owner can run, said out loud rather than left to be
# inferred. This check measures the load's evidence; it never starts one.
step_load() {
  fact load-prompt INFO "not run by this check: the load itself. CHECK770_RUNS=${RUNS} concurrent runs across CHECK770_MEMBERS=${MEMBERS} members, each 'fugaro run' on a repository whose kind the route sends to the workspace (fugaro workspace show <workspace> names the members and their repositories). The git pushes and clones of those runs ride the same service (D2984), so the load is model calls AND forge traffic. Run it, then run this check inside CHECK770_WINDOW_MINUTES=${WINDOW_MINUTES}"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 770 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project ${INSTALLATION} --all (it reads the installation's config, policy and billing, and the update check, so this is the dry run's one network read) and fugaro version"
  say "  [network, read] gcloud run services describe ${SERVICE} --project ${P} --region ${L} --format=json — the url, the revision and the three limits"
  say "  [network, read] gcloud logging read — the last ${WINDOW_MINUTES}m of the service's own REQUESTS (httpRequest entries only), at most ${LOG_LIMIT} of them: how many, over how many instances, refused how, and the slowest. A read that comes back at the limit measures nothing and says so"
  if [ -n "$DATABASE_URL" ] && [ -n "$WORKSPACE" ]; then
    say "  [network, read] one GET of <database>/meter/workspace/<workspace>/<month>.json — the reservations still held for calls that never settled"
  else
    say "  [not asked for] the meter read: CHECK770_DATABASE_URL and CHECK770_WORKSPACE are unset"
  fi
  say "  [not run] the load itself (CHECK770_RUNS=${RUNS} concurrent runs across ${MEMBERS} members): the owner's, never a script's"
  say "  [nothing created] no job, no build, no container, no binding, no account; the scratch directory is removed at the end"
  for key in fugaro-version project-region xtrace service limits load-window load-ceiling reservations load-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no service was described, no log was read and no database was touched"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region xtrace dry-run service limits
  load-window load-ceiling reservations load-prompt restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 770 RESULT\n' >&3
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
  printf 'CHECK 770 RESULT END (exit %s)\n' "$status" >&3
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
# Hard-coded, never `gcloud config get-value project`: a check that reads the
# project from the environment can be pointed at another installation by a stray
# config, and this one reads a real service's log and database.
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

SERVICE=${CHECK770_SERVICE:-fugaro-workspace-credentials}
INSTALLATION=${CHECK770_INSTALLATION:-fugaro}
WINDOW_MINUTES=${CHECK770_WINDOW_MINUTES:-60}
LOG_LIMIT=${CHECK770_LOG_LIMIT:-1000}
RUNS=${CHECK770_RUNS:-100}
MEMBERS=${CHECK770_MEMBERS:-2}
MAX_INSTANCES=${CHECK770_MAX_INSTANCES:-10}
CONCURRENCY=${CHECK770_CONCURRENCY:-40}
DATABASE_URL=${CHECK770_DATABASE_URL:-}
WORKSPACE=${CHECK770_WORKSPACE:-}
DATABASE_TIMEOUT=${CHECK770_DATABASE_TIMEOUT_SECONDS:-30}
DRY_RUN=${CHECK770_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK770_KEEP_SCRATCH:-}
# The impersonation-free identity: a value that does not look like one is
# refused rather than guessed at.
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check reads the service's log and the meter with that account's own roles. Run 'gcloud auth login' first"
  [ "$WINDOW_MINUTES" -gt 0 ] || die "CHECK770_WINDOW_MINUTES is ${WINDOW_MINUTES}: the window is minutes of the service's log, so it has to be a positive number"
  [ "$LOG_LIMIT" -gt 0 ] || die "CHECK770_LOG_LIMIT is ${LOG_LIMIT}: the limit is how many of the window's newest request entries are read, so it has to be a positive number"
  [ "$RUNS" -gt 0 ] || die "CHECK770_RUNS is ${RUNS}: the load is a number of concurrent runs, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "checking the workspace's service and the last ${WINDOW_MINUTES}m of its log"
  step_service
  step_limits
  step_window
  step_reservations
  step_load
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
