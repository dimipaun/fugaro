package orchestrator_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The ladder's executor past rung 2 (E10b): rung 3's merge under D1245's gate,
// and rung 4's split.
//
// What is pinned here is the acting and never the deciding: escalation.Decide
// answered the rung and E1's own tests hold that rule, so every test below hands
// the hook a step, a reason and a reading and asserts on what it did with them.
// The reading, the host and the log are stubs; the state is the real one
// (internal/gcpfake) and the writes go through Apply, so the ledger these tests
// read back is a document this release accepted and the question they read back
// is one the validator would take.
//
// The hook is called directly rather than through a tick, and that is the one
// thing a reader of this file should know. MergeSeams.Node is the cycle's own
// state and commit path: a hook's write lands inside a cycle that holds a
// revision of its own (patch.go's Apply is bound to BaseRev, store.go's Save to
// the ETag it read), so a node of the hook's own would leave the cycle's next
// write refused. The node here is therefore re-read before every write, which is
// what O14's wiring has to do, and a tick-driven test would be a test about a
// wiring no release has yet.

const (
	// mergeRun is the run behind the head under judgement, as the record names
	// it.
	mergeRun = "20261107-090000-abcd"
	// mergeReviewRun and mergeJudgeRun are the two rung 3 runs of a head that
	// is covered: the review D2043 requires and the judge whose answer may veto.
	// They are run ids and not names, because the ledger the hook writes names
	// the run its move launched and the state admits a block only under a run id:
	// a mint that returned anything else would have the whole write refused.
	mergeReviewRun = "20261107-100000-a1b2"
	mergeJudgeRun  = "20261107-110000-c3d4"
	// mergePR is the pull request every reading here belongs to.
	mergePR = 7
	// mergeFinding is a finding key of the shape the tree writes (runstore's
	// ValidFindingKey): lower hex, sixteen characters.
	mergeFinding = "0123456789abcdef"
	// mergeRoutinePath is a path sensitivity.Classify calls Routine, and
	// mergeSecurityPath one it does not: the two the Routine clause is read on.
	mergeRoutinePath  = "docs/status.md"
	mergeSecurityPath = "internal/gateway/proxy.go"
)

// mergeStub is the launcher, the commenter and the log as one seam: what the
// hook started, what it posted and what it wrote down. The log remembers the
// entries it was given, which is what makes the hook's own read of an earlier
// line (MergeLog.Logged) the read a real log would answer.
type mergeStub struct {
	t          *testing.T
	launchErr  error
	commentErr error
	loggedErr  error
	appendErr  error
	// appendErrOn is the one transition whose line this log refuses, and "" for
	// a log that takes every line. A test that needs a landing's marker down and
	// its outcome line lost sets it to that line's transition: appendErr refuses
	// every line, which would refuse the marker before the host was ever asked.
	appendErrOn string
	launched    []orchestrator.LadderRequest
	comments    []string
	lines       []orchestrator.Entry
	written     map[string]bool
}

func newMergeStub(t *testing.T) *mergeStub {
	t.Helper()
	return &mergeStub{t: t, written: map[string]bool{}}
}

func (m *mergeStub) LaunchLadder(_ context.Context, req orchestrator.LadderRequest) error {
	if m.launchErr != nil {
		return m.launchErr
	}
	m.launched = append(m.launched, req)
	return nil
}

func (m *mergeStub) LadderComment(_ context.Context, _ orchestrator.WorkItem, body string) error {
	if m.commentErr != nil {
		return m.commentErr
	}
	m.comments = append(m.comments, body)
	return nil
}

func (m *mergeStub) Append(_ context.Context, e orchestrator.Entry) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	if m.appendErrOn != "" && e.Transition == m.appendErrOn {
		return errors.New("the log would not take this line")
	}
	m.lines = append(m.lines, e)
	m.written[orchestrator.EntryID(e.Subject, e.Transition)] = true
	return nil
}

func (m *mergeStub) Logged(_ context.Context, id string) (bool, error) {
	if m.loggedErr != nil {
		return false, m.loggedErr
	}
	return m.written[id], nil
}

// line is the one line a test expects under one transition, and it fails when
// there is none: every outcome this file pins writes exactly one, and the id is
// derived from the head, so a second entry under it is the same key rewritten.
func (m *mergeStub) line(transition string) orchestrator.Entry {
	m.t.Helper()
	var found []orchestrator.Entry
	for _, e := range m.lines {
		if e.Transition == transition {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		m.t.Fatalf("the log carries %d lines under %q, and one outcome writes one: %+v", len(found), transition, m.lines)
	}
	return found[0]
}

// mergeHostStub is MergeHost as a test sets it: the pull request and the checks
// it answers with, the refusals it gives, and every call it was asked for in
// the order it was asked.
//
// merged records only the heads a merge landed for, so a test that sees nothing
// in it has seen no merge rather than a refused one; calls records the attempts
// either way, which is what lets a test pin the order of a landing's two calls.
type mergeHostStub struct {
	t         *testing.T
	state     gitprov.PRState
	draft     bool
	head      string
	checks    []gitprov.CheckRun
	prErr     error
	checksErr error
	readyErr  error
	mergeErr  error
	calls     []string
	merged    []string
}

func newMergeHost() *mergeHostStub {
	return &mergeHostStub{state: gitprov.PROpen}
}

func (h *mergeHostStub) PullRequest(context.Context, int) (gitprov.PRInfo, error) {
	if h.prErr != nil {
		return gitprov.PRInfo{}, h.prErr
	}
	h.calls = append(h.calls, "pull")
	return gitprov.PRInfo{Number: mergePR, URL: "https://example.invalid/pr/7", State: h.state, Draft: h.draft, HeadSHA: h.head}, nil
}

func (h *mergeHostStub) Checks(_ context.Context, _ int, sha string) ([]gitprov.CheckRun, error) {
	if h.checksErr != nil {
		return nil, h.checksErr
	}
	h.calls = append(h.calls, "checks "+sha)
	return h.checks, nil
}

func (h *mergeHostStub) MarkReady(_ context.Context, _ int, head string) error {
	h.calls = append(h.calls, "ready "+head)
	return h.readyErr
}

func (h *mergeHostStub) Merge(_ context.Context, _ int, head string) error {
	h.calls = append(h.calls, "merge "+head)
	if h.mergeErr != nil {
		return h.mergeErr
	}
	h.merged = append(h.merged, head)
	return nil
}

// beyond is every call but the two the gate reads whatever it decides: a pull
// request and the checks on a head. A test that sees this empty has seen the
// gate read and then refuse, and nothing asked for on the strength of it.
func (h *mergeHostStub) beyond() []string {
	var out []string
	for _, c := range h.calls {
		if !strings.HasPrefix(c, "pull") && !strings.HasPrefix(c, "checks ") {
			out = append(out, c)
		}
	}
	return out
}

// storeNode is the cycle's own write path as the seam sees it: the state the
// node carries and one Apply of one op list committed through the store. It
// re-reads the node before every write, so what the hook reads and what it lands
// are the documents the store holds — and so Apply's own validation, and the
// store's, stand between a test and a ledger this release would refuse.
type storeNode struct {
	t    *testing.T
	node *orchestrator.Node
	now  time.Time
}

func (n *storeNode) State(ctx context.Context) (orchestrator.State, error) {
	s, _, err := n.node.Load(ctx)
	return s, err
}

func (n *storeNode) Apply(ctx context.Context, ops []orchestrator.Op) error {
	s, etag, err := n.node.Load(ctx)
	if err != nil {
		return err
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops}, orchestrator.Actor{Class: orchestrator.Daemon, Now: n.now})
	if err != nil {
		return err
	}
	return n.node.Save(ctx, next, etag)
}

// mergeHarness is one rung 3 or 4 execution: the node the state lives in, the
// reading, the host, the log, the sink and the seams a hook acts through.
type mergeHarness struct {
	t      *testing.T
	now    time.Time
	db     *gcpfake.RTDB
	node   *orchestrator.Node
	reader *mergeReaderStub
	host   *mergeHostStub
	stub   *mergeStub
	sink   *tickSink
	seams  orchestrator.MergeSeams
}

// newMergeHarness is a hook over one node: the state is seeded through the
// store's own write, and every seam but the state is a stub. The run ids are the
// ones the minter hands out in order, so a test names the ids it expects a
// launch to be minted under and a launch it did not expect fails loudly.
func newMergeHarness(t *testing.T, now time.Time, runs []string, items ...orchestrator.WorkItem) *mergeHarness {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	node := mustNode(t, rtdbClient(t, f), nil)
	ctx := context.Background()
	s := o6Node(t, now, items...)
	s.Intents = mergeAttempts(now, items)
	_, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty node: %v", err)
	}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("seeding the node with the test's state: %v", err)
	}
	m := &mergeHarness{
		t: t, now: now, db: f, node: node,
		reader: &mergeReaderStub{t: t, reads: map[string]orchestrator.MergeReading{}},
		host:   newMergeHost(),
		stub:   newMergeStub(t),
		sink:   &tickSink{},
	}
	mint := &o6Mint{t: t, runs: runs}
	m.seams = orchestrator.MergeSeams{
		Read:    m.reader,
		Host:    m.host,
		Node:    &storeNode{t: t, node: node, now: now},
		Launch:  m.stub,
		Comment: m.stub,
		Log:     m.stub,
		Notify:  m.sink,
		Now:     clockAt(now),
		Mint:    mint.mint,
	}
	return m
}

// act drives one entry of the hook, as E10a's ladder does: the item looked up
// again from the node rather than carried in, the step and the reason the
// decision gave, and the reading the daemon made.
func (m *mergeHarness) act(id string, step escalation.Step, reason escalation.Reason, read orchestrator.LadderReading) {
	m.t.Helper()
	beyond := m.seams.Beyond()
	if beyond == nil {
		m.t.Fatalf("the merge seams answered no hook, and every seam but the sink is set")
	}
	if _, err := beyond(context.Background(), itemAt(m.t, m.state(), id), step, reason, read); err != nil {
		m.t.Fatalf("the rung %s hook ended as %v", step.Rung, err)
	}
}

// state is the node as it now stands, read back through the store.
func (m *mergeHarness) state() orchestrator.State {
	m.t.Helper()
	s, _, err := m.node.Load(context.Background())
	if err != nil {
		m.t.Fatalf("reading the node back: %v", err)
	}
	return s
}

// item is one item of the node as it now stands.
func (m *mergeHarness) item(id string) orchestrator.WorkItem {
	m.t.Helper()
	return itemAt(m.t, m.state(), id)
}

// edit writes one change into the node's state, which is how a test puts an item
// in a position whose only difference from mergeItem is one field: the approval
// taken away, a spec edited since it, a rung's runs spent. It is the store's own
// write, so what it leaves behind is a node the daemon could have written, and
// whatever the write side refuses is refused here rather than by the gate.
func (m *mergeHarness) edit(change func(*orchestrator.State)) {
	m.t.Helper()
	ctx := context.Background()
	s, etag, err := m.node.Load(ctx)
	if err != nil {
		m.t.Fatalf("reading the node to edit it: %v", err)
	}
	change(&s)
	if err := m.node.Save(ctx, s, etag); err != nil {
		m.t.Fatalf("writing the edited node: %v", err)
	}
}

// mergeChange is one field changed on one item of the state, which is what most
// of these edits are.
func mergeChange(s *orchestrator.State, id string, change func(*orchestrator.WorkItem)) {
	for i := range s.WorkItems {
		if s.WorkItems[i].ID == id {
			change(&s.WorkItems[i])
		}
	}
}

