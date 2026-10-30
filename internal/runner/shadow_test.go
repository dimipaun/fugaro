package runner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitops"
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
	return armGWSpec(t, cfg, capUSD, armTask(challenger), layer)
}

// champTask is a champion arm's task: the pair block with the champion's own
// arm, which publishes exactly as an unsampled run does (D554).
func champTask() *task.Spec {
	spec := armTask("")
	spec.AB = &task.AB{PairID: "pair-" + runID, Arm: task.ABArmChampion}
	return spec
}

// armGWSpec is armGW for a task of the test's own: a champion's (champTask),
// or a challenger the fixture's layer does not list.
func armGWSpec(t *testing.T, cfg, capUSD string, spec *task.Spec, layer string) (*gw, *armProvider) {
	t.Helper()
	return armGWFiles(t, cfg, capUSD, spec, layer, nil)
}

// armGWFiles is armGWSpec with the repository's own files, for a run whose
// configuration is a repo recipe (the check gate's).
func armGWFiles(t *testing.T, cfg, capUSD string, spec *task.Spec, layer string, files map[string]string) (*gw, *armProvider) {
	t.Helper()
	h := newHarnessFiles(t, cfg, withLayer(spec, layer), files)
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

// plainLayer is the same layer with no ab: block: an installation that never
// turned the feature on, which every fixture run but these has.
const plainLayer = `version: 1
project: aurora
gcp_project: proj-1234
`

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

// TestShadowAgentEnvDropsEveryForgeToken: not one variable the champion's
// agent carries a forge credential in reaches the challenger's agent — the
// arm pushes nothing, so a credential in that environment is one an agent
// could push with (A2.1, D554). The set is read off the code that builds it
// rather than written out here: gitops.CredentialVars's variables, which
// authVars copies into the agent's environment (the token itself, the
// username, and the credential-helper config that makes git hand the token
// over), and the provider's own GitAuth.Env, which is GitHub's GH_TOKEN. A
// credential a later change adds to either is covered without an edit here,
// and the champion's run proves the enumeration is the real one: every key it
// names is in the champion's environment.
func TestShadowAgentEnvDropsEveryForgeToken(t *testing.T) {
	const (
		user    = "x-access-token"
		token   = "forge-token"
		roToken = "ro-token"
	)
	// A provider that mints the way GitHub's does: the same value in the
	// credential helper's environment and in GH_TOKEN (github.Provider.GitAuth).
	champ, _ := armGWSpec(t, armCfg(t), "50", champTask(), armLayer)
	champRemote := champ.useHTTPRemote(t, testutil.Token(user, token, roToken))
	champ.provider.Auth = func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: user, Token: token, Expires: time.Now().Add(time.Hour),
			Env: map[string]string{"GH_TOKEN": token}}
	}

	// forgeKeys is every variable name a forge credential travels in, and the
	// value the champion's agent carries for it.
	cred, err := gitops.CredentialVars(gitops.CredentialURL(champRemote.URL), user, token)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := champ.provider.GitAuth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	forgeKeys := map[string]string{}
	maps.Copy(forgeKeys, cred)
	maps.Copy(forgeKeys, auth.Env)
	carrying := 0
	for _, v := range forgeKeys {
		if v == token {
			carrying++
		}
	}
	if carrying < 2 || len(forgeKeys) < len(cred) {
		t.Fatalf("the enumeration found no forge credentials to test the drop with: %v", forgeKeys)
	}

	if _, err := champ.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	champEnv := envKeys(champ.agent.calls[0].Env)
	for k := range forgeKeys {
		if !champEnv[k] {
			t.Fatalf("the enumeration names %s, which the champion's agent environment does not carry: %v", k, champ.agent.calls[0].Env)
		}
	}

	shadow, p := armGWSpec(t, armCfg(t), "50", armTask(challengerModel), armLayer)
	p.roToken = roToken
	shadow.useHTTPRemote(t, testutil.Token(user, roToken))
	if _, err := shadow.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	shadowEnv := envKeys(shadow.agent.calls[0].Env)
	for k := range forgeKeys {
		if shadowEnv[k] {
			t.Fatalf("the challenger's agent environment carries %s", k)
		}
	}
	for _, kv := range shadow.agent.calls[0].Env {
		if strings.Contains(kv, token) || strings.Contains(kv, roToken) {
			t.Fatalf("the challenger's agent environment carries a forge token: %s", kv)
		}
	}
}

