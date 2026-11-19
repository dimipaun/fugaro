# Sourced by scripts/release.sh and scripts/release-rehearsal.sh: the one
# reading of "this is a coding agent's session", so the release refuses the same
# thing in both places it has to. A release is the person's to make -- the tag
# it cuts is permanent and the pull request it opens merges the version bump to
# main -- and the catalog build its rehearsal submits is billable, so an agent's
# session neither submits that build nor cuts the tag.
#
# The markers are internal/agentenv's, and the rule is that package's Session:
# one of AgentMarkers, which an agent's own processes set and its tool calls
# inherit, whatever the terminal it is in; or IDEMarker, which the IDE
# extension's own integrated terminal sets for a person too, and so counts only
# where no real terminal is attached (a tool call has none). scripts/land-prs.sh
# spells the same check for a merge, in its own words, over the same five names.
#
# It is a courtesy against the accident and not a barrier (D1590,
# docs/roles-scopes/reconciliation.md): the markers are environment variables an
# agent can unset, so this stops the agent that did not mean to cut a release
# and does not stop one that means to.

# agent_session_marker prints the name of the variable that says this is a
# coding agent's session, and prints nothing at all when it is not one. It
# always succeeds, so a caller may read it with a plain command substitution
# under set -e.
agent_session_marker() {
  local name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      printf '%s\n' "$name"
      return 0
    fi
  done
  if [ -n "$(printenv CLAUDE_CODE_SSE_PORT 2>/dev/null || true)" ] && [ ! -t 0 ]; then
    printf '%s\n' CLAUDE_CODE_SSE_PORT
  fi
  return 0
}
