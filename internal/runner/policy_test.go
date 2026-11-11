package runner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
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

// ceilingOf builds the job-environment ceiling the way fugaro update writes it.
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

// critRigor pins a run to Critical. The tests that use it are about which
// layer's value wins, and Critical contributes no per-run default, so the
// deployed cap is the ceiling exactly as it was before a mode had one
// (docs/rigor-modes D16, plan T6.1); TestPolicyRigorModeScalesTheCeiling is
// where the scaling itself is pinned.
const critRigor = "rigor:\n  default: critical\n"

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
	g := newGW(t, gwConfig(t, critRigor)+"budget:\n  per_run_usd: 1000\n", "enforce", "0.10", capScript(3)...)
	g.deps.Project = "aurora"
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.10") {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
}

// TestRepositoryKeyCannotWidenTheList is P2's runner half. fugaro.yaml sits
// inside the checkout the run's own agent writes, so it is not a file the
// binary allow rule reads: internal/config decodes strictly and the key does
// not exist there, so a repository that writes one does not load at all rather
// than load and be ignored — and the layer that file becomes (FileLayer)
// carries no binaries field to set. What the check reads is the run's resolved
// policy, whose list is the owner's node's, and the merge only ever intersects
// a list, so nothing under the ceiling can add a path to it.
func TestRepositoryKeyCannotWidenTheList(t *testing.T) {
	if _, probs := config.Parse([]byte(fixtureYAML(t) + "binaries:\n  allow: [\"**\"]\n")); len(probs) == 0 {
		t.Fatal("a fugaro.yaml naming binaries loaded: the key is the policy node's alone (D986)")
	} else if !strings.Contains(probs[0].Message, "binaries") {
		t.Fatalf("the file was refused for %q, not for the binaries key", probs[0].Message)
	}
	cfg, probs := config.Parse([]byte(fixtureYAML(t)))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if file := runner.FileLayer(cfg); file.BinariesAllow != nil || file.BinariesMaxBytes != 0 {
		t.Fatalf("FileLayer carries binaries: %+v", file)
	}
	ceiling := policy.Layer{BinariesAllow: []string{"brand/*.png", "website/public/**"}, BinariesMaxBytes: 1048576}
	e := policy.Merge(ceiling, runner.FileLayer(cfg))
	if want := []string{"brand/*.png", "website/public/**"}; !slices.Equal(e.BinariesAllow, want) || e.BinariesMaxBytes != 1048576 {
		t.Fatalf("BinariesAllow = %v, cap = %d, want %v and 1048576: the repository's own file moved the rule",
			e.BinariesAllow, e.BinariesMaxBytes, want)
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
		"negative per day": {"budget:\n  per_day_usd: -5\n", "per_day_usd"},
		"not a mapping":    {"budget: 5\n", "budget"},
		"budget typo":      {"budget:\n  per_run_usdd: 2\n", "per_run_usdd"},
		"allowed typo":     {"budget:\n  allowed_model: [" + sonnet + "]\n", "allowed_model"},
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

func TestDefaultBranchAgentKeyTypoFailsClosed(t *testing.T) {
	for _, kv := range []string{"max_run_token: 5", "max_output_token: { coder: 5 }"} {
		h := projectHarness(t, "aurora")
		setMain(t, h, strings.Replace(fixtureYAML(t), "  review_rounds: 2\n", "  review_rounds: 2\n  "+kv+"\n", 1))
		pushBranch(t, h, "feature", fixtureYAML(t))
		setRef(t, h, task.Spec{Ref: "feature"})
		refused(t, h, "fugaro.yaml", "default branch", "main", strings.SplitN(kv, ":", 2)[0])
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
	// A published policy *is* the ceiling, so the refusal names the node once:
	// the old wording printed the policy's own list as "the project's
	// allow-list" beside "allowed_models from policy", the same node under two
	// labels (the review's low finding on internal/config/pins.go).
	t.Run("the policy's own list is not repeated as the project's", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "off", "",
			`{"version":4,"budget":{"mode":"off","allowed_models":["`+haiku+`"]}}`, capScript(3)...)
		b.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, both)
		withBucket(b.harness)
		rec, err := b.run(t) // no agent step may run
		if err == nil || rec.Status != runstore.StatusInfraError {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if !strings.Contains(rec.Reason, "allowed_models from policy") || !strings.Contains(rec.Reason, "agent.models.coder") {
			t.Fatalf("reason %q: want the policy's list named as the policy's", rec.Reason)
		}
		// The environment's list is a different list, and the policy replaced it:
		// neither label may appear beside the node's own.
		for _, w := range []string{"the project's allow-list", "allowed_models from ceiling"} {
			if strings.Contains(rec.Reason, w) {
				t.Errorf("reason %q names the policy's own list twice (%q)", rec.Reason, w)
			}
		}
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

// The owner's cap holds with the ceiling's mode off: a repository that
// escalates to enforce with a looser cap runs at the owner's.
func TestPolicyOffCeilingCapHolds(t *testing.T) {
	g := newGW(t, gwConfig(t, critRigor)+"budget:\n  mode: enforce\n  per_run_usd: 20\n", "off", "0.10", capScript(3)...)
	g.deps.Project = "aurora"
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || len(st) != 3 || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.10") {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
}

// ... and the owner's price overrides, not the built-in table, charge it.
func TestPolicyOffCeilingPricesHold(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  mode: observe\n", "off", "", capScript(1)...)
	g.deps.Project = "aurora"
	g.deps.Spend = ceilingOf(t, runner.ModelPricesEnv, `{"`+sonnet+`":{"input_per_m":20,"output_per_m":100}}`)
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || len(st) != 1 || st[0] != 200 || !near(rec.CostUSD, 0.40) {
		t.Fatalf("statuses %v, cost %v, err = %v", st, rec.CostUSD, err)
	}
}

// agent.max_output_tokens: the default branch's limit is the most a branch's
// own file can have.
func TestPolicyOutputLimitMergedWithDefaultBranch(t *testing.T) {
	cfg := gwConfig(t, "")
	main := strings.Replace(cfg, "max_output_tokens: { coder: 4096, reviewer: 4096 }", "max_output_tokens: { coder: 1000, reviewer: 4096 }", 1)
	if main == cfg {
		t.Fatal("no output limits in the fixture")
	}
	g := newGW(t, main, "observe", "", capScript(2)...)
	g.deps.Project = "aurora"
	pushBranch(t, g.harness, "feature", strings.Replace(cfg, "coder: 4096", "coder: 8192", 1))
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	var st []int
	if _, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 1000}), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0] == 200 || st[1] != 200 {
		t.Fatalf("statuses %v: want the 4000-token call refused and the 1000-token one served", st)
	}
}

// ref is a pointer to v: the four chain limits a task carries are pointers,
// and a nil one is a dimension the launch left unbounded (task.Overrides).
func ref[T any](v T) *T { return &v }

// limitTask writes the chain limits a launch asked for into the run's stored
// task, which is what bootstrap reads: what the four --max-* flags leave
// there, and what a hand-written task or an older CLI's may leave there
// instead of a remaining (D1041, D1044).
func limitTask(t *testing.T, h *harness, o task.Overrides) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature", Overrides: o}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

// TestPolicyMergesTheChainRemaining: the runner resolves the chain again at
// bootstrap and merges what is left of it as the last layer (D1044, D1047).
// The task carries the launch's own token limit of 1000 — the figure a
// hand-written task, or one an older CLI wrote, holds instead of a remaining
// — and the run's ledger says it already holds 400 of them, so the figure the
// resolved policy carries is 600, it comes from the chain, and a run that
// then spends past it halts there. A merge that does not consult the chain
// would resolve the task's 1000, which is the hole D1052 closes.
func TestPolicyMergesTheChainRemaining(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "observe", "")
	limitTask(t, b.harness, task.Overrides{MaxTokens: ref(int64(1000))})
	b.db.Set(budget.PathRun(repoSlug, runID), budget.RunLedger{Tokens: 400})
	rec, err := b.run(t, big(implement("feature"), 700), review("ship", 0))
	if rec.Policy == nil || rec.Policy.Sources[policy.KeyMaxRunTokens] != policy.SourceChain {
		t.Fatalf("the run's token cap is not the chain's remaining: policy = %+v", rec.Policy)
	}
	if got := rec.Policy.Effective.MaxRunTokens; got != 600 {
		t.Fatalf("the resolved policy carries %d tokens, want the chain's 600 (its 1000 less the 400 held)", got)
	}
	tokenHalt(t, rec, err, "run used 700 tokens of 600")
}

