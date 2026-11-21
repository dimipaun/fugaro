// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestDeclareNoLongerSkipsOnOff: the launch declarer's switch is the database
// and nothing else. It used to read budget.mode: off as a launch told not to
// use the budget database, which would now skip a declaration on a launch that
// has one — there is always a budget and no mode is off any more
// (docs/always-a-budget/contract-mode.md, C1, D3009). A project with no
// budget.rtdb_url still declares nothing, which is the switch that survives
// (TestDeclareAsksNothingWhenTheCoordinatorIsOff), and --no-budget-check is
// still the operator saying it for one launch.
//
// The fixture's local config is rewritten to say the word rather than built
// with it, because that is how such a config reaches a launch after the
// release: the mode a file carries is a word the migration rewrites, and until
// it does, the launch under it declares as it always would have.
func TestDeclareNoLongerSkipsOnOff(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	rewriteBudgetBlock(t, f, "budget: { mode: off, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+f.db.URL+" }\n")
	// The premise, read through the vocabulary rather than through a constant
	// of its own: the config this drives really says the word that was a mode.
	if mode := policy.Mode(f.config(t).BudgetMode()); !mode.Removed() {
		t.Fatalf("the config reads mode %q, want the word that was a budget mode", string(mode))
	}

	spec := &task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"}
	if err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug, spec); err != nil {
		t.Fatalf("a launch that names a database was not declared under a mode of off: %v", err)
	}
	if n := len(f.intents()); n != 1 {
		t.Fatalf("the declaration wrote %d records, want 1: %v", n, f.intents())
	}
	if rec := f.oneIntent(t); rec.DeclaredBy != "someone@example.com" || rec.Branch != "fugaro/x" {
		t.Errorf("the record = %+v, want the task's requester and branch", rec)
	}
}
