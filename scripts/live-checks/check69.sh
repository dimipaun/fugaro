#!/usr/bin/env bash
# Check 69a and Check 69b — the site is live and honest, and nothing phones home
# — as ONE script YOU run. The register's rows and sections are
# docs/gcp-live-checklist.md, "Check 69a" and "Check 69b".
#
# What it settles is the MACHINE half of both checks. 69a's is its steps 1 to 4:
# the canonical origin answers over HTTPS with a certificate for that host, the
# deployed home page is the artifact `npm run build` produces from this
# checkout, the page a stranger lands on carries no player for the video that
# does not exist, every console download it offers resolves to an asset of the
# release the tree's own facts name with the checksum the page prints beside it
# equal to the one the release's own checksum list records, and every link on it
# resolves. 69b's is its step 4: every absolute URL the deployed markup of `/`,
# `/faq` and `/docs/` carries names a host on `allowed_hosts`.
#
# What stays yours is the browser, and nothing here fakes it. 69a's step 3 as a
# reader sees it — what the console section says, that the Linux pair is the
# unsigned one, and that each link offers the artifact its label names with the
# steps that stand behind it — and 69b's steps 1, 2 and 3: the Network panel's
# host list per page, the consent gate before and after a choice, and the
# cookies a withdrawal deletes. They are printed as MANUAL lines carrying the
# exact text to paste back, and they are the only steps this script does not
# take.
#
# Check 69c (the published ranges are real) is Task W2.3's and is not in the
# register yet — the register's own paragraph says so — so no step of it is
# scripted or prompted here. When its section lands, its scriptable half belongs
# in this script rather than beside it.
#
# Usage:      bash scripts/live-checks/check69.sh [--dry-run]
#             Run it from the root of a checkout of this repository: the site is
#             read from ./website and built there, and the canonical origin is
#             `website/data/site.yaml`'s own `canonical_url`. That file is the
#             one source for it, so no host here is typed from memory, and there
#             is no flag and no CHECK69_* variable that can name another host,
#             another repository or another site directory.
#             --dry-run runs the local guards, prints every command a real run
#             would make and the MANUAL text it would print — and touches
#             nothing: no request leaves the machine. Its block carries no
#             verdict about the site, because it measured none of it.
# Needs:      bash, mktemp, sed, grep, curl, openssl, npm and one of sha256sum
#             or shasum on PATH. Nothing is installed and nothing but the site's
#             own origin and the release's own asset URLs is fetched; a missing
#             tool stops the run with one line naming it.
# Where:      the canonical origin, and nothing else: it is the only host this
#             script names. The release asset URLs the deployed pages carry are
#             followed where the pages carry them, and they are github.com,
#             which redirects them to the release's own storage. No cloud
#             project is named anywhere, no `fugaro` command runs, and the
#             site's own Firebase project is not named either — it is not
#             needed to read a public site. The owner's own installation,
#             `belong`, is refused by name before anything is fetched.
# Costs:      a few GETs and HEADs to the site and to the release's assets. No
#             money, no project, no model call and no credential.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory holding the
#             fetched pages and the build log is removed, and the `restore` row
#             says whether it really went. This check changes nothing live — it
#             is reads only. Its one local write is the site's own build output,
#             `website/dist/`, which check 69a's step 2 builds there anyway; the
#             script leaves it in place, because it is the artifact every fact
#             below is about.
# Output:     progress on stderr, the MANUAL lines and ONE block on stdout
#             headed `CHECK 69 RESULT`, whose last line is
#             `CHECK 69 RESULT: PASS|FAIL`. Exit 0 when every fact held, 1 when
#             one FAILed — a deployed page that is not this commit's build, a
#             download that 404s and a markup URL off `allowed_hosts` are all
#             readings about the site and all exit 1 — 2 when the run could not
#             tell (a guard stopped it, a tool is missing, an internal error
#             ended it) and 130 when it was interrupted.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: `bash -x check69.sh` is
# what someone reaches for when a run failed, and a trace would print every
# command and every fetched byte a second time, into the log they would paste
# back. The flag sits in SHELLOPTS/BASHOPTS for a shell that arrived tracing, so
# each source is read on its own — BASHOPTS carries names like extquote, and one
# pattern over the three concatenated would match that 'x' and refuse every run.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so MANUAL lines and the
# RESULT block are written to the saved descriptors rather than to whatever fd 1
# is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
HELP=""
# Set while the exit trap is running, so a second Ctrl-C is refused rather than
# killing this shell mid-cleanup: see on_signal.
CLEANUP_RUNNING=no
DRY_RUN=""
# Set by halt() when a reading about the site makes every fact below it
# unmeasurable — an origin that does not answer, a page that 404s, a build that
# fails. The rows after it then print as not reached, and the row that caused it
# carries the answer.
HALT=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back. It is
# for a run that could not tell — a missing tool, a guard that refused the
# subject — and never for a reading about the site, which is a FAILed fact and
# an exit 1.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

# halt REASON: a reading about the site that makes the facts below it
# unmeasurable. It is NOT an abort — the site really did answer this way, and
# that is the finding — so the run finishes with the rows it took and exits 1.
halt() {
  fact aborted FAIL "$1"
  HALT=$1
  owner "stopping the chain here: $1"
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as an answer about the site for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the site"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 69 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 69 needs bash"
  need mktemp
  need sed "the canonical origin, the allowed hosts and the release's own fields are read out of the site's files with sed, because nothing here may name a host from memory"
  need grep "the page greps ARE the check: a player, a download URL and an absolute URL are all found by one"
  need curl "every fact about the live site is a response curl read"
  need openssl "the certificate's subject is read with s_client and x509, which is the one part of step 1 curl cannot print"
  command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 \
    || die "Check 69 needs 'sha256sum' (Linux) or 'shasum' (macOS) on PATH and neither is there: the deployed home page and the built one are compared by hash"
}

