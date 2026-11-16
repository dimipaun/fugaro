package orchestrator_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The merge read's tests (G3).
//
// A whole tick is driven here, as the tick's and the label syncer's tests are:
// the node is the state a test seeds, the run view is what the test says the
// runs reported, and the reader behind the merge seam is a fake of this
// package's own — so what is pinned is what a daemon does with an item waiting
// on its pull request: the fold that leaves it in `review`, the read that moves
// it when the pull request merges or closes, the read that costs a line and
// never the cycle, the cadence no wake may shorten, the listing that saves the
// `get`s it can, and the one fact a Bitbucket installation with no read
// consumer leaves.

// mergeRepo is the repository these tests' items are on, and mergeBitbucket is
// the one whose host has no listing at all: names of the same shape as any
// other, which is the whole of what the daemon knows about a host from the
// node.
const (
	mergeRepo      = "owner/repo"
	mergeBitbucket = "workspace/repo"
)

// The run ids a merge test works with: ids a run really carries, because the
// state validator refuses anything else.
const (
	mergeRunA = "20261107-090000-a1b2"
	mergeRunB = "20261107-090001-c3d4"
	mergeRunC = "20261107-090002-e5f6"
)

// mergeReader is the reader behind the merge seam: the open pull requests of
// each repository, the hosts with no listing, one pull request's state by
// number, and the failures a test makes it answer with. It records what it was
// asked, in the order it was asked, so a test asserts on the reads a cadence
// made rather than on a count of them.
type mergeReader struct {
	open     map[string]map[int]bool
	noList   map[string]bool
	states   map[string]orchestrator.MergeState
	listErr  map[string]error
	stateErr map[string]error
	lists    []string
	gets     []string
}

// mergeKey is one pull request as this fake is keyed and as its call log names
// it.
func mergeKey(repo string, number int) string { return fmt.Sprintf("%s#%d", repo, number) }

func (r *mergeReader) ListOpen(_ context.Context, repo string) (map[int]bool, bool, error) {
	r.lists = append(r.lists, repo)
	if err := r.listErr[repo]; err != nil {
		return nil, false, err
	}
	if r.noList[repo] {
		return nil, false, nil
	}
	return r.open[repo], true, nil
}

func (r *mergeReader) State(_ context.Context, repo string, number int) (orchestrator.MergeState, error) {
	key := mergeKey(repo, number)
	r.gets = append(r.gets, key)
	if err := r.stateErr[key]; err != nil {
		return "", err
	}
	return r.states[key], nil
}

// mergeLines is the log a merge test reads.
type mergeLines struct{ lines []string }

func (l *mergeLines) Warn(_ context.Context, line string) { l.lines = append(l.lines, line) }

// mergeClock is the clock a merge test advances: the database server's reading,
// which is what a cadence is measured against (D1230).
type mergeClock struct{ at time.Time }

func (c *mergeClock) now() (time.Time, bool) { return c.at, true }

// mergeHarness is one daemon's tick with the merge read wired to a fake, and
// the clock a test moves. A second step is the same daemon's next tick, which
// is what a cadence is measured across; moving the clock is the server
// answering a later reading.
type mergeHarness struct {
	*harness
	reads *mergeReader
	lines *mergeLines
	clock *mergeClock
}

func newMergeHarness(t *testing.T, s orchestrator.State, now time.Time, runs ...string) *mergeHarness {
	t.Helper()
	h := newHarness(t, s, now, runs...)
	reads := &mergeReader{
		open:     map[string]map[int]bool{},
		noList:   map[string]bool{},
		states:   map[string]orchestrator.MergeState{},
		listErr:  map[string]error{},
		stateErr: map[string]error{},
	}
	lines, clock := &mergeLines{}, &mergeClock{at: now}
	h.tick.Merge = orchestrator.MergeSeams{Reader: reads, Log: lines}
	h.tick.Now = clock.now
	return &mergeHarness{harness: h, reads: reads, lines: lines, clock: clock}
}

