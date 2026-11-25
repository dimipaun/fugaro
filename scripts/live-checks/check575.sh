#!/usr/bin/env bash
# Check 575 — a staging reseller's month: its accrual, its statement's mail, and
# a sale the credit limit refuses.
#
# Plan task P9.4 of docs/portal/implementation-plan.md writes this check, and the
# number is that work's own (its "Checks this plan takes (570–579)"). What it
# settles is read from the outside and nowhere else: **P6.2 and P6.3 as the
# deployed portal runs them** — D2189's ceiling (a reseller's accrual is days
# times seats times the wholesale rate, and a sale that would pass the credit
# limit operations set is refused with `over_credit_limit`), the month's statement
# (one line per customer, the sum of the part of each term inside a UTC month),
# and the run that issues and mails it (P1.5's Cloud Scheduler job, F33's OIDC
# token, `api_statements.go`'s own line for what the run did).
#
# What it measures, in the order the block reads:
#
#   * the API origin, with one read that carries NO sign-in: `GET
#     /api/workspaces` answers 401 `unauthenticated`;
#   * the sign-in: the token's provider has to be an SSO one (`google.com` or
#     `microsoft.com`). A reseller organisation's members sign in that way and an
#     email-link account is refused at its routes (D2207/F10 — that refusal is
#     Check 572's row, and this check is the other side of the same rule), so a
#     token of the other kind would make every row below say nothing about a
#     reseller;
#   * **the terms** (`GET /api/resellers/terms`): the credit limit and the
#     wholesale price per row that operations set. They are what the sale below is
#     composed against, and a row this reseller is not priced for is a row it
#     cannot sell (`resellers.schema.json`);
#   * **the customers and their keys** (`GET /api/resellers/customers`, `GET
#     /api/resellers/customers/{id}/keys`): one customer, and the newest key it
#     holds, which is bound to the Firebase project the sale below has to be
#     composed for. F5 is why this is all of the key a reseller sees: metadata
#     and never a key;
#   * **the workspace the sale names** (`GET
#     /api/ops/workspaces?organisation_id={customer}`, or `--workspace-id`): the
#     key's own `workspace_id` is the Firebase PROJECT id it is bound to (TN2),
#     while the mint reads the sale's `workspace_id` as a portal workspace
#     DOCUMENT id (`accrual.go`'s `mintResellerLicence` loads
#     `workspaces/{workspace_id}`), so one has to be resolved into the other. No
#     reseller route answers a portal workspace id, so the check reads the
#     operations area's own list of the customer's workspaces and takes the one
#     whose `project_id` is the key's project id. That read needs an account that
#     is a member of the operations organisation — the owner's own is, on
#     staging, P0.5 — and it writes the operations log's own line (D456), so
#     `--workspace-id` names the workspace instead, for a token that is a
#     reseller's and nothing else;
#   * **the job** (`gcloud scheduler jobs describe`): P1.5's monthly statement job
#     — its method and path, the account its OIDC token is minted for, the
#     audience that token carries, and whether the deployment has lifted the pause
#     the apply creates it under;
#   * **the run and its mail**: the check FIRES that job (`gcloud scheduler jobs
#     run`), and then reads the API's own log line for it — `the statement run as
#     <account> issued N statement(s) and mailed M message(s)` — which is what
#     `api_statements.go` says an operator reads of a run. The account in that
#     line has to be the job's own token's, which is what makes the line this
#     fire's and not an earlier run's; the counts are the run's own;
#   * **the month** (`GET /api/statements`): the reseller's newest statement, held
#     to the arithmetic the schema promises — every line positive, the lines'
#     sum equal to the statement's `accrued_micros`, `state` following the two
#     amounts, the month a whole UTC month that is over, and every line a customer
#     this reseller holds. This is the accrual read as the month it was billed in
#     rather than recomputed here;
#   * **the refused sale** (`POST /api/resellers/customers/{id}/keys`): the check
#     composes a sale of its own arithmetic — the customer's workspace resolved
#     above, a row, a seat count and a span whose cost is strictly greater than
#     the WHOLE credit limit, so that no state of the accrual can admit it — and
#     holds the answer to 409 `over_credit_limit`
#     with the sentence `This sale would pass the credit limit operations set for
#     this reseller. The licences it already holds are unchanged.` The customer's
#     keys are read before and after it: that sentence's own claim, measured.
#
# What it creates: **the run it fires, and the operations log's own line for the
# workspace read** (`/api/ops/workspaces` is one of the operations area's reads,
# and every one of them is written into that log before it answers, D456) —
# nothing else, and `--workspace-id` skips even that. The refused sale writes
# nothing, and the check writes no customer, no term and no licence of its own.
# Firing P1.5's job is the one act here, and it is the act the deployment does by
# itself once a month (the first of the month, 03:00 UTC): the run is idempotent
# per month (`issueStatement` reads a month it already holds), so a second fire
# states no month twice, and a month it does state is mailed to that reseller's
# owners — on staging, through the merchant's and the mail provider's test modes
# (P0.2). If the deployment's job is still PAUSED, this check fires it by hand
# and says so: a job left paused is a month nobody is billed for.
#
# Usage:      bash scripts/live-checks/check575.sh --project <p> --token-file <file>
#             bash scripts/live-checks/check575.sh --project <p> --token-file <file> --dry-run
#             bash scripts/live-checks/check575.sh --help
#             --project is the project the portal is deployed in (fugaro-staging
#             on staging), and --token-file is a file YOU wrote, mode 0600,
#             holding ONE line: the ID token your browser holds after signing in
#             to the portal as a MEMBER OF A RESELLER ORGANISATION with Google or
#             Microsoft. --customer names the customer to read (the newest link by
#             default), --row the policy row the refused sale is made on
#             (team-annual by default), --days the span it asks for,
#             --workspace-id the portal workspace id the refused sale names (read
#             from the customer's own workspaces through the operations read when
#             it is not given: the token's account has to be a member of the
#             operations organisation for that, and a reseller's own is not),
#             --api-service the API's Cloud Run service and --statement-job
#             P1.5's own job.
# Needs:      bash, mktemp, python3 (every document is read with it), curl (the
#             portal API) and gcloud, authenticated to an account that may read
#             Cloud Logging and describe and run Cloud Scheduler jobs in
#             --project, and a sign-in whose account is a member of the RESELLER
#             organisation — plus, for the sale's workspace, one of: a membership
#             of the operations organisation (the token's own account, on staging
#             the owner's, seeded by P0.5) or --workspace-id. No `fugaro` command
#             runs at all.
# Where:      your own machine and the STAGING deployment (`fugaro-staging`)
#             first. `fugaro-prod` is refused unless `--production` is given,
#             because whoever reads a production reseller's month is reading real
#             money; a value naming belong is refused by name; and so is a coding
#             agent's session.
# Costs:      nothing billable: a handful of reads, one refused sale and one
#             scheduler fire. The fire is the monthly run the deployment makes
#             anyway, and it issues and mails only what the month owed.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check575_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory — which holds this check's copy of the
#             composed sale and the tools' output — removed from the exit trap
#             even on a failed assertion or a Ctrl-C. Nothing in the project is
#             this check's to undo: the refused sale wrote nothing.
# Output:     progress on stderr, ONE block on stdout headed CHECK 575 RESULT,
#             then CHECK 575 RESULT END (exit N) — to copy back into the
#             register's Check 575 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold — a sale beyond the limit that was ADMITTED, a month whose
#             lines do not sum to what it says it accrued, a job whose token is
#             minted for the wrong account or audience — or a guard stopped the
#             check. 2: a row could not be measured for a reason that is this
#             check's own setup or the staging data — a token file that is not
#             0600, an account that is not a member of a reseller organisation, a
#             customer that holds no key, a job the deployment does not have. A
#             key the run never reached reads `FAIL — not reached`, which is a row
#             that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no ID token, in whole or in part, and no
# address, uid or name of the account. The token travels in a 0600 curl config
# file and reaches neither a command line nor the block; its payload is decoded in
# the scratch directory (mode 0700, removed at the end) to read three claims, and
# what the block carries of it is the provider spelling, the `aud` project id and
# an expiry state — never a claim that names the person. Organisation ids, key ids
# and the scheduler account are named, which is what a register row is read
# against.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK575_PROJECT, CHECK575_TOKEN_FILE, CHECK575_API (the portal's origin; the
# staging default is the staging site's own domain, `app.staging.fugaro.dev`,
# and the production one is `app.fugaro.dev` — P0.1, P9.2),
# CHECK575_CUSTOMER, CHECK575_WORKSPACE_ID (empty: the sale's workspace is read
# from the customer's own workspaces — the one bound to the key's Firebase project
# id — and this names it instead when the token's account is not a member of the
# operations organisation), CHECK575_ROW (team-annual: a row whose seats the caller
# names, so the sale can be sized to the limit), CHECK575_DAYS (29: the span the
# refused sale is asked for — inside the monthly row's own 30 days and inside the
# annual rows'), CHECK575_SEATS (0: computed from the limit and the rate),
# CHECK575_API_SERVICE (fugaro-portal-api: P1.5's own default, the API's Cloud Run
# service, whose revision's environment holds the pair the job's token is held
# against), CHECK575_STATEMENT_JOB (fugaro-portal-statements: P1.5's own default),
# CHECK575_REGION (us-east5: the region the portal's own deploy script uses),
# CHECK575_STATEMENT_PATH (/api/statements/run: P1.5's statement_job_path),
# CHECK575_PRODUCTION, CHECK575_DRY_RUN, CHECK575_KEEP_SCRATCH,
# CHECK575_CURL_SECONDS (60: the bound on each call to the API),
# CHECK575_RUN_SECONDS (150: how long the fired run is given to answer in the log)
# and CHECK575_LOG_FRESHNESS (15m: the log read's own window).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a
# sign-in, and a trace of the call that carries it is a second copy of the
# Authorization header.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory. A stop right after an `unmeasured` row
# ends the run as one that could not measure (exit 2): the difference matters to
# whoever reads the block, because 1 is a finding about the release and 2 is a run
# that never got to measure it.
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
# there or ran to the end. A `stop` right after one of these rows is the check
# saying so: it ends 2, not 1, and the block names what it could not measure.
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
  for t in bash mktemp python3 curl gcloud sleep; do
    command -v "$t" >/dev/null 2>&1 || die "Check 575 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

