package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The image-build half of `fugaro doctor` (D1548, plan T4): whether a billable
// build this project starts is refused when the project's caps have no room for
// it.
//
// Why doctor has something to say here at all, and what changed under it. A
// build used to be bounded by a count the owner's own paths recorded in the
// project's creation fact (D1013), and before the no-prompt policy a person
// typed the project's name at the terminal for each one. Both are gone: the
// count is removed (D1543) and no step of `image build` or `image refresh` asks
// anyone anything (D1540, T4), so what bounds a build is the lease its
// estimated cost is taken under immediately before the submission
// (image_cost.go, T8). That lease meets the same predicate a run's does, and
// the predicate follows the database's mode for its cap comparisons: in
// enforce a build over a cap is refused and exits non-zero, and in observe the
// same refusal comes back as an advisory the build proceeds under. An absent
// mode behaves as observe (internal/budget/model.go's Caps), and `fugaro
// update` seeds that node at install, so the state this check reports is the
// ordinary state of an installation nobody has raised.
//
// Why the check reads the database rather than this machine's config. The mode
// that decides the comparison is /config/mode — localcfg's budget.mode decides
// only whether this CLI engages the backend at all (image_cost.go's
// buildLeases) — and a doctor run in one checkout answers about the project,
// not about itself, exactly as its other database checks do (doctorRulesSkew,
// doctorPolicyChecks).
//
// Why a warning and not a failure. What it describes is a mode an owner may
// have chosen on purpose: with the kill switches to hand, observe is how a
// project watches its spend before it caps it, and every other mode-dependent
// check in doctor reads the same way. It is a warning because the state is
// worth being read: in observe the reservation still counts the day and
// maxReserveMicros still refuses a lease above it, but no cap stops a build and
// nothing bounds how many are started.
//
// Nothing is checked for an installation that reaches no database
// (budget.rtdb_url unset, or a local mode of off): there is no lease and no
// mode, which doctorPolicyChecks' own line already says for the first, and the
// second is this machine's own choice about a backend it does not use.
func doctorBuildBudget(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "build-budget-mode"
	if lc.Budget == nil || lc.Budget.RTDBURL == "" || lc.BudgetMode() == localcfg.BudgetOff {
		return nil
	}
	// The same client the policy and rules checks use, over the same URL: a
	// database that cannot be reached is one state for all three, and a
	// check that opened its own would report it three times with three
	// wordings.
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: fmt.Sprintf("whether this project refuses an over-cap image build could not be checked: %v", err),
			Fix:     "fix budget.rtdb_url in the project's config, then run doctor again"}}
	}
	var mode string
	found, err := db.Get(ctx, budget.PathMode, &mode)
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: fmt.Sprintf("whether this project refuses an over-cap image build could not be checked: reading /%s: %v", budget.PathMode, err),
			Fix:     "rerun fugaro doctor with access to the project's Firebase database, or as an operator"}}
	}
	if found && mode == budget.ModeEnforce {
		return []doctorCheck{{ID: id, OK: true}}
	}
	// The node's own value, on one line, as every other check prints a fact a
	// project answered: absent and empty are the two states the failed read
	// above did not cover, and both act as observe.
	shown := mode
	if !found || mode == "" {
		shown = "not set"
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("the budget mode of %s is %s, so an image build whose estimated cost the project's caps have no room for is recorded and submitted anyway: every billable build takes a lease of its estimate before its submission, and in observe that lease's cap refusal is an advisory rather than a stop (D1548). Nothing else bounds these builds — the count this project used to record is gone (D1543), and no step of image build or image refresh asks a person anything — so only limits.maxReserveMicros and the project's kill switches refuse one, and nothing bounds how many run", lc.GCPProject, shown),
		Fix:     fmt.Sprintf("fugaro budget set --global --daily <usd> --per-run <usd> --mode enforce in %s raises the mode, together with the project-wide caps it needs; to keep observe instead, fugaro budget kill halts the project's builds until you resume them", lc.GCPProject)}}
}
