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
//     order. The one byte this package does not write is the tail a registered
//     section hook returns (section 5.4): it is appended to a report that is
//     itself byte-identical, and it is byte-identical across runs only if the
//     hook is a pure function of the digest it is handed -- which is why the
//     size cap measures the core report alone (capTo).
//
// Nothing here reads a record by itself: Build is handed the records of one
// repository, which is the caller's business (G2).
package digest

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/stats"
	"github.com/dimipaun/fugaro/internal/task"
)

// MaxBytes caps the report the digest builds: a daily file is read in a pull
// request, and D321's figure is "under ~8 KiB".
//
// It is measured over the core report alone (capTo), never over the tail a
// registered extra-section hook adds (section 5.4): the sections are appended
// to a report already trimmed to fit, so a build that registered a hook may
// commit a file larger than this bound by however much its sections cost. The
// bound is the core's because the core is what owns the rows it drops;
// TestCapMeasuresTheCoreReportNotTheSectionHook pins that.
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
	// RepeatedCommandThreshold is the times one identical shell command may
	// run in one stage before the run is flagged (T1.4, D332): at the
	// threshold or past it the stage is looping on the same command.
	RepeatedCommandThreshold = 5
)

// The flag names: the report's own vocabulary, never a word a record chose.
const (
	FlagReviewCalls = "review_calls"
	FlagSubagents   = "subagents"
	FlagHalt        = "halt"
	FlagFixRounds   = "fix_rounds"
	// FlagRepeatedCommand is a stage that ran one identical shell command
	// RepeatedCommandThreshold times or more: the loop tripwire, which the
	// digest reports and nothing acts on (T1.4, G8).
	FlagRepeatedCommand = "repeated_command"
	// FlagReviewScopeDrift is a review the run's own commits no longer cover:
	// the range was narrowed to them and then moved off them, and it handed
	// the reviewer paths implement never wrote (T1.4, T5.2, T7.3). The name
	// is the digest's own and is deliberately not the halt reason it feeds
	// (runstore.HaltReviewScope): this one is a report line, and reading it
	// as a stop would be reading a flag as a halt.
	FlagReviewScopeDrift = "review_scope_drift"
	// FlagScopeDrift is a run that changed a path the scope its task declared
	// does not name (T8.3, D332): the expectation envelope's "roughly which
	// files or areas" against the paths the run really touched. It is report
	// only (G8) — D332 keeps a heuristic in the reports until the reports show
	// it is reliable, and this one is raised from two sets a record wrote — and
	// it is absent for a run whose task declared no scope, because a flag
	// raised from a set that is not there would say nothing.
	FlagScopeDrift = "scope_drift"
)

// FlagNames is every flag this report raises, in the order it prints.
var FlagNames = []string{FlagReviewCalls, FlagSubagents, FlagHalt, FlagFixRounds, FlagRepeatedCommand, FlagReviewScopeDrift, FlagScopeDrift}

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
		string(runstore.HaltExpectedExceeded), string(runstore.HaltOwnerDecision),
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
	Dim   Dim
	Key   string
	Start string
	End   string
}

// NewPeriod is the period date ("2006-01-02") falls in. It reuses
// PeriodKey's clock, so a week here is the ISO week in UTC every other
// period in this tree is: 2026-12-31 and 2027-01-01 are both 2026-W53.
func NewPeriod(dim Dim, date string) (Period, error) {
	key, start, err := PeriodKey(dim, date)
	if err != nil {
		return Period{}, err
	}
	t, err := time.Parse("2006-01-02", start)
	if err != nil {
		return Period{}, fmt.Errorf("bad period start %q", start)
	}
	end := start
	switch dim {
	case ByWeek:
		end = t.AddDate(0, 0, 6).Format("2006-01-02")
	case ByMonth:
		end = t.AddDate(0, 1, -1).Format("2006-01-02")
	case ByYear:
		end = t.AddDate(1, 0, -1).Format("2006-01-02")
	}
	return Period{Dim: dim, Key: key, Start: start, End: end}, nil
}