PROJECT=""
API=""
TOKEN_FILE=""
ROW=""
DAYS=""
API_SERVICE=""
STATEMENT_JOB=""
REGION=""
STATEMENT_PATH=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
CURL_SECONDS=60
RUN_SECONDS=150
LOG_FRESHNESS=15m
CUSTOMER_ID=""
CUSTOMER_NAME=""
WORKSPACE_ID=""
KEY_PROJECT_ID=""
KEYS_BEFORE=""
JOB_ACCOUNT=""
API_ENV_ACCOUNT=""
API_ENV_AUDIENCE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check575.XXXXXX") || die "Check 575 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK575_KEEP_SCRATCH is set)"
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
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK575_KEEP_SCRATCH is set)"
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
BLOCK_KEYS=(xtrace agent-session project belong api token signin terms customers keys workspace service job run statements limit restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 575 RESULT\n' >&3
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
  printf 'CHECK 575 RESULT END (exit %s)\n' "$status" >&3
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
# caller reads the missing key as "the document did not answer", which is a row
# about the portal rather than this check's crash.

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

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

# token_fields FILE — the three claims this check reads out of the ID token's own
# payload, decoded locally with no signature check: the check is not the API, and
# a signature it cannot verify would prove nothing. Nothing else of the payload is
# read, and nothing that names the person is printed.
token_fields() { # token_fields FILE
  python3 - "$1" <<'PY'
import base64, json, sys, time

def b64(part):
    pad = "=" * (-len(part) % 4)
    return base64.urlsafe_b64decode(part + pad)

raw = open(sys.argv[1]).read().strip()
parts = raw.split(".")
if len(parts) != 3:
    print("shape=not-a-jwt")
    raise SystemExit(0)
try:
    payload = json.loads(b64(parts[1]))
except Exception:
    print("shape=undecodable")
    raise SystemExit(0)
print("shape=jwt")
firebase = payload.get("firebase") or {}
print("provider=%s" % (firebase.get("sign_in_provider") or ""))
print("aud=%s" % (payload.get("aud") or ""))
print("iss=%s" % (payload.get("iss") or ""))
print("exp=%s" % (payload.get("exp") or ""))
print("now=%s" % int(time.time()))
PY
}

# terms_fields FILE — the reseller's own commercial terms: the credit limit (in
# micro-dollars and in dollars, because a reader of the register compares the
# ceiling with what a term costs) and one `rate_<row>=<micros>` line per priced
# row. A document that is not the terms answers `error=`.
terms_fields() { # terms_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, dict) or "credit_limit_micros" not in doc:
    print("error=not the terms document this route answers")
    raise SystemExit(0)
limit = doc.get("credit_limit_micros") or 0
print("limit=%s" % limit)
print("limit_usd=%.2f" % (limit / 1_000_000))
rates = doc.get("rates") or []
print("rates=%d" % len(rates))
for rate in rates:
    print("rate_%s=%s" % (rate.get("row") or "", rate.get("daily_rate_micros") or 0))
PY
}

# customers_fields FILE — the reseller's customers, newest link first as the route
# answers them: the count, and the first one's id and name.
customers_fields() { # customers_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, list):
    print("error=not the customers list this route answers")
    raise SystemExit(0)
print("count=%d" % len(doc))
if doc:
    print("first=%s" % (doc[0].get("organisation_id") or ""))
    print("first_name=%s" % (doc[0].get("name") or ""))
print("ids=%s" % ",".join((row.get("organisation_id") or "") for row in doc))
PY
}

# keys_fields FILE — one customer's keys as the reseller may see them (F5:
# metadata, never a key): how many, their ids, and the newest one's workspace, row
# and term. `digest` is a short hash of the sorted ids, which is how "the same
# keys after the refused sale as before it" is compared without printing a list
# nobody reads.
keys_fields() { # keys_fields FILE
  python3 - "$1" <<'PY'
import hashlib, json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, list):
    print("error=not the keys list this route answers")
    raise SystemExit(0)
print("count=%d" % len(doc))
ids = sorted((key.get("key_id") or "") for key in doc)
print("digest=%s" % hashlib.sha256(",".join(ids).encode()).hexdigest()[:12])
print("ids=%s" % ",".join(ids))
if doc:
    newest = max(doc, key=lambda key: key.get("issued_at") or "")
    print("newest=%s" % (newest.get("key_id") or ""))
    print("workspace=%s" % (newest.get("workspace_id") or ""))
    print("tier=%s" % (newest.get("tier") or ""))
    print("seats=%s" % (newest.get("seats") or 0))
    print("paid_through=%s" % (newest.get("paid_through") or ""))
PY
}

# ops_workspaces_fields FILE PROJECT_ID — one organisation's workspaces as the
# operations area answers them (WireWorkspace), held to the one thing this check
# needs from them: which of them is bound to a Firebase project id. `match` is that
# workspace's portal id and is empty when none of them carries the project, and
# `held` is every workspace the list names with its project, which is what an
# unmeasured row is read against.
ops_workspaces_fields() { # ops_workspaces_fields FILE PROJECT_ID
  python3 - "$1" "$2" <<'PY'
import json, sys

try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, list):
    print("error=not the workspaces list this route answers")
    raise SystemExit(0)
project = sys.argv[2]
held, match = [], ""
for row in doc:
    if not isinstance(row, dict) or not (row.get("id") or ""):
        print("error=an entry is not a workspace this check can read")
        raise SystemExit(0)
    bound = row.get("project_id") or ""
    held.append("%s=%s" % (row.get("id"), bound or "<unbound>"))
    if bound and bound == project:
        match = row.get("id")
print("count=%d" % len(doc))
print("match=%s" % match)
print("held=%s" % ",".join(held))
PY
}

# statements_fields FILE — the reseller's newest statement, held to the arithmetic
# the schema promises: the month, the two amounts, the state, the lines and their
# own sum. `months_ok` is 1 when the month is a whole UTC month that is over (a
# statement is issued for a month nobody can add a day to), `state_ok` is 1 when
# the state follows the two amounts, and `sum_ok` is 1 when the lines sum to what
# the statement says it accrued.
statements_fields() { # statements_fields FILE
  python3 - "$1" <<'PY'
import datetime, json, sys

try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, list):
    print("error=not the statements list this route answers")
    raise SystemExit(0)
print("count=%d" % len(doc))
if not doc:
    raise SystemExit(0)
newest = max(doc, key=lambda row: row.get("month") or "")
month = newest.get("month") or ""
accrued = newest.get("accrued_micros") or 0
paid = newest.get("paid_micros") or 0
lines = newest.get("lines") or []
total = sum((line.get("amount_micros") or 0) for line in lines)
print("month=%s" % month)
print("accrued=%s" % accrued)
print("paid=%s" % paid)
print("state=%s" % (newest.get("state") or ""))
print("lines=%d" % len(lines))
print("sum=%s" % total)
print("orgs=%s" % ",".join(sorted((line.get("organisation_id") or "") for line in lines)))
print("positive=%s" % (1 if all((line.get("amount_micros") or 0) > 0 for line in lines) else 0))
print("sum_ok=%s" % (1 if total == accrued else 0))
print("state_ok=%s" % (1 if (newest.get("state") == "paid") == (paid >= accrued and accrued > 0) else 0))
now = datetime.datetime.now(datetime.timezone.utc)
this_month = now.strftime("%Y-%m")
ok = 0
try:
    at = datetime.datetime.strptime(month, "%Y-%m").replace(tzinfo=datetime.timezone.utc)
    ok = 1 if month < this_month else 0
except Exception:
    ok = 0
print("months_ok=%s" % ok)
PY
}

# job_fields FILE — P1.5's statement job as the deployment has it: what it posts,
# where, with whose token, and whether the pause the apply creates it under has
# been lifted.
job_fields() { # job_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, dict):
    print("error=not a job document")
    raise SystemExit(0)
state = (doc.get("state") or "").upper()
target = doc.get("httpTarget") or {}
oidc = target.get("oidcToken") or {}
uri = target.get("uri") or ""
audience = oidc.get("audience") or ""
print("name=%s" % (doc.get("name", "").split("/")[-1]))
print("state=%s" % state)
print("paused=%s" % (1 if state and state != "ENABLED" else 0))
print("schedule=%s" % (doc.get("schedule") or ""))
print("time_zone=%s" % (doc.get("timeZone") or ""))
print("method=%s" % (target.get("httpMethod") or ""))
print("uri=%s" % uri)
print("path=%s" % ("/" + uri.split("//", 1)[-1].split("/", 1)[1] if "//" in uri and "/" in uri.split("//", 1)[-1] else ""))
print("host=%s" % (uri.split("//", 1)[-1].split("/", 1)[0] if "//" in uri else ""))
print("origin=%s" % (uri.split("//", 1)[0] + "//" + uri.split("//", 1)[-1].split("/", 1)[0] if "//" in uri else ""))
print("audience_origin=%s" % (audience.split("//", 1)[0] + "//" + audience.split("//", 1)[-1].split("/", 1)[0] if "//" in audience else audience))
print("account=%s" % (oidc.get("serviceAccountEmail") or ""))
print("audience=%s" % audience)
PY
}

