// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// This file is the A/B report (docs/model-ab-testing, plan task A4.3, decisions
// D560, D563, D564, D565, D562, D570): the rows `fugaro ab` prints, built from
// run records alone.
//
// The pairs, the signals and the statistics are ee/ab's; this is where they are
// grouped so a person can read them, and two promises hold it together.
//
// A row is a rigor mode, a tier and two coder models, and nothing else (D560):
// no person, no repository, no run, no task text. What a reader was never
// handed it cannot publish, and the records it is handed carry all of those.
//
// The report reports and never switches (R3): the recommendation is a value
// printed beside the figures it was read through, and nothing here writes a
// default anywhere.
package report

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/dimipaun/fugaro/ee/ab"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// ABTierNone is the group of pairs whose runs carried no expectation tier
// (D564): the tier is read ex ante, from the task, and most runs carry none, so
// they are grouped as untiered rather than left out.
const ABTierNone = "untiered"

// implementStage is the stage the coder runs, as the runner dispatches it: the
// stage whose pinned model is that arm's coder model (D555 moves the coder
// alone, so this one stage is where the two arms differ).
const implementStage = "implement"

// ABRow is one line of `fugaro ab`: a group and what its pairs did.
type ABRow struct {
	// Mode is the rigor mode the group's pairs resolved, ModeNone for a pair
	// whose records carry no mode this build knows — the same reading, and the
	// same word, the by-mode spend breakdown gives.
	Mode string
	// Tier is the tier the runs carried (D564), ABTierNone for a pair that
	// carried none.
	Tier string
	// Champion and Challenger are the two arms' coder models.
	Champion, Challenger string
	// Pairs is the group's valid pairs, and Wins, Losses and Ties the pairs the
	// challenger took, lost and drew (D563).
	Pairs              int
	Wins, Losses, Ties int
	// ChampionHalts and ChallengerHalts are how many of the group's pairs each
	// arm halted in (D567). A halt is already a loss; this is the figure that
	// says how often the cap rather than the work decided a pair.
	ChampionHalts, ChallengerHalts int
	// FollowUps is how many runs continue the champion's pull request across
	// the group's pairs (D562). It is context and never a signal: the shadow
	// opens no pull request, so rework cannot enter a paired comparison.
	FollowUps int
	// Rec is the rule's answer for the group (D565), with the figures it was
	// read through.
	Rec ab.Recommendation
}

// ABReport is the whole of `fugaro ab`: one row per group, in a fixed order,
// and the valid pairs they were built from.
type ABReport struct {
	Rows []ABRow
	// Pairs is every valid pair, which is the sum of the rows' pairs: a pair R2
	// refuses is in no figure at all (D571) and is counted here no more than in
	// a row.
	Pairs int
}

// BuildAB groups records into the report's rows (D560, D564): a row per rigor
// mode × tier × model pair, in that order and then by the champion's model and
// the challenger's.
//
// The order is part of the contract and not an accident of the input: the same
// records must give the same report (D563), and a bucket listing hands its
// records over in whatever order it likes. ab.Pairs does the pairing and R2's
// refusals, ab.Score the signals and ab.Recommend the rule, so a change to any
// of them reaches this report without a second copy of it here.
//
// rates is the project's exchange rate per mode (D565): the extra cost a pair
// may carry before the mode keeps the champion. A mode the table leaves out —
// and nil, which is the whole table — takes the figure Fugaro ships.
func BuildAB(records []runstore.Record, rates map[rigor.Mode]float64) ABReport {
	pairs := ab.Pairs(records)
	rep := ABReport{Rows: []ABRow{}, Pairs: len(pairs)}
	groups := map[abGroup]*abAcc{}
	order := make([]abGroup, 0, len(pairs))
	for _, p := range pairs {
		g := groupOf(p)
		a, ok := groups[g]
		if !ok {
			a = &abAcc{group: g}
			groups[g] = a
			order = append(order, g)
		}
		a.add(p)
	}
	slices.SortFunc(order, func(a, b abGroup) int { return a.compare(b) })
	for _, g := range order {
		rep.Rows = append(rep.Rows, groups[g].row(abRate(rates, g.mode)))
	}
	return rep
}

