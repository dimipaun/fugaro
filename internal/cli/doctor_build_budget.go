package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// doctorBuildBudgetID is the check's ID. It names the state, not the command
// that changes it: the reader arrives from a night of builds, not from a
// manual.
const doctorBuildBudgetID = "build-budget-observe"

// doctorBuildBudgetCheck is D1548's line: what the mode costs image builds,
// said while it is still cheap to change.
//
// Image builds lease their estimated cost the way runs do, and the lease is
// judged by the same predicate the database rules are generated from — so in
// observe mode a build over a cap is recorded and proceeds, and only the
// reserve limit and the kill switches stop it (internal/budget/model.go:
// "anything else, absent included, behaves as observe"). What makes that
// worth a line of its own is that this installation's builds run unattended:
// the daily image check job and the project's catalog check job submit Cloud
// Builds with nobody at the terminal, so a rebuild storm is bounded by the
// caps only when the mode enforces. `fugaro update` sets the mode to enforce
// on a managed project (reconciliation T6), which leaves this check reporting
// an installation that has chosen observe, or one whose caps were never set
// and so could not be raised into it (D6, RequireCapsForEnforce).
//
// It reads the mode node and nothing else, and every other answer is a state
// rather than a failure: a node that could not be read is an info note (the
// shape the rest of doctor has for a read that did not answer), and a node
// holding something that is not the string enforce is the observe state with
// the value printed, because that value is what the reader has to see to fix
// it. Nothing here writes.
func doctorBuildBudgetCheck(ctx context.Context, db *rtdb.Client) *doctorCheck {
	var raw json.RawMessage
	found, err := db.Get(ctx, budget.PathMode, &raw)
	if err != nil {
		return &doctorCheck{ID: doctorBuildBudgetID, Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether the budget stops an image build could not be checked: reading /%s: %v", budget.PathMode, err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}
	}
	// The value is read as a string where it is one and printed as written
	// where it is not: only enforce enforces, so anything else is the state
	// below, and a node nobody can point at is a node nobody can fix.
	mode := string(raw)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		mode = s
	}
	if found && mode == budget.ModeEnforce {
		return nil
	}
	var state string
	switch {
	case !found:
		state = "/" + budget.PathMode + " is not set, and an unset mode behaves as observe"
	case mode == budget.ModeObserve:
		state = "/" + budget.PathMode + " is observe"
	case mode == "":
		state = "/" + budget.PathMode + " is empty, and an empty mode behaves as observe"
	default:
		state = "/" + budget.PathMode + " is " + oneLine(mode) + ", and anything but enforce behaves as observe"
	}
	return &doctorCheck{ID: doctorBuildBudgetID, Severity: "warning",
		Problem: state + ": an image build that meets a cap is recorded and never refused, and this installation's builds run unattended — the daily image check job and the project's catalog check job submit builds with nobody at the terminal — so only the reserve limit (maxReserveMicros) and the kill switches bite",
		Fix:     "run `fugaro budget set --global --mode enforce` to make the caps refuse rather than advise; if the project's global caps were never set, set them in the same command (fugaro budget set --global --daily <usd> --per-run <usd> --mode enforce), because enforce is refused without them"}
}
