package managed

import (
	"errors"
	"fmt"
)

// The config gate the managed apply ships behind: it is refused by config
// until phase 4 lands, and Task 4.4 is the only task that opens it.
//
// Why it exists (the plan's Task 3.1, "The apply is inert until phase 4
// lands"): Task 4.1 (the whole-project inventory and its fallback to
// confirmations) and Task 4.2 (the budget amount, required at creation) are in
// a later phase, and an apply that shipped with phase 3 would run in a managed
// project with no inventory fallback and no budget — two of the spec's own
// safeguards. So the apply is built, tested and unreachable: every path into
// it passes through Request.Enable, whose zero value is closed.
//
// It is a value in the build and NOT a flag or an environment variable, which
// is the whole of the shape: the session that applies is a coding agent's, and
// the refusal it removes is a mitigation against an agent going through the
// program. A gate such a session could open on its own command line would
// remove nothing. Task 4.4 opens it by changing Shipped, in this file.
//
// Enable is exported so Task 4.4's caller can pass the open value and so
// internal/cli's command has one thing to thread through; the zero value is
// the closed one, so a caller that forgets is refused rather than admitted.
type Enable struct {
	// Open is whether this build enables the managed apply. It is false in
	// every path of this release: Shipped is the only value internal/cli
	// passes, and Task 4.4 is the task that changes it.
	Open bool
}

// ErrNotEnabled is the refusal of an apply under a closed gate. It is a value
// as well as a message, so a test can match on it without reading English.
var ErrNotEnabled = errors.New("the managed apply is not enabled")

// NotEnabled is the refusal's own words, fixed by the plan: the sentence names
// the three things that are not in this release, because a reader who is told
// "not enabled" and nothing else would look for a flag to pass.
const NotEnabled = "the managed apply is not enabled in this release: the whole-project inventory (Task 4.1), the required budget (Task 4.2) and the apply-time budget read (Task 4.4) are not in it"

// Shipped is the gate as this release ships it: closed. Task 4.4 replaces this
// value (and nothing else in this file) when the three safeguards are in the
// build, which is why the gate is a function and not a constant: the one place
// that decides is named, and a test asserts what it returns today.
func Shipped() Enable { return Enable{} }

// Refuse is the gate's whole behaviour: nil when the apply is enabled, and the
// one refusal of the plan's own words when it is not.
func (e Enable) Refuse() error {
	if e.Open {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotEnabled, NotEnabled)
}
