package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Adoption (R4, D2702): the runs a daemon's first start finds in flight, the
// items and the intents it writes for them, and the one rung it may not walk
// for them.
//
// What is pinned here is that step and nothing else: the runs come from the
// lister the test hands it — the seam W1 builds the real one behind — and the
// store is the real one (internal/gcpfake), so an item this file reads back is
// a document this release accepted.

// The run ids these tests find in flight.
const (
	runA = "20261107-100000-0a0a"
	runB = "20261107-100001-1b1b"
	runC = "20261107-100002-2c2c"
	runD = "20261107-100003-3d3d"
)

// adoptLister is the RunLister seam as a test sets it: the runs in flight it
// answers with, an error where the read itself is the subject, and the count of
// times it was asked.
type adoptLister struct {
	runs  []orchestrator.RunInFlight
	err   error
	calls int
}

func (l *adoptLister) RunsInFlight(context.Context) ([]orchestrator.RunInFlight, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return l.runs, nil
}

// adoptRun is one code-change run in flight of these tests' shape: a complete
// record, so that what an item is missing is never this file's fixture, and the
// plan task the launch recorded (empty for a run launched with none).
func adoptRun(runID, planTask string) orchestrator.RunInFlight {
	return orchestrator.RunInFlight{
		RunID:     runID,
		Repo:      "owner/repo",
		Title:     "a run somebody else started",
		Recipe:    "default",
		RigorTier: "small",
		Brief:     "the brief the run was launched with",
		PlanTask:  planTask,
	}
}

// modeRun is one run in flight of the mode the caller names.
func modeRun(runID, mode string) orchestrator.RunInFlight {
	r := adoptRun(runID, "")
	r.Mode = mode
	return r
}

// ladderRun is one run in flight of the escalation ladder's own recipes: a run
// of an item already on the node, whose record carries the recipe and — like
// every code-change run — no mode at all.
func ladderRun(runID, ladderRecipe string) orchestrator.RunInFlight {
	r := adoptRun(runID, "")
	r.Recipe = ladderRecipe
	return r
}

// seededNode is a database holding the state the caller hands in, written
// through the node a person's own patch writes through, plus the node adoption
// is then run over: a fresh fake whose Date header answers with the test's
// instant, and a lease the daemon holds.
func seededNode(t *testing.T, at time.Time, s orchestrator.State) (*gcpfake.RTDB, *orchestrator.Node) {
	t.Helper()
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return at })
	plain := mustNode(t, rtdbClient(t, f), nil)
	_, etag, err := plain.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty node: %v", err)
	}
	if err := plain.Save(ctx, s, etag); err != nil {
		t.Fatalf("seeding the node with the test's state: %v", err)
	}
	return f, adoptNode(t, f, at, &stubLease{held: true})
}

// adoptNode is the node adoption writes through: a fresh database whose Date
// header answers with the test's instant, which is the only clock adoption
// stamps with (D1230), and the lease the caller names.
func adoptNode(t *testing.T, f *gcpfake.RTDB, at time.Time, lease orchestrator.LeaseGuard) *orchestrator.Node {
	t.Helper()
	f.SetClock(func() time.Time { return at })
	return mustNode(t, rtdbClient(t, f), lease)
}

// adopt is one call of adoption over a fresh state node under a lease the
// daemon holds: the fake it wrote to, the node, the lister it asked and what
// the call answered.
func adopt(t *testing.T, at time.Time, runs ...orchestrator.RunInFlight) (*gcpfake.RTDB, *orchestrator.Node, *adoptLister, int) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	node := adoptNode(t, f, at, &stubLease{held: true})
	lister := &adoptLister{runs: runs}
	n, err := orchestrator.Adopt(context.Background(), lister, node)
	if err != nil {
		t.Fatalf("adoption ended as %v", err)
	}
	return f, node, lister, n
}

// nodeState is a node as the store now reads it.
func nodeState(t *testing.T, node *orchestrator.Node) orchestrator.State {
	t.Helper()
	s, _, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the node back: %v", err)
	}
	return s
}

// planNodeState is a plan's own node as the store now reads it.
func planNodeState(t *testing.T, f *gcpfake.RTDB, plan string) orchestrator.State {
	t.Helper()
	return nodeState(t, mustShardNode(t, rtdbClient(t, f), &stubLease{held: true}, orchestrator.PlanShard(plan)))
}

// denyPathCAS is the store with one node's conditional write refused the way a
// save that lost its race is refused and every other node's left alone: how a
// call whose plan node landed and whose state node did not is reached. It is
// both halves of the client a node is built over, because adoption reads the
// server's clock off the same store it writes through (D1230).
type denyPathCAS struct {
	orchestrator.CAS
	orchestrator.Reader
	path string
	deny bool
}

