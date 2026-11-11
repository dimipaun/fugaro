// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package report

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/ab"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The facts every pair in these tests shares: one start commit, one resolved
// recipe and a passing test at the final head, so the pair is valid (R2) and a
// test that moves one of them is a test about that one.
const (
	abStartSHA  = "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
	abOtherSHA  = "3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c"
	abHeadSHA   = "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e"
	abRecipe    = "default"
	abRecipeSHA = "abababababababababababababababababababababababababababababababab"
)

// The coder models the fixtures name. Invented IDs: nothing here is a price
// table's model, and a group is two of these and nothing else. Delta and the
// names after it belong to pairs R2 refuses, so their absence from a report is
// visible.
const (
	abModelAlpha   = "model-alpha"
	abModelBeta    = "model-beta"
	abModelGamma   = "model-gamma"
	abModelDelta   = "model-delta"
	abModelEpsilon = "model-epsilon"
	abModelZeta    = "model-zeta"
	abModelEta     = "model-eta"
)

// abPair is one pair's two records.
type abPair struct {
	champion, challenger runstore.Record
}

// records flattens pairs into the record list a read hands over.
func records(pairs ...abPair) []runstore.Record {
	out := make([]runstore.Record, 0, 2*len(pairs))
	for _, p := range pairs {
		out = append(out, p.champion, p.challenger)
	}
	return out
}

// arm is what a test moves about one arm's record.
type arm struct {
	model string
	// cost is the arm's recorded model dollars, and priced says whether the
	// record carries a cost block at all: an arm with no block holds no figure,
	// which is not a figure of 0.
	cost   float64
	priced bool
	halted bool
	// failing is a head whose test failed, so the arm is not verified.
	failing bool
	// findings is the deciding senior round's count, the round that shipped.
	findings int
	// start overrides the pair's start commit, which is what makes a pair
	// invalid (R2).
	start string
}

// pair is one pair's two records, at one mode and tier, differing only in the
// arm each spec describes.
func pair(pairID string, mode rigor.Mode, tr tier.Tier, champion, challenger arm) abPair {
	return abPair{
		champion:   armRecord(pairID, pairID+"-champion", task.ABArmChampion, mode, tr, champion),
		challenger: armRecord(pairID, pairID+"-challenger", task.ABArmChallenger, mode, tr, challenger),
	}
}

// armRecord is one arm's record as a run writes it. Only the champion carries a
// branch and a pull request: the shadow pushes nothing and opens nothing
// (D554), and the report must be readable with those fields set.
func armRecord(pairID, runID string, armTask task.ABArm, mode rigor.Mode, tr tier.Tier, a arm) runstore.Record {
	start := a.start
	if start == "" {
		start = abStartSHA
	}
	rec := runstore.Record{
		RunID:         runID,
		Repo:          "acme/marker-repo",
		Workflow:      "marker-workflow",
		Reason:        "marker-reason",
		StartSHA:      start,
		HeadSHA:       abHeadSHA,
		ExpectedScope: []string{"internal/marker/secret.go"},
		AB:            &task.AB{PairID: pairID, Arm: armTask},
		Recipe:        &runstore.RecipeRecord{Name: abRecipe, Source: "catalog", SHA256: abRecipeSHA},
		Verify:        []verify.Record{{Kind: verify.KindTest, HeadSHA: abHeadSHA, CleanTree: true, Passed: !a.failing}},
		Reviews:       []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierSenior, Verdict: "ship", Findings: a.findings}},
		Stages:        []runstore.StageTiming{{Name: ab.ImplementStage, Model: a.model}},
	}
	if armTask == task.ABArmChampion {
		rec.Branch = "fugaro/marker-branch"
		rec.PR = &runstore.PRRef{Number: 1, URL: "https://example.invalid/acme/marker-repo/pull/1"}
	} else {
		rec.AB.Challenger = a.model
		rec.AB.ChallengerSource = task.ABChallengerOwner
	}
	if mode != "" {
		rec.Rigor = &runstore.RigorRecord{Mode: mode}
	}
	if tr != "" {
		rec.Tier = &runstore.TierRecord{Tier: tr}
	}
	if a.priced {
		rec.Cost = &runstore.Cost{ModelUSD: a.cost, TotalUSD: a.cost, Estimate: true}
	}
	if a.halted {
		rec.Halt = &runstore.Halt{Reason: runstore.HaltRunCap}
	}
	return rec
}

