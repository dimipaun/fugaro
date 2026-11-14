// The ladder's executor: what one cycle walks an item up, rungs 0 to 2 (E10a).
//
// The decision is E1's and is pure. Everything a decision cannot be is here:
// the facts gathered out of the daemon's own readings, the launch each rung
// asks for, and the records a move leaves — the item's ledger, which is what
// makes a rung spent, and the audit line and the pull-request comment that say
// so. A rung past rung 2 is not walked here: the move out of it is E10b's, and
// what this file leaves for it is the LadderBeyond hook.
//
// Nothing here reads a clock, a file or a host of its own. A cycle's stamps are
// the database server's (D1230) and everything else is a seam (LadderSeams),
// which is what lets a test drive a whole ladder without a run store, a
// provider or a log.

package orchestrator

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The recipes the ladder launches beside the item's own (E5): rung 0's rebase
// and rung 2's one stronger single pass. Both are the catalog's names, and a
// rung names a recipe and never a model — the grade travels beside it, in the
// task the launch builds.
const (
	RecipeRebase   = "rebase"
	RecipeEscalate = "escalate"
)

// LadderSeams is what one cycle walks the ladder through (E10a): the table the
// decision reads, the readings the facts are gathered from, the launcher a rung
// starts its run through, the comment a move posts on the pull request, and the
// audit log every move leaves a line in. All four or none — a Tick built with
// none escalates nothing, which is where a daemon with no run store, no
// provider and no log stands, and the direction a seam that was never wired
// fails in.
//
// Beyond is not part of that: it is where rungs 3 and 4 attach (E10b), and a
// ladder with no hook still walks the two rungs it can.
type LadderSeams struct {
	// Thresholds is the ladder's own table, already merged by the layers that
	// may set one (E3, E2): the executor reads it and never a config of its
	// own.
	Thresholds escalation.Thresholds
	Read       LadderReader
	Launch     LadderLauncher
	Comment    LadderCommenter
	Log        LadderLog
	// Beyond is what the move into rung 3 or rung 4 does (E10b: the judge, the
	// merge under D1245's gate, the split). Nil is this release: the move is
	// recorded and the item is left at the rung.
	Beyond LadderBeyond
}

// Set reports whether every seam a rung can be walked through is there.
func (s LadderSeams) Set() bool {
	return s.Read != nil && s.Launch != nil && s.Comment != nil && s.Log != nil
}

// LadderReading is what the ladder is told about one item from outside this
// package: the two facts only the runs' own records carry (how long the item
// has been going and what it has cost), the two only a host carries (the head
// and whether the last review left anything blocking), the churn E9 compared
// across the item's review rounds, the rigor mode its layers resolved to, and
// whether a run the ladder started for it has not reported yet.
//
// Read false is a reading that could not be made, and it is not the zero
// reading of an item with nothing to say: the decision refuses on facts it
// cannot read (G5), so an unread item is a person's and never rung 1's.
type LadderReading struct {
	Read               bool
	Mode               rigor.Mode
	Elapsed            time.Duration
	CostUSD            float64
	Churn              escalation.Kind
	HeadSHA            string
	LastReviewBlocking bool
	// Running is the run id of a ladder run of this item that has started and
	// not reported, or "". It is the half of the idempotence a ledger cannot
	// carry: a cycle that died between starting a run and recording the rung
	// leaves the item where it was, and this is what stops the next cycle from
	// starting the same rung a second time (G4).
	Running string
}

// LadderReader gathers the ladder's input for one item, each reading from where
// it lives. An error is a read that did not happen and ends the cycle; an item
// whose runs cannot be read at all is a reading with Read false, which the
// executor turns into a person's question rather than a rung.
type LadderReader interface {
	LadderFacts(ctx context.Context, item WorkItem) (LadderReading, error)
}

// LadderRequest is one launch a rung asks for: the item, the run id the cycle
// has already minted for it, the recipe and the grade the run is built from,
// and the head the rung was decided at.
//
// Rung 1 is the one rung whose request carries an Action: the run it starts is
// a run of the item's own recipe, which is what E11's gate plans and what the
// state records as an attempt under the item's approval. Every other rung
// starts a run of another recipe, which is the ledger's and not an attempt's,
// and the grade is never part of the spec — a launch that changed the spec
// would be a launch of work nobody approved.
type LadderRequest struct {
	Item    WorkItem
	RunID   string
	Recipe  string
	Rung    escalation.Rung
	Grade   escalation.Grade
	HeadSHA string
	Action  Action
}

