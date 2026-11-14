// The reconcile tick: one cycle of the daemon's loop (O7).
//
// A tick is level-triggered (D1229). It reads the node, reads what the runs
// behind the node's intents reported, folds the finished ones into the items,
// plans (O6), carries the plan out (O6's starter, intent first), triages the
// runs the ladder's threshold has passed (O14b) and commits. There is no
// cursor and no replay: a tick derives its whole answer from the node as it
// now stands, so a tick that ran twice over one node is the same as one that
// ran once (TestTickIsIdempotentOnRepeat), and a daemon that crashed mid-cycle
// resumes by running the next tick.
//
// Nothing here calls a model, a cloud or a person (D1221): the state is the
// store's (O3), the runs are the reader's, a launch is the launcher's, the
// clock is the database server's (D1230), and the one thing a tick says to
// somebody goes through a Notifier. That is what makes the loop a unit test
// rather than a cloud experiment.

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// Tick is one daemon's cycle and everything it acts through. It is a value
// rather than a function because the loop the plan names — `Tick(ctx, Store,
// RunViewReader, Launcher, Notifier)` — has to carry four things a cycle reads
// and must not reach for: the caps and the kill switch a plan is bounded by,
// the mint that names a run, and the server clock every stamp comes from. O8's
// loop holds one of these, wakes it, and re-arms the breaker it was given.
//
// Nothing here sleeps, waits, retries or reads a clock of its own: one call to
// Step is one cycle, and the waiting between cycles is the loop's.
type Tick struct {
	// Store is the state node the cycle reads and writes.
	Store Store
	// Runs reads what the runs behind the intents reported. It is asked about
	// the run ids the state recorded, and about no others.
	Runs RunViewReader
	// Launcher starts one run for one attempt, under the run id the state has
	// already recorded.
	Launcher Launcher
	// Mint mints that run id. It is the daemon's (internal/task in O8): this
	// package knows no run ids of its own.
	Mint MintFunc
	// Caps are the guardrails the cycle plans under. KillSwitch comes from
	// whoever can read the budget (O8) and is on whenever it could not be
	// read; a Tick built with the zero Caps starts nothing, which is the
	// direction a guard that was never set fails in.
	Caps Caps
	// Breaker counts the cycles in a row that ended with nothing done, and the
	// cycle that reaches the threshold asks a person and leaves the question
	// on the node for the loop to re-arm it over (breaker.go). A nil one
	// counts nothing and asks nothing.
	Breaker *Breaker
	// Notifier is where a finished run and a question for a person go, or nil
	// for a daemon that tells nobody.
	Notifier Notifier
	// Triage is what this cycle acts on the long-run rule through (O14b): the
	// readings, the two actions an outcome can ask for that are not a state
	// write, and the audit log every outcome leaves a line in. Left unset the
	// cycle triages nothing at all, which is the direction a daemon that was
	// given no host, no run storage and no log fails in.
	Triage TriageSeams
	// Ladder is what this cycle walks the escalation ladder through (E10a):
	// the readings the decision's facts are gathered from, the launcher a rung
	// starts its run through, the comment a move posts and the log every move
	// leaves a line in. Left unset the cycle escalates nothing at all, which is
	// the direction a daemon that was given no run store, no provider and no
	// log fails in.
	Ladder LadderSeams
	// Now is the database server's clock (rtdb.ServerNow). Every stamp a cycle
	// writes comes from it, and a cycle that cannot read it refuses rather
	// than stamping anything with this machine's clock (D1230).
	Now ServerNow
}

// Report is what one cycle did, for the loop's log and for the tests.
type Report struct {
	// Outcome is how the cycle ended, in the breaker's vocabulary.
	Outcome CycleOutcome
	// Folded is how many finished runs the cycle wrote into the items.
	Folded int
	// Carried is how many of the plan's actions the starter carried out — a
	// launch, a retry, a re-issue or an abandonment.
	Carried int
	// Triaged is how many runs the cycle judged past the threshold and acted
	// on. A run the rule was read about but did not have an outcome for — one
	// under the threshold — is not counted, and neither is one whose line the
	// log already carries.
	Triaged int
	// Escalated is how many moves the cycle's ladder made: a rung a run was
	// started for, and a rung recorded for the hook the rungs past rung 2
	// attach through. The floor, rung 1, is not a move — the item's own loop
	// is what resumes it — and neither is an item the ladder asked a person
	// about.
	Escalated int
	// NotifyErr is a notification a sink would not take. It never fails the
	// cycle: what a cycle must not lose is the write behind the notification.
	NotifyErr error
	// LadderErr is a ladder comment a host would not take, which never fails
	// the cycle for the same reason: the comment is a channel and the ledger
	// is the record.
	LadderErr error
}