func (c *denyPathCAS) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	if c.deny && path == c.path {
		return fmt.Errorf("the state node's write lost its race: %w", rtdb.ErrPrecondition)
	}
	return c.CAS.PutIfMatch(ctx, path, etag, v)
}

// TestAdoptWritesRunningItemsOnce is D2702's first half: a run the daemon did
// not start becomes the item the daemon would have written had it started it —
// running, with the run id as its own id and one committed attempt under it —
// and a second start over the same run adopts nothing and writes nothing at
// all, because what adoption reads is the node and not what this process did.
//
// Mutation: write the item without the run's id as its own, or without the
// attempt under it, and the second start writes a second item for the same run.
func TestAdoptWritesRunningItemsOnce(t *testing.T) {
	at := o6Now()
	_, node, lister, got := adopt(t, at, adoptRun(runA, ""))
	if got != 1 {
		t.Fatalf("adoption wrote %d items for one run in flight", got)
	}
	s := nodeState(t, node)
	if len(s.WorkItems) != 1 || len(s.Intents) != 1 {
		t.Fatalf("the node carries %d items and %d intents, want one of each", len(s.WorkItems), len(s.Intents))
	}
	w := s.WorkItems[0]
	if w.ID != runA || w.RunID != runA {
		t.Fatalf("the item is %q running %q, and the item is the run it was written for", w.ID, w.RunID)
	}
	if w.Phase != orchestrator.PhaseRunning || !w.Adopted || w.Attempts != 1 {
		t.Fatalf("the item reads %+v, want one running attempt under the adopted mark", w)
	}
	if w.Title != "a run somebody else started" || w.Brief != "the brief the run was launched with" || w.RigorTier != "small" {
		t.Fatalf("the item reads %+v, and it carries the run's own record", w)
	}
	it := s.Intents[0]
	if it.ID != orchestrator.IntentID(runA, 1) || it.RunID != runA || it.Outcome != "" {
		t.Fatalf("the intent reads %+v, want the attempt the run is under", it)
	}
	if it.SpecDigest != orchestrator.SpecDigest(w) {
		t.Fatalf("the intent's digest is %q and the item's is %q, and the attempt is the item's own", it.SpecDigest, orchestrator.SpecDigest(w))
	}
	if !it.At.Equal(at) || !it.ApprovalAt.Equal(at) {
		t.Fatalf("the intent is stamped %v/%v and the server's clock reads %v", it.At, it.ApprovalAt, at)
	}

	_, before, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the node's etag: %v", err)
	}
	again, err := orchestrator.Adopt(context.Background(), lister, node)
	if err != nil || again != 0 {
		t.Fatalf("the second start wrote %d items and answered %v, want nothing at all", again, err)
	}
	_, after, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the node's etag back: %v", err)
	}
	if after != before {
		t.Fatalf("the second start moved the node's etag from %q to %q, so it wrote", before, after)
	}
	if s := nodeState(t, node); len(s.WorkItems) != 1 || len(s.Intents) != 1 || len(s.Facts) != 0 {
		t.Fatalf("the second start left %d items, %d intents and %d facts", len(s.WorkItems), len(s.Intents), len(s.Facts))
	}
}

// TestAdoptCarriesPlanTask is D2702's name for the work: the item carries the
// plan task the launch recorded, whatever that text is, because the plan's own
// import meets it on exactly that name and writes no second item beside it
// (D2746).
//
// Mutation: drop the plan task from the item and the import duplicates work
// that is already running.
func TestAdoptCarriesPlanTask(t *testing.T) {
	at := o6Now()
	_, node, _, got := adopt(t, at, adoptRun(runA, "plans/2026-11-01-legacy/3"))
	if got != 1 {
		t.Fatalf("adoption wrote %d items for one run in flight", got)
	}
	w := nodeState(t, node).WorkItems[0]
	if w.PlanTask != "plans/2026-11-01-legacy/3" {
		t.Fatalf("the item's plan task is %q, want the launch's own", w.PlanTask)
	}
}

