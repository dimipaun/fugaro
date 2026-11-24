#!/usr/bin/env bash
# Check 570 — pay before setup: a sandbox purchase leaves an UNBOUND entitlement,
# a binding to a project with no installation is refused, `fugaro licence login`
# binds and mints, and a seat purchase reaches the installation through
# `fugaro licence refresh` with nothing pasted.
#
# Plan task P9.4 of docs/portal/implementation-plan.md writes this check, and the
# number is that work's own (its "Checks this plan takes (570–579)"). What it
# settles is read from the outside and nowhere else: **F25/D2197** — a purchase
# creates an entitlement and never a key, so a customer pays before their project
# exists and the term runs from the payment; **D2205** — binding needs an
# installation, and the API refuses rather than binding unchecked; **D2196** —
# binding is the act that mints; and **D2200/K3.4** — a seat change reaches the
# installation by `fugaro licence refresh`, with the owner pasting nothing.
#
# What it measures:
#
#   * the installation under test, read from the checkout (`fugaro doctor --json`):
#     its name, its GCP project and its region, and that the project is the one
#     `--project` names — the check measures the installation you work in rather
#     than one it guessed;
#   * the API origin, with one read that carries NO sign-in: `GET
#     /api/workspaces` answers 401 `unauthenticated`, which is what says the
#     origin in front of this check is the portal's API and not a marketing page
#     or somebody else's host;
#   * the sandbox purchase, read through the owner's own sign-in (`GET
#     /api/entitlements`): one entitlement whose `source` is `purchase`, whose
#     `status` is `active`, whose `bound_at` is null — the state F25 is about, a
#     licence paid for and not yet bound — and which carries no key: the row is
#     the unbound state itself and not a key's absence;
#   * **D2205**, the refusal: `POST /api/workspaces/bind` for the unbound
#     entitlement and a project that holds no Fugaro installation answers 422
#     `no_installation`, and the sentence it carries names `fugaro setup`. This
#     is the one row of the check that is a WRITE attempt and it must not bind:
#     the entitlement is re-read afterwards and has to still be unbound;
#   * `fugaro licence login` in the checkout: the check RUNS it (the browser step
#     is the owner's, and the command's own device code is its own bound), and the
#     entitlement is then re-read: `bound_at` is set, the workspace's
#     `project_id` is the installation's own Firebase project id, and the
#     installation holds a key (`fugaro licence status --json` carries a tier and
#     a `paid_through`). Minting is what binding did;
#   * the seats (**K3.4**): `fugaro licence refresh` is run, and the installation's
#     own `licence status --json` seats are the entitlement's — the number the
#     portal shows, arrived at with nothing pasted into a terminal.
#
# What it creates: one binding and the key it mints, which is the whole point of
# the check, plus whatever a `licence refresh` writes (a successor key, or the
# "nothing to renew yet" answer). The sandbox PURCHASE is not this check's to
# make — it is a Paddle sandbox payment in a browser — so the check reads it and
# never starts one: an organisation with no unbound purchase reads as a row that
# could not be measured, naming what to do first.
#
# Usage:      bash scripts/live-checks/check570.sh --sandbox <checkout> --project <p> \
#               --token-file <file> --no-installation-project <project id>
#             bash scripts/live-checks/check570.sh --sandbox <checkout> --project <p> \
#               --token-file <file> --no-installation-project <id> --dry-run
#             bash scripts/live-checks/check570.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             curl (the portal API) and fugaro (the installed release — the login
#             and the refresh this check runs are the ones you have).
# Where:      your own machine, the STAGING project (`fugaro-staging`) and the
#             staging portal first. `fugaro-prod` is refused unless `--production`
#             is given, because production is only ever measured on purpose; a
#             value naming belong is refused by name; and so is a coding agent's
#             session, because every command this check runs is one you could type
#             yourself.
# Costs:      no model call and no run: one binding, one key, one refresh and a
#             handful of reads. The sandbox purchase you made first is Paddle
#             sandbox's own, which is not settled money.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check570_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. Nothing in the project is undone: the binding
#             and the key are the check's own evidence.
# Output:     progress on stderr, ONE block on stdout headed CHECK 570 RESULT,
#             then CHECK 570 RESULT END (exit N) — to copy back into the
#             register's Check 570 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             or the installation's configuration rather than a break — a
#             missing tool, a token file that is not 0600, a project whose
#             doctor disagrees with `--project`, an organisation with no unbound
#             purchase. A key the run never reached reads `FAIL — not reached`,
#             which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no key, no renewal secret and no ID token.
# The ID token travels in a 0600 curl config file and reaches neither a command
# line nor the block; every fact below is a NAME (a project id, a workspace id,
# an entitlement id, a tier, a status) or a NUMBER (seats, an HTTP status).
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK570_SANDBOX, CHECK570_PROJECT, CHECK570_TOKEN_FILE,
# CHECK570_NO_INSTALLATION_PROJECT, CHECK570_API (the portal's origin; the
# staging default is the staging site's own domain, `app.staging.fugaro.dev`,
# and the production one is `app.fugaro.dev` — P0.1, P9.2 — an origin typed from
# memory is how a check measures the wrong deployment),
# CHECK570_EXPECT_SEATS (1), CHECK570_PRODUCTION, CHECK570_DRY_RUN,
# CHECK570_KEEP_SCRATCH, CHECK570_LOGIN_SECONDS (600: the bound on the login this
# check runs, which is `fugaro licence login`'s own ten-minute code),
# CHECK570_REFRESH_SECONDS (180) and CHECK570_CLI_TIMEOUT_SECONDS (180).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check runs a login
# and a refresh, and a trace of them is a second copy of what they print.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block and the login's
# own prompt are written to the saved descriptors.
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

