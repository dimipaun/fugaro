// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
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
//   - **One subscription per scope subtree.** The child opens one RTDB
//     connection per subtree the scope covers and no more (H6, F12): an RTDB
//     stream is a held connection against a project-wide limit, so it is opened
//     per scope and never per node or per repository. The subtrees are
//     `graph.ScopeSubtrees`' — the coordinator's and the orchestrator's for
//     `all`, one node for a named orchestrator scope — which is the same
//     function a second reader of the same scope reads, so two readers cannot
//     disagree about what one subscription costs.
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
// and refused (runGraph), and the cloud is open: this subscribes to the
// subtrees the scope reads — one connection each (H6) — and prints until the
// context ends or its reader goes away.
//
// db is the connection the command already opened, and it is the caller's for
// the same reason it is readGraph's: a project with no database is refused by
// that one call, with the sentence `graph --json` refuses it with, rather than
// here with a second check worded alike; and the store over it, the
// subscription on it and every read behind a document share one client, one
// credential and one connection.
func runGraphWatch(ctx context.Context, out io.Writer, env *fcli.Cloud, db *rtdb.Client, scope graph.Scope) error {
	subs, err := graph.ScopeSubtrees(scope)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	// The store the child reads through is opened once, over the same client the
	// subscriptions are on, and lives as long as the child: a document per
	// change, per keep-alive and per retry of a failed read, each answered by a
	// read that goes through it (readGraph, StoredProjectThrough). One per
	// document would build an rtdb client, and mint its credential, on every
	// change a console is watching for, which is the cost this one store is here
	// to take off it.
	stream, err := graph.NewStream()
	if err != nil {
		return fcli.RemoteErr(err)
	}
	stream.Floor, stream.Keep = graphFloor, graphKeepAlive
	w := &watcher{env: env, out: out, st: stream, store: coordinator.NewStore(db), db: db, scope: scope, subs: subs}
	return w.run(ctx)
}

// watcher is the child's own state: the last document it wrote, whether the
// subscription is up, and whether a change is waiting for the floor to pass.
type watcher struct {
	env *fcli.Cloud
	out io.Writer
	st  graph.Stream
	// store is the connection every document's read goes through, and the same
	// one for as long as the child lives (runGraphWatch).
	store *coordinator.Store
	// db is that connection's client, which the orchestrator scopes are read
	// through: the same client the subscriptions are on, so one credential
	// serves the stream and every read behind it (runGraphWatch).
	db *rtdb.Client
	// scope is what the caller asked for, and subs are the subtrees it covers:
	// one held connection each (graph.ScopeSubtrees, H6). They are the child's
	// own fields rather than the read's because the first snapshot waits for one
	// opening put per subtree, which is a fact about the connections and not
	// about the document.
	scope graph.Scope
	subs  []string
	// base is the document the last read answered, and what a scope that is
	// down still draws: the nodes are the last thing the scope said, and the
	// state beside them is what has changed (H3). A document written because a
	// scope is down is not kept here: it says nothing new about the scopes that
	// are live, and one of them would be left carrying the state it had while
	// its own feed was over.
	base graph.Document
	// down is why a subscription's feed is over, by subtree, and empty while
	// every one of them is live. It is per subtree and not one state for the
	// child, because the children of `all` hold two subscriptions and one of
	// them being over says nothing about the other: a put on a live subtree does
	// not clear a listen the server withdrew on a dead one, and the document a
	// console draws says so per scope (H6, contract §10.6).
	//
	// Two things set one: a transport that fell over, which the subtree's own
	// next put clears, and a listen the server withdrew, which nothing clears
	// while the child lives because no subscription is left to deliver the put
	// that would — that scope then stays down exactly as `watch`'s view stays
	// offline (contract §10.6).
	down map[string]string
	// dirty is a change that has not been printed yet, either because it has
	// just arrived, because the floor has not passed since the last document, or
	// because the read that would have answered it failed and has to be tried
	// again.
	dirty bool
}

