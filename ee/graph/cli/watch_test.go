// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// `fugaro graph --watch --json` (CG7 of
// docs/cli-output-console-graph/implementation-plan-graph.md): the long-lived
// child the console spawns, driven here against the fake database the one-shot
// read's tests already use.
//
// What each test holds:
//
//   - TestWatchFirstSnapshotThenOneDocumentPerChange: a first snapshot within a
//     bounded time, then one whole document per change and nothing between two
//     of them.
//   - TestWatchSeqRisesAndStreamIDIsStable: the sequence rises by one within
//     one child, and every document carries that child's own stream_id (H4).
//   - TestWatchKeepAliveWhenQuiet: a stream with nothing to say still writes,
//     on a timer, because an orphaned child dies on its next write (F11, F20).
//     This is the plan's mutation: drop the keep-alive and it fails.
//   - TestWatchReconnectSendsAFullSnapshotAndBumpsSeq: a transport that fell
//     over is a state in the document, and the subscription's own reconnect
//     prints a whole snapshot with the next sequence (H4, F12). This is the
//     plan's second mutation: drop the sequence bump on reconnect and it fails.
//   - TestWatchSourceDownIsAStateNotAnExit: a scope that cannot be reached is a
//     state beside the last nodes, never an exit (H3).
//   - TestWatchClosedStdoutEndsTheChild: a reader that is gone ends the child
//     on its next write (H5, §10.4).
//   - TestWatchSlowReaderBlocksAndNeverDrops: a slow reader makes the child
//     block rather than lose a document (H5, §10.3).
//   - TestWatchOneSubscriptionPerScope: one held connection for the scope,
//     however many repositories and nodes it has (H6, F12).
//   - TestWatchAWithdrawnListenIsADownScopeAndNotAnExit: the server taking the
//     listen away is a scope that is down, not a refusal: the last graph stays,
//     the child stays alive and keeps its clock (§10.6, F20(3), the plan's CG7).
//   - TestWatchAWithdrawnListenIsADownScopeAtEveryInstantItCanLand: the same
//     for the two instants the fake cannot produce — the cancel event itself,
//     and a withdrawal before the opening put ever arrived, which is a document
//     rather than a wait for a bound that nothing can beat.
//   - TestWatchRefusedReadIsTerminalAndNamesTheCommand: the listen *refused* —
//     a 401 or 403 on the subscription itself — ends the child and names the
//     command, which is what a console reads as "this CLI has no graph" (T3).
//   - TestWatchRefusedReadIsTerminalWhileTheListenStands: a *read* this CLI is
//     refused, with the listen served throughout, is the other refusal: exit 1
//     and no document, where a read that merely failed is a scope marked down
//     (H3, T3).
//   - TestWatchFirstSnapshotIsBoundedWhenTheListenIsSilent: the first document
//     does not wait for a subscription that never speaks, because the read
//     behind it needs no listen.
//   - TestWatchAFirstReadThatFailsIsADownScopeWithNoNodes: a child whose first
//     read failed prints the scope, down, with an empty graph rather than ending
//     (H3), and that document carries `[]` where §6.22 has a list.
//   - TestWatchAuthRevokedIsAChangeAndNotAnExit: an expired credential is a
//     change the child prints, not a refusal: the transport reconnects itself.
//
// The tests shorten the stream's clocks (fast) and read the child's stdout line
// by line (watchSink), because the guarantees are about time and about a reader
// that is slow or gone, and neither can be seen from a document read after the
// fact.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The clocks these tests drive the stream with: a floor of milliseconds rather
// than a second, and a quiet stream that keeps alive within a test's patience.
const (
	testFloor = 20 * time.Millisecond
	testKeep  = 5 * time.Second
	// testPatience is how long a test waits for something the child must do.
	// It is generous because it is a failure budget, never a sleep: the child
	// answers in milliseconds when it is working.
	testPatience = 10 * time.Second
)

// fast shortens the stream's clocks for one test. Tests in a package run one at
// a time, so a test that changes them puts them back; the restore is registered
// first, so it runs after the child's own cleanup has stopped the child.
func fast(t *testing.T, floor, keep time.Duration) {
	t.Helper()
	prevFloor, prevKeep := graphFloor, graphKeepAlive
	graphFloor, graphKeepAlive = floor, keep
	t.Cleanup(func() { graphFloor, graphKeepAlive = prevFloor, prevKeep })
}

// boundFirst shortens the bound on the first snapshot, for the one test that
// leaves the subscription silent: the production bound is five seconds, which
// is a test's whole patience, and the var exists so a test can drive it.
func boundFirst(t *testing.T, d time.Duration) {
	t.Helper()
	prev := graphFirstSnapshot
	graphFirstSnapshot = d
	t.Cleanup(func() { graphFirstSnapshot = prev })
}

// watchSink is the child's stdout as a test reads it: every complete line is
// delivered on a channel as it is written, and hook is called before every
// write, so a test can hold one (a reader that stopped reading) or make it fail
// (a reader that is gone). n counts from 1.
type watchSink struct {
	hook func(n int) error
	ch   chan string

	mu     sync.Mutex
	buf    bytes.Buffer
	lines  []string
	writes int
}

func newWatchSink() *watchSink { return &watchSink{ch: make(chan string, 64)} }

func (s *watchSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.writes++
	n := s.writes
	s.mu.Unlock()
	if s.hook != nil {
		if err := s.hook(n); err != nil {
			return 0, err
		}
	}
	var out []string
	s.mu.Lock()
	s.buf.Write(p)
	for {
		line, rest, ok := strings.Cut(s.buf.String(), "\n")
		if !ok {
			break
		}
		s.buf.Reset()
		s.buf.WriteString(rest)
		s.lines = append(s.lines, line)
		out = append(out, line)
	}
	s.mu.Unlock()
	for _, line := range out {
		s.ch <- line
	}
	return len(p), nil
}

