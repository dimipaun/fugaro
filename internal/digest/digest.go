// Package digest builds one period's report over a repository's run records
// and renders it as Markdown.
//
// Two rules shape everything here:
//
//   - No person, and no text a record wrote. A record is untrusted input --
//     any run of the repository can write one -- and a report is committed
//     into a repository that may be public, so the types below have no field
//     for an address, an account or a task's own words, and no free-text
//     field of a record is ever rendered (G1). What is printed is a closed
//     vocabulary checked against its known set (an unknown value renders
//     "other"), a bounded number, a date, or a run ID or pull-request number
//     checked by shape.
//   - Deterministic figures. The same records give byte-identical Markdown
//     (G3): nothing here reads a clock, and every map is walked in sorted key
//     order.
//
// Nothing here reads a record by itself: Build is handed the records of one
// repository, which is the caller's business (G2).
package digest

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/stats"
	"github.com/dimipaun/fugaro/internal/task"
)

// MaxBytes caps the rendered report: a daily file is read in a pull request,
// and D321's figure is "under ~8 KiB".
const MaxBytes = 8 << 10

// The selection D325 pins: every flagged run whatever its cost, plus the
// dearest DearPercent of the period's runs, never fewer than MinDear of them
// and never more than TopRuns rows of the dearest kind.
const (
	DearPercent = 15
	MinDear     = 3
	TopRuns     = 10
)

// The fixed flags' thresholds (D325).
const (
	// ReviewCallsRatio and ReviewCallsFloor: a run whose review stages made
	// more than ReviewCallsRatio x max(implement calls, ReviewCallsFloor)
	// calls is flagged.
	ReviewCallsRatio = 5
	ReviewCallsFloor = 20
	// MaxStageAgents is the most sub-agent calls one stage may carry.
	MaxStageAgents = 3
	// MaxFixRounds is the most fix rounds a run may have.
	MaxFixRounds = 1
)

// The flag names: the report's own vocabulary, never a word a record chose.
const (
	FlagReviewCalls = "review_calls"
	FlagSubagents   = "subagents"
	FlagHalt        = "halt"
	FlagFixRounds   = "fix_rounds"
)

// FlagNames is every flag this report raises, in the order it prints.
var FlagNames = []string{FlagReviewCalls, FlagSubagents, FlagHalt, FlagFixRounds}

// other is what a value outside its known set renders as (G1).
const other = "other"

// maxCount bounds a count read from a record. A record is untrusted input, so
// a count outside this is a figure no stage of a real run records and the
// report leaves it out rather than print it.
const maxCount = 1 << 20

// maxPR bounds a pull-request number.
const maxPR = 1 << 30

// maxUSD bounds a dollar figure read from a record: a million dollars in one
// run is past any figure this product produces.
const maxUSD = 1e6

// The closed sets a record's words are checked against. They are the wire's
// own enums (schemas/result.schema.json, runstore's constants); a value
// outside them is a record this runner did not write, and it renders "other"
// rather than itself.
var (
	statuses = []string{
		string(runstore.StatusRunning), string(runstore.StatusSucceeded),
		string(runstore.StatusFailed), string(runstore.StatusInfraError),
		string(runstore.StatusCancelled), string(runstore.StatusHalted),
	}
	haltReasons = []string{
		string(runstore.HaltKillSwitch), string(runstore.HaltRunCap),
		string(runstore.HaltRepoDailyCap), string(runstore.HaltGlobalDailyCap),
		string(runstore.HaltNoCap), string(runstore.HaltTokenCap),
		string(runstore.HaltBudgetUnavailable), string(runstore.HaltBudgetTokenExpired),
		string(runstore.HaltNoCredential), string(runstore.HaltCredsRulesDenied),
		string(runstore.HaltStageBudget), string(runstore.HaltReviewRatio),
		string(runstore.HaltFanout), string(runstore.HaltReviewScope),
		string(runstore.HaltExpectedExceeded),
	}
	// stageNames is the record's own stage vocabulary plus the one name the
	// runner writes into a run's stage timings and the record's stage enum
	// does not carry (provision.go).
	stageNames = []string{
		"bootstrap", "plan", "implement", "review_first", "review", "fix",
		"render", "finalize", "writeback", "provision",
	}
)

// Period is one report period: the time grouping, its key and its first and
// last day.
type Period struct {
	Dim   report.Dim
	Key   string
	Start string
	End   string
}

