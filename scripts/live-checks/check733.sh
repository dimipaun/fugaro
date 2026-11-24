#!/usr/bin/env bash
# Check 733 -- provider tokens, the device key, the share route and the synced
# keychain on two real phones.
#
# Task M0 of docs/mobile-identity-pairing/implementation-plan.md owes the result
# document this script drafts; the reconciliation's U4, U5, U7 and U10 are what
# it settles. The machine half reads the installation's paired devices
# (`fugaro pair ls --json`); everything else is on the phones. This script
# writes app-spike-DRAFT.md, a skeleton of app-spike.md with the paired devices
# named and a per-phone table (iPhone, Android) whose every answer reads
# UNVERIFIED until you fill it. It waits for no input.
#
# Usage:   bash scripts/live-checks/check733.sh [--out PATH]
#          bash scripts/live-checks/check733.sh --help
# Needs:   bash, jq, mktemp and fugaro (your own installation, signed in).
# Creates: the draft file only. Never overwrites: a default name that is taken
#          gets the next free number, and an explicit --out that exists is refused.
# Costs:   nothing billable; no model call. Prints no token, key or keychain
#          value: the device nodes hold none.
# Runs:    by YOU. No agent runs any step of it: the script refuses a coding
#          agent's session. Its test (check733_test.sh) uses fake tools only.
# Exit:    0 the draft was written (however many devices are paired, including
#          none); 1 the draft was written but `fugaro pair ls` could not be read;
#          2 the check could not run (agent session, a tool missing, a bad
#          argument, an --out that exists).
set -Eeuo pipefail
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

out=""

die() {
  echo "check733: $*" >&2
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
    die "${name} is set, so this is a coding agent's session: Check 733 is yours to run at your own terminal, and no agent runs any step of it"
  fi
done
unset CLAUDE_CODE_SSE_PORT

for tool in fugaro jq mktemp; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done

if [ -z "$out" ]; then
  out=./app-spike-DRAFT.md
  n=2
  while [ -e "$out" ]; do
    out=./app-spike-DRAFT-$n.md
    n=$((n + 1))
  done
elif [ -e "$out" ]; then
  die "$out already exists and this check never overwrites: pass another --out"
fi

umask 077
scratch=$(mktemp -d "${TMPDIR:-/tmp}/check733.XXXXXX")
trap 'rm -rf "$scratch"' EXIT

rc=0
fugaro pair ls --json >"$scratch/pair.json" || rc=$?
devices=$scratch/devices.jsonl
: >"$devices"
if [ "$rc" -eq 0 ]; then
  # The command prints one event whose list carries the devices; a bare array is read too.
  jq -c '(if type == "array" then . else (.list.devices // .devices // []) end)[]' "$scratch/pair.json" >"$devices" 2>/dev/null || rc=$?
fi
count=$(wc -l <"$devices" | tr -d ' ')

if [ "$rc" -eq 0 ]; then
  echo "paired devices: $count"
  jq -c '.' "$devices"
else
  echo "paired devices: could not be read (fugaro pair ls --json failed); the draft says so"
fi

{
  set -o noclobber
  {
    cat <<'MD'
# App spike (DRAFT -- Check 733, task M0)

Status: DRAFT. Every answer below is UNVERIFIED until you fill it; a question the spike could not answer stays UNVERIFIED.

## Paired devices (machine reading: `fugaro pair ls --json`)

MD
    if [ "$rc" -ne 0 ]; then
      echo "- The read failed (exit $rc): the devices are UNVERIFIED. Name the two phones by hand below."
    else
      echo "- Devices paired: $count"
      jq -r '"- \(.name // "(no name)") -- role \(.role // "?"), email \(.email // "?"), uid \(.uid // "?"), paired \(.at // "?")"' "$devices"
    fi
    cat <<'MD'

Which paired device is which phone: OWNER: FILL IN (UNVERIFIED)

MD
    for phone in iPhone Android; do
      cat <<MD
## $phone

- Device name (from the list above): OWNER: FILL IN

| Item | Question | Answer |
| --- | --- | --- |
| U4 | Google token accepted by \`signInWithIdp\`? Refusal if not | UNVERIFIED |
| U4 | Microsoft token accepted by \`signInWithIdp\`? Refusal if not | UNVERIFIED |
| U4 | Apple token accepted by \`signInWithIdp\`? Refusal if not | UNVERIFIED |
| U4 | Can the app hold two identities at once? | UNVERIFIED |
| U5 | Hardware-backed, non-exportable signing key in this stack? Else software, say so | UNVERIFIED |
| U7 | $( [ "$phone" = iPhone ] && echo "Share extension: listed in the sharesheet?" || echo "Intent filter: app listed in the share sheet?" ) | UNVERIFIED |
| U7 | $( [ "$phone" = iPhone ] && echo "Bundle id the extension arrives under" || echo "Package name it arrives under" ) | UNVERIFIED |
| U7 | What a text selection carries into the app | UNVERIFIED |
| U10 | Synced keychain works with the app's secure-store option? | UNVERIFIED |

MD
    done
    cat <<'MD'
## What waits on this

M1 to M3 (identity records, storing and refresh, restore), X3 and the packages the owner approves.
MD
  } >"$out"
} || die "could not write $out"

cat <<EOF

On each phone, with the spike build installed, fill that phone's table: sign in with each provider and see whether the exchange accepts it (U4), check whether the key is hardware-backed (U5), open the sharesheet with a text selection (U7), and read the synced-keychain behaviour with the secure-store option (U10). This script waits for none of it.

Draft written: $out
Next: send that file back, so an agent can write docs/mobile-identity-pairing/app-spike.md from it.
EOF
[ "$rc" -eq 0 ] || exit 1
