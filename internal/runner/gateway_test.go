package runner_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	sonnet = "claude-sonnet-5-5"
	haiku  = "claude-haiku-4-5"
	opus   = "claude-opus-5"
	// plantedKey stands in for the real API key: it may reach the upstream
	// and nothing else.
	plantedKey = "sk-ant-TESTKEY-0123456789abcdefghijklmnop"
)

// gwConfig is the fixture's configuration with priced models: the coder and
// the reviewer on sonnet, the background model haiku, and an output limit
// per call on each role.
func gwConfig(t *testing.T, extra string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	out := strings.Replace(cfg, "  review_rounds: 2\n", `  review_rounds: 2
  model: `+sonnet+`
  models: { coder: `+sonnet+`, reviewer: `+sonnet+`, background: `+haiku+` }
  max_output_tokens: { coder: 4096, reviewer: 4096 }
`+extra, 1)
	if out == cfg {
		t.Fatal("fixture has no review_rounds line")
	}
	return out
}

// lockedBuf is a log sink the gateway's goroutines may write to.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// gw is a harness whose run goes through the gateway, to a fake upstream.
type gw struct {
	*harness
	fake *anthropicfake.Fake
	logs *lockedBuf
	// compat is the provider's fake upstream, set by providerGW.
	compat *anthropicfake.Fake
}

// newGW starts a fake upstream answering with script and a run in budget
// mode (observe or enforce, with capUSD when not empty) against it.
func newGW(t *testing.T, cfg, mode, capUSD string, script ...anthropicfake.Reply) *gw {
	t.Helper()
	return newGWFiles(t, cfg, mode, capUSD, nil, script...)
}

// newGWFiles is newGW with extra files committed to the repository.
func newGWFiles(t *testing.T, cfg, mode, capUSD string, files map[string]string, script ...anthropicfake.Reply) *gw {
	t.Helper()
	h := newHarnessFiles(t, cfg, nil, files)
	fake, up := anthropicfake.New(t, script...)
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	env := map[string]string{runner.BudgetModeEnv: mode, runner.MaxRunUSDEnv: capUSD}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend, h.deps.GatewayUpstream = spend, up.URL
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	return &gw{harness: h, fake: fake, logs: logs}
}

// gwEnv reads where the agent was pointed.
func gwEnv(req agent.Request) (base, key string) {
	return envValue(req.Env, "ANTHROPIC_BASE_URL"), envValue(req.Env, "ANTHROPIC_API_KEY")
}

// post sends one Messages request as Claude Code would, and returns the
// status and the whole body.
func post(t *testing.T, req agent.Request, model string, maxTokens int64) (int, string) {
	t.Helper()
	base, key := gwEnv(req)
	body := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"messages":[{"role":"user","content":"hello"}]}`, model, maxTokens)
	return postBody(t, base+"/v1/messages", key, body)
}

func postBody(t *testing.T, url, key, body string) (int, string) {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("anthropic-version", "2023-06-01")
	if key != "" {
		r.Header.Set("x-api-key", key)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// call is one model call of a scripted agent.
type call struct {
	model  string
	maxTok int64
}

// calling makes the calls before inner, recording each status.
func calling(statuses *[]int, inner step, calls ...call) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for _, c := range calls {
			status, _ := post(t, req, c.model, c.maxTok)
			*statuses = append(*statuses, status)
		}
		return inner(t, ctx, req)
	}
}

// out4k is a reply that costs $0.04 on sonnet: 4,000 output tokens.
func out4k() anthropicfake.Reply {
	return anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 4000})
}

func near(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

func TestNoCapHaltAtBootstrap(t *testing.T) {
	g := newGW(t, "", "enforce", "")
	b := withBucket(g.harness)
	rec, err := g.run(t) // no stage may run
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap || rec.Halt.Scope != "run" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.HasPrefix(rec.Reason, "halted: no_cap: ") || !strings.Contains(rec.Halt.Detail, "budget.per_run_usd") {
		t.Fatalf("reason = %q, halt = %+v", rec.Reason, rec.Halt)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key(repoSlug, "fugaro/"+runID)); ok {
		t.Fatal("the branch lock was taken")
	}
	if len(g.provider.State.PRs) != 0 || testutil.Git(t, g.remote, "for-each-ref", "refs/heads/fugaro/") != "" {
		t.Fatal("a halted bootstrap opened a PR or pushed a branch")
	}
	if g.fake.Count() != 0 {
		t.Fatal("the gateway was used")
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Status != runstore.StatusHalted || stored.Halt == nil || stored.FinishedAt == nil {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

// TestCascadeEnforceWithoutCapsHaltsNoCap is C1.19's admission half: a cascade
// run is a *gatewayed* run — its model calls are leased and accounted for by
// the credentials service's upstream, exactly as an api-key run's are (D143) —
// so an enforcing installation with no caps has to halt it no_cap at bootstrap,
// as TestNoCapHaltAtBootstrap's api-key run is halted, rather than admit it and
// let the calls fail one by one against a service that has nothing to lease.
//
// The run is a real one through the policy path: the switch is published (O2,
// without which the run does not start at all), the mode enforces and the caps
// are gone from the database, so the halt is Admit's own. Its outcome is none
// and the process exits 0: nothing was locked, pushed or opened, and no model
// call was made.
//
// It is a job run, because a cascade run is: `fugaro exec` never cascades
// (D149), and a local one is refused in bootstrap before it reaches admission
// at all.
//
// Hand mutation this fails on: building Admit's Gateway flag from api-key and
// vertex alone — the predicate C1.19 left behind here, which reads a cascade
// run as the unleased run an oauth run is and returns no halt at all.
func TestCascadeEnforceWithoutCapsHaltsNoCap(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: cascade", 1)
	if cfg == gwConfig(t, "") {
		t.Fatal("the fixture's agent.auth line moved: this test is not switching it to cascade")
	}
	g := newGW(t, cfg, "enforce", "")
	// A cascade run is a job run — `fugaro exec` never cascades (D149) — so
	// the harness is the cloud here, as TestTestKnobIgnoredInCloud's is: the
	// platform's own execution of this repository's job, and the project the
	// job names.
	g.deps.Local = false
	g.deps.Project = "aurora"
	g.deps.Execution = cloudRunExecution(gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")))
	g.deps.Env = append(g.deps.Env, "CLOUD_RUN_EXECUTION=exec-1")
	b := publishPolicy(t, g, fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":"enforce","allowed_models":["claude-sonnet-5-5"]}}`, projectpolicy.Version))
	// Nothing to lease against, anywhere: the run may not begin.
	b.db.Set(budget.PathCapsRepo(repoSlug), nil)
	b.db.Set(budget.PathCapsGlobal, nil)
	rec, err := b.run(t) // no stage may run
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.HasPrefix(rec.Reason, "halted: no_cap: ") || !strings.Contains(rec.Halt.Detail, "no per-run cap") {
		t.Fatalf("reason = %q, halt = %+v", rec.Reason, rec.Halt)
	}
	if ok, _ := b.bucket.Exists(context.Background(), lock.Key(repoSlug, "fugaro/"+runID)); ok {
		t.Fatal("the branch lock was taken")
	}
	if len(g.provider.State.PRs) != 0 {
		t.Fatal("a halted bootstrap opened a PR")
	}
	if g.fake.Count() != 0 {
		t.Fatal("the gateway was used")
	}
}