// envKeys is the variable names an environment carries, whatever their values:
// a credential variable set to the empty string is still a variable that
// changes what git does (GIT_CONFIG_VALUE_0 is).
func envKeys(env []string) map[string]bool {
	out := make(map[string]bool, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok {
			out[k] = true
		}
	}
	return out
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
	// And the arm runs the champion's stages: one implement, one review. The
	// first-line review is derived from the coder's own family
	// (Agent.FirstLineOn turns it on for a coder a route serves), so an arm
	// whose challenger is a routed model would gain a stage the champion
	// never had — two runs whose stage sets differ are not two runs that
	// differ in the coder model (D555).
	if n := len(g.agent.calls); n != 2 {
		t.Fatalf("the challenger arm ran %d stages, want the champion's 2", n)
	}
}

// TestShadowChallengerPolicyRechecked: a replayed or hand-edited task names
// a challenger the run's own policy does not admit — the owner lists it, but
// no route of this run serves it — and the run refuses it at bootstrap,
// before any call and before the clone (D556).
func TestShadowChallengerPolicyRechecked(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", unroutedChallenger, armLayer)
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err == nil {
		t.Fatal("a challenger the run's policy does not admit was run")
	}
	if rec == nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a refusal", rec)
	}
	if !strings.Contains(rec.Reason, unroutedChallenger) {
		t.Fatalf("the reason does not name the challenger: %q", rec.Reason)
	}
	if len(g.agent.calls) != 0 {
		t.Fatalf("the agent ran %d stage(s) for a refused challenger", len(g.agent.calls))
	}
	if n := pushes(); n != 0 {
		t.Fatalf("the remote accepted %d push(es) from a refused challenger", n)
	}
}

// TestShadowChallengerNotInOwnerListHalts: membership is the owner's list in
// the project layer the task carries, re-checked here and never trusted from
// the task: an arm naming a model the owner did not open for this repository
// is refused, whatever the run's own policy would say about the model (D556).
func TestShadowChallengerNotInOwnerListHalts(t *testing.T) {
	otherOnly := strings.Replace(armLayer, "challengers: ["+challengerModel+", "+unroutedChallenger+"]",
		"challengers: ["+unroutedChallenger+"]", 1)
	if otherOnly == armLayer {
		t.Fatal("the layer does not carry a challenger list")
	}
	g, _ := armGW(t, armCfg(t), "50", challengerModel, otherOnly)
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err == nil {
		t.Fatal("a challenger the owner did not list for this repository was run")
	}
	if rec == nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a refusal", rec)
	}
	if !strings.Contains(rec.Reason, "ab.challengers") {
		t.Fatalf("the reason does not name the owner's list: %q", rec.Reason)
	}
	// The run's own policy would have admitted this model: what refused it is
	// the owner's list alone.
	if len(g.agent.calls) != 0 || pushes() != 0 {
		t.Fatalf("a refused arm ran a stage or pushed: calls %d, pushes %d", len(g.agent.calls), pushes())
	}
}

