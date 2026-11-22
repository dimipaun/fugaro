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
//   - TestEdgesJoinNodesOfTheDocument: a dependency the node does not carry
//     draws nothing, because an edge to a node the document does not hold is not
//     one a reader can draw (contract §6.22) — and two orchestrators that each
//     carry a `wi-1` are two keys, so no edge reaches across namespaces by
//     accident (D1356).
//   - TestCrossOrchestratorDependencyEdges: a `depends_on` naming another
//     orchestrator's item draws an edge to the peer's node, keyed under the
//     scope the document draws the peer in, and a peer the document does not
//     draw draws nothing (D3208, F17).
//   - TestCrossOrchestratorDependencyFromAStoredState: the same edge drawn from
//     a node a daemon wrote rather than one built here — the end-to-end case
//     I-ORCH runs. It is gated on the two halves of D3208 this task does not
//     carry: an id of `<project>~<name>` (CG1a) and a state whose validator
//     accepts a cross reference (CG3).
//   - TestLocalScopeFallsBackToTheCache: a local scope whose node cannot be read
//     answers with the cached brief's own stamp as its age and no nodes — a
//     brief is not a graph (D1358, T5(a)).
//   - TestUnknownOrchestratorIsRefused: an id neither the database nor this
//     machine has is ErrNoOrchestrator, the one failure of this source that is a
//     refusal rather than a state (H3, §6.22).
//   - TestListedOrchestratorWithoutAStateIsDown: an id the database lists whose
//     state node is not written yet is a scope that is down — the shape a daemon
//     that has beaten its lease and saved no state leaves behind, which `ls`
//     lists and a screen sends back.
//   - TestLocalScopeUsesTheLiveEndpoint: a local scope draws the endpoint this
//     machine's daemon serves when one is live — stamped with the tick it came
//     from — and the node when the file's port belongs to something that is not
//     that daemon (CG13c).
//   - TestLocalScopeFallsBackToTheNodeAndThenTheCache: the order T5(a) was
//     amended to, as a machine with no endpoint sees it.
//   - TestTheScopeRuleIsTheIDRule: a named id is a local scope's argument like
//     any other, so the endpoint serving it is read (F40, CG8), while an id no
//     orchestrator may be keyed by is judged before the endpoint is read and
//     answered from its node (CG13c, H1).
//   - TestOnlyALocalScopeReadsTheEndpoint: a live endpoint beside an
//     `orchestrator:` scope is not read at all, counted at the transport.
//   - TestTheEndpointStreamFeedsTheWatch: the endpoint's stream feeds the
//     document stream — one whole document per tick, rising seq, one stream_id.
//
// The source is pure but for the endpoint it reaches, so every case that is not
// an endpoint case is a struct literal and a returned value: no fake, no clock
// and no I/O.
// The endpoint cases start a real one on loopback, with its registry file in a
// temp directory, and no case reads a developer's own state directory.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/endpoint"
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