// Step runs one cycle.
//
// An error is a cycle that did not finish — a node it could not read, a write
// that was refused, a launcher that would not start — and what it leaves
// behind is what it committed before that. The intent of a launch that did not
// happen stands, which is the record the next tick re-issues (D1231); a node
// another writer moved is not written over at all. An error is a failed cycle
// whatever the report's outcome says.
func (t *Tick) Step(ctx context.Context) (Report, error) {
	if err := t.ready(); err != nil {
		return Report{Outcome: CycleFailed}, err
	}
	c := &cycle{tick: t}
	err := c.run(ctx)
	if err != nil {
		c.rep.Outcome = CycleFailed
	}
	if t.Breaker != nil && t.Breaker.Observe(c.rep.Outcome) {
		if rerr := c.ask(ctx); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	return c.rep, err
}

func (t *Tick) ready() error {
	switch {
	case t.Store == nil:
		return errors.New("orchestrator: a tick needs the store its state node is read and written through")
	case t.Runs == nil:
		return errors.New("orchestrator: a tick needs a run view, and one it cannot read is not an empty one")
	case t.Mint == nil:
		return errors.New("orchestrator: a tick needs a mint, and a run id this package invents is a run nobody recorded")
	case t.Now == nil:
		return errors.New("orchestrator: a tick needs the database server's clock, and a stamp from this machine is not one (D1230)")
	}
	return nil
}

// cycle is one tick's node: the state as this cycle last read or wrote it, the
// ETag the next write has to carry, whether that node is still this cycle's to
// write, and the report being built.
type cycle struct {
	tick  *Tick
	rep   Report
	state State
	etag  ETag
	now   time.Time
	fresh bool
}

func (c *cycle) run(ctx context.Context) error {
	s, etag, err := c.tick.Store.Load(ctx)
	if err != nil {
		return err
	}
	now, ok := c.tick.Now()
	if !ok {
		// A cycle that cannot read the server's clock writes nothing at all
		// (D1230): the intents' stamps are what the caps count over, and a
		// reading from this machine is a count nothing derived can be trusted
		// with.
		return ErrNoServerTime
	}
	c.state, c.etag, c.now, c.fresh = s, etag, stamp(now), true

	reports, err := c.tick.Runs.Runs(ctx, openRunIDs(s))
	if err != nil {
		return err
	}
	if err := c.fold(ctx, reports); err != nil {
		return err
	}
	view := viewOf(reports)
	if err := c.carry(ctx, view); err != nil {
		return err
	}
	if err := c.escalate(ctx); err != nil {
		return err
	}
	if err := c.triage(ctx); err != nil {
		return err
	}
	c.rep.Outcome = c.outcome()
	return nil
}

// fold writes what the finished runs mean into the items, and says so once the
// write has landed.
func (c *cycle) fold(ctx context.Context, reports []RunReport) error {
	ops, notes, err := foldOps(c.state, reports)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.rep.Folded = len(ops)
	for _, n := range notes {
		c.say(ctx, n)
	}
	return nil
}

// carry runs the plan: each action in the order the plan decided them, each
// one through the starter, whose commit is this cycle's write. The first action
// that cannot be carried out ends the cycle — what it left is what the starter
// committed before it, and the next tick re-derives the rest.
func (c *cycle) carry(ctx context.Context, view RunView) error {
	starter := Starter{Launcher: c.tick.Launcher, Commit: c.commit, Mint: c.tick.Mint}
	for _, a := range Plan(c.state, view, c.tick.Caps, c.now) {
		next, err := starter.Start(ctx, c.state, a, c.now)
		if err != nil {
			return err
		}
		c.state = next
		c.rep.Carried++
	}
	return nil
}

// outcome is how the cycle reads itself for the breaker: a cycle that folded
// or carried something moved the work, a cycle with an attempt that has not
// reported is waiting on a run — never a failure, however many ticks of one run
// go by (D1228) — and anything else did nothing at all.
func (c *cycle) outcome() CycleOutcome {
	switch {
	case c.rep.Folded > 0 || c.rep.Carried > 0 || c.rep.Triaged > 0 || c.rep.Escalated > 0:
		return CycleProgress
	case hasOpenIntent(c.state):
		return CycleInFlight
	}
	return CycleEmpty
}

// commit writes a state this cycle derived and takes the ETag that write left.
//
// One conditional write per state, under the ETag the node was last read at —
// the store's own rule (O3): a body derived from the document read at that ETag,
// written once, and never pushed again under a fresher one. A cycle writes
// several times (the fold, then the intent and the outcome of each action), and
// the ETag each write needs is the one the previous write left; the store's
// Save answers nothing, so the only way to know it is to read the node back,
// and that read is also the check that the node is still the one this cycle
// wrote.
func (c *cycle) commit(ctx context.Context, next State) error {
	if err := c.tick.Store.Save(ctx, next, c.etag); err != nil {
		// Whatever the reason, this cycle no longer holds the document its
		// next write would be derived from.
		c.fresh = false
		return err
	}
	back, etag, err := c.tick.Store.Load(ctx)
	if err != nil {
		c.fresh = false
		return err
	}
	if back.Rev != next.Rev {
		c.fresh = false
		return fmt.Errorf("%w: the node reads back at revision %d after the write of revision %d",
			ErrStateMoved, back.Rev, next.Rev)
	}
	c.state, c.etag = next, etag
	return nil
}

// ask commits the question a tripped breaker raises, and says so. The breaker
// asks at most one question per trip (Decision refuses one the node already
// carries), so a daemon that restarted into an unanswered question does not
// leave a second one behind — and a cycle whose node is not its own to write
// asks nothing at all.
func (c *cycle) ask(ctx context.Context) error {
	if c.tick.Breaker == nil || !c.fresh {
		return nil
	}
	op, ok := c.tick.Breaker.Decision(c.state, c.now)
	if !ok {
		return nil
	}
	next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: []Op{op}}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.say(ctx, Notification{Kind: NotifyDecisionNeeded, Subject: op.Decision.ID, Text: op.Decision.Question})
	return nil
}

