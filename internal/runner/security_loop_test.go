package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The escalation's activation wiring (D1507): the two seams the runner calls
// it at, and the plan the loop that follows walks. security_test.go pins the
// escalation's own body — what it reads, what it decides and what it records —
// and these two cases are the half no body-level test can see: a call site
// removed, or a plan the loop captured before the escalation rebuilt it, would
// leave every one of those green while a real run records an escalation and
// never spends it.

// securityLoopRecipe is a loop recipe with the first line and the senior review
// and no adversarial step: level 3 is off for the run it starts, so the only
// adversarial stage a case below reaches is the one the escalation inserted in
// the plan (planRecipe's O7 insertion).
const securityLoopRecipe = "version: 1\nname: security\nsteps:\n  - first_line: {}\n  - review: {}\n"

// securityLoopFiles is the repository file map for securityLoopRecipe.
var securityLoopFiles = map[string]string{".fugaro/recipes/security.yaml": securityLoopRecipe}

// securityLoopCfg is a Startup repository — level 2 on, level 3 off — with the
// security recipe and the first line on, so the plan the run starts with is the
// first line and the senior review and nothing else.
func securityLoopCfg(t *testing.T) string {
	t.Helper()
	return modeCfg(t, "startup", 2, "  first_line_review: on\n  recipe: security\n")
}

// implementOnAListedPath is implement's step with the commit touching a path
// the list in force matches: what the post-implement seam classifies.
func implementOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Work on the credentials tree'")
	verifyTest(t, ctx, req)
	pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
	if err := os.WriteFile(pr, []byte("# Work on the credentials tree\n\nAdds internal/creds/state.go."), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 1}, nil
}

// fixOnAListedPath is a fix that adds a path the implement round did not touch,
// which is the case D1507's second seam exists for.
func fixOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

// TestRunEscalationRunsTheAdversarialStepThroughTheImplementSeam: the
// escalation fires in the post-implement window, and the plan the loop then
// walks carries level 3's step — the run has to spend the level it recorded,
// not merely record it (D1507, D1509).
func TestRunEscalationRunsTheAdversarialStepThroughTheImplementSeam(t *testing.T) {
	h := newHarnessFiles(t, securityLoopCfg(t), nil, securityLoopFiles)
	rec, err := h.run(t,
		implementOnAListedPath,
		review("ship", 0), // review_first
		review("ship", 0), // review: level 2 ships
		review("ship", 0), // adversarial: the escalation's own step
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the senior review", got)
	}
	if rec.Security == nil {
		t.Fatal("the run recorded no escalation, so the adversarial stage proves nothing about the seam")
	}
	if rec.Security.At != runstore.SecurityAtImplement || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the implement seam", rec.Security)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q): every enabled level shipped", rec.Outcome, rec.Reason)
	}
}

