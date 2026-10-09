package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The job environment variables that point the runner at the project's
// budget backend, set by fugaro init --firebase on every workflow job whose
// budget is not off (plan R10). Without RTDBURLEnv the runner is M9a's: a
// static cap in memory and no shared state.
const (
	RTDBURLEnv        = "FUGARO_RTDB_URL"
	FirebaseAPIKeyEnv = "FUGARO_FIREBASE_API_KEY"
	// BudgetGraceEnv shortens the D14 grace (a Go duration, at least 5 s and
	// at most the default 3 m): it exists for tests and can only tighten.
	BudgetGraceEnv = "FUGARO_BUDGET_GRACE"
)

// Backend is how the run reaches the project's budget database. The zero
// value is no backend.
type Backend struct {
	RTDBURL string
	APIKey  string
	// Grace is the D14 window; zero is budget.DefaultGrace.
	Grace time.Duration
	// Tune, for tests only, adjusts the session's configuration (the fakes'
	// URLs, short intervals).
	Tune func(*budget.Config)
	// HTTP, for tests only, serves the backend's calls.
	HTTP *http.Client
}

// On reports whether a backend is configured.
func (b Backend) On() bool { return b.RTDBURL != "" }

// BackendFromEnv reads the backend from the job's environment. loopbackOK
// admits a database on a loopback address (a local run against a fake or an
// emulator). A malformed value is an error, which the runner reports as an
// infra_error at bootstrap: never a silent "no backend".
func BackendFromEnv(lookup func(string) (string, bool), loopbackOK bool) (Backend, error) {
	var b Backend
	if v, ok := lookup(RTDBURLEnv); ok {
		if err := rtdb.ValidateURL(v, loopbackOK); err != nil {
			return Backend{}, fmt.Errorf("%s: %v", RTDBURLEnv, err)
		}
		b.RTDBURL = v
	}
	if v, ok := lookup(FirebaseAPIKeyEnv); ok {
		if strings.TrimSpace(v) != v || v == "" || strings.ContainsAny(v, " \t\r\n&?#/") {
			return Backend{}, fmt.Errorf("%s: it must be a Firebase web API key", FirebaseAPIKeyEnv)
		}
		b.APIKey = v
	}
	if v, ok := lookup(BudgetGraceEnv); ok {
		d, err := time.ParseDuration(v)
		if err != nil || d < budget.MinGrace || d > budget.MaxGrace {
			return Backend{}, fmt.Errorf("%s %q: it must be a duration between %s and %s", BudgetGraceEnv, v, budget.MinGrace, budget.MaxGrace)
		}
		b.Grace = d
	}
	return b, nil
}

// backendOn reports whether this run uses the budget backend: a backend is
// configured and the merged budget mode is observe or enforce. A run whose
// budget is off never touches it (D14 exempts off).
func (r *run) backendOn() bool { return r.d.Backend.On() && r.spend.On() }

// budgetHalt records a halt the session found and stops the run: the
// running stage is cancelled and the gateway refuses every call and cuts the
// calls in flight. It is ignored once the agent loop is over (a kill during
// finalize or writeback changes nothing), and loses to an earlier cancel or
// halt.
func (r *run) onBudgetHalt(h budget.Halt) {
	rh := runstore.Halt{Reason: runstore.HaltReason(h.Reason), Scope: string(h.Scope), At: r.d.Now().UTC(), Detail: h.Detail}
	if !r.haltExternal(rh) {
		r.d.Log.Info("budget: a halt arrived too late or after another cause; ignoring it", "reason", string(h.Reason))
		return
	}
	got := r.haltValue()
	r.mu.Lock()
	gw := r.gw
	r.mu.Unlock()
	if gw != nil {
		gw.HaltExternal(gateway.Halt{Reason: string(got.Reason), Scope: got.Scope, Detail: got.Detail, At: got.At})
	}
	r.cancelHaltedStage(*got)
	r.d.Log.Warn("budget: the run is halted", "reason", string(got.Reason), "scope", got.Scope)
}

// haltExternal is haltNow for a cause outside the stage, ignored once the
// agent loop is over.
func (r *run) haltExternal(h runstore.Halt) bool {
	h.Detail = r.redact(h.Detail)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return false
	}
	return r.recordHaltLocked(h)
}

// refuseStrayToken ends a budget-on run that has no backend in its job
// environment but was launched with a budget token: the job was not
// re-applied after the shared budget was turned on, and the run would
// otherwise go on outside the shared caps without a word. One bucket read,
// only for budget-on runs without a backend; with no token object (every
// install that never turned the shared budget on) it changes nothing.
func (r *run) refuseStrayToken(ctx context.Context) error {
	slug, runID := r.d.Store.Slug(), r.d.Store.RunID()
	if r.d.Bucket == nil || budget.Key(slug) != slug || budget.Key(runID) != runID || slug == "" || runID == "" {
		return nil
	}
	_, err := token.TakeObject(ctx, r.d.Bucket, slug, runID) // also deletes it
	if errors.Is(err, token.ErrNoToken) {
		return nil
	}
	if err != nil && !errors.Is(err, token.ErrTokenMismatch) {
		return fmt.Errorf("checking for a budget token in the runs bucket: %s", r.redact(err.Error()))
	}
	return fmt.Errorf("the run was launched with a budget token but this job has no budget backend (%s is not set in its environment): run fugaro init --repo for this repository, then launch the run again", RTDBURLEnv)
}