# run_bounded SECONDS KEY CMD...: run one command under a bound, from the
# checkout, capturing its output into $SCRATCH/KEY.{out,err}. The child execs, so
# the pid the bound kills IS the command — a hung cloud read would otherwise
# outlive the check that gave up on it. The ERR trap is dropped in it: this check
# runs commands whose failing exit status is one of its own readings, and a trap
# inherited there would turn each of them into "an internal error".
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; if [ -n "$SANDBOX" ] && [ -d "$SANDBOX" ]; then cd "$SANDBOX" || exit 1; fi; exec "$@" ) \
    >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

need_tools() {
  local t
  for t in bash mktemp python3 curl; do
    command -v "$t" >/dev/null 2>&1 || die "Check 570 needs '$t' and it is not on PATH"
  done
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""
BOUNDED_STATUS=0
BOUNDED_TIMED_OUT=no

SANDBOX=""
PROJECT=""
API=""
TOKEN_FILE=""
NO_INSTALLATION=""
EXPECT_SEATS=1
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
INSTALLATION=""
INSTALL_PROJECT=""
REGION=""
WORKSPACE_ID=""
ENTITLEMENT_ID=""
WORKSPACE_PROJECT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check570.XXXXXX") || die "Check 570 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK570_KEEP_SCRATCH is set)"
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
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK570_KEEP_SCRATCH is set)"
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
BLOCK_KEYS=(fugaro-version xtrace agent-session project belong api token installation purchase binding login seats restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 570 RESULT\n' >&3
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
  printf 'CHECK 570 RESULT END (exit %s)\n' "$status" >&3
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
# about the installation rather than this check's crash.

doctor_fields() { # doctor_fields FILE
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

entitlement_fields() { # entitlement_fields FILE — one line per entitlement
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
for one in doc.get("entitlements") or []:
    print("entitlement=%s|%s|%s|%s|%s|%s|%s" % (
        one.get("id") or "",
        one.get("workspace_id") or "-",
        one.get("source") or "",
        one.get("status") or "",
        one.get("seats"),
        one.get("bound_at") or "-",
        "yes" if one.get("air_gapped") else "no",
    ))
PY
}

workspace_fields() { # workspace_fields FILE — one line per workspace
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
for one in doc.get("workspaces") or []:
    print("workspace=%s|%s|%s|%s|%s" % (
        one.get("id") or "",
        one.get("name") or "",
        one.get("project_id") or "-",
        one.get("entitlement_id") or "-",
        one.get("bound_at") or "-",
    ))
PY
}

licence_fields() { # licence_fields FILE — `fugaro licence status --json`
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
print("project=%s" % (doc.get("project") or ""))
print("state=%s" % (doc.get("state") or ""))
print("tier=%s" % (doc.get("tier") or ""))
print("seats=%s" % (doc.get("seats")))
print("kind=%s" % (doc.get("kind") or ""))
print("paid_through=%s" % (doc.get("paid_through") or ""))
print("revoked=%s" % ("yes" if doc.get("revoked") else "no"))
PY
}

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

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check570.sh --sandbox <checkout> --project <p> --token-file <file> --no-installation-project <id> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a command's output to the terminal"
}

# guard_agent_session: the check runs `fugaro licence login` and `licence
# refresh`, which the CLI itself refuses inside an agent's session. Refusing here
# rather than letting each command refuse is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 570 is yours to run at your own terminal, on the staging project, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_inputs: the project, the origin and the two paths, read before anything
# runs. `fugaro-prod` is the one project a check never measures by accident: the
# rows here are about a sandbox installation, and production is only ever
# measured on purpose.
guard_inputs() {
  local all="${SANDBOX:-}${PROJECT:-}${API:-}${TOKEN_FILE:-}${NO_INSTALLATION:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: the rows below move a licence and mint a key on a real customer's installation, and production is only ever measured on purpose. Nothing was read"
  fi
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 570 to 575 run on fugaro-staging first, so read this block as a production run by choice"
  else
    fact project PASS "$PROJECT is the GCP project this check was given, and it is not fugaro-prod, which is refused without --production"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact api PASS "$API (the portal's origin; a real run holds it to one read that answers the API's own unauthenticated refusal)"
    fact token PASS "$TOKEN_FILE (a real run reads its mode and size, and never a byte of what it holds)"
    fact installation PASS "planned: ${SANDBOX} is read for the installation's name, project and region"
    return 0
  fi
  if [ ! -f "$TOKEN_FILE" ]; then
    unmeasured token "the token file $TOKEN_FILE is not there: this check reads the portal through YOUR OWN sign-in (copy the ID token your browser holds after signing in to $API), and a file that is not there is a row it cannot measure. Nothing about the portal was read"
    stop "the token file $TOKEN_FILE is not there, so none of the portal's rows could be read. Copy your ID token into a file of its own, then run the check again"
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

guard_no_installation_project() {
  local shape='^[a-z][a-z0-9-]{4,28}[a-z0-9]$'
  if [ "$NO_INSTALLATION" = "$PROJECT" ]; then
    die "--no-installation-project names $PROJECT, which is the installation this check reads: a project that HAS an installation cannot be the one a binding is refused for. Pass a project of your own that holds no Fugaro installation"
  fi
  if ! printf '%s' "$NO_INSTALLATION" | grep -Eq "$shape"; then
    die "--no-installation-project '$NO_INSTALLATION' is not a Firebase project id (6 to 30 characters of lower-case letters, digits and dashes, beginning with a letter): the shape is checked before the probe, and a project the check cannot name is one it will not send"
  fi
}

version_row() {
  local v=""
  if [ -n "$FUGARO_BIN" ]; then
    v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fi
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the login and the refresh are the ones this release makes)"
}

# ------------------------------------------------------------------ the steps
# step_installation: the premise. The installation, its project and its region are
# read from the checkout itself, so the check measures the installation you work
# in, and `--project` has to be the project that checkout belongs to.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of the installation's own repository)"
  fi
  run_bounded "$CLI_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured installation "fugaro doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX"
    stop "fugaro doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX, so the installation this check measures could not be named"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured installation "fugaro doctor --json exited ${BOUNDED_STATUS} in $SANDBOX: $(last_lines "$SCRATCH/doctor.err")"
    stop "fugaro doctor --json exited ${BOUNDED_STATUS} in $SANDBOX, so this check could not name the installation it measures"
  fi
  local fields="$SCRATCH/doctor.txt"
  doctor_fields "$SCRATCH/doctor.out" >"$fields"
  INSTALLATION=$(field "$fields" installation)
  INSTALL_PROJECT=$(field "$fields" project)
  REGION=$(field "$fields" region)
  if [ -z "$INSTALL_PROJECT" ]; then
    unmeasured installation "$SANDBOX's fugaro doctor --json answers no project: this checkout is not an installation's, and every row below rests on the installation it names. Run the check against a checkout that belongs to $PROJECT"
    stop "fugaro doctor --json in $SANDBOX answers no project, so no row of this check has an installation to be about"
  fi
  if [ "$INSTALL_PROJECT" != "$PROJECT" ]; then
    unmeasured installation "$SANDBOX's installation is ${INSTALLATION:-<unnamed>} in $INSTALL_PROJECT, and --project names $PROJECT: this check measures one installation and reads it from the checkout, so the two have to be the same project. Either point --sandbox at the checkout of $PROJECT or pass --project $INSTALL_PROJECT"
    stop "the checkout's installation is in $INSTALL_PROJECT and --project names $PROJECT: nothing here is about the installation you named"
  fi
  fact installation PASS "${INSTALLATION:-<unnamed>} in $PROJECT (region ${REGION:-unknown}), read from $SANDBOX's own fugaro doctor --json"
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
  local args=(-sS --max-time "$CLI_TIMEOUT" --config "$cfg" -o "$SCRATCH/$key.body" -w '%{http_code}' -X "$method")
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
  code=$(curl -sS --max-time "$CLI_TIMEOUT" -o "$SCRATCH/anonymous.body" -w '%{http_code}' "$API/api/workspaces" 2>"$SCRATCH/anonymous.curl.err" || true)
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

# entitlement_read: the owner's own entitlements, one `entitlement=…` line each.
# The row that reads it is about the sandbox PURCHASE — the check never starts
# one, because the purchase is a Paddle sandbox payment in a browser.
entitlement_read() {
  local code
  code=$(api_call GET /api/entitlements entitlements)
  if [ "$code" != 200 ]; then
    fault_fields "$SCRATCH/entitlements.body" >"$SCRATCH/entitlements.txt"
    unmeasured purchase "GET /api/entitlements with your sign-in answered ${code:-nothing} (code '$(field "$SCRATCH/entitlements.txt" code)'): this check is reading the portal as the account whose token is in $TOKEN_FILE, and a refusal here is that sign-in's own. Nothing about the purchase was read"
    stop "the portal refused the entitlement read with ${code:-no answer}, so the installation's licence state could not be measured"
  fi
  entitlement_fields "$SCRATCH/entitlements.body" >"$SCRATCH/entitlements.txt"
}

# step_purchase: **F25/D2197** — the sandbox purchase left an UNBOUND
# entitlement. `bound_at` null is the state itself: the licence is paid for, its
# term runs from the payment, and no key exists yet.
step_purchase() {
  entitlement_read
  local hit="" line
  while IFS= read -r line; do
    case "$line" in entitlement=*) ;; *) continue ;; esac
    local one=${line#entitlement=}
    local source status
    source=$(printf '%s' "$one" | cut -d'|' -f3)
    status=$(printf '%s' "$one" | cut -d'|' -f4)
    if [ "$source" = purchase ] && [ "$status" = active ]; then hit=$one; break; fi
  done <"$SCRATCH/entitlements.txt"
  if [ -z "$hit" ]; then
    local count
    count=$(grep -c '^entitlement=' "$SCRATCH/entitlements.txt" || true)
    unmeasured purchase "your organisation holds ${count} entitlement(s) and none of them is an active one whose source is 'purchase': this row is about the licence a SANDBOX purchase leaves behind, and the purchase is a browser step this check does not take. Buy one in the portal ($API) — its overlay is Paddle's sanbdbox — and run the check again. Nothing was bound or minted by this run"
    stop "no unbound sandbox purchase was found in the organisation, so the path F25 is about could not be measured"
  fi
  ENTITLEMENT_ID=$(printf '%s' "$hit" | cut -d'|' -f1)
  WORKSPACE_ID=$(printf '%s' "$hit" | cut -d'|' -f2)
  local bound seats airgapped
  bound=$(printf '%s' "$hit" | cut -d'|' -f6)
  seats=$(printf '%s' "$hit" | cut -d'|' -f5)
  airgapped=$(printf '%s' "$hit" | cut -d'|' -f7)
  if [ "$airgapped" = yes ]; then
    unmeasured purchase "the purchase ${ENTITLEMENT_ID} is air-gapped (D2206: issued once, at binding, as a file, and never revealed): this check reads a bound installation's key and an air-gapped licence has none to read. Use an ordinary sandbox purchase"
    stop "the sandbox purchase is air-gapped, which is a licence this check's rows are not about"
  fi
  if [ "$bound" != - ]; then
    unmeasured purchase "the purchase ${ENTITLEMENT_ID} is already bound (bound_at ${bound}), so the UNBOUND state D2196/D2197 is about is not the state in front of this check. Buy a fresh sandbox licence for a workspace you have not bound and run the check again"
    stop "the sandbox purchase is already bound, so this check cannot measure the unbound state it exists for"
  fi
  fact purchase PASS "the purchase ${ENTITLEMENT_ID} is active with ${seats} seat(s) and bound_at null (workspace ${WORKSPACE_ID:-none}): a licence paid for and not yet bound, which is F25's state — the term runs from the payment and binding is what mints the key"
}

