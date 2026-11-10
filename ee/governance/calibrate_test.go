// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance_test

// The tier calibration (plan T8.3): the window it reads, the halts it leaves
// out, the bounds it suggests within, and the one thing it never does --
// apply anything (G9).
//
// The package is external and blank-imports ee/register (see policy_test.go),
// so every test below drives the calibration the way a tagged build's report
// does: through the digest's section hook, over a digest that digest.Build
// wrote. That is the whole point of the seam (design ee-boundary-moves.md
// §5.4) -- a test that called governance.Sections directly would pass while
// ee/register never wired it -- and it is why nothing here calls SetSections:
// the slot is taken by the binary's own init.

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/governance"
	_ "github.com/dimipaun/fugaro/ee/register"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// calPeriod is the week every fixture below finished in: 2026-10-19 to
// 2026-10-25.
func calPeriod(t *testing.T) digest.Period {
	t.Helper()
	p, err := digest.NewPeriod(digest.ByWeek, "2026-10-21")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// calTime is one fixture instant.
func calTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// calRun is one fixture run: the rate every table below is a set of multiples
// of, spelled out rather than derived.
type calRun struct {
	id      string
	at      string // when it finished
	took    time.Duration
	tier    tier.Tier // the tier it was dispatched at
	usd     float64   // what that tier expected it to cost, in dollars
	minutes int64     // and to take
	billed  float64   // what it really cost, in dollars
	halt    runstore.HaltReason
}

// calRecord is r as the record a report reads.
func calRecord(t *testing.T, r calRun) runstore.Record {
	t.Helper()
	fin := calTime(t, r.at)
	c := runstore.NewCost(r.billed, 0, runstore.BasisAPIList)
	rec := runstore.Record{
		Version: 1, RunID: r.id, Repo: "owner/repo-a", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		BaseBranch: "main", Cost: &c, CostUSD: r.billed,
		FinishedAt: &fin, StartedAt: fin.Add(-r.took),
		Tier: &runstore.TierRecord{Tier: r.tier, ExpectedUSD: r.usd, ExpectedMinutes: r.minutes},
	}
	if r.halt != "" {
		rec.Status = runstore.StatusHalted
		rec.Halt = &runstore.Halt{Reason: r.halt, Scope: "run", At: fin}
	}
	return rec
}

// calMarkdown is the records as the report a tagged build writes: the core
// report plus whatever the registered hook adds.
func calMarkdown(t *testing.T, recs []runstore.Record) string {
	t.Helper()
	return string(digest.Markdown(digest.Build(recs, calPeriod(t))))
}

// TestCalibrationExcludesGovernanceHalts (plan T8.3, spec §6): a run a
// governance tripwire stopped is not a figure of what its tier costs. Such a
// run spent what the tripwire let it spend -- nothing, here -- so counting it
// would calibrate a tier on runs that were never allowed to run as one and
// pull the suggestion down to the floor.
//
// A run a *budget* halt stopped stays in: the run cap is not governance (D520),
// and a run that ran out of money says what a tier costs like any other.
func TestCalibrationExcludesGovernanceHalts(t *testing.T) {
	// small expects $1.00 and 30 minutes. Four of its runs were allowed to run
	// as one, at twice the expectation (one of them stopped at the run cap);
	// four were stopped by a tripwire after a minute and a cent.
	recs := []runstore.Record{}
	for i, r := range []calRun{
		{id: "20261019-090000-0001", at: "2026-10-19T09:00:00Z", took: time.Hour, tier: tier.Small, usd: 1, minutes: 30, billed: 2},
		{id: "20261020-090000-0002", at: "2026-10-20T09:00:00Z", took: time.Hour, tier: tier.Small, usd: 1, minutes: 30, billed: 2},
		{id: "20261021-090000-0003", at: "2026-10-21T09:00:00Z", took: time.Hour, tier: tier.Small, usd: 1, minutes: 30, billed: 2},
		{id: "20261022-090000-0004", at: "2026-10-22T09:00:00Z", took: 30 * time.Minute, tier: tier.Small, usd: 1, minutes: 30, billed: 1, halt: runstore.HaltRunCap},
		{id: "20261023-090000-0005", at: "2026-10-23T09:00:00Z", took: time.Minute, tier: tier.Small, usd: 1, minutes: 30, billed: 0.01, halt: runstore.HaltStageBudget},
		{id: "20261024-090000-0006", at: "2026-10-24T09:00:00Z", took: time.Minute, tier: tier.Small, usd: 1, minutes: 30, billed: 0.01, halt: runstore.HaltReviewRatio},
		{id: "20261025-090000-0007", at: "2026-10-25T09:00:00Z", took: time.Minute, tier: tier.Small, usd: 1, minutes: 30, billed: 0.01, halt: runstore.HaltFanout},
		{id: "20261025-100000-0008", at: "2026-10-25T10:00:00Z", took: time.Minute, tier: tier.Small, usd: 1, minutes: 30, billed: 0.01, halt: runstore.HaltExpectedExceeded},
	} {
		if i >= 4 && !slices.Contains(runstore.GovernanceHaltReasons(), r.halt) {
			t.Fatalf("fixture %d is the %s halt and the test reads it as a governance one", i, r.halt)
		}
		recs = append(recs, calRecord(t, r))
	}

	cal := governance.Calibrate(digest.Build(recs, calPeriod(t)))
	if len(cal) != 1 {
		t.Fatalf("the window gave %d rows, want one: %+v", len(cal), cal)
	}
	c := cal[0]
	if c.Tier != tier.Small || c.Runs != 4 {
		t.Errorf("the small row = %+v, want the four runs that were not governance-halted", c)
	}
	// The medians of {2.00, 2.00, 2.00, 1.00} cost and duration ratios.
	if !c.CostKnown || c.CostRatio != 2 {
		t.Errorf("cost ratio = %v (known %v), want 2.00", c.CostRatio, c.CostKnown)
	}
	if !c.DurationKnown || c.DurationRatio != 2 {
		t.Errorf("duration ratio = %v (known %v), want 2.00", c.DurationRatio, c.DurationKnown)
	}
	if c.SuggestedUSD != 2_000_000 || c.SuggestedMinutes != 60 {
		t.Errorf("the suggestion = %s / %dm, want $2.00 / 60m (2.00x the expectation); counting the halted runs would have held it at the floor of $0.50",
			digest.USD(c.SuggestedUSD), c.SuggestedMinutes)
	}
	// And the report prints the window the same way: the halted runs are not
	// in the row a person reads either.
	md := calMarkdown(t, recs)
	if !strings.Contains(md, "| small | 4 | 2.00x | 2.00x | $1.00 / 30m | $2.00 / 60m |") {
		t.Errorf("the report's calibration row is not the window the calibration read:\n%s", calSection(t, md))
	}
}

// TestCalibrationBounds (plan T8.3, G9): a suggestion is the median ratio the
// window gave, held between half and twice the expectation the runs carried.
// A tier whose runs cost ten times it is suggested at twice it and no more --
// the window is evidence about the figure it holds, not a reason to move a
// default past the bound -- and a tier far under it is suggested at half.
func TestCalibrationBounds(t *testing.T) {
	var recs []runstore.Record
	next := 0
	// add is n runs of one tier, every one of them the same multiple of the
	// expectation, so the median is that multiple exactly.
	add := func(n int, tr tier.Tier, usd float64, minutes int64, billed float64, took time.Duration) {
		for range n {
			next++
			recs = append(recs, calRecord(t, calRun{
				id:      fmt.Sprintf("20261021-%06d-%04x", 90000+next, next),
				at:      fmt.Sprintf("2026-10-21T09:%02d:00Z", next),
				took:    took,
				tier:    tr,
				usd:     usd,
				minutes: minutes,
				billed:  billed,
			}))
		}
	}
	add(3, tier.Trivial, 1, 100, 10, 1000*time.Minute) // 10.00x: past the ceiling
	add(3, tier.Small, 1, 100, 0.05, 5*time.Minute)    // 0.05x: past the floor
	add(3, tier.Medium, 1, 100, 1.25, 125*time.Minute) // 1.25x: inside both
	// tier.Large is the absent tier: no run of it, no row.

	cal := governance.Calibrate(digest.Build(recs, calPeriod(t)))
	if len(cal) != 3 {
		t.Fatalf("the window gave %d rows, want the three tiers it holds runs of: %+v", len(cal), cal)
	}
	for i, want := range []tier.Tier{tier.Trivial, tier.Small, tier.Medium} {
		if cal[i].Tier != want {
			t.Errorf("row %d is %q, want %q: the rows print in the vocabulary's order", i, cal[i].Tier, want)
		}
	}
	for _, c := range cal {
		if c.Runs != 3 {
			t.Errorf("%s holds %d runs, want 3", c.Tier, c.Runs)
		}
		// Whatever the ratio, the suggestion is inside the bounds the plan
		// names, measured against the expectation the window's runs carried.
		if lo, hi := float64(c.ExpectedUSD)*governance.SuggestionFloor, float64(c.ExpectedUSD)*governance.SuggestionCeiling; float64(c.SuggestedUSD) < lo || float64(c.SuggestedUSD) > hi {
			t.Errorf("%s suggests %s, outside %.2fx..%.2fx of %s", c.Tier,
				digest.USD(c.SuggestedUSD), governance.SuggestionFloor, governance.SuggestionCeiling, digest.USD(c.ExpectedUSD))
		}
	}
	// Every tier's runs carried the same expectation, $1.00 over 100 minutes,
	// so the base is that for all three and the ratios alone differ.
	for _, tc := range []struct {
		tr               tier.Tier
		ratio            float64
		usdWant, minWant int64
	}{
		{tier.Trivial, 10, 2_000_000, 200},
		{tier.Small, 0.05, 500_000, 50},
		{tier.Medium, 1.25, 1_250_000, 125},
	} {
		var c governance.Calibration
		for _, got := range cal {
			if got.Tier == tc.tr {
				c = got
			}
		}
		if !c.CostKnown || c.CostRatio != tc.ratio || !c.DurationKnown || c.DurationRatio != tc.ratio {
			t.Errorf("%s ratios = %v cost (known %v), %v duration (known %v), want %v for both",
				tc.tr, c.CostRatio, c.CostKnown, c.DurationRatio, c.DurationKnown, tc.ratio)
		}
		if c.ExpectedUSD != 1_000_000 || c.ExpectedMinutes != 100 {
			t.Errorf("%s base = %s / %dm, want the expectation its runs carried", tc.tr, digest.USD(c.ExpectedUSD), c.ExpectedMinutes)
		}
		if int64(c.SuggestedUSD) != tc.usdWant || c.SuggestedMinutes != tc.minWant {
			t.Errorf("%s suggests %s / %dm, want %s / %dm",
				tc.tr, digest.USD(c.SuggestedUSD), c.SuggestedMinutes, digest.USD(budget.Micros(tc.usdWant)), tc.minWant)
		}
	}

	// The report prints the same three rows, at the same figures.
	md := calMarkdown(t, recs)
	for _, row := range []string{
		"| trivial | 3 | 10.00x | 10.00x | $1.00 / 100m | $2.00 / 200m |",
		"| small | 3 | 0.05x | 0.05x | $1.00 / 100m | $0.50 / 50m |",
		"| medium | 3 | 1.25x | 1.25x | $1.00 / 100m | $1.25 / 125m |",
	} {
		if !strings.Contains(md, row) {
			t.Errorf("the report holds no %q:\n%s", row, calSection(t, md))
		}
	}
	if strings.Contains(md, "| large |") {
		t.Errorf("the report carries a row for a tier the window holds no run of:\n%s", calSection(t, md))
	}
}

// TestCalibrationWindowIsPerTier (plan T8.3): the window is the tier's own.
// The plan reads it "per tier, over the last 20 runs or 14 days", so a busy
// tier may not push a quiet one out of the section and may not shorten the
// quiet one's window: 25 small runs and 3 medium ones older than every small
// run of the last twenty give two rows, small on its own last 20 and medium on
// its 3 -- where one window cut over the report's expectations at once would
// hold the 20 newest, all small, and print no medium row at all.
func TestCalibrationWindowIsPerTier(t *testing.T) {
	var recs []runstore.Record
	// small: 25 runs ten minutes apart, 1.00x for the five the window drops
	// and 2.00x for the twenty it keeps.
	for i := range 25 {
		billed := 2.0
		if i < 5 {
			billed = 1
		}
		recs = append(recs, calRecord(t, calRun{
			id:      fmt.Sprintf("20261021-%06d-%04x", 90000+i, i),
			at:      calTime(t, "2026-10-21T00:00:00Z").Add(time.Duration(i) * 10 * time.Minute).Format(time.RFC3339),
			took:    time.Hour,
			tier:    tier.Small,
			usd:     1,
			minutes: 30,
			billed:  billed,
		}))
	}
	// medium: 3 runs two days earlier, older than every small run the window
	// keeps, at 1.50x.
	for i := range 3 {
		recs = append(recs, calRecord(t, calRun{
			id:      fmt.Sprintf("20261019-%06d-%04x", 90000+i, i),
			at:      fmt.Sprintf("2026-10-19T09:%02d:00Z", i),
			took:    45 * time.Minute,
			tier:    tier.Medium,
			usd:     1,
			minutes: 30,
			billed:  1.5,
		}))
	}

	cal := governance.Calibrate(digest.Build(recs, calPeriod(t)))
	if len(cal) != 2 {
		t.Fatalf("the window gave %d rows, want one per tier it holds runs of: %+v", len(cal), cal)
	}
	for _, c := range cal {
		switch c.Tier {
		case tier.Small:
			if c.Runs != governance.WindowRuns {
				t.Errorf("small holds %d runs, want its own last %d", c.Runs, governance.WindowRuns)
			}
			if c.CostRatio != 2 {
				t.Errorf("small's cost ratio = %v, want the 2.00x its last %d runs carry", c.CostRatio, governance.WindowRuns)
			}
		case tier.Medium:
			if c.Runs != 3 {
				t.Errorf("medium holds %d runs, want the 3 it ran: a window cut over every tier's runs at once would hold none of them", c.Runs)
			}
			if c.CostRatio != 1.5 {
				t.Errorf("medium's cost ratio = %v, want 1.50x", c.CostRatio)
			}
		default:
			t.Errorf("the window holds a %s row and the fixture ran none", c.Tier)
		}
	}

	// The report prints both rows, each on its own tier's window.
	md := calMarkdown(t, recs)
	for _, row := range []string{
		"| small | 20 | 2.00x | 2.00x | $1.00 / 30m | $2.00 / 60m |",
		"| medium | 3 | 1.50x | 1.50x | $1.00 / 30m | $1.50 / 45m |",
	} {
		if !strings.Contains(md, row) {
			t.Errorf("the report holds no %q:\n%s", row, calSection(t, md))
		}
	}
}

// TestCalibrationWindowStopsAtFourteenDays (plan T8.3): the window is the last
// WindowDays of the tier's own runs as well as its last WindowRuns, whichever
// is fewer. A year's runs are read here because they are the only period a run
// older than a fortnight fits in: the two large runs of October are the window
// and the three of September are outside it, so the ratio is the recent 3.00x
// and not the 1.00x a reader that kept every run would take.
func TestCalibrationWindowStopsAtFourteenDays(t *testing.T) {
	p, err := digest.NewPeriod(digest.ByYear, "2026-10-21")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's own claim: the cutoff falls between the September runs and
	// the October ones, so the two halves are on either side of it.
	cutoff := calTime(t, "2026-10-21T09:00:00Z").AddDate(0, 0, -governance.WindowDays)
	if !cutoff.After(calTime(t, "2026-09-12T09:00:00Z")) || cutoff.After(calTime(t, "2026-10-20T09:00:00Z")) {
		t.Fatalf("the fixture no longer straddles the %d-day cutoff %s", governance.WindowDays, cutoff.Format(time.RFC3339))
	}
	var recs []runstore.Record
	for _, r := range []calRun{
		{id: "20260910-090000-0001", at: "2026-09-10T09:00:00Z", took: 30 * time.Minute, tier: tier.Large, usd: 1, minutes: 30, billed: 1},
		{id: "20260911-090000-0002", at: "2026-09-11T09:00:00Z", took: 30 * time.Minute, tier: tier.Large, usd: 1, minutes: 30, billed: 1},
		{id: "20260912-090000-0003", at: "2026-09-12T09:00:00Z", took: 30 * time.Minute, tier: tier.Large, usd: 1, minutes: 30, billed: 1},
		{id: "20261020-090000-0004", at: "2026-10-20T09:00:00Z", took: 90 * time.Minute, tier: tier.Large, usd: 1, minutes: 30, billed: 3},
		{id: "20261021-090000-0005", at: "2026-10-21T09:00:00Z", took: 90 * time.Minute, tier: tier.Large, usd: 1, minutes: 30, billed: 3},
	} {
		recs = append(recs, calRecord(t, r))
	}

	cal := governance.Calibrate(digest.Build(recs, p))
	if len(cal) != 1 {
		t.Fatalf("the window gave %d rows, want the one tier it holds runs of: %+v", len(cal), cal)
	}
	c := cal[0]
	if c.Runs != 2 {
		t.Errorf("the large row holds %d runs, want the 2 inside the last %d days", c.Runs, governance.WindowDays)
	}
	if !c.CostKnown || c.CostRatio != 3 || !c.DurationKnown || c.DurationRatio != 3 {
		t.Errorf("the large row's ratios = %v cost (known %v), %v duration (known %v), want 3.00x for both: the runs outside the window are not read",
			c.CostRatio, c.CostKnown, c.DurationRatio, c.DurationKnown)
	}
	if c.SuggestedUSD != 2_000_000 || c.SuggestedMinutes != 60 {
		t.Errorf("the suggestion = %s / %dm, want $2.00 / 60m (3.00x held at the ceiling)", digest.USD(c.SuggestedUSD), c.SuggestedMinutes)
	}
}

// TestCalibrationAppliesNothing (plan T8.3, G9): a suggestion is printed and
// nothing else. The window says a tier costs twice what it expects, the report
// says so, and the governance a run is merged under still resolves the figure
// Fugaro ships -- no key is written, no run reads a suggestion, and the report
// is byte-identical every time it is written (G3).
func TestCalibrationAppliesNothing(t *testing.T) {
	var recs []runstore.Record
	for i := range 3 {
		recs = append(recs, calRecord(t, calRun{
			id:      fmt.Sprintf("20261021-%06d-%04x", 90000+i+1, i+1),
			at:      fmt.Sprintf("2026-10-21T09:%02d:00Z", i+1),
			took:    2 * time.Hour,
			tier:    tier.Small,
			usd:     1,
			minutes: 30,
			billed:  10,
		}))
	}
	d := digest.Build(recs, calPeriod(t))
	report := string(digest.Markdown(d))

	// The suggestion is a real move, so what is asserted below is about
	// something: three runs at ten times the expectation, held at the ceiling.
	if !strings.Contains(report, "| small | 3 | 10.00x | 4.00x | $1.00 / 30m | $2.00 / 60m |") {
		t.Errorf("the report does not carry the suggestion the window gives:\n%s", calSection(t, report))
	}
	// It is printed as a suggestion and says what it is.
	for _, want := range []string{
		"Nothing is applied",
		"a suggestion is a figure a person writes into the policy",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the section does not say %q:\n%s", want, calSection(t, report))
		}
	}

	// Nothing was applied. The suggestion differs from what the installation
	// ships, and the installation still ships it: what a run merges under is
	// the same governance it merged under before a report was written.
	shipped := policy.Governance{}.WithDefaults().Tiers.For(tier.Small)
	if shipped.ExpectedUSD == 2 {
		t.Fatalf("the shipped small expectation is %v, which is the suggestion: this test would pass on a calibration that applied itself", shipped.ExpectedUSD)
	}
	if got := (policy.Governance{}).WithDefaults().Tiers.For(tier.Small); got != shipped {
		t.Errorf("the governance a run merges under = %+v, want the shipped %+v: a suggestion reached the policy", got, shipped)
	}
	if got, want := governance.Defaults().Tiers, governance.TierExpectations(); got != want {
		t.Errorf("the shipped expectations = %+v, want the unchanged %+v", got, want)
	}
	// The report's own figures are the runs' and not the section's: the
	// calibration wrote no number into the digest.
	if !strings.Contains(report, "Billed $30.00,") {
		t.Errorf("the report's own total is not the runs' own figure:\n%s", report)
	}
	// And the digest the hook was handed is the digest that was built: a hook
	// that wrote into it would make the next report of it a different one.
	if !reflect.DeepEqual(d, digest.Build(recs, calPeriod(t))) {
		t.Errorf("the calibration changed the digest it was handed: %+v", d.Expectations)
	}
	if again := string(digest.Markdown(digest.Build(recs, calPeriod(t)))); again != report {
		t.Errorf("the same records gave two reports:\n%s\n---\n%s", report, again)
	}
}