// run is the child's whole life: subscribe, print the first snapshot, then
// print whenever one is due until the context ends or a reader goes away.
func (w *watcher) run(ctx context.Context) error {
	events := w.subscribe(ctx)
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
		case e, ok := <-events:
			if !ok {
				// The merged channel closes when every subtree's subscription is
				// over, and each of them has already reported its own
				// withdrawal; the context was checked above, so this is that. The
				// child is not refused by it: its scope goes down and it stays
				// alive with the last graph, which is `watch`'s rule for the same
				// event (contract §10.6) and the rule this scope's plan pins
				// (CG7, F20(3)). Nothing arrives on a closed channel again, so it
				// is dropped from the select rather than read as an endless
				// stream of zero events.
				events = nil
				for _, sub := range w.subs {
					w.feedDead(sub, withdrawnListen)
				}
				w.dirty = true
			} else if err := w.observe(e.subtree, e.ev); err != nil {
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

// streamEvent is one event of one subscription: the subtree it came from and
// the event itself. The tag is what the first snapshot counts, because two
// subtrees send the same opening put and the event alone does not say which
// subscription carried it.
type streamEvent struct {
	subtree string
	ev      rtdb.Event
}

// subscribe opens one held connection per subtree the scope covers (H6) and
// merges their events into one channel, each tagged with its subtree.
//
// The merge is what lets the rest of the child be written for one stream of
// events: a change is a change whichever subtree it came from — the read behind
// the document covers every scope it draws — and only the first snapshot needs
// to tell the subtrees apart, which the tag gives it.
//
// Each goroutine ends when its own subscription does, and closes nothing: the
// channel the transport hands out ends after a cancel, which is delivered as
// the event it is, so a closed subscription is one the child has already been
// told about. The merged channel closes when the last of them is done, which is
// the same thing one level up.
func (w *watcher) subscribe(ctx context.Context) <-chan streamEvent {
	merged := make(chan streamEvent)
	var wg sync.WaitGroup
	for _, subtree := range w.subs {
		events := w.db.Stream(ctx, subtree)
		wg.Add(1)
		go func(subtree string, events <-chan rtdb.Event) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					select {
					case merged <- streamEvent{subtree: subtree, ev: ev}:
					case <-ctx.Done():
						return
					}
				}
			}
		}(subtree, events)
	}
	go func() {
		wg.Wait()
		close(merged)
	}()
	return merged
}

// first prints the stream's first document: the full snapshot, taken once every
// subscription has proved it is listening — each transport's own opening put —
// or after the bound, whichever comes first.
//
// Waiting for those puts is what keeps the first snapshot from being two: a put
// is what a reconnect delivers as well, so a child that printed before they
// arrived would print a second, identical document a floor later. One put per
// subtree is what makes the wait complete: `all` covers two subtrees, and a
// child that printed on the first of them would print again on the second. The
// bound is what keeps the wait from being open-ended: a database that never
// answers still gets the read it can make.
func (w *watcher) first(ctx context.Context, events <-chan streamEvent) error {
	timer := time.NewTimer(graphFirstSnapshot)
	defer timer.Stop()
	open := make(map[string]bool, len(w.subs))
	for len(open) < len(w.subs) {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return w.write(ctx, time.Now())
		case e, ok := <-events:
			if !ok {
				// Every subscription is over before any of them proved it was
				// listening, or the context ended. For a withdrawal no put is
				// coming, so waiting for one waits for the bound to pass for
				// nothing: the first snapshot is written now, with its scope
				// down, and the child is not refused by it (H3, contract §10.6).
				if ctx.Err() != nil {
					return nil
				}
				for _, sub := range w.subs {
					w.feedDead(sub, withdrawnListen)
				}
				w.dirty = true
				return w.write(ctx, time.Now())
			}
			switch {
			case e.ev.Type == "put" && e.ev.Path == "/":
				open[e.subtree] = true
			case e.ev.Type == "cancel":
				// This subtree's listen is over before its opening put ever
				// came, so no put is coming for it and waiting for one would
				// wait for the bound to pass for nothing. The scope is down
				// from here on, and the other subtrees are still counted.
				open[e.subtree] = true
				w.feedDead(e.subtree, withdrawnListen)
				w.dirty = true
			case e.ev.Type == "error" && errors.Is(e.ev.Err, rtdb.ErrPermission):
				return refusedStream(e.subtree, e.ev.Err)
			}
		}
	}
	return w.write(ctx, time.Now())
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
//
// subtree is the subtree the event came from, and it is carried through to the
// refusal's sentence: an event is one of the child's subscriptions, and which
// one is a fact only the tag knows (subscribe).
func (w *watcher) observe(subtree string, ev rtdb.Event) error {
	switch ev.Type {
	case "put", "patch":
		// This subtree's feed is live again — a reconnect delivers a full put —
		// and no other subtree's is spoken for by it (H6).
		w.feedLive(subtree)
		w.dirty = true
	case "cancel":
		// The server withdrew the listen — a rule no longer allows it — and the
		// transport does not reconnect after a cancel, so this child's
		// subscription is over. The scope goes down and the child stays alive
		// (contract §10.6): the listen it was holding stopped being allowed,
		// which is not the same as this CLI being refused the read it makes, and
		// it is the read's refusal that ends a child.
		w.feedDead(subtree, withdrawnListen)
		w.dirty = true
	case "auth_revoked":
		// The credential the subscription was made with expired. The transport
		// mints another and reconnects itself, and what follows is a full put,
		// so this is a change like any other; a revoke loop the transport gives
		// up on arrives as an "error" below instead.
		w.dirty = true
	case "error":
		if errors.Is(ev.Err, rtdb.ErrPermission) {
			return refusedStream(subtree, ev.Err)
		}
		w.feedDead(subtree, "the database stream is down: "+ev.Err.Error())
		w.dirty = true
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
	return graph.WriteDoc(w.out, w.st.Next(doc, now))
}

// snapshot is the document this instant's read makes — with the sources under a
// feed that is over marked down on top of it — or, when the read cannot be made,
// the last one it made with every source marked down: a scope that is down is a
// state, and the last thing it said is what a reader still draws beside it (H3).
//
// The read is made whether or not a subscription is still delivering. A feed
// that is over is one subscription, and the read is not that subscription's: a
// withdrawn listen leaves the scope behind it frozen, and skipping the read
// would freeze the scopes that are still listening with it — under `all`, one
// dead feed would stop every change on the live one from ever reaching a reader
// (H3, H4, H6).
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
	var warned bytes.Buffer
	// The read's own instant is the age the stream can give (contract §6.22):
	// it is the caller that read the views and knows when (CG7), where the
	// one-shot read has no such time to hand over.
	doc, err := readGraph(ctx, w.env, w.store, w.db, w.scope, now, now, &warned)
	switch {
	case err == nil:
		w.base = doc
		// A source under a feed that is over is drawn from this answer and
		// marked down over it: the read still answers for it, but the
		// subscription that would have said it changed is gone, so that scope
		// is not being kept up to date (H3, §10.6).
		return w.downDoc(doc, w.down), nil
	case fcli.ExitCode(err) != fcli.ExitUserError:
		// A read that failed is every source's: the read covers every scope the
		// child draws, so there is no subtree whose data is still fresh. It is
		// not kept: the flag makes the next floor read again, and the document
		// is only what this instant could not answer (snapshot).
		w.dirty = true
		reason := "the graph could not be read: " + err.Error()
		dead := make(map[string]string, len(w.subs))
		for _, sub := range w.subs {
			dead[sub] = reason
		}
		return w.downDoc(w.base, dead), nil
	default:
		return graph.Document{}, err
	}
}

