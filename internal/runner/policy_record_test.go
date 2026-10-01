package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reportOf reads the report the run stored.
func reportOf(t *testing.T, h *harness) string {
	t.Helper()
	b, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// storedPolicy reads the policy object from the stored result.json.
func storedPolicy(t *testing.T, h *harness) *runstore.PolicyRecord {
	t.Helper()
	rec, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rec.Policy
}

func warnCount(logs, key string) int {
	n := 0
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "level=WARN") && strings.Contains(l, "key="+key+" ") {
			n++
		}
	}
	return n
}

func ignoredKeys(p *runstore.PolicyRecord) map[string]int {
	m := map[string]int{}
	for _, ig := range p.Ignored {
		m[ig.Key]++
	}
	return m
}

func TestIgnoredValuesRecordedAndReported(t *testing.T) {
	const both = sonnet + "," + haiku
	main := gwConfig(t, "  max_run_tokens: 100000\n") + "budget:\n  per_run_usd: 2\n"
	branch := gwConfig(t, "  max_run_tokens: 900000\n") +
		"budget:\n  per_run_usd: 500\n  allowed_models: [" + sonnet + ", " + haiku + ", " + opus + "]\n"
	g := newGW(t, main, "enforce", "5")
	g.deps.Project = "aurora"
	g.deps.Spend = ceilingOf(t, runner.BudgetModeEnv, "enforce", runner.MaxRunUSDEnv, "5", runner.AllowedModelsEnv, both)
	pushBranch(t, g.harness, "feature", branch)
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	p := storedPolicy(t, g.harness)
	if p == nil {
		t.Fatal("no policy in result.json")
	}
	want := []runstore.PolicyIgnored{
		{Key: "per_run_usd", Value: "500", Effective: "2", Source: "default-branch", From: "branch"},
		{Key: "max_run_tokens", Value: "900000", Effective: "100000", Source: "default-branch", From: "branch"},
		{Key: "allowed_models", Value: sonnet + "," + haiku + "," + opus, Effective: both, Source: "ceiling", From: "branch"},
	}
	if len(p.Ignored) != len(want) {
		t.Fatalf("ignored = %+v", p.Ignored)
	}
	for i := range want {
		if p.Ignored[i] != want[i] {
			t.Errorf("ignored[%d] = %+v, want %+v", i, p.Ignored[i], want[i])
		}
	}
	e := p.Effective
	if e.PerRunUSD != 2 || e.Mode != "enforce" || e.MaxRunTokens != 100000 || strings.Join(e.AllowedModels, ",") != both {
		t.Errorf("effective = %+v", e)
	}
	if p.Sources["per_run_usd"] != "default-branch" || p.Sources["mode"] != "ceiling" || p.Sources["allowed_models"] != "ceiling" {
		t.Errorf("sources = %v", p.Sources)
	}
	line := "**Policy:** 3 values from fugaro.yaml on this branch were looser than the project's limits and were ignored (" +
		"budget.per_run_usd 500 -> 2; agent.max_run_tokens 900000 -> 100000; budget.allowed_models " + sonnet + "," + haiku + "," + opus + " -> " + both + ")\n"
	if report := reportOf(t, g.harness); strings.Count(report, "**Policy:**") != 1 || !strings.Contains(report, line) {
		t.Errorf("report:\n%s\nwant the line %q", report, line)
	}
	logs := g.logs.String()
	for _, k := range []string{"budget.per_run_usd", "agent.max_run_tokens", "budget.allowed_models"} {
		if n := warnCount(logs, k); n != 1 {
			t.Errorf("%d warnings for %s, want 1:\n%s", n, k, logs)
		}
	}
	if n := strings.Count(logs, "msg=policy "); n != 1 {
		t.Errorf("the policy was logged %d times, want once:\n%s", n, logs)
	}
}