// freezeHalts is called when the agent loop is over: nothing the backend says
// can stop a stage any more.
func (r *run) freezeHalts() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

// bootstrapHalt records a policy halt before anything was locked and returns
// the error that ends bootstrap (outcome none, exit 0).
func (r *run) bootstrapHalt(reason runstore.HaltReason, scope, detail string) error {
	r.haltNow(runstore.Halt{Reason: reason, Scope: scope, At: r.d.Now().UTC(), Detail: detail})
	return &HaltError{*r.haltValue()}
}

// startBudgetBackend takes the run's Firebase identity and joins the shared
// budget (plan T6): it reads and deletes the token object, exchanges it,
// reads the caps and the kill switches, halts the run if it may not begin,
// creates the registry entry and starts the heartbeat and the kill watch.
// A halt here has outcome none and exit 0: nothing was locked or pushed.
func (r *run) startBudgetBackend(ctx context.Context) error {
	// A run whose budget is off ignores the budget's environment entirely.
	if !r.spend.On() {
		return nil
	}
	if r.d.BackendErr != nil {
		return fmt.Errorf("the budget backend in the job's environment: %w", r.d.BackendErr)
	}
	if !r.backendOn() {
		return r.refuseStrayToken(ctx)
	}
	b := r.d.Backend
	if b.APIKey == "" {
		return fmt.Errorf("the budget backend is configured (%s) but %s is not set", RTDBURLEnv, FirebaseAPIKeyEnv)
	}
	if r.d.Bucket == nil {
		return errors.New("the budget token is delivered through the runs bucket, and this run has none")
	}
	slug, runID := r.d.Store.Slug(), r.d.Store.RunID()
	if budget.Key(slug) != slug || budget.Key(runID) != runID || slug == "" || runID == "" {
		return fmt.Errorf("the repository slug %q or run id %q cannot be used as keys of the budget database", slug, runID)
	}
	custom, err := token.TakeObject(ctx, r.d.Bucket, slug, runID)
	switch {
	case errors.Is(err, token.ErrNoToken):
		return errors.New("the run's budget token is not in the runs bucket (already taken, or the run was not launched with one): launch the run again")
	case err != nil:
		return fmt.Errorf("taking the run's budget token: %s", r.redact(err.Error()))
	}
	// Registered before anything can print it.
	r.addSecret(custom)

	mode := r.spend.Mode
	cfg := budget.Config{
		RTDBURL: b.RTDBURL, APIKey: b.APIKey, HTTP: b.HTTP,
		Slug: slug, Run: runID, Grace: b.Grace,
		PolicyCap: r.spend.Cap, CommittedDaily: r.committedDaily(), LocalEnforce: mode == policyEnforce,
		Now: r.d.Now, Register: r.addSecret, Log: slog.New(redactHandler{h: r.d.Log.Handler(), redact: r.redact}),
		OnHalt: r.onBudgetHalt,
	}
	if r.rec.Deadline != nil {
		cfg.Deadline = *r.rec.Deadline
	}
	if b.Tune != nil {
		b.Tune(&cfg)
	}
	sess, err := budget.Open(ctx, cfg, custom)
	switch {
	case errors.Is(err, token.ErrTokenExpired):
		return r.bootstrapHalt(runstore.HaltBudgetTokenExpired, "run",
			"the run's budget token was minted more than an hour ago, usually because the run queued for that long: launch it again")
	case errors.Is(err, budget.ErrGraceExpired):
		return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
	case err != nil:
		return fmt.Errorf("the budget backend refused the run's identity: %s", r.redact(err.Error()))
	}
	r.mu.Lock()
	r.sess = sess
	r.mu.Unlock()

	gw := r.cfg.Agent.Auth == "api-key" || r.cfg.Agent.Auth == "vertex"
	h, err := sess.Admit(ctx, budget.AdmitOptions{Gateway: gw, LocalEnforce: cfg.LocalEnforce, PolicyCap: r.spend.Cap})
	switch {
	case errors.Is(err, budget.ErrGraceExpired):
		return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
	case err != nil:
		return fmt.Errorf("reading the project's budget: %s", r.redact(err.Error()))
	case h != nil:
		return r.bootstrapHalt(runstore.HaltReason(h.Reason), string(h.Scope), h.Detail)
	}
	if err := sess.Start(ctx, r.registryEntry()); err != nil {
		if errors.Is(err, budget.ErrGraceExpired) {
			return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
		}
		return fmt.Errorf("registering the run: %s", r.redact(err.Error()))
	}
	r.rec.Budget = &runstore.BudgetRecord{Day: budget.Day(sess.Now()), Mode: mode, Backend: "rtdb"}
	return nil
}