# step_binding: **D2205** — binding needs an installation, and a project without
# one is refused rather than bound unchecked. This is the check's one write
# ATTEMPT, and the entitlement is re-read afterwards: a refusal that bound the
# licence anyway would be the finding this row exists for.
step_binding() {
  local body
  body=$(printf '{"entitlement_id":"%s","project_id":"%s"}' "$ENTITLEMENT_ID" "$NO_INSTALLATION")
  local code
  code=$(api_call POST /api/workspaces/bind bind "$body")
  fault_fields "$SCRATCH/bind.body" >"$SCRATCH/bind.txt"
  local fault_code message
  fault_code=$(field "$SCRATCH/bind.txt" code)
  message=$(field "$SCRATCH/bind.txt" message)
  if [ "$code" != 422 ] || [ "$fault_code" != no_installation ]; then
    fact binding FAIL "POST /api/workspaces/bind for ${ENTITLEMENT_ID} and the project $NO_INSTALLATION answered ${code:-nothing} with code '${fault_code:-none}' (${message:-no sentence}), and D2205's refusal is 422 'no_installation' whose sentence names 'fugaro setup'. That the two must agree is the whole of the row: a binding that went through is a licence bound to a project with no installation in it, and a row that reads as answered but carries another code is a person who is not told to run setup"
    stop "the binding for a project with no installation was not refused with 422 'no_installation', so D2205's refusal could not be measured and the licence is left unbound"
  fi
  case "$message" in
  *"fugaro setup"*) ;;
  *)
    fact binding FAIL "the refusal for $NO_INSTALLATION carries code 'no_installation' but its sentence is '${message:-empty}', which does not name 'fugaro setup': D2205's sentence is what sends a person to the one command that fixes it"
    stop "the refused binding did not name 'fugaro setup', so the sentence a person acts on is not the one D2205 registers"
    ;;
  esac
  entitlement_read
  local after
  after=$(grep -m1 "^entitlement=${ENTITLEMENT_ID}|" "$SCRATCH/entitlements.txt" || true)
  local bound
  bound=$(printf '%s' "$after" | cut -d'|' -f6)
  if [ "$bound" != - ]; then
    fact binding FAIL "the refused binding left ${ENTITLEMENT_ID} bound (bound_at ${bound}): a refusal that writes is the worst of both, because the person is told setup is needed and the licence is bound all the same"
    stop "the refused binding bound the licence anyway, so the login below would have nothing to prove"
  fi
  fact binding PASS "POST /api/workspaces/bind for ${ENTITLEMENT_ID} and the project $NO_INSTALLATION answered 422 'no_installation' — '${message}' — and the licence is still unbound: binding needs an installation (D2205)"
}