// TestEdgesJoinNodesOfTheDocument: an edge this source draws has both of its
// ends among the nodes the document carries, and a dependency reaches into
// another orchestrator's key space only where the reference names one
// (TestCrossOrchestratorDependencyEdges) — the ids are the node's own
// namespace, so two orchestrators can each carry a `wi-1` and their keys are
// still two keys (D1356, §6.22).
//
// A bare `depends_on` naming an item the node does not carry never reaches this
// reader at all: the state's own validator refuses a state whose item waits on
// an item it does not hold (ValidateState), so what this test holds is the
// property a reader relies on — both ends are keys of the document it is
// reading — rather than a shape the writer cannot produce.
func TestEdgesJoinNodesOfTheDocument(t *testing.T) {
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

// TestCrossOrchestratorDependencyEdges: a `depends_on` naming another
// orchestrator's item — `<orchestrator>/<item>`, D3208 — is an edge to that
// peer's node, keyed under the scope this document draws the peer in (F17).
//
// Which scope that is, is the document's fact and not this source's, and the
// local half below is what holds it: the same reference keyed `local:<peer>`
// ends at `local:aurora~night:wi-7` where the central one ends at
// `orchestrator:aurora~night:wi-7`, and a source that mirrored its own kind
// would draw the far end as a key no node of the document has.
//
// The four references that draw nothing are the other half: a bare id this node
// does not carry (the §6.22 rule), a reference to this very orchestrator, one to
// an orchestrator this document does not draw, and one whose item half is no
// item id — a node's own namespace answers to exactly its own ids, and a peer
// that is not on the map has no node here to point at.
func TestCrossOrchestratorDependencyEdges(t *testing.T) {
	const peer = "aurora~night"
	state := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems: []orchestrator.WorkItem{
			item("wi-1", "wait on the other orchestrator", "acme/app", peer+"/wi-7"),
			item("wi-2", "wait on an item this node does not carry", "acme/app", "wi-9"),
			item("wi-3", "wait on this very orchestrator", "acme/app", "aurora/wi-3"),
			item("wi-4", "wait on an orchestrator this document does not draw", "acme/app", "elsewhere/wi-1"),
			item("wi-5", "wait on something that is not an item", "acme/app", "Aurora~Night/wi-1"),
		},
	}
	peers := map[string]string{"aurora": "orchestrator:aurora", peer: "orchestrator:" + peer}
	_, edges, err := orchestratorNodes("orchestrator:aurora", state, peers)
	if err != nil {
		t.Fatalf("reading the state: %v", err)
	}
	want := []Edge{{From: "orchestrator:aurora:wi-1", To: "orchestrator:" + peer + ":wi-7", Kind: DependsOnEdge}}
	if len(edges) != len(want) || edges[0] != want[0] {
		t.Fatalf("the state drew %+v, want the one cross-orchestrator dependency and nothing else", edges)
	}

	// The peer this machine runs rather than the database alone: the same
	// reference, and the far end is the scope the document draws the peer in.
	local := map[string]string{"aurora": "local:aurora", peer: "local:" + peer}
	_, localEdges, err := orchestratorNodes("local:aurora", state, local)
	if err != nil {
		t.Fatalf("reading the state under a local scope: %v", err)
	}
	if len(localEdges) != 1 || localEdges[0] != (Edge{From: "local:aurora:wi-1", To: "local:" + peer + ":wi-7", Kind: DependsOnEdge}) {
		t.Errorf("the local scope drew %+v, want its item waiting on the peer under the peer's own scope", localEdges)
	}
}