// text is every line the child has written so far, for a failure message.
func (s *watchSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

// next waits for the child's next document and reads it the way a reader
// outside this binary does: strictly, one object per line.
func (s *watchSink) next(t *testing.T, within time.Duration) graph.Document {
	t.Helper()
	line := s.nextLine(t, within)
	doc, err := graph.ParseDocument([]byte(line))
	if err != nil {
		t.Fatalf("the child wrote a line that is not a document: %v: %s", err, line)
	}
	return doc
}

// nextLine is the child's next document as a reader outside this binary gets
// it: the line itself, for a rule about the JSON rather than about the parsed
// document — §6.22's table has no null in an empty list, and a strict reader
// (the console) decodes this exact text.
func (s *watchSink) nextLine(t *testing.T, within time.Duration) string {
	t.Helper()
	select {
	case line := <-s.ch:
		return line
	case <-time.After(within):
		t.Fatalf("no document within %s; the child wrote:\n%s", within, s.text())
		return ""
	}
}

// quiet fails if the child writes anything within d: "one document per change"
// and "a slow reader blocks the child" are both silences a test has to hold it
// to.
func (s *watchSink) quiet(t *testing.T, d time.Duration, why string) {
	t.Helper()
	select {
	case line := <-s.ch:
		t.Fatalf("the child wrote %s, want nothing: %s", line, why)
	case <-time.After(d):
	}
}

// watchChild is a `graph …` command running with a context a test can end.
type watchChild struct {
	errOut  bytes.Buffer
	cancel  context.CancelFunc
	done    chan error
	stopped bool
}

// startWatch runs args and returns the child running. The command is stopped
// when the test ends, so a test that never expects it to return still leaves no
// goroutine behind.
func startWatch(t *testing.T, out io.Writer, args ...string) *watchChild {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &watchChild{cancel: cancel, done: make(chan error, 1)}
	cmd := graphTestRoot()
	cmd.SetOut(out)
	cmd.SetErr(&c.errOut)
	cmd.SetArgs(fcli.NormalizeArgs(args))
	go func() { c.done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		if c.stopped {
			return
		}
		cancel()
		select {
		case <-c.done:
		case <-time.After(testPatience):
			t.Errorf("the child did not end after its context was cancelled; it wrote:\n%s", c.errOut.String())
		}
	})
	return c
}

// wait is the child's own end and the error it ended on, which fails the test
// if it is still running.
func (c *watchChild) wait(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-c.done:
		c.stopped = true
		return err
	case <-time.After(within):
		t.Fatalf("the child was still running after %s, want it to have ended; stderr:\n%s", within, c.errOut.String())
		return nil
	}
}

// stillRunning fails the test if the child has ended: a scope that is down is a
// state rather than an exit (H3), and this is the half of that which says the
// child is still there.
func (c *watchChild) stillRunning(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case err := <-c.done:
		c.stopped = true
		t.Fatalf("the child ended with %v, want it to keep streaming", err)
	case <-time.After(within):
	}
}

// changedView and newestView are the fixture's repository reconciled again: the
// same pull request, another number and title, on another release. A stream's
// documents are compared across a write, and a view that differed in nothing
// would be no change at all. There are two of them because a test that holds
// the reader still has to be able to make a change while the child is stuck
// writing another one.
func changedView() coordinator.View { return reconciledView(13, "the changed title", "0.12.0", "bbbb") }

func newestView() coordinator.View { return reconciledView(14, "the newest title", "0.13.0", "cccc") }

func reconciledView(number int, title, release, suffix string) coordinator.View {
	branch := "release/v" + release
	return coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{{
			PR: &coordinator.PullRequest{
				Number: number, URL: "https://github.com/acme/app/pull/" + strconv.Itoa(number),
				Title: title, Branch: branch, State: coordinator.StateMerged,
			},
			Runs: []coordinator.Run{{
				ID: "20261014-100000-" + suffix, PR: number, Status: "succeeded",
				Branch: branch, DeclaredBy: "someone@example.com", Files: []string{},
			}},
			Branch: branch, Release: release, InFlight: true,
			Files: []string{}, Services: []string{},
		}},
		Collisions: []coordinator.Collision{},
	}
}

// TestWatchFirstSnapshotThenOneDocumentPerChange: the first snapshot arrives
// promptly and is a whole document of the stream, and every change after it is
// one more document — not a delta, and not two documents for one change
// (D1360, F19).
func TestWatchFirstSnapshotThenOneDocumentPerChange(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	startWatch(t, sink, "graph", "--watch", "--json")

	first := sink.next(t, testPatience)
	if first.StreamID == "" || first.Seq != 1 {
		t.Fatalf("the first document carries stream_id %q and seq %d, want a stream id and 1", first.StreamID, first.Seq)
	}
	if len(first.Sources) != 1 || first.Sources[0].State != graph.SourceUp {
		t.Errorf("the first document's sources are %+v, want the coordinator up", first.Sources)
	}
	if len(first.Nodes) != 1 || first.Nodes[0].Title != "the stored title" {
		t.Fatalf("the first document's nodes are %+v, want the stored record", first.Nodes)
	}

	f.writeStoredView(t, changedView())

	second := sink.next(t, testPatience)
	if second.StreamID != first.StreamID {
		t.Errorf("the second document's stream_id is %q, want the first's %q: one child is one stream", second.StreamID, first.StreamID)
	}
	if second.Seq != first.Seq+1 {
		t.Errorf("the second document is seq %d, want %d", second.Seq, first.Seq+1)
	}
	if len(second.Nodes) != 1 || second.Nodes[0].Title != "the changed title" {
		t.Fatalf("the second document's nodes are %+v, want the changed record: a change is a whole new document", second.Nodes)
	}
	if got := second.Nodes[0].Release; got != "0.12.0" {
		t.Errorf("the changed node's release is %q, want 0.12.0", got)
	}

	sink.quiet(t, 20*testFloor, "one change, one document")
}

// TestWatchFirstSnapshotIsBoundedWhenTheListenIsSilent: the child answers
// within a bounded time whatever the database does (H3, §6.22). The listen is
// the only thing it waits for before its first document — the transport's own
// opening put, which is also what a reconnect delivers — so a subscription the
// server accepts and then says nothing on would leave a screen with no document
// and no refusal at all if that wait had no bound. The read behind the first
// snapshot needs no listen: it is answered here while the subscription is still
// open and silent.
func TestWatchFirstSnapshotIsBoundedWhenTheListenIsSilent(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	boundFirst(t, 50*time.Millisecond)
	fast(t, testFloor, testKeep)

	// The listen is accepted and held before its opening put: the client's
	// request is stamped and answered with nothing, which is a database that is
	// reachable, slow to speak and never refused.
	stamped, release := f.db.HoldNext(coordpath.PathRoot)
	defer release()

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")
	<-stamped

	first := sink.next(t, testPatience)
	if len(first.Nodes) != 1 || first.Nodes[0].Title != "the stored title" {
		t.Fatalf("the bounded first snapshot is %+v, want the whole stored graph", first.Nodes)
	}
	if len(first.Sources) != 1 || first.Sources[0].State != graph.SourceUp {
		t.Errorf("the bounded first snapshot's sources are %+v, want the coordinator up: the read answered", first.Sources)
	}
	if first.Seq != 1 {
		t.Errorf("the bounded first snapshot is seq %d, want 1", first.Seq)
	}
	child.stillRunning(t, 20*testFloor)

	// And the held listen is the child's own subscription, not a second one:
	// releasing it delivers the opening put, which the child reads as the change
	// it is.
	release()
	after := sink.next(t, testPatience)
	if after.StreamID != first.StreamID || after.Seq != first.Seq+1 {
		t.Errorf("the document after the opening put is %q seq %d, want %q seq %d: one child, one stream",
			after.StreamID, after.Seq, first.StreamID, first.Seq+1)
	}
}

