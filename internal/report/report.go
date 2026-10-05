// Package report turns spend-history day records into the rows of
// `fugaro report`: grouping by period or dimension, exact dollar rendering,
// and the table, CSV and JSON outputs.
//
// Rules the outputs keep:
//   - Amounts are integer micro-dollars end to end and are rendered from the
//     integers, never through a float.
//   - Model dollars, notional dollars (a subscription's list-price figure,
//     never billed) and compute dollars are three separate columns and are
//     never added together.
//   - Weeks are ISO weeks in UTC: they start on Monday and are named
//     <ISO year>-W<week> (2026-12-31 and 2027-01-01 are both 2026-W53).
//     Months and years are UTC calendar months and years. Week, month and year
//     rows are computed here from the per-day documents.
//   - Compute is "n/a" when no run of the row had its compute estimated, never
//     0; "-" (null in JSON) means the figure is not recorded for that
//     breakdown (compute per model or per person).
//   - The runner records one dollar figure per model, billed or notional. By
//     model, a day with no billed spend counts it all as notional, a day with
//     no notional as billed, and a mixed day splits it in the day's
//     billed:notional proportion.
//   - Every string that came from the database goes through safetext.Strip,
//     in every output.
package report

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Dim is what a report groups by.
type Dim string

const (
	ByDay    Dim = "day"
	ByWeek   Dim = "week"
	ByMonth  Dim = "month"
	ByYear   Dim = "year"
	ByRepo   Dim = "repo"
	ByModel  Dim = "model"
	ByPerson Dim = "person"
)

// ParseDim parses --by.
func ParseDim(s string) (Dim, error) {
	switch d := Dim(s); d {
	case ByDay, ByWeek, ByMonth, ByYear, ByRepo, ByModel, ByPerson:
		return d, nil
	}
	return "", fmt.Errorf("--by %q: use day, week, month, year, repo, model or person", s)
}

// IsPeriod says whether d groups by time.
func (d Dim) IsPeriod() bool { return d == ByDay || d == ByWeek || d == ByMonth || d == ByYear }

// Rec is one day record and whether it is a partial (not final) day.
type Rec struct {
	budget.DayRecord
	Partial bool
}

// Compute states of a Row.
const (
	ComputeKnown        = iota // Compute is the sum of the estimated runs' compute
	ComputeNotEstimated        // no run's compute was estimated: n/a, never 0
	ComputeNotTracked          // not recorded for this breakdown
)

// Row is one line of a report.
type Row struct {
	Key     string // sanitized
	Start   string // first day of a period (YYYY-MM-DD), "" otherwise
	End     string // last day seen in the row
	Partial bool

	Spent, Notional, Compute budget.Micros
	NotionalTracked          bool
	ComputeState             int
	RunHours                 float64
	Runs, Calls              int64
	In, Out, CR, CW          int64 // by model
	TokensTracked            bool
}

// Report is the grouped rows and their totals row.
type Report struct {
	By Dim
	// NoHours: run-hours are not known (run-record totals): n/a, never 0.
	NoHours bool
	// MixedModelDays: a by-model day had both billed and notional spend, so
	// its models' dollars were split in that day's proportion.
	MixedModelDays bool
	Rows           []Row
	Totals         Row
}

// Text is database-derived text made safe: stripped of terminal controls.
func Text(s string) string { return safetext.Strip(s) }

func unkey(s string) string {
	if v, err := budget.Unkey(s); err == nil {
		return v
	}
	return s
}

func satAdd(a, b budget.Micros) budget.Micros {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > budget.Micros(1<<63-1)-b {
		return budget.Micros(1<<63 - 1)
	}
	return a + b
}

func satN(a, b int64) int64 { return int64(satAdd(budget.Micros(a), budget.Micros(b))) }

// acc accumulates a row.
type acc struct {
	row           Row
	estimatedRuns int64
	runs          int64
}