// NewPeriod is the period date ("2006-01-02") falls in. It reuses
// report.PeriodKey's clock, so a week here is the ISO week in UTC every other
// period in this tree is: 2026-12-31 and 2027-01-01 are both 2026-W53.
func NewPeriod(dim report.Dim, date string) (Period, error) {
	key, start, err := report.PeriodKey(dim, date)
	if err != nil {
		return Period{}, err
	}
	t, err := time.Parse("2006-01-02", start)
	if err != nil {
		return Period{}, fmt.Errorf("bad period start %q", start)
	}
	end := start
	switch dim {
	case report.ByWeek:
		end = t.AddDate(0, 0, 6).Format("2006-01-02")
	case report.ByMonth:
		end = t.AddDate(0, 1, -1).Format("2006-01-02")
	case report.ByYear:
		end = t.AddDate(1, 0, -1).Format("2006-01-02")
	}
	return Period{Dim: dim, Key: key, Start: start, End: end}, nil
}

// Holds reports whether t is in the period. A period is closed: it holds the
// runs that finished in it (G4), so a run that has not finished is in no
// period, and a finish time is read in UTC whatever zone the record wrote.
func (p Period) Holds(t time.Time) bool {
	key, _, err := report.PeriodKey(p.Dim, t.UTC().Format("2006-01-02"))
	return err == nil && key == p.Key
}

// Flag is one flag of one run: its name, and the value it carries when it
// names one (the halt reason, an enum).
type Flag struct {
	Name  string
	Value string
}

// String is the flag as the report prints it.
func (f Flag) String() string {
	if f.Value == "" {
		return f.Name
	}
	return f.Name + ":" + f.Value
}

// Run is one selected run's row. Every field is a figure or a closed
// vocabulary: nothing here can hold a word a record chose.
type Run struct {
	ID       string
	Status   string
	PR       int // 0 when the record names no pull request
	Base     bool
	Reviews  int
	Billed   budget.Micros
	Notional budget.Micros
	Flags    []Flag
}

// Stage is one stage name's figures over the period's runs.
type Stage struct {
	Name string
	// Calls is every recorded call count of the name, and CallsKnown is
	// false when no stage of the name recorded one: absent, printed n/a,
	// never 0 (G5).
	Calls       int
	CallsKnown  bool
	Agents      int
	AgentsKnown bool
}

// Figures is a set of runs' totals.
type Figures struct {
	Runs     int
	Flagged  int
	Halts    int
	Billed   budget.Micros
	Notional budget.Micros
	// Review is the model dollars of the runs' review stages.
	Review budget.Micros
}

// FlagCount is how many runs of the period raised one flag.
type FlagCount struct {
	Name  string
	Count int
}

// Digest is one period's report.
type Digest struct {
	Period Period
	// Runs are the rows the report shows: the flagged ones first, then the
	// dearest of the rest (D325).
	Runs []Run
	// Stages covers every run that finished in the period, selected or not,
	// the same set Total counts.
	Stages []Stage
	// Total covers every run that finished in the period, selected or not:
	// the header's figures, which no size cap changes.
	Total Figures
	// Flags counts the period's flags by name, in FlagNames order.
	Flags []FlagCount
	// Recipes is each recipe's median cost over the period's runs, by the
	// name the report writes: a catalog recipe's own, "other" for every other
	// name. It is the per-recipe figure D326's third rule reads month over
	// month.
	Recipes map[string]budget.Micros
	// Dropped is how many selected rows the size cap left out.
	Dropped int
}

// Build selects the period's runs and returns its report. recs are one
// repository's records, in any order. A record that did not finish in the
// period, or whose run ID is not a run ID, is not one of the period's runs:
// the first has no period, and the second is not a run this repository's
// prefix names.
func Build(recs []runstore.Record, p Period) Digest {
	all := make([]Run, 0, len(recs))
	byRecipe := map[string][]budget.Micros{}
	total := Figures{}
	for i := range recs {
		rec := &recs[i]
		if rec.FinishedAt == nil || !p.Holds(*rec.FinishedAt) || !task.ValidRunID(rec.RunID) {
			continue
		}
		r := row(rec)
		all = append(all, r)
		name := recipeName(rec)
		byRecipe[name] = append(byRecipe[name], cost(r))
		total.Runs++
		if len(r.Flags) > 0 {
			total.Flagged++
		}
		if r.Status == string(runstore.StatusHalted) {
			total.Halts++
		}
		total.Billed = satAdd(total.Billed, r.Billed)
		total.Notional = satAdd(total.Notional, r.Notional)
		total.Review = satAdd(total.Review, reviewUSD(rec))
	}

	var flagged, rest []Run
	for _, r := range all {
		if len(r.Flags) > 0 {
			flagged = append(flagged, r)
		} else {
			rest = append(rest, r)
		}
	}
	byCost := func(rows []Run) {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := cost(rows[i]), cost(rows[j])
			if a != b {
				return a > b
			}
			return rows[i].ID < rows[j].ID
		})
	}
	byCost(flagged)
	byCost(rest)
	if n := min(TopRuns, max(MinDear, ceilShare(total.Runs))); n < len(rest) {
		rest = rest[:n]
	}

	d := Digest{
		Period:  p,
		Runs:    append(flagged, rest...),
		Stages:  stages(recs, p),
		Total:   total,
		Flags:   flagCounts(all),
		Recipes: recipeMedians(byRecipe),
	}
	return capTo(d, MaxBytes)
}