# step_login: the act that mints. The check RUNS `fugaro licence login` — the
# browser step is yours, and the command's own device code is its bound — and
# then reads the portal again: the entitlement is bound to the installation's own
# Firebase project id, and the installation holds a key.
step_login() {
  owner "running: fugaro licence login --device in $SANDBOX — sign in at the address it prints and type the code it gives you"
  local status=0
  run_bounded "$LOGIN_SECONDS" login "$FUGARO_BIN" licence login --device
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured login "fugaro licence login --device did not answer within ${LOGIN_SECONDS}s: $(last_lines "$SCRATCH/login.err"). The device code is worth ten minutes, so a run that outlasted the bound is one you did not finish at the licence site. Nothing was minted by this check"
    stop "the licence login did not finish within ${LOGIN_SECONDS}s, so nothing was bound and no key could be read"
  fi
  status=$BOUNDED_STATUS
  if [ "$status" -ne 0 ]; then
    unmeasured login "fugaro licence login --device exited ${status}: $(last_lines "$SCRATCH/login.err")"
    stop "fugaro licence login exited ${status}, so the installation was not signed in and no key was minted"
  fi
  entitlement_read
  local after bound workspace_project
  after=$(grep -m1 "^entitlement=${ENTITLEMENT_ID}|" "$SCRATCH/entitlements.txt" || true)
  bound=$(printf '%s' "$after" | cut -d'|' -f6)
  if [ "$bound" = - ] || [ -z "$bound" ]; then
    fact login FAIL "fugaro licence login --device answered, and ${ENTITLEMENT_ID} is still unbound (bound_at null) after it: the sign-in is supposed to be the act that binds the picked licence to this installation's Firebase project id (D2196), so a login that leaves the licence unbound minted nothing"
    stop "the licence login left ${ENTITLEMENT_ID} unbound, so nothing was minted and the seat row below has no licence to read"
  fi
  api_call GET /api/workspaces workspaces >/dev/null
  workspace_fields "$SCRATCH/workspaces.body" >"$SCRATCH/workspaces.txt"
  workspace_project=""
  local line
  while IFS= read -r line; do
    case "$line" in workspace=*) ;; *) continue ;; esac
    local id
    id=$(printf '%s' "${line#workspace=}" | cut -d'|' -f1)
    if [ "$id" = "$WORKSPACE_ID" ]; then
      workspace_project=$(printf '%s' "${line#workspace=}" | cut -d'|' -f3)
      break
    fi
  done <"$SCRATCH/workspaces.txt"
  if [ -z "$workspace_project" ] || [ "$workspace_project" = - ]; then
    fact login FAIL "fugaro licence login bound ${ENTITLEMENT_ID} (bound_at ${bound}) and the workspace ${WORKSPACE_ID:-none} names no project_id: the sign-in carries this installation's own Firebase project id and the site binds the licence to it, so a binding with no project id is one nothing can hold against the installation"
    stop "the binding names no project_id, so this check cannot say which installation holds the licence"
  fi
  run_bounded "$CLI_TIMEOUT" licence "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_STATUS" -ne 0 ] || [ "$BOUNDED_TIMED_OUT" = yes ]; then
    fact login FAIL "fugaro licence login bound ${ENTITLEMENT_ID} to ${workspace_project}, and this installation's own 'fugaro licence status --json' did not answer ($(last_lines "$SCRATCH/licence.err")): binding is supposed to MINT, and an installation that holds no key after a login is the half of D2196 that did not happen"
    stop "the installation's own licence did not answer after the login, so the mint could not be read back"
  fi
  licence_fields "$SCRATCH/licence.out" >"$SCRATCH/licence.txt"
  WORKSPACE_PROJECT=$workspace_project
  local tier state paid
  tier=$(field "$SCRATCH/licence.txt" tier)
  state=$(field "$SCRATCH/licence.txt" state)
  paid=$(field "$SCRATCH/licence.txt" paid_through)
  if [ -z "$tier" ] || [ -z "$paid" ]; then
    fact login FAIL "the licence this installation holds after the login carries tier '${tier:-none}' and paid_through '${paid:-none}': a key the portal minted for the purchase is the purchase's row and its term, so an installation holding a key with neither is not one bound to this entitlement"
    stop "the installation's licence carries no tier and no paid_through, so it is not the key this purchase minted"
  fi
  fact login PASS "fugaro licence login --device bound ${ENTITLEMENT_ID} to ${workspace_project} (the installation's own project, read back from the workspace) and this installation holds a ${tier} key in state '${state}' paid through ${paid}: binding is the act that mints (D2196)"
}