// TestShadowBudgetBackendFailureIsInfraError: an infrastructure failure at
// bootstrap is infrastructure for a challenger arm too, and ends the run the
// way it ends a run carrying no arm at all. The arm's own model is the one
// thing about it the gate may refuse (D556), so a budget backend this run
// cannot use — a Vertex budget under enforce, and a managed settings directory
// the image cannot hold — is recorded infra_error/none, never as the arm's
// refusal: a refusal says the boundary held, and reporting a broken
// installation as one would hide it behind a decision.
func TestShadowBudgetBackendFailureIsInfraError(t *testing.T) {
	t.Run("vertex budget", func(t *testing.T) {
		cfg := strings.Replace(armCfg(t), "auth: api-key", "auth: vertex", 1)
		if cfg == armCfg(t) {
			t.Fatal("the fixture does not name api-key auth")
		}
		g, _ := armGW(t, cfg, "50", challengerModel, armLayer)
		g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
		pushes := countPushes(t, g.harness)
		rec, err := g.run(t)
		if err == nil || rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
			t.Fatalf("record = %+v, err = %v; want an infra_error with outcome none", rec, err)
		}
		if !strings.Contains(rec.Reason, "Vertex budgets are not supported yet") {
			t.Fatalf("the reason does not name the Vertex budget: %q", rec.Reason)
		}
		if len(g.agent.calls) != 0 || pushes() != 0 {
			t.Fatalf("a run that stopped at bootstrap ran a stage or pushed: calls %d, pushes %d", len(g.agent.calls), pushes())
		}
	})

	t.Run("unwritable managed settings", func(t *testing.T) {
		g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
		g.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
		pushes := countPushes(t, g.harness)
		rec, err := g.run(t)
		if err == nil || rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
			t.Fatalf("record = %+v, err = %v; want an infra_error with outcome none", rec, err)
		}
		if !strings.Contains(rec.Reason, "can't hold Claude Code's managed settings") {
			t.Fatalf("the reason does not name the managed settings: %q", rec.Reason)
		}
		if len(g.agent.calls) != 0 || pushes() != 0 {
			t.Fatalf("a run that stopped at bootstrap ran a stage or pushed: calls %d, pushes %d", len(g.agent.calls), pushes())
		}
	})
}

// TestShadowModelRefusalStaysArmRefusal is the other half (D556): a model this
// repository may not run is the arm's own refusal and is recorded as one —
// failed/none with the policy's reason, and never infra_error, which would say
// the runner broke where the boundary held. The challenger is the owner's (the
// layer lists it) and it is the run's own policy that refuses it: no route of
// this run serves the model, so the same check that judges a run's own pins
// refuses it (TestShadowChallengerPolicyRechecked owns that path; this is the
// class it is recorded as).
func TestShadowModelRefusalStaysArmRefusal(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", unroutedChallenger, armLayer)
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err == nil {
		t.Fatal("a challenger the run's policy does not admit was run")
	}
	if rec == nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want the arm's refusal", rec)
	}
	if !strings.Contains(rec.Reason, unroutedChallenger) {
		t.Fatalf("the reason does not name the challenger's model: %q", rec.Reason)
	}
	if len(g.agent.calls) != 0 || pushes() != 0 || len(g.provider.State.PRs) != 0 {
		t.Fatalf("a refused arm ran a stage or published: calls %d, pushes %d, PRs %d",
			len(g.agent.calls), pushes(), len(g.provider.State.PRs))
	}
}

// TestShadowHaltStaysHalt: a halt is not a refusal, whatever arm the run
// carries: a cap the run has reached stops a challenger exactly as it stops
// any other run (D556's "a failing challenger halts the shadow" is about the
// model, and a halt is the governance's own outcome). The run here is an
// enforcing one under api-key auth with no per-run cap, which halts at
// bootstrap before anything is locked, pushed or opened.
func TestShadowHaltStaysHalt(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "", challengerModel, armLayer)
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone ||
		rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap {
		t.Fatalf("record = %+v, want a halt over the missing cap", rec)
	}
	if len(g.agent.calls) != 0 || pushes() != 0 {
		t.Fatalf("a halted arm ran a stage or pushed: calls %d, pushes %d", len(g.agent.calls), pushes())
	}
}

// TestShadowBitbucketHalts: a challenger's one credential must be read-only,
// and only GitHub mints such a token, so a Bitbucket repository is never a
// challenger in v1 (A2.1): the run stops at bootstrap, before the clone,
// rather than mint the write-capable token a challenger must never hold.
func TestShadowBitbucketHalts(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	// The provider a Bitbucket repository gets: a provider, with no
	// read-only mint of its own.
	g.deps.ProviderKind = gitprov.KindBitbucket
	g.deps.OpenProvider = gitprov.Static(&auditProvider{Provider: g.provider, t: t, store: g.store})
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err == nil {
		t.Fatal("a challenger arm ran against a provider that mints no read-only token")
	}
	if rec == nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a refusal", rec)
	}
	if !strings.Contains(rec.Reason, "read-only") {
		t.Fatalf("the reason does not say what was missing: %q", rec.Reason)
	}
	if len(g.agent.calls) != 0 {
		t.Fatalf("the agent ran %d stage(s) after the refusal", len(g.agent.calls))
	}
	if n := pushes(); n != 0 {
		t.Fatalf("the remote accepted %d push(es)", n)
	}
	if remoteHasBranch(t, g.harness) {
		t.Fatal("the run branch reached the remote")
	}
}