// TestTokenCapIsTheRemaining: the token cap the run is held to is what the
// chain has left, not what any layer above the merge allows (D1047, D1059).
// The run's own fugaro.yaml allows 2000 tokens and the task asks for 1000,
// while the chain holds 400 of them already — so the run halts at 600, a
// figure below both. A run held to either of the other two would not halt at
// all: this is the comparison halt.go's capReached makes, against the figure
// the resolved policy yields.
func TestTokenCapIsTheRemaining(t *testing.T) {
	b := newBK(t, gwConfig(t, "  max_run_tokens: 2000\n"), "observe", "")
	limitTask(t, b.harness, task.Overrides{MaxTokens: ref(int64(1000))})
	b.db.Set(budget.PathRun(repoSlug, runID), budget.RunLedger{Tokens: 400})
	rec, err := b.run(t, big(implement("feature"), 700), review("ship", 0))
	tokenHalt(t, rec, err, "run used 700 tokens of 600")
}

// TestSpendCapIsTheRemaining: the cap the gateway holds is the chain's
// remaining and not the launch's limit (D1059, D1050). The task asks for
// $0.10 and the run's ledger holds $0.04 of it, so the cap the gateway
// refuses a call against is $0.06 — the gateway itself is unchanged, and the
// figure it names in the refusal is the merge's.
func TestSpendCapIsTheRemaining(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	limitTask(t, b.harness, task.Overrides{MaxUSD: ref(0.10)})
	b.db.Set(budget.PathRun(repoSlug, runID), budget.RunLedger{Reserved: 40_000})
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || len(st) != 3 || st[2] != 403 {
		t.Fatalf("statuses %v, err = %v", st, err)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.06") {
		t.Fatalf("the gateway's refusal does not name the remaining: %+v", rec.Halt)
	}
}

