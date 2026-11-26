package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_selfflag.go is TN1's stop and D2215's rewrite: the two things the head
// of a run that converges an installation does about the project's managed
// marks.
//
// The stop. An installation the managed read answers is not managed is one this
// program may not converge: `fugaro update`, and `fugaro setup` on an
// installation that is already there (R8), refuse through the sentence every
// other command that changes a managed project uses (managedRequired), which
// names `fugaro managed flag` — the one command that flags a project, run by
// its owner at their own terminal (D1541, D1553 as TN1 amends it). The stop
// sits at the head of the engine, before the first cloud write and before the
// local config is rewritten (F16): everything below it converges an
// installation this run has no business changing, and a run that stopped after
// the catalog guard or the budget-mode rewrite would have edited the owner's
// own files on its way to a refusal.
//
// That the upgrade is then two commands once is the owner's ruling of
// 2026-11-21 (F11): `fugaro managed flag` is that once, and every `fugaro
// update` after it is the one command it always was. What the stop replaces —
// a converge that wrote the marks itself when it was given its budget once
// (D1553's old self-flag) — is TN1's deletion, because a converge is not where
// an owner decides that a project is managed, and because that write took the
// installation's typed confirmation away on the word of a flag.
//
// The rewrite. On a managed installation an explicit `--budget` rewrites the
// amount the creation fact records, through the writer that owns that object
// (managed.RewriteBudget), with the currency defaulting to the one the record
// holds where nobody named one (R6, D2211), and it prints the old and the new
// figures (D2215). A run given no amount writes nothing — a default never
// overwrites a recorded amount — and neither does one whose record already
// holds the figures it was given. The write is made here, at the head, before
// the run's own apply: the installation's alert follows in that same apply,
// because an explicit --budget is a figure this run's own document carries
// (R7, explicitBudgetAccount).
//
// The two fences stand where the write would be, and not at the head of the
// step (F20, D2217): a coding agent's session and a Fugaro run's own process
// may not rewrite the amount a project's runs are judged by — the record every
// later apply reads — so a rewrite either of them would make ends the run
// rather than going on to apply the explicit amount anyway. A run whose record
// already holds the figures it was given meets no fence, because there is no
// write for one to stand in front of; what reaches the fences is a run that
// would really change the record, and nothing else in this file can fail an
// update: a fact that cannot be read, or that holds no amount this build can
// replace, or a write the writer refused, are all warnings, and the run's own
// apply goes on with the amount it was given.

// requireManagedInstallation is TN1's stop, called first by the converge of
// every run that converges an installation: a project the managed read answers
// is not managed is refused with the command that flags one, before anything is
// read from the installation's own files and before anything is written.
//
// --plan-only is not stopped. It applies nothing by its own definition — it is
// what previews a plan — and refusing it would take the preview of an
// installation the owner is deciding about away from them. It reads, like every
// other run, and writes nothing.
func (e *initEngine) requireManagedInstallation() error {
	r, o := e.r, e.r.o
	if o.planOnly || !e.convergesAnInstallation() {
		return nil
	}
	g := readUpdateGate(e, r)
	if g.readErr != nil {
		// D2218, amended: a read that did not answer is not the answer "not
		// managed", and this run takes the path a project that is not managed
		// took before this plan — it warns and goes on. Stopping here would end
		// an owner's update over an outage, and the fail-closed direction is
		// the other one: an unproven project keeps every confirmation it has.
		r.warn(fmt.Sprintf("could not read whether %s is managed (%s): going on as a project that is not managed, which keeps every confirmation this run would otherwise skip", safetext.Field(r.gcpProject), safetext.FieldErr(g.readErr)))
		return nil
	}
	if g.managed {
		return nil
	}
	return managedRequired(r.cmd, r.gcpProject, g.g.marks.Reason)
}

// convergesAnInstallation is whether this run converges an installation that is
// already there rather than creating one: `fugaro update`, which does whatever
// its first run is, and `fugaro setup` on an installation this machine already
// has (R8) — a setup with a project config, which is update's stage set and
// update's own stage for the project it never creates. A first run of setup
// creates the installation (setupCreates): its project stage is registered, it
// marks the project itself (markCreated, T2a), and it is not this stop's. Nor
// is `fugaro apply`, whose own steps are fenced by the command that saved them.
func (e *initEngine) convergesAnInstallation() bool {
	o := e.r.o
	if o.createProject {
		return false
	}
	if o.door == updateDoor.name {
		return true
	}
	return o.door == setupDoor.name && !e.firstRun()
}

