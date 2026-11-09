package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_budget_mode.go is D1548's consequence and T6(b): on a managed project
// `fugaro update` raises the project-wide budget mode to enforce, unless the
// installation's owner chose a mode of their own.
//
// Why it is this step's and not a command's: an absent mode is observe
// (D1548), and in observe a lease does not bite, so a default installation's
// nightly catalog-check lease (D1554) and every image build are unbounded. The
// HARD RULE says the upgrade is one command — `fugaro update` — so the raise
// happens here, at the end of the converge, in the run that would otherwise
// leave the installation observing nothing.
//
// The trigger is the RECORD of an owner's choice and never the mode's absence:
// the installation's own converge seeds `config/mode` = observe and keeps it
// ("changed only on request", internal/infra/firebasedb.go), so on every
// installation that could take a build lease the node is present and a step
// that fired on an absent mode would never fire. The writers beside the mode
// are what tell the two apart: `--budget-mode` on setup/update and `fugaro
// budget set --global --mode …` record `config/modeSet` = owner, the seed
// records nothing, and an update that raises stamps `config/modeSet` = update.
// A mode the owner sets afterwards is recorded as theirs and no later update
// touches it, ever (D1542: every way of choosing a mode is the owner's).
//
// Nothing here may fail the update. Every state that leaves the mode as it is
// — a project that is not managed, a database that is not this installation's,
// a read that did not answer, the caps being missing so that enforce would
// halt every run (D6) — is a warning or silence, and the mode is exactly what
// the run found. What each of those states means is said by the line that
// reports it, and `fugaro doctor` has a check of its own for the installation
// left in observe (doctor_budget_mode.go).
func (e *initEngine) raiseBudgetMode(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first: this is the upgrade's step, and the hook both doors
	// share is the end of the converge (init_stages.go's outcome, beside
	// D1553's self-flag). `fugaro setup` shows a plan and creates an
	// installation; the mode of one that exists is not its business.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// A run that never opened the cloud, or a project with no shared budget
	// backend at all, has no mode to raise: there is no node, nothing to
	// enforce, and the line about that installation is doctor's
	// (policy-published). The command that gives it a backend is
	// `fugaro update --firebase-project`, and this step runs at the end of
	// that very run.
	if e.c == nil || e.lc == nil || e.lc.Budget == nil || e.lc.Budget.RTDBURL == "" {
		return
	}
	// Whether this is a managed project, from the read this run already made.
	// The converge reads the two marks once for the whole run (update_managed
	// .go's gateRun) and its answer is the one every step of it uses; a run
	// whose own arguments shape the installation has no gate at all, and
	// --firebase-project is exactly that run — the one that deploys the
	// database this step writes into — so it asks the same fact itself,
	// through the engine's cached read (managedProject). A project that is not
	// managed keeps its mode exactly as it keeps every other confirmation:
	// D1553's flag is what makes such an installation managed, never this step.
	managed := r.gateUnderway()
	if r.gate == nil {
		var err error
		if managed, err = r.managedProject(ctx, e.c); err != nil {
			r.warn(fmt.Sprintf("the budget mode was left as it is: whether %s is managed could not be read (%s), and this step raises the mode on a managed project alone", safetext.Field(r.gcpProject), safetext.FieldErr(err)))
			return
		}
	}
	if !managed {
		return
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("the budget mode was left as it is: the budget database could not be opened (%s)", safetext.FieldErr(err)))
		return
	}
	// The one ownership question every writer into that database asks, before
	// anything is read out of it and before anything is written into it
	// (budgetDBUnadopted): a database that is not this installation's is not
	// this step's to change, whatever it holds.
	// Both of the predicate's answers are the same one here — the mode is left
	// as it is — so the string form is the whole of it and the error, which a
	// command turns into an exit code, is not: nothing in an update may fail
	// over this step.
	if why := budgetDBUnadopted(ctx, e.lc, infra.NewDB(db, e.lc.Name, e.lc.GCPProject, ""), "the budget mode was left as it is", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	var cur string
	modeEtag, modeFound, err := db.GetETag(ctx, budget.PathMode, &cur)
	if err != nil {
		r.warn(fmt.Sprintf("the budget mode was left as it is: %s could not be read (%s)", budget.PathMode, safetext.FieldErr(err)))
		return
	}
	// D1548 again, in the one place the raise itself needs it: an absent mode
	// behaves as observe, and an installation whose mode node is absent is one
	// the raise is for as much as a seeded observe is.
	if !modeFound {
		cur = budget.ModeObserve
	}
	var set string
	setEtag, setFound, err := db.GetETag(ctx, budget.PathModeSet, &set)
	if err != nil {
		r.warn(fmt.Sprintf("the budget mode was left as it is: %s could not be read (%s)", budget.PathModeSet, safetext.FieldErr(err)))
		return
	}
	// An owner's own choice, left alone ever after — and the owner is the only
	// writer that records one, so this is the one state in which a mode that
	// is not enforce stays what it is.
	if setFound && set == budget.ChosenByOwner {
		return
	}
	// Nothing to raise. The second run over this installation reaches here,
	// which is what makes the step a no-op after the raise: it neither writes
	// nor says anything about the mode.
	if cur == budget.ModeEnforce {
		return
	}
	// Enforce needs both project-wide caps (D6, RequireCapsForEnforce's
	// refusal): an absent daily cap is a refusal in the rules, so every lease
	// is refused and every run halts, and without the per-run cap a run whose
	// repository has no cap of its own and no default has none to compare
	// against. Where they are missing the step sets no mode and says which are
	// missing and that the installation is unbounded — the caps have no writer
	// here, they are `fugaro budget set --global`'s, and doctor says the same
	// line (doctor_budget_mode.go).
	var caps budget.GlobalCaps
	capsFound, err := db.Get(ctx, budget.PathCapsGlobal, &caps)
	if err != nil {
		r.warn(fmt.Sprintf("the budget mode was left as it is: %s could not be read (%s), and enforce on a database whose caps are unknown would halt every run", budget.PathCapsGlobal, safetext.FieldErr(err)))
		return
	}
	var missing []string
	if !capsFound || caps.DailyMicros == nil {
		missing = append(missing, "dailyMicros")
	}
	if !capsFound || caps.PerRunMicros == nil {
		missing = append(missing, "perRunMicros")
	}
	if len(missing) > 0 {
		r.warn(fmt.Sprintf("the budget mode stays %s on this managed project: enforce needs both project-wide caps (%s: %s are not set, and neither setup nor update seeds them), and the installation is unbounded until they are — nothing bounds how many image builds run. Set them with `fugaro budget set --global --daily <usd> --per-run <usd>`; the next `fugaro update` raises the mode itself",
			cur, budget.PathCapsGlobal, strings.Join(missing, " and ")))
		return
	}
	// The mode first and the record after it, because the two halves fail in
	// opposite directions: a mode raised with the record missing leaves an
	// installation the next update finds already enforcing and says nothing
	// about, while a record written first would mark the owner's own choice
	// beside a mode the raise then failed to change — and no later update
	// would ever raise it.
	if err := db.PutIfMatch(ctx, budget.PathMode, modeEtag, budget.ModeEnforce); err != nil {
		r.warn(fmt.Sprintf("the budget mode was left as it is: writing %s failed (%s)", budget.PathMode, safetext.FieldErr(err)))
		return
	}
	if err := db.PutIfMatch(ctx, budget.PathModeSet, setEtag, budget.ChosenByUpdate); err != nil {
		r.warn(fmt.Sprintf("the budget mode is %s, but %s could not be written (%s): the next update finds the mode already raised and writes nothing, and `fugaro budget set --global --mode …` records the owner's own choice beside it",
			budget.ModeEnforce, budget.PathModeSet, safetext.FieldErr(err)))
		return
	}
	fmt.Fprintf(r.w, "budget mode: %s -> %s (managed project): the project-wide caps now bound every run, and an image build leases its estimated cost before it is submitted. A mode you choose yourself — `fugaro budget set --global --mode observe` — is recorded as yours, and no later update touches it.\n", cur, budget.ModeEnforce)
	return
}