# run_line FILE SINCE — the API's own line for the run this check fired, read out
# of the log entries gcloud answered: the newest entry whose text carries the
# sentence `api_statements.go` writes and whose own instant is not older than
# SINCE (the check's own fire, so an earlier run's line cannot be read as this
# one's). Nothing is printed when no entry qualifies.
run_line() { # run_line FILE SINCE
  python3 - "$1" "$2" <<'PY'
import datetime, json, re, sys

path, since = sys.argv[1], sys.argv[2]
phrase = "the statement run as "
try:
    entries = json.load(open(path))
except Exception:
    raise SystemExit(0)
pattern = re.compile(
    r"the statement run as (?P<account>\S+) issued (?P<issued>\d+) statement\(s\)"
    r" and mailed (?P<mailed>\d+) message\(s\)")
found = None
for entry in entries or []:
    text = entry.get("textPayload") or ""
    if not text and isinstance(entry.get("jsonPayload"), dict):
        text = entry["jsonPayload"].get("message") or ""
    if phrase not in text:
        continue
    stamp = entry.get("timestamp") or ""
    try:
        at = datetime.datetime.fromisoformat(stamp.replace("Z", "+00:00"))
    except Exception:
        continue
    if at < datetime.datetime.fromisoformat(since.replace("Z", "+00:00")):
        continue
    match = pattern.search(text)
    if not match:
        continue
    if found is None or at > found[0]:
        found = (at, match, stamp)
if found is None:
    raise SystemExit(0)
_, match, stamp = found
print("at=%s" % stamp)
print("account=%s" % match.group("account"))
print("issued=%s" % match.group("issued"))
print("mailed=%s" % match.group("mailed"))
PY
}

# env_fields FILE — one Cloud Run revision's own environment as far as this check
# reads it: the two values the API's SchedulerVerifier compares a scheduled call's
# token against (F33), which are the deployment's and not a naming convention this
# check could infer. The revision name is printed so a block says which one was
# read, and every other variable of the revision is ignored and never printed.
env_fields() { # env_fields FILE
  python3 - "$1" <<'PYENV'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}
containers = spec.get("containers") or []
if not containers:
    print("error=the revision carries no container")
    raise SystemExit(0)
print("revision=%s" % (doc.get("status", {}).get("latestReadyRevisionName") or ""))
env = {}
for item in containers[0].get("env") or []:
    if "value" in item:
        env[item.get("name") or ""] = item.get("value") or ""
print("account=%s" % env.get("FUGARO_SCHEDULER_ACCOUNT", ""))
print("audience=%s" % env.get("FUGARO_SCHEDULER_AUDIENCE", ""))
PYENV
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check575.sh --project <p> --token-file <file> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of a call carrying a sign-in is a copy of it"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the Authorization header of a call to the terminal"
}

# guard_agent_session: this check reads a sign-in YOU made in a browser and fires
# the deployment's own monthly run, which mails a reseller. A coding agent's
# session has no browser, no account of yours and no business mailing anybody, so
# a run there would measure somebody else's session or nothing at all.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 575 fires the monthly statement run and reads a reseller's own month, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_inputs: the project, the origin and the token file, read before anything
# runs. `fugaro-prod` is the one project a check never measures by accident: the
# rows here fire the monthly run and read a reseller's money, and production is
# only ever measured on purpose.
guard_inputs() {
  local all="${PROJECT:-}${API:-}${TOKEN_FILE:-}${STATEMENT_JOB:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: the rows below fire the monthly statement run and read a real reseller's month, and production is only ever measured on purpose. Nothing was read and nothing was fired"
  fi
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 570 to 575 run on fugaro-staging first, so read this block as a production run by choice"
  else
    fact project PASS "$PROJECT is the project the portal is deployed in (the staging deployment's own is fugaro-staging), and it is not fugaro-prod, which is refused without --production"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact api PASS "$API (the portal's origin; a real run holds it to one read that answers the API's own unauthenticated refusal)"
    fact token PASS "$TOKEN_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact signin PASS "planned: the token's own payload, decoded locally — its provider, its aud against $PROJECT and its expiry"
    return 0
  fi
  if [ ! -f "$TOKEN_FILE" ]; then
    unmeasured token "the token file $TOKEN_FILE is not there: this check reads the portal through YOUR OWN sign-in AS A MEMBER OF A RESELLER ORGANISATION (copy the ID token your browser holds after signing in to $API with Google or Microsoft), and a file that is not there is a row it cannot measure. Nothing about the portal was read"
    stop "the token file $TOKEN_FILE is not there, so none of the portal's rows could be read. Copy the reseller member's ID token into a file of its own, then run the check again"
  fi
  local mode
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

# step_signin: the premise. The rows below are a reseller's own, and a reseller
# organisation's members sign in with Google or Microsoft (D2207/F10): an
# email-link account is refused at every reseller route, which is Check 572's row,
# so a token of that kind would make every row here say nothing about a reseller.
step_signin() {
  token_fields "$TOKEN_FILE" >"$SCRATCH/token.txt"
  local shape provider aud exp now
  shape=$(field "$SCRATCH/token.txt" shape)
  provider=$(field "$SCRATCH/token.txt" provider)
  aud=$(field "$SCRATCH/token.txt" aud)
  exp=$(field "$SCRATCH/token.txt" exp)
  now=$(field "$SCRATCH/token.txt" now)
  case "${exp:-x}${now:-x}" in
  *[!0-9]*) exp="" now="" ;;
  esac
  if [ "$shape" != jwt ] || [ -z "$provider" ] || [ -z "$aud" ] || [ -z "$exp" ]; then
    unmeasured signin "the token file's first line is not an ID token this check can read ($shape, provider '${provider:-none}', aud '${aud:-none}'): copy the whole ID token your browser holds as a member of the reseller organisation — the three dot-separated parts and nothing else — into a file of its own at mode 0600"
    stop "the token file does not hold a readable ID token, so the check cannot say which provider signed in"
  fi
  case "$provider" in
  google.com | microsoft.com) ;;
  password | emailLink)
    unmeasured signin "the token's provider is '$provider', which is an email-link sign-in: a reseller organisation's members sign in with Google or Microsoft, and an email-link account is refused at every reseller route (D2207/F10 — that refusal is Check 572's row). Nothing about a reseller's month was measured"
    stop "the token belongs to an email-link sign-in, which is not the sign-in a reseller organisation admits"
    ;;
  *)
    unmeasured signin "the token's provider is '${provider:-none}', which is neither an SSO sign-in nor an email link: sign in at the portal as a member of the staging reseller organisation with Google or Microsoft, and copy THAT token"
    stop "the token's provider is '${provider:-none}', which is not the SSO sign-in a reseller's routes admit"
    ;;
  esac
  if [ "$aud" != "$PROJECT" ]; then
    unmeasured signin "the token's aud is '$aud' and --project names $PROJECT: an ID token is minted for one Firebase project, and this check reads the deployment of the project it was given. Sign in at that deployment's own site, or pass --project $aud"
    stop "the token was minted for $aud and this check was given $PROJECT, so the sign-in and the deployment it is about are not the same one"
  fi
  if [ "$exp" -le "$now" ]; then
    unmeasured signin "the token expired at epoch $exp and it is epoch $now by this machine's clock: the API would refuse an expired token for its own reason, so a refusal read now would say nothing about the reseller's month. Sign in again and copy the fresh token"
    stop "the token has expired, so the API's own answers could not be told apart from the sign-in's refusals"
  fi
  local minutes=$(((exp - now) / 60))
  fact signin PASS "the token is a live ID token of $PROJECT (expires in ${minutes} min) whose firebase.sign_in_provider is '$provider' — the SSO sign-in a reseller organisation's members reach the portal with (D2207/F10; the email-link account's own refusal is Check 572)"
}

# api_call METHOD PATH KEY [BODY] — one call to the portal API with the owner's
# sign-in. The token goes into a 0600 curl config and never onto the command
# line, where `ps` and this check's own error output would both show it. The
# answer's status is printed by curl into $SCRATCH/KEY.code and its body into
# $SCRATCH/KEY.body.
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

# reseller_refusal KEY WHAT — reads the answer of a call whose 403s are the
# membership's own rule rather than a finding: a caller who is not a member of a
# reseller organisation, and the sign-in rule of one. Both are rows this check
# cannot measure in (which account to sign in as is the owner's), and each names
# what to do. Returns 0 when the caller IS a reseller's person.
refuse_reseller_call() { # refuse_reseller_call KEY WHAT CODE
  local key=$1 what=$2 code=$3
  fault_fields "$SCRATCH/$key.body" >"$SCRATCH/$key.txt"
  local fault_code message
  fault_code=$(field "$SCRATCH/$key.txt" code)
  message=$(field "$SCRATCH/$key.txt" message)
  if [ "$fault_code" = not_reseller ]; then
    unmeasured "$key" "GET $API$what answered $code 'not_reseller' ('$message'): the account this token belongs to is not a member of a reseller organisation in $PROJECT, and every row below is a reseller's own. Add the account to the staging reseller organisation's membership, then run the check again"
    return 1
  fi
  if [ "$fault_code" = sso_required ]; then
    unmeasured "$key" "GET $API$what answered $code 'sso_required' ('$message'): the sign-in this token carries is not the one this reseller organisation's routes admit (D2207/F10 asks for Google or Microsoft), so the rows below would say nothing about a reseller's month"
    return 1
  fi
  unmeasured "$key" "GET $API$what answered $code with code '${fault_code:-none}' ('$message') rather than the reseller's own terms: this row could not be measured. The API's unauthenticated answer would be 401 — if that is what this is, the token stopped working between the reads above"
  return 1
}