// A key two layers both loosen is warned about and reported once, but
// result.json keeps each attempt.
func TestIgnoredOncePerKeyAcrossLayers(t *testing.T) {
	main := gwConfig(t, "") + "budget:\n  per_run_usd: 20\n"
	g := newGW(t, main, "enforce", "5")
	g.deps.Project = "aurora"
	pushBranch(t, g.harness, "feature", gwConfig(t, "")+"budget:\n  per_run_usd: 500\n")
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	if rec, err := g.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	p := storedPolicy(t, g.harness)
	if p == nil || len(p.Ignored) != 2 || p.Ignored[0].From != "default-branch" || p.Ignored[1].From != "branch" || p.Effective.PerRunUSD != 5 {
		t.Fatalf("policy = %+v", p)
	}
	if n := warnCount(g.logs.String(), "budget.per_run_usd"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, g.logs.String())
	}
	report := reportOf(t, g.harness)
	want := "**Policy:** 1 value from fugaro.yaml was looser than the limits set above them and was ignored (budget.per_run_usd 20 -> 5)\n"
	if strings.Count(report, "**Policy:**") != 1 || !strings.Contains(report, want) {
		t.Errorf("report:\n%s\nwant %q", report, want)
	}
}

// A policy that only tightens is recorded but nothing is said about it.
func TestTighteningPolicyRecordedNotReported(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  per_run_usd: 2\n", "enforce", "5")
	g.deps.Project = "aurora"
	if rec, err := g.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	p := storedPolicy(t, g.harness)
	if p == nil || len(p.Ignored) != 0 || p.Effective.PerRunUSD != 2 || p.Sources["per_run_usd"] != "default-branch" {
		t.Fatalf("policy = %+v", p)
	}
	if report := reportOf(t, g.harness); strings.Contains(report, "Policy:") {
		t.Errorf("report mentions policy:\n%s", report)
	}
	if strings.Contains(g.logs.String(), "looser value") {
		t.Errorf("warnings:\n%s", g.logs.String())
	}
}

// A branch cut before the team tightened its cap: the run goes on, is
// clamped to today's cap and is told so.
func TestStaleBranchLooserThanDefault(t *testing.T) {
	g := newGW(t, gwConfig(t, "")+"budget:\n  per_run_usd: 0.10\n", "enforce", "50", capScript(3)...)
	g.deps.Project = "aurora"
	pushBranch(t, g.harness, "feature", gwConfig(t, "")+"budget:\n  per_run_usd: 5\n")
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || len(st) != 3 || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("statuses %v, rec = %+v, err = %v", st, rec, err)
	}
	p := storedPolicy(t, g.harness)
	if p == nil || len(p.Ignored) != 1 || p.Ignored[0] != (runstore.PolicyIgnored{
		Key: "per_run_usd", Value: "5", Effective: "0.1", Source: "default-branch", From: "branch"}) {
		t.Fatalf("policy = %+v", p)
	}
	if report := reportOf(t, g.harness); !strings.Contains(report, "(budget.per_run_usd 5 -> 0.1)") {
		t.Errorf("report does not tell the author:\n%s", report)
	}
	if n := warnCount(g.logs.String(), "budget.per_run_usd"); n != 1 {
		t.Errorf("%d warnings:\n%s", n, g.logs.String())
	}
}

// bigOutput is a reply that costs $2 on sonnet.
func bigOutput() anthropicfake.Reply {
	return anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 200000})
}

// The owner's ceiling is $5; the branch asks for $500. A hermetic run
// against a fake upstream: the gateway's cap is $5, so the call that would
// cross $5 is refused and the run halts with run_cap.
func TestBranchRaisesCapIsClamped(t *testing.T) {
	cfg := strings.ReplaceAll(gwConfig(t, ""), "4096", "128000")
	g := newGW(t, cfg, "enforce", "5", bigOutput(), bigOutput(), bigOutput())
	g.deps.Project = "aurora"
	pushBranch(t, g.harness, "feature", cfg+"budget:\n  per_run_usd: 500\n")
	setRef(t, g.harness, task.Spec{Ref: "feature"})
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 128000}, call{sonnet, 128000}, call{sonnet, 128000}))
	if err != nil {
		t.Fatal(err)
	}
	// $2 spent, then $4; the third call reserves $1.28 more than $5 allows.
	// At $500 it would have been served.
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 || g.fake.Count() != 2 {
		t.Fatalf("statuses %v, upstream calls %d", st, g.fake.Count())
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap ||
		!strings.Contains(rec.Halt.Detail, "$5.00") || strings.Contains(rec.Halt.Detail, "500") {
		t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
	}
	p := storedPolicy(t, g.harness)
	if p == nil || p.Effective.PerRunUSD != 5 || p.Sources["per_run_usd"] != "ceiling" || len(p.Ignored) != 1 ||
		p.Ignored[0] != (runstore.PolicyIgnored{Key: "per_run_usd", Value: "500", Effective: "5", Source: "ceiling", From: "branch"}) {
		t.Fatalf("policy = %+v", p)
	}
}