// feedDead marks one subtree's feed as over, and why.
func (w *watcher) feedDead(subtree, reason string) {
	if w.down == nil {
		w.down = make(map[string]string, len(w.subs))
	}
	w.down[subtree] = reason
}

// feedLive marks one subtree's feed as live again, which is what a put on it
// says: the transport reconnects on its own and each connect begins with a full
// put, so a subtree that fell over is back the moment one arrives. It says
// nothing about any other subtree (H6).
func (w *watcher) feedLive(subtree string) { delete(w.down, subtree) }

// downReason is why the child has a scope that is down at all, and "" when the
// map holds none: the first dead subtree in the order the child holds them,
// which is the one a document with no read behind it is named by.
func (w *watcher) downReason(down map[string]string) string {
	for _, sub := range w.subs {
		if reason, ok := down[sub]; ok {
			return reason
		}
	}
	return ""
}

// sourceSubtree is the subscription a document's source is read under: the
// subtree the child holds that covers it. A named scope is covered by its own
// node when the child subscribed to that node, and otherwise by the subtree
// that holds it — under `all` every orchestrator scope, named or not, is read
// under the one /orchestrator subscription (ScopeSubtrees, H6).
//
// "" is a source nothing covers, which is a source this child cannot say a feed
// about: the strings a document carries are a reader's own, and a hand-made one
// is not this child's. Such a source is left as the last document had it.
func (w *watcher) sourceSubtree(source string) string {
	scope, err := graph.ParseScope(source)
	if err != nil || scope.Kind == graph.ScopeAll {
		return ""
	}
	subs, err := graph.ScopeSubtrees(scope)
	if err != nil || len(subs) != 1 {
		return ""
	}
	for _, sub := range w.subs {
		if sub == subs[0] || strings.HasPrefix(subs[0], sub+"/") {
			return sub
		}
	}
	return ""
}

