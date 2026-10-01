package runner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// setMain commits yaml as fugaro.yaml on the remote's default branch.
func setMain(t *testing.T, h *harness, yaml string) {
	t.Helper()
	commitToRemote(t, h, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	})
}

// ceilingOf builds the job-environment ceiling the way init --repo writes it.
func ceilingOf(t *testing.T, kv ...string) runner.Spend {
	t.Helper()
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tokenHalt(t *testing.T, rec *runstore.Record, err error, detail string) {
	t.Helper()
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap || rec.Halt.Detail != detail {
		t.Fatalf("want a token_cap halt %q; rec = %+v, halt = %+v, err = %v", detail, rec, rec.Halt, err)
	}
}

// big is a stage that reports n input tokens.
func big(s step, n int64) step { return withUsage(s, agent.Usage{Input: n}, nil) }

func TestPolicyReadFromDefaultBranchNotRef(t *testing.T) {
	// The default branch says $0.10; the run's branch asks for $1000. The
	// owner's ceiling is $50. The run uses 0.10.
	g := newGW(t, gwConfig(t, "")+"budget:\n  per_run_usd: 0.10\n", "enforce", "50", capScript(3)...)
	g.deps.Project = "aurora"
	pushBranch(t, g.harness, "feature", gwConfig(t, "")+"budget:\n  per_run_usd: 1000\n")
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.10") {
		t.Fatalf("statuses %v, rec = %+v, halt = %+v", st, rec, rec.Halt)
	}
}

func TestPolicyCeilingClampsBothFiles(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  per_run_usd: 1000\n", "enforce", "0.10", capScript(3)...)
	g.deps.Project = "aurora"
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.10") {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
}