// An empty allow-list (here, disjoint lists) forbids every model and is
// written to result.json as present, never omitted.
func TestEmptyAllowListRecordedAsPresent(t *testing.T) {
	h := projectHarness(t, "aurora")
	setMain(t, h, gwConfig(t, "")+"budget:\n  allowed_models: ["+opus+"]\n")
	h.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, sonnet+","+haiku)
	pushBranch(t, h, "feature", gwConfig(t, ""))
	setRef(t, h, task.Spec{Ref: "feature"})
	refused(t, h, "agent.models.coder", "none")
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"result.json")
	if err != nil || !strings.Contains(strings.ReplaceAll(string(raw), " ", ""), `"allowed_models":[]`) {
		t.Fatalf("result.json: %s, %v", raw, err)
	}
	p := storedPolicy(t, h)
	if p == nil || p.Effective.AllowedModels == nil || len(p.Effective.AllowedModels) != 0 || ignoredKeys(p)["allowed_models"] != 1 {
		t.Fatalf("policy = %+v", p)
	}
}

// oauth stays budget-off for dollars whatever the mode: the ceiling's
// enforce (budgetEnv still writes FUGARO_BUDGET_MODE for an oauth workflow
// when the project config sets a mode) and a committed enforce start no
// gateway and take no dollar cap. The token cap still applies.
func TestOAuthStaysBudgetOff(t *testing.T) {
	oauthCfg := func(extra string) string {
		return strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "auth: api-key", "auth: oauth", 1) + extra
	}
	setup := func(t *testing.T, cfg string, env ...string) *gw {
		g := newGW(t, cfg, "off", "")
		g.deps.Project = "aurora"
		g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
		g.deps.Spend = ceilingOf(t, env...)
		return g
	}
	noGateway := func(t *testing.T, g *gw) {
		t.Helper()
		if g.fake.Count() != 0 {
			t.Fatal("an oauth run reached the gateway's upstream")
		}
		for _, c := range g.agent.calls {
			if envValue(c.Env, "ANTHROPIC_BASE_URL") != "" {
				t.Fatal("an oauth run was pointed at a gateway")
			}
		}
	}
	// $1.50 of Claude Code cost against a $0.01 cap: no dollar cap applies.
	for name, tc := range map[string]struct {
		cfg string
		env []string
	}{
		"ceiling enforce":               {oauthCfg(""), []string{runner.BudgetModeEnv, "enforce", runner.MaxRunUSDEnv, "0.01"}},
		"ceiling enforce without a cap": {oauthCfg(""), []string{runner.BudgetModeEnv, "enforce"}},
		"committed enforce":             {oauthCfg("budget:\n  mode: enforce\n  per_run_usd: 0.01\n"), nil},
	} {
		t.Run(name, func(t *testing.T) {
			g := setup(t, tc.cfg, tc.env...)
			rec, err := g.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			noGateway(t, g)
			if rec.Cost == nil || rec.Cost.ModelSource != "claude-code" {
				t.Fatalf("cost = %+v", rec.Cost)
			}
		})
	}
	t.Run("the token cap applies", func(t *testing.T) {
		g := setup(t, oauthCfg(""), runner.BudgetModeEnv, "enforce", runner.MaxRunUSDEnv, "0.01", runner.MaxRunTokensEnv, "100")
		rec, err := g.run(t, big(implement("feature"), 500))
		tokenHalt(t, rec, err, "run used 500 tokens of 100")
		noGateway(t, g)
	})
	t.Run("the committed token cap applies", func(t *testing.T) {
		g := setup(t, oauthCfg("budget:\n  mode: enforce\n"))
		g.deps.Spend = ceilingOf(t, runner.BudgetModeEnv, "enforce")
		cfg := strings.Replace(oauthCfg(""), "  review_rounds: 2\n", "  review_rounds: 2\n  max_run_tokens: 100\n", 1)
		setMain(t, g.harness, cfg)
		rec, err := g.run(t, big(implement("feature"), 500))
		tokenHalt(t, rec, err, "run used 500 tokens of 100")
		noGateway(t, g)
	})
}