# step_seats: **K3.4/D2200** — a seat change reaches the installation through
# `fugaro licence refresh`, with nothing pasted. The check runs the refresh and
# reads the installation's own status: the seats there have to be the
# entitlement's, and the run has to have needed nothing from the terminal but the
# command.
step_seats() {
  local code
  code=$(api_call GET /api/entitlements entitlements-after)
  if [ "$code" != 200 ]; then
    unmeasured seats "GET /api/entitlements answered ${code:-nothing} after the login, so the entitlement's own seat count could not be read"
    stop "the entitlement could not be re-read after the login, so the seats row could not be measured"
  fi
  entitlement_fields "$SCRATCH/entitlements-after.body" >"$SCRATCH/entitlements-after.txt"
  local after seats
  after=$(grep -m1 "^entitlement=${ENTITLEMENT_ID}|" "$SCRATCH/entitlements-after.txt" || true)
  seats=$(printf '%s' "$after" | cut -d'|' -f5)
  if [ "$seats" != "$EXPECT_SEATS" ]; then
    unmeasured seats "the entitlement ${ENTITLEMENT_ID} carries ${seats:-no} seat(s) and this check was told to expect $EXPECT_SEATS: the seat purchase is a browser step this check does not take, so buy $EXPECT_SEATS seat(s) in the portal ($API) and run the check again with --expect-seats $seats"
    stop "the entitlement carries ${seats:-no} seat(s) and the check expected $EXPECT_SEATS, so the seat path was not the one in front of it"
  fi
  owner "running: fugaro licence refresh in $SANDBOX — it asks the licence server for this installation's next key"
  run_bounded "$REFRESH_SECONDS" refresh "$FUGARO_BIN" licence refresh
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured seats "fugaro licence refresh did not answer within ${REFRESH_SECONDS}s: $(last_lines "$SCRATCH/refresh.err")"
    stop "fugaro licence refresh did not answer within ${REFRESH_SECONDS}s, so the installation's own seats could not be read after it"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured seats "fugaro licence refresh exited ${BOUNDED_STATUS}: $(last_lines "$SCRATCH/refresh.err"). A refresh that refuses is a finding about the installation or the licence, and not one this check can read as a seat count"
    stop "fugaro licence refresh exited ${BOUNDED_STATUS}, so the installation's seats could not be read after it"
  fi
  run_bounded "$CLI_TIMEOUT" licence-after "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_STATUS" -ne 0 ] || [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured seats "fugaro licence status --json did not answer after the refresh: $(last_lines "$SCRATCH/licence-after.err")"
    stop "the installation's licence could not be read after the refresh"
  fi
  licence_fields "$SCRATCH/licence-after.out" >"$SCRATCH/licence-after.txt"
  local held
  held=$(field "$SCRATCH/licence-after.txt" seats)
  if [ "$held" != "$EXPECT_SEATS" ]; then
    fact seats FAIL "the portal's entitlement carries ${seats} seat(s) and the installation's own licence carries ${held:-none} after 'fugaro licence refresh': K3.4 is that a seat change reaches the installation through the refresh, so the two numbers are what the row compares. A refresh that answered and left the old count is the finding — read its own output above ($(last_lines "$SCRATCH/refresh.out"))"
    return 0
  fi
  fact seats PASS "the entitlement carries ${seats} seat(s) and this installation's licence carries the same ${held} after 'fugaro licence refresh' — the seat change reached the installation with nothing pasted into a terminal (K3.4, D2200)"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 570 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, its project and its region"
  say "  [network, read-only] GET ${API}/api/workspaces with no sign-in — 401 'unauthenticated', which says the origin is the portal's API"
  say "  [network, read-only] GET ${API}/api/entitlements with your sign-in — the sandbox purchase and its unbound state"
  say "  [network, WRITE ATTEMPT, refused] POST ${API}/api/workspaces/bind for the purchase and the project ${NO_INSTALLATION} — 422 'no_installation'"
  say "  [network, WRITE, billable-free] fugaro licence login --device in ${SANDBOX} — binds the purchase to this installation's Firebase project id and mints the key; the sign-in is yours in a browser"
  say "  [network, read-only] GET ${API}/api/workspaces and /api/entitlements — the binding read back"
  say "  [network, WRITE] fugaro licence refresh in ${SANDBOX}, then licence status --json — the seats, with nothing pasted"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no portal call, no login, no refresh"
}