// TestWatchSeqRisesAndStreamIDIsStable: three changes are three documents whose
// sequences are 1, 2 and 3 under one stream_id, which is what a reader compares
// a sequence inside (H4).
func TestWatchSeqRisesAndStreamIDIsStable(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	startWatch(t, sink, "graph", "--watch", "--json")

	docs := []graph.Document{sink.next(t, testPatience)}
	for i := 0; i < 2; i++ {
		if i == 0 {
			f.writeStoredView(t, changedView())
		} else {
			f.writeStoredView(t, storedView())
		}
		docs = append(docs, sink.next(t, testPatience))
	}
	for i, doc := range docs {
		if doc.Seq != uint64(i+1) {
			t.Errorf("document %d carries seq %d, want %d", i+1, doc.Seq, i+1)
		}
		if doc.StreamID != docs[0].StreamID {
			t.Errorf("document %d carries stream_id %q, want %q", i+1, doc.StreamID, docs[0].StreamID)
		}
	}
}

// TestWatchKeepAliveWhenQuiet: a graph that does not change still prints, on a
// timer and with the next sequence, because that write is how an orphaned child
// finds out its reader is gone (F11, F20). It is an ordinary document of the
// stream: same identity, same sources, same nodes.
func TestWatchKeepAliveWhenQuiet(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	// A keep-alive inside a test's patience: the same rule, a shorter clock.
	fast(t, testFloor, 60*time.Millisecond)

	sink := newWatchSink()
	startWatch(t, sink, "graph", "--watch", "--json")

	first := sink.next(t, testPatience)
	second := sink.next(t, testPatience)
	if second.StreamID != first.StreamID {
		t.Errorf("the keep-alive's stream_id is %q, want %q", second.StreamID, first.StreamID)
	}
	if second.Seq != first.Seq+1 {
		t.Errorf("the keep-alive is seq %d, want the next one, %d", second.Seq, first.Seq+1)
	}
	if !second.GeneratedAt.After(first.GeneratedAt) {
		t.Errorf("the keep-alive was stamped %s, want later than the first document's %s", second.GeneratedAt, first.GeneratedAt)
	}
	if len(second.Nodes) != len(first.Nodes) || len(second.Sources) != len(first.Sources) {
		t.Errorf("the keep-alive is %+v, want the same graph as %+v", second, first)
	}
	// And it repeats, rather than being one delayed write.
	third := sink.next(t, testPatience)
	if third.Seq != second.Seq+1 {
		t.Errorf("the second keep-alive is seq %d, want %d", third.Seq, second.Seq+1)
	}
}

// TestWatchReconnectSendsAFullSnapshotAndBumpsSeq: a subscription whose
// connection fell over is a scope that is down in the document — a state, and
// not an exit — and the reconnect that follows is a whole snapshot with the
// next sequence, so a reader never has to ask what it missed (H3, H4, F12).
func TestWatchReconnectSendsAFullSnapshotAndBumpsSeq(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")

	first := sink.next(t, testPatience)
	if first.Sources[0].State != graph.SourceUp {
		t.Fatalf("the first document's source is %+v, want it up", first.Sources[0])
	}

	// The connection goes away without a word, as a network does.
	f.db.DropStreams()

	down := sink.next(t, testPatience)
	if down.Sources[0].State != graph.SourceDown {
		t.Fatalf("the source is %+v while the stream is gone, want it down", down.Sources[0])
	}
	if down.Sources[0].Reason == "" {
		t.Errorf("a down source carries no reason: %+v", down.Sources[0])
	}
	if len(down.Nodes) != len(first.Nodes) {
		t.Errorf("the down document drew %d nodes, want the last known %d", len(down.Nodes), len(first.Nodes))
	}
	if down.Seq != first.Seq+1 {
		t.Errorf("the down document is seq %d, want %d", down.Seq, first.Seq+1)
	}
	child.stillRunning(t, 20*testFloor)

	// The transport reconnects on its own, and begins with a full put: the next
	// document is the whole graph again, with the next sequence.
	back := sink.next(t, testPatience)
	if back.Sources[0].State != graph.SourceUp {
		t.Fatalf("the source after the reconnect is %+v, want it up again", back.Sources[0])
	}
	if len(back.Nodes) != len(first.Nodes) {
		t.Errorf("the reconnected snapshot drew %d nodes, want the whole graph's %d", len(back.Nodes), len(first.Nodes))
	}
	if back.Seq != down.Seq+1 {
		t.Errorf("the reconnected snapshot is seq %d, want the next one after the down document, %d", back.Seq, down.Seq+1)
	}
}

