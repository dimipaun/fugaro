#!/usr/bin/env bash
# `--preflight`, shared by the live checks: what a check would need, read
# before anything is written or launched.
#
# A check that only ever ran on the owner's terminal surfaced one wrong
# assumption per run -- a flag the CLI does not take, a secret with no version,
# a repository already enrolled, a job on the wrong image. `--preflight` reads
# what the check would read (config, `fugaro doctor`, `fugaro ... ls --json`,
# `gcloud describe`/`list`, secret VERSION COUNTS and never values), evaluates
# every precondition the check has, prints ALL of them at once -- one laconic
# line each, with the exact command that fixes it, every value filled in from
# what it read -- and writes and launches nothing. It exits non-zero when
# anything blocks, so it can gate a real run.
#
# A check uses it like this:
#
#   . "$LIVE_CHECK_LIB/preflight.sh"
#   ...
#   step_preflight() {                       # reads only, no write, no launch
#     preflight_block installation "fugaro doctor --all did not answer" \
#       "fugaro doctor --project $PROJECT --all"
#     preflight_note accounts "$(count) credentials declared (the write door needs one of each path)"
#   }
#   ...
#   if [ -n "$PREFLIGHT" ]; then step_preflight; preflight_report; fi
#
# A blocker's fix is a real command with real values in it; a line with a
# `<placeholder>` in it is a defect, not a preflight.
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}

PREFLIGHT_BLOCKS=""
PREFLIGHT_NOTES=""
PREFLIGHT_TOOL=${PREFLIGHT_TOOL:-a live check}

# preflight_block KEY LINE FIX -- a precondition that stops the run, and the
# one command that clears it.
preflight_block() { # preflight_block KEY LINE FIX
  PREFLIGHT_BLOCKS="${PREFLIGHT_BLOCKS}${PREFLIGHT_BLOCKS:+
}$1|$2|$3"
}

# preflight_note KEY LINE -- what it read, when nothing blocks on it.
preflight_note() { # preflight_note KEY LINE
  PREFLIGHT_NOTES="${PREFLIGHT_NOTES}${PREFLIGHT_NOTES:+
}$1|$2"
}

# preflight_agent_session -- the same refusal the checks make, made here too:
# the live reads a preflight makes are the owner's, not a coding agent's.
preflight_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    printf '%s: %s is set, so this is a coding agent'"'"'s session: --preflight reads the live installation at your own terminal like the check itself, and no agent runs it\n' "$PREFLIGHT_TOOL" "$marker" >&2
    exit 2
  fi
}

# preflight_report -- print every blocker (and then the notes it read), one line
# each, and exit non-zero when anything blocked. Nothing was written.
preflight_report() {
  local key line fix
  if [ -n "$PREFLIGHT_NOTES" ]; then
    while IFS='|' read -r key line; do
      [ -n "$key" ] || continue
      printf '%s: read: %s\n' "$PREFLIGHT_TOOL" "$line" >&2
    done <<<"$PREFLIGHT_NOTES"
  fi
  if [ -z "$PREFLIGHT_BLOCKS" ]; then
    printf '%s: preflight passed: every precondition holds, and nothing was written or launched\n' "$PREFLIGHT_TOOL" >&2
    exit 0
  fi
  local n=0
  while IFS='|' read -r key line fix; do
    [ -n "$key" ] || continue
    n=$((n + 1))
    printf '%s: BLOCKED %s: %s\n' "$PREFLIGHT_TOOL" "$key" "$line" >&2
    printf '%s:   fix: %s\n' "$PREFLIGHT_TOOL" "$fix" >&2
  done <<<"$PREFLIGHT_BLOCKS"
  printf '%s: preflight found %d blocker(s); nothing was written or launched\n' "$PREFLIGHT_TOOL" "$n" >&2
  exit 1
}