sha256_of() { # sha256_of FILE — the digest alone, whichever tool this machine has
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk69-mine"
SCRATCH_REMOVED=no
PRINTED=""
# The site, read from the current directory and never from a variable: the
# register's own guard reads the origin out of this file, and a knob that could
# point it elsewhere would make "no host is typed from memory" untrue.
SITE_DIR="website"
SITE_YAML=""
DOWNLOADS_JSON=""
SUMS_FILE=""
C=""
HOST=""
REPOSITORY=""
ALLOWED=()
FLAG_DOWNLOADS=""
FLAG_VIDEO=""
AVAILABLE=""
TAG_PREFIX=""
VERSION=""
TAG=""
HOME_HTML=""
CONSOLE_HTML=""
FAQ_HTML=""
DOCS_HTML=""
LOCAL_SHA=""
BUILD_LOG=""
CURL_LOG=""
# The steps no script can take, set by step_manual on every path so print_block
# can ask for them by name.
MANUAL_1=""
MANUAL_2=""
MANUAL_3=""
MANUAL_4=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk69.XXXXXXXX") || die "Check 69 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  HOME_HTML="$SCRATCH/home.html"
  CONSOLE_HTML="$SCRATCH/console.html"
  FAQ_HTML="$SCRATCH/faq.html"
  DOCS_HTML="$SCRATCH/docs.html"
  BUILD_LOG="$SCRATCH/build.log"
  CURL_LOG="$SCRATCH/curl.log"
  : >"$CURL_LOG"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 69 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  CLEANUP_RUNNING=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
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
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  # --help measured nothing and asked nothing: it printed the usage and it gets
  # no block and no verdict, only its own exit status.
  [ -n "$HELP" ] && exit 0
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the live site when the steps
  # that would have filled them never ran. Any such ending is an abort, and 2
  # says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the site"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and nothing live was touched"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local note="this check changed nothing live — every call it makes is a read — and its scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"). Its one local write is $SITE_DIR/dist, the site's own build output, which check 69a's step 2 builds there too; it is left in place because it is the artifact the deployed page is compared against"
    facts="${facts}"$'\n'"restore|$restore|${note}"
  fi
  # The restore row is a row this run measured, and main decided its status from
  # the facts file before the row existed: a block whose last line says FAIL
  # beside an exit status of 0 is the check telling two stories, and the section
  # promises the status the block gives. So a finished run whose verdict is FAIL
  # exits 1 — and only that: 2 (the run could not tell) and 130 (interrupted)
  # keep their meanings.
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ] && [ "$(verdict_of "$facts")" = FAIL ]; then
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs and the bytes it fetched into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check69.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_site: the site's own files, read from the current directory. The check is
# about the site this checkout builds, so a tree without them has nothing to
# read and the honest answer is the stop.
guard_site() {
  [ -d "$SITE_DIR" ] \
    || stop "Check 69 stopped: there is no $SITE_DIR directory in $(pwd), so which site to read is unknown. Run it from the root of a checkout of this repository: cd <checkout> && bash scripts/live-checks/check69.sh"
  SITE_YAML="$SITE_DIR/data/site.yaml"
  DOWNLOADS_JSON="$SITE_DIR/content/downloads.json"
  SUMS_FILE="$SITE_DIR/data/console-sha256sums.txt"
  local f
  for f in "$SITE_YAML" "$DOWNLOADS_JSON" "$SUMS_FILE"; do
    [ -f "$f" ] || stop "Check 69 stopped: $f is not in this tree, and it is one of the site's own facts — the canonical origin ($SITE_YAML), the console's release ($DOWNLOADS_JSON) and the release's own checksum list ($SUMS_FILE). Run this check in a checkout that carries it"
  done
  fact site PASS "$SITE_DIR (read from $(pwd)): $SITE_YAML, $DOWNLOADS_JSON and $SUMS_FILE are all in the tree"
}

