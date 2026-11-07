// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// This file is the registry's own tests: what it serves, what it refuses to
// register, and the one path a handler is reached by. The door's tests are
// resource_test.go's.

// testSchema is the input schema the registrations below carry. Its content is
// not the point of any test here — that a tool has one is.
var testSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// TestToolsListNamesBothSetsOnce is D931: one endpoint, one tools/list, and
// both tool sets under the names they already have. It is checked through the
// registry rather than through a document so that the count is a property of
// the source: what a client is told it can call is what was registered, once
// each.
//
// The second half is D936's deferred pair. spawn_task and cancel_task are in
// the catalogue — their scope is decided — and nothing registers them, so they
// are not listed: a client that read them out of a document and called one
// would get a refusal, and tools/list is the one place a client learns what it
// may call.
func TestToolsListNamesBothSetsOnce(t *testing.T) {
	ownerSet := []string{"spawn_task", "get_task_status", "stream_logs", "get_result", "cancel_task", "list_tasks"}
	coordinatorSet := []string{"report_intent", "check_conflicts", "get_project_state", "get_release_status", "record_override"}

	r := NewRegistry()
	for _, name := range append(append([]string{}, ownerSet...), coordinatorSet...) {
		if err := r.Register(Tool{Name: name, InputSchema: testSchema, Handler: func(context.Context, Call) (any, error) { return name, nil }}); err != nil {
			t.Fatalf("registering %q: %v", name, err)
		}
	}

	listed := r.List()
	names := make([]string, 0, len(listed))
	for _, d := range listed {
		names = append(names, d.Name)
		if d.Description == "" {
			t.Errorf("%s is listed with no description: a client decides to call a tool from the line tools/list shows", d.Name)
		}
		if len(d.InputSchema) == 0 {
			t.Errorf("%s is listed with no input schema: a client renders the form it calls the tool with from this", d.Name)
		}
	}
	if len(names) != len(ownerSet)+len(coordinatorSet) {
		t.Fatalf("tools/list names %d tools (%v); want the owner's %d and the coordinator's %d, once each", len(names), names, len(ownerSet), len(coordinatorSet))
	}
	for _, name := range append(append([]string{}, ownerSet...), coordinatorSet...) {
		if got := countOf(names, name); got != 1 {
			t.Errorf("%s is listed %d times (%v); one name is one tool", name, got, names)
		}
	}
	if !slices.Equal(names, append(append([]string{}, ownerSet...), coordinatorSet...)) {
		t.Errorf("tools/list names %v; want the registration order, which is the order a client's cached answer is compared against", names)
	}

	// The deferred pair: their scope is decided and nothing serves them, so a
	// registry that registered only the rest lists only the rest.
	deferred := NewRegistry()
	served := []string{"get_task_status", "stream_logs", "get_result", "list_tasks"}
	for _, name := range served {
		if err := deferred.Register(Tool{Name: name, InputSchema: testSchema, Handler: func(context.Context, Call) (any, error) { return nil, nil }}); err != nil {
			t.Fatalf("registering %q: %v", name, err)
		}
	}
	got := deferred.List()
	if len(got) != len(served) {
		t.Fatalf("tools/list names %d tools; want the %d that were registered", len(got), len(served))
	}
	for _, d := range got {
		if d.Name == "spawn_task" || d.Name == "cancel_task" {
			t.Errorf("%s is listed although nothing registered a handler for it: D936 deferred the two tools that start and stop work, and tools/list is where a client learns what it may call", d.Name)
		}
	}
}

func countOf(names []string, name string) int {
	n := 0
	for _, got := range names {
		if got == name {
			n++
		}
	}
	return n
}

