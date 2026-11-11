// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

// The orchestrator and local scopes (CG5 of
// docs/cli-output-console-graph/implementation-plan-graph.md): one
// orchestrator's state node as nodes and edges, and what the scope says when it
// has no node to read.
//
// What each test holds:
//
//   - TestOrchestratorTasksAndEdges: a work item is a `task` node keyed
//     `<source>:<id>`, its phase is the node's state, and its `depends_on` are
//     the document's edges (F14, F18) — the source whose graph has edges at all.
//   - TestDependsOnEdgesStayInsideOneOrchestrator: a dependency the node does
//     not carry draws nothing, because an edge to a node the document does not
//     hold is not one a reader can draw (contract §6.22).
//   - TestLocalScopeFallsBackToTheCache: a local scope whose node cannot be read
//     answers with the cached brief's own stamp as its age and no nodes — a
//     brief is not a graph (D1358, T5(a)).
//   - TestUnknownOrchestratorIsRefused: an id neither the database nor this
//     machine has is ErrNoOrchestrator, the one failure of this source that is a
//     refusal rather than a state (H3, §6.22).
//
// The source is pure, so every one of these is a struct literal and a returned
// value: no fake, no clock and no I/O.

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// stateOf encodes the fixture state the way the daemon writes it, which is the
// reader's own contract: the source decodes what this writes (EncodeState), so
// a fixture built by hand and a node written by a daemon reach it identically.
func stateOf(t *testing.T, id string, items ...orchestrator.WorkItem) []byte {
	t.Helper()
	raw, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       id,
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     items,
	})
	if err != nil {
		t.Fatalf("the fixture state is not one this release writes: %v", err)
	}
	return raw
}

// attempted is one item with the attempts the intents against it committed: the
// state's own validator counts them, so a fixture that carries an intent for
// attempt 2 says so on the item.
func attempted(w orchestrator.WorkItem, n int) orchestrator.WorkItem {
	w.Attempts = n
	return w
}

// item is one work item of the fixture: a queued task of the fixture's
// repository, with a brief so the state's own validator accepts it.
func item(id, title, repo string, deps ...string) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID:        id,
		Title:     title,
		Repo:      repo,
		Recipe:    "standard",
		RigorTier: "medium",
		Brief:     "The brief of " + id + ".",
		DependsOn: deps,
		Phase:     orchestrator.PhaseQueued,
	}
}