# guard_canonical: the origin, from the site's own facts file, which the
# register's guard block reads the same way. An empty value is a stop rather
# than a host to invent, and anything that is not an https origin is one too:
# this check is ABOUT https, so a value of another shape is not a reading it can
# take.
guard_canonical() {
  C=$(sed -n 's/^canonical_url:[[:space:]]*//p' "$SITE_YAML" | head -n 1)
  [ -n "$C" ] \
    || stop "Check 69 stopped: $SITE_YAML gives canonical_url='$C'. The check fetches that origin and nothing else, and no host here is typed from memory, so an empty value is a stop rather than a host to invent"
  case "$C" in
  https://*) : ;;
  *) stop "Check 69 stopped: $SITE_YAML gives canonical_url='$C', which is not an https origin. Check 69a asserts that the canonical origin answers over HTTPS with a certificate for that host, so there is nothing this check could fetch and read" ;;
  esac
  HOST=${C#https://}
  HOST=${HOST%%/*}
  HOST=$(printf '%s' "$HOST" | sed 's/^[^@]*@//; s/:.*$//' | tr 'A-Z' 'a-z')
  [ -n "$HOST" ] \
    || stop "Check 69 stopped: $SITE_YAML gives canonical_url='$C', which names no host — the check has nothing to fetch and no certificate subject to read"
  REPOSITORY=$(sed -n 's/^repository:[[:space:]]*//p' "$SITE_YAML" | head -n 1)
  ALLOWED=()
  local h
  while IFS= read -r h; do
    if [ -n "$h" ]; then ALLOWED+=("$h"); fi
  done < <(sed -n '/^allowed_hosts:/,/^[^ -]/p' "$SITE_YAML" | sed -n 's/^[[:space:]]*-[[:space:]]*//p')
  [ "${#ALLOWED[@]}" -gt 0 ] \
    || stop "Check 69 stopped: $SITE_YAML names no allowed_hosts entry, so 69b's step 4 and this check's own markup fact would be compared against nothing"
  say "canonical origin: $C (host $HOST, on allowed_hosts: $(host_allowed "$HOST" && echo yes || echo NO))"
  say "allowed hosts: $(printf '%s, ' "${ALLOWED[@]}" | sed 's/, $//')"
}

# guard_belong: `belong` is the owner's own installation, and this check names
# no project of anyone's — but a host or a repository that names it is a
# mistyped tree, and reading the wrong site is what this refusal exists to stop.
# It comes before any fetch, so the refusal is the reason the run ended rather
# than a stop another guard's message could hide behind.
guard_belong() {
  local low
  low=$(printf '%s %s' "$HOST" "$REPOSITORY" | tr 'A-Z' 'a-z')
  # A SUBSTRING test, as Check 340's own refusal is: the host and the repository
  # come out of the site's own file, so a value that merely contains one of these
  # names is a mistyped tree, and refusing it is the harmless direction. `belong`
  # and the two Edge names are the owner's own installations, which no check of
  # this family reads.
  case "$low" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "'$HOST' or '$REPOSITORY' names one of the owner's own installations (belong, EdgeWeb, EdgeServer): Check 69 reads a public site and no step of it may touch them"
    stop "Check 69 never runs against 'belong', EdgeWeb or EdgeServer. $SITE_YAML's canonical_url is $C and its repository is ${REPOSITORY:-none}, and one of them carries a name this check refuses. Fix the tree, or run the check in the checkout whose site you mean to read"
    ;;
  esac
  fact belong PASS "neither the canonical host ($HOST) nor the repository (${REPOSITORY:-none}) carries 'belong', 'edgeweb' or 'edgeserver', and this check names no cloud project and runs no fugaro command"
}

# host_allowed HOST: is this host on the site's own allowed_hosts list?
host_allowed() { # host_allowed HOST
  local a
  for a in "${ALLOWED[@]}"; do
    [ "$a" = "$1" ] && return 0
  done
  return 1
}

# --------------------------------------------------------------- the site facts
# step_site_facts: the flags and the release, read OUT of the tree rather than
# remembered here. What step 3 expects is what the tree says: the console's flag
# is `site.yaml`'s, the release the pages should offer is `downloads.json`'s, and
# the assets and their checksums are the release's own list's.
step_site_facts() {
  FLAG_DOWNLOADS=$(sed -n '/^features:/,/^[^ ]/p' "$SITE_YAML" | sed -n 's/^[[:space:]]*console_downloads:[[:space:]]*//p' | head -n 1)
  FLAG_VIDEO=$(sed -n '/^features:/,/^[^ ]/p' "$SITE_YAML" | sed -n 's/^[[:space:]]*video:[[:space:]]*//p' | head -n 1)
  local release_block
  release_block=$(sed -n '/"release": {/,/}/p' "$DOWNLOADS_JSON")
  TAG_PREFIX=$(printf '%s\n' "$release_block" | sed -n 's/^[[:space:]]*"tag_prefix":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  VERSION=$(printf '%s\n' "$release_block" | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  # The tag a URL carries is the two together — `console-v` and `0.122.0` are the
  # release `console-v0.122.0` — and the site's own console-downloads.mjs composes
  # it the same way.
  TAG="${TAG_PREFIX}${VERSION}"
  AVAILABLE=$(sed -n 's/^[[:space:]]*"available":[[:space:]]*\([a-z]*\).*/\1/p' "$DOWNLOADS_JSON" | head -n 1)
  case "$FLAG_DOWNLOADS" in
  true | false) : ;;
  *) stop "Check 69 stopped: $SITE_YAML's features.console_downloads reads '$FLAG_DOWNLOADS', which is neither true nor false, so what step 3 expects the page to offer is unknown" ;;
  esac
  case "$FLAG_VIDEO" in
  true | false) : ;;
  *) stop "Check 69 stopped: $SITE_YAML's features.video reads '$FLAG_VIDEO', which is neither true nor false, so whether a player is expected is unknown" ;;
  esac
  [ -n "$TAG_PREFIX" ] && [ -n "$VERSION" ] \
    || stop "Check 69 stopped: $DOWNLOADS_JSON's release block gives tag_prefix='$TAG_PREFIX' and version='$VERSION'. The tag a download URL carries is the two together and the version is what the release's own asset names carry, so a missing value is a stop rather than a URL to guess at"

  # The release's own checksum list, one `<64 hex>  <name>` line per asset. The
  # name here is the RELEASE's spelling; the name a reader's link carries is the
  # one GitHub serves, and the one step between them is a space becoming a dot
  # (src/lib/console-downloads.mjs's own rule). Both are recorded, because the
  # checksum is checked against the release's name and the URL against the
  # served one.
  : >"$SCRATCH/sums"
  : >"$SCRATCH/artifacts"
  local line sha name served
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    sha=${line%%  *}
    name=${line#*  }
    if [ "$sha" = "$line" ] || [ -z "$name" ] || ! printf '%s' "$sha" | grep -qE '^[0-9a-f]{64}$'; then
      stop "Check 69 stopped: $SUMS_FILE carries the line '$line', and the file the release publishes is one '<64 lower-case hex>  <name>' line per asset — the shape sha256sum writes. A line this script cannot read is an asset nothing below would cover, so it is a stop rather than a line to skip"
    fi
    served=$(printf '%s' "$name" | tr ' ' '.')
    printf '%s|%s|%s\n' "$served" "$sha" "$name" >>"$SCRATCH/sums"
    case "$name" in
    *"$VERSION"*) printf '%s\n' "$served" >>"$SCRATCH/artifacts" ;;
    esac
  done <"$SUMS_FILE"
  local n_artifacts n_sums
  n_sums=$(grep -c . "$SCRATCH/sums" || true)
  n_artifacts=$(grep -c . "$SCRATCH/artifacts" || true)
  [ "$n_artifacts" -gt 0 ] \
    || stop "Check 69 stopped: no line of $SUMS_FILE carries the version $VERSION, so the release's own list names no asset of the release $TAG — the release the tree names is not the one its own checksum list is about"

  local tallied="$n_artifacts artifact(s) of $TAG in $SUMS_FILE"
  if [ "$AVAILABLE" = "true" ] && [ "$FLAG_DOWNLOADS" = "false" ]; then
    tallied="$tallied, AND downloads.json's available is true while site.yaml's features.console_downloads is false: the site's own build refuses that pair"
  elif [ "$AVAILABLE" != "true" ] && [ "$FLAG_DOWNLOADS" = "true" ]; then
    tallied="$tallied, AND site.yaml's features.console_downloads is true while downloads.json's available is '$AVAILABLE': the site's own build refuses that pair"
  fi
  fact site-facts PASS "features.console_downloads=$FLAG_DOWNLOADS, features.video=$FLAG_VIDEO, downloads.json's available=$AVAILABLE, the release $TAG, $tallied, and ${#ALLOWED[@]} allowed host(s): $(printf '%s, ' "${ALLOWED[@]}" | sed 's/, $//'). Every value is read out of the tree, so what the steps below expect is what this checkout says"
}

# ---------------------------------------------------------------- the live reads
curl_get() { # curl_get URL OUTFILE — the body into OUTFILE, "code effective-url" on stdout
  curl -sS -L -H 'Accept-Encoding: identity' -o "$2" -w '%{http_code} %{url_effective}' "$1" 2>>"$CURL_LOG" || true
}