// TestCrossOrchestratorDependencyFromAStoredState: the same edge read from the
// nodes a daemon wrote, with the two orchestrators keyed as the read that
// enumerated them keys them — the end-to-end shape I-ORCH's run draws, where
// both ends of the edge are nodes of one document.
//
// It is gated on the two halves of D3208 this task does not carry, and it skips
// rather than fails until they land: an orchestrator id of `<project>~<name>`
// (CG1a) and a state whose own validator accepts a cross reference (CG3). The
// skip is the honest state of the tree; the test is what says the two halves of
// this edge meet when they are there.
func TestCrossOrchestratorDependencyFromAStoredState(t *testing.T) {
	const peer = "aurora~night"
	waiting := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{item("wi-1", "wait on the other orchestrator", "acme/app", peer+"/wi-7")},
	}
	if _, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       peer,
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{item("wi-7", "the item the other waits on", "acme/app")},
	}); err != nil {
		t.Skipf("the store does not address a named orchestrator yet, so two orchestrators of one project cannot be written: %v", err)
	}
	if _, err := orchestrator.EncodeState(waiting); err != nil {
		t.Skipf("a dependency on another orchestrator's item is not a document this release writes, so no node can carry the edge: %v", err)
	}

	peers := map[string]string{"aurora": "orchestrator:aurora", peer: "orchestrator:" + peer}
	waiter := OrchestratorSource{ID: "aurora", Peers: peers,
		Node: stateOf(t, "aurora", item("wi-1", "wait on the other orchestrator", "acme/app", peer+"/wi-7"))}
	waited := OrchestratorSource{ID: peer, Peers: peers,
		Node: stateOf(t, peer, item("wi-7", "the item the other waits on", "acme/app"))}
	nodes, edges, _, err := waiter.Nodes()
	if err != nil {
		t.Fatalf("reading the orchestrator that waits: %v", err)
	}
	peerNodes, peerEdges, _, err := waited.Nodes()
	if err != nil {
		t.Fatalf("reading the orchestrator that is waited on: %v", err)
	}
	want := Edge{From: "orchestrator:aurora:wi-1", To: "orchestrator:" + peer + ":wi-7", Kind: DependsOnEdge}
	if len(edges) != 1 || edges[0] != want {
		t.Fatalf("the scope drew %+v, want %+v", edges, want)
	}
	if len(peerEdges) != 0 {
		t.Errorf("the orchestrator that is waited on drew %+v, want no edge: an item nothing waits on has none", peerEdges)
	}

	// The two sources as one document, which is what a screen draws: the far end
	// of the edge is a node of it, keyed the way that peer's own nodes are.
	doc := Document{Version: DocumentVersion, Nodes: append(nodes, peerNodes...), Edges: edges}
	if err := doc.Sanitise(); err != nil {
		t.Fatalf("the document the two scopes filled is not fit to print: %v", err)
	}
	keys := map[string]bool{}
	for _, n := range doc.Nodes {
		keys[n.Key] = true
	}
	for _, e := range doc.Edges {
		if !keys[e.From] || !keys[e.To] {
			t.Errorf("the edge %+v has an end that is not a node of the document: %v", e, keys)
		}
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
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written, Endpoint: noEndpoint(t),
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
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written, Endpoint: noEndpoint(t)}
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
		src := OrchestratorSource{ID: "o1", Local: true, CachedAt: written, Endpoint: noEndpoint(t),
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
		{ID: "o1", Local: true, Endpoint: noEndpoint(t)},
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

// TestListedOrchestratorWithoutAStateIsDown: an id the project's database lists
// with no state node yet is an orchestrator this installation has, so its scope
// is down and never a scope that does not exist. That shape is reachable —
// `orchestrator run` writes the lease node before the cycle that saves a state,
// so a daemon whose cycles have all failed is listed by `fugaro orchestrator ls`
// and has nothing to draw — and it is why the value a screen sends back is a
// value this read must answer for (H1, F21, §6.22).
//
// The fact itself is the caller's, because this source reads nothing: `all`
// knows it of every id it enumerated and a named scope is asked about it
// (graphSources), and the command's own test is where that wiring is held. Here
// it is the field and what the scope says with it.
func TestListedOrchestratorWithoutAStateIsDown(t *testing.T) {
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	for _, src := range []OrchestratorSource{
		{ID: "o1", Listed: true},
		{ID: "o1", Local: true, Listed: true, Endpoint: noEndpoint(t)},
	} {
		nodes, edges, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("a listed orchestrator with no state node (%+v) answered with %v, want the scope down", src, err)
		}
		if st.State != SourceDown || st.Source != src.Scope().String() {
			t.Errorf("the scope is %+v, want %s down", st, src.Scope())
		}
		if len(nodes) != 0 || len(edges) != 0 {
			t.Errorf("the scope drew %d nodes and %d edges, want none: there is no state to draw", len(nodes), len(edges))
		}
		if !strings.Contains(st.Reason, "state node is not written yet") {
			t.Errorf("the reason is %q, want it to say the state node is not written", st.Reason)
		}
	}

	// A local scope with a cached brief keeps the brief's own stamp: the cache is
	// the last thing written about the id, and a database that lists it does not
	// change what the brief says (D1358, D1243).
	src := OrchestratorSource{ID: "o1", Local: true, Listed: true, CachedAt: written, Endpoint: noEndpoint(t)}
	_, _, st, err := src.Nodes()
	if err != nil {
		t.Fatalf("reading a listed local scope with a cached brief: %v", err)
	}
	if st.State != SourceDown || st.AsOf == nil || !st.AsOf.Equal(written) {
		t.Errorf("the scope is %+v, want it down with the cached brief's stamp %s", st, written)
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

// The local scope against the endpoint a daemon serves (CG13c of
// docs/cli-output-console-graph/implementation-plan-graph.md): a live endpoint
// first, then the node, then the cache — and never a read the scope's own id
// rule refused.

// endpointEnv is one machine's state directory and the two readers placed in
// it: the getenv the registry file is written and looked up through, and the
// Client a local scope reads with. The directory is a temp one, so no case here
// touches the state directory of the developer running it (the plan's
// hermeticity rule, and the reason Client's placement is two fields).
func endpointEnv(t *testing.T) (func(string) string, endpoint.Client) {
	t.Helper()
	dir := t.TempDir()
	getenv := func(name string) string {
		if name == "XDG_STATE_HOME" {
			return dir
		}
		return ""
	}
	return getenv, endpoint.Client{Getenv: getenv, GOOS: "linux"}
}

// noEndpoint is the reader of a machine that runs no daemon: a temp state
// directory with nothing written in it. Every local fixture carries one, because
// the zero value of the field is this process's own environment and platform.
func noEndpoint(t *testing.T) endpoint.Client {
	t.Helper()
	_, client := endpointEnv(t)
	return client
}

// fileToken is a value of the shape the endpoint mints, drawn here and never
// written down: a fixture carries no literal credential, and nothing in a test
// is a secret of anybody's.
func fileToken(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("drawing a token: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// closeEndpoint closes what a case started, at the end of the case.
func closeEndpoint(t *testing.T, srv *endpoint.Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Errorf("closing the endpoint: %v", err)
		}
	})
}

// servedDaemon is the daemon half of a local scope: a real endpoint on loopback,
// started with the state a case wants it to answer with, and the registry file
// that daemon would have written for its own id.
func servedDaemon(t *testing.T, getenv func(string) string, id string, s orchestrator.State, at time.Time) {
	t.Helper()
	srv, err := endpoint.Start(t.Context(), endpoint.Options{ID: id, Snapshot: func() (orchestrator.State, time.Time, bool) {
		return s, at, true
	}})
	if err != nil {
		t.Fatalf("starting the endpoint: %v", err)
	}
	closeEndpoint(t, srv)
	holdRegistry(t, getenv, endpoint.Registry{ID: id, PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: srv.Nonce(), Started: time.Now().UTC()})
}

// anotherDaemon is the case D1359's nonce exists for: a live endpoint that is
// not this scope's daemon, and a registry file that names its port for this
// scope's id with the token it answers to and a nonce it was not started with.
// Everything about the file looks like a daemon's — it is a daemon's, of another
// orchestrator — and what refuses it is the handshake, so a reader that trusted
// the file would draw another orchestrator's work under this scope's name.
func anotherDaemon(t *testing.T, getenv func(string) string, id string, s orchestrator.State, at time.Time) {
	t.Helper()
	srv, err := endpoint.Start(t.Context(), endpoint.Options{ID: "o2", Snapshot: func() (orchestrator.State, time.Time, bool) {
		return s, at, true
	}})
	if err != nil {
		t.Fatalf("starting the second endpoint: %v", err)
	}
	closeEndpoint(t, srv)
	holdRegistry(t, getenv, endpoint.Registry{ID: id, PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: fileToken(t), Started: time.Now().UTC()})
}

// holdRegistry writes one daemon's registry file for the life of the case.
func holdRegistry(t *testing.T, getenv func(string) string, reg endpoint.Registry) {
	t.Helper()
	holder, err := endpoint.Hold(getenv, "linux", reg)
	if err != nil {
		t.Fatalf("writing the registry file: %v", err)
	}
	t.Cleanup(func() { _ = holder.Remove() })
}

// stateWith is one work item as the daemon's own state — what an endpoint serves
// and what a local scope reads back — with the item's id the case's own, so a
// document drawn from the endpoint and one drawn from a node cannot be confused.
func stateWith(id string) orchestrator.State {
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "o1",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{item(id, "the endpoint's item", "acme/app")},
	}
}

