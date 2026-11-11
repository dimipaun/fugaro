package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// doctor_build_budget.go holds T4's doctor line about this installation's
// billable image builds: the warning D1548 owes the reader of an installation
// whose over-cap builds are recorded and never refused.

// buildBudgetCheckID is the ID of that line: one state, an observe mode the
// owner chose — the installation no later `fugaro update` raises, and the one
// where no cap ever refuses a build. One ID rather than one per cause, because
// everything the line has to say about that state is one sentence: the lease is
// taken, the day counts what it reserves, and the build runs.
const buildBudgetCheckID = "build-budget"

// Why the line exists at all, and why it lands with T4 rather than with the
// lease it describes. Every image build now takes a lease before it is
// submitted (image_build_lease.go), and in enforce mode a build no cap has room
// for fails with the spec's error. In observe mode the same refusal is recorded
// and the build is submitted anyway (D1548), so on an installation that observes
// the lease counts money for the day and stops nothing — and, unlike a run, an
// image build has no other bound: the per-build confirmation that used to stand
// in front of one is gone (T4), so what a person reads here is the only notice
// they get. Nothing takes a lease before this task wires it into the build path,
// which is why the check arrives with the lease and not before it.
//
// Why it is a check of its own rather than a branch of doctor_budget_mode.go
// (T16). That line is about the mode nobody chose: a seeded observe the next
// `fugaro update` raises, and the caps whose absence keeps the raise from
// landing. It says nothing where the mode is enforce, and nothing where the
// owner chose the mode themselves — an owner's choice is not a finding
// (D1542). This check is about exactly the second of those states, which is the
// one installation where the mode will stay observe for good: the raise never
// comes, so the fact has to be said by doctor or by nobody. The two lines never
// fire together, which is the property worth keeping: this one returns nothing
// for every state doctor_budget_mode.go already reports, and that one returns
// nothing for every state this one reports.
//
// It reads and never writes, like every other check, and a read that did not
// answer is an info line carrying the unread marker rather than a pass: whether
// a cap would refuse a build is unknown, and "unknown" is not "fine".
func doctorBuildBudget(ctx context.Context, db *rtdb.Client) []doctorCheck {
	unread := func(what string, err error) []doctorCheck {
		return []doctorCheck{{ID: buildBudgetCheckID, Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether this installation's budget mode refuses an over-cap image build could not be read (%s: %v)", what, err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	}
	var mode string
	found, err := db.Get(ctx, budget.PathMode, &mode)
	if err != nil {
		return unread(budget.PathMode, err)
	}
	if found && mode == budget.ModeEnforce {
		return nil
	}
	var set string
	setFound, err := db.Get(ctx, budget.PathModeSet, &set)
	if err != nil {
		return unread(budget.PathModeSet, err)
	}
	if !setFound || set != budget.ChosenByOwner {
		// Every other way of being in observe is doctor_budget_mode.go's: a
		// seeded mode is one the next update raises, and the caps the raise
		// waits for are what that line names. Saying it here as well would be
		// one fact printed twice.
		return nil
	}
	// The mode as its reader has to hear it: an absent node is observe (D1548),
	// and it is worth saying that the node is not there, because the repair is
	// a write to it.
	shown := mode
	if !found {
		shown = budget.ModeObserve + " (" + budget.PathMode + " is absent)"
	}
	return []doctorCheck{{ID: buildBudgetCheckID, Severity: "warning",
		Problem: fmt.Sprintf("the project-wide budget mode is %s and its owner chose it, so an image build that is over the project's caps is recorded and submitted anyway: every build takes a lease, the day counts what it reserves, and nothing refuses it — a build started by CI, a script or a coding agent's session, with nobody at the terminal to read the reservation, runs unbounded. Only %s and the project's kill switches bound a build in observe", shown, budget.PathLimits+"/maxReserveMicros"),
		Fix:     "raise the mode with 'fugaro budget set --global --mode " + budget.ModeEnforce + "' once the caps it compares are set ('fugaro budget set --global --daily <usd> --per-run <usd>' and 'fugaro budget set --defaults --daily <usd>'), so a build over the cap fails before it is submitted; leave the mode as it is and watch the day's counters with `fugaro budget show` if recording without refusing is what you want"}}
}
