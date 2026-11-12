package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The cap's own deferrals (T-TR8, docs/post-review-triage): when a run's
// review rounds end with findings still open, the findings the cap does not
// hold it on are filed as follow-ups instead of being dropped. The rule is the
// stop's, not the judge's, so a repository whose `agent.triage` is off files
// them all the same (D1858) — and nothing is filed where the repository never
// opted in (D1852). What each mode does at its cap is the mode's own column of
// the same table: Startup and POC merge with the round and file what they
// merged with, Pro keeps the draft and still files the leftovers (D1845),
// Critical tolerates nothing so it files nothing (D7), and a floor finding is
// never among them (D1845).

// capReason is the one line the cap writes for a finding it files: the entry's
// reason reaches a follow-up task's brief and the pull request's deferred
// section, so a case pins the words and not only the presence of an entry.
const capReason = "the run reached its review cap with this finding still open"

// TestCapHitStartupFinalizesReadyWithDeferrals is acceptance 43 and this
// slice's red step: a Startup run whose one review round is its last ends ready
// with the round's findings filed, and not a draft on a count it has no round
// left to act on. The finding the mode's floor blocks and the judge blocks with
// it is the stop's to accept (BF1m), and the run files it as the follow-up it
// is — the round the cap ended is the round neither the fixer nor another
// review will see again.
//
// The trivial finding is already in the block when the cap reads it, filed by
// the table at the judge's own round (T-TR7b), and the cap keeps that entry:
// the finding is not deferred twice and the entry still says who placed it.
func TestCapHitStartupFinalizesReadyWithDeferrals(t *testing.T) {
	h := newHarness(t, startupCfg(t, 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding(), trivialFinding()),
		triageVerdicts("block", "block"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage") {
		t.Fatalf("stages = %v, want the round judged and no fix bought: the cap leaves no round to fix in", got)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, want a ready run: the stop accepts the round it ended on", rec)
	}
	if want := "review round 1 left 2 findings, accepted at startup (rounds spent)"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	ac := rec.Accepted
	if ac == nil || ac.Total != 2 || ac.At != runstore.StopRoundsSpent {
		t.Errorf("the record's accepted block = %+v, want the round the cap ended with the findings in it", ac)
	}
	d := deferredOf(rec)
	blocker, ok := d[findingKeyOf("feature.txt", "unbounded retry loop in the fetch path")]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the finding the cap left open", rec.Deferred)
	}
	if blocker.Source != runstore.TriageSourceCap || blocker.Reason != capReason {
		t.Errorf("the cap's deferral = %+v, want the stop's own source and one-line reason", blocker)
	}
	trivial, ok := d[findingKeyOf("feature.txt", trivialSummary)]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the tolerated finding filed too", rec.Deferred)
	}
	if trivial.Source != runstore.TriageSourceTable {
		t.Errorf("the trivial finding's entry = %+v, want the table's entry the judge's round made: the cap does not rewrite who deferred a finding", trivial)
	}
}

// TestCapHitProStaysDraftButRecordsDeferrals: Pro's row names no stop, so a run
// of it that spends its rounds is a draft however few findings the round left
// (BF1m) — and the leftovers are still filed (D1845). The finding the mode's
// own floor blocks holds the merge and stays the round's own business: it is
// not a follow-up, and the entry is the one the mode's table already made
// (Tolerated).
func TestCapHitProStaysDraftButRecordsDeferrals(t *testing.T) {
	h := newHarness(t, modeCfg(t, "pro", 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding(), trivialFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, want a draft: Pro's row keeps the round it stopped on", rec)
	}
	if want := "review round 1 still has 2 findings"; rec.Reason != want {
		t.Errorf("reason = %q, want %q: the count is the reason Pro always gave", rec.Reason, want)
	}
	if ac := rec.Accepted; ac != nil {
		t.Errorf("the record's accepted block = %+v, want none: the stop accepted nothing on a draft", ac)
	}
	tol := rec.Tolerated
	if tol == nil || tol.Total != 1 {
		t.Errorf("the record's tolerated block = %+v, want the finding the mode's table let through, as it always was", tol)
	}
	d := deferredOf(rec)
	if len(d) != 1 {
		t.Fatalf("the record's deferred block = %+v, want the one leftover the draft left open", rec.Deferred)
	}
	left, ok := d[findingKeyOf("feature.txt", trivialSummary)]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the tolerated finding filed as a follow-up", rec.Deferred)
	}
	if left.Source != runstore.TriageSourceCap {
		t.Errorf("the deferral = %+v, want the cap's own source: no judge was asked in this run", left)
	}
	if _, ok := d[findingKeyOf("feature.txt", "unbounded retry loop in the fetch path")]; ok {
		t.Errorf("the record filed the finding that holds the draft as a follow-up: %+v", rec.Deferred)
	}
}

