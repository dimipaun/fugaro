#!/usr/bin/env bash
# Check 511 — a request every N seconds keeps ONE service instance up.
#
# Plan task DS0.2 writes it and DS0.3 runs it (its steps are
# docs/secrets-brokering/dynamic-delivery/check511-steps.md). What it settles is
# the reconciliation's U4, which DS1.5's keep-warm heartbeat rests on: the
# credentials service keeps scaling to zero (D501) and D1934 keeps it that way,
# answering U4's cold start with a heartbeat from the runner. That answer is only
# an answer if a beat really does keep an instance alive, and the platform's
# instance idle behaviour is not something this repository can read anywhere but
# the platform.
#
# What it measures, and with what:
#
#   * the service D501 says scales to zero — `min_instance_count` as the
#     platform holds it, not as the Terraform in this tree writes it: the row is
#     the premise, because an instance kept up by configuration says nothing
#     about a heartbeat;
#   * a request every CHECK511_INTERVAL_SECONDS (15 by default: the beat the
#     runner's budget session already keeps, internal/budget/session.go) for
#     CHECK511_HOLD_SECONDS, each answered BY THE CONTAINER — the route is one
#     the service does not serve, so the container itself answers 404 with its
#     own route list, and an answer that is not that one is a refusal by the
#     front end rather than an instance serving;
#   * each of those requests' own latency: a request that pays a cold start
#     inside the window is the instance having been reaped DESPITE the beat,
#     which is U4 answered no at that interval;
#   * the instance ids Cloud Logging recorded for those requests: one id across
#     the window is one instance kept up, several ids are the beat having
#     measured nothing about a single instance;
#   * (only when asked for) the contrast an idle gap makes: what the same
#     request costs after CHECK511_IDLE_GAP_SECONDS of silence.
#
# It creates NOTHING in the cloud: no job, no build, no container, no binding
# and no account. Every request goes to a route the service does not serve (so
# no credential is read, no model is called and no budget is spent) and every
# read is a `describe` or a log listing.
#
# Usage:      bash scripts/live-checks/check511.sh
#             bash scripts/live-checks/check511.sh --dry-run   # print the plan
#             bash scripts/live-checks/check511.sh --help
# Needs:      bash, mktemp, python3 (the reader for the service document and the
#             log entries), curl, gcloud (authenticated as an OWNER of the
#             sandbox project: the check describes the service, mints an
#             identity token for itself and reads the service's log entries) and
#             fugaro (the doctor guard below).
# Where:      the fugaro-dev installation ONLY, and the project and region are
#             hard-coded in this file (never `gcloud config get-value project`).
#             A project, region or account naming belong is refused by name.
# Creates:    nothing in the cloud. It does make real requests to the service,
#             and the platform writes its own request log entries for them —
#             which is where the instance ids are read from, and which nobody
#             deletes.
# Costs:      nothing but wall clock: the window is CHECK511_HOLD_SECONDS long
#             (5 minutes by default) and the requests are answered by the
#             container's own 404. No job, no build and no model call.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check511_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — the check has nothing to
#             delete, and it says so in the restore row rather than leaving the
#             reader to assume it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 511 RESULT,
#             then CHECK 511 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure — a tool is missing, the service or its
#             logs could not be read, or a row of the block has no reading. In
#             the block, a key the run never reached reads `FAIL — not reached`,
#             which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value and no identity token
# reaches this script's stdout, its stderr, the block or any file it writes. The
# token the requests carry is minted into a shell variable and passed to curl in
# a header curl never prints, and every log entry read here is cut to an
# instance id and a count before it reaches the block.
#
# Knobs (all CHECK511_*): DRY_RUN, KEEP_SCRATCH, STAMP (the suffix every request
# this run makes carries, so its log entries are its own; the clock by default),
# SERVICE (the Cloud Run service's name), INTERVAL_SECONDS, HOLD_SECONDS,
# WARM_MAX_MS (the latency above which a request is read as a cold start),
# REQUEST_TIMEOUT_SECONDS, INSTANCE_IDS (0 or empty: do not read the log
# entries, and record the row as not asked for), LOG_WAIT_SECONDS,
# LOG_POLL_SECONDS, IDLE_GAP_SECONDS (0 by default: the contrast an idle gap
# makes is not asked for).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below
# carry an identity token, and the shell it is started from may be a login shell
# whose trace ends up in a log.
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
  command -v bash >/dev/null 2>&1 || die "Check 511 needs bash"
  local t
  for t in mktemp python3 curl gcloud fugaro seq; do
    command -v "$t" >/dev/null 2>&1 || die "Check 511 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk511-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