// TestWatchSourceDownIsAStateNotAnExit: a scope the read answers nothing for is
// a state beside the nodes the document still carries (H3). Both ways a scope
// is down are here — nothing stored, which the read itself reports, and a
// subscription that cannot be reached — and neither ends the child.
func TestWatchSourceDownIsAStateNotAnExit(t *testing.T) {
	t.Run("nothing is stored", func(t *testing.T) {
		f := newGraphFixture(t)
		// A project whose coordinator has never reconciled: the read answers
		// with the runs half and the scope's state says so.
		f.seedRun(t)
		fast(t, testFloor, testKeep)

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")

		first := sink.next(t, testPatience)
		if first.Sources[0].State != graph.SourceDown {
			t.Fatalf("the scope is %+v, want it down: nothing is stored for it", first.Sources[0])
		}
		if len(first.Nodes) == 0 {
			t.Errorf("a down scope drew no nodes, want the runs half it still has")
		}
		child.stillRunning(t, 20*testFloor)

		// The sweep reconciles it, and the very same child says so.
		f.writeStoredView(t, storedView())
		next := sink.next(t, testPatience)
		if next.Sources[0].State != graph.SourceUp {
			t.Errorf("the scope is %+v after the sweep stored a view, want it up", next.Sources[0])
		}
		if next.Seq != first.Seq+1 {
			t.Errorf("the document after the reconcile is seq %d, want %d", next.Seq, first.Seq+1)
		}
	})

	t.Run("the subscription cannot be reached", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())
		fast(t, testFloor, testKeep)

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")
		first := sink.next(t, testPatience)

		// The database stops answering and the connection dies with it.
		f.db.Refuse(503, "503", "Service Unavailable", "the database is down")
		f.db.DropStreams()

		down := sink.next(t, testPatience)
		if down.Sources[0].State != graph.SourceDown {
			t.Fatalf("the scope is %+v with the subscription gone, want it down", down.Sources[0])
		}
		if !strings.Contains(down.Sources[0].Reason, "down") {
			t.Errorf("the down scope's reason is %q, want the transport's own words", down.Sources[0].Reason)
		}
		child.stillRunning(t, 20*testFloor)
		if down.Seq != first.Seq+1 {
			t.Errorf("the down document is seq %d, want %d", down.Seq, first.Seq+1)
		}
	})
}

// TestWatchTransientReadFailureIsADownScopeAndIsRetried: the subscription is
// up and the read behind a document fails — a database answering 503, a bucket
// that refused a listing — which is a scope that is down and never the end of
// the child (H3). The last document stays beside the state, the change stays
// pending so the next floor reads again rather than waiting for something to
// change, and the same child says up with the next sequence once the database
// answers again. A long-lived stream is the one output that has to survive
// this: a console holds it open for as long as the tab is, and a child that
// exits on a blip takes the graph down with it until someone notices.
//
// The retry is the failed read's own doing and nothing else's: the keep-alive
// stays at the test's five seconds, so a document a few floors after the down
// one can only be the pending change that same read left behind. With a
// keep-alive inside the test's patience the timer alone re-reads, and then a
// regression that dropped the pending change — recovery slowing from the floor
// to half a minute — would pass this test.
func TestWatchTransientReadFailureIsADownScopeAndIsRetried(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")
	first := sink.next(t, testPatience)
	if len(first.Nodes) != 1 || first.Sources[0].State != graph.SourceUp {
		t.Fatalf("the first document is %+v, want the stored graph up", first)
	}

	// The database stops answering the view's read, and the subscription — a
	// connection that is already open — is untouched by it. The refusal is on
	// the view's own path, so the scope still has changes to report: a write
	// anywhere in the subtree the subscription watches is one, and the read it
	// triggers is the one that fails. The change is a declaration the
	// coordinator's front door wrote, which the graph's own read does not
	// consume: what the child draws while its read fails is the graph it had and
	// not the change, and what it draws when the read is answered again is the
	// stored view, up.
	intent := coordinator.Intent{DeclaredBy: "someone@example.com", At: fcli.LsNow().UTC(), Files: []string{}, Services: []string{}}
	f.db.RefusePath(coordinator.PathView(appSlug), http.StatusServiceUnavailable, "503", "Service Unavailable", "the database is down")
	if err := f.store(t).Write(context.Background(), coordinator.PathIntent(appSlug, coordinator.IntentID(intent)), intent); err != nil {
		t.Fatal(err)
	}

	down := sink.next(t, testPatience)
	if down.Sources[0].State != graph.SourceDown {
		t.Fatalf("the scope is %+v while its read fails, want it down", down.Sources[0])
	}
	if !strings.Contains(down.Sources[0].Reason, "503") {
		t.Errorf("the down scope's reason is %q, want the read's own failure in it", down.Sources[0].Reason)
	}
	if len(down.Nodes) != len(first.Nodes) {
		t.Errorf("the down document drew %d nodes, want the last known %d", len(down.Nodes), len(first.Nodes))
	}
	if down.Seq != first.Seq+1 {
		t.Errorf("the down document is seq %d, want %d", down.Seq, first.Seq+1)
	}
	if down.StreamID != first.StreamID {
		t.Errorf("the down document's stream_id is %q, want the first's %q: the child did not restart", down.StreamID, first.StreamID)
	}

	// And it is retried rather than waited on: the next floor reads again, which
	// is what makes the recovery below the child's own doing and not a change's.
	// A few floors is the whole of the patience here: the keep-alive is seconds
	// away, so a document this soon is the pending change and nothing else.
	before := len(f.db.Requests())
	retry := sink.next(t, 20*testFloor)
	if retry.Sources[0].State != graph.SourceDown {
		t.Errorf("the document after the down one is %+v, want the scope still down until a read succeeds", retry.Sources[0])
	}
	if retry.Seq != down.Seq+1 {
		t.Errorf("the retried document is seq %d, want %d", retry.Seq, down.Seq+1)
	}
	if got := len(f.db.Requests()); got == before {
		t.Errorf("the child wrote another document without reading again: a read that failed is retried, not waited on")
	}
	child.stillRunning(t, 20*testFloor)

	// The database answers again, and the same child is up again: the whole
	// document, the next sequence, and one stream_id throughout.
	f.db.RefusePath(coordinator.PathView(appSlug), 0, "", "", "")

	var back graph.Document
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); {
		doc := sink.next(t, testPatience)
		if len(doc.Sources) > 0 && doc.Sources[0].State == graph.SourceUp {
			back = doc
			break
		}
	}
	if len(back.Sources) == 0 {
		t.Fatalf("the scope never came back up; the child wrote:\n%s", sink.text())
	}
	if back.StreamID != first.StreamID {
		t.Errorf("the recovered document's stream_id is %q, want %q", back.StreamID, first.StreamID)
	}
	if back.Seq <= retry.Seq {
		t.Errorf("the recovered document is seq %d, want the next one after the down document's %d", back.Seq, retry.Seq)
	}
	if len(back.Nodes) != len(first.Nodes) || back.Nodes[0].Title != first.Nodes[0].Title {
		t.Errorf("the recovered document drew %+v, want the whole stored graph again %+v", back.Nodes, first.Nodes)
	}
	child.stillRunning(t, 20*testFloor)
}

