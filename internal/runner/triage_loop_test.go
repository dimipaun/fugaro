package runner_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The triage stage's second slice (T-TR7b, docs/post-review-triage): the judge
// now acts. A round whose findings are all deferred ends its own level and buys
// no fix round, a mixed round hands the fixer only the findings the judge
// blocked, and every level's rounds are judged. What the stage is *not* is
// below as well: with the switch off, in POC and Critical, and for a repository
// that never opted in, the loop is the one it was before the stage existed
// (F32), and a deferral a Startup round made never becomes ready over a mode
// that rose at finalize (F31).

const (
	// trivialSummary is the finding trivialFinding carries: a case names it
	// again to read the key the record files the deferral under.
	trivialSummary = "the doc comment drifts from the retry"
	// floorSummary is floorFinding's, for the same reason.
	floorSummary = "the token is logged in the clear"
)

// trivialFinding is a finding the mode's table tolerates and no floor names:
// likelihood 1 x impact 1 is a risk of 1 against Startup's floor of 8, so the
// table alone lets it through whatever a judge answers — the judge may neither
// add a block the table does not hold nor claim a deferral the table already
// made (D1857, T-TR6).
func trivialFinding() map[string]any {
	return map[string]any{
		"severity": "minor", "file": "feature.txt", "summary": trivialSummary,
		"likelihood": 1, "impact": 1, "fix_cost": 1, "class": "cosmetic",
	}
}

// floorFinding is a finding no verdict may defer: class security is the floor in
// every mode (rigor.Floor), so a judge that defers it changes nothing at all.
func floorFinding() map[string]any {
	return map[string]any{
		"severity": "blocker", "file": "feature.txt", "summary": floorSummary,
		"likelihood": 2, "impact": 2, "fix_cost": 1, "class": "security",
	}
}

// triageVerdicts answers the judge's brief with one word per finding it
// numbered, taken in the brief's own order: triageStep's single word cannot
// answer a mixed round, where the two answers are what the case is about. A
// finding the words do not reach is answered block, which is the strict
// direction and never a deferral a case did not ask for.
func triageVerdicts(words ...string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		findings := []map[string]any{}
		for _, m := range briefFindingRE.FindAllStringSubmatch(req.Prompt, -1) {
			i, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("the brief's finding header %q does not parse: %v", m[0], err)
			}
			word := "block"
			if i < len(words) {
				word = words[i]
			}
			findings = append(findings, map[string]any{
				"index": i, "key": m[2], "verdict": word, "reason": "judged for this test",
			})
		}
		b, err := json.Marshal(map[string]any{"version": 1, "findings": findings})
		if err != nil {
			t.Fatal(err)
		}
		return agent.Result{Structured: b, CostUSD: 0.25}, nil
	}
}

// deferredOf is the record's deferred block keyed by the finding's own key, so a
// case can say which finding was filed and not only how many were.
func deferredOf(rec *runstore.Record) map[string]runstore.DeferredFinding {
	out := map[string]runstore.DeferredFinding{}
	if rec.Deferred == nil {
		return out
	}
	for _, f := range rec.Deferred.Findings {
		out[f.Key] = f
	}
	return out
}

// TestStartupTrivialOnlyFindingsAreReadyAfterOneReview is acceptance 1 and this
// slice's red step: a Startup patch whose only finding is a trivial one is ready
// after one review, with the finding filed as a follow-up. The judge is asked
// and answers *block* — the table's tolerance is what stands, and the run must
// not buy a fix round for a finding its mode already let through (F4, F16).
func TestStartupTrivialOnlyFindingsAreReadyAfterOneReview(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", trivialFinding()),
		triageStep("block"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage") {
		t.Fatalf("stages = %v, want the loop to end at the round nothing blocks in", got)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, want the patch published ready after its one review", rec)
	}
	key := findingKeyOf("feature.txt", trivialSummary)
	d, ok := deferredOf(rec)[key]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the tolerated finding filed as a follow-up", rec.Deferred)
	}
	if d.Source != runstore.TriageSourceTable || d.Summary != trivialSummary {
		t.Errorf("the deferral = %+v, want the reviewer's own words under the table's source: the judge answered block and the mode's row is what let the finding through (D1857)", d)
	}
}

