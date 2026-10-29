package digest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// The fixed rules (D326) and their names: the report's own vocabulary, never
// a word a record or a committed file chose.
const (
	// RuleFlagRecurs is one flag raised in FlagWeeks of the last FlagWindow
	// weekly reports.
	RuleFlagRecurs = "flag_recurs"
	// RuleReviewShare is the review share of cost rising ReviewShareMonths
	// months running.
	RuleReviewShare = "review_share_rising"
	// RuleRecipeMedian is one recipe's median cost up RecipeRiseNum/RecipeRiseDen
	// on last month's.
	RuleRecipeMedian = "recipe_median_up"
	// RuleHaltsHigh is halts above HaltShareNum/HaltShareDen of the runs,
	// HaltWeeks weeks running.
	RuleHaltsHigh = "halts_high"
)

// RuleNames is every rule, in the order a report lists them.
var RuleNames = []string{RuleFlagRecurs, RuleReviewShare, RuleRecipeMedian, RuleHaltsHigh}

// The rules' thresholds (D326).
const (
	// FlagWeeks of the last FlagWindow weekly reports must raise a flag.
	FlagWeeks  = 3
	FlagWindow = 4
	// ReviewShareMonths is how many months of rises the review share must
	// run: one month more than rises, a rise being between two of them.
	ReviewShareMonths = 3
	// RecipeRiseNum/RecipeRiseDen is what "up 50%" means: this month's median
	// is at least this much of last month's.
	RecipeRiseNum = 3
	RecipeRiseDen = 2
	// HaltShareNum/HaltShareDen is what "above 10%" means, and HaltWeeks is
	// how many weeks running it must hold.
	HaltShareNum = 1
	HaltShareDen = 10
	HaltWeeks    = 2
)

// Pattern is one pattern a fixed rule found in the reports committed before
// this one. A report is committed into a repository that may be public, so
// every field here is a value this package chose or a bounded figure: a rule
// name, a period key, a flag's name, a catalog recipe's name or "other", and
// figures as exact decimal strings. A name outside those sets is in no
// pattern (G1).
// The json keys are the document's own, not Go's field names: fugaro runs
// digest prints a pattern under --json, beside a summary block whose keys are
// snake_case, so a consumer reads one shape rather than two.
type Pattern struct {
	// Rule is the rule that fired, one of RuleNames.
	Rule string `json:"rule"`
	// Subject is what the rule counted, for the rules that count one thing: a
	// flag's name, or a recipe's name -- a catalog recipe's own, "other" for
	// the runs no catalog recipe names. It is empty for the rules that count
	// a whole period, and the key is then absent rather than empty.
	Subject string `json:"subject,omitempty"`
	// Periods are the period keys the rule read, oldest first, and Figures
	// are its figures, one per period, as exact decimal strings: whether a
	// week raised the flag, the review share of a month's cost, a recipe's
	// median cost in dollars, or a week's halts over its runs.
	Periods []string `json:"periods"`
	Figures []string `json:"figures"`
}

// Patterns is the fixed rules (D326) read over the summaries of the reports
// committed before this one and this report's own. prev is in any order and
// may repeat a period; the rules run in RuleNames order over periods in key
// order, so the same summaries give the same patterns (G3).
//
// The rules are a table, not a search: each reads the periods it names, and a
// summary whose period key is not one this package writes is in none of them
// (G1).
func Patterns(prev []Summary, cur Summary) []Pattern {
	all := make([]Summary, 0, len(prev)+1)
	all = append(append(all, prev...), cur)
	series := byPeriod(all)

	var out []Pattern
	out = append(out, flagRecurs(series[ByWeek])...)
	out = append(out, reviewShareRising(series[ByMonth])...)
	out = append(out, recipeMedianUp(series[ByMonth])...)
	out = append(out, haltsHigh(series[ByWeek])...)
	return out
}