curl_head() { # curl_head URL — "code effective-url" for a HEAD, following redirects
  curl -sS -L -I -o /dev/null -w '%{http_code} %{url_effective}' "$1" 2>>"$CURL_LOG" || true
}

# step_origin: 69a's step 1. The scheme, the status, the resolved address and
# curl's own TLS verdict, then the certificate itself. A canonical origin that
# does not answer over HTTPS with a verified chain is a reading about the site —
# 69a's own Fails-when names the registrar and the hosting step behind it — so
# it FAILs the row and stops the chain rather than the run.
step_origin() {
  local out code scheme ip verify effective err first
  err="$SCRATCH/origin.err"
  out=$(curl -sS -L -o /dev/null \
    -w '%{scheme} %{http_code} %{remote_ip} %{ssl_verify_result} %{url_effective}' "$C" 2>"$err") || true
  set -- $out
  scheme=${1:-}
  code=${2:-}
  ip=${3:-}
  verify=${4:-}
  effective=${5:-}
  if [ -z "$code" ]; then
    first=$(head -n 1 "$err" 2>/dev/null || true)
    fact https FAIL "$C did not answer at all: curl said '${first:-no line}'. Check 69a's fails-when clause is where the fix belongs — an unattached domain, a missing DNS record or a certificate that does not verify is your registrar and hosting step, not a site bug"
    halt "the canonical origin $C did not answer, so nothing below it could be measured"
    return 0
  fi
  # The response's own first lines, as the register's step 1 prints them: an
  # ETag, a Cache-Control and a Server say what served this and whether anything
  # is in front of it.
  local headers
  headers=$(curl -sS -I "$C" 2>>"$CURL_LOG" | head -n 5 | tr '\n' ' ' | sed 's/  */ /g' || true)
  if [ "$scheme" != "https" ] || [ "$code" != "200" ] || [ "$verify" != "0" ]; then
    fact https FAIL "$scheme $code remote_ip=$ip ssl_verify_result=$verify url_effective=$effective, and the response's first lines are: ${headers:-none}. The reading that passes is https with status 200 and ssl_verify_result 0; where the fix belongs follows from which of the three failed — an unattached domain, a missing DNS record or an unverified chain is your registrar and hosting step, and a status that is not 200 is what the site serves at its own canonical origin"
    halt "the canonical origin did not answer over https with status 200 and a verified chain, so nothing below it could be measured"
    return 0
  fi
  fact https PASS "$scheme $code remote_ip=$ip ssl_verify_result=$verify url_effective=$effective — HTTPS from the canonical origin with a verified chain. The response's first lines: ${headers:-none}"

  # The certificate. `echo | openssl s_client` hands it to `openssl x509`, which
  # is what the register's step 1 prints: the subject, the issuer and the two
  # dates. The subject-Alt-Names half is asked separately, because a certificate
  # for a host commonly carries it there rather than in the CN.
  local cert san
  cert=$( { echo | openssl s_client -connect "$HOST:443" -servername "$HOST" 2>/dev/null || true; } | openssl x509 -noout -subject -issuer -dates 2>/dev/null || true)
  san=$( { echo | openssl s_client -connect "$HOST:443" -servername "$HOST" 2>/dev/null || true; } | openssl x509 -noout -ext subjectAltName 2>/dev/null || true)
  local flat
  flat=$(printf '%s %s' "$cert" "$san" | tr '\n' ' ' | sed 's/  */ /g')
  if [ -z "$flat" ]; then
    fact certificate FAIL "the certificate could not be read at all: openssl printed nothing for ${HOST}:443. Check 69a's step 1 reads the subject, the issuer and the dates with s_client and x509, and a handshake that gives no certificate is a hosting step rather than a site bug"
    halt "no certificate could be read for $HOST, so the subject this fact is about is unknown"
    return 0
  fi
  if printf '%s' "$flat" | grep -qiE "(^|[^a-z0-9.-])${HOST//./\\.}([^a-z0-9.-]|\$)"; then
    fact certificate PASS "the certificate names $HOST — ${flat}"
  else
    fact certificate FAIL "the certificate does not name $HOST: ${flat}. Check 69a's step 1 asserts a certificate whose subject is that host, and a chain that verifies for another name is the domain-attachment step rather than a site bug"
  fi
}

# step_pages: the four pages every fact below reads — the home page and the
# console page for the downloads, and the home page, the FAQ and the docs hub for
# 69b's markup scan — fetched once each, into the scratch directory. A page that
# does not answer 200 stops the chain: a grep over an empty body would pass for
# the wrong reason, and a redirect off the canonical host would make every
# markup reading a reading about another site.
step_pages() {
  local p file res code where host bad=""
  for p in / /console/ /faq /docs/; do
    case "$p" in
    /) file=$HOME_HTML ;;
    /console/) file=$CONSOLE_HTML ;;
    /faq) file=$FAQ_HTML ;;
    /docs/) file=$DOCS_HTML ;;
    esac
    res=$(curl_get "$C$p" "$file")
    set -- $res
    code=${1:-}
    where=${2:-}
    if [ -z "$code" ]; then
      bad="$bad $p (no answer, curl's own line is in $CURL_LOG)"
      continue
    fi
    if [ "$code" != "200" ]; then
      bad="$bad $p (HTTP $code)"
      continue
    fi
    host=$(host_of "$where")
    if [ "$host" != "$HOST" ]; then
      bad="$bad $p (200 but it landed on $where, whose host is $host and not $HOST)"
    fi
  done
  if [ -n "$bad" ]; then
    fact pages FAIL "these pages of the canonical origin did not answer 200 on the canonical host:$bad. Every fact below reads one of them, and a grep over an empty body or another site's page would pass for the wrong reason, so the run stops here rather than reporting them"
    halt "the site's own pages did not all answer 200 on the canonical host, so the page facts could not be measured"
    return 0
  fi
  fact pages PASS "$C/ , $C/console/ , $C/faq and $C/docs/ all answered 200 on $HOST (fetched with Accept-Encoding: identity, so the bytes the hash below is about are the ones the server holds)"
}