func TestVertexEnforceRefused(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg, "enforce", "5")
	g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone ||
		!strings.Contains(rec.Reason, "Vertex budgets are not supported yet") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestVertexEnforceBeforeNoCap(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg, "enforce", "")
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || rec.Halt != nil {
		t.Fatalf("a misconfiguration must not be a policy halt: rec = %+v, err = %v", rec, err)
	}
}

func TestVertexObserveStartsGatewayWithRegionOverrides(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg, "observe", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 10}))
	g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234", "VERTEX_REGION_CLAUDE_HAIKU_4_5=europe-west1")
	g.deps.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.TEST", TokenType: "Bearer"})
	var statuses []int
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		base := envValue(req.Env, "ANTHROPIC_VERTEX_BASE_URL")
		if base == "" || envValue(req.Env, "CLAUDE_CODE_SKIP_VERTEX_AUTH") != "1" {
			t.Errorf("the agent is not pointed at the gateway: %v", req.Env)
		}
		path := func(loc string) string {
			return base + "/projects/proj-1234/locations/" + loc + "/publishers/anthropic/models/" + sonnet + ":rawPredict"
		}
		body := `{"anthropic_version":"vertex-2023-10-16","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
		for _, loc := range []string{"europe-west1", "asia-east1"} {
			s, _ := postBody(t, path(loc), "", body)
			statuses = append(statuses, s)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, step, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(statuses) != 2 || statuses[0] != 200 || statuses[1] != 404 {
		t.Fatalf("statuses = %v, want the overridden region allowed and another refused", statuses)
	}
	if got := g.fake.Seen()[0].Header.Get("Authorization"); got != "Bearer ya29.TEST" {
		t.Fatalf("the upstream saw Authorization %q", got)
	}
}

func TestOAuthEnforceWithoutCapIsNotNoCap(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "auth: api-key", "auth: oauth", 1)
	g := newGW(t, cfg, "enforce", "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil || rec.Cost.ModelSource != "claude-code" {
		t.Fatalf("cost = %+v: no gateway may have run", rec.Cost)
	}
	for _, c := range g.agent.calls {
		if envValue(c.Env, "ANTHROPIC_BASE_URL") != "" {
			t.Fatal("an oauth run was pointed at a gateway")
		}
	}
}

func TestOAuthNeverProxied(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: oauth", 1)
	g := newGW(t, cfg, "observe", "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	check := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if envValue(req.Env, "ANTHROPIC_BASE_URL") != "" || envValue(req.Env, "ANTHROPIC_API_KEY") != "" {
				t.Errorf("the oauth agent's env routes elsewhere: %v", req.Env)
			}
			// No gateway, so no pins and no managed settings at all.
			if _, err := os.Stat(g.deps.ManagedSettingsPath); !os.IsNotExist(err) {
				t.Errorf("an oauth run has managed settings: %v", err)
			}
			return inner(t, ctx, req)
		}
	}
	rec, err := g.run(t, check(implement("feature")), check(review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if g.fake.Count() != 0 {
		t.Fatal("an oauth run reached the gateway's upstream")
	}
}

func TestGatewayCostReplacesClaudeCost(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "",
		out4k(), anthropicfake.MessageOK(haiku, pricing.Usage{Input: 100000}))
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{haiku, 1000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// implement() reports $1 and review $0.5 of its own: neither counts.
	if !near(rec.CostUSD, 0.14) || rec.Cost == nil || !near(rec.Cost.ModelUSD, 0.14) || rec.Cost.ModelSource != "gateway" {
		t.Fatalf("cost_usd = %v, cost = %+v", rec.CostUSD, rec.Cost)
	}
	if !near(rec.Cost.ModelBy[sonnet], 0.04) || !near(rec.Cost.ModelBy[haiku], 0.10) {
		t.Fatalf("model_by = %v", rec.Cost.ModelBy)
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || stored.Cost.ModelSource != "gateway" || !near(stored.CostUSD, 0.14) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

// gwRolesConfig is gwConfig with the reviewer on opus: a coder stage and a
// reviewer stage differ in Model and in price, so a mix-up between them
// shows in a test.
func gwRolesConfig(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "")
	out := strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+opus, 1)
	if out == cfg {
		t.Fatal("gwConfig has no reviewer line")
	}
	return out
}

// stageMicros is the stages' model_usd total in whole micro-dollars: the
// unit the gateway settles in, so the sums below compare exactly rather
// than within a float tolerance.
func stageMicros(t *testing.T, stages []runstore.StageTiming) pricing.Micros {
	t.Helper()
	var sum pricing.Micros
	for _, s := range stages {
		sum += micros(t, s.ModelUSD)
	}
	return sum
}

func micros(t *testing.T, usd float64) pricing.Micros {
	t.Helper()
	m, err := pricing.FromUSD(usd)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestStageTimingCarriesCost: each StageTiming carries its stage's own model
// cost and the model the stage was pinned to, so that a cost preview can
// average by stage kind and model (generic-tool plan Task 24). The coder and
// the reviewer are on different models, the loop runs a fix and a second
// review, and one stage also calls the background model, whose spend is
// inside that stage's figure.
func TestStageTimingCarriesCost(t *testing.T) {
	// With the gateway on, a stage's figure is the gateway's StageReport for
	// that stage, and the stages sum to the record's model cost to the
	// micro-dollar.
	g := newGW(t, gwRolesConfig(t), "observe", "",
		out4k(), // implement: sonnet, $0.04
		anthropicfake.MessageOK(haiku, pricing.Usage{Input: 100000}), // implement's background call, $0.10
		anthropicfake.MessageOK(opus, pricing.Usage{Output: 4000}),   // review 1: opus, $0.10
		out4k(), // fix: sonnet, $0.04
		anthropicfake.MessageOK(opus, pricing.Usage{Output: 4000})) // review 2: opus, $0.10
	var st []int
	rec, err := g.run(t,
		calling(&st, implement("feature"), call{sonnet, 4000}, call{haiku, 1000}),
		calling(&st, review("changes", 1), call{opus, 4000}),
		calling(&st, fixStep, call{sonnet, 4000}),
		calling(&st, review("ship", 0), call{opus, 4000}))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	want := []struct {
		name  string
		model string
		usd   float64
	}{
		{"implement", sonnet, 0.14}, // the coder's $0.04 plus the background model's $0.10
		{"review", opus, 0.10},      // the reviewer's model, not the coder's
		{"fix", sonnet, 0.04},
		{"review", opus, 0.10}, // the same stage kind a second time
	}
	if len(rec.Stages) != len(want) {
		t.Fatalf("stages = %+v", rec.Stages)
	}
	for i, w := range want {
		s := rec.Stages[i]
		if s.Name != w.name || s.Model != w.model || micros(t, s.ModelUSD) != micros(t, w.usd) {
			t.Errorf("stage %d = %+v, want %s on %s for $%v", i, s, w.name, w.model, w.usd)
		}
	}
	if rec.Cost == nil || stageMicros(t, rec.Stages) != micros(t, rec.Cost.ModelUSD) {
		t.Fatalf("the stages sum to %v, model_usd = %+v", stageMicros(t, rec.Stages), rec.Cost)
	}
	// With oauth there is no gateway: a stage's figure is the result event's
	// total_cost_usd, which implement() and review() report. The models are
	// still told apart.
	oauthCfg := strings.Replace(gwRolesConfig(t), "auth: api-key", "auth: oauth", 1)
	og := newGW(t, oauthCfg, "observe", "")
	og.deps.Env = append(filterEnv(og.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	orec, err := og.run(t, implement("feature"), review("ship", 0))
	if err != nil || orec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", orec, err)
	}
	if len(orec.Stages) != 2 || micros(t, orec.Stages[0].ModelUSD) != micros(t, 1) || micros(t, orec.Stages[1].ModelUSD) != micros(t, 0.5) {
		t.Fatalf("oauth stages = %+v", orec.Stages)
	}
	if orec.Stages[0].Model != sonnet || orec.Stages[1].Model != opus {
		t.Fatalf("oauth stage models = %+v", orec.Stages)
	}
	if orec.Cost == nil || stageMicros(t, orec.Stages) != micros(t, orec.Cost.ModelUSD) {
		t.Fatalf("oauth stages do not sum to model_usd: %+v, %+v", orec.Stages, orec.Cost)
	}
}

// subAgentCalls makes inner write n sub-agent tool calls into the stage's
// stream, so a test's stage carries a sub-agent count the relay sees.
func subAgentCalls(inner step, n int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for i := 0; i < n; i++ {
			_, _ = req.Transcript.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Agent","input":{"subagent_type":"general-purpose","prompt":"x"}}]}}` + "\n"))
		}
		return inner(t, ctx, req)
	}
}

