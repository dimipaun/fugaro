package runstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestModelUSDSumInvariantMixedRun (C1.16, D140, P12): a run whose coder stage
// went through the gateway -- some of its calls on the subscription (tier A,
// notional) and some paid (tier B, real) -- and whose reviewer stage ran on
// oauth, where Claude Code's own total is the figure and all of it is
// notional. Three things must hold together for such a record:
//
//   - the documented sum invariant, exactly: every stage's model_usd plus
//     model_by's "unattributed" is cost.model_usd, which is why ModelUSD keeps
//     its meaning and ModelNotionalUSD is a statement about it rather than a
//     subtraction from it;
//   - total_usd is billed dollars: the notional part is not in it, although
//     the run's model_basis is api-list (the billed part is the gateway's),
//     which is exactly why a reader may not split on the basis;
//   - the stage's own split is where the notional figure came from, and it
//     never exceeds the run's.
func TestModelUSDSumInvariantMixedRun(t *testing.T) {
	const (
		coderBilled     = 3.00 // tier B, settled by the gateway
		coderNotional   = 1.00 // the coder's tier-A calls, at list rates
		reviewerTotal   = 2.00 // Claude Code's own figure, entirely notional
		unattributed    = 0.25 // gateway spend that settled after its stage's wait
		compute         = 0.38
		wantModelUSD    = coderBilled + coderNotional + reviewerTotal + unattributed
		wantNotionalUSD = coderNotional + reviewerTotal
		wantTotalUSD    = compute + coderBilled + unattributed
	)
	cost := NewCost(wantModelUSD, compute, BasisAPIList).WithNotional(wantNotionalUSD)
	rec := Record{
		Version: 1, RunID: "20261022-120000-abcd", Repo: "acme/app", Status: StatusRunning, Stage: "review",
		CostUSD: wantModelUSD, Cost: &cost,
		Stages: []StageTiming{
			{Name: "implement", StartedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), DurationS: 61,
				ModelUSD: coderBilled + coderNotional, ModelNotionalUSD: coderNotional, Model: "claude-sonnet-5-5"},
			{Name: "review", StartedAt: time.Date(2026, 10, 22, 12, 1, 0, 0, time.UTC), DurationS: 42,
				ModelUSD: reviewerTotal, ModelNotionalUSD: reviewerTotal},
		},
	}
	cost.ModelSource = "gateway"
	cost.ModelBy = map[string]float64{"claude-sonnet-5-5": coderBilled + coderNotional + reviewerTotal, "unattributed": unattributed}

	var stageSum, stageNotional float64
	for _, s := range rec.Stages {
		stageSum += s.ModelUSD
		stageNotional += s.ModelNotionalUSD
		if s.ModelNotionalUSD > s.ModelUSD {
			t.Fatalf("stage %s is more notional (%v) than its figure (%v)", s.Name, s.ModelNotionalUSD, s.ModelUSD)
		}
	}
	if got := stageSum + rec.Cost.ModelBy["unattributed"]; got != rec.Cost.ModelUSD || got != wantModelUSD {
		t.Fatalf("sum(stages) + unattributed = %v, model_usd = %v, want %v", got, rec.Cost.ModelUSD, wantModelUSD)
	}
	if rec.Cost.ModelNotionalUSD != wantNotionalUSD {
		t.Fatalf("model_notional_usd = %v, want %v", rec.Cost.ModelNotionalUSD, wantNotionalUSD)
	}
	if stageNotional != rec.Cost.ModelNotionalUSD {
		t.Fatalf("the stages' notional figures sum to %v, the run's to %v", stageNotional, rec.Cost.ModelNotionalUSD)
	}
	if rec.Cost.ModelBasis != BasisAPIList {
		t.Fatalf("a mixed run's basis = %q, want the billed part's %q", rec.Cost.ModelBasis, BasisAPIList)
	}
	// The point of the field: the run's basis says nothing about which of its
	// dollars were billed. Splitting on it would bill the reviewer's $2 as if
	// the gateway had spent it.
	if rec.Cost.TotalUSD != wantTotalUSD {
		t.Fatalf("total_usd = %v, want %v (compute + the billed model dollars)", rec.Cost.TotalUSD, wantTotalUSD)
	}
	if rec.Cost.TotalUSD == wantModelUSD+compute {
		t.Fatal("total_usd billed the notional part: this is the run the field exists for")
	}

	// A subscription run is the same rule at its extreme: every model dollar
	// is notional, and the total was already excluding it.
	sub := NewCost(wantModelUSD, compute, BasisSubscription).WithNotional(wantModelUSD)
	if sub.TotalUSD != compute || sub.ModelNotionalUSD != wantModelUSD {
		t.Fatalf("subscription = %+v", sub)
	}

	// The constructor's own figure rounds to the cent, and so does the
	// recomputation: a run priced 1/3 of a cent over must not read
	// differently to a reader before and after the notional part is known.
	if got := NewCost(0.005, 0, BasisAPIList); got.TotalUSD != 0.01 {
		t.Fatalf("the constructor's rounding changed: %v", got.TotalUSD)
	}
	if got := NewCost(0.015, 0, BasisAPIList).WithNotional(0.005); got.TotalUSD != 0.01 {
		t.Fatalf("WithNotional stopped rounding to the cent: %+v", got)
	}
	// A notional figure the cost cannot hold is clamped into it, not billed
	// as a negative total (see WithNotional).
	over := NewCost(1, 0.5, BasisAPIList).WithNotional(2)
	if over.ModelNotionalUSD != 1 || over.TotalUSD != 0.5 {
		t.Fatalf("an over-large notional part = %+v", over)
	}
	if neg := NewCost(1, 0.5, BasisAPIList).WithNotional(-1); neg.ModelNotionalUSD != 0 || neg.TotalUSD != 1.5 {
		t.Fatalf("a negative notional part = %+v", neg)
	}
}

