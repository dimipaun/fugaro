#!/usr/bin/env bash
# Check 563 — the device code from a machine with no browser (K3.3): the sign-in
# a terminal prints an address and a short code for, approved on any machine that
# has a browser, and the poll that stops at the code's own ten minutes whatever
# the site answers.
#
# Plan task K7.4 of docs/licence-key-v2/implementation-plan.md writes this check,
# and the number is that work's own (its "Checks this plan takes (560–569)"). What
# it settles is read from the outside and nowhere else: **K3.3's device flow**
# (`fugaro licence login --device`, ee/register/licence_login.go and
# ee/licence/login's `Client.Device`) — a machine that cannot open a browser
# prints the licence site's own approve address and a short code and waits, a
# person opens that address on ANY machine that has a browser, signs in as the
# account that holds the licence, types the code, and the terminal is signed in
# with no browser ever opening on it. The address is the site's own device page
# (ee/keyservice/signin.go's `RouteDeviceApprove`, `/cli/device`), the code is
# K2.4's user code, and the wait is bounded by the code's ten minutes.
#
# What it measures, in the order the block reads:
#
#   * the three guards, before anything runs: no trace (xtrace), not a coding
#     agent's session (agent-session), no value naming belong (belong), the
#     installation is not `fugaro-prod` unless `--production` was given
#     (project), and the site is the CLI's own licence site (site);
#   * the installation under test, read from the checkout (`fugaro doctor --json`
#     in --sandbox): its name, its GCP project and its region, all three present —
#     a real installation, which is what a sign-in is about;
#   * **the address** the device flow prints (`address`): the licence site's own
#     approve page — https, on the site, on the approve route `/cli/device` — and
#     the row says that no browser was opened on this machine and none is needed;
#   * **the code** it prints (`code`): eight characters of K2.4's alphabet
#     (`A-H`, `J-N`, `P-Z` — no I and no O — and `2-9` — no 0 and no 1), grouped
#     four and four with one `-`, and the block's own sentence names its
#     ten-minute life (`The code is worth 10m0s.`);
#   * ONE of the flow's two halves, chosen by `--approve` (the default) or
#     `--expiry`, so it is one command either way:
#       - `--approve`: the check prints ONE `MANUAL:` line with the exact steps
#         and waits, bounded, for the login to end. The row holds that it exited
#         0 and that `fugaro licence status --json` in the checkout now reads
#         `source: project` with a non-empty `tier` and a state of `active` or
#         `grace` — the sign-in installed the installation's own key. The key is
#         NEVER read or printed. The `expiry` row reads INFO (`not this run's
#         half`);
#       - `--expiry`: nothing is approved. The check waits for the code to run
#         out and holds that the login ended with the client's own refusal — `the
#         sign-in was not finished before its code expired` — that the
#         installation's licence is UNCHANGED from a read taken before the flow
#         started (`source`, `tier`, `state` and `paid_through`), and that the
#         address and code held above were live while it waited. The `signin` row
#         reads INFO (`not this run's half`). This half proves the code is worth
#         ten minutes and that the poll STOPS at the expiry rather than hanging.
#
# What it creates: under `--approve`, the key the sign-in installs — the
# installation's own licence, written by `fugaro licence login` to the project's
# store and the renewal secret to its own container (D2137), and kept on purpose.
# Under `--expiry`, nothing: the flow writes nothing, and the row says so. This
# check starts no run, makes no cloud write of its own and calls no API itself:
# the device flow's two calls are the CLI's.
#
# Usage:      bash scripts/live-checks/check563.sh --sandbox <a checkout>
#             bash scripts/live-checks/check563.sh --sandbox <a checkout> --approve
#             bash scripts/live-checks/check563.sh --sandbox <a checkout> --expiry
#             bash scripts/live-checks/check563.sh --help
#             --sandbox is a checkout of the installation being signed in (a
#             throwaway/staging one, never belong); the installed `fugaro` is run
#             from it. --site holds the address the flow prints against the
#             origin you expect. --wait bounds the wait for the flow.
# Needs:      bash, mktemp and python3 (the device block is read with it). No
#             `fugaro` command of this check's own beyond the login and the
#             status read it runs in the checkout, no `curl`, no `gcloud`, and no
#             browser.
# Where:      your own machine and the STAGING installation first.
#             `fugaro-prod` is refused unless `--production` is given, because
#             production is only ever measured on purpose; a value naming belong
#             is refused by name; and so is a coding agent's session.
# Costs:      no model call and no run: one sign-in and its reads. Nothing
#             billable.
# Runs:       by YOU. No agent runs any step of it — the CLI itself refuses the
#             sign-in in an agent's session — and its own test
#             (check563_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the background login killed with it.
#             Under `--approve` the key the sign-in installed is kept: it is the
#             installation's own licence. Under `--expiry` nothing was written.
# Output:     progress on stderr, the `MANUAL:` line (under `--approve`) and ONE
#             block on stdout headed CHECK 563 RESULT, then CHECK 563 RESULT END
#             (exit N) — to copy back into the register's Check 563 section.
#             Exit 0: the half this run took held. 1: a measured criterion did
#             not hold — a code that is not K2.4's shape, an address that is not
#             the site's device page, a login that ended 0 without a key, a poll
#             that did not stop at the expiry, a status that changed under
#             `--expiry`, or a guard that stopped the check. 2: a row could not
#             be measured — the site is not the licence site, the installation
#             has no Firebase project, a `doctor` that answers no installation,
#             an approval that never came. A key the run never reached reads
#             `FAIL — not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: the device code, the key and the renewal
# secret are never read and never printed. The device flow answers a device code
# beside the user code, and the CLI shows only the user code — the half the site
# requires a person to type, which is what makes a page a person did not open
# useless to whoever showed it to them — so this check reads the user code and
# NEVER touches the device code at all. The key the sign-in installs is read
# only through `licence status --json`, which carries no key (ee/register/
# licence.go); the check shows the user code because the CLI shows it and a
# person has to type it, and shows no other code at all.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK563_SANDBOX, CHECK563_SITE (the licence site's origin: the CLI's own
# constant is https://keys.fugaro.dev, licenceServerURL in ee/register/licence.go,
# which no environment variable changes), CHECK563_EXPIRY (1 takes the expiry
# half instead of the approval), CHECK563_WAIT_SECONDS (700: the ten-minute code
# plus a margin), CHECK563_START_SECONDS (60: the bound on the device block
# appearing), CHECK563_CLI_TIMEOUT_SECONDS (180: the bound on each `fugaro` read),
# CHECK563_PRODUCTION, CHECK563_DRY_RUN and CHECK563_KEEP_SCRATCH.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check runs a
# sign-in, and a trace of the login is a second copy of the address and code it
# prints (and of anything the command prints beside them).
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
  ( trap - ERR; cd "$SANDBOX" || exit 1; exec "$@" ) \
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