// TestWatchRefusedReadIsTerminalWhileTheListenStands: a read this CLI is
// *refused* — a 403 on GET /coordinator/repos/<slug>/view, the role its caller
// does not hold — is the one failure of a read that ends the child, and it is
// the CLI's own classification (ExitCode) that tells it apart from a read that
// merely failed: a read `graph --json` would exit 2 for is a scope that is down,
// which the test above holds, and a read it would exit 1 for is this (H3, T3,
// §6.22).
//
// Nothing else here is refused: the repository's own paths are the only ones
// the database answers 403, so the subscription is served throughout and the
// sentence the child ends on is the read's, not the stream's. That is what a
// console reads as "this CLI has no graph"; a document with the scope marked
// down would say the opposite, and a child that kept running would never say
// anything at all.
//
// Both instants are held, because they reach the arm from the two places it can
// be reached: the first read, whose refusal leaves no document, and a read a
// later floor makes, whose refusal leaves the graph the child had already drawn
// and nothing after it.
func TestWatchRefusedReadIsTerminalWhileTheListenStands(t *testing.T) {
	t.Run("the first read", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())
		fast(t, testFloor, testKeep)

		// Only the repository's own paths: the subscription's path is the
		// coordinator's subtree, which this refusal does not touch.
		f.db.RefusePath("/repos/", http.StatusForbidden, "Forbidden", "PERMISSION_DENIED", "Permission denied")

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")

		err := child.wait(t, testPatience)
		if err == nil {
			t.Fatalf("the child kept streaming a graph whose read it is refused; it wrote:\n%s", sink.text())
		}
		if got := fcli.ExitCode(err); got != fcli.ExitUserError {
			t.Errorf("a refused read exits %d, want %d: it is the caller's, not the database's", got, fcli.ExitUserError)
		}
		for _, want := range []string{"refused the read", "roles/firebasedatabase.viewer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal is %q, want the stored view's own sentence, naming %q", err, want)
			}
		}
		if got := sink.text(); got != "" {
			t.Errorf("the refused read wrote %q, want no document at all: a scope marked down is drawn as a graph that is down, which is not what a refusal is", got)
		}
	})

	t.Run("a read after the first snapshot", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())
		// A keep-alive inside a test's patience: it is what makes the next read
		// due with nothing changed, so this refusal lands on the child's own
		// floor and there is no change to race.
		fast(t, testFloor, 60*time.Millisecond)

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")
		first := sink.next(t, testPatience)
		if len(first.Nodes) != 1 || first.Sources[0].State != graph.SourceUp {
			t.Fatalf("the first document is %+v, want the stored graph up", first)
		}

		f.db.RefusePath("/repos/", http.StatusForbidden, "Forbidden", "PERMISSION_DENIED", "Permission denied")

		err := child.wait(t, testPatience)
		if err == nil {
			t.Fatalf("the child kept streaming a graph whose read it is refused; it wrote:\n%s", sink.text())
		}
		if got := fcli.ExitCode(err); got != fcli.ExitUserError {
			t.Errorf("a refused read exits %d, want %d: it is the caller's, not the database's", got, fcli.ExitUserError)
		}
		if !strings.Contains(err.Error(), "refused the read") {
			t.Errorf("the refusal is %q, want the read's own sentence", err)
		}
		// The graph it had is the last thing it wrote: a refusal is an exit, and
		// not a scope marked down beside the nodes of the last read.
		written := sink.text()
		if strings.Contains(written, `"state":"down"`) {
			t.Errorf("the refused read was drawn as a down scope:\n%s", written)
		}
		if n := strings.Count(strings.TrimSpace(written), "\n") + 1; n != 1 {
			t.Errorf("the child wrote %d documents, want the first snapshot and nothing after it:\n%s", n, written)
		}
	})
}

// TestWatchAFirstReadThatFailsIsADownScopeWithNoNodes: a child whose *first*
// read fails has no last document to keep, and it still prints one: the scope
// it covers, down, with the read's own words and no nodes — a graph a console
// draws and explains rather than a child that ended with an error (H3, T3).
//
// The document is the one shape in this change that is hand-built, and it is
// read here as a reader outside this binary reads it — the line itself, through
// ParseDocument — because §6.22 pins what it carries: the scope this child
// covers, named, down, with the read's words and no age. Its lists are asserted
// too, so the wire shape of an empty graph is held at this one place; the `[]`
// rather than `null` is `Document.MarshalJSON`'s (`Document.normalise`), which
// is where that guarantee lives, and what this branch contributes is the
// document existing at all. The failure is a 503 and not a 403: a refusal is
// still terminal, which is the other test's half.
func TestWatchAFirstReadThatFailsIsADownScopeWithNoNodes(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	// The listen is refused as well — a database that is not answering answers
	// nothing — so the first document is the bound's and then the read's.
	boundFirst(t, 50*time.Millisecond)
	fast(t, testFloor, 60*time.Millisecond)
	f.db.Refuse(http.StatusServiceUnavailable, "503", "Service Unavailable", "the database is down")

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")

	line := sink.nextLine(t, testPatience)
	first, err := graph.ParseDocument([]byte(line))
	if err != nil {
		t.Fatalf("the child wrote a line that is not a document: %v: %s", err, line)
	}
	if len(first.Sources) != 1 {
		t.Fatalf("the first document's sources are %+v, want the scope it covers", first.Sources)
	}
	s := first.Sources[0]
	if s.State != graph.SourceDown {
		t.Errorf("the scope is %+v, want it down: the read failed and there is no last document", s)
	}
	if want := (graph.Scope{Kind: graph.ScopeCoordinator}).String(); s.Source != want {
		t.Errorf("the down scope is named %q, want the coordinator scope", s.Source)
	}
	if !strings.Contains(s.Reason, "503") {
		t.Errorf("the down scope's reason is %q, want the read's own failure in it", s.Reason)
	}
	if s.AsOf != nil {
		t.Errorf("the down scope claims an age of %s, want none: nothing was re-read", s.AsOf)
	}
	// The lists, as the reader gets them: an empty graph, never a null.
	for _, want := range []string{`"nodes":[]`, `"edges":[]`, `"warnings":[]`} {
		if !strings.Contains(line, want) {
			t.Errorf("the first document does not carry %s: %s", want, line)
		}
	}
	child.stillRunning(t, 20*testFloor)

	// And it is the same child that comes back up, with the next sequence and
	// the graph the database is holding again.
	f.db.Refuse(0, "", "", "")

	var back graph.Document
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); {
		doc := sink.next(t, testPatience)
		if len(doc.Sources) > 0 && doc.Sources[0].State == graph.SourceUp {
			back = doc
			break
		}
	}
	if len(back.Sources) == 0 {
		t.Fatalf("the scope never came back up; the child wrote:\n%s", sink.text())
	}
	if back.StreamID != first.StreamID {
		t.Errorf("the recovered document's stream_id is %q, want the first's %q: the child did not restart", back.StreamID, first.StreamID)
	}
	if back.Seq <= first.Seq {
		t.Errorf("the recovered document is seq %d, want later than the first document's %d", back.Seq, first.Seq)
	}
	if len(back.Nodes) != 1 || back.Nodes[0].Title != "the stored title" {
		t.Errorf("the recovered document drew %+v, want the stored graph", back.Nodes)
	}
}