// TestStageTimingRecordsGatewayCalls: a gateway run's stage records its
// calls from the stage report, and its agents from the relay's count of the
// sub-agent tool calls in the stage's own stream -- never the gateway's
// distinct-agent-ID count, which rests on an ID no one verifies
// (task-governance T1.3). The fake's model call carries no agent ID, so the
// gateway report would count no sub-agent while the stream names two.
func TestStageTimingRecordsGatewayCalls(t *testing.T) {
	g := newGW(t, gwRolesConfig(t), "observe", "",
		out4k(), // implement: sonnet, one call
		out4k()) // review: opus, one call
	var st []int
	rec, err := g.run(t,
		calling(&st, subAgentCalls(implement("feature"), 2), call{sonnet, 4000}),
		calling(&st, review("ship", 0), call{opus, 4000}))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	want := []struct {
		name   string
		calls  int
		agents int
	}{{"implement", 1, 2}, {"review", 1, 0}}
	if len(rec.Stages) != len(want) {
		t.Fatalf("stages = %+v", rec.Stages)
	}
	for i, w := range want {
		s := rec.Stages[i]
		if s.Name != w.name {
			t.Fatalf("stage %d = %+v, want %s", i, s, w.name)
		}
		if s.Calls == nil || *s.Calls != w.calls {
			t.Errorf("stage %q calls = %v, want %d from the stage report", s.Name, s.Calls, w.calls)
		}
		if s.Agents == nil || *s.Agents != w.agents {
			t.Errorf("stage %q agents = %v, want %d from the relay, not the gateway's distinct IDs", s.Name, s.Agents, w.agents)
		}
	}
}