// countingTransport counts the requests that left the process. A scope that
// refuses an argument before it reads anything is a claim about a read that did
// not happen, and this is what makes it a fact.
type countingTransport struct {
	base  http.RoundTripper
	count atomic.Int64
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.count.Add(1)
	return c.base.RoundTrip(req)
}

// TestLocalScopeUsesTheLiveEndpoint: a `local:` scope draws the document the
// daemon on this machine serves when there is one, and the node when there is
// not. The two answers are distinguishable in the document without knowing
// which process wrote it: the endpoint's is stamped with the server time of the
// tick it came from, which is the one freshness fact a local read has, and the
// node's claims no age at all.
//
// The second sub-case is the one CG13b's handshake exists for, and it is the
// reason "live" is a proven process rather than a file: a registry file naming a
// port that is not this scope's daemon is refused, and the scope draws the node
// instead of another orchestrator's work under this scope's name.
func TestLocalScopeUsesTheLiveEndpoint(t *testing.T) {
	tickedAt := time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	node := func(t *testing.T) json.RawMessage {
		t.Helper()
		return stateOf(t, "o1", item("wi-node", "the node's item", "acme/app"))
	}

	t.Run("the daemon's own document, stamped with its tick", func(t *testing.T) {
		getenv, client := endpointEnv(t)
		servedDaemon(t, getenv, "o1", stateWith("wi-endpoint"), tickedAt)

		src := OrchestratorSource{ID: "o1", Local: true, Endpoint: client, Node: node(t), CachedAt: written}
		nodes, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("reading a local scope beside a live endpoint: %v", err)
		}
		if st.Source != "local:o1" || st.State != SourceUp {
			t.Fatalf("the scope is %+v, want local:o1 up", st)
		}
		if st.AsOf == nil || !st.AsOf.Equal(tickedAt) {
			t.Errorf("the scope's as_of is %v, want the endpoint's own tick %s: it is what a document drawn from an endpoint carries and a document drawn from a node does not", st.AsOf, tickedAt)
		}
		if len(nodes) != 1 || nodes[0].ID != "wi-endpoint" {
			t.Fatalf("the scope drew %+v, want the live endpoint's own item", nodes)
		}
		if nodes[0].Key != "local:o1:wi-endpoint" {
			t.Errorf("the node is keyed %q, want the scope's own key", nodes[0].Key)
		}
	})

	t.Run("a port that is not this scope's daemon is not an endpoint", func(t *testing.T) {
		getenv, client := endpointEnv(t)
		anotherDaemon(t, getenv, "o1", stateWith("wi-another-orchestrator"), tickedAt)

		src := OrchestratorSource{ID: "o1", Local: true, Endpoint: client, Node: node(t)}
		nodes, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("reading a local scope beside a file that is not this daemon's: %v", err)
		}
		if st.State != SourceUp || st.AsOf != nil {
			t.Errorf("the scope is %+v, want it up from the node and claiming no age", st)
		}
		if len(nodes) != 1 || nodes[0].ID != "wi-node" {
			t.Fatalf("the scope drew %+v, want the node: the endpoint at the file's address is not the daemon that wrote it (D1359)", nodes)
		}
	})
}

