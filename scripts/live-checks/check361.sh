#!/usr/bin/env bash
# Check 361 — the workspace's own Firebase's sign-in providers (W3's, and
# spec §3's provider pin), as ONE script YOU run.
#
# A workspace has a Firebase project of its own (docs/workspaces/spec.md §3,
# D1290), and it is the one Firebase in this design where a person signs in with
# a real provider: the workspace's signer endpoint — and the member, which
# re-checks the claim — accepts only the **pinned set** of
# `firebase.sign_in_provider` values, `google.com` and `password`, and refuses
# `custom` and `anonymous` (a custom token is mintable by anyone holding
# `signBlob` on any service account in the project, and an anonymous sign-in
# proves no address at all). The project and its rules come from the installation
# path — the owner's `fugaro setup` for the workspace root, then `fugaro update`
# — but `fugaro setup`'s Identity Platform step initializes with **no provider**
# (`IdentityPlatformStep`, internal/infra/identityplatform.go), which is right for
# a member's project and wrong for the workspace's. `refusePublicSignUp`
# (internal/cli/init_firebase.go) holds a workspace's own project to the pin in
# one direction: the pinned entries pass, and an off-pin path is refused by name.
# It never *requires* them, so a workspace whose providers are not enabled yet
# converges as usual — which is what this check reads back: a project initialized
# with no provider has no one who can sign in, and a project that drifted one
# provider further is a provider the exchange never sees but the Firebase does.
# (`fugaro update` refuses that drift on its next run, naming the paths.)
#
# What it reads, once, over REST: `GET /admin/v2/projects/<project>/config` on
# identitytoolkit.googleapis.com — the same document `infra.PublicSignUp` parses
# (internal/infra/identityplatform.go) — with YOUR own credentials. From it: the
# password provider (`signIn.email.enabled`), every ENABLED entry of
# `defaultSupportedIdpConfigs` and `oauthIdpConfigs` (`google.com` and anything
# else), `signIn.anonymous.enabled`, `signIn.phoneNumber.enabled`, and
# `client.permissions.disabledUserSignup`.
#
# The pin holds when **password and `google.com` are the only two enabled**, no
# other provider is enabled, anonymous and phoneNumber are off, and public
# sign-up is **not** disabled — the workspace's Firebase is the one that does not
# refuse public sign-up (spec §3), which is what lets a person who does not
# already hold a password create one and reach the workspace at all.
#
# `custom` is the half of the pin no configuration carries: it is not a toggle in
# this document, and what refuses it is the signer endpoint's own pin on the
# `firebase.sign_in_provider` claim (spec §3, W4). The block records that as an
# INFO row, and never as a criterion this read settled.
#
# It creates nothing, deletes nothing and writes nothing outside its own scratch
# directory: ONE GET, read-only, and it is safe to run repeatedly.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 361".
#
# Usage:      bash scripts/live-checks/check361.sh --project <the workspace's own project> [--dry-run]
#             --project is the workspace's OWN Firebase project — the project the
#             workspace is named after, and the one `fugaro setup --name
#             <workspace>` created. It is required: this check never reads a
#             project from a config that may name another, and the project id is
#             what the read is addressed to.
#             `--dry-run` prints the one command a real run would make and runs
#             nothing at all.
# Needs:      bash, mktemp, sed, python3, gcloud and curl on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      anywhere, and it prefers nothing: the check is READ-ONLY in the
#             project it names, so it is not restricted to a sandbox. The name
#             `belong` is still refused before any call, because a check that is
#             read-only in this project is still a check nobody should point at
#             the owner's own installation by accident.
#             You need an account that may read the project's Identity Platform
#             configuration (Firebase Admin, or the project's owner); a refused
#             read stops the run rather than reading as drift.
# Costs:      nothing billable and no model call: one GET.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 361 RESULT.
#             Exit 0 when the pin holds, 1 when the project has drifted from it
#             (a provider the pin admits is off or absent, one it refuses is on,
#             public sign-up is disabled, or Identity Platform is not initialized
#             at all), and 2 when the run could not tell (a guard stopped it, no
#             token could be minted, the read did not answer, the document could
#             not be parsed, a tool is missing, an internal error ended it).
#
# No credential is printed, logged or put on a command line: the access token is
# minted into a 0600 config file inside a 0700 scratch directory and curl reads
# it with `--config` (the pattern Check 91 uses, and for the same reason — `ps`
# shows a process's argv to every user on the machine), and that file is removed
# whether the run passes, fails or is interrupted.
#
# CHECK361_CURL_SECONDS exists for this script's own test: it bounds the one
# request, and it cannot point the check at another project.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had. This check mints an
# access token and writes it into a file with printf, and a trace prints that
# line in full — the token, into the log the owner would then paste back. So the
# trace is turned off here, BASH_XTRACEFD is unset so a trace redirected to a file
# this script knows nothing about cannot be reopened, and guard_xtrace refuses a
# shell that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then, and every
# child would start tracing on its own). Check 145 spells the three parts out;
# this is the same guard.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once the reading has been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and it is read by the exit
# trap, which has to see both of these whatever path ended the run.
HELPED=no
DRY_RUN=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something to
# paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the providers for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the workspace's providers"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 361 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 361 needs bash"
  need mktemp
  need sed "the block's rows are read back out of this run's own fact file with it"
  need python3 "the Identity Platform document is JSON, and the reader that parses it is one python3 file"
  need gcloud "the access token this read carries is minted with it"
  need curl "the configuration is read over REST with it"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk361-mine"
