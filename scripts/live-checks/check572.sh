#!/usr/bin/env bash
# Check 572 — an email-link account is refused by the operations and the reseller
# API, and sees the SSO dialog in the portal, on a phone-width window too.
#
# Plan task P9.4 of docs/portal/implementation-plan.md writes this check, and the
# number is that work's own (its "Checks this plan takes (570–579)"). What it
# settles is read from the outside and nowhere else: **D2207/F10** — the sign-in
# rule follows an operations and a reseller membership, an email link is not an
# SSO sign-in, and a member of either organisation is refused on every request
# that membership is what reaches. The refusal is the API's own (`403`, code
# `sso_required`, `ee/keyservice/api_ops.go` and `api_resellers.go`), and the
# dialog the person sees is the SPA's half of the same rule.
#
# What it measures:
#
#   * the API origin, with one read that carries NO sign-in: `GET
#     /api/workspaces` answers 401 `unauthenticated`, which is what says the
#     origin in front of this check is the portal's API and not a marketing page
#     or somebody else's host;
#   * the account's own token, decoded LOCALLY and never printed: its
#     `firebase.sign_in_provider` has to be one of the two spellings an email
#     link signs in under (`password` or `emailLink` — P1.3's UNVERIFIED item in
#     the reconciliation is which of the two the project records, so the row says
#     which it read), its `aud` has to be the project `--project` names, and it
#     has to be unexpired. A token that fails any of those would be refused for a
#     reason of its own, and every row below would then say nothing about D2207;
#   * that the same token VERIFIES: `GET /api/entitlements` answers 200. This is
#     the control row — it is what makes the two refusals below the memberships'
#     own rule rather than a sign-in the API rejected;
#   * **the operations refusal**: `GET /api/ops/organisations` answers 403 with
#     code `sso_required` and the sentence "The operations organisation's members
#     sign in with Google or Microsoft. Sign in that way to reach these screens."
#     A `not_operations` here is a row that could not be measured — the account is
#     not a member of the staging operations organisation — and a 200 is the
#     finding this check exists for;
#   * **the reseller refusal**: `GET /api/resellers/customers` answers the same
#     way, with the reseller organisation's own sentence. One account is a member
#     of both on staging, and each refusal is measured on the membership it is
#     about;
#   * the DIALOG: printed as ONE `MANUAL:` line carrying the exact browser steps,
#     because no script sees a dialog. The row that reads it (`manual`) is the
#     check saying it printed the line, and it is not a claim about the browser.
#
# What it creates: nothing. Every call is a read with the token you already hold,
# and the check writes no account, no organisation and no membership.
#
# Usage:      bash scripts/live-checks/check572.sh --project <p> --token-file <file>
#             bash scripts/live-checks/check572.sh --project <p> --token-file <file> --dry-run
#             bash scripts/live-checks/check572.sh --help
# Needs:      bash, mktemp, python3 (the token's own payload is decoded with it)
#             and curl (the portal API). No `fugaro` command runs at all, and no
#             `gcloud` one: the account this check reads is one you sign in as in
#             a browser.
# Where:      your own machine and the STAGING deployment (`fugaro-staging`)
#             first. `fugaro-prod` is refused unless `--production` is given,
#             because production is only ever measured on purpose; a value naming
#             belong is refused by name; and so is a coding agent's session.
# Costs:      nothing billable: four reads and one refused read against the
#             portal's API.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check572_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. Nothing in the project is touched at all.
# Output:     progress on stderr, the `MANUAL:` line and ONE block on stdout
#             headed CHECK 572 RESULT, then CHECK 572 RESULT END (exit N) — to
#             copy back into the register's Check 572 section, and nothing else.
#             Exit 0: every criterion that was measured held. 1: a measured
#             criterion did not hold (an email-link sign-in that reached the
#             operations or the reseller screens), or a guard stopped the check.
#             2: a row could not be measured for a reason that is this check's
#             own setup — a token file that is not 0600, a token that is not an
#             email-link one, an account that is not a member of the staging
#             organisation. A key the run never reached reads `FAIL — not
#             reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no ID token, in whole or in part, and no
# address, uid or organisation id of the account. The token travels in a 0600
# curl config file and reaches neither a command line nor the block; the token's
# payload is decoded in the scratch directory (mode 0700, removed at the end) to
# read three claims, and what the block carries of it is a provider name (one of
# the project's own provider spellings), the `aud` project id and an expiry state
# — never a claim that names the person.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK572_PROJECT, CHECK572_TOKEN_FILE, CHECK572_API (the portal's origin; the
# staging default is the staging site's own domain, `app.staging.fugaro.dev`,
# and the production one is `app.fugaro.dev` — P0.1, P9.2 — an origin typed from
# memory is how a check measures the wrong deployment), CHECK572_PRODUCTION,
# CHECK572_DRY_RUN, CHECK572_KEEP_SCRATCH and
# CHECK572_CURL_SECONDS (60: the bound on each call).
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
# redirection is in effect when the signal arrives, so the block and the MANUAL
# line are written to the saved descriptors.
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
  for t in bash mktemp python3 curl; do
    command -v "$t" >/dev/null 2>&1 || die "Check 572 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