// TestOnlyALocalScopeReadsTheEndpoint: the endpoint is the local scope's own
// half of D1359 and is read for no other kind. An `orchestrator:<id>` scope is
// one the project's database holds (CG4's rule, D1358), and this machine's
// daemon may well be serving that very id — what such a scope draws still comes
// from the node, because a name a database lists is not a claim about which
// machine runs it, and a document drawn from a local process under a scope the
// console asked the project for is a read nobody asked for.
//
// The count is what holds it, and the control is what makes the count mean
// something: the same fixture read as `local:` does reach the endpoint, so a
// zero below is a scope that did not read and not a fixture that had nothing to
// read.
func TestOnlyALocalScopeReadsTheEndpoint(t *testing.T) {
	tickedAt := time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	getenv, client := endpointEnv(t)
	servedDaemon(t, getenv, "o1", stateWith("wi-endpoint"), tickedAt)
	counted := &countingTransport{base: http.DefaultTransport}
	client.HTTP = &http.Client{Transport: counted}

	node := stateOf(t, "o1", item("wi-node", "the node's item", "acme/app"))
	src := OrchestratorSource{ID: "o1", Endpoint: client, Node: node}
	nodes, _, st, err := src.Nodes()
	if err != nil {
		t.Fatalf("reading an orchestrator scope beside a live endpoint: %v", err)
	}
	if st.Source != "orchestrator:o1" || st.State != SourceUp || st.AsOf != nil {
		t.Errorf("the scope is %+v, want orchestrator:o1 up from the node and claiming no age", st)
	}
	if len(nodes) != 1 || nodes[0].ID != "wi-node" {
		t.Fatalf("the scope drew %+v, want the node: an orchestrator scope reads no endpoint", nodes)
	}
	if n := counted.count.Load(); n != 0 {
		t.Errorf("an orchestrator scope made %d requests to the endpoint beside it, want none", n)
	}

	control := OrchestratorSource{ID: "o1", Local: true, Endpoint: client, Node: node}
	nodes, _, st, err = control.Nodes()
	if err != nil {
		t.Fatalf("reading the local control: %v", err)
	}
	if st.AsOf == nil || !st.AsOf.Equal(tickedAt) || len(nodes) != 1 || nodes[0].ID != "wi-endpoint" {
		t.Fatalf("the local control is %+v with nodes %+v, want the live document: without it the count above proves nothing", st, nodes)
	}
	if n := counted.count.Load(); n == 0 {
		t.Error("the local control reached no endpoint, so the fixture has none to refuse")
	}
}