// TestSwitchLogInTheRecord (C1.16, D140): the switch log is the credentials
// service's own record of a credential change -- from, to and the reason word
// -- beside the one figure only the gateway can compute, the estimated
// cache-miss cost (F24). A miss the tier's behaviour does not measure is
// recorded as unmeasured and not as $0: the two mean different things to a
// reader, and one of them is a lie.
func TestSwitchLogInTheRecord(t *testing.T) {
	rec := Record{Version: 1, RunID: "20261022-120000-abcd", Repo: "acme/app", Status: StatusRunning, Stage: "implement",
		Switches: []Switch{
			{To: "repo-shared-b", Reason: "exhausted", CacheMissUSD: 0.00054},
			{From: "repo-shared-b", To: "api-key", Reason: "not_admitted", CacheMissUnmeasured: true},
		}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Switches) != 2 {
		t.Fatalf("the log read back as %+v", got.Switches)
	}
	first, second := got.Switches[0], got.Switches[1]
	if first.From != "" || first.To != "repo-shared-b" || first.Reason != "exhausted" || first.CacheMissUSD != 0.00054 || first.CacheMissUnmeasured {
		t.Fatalf("the first switch = %+v", first)
	}
	if second.From != "repo-shared-b" || second.To != "api-key" || second.CacheMissUnmeasured != true || second.CacheMissUSD != 0 {
		t.Fatalf("the second switch = %+v", second)
	}
	for _, want := range []string{
		`"from":"repo-shared-b"`,
		`"to":"repo-shared-b"`,
		`"reason":"exhausted"`,
		`"cache_miss_usd":0.00054`,
		`"cache_miss_unmeasured":true`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record does not carry %s: %s", want, data)
		}
	}
	// A call that started on the credential that answered it has no from, and
	// a switch that cost nothing measured carries no estimate -- but its to
	// and its reason are always there: an entry without them is not a record
	// of a switch.
	plain, _ := json.Marshal(Switch{To: "repo-a", Reason: "expired"})
	if string(plain) != `{"to":"repo-a","reason":"expired"}` {
		t.Fatalf("a switch with nothing else = %s", plain)
	}
}