# step_terms: the reseller's own commercial terms — operations' credit limit and
# wholesale prices. They are what the refused sale below is composed against, and
# a row this reseller is not priced for is a row it cannot sell.
step_terms() {
  local code
  code=$(api_call GET /api/resellers/terms terms)
  if [ "$code" != 200 ]; then
    refuse_reseller_call terms /api/resellers/terms "$code"
    stop "the reseller's own terms were not read (${code:-no answer}), so the credit limit the refused sale is about could not be read"
  fi
  terms_fields "$SCRATCH/terms.body" >"$SCRATCH/terms.txt"
  local error limit usd rates
  error=$(field "$SCRATCH/terms.txt" error)
  if [ -n "$error" ]; then
    unmeasured terms "GET /api/resellers/terms answered 200 and a document this check cannot read as the terms ($error): the shape is resellers.reseller (organisation_id, credit_limit_micros, rates[])"
    stop "the terms document is not the shape this check reads, so the credit limit was not measured"
  fi
  limit=$(field "$SCRATCH/terms.txt" limit)
  usd=$(field "$SCRATCH/terms.txt" limit_usd)
  rates=$(field "$SCRATCH/terms.txt" rates)
  if [ -z "$limit" ]; then
    unmeasured terms "GET /api/resellers/terms answered a document with no credit_limit_micros: the ceiling D2189 puts on a reseller's accrual is the whole of what the sale below is composed against"
    stop "the terms carry no credit limit, so no sale could be composed against it"
  fi
  if [ "$limit" = 0 ]; then
    fact terms PASS "GET /api/resellers/terms answers the reseller's own terms: a credit limit of 0 micros and $rates priced row(s). A limit of nothing is a reseller that may not mint at all (resellers.schema.json, accrual.go's own reading), so the sale below is refused whatever it costs — and if this is not the state you meant on staging, operations sets the limit on its own terms route"
    return 0
  fi
  fact terms PASS "GET /api/resellers/terms answers the reseller's own terms: a credit limit of $limit micros (\$$usd) and $rates priced row(s) — the ceiling D2189's accrual is measured against and the wholesale price the sale below is composed from"
}

# step_customers: one customer of the reseller's. --customer names it, and the
# newest link is the default because that is the one the route answers first.
step_customers() {
  local code
  code=$(api_call GET /api/resellers/customers customers)
  if [ "$code" != 200 ]; then
    refuse_reseller_call customers /api/resellers/customers "$code"
    stop "the reseller's customers were not read (${code:-no answer})"
  fi
  customers_fields "$SCRATCH/customers.body" >"$SCRATCH/customers.txt"
  local error count first name
  error=$(field "$SCRATCH/customers.txt" error)
  if [ -n "$error" ]; then
    unmeasured customers "GET /api/resellers/customers answered 200 and a document this check cannot read as the customers list ($error): the shape is resellers.customer[] (organisation_id, name, linked_at)"
    stop "the customers document is not the shape this check reads"
  fi
  count=$(field "$SCRATCH/customers.txt" count)
  first=$(field "$SCRATCH/customers.txt" first)
  name=$(field "$SCRATCH/customers.txt" first_name)
  if [ "${count:-0}" = 0 ]; then
    unmeasured customers "GET /api/resellers/customers answers no customer at all: this reseller has sold to nobody in $PROJECT, so it has no month to state and no customer to make a sale against. Make one customer and sell it a licence from the reseller's own screens, then run the check again"
    stop "the reseller holds no customer, so there is no month and no sale for this check to read"
  fi
  CUSTOMER_ID="$first"
  CUSTOMER_NAME="$name"
  if [ -n "$CUSTOMER_ARG" ]; then
    case ",$(field "$SCRATCH/customers.txt" ids)," in
    *",$CUSTOMER_ARG,"*) CUSTOMER_ID="$CUSTOMER_ARG" ;;
    *)
      unmeasured customers "the customer this check was given (--customer $CUSTOMER_ARG) is not one this reseller holds: its $count customer(s) are the ids it answers on GET /api/resellers/customers. Pass --customer one of those"
      stop "--customer $CUSTOMER_ARG is not a customer of this reseller, so the rows below would read somebody else's organisation"
      ;;
    esac
  fi
  fact customers PASS "the reseller holds $count customer(s), and this check reads ${CUSTOMER_ID}${CUSTOMER_NAME:+ ($CUSTOMER_NAME)} — the customer the refused sale names and the keys the accrual is priced from"
}

# step_keys: the customer's keys as a reseller may see them (F5: metadata and
# never a key). One is needed for two reasons: a month's accrual is priced from
# the licences a reseller sold, and its Firebase project id is what the workspace
# the sale names is resolved from — the key's own `workspace_id` is that project
# id (TN2), and step_workspace holds it to the workspace the mint reads.
step_keys() {
  local code
  code=$(api_call GET "/api/resellers/customers/$CUSTOMER_ID/keys" keys)
  if [ "$code" != 200 ]; then
    refuse_reseller_call keys "/api/resellers/customers/$CUSTOMER_ID/keys" "$code"
    stop "the customer's keys were not read (${code:-no answer})"
  fi
  keys_fields "$SCRATCH/keys.body" >"$SCRATCH/keys.txt"
  local error count digest newest workspace tier seats paid_through
  error=$(field "$SCRATCH/keys.txt" error)
  if [ -n "$error" ]; then
    unmeasured keys "GET /api/resellers/customers/$CUSTOMER_ID/keys answered 200 and a document this check cannot read as a keys list ($error): the shape is keys.key[] (key_id, workspace_id, tier, seats, paid_through)"
    stop "the keys document is not the shape this check reads"
  fi
  count=$(field "$SCRATCH/keys.txt" count)
  digest=$(field "$SCRATCH/keys.txt" digest)
  newest=$(field "$SCRATCH/keys.txt" newest)
  workspace=$(field "$SCRATCH/keys.txt" workspace)
  tier=$(field "$SCRATCH/keys.txt" tier)
  seats=$(field "$SCRATCH/keys.txt" seats)
  paid_through=$(field "$SCRATCH/keys.txt" paid_through)
  if [ "${count:-0}" = 0 ]; then
    unmeasured keys "the customer ${CUSTOMER_ID} holds no key at all: this check needs one for two things — a month's accrual is priced from the licences a reseller sold, and the refused sale below has to name a workspace of this customer's organisation. Sell this customer a licence from the reseller's own screens, then run the check again"
    stop "the customer holds no key, so there is no workspace to name and no sale the accrual is priced from"
  fi
  if [ -z "$workspace" ]; then
    unmeasured keys "the customer's $count key(s) carry no workspace_id: a key's workspace_id is the Firebase project id it is bound to (TN2), and the workspace the sale below names is resolved from it. This is a document this check cannot compose a sale from"
    stop "the customer's keys name no Firebase project, so the refused sale's workspace could not be resolved"
  fi
  KEY_PROJECT_ID="$workspace"
  KEYS_BEFORE="$digest"
  COUNT_BEFORE="$count"
  fact keys PASS "the customer ${CUSTOMER_ID} holds $count key(s) (ids digest $digest), the newest of them $newest bound to the Firebase project $workspace — ${tier:-<no tier>}, ${seats:-?} seat(s), paid through ${paid_through:-<none>}. This is F5's whole: metadata and never a key. This project is what the workspace the sale names is resolved from, and this digest is what the after row compares"
}