// TestStageTimingOAuthHasNoCalls: without a gateway a stage has no call
// count, so calls is absent rather than zero (G5), while agents is still the
// relay's count of the stage's sub-agent tool calls (task-governance T1.3).
func TestStageTimingOAuthHasNoCalls(t *testing.T) {
	oauthCfg := strings.Replace(gwRolesConfig(t), "auth: api-key", "auth: oauth", 1)
	og := newGW(t, oauthCfg, "observe", "")
	og.deps.Env = append(filterEnv(og.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	rec, err := og.run(t,
		subAgentCalls(implement("feature"), 2),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(rec.Stages) != 2 {
		t.Fatalf("stages = %+v", rec.Stages)
	}
	for _, s := range rec.Stages {
		if s.Calls != nil {
			t.Errorf("stage %q calls = %d: an oauth run has no gateway, so calls is absent, never zero", s.Name, *s.Calls)
		}
	}
	if a := rec.Stages[0].Agents; a == nil || *a != 2 {
		t.Errorf("implement agents = %v, want 2 from the relay", a)
	}
	if a := rec.Stages[1].Agents; a == nil || *a != 0 {
		t.Errorf("review agents = %v, want 0 from the relay", a)
	}
}

// TestStageTimingLateSpendIsUnattributed: a gateway call that settles after
// its stage's wait ran out counts in the run's model cost and in no stage's
// report. The stages then sum to model_usd minus cost.model_by's
// "unattributed", which is the exact relation the runner's comment states
// (generic-tool plan Task 24).
//
// The ordering is enforced by the fakes, never by a sleep lining up with a
// stream: the late call is held inside the scripted upstream until the
// second stage releases it, and that stage then waits for the gateway's own
// settle log line before returning. So the call is in flight for every
// microsecond of its stage's report (the upstream function has not even
// answered its headers), and it has provably settled before the run closes
// the gateway and reads the ledger.
func TestStageTimingLateSpendIsUnattributed(t *testing.T) {
	slow := out4k()
	slow.Events = anthropicfake.StreamOK(sonnet, pricing.Usage{Output: 4000}).Events
	slow.Body = ""
	g := newGW(t, gwConfig(t, ""), "observe", "")
	g.deps.GatewayStageWait = 20 * time.Millisecond
	// release holds the late call upstream; upstream says it arrived, so a
	// stage can return knowing the call is registered and in flight.
	release, upstream := make(chan struct{}), make(chan struct{})
	var upstreamOnce sync.Once
	g.fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		upstreamOnce.Do(func() { close(upstream) })
		<-release
		return slow
	}
	fireAndForget := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		go post(t, req, sonnet, 4000) // outlives its stage's wait
		<-upstream                    // it reached the fake, so it is in flight
		return implement("feature")(t, ctx, req)
	}
	waits := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		close(release) // the call settles now, after its stage's report
		// The settle is logged after the ledger is updated, so this is the
		// moment the run's money includes it.
		waitUntil(t, "the late call to settle", func() bool {
			s := g.logs.String()
			return strings.Contains(s, "stage=implement") && strings.Contains(s, "settled=usage")
		})
		return review("ship", 0)(t, ctx, req)
	}
	rec, err := g.run(t, fireAndForget, waits)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil || micros(t, rec.Cost.ModelUSD) != micros(t, 0.04) {
		t.Fatalf("cost = %+v: the late call must count in the run's total", rec.Cost)
	}
	if got := stageMicros(t, rec.Stages); got != 0 {
		t.Fatalf("the stages total %v µ$: the late call is in no stage's report (%+v)", got, rec.Stages)
	}
	if got := micros(t, rec.Cost.ModelBy["unattributed"]); got != micros(t, 0.04) {
		t.Fatalf("unattributed = %v µ$, want the whole late call", got)
	}
	// The invariant, exactly: the stages plus that residual are model_usd.
	if sum := stageMicros(t, rec.Stages) + micros(t, rec.Cost.ModelBy["unattributed"]); sum != micros(t, rec.Cost.ModelUSD) {
		t.Fatalf("stages (%+v) plus unattributed (%v) is %v µ$, model_usd is %v µ$",
			rec.Stages, rec.Cost.ModelBy["unattributed"], sum, micros(t, rec.Cost.ModelUSD))
	}
}