// mergeDependsOn makes one item wait on a second item the same write puts in the
// state, unfinished: an item waiting on work nobody has done is one no run of the
// ladder's may start for, and the dependency has to be a real item for the write
// side to take it at all.
func mergeDependsOn(s *orchestrator.State, id, dep string) {
	blocker := workItemOf(dep)
	blocker.Phase = orchestrator.PhaseQueued
	s.WorkItems = append(s.WorkItems, blocker)
	mergeChange(s, id, func(w *orchestrator.WorkItem) { w.DependsOn = []string{dep} })
}

// mergeMarker is the line a landing writes before it asks the host to merge, as
// the log holds it when the outcome line never arrived: the window the replay
// tests below cover begins there, and it is the only thing that says the merge
// at that head is this ladder's.
func (m *mergeHarness) mergeMarker(id, head string) {
	m.t.Helper()
	m.stub.written[orchestrator.EntryID(id, "ladder-r3-landing-"+head[:8])] = true
}

// question is the ladder's question about one item, which these tests expect to
// be there: every refusal this file pins raises one.
func (m *mergeHarness) question(id string) orchestrator.Decision {
	m.t.Helper()
	d, ok := m.state().Decision(id + "-escalation")
	if !ok {
		m.t.Fatalf("the node carries no escalation question about %q", id)
	}
	if d.WaitingOn != orchestrator.OnPerson {
		m.t.Errorf("the question about %q waits on %q, and every one of these is a person's (D1247)", id, d.WaitingOn)
	}
	return d
}

// mergeReaderStub is MergeReader as a test sets it: the readings by item, and
// the heads it was asked about, which is what pins that the gate judged the head
// the ladder named.
type mergeReaderStub struct {
	t     *testing.T
	reads map[string]orchestrator.MergeReading
	err   error
	heads []string
}

func (r *mergeReaderStub) MergeAt(_ context.Context, item orchestrator.WorkItem, head string) (orchestrator.MergeReading, error) {
	if r.err != nil {
		return orchestrator.MergeReading{}, r.err
	}
	read, ok := r.reads[item.ID]
	if !ok {
		r.t.Fatalf("the merge gate read %q, which this test gave no reading for", item.ID)
	}
	r.heads = append(r.heads, item.ID+"/"+head)
	return read, nil
}

// r2Ledger is an item's ledger when the decision is about to put it at rung 3:
// it has spent its rebase and its one stronger pass, and the rung 3 transition
// is the hook's to write.
func r2Ledger(now time.Time, head string, outer int) *orchestrator.Escalation {
	e := ledger(escalation.R2, outer,
		step("", escalation.R0, escalation.ReasonRebase, now.Add(-3*time.Hour), head),
		step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(-2*time.Hour), ""))
	return &e
}

// r3Ledger is an item's ledger with rung 3 already written: the hook that
// re-enters it re-reads a head rather than making a move.
func r3Ledger(now time.Time, head string, outer int) *orchestrator.Escalation {
	e := ledger(escalation.R3, outer,
		step("", escalation.R0, escalation.ReasonRebase, now.Add(-3*time.Hour), head),
		step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(-2*time.Hour), ""),
		step(escalation.R2, escalation.R3, escalation.ReasonR2Clean, now.Add(-time.Hour), head))
	return &e
}

// mergeItem is one of these tests' items: the item the ladder has walked to the
// rung its ledger names, in the phase a stalled item is left in, and under the
// approval that covers the ladder's launches on it — spent, both of its attempts
// run, which is where a rung 3 item stands: the loop has failed twice and the
// ladder is what is left (D2042, G15). A rung 3 review and a judge are runs of
// another recipe under that approval's At, so the state the ladder reads them
// through is the same one the rungs below read. The tests that take the approval
// away are the ones about a launch the ladder may not make.
func mergeItem(id string, e *orchestrator.Escalation) orchestrator.WorkItem {
	w := workItemOf(id)
	w.Phase = orchestrator.PhaseFailed
	w.RunID = mergeRun
	w.Attempts = orchestrator.ApprovalAttempts
	at := e.EnteredAt
	w.SpentApprovalAt = &at
	w.Escalation = e
	return w
}

// mergeAttempts are the intents a spent approval leaves behind: the item's own
// failed runs, which are what the approval covered (G15). They are seeded for
// every item these tests hand the harness, because that is the shape a rung 3
// item reaches the merge gate in — and without them the state holds nothing
// under the spent marker, so the digest the ladder is covered by cannot be read
// (ladderCoveredDigest) and every launch in this file would be refused.
func mergeAttempts(now time.Time, items []orchestrator.WorkItem) []orchestrator.Intent {
	var out []orchestrator.Intent
	for _, w := range items {
		if w.SpentApprovalAt == nil {
			continue
		}
		out = append(out, twoAttempts(w.ID, *w.SpentApprovalAt, now.Add(-100*time.Minute), w)...)
	}
	return out
}

// mergeRecord is the run behind the judged head: it ended, it pushed that head,
// it changed one routine path, and it opened a pull request.
func mergeRecord(head string) *runstore.Record {
	return &runstore.Record{
		Version:      1,
		RunID:        mergeRun,
		Status:       runstore.StatusSucceeded,
		PushedHead:   head,
		ChangedFiles: runstore.NewChangedFiles([]string{mergeRoutinePath}),
		PR:           &runstore.PRRef{Number: mergePR},
	}
}

// mergeYes is the judge's answer that vetoes nothing: its only two actions are
// the yes and the block, and the yes adds nothing the gate did not have (D2031).
func mergeYes() runstore.MergeJudge {
	return runstore.MergeJudge{
		Verdict: runstore.MergeJudgeMergeWithFollowUps,
		Reason:  "nothing fundamental",
		Model:   "test-judge",
	}
}

// mergePass is a reading every clause of D1245's gate passes on: a run that
// ended at the judged head, a routine path set the host reports for that head,
// green CI, no finding in either of the two reads the gate makes, a scored
// review of that head and the judge's yes.
func mergePass(head string) orchestrator.MergeReading {
	return orchestrator.MergeReading{
		Read:           true,
		Number:         mergePR,
		Record:         mergeRecord(head),
		Paths:          runstore.NewChangedFiles([]string{mergeRoutinePath}),
		Findings:       mergeClearFindings(),
		ReviewFindings: mergeClearFindings(),
		ReviewRun:      mergeReviewRun,
		Scored:         true,
		JudgeRun:       mergeJudgeRun,
		Verdict:        mergeYes(),
	}
}

// mergeClearFindings is an artifact the reader found and read, carrying no open
// finding: the read that is not the same fact as no read at all.
func mergeClearFindings() orchestrator.Findings {
	return orchestrator.Findings{Read: orchestrator.FindingsReadable, List: []orchestrator.Finding{}}
}

// mergeGreen is CI as a host reports it green: one check, finished, successful.
func mergeGreen() []gitprov.CheckRun {
	return []gitprov.CheckRun{{Status: "completed", Conclusion: "success"}}
}

// r3Step is the decision's own step for rung 3, at the ceiling the caller names:
// escalation.Decide takes it from the item's row, and the gate reads it from
// there rather than from the mode (D2030).
func r3Step(ceiling escalation.CeilingAction) escalation.Step {
	return escalation.Step{Rung: escalation.R3, AtCeiling: ceiling}
}

// r4Step is the decision's step for rung 4: no ceiling, because the split
// merges nothing and the branch stays open.
func r4Step() escalation.Step { return escalation.Step{Rung: escalation.R4} }

// r3Read is the ladder's reading of the item at the head under judgement.
// Nothing in it but the head and a run under way reaches the gate.
func r3Read(head string) orchestrator.LadderReading {
	return orchestrator.LadderReading{Read: true, Mode: rigor.POC, Elapsed: 4 * time.Hour, Churn: escalation.KindNone, HeadSHA: head}
}

// landable is a harness whose gate passes on the whole of D1245's evidence.
func landable(t *testing.T, now time.Time, runs ...string) *mergeHarness {
	t.Helper()
	m := newMergeHarness(t, now, runs, mergeItem("wi-1", r2Ledger(now, headA, 2)))
	m.reader.reads["wi-1"] = mergePass(headA)
	m.host.head = headA
	m.host.checks = mergeGreen()
	return m
}

// TestR3MergesOnlyUnderGate is the gate itself: the whole of D1245's evidence
// lands the head the ladder judged, and each missing piece of it refuses.
//
// The clause under each case is what the case is named for, and the mutation is
// the same one for all of them: drop that clause from the gate and the case
// below merges a head the clause is there to refuse.
func TestR3MergesOnlyUnderGate(t *testing.T) {
	now := o6Now()

	t.Run("the whole gate lands the head it judged", func(t *testing.T) {
		m := landable(t, now)
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))

		if !slices.Equal(m.host.merged, []string{headA}) {
			t.Fatalf("the host merged %v, and the gate passed at %s", m.host.merged, headA)
		}
		if strings.Contains(strings.Join(m.host.calls, " "), "ready") {
			t.Fatalf("the host was asked %v, and an ordinary pull request needs no ready call", m.host.calls)
		}
		if got := m.reader.heads; len(got) != 1 || got[0] != "wi-1/"+headA {
			t.Fatalf("the gate read %v, and it judges the head the ladder named", got)
		}

		e := m.item("wi-1").Escalation
		if e == nil || e.Rung != escalation.R3 || len(e.Transitions) != 3 {
			t.Fatalf("the ledger reads %+v, and rung 3 is one more transition", e)
		}
		if last := e.Transitions[2]; last.From != escalation.R2 || last.Reason != escalation.ReasonR2Clean || last.HeadSHA != headA {
			t.Fatalf("the transition is %+v, and the rung the decision named is what moved the item", last)
		}
		if e.OuterAttempts != 2 {
			t.Fatalf("the ledger counts %d ladder runs, and a landing spends none of them", e.OuterAttempts)
		}
		if got := m.stub.line("ladder-r3-land-" + headA[:8]); !strings.Contains(got.Text, "rung R3 (r2_clean)") ||
			!strings.Contains(got.Text, string(orchestrator.ReasonPathsRoutine)) || !strings.Contains(got.Text, string(orchestrator.ReasonCIGreen)) {
			t.Fatalf("the line reads %q, and a landing carries the whole of its evidence (D1245)", got.Text)
		}
		if len(m.stub.comments) != 1 {
			t.Fatalf("the ladder posted %d comments for one move", len(m.stub.comments))
		}

		m.host.state = gitprov.PRMerged
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))
		if len(m.host.merged) != 1 {
			t.Fatalf("the second cycle merged %v, and a pull request merges once", m.host.merged[1:])
		}
		if len(m.stub.comments) != 1 {
			t.Fatalf("the second cycle posted %d comments, and a cycle that made no move posts none", len(m.stub.comments))
		}
		if got := m.item("wi-1").Escalation; len(got.Transitions) != 3 {
			t.Fatalf("the ledger reads %+v, and a cycle at the rung the item is at makes no move", got)
		}
		m.stub.line("ladder-r3-merged-" + headA[:8])
	})

	for _, tc := range []struct {
		name string
		code string
		drop func(*orchestrator.MergeReading, *mergeHostStub)
	}{
		{
			"a run that has not ended",
			string(orchestrator.GateRunRunning),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Record.Status = runstore.StatusRunning },
		},
		{
			"a record that pushed another commit",
			string(orchestrator.GateHeadUnpushed),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Record.PushedHead = headB },
		},
		{
			"a record with no changed-path set",
			string(orchestrator.ReasonPathsMissing),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Record.ChangedFiles = nil },
		},
		{
			"a changed-path set nobody bounded",
			string(orchestrator.ReasonPathsTruncated),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{"docs/status.md"}, Total: 500}
			},
		},
		{
			"a run that changed nothing",
			string(orchestrator.ReasonNoCommits),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Record.ChangedFiles = runstore.NewChangedFiles(nil)
			},
		},
		{
			"a path outside the routine allowlist",
			string(orchestrator.GatePathsNotRoutine),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Paths = runstore.NewChangedFiles([]string{mergeSecurityPath})
			},
		},
		{
			"a path set nobody read from the host",
			string(orchestrator.GatePathsUncorroborated),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Paths = nil },
		},
		{
			"a host path set nobody bounded",
			string(orchestrator.ReasonPathsTruncated),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Paths = &runstore.ChangedFiles{Paths: []string{mergeRoutinePath}, Total: 500}
			},
		},
		{
			"CI nobody could read",
			string(orchestrator.ReasonCIUnknown),
			func(_ *orchestrator.MergeReading, h *mergeHostStub) { h.checks = nil },
		},
		{
			"CI that failed",
			string(orchestrator.GateCIRed),
			func(_ *orchestrator.MergeReading, h *mergeHostStub) {
				h.checks = []gitprov.CheckRun{{Status: "completed", Conclusion: "failure"}}
			},
		},
		{
			"a critical finding",
			string(orchestrator.ReasonCriticalFinding),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityCritical, Path: "docs/status.md", Key: mergeFinding}}
			},
		},
		{
			"a high finding in a security path",
			string(orchestrator.ReasonSecurityHighFinding),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityHigh, Path: "internal/gateway/proxy.go", Key: mergeFinding}}
			},
		},
		{
			"findings nobody could read",
			string(orchestrator.ReasonFindingsAbsent),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Findings.Read = orchestrator.FindingsAbsent },
		},
		{
			"a critical finding in the review at this head",
			string(orchestrator.ReasonCriticalFinding),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.ReviewFindings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityCritical, Path: mergeRoutinePath, Key: mergeFinding}}
			},
		},
		{
			"a high finding in a security path, in the review at this head",
			string(orchestrator.ReasonSecurityHighFinding),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.ReviewFindings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityHigh, Path: mergeSecurityPath, Key: mergeFinding}}
			},
		},
		{
			"review findings nobody could read",
			string(orchestrator.ReasonFindingsAbsent),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) {
				r.ReviewFindings.Read = orchestrator.FindingsAbsent
			},
		},
		{
			"a record nobody can place",
			string(orchestrator.ReasonRecordUnreadable),
			func(r *orchestrator.MergeReading, _ *mergeHostStub) { r.Record.Status = runstore.Status("invented") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := landable(t, now)
			read := m.reader.reads["wi-1"]
			tc.drop(&read, m.host)
			m.reader.reads["wi-1"] = read
			m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

			refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], tc.code)
		})
	}
}

