// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance_test

// The caps are not governance (D520). What a run stops at is the deployed cap
// and the kill switch, both the core's, and neither is a key of the block that
// moved here: this file pins that the two editions answer them identically, so
// the split cannot quietly have taken a money bound with it.

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/governance"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/policy"
)

// TestCapsUnchangedByTheTag: the per-run cap, the 80% daily warning and the
// kill switch answer the same in a tagged build as in an untagged one (D520).
//
// The three are core values the split never touches: per_run_usd is a policy
// key of its own and not a governance one, and the warning and the refusal are
// internal/budget's, which asks neither hook. The test holds the two apart the
// way the code does — a governance merge cannot move a cap, and the decision
// does not depend on what the merge made of the block.
func TestCapsUnchangedByTheTag(t *testing.T) {
	// The shipped governance resolved (this binary's hooks) and the zero
	// governance an untagged build resolves are the same cap: the key is not
	// in the block, and no governance value can raise or replace it (G6).
	tagged := policy.Merge(policy.Layer{PerRunUSD: 2, Governance: policy.Governance{}.WithDefaults()})
	untagged := policy.Merge(policy.Layer{PerRunUSD: 2})
	if tagged.PerRunUSD != 2 || untagged.PerRunUSD != 2 {
		t.Fatalf("per_run_usd = %v tagged, %v untagged, want the deployed cap in both", tagged.PerRunUSD, untagged.PerRunUSD)
	}
	looser := policy.Merge(
		policy.Layer{PerRunUSD: 2},
		policy.Layer{Governance: policy.Governance{MaxSubagents: 1}},
		policy.Layer{Governance: policy.Governance{MaxSubagents: 9, StageBudget: policy.StageBudget{Implement: 0.99}}})
	if looser.PerRunUSD != 2 {
		t.Errorf("a governance block moved per_run_usd to %v: a governance value is not a cap", looser.PerRunUSD)
	}

	// The 80% warning is one threshold for both editions (WarnShare, D520):
	// below it nothing is said, at it the note names the cap and says the cap
	// keeps applying.
	if got := budget.DailyWarnNote("the repository's", 79, 100); got != "" {
		t.Errorf("79%% of the cap warned: %q", got)
	}
	note := budget.DailyWarnNote("the repository's", 80, 100)
	if !strings.Contains(note, "(80.0%)") || !strings.Contains(note, "the cap keeps applying") {
		t.Errorf("the 80%% note = %q", note)
	}

	// The kill switch refuses identically whatever the governance resolved:
	// it is checked on the run's first lease, before any bound of the block is
	// looked at, and it names the switch's own scope. Evaluate takes no
	// governance and asks no hook, which is the whole of the claim.
	now := time.Now()
	write := budget.Write{Amount: 1, Op: budget.OpLease, Day: budget.Day(now)}
	kills := budget.Kills{Global: &budget.Kill{On: true}}
	d := budget.Evaluate(budget.State{}, write, budget.Caps{}, kills, now)
	if d.Allow || d.Reason != budget.ReasonKillSwitch || d.Scope != budget.ScopeGlobal {
		t.Errorf("the kill switch answered %+v, want the global refusal (D520)", d)
	}
	if repo := budget.Evaluate(budget.State{}, write, budget.Caps{}, budget.Kills{Repo: &budget.Kill{On: true}}, now); repo.Reason != budget.ReasonKillSwitch || repo.Scope != budget.ScopeRepo {
		t.Errorf("the repository's kill switch answered %+v", repo)
	}

	// And a build that enforces governance still stops at the shipped figures
	// rather than at anything the block could raise: the guardrails this
	// package ships are positive and below one, so no stage budget is the cap.
	g := governance.Defaults()
	for _, v := range []float64{g.StageBudget.Implement, g.StageBudget.Review, g.StageBudget.Fix} {
		if !(v > 0 && v < 1) {
			t.Errorf("stage budget %v is not a fraction of the per-run cap (G6)", v)
		}
	}
}