// TestUntaggedRunnerABKeysCapabilityLine: a build with no A/B feature that
// reads ab: keys in the project layer says so once, in the run log, and says
// nothing about a licence (D522, D523). A layer without the block gets
// nothing: a line that fired for every run would be a lie about a run nobody
// configured.
func TestUntaggedRunnerABKeysCapabilityLine(t *testing.T) {
	const line = "ab: is set but this build does not include it, so it is ignored"
	g, _ := armGWSpec(t, armCfg(t), "50", armTask(""), armLayer)
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	logs := g.logs.String()
	if n := strings.Count(logs, line); n != 1 {
		t.Fatalf("the capability line appears %d time(s) in the run log, want once:\n%s", n, logs)
	}
	if s := strings.ToLower(logs); strings.Contains(s, "licen") {
		t.Fatalf("the run log carries licence wording:\n%s", logs)
	}

	plain, _ := armGWSpec(t, armCfg(t), "50", armTask(""), plainLayer)
	if _, err := plain.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if s := plain.logs.String(); strings.Contains(s, line) {
		t.Fatalf("a layer with no ab: block got the capability line:\n%s", s)
	}
}

// TestShadowUploadsPatch: a challenger arm's patch object is the run's own
// tree diff, from the commit it started at to its final head, with no commit
// metadata in it — the pair's material, and the only thing a challenger
// leaves anywhere (D559, A2.1).
func TestShadowUploadsPatch(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	patch := readPatch(t, g)
	if rec.PatchTruncated {
		t.Fatal("the patch of a one-file run is truncated")
	}
	for _, want := range []string{"diff --git a/feature.txt b/feature.txt", "+feature"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("the patch does not carry %q:\n%s", want, patch)
		}
	}
	for _, unwanted := range []string{"commit ", "Author:", "Date:", "fugaro: "} {
		if strings.Contains(patch, unwanted) {
			t.Fatalf("the patch carries commit metadata (%q):\n%s", unwanted, patch)
		}
	}
	if rec.PatchObject != runstore.PatchKey(g.store.Prefix()) {
		t.Fatalf("patch_object = %q, want the run's own key", rec.PatchObject)
	}
	if rec.AB == nil || rec.AB.Arm != task.ABArmChallenger {
		t.Fatalf("record ab = %+v, want the challenger's arm", rec.AB)
	}
}

// TestShadowPatchCapped: a patch longer than the cap is stored cut, at the
// cap, and the record says so — the cap is a fact (PatchTruncated) rather
// than a silent cut, and the run it belongs to is not failed over it (A2.1).
func TestShadowPatchCapped(t *testing.T) {
	g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	big := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "awk 'BEGIN { for (i = 0; i < 300000; i++) print \"aaaaaaaaaaaaaaaa\" }' > big.txt && git add -A && git commit -qm big")
		verifyTest(t, ctx, req)
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := g.run(t, big, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	patch := readPatch(t, g)
	if !rec.PatchTruncated {
		t.Fatal("a patch over the cap is not marked truncated")
	}
	if len(patch) > runner.MaxPatchBytes {
		t.Fatalf("the stored patch is %d bytes, over the cap of %d", len(patch), runner.MaxPatchBytes)
	}
	if len(patch) < runner.MaxPatchBytes/2 {
		t.Fatalf("the stored patch is %d bytes: the cap cut far more than the cap", len(patch))
	}
	if !strings.HasSuffix(patch, "\n") {
		t.Fatal("the stored patch does not end at a line boundary")
	}
}

// readPatch reads the run's patch object back out of the store. It reads
// through the bucket rather than runstore.ReadFile, whose cap for anything
// but a transcript is blobx.MaxReadBytes (1 MiB): a patch may be twice that
// (MaxPatchBytes), so the runner's own reader would refuse a large one.
func readPatch(t *testing.T, g *gw) string {
	t.Helper()
	r, err := g.bucket.NewReader(context.Background(), g.store.Prefix()+runstore.PatchName, nil)
	if err != nil {
		t.Fatalf("reading the patch object: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the patch object: %v", err)
	}
	return string(data)
}