// TestCalibrationPrintsOnlyInAWeeklyReport (plan T8.3): the section is the
// weekly report's. The window it reads is 20 runs or 14 days, so a day's
// report would print the same suggestion under a period it is not a figure of
// and a month's would print a window no longer than its own. Neither carries
// the section: a build with a hook still prints the report it printed before
// on every other cadence.
func TestCalibrationPrintsOnlyInAWeeklyReport(t *testing.T) {
	var recs []runstore.Record
	for i := range 3 {
		recs = append(recs, calRecord(t, calRun{
			id:      fmt.Sprintf("20261021-%06d-%04x", 90000+i+1, i+1),
			at:      fmt.Sprintf("2026-10-21T09:%02d:00Z", i+1),
			took:    time.Hour,
			tier:    tier.Small,
			usd:     1,
			minutes: 30,
			billed:  2,
		}))
	}
	if !strings.Contains(string(digest.Markdown(digest.Build(recs, calPeriod(t)))), "## Tier calibration") {
		t.Fatal("the weekly report carries no calibration section: ee/register did not wire the hook")
	}
	for _, dim := range []digest.Dim{digest.ByDay, digest.ByMonth, digest.ByYear} {
		p, err := digest.NewPeriod(dim, "2026-10-21")
		if err != nil {
			t.Fatal(err)
		}
		if d := string(digest.Markdown(digest.Build(recs, p))); strings.Contains(d, "Tier calibration") {
			t.Errorf("the %s report carries the calibration section:\n%s", dim, d)
		}
	}
	// The section slot holds a hook -- the one this binary's init installed --
	// and a second registration is refused (design §5.5).
	if err := digest.SetSections(governance.Sections); !errors.Is(err, digest.ErrSectionHookSet) {
		t.Errorf("SetSections answered %v, want %v: ee/register registered no hook, and this test just took the slot", err, digest.ErrSectionHookSet)
	}
}

// calSection is the report's calibration section, for an error message.
func calSection(t *testing.T, report string) string {
	t.Helper()
	i := strings.Index(report, "## Tier calibration")
	if i < 0 {
		return "(the report carries no calibration section)"
	}
	return report[i:]
}