// TestOrchestratorTasksAndEdges: one orchestrator's state node is a scope whose
// nodes are its work items and whose edges are their `depends_on`, which is what
// makes this source the first one that draws a graph rather than a list (F14,
// F17, F18).
//
// The identities are the point of the test as much as the shapes are: every key
// is `<source>:<id>` built from the scope's own name (D1356), the scope's state
// is up, and a node's `updated` is the newest intent committed against it and
// null for an item nothing has been tried on — never the source's own read time,
// which is not a fact about the work.
func TestOrchestratorTasksAndEdges(t *testing.T) {
	approved := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	earlier := approved.Add(-time.Hour)
	items := []orchestrator.WorkItem{
		attempted(item("wi-1", "land the state migration", "acme/app"), 1),
		attempted(item("wi-2", "move the sweep onto the daemon", "acme/app", "wi-1"), 2),
		item("wi-3", "read the cache offline", "acme/lib", "wi-1", "wi-2"),
	}
	committed := func(w orchestrator.WorkItem, attempt int, runID string, at time.Time) orchestrator.Intent {
		return orchestrator.Intent{ID: w.ID + ":" + strconv.Itoa(attempt), RunID: runID,
			SpecDigest: orchestrator.SpecDigest(w), ApprovalAt: at, At: at}
	}
	state := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       "o1",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     items,
		Intents: []orchestrator.Intent{
			// The newest intent is not the one the state lists last: an item's
			// age is the last thing committed against it, whatever the order.
			committed(items[1], 1, "20261109-100000-aaaa", earlier),
			committed(items[1], 2, "20261109-110000-bbbb", approved),
			committed(items[0], 1, "20261109-090000-cccc", earlier),
		},
	}
	raw, err := orchestrator.EncodeState(state)
	if err != nil {
		t.Fatalf("the fixture state is not one this release writes: %v", err)
	}

	src := OrchestratorSource{ID: "o1", Node: raw}
	nodes, edges, st, err := src.Nodes()
	if err != nil {
		t.Fatalf("reading the scope: %v", err)
	}
	if st.Source != "orchestrator:o1" || st.State != SourceUp {
		t.Fatalf("the scope is %+v, want orchestrator:o1 up", st)
	}
	if st.AsOf != nil {
		t.Errorf("the scope carries as_of %v, want null: the node carries no time of its own", st.AsOf)
	}

	want := []Node{
		{Key: "orchestrator:o1:wi-1", Source: "orchestrator:o1", Kind: NodeTask,
			ID: "wi-1", Title: "land the state migration", Repo: "acme/app", State: "queued", Updated: &earlier},
		{Key: "orchestrator:o1:wi-2", Source: "orchestrator:o1", Kind: NodeTask,
			ID: "wi-2", Title: "move the sweep onto the daemon", Repo: "acme/app", State: "queued", Updated: &approved},
		{Key: "orchestrator:o1:wi-3", Source: "orchestrator:o1", Kind: NodeTask,
			ID: "wi-3", Title: "read the cache offline", Repo: "acme/lib", State: "queued"},
	}
	if len(nodes) != len(want) {
		t.Fatalf("the scope drew %d nodes, want %d: %+v", len(nodes), len(want), nodes)
	}
	for i, w := range want {
		if !sameNode(nodes[i], w) {
			t.Errorf("node %d is %+v, want %+v", i, nodes[i], w)
		}
	}
	// The newest intent wins, and it is not the one the state lists last: an
	// item's age is the last thing committed against it.
	if nodes[1].Updated == nil || !nodes[1].Updated.Equal(approved) {
		t.Errorf("wi-2's updated is %v, want the newest intent's time %s", nodes[1].Updated, approved)
	}

	wantEdges := []Edge{
		{From: "orchestrator:o1:wi-2", To: "orchestrator:o1:wi-1", Kind: DependsOnEdge},
		{From: "orchestrator:o1:wi-3", To: "orchestrator:o1:wi-1", Kind: DependsOnEdge},
		{From: "orchestrator:o1:wi-3", To: "orchestrator:o1:wi-2", Kind: DependsOnEdge},
	}
	if len(edges) != len(wantEdges) {
		t.Fatalf("the scope drew %d edges, want %d: %+v", len(edges), len(wantEdges), edges)
	}
	for i, w := range wantEdges {
		if edges[i] != w {
			t.Errorf("edge %d is %+v, want %+v", i, edges[i], w)
		}
	}

	// The document these nodes belong to: the source half of every key is the
	// scope's own name, so a console sends back exactly what it read (§6.22).
	doc := Document{Version: DocumentVersion, Sources: []SourceState{st}, Nodes: nodes, Edges: edges}
	if err := doc.Sanitise(); err != nil {
		t.Fatalf("the document a scope filled is not fit to print: %v", err)
	}
	for _, n := range doc.Nodes {
		if n.Key != NodeKey(n.Source, n.ID) || n.Source != st.Source {
			t.Errorf("the node %+v is not keyed by the scope it came from", n)
		}
	}
}