// TestAllDeferredRoundEndsTheLoop: a round whose findings are all deferred ends
// the review loop — no fix round, no second review round — and the run
// finalizes ready, naming the mode that let the findings through. The finding
// here is one the table blocks and the judge deferred, which is the judge's own
// deferral and not the table's.
func TestAllDeferredRoundEndsTheLoop(t *testing.T) {
	h := newHarness(t, triageCfg(t, "rigor:\n  default: pro\n", 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage") {
		t.Fatalf("stages = %v, want no fix round and no second review", got)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, want a ready run: nothing the round left blocks", rec)
	}
	if want := "review round 1 left 1 finding, tolerated at pro"; rec.Reason != want {
		t.Errorf("reason = %q, want %q: the mode and the count the round was tolerated at", rec.Reason, want)
	}
	d, ok := deferredOf(rec)[findingKeyOf("feature.txt", "unbounded retry loop in the fetch path")]
	if !ok {
		t.Fatalf("the record's deferred block = %+v, want the judge's deferral filed", rec.Deferred)
	}
	if d.Source != runstore.TriageSourceAgent || d.Reason != "judged for this test" {
		t.Errorf("the deferral = %+v, want the judge's own word and reason", d)
	}
}

// TestFixerIsNeverGivenADeferredFinding is acceptance 2: the fixer never
// changes code for a deferred finding. The round below leaves one finding the
// judge blocks and one it defers, so a fix round is bought — and its brief
// carries the blocking finding alone, the deferred one's text nowhere in it.
func TestFixerIsNeverGivenADeferredFinding(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding(), trivialFinding()),
		triageVerdicts("block", "defer"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage", "fix", "review", "triage") {
		t.Fatalf("stages = %v, want the round's fix bought and the round after it reviewed", got)
	}
	fix := h.agent.calls[3]
	if !strings.Contains(fix.Prompt, "- feature.txt: unbounded retry loop in the fetch path") {
		t.Errorf("the fixer was not handed the finding the judge blocked:\n%s", fix.Prompt)
	}
	if strings.Contains(fix.Prompt, trivialSummary) {
		t.Errorf("the fixer was handed a finding the judge deferred:\n%s", fix.Prompt)
	}
	if _, ok := deferredOf(rec)[findingKeyOf("feature.txt", trivialSummary)]; !ok {
		t.Errorf("the record's deferred block = %+v, want the deferred finding filed", rec.Deferred)
	}
}

// TestLevelThreeFindingsAreTriagedButFloorStillBlocks: every level's findings
// are judged, level 3's included (D1847), and the floor is what no verdict
// moves (F3): the adversarial round's security finding is deferred by the judge
// and blocks anyway, so the run ends a draft on the level that read it.
func TestLevelThreeFindingsAreTriagedButFloorStillBlocks(t *testing.T) {
	cfg := startupCfg(t, 1, "  adversarial_rounds: 1\n  levels: { l3: { model: senior-model } }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0),
		triageStep("defer"),
		reviewScored("changes", floorFinding()),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage", "adversarial", "triage") {
		t.Fatalf("stages = %v, want level 3's round judged like level 2's", got)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want a round per level", tr)
	}
	last := tr.Rounds[1]
	if last.Level != policy.LevelL3 || last.Round != 1 {
		t.Fatalf("the second triage round = %+v, want round 1 of level 3", last)
	}
	if len(last.Entries) != 1 {
		t.Fatalf("the round's entries = %+v, want the one finding it read", last.Entries)
	}
	e := last.Entries[0]
	if e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceTable {
		t.Errorf("the entry = %+v, want a block the table placed: the judge deferred a floor finding and the floor is what stands (F3)", e)
	}
	if e.Key != findingKeyOf("feature.txt", floorSummary) {
		t.Errorf("the entry's key = %q, want the digest of the finding it is about", e.Key)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Errorf("rec = %+v, want a draft: the floor finding holds the merge", rec)
	}
	if _, ok := deferredOf(rec)[findingKeyOf("feature.txt", floorSummary)]; ok {
		t.Errorf("the record filed a floor finding as a deferral: %+v", rec.Deferred)
	}
}