// TestLocalScopeFallsBackToTheNodeAndThenTheCache: `local:<id>` prefers a live
// endpoint, then the node, then the cache (T5(a), amended 2026-11-11). This is
// the order as a machine without a daemon sees it — the endpoint tries first and
// answers nothing — so what a machine whose daemon predates the endpoint, or is
// not running, reads is exactly what CG5 built.
func TestLocalScopeFallsBackToTheNodeAndThenTheCache(t *testing.T) {
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

	t.Run("the node", func(t *testing.T) {
		src := OrchestratorSource{ID: "o1", Local: true, Endpoint: noEndpoint(t),
			Node: stateOf(t, "o1", item("wi-node", "the node's item", "acme/app")), CachedAt: written}
		nodes, _, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("reading a local scope with no endpoint: %v", err)
		}
		if st.State != SourceUp || st.AsOf != nil || len(nodes) != 1 || nodes[0].ID != "wi-node" {
			t.Errorf("the scope is %+v with nodes %+v, want it up from the node and claiming no age", st, nodes)
		}
	})

	t.Run("the cache", func(t *testing.T) {
		src := OrchestratorSource{ID: "o1", Local: true, Endpoint: noEndpoint(t), CachedAt: written,
			ReadErr: errors.New("the database is down")}
		nodes, edges, st, err := src.Nodes()
		if err != nil {
			t.Fatalf("reading a local scope with a cache: %v", err)
		}
		if st.State != SourceDown || st.AsOf == nil || !st.AsOf.Equal(written) {
			t.Errorf("the scope is %+v, want it down with the cached brief's own stamp %s", st, written)
		}
		if len(nodes) != 0 || len(edges) != 0 {
			t.Errorf("the fallback drew %d nodes and %d edges, want none: a brief is not a graph", len(nodes), len(edges))
		}
	})

	t.Run("a scope with neither is refused", func(t *testing.T) {
		src := OrchestratorSource{ID: "o1", Local: true, Endpoint: noEndpoint(t)}
		if _, _, _, err := src.Nodes(); !errors.Is(err, ErrNoOrchestrator) {
			t.Errorf("a local scope with no endpoint, no node and no cache answered %v, want ErrNoOrchestrator", err)
		}
	})

	t.Run("a watch with no endpoint says so", func(t *testing.T) {
		// The watch's half of the same order: a caller that is watching holds the
		// node subscription already, so what it is told is that there is nothing
		// live here to follow rather than a scope that is empty.
		err := LocalLive{Client: noEndpoint(t), ID: "o1"}.Watch(t.Context(), func(orchestrator.State, time.Time) error {
			t.Error("a watch with no endpoint drew a document")
			return nil
		})
		if err == nil {
			t.Fatal("a watch with no endpoint answered with no error")
		}
		if !strings.Contains(err.Error(), "no live endpoint") {
			t.Errorf("the watch failed with %q, want it to say there is no live endpoint", err)
		}
	})
}