// TestAToolWithNoScopeIsRefused is finding M-e as code: a tool the catalogue
// does not map is refused at registration rather than served under a scope
// somebody assumed. The registration is the only door into the registry, so a
// name with no mapping is not reachable by a call either.
//
// The rest of the refusals here are the same rule seen from the other sides: a
// declaration that could not be served (no handler, no schema), a name
// registered twice, and a call the registry itself refuses — a name nothing
// serves, and a token that does not hold the tool's scope. That last one is the
// invariant the door rests on: no handler is reached without its scope, whatever
// a later caller does.
func TestAToolWithNoScopeIsRefused(t *testing.T) {
	ran := 0
	handler := func(context.Context, Call) (any, error) { ran++; return "ran", nil }

	t.Run("a name the catalogue does not map", func(t *testing.T) {
		r := NewRegistry()
		err := r.Register(Tool{Name: "roadmap_plan", InputSchema: testSchema, Handler: handler})
		if !errors.Is(err, ErrToolWithoutScope) {
			t.Fatalf("Register(roadmap_plan) = %v; want ErrToolWithoutScope: every tool maps to a scope and a tool with no mapping is refused (spec.md §5)", err)
		}
		if _, ok := ToolScope("roadmap_plan"); ok {
			t.Error("ToolScope maps a name the catalogue does not hold")
		}
		if got := r.List(); len(got) != 0 {
			t.Errorf("the refused tool is listed anyway: %+v", got)
		}
	})

	t.Run("a declaration that could not be served", func(t *testing.T) {
		for _, tc := range []struct {
			what string
			tool Tool
		}{
			{"no handler", Tool{Name: "get_project_state", InputSchema: testSchema}},
			{"no input schema", Tool{Name: "get_project_state", Handler: handler}},
			{"no name", Tool{InputSchema: testSchema, Handler: handler}},
			// A schema that is present and is not JSON is the one refusal here
			// that is not about a missing member: it would be written to the wire
			// with every other declaration, so the encoder would fail on the
			// whole answer and every client's tools/list with it — the ten sound
			// tools included. The row is the probe's own value.
			{"an input schema that is not JSON", Tool{Name: "get_project_state", InputSchema: json.RawMessage(`{"type":`), Handler: handler}},
		} {
			r := NewRegistry()
			if err := r.Register(tc.tool); !errors.Is(err, ErrToolIncomplete) {
				t.Errorf("Register(%s) = %v; want ErrToolIncomplete", tc.what, err)
			}
			if got := r.List(); len(got) != 0 {
				t.Errorf("a registry served %d tools after refusing %q: the tool is registered or it is not, and a refused one must not make the list unencodable", len(got), tc.what)
			}
		}
	})

	t.Run("one name registered twice", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(Tool{Name: "get_project_state", InputSchema: testSchema, Handler: handler}); err != nil {
			t.Fatalf("the first registration: %v", err)
		}
		err := r.Register(Tool{Name: "get_project_state", InputSchema: testSchema, Handler: handler})
		if !errors.Is(err, ErrToolNameTaken) {
			t.Fatalf("the second registration = %v; want ErrToolNameTaken: one name is one tool", err)
		}
		if got := r.List(); len(got) != 1 {
			t.Errorf("tools/list names %d tools after a refused second registration; want the one that was accepted", len(got))
		}
	})

	t.Run("the scope is the catalogue's, not the registration's", func(t *testing.T) {
		calls := 0
		write := func(context.Context, Call) (any, error) { calls++; return "ran", nil }
		r := NewRegistry()
		if err := r.Register(Tool{Name: "record_override", InputSchema: testSchema, Handler: write}); err != nil {
			t.Fatalf("registering a write tool: %v", err)
		}
		want := ScopeTasksWrite
		if scope, ok := ToolScope("record_override"); !ok || scope != want {
			t.Fatalf("ToolScope(record_override) = %q, %v; want %q", scope, ok, want)
		}
		// A token holding only tasks:read does not reach the handler.
		if _, err := r.Call(context.Background(), Call{Tool: "record_override", Scopes: []string{ScopeTasksRead}}); !errors.Is(err, ErrScopeNotHeld) {
			t.Errorf("Call with tasks:read = %v; want ErrScopeNotHeld", err)
		}
		if _, err := r.Call(context.Background(), Call{Tool: "record_override", Scopes: nil}); !errors.Is(err, ErrScopeNotHeld) {
			t.Errorf("Call with no scopes = %v; want ErrScopeNotHeld", err)
		}
		if calls != 0 {
			t.Errorf("the handler ran %d times for calls that did not hold its scope; want none: Call is the only path to a handler and it checks first", calls)
		}
		out, err := r.Call(context.Background(), Call{Tool: "record_override", Scopes: []string{ScopeTasksRead, ScopeTasksWrite}})
		if err != nil || out != "ran" || calls != 1 {
			t.Fatalf("Call with the tool's own scope = %v, %v (the handler ran %d times); want the handler's answer", out, err, calls)
		}
	})

	t.Run("a call to a name nothing serves", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(Tool{Name: "get_project_state", InputSchema: testSchema, Handler: handler}); err != nil {
			t.Fatalf("registering: %v", err)
		}
		// Mapped by the catalogue and not registered: D936's deferred pair is
		// exactly this shape at the door.
		for _, name := range []string{"spawn_task", "roadmap_plan"} {
			if _, err := r.Call(context.Background(), Call{Tool: name, Scopes: []string{ScopeTasksRead, ScopeTasksWrite}}); !errors.Is(err, ErrNoSuchTool) {
				t.Errorf("Call(%s) = %v; want ErrNoSuchTool", name, err)
			}
		}
	})

	t.Run("a tool's own failure is a refusal and not an answer", func(t *testing.T) {
		r := NewRegistry()
		err := r.Register(Tool{Name: "list_tasks", InputSchema: testSchema, Handler: func(context.Context, Call) (any, error) {
			return nil, errors.New("the run record is not readable")
		}})
		if err != nil {
			t.Fatalf("registering: %v", err)
		}
		_, err = r.Call(context.Background(), Call{Tool: "list_tasks", Scopes: []string{ScopeTasksRead}})
		if err == nil || !strings.Contains(err.Error(), "the run record is not readable") {
			t.Fatalf("Call = %v; want the handler's own refusal, which the door shows the client as the tool's answer", err)
		}
	})
}