main() {
  need_tools
  FUGARO_BIN=$(command -v fugaro || true)
  if [ -z "$FUGARO_BIN" ]; then
    die "Check 570 needs 'fugaro' on PATH: the check runs the login and the refresh you have installed, and it is not there"
  fi
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  guard_no_installation_project
  step_installation
  step_api
  step_purchase
  step_binding
  step_login
  step_seats
  fact restore PASS "nothing of this check's own is left behind: the binding, the key it minted and the refresh are the installation's, which is what the rows above exist to prove"
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
PROJECT_CLI=""
API_CLI=""
TOKEN_CLI=""
NO_INSTALLATION_CLI=""
USAGE="check570.sh --sandbox <checkout> --project <p> --token-file <file> --no-installation-project <project id> [--api <origin>] [--expect-seats N] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path. usage: $USAGE"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --project)
    [ $# -ge 2 ] || die "--project needs a GCP project id. usage: $USAGE"
    PROJECT_CLI=$2
    shift 2
    ;;
  --token-file)
    [ $# -ge 2 ] || die "--token-file needs a path. usage: $USAGE"
    TOKEN_CLI=$2
    shift 2
    ;;
  --no-installation-project)
    [ $# -ge 2 ] || die "--no-installation-project needs a Firebase project id. usage: $USAGE"
    NO_INSTALLATION_CLI=$2
    shift 2
    ;;
  --api)
    [ $# -ge 2 ] || die "--api needs the portal's origin. usage: $USAGE"
    API_CLI=$2
    shift 2
    ;;
  --expect-seats)
    [ $# -ge 2 ] || die "--expect-seats needs a number. usage: $USAGE"
    EXPECT_SEATS=$2
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

SANDBOX=${CHECK570_SANDBOX:-${SANDBOX_CLI:-}}
PROJECT=${CHECK570_PROJECT:-${PROJECT_CLI:-}}
TOKEN_FILE=${CHECK570_TOKEN_FILE:-${TOKEN_CLI:-}}
NO_INSTALLATION=${CHECK570_NO_INSTALLATION_PROJECT:-${NO_INSTALLATION_CLI:-}}
PRODUCTION=${CHECK570_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK570_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK570_KEEP_SCRATCH:-}
EXPECT_SEATS=${CHECK570_EXPECT_SEATS:-$EXPECT_SEATS}
API=${CHECK570_API:-${API_CLI:-}}
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
CLI_TIMEOUT=${CHECK570_CLI_TIMEOUT_SECONDS:-180}
LOGIN_SECONDS=${CHECK570_LOGIN_SECONDS:-600}
# A refresh is a call to the licence server, which has the CLI's own bound: this
# one is the extra fence for a build whose call never answers at all.
REFRESH_SECONDS=${CHECK570_REFRESH_SECONDS:-180}

if [ -z "$SANDBOX" ]; then
  die "Check 570 needs --sandbox: the checkout of the test installation this check binds a licence to. Usage: $USAGE"
fi
if [ -z "$PROJECT" ]; then
  die "Check 570 needs --project: the GCP project of that installation (fugaro doctor --json's own project.gcp_project, not Fugaro's name for it). Usage: $USAGE"
fi
if [ -z "$TOKEN_FILE" ]; then
  die "Check 570 needs --token-file: a file, mode 0600, holding one line — the ID token your browser holds after signing in to the portal. Usage: $USAGE"
fi
if [ -z "$NO_INSTALLATION" ]; then
  die "Check 570 needs --no-installation-project: a project id of your own that holds NO Fugaro installation, which is what the refused binding is about. Usage: $USAGE"
fi
case "$EXPECT_SEATS" in
"" | *[!0-9]*) die "--expect-seats '$EXPECT_SEATS' is not a number. usage: $USAGE" ;;
esac
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name another file there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
case "$TOKEN_FILE" in
/*) ;;
*) TOKEN_FILE="$PWD/$TOKEN_FILE" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