PROJECT=""
API=""
TOKEN_FILE=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check572.XXXXXX") || die "Check 572 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK572_KEEP_SCRATCH is set)"
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
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK572_KEEP_SCRATCH is set)"
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
BLOCK_KEYS=(xtrace agent-session project belong api token signin verified operations reseller manual restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 572 RESULT\n' >&3
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
  if [ -n "$MANUAL" ]; then
    printf 'MANUAL: %s\n' "$MANUAL" >&3
  fi
  printf 'CHECK 572 RESULT END (exit %s)\n' "$status" >&3
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

# token_fields FILE — the three claims this check reads out of the ID token's own
# payload, decoded locally with no signature check: the check is not the API, and
# a signature it cannot verify would prove nothing. Nothing else of the payload is
# printed, and nothing that names the person is read at all.
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

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check572.sh --project <p> --token-file <file> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of a call carrying a sign-in is a copy of it"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the Authorization header of a call to the terminal"
}

# guard_agent_session: this check reads a sign-in YOU made in a browser. A coding
# agent's session has no browser and no account of yours, so a run there would
# measure somebody else's session or nothing at all.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 572 is yours to run at your own terminal, on the staging deployment, signed in as the email-link account — no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_inputs: the project, the origin and the token file, read before anything
# runs. `fugaro-prod` is the one project a check never measures by accident: the
# rows here read the production operations organisation's membership rule, and
# production is only ever measured on purpose.
guard_inputs() {
  local all="${PROJECT:-}${API:-}${TOKEN_FILE:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: the rows below read a real sign-in against the production portal's operations screens, and production is only ever measured on purpose. Nothing was read"
  fi
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 570 to 575 run on fugaro-staging first, so read this block as a production run by choice"
  else
    fact project PASS "$PROJECT is the Firebase project this check was given (the staging deployment's own is fugaro-staging), and it is not fugaro-prod, which is refused without --production"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact api PASS "$API (the portal's origin; a real run holds it to one read that answers the API's own unauthenticated refusal)"
    fact token PASS "$TOKEN_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact signin PASS "planned: the token's own payload, decoded locally — its provider, its aud against $PROJECT and its expiry"
    return 0
  fi
  if [ ! -f "$TOKEN_FILE" ]; then
    unmeasured token "the token file $TOKEN_FILE is not there: this check reads the portal through YOUR OWN sign-in AS THE EMAIL-LINK ACCOUNT (copy the ID token your browser holds after signing in to $API with the email link), and a file that is not there is a row it cannot measure. Nothing about the portal was read"
    stop "the token file $TOKEN_FILE is not there, so none of the portal's rows could be read. Copy the email-link account's ID token into a file of its own, then run the check again"
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
# step_signin: the premise. Three claims of the token's own payload decide whether
# the rows below say anything about D2207: the provider (an email link signs in
# under `password` or `emailLink`, and P1.3's UNVERIFIED item is which of the two
# this project records), the aud (the project the sign-in was made in, which
# `--project` names) and the expiry (a token the API rejects for being old would
# be refused for a reason of its own).
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
    unmeasured signin "the token file's first line is not an ID token this check can read ($shape, provider '${provider:-none}', aud '${aud:-none}'): copy the whole ID token your browser holds as the email-link account — the three dot-separated parts and nothing else — into a file of its own at mode 0600"
    stop "the token file does not hold a readable ID token, so the check cannot say which provider signed in"
  fi
  case "$provider" in
  password | emailLink) ;;
  google.com | microsoft.com)
    unmeasured signin "the token's provider is '$provider', which is an SSO sign-in: this check is about the account that signs in by EMAIL LINK, and an SSO one is admitted by the operations and the reseller organisations for the reason it is admitted at all (D2207). Nothing about the email-link rule was measured"
    stop "the token belongs to an SSO sign-in ($provider), not to the email-link account this check is about"
    ;;
  *)
    unmeasured signin "the token's provider is '${provider:-none}', which is neither an email link ('password' or 'emailLink') nor an SSO one: sign in as the account that reaches the portal by email link and copy THAT token"
    stop "the token's provider is '${provider:-none}', which is not an email-link sign-in"
    ;;
  esac
  if [ "$aud" != "$PROJECT" ]; then
    unmeasured signin "the token's aud is '$aud' and --project names $PROJECT: an ID token is minted for one Firebase project, and this check reads the deployment of the project it was given. Sign in at that deployment's own site, or pass --project $aud"
    stop "the token was minted for $aud and this check was given $PROJECT, so the sign-in and the deployment it is about are not the same one"
  fi
  if [ "$exp" -le "$now" ]; then    unmeasured signin "the token expired at epoch $exp and it is epoch $now by this machine's clock: the API would refuse an expired token for its own reason, so a refusal read now would say nothing about D2207. Sign in again as the email-link account and copy the fresh token"
    stop "the token has expired, so the API's refusal of it could not be told apart from the provider's rule"
  fi
  local minutes=$(((exp - now) / 60))
  case "$provider" in
  password) provider_note="the provider this project records for an email link: 'password' — P1.3's UNVERIFIED item in docs/portal/reconciliation.md is answered by this reading" ;;
  *) provider_note="'emailLink', the other spelling an email link can be recorded under (P1.3's UNVERIFIED item names both, and this project uses this one)" ;;
  esac
  fact signin PASS "the token is a live ID token of $PROJECT (expires in ${minutes} min) whose firebase.sign_in_provider is '$provider' — $provider_note"
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