# step_build: 69a's step 2's first half. The artifact the deployed page has to
# BE is the one this checkout builds, so the build runs here rather than being
# left to you — and a build that fails stops the chain, because there is then
# nothing to compare the deployed page against.
step_build() {
  if [ ! -d "$SITE_DIR/node_modules" ]; then
    fact build FAIL "there is no $SITE_DIR/node_modules in this tree, so the site cannot be built and the deployed page has nothing to be compared against. Run 'cd $SITE_DIR && npm ci' once and run this check again"
    halt "the site's dependencies are not installed, so the artifact to compare against does not exist"
    return 0
  fi
  command -v npm >/dev/null 2>&1 \
    || { fact build FAIL "npm is not on PATH, so the site cannot be built and the deployed page has nothing to be compared against"; halt "npm is not on PATH, so the artifact to compare against does not exist"; return 0; }
  if ! (cd "$SITE_DIR" && npm run build) >"$BUILD_LOG" 2>&1; then
    fact build FAIL "'cd $SITE_DIR && npm run build' failed, so the deployed page has nothing to be compared against. The last lines of the build: $(tail -n 3 "$BUILD_LOG" 2>/dev/null | tr '\n' ' ')"
    halt "the site's own build failed, so the artifact to compare against does not exist"
    return 0
  fi
  if [ ! -f "$SITE_DIR/dist/index.html" ]; then
    fact build FAIL "'npm run build' answered 0 and wrote no $SITE_DIR/dist/index.html, so the deployed page has nothing to be compared against: the build's own output is in the log at $BUILD_LOG"
    halt "the build wrote no home page, so the artifact to compare against does not exist"
    return 0
  fi
  LOCAL_SHA=$(sha256_of "$SITE_DIR/dist/index.html")
  local commit
  commit=$(git log -1 --format='%h %s' 2>/dev/null || true)
  fact build PASS "npm run build produced $SITE_DIR/dist/index.html, sha256 $LOCAL_SHA ($(wc -c <"$SITE_DIR/dist/index.html" | tr -d ' ') bytes), from the tree checked out here${commit:+ — the tree is at $commit}"
}

# ------------------------------------------------------------------ the compares
host_of() { # host_of URL — the host alone, lower-cased
  printf '%s' "$1" | sed -E 's#^[A-Za-z]+://##; s#^[^@/]*@##; s#[/?].*$##; s#:.*$##' | tr 'A-Z' 'a-z'
}

# step_hash: 69a's step 2's second half. The deployed home page's sha256 against
# the build's — the one reading that says the site is THIS commit's artifact, and
# the one 69a's Fails-when calls a deploy of an older commit.
step_hash() {
  local deployed
  deployed=$(sha256_of "$HOME_HTML")
  if [ "$deployed" = "$LOCAL_SHA" ]; then
    fact deployed-hash PASS "the deployed home page's sha256 is $LOCAL_SHA, which is the hash of the $SITE_DIR/dist/index.html this tree just built, so the live home page IS the artifact this commit's build produced"
  else
    fact deployed-hash FAIL "the deployed home page's sha256 is $deployed and this tree's build is $LOCAL_SHA ($(wc -c <"$HOME_HTML" | tr -d ' ') bytes deployed against $(wc -c <"$SITE_DIR/dist/index.html" | tr -d ' ') built). Check 69a's fails-when clause: a page that is not the build's output is a deploy of an older commit — scripts/deploy-website.sh's --dry-run prints the command without running it — and until it is deployed the live site is not this commit's"
  fi
}

# step_player: 69a's step 3's first grep. The explainer video does not exist
# (`features.video` is false), so the deployed home page may carry no player, no
# iframe, no poster and no embed for one. The grep is the register's own.
step_player() {
  # Both pages the register's step 3 greps, and the grep is the register's own.
  # A hit is carried with the page it came from, because "youtube" in a sentence
  # is not a player and the owner is the one who reads the hit.
  local page found="" what
  for page in home console; do
    case "$page" in
    home) what="$HOME_HTML" ;;
    console) what="$CONSOLE_HTML" ;;
    esac
    local page_hits
    page_hits=$(grep -niE '<video|<iframe|youtube|vimeo|poster=' "$what" | head -n 20 || true)
    if [ -n "$page_hits" ]; then
      found="$found the $page page: $page_hits;"
    fi
  done
  case "$FLAG_VIDEO" in
  true)
    fact player INFO "features.video is true, so a player IS expected and W3.3 is the task that puts one there — but this script's grep is the rule of the other state: it refuses a player for a video that does not exist. Read the hero in a browser (W3.3 is parked, so this checkout should not be in that state). What the grep printed:${found:- none on either page}"
    ;;
  *)
    if [ -n "$found" ]; then
      fact player FAIL "a page carries a player for a video that does not exist (site.yaml's features.video is false), and step 3 of Check 69a is about exactly this: no <video>, no <iframe>, no poster and no embed for one. What the grep printed:$found"
    else
      fact player PASS "features.video is false and neither the deployed home page nor the console page carries a player: the grep for '<video', '<iframe', 'youtube', 'vimeo' and 'poster=' printed nothing on $C/ or $C/console/"
    fi
    ;;
  esac
}