// oneRow is the single row a test expects, the failure naming what came
// instead.
func oneRow(t *testing.T, rep ABReport) ABRow {
	t.Helper()
	if len(rep.Rows) != 1 {
		t.Fatalf("the report holds %d rows, want 1: %+v", len(rep.Rows), rep.Rows)
	}
	return rep.Rows[0]
}

// TestABRows is the report's plain job: one row per mode × tier × model pair
// (D560, D564), the challenger's wins, losses and ties, each arm's halts, the
// champion's follow-ups, the median pair cost ratio, and rows in an order the
// input cannot change (D563's "same records, same report").
func TestABRows(t *testing.T) {
	// Three groups: in Pro/Small the challenger halts once and loses once to an
	// arm whose test failed, and draws once; a second model pair in the same
	// mode and tier wins on the champion's findings; and a POC pair carries no
	// tier at all.
	haltLoss := pair("p1", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta, cost: 2, priced: true, halted: true})
	testLoss := pair("p2", rigor.Pro, tier.Small,
		arm{model: abModelAlpha},
		arm{model: abModelBeta, failing: true})
	draw := pair("p3", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta})
	findingsWin := pair("p4", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true, findings: 2},
		arm{model: abModelGamma, cost: 1.5, priced: true})
	untiered := pair("p5", rigor.POC, "",
		arm{model: abModelAlpha},
		arm{model: abModelBeta, halted: true})

	rep := BuildAB(records(haltLoss, testLoss, draw, findingsWin, untiered), nil)

	if rep.Pairs != 5 {
		t.Errorf("the report counts %d pairs, want 5: every pair here is valid", rep.Pairs)
	}
	if len(rep.Rows) != 3 {
		t.Fatalf("the report holds %d rows, want 3 (POC/untiered, Pro/Small alpha-beta, Pro/Small alpha-gamma): %+v", len(rep.Rows), rep.Rows)
	}
	type want struct {
		mode, tier, champion, challenger                          string
		pairs, wins, losses, ties, championHalts, challengerHalts int
	}
	wants := []want{
		{"poc", ABTierNone, abModelAlpha, abModelBeta, 1, 0, 1, 0, 0, 1},
		{"pro", "small", abModelAlpha, abModelBeta, 3, 0, 2, 1, 0, 1},
		{"pro", "small", abModelAlpha, abModelGamma, 1, 1, 0, 0, 0, 0},
	}
	for i, w := range wants {
		got := rep.Rows[i]
		if got.Mode != w.mode || got.Tier != w.tier || got.Champion != w.champion || got.Challenger != w.challenger {
			t.Errorf("row %d is %s/%s %s vs %s, want %s/%s %s vs %s: rows are ordered by mode, tier and then the two models", i, got.Mode, got.Tier, got.Champion, got.Challenger, w.mode, w.tier, w.champion, w.challenger)
		}
		if got.Pairs != w.pairs || got.Wins != w.wins || got.Losses != w.losses || got.Ties != w.ties {
			t.Errorf("row %d counts %d pairs (%d wins, %d losses, %d ties), want %d (%d, %d, %d)", i, got.Pairs, got.Wins, got.Losses, got.Ties, w.pairs, w.wins, w.losses, w.ties)
		}
		if got.ChampionHalts != w.championHalts || got.ChallengerHalts != w.challengerHalts {
			t.Errorf("row %d halts champion %d, challenger %d, want %d and %d", i, got.ChampionHalts, got.ChallengerHalts, w.championHalts, w.challengerHalts)
		}
	}
	// The ratio is the challenger's dollars over the champion's, per pair that
	// carries both figures: the halt-loss pair is 2/1, the findings win is
	// 1.5/1, and the pairs whose second arm carries no cost block are in no
	// ratio at all.
	if r := rep.Rows[1].Rec; !r.HasCost || r.CostRatio != 2 {
		t.Errorf("Pro/Small alpha-beta cost ratio is %v (has %v), want 2: the challenger cost twice the champion on the one pair that carried both figures", r.CostRatio, r.HasCost)
	}
	if r := rep.Rows[2].Rec; !r.HasCost || r.CostRatio != 1.5 {
		t.Errorf("Pro/Small alpha-gamma cost ratio is %v (has %v), want 1.5", r.CostRatio, r.HasCost)
	}
	if r := rep.Rows[0].Rec; r.HasCost {
		t.Errorf("the POC row carries a cost ratio of %v, want none: neither arm of its pair held a cost block", r.CostRatio)
	}
	if r := rep.Rows[1].Rec; r.Decisive != 2 {
		t.Errorf("Pro/Small alpha-beta counts %d decisive pairs, want 2: the tie decided nothing (D563)", r.Decisive)
	}

	// The same records in another order are the same report: a bucket listing
	// hands its records over in whatever order it likes.
	shuffled := records(untiered, findingsWin, draw, testLoss, haltLoss)
	if other := BuildAB(shuffled, nil); !reflect.DeepEqual(rep, other) {
		t.Errorf("the same records in another order gave a different report:\n%+v\n%+v", rep, other)
	}
}