// TestAdoptClipsAnOverCapRecord is the bound every writer of this document
// keeps on free text: a run's brief is the whole task file and its title the
// task's first line, so a record past MaxTitleRunes or MaxBriefRunes is an
// ordinary run — and an item built from it raw is one ValidateState refuses, so
// the run is named in adopt_not_carried and then never folded, never counted
// and never adopted by a later start.
//
// Mutation: build the item from the run's text as it comes and a run launched
// with a long brief is silently not adopted at all.
func TestAdoptClipsAnOverCapRecord(t *testing.T) {
	at := o6Now()
	long := adoptRun(runA, "")
	long.Title = strings.Repeat("t", orchestrator.MaxTitleRunes+100)
	long.Brief = strings.Repeat("b", orchestrator.MaxBriefRunes+500)
	_, node, _, got := adopt(t, at, long)
	if got != 1 {
		t.Fatalf("adoption wrote %d items for one over-cap run in flight, want the one item its other fields are good for", got)
	}
	s := nodeState(t, node)
	if len(s.WorkItems) != 1 {
		t.Fatalf("the node carries %d items, want the one the run's other fields are good for", len(s.WorkItems))
	}
	w := s.WorkItems[0]
	if len([]rune(w.Title)) != orchestrator.MaxTitleRunes || len([]rune(w.Brief)) != orchestrator.MaxBriefRunes {
		t.Fatalf("the item's title is %d runes and its brief %d, want them at the caps of %d and %d",
			len([]rune(w.Title)), len([]rune(w.Brief)), orchestrator.MaxTitleRunes, orchestrator.MaxBriefRunes)
	}
	if !strings.HasPrefix(w.Title, "ttt") || !strings.HasPrefix(w.Brief, "bbb") {
		t.Fatalf("the item reads %q / %q, and clipping keeps the run's own words", w.Title, w.Brief)
	}
	if fact, ok := s.Facts["adopt_not_carried"]; ok {
		t.Fatalf("the node names the run as left out (%q), and it was adopted", fact.Value)
	}
}

// TestAdoptRecordsPlanInFlightFact is D2721: one fact per plan an adopted run
// belongs to, so the session that opens a checkout afterwards imports it and
// takes it to a person — one fact for two runs of the same plan, and none for a
// run that names no plan, which is the one thing that would make the daemon
// look busy with work nobody is doing.
//
// Mutation: record a fact per run and a first start over a busy checkout writes
// more facts than the node holds.
func TestAdoptRecordsPlanInFlightFact(t *testing.T) {
	at := o6Now()
	_, node, _, got := adopt(t, at,
		adoptRun(runA, planFolder+"/3"),
		adoptRun(runB, planFolder+"/4"),
		adoptRun(runC, ""),
	)
	if got != 3 {
		t.Fatalf("adoption wrote %d items for three runs in flight", got)
	}
	s := nodeState(t, node)
	if len(s.Facts) != 1 {
		t.Fatalf("the node carries %d facts, want the one plan two adopted runs name", len(s.Facts))
	}
	key := "plan_in_flight_" + planFolder
	fact, ok := s.Facts[key]
	if !ok {
		t.Fatalf("the node carries no %q fact", key)
	}
	if fact.By != orchestrator.Daemon {
		t.Fatalf("the fact is from %q, and adoption is the daemon's step", fact.By)
	}
	if !strings.Contains(fact.Value, planFolder) {
		t.Fatalf("the fact reads %q, and the plan it is about is what a session acts on", fact.Value)
	}
	if !strings.Contains(fact.Value, "fugaro orchestrator import --plan "+planFolder) {
		t.Fatalf("the fact reads %q, and the import is what it asks for (R5)", fact.Value)
	}
}

// TestAdoptRecordsPlanFactFromTheItem is D2721's hand-over surviving the write
// that makes it: the plan node is saved before the state node, so a state save
// that loses its race leaves the run's item on the plan node with no fact
// beside it — and the next start finds the run claimed by that item and adopts
// nothing. The fact is what a session acts on, and what it is made from is the
// item, so it is written by that later start rather than lost with the save
// that did not land.
//
// Mutation: record the plans from this call's own writes and the fact is lost
// for good the moment one state save is refused.
func TestAdoptRecordsPlanFactFromTheItem(t *testing.T) {
	at := o6Now()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return at })
	client := rtdbClient(t, f)
	store := &denyPathCAS{CAS: client, Reader: client}
	leased := mustNode(t, store, &stubLease{held: true})
	store.path = leased.Path()
	plain := mustNode(t, rtdbClient(t, f), nil)
	lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, planFolder+"/3")}}

	store.deny = true
	n, err := orchestrator.Adopt(context.Background(), lister, leased)
	if err == nil || n != 1 {
		t.Fatalf("adoption wrote %d items and answered %v, want the one plan item and the refused state save", n, err)
	}
	if s := planNodeState(t, f, planFolder); len(s.WorkItems) != 1 {
		t.Fatalf("the plan's node carries %d items, and its save is the one before the state's", len(s.WorkItems))
	}
	if s := nodeState(t, plain); len(s.Facts) != 0 {
		t.Fatalf("the state node carries %v, and its save is the one that was refused", s.Facts)
	}

	store.deny = false
	again, err := orchestrator.Adopt(context.Background(), lister, leased)
	if err != nil || again != 0 {
		t.Fatalf("the second start wrote %d items and answered %v, want the item it already has and nothing new", again, err)
	}
	s := nodeState(t, plain)
	fact, ok := s.Facts["plan_in_flight_"+planFolder]
	if !ok {
		t.Fatalf("the state node carries %v, and the plan the item stands in is what a session has to import", s.Facts)
	}
	if !strings.Contains(fact.Value, "fugaro orchestrator import --plan "+planFolder) {
		t.Fatalf("the fact reads %q, want the import of the plan the item names", fact.Value)
	}
	if s := planNodeState(t, f, planFolder); len(s.WorkItems) != 1 {
		t.Fatalf("the plan's node carries %d items after the second start", len(s.WorkItems))
	}
}

