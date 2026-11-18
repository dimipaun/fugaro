// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package report

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/ab"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The reviewer the fixtures hold fixed: the model the champion's own level ran
// on, and the one the shadow replayed it with, both invented IDs and neither a
// price table's. The coder both arms of a reviewer pair ran is abModelGamma.
const (
	abReviewerOne = "reviewer-one"
	abReviewerTwo = "reviewer-two"
	abReviewerAlt = "reviewer-alt"
	abCoderShared = abModelGamma
)

// reviewerSpec is what one arm of a reviewer pair brings (B1.3, B1.2): the round
// it ran at the level, and what the level's stage cost it.
type reviewerSpec struct {
	// model and effort are the reviewer that round ran on.
	model, effort string
	// findings is the round's count and verdict the word it answered, "" for a
	// round that answered nothing.
	findings int
	verdict  string
	// reviewUSD is what the level's stage cost this arm, coderUSD what its own
	// implement stage cost, and runUSD the arm's whole recorded model dollars —
	// which is the coder's work and not the level's, so the report must read
	// the stage and not this (B1.3).
	reviewUSD, coderUSD, runUSD float64
}

// reviewerPairAt is one reviewer pair's two records, at one mode and tier: the
// champion's own run, and the shadow that replayed the same level over the
// champion's patch (B1.2).
func reviewerPairAt(pairID, slot string, mode rigor.Mode, tr tier.Tier, champion, challenger reviewerSpec) abPair {
	championRun := pairID + "-champion"
	return abPair{
		champion:   reviewerRecord(pairID, championRun, task.ABArmChampion, slot, mode, tr, champion),
		challenger: reviewerRecord(pairID, pairID+"-challenger", task.ABArmChallenger, slot, mode, tr, challenger, championRun),
	}
}

// reviewerRecord is one arm of a reviewer pair as a run writes it: the arm's own
// round at the level is what the pair is scored on, and the coder facts the
// coder reading would decide on are deliberately shared and dull — both arms
// read one change, so neither arm's coding says anything about the reviewers
// (B1.2). The challenger carries the champion's run ID, which is the patch it
// read, and a coder pair's challenger carries none.
func reviewerRecord(pairID, runID string, armTask task.ABArm, slot string, mode rigor.Mode, tr tier.Tier, s reviewerSpec, championRun ...string) runstore.Record {
	a := arm{model: abCoderShared, cost: s.runUSD, priced: true}
	rec := armRecord(pairID, runID, armTask, mode, tr, a)
	rec.AB.Slot = task.ABSlot(slot)
	rec.Reviews = []runstore.ReviewSummary{{
		Round: 1, Tier: slotTier(task.ABSlot(slot)), Verdict: s.verdict, Findings: s.findings,
		Model: s.model, Effort: s.effort,
	}}
	rec.Stages = []runstore.StageTiming{
		{Name: ab.ImplementStage, Model: abCoderShared, ModelUSD: s.coderUSD},
		{Name: slotStage(task.ABSlot(slot)), Model: s.model, ModelUSD: s.reviewUSD},
	}
	if len(championRun) > 0 {
		rec.AB.ChampionRun = championRun[0]
	}
	return rec
}

// slotTier is the tier a level's round is filed under, and slotStage the stage
// its review runs under — the runner's own names for the two (B1.3).
func slotTier(slot task.ABSlot) string {
	switch slot {
	case task.ABSlotL1:
		return runstore.TierFirst
	case task.ABSlotL3:
		return runstore.TierAdversarial
	}
	return runstore.TierSenior
}

func slotStage(slot task.ABSlot) string {
	switch slot {
	case task.ABSlotL1:
		return "review_first"
	case task.ABSlotL3:
		return "adversarial"
	}
	return "review"
}

