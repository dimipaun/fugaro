package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// VertexBudgetRefusal is what a Vertex workflow under a budget in enforce
// mode is told: the gateway can't yet be relied on to cap Vertex spend.
// fugaro validate, fugaro update and the runner's bootstrap all say it.
const VertexBudgetRefusal = "Vertex budgets are not supported yet: use budget.mode observe or off for this repository (see docs/gcp-live-checklist.md, check 20)"

// coderPinPath is where config's own checks point a problem with the coder
// pin: the first of internal/config's pinPaths, the string every
// agent.models.* problem carries in its Path. Restated here because config
// keeps those paths unexported, and pinned by the arm tests that name a coder
// the run's policy refuses (shadow_test.go).
const coderPinPath = "agent.models.coder"

// modelRefusal is the class of checkBudget failure the run's model policy
// raises — a model this run may not run, or cannot price — rather than
// infrastructure failing under it, and it carries whether the coder pin was
// one of the models refused. The coder is the one pin an A/B arm moves
// (D555): a challenger arm's own model refused is the arm's own refusal
// (D556), while the pins both arms share, and every infrastructure failure
// (a Vertex budget, a managed settings directory, a price table), are the
// champion's conditions too and are never recorded as the arm's decision.
type modelRefusal struct {
	msg      string
	coderPin bool
}

func (e *modelRefusal) Error() string { return e.msg }

// namesCoderPin reports whether the coder pin is one of the models a
// model-policy check refused.
func namesCoderPin(ps []config.Problem) bool {
	return slices.ContainsFunc(ps, func(p config.Problem) bool { return p.Path == coderPinPath })
}

// refusal joins a model-policy check's problems into checkBudget's error, of
// the class above. what is the sentence the run reports them under, exactly
// as checkBudget has always reported them.
func refusal(what string, ps []config.Problem) *modelRefusal {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return &modelRefusal{msg: what + ": " + strings.Join(msgs, "; "), coderPin: namesCoderPin(ps)}
}

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

// credentialsServiceURLEnv is where a job learns the URL of its
// installation's credentials service (C1.15, D146/D147): the installation's
// own Cloud Run service, which the owner opts into and which the job is
// given an invoker grant on (C1.18). It is set on the job only when the
// installation has the service on, so its presence is the "the service is
// on" fact a cascade run is built on, and its absence is what refuses one
// (rather than a run reaching for a service that is not there).
const credentialsServiceURLEnv = "FUGARO_CREDENTIALS_SERVICE_URL"

// metadataHostEnv and the metadata path below are the job's own identity:
// the Google ID token the metadata server mints for the credentials
// service's URL, which Cloud Run IAM admits for run.invoker (D135). The host
// variable is the platform's own knob (google.golang.org's client libraries
// read the same one for the same reason, and the job's env is where the
// platform puts it), so a test can point this at a stand-in without any
// test-only switch in this file.
const (
	metadataHostEnv      = "GCE_METADATA_HOST"
	defaultMetadataHost  = "metadata.google.internal"
	metadataIdentityPath = "/computeMetadata/v1/instance/service-accounts/default/identity"
	// maxIDTokenBytes bounds a token this job will present, and is
	// internal/credsvc's own bound on one it will read (auth.go's
	// maxTokenBytes): a token longer than it, or one that is not a single
	// clean value, is refused here rather than sent to be refused there.
	maxIDTokenBytes = 8 << 10
)

// metadataClient is the client the metadata request is made with: a bound on
// the whole exchange, because the metadata server is the platform's own
// link-local address and an answer that takes longer than this is not one
// this run waits for. (Nothing is retried: the gateway never retries, and a
// call that cannot present its identity fails like any other upstream
// error.)
var metadataClient = &http.Client{Timeout: 10 * time.Second}

