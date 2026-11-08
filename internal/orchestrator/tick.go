// The reconcile tick: one cycle of the daemon's loop (O7).
//
// A tick is level-triggered (D1229). It reads the node, reads what the runs
// behind the node's intents reported, folds the finished ones into the items,
// plans (O6), carries the plan out (O6's starter, intent first), and commits.
// There is no cursor and no replay: a tick derives its whole answer from the
// node as it now stands, so a tick that ran twice over one node is the same as
// one that ran once (TestTickIsIdempotentOnRepeat), and a daemon that crashed
// mid-cycle resumes by running the next tick.
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
	"time"
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
	// NotifyErr is a notification a sink would not take. It never fails the
	// cycle: what a cycle must not lose is the write behind the notification.
	NotifyErr error
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
	if err := c.carry(ctx, viewOf(reports)); err != nil {
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
	case c.rep.Folded > 0 || c.rep.Carried > 0:
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
