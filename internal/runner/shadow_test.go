package runner_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// challengerModel is the coder a challenger arm runs (D555): the provider
// family's own fixture model, routed for this repository, so an arm's one
// difference is a model the run's checks see and nothing that could reach the
// network.
const challengerModel = deepseek

// unroutedChallenger is a vendor/model ID no route claims: the shape a
// replayed or hand-edited task names when it wants a model the owner never
// opened for this repository (D556).
const unroutedChallenger = "invented-vendor/model-1"

// armFamily is the route the owner opened for the challenger's family.
const armFamily = "deepseek/*"

// armLayer is the project layer of a sampled run: the owner's ab: block,
// naming the challengers a task may carry. project: and gcp_project: are the
// fixture fugaro.yaml's own (armCfg), so the layer applies.
const armLayer = `version: 1
project: aurora
gcp_project: proj-1234
ab:
  rate: 0.05
  challengers: [` + challengerModel + `, ` + unroutedChallenger + `]
`

// armCfg is the fixture's configuration with priced models (gwConfig) and the
// project identity the layer above carries.
func armCfg(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "")
	out := strings.Replace(cfg, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	if out == cfg {
		t.Fatal("fixture has no project line")
	}
	return out
}

// armPRCfg is armCfg with reviewers, labels and review rounds, which the
// pull-request side of a run reads (prCfg).
func armPRCfg(t *testing.T, rounds int, prExtra string) string {
	t.Helper()
	cfg := armCfg(t)
	out := strings.Replace(cfg, "  base_branch: main\n", "  base_branch: main\n  pr: { labels: [fugaro], reviewers: [octocat]"+prExtra+" }\n", 1)
	out = strings.Replace(out, "review_rounds: 2", fmt.Sprintf("review_rounds: %d", rounds), 1)
	return out
}

// armTask is a challenger arm's task: the block A1.1 defines, carrying the
// challenger the owner named. An empty challenger is a task with no ab block
// at all, which is an unsampled run.
func armTask(challenger string) *task.Spec {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	if challenger != "" {
		spec.AB = &task.AB{PairID: "pair-" + runID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
			Challenger: challenger, ChallengerSource: task.ABChallengerOwner}
	}
	return spec
}

// withLayer is spec carrying a project layer.
func withLayer(spec *task.Spec, layer string) *task.Spec {
	spec.ProjectLayer = &task.ProjectLayer{SHA256: config.LayerSum([]byte(layer)), Generation: 3, YAML: layer}
	return spec
}

// armProvider wraps the audited fake and counts the git credentials the
// runner minted from it: the write path (GitAuth, a run's own installation
// token) and the read-only one (GitAuthReadOnly). A shadow arm must never
// take the first, and its clone must take the second — both are counted
// rather than inferred from the token a run happened to work with.
type armProvider struct {
	*auditProvider
	mu      sync.Mutex
	writes  int
	reads   int
	roToken string
}

func (a *armProvider) counts() (writes, reads int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes, a.reads
}

func (a *armProvider) GitAuth(ctx context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	a.mu.Lock()
	a.writes++
	a.mu.Unlock()
	return a.auditProvider.GitAuth(ctx, minValid)
}

func (a *armProvider) GitAuthReadOnly(_ context.Context, _ time.Duration) (gitprov.GitAuth, error) {
	a.mu.Lock()
	a.reads++
	a.mu.Unlock()
	return gitprov.GitAuth{Username: "x-access-token", Token: a.roToken, Expires: time.Now().Add(time.Hour)}, nil
}

// armGW is a run of the arm cfg: the fixture's configuration and the owner's
// route, the task's ab block, the project layer naming the challenger, the
// budget gateway enforcing, and audited provider calls. capUSD "" leaves the
// run with no per-run cap.
func armGW(t *testing.T, cfg, capUSD, challenger, layer string) (*gw, *armProvider) {
	t.Helper()
	h := newHarnessFiles(t, cfg, withLayer(armTask(challenger), layer), nil)
	up, srv := anthropicfake.New(t)
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	env := map[string]string{runner.BudgetModeEnv: "enforce", runner.MaxRunUSDEnv: capUSD}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend, h.deps.GatewayUpstream = spend, srv.URL
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	g := &gw{harness: h, fake: up, logs: logs}
	wireRoutes(t, g, []string{armFamily}, "", nil, "acme/app")
	p := &armProvider{auditProvider: &auditProvider{Provider: h.provider, t: t, store: h.store}, roToken: "ro-token"}
	h.deps.OpenProvider = gitprov.Static(p)
	h.deps.RetryDelay = time.Millisecond
	return g, p
}

// publishes are the provider calls that reach a person: every one of them is
// a call a shadow arm must never make.
func publishes(h *harness) []string {
	out := []string{}
	for _, c := range ops(h) {
		switch {
		case strings.HasPrefix(c, "EnsurePR#"), strings.HasPrefix(c, "UpdatePR#"),
			strings.HasPrefix(c, "Comment#"), strings.HasPrefix(c, "ApplyReady#"):
			out = append(out, c)
		}
	}
	return out
}