// TestStageTimingFailedStage: a stage that fails still records its own cost
// and model, so a failed run's per-stage spend is complete.
func TestStageTimingFailedStage(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", out4k(), out4k())
	var st []int
	fails := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		post(t, req, sonnet, 4000) // the reviewer spent before it fell over
		return agent.Result{}, errors.New("the reviewer fell over")
	}
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), fails)
	if err != nil || rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(rec.Stages) != 2 || rec.Stages[1].Name != "review" || rec.Stages[1].Model != sonnet ||
		micros(t, rec.Stages[1].ModelUSD) != micros(t, 0.04) {
		t.Fatalf("stages = %+v: the failed stage's own spend is missing", rec.Stages)
	}
	if rec.Cost == nil || stageMicros(t, rec.Stages) != micros(t, rec.Cost.ModelUSD) {
		t.Fatalf("the stages sum to %v, model_usd = %+v", stageMicros(t, rec.Stages), rec.Cost)
	}
}

// TestStageTimingProviderRouteAndReviewFirst: a stage whose coder is a
// provider's model is recorded with that model and its own figure, and the
// first-line review is a stage like any other. A stage with no model call
// records none.
func TestStageTimingProviderRouteAndReviewFirst(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerFirstLine(t, "on"), "enforce", "50",
		[]anthropicfake.Reply{
			anthropicfake.Compat{}.MessageOK(deepseek, use),
			anthropicfake.Compat{}.MessageOK(deepseek, use),
		}, "acme/app")
	// The figure is the run's own table's for that usage (Task 4: the
	// catalog prices the route's model, so naming a number here would only
	// pin the catalog's rates, not the stage's attribution).
	m, ok := g.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the model it routed")
	}
	one := m.Rates.Cost(use).USD()
	var status, firstStatus int
	var body, firstBody string
	rec, err := g.run(t,
		callDeepseek(&status, &body, implement("feature")),
		callDeepseek(&firstStatus, &firstBody, review("ship", 0)), // review_first, on the coder's model
		review("ship", 0)) // the senior review, on the reviewer's
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !eq(stageNames(rec), "implement", "review_first", "review") {
		t.Fatalf("stages = %v", stageNames(rec))
	}
	want := []struct {
		name  string
		model string
		usd   float64
	}{
		{"implement", deepseek, one},
		{"review_first", deepseek, one},
		{"review", sonnet, 0},
	}
	for i, w := range want {
		s := rec.Stages[i]
		if s.Name != w.name || s.Model != w.model || micros(t, s.ModelUSD) != micros(t, w.usd) {
			t.Errorf("stage %d = %+v, want %s on %s for $%v", i, s, w.name, w.model, w.usd)
		}
	}
	if rec.Cost == nil || stageMicros(t, rec.Stages) != micros(t, rec.Cost.ModelUSD) || micros(t, rec.Cost.ModelUSD) != micros(t, 2*one) {
		t.Fatalf("the stages total %v, model_usd = %+v", stageMicros(t, rec.Stages), rec.Cost)
	}
}

func TestCostCrossCheckWarns(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", out4k())
	var st []int
	// implement() says $1 against the gateway's $0.04.
	if _, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	logs := g.logs.String()
	if n := strings.Count(logs, `msg="cost cross-check" stage=implement`); n != 1 || !strings.Contains(logs, "gateway_usd=0.04") || !strings.Contains(logs, "claude_code_usd=1") {
		t.Fatalf("cross-check warnings: %d\n%s", n, logs)
	}
	// Figures within 5% of each other raise nothing.
	g2 := newGW(t, gwConfig(t, ""), "observe", "", out4k())
	costing := func(inner step, usd float64) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			res, err := inner(t, ctx, req)
			res.CostUSD = usd
			return res, err
		}
	}
	if _, err := g2.run(t, costing(calling(&st, implement("feature"), call{sonnet, 4000}), 0.0405), costing(review("ship", 0), 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g2.logs.String(), "cost cross-check") {
		t.Fatalf("a close figure warned:\n%s", g2.logs.String())
	}
}

// A stage the halt stopped never reported its cost; the figure it left
// says nothing and raises no cross-check warning.
func TestCostCrossCheckSkipsAHaltedStage(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "enforce", "0.10", capScript(3)...)
	var st []int
	// implement() says $1 against the gateway's $0.08, but the third call
	// is refused and the run halts.
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if strings.Contains(g.logs.String(), "cost cross-check") {
		t.Fatalf("a halted stage warned:\n%s", g.logs.String())
	}
}

func unparsed2xx() anthropicfake.Reply {
	return anthropicfake.Reply{Status: 200, Body: "not json at all"}
}

func TestUnreconciledRecorded(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", unparsed2xx())
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Cost == nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// A reply nobody could read costs its whole reservation: more than $0.04.
	if rec.Cost.Unreconciled < 0.04 || !near(rec.Cost.Unreconciled, rec.Cost.ModelUSD) {
		t.Fatalf("unreconciled = %v of model %v", rec.Cost.Unreconciled, rec.Cost.ModelUSD)
	}
}

func TestUsageUnparsedNotedInRecord(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", unparsed2xx())
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Cost == nil || rec.Cost.UsageUnparsed != 1 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(g.logs.String(), "usage_unparsed: 1") {
		t.Fatalf("no usage_unparsed line:\n%s", g.logs.String())
	}
}