// TestHaltedOpenLedgerChargesTheHeldAmount: a restart charges exactly what
// the run still holds — reserved less released (D1050 case 3, D1046). The
// task asks for $0.10 and the ledger holds $0.03: $0.05 reserved with $0.02
// released, the shape a run killed between its halt and its release leaves —
// so the remaining the run carries is $0.07.
func TestHaltedOpenLedgerChargesTheHeldAmount(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	limitTask(t, b.harness, task.Overrides{MaxUSD: ref(0.10)})
	b.db.Set(budget.PathRun(repoSlug, runID), budget.RunLedger{Reserved: 50_000, Released: 20_000})
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || len(st) != 3 || st[2] != 403 {
		t.Fatalf("statuses %v, err = %v", st, err)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.07") {
		t.Fatalf("the cap is not what the ledger still holds: %+v", rec.Halt)
	}
}

// TestChainZeroRemainingRefusesAtLaunch: a chain with nothing left is refused
// at bootstrap (D1050 case 1) — an outcome and not a failure, like every
// other halt, and before anything is locked, pushed or opened. The task asks
// for $0.10 and the run's own ledger already holds all of it.
func TestChainZeroRemainingRefusesAtLaunch(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(1)...)
	limitTask(t, b.harness, task.Overrides{MaxUSD: ref(0.10)})
	b.db.Set(budget.PathRun(repoSlug, runID), budget.RunLedger{Reserved: 100_000})
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
	if !strings.Contains(rec.Halt.Detail, runID) || !strings.Contains(rec.Halt.Detail, "dollar") {
		t.Fatalf("the halt does not name the chain and the dimension: %q", rec.Halt.Detail)
	}
	if len(b.agent.calls) != 0 || b.fake.Count() != 0 {
		t.Fatal("the agent or the gateway ran")
	}
}