// TestABReportHasNoPerson is D560's second half, the one the marketing site
// promises: a row is a mode, a tier and two coder models. The requester is not
// on a record at all (it is the task's requested_by, and this read opens
// result.json and never task.json), so the repository, the branch, the pull
// request, the run IDs and the paths the run touched are the identity a record
// does carry — and none of it may reach the table or the JSON.
func TestABReportHasNoPerson(t *testing.T) {
	p := pair("p1", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta, cost: 0.5, priced: true})
	rep := BuildAB(records(p), nil)
	row := oneRow(t, rep)

	markers := []string{"acme/marker-repo", "marker-workflow", "marker-reason", "marker-branch", "example.invalid", "internal/marker/secret.go", "p1-champion", "p1-challenger"}
	var table, doc bytes.Buffer
	meta := ABMeta{Project: "proj-1234", Source: "run-records", Since: "2026-10-01T00:00:00Z", Until: "2026-11-01T00:00:00Z"}
	if err := ABTable(&table, rep, meta); err != nil {
		t.Fatalf("ABTable: %v", err)
	}
	if err := ABJSON(&doc, rep, meta); err != nil {
		t.Fatalf("ABJSON: %v", err)
	}
	for _, out := range []string{table.String(), doc.String()} {
		for _, marker := range markers {
			if strings.Contains(out, marker) {
				t.Errorf("the report prints %q, which is a record's identity and never a group's (D560):\n%s", marker, out)
			}
		}
	}

	// And the row has no field for one: every string a row carries is a word of
	// the vocabulary the report promises. A new string field has to be added to
	// this list deliberately, which is the point.
	vocabulary := []string{
		abModelAlpha, abModelBeta, abModelGamma, "poc", "pro", "small", ABTierNone, ModeNone,
		string(ab.DecisionChallenger), string(ab.DecisionKeep), string(ab.DecisionNoConclusion),
		string(ab.RuleCheaperNotWorse), string(ab.RuleBetterWithinRate), string(ab.RuleKeep), string(ab.RuleBelowMinimum),
	}
	for _, s := range abRowStrings(row) {
		if !slices.Contains(vocabulary, s) {
			t.Errorf("a row carries the string %q, which is not a mode, a tier, a model or a word of the rule's own vocabulary (D560): %+v", s, row)
		}
	}
}