// TestAdoptKeepsItsItemsWhenTheFactsAreAtTheirBound is the node a year of facts
// arrives at: MaxFacts facts and no room for one more. A fact past the bound is
// a document ValidateState refuses, and the state node is saved with the items
// this same call writes into it, so a fact queued regardless would lose the
// items with it — the plan's node saved, the shared node not, and runs adopted
// into it neither folded nor counted. The fact is what adoption drops, the items
// land, and a node with room again records the plan on a later start.
//
// Mutation: queue the fact without asking whether it fits and a node at its
// bound loses every item adopted into it to a save that never lands.
func TestAdoptKeepsItsItemsWhenTheFactsAreAtTheirBound(t *testing.T) {
	at := o6Now()
	seed := stateWithAQueuedItem(t, at)
	seed.WorkItems, seed.Intents = nil, nil
	seed.Facts = map[string]orchestrator.Fact{}
	for i := range orchestrator.MaxFacts {
		seed.Facts[fmt.Sprintf("fact_%02d", i)] = orchestrator.Fact{Value: "a fact of a node that has been running a while", By: orchestrator.Daemon}
	}
	h := newHarness(t, seed, at)
	node := adoptNode(t, h.db, at, &stubLease{held: true})
	lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, ""), adoptRun(runB, planFolder+"/3")}}

	n, err := orchestrator.Adopt(context.Background(), lister, node)
	if err != nil || n != 2 {
		t.Fatalf("adoption wrote %d items and answered %v, want both items on a node whose facts are full", n, err)
	}
	s := h.state()
	if _, ok := s.Item(runA); !ok {
		t.Fatalf("the state node carries %d items and not the run adopted into it", len(s.WorkItems))
	}
	if len(s.Facts) != orchestrator.MaxFacts {
		t.Fatalf("the state node carries %d facts, want the bound of %d it was at", len(s.Facts), orchestrator.MaxFacts)
	}
	if _, ok := s.Facts["plan_in_flight_"+planFolder]; ok {
		t.Fatalf("the state node carries the plan fact, and its facts were full before this call")
	}
	if one := planNodeState(t, h.db, planFolder); len(one.WorkItems) != 1 || one.WorkItems[0].ID != runB {
		t.Fatalf("the node of %q carries %+v, want the run of that plan", planFolder, one.WorkItems)
	}
}

// TestAdoptKeepsItsItemsWhenTheFactsWouldNotFitTheNode is the node's byte
// budget, which the items are spent against first and the facts after: twelve
// runs whose briefs are whole task files fill the state node to just under
// MaxStateBytes, and a fact written over that bound is a document ValidateState
// refuses — the whole save with it, so no adopted item ever lands in the main
// node and every start repeats it. The items win over the fact, which is what a
// start that leaves runs behind must not pay for.
//
// Mutation: queue the fact without asking whether it fits and twelve runs of
// ordinary task-file size are adopted into nothing, for good.
func TestAdoptKeepsItsItemsWhenTheFactsWouldNotFitTheNode(t *testing.T) {
	at := o6Now()
	const runs = 12
	inFlight := make([]orchestrator.RunInFlight, 0, runs)
	for i := range runs {
		r := adoptRun(mainRun(i), "")
		r.Brief = strings.Repeat("b", 1007)
		inFlight = append(inFlight, r)
	}
	_, node, _, got := adopt(t, at, inFlight...)
	if got == 0 || got == runs {
		t.Fatalf("adoption wrote %d of %d items, and the node's bytes are meant to leave room for some of them and not for all", got, runs)
	}
	s := nodeState(t, node)
	if len(s.WorkItems) != got {
		t.Fatalf("the state node carries %d items and adoption answered %d", len(s.WorkItems), got)
	}
	if orchestrator.OverCap(s) {
		t.Fatalf("the state node is over the target of %d bytes, and adoption wrote a document this release refuses", orchestrator.MaxStateBytes)
	}
	if fact, ok := s.Facts["adopt_not_carried"]; ok {
		t.Fatalf("the node carries %q, and the fact that does not fit is what the items would be lost for", fact.Value)
	}
	if !s.WorkItems[0].Adopted || s.WorkItems[0].RunID == "" {
		t.Fatalf("the item reads %+v, want one adoption wrote", s.WorkItems[0])
	}
}