# step_workspace: the workspace the refused sale is composed for, which is the one
# field of the sale this check has to translate rather than read. A key's own
# `workspace_id` is the Firebase PROJECT id it is bound to (TN2), and the mint
# reads the sale's `workspace_id` as a portal workspace DOCUMENT id
# (`mintResellerLicence` loads `workspaces/{workspace_id}` and refuses a workspace
# that is not there or is another organisation's as `not_found`, BEFORE it compares
# anything against the credit limit): posting the project id is a sale the route
# 404s, which measures no ceiling at all. No reseller route answers a portal
# workspace id — the customers, the keys and the terms carry none — so the check
# reads the operations area's list of this customer's workspaces and takes the one
# whose `project_id` is the key's own. That read is the operations membership's,
# and it writes that area's own line in the log (D456); `--workspace-id` names the
# workspace instead, which is what a token that is a reseller's and nothing else
# is run with.
step_workspace() {
  if [ -n "$WORKSPACE_ARG" ]; then
    WORKSPACE_ID="$WORKSPACE_ARG"
    fact workspace PASS "--workspace-id names the portal workspace ${WORKSPACE_ID} as the one the refused sale is composed for, and nothing was read to resolve it. The sale's workspace_id is a workspace DOCUMENT id and the key's own is the Firebase project id it is bound to (TN2), so this is the one field of the sale that is named rather than read — a workspace id this customer does not hold is a sale the route answers 404 'not_found' ('mintResellerLicence' resolves the workspace before it compares anything against the limit)"
    return 0
  fi
  local code
  code=$(api_call GET "/api/ops/workspaces?organisation_id=$CUSTOMER_ID" opsworkspaces)
  if [ "$code" != 200 ]; then
    fault_fields "$SCRATCH/opsworkspaces.body" >"$SCRATCH/opsworkspaces.txt"
    local fault_code message
    fault_code=$(field "$SCRATCH/opsworkspaces.txt" code)
    message=$(field "$SCRATCH/opsworkspaces.txt" message)
    if [ "$code" = 403 ] && [ "$fault_code" = not_operations ]; then
      unmeasured workspace "GET /api/ops/workspaces?organisation_id=$CUSTOMER_ID answered 403 'not_operations': the workspace the sale below names is a portal workspace document id and no reseller route answers one, so it is read from the operations area's list of the customer's workspaces — and the account this token belongs to is not a member of the operations organisation in $PROJECT. Pass --workspace-id <the portal workspace id of the customer's workspace bound to the Firebase project $KEY_PROJECT_ID> (the operations screen lists one organisation's workspaces with their project ids), then run the check again"
    elif [ "$code" = 404 ]; then
      unmeasured workspace "GET /api/ops/workspaces?organisation_id=$CUSTOMER_ID answered 404 '${fault_code:-none}' ('$message'): the customer ${CUSTOMER_ID} is not an organisation this deployment holds, so its workspaces — one of which the sale below names — could not be read. This is the customer the reseller's own customers read answered, so the two reads disagree about it. Pass --workspace-id with the customer's own workspace id, then run the check again"
    else
      unmeasured workspace "GET /api/ops/workspaces?organisation_id=$CUSTOMER_ID answered ${code:-nothing} with code '${fault_code:-none}' ('$message') rather than the customer's workspaces: the sale below names a portal workspace document id, which no reseller route answers, so this row could not be measured. Pass --workspace-id <the customer's own portal workspace id>, then run the check again"
    fi
    stop "the workspace the refused sale names could not be resolved"
  fi
  ops_workspaces_fields "$SCRATCH/opsworkspaces.body" "$KEY_PROJECT_ID" >"$SCRATCH/opsworkspaces.txt"
  local error count match held
  error=$(field "$SCRATCH/opsworkspaces.txt" error)
  if [ -n "$error" ]; then
    unmeasured workspace "GET /api/ops/workspaces?organisation_id=$CUSTOMER_ID answered 200 and a document this check cannot read as a workspaces list ($error): the shape is a list of workspaces.workspace (id, organisation_id, project_id, bound_at), which is what the operations area answers"
    stop "the workspaces document is not the shape this check reads"
  fi
  count=$(field "$SCRATCH/opsworkspaces.txt" count)
  match=$(field "$SCRATCH/opsworkspaces.txt" match)
  held=$(field "$SCRATCH/opsworkspaces.txt" held)
  if [ "${count:-0}" = 0 ]; then
    unmeasured workspace "the customer ${CUSTOMER_ID} holds no workspace at all: the sale below names one, and a customer with a key bound to the Firebase project $KEY_PROJECT_ID has the workspace that key was minted into. Pass --workspace-id with that workspace's portal id, then run the check again"
    stop "the customer holds no workspace, so the refused sale has nothing to name"
  fi
  if [ -z "$match" ]; then
    unmeasured workspace "the customer ${CUSTOMER_ID} holds $count workspace(s) ($held) and none of them is bound to the Firebase project $KEY_PROJECT_ID, which is the project the key this check read is bound to: the sale below is composed for the workspace that key was minted into, and one of the two reads is about something else. Pass --workspace-id with the customer's own workspace id, then run the check again"
    stop "no workspace of the customer is bound to the project its key is bound to, so the sale's workspace could not be resolved"
  fi
  WORKSPACE_ID="$match"
  fact workspace PASS "the refused sale is composed for the workspace ${WORKSPACE_ID} of the customer ${CUSTOMER_ID} — the one of its $count workspace(s) whose project_id is $KEY_PROJECT_ID, the Firebase project its key is bound to. It is read rather than assumed because the mint resolves the sale's workspace_id as a portal workspace DOCUMENT id while a key's own workspace_id is the project id (TN2), and no reseller route answers a portal workspace id: this is the operations area's own read of the customer's workspaces (GET /api/ops/workspaces), which writes that area's line in the log (D456)"
}

# step_service: the API's own revision, and the two values its SchedulerVerifier
# compares a scheduled call's token against (F33). The check READS them rather
# than inferring an account from the project or an audience from a URL: the
# verifier refuses to be built over an empty one, and a deployment whose value
# this check guessed would make the row below a reading of this check's own
# convention. Nothing else of the revision's environment is read.
step_service() {
  if ! gcloud run services describe "$API_SERVICE" --region "$REGION" --project "$PROJECT" --format=json \
    >"$SCRATCH/service.json" 2>"$SCRATCH/service.err"; then
    unmeasured service "gcloud run services describe $API_SERVICE --region $REGION did not answer: $(last_lines "$SCRATCH/service.err"). Reading the revision's own FUGARO_SCHEDULER_ACCOUNT and FUGARO_SCHEDULER_AUDIENCE is what makes the job row below exact, and it needs roles/run.viewer in $PROJECT (or --api-service, if P1.5's service is named differently here). The rest of the check is unaffected: the account the run's own log line names is still compared with the job's token"
    return 0
  fi
  env_fields "$SCRATCH/service.json" >"$SCRATCH/service.txt"
  local error revision account audience
  error=$(field "$SCRATCH/service.txt" error)
  if [ -n "$error" ]; then
    unmeasured service "gcloud answered a document this check cannot read as a Cloud Run service ($error)"
    return 0
  fi
  revision=$(field "$SCRATCH/service.txt" revision)
  account=$(field "$SCRATCH/service.txt" account)
  audience=$(field "$SCRATCH/service.txt" audience)
  API_ENV_ACCOUNT="$account"
  API_ENV_AUDIENCE="$audience"
  if [ -z "$account" ] || [ -z "$audience" ]; then
    unmeasured service "the API's newest ready revision${revision:+ $revision} of $API_SERVICE carries no FUGARO_SCHEDULER_ACCOUNT ('${account:-none}') or no FUGARO_SCHEDULER_AUDIENCE ('${audience:-none}'): the verifier refuses to be built without both (NewSchedulerVerifier refuses an empty account or audience, because one compared against nothing admits whatever matches), so every scheduled call to the statements route is answered as unverified and the month is never billed"
    return 0
  fi
  fact service PASS "the API's newest ready revision${revision:+ $revision} of $API_SERVICE is configured with FUGARO_SCHEDULER_ACCOUNT $account and FUGARO_SCHEDULER_AUDIENCE $audience — the pair portalauth.go's SchedulerVerifier compares a scheduled call's token against, account and audience both (F33). Nothing else of that revision's environment was read"
}