// mergePhase is one item's phase as the node now stands.
func (h *mergeHarness) mergePhase(id string) orchestrator.Phase {
	h.t.Helper()
	w, ok := h.state().Item(id)
	if !ok {
		h.t.Fatalf("the node no longer carries %s", id)
	}
	return w.Phase
}

// mergeItem is one item of these tests: the standard declaration on the
// repository the caller names, the phase it is in, and a person's standing
// approval over it.
func mergeItem(id, repo string, phase orchestrator.Phase, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.Repo = repo
	w.Phase = phase
	return o6ApprovedBy(w, approvedAt)
}

// mergeDependent is an item that waits on the items the caller names: the
// dependency is set before the approval, because the approval is over the
// declaration the item carries and a field changed after it has drifted from
// what a person read.
func mergeDependent(id, repo string, on []string, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.Repo = repo
	w.DependsOn = on
	w.Phase = orchestrator.PhaseQueued
	return o6ApprovedBy(w, approvedAt)
}

// mergeAttempt is one attempt of an item as the node records it: the run id the
// daemon minted before anything started, the approval it spends, and how it
// ended — none while its run is in flight.
func mergeAttempt(w orchestrator.WorkItem, runID string, outcome orchestrator.Outcome) orchestrator.Intent {
	return orchestrator.Intent{
		ID:         orchestrator.IntentID(w.ID, 1),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: w.Approved.At,
		At:         w.Approved.At.Add(time.Minute),
		Outcome:    outcome,
	}
}

// mergeOpenItem is an item whose attempt is committed and has not reported: the
// position the fold reads its run's outcome from.
func mergeOpenItem(id, repo, runID string, approvedAt time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	w := mergeItem(id, repo, orchestrator.PhaseRunning, approvedAt)
	w.RunID = runID
	w.Attempts = 1
	return w, mergeAttempt(w, runID, "")
}

// mergeReviewItem is an item waiting on a merge: its attempt reported succeeded
// and left a pull request, which is where the fold leaves such an item (D2705).
func mergeReviewItem(id, repo, runID string, approvedAt time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	w := mergeItem(id, repo, orchestrator.PhaseReview, approvedAt)
	w.RunID = runID
	w.Attempts = 1
	return w, mergeAttempt(w, runID, orchestrator.OutcomeSucceeded)
}

// mergeReport is what a run of these tests reported: it succeeded and left the
// pull request the caller names.
func mergeReport(runID string, number int) orchestrator.RunReport {
	return orchestrator.RunReport{
		RunID: runID, Started: true, Finished: true,
		Outcome: orchestrator.OutcomeSucceeded, PRNumber: number,
	}
}

// TestSucceededWithPRGoesToReview: a run that succeeded having left a pull
// request leaves its item in `review`, where it waits for that pull request to
// merge; one that left none is finished work. The fold alone decides this, so
// the merge read is not wired in this test.
func TestSucceededWithPRGoesToReview(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)

	t.Run("a run that left a pull request leaves the item in review", func(t *testing.T) {
		w, it := mergeOpenItem("wi-1", mergeRepo, mergeRunA, approvedAt)
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}

		rep := h.step()
		if rep.Folded != 1 {
			t.Fatalf("the tick folded %d finished runs where one reported", rep.Folded)
		}
		w1, ok := h.state().Item("wi-1")
		if !ok {
			t.Fatalf("the node no longer carries wi-1")
		}
		if w1.Phase != orchestrator.PhaseReview {
			t.Errorf("the item is %q, and a run that left a pull request waits for it to merge (D2705)", w1.Phase)
		}
	})

	t.Run("a run that left none is done at once", func(t *testing.T) {
		w, it := mergeOpenItem("wi-1", mergeRepo, mergeRunA, approvedAt)
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		h.reader.reports = []orchestrator.RunReport{{
			RunID: mergeRunA, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded,
		}}

		h.step()
		w1, ok := h.state().Item("wi-1")
		if !ok {
			t.Fatalf("the node no longer carries wi-1")
		}
		if w1.Phase != orchestrator.PhaseDone {
			t.Errorf("the item is %q, and a run that left no pull request has nothing to wait for", w1.Phase)
		}
	})
}

