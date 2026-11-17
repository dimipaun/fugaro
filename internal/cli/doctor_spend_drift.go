package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/watch"
)

// doctor_spend_drift.go is R6's drift alarm in doctor (D2649, D2650): the
// models whose provider-reported cost has left the price table's band over
// today's and yesterday's day counters, read from the project's budget
// database with the operator's own credentials.
//
// It is the line `fugaro watch` draws, asked of the database instead of a
// stream: the same two days, the same clock deciding which two they are (the
// database's), the same leaves, the same 10% and $1, so a reader who sees one
// of the two sees what the other would say — and the rule itself is watch's
// (driftLines), which is the only place it is written down. The
// counters are per UTC day, so D2649's "a rolling day" can only be today plus
// yesterday (F30), and the leaves are summed per model across every repository
// because what is being read is the table's reading of a model, not a
// repository's (docs/spend-accuracy/day-leaves.md).
//
// It is a warning and never a failure, and no `fugaro update` puts it right: a
// report outside the band is a fact about the price table or about what a
// provider bills, which the reader settles by pricing the model under
// model_prices or by the account's own provider settings. So the class is the
// default view's own (doctorClassOf: a warning is an item), with no allow-list
// entry and — deliberately — no short form in doctor_render.go's maps: the
// sentence the reader has to see names the model and the factor (R6), which no
// entry keyed by ID alone can carry, and a generic one-line stand-in would hide
// exactly the two things the line exists for.

// spendDriftCheckID is doctor's line about a provider's reported cost leaving
// the price table's band: one check per model, however many repositories its
// counters are spread over.
const spendDriftCheckID = "spend-drift"

// doctorSpendDrift reads the two days and reports one warning per drifting
// model, and nothing at all when nothing drifted: a model inside its band, one
// under DriftMinSpend, a model with no band because no call brought a report
// back, and a day with no counters at all are all silence, since a line there
// would be a finding about a comparison the counters cannot make.
//
// A read that did not answer is an info line carrying the unread marker rather
// than silence or a pass: with the counters unreadable, whether nothing drifted
// is unknown, and "unknown" is not "nothing to say" (doctorClassOf).
//
// now is the local clock, and the day is the database's (rtdb.Client.ServerNow):
// the counters are keyed by the database's UTC day, and `fugaro watch` reads
// that same clock (watch.Supervisor.now), so a host whose own clock is off by a
// day would otherwise sum two days that are not today and yesterday while its
// sentence says they are, and answer differently from the screen this line is
// copied from. The local clock is what is left when no response has stamped the
// client yet, which is watch's own fallback — and by the time this check runs
// the checks above have read the same client several times, so the fallback is
// for a caller that reaches here alone.
func doctorSpendDrift(ctx context.Context, db *rtdb.Client, now time.Time) []doctorCheck {
	unread := func(path string, err error) []doctorCheck {
		return []doctorCheck{{ID: spendDriftCheckID, Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether a model's reported cost has left the price table's band could not be checked (%s: %v), so a drift over today and yesterday is unknown rather than absent", path, err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	}
	if sn, ok := db.ServerNow(); ok {
		now = sn
	}
	day := budget.Day(now)
	st := watch.NewState()
	st.SetDay(day)
	today := "spend/" + budget.DayKey(day) + "/repos"
	var raw json.RawMessage
	found, err := db.Get(ctx, today, &raw)
	if err != nil {
		return unread(today, err)
	}
	if !found {
		// A project that has spent nothing has no node, which is a day of no
		// counters and not a read that failed.
		raw = json.RawMessage("null")
	}
	if err := st.Apply(watch.SrcRepos, rtdb.Event{Type: "put", Path: "/", Data: raw}, now); err != nil {
		return unread(today, err)
	}
	// Yesterday is the window's other half and the reader watch's own
	// (loadPrevRepos): the day is over, so nobody writes it again, and a day
	// without the node is a day whose models have nothing to compare.
	yesterday := "spend/" + budget.DayKey(day-1) + "/repos"
	var prev json.RawMessage
	if _, err := db.Get(ctx, yesterday, &prev); err != nil {
		return unread(yesterday, err)
	}
	st.SetPrev(prev)
	var checks []doctorCheck
	for _, d := range watch.Build(st, now, watch.Config{}).Drift {
		checks = append(checks, doctorCheck{ID: spendDriftCheckID, Severity: "warning",
			Problem: spendDriftProblem(d), Fix: spendDriftFix(d)})
	}
	return checks
}

// spendDriftProblem is one drift in the check's own words: the model and the
// factor first, because the default view prints the first sentence and R6 asks
// for both of them there, and the two figures behind the factor second so a
// reader of --all or --json has the report and the band the factor is a ratio
// of. The window and the fact that the band covers only the calls that carried
// a report are said for the same reason watch's line says them: whoever reads
// this sentence has the counters nowhere else (D2650).
func spendDriftProblem(d watch.Drift) string {
	end, where := "cheapest end", "below"
	if d.High {
		end, where = "dearest end", "above"
	}
	band := watch.USD(d.TLo) + "-" + watch.USD(d.THi)
	return fmt.Sprintf("%s's reported cost is %.2fx the %s of the price table's band over today and yesterday. "+
		"The provider reported %s for the calls that came back with a cost, against the table's band %s for those same calls, so the report is more than %d%% %s the %s",
		d.Model, d.Factor(), end, watch.USD(d.Rep), band, watch.DriftGapPercent, where, end)
}

// spendDriftFix is what the reader settles a drift with, and it is the two
// answers a model's price has here: the owner's own price under model_prices,
// or the table's dearest end for a catalog-priced route that served an endpoint
// dearer still (design §7, D12). A report below the band is the other
// direction, where the same pair reads as a price that is too high or a
// provider that under-reported what it billed — the one direction in which a
// low report hides spend, since the caps are enforced against the charge.
func spendDriftFix(d watch.Drift) string {
	if d.High {
		return "compare `fugaro budget prices` with what the provider billed: raise that model's price under model_prices in the project config, or set price_policy: highest so a catalog-priced route is charged the tier's dearest end"
	}
	return "compare `fugaro budget prices` with what the provider billed: lower that model's price under model_prices in the project config, or check whether the provider under-reported what it billed"
}
