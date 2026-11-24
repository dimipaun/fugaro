#!/usr/bin/env bash
# Check 732 -- the operator's Claude Code session: context fullness, an injected
# `/compact`, the auto-compact threshold and the usage limits.
#
# Task Z0 of docs/mobile-identity-pairing/implementation-plan.md owes the result
# document this script drafts; the reconciliation's U1, U2 and U3 are what it
# settles. The machine half is ONE read: the five-hour and weekly usage windows
# for your own session's token (U3). U1 and U2 are observations in your own
# Claude Code session that no script can take, so this script prints the exact
# steps and writes context-spike-DRAFT.md, a skeleton of context-spike.md with
# the machine reading filled in and U1 and U2 left as marked fields for you.
# It waits for no input.
#
# Usage:   export CLAUDE_CODE_OAUTH_TOKEN=...   # your own login's token
#          bash scripts/live-checks/check732.sh [--out PATH]
#          bash scripts/live-checks/check732.sh --help
# Needs:   bash, curl, jq, mktemp.
# Creates: the draft file only. Never overwrites: a default name that is taken
#          gets the next free number, and an explicit --out that exists is refused.
# Costs:   nothing billable; no model call.
# Secret:  the token is read from CLAUDE_CODE_OAUTH_TOKEN only. It is never
#          printed, logged or put on a command line: it reaches curl through a
#          --config file (mode 0600, removed on exit) written by a shell builtin.
# Runs:    by YOU. No agent runs any step of it: the script refuses a coding
#          agent's session. Its test (check732_test.sh) uses fake tools only.
# Exit:    0 the draft was written; 2 the check could not run (agent session,
#          no token, a tool missing, a bad argument, an --out that exists).
set -Eeuo pipefail
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

USAGE_URL=${CHECK732_USAGE_URL:-https://api.anthropic.com/api/oauth/usage}
out=""

die() {
  echo "check732: $*" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
  --out)
    [ $# -ge 2 ] || die "--out needs a path"
    out=$2
    shift 2
    ;;
  --out=*)
    out=${1#--out=}
    shift
    ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
  if [ -n "${!name:-}" ]; then
    die "${name} is set, so this is a coding agent's session: Check 732 is yours to run at your own terminal, and no agent runs any step of it"
  fi
done
unset CLAUDE_CODE_SSE_PORT

[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] || die "CLAUDE_CODE_OAUTH_TOKEN is not set: export your own login's token in this shell, then run this again"
for tool in curl jq mktemp; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done

if [ -z "$out" ]; then
  out=./context-spike-DRAFT.md
  n=2
  while [ -e "$out" ]; do
    out=./context-spike-DRAFT-$n.md
    n=$((n + 1))
  done
elif [ -e "$out" ]; then
  die "$out already exists and this check never overwrites: pass another --out"
fi

umask 077
scratch=$(mktemp -d "${TMPDIR:-/tmp}/check732.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
cfg=$scratch/curl.cfg
body=$scratch/body
printf 'header = "Authorization: Bearer %s"\n' "$CLAUDE_CODE_OAUTH_TOKEN" >"$cfg"

status=$(curl -sS --config "$cfg" -o "$body" -w '%{http_code}' "$USAGE_URL" 2>/dev/null) || status=000
windows=""
reading="not readable by this route"
case "$status" in
2??)
  if windows=$(jq -c '{five_hour, seven_day}' "$body" 2>/dev/null) &&
    jq -e '(.five_hour != null) and (.seven_day != null)' <<<"$windows" >/dev/null 2>&1; then
    reading="readable"
  else
    windows=""
  fi
  ;;
esac

case "$reading" in
readable) echo "U3 machine reading: $windows" ;;
*) echo "U3 machine reading: not readable by this route (HTTP $status) -- that is U3's finding, not a failure of this check" ;;
esac

{
  set -o noclobber
  {
    cat <<'MD'
# Context spike (DRAFT -- Check 732, task Z0)

Status: DRAFT. Every field marked `OWNER: FILL IN` is UNVERIFIED until you fill it.

## U3: five-hour and weekly limits per token (machine reading)

MD
    printf -- '- Route: `GET %s` with the session token (token not recorded here)\n' "$USAGE_URL"
    printf -- '- HTTP status: %s\n' "$status"
    if [ "$reading" = readable ]; then
      printf -- '- Result: readable by this route\n- Windows: `%s`\n' "$windows"
    else
      printf -- '- Result: **not readable by this route** (a 401, a 403 or a document without the windows is U3'"'"'s finding)\n- Windows: none\n'
    fi
    cat <<'MD'
- Within Anthropic's terms to read this way? OWNER: FILL IN (UNVERIFIED)

## U1: context fullness, and an injected `/compact`

- What the screen / status line shows about how full the context is: OWNER: FILL IN (UNVERIFIED)
- Does a hook or status line receive the context fullness? OWNER: FILL IN (UNVERIFIED)
- `/compact` injected while the session was idle: honoured / queued / ignored: OWNER: FILL IN (UNVERIFIED)
- Another slash command injected while idle: honoured / queued / ignored: OWNER: FILL IN (UNVERIFIED)
- Evidence (what you typed, what you saw): OWNER: FILL IN

## U2: the auto-compact threshold

- Is the built-in threshold configurable? OWNER: FILL IN (UNVERIFIED)
- Where does that setting live (file, key, flag, env)? OWNER: FILL IN (UNVERIFIED)
- Evidence: OWNER: FILL IN

## What waits on this

Z1 (the gauge's source), Z2 (the compaction's mechanism) and Z3 (the meter's route).
MD
  } >"$out"
} || die "could not write $out"

cat <<EOF

Steps for U1 and U2, in your own Claude Code session (this script waits for none of them):
  U1a. In an idle Claude Code session, look at the screen and the status line: note what, if anything, says how full the context is.
  U1b. Run /context (or whatever your build offers) and note what it reports.
  U1c. With nothing running, inject /compact (type it, or send it the way the app would) and note whether it was honoured at once, queued behind output, or ignored. Repeat with one other slash command.
  U2a. Look in Claude Code's settings (settings.json, the /config screen, the documentation for auto-compact): is the auto-compact threshold configurable?
  U2b. If it is, note the exact file and key (or flag or env var) that sets it.
Fill the OWNER: FILL IN fields in the draft with what you saw.

Draft written: $out
Next: send that file back, so an agent can write docs/mobile-identity-pairing/context-spike.md from it.
EOF
