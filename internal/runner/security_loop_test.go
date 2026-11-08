package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
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