// refusedAt is the outcome of a rung 3 the gate refused: nothing merged, one
// question for a person carrying the code, and one line under the head's own
// name. It is the assertion every refusal case below shares.
func refusedAt(t *testing.T, m *mergeHarness, head, transition, code string) {
	t.Helper()
	if len(m.host.merged) != 0 {
		t.Fatalf("the host merged %v, and the gate refused this head", m.host.merged)
	}
	line := m.stub.line(transition)
	if !strings.Contains(line.Text, code) {
		t.Fatalf("the line reads %q, and %q is the code this refusal is recorded under", line.Text, code)
	}
	if !strings.Contains(m.question("wi-1").Question, code) {
		t.Fatalf("the question reads %q, and the person it goes to reads the code", m.question("wi-1").Question)
	}
	if m.item("wi-1").Phase == orchestrator.PhaseCancelled {
		t.Fatalf("the item was cancelled, and a rung 3 that cannot merge waits for a person")
	}
}

// TestR3HeadMovedRefuses is D1243 and G2 at the one place they are worth the
// most: the merge names the head the gate judged, so a push after the gate
// refuses the merge rather than landing a commit no verdict covers.
//
// Mutation: drop the head from the merge and the second case below lands the
// commit the review and the judge never read.
func TestR3HeadMovedRefuses(t *testing.T) {
	now := o6Now()

	t.Run("the pull request moved past the judged head", func(t *testing.T) {
		m := landable(t, now)
		m.host.head = headB
		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateHeadMoved))
		if got := m.host.beyond(); len(got) != 0 {
			t.Fatalf("the host was asked %v, and a head that moved is refused before the merge is asked for", got)
		}
	})

	t.Run("the host refuses the merge it was asked for", func(t *testing.T) {
		m := landable(t, now)
		m.host.mergeErr = gitprov.ErrHeadMoved
		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		if !slices.Equal(m.host.calls, []string{"pull", "checks " + headA, "merge " + headA}) {
			t.Fatalf("the host was asked %v, and the merge names the judged head", m.host.calls)
		}
		refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateMergeRefused))
		line := m.stub.line("ladder-r3-wait-" + headA[:8])
		if !strings.Contains(line.Text, "head_moved") {
			t.Fatalf("the line reads %q, and a refusal the host gave is recorded under its own closed code", line.Text)
		}
	})
}

// TestR3VetoRaisesDecision is G1 and D1247: a judge that blocked the merge
// raises one question for a person on the item's own ladder id, carrying the
// judge's own words fenced, and says so once.
//
// Mutation: treat a blocked verdict as an absent one and the item waits with no
// verdict attached, or treat it as a yes and the blocked head merges.
func TestR3VetoRaisesDecision(t *testing.T) {
	now := o6Now()
	m := landable(t, now)
	m.reader.reads["wi-1"] = func() orchestrator.MergeReading {
		r := mergePass(headA)
		r.Verdict = runstore.MergeJudge{
			Verdict: runstore.MergeJudgeBlocked,
			Reason:  "the change rewrites the gate it is judged by",
			Blocker: "the blocking issue",
			Model:   "test-judge",
		}
		return r
	}()
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateJudgeVeto))
	q := m.question("wi-1").Question
	if !strings.Contains(q, "the blocking issue") || !strings.Contains(q, "the change rewrites the gate it is judged by") {
		t.Fatalf("the question reads %q, and D2037 attaches the judge's own answer to it", q)
	}
	if !strings.Contains(q, "```") {
		t.Fatalf("the question reads %q, and the judge's words are fenced off from this program's own", q)
	}
	if len(m.sink.got) != 1 || m.sink.got[0].Kind != orchestrator.NotifyDecisionNeeded || m.sink.got[0].Subject != "wi-1-escalation" {
		t.Fatalf("the sink was told %+v, and one question is one notification", m.sink.got)
	}

	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))
	if len(m.sink.got) != 1 {
		t.Fatalf("a second cycle told a person %+v, and the question the node already carries is not asked twice", m.sink.got[1:])
	}
}

// TestR3NonRoutineWaitsInEveryMode is G10 and D2047: a path set outside the
// allowlist never auto-merges, in any mode. The mode is not what decides — the
// step's ceiling is read, and the gate's own clause refuses whatever it says.
//
// Mutation: read the mode instead of the ceiling and the Hotfix case below
// merges a change to the gateway.
func TestR3NonRoutineWaitsInEveryMode(t *testing.T) {
	now := o6Now()
	for _, mode := range []rigor.Mode{rigor.POC, rigor.Pro, rigor.Hotfix} {
		t.Run(string(mode), func(t *testing.T) {
			m := landable(t, now)
			read := m.reader.reads["wi-1"]
			read.Record.ChangedFiles = runstore.NewChangedFiles([]string{mergeSecurityPath})
			read.Paths = runstore.NewChangedFiles([]string{mergeSecurityPath})
			m.reader.reads["wi-1"] = read

			ladder := r3Read(headA)
			ladder.Mode = mode
			m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, ladder)

			refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GatePathsNotRoutine))
			if len(m.stub.launched) != 0 {
				t.Fatalf("the ladder launched %+v, and a head the gate refuses spends no run", m.stub.launched)
			}
		})
	}
}

// TestR3PathsComeFromTheHostNotTheRecord is D1245's Routine clause read on the
// document that can carry it: the host's changed files at the judged head.
//
// The record's changed-path set is the run's own account of its diff — the job
// writes it from inside the container it shares with the agent — and a merge
// that lands unread rests on this clause, so the account cannot be its own
// corroboration. The triage reads the same rule into ErrTriageUncorroborated,
// and the read that lifts it is a host's own report of the head. What the two
// documents say here is deliberately at odds, and the host's answer is the one
// the clause follows: a routine record over a host that reports a security path
// refuses, because what the pull request would carry is the host's to know and
// not the run's to claim.
//
// Mutation: classify the record's list instead of the host's and the first case
// below merges a head whose diff touches the gateway.
func TestR3PathsComeFromTheHostNotTheRecord(t *testing.T) {
	now := o6Now()

	t.Run("a routine record over a host that reports otherwise refuses", func(t *testing.T) {
		m := landable(t, now)
		read := m.reader.reads["wi-1"]
		read.Record.ChangedFiles = runstore.NewChangedFiles([]string{mergeRoutinePath})
		read.Paths = runstore.NewChangedFiles([]string{mergeSecurityPath})
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GatePathsNotRoutine))
		if got := m.host.beyond(); len(got) != 0 {
			t.Fatalf("the host was asked %v, and a head the clause refuses merges nothing", got)
		}
	})

	t.Run("a record that claims more than the host reports still lands", func(t *testing.T) {
		m := landable(t, now)
		read := m.reader.reads["wi-1"]
		read.Record.ChangedFiles = runstore.NewChangedFiles([]string{mergeSecurityPath})
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		if !slices.Equal(m.host.merged, []string{headA}) {
			t.Fatalf("the host merged %v, and the set the clause reads is the host's at the judged head", m.host.merged)
		}
	})
}

// TestR3CriticalAlwaysWaits is G10's other half: the ceiling D2037 gives
// Critical is a person, and the ladder will not merge such an item at any mode.
//
// Mutation: let a merge through when the ceiling is wait_person and the case
// below lands work nothing but a person may land.
func TestR3CriticalAlwaysWaits(t *testing.T) {
	now := o6Now()
	m := landable(t, now)

	read := r3Read(headA)
	read.Mode = rigor.Hotfix
	m.act("wi-1", r3Step(escalation.CeilingWaitPerson), escalation.ReasonR2Clean, read)

	refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateModeWaits))
	if !strings.Contains(m.question("wi-1").Question, "nothing fundamental") {
		t.Fatalf("the question reads %q, and the wait carries the verdict it was read with", m.question("wi-1").Question)
	}

	m.host.state = gitprov.PRMerged
	m.act("wi-1", r3Step(escalation.CeilingWaitPerson), escalation.ReasonR2Clean, read)
	if len(m.host.merged) != 0 {
		t.Fatalf("a pull request that merged itself changed the outcome: %v", m.host.merged)
	}
}

// TestR3DraftNotReadiedWhenSwitchOff is D2041 and D2046: `escalation.ready_drafts`
// off means a stalled draft the gate would otherwise land waits for a person —
// the switch is off in every installation that never turned it on, and a daemon
// must not ready what nobody asked it to.
//
// Mutation: ready the draft unconditionally and the case below merges a pull
// request no person moved out of draft.
func TestR3DraftNotReadiedWhenSwitchOff(t *testing.T) {
	now := o6Now()
	m := landable(t, now)
	m.host.draft = true
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateDraftHeld))
	if got := m.host.beyond(); len(got) != 0 {
		t.Fatalf("the host was asked %v, and a draft nobody readied is not touched", got)
	}
}