TOKEN=""
SERVICE_URL=""
MIN_INSTANCES=""
REVISION=""
WINDOW_REQUESTS=0

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk511.XXXXXXXX") || die "Check 511 cannot make a scratch directory"
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
    say "CHECK511_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: this check makes nothing, and the row says so rather than missing. It
# is in the block because every check's block has it, and because "nothing was
# created" is a claim a reader is owed: the requests it made to the service are
# in the platform's own log, which is not this check's to delete.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created: no job, no build, no container, no binding and no account — ${WINDOW_REQUESTS} request(s) went to a route the service does not serve, so no credential was read and no model was called. The platform's own request log entries for them stay, and are not this check's to delete"
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

# describe_service: the service as the PLATFORM holds it. Read once: every row
# below is about this one service and this one revision.
describe_service() {
  gcloud_admin run services describe "$SERVICE" --region="$L" --format=json >"$SCRATCH/service.json" 2>"$SCRATCH/service.err" || true
}

# probe N: ONE request, to a route the service does not serve, carrying this
# check's own stamp. It lands in the platform's request log under that stamp,
# which is how the instance ids below are read. Sets P_STATUS, P_MS and
# P_BODY_FILE.
probe() { # probe N
  # In two statements, not one `local`: a simple command's words are all
  # expanded before any of the assignments lands, so `local n=$1 url=...$n...`
  # reads an unbound `n` under `set -u`.
  local n=$1
  local url="${SERVICE_URL}/chk511/health?stamp=${STAMP}&n=${n}"
  P_BODY_FILE="$SCRATCH/probe.${n}.body"
  local timed
  timed=$(curl -sS -o "$P_BODY_FILE" -w '%{http_code} %{time_total}' \
    --max-time "$REQUEST_TIMEOUT" -H "Authorization: Bearer $TOKEN" "$url" 2>"$SCRATCH/probe.${n}.err" || true)
  P_STATUS=${timed%% *}
  [ -n "$P_STATUS" ] || P_STATUS=000
  P_MS=$(awk -v t="${timed#* }" 'BEGIN { printf "%d", (t + 0) * 1000 }')
}

# container_answered: whether the CONTAINER answered this probe. The route is
# one the service does not serve, so the container itself answers 404 with the
# list of routes it does serve (internal/credsvc/server.go's ServeHTTP). An
# answer that is not that one is the front end refusing the call — a different
# thing, and the row says which one it saw.
container_answered() {
  grep -qF 'this service serves POST /v1/messages' "$P_BODY_FILE" 2>/dev/null
}