// LadderLauncher starts one ladder run: the intent-first launch D1231 states,
// under the run id the request carries and no other. An error is a run that did
// not start, which leaves the item where it was for the next cycle to decide
// again.
type LadderLauncher interface {
	LaunchLadder(ctx context.Context, req LadderRequest) error
}

// LadderCommenter posts one comment on the pull request of the item: the
// transition the ladder made, ending in the marker E8 reads back. A host that
// will not take it is a channel that failed and not a record (the triage's own
// rule): the executor reports it and the cycle goes on.
type LadderCommenter interface {
	LadderComment(ctx context.Context, item WorkItem, body string) error
}

// LadderLog is O3's audit log as the ladder writes it, under the id the entry
// derives from the item and the rung: a retry of the same move writes the key
// it wrote before rather than a second line, which is the whole of the log's
// idempotence (G4).
type LadderLog interface {
	Append(ctx context.Context, entry Entry) error
}

// LadderBeyond is the extension point rungs 3 and 4 attach through (E10b). The
// executor calls it in place of what it does for a rung whose move this release
// carries out — the transition and the line, with the item left where it is —
// and what a rung needs beyond that (the advisory judge, the merge under
// D1245's gate, the split) is the hook's own business and its own seams.
//
// It is a function and not an interface because both sides of it are this
// package's: E10b adds its files beside this one and hands its own function to
// LadderSeams. A hook that returns an error ends the item's turn in the cycle
// the way an error of the executor's own would.
type LadderBeyond func(ctx context.Context, item WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) error

// escalate is one cycle's turn at the ladder: every item on the node, walked as
// far as the facts take it.
//
// It is a cycle's own write or nothing at all: a node this cycle no longer
// holds is not a state to file questions from, and the three questions Plan
// asks before it launches anything are asked here too — a ladder run is a run,
// so a cycle that spends nothing while the budget switch holds starts nothing
// while it holds either.
func (c *cycle) escalate(ctx context.Context, view RunView) error {
	if !c.tick.Ladder.Set() || !c.fresh {
		return nil
	}
	if c.tick.Caps.KillSwitch || OverCap(c.state) || BreakerOpen(c.state) {
		return nil
	}
	blocked := blocksOthers(c.state)
	for _, w := range c.state.WorkItems {
		if err := c.ladderItem(ctx, view, w.ID, blocked); err != nil {
			return err
		}
	}
	return nil
}

// ladderItem walks one item one step. The item is looked up again rather than
// carried in from the caller, because every move this cycle makes is a write
// and a write replaces the node the item came from.
func (c *cycle) ladderItem(ctx context.Context, view RunView, id string, blocked map[string]bool) error {
	w, ok := c.state.Item(id)
	if !ok || w.Phase.Done() {
		return nil
	}
	if _, open := openIntentOf(c.state, w.ID); open {
		// An attempt of the item's has not reported. What the loop does with it
		// is the loop's — its recovery, its retry, its abandonment — and the
		// ladder starts nothing beside it: a rebase under a run that is going
		// moves the head that run is working on.
		return nil
	}
	if ladderTransitions(w) >= MaxEscalationTransitions {
		// The bound the validator puts on a ledger, which the ladder itself
		// cannot reach (it spends at most six). A ledger this long was written
		// by something else, and a cycle that kept deciding rungs for it would
		// fail on the write every time.
		return c.askLadder(ctx, w, ladderFullQuestion(w))
	}
	read, err := c.tick.Ladder.Read.LadderFacts(ctx, w)
	if err != nil {
		return err
	}
	if !read.Read {
		return c.askLadder(ctx, w, ladderUnreadableQuestion(w))
	}
	if read.Running != "" {
		// A ladder run of the item's has started and not reported: the rung it
		// carries is the rung the item is spending, and what it answers with is
		// the next cycle's — including the cycle that follows a daemon whose
		// crash left the launch and not the ledger behind (G4).
		return nil
	}
	step, reason := escalation.Decide(ladderFacts(c.state, w, read, blocked[w.ID]), c.tick.Ladder.Thresholds)
	if step.Refuse {
		return c.askLadder(ctx, w, ladderRefusedQuestion(w))
	}
	if step.Rung != escalation.R0 && step.Rung.Rank() <= ladderReached(w).Rank() {
		// A rung the item is at or past is a rung it has spent (G4). Rung 0 is
		// the exception the decision already keeps: it answers R0 only for a
		// head whose rebase is still the item's to take, which is a fact held
		// in the ledger's own transitions and not in its rung.
		return nil
	}
	return c.ladderAct(ctx, view, w, step, reason, read)
}