// TestDependsOnEdgesStayInsideOneOrchestrator: every edge a scope draws has
// both of its ends among the nodes that same scope carries, and an edge never
// reaches into another orchestrator's key space — the ids are the node's own
// namespace, so two orchestrators can each carry a `wi-1` and their keys are
// still two keys (D1356, §6.22).
//
// A `depends_on` naming an item the node does not carry never reaches this
// reader at all: the state's own validator refuses a state whose item waits on
// an item it does not hold (ValidateState), so what this test holds is the
// property a reader relies on — both ends are keys of the document it is
// reading — rather than a shape the writer cannot produce.
func TestDependsOnEdgesStayInsideOneOrchestrator(t *testing.T) {
	itemOf := func(id, waitOn string) orchestrator.WorkItem {
		if waitOn == "" {
			return item(id, "the item everything waits on", "acme/app")
		}
		return item(id, "the item that waits", "acme/app", waitOn)
	}
	src := OrchestratorSource{ID: "o1", Node: stateOf(t, "o1", itemOf("wi-1", ""), itemOf("wi-2", "wi-1"))}
	nodes, edges, _, err := src.Nodes()
	if err != nil {
		t.Fatalf("reading the scope: %v", err)
	}
	if len(nodes) != 2 || len(edges) != 1 {
		t.Fatalf("the scope drew %d nodes and %d edges, want its two items and their one dependency", len(nodes), len(edges))
	}
	if edges[0] != (Edge{From: "orchestrator:o1:wi-2", To: "orchestrator:o1:wi-1", Kind: DependsOnEdge}) {
		t.Errorf("the edge is %+v, want wi-2 waiting on wi-1 under this scope's keys", edges[0])
	}
	keys := map[string]bool{}
	for _, n := range nodes {
		keys[n.Key] = true
	}
	for _, e := range edges {
		if !keys[e.From] || !keys[e.To] {
			t.Errorf("the edge %+v has an end that is not a node of the document: %v", e, keys)
		}
	}

	// The other half: the same item ids under another id's scope are other keys,
	// and that scope's edge points at its own node and never at this one's.
	other := OrchestratorSource{ID: "o2", Node: stateOf(t, "o2", itemOf("wi-1", ""), itemOf("wi-2", "wi-1"))}
	otherNodes, otherEdges, _, err := other.Nodes()
	if err != nil {
		t.Fatalf("reading the second scope: %v", err)
	}
	if len(otherEdges) != 1 || otherEdges[0] != (Edge{From: "orchestrator:o2:wi-2", To: "orchestrator:o2:wi-1", Kind: DependsOnEdge}) {
		t.Fatalf("the second scope's edges are %+v, want its own wi-2 -> wi-1", otherEdges)
	}
	if otherNodes[0].Key == nodes[0].Key {
		t.Errorf("two orchestrators' items share the key %q: the keys are unique within one source and never across (D1356)", otherNodes[0].Key)
	}
}

// TestLocalScopeFallsBackToTheCache: a local scope whose node cannot be read —
// the database unreachable, or the node gone — answers with the cached brief
// this machine's daemon wrote (D1358, T5(a)). What it draws with it is one state
// and an age and no nodes: the brief a session-start print reads is prose, not a
// graph, so a document of it has no work items to show and says so with an empty
// list rather than with the graph it had.
//
// The age is the cache's own stamp and never this machine's clock, which is what
// makes it mean something on a laptop whose clock is off (D1243).
func TestLocalScopeFallsBackToTheCache(t *testing.T) {
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

	t.Run("the node cannot be read", func(t *testing.T) {
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written,
			ReadErr: errors.New("the database is down")}
		nodes, edges, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("a local scope with a cache answered with a failure: %v", err)
		}
		if st.Source != "local:o1" || st.State != SourceDown {
			t.Fatalf("the scope is %+v, want local:o1 down", st)
		}
		if st.AsOf == nil || !st.AsOf.Equal(written) {
			t.Errorf("the scope's as_of is %v, want the cached brief's own stamp %s", st.AsOf, written)
		}
		if !strings.Contains(st.Reason, "cached brief") || !strings.Contains(st.Reason, written.Format(time.RFC3339)) {
			t.Errorf("the reason is %q, want it to name the cached brief and when it was written", st.Reason)
		}
		if len(nodes) != 0 || len(edges) != 0 {
			t.Errorf("the fallback drew %d nodes and %d edges, want none: a brief is not a graph", len(nodes), len(edges))
		}
	})

	t.Run("the node is gone", func(t *testing.T) {
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written}
		_, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("a local scope with a cache answered with a failure: %v", err)
		}
		if st.State != SourceDown || st.AsOf == nil || !st.AsOf.Equal(written) {
			t.Errorf("the scope is %+v, want it down with the cached brief's stamp", st)
		}
	})

	t.Run("a scope with no cache claims no age", func(t *testing.T) {
		// The `orchestrator:` kind is one only the database holds (CG4's rule),
		// so a failed read of it has nothing to fall back to: the state is down
		// and the age is null, because nothing here knows when its data was last
		// good (H3).
		src := OrchestratorSource{ID: "o1", ReadErr: errors.New("the database is down")}
		_, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("a scope whose node could not be read answered with a failure: %v", err)
		}
		if st.State != SourceDown || st.AsOf != nil {
			t.Errorf("the scope is %+v, want it down and claiming no age", st)
		}
		if !strings.Contains(st.Reason, "could not be read") {
			t.Errorf("the reason is %q, want the failure it met", st.Reason)
		}
	})

	t.Run("a readable node is up whatever the cache says", func(t *testing.T) {
		// A stale cache beside a live node is not staleness: what the scope
		// draws is the node, and the cache is only what it has when the node is
		// not there (D1358).
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written,
			Node: stateOf(t, "o1", item("wi-1", "a live item", "acme/app"))}
		nodes, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("reading the scope: %v", err)
		}
		if st.State != SourceUp || st.AsOf != nil {
			t.Errorf("the scope is %+v, want it up with no age", st)
		}
		if len(nodes) != 1 {
			t.Errorf("the scope drew %d nodes, want the node's one item", len(nodes))
		}
	})
}