// byPeriod groups summaries by the period each names, oldest first and one
// per key: a period reported twice counts once, the later summary of it being
// the one this report's own figures belong to. A key of no shape this package
// writes is in no group (G1).
func byPeriod(all []Summary) map[Dim][]Summary {
	kept := make([]Summary, 0, len(all))
	for _, s := range all {
		if _, ok := periodDim(s.Period); ok {
			kept = append(kept, s)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Period < kept[j].Period })

	out := map[Dim][]Summary{}
	for _, s := range kept {
		dim, _ := periodDim(s.Period)
		if n := len(out[dim]); n > 0 && out[dim][n-1].Period == s.Period {
			out[dim][n-1] = s
			continue
		}
		out[dim] = append(out[dim], s)
	}
	return out
}

// The period keys this package writes, by shape. A key read from a committed
// file is printed only when it is one of them (G1).
var (
	dayKeyRE   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	weekKeyRE  = regexp.MustCompile(`^\d{4}-W\d{2}$`)
	monthKeyRE = regexp.MustCompile(`^\d{4}-\d{2}$`)
	yearKeyRE  = regexp.MustCompile(`^\d{4}$`)
)

// periodDim is the period a report's key names, and whether it names one this
// package writes at all.
func periodDim(key string) (Dim, bool) {
	switch {
	case dayKeyRE.MatchString(key):
		return ByDay, true
	case weekKeyRE.MatchString(key):
		return ByWeek, true
	case monthKeyRE.MatchString(key):
		return ByMonth, true
	case yearKeyRE.MatchString(key):
		return ByYear, true
	}
	return "", false
}

// window is the newest n summaries of a series, and whether they are n
// periods running: a rule reads the last four weeks, not the last four weeks
// a repository happens to have committed, so a gap in the series means the
// rule does not fire (G5's absent, never zero). It is the same for a series
// that holds fewer than n of them.
func window(series []Summary, n int) ([]Summary, bool) {
	if len(series) < n {
		return nil, false
	}
	recent := series[len(series)-n:]
	for i := 1; i < len(recent); i++ {
		if !consecutive(recent[i-1].Period, recent[i].Period) {
			return nil, false
		}
	}
	return recent, true
}

// consecutive is whether b is the period right after a: the next week of the
// year, across its end, or the next month, across December.
func consecutive(a, b string) bool {
	switch {
	case weekKeyRE.MatchString(a) && weekKeyRE.MatchString(b):
		ya, wa := weekOf(a)
		yb, wb := weekOf(b)
		return yb == ya && wb == wa+1 || yb == ya+1 && wa == isoWeeks(ya) && wb == 1
	case monthKeyRE.MatchString(a) && monthKeyRE.MatchString(b):
		ya, ma := monthOf(a)
		yb, mb := monthOf(b)
		return yb == ya && mb == ma+1 || yb == ya+1 && ma == 12 && mb == 1
	}
	return false
}

// weekOf is an ISO week key's year and week. The key is one weekKeyRE
// matched, so both parse.
func weekOf(key string) (year, week int) {
	year, _ = strconv.Atoi(key[:4])
	week, _ = strconv.Atoi(key[6:])
	return year, week
}

// monthOf is a month key's year and month.
func monthOf(key string) (year, month int) {
	year, _ = strconv.Atoi(key[:4])
	month, _ = strconv.Atoi(key[5:])
	return year, month
}

// isoWeeks is how many ISO weeks a year has: 53 when it begins on a Thursday
// or is a leap year beginning on a Wednesday, 52 otherwise. 2026 is such a
// year, which is what makes 2026-W53 and 2027-W01 consecutive.
func isoWeeks(year int) int {
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	if jan1.Weekday() == time.Thursday || (jan1.Weekday() == time.Wednesday && isLeap(year)) {
		return 53
	}
	return 52
}

// isLeap is whether year is a leap year in the Gregorian calendar.
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// flagRecurs is D326's first rule.
func flagRecurs(weeks []Summary) []Pattern {
	recent, ok := window(weeks, FlagWindow)
	if !ok {
		return nil
	}
	var out []Pattern
	for _, name := range FlagNames {
		p := Pattern{Rule: RuleFlagRecurs, Subject: name}
		raised := 0
		for _, w := range recent {
			hit := "0"
			if w.Flags[name] > 0 {
				hit, raised = "1", raised+1
			}
			p.Periods = append(p.Periods, w.Period)
			p.Figures = append(p.Figures, hit)
		}
		if raised >= FlagWeeks {
			out = append(out, p)
		}
	}
	return out
}