# step_job: P1.5's monthly statement job as the deployment has it. What it posts
# and where, whose OIDC token it carries, and whether the pause the apply creates
# it under has been lifted — a job left paused is a month nobody is billed for.
# The token's pair is held against the API's own configured pair: an audience
# minted for the target's assigned URL is the misconfiguration scheduler.tf names,
# and it is refused only when the call is made, which is a month later.
step_job() {
  if ! gcloud scheduler jobs describe "$STATEMENT_JOB" --location "$REGION" --project "$PROJECT" --format=json \
    >"$SCRATCH/job.json" 2>"$SCRATCH/job.err"; then
    unmeasured job "gcloud scheduler jobs describe $STATEMENT_JOB --location $REGION did not answer: $(last_lines "$SCRATCH/job.err"). This is P1.5's job (terraform/services/scheduler.tf, var.statement_job's own default is fugaro-portal-statements), and the run below is fired through it. If the job is named differently in $PROJECT, pass --statement-job; if this account may not describe it, this row could not be measured"
    stop "the statement job could not be read in $PROJECT, so the run this check fires could not be named"
  fi
  job_fields "$SCRATCH/job.json" >"$SCRATCH/job.txt"
  local error state paused schedule zone method path origin aud_origin account audience
  error=$(field "$SCRATCH/job.txt" error)
  if [ -n "$error" ]; then
    unmeasured job "gcloud answered a document this check cannot read as a job ($error)"
    stop "the job document is not the shape this check reads"
  fi
  state=$(field "$SCRATCH/job.txt" state)
  paused=$(field "$SCRATCH/job.txt" paused)
  schedule=$(field "$SCRATCH/job.txt" schedule)
  zone=$(field "$SCRATCH/job.txt" time_zone)
  method=$(field "$SCRATCH/job.txt" method)
  path=$(field "$SCRATCH/job.txt" path)
  origin=$(field "$SCRATCH/job.txt" origin)
  aud_origin=$(field "$SCRATCH/job.txt" audience_origin)
  account=$(field "$SCRATCH/job.txt" account)
  audience=$(field "$SCRATCH/job.txt" audience)
  JOB_ACCOUNT="$account"
  if [ "$method" != POST ] || [ "$path" != "$STATEMENT_PATH" ]; then
    fact job FAIL "the job $STATEMENT_JOB posts $method ${path:-<no path>}, and P6.3's run is POST $STATEMENT_PATH (terraform/services/variables.tf's statement_job_path): this job does not reach the route the month is issued at, so the monthly run bills nobody. Fix the deployment's statement_job_path and its route, then run the check again"
    stop "the statement job does not post to the statement run's route, so firing it would measure nothing"
  fi
  case "$account" in
  *"@$PROJECT.iam.gserviceaccount.com") ;;
  *)
    fact job FAIL "the job's OIDC token is minted for '${account:-nobody}', which is not an account in $PROJECT: the API verifies the account as well as the audience of a scheduled call (F33), so a token from another project's account is refused and the monthly run never happens"
    stop "the statement job's OIDC account is not one of $PROJECT's, so the run it fires cannot be admitted"
    ;;
  esac
  if [ -z "$audience" ] || [ "$aud_origin" = "$origin" ]; then
    fact job FAIL "the job's OIDC audience is '${audience:-unset}', whose origin is $origin — the target's own assigned URL: scheduler.tf names exactly this — an audience that is the target's own assigned URI is a token the API refuses, because it compares the claim against the deployment's own FUGARO_SCHEDULER_AUDIENCE and the assigned URL is a different string for the same service. The call is refused only when it is made, which is a month later and long after the apply that read as a success"
    stop "the statement job's token audience is the target's own URL, which the API refuses"
  fi
  if [ -n "$API_ENV_AUDIENCE" ] && [ "$audience" != "$API_ENV_AUDIENCE" ]; then
    fact job FAIL "the job's OIDC audience is '$audience' and the API's own revision is configured with FUGARO_SCHEDULER_AUDIENCE '$API_ENV_AUDIENCE': the claim is compared exactly (portalauth.go's SchedulerVerifier), so every scheduled call to the statements route is refused and the month is never billed. The job's audience and the API's value come from one Terraform value (main.tf's local.api_url); this deployment has them apart"
    stop "the statement job's token audience is not the one the API compares against"
  fi
  if [ -n "$API_ENV_ACCOUNT" ] && [ "$account" != "$API_ENV_ACCOUNT" ]; then
    fact job FAIL "the job's OIDC token is minted for '$account' and the API's own revision is configured with FUGARO_SCHEDULER_ACCOUNT '$API_ENV_ACCOUNT': the account is half of what the verifier compares (the audience is the other half), so the scheduled call is refused and the month is never billed"
    stop "the statement job's token account is not the one the API compares against"
  fi
  if [ "$paused" = 1 ]; then
    unmeasured job "the job $STATEMENT_JOB is $state, not ENABLED: P1.5 creates it paused and the apply that ships the route lifts the pause (statements_paused), so this deployment's monthly run does not fire by itself — a month nobody is billed for. The check fires it by hand below, which is what the run row reads; to leave it firing, apply the services root with -var statements_paused=false"
  else
    fact job PASS "the job $STATEMENT_JOB is ENABLED, posts POST $STATEMENT_PATH on '$schedule' ($zone), and mints its OIDC token for $account with the audience $audience${API_ENV_ACCOUNT:+ — the same pair the API revision is configured with} (F33). Firing it is the run the row below reads"
  fi
}