// TestWatchAuthRevokedIsAChangeAndNotAnExit: the credential the subscription was
// made with expires, and the transport mints another and reconnects itself after
// its minimum delay — a revoke loop it gives up on arrives as a permission error
// instead, and that is the refusal. So a revoke is a change like any other: the
// child reads and prints it, and the graph never goes dark for a token refresh.
//
// The document the revoke owes is written while the transport is *between*
// connections, which is what tells it from the reconnected listen's own opening
// put: no subscription is open at that instant, so nothing could have put it.
func TestWatchAuthRevokedIsAChangeAndNotAnExit(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")
	first := sink.next(t, testPatience)

	f.db.SendAuthRevoked()

	revoked := sink.next(t, testPatience)
	if got := f.db.Streams(); got != 0 {
		t.Fatalf("the child wrote with %d subscription(s) open, want the document of the revoke itself, written before the transport reconnected", got)
	}
	if revoked.StreamID != first.StreamID {
		t.Errorf("the revoke's document carries stream_id %q, want the child's own %q", revoked.StreamID, first.StreamID)
	}
	if revoked.Seq != first.Seq+1 {
		t.Errorf("the revoke's document is seq %d, want %d", revoked.Seq, first.Seq+1)
	}
	if len(revoked.Sources) != 1 || revoked.Sources[0].State != graph.SourceUp {
		t.Errorf("the revoke's document is %+v, want the graph up: a token refresh is not an outage", revoked.Sources)
	}
	if len(revoked.Nodes) != len(first.Nodes) {
		t.Errorf("the revoke's document drew %d nodes, want the same graph's %d", len(revoked.Nodes), len(first.Nodes))
	}
	child.stillRunning(t, 20*testFloor)
}

// TestWatchClosedStdoutEndsTheChild: a console that was killed leaves a child
// whose next write fails, and that write is the child's last (H5, §10.4). The
// write that fails is a later one, so what is under test is the exit and not
// the first document.
func TestWatchClosedStdoutEndsTheChild(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	broken := errors.New("write: broken pipe")
	sink := newWatchSink()
	sink.hook = func(n int) error {
		if n > 1 {
			return broken
		}
		return nil
	}
	child := startWatch(t, sink, "graph", "--watch", "--json")

	if first := sink.next(t, testPatience); first.Seq != 1 {
		t.Fatalf("the first document is seq %d, want 1", first.Seq)
	}

	// The change is what makes the child write again, and that write is the one
	// whose refusal ends it.
	f.writeStoredView(t, changedView())
	err := child.wait(t, testPatience)
	if !errors.Is(err, broken) {
		t.Fatalf("the child ended with %v, want the writer's own refusal %v", err, broken)
	}
}

// TestWatchSlowReaderBlocksAndNeverDrops: a reader that stops reading makes the
// child block — writes are synchronous, so nothing is buffered away or replaced
// — and everything the changes said still arrives, in order, once the reader
// reads again (H5, §10.3).
func TestWatchSlowReaderBlocksAndNeverDrops(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	gate := make(chan struct{})
	sink := newWatchSink()
	sink.hook = func(n int) error {
		if n == 2 {
			<-gate
		}
		return nil
	}
	child := startWatch(t, sink, "graph", "--watch", "--json")

	first := sink.next(t, testPatience)

	// A change while the reader is stuck: the child reads it and blocks in the
	// write into a pipe nobody is reading, which is what makes the silence
	// below it blocking rather than a document it dropped.
	f.writeStoredView(t, changedView())
	sink.quiet(t, 20*testFloor, "the reader has stopped reading, so the child is blocked in a write")
	child.stillRunning(t, 20*testFloor)

	// And a second change while it is still stuck: this one is not lost either,
	// it waits in the subscription until the child can write again.
	f.writeStoredView(t, newestView())

	close(gate)

	// Both changes are documents — the one the child was blocked writing, and
	// the one that arrived while it was stuck — in that order and one sequence
	// after another: a slow reader delays the child and never costs it a
	// document, and no document merges two changes into one (H5, §10.3).
	after := []graph.Document{sink.next(t, testPatience), sink.next(t, testPatience)}
	for i, doc := range after {
		if doc.Seq != first.Seq+uint64(i)+1 {
			t.Errorf("document %d after the blocked write is seq %d, want %d: a document was dropped", i+1, doc.Seq, first.Seq+uint64(i)+1)
		}
	}
	if got := after[len(after)-1].Nodes[0].Title; got != "the newest title" {
		t.Errorf("the last document's node is %q, want the newest change: the child is not behind its subscription", got)
	}
}

// TestWatchOneSubscriptionPerScope: the child holds one connection per scope,
// shared by every repository and node that scope produces (H6, F12). The read
// behind each document is not a connection — it is a request the database
// answers and closes — so the count stays at one while the documents keep
// coming.
func TestWatchOneSubscriptionPerScope(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	f.writeConfig(t, "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"+
		"  acme/lib: { provider: github, base_branch: main, workflows: [web] }\n")
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	startWatch(t, sink, "graph", "--watch", "--json")
	sink.next(t, testPatience)

	if got := f.db.Streams(); got != 1 {
		t.Fatalf("the child holds %d subscription(s), want exactly one for its one scope", got)
	}
	before := len(f.db.Requests())

	f.writeStoredView(t, changedView())
	sink.next(t, testPatience)

	if got := f.db.Streams(); got != 1 {
		t.Errorf("the child holds %d subscription(s) after a change, want exactly one: a connection is opened per scope, never per node or per repository", got)
	}
	if len(f.db.Requests()) == before {
		t.Errorf("the change made no request at all: the document does not come from a read")
	}
}