// gatewayOn reports whether the agent's calls go through the gateway. It is
// GatewayOn — the same predicate for the readers that hold a mode and an auth
// rather than a run — read off the run, and it is the only place that happens:
// the routing switch's own question (RoutingInertReason) and the gateway's
// rules all ask one function, so no reader can answer it differently.
func (r *run) gatewayOn() bool {
	return r.cfg != nil && GatewayOn(r.cfg.Agent.Auth, r.spend.Mode)
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
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess != nil {
		// The lease is the judge: it refuses a cap only when the project's
		// mode, or this run's own, enforces it, and it never refuses one in
		// observe. The gateway enforces whatever the lease refuses, so a
		// project switched to enforce mid-run stops this run too, rather
		// than leaving its calls waiting.
		o.Mode, o.Lease = gateway.Enforce, sess.Lease()
	}
	up, err := r.gatewayUpstream(ctx)
	if err != nil {
		return err
	}
	o.Upstream = up
	// The service's answer reaches the run here (C1.15): the typed
	// `exhausted_until` is what ends the stage in flight and starts the wait
	// (C1.17, D138). The same answers carry the tier, the credential's name
	// and the switch, which C1.16's ledger and C1.19's record consume; this
	// is the one hand-off C1.15 owns, and an answer that is not the exhausted
	// one is left alone rather than guessed at here.
	o.OnServiceAnswer = r.serviceAnswer
	routes, err := r.gatewayRoutes()
	if err != nil {
		return errors.New(r.redact(err.Error()))
	}
	o.Routes = routes
	r.routed = len(routes) > 0
	gw, err := gateway.Start(ctx, o)
	if err != nil {
		return fmt.Errorf("starting the gateway: %s", r.redact(err.Error()))
	}
	r.addSecret(gw.Token())
	if sess != nil {
		sess.SetUsage(func() pricing.Micros { return gw.Ledger().Used })
	}
	r.mu.Lock()
	r.gw = gw
	r.gwAgent = &agent.Gateway{BaseURL: gw.URL(), Token: gw.Token()}
	r.modelBy = map[string]pricing.Micros{}
	r.routeBy = map[string]pricing.Micros{}
	r.stageExtra = r.endGatewayStage
	r.mu.Unlock()
	return nil
}

// gatewayUpstream is where this run's gateway sends its calls and with what
// credential: the run's own auth, spelled once. startGateway builds its
// options from it, and the two are separate so that what a run does with
// each auth is one function a reader -- and a test -- can ask without
// starting a gateway.
//
// The auths are the three the gateway serves. api-key and vertex are the
// job's own credential (the run's key, or the job's own Vertex identity);
// cascade is the credentials service (C1.15, service.go), which holds the
// credential and attaches it per call, so the job has none and must never
// reach for one (D143). An auth with no arm is refused rather than guessed
// at: the gateway is the only thing between the agent and a model, and an
// upstream nobody chose is a credential nobody chose.
func (r *run) gatewayUpstream(ctx context.Context) (gateway.Upstream, error) {
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
			return gateway.Upstream{}, errors.New("starting the gateway: a test upstream needs a test token source")
		default:
			ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				return gateway.Upstream{}, fmt.Errorf("starting the gateway: Vertex credentials: %w", err)
			}
			up.Token = ts
		}
	case config.AuthCascade:
		return r.serviceUpstream()
	default:
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: auth %q never goes through it", r.cfg.Agent.Auth)
	}
	return up, nil
}

// cascadeLocalRefusal is D149's refusal of a cascade auth in a local run, kept
// in one place because two doers say it: the service upstream below, and the
// routing switch's own arm in bootstrap (routing.go's applyRouting), which
// judges the auth before a gateway is ever started.
//
// The second caller is why this is not just a line in serviceUpstream: that
// function is reached only through the gateway, and only for a run whose mode
// turns the gateway on — while a local `fugaro exec` is usually mode-off or
// mode-unset, so its cascade file was answered by agent.BuildEnv instead
// ("agent.auth cascade needs..."), and a local run whose mode *is* on was
// refused for the switch, with the operator's remedy (publish the policy),
// which no local invocation can be fixed by: `fugaro exec` never cascades.
// The local invocation's own sentence is the one that names the fix — use
// api-key or oauth locally — so applyRouting says this before it judges the
// switch at all.
func cascadeLocalRefusal() error {
	return fmt.Errorf("agent.auth is %s and this is a local run: %s runs on the developer's own credentials and never through the credentials service (D149). Use agent.auth api-key or oauth locally", config.AuthCascade, execName)
}