# login_start: start `fugaro licence login --device` in the BACKGROUND, from the
# checkout, with its output in the scratch directory. It is not waited for here,
# because the check has to read the address and the code it prints WHILE it is
# still waiting for the sign-in. LOGIN_PID is killed by the EXIT trap whatever
# happens, so the check never leaves it behind.
login_start() {
  : >"$SCRATCH/login.out"
  : >"$SCRATCH/login.err"
  ( trap - ERR; cd "$SANDBOX" || exit 1; exec "$FUGARO_BIN" licence login --device ) \
    >>"$SCRATCH/login.out" 2>>"$SCRATCH/login.err" &
  LOGIN_PID=$!
  LOGIN_STATUS=0
  LOGIN_TIMED_OUT=no
}

# device_block_wait: wait, bounded, until the login has printed its device block
# (or ended). The block comes back as soon as the site answers the start call, so
# this is short; a login that ends without one is read as its own refusal.
device_block_wait() {
  local waited=0
  while [ "$waited" -lt "$START_SECONDS" ]; do
    if grep -qF "Open this address in a browser, on any machine:" "$SCRATCH/login.out" 2>/dev/null; then
      return 0
    fi
    kill -0 "$LOGIN_PID" 2>/dev/null || return 0
    sleep 1
    waited=$((waited + 1))
  done
  return 0
}