// ladderAct carries out the one step the decision named, and false where a rung
// starts nothing here.
func (c *cycle) ladderAct(ctx context.Context, view RunView, w WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) error {
	switch step.Rung {
	case escalation.R0, escalation.R2:
		req, ok := c.ladderOuter(view, w, step, read)
		if !ok {
			// A run of another recipe is the item's approval's to cover, and
			// the ladder has no approval of its own (G6, G15). An item no
			// approval covers is left where it is: a rung entered by a run
			// nobody approved is the standing approval D2042 refuses.
			return nil
		}
		runID, err := c.ladderStart(ctx, req)
		if err != nil {
			return err
		}
		head := ""
		if step.Rung == escalation.R0 {
			head = read.HeadSHA
		}
		if err := c.ladderRecord(ctx, w, step, reason, head, true); err != nil {
			return err
		}
		return c.ladderSay(ctx, w, step, reason, runID)
	case escalation.R1:
		return c.ladderResume(ctx, view, w, step, read)
	default:
		return c.ladderBeyond(ctx, w, step, reason, read)
	}
}

// ladderOuter is the launch rungs 0 and 2 ask for, and false where the ladder
// may not start one: a run of another recipe is covered by the item's own
// approval (D2042, G15) — the standing one, or the one a retry spent — and
// bounded by the ladder's own count and by the cost ceiling a person set.
func (c *cycle) ladderOuter(view RunView, w WorkItem, step escalation.Step, read LadderReading) (LadderRequest, bool) {
	at, ok := ladderCoveredAt(w)
	if !ok {
		return LadderRequest{}, false
	}
	covered, ok := ladderCoveredDigest(c.state, w, at)
	if !ok || SpecDigest(w) != covered {
		// The approval covers the spec it was given over, and an item edited
		// since is another spec: what the ladder escalates is the work a person
		// read, or nothing at all.
		return LadderRequest{}, false
	}
	if LadderRuns(c.state, w) >= MaxOuterAttempts || view.Spend[w.ID].AtCeiling() {
		return LadderRequest{}, false
	}
	recipe := RecipeRebase
	if step.Rung == escalation.R2 {
		recipe = RecipeEscalate
	}
	return LadderRequest{Item: w, Recipe: recipe, Rung: step.Rung, Grade: step.Grade, HeadSHA: read.HeadSHA}, true
}

// ladderResume is rung 1: today's behaviour, which is a run of the item's own
// recipe past the attempts its approval covers. What may start one is E11's
// gate — the approval that covers it, the loop that has nothing left to do with
// it, the ladder's count and the cost ceiling — and the item's rung is not
// moved here: escalation.Decide answers the floor with no reason, and a
// transition records why an item entered a rung.
//
// The run is recorded as an attempt under the approval like any other, which
// is what makes a resume the loop's own answer and not a second ladder beside
// it, and that write is the daemon's: what this asks for is the launch.
func (c *cycle) ladderResume(ctx context.Context, view RunView, w WorkItem, step escalation.Step, read LadderReading) error {
	action, ok := LadderLaunch(c.state, w, view, c.tick.Caps)
	if !ok {
		return nil
	}
	runID, err := c.ladderStart(ctx, LadderRequest{Item: w, Recipe: w.Recipe, Rung: step.Rung, HeadSHA: read.HeadSHA, Action: action})
	if err != nil {
		return err
	}
	return c.ladderSay(ctx, w, step, "", runID)
}

// ladderBeyond hands a rung past rung 1's to the hook the rungs past this task
// attach through, and records the move itself where there is no hook: an item
// that has entered rung 3 or rung 4 is left where it is, which is the stop
// D2030 and D2037 state — the ladder never walks back off either, whatever a
// fact recomputed each cycle says.
func (c *cycle) ladderBeyond(ctx context.Context, w WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) error {
	if c.tick.Ladder.Beyond != nil {
		return c.tick.Ladder.Beyond(ctx, w, step, reason, read)
	}
	runID := w.RunID
	if err := c.ladderRecord(ctx, w, step, reason, "", false); err != nil {
		return err
	}
	return c.ladderSay(ctx, w, step, reason, runID)
}

// ladderStart asks the daemon for one run, under the id this cycle mints. The
// id is minted here and committed nowhere before the launch: a ladder run of
// another recipe is the ledger's and not an attempt's, so what records it is
// the transition the caller writes once the start has come back.
func (c *cycle) ladderStart(ctx context.Context, req LadderRequest) (string, error) {
	runID, err := c.tick.Mint(c.now)
	if err != nil {
		return "", err
	}
	req.RunID = runID
	if err := c.tick.Ladder.Launch.LaunchLadder(ctx, req); err != nil {
		return "", err
	}
	return runID, nil
}

