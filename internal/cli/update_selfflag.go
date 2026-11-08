package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	// managedmode is the package of the managed apply path, and the derived
	// bucket names are its (the same alias init.go and managed_flag.go use): the
	// check below is the flag command's own, by its name and not a literal.
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_selfflag.go is D1553's second half, and the HARD RULE's: `fugaro
// update` on an installation that is not managed flags it itself when it is
// given its budget once, so the upgrade stays one command and the owner runs no
// second one. `fugaro managed flag` remains the owner's own command — it is
// what a project unmanaged for a reason update cannot fix is flagged with, and
// what the unmanaged refusal of every other mutating command names (D1541) —
// but it is not a step the upgrade needs.
//
// Five things decide what the step does, and each is one of the answer's own:
//
//   - It runs in `fugaro update` and in no other run. The doors share this
//     engine and this hook, and D1553's answer is about the upgrade: update is
//     the door that re-converges an installation that already exists, and the
//     flag that triggers this step says so — `--budget`'s help on that door
//     (budgetFlagHelp, init.go), which is where it has to be, because
//     `--managed` is declared on this door, hidden and refused by name, so its
//     text is rendered to nobody. `fugaro setup` on an existing project is a
//     different question — its own --managed help says flagging a project that
//     already exists is not setup's, and it stays that way: setup flags nothing
//     it did not create.
//   - It never runs under --plan-only. That door creates and changes nothing —
//     it is what previews the plan — and this step's whole output is two marks
//     that take an installation's typed confirmation away. A preview that wrote
//     them would turn a consent gate off on a read-only flag's word, which is
//     the one thing --plan-only promises it cannot do.
//   - It runs only when the run was given the budget. The amount is the
//     owner's, required and never defaulted (D64), and a fact that records no
//     amount is refused by every managed apply — so a run given no amount has
//     nothing to record and flags nothing: it says nothing rather than writing
//     a marker the apply would refuse anyway. The pair is the installation's
//     own --budget and --budget-currency; the billing account that must come
//     with them is init's own check, before the run starts.
//   - It flags what is not managed, through the flag command's own writers.
//     The read is ReadBudgetAnswer, the read the apply-time gate makes: an
//     installation whose fact already records an amount needs nothing (this is
//     the ordinary second run, and it is silent), and everything else goes to
//     managed.WriteFlag, which is the authority on what may be written — it
//     creates the object where none exists and replaces this project's
//     version-1 creation fact, and refuses every other state with what is
//     there. The label is the flag command's own write beside it (WriteManaged-
//     LabelForFlag), because the read requires both marks. The one refused
//     write that is not the end of it is this step's own half-written state —
//     the object recording the amount and the bound the flag writes, the label
//     missing, which is what a run leaves when its label write fails; it is read
//     without the label (ReadFlagFact), and the label is written to finish it,
//     so the owner's way out of that state is this same command rather than a
//     hand-written label
//     (see the completion below).
//   - It refuses in a coding agent's session, even when --budget was passed,
//     and in a Fugaro run's own process whichever markers its agent carries:
//     what it writes is the marks that take the installation's typed
//     confirmation away, so consent given by a flag is only as strong as the
//     markers that identify the session (F20). The two refusals are the flag
//     command's own pair (managed_flag.go), kept here for the same reason: a
//     run's variables are set by the platform rather than by the agent, so the
//     run's credential would write these marks where the agent's session cannot
//     — and here it would write them on its own installation. The refusal names
//     the marker and the command the owner runs at their own terminal, which is
//     the same one this step would have run for them.
//
// What the step writes, it states before it writes it: the flag command's own
// statement (managedFlagStatement, managed_flag.go), so a run that flags an
// installation says what it records and what it gives up before either mark is
// written, and not only in the line that reports the flag afterwards.
//
// The fences stand where the write would be, and not at the head of the step:
// a run whose installation already records an amount flags nothing, so an
// agent's session that passes its budget to an up-to-date installation meets no
// fence at all — refusing there would refuse an ordinary update for a write
// that was never going to happen. What it reads before the fences is the same
// read the apply-time gate makes, and reading writes nothing.
//
// Nothing here can make a `fugaro update` fail: a write that did not happen is
// a warning and the run's outcome is unchanged (the unmarked project is the
// fail-closed direction, and the update itself succeeded). The one exception is
// the two refusals above, which are the run's outcome because they are the
// fence F20 describes, and the exit code is a user error like every other
// refusal.
//
// A run that created the project is not this step's: its mark is markCreated's
// (init.go), which writes both halves for every project a run creates and
// records the amount when the creation was asked to be managed. Flagging cannot
// add anything to it, and a second writer of the same two marks is the drift
// markCreated's own comments exist to prevent.