// code-change runs": a review job and a render are a job somebody else asked
// for and not work of the project's a dependent waits on, so the daemon writes
// no item for them — and names them nowhere, because they are not runs it left
// out. An evaluation is the same shape and is left the same way.
//
// Mutation: adopt every run the lister answers with and a review job becomes an
// item the daemon plans a spawn over and counts against the project's caps.
func TestAdoptSkipsReviewAndArtifactRuns(t *testing.T) {
	at := o6Now()
	_, node, _, got := adopt(t, at,
		modeRun(runA, recipe.ModeReview),
		modeRun(runB, recipe.ModeArtifact),
		modeRun(runC, recipe.ModeEvaluate),
		adoptRun(runD, ""),
	)
	if got != 1 {
		t.Fatalf("adoption wrote %d items for one code-change run among four", got)
	}
	s := nodeState(t, node)
	if len(s.WorkItems) != 1 || s.WorkItems[0].ID != runD {
		t.Fatalf("the node carries the items %+v, want the one code-change run", s.WorkItems)
	}
	if len(s.Facts) != 0 {
		t.Fatalf("the node carries %d facts, and a run the daemon does not adopt is not one it left out", len(s.Facts))
	}
}

// TestAdoptLeavesLadderRunsToTheirItems is the bound the ladder's own runs put
// on adoption: a rebase and the one stronger pass are runs of an item the node
// already carries, and the id of one is the ledger's and never the item's
// (Escalation.RunID, E12). Adoption takes neither of the two ways one can be in
// flight at start-up — the ledger that names it, and the recipe that says what
// it is — because a second item for work that already has one sits running for
// good: no runner reports on it, nothing folds it, and the rung that could
// merge it is refused for it (D2702).
//
// Mutation: filter on the mode alone and a rebase in flight becomes an item of
// its own, counted against the node's budget and the plan's.
func TestAdoptLeavesLadderRunsToTheirItems(t *testing.T) {
	now := o6Now()
	const attempt = "20261107-090000-abcd"

	// seed is a node whose one item the ladder has already moved: its own run is
	// the attempt's, and the run its move spent is the ledger's.
	seed := func(t *testing.T, ladderRunID string) *orchestrator.State {
		t.Helper()
		w, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
		w.RunID, w.Escalation.RunID = attempt, ladderRunID
		s := stack(t, now, []orchestrator.WorkItem{w}, its)
		return &s
	}

	t.Run("the ledger names the run", func(t *testing.T) {
		// The run is an ordinary code-change run by its recipe, and the recipe
		// is not what claims it: the block beside the item is, so a rung whose
		// run this release cannot recognise by name is covered too.
		_, node := seededNode(t, now, *seed(t, runA))
		lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, ""), adoptRun(runB, "")}}

		n, err := orchestrator.Adopt(context.Background(), lister, node)

		if err != nil || n != 1 {
			t.Fatalf("adoption wrote %d items and answered %v", n, err)
		}
		s := nodeState(t, node)
		if _, ok := s.Item(runA); ok {
			t.Fatalf("the state node carries an item for %q, a run the ledger of wi-1 already names", runA)
		}
		if w := itemAt(t, s, runB); !w.Adopted {
			t.Fatalf("the run nothing names reads %+v, and adoption still takes a code change in flight", w)
		}
		if len(s.Facts) != 0 {
			t.Fatalf("the state node carries the facts %v, and a ladder run is not one the daemon left out", s.Facts)
		}
	})

	t.Run("the recipe says what it is", func(t *testing.T) {
		// A ladder run a crash left with the launch and not the ledger: no
		// block names it, so the recipe is the whole of the evidence that it
		// belongs to an item that is already there.
		_, node := seededNode(t, now, *seed(t, ""))
		lister := &adoptLister{runs: []orchestrator.RunInFlight{
			ladderRun(runA, orchestrator.RecipeRebase),
			ladderRun(runB, orchestrator.RecipeEscalate),
			adoptRun(runC, ""),
		}}

		n, err := orchestrator.Adopt(context.Background(), lister, node)

		if err != nil || n != 1 {
			t.Fatalf("adoption wrote %d items and answered %v", n, err)
		}
		s := nodeState(t, node)
		for _, id := range []string{runA, runB} {
			if _, ok := s.Item(id); ok {
				t.Fatalf("the state node carries an item for %q, a ladder run of an item that already stands", id)
			}
		}
		if w := itemAt(t, s, runC); !w.Adopted {
			t.Fatalf("the run nothing names reads %+v, and adoption still takes a code change in flight", w)
		}
		if len(s.Facts) != 0 {
			t.Fatalf("the state node carries the facts %v, and a ladder run is not one the daemon left out", s.Facts)
		}
	})
}