// TestR3DraftReadiedThenMergedWhenOn is the other side of the switch: with
// `escalation.ready_drafts` on the ladder readies the draft first and merges the
// same head immediately after, which is the one automatic merge D2030 allows
// without a person reading the pull request first.
//
// Mutation: merge the draft without the ready call and a provider that refuses
// a merge on a draft turns every rung 3 into a refusal.
func TestR3DraftReadiedThenMergedWhenOn(t *testing.T) {
	now := o6Now()
	m := landable(t, now)
	m.host.draft = true
	m.seams.ReadyDrafts = true
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	want := []string{"pull", "checks " + headA, "ready " + headA, "merge " + headA}
	if !slices.Equal(m.host.calls, want) {
		t.Fatalf("the host was asked %v, and a draft is readied before it is merged", m.host.calls)
	}
	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Fatalf("the host merged %v, and the gate passed", m.host.merged)
	}
	m.stub.line("ladder-r3-land-" + headA[:8])
}

// TestR3FollowUpsFiledWithImportance is D2040: the findings a merge did not
// block are filed as one item each, ranked by the judge where it ranked them and
// by their severity where it did not, and every one of them waits for a person.
//
// The two structural halves are pinned with it: the items are smaller work of
// the merged item and carry no specification (D1246), and a second cycle files
// nothing twice.
//
// Mutation: file the findings as a re-run of the item's own spec and the work
// the ladder just decided about is relaunched under a fresh approval.
func TestR3FollowUpsFiledWithImportance(t *testing.T) {
	now := o6Now()
	m := landable(t, now)
	read := mergePass(headA)
	read.Findings.List = []orchestrator.Finding{
		{Severity: orchestrator.SeverityHigh, Path: "docs/status.md", Key: mergeFinding},
		{Severity: orchestrator.SeverityMedium, Path: "docs/status.md", Key: "fedcba9876543210"},
	}
	read.Verdict.FollowUps = []runstore.MergeJudgeFollowUp{{Key: mergeFinding, Importance: runstore.MergeJudgeMinor}}
	m.reader.reads["wi-1"] = read

	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Fatalf("the host merged %v, and a high finding on a routine path does not block a landing", m.host.merged)
	}
	s := m.state()
	for _, tc := range []struct {
		suffix     string
		importance runstore.MergeJudgeImportance
		severity   orchestrator.Severity
	}{
		{"-fu-" + mergeFinding, runstore.MergeJudgeMinor, orchestrator.SeverityHigh},
		{"-fu-fedcba9876543210", runstore.MergeJudgeMinor, orchestrator.SeverityMedium},
	} {
		w, ok := s.Item("wi-1" + tc.suffix)
		if !ok {
			t.Fatalf("the node carries no follow-up %q: %+v", "wi-1"+tc.suffix, s.WorkItems)
		}
		if w.Phase != orchestrator.PhaseQueued || w.Approved != nil {
			t.Fatalf("the follow-up is %s with approval %+v, and the daemon approves its own work in neither case", w.Phase, w.Approved)
		}
		if w.Spec != "" {
			t.Fatalf("the follow-up carries the spec %q, and a follow-up is never a relaunch of it (D1246)", w.Spec)
		}
		if w.Repo != "owner/repo" || w.Recipe != "standard" || w.RigorTier != "medium" {
			t.Fatalf("the follow-up is %+v, and it is the merged item's own work", w)
		}
		if !strings.Contains(w.Brief, string(tc.severity)) || !strings.Contains(w.Brief, string(tc.importance)) {
			t.Fatalf("the brief reads %q, and the person reading it sees the severity and the importance", w.Brief)
		}
	}

	m.host.state = gitprov.PRMerged
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))
	if len(m.state().WorkItems) != 3 {
		t.Fatalf("the second cycle filed %d items, and a landing files each follow-up once", len(m.state().WorkItems))
	}
}

// TestR3TheReviewAtTheHeadIsRead is D2043 at the clause it is about: the
// review-only run a rebased head wears is not a yes the gate passes on. Its own
// findings are the severities the no-critical-finding clause is decided on, so a
// head its review called critical never merges, and a finding it left open is
// what the landing files with the importance the judge gave it (D2040).
//
// The run behind the record is clean in every case, which is the reading a gate
// that carried the review as a flag alone would land on.
//
// Mutation: read the clause on the run's own findings only — the state this
// finding was written about — and the first case below merges a head whose
// review reported a critical finding.
func TestR3TheReviewAtTheHeadIsRead(t *testing.T) {
	now := o6Now()

	t.Run("a review that reported a critical finding never merges", func(t *testing.T) {
		m := landable(t, now)
		read := mergePass(headA)
		read.ReviewFindings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityCritical, Path: mergeRoutinePath, Key: mergeFinding}}
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.ReasonCriticalFinding))
	})

	t.Run("a finding the review left open is what the landing files", func(t *testing.T) {
		m := landable(t, now)
		read := mergePass(headA)
		read.ReviewFindings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityMedium, Path: mergeRoutinePath, Key: mergeFinding}}
		read.Verdict.FollowUps = []runstore.MergeJudgeFollowUp{{Key: mergeFinding, Importance: runstore.MergeJudgeImportant}}
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		if !slices.Equal(m.host.merged, []string{headA}) {
			t.Fatalf("the host merged %v, and a medium finding on a routine path blocks no landing", m.host.merged)
		}
		w, ok := m.state().Item("wi-1-fu-" + mergeFinding)
		if !ok {
			t.Fatalf("the node carries no follow-up for the review's finding: %+v", m.state().WorkItems)
		}
		if !strings.Contains(w.Brief, string(orchestrator.SeverityMedium)) || !strings.Contains(w.Brief, string(runstore.MergeJudgeImportant)) {
			t.Fatalf("the brief reads %q, and the finding the review left open is filed as the landing's own work (D2040)", w.Brief)
		}
	})

	t.Run("one finding the two reads agree on is one finding", func(t *testing.T) {
		// Both runs wrote the same key, which is what a key is for: the item is
		// one, and the severity the brief names is the independent reader's.
		m := landable(t, now)
		read := mergePass(headA)
		read.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityMedium, Path: mergeRoutinePath, Key: mergeFinding}}
		read.ReviewFindings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityLow, Path: mergeRoutinePath, Key: mergeFinding}}
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		s := m.state()
		if len(s.WorkItems) != 2 {
			t.Fatalf("the node carries %d items, and one finding is one follow-up", len(s.WorkItems))
		}
		w, ok := s.Item("wi-1-fu-" + mergeFinding)
		if !ok {
			t.Fatalf("the node carries no follow-up for the shared key: %+v", s.WorkItems)
		}
		if !strings.Contains(w.Brief, string(orchestrator.SeverityLow)) {
			t.Fatalf("the brief reads %q, and the review's own word is kept", w.Brief)
		}
	})
}

// TestR3ReplayFinishesALostLanding is the landing the node never heard about:
// the host took the merge, and the cycle died between that call and the write
// that files what the landing left open. Every cycle after it reads a pull
// request that is already merged, and without the replay the findings are filed
// by nobody and the person a Pro merge is owed is told nothing at all.
//
// What tells a lost landing from a pull request somebody else merged is the
// marker the landing writes before it asks the host to merge, and never the
// item's ledger: the item reaches rung 3 at a head it refused just as it does at
// a head it merged, so the ledger reads the same either way
// (TestR3ALandingIsTheLaddersOwn). The log's lines are the second half — a cycle
// that finds either of them has already done this — so a replay happens once and
// a landing that landed properly is never replayed on top of itself.
//
// Mutation: drop the replay and the first case below merges nothing, files
// nothing and tells nobody.
func TestR3ReplayFinishesALostLanding(t *testing.T) {
	now := o6Now()
	const second = "fedcba9876543210"

	lost := func(t *testing.T) *mergeHarness {
		t.Helper()
		m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
		m.mergeMarker("wi-1", headA)
		read := mergePass(headA)
		read.Findings.List = []orchestrator.Finding{
			{Severity: orchestrator.SeverityHigh, Path: mergeRoutinePath, Key: mergeFinding},
			{Severity: orchestrator.SeverityMedium, Path: mergeRoutinePath, Key: second},
		}
		read.Verdict.FollowUps = []runstore.MergeJudgeFollowUp{{Key: mergeFinding, Importance: runstore.MergeJudgeMinor}}
		m.reader.reads["wi-1"] = read
		m.host.head, m.host.checks, m.host.state = headA, mergeGreen(), gitprov.PRMerged
		return m
	}

	t.Run("the next cycle files what the landing left and tells the person", func(t *testing.T) {
		m := lost(t)
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))

		s := m.state()
		if len(s.WorkItems) != 3 {
			t.Fatalf("the node carries %d items, and a replay files one follow-up per open finding", len(s.WorkItems))
		}
		for _, tc := range []struct {
			suffix     string
			importance runstore.MergeJudgeImportance
			severity   orchestrator.Severity
		}{
			{"-fu-" + mergeFinding, runstore.MergeJudgeMinor, orchestrator.SeverityHigh},
			{"-fu-" + second, runstore.MergeJudgeMinor, orchestrator.SeverityMedium},
		} {
			w, ok := s.Item("wi-1" + tc.suffix)
			if !ok {
				t.Fatalf("the node carries no follow-up %q: %+v", "wi-1"+tc.suffix, s.WorkItems)
			}
			if !strings.Contains(w.Brief, string(tc.importance)) || !strings.Contains(w.Brief, string(tc.severity)) {
				t.Fatalf("the brief reads %q, and a replayed landing's findings are filed as the landing's are", w.Brief)
			}
		}
		if len(m.host.merged) != 0 {
			t.Fatalf("the host merged %v, and a pull request that is already merged is not merged again", m.host.merged)
		}
		m.stub.line("ladder-r3-merged-" + headA[:8])

		if len(m.sink.got) != 1 || m.sink.got[0].Kind != orchestrator.NotifyMergedNeedsReview {
			t.Fatalf("the sink was told %+v, and an unattended merge nobody heard about is what a Pro ceiling tells", m.sink.got)
		}
		if text := m.sink.got[0].Text; !strings.Contains(text, "already merged") || !strings.Contains(text, "2 findings") {
			t.Fatalf("the notification reads %q, and it says what is true of a merge this cycle did not make", text)
		}

		// The line the replay wrote is the fence: the cycle after it finds the
		// work filed and the person told, and does neither again.
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))
		if got := len(m.state().WorkItems); got != 3 {
			t.Fatalf("the cycle after the replay carries %d items, and a replay files each finding once", got)
		}
		if len(m.sink.got) != 1 {
			t.Fatalf("the cycle after the replay told the sink %d times, and a replay is said once", len(m.sink.got))
		}
	})

	t.Run("a marker at another head is not this head's landing", func(t *testing.T) {
		// The ladder landed another head, so this head is none of its landing's:
		// it has no landing to finish and nothing to say.
		m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headB, 2)))
		m.mergeMarker("wi-1", headB)
		read := mergePass(headA)
		read.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityHigh, Path: mergeRoutinePath, Key: mergeFinding}}
		m.reader.reads["wi-1"] = read
		m.host.head, m.host.checks, m.host.state = headA, mergeGreen(), gitprov.PRMerged
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))

		if got := len(m.state().WorkItems); got != 1 {
			t.Fatalf("the node carries %d items, and a merge this ladder did not reach files nothing", got)
		}
		if len(m.sink.got) != 0 {
			t.Fatalf("the sink was told %+v, and the ladder has nothing to say about a merge it did not make", m.sink.got)
		}
	})

	t.Run("a findings read nobody could make files nothing", func(t *testing.T) {
		m := lost(t)
		read := m.reader.reads["wi-1"]
		read.Findings = orchestrator.Findings{Read: orchestrator.FindingsAbsent}
		m.reader.reads["wi-1"] = read
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))

		if got := len(m.state().WorkItems); got != 1 {
			t.Fatalf("the node carries %d items, and a replay invents no finding it could not read", got)
		}
		if len(m.sink.got) != 0 {
			t.Fatalf("the sink was told %+v, and a replay with nothing to file says nothing", m.sink.got)
		}
	})
}