# step_run: the run, fired the way the deployment fires it and read from the API's
# own log line. What a run did is in the log and in the mails (api_statements.go
# says so itself, and the route answers 204 with no body), so the log line is the
# whole of what an operator reads of it — and the account it names has to be this
# job's own token's, which is what makes the line this fire's and not an earlier
# run's.
step_run() {
  local since fired_now wait
  fired_now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  say "firing $STATEMENT_JOB in $PROJECT (region $REGION): the monthly statement run, which issues and mails the months the resellers are owed"
  if ! gcloud scheduler jobs run "$STATEMENT_JOB" --location "$REGION" --project "$PROJECT" \
    >"$SCRATCH/run.out" 2>"$SCRATCH/run.err"; then
    unmeasured run "gcloud scheduler jobs run $STATEMENT_JOB did not answer: $(last_lines "$SCRATCH/run.err"). The run is what this row is about, and a job this account may not run by hand is a row that could not be measured. An account that may describe a job may not necessarily run it: grant roles/cloudscheduler.jobRunner (or run the check as the owner of the project), then run the check again"
    stop "the statement job could not be fired, so the run and its mail were not measured"
  fi
  since=$(python3 -c 'import datetime,sys; print((datetime.datetime.fromisoformat(sys.argv[1].replace("Z","+00:00")) - datetime.timedelta(seconds=60)).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$fired_now")
  wait=${RUN_SECONDS}
  while :; do
    if ! gcloud logging read "resource.type=\"cloud_run_revision\" AND textPayload:\"the statement run as\"" \
      --project "$PROJECT" --freshness="$LOG_FRESHNESS" --limit=20 --format=json \
      >"$SCRATCH/log.json" 2>"$SCRATCH/log.err"; then
      unmeasured run "gcloud logging read did not answer: $(last_lines "$SCRATCH/log.err"). This check reads the run through the API's own log line, which needs an account that may read Cloud Logging in $PROJECT (roles/logging.viewer). The run itself was fired: read the API's logs for 'the statement run as' to see what it did"
      stop "the run's log line could not be read, so what the fired run did was not measured"
    fi
    run_line "$SCRATCH/log.json" "$since" >"$SCRATCH/run.txt"
    if [ -n "$(field "$SCRATCH/run.txt" account)" ]; then break; fi
    if [ "$wait" -le 0 ]; then break; fi
    sleep 5
    wait=$((wait - 5))
  done
  local account issued mailed at
  account=$(field "$SCRATCH/run.txt" account)
  issued=$(field "$SCRATCH/run.txt" issued)
  mailed=$(field "$SCRATCH/run.txt" mailed)
  at=$(field "$SCRATCH/run.txt" at)
  if [ -z "$account" ]; then
    fact run FAIL "the job $STATEMENT_JOB was fired at $fired_now and the API logged no statement run within ${RUN_SECONDS}s: the call the job makes is either refused (a token the API does not accept — the account and the audience above are the pair it verifies) or the job did not reach the service at all. Cloud Scheduler's own attempt for $STATEMENT_JOB carries the HTTP status it was answered with: read it (gcloud scheduler jobs describe $STATEMENT_JOB --location $REGION) and the API's log, then run the check again"
    stop "the fired run left no line in the API's log, so the run did not happen"
  fi
  if [ "$account" != "$JOB_ACCOUNT" ]; then
    unmeasured run "the newest statement run line is as '$account' and this job's OIDC token is minted for '$JOB_ACCOUNT': the line this check read is not the fire it made — an earlier run's, or another job's. Read the API's log for 'the statement run as' with a window that starts at the fire, and run the check again"
    stop "the run line this check read belongs to another account, so it is not this fire's"
  fi
  fact run PASS "the job was fired at $fired_now and the API logged the run as $account at ${at:-an unstated instant}: 'issued $issued statement(s) and mailed $mailed message(s)'. That sentence is api_statements.go's own — what an operator reads of a run, beside who fired it — and the account in it is this job's token's, so this is the run this check fired. A run issues the months a reseller is owed and mails each statement to that reseller's owners once (statement_sends); a count of 0 is a month already stated and already mailed, or a reseller that sold nothing in it"
}

# step_statements: the reseller's own read of its months, held to the arithmetic
# the schema promises. The accrual is not recomputed here: what the month billed
# is what the statement says it accrued, and the check's reading of it is that the
# lines sum to that figure, that each line is a customer of this reseller, and
# that the month is one that is over.
step_statements() {
  local code
  code=$(api_call GET /api/statements statements)
  if [ "$code" != 200 ]; then
    refuse_reseller_call statements /api/statements "$code"
    stop "the reseller's own statements were not read (${code:-no answer})"
  fi
  statements_fields "$SCRATCH/statements.body" >"$SCRATCH/statements.txt"
  local error count month accrued paid state lines sum orgs positive sum_ok state_ok months_ok
  error=$(field "$SCRATCH/statements.txt" error)
  if [ -n "$error" ]; then
    unmeasured statements "GET /api/statements answered 200 and a document this check cannot read as statements ($error): the shape is statements.statement[] (month, accrued_micros, paid_micros, state, lines[])"
    stop "the statements document is not the shape this check reads"
  fi
  count=$(field "$SCRATCH/statements.txt" count)
  if [ "${count:-0}" = 0 ]; then
    unmeasured statements "GET /api/statements answers no statement at all for this reseller: a month that accrued nothing is no statement (statements.go), and an empty list is what a reseller that sold nothing reads. The run above was fired — if its own line says it issued 0 statement(s), this reseller was owed no month: sell a licence from its screens in a month that has since ended, then run the check again"
    stop "the reseller holds no statement, so there is no month's accrual to read"
  fi
  month=$(field "$SCRATCH/statements.txt" month)
  accrued=$(field "$SCRATCH/statements.txt" accrued)
  paid=$(field "$SCRATCH/statements.txt" paid)
  state=$(field "$SCRATCH/statements.txt" state)
  lines=$(field "$SCRATCH/statements.txt" lines)
  sum=$(field "$SCRATCH/statements.txt" sum)
  orgs=$(field "$SCRATCH/statements.txt" orgs)
  positive=$(field "$SCRATCH/statements.txt" positive)
  sum_ok=$(field "$SCRATCH/statements.txt" sum_ok)
  state_ok=$(field "$SCRATCH/statements.txt" state_ok)
  months_ok=$(field "$SCRATCH/statements.txt" months_ok)
  if [ "$months_ok" != 1 ]; then
    fact statements FAIL "the newest statement's month is '$month', which is not a whole UTC month that is over: a month is stated once it has ended (statements.go's month window), so a statement for the month in progress is a bill nobody can add up"
    stop "the newest statement's month is not one that is over"
  fi
  if [ "$positive" != 1 ]; then
    fact statements FAIL "the $month statement's $lines line(s) include a line of nothing: a line is a customer's accrual for the month, and a customer who accrued nothing is no line at all (statements.go's statementAccrual)"
    stop "the statement carries a line of nothing, so its lines are not the month's accrual"
  fi
  if [ "$sum_ok" != 1 ]; then
    fact statements FAIL "the $month statement's $lines line(s) sum to $sum micros and the statement says it accrued $accrued: the month's own arithmetic does not hold. A reseller reconciles a month with these lines, and a total that is not their sum is a statement nobody can check"
    stop "the statement's lines do not sum to what it says it accrued"
  fi
  if [ "$state_ok" != 1 ]; then
    fact statements FAIL "the $month statement is '$state' with $accrued accrued and $paid paid: the state follows the two amounts (a statement whose payments cover its accrual is paid), and this one does not. A state somebody set is a month that says something its figures do not"
    stop "the statement's state does not follow its two amounts"
  fi
  local not_mine
  not_mine=$(python3 - "$orgs" "$(field "$SCRATCH/customers.txt" ids)" <<'PY'
import sys
lines = {row for row in sys.argv[1].split(",") if row}
mine = {row for row in sys.argv[2].split(",") if row}
print(",".join(sorted(lines - mine)))
PY
)
  if [ -n "$not_mine" ]; then
    fact statements FAIL "the $month statement carries line(s) for $not_mine, which are not customers of this reseller's now: a statement is one line per customer the reseller holds (statementAccrual), and a line for anybody else is money the reseller is billed for and cannot look up"
    stop "the statement carries a line for an organisation this reseller does not hold"
  fi
  fact statements PASS "the reseller's newest statement is $month: $lines line(s) summing to $accrued micros, exactly what it says it accrued, each line positive and each one a customer this reseller holds ($orgs), and its state '$state' follows the two amounts ($paid paid of $accrued). D2189's accrual read as the month it was billed in"
}

# step_limit: the refused sale, composed for the workspace step_workspace resolved
# (a portal workspace DOCUMENT id, which is what the mint reads that field as). The
# check composes a sale whose cost is strictly greater than the WHOLE credit limit
# — days times seats times the wholesale rate,
# with the seats read off the limit and the rate — so that no state of the accrual
# can admit it: `used > limit || cost > limit - used` is satisfied by `cost >
# limit` alone, whatever the reseller already owes. A sale that lands instead is
# D2189's ceiling broken, and the customer's keys before and after it are what the
# refusal's own sentence claims.
step_limit() {
  local rate seats limit cost days
  rate=$(field "$SCRATCH/terms.txt" "rate_$ROW")
  limit=$(field "$SCRATCH/terms.txt" limit)
  days=$DAYS
  if [ -z "$rate" ]; then
    unmeasured limit "this reseller is priced for no '$ROW' row: GET /api/resellers/terms answers $(field "$SCRATCH/terms.txt" rates) priced row(s), and a row with no wholesale price is a row this reseller cannot sell (resellers.schema.json, rateFor). Pass --row one of the rows the terms name, then run the check again"
    stop "the row $ROW carries no wholesale price for this reseller, so no sale of it could be composed"
  fi
  if [ "$rate" = 0 ]; then
    unmeasured limit "the '$ROW' row's wholesale rate is 0 micros a seat a day: a sale of it costs nothing whatever its seats and term, so no sale of it can pass a credit limit — the ceiling D2189 states would be reached only by a rate operations has not set. Set a wholesale price for $ROW on the reseller's terms (operations' own route), then run the check again"
    stop "the row $ROW costs this reseller nothing, so no sale of it can be composed against the limit"
  fi
  if [ "${SEATS_ARG:-0}" != 0 ]; then
    seats=$SEATS_ARG
  else
    seats=$(python3 -c 'import sys; limit, days, rate = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3]); print(max(1, limit // (days * rate) + 1))' "$limit" "$days" "$rate")
  fi
  cost=$(python3 -c 'import sys; print(int(sys.argv[1]) * int(sys.argv[2]) * int(sys.argv[3]))' "$days" "$seats" "$rate")
  if [ "$cost" -le "$limit" ]; then
    unmeasured limit "the sale this check composed costs $cost micros and the credit limit is $limit: the arithmetic that makes the refusal certain is days ($days) times seats ($seats) times the row's rate ($rate) strictly greater than the limit, and --seats $seats does not clear it. Pass --seats larger than $((limit / (days * rate))) (or raise --days), then run the check again"
    stop "the sale this check composed is one the limit may admit, so a refusal would not have been the ceiling's"
  fi
  python3 - "$SCRATCH/mint.json" "$WORKSPACE_ID" "$ROW" "$seats" "$days" <<'PY'
import datetime, json, sys
path, workspace, row, seats, days = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
ends = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=days)
body = {"workspace_id": workspace, "row": row, "seats": seats,
        "ends_at": ends.strftime("%Y-%m-%dT%H:%M:%SZ"), "check_interval": "P1D"}
open(path, "w").write(json.dumps(body, separators=(",", ":")))
PY
  local code
  code=$(api_call POST "/api/resellers/customers/$CUSTOMER_ID/keys" mint "$(cat "$SCRATCH/mint.json")")
  if [ "$code" = 201 ] || [ "$code" = 200 ]; then
    fact limit FAIL "the sale was ADMITTED: POST /api/resellers/customers/$CUSTOMER_ID/keys answered $code for a $ROW sale of $seats seat(s) over $days day(s), which costs $cost micros — strictly more than this reseller's whole credit limit of $limit. The check's own arithmetic is what makes the refusal certain (used > limit || cost > limit - used, accrual.go), so a build that admitted this has no ceiling at all: D2189 is broken, and a licence has been written for the customer ${CUSTOMER_ID}. Its keys are now: $(field "$SCRATCH/keys.txt" ids)"
    stop "a sale beyond the whole credit limit was admitted, which is D2189's ceiling broken"
  fi
  if [ -z "$code" ]; then
    unmeasured limit "POST /api/resellers/customers/$CUSTOMER_ID/keys answered nothing (curl said: $(last_lines "$SCRATCH/mint.curl.err")): the refusal this row is about was never read"
    stop "the sale this check composed was not answered, so no refusal was measured"
  fi
  fault_fields "$SCRATCH/mint.body" >"$SCRATCH/mint.txt"
  local fault_code message
  fault_code=$(field "$SCRATCH/mint.txt" code)
  message=$(field "$SCRATCH/mint.txt" message)
  if [ "$fault_code" = over_credit_limit ]; then
    if [ "$code" != 409 ]; then
      fact limit FAIL "the refusal this sale earned carries the code 'over_credit_limit' and the status $code rather than 409: the code is D2189's own and the status is what the reseller's screen branches on (refuseResellerMint answers 409 for it), so a screen that reads the status shows the sale as a request it typed wrongly"
      stop "the credit-limit refusal carries a status that is not the one the code belongs to"
    fi
  elif [ "$code" = 422 ]; then
    unmeasured limit "the sale was refused with 422 '${fault_code:-none}' ('$message'): this is a row the request itself failed, not the credit limit — the row the check named carries its own seats, the span is outside the row's term, or the check interval is not one F28 allows. Pass --row with a row whose seats the caller names and --days inside its term (the terms route names every row this reseller is priced for), then run the check again"
    stop "the composed sale was refused for a reason of its own, so the credit limit was not measured"
  elif [ "$code" = 404 ]; then
    unmeasured limit "POST /api/resellers/customers/$CUSTOMER_ID/keys answered 404 '${fault_code:-none}' ('$message'): the mint resolves the sale's workspace_id as a portal workspace DOCUMENT id (accrual.go's mintResellerLicence loads workspaces/{workspace_id}) and answers a workspace that is not there, or is another organisation's, as not_found — BEFORE it compares anything against the credit limit — so a sale naming the key's Firebase project id instead is this answer and no ceiling is measured by it. The sale this check composed names the workspace $WORKSPACE_ID, resolved from the key's own project id $KEY_PROJECT_ID: if that pair is wrong, pass --workspace-id with the customer's own workspace id, then run the check again"
    stop "the composed sale's workspace was not one the mint resolves, so the credit limit was not measured"
  else
    unmeasured limit "POST /api/resellers/customers/$CUSTOMER_ID/keys answered $code with code '${fault_code:-none}' ('$message') rather than 409 'over_credit_limit': this is not the refusal D2189's ceiling is"
    stop "the composed sale was answered $code '${fault_code:-none}', which is not the ceiling's refusal"
  fi
  case "$message" in
  *"would pass the credit limit operations set for this reseller"*) ;;
  *)
    unmeasured limit "the refusal carries the code 'over_credit_limit' and the sentence '$message', which is not refuseResellerMint's own: the code is what a screen branches on and the sentence is what it shows beside the sale"
    stop "the credit-limit refusal carries a sentence this check does not recognise"
    ;;
  esac
  local code_after digest_after count_after
  code_after=$(api_call GET "/api/resellers/customers/$CUSTOMER_ID/keys" keys_after)
  if [ "$code_after" != 200 ]; then
    unmeasured limit "the customer's keys could not be read after the refused sale ($code_after): this check cannot say whether it wrote anything"
    stop "the after read of the customer's keys did not answer, so nothing was measured about what the refusal wrote"
  fi
  keys_fields "$SCRATCH/keys_after.body" >"$SCRATCH/keys_after.txt"
  digest_after=$(field "$SCRATCH/keys_after.txt" digest)
  count_after=$(field "$SCRATCH/keys_after.txt" count)
  if [ "$digest_after" != "$KEYS_BEFORE" ]; then
    fact limit FAIL "the refused sale changed the customer's keys: they read digest $KEYS_BEFORE before it and $digest_after after it ($count_after key(s), ids $(field "$SCRATCH/keys_after.txt" ids)). The refusal's own sentence is that the licences the reseller already holds are unchanged, and a refusal that wrote one is a sale nobody admitted and everybody bills"
    stop "the refused sale wrote a licence, which the refusal's own sentence says it did not"
  fi
  fact limit PASS "a $ROW sale of $seats seat(s) over $days day(s) into the customer's workspace $WORKSPACE_ID, costing $cost micros — strictly more than this reseller's whole credit limit of $limit — is refused with 409 'over_credit_limit': '$message'. The customer's keys are the same $count_after key(s) after it as before it (digest $digest_after), which is the sentence's own 'the licences it already holds are unchanged' measured rather than read. This is D2189's ceiling at the route a reseller mints by, composed so that no state of the accrual could have admitted it"
}

