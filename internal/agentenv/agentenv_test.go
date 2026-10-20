package agentenv_test

import (
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