// TestAllDeferredLevelTwoAdvancesToLevelThree: an all-deferred round ends *its
// level* and the loop moves to the next enabled one (F21). Level 2's round is
// one the judge deferred whole, level 3's ships, and both levels' rounds are in
// the record.
func TestAllDeferredLevelTwoAdvancesToLevelThree(t *testing.T) {
	cfg := startupCfg(t, 1, "  levels: { l3: { model: senior-model } }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("defer"),
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage", "adversarial", "triage") {
		t.Fatalf("stages = %v, want level 3 to run after a level 2 round nothing blocks in", got)
	}
	if tr := rec.Triage; tr == nil || len(tr.Rounds) != 2 || tr.Rounds[0].Level != policy.LevelL2 || tr.Rounds[1].Level != policy.LevelL3 {
		t.Fatalf("the record's triage block = %+v, want one round per level", rec.Triage)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Errorf("rec = %+v, want a ready run: level 3 shipped and level 2 held nothing back", rec)
	}
}

// TestHeldBackLevelReadsTheDeferredSet: the held-back rule reads the deferred
// set and not the recorded verdict word (F21). A level 2 round whose findings
// the judge deferred whole does not hold the pull request a draft after level 3
// ships, and the same round with one finding the judge blocked does — which is
// the rule, and not a level 2 the loop never left.
func TestHeldBackLevelReadsTheDeferredSet(t *testing.T) {
	cfg := startupCfg(t, 1, "  levels: { l3: { model: senior-model } }\n")
	steps := []struct {
		name string
		word string
		want runstore.Outcome
	}{
		{"the judge deferred the round whole", "defer", runstore.OutcomeReady},
		{"the judge blocked it", "block", runstore.OutcomeDraft},
	}
	for _, c := range steps {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, cfg, nil)
			rec, err := h.run(t,
				implement("feature"),
				reviewScored("changes", blockingFinding()),
				triageStep(c.word),
				review("ship", 0),
				triageStep("defer"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Outcome != c.want {
				t.Fatalf("outcome = %q (reason %q), want %q", rec.Outcome, rec.Reason, c.want)
			}
			if c.want == runstore.OutcomeDraft && !strings.Contains(rec.Reason, "last shipped nothing") {
				t.Errorf("reason = %q, want the held-back level named", rec.Reason)
			}
		})
	}
}