// say hands one notification to the sink, once the write that made it true has
// landed. A sink that will not take it is recorded on the report and the cycle
// goes on: the notification is a channel, and the node is the record.
func (c *cycle) say(ctx context.Context, n Notification) {
	if c.tick.Notifier == nil {
		return
	}
	if err := c.tick.Notifier.Notify(ctx, n); err != nil {
		c.rep.NotifyErr = errors.Join(c.rep.NotifyErr, err)
	}
}

// TriageSeams is what one cycle acts on the long-run rule through (O14b): the
// read that gathers the rule's input, the two things an outcome can ask for
// that are not a state write, and the log every outcome leaves a line in. All
// three or none — a Tick built with none triages nothing, which is where a
// daemon with no host, no run storage or no audit log stands.
type TriageSeams struct {
	Read TriageReader
	Act  TriageActor
	Log  TriageLog
}

// Set reports whether every seam is there.
func (s TriageSeams) Set() bool { return s.Read != nil && s.Act != nil && s.Log != nil }

// TriageTarget is one run this cycle is due to judge: the item waiting on it,
// the run id its attempt recorded, how long that attempt has been going and
// the threshold it passed.
type TriageTarget struct {
	Item      WorkItem
	RunID     string
	Age       time.Duration
	Threshold time.Duration
}

// TriageReader gathers the rule's input for one run: the record, the pull
// request, the CI state and the findings file, each from where it lives. A
// reading that could not be made is reported inside the input — the rule's own
// unreadable inputs, which it fails closed on — and an error is a read that
// did not happen, which ends the cycle instead of judging a run on nothing.
//
// The age and the threshold are not the reader's: the cycle overwrites them
// from the target, so a run is due by the daemon's clock and the ladder's
// figure and by nothing else.
type TriageReader interface {
	TriageInput(ctx context.Context, target TriageTarget) (TriageInput, error)
}

// TriageActor carries out the two things an outcome can ask for beside a state
// write: landing the pull request the rule judged, and stopping the run it
// judged. A merge is at the head the rule read — a host that refuses it is a
// refusal the cycle reports to a person — and a cancel is the run's own cancel
// path, so the runner it stops still finalizes what it has.
type TriageActor interface {
	Merge(ctx context.Context, target TriageTarget, pr gitprov.PRInfo) error
	Cancel(ctx context.Context, target TriageTarget) error
}