// PeriodKey is the key and the first day of date's period for a time grouping.
func PeriodKey(d Dim, date string) (key, start string, err error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", fmt.Errorf("bad date %q", date)
	}
	switch d {
	case ByDay:
		return date, date, nil
	case ByWeek:
		y, w := t.ISOWeek()
		off := (int(t.Weekday()) + 6) % 7 // Monday = 0
		return fmt.Sprintf("%04d-W%02d", y, w), t.AddDate(0, 0, -off).Format("2006-01-02"), nil
	case ByMonth:
		return t.Format("2006-01"), t.Format("2006-01") + "-01", nil
	case ByYear:
		return t.Format("2006"), t.Format("2006") + "-01-01", nil
	}
	return "", "", fmt.Errorf("%s is not a period", d)
}

// Build groups recs by d. Records whose date is not a date are ignored (the
// caller has warned). Rows are in key order for periods and by model dollars
// (largest first, then key) for the other groupings.
func Build(recs []Rec, d Dim) Report {
	groups := map[string]*acc{}
	order := []string{}
	get := func(key, start string) *acc {
		a, ok := groups[key]
		if !ok {
			a = &acc{row: Row{Key: Text(key), Start: start}}
			groups[key] = a
			order = append(order, key)
		}
		return a
	}
	tot := &acc{}
	mixed := false
	for _, r := range recs {
		switch {
		case d.IsPeriod() || d == ByRepo:
			var a *acc
			if d == ByRepo {
				name := r.Repo
				if name == "" {
					name = r.Slug
				}
				a = get(r.Slug, "")
				a.row.Key = Text(name)
			} else {
				key, start, err := PeriodKey(d, r.Date)
				if err != nil {
					continue
				}
				a = get(key, start)
			}
			a.row.NotionalTracked, a.row.TokensTracked = true, false
			addRecord(a, r)
		case d == ByModel:
			for k, m := range r.ByModel {
				a := get(k, "")
				a.row.Key = Text(unkey(k))
				billed, notional := splitModel(r.DayRecord, m.Micros)
				if r.SpentMicros > 0 && r.NotionalMicros > 0 {
					mixed = true
				}
				a.row.Spent = satAdd(a.row.Spent, billed)
				a.row.Notional = satAdd(a.row.Notional, notional)
				a.row.NotionalTracked = true
				a.row.In, a.row.Out = satN(a.row.In, m.In), satN(a.row.Out, m.Out)
				a.row.CR, a.row.CW = satN(a.row.CR, m.CR), satN(a.row.CW, m.CW)
				a.row.TokensTracked = true
				a.row.ComputeState = ComputeNotTracked
				markDay(a, r)
			}
		case d == ByPerson:
			var attributed int64
			for k, p := range r.ByPerson {
				attributed = satN(attributed, p.Runs)
				a := get(k, "")
				a.row.Key = Text(unkey(k))
				a.row.Spent = satAdd(a.row.Spent, p.Micros)
				a.row.Notional = satAdd(a.row.Notional, p.NotionalMicros)
				a.row.NotionalTracked = true
				a.row.Runs = satN(a.row.Runs, p.Runs)
				a.row.ComputeState = ComputeNotTracked
				markDay(a, r)
			}
			// Runs with no per-person figure (no requester recorded) are
			// still runs: count them under unknown.
			if r.Runs > attributed {
				a := get(budget.UnknownPerson, "")
				a.row.Key = budget.UnknownPerson
				a.row.NotionalTracked = true
				a.row.ComputeState = ComputeNotTracked
				a.row.Runs = satN(a.row.Runs, r.Runs-attributed)
				markDay(a, r)
			}
		}
	}
	rep := Report{By: d, MixedModelDays: mixed}
	for _, k := range order {
		a := groups[k]
		finish(a)
		rep.Rows = append(rep.Rows, a.row)
	}
	if d.IsPeriod() {
		sort.SliceStable(rep.Rows, func(i, j int) bool {
			return rep.Rows[i].Start+"|"+rep.Rows[i].Key < rep.Rows[j].Start+"|"+rep.Rows[j].Key
		})
	} else {
		sort.SliceStable(rep.Rows, func(i, j int) bool {
			if rep.Rows[i].Spent != rep.Rows[j].Spent {
				return rep.Rows[i].Spent > rep.Rows[j].Spent
			}
			return rep.Rows[i].Key < rep.Rows[j].Key
		})
	}
	// Totals: the sum of the rows, by the same rules.
	tot.row = Row{Key: "TOTAL", NotionalTracked: true, TokensTracked: d == ByModel}
	tot.row.ComputeState = ComputeNotEstimated
	if d == ByModel || d == ByPerson {
		tot.row.ComputeState = ComputeNotTracked
	}
	anyKnown := false
	for _, r := range rep.Rows {
		tot.row.Spent = satAdd(tot.row.Spent, r.Spent)
		tot.row.Notional = satAdd(tot.row.Notional, r.Notional)
		tot.row.Compute = satAdd(tot.row.Compute, r.Compute)
		tot.row.RunHours += r.RunHours
		tot.row.Runs, tot.row.Calls = satN(tot.row.Runs, r.Runs), satN(tot.row.Calls, r.Calls)
		tot.row.In, tot.row.Out = satN(tot.row.In, r.In), satN(tot.row.Out, r.Out)
		tot.row.CR, tot.row.CW = satN(tot.row.CR, r.CR), satN(tot.row.CW, r.CW)
		tot.row.Partial = tot.row.Partial || r.Partial
		if r.ComputeState == ComputeKnown {
			anyKnown = true
		}
		if tot.row.End < r.End {
			tot.row.End = r.End
		}
	}
	if anyKnown {
		tot.row.ComputeState = ComputeKnown
	}
	rep.Totals = tot.row
	return rep
}

