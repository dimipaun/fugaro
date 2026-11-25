#!/usr/bin/env bash
# Check 573 — a redelivered sandbox event leaves the ledger unchanged, and a
# replayed one outside the delivery window is refused.
#
# Plan task P9.4 of docs/portal/implementation-plan.md writes this check, and the
# number is that work's own (its "Checks this plan takes (570–579)"). What it
# settles is read from the outside and nowhere else: **F16/D2192** — a delivery is
# verified, then stored, then processed, and a redelivery of an event the ledger
# already holds does not unmake it, nor is it applied twice — and F16's window:
# `VerifyDelivery` (`ee/keyservice/paddleevents.go`) refuses a delivery whose own
# `ts` is more than five minutes (`DeliveryWindow`) from the service's clock, and
# a body that failed verification never reaches the store at all.
#
# What it measures, and how: **the check signs its own deliveries.** Paddle
# sandbox cannot be made to redeliver on command, so the check takes the
# deployment's own sandbox webhook secret (in a 0600 file, from Secret Manager,
# by you), writes two Paddle-shaped `transaction.completed` bodies of its own and
# signs them Paddle's own way — HMAC-SHA256 of `ts:<body>` under that secret — and
# posts them at the webhook route. Every id it uses carries the instant this run
# started, so a second run's deliveries are its own events and not the first
# run's.
#
#   * the API origin, with one read that carries NO sign-in: `GET
#     /api/workspaces` answers 401 `unauthenticated`;
#   * the parked queue (`GET /api/ops/events?state=parked`, read with the
#     operations member's own token), which is where one of the check's events is
#     visible afterwards, with its id, its state and the instant it was received:
#     this is the ledger the rows below compare, and the check reads it BEFORE it
#     delivers anything. A body whose transaction did not begin with a checkout
#     this API created is PARKED there (F15); an invented transaction is exactly
#     that, so parking is the state the check's events have;
#   * **the delivery**: the fresh body A, signed with a `ts` of now, answers 200
#     with no body, and the queue then holds exactly ONE record for A's id, in
#     state `parked`;
#   * **the redelivery** (D2192): the same bytes and the SAME signature again —
#     one second later, so the two instants are told apart — answer 200 with no
#     body, and the queue still holds exactly ONE record for A: its state did not
#     move, and its `received_at` did (the second delivery was received and stored
#     as received, and it did not unmake what the first one did). One record and
#     an unchanged state is the whole of what "the ledger is unchanged" means
#     here;
#   * **the window**: body B, signed with a `ts` half an hour old, answers 200
#     with no body — a forger and a stale delivery get the same answer (§5.2) —
#     and the queue holds NO record for B at all, which is the difference: a
#     refused delivery never reaches the store;
#   * **the control that makes that reading the window's and not the signature's**:
#     the SAME body B signed with a `ts` of now is stored, and the queue then holds
#     exactly one record for it. Same body, same secret, same scheme — the only
#     difference between the two deliveries is the age of the timestamps, so the
#     5-minute window is what refused the first.
#
# What it creates: **two parked events per run**, one for A and one for B, whose
# ids the block names. They are what the check's own evidence is, and they are
# left in the operations queue on purpose: ending one is a WRITE with a sign-in
# from the last five minutes (`POST /api/ops/events/resolutions` with
# `{"id":"<id>","decision":"dismiss"}`), which the check does not make on your
# behalf. Parse the `restore` row for the two ids and dismiss them when you are
# done reading them.
#
# Usage:      bash scripts/live-checks/check573.sh --project <p> --token-file <file> \
#               --webhook-secret-file <file>
#             bash scripts/live-checks/check573.sh --project <p> --token-file <file> \
#               --webhook-secret-file <file> --dry-run
#             bash scripts/live-checks/check573.sh --help
# Needs:      bash, mktemp, python3 (the signature is computed and every document
#             is read with it), curl and sleep. No `fugaro` command runs, and no
#             `gcloud` one: the secret is read by YOU, once, with the line in the
#             register's section.
# Where:      your own machine and the STAGING deployment (`fugaro-staging`)
#             first. `fugaro-prod` is refused unless `--production` is given,
#             because production is only ever measured on purpose; a value naming
#             belong is refused by name; and so is a coding agent's session.
# Costs:      nothing billable: four deliveries at the webhook route and a handful
#             of reads.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check573_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory — which holds the signature configs and the
#             two bodies — removed from the exit trap even on a failed assertion
#             or a Ctrl-C. What the project keeps is the two parked events above.
# Output:     progress on stderr, ONE block on stdout headed CHECK 573 RESULT,
#             then CHECK 573 RESULT END (exit N) — to copy back into the
#             register's Check 573 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold — a ledger that moved under a redelivery, a stale delivery that
#             was stored — or a guard stopped the check. 2: a row could not be
#             measured for a reason that is this check's own setup — a secret file
#             that is not 0600, a token that is not an operations member's, a body
#             this service parked as something else. A key the run never reached
#             reads `FAIL — not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: not the webhook secret, not one byte of the
# signature over a body, and not the ID token. The secret is read from its file
# into python3's own stdin, never onto a command line; each signature travels in a
# 0600 curl config file; and the block carries names and numbers only — event ids
# this check minted, states, a received instant, an HTTP status.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK573_PROJECT, CHECK573_TOKEN_FILE, CHECK573_WEBHOOK_SECRET_FILE,
# CHECK573_API (the portal's origin; the staging default is the staging site's
# own domain, `app.staging.fugaro.dev`, and the production one is
# `app.fugaro.dev` — P0.1, P9.2), CHECK573_STALE_SECONDS (1800: how old
# the refused delivery's own stamp is, well past the service's five minutes),
# CHECK573_PRODUCTION, CHECK573_DRY_RUN, CHECK573_KEEP_SCRATCH and
# CHECK573_CURL_SECONDS (60).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads the
# deployment's webhook secret and signs with it, and a trace of that is a copy of
# a signature and of the command that made it.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to the
# saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory. A stop right after an `unmeasured` row
# ends the run as one that could not measure (exit 2): the difference matters to
# whoever reads the block, because 1 is a finding about the release and 2 is a
# run that never got to measure it.
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