# step_downloads: 69a's step 3's second half — the download URLs the pages carry,
# the checksums printed beside them, and whether the bytes are there. Four
# readings in one, because they are four readings of the same rows:
#   * every URL the pages carry IS an asset of the release the tree names (the
#     tag is downloads.json's, the last segment is a name the release's own
#     checksum list holds, and no URL carries an encoded space — the macOS
#     artifacts are spelled with a space in the release and served with a dot,
#     so a %20 is a 404 on every reader's link);
#   * every artifact of that release is offered by some page;
#   * every URL answers 200, or a redirect that lands on 200;
#   * the checksum printed beside it is the one the release's own list records.
step_downloads() {
  local page f urls shas nu ns
  local dl_fail="" sum_fail="" urls_seen="" pairs=0 total_urls=0 total_shas=0
  for page in home console; do
    case "$page" in
    home) f=$HOME_HTML ;;
    console) f=$CONSOLE_HTML ;;
    esac
    urls=$(grep -oE 'href="https?://[^"]*releases/download/[^"]*"' "$f" 2>/dev/null | sed -E 's/^href="//; s/"$//' || true)
    shas=$(grep -oE 'download-sha256[^>]*>[0-9a-f]{64}<' "$f" 2>/dev/null | grep -oE '[0-9a-f]{64}' || true)
    printf '%s\n' "$urls" | grep . >"$SCRATCH/urls.$page" || true
    printf '%s\n' "$shas" | grep . >"$SCRATCH/shas.$page" || true
    nu=$(grep -c . "$SCRATCH/urls.$page" || true)
    ns=$(grep -c . "$SCRATCH/shas.$page" || true)
    total_urls=$((total_urls + nu))
    total_shas=$((total_shas + ns))
    if [ "$nu" -ne "$ns" ]; then
      dl_fail="${dl_fail}the $page page carries $nu download link(s) and $ns checksum(s) printed beside them, so no link can be paired with the checksum step 3 asks you to read beside it. "
    fi
    paste -d'|' "$SCRATCH/urls.$page" "$SCRATCH/shas.$page" 2>/dev/null | sed "s/^/$page|/" >>"$SCRATCH/dl" || true
  done

  local line rowurl rowsha seg code where tagprefix
  while IFS='|' read -r page rowurl rowsha; do
    [ -n "$rowurl" ] || continue
    pairs=$((pairs + 1))
    seg=${rowurl##*/}
    case "$rowurl" in
    *%20* | *%2520*) dl_fail="${dl_fail}$rowurl carries an encoded space, and the name the release SERVES has a dot there: GitHub rewrites a space in an uploaded asset's name, so the URL with %20 is a 404 on every reader's link. " ;;
    esac
    # The leading `*` sits OUTSIDE the quotes on purpose: inside them it is a
    # literal asterisk and the pattern would match no URL at all, which is a
    # failure message that blames the site for this script's own bug.
    case "$rowurl" in
    */"releases/download/$TAG/"*) : ;;
    *) dl_fail="${dl_fail}$rowurl is not an asset of the release the tree names ($TAG, from $DOWNLOADS_JSON): it does not carry /releases/download/$TAG/. " ;;
    esac
    local sums_line
    sums_line=$(grep -m1 "^${seg}|" "$SCRATCH/sums" || true)
    if [ -z "$sums_line" ]; then
      dl_fail="${dl_fail}$rowurl's last segment is '$seg', which no line of $SUMS_FILE names — neither the release's own spelling nor the one GitHub serves. A download the release does not hold is the drift this step is for. "
    else
      urls_seen="$urls_seen $seg"
      if [ -n "$rowsha" ]; then
        local recorded
        recorded=$(printf '%s' "$sums_line" | cut -d'|' -f2)
        if [ "$rowsha" != "$recorded" ]; then
          sum_fail="${sum_fail}$seg: the page prints $rowsha and $SUMS_FILE records $recorded for $(printf '%s' "$sums_line" | cut -d'|' -f3). A checksum that is not the release's own is a checksum that fails for every reader who checks their download. "
        fi
      fi
    fi
    code=$(curl_head "$rowurl")
    set -- $code
    code=${1:-}
    where=${2:-}
    if [ -z "$code" ]; then
      dl_fail="${dl_fail}$rowurl did not answer at all (curl's own line is in $CURL_LOG). "
    elif [ "$code" != "200" ]; then
      dl_fail="${dl_fail}$rowurl answered HTTP $code (landing on ${where:-nowhere}) — a redirect to the release's own storage is fine and a 404 is not, and this is the one failure a hermetic gate cannot see: the site's own tests check the URL's shape against the release's facts, and only the release can answer whether the bytes are there. "
    fi
    say "  $seg -> HTTP ${code:-no answer}${where:+ (${where})}"
  done <"$SCRATCH/dl"

  # Every artifact of the release is offered SOMEWHERE, which is what makes the
  # four rows of 69a's pass clause true all at once — the two Linux ones too.
  local artifact
  while IFS= read -r artifact; do
    [ -n "$artifact" ] || continue
    case " $urls_seen " in
    *" $artifact "*) : ;;
    *) dl_fail="${dl_fail}$artifact is an artifact of $TAG and no page this check read offers it, so a reader of the live site cannot download it. " ;;
    esac
  done <"$SCRATCH/artifacts"

  if [ "$FLAG_DOWNLOADS" = "false" ]; then
    # The unreleased state: the flag is the site's own, and while it is off the
    # rule is the other way round — no page may carry a release download URL at
    # all.
    if [ "$pairs" -gt 0 ]; then
      fact downloads FAIL "site.yaml's features.console_downloads is false and a page this check read carries $pairs release download link(s). A reader must meet no link for a release the site's own facts say is not offered, and every URL here is a link the flag exists to withhold"
    else
      fact downloads PASS "features.console_downloads is false and neither the home page nor the console page carries a release download URL, which is the state the flag asks for"
    fi
  elif [ -n "$dl_fail" ]; then
    fact downloads FAIL "features.console_downloads is true, so the pages must offer the release's own downloads and every one of them has to resolve. ${dl_fail}$(wc -l <"$SCRATCH/dl" | tr -d ' ') link(s) were checked, one HEAD each"
  else
    fact downloads PASS "features.console_downloads is true and every download the home page and the console page offer is an artifact of $TAG, resolving to 200: $(printf '%s' "$urls_seen" | sed 's/^ //; s/ /, /g') — every artifact the release's own list names is offered by a page a reader can reach, the two Linux rows included"
  fi

  if [ "$total_urls" -eq 0 ]; then
    fact checksums INFO "no download link and no checksum was printed on the pages this check read, so there is nothing to pair. On today's tree that is the unreleased state and the downloads row says so; with features.console_downloads true it is the downloads row's own failure"
  elif [ "$total_shas" -ne "$total_urls" ]; then
    fact checksums FAIL "the pages this check read print $total_urls download link(s) and $total_shas checksum(s), so the checksum step 3 asks you to read BESIDE each link is missing from $((total_urls - total_shas)) of them — a reader cannot check a download whose checksum is not printed with it"
  elif [ -n "$sum_fail" ]; then
    fact checksums FAIL "${sum_fail}The checksums are compared against $SUMS_FILE, the release's own list, which is the only thing that says WHICH value is the right one — a well-formed checksum is one a reader can compare a download against and says nothing about whether it is this release's"
  else
    fact checksums PASS "all $pairs checksum(s) printed beside a download link on the home page and the console page equal the value $SUMS_FILE records for that asset — the release's own list, read by hand rather than with shasum -c, whose macOS lines are spelled with a space this file's URLs are served with a dot (a defect on the release side and not on the site's, which is why the site's own copy is the one the build reads)"
  fi
}