// splitModel splits one model's recorded dollars of a day into billed and
// notional: all notional on a day with no billed spend, all billed on a day
// with no notional, otherwise in the day's billed:notional proportion.
func splitModel(r budget.DayRecord, m budget.Micros) (billed, notional budget.Micros) {
	switch {
	case m <= 0:
		return 0, 0
	case r.NotionalMicros <= 0:
		return m, 0
	case r.SpentMicros <= 0:
		return 0, m
	}
	hi, lo := bits.Mul64(uint64(m), uint64(r.NotionalMicros))
	den := uint64(satAdd(r.SpentMicros, r.NotionalMicros))
	if hi >= den { // cannot happen while NotionalMicros <= den; stay safe
		return 0, m
	}
	q, _ := bits.Div64(hi, lo, den)
	notional = budget.Micros(q)
	return m - notional, notional
}

func markDay(a *acc, r Rec) {
	a.row.Partial = a.row.Partial || r.Partial
	if a.row.End < r.Date {
		a.row.End = r.Date
	}
}

func addRecord(a *acc, r Rec) {
	a.row.Spent = satAdd(a.row.Spent, r.SpentMicros)
	a.row.Notional = satAdd(a.row.Notional, r.NotionalMicros)
	a.row.Compute = satAdd(a.row.Compute, r.ComputeMicros)
	a.row.RunHours += r.RunHours
	a.row.Runs, a.row.Calls = satN(a.row.Runs, r.Runs), satN(a.row.Calls, r.Calls)
	a.estimatedRuns = satN(a.estimatedRuns, r.ComputeEstimatedRuns)
	a.runs = satN(a.runs, r.Runs)
	markDay(a, r)
}

func finish(a *acc) {
	if a.row.ComputeState == ComputeNotTracked {
		return
	}
	// n/a unless a run's compute was estimated (or there were no runs at all).
	if a.estimatedRuns > 0 || a.runs == 0 {
		a.row.ComputeState = ComputeKnown
	} else {
		a.row.ComputeState = ComputeNotEstimated
	}
}

// ---------------------------------------------------------------- dollars

// USD6 is m as dollars with exactly six decimals, from the integer.
func USD6(m budget.Micros) string {
	if m < 0 {
		return "-" + USD6(-m)
	}
	return fmt.Sprintf("%d.%06d", int64(m)/1_000_000, int64(m)%1_000_000)
}

// USD is m for people: at least cents, up to six decimals when there are
// some, exact.
func USD(m budget.Micros) string {
	s := USD6(m)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	return "$" + s
}