// ladderRecord moves the item's ledger to the rung, appending the transition
// that says why. It is a daemon write of a daemon-only field (G12) and the
// whole of what the ladder changes on the node: the rung, the count of the
// runs it has spent and the transition, and never a phase, an approval or an
// intent.
func (c *cycle) ladderRecord(ctx context.Context, w WorkItem, step escalation.Step, reason escalation.Reason, head string, launched bool) error {
	held := Escalation{}
	if w.Escalation != nil {
		held = *w.Escalation
	}
	next := Escalation{
		Rung:          step.Rung,
		EnteredAt:     c.now,
		OuterAttempts: held.OuterAttempts,
		Transitions: append(append([]Transition{}, held.Transitions...),
			Transition{From: held.Rung, To: step.Rung, Reason: reason, At: c.now, HeadSHA: head}),
	}
	if launched {
		next.OuterAttempts++
	}
	op := Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: w.ID, Escalation: &next}}
	st, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: []Op{op}}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	return c.commit(ctx, st)
}

// ladderSay writes the line the log keeps and posts the comment the pull
// request reads, once the move has landed. A comment a host will not take is
// reported on the cycle and never fails it: the comment is a channel and the
// ledger is the record.
func (c *cycle) ladderSay(ctx context.Context, w WorkItem, step escalation.Step, reason escalation.Reason, runID string) error {
	move := ladderMove(step, reason, runID)
	entry := Entry{Subject: w.ID, Transition: ladderTransition(w, step), Text: "escalation: " + move}
	if err := c.tick.Ladder.Log.Append(ctx, entry); err != nil {
		return err
	}
	if err := c.tick.Ladder.Comment.LadderComment(ctx, w, ladderComment(step, move, runID)); err != nil {
		c.rep.LadderErr = errors.Join(c.rep.LadderErr, err)
	}
	c.rep.Escalated++
	return nil
}

// askLadder raises the one question a ladder that cannot decide something owes
// a person, under the item's own id, and says so once: a decision whose id the
// node already carries is the question already asked, and a cycle that re-runs
// after its write landed asks nothing more.
func (c *cycle) askLadder(ctx context.Context, w WorkItem, question string) error {
	id := w.ID + "-escalation"
	if _, ok := c.state.Decision(id); ok {
		return nil
	}
	op := Op{Op: OpDecisionAdd, Decision: &DecisionOp{ID: id, Question: question, WaitingOn: OnPerson}}
	next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: []Op{op}}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.say(ctx, Notification{Kind: NotifyDecisionNeeded, Subject: id, Text: question})
	return nil
}

// ladderFacts is the decision's input, built from the reading and the node. The
// two counts D2042 keeps apart are the whole of it: the attempts are the item's
// own recipe's runs, which the intents hold, and the runs the ladder launched
// are its ledger's, which is why neither is read off the other.
func ladderFacts(s State, w WorkItem, read LadderReading, blocks bool) escalation.Facts {
	reached, heads, r2 := ladderLedger(w)
	return escalation.Facts{
		Mode:               read.Mode,
		Elapsed:            read.Elapsed,
		Attempts:           ladderAttempts(s, w),
		CostUSD:            read.CostUSD,
		BlocksOthers:       blocks,
		Churn:              read.Churn,
		HeadSHA:            read.HeadSHA,
		LastReviewBlocking: read.LastReviewBlocking,
		Reached:            reached,
		RebasedHeads:       heads,
		R2Runs:             r2,
	}
}

// ladderAttempts is how many normal runs of the item's own spec its intents
// hold. A run of another recipe — a rebase, the one stronger pass — is not an
// attempt and is recorded in the ledger instead (E11), so what is here is the
// item's own recipe and nothing else.
func ladderAttempts(s State, w WorkItem) int {
	digest := SpecDigest(w)
	n := 0
	for _, it := range s.Intents {
		if IntentItem(it.ID) == w.ID && it.SpecDigest == digest {
			n++
		}
	}
	return n
}

// ladderLedger is the ledger as the decision reads it: the rung the item is at,
// every head a rung 0 run has been spent by, and how many rung 2 runs it has
// had.
func ladderLedger(w WorkItem) (escalation.Rung, []string, int) {
	if w.Escalation == nil {
		return "", nil, 0
	}
	var heads []string
	r2 := 0
	for _, tr := range w.Escalation.Transitions {
		if tr.To == escalation.R0 && tr.HeadSHA != "" {
			heads = append(heads, tr.HeadSHA)
		}
		if tr.To == escalation.R2 {
			r2++
		}
	}
	return w.Escalation.Rung, heads, r2
}