step_restore() {
  fact restore PASS "nothing this check wrote is left in $PROJECT: the refused sale wrote nothing (the customer's keys are the $COUNT_BEFORE key(s) they were), no terms, customer or licence was written, and the scratch directory holding this check's own copy of the composed sale is removed from the exit trap. The one entry this run caused is the operations log's own line for its read of the customer's workspaces (D456), which is the deployment's record of a read rather than a thing to undo. What the run above did is the deployment's own monthly work — a month a reseller was owed, issued and mailed once — and undoing it is a write this check does not make"
}

plan() {
  say "CHECK 575 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read-only] GET ${API}/api/workspaces with no sign-in — 401 'unauthenticated', which says the origin is the portal's API"
  say "  [local, no network] decode the token file's payload in the scratch directory — provider (an SSO one), aud against ${PROJECT}, expiry; no claim that names the person is read"
  say "  [network, read-only] GET ${API}/api/resellers/terms — the credit limit and the wholesale prices operations set"
  say "  [network, read-only] GET ${API}/api/resellers/customers, then .../customers/<id>/keys — the customer the sale names and the Firebase project its newest key is bound to (F5: metadata, never a key)"
  if [ -n "$WORKSPACE_ARG" ]; then
    say "  [local, nothing read] the sale's workspace is the one --workspace-id names (${WORKSPACE_ARG}): the mint reads a sale's workspace_id as a portal workspace DOCUMENT id while a key's own is the Firebase project id (TN2), so it is named rather than resolved"
  else
    say "  [network, read-only, WRITES THE OPERATIONS LOG LINE] GET ${API}/api/ops/workspaces?organisation_id=<the customer> — the portal workspace bound to the key's Firebase project id, which is what the mint reads a sale's workspace_id as; no reseller route answers one, so this is the operations area's read and it needs a member of that organisation (D456's log line is written by the read itself)"
  fi
  say "  [gcloud, read-only] scheduler jobs describe ${STATEMENT_JOB} --location ${REGION} — the method and path it posts, the account and audience of its OIDC token, and whether the pause has been lifted"
  say "  [gcloud, THE ONE ACT] scheduler jobs run ${STATEMENT_JOB} — the monthly statement run, fired by hand: it issues the months the resellers are owed and mails each statement once (idempotent per month, statement_sends)"
  say "  [gcloud, read-only] logging read resource.type=\"cloud_run_revision\" AND textPayload:\"the statement run as\" — the API's own line for what the run did (issued N, mailed M), within ${RUN_SECONDS}s of the fire"
  say "  [network, read-only] GET ${API}/api/statements — the newest statement, held to its lines' own arithmetic and to a month that is over"
  say "  [network, WOULD WRITE IF THE BUILD IS WRONG] POST ${API}/api/resellers/customers/<id>/keys with a ${ROW} sale of ${DAYS} day(s) and seats computed so the cost is strictly greater than the whole credit limit: nothing can admit it, so on a correct build it is refused with 409 'over_credit_limit' and writes nothing — a build that admits it has written a licence beyond the ceiling this check exists for"
  say "  [network, read-only] GET ${API}/api/resellers/customers/<id>/keys again — the same keys, which is the refusal's own sentence measured"
  fact dry-run PASS "the plan was printed and nothing was run: no portal call was made, no token was read and no job was fired"
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
  step_signin
  step_terms
  step_customers
  step_keys
  step_workspace
  step_service
  step_job
  step_run
  step_statements
  step_limit
  step_restore
}

# ---------------------------------------------------------------------- inputs
COUNT_BEFORE=""

DRY_RUN_CLI=""
PROJECT_CLI=""
API_CLI=""
TOKEN_CLI=""
CUSTOMER_ARG=""
WORKSPACE_ARG=""
ROW_CLI=""
DAYS_CLI=""
SEATS_ARG=0
SERVICE_CLI=""
JOB_CLI=""
REGION_CLI=""
USAGE="check575.sh --project <p> --token-file <file> [--api <origin>] [--api-service <name>] [--customer <org id>] [--workspace-id <workspace id>] [--row <policy row>] [--days <n>] [--seats <n>] [--statement-job <name>] [--region <region>] [--production] [--dry-run]"
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
  --api)
    [ $# -ge 2 ] || die "--api needs the portal's origin. usage: $USAGE"
    API_CLI=$2
    shift 2
    ;;
  --customer)
    [ $# -ge 2 ] || die "--customer needs a customer organisation id. usage: $USAGE"
    CUSTOMER_ARG=$2
    shift 2
    ;;
  --workspace-id)
    [ $# -ge 2 ] || die "--workspace-id needs a portal workspace id. usage: $USAGE"
    WORKSPACE_ARG=$2
    shift 2
    ;;
  --row)
    [ $# -ge 2 ] || die "--row needs a policy row name. usage: $USAGE"
    ROW_CLI=$2
    shift 2
    ;;
  --days)
    [ $# -ge 2 ] || die "--days needs a number of days. usage: $USAGE"
    DAYS_CLI=$2
    shift 2
    ;;
  --seats)
    [ $# -ge 2 ] || die "--seats needs a seat count. usage: $USAGE"
    SEATS_ARG=$2
    shift 2
    ;;
  --api-service)
    [ $# -ge 2 ] || die "--api-service needs a Cloud Run service name. usage: $USAGE"
    SERVICE_CLI=$2
    shift 2
    ;;
  --statement-job)
    [ $# -ge 2 ] || die "--statement-job needs a job name. usage: $USAGE"
    JOB_CLI=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || die "--region needs a region. usage: $USAGE"
    REGION_CLI=$2
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

PROJECT=${CHECK575_PROJECT:-${PROJECT_CLI:-}}
TOKEN_FILE=${CHECK575_TOKEN_FILE:-${TOKEN_CLI:-}}
PRODUCTION=${CHECK575_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK575_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK575_KEEP_SCRATCH:-}
API=${CHECK575_API:-${API_CLI:-}}
CUSTOMER_ARG=${CHECK575_CUSTOMER:-${CUSTOMER_ARG:-}}
WORKSPACE_ARG=${CHECK575_WORKSPACE_ID:-${WORKSPACE_ARG:-}}
ROW=${CHECK575_ROW:-${ROW_CLI:-team-annual}}
DAYS=${CHECK575_DAYS:-${DAYS_CLI:-29}}
SEATS_ARG=${CHECK575_SEATS:-${SEATS_ARG:-0}}
API_SERVICE=${CHECK575_API_SERVICE:-${SERVICE_CLI:-fugaro-portal-api}}
STATEMENT_JOB=${CHECK575_STATEMENT_JOB:-${JOB_CLI:-fugaro-portal-statements}}
REGION=${CHECK575_REGION:-${REGION_CLI:-us-east5}}
STATEMENT_PATH=${CHECK575_STATEMENT_PATH:-/api/statements/run}
CURL_SECONDS=${CHECK575_CURL_SECONDS:-60}
RUN_SECONDS=${CHECK575_RUN_SECONDS:-150}
LOG_FRESHNESS=${CHECK575_LOG_FRESHNESS:-15m}
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

if [ -z "$PROJECT" ]; then
  die "Check 575 needs --project: the project the portal is deployed in (the staging deployment's own is fugaro-staging). Usage: $USAGE"
fi
if [ -z "$TOKEN_FILE" ]; then
  die "Check 575 needs --token-file: a file, mode 0600, holding one line — the ID token your browser holds after signing in to the portal as a member of a reseller organisation. Usage: $USAGE"
fi
case "$DAYS" in
*[!0-9]* | "") die "--days has to be a whole number of days. Usage: $USAGE" ;;
esac
if [ "$DAYS" -lt 1 ]; then
  die "--days has to be at least 1: the composed sale's cost is days times seats times the row's rate. Usage: $USAGE"
fi
case "$SEATS_ARG" in
*[!0-9]* | "") die "--seats has to be a whole number of seats. Usage: $USAGE" ;;
esac
case "$ROW" in
"" | *[!A-Za-z0-9-]*) die "--row has to be a policy row name (team-monthly, team-annual, enterprise, enterprise-air-gapped, trial, free-seat). Usage: $USAGE" ;;
esac
# Absolute, before anything changes directory: the readers below are handed the
# path, and a relative one would name another file after a cd.
case "$TOKEN_FILE" in
/*) ;;
*) TOKEN_FILE="$PWD/$TOKEN_FILE" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
