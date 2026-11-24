#!/usr/bin/env bash
# Check 564 — a Paddle SANDBOX purchase, its renewal and its refund, end to end.
#
# Plan task K7.4 of docs/licence-key-v2/implementation-plan.md writes this check,
# and the number is that work's own (its "Checks this plan takes (560–569)"). What
# it settles is **K2.5** — Paddle's events reach v1 and mean what the licence says
# they mean — read from the outside and nowhere else: a card buys a licence in the
# Paddle SANDBOX, the licence is granted, the term renews, and a refund withdraws
# the WHOLE licence and not merely the key the event happened to name
# (**D2147**). The last of those is the one a refund stream gets wrong most often,
# and the row that reads it is the reason this check exists.
#
# Two rows are about the INSTALLATION as much as the portal: after the refund
# `fugaro licence status --json` has to read `revoked: true` with the tier and
# the seats STILL READABLE and the key STILL INSTALLED, because a withdrawal
# deletes nothing (**D436**, **D2148**'s shape: the refusal is uniform and the
# record is kept).
#
# What it measures:
#
#   * the API origin, with one read that carries NO sign-in: `GET /api/workspaces`
#     answers 401 `unauthenticated`, which is what says the origin in front of
#     this check is the portal's API and not a marketing page or somebody else's
#     host;
#   * the SANDBOX checkout you named: `fugaro doctor --json` in it names the
#     installation, its GCP project and its region, and the token file is one line
#     at mode 0600. Neither value is printed;
#   * **the Paddle SANDBOX account**: one read of `GET /prices?status=active`
#     against `https://sandbox-api.paddle.com`, printed as a count of active
#     prices and the first price id — never the key. A 401 or a 403 is a row this
#     check could not measure, and it names S8.5, the owner-run task that creates
#     that account;
#   * **the purchase**: `POST /api/checkout` with your token, the workspace and the
#     licence terms you accepted answers 201 with a Paddle `transaction_id`, and
#     that transaction is read back from the SANDBOX (`GET /transactions/{id}`) —
#     a transaction the SANDBOX does not hold was minted by a LIVE merchant, which
#     is the finding that this row is about;
#   * **the entitlement**: the purchase appears at `GET /api/entitlements` for the
#     workspace, with source `purchase` and status `active`, and the workspace's
#     own mirror (`GET /api/workspaces/{workspace}/subscription`) names Paddle's
#     subscription. A purchase whose workspace names no subscription is a finding;
#   * **the binding**: the purchase is a licence of the ACCOUNT you sign in as,
#     and nothing binds it to an installation by itself (a purchase mints no key,
#     F25). ONE browser sign-in (`fugaro licence login` in the sandbox, as the
#     account that bought) puts that account's key in the installation, and the
#     installation's own read is held against the purchase's own tier, kind and
#     seats. The key is never read;
#   * **the renewal**: Paddle's own period end (`GET /subscriptions/{id}`) and the
#     entitlement's own end agree, and after you bill the renewal the term has
#     MOVED. A renewal that did not move the term is a row this check could not
#     measure — not a finding — because it may mean the sandbox did not bill;
#   * **the refund**: the entitlement reads `revoked`, which is every key it ever
#     minted and not only the key the event named (D2147, F29), and it is the
#     portal's own record that says so;
#   * **the tick after the refund**: the installation hears about a withdrawal
#     only from its own check (K4.1), so after the refund this check fires the
#     deployed Scheduler job (`fugaro-licence-renew`) once more and waits for a
#     completed execution of the Cloud Run job `fugarolicence` that was CREATED
#     after that fire. No check recorded since the refund is a row this check
#     could NOT measure — never a FAIL;
#   * **the installation**: `fugaro licence status --json` in the sandbox reads
#     `revoked: true` with `last_check.status` `revoked` and `last_check.at`
#     AFTER the instant this run saw the refund land at the portal, and the tier,
#     the seats and the key itself are still there (D436). A key still serving
#     after the refund is the finding.
#
# What it creates: a Paddle SANDBOX transaction, its subscription and its refund —
# real objects in the sandbox and nothing at all in the live merchant — plus one
# withdrawn entitlement in the portal deployment you point at. The installation is
# left holding a revoked key; the restore row names what you do about it.
#
# Usage:      bash scripts/live-checks/check564.sh --sandbox <dir> --token-file <file> --paddle-key-file <file>
#             bash scripts/live-checks/check564.sh --sandbox <dir> --token-file <file> --paddle-key-file <file> [--scheduler-region <region>]
#             bash scripts/live-checks/check564.sh --sandbox <dir> --token-file <file> --paddle-key-file <file> --dry-run
#             bash scripts/live-checks/check564.sh --help
# Needs:      bash, mktemp, python3 and curl (the portal API, the Paddle sandbox
#             API and the key files), plus the released `fugaro` CLI and `gcloud`
#             (the installation's own `doctor`, `licence status` and renewal job).
#             The Paddle SANDBOX account and its API key come from subscriptions
#             S8.5, which is yours to run: without it this check refuses at once.
#             The renewal job is fired through `gcloud scheduler jobs run`, and
#             Scheduler is not offered in every Cloud Run region (K4.2): the job's
#             own location is read from the installation's project, and
#             `--scheduler-region` names it when there is nothing to read.
# Where:      your own machine, a THROWAWAY installation's checkout (`--sandbox`),
#             and the Paddle SANDBOX. The LIVE Paddle host is refused by name and
#             no live purchase is ever made. `fugaro-prod` among the values you
#             were given, and the GCP project the installation's own `doctor`
#             names, are refused unless `--production`; a value naming belong is
#             refused by name; and so is a coding agent's session.
#             ONE browser sign-in is asked for in the middle of the run (the bind
#             step): sign in as the account that bought the licence, and the check
#             waits for it with the same bound as the sign-in's own certificate.
# Costs:      nothing real. Every price, transaction, subscription and refund is
#             the Paddle SANDBOX's, whose cards are test cards; the portal and the
#             installation are ones you can throw away.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check564_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The sandbox transaction, its subscription and
#             its refund stay in the sandbox, and the installation keeps a revoked
#             key: the restore row names them.
# Output:     progress on stderr, the `MANUAL:` lines and ONE block on stdout
#             headed CHECK 564 RESULT, then CHECK 564 RESULT END (exit N) — to
#             copy back into the register's Check 564 section, and nothing else.
#             Exit 0: every criterion that was measured held. 1: a measured
#             criterion did not hold (a live merchant minted the transaction, a
#             purchase whose workspace names no subscription, a key still serving
#             after the refund). 2: a row could not be measured for a reason that
#             is this check's own setup — the sandbox API key is not in hand
#             (S8.5), the token file is not 0600, the Paddle host is not the
#             sandbox, the renewal did not move the term, the Scheduler job is in
#             no location this project serves or could not be fired, the sign-in
#             did not come back, or no check has been recorded since the refund. A key the run never
#             reached reads `FAIL — not reached`, which is a row that was NOT
#             measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no Paddle API key, no ID token, no licence key
# and no renewal secret, in whole or in part. The key and the token each travel in
# a 0600 curl `--config` file of their own and reach neither a command line nor
# the block; what the block carries of them is a count, an id and a mode. The
# Paddle key is read as bytes by a `head` whose output goes straight into the
# config file, so it is never an argument to any command.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK564_SANDBOX, CHECK564_TOKEN_FILE, CHECK564_PADDLE_KEY_FILE,
# CHECK564_WORKSPACE_ID, CHECK564_TERMS, CHECK564_API (the portal's origin),
# CHECK564_PADDLE_API (the Paddle host; a value that is not the sandbox is
# refused by name), CHECK564_PRODUCTION, CHECK564_SCHEDULER_REGION (where the
# licence renewal Scheduler job lives, read from the installation's project when
# it is not given), CHECK564_FUGARO, CHECK564_GCLOUD,
# CHECK564_DRY_RUN, CHECK564_KEEP_SCRATCH, CHECK564_CURL_SECONDS (60),
# CHECK564_CLI_TIMEOUT_SECONDS (180), CHECK564_WAIT (30: how many times a poll
# looks) and CHECK564_POLL_SECONDS (10: the pause between looks).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a
# Paddle API key and a sign-in, and a trace of the calls that carry them is a
# second copy of both.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block and the MANUAL
# lines are written to the saved descriptors.
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

# manual: a step no script takes, and one the owner has to take WHILE the check
# waits. It is printed at once on the owner's own stderr and kept for the block's
# `MANUAL:` lines, so a person watching the run sees it before the poll below it
# begins and a reader of the block sees everything the run asked for.
MANUALS=()
manual() { # manual "text"
  MANUALS+=("$1")
  owner "MANUAL: $1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  local t
  for t in bash mktemp python3 curl; do
    command -v "$t" >/dev/null 2>&1 || die "Check 564 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""
LOGIN_PID=""
LOGIN_REAPED=yes
LOGIN_STATUS=0

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check564.XXXXXX") || die "Check 564 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK564_KEEP_SCRATCH is set)"
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

# login_stop kills the sign-in if this check ends before it does, so a stop, a
# Ctrl-C or a failed assertion never leaves a `fugaro licence login` holding this
# terminal and a listener.
login_stop() {
  [ -n "$LOGIN_PID" ] || return 0
  if kill -0 "$LOGIN_PID" 2>/dev/null; then
    kill -TERM "$LOGIN_PID" 2>/dev/null || true
    sleep 1
    kill -KILL "$LOGIN_PID" 2>/dev/null || true
  fi
  wait "$LOGIN_PID" 2>/dev/null || true
  LOGIN_REAPED=yes
  LOGIN_PID=""
}