// TestABReportSuppressesSmallGroups is D563's line under the rule: fewer than
// ab.MinRuns decisive pairs is a sample that cannot say, which is a different
// answer from "keep the champion" — and the figures are printed anyway, so a
// reader sees how far the group is from the threshold.
func TestABReportSuppressesSmallGroups(t *testing.T) {
	// A group whose every pair the challenger took by the champion halting,
	// priced at half: the rule's best case, held back by the count alone.
	group := func(n int) ABReport {
		pairs := make([]abPair, 0, n)
		for i := 0; i < n; i++ {
			pairs = append(pairs, pair(pairName(i), rigor.Pro, tier.Small,
				arm{model: abModelAlpha, cost: 1, priced: true, halted: true},
				arm{model: abModelBeta, cost: 0.5, priced: true}))
		}
		return BuildAB(records(pairs...), nil)
	}

	short := oneRow(t, group(ab.MinRuns-1))
	if short.Pairs != ab.MinRuns-1 || short.Rec.Decisive != ab.MinRuns-1 {
		t.Fatalf("the short group holds %d pairs and %d decisive, want %d of each", short.Pairs, short.Rec.Decisive, ab.MinRuns-1)
	}
	if short.Rec.Decision != ab.DecisionNoConclusion || short.Rec.Rule != ab.RuleBelowMinimum {
		t.Errorf("a group of %d decisive pairs reads %q/%q, want %q/%q: below the minimum the sample says nothing, which is not the answer \"keep the champion\"", short.Rec.Decisive, short.Rec.Decision, short.Rec.Rule, ab.DecisionNoConclusion, ab.RuleBelowMinimum)
	}
	if !short.Rec.HasCost || short.Rec.CostRatio != 0.5 {
		t.Errorf("the short group's cost ratio is %v (has %v), want 0.5: the figures are printed even where no conclusion is drawn", short.Rec.CostRatio, short.Rec.HasCost)
	}

	full := oneRow(t, group(ab.MinRuns))
	if full.Rec.Decisive != ab.MinRuns {
		t.Fatalf("the full group counts %d decisive pairs, want %d", full.Rec.Decisive, ab.MinRuns)
	}
	if full.Rec.Decision != ab.DecisionChallenger {
		t.Errorf("a group of %d pairs the challenger took reads %q, want %q: the sample is big enough and every signal points one way", full.Rec.Decisive, full.Rec.Decision, ab.DecisionChallenger)
	}
	if full.Rec.Lo <= 0.5 {
		t.Errorf("the challenger's win share lower bound is %v, want above 0.5: no loss in %d pairs is what \"clearly better\" means", full.Rec.Lo, ab.MinRuns)
	}
}

// pairName is one pair's ID: unique per index, and short enough to read in a
// failure.
func pairName(i int) string { return "p" + string(rune('a'+i)) }

