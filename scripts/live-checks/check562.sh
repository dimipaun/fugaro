#!/usr/bin/env bash
# Check 562 — browser sign-in against the staging site, including the wrong-
# `state` refusal. Plan task K7.4 of docs/licence-key-v2/implementation-plan.md,
# its "Checks this plan takes (560–569)".
#
# It settles two claims, read from the outside. K3.3: `fugaro licence login`
# opens the licence site, takes ONE callback on a listener bound to the literal
# 127.0.0.1, exchanges the code for a key and installs it. K2.4: the site's own
# authorize route (ee/keyservice/signin.go) answers an incomplete section-11.3
# request with its own refusal — and the CLI's listener (ee/licence/login/login.go)
# refuses a callback whose `state` is not the flow's own without ending the flow
# (RFC 8252 §7.3). The site the CLI talks to is a constant,
# `licenceServerURL = "https://keys.fugaro.dev"` in ee/register/licence.go, which
# nothing can repoint, so `--site` only holds the printed address against the
# origin you expect — it never moves the CLI.
#
# What it measures:
#   * the origin on its own, with ONE read that carries no sign-in: `GET
#     <site>/cli/authorize` with no query answers the site's own refusal (HTTP
#     403, "This sign-in could not be verified. ..." — signin.go's refuseSignIn),
#     so the host is the licence site and not a marketing page;
#   * the installation, from `fugaro doctor --json` in the sandbox, so the block
#     says what was signed in;
#   * the address `fugaro licence login` prints: `<site>/cli/authorize` over
#     https, carrying the flow's own `state` and a PKCE `code_challenge`, its
#     callback `http://127.0.0.1:<port>/callback` (the literal loopback, the port
#     the kernel gave). No code is read: the address is not a secret;
#   * THE WRONG-`state` REFUSAL: `GET .../callback?state=…&code=…` — values the
#     CHECK made up, so nothing a person's sign-in holds travelled — answers HTTP
#     400 with the page headed "This is not the sign-in's own callback", and the
#     login command is STILL RUNNING. A 200, or an exited login, is a FAIL;
#   * the sign-in: ONE `MANUAL:` line, a bounded wait for the login to end, then
#     `fugaro licence status --json` held to `source: project`, a non-empty
#     `tier`, `state` active|grace and a non-empty `renewal_mode`; no key is read.
#
# What it creates: the key the sign-in installs — the installation's own licence
# — and nothing else. Every other call is a read, plus the callback the CHECK
# makes up, which is refused.
#
# Usage:      bash scripts/live-checks/check562.sh --sandbox <a checkout> [--site <origin>] [--project <p>] [--production] [--dry-run]
#             bash scripts/live-checks/check562.sh --help
# Needs:      bash, mktemp, python3 (the address and the `--json` documents are
#             read with it), curl (the site and the loopback) and the installed
#             `fugaro` (the build you have, run from the sandbox checkout).
# Where:      your own machine and the STAGING site (`keys.fugaro.dev` with no
#             `--site`). A value naming belong is refused by name; `fugaro-prod`
#             is refused unless `--production`; a coding agent's session is
#             refused — no agent runs any step of it.
# Costs:      nothing billable: one refused read of the site and one loopback
#             callback. The sign-in is yours.
# Runs:       by YOU; its own test (check562_test.sh) drives it against fakes.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C; the login process, killed if the check ends
#             before it does. The key the sign-in installs is kept on purpose.
# Output:     progress on stderr; the `MANUAL:` line and ONE block on stdout
#             headed CHECK 562 RESULT, then CHECK 562 RESULT END (exit N), to
#             copy into the register's Check 562 section. Exit 0: every criterion
#             measured held. 1: a measured criterion did not hold (the wrong-state
#             callback was accepted, or the flow ended by it), or a guard stopped
#             the check. 2: a row could not be measured — the origin is not the
#             licence site, the installation has no Firebase project, or the
#             sign-in was not completed. An unreached key reads `FAIL — not
#             reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no licence key, no renewal secret and no
# authorize code, in whole or in part. The `state` and `code` the check makes up
# for the wrong-state callback are its own and non-secret, the CLI prints no code
# at all, and the block reads only `source`, `tier`, `state` and `renewal_mode` —
# `licence status --json` carries no key.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK562_SANDBOX, CHECK562_SITE (https://keys.fugaro.dev), CHECK562_PROJECT,
# CHECK562_PRODUCTION, CHECK562_WAIT (360), CHECK562_DRY_RUN,
# CHECK562_KEEP_SCRATCH, CHECK562_CURL_SECONDS (60), CHECK562_ADDRESS_WAIT (30),
# CHECK562_WRONG_SLEEP (1) and CHECK562_CLI_SECONDS (180).
set -Eeuo pipefail
# No xtrace: a trace of the run that holds an authorize address is a second copy of the flow's values.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true
# The owner's stdout and stderr on 3 and 4: a trap runs with whatever redirection
# is in effect when the signal arrives, so the block and MANUAL go to the saved fds.
exec 3>&1 4>&2
PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }
# stop REASON records a FAILed fact and ends the run; the EXIT trap prints the
# block and removes the scratch directory. After an `unmeasured` row it ends as
# one that could NOT measure (exit 2); otherwise 1, a finding about the release.
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}
FACTS_FILE=""; LAST_ROW=""
fact() { # fact KEY PASS|FAIL "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in FAIL) LAST_ROW=fail ;; *) LAST_ROW="" ;; esac
}
# unmeasured: a row the check could NOT measure — it reads FAIL and ends as exit 2.
UNMEASURED_KEYS=""
unmeasured() { fact "$1" FAIL "$2"; LAST_ROW=unmeasured; UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"; }
on_error() { trap - ERR; say "an internal error ended this check at line ${BASH_LINENO[0]:-?}"; exit 2; }
SCRATCH=""; SCRATCH_KEPT=no; CLEANUP_RUNNING=no; PRINTED=""; MANUAL=""
PROJECT=""; SITE=""; SITE_HOST=""; SANDBOX=""; PRODUCTION=""; DRY_RUN=""; KEEP_SCRATCH=""
FUGARO_BIN=""; ADDRESS=""; CALLBACK=""; BOUNDED_TIMED_OUT=no; BOUNDED_STATUS=0
LOGIN_PID=""; LOGIN_REAPED=yes; LOGIN_STATUS=0
scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check562.XXXXXX") || die "Check 562 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK562_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"; : >"$FACTS_FILE"
}
scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then SCRATCH_KEPT=yes; return 0; fi
  rm -rf "$SCRATCH"
}
# login_stop kills the sign-in if this check ends before it does.
login_stop() {
  [ -n "$LOGIN_PID" ] || return 0
  if kill -0 "$LOGIN_PID" 2>/dev/null; then
    kill -TERM "$LOGIN_PID" 2>/dev/null || true; sleep 1; kill -KILL "$LOGIN_PID" 2>/dev/null || true
  fi
  wait "$LOGIN_PID" 2>/dev/null || true
  LOGIN_REAPED=yes; LOGIN_PID=""
}
reap_login() { [ "$LOGIN_REAPED" = yes ] && return 0; LOGIN_STATUS=0; wait "$LOGIN_PID" 2>/dev/null || LOGIN_STATUS=$?; LOGIN_REAPED=yes; LOGIN_PID=""; }
# finish (the EXIT trap) kills the login, removes the scratch tree, appends the
# `scratch` row, settles the exit code, and prints the block once.
finish() {
  local status=$? facts="" key verdict
  CLEANUP_RUNNING=yes; trap - ERR; login_stop
  [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ] && facts=$(cat "$FACTS_FILE")
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK562_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    [ "$status" -eq 0 ] && status=2
  fi
  if [ "$status" -eq 0 ]; then
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      status=1
    done <<<"$facts"
  fi
  if [ -z "$PRINTED" ]; then PRINTED=1; print_block "$facts" "$status"; fi
  exit "$status"
}
# The block's shape does not depend on how far the run got: these keys print in
# this order whatever happened (an unreached one reads `FAIL — not reached`).
BLOCK_KEYS=(xtrace agent-session belong project site sandbox address wrong-state signin restore scratch)
print_block() { # print_block FACTS STATUS — always on the owner's own stdout (fd 3)
  local facts=$1 status=$2 key printed="" line
  printf '\nCHECK 562 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" "$(printf '%s' "$line" | cut -d'|' -f2)" "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict line; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$line" >&3
  done
  [ -n "$MANUAL" ] && printf 'MANUAL: %s\n' "$MANUAL" >&3
  printf 'CHECK 562 RESULT END (exit %s)\n' "$status" >&3
}
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then owner "cleanup is running, do not press Ctrl-C again"; return 0; fi
  owner "interrupted"; exit 130
}
# Each document is read with python3, never by pattern, so a field this check does
# not know cannot be mistaken for one it does; an unreadable one prints nothing.
doctor_fields() { # doctor_fields FILE — the project block of `doctor --json`
  python3 - "$1" <<'PY'
import json, sys
try: doc = json.load(open(sys.argv[1]))
except Exception: raise SystemExit(0)
p = doc.get("project") or {}
flat = lambda v: " ".join(str(v if v is not None else "").split())
for k in ("name", "gcp_project", "region"): print("%s=%s" % (k, flat(p.get(k))))
PY
}
status_fields() { # status_fields FILE — only the four fields this check reads
  python3 - "$1" <<'PY'
import json, sys
try: doc = json.load(open(sys.argv[1]))
except Exception: raise SystemExit(0)
flat = lambda v: " ".join(str(v if v is not None else "").split())
for k in ("source", "state", "tier", "renewal_mode"): print("%s=%s" % (k, flat(doc.get(k))))
PY
}
# url_fields URL — scheme, host, port, path and every query parameter prefixed `q_`.
url_fields() { # url_fields URL
  python3 - "$1" <<'PY'
import sys, urllib.parse
parts = None
try:
    parts = urllib.parse.urlsplit(sys.argv[1].strip()); port = parts.port or ""
except ValueError:
    port = ""
if parts is None:
    for k in ("scheme", "host", "port", "path"): print("%s=" % k)
else:
    print("scheme=%s" % parts.scheme); print("host=%s" % (parts.hostname or ""))
    print("port=%s" % port); print("path=%s" % parts.path)
    for n, v in urllib.parse.parse_qsl(parts.query, keep_blank_values=True): print("q_%s=%s" % (n, v))
PY
}
extract_address() { # extract_address FILE — the first authorize address, or ""
  python3 - "$1" <<'PY'
import re, sys
try: text = open(sys.argv[1], encoding="utf-8", errors="replace").read()
except OSError: raise SystemExit(0)
m = re.search(r'https?://[^\s]+/cli/authorize\?[^\s]+', text)
print(m.group(0) if m else "")
PY
}
field() { sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1; }
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check562.sh --sandbox <a checkout> — the check prints its own RESULT block, so no trace is needed to report a failure, and a trace of the run that holds an authorize address is a copy of every value the flow carries"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the address or the callback it holds"
}
# guard_agent_session: a sign-in is a person's own account in a browser, and an
# agent's session has neither, so no agent runs any step of it.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    [ -n "${!name:-}" ] && { marker=$name; break; }
  done
  if [ -n "$marker" ]; then
    stop "$marker is set, so this is a coding agent's session: Check 562 is yours to run at your own terminal, signed in as the account that holds the licence — no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}