// TestR3ALostLandingLineLeavesTheItemWalkable is the order a landing's writes
// are made in, read at the one place it shows: the stop that ends the item is
// also the write that ends the landing's recoverability, so it comes last. A
// cycle whose outcome line the log refuses leaves the item at rung 3 — the
// marker is down before the host is asked, which is what a replay reads — and
// the cycle after it finishes the landing and stops the item.
//
// Mutation: stop the item in the landing's own op list, beside the ledger and
// the follow-ups, and the cycle after the refused line finds an item the ladder
// reads no further rungs for: the outcome line never lands and the person a Pro
// ceiling owes is told nothing, because the replay is the only cycle that would
// have told them.
func TestR3ALostLandingLineLeavesTheItemWalkable(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	read := mergePass(headA)
	read.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityHigh, Path: mergeRoutinePath, Key: mergeFinding}}
	read.Verdict.FollowUps = []runstore.MergeJudgeFollowUp{{Key: mergeFinding, Importance: runstore.MergeJudgeMinor}}
	m.reader.reads["wi-1"] = read
	m.host.head, m.host.checks = headA, mergeGreen()

	step := r3Step(escalation.CeilingAutoMergeNotify)
	m.stub.appendErrOn = "ladder-r3-land-" + headA[:8]
	beyond := m.seams.Beyond()
	if beyond == nil {
		t.Fatal("the merge seams answered no hook, and every seam but the sink is set")
	}
	if _, err := beyond(context.Background(), itemAt(t, m.state(), "wi-1"), step, escalation.ReasonR2Clean, r3Read(headA)); err == nil {
		t.Fatalf("the landing ended clean, and the log refused the line it writes last")
	}
	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Fatalf("the host merged %v, and the merge is asked for before the line", m.host.merged)
	}
	if got := m.item("wi-1").Phase; got == orchestrator.PhaseCancelled {
		t.Fatalf("the item is %s, and a landing whose line was lost is one the cycle after it finishes", got)
	}
	if len(m.sink.got) != 0 {
		t.Fatalf("the sink was told %+v, and the cycle that could not write the line reached nobody", m.sink.got)
	}

	// The log takes lines again, the host holds the merge and nothing has said
	// this head is merged: that is the replay, and the replay is what stops the
	// item.
	m.stub.appendErrOn = ""
	m.host.state = gitprov.PRMerged
	m.act("wi-1", step, escalation.ReasonR2Clean, r3Read(headA))

	m.stub.line("ladder-r3-merged-" + headA[:8])
	if got := m.item("wi-1").Phase; got != orchestrator.PhaseCancelled {
		t.Fatalf("the item is %s after the replay, and the merge this ladder finished with is over", got)
	}
	if _, ok := m.state().Item("wi-1-fu-" + mergeFinding); !ok {
		t.Fatalf("the replay left no follow-up for the finding the merge did not block: %+v", m.state().WorkItems)
	}
	if len(m.sink.got) != 1 || m.sink.got[0].Kind != orchestrator.NotifyMergedNeedsReview {
		t.Fatalf("the sink was told %+v, and the person a Pro ceiling owes is told by the replay", m.sink.got)
	}
}

// TestR3ALandingIsTheLaddersOwn is what a replay must not do: read a person's
// own merge as the ladder's. The gate refusing at a head is the case that shows
// why the item's ledger cannot answer the question — the item reaches rung 3 at
// the head it refused exactly as it does at the head it merged, so its
// transitions read the same either way — and a cycle that read the landing from
// them files follow-ups for a merge this ladder never made, under a brief that
// says the ladder made it.
//
// Mutation: read Landed from the ledger's rung 3 transition again and the first
// case below files an item and tells the sink about a merge a person made.
func TestR3ALandingIsTheLaddersOwn(t *testing.T) {
	now := o6Now()

	t.Run("the gate refuses and a person merges the head it refused", func(t *testing.T) {
		m := landable(t, now)
		read := m.reader.reads["wi-1"]
		read.Findings.List = []orchestrator.Finding{{Severity: orchestrator.SeverityCritical, Path: mergeRoutinePath, Key: mergeFinding}}
		m.reader.reads["wi-1"] = read
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))
		refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.ReasonCriticalFinding))

		// The person reads the question and merges that head themselves, which
		// is a landing the ladder had nothing to do with.
		m.host.state = gitprov.PRMerged
		told := len(m.sink.got)
		m.act("wi-1", r3Step(escalation.CeilingAutoMergeNotify), escalation.ReasonR2Clean, r3Read(headA))

		if got := len(m.state().WorkItems); got != 1 {
			t.Fatalf("the node carries %d items, and a landing the gate refused files nothing", got)
		}
		if got := m.sink.got[told:]; len(got) != 0 {
			t.Fatalf("the cycle of the person's merge told the sink %+v, and the ladder has nothing to say about a merge it refused", got)
		}
		if len(m.host.merged) != 0 {
			t.Fatalf("the host merged %v, and a pull request a person merged is not merged again", m.host.merged)
		}
		for _, e := range m.stub.lines {
			if strings.HasPrefix(e.Transition, "ladder-r3-landing-") {
				t.Fatalf("the log carries %q, and a landing the gate refused never wrote its marker", e.Transition)
			}
		}
	})
}

// TestR3NotifiesProAndHotfix is D2030: the two ceilings that merge and tell
// somebody do, and the one that merges quietly does not.
//
// Mutation: notify on every merge and POC's every small change wakes the owner;
// notify on none and a Pro landing lands without a word.
func TestR3NotifiesProAndHotfix(t *testing.T) {
	now := o6Now()
	for _, tc := range []struct {
		ceiling escalation.CeilingAction
		want    []orchestrator.Kind
	}{
		{escalation.CeilingAutoMerge, nil},
		{escalation.CeilingAutoMergeNotify, []orchestrator.Kind{orchestrator.NotifyMergedNeedsReview}},
	} {
		t.Run(string(tc.ceiling), func(t *testing.T) {
			m := landable(t, now)
			m.act("wi-1", r3Step(tc.ceiling), escalation.ReasonR2Clean, r3Read(headA))

			if !slices.Equal(m.host.merged, []string{headA}) {
				t.Fatalf("the host merged %v, and both of these ceilings merge under the same gate", m.host.merged)
			}
			kinds := make([]orchestrator.Kind, 0, len(m.sink.got))
			for _, n := range m.sink.got {
				kinds = append(kinds, n.Kind)
			}
			if !slices.Equal(kinds, tc.want) {
				t.Fatalf("the sink was told %v, and this ceiling tells %v", kinds, tc.want)
			}
			if len(tc.want) == 1 && (m.sink.got[0].Subject != "wi-1" || !strings.Contains(m.sink.got[0].Text, "under the merge gate")) {
				t.Fatalf("the notification is %+v, and it is about the item the ladder merged", m.sink.got[0])
			}
		})
	}
}

// TestR4SplitScopedToBlocker is rung 4: the branch and the pull request are kept
// — the ladder asks the host for nothing at all — the item is stopped, and the
// scope a person approves is the blocker the judge named (D2031, D1246).
//
// Mutation: relaunch the item's own spec instead of filing a scope and the same
// specification runs a third time.
func TestR4SplitScopedToBlocker(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	read := mergePass(headA)
	read.Verdict = runstore.MergeJudge{
		Verdict: runstore.MergeJudgeBlocked,
		Reason:  "the split is the fundamental issue and not the rest",
		Blocker: "the retry loop has no bound and no test covers it",
		Model:   "test-judge",
	}
	m.reader.reads["wi-1"] = read

	m.act("wi-1", r4Step(), escalation.ReasonChurnSame, r3Read(headA))

	if len(m.host.calls) != 0 {
		t.Fatalf("the host was asked %v, and rung 4 keeps the branch and the pull request", m.host.calls)
	}
	w := m.item("wi-1")
	if w.Phase != orchestrator.PhaseCancelled {
		t.Fatalf("the item is %s, and rung 4 stops it", w.Phase)
	}
	if e := w.Escalation; e == nil || e.Rung != escalation.R4 || len(e.Transitions) != 4 {
		t.Fatalf("the ledger reads %+v, and rung 4 is one more transition", e)
	}
	split, ok := m.state().Item("wi-1-split")
	if !ok {
		t.Fatalf("the node carries no split: %+v", m.state().WorkItems)
	}
	if split.Phase != orchestrator.PhaseQueued || split.Approved != nil || split.Spec != "" {
		t.Fatalf("the split is %+v, and it is work a person approves before anything runs", split)
	}
	if !strings.Contains(split.Brief, "the retry loop has no bound") || !strings.Contains(split.Brief, "```") {
		t.Fatalf("the brief reads %q, and the judge's blocker is what the scope is", split.Brief)
	}
	if strings.Contains(split.Brief, "the specification the ladder decided about: nothing") == false {
		t.Fatalf("the brief reads %q, and it says the original specification is not what runs", split.Brief)
	}
	if _, ok := m.state().Decision("wi-1-escalation"); ok {
		t.Fatalf("rung 4 asked a person a question, and a scoped split has nothing to ask")
	}
	if len(m.stub.comments) != 1 {
		t.Fatalf("the ladder posted %d comments for one move", len(m.stub.comments))
	}
	m.stub.line("ladder-r4-split-" + headA[:8])
}

// TestR4NoBlockerWaitsForAPerson is G5's rung 4: with no readable blocker there
// is no scope this code has any business inventing, so the item is stopped — the
// same as a scoped split — and the scope becomes a person's question.
//
// Mutation: take the verdict's reason for a scope and rung 4 files a split
// scoped to a sentence that is not the fundamental issue.
func TestR4NoBlockerWaitsForAPerson(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	read := mergePass(headA)
	read.Read = true
	read.Verdict = runstore.MergeJudge{Verdict: runstore.MergeJudgeBlocked, Reason: "the same finding came back twice", Unreadable: true}
	m.reader.reads["wi-1"] = read

	m.act("wi-1", r4Step(), escalation.ReasonChurnSame, r3Read(headA))

	if w := m.item("wi-1"); w.Phase != orchestrator.PhaseCancelled {
		t.Fatalf("the item is %s, and rung 4 stops it whether or not it can scope the split", w.Phase)
	}
	if _, ok := m.state().Item("wi-1-split"); ok {
		t.Fatalf("a split was filed with no blocker to scope it to: %+v", m.state().WorkItems)
	}
	q := m.question("wi-1").Question
	if !strings.Contains(q, "no blocker") {
		t.Fatalf("the question reads %q, and it says what is missing", q)
	}
	m.stub.line("ladder-r4-split-" + headA[:8])
}