// serviceUpstream is the credentials service's upstream for a cascade run
// (C1.15, D135): the service's URL from the job's environment, and the two
// identities every call carries -- the job's Google ID token, minted from
// the metadata server for the service's own URL, and the run's own Firebase
// ID token. No credential of the job's is anywhere in it: the whole point of
// the cascade is that the job holds none (D143).
//
// Everything it refuses, it refuses before a call is made, because each of
// these is a run that cannot be served at all:
//
//   - a local run. `fugaro exec` is unchanged by the cascade (D149): it uses
//     the developer's own environment, has no installation identity and no
//     network path to a private service, and gains nothing by being routed
//     through one. A cascade auth locally is a mistake in the file, said so
//     rather than reached for.
//   - a job with no credentials service. The installation has not opted in
//     (D147), so there is no service to call and no credential anywhere this
//     run may use.
//   - a run with no budget identity. The run's own token is what the service
//     verifies the run with (C1.10); without it the service can only refuse,
//     and a cascade run is always admitted by the budget backend (C1.19), so
//     this is a fact about how the run was started and not about the service.
//   - a test upstream. Deps.GatewayUpstream redirects the api-key and vertex
//     kinds to a stand-in; a cascade run's destination is the installation's
//     own service and nothing else, so a test that set both is told rather
//     than silently sent to the service.
//
// The job's name is read the way the rest of the run reads it (jobName:
// from the platform's own report of the execution, never from an environment
// a launcher can write). It is a claim the service holds to the job-name
// rule and decides nothing from -- which job called is decided from the
// verified service-account email (C1.10) -- and the gateway refuses a
// service upstream without a valid one, so a run that cannot name its job
// stops here.
func (r *run) serviceUpstream() (gateway.Upstream, error) {
	if r.d.Local {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: %w", cascadeLocalRefusal())
	}
	url := envLookup(r.d.Env, credentialsServiceURLEnv)
	if url == "" {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: agent.auth is %s and this job has no credentials service: %s is not set, so the installation has not opted into one (D147) and this run has no credential of its own to fall back to (D143)", config.AuthCascade, credentialsServiceURLEnv)
	}
	if r.d.GatewayUpstream != "" {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: a cascade run's upstream is the installation's own credentials service (%s), not a test upstream", credentialsServiceURLEnv)
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return gateway.Upstream{}, errors.New("starting the gateway: a cascade run has no run identity: the budget backend is what admitted it, and the credentials service verifies the run by the identity that admitted it")
	}
	job, err := r.jobName()
	if err != nil {
		return gateway.Upstream{}, errors.New(r.redact(err.Error()))
	}
	if job == "" {
		// Belt and braces: jobName's own contract means this cannot fire
		// today -- the only run it answers "" for is a local one, refused
		// above -- but an upstream that reports no job is one the gateway
		// refuses anyway (validate), and a run that got here with none
		// should stop where the reason is known rather than there.
		return gateway.Upstream{}, errors.New("starting the gateway: a cascade run has no Cloud Run job name to report")
	}
	return gateway.Upstream{
		Kind:    gateway.KindService,
		BaseURL: url,
		Service: &gateway.Service{
			Job:        job,
			RunIDToken: sess.IDToken(),
			IDToken:    func(ctx context.Context) (string, error) { return metadataIDToken(ctx, r.d.Env, url) },
		},
	}, nil
}

// execName is what a local invocation is called, for the sentences that have
// to name it.
const execName = "fugaro exec"

