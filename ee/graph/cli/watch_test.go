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
//   - TestWatchRefusedReadIsTerminalAndNamesTheCommand: the server withdrawing
//     the listen is a refusal that ends the child and names the command, which
//     is what a console reads as "this CLI has no graph" (T3).
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
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
	select {
	case line := <-s.ch:
		doc, err := graph.ParseDocument([]byte(line))
		if err != nil {
			t.Fatalf("the child wrote a line that is not a document: %v: %s", err, line)
		}
		return doc
	case <-time.After(within):
		t.Fatalf("no document within %s; the child wrote:\n%s", within, s.text())
		return graph.Document{}
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

// TestWatchRefusedReadIsTerminalAndNamesTheCommand: the server withdrawing the
// listen is the one thing that ends the child, and the sentence it ends with
// names the command a person can run instead — which is what a console reads as
// "this CLI cannot stream the graph" and answers by showing that sentence (H3,
// T3, contract §6.22).
func TestWatchRefusedReadIsTerminalAndNamesTheCommand(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	fast(t, testFloor, testKeep)

	sink := newWatchSink()
	child := startWatch(t, sink, "graph", "--watch", "--json")
	if first := sink.next(t, testPatience); first.Seq != 1 {
		t.Fatalf("the first document is seq %d, want 1: the child streamed before it was refused", first.Seq)
	}

	// The server takes the listen away: a rule no longer allows the read, which
	// is a refusal rather than a scope that is down.
	f.db.SendCancel()

	err := child.wait(t, testPatience)
	if err == nil {
		t.Fatalf("the child kept streaming after the server withdrew the listen")
	}
	if !strings.Contains(err.Error(), "fugaro graph") {
		t.Errorf("the refusal is %q, want it to name the command to run instead", err)
	}
}