reap_login() {
  [ "$LOGIN_REAPED" = yes ] && return 0
  LOGIN_STATUS=0
  wait "$LOGIN_PID" 2>/dev/null || LOGIN_STATUS=$?
  LOGIN_REAPED=yes
  LOGIN_PID=""
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  login_stop
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK564_KEEP_SCRATCH is set)"
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
BLOCK_KEYS=(xtrace agent-session belong project paddle-sandbox api token sandbox scheduler checkout entitlement bind renewal refund tick install restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 564 RESULT\n' >&3
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
  local line
  for line in ${MANUALS+"${MANUALS[@]}"}; do
    printf 'MANUAL: %s\n' "$line" >&3
  done
  printf 'CHECK 564 RESULT END (exit %s)\n' "$status" >&3
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
# caller reads the empty key as "the document did not answer".

fault_fields() { # fault_fields FILE — the portal API's one refusal shape
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

paddle_fault_fields() { # paddle_fault_fields FILE — Paddle's own refusal shape
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
fault = doc.get("error") or {}
print("code=%s" % (fault.get("code") or ""))
print("detail=%s" % (fault.get("detail") or ""))
PY
}

doctor_fields() { # doctor_fields FILE — the installation's own three fields
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
project = doc.get("project") or {}
print("installation=%s" % (project.get("name") or ""))
print("project=%s" % (project.get("gcp_project") or ""))
print("region=%s" % (project.get("region") or ""))
PY
}

checkout_fields() { # checkout_fields FILE — the transaction the checkout minted
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
print("transaction_id=%s" % (doc.get("transaction_id") or ""))
PY
}

paddle_prices_fields() { # paddle_prices_fields FILE — GET /prices?status=active
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
data = doc.get("data") or []
print("count=%d" % len(data))
first = data[0] if data else {}
print("first_id=%s" % (first.get("id") or ""))
print("currency=%s" % ((first.get("unit_price") or {}).get("currency_code") or ""))
PY
}

paddle_txn_fields() { # paddle_txn_fields FILE — GET /transactions/{id}
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
data = doc.get("data") or {}
custom = data.get("custom_data") or {}
print("id=%s" % (data.get("id") or ""))
print("status=%s" % (data.get("status") or ""))
print("subscription_id=%s" % (data.get("subscription_id") or ""))
print("workspace_id=%s" % (custom.get("workspace_id") or ""))
PY
}

paddle_sub_fields() { # paddle_sub_fields FILE — GET /subscriptions/{id}
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
data = doc.get("data") or {}
period = data.get("current_billing_period") or {}
print("id=%s" % (data.get("id") or ""))
print("status=%s" % (data.get("status") or ""))
print("period_ends_at=%s" % (period.get("ends_at") or ""))
print("next_billed_at=%s" % (data.get("next_billed_at") or ""))
PY
}

# entitlement_list FILE WANT: the entitlement of ONE workspace out of the list
# `GET /api/entitlements` answers — the org's whole set, so the workspace is what
# picks the row out. A workspace the list does not carry answers with empty keys,
# which the caller reads as "the purchase is not there yet" and polls again.
entitlement_list() { # entitlement_list FILE WANT
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
want = sys.argv[2]
rows = doc.get("entitlements") or []
for row in rows:
    if (row.get("workspace_id") or "") != want:
        continue
    print("id=%s" % (row.get("id") or ""))
    print("workspace_id=%s" % (row.get("workspace_id") or ""))
    print("source=%s" % (row.get("source") or ""))
    print("status=%s" % (row.get("status") or ""))
    print("tier=%s" % (row.get("tier") or ""))
    print("kind=%s" % (row.get("kind") or ""))
    print("seats=%s" % (row.get("seats") if row.get("seats") is not None else ""))
    print("row=%s" % (row.get("row") or ""))
    print("ends_at=%s" % (row.get("ends_at") or ""))
    raise SystemExit(0)
PY
}

subscription_fields() { # subscription_fields FILE — GET /workspaces/{w}/subscription
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
print("id=%s" % (doc.get("id") or ""))
print("workspace_id=%s" % (doc.get("workspace_id") or ""))
print("state=%s" % (doc.get("state") or ""))
print("seats=%s" % (doc.get("seats") if doc.get("seats") is not None else ""))
print("renews_at=%s" % (doc.get("renews_at") or ""))
print("ends_at=%s" % (doc.get("ends_at") or ""))
PY
}

licence_fields() { # licence_fields FILE — `fugaro licence status --json`
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
last = doc.get("last_check") or {}
print("source=%s" % (doc.get("source") or ""))
print("revoked=%s" % ("true" if doc.get("revoked") else "false"))
print("state=%s" % (doc.get("state") or ""))
print("tier=%s" % (doc.get("tier") or ""))
print("kind=%s" % (doc.get("kind") or ""))
print("seats=%s" % (doc.get("seats") if doc.get("seats") is not None else ""))
print("paid_through=%s" % (doc.get("paid_through") or ""))
print("check_status=%s" % (last.get("status") or ""))
print("check_at=%s" % (last.get("at") or ""))
PY
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# scheduler_fields FILE — the Scheduler job's own document, as far as this check
# reads it. The state and the schedule are printed rather than held against
# anything: `gcloud scheduler jobs run` fires a paused job too, and the row this
# check is about is the fire, not the cron.
scheduler_fields() { # scheduler_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
if not isinstance(doc, dict):
    raise SystemExit(0)
print("state=%s" % (doc.get("state") or ""))
print("schedule=%s" % (doc.get("schedule") or ""))
PY
}

# The newest execution of the job created at or after the fire. `gcloud run jobs
# executions list --format=json` prints the executions in whichever order the API
# hands them back, so the newest is picked here by its own creation time against
# the instant this check fired the Scheduler job: the only thing this check does
# to start work is that fire, so an execution created after it is that fire's.
executions_fields() { # executions_fields FILE STARTED_EPOCH
  python3 - "$1" "$2" <<'PY'
import datetime, json, sys

def epoch(ts):
    if not isinstance(ts, str) or not ts:
        return None
    try:
        return datetime.datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
started = int(sys.argv[2])
best = None
newest = None
for e in doc:
    if not isinstance(e, dict):
        continue
    md = e.get("metadata") or {}
    st = e.get("status") or {}
    created = md.get("creationTimestamp") or st.get("startTime") or ""
    at = epoch(created)
    if at is None:
        continue
    if newest is None or at > newest[0]:
        newest = (at, e, created)
    if at < started:
        continue
    if best is None or at > best[0]:
        best = (at, e, created)


def name_of(entry):
    e = entry[1]
    md = e.get("metadata") or {}
    st = e.get("status") or {}
    completed = ""
    for c in st.get("conditions") or []:
        if isinstance(c, dict) and c.get("type") == "Completed":
            completed = c.get("status") or ""
    return (md.get("name") or "").rsplit("/", 1)[-1], completed


# The newest execution is printed whichever answer this is: when none of them was
# created after the fire, the row that says so NAMES the newest one it did see —
# "the job has not run since you fired it" and "the job's newest run is this one,
# from before the fire" are different sentences, and only one of them is true.
if best is None:
    print("found=no")
    if newest is None:
        raise SystemExit(0)
    entry = newest
    created = entry[2]
else:
    print("found=yes")
    entry = best
    created = entry[2]
name, completed = name_of(entry)
print("name=%s" % name)
print("created=%s" % created)
print("completed=%s" % completed)
PY
}

# extract_address FILE — the first sign-in address the command printed, or "". The
# check reads that address off its own line so the MANUAL line can name it; it
# reads no code, and it provides none.
extract_address() { # extract_address FILE
  python3 - "$1" <<'PY'
import re, sys
try:
    text = open(sys.argv[1], encoding="utf-8", errors="replace").read()
except OSError:
    raise SystemExit(0)
m = re.search(r'https?://[^\s]+/cli/authorize\?[^\s]+', text)
print(m.group(0) if m else "")
PY
}

# stamp_after LATER EARLIER — "yes" when both are timestamps and LATER is
# strictly after EARLIER, "no" when it is not, "unknown" when either cannot be
# read as one. The comparison is made in one place so a row's sentence is about a
# fact rather than about how a shell compares two strings.
stamp_after() { # stamp_after A B
  python3 - "$1" "$2" <<'PY'
import datetime, sys

def at(ts):
    if not isinstance(ts, str) or not ts.strip():
        return None
    try:
        return datetime.datetime.fromisoformat(ts.strip().replace("Z", "+00:00")).timestamp()
    except Exception:
        return None

a, b = at(sys.argv[1]), at(sys.argv[2])
if a is None or b is None:
    print("unknown")
else:
    print("yes" if a > b else "no")
PY
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    unmeasured xtrace "this shell is tracing (bash -x): every command of this run would be printed, and the calls that carry the sandbox API key and your sign-in are among them. No row below was measured"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check564.sh --sandbox <dir> --token-file <file> --paddle-key-file <file> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of a call carrying the sandbox API key or a sign-in is a copy of it"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the Paddle key or the Authorization header of a portal call to the terminal"
}

# guard_agent_session: this check buys a licence and reads a sign-in YOU made in a
# browser. A coding agent's session has no browser, no Paddle account of yours and
# no terminal to pay at, so a run there would measure somebody else's session or
# nothing at all.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    unmeasured agent-session "${marker} is set, so this is a coding agent's session: Check 564 buys through the Paddle sandbox and reads YOUR sign-in, so it is yours to run at your own terminal. No row below was measured"
    stop "${marker} is set, so this is a coding agent's session: Check 564 buys through the Paddle sandbox and reads YOUR sign-in, so it is yours to run at your own terminal — no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# PADDLE_KEY_NEED is the exact sentence every refusal of the sandbox API key
# carries. The key and the account are BOTH S8.5's, which is an owner-run task in
# the licence-key-v2 plan, so a check that ran without them would be measuring a
# premise nobody had built yet.
PADDLE_KEY_NEED="Check 564 buys through the Paddle SANDBOX, and subscriptions S8.5 is the owner-run task that creates that account and its API key: write the sandbox API key into a file of its own at mode 0600 and pass it"

guard_paddle_key_file() {
  if [ ! -f "$PADDLE_KEY_FILE" ]; then
    unmeasured paddle-sandbox "$PADDLE_KEY_NEED (the file $PADDLE_KEY_FILE is not there)"
    stop "$PADDLE_KEY_NEED — the file $PADDLE_KEY_FILE is not there, so no sandbox call could be made"
  fi
  local mode
  mode=$(python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$PADDLE_KEY_FILE" 2>/dev/null || true)
  if [ "$mode" != 600 ]; then
    unmeasured paddle-sandbox "$PADDLE_KEY_NEED (the file $PADDLE_KEY_FILE is mode ${mode:-unknown}, not 600)"
    stop "$PADDLE_KEY_NEED — the file $PADDLE_KEY_FILE is mode ${mode:-unknown} and has to be 0600"
  fi
  local bytes lines
  bytes=$(wc -c <"$PADDLE_KEY_FILE" | tr -d ' ')
  lines=$(wc -l <"$PADDLE_KEY_FILE" | tr -d ' ')
  if [ "$bytes" -eq 0 ] || [ "$lines" -gt 1 ]; then
    unmeasured paddle-sandbox "$PADDLE_KEY_NEED (the file $PADDLE_KEY_FILE holds $bytes byte(s) on $lines line(s))"
    stop "$PADDLE_KEY_NEED — the file $PADDLE_KEY_FILE is empty or holds more than one line"
  fi
}

# guard_inputs: everything read before any call is made. The Paddle host is
# refused by name when it is not the sandbox: a check that bought a real
# subscription would take a real card and leave a real charge behind.
guard_inputs() {
  local all="${SANDBOX:-}${TOKEN_FILE:-}${PADDLE_KEY_FILE:-}${API:-}${PADDLE_API:-}${WORKSPACE_ID:-}${TERMS:-}"
  case "$all" in
  *belong*)
    unmeasured belong "a value this check was given names belong: this check is never run there, and no row below was measured"
    stop "a value this check was given names belong: this check is never run there"
    ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  case "$all" in
  *fugaro-prod*)
    if [ "$PRODUCTION" != 1 ]; then
      unmeasured project "a value this check was given names fugaro-prod, which is refused unless --production is given: the rows below make a real purchase against a real deployment, and production is only ever measured on purpose. No row below was measured"
      stop "a value this check was given names fugaro-prod, which is refused unless --production is given: the rows below make a real purchase against a real deployment, and production is only ever measured on purpose. Nothing was read"
    fi
    ;;
  esac
  if [ "$PRODUCTION" = 1 ]; then
    fact project PASS "this run was named --production: the deployment behind $API is measured by choice, and the register's own rule is that Checks 560 to 569 run on staging first"
  else
    fact project PASS "this check is not in production (no fugaro-prod among the values it was given, and --production is not set): it is the staging deployment $API, which is where Checks 560 to 569 run first"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact paddle-sandbox PASS "$PADDLE_API (the Paddle host; a real run holds it to the sandbox and refuses any other by name) and $PADDLE_KEY_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact api PASS "$API (the portal's origin; a real run holds it to one read that answers the API's own unauthenticated refusal)"
    fact token PASS "$TOKEN_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact sandbox PASS "planned: $FUGARO_BIN doctor --json in $SANDBOX — the installation, its GCP project and its region"
    return 0
  fi
  if [ "$PADDLE_API" != "$PADDLE_SANDBOX" ]; then
    unmeasured paddle-sandbox "$PADDLE_API is not the Paddle SANDBOX ($PADDLE_SANDBOX): this check buys a subscription through Paddle, and the LIVE host is refused BY NAME — a live purchase takes a real card and leaves a real charge, so no call was made. Set CHECK564_PADDLE_API to the sandbox origin"
    stop "the Paddle host $PADDLE_API is refused by name: this check speaks to the Paddle SANDBOX ($PADDLE_SANDBOX) and to no other host"
  fi
  guard_paddle_key_file
  local mode
  if [ ! -f "$TOKEN_FILE" ]; then
    unmeasured token "the token file $TOKEN_FILE is not there: this check reads the portal through YOUR OWN sign-in (copy the ID token your browser holds after signing in to $API), and a file that is not there is a row it cannot measure. Nothing about the portal was read"
    stop "the token file $TOKEN_FILE is not there, so none of the portal's rows could be read. Copy the ID token into a file of its own, then run the check again"
  fi
  mode=$(python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$TOKEN_FILE" 2>/dev/null || true)
  if [ "$mode" != 600 ]; then
    unmeasured token "the token file $TOKEN_FILE is mode ${mode:-unknown}, not 600: this check will not carry a sign-in out of a world-readable file"
    stop "the token file $TOKEN_FILE is mode ${mode:-unknown} and has to be 0600. Nothing was read from it"
  fi
  local bytes lines
  bytes=$(wc -c <"$TOKEN_FILE" | tr -d ' ')
  lines=$(wc -l <"$TOKEN_FILE" | tr -d ' ')
  if [ "$bytes" -eq 0 ] || [ "$lines" -gt 1 ]; then
    unmeasured token "the token file $TOKEN_FILE holds $bytes byte(s) on $lines line(s): it carries the ID token your browser holds, one line and nothing else"
    stop "the token file $TOKEN_FILE is empty or holds more than one line. Nothing was read from it"
  fi
  fact token PASS "$TOKEN_FILE is one line at mode 0600 (the value itself is never read into a fact, a command line or the block)"
}

# ------------------------------------------------------------------ the calls
# api_call METHOD PATH KEY [BODY] — one call to the portal API with the owner's
# sign-in. The token goes into a 0600 curl config and never onto a command line,
# where `ps` and this check's own error output would both show it. The answer's
# status is printed by curl into $SCRATCH/KEY.code and its body into
# $SCRATCH/KEY.body.
api_call() {
  local method=$1 path=$2 key=$3 body=${4:-}
  local cfg="$SCRATCH/$key.curlrc"
  ( umask 077; { printf 'header = "Authorization: Bearer '; head -n 1 "$TOKEN_FILE" | tr -d '\n'; printf '"\n'; } >"$cfg" ) \
    || die "the token file could not be read into a 0600 config"
  local args=(-sS --max-time "$CURL_SECONDS" --config "$cfg" -o "$SCRATCH/$key.body" -w '%{http_code}' -X "$method")
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  args+=("$API$path")
  curl "${args[@]}" >"$SCRATCH/$key.code" 2>"$SCRATCH/$key.curl.err" || true
  tr -d '\n' <"$SCRATCH/$key.code"
}

# paddle_call METHOD PATH KEY — one call to the Paddle SANDBOX with the sandbox
# API key. The key goes into its own 0600 curl config the same way, and `head`'s
# output is piped straight into the file so the key is never an argument to any
# command.
paddle_call() {
  local method=$1 path=$2 key=$3
  local cfg="$SCRATCH/paddle-$key.curlrc"
  ( umask 077; { printf 'header = "Authorization: Bearer '; head -n 1 "$PADDLE_KEY_FILE" | tr -d '\n'; printf '"\n'; } >"$cfg" ) \
    || die "the sandbox API key file could not be read into a 0600 config"
  curl -sS --max-time "$CURL_SECONDS" --config "$cfg" -o "$SCRATCH/paddle-$key.body" \
    -w '%{http_code}' -X "$method" "$PADDLE_API$path" >"$SCRATCH/paddle-$key.code" 2>"$SCRATCH/paddle-$key.curl.err" || true
  tr -d '\n' <"$SCRATCH/paddle-$key.code"
}

# run_bounded_env SECONDS KEY CMD... — one CLI run, from the sandbox, under a
# bound. Nothing is handed to it on stdin, so a command that would wait for an
# answer gets a closed one instead.
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  (
    trap - ERR
    if [ -n "$SANDBOX" ] && [ -d "$SANDBOX" ]; then cd "$SANDBOX" || exit 1; fi
    exec "$@"
  ) </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

# poll_again — one more look, while the bounded wait has not run out. WAIT is how
# many times a poll looks and POLL_SECONDS is the pause between looks, so a run
# never waits on a person's stdin: it waits a documented number of seconds and
# then reports the row as one it could not measure.
POLL_TRIES=0
poll_reset() { POLL_TRIES=0; }
poll_again() {
  [ "$POLL_TRIES" -lt "$WAIT" ] || return 1
  POLL_TRIES=$((POLL_TRIES + 1))
  sleep "$POLL_SECONDS"
  return 0
}

# ------------------------------------------------------------------ the steps
# step_api: one read with NO sign-in. The API's own 401 `unauthenticated` is what
# says the origin in front of this check is the portal's API.
step_api() {
  local code
  code=$(curl -sS --max-time "$CURL_SECONDS" -o "$SCRATCH/anonymous.body" -w '%{http_code}' "$API/api/workspaces" 2>"$SCRATCH/anonymous.curl.err" || true)
  code=$(printf '%s' "$code" | tr -d '\n')
  if [ "$code" != 401 ]; then
    unmeasured api "GET $API/api/workspaces with no sign-in answered ${code:-nothing} rather than 401: the origin this check was given does not answer as the portal's API (the check reads the API's own unauthenticated refusal, which is 401 with code 'unauthenticated'). Nothing below this row is about the deployment you meant"
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

# step_sandbox: the installation this check drives, read from its own `doctor`.
# The three fields say which checkout the `licence status` row below is about and
# where the renewal job is; the check never assumes them from a flag. The project
# doctor names is held against `fugaro-prod` HERE, where it is known: this check
# buys a real subscription and fires that project's renewal job, so the fence
# cannot wait for a value the owner typed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    unmeasured sandbox "the sandbox checkout $SANDBOX is not a directory: --sandbox names a THROWAWAY installation's checkout, whose own \`fugaro\` this check drives. Nothing about it was read"
    stop "the sandbox checkout $SANDBOX is not a directory, so the installation's own rows could not be read"
  fi
  run_bounded "$CLI_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured sandbox "$FUGARO_BIN doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX"
    stop "$FUGARO_BIN doctor --json timed out, so the installation could not be named"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured sandbox "$FUGARO_BIN doctor --json exited $BOUNDED_STATUS in $SANDBOX: $(last_lines "$SCRATCH/doctor.err")"
    stop "$FUGARO_BIN doctor --json did not answer, so the installation could not be named"
  fi
  doctor_fields "$SCRATCH/doctor.out" >"$SCRATCH/doctor.txt"
  local installation project region
  installation=$(field "$SCRATCH/doctor.txt" installation)
  project=$(field "$SCRATCH/doctor.txt" project)
  region=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$project" ]; then
    unmeasured sandbox "$SANDBOX's $FUGARO_BIN doctor --json names no GCP project: this checkout is not an installation's, and the licence rows below read a project before they read a key"
    stop "the sandbox checkout $SANDBOX has no GCP project, so it is not an installation this check can drive"
  fi
  if [ "$project" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    unmeasured sandbox "$SANDBOX's $FUGARO_BIN doctor --json names the GCP project fugaro-prod, which is refused unless --production is given: this check makes a real purchase and fires that project's \`fugaro-licence-renew\` job, and production is only ever measured on purpose. No row below was measured"
    stop "the installation in $SANDBOX is the fugaro-prod project, which is refused unless --production is given: nothing was bought, nothing was read and nothing was fired"
  fi
  SANDBOX_PROJECT=$project
  SANDBOX_REGION=$region
  fact sandbox PASS "${installation:-<unnamed>} in $project (region ${region:-unknown}), read from $SANDBOX's own $FUGARO_BIN doctor --json — the THROWAWAY installation whose own licence the install row reads"
}

# scheduler_location — the region the licence renewal Scheduler job lives in,
# resolved rather than assumed. K4.2's module places the job in the NEAREST
# Scheduler location it can, and Scheduler is not offered everywhere Cloud Run is:
# the installation's own Cloud Run region (us-east5 among them) is frequently not
# a Scheduler location at all, so firing the job at $SANDBOX_REGION would aim the
# fire at a location the job is not in. SCHEDULER_REGION is set HERE and not
# returned through a command substitution: a stop below has to end THIS run, and a
# stop inside $( ) would end a subshell instead, leaving the check to read an empty
# answer as one it could not measure. Every path below leaves the job's own
# document in $SCRATCH/scheduler.json.
scheduler_location() {
  local rc=0
  if [ -n "$SCHEDULER_REGION" ]; then
    "$GCLOUD_BIN" scheduler jobs describe "$SCHEDULER_JOB" --location "$SCHEDULER_REGION" --project "$SANDBOX_PROJECT" --format=json >"$SCRATCH/scheduler.json" 2>"$SCRATCH/scheduler.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
      unmeasured scheduler "the Scheduler job $SCHEDULER_JOB is not in the location --scheduler-region named (\`$GCLOUD_BIN scheduler jobs describe\` exited $rc: $(last_lines "$SCRATCH/scheduler.err")). K4.2 applies that job UNDER THE COORDINATOR'S OWN CONDITION: an installation that has not opted into the coordinator has no tagged image to run and no renewal job at all, and \`fugaro licence status\` says renewal is by hand there (TN5). Nothing about the renewal was measured. Name the region the installation's Scheduler jobs live in (\`$GCLOUD_BIN scheduler jobs list --location <location>\`) or turn the coordinator on with \`fugaro update\`"
      stop "the Scheduler job $SCHEDULER_JOB was not found in the location this check was given, so the renewal this check fires is not there"
    fi
    return 0
  fi
  "$GCLOUD_BIN" scheduler locations list --project "$SANDBOX_PROJECT" --format=json >"$SCRATCH/locations.json" 2>"$SCRATCH/locations.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured scheduler "the project's Scheduler locations could not be listed (\`$GCLOUD_BIN scheduler locations list\` exited $rc: $(last_lines "$SCRATCH/locations.err")), so this check cannot find the region the renewal job runs in. Scheduler is not offered in every Cloud Run region (K4.2), so its region is a fact about this installation rather than a name this check can derive: pass --scheduler-region with the region the installation's Scheduler jobs live in, or run the command yourself"
    stop "the Scheduler locations could not be listed, so the check cannot aim its fire at the renewal job"
  fi
  local candidate found=""
  for candidate in "$SANDBOX_REGION" $(python3 - "$SCRATCH/locations.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
names = []
for e in doc:
    if isinstance(e, dict):
        n = e.get("locationId") or (e.get("name") or "").rsplit("/", 1)[-1]
        if n:
            names.append(n)
print(" ".join(sorted(set(names))))
PY
  ); do
    [ -n "$candidate" ] || continue
    rc=0
    "$GCLOUD_BIN" scheduler jobs describe "$SCHEDULER_JOB" --location "$candidate" --project "$SANDBOX_PROJECT" --format=json >"$SCRATCH/scheduler.json" 2>"$SCRATCH/scheduler.err" || rc=$?
    if [ "$rc" -eq 0 ]; then
      found=$candidate
      break
    fi
  done
  if [ -z "$found" ]; then
    unmeasured scheduler "the Scheduler job $SCHEDULER_JOB is in none of this project's Scheduler locations (the installation's own Cloud Run region ${SANDBOX_REGION:-unknown} first). K4.2 applies that job UNDER THE COORDINATOR'S OWN CONDITION: an installation that has not opted into the coordinator has no tagged image to run and no renewal job at all, and \`fugaro licence status\` says renewal is by hand there (TN5). Nothing about the renewal was measured. Read that job's region yourself (\`$GCLOUD_BIN scheduler jobs list --location <location>\`) and pass --scheduler-region, or turn the coordinator on with \`fugaro update\`"
    stop "the Scheduler job $SCHEDULER_JOB was not found in any location the project serves, so the renewal this check fires is not there"
  fi
  SCHEDULER_REGION=$found
}

# step_scheduler: where the renewal tick is, read BEFORE anything is bought. A job
# this check cannot find is a row it could not measure, and it says so before a
# sandbox transaction exists rather than after two billings.
step_scheduler() {
  scheduler_location
  scheduler_fields "$SCRATCH/scheduler.json" >"$SCRATCH/scheduler.txt"
  local state schedule
  state=$(field "$SCRATCH/scheduler.txt" state)
  schedule=$(field "$SCRATCH/scheduler.txt" schedule)
  fact scheduler PASS "the renewal tick this check fires is $SCHEDULER_JOB in Scheduler location $SCHEDULER_REGION (state '${state:-unknown}', schedule '${schedule:-none}'): Scheduler is not offered in every Cloud Run region, so the location was read from the project $SANDBOX_PROJECT rather than taken to be the installation's own Cloud Run region ${SANDBOX_REGION:-unknown}. This check fires it twice — before the refund, for the term's move, and after it, so the installation's own check records the withdrawal"
}

# step_paddle_sandbox: one read of the Paddle SANDBOX, which is what proves the
# account S8.5 creates is reachable and carries the licence's prices. What the
# block carries is a count and an id: the key is never read into a fact.
step_paddle_sandbox() {
  local code
  code=$(paddle_call GET /prices?status=active prices)
  if [ "$code" = 401 ] || [ "$code" = 403 ]; then
    paddle_fault_fields "$SCRATCH/paddle-prices.body" >"$SCRATCH/paddle-prices.txt"
    unmeasured paddle-sandbox "GET $PADDLE_API/prices answered $code (code '$(field "$SCRATCH/paddle-prices.txt" code)', '$(field "$SCRATCH/paddle-prices.txt" detail)'): the sandbox API key in $PADDLE_KEY_FILE is not one this account accepts. subscriptions S8.5 is the owner-run task that creates the Paddle sandbox account and its API key — run it, write the sandbox key into a file of its own at mode 0600, and run this check again"
    stop "the Paddle SANDBOX refused the API key ($code), and S8.5 is the task that creates the account and its key"
  fi
  if [ "$code" != 200 ]; then
    unmeasured paddle-sandbox "GET $PADDLE_API/prices answered ${code:-nothing} rather than 200 (curl said: $(last_lines "$SCRATCH/paddle-prices.curl.err")): the sandbox read this row is made of never completed"
    stop "the Paddle sandbox read did not answer, so the sandbox account could not be measured"
  fi
  paddle_prices_fields "$SCRATCH/paddle-prices.body" >"$SCRATCH/paddle-prices.txt"
  local count first_id currency
  count=$(field "$SCRATCH/paddle-prices.txt" count)
  first_id=$(field "$SCRATCH/paddle-prices.txt" first_id)
  currency=$(field "$SCRATCH/paddle-prices.txt" currency)
  if [ "${count:-0}" -eq 0 ]; then
    unmeasured paddle-sandbox "$PADDLE_API answered 200 with no active prices: the sandbox account S8.5 creates is reachable, and it carries none of the licence's prices yet. Load the sandbox catalogue (the licence's own prices) and run the check again"
    stop "the Paddle SANDBOX account holds no active price, so a purchase against it could not be priced"
  fi
  fact paddle-sandbox PASS "$PADDLE_API answers GET /prices?status=active with $count active price(s), the first ${first_id:-<unnamed>}${currency:+ priced in $currency}: the sandbox account subscriptions S8.5 creates is reachable and priced, read with the key in $PADDLE_KEY_FILE (the key itself is never printed, and it travels in a 0600 curl config)"
}

# step_checkout: the purchase. The API creates the transaction server-side with
# the workspace in its custom_data (F15), and the row holds two things: the answer
# carries a Paddle transaction id, and the SANDBOX holds that transaction — a
# transaction the sandbox does not have was minted by a LIVE merchant, which is
# the finding this row exists for.
step_checkout() {
  local body code
  body=$(printf '{"workspace_id":"%s","terms_version":"%s"}' "$WORKSPACE_ID" "$TERMS")
  code=$(api_call POST /api/checkout checkout "$body")
  if [ "$code" = 409 ]; then
    unmeasured checkout "POST /api/checkout answered 409 'already_subscribed': the workspace $WORKSPACE_ID already has a running subscription, and one workspace has one (D2192). Use a workspace with no licence (the sandbox is throwaway), or cancel the running subscription first"
    stop "the workspace $WORKSPACE_ID already has a subscription, so no new purchase could be made"
  fi
  if [ "$code" = 422 ]; then
    fault_fields "$SCRATCH/checkout.body" >"$SCRATCH/checkout.txt"
    unmeasured checkout "POST /api/checkout answered 422 '$(field "$SCRATCH/checkout.txt" code)': the licence terms were not accepted, so the row is about the terms and not the purchase. Pass --terms with the licence terms version you accepted (the version the API records)"
    stop "the checkout was refused for the licence terms, so the purchase was not made"
  fi
  if [ "$code" != 201 ]; then
    fault_fields "$SCRATCH/checkout.body" >"$SCRATCH/checkout.txt"
    unmeasured checkout "POST /api/checkout answered ${code:-nothing} (code '$(field "$SCRATCH/checkout.txt" code)', '$(field "$SCRATCH/checkout.txt" message)'): the API did not open the purchase, so nothing below it could be read"
    stop "the API refused the checkout (${code:-no answer}), so no transaction was minted"
  fi
  checkout_fields "$SCRATCH/checkout.body" >"$SCRATCH/checkout.txt"
  local txn
  txn=$(field "$SCRATCH/checkout.txt" transaction_id)
  if [ -z "$txn" ]; then
    unmeasured checkout "POST /api/checkout answered 201 with no transaction id: the answer the overlay is opened with is Paddle's id for the transaction the API created, and a checkout without one is a row this check cannot read"
    stop "the checkout answered 201 and named no transaction, so the purchase has nothing to be measured against"
  fi
  TXN_ID=$txn
  # The transaction is read back from the SANDBOX. Paddle's own account is what
  # says which merchant minted it: the sandbox holds the sandbox's transactions
  # and no other merchant's.
  local code2
  code2=$(paddle_call GET "/transactions/$TXN_ID" txn)
  if [ "$code2" = 404 ]; then
    fact checkout FAIL "POST /api/checkout minted the Paddle transaction $TXN_ID and the SANDBOX does not hold it (GET /transactions/$TXN_ID answered 404): the transaction was minted by a merchant other than the sandbox — the LIVE merchant — so this deployment is buying through Paddle LIVE and not through the sandbox S8.5 creates. No live purchase is ever made by this check, and this is the row that says the deployment was pointed at live"
    stop "the transaction the checkout minted is not in the Paddle sandbox, so a LIVE merchant minted it"
  fi
  if [ "$code2" != 200 ]; then
    unmeasured checkout "GET $PADDLE_API/transactions/$TXN_ID answered ${code2:-nothing} rather than 200 (curl said: $(last_lines "$SCRATCH/paddle-txn.curl.err")): the sandbox read-back this row is made of never completed"
    stop "the transaction could not be read back from the sandbox, so the merchant that minted it was not measured"
  fi
  paddle_txn_fields "$SCRATCH/paddle-txn.body" >"$SCRATCH/paddle-txn.txt"
  local txn_id_back status ws_in_txn
  txn_id_back=$(field "$SCRATCH/paddle-txn.txt" id)
  status=$(field "$SCRATCH/paddle-txn.txt" status)
  ws_in_txn=$(field "$SCRATCH/paddle-txn.txt" workspace_id)
  if [ "$txn_id_back" != "$TXN_ID" ]; then
    unmeasured checkout "the sandbox answered GET /transactions/$TXN_ID with the transaction ${txn_id_back:-<unnamed>}: the read-back is not of the transaction the checkout minted, so this row cannot say which merchant minted it"
    stop "the sandbox's read-back names another transaction, so the merchant was not measured"
  fi
  if [ -n "$ws_in_txn" ] && [ "$ws_in_txn" != "$WORKSPACE_ID" ]; then
    fact checkout FAIL "the sandbox holds the transaction $TXN_ID and its custom_data names the workspace '$ws_in_txn' rather than $WORKSPACE_ID: the API creates the transaction with the workspace in its custom_data (F15), so a workspace that is not the one bought for is F15's own fault"
    stop "the transaction's custom_data names another workspace than the one bought for"
  fi
  fact checkout PASS "POST /api/checkout answered 201 with the Paddle transaction $TXN_ID (status '${status:-unknown}', custom_data workspace '${ws_in_txn:-none}'), and the SANDBOX holds it (GET /transactions/$TXN_ID answered 200): the sandbox merchant minted the transaction, so this deployment buys through the sandbox and not through the live one"
  manual "the transaction $TXN_ID is made. Open Paddle's sandbox overlay for it (https://sandbox-checkout.paddle.com/... is where the sandbox sends a buyer; the API has already created the transaction, so nothing but the card is left) and pay with a Paddle SANDBOX test card. Then come back: the check waits up to ${WAIT_WINDOW}s for the purchase to appear at the portal. What to paste back with the block: the sandbox card you used and whether Paddle answered the payment"
}

# step_entitlement: the purchase, read from the portal. The row holds the
# entitlement's own source, status and workspace AND the workspace's mirror of
# Paddle's subscription (D2196, D2192): a purchase whose workspace names no
# subscription is a finding, because a licence the installation cannot renew is a
# licence that stops.
step_entitlement() {
  poll_reset
  local code found="" ends_at="" tier="" seats="" kind="" source="" status="" ent_id=""
  while :; do
    code=$(api_call GET /api/entitlements ent)
    if [ "$code" = 401 ]; then
      unmeasured entitlement "GET /api/entitlements answered 401 with this token: the sign-in stopped working between the reads above. Sign in again and copy the fresh token"
      stop "the API refused the sign-in, so the purchase could not be read"
    fi
    if [ "$code" != 200 ]; then
      unmeasured entitlement "GET /api/entitlements answered ${code:-nothing} (curl said: $(last_lines "$SCRATCH/ent.curl.err")): the read this row is made of never completed"
      stop "GET /api/entitlements did not answer, so the purchase was not read"
    fi
    entitlement_list "$SCRATCH/ent.body" "$WORKSPACE_ID" >"$SCRATCH/ent.txt"
    ent_id=$(field "$SCRATCH/ent.txt" id)
    source=$(field "$SCRATCH/ent.txt" source)
    status=$(field "$SCRATCH/ent.txt" status)
    tier=$(field "$SCRATCH/ent.txt" tier)
    seats=$(field "$SCRATCH/ent.txt" seats)
    kind=$(field "$SCRATCH/ent.txt" kind)
    ends_at=$(field "$SCRATCH/ent.txt" ends_at)
    if [ -n "$ent_id" ] && [ "$source" = purchase ] && [ "$status" = active ]; then
      found=yes
      break
    fi
    if ! poll_again; then break; fi
  done
  if [ "$found" != yes ]; then
    unmeasured entitlement "no purchase entitlement for the workspace $WORKSPACE_ID appeared at GET /api/entitlements within ${WAIT_WINDOW}s (the workspace's own row was ${ent_id:+$ent_id, source '${source:-none}', status '${status:-none}'}${ent_id:-absent}). Pay the sandbox transaction $TXN_ID (the MANUAL line above names how) and run the check again"
    stop "the paid purchase did not appear at the portal, so the entitlement was not read"
  fi
  ENT_ENDS_BEFORE=$ends_at
  ENT_TIER=$tier
  ENT_KIND=$kind
  ENT_SEATS=$seats
  # The workspace's own mirror names Paddle's subscription: an entitlement whose
  # workspace names none cannot be renewed, and the rows below read its period.
  local mcode
  mcode=$(api_call GET "/api/workspaces/$WORKSPACE_ID/subscription" mirror)
  if [ "$mcode" = 404 ]; then
    fact entitlement FAIL "the purchase entitlement $ent_id is active for the workspace $WORKSPACE_ID and GET /api/workspaces/$WORKSPACE_ID/subscription answered 404 'no_subscription': the purchase names no subscription, so nothing holds the workspace's term and the licence cannot renew. A purchase whose workspace names no subscription is the finding this row is about"
    stop "the workspace the purchase was for names no subscription"
  fi
  if [ "$mcode" != 200 ]; then
    unmeasured entitlement "the entitlement $ent_id is active for $WORKSPACE_ID and GET /api/workspaces/$WORKSPACE_ID/subscription answered ${mcode:-nothing} (curl said: $(last_lines "$SCRATCH/mirror.curl.err")): the subscription the purchase names could not be read"
    stop "the workspace's subscription could not be read, so the purchase was only half measured"
  fi
  subscription_fields "$SCRATCH/mirror.body" >"$SCRATCH/mirror.txt"
  SUB_ID=$(field "$SCRATCH/mirror.txt" id)
  local sub_ws sub_state
  sub_ws=$(field "$SCRATCH/mirror.txt" workspace_id)
  sub_state=$(field "$SCRATCH/mirror.txt" state)
  if [ -z "$SUB_ID" ]; then
    fact entitlement FAIL "the purchase entitlement $ent_id is active and the workspace's mirror answers 200 with no subscription id: the mirror's id is Paddle's own subscription id, and a purchase that names none is a licence nothing holds the term of"
    stop "the workspace's subscription mirror names no subscription"
  fi
  fact entitlement PASS "the purchase is active at the portal: entitlement $ent_id for the workspace $WORKSPACE_ID, source 'purchase', status 'active', tier '${tier:-unknown}' (${kind:-unknown}, ${seats:-?} seat(s)), ending $ends_at, and the workspace's mirror names Paddle's subscription $SUB_ID (workspace '$sub_ws', state '${sub_state:-unknown}')"
}

# step_bind: the purchase has to be the INSTALLATION's before the renewal and the
# refund can be read there. A purchase makes an unbound entitlement and mints no
# key (F25), so this is the step that binds it: ONE `fugaro licence login` in the
# sandbox, signed in as the account that bought the licence (the key is bound to
# the account whose verified address bought it, K3.3), and then the installation's
# own read held against the purchase's own tier, kind and seats. The key is never
# read, and no line of the command's output may carry one.
step_bind() {
  say "binding the purchase to the installation: $FUGARO_BIN licence login, in ${SANDBOX}"
  ( cd "$SANDBOX" || exit 1; exec "$FUGARO_BIN" licence login ) >"$SCRATCH/login.out" 2>"$SCRATCH/login.err" &
  LOGIN_PID=$!
  LOGIN_REAPED=no
  local line="" ticks=0 max=$((CLI_TIMEOUT * 2))
  while [ "$ticks" -lt "$max" ]; do
    line=$(extract_address "$SCRATCH/login.out")
    [ -n "$line" ] && break
    kill -0 "$LOGIN_PID" 2>/dev/null || break
    sleep 0.5
    ticks=$((ticks + 1))
  done
  if [ -z "$line" ]; then
    reap_login
    unmeasured bind "\`$FUGARO_BIN licence login\` in $SANDBOX printed no sign-in address within ${CLI_TIMEOUT}s (exit $LOGIN_STATUS): the purchase cannot be bound to the installation without one, and the renewal and the install rows below read the INSTALLATION's own licence. What it printed is in $SCRATCH/login.out and $SCRATCH/login.err"
    stop "the installation's sign-in printed no address, so the purchase was not bound to it"
  fi
  manual "open $line and sign in as the account that bought this licence — the workspace $WORKSPACE_ID's own account, which the browser the login command opens is already at. The key is bound to the account whose verified address bought it (K3.3), which is what makes the renewal and the refund read THIS installation. Nothing is typed at this terminal and the check never sees your key; wait for the terminal to finish by itself"
  say "waiting up to ${WAIT_WINDOW}s for the sign-in to come back"
  ticks=0
  max=$((WAIT * 2))
  while kill -0 "$LOGIN_PID" 2>/dev/null; do
    [ "$ticks" -lt "$max" ] || break
    sleep 0.5
    ticks=$((ticks + 1))
  done
  if kill -0 "$LOGIN_PID" 2>/dev/null; then
    login_stop
    unmeasured bind "the sign-in was not finished within ${WAIT_WINDOW}s: the installation is still not bound to the purchase, so every row below would read a licence that is not this purchase's. Sign in again (raise CHECK564_WAIT if this machine is slow) and run the check again. Nothing was bought twice and nothing was written"
    stop "the sign-in was not finished in ${WAIT_WINDOW}s, so the purchase was not bound to the installation"
  fi
  reap_login
  if [ "$LOGIN_STATUS" != 0 ]; then
    unmeasured bind "\`$FUGARO_BIN licence login\` in $SANDBOX ended with exit $LOGIN_STATUS: the installation was not bound to the purchase, so the rows below would read a licence that is not this purchase's. Sign in as the account that bought it, then run the check again. What it printed is in $SCRATCH/login.out and $SCRATCH/login.err"
    stop "the sign-in ended with a refusal, so the purchase was not bound to the installation"
  fi
  if grep -q 'fgk_' "$SCRATCH/login.out" "$SCRATCH/login.err" 2>/dev/null; then
    fact bind FAIL "\`$FUGARO_BIN licence login\`'s own output carries a key-shaped string ('fgk_'): no line of the command's output may carry the key (K3.3, F5). The value is not printed here"
    stop "the sign-in's own output carries a key-shaped string"
  fi
  run_bounded "$CLI_TIMEOUT" bind "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured bind "\`$FUGARO_BIN licence status --json\` did not answer within ${CLI_TIMEOUT}s in $SANDBOX after the sign-in: the installation signed in and this check cannot read what it now holds"
    stop "the installation's licence status did not answer, so the bind row was not read"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured bind "\`$FUGARO_BIN licence status --json\` exited $BOUNDED_STATUS in $SANDBOX after the sign-in: $(last_lines "$SCRATCH/bind.err")$(last_lines "$SCRATCH/bind.out")"
    stop "the installation's licence status did not answer, so the bind row was not read"
  fi
  licence_fields "$SCRATCH/bind.out" >"$SCRATCH/bind.txt"
  local source state tier kind seats
  source=$(field "$SCRATCH/bind.txt" source)
  state=$(field "$SCRATCH/bind.txt" state)
  tier=$(field "$SCRATCH/bind.txt" tier)
  kind=$(field "$SCRATCH/bind.txt" kind)
  seats=$(field "$SCRATCH/bind.txt" seats)
  if [ "$source" != project ]; then
    fact bind FAIL "\`$FUGARO_BIN licence status --json\` in $SANDBOX reads source '${source:-none}' rather than 'project' after the sign-in (state '${state:-none}'): K3.3's sign-in writes a key of the signed-in account's own project, and a key from anywhere else is not the licence this purchase made. The rows below would be about somebody else's key"
    stop "the installation's key did not come from the sign-in's own project"
  fi
  if [ -n "$ENT_TIER" ] && [ "$tier" != "$ENT_TIER" ]; then
    unmeasured bind "the purchase is tier '$ENT_TIER' (${ENT_KIND:-unknown}, ${ENT_SEATS:-?} seat(s)) and the installation's own read after the sign-in is tier '${tier:-none}' (${kind:-unknown}, ${seats:-?} seat(s)): the account signed in for a licence that is not the one this purchase made, so the rows below would not be about it. Sign in as the account that bought the licence for $WORKSPACE_ID — or, if this purchase is the licence you mean to measure, take the key the portal issued for it — and run the check again"
    stop "the installation signed in for a licence other than the one this purchase made"
  fi
  fact bind PASS "the installation is bound to the purchase: \`$FUGARO_BIN licence login\` in $SANDBOX wrote a key of the signed-in account's own project (source 'project'), and \`$FUGARO_BIN licence status --json\` describes the licence this purchase made (tier '${tier:-unknown}', ${kind:-unknown}, ${seats:-?} seat(s), state '${state:-none}') against the purchase's own tier '$ENT_TIER'. The key was never read by this check, and no line of the sign-in's output carried one"
}

# step_renewal: Paddle's own period end and the portal's own end agree, and after
# the renewal is billed the term has MOVED. The agreement is the portal mirroring
# Paddle (P4.3); the move is Paddle's event reaching v1, which is K2.5's own
# claim. A term that did not move is a row this check could NOT measure — not a
# finding — because it may mean the sandbox did not bill.
step_renewal() {
  local code
  code=$(paddle_call GET "/subscriptions/$SUB_ID" sub)
  if [ "$code" = 404 ]; then
    unmeasured renewal "GET $PADDLE_API/subscriptions/$SUB_ID answered 404: the sandbox does not hold the subscription the portal mirrors, so Paddle's own period end cannot be read. This is a state of the sandbox and not of the release"
    stop "the sandbox holds no subscription for the one the portal mirrors, so the renewal could not be read"
  fi
  if [ "$code" != 200 ]; then
    unmeasured renewal "GET $PADDLE_API/subscriptions/$SUB_ID answered ${code:-nothing} (curl said: $(last_lines "$SCRATCH/paddle-sub.curl.err")): Paddle's own period end could not be read"
    stop "the subscription could not be read from the sandbox, so the renewal could not be measured"
  fi
  paddle_sub_fields "$SCRATCH/paddle-sub.body" >"$SCRATCH/paddle-sub.txt"
  local period_end next_billed
  period_end=$(field "$SCRATCH/paddle-sub.txt" period_ends_at)
  next_billed=$(field "$SCRATCH/paddle-sub.txt" next_billed_at)
  # The portal's mirror of the same period end.
  local mcode
  mcode=$(api_call GET "/api/workspaces/$WORKSPACE_ID/subscription" mirror2)
  if [ "$mcode" != 200 ]; then
    unmeasured renewal "GET /api/workspaces/$WORKSPACE_ID/subscription answered ${mcode:-nothing} on the second read: the portal's own period end could not be compared with Paddle's"
    stop "the portal's period end could not be re-read, so the two could not be compared"
  fi
  subscription_fields "$SCRATCH/mirror2.body" >"$SCRATCH/mirror2.txt"
  local portal_renews portal_ends
  portal_renews=$(field "$SCRATCH/mirror2.txt" renews_at)
  portal_ends=$(field "$SCRATCH/mirror2.txt" ends_at)
  local portal_end=${portal_renews:-$portal_ends}
  if [ -n "$period_end" ] && [ -n "$portal_end" ] && [ "$period_end" != "$portal_end" ]; then
    fact renewal FAIL "Paddle says the subscription $SUB_ID's current period ends at $period_end and the portal's own mirror says $portal_end: the portal mirrors what Paddle says and never decides it (P4.3), so a period end the two disagree about is a term the installation would read wrong"
    stop "the portal and Paddle disagree about the subscription's period end"
  fi
  manual "the purchase is active, bound and its term is $ENT_ENDS_BEFORE. Bill the renewal: in the Paddle SANDBOX, take the subscription $SUB_ID through its next billing period with Paddle's own sandbox billing (the sandbox dashboard's subscription screen for $SUB_ID, or Paddle's sandbox API — this check names no command it cannot verify), so that Paddle sends the renewal event. The check then runs the installation's own renewal job ($GCLOUD_BIN scheduler jobs run $SCHEDULER_JOB --project $SANDBOX_PROJECT --location $SCHEDULER_REGION in $SANDBOX) and waits up to ${WAIT_WINDOW}s for the term to move. What to paste back: what you billed and when"
  # The installation's own renewal: the documented scheduler invocation, which is
  # the command that makes the installation re-read its licence. It writes nothing
  # to the portal; it is here because the term's move is read where the
  # installation reads it, and because the command is one the owner runs by hand
  # otherwise.
  run_bounded "$CLI_TIMEOUT" renew "$GCLOUD_BIN" scheduler jobs run "$SCHEDULER_JOB" --project "$SANDBOX_PROJECT" --location "$SCHEDULER_REGION"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured renewal "\`$GCLOUD_BIN scheduler jobs run $SCHEDULER_JOB --location $SCHEDULER_REGION\` did not answer within ${CLI_TIMEOUT}s in the project $SANDBOX_PROJECT: the installation's own renewal was not triggered"
    stop "the installation's renewal job did not answer, so the term's move was not measured"
  fi
  # A fire that failed is not a finding about the release: the term's move is only
  # ever read after this fire, so there is nothing below to read as one.
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured renewal "\`$GCLOUD_BIN scheduler jobs run $SCHEDULER_JOB --project $SANDBOX_PROJECT --location $SCHEDULER_REGION\` exited $BOUNDED_STATUS ($(last_lines "$SCRATCH/renew.err")): the installation's own renewal was NOT triggered, and the term's move is read only after that fire. Nothing about the renewal was measured — run that command yourself, read what it says, and run the check again"
    stop "the renewal job could not be fired, so the term's move was not measured"
  fi
  # The term's move, read at the portal, by the bounded wait.
  poll_reset
  local moved=no ent_after=""
  while :; do
    local c
    c=$(api_call GET /api/entitlements ent2)
    if [ "$c" = 200 ]; then
      entitlement_list "$SCRATCH/ent2.body" "$WORKSPACE_ID" >"$SCRATCH/ent2.txt"
      ent_after=$(field "$SCRATCH/ent2.txt" ends_at)
      if [ -n "$ent_after" ] && [ "$ent_after" != "$ENT_ENDS_BEFORE" ] && [ "$ent_after" \> "$ENT_ENDS_BEFORE" ]; then
        moved=yes
        break
      fi
    fi
    if ! poll_again; then break; fi
  done
  if [ "$moved" != yes ]; then
    unmeasured renewal "the entitlement's term did not move within ${WAIT_WINDOW}s of the renewal: it ended $ENT_ENDS_BEFORE before and ${ent_after:-unread} after. This is a row this check could NOT measure — the sandbox may not have billed the renewal, which is a state of the sandbox and not of the release. Bill the next period for $SUB_ID (Paddle's sandbox billing) and run the check again"
    stop "the renewal did not move the entitlement's term, so K2.5's own claim was not measured"
  fi
  fact renewal PASS "Paddle's current period end ${period_end:-unknown} (next billed ${next_billed:-unknown}) and the portal's own mirror ${portal_end:-unknown} agree, and after the renewal the entitlement's term moved from $ENT_ENDS_BEFORE to $ent_after: Paddle's renewal reached v1 and moved the licence the installation reads"
}

# step_refund: the refund withdraws the WHOLE licence. D2147's rule is that the
# event resolves to the entitlement and not to a key — every key the licence ever
# minted is withdrawn — and the portal's own record (`status: revoked`) is what
# says so. No key list is read: the entitlement's revocation IS the whole
# withdrawal (F29).
step_refund() {
  manual "refund the transaction $TXN_ID in the Paddle SANDBOX (the sandbox dashboard's transaction screen for $TXN_ID, or Paddle's sandbox API — this check names no command it cannot verify). The check waits up to ${WAIT_WINDOW}s for the entitlement to read 'revoked'. What to paste back: the refund you made and Paddle's own answer"
  poll_reset
  local revoked=no ent_id="" status="" ends_at=""
  while :; do
    local c
    c=$(api_call GET /api/entitlements ent3)
    if [ "$c" = 200 ]; then
      entitlement_list "$SCRATCH/ent3.body" "$WORKSPACE_ID" >"$SCRATCH/ent3.txt"
      ent_id=$(field "$SCRATCH/ent3.txt" id)
      status=$(field "$SCRATCH/ent3.txt" status)
      ends_at=$(field "$SCRATCH/ent3.txt" ends_at)
      if [ "$status" = revoked ]; then
        revoked=yes
        break
      fi
    fi
    if ! poll_again; then break; fi
  done
  if [ "$revoked" != yes ]; then
    unmeasured refund "the entitlement of the workspace $WORKSPACE_ID did not read 'revoked' within ${WAIT_WINDOW}s of the refund (it read status '${status:-none}'). A refund whose event did not reach the portal is a row this check could not measure: make the refund and run the check again"
    stop "the refund did not withdraw the entitlement at the portal, so D2147's rule was not measured"
  fi
  REFUNDED_ENT=$ent_id
  REFUND_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  fact refund PASS "the entitlement $ent_id reads 'revoked' after the refund of the transaction $TXN_ID: the withdrawal resolved to the ENTITLEMENT and not to a key (D2147) — every key the licence ever minted is withdrawn (F29), and the term it held (ending ${ends_at:-unknown}) is kept on the record"
}

# step_tick: ONE more fire, AFTER the refund. The installation records a
# withdrawal only when its own check asks the licence server (K4.1) — the portal's
# record and the local store both say nothing to the installation until then — so
# the install row needs a check recorded after the refund, and this is what makes
# one happen. The execution is the one the fire STARTED: read back by its own
# creation time against the instant of the fire, because an execution that ran
# earlier (last night's tick, say) is not this fire's answer.
step_tick() {
  local started
  started=$(date -u +%s)
  say "firing $SCHEDULER_JOB once more, so the installation checks its licence after the refund..."
  local rc=0
  "$GCLOUD_BIN" scheduler jobs run "$SCHEDULER_JOB" --project "$SANDBOX_PROJECT" --location "$SCHEDULER_REGION" >"$SCRATCH/tick-fire.out" 2>"$SCRATCH/tick-fire.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured tick "\`$GCLOUD_BIN scheduler jobs run $SCHEDULER_JOB --project $SANDBOX_PROJECT --location $SCHEDULER_REGION\` exited $rc ($(last_lines "$SCRATCH/tick-fire.err")): no check has been asked for since the refund, so the installation has not heard about the withdrawal and the install row below could not be measured. Fire it yourself and run the check again"
    stop "the renewal job could not be fired after the refund, so the installation was not made to check"
  fi
  poll_reset
  local found=no exec_name="" exec_created="" exec_completed=""
  while :; do
    "$GCLOUD_BIN" run jobs executions list --job "$LICENCE_JOB" --project "$SANDBOX_PROJECT" --region "$SANDBOX_REGION" --limit 5 --format=json >"$SCRATCH/tick-execs.json" 2>"$SCRATCH/tick-execs.err" || true
    executions_fields "$SCRATCH/tick-execs.json" "$started" >"$SCRATCH/tick-execs.txt"
    exec_name=$(field "$SCRATCH/tick-execs.txt" name)
    exec_created=$(field "$SCRATCH/tick-execs.txt" created)
    exec_completed=$(field "$SCRATCH/tick-execs.txt" completed)
    if [ "$(field "$SCRATCH/tick-execs.txt" found)" = yes ] && [ "$exec_completed" = True ]; then
      found=yes
      break
    fi
    if ! poll_again; then break; fi
  done
  if [ "$found" != yes ]; then
    unmeasured tick "no execution of the Cloud Run job $LICENCE_JOB created after this run fired $SCHEDULER_JOB had completed within ${WAIT_WINDOW}s (the newest was ${exec_name:-none}, created ${exec_created:-unknown}, Completed '${exec_completed:-nothing}'; \`$GCLOUD_BIN run jobs executions list\` said $(last_lines "$SCRATCH/tick-execs.err")). The job's own lines for this fire are what say why (\`$GCLOUD_BIN logging read 'resource.type=\"cloud_run_job\" AND resource.labels.job_name=\"$LICENCE_JOB\" AND labels.\"run.googleapis.com/execution_name\"=\"$exec_name\"' --freshness 30m --limit 50\`), and the check's own log reader in check560.sh reads the same way. Nothing about the tick was measured"
    stop "the tick the refund needs did not run to completion, so the installation was not made to record the withdrawal"
  fi
  fact tick PASS "firing $SCHEDULER_JOB after the refund started the Cloud Run job $LICENCE_JOB's execution $exec_name (created $exec_created, after the fire), which COMPLETED: the installation's own check ran after the refund, which is what makes the install row below about the withdrawal rather than about the previous read"
}

# step_install: the installation's own read after the refund. Three things have
# to hold together: the key is STILL INSTALLED and still describes itself (the
# tier and the seats are readable, D436 — a withdrawal deletes nothing), the
# licence reads revoked, and the last check recorded the revocation. A key still
# SERVING is the finding.
step_install() {
  run_bounded "$CLI_TIMEOUT" status "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured install "$FUGARO_BIN licence status --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX: the installation's own read after the refund was not taken"
    stop "the installation's licence status did not answer, so the install row was not read"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured install "$FUGARO_BIN licence status --json exited $BOUNDED_STATUS in $SANDBOX after the refund: $(last_lines "$SCRATCH/status.err")$(last_lines "$SCRATCH/status.out")"
    stop "the installation's licence status did not answer, so the install row was not read"
  fi
  licence_fields "$SCRATCH/status.out" >"$SCRATCH/status.txt"
  local revoked state tier seats kind check_status check_at freshness
  revoked=$(field "$SCRATCH/status.txt" revoked)
  state=$(field "$SCRATCH/status.txt" state)
  tier=$(field "$SCRATCH/status.txt" tier)
  seats=$(field "$SCRATCH/status.txt" seats)
  kind=$(field "$SCRATCH/status.txt" kind)
  check_status=$(field "$SCRATCH/status.txt" check_status)
  check_at=$(field "$SCRATCH/status.txt" check_at)
  # The installation hears about the withdrawal from its OWN check and from
  # nowhere else, so an installation whose last check was recorded before the
  # refund has not heard about it: that is a row this check could not measure (the
  # tick above asked and the job's lines say why), never a FAIL. Only a check
  # recorded AFTER the refund makes the rows below about the withdrawal.
  freshness=$(stamp_after "$check_at" "$REFUND_AT")
  if [ "$freshness" != yes ]; then
    unmeasured install "\`$FUGARO_BIN licence status --json\` in $SANDBOX reads last_check.at '${check_at:-none}', which is not after the instant this run saw the refund land at the portal (${REFUND_AT:-unknown}): the installation records a withdrawal when its own check asks the licence server (K4.1), and no check has been recorded since the refund. The tick row above fired $SCHEDULER_JOB, so the job's own lines for that fire are what say why. Nothing about the install row was measured"
    stop "the installation has recorded no check since the refund, so it has not heard about the withdrawal and the install row was not measured"
  fi
  if [ "$revoked" != true ]; then
    fact install FAIL "\`$FUGARO_BIN licence status --json\` in $SANDBOX reads revoked=$revoked (state '${state:-none}') after the refund of $TXN_ID: the entitlement is revoked at the portal and the key this installation runs on STILL SERVES, which is the finding this check exists for — a refund that left a key serving (D2147)"
    stop "the refund left the installation's key serving"
  fi
  if [ "$check_status" != revoked ]; then
    fact install FAIL "\`$FUGARO_BIN licence status --json\` reads revoked=true and last_check.status '${check_status:-none}' rather than 'revoked': the key stopped serving, and the check that would have recorded why did not. Read this as a revoked key whose last check is not the revocation's own"
    stop "the installation's key reads revoked and its last check is not the revocation's"
  fi
  if [ -z "$tier" ] && [ -z "$seats" ] && [ -z "$kind" ]; then
    fact install FAIL "\`$FUGARO_BIN licence status --json\` reads revoked=true and describes nothing else: the tier, the seats and the kind are gone, and a withdrawal deletes NOTHING (D436) — a customer whose revoked key no longer says what it was cannot be told from one whose key was never read"
    stop "the revoked key no longer describes itself, so D436's own rule is broken"
  fi
  fact install PASS "\`$FUGARO_BIN licence status --json\` in $SANDBOX reads revoked=true with last_check.status 'revoked', and the key is STILL INSTALLED and still describes itself (tier '${tier:-unknown}', ${kind:-unknown}, ${seats:-?} seat(s), state '${state:-none}'): the refund stopped the key serving and deleted nothing (D436)"
}

# step_restore: what this run leaves behind, named so the owner can settle it.
# The sandbox objects and the withdrawn entitlement are the check's own and stay
# where they are; the installation holds a revoked key, and the MANUAL line names
# the command that replaces it.
step_restore() {
  manual "restore the sandbox installation: it holds a REVOKED key after this run. Take a fresh key for the workspace $WORKSPACE_ID from the portal (or buy a new sandbox licence) and install it with \`$FUGARO_BIN licence set\` in $SANDBOX — this check never writes a key itself, so the install is yours. The portal's withdrawn entitlement and the sandbox transaction $TXN_ID, its subscription $SUB_ID and its refund stay where they are: they are the sandbox's own"
  fact restore PASS "what this check made is named rather than hidden: the Paddle SANDBOX transaction $TXN_ID, its subscription $SUB_ID and its refund; the withdrawn entitlement ${REFUNDED_ENT:-<unread>} in the deployment $API; and the revoked key in the throwaway installation $SANDBOX. The sandbox objects and the withdrawn entitlement are left as they are — they are the sandbox's — and the revoked key is replaced by the MANUAL line above"
}

plan() {
  say "CHECK 564 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read-only] GET ${API}/api/workspaces with no sign-in — 401 'unauthenticated', which says the origin is the portal's API"
  say "  [local, read-only] ${FUGARO_BIN} doctor --json in ${SANDBOX} — the installation, its GCP project and its region"
  say "  [network, sandbox read-only] GET ${PADDLE_API}/prices?status=active with the sandbox API key — the count and the first price id; the key never leaves a 0600 curl config"
  say "  [network, WOULD CREATE] POST ${API}/api/checkout for the workspace ${WORKSPACE_ID} — a real transaction in the Paddle SANDBOX, then GET ${PADDLE_API}/transactions/{id} to hold that the sandbox minted it"
  say "  [browser + network] pay the sandbox overlay, then poll ${API}/api/entitlements and ${API}/api/workspaces/${WORKSPACE_ID}/subscription — the entitlement and the subscription it names"
  say "  [local, read-only] ${GCLOUD_BIN} scheduler locations list and jobs describe for $SCHEDULER_JOB — the Scheduler location the renewal job is in, which is NOT the installation's Cloud Run region (K4.2), so the fire is aimed where the job is"
  say "  [browser + network] sign in ONCE at ${FUGARO_BIN} licence login in ${SANDBOX} as the account that bought, then hold the installation's own read against the purchase's tier — the purchase mints no key by itself (F25)"
  say "  [browser + network] bill the renewal, then hold Paddle's period end and the portal's agree and the term MOVED"
  say "  [browser + network] refund the sandbox transaction, then hold the entitlement reads 'revoked' — the whole licence (D2147)"
  say "  [fire + read] fire ${GCLOUD_BIN} scheduler jobs run $SCHEDULER_JOB once more after the refund and wait for a completed execution of the Cloud Run job $LICENCE_JOB created after that fire — the installation hears about a withdrawal only from its own check (K4.1)"
  say "  [read] ${FUGARO_BIN} licence status --json in ${SANDBOX} — revoked=true, last_check 'revoked' and last_check.at AFTER the refund, the tier and the seats still readable (D436)"
  say "  [no write by this check] the revoked key in ${SANDBOX} is replaced by you, per the restore MANUAL line"
  fact dry-run PASS "the plan was printed and nothing was run: no portal call, no sandbox call and no install — and the two MANUAL steps that buy and refund are named above as the acts they are"
}

main() {
  need_tools
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
  step_sandbox
  step_scheduler
  step_paddle_sandbox
  step_checkout
  step_entitlement
  step_bind
  step_renewal
  step_refund
  step_tick
  step_install
  step_restore
}

# ---------------------------------------------------------------------- inputs
PADDLE_SANDBOX=https://sandbox-api.paddle.com
TXN_ID=""
SUB_ID=""
ENT_ENDS_BEFORE=""
REFUNDED_ENT=""
SANDBOX_PROJECT=""
SANDBOX_REGION=""
# The two names K4.2 gives these resources, spelled as internal/infra/licence.go
# spells them: the Cloud Run job must not start with fugaro- (that is what
# `fugaro runs ls` counts as a workflow job), and the schedule job carries the
# prefix.
LICENCE_JOB=fugarolicence
SCHEDULER_JOB=fugaro-licence-renew
# Where the Scheduler job lives. Scheduler is not offered in every Cloud Run
# region (K4.2's module comment), so this is resolved rather than assumed to be
# the installation's own region.
SCHEDULER_REGION=""
# The instant this run saw the refund land at the portal. The installation hears
# about a withdrawal only from its own check, so the install row needs a check
# recorded after this instant.
REFUND_AT=""
# The purchase's own terms, kept so the bind row can hold the key the
# installation signed in for against the licence this purchase issued.
ENT_TIER=""
ENT_KIND=""
ENT_SEATS=""

DRY_RUN_CLI=""
SANDBOX_CLI=""
TOKEN_CLI=""
PADDLE_KEY_CLI=""
API_CLI=""
WORKSPACE_CLI=""
TERMS_CLI=""
PRODUCTION_CLI=""
SCHEDULER_REGION_CLI=""
USAGE="check564.sh --sandbox <dir> --token-file <file> --paddle-key-file <file> [--api <origin>] [--workspace-id <id>] [--terms <row>] [--scheduler-region <region>] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a directory. usage: $USAGE"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --token-file)
    [ $# -ge 2 ] || die "--token-file needs a path. usage: $USAGE"
    TOKEN_CLI=$2
    shift 2
    ;;
  --paddle-key-file)
    [ $# -ge 2 ] || die "--paddle-key-file needs a path. usage: $USAGE"
    PADDLE_KEY_CLI=$2
    shift 2
    ;;
  --api)
    [ $# -ge 2 ] || die "--api needs the portal's origin. usage: $USAGE"
    API_CLI=$2
    shift 2
    ;;
  --workspace-id)
    [ $# -ge 2 ] || die "--workspace-id needs an id. usage: $USAGE"
    WORKSPACE_CLI=$2
    shift 2
    ;;
  --terms)
    [ $# -ge 2 ] || die "--terms needs the licence terms version. usage: $USAGE"
    TERMS_CLI=$2
    shift 2
    ;;
  --scheduler-region)
    [ $# -ge 2 ] || die "--scheduler-region needs a region. usage: $USAGE"
    SCHEDULER_REGION_CLI=$2
    shift 2
    ;;
  --production)
    PRODUCTION_CLI=1
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

SANDBOX=${CHECK564_SANDBOX:-${SANDBOX_CLI:-}}
TOKEN_FILE=${CHECK564_TOKEN_FILE:-${TOKEN_CLI:-}}
PADDLE_KEY_FILE=${CHECK564_PADDLE_KEY_FILE:-${PADDLE_KEY_CLI:-}}
WORKSPACE_ID=${CHECK564_WORKSPACE_ID:-${WORKSPACE_CLI:-}}
TERMS=${CHECK564_TERMS:-${TERMS_CLI:-}}
PRODUCTION=${CHECK564_PRODUCTION:-${PRODUCTION_CLI:-}}
SCHEDULER_REGION=${CHECK564_SCHEDULER_REGION:-${SCHEDULER_REGION_CLI:-}}
DRY_RUN=${CHECK564_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK564_KEEP_SCRATCH:-}
API=${CHECK564_API:-${API_CLI:-}}
PADDLE_API=${CHECK564_PADDLE_API:-$PADDLE_SANDBOX}
CURL_SECONDS=${CHECK564_CURL_SECONDS:-60}
CLI_TIMEOUT=${CHECK564_CLI_TIMEOUT_SECONDS:-180}
WAIT=${CHECK564_WAIT:-30}
POLL_SECONDS=${CHECK564_POLL_SECONDS:-10}
# The bounded wait, as one number for the sentences that name it: the two knobs
# are read together so a fractional pause (a test's, above all) never reaches a
# shell arithmetic expansion.
WAIT_WINDOW=$(python3 -c 'import sys; print("%g" % (float(sys.argv[1]) * float(sys.argv[2])))' "$WAIT" "$POLL_SECONDS" 2>/dev/null || printf '%s' "$WAIT")

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
case "$PADDLE_API" in
*/) PADDLE_API=${PADDLE_API%/} ;;
esac

if [ -z "$SANDBOX" ]; then
  die "Check 564 needs --sandbox: a THROWAWAY installation's checkout, with the released \`fugaro\` in it, that this check drives. Usage: $USAGE"
fi
if [ -z "$TOKEN_FILE" ]; then
  die "Check 564 needs --token-file: a file, mode 0600, holding one line — the ID token your browser holds after signing in to the portal. Usage: $USAGE"
fi
if [ -z "$PADDLE_KEY_FILE" ]; then
  die "Check 564 needs --paddle-key-file: a file, mode 0600, holding one line — the Paddle SANDBOX API key. $PADDLE_KEY_NEED. Usage: $USAGE"
fi
if [ -z "$WORKSPACE_ID" ]; then
  die "Check 564 needs --workspace-id: the portal workspace the purchase is for (one workspace has one subscription, D2192). Usage: $USAGE"
fi
# Absolute, before anything changes directory: the readers below are handed the
# path, and a relative one would name another file after a cd.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
case "$TOKEN_FILE" in
/*) ;;
*) TOKEN_FILE="$PWD/$TOKEN_FILE" ;;
esac
case "$PADDLE_KEY_FILE" in
/*) ;;
*) PADDLE_KEY_FILE="$PWD/$PADDLE_KEY_FILE" ;;
esac

# The released CLI and gcloud are the tools this check drives; both are resolved
# here so a run that has neither says so before it makes any call.
FUGARO_CLI=${CHECK564_FUGARO:-}
if [ -z "$FUGARO_CLI" ]; then FUGARO_CLI=$(command -v fugaro || true); fi
if [ -z "$FUGARO_CLI" ]; then
  die "Check 564 needs the released \`fugaro\` CLI: the installation's own \`doctor\` and \`licence status\` are what the sandbox rows read. Install it, or pass CHECK564_FUGARO. Usage: $USAGE"
fi
case "$FUGARO_CLI" in
/*) FUGARO_BIN="$FUGARO_CLI" ;;
*) FUGARO_BIN="$PWD/$FUGARO_CLI" ;;
esac
[ -x "$FUGARO_BIN" ] || die "CHECK564_FUGARO '$FUGARO_BIN' is not an executable file. Usage: $USAGE"
GCLOUD_CLI=${CHECK564_GCLOUD:-}
if [ -z "$GCLOUD_CLI" ]; then GCLOUD_CLI=$(command -v gcloud || true); fi
if [ -z "$GCLOUD_CLI" ]; then
  die "Check 564 needs \`gcloud\`: the renewal row runs the installation's own renewal job (gcloud scheduler jobs run fugaro-licence-renew). Install it, or pass CHECK564_GCLOUD. Usage: $USAGE"
fi
GCLOUD_BIN=$GCLOUD_CLI

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