// TestTheScopeRuleIsTheIDRule: the local scope's argument and the id of
// `orchestrator/<id>` are one rule (F40, CG8 — ParseScope holds the value to
// naming.go's ValidOrchestratorID), so a named orchestrator is a local scope like
// any other and the endpoint this machine's daemon serves for it is read: the
// split the two rules had when CG13c put the judgement here is gone, and with it
// the reading that a named id had no local scope.
//
// What the judgement still does is keep this source from looking an endpoint up
// for a name no orchestrator may be keyed by — an id with `_`, one past 81
// characters — and it does it before the read rather than after: the node and
// then the cache answer for such an id exactly as they do on a machine with no
// daemon at all. The command line refuses such a value as it parses the argument,
// before a source exists to judge anything
// (TestGraphRefusesAnInvalidScopeBeforeAnyRead), and no enumerator of this CLI
// can hand this source one, because NodeIDs and CachedBriefs hold every id they
// answer with to the same rule — so the case below is a source built by hand,
// which is the shape that reaches the guard at all.
//
// The cached brief is the last half of what `all` enumerates: an id this machine
// holds a brief for and no node is a local scope of the document, down, stamped
// with the brief's own time, and never one left out of it (graphSources, D1358).
func TestTheScopeRuleIsTheIDRule(t *testing.T) {
	tickedAt := time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	const named = "my-project~night"
	// A name no orchestrator may be keyed by: the rule admits a-z, 0-9 and `-` in
	// each half and no `_`, so neither a path nor a registry file carries it.
	const refused = "my_project"

	getenv, client := endpointEnv(t)
	servedDaemon(t, getenv, named, stateWith("wi-endpoint"), tickedAt)
	counted := &countingTransport{base: http.DefaultTransport}
	client.HTTP = &http.Client{Transport: counted}

	// The named id is a local scope like any other, and the daemon serving it is
	// the answer: up, stamped with the server time of the tick the document came
	// from and never this machine's clock (D1230). The node beside it holds a
	// different item, so an answer from the node is not this one.
	nodes, edges, st, err := OrchestratorSource{ID: named, Local: true, Endpoint: client,
		Node: stateOf(t, named, item("wi-1", "an item of the node", "acme/app"))}.Nodes()
	if err != nil {
		t.Fatalf("a local scope for the named id %q was refused: %v", named, err)
	}
	if st.State != SourceUp || st.AsOf == nil || !st.AsOf.Equal(tickedAt) {
		t.Errorf("the scope for %q is %+v, want it up from the endpoint stamped %s", named, st, tickedAt)
	}
	if len(nodes) != 1 || nodes[0].ID != "wi-endpoint" || len(edges) != 0 {
		t.Errorf("the scope for %q drew %+v with edges %+v, want the endpoint's one item", named, nodes, edges)
	}
	if n := counted.count.Load(); n == 0 {
		t.Error("the endpoint was not read for a named id, and the scope rule admits it (F40)")
	}

	// The guard: an id the rule refuses is judged before the endpoint is read, and
	// the node answers exactly as it does on a machine with no daemon. The node is
	// a document under an id the rule admits, because a state's own agent id is
	// held to this rule too (ValidateState): what carries the refused id here is
	// the source, which is what a value reaching the guard is.
	before := counted.count.Load()
	nodes, edges, st, err = OrchestratorSource{ID: refused, Local: true, Endpoint: client,
		Node: stateOf(t, "my-project", item("wi-1", "an item of the node", "acme/app"))}.Nodes()
	if err != nil {
		t.Fatalf("a local scope whose id is %q was refused: %v", refused, err)
	}
	if st.State != SourceUp || st.AsOf != nil {
		t.Errorf("the scope for %q is %+v, want it up from the node and claiming no age", refused, st)
	}
	if len(nodes) != 1 || nodes[0].ID != "wi-1" || len(edges) != 0 {
		t.Errorf("the scope for %q drew %+v with edges %+v, want the node's one item", refused, nodes, edges)
	}
	if after := counted.count.Load(); after != before {
		t.Errorf("the scope made %d requests for an id no orchestrator may be keyed by, want none: the rule is judged before the endpoint is read (H1)", after-before)
	}

	// The other half of what `all` enumerates: an id with a brief and no node is
	// this machine's own scope, down, stamped with the brief's own time — a scope
	// of the document and not one left out of it (D1358, T5(a)).
	cached := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	withCache := OrchestratorSource{ID: named, Local: true, Endpoint: noEndpoint(t), CachedAt: cached}
	if _, _, st, err := withCache.Nodes(); err != nil {
		t.Fatalf("a local scope for %q with a cached brief was refused: %v", named, err)
	} else if st.State != SourceDown || st.AsOf == nil || !st.AsOf.Equal(cached) {
		t.Errorf("the scope for %q is %+v, want it down stamped %s", named, st, cached)
	}

	// The control: the same id under the kind the database holds is read as it was
	// before, because only a local scope's id is judged at all.
	control := OrchestratorSource{ID: named, Node: stateOf(t, named, item("wi-1", "an item", "acme/app"))}
	if _, _, st, err := control.Nodes(); err != nil || st.State != SourceUp {
		t.Errorf("an orchestrator scope for %q is %+v, %v, want it up", named, st, err)
	}
}