func TestPolicyBranchZeroTokenCapIgnored(t *testing.T) {
	// The branch's max_run_tokens: 0 (the M9a hole) can't lift the default branch's cap.
	h := projectHarness(t, "aurora")
	setMain(t, h, tokenCfg(t, "100"))
	pushBranch(t, h, "feature", tokenCfg(t, "0"))
	setRef(t, h, task.Spec{Ref: "feature"})
	rec, err := h.run(t, big(implement("feature"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 100")
}

func TestPolicyDefaultBranchTokenCapTightensBranch(t *testing.T) {
	h := projectHarness(t, "aurora")
	setMain(t, h, tokenCfg(t, "100"))
	pushBranch(t, h, "feature", tokenCfg(t, "1000000"))
	setRef(t, h, task.Spec{Ref: "feature"})
	rec, err := h.run(t, big(implement("feature"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 100")
}

func TestPolicyCeilingTokenCapWins(t *testing.T) {
	h := projectHarness(t, "aurora")
	h.deps.Spend = ceilingOf(t, runner.MaxRunTokensEnv, "50")
	setMain(t, h, tokenCfg(t, "1000000"))
	rec, err := h.run(t, big(implement("feature"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 50")
}

func TestPolicyLocalRunSkipsDefaultBranch(t *testing.T) {
	// No project: the layers are the ceiling and the branch.
	h := newHarness(t, tokenCfg(t, "100"), nil)
	var logs strings.Builder
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, big(implement("feature"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 100")
	if !strings.Contains(logs.String(), "not reading the budget policy from the default branch") {
		t.Errorf("no log line:\n%s", logs.String())
	}
}

func TestPolicyFollowUpReadsDefaultBranch(t *testing.T) {
	// The base is a release branch with a looser file; the default
	// branch's cap applies to the follow-up as well.
	h := followUpHarness(t, "", func(h *harness) { h.deps.Project = "aurora" })
	setMain(t, h.harness, tokenCfg(t, "100"))
	release := strings.Replace(tokenCfg(t, "1000000"), "base_branch: main", "base_branch: release", 1)
	pushBranch(t, h.harness, "release", release)
	h.followUp(t, followID, runID, "Tidy up.")
	spec := &task.Spec{Version: 1, RunID: followID, Repo: "acme/app", Ref: "release", Workflow: h.first.Workflow,
		Task: "Tidy up.", Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, big(implement("tidy"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 100")
}

func TestPolicyAgentEditMidRunIgnored(t *testing.T) {
	// The agent raises the cap in fugaro.yaml during the first stage: the
	// second stage still stops at the cap computed at bootstrap.
	h := newHarness(t, tokenCfg(t, "100"), nil)
	edit := then(big(implement("feature"), 10), func(t *testing.T, req agent.Request) {
		testutil.WriteFiles(t, req.Dir, map[string]string{"fugaro.yaml": tokenCfg(t, "1000000")})
	})
	rec, err := h.run(t, edit, big(review("ship", 0), 500))
	tokenHalt(t, rec, err, "run used 510 tokens of 100")
}

func TestPolicyAgentEditCannotRemoveCeilingCap(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  per_run_usd: 5\n", "enforce", "0.10", capScript(3)...)
	var st []int
	edit := then(implement("feature"), func(t *testing.T, req agent.Request) {
		testutil.WriteFiles(t, req.Dir, map[string]string{"fugaro.yaml": gwConfig(t, "")})
	})
	rec, err := g.run(t, calling(&st, edit, call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
}

func TestDefaultBranchPolicyInvalidFailsClosed(t *testing.T) {
	for name, c := range map[string]struct{ block, key string }{
		"bad mode":         {"budget:\n  mode: strict\n", "budget.mode"},
		"negative cap":     {"budget:\n  per_run_usd: -1\n", "per_run_usd"},
		"alias":            {"budget:\n  allowed_models: [sonnet]\n", "allowed_models"},
		"empty list":       {"budget:\n  allowed_models: []\n", "allowed_models"},
		"negative tokens":  {"agent: {max_run_tokens: -5}\n", "max_run_tokens"},
		"per day":          {"budget:\n  per_day_usd: 5\n", "per_day_usd"},
		"not a mapping":    {"budget: 5\n", "cannot unmarshal"},
		"duplicate budget": {"budget: {mode: off}\nbudget: {mode: off}\n", "budget"},
	} {
		t.Run(name, func(t *testing.T) {
			h := projectHarness(t, "aurora")
			yaml := fixtureYAML(t) + c.block
			if strings.HasPrefix(c.block, "agent:") {
				yaml = strings.Replace(fixtureYAML(t), "  review_rounds: 2\n", "  review_rounds: 2\n  max_run_tokens: -5\n", 1)
			}
			setMain(t, h, yaml)
			pushBranch(t, h, "feature", fixtureYAML(t))
			setRef(t, h, task.Spec{Ref: "feature"})
			refused(t, h, "fugaro.yaml", "default branch", "main", c.key)
		})
	}
}

func TestDefaultBranchWithoutFileKeepsProjectCheckError(t *testing.T) {
	h := projectHarness(t, "aurora")
	pushBranch(t, h, "feature", fixtureYAML(t))
	commitToRemote(t, h, func(dir string) {
		if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
			t.Fatal(err)
		}
	})
	setRef(t, h, task.Spec{Ref: "feature"})
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.HasPrefix(rec.Reason, "bootstrap: reading fugaro.yaml at origin/main: ") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestDefaultBranchPolicyUnknownKeyElsewhereOK(t *testing.T) {
	h := projectHarness(t, "aurora")
	setMain(t, h, tokenCfg(t, "1000000")+"surprise: true\nmodel_prices: nonsense\n")
	pushBranch(t, h, "feature", fixtureYAML(t))
	setRef(t, h, task.Spec{Ref: "feature"})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestModelOutsideAllowListInfraError(t *testing.T) {
	const both = sonnet + "," + haiku
	t.Run("ceiling", func(t *testing.T) {
		h := newHarness(t, gwConfig(t, ""), nil)
		h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, haiku)
		refused(t, h, "agent.models.coder", sonnet, "agent.models.reviewer", "allowed_models from ceiling")
	})
	t.Run("default branch narrows the ceiling", func(t *testing.T) {
		h := projectHarness(t, "aurora")
		setMain(t, h, gwConfig(t, "")+"budget:\n  allowed_models: ["+haiku+"]\n")
		h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, both)
		pushBranch(t, h, "feature", gwConfig(t, ""))
		setRef(t, h, task.Spec{Ref: "feature"})
		refused(t, h, "agent.models.coder", sonnet, "allowed_models from default-branch")
	})
	t.Run("disjoint lists deny every model", func(t *testing.T) {
		h := projectHarness(t, "aurora")
		setMain(t, h, gwConfig(t, "")+"budget:\n  allowed_models: ["+opus+"]\n")
		h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, both)
		pushBranch(t, h, "feature", gwConfig(t, ""))
		setRef(t, h, task.Spec{Ref: "feature"})
		refused(t, h, "agent.models.coder", "agent.models.reviewer", "agent.models.background", "none")
	})
	t.Run("a branch cannot add a model", func(t *testing.T) {
		h := newHarness(t, gwConfig(t, "")+"budget:\n  allowed_models: ["+both+"]\n", nil)
		h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, haiku)
		refused(t, h, "agent.models.coder", sonnet)
	})
	t.Run("a role naming no model", func(t *testing.T) {
		cfg := strings.Replace(gwConfig(t, ""), "  model: "+sonnet+"\n", "", 1)
		cfg = strings.Replace(cfg, "models: { coder: "+sonnet+", reviewer: "+sonnet+", background: "+haiku+" }", "models: { coder: "+sonnet+", background: "+haiku+" }", 1)
		h := newHarness(t, cfg, nil)
		h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, both)
		refused(t, h, "agent.models.reviewer", "names no model")
	})
}

func TestAllowedModelsAllInListRuns(t *testing.T) {
	h := projectHarness(t, "aurora")
	h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, sonnet+","+haiku)
	cfg := gwConfig(t, "")
	setMain(t, h, cfg+"budget:\n  allowed_models: ["+sonnet+", "+haiku+", "+opus+"]\n")
	pushBranch(t, h, "feature", cfg)
	setRef(t, h, task.Spec{Ref: "feature"})
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestPolicyVertexEnforceRefused(t *testing.T) {
	// A committed enforce on a Vertex repository: the ceiling is off.
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg+"budget:\n  mode: enforce\n  per_run_usd: 5\n", "off", "")
	g.deps.Project = "aurora"
	g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "Vertex budgets are not supported yet") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestPolicyCommittedEnforceStartsGateway(t *testing.T) {
	// The ceiling is off; the default branch escalates to enforce.
	g := newGW(t, gwConfig(t, "")+"budget:\n  mode: enforce\n  per_run_usd: 0.10\n", "off", "", capScript(3)...)
	g.deps.Project = "aurora"
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || len(st) != 3 || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
}

func TestPolicyCommittedEnforceWithoutCapHalts(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  mode: enforce\n", "off", "")
	g.deps.Project = "aurora"
	rec, err := g.run(t)
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