// TriageLog is O3's audit log as one cycle writes it. Triaged answers whether
// this run's line is already there, which is what makes a repeated cycle
// append nothing and act on nothing; Append writes one line under the id the
// entry derives from its subject and transition.
type TriageLog interface {
	Triaged(ctx context.Context, run string) (bool, error)
	Append(ctx context.Context, entry Entry) error
}

// TriageAfter is the age at which the long-run rule is due: the ladder's own
// ceiling for the mode, `escalation.r3_after_hours` — the moment its rung 3,
// this rule's MergeAsIs, applies — and never a key of its own, because the
// local key this slice was once planned around was never built and is retired
// with the ladder's own figure (D2032). Zero when the ladder carries no row
// for the mode, which the rule reads as not due.
//
// A work item carries a rigor *tier* and the ladder is keyed by rigor *mode*,
// and nothing maps one to the other yet (the mode is gathered per run, which
// no release does): every item is therefore judged under the default mode's
// row. The critical-path factor is not applied here either — it scales the
// review rounds a mode gets, not the age at which a long run is judged.
func TriageAfter(mode rigor.Mode) time.Duration {
	row, ok := escalation.DefaultThresholds().Rows[mode]
	if !ok || row.CeilingHours <= 0 {
		return 0
	}
	return time.Duration(row.CeilingHours * float64(time.Hour))
}