// selfFlagUnmanaged is the step: what it returns is the run's outcome only for
// the agent refusal, and "" (nil) everywhere else.
func (e *initEngine) selfFlagUnmanaged(ctx context.Context) error {
	r, o := e.r, e.r.o
	// The door first, because it is the run and not the input: this step is the
	// upgrade's (D1553), and the hook it hangs on is the one both doors share.
	// `fugaro setup` on an existing unmanaged project flags nothing — its own
	// --managed help says flagging one that already exists is not setup's, and a
	// wizard door that wrote the marks taking the installation's typed
	// confirmation away on its way past would be that sentence turned round.
	if o.doorName() != updateDoor.name {
		return nil
	}
	// Then --plan-only, which creates and changes nothing, and the marks below
	// are the marks that take this installation's typed confirmation away: a
	// preview writes neither, and it is no fence's business either — what an
	// agent's session could do with --plan-only is read a plan.
	if o.planOnly {
		return nil
	}
	if o.budget <= 0 || o.budgetCurrency == "" {
		return nil
	}
	// A run that never opened the cloud (an environment refusal, a refusal
	// before the clients) has nothing to read and nothing to write, and it has
	// already said why it stopped: this step adds no second failure to it. The
	// test is the clients and not the run's outputs, because a coding agent's
	// session skips the local config's write and with it the read those outputs
	// come from — and that session is exactly the one the fence below is for.
	if e.c == nil {
		return nil
	}
	// The creating path's mark, and not this step's (see the file's comment).
	if r.creation() != "" {
		return nil
	}
	// The bucket the marks live in, under the default name the read looks in: a
	// project whose config names another one is one whose mark nobody would
	// read, so this step warns rather than writing it somewhere the managed read
	// does not look (markCreated keeps the same rule for a creating run, and the
	// same comparison `fugaro managed flag` and `unflag` refuse on). What names
	// it is the installation's own outputs when the run read them, and the
	// selected config otherwise — a coding agent's session skips the local
	// config's write and with it the read those outputs come from.
	bucket := "fugaro-runs-" + r.gcpProject
	named := ""
	switch {
	case r.res.Outputs != nil:
		named = r.res.Outputs.RunsBucket
	case e.lc != nil:
		named = e.lc.RunsBucketName()
	}
	if named != "" && named != bucket {
		r.warn(fmt.Sprintf("this run was given a budget and %s is not managed, but its runs bucket is %s, not %s: the managed read only looks for the creation fact in the default bucket name, so nothing was flagged. Check runs_bucket in the project config", r.gcpProject, named, bucket))
		return nil
	}
	answer, err := managed.ReadBudgetAnswer(ctx, e.c.Storage, e.c.CRM, r.projectName, r.gcpProject)
	if err != nil {
		r.warn(fmt.Sprintf("this run was given a budget and could not read whether %s is managed (%v), so nothing was flagged and the installation stays as it is: a read that did not answer is not a reason to write", r.gcpProject, safetext.FieldErr(err)))
		return nil
	}
	if answer.OK {
		return nil
	}
	// A fact this build refuses is not this step's to replace, and the flag
	// command's own writer would refuse it too: managed.WriteFlag replaces one
	// object and one only — this project's version-1 creation fact — and leaves
	// every other state exactly where it is. So there is no write to make here,
	// which means no fence either, and the run says what it read instead of
	// naming a command that would refuse the same object (the round-2 review of
	// restore's own message made the same correction).
	if answer.Invalid {
		r.warn(fmt.Sprintf("this run was given a budget, and %s's creation fact is a figure this build refuses (%s): nothing was flagged, and no command of ours replaces that fact — `fugaro managed flag` replaces a version-1 creation fact, which records no amount, and nothing else. Read the object with `gcloud storage cat gs://%s/%s` and write the fact the owner means",
			r.gcpProject, safetext.Field(managedReasonFor(answer)), bucket, managed.MarkerObject))
		return nil
	}
	// And the installation's own state bucket, by the same rule and for the same
	// reason `fugaro managed flag` refuses on (managed_flag.go's check, and
	// checkManagedStateBucket's on the creating path): the managed apply reads
	// what the installation's Terraform accounts for out of
	// gs://fugaro-tfstate-<gcp_project> BY THAT DERIVED NAME, never from a flag
	// or from the config, so state kept anywhere else reads as CauseStateUnread —
	// and every managed apply of the project would keep the typed confirmation
	// this step exists to take away, with a coding agent's run stopping there
	// rather than applying. The flag command refuses the write; nothing here may
	// fail an update, so this step warns and flags nothing: the state is the
	// owner's to move, and a mark written first would only be one the managed
	// read's apply-time gate does not believe.
	if e.lc != nil {
		want := managedmode.InstallationsStateBucket(r.gcpProject)
		if got := e.lc.Terraform.StateBucket; got != "" && got != want {
			r.warn(fmt.Sprintf("this run was given a budget and %s is not managed, but the project config records the Terraform state bucket %q, and a managed project's installation state must live in %s: the managed apply reads that state out of the derived bucket name — a function of the GCP project, never of a flag or of the config — so state kept anywhere else reads as state that could not be read at all, and every apply of %s would keep the typed confirmation instead of skipping it, so nothing was flagged: move the state to %s and run this again, or leave this project unmanaged", r.gcpProject, safetext.Field(got), safetext.Field(want), safetext.Field(r.projectName), safetext.Field(want)))
			return nil
		}
	}
	// The fence, exactly where the write would be: past this point this run
	// flags the installation, and what it writes is what turns the typed
	// confirmation off — so in a coding agent's session it is refused, whatever
	// flags it was given (F20). The refusal is the run's outcome and not a
	// warning, because a warning would let the session report a finished update
	// over an installation that is still unmanaged, which is the state the next
	// apply refuses.
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		return userErr("nothing was flagged: GCP project %s is not managed (%s), and this run was given the budget once — a fugaro update that is given its budget turns an installation that is not managed into a managed one in the same command, with no second command. What that write does is take the installation's typed confirmation away, and consent given by a flag is only as strong as the markers that identify the session (F20): %s. Run `fugaro update --budget N --budget-currency CCC --billing-account <account>` yourself at your own terminal, and nothing after it needs a second command",
			safetext.Field(r.gcpProject), safetext.Field(managedReasonFor(answer)), initflow.AgentRefusal(m))
	}
	// And a Fugaro run's own process, whatever its agent's markers: the plan
	// refuses this write from a run credential as well as from an agent's
	// session, and the two are not the same test — the flag command and the
	// unflag both keep it beside the agent one, for the reason managed_flag.go
	// states (a run's variables are set by the platform rather than by the
	// agent, and the credential could write these marks itself). It matters
	// more here than there: this write is the one a run makes on its own
	// installation, so a run that could make it would take away the
	// confirmation that stands between its next apply and its own verdict.
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was flagged: GCP project %s is not managed, this run was given the budget once, and this process is a Fugaro run's (%s is set): the marks this step writes are the ones that take the installation's typed confirmation away, and a run credential may not write them — it holds the operator's credentials and could take the confirmation off the project its own applies are judged by. Run `fugaro update --budget N --budget-currency CCC --billing-account <account>` yourself at your own terminal, and nothing after it needs a second command",
			safetext.Field(r.gcpProject), m)
	}
	number, err := infra.ProjectNumber(ctx, e.c, r.gcpProject)
	if err != nil {
		r.warn(fmt.Sprintf("this run was given a budget and %s records no amount, but its project number could not be read (%v), and the number is what proves a runs bucket is this project's own: nothing was flagged", r.gcpProject, safetext.Field(err.Error())))
		return nil
	}
	budget := managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}
	// What is about to be written, and what it costs, before it is written: the
	// flag command's own statement, because this step makes the flag command's
	// own write (managedFlagStatement, and managed_flag.go for why the two
	// share it). `--budget`'s help on this door says in one sentence what the
	// trio does here; this is the same statement the owner's own flag command
	// prints, so a run that flags an installation is not quieter about it than
	// the command that does the same thing. Its last line is this step's, where
	// the two differ: what may be finished rather than refused.
	managedFlagStatement(r.w, r.gcpProject, bucket, o.budget, o.budgetCurrency)
	fmt.Fprintf(r.w, "  if this project already records whether it is managed: nothing is written unless its marker is the creation fact that records no amount (what `fugaro setup --create-project` without --managed leaves), which this replaces; a fact that already records the budget amount this run was given is left where it is, and only the missing label is written; anything else is refused, and so is any object this build cannot read as this project's.\n")
	// The flag command's own writers, in the flag command's own order: the
	// object is the authority and goes first, and the label is the narrowing
	// mark that completes it. Both are what the read requires, so one without
	// the other leaves the installation exactly as unmanaged as it was.
	replaced, err := managed.WriteFlag(ctx, e.c.Storage, bucket, r.gcpProject, number, budget)
	if err != nil && !errors.Is(err, managed.ErrAlreadyMarked) {
		r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged (%v): the installation stays as it is, and nothing of the update above is undone. `fugaro managed flag %s --budget N --budget-currency CCC`, run by its owner at their own terminal, is the command that does it",
			r.gcpProject, managedReasonFor(answer), safetext.FieldErr(err), r.gcpProject))
		return nil
	}
	// ErrAlreadyMarked is the one refused write with something left to do, and
	// what that is depends on the object — which no read of this package that
	// requires the label can tell (readFact drops the object when the label is
	// missing), so it is read without the label here (ReadFlagFact, which
	// proves the bucket the same way the writer did). The state it exists for
	// is this step's own: the object written by a run whose label write then
	// failed, which is this project's fact recording both figures the flag
	// writes. A second run reads not-managed — the label it requires is the
	// half that is missing — reaches this writer, and meets that same object,
	// so without the completion below the owner's way out would be a label
	// written by hand: exactly the manual repair the `fugaro update` rule
	// forbids, and one `fugaro managed flag` cannot make either (it writes
	// through this same WriteFlag, and refuses this object for the same
	// reason).
	already := false
	if err != nil {
		why := safetext.FieldErr(err)
		fact, ferr := managed.ReadFlagFact(ctx, e.c.Storage, bucket, r.gcpProject, number)
		switch {
		case ferr != nil:
			r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged: %v. The object the flag writer found could not be read back (%v), so what is there is unknown: check gs://%s/%s with `gcloud storage cat` as the operator before relying on either mark",
				r.gcpProject, managedReasonFor(answer), why, safetext.FieldErr(ferr), bucket, managed.MarkerObject))
			return nil
		case !fact.Ours:
			// The object is not this project's fact, so it is not an amount this
			// run may adopt either — the same answer `fugaro managed flag` gives
			// it, and the reason the completion above is gated on the fact being
			// this project's. What the object is, is readFact's own sentence
			// (ReadFlagFact keeps the package's wording), so nothing here is a
			// second account of what is there.
			r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged: %v. Nothing of the update above is undone, and there is nothing for this step to write — it replaces one object, this project's version-1 creation fact, which records no amount, and completes a fact that already records the amount this run was given. %s, and `fugaro managed flag` refuses that same object for the same reason: read it with `gcloud storage cat gs://%s/%s` as the operator before deciding what to do with it",
				r.gcpProject, managedReasonFor(answer), why, safetext.Field(fact.Reason), bucket, managed.MarkerObject))
			return nil
		case fact.Budget != budget:
			r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged: %v. The fact where the object is records the budget amount %d %s, and this run was given %d %s — a flag records an amount, it does not edit one — so neither mark was written and the installation stays as it is. Run `fugaro update --budget %d --budget-currency %s --billing-account <account>` again if that is the amount meant: that run finds the fact recording it, writes the missing %s=%s label, and flags the installation in the same command",
				r.gcpProject, managedReasonFor(answer), why, fact.Budget.Amount, safetext.Field(fact.Budget.Currency), o.budget, safetext.Field(o.budgetCurrency), fact.Budget.Amount, safetext.Field(fact.Budget.Currency), managed.ProjectLabelKey, managed.ProjectLabelValue))
			return nil
		case fact.Builds != managed.DefaultImageBuilds:
			// The amount is the one this run was given, and the other figure the
			// fact carries is not the one the flag writes: the statement above —
			// which this run printed before asking for either write — says the
			// bound is one build per invocation (D1013), and this object holds
			// the project's applies to another count. Finishing it would make
			// the project managed at a bound this run never stated, which is the
			// same harm as recording another amount one step along: the figure
			// the owner read is not the figure that takes effect. Nothing of
			// ours writes such an object — a bound other than the default is
			// recorded by hand, with the label beside it (D1013's own way past
			// the bound, docs/design/managed-projects.md) — so no command of
			// ours closes this state, and this run says what is there instead of
			// naming one that would refuse the same object.
			r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged: %v. The fact where the object is records the budget amount %d %s, and it holds this project's applies to %d billable builds per invocation, where the flag this step would finish records %d: neither mark was written and the installation stays as it is, because the label would complete a bound this run never stated. Read the object with `gcloud storage cat gs://%s/%s` as the operator and decide: a bound other than the default is one the owner records by hand, with the %s=%s label written beside it, and `fugaro managed unflag %s` takes both marks off so a later `fugaro update --budget %d --budget-currency %s --billing-account <account>` flags the project at the default bound",
				r.gcpProject, managedReasonFor(answer), why, fact.Budget.Amount, safetext.Field(fact.Budget.Currency), fact.Builds, managed.DefaultImageBuilds, bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, r.gcpProject, fact.Budget.Amount, safetext.Field(fact.Budget.Currency)))
			return nil
		}
		// This project's fact, recording the amount this run was given and the
		// bound the flag records, with the label the only half missing: the
		// object is left exactly where it is, and the label below finishes what
		// the earlier run started.
		already = true
	}
	what := fmt.Sprintf("wrote gs://%s/%s: %s is a managed project", bucket, managed.MarkerObject, r.projectName)
	switch {
	case replaced:
		what = fmt.Sprintf("replaced the creation fact in gs://%s/%s, which recorded no budget amount, with the version that records %d %s", bucket, managed.MarkerObject, o.budget, safetext.Field(o.budgetCurrency))
	case already:
		what = fmt.Sprintf("left the creation fact in gs://%s/%s where it was, recording %d %s, and finished it", bucket, managed.MarkerObject, budget.Amount, safetext.Field(budget.Currency))
	}
	return e.selfFlagLabel(ctx, bucket, budget, what)
}