// TestNeedsOwnerFindingStillStopsTheLoopWhenTriaged: a finding only the owner
// can settle is never the judge's to defer (D1856, F22). The judge answers
// defer, the placement stands as the table's block, and the run stops waiting
// for the person — no fix round, and the question in the record where the
// follow-up that answers it can be built from it.
func TestNeedsOwnerFindingStillStopsTheLoopWhenTriaged(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := countStage(rec, "fix"); got != 0 {
		t.Errorf("%d fix stages, want none: no code change settles the owner's decision", got)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltOwnerDecision {
		t.Errorf("halt = %+v, want the owner-decision halt", rec.Halt)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 {
		t.Fatalf("owner decisions = %+v, want the one the round left", rec.OwnerDecisions)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 1 || len(tr.Rounds[0].Entries) != 1 {
		t.Fatalf("the record's triage block = %+v, want the round's one placement", tr)
	}
	e := tr.Rounds[0].Entries[0]
	if e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceTable {
		t.Errorf("the entry = %+v, want a block the table placed: a needs_owner finding is never a judge's to defer (D1856)", e)
	}
	if _, ok := deferredOf(rec)[e.Key]; ok {
		t.Errorf("the record filed the owner's finding as a deferral: %+v", rec.Deferred)
	}
}

// TestTriageOffLoopUnchanged is F32's first half: with the switch off the loop
// and the fixer are the ones they were before the stage existed. A round whose
// only finding the table tolerates still buys a fix round and still gets a
// second review — the tolerance acts in Decide and nowhere earlier — and
// nothing is filed as a deferral, because nothing deferred it.
func TestTriageOffLoopUnchanged(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	cfg := triageOff(t, startupCfg(t, 2, ""))
	h = newHarness(t, cfg, nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", trivialFinding()),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
		t.Fatalf("stages = %v, want the loop the run ran before the stage existed", got)
	}
	if rec.Triage != nil || rec.Deferred != nil {
		t.Errorf("a run that triages nothing carries a triage block or a deferral: %+v / %+v", rec.Triage, rec.Deferred)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Errorf("rec = %+v, want the round's tolerated finding let through as it always was", rec)
	}
}

// TestUnconfiguredRepositoryNeverDefers is F32's second half: a repository that
// never opted in defers nothing (D1852), whatever its own file says. The switch
// is written on and the mode resolves Pro by default (D19), and the loop is the
// one the run would have run with the stage off.
func TestUnconfiguredRepositoryNeverDefers(t *testing.T) {
	h := newHarness(t, triageOn(t, firstLineCfg(t, 2, "")), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", trivialFinding()),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Pro {
		t.Fatalf("the run resolved %+v, want the Pro an unconfigured repository gets (D19)", rec.Rigor)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
		t.Fatalf("stages = %v, want the loop the run ran before the stage existed", got)
	}
	if rec.Triage != nil || rec.Deferred != nil {
		t.Errorf("an unconfigured repository triaged or deferred something: %+v / %+v", rec.Triage, rec.Deferred)
	}
}

// TestPOCAndCriticalLoopUnchanged is F32's third half: the two modes whose own
// table decides everything make no triage call (D1851, D1855), and the switch
// the repository wrote cannot put the stage back into them. Both runs buy the
// fix round a tolerated-only round has always bought, and neither records a
// deferral.
func TestPOCAndCriticalLoopUnchanged(t *testing.T) {
	cases := []struct {
		mode   string
		stages []string
		steps  []step
	}{
		// POC runs the cheap loop alone and ends the run after the fix its round
		// asked for.
		{"poc", []string{"implement", "review_first", "fix"}, []step{implement("feature"), reviewScored("changes", trivialFinding()), fixStep}},
		// Critical turns level 3 on for every run (rigor.defaultLevels), so its
		// script answers the adversarial round too. Its level 2 has both rounds
		// to spend, which is where the fix a tolerated-only round has always
		// bought is visible.
		{"critical", []string{"implement", "review", "fix", "review", "adversarial"}, []step{
			implement("feature"), reviewScored("changes", trivialFinding()), fixStep, review("ship", 0), review("ship", 0),
		}},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			h := newHarness(t, triageCfg(t, "rigor:\n  default: "+c.mode+"\n", 2, ""), nil)
			rec, err := h.run(t, c.steps...)
			if err != nil {
				t.Fatal(err)
			}
			if got := stageNames(rec); !eq(got, c.stages...) {
				t.Fatalf("stages = %v, want %v: the mode's loop is the one it always ran", got, c.stages)
			}
			if got := countStage(rec, "fix"); got != 1 {
				t.Errorf("%d fix stages, want the round's fix bought as it always was", got)
			}
			if rec.Triage != nil || rec.Deferred != nil {
				t.Errorf("%s triaged or deferred something: %+v / %+v", c.mode, rec.Triage, rec.Deferred)
			}
		})
	}
}