// TestBitbucketSucceededGoesToReview: a Bitbucket repository's run goes through
// `review` like a GitHub one (D2736 as amended by the owner). The host with no
// listing is read a `get` at a time, and that read is what says whether the
// item still waits.
func TestBitbucketSucceededGoesToReview(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeOpenItem("wi-1", mergeBitbucket, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.noList[mergeBitbucket] = true
	h.reads.states[mergeKey(mergeBitbucket, 12)] = orchestrator.MergeOpen

	h.step()

	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseReview {
		t.Errorf("the item is %q, and a Bitbucket run that left a pull request waits for its merge like any other", got)
	}
	if got := h.reads.gets; len(got) != 1 || got[0] != mergeKey(mergeBitbucket, 12) {
		t.Errorf("the repository's reads are %q, and a host with no listing is read one pull request at a time", got)
	}
}

// TestMergedPRMovesToDone: the item whose pull request has merged is `done`,
// which is what releases everything that depends on it.
func TestMergedPRMovesToDone(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeReviewItem("wi-1", mergeRepo, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	// A merged pull request is not in the listing of what is open: that is the
	// `get` D2742 spends its budget on.
	h.reads.open[mergeRepo] = map[int]bool{}
	h.reads.states[mergeKey(mergeRepo, 12)] = orchestrator.MergeMerged

	rep := h.step()

	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseDone {
		t.Errorf("the item is %q, and its pull request merged", got)
	}
	if rep.Merged != 1 {
		t.Errorf("the cycle reads %d items as merged, and one pull request merged", rep.Merged)
	}
	if rep.Outcome != orchestrator.CycleProgress {
		t.Errorf("a cycle that moved an item out of review ended %q, and it did work", rep.Outcome)
	}
	if len(h.launcher.asked) != 0 {
		t.Errorf("the cycle launched %q, and a merge launches nothing", h.launcher.asked)
	}
}

// TestClosedUnmergedFails: a pull request closed without merging fails the
// item. It is not a wait that goes on — nothing will merge it — and it is not
// the success the run itself reported.
func TestClosedUnmergedFails(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeReviewItem("wi-1", mergeRepo, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.open[mergeRepo] = map[int]bool{}
	h.reads.states[mergeKey(mergeRepo, 12)] = orchestrator.MergeClosed

	rep := h.step()

	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseFailed {
		t.Errorf("the item is %q, and its pull request closed without merging", got)
	}
	if rep.Merged != 1 {
		t.Errorf("the cycle moved %d items out of review, and a closed pull request is one of them", rep.Merged)
	}
}

// TestFailedMergeReadLeavesReview: a read that did not happen is not an answer.
// The item waits where it is, the cycle goes on, and the failure is one line.
func TestFailedMergeReadLeavesReview(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeReviewItem("wi-1", mergeRepo, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.listErr[mergeRepo] = fmt.Errorf("the host said no")

	rep := h.step()

	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseReview {
		t.Errorf("the item is %q, and a read that failed said nothing about its pull request", got)
	}
	if rep.Merged != 0 {
		t.Errorf("the cycle reads %d items as merged, and it read none", rep.Merged)
	}
	if len(h.lines.lines) != 1 || !strings.Contains(h.lines.lines[0], mergeRepo) {
		t.Errorf("the read of %s left the lines %q, and a failure is one line naming the repository", mergeRepo, h.lines.lines)
	}
}

// TestDependentWaitsForMerge is the whole of D2705: the run succeeding is not
// what unlocks the work behind it — the merge is. The dependent waits through
// the success, and launches in the cycle the pull request merges.
func TestDependentWaitsForMerge(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	a, itA := mergeOpenItem("wi-a", mergeRepo, mergeRunA, approvedAt)
	b := mergeDependent("wi-b", mergeRepo, []string{"wi-a"}, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{a, b}, []orchestrator.Intent{itA}), now, mergeRunC)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.open[mergeRepo] = map[int]bool{12: true}

	h.step()

	if got := h.mergePhase("wi-a"); got != orchestrator.PhaseReview {
		t.Fatalf("the item that succeeded is %q, and it waits for its pull request to merge", got)
	}
	if got := h.mergePhase("wi-b"); got != orchestrator.PhaseQueued {
		t.Errorf("the dependent is %q, and the item it waits on has not merged", got)
	}
	if len(h.launcher.asked) != 0 {
		t.Errorf("the cycle launched %q, and the item the dependent waits on is in review", h.launcher.asked)
	}

	// A person merges the pull request, and the next cadence reads it.
	h.reads.open[mergeRepo] = map[int]bool{}
	h.reads.states[mergeKey(mergeRepo, 12)] = orchestrator.MergeMerged
	h.clock.at = now.Add(orchestrator.MergeReadEvery)

	h.step()

	if got := h.mergePhase("wi-a"); got != orchestrator.PhaseDone {
		t.Fatalf("the item is %q after its pull request merged", got)
	}
	if len(h.launcher.asked) != 1 || h.launcher.asked[0] != mergeRunC {
		t.Errorf("the dependent launched as %q, and the merge is what unlocks it", h.launcher.asked)
	}
	if w, ok := h.state().Item("wi-b"); !ok || w.RunID != mergeRunC {
		t.Errorf("the dependent's node entry is %+v, and the launch is recorded on it", w)
	}
}

// TestMergeReadKeepsItsCadence: the read is per repository per cadence, and a
// wake is not a reason to make another one (D2742). The loop wakes on a stream
// and on a ceiling, so a read per wake would spend the label route's budget the
// runs themselves go through.
func TestMergeReadKeepsItsCadence(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeReviewItem("wi-1", mergeRepo, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.open[mergeRepo] = map[int]bool{12: true}

	h.step()
	if got := len(h.reads.lists); got != 1 {
		t.Fatalf("the first tick listed %d times, and the first read of a repository is due", got)
	}

	// A wake inside the cadence, and the ceiling's own tick beside it.
	h.step()
	h.clock.at = now.Add(orchestrator.MergeReadEvery - time.Second)
	h.step()
	if got := len(h.reads.lists); got != 1 {
		t.Errorf("the repository was listed %d times inside one cadence, and a wake is not a reading (D2742)", got)
	}
	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseReview {
		t.Errorf("the item is %q, and no read was made to move it", got)
	}

	h.clock.at = now.Add(orchestrator.MergeReadEvery)
	h.step()
	if got := len(h.reads.lists); got != 2 {
		t.Errorf("the repository was listed %d times past one cadence, and the next read is due", got)
	}
}

// TestMergeReadGetsOnlyMissing: the listing answers for every pull request it
// carries, and only what it does not carry is read one by one (D2742) — a
// merged or closed pull request is not in it, and a `get` is the only way to
// tell those two from an open one the listing is a moment behind on.
func TestMergeReadGetsOnlyMissing(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	open, itOpen := mergeReviewItem("wi-open", mergeRepo, mergeRunA, approvedAt)
	merged, itMerged := mergeReviewItem("wi-merged", mergeRepo, mergeRunB, approvedAt)
	h := newMergeHarness(t, tickState(t, now,
		[]orchestrator.WorkItem{open, merged}, []orchestrator.Intent{itOpen, itMerged}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12), mergeReport(mergeRunB, 14)}
	h.reads.open[mergeRepo] = map[int]bool{12: true}
	h.reads.states[mergeKey(mergeRepo, 14)] = orchestrator.MergeMerged

	h.step()

	if got := h.reads.gets; len(got) != 1 || got[0] != mergeKey(mergeRepo, 14) {
		t.Errorf("the cycle read %q, and only the pull request the listing does not carry is read (D2742)", got)
	}
	if len(h.reads.lists) != 1 {
		t.Errorf("the repository was listed %d times, and one listing answers for every item of it", len(h.reads.lists))
	}
	if got := h.mergePhase("wi-open"); got != orchestrator.PhaseReview {
		t.Errorf("the open item's phase is %q, and the listing says its pull request is open", got)
	}
	if got := h.mergePhase("wi-merged"); got != orchestrator.PhaseDone {
		t.Errorf("the merged item's phase is %q, and its pull request merged", got)
	}
}

// TestBitbucketMergeReadGetsEachItem: a host with no listing is read a `get` at
// a time (D2736 as amended by the owner). There is nothing to save the reads
// with — a Bitbucket pull request's state is one call — so every item in review
// is read, and each one the host reports open waits.
func TestBitbucketMergeReadGetsEachItem(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	a, itA := mergeReviewItem("wi-a", mergeBitbucket, mergeRunA, approvedAt)
	b, itB := mergeReviewItem("wi-b", mergeBitbucket, mergeRunB, approvedAt)
	h := newMergeHarness(t, tickState(t, now,
		[]orchestrator.WorkItem{a, b}, []orchestrator.Intent{itA, itB}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12), mergeReport(mergeRunB, 14)}
	h.reads.noList[mergeBitbucket] = true
	h.reads.states[mergeKey(mergeBitbucket, 12)] = orchestrator.MergeMerged
	h.reads.states[mergeKey(mergeBitbucket, 14)] = orchestrator.MergeOpen

	h.step()

	if got := h.reads.gets; len(got) != 2 ||
		got[0] != mergeKey(mergeBitbucket, 12) || got[1] != mergeKey(mergeBitbucket, 14) {
		t.Errorf("the cycle read %q, and a host with no listing is read one pull request per item", got)
	}
	if got := h.mergePhase("wi-a"); got != orchestrator.PhaseDone {
		t.Errorf("the merged item's phase is %q", got)
	}
	if got := h.mergePhase("wi-b"); got != orchestrator.PhaseReview {
		t.Errorf("the open item's phase is %q, and it waits", got)
	}
}

// TestNoReadConsumerRecordsOneFact: an installation that can read no Bitbucket
// pull request records one fact naming the repository and the fix, once per
// repository and not once per cadence (G3, D2736). The items stay in `review`:
// the merge is still what they wait for, and the fix is what makes a read
// possible.
func TestNoReadConsumerRecordsOneFact(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := mergeReviewItem("wi-1", mergeBitbucket, mergeRunA, approvedAt)
	h := newMergeHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12)}
	h.reads.noList[mergeBitbucket] = true
	h.reads.stateErr[mergeKey(mergeBitbucket, 12)] = orchestrator.ErrMergeNoReadConsumer

	h.step()

	facts := h.state().Facts
	if len(facts) != 1 {
		t.Fatalf("the node carries %d facts where the missing read consumer is one: %+v", len(facts), facts)
	}
	var key, value string
	for k, f := range facts {
		key, value = k, f.Value
		if f.By != orchestrator.Daemon {
			t.Errorf("the fact %q is from %q, and the daemon is what made the read", k, f.By)
		}
	}
	if !strings.Contains(value, mergeBitbucket) {
		t.Errorf("the fact %q reads %q, and it names the repository the read is about", key, value)
	}
	if !strings.Contains(value, "fugaro creds set bitbucket-read-consumer") {
		t.Errorf("the fact %q reads %q, and it names the fix", key, value)
	}
	if got := h.mergePhase("wi-1"); got != orchestrator.PhaseReview {
		t.Errorf("the item is %q, and a read that could not be made says nothing about its pull request", got)
	}

	// The next cadence: the read is tried again — a consumer set since has to
	// heal the repository without a restart — and the fact it already recorded
	// is not written again.
	h.clock.at = now.Add(orchestrator.MergeReadEvery)
	h.step()

	if got := len(h.reads.gets); got != 2 {
		t.Errorf("the pull request was read %d times over two cadences, and the read is what heals (D2742)", got)
	}
	if got := len(h.state().Facts); got != 1 {
		t.Errorf("the node carries %d facts after a second cadence, and the fact is recorded once per repository", got)
	}
}