// selfFlagLabel writes the label half of the flag and reports what happened: the
// label write itself, the read-back through the managed read the apply-time gate
// makes, and the success line. It is one function because both of the step's
// paths reach it — the write that created or replaced the object, and the write
// that found the fact already there recording the amount this run was given —
// and because a run whose label write fails has to say the one thing that
// finishes it, which is the same sentence in both.
//
// Nothing here can fail the update: every outcome that is not the whole flag is
// a warning (the unmarked project is the fail-closed direction, and the update
// itself succeeded). what opens the success line and is the caller's, because
// the three paths differ in what the object half did.
func (e *initEngine) selfFlagLabel(ctx context.Context, bucket string, budget managed.Budget, what string) error {
	r := e.r
	if err := managed.WriteManagedLabelForFlag(ctx, e.c.CRM, r.gcpProject); err != nil {
		r.warn(fmt.Sprintf("this run wrote gs://%s/%s but not the %s=%s label on %s (%v): the read requires both marks, so the installation is not managed yet and nothing is skipped for it. The fact in the runs bucket records the amount %d %s, so running `fugaro update --budget %d --budget-currency %s --billing-account <account>` again finishes the flag in that same run — it finds the fact recording that amount and writes the label this run could not; the label's own line, for the owner at their own terminal, is in docs/design/managed-projects.md",
			bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, r.gcpProject, safetext.FieldErr(err), budget.Amount, safetext.Field(budget.Currency), budget.Amount, safetext.Field(budget.Currency)))
		return nil
	}
	// Read back through the managed read itself, the one the apply-time gate
	// makes: a project this run said it flagged and the read still refuses is
	// one nothing is skipped for, and the warning says so rather than the
	// success line claiming a mark the read does not believe.
	if ok, reason, err := managed.IsManaged(ctx, e.c.Storage, e.c.CRM, r.projectName, r.gcpProject); err != nil || !ok {
		if err != nil {
			reason = safetext.FieldErr(err).Error()
		}
		r.warn(fmt.Sprintf("this run wrote gs://%s/%s and the %s=%s label, and the managed read of %s still does not believe it (%s): check the two marks with the operator's own credentials before relying on them",
			bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, r.gcpProject, safetext.Field(reason)))
		return nil
	}
	fmt.Fprintf(r.w, "%s, and the label %s=%s on the project itself: this run was given the budget once, so it flagged the installation in the same command — no second command, and %s's applies no longer stop for the typed confirmation from the next run on\n",
		what, managed.ProjectLabelKey, managed.ProjectLabelValue, r.projectName)
	return nil
}

// managedReasonFor is what a refused flag write says about what this run read:
// the read's own reason when it gave one, and the state it answered with
// otherwise. It keeps the warning about the write and the state the read
// reported apart, so a sentence that says "not flagged" also says why.
func managedReasonFor(a managed.BudgetAnswer) string {
	if a.Reason != "" {
		return safetext.Field(a.Reason)
	}
	if a.Invalid {
		return "not a creation fact whose recorded amount this build could use"
	}
	return "not an installation whose creation fact records an amount"
}