// TestAdoptedItemNeverMergesUnapproved is D2702's merge rule: the rung that
// merges is the one rung the ladder may not enter for an item the daemon
// adopted at start-up that no approval of a person's covers — their run is
// somebody else's launch, and a merge is the one move nobody takes back, so it
// waits for the person D2721's fact sends to the item. The same item under a
// person's approval walks the rung, which is where the refusal ends.
//
// Mutation: drop the adopted guard and the ladder merges a run a session
// launched by hand, with the findings it left still unread.
func TestAdoptedItemNeverMergesUnapproved(t *testing.T) {
	now := o6Now()
	const seeded = "20261107-120000-c0de"

	t.Run("the rung that merges is skipped", func(t *testing.T) {
		w, its := spentOn(t, "wi-1", now, rungTwo(now))
		w.Adopted = true
		w.Approved, w.SpentApprovalAt = nil, nil
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, seeded)
		l.reads["wi-1"] = reading(rigor.POC, 4*time.Hour, headA)

		h.step()

		if got := rungs(itemAt(t, h.state(), "wi-1").Escalation); len(got) != 2 {
			t.Fatalf("the ledger reads %v, and the merge is a rung an adopted item no person approved never enters", got)
		}
		if len(l.launched) != 0 {
			t.Fatalf("the ladder launched %+v for an item nobody approved", l.launched)
		}
		if len(l.comments) != 0 {
			t.Fatalf("the ladder posted %q, and a move it does not make says nothing", l.comments)
		}
	})

	t.Run("a person's approval is what ends it", func(t *testing.T) {
		w, its := spentOn(t, "wi-1", now, rungTwo(now))
		w.Adopted = true
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, seeded)
		l.reads["wi-1"] = reading(rigor.POC, 4*time.Hour, headA)

		h.step()

		e := itemAt(t, h.state(), "wi-1").Escalation
		if got := rungs(e); len(got) != 3 || got[2] != string(escalation.R3) {
			t.Fatalf("the ledger reads %v, and an adopted item a person's approval covers walks the merge", got)
		}
		if len(l.comments) != 1 {
			t.Fatalf("the ladder posted %d comments for the one move", len(l.comments))
		}
	})
}

// TestAdoptRefusedWithoutLease: adoption writes under the lease and nowhere
// else. A daemon that does not hold it writes nothing at all, and a node built
// with no guard — a person's own patch (NodeOptions.Lease) — is refused before
// the runs are even read, because adoption is a daemon's step and the lease is
// what makes one.
//
// Mutation: adopt before the lease is checked and a daemon that lost the id
// writes its items over the one that holds it.
func TestAdoptRefusedWithoutLease(t *testing.T) {
	at := o6Now()
	t.Run("a lease the daemon does not hold", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		node := adoptNode(t, f, at, &stubLease{held: false})
		lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, "")}}

		n, err := orchestrator.Adopt(context.Background(), lister, node)

		if n != 0 || !errors.Is(err, orchestrator.ErrLeaseLost) {
			t.Fatalf("adoption wrote %d items and answered %v, want the lease's refusal", n, err)
		}
		if s := nodeState(t, node); len(s.WorkItems) != 0 || len(s.Intents) != 0 {
			t.Fatalf("the node carries %d items and %d intents under a lease somebody else holds", len(s.WorkItems), len(s.Intents))
		}
	})

	t.Run("a node with no guard at all", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		node := adoptNode(t, f, at, nil)
		lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, "")}}

		n, err := orchestrator.Adopt(context.Background(), lister, node)

		if n != 0 || !errors.Is(err, orchestrator.ErrLeaseLost) {
			t.Fatalf("adoption wrote %d items and answered %v for a node no daemon holds", n, err)
		}
		if lister.calls != 0 {
			t.Fatalf("the runs in flight were read %d times for a node no daemon holds", lister.calls)
		}
	})
}