// TestR4BlockerTextFencedAndClipped is G8 and the untrusted-text rule: the
// blocker is a record's own field, so what reaches a brief is cleaned, clipped to
// the bound the record itself uses, and fenced with more backticks than the text
// carries — a value that ended the fence would write the words after it as this
// program's own.
//
// Mutation: write the blocker into the brief unfenced and a judge that answered
// with a fence in it writes the rest of the brief.
func TestR4BlockerTextFencedAndClipped(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	read := mergePass(headA)
	raw := "\x1b[31mthe fence ``` is inside this\x1b[0m and then " + strings.Repeat("more ", 200)
	read.Verdict = runstore.MergeJudge{
		Verdict: runstore.MergeJudgeBlocked,
		Blocker: raw,
		Model:   "test-judge",
	}
	m.reader.reads["wi-1"] = read

	m.act("wi-1", r4Step(), escalation.ReasonChurnSame, r3Read(headA))

	split, ok := m.state().Item("wi-1-split")
	if !ok {
		t.Fatalf("the node carries no split: %+v", m.state().WorkItems)
	}
	if strings.ContainsRune(split.Brief, 0x1b) {
		t.Fatalf("the brief carries an escape sequence: %q", split.Brief)
	}
	if !strings.Contains(split.Brief, "````") {
		t.Fatalf("the brief reads %q, and a blocker carrying the fence opens a longer one", split.Brief)
	}
	if got := len([]rune(split.Brief)); got > orchestrator.MaxBriefRunes {
		t.Fatalf("the brief is %d runes and the state accepts %d", got, orchestrator.MaxBriefRunes)
	}
	if strings.Contains(split.Brief, raw) {
		t.Fatalf("the brief carries the blocker whole, and a blocker is clipped to the bound the record itself keeps it at")
	}
	want := safetext.Clip(safetext.MultiLine(raw), runstore.MaxMergeJudgeBlocker)
	if !strings.Contains(split.Brief, want) {
		t.Fatalf("the brief reads %q, and the blocker is the cleaned text clipped to %d runes", split.Brief, runstore.MaxMergeJudgeBlocker)
	}
	if !strings.Contains(split.Brief, "and then") {
		t.Fatalf("the brief reads %q, and the blocker's own opening words are what a person reads", split.Brief)
	}
}

// TestR3UnreadableInputRecommendsOnly is G5: a reading the daemon could not make
// merges nothing and launches nothing, and the item waits for a person with the
// input that could not be read.
//
// Mutation: treat an unreadable reading as an ordinary one and every clause of
// the gate is decided on a zero value.
func TestR3UnreadableInputRecommendsOnly(t *testing.T) {
	now := o6Now()
	for _, tc := range []struct {
		name string
		drop func(*orchestrator.MergeReading)
		code string
	}{
		{
			"the reading was not made",
			func(r *orchestrator.MergeReading) { *r = orchestrator.MergeReading{} },
			string(orchestrator.GateUnreadable),
		},
		{
			"the head is not one the ladder recorded",
			func(r *orchestrator.MergeReading) { r.Record = nil },
			string(orchestrator.ReasonRecordUnreadable),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := landable(t, now)
			read := m.reader.reads["wi-1"]
			tc.drop(&read)
			m.reader.reads["wi-1"] = read

			m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

			refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], tc.code)
			if len(m.stub.launched) != 0 {
				t.Fatalf("the ladder launched %+v, and an unreadable head spends no run", m.stub.launched)
			}
			if got := m.host.beyond(); len(got) != 0 {
				t.Fatalf("the host was asked %v, and there is nothing to ask it for", got)
			}
		})
	}
}

// TestR3JudgeAbsentIsVeto is D2044: a judge that did not answer — absent, failed,
// malformed — is a veto and never a pass, while a head no judge has been asked
// about is one launch and not a refusal.
//
// Mutation: read the zero verdict as a yes and a head nothing judged merges.
func TestR3JudgeAbsentIsVeto(t *testing.T) {
	now := o6Now()

	t.Run("a head no judge has been asked about", func(t *testing.T) {
		m := landable(t, now, mergeJudgeRun)
		read := mergePass(headA)
		read.JudgeRun, read.Verdict = "", runstore.MergeJudge{}
		m.reader.reads["wi-1"] = read

		m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

		if len(m.host.merged) != 0 {
			t.Fatalf("the host merged %v, and no judge has read this head", m.host.merged)
		}
		if len(m.stub.launched) != 1 || m.stub.launched[0].Recipe != orchestrator.RecipeMergeJudge {
			t.Fatalf("the ladder launched %+v, and rung 3's second run is the judge", m.stub.launched)
		}
		if m.stub.launched[0].HeadSHA != headA || m.stub.launched[0].Rung != escalation.R3 {
			t.Fatalf("the judge was asked about %+v, and it judges the head the ladder named", m.stub.launched[0])
		}
		if e := m.item("wi-1").Escalation; e.OuterAttempts != 3 {
			t.Fatalf("the ledger counts %d ladder runs, and the judge is one", e.OuterAttempts)
		}
		m.stub.line("ladder-r3-judge-" + headA[:8])
	})

	for _, tc := range []struct {
		name string
		give runstore.MergeJudge
	}{
		{"a judge that failed", runstore.MergeJudge{Unreadable: true}},
		{"a judge that answered nothing", runstore.MergeJudge{}},
		{"a judge whose answer nobody can place", runstore.MergeJudge{Verdict: runstore.MergeJudgeVerdict("maybe")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := landable(t, now)
			read := mergePass(headA)
			read.Verdict = tc.give
			m.reader.reads["wi-1"] = read

			m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

			refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateJudgeVeto))
		})
	}
}

// TestR3RebasedHeadNeedsScoredReview is D2043 and G13: a head only a rebase run
// has touched is an agent's own work, so rung 3 covers it with a review-only run
// at that head before it may merge — one such run per head — and the merge waits
// until the review has a verdict.
//
// Mutation: merge a head the review has not read and a rebase agent's push lands
// on the strength of the rebase's own record.
func TestR3RebasedHeadNeedsScoredReview(t *testing.T) {
	now := o6Now()
	m := landable(t, now, mergeReviewRun)
	read := mergePass(headA)
	read.Scored, read.ReviewRun = false, ""
	m.reader.reads["wi-1"] = read

	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	if len(m.host.merged) != 0 {
		t.Fatalf("the host merged %v, and no scored review covers this head", m.host.merged)
	}
	if len(m.stub.launched) != 1 || m.stub.launched[0].Recipe != orchestrator.RecipeReviewOnly {
		t.Fatalf("the ladder launched %+v, and D2043's first step is a review-only run", m.stub.launched)
	}
	if m.stub.launched[0].HeadSHA != headA {
		t.Fatalf("the review was asked for head %q, and it reads the head under judgement", m.stub.launched[0].HeadSHA)
	}
	m.stub.line("ladder-r3-review-" + headA[:8])

	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))
	if len(m.stub.launched) != 1 {
		t.Fatalf("the ladder launched %+v, and one review per head is the bound D2043 states", m.stub.launched[1:])
	}
	refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateReviewSpent))

	read = mergePass(headA)
	m.reader.reads["wi-1"] = read
	m.host.state = gitprov.PROpen
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))
	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Fatalf("the host merged %v, and a scored review of the head is what lets the gate pass", m.host.merged)
	}
}

// TestR3LaunchNeedsTheApprovalTheRungsBelowNeed is G6, F21 and D2042 at the two
// launches rung 3 makes. A review-only run and a judge are runs of the item's
// own work under a person's approval, exactly as rung 0's rebase is, so the gate
// that starts them is held to the same rules every other ladder launch is
// (ladderLaunchable): the item is one an attempt may be declared from, nothing
// it depends on is still open, the approval covers the spec as it now stands,
// and the ladder has runs left. A launch is not this file's to loosen because it
// is reached through the hook rather than through rung 0.
//
// The refusal is a person's question (G5): the ladder cannot start the run the
// head needs, and no cycle of it ever will, so the item waits rather than
// spending a run nothing approved.
//
// Mutation: drop the check from either launch clause and the case below starts a
// review, or a judge, for an item no person's approval covers.
func TestR3LaunchNeedsTheApprovalTheRungsBelowNeed(t *testing.T) {
	now := o6Now()

	for _, tc := range []struct {
		name string
		edit func(*orchestrator.State)
	}{
		{
			// No approval at all is the position G6 closes: the merge is not
			// the launch, so what this refuses is the run and not the landing.
			"no approval on the item",
			func(s *orchestrator.State) {
				mergeChange(s, "wi-1", func(w *orchestrator.WorkItem) { w.Approved, w.SpentApprovalAt = nil, nil })
			},
		},
		{
			// A spent marker whose runs are not in the state: the spec the
			// approval covered cannot be read, and the one thing not to do with
			// a spec nobody can place is to escalate it.
			"an approval with no runs under it",
			func(s *orchestrator.State) {
				mergeChange(s, "wi-1", func(w *orchestrator.WorkItem) {
					at := w.SpentApprovalAt.Add(time.Hour)
					w.SpentApprovalAt = &at
				})
			},
		},
		{
			// The item was edited after the approval: what the ladder escalates
			// is the work the person read, or nothing at all.
			"a spec edited since the approval",
			func(s *orchestrator.State) {
				mergeChange(s, "wi-1", func(w *orchestrator.WorkItem) { w.Brief += "\nand one line more" })
			},
		},
		{
			// A run is moving through the item, and a review started beside it
			// reads a head that run is still pushing to.
			"an item a run is still moving",
			func(s *orchestrator.State) {
				mergeChange(s, "wi-1", func(w *orchestrator.WorkItem) { w.Phase = orchestrator.PhaseRunning })
			},
		},
		{
			// The item waits on work that is not done, and the head under
			// judgement is one the work it waits for will move.
			"an item still waiting on another",
			func(s *orchestrator.State) { mergeDependsOn(s, "wi-1", "wi-2") },
		},
		{
			// The ladder's own bound (G15), which the launch is what spends.
			"every ladder run spent",
			func(s *orchestrator.State) {
				mergeChange(s, "wi-1", func(w *orchestrator.WorkItem) {
					w.Escalation.OuterAttempts = orchestrator.MaxOuterAttempts
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, launch := range []struct {
				name string
				read func(string) orchestrator.MergeReading
			}{
				{
					// The head of a rebase no review has read.
					"the review",
					func(head string) orchestrator.MergeReading {
						r := mergePass(head)
						r.Scored, r.ReviewRun = false, ""
						return r
					},
				},
				{
					// The head the review scored and no judge has read.
					"the judge",
					func(head string) orchestrator.MergeReading {
						r := mergePass(head)
						r.JudgeRun = ""
						return r
					},
				},
			} {
				m := landable(t, now, mergeReviewRun, mergeJudgeRun)
				m.edit(tc.edit)
				m.reader.reads["wi-1"] = launch.read(headA)

				m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

				refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateLaunchUncovered))
				if len(m.stub.launched) != 0 {
					t.Fatalf("the ladder launched %+v for %s, and this item is not one it may start a run for", m.stub.launched, launch.name)
				}
			}
		})
	}
}

