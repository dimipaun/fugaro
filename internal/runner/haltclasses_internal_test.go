package runner

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// haltFamilies is the family each halt reason is reported under, read from the
// design's §3 rows: the dollar and token caps are halted_budget; the project's
// switch is halted_kill_switch; halted_credential is its own definition, "no
// credential became usable, or the budget database refused", which is what
// budget_unavailable (the backend never answered) and budget_token_expired
// (the run waited past its own budget token) are — their remedy is an account
// that answers again, never a cap raised; and the governance tripwires are
// halted_governance, the slice the checkpoint schema is pinned to.
//
// The two reasons whose names carry "budget" and whose family is not the
// budget one are the design's row itself, not a reading of it:
// halted_credential is "no credential became usable, or the budget database
// refused", and it names budget_unavailable and budget_token_expired
// (`docs/design/failure-classes.md:167`). F17 corrects only that row's line
// citation — the four names span `runstore.go:54`–`71`, so the row's `:55–71`
// starts one line late — and does not move the reason out of the family.
//
// It is the expected side of the walk below, written out from the design
// rather than read off the switch it is compared with, and it is a function
// rather than a package-level map so that a test that mutates what it is
// handed cannot reach the next one. The two walks over it — the mapping itself
// and the record a halt leaves — read the one table, so neither can drift from
// the other. It cannot catch a family moved in both this table and the switch:
// what holds the value itself is D1455's agreement with the reader, whose half
// is FC5's (below).
func haltFamilies() map[runstore.HaltReason]failure.Class {
	return map[runstore.HaltReason]failure.Class{
		runstore.HaltKillSwitch:         failure.HaltedKillSwitch,
		runstore.HaltRunCap:             failure.HaltedBudget,
		runstore.HaltRepoDailyCap:       failure.HaltedBudget,
		runstore.HaltGlobalDailyCap:     failure.HaltedBudget,
		runstore.HaltNoCap:              failure.HaltedBudget,
		runstore.HaltTokenCap:           failure.HaltedBudget,
		runstore.HaltBudgetUnavailable:  failure.HaltedCredential,
		runstore.HaltBudgetTokenExpired: failure.HaltedCredential,
		runstore.HaltNoCredential:       failure.HaltedCredential,
		runstore.HaltCredsRulesDenied:   failure.HaltedCredential,
		runstore.HaltStageBudget:        failure.HaltedGovernance,
		runstore.HaltReviewRatio:        failure.HaltedGovernance,
		runstore.HaltFanout:             failure.HaltedGovernance,
		runstore.HaltReviewScope:        failure.HaltedGovernance,
		runstore.HaltExpectedExceeded:   failure.HaltedGovernance,
		// The owner-decision stop is the one halt with no family of its own in
		// the design's §3: it is deliberately not a governance tripwire, so no
		// checkpoint is written and its row is not that one (OD1), and what
		// comes next is a person rather than the run — which is what
		// unclassified says, and the whole vocabulary says it: the terminal has
		// no class of its own, the report prints its count, and that count is
		// the signal to name one. It is written down here because the walk is
		// over the closed set, where a reason with no line is a family nobody
		// chose.
		runstore.HaltOwnerDecision: failure.Unclassified,
	}
}

// TestHaltReasonsMapToTheirFamily: the writer's halt table, reason by reason,
// over the whole closed set — every value runstore declares, and not the
// fifteen that happen to be there today.
//
// The class is durable (the report counts runs by it and nothing rewrites it),
// so a reason left on the wrong family is a figure a reader acts on with the
// wrong remedy: halted_budget's is "once the cap is raised", halted_
// credential's "once the account is usable". The walk covers the closed set in
// both directions, so a reason added without a family and a family kept for a
// reason runstore no longer declares are both red.
//
// D1455's agreement test is the same walk read against the reader's mapping,
// and it cannot be written from here: the reader is internal/runview's ClassOf
// (FC5), which does not exist yet, and its half of the agreement is asserted
// there — TestLegacyClassFromReasonTable reads a legacy halt's reason through
// the same families this table states. What this file owes that one is the
// table itself, which is the one home of the mapping on the writer's side.
func TestHaltReasonsMapToTheirFamily(t *testing.T) {
	families := haltFamilies()
	reasons := haltReasonConstants(t)
	if got := len(reasons); got != len(families) {
		t.Errorf("runstore declares %d halt reasons and this table places %d: a reason with no family is classed by nothing, and the count is how the two are held together", got, len(families))
	}
	for _, reason := range reasons {
		class, ok := families[reason]
		if !ok {
			t.Errorf("runstore's %q has no family here: a new reason is a class a report prints and a reader acts on, so it is placed deliberately and not left to the switch's default", reason)
			continue
		}
		t.Run(string(reason), func(t *testing.T) {
			if got := haltClass(reason); got != class {
				t.Errorf("haltClass(%q) is %q, want %q: the reason is a name, the class is the cause's family and the remedy the report prints for it", reason, got, class)
			}
		})
	}
	for reason := range families {
		if !slices.Contains(reasons, reason) {
			t.Errorf("this table places a family for %q, which runstore no longer declares: a family for a reason no run can hold is a rule about a value that does not exist", reason)
		}
	}
}