// TestCapHitFloorFindingHoldsTheDraft is D1845's floor clause at the cap: a
// finding the mode's floor names holds the merge whatever the stop would take,
// and it is no one's follow-up — not the judge's (F3), and not the cap's,
// which is the block this case pins. The trivial finding beside it is filed,
// so the case would see a block the cap wrote and not an empty one.
func TestCapHitFloorFindingHoldsTheDraft(t *testing.T) {
	h := newHarness(t, modeCfg(t, "startup", 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", floorFinding(), trivialFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v (reason %q), want a draft: the floor finding is one the stop does not accept", rec, rec.Reason)
	}
	if want := "review round 1 still has 2 findings"; rec.Reason != want {
		t.Errorf("reason = %q, want %q: the cap let nothing of this round through", rec.Reason, want)
	}
	if ac := rec.Accepted; ac != nil {
		t.Errorf("the record's accepted block = %+v, want none: a floor finding is not accepted at the stop", ac)
	}
	d := deferredOf(rec)
	if _, ok := d[findingKeyOf("feature.txt", floorSummary)]; ok {
		t.Fatalf("the record filed a floor finding as the cap's follow-up: %+v", rec.Deferred)
	}
	if _, ok := d[findingKeyOf("feature.txt", trivialSummary)]; !ok {
		t.Errorf("the record's deferred block = %+v, want the one finding the cap did leave open", rec.Deferred)
	}
}

// TestCapHitCriticalUnchanged: Critical tolerates nothing (D7), so a cap on a
// run of it leaves nothing to defer and the run records what it always did — a
// draft on the round's count, with no triage block, no deferral and no accepted
// block. The adversarial round is the one the cap ends on, which is the plan's
// last step, and its finding is one every mode above POC blocks on.
func TestCapHitCriticalUnchanged(t *testing.T) {
	h := newHarness(t, criticalCfg(t, 1, "  adversarial_rounds: 1\n"), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", trivialFinding()),
		reviewScored("changes", trivialFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "adversarial") {
		t.Fatalf("stages = %v, want the mode's own levels and no triage stage (D1851)", got)
	}
	if rec.Triage != nil || rec.Deferred != nil {
		t.Errorf("Critical triaged or deferred something: %+v / %+v", rec.Triage, rec.Deferred)
	}
	if rec.Tolerated != nil || rec.Accepted != nil {
		t.Errorf("the record = %+v / %+v, want the two blocks a Critical run never had", rec.Tolerated, rec.Accepted)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, want a draft: Every finding blocks in Critical", rec)
	}
	if want := "review round 1 still has 1 finding"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
}

// TestCapHitUnconfiguredRecordsNoDeferrals: a repository that never opted in
// defers nothing at a cap (D1858, D1852), which is the record it always had —
// the run resolves Pro (D19), blocks on the count and files no follow-up.
func TestCapHitUnconfiguredRecordsNoDeferrals(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", trivialFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Pro {
		t.Fatalf("the run resolved %+v, want the Pro an unconfigured repository gets (D19)", rec.Rigor)
	}
	if rec.Triage != nil || rec.Deferred != nil {
		t.Errorf("an unconfigured repository triaged or deferred something: %+v / %+v", rec.Triage, rec.Deferred)
	}
	if rec.Tolerated != nil || rec.Accepted != nil {
		t.Errorf("the record = %+v / %+v, want the blocks an unconfigured repository never had", rec.Tolerated, rec.Accepted)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v (reason %q), want the draft a configured-mode run would not get", rec, rec.Reason)
	}
	if want := "review round 1 still has 1 finding"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
}