// recipeName is the record's recipe as the report names it: the catalog
// recipe's own name, and "other" for every name that is not one (G1).
//
// The name alone is not the recipe: a repository's own recipe file, and a
// project layer's, may reuse a catalog name and shadow the built-in one, so
// only a run whose recipe came from the catalog is counted under a catalog
// label -- otherwise one repository's own loop and its figures would sit
// where a reader reads the catalog's. It is the rule internal/stats publishes
// by, and a name the catalog retired keeps its figures under the name the
// catalog carries now. A record that names no recipe is one no catalog recipe
// accounts for, which is what "other" says.
func recipeName(rec *runstore.Record) string {
	if rec.Recipe == nil {
		return other
	}
	if name, ok := stats.ShippedName(rec.Recipe.Name, rec.Recipe.Source); ok {
		return name
	}
	return other
}

// recipeMedians is each recipe's median cost over the period's runs.
func recipeMedians(byRecipe map[string][]budget.Micros) map[string]budget.Micros {
	if len(byRecipe) == 0 {
		return nil
	}
	out := make(map[string]budget.Micros, len(byRecipe))
	for name, costs := range byRecipe {
		out[name] = median(costs)
	}
	return out
}

// median is the middle figure, and the mean of the two middle ones, rounded
// up, when there is an even number of them.
func median(vals []budget.Micros) budget.Micros {
	sorted := slices.Sorted(slices.Values(vals))
	switch n := len(sorted); {
	case n == 0:
		return 0
	case n%2 == 1:
		return sorted[n/2]
	default:
		return (sorted[n/2-1] + sorted[n/2] + 1) / 2
	}
}

// row is one record as the report's row.
func row(rec *runstore.Record) Run {
	r := Run{
		ID:       rec.RunID,
		Status:   known(statuses, string(rec.Status)),
		PR:       prNumber(rec),
		Base:     rec.BaseBranch != "",
		Reviews:  min(len(rec.Reviews), maxCount),
		Billed:   billed(rec),
		Notional: notional(rec),
		Flags:    flags(rec),
	}
	return r
}

// flags is the fixed set of flags one record raises (D325). Each is a
// comparison of figures the record carries, so a forged record can raise a
// flag but never make the report print a word of its own.
func flags(rec *runstore.Record) []Flag {
	var out []Flag
	if review, ok := stageCalls(rec, config.IsReviewStage); ok {
		implement, _ := stageCalls(rec, func(name string) bool { return name == "implement" })
		if review > ReviewCallsRatio*max(implement, ReviewCallsFloor) {
			out = append(out, Flag{Name: FlagReviewCalls})
		}
	}
	if fanOut(rec) {
		out = append(out, Flag{Name: FlagSubagents})
	}
	if rec.Status == runstore.StatusHalted || rec.Halt != nil {
		reason := ""
		if rec.Halt != nil {
			reason = string(rec.Halt.Reason)
		}
		out = append(out, Flag{Name: FlagHalt, Value: known(haltReasons, reason)})
	}
	if fixRounds(rec) > MaxFixRounds {
		out = append(out, Flag{Name: FlagFixRounds})
	}
	return out
}

// stageCalls is every call count the record's stages of a kind recorded, and
// whether any of them recorded one: a stage nobody counted is absent, not
// zero (G5).
func stageCalls(rec *runstore.Record, kind func(string) bool) (int, bool) {
	total, counted := 0, false
	for _, s := range rec.Stages {
		if !kind(s.Name) {
			continue
		}
		if n, ok := count(s.Calls); ok {
			total, counted = total+n, true
		}
	}
	return total, counted
}

// fanOut reports whether any stage of the record carries more sub-agent calls
// than a stage may.
func fanOut(rec *runstore.Record) bool {
	for _, s := range rec.Stages {
		if n, ok := count(s.Agents); ok && n > MaxStageAgents {
			return true
		}
	}
	return false
}

// fixRounds is how many fix stages the record ran.
func fixRounds(rec *runstore.Record) int {
	n := 0
	for _, s := range rec.Stages {
		if s.Name == "fix" {
			n++
		}
	}
	return n
}