// TestWatchAWithdrawnListenIsADownScopeAndNotAnExit: the server can take the
// listen away — a rule no longer allows it — and the transport does not
// reconnect after a cancel, so the child's one subscription is over for good.
// What that is not is a refusal: the scope goes down, the child stays alive
// with the last graph and keeps its clock, which is §10.6's rule for `watch` on
// the same event and the rule this scope's plan pins (CG7, F20(3)). A console
// reads the state and ages the figures itself.
func TestWatchAWithdrawnListenIsADownScopeAndNotAnExit(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")
	first := sink.next(t, testPatience)
	if len(first.Sources) != 1 || first.Sources[0].State != graph.SourceUp {
		t.Fatalf("the first document's sources are %+v, want the one scope up", first.Sources)
	}

	f.db.SendCancel()

	down := sink.next(t, testPatience)
	if len(down.Sources) != 1 || down.Sources[0].State != graph.SourceDown {
		t.Errorf("the scope is %+v after the listen was withdrawn, want it down", down.Sources)
	}
	if got := down.Sources[0].Reason; got != withdrawnListen {
		t.Errorf("the reason is %q, want %q: the document says why the scope is down", got, withdrawnListen)
	}
	if len(down.Nodes) == 0 {
		t.Errorf("the withdrawn listen dropped the graph: the last nodes are what a screen still draws (H3)")
	}
	if down.StreamID != first.StreamID || down.Seq != first.Seq+1 {
		t.Errorf("the document after the withdrawal is %q seq %d, want %q seq %d: the same child, the next document",
			down.StreamID, down.Seq, first.StreamID, first.Seq+1)
	}
	child.stillRunning(t, 20*testFloor)

	// The clock keeps running, which is how a reader tells a child that is alive
	// and a feed that is not: the next document is the keep-alive, nothing
	// changed, the same scope still down.
	alive := sink.next(t, testPatience)
	if len(alive.Sources) != 1 || alive.Sources[0].State != graph.SourceDown {
		t.Errorf("the keep-alive after the withdrawal is %+v, want the scope still down", alive.Sources)
	}
	if alive.StreamID != first.StreamID || alive.Seq != down.Seq+1 {
		t.Errorf("the keep-alive is %q seq %d, want %q seq %d", alive.StreamID, alive.Seq, first.StreamID, down.Seq+1)
	}
	child.stillRunning(t, 20*testFloor)
}

// TestWatchAWithdrawnListenIsADownScopeAtEveryInstantItCanLand: the withdrawal
// is two things in the transport — a `cancel` event, and the closed channel
// that follows it — and the child folds both of them into one state, in the two
// places it can arrive: out of the ordinary event loop, and before the opening
// put that proves the listen works has ever come.
//
// Both instants are handed to the child directly, because the fake cannot
// produce them: it registers a listener and writes its opening put in one
// breath, so a cancel sent by a test cannot land between the two, and it always
// closes the stream it cancels. Every other instant is held end to end by
// TestWatchAWithdrawnListenIsADownScopeAndNotAnExit.
func TestWatchAWithdrawnListenIsADownScopeAtEveryInstantItCanLand(t *testing.T) {
	t.Run("the cancel event", func(t *testing.T) {
		var w watcher
		if err := w.observe(rtdb.Event{Type: "cancel"}); err != nil {
			t.Fatalf("the cancel event ended the child with %v, want a scope that is down", err)
		}
		if w.down != withdrawnListen {
			t.Errorf("the scope is down because %q, want %q", w.down, withdrawnListen)
		}
		if !w.dirty {
			t.Errorf("the withdrawal left no change pending: nothing would tell a reader the scope is down")
		}
	})

	t.Run("before the opening put", func(t *testing.T) {
		closed := make(chan rtdb.Event)
		close(closed)

		st, err := graph.NewStream()
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		w := &watcher{out: &out, st: st}
		if err := w.first(context.Background(), closed); err != nil {
			t.Fatalf("a withdrawn listen ended the child with %v, want the first document, its scope down", err)
		}
		doc, err := graph.ParseDocument(bytes.TrimSpace(out.Bytes()))
		if err != nil {
			t.Fatalf("the child wrote a line that is not a document: %v: %s", err, out.String())
		}
		if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceDown {
			t.Errorf("the first document's sources are %+v, want the one scope down", doc.Sources)
		}
		if got := doc.Sources[0].Reason; got != withdrawnListen {
			t.Errorf("the reason is %q, want %q", got, withdrawnListen)
		}
		if doc.Seq != 1 || doc.StreamID == "" {
			t.Errorf("the first document is seq %d under stream %q, want seq 1 under this child's stream", doc.Seq, doc.StreamID)
		}
	})
}

// TestWatchRefusedReadIsTerminalAndNamesTheCommand: a refusal is what ends the
// child, and the sentence it ends with names what to do instead, because a
// console shows that sentence and draws no graph (H3, T3, contract §6.22).
// Three refusals reach it — the listen refused when it opens, the listen refused
// on a reconnect, and a caller asking for the stream without --json — and each
// names its own way out. A listen the server *withdrew* is not one of them:
// that is a scope that is down, and
// TestWatchAWithdrawnListenIsADownScopeAndNotAnExit holds it.
func TestWatchRefusedReadIsTerminalAndNamesTheCommand(t *testing.T) {
	t.Run("the listen is refused when the stream opens", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())
		fast(t, testFloor, testKeep)

		// The grant the subscription needs is gone before the child starts: a
		// 403 on the listen itself, which is this CLI being refused the graph
		// rather than a scope whose transport fell over.
		f.db.Refuse(http.StatusForbidden, "403", "Forbidden", "Permission denied")

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")
		err := child.wait(t, testPatience)
		if err == nil {
			t.Fatalf("the child streamed a project it may not read")
		}
		if !strings.Contains(err.Error(), "fugaro graph") {
			t.Errorf("the refusal is %q, want it to name the command to run instead", err)
		}
		if got := sink.text(); got != "" {
			t.Errorf("the refused stream wrote %q, want no document at all: a refusal is not a graph with nothing in it", got)
		}
	})

	t.Run("the listen is refused on a reconnect", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())
		fast(t, testFloor, testKeep)

		sink := newWatchSink()
		child := startWatch(t, sink, "graph", "--watch", "--json")
		if first := sink.next(t, testPatience); first.Seq != 1 {
			t.Fatalf("the first document is seq %d, want 1", first.Seq)
		}

		// The grant is withdrawn while the console is watching: the connection
		// dies without a word — a scope that is down, a document and not an
		// exit — and the reconnect that follows is refused with 403, which is
		// the other thing entirely.
		f.db.Refuse(http.StatusForbidden, "403", "Forbidden", "Permission denied")
		f.db.DropStreams()

		err := child.wait(t, testPatience)
		if err == nil {
			t.Fatalf("the child kept streaming after the listen was refused")
		}
		if !strings.Contains(err.Error(), "fugaro graph") {
			t.Errorf("the refusal is %q, want it to name the command to run instead", err)
		}
		// The down document came first, and it is what tells a reader the two
		// apart: the transport fell over, and then this CLI was refused.
		if written := sink.text(); !strings.Contains(written, `"state":"down"`) {
			t.Errorf("the child never said the scope was down before it was refused; it wrote:\n%s", written)
		}
	})

	t.Run("--watch without --json", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, storedView())

		// A stream is one document per line, which is a shape only --json has:
		// a table interleaved with documents is not one a reader can consume.
		// The refusal is made before anything is read, like the scope's.
		before := len(f.db.Requests())
		out, errOut, err := execute(t, "graph", "--watch")
		if err == nil {
			t.Fatalf("graph --watch without --json printed %q, want a refusal", out)
		}
		for _, want := range []string{"--json", "--watch"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal is %q, want it to name %s", err, want)
			}
		}
		if out != "" {
			t.Errorf("the refused stream wrote %q to stdout, want no document at all", out)
		}
		if errOut != "" {
			t.Errorf("the refused stream wrote %q to stderr: the project was selected before the flags were checked", errOut)
		}
		if got := len(f.db.Requests()); got != before {
			t.Errorf("the refused stream made %d database calls, want none", got-before)
		}
	})
}

