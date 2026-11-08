// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	coordcli "github.com/dimipaun/fugaro/ee/coordinator/cli"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// `fugaro graph --watch --json`: the console's live graph. It is CG7 of
// docs/cli-output-console-graph/implementation-plan-graph.md, and the model is
// `watch --json`'s (F19): whole documents, one per change, on a floor — never
// event deltas, so a lost document needs no repair because the next one is the
// whole graph (D1360).
//
// Five properties are this child, each of them a rule of the contract rather
// than an implementation detail:
//
//   - **One subscription per scope, and one scope today.** The child holds one
//     RTDB connection, to the coordinator's subtree, shared by every node that
//     scope produces (H6, F12): an RTDB stream is a held connection against a
//     project-wide limit, so it is opened per scope and never per node or per
//     repository. A scope added later — the orchestrator's, the local
//     orchestrator's — opens its own subtree beside this one.
//   - **A change is a read, not a patch applied to a document.** The event is
//     the trigger and the one-shot read is what answers, so a stream's document
//     and `graph --json`'s are the same document for the same data. That is
//     what lets a screen switch between the two without the graph changing
//     shape, and it is why a repository the coordinator has never reconciled
//     keeps its runs half here exactly as it does there. The floor bounds the
//     cost: a burst of events is one read, and at most one a second.
//   - **A scope that is down is a state, and a refusal is terminal.** A
//     transport that has fallen over, a listen the server has withdrawn, and a
//     read that failed while the listen stood — an unreachable database, a
//     bucket that refused a listing — are all carried in `sources[].state`, and
//     the child keeps running with the last document's nodes (H3, F20(3),
//     contract §6.22, §10.6); a read this CLI is *refused* is the one thing that
//     ends it, with a sentence naming the command, because that is what the
//     console reads as "this CLI has no graph" (T3).
//   - **A keep-alive, so an orphaned child dies.** A graph that does not change
//     prints nothing, and a child whose reader was killed notices only on its
//     next write: the stream writes a document on a timer even when nothing
//     changed (F11, F20).
//   - **A closed stdout ends the child.** Every write's error is returned, so
//     the next write after the pipe closes is the child's last.

// The stream's two clocks and the bound on its first snapshot. Vars rather than
// consts, and for the reason internal/cli's own watch has vars: the production
// values are the contract's — a keep-alive every 30 seconds, one document a
// second at most — and a test that drives the loop has to see them happen in
// milliseconds rather than in half a minute. A test that changes one puts it
// back; nothing in this package changes them at run time.
var (
	// graphFloor is the least time between two documents (graph.Floor).
	graphFloor = graph.Floor
	// graphKeepAlive is how long a quiet stream waits before its keep-alive
	// (graph.KeepAlive).
	graphKeepAlive = graph.KeepAlive
	// graphFirstSnapshot bounds the wait for the subscription's own first put
	// before the first snapshot is printed anyway. It is `watch --json`'s
	// watchFirstFrame read for one stream instead of four: the child answers
	// within a bounded time whatever the database does, so a screen is never
	// left with nothing to draw because a connection was slow to open.
	graphFirstSnapshot = 5 * time.Second
)

// withdrawnListen is why a scope is down when the server has taken the listen
// away: the subscription is over — the transport does not reconnect after a
// cancel (rtdb.Client.Stream) — and this is the reason a screen shows beside the
// state. The words are `watch`'s own for the same event
// (internal/watch/state.go), because the two children mean the same thing by it.
const withdrawnListen = "the server withdrew the listen"

// runGraphWatch is `graph --watch --json`. The scope has already been parsed
// and refused (runGraph), and the cloud is open: this opens the project's
// database, subscribes to the one subtree the scope reads, and prints until the
// context ends or its reader goes away.
func runGraphWatch(ctx context.Context, out io.Writer, env *fcli.Cloud) error {
	// The connection is the coordinator read's own (CoordinatorDB), so a project
	// with no database is refused here with the sentence `graph --json` refuses
	// it with — the same call and not a second check worded alike — rather than
	// dereferenced.
	db, err := coordcli.CoordinatorDB(ctx, env.Config())
	if err != nil {
		return err
	}
	stream, err := graph.NewStream()
	if err != nil {
		return fcli.RemoteErr(err)
	}
	stream.Floor, stream.Keep = graphFloor, graphKeepAlive
	w := &watcher{env: env, out: out, st: stream}
	return w.run(ctx, db)
}

