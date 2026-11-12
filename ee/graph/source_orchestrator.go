// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/endpoint"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The orchestrator scopes: `orchestrator:<id>`, one orchestrator's state node
// under `orchestrator/`, and `local:<id>`, the same node read as one this
// machine's daemon drives. They are CG5 of
// docs/cli-output-console-graph/implementation-plan-graph.md, and the second
// source of a document — the first whose graph has edges.
//
// Three things about them are facts about the data rather than choices here:
//
//   - **The tasks are the node's work items and the edges are their
//     `depends_on`** (F14, F18): the state carries a task-level graph, which is
//     what the coordinator's stored view has none of (F17, CG2). So this is
//     where `graph --json` stops being a list of nodes.
//   - **An edge stays inside one orchestrator.** A `depends_on` naming an item
//     the same node does not carry draws nothing (contract §6.22): the ids are
//     the node's own namespace, and an edge to a node this document does not
//     hold is not one a screen can draw.
//   - **It reads almost nothing.** The caller has already read
//     `orchestrator/<id>` through the person's own credential (D1357, T2) and,
//     for a local scope, listed the cached briefs this machine holds
//     (`orchestrator.CachedBriefs`, D1358). The one read this file makes is a
//     local scope's endpoint (CG13c): one lookup of a registry file beside a
//     handshake, and then one document from a live endpoint on loopback, all of
//     it bounded by `endpointTimeout`. A machine whose daemon is not running, or
//     whose daemon predates the endpoint, has none of it and reads exactly what
//     CG5 built: the node and then the cache (T5(a), amended 2026-11-11).

// ErrNoOrchestrator is a scope that does not exist: an id the project's
// database does not hold at all and this machine holds no cached brief for. The
// question it is the answer to is the one `fugaro orchestrator ls` asks — is
// there a key under `orchestrator/<id>` — and never whether a state node was
// written: an id the database lists with no state node yet is an orchestrator
// this installation has, and its scope is down (OrchestratorSource.Listed). It
// is the one failure of a read here that is a refusal — no document at all —
// and it is wrapped rather than returned bare, so a caller tells it apart from a
// read that failed or an answer this build cannot read, which are states of the
// scope (H3).
//
// It is worth refusing rather than answering with an empty scope because the
// value reaches argv from the console's renderer (H1) and the console sends only
// a value a previous read listed, so an id nothing knows about is a value that
// came from somewhere else — and a screen must be able to tell "nothing here"
// from "not asked" (contract §6.22).
var ErrNoOrchestrator = errors.New("graph: no such orchestrator")

// DependsOnEdge is the kind of the one relation this source draws: a work item
// and the item it waits for, as the state spells it. The document defines no
// vocabulary of its own, so the source's own word travels unchanged (contract
// §6.22).
const DependsOnEdge = "depends_on"

// OrchestratorSource is one orchestrator scope: the state node the caller read,
// the failure of that read when there was one, the brief this machine's daemon
// cached for the id, and the endpoint it may be serving (CG13c). It holds no
// path and no credential of its own — the endpoint's token lives in the Client
// and reaches nothing but a request header — so building one is not a read and
// the zero value of the two fields it added reads this machine.
type OrchestratorSource struct {
	// ID is the orchestrator id: the `<id>` of `orchestrator/<id>`, and the id
	// half of the scope's own name.
	ID string
	// Local is the `local:` kind: an orchestrator this machine runs, whose scope
	// falls back to the daemon's cached brief when the node cannot be read. A
	// scope of the `orchestrator:` kind is one only the database holds (CG4's
	// rule, D1358), and it has no cache to fall back to.
	Local bool
	// Listed is that the project's database lists this id: a key under
	// `orchestrator/<id>` is there, which is the read `fugaro orchestrator ls`
	// answers with (orchestrator.NodeIDs, D1358). It is a fact of its own and
	// not the same one as a state node: `orchestrator run` writes the lease node
	// before the cycle that saves a state, so an id can be listed while there is
	// nothing to draw for it. It is what tells that orchestrator, whose scope is
	// down, from an id the database does not hold at all, whose scope does not
	// exist (ErrNoOrchestrator). The caller answers it because this source reads
	// nothing: `all` knows it of every id it enumerated, and a scope named on
	// the command line is asked about — one shallow read, and only when its
	// state node is not there (graphSources).
	Listed bool
	// Node is the state document the database answered with: nil when the
	// database holds no node at this id.
	Node json.RawMessage
	// ReadErr is why the node's read failed, when it did — a database that could
	// not be reached, a bucket that refused a listing. It is the caller's to
	// classify: a read this CLI is refused is never passed here, because a
	// refusal is terminal for the whole read and the caller returns it instead
	// (CG7's rule, and the reason a local scope can answer a failure with a
	// cached brief without ever answering a refusal with one).
	ReadErr error
	// CachedAt is when this machine's daemon last wrote the id's cached brief
	// (`$XDG_CACHE_HOME/fugaro/orchestrator/<id>.json`, D1243): the zero time
	// when this machine holds no cache for it. It is the local scope's offline
	// snapshot (D1358, T5), and it is a time and not a state: the cache holds
	// the brief a session-start print reads, never a graph, so what this scope
	// draws with it is one state and an age and no nodes.
	CachedAt time.Time
	// Endpoint is the reader of the local endpoint this machine's daemon may be
	// serving for the id (CG13c). It is read for a `local:` scope and for no
	// other kind, and its zero value — the fields a caller leaves alone — is
	// this process's own environment and platform, which is what a scope built
	// from the command line has (graphSources).
	Endpoint endpoint.Client
}