// serviceAnswer is where the credentials service's answer reaches the run
// (C1.15's hand-off to the runner, C1.17's reaction to it). The gateway calls
// it from its request path, once per answer, from the calls of every stage
// that is in flight, so it takes no lock of its own beyond noteExhausted's
// and does nothing a caller could block on.
//
// Two answers are acted on. The exhausted one is a decision about the stage in
// flight: the stage ends -- it did not fail -- and the runner waits for the
// reset and resumes it (C1.17, D138). The other is the name of the credential
// that answered (C1.19): a cascade run is the one run whose credential is not
// its own, so it is the one run that has to record which one it spent on --
// the record `/runs/<id>/result.json` carries (D140) and the registry entry
// the dashboard reads (C1.3). Both are written by r.recordCredential
// (runner.go, with the run's other record writers) and decided here, where the
// answer arrives, because a second reader of the same contract is how two
// records come to disagree. The tier a call settled by and the switch are
// C1.16's ledger.
//
// The typed answer is turned back into the contract's own type
// (credsvc.ExhaustedUntil) rather than a runner type of its own: the wire
// contract is the only spelling of it, and noteExhausted's caller in a test
// hands it the same shape.
func (r *run) serviceAnswer(a gateway.ServiceAnswer) {
	if a.Credential != "" {
		r.recordCredential(a.Credential)
	}
	if a.Switch != "" {
		// The switch the answer carries (C1.16, D140): the run's log of what
		// the cascade moved and why, with the cache-miss estimate the gateway
		// computed for that move.
		r.recordSwitch(a.Switch, a.SwitchCost)
	}
	if a.Exhausted == nil {
		return
	}
	tried := make([]credsvc.Tried, 0, len(a.Exhausted.Tried))
	for _, tr := range a.Exhausted.Tried {
		tried = append(tried, credsvc.Tried{Name: tr.Name, Tier: tr.Tier, Reason: tr.Reason})
	}
	r.noteExhausted(credsvc.ExhaustedUntil{ResetAt: a.Exhausted.ResetAt, Tried: tried})
}

// metadataIDToken mints the job service account's Google ID token for
// audience from the metadata server: the credential Cloud Run IAM admits for
// run.invoker on the credentials service (D135, F30). The audience is the
// service's own URL, which is the whole of the check Cloud Run makes.
//
// It is minted per call rather than cached. The metadata server is the
// platform's own link-local endpoint, so the round trip is a millisecond
// against a model call's seconds; what a cache would buy is a token this run
// keeps using after the platform stopped issuing it -- a revoked service
// account, a job whose identity changed -- and what the run needs is the
// shortest-lived credential the platform will give it.
//
// The body is never read into an error and never logged: on success it is
// the token, and on failure it is the metadata server's own text about a
// request this code made. Only the status is reported.
func metadataIDToken(ctx context.Context, env []string, audience string) (string, error) {
	u := metadataIdentityURL(env, audience)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", errors.New("the metadata request could not be built")
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := metadataClient.Do(req)
	if err != nil {
		return "", errors.New("the metadata server could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the metadata server answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIDTokenBytes+1))
	if err != nil {
		return "", errors.New("the metadata server's answer could not be read")
	}
	tok := strings.TrimSpace(string(b))
	// One clean header value, at the bound the service reads a presented
	// token by: a token with a newline in it, or one longer than that bound,
	// is refused here rather than sent to be refused there.
	if len(tok) > maxIDTokenBytes || !printableToken(tok) {
		return "", errors.New("the metadata server answered with no usable identity token")
	}
	return tok, nil
}

// metadataIdentityURL is the metadata server's own endpoint for this job's
// identity: the link-local name the platform serves it at unless the job's
// environment says otherwise (GCE_METADATA_HOST, which the platform sets on
// its own hosts and which is therefore the only thing that may move it), the
// instance's default service account, and the audience Cloud Run IAM checks.
// It is separate from the request so that where the token is asked for is one
// expression a reader -- and a test -- can see without a call.
func metadataIdentityURL(env []string, audience string) string {
	host := envLookup(env, metadataHostEnv)
	if host == "" {
		host = defaultMetadataHost
	}
	return "http://" + host + metadataIdentityPath + "?audience=" + url.QueryEscape(audience)
}

// printableToken reports whether tok is one clean header value: non-empty and
// printable ASCII with no space (internal/credsvc's rule for a presented
// token, auth.go's maxTokenBytes and wire.go's printableHeaderValue).
func printableToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 0x21 || tok[i] > 0x7e {
			return false
		}
	}
	return true
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
	r.gw.BeginStage(r.stageSpec(name))
}