// ladderReached is the rung the item is at.
func ladderReached(w WorkItem) escalation.Rung {
	if w.Escalation == nil {
		return ""
	}
	return w.Escalation.Rung
}

// ladderTransitions is how many transitions the item's ledger holds.
func ladderTransitions(w WorkItem) int {
	if w.Escalation == nil {
		return 0
	}
	return len(w.Escalation.Transitions)
}

// blocksOthers is the critical-path flag G7 states, for every item on the node
// in one pass: an item is on the critical path when another item a person's
// approval stands for, and that has not reached a terminal phase, names it in
// DependsOn.
//
// It is a scan of the node and never a walk of the graph, because DependsOn is
// a graph a session writes and a cycle in it is a graph somebody wrote: a walk
// would be a loop with nothing to stop it, and the answer is the same whether
// the graph is a tree or a cycle. An item no approval stands for names nothing
// either — the flag halves times and raises grades, and a person must have
// approved the work that waits (F15).
func blocksOthers(s State) map[string]bool {
	out := map[string]bool{}
	for _, w := range s.WorkItems {
		if w.Phase.Done() || (w.Approved == nil && w.SpentApprovalAt == nil) {
			continue
		}
		for _, dep := range w.DependsOn {
			out[dep] = true
		}
	}
	return out
}

// ladderMove is one move as one line of closed codes: the rung, the reason the
// decision gave for it, the run that carries it and the grade it runs at.
// Nothing here is a path, a finding, a prompt or a model (G8), which is what
// lets the same line go into the log and onto the pull request.
func ladderMove(step escalation.Step, reason escalation.Reason, runID string) string {
	line := "rung " + string(step.Rung)
	if reason != "" {
		line += " (" + string(reason) + ")"
	}
	if runID != "" {
		line += ", run " + runID
	}
	if step.Grade != "" {
		line += " at grade " + string(step.Grade)
	}
	return line
}

// ladderComment is what the pull request is told about one move, ending in the
// marker E8 reads back. The marker names the run whose comment this is — the
// one the rung started, or the item's own run where the rung starts none — and
// a comment with no run to name is posted without one, because a marker around
// no run id is one no reader can place.
func ladderComment(step escalation.Step, move, runID string) string {
	body := "**Fugaro:** the ladder moved this pull request to " + move + "."
	if runID == "" {
		return body
	}
	return body + "\n\n" + gitprov.TransitionMarker(runID, string(step.Rung))
}

// ladderTransition is the audit id's transition half for one move: the rung,
// and for rung 0 the ordinal of the rebase, so the line says which of the two
// rebases an item may spend it is about. A repeated cycle writes the key it
// wrote before rather than a second line, which is the whole of what the log
// promises (G4).
func ladderTransition(w WorkItem, step escalation.Step) string {
	name := "ladder-" + strings.ToLower(string(step.Rung))
	if step.Rung == escalation.R0 {
		_, heads, _ := ladderLedger(w)
		return name + "-" + strconv.Itoa(len(heads)+1)
	}
	return name
}

// ladderUnreadableQuestion is what a person is asked about an item whose runs
// the daemon cannot read: every fact the ladder decides on comes from those
// records, and a rung decided on a reading that was not made is the guess G5
// refuses.
func ladderUnreadableQuestion(w WorkItem) string {
	return "The escalation ladder cannot read the runs behind " + w.ID + ": its age, its cost, its head and its review churn come from records that could not be read, so no rung is decided for it. Read the item and decide what happens to it."
}

// ladderRefusedQuestion is what a person is asked about an item the ladder will
// not decide a rung for: the thresholds or the grade that decide it are not
// readable, and nothing here has a default — an unset grade, a mode with no row
// and a threshold nobody set are each a model or a clock nobody chose.
func ladderRefusedQuestion(w WorkItem) string {
	return "The escalation ladder will not decide a rung for " + w.ID + ": the thresholds that decide it, or the grade its rung 2 would run at (escalation.r2_coder), are not readable. Set them, or decide the item yourself."
}

// ladderFullQuestion is what a person is asked about an item whose ledger holds
// more transitions than a ledger may: the ladder spends at most six, so this
// one was written by something else, and every further rung would be a write
// the state refuses.
func ladderFullQuestion(w WorkItem) string {
	return "The escalation ledger of " + w.ID + " is full: it carries more transitions than the state accepts, so no rung can be recorded for it. Read the item and decide what happens to it."
}
