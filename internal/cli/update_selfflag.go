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

// update_selfflag.go is D1553's second half, and the HARD RULE's: `fugaro
// update` on an installation that is not managed flags it itself when it is
// given its budget once, so the upgrade stays one command and the owner runs no
// second one. `fugaro managed flag` remains the owner's own command — it is
// what a project unmanaged for a reason update cannot fix is flagged with, and
// what the unmanaged refusal of every other mutating command names (D1541) —
// but it is not a step the upgrade needs.
//
// Three things decide what the step does, and each is one of the answer's own:
//
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
//     LabelForFlag), because the read requires both marks.
//   - It refuses in a coding agent's session, even when --budget was passed:
//     what it writes is the marks that take the installation's typed
//     confirmation away, so consent given by a flag is only as strong as the
//     markers that identify the session (F20). The refusal names the marker and
//     the command the owner runs at their own terminal, which is the same one
//     this step would have run for them.
//
// The fence stands where the write would be, and not at the head of the step:
// a run whose installation already records an amount flags nothing, so an
// agent's session that passes its budget to an up-to-date installation meets no
// fence at all — refusing there would refuse an ordinary update for a write
// that was never going to happen. What it reads before the fence is the same
// read the apply-time gate makes, and reading writes nothing.
//
// Nothing here can make a `fugaro update` fail: a write that did not happen is
// a warning and the run's outcome is unchanged (the unmarked project is the
// fail-closed direction, and the update itself succeeded). The one exception is
// the agent refusal above, which is the run's outcome because it is the fence
// F20 describes, and the exit code is a user error like every other refusal.
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
	number, err := infra.ProjectNumber(ctx, e.c, r.gcpProject)
	if err != nil {
		r.warn(fmt.Sprintf("this run was given a budget and %s records no amount, but its project number could not be read (%v), and the number is what proves a runs bucket is this project's own: nothing was flagged", r.gcpProject, safetext.Field(err.Error())))
		return nil
	}
	budget := managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}
	// The flag command's own writers, in the flag command's own order: the
	// object is the authority and goes first, and the label is the narrowing
	// mark that completes it. Both are what the read requires, so one without
	// the other leaves the installation exactly as unmanaged as it was.
	replaced, err := managed.WriteFlag(ctx, e.c.Storage, bucket, r.gcpProject, number, budget)
	if err != nil {
		r.warn(fmt.Sprintf("this run was given a budget and %s is %s, so it was not flagged (%v): the installation stays as it is, and nothing of the update above is undone. `fugaro managed flag %s --budget N --budget-currency CCC`, run by its owner at their own terminal, is the command that does it",
			r.gcpProject, managedReasonFor(answer), safetext.FieldErr(err), r.gcpProject))
		return nil
	}
	if err := managed.WriteManagedLabelForFlag(ctx, e.c.CRM, r.gcpProject); err != nil {
		r.warn(fmt.Sprintf("this run wrote gs://%s/%s but not the %s=%s label on %s (%v): the read requires both marks, so the installation is not managed yet and nothing is skipped for it. The label is written on the account that owns the project, at the owner's own terminal, and docs/design/managed-projects.md has that one line",
			bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, r.gcpProject, safetext.FieldErr(err)))
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
	what := fmt.Sprintf("wrote gs://%s/%s: %s is a managed project", bucket, managed.MarkerObject, r.projectName)
	if replaced {
		what = fmt.Sprintf("replaced the creation fact in gs://%s/%s, which recorded no budget amount, with the version that records %d %s", bucket, managed.MarkerObject, o.budget, safetext.Field(o.budgetCurrency))
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