// stageSpec is the set the gateway admits while a stage runs: the model the
// stage's own pins name (stageModel — a review level's own model is its
// stage's, L4.5), the background model, and the stage's output limit per call.
// A set that named the role slot where the pins name a level's own model would
// refuse every call of the stage as not pinned for it.
func (r *run) stageSpec(name string) gateway.Stage {
	return gateway.Stage{
		Name:            name,
		Model:           r.stageModel(name),
		Background:      r.backgroundModel(),
		MaxOutputTokens: r.cfg.Agent.MaxOutputFor(stageRole(name)),
	}
}

// endGatewayStage closes the stage in the gateway, takes its figures into
// the run's, and says what it refused: violations first, then tokens.
func (r *run) endGatewayStage(stage string) ([]string, int64) {
	rep := r.gw.EndStage()
	r.mu.Lock()
	r.gwUsed += rep.Used
	r.gwNotional += rep.Notional
	r.lastStage = &rep
	for m, v := range rep.ByModel {
		r.modelBy[m] += v
	}
	for m, v := range rep.ByRoute {
		r.routeBy[m] += v
	}
	r.reported += rep.Reported
	r.unreconciled += rep.Unreconciled
	r.unparsed += rep.UsageUnparsed
	r.serviceTiers.Merge(rep.ServiceTiers)
	r.mu.Unlock()
	log := r.d.Log.With("stage", stage)
	if rep.UsageUnparsed > 0 {
		log.Warn(fmt.Sprintf("usage_unparsed: %d", rep.UsageUnparsed))
	}
	if rep.WouldHalt > 0 {
		log.Warn("budget: calls the cap would have refused", "calls", rep.WouldHalt)
	}
	// A response that named a service tier the table has no multiplier for
	// was charged the conservative multiple rather than failing the stage:
	// the run says which tiers, so an operator reading the log can tell a
	// provider's own tier from a price that is wrong (gateway.StageReport's
	// ServiceTiers, which the line prints as a record carries it).
	if tiers := rep.ServiceTiers.List(); len(tiers) > 0 {
		log.Warn("budget: service tiers charged conservatively", "tiers", strings.Join(tiers, ", "))
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

// recordGatewayStageCost is the run's own account of a gateway stage that has
// just ended: the gateway's settled figure is the run's model cost and Claude
// Code's own is only a cross-check against it (runner.go calls this where the
// stage's agent returned), and a halt the gateway raised after the stage's
// watcher looked is recorded now.
//
// The figure is the money and the notional part together, which is what
// record.CostUSD and cost.model_usd mean (C1.16, P12): the run's whole model
// spend, of which cost.ModelNotionalUSD says how much was the subscription's.
// The billed side is cost.TotalUSD, which WithNotional keeps money-only --
// a cascade run's tier-A dollars are in the model figure and in no total and
// against no cap (D140).
func (r *run) recordGatewayStageCost(stage string, claudeUSD float64, stopped bool) {
	r.drainHalt()
	r.crossCheckCost(stage, claudeUSD, stopped)
	r.mu.Lock()
	r.rec.CostUSD = (r.gwUsed + r.gwNotional).USD()
	r.mu.Unlock()
}

// crossCheckCost warns when Claude Code's own figure for a stage and the
// gateway's differ by more than 5%. A stage that was stopped (by a halt or
// a cancel or its deadline) never got to report its cost, so its figure
// says nothing and nothing is compared.
//
// The gateway's side is its whole model figure -- the money and the notional
// part together -- because that is what Claude Code's own total_cost_usd
// counts: it prices every call the stage made, whatever credential paid for
// it, and a cascade stage's tier-A calls are in it (C1.16, F25). Comparing the
// billed part alone against it would read every cascade stage as a 100%
// disagreement and bury the cross-check's real finding.
func (r *run) crossCheckCost(stage string, claude float64, stopped bool) {
	r.mu.Lock()
	rep := r.lastStage
	r.mu.Unlock()
	if rep == nil || stopped {
		return
	}
	gw := (rep.Used + rep.Notional).USD()
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
	scope := gh.Scope
	if scope == "" {
		scope = "run"
	}
	return runstore.Halt{Reason: runstore.HaltReason(gh.Reason), Scope: scope, At: gh.At.UTC(), Detail: gh.Detail}
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
	// The ledger's settled totals are the run's, and they are the ones
	// r.gwUsed and r.gwNotional keep: the money its calls paid and the
	// subscription part of them (C1.16, D140). The stages' own reports are
	// what the splits (model_by, route_by) are built from.
	r.gwUsed, r.gwNotional = led.Used, led.Notional
	r.gwCacheRead, r.gwCacheWrite = led.CacheReadTokens, led.CacheWriteTokens
	r.mu.Unlock()
	r.rec.CostUSD = (led.Used + led.Notional).USD()
	r.updateCost()
	r.warnReportedGap()
}

// routingKnob reports whether the live test's knob skips the
// routing refusal. In the cloud, or against a test upstream, it never
// does: Deps.Local is the CLI's answer and its zero value is the cloud
// (design compute-backend.md §2.5), so a platform no backend claims
// refuses the knob too. The warning still says Cloud Run, the only cloud
// platform that can reach it today.
func (r *run) routingKnob() bool {
	if envLookup(r.d.Env, allowRoutingSettingsEnv) != "1" {
		return false
	}
	if !r.d.Local {
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
	// With the backend the caps are the database's too, and the session has
	// already judged them (Admit): an enforcing run with none halted there.
	if auth == "api-key" && s.Mode == "enforce" && s.Cap <= 0 && r.sess == nil {
		r.haltNow(runstore.Halt{Reason: runstore.HaltNoCap, Scope: "run", At: r.d.Now().UTC(),
			Detail: r.noCapDetail()})
		return &HaltError{*r.haltValue()}
	}
	// A model another provider serves needs the owner's approval of this
	// repository and api-key auth, whatever the mode: refused before any
	// call, naming the rule.
	if err := r.checkProviderModels(); err != nil {
		return err
	}
	// The allow-list bounds the models whatever the credential or the mode. The
	// switch and the routes are the run's own: a route ID is admitted only
	// where the switch routes the model to it, which is the resolution
	// applyRouting performed a step ago (resolvePolicy).
	if ps := config.CheckAllowed(r.cfg.Agent, r.policy, r.ceilingAllowed(), r.routingSwitch(), r.modelProviders()); len(ps) > 0 {
		return refusal("the models are not all allowed", ps)
	}
	if !r.gatewayOn() {
		return nil
	}
	if ps := config.CheckPins(r.cfg.Agent, s.Prices); len(ps) > 0 {
		return refusal("the models can't be priced under the budget", ps)
	}
	for _, w := range config.PinWarnings(r.cfg.Agent, s.Prices) {
		r.d.Log.Warn("model pin: "+w.String(), "code", w.Code)
	}
	if catalogStale(s.Prices, r.cfg.Agent.PinnedModels()) {
		// One line per run, whatever the mode: the models this run is pinned
		// to are charged the dearest end of their tier's range, and
		// result.json says what was charged once the run is over (design §7,
		// P7). A stale row no pin names is not this run's business: the routes
		// may have materialized one, and a run that never calls it, or prices
		// it by hand, over-charges nothing.
		r.d.Log.Warn("budget: price catalog stale", "code", "catalog_stale")
	}
	if err := r.checkManagedDirWritable(); err != nil {
		return err
	}
	if reason := r.checkSettingsRouting(); reason != "" {
		return errors.New(reason)
	}
	return nil
}

// warnReportedGap logs, once, when the providers' own cost figures are more
// than reportedGapPct above what the gateway charged on the provider routes
// without the route fee, which providers don't report (the table price
// settles, except where the report floors an unpriced tier's charge up to the
// call's reservation, so a high report means a wrong price, a served-by
// fallback, or a tier report above that reservation; a low one only means
// Fugaro over-charged).
func (r *run) warnReportedGap() {
	r.mu.Lock()
	var charged pricing.Micros
	for name, v := range r.routeBy {
		// The provider reports the model's price, not Fugaro's route fee.
		charged += pricing.WithoutFee(v, r.modelProviders()[name].RouteFeePct)
	}
	reported := r.reported
	r.mu.Unlock()
	if reported <= 0 || float64(reported) <= float64(charged)*(1+float64(reportedGapPct)/100) {
		return
	}
	r.d.Log.Warn(fmt.Sprintf("reported cost: the providers reported $%.4f, more than %d%% over the $%.4f charged: check model_prices and the account's provider settings", reported.USD(), reportedGapPct, charged.USD()), "code", "reported_gap")
}

// reportedGapPct is the margin (percent) a provider's reported cost may
// exceed the charge by before the run warns.
const reportedGapPct = 10

// checkProviderModels is the owner's data policy for provider models
// (config.CheckProviderPolicy, config.CheckProviderAuth). The run's own
// allowed_models layer is the file's here; the merged list is CheckAllowed's.
func (r *run) checkProviderModels() error {
	var allowed []string
	if b := r.cfg.Budget; b != nil {
		allowed = b.AllowedModels
	}
	prices := r.spend.Prices
	if prices == nil {
		// The budget is off, so the ceiling loaded no table; the checks
		// below still judge the prices the run would charge, built the
		// same way.
		var err error
		if prices, err = r.priceTable(nil); err != nil {
			return fmt.Errorf("pricing the models: %w", err)
		}
	}
	ps := config.CheckProviderPolicy(r.cfg.Agent, allowed, r.spec.Repo, r.modelProviders(), prices)
	ps = append(ps, config.CheckProviderAuth(r.cfg.Agent, r.modelProviders())...)
	if len(ps) > 0 {
		return refusal("the models are not approved for this repository", ps)
	}
	// From here the pins are the run's: an unset background model becomes
	// the provider coder's, so that CheckPins, CheckAllowed, the gateway's
	// stage pins and Claude Code's haiku role all see the same one.
	bg := r.backgroundModel()
	mode := policy.ModeOff
	if r.gatewayOn() {
		mode = r.spend.Mode
	}
	if ps := config.CheckProviderGateway(r.cfg.Agent, bg, r.modelProviders(), mode); len(ps) > 0 {
		// The first problem's own sentence, as this check has always
		// reported; the class is what the caller reads.
		return &modelRefusal{msg: ps[0].Message, coderPin: namesCoderPin(ps)}
	}
	r.cfg.Agent.Models.Background = bg
	return nil
}

// noCapDetail says how to give an enforcing run a cap. Where the owner's
// ceiling asked for enforce, the cap belongs in the project config; where a
// repository's fugaro.yaml escalated the mode, either place will do.
func (r *run) noCapDetail() string {
	const base = "budget.mode is enforce but no per-run cap is set: set budget.per_run_usd in the project config and run fugaro update"
	switch r.policy.Sources[policy.KeyMode] {
	case policy.SourceCeiling:
		return base
	case policy.SourcePolicy:
		// The ceiling is the project's published policy: fugaro update copies
		// the local config's value into the jobs, and would not change what
		// this run read.
		return "budget.mode is enforce but no per-run cap is set: set budget.per_run_usd in the project's policy (" + projectpolicy.PathShared() + "), which is what the run enforces"
	}
	return "budget.mode is enforce (set in fugaro.yaml) but no per-run cap is set: set budget.per_run_usd in fugaro.yaml on the default branch, or in the project config and run fugaro update"
}

// ceilingAllowed is the owner's allow-list from the run's ceiling, nil when
// none was set: error messages name it next to the merged one. The ceiling is
// the job environment's copy of the config, or the project's published policy
// when it has one (resolvePolicy).
func (r *run) ceilingAllowed() []string { return r.ceiling.AllowedModels }