// watcher is the child's own state: the last document it wrote, whether the
// subscription is up, and whether a change is waiting for the floor to pass.
type watcher struct {
	env *fcli.Cloud
	out io.Writer
	st  graph.Stream
	// last is the document written most recently. It is what a scope that is
	// down still draws: the nodes are the last thing the scope said, and the
	// state beside them is what has changed (H3).
	last graph.Document
	// down is why the scope is down, "" while it is up. Two things set it: a
	// transport that fell over or a read that failed, which the next read
	// clears, and a listen the server withdrew, which nothing clears while the
	// child lives because no subscription is left to deliver the put that
	// would — the scope then stays down exactly as `watch`'s view stays
	// offline (contract §10.6).
	down string
	// dirty is a change that has not been printed yet, either because it has
	// just arrived, because the floor has not passed since the last document, or
	// because the read that would have answered it failed and has to be tried
	// again.
	dirty bool
}

// run is the child's whole life: subscribe, print the first snapshot, then
// print whenever one is due until the context ends or a reader goes away.
func (w *watcher) run(ctx context.Context, db *rtdb.Client) error {
	// The one held connection, to the coordinator's subtree (H6). Every
	// repository's view is a node inside it, so this one stream watches the
	// whole scope: the subscription is per scope and never per node.
	events := db.Stream(ctx, coordpath.PathRoot)
	if err := w.first(ctx, events); err != nil {
		return err
	}
	// The ticker is the floor, because the floor is the finest thing that can
	// become due: a change held by it is retried on the next tick, and a quiet
	// stream checks its keep-alive on the same one.
	tick := time.NewTicker(w.st.Floor)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case ev, ok := <-events:
			if !ok {
				// The channel closes when the context ends or after the server
				// withdrew the listen; the context was checked above, so this is
				// the withdrawal, and the child is not refused by it: its scope
				// goes down and it stays alive with the last graph, which is
				// `watch`'s rule for the same event (contract §10.6) and the rule
				// this scope's plan pins (CG7, F20(3)). Nothing arrives on a
				// closed channel again, so it is dropped from the select rather
				// than read as an endless stream of zero events.
				events = nil
				w.down, w.dirty = withdrawnListen, true
			} else if err := w.observe(ev); err != nil {
				return err
			}
		}
		now := time.Now()
		// The dirty flag stays set across a tick that was too early: the floor
		// delays a document, and never drops one (H5).
		if !w.st.Due(now, w.dirty) {
			continue
		}
		// write answers the change; a read that failed sets the flag again, so
		// the next floor tries the read rather than waiting for a change.
		if err := w.write(ctx, now); err != nil {
			return err
		}
	}
}

// first prints the stream's first document: the full snapshot, taken once the
// subscription has proved it is listening — the transport's own opening put —
// or after the bound, whichever comes first.
//
// Waiting for that put is what keeps the first snapshot from being two: the
// put is what a reconnect delivers as well, so a child that printed before it
// arrived would print a second, identical document a floor later. The bound is
// what keeps the wait from being open-ended: a database that never answers
// still gets the read it can make.
func (w *watcher) first(ctx context.Context, events <-chan rtdb.Event) error {
	timer := time.NewTimer(graphFirstSnapshot)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return w.write(ctx, time.Now())
		case ev, ok := <-events:
			if !ok {
				// A closed channel before the opening put is the context ending
				// or the listen withdrawn before it ever proved it was
				// listening. For the withdrawal no put is coming, so waiting for
				// one waits for the bound to pass for nothing: the first snapshot
				// is written now, with its scope down, and the child is not
				// refused by it (H3, contract §10.6).
				if ctx.Err() != nil {
					return nil
				}
				w.down, w.dirty = withdrawnListen, true
				return w.write(ctx, time.Now())
			}
			switch {
			case ev.Type == "put" && ev.Path == "/":
				return w.write(ctx, time.Now())
			case ev.Type == "error" && errors.Is(ev.Err, rtdb.ErrPermission):
				return refusedStream()
			}
		}
	}
}

// observe folds one event of the subscription into the child's state.
//
// A change waits for the floor. Two things are states rather than exits: a
// transport that has fallen over — the child keeps its subscription, the client
// reconnects on its own and begins again with a full put — and a listen the
// server withdrew, after which there is no subscription left at all. What it
// prints for either is the last document with its sources marked down (H3). The
// one event that ends the child is a refusal: a database answering 401 or 403,
// which is a read this CLI may not make rather than a scope that is down.
func (w *watcher) observe(ev rtdb.Event) error {
	switch ev.Type {
	case "put", "patch":
		w.down, w.dirty = "", true
	case "cancel":
		// The server withdrew the listen — a rule no longer allows it — and the
		// transport does not reconnect after a cancel, so this child's
		// subscription is over. The scope goes down and the child stays alive
		// (contract §10.6): the listen it was holding stopped being allowed,
		// which is not the same as this CLI being refused the read it makes, and
		// it is the read's refusal that ends a child.
		w.down, w.dirty = withdrawnListen, true
	case "auth_revoked":
		// The credential the subscription was made with expired. The transport
		// mints another and reconnects itself, and what follows is a full put,
		// so this is a change like any other; a revoke loop the transport gives
		// up on arrives as an "error" below instead.
		w.dirty = true
	case "error":
		if errors.Is(ev.Err, rtdb.ErrPermission) {
			return refusedStream()
		}
		w.down, w.dirty = "the database stream is down: "+ev.Err.Error(), true
	}
	return nil
}