// triage is one cycle's turn at the rule: every item with a run recorded whose
// attempt has passed the threshold, the rule read about each of them, and what
// it answered carried out — unless the log already carries the run's line, in
// which case the cycle leaves it alone, which is the whole of the idempotence.
//
// A cycle that no longer holds the node does not triage at all: the outcomes
// act on the world (a merge, a cancel) and file work, and one read from a node
// this cycle cannot write is not a state to act from.
func (c *cycle) triage(ctx context.Context) error {
	if !c.tick.Triage.Set() || !c.fresh {
		return nil
	}
	for _, target := range triageDue(c.state, c.now, TriageAfter(rigor.Default)) {
		done, err := c.tick.Triage.Log.Triaged(ctx, target.RunID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		in, err := c.tick.Triage.Read.TriageInput(ctx, target)
		if err != nil {
			return err
		}
		in.Elapsed, in.Threshold = target.Age, target.Threshold
		out := Triage(in)
		if !out.Due() {
			// A run the rule read no outcome for is left undocumented: it is
			// judged again next cycle, which is what makes a threshold that
			// comes due later one the daemon still reaches.
			continue
		}
		if err := c.triaged(ctx, target, in, out); err != nil {
			return err
		}
		c.rep.Triaged++
	}
	return nil
}

// triageDue is the runs a cycle's triage is about: every item with a run
// recorded whose attempt for that run has been going at least the threshold.
// The attempt is found by the run id the item names rather than by whether it
// has reported: the rule has an outcome for a run that has ended (the merge
// gate reads a terminal record) and one for a run still going, so both are
// judged here.
func triageDue(s State, now time.Time, threshold time.Duration) []TriageTarget {
	if threshold <= 0 {
		return nil
	}
	var out []TriageTarget
	for _, w := range s.WorkItems {
		if w.RunID == "" {
			continue
		}
		it, ok := attemptOf(s, w.RunID)
		if !ok {
			continue
		}
		age := now.Sub(it.At)
		if age < threshold {
			continue
		}
		out = append(out, TriageTarget{Item: w, RunID: w.RunID, Age: age, Threshold: threshold})
	}
	return out
}

// attemptOf is the intent recorded for one run id, reported or not.
func attemptOf(s State, runID string) (Intent, bool) {
	for _, it := range s.Intents {
		if it.RunID == runID {
			return it, true
		}
	}
	return Intent{}, false
}

// triaged carries one outcome out and writes the line that fences it.
//
// The order is the rule's: the act first — the merge the rule judged with
// every input read, the cancel that stops the run — then the state the outcome
// demands, then the line. A line is therefore only ever written for something
// that happened, and a cycle that died in the act leaves the run unfenced for
// the next one, which is the direction a lost write has to fail in.
func (c *cycle) triaged(ctx context.Context, target TriageTarget, in TriageInput, out TriageOutcome) error {
	text := triageLine(out)
	var (
		ops  []Op
		told []Notification
	)
	switch out.Kind {
	case MergeAsIs:
		// The one outcome that asks for a merge without a person, and the
		// only merge this package ever asks for: the head is the one the
		// rule judged, and the actor's own implementation is what refuses a
		// head that moved or a diff nothing corroborates.
		err := c.tick.Triage.Act.Merge(ctx, target, *in.PR)
		code := mergeRefusalCode(err)
		switch {
		case err == nil:
			text = triageLine(out) + " (merged)"
			if op, ok := c.followUp(target, in); ok {
				ops = append(ops, op)
			}
		case code != "":
			// A refusal is not an outage: the host said no to this merge —
			// the head moved, its merge cannot be bound to one, it holds no
			// merge credential — and what the rule judged is not what lands.
			// That is a person's to read, and the line below fences the run
			// for it. The line carries both: the outcome and its evidence,
			// then the refusal and its closed code, so the log is never read
			// as a merge that landed.
			text = triageLine(out) + " " + string(ReasonMergeRefused) + " (" + code + ")"
			if op, question, ok := c.askPerson(target, mergeRefusedQuestion(target, code)); ok {
				ops, told = append(ops, op), append(told, question)
			}
		default:
			return err
		}
	case Escalate:
		if err := c.tick.Triage.Act.Cancel(ctx, target); err != nil {
			return err
		}
		if op, question, ok := c.askPerson(target, escalateQuestion(target, out)); ok {
			ops, told = append(ops, op), append(told, question)
		}
	case Restart:
		// A run that is going nowhere is stopped here, and the smaller scopes
		// the rule named are filed for a person to approve: the daemon files
		// them unapproved, and only a person's own approval starts them
		// (D1226).
		if err := c.tick.Triage.Act.Cancel(ctx, target); err != nil {
			return err
		}
		ops = append(ops, c.smallerScopes(target, out)...)
	case RecommendOnly:
		// The outcome that raises nothing, files nothing, merges nothing and
		// cancels nothing: what it does is the line below, which names the
		// input nobody could read.
	}
	if len(ops) > 0 {
		next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
		if err != nil {
			return err
		}
		if err := c.commit(ctx, next); err != nil {
			return err
		}
	}
	if err := c.tick.Triage.Log.Append(ctx, Entry{Subject: target.RunID, Transition: triageTransition, Text: text}); err != nil {
		return err
	}
	for _, n := range told {
		c.say(ctx, n)
	}
	return nil
}

// ErrTriageUncorroborated is how a triage actor refuses a merge the rule
// judged: the paths the merge gate classified are the run's own account of its
// diff, and nothing the actor can read corroborates them at the judged head.
//
// The job writes that record from inside the container it shares with the
// agent (design §6.1), so a run can describe its diff as one of audit-safe
// paths while carrying a sensitive one, and a merge taken on it would land
// work nobody was asked about. The read that lifts this refusal is the host's
// changed files at the head under judgement; until a provider answers one, no
// merge lands unattended, and the outcome becomes a person's question.
var ErrTriageUncorroborated = errors.New("the changed paths are not corroborated at the judged head")

// codeUncorroborated is that refusal's closed code in the log and the question.
const codeUncorroborated = "uncorroborated"

// mergeRefusalCode is the closed code of a merge the actor refused, or ""
// when the error is not a refusal at all. A refusal is a person's: the head
// moved under the call, the provider's merge cannot be bound to an expected
// head at all, it holds no merge credential, the pull request is no longer
// open, or the diff under judgement is nobody's but the run's own word.
// Everything else is an outage — a host that did not answer — which the next
// cycle retries.
//
// The codes are this file's own vocabulary and never the provider's message:
// O3's line carries closed codes and no untrusted text, and two providers
// spell one refusal two ways.
func mergeRefusalCode(err error) string {
	for code, sentinel := range map[string]error{
		codeUncorroborated:      ErrTriageUncorroborated,
		"head_moved":            gitprov.ErrHeadMoved,
		"no_head_precondition":  gitprov.ErrMergeNoHeadPrecondition,
		"no_merge_mint":         gitprov.ErrNoMergeMint,
		"pull_request_not_open": gitprov.ErrPRNotOpen,
	} {
		if errors.Is(err, sentinel) {
			return code
		}
	}
	return ""
}

// askPerson is the question an outcome raises for a person: one decision per
// run, under the run's own id, and nothing when the node already carries it — a
// decision_add on an id that is there is refused, so a cycle that re-runs
// after a write that landed asks once.
func (c *cycle) askPerson(target TriageTarget, question string) (Op, Notification, bool) {
	id := target.RunID + "-" + triageTransition
	if _, ok := c.state.Decision(id); ok {
		return Op{}, Notification{}, false
	}
	op := Op{Op: OpDecisionAdd, Decision: &DecisionOp{ID: id, Question: question, WaitingOn: OnPerson}}
	return op, Notification{Kind: NotifyDecisionNeeded, Subject: id, Text: question}, true
}

// smallerScopes is the work a restart files: one item per reason the rule gave,
// each waiting for a person's approval — never approved here, because a daemon
// that approved its own work items would be the standing approval D1226
// refuses. An item whose name the node already carries is not filed again, so a
// cycle that re-runs after a committed write adds nothing.
func (c *cycle) smallerScopes(target TriageTarget, out TriageOutcome) []Op {
	var ops []Op
	for _, reason := range out.Reasons {
		id := target.RunID + "-" + string(reason)
		if _, ok := c.state.Item(id); ok {
			continue
		}
		ops = append(ops, newItemOp(id,
			"a smaller scope: "+target.Item.Title,
			"The triage restarted run "+target.RunID+" ("+string(reason)+"): the run is over or going nowhere, and what it was asked to do is not one attempt's work. Split it into a scope a person can approve; this item waits for that approval.",
			target.Item))
	}
	return ops
}

// followUp is the work a merge leaves behind: the findings the run wrote that
// did not block the landing are still open, and a person decides what to do
// about them. A merge with nothing open leaves nothing to file.
func (c *cycle) followUp(target TriageTarget, in TriageInput) (Op, bool) {
	if len(in.Findings.List) == 0 || in.Record == nil {
		return Op{}, false
	}
	id := target.RunID + "-follow-up"
	if _, ok := c.state.Item(id); ok {
		return Op{}, false
	}
	lines := make([]string, 0, len(in.Findings.List))
	for _, f := range in.Findings.List {
		lines = append(lines, string(f.Severity))
	}
	return newItemOp(id,
		"the findings run "+target.RunID+" left open",
		"Run "+target.RunID+" was merged with these findings still open: "+strings.Join(lines, ", ")+". Decide each one; this item waits for a person's approval.",
		target.Item), true
}

// newItemOp is one work item a cycle files: the repo, the recipe and the tier
// of the item the run was for, the phase work starts in, and no approval —
// only a person's own work_item_approve is one (D1226). The title and the
// brief are the store's to clip to their bounds, which is why this never
// builds one longer than they allow.
func newItemOp(id, title, brief string, of WorkItem) Op {
	phase := PhaseQueued
	repo, recipe, tier := of.Repo, of.Recipe, of.RigorTier
	return Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{
		ID: id, Title: &title, Repo: &repo, Recipe: &recipe, RigorTier: &tier, Brief: &brief, Phase: &phase,
	}}
}