func TestTokenCapUsesGatewayWhenLarger(t *testing.T) {
	g := newGW(t, gwConfig(t, "  max_run_tokens: 100\n"), "observe", "",
		anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 500, Output: 50}))
	var st []int
	small := withUsage(implement("feature"), agent.Usage{Input: 10}, nil) // the result event says 10
	rec, err := g.run(t, calling(&st, small, call{sonnet, 100}))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap || rec.Halt.Detail != "run used 550 tokens of 100" {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
}

func TestRecordCostFromLedger(t *testing.T) {
	slow := out4k()
	slow.Events = anthropicfake.StreamOK(sonnet, pricing.Usage{Output: 4000}).Events
	slow.Body, slow.EventDelay = "", 80*time.Millisecond
	g := newGW(t, gwConfig(t, ""), "observe", "", slow)
	g.deps.GatewayStageWait = 20 * time.Millisecond
	fireAndForget := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		go post(t, req, sonnet, 4000) // outlives its stage's wait
		time.Sleep(50 * time.Millisecond)
		return implement("feature")(t, ctx, req)
	}
	waits := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		time.Sleep(900 * time.Millisecond) // the call settles meanwhile
		return review("ship", 0)(t, ctx, req)
	}
	rec, err := g.run(t, fireAndForget, waits)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !near(rec.CostUSD, 0.04) || !near(rec.Cost.ModelUSD, 0.04) {
		t.Fatalf("cost_usd = %v, cost = %+v: the late call must count", rec.CostUSD, rec.Cost)
	}
}

func TestSpendErrIsInfraErrorAfterClaim(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	h.deps.SpendErr = errors.New(`FUGARO_MAX_RUN_USD "abc": it must be a number`)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, `FUGARO_MAX_RUN_USD "abc"`) {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if stored, rerr := h.store.ReadRecord(context.Background()); rerr != nil || stored.Status != runstore.StatusInfraError {
		t.Fatalf("the failure was not recorded: %+v, %v", stored, rerr)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key(repoSlug, "fugaro/"+runID)); ok {
		t.Fatal("the branch lock was taken")
	}
}

func TestUnpinnedModelFailsStage(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "")
	var st []int
	review := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		status, _ := post(t, req, opus, 100) // not the reviewer's, not the background model
		st = append(st, status)
		return review("ship", 0)(t, ctx, req) // Claude Code carries on regardless
	}
	rec, err := g.run(t, implement("feature"), review)
	if err != nil {
		t.Fatal(err)
	}
	if st[0] != 400 {
		t.Fatalf("the unpinned model got %d", st[0])
	}
	if rec.Status != runstore.StatusFailed || rec.Halt != nil || rec.Outcome != runstore.OutcomeDraft ||
		!strings.Contains(rec.Reason, "stage review") || !strings.Contains(rec.Reason, opus) || !strings.Contains(rec.Reason, "not pinned") {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, g.provider).Draft {
		t.Fatal("the run opened no draft")
	}
}

func TestBackgroundModelPinned(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", anthropicfake.MessageOK(haiku, pricing.Usage{Output: 10}))
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{haiku, 100}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || len(st) != 1 || st[0] != 200 {
		t.Fatalf("rec = %+v, err = %v, statuses = %v", rec, err, st)
	}
	if got := envValue(g.agent.calls[0].Env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != haiku {
		t.Fatalf("the background model is pinned as %q", got)
	}
}

func TestMaxOutputLimitReachesTheGateway(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", anthropicfake.MessageOK(haiku, pricing.Usage{Output: 10}))
	var st []int
	// 5,000 is over the role limit of 4,096 on the role's model, and fine for the background model.
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 5000}, call{haiku, 5000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0] != 400 || st[1] != 200 {
		t.Fatalf("statuses = %v", st)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "limit") {
		t.Fatalf("an over-limit call is a violation: %+v", rec)
	}
}

func TestPinsInvalidForBudgetIsInfraError(t *testing.T) {
	for name, cfg := range map[string]string{
		"no models": "",
		"an alias":  strings.Replace(gwConfig(t, ""), "coder: "+sonnet, "coder: sonnet", 1),
		"no price":  strings.Replace(gwConfig(t, ""), "background: "+haiku, "background: claude-unpriced-1", 1),
	} {
		t.Run(name, func(t *testing.T) {
			g := newGW(t, cfg, "observe", "")
			rec, err := g.run(t)
			if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "agent.models.") {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if len(g.agent.calls) != 0 {
				t.Fatal("the agent ran")
			}
		})
	}
}

// capScript replies to each call with $0.04 of output.
func capScript(n int) []anthropicfake.Reply {
	out := make([]anthropicfake.Reply, n)
	for i := range out {
		out[i] = out4k()
	}
	return out
}

func TestRunCapHaltOpensDraft(t *testing.T) {
	g := newGW(t, gwConfig(t, critRigor), "enforce", "0.10", capScript(3)...)
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 {
		t.Fatalf("statuses = %v: the third call is the one refused", st)
	}
	if g.fake.Count() != 2 {
		t.Fatalf("the upstream saw %d calls: a refused call is never forwarded", g.fake.Count())
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Halt.Detail, "$0.10") || !strings.Contains(rec.Halt.Detail, "$0.08") {
		t.Fatalf("the halt's detail names neither the cap nor the spend: %q", rec.Halt.Detail)
	}
	if len(g.agent.calls) != 1 || !near(rec.CostUSD, 0.08) {
		t.Fatalf("%d stages ran, cost %v", len(g.agent.calls), rec.CostUSD)
	}
	if !onlyPR(t, g.provider).Draft {
		t.Fatal("the PR is not a draft")
	}
	if report := lastComment(t, g.harness); !strings.Contains(report, "**Halted:** the per-run dollar cap was reached") ||
		!strings.Contains(report, "$0.10") || !strings.Contains(report, "$0.08") || !strings.Contains(report, "fugaro run --pr 1") {
		t.Fatalf("report:\n%s", report)
	}
}