# guard_inputs: the project, the site and the sandbox, read first; production is
# the one project a sign-in is only ever measured against on purpose.
guard_inputs() {
  local all="${PROJECT:-}${SITE:-}${SANDBOX:-}"
  case "$all" in *belong*) stop "a value this check was given names belong: this check is never run there" ;; esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: a sign-in against production is only ever measured on purpose. Nothing was read"
  fi
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 560 to 569 run on staging first, so read this block as a production run by choice"
  elif [ -n "$PROJECT" ]; then
    fact project PASS "$PROJECT is the Firebase project this sign-in is about (the staging deployment's own is fugaro-staging), and it is not fugaro-prod, which is refused without --production"
  else
    fact project PASS "no --project was given: the site's own origin ($SITE) is the deployment under test, and the installation's Firebase project id is the one the CLI reads from its own config"
  fi
}
# run_bounded SECONDS KEY DIR CMD...: one command under a bound, so a cloud call
# that has not answered never holds the terminal. Status into BOUNDED_STATUS.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3; shift 3
  BOUNDED_TIMED_OUT=no
  ( cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  local pid=$! ticks=0 max=$((secs * 10))
  while kill -0 "$pid" 2>/dev/null; do [ "$ticks" -lt "$max" ] || break; sleep 0.1; ticks=$((ticks + 1)); done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes; kill -TERM "$pid" 2>/dev/null || true; sleep 1; kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0; wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}