// stages is every stage name's figures over the period's runs, in name order
// with the unknown names last.
func stages(recs []runstore.Record, p Period) []Stage {
	byName := map[string]*Stage{}
	for i := range recs {
		rec := &recs[i]
		if rec.FinishedAt == nil || !p.Holds(*rec.FinishedAt) || !task.ValidRunID(rec.RunID) {
			continue
		}
		for _, s := range rec.Stages {
			name := known(stageNames, s.Name)
			st := byName[name]
			if st == nil {
				st = &Stage{Name: name}
				byName[name] = st
			}
			if n, ok := count(s.Calls); ok {
				st.Calls, st.CallsKnown = st.Calls+n, true
			}
			if n, ok := count(s.Agents); ok {
				st.Agents, st.AgentsKnown = st.Agents+n, true
			}
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == other) != (names[j] == other) {
			return names[j] == other
		}
		return names[i] < names[j]
	})
	out := make([]Stage, 0, len(names))
	for _, name := range names {
		out = append(out, *byName[name])
	}
	return out
}

// flagCounts is how many runs raised each flag, in FlagNames order, leaving
// out the flags no run raised.
func flagCounts(runs []Run) []FlagCount {
	counts := map[string]int{}
	for _, r := range runs {
		for _, f := range r.Flags {
			counts[f.Name]++
		}
	}
	out := make([]FlagCount, 0, len(FlagNames))
	for _, name := range FlagNames {
		if n := counts[name]; n > 0 {
			out = append(out, FlagCount{Name: name, Count: n})
		}
	}
	return out
}

// capTo drops unflagged rows, cheapest first, until the rendered report fits
// max, and returns the digest that says how many it dropped. A flagged row is
// never dropped: it is why the report exists.
//
// It renders between drops because the cap is on the rendered bytes, and it
// takes the first drop before it measures again because the note reporting a
// drop costs bytes of its own: measured against the note-free report, a drop
// that freed less than the note costs would read as having made the report
// longer, and the loop would chase its own note.
func capTo(d Digest, max int) Digest {
	if len(Markdown(d)) <= max {
		return d
	}
	for {
		i := cheapestUnflagged(d.Runs)
		if i < 0 {
			return d
		}
		d.Runs = slices.Delete(d.Runs, i, i+1)
		d.Dropped++
		if len(Markdown(d)) <= max {
			return d
		}
	}
}

// cheapestUnflagged is the index of the cheapest row that carries no flag, or
// -1 when every row is flagged. It is the last of equals, so a tie drops from
// the end of the table.
func cheapestUnflagged(rows []Run) int {
	best := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if len(rows[i].Flags) > 0 {
			continue
		}
		if best < 0 || cost(rows[i]) < cost(rows[best]) {
			best = i
		}
	}
	return best
}

// cost is what the selection and the cap order a run by: what it billed plus
// what a subscription paid for it, which is the run's whole model and compute
// figure.
func cost(r Run) budget.Micros { return satAdd(r.Billed, r.Notional) }

// ceilShare is DearPercent of n, rounded up so the share is never under-
// selected.
func ceilShare(n int) int { return (n*DearPercent + 99) / 100 }

// billed is the record's billed dollars: its cost breakdown's total, which
// excludes what a subscription paid. A record with no breakdown at all is one
// that died before its cost settled (runner.updateCost writes the breakdown
// from the first figure it has), so the one figure it carries is the whole of
// what is known about it.
func billed(rec *runstore.Record) budget.Micros {
	if rec.Cost == nil {
		return usd(rec.CostUSD)
	}
	return usd(rec.Cost.TotalUSD)
}

// notional is the part of the record's model figure a subscription paid.
func notional(rec *runstore.Record) budget.Micros {
	if rec.Cost == nil {
		return 0
	}
	return usd(rec.Cost.ModelSubscriptionUSD())
}

// reviewUSD is the model dollars of the record's review stages.
func reviewUSD(rec *runstore.Record) budget.Micros {
	var total budget.Micros
	for _, s := range rec.Stages {
		if config.IsReviewStage(s.Name) {
			total = satAdd(total, usd(s.ModelUSD))
		}
	}
	return total
}

// usd is a record's dollar figure in micro-dollars. A figure that cannot be
// one -- negative, not a number, past any cost -- is no figure, and the
// report counts nothing rather than print it (G1's bounded numbers).
func usd(v float64) budget.Micros {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxUSD {
		return 0
	}
	return budget.Micros(math.Round(v * 1e6))
}

// count is a stage's recorded figure: absent when the pointer is nil, and no
// figure at all when it is outside what a count can be.
func count(v *int) (int, bool) {
	if v == nil || *v < 0 || *v > maxCount {
		return 0, false
	}
	return *v, true
}

// prNumber is the record's pull-request number, 0 when it names none or names
// one no pull request could have.
func prNumber(rec *runstore.Record) int {
	if rec.PR == nil || rec.PR.Number < 1 || rec.PR.Number > maxPR {
		return 0
	}
	return rec.PR.Number
}

// known is v when it is one of set's values, and "other" when it is not: the
// only way a word a record chose could reach a report, and it never does
// (G1).
func known(set []string, v string) string {
	if slices.Contains(set, v) {
		return v
	}
	return other
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
