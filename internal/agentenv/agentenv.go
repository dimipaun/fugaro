// Package agentenv names the environment variables a coding agent's session
// sets. It is the one list: `fugaro update`'s and `fugaro image refresh`'s gates
// refuse to apply anything with one of them present, and the owner-run live
// credentials-rules check (internal/e2e, TestLiveCredsRules) refuses to run at
// all, because a check that writes to a real installation is the owner's, at
// the owner's terminal, and not the agent's that wrote it (credentials-service
// plan F33 and D146: "every live step is owner-run, on installation `fugaro`
// and a sandbox repository only, never `belong`"; `agentMarker` refuses live
// applies inside a coding agent).
//
// That check is the only helper in internal/e2e that reads this list. The
// others (the spend helper of check 21, the gateway helper of check 20 and the
// gcp ones) gate on their own explicit opt-in variable and do not look at the
// markers, so this package's name is not a claim about them.
//
// It is a mitigation, not a barrier: an agent that unsets its own variables is
// not stopped, and the real controls are the typed confirmations and the
// pins each helper hard-codes. What it stops is the accident — an agent
// running a live check because the variables happened to be in its session.
//
// The markers are two kinds, and the kind is why a session is judged by more
// than "a marker is set". AgentMarkers are set in the agent's own processes and
// inherited by the tool calls it makes: any one of them means the session is an
// agent's, whatever else is true of it. IDEMarker is set by the Claude Code IDE
// extension in the integrated terminals of VS Code and JetBrains, which is a
// person's own terminal as much as an agent's: alone, and only with a real
// terminal on the run's input, it is a person's IDE terminal and not a coding
// agent's session (Session). An agent's tool call has no terminal, so the IDE
// marker alone still refuses there, and an agent-set marker refuses in an IDE
// terminal exactly as it does anywhere else.
package agentenv

// AgentMarkers are the environment variables set in a coding agent's own
// processes. Its tool calls inherit them, so any one of them present means the
// session is the agent's, at a terminal or not: keep refusing on each.
var AgentMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}

// IDEMarker is the one marker the Claude Code IDE extension also sets in a
// person's own integrated terminal (VS Code, JetBrains), and nowhere else. It
// says "this terminal belongs to an IDE with the extension installed", not
// "a coding agent is typing": it is a coding agent's session only without a
// real terminal on the run's input (an agent's tool call has none), which is
// what Session decides.
const IDEMarker = "CLAUDE_CODE_SSE_PORT"

// Markers are every marker, agent-set and IDE alike, in the order a refusal
// reports them. It is the one list a caller loops over — the completeness tests
// and the e2e guard's own loop read it — so a marker the tree adds is one every
// such caller sees; which kind it is is AgentMarkers and IDEMarker.
var Markers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}

// Marker is the first marker getenv shows set, or "" with none: the variable a
// refusal names, whether it is an agent-set marker or the IDE extension's. The
// order is Markers', so the name a refusal reports is stable.
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

// Session is the marker that makes getenv a coding agent's session, or "" when
// it is a person's, given whether the run has a real terminal on its input.
//
// An agent-set marker (AgentMarkers) is one whatever the terminal, because the
// agent's own processes set it and its tool calls inherit it. IDEMarker alone
// is one only without a real terminal: the IDE extension sets it in a person's
// integrated terminal too, and a person typing there is not an agent. An
// agent-set marker beside IDEMarker is the agent's case, and this reports the
// agent-set one, so a refusal names the variable that actually means an agent
// rather than the IDE marker it shares the environment with.
func Session(getenv func(string) string, terminal bool) string {
	if getenv == nil {
		return ""
	}
	for _, k := range AgentMarkers {
		if getenv(k) != "" {
			return k
		}
	}
	if !terminal && getenv(IDEMarker) != "" {
		return IDEMarker
	}
	return ""
}

// IDETerminal reports whether getenv is a person's own IDE terminal: the IDE
// extension's marker with no agent-set one, and a real terminal on the run's
// input. It is the one case a typed confirmation is accepted from, and the one
// case a caller prints the IDE notice for: Session's "" here is the IDE marker
// being read as a person's terminal rather than as no marker at all.
func IDETerminal(getenv func(string) string, terminal bool) bool {
	return terminal && getenv != nil && getenv(IDEMarker) != "" && Session(getenv, terminal) == ""
}

// Present reports whether getenv shows any marker at all, agent-set or IDE. It
// is the raw question — a marker is set — and not Session's: a caller asking
// whether the SESSION is an agent's asks Session.
func Present(getenv func(string) string) bool { return Marker(getenv) != "" }