// Scope is the scope this source is, as the command line spells it: the source
// half of every key it produces and the value a console may send back as
// --source (contract §6.22).
func (s OrchestratorSource) Scope() Scope {
	kind := ScopeOrchestrator
	if s.Local {
		kind = ScopeLocal
	}
	return Scope{Kind: kind, ID: s.ID}
}

// Nodes returns the scope's nodes, the edges between them and the scope's own
// state.
//
// A work item becomes one `task` node: its id (which is unique inside one node,
// so keys are unique inside one source — D1356), its title, the repository it
// names, its phase as its state, and the time of the newest attempt committed
// against it, which is the one age a work item carries. Its `depends_on` become
// edges to the items this same node holds, and a dependency it does not hold
// draws nothing.
//
// A `local:` scope tries the endpoint this machine's daemon serves before it
// answers from the node: a live endpoint is the daemon's own document, fresher
// than the node a person's credential reads out of the database, and where
// there is none the node and then the cache are read exactly as CG5 built them
// (T5(a), amended 2026-11-11). "Live" is the endpoint's own handshake and never
// a file on disk: a registry file left behind by a daemon that is gone is no
// live endpoint, and a process that has taken its port answers for neither.
//
// The scope's state is what the read could say:
//
//   - `up` — the node was read and decoded, or a live endpoint served the
//     daemon's own document. A node claims no age, and neither does this scope
//     when it answers from one: the node carries no time of its own, exactly as
//     the coordinator's stored view does not (CG2), so `as_of` is null. A live
//     endpoint is the one answer that carries a time — the server time of the
//     tick the document came from, which is the same stamp the cached brief
//     carries and never this machine's clock (D1230) — and that is the scope's
//     `as_of`: it says the document is the daemon's own and how old it is. The
//     other freshness fact an orchestrator has — its lease's last beat — is what
//     `fugaro orchestrator ls` reads (CG4), and it is one request per id this
//     read does not make, for an age no screen draws.
//   - `unreadable` — the node answered with a document this build cannot read:
//     a schema version from a later release, a field this release does not model
//     (DecodeState), or an item id sanitising would rewrite, which would let two
//     items share one key (Sanitise, D1356). The nodes are empty rather than
//     partial, so a screen says what it could not read instead of drawing a
//     graph with work quietly missing.
//   - `down` — the node could not be read at all, and a local scope answered
//     with the cached brief instead: the cache says when this machine's daemon
//     last wrote something about the id, and the document carries that as the
//     age with no nodes, because a brief is not a graph (D1358, T5(a)). A scope
//     with no cache to fall back to is down too, saying what failed and claiming
//     no age — a read that did not happen is not a read that was refused, and it
//     is the same rule the coordinator's scope follows for an unreachable
//     database (H3).
//
// The one failure that is not a state is a scope that does not exist: no node,
// no cached brief and no id in the database's own listing, which is a fact this
// read has rather than a failure it met. It comes back as ErrNoOrchestrator, and
// the caller decides — a scope named on the command line is refused (a screen
// must tell "nothing here" from "not asked", §6.22), while a scope `all`
// enumerated is never refused: that enumeration is the database's own answer
// about existence, so an id it named is one this read has something to say about,
// and one it lost between the enumeration and the read is left out with the
// sentence saying so rather than taking the document with it (readGraph).
//
// A local scope's id is judged before anything is read: a name H1's pattern
// cannot carry is refused rather than read, because the id of a registry file is
// a path component and the scope's id is an argument. That is the one place the
// scope's rule is narrower than the daemon's own (agentRE admits `_` and 64
// characters), and the limit is named here rather than widened: a project whose
// name carries `_`, or is longer than 40 characters, has no local scope (H1,
// CG13c).
//
// It never returns a read's own failure: the caller classifies that before it
// gets here (a refusal is terminal and is returned by the caller without asking
// this source at all), and everything else reaches it as ReadErr, which is a
// state.
func (s OrchestratorSource) Nodes() ([]Node, []Edge, SourceState, error) {
	source := s.Scope().String()
	if s.Local {
		if err := s.localID(source); err != nil {
			return nil, nil, SourceState{}, err
		}
		if state, at, ok := s.live(); ok {
			nodes, edges, err := orchestratorNodes(source, state)
			if err != nil {
				return nil, nil, unreadableState(source, err), nil
			}
			return nodes, edges, SourceState{Source: source, State: SourceUp, AsOf: &at}, nil
		}
	}
	switch {
	case len(s.Node) > 0:
		state, err := orchestrator.DecodeState(s.Node)
		if err != nil {
			return nil, nil, unreadableState(source, err), nil
		}
		nodes, edges, err := orchestratorNodes(source, state)
		if err != nil {
			return nil, nil, unreadableState(source, err), nil
		}
		return nodes, edges, SourceState{Source: source, State: SourceUp}, nil
	case s.ReadErr != nil:
		return nil, nil, s.down(source, "the orchestrator's node could not be read: "+s.ReadErr.Error()), nil
	default:
		return s.absent(source)
	}
}

