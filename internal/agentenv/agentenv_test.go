package agentenv_test

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// TestMarkerReportsTheFirstSetMarker: the refusal a caller prints names the
// variable the person can unset, and it is the first of the list, so the same
// session always reports the same name.
func TestMarkerReportsTheFirstSetMarker(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := agentenv.Marker(getenv); got != "" {
		t.Fatalf("Marker on an empty environment = %q, want none", got)
	}
	if agentenv.Present(getenv) {
		t.Error("Present on an empty environment = true, want false")
	}
	// Every marker is one, alone and last in the list as well as first: a
	// marker dropped from the list would let that session through.
	for _, want := range agentenv.Markers {
		env[want] = "1"
		if got := agentenv.Marker(getenv); got != want {
			t.Errorf("Marker with only %s set = %q, want %q", want, got, want)
		}
		if !agentenv.Present(getenv) {
			t.Errorf("Present with %s set = false, want true", want)
		}
		delete(env, want)
	}
	env["CLAUDE_CODE_SSE_PORT"] = "1"
	env["CURSOR_AGENT"] = "1"
	if got := agentenv.Marker(getenv); got != "CLAUDE_CODE_SSE_PORT" {
		t.Errorf("Marker with two set = %q, want the first of the list, CLAUDE_CODE_SSE_PORT", got)
	}
	// An empty value is not a set variable: a shell that exports CLAUDECODE=
	// is not a session.
	env = map[string]string{"CLAUDECODE": ""}
	if got := agentenv.Marker(getenv); got != "" {
		t.Errorf("Marker with CLAUDECODE empty = %q, want none", got)
	}
	if agentenv.Marker(nil) != "" {
		t.Error("Marker(nil) must be none, not a panic")
	}
}

// TestMarkersIsTheTwoKinds: the one list every caller loops over is exactly the
// two kinds this package classifies by — the agent-set markers and the IDE
// extension's one — so a marker added to one and not the other cannot slip
// past the refusals that loop over Markers.
func TestMarkersIsTheTwoKinds(t *testing.T) {
	want := append(append([]string{}, agentenv.AgentMarkers[:2]...), agentenv.IDEMarker)
	want = append(want, agentenv.AgentMarkers[2:]...)
	if !slices.Equal(agentenv.Markers, want) {
		t.Fatalf("Markers is %v, want the agent-set markers and the IDE marker in one list %v", agentenv.Markers, want)
	}
	if slices.Contains(agentenv.AgentMarkers, agentenv.IDEMarker) {
		t.Errorf("AgentMarkers holds %s: the IDE extension sets it in a person's own terminal too", agentenv.IDEMarker)
	}
}

// TestSessionIsTheMarkerThatMakesASessionAnAgents is the table of the one
// classification: an agent-set marker is an agent's session whatever the
// terminal, and the IDE extension's marker alone is one only without a real
// terminal on the run's input (a person's own IDE terminal is not an agent).
// The cases are named, and the two mutations that matter fail here: treating
// the IDE marker as accepted beside an agent-set marker (the "beside" rows),
// and dropping the terminal requirement (the "without a terminal" rows).
func TestSessionIsTheMarkerThatMakesASessionAnAgents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		terminal bool
		want     string
	}{
		{"no marker", nil, true, ""},
		{"no marker without a terminal", nil, false, ""},
		{"the IDE marker alone with a terminal is a person's", map[string]string{agentenv.IDEMarker: "1"}, true, ""},
		{"the IDE marker alone without a terminal is an agent's", map[string]string{agentenv.IDEMarker: "1"}, false, agentenv.IDEMarker},
		{"an agent-set marker alone with a terminal", map[string]string{"CLAUDECODE": "1"}, true, "CLAUDECODE"},
		{"an agent-set marker alone without a terminal", map[string]string{"AI_AGENT": "1"}, false, "AI_AGENT"},
		{"the IDE marker beside an agent-set marker, with a terminal", map[string]string{agentenv.IDEMarker: "1", "CURSOR_AGENT": "1"}, true, "CURSOR_AGENT"},
		{"the IDE marker beside an agent-set marker, without a terminal", map[string]string{agentenv.IDEMarker: "1", "CLAUDE_CODE_ENTRYPOINT": "1"}, false, "CLAUDE_CODE_ENTRYPOINT"},
		{"an empty IDE marker is not a set one", map[string]string{agentenv.IDEMarker: ""}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			if got := agentenv.Session(getenv, tc.terminal); got != tc.want {
				t.Errorf("Session(%v, terminal=%v) = %q, want %q", tc.env, tc.terminal, got, tc.want)
			}
			// Every agent-set marker is an agent's session alone, in either
			// terminal state: the loop reads the list under test so one dropped
			// from it is a case that stops being pinned.
			for _, k := range agentenv.AgentMarkers {
				one := func(s string) string {
					if s == k {
						return "1"
					}
					return ""
				}
				if got := agentenv.Session(one, true); got != k {
					t.Errorf("Session with only %s, terminal=true = %q, want %q", k, got, k)
				}
				if got := agentenv.Session(one, false); got != k {
					t.Errorf("Session with only %s, terminal=false = %q, want %q", k, got, k)
				}
			}
			if agentenv.Session(nil, true) != "" {
				t.Error("Session(nil) must be none, not a panic")
			}
		})
	}
}

// TestIDETerminalIsTheAcceptedCase pins the one case a caller prints the IDE
// notice for: the IDE marker alone, with a real terminal, and no agent-set
// marker. It is Session's "" together with the IDE marker being set, so a
// session with no marker at all is not one.
func TestIDETerminalIsTheAcceptedCase(t *testing.T) {
	ide := func(k string) string {
		if k == agentenv.IDEMarker {
			return "1"
		}
		return ""
	}
	if !agentenv.IDETerminal(ide, true) {
		t.Error("IDETerminal with the IDE marker alone and a terminal = false, want true")
	}
	if agentenv.IDETerminal(ide, false) {
		t.Error("IDETerminal with the IDE marker alone and no terminal = true, want false")
	}
	if agentenv.IDETerminal(func(string) string { return "" }, true) {
		t.Error("IDETerminal with no marker = true, want false")
	}
	both := func(k string) string {
		if k == agentenv.IDEMarker || k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}
	if agentenv.IDETerminal(both, true) {
		t.Error("IDETerminal with an agent-set marker beside the IDE marker = true, want false")
	}
}
