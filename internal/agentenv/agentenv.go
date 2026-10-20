// Package agentenv names the environment variables a coding agent's session
// sets. It is the one list: `fugaro init`'s and `fugaro image refresh`'s gates
// refuse to apply anything with one of them present, and the owner-run live
// helpers in internal/e2e refuse to run at all, because a check that writes to
// a real installation is the owner's, at the owner's terminal, and not the
// agent's that wrote it (credentials-service plan F33 and D146: "every live
// step is owner-run, on installation `fugaro` and a sandbox repository only,
// never `belong`"; `agentMarker` refuses live applies inside a coding agent).
//
// It is a mitigation, not a barrier: an agent that unsets its own variables is
// not stopped, and the real controls are the typed confirmations and the
// pins each helper hard-codes. What it stops is the accident — an agent
// running a live check because the variables happened to be in its session.
package agentenv

// Markers are the environment variables a coding agent's session sets. With
// any of them present, a live apply or a live check refuses. CLAUDE_CODE_SSE_PORT
// is also set by the Claude Code IDE extension in the integrated terminals of
// VS Code and JetBrains, so a person's own IDE terminal is refused too: only
// for that marker should a refusal tell the person (not the agent) what to do
// about it, which is what the callers' messages do.
var Markers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}

// Marker is the first marker getenv shows set, or "" with none: a coding
// agent's session. The order is Markers', so the name a refusal reports is
// stable.
func Marker(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	for _, k := range Markers {
		if getenv(k) != "" {
			return k
		}
	}
	return ""
}

// Present reports whether getenv shows a coding agent's session.
func Present(getenv func(string) string) bool { return Marker(getenv) != "" }