// localID judges the scope's id by the two rules that govern it, before
// anything is read: H1's pattern, which is what a value that reaches argv from
// a renderer may look like (ParseScope), and the daemon's own rule for the id
// of `orchestrator/<id>` (agentRE). H1's is the narrower of the two — it admits
// no `_` and stops at 40 characters where the daemon's carries 64 — so a scope
// whose own name parses is one both rules accept, and a name that does not is
// refused here rather than read: the id of a registry file is a path component
// and the scope's id is an argument, and neither may carry what the other's
// rule refused.
//
// The refusal is ErrNoOrchestrator's, because that is what it is: an id this
// read cannot have a local scope for. `all` therefore leaves such an id out of
// the document with the warning saying so, instead of taking a console's whole
// graph down over a project named `my_project` (readGraph), and a scope named
// on the command line is refused with the pattern's own sentence.
func (s OrchestratorSource) localID(source string) error {
	if _, err := ParseScope(source); err != nil {
		return fmt.Errorf("%w: source %s names no id a local scope can carry: an id is 1 to 40 characters of a-z, 0-9 and -, starting with a letter or a digit, and a project whose name is not one has no local scope (H1)", ErrNoOrchestrator, safetext.Field(source))
	}
	return nil
}

// live is the endpoint this machine's daemon serves for the scope's id, and
// whether one answered. Nothing here has a context of its own — Nodes is a
// method on a value a caller built — so the read is bounded here, by
// endpointTimeout: an endpoint on loopback answers at once or not at all, and a
// graph is never worth a hung read. A machine with no endpoint is a false in the
// time a refused connection takes, which is what makes this cheap enough to try
// first (T5(a)).
func (s OrchestratorSource) live() (orchestrator.State, time.Time, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), endpointTimeout)
	defer cancel()
	return LocalLive{Client: s.Endpoint, ID: s.ID}.Read(ctx)
}

// Absent is a source with nothing to answer for itself: the database holds no
// node at this id and nothing failed to read one. It is the one shape the caller
// has a question about — whether the id exists at all — because answering that
// is a read, and this source reads nothing: `all` knows it of every id it
// enumerated, and a scope named on the command line is asked about, which is what
// it then carries in Listed (graphSources).
func (s OrchestratorSource) Absent() bool { return s.Node == nil && s.ReadErr == nil }

// absent is the scope of a node this read does not have: a local scope with a
// cached brief answers with the brief's own time, an id the database's own
// listing named is a scope that is down, and every other shape of it is a scope
// that does not exist.
func (s OrchestratorSource) absent(source string) ([]Node, []Edge, SourceState, error) {
	if s.Local && !s.CachedAt.IsZero() {
		// A local scope with a cache: the daemon that wrote the brief is the one
		// thing left of the orchestrator here, and the age it carries is the cache's
		// own stamp — the server time that daemon wrote at (D1243) and never this
		// machine's clock, so a laptop whose clock is off still reports the age the
		// daemon recorded.
		return nil, nil, s.down(source, "the orchestrator's node is not in the project's database"), nil
	}
	if s.Listed {
		// The database holds the id and its state node is not there yet. That is
		// an orchestrator this installation has — `fugaro orchestrator ls` lists
		// it, and the value reaches argv from a screen that read that listing
		// (H1, F21) — so it is a scope whose graph cannot be read and never a
		// scope that does not exist (H3, §6.22). `orchestrator run` writes the
		// lease node before the cycle that saves a state, so a daemon whose
		// cycles have all failed is exactly this shape.
		return nil, nil, s.down(source, "the project's database lists this orchestrator and its state node is not written yet"), nil
	}
	// The two kinds are refused for different reasons, and the sentence has to
	// say which one it is: a `local:<id>` is this machine's own reading of the
	// node, a cached brief is what says this machine runs a daemon for the id,
	// and the scope that asked for that daemon and has found neither is refused
	// over the brief and the node and never over the database's listing (§6.22).
	// An `orchestrator:<id>` is refused because the database does not hold the id
	// at all — the question its caller asked before this scope was built
	// (graphSources).
	because := "is not in the project's database"
	if s.Local {
		because = "has no state node in the project's database, and this machine holds no cached brief for it"
	}
	return nil, nil, SourceState{}, fmt.Errorf("%w: %s %s: list the orchestrators this installation has with `fugaro orchestrator ls`", ErrNoOrchestrator, source, because)
}