# step_links: 69a's step 4 — every link the home page carries resolves. The
# paths are the ones the deployed markup carries, so a 404 here is the drift the
# hermetic link check cannot see: that check walks the links of the build it just
# made, and this one walks the links the live site serves.
step_links() {
  local hrefs path seen="" checked=0 bad=""
  hrefs=$(grep -oE 'href="/[^"]*"' "$HOME_HTML" 2>/dev/null | sed -E 's/^href="//; s/"$//' || true)
  local p
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    path=${p%%#*}
    path=${path%%\?*}
    [ -n "$path" ] || continue
    case " $seen " in
    *" $path "*) continue ;;
    esac
    seen="$seen $path"
    checked=$((checked + 1))
    local res code
    res=$(curl_head "$C$path")
    set -- $res
    code=${1:-}
    if [ "$code" != "200" ]; then
      bad="$bad $path (HTTP ${code:-no answer})"
    fi
  done <<<"$hrefs"
  if [ -n "$bad" ]; then
    fact links FAIL "the deployed home page carries these same-origin links and they do not all resolve:$bad. Step 4 of Check 69a follows every path the page prints, and a 404 here is drift between what the site links and what it serves"
  else
    fact links PASS "every one of the $checked distinct same-origin link(s) the deployed home page carries answered 200 — step 4 of Check 69a, over the links the LIVE site serves rather than over the ones the build just made"
  fi
}

# step_markup_origins: 69b's step 4, the machine half of nothing-phones-home.
# Every absolute URL the deployed markup of the three pages carries has to name a
# host on `allowed_hosts` — and the analytics origin counts here too: it is
# admitted by the consent task's own check and not by that list, and the gate is
# code in the site's own file rather than a tag in the markup, so an absolute
# URL to it in the markup is a failure of this step.
step_markup_origins() {
  local page file offenders="" found=0 host
  for page in "/:$HOME_HTML" "/faq:$FAQ_HTML" "/docs/:$DOCS_HTML"; do
    local p=${page%%:*}
    file=${page#*:}
    local u
    while IFS= read -r u; do
      [ -n "$u" ] || continue
      host=$(host_of "$u")
      [ -n "$host" ] || continue
      found=$((found + 1))
      if ! host_allowed "$host"; then
        offenders="$offenders $p carries an absolute URL on $host"
      fi
    done < <(grep -oE '(src|href)="https?://[^"]+"' "$file" 2>/dev/null | sed -E 's/^[a-z]+="//; s/"$//' || true)
  done
  if [ -n "$offenders" ]; then
    fact markup-origins FAIL "$offenders — and none of them is on allowed_hosts ($(printf '%s, ' "${ALLOWED[@]}" | sed 's/, $//')). Step 4 of Check 69b holds every absolute URL the deployed markup carries to that list; a host that is not on it is a request a reader's browser makes for this site's page, and the fix is never to add the host to allowed_hosts — that list is what the site's build refuses links to, so a new origin is a decision about the site's privacy page taken by the task that wants it"
  else
    fact markup-origins PASS "every one of the $found absolute URL(s) the deployed markup of $C/ , $C/faq and $C/docs/ carries names a host on allowed_hosts ($(printf '%s, ' "${ALLOWED[@]}" | sed 's/, $//')), and no page carries an absolute src or href to the analytics origin: the gate's own origin reaches a page as a data attribute on the consent banner, which is what the site's file reads before it decides, and the register's own step 4 grep is over src and href — a tag or a link to it in the markup would be a request a reader's browser makes before any choice"
  fi
}

# ------------------------------------------------------------- the manual lines
# The steps no script can take: a browser, a panel and a person looking at a
# cookie list. They are printed as MANUAL lines carrying the exact text to paste
# back, because the register's rows are written from what you saw — and nothing
# here fakes them.
step_manual() {
  MANUAL_1="open $C/ and $C/console/ in a browser and read them: the console section says what the console is, that the macOS build is signed and notarised and that the Linux build is not signed at all, and each of the four links offers a .dmg, a .zip, an .AppImage or a .deb with its checksum beside it and the steps that stand behind the unsigned pair under them. Paste back: what each grep hit above was (a hit is not automatically a failure — the word download in a sentence is not a link), the page's own wording for each of those, your browser and its version, and the date"
  MANUAL_2="open $C/ , $C/faq and $C/docs/ with the Network panel open, Disable cache on and a hard reload, once per page. (1) Write down the host list per page — it must hold the canonical host and, where the page links them, github.com and raw.githubusercontent.com, and nothing else. (2) Then, on one page: no request to the analytics origin (https://www.googletagmanager.com) BEFORE consent; accept the banner and read the requests again — that one origin and no other third party; open a page with ?probe=1#frag in its address, accept, and read the collect request's dl and dr (the path with no query and no fragment); on /privacy/ decline after accepting and check that no further collect request follows; and in the GA4 property's admin check that Enhanced measurement is off (or reduced to page views) and Google signals is off. Paste back: the host list per page, the origins of anything that is not the canonical host, the analytics requests with the banner's state at each of those points, the dl and dr values, and the two admin settings"
  MANUAL_3="still on /privacy/ in the same browser, open the developer tools' Application (Chrome/Edge) or Storage (Firefox/Safari) panel, Cookies, and the site's own origin: accept analytics, reload, and write down the tag's cookie NAMES and their Path and Domain — the names only, because a cookie's value is an identifier and is never pasted anywhere — then press Decline on that page and read the list again. Paste back: the names with Path and Domain before the withdrawal, and whether every one of them is gone after it. The production host is the apex $HOST, which has no parent domain, so the host-scope deletion is the whole of it here; this is the step no hermetic test can take, because the site's own test runs its file against a stub jar and settles what the code asks a browser to do rather than what the browser does with it"
  MANUAL_4="Check 69c (the published ranges are real) is Task W2.3's and is not in docs/gcp-live-checklist.md yet — the register's own paragraph says so — so no step of it is scripted or prompted here. When its section lands, its scriptable half belongs in this script rather than beside it, and this line then carries its browser half"
}

# ------------------------------------------------------------------- the plan
# print_plan: every command a real run would make, in order, and what its Restore
# would remove. Printed by --dry-run; nothing here is run.
print_plan() {
  say "dry run: NOTHING below was run, and no request left this machine. A real run would run, in this order:"
  say "  [network] curl -sS -L -o /dev/null -w '%{scheme} %{http_code} %{remote_ip} %{ssl_verify_result} %{url_effective}' $C — 69a's step 1, over the origin read from $SITE_YAML"
  say "  [network] curl -sS -I $C — the response's first lines, which step 1 prints beside the status"
  say "  [network] echo | openssl s_client -connect ${HOST}:443 -servername ${HOST} | openssl x509 -noout -subject -issuer -dates — the certificate, and the same pipeline with -ext subjectAltName for the names it carries"
  say "  [network, reads] curl -sS -L -H 'Accept-Encoding: identity' <page> into \$SCRATCH for each of $C/ , $C/console/ , $C/faq and $C/docs/, asserting 200 on the canonical host"
  say "  [local, writes] cd $SITE_DIR && npm run build — the artifact the deployed home page has to BE (69a's step 2). The build's own output goes to the scratch directory's log, and its result, $SITE_DIR/dist/index.html, is left in place"
  say "  [local, reads] sha256sum $SITE_DIR/dist/index.html against sha256sum of the deployed home page — the hash comparison that says the live page is this commit's build"
  say "  [local, reads] grep -niE '<video|<iframe|youtube|vimeo|poster=' over the deployed home page, and the same greps the register's step 3 runs over the console page"
  say "  [network, reads] one HEAD per download URL the two pages carry, following redirects, asserting 200"
  say "  [local, reads] each checksum printed beside a download link against $SUMS_FILE, the release's own list, keyed by the name GitHub serves"
  say "  [network, reads] one HEAD per distinct same-origin link the deployed home page carries, asserting 200 (69a's step 4)"
  say "  [local, reads] every absolute URL the markup of $C/ , $C/faq and $C/docs/ carries, against allowed_hosts (69b's step 4)"
  say "and its Restore (also from the exit trap) would remove: the scratch directory $SCRATCH and nothing else. This check changes nothing live — every call it makes is a read — and its only local write, $SITE_DIR/dist, is left in place because it is the artifact the facts above are about"
  say "NOT run by it, and printed as MANUAL lines for you: 69a's step 3 as a reader sees the page in a browser, and 69b's steps 1, 2 and 3 (the Network panel per page, the consent gate before and after a choice, and the cookies a withdrawal deletes)"
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. The MANUAL lines sit above the last line, which is the verdict.
BLOCK_KEYS=(site canonical belong site-facts https certificate pages build
  deployed-hash player downloads checksums links markup-origins restore)

# verdict_of FACTS: PASS unless one of the rows FAILed. A row that was never
# reached printed as FAIL by print_block is written into the facts by finish()
# as an abort, so a stopped run does not read as a pass.
verdict_of() { # verdict_of FACTS
  if printf '%s\n' "$1" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    printf 'FAIL\n'
  else
    printf 'PASS\n'
  fi
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 69 RESULT\n' >&3
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
  for key in MANUAL_1 MANUAL_2 MANUAL_3 MANUAL_4; do
    if [ -n "${!key:-}" ]; then
      printf 'MANUAL: %s\n' "${!key}" >&3
    fi
  done
  if [ -n "$DRY_RUN" ]; then
    # A dry run measured nothing about the site, and a PASS or a FAIL here would
    # be a verdict about a site it never fetched.
    printf 'CHECK 69 RESULT: NOT RUN (--dry-run: nothing was fetched and nothing was built, so nothing above is a verdict about the site)\n' >&3
  else
    printf 'CHECK 69 RESULT: %s\n' "$(verdict_of "$facts")" >&3
  fi
}

# interrupted: exit 130 and let the EXIT trap print the block and clean up.
interrupted() {
  owner "interrupted"
  exit 130
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# — including through the cleanup, which is where its second answer matters. A
# second Ctrl-C during the Restore would otherwise kill this shell mid-cleanup
# and the block that says what was left behind would never be printed.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  interrupted
}

# ----------------------------------------------------------------------- main
USAGE="check69.sh [--dry-run]"

parse_args() {
  DRY_RUN=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --dry-run) DRY_RUN=1 ;;
    -h | --help) HELP=1 ;;
    *) die "unknown argument '$1'. usage: $USAGE" ;;
    esac
    shift
  done
}