func TestHaltWaitsForCallBoundary(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "enforce", "0.01")
	var cancelledAtEnd bool
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintln(req.Transcript, `{"type":"system"}`)
		if status, _ := post(t, req, sonnet, 4000); status != 403 {
			t.Errorf("status = %d, want the 403", status)
		}
		time.Sleep(150 * time.Millisecond) // Claude Code finishing its turn
		fmt.Fprintln(req.Transcript, `{"type":"result"}`)
		cancelledAtEnd = ctx.Err() != nil
		return agent.Result{}, nil
	}
	rec, err := g.run(t, step)
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if cancelledAtEnd {
		t.Fatal("the stage was stopped although the agent was exiting by itself")
	}
	data, err := g.bucket.ReadAll(context.Background(), "runs/"+repoSlug+"/"+runID+"/transcripts/implement-1.jsonl")
	if err != nil || !strings.Contains(string(data), `"result"`) {
		t.Fatalf("the transcript is incomplete: %q, %v", data, err)
	}
}

func TestHaltGraceThenKill(t *testing.T) {
	runner.SetHaltGrace(t, 50*time.Millisecond)
	g := newGW(t, gwConfig(t, ""), "enforce", "0.01")
	var cause error
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		post(t, req, sonnet, 4000)
		select { // an agent that ignores the 403
		case <-ctx.Done():
			cause = context.Cause(ctx)
			return agent.Result{}, ctx.Err()
		case <-time.After(10 * time.Second):
			t.Error("the stage was never stopped")
			return agent.Result{}, nil
		}
	}
	rec, err := g.run(t, step)
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !errors.Is(cause, runner.ErrHalted) {
		t.Fatalf("the stage was stopped with cause %v", cause)
	}
}

func TestInFlightCallsFinishAfterHalt(t *testing.T) {
	slow := anthropicfake.StreamOK(sonnet, pricing.Usage{Output: 4000})
	slow.EventDelay = 60 * time.Millisecond
	g := newGW(t, gwConfig(t, critRigor), "enforce", "0.10", slow, out4k())
	var slowStatus int
	var slowBody string
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); slowStatus, slowBody = post(t, req, sonnet, 4000) }()
		time.Sleep(150 * time.Millisecond) // the slow call is in flight, holding its reservation
		if s, _ := post(t, req, sonnet, 4000); s != 200 {
			t.Errorf("second call = %d", s)
		}
		// A call that can't fit the cap even alone halts the run, with the
		// slow one still in flight.
		base, key := gwEnv(req)
		big := fmt.Sprintf(`{"model":%q,"max_tokens":10,"messages":[{"role":"user","content":%q}]}`, sonnet, strings.Repeat("a", 300000))
		if s, _ := postBody(t, base+"/v1/messages", key, big); s != 403 {
			t.Errorf("third call = %d, want the cap refusing it", s)
		}
		wg.Wait()
		return agent.Result{}, nil
	}
	rec, err := g.run(t, step)
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if slowStatus != 200 || !strings.Contains(slowBody, "message_stop") {
		t.Fatalf("the call in flight did not finish: %d %q", slowStatus, slowBody)
	}
	if !near(rec.CostUSD, 0.08) {
		t.Fatalf("cost = %v, want both settled calls", rec.CostUSD)
	}
}

func TestHaltAgentExitsSuccessStopsLoop(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "enforce", "0.01")
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		post(t, req, sonnet, 4000)
		return agent.Result{IsError: false, Subtype: "success"}, nil // exits 0 after the 403
	}
	rec, err := g.run(t, step) // a review stage would be an unexpected call
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap ||
		!strings.HasPrefix(rec.Reason, "halted: run_cap") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(g.agent.calls) != 1 {
		t.Fatalf("%d stages started", len(g.agent.calls))
	}
}

func TestHaltRaceWithGateway(t *testing.T) {
	runner.SetStrictHaltCheck(t)
	for i := 0; i < 8; i++ {
		b := newHandleBox(t)
		g := newGW(t, gwConfig(t, ""), "enforce", "0.01")
		step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			done := make(chan struct{})
			go func() { defer close(done); b.h.MarkCancelled() }()
			post(t, req, sonnet, 4000) // the gateway halts as the cancel lands
			<-done
			return agent.Result{}, nil
		}
		// MarkCancelled alone doesn't stop the loop: a review may follow.
		rec, err := g.run(t, step, review("ship", 0))
		if err != nil && !strings.Contains(err.Error(), "cancel") {
			t.Fatalf("err = %v", err)
		}
		switch {
		case rec.Status == runstore.StatusHalted && rec.Halt != nil:
		case rec.Status == runstore.StatusCancelled && rec.Halt == nil:
		default:
			t.Fatalf("record = %+v: a halt and a cancel must exclude each other", rec)
		}
	}
}

func TestObserveModeNeverHalts(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "0.10", capScript(3)...)
	var st []int
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for _, s := range st {
		if s != 200 {
			t.Fatalf("statuses = %v: observe refuses nothing", st)
		}
	}
	if !strings.Contains(g.logs.String(), "would halt") {
		t.Fatalf("the would-halt was not logged:\n%s", g.logs.String())
	}
}