// TestABReportJSONRecommendation is R3's seam: the JSON carries the
// recommendation as an object with the figures the rule was read through, so a
// later writer reads a decision rather than re-deriving one from prose. A group
// no pair carried a price for reports a null ratio, not a ratio of 0.
func TestABReportJSONRecommendation(t *testing.T) {
	won := make([]abPair, 0, ab.MinRuns)
	for i := 0; i < ab.MinRuns; i++ {
		won = append(won, pair(pairName(i), rigor.Pro, tier.Small,
			arm{model: abModelAlpha, cost: 1, priced: true, findings: 2},
			arm{model: abModelBeta, cost: 0.5, priced: true}))
	}
	unpriced := pair("unpriced", rigor.POC, tier.Small,
		arm{model: abModelAlpha, findings: 2},
		arm{model: abModelBeta})
	all := records(append(won, unpriced)...)
	rep := BuildAB(all, nil)
	if len(rep.Rows) != 2 {
		t.Fatalf("the report holds %d rows, want 2: %+v", len(rep.Rows), rep.Rows)
	}

	var out bytes.Buffer
	meta := ABMeta{Project: "proj-1234", Source: "run-records", Since: "2026-10-01T00:00:00Z", Until: "2026-11-01T00:00:00Z"}
	if err := ABJSON(&out, rep, meta); err != nil {
		t.Fatalf("ABJSON: %v", err)
	}
	doc := decodeABJSON(t, out.Bytes())
	if doc.Project != "proj-1234" || doc.Source != "run-records" || doc.Since != meta.Since || doc.Until != meta.Until {
		t.Errorf("the document's head is %+v, want the project, the source and both ends of the window", doc)
	}
	if len(doc.Rows) != 2 {
		t.Fatalf("the document holds %d rows, want 2:\n%s", len(doc.Rows), out.String())
	}
	win, none := doc.row(t, "pro"), doc.row(t, "poc")
	if win.Tier != "small" || win.Pairs != ab.MinRuns || win.Wins != ab.MinRuns || win.Losses != 0 {
		t.Errorf("the won group reads %+v, want %d wins of %d pairs at Pro/Small", win, ab.MinRuns, ab.MinRuns)
	}
	if win.Rec.Decision != string(ab.DecisionChallenger) {
		t.Errorf("the won group's recommendation is %q, want %q", win.Rec.Decision, ab.DecisionChallenger)
	}
	if win.Rec.Rule != string(ab.RuleBetterWithinRate) {
		t.Errorf("the won group's rule is %q, want %q: it costs half and Pro's rate is 0.5", win.Rec.Rule, ab.RuleBetterWithinRate)
	}
	if win.Rec.Decisive != ab.MinRuns || win.Rec.P >= 0.05 || win.Rec.Lo <= 0.5 || win.Rec.Hi != 1 {
		t.Errorf("the won group's figures are decisive=%d p=%v lo=%v hi=%v, want %d decisive, a p under 0.05, a lower bound above 0.5 and an upper bound of 1", win.Rec.Decisive, win.Rec.P, win.Rec.Lo, win.Rec.Hi, ab.MinRuns)
	}
	if win.Rec.Rate != 0.5 {
		t.Errorf("the Pro group's exchange rate reads %v, want 0.5: no caller figure was passed, so the shipped table applies", win.Rec.Rate)
	}
	if win.Rec.CostRatio == nil || math.Abs(*win.Rec.CostRatio-0.5) > 1e-9 {
		t.Errorf("the won group's median cost ratio is %v, want 0.5", win.Rec.CostRatio)
	}

	if none.Rec.CostRatio != nil {
		t.Errorf("the unpriced group's median cost ratio is %v, want null: no pair carries both arms' figures, and no price is not a price of 0", *none.Rec.CostRatio)
	}
	if none.Rec.Decision != string(ab.DecisionNoConclusion) {
		t.Errorf("the unpriced group of one pair reads %q, want %q", none.Rec.Decision, ab.DecisionNoConclusion)
	}

	// The rate a caller passes is the rate the document reports: a project that
	// raised Pro's allowance is what the advice was read at.
	raised := BuildAB(all, map[rigor.Mode]float64{rigor.Pro: 3})
	var raisedOut bytes.Buffer
	if err := ABJSON(&raisedOut, raised, meta); err != nil {
		t.Fatalf("ABJSON: %v", err)
	}
	if rate := decodeABJSON(t, raisedOut.Bytes()).row(t, "pro").Rec.Rate; rate != 3 {
		t.Errorf("the Pro group's exchange rate reads %v, want the 3 the caller passed", rate)
	}
}

// abJSONDoc is the document `fugaro ab --json` prints, decoded for a test. The
// field names are the wire contract, so a rename here is a rename there.
type abJSONDoc struct {
	Project  string      `json:"project"`
	Source   string      `json:"source"`
	Since    string      `json:"since"`
	Until    string      `json:"until"`
	Rows     []abJSONRow `json:"rows"`
	Warnings []string    `json:"warnings"`
}

