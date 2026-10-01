package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// VertexBudgetRefusal is what a Vertex workflow under a budget in enforce
// mode is told: the gateway can't yet be relied on to cap Vertex spend.
// fugaro validate, fugaro init --repo and the runner's bootstrap all say it.
const VertexBudgetRefusal = "Vertex budgets are not supported yet: use budget.mode observe or off for this repository (see docs/gcp-live-checklist.md, check 20)"

// haltGrace is how long a halted stage's agent gets to exit on its own
// before it is stopped. A var so a test can shorten it.
var haltGrace = 60 * time.Second

// gatewayCloseTimeout bounds closing the gateway after the last stage.
const gatewayCloseTimeout = 10 * time.Second

// allowRoutingSettingsEnv is the live test's knob: it skips the refusal of
// routing settings, so check 20 can measure what Claude Code does with
// them. It works only on a local run against the real API.
const allowRoutingSettingsEnv = "FUGARO_TEST_ALLOW_ROUTING_SETTINGS"

// vertexRegionPrefix starts Claude Code's per-model Vertex region overrides.
const vertexRegionPrefix = "VERTEX_REGION_"

// gatewayOn reports whether the agent's calls go through the gateway: the
// budget is on and the credential is a model API's (an oauth run never is).
func (r *run) gatewayOn() bool {
	return r.cfg != nil && r.spend.On() && (r.cfg.Agent.Auth == "api-key" || r.cfg.Agent.Auth == "vertex")
}

// gateway is where the agent's model calls go, nil until the gateway runs.
func (r *run) gateway() *agent.Gateway { return r.gwAgent }

// startGateway starts the run's gateway and makes the agent's environment
// point at it. The real credential stays here.
func (r *run) startGateway(ctx context.Context) error {
	s := r.spend
	o := gateway.Options{Prices: s.Prices, Cap: s.Cap, Log: r.d.Log, EndStageWait: r.d.GatewayStageWait}
	o.Mode = gateway.Observe
	if s.Mode == "enforce" {
		o.Mode = gateway.Enforce
	}
	up := gateway.Upstream{BaseURL: r.d.GatewayUpstream}
	switch r.cfg.Agent.Auth {
	case "api-key":
		up.Kind, up.APIKey = "anthropic", envLookup(r.d.Env, "ANTHROPIC_API_KEY")
		r.addSecret(up.APIKey)
	case "vertex":
		up.Kind = "vertex"
		up.VertexProject = envLookup(r.d.Env, "ANTHROPIC_VERTEX_PROJECT_ID")
		up.VertexLocations = vertexLocations(r.d.Env)
		switch {
		case r.d.VertexTokens != nil:
			up.Token = r.d.VertexTokens
		case r.d.GatewayUpstream != "":
			// A test upstream must never be paired with real credentials.
			return errors.New("starting the gateway: a test upstream needs a test token source")
		default:
			ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				return fmt.Errorf("starting the gateway: Vertex credentials: %w", err)
			}
			up.Token = ts
		}
	default:
		return fmt.Errorf("starting the gateway: auth %q never goes through it", r.cfg.Agent.Auth)
	}
	o.Upstream = up
	gw, err := gateway.Start(ctx, o)
	if err != nil {
		return fmt.Errorf("starting the gateway: %s", r.redact(err.Error()))
	}
	r.addSecret(gw.Token())
	r.mu.Lock()
	r.gw = gw
	r.gwAgent = &agent.Gateway{BaseURL: gw.URL(), Token: gw.Token()}
	r.modelBy = map[string]pricing.Micros{}
	r.stageExtra = r.endGatewayStage
	r.mu.Unlock()
	return nil
}

// vertexLocations are the locations a Vertex call may name: the job's
// region and every per-model region override.
func vertexLocations(env []string) []string {
	var out []string
	add := func(l string) {
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	add(envLookup(env, "CLOUD_ML_REGION"))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, vertexRegionPrefix) {
			add(v)
		}
	}
	return out
}

// beginGatewayStage tells the gateway which models the stage may use.
func (r *run) beginGatewayStage(name string) {
	role := config.StageRole(name)
	a := r.cfg.Agent
	r.gw.BeginStage(gateway.Stage{Name: name, Model: a.ModelFor(role), Background: a.Models.Background, MaxOutputTokens: a.MaxOutputFor(role)})
}

// endGatewayStage closes the stage in the gateway, takes its figures into
// the run's, and says what it refused: violations first, then tokens.
func (r *run) endGatewayStage(stage string) ([]string, int64) {
	rep := r.gw.EndStage()
	r.mu.Lock()
	r.gwUsed += rep.Used
	r.lastStage = &rep
	for m, v := range rep.ByModel {
		r.modelBy[m] += v
	}
	r.unreconciled += rep.Unreconciled
	r.unparsed += rep.UsageUnparsed
	r.mu.Unlock()
	log := r.d.Log.With("stage", stage)
	if rep.UsageUnparsed > 0 {
		log.Warn(fmt.Sprintf("usage_unparsed: %d", rep.UsageUnparsed))
	}
	if rep.WouldHalt > 0 {
		log.Warn("budget: calls the cap would have refused", "calls", rep.WouldHalt)
	}
	return rep.Violations, rep.Tokens
}

// lastWaited is how many calls the gateway told to retry in the stage
// that just ended.
func (r *run) lastWaited() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastStage == nil {
		return 0
	}
	return r.lastStage.Waited
}