SCRATCH_REMOVED=no
AUTH_CONF=""
AUTH_CONF_REMOVED=yes
PRINTED=""
FACTS_CAPTURED=""
# What the run read, and what the reader made of it. The two flags say which of
# the two endings the judging step reached: an uninitialized project is a
# DEFINITE answer (the pin does not hold — nobody can sign in), while a document
# nobody could read is a could-not-tell.
CONFIG_READ=no
UNINITIALIZED=no
READER_ERROR=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk361.XXXXXXXX") || die "Check 361 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  AUTH_CONF="$SCRATCH/auth.conf"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 361 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_auth_conf
  remove_scratch
}

# The credential file goes first, and its outcome is recorded: a run that stopped
# between the mint and the read would otherwise leave a live access token on disk
# in a directory the trap is about to remove. Guarded, because this runs from the
# exit trap, where a failing `rm` under `set -e` would end the shell before the
# block is printed.
remove_auth_conf() {
  [ -n "$AUTH_CONF" ] || return 0
  rm -f "$AUTH_CONF" 2>/dev/null || true
  if [ ! -e "$AUTH_CONF" ]; then AUTH_CONF_REMOVED=yes; else AUTH_CONF_REMOVED=no; fi
  return 0
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing would print the token this check
# mints, so the run is refused rather than read: the trace is already off (the
# top of this file), and what is left to catch is the shell that started with it,
# whose flag sits in SHELLOPTS/BASHOPTS and would be inherited by every child.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the access token this check mints — and the log you would then paste. Run it without -x"
    stop "the shell this check was started from is tracing (-x): the trace would print the token this check mints. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the access token this check mints"
}

# guard_belong: the installation named `belong` is the owner's own. This check is
# read-only in the project it names, and the name is still refused before any
# call: a check pointed at the owner's own installation by a mistyped id would be
# a reading nobody asked for, and the refusal is what this check's own test pins.
# It is a substring test on purpose: a project that merely contains the name is
# refused too, and the message says which one matched.
guard_belong() {
  case "$(printf '%s' "$P" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact belong FAIL "'$P' names the owner's own installation: Check 361 reads a workspace's own Firebase, and it is never pointed at 'belong'"
    stop "the project '$P' matches the installation 'belong', which this check must never touch"
    ;;
  esac
  fact belong PASS "the project ($P) does not name the owner's own installation (nothing typed contains 'belong')"
}