type abJSONRow struct {
	Mode   string    `json:"mode"`
	Tier   string    `json:"tier"`
	Pairs  int       `json:"pairs"`
	Wins   int       `json:"wins"`
	Losses int       `json:"losses"`
	Rec    abJSONRec `json:"recommendation"`
}

// row is the row of one mode, so a test names the group it is about rather than
// a position in the table.
func (d abJSONDoc) row(t *testing.T, mode string) abJSONRow {
	t.Helper()
	for _, r := range d.Rows {
		if r.Mode == mode {
			return r
		}
	}
	t.Fatalf("the document holds no %s row: %+v", mode, d.Rows)
	return abJSONRow{}
}

type abJSONRec struct {
	Decision  string   `json:"decision"`
	Rule      string   `json:"rule"`
	Decisive  int      `json:"decisive"`
	P         float64  `json:"p"`
	Lo        float64  `json:"win_share_lo"`
	Hi        float64  `json:"win_share_hi"`
	CostRatio *float64 `json:"median_cost_ratio"`
	Rate      float64  `json:"exchange_rate"`
}

func decodeABJSON(t *testing.T, data []byte) abJSONDoc {
	t.Helper()
	var doc abJSONDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the JSON does not decode: %v\n%s", err, data)
	}
	return doc
}

// TestABReportExcludesInvalidPairs is D571: a pair the arms do not make — a
// different start commit, or an arm that never started — is in no figure at all,
// so no row counts it and no model named only by such a pair appears.
func TestABReportExcludesInvalidPairs(t *testing.T) {
	valid1 := pair("p1", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta, cost: 0.5, priced: true, halted: true})
	valid2 := pair("p2", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, findings: 2},
		arm{model: abModelBeta})
	skewed := pair("p3", rigor.Pro, tier.Small,
		arm{model: abModelDelta},
		arm{model: abModelEpsilon, start: abOtherSHA})
	neverStarted := pair("p4", rigor.Pro, tier.Small,
		arm{model: abModelZeta},
		arm{model: abModelEta})
	neverStarted.champion.StartSHA = ""
	neverStarted.champion.HeadSHA = ""
	// A follow-up run carries no ab block at all, so it is no arm of anything.
	followUp := runstore.Record{RunID: "follow-up", Repo: "acme/marker-repo", Rigor: &runstore.RigorRecord{Mode: rigor.Pro}}

	all := append(records(valid1, valid2, skewed, neverStarted), followUp)
	rep := BuildAB(all, nil)

	if rep.Pairs != 2 || len(rep.Rows) != 1 {
		t.Fatalf("the report counts %d pairs in %d rows, want 2 in 1: %+v", rep.Pairs, len(rep.Rows), rep.Rows)
	}
	row := rep.Rows[0]
	if row.Pairs != 2 {
		t.Errorf("the one row counts %d pairs, want 2: only two of the records' pairs are valid", row.Pairs)
	}
	var table bytes.Buffer
	if err := ABTable(&table, rep, ABMeta{Project: "proj-1234", Source: "run-records"}); err != nil {
		t.Fatalf("ABTable: %v", err)
	}
	for _, model := range []string{abModelDelta, abModelEpsilon, abModelZeta, abModelEta} {
		if strings.Contains(table.String(), model) {
			t.Errorf("the table names %q, a model only an invalid pair ran: a refused pair is in no figure at all (D571)\n%s", model, table.String())
		}
	}
}

// abRowStrings is every string a row carries, the recommendation's own words
// included, so a test can hold them all to the vocabulary the report promises.
func abRowStrings(r ABRow) []string {
	var out []string
	collectStrings(reflect.ValueOf(r), &out)
	return out
}

func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collectStrings(v.Field(i), out)
		}
	}
}