// TestRunEscalationKeepsAPOCRunFromStoppingOnTheCheapLoop: at POC the cheap
// loop is the level that decides readiness (TestLevelsPOCL1Decides), and a fix
// inside it can turn level 3 on (the fix seam above). From that round on the
// loop is no longer the decider — level 3 is — so a cheap round whose findings
// are all the owner's is handed to the level that decides instead of stopping
// the run (OD1, owner.go). Readiness is the same question: a loop that still
// thought it decided would stop here and the escalation's adversarial step,
// which is the whole of what it bought, would never run.
func TestRunEscalationKeepsAPOCRunFromStoppingOnTheCheapLoop(t *testing.T) {
	cfg := modeCfg(t, "poc", 2, "  first_line_rounds: 2\n  first_line_review: on\n  recipe: security\n")
	h := newHarnessFiles(t, cfg, nil, securityLoopFiles)
	rec, err := h.run(t,
		implement("feature"), // nothing on the list: the implement seam escalates nothing
		review("changes", 1), // review_first round 1: asks for changes
		fixOnAListedPath,     // the fix adds a path on the list: the fix seam turns level 3 on
		reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")), // review_first round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review_first", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the cheap round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if rec.Halt != nil {
		t.Fatalf("the run halted on %q: the cheap loop stopped deciding the moment the escalation turned level 3 on", rec.Halt.Reason)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: the adversarial round is the one that shipped", rec.Outcome, rec.Reason)
	}
}

// TestRunEscalationRunsTheAdversarialStepWhenTheFixRaisesTheMode: the fix that
// matches the list is the same fix that raises the rigor mode — roundRigor runs
// first, then the escalation, so the escalation resolves the levels the raise
// left (security.go, D22). At POC the mode's own row is what turns level 1 on
// (rigor.defaultLevels), so a raise out of POC takes the first_line step away:
// the plan the escalation rebuilds is the plan the loop is walking with a step
// the loop has already run removed from the front, and every step after it
// moves up one place. A loop that walks the plan by index therefore steps over
// the adversarial step it just recorded: the escalation is on the record, level
// 3 is on, and no round of it is ever asked — the run ends a draft, because the
// level whose verdict readiness needs never ran.
func TestRunEscalationRunsTheAdversarialStepWhenTheFixRaisesTheMode(t *testing.T) {
	base := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg := strings.Replace(base, "agent:\n",
		"rigor:\n  default: poc\n  areas:\n    - { name: app, match: \"feature.txt\", mode: critical }\nagent:\n", 1)
	if cfg == base {
		t.Fatal("the fixture has no agent: block to put rigor: before, so the repository would not opt in")
	}
	// Level 2 is on at POC through a layer of its own (the mode's row turns it
	// off), and the catalog's own `default` recipe supplies the first_line step
	// the mode's row turns on. Level 1 is on by that row alone and on no model
	// of the run's: the run names none, so nothing writes level 1 into its own
	// configuration (applyLevels) and the raise out of POC takes the step with
	// it.
	raised := strings.Replace(cfg, "  review_rounds: 2\n",
		"  review_rounds: 2\n  first_line_rounds: 1\n  levels: { l2: { model: senior-model } }\n", 1)
	if raised == cfg {
		t.Fatal("the fixture has no review_rounds line to put the levels beside")
	}
	h := newHarness(t, raised, nil)
	rec, err := h.run(t,
		implement("feature"),           // the file the area rule matches, and nothing on the list
		review("ship", 0),              // review_first: the step the mode's POC row inserted
		review("changes", 1),           // review round 1: asks for changes
		fixRaisingTheModeOnAListedPath, // the fix matches the area and the list: the mode and level 3 move at once
		review("ship", 0),              // review round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the fix's review round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: the adversarial round is the one that shipped", rec.Outcome, rec.Reason)
	}
}

// fixRaisingTheModeOnAListedPath is the fix that moves both the rigor mode and
// the security escalation at the same seam: it writes the file the area rule
// matches, so roundRigor raises the mode, and commits a path on the list, so
// the escalation beside it turns level 3 on.
func fixRaisingTheModeOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo more >> feature.txt && mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

// TestRunEscalationRunsTheAdversarialStepThroughTheFixSeam: the second seam is
// inside the loop that is already walking the plan, so the step the escalation
// adds has to reach that loop. A loop that captured the plan once would run the
// fix's escalation, record it, and never ask the level — the run would claim
// the escalation and not spend it (D1507).
func TestRunEscalationRunsTheAdversarialStepThroughTheFixSeam(t *testing.T) {
	h := newHarnessFiles(t, securityLoopCfg(t), nil, securityLoopFiles)
	rec, err := h.run(t,
		implement("feature"), // nothing on the list: the implement seam escalates nothing
		review("ship", 0),    // review_first
		review("changes", 1), // review round 1: asks for changes
		fixOnAListedPath,     // the fix adds a path the implement round did not touch
		review("ship", 0),    // review round 2
		review("ship", 0),    // adversarial: the escalation's own step
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the fix's review round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if n := countStage(rec, "adversarial"); n != 1 {
		t.Fatalf("adversarial stages = %d, want 1", n)
	}
}