// TestTheEndpointStreamFeedsTheWatch: the endpoint's stream is what a local
// scope's watch follows, and what it feeds is the same document the one-shot
// read builds — one whole document per tick, carrying the stream's identity and
// a sequence that rises within it (D1360, H4). The state at connect time comes
// first, so a watch opened on a quiet orchestrator draws something at once.
func TestTheEndpointStreamFeedsTheWatch(t *testing.T) {
	first := time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	later := first.Add(5 * time.Minute)

	var mu sync.Mutex
	state, at := stateWith("wi-first"), first
	getenv, client := endpointEnv(t)
	srv, err := endpoint.Start(t.Context(), endpoint.Options{ID: "o1", Snapshot: func() (orchestrator.State, time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		return state, at, true
	}})
	if err != nil {
		t.Fatalf("starting the endpoint: %v", err)
	}
	closeEndpoint(t, srv)
	holdRegistry(t, getenv, endpoint.Registry{ID: "o1", PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: srv.Nonce(), Started: time.Now().UTC()})

	stream, err := NewStream()
	if err != nil {
		t.Fatalf("drawing a stream: %v", err)
	}
	var printed bytes.Buffer
	frames := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- LocalLive{Client: client, ID: "o1"}.Watch(ctx, func(s orchestrator.State, tick time.Time) error {
			nodes, edges, err := orchestratorNodes("local:o1", s, nil)
			if err != nil {
				return err
			}
			doc := stream.Next(Document{Version: DocumentVersion,
				Sources: []SourceState{{Source: "local:o1", State: SourceUp, AsOf: &tick}},
				Nodes:   nodes, Edges: edges}, tick)
			if err := WriteDoc(&printed, doc); err != nil {
				return err
			}
			frames <- struct{}{}
			return nil
		})
	}()

	waitForFrame(t, frames, "the state at connect time")
	mu.Lock()
	state, at = stateWith("wi-moved"), later
	mu.Unlock()
	waitForFrame(t, frames, "the tick that moved")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the watch ended with %v, want the caller's own cancellation", err)
	}

	lines := strings.Split(strings.TrimSpace(printed.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("the watch printed %d documents, want one per tick: %q", len(lines), printed.String())
	}
	var docs []Document
	for i, line := range lines {
		var doc Document
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("document %d is not one this release reads: %v", i, err)
		}
		docs = append(docs, doc)
	}
	if docs[0].Version != DocumentVersion || docs[0].StreamID == "" || docs[0].StreamID != docs[1].StreamID {
		t.Errorf("the two documents are %+v and %+v, want one version and one stream_id", docs[0], docs[1])
	}
	if docs[0].Seq != 1 || docs[1].Seq != 2 {
		t.Errorf("the sequences are %d and %d, want them rising within one stream (H4)", docs[0].Seq, docs[1].Seq)
	}
	if docs[0].GeneratedAt != first.UTC() {
		t.Errorf("the first document is stamped %s, want the endpoint's own tick %s", docs[0].GeneratedAt, first)
	}
	if len(docs[0].Nodes) != 1 || docs[0].Nodes[0].ID != "wi-first" {
		t.Errorf("the first document holds %+v, want the state at connect time", docs[0].Nodes)
	}
	if len(docs[1].Nodes) != 1 || docs[1].Nodes[0].ID != "wi-moved" {
		t.Errorf("the second document holds %+v, want the tick that moved", docs[1].Nodes)
	}
	if docs[0].Sources[0].AsOf == nil || !docs[0].Sources[0].AsOf.Equal(first) {
		t.Errorf("the first document's source is %+v, want it stamped with the tick it came from", docs[0].Sources[0])
	}
}

// waitForFrame waits for one document the watch printed, and fails the case at
// the deadline: the watch runs in its own goroutine, so a case waits for what
// arrived rather than sleeping for a guess.
func waitForFrame(t *testing.T, frames <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-frames:
	case <-time.After(20 * time.Second):
		t.Fatalf("the watch printed no document for %s", what)
	}
}