body_head() { head -c 200 "$P_BODY_FILE" 2>/dev/null || true; }

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# field_of NAME TEXT: the value of a NAME= line the reader printed.
field_of() { # field_of NAME TEXT
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 511's reader: two readings, and no credential in either of them.

service  argv: `gcloud run services describe --format=json`'s file. Prints the
         url, the minimum instance count the platform holds, and the latest
         ready revision. The count sits in more than one place across the
         service's own shape and the Knative one gcloud sometimes renders, so
         each is tried in turn and `min_instances=unknown` is printed when none
         of them carries a number — never a guess.
logs     argv: a log-entry listing's file, the stamp this run's requests carry.
         Prints `entries=` (how many entries belong to this run's stamp) and one
         `instance=` line per entry that carries an instance id. The id is
         looked for in the places the platform has carried it; an entry with
         none contributes nothing, and `instances=0` is printed when the
         entries this project writes carry no id at all — which is a reading a
         reader has to be able to tell from "one instance".
"""

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
    url = dig(doc, "status", "url") or ""
    print("url=" + url)
    rev = dig(doc, "status", "latestReadyRevisionName") or ""
    print("revision=" + rev)
    count = None
    for path_ in (
        ("spec", "template", "scaling", "minInstanceCount"),
        ("spec", "template", "spec", "scaling", "minInstanceCount"),
        ("spec", "template", "metadata", "annotations", "autoscaling.knative.dev/minScale"),
        ("metadata", "annotations", "run.googleapis.com/minScale"),
    ):
        found = dig(doc, *path_)
        if isinstance(found, int):
            count = found
            break
        if isinstance(found, str) and found.strip().lstrip("-").isdigit():
            count = int(found.strip())
            break
    print("min_instances=%s" % ("unknown" if count is None else count))
    print("name=" + (dig(doc, "metadata", "name") or ""))


def logs(path, stamp):
    with open(path) as fh:
        entries = json.load(fh)
    if not isinstance(entries, list):
        entries = []
    mine = 0
    ids = []
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        request = entry.get("httpRequest") if isinstance(entry.get("httpRequest"), dict) else {}
        url = str(request.get("requestUrl") or "")
        if stamp not in url:
            continue
        mine += 1
        candidates = [
            dig(entry, "resource", "labels", "instance_id"),
            dig(entry, "resource", "labels", "instanceId"),
            dig(entry, "labels", "instanceId"),
            dig(entry, "labels", "instance_id"),
            dig(entry, "labels", "run.googleapis.com/instance_id"),
        ]
        for candidate in candidates:
            if isinstance(candidate, str) and candidate:
                if candidate not in ids:
                    ids.append(candidate)
                break
    print("entries=%d" % mine)
    print("distinct=%d" % len(ids))
    for one in ids:
        print("instance=" + one)


def main(argv):
    if len(argv) < 2:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "service":
        service(argv[2])
        return 0
    if argv[1] == "logs":
        logs(argv[2], argv[3])
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check511.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would carry the identity token the check mints for its own requests"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the identity token this check mints for its requests"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 511 makes real requests to a real service and reads its logs: run
  # anywhere else and it measures another installation's, which is why this
  # stops rather than warns.
  local doctor_out v
  doctor_out=$(fugaro doctor --project fugaro --all || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check measures the service in ${P} and in no other project, and never belong"
  fi
  case "${P}${L}${ACCOUNT:-}" in
  *belong*) stop "the project, region or account this check names is belong: this check is never run there" ;;
  esac
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  # Not a release gate, unlike the checks that measure a run: what it reads is
  # the platform's own service and log, so any build of fugaro does here.
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads the platform's service and its log, not a run: any build will do)"
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

# ------------------------------------------------------------------- the steps
step_service() {
  describe_service
  if [ ! -s "$SCRATCH/service.json" ]; then
    die "the Cloud Run service ${SERVICE} could not be described in ${P}/${L} ($(last_line "$(cat "$SCRATCH/service.err" 2>/dev/null || true)")): this check measures the service D501 keeps scaling to zero and D1934's heartbeat keeps warm, and it is deployed by 'fugaro update' on every installation. Run 'gcloud run services describe $SERVICE --project $P --region $L' here yourself to read the error, or name the service with CHECK511_SERVICE"
  fi
  local out
  out=$(reader service "$SCRATCH/service.json") || die "the service document could not be read ($(last_line "$out"))"
  SERVICE_URL=$(field_of url "$out")
  MIN_INSTANCES=$(field_of min_instances "$out")
  REVISION=$(field_of revision "$out")
  if [ -z "$SERVICE_URL" ]; then
    die "the service document carries no status.url: the check has no address to send its requests to. Run 'gcloud run services describe $SERVICE --project $P --region $L --format=json' here yourself and read its status"
  fi
  fact service PASS "${SERVICE} in ${P}/${L} at ${SERVICE_URL}, latest ready revision ${REVISION:-unknown} — every row below is about this one service"
}

step_scale_to_zero() {
  case "$MIN_INSTANCES" in
  0)
    fact scale-to-zero PASS "min_instance_count is 0 in ${P}: the platform keeps no instance for this service between requests, which is D501's shape and the premise of every row below — an instance that survives here survives because requests keep arriving"
    ;;
  '' | unknown)
    unmeasured scale-to-zero "the service document carries no minimum instance count in any of the places the platform has held it, so the check cannot say whether the instance the window below sees is kept up by configuration or by the requests. Read it yourself: gcloud run services describe $SERVICE --project $P --region $L --format='value(spec.template.scaling.minInstanceCount)'"
    ;;
  *)
    fact scale-to-zero FAIL "min_instance_count is ${MIN_INSTANCES} in ${P}: the platform keeps at least one instance up whatever the traffic is, so no reading below can be attributed to a heartbeat and D501 (the scale to zero this work is built on) does not hold for this service. The rows below are still measured, and their warmth is the CONFIGURATION's"
    ;;
  esac
}

# step_token: the identity token the requests carry. It is minted for the
# account gcloud is using — an operator of this installation, whose invoker grant
# on the service is what iam.tf writes — and it is never printed.
step_token() {
  TOKEN=$(gcloud auth print-identity-token --audiences="$SERVICE_URL" 2>"$SCRATCH/token.err" | head -n 1 || true)
  [ -n "$TOKEN" ] || die "gcloud could not mint an identity token for ${SERVICE_URL} ($(last_line "$(cat "$SCRATCH/token.err" 2>/dev/null || true)")): the check's requests have to pass the service's IAM front end the way a run's do. Run 'gcloud auth print-identity-token --audiences=$SERVICE_URL' here yourself to read the error"
}

# step_first: the request that opens the window, and the one that says whether
# the check can reach the container at all. The service has been idle until now
# on an installation nobody is using, so this request is usually the one that
# pays a cold start — which is the contrast the window is read against, and it
# is recorded as a reading rather than as a criterion.
step_first() {
  probe first
  case "$P_STATUS" in
  404)
    if ! container_answered; then
      die "the service answered 404 with a body this check does not recognise as its container's ($(body_head)): the route /chk511/health and the container's answer to it are how this check tells an instance serving from the front end refusing. Read it yourself: curl -i -H \"Authorization: Bearer <token>\" $SERVICE_URL/chk511/health"
    fi
    fact container-answers PASS "the service answered the unserved route /chk511/health with the container's OWN 404 — the body lists the routes it does serve (this service serves POST /v1/messages, ...) — so the requests below reach an instance and not only the front end"
    ;;
  403 | 401)
    die "the service refused the check's identity token with ${P_STATUS}: the requests have to pass roles/run.invoker on it the way a run's do, and that grant is on the installation's operators (deploy/terraform/gcp/modules/installation/iam.tf). Check the account gcloud is using ('gcloud config get-value account') holds it, and that the token was minted for ${SERVICE_URL}"
    ;;
  000)
    die "the request to ${SERVICE_URL} did not complete ($(last_line "$(cat "$SCRATCH/probe.first.err" 2>/dev/null || true)")): the check needs to reach the service from here"
    ;;
  *)
    fact container-answers FAIL "the unserved route /chk511/health answered ${P_STATUS}: $(body_head) — the container answers 404 on it, so this is the service refusing the call rather than an instance serving it, and the window below would measure the front end and not an instance"
    ;;
  esac
  local first=$P_MS
  FIRST_MS=$first
  fact idle-cost INFO "the request that opened the window took ${first}ms, after whatever idle period this installation had before the check ran — that is the one request in this run which could pay a cold start (the beat's own requests follow, and the heartbeat-warm row reads their cost)"
  say "the opening request took ${first}ms (a cold start shows here if this service had been idle)"
}

# step_window: the beat. One request every INTERVAL seconds for HOLD seconds,
# each read for two things: whether an INSTANCE answered it, and what it cost.
# A request inside the window that pays a cold start is the instance having been
# reaped despite the beat; a request that is refused is the front end having no
# instance to send it to. A request that NEVER COMPLETED is neither: status 000
# is curl's own --max-time or a connection that never came up, which says nothing
# about an instance — so the rows here read as ones this run could NOT measure
# rather than as U4 answered no, exactly as the same failure on the opening
# request does in step_first.
step_window() {
  local i n=$WINDOW_REQUESTS first_bad="" unfinished=0 unfinished_why="" answered=0 measured=0 max=0 min=-1 slow="" ms total=0
  for i in $(seq 1 "$n"); do
    sleep "$INTERVAL"
    probe "w$i"
    ms=$P_MS
    if [ "$P_STATUS" = 000 ]; then
      # Not an answer, and not a latency either: a request that timed out would
      # otherwise put the whole timeout into the min/mean/max read below.
      unfinished=$((unfinished + 1))
      if [ -z "$unfinished_why" ]; then
        unfinished_why="request ${i} of ${n} never completed (curl: $(last_line "$(cat "$SCRATCH/probe.w${i}.err" 2>/dev/null || true)"))"
      fi
    else
      measured=$((measured + 1))
      total=$((total + ms))
      if [ "$min" -lt 0 ] || [ "$ms" -lt "$min" ]; then min=$ms; fi
      if [ "$ms" -gt "$max" ]; then max=$ms; fi
      if [ "$P_STATUS" = 404 ] && container_answered; then
        answered=$((answered + 1))
      elif [ -z "$first_bad" ]; then
        first_bad="request ${i} of ${n} answered ${P_STATUS} after ${ms}ms: $(body_head)"
      fi
      if [ "$ms" -gt "$WARM_MAX_MS" ] && [ -z "$slow" ]; then
        slow="request ${i} of ${n} took ${ms}ms (over CHECK511_WARM_MAX_MS=${WARM_MAX_MS}), which is a cold start inside the window"
      fi
    fi
    say "  beat ${i}/${n}: ${P_STATUS} in ${ms}ms"
  done
  local mid=0
  if [ "$measured" -gt 0 ]; then mid=$((total / measured)); fi
  # The count the transport failure is reported with, and what it costs the rows
  # below: a request that never completed is not one an instance answered.
  local lost=""
  if [ "$unfinished" -gt 0 ]; then
    lost=" ${unfinished} of the window's ${n} requests never completed at all, which says nothing about an instance: read that as this run not having measured them, not as U4 answered no."
  fi

  if [ -n "$first_bad" ]; then
    fact heartbeat-answered FAIL "${first_bad}.${lost} A request inside the window that the container did not answer is the platform having no instance up for it: the beat did not keep one, which is U4 answered no at ${INTERVAL}s"
  elif [ "$unfinished" -gt 0 ]; then
    unmeasured heartbeat-answered "${unfinished_why}.${lost} A request that never completes is either the platform having no instance to send it to — which IS U4's question — or this machine not reaching ${SERVICE_URL} at all, and this check cannot tell those apart from here: the ${answered} request(s) that did complete were each answered by the container itself. Rerun it (CHECK511_REQUEST_TIMEOUT_SECONDS=${REQUEST_TIMEOUT}, CHECK511_INTERVAL_SECONDS=${INTERVAL}, CHECK511_HOLD_SECONDS=${HOLD} are the knobs), or read the service's own request log for this run's stamp"
  else
    fact heartbeat-answered PASS "${answered} requests, one every ${INTERVAL}s for $((n * INTERVAL))s, were each answered by the container itself (404 on the unserved route, with its own route list): the front end always had an instance to send them to, so nothing in the window was refused for lack of one"
  fi

  local warmth="the window's latencies were min ${min}ms, mean ${mid}ms, max ${max}ms over the ${measured} of its ${n} requests that completed, with a cold start read as anything over CHECK511_WARM_MAX_MS=${WARM_MAX_MS}"
  if [ "$measured" -eq 0 ]; then
    warmth="not one of the window's ${n} requests completed, so there are no window latencies to read"
  fi
  case "$MIN_INSTANCES" in
  0)
    if [ "$measured" -lt "$n" ]; then
      unmeasured heartbeat-warm "${warmth}${lost} U4's claim is about the WHOLE window, and this run read ${measured} of its ${n} requests, so nothing here says an instance was up throughout it"
    elif [ -z "$slow" ]; then
      fact heartbeat-warm PASS "${warmth}: every request in the window was answered WITHOUT a cold start, so a request every ${INTERVAL}s kept an instance up for the whole $((n * INTERVAL))s — U4's answer at this interval, which is the beat the runner's budget session already keeps (internal/budget/session.go's 15s). The opening request before the window took ${FIRST_MS}ms"
    else
      fact heartbeat-warm FAIL "${slow}; ${warmth}. The instance was reaped despite the beat, so a request every ${INTERVAL}s does NOT keep one up: DS1.5's interval is not enough and this returns to the owner before the heartbeat is written"
    fi
    ;;
  *)
    fact heartbeat-warm INFO "${warmth}. NOT read as a criterion: min_instance_count is ${MIN_INSTANCES}, so an instance is kept up by configuration and this warmth cannot be attributed to the beat — the scale-to-zero row above is the finding"
    ;;
  esac
}

# step_idle_gap: what the same request costs after silence. Only when asked
# for: the platform reaps an idle instance on its own schedule, and a gap that
# is shorter than that schedule says nothing. A reading either way, never a
# criterion.
step_idle_gap() {
  if [ "$IDLE_GAP" -le 0 ]; then
    fact idle-gap INFO "not asked for (CHECK511_IDLE_GAP_SECONDS=0): set it to a gap longer than the platform's own idle window (900 is 15 minutes) to measure what silence costs, which is the contrast the beat prevents"
    return 0
  fi
  say "idling for ${IDLE_GAP}s before the contrast request"
  sleep "$IDLE_GAP"
  probe gap
  local gap=$P_MS reading
  reading="after ${IDLE_GAP}s of silence the same request took ${gap}ms (the window's max was a cold start over ${WARM_MAX_MS}ms or less; the opening request took ${FIRST_MS}ms)"
  if [ "$gap" -gt "$WARM_MAX_MS" ]; then
    fact idle-gap INFO "${reading}: the gap cost a cold start, so silence longer than ${IDLE_GAP}s DOES reap the instance and the beat at ${INTERVAL}s is what prevents it"
  else
    fact idle-gap INFO "${reading}: the instance survived the gap, so at ${IDLE_GAP}s of silence the platform's own idle window keeps the instance up on its own — the beat still covers the longer gap a run's own idle can be"
  fi
}

# step_instance_ids: which instances answered. One instance id across the
# window's own log entries is one instance kept up (U4's own words); several are
# the beat having measured nothing about a single instance; none readable is a
# row this run could not measure, and it names what to read by hand.
step_instance_ids() {
  if [ -n "$INSTANCE_IDS_OFF" ]; then
    fact instance-ids INFO "not asked for (CHECK511_INSTANCE_IDS=0): the window's rows above stand on the requests themselves, and this row is their supporting reading from Cloud Logging"
    return 0
  fi
  # Every name is given a value here: `local a=0 out` leaves `out` unset, and
  # reading it under `set -u` ends the run.
  local waited=0 out="" entries="" distinct=""
  while :; do
    gcloud_admin logging read \
      "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE}\" AND httpRequest.requestUrl:*${STAMP}*" \
      --freshness=30m --limit=200 --format=json >"$SCRATCH/logs.json" 2>"$SCRATCH/logs.err" || true
    if [ -s "$SCRATCH/logs.json" ]; then
      out=$(reader logs "$SCRATCH/logs.json" "$STAMP") || out=""
      entries=$(field_of entries "$out")
      if [ "${entries:-0}" -ge "$((WINDOW_REQUESTS + 1))" ]; then break; fi
    fi
    [ "$waited" -lt "$LOG_WAIT_SECONDS" ] || break
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
  if [ -z "$out" ]; then
    unmeasured instance-ids "the service's log entries could not be read or parsed ($(last_line "$(cat "$SCRATCH/logs.err" 2>/dev/null || true)")): reading them needs roles/logging.viewer on ${P}. Read them yourself: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\" AND httpRequest.requestUrl:*${STAMP}*' --project $P --freshness=30m --format=json | head -50"
    return 0
  fi
  entries=$(field_of entries "$out")
  distinct=$(field_of distinct "$out")
  if [ "${entries:-0}" -lt "$((WINDOW_REQUESTS + 1))" ]; then
    unmeasured instance-ids "Cloud Logging held ${entries:-0} entries for this run's stamp after ${waited}s of waiting, and this run made $((WINDOW_REQUESTS + 1)) requests: the log lags the platform, so the instances that answered cannot be read yet. Rerun with a longer CHECK511_LOG_WAIT_SECONDS, or run that log listing yourself"
    return 0
  fi
  if [ "${distinct:-0}" -eq 0 ]; then
    unmeasured instance-ids "the ${entries} log entries this project writes for the service carry no instance id (looked in resource.labels.instance_id, labels.instanceId and the run.googleapis.com spellings), so this run cannot say how many instances answered: read the entries yourself and look for the id — gcloud logging read 'resource.labels.service_name=\"${SERVICE}\" AND httpRequest.requestUrl:*${STAMP}*' --project $P --freshness=30m --format=json"
    return 0
  fi
  if [ "$distinct" -eq 1 ]; then
    fact instance-ids PASS "the ${entries} log entries for this run's own requests name ONE instance ($(field_of instance "$out")): a request every ${INTERVAL}s kept a single instance up for the whole window, which is U4's reading rather than only its timing"
  else
    fact instance-ids FAIL "the ${entries} log entries for this run's own requests name ${distinct} different instances — $(printf '%s\n' "$out" | sed -n 's/^instance=//p' | tr '\n' ' '): the requests were served by more than one instance, so nothing in this run measured ONE instance being kept up, and DS1.5's heartbeat would have to keep several up rather than one"
  fi
}

# -------------------------------------------------------------------- the plan
plan() {
  local key n=$WINDOW_REQUESTS
  say "CHECK 511 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project fugaro --all (it reads the installation's config, policy and billing, and the update check, so this is the dry run's one network read) and fugaro version"
  say "  [network, read] gcloud run services describe ${SERVICE} --project ${P} --region ${L} --format=json — the url, the minimum instance count and the latest ready revision"
  say "  [network, read] gcloud auth print-identity-token --audiences=<the service's url> — minted, never printed"
  say "  [network] $((n + 1)) requests to <url>/chk511/health?stamp=${STAMP} — one to open the window and then one every ${INTERVAL}s for $((n * INTERVAL))s; the service does not serve the route, so its container answers 404 itself, no credential is read and no model is called"
  if [ "$IDLE_GAP" -gt 0 ]; then
    say "  [network] one more after ${IDLE_GAP}s of silence (CHECK511_IDLE_GAP_SECONDS)"
  fi
  if [ -z "$INSTANCE_IDS_OFF" ]; then
    say "  [network, read] gcloud logging read — the instance ids of those requests' own log entries"
  else
    say "  [not asked for] the log listing: CHECK511_INSTANCE_IDS=0"
  fi
  say "  [nothing created] no job, no build, no container, no binding, no account; the scratch directory is removed at the end"
  for key in fugaro-version project-region xtrace service scale-to-zero container-answers \
    idle-cost heartbeat-warm heartbeat-answered instance-ids idle-gap; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no request was made, no token was minted and no service was touched"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region xtrace dry-run service scale-to-zero
  container-answers idle-cost heartbeat-warm heartbeat-answered instance-ids
  idle-gap restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 511 RESULT\n' >&3
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
  printf 'CHECK 511 RESULT END (exit %s)\n' "$status" >&3
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
# config, and this one measures a real service and reads its logs.
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

SERVICE=${CHECK511_SERVICE:-fugaro-credentials}
STAMP=${CHECK511_STAMP:-$(date -u +%Y%m%d%H%M%S)}
# The beat is the runner's own: budget.Session's default HeartbeatEvery is 15
# seconds (internal/budget/session.go), and F13 has the keep-warm ride it.
INTERVAL=${CHECK511_INTERVAL_SECONDS:-15}
HOLD=${CHECK511_HOLD_SECONDS:-300}
WARM_MAX_MS=${CHECK511_WARM_MAX_MS:-1000}
REQUEST_TIMEOUT=${CHECK511_REQUEST_TIMEOUT_SECONDS:-30}
INSTANCE_IDS=${CHECK511_INSTANCE_IDS:-1}
# CHECK511_INSTANCE_IDS=0 (or an empty value) turns the log listing off: the
# window's rows stand on the requests themselves, and a reader who cannot read
# the project's logs still gets them.
INSTANCE_IDS_OFF=""
case "$INSTANCE_IDS" in "" | 0) INSTANCE_IDS_OFF=yes ;; esac
LOG_WAIT_SECONDS=${CHECK511_LOG_WAIT_SECONDS:-90}
LOG_POLL_SECONDS=${CHECK511_LOG_POLL_SECONDS:-10}
IDLE_GAP=${CHECK511_IDLE_GAP_SECONDS:-0}
DRY_RUN=${CHECK511_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK511_KEEP_SCRATCH:-}
WINDOW_REQUESTS=$((HOLD / (INTERVAL > 0 ? INTERVAL : 1)))
[ "$WINDOW_REQUESTS" -ge 2 ] || WINDOW_REQUESTS=2
FIRST_MS=-1
# The impersonation-free identity: the check's requests run as the account
# gcloud is using, and a value that does not look like one is refused rather
# than guessed at.
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check sends its requests as that account, and the service's invoker grant is on this installation's operators. Run 'gcloud auth login' first"
  [ "$INTERVAL" -gt 0 ] || die "CHECK511_INTERVAL_SECONDS is ${INTERVAL}: the window is requests every INTERVAL seconds, so it has to be a positive number of seconds"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "the beat: ${WINDOW_REQUESTS} requests, one every ${INTERVAL}s after the opening one"
  step_service
  step_scale_to_zero
  step_token
  step_first
  step_window
  step_idle_gap
  step_instance_ids
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