// downDoc renders a document over the graph a scope that is down still has: the
// sources whose feed is over are marked down and why, with no age on them,
// because the subscription that would have kept them up to date is gone (H3,
// contract §6.22). The nodes stay as the document had them, so a screen draws
// the graph it last had and says beside it that it is not being kept up to date.
//
// Only the sources whose own subtree is over are marked: a child of `all` holds
// two subscriptions, and one of them being dead is not a fact about the scope
// the other one carries — a screen keys every node by the source it came from,
// and a source drawn down while its feed is still delivering would hide a graph
// that is up to date (H3, H6).
//
// What is rendered over is either this instant's read or — when the read itself
// failed — the last one it made, and the states are copied before they are
// marked, so neither of those is changed by the rendering.
//
// Before the child has read anything there is no graph to render over, and the
// scope itself is still what is known: one source, down, with the read's own
// words as its reason and no nodes. That is a scope whose first read failed,
// which is a document a console draws rather than the child ending with no graph
// at all — which is the whole of H3, at the one instant a child has no document
// behind it.
// That source is the scope the child was asked for and never the coordinator by
// default: `all` is a selector and not a source, so it is named by the scope its
// documents list first (contract §6.22), and a child of `orchestrator:<id>` or
// `local:<id>` names that scope, which is the only one it holds.
func (w *watcher) downDoc(over graph.Document, down map[string]string) graph.Document {
	doc := over
	if len(doc.Sources) == 0 {
		doc = graph.Document{
			Version: graph.DocumentVersion,
			Sources: []graph.SourceState{{
				Source: w.scopeName(), State: graph.SourceDown, Reason: w.downReason(down),
			}},
			Nodes: []graph.Node{},
			Edges: []graph.Edge{},
		}
		return doc
	}
	// The states are the caller's to keep: what is rendered over is the read's
	// own answer, which is also the base a failed read is drawn from, and a mark
	// written into its slice would stay in it (snapshot).
	sources := make([]graph.SourceState, len(doc.Sources))
	copy(sources, doc.Sources)
	doc.Sources = sources
	for i := range doc.Sources {
		reason, dead := down[w.sourceSubtree(doc.Sources[i].Source)]
		if !dead {
			continue
		}
		doc.Sources[i].State = graph.SourceDown
		doc.Sources[i].Reason = reason
		// No age is claimed for data that could not be re-read, and none was
		// ever claimed for the stored view itself (contract §6.22).
		doc.Sources[i].AsOf = nil
	}
	return doc
}

// scopeName is the scope this child was asked for, as a document names a source:
// the two scopes that name a node — `orchestrator:<id>` and `local:<id>` — name
// themselves, and everything else is the coordinator, because `all` is a
// selector and never a source and is named by the scope its documents list
// first (contract §6.22).
func (w *watcher) scopeName() string {
	if w.scope.Kind == graph.ScopeOrchestrator || w.scope.Kind == graph.ScopeLocal {
		return w.scope.String()
	}
	return graph.Scope{Kind: graph.ScopeCoordinator}.String()
}

// refusedStream is the refusal that ends the child: the database refused the
// credential this CLI listens with. Three failures arrive as one event of that
// kind — a 401 or 403 on the subscription itself, a token endpoint refusing the
// stored grant (tokenErr: an expired or revoked ADC grant answers 400, 401 or
// 403), and the transport's three revokes in a row — and all three are the same
// fact about the caller: the read is one this CLI may not make, which is what a
// console shows as "this CLI has no graph" (T3, contract §6.22, §10.5), and not
// a scope that is down. It is told apart from a scope that is down by exactly
// that: a transport that fell over, or a listen withdrawn, is a state in the
// document.
//
// The sentence carries the event's own error, as the one-shot read's refusal
// carries its own (coordinatorStoredErr), because the cause is what tells a
// person which of the three it was, and it names what to do about it rather than
// a command to run: `graph --json` reads the same node with the same credential
// and is refused in the same breath, so sending a person there is sending them
// in a circle.
//
// The subtree is the one the refused subscription was on, and it is named
// as it is: a child of `--source all` holds two and a named orchestrator scope
// holds that id's own node (ScopeSubtrees), so a sentence that said
// `/coordinator` for every one of them would send a person to a node this child
// never asked to read — and, for a scope whose own node is the only one it
// holds, name a scope the caller did not ask for at all.
func refusedStream(subtree string, err error) error {
	return fcli.UserErr("graph: the database refused this CLI's credential and it may not listen to /%s: that read is a signed-in reader's and the database role its caller holds (roles/firebasedatabase.viewer, which terraform.operators and terraform.admins have), so run gcloud auth application-default login if the grant has expired, and otherwise ask an owner to add the role (%v)", subtree, err)
}