// TestAdoptGoesToThePlanNode is F44: a run whose launch names a plan is adopted
// into the plan's own node and into no other, because the state node is every
// reader's shared document and a plan's runs are the plan's (D2735) — while the
// fact about the plan lands in the state node, which is where the session that
// imports it reads.
//
// Mutation: write every item into the state node and the daemon plans its spawn
// over work the plan's own node owns.
func TestAdoptGoesToThePlanNode(t *testing.T) {
	at := o6Now()
	f, node, _, got := adopt(t, at,
		adoptRun(runA, planFolder+"/3"),
		adoptRun(runB, planFolder+"/4"),
		adoptRun(runC, otherPlan+"/1"),
	)
	if got != 3 {
		t.Fatalf("adoption wrote %d items for three runs in flight", got)
	}
	if s := nodeState(t, node); len(s.WorkItems) != 0 || len(s.Intents) != 0 {
		t.Fatalf("the state node carries %d items and %d intents, and a run of a plan goes to the plan's node", len(s.WorkItems), len(s.Intents))
	}
	one := planNodeState(t, f, planFolder)
	if len(one.WorkItems) != 2 || len(one.Intents) != 2 {
		t.Fatalf("the node of %q carries %d items and %d intents, want the two runs of the plan", planFolder, len(one.WorkItems), len(one.Intents))
	}
	for i, id := range []string{runA, runB} {
		if w := one.WorkItems[i]; w.ID != id || w.PlanTask != planFolder+"/"+fmt.Sprint(3+i) {
			t.Fatalf("the plan's item %d reads %+v", i, w)
		}
	}
	if two := planNodeState(t, f, otherPlan); len(two.WorkItems) != 1 || two.WorkItems[0].ID != runC {
		t.Fatalf("the node of %q carries %+v, want the one run of that plan", otherPlan, two.WorkItems)
	}
	if s := nodeState(t, node); len(s.Facts) != 2 || s.Facts["plan_in_flight_"+planFolder].Value == "" || s.Facts["plan_in_flight_"+otherPlan].Value == "" {
		t.Fatalf("the state node carries the facts %v, want one per plan an adopted run named", s.Facts)
	}
}

// TestAdoptLeavesBacklogRunsToTheirItems is D2702 at the node a standing
// backlog item lives in (D2735, W1): a run the daemon launched from the standing
// backlog carries no plan task, so adoption's own shardOf sends it to the state
// node — while the item it is already under stands in the backlog's node and
// nowhere else. The claim is read off every node this call loads, and the
// backlog's is one of them, so the run is left to the item working it instead of
// being written a second time into the state node: a duplicate running item and
// a duplicate intent, folded and counted twice, on every start over a run the
// daemon itself launched.
//
// Mutation: read the nodes the runs name and the state node alone, and a run
// launched from the backlog is adopted into the state node beside its own item.
func TestAdoptLeavesBacklogRunsToTheirItems(t *testing.T) {
	at := o6Now()
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return at })

	// The item the daemon launched from the backlog before this start: running,
	// with its own run under it, and with no plan task — which is every item
	// `fugaro backlog add` and the issue ingest write (D2715).
	launched := orchestrator.WorkItem{
		ID: runA, Title: "a standing backlog item", Repo: "owner/repo",
		Recipe: "default", RigorTier: "small",
		Brief: "the brief the daemon launched it with",
		Phase: orchestrator.PhaseRunning, RunID: runA, Attempts: 1,
	}
	backlog := mustShardNode(t, rtdbClient(t, f), &stubLease{held: true}, orchestrator.ShardBacklog)
	_, etag, err := backlog.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty backlog node: %v", err)
	}
	seeded := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       daemonID,
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{launched},
		Intents: []orchestrator.Intent{{
			ID: orchestrator.IntentID(runA, 1), RunID: runA, SpecDigest: orchestrator.SpecDigest(launched),
			ApprovalAt: at, At: at,
		}},
	}
	if err := backlog.Save(ctx, seeded, etag); err != nil {
		t.Fatalf("seeding the backlog node with the item the daemon launched: %v", err)
	}

	node := adoptNode(t, f, at, &stubLease{held: true})
	lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, "")}}
	got, err := orchestrator.Adopt(ctx, lister, node)
	if err != nil {
		t.Fatalf("adoption ended as %v", err)
	}
	if got != 0 {
		t.Fatalf("adoption wrote %d item(s) for a run the backlog's node already carries as its item", got)
	}
	after := nodeState(t, node)
	if len(after.WorkItems) != 0 || len(after.Intents) != 0 || len(after.Facts) != 0 {
		t.Fatalf("the state node carries %d items, %d intents and %d facts, and a run already an item of the backlog's node is nothing adoption writes into it",
			len(after.WorkItems), len(after.Intents), len(after.Facts))
	}
	if kept := nodeState(t, backlog); len(kept.WorkItems) != 1 || kept.WorkItems[0].RunID != runA {
		t.Fatalf("the backlog node carries %+v, and adoption writes nothing into the node whose item is working the run", kept.WorkItems)
	}
}