// TestChampionUploadsPatch: a champion publishes exactly as an unsampled run
// and adds its patch object — the arms' one shared addition (A2.1).
func TestChampionUploadsPatch(t *testing.T) {
	g, _ := armGWSpec(t, armCfg(t), "50", champTask(), armLayer)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	patch := readPatch(t, g)
	if !strings.Contains(patch, "+feature") {
		t.Fatalf("the champion's patch is not the branch it published:\n%s", patch)
	}
	if rec.PatchObject != runstore.PatchKey(g.store.Prefix()) {
		t.Fatalf("patch_object = %q, want the run's own key", rec.PatchObject)
	}
	if rec.AB == nil || rec.AB.Arm != task.ABArmChampion {
		t.Fatalf("record ab = %+v, want the champion's arm", rec.AB)
	}
	if rec.PushedHead == "" || len(g.provider.State.PRs) != 1 {
		t.Fatalf("the champion did not publish: head %q, %d PR(s)", rec.PushedHead, len(g.provider.State.PRs))
	}
}

// TestChampionPublishesAsUnsampled: the champion's run is an unsampled run
// in everything but its record's ab block and its patch object: the same
// provider calls, in the same order, with the same outcome (D554).
func TestChampionPublishesAsUnsampled(t *testing.T) {
	champ, _ := armGWSpec(t, armCfg(t), "50", champTask(), armLayer)
	champRec, err := champ.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := armGWSpec(t, armCfg(t), "50", armTask(""), plainLayer)
	plainRec, err := plain.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ops(champ.harness), ops(plain.harness); !slices.Equal(got, want) {
		t.Fatalf("the champion's provider calls differ from an unsampled run's:\n%v\n%v", got, want)
	}
	if champRec.Status != plainRec.Status || champRec.Outcome != plainRec.Outcome || champRec.Reason != plainRec.Reason {
		t.Fatalf("the champion ended %s/%s (%s), an unsampled run %s/%s (%s)",
			champRec.Status, champRec.Outcome, champRec.Reason, plainRec.Status, plainRec.Outcome, plainRec.Reason)
	}
	if champRec.AB == nil || plainRec.AB != nil {
		t.Fatalf("ab: champion %+v, unsampled %+v", champRec.AB, plainRec.AB)
	}
	if champRec.PatchObject == "" || plainRec.PatchObject != "" {
		t.Fatalf("patch_object: champion %q, unsampled %q", champRec.PatchObject, plainRec.PatchObject)
	}
}

// armGateCfg is the check-gate fixture's configuration with the project
// identity the owner's layer carries (armCfg's own insertion, on gateCfg's
// file): the layer applies, and the recipe under test is the gate's.
func armGateCfg(t *testing.T) string {
	t.Helper()
	cfg := armCfg(t)
	out := strings.Replace(cfg, "  review_rounds: 2\n", "  recipe: test-and-fix\n  review_rounds: 2\n", 1)
	if out == cfg {
		t.Fatal("fixture has no agent.review_rounds line")
	}
	return out
}

// TestShadowGateVerdictStands: a check gate that ends the run ends both arms
// of a pair the same way. finalize stands down for an arm wherever it stands
// down for the champion, so an arm of a clean gate keeps the gate's verdict
// instead of re-deriving the record from a checkout no stage ran in — "the
// agent made no commits" — which would leave the two records of a pair
// disagreeing about one run (D559) and hide a gate's own infra_error.
func TestShadowGateVerdictStands(t *testing.T) {
	cfg, files := armGateCfg(t), gateFiles("test-and-fix", testAndFixRecipe)

	champ, _ := armGWFiles(t, cfg, "50", champTask(), armLayer, files)
	champRec, err := champ.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if champRec.Status != runstore.StatusSucceeded || champRec.Outcome != runstore.OutcomeNone || champRec.Gate != "clean" {
		t.Fatalf("the champion's gate did not end the run cleanly: %+v", champRec)
	}

	shadow, _ := armGWFiles(t, cfg, "50", armTask(challengerModel), armLayer, files)
	rec, err := shadow.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != champRec.Status || rec.Outcome != champRec.Outcome ||
		rec.Reason != champRec.Reason || rec.Gate != champRec.Gate {
		t.Fatalf("the arm ended %s/%s %q gate %q, the champion %s/%s %q gate %q",
			rec.Status, rec.Outcome, rec.Reason, rec.Gate,
			champRec.Status, champRec.Outcome, champRec.Reason, champRec.Gate)
	}
	if strings.Contains(rec.Reason, "no commits") {
		t.Fatalf("the arm's record was re-derived from the checkout: %q", rec.Reason)
	}
	if got := publishes(shadow.harness); len(got) != 0 {
		t.Fatalf("a challenger arm called the provider: %v", got)
	}
}