# step_api: one read with NO sign-in. The API's own 401 `unauthenticated` is what
# says the origin in front of this check is the portal's API: a marketing page, a
# hosting provider's 404 or somebody else's host answers something else, and
# every row below would then be about a deployment this check cannot name.
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

# step_verified: the control. The same token, on a read that asks nothing of a
# membership's sign-in rule, has to be ADMITTED — which is what makes the two
# refusals below the rule's own and not a sign-in the API rejected.
step_verified() {
  local code
  code=$(api_call GET /api/entitlements entitlements)
  if [ "$code" != 200 ]; then
    fault_fields "$SCRATCH/entitlements.body" >"$SCRATCH/entitlements.txt"
    unmeasured verified "GET /api/entitlements with this token answered ${code:-nothing} (code '$(field "$SCRATCH/entitlements.txt" code)'): this read asks nothing of an operations or a reseller membership, so a refusal here is the sign-in's own — an expired token, a token minted for another project, or an account the API does not know. Every row below would say nothing about D2207. Sign in again and copy the fresh token"
    stop "the API refused an ordinary read with this token (${code:-no answer}), so its refusals of the operations and the reseller screens could not be attributed to the sign-in rule"
  fi
  fact verified PASS "GET /api/entitlements with the same token answers 200: the sign-in verifies to the API, so the refusals below are the operations and the reseller memberships' own rule and not a token the API threw out"
}

# refuse_row KEY PATH WHAT SENTENCE CODE_NOT_MEMBER — one membership rule, read at
# the route that membership reaches. The three outcomes are kept apart on purpose:
# `sso_required` is the row held, `not_operations`/`not_reseller` is an account
# that is not a member of the staging organisation (a row that could not be
# measured), and any other answer — a 200 above all — is the finding.
refuse_row() { # refuse_row KEY PATH WHAT SENTENCE CODE_NOT_MEMBER
  local key=$1 path=$2 what=$3 sentence=$4 not_member=$5
  local code
  code=$(api_call GET "$path" "$key")
  if [ "$code" = 200 ]; then
    fact "$key" FAIL "GET $path answered 200 with an email-link sign-in: $what admitted it, and D2207's rule is that an email link is refused on every request that membership is what reaches"
    stop "the $what admitted the email-link sign-in: D2207's rule is broken at $path"
  fi
  if [ -z "$code" ]; then
    unmeasured "$key" "GET $path answered nothing (curl said: $(last_lines "$SCRATCH/$key.curl.err")): the read this row is made of never completed"
    stop "GET $path did not answer, so the $what refusal was not measured"
  fi
  fault_fields "$SCRATCH/$key.body" >"$SCRATCH/$key.txt"
  local fault_code message
  fault_code=$(field "$SCRATCH/$key.txt" code)
  message=$(field "$SCRATCH/$key.txt" message)
  if [ "$fault_code" = "$not_member" ]; then
    unmeasured "$key" "GET $path answered $code '$fault_code' ('$message') rather than '$sentence': the API refused the read because this account is not a member of the staging $what organisation, which is a state of the account and not of the rule. Add the email-link account to that organisation in $PROJECT, then run the check again"
    stop "the account is not a member of the $what organisation, so its sign-in rule is not the rule this read met"
  fi
  if [ "$code" != 403 ] || [ "$fault_code" != sso_required ]; then
    unmeasured "$key" "GET $path answered $code with code '${fault_code:-none}' ('$message') rather than 403 'sso_required': this is not the refusal D2207's rule is, and the row cannot be read either way. The API's unauthenticated answer would be 401 — if that is what this is, the token stopped working between the reads above"
    stop "GET $path answered $code '${fault_code:-none}', which is neither the rule's refusal nor a membership the account lacks"
  fi
  case "$message" in
  *"$sentence"*) ;;
  *)
    unmeasured "$key" "GET $path answered 403 'sso_required' with the sentence '$message', which does not carry '$sentence': the code is the rule's and the sentence is not, and the sentence is what the SPA shows the person"
    stop "the $what refusal carries a sentence this check does not recognise"
    ;;
  esac
  fact "$key" PASS "GET $path with the email-link sign-in answers 403 'sso_required' — '$message' — which is D2207's rule at the route the membership reaches"
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# step_manual: the half of this check no script can take. The dialog is drawn by
# the SPA after the API's refusal, and what it looks like at a phone width is a
# reading a person takes in a browser. It is ONE `MANUAL:` line carrying the
# exact steps and what to paste back; the row is the check saying it printed it.
step_manual() {
  MANUAL="open $API with the device toolbar at a phone width (390x844, or any width under 480px), sign in AS THE EMAIL-LINK ACCOUNT, and try to reach the operations screens and the reseller screens in that same window. What to paste back: (1) whether each area shows the SSO dialog rather than its own screen — the sentence the API's refusal carries above, with the way to sign in with Google or Microsoft beside it; (2) whether the page is usable at that width (nothing clipped, no horizontal scroll, the dialog's own button reachable) and your browser and its version; (3) the same two readings at a desktop width, so the phone one is a difference you saw rather than the only state there is. The API's half of this is measured above — this line is the part where the person sees it"
  fact manual PASS "the browser half is printed as one MANUAL line carrying the exact steps (the SSO dialog in a phone-width window, at the operations screens and the reseller screens); this check cannot see a dialog, so no row here claims what the browser drew"
}