func TestManagedSettingsWrittenBeforeFirstStage(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "")
	first := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		env, deny, raw := settingsEnv(t, g.harness)
		base, key := gwEnv(req)
		if env["ANTHROPIC_BASE_URL"] != base || base == "" || !strings.HasPrefix(base, "http://127.0.0.1:") || env["ANTHROPIC_API_KEY"] != key {
			t.Errorf("settings env = %v, agent env base %q", env, base)
		}
		if strings.Contains(raw, plantedKey) || key == plantedKey {
			t.Errorf("the real key is in the settings or the agent's env: %s", raw)
		}
		if len(deny) != 2 {
			t.Errorf("web tools not denied: %s", raw)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, first, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

func TestTestKnobIgnoredInCloud(t *testing.T) {
	g := newGWFiles(t, gwConfig(t, ""), "observe", "", map[string]string{".claude/settings.json": rerouting})
	g.deps.GatewayUpstream = "" // as a local run against the real API would be
	g.deps.Local = false        // the CLI's resolved platform: a Cloud Run execution, or any it can't name
	g.deps.Project = "aurora"   // and its job names the project, as every job does
	// The execution the platform reports: the run's own per-repository job,
	// so the admission places it (a cloud process with none is refused there).
	g.deps.Execution = cloudRunExecution(gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")))
	g.deps.Env = append(g.deps.Env, "FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1", "CLOUD_RUN_EXECUTION=exec-1")
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "ANTHROPIC_BASE_URL") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(g.logs.String(), "ignored on Cloud Run"); n != 1 {
		t.Fatalf("%d warnings:\n%s", n, g.logs.String())
	}
}

func TestTestKnobLocal(t *testing.T) {
	h := newHarnessFiles(t, gwConfig(t, ""), nil, map[string]string{".claude/settings.json": rerouting})
	env := map[string]string{runner.BudgetModeEnv: "observe"}
	var err error
	if h.deps.Spend, err = runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err != nil {
		t.Fatal(err)
	}
	h.deps.Env = append(h.deps.Env, "FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1")
	// No upstream is set: the gateway would talk to the real API, so the
	// scripted agent makes no calls.
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// bucketText is everything the run stored.
func bucketText(t *testing.T, b *blob.Bucket) map[string]string {
	t.Helper()
	out := map[string]string{}
	it := b.List(nil)
	for {
		o, err := it.Next(context.Background())
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := b.ReadAll(context.Background(), o.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[o.Key] = string(data)
	}
}

func TestGatewayRunSecretScan(t *testing.T) {
	echoing := anthropicfake.Error(401, "authentication_error", "invalid x-api-key "+plantedKey)
	g := newGW(t, gwConfig(t, ""), "observe", "", out4k(), echoing)
	var seenEnv []string
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		seenEnv = append(seenEnv, req.Env...)
		for _, c := range []call{{sonnet, 4000}, {sonnet, 100}} {
			status, body := post(t, req, c.model, c.maxTok)
			fmt.Fprintf(req.Transcript, "{\"status\":%d,\"body\":%q}\n", status, body)
			fmt.Fprintf(req.Stderr, "status %d: %s\n", status, body)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, step, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	for _, kv := range seenEnv {
		if strings.Contains(kv, plantedKey) {
			t.Errorf("the agent's env holds the real key: %s", kv)
		}
	}
	for name, text := range bucketText(t, g.bucket) {
		if strings.Contains(text, plantedKey) {
			t.Errorf("bucket object %s holds the real key", name)
		}
	}
	if strings.Contains(g.logs.String(), plantedKey) {
		t.Errorf("a log line holds the real key:\n%s", g.logs.String())
	}
	// The managed settings are removed when the run ends; what they held
	// was read while the agent ran (TestManagedSettingsBeforeEveryStage).
	if raw, err := os.ReadFile(g.deps.ManagedSettingsPath); err == nil && strings.Contains(string(raw), plantedKey) {
		t.Errorf("settings file holds the key: %s", raw)
	}
	for _, c := range g.agent.calls {
		for _, kv := range c.Env {
			if strings.Contains(kv, plantedKey) {
				t.Errorf("the agent's env holds the real key: %s", kv)
			}
		}
	}
	// Positive control: the upstream is where the key goes.
	if seen := g.fake.Seen(); len(seen) == 0 || seen[0].Header.Get("x-api-key") != plantedKey {
		t.Fatal("the upstream never saw the real key")
	}
}

// spyProvider tries a model call from finalize.
type spyProvider struct {
	gitprov.Provider
	onFinalize func()
}

func (s *spyProvider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	s.onFinalize()
	return s.Provider.EnsurePR(ctx, spec)
}

func TestGatewayClosedBeforeFinalize(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "")
	var base, key string
	var status int
	var body string
	inner := g.deps.OpenProvider
	g.deps.OpenProvider = func(ctx context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		p, s, err := inner(ctx, kind, repo)
		return &spyProvider{Provider: p, onFinalize: func() {
			status, body = postBody(t, base+"/v1/messages", key, `{"model":"`+sonnet+`","max_tokens":10,"messages":[{"role":"user","content":"x"}]}`)
		}}, s, err
	}
	remember := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			base, key = gwEnv(req)
			return inner(t, ctx, req)
		}
	}
	rec, err := g.run(t, remember(implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if status != 0 || !strings.Contains(body, "refused") {
		t.Fatalf("a model call during finalize got %d %q, want connection refused", status, body)
	}
	if g.fake.Count() != 0 {
		t.Fatal("a call reached the upstream")
	}
}