// TestAdoptBoundedInMainNode is the bound on the shared node (D2702): twenty
// runs go into the state node and the rest are named in one fact, because a
// first start over a checkout a person drove by hand must not fill the node
// every reader shares. A start over a node another start already adopted into
// takes no more, so the bound is on the node and not on this process.
//
// Mutation: count the adopted runs from this call instead of the node and every
// restart takes twenty more.
func TestAdoptBoundedInMainNode(t *testing.T) {
	at := o6Now()
	runs := make([]orchestrator.RunInFlight, 0, 25)
	for i := range 25 {
		runs = append(runs, adoptRun(mainRun(i), ""))
	}
	_, node, lister, got := adopt(t, at, runs...)
	if got != orchestrator.MaxAdoptedInMain {
		t.Fatalf("adoption wrote %d items for 25 runs in flight, want the node's own bound of %d", got, orchestrator.MaxAdoptedInMain)
	}
	s := nodeState(t, node)
	if len(s.WorkItems) != orchestrator.MaxAdoptedInMain {
		t.Fatalf("the state node carries %d items", len(s.WorkItems))
	}
	for i := range orchestrator.MaxAdoptedInMain {
		if _, ok := s.Item(mainRun(i)); !ok {
			t.Fatalf("the state node carries no item for the run %q", mainRun(i))
		}
	}
	fact, ok := s.Facts["adopt_not_carried"]
	if !ok {
		t.Fatalf("the node carries %d facts and none naming the runs it left out", len(s.Facts))
	}
	for i := orchestrator.MaxAdoptedInMain; i < 25; i++ {
		if _, ok := s.Item(mainRun(i)); ok {
			t.Fatalf("the state node carries an item for %q, which is past the bound", mainRun(i))
		}
		if !strings.Contains(fact.Value, mainRun(i)) {
			t.Fatalf("the fact reads %q and does not name the run %q it left out", fact.Value, mainRun(i))
		}
	}

	// The bound is the node's: a start that finds twenty adopted already takes
	// no more, and says so about the run it left.
	lister.runs = []orchestrator.RunInFlight{adoptRun(extraRun, "")}
	again, err := orchestrator.Adopt(context.Background(), lister, node)
	if err != nil || again != 0 {
		t.Fatalf("the second start wrote %d items and answered %v", again, err)
	}
	s = nodeState(t, node)
	if len(s.WorkItems) != orchestrator.MaxAdoptedInMain {
		t.Fatalf("the state node carries %d items after a second start", len(s.WorkItems))
	}
	if _, ok := s.Item(extraRun); ok {
		t.Fatalf("the state node carries an item for %q, and the node was already at its adopted bound", extraRun)
	}
	if !strings.Contains(s.Facts["adopt_not_carried"].Value, extraRun) {
		t.Fatalf("the fact reads %q and does not name %q", s.Facts["adopt_not_carried"].Value, extraRun)
	}
}

// TestAdoptedRunIsFoldedAndCounted is D2744: the intent adoption writes is what
// makes an adopted run one of the loop's. The caps count it from the moment it
// is adopted, so the daemon does not start work over a run that is already
// spending the project's money, and its own report moves the item it is under
// — a run with no intent is neither folded nor counted (F54).
//
// Mutation: write the item without the intent and the run reports into nothing,
// with the cap it spends never coming back.
func TestAdoptedRunIsFoldedAndCounted(t *testing.T) {
	now := o6Now()
	seed := stateWithAQueuedItem(t, now)
	seed.WorkItems = nil
	h := newHarness(t, seed, now)
	node := adoptNode(t, h.db, now, &stubLease{held: true})
	lister := &adoptLister{runs: []orchestrator.RunInFlight{adoptRun(runA, "")}}

	n, err := orchestrator.Adopt(context.Background(), lister, node)
	if err != nil || n != 1 {
		t.Fatalf("adoption wrote %d items and answered %v", n, err)
	}
	if got := orchestrator.CountsOf(h.state(), now).Running; got != 1 {
		t.Fatalf("the caps count %d runs in flight after adoption, and the adopted run is one", got)
	}

	h.reader.reports = []orchestrator.RunReport{{RunID: runA, Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed}}
	h.step()

	if w := itemAt(t, h.state(), runA); w.Phase != orchestrator.PhaseFailed {
		t.Fatalf("the item is %q after the run it is reported failed", w.Phase)
	}
	if got := orchestrator.CountsOf(h.state(), now).Running; got != 0 {
		t.Fatalf("the caps count %d runs in flight after the run reported", got)
	}
}

// rungTwo is the ledger of an item whose one stronger pass is spent: it is at
// rung 2, with the two transitions that brought it there, so the decision the
// next cycle makes about it is the rung past it — the one that merges.
func rungTwo(now time.Time) *orchestrator.Escalation {
	e := ledger(escalation.R2, 2,
		step("", escalation.R0, escalation.ReasonRebase, now.Add(-2*time.Hour), headA),
		step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(-time.Hour), headA))
	return &e
}

// mainRun is one of the many runs a first start finds in flight: a run id of
// the shape a run has, in the order of i.
func mainRun(i int) string { return fmt.Sprintf("20261107-1200%02d-%04x", i, i) }

// extraRun is a run in flight of a later start: later than every mainRun, so a
// node at its bound leaves it where mainRun's own over-cap runs were left.
const extraRun = "20261107-120100-0abc"