// ownerAskSummary is a finding whose only remedy is a decision the run cannot
// make (OD1), which is the reviewer's flag and not a score.
const ownerAskSummary = "the licence text needs counsel's clearance"

// ownerAskFinding is that finding at scores the stop would otherwise let
// through: likelihood 1 x impact 1 is below Startup's floor of 8, so the flag
// alone is what this case reads.
func ownerAskFinding() map[string]any {
	return map[string]any{
		"severity": "minor", "file": "feature.txt", "summary": ownerAskSummary, "needs_owner": true,
		"likelihood": 1, "impact": 1, "fix_cost": 1, "class": "cosmetic",
	}
}

// TestCapHitDoesNotFileAnOwnerFinding: the cap files what it leaves open as
// follow-ups — tasks a later run picks up and acts on — and a finding only the
// owner can settle is not one of them: no code change makes the decision (OD1),
// and the judge is the same way about it (triage.Apply). A cap filing it would
// hand `fugaro followups` a coding task for the owner's question while the
// record beside it says the run is waiting on that very decision.
func TestCapHitDoesNotFileAnOwnerFinding(t *testing.T) {
	h := newHarness(t, modeCfg(t, "startup", 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding(), ownerAskFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 {
		t.Fatalf("owner decisions = %+v, want the one the capped round left", rec.OwnerDecisions)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review is waiting on 1 owner decision" {
		t.Fatalf("rec = %+v (reason %q), want the draft the owner's question leaves", rec, rec.Reason)
	}
	d := deferredOf(rec)
	if _, ok := d[findingKeyOf("feature.txt", ownerAskSummary)]; ok {
		t.Errorf("the record filed the owner's finding as a follow-up: %+v", rec.Deferred)
	}
	left, ok := d[findingKeyOf("feature.txt", "unbounded retry loop in the fetch path")]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the round's other finding filed by the cap", rec.Deferred)
	}
	if left.Source != runstore.TriageSourceCap || left.Reason != capReason {
		t.Errorf("the cap's deferral = %+v, want the stop's own source and one-line reason", left)
	}
}

// TestCapHitTriageOffStillRecordsTheCapsDeferrals is D1858: the judge's switch
// is not what defers these findings — the cap is — so a Startup run that asked
// no judge at all files the whole round it stopped on, and merges with it. The
// stages say so: nothing between the review and the ending.
func TestCapHitTriageOffStillRecordsTheCapsDeferrals(t *testing.T) {
	h := newHarness(t, modeCfg(t, "startup", 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding(), trivialFinding()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v, want no triage stage: the switch is off", got)
	}
	if rec.Triage != nil {
		t.Errorf("the record carries a triage block with the stage off: %+v", rec.Triage)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v (reason %q), want a ready run: the stop accepts the round it ended on", rec, rec.Reason)
	}
	d := deferredOf(rec)
	if len(d) != 2 {
		t.Fatalf("the record's deferred block = %+v, want every finding the cap left open", rec.Deferred)
	}
	for _, key := range []string{
		findingKeyOf("feature.txt", "unbounded retry loop in the fetch path"),
		findingKeyOf("feature.txt", trivialSummary),
	} {
		e, ok := d[key]
		if !ok {
			t.Fatalf("the record's deferred block = %+v, want the finding %q filed by the cap", rec.Deferred, key)
		}
		if e.Source != runstore.TriageSourceCap || e.Reason != capReason {
			t.Errorf("the cap's deferral = %+v, want the stop's own source and reason with no judge in the run", e)
		}
	}
}