// write builds the document to print now, stamps it as the stream's next and
// writes it as one line. Its error is the child's: a writer that refuses is a
// reader that is gone, and a read this CLI is refused is the one failure of a
// read that ends it.
func (w *watcher) write(ctx context.Context, now time.Time) error {
	// This document answers whatever was pending; a read that failed below is
	// the one thing that leaves something pending, and it is what the next floor
	// acts on.
	w.dirty = false
	doc, err := w.snapshot(ctx, now)
	if err != nil {
		return err
	}
	// The document is the boundary: every text field of it is made fit to print
	// here, whichever path filled it (H2).
	if err := doc.Sanitise(); err != nil {
		return fcli.RemoteErr(err)
	}
	stamped := w.st.Next(doc, now)
	if err := graph.WriteDoc(w.out, stamped); err != nil {
		return err
	}
	w.last = stamped
	return nil
}

// snapshot is the document this instant's read makes, or the last one with its
// sources marked down when the read cannot be made: a scope that is down is a
// state, and the last thing it said is what a reader still draws beside it (H3).
//
// A read that *fails* is such a state, and never the end of the child: an
// unreachable database, a bucket that refused a listing, a server that answered
// 503. The stream is the one output that must survive one — the console holds it
// open for as long as its tab is — so the failure is carried in the document and
// the change stays pending, which makes the next floor read again and the graph
// come back by itself. The floor is the rate limit on that retry, the same one
// that bounds a burst of changes.
//
// A refusal is the other thing, and it is terminal: it is a read this CLI may
// not make at all, which is exactly the failure `graph --json` exits 1 for (T3),
// and the classification is the CLI's own — a read the command would exit 2 for
// is a scope that is down, a read it would exit 1 for is a refusal. A console
// shows the refusal's sentence as "this CLI has no graph"; a state it draws as a
// graph that is down.
func (w *watcher) snapshot(ctx context.Context, now time.Time) (graph.Document, error) {
	if w.down != "" {
		return downDoc(w.last, w.down), nil
	}
	var warned bytes.Buffer
	// The read's own instant is the age the stream can give (contract §6.22):
	// it is the caller that read the views and knows when (CG7), where the
	// one-shot read has no such time to hand over.
	doc, err := readGraph(ctx, w.env, now, now, &warned)
	switch {
	case err == nil:
		return doc, nil
	case fcli.ExitCode(err) != fcli.ExitUserError:
		w.dirty = true
		return downDoc(w.last, "the graph could not be read: "+err.Error()), nil
	default:
		return graph.Document{}, err
	}
}

// downDoc is the document of a scope that is down: the last thing the scope
// said, every source marked down and why, and no age, because nothing was
// re-read (H3, contract §6.22). The nodes stay, so a screen draws the graph it
// last had and says beside it that it is not being kept up to date.
//
// Before the child has written anything there is no last graph, and the scope
// itself is still what is known: one source, down, with the read's own words as
// its reason and no nodes. That is a scope whose first read failed, which is a
// document a console draws rather than the child ending with no graph at all —
// which is the whole of H3, at the one instant a child has no last document.
func downDoc(last graph.Document, reason string) graph.Document {
	doc := last
	if len(doc.Sources) == 0 {
		doc = graph.Document{
			Version: graph.DocumentVersion,
			Sources: []graph.SourceState{{Source: graph.Scope{Kind: graph.ScopeCoordinator}.String()}},
			Nodes:   []graph.Node{},
			Edges:   []graph.Edge{},
		}
	}
	for i := range doc.Sources {
		doc.Sources[i].State = graph.SourceDown
		doc.Sources[i].Reason = reason
		// No age is claimed for data that could not be re-read, and none was
		// ever claimed for the stored view itself (contract §6.22).
		doc.Sources[i].AsOf = nil
	}
	return doc
}

// refusedStream is the refusal that ends the child: the database answered 401
// or 403 to the subscription itself, so this CLI may not read the graph at all
// — which is the refusal a console shows as "this CLI has no graph", beside the
// sentence that names the command a person can run instead (T3, contract §6.22,
// §10.5). It is told apart from a scope that is down by exactly that: a
// transport that fell over, or a listen withdrawn, is a state in the document.
func refusedStream() error {
	return fcli.UserErr("graph: the database refused the stream and this build cannot stream the graph: run fugaro graph --json to read it once")
}