// reviewShareRising is D326's second rule. A month whose runs cost nothing at
// all has no share to compare and the rule does not fire on it (G5).
func reviewShareRising(months []Summary) []Pattern {
	recent, ok := window(months, ReviewShareMonths+1)
	if !ok {
		return nil
	}
	p := Pattern{Rule: RuleReviewShare}
	before := int64(0)
	for i, m := range recent {
		review, okReview := micros(m.Review)
		billed, okBilled := micros(m.Billed)
		notional, okNotional := micros(m.Notional)
		if !okReview || !okBilled || !okNotional {
			return nil
		}
		total := billed + notional
		if total <= 0 {
			return nil
		}
		share := part(review, total)
		if i > 0 && share <= before {
			return nil
		}
		before = share
		p.Periods = append(p.Periods, m.Period)
		p.Figures = append(p.Figures, shareText(share))
	}
	return []Pattern{p}
}

// recipeMedianUp is D326's third rule.
func recipeMedianUp(months []Summary) []Pattern {
	recent, ok := window(months, 2)
	if !ok {
		return nil
	}
	prev, cur := recent[0], recent[1]
	var out []Pattern
	for _, name := range sharedRecipes(prev, cur) {
		before, okBefore := micros(prev.Recipes[name])
		after, okAfter := micros(cur.Recipes[name])
		if !okBefore || !okAfter || before <= 0 {
			continue
		}
		if after*RecipeRiseDen < before*RecipeRiseNum {
			continue
		}
		out = append(out, Pattern{
			Rule: RuleRecipeMedian, Subject: name,
			Periods: []string{prev.Period, cur.Period},
			Figures: []string{USD6(before), USD6(after)},
		})
	}
	return out
}

// sharedRecipes are the recipes two months both carry a median for, in name
// order with "other" last. A key outside the catalog is not one this package
// writes, and printing it would print a word a record chose (G1).
func sharedRecipes(a, b Summary) []string {
	var out []string
	for name := range a.Recipes {
		if !knownRecipe(name) {
			continue
		}
		if _, ok := b.Recipes[name]; ok {
			out = append(out, name)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == other) != (out[j] == other) {
			return out[j] == other
		}
		return out[i] < out[j]
	})
	return out
}

// haltsHigh is D326's fourth rule.
func haltsHigh(weeks []Summary) []Pattern {
	recent, ok := window(weeks, HaltWeeks)
	if !ok {
		return nil
	}
	p := Pattern{Rule: RuleHaltsHigh}
	for _, w := range recent {
		if w.Runs <= 0 || w.Runs > maxCount || w.Halts < 0 || w.Halts > maxCount {
			return nil
		}
		if w.Halts*HaltShareDen <= w.Runs*HaltShareNum {
			return nil
		}
		p.Periods = append(p.Periods, w.Period)
		p.Figures = append(p.Figures, fmt.Sprintf("%d/%d", w.Halts, w.Runs))
	}
	return []Pattern{p}
}

// PatternsMarkdown is the patterns as the report's own section, for a report
// to append. It renders what Patterns found and nothing else, so the section
// carries no person and no text a record or a committed file wrote (G1).
func PatternsMarkdown(patterns []Pattern) []byte {
	var b strings.Builder
	b.WriteString("## Patterns\n\n")
	if len(patterns) == 0 {
		b.WriteString("No pattern rule fired.\n")
		return []byte(b.String())
	}
	b.WriteString("The fixed rules (D326) read over this report and the ones committed before it, oldest period first.\n\n")
	for _, p := range patterns {
		fmt.Fprintf(&b, "- `%s`", p.Rule)
		if p.Subject != "" {
			fmt.Fprintf(&b, " `%s`", p.Subject)
		}
		b.WriteString(": ")
		for i, period := range p.Periods {
			if i > 0 {
				b.WriteString(", ")
			}
			figure := ""
			if i < len(p.Figures) {
				figure = p.Figures[i]
			}
			fmt.Fprintf(&b, "%s %s", period, figure)
		}
		b.WriteString(".\n")
	}
	return []byte(b.String())
}