# guard_project: the id has to BE a project id. It is also what keeps the value
# out of the curl config file's own syntax — a quote or a newline in it would be
# read as another directive rather than as part of a URL — and a mistyped id is a
# stop rather than a read of some other project. The shape is GCP's own rule
# (6-30 characters, lowercase letters, digits and hyphens, starting with a letter
# and not ending in a hyphen).
guard_project() {
  case "$P" in
  [a-z]*) ;;
  *) die "--project '$P' is not a GCP project id: it starts with a lowercase letter. usage: $USAGE" ;;
  esac
  if ! printf '%s' "$P" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$'; then
    die "--project '$P' is not a GCP project id: 6 to 30 characters of lowercase letters, digits and hyphens, not ending in a hyphen. usage: $USAGE"
  fi
}

# --------------------------------------------------------------- the one read
# mint_token writes the read's Authorization header into AUTH_CONF. The token is
# never printed, never exported and never on a command line: curl is given the
# file with --config, and the file is 0600 inside a 0700 scratch directory that
# the exit trap removes. Non-zero when no token can be minted here.
mint_token() {
  local tok
  tok=$(gcloud auth print-access-token --quiet) || return 1
  [ -n "$tok" ] || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
  return 0
}

CONFIG_URL=""
read_config() {
  CONFIG_URL="https://identitytoolkit.googleapis.com/admin/v2/projects/${P}/config"
  say "reading $CONFIG_URL with your own credentials (one GET, nothing is written anywhere)"
  if ! mint_token; then
    fact token FAIL "no access token could be minted with gcloud (an expired session, or no credential at all), so the configuration was never read: run 'gcloud auth login' and this check again"
    stop "gcloud auth print-access-token did not answer, so this run read nothing (its own line is above). Run 'gcloud auth login' and this check again"
  fi
  fact token PASS "an access token was minted for this one read, written to a 0600 file inside this run's 0700 scratch directory and never printed or put on a command line"
  local code=""
  # -w on stdout and the body to the file: the two answers are needed separately,
  # because a 400 or a 404 is CONFIGURATION_NOT_FOUND (a definite reading: no
  # Identity Platform) only when the body says so, and every other failure is a
  # could-not-tell rather than a verdict about the providers. `x-goog-user-project`
  # is the header internal/infra's own client sets, so the call is charged to the
  # project this check is about.
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -o "$SCRATCH/config.json" -w '%{http_code}' \
    -H "x-goog-user-project: $P" --config "$AUTH_CONF" "$CONFIG_URL" 2>"$SCRATCH/curl.err"); then
    fact config FAIL "the read did not answer: curl failed before any HTTP response (no network, a proxy, DNS). Check 361 read nothing, so nothing is concluded about $P's providers ($(first_line "$SCRATCH/curl.err"))"
    stop "the read of $P's Identity Platform configuration did not answer (curl's own line is above and in the block). Nothing was concluded: check the network and run again"
  fi
  case "$code" in
  200)
    CONFIG_READ=yes
    fact config PASS "the Identity Platform configuration of $P was read over REST ($CONFIG_URL answered HTTP 200)"
    ;;
  400 | 404)
    if grep -q 'CONFIGURATION_NOT_FOUND' "$SCRATCH/config.json" 2>/dev/null; then
      UNINITIALIZED=yes
      fact config FAIL "Identity Platform is NOT initialized in $P (the read answered HTTP $code with CONFIGURATION_NOT_FOUND): the workspace's Firebase has no sign-in provider at all, so nobody can sign in to it. \`fugaro setup\` for the workspace root initializes it (its Identity Platform step runs with no provider on purpose), and the providers are then enabled by you: initialize it and enable google.com and Email/Password, then run this check again"
    else
      fact config FAIL "the read of $P's Identity Platform configuration answered HTTP $code and the body does not say CONFIGURATION_NOT_FOUND, so this is not the reading 'there is no Identity Platform here' ($(first_line "$SCRATCH/config.json"))"
      stop "the read answered HTTP $code without CONFIGURATION_NOT_FOUND in the body (the line above), which is a could-not-tell and not a verdict: read the project's Identity Platform configuration by hand and run again"
    fi
    ;;
  403)
    fact config FAIL "the read was refused (HTTP 403): the account you are signed in as cannot read $P's Identity Platform configuration, so this run measured nothing. Reading it needs Firebase Admin on the project (or its owner) — 'gcloud auth login' as such an account, or grant it, and run again ($(first_line "$SCRATCH/config.json"))"
    stop "the read of $P's Identity Platform configuration was refused with HTTP 403 (the line above): this run measured nothing. Sign in as an account that may read it and run again"
    ;;
  *)
    fact config FAIL "the read answered HTTP $code, which is neither the configuration nor a refusal this check can read as one ($(first_line "$SCRATCH/config.json"))"
    stop "the read of $P's Identity Platform configuration answered HTTP $code (the line above). Nothing is concluded: read it by hand and run again"
    ;;
  esac
}