// Holds reports whether t is in the period. A period is closed: it holds the
// runs that finished in it (G4), so a run that has not finished is in no
// period, and a finish time is read in UTC whatever zone the record wrote.
func (p Period) Holds(t time.Time) bool {
	key, _, err := PeriodKey(p.Dim, t.UTC().Format("2006-01-02"))
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

// Expectation is one run's tier expectation beside what the run really spent
// and took: the per-run figures a tier calibration reads (T8.3, spec §6).
//
// Every run of the period that carries a tier is here, whether the report
// shows it as a row or not, because a calibration reads a window and not the
// rows a size cap trimmed. Every field is a bounded figure or a value of the
// closed tier vocabulary, so nothing here can carry a word a record chose (G1),
// and the report itself renders none of it: what a build does with the figures
// is its section hook's business (section.go).
type Expectation struct {
	// Tier is the tier the run was dispatched at, one of tier.Tiers.
	Tier string
	// ExpectedUSD and ExpectedMinutes are what that tier expected of the run:
	// the figures the record carries, which a repository's own layers may have
	// tightened and which are never a cap (G6). Both are read the way every
	// other figure a record carries is — bounded, and no figure at all when
	// they cannot be one — so zero reads as "no expectation" and never as a
	// free run (G1, G5).
	ExpectedUSD     budget.Micros
	ExpectedMinutes int64
	// Billed is what the run's model calls cost, in the report's own unit.
	Billed budget.Micros
	// Duration is how long the run took, and DurationKnown is false when the
	// record's window cannot give one — no start, or a finish before it. An
	// unknown duration is not a zero one: a reader that has no figure leaves
	// the run out rather than read it as instant (G5).
	Duration      time.Duration
	DurationKnown bool
	// Governance is true when a governance tripwire halted the run: what such
	// a run spent is the tripwire's figure and not the tier's, so a
	// calibration leaves it out of the window (spec §6).
	Governance bool
	// Finished is when the run finished, UTC: the window a calibration reads
	// is ordered by it.
	Finished time.Time
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

// ClassCount is one class of the period's failures: how many runs it holds,
// what they billed, and one of their run IDs. Every field is a figure, a run
// ID or a value of the closed vocabulary, so a row can hold no word a record
// chose.
type ClassCount struct {
	Class failure.Class
	Runs  int
	// Billed is the class's runs' billed dollars, the figure the report's run
	// table prints in its own column.
	Billed budget.Micros
	// Example is one run of the class, the newest of them: a run ID begins
	// with the run's own start time, so the figure is the same whatever order
	// the records arrive in (G3).
	Example string
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
	// Failures is why the period's runs did not succeed, one row per class
	// that holds any of them, in failure.Classes() order. A run that did not
	// fail is in no row: a class is a record's own ending, never a derivation
	// (design §3, D1464).
	Failures []ClassCount
	// Recipes is each recipe's median cost over the period's runs, by the
	// name the report writes: a catalog recipe's own, "other" for every other
	// name. It is the per-recipe figure D326's third rule reads month over
	// month.
	Recipes map[string]budget.Micros
	// Expectations is every run of the period that carries a tier, ordered by
	// its finish time: the figures a calibration reads (T8.3). The report
	// renders none of them, and the order is the runs' own so that a hook
	// reading the window is a pure function of this digest (G3).
	Expectations []Expectation
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
	byClass := map[failure.Class]*ClassCount{}
	var expectations []Expectation
	total := Figures{}
	for i := range recs {
		rec := &recs[i]
		if rec.FinishedAt == nil || !p.Holds(*rec.FinishedAt) || !task.ValidRunID(rec.RunID) {
			continue
		}
		r := row(rec)
		all = append(all, r)
		if exp, ok := expectation(rec); ok {
			expectations = append(expectations, exp)
		}
		name := recipeName(rec)
		byRecipe[name] = append(byRecipe[name], cost(r))
		if class := runview.ClassOf(rec); class != "" {
			c := byClass[class]
			if c == nil {
				c = &ClassCount{Class: class}
				byClass[class] = c
			}
			c.Runs++
			c.Billed = satAdd(c.Billed, r.Billed)
			if r.ID > c.Example {
				c.Example = r.ID
			}
		}
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
		Period:       p,
		Runs:         append(flagged, rest...),
		Stages:       stages(recs, p),
		Total:        total,
		Flags:        flagCounts(all),
		Failures:     classCounts(byClass),
		Recipes:      recipeMedians(byRecipe),
		Expectations: byFinished(expectations),
	}
	return capTo(d, MaxBytes)
}

// expectation is one record as a calibration reads it, and whether it is one
// at all: a run that carries no tier has no expectation to compare against,
// and a tier the vocabulary does not know is a block no runner resolved
// (runstore.TierRecord.Bounded), which is the answer the field's absence
// gives.
func expectation(rec *runstore.Record) (Expectation, bool) {
	t := rec.Tier.Bounded()
	if t == nil {
		return Expectation{}, false
	}
	exp := Expectation{
		Tier:            string(t.Tier),
		ExpectedUSD:     usd(t.ExpectedUSD),
		ExpectedMinutes: expectedMinutes(t.ExpectedMinutes),
		Billed:          billed(rec),
		Governance:      governanceHalt(rec),
		Finished:        rec.FinishedAt.UTC(),
	}
	if !rec.StartedAt.IsZero() {
		if d := rec.FinishedAt.Sub(rec.StartedAt); d >= 0 {
			exp.Duration, exp.DurationKnown = d, true
		}
	}
	return exp, true
}

// expectedMinutes is a record's expected duration in minutes, bounded the way
// a count is: a figure outside what an expectation can be is no figure, which
// zero already means here.
func expectedMinutes(v int64) int64 {
	if v < 0 || v > maxCount {
		return 0
	}
	return v
}

// governanceHalt is whether a governance tripwire stopped the run (T6.1,
// D329): the reasons a checkpoint carries, which a calibration leaves out of
// its window. A budget or credential halt is not one of them — a run that ran
// out of money or a credential says nothing about what its tier expected.
func governanceHalt(rec *runstore.Record) bool {
	if rec.Halt == nil {
		return false
	}
	return slices.Contains(runstore.GovernanceHaltReasons(), rec.Halt.Reason)
}

// byFinished is the expectations ordered by finish time, and by the figures
// themselves where two runs finished in the same second: Build is handed the
// records in any order, so an order the hook reads a window off has to be the
// runs' own and not the caller's.
func byFinished(exp []Expectation) []Expectation {
	sort.Slice(exp, func(i, j int) bool {
		a, b := exp[i], exp[j]
		if !a.Finished.Equal(b.Finished) {
			return a.Finished.Before(b.Finished)
		}
		return lessExpectation(a, b)
	})
	return exp
}

// lessExpectation orders two expectations that finished at the same instant by
// every figure they carry. Two runs that agree on all of them are the same
// entry as far as a reader of the window is concerned, so the order between
// them cannot change what one computes.
func lessExpectation(a, b Expectation) bool {
	switch {
	case a.Tier != b.Tier:
		return a.Tier < b.Tier
	case a.Billed != b.Billed:
		return a.Billed < b.Billed
	case a.Duration != b.Duration:
		return a.Duration < b.Duration
	case a.ExpectedUSD != b.ExpectedUSD:
		return a.ExpectedUSD < b.ExpectedUSD
	case a.ExpectedMinutes != b.ExpectedMinutes:
		return a.ExpectedMinutes < b.ExpectedMinutes
	}
	return !a.Governance && b.Governance
}

// classCounts is the period's failures in the vocabulary's order, one row per
// class that holds any run.
//
// The class is read by runview.ClassOf, the one reader of a record's ending
// (D1454): a record written before the field shipped carries a reason
// instead, and counting classes any other way would drop it from a table that
// is closed over the period's failures.
func classCounts(byClass map[failure.Class]*ClassCount) []ClassCount {
	if len(byClass) == 0 {
		return nil
	}
	out := make([]ClassCount, 0, len(byClass))
	for _, class := range failure.Classes() {
		if c := byClass[class]; c != nil {
			out = append(out, *c)
		}
	}
	return out
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
	if repeatedCommand(rec) {
		out = append(out, Flag{Name: FlagRepeatedCommand})
	}
	if reviewScopeDrift(rec) {
		out = append(out, Flag{Name: FlagReviewScopeDrift})
	}
	if scopeDrift(rec) {
		out = append(out, Flag{Name: FlagScopeDrift})
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

// repeatedCommand reports whether any stage of the record ran one identical
// shell command RepeatedCommandThreshold times or more: a stage looping on
// the same command spends calls without getting anywhere. The record carries
// the count alone, so nothing here reads a command (T1.4).
func repeatedCommand(rec *runstore.Record) bool {
	for _, s := range rec.Stages {
		if n, ok := count(s.Repeats); ok && n >= RepeatedCommandThreshold {
			return true
		}
	}
	return false
}

// reviewScopeDrift reports whether the record says the reviewer was handed
// paths the run's own commits never wrote: the range was narrowed to those
// commits and then moved off them (runstore.ReviewScopeRangeMoved), which is
// the mismatch T7.3 stops on. A range kept wider on purpose -- the one a
// branch that merged its base leaves, a follow-up's, a review run's, an
// autofix's -- records outside paths as a fact about the run and is never a
// mismatch (D332), so it is not drift and is not flagged, and neither is a
// moved range that handed the reviewer nothing implement had not written. A
// truncated comparison is not flagged either: Outside is taken over the two
// bounded lists (runstore.NewReviewScope), so a capped ImplementFiles can
// name a path the run did change, and a flag raised from a set that is not
// the whole one would be a false positive (runstore.MaxReviewScopeFiles).
func reviewScopeDrift(rec *runstore.Record) bool {
	sc := rec.ReviewScope
	return sc != nil && !sc.Truncated && sc.WhyNot == runstore.ReviewScopeRangeMoved && len(sc.Outside) > 0
}

// scopeDrift reports whether the record says the run changed a path the scope
// its task declared does not name (T8.3, D332): the expectation envelope's
// "roughly which files or areas" against what the run really touched. It is
// report only (G8) — nothing halts on it — and it is raised from two sets the
// record wrote, so a forged record can raise it and nothing else.
//
// A run whose task declared no scope, and a record written before the field
// existed, carries none and is never flagged: with no scope there is nothing
// the paths could be outside of. The changed-file set is bounded the way every
// other figure a record carries is, and a set the bound truncated can only hide
// drift and never invent it, which is the opposite of the review-scope
// comparison above: there the flag rests on a difference between two capped
// sets, and here on one path that is in the set and outside the scope whatever
// the rest of the set holds.
func scopeDrift(rec *runstore.Record) bool {
	scope := declaredScope(rec.ExpectedScope)
	files := rec.ChangedFiles.Bounded()
	if len(scope) == 0 || files == nil {
		return false
	}
	for _, p := range files.Paths {
		if !matchesAny(scope, p) {
			return true
		}
	}
	return false
}

// declaredScope is a record's expected scope as a reader may use it. The
// record is an object the job's service account can write, so the list read
// back is not trusted to be the one a task declared: it is bounded again the
// way the task parser bounds it (task.MaxDeclaredPaths entries of at most
// task.MaxDeclaredPathBytes), and a pattern the matcher cannot read is dropped
// rather than kept as one nothing matches — a pattern no path could match
// would raise the flag for every run that carried it.
func declaredScope(scope []string) []string {
	if len(scope) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(scope), task.MaxDeclaredPaths))
	for _, p := range scope {
		if len(out) == task.MaxDeclaredPaths {
			break
		}
		if strings.TrimSpace(p) == "" || len(p) > task.MaxDeclaredPathBytes {
			continue
		}
		if !doublestar.ValidatePattern(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// matchesAny reports whether a changed path is one of the patterns the scope
// declared, by the same doublestar matcher rigor matches a run's declared
// paths with (F14): a scope is written the way a task's own paths are, so the
// two are read by one rule.
func matchesAny(scope []string, path string) bool {
	for _, p := range scope {
		if ok, err := doublestar.Match(p, path); err == nil && ok {
			return true
		}
	}
	return false
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
//
// The bytes it measures are the CORE report's (markdownCore), not Markdown's:
// a registered section hook's tail is appended after the cap, so its bytes
// never count toward it and the hook is not run once per dropped row. A hook
// therefore needs no size discipline of its own, and a report that fits the cap
// does so whatever section a build adds.
func capTo(d Digest, max int) Digest {
	if len(markdownCore(d)) <= max {
		return d
	}
	for {
		i := cheapestUnflagged(d.Runs)
		if i < 0 {
			return d
		}
		d.Runs = slices.Delete(d.Runs, i, i+1)
		d.Dropped++
		if len(markdownCore(d)) <= max {
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