// down is the scope of an orchestrator this read could not answer for: the
// reason it says, and — for a local scope this machine has a cached brief for —
// the brief's own stamp as the age, because the daemon that wrote it is the last
// thing that has something to say about the id (D1358, T5(a)). A scope with no
// such cache claims no age at all: nothing here knows when its data was last
// good.
func (s OrchestratorSource) down(source, why string) SourceState {
	st := SourceState{Source: source, State: SourceDown, Reason: why + s.cacheClause()}
	if s.Local && !s.CachedAt.IsZero() {
		at := s.CachedAt.UTC()
		st.AsOf = &at
	}
	return st
}

// cacheClause is what a scope that falls back to the cache says about it, and
// the empty string for a scope with no cache: the sentence is one clause of the
// reason either way, so the two callers above cannot word the same fact twice.
func (s OrchestratorSource) cacheClause() string {
	if !s.Local || s.CachedAt.IsZero() {
		return ""
	}
	return fmt.Sprintf(": this machine's cached brief, written %s, is the last thing written about it",
		s.CachedAt.UTC().Format(time.RFC3339))
}

// unreadableState is the scope of an answer this build cannot read (§6.22): the
// state, the refusal's own sentence as its reason, and no nodes. Nothing here
// claims an age, because nothing here says when the data was last good.
func unreadableState(source string, reason error) SourceState {
	return SourceState{Source: source, State: SourceUnreadable, Reason: reason.Error()}
}

// orchestratorNodes is one state node as the nodes and edges of its scope, and
// the refusal of an item or repository whose identity this build cannot carry
// into a key: sanitising such an id would rewrite it, and a rewritten key is one
// two nodes can share (Sanitise, D1356). The state's own validator already
// refuses an id that is not a name (ValidateState), so this is the second half
// of the same fence and not the first: a document can also be hand-written, and
// safetext.Field is lossy in ways a name's own pattern is not.
func orchestratorNodes(source string, s orchestrator.State) ([]Node, []Edge, error) {
	nodes := make([]Node, 0, len(s.WorkItems))
	edges := []Edge{}
	for _, w := range s.WorkItems {
		n := Node{
			Source: source,
			Kind:   NodeTask,
			ID:     w.ID,
			Title:  w.Title,
			Repo:   w.Repo,
			State:  string(w.Phase),
			// The state carries no release: a work item is work on a repository
			// and not a place in a release, so a screen groups these nodes by
			// nothing rather than by a value this source invented.
			Release: "",
			Updated: newestAttempt(s.Intents, w.ID),
		}
		if err := n.sanitise(); err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, n)
	}
	// Edges after the nodes, so an edge is only drawn between two items the
	// document carries: a dependency that is not an item of this state (an id
	// from a later release's document, a typo, an item that has been archived
	// out) is nothing a reader could draw, and the contract says so (§6.22).
	held := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		held[n.ID] = true
	}
	for _, w := range s.WorkItems {
		for _, dep := range w.DependsOn {
			if !held[dep] {
				continue
			}
			edges = append(edges, Edge{
				From: NodeKey(source, w.ID),
				To:   NodeKey(source, dep),
				Kind: DependsOnEdge,
			})
		}
	}
	return nodes, edges, nil
}

// newestAttempt is when the newest attempt at one item was committed, or nil
// when none has been: the one time a work item has of its own, since the item
// itself carries no stamp (the daemon stamps the intent it commits, D1231).
// A node's `updated` is what a screen ages it by, and null — "the source does
// not say" — is the ordinary answer for an item nothing has been tried on yet.
func newestAttempt(intents []orchestrator.Intent, itemID string) *time.Time {
	var newest time.Time
	for _, it := range intents {
		if orchestrator.IntentItem(it.ID) != itemID {
			continue
		}
		if it.At.After(newest) {
			newest = it.At
		}
	}
	if newest.IsZero() {
		return nil
	}
	return &newest
}