// abGroup is what a row is keyed by: the mode and tier the pair's runs
// resolved, and the two coder models (D560, D564).
//
// mode is the mode ab.Score reads the signals at and modeKey the word the row
// prints. They differ for a pair whose records carry no mode this build knows:
// the key is the by-mode breakdown's ModeNone, so one row holds every such
// pair, while the score is read at "", the strictest reading (ab's rule for a
// mode outside the four).
type abGroup struct {
	mode                 rigor.Mode
	modeKey, tier        string
	champion, challenger string
}

// groupOf reads one pair's group off its records. The champion is read for
// every fact but the challenger's one model: R2 made the pair, so the two arms
// agree on the mode, the tier and the recipe, and the champion's record is the
// one that carries them as the pair resolved them.
func groupOf(p ab.Pair) abGroup {
	return abGroup{
		mode:       scoredMode(p.Champion),
		modeKey:    modeKey(string(recordedMode(p.Champion))),
		tier:       tierKey(p.Champion),
		champion:   armModel(p.Champion),
		challenger: armModel(p.Challenger),
	}
}

// scoredMode is the mode the pair's signals are read at: the mode the record
// resolved, and "" for one that resolved none or one this build does not know,
// which ab reads through its whole signal list.
func scoredMode(rec runstore.Record) rigor.Mode {
	if rec.Rigor == nil {
		return ""
	}
	mode, ok := rigor.Parse(string(rec.Rigor.Mode))
	if !ok {
		return ""
	}
	return mode
}

// recordedMode is the mode the record carries, as it is written: the group key
// reads it the way the by-mode breakdown does, so a record with no mode and one
// whose mode this build does not know are one row.
func recordedMode(rec runstore.Record) string {
	if rec.Rigor == nil {
		return ""
	}
	return string(rec.Rigor.Mode)
}

// tierKey is the row's tier: the tier the record carried, ABTierNone for a run
// that carried none and for one carrying a name this build does not know — a
// tier no expectation can be read at is not a group of its own.
func tierKey(rec runstore.Record) string {
	if rec.Tier == nil || !tier.Known(rec.Tier.Tier) {
		return ABTierNone
	}
	return string(rec.Tier.Tier)
}

// armModel is the coder model one arm ran: the model its implement stage was
// pinned to. An arm that reached no implement stage — its job died before the
// first one — falls back to the model its own task named, which only a
// challenger's task carries (AB.Challenger); a champion's task names none, so a
// champion that never implemented groups under the empty model rather than
// under a guess.
func armModel(rec runstore.Record) string {
	for _, s := range rec.Stages {
		if s.Name == implementStage && s.Model != "" {
			return s.Model
		}
	}
	if rec.AB != nil {
		return rec.AB.Challenger
	}
	return ""
}

// compare orders two groups: by mode, tier, champion model and challenger
// model, the first two in their own vocabularies' order so a reader finds the
// rows in the same places every time and the last two alphabetically, the only
// order model IDs have.
func (g abGroup) compare(o abGroup) int {
	if c := cmp.Compare(modeRank(g.modeKey), modeRank(o.modeKey)); c != 0 {
		return c
	}
	if c := cmp.Compare(tierRank(g.tier), tierRank(o.tier)); c != 0 {
		return c
	}
	if c := cmp.Compare(g.champion, o.champion); c != 0 {
		return c
	}
	return cmp.Compare(g.challenger, o.challenger)
}

// modeRank is a group key's place in the mode vocabulary, and the place after
// it for ModeNone: a pair nobody could read a mode for is a row of its own, at
// the end, rather than one mixed into a mode it did not resolve.
func modeRank(key string) int {
	if i := slices.IndexFunc(rigor.Modes, func(m rigor.Mode) bool { return string(m) == key }); i >= 0 {
		return i
	}
	return len(rigor.Modes)
}

