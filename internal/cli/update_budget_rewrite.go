package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_budget_rewrite.go is D2215's write on the converge's own door: a run
// given an explicit --budget records the amount in the managed project's
// creation fact, before anything is applied.
//
// The marker is the source of truth for the amount and for the currency (R7 of
// docs/managed-flag-ergonomics/reconciliation.md): it is what the next run's
// carry reads (init.go's carriedBudget), and a run that planned the alert from
// its own flags without rewriting the marker would leave the two disagreeing —
// the alert it applied would be moved back by the next update's carry, which
// reads the marker. So the write happens first, and the apply that follows is
// the one the owner asked for.
//
// It is not a marking, and this file holds no marking any more: TN1's ruling is
// that `update` never turns an installation that is not managed into one — a
// run on such an installation is stopped at the head of the engine
// (update_managed.go's stopUnmanaged), naming `fugaro managed flag`. What is
// left here is the one write a converge makes to the marker of a project that
// IS managed, and it is D1553's successor: the amount is the owner's, stated on
// the command line, and the run records what it was told.
//
// Four things decide what the step does, and each is one of the answer's own:
//
//   - It runs only when the run was given --budget (budgetChanged). An amount
//     has no default here (R6): the person who never named one is not asking
//     for a change, and the installation's own figures are carried to the alert
//     by the next stage (carriedBudget). A rewrite is a statement by the owner
//     and never a default's doing (D2215).
//   - It runs on a managed installation only, and only with the read's ANSWER
//     behind it. A read that failed is not an answer: the run warns and goes on
//     as an unmanaged project did before this plan, and no amount is written
//     (D2218). An installation the read answers is unmanaged is refused before
//     this step is reached.
//   - It refuses in a coding agent's session, and in a Fugaro run's own process
//     whichever markers its agent carries — the pair the marking kept, kept
//     here for the same reason (F20): what the amount is, is the owner's own
//     decision about the alert their project pays for, and a run's credential
//     could make it. The refusal ends the run: it never applies the amount it
//     was given.
//   - Its failures are the run's outcome and not a warning. What the run would
//     do next is apply an alert on the amount it was given, while the marker —
//     the source of truth — still records the old one, and the next update's
//     carry would quietly move the alert back. So a rewrite the marker refuses
//     stops the run where it stands, having written nothing.
//
// Nothing is applied before this step and nothing is written beside the marker:
// it runs at the head of the converge, before the first stage, so a run that
// stops here leaves the installation exactly as it was.
func (e *initEngine) rewriteBudget(ctx context.Context) error {
	r, o := e.r, e.r.o
	if !o.budgetChanged || o.planOnly || o.createProject {
		return nil
	}
	// The read the stop at the head already made (gateRead): this step asks the
	// same question about the same installation, and D1015's one read per
	// command is what it asks it through. Anything but the read's ANSWER that
	// the project is managed writes nothing: a read that failed is not an
	// answer (D2218, and the stop has already warned in the fence's words), and
	// a project the read answers is unmanaged was refused there.
	g := e.gateRead()
	if g.unread != nil || !g.managed {
		return nil
	}
	// The bucket the managed read looks in, by its default name: a fact kept
	// anywhere else is one no reader of this build ever finds (readFactObject
	// derives the name from the project), so an amount written there would be
	// one the next run's carry does not read either. What names the
	// installation's own bucket is the outputs when the run read them, and the
	// selected config otherwise.
	bucket, named := e.runsBucketFor()
	if named != "" && named != bucket {
		r.warn(fmt.Sprintf("this run was given a budget and the amount was not recorded: the project config names the runs bucket %s, and a managed project's creation fact is read from %s, so a fact written into another bucket would be one nothing reads. Check runs_bucket in the project config", safetext.Field(named), safetext.Field(bucket)))
		return nil
	}
	// What the installation records now, read before the statement and before
	// anything is written: it answers the currency a rewrite keeps when the
	// owner named none (R6), and its reason is what a refused write says about
	// the state it found (D1545's warning states the same read's answer).
	answer, err := managed.ReadBudgetAnswer(ctx, e.c.Storage, e.c.CRM, r.projectName, r.gcpProject)
	if err != nil {
		return remote(fmt.Errorf("nothing was applied: the budget %s's creation fact records could not be read, so the amount this run was given cannot be recorded there: %s", safetext.Field(r.gcpProject), safetext.FieldErr(err)))
	}
	currency := o.budgetCurrency
	if !o.budgetCurrencyChanged {
		// R6: the currency a rewrite writes is the one recorded unless the
		// owner named another. Putting a default over a recorded EUR would be
		// the one thing this flag's default must never do. An installation
		// recording none takes the default, and the writer below is what
		// refuses that state if its marker is one this build will not write.
		if currency = answer.Budget.Currency; currency == "" {
			currency = managed.DefaultBudgetCurrency
		}
		// The run's own installation block carries the same pair the marker is
		// about to record, so what the apply writes and what the marker says
		// cannot disagree — the carry's own rule, taken here from the same
		// read (`managed flag` does the same for the flag it rewrites).
		o.budgetCurrency = currency
	}
	budget := managed.Budget{Amount: o.budget, Currency: currency}
	// The fences, exactly where the write would be: past this point this run
	// records a new amount for the installation, and that is the owner's own
	// decision — consent given by a flag is only as strong as the markers that
	// identify the session (F20).
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		return userErr("nothing was applied: this run was given --budget, and the amount a managed project's creation fact records is the alert its owner pays for: %s. Run `fugaro update --budget %d --budget-currency %s` yourself at your own terminal",
			initflow.AgentRefusal(m), o.budget, safetext.Field(currency))
	}
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was applied: this run was given --budget and this process is a Fugaro run's (%s is set), and the amount a managed project's creation fact records is its owner's own decision. Run `fugaro update --budget %d --budget-currency %s` yourself at your own terminal",
			m, o.budget, safetext.Field(currency))
	}
	number, err := infra.ProjectNumber(ctx, e.c, r.gcpProject)
	if err != nil {
		return remote(fmt.Errorf("nothing was applied: reading the number of GCP project %s, which is what proves a runs bucket is this project's own: %s", safetext.Field(r.gcpProject), safetext.FieldErr(err)))
	}
	// Equal figures write nothing at all (RewriteBudget), and that is not an
	// optimisation: it is what makes this step safe to reach on every update
	// given a --budget, and it is why nothing here compares the figures to
	// decide whether to write.
	old, err := managed.RewriteBudget(ctx, e.c.Storage, bucket, r.gcpProject, number, budget)
	if err != nil {
		return userErr("nothing was applied: the budget %s's creation fact records was not replaced with %d %s, and this run does not apply the amount it was given over a marker that still records another one: %s",
			safetext.Field(r.gcpProject), budget.Amount, safetext.Field(budget.Currency), safetext.FieldErr(err))
	}
	if old == budget {
		return nil
	}
	fmt.Fprintf(r.w, "%s: the budget recorded in gs://%s/%s is now %d %s, was %d %s\n",
		o.doorName(), bucket, managed.MarkerObject, budget.Amount, safetext.Field(budget.Currency), old.Amount, safetext.Field(old.Currency))
	return nil
}
