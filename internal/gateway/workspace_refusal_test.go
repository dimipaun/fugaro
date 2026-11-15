package gateway

// The marker a workspace's credentials service puts on the refusals it answers
// on the workspace's own account, at the member's end (docs/workspaces/spec.md
// §5 and §9, D2022's O19). What these tests hold is that the marker is READ off
// the refusal the service already sends, that it lands on the halt the refusal
// forces, and that it is the ONLY thing it changes: a refusal with no marker
// halts exactly as it did before the field existed, and a marker this release
// does not know is no marker at all.
//
// The service's own decision is not here and is not touched by any of this: the
// refusal is the service's, the cap and the stop stay where they are
// (TestWorkspaceCapRefusesTheCallThatWouldExceedIt, internal/credsvc), and what
// these tests add is that the member's run can say in its own record who
// refused it (internal/runstore's Halt.WorkspaceRefusal).

import (
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// wsRefusalBody is the refusal a workspace's service answers: the contract's
// own state_unavailable, which is the type the workspace's stop answers with
// and the type a member's own service answers with when it cannot read its
// state. The two are the same body, which is exactly why the marker beside it
// is what tells a reader which service refused the run (U12).
const wsRefusalBody = `{"error":"state_unavailable"}`

// wsRefusalAnswer is that refusal with the workspace's marker on it, as the
// service writes the two together.
func wsRefusalAnswer(reason string) anthropicfake.Reply {
	hdr := http.Header{}
	if reason != "" {
		hdr.Set(WorkspaceRefusalHeader, reason)
	}
	return anthropicfake.Reply{Status: http.StatusServiceUnavailable, Body: wsRefusalBody, Header: hdr}
}

// wsRefusalHalt drives one refused call through the gateway and returns the
// halt it forced. Every test here reads the halt off the run's own channel, so
// what is held is what the runner is handed and not what a helper believes.
func wsRefusalHalt(t *testing.T, reason string) Halt {
	t.Helper()
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return wsRefusalAnswer(reason)
	}, nil)
	resp, _ := h.post(msg(sonnet, 64))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the refused call answered %d, want the contract's 403 to the agent", resp.StatusCode)
	}
	halt, ok := h.halted()
	if !ok {
		t.Fatalf("a refusal the service answered (%s) halted nothing", reason)
	}
	return halt
}

// Each of the three refusals the workspace's service answers on its own account
// is named on the halt the refusal forces, and each leaves the halt's reason,
// scope and detail exactly what they are for a refusal with no marker: the
// field says who refused and never why the run stopped.
func TestWorkspaceRefusalMarksTheHaltAndChangesNothingElse(t *testing.T) {
	plain := wsRefusalHalt(t, "")
	if plain.WorkspaceRefusal != "" {
		t.Fatalf("a refusal carrying no marker was marked %q", plain.WorkspaceRefusal)
	}
	if plain.Reason != ReasonBudgetUnavailable || plain.Scope != "run" {
		t.Fatalf("the unmarked refusal halted %s/%s, want %s/run", plain.Reason, plain.Scope, ReasonBudgetUnavailable)
	}

	for _, reason := range WorkspaceRefusals() {
		halt := wsRefusalHalt(t, reason)
		if halt.WorkspaceRefusal != reason {
			t.Errorf("the halt for the workspace's %s refusal carries %q, want %q", reason, halt.WorkspaceRefusal, reason)
		}
		if halt.Reason != plain.Reason || halt.Scope != plain.Scope || halt.Detail != plain.Detail {
			t.Errorf("the workspace's %s refusal changed the halt itself: %+v, want %s/%s with the detail %q",
				reason, halt, plain.Reason, plain.Scope, plain.Detail)
		}
	}
}

// A marker that is not one of the three is not a refusal this contract could
// have written, and it is read as no marker: a value a later release invents
// must not be able to make an older run's record say the workspace refused it.
// The answer's own text is not a marker either, in any of the places a service
// states it.
func TestOnlyTheThreeRefusalsAreReadAsAMarker(t *testing.T) {
	for _, v := range []string{"", "STOP", "Stop", "stop ", " stopped", "capped", "budget_unavailable", "state_unavailable"} {
		if got, ok := WorkspaceRefusal(http.Header{WorkspaceRefusalHeader: []string{v}}); ok || got != "" {
			t.Errorf("the wire read %q as the workspace's %q refusal, want no marker", v, got)
		}
	}
	for _, reason := range WorkspaceRefusals() {
		if !ValidWorkspaceRefusal(reason) {
			t.Errorf("%q is one of WorkspaceRefusals() and ValidWorkspaceRefusal refuses it", reason)
		}
		got, ok := WorkspaceRefusal(http.Header{WorkspaceRefusalHeader: []string{reason}})
		if !ok || got != reason {
			t.Errorf("the wire read %q as (%q, %v), want (%q, true)", reason, got, ok, reason)
		}
	}
}

// The marker is between the two binaries, so the agent is handed no part of it:
// it is dropped from every answer copied on, as the headroom is. A marker on an
// answer that is not a refusal marks nothing either -- a served call is not a
// refusal, whatever a service wrote beside it.
func TestWorkspaceRefusalNeverReachesTheAgentAndMarksNoServedCall(t *testing.T) {
	if !dropResponseHeaders[WorkspaceRefusalHeader] {
		t.Errorf("%s is not in dropResponseHeaders: the agent would be told which of the workspace's refusals its own service answered", WorkspaceRefusalHeader)
	}

	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		rep := serviceOK(TierA, "cred-1")
		rep.Header.Set(WorkspaceRefusalHeader, WorkspaceRefusalStop)
		return rep
	}, nil)
	resp, _ := h.post(msg(sonnet, 64))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the served call answered %d, want the model's own answer", resp.StatusCode)
	}
	if v := resp.Header.Get(WorkspaceRefusalHeader); v != "" {
		t.Errorf("the agent's own answer carried %s: %s", WorkspaceRefusalHeader, v)
	}
	noHalt(t, h)
}