site_get() { curl -sS --max-time "$CURL_SECONDS" -o "$SCRATCH/$1.body" -w '%{http_code}' "$2" 2>"$SCRATCH/$1.err" || true; }
# step_site: ONE read with NO sign-in — the site's own refusal is what says the
# host is the licence site; anything else and every row below is off-target.
step_site() {
  local code; code=$(site_get site "$SITE/cli/authorize")
  if [ "$code" != 403 ]; then
    unmeasured site "GET $SITE/cli/authorize with no query answered ${code:-nothing} rather than 403: the origin this check was given does not answer as the licence site (an authorize request with no port or state answers the site's own refusal — HTTP 403, 'This sign-in could not be verified. ...'). Nothing below this row is about the deployment you meant; on staging, pass --site with the staging site's own origin"
    stop "the origin $SITE did not answer 403 to an authorize request with no query, so this check cannot say it is the licence site"
  fi
  if ! grep -q "This sign-in could not be verified" "$SCRATCH/site.body" 2>/dev/null; then
    unmeasured site "GET $SITE/cli/authorize answered 403 with a body that holds no 'This sign-in could not be verified': the origin answers, and not with the licence site's own refusal sentence"
    stop "the origin $SITE answered 403 with a body that is not the licence site's own refusal"
  fi
  fact site PASS "GET $SITE/cli/authorize with no query answers 403 with the licence site's own refusal, so the origin in front of this check is the licence site"
}
# step_sandbox: the installation this check signs in, read from the checkout
# itself with `fugaro doctor --json`, not from one this check guessed.
step_sandbox() {
  [ -d "$SANDBOX" ] || die "--sandbox is not a directory: $SANDBOX (pass the path of a throwaway/staging installation's checkout)"
  [ -f "$SANDBOX/fugaro.yaml" ] || die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the sign-in this check drives is one of that installation's"
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_SECONDS" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation the checkout belongs to, and the sign-in is one of that installation's. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    stop "fugaro doctor --json did not answer in the sandbox checkout"
  fi
  doctor_fields "$SCRATCH/doctor.out" >"$SCRATCH/doctor.txt"
  local name gcp region
  name=$(field "$SCRATCH/doctor.txt" name); gcp=$(field "$SCRATCH/doctor.txt" gcp_project); region=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$gcp" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to. What it printed is in $SCRATCH/doctor.out"
    stop "fugaro doctor --json answered without a project block"
  fi
  case "$gcp" in *belong*) stop "the checkout at $SANDBOX belongs to ${gcp}: this check is never run there" ;; esac
  fact sandbox PASS "${SANDBOX} is a Fugaro installation's checkout belonging to project ${name:-?} (GCP ${gcp}, region ${region:-?}): this is the installation the check signs in"
}
# step_address: start the sign-in and read the address it prints; the check reads
# no code and provides none.
step_address() {
  say "starting the sign-in: fugaro licence login, in ${SANDBOX}"
  ( cd "$SANDBOX" || exit 1; exec "$FUGARO_BIN" licence login ) >"$SCRATCH/login.out" 2>"$SCRATCH/login.err" &
  LOGIN_PID=$!; LOGIN_REAPED=no
  local line="" ticks=0 max=$((ADDRESS_WAIT * 2))
  while [ "$ticks" -lt "$max" ]; do
    line=$(extract_address "$SCRATCH/login.out"); [ -n "$line" ] && break
    kill -0 "$LOGIN_PID" 2>/dev/null || break
    sleep 0.5; ticks=$((ticks + 1))
  done
  if [ -z "$line" ]; then
    if ! kill -0 "$LOGIN_PID" 2>/dev/null; then
      reap_login
      local said; said=$(cat "$SCRATCH/login.out" "$SCRATCH/login.err" 2>/dev/null || true)
      case "$said" in
      *"fugaro update --firebase-project"*)
        unmeasured address "fugaro licence login refused before it printed an authorize address, with the no-workspace sentence: this installation has no Firebase project (budget.firebase_project is not set in its project config), and a licence key is bound to that project's id, so there is nothing to sign in with. An operator runs \`fugaro update --firebase-project <the project> --name <the installation>\` first, then run this check again"
        stop "the installation has no Firebase project: run \`fugaro update --firebase-project <the project>\`"
        ;;
      *)
        unmeasured address "fugaro licence login ended (exit ${LOGIN_STATUS}) before it printed an authorize address: the sign-in did not start. What it printed is in $SCRATCH/login.out and $SCRATCH/login.err"
        stop "fugaro licence login ended before it printed an authorize address"
        ;;
      esac
    fi
    unmeasured address "fugaro licence login printed no authorize address in ${ADDRESS_WAIT}s: the check reads that address off the line the CLI prints, and without it there is nothing to sign in at or to refuse. What it printed is in $SCRATCH/login.out"
    stop "the sign-in printed no authorize address"
  fi
  ADDRESS=$line; url_fields "$ADDRESS" >"$SCRATCH/addr.txt"
  local scheme host path q_state q_challenge q_port
  scheme=$(field "$SCRATCH/addr.txt" scheme); host=$(field "$SCRATCH/addr.txt" host); path=$(field "$SCRATCH/addr.txt" path)
  q_state=$(field "$SCRATCH/addr.txt" q_state); q_challenge=$(field "$SCRATCH/addr.txt" q_code_challenge); q_port=$(field "$SCRATCH/addr.txt" q_port)
  if [ "$scheme" != https ] || [ "$host" != "$SITE_HOST" ] || [ "$path" != /cli/authorize ]; then
    unmeasured address "the address fugaro licence login printed is not <site>/cli/authorize (scheme '${scheme}', host '${host}', path '${path}'; --site's own host is '${SITE_HOST}'): the CLI talks to its own constant (licenceServerURL in ee/register/licence.go), and --site here only holds that address against the origin you expect"
    stop "the printed address is not <site>/cli/authorize over https"
  fi
  if [ -z "$q_state" ] || [ -z "$q_challenge" ]; then
    unmeasured address "the printed address carries no state or no code_challenge (state '${q_state:+set}', code_challenge '${q_challenge:+set}'): K3.3's flow hands the state the callback is checked against and a PKCE S256 challenge, and one without them is not this flow"
    stop "the printed address carries no state or no code_challenge"
  fi
  case "$q_port" in
  '' | *[!0-9]*)
    unmeasured address "the printed address carries no usable loopback port ('${q_port}'): K3.3's listener binds the literal 127.0.0.1 on a port the kernel gives. What it printed is in $SCRATCH/login.out"
    stop "the printed address carries no loopback port"
    ;;
  esac
  if [ "$q_port" -lt 1 ] || [ "$q_port" -gt 65535 ]; then
    unmeasured address "the printed address carries a port outside 1..65535 ('${q_port}'): the callback must be a real loopback address, not a value this check cannot dial"
    stop "the printed address carries an unusable loopback port"
  fi
  CALLBACK="http://127.0.0.1:${q_port}/callback"
  fact address PASS "the printed address is <site>/cli/authorize over https carrying the flow's own state and a PKCE code_challenge, and its callback is ${CALLBACK} (the literal loopback, the port the kernel gave). The address is not a secret, and no byte of a code was read"
}
# step_wrong_state: the callback carries a state and a code the CHECK made up, so
# nothing a person's sign-in holds travels; it must answer 400 with the refusal
# page, and the flow must still be running (RFC 8252 §7.3).
step_wrong_state() {
  local code; code=$(site_get wrongstate "${CALLBACK}?state=chk562-not-the-flow-state&code=chk562-not-a-code")
  sleep "$WRONG_SLEEP"
  if [ "$code" != 400 ]; then
    fact wrong-state FAIL "GET ${CALLBACK} with a state the check made up answered ${code:-nothing} rather than 400: a callback whose state is not the flow's own must be refused (HTTP 400) — it is what keeps a local process that guessed the port from spending the person's sign-in (RFC 8252 §7.3). The values are the check's own, so nothing a person's sign-in holds travelled"
    stop "the wrong-state callback did not answer 400"
  fi
  if ! grep -q "This is not the sign-in's own callback" "$SCRATCH/wrongstate.body" 2>/dev/null; then
    fact wrong-state FAIL "GET ${CALLBACK} with a state the check made up answered 400 with a body that is not the sign-in's own refusal page (the page headed \"This is not the sign-in's own callback\"): the status is the rule's and the page is not"
    stop "the wrong-state callback answered 400 with an unrecognised page"
  fi
  if ! kill -0 "$LOGIN_PID" 2>/dev/null; then
    fact wrong-state FAIL "the login command had ended after the wrong-state callback was sent to ${CALLBACK}: a callback whose state is not the flow's own must NOT end the flow — a local process that guessed the port must not be able to spend the person's sign-in by hitting it first"
    stop "the wrong-state callback ended the flow"
  fi
  fact wrong-state PASS "GET ${CALLBACK} with a state and a code the check made up answers 400 with the page headed \"This is not the sign-in's own callback\", and the login command is still running afterwards: the flow was not ended by a local process guessing the port (RFC 8252 §7.3)"
}
# step_signin: the human half — the command must end 0 and the installation must
# then read the signed-in licence. The key is never read.
step_signin() {
  MANUAL="open ${ADDRESS} in a browser (the login command already opened it), sign in at the licence site, and pick the licence this installation should run on; the terminal finishes by itself once the sign-in comes back. Nothing is typed at this terminal, and the check never sees your key"
  say "waiting up to ${WAIT}s for the sign-in to come back"
  local ticks=0 max=$((WAIT * 2))
  while kill -0 "$LOGIN_PID" 2>/dev/null; do [ "$ticks" -lt "$max" ] || break; sleep 0.5; ticks=$((ticks + 1)); done
  if kill -0 "$LOGIN_PID" 2>/dev/null; then
    login_stop
    unmeasured signin "the browser sign-in was not finished within ${WAIT}s: K3.3's listener holds five minutes (defaultDeadline) and this check waited ${WAIT}s, that window plus a margin. Sign in again as the account that holds the licence — the key is bound to the account whose verified address bought it — or raise CHECK562_WAIT. Nothing was written"
    stop "the sign-in was not finished in ${WAIT}s"
  fi
  reap_login
  if [ "$LOGIN_STATUS" != 0 ]; then
    unmeasured signin "fugaro licence login ended with exit ${LOGIN_STATUS} rather than 0: a refusal or a sign-in that expired leaves nothing written, so the installation holds whatever it held before. Sign in again as the account that holds the licence — the key is bound to the account whose verified address bought it. What it printed is in $SCRATCH/login.out and $SCRATCH/login.err"
    stop "the sign-in ended with a refusal or expired"
  fi
  if grep -q 'fgk_' "$SCRATCH/login.out" "$SCRATCH/login.err" 2>/dev/null; then
    fact signin FAIL "fugaro licence login's own output carries a key-shaped string ('fgk_'): no line of the command's output may carry the key (K3.3, F5). The value is not printed here"
    stop "fugaro licence login's output carries a key-shaped string"
  fi
  say "reading what the installation now holds: fugaro licence status --json, in ${SANDBOX}"
  run_bounded "$CLI_SECONDS" status "$SANDBOX" "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured signin "fugaro licence status --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the sign-in finished, and this check cannot read what the installation now holds. Its output is in $SCRATCH/status.out and $SCRATCH/status.err"
    stop "fugaro licence status --json did not answer after the sign-in"
  fi
  status_fields "$SCRATCH/status.out" >"$SCRATCH/status.txt"
  local source state tier renewal state_ok=no
  source=$(field "$SCRATCH/status.txt" source); state=$(field "$SCRATCH/status.txt" state)
  tier=$(field "$SCRATCH/status.txt" tier); renewal=$(field "$SCRATCH/status.txt" renewal_mode)
  case "$state" in active | grace) state_ok=yes ;; esac
  if [ "$source" != project ] || [ -z "$tier" ] || [ -z "$renewal" ] || [ "$state_ok" != yes ]; then
    fact signin FAIL "the sign-in ended 0, and fugaro licence status --json in ${SANDBOX} reads source='${source:-none}', tier='${tier:-none}', state='${state:-none}', renewal_mode='${renewal:-none}' rather than source 'project', a non-empty tier, a state of active or grace, and a non-empty renewal_mode: the key the sign-in installs belongs in the runs bucket (source 'project'), and another source or Free would mean the sign-in did not install it. The status document is in $SCRATCH/status.out"
    stop "licence status --json does not read the signed-in licence"
  fi
  fact signin PASS "the sign-in finished (exit 0) and fugaro licence status --json in ${SANDBOX} reads source 'project', tier '${tier}', state '${state}' and renewal_mode '${renewal}': the key the sign-in installed is the installation's own licence. No key was read — licence status --json carries none, and the check reads only these fields"
}
step_restore() { fact restore PASS "nothing of this check's own is left behind: the key the sign-in installed is kept on purpose — it is this installation's own licence and the object of the whole check — and every other call was a read or the callbacks the check made up, which were refused"; }
plan() {
  say "CHECK 562 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read-only] GET ${SITE}/cli/authorize with no query — 403 with the licence site's own refusal"
  say "  [network, read-only] fugaro doctor --json in ${SANDBOX} — the installation this check signs in"
  say "  [local] fugaro licence login from ${SANDBOX} — prints <site>/cli/authorize?... and holds a listener on 127.0.0.1 for one callback"
  say "  [local] GET http://127.0.0.1:<port>/callback?state=<made up>&code=<made up> — must answer 400 and leave the flow running"
  say "  [browser, yours] visit the printed address, sign in and pick the licence — printed as one MANUAL line"
  say "  [local, reads] fugaro licence status --json — source 'project', a non-empty tier, active or grace, a non-empty renewal_mode; the key itself is never read"
  fact dry-run PASS "the plan was printed and nothing was run: no site read, no login started and no status read"
}
need_tools() {
  local t
  for t in bash mktemp python3 curl; do command -v "$t" >/dev/null 2>&1 || die "Check 562 needs '$t' and it is not on PATH"; done
  FUGARO_BIN=$(command -v fugaro || true)
  [ -n "$FUGARO_BIN" ] || die "Check 562 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
}
main() {
  need_tools; scratch_up; trap on_error ERR
  guard_xtrace; guard_agent_session; guard_inputs
  if [ -n "$DRY_RUN" ]; then plan; return 0; fi
  step_site; step_sandbox; step_address; step_wrong_state; step_signin; step_restore
}
USAGE="check562.sh --sandbox <a checkout> [--site <origin>] [--project <firebase project>] [--production] [--dry-run]"
usage() { sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed -e '$d' -e 's/^# \{0,1\}//' >&3; }
DRY_RUN_CLI=""; SANDBOX_CLI=""; SITE_CLI=""; PROJECT_CLI=""; WAIT_CLI=""
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a throwaway/staging installation whose licence is being signed in. usage: $USAGE"
    SANDBOX_CLI=$2; shift 2 ;;
  --site)
    [ $# -ge 2 ] || die "--site needs an origin (the licence site the CLI talks to). usage: $USAGE"
    SITE_CLI=$2; shift 2 ;;
  --project)
    [ $# -ge 2 ] || die "--project needs a Firebase project id. usage: $USAGE"
    PROJECT_CLI=$2; shift 2 ;;
  --wait)
    [ $# -ge 2 ] || die "--wait needs a number of seconds (the bound on the sign-in; default 360). usage: $USAGE"
    WAIT_CLI=$2; shift 2 ;;
  --production) PRODUCTION=1; shift ;;
  --dry-run) DRY_RUN_CLI=1; shift ;;
  -h | --help) usage; exit 0 ;;
  *) die "unknown argument '$1'. usage: $USAGE" ;;
  esac