// settleExplicitBudget is D2215's rewrite and R6's currency with it, called by
// the converge right after the stop and before the first stage.
//
// It returns an error only for the two fences, which are the run's outcome: the
// write they refuse is one nothing else here may go around, and a run that
// applied the explicit amount after a refusal would leave the record saying one
// thing and the alert another.
func (e *initEngine) settleExplicitBudget(ctx context.Context) error {
	r, o := e.r, e.r.o
	if o.budget <= 0 || !e.convergesAnInstallation() {
		return nil
	}
	g := readUpdateGate(e, r)
	recorded, why := e.recordedBudget(ctx, g)
	// The currency first, and whether or not this run rewrites anything: the
	// document it plans is built before the converge (installSpec), so a
	// --budget-currency settled after the plan was made would leave the
	// installation's alert on a line the plan does not carry. USD is the
	// currency of a record that holds none (D2211), which is also every
	// project that is not managed.
	if o.budgetCurrency == "" {
		currency := managed.DefaultBudgetCurrency
		if why == "" {
			currency = recorded.Currency
		}
		e.setBudgetCurrency(currency)
	}
	switch {
	case o.planOnly:
		// A preview writes nothing, and it is not a fence's business either: it
		// has read what the record holds and says what it would plan.
		return nil
	case why != "":
		r.warn(fmt.Sprintf("this run was given --budget %d %s, and %s: nothing was written to the creation fact, and this run's own apply uses the amount it was given. `fugaro managed flag %s --budget %d --budget-currency %s`, run by its owner at their own terminal, is the command that records an amount there",
			o.budget, safetext.Field(o.budgetCurrency), why, r.gcpProject, o.budget, safetext.Field(o.budgetCurrency)))
		return nil
	}
	want := managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}
	if recorded == want {
		return nil
	}
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		return userErr("nothing was applied: %s is a managed project whose creation fact records the budget %d %s, and the --budget %d %s this run was given would rewrite what the project's runs are judged by. That write is the owner's own decision, made at their own terminal: %s. Run `fugaro update --budget %d --budget-currency %s` yourself at your own terminal",
			safetext.Field(r.gcpProject), recorded.Amount, safetext.Field(recorded.Currency), o.budget, safetext.Field(o.budgetCurrency), initflow.AgentRefusal(m), o.budget, safetext.Field(o.budgetCurrency))
	}
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was applied: %s is a managed project whose creation fact records the budget %d %s, this run's --budget %d %s would rewrite it, and this process is a Fugaro run's (%s is set): a run credential holds the operator's credentials and may not decide the amount the project it runs in is judged by. Run `fugaro update --budget %d --budget-currency %s` yourself at your own terminal",
			safetext.Field(r.gcpProject), recorded.Amount, safetext.Field(recorded.Currency), o.budget, safetext.Field(o.budgetCurrency), m, o.budget, safetext.Field(o.budgetCurrency))
	}
	if e.c == nil {
		// A run that never opened the cloud has already said why it stopped,
		// and this step adds no second failure to it.
		return nil
	}
	number, err := infra.ProjectNumber(ctx, e.c, r.gcpProject)
	if err != nil {
		r.warn(fmt.Sprintf("this run was given the budget %d %s and the creation fact of %s records %d %s, but its project number could not be read (%s), and the number is what proves the runs bucket is this project's own: nothing was written to the fact, and this run's own apply uses the amount it was given",
			o.budget, safetext.Field(o.budgetCurrency), safetext.Field(r.gcpProject), recorded.Amount, safetext.Field(recorded.Currency), safetext.Field(err.Error())))
		return nil
	}
	bucket, _ := e.runsBucketFor()
	old, err := managed.RewriteBudget(ctx, e.c.Storage, bucket, r.gcpProject, number, want)
	if err != nil {
		r.warn(fmt.Sprintf("the creation fact of %s was left as it is (%s): the update itself is unchanged, and this run's own apply uses the --budget %d %s it was given. `fugaro managed flag %s --budget %d --budget-currency %s` is the command that replaces what a fact records",
			safetext.Field(r.gcpProject), safetext.FieldErr(err), o.budget, safetext.Field(o.budgetCurrency), r.gcpProject, o.budget, safetext.Field(o.budgetCurrency)))
		return nil
	}
	fmt.Fprintf(r.w, "the creation fact in gs://%s/%s recorded the budget %d %s and now records %d %s: this run's own apply sets the installation's alert to that amount, keep the account its state records\n",
		bucket, managed.MarkerObject, old.Amount, safetext.Field(old.Currency), want.Amount, safetext.Field(want.Currency))
	return nil
}

// recordedBudget is the budget amount the installation's creation fact records,
// read through the same read the apply-time gate makes (ReadBudgetAnswer): an
// amount, and the currency with it (R6).
//
// Its second return is why there is no amount to compare an explicit --budget
// with, "" when there is one, and every way of not having one is a warning
// rather than a failure: a fact that could not be read at all, a fact that
// records no amount this build can use (a version-1 creation, or a figure
// Budget.Check refuses), and a project this read does not answer managed for —
// which under --plan-only is the one run that reaches here with the stop
// skipped.
func (e *initEngine) recordedBudget(ctx context.Context, g *initGate) (managed.Budget, string) {
	r := e.r
	if g.readErr != nil {
		return managed.Budget{}, fmt.Sprintf("the creation fact of %s could not be read (%s)", safetext.Field(r.gcpProject), safetext.FieldErr(g.readErr))
	}
	if !g.managed || e.c == nil {
		return managed.Budget{}, fmt.Sprintf("%s is not an installation whose creation fact records an amount", safetext.Field(r.gcpProject))
	}
	answer, err := managed.ReadBudgetAnswer(ctx, e.c.Storage, e.c.CRM, r.projectName, r.gcpProject)
	switch {
	case err != nil:
		return managed.Budget{}, fmt.Sprintf("the creation fact of %s could not be read (%s)", safetext.Field(r.gcpProject), safetext.FieldErr(err))
	case !answer.OK:
		return managed.Budget{}, answer.Reason
	}
	return answer.Budget, ""
}

// setBudgetCurrency settles the currency of this run's explicit --budget: on
// the flag's own field, which every later reader of it reads, and on the
// document this run plans, which was already built from that field
// (installSpec) and would otherwise carry the empty string the flag arrived
// with.
func (e *initEngine) setBudgetCurrency(currency string) {
	e.r.o.budgetCurrency = currency
	if e.spec.Budget != nil {
		e.spec.Budget.CurrencyCode = currency
	}
}