// TestPatchRedacted: the patch object is redacted, both arms' alike. An agent
// may commit a value the run redacts (SECURITY.md says a run cannot be
// stopped from it), a challenger's diff reaches no remote where anything else
// would scan it, and a pair's judge run fetches exactly this object (A5.1);
// the same pass for both arms keeps the two diffs comparable.
func TestPatchRedacted(t *testing.T) {
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, `echo "key = $ANTHROPIC_API_KEY" > leak.txt && git add -A && git commit -qm leak`)
		verifyTest(t, ctx, req)
		return agent.Result{CostUSD: 1}, nil
	}
	for _, tc := range []struct {
		name string
		spec *task.Spec
	}{
		{"challenger", armTask(challengerModel)},
		{"champion", champTask()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := armGWSpec(t, armCfg(t), "50", tc.spec, armLayer)
			rec, err := g.run(t, leak, review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			patch := readPatch(t, g)
			if strings.Contains(patch, plantedKey) {
				t.Fatalf("the stored patch carries the value the run redacts:\n%s", patch)
			}
			if !strings.Contains(patch, "leak.txt") {
				t.Fatalf("the stored patch is not the run's own diff:\n%s", patch)
			}
			if rec.PatchObject == "" {
				t.Fatal("the run recorded no patch object")
			}
		})
	}
}

// armFollowCfg is the arm fixture's configuration with the follow-up block
// its second run reads.
func armFollowCfg(t *testing.T) string {
	t.Helper()
	return armCfg(t) + "followup:\n  trusted: [\"" + aliceID + "\", \"" + bobID + "\"]\n"
}

// armFollowupHarness is a harness whose first run (runID) opened PR 1
// through the gateway, with the arm's provider: the state a follow-up of that
// pull request starts from.
func armFollowupHarness(t *testing.T, cfg string) (*gw, *fuHarness) {
	t.Helper()
	h := newHarnessFiles(t, cfg, withLayer(armTask(""), armLayer), nil)
	h.provider.SelfID = fugaroSelf
	statePath := filepath.Join(t.TempDir(), "fake.json")
	h.provider.Path = statePath
	up, srv := anthropicfake.New(t)
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	env := map[string]string{runner.BudgetModeEnv: "enforce", runner.MaxRunUSDEnv: "50"}
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

	first, err := g.run(t, commitOnly("feature"), review("ship", 0))
	if err != nil || first.PR == nil || first.PR.Number != 1 || first.PushedHead == "" {
		t.Fatalf("first run: rec = %+v, err = %v", first, err)
	}
	return g, &fuHarness{harness: h, statePath: statePath, first: first}
}

// armFollowup stores a challenger arm's task as a follow-up of PR 1, as the
// follow-up harness's own followUp does, with this task's ab block.
func (h *fuHarness) armFollowup(t *testing.T, id, text string, ab *task.AB) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: h.first.Workflow,
		Task: text, Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID, AB: ab,
		ProjectLayer: &task.ProjectLayer{SHA256: config.LayerSum([]byte(armLayer)), Generation: 3, YAML: armLayer}}
	store := runstore.Open(h.bucket, repoSlug, id)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	h.store, h.deps.Store = store, store
	for _, dir := range []string{h.deps.WorkDir, h.deps.StateDir} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	h.deps.Env = append(filterEnv(h.deps.Env, "HOME"), "HOME="+t.TempDir())
	h.agent = &scriptedAgent{t: t}
	h.deps.Agent = h.agent
}