step_restore() {
  fact restore PASS "nothing of this check's own is left behind: it wrote nothing to the project, and its own account and organisation are ones you made before it ran"
}

plan() {
  say "CHECK 572 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read-only] GET ${API}/api/workspaces with no sign-in — 401 'unauthenticated', which says the origin is the portal's API"
  say "  [local, no network] decode the token file's payload in the scratch directory — provider, aud against ${PROJECT}, expiry; no claim that names the person is read"
  say "  [network, read-only] GET ${API}/api/entitlements with the email-link token — 200, the control that says the token verifies"
  say "  [network, read-only] GET ${API}/api/ops/organisations with the email-link token — 403 'sso_required'"
  say "  [network, read-only] GET ${API}/api/resellers/customers with the email-link token — 403 'sso_required'"
  say "  [browser, yours] the SSO dialog at a phone width — printed as one MANUAL line"
  fact dry-run PASS "the plan was printed and nothing was run: no portal call was made and no token was read"
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
  step_verified
  refuse_row operations "$OPS_PATH" "operations organisation" "sign in with Google or Microsoft" "not_operations"
  refuse_row reseller "$RESELLER_PATH" "reseller organisation" "sign in with Google or Microsoft" "not_reseller"
  step_manual
  step_restore
}

# ---------------------------------------------------------------------- inputs
OPS_PATH=/api/ops/organisations
RESELLER_PATH=/api/resellers/customers
MANUAL=""

DRY_RUN_CLI=""
PROJECT_CLI=""
API_CLI=""
TOKEN_CLI=""
USAGE="check572.sh --project <p> --token-file <file> [--api <origin>] [--production] [--dry-run]"
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

PROJECT=${CHECK572_PROJECT:-${PROJECT_CLI:-}}
TOKEN_FILE=${CHECK572_TOKEN_FILE:-${TOKEN_CLI:-}}
PRODUCTION=${CHECK572_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK572_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK572_KEEP_SCRATCH:-}
API=${CHECK572_API:-${API_CLI:-}}
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
CURL_SECONDS=${CHECK572_CURL_SECONDS:-60}

if [ -z "$PROJECT" ]; then
  die "Check 572 needs --project: the Firebase project the portal and the account live in (the staging deployment's own is fugaro-staging). Usage: $USAGE"
fi
if [ -z "$TOKEN_FILE" ]; then
  die "Check 572 needs --token-file: a file, mode 0600, holding one line — the ID token your browser holds after signing in to the portal BY EMAIL LINK. Usage: $USAGE"
fi
# Absolute, before anything changes directory: the readers below are handed the
# path, and a relative one would name another file after a cd.
case "$TOKEN_FILE" in
/*) ;;
*) TOKEN_FILE="$PWD/$TOKEN_FILE" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