# login_wait: wait, bounded, for the background login to end; a run that outlasts
# the bound is killed so nothing is left behind.
login_wait() {
  local waited=0
  while kill -0 "$LOGIN_PID" 2>/dev/null; do
    [ "$waited" -lt "$WAIT_SECONDS" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$LOGIN_PID" 2>/dev/null; then
    LOGIN_TIMED_OUT=yes
    kill -TERM "$LOGIN_PID" 2>/dev/null || true
    sleep 2
    kill -KILL "$LOGIN_PID" 2>/dev/null || true
  fi
  LOGIN_STATUS=0
  wait "$LOGIN_PID" 2>/dev/null || LOGIN_STATUS=$?
}

# login_says PATTERN: whether the login's own output (either stream) carries a
# sentence. The expired refusal is a user error the command writes to stderr; the
# block is on stdout.
login_says() { # login_says PATTERN
  grep -qF "$1" "$SCRATCH/login.out" 2>/dev/null && return 0
  grep -qF "$1" "$SCRATCH/login.err" 2>/dev/null && return 0
  return 1
}

need_tools() {
  local t
  for t in bash mktemp python3; do
    command -v "$t" >/dev/null 2>&1 || die "Check 563 needs '$t' and it is not on PATH"
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
LOGIN_PID=""
LOGIN_STATUS=0
LOGIN_TIMED_OUT=no

SANDBOX=""
SITE=""
EXPIRY=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
INSTALLATION=""
INSTALL_PROJECT=""
REGION=""
ADDRESS=""
CODE=""
DEVICE_TTL=""
MANUAL=""

# The CLI's own licence site, a right constant and not a configurable one
# (licenceServerURL, ee/register/licence.go). This check holds the address the
# device flow prints against this origin and no other.
LICENCE_SITE=https://keys.fugaro.dev
LICENCE_SITE_HOST=keys.fugaro.dev

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check563.XXXXXX") || die "Check 563 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK563_KEEP_SCRATCH is set)"
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

# kill_login: end the background login if it is still running. Called by the EXIT
# trap before the scratch directory goes, so a Ctrl-C or a failed assertion never
# leaves a poll behind.
kill_login() {
  [ -n "$LOGIN_PID" ] || return 0
  kill -0 "$LOGIN_PID" 2>/dev/null || return 0
  kill -TERM "$LOGIN_PID" 2>/dev/null || true
  sleep 1
  kill -KILL "$LOGIN_PID" 2>/dev/null || true
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  kill_login
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK563_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap, and the background login with it"
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
BLOCK_KEYS=(xtrace agent-session belong project site sandbox address code signin expiry restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 563 RESULT\n' >&3
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
  printf 'CHECK 563 RESULT END (exit %s)\n' "$status" >&3
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

# url_parts URL: the four pieces this check holds an address against.
url_parts() { # url_parts URL
  python3 - "$1" <<'PY'
import sys
from urllib.parse import urlsplit
u = urlsplit(sys.argv[1])
print("scheme=%s" % u.scheme)
print("host=%s" % (u.hostname or ""))
print("port=%s" % (u.port if u.port else ""))
print("path=%s" % u.path)
PY
}

# device_fields FILE: the two values the device flow prints, and its own sentence
# for the code's life. It reads the block the CLI wrote (ee/licence/login's
# `Client.Device`) and nothing else — the device code beside the user code never
# reaches a line of the CLI's output, so there is nothing here to read it from.
device_fields() { # device_fields FILE
  python3 - "$1" <<'PY'
import re, sys
try:
    text = open(sys.argv[1], encoding="utf-8", errors="replace").read()
except Exception:
    raise SystemExit(0)

def grab(pattern):
    m = re.search(pattern, text)
    return m.group(1) if m else ""

print("address=%s" % grab(r"Open this address in a browser, on any machine:[^\n]*\n\s*\n\s*(\S+)"))
print("code=%s" % grab(r"and type this code when it asks:[^\n]*\n\s*\n\s*(\S+)"))
print("ttl=%s" % grab(r"The code is worth (\S+?)\."))
PY
}

# doctor_fields FILE: the installation's own three fields, from `doctor --json`.
doctor_fields() { # doctor_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
project = doc.get("project") or {}
print("name=%s" % (project.get("name") or ""))
print("project=%s" % (project.get("gcp_project") or ""))
print("region=%s" % (project.get("region") or ""))
PY
}

# status_fields FILE: the four fields this check holds the installation's licence
# against. `licence status --json` carries no key (ee/register/licence.go), so
# there is nothing here that could print one.
status_fields() { # status_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
print("source=%s" % (doc.get("source") or ""))
print("state=%s" % (doc.get("state") or ""))
print("tier=%s" % (doc.get("tier") or ""))
print("paid_through=%s" % (doc.get("paid_through") or ""))
PY
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell is tracing (bash -x), and this check runs a sign-in: a trace of the login is a second copy of the address and code it prints. Run it without -x — 'bash scripts/live-checks/check563.sh --sandbox <checkout>' — the check prints its own RESULT block, so a trace is never needed to report a failure"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check563.sh --sandbox <checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of the login is a second copy of the address and code it prints"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the address or the code the device flow prints to the terminal"
}

# guard_agent_session: this check runs a sign-in, which the CLI itself refuses
# inside an agent's session. Refusing here rather than letting the command refuse
# is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 563 is yours to run at your own terminal, and a sign-in is a person's own account — no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 563 is yours to run at your own terminal, and a sign-in is a person's own account — no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_site: the address the device flow prints is the CLI's own licence site's
# approve page, and the CLI's licence site is a right constant (licenceServerURL,
# ee/register/licence.go) that no environment variable changes. A `--site` that
# is not that origin cannot be the origin the printed address is held against, so
# the row is one this check could not measure.
guard_site() {
  url_parts "$SITE" >"$SCRATCH/site.txt"
  local scheme host
  scheme=$(field "$SCRATCH/site.txt" scheme)
  host=$(field "$SCRATCH/site.txt" host)
  if [ "$scheme" != https ]; then
    unmeasured site "the site this check was given ($SITE) is not an https address, and the licence site's own address is the constant $LICENCE_SITE (licenceServerURL, ee/register/licence.go). A device code's address is one this check holds against the site, and a site that is not https cannot be it. Nothing was run"
    stop "the site $SITE is not an https address, so the address the flow would print could not be held against it"
  fi
  if [ "$host" != "$LICENCE_SITE_HOST" ]; then
    unmeasured site "the site this check was given ($SITE) is not the licence site: the CLI's own licence site is the constant $LICENCE_SITE (licenceServerURL, ee/register/licence.go), which no environment variable changes, so the address a login prints can only be held against that origin. Pass --site $LICENCE_SITE, or nothing. Nothing was run"
    stop "the site $SITE is not the CLI's own licence site ($LICENCE_SITE), so the printed address could not be held against the site the CLI actually uses"
  fi
  fact site PASS "$SITE is the licence site — the CLI's own constant licenceServerURL (ee/register/licence.go), which nothing configures — and the address the device flow prints is held against this origin"
}

# guard_inputs: the values this check was given, read before anything runs.
# `fugaro-prod` is the one installation a check never signs in by accident, and
# its project is read from the checkout itself, so that guard is applied in
# step_sandbox once doctor has answered; here belong and the site are read, and
# a dry run is told what a real run would hold.
guard_inputs() {
  local all="${SANDBOX:-}${SITE:-}"
  case "$all" in
  *belong*)
    fact belong FAIL "a value this check was given names belong ($all): this check signs an installation in to the licence site, and it is never run there. Nothing was run"
    stop "a value this check was given names belong: this check is never run there"
    ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  guard_site
  if [ "$DRY_RUN" = 1 ]; then
    fact project PASS "planned: the installation's own project is read from $SANDBOX's fugaro doctor --json, and fugaro-prod is refused unless --production is given"
    fact sandbox PASS "planned: ${SANDBOX} is read for the installation's name, project and region"
    fact address PASS "planned: the address fugaro licence login --device prints is held to https, on $SITE, path /cli/device — no browser is opened here, and none is needed"
    fact code PASS "planned: the code it prints is held to K2.4's shape (8 characters of A-HJ-NP-Z2-9, grouped 4 and 4) and to the block's ten-minute life (10m0s)"
    return 0
  fi
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of the installation's own repository)"
  fi
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the premise. The installation, its project and its region are read
# from the checkout itself with `fugaro doctor --json`, so the check signs in the
# installation you work in, and a project with no installation is a row it cannot
# measure. `fugaro-prod` is refused here, on the project doctor names.
step_sandbox() {
  run_bounded "$CLI_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured sandbox "fugaro doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX"
    stop "fugaro doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX, so the installation this check signs in could not be named"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured sandbox "fugaro doctor --json exited ${BOUNDED_STATUS} in $SANDBOX: $(last_lines "$SCRATCH/doctor.err")"
    stop "fugaro doctor --json exited ${BOUNDED_STATUS} in $SANDBOX, so this check could not name the installation it signs in"
  fi
  local fields="$SCRATCH/doctor.txt"
  doctor_fields "$SCRATCH/doctor.out" >"$fields"
  INSTALLATION=$(field "$fields" name)
  INSTALL_PROJECT=$(field "$fields" project)
  REGION=$(field "$fields" region)
  if [ -z "$INSTALLATION" ] || [ -z "$INSTALL_PROJECT" ] || [ -z "$REGION" ]; then
    unmeasured sandbox "$SANDBOX's fugaro doctor --json answers name '${INSTALLATION:-none}', project '${INSTALL_PROJECT:-none}' and region '${REGION:-none}': this checkout is not a real installation's, and every row below rests on the installation it names. Run the check against the checkout of an installation ($SANDBOX/fugaro doctor --json must carry project.name, project.gcp_project and project.region)"
    stop "fugaro doctor --json in $SANDBOX does not answer a whole installation, so no row of this check has one to be about"
  fi
  if [ "$INSTALL_PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    fact project FAIL "the installation in $SANDBOX is in fugaro-prod (fugaro doctor --json's project.gcp_project), which is refused unless --production is given: the rows below sign an installation in to the licence site, and production is only ever measured on purpose. Nothing was run"
    stop "the installation in $SANDBOX is in fugaro-prod, which is refused unless --production is given: the rows below sign an installation in to the licence site, and production is only ever measured on purpose. Nothing was run"
  fi
  if [ "$INSTALL_PROJECT" = fugaro-prod ]; then
    fact project PASS "$INSTALL_PROJECT — production, named with --production: the registration's own rule is that Checks 560 to 569 run on staging first, so read this block as a production run by choice"
  else
    fact project PASS "$INSTALL_PROJECT is the installation's own GCP project (fugaro doctor --json's project.gcp_project), and it is not fugaro-prod, which is refused without --production"
  fi
  fact sandbox PASS "${INSTALLATION:-<unnamed>} in $INSTALL_PROJECT (region $REGION), read from $SANDBOX's own fugaro doctor --json: a real installation, which is what this check signs in"
}

# step_status_before: the read `--expiry` holds the installation's licence
# against. It is taken BEFORE the flow starts, so a write of any kind during the
# wait would show as a difference.
step_status_before() {
  run_bounded "$CLI_TIMEOUT" licence-before "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured expiry "fugaro licence status --json could not be read before the flow started ($(last_lines "$SCRATCH/licence-before.err")): this half compares the installation's licence before and after, so with no 'before' there is nothing to hold the code's expiry against. Nothing was run"
    stop "the installation's licence could not be read before the flow, so the expiry half had nothing to compare"
  fi
}

# step_device: start `fugaro licence login --device` in the background and read
# the two values it prints. `--device` is used precisely because this machine has
# no browser: the check never opens one and says so in the address row.
step_device() {
  owner "running: $FUGARO_BIN licence login --device in $SANDBOX — it prints an address and a code, and this machine needs no browser"
  login_start
  device_block_wait
  device_fields "$SCRATCH/login.out" >"$SCRATCH/device.txt"
  ADDRESS=$(field "$SCRATCH/device.txt" address)
  CODE=$(field "$SCRATCH/device.txt" code)
  DEVICE_TTL=$(field "$SCRATCH/device.txt" ttl)
  if [ -z "$ADDRESS" ]; then
    if login_says "fugaro update --firebase-project"; then
      unmeasured address "this installation has no Firebase project (budget.firebase_project is not set), so 'fugaro licence login' refuses before it prints anything: an operator runs 'fugaro update --firebase-project <project> --name <name>' (licenceNoWorkspace, ee/register/licence.go), then run the check again. Nothing was printed and nothing was signed in"
      stop "the installation has no Firebase project, so the device flow could not be started and nothing was printed"
    fi
    unmeasured address "fugaro licence login --device printed no device block within ${START_SECONDS}s ($(last_lines "$SCRATCH/login.err")): the flow is one this check reads from its output, and with no address there is nothing to hold against the site. Nothing was signed in"
    stop "the login printed no address, so neither the address nor the code could be read"
  fi
  url_parts "$ADDRESS" >"$SCRATCH/address.txt"
  local a_scheme a_host a_path site_host site_path expect_path
  a_scheme=$(field "$SCRATCH/address.txt" scheme)
  a_host=$(field "$SCRATCH/address.txt" host)
  a_path=$(field "$SCRATCH/address.txt" path)
  site_host=$(field "$SCRATCH/site.txt" host)
  site_path=$(field "$SCRATCH/site.txt" path)
  expect_path="${site_path}/cli/device"
  if [ "$a_scheme" != https ] || [ "$a_host" != "$site_host" ] || [ "$a_path" != "$expect_path" ]; then
    fact address FAIL "fugaro licence login --device printed the address '$ADDRESS', and the device flow's own approve page is https, on $SITE, path ${expect_path} (K2.4's RouteDeviceApprove, /cli/device): a person typing the code there reaches a page this site does not serve, so the address the terminal hands out is not the site's own device page"
    stop "the address the login printed is not the licence site's device page, so nothing was approved"
  fi
  fact address PASS "the login printed the licence site's own approve page $ADDRESS — https, on $SITE, path /cli/device — and no browser was opened on this machine, and none is needed (--device): the address is for a browser on ANY machine"
  if ! printf '%s' "$CODE" | grep -Eq '^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$'; then
    fact code FAIL "the login printed the user code '${CODE:-none}', and K2.4's user code is eight characters of its alphabet (A-H, J-N and P-Z without I and O, and 2-9 without 0 and 1), grouped four and four with one '-': a code a person cannot read back letter by letter is the wrong shape"
    stop "the user code the login printed is not K2.4's shape"
  fi
  if [ "$DEVICE_TTL" != "10m0s" ]; then
    fact code FAIL "the login printed the user code $CODE and says it is worth '${DEVICE_TTL:-nothing}', and K3.3's device code is worth ten minutes (the block reads 'The code is worth 10m0s.'): a code with another life is not the ten-minute code this check is about"
    stop "the login says the code is worth ${DEVICE_TTL:-nothing}, not ten minutes"
  fi
  fact code PASS "the login printed the user code $CODE — eight characters of K2.4's alphabet (A-H, J-N and P-Z without I and O, and 2-9 without 0 and 1), grouped four and four — and the block says it is worth 10m0s, the ten-minute life (the device code beside it is never read or printed)"
}

# step_approve: the default half. The browser steps are a person's, so the check
# prints ONE MANUAL line and waits, bounded, for the login to end; then it reads
# the installation's own licence and holds that the sign-in installed a key.
step_approve() {
  MANUAL="open $ADDRESS on ANY machine that has a browser, sign in there as the account that holds the licence, type the code $CODE when it asks, and pick/bind the licence this installation should run on — no browser is opened on this machine, and none is needed"
  owner "$MANUAL"
  owner "waiting up to ${WAIT_SECONDS}s for the sign-in to be approved"
  login_wait
  if [ "$LOGIN_TIMED_OUT" = yes ]; then
    fact expiry INFO "not this run's half — this run took --approve"
    unmeasured signin "the sign-in was not approved within ${WAIT_SECONDS}s: the code is worth 10m0s, so a login that outlasted the bound is one nobody finished at the licence site. Open $ADDRESS on a machine with a browser, type $CODE, approve the licence this installation should run on, and run the check again. Nothing was installed by this run"
    stop "the sign-in was not approved within ${WAIT_SECONDS}s, so nothing was installed and no key could be read"
  fi
  if login_says "the sign-in was not finished before its code expired"; then
    fact expiry INFO "the login this run started ended with the code's own expiry sentence — what the run read: $(last_lines "$SCRATCH/login.err")"
    unmeasured signin "the code expired before it was approved: 'the sign-in was not finished before its code expired'. This run took --approve, so the code had to be approved within its ten minutes. Run the check again and approve the sign-in at $ADDRESS with the code it prints, inside the code's ten minutes. Nothing was installed"
    stop "the code expired before it was approved, so the approval this half is about never happened"
  fi
  if [ "$LOGIN_STATUS" -ne 0 ]; then
    fact expiry INFO "not this run's half — this run took --approve"
    unmeasured signin "fugaro licence login --device exited ${LOGIN_STATUS}: $(last_lines "$SCRATCH/login.err"). A login that refuses is a finding about the installation or the site, and not one this check can read as an installed key. Nothing was installed by this run"
    stop "fugaro licence login exited ${LOGIN_STATUS}, so no key was installed"
  fi
  run_bounded "$CLI_TIMEOUT" licence-after "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then
    fact expiry INFO "not this run's half — this run took --approve"
    unmeasured signin "fugaro licence login answered 0, and this installation's own 'fugaro licence status --json' did not answer ($(last_lines "$SCRATCH/licence-after.err")): the sign-in is supposed to install the key, and a status that cannot be read after it leaves the installed key unproven"
    stop "the installation's licence could not be read after the login, so the installed key could not be held"
  fi
  status_fields "$SCRATCH/licence-after.out" >"$SCRATCH/licence-after.txt"
  local source state tier
  source=$(field "$SCRATCH/licence-after.txt" source)
  state=$(field "$SCRATCH/licence-after.txt" state)
  tier=$(field "$SCRATCH/licence-after.txt" tier)
  if [ "$source" != project ] || [ -z "$tier" ]; then
    fact expiry INFO "not this run's half — this run took --approve"
    fact signin FAIL "fugaro licence login --device answered 0, and this installation's licence reads source '${source:-none}' with tier '${tier:-none}': a sign-in installs the installation's own key in the project's store (D2137), so a licence with no project key after it is a login that ended 0 without a key"
    stop "the login ended 0 and the installation holds no project key, so the sign-in did not install one"
  fi
  case "$state" in
  active | grace) ;;
  *)
    fact expiry INFO "not this run's half — this run took --approve"
    fact signin FAIL "fugaro licence login --device installed a ${tier} key whose state reads '${state:-none}': a licence the sign-in just issued is active (or in its own grace), so a key in state '${state:-none}' is one the installation will not run on"
    stop "the installed key is in state '${state:-none}', not active or grace"
    ;;
  esac
  fact signin PASS "fugaro licence login --device answered 0, and this installation's own licence now reads source 'project' with tier '$tier' and state '$state': the sign-in installed the installation's own key in the project's store, and the key itself was never read (the reply carries no key)"
  fact expiry INFO "not this run's half — this run took --approve"
}

# step_expiry: the other half. Nothing is approved; the check waits for the code
# to run out and holds that the poll stopped at the expiry and wrote nothing.
step_expiry() {
  owner "this run took --expiry: nothing is approved, and the check waits for the code to run out (up to ${WAIT_SECONDS}s)"
  login_wait
  run_bounded "$CLI_TIMEOUT" licence-after "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then
    fact signin INFO "not this run's half — this run took --expiry"
    unmeasured expiry "fugaro licence status --json could not be read after the code expired ($(last_lines "$SCRATCH/licence-after.err")): this half holds that the installation's licence is UNCHANGED, and with no 'after' read there is nothing to compare"
    stop "the installation's licence could not be read after the flow, so the code's expiry could not be held against it"
  fi
  status_fields "$SCRATCH/licence-before.out" >"$SCRATCH/status-before.txt"
  status_fields "$SCRATCH/licence-after.out" >"$SCRATCH/status-after.txt"
  local b_source b_state b_tier b_paid a_source a_state a_tier a_paid
  b_source=$(field "$SCRATCH/status-before.txt" source)
  b_state=$(field "$SCRATCH/status-before.txt" state)
  b_tier=$(field "$SCRATCH/status-before.txt" tier)
  b_paid=$(field "$SCRATCH/status-before.txt" paid_through)
  a_source=$(field "$SCRATCH/status-after.txt" source)
  a_state=$(field "$SCRATCH/status-after.txt" state)
  a_tier=$(field "$SCRATCH/status-after.txt" tier)
  a_paid=$(field "$SCRATCH/status-after.txt" paid_through)
  if [ "$b_source" != "$a_source" ] || [ "$b_state" != "$a_state" ] || [ "$b_tier" != "$a_tier" ] || [ "$b_paid" != "$a_paid" ]; then
    fact signin INFO "not this run's half — this run took --expiry"
    fact expiry FAIL "the installation's licence changed under --expiry: before source '$b_source' tier '$b_tier' state '$b_state' paid_through '$b_paid', after source '$a_source' tier '$a_tier' state '$a_paid' paid_through '$a_paid'. A code nobody approved writes nothing, so a licence that moved while this run waited is a flow that installed a key it should not have"
    stop "the installation's licence changed while the code ran out, so something was written under --expiry"
  fi
  if [ "$LOGIN_TIMED_OUT" = yes ]; then
    fact signin INFO "not this run's half — this run took --expiry"
    fact expiry FAIL "the poll did not stop at the code's own expiry: fugaro licence login --device was still running after ${WAIT_SECONDS}s, and K3.3's poll stops at the code's ten minutes whatever the site answers (ErrExpired, ee/licence/login). The code printed above was worth 10m0s"
    stop "the login outlasted the bound with the code at its expiry, so the poll did not stop"
  fi
  if login_says "the sign-in was not finished before its code expired"; then
    fact signin INFO "not this run's half — this run took --expiry"
    fact expiry PASS "the code ran out at its own ten minutes: fugaro licence login --device ended with the client's own refusal — 'the sign-in was not finished before its code expired' — and the installation's licence is UNCHANGED (source '$a_source', tier '$a_tier', state '$a_state', paid_through '$a_paid'), so the poll stopped at the expiry rather than hanging, and nothing was written. The address and code held above were live for the whole wait"
    return 0
  fi
  fact signin INFO "not this run's half — this run took --expiry"
  fact expiry FAIL "fugaro licence login --device ended (exit ${LOGIN_STATUS}) without the client's own expiry refusal: the run read $(last_lines "$SCRATCH/login.err"). Under --expiry the code has to run out, and the ending K3.3 gives it is 'the sign-in was not finished before its code expired'"
  stop "the login ended without the code's expiry refusal, so the expiry half could not be measured"
}

step_restore() {
  if [ "$EXPIRY" = 1 ]; then
    fact restore PASS "nothing of this check's own is left: the scratch directory goes from the exit trap (and the background login with it), and under --expiry nothing was written anywhere — the installation's licence is the one it held before the flow started"
  else
    fact restore PASS "nothing of this check's own is left: the scratch directory goes from the exit trap (and the background login with it); the key this sign-in installed is the installation's own licence, written by 'fugaro licence login' to the project's store (D2137), and it is kept on purpose — a check does not delete a person's licence"
  fi
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 563 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [no browser] start '$FUGARO_BIN licence login --device' in ${SANDBOX} — the device code from a machine with no browser (K3.3)"
  say "  [hold] the address it prints is held to https, on ${SITE}, path /cli/device — no browser is opened here, and none is needed"
  say "  [hold] the code it prints is held to K2.4's shape (8 characters of A-HJ-NP-Z2-9, grouped 4 and 4) and to the block's ten-minute life (10m0s)"
  if [ "$EXPIRY" = 1 ]; then
    say "  [wait] let the code run out (up to ${WAIT_SECONDS}s): the poll must stop at the expiry and write nothing — licence status --json must read the same before and after"
  else
    say "  [wait, yours] approve at the printed address on ANY machine with a browser, type the printed code, and pick/bind the licence (up to ${WAIT_SECONDS}s); licence status --json must then read source 'project'"
  fi
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no login, no browser opened, and no licence read"
}

main() {
  need_tools
  FUGARO_BIN=$(command -v fugaro || true)
  if [ -z "$FUGARO_BIN" ]; then
    die "Check 563 needs 'fugaro' on PATH: the check runs the installed release's 'licence login --device' and 'licence status --json' in the checkout, and it is not there"
  fi
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_sandbox
  if [ "$EXPIRY" = 1 ]; then
    step_status_before
  fi
  step_device
  if [ "$EXPIRY" = 1 ]; then
    step_expiry
  else
    step_approve
  fi
  step_restore
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
SITE_CLI=""
WAIT_CLI=""
MODE_CLI=""
USAGE="check563.sh --sandbox <checkout> [--site <origin>] [--approve|--expiry] [--wait <seconds>] [--production] [--dry-run]"
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
  --site)
    [ $# -ge 2 ] || die "--site needs the licence site's origin. usage: $USAGE"
    SITE_CLI=$2
    shift 2
    ;;
  --approve)
    MODE_CLI=approve
    shift
    ;;
  --expiry)
    MODE_CLI=expiry
    shift
    ;;
  --wait)
    [ $# -ge 2 ] || die "--wait needs a number of seconds. usage: $USAGE"
    WAIT_CLI=$2
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

SANDBOX=${CHECK563_SANDBOX:-${SANDBOX_CLI:-}}
SITE=${CHECK563_SITE:-${SITE_CLI:-$LICENCE_SITE}}
case "$SITE" in
*/) SITE=${SITE%/} ;;
esac
EXPIRY=${CHECK563_EXPIRY:-}
case "${MODE_CLI:-}" in
expiry) EXPIRY=1 ;;
approve) EXPIRY="" ;;
esac
PRODUCTION=${CHECK563_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK563_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK563_KEEP_SCRATCH:-}
WAIT_SECONDS=${CHECK563_WAIT_SECONDS:-${WAIT_CLI:-700}}
START_SECONDS=${CHECK563_START_SECONDS:-60}
CLI_TIMEOUT=${CHECK563_CLI_TIMEOUT_SECONDS:-180}

if [ -z "$SANDBOX" ]; then
  die "Check 563 needs --sandbox: the checkout of the installation being signed in. Usage: $USAGE"
fi
case "$WAIT_SECONDS" in
"" | *[!0-9]*) die "--wait '$WAIT_SECONDS' is not a number of seconds. usage: $USAGE" ;;
esac
case "$START_SECONDS" in
"" | *[!0-9]*) die "CHECK563_START_SECONDS '$START_SECONDS' is not a number of seconds" ;;
esac
case "$CLI_TIMEOUT" in
"" | *[!0-9]*) die "CHECK563_CLI_TIMEOUT_SECONDS '$CLI_TIMEOUT' is not a number of seconds" ;;
esac
# Absolute, before anything changes directory: the login and the reads below are
# run from the checkout, and a relative path would name another directory there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