main() {
  need_tools
  parse_args "$@"
  if [ -n "$HELP" ]; then
    printf 'usage: %s\n' "$USAGE"
    return 0
  fi
  scratch_up
  guard_xtrace
  guard_site
  guard_canonical
  guard_belong
  local on_list=no verdict=FAIL
  if host_allowed "$HOST"; then
    on_list=yes
    verdict=PASS
  fi
  fact canonical "$verdict" \
    "the canonical origin is $C (host $HOST), read from $SITE_YAML's canonical_url and nothing else. The host is $([ "$on_list" = yes ] && echo on || echo NOT on) allowed_hosts, which the site's own boundary test holds the two values to — a canonical origin on a host the site does not admit itself is the site's own link rule broken at the first URL a reader follows"
  step_site_facts

  if [ -n "$DRY_RUN" ]; then
    print_plan
    step_manual
    # A dry run measured none of the site: each row a real run would fill says
    # so rather than reading FAIL — not reached, because a dry run did not stop,
    # it was asked for nothing.
    fact dry-run PASS "guards only: the site's own files were read, the canonical origin and every allowed host came out of $SITE_YAML, and every command a real run would make and every MANUAL line it would print were printed. No request left this machine and no build ran"
    local k
    for k in https certificate pages build deployed-hash player downloads checksums links markup-origins; do
      fact "$k" INFO "not measured: this is a dry run, so this row's reading is not here — the plan above says what a real run would do to fill it"
    done
    REACHED_END=yes
    return 0
  fi

  step_origin
  [ -z "$HALT" ] || { step_manual; REACHED_END=yes; return 0; }
  step_pages
  [ -z "$HALT" ] || { step_manual; REACHED_END=yes; return 0; }
  step_build
  [ -z "$HALT" ] || { step_manual; REACHED_END=yes; return 0; }

  step_hash
  step_player
  : >"$SCRATCH/dl"
  step_downloads
  step_links
  step_markup_origins
  step_manual

  REACHED_END=yes
  if printf '%s\n' "$(cat "$FACTS_FILE")" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    exit 1
  fi
  return 0
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