// TestABReportGroupsBySlot is B1.3's grouping: `fugaro ab` groups by slot ×
// model × effort, so a repository's coder experiment and each of its review
// levels are rows of their own, and at one level two reviewers — or one
// reviewer at two efforts — are two experiments rather than one group (D560's
// ban is on a per-person dimension, and none of these is one).
func TestABReportGroupsBySlot(t *testing.T) {
	coder := pair("p-coder", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta, cost: 1, priced: true})

	level1 := reviewerPairAt("p-l1", "l1", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 1, verdict: "ship"},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 1, verdict: "ship"})

	level2 := reviewerPairAt("p-l2", "l2", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 2, verdict: "ship"},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 2, verdict: "ship"})

	// The same reviewer models at another effort of the champion's, and the
	// same champion reviewer against a third model: two experiments again, and
	// the only two facts that differ from the pair above.
	level2Effort := reviewerPairAt("p-l2-effort", "l2", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "medium", findings: 2, verdict: "ship"},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 2, verdict: "ship"})

	level2Model := reviewerPairAt("p-l2-model", "l2", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 2, verdict: "ship"},
		reviewerSpec{model: abReviewerAlt, effort: "high", findings: 2, verdict: "ship"})

	level3 := reviewerPairAt("p-l3", "l3", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 3, verdict: "ship"},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 3, verdict: "ship"})

	rep := BuildAB(records(coder, level1, level2, level2Effort, level2Model, level3), nil, nil)
	if rep.Pairs != 6 {
		t.Fatalf("the report counts %d pairs, want 6: every pair here is valid (R2)", rep.Pairs)
	}

	// One row per experiment, in the slot vocabulary's order and then by the
	// four model and effort keys: the coder's pair, then level 1, then level 2's
	// three, then level 3.
	want := []struct {
		slot             string
		champion         string
		championEffort   string
		challenger       string
		challengerEffort string
	}{
		{string(task.ABSlotCoder), abModelAlpha, "", abModelBeta, ""},
		{"l1", abReviewerOne, "high", abReviewerTwo, "low"},
		{"l2", abReviewerOne, "high", abReviewerAlt, "high"},
		{"l2", abReviewerOne, "high", abReviewerTwo, "low"},
		{"l2", abReviewerOne, "medium", abReviewerTwo, "low"},
		{"l3", abReviewerOne, "high", abReviewerTwo, "low"},
	}
	if len(rep.Rows) != len(want) {
		t.Fatalf("the report holds %d rows, want %d: %+v", len(rep.Rows), len(want), rep.Rows)
	}
	for i, w := range want {
		r := rep.Rows[i]
		if r.Slot != w.slot || r.Champion != w.champion || r.ChampionEffort != w.championEffort ||
			r.Challenger != w.challenger || r.ChallengerEffort != w.challengerEffort {
			t.Errorf("row %d = %s %s@%s -> %s@%s, want %s %s@%s -> %s@%s: a row is one slot, model and effort pair (B1.3)",
				i, r.Slot, r.Champion, r.ChampionEffort, r.Challenger, r.ChallengerEffort,
				w.slot, w.champion, w.championEffort, w.challenger, w.challengerEffort)
		}
		if r.Pairs != 1 || r.Ties != 1 {
			t.Errorf("row %d counts %d pairs and %d ties, want 1 and 1: one pair each, and both rounds answered", i, r.Pairs, r.Ties)
		}
		if r.Champion == abCoderShared || r.Challenger == abCoderShared {
			t.Errorf("row %d shows the coder model %q, which both arms of a reviewer pair share: the model that varies is the level's reviewer (D728)", i, abCoderShared)
		}
	}

	// The models a reviewer pair does NOT vary never reach the row: the coder
	// both arms ran is fixed by the arm's own replay (B1.2).
	reviewerRow := rep.Rows[1]
	if reviewerRow.Champion != abReviewerOne || reviewerRow.Challenger != abReviewerTwo {
		t.Errorf("the level-1 row reads %q -> %q, want the two reviewers", reviewerRow.Champion, reviewerRow.Challenger)
	}

	var table bytes.Buffer
	if err := ABTable(&table, rep, ABMeta{Project: "proj-1234", Source: "run-records"}); err != nil {
		t.Fatalf("ABTable: %v", err)
	}
	for _, cell := range []string{"SLOT", "EFFORT", "MISSES", "FALSE ALARMS", "MISS RATE", "l1", "l2", "l3", "high", "low", "medium"} {
		if !strings.Contains(table.String(), cell) {
			t.Errorf("the table prints no %q, so the columns B1.3 adds are not there:\n%s", cell, table.String())
		}
	}
}

// TestABReviewerSlotCostIsTheLevels is B1.3's cost for a reviewer pair: the
// ratio is the level's own review cost, the challenger's over the champion's,
// and never the runs' whole model dollars — both arms ran the champion's own
// coder over the champion's own patch (B1.2), and what the pair varies is the
// review, so the coder's spend is the fixed part of the experiment.
func TestABReviewerSlotCostIsTheLevels(t *testing.T) {
	p := reviewerPairAt("p-l2", "l2", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 2, verdict: "ship", reviewUSD: 0.20, coderUSD: 1, runUSD: 1},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 2, verdict: "ship", reviewUSD: 0.40, coderUSD: 10, runUSD: 10})

	row := oneRow(t, BuildAB(records(p), nil, nil))
	if !row.Rec.HasCost || row.Rec.CostRatio != 2 {
		t.Errorf("the row's cost ratio reads %v (has %v), want 2: the level's stages price the pair, 0.40 over 0.20", row.Rec.CostRatio, row.Rec.HasCost)
	}
}