// TestShadowNeverPushes is the side-effect boundary of a challenger arm
// (D554, D568): across a ready run, a cap stop, a governance halt with
// checkpoints on and a config that opens an early draft, the remote accepts
// no push at all, mid-stage or at finalize, and the run branch never reaches
// it.
func TestShadowNeverPushes(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
		pushes := countPushes(t, g.harness)
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		if n := pushes(); n != 0 {
			t.Fatalf("the remote accepted %d push(es) from a challenger arm", n)
		}
		if rec.Outcome != runstore.OutcomeNone {
			t.Fatalf("outcome = %q, want none: a challenger publishes nothing", rec.Outcome)
		}
		if remoteHasBranch(t, g.harness) {
			t.Fatal("the run branch reached the remote")
		}
		if rec.PushedHead != "" {
			t.Fatalf("the record says a tip was pushed: %q", rec.PushedHead)
		}
	})

	t.Run("cap halt", func(t *testing.T) {
		b := newHandleBox(t)
		g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
		pushes := countPushes(t, g.harness)
		rec, err := g.run(t, haltAfter(b, implement("feature")), review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil {
			t.Fatalf("record = %+v, want a halt", rec)
		}
		if n := pushes(); n != 0 {
			t.Fatalf("the remote accepted %d push(es) from a halted challenger arm", n)
		}
		if remoteHasBranch(t, g.harness) {
			t.Fatal("the run branch reached the remote")
		}
	})

	t.Run("governance halt with checkpoints on", func(t *testing.T) {
		g, _ := armGW(t, armPRCfg(t, 2, ", early_draft: false"), "50", challengerModel, armLayer)
		runner.ResetStageHook(t)
		if err := runner.SetStageHook(runner.StageFuncs{
			Timeout: func(runner.StageView) time.Duration { return 300 * time.Millisecond },
			TimedOut: func(runner.StageView) runner.StageDecision {
				return runner.StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}
			},
		}); err != nil {
			t.Fatal(err)
		}
		pushes := countPushes(t, g.harness)
		long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			commitWIP(t, req, "wip")
			<-ctx.Done()
			return agent.Result{}, ctx.Err()
		}
		rec, err := g.run(t, long)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget {
			t.Fatalf("record = %+v, want a governance halt", rec)
		}
		if n := pushes(); n != 0 {
			t.Fatalf("the remote accepted %d push(es) from a challenger arm that halted mid-stage", n)
		}
		if remoteHasBranch(t, g.harness) {
			t.Fatal("the run branch reached the remote")
		}
	})

	t.Run("early draft", func(t *testing.T) {
		g, _ := armGW(t, armPRCfg(t, 2, ", early_draft: true"), "50", challengerModel, armLayer)
		pushes := countPushes(t, g.harness)
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Outcome != runstore.OutcomeNone {
			t.Fatalf("outcome = %q, want none", rec.Outcome)
		}
		if n := pushes(); n != 0 {
			t.Fatalf("the remote accepted %d push(es) from a challenger arm with early_draft on", n)
		}
		if remoteHasBranch(t, g.harness) {
			t.Fatal("the run branch reached the remote")
		}
	})
}

// TestShadowOpensNoPR: no call that opens, updates, comments on or sets a
// status on a pull request, the refused-push note included (D554).
func TestShadowOpensNoPR(t *testing.T) {
	g, _ := armGW(t, armPRCfg(t, 2, ", early_draft: true"), "50", challengerModel, armLayer)
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if got := publishes(g.harness); len(got) != 0 {
		t.Fatalf("a challenger arm called the provider: %v", got)
	}
	if n := len(g.provider.State.PRs); n != 0 {
		t.Fatalf("%d pull request(s) exist after a challenger run", n)
	}
}

// TestShadowAgentHasNoGitToken: the agent's environment carries no git
// credential at all — no FUGARO_GIT_TOKEN, no GH_TOKEN, no credential
// helper — as a review run's does not (A2.1).
func TestShadowAgentHasNoGitToken(t *testing.T) {
	g, p := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	p.roToken = "ro-token"
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	req := g.agent.calls[0]
	for _, key := range []string{"FUGARO_GIT_TOKEN", "GH_TOKEN", "GIT_ASKPASS", "GIT_CONFIG_KEY_0"} {
		if v := envValue(req.Env, key); v != "" {
			t.Fatalf("the challenger's agent environment carries %s=%q", key, v)
		}
	}
	for _, kv := range req.Env {
		if strings.Contains(kv, "ro-token") {
			t.Fatalf("the challenger's agent environment carries the run's git credential: %s", kv)
		}
	}
}

// TestShadowMintsNoWriteToken: the run never asks the provider for a
// write-capable installation token, and its clone is made with the read-only
// one — the remote accepts only that token, so a clone that used a write
// token would not happen at all (A2.1).
func TestShadowMintsNoWriteToken(t *testing.T) {
	g, p := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	g.useHTTPRemote(t, testutil.Token("x-access-token", "ro-token"))
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	writes, reads := p.counts()
	if writes != 0 {
		t.Fatalf("the runner minted %d write-capable token(s) for a challenger arm", writes)
	}
	if reads == 0 {
		t.Fatal("the runner never asked for a read-only token")
	}
	if rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("outcome = %q, want none", rec.Outcome)
	}
}

// TestShadowChallengerCoderOnly: the challenger is the coder's model alone
// (D555). The run's stages are the proof: the coder runs on the challenger
// and the reviewer keeps the model the champion's run has, which a config
// with no per-role models must not have moved.
func TestShadowChallengerCoderOnly(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	var coder, reviewer string
	watch := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if req.Model == challengerModel {
			coder = req.Model
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, watch, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	reviewer = g.agent.calls[1].Model
	if coder != challengerModel {
		t.Fatalf("the implement stage ran on %q, want the challenger %q", coder, challengerModel)
	}
	if reviewer == challengerModel {
		t.Fatalf("the review stage ran on the challenger too: %q", reviewer)
	}
	if want := sonnet; reviewer != want {
		t.Fatalf("the review stage ran on %q, want the champion's reviewer %q", reviewer, want)
	}
}