// triageLine is what the audit log carries for one outcome: the outcome and
// the reasons it was taken for, in the rule's own words, so a person reading a
// long run's ending reads the evidence and not a story about it.
func triageLine(out TriageOutcome) string {
	return string(out.Kind) + ": " + triageCodes(out)
}

// triageCodes is an outcome's reasons as one line of closed codes.
func triageCodes(out TriageOutcome) string {
	codes := make([]string, 0, len(out.Reasons))
	for _, r := range out.Reasons {
		codes = append(codes, string(r))
	}
	return strings.Join(codes, " ")
}

// escalateQuestion is what a person is asked about a run the triage escalated:
// the codes the rule read it for, and the run they were read in.
func escalateQuestion(target TriageTarget, out TriageOutcome) string {
	return "The triage escalated run " + target.RunID + " (" + triageCodes(out) + "). The run was stopped; read the finding and decide what happens to it."
}

// mergeRefusedQuestion is what a person is asked about a merge that was
// refused: the refusal is theirs to act on — a moved head is a review, an
// unbound merge and a missing credential are configuration, an uncorroborated
// diff is the one the daemon will not land by itself — and the run keeps its
// own record either way.
func mergeRefusedQuestion(target TriageTarget, code string) string {
	if code == codeUncorroborated {
		return "The triage will not land the pull request of run " + target.RunID + " by itself: the changed paths it judged are the run's own account of its diff, and no host read of the judged head corroborates them (" + code + "). Read the diff and land it yourself, or close it."
	}
	return "The triage could not land the pull request of run " + target.RunID + " (" + code + "). Decide what happens to it."
}