// tierRank is a group key's place in the tier vocabulary, and the place after
// it for the untiered group, for the reason modeRank gives.
func tierRank(key string) int {
	if i := slices.IndexFunc(tier.Tiers, func(t tier.Tier) bool { return string(t) == key }); i >= 0 {
		return i
	}
	return len(tier.Tiers)
}

// abAcc accumulates one group's row.
type abAcc struct {
	group     abGroup
	pairs     int
	followUps int
	summary   ab.Summary
}

func (a *abAcc) add(p ab.Pair) {
	res := ab.Score(p, a.group.mode)
	switch res.Winner {
	case ab.WinChallenger:
		a.summary.Wins++
	case ab.WinChampion:
		a.summary.Losses++
	case ab.WinTie:
		a.summary.Ties++
	}
	if res.Champion.Halted {
		a.summary.ChampionHalts++
	}
	if res.Challenger.Halted {
		a.summary.ChallengerHalts++
	}
	if ratio := pairCostRatio(p); !math.IsNaN(ratio) {
		a.summary.CostRatios = append(a.summary.CostRatios, ratio)
	}
	a.pairs++
	a.followUps += p.ChampionFollowUps
}

func (a *abAcc) row(rate float64) ABRow {
	return ABRow{
		Mode:            a.group.modeKey,
		Tier:            a.group.tier,
		Champion:        a.group.champion,
		Challenger:      a.group.challenger,
		Pairs:           a.pairs,
		Wins:            a.summary.Wins,
		Losses:          a.summary.Losses,
		Ties:            a.summary.Ties,
		ChampionHalts:   a.summary.ChampionHalts,
		ChallengerHalts: a.summary.ChallengerHalts,
		FollowUps:       a.followUps,
		Rec:             ab.Recommend(a.summary, rate),
	}
}

// pairCostRatio is one pair's price as a ratio, the challenger's model dollars
// over the champion's (D563), which is what ab's median is taken over.
//
// It is NaN — no figure, which is not a ratio of 0 — for a pair either arm
// carries no cost block for, and for one whose champion cost nothing: the first
// is no price at all and the second no denominator. Model dollars are read
// whole, the notional part included: both arms are the same task on the same
// credentials, so the two figures are read the same way.
func pairCostRatio(p ab.Pair) float64 {
	if p.Champion.Cost == nil || p.Challenger.Cost == nil {
		return math.NaN()
	}
	if p.Champion.Cost.ModelUSD <= 0 {
		return math.NaN()
	}
	return p.Challenger.Cost.ModelUSD / p.Champion.Cost.ModelUSD
}

// abRate is the extra cost a mode's groups are read at (D565): the caller's
// figure, and Fugaro's own for a mode it leaves out. Nil is the shipped table,
// which is what a machine that has read no project layer reports.
func abRate(rates map[rigor.Mode]float64, m rigor.Mode) float64 {
	if rate, ok := rates[m]; ok {
		return rate
	}
	return config.ABExchangeRateDefault(m)
}

// ABMeta is the window and the provenance an ABReport is shown with. It is a
// struct of its own rather than the spend report's Meta: this read has no
// degraded path, no day breakdown and no money column, and a reader of one
// shared struct would have to work out for himself which of its fields mean
// nothing here.
type ABMeta struct {
	Project string
	// Source says where the rows came from: "run-records" for every read of the
	// runs bucket.
	Source string
	// Since and Until are the window's ends as UTC instants (RFC3339), Since
	// empty for the one --since 0 leaves open.
	Since, Until string
	Warnings     []string
}