# --------------------------------------------------------------- the judgement
# The reader is one python3 file (there is no jq here, and the document is JSON):
# it prints one `key=value` line per question, and the shell below judges them, so
# the parsing and the verdict stay in one place each. A document it cannot parse
# is a stop, never a set of zeroes.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Reads an Identity Platform configuration and prints what the pin asks.

One `key=value` line per question: the password provider, the enabled google.com
provider, the two paths the pin refuses (anonymous, phoneNumber), every OTHER
provider that is enabled, and whether public sign-up is disabled. Nothing here
decides anything: the shell judges the lines.
"""
import json
import sys


def enabled(node):
    return bool(node) and bool(node.get("enabled"))


def idp_name(cfg, field):
    """The provider a config entry names, as its last path segment.

    defaultSupportedIdpConfigs entries are named .../defaultSupportedIdpConfigs/
    google.com; an oauthIdpConfigs entry's name is the provider id the console
    gave it. An entry that carries neither a name nor a providerId is reported by
    the field it came from, so it is still visible in the block rather than
    silently dropped.
    """
    name = str(cfg.get("name") or cfg.get("providerId") or "").rstrip("/")
    last = name.rsplit("/", 1)[-1]
    return last or field


with open(sys.argv[1]) as fh:
    doc = json.load(fh)
signin = doc.get("signIn") or {}
google = 0
others = []
for field in ("defaultSupportedIdpConfigs", "oauthIdpConfigs"):
    for cfg in doc.get(field) or []:
        if not isinstance(cfg, dict) or not enabled(cfg):
            continue
        name = idp_name(cfg, field)
        if name == "google.com":
            google = 1
        else:
            others.append("%s (%s)" % (name, field))
print("password=%d" % (1 if enabled(signin.get("email")) else 0))
print("google=%d" % google)
print("anonymous=%d" % (1 if enabled(signin.get("anonymous")) else 0))
print("phone=%d" % (1 if enabled(signin.get("phoneNumber")) else 0))
client = doc.get("client") or {}
perms = client.get("permissions") or {}
print("signup_disabled=%d" % (1 if perms.get("disabledUserSignup") else 0))
print("other=" + ", ".join(sorted(others)))
READER_EOF
}

READ_OUT=""
read_and_judge() {
  if ! READ_OUT=$(python3 "$SCRATCH/reader.py" "$SCRATCH/config.json" 2>"$SCRATCH/reader.err"); then
    READER_ERROR=$(first_line "$SCRATCH/reader.err")
    fact providers FAIL "the configuration of $P could not be parsed as an Identity Platform document, so no provider was judged: this is a could-not-tell and not a reading about the pin ($READER_ERROR)"
    stop "the Identity Platform document of $P could not be parsed (the line above). Its shape may have changed; nothing is concluded about the providers"
  fi
  local password google anonymous phone signup other
  password=$(field password)
  google=$(field google)
  anonymous=$(field anonymous)
  phone=$(field phone)
  signup=$(field signup_disabled)
  other=$(field other)

  if [ "$password" = 1 ]; then
    fact password PASS "the password provider is enabled ($P's signIn.email.enabled is true), and the pin admits it"
  else
    fact password FAIL "the password provider is OFF ($P's signIn.email.enabled is false), and the pin admits it: a person signing in with the workspace's Firebase with an email address and a password cannot. Enable Email/Password under Authentication, Sign-in method in the workspace's own project, then run this check again"
  fi
  if [ "$google" = 1 ]; then
    fact google PASS "the google.com provider is enabled ($P carries an enabled defaultSupportedIdpConfigs entry named google.com), and the pin admits it"
  else
    fact google FAIL "the google.com provider is NOT enabled in $P (no enabled entry named google.com), and the pin admits it: a person signing in with their Google account reaches nothing. Enable the Google provider in the workspace's own project, then run this check again"
  fi
  if [ "$anonymous" = 0 ]; then
    fact anonymous PASS "anonymous sign-in is off ($P's signIn.anonymous.enabled is false), which is the half of the pin that refuses 'anonymous'"
  else
    fact anonymous FAIL "anonymous sign-in is ON in $P (signIn.anonymous.enabled is true) and the pin refuses it: an anonymous sign-in proves no address at all, so a caller admitted by one would be a caller nobody proved anything about (spec §3). Disable it, then run this check again"
  fi
  if [ "$phone" = 0 ]; then
    fact phone PASS "phone-number sign-in is off ($P's signIn.phoneNumber.enabled is false): the pinned set is google.com and password, and nothing else"
  else
    fact phone FAIL "phone-number sign-in is ON in $P (signIn.phoneNumber.enabled is true) and the pin does not admit it (the set is google.com and password). Disable it, then run this check again"
  fi
  if [ -z "$other" ]; then
    fact other-providers PASS "no provider other than google.com is enabled in $P (neither defaultSupportedIdpConfigs nor oauthIdpConfigs carries another enabled entry)"
  else
    fact other-providers FAIL "these providers are ENABLED in $P and the pin admits neither them: $other. A provider the workspace's Firebase does not carry is a provider the exchange never sees, and one it does carry is a sign-in path the pin's own reasoning never covered — disable them, then run this check again"
  fi
  if [ "$signup" = 0 ]; then
    fact public-signup PASS "public sign-up is not disabled in $P (client.permissions.disabledUserSignup is false): the workspace's Firebase is the one that does not refuse public sign-up (spec §3), which is what lets a person who does not already hold a password create one"
  else
    fact public-signup FAIL "public sign-up is DISABLED in $P (client.permissions.disabledUserSignup is true): the workspace's Firebase is the one that does not refuse public sign-up (spec §3), and with this on, the password half of the pin admits nobody who does not already have an account. Allow public sign-up (users can create accounts) in the workspace's own project, then run this check again"
  fi
  # Not a criterion, and it says so: nothing in this document turns custom sign-in
  # off, and what refuses it is the pin on the claim at the workspace's signer
  # endpoint (spec §3, W4).
  fact custom INFO "custom sign-in is not a toggle in this document: the pin refuses it at the workspace's signer endpoint, which admits a Firebase ID token only when its firebase.sign_in_provider claim is one of the pinned values. Nothing here read it, and this row is not a criterion"

  # One verdict out of the rows above, so the block and the exit status cannot
  # disagree: every FAIL row is named here, and the exit status is the verdict's.
  # The list is read back out of the run's own fact file rather than remembered,
  # so the pin's own sentence is a reading of the rows the block prints and the
  # two cannot drift apart.
  local drifted=() key list
  for key in password google anonymous phone other-providers public-signup; do
    if [ "$(row_verdict "$key")" = FAIL ]; then drifted+=("$key"); fi
  done
  if [ ${#drifted[@]} -eq 0 ]; then
    fact pin PASS "$P carries exactly the pinned provider set: google.com and password enabled, anonymous and phone-number sign-in off, no other provider enabled, and public sign-up not disabled (spec §3). This is the set the workspace's signer endpoint and every member accept a firebase.sign_in_provider claim against"
    step_verdict 0
  fi
  list=$(printf '%s, ' "${drifted[@]}"); list=${list%, }
  fact pin FAIL "$P does not carry the pinned provider set (spec §3): $list. Each row above names what to change and where; the pin is what turns 'the workspace's Firebase said so' into a statement a member can hold, so a project that drifted from it is one the workspace's sign-in cannot be trusted from"
  step_verdict 1
}

# row_verdict KEY — the verdict this run recorded for KEY, empty when it recorded
# none.
row_verdict() {
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

# field KEY — the reader's own line for KEY. The reader's output is fixed and
# small, and a key it stopped printing would leave this empty: an empty value is
# then judged as "not enabled" (the safe direction for every value but password
# and google, and those two FAIL on it, which is the reading a reader that stopped
# answering should produce).
field() { printf '%s\n' "$READ_OUT" | sed -n "s/^$1=//p" | head -1; }

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# ------------------------------------------------------------------- the verdict
# step_verdict STATUS ends a judged run. REACHED_END is what keeps the exit trap
# from reading this as a run that died on an error, and it is set before the exit
# so the trap's own accounting is the same on both endings.
step_verdict() {
  REACHED_END=yes
  case "$1" in
  0) owner "the pin holds: $P carries google.com and password, and nothing the pin refuses" ;;
  *) owner "the pin does NOT hold: the block's FAILed rows name what $P carries instead, and each one says where to change it" ;;
  esac
  exit "$1"
}

# print_plan: the one command a real run would make, printed by --dry-run INSTEAD
# of running anything. This check's own test pins that a dry run makes no gcloud
# and no curl call at all, so the plan names the call's shape and never makes it.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, reads, credentials] gcloud auth print-access-token --quiet — the token for the one read below, written into a 0600 file this run's 0700 scratch directory holds and curl reads with --config; it is never printed and never on a command line"
  say "  [network, reads] curl -sS -o <the run's scratch>/config.json -w '%{http_code}' -H 'x-goog-user-project: $P' --config <that file> https://identitytoolkit.googleapis.com/admin/v2/projects/$P/config"
  say "  [local, reads] the same document parsed by a python3 reader that prints the password provider, the enabled google.com provider, anonymous and phone-number sign-in, every other enabled provider, and whether public sign-up is disabled"
  say "Nothing is created, granted, written or deleted: this dry run made no gcloud and no curl call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project belong xtrace token config password google anonymous phone
  other-providers public-signup custom pin)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 361 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 361 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer a
  # FAILed pin row as an answer about the providers when the read that would have
  # filled it never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the providers"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The credential file and the scratch directory are this check's own, and both
  # are read back rather than assumed: a run that leaves a live access token on
  # disk has to say so.
  if [ -n "$AUTH_CONF" ]; then
    facts="${facts}"$'\n'"auth-conf|$([ "$AUTH_CONF_REMOVED" = yes ] && echo PASS || echo FAIL)|the credential file $([ "$AUTH_CONF_REMOVED" = yes ] && echo "is gone" || echo "IS STILL THERE at $AUTH_CONF (mode 600, holding an access token) — remove it yourself")"
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check361.sh --project <the workspace's own project> [--dry-run]"

parse_args() {
  P=""
  DRY_RUN=${CHECK361_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$P" ] || die "--project is required, and it has to be the workspace's OWN project: this check reads that project's Identity Platform configuration and never reads a project from a config that may name another. usage: $USAGE"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  fact project PASS "$P (typed by the owner, and the project whose Identity Platform configuration is read)"
  guard_belong
  guard_project
  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: nothing was read from the project, and no gcloud and no curl call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the two arguments, the belong guard, and the dry-run row itself. It reached
    # its end -- the plan IS the end of a dry run -- so the exit trap must not
    # read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project belong xtrace dry-run auth-conf)
    return 0
  fi
  read_config
  if [ "$UNINITIALIZED" = yes ]; then
    fact pin FAIL "$P carries no provider set at all: Identity Platform is not initialized there (CONFIGURATION_NOT_FOUND), so the pinned set google.com and password is not what a person signing in meets. Initialize Identity Platform in the workspace's own project and enable those two providers, then run this check again"
    step_verdict 1
  fi
  if [ "$CONFIG_READ" != yes ]; then
    stop "the configuration was never read, so nothing is concluded about $P's providers"
  fi
  write_reader
  read_and_judge
}

CURL_SECONDS=${CHECK361_CURL_SECONDS:-60}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