// TestABReviewerMissRateIsNotAFigure is B1.3's caveat, and it is the test the
// plan names for B1.3's own change: a reviewer slot's miss rate is not a figure
// while U4 — whether the blind judge can match two finding sets reliably — is
// unmeasured, so `fugaro ab` prints the reason in the cell and never a number,
// and the counts it does print (misses and false alarms) are the judge's
// marking, absent today because no judge run matches two finding sets.
//
// The wording is the whole of the seam: ABMissRateUnreliable is what the cell,
// the note and the document's warning read, and a record of Check 211 replaces
// that one constant with the figure. This test fails on any other spelling
// on purpose — a rate printed as a number is the thing it exists to prevent.
func TestABReviewerMissRateIsNotAFigure(t *testing.T) {
	const wording = "unreliable (U4 not measured)"
	if ABMissRateUnreliable != wording {
		t.Fatalf("the seam reads %q, want %q: the cell, the note and the document's warning all print it, so this is the one line a recorded Check 211 changes", ABMissRateUnreliable, wording)
	}

	reviewer := reviewerPairAt("p-l2", "l2", rigor.Pro, tier.Small,
		reviewerSpec{model: abReviewerOne, effort: "high", findings: 2, verdict: "ship", reviewUSD: 0.2},
		reviewerSpec{model: abReviewerTwo, effort: "low", findings: 1, verdict: "ship", reviewUSD: 0.2})
	coder := pair("p-coder", rigor.Pro, tier.Small,
		arm{model: abModelAlpha, cost: 1, priced: true},
		arm{model: abModelBeta, cost: 1, priced: true})
	rep := BuildAB(records(coder, reviewer), nil, nil)
	if len(rep.Rows) != 2 {
		t.Fatalf("the report holds %d rows, want 2 (a coder pair and a reviewer pair): %+v", len(rep.Rows), rep.Rows)
	}
	coderRow, reviewerRow := rep.Rows[0], rep.Rows[1]

	if got := missRateCell(reviewerRow); got != ABMissRateUnreliable {
		t.Errorf("the reviewer row's MISS RATE cell reads %q, want %q: a rate nobody has checked is never printed as a number beside the counts that were", got, ABMissRateUnreliable)
	}
	if got := missRateCell(coderRow); got != "-" {
		t.Errorf("the coder row's MISS RATE cell reads %q, want %q: a coder pair varies no review level and has no miss rate at all", got, "-")
	}
	if got := effortCell(reviewerRow); got != "high/low" {
		t.Errorf("the reviewer row's EFFORT cell reads %q, want %q", got, "high/low")
	}
	if got := effortCell(coderRow); got != "-" {
		t.Errorf("the coder row's EFFORT cell reads %q, want %q: a coder pair records no effort", got, "-")
	}
	// The two counts a judge would mark are reported as dashes and not as the
	// zeroes they are held at: nothing has matched this pair's findings.
	if got := marked(reviewerRow.Misses, reviewerRow.Matched); got != "-" {
		t.Errorf("the reviewer row's MISSES cell reads %q, want %q: no judge has matched the pair, which is not a marking of zero", got, "-")
	}
	if got := marked(reviewerRow.FalseAlarms, reviewerRow.Matched); got != "-" {
		t.Errorf("the reviewer row's FALSE ALARMS cell reads %q, want %q", got, "-")
	}

	var table bytes.Buffer
	if err := ABTable(&table, rep, ABMeta{Project: "proj-1234", Source: "run-records"}); err != nil {
		t.Fatalf("ABTable: %v", err)
	}
	if !strings.Contains(table.String(), wording) {
		t.Errorf("the table does not print %q, so a reader is shown a miss rate column with nothing in it to explain it:\n%s", wording, table.String())
	}
	if !slices.ContainsFunc(abNotes(rep), func(n string) bool { return strings.Contains(n, wording) }) {
		t.Errorf("no note under the table carries %q: the reasons the columns are what they are belong with the table (B1.3)", wording)
	}

	var doc bytes.Buffer
	if err := ABJSON(&doc, rep, ABMeta{Project: "proj-1234", Source: "run-records"}); err != nil {
		t.Fatalf("ABJSON: %v", err)
	}
	var parsed struct {
		Rows []struct {
			Slot        string `json:"slot"`
			Misses      *int   `json:"misses"`
			FalseAlarms *int   `json:"false_alarms"`
		} `json:"rows"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(doc.Bytes(), &parsed); err != nil {
		t.Fatalf("the document is not JSON: %v\n%s", err, doc.String())
	}
	if len(parsed.Rows) != 2 {
		t.Fatalf("the document holds %d rows, want 2", len(parsed.Rows))
	}
	if parsed.Rows[1].Slot != "l2" {
		t.Errorf("the reviewer row's slot reads %q, want %q", parsed.Rows[1].Slot, "l2")
	}
	for i, row := range parsed.Rows {
		if row.Misses != nil || row.FalseAlarms != nil {
			t.Errorf("row %d carries misses %v and false alarms %v, want both null: no judge run matches two finding sets yet", i, row.Misses, row.FalseAlarms)
		}
	}
	if !slices.ContainsFunc(parsed.Warnings, func(w string) bool { return strings.Contains(w, wording) }) {
		t.Errorf("the document's warnings do not carry %q, so a consumer reading misses and false alarms is not told the rate is missing:\n%s", wording, doc.String())
	}

	// A report of coder pairs alone says nothing about a reviewer rate: the
	// caveat is printed where it applies, like every other note.
	doc.Reset()
	if err := ABJSON(&doc, BuildAB(records(coder), nil, nil), ABMeta{Project: "proj-1234", Source: "run-records"}); err != nil {
		t.Fatalf("ABJSON: %v", err)
	}
	if strings.Contains(doc.String(), wording) {
		t.Errorf("a report of coder pairs alone carries the reviewer caveat:\n%s", doc.String())
	}
}