// TestR3BitbucketWithoutPreconditionWaits is G2 on a host that cannot bind a
// merge to an expected head: the refusal is a person's question with the
// provider's closed code on it, and never a merge taken on the hope that the
// head is still the judged one.
//
// Mutation: retry the merge without the precondition and a head that moved
// between the gate and the call lands unreviewed.
func TestR3BitbucketWithoutPreconditionWaits(t *testing.T) {
	now := o6Now()
	m := landable(t, now)
	m.host.mergeErr = gitprov.ErrMergeNoHeadPrecondition
	m.act("wi-1", r3Step(escalation.CeilingAutoMerge), escalation.ReasonR2Clean, r3Read(headA))

	refusedAt(t, m, headA, "ladder-r3-wait-"+headA[:8], string(orchestrator.GateMergeRefused))
	line := m.stub.line("ladder-r3-wait-" + headA[:8])
	if !strings.Contains(line.Text, "no_head_precondition") {
		t.Fatalf("the line reads %q, and a host that cannot bind a merge says so in a closed code", line.Text)
	}
	if !strings.Contains(m.question("wi-1").Question, "no_head_precondition") {
		t.Fatalf("the question reads %q, and the person it goes to reads the code", m.question("wi-1").Question)
	}
	if len(m.host.merged) != 0 {
		t.Fatalf("the host merged %v, and it refused the merge it was asked for", m.host.merged)
	}
}

// TestR3NoAdminBypass is G14: the seam a landing is made through is four calls
// whose arguments are a number, a head and a context, so there is no room in it
// for an admin merge, a bypass or a force — it is a property of the interface and
// not a discipline a caller keeps.
//
// Mutation: add a merge method or an option to the interface and this test fails
// until somebody argues for it.
func TestR3NoAdminBypass(t *testing.T) {
	typ := reflect.TypeOf((*orchestrator.MergeHost)(nil)).Elem()
	var names []string
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		names = append(names, m.Name)
		for j := range m.Type.NumIn() {
			switch got := m.Type.In(j).String(); got {
			case "context.Context", "int", "string":
			default:
				t.Errorf("%s takes a %s, and every argument a landing can name is a number, a head or a context", m.Name, got)
			}
		}
	}
	if !slices.Equal(names, []string{"Checks", "MarkReady", "Merge", "PullRequest"}) {
		t.Fatalf("the seam is %v, and a landing needs exactly these four calls", names)
	}

	var host orchestrator.MergeHost = gitprov.Provider(nil)
	if host != nil {
		t.Fatalf("the provider is not the seam's own implementation: %T", host)
	}
}

// TestR3TickReentersTheRung is the half of rung 3 the hook alone cannot show.
// Rung 3 is a sequence and not a step — a review of the head, a judge's reading
// of it, the landing — and each of the three takes a cycle of its own, so the
// item stands at rung 3 for as long as the sequence runs and every cycle of it
// has to enter the rung the item is already at. A cycle that treated rung 3 as a
// rung already spent would start the review-only run and never come back for the
// judge: D2043's sequence would never finish and no head would ever land.
//
// So this is the wiring rather than the acting: the real cycle over the real
// state (internal/gcpfake, the real Apply) with E10b's hook where E10a's ladder
// calls it, which is the routing the daemon will have. Rung 3 is entered again
// until the sequence is over — and the cycle after the landing, with the host
// answering that the pull request is merged, lands nothing a second time.
func TestR3TickReentersTheRung(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, []string{mergeReviewRun, mergeJudgeRun},
		mergeItem("wi-1", r3Ledger(now, headA, 2)))
	m.host.head, m.host.checks = headA, mergeGreen()

	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": reading(rigor.POC, 4*time.Hour, headA),
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	launcher := &o6Launcher{}
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: launcher,
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
	}
	cycle := func(read orchestrator.MergeReading) {
		t.Helper()
		m.reader.reads["wi-1"] = read
		if _, err := tick.Step(context.Background()); err != nil {
			t.Fatalf("the cycle ended as %v", err)
		}
	}

	// Nothing scored at the head yet: the cycle starts the review D2043
	// requires, and it does not judge a head nothing has read.
	uncovered := mergePass(headA)
	uncovered.Scored, uncovered.ReviewRun = false, ""
	cycle(uncovered)
	if len(m.stub.launched) != 1 || m.stub.launched[0].Recipe != orchestrator.RecipeReviewOnly {
		t.Fatalf("the first cycle started %+v, and a head with no verdict gets the review-only run first", m.stub.launched)
	}
	if m.stub.launched[0].HeadSHA != headA || m.stub.launched[0].RunID != mergeReviewRun {
		t.Errorf("the review was started as %+v, and it is the review of the judged head", m.stub.launched[0])
	}

	// The review came back scored, and the judge is the second cycle's: the
	// re-entry is what gets there, because the item never left rung 3.
	judged := mergePass(headA)
	judged.JudgeRun = ""
	cycle(judged)
	if len(m.stub.launched) != 2 || m.stub.launched[1].Recipe != orchestrator.RecipeMergeJudge {
		t.Fatalf("the second cycle started %+v, and the judge is what a scored head gets", m.stub.launched)
	}
	if m.stub.launched[1].RunID != mergeJudgeRun {
		t.Errorf("the judge was started as %s; want %s", m.stub.launched[1].RunID, mergeJudgeRun)
	}

	// The verdict is in and vetoes nothing: the third cycle lands the head it
	// judged, and the two launches are what the item spent.
	cycle(mergePass(headA))
	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Fatalf("the host merged %v, and the sequence ends in the landing of the judged head", m.host.merged)
	}

	// The host says the head is merged, which is what a landing leaves behind:
	// the cycle after it reads a merged pull request, stops the item — the merge
	// at its head is the end of the rung 3 sequence, and an item left at the
	// rung is one every later cycle walks again — and does nothing else at the
	// host: no second merge, no ready, no question, nothing for a person. What
	// it does read is the pull request itself, which is how it learns the head
	// landed.
	m.host.state = gitprov.PRMerged
	calls := len(m.host.calls)
	cycle(mergePass(headA))
	if len(m.host.merged) != 1 {
		t.Errorf("the host merged %v, and a merged pull request is one nothing merges again", m.host.merged)
	}
	for _, c := range m.host.calls[calls:] {
		if !strings.HasPrefix(c, "pull") && !strings.HasPrefix(c, "checks ") {
			t.Errorf("the host was asked %q after the landing, and a merged pull request is read and nothing more", c)
		}
	}
	if _, ok := m.state().Decision("wi-1-escalation"); ok {
		t.Errorf("a landing under the ceiling asked a person something, and the ceiling decides who is told (D2030)")
	}
	if len(m.sink.got) != 0 {
		t.Errorf("the sink was told %+v, and POC's landing is the quiet one", m.sink.got)
	}
	if started := launcher.started; len(started) != 0 {
		t.Errorf("the loop started %v, and an item the ladder is walking is not one the planner starts beside it", started)
	}

	// Four cycles, and the item is still at the rung it never left.
	if w := m.item("wi-1"); w.Escalation == nil || w.Escalation.Rung != escalation.R3 {
		t.Errorf("the item stands at %+v; want rung %s", w.Escalation, escalation.R3)
	}
}

// TestR3CycleWritesAfterTheHooksWrite is the half of the hook that belongs to
// the cycle rather than to the rung: the hook lands its rung 3 moves through its
// own seam (MergeSeams.Node), so the moment it returns, the state and the ETag
// the cycle read at the top of the Step are older than the node — and the Step
// has more to write, here the question owed to a second item whose runs nobody
// could read. The cycle re-reads the node once the hook is done (reread), so
// what it writes next derives from the document the hook left. A cycle that kept
// the document it read before the hook would be refused by the store's own
// conditional save, the Step would end as ErrStateMoved, and the person wi-2's
// question is owed would never read it.
//
// What the second item is here is anything that makes the cycle write after the
// hook: its reading was not made, which is the one case that asks a person
// without deciding anything (G5).
//
// Mutation: drop the re-read from ladderBeyond and the first cycle below ends as
// "state moved" with no question about wi-2 on the node; write the ledger's
// block without the two names a reader outside the daemon joins on and it reads
// empty after the move, where the item says the repository and the launched
// review it should carry.
func TestR3CycleWritesAfterTheHooksWrite(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, []string{mergeReviewRun, mergeJudgeRun},
		mergeItem("wi-1", r3Ledger(now, headA, 2)),
		mergeItem("wi-2", r2Ledger(now, headB, 0)))
	m.host.head, m.host.checks = headA, mergeGreen()

	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": reading(rigor.POC, 4*time.Hour, headA),
		// The second item's runs are the ones the daemon could not read.
		"wi-2": {},
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: &o6Launcher{},
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
	}

	// wi-1 comes first in the node and the hook is what the cycle reaches for
	// it: the review-only run D2043 requires, written through the hook's seam.
	uncovered := mergePass(headA)
	uncovered.Scored, uncovered.ReviewRun = false, ""
	m.reader.reads["wi-1"] = uncovered
	if _, err := tick.Step(context.Background()); err != nil {
		t.Fatalf("the cycle ended as %v, and the hook's own write is not one the cycle has to hold", err)
	}
	if len(m.stub.launched) != 1 || m.stub.launched[0].Recipe != orchestrator.RecipeReviewOnly {
		t.Fatalf("the cycle started %+v, and the hook is what rung 3's head gets", m.stub.launched)
	}
	question, ok := m.state().Decision("wi-2-escalation")
	if !ok {
		t.Fatalf("the cycle wrote no question about wi-2, and the write it makes after the hook is what this test is about")
	}
	if question.WaitingOn != orchestrator.OnPerson || !strings.Contains(question.Question, "cannot read the runs behind wi-2") {
		t.Fatalf("the question about wi-2 reads %+v, and a reading nobody could make is a person's question (G5)", question)
	}

	// And the write the hook made is still on the node: the transition the
	// review launched under is the ledger's, the line is the log's, and both
	// survived the cycle's own write after them.
	if e := m.item("wi-1").Escalation; e == nil || e.OuterAttempts != 3 {
		t.Fatalf("the ledger reads %+v, and the review the hook launched is one ladder run", e)
	}
	// And the two names the reports job's read filters and joins on are the ones
	// the move writes: the block is replaced whole and not patched, so a block
	// written without them leaves the item in no repository's section and the
	// chain's run — its pull request, hours and cost — behind it (E12, F27).
	if w := m.item("wi-1"); w.Escalation.Repo != w.Repo || w.Escalation.RunID != mergeReviewRun {
		t.Fatalf("the ledger names %q and the run %q; the item it sits on says %q, and the review this move launched is %q",
			w.Escalation.Repo, w.Escalation.RunID, w.Repo, mergeReviewRun)
	}
	m.stub.line("ladder-r3-review-" + headA[:8])

	// The sequence goes on from there: the next cycle judges the head the
	// review scored, which it can only read off what the hook wrote.
	judged := mergePass(headA)
	judged.JudgeRun = ""
	m.reader.reads["wi-1"] = judged
	if _, err := tick.Step(context.Background()); err != nil {
		t.Fatalf("the second cycle ended as %v", err)
	}
	if len(m.stub.launched) != 2 || m.stub.launched[1].Recipe != orchestrator.RecipeMergeJudge {
		t.Fatalf("the second cycle started %+v, and the judge follows the review the hook recorded", m.stub.launched)
	}
}

