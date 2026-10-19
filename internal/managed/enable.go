package managed

import (
	"errors"
	"fmt"
)

// The apply gate: the one value every path into the managed apply reads first,
// and the one Task 4.4 opens.
//
// Why it exists (the plan's Task 3.1, "The apply is inert until phase 4
// lands"): an apply that shipped with phase 3 would have run in a managed
// project with no recorded budget. So the apply was built, tested and
// unreachable: every path into it passes through Request.Enable, whose zero
// value is closed.
//
// What it is opened WITH, as of Task 4.5 (the plan's wiring of the
// whole-project inventory, in 0.9.0 as the owner's ruling of 2026-10-18 set;
// the redesigned managed path that wiring belongs to moved to 0.9.5 by the
// owner's ruling of 2026-10-19, and 0.9.0 ships with the managed deployer's
// scoped role still gated off): the managed apply now also consults the
// whole-project inventory
// (inventory.go, Task 4.1), so a resource the installation's own state does
// not account for falls that run back to its confirmations and the gate's
// opening buys an agent's session nothing there (D55, D59). The ruling's own
// record — in 0.8.5 the inventory was a library only, and the gate opened
// without it — is kept where it belongs, in the plan's Task 4.5 and the
// design's D59 row. The gate here is still about the budget and nothing else:
// the inventory's answer is not an input to Enable, it is read by the command
// and handed to Apply beside this gate's own answer.
//
// Task 4.4 is the task that opens it, and it opens it in exactly one way: the
// apply-time budget read of internal/managed/budget.go succeeded. The gate is
// therefore no longer a value the release chooses — it is the *answer* of a
// read of the project's own creation fact, which is what makes it the
// privilege boundary the plan's Task 4.4 says the commit is about. A managed
// project with no recorded budget, an unreadable marker, a marker of a version
// this build does not know and a marker naming another project all leave the
// gate closed.
//
// It is a value in the build and NOT a flag or an environment variable, which
// is the whole of the shape: the session that applies is a coding agent's, and
// the refusal it removes is a mitigation against an agent going through the
// program. A gate such a session could open on its own command line would
// remove nothing. There is no longer any constant in this file that opens it:
// Budget's zero value is closed, Enable's zero value is closed, and the
// fields are unexported, so outside this package the only way to hold an open
// Enable is to be handed one by ApplyGate's acceptance of a read that answered
// with a valid amount — another package can copy a gate or hold the zero
// value, and cannot build an open one. (Inside the package, Refuse still
// re-checks the pair with the marker's own rule.)
type Enable struct {
	// budget is the amount and the currency the apply-time read answered with
	// (Task 4.4). It is what the read of the creation fact recorded, and it is
	// carried here rather than left to the caller because the two fields are
	// the pair the gate is open on and nothing else may be substituted for
	// them.
	budget Budget
	// read is whether an apply-time budget read answered with that pair. The
	// gate is open exactly when it is true and the pair is valid, and the zero
	// value is false, so a caller that forgets to thread a read through is
	// refused rather than admitted.
	read bool
}

// Budget is the pair the gate was opened on: the zero Budget for a closed
// gate.
func (e Enable) Budget() Budget { return e.budget }

// Open reports whether an apply-time budget read opened the gate — the same
// answer Refuse gives, as a boolean.
func (e Enable) Open() bool { return e.Refuse() == nil }

// ErrNotEnabled is the refusal of an apply under a closed gate. It is a value
// as well as a message, so a test can match on it without reading English.
var ErrNotEnabled = errors.New("the managed apply is not enabled")

// NotEnabled is the refusal of a gate that no read opened. Task 3.1 shipped
// this sentence naming the three safeguards that were missing from the release
// ("the whole-project inventory (Task 4.1), the required budget (Task 4.2) and
// the apply-time budget read (Task 4.4) are not in it"); Task 4.4 supersedes it
// because those three are now in the build, and what is left for a closed gate
// to mean is that nothing was read at all. The sentence a reader acts on is
// not this one — it is the read's own refusal, built by ApplyGate and returned
// by the caller before any of this is reached.
const NotEnabled = "the managed apply was refused: no apply-time budget read opened it, and an apply with no recorded budget amount is never made"

// Refuse is the gate's whole behaviour: nil when an apply-time budget read
// opened it, and the one refusal when it is not. It is the FIRST thing Apply
// reads, so an Enable that was never built by ApplyGate refuses however it is
// called.
func (e Enable) Refuse() error {
	if e.read && e.budget.Valid() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotEnabled, NotEnabled)
}