// crossCheckCost warns when Claude Code's own figure for a stage and the
// gateway's differ by more than 5%. A stage that was stopped (by a halt or
// a cancel or its deadline) never got to report its cost, so its figure
// says nothing and nothing is compared.
func (r *run) crossCheckCost(stage string, claude float64, stopped bool) {
	r.mu.Lock()
	rep := r.lastStage
	r.mu.Unlock()
	if rep == nil || stopped {
		return
	}
	gw := rep.Used.USD()
	if larger := max(claude, gw); larger > 0 && abs(claude-gw) > 0.05*larger {
		r.d.Log.Warn("cost cross-check", "stage", stage, "claude_code_usd", claude, "gateway_usd", gw)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// watchHalt waits for the gateway's halt while a stage runs. On a halt it
// records it at once, then gives the agent haltGrace to exit by itself
// (done closes) before stopping the stage.
func (r *run) watchHalt(stageCtx context.Context, done <-chan struct{}) {
	select {
	case gh, ok := <-r.gw.Halted():
		if !ok || gh.Reason == "" {
			return // delivered before
		}
		h := gatewayHalt(gh)
		if !r.haltNow(h) {
			return // a cancel came first, and stops the stage itself
		}
		t := time.NewTimer(haltGrace)
		defer t.Stop()
		select {
		case <-done:
		case <-stageCtx.Done():
		case <-t.C:
			r.cancelHaltedStage(h)
		}
	case <-done:
	case <-stageCtx.Done():
	}
}

// drainHalt records a halt the watcher did not get to, once the stage's
// agent has returned.
func (r *run) drainHalt() {
	select {
	case gh, ok := <-r.gw.Halted():
		if ok && gh.Reason != "" {
			r.haltNow(gatewayHalt(gh))
		}
	default:
	}
}

func gatewayHalt(gh gateway.Halt) runstore.Halt {
	return runstore.Halt{Reason: runstore.HaltReason(gh.Reason), Scope: "run", At: gh.At.UTC(), Detail: gh.Detail}
}

// closeGateway stops the gateway, once, and takes the run's model cost from
// its ledger: the figure includes a call that settled after its stage's
// wait ran out. It also removes the managed settings file the run wrote.
func (r *run) closeGateway() {
	defer r.removeManagedSettings()
	r.mu.Lock()
	gw, closed := r.gw, r.gwClosed
	r.gwClosed = true
	r.mu.Unlock()
	if gw == nil || closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gatewayCloseTimeout)
	defer cancel()
	if err := gw.Close(ctx); err != nil {
		r.d.Log.Warn("closing the gateway failed", "err", r.redact(err.Error()))
	}
	led := gw.Ledger()
	r.mu.Lock()
	r.gwUsed = led.Used
	r.mu.Unlock()
	r.rec.CostUSD = led.Used.USD()
	r.updateCost()
}

// routingKnob reports whether the live test's knob skips the
// routing refusal. On Cloud Run, or against a test upstream, it never does.
func (r *run) routingKnob() bool {
	if envLookup(r.d.Env, allowRoutingSettingsEnv) != "1" {
		return false
	}
	if backend.OnCloudRun(func(k string) string { return envLookup(r.d.Env, k) }) {
		if !r.warnedKnob {
			r.warnedKnob = true
			r.d.Log.Warn(allowRoutingSettingsEnv + " is ignored on Cloud Run")
		}
		return false
	}
	return r.d.GatewayUpstream == ""
}

// checkManagedDirWritable is the gateway's precondition: Claude Code's
// managed settings are what keep a repository's own settings from
// rerouting the agent, so a directory that can't hold them fails the run.
func (r *run) checkManagedDirWritable() error {
	dir := filepath.Dir(r.managedPath())
	f, err := os.CreateTemp(dir, agent.ManagedSettingsTmpPattern)
	if err != nil {
		return fmt.Errorf("the image can't hold Claude Code's managed settings (%v); rebuild it on an M9a base image", err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// checkBudget is bootstrap's budget gate, after the project check and
// before the lock: a Vertex run that enforces is refused (a misconfiguration,
// so an infra_error), an api-key run that enforces without a cap halts (a
// policy, so a halt), and a run through the gateway needs prices for its
// models and a managed settings directory it can write.
func (r *run) checkBudget() error {
	auth, s := r.cfg.Agent.Auth, r.spend
	if auth == "vertex" && s.Mode == "enforce" {
		return errors.New(VertexBudgetRefusal)
	}
	if auth == "api-key" && s.Mode == "enforce" && s.Cap <= 0 {
		r.haltNow(runstore.Halt{Reason: runstore.HaltNoCap, Scope: "run", At: r.d.Now().UTC(),
			Detail: "budget.mode is enforce but no per-run cap is set: set budget.per_run_usd in the project config and run fugaro init --repo"})
		return &HaltError{*r.haltValue()}
	}
	// The allow-list bounds the models whatever the credential or the mode.
	if ps := config.CheckAllowed(r.cfg.Agent, r.policy); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return fmt.Errorf("the models are not all allowed: %s", strings.Join(msgs, "; "))
	}
	if !r.gatewayOn() {
		return nil
	}
	if ps := config.CheckPins(r.cfg.Agent, s.Prices); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return fmt.Errorf("the models can't be priced under the budget: %s", strings.Join(msgs, "; "))
	}
	if err := r.checkManagedDirWritable(); err != nil {
		return err
	}
	if reason := r.checkSettingsRouting(); reason != "" {
		return errors.New(reason)
	}
	return nil
}