// ABTable is the human table of `fugaro ab`: text only, every cell sanitized.
func ABTable(w io.Writer, rep ABReport, m ABMeta) error {
	fmt.Fprintln(w, abWindowLine(m))
	for _, warning := range m.Warnings {
		fmt.Fprintln(w, "warning: "+Text(warning))
	}
	if len(rep.Rows) == 0 {
		fmt.Fprintln(w, "no valid A/B pair in the window this read covers: no two records of it carry one pair ID as a champion and a challenger")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join([]string{
		"MODE", "TIER", "CHAMPION", "CHALLENGER", "PAIRS", "WINS", "LOSSES", "TIES",
		"CHALLENGER WIN 95%", "COST", "HALTS", "FOLLOW-UPS", "ADVICE",
	}, "\t"))
	for _, row := range rep.Rows {
		fmt.Fprintln(tw, strings.Join(abCells(row), "\t"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, note := range abNotes(rep) {
		fmt.Fprintln(w, note)
	}
	return nil
}

// abWindowLine is the table's own line about the span it covers: a bound at
// both ends, or the one end --since 0 leaves open.
func abWindowLine(m ABMeta) string {
	const tail = ", by mode, tier and model pair (UTC)"
	switch {
	case m.Since == "" && m.Until == "":
		return "A/B pairs" + tail
	case m.Since == "":
		return fmt.Sprintf("A/B pairs up to %s%s", Text(m.Until), tail)
	default:
		return fmt.Sprintf("A/B pairs from %s to %s%s", Text(m.Since), Text(m.Until), tail)
	}
}

func abCells(r ABRow) []string {
	cost, halts := "-", strconv.Itoa(r.ChampionHalts)+"/"+strconv.Itoa(r.ChallengerHalts)
	if r.Rec.HasCost {
		cost = fmt.Sprintf("%.2fx", r.Rec.CostRatio)
	}
	return []string{
		Text(r.Mode), Text(r.Tier), Text(r.Champion), Text(r.Challenger),
		strconv.Itoa(r.Pairs), strconv.Itoa(r.Wins), strconv.Itoa(r.Losses), strconv.Itoa(r.Ties),
		fmt.Sprintf("%.2f-%.2f", r.Rec.Lo, r.Rec.Hi),
		cost, halts, strconv.Itoa(r.FollowUps), abAdvice(r.Rec),
	}
}

// abAdvice is the ADVICE cell: the recommendation in the words a reader
// decides with, then the rule that fired, so the table explains itself.
func abAdvice(rec ab.Recommendation) string {
	switch rec.Decision {
	case ab.DecisionChallenger:
		return "the challenger is better (" + string(rec.Rule) + ")"
	case ab.DecisionKeep:
		return "keep the champion (" + string(rec.Rule) + ")"
	default:
		return "no conclusion: too few decisive pairs"
	}
}

// abNotes are the lines under the table: what the columns mean, and the two
// promises a reader is owed. Each is printed only where it applies, so a table
// nobody's sample is thin under says nothing about thin samples.
func abNotes(rep ABReport) []string {
	notes := []string{
		"WINS and LOSSES are the challenger's -- a pair it took and a pair it lost; a TIE decided nothing and no figure here is drawn from one",
		"COST is the median pair cost ratio, the challenger's model dollars over the champion's; - means no pair carried both arms' figures",
		"no person, no repository and no task text is read: a row is a mode, a tier and two coder models (D560)",
	}
	if slices.ContainsFunc(rep.Rows, func(r ABRow) bool { return r.Rec.Decision == ab.DecisionNoConclusion }) {
		notes = append(notes, fmt.Sprintf("a row with fewer than %d decisive pairs carries no conclusion: the sample cannot say, which is not the same answer as keeping the champion", ab.MinRuns))
	}
	if slices.ContainsFunc(rep.Rows, func(r ABRow) bool { return r.FollowUps > 0 }) {
		notes = append(notes, "FOLLOW-UPS counts the runs continuing the champion's pull request: context only, since the shadow opens no pull request (D562)")
	}
	return notes
}

// ABJSONDoc is the --json document of `fugaro ab`. Every figure is a number or
// a string a reader can act on, and an absent figure is null rather than 0.
type ABJSONDoc struct {
	Project  string      `json:"project"`
	Source   string      `json:"source"`
	Since    string      `json:"since"`
	Until    string      `json:"until"`
	Rows     []ABJSONRow `json:"rows"`
	Warnings []string    `json:"warnings"`
}

// ABJSONRow is one group: the group, the counts, and the recommendation object
// R3 reserves as the seam a writer would read.
type ABJSONRow struct {
	Mode              string               `json:"mode"`
	Tier              string               `json:"tier"`
	Champion          string               `json:"champion"`
	Challenger        string               `json:"challenger"`
	Pairs             int                  `json:"pairs"`
	Wins              int                  `json:"wins"`
	Losses            int                  `json:"losses"`
	Ties              int                  `json:"ties"`
	ChampionHalts     int                  `json:"champion_halts"`
	ChallengerHalts   int                  `json:"challenger_halts"`
	ChampionFollowUps int                  `json:"champion_follow_ups"`
	Recommendation    ABJSONRecommendation `json:"recommendation"`
}

// ABJSONRecommendation is the rule's answer as the JSON carries it: the advice,
// the region behind it and every figure it was read through, so a reader can
// check the arithmetic without the records. The group it is about is the row it
// sits in.
type ABJSONRecommendation struct {
	// Decision is "challenger", "keep" or "no-conclusion", and Rule the region
	// that fired ("cheaper-not-worse", "better-within-rate", "keep" or
	// "below-minimum").
	Decision string `json:"decision"`
	Rule     string `json:"rule"`
	// Decisive is the wins plus losses every figure here was drawn from.
	Decisive int `json:"decisive"`
	// P is the exact sign test's p-value; WinShareLo and WinShareHi are the
	// Clopper-Pearson 95% interval for the challenger's share of the decisive
	// pairs (D563). The two are reported even where the decision is
	// "no-conclusion", where the interval is the reason for it.
	P          float64 `json:"p"`
	WinShareLo float64 `json:"win_share_lo"`
	WinShareHi float64 `json:"win_share_hi"`
	// MedianCostRatio is null when no pair of the group carried both arms'
	// costs: no price is not a price of 0, and the rule reads the two
	// differently.
	MedianCostRatio *float64 `json:"median_cost_ratio"`
	// ExchangeRate is the extra cost per pair the group was read at (D565).
	ExchangeRate float64 `json:"exchange_rate"`
}

// ABJSON writes the document `fugaro ab --json` prints.
func ABJSON(w io.Writer, rep ABReport, m ABMeta) error {
	doc := ABJSONDoc{
		Project:  Text(m.Project),
		Source:   m.Source,
		Since:    m.Since,
		Until:    m.Until,
		Rows:     []ABJSONRow{},
		Warnings: []string{},
	}
	for _, r := range rep.Rows {
		rec := ABJSONRecommendation{
			Decision:     string(r.Rec.Decision),
			Rule:         string(r.Rec.Rule),
			Decisive:     r.Rec.Decisive,
			P:            r.Rec.P,
			WinShareLo:   r.Rec.Lo,
			WinShareHi:   r.Rec.Hi,
			ExchangeRate: r.Rec.Rate,
		}
		if r.Rec.HasCost {
			ratio := r.Rec.CostRatio
			rec.MedianCostRatio = &ratio
		}
		doc.Rows = append(doc.Rows, ABJSONRow{
			Mode:              r.Mode,
			Tier:              r.Tier,
			Champion:          r.Champion,
			Challenger:        r.Challenger,
			Pairs:             r.Pairs,
			Wins:              r.Wins,
			Losses:            r.Losses,
			Ties:              r.Ties,
			ChampionHalts:     r.ChampionHalts,
			ChallengerHalts:   r.ChallengerHalts,
			ChampionFollowUps: r.FollowUps,
			Recommendation:    rec,
		})
	}
	for _, warning := range m.Warnings {
		doc.Warnings = append(doc.Warnings, Text(warning))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