// TestShadowFollowupHaltWritesNoStatus: a governance halt of a challenger arm
// that is a follow-up writes nothing on the pull request its task names. The
// halt path reaches the status write without a pull request of the arm's own
// — a follow-up's bootstrap records one — so the guard belongs at the write
// itself, as it is for a push, a draft and the early draft.
func TestShadowFollowupHaltWritesNoStatus(t *testing.T) {
	g, fu := armFollowupHarness(t, armFollowCfg(t))
	fu.armFollowup(t, followID, "Please rename the helper to fooBar.",
		&task.AB{PairID: "pair-" + followID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
			Challenger: challengerModel, ChallengerSource: task.ABChallengerOwner})

	runner.ResetStageHook(t)
	if err := runner.SetStageHook(runner.StageFuncs{
		Timeout: func(runner.StageView) time.Duration { return 300 * time.Millisecond },
		TimedOut: func(runner.StageView) runner.StageDecision {
			return runner.StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}
		},
	}); err != nil {
		t.Fatal(err)
	}
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		<-ctx.Done()
		return agent.Result{}, ctx.Err()
	}

	pubs := func() []string { return publishes(g.harness) }
	before := len(pubs())
	rec, err := g.run(t, long)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil {
		t.Fatalf("record = %+v, want a halt", rec)
	}
	if rec.Checkpoint == nil {
		t.Fatal("the halt did not go through the governance path, so nothing wrote a status section")
	}
	if rec.AB == nil || rec.AB.Arm != task.ABArmChallenger {
		t.Fatalf("the follow-up did not run as the arm it was given: %+v", rec.AB)
	}
	if rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("the follow-up recorded no pull request: %+v", rec.PR)
	}
	if got := pubs(); len(got) != before {
		t.Fatalf("a halted challenger follow-up called the provider: %v", got[before:])
	}
}

// TestShadowReviewRunPostsNothing: a review run's whole output is the comment
// it writes on the pull requests it reviews, so a task that is both a review
// run and a challenger arm could keep its promise only by doing nothing. It
// is refused at bootstrap, before the provider is opened, and the pull request
// it names is never written to (D554).
//
// Mutation (drop the review-run refusal in readArm): the challenger posts its
// verdict on PR 7 and this test fails.
func TestShadowReviewRunPostsNothing(t *testing.T) {
	spec := reviewTaskSpec("reviewonly", reviewOnlyYAML, 7)
	spec.AB = armTask(challengerModel).AB
	g, _ := armGWSpec(t, armCfg(t), "50", spec, armLayer)
	pushReviewBranch(t, g.harness, "feature", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(g.harness, 7, "feature", "")
	rec, _ := g.run(t, review("changes", 1))
	if got := publishes(g.harness); len(got) != 0 {
		t.Fatalf("a challenger review run wrote to the forge: %v", got)
	}
	if c := g.harness.provider.State.PRs[0].Comments; len(c) != 0 {
		t.Fatalf("a challenger review run commented on PR 7: %q", c)
	}
	if rec == nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want the arm refused", rec)
	}
}

// TestShadowNeverCheckpoints: checkpoints push mid-stage through a path of
// their own (pushCheckpoint), not through pushBranch, so the arm's one gate
// on it is checkpointsOn: a challenger with checkpoints configured starts no
// checkpointer and pushes nothing, however long its stage holds a commit.
//
// Mutation (drop !r.shadow from checkpointsOn): a checkpointer runs during
// the challenger's stage, the settled tip is pushed, and this test fails.
func TestShadowNeverCheckpoints(t *testing.T) {
	tick := runner.DriveCheckpoints(t)
	cfg := strings.Replace(armPRCfg(t, 2, ", early_draft: false"), "total: 5m", "total: 1h", 1)
	g, _ := armGW(t, cfg, "50", challengerModel, armLayer)
	c := newClock()
	g.harness.deps.Now = c.Now
	pushes := countPushes(t, g.harness)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpoint goroutine(s) during a challenger's stage, want none", n)
			tick()
			c.advance(runner.CheckpointMinGap)
			tick()
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("the remote accepted %d push(es) from a challenger with checkpoints on", n)
	}
	if remoteHasBranch(t, g.harness) {
		t.Fatal("the run branch reached the remote")
	}
}