done
PRODUCTION=${CHECK562_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK562_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK562_KEEP_SCRATCH:-}
PROJECT=${CHECK562_PROJECT:-${PROJECT_CLI:-}}
SANDBOX=${CHECK562_SANDBOX:-${SANDBOX_CLI:-$PWD}}
SITE=${CHECK562_SITE:-${SITE_CLI:-https://keys.fugaro.dev}}; SITE=${SITE%/}
WAIT=${CHECK562_WAIT:-${WAIT_CLI:-360}}
CURL_SECONDS=${CHECK562_CURL_SECONDS:-60}
ADDRESS_WAIT=${CHECK562_ADDRESS_WAIT:-30}
WRONG_SLEEP=${CHECK562_WRONG_SLEEP:-1}
CLI_SECONDS=${CHECK562_CLI_SECONDS:-180}
case "$WAIT" in '' | *[!0-9]*) die "--wait (or CHECK562_WAIT) must be a whole number of seconds, not '$WAIT'. usage: $USAGE" ;; esac
# Absolute before any cd: the steps run from the sandbox checkout.
case "$SANDBOX" in /*) ;; *) SANDBOX="$PWD/$SANDBOX" ;; esac
SITE_HOST=$(python3 -c 'import sys,urllib.parse;print(urllib.parse.urlsplit(sys.argv[1]).hostname or "")' "$SITE" 2>/dev/null || true)
[ -n "$SITE_HOST" ] || die "--site '$SITE' is not an http(s) origin this check can read a host from. usage: $USAGE"
trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
