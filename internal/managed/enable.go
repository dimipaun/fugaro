package managed

import (
	"errors"
	"fmt"
)

// The apply gate: the one value every path into the managed apply reads first,
// and the one Task 4.4 opens.
//
// Why it exists (the plan's Task 3.1, "The apply is inert until phase 4
// lands"): Task 4.1 (the whole-project inventory and its fallback to
// confirmations) and Task 4.2 (the budget amount, required at creation) are in
// a later phase, and an apply that shipped with phase 3 would run in a managed
// project with no inventory fallback and no budget — two of the spec's own
// safeguards. So the apply was built, tested and unreachable: every path into
// it passes through Request.Enable, whose zero value is closed.
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
// Budget's zero value is closed, Enable's zero value is closed, and the only
// thing that sets Read is ApplyGate's acceptance of a read that answered with
// an amount.
type Enable struct {
	// Budget is the amount and the currency the apply-time read answered with
	// (Task 4.4). It is what the read of the creation fact recorded, and it is
	// carried here rather than left to the caller because the two fields are
	// the pair the gate is open on and nothing else may be substituted for
	// them.
	Budget Budget
	// Read is whether an apply-time budget read answered with that pair. The
	// gate is open exactly when it is true, and the zero value is false, so a
	// caller that forgets to thread a read through is refused rather than
	// admitted.
	Read bool
}

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
	if e.Read && e.Budget.Valid() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotEnabled, NotEnabled)
}