// TestR4CycleWritesAfterTheHooksWrite is the same half of the hook at the other
// rung the finding names: rung 4's split writes through the seam rung 3's launch
// does (MergeSeams.Node), so the cycle that entered it holds a document the hook
// has already moved past — and its Step is not over. What it owes here is the
// question a second item's unreadable reading raises, and the split is what the
// hook left on the node: the item stopped, and the scope it filed beside it.
//
// Mutation: drop the re-read from ladderBeyond and the cycle ends as "state
// moved" with the split's items on the node and no question about wi-2; leave
// the two names off the block mergeLedgerOp writes and they read empty after the
// split, where the item the block sits on still names both.
func TestR4CycleWritesAfterTheHooksWrite(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil,
		mergeItem("wi-1", r2Ledger(now, headA, 1)),
		mergeItem("wi-2", r2Ledger(now, headB, 0)))
	blocked := mergePass(headA)
	blocked.Verdict = runstore.MergeJudge{
		Verdict: runstore.MergeJudgeBlocked,
		Reason:  "the split is the fundamental issue and not the rest",
		Blocker: "the retry loop has no bound and no test covers it",
		Model:   "test-judge",
	}
	m.reader.reads["wi-1"] = blocked

	// The item is at rung 2 with its one stronger pass spent and the reviewer
	// raising the same finding again, which is the churn D2036 answers with the
	// split rather than with a rung 3 the item never reaches.
	churned := reading(rigor.POC, 4*time.Hour, headA)
	churned.Churn = escalation.KindSame
	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": churned,
		// The second item's runs are the ones the daemon could not read.
		"wi-2": {},
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: &o6Launcher{},
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
	}

	if _, err := tick.Step(context.Background()); err != nil {
		t.Fatalf("the cycle ended as %v, and the split's own write is not one the cycle has to hold", err)
	}
	if w := m.item("wi-1"); w.Phase != orchestrator.PhaseCancelled || w.Escalation == nil || w.Escalation.Rung != escalation.R4 {
		t.Fatalf("the item reads %+v, and the churn the ladder read is rung 4's", w)
	}
	// The split starts no run, so the block names the item's own run — and the
	// repository the reports job's digest filters on is still the item's, the
	// move having replaced the whole block.
	if w := m.item("wi-1"); w.Escalation.Repo != w.Repo || w.Escalation.RunID != w.RunID {
		t.Fatalf("the ledger names %q and the run %q; the item it sits on says %q and %q",
			w.Escalation.Repo, w.Escalation.RunID, w.Repo, w.RunID)
	}
	split, ok := m.state().Item("wi-1-split")
	if !ok {
		t.Fatalf("the node carries no split: %+v", m.state().WorkItems)
	}
	if !strings.Contains(split.Brief, "the retry loop has no bound") {
		t.Fatalf("the split's brief reads %q, and the judge's blocker is what it is scoped to", split.Brief)
	}
	question, ok := m.state().Decision("wi-2-escalation")
	if !ok {
		t.Fatalf("the cycle wrote no question about wi-2, and the write it makes after the hook is what this test is about")
	}
	if question.WaitingOn != orchestrator.OnPerson || !strings.Contains(question.Question, "cannot read the runs behind wi-2") {
		t.Fatalf("the question about wi-2 reads %+v, and a reading nobody could make is a person's question (G5)", question)
	}
	if len(m.host.calls) != 0 {
		t.Errorf("the host was asked %v, and the split keeps the branch and the pull request", m.host.calls)
	}
	// And the write the hook made is still on the node: the transition the
	// split was recorded under, and the line beside it.
	m.stub.line("ladder-r4-split-" + headA[:8])
}

// TestR3HookCyclesDoNotTripTheBreaker: a cycle whose whole work was the hook's
// is a cycle that did something, and the breaker has to read it that way. The
// moves these rungs make are landed through the hook's own seam and counted
// nowhere else in the Step, so a cycle the hook moved the item on reports
// nothing done at all unless the hook's own answer counts — and three of those
// in a row reach the threshold and put the breaker's question on the node, which
// pauses the plan and the ladder daemon-wide over a ladder that is working
// (D1228, Report.Escalated).
//
// The three cycles here are rung 3's whole sequence, one move each: the review
// D2043 requires, the judge a scored head gets, and the landing. The other side
// of the count — the cycles a rung 3 hook is read for and moved nothing on — is
// the merged pull request below (TestR3MergedPullRequestLeavesTheItemStopped).
//
// Mutation: drop c.rep.Escalated++ from ladderBeyond's hook branch and the first
// cycle below reports CycleEmpty, and the third leaves the breaker's question on
// the node.
func TestR3HookCyclesDoNotTripTheBreaker(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, []string{mergeReviewRun, mergeJudgeRun},
		mergeItem("wi-1", r3Ledger(now, headA, 2)))
	m.host.head, m.host.checks = headA, mergeGreen()

	// Nothing scored at the head, which is where a rung 3 item stands before
	// D2043's review; then the review came back scored and the judge is owed;
	// then the verdict is in and the head lands.
	uncovered := mergePass(headA)
	uncovered.Scored, uncovered.ReviewRun = false, ""
	judged := mergePass(headA)
	judged.JudgeRun = ""

	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": reading(rigor.POC, 4*time.Hour, headA),
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	var breaker orchestrator.Breaker
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: &o6Launcher{},
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
		Breaker:  &breaker,
	}

	// The threshold's own number of cycles, every one of them a move the hook
	// made: the item stands at rung 3 throughout, and the rung it is at is the
	// one the hook re-enters for as long as the sequence runs.
	for i, read := range []orchestrator.MergeReading{uncovered, judged, mergePass(headA)} {
		m.reader.reads["wi-1"] = read
		rep, err := tick.Step(context.Background())
		if err != nil {
			t.Fatalf("cycle %d ended as %v, and the hook acted on it", i+1, err)
		}
		if rep.Escalated != 1 || rep.Outcome != orchestrator.CycleProgress {
			t.Fatalf("cycle %d reports %+v, and a cycle the hook was entered for moved the item", i+1, rep)
		}
	}
	if len(m.stub.launched) != 2 {
		t.Errorf("the hook launched %+v, and one head spends one review and one judge", m.stub.launched)
	}
	if !slices.Equal(m.host.merged, []string{headA}) {
		t.Errorf("the host merged %v, and the sequence ends in the landing of the judged head", m.host.merged)
	}
	if orchestrator.BreakerOpen(m.state(), now) {
		t.Fatalf("three cycles the hook acted on asked a person to look at a ladder that is working: %+v", m.state().Decisions)
	}
}

// TestR3MergedPullRequestLeavesTheItemStopped is the other side of the count:
// the cycle that reads a pull request already merged at the head has nothing to
// do for the item and must read as one that did nothing, and the item it finds
// merged must not be walked again.
//
// Neither half is free. The ledger keeps an item at rung 3 — the rung a landing
// is written at is the rung a later decision answers from, and nothing carries
// an item to rung 4 but the judge — so the item that is not stopped is one the
// hook re-enters on every tick of the daemon's life: the host is asked for the
// pull request and its checks for ever, and a hook whose clean return counted as
// a move would report progress for ever, which is the breaker defeated in the
// one state that never ends (D1228).
//
// Mutation: drop the cancelItemOp from merge's landing branch and the item is
// left failed at rung 3, so every cycle below reads the host again and the item
// is never over; count every clean hook return as a move again and the first
// cycle reports CycleProgress where the item it found merged moved nothing.
func TestR3MergedPullRequestLeavesTheItemStopped(t *testing.T) {
	now := o6Now()
	m := newMergeHarness(t, now, nil, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	m.reader.reads["wi-1"] = mergePass(headA)
	// A pull request the ladder did not land: no marker of its own, and the
	// host's word for it is the merge. It is D1245's gate read at a head whose
	// merge is not this ladder's, which is where the item that is never stopped
	// loops.
	m.host.head, m.host.checks, m.host.state = headA, mergeGreen(), gitprov.PRMerged

	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": reading(rigor.POC, 4*time.Hour, headA),
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: &o6Launcher{},
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
	}

	for i := range orchestrator.BreakerThreshold {
		rep, err := tick.Step(context.Background())
		if err != nil {
			t.Fatalf("cycle %d ended as %v", i+1, err)
		}
		if rep.Escalated != 0 || rep.Outcome != orchestrator.CycleEmpty {
			t.Fatalf("cycle %d reports %+v, and a cycle that found the pull request merged moved nothing", i+1, rep)
		}
	}
	if len(m.host.merged) != 0 {
		t.Errorf("the host merged %v, and a pull request that is already merged is not merged again", m.host.merged)
	}
	if got := m.item("wi-1").Phase; got != orchestrator.PhaseCancelled {
		t.Fatalf("the item is %s, and the item a merge is behind is over", got)
	}
	// The item is terminal, so the cycles after the first read no host at all:
	// the poll is three reads of the pull request and its checks if the item is
	// left standing, and one of them here, from the cycle that stops it.
	if len(m.host.calls) != 2 {
		t.Fatalf("the host was asked %v, and a stopped item is no reason to ask it anything", m.host.calls)
	}
}

// TestR3HookFailureLeavesTheBreakerItsQuestion is the hook's failure and not its
// return: a rung 3 move that landed on the node and a line the log would not
// take leave the hook failing with the cycle holding the document it read before
// the write — exactly the shape a clean return leaves, and the Step still has
// one write left, the breaker's question on the cycle that reached the threshold
// (Step's ask). A cycle that kept the stale document has that question refused
// like any other write, so the daemon fails on and on with nobody asked, which
// is the opposite of what the streak is for (D1228).
//
// Mutation: drop the re-read from ladderBeyond's failure branch and the third
// cycle below ends with no breaker question on the node at all.
func TestR3HookFailureLeavesTheBreakerItsQuestion(t *testing.T) {
	now := o6Now()
	// Three ids for three cycles: a mint is asked for its id before the launch
	// that is refused, so the two failing launches spend one each.
	m := newMergeHarness(t, now, []string{mergeRun, mergeRun, mergeReviewRun}, mergeItem("wi-1", r3Ledger(now, headA, 2)))
	m.host.head, m.host.checks = headA, mergeGreen()

	// Nothing scored at the head: the hook's first move is D2043's review, which
	// is the write it lands before the line fails.
	uncovered := mergePass(headA)
	uncovered.Scored, uncovered.ReviewRun = false, ""
	m.reader.reads["wi-1"] = uncovered

	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{
		"wi-1": reading(rigor.POC, 4*time.Hour, headA),
	}}
	seams := l.seams(escalation.DefaultThresholds())
	seams.Beyond = m.seams.Beyond()
	var breaker orchestrator.Breaker
	tick := &orchestrator.Tick{
		Store:    m.node,
		Runs:     ladderNoRuns{},
		Launcher: &o6Launcher{},
		Mint:     func(time.Time) (string, error) { return mergeRun, nil },
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Ladder:   seams,
		Notifier: m.sink,
		Breaker:  &breaker,
	}

	// The two cycles before the threshold: the launch is what fails, so the
	// hook has written nothing and the streak is the only thing they are for.
	m.stub.launchErr = errors.New("the launcher refused")
	for i := range orchestrator.BreakerThreshold - 1 {
		if _, err := tick.Step(context.Background()); err == nil {
			t.Fatalf("cycle %d ended cleanly, and the launcher refused it", i+1)
		}
	}

	// The cycle that reaches the threshold: the launch is taken, the ledger it
	// writes is on the node, and the line failing is what ends the cycle.
	m.stub.launchErr, m.stub.appendErr = nil, errors.New("the log would not take the line")
	if _, err := tick.Step(context.Background()); err == nil {
		t.Fatalf("the cycle ended cleanly, and the line the hook's log would not take fails it")
	}
	if e := m.item("wi-1").Escalation; e == nil || e.OuterAttempts != 3 {
		t.Fatalf("the ledger reads %+v, and the review the hook launched is one ladder run", e)
	}
	if !orchestrator.BreakerOpen(m.state(), now) {
		t.Fatalf("the cycle that reached the threshold asked a person nothing: %+v", m.state().Decisions)
	}
}