// TestWatchSanitisesWhatTheTransportSays: a down scope's reason is the
// transport's own error text, and that text carries the database's response
// body — a string this CLI never wrote (rtdb's statusErr keeps the server's
// message). The document is the boundary (H2), so a bidi override, a zero-width
// formatter and a forged line in that body reach the child's stdout as plain
// one-line text or not at all: a console renders this reason beside the state,
// and print it raw and this child is the thing that hands a renderer the text
// it exists not to trust.
func TestWatchSanitisesWhatTheTransportSays(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	startWatch(t, sink, "graph", "--watch", "--json")
	if first := sink.next(t, testPatience); first.Sources[0].State != graph.SourceUp {
		t.Fatalf("the first document's source is %+v, want it up before the transport fails", first.Sources[0])
	}

	// What the database answers the reconnect with: the server's own words,
	// carrying the two kinds of text a document may not print, and a newline
	// that would read as a line of its own on a terminal.
	hostile := "the database is down " + string(rune(0x202e)) + "reversed " + string(rune(0x200b)) + "and\nforged line"
	f.db.Refuse(http.StatusServiceUnavailable, "503", "Service Unavailable", hostile)
	f.db.DropStreams()

	// The first down document is the transport's own sentence about a closed
	// connection; the one under test is the document that carries the server's
	// body, which arrives with the reconnect.
	var down graph.Document
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); {
		doc := sink.next(t, testPatience)
		if len(doc.Sources) > 0 && strings.Contains(doc.Sources[0].Reason, "the database is down") {
			down = doc
			break
		}
	}
	if len(down.Sources) == 0 {
		t.Fatalf("no document carried the transport's own words; the child wrote:\n%s", sink.text())
	}

	s := down.Sources[0]
	if s.State != graph.SourceDown {
		t.Errorf("the source carrying the server's words is %q, want %q", s.State, graph.SourceDown)
	}
	// The server's words did arrive — this is not a reason that says nothing —
	// and they arrived as one plain line: the formatters gone rather than
	// printed, the newline a space rather than a line break.
	for _, want := range []string{"the database is down", "reversed and forged line"} {
		if !strings.Contains(s.Reason, want) {
			t.Errorf("the down reason %q does not carry %q: the transport's own sentence is what a screen shows beside the state", s.Reason, want)
		}
	}
	for _, bad := range []string{"\n", "\r", "‮", "​"} {
		if strings.Contains(s.Reason, bad) {
			t.Errorf("the down reason %q carries %q, which a renderer draws as something other than text", s.Reason, bad)
		}
	}
	// And nothing anywhere on the stream: the guard is the document's, so every
	// document it writes is held to it, not this one field.
	for _, bad := range []string{"‮", "​"} {
		if strings.Contains(sink.text(), bad) {
			t.Errorf("a document on stdout carries %q raw:\n%s", bad, sink.text())
		}
	}
}

// TestWatchRefusesAProjectWithNoDatabase: the project's config names no
// Firebase database at all — an installation that never ran `fugaro update
// --firebase-project` — and there is no connection to subscribe to, so the
// child must be refused, exit 1 with the sentence the one-shot read refuses it
// with (the command's own exit codes, §6.22), and never dereferenced: a console
// that spawns this child reads a refusal it can show, where a panic is a
// goroutine dump on its stderr and a stream that never prints a document.
func TestWatchRefusesAProjectWithNoDatabase(t *testing.T) {
	f := newGraphFixture(t)
	nodb := noDatabase(t, f)

	_, _, err := execute(t, "graph", "--config", nodb, "--json")
	if err == nil {
		t.Fatalf("graph --json answered a project with no database")
	}
	if !strings.Contains(err.Error(), "has no Firebase database") {
		t.Fatalf("the one-shot read refuses a project with no database with %q, want the documented sentence", err)
	}

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--config", nodb, "--watch", "--json")
	serr := child.wait(t, testPatience)
	if serr == nil {
		t.Fatalf("the stream kept running on a project with no database")
	}
	// The stream's refusal is the read's own, character for character: it is
	// the same call and not a second check worded alike, so a console that
	// reads both never has to know which one it is looking at.
	if serr.Error() != err.Error() {
		t.Errorf("the stream refuses a project with no database with\n  %q\nwant the one-shot read's own\n  %q", serr, err)
	}
	if got := fcli.ExitCode(serr); got != fcli.ExitUserError {
		t.Errorf("the refusal exits %d, want %d: a project with no database is the user's to fix", got, fcli.ExitUserError)
	}
	if got := sink.text(); got != "" {
		t.Errorf("the refused stream wrote %q, want no document at all", got)
	}
}

// noDatabase removes the budget block from a fixture's config, leaving the
// project of an installation that has no database, and answers the same path.
func noDatabase(t *testing.T, f *graphFixture) string {
	t.Helper()
	path := f.writeConfig(t, "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block := "budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.db.URL + " }\n"
	if !strings.Contains(string(body), block) {
		t.Fatalf("the fixture's config does not carry %q:\n%s", block, body)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), block, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