// ReadSummary is one committed report's summary block: the bytes of its
// fenced json block, decoded strictly -- a field this report does not write is
// an error -- and checked by shape.
//
// A report is committed into a repository a person may edit by hand, so
// nothing here is trusted: a block outside those shapes is refused rather than
// printed later (G1), and no error here repeats any part of it.
func ReadSummary(file []byte) (Summary, error) {
	block, ok := fencedBlock(file)
	if !ok {
		return Summary{}, errors.New("the report carries no json block")
	}
	dec := json.NewDecoder(bytes.NewReader(block))
	dec.DisallowUnknownFields()
	var s Summary
	if err := dec.Decode(&s); err != nil {
		return Summary{}, errors.New("the summary block is not one this report writes")
	}
	if dec.More() {
		return Summary{}, errors.New("the summary block carries more than one value")
	}
	if err := s.check(); err != nil {
		return Summary{}, err
	}
	return s, nil
}

// ReadSummaries is the summary block of every committed report, in the order
// given, and a name for each report it skipped: the report's position in the
// list, never anything the file held (G1).
//
// A report is committed into a repository a person may edit by hand, and one
// unreadable file must not stop the report being written: a file this package
// cannot read is left out of the patterns and named, so the report says it
// read less than it was given.
func ReadSummaries(reports [][]byte) ([]Summary, []string) {
	summaries := make([]Summary, 0, len(reports))
	var skipped []string
	for i, file := range reports {
		s, err := ReadSummary(file)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("report %d", i+1))
			continue
		}
		summaries = append(summaries, s)
	}
	return summaries, skipped
}