// TestUnknownOrchestratorIsRefused: an id neither the project's database nor
// this machine holds is a scope that does not exist, and that is the one failure
// of this source that is a refusal rather than a state (H3, §6.22). A reader
// must be able to tell it from a scope that is merely down, which is why it is
// an error and not an empty document.
//
// It is the source's half of the command's refusal; the command is where it
// becomes exit 1 with a printed sentence, and the sentence's own hint — where to
// find the ids this installation has — is asserted here so that both ends of it
// stay one string.
func TestUnknownOrchestratorIsRefused(t *testing.T) {
	// Both kinds, and both shapes of "nothing here": an id the database does
	// not hold, and one this machine has no cached brief for either.
	for _, src := range []OrchestratorSource{
		{ID: "o1"},
		{ID: "o1", Local: true},
	} {
		nodes, edges, st, err := src.Nodes()
		if !errors.Is(err, ErrNoOrchestrator) {
			t.Errorf("a scope that does not exist (%+v) answered with %v, want ErrNoOrchestrator", src, err)
			continue
		}
		if !strings.Contains(err.Error(), src.Scope().String()) {
			t.Errorf("the refusal %q does not name the scope %s it refused", err, src.Scope())
		}
		if !strings.Contains(err.Error(), "fugaro orchestrator ls") {
			t.Errorf("the refusal %q does not say where the ids this installation has are listed", err)
		}
		if len(nodes) != 0 || len(edges) != 0 || st.State != "" {
			t.Errorf("the refused scope answered with nodes %+v, edges %+v and state %+v", nodes, edges, st)
		}
	}

	// The other kinds are not refused by this path: a scope with a node reads,
	// which is the control that keeps the assertions above from passing on a
	// source that refuses everything.
	src := OrchestratorSource{ID: "o1", Node: stateOf(t, "o1", item("wi-1", "a live item", "acme/app"))}
	if _, _, st, err := src.Nodes(); err != nil || st.State != SourceUp {
		t.Errorf("a scope with a node is %+v, %v, want it up", st, err)
	}
}

// sameNode compares two nodes field by field, because a Node carries a pointer
// and reflect.DeepEqual on a time pointer is a comparison of what it points at
// only when it is the same instant: this is one place where a test's intent is
// easier to read than a diff of two structs.
func sameNode(a, b Node) bool {
	if a.Key != b.Key || a.Source != b.Source || a.Kind != b.Kind || a.ID != b.ID ||
		a.Title != b.Title || a.Repo != b.Repo || a.State != b.State || a.Release != b.Release {
		return false
	}
	switch {
	case a.Updated == nil && b.Updated == nil:
		return true
	case a.Updated == nil || b.Updated == nil:
		return false
	default:
		return a.Updated.Equal(*b.Updated)
	}
}