FACTS_FILE=""
LAST_ROW=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2), whether it stopped
# there or ran to the end.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  LAST_ROW=unmeasured
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  local t
  for t in bash mktemp python3 curl sleep; do
    command -v "$t" >/dev/null 2>&1 || die "Check 573 needs '$t' and it is not on PATH"
  done
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

PROJECT=""
API=""
TOKEN_FILE=""
SECRET_FILE=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
STALE_SECONDS=1800
STARTED=0
ID_A=""
ID_B=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check573.XXXXXX") || die "Check 573 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK573_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK573_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — it is a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ]; then
    local failed=no key
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(xtrace agent-session project belong api token secret queue delivery redelivery window replay restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 573 RESULT\n' >&3
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
  printf 'CHECK 573 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------- the readers
# Every document below is read with python3 rather than by pattern, so a field
# this check does not know about cannot be mistaken for one it does. Each reader
# prints `key=value` lines and exits 0 even on a document it cannot read: the
# caller reads the empty key as "the document did not answer", which is a row
# about the portal rather than this check's crash.

fault_fields() { # fault_fields FILE — the API's one refusal shape
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
fault = doc.get("error") or {}
print("code=%s" % (fault.get("code") or ""))
print("message=%s" % (fault.get("message") or ""))
PY
}

# queue_fields FILE ID — one parked event read out of the operations queue: how
# many the queue holds under that id (0 or 1, and the reader says so rather than
# the caller counting), the state it is in, and the instant it was received as a
# unix second. The envelope is not assumed: the route answers an array today, and
# a reader that only knew that shape would call a wrapped one empty.
queue_fields() { # queue_fields FILE ID
  python3 - "$1" "$2" <<'PY'
import datetime, json, sys


def parse_instant(value):
    """An RFC3339 instant as a unix second, or "" for one that is not one."""
    if not isinstance(value, str) or not value.strip():
        return ""
    try:
        return int(datetime.datetime.fromisoformat(value.strip().replace("Z", "+00:00")).timestamp())
    except Exception:
        return ""


try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("shape=unreadable")
    raise SystemExit(0)
events = doc
if isinstance(doc, dict):
    events = next((v for v in doc.values() if isinstance(v, list)), None)
if not isinstance(events, list):
    print("shape=unreadable")
    raise SystemExit(0)
mine = [e for e in events if isinstance(e, dict) and e.get("id") == sys.argv[2]]
print("shape=queue")
print("count=%d" % len(mine))
if mine:
    print("state=%s" % (mine[0].get("state") or ""))
    print("received=%s" % parse_instant(mine[0].get("received_at")))
    print("queued=%d" % len(events))
else:
    print("queued=%d" % len(events))
PY
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check573.sh --project <p> --token-file <file> --webhook-secret-file <file> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of a signature is a copy of one"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a signature, a curl config or the secret to the terminal"
}

# guard_agent_session: this check SIGNS deliveries with the deployment's own
# webhook secret and writes two parked events into the operations queue. That is
# the owner's act on the owner's deployment.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 573 is yours to run at your own terminal, on the staging deployment — it signs deliveries with the deployment's webhook secret and leaves two parked events in the queue, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_file MODE KEY PATH WHAT: one 0600, one-line file, checked the same way for
# the token and for the secret. The value is never read into a fact.
guard_file() { # guard_file KEY PATH WHAT
  local key=$1 path=$2 what=$3
  if [ ! -f "$path" ]; then
    unmeasured "$key" "the $what file $path is not there: this check reads the $what out of a file of its own. Nothing about the portal was read"
    stop "the $what file $path is not there, so none of this check's rows could be measured"
  fi
  local mode
  mode=$(python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$path" 2>/dev/null || true)
  if [ "$mode" != 600 ]; then
    unmeasured "$key" "the $what file $path is mode ${mode:-unknown}, not 600: this check will not carry a $what out of a world-readable file"
    stop "the $what file $path is mode ${mode:-unknown} and has to be 0600. Nothing was read from it"
  fi
  local bytes lines
  bytes=$(wc -c <"$path" | tr -d ' ')
  lines=$(wc -l <"$path" | tr -d ' ')
  if [ "$bytes" -eq 0 ] || [ "$lines" -gt 1 ]; then
    unmeasured "$key" "the $what file $path holds $bytes byte(s) on $lines line(s): it carries one value on one line and nothing else"
    stop "the $what file $path is empty or holds more than one line. Nothing was read from it"
  fi
  fact "$key" PASS "$path is one line at mode 0600 (the value itself is never read into a fact, a command line, a curl config's name or the block)"
}

guard_inputs() {
  local all="${PROJECT:-}${API:-}${TOKEN_FILE:-}${SECRET_FILE:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: the rows below WRITE two events into a real deployment's operations queue, and production is only ever measured on purpose. Nothing was read"
  fi
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 570 to 575 run on fugaro-staging first, so read this block as a production run by choice"
  else
    fact project PASS "$PROJECT is the Firebase project this check was given (the staging deployment's own is fugaro-staging), and it is not fugaro-prod, which is refused without --production"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact api PASS "$API (the portal's origin; a real run holds it to one read that answers the API's own unauthenticated refusal)"
    fact token PASS "$TOKEN_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact secret PASS "$SECRET_FILE (a real run reads its mode and size; the value is opened by the signing reader and reaches no command line, no fact and no terminal)"
    fact queue PASS "planned: GET $API/api/ops/events?state=parked — the ledger the rows below compare"
    return 0
  fi
  guard_file token "$TOKEN_FILE" "ID token"
  guard_file secret "$SECRET_FILE" "webhook secret"
}

# ------------------------------------------------------------------ the steps
# step_api: one read with NO sign-in. The API's own 401 `unauthenticated` is what
# says the origin in front of this check is the portal's API: a marketing page, a
# hosting provider's 404 or somebody else's host answers something else, and every
# row below would then be about a deployment this check cannot name.
step_api() {
  local code
  code=$(curl -sS --max-time "$CURL_SECONDS" -o "$SCRATCH/anonymous.body" -w '%{http_code}' "$API/api/workspaces" 2>"$SCRATCH/anonymous.curl.err" || true)
  code=$(printf '%s' "$code" | tr -d '\n')
  if [ "$code" != 401 ]; then
    unmeasured api "GET $API/api/workspaces with no sign-in answered ${code:-nothing} rather than 401: the origin this check was given does not answer as the portal's API (the check reads the API's own unauthenticated refusal, which is 401 with code 'unauthenticated'). Nothing below this row is about the deployment you meant. If you are on staging, pass --api with the staging site's own origin"
    stop "the API origin $API did not answer 401 to an unauthenticated read, so this check cannot say which deployment it is reading"
  fi
  fault_fields "$SCRATCH/anonymous.body" >"$SCRATCH/anonymous.txt"
  local fault_code
  fault_code=$(field "$SCRATCH/anonymous.txt" code)
  if [ "$fault_code" != unauthenticated ]; then
    unmeasured api "GET $API/api/workspaces with no sign-in answered 401 with code '${fault_code:-none}' rather than 'unauthenticated': the origin answers, and not with this API's own refusal shape"
    stop "the API's unauthenticated refusal carries code '${fault_code:-none}', not 'unauthenticated'"
  fi
  fact api PASS "$API answers GET /api/workspaces with 401 and code 'unauthenticated' when no sign-in is carried, so the origin in front of this check is the portal's API"
}

# api_call METHOD PATH KEY [BODY] — one call to the portal API with the
# operations member's own sign-in. The token goes into a 0600 curl config and
# never onto the command line.
api_call() {
  local method=$1 path=$2 key=$3 body=${4:-}
  local cfg="$SCRATCH/$key.curlrc"
  ( umask 077; printf 'header = "Authorization: Bearer %s"\n' "$(head -n 1 "$TOKEN_FILE")" >"$cfg" ) || die "the token file could not be read into a 0600 config"
  local args=(-sS --max-time "$CURL_SECONDS" --config "$cfg" -o "$SCRATCH/$key.body" -w '%{http_code}' -X "$method")
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  args+=("$API$path")
  curl "${args[@]}" >"$SCRATCH/$key.code" 2>"$SCRATCH/$key.curl.err" || true
  tr -d '\n' <"$SCRATCH/$key.code"
}

# queue_read KEY ID: one read of the parked queue, filtered for the event ID and
# kept as KEY.txt. A refusal
# here is about the token — the queue is the operations area, which needs an
# operations member who signed in with Google or Microsoft (D2207) — and it is a
# row that could not be measured rather than a finding.
queue_read() { # queue_read KEY ID
  local key=$1 id=$2 code
  code=$(api_call GET "/api/ops/events?state=parked" "$key")
  if [ "$code" != 200 ]; then
    fault_fields "$SCRATCH/$key.body" >"$SCRATCH/$key.fault"
    local fault_code message
    fault_code=$(field "$SCRATCH/$key.fault" code)
    message=$(field "$SCRATCH/$key.fault" message)
    unmeasured queue "GET /api/ops/events?state=parked answered ${code:-nothing} ('${fault_code:-none}': '$message'): this read needs an operations member's sign-in, and the token in $TOKEN_FILE is not one (an email-link sign-in is refused here by D2207's rule — Check 572 measures that — so the token has to be one signed in with Google or Microsoft). The ledger the rows below compare could not be read"
    stop "the operations queue could not be read with this token (${code:-no answer}, '${fault_code:-none}'), so no row of this check has a ledger to be about"
  fi
  queue_fields "$SCRATCH/$key.body" "$id" >"$SCRATCH/$key.txt"
  if [ "$(field "$SCRATCH/$key.txt" shape)" != queue ]; then
    unmeasured queue "GET /api/ops/events?state=parked answered 200 with a document this check cannot read ($(last_lines "$SCRATCH/$key.body")): the queue's own shape is not what came back"
    stop "the operations queue answered a document this check cannot read as a queue"
  fi
}

# step_queue: the ledger BEFORE. Nothing of this run is in it, which is what makes
# the count after a delivery mean what the rows below say it means: the record is
# one, and it is the one this delivery made.
step_queue() {
  queue_read queue "$ID_A"
  local queued b
  queued=$(field "$SCRATCH/queue.txt" queued)
  b=$(queued_ids)
  if [ "$b" != 0 ]; then
    unmeasured queue "the parked queue already holds $b event(s) under this run's own ids ($ID_A, $ID_B): the ids carry the instant this run started, so a record under one is another run of this check with the same instant, and this run's deliveries would be measured against it. Wait a second and run it again"
    stop "this run's ids are already in the parked queue, so a delivery under one would be a redelivery from the start"
  fi
  fact queue PASS "the operations queue answers 200 and holds $queued parked event(s), none under this run's own ids ($ID_A, $ID_B): the ledger these rows compare is empty of this check"
}

# queued_ids: how many records the queue holds under this run's two ids.
queued_ids() {
  local a b
  queue_fields "$SCRATCH/queue.body" "$ID_A" >"$SCRATCH/queue.a"
  queue_fields "$SCRATCH/queue.body" "$ID_B" >"$SCRATCH/queue.b"
  a=$(field "$SCRATCH/queue.a" count)
  b=$(field "$SCRATCH/queue.b" count)
  printf '%s' "$((a + b))"
}

# write_body FILE EVENT_ID TRANSACTION_ID: a Paddle-shaped `transaction.completed`
# body signed by this check. Its transaction is one no checkout of this API
# created, which is what makes the API PARK it (F15) — and parking is the state
# the rows below read. The ids carry the instant this run started.
write_body() { # write_body FILE EVENT_ID TRANSACTION_ID
  python3 - "$1" "$2" "$3" <<'PY'
import datetime, json, sys

now = datetime.datetime.now(datetime.timezone.utc)
period = now + datetime.timedelta(days=30)
body = {
    "event_id": sys.argv[2],
    "event_type": "transaction.completed",
    "occurred_at": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
    "data": {
        "id": sys.argv[3],
        "status": "completed",
        "origin": "web",
        "customer_id": "ctm-%s" % sys.argv[3],
        "currency_code": "USD",
        "custom_data": {
            "org_id": "chk573-check-organisation",
            "workspace_id": "chk573-check-workspace",
        },
        "items": [{"price_id": "pri-chk573-check", "quantity": 1}],
        "billing_period": {
            "starts_at": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "ends_at": period.strftime("%Y-%m-%dT%H:%M:%SZ"),
        },
        "totals": {"grand_total": "1000"},
    },
}
with open(sys.argv[1], "w") as out:
    json.dump(body, out, separators=(",", ":"))
PY
}

# sign_body BODY_FILE STAMP OUT: Paddle's own scheme — HMAC-SHA256 of `ts:<body>`
# under the deployment's webhook secret. python3 opens the secret's own file: the
# value reaches no command line, no environment, no fact and no terminal, and what
# comes out is the header over one body, which goes straight into a 0600 curl
# config.
sign_body() { # sign_body BODY_FILE STAMP OUT
  python3 - "$SECRET_FILE" "$1" "$2" "$3" <<'PY'
import hashlib, hmac, sys

secret = open(sys.argv[1], "rb").read().strip()
body = open(sys.argv[2], "rb").read()
stamp = sys.argv[3].encode()
mac = hmac.new(secret, stamp + b":" + body, hashlib.sha256).hexdigest()
header = "ts=%s;h1=%s" % (sys.argv[3], mac)
with open(sys.argv[4], "w") as out:
    out.write(header)
PY
}

# deliver KEY BODY_FILE STAMP: one signed delivery at the webhook route. The
# signature travels in a 0600 curl config file and never on the command line. The
# status lands in $SCRATCH/KEY.code and the body in $SCRATCH/KEY.body.
deliver() { # deliver KEY BODY_FILE STAMP
  local key=$1 body_file=$2 stamp=$3
  sign_body "$body_file" "$stamp" "$SCRATCH/$key.sig" || die "the signature over $key could not be computed"
  local cfg="$SCRATCH/$key.sig.curlrc"
  ( umask 077; { printf 'header = "Paddle-Signature: '; cat "$SCRATCH/$key.sig"; printf '"\n'; } >"$cfg" ) \
    || die "the signature could not be read into a 0600 config"
  curl -sS --max-time "$CURL_SECONDS" --config "$cfg" -H 'Content-Type: application/json' \
    --data-binary "@$body_file" -o "$SCRATCH/$key.body" -w '%{http_code}' \
    "$API/api/paddle/webhook" >"$SCRATCH/$key.code" 2>"$SCRATCH/$key.curl.err" || true
  rm -f "$SCRATCH/$key.sig"
  tr -d '\n' <"$SCRATCH/$key.code"
}

# answer_ok KEY WHAT: the answer a delivery gets — 200 and no body, which is the
# one answer a refusal and an acceptance have in common (§5.2). It prints nothing;
# the caller's row says what the ledger then holds, which is the only thing that
# tells the two apart.
answer_is_200() { # answer_is_200 KEY
  local key=$1 code
  code=$(tr -d '\n' <"$SCRATCH/$key.code")
  [ "$code" = 200 ] || return 1
  [ ! -s "$SCRATCH/$key.body" ] || return 1
  return 0
}

step_delivery() {
  write_body "$SCRATCH/a.json" "$ID_A" "txn-$ID_A" || die "the check's own body could not be written"
  local code
  code=$(deliver a "$SCRATCH/a.json" "$(now_epoch)")
  if ! answer_is_200 a; then
    unmeasured delivery "the delivery answered ${code:-nothing} with a body of $(wc -c <"$SCRATCH/a.body" | tr -d ' ') byte(s) rather than 200 with no body (curl said: $(last_lines "$SCRATCH/a.curl.err")): the webhook's own answer is 200 and no body for everything it accepts and everything it refuses, so this is neither — and the secret in $SECRET_FILE may not be the deployment's (a delivery signed with another secret is refused in exactly this answer's shape only if the route is reading one)"
    stop "the delivery did not answer the webhook's own 200-with-no-body, so nothing below it could be read"
  fi
  queue_read delivery "$ID_A"
  local count state received
  count=$(field "$SCRATCH/delivery.txt" count)
  state=$(field "$SCRATCH/delivery.txt" state)
  received=$(field "$SCRATCH/delivery.txt" received)
  if [ "$count" != 1 ]; then
    unmeasured delivery "the queue holds ${count:-no} record(s) under the delivered event's id ($ID_A) rather than one: the body was signed with the secret in $SECRET_FILE and the route answered 200, so either the secret is not this deployment's (a body that does not verify is answered the same way and stored by nothing) or the store did not take it. Nothing below compares a ledger this row could name"
    stop "the delivered event is not one record in the operations queue, so the redelivery below has nothing to be measured against"
  fi
  if [ "$state" != parked ]; then
    unmeasured delivery "the queue answers state '$state' for $ID_A, and this route answers PARKED events alone: this check's transaction is one no checkout of this API created, which is what F15 parks — a state that is not parked means this read met something that is not the queue of parked events, and the rows below would compare the wrong ledger"
    stop "the record the queue answered for the delivered event is in state '$state', so this is not the parked queue"
  fi
  printf '%s' "$received" >"$SCRATCH/received.1"
  fact delivery PASS "the signed delivery is stored as one event in the operations queue, in state 'parked' (F15: its transaction did not begin with a checkout this API created), received at epoch $received — the route answered 200 with no body, which is the answer a refusal gets too, so the ledger is what says this one landed"
}

step_redelivery() {
  local first code
  first=$(cat "$SCRATCH/received.1")
  # The two deliveries are a second apart so that the instants are told apart: a
  # redelivery that moved the state would be visible either way, and one that did
  # not reach the store would not be — and this is a reading, not a guess.
  sleep 1
  code=$(deliver a2 "$SCRATCH/a.json" "$(now_epoch)")
  if ! answer_is_200 a2; then
    unmeasured redelivery "the redelivery answered ${code:-nothing} rather than 200 with no body (curl said: $(last_lines "$SCRATCH/a2.curl.err")): the same bytes and the same signature answered 200 the first time, and an answer that changed is not this check's to read"
    stop "the redelivery did not answer as the delivery did, so the ledger was not compared"
  fi
  queue_read redelivery "$ID_A"
  local count state received
  count=$(field "$SCRATCH/redelivery.txt" count)
  state=$(field "$SCRATCH/redelivery.txt" state)
  received=$(field "$SCRATCH/redelivery.txt" received)
  if [ "$count" = 0 ]; then
    fact redelivery FAIL "the record for $ID_A is gone from the parked queue after the redelivery: the first delivery parked it and the second one did not leave it parked, so a retry unmade what the delivery before it did, which is the one thing F16/D2192 says a redelivery must not do"
    stop "the redelivery moved the event out of the state the first delivery put it in, so the ledger was not unchanged"
  fi
  if [ "$count" != 1 ]; then
    fact redelivery FAIL "the queue answers $count records under $ID_A: the store is keyed by an event's own id, so one event is one record, and a second one under the same id is a ledger this check cannot read as unchanged"
    stop "the redelivery left more than one record under one event id"
  fi
  if [ "$received" = "$first" ]; then
    fact redelivery FAIL "the record for $ID_A still carries the instant of the FIRST delivery (epoch $received): the redelivery was answered 200 and left no trace of itself, so what its answer licensed is not known — the store writes a delivery as it arrived, and an instant that did not move means this second body never reached it"
    stop "the redelivery left no trace in the record, so this row cannot say what the answer was about"
  fi
  fact redelivery PASS "the same bytes and the same signature delivered again leave the ledger unchanged: still exactly ONE record for $ID_A, still 'parked', and its received instant moved from epoch $first to epoch $received — the second delivery was received and stored as received, and it applied nothing (F16/D2192)"
}

step_window() {
  write_body "$SCRATCH/b.json" "$ID_B" "txn-$ID_B" || die "the check's own body could not be written"
  local stale code
  stale=$((STARTED - STALE_SECONDS))
  code=$(deliver b-stale "$SCRATCH/b.json" "$stale")
  if ! answer_is_200 b-stale; then
    unmeasured window "the stale delivery answered ${code:-nothing} rather than 200 with no body (curl said: $(last_lines "$SCRATCH/b-stale.curl.err")): everything this route refuses is refused in that one shape, and an answer that is not it means the read did not reach the webhook"
    stop "the stale delivery did not answer as the webhook answers, so the window was not measured"
  fi
  queue_read window "$ID_B"
  local count
  count=$(field "$SCRATCH/window.txt" count)
  if [ "$count" != 0 ]; then
    fact window FAIL "a body signed with a timestamp $STALE_SECONDS seconds old is IN the ledger ($count record(s) under $ID_B): the delivery window refuses a delivery whose own stamp is more than five minutes from the service's clock, and a body that failed verification is stored by nothing (F16)"
    stop "the stale delivery was stored, so the window is not the fence it is documented as"
  fi
  fact window PASS "a body signed with a timestamp $STALE_SECONDS seconds old is refused by the delivery window: the route answers 200 with no body (the one answer a refusal and an acceptance share) and the ledger holds NOTHING under its id ($ID_B)"
  # The control: the SAME body, the same secret, the same scheme, a stamp of now.
  code=$(deliver b-fresh "$SCRATCH/b.json" "$(now_epoch)")
  if ! answer_is_200 b-fresh; then
    unmeasured window "the same body with a stamp of now answered ${code:-nothing} rather than 200 with no body (curl said: $(last_lines "$SCRATCH/b-fresh.curl.err")): without this half the row above could be a body the route refuses for any reason at all"
    stop "the fresh delivery of the same body did not answer as the webhook answers, so the window reading has no control"
  fi
  queue_read replay "$ID_B"
  local fresh_state fresh_count
  fresh_count=$(field "$SCRATCH/replay.txt" count)
  fresh_state=$(field "$SCRATCH/replay.txt" state)
  if [ "$fresh_count" != 1 ]; then
    fact replay FAIL "the SAME body delivered with a stamp of now left ${fresh_count:-no} record(s) under $ID_B: the two deliveries differ in the age of their timestamps and in nothing else, so a body that does not store when it is fresh is not one the delivery window refused — and the window row above is not proven by it, because what refused the stale delivery could be anything this route refuses"
    stop "the control delivery did not store, so the window reading cannot be attributed to the window"
  fi
  fact replay PASS "the SAME body B — same secret, same scheme, a stamp of now instead of $STALE_SECONDS seconds ago — is stored as one record under $ID_B in state '$fresh_state': the only difference between the two deliveries is the age of their stamps, so what refused the first is the five-minute window (F16)"
}

step_restore() {
  fact restore PASS "what this check leaves is two parked events, and it names them: $ID_A (delivered twice, unchanged) and $ID_B (refused stale, then stored fresh). They are in the operations queue for you to dismiss when you have read them — POST $API/api/ops/events/resolutions with {\"id\":\"<id>\",\"decision\":\"dismiss\"} and a sign-in from the last five minutes — and the check ends no event on your behalf"
}

now_epoch() { printf '%s' "$(date -u +%s)"; }

plan() {
  say "CHECK 573 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read-only] GET ${API}/api/workspaces with no sign-in — 401 'unauthenticated', which says the origin is the portal's API"
  say "  [network, read-only] GET ${API}/api/ops/events?state=parked with the operations member's token — the ledger the rows below compare"
  say "  [local, no network] sign body A with the webhook secret in ${SECRET_FILE} — Paddle's own scheme, the signature in a 0600 config"
  say "  [network, WRITE] POST ${API}/api/paddle/webhook with body A — parked, and one record in the queue"
  say "  [network, WRITE] POST ${API}/api/paddle/webhook with body A again (same bytes, same signature) — the ledger unchanged"
  say "  [network, WRITE] POST ${API}/api/paddle/webhook with body B stamped $STALE_SECONDS seconds ago — refused, and stored by nothing"
  say "  [network, WRITE] POST ${API}/api/paddle/webhook with body B stamped now — stored, which is what makes the refusal the window's"
  fact dry-run PASS "the plan was printed and nothing was run: no delivery was signed, no call was made and no event was read"
}

main() {
  need_tools
  STARTED=$(date -u +%s)
  ID_A="evt-chk573-a-$STARTED"
  ID_B="evt-chk573-b-$STARTED"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_api
  step_queue
  step_delivery
  step_redelivery
  step_window
  step_restore
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
PROJECT_CLI=""
API_CLI=""
TOKEN_CLI=""
SECRET_CLI=""
USAGE="check573.sh --project <p> --token-file <file> --webhook-secret-file <file> [--api <origin>] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    [ $# -ge 2 ] || die "--project needs a Firebase project id. usage: $USAGE"
    PROJECT_CLI=$2
    shift 2
    ;;
  --token-file)
    [ $# -ge 2 ] || die "--token-file needs a path. usage: $USAGE"
    TOKEN_CLI=$2
    shift 2
    ;;
  --webhook-secret-file)
    [ $# -ge 2 ] || die "--webhook-secret-file needs a path. usage: $USAGE"
    SECRET_CLI=$2
    shift 2
    ;;
  --api)
    [ $# -ge 2 ] || die "--api needs the portal's origin. usage: $USAGE"
    API_CLI=$2
    shift 2
    ;;
  --production)
    PRODUCTION=1
    shift
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
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done

PROJECT=${CHECK573_PROJECT:-${PROJECT_CLI:-}}
TOKEN_FILE=${CHECK573_TOKEN_FILE:-${TOKEN_CLI:-}}
SECRET_FILE=${CHECK573_WEBHOOK_SECRET_FILE:-${SECRET_CLI:-}}
PRODUCTION=${CHECK573_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK573_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK573_KEEP_SCRATCH:-}
STALE_SECONDS=${CHECK573_STALE_SECONDS:-$STALE_SECONDS}
API=${CHECK573_API:-${API_CLI:-}}
# The portal's origin, per project (P0.1): `fugaro-portal-staging` serves
# app.staging.fugaro.dev and `fugaro-portal` serves app.fugaro.dev, each site's
# custom domain being the host the owner attached to it.
if [ -z "$API" ]; then
  if [ "$PRODUCTION" = 1 ]; then
    API=https://app.fugaro.dev
  else
    API=https://app.staging.fugaro.dev
  fi
fi
case "$API" in
*/) API=${API%/} ;;
esac
CURL_SECONDS=${CHECK573_CURL_SECONDS:-60}

if [ -z "$PROJECT" ]; then
  die "Check 573 needs --project: the Firebase project the portal and its operations organisation live in (the staging deployment's own is fugaro-staging). Usage: $USAGE"
fi
if [ -z "$TOKEN_FILE" ]; then
  die "Check 573 needs --token-file: a file, mode 0600, holding one line — the ID token of an OPERATIONS member of the deployment (signed in with Google or Microsoft; an email-link sign-in is refused by the operations area, D2207). Usage: $USAGE"
fi
if [ -z "$SECRET_FILE" ]; then
  die "Check 573 needs --webhook-secret-file: a file, mode 0600, holding one line — the deployment's Paddle webhook secret, which is what signs a delivery. The register's Check 573 section carries the one gcloud line that reads it. Usage: $USAGE"
fi
case "$STALE_SECONDS" in
"" | *[!0-9]*) die "--stale-seconds '$STALE_SECONDS' is not a number. usage: $USAGE" ;;
esac
if [ "$STALE_SECONDS" -le 300 ]; then
  die "CHECK573_STALE_SECONDS is $STALE_SECONDS and the delivery window is 300 seconds: a stamp inside the window is not a replay the window refuses, and a check that read one would prove nothing. Use a value above 300"
fi
# Absolute, before anything changes directory: the readers below are handed the
# path, and a relative one would name another file after a cd.
case "$TOKEN_FILE" in /*) ;; *) TOKEN_FILE="$PWD/$TOKEN_FILE" ;; esac
case "$SECRET_FILE" in /*) ;; *) SECRET_FILE="$PWD/$SECRET_FILE" ;; esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
