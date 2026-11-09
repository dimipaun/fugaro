package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// doctor_budget_mode.go holds T16's doctor line about the project-wide budget
// mode, read from the project's budget database with the operator's own
// credentials. It is one line, it names no node a run may write, and it exists
// for the states an installation reaches without anyone deciding anything:
// D1548 makes a mode nobody chose observe, and in observe a lease does not
// bite.
//
// It is a file of its own rather than the line the plan puts in
// doctor_build_budget.go because T4 creates that file: this branch lands first,
// and T4 adding its own check there would meet a file that already exists
// holding someone else's check.
//
// It reads the mode the way the step that raises it does
// (update_budget_mode.go): the two nodes together, because the mode alone
// cannot say whether anyone chose it.

// budgetModeCheckID is doctor's line about the project-wide budget mode: the
// one ID with three states — a seeded observe the next update raises, a mode
// the update cannot raise because the caps enforce needs are missing (D6), and
// nothing at all once the installation enforces or its owner chose the mode.
const budgetModeCheckID = "budget-mode"

// doctorBudgetMode reports the project-wide mode (config/mode) and the record
// of who chose it (config/modeSet), and says nothing in the two states where
// there is nothing to say: an installation that enforces, and one whose mode
// its owner chose — the owner's choice is not a finding, and D1542 makes every
// way of choosing one theirs.
//
// The other two states are one warning each. A seeded observe (config/modeSet
// absent, or stamped by an earlier update) is the state `fugaro update` exists
// for: it raises the mode to enforce on a managed project in the same command.
// Where a cap enforce compares every lease against is missing the raise cannot
// land — the raise requires the same three the runner's Admit does, and the
// third is the daily cap a repository's day counter is compared against, which
// is the repository's own or the default and never the project-wide one
// (update_budget_mode.go) — and the installation is unbounded: nothing bounds
// how many image builds run, which is the state a person has to be told about
// rather than one doctor may leave to the update. This line names the caps on
// the same terms as the step, so a reader who sets what it names is left with
// an installation the next update raises and no run halts.
//
// The state is one every installation can reach and the repair is not: the
// update raises the mode on a managed project alone, so a line whose only fix
// were `fugaro update` could never clear on one that is not managed — the same
// indefinitely-recurring finding doctor exists to avoid. Both fixes therefore
// name the owner's own `fugaro budget set --global --mode enforce` as the step
// that works wherever the installation is, beside the update that does it for
// them on a managed one. The observe state itself is worth the line either way:
// in observe a lease is recorded and does not bite, whatever the project is
// flagged as.
//
// It reads and never writes, like every other check, and a read that did not
// answer is an info line carrying the unread marker rather than a pass: the
// mode is unknown, and "unknown" is not "fine" (doctorClassOf).
func doctorBudgetMode(ctx context.Context, db *rtdb.Client) []doctorCheck {
	unread := func(what string, err error) []doctorCheck {
		return []doctorCheck{{ID: budgetModeCheckID, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the project-wide budget mode could not be read (%s: %v), so whether this installation enforces its caps is unknown", what, err),
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
	if setFound && set == budget.ChosenByOwner {
		return nil
	}
	// The mode as the reader has to hear it: the node is absent on a database
	// whose converge has not seeded it, and an absent mode is observe (D1548),
	// which is the fact the line is about.
	shown := mode
	if !found {
		shown = budget.ModeObserve + " (" + budget.PathMode + " is absent)"
	}
	var caps budget.GlobalCaps
	capsFound, err := db.Get(ctx, budget.PathCapsGlobal, &caps)
	if err != nil {
		return unread(budget.PathCapsGlobal, err)
	}
	var defaults budget.DefaultCaps
	defaultsFound, err := db.Get(ctx, budget.PathCapsDefaults, &defaults)
	if err != nil {
		return unread(budget.PathCapsDefaults, err)
	}
	var missing []string
	if !capsFound || caps.DailyMicros == nil {
		missing = append(missing, "dailyMicros in "+budget.PathCapsGlobal)
	}
	if !capsFound || caps.PerRunMicros == nil {
		missing = append(missing, "perRunMicros in "+budget.PathCapsGlobal)
	}
	if !defaultsFound || defaults.RepoDailyMicros == nil {
		missing = append(missing, "repoDailyMicros in "+budget.PathCapsDefaults+" (the daily cap a repository without one of its own is compared against)")
	}
	if len(missing) > 0 {
		return []doctorCheck{{ID: budgetModeCheckID, Severity: "warning",
			Problem: fmt.Sprintf("the project-wide budget mode is %s, and the caps enforce compares every lease against are not set — %s — so `fugaro update` leaves the mode as it is (it raises a mode only where a lease would still be taken) and this installation is unbounded: nothing bounds how many image builds run", shown, strings.Join(missing, ", ")),
			Fix:     "set them with 'fugaro budget set --global --daily <usd> --per-run <usd>' and 'fugaro budget set --defaults --daily <usd>'; the next `fugaro update` then raises the mode to " + budget.ModeEnforce + " on a managed project, and 'fugaro budget set --global --mode " + budget.ModeEnforce + "' raises it on any installation"}}
	}
	return []doctorCheck{{ID: budgetModeCheckID, Severity: "warning",
		Problem: fmt.Sprintf("the project-wide budget mode is %s and no one chose it: a lease does not bite in observe, so an image build runs unbounded, and `fugaro update` raises the mode to %s on a managed project", shown, budget.ModeEnforce),
		Fix:     "run `fugaro update` on a managed installation, or raise the mode yourself with 'fugaro budget set --global --mode " + budget.ModeEnforce + "'"}}
}