// RollUp is the year's summary of its monthly ones (F24): a yearly report has
// no runs of its own to read, so it is the months added up, and each recipe's
// median is the median of the months' medians, the only per-recipe figure a
// summary carries.
//
// months are one year's, in any order, and year is which year is being rolled
// up: the caller knows it -- a report is of a period, not of whatever month
// happens to sort newest in the folder -- so a committed month of another
// year, ahead of the report's own period or behind it, is not one of the
// year's months and contributes nothing. A summary whose key is not a month
// key this package writes, whose figures are not ones this package writes, or
// which repeats a month another one already named, is likewise not one of the
// year's months (G1, G5).
//
// The block carries the period's own dates whatever the months are, so it also
// carries how many months it added up: fewer than twelve is a partial year,
// and a reader of the committed file is told so rather than left to count the
// rows of a table (G5).
func RollUp(year string, months []Summary) Summary {
	if !yearKeyRE.MatchString(year) {
		return Summary{}
	}
	kept := make([]Summary, 0, len(months))
	for _, m := range byPeriod(months)[ByMonth] {
		if m.Period[:4] == year && m.figuresOK() {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return Summary{}
	}
	s := Summary{Period: year, Start: year + "-01-01", End: year + "-12-31", Months: len(kept)}

	var billed, notional, review budget.Micros
	medians := map[string][]budget.Micros{}
	for _, m := range kept {
		s.Runs += m.Runs
		s.Shown += m.Shown
		s.Flagged += m.Flagged
		s.Dropped += m.Dropped
		s.Halts += m.Halts
		b, _ := micros(m.Billed)
		n, _ := micros(m.Notional)
		r, _ := micros(m.Review)
		billed, notional, review = satAdd(billed, b), satAdd(notional, n), satAdd(review, r)
		for _, name := range FlagNames {
			if c := m.Flags[name]; c > 0 {
				if s.Flags == nil {
					s.Flags = map[string]int{}
				}
				s.Flags[name] += c
			}
		}
		for name, v := range m.Recipes {
			if !knownRecipe(name) {
				continue
			}
			if med, ok := micros(v); ok {
				medians[name] = append(medians[name], med)
			}
		}
	}
	s.Billed, s.Notional, s.Review = USD6(billed), USD6(notional), USD6(review)
	for name, vals := range medians {
		if s.Recipes == nil {
			s.Recipes = map[string]string{}
		}
		s.Recipes[name] = USD6(median(vals))
	}
	return s
}

// check refuses a summary block this report did not write: its period, its
// dates, its figures and its counts.
func (s Summary) check() error {
	dim, ok := periodDim(s.Period)
	if !ok {
		return errors.New("the period key is not one this report writes")
	}
	// The period's own last day is part of its shape, not a second date to
	// compare: a month that says it ends on the 30th is a block this report
	// did not write, and one a reader would take for a shorter month.
	p, err := NewPeriod(dim, s.Start)
	if err != nil || p.Key != s.Period || p.Start != s.Start || p.End != s.End {
		return errors.New("the period is not one this report writes, from its first day to its last")
	}
	// A year's block is its months added up (F24), so it says how many of
	// them it added up: a year that does not, or another period that does, is
	// not a block this report wrote, and a reader is never left to take a
	// partial year for a whole one (G5).
	if dim == ByYear {
		if s.Months < 1 || s.Months > 12 {
			return errors.New("a year does not say how many months it added up")
		}
	} else if s.Months != 0 {
		return errors.New("a period that is not a year says how many months it added up")
	}
	if !s.figuresOK() {
		return errors.New("a figure or a count is not one this report writes")
	}
	return nil
}

// figuresOK is whether every figure and count a rule or a roll-up reads is
// one this package writes: an exact dollar string, or a count within bounds.
func (s Summary) figuresOK() bool {
	for _, v := range []string{s.Billed, s.Notional, s.Review} {
		if _, ok := micros(v); !ok {
			return false
		}
	}
	for _, n := range []int{s.Runs, s.Shown, s.Flagged, s.Dropped, s.Halts} {
		if n < 0 || n > maxCount {
			return false
		}
	}
	for _, v := range s.Recipes {
		if _, ok := micros(v); !ok {
			return false
		}
	}
	for _, n := range s.Flags {
		if n < 0 || n > maxCount {
			return false
		}
	}
	return true
}

// knownRecipe is whether a summary's recipe key is one this package writes: a
// catalog recipe's name, or "other" for the runs no catalog recipe names. A
// key outside that set is a word a record chose, and no pattern, section or
// roll-up prints one (G1).
func knownRecipe(name string) bool {
	return name == other || slices.Contains(recipe.CatalogNames(), name)
}

// micros is a report's dollar string as exact micro-dollars. A string that is
// not one -- a float, a negative, a figure past any cost this product
// produces -- is no figure, and a rule that needs it does not fire (G1's
// bounded numbers).
func micros(s string) (budget.Micros, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(frac) > 6 {
		return 0, false
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	for len(frac) < 6 {
		frac += "0"
	}
	dollars, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || dollars > int64(maxUSD) {
		return 0, false
	}
	micro, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return budget.Micros(dollars*1_000_000 + micro), true
}

// part is review's part of total in millionths, from exact integer figures.
// The division truncates -- a rise smaller than a millionth of the cost reads
// as no rise -- and no figure here goes through a float (G3). The figures are
// bounded by micros, so the multiplication cannot overflow.
func part(review, total budget.Micros) int64 {
	if review <= 0 {
		return 0
	}
	if review > total {
		review = total
	}
	return int64(review) * 1_000_000 / int64(total)
}

// shareText is a part in millionths as an exact decimal string: 170114 is
// 0.170114.
func shareText(millionths int64) string {
	return fmt.Sprintf("%d.%06d", millionths/1_000_000, millionths%1_000_000)
}

// fencedBlock is the report's ```json block, the bytes between its fence
// lines.
func fencedBlock(file []byte) ([]byte, bool) {
	lines := strings.Split(string(file), "\n")
	for i, l := range lines {
		if l != "```json" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "```" {
				return []byte(strings.Join(lines[i+1:j], "\n")), true
			}
		}
	}
	return nil, false
}