const policyEnforce = "enforce"

// committedDaily is the repository's committed per_day_usd in micro-dollars.
func (r *run) committedDaily() budget.Micros {
	if r.policy.PerDayUSD <= 0 {
		return 0
	}
	m, err := pricing.FromUSD(r.policy.PerDayUSD)
	if err != nil || m < 1 {
		return 0
	}
	return m
}

// registryEntry is what the run says about itself at bootstrap. The task's
// first line is untrusted text: redacted and clipped by the session.
func (r *run) registryEntry() budget.AgentEntry {
	title, _, _ := strings.Cut(strings.TrimSpace(r.spec.Task), "\n")
	title = r.redact(title)
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:80])
	}
	e := budget.AgentEntry{
		Repo: r.spec.Repo, Workflow: r.rec.Workflow, Title: title, Stage: "bootstrap", Auth: r.cfg.Agent.Auth,
		Coder: r.cfg.Agent.ModelFor(config.RoleCoder), Reviewer: r.cfg.Agent.ModelFor(config.RoleReviewer),
	}
	if r.rec.Deadline != nil {
		e.StageDeadline = r.rec.Deadline.UnixMilli()
	}
	if rr := r.rec.Recipe; rr != nil && rr.Name != recipe.DefaultName {
		e.Recipe = rr.Name
	}
	return e
}

// beginBudgetStage shows the stage in the registry.
func (r *run) beginBudgetStage(name string, n int, deadline time.Time) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return
	}
	now := r.d.Now().UnixMilli()
	sess.Update(func(e *budget.AgentEntry) {
		e.Stage, e.StageStartedAt, e.StageDeadline, e.Action = name, now, deadline.UnixMilli(), ""
		if name == "review" || name == "review_first" {
			e.Round = n
		}
	})
}

// reportNotional hands an oauth stage's own cost figure to the backend:
// notional dollars, advisory only, by model (plan R7). A write that fails is
// kept and retried by the heartbeat; the grace decides about an outage.
func (r *run) reportNotional(ctx context.Context, res agent.Result) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil || r.gatewayOn() {
		return
	}
	notional := budget.Micros(0)
	if res.CostUSD > 0 {
		m, err := pricing.FromUSD(res.CostUSD)
		if err != nil {
			r.d.Log.Warn("a stage's cost figure is not usable as a notional amount", "usd", res.CostUSD)
		} else {
			notional = m
		}
	}
	by := map[string]budget.ModelUse{}
	for model, u := range res.ModelUsage {
		use := budget.ModelUse{In: u.Input, Out: u.Output, CR: u.CacheRead, CW: u.CacheCreation}
		if m, ok := r.spend.Prices.Lookup(model); ok {
			use.Micros = m.Rates.Cost(pricing.Usage{Input: u.Input, CacheWrite5m: u.CacheCreation, CacheRead: u.CacheRead, Output: u.Output})
		}
		by[model] = use
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := sess.AddNotional(wctx, notional, by); err != nil {
		r.d.Log.Warn("reporting a stage's notional spend failed; it will be retried", "err", r.redact(err.Error()))
	}
}

// finishBudget ends the session: the last report, the outcome, the registry
// entry gone. It runs on a context of its own, so a cancelled run still
// tells the backend how it ended; every step is bounded.
func (r *run) finishBudget(ctx context.Context) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budgetFinishTimeout)
	defer cancel()
	sess.Finish(fctx, string(r.rec.Status))
	if b := r.rec.Budget; b != nil {
		b.GrantedMicros, b.ReleasedMicros = int64(sess.GrantedMicros()), int64(sess.ReleasedMicros())
	}
}

// budgetFinishTimeout bounds everything the session does at the end, inside
// Cloud Run's ~10 s SIGTERM window. Finish orders its writes by importance:
// the release first, then the last usage, the outcome, the entry; what a
// kill cuts short stays for the sweeper.
const budgetFinishTimeout = 8 * time.Second

// redactHandler redacts every string the session logs, so a credential can
// never reach the job's log through an error text.
type redactHandler struct {
	h      slog.Handler
	redact func(string) string
}

func (h redactHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.h.Enabled(ctx, l) }

func (h redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool { out.AddAttrs(h.attr(a)); return true })
	return h.h.Handle(ctx, out)
}

func (h redactHandler) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.redact(a.Value.String()))
	case slog.KindGroup:
		g := a.Value.Group()
		out := make([]slog.Attr, len(g))
		for i, c := range g {
			out[i] = h.attr(c)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(h.redact(err.Error()))
		} else {
			a.Value = slog.StringValue(h.redact(fmt.Sprint(a.Value.Any())))
		}
	}
	return a
}

func (h redactHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = h.attr(a)
	}
	return redactHandler{h: h.h.WithAttrs(out), redact: h.redact}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{h: h.h.WithGroup(name), redact: h.redact}
}
