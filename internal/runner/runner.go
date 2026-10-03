// Package runner executes one Fugaro run: bootstrap, implement, review and
// fix rounds, finalize (design §4).
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Deps is everything a run touches.
type Deps struct {
	Store *runstore.Store
	// OpenProvider opens the git provider. It is called once, as soon as
	// the provider's kind is known: before the checkout when ProviderKind
	// or the origin URL's host names it, otherwise right after fugaro.yaml
	// is read, from git.provider.
	OpenProvider gitprov.Opener
	// ProviderKind, when set, names the provider before fugaro.yaml is
	// read, for remotes whose host does not identify it. git.provider
	// must agree with it.
	ProviderKind string
	// RetryDelay is the pause between attempts to open the pull request;
	// zero means 3 seconds.
	RetryDelay  time.Duration
	Agent       agent.Agent
	WorkDir     string   // the repository checkout (baked into the image)
	Remote      string   // cloned into WorkDir when it has no checkout
	StateDir    string   // FUGARO_STATE_DIR; must be outside WorkDir
	Env         []string // the runner's environment, usually os.Environ()
	PathPrepend string   // directory holding the fugaro binary, put first on the agent's PATH
	Log         *slog.Logger
	Now         func() time.Time
	CancelPoll  time.Duration
	// Bucket is the runs bucket, for the branch lock and caches
	// (design §3.3). Nil disables both.
	Bucket *blobx.Bucket
	// Execution is the canonical execution name (backend.ExecID.String()),
	// recorded in result.json; empty for local runs, which skips the
	// duplicate-execution check. Compare with backend.SameExecution.
	Execution string
	// BaseImage is FUGARO_BASE_IMAGE, the base the image was built FROM;
	// it is part of every cache key.
	BaseImage string
	// CacheMaxBytes caps a cache archive; zero means cache.DefaultMaxBytes.
	CacheMaxBytes int64
	// Prices are the backend's list prices for the job's compute; nil
	// means compute is not estimated, as on a local run.
	Prices *backend.Prices
	// ImageInfoPath is the image's build record; empty means
	// DefaultImageInfoPath.
	ImageInfoPath string
	// Project is FUGARO_PROJECT: the Fugaro project this job belongs to.
	// When set, fugaro.yaml on the base branch must name it.
	Project string
	// RequireProject (backend.OnCloudRun) makes a missing Project fail
	// bootstrap; a local run without one skips the check.
	RequireProject bool
	// ManagedSettingsPath is where the managed Claude Code settings are
	// written before each stage; empty means agent.ManagedSettingsPath.
	ManagedSettingsPath string
	// Spend is the job's budget, from SpendFromEnv; the zero value is off.
	Spend Spend
	// SpendErr is SpendFromEnv's error: bootstrap fails with it once the
	// run's record is claimed.
	SpendErr error
	// GatewayUpstream, for tests only, is http://127.0.0.1:<port>: where the
	// gateway sends its calls instead of the real API. Empty is the real one.
	GatewayUpstream string
	// GatewayStageWait, for tests only, shortens how long the gateway waits
	// for a stage's calls in flight; zero is 30 seconds.
	GatewayStageWait time.Duration
	// VertexTokens is the gateway's Vertex credential; nil is
	// google.DefaultTokenSource (the metadata server on Cloud Run).
	VertexTokens oauth2.TokenSource
	// Backend is the project's budget database (BackendFromEnv); the zero
	// value is none, and the run is M9a's. It is used only when the merged
	// budget mode is observe or enforce.
	Backend Backend
	// BackendErr is BackendFromEnv's error: bootstrap fails with it once the
	// run's record is claimed.
	BackendErr error
}

type run struct {
	d       Deps
	rec     *runstore.Record
	spec    *task.Spec
	cfg     *config.Config
	wf      config.Workflow
	repo    *gitops.Repo
	env     []string
	secrets []string
	budget  Budget
	// secMu guards secrets: the budget session registers a refreshed ID
	// token from its own goroutine.
	secMu sync.RWMutex
	// spend and policy are the run's effective budget, merged once at
	// bootstrap (resolvePolicy) from the ceiling, the default branch and
	// the run's own fugaro.yaml. Nothing recomputes them, so an edit of
	// fugaro.yaml during the run changes neither.
	spend        Spend
	policy       policy.Effective
	defFile      defaultBranchData
	instructions string
	reviewFile   string
	stageN       map[string]int
	// mu guards halt, cancelled, failReason, tokens, gatewayTokens and
	// haltStage: the cancel watcher and, with the gateway, its halt
	// callback record into them from other goroutines. Whichever of a halt
	// and a cancel is recorded first wins (see haltNow and markCancelled).
	mu            sync.Mutex
	failReason    string
	cancelled     bool
	halt          *runstore.Halt
	tokens        int64                   // the stages' tokens so far, for the token cap
	gatewayTokens int64                   // the last stage's tokens as the gateway counted them
	haltStage     context.CancelCauseFunc // the running stage's cancel; nil between stages
	// stageExtra, when set, is called once after each stage's agent
	// returns, and says what the gateway saw: violations of the stage's
	// rules (the first fails the stage) and its token count.
	wroteManaged bool // this run wrote the managed settings file (guarded by mu)
	warnedKnob   bool // the routing-settings knob's Cloud Run warning was logged
	stageExtra   func(stage string) (violations []string, tokens int64)
	// sess is the run's session with the budget backend; nil when the run
	// has none. frozen is set when the agent loop is over, after which a
	// halt from the backend changes nothing. Both guarded by mu.
	sess   *budget.Session
	frozen bool
	// The gateway, when the budget is on for an api-key or vertex run, and
	// what its stages cost; all guarded by mu.
	gw           *gateway.Server
	gwAgent      *agent.Gateway
	gwClosed     bool
	gwUsed       pricing.Micros
	lastStage    *gateway.StageReport
	modelBy      map[string]pricing.Micros
	routeBy      map[string]pricing.Micros
	reported     pricing.Micros
	unreconciled pricing.Micros
	unparsed     int
	provider     gitprov.Provider
	providerKind string
	providerFrom string          // where providerKind came from, for mismatch errors
	credURL      string          // scheme://host of an HTTPS origin; "" when git needs no token
	auth         gitprov.GitAuth // current git credentials
	authWarned   bool            // whether a mid-run refresh failure has already been logged
	tail         *LogTail        // output of the first failed stage, for the draft PR
	lock         *lock.Lock      // the branch lock, while held
	// owned is whether this execution owns result.json: it created the
	// first record, or found one naming itself. Until then the record is
	// only ever created if absent, never overwritten (design §4.7).
	owned     bool
	caches    []cacheSlot // the cache entries restored at bootstrap, for writeback
	cacheBase string      // the base-image part of every cache key
	toolchain string      // the toolchain part of every cache key (toolchainHash)
	// sessionID is the session the latest implement or fix stage's result
	// reported: the one the next fix resumes and writeback saves.
	sessionID string
	// follow is a follow-up's state; nil for a first run.
	follow *followState
	// pr is the early draft pull request's state (prflow.go).
	pr prFlow
}

// Git credential lifetimes (design §6.2). A stage must not outlive its
// token, so before each stage the runner asks for one valid for the stage
// timeout plus gitAuthSlack; bootstrap's clone and fetch need only
// bootstrapAuthMinValid. authRefreshTimeout bounds how long a mid-run
// refresh itself may take, so a slow or hanging provider call cannot eat
// into the stage's own budget.
//
// No request asks for more than maxAuthValid: a GitHub installation token
// lives about an hour, so a longer request could never be met and every
// refresh would fail, keeping the old token until it expired. A stage
// longer than that therefore outlives its token; the agent's git and gh
// calls late in such a stage fail, and only the refresh before the next
// stage (and before finalize's push) restores working credentials.
const (
	gitAuthSlack          = 5 * time.Minute
	bootstrapAuthMinValid = 10 * time.Minute
	maxAuthValid          = 50 * time.Minute
	authRefreshTimeout    = 30 * time.Second
)

// authValidity is the validity to ask a token for when it must last d.
func authValidity(d time.Duration) time.Duration { return min(d, maxAuthValid) }

// giveUpCommentTimeout bounds the not-ready comment finalize posts when
// ensurePR gives up with a pull request, on a context detached from the
// possibly expired finalize deadline, so it may run up to this long past
// FinalizeReserve.
const giveUpCommentTimeout = 30 * time.Second

// prAttempts is how many times finalize tries to open the pull request.
// EnsurePR finds what an earlier attempt created, so retrying is safe.
const prAttempts = 3

// Run executes the run whose task spec is in d.Store. It returns the final
// record, reflecting the run's actual status and outcome whether or not Run
// itself returns an error, except when this execution does not own the run
// (ErrDuplicateExecution, or a record it may not replace): then it writes
// nothing and returns a nil record. The error is non-nil for an
// infra_error, for a cancellation seen during bootstrap (before any stage
// or finalize runs, where the record's status is cancelled rather than
// infra_error), or when writing the final record itself fails.
func Run(ctx context.Context, d Deps) (rec *runstore.Record, err error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.CancelPoll == 0 {
		d.CancelPoll = 30 * time.Second
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	r := &run{
		d:      d,
		stageN: map[string]int{},
		rec: &runstore.Record{Version: 1, RunID: d.Store.RunID(), Execution: d.Execution, Status: runstore.StatusRunning,
			Stage: "bootstrap", Outcome: runstore.OutcomeNone, StartedAt: d.Now().UTC()},
		// A local run has no duplicate to tell apart: its record is its own.
		owned: d.Execution == "",
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
		if errors.Is(err, ErrDuplicateExecution) || errors.Is(err, errOwnerUnknown) {
			// Another execution owns this run, or may: its result.json and
			// lock are not ours to touch.
			d.Log.Error("not the run's owner; exiting without writing", "execution", d.Execution, "err", r.redact(err.Error()))
			rec = nil
			return
		}
		r.closeGateway()
		rctx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), releaseDeferredTimeout)
		r.releaseLock(rctx)
		cancelRelease()
		if err != nil {
			// Provider and git errors can quote what they were sent, so
			// the reason is redacted like everything else published.
			reason := r.redact(err.Error())
			// A more specific outcome (such as a bootstrap cancellation) may
			// already be recorded; only fall back to infra_error when the
			// run never got far enough to decide anything else.
			if r.rec.Status == runstore.StatusRunning {
				r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, reason
			}
			d.Log.Error("run failed", "stage", r.rec.Stage, "err", reason)
		}
		if h := r.haltValue(); h != nil && r.rec.Halt == nil {
			r.rec.Halt = h // kept even when finalize then failed
		}
		// The shared budget hears how the run ended: the outcome, once, and
		// the registry entry goes.
		r.freezeHalts()
		r.finishBudget(ctx)
		// The report's figure stops before writeback; the record's covers
		// the whole run.
		r.updateCost()
		finished := d.Now().UTC()
		r.rec.FinishedAt = &finished
		if !r.owned {
			// The run failed before it could claim result.json: record the
			// failure only if no execution has, so a duplicate that failed
			// early never replaces the owner's record.
			switch cerr := r.createRecord(ctx); {
			case errors.Is(cerr, runstore.ErrExists):
				d.Log.Error("another execution's run record is in place; not replacing it", "execution", d.Execution)
				rec = nil
				return
			case cerr != nil && err == nil:
				err = fmt.Errorf("writing run record: %w", cerr)
			}
			rec = r.rec
			return
		}
		wctx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
		defer cancelWrite()
		if werr := writeRecord(d.Store, wctx, r.rec); werr != nil && err == nil {
			err = fmt.Errorf("writing run record: %w", werr)
		}
		rec = r.rec
	}()

	r.addMountedSecrets()
	if onNewRun != nil {
		onNewRun(r)
	}
	runCtx, stopWatch := WatchCancel(ctx, d.Store.CancelRequested, d.CancelPoll, r.markCancelled)
	defer stopWatch()
	if err := r.bootstrap(runCtx); err != nil {
		var halt *HaltError
		switch {
		case errors.As(err, &halt):
			// A policy halt before anything was locked, pushed or opened:
			// an outcome, not a failure, so there is no error to return.
			r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, &halt.Halt
			r.rec.Reason = halt.Error()
			d.Log.Warn("run halted at bootstrap", "reason", string(halt.Halt.Reason), "detail", halt.Halt.Detail)
			return r.rec, nil
		case errors.Is(err, ErrCancelled) || errors.Is(context.Cause(runCtx), ErrCancelled):
			if r.markCancelled() {
				r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, "cancelled during bootstrap"
			}
		}
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	r.runAgentLoop(runCtx)
	// From here nothing the backend says can stop a stage.
	r.freezeHalts()
	// Finalize makes no model calls: the gateway is closed first, and the
	// run's model cost is its ledger's.
	r.closeGateway()
	if errors.Is(context.Cause(runCtx), ErrCancelled) {
		// The cancel may have landed after the last stage already returned
		// successfully; make sure it still turns into a draft PR, unless a
		// halt was recorded first.
		if r.markCancelled() {
			r.fail("cancelled")
		}
	}
	finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.wf.Timeouts.FinalizeReserve.Duration)
	defer cancel()
	// writeback, which saves the session, won't run when finalize fails
	// or panics: save it then, on a context of its own, as finalize's may
	// have run out.
	saveSession := func() {
		sctx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), sessionSaveTimeout)
		r.saveSession(sctx)
		cancelSave()
	}
	finErr := func() error {
		defer func() {
			if p := recover(); p != nil {
				saveSession()
				panic(p)
			}
		}()
		return r.finalize(finCtx)
	}()
	if finErr != nil {
		saveSession()
		return nil, fmt.Errorf("finalize: %w", finErr)
	}
	r.writeback(ctx)
	return r.rec, nil
}

// runAgentLoop runs agentLoop, recovering a panic so finalize always runs and
// still pushes a draft PR explaining what happened.
func (r *run) runAgentLoop(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			r.fail(fmt.Sprintf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.agentLoop(ctx)
}

// save writes the record, if this execution owns it, within
// recordWriteTimeout of its own: detached from ctx's cancellation and
// deadline, and bounded, so a stalled bucket can't hold the run.
func (r *run) save(ctx context.Context) {
	if !r.owned {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	if err := writeRecord(r.d.Store, wctx, r.rec); err != nil {
		r.d.Log.Warn("writing run record failed", "err", err)
	}
}

// createRecord creates result.json if absent, within recordWriteTimeout.
func (r *run) createRecord(ctx context.Context) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	return createRecord(r.d.Store, wctx, r.rec)
}

// errOwnerUnknown means the first record's create failed and nothing
// could be read back, so whether another execution owns the run is not
// known. The run stops and writes nothing.
var errOwnerUnknown = errors.New("could not tell whether another execution owns this run")

// claimRecord makes this Cloud Run execution the owner of result.json, or
// tells it that it is a duplicate (design §4.7). It creates the first
// record if absent; when one is already there, or the create failed for
// another reason, it reads the record back: one naming this very
// execution (a restart, or a create whose response was lost) is ours,
// one naming any other execution makes this a duplicate, and an
// unreadable one after a failed create leaves ownership unknown.
func (r *run) claimRecord(ctx context.Context) error {
	cerr := r.createRecord(ctx)
	if cerr == nil {
		r.owned = true
		return nil
	}
	exists := errors.Is(cerr, runstore.ErrExists)
	if !exists {
		r.d.Log.Warn("creating the first run record failed; reading it back", "err", r.redact(cerr.Error()))
	}
	prev, rerr := r.d.Store.ReadRecord(ctx)
	switch {
	case rerr != nil && exists:
		return ErrDuplicateExecution // someone else's, unreadable: nothing written
	case rerr != nil:
		return fmt.Errorf("%w: creating the first run record: %w; reading it back: %w", errOwnerUnknown, cerr, rerr)
	case !backend.SameExecution(prev.Execution, r.d.Execution):
		return ErrDuplicateExecution
	}
	r.owned = true
	r.save(ctx) // this very execution restarted, or its create landed: carry on
	return nil
}

// redact removes every known secret value from s.
func (r *run) redact(s string) string { return agent.Redact(s, r.secretList()) }

// secretList is a snapshot of the secrets known now.
func (r *run) secretList() []string {
	r.secMu.RLock()
	defer r.secMu.RUnlock()
	return slices.Clone(r.secrets)
}

// addSecret adds v to the values redacted from everything the run publishes.
func (r *run) addSecret(v string) {
	r.secMu.Lock()
	defer r.secMu.Unlock()
	if v != "" && !slices.Contains(r.secrets, v) {
		r.secrets = append(r.secrets, v)
	}
}

// SecretEnvsVar is set on the Cloud Run job to the comma-separated names
// of every secret variable the job mounts: the platform's own and the
// job's own workflow's secrets in the fugaro.yaml it was deployed from.
// The ref a task runs may declare fewer, but the values are in the
// runner's environment either way, where the agent can read them.
const SecretEnvsVar = "FUGARO_SECRET_ENVS"

// minSecretLen is the shortest value redacted; agent.BuildEnv refuses a
// shorter declared secret for the same reason.
const minSecretLen = 4

// addMountedSecrets registers the values of the variables SecretEnvsVar
// names, before anything else runs, so even a failure reason from the
// first moments of bootstrap is redacted of them. A missing variable is
// skipped; one too short to redact safely is skipped with a warning that
// names only the variable.
func (r *run) addMountedSecrets() {
	for _, name := range strings.Split(envLookup(r.d.Env, SecretEnvsVar), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch v := envLookup(r.d.Env, name); {
		case v == "":
		case len(v) < minSecretLen:
			r.d.Log.Warn("a mounted secret is too short to redact", "env", name)
		default:
			r.addSecret(v)
		}
	}
}

// redactRecords returns records with the text the agent can influence
// (test names from its reports, the warning quoting them) redacted.
func (r *run) redactRecords(records []verify.Record) []verify.Record {
	out := make([]verify.Record, len(records))
	for i, v := range records {
		v.Failed = r.redactAll(v.Failed)
		v.Flaky = r.redactAll(v.Flaky)
		v.Warning = r.redact(v.Warning)
		out[i] = v
	}
	return out
}

// redactAll redacts each of ss, keeping nil as nil.
func (r *run) redactAll(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = r.redact(s)
	}
	return out
}

// openProvider opens the provider of kind, which from names the source of
// (for error messages), and fetches its first git credentials.
func (r *run) openProvider(ctx context.Context, kind, from string) error {
	p, secrets, err := r.d.OpenProvider(ctx, kind, r.spec.Repo)
	for _, s := range secrets {
		r.addSecret(s)
	}
	if err != nil {
		return fmt.Errorf("opening the %s provider: %w", kind, err)
	}
	r.provider, r.providerKind, r.providerFrom = p, kind, from
	if err := r.refreshGitAuth(ctx, bootstrapAuthMinValid); err != nil {
		return fmt.Errorf("getting git credentials from the %s provider: %w", kind, err)
	}
	return nil
}

// refreshGitAuth gets git credentials valid for at least minValid and puts
// them in the environment of the runner's git and of the agent (design
// §6.2). With the GitHub provider this is what replaces a near-expiry
// installation token between stages. The credentials are validated (and the
// environment built) before r.auth is updated, so a bad refresh — for
// instance a token or username CredentialVars refuses — never clobbers
// working credentials with ones that can't be turned into an environment;
// the caller decides how to handle the error (fail at open, warn and keep
// the current ones mid-run).
func (r *run) refreshGitAuth(ctx context.Context, minValid time.Duration) error {
	auth, err := r.provider.GitAuth(ctx, minValid)
	if err != nil {
		return err
	}
	vars, err := r.credentialVars(auth)
	if err != nil {
		return err
	}
	r.addSecret(auth.Token)
	for _, v := range auth.Env {
		r.addSecret(v)
	}
	r.auth = auth
	if r.repo != nil {
		r.repo.Env = gitops.WithVars(r.repo.Env, vars)
	}
	// A follow-up's agent gets no git credentials (design §6.1): the
	// runner fetches and pushes for it.
	if r.env != nil && r.follow == nil {
		r.env = gitops.WithVars(r.env, vars)
	}
	return nil
}

// authVars is the environment that carries the current git credentials.
func (r *run) authVars() (map[string]string, error) {
	return r.credentialVars(r.auth)
}

// credentialVars builds the environment that carries auth's credentials
// against r.credURL. It errors when gitops.CredentialVars refuses auth's
// username or token (for instance one holding a newline), rather than
// silently dropping the credentials.
func (r *run) credentialVars(auth gitprov.GitAuth) (map[string]string, error) {
	vars := map[string]string{}
	if auth.Token != "" && r.credURL != "" {
		cred, err := gitops.CredentialVars(r.credURL, auth.Username, auth.Token)
		if err != nil {
			return nil, err
		}
		maps.Copy(vars, cred)
	}
	maps.Copy(vars, auth.Env)
	return vars, nil
}

// warnAuthRefresh logs a mid-run git-credential refresh failure once per
// run: a persistent problem (an expiring provider, a misconfigured token)
// would otherwise print the same warning before every stage.
func (r *run) warnAuthRefresh(err error) {
	if r.authWarned {
		return
	}
	r.authWarned = true
	r.d.Log.Warn("refreshing git credentials failed; keeping the current ones", "err", r.redact(err.Error()))
}

// originURL returns the checkout's origin URL, or the remote it will be
// cloned from when there is no checkout yet.
func (r *run) originURL(ctx context.Context) string {
	if repo, err := gitops.Open(r.d.WorkDir, nil); err == nil {
		if u, err := strip(repo).OriginURL(ctx); err == nil {
			return u
		}
	}
	return r.d.Remote
}

// fail records the first reason the run cannot produce a ready PR.
func (r *run) fail(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(reason)
}

// failLocked is fail for a caller holding r.mu.
func (r *run) failLocked(reason string) {
	if r.failReason == "" {
		r.failReason = reason
	}
}

// stateDirOutsideCheckout validates that stateDir is non-empty, resolvable,
// and disjoint from workDir in both directions, so it can be neither inside,
// equal to, nor an ancestor of the checkout. Symlinks are resolved first, so
// a state dir that only looks outside the checkout is refused too. This must
// run before any git or filesystem work.
func stateDirOutsideCheckout(workDir, stateDir string) error {
	if stateDir == "" {
		return errors.New("state dir must not be empty")
	}
	bad := fmt.Errorf("state dir %s must be outside the checkout %s", stateDir, workDir)
	absWork, err := resolvePath(workDir)
	if err != nil {
		return bad
	}
	absState, err := resolvePath(stateDir)
	if err != nil {
		return bad
	}
	sep := string(filepath.Separator)
	fromWork, err := filepath.Rel(absWork, absState)
	if err != nil || !(fromWork == ".." || strings.HasPrefix(fromWork, ".."+sep)) {
		return bad
	}
	fromState, err := filepath.Rel(absState, absWork)
	if err != nil || !strings.HasPrefix(fromState, ".."+sep) {
		return bad
	}
	return nil
}

// resolvePath returns p as an absolute path with symlinks resolved. Only the
// part of p that exists can be resolved; the rest (a checkout still to be
// cloned, a state dir still to be created) is appended unchanged.
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	var rest []string
	for dir := abs; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		if dir == filepath.Dir(dir) {
			return abs, nil
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
	}
}

// prFile is where the agent writes the PR title and body.
const prFile = "pr.md"

// clearStateDir removes Fugaro's own entries left in stateDir by an earlier
// run, creating stateDir if it is absent. The directory is operator-chosen,
// so anything else in it is left alone.
func clearStateDir(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state dir %s: %w", stateDir, err)
	}
	if err := verify.ClearState(stateDir); err != nil {
		return err
	}
	for _, name := range []string{prFile, followupFile} {
		if err := os.Remove(filepath.Join(stateDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing stale %s: %w", name, err)
		}
	}
	return nil
}

// bashTimeoutSlack is added to timeouts.verify for the agent's Bash tool
// timeout, so the tool never kills `fugaro verify` before verify's own
// timeout does.
const bashTimeoutSlack = 2 * time.Minute

func (r *run) bootstrap(ctx context.Context) error {
	spec, err := r.d.Store.ReadTask(ctx)
	if err != nil {
		return fmt.Errorf("reading task spec: %w", err)
	}
	r.spec = spec
	r.rec.RunID, r.rec.Repo = spec.RunID, spec.Repo
	// Read before the sync below moves the checkout off the baked commit.
	r.rec.Image = r.imageInfo(ctx)
	if r.owned {
		r.save(ctx)
	} else if err := r.claimRecord(ctx); err != nil {
		// Claimed before the lock, so two executions of one run are
		// always told apart here (design §4.7).
		return err
	}
	if r.d.SpendErr != nil {
		return fmt.Errorf("the budget in the job's environment: %w", r.d.SpendErr)
	}
	// A cancel that landed before the run started is seen now, not only
	// at the watcher's first poll, before anything is cloned or locked.
	if ok, err := r.d.Store.CancelRequested(ctx); err != nil {
		r.d.Log.Warn("checking for a cancel request failed", "err", r.redact(err.Error()))
	} else if ok {
		return fmt.Errorf("%w before the run started", ErrCancelled)
	}
	if spec.IsFollowUp() {
		r.follow = &followState{}
	}
	if err := stateDirOutsideCheckout(r.d.WorkDir, r.d.StateDir); err != nil {
		return err
	}

	// The clone and fetch below may already need credentials, so the
	// provider is opened now if anything but fugaro.yaml names it.
	origin := r.originURL(ctx)
	r.credURL = gitops.CredentialURL(origin)
	if kind, from := r.d.ProviderKind, "the --provider flag"; kind != "" {
		if err := r.openProvider(ctx, kind, from); err != nil {
			return err
		}
	} else if kind := gitprov.KindForURL(origin); kind != "" {
		// origin may carry embedded userinfo (a token in the remote
		// URL); the message never quotes more than scheme://host.
		display := origin
		if r.credURL != "" {
			display = r.credURL
		}
		if err := r.openProvider(ctx, kind, "the origin URL "+display); err != nil {
			return err
		}
	}
	cloneVars, err := r.authVars()
	if err != nil {
		return fmt.Errorf("building git credentials: %w", err)
	}
	repo, err := gitops.OpenOrClone(ctx, r.d.WorkDir, r.d.Remote, gitops.WithVars(gitops.IdentityEnv(), cloneVars), modelCredentialVars...)
	if err != nil {
		return fmt.Errorf("opening checkout %s: %w", r.d.WorkDir, err)
	}
	r.repo = strip(repo)
	var cfg *config.Config
	if r.follow != nil {
		// The pull request's branch, with the configuration of its base.
		if err := r.checkoutFollowUp(ctx, repo); err != nil {
			return err
		}
		r.rec.Branch = spec.Branch
		if cfg, err = r.readBaseConfig(ctx); err != nil {
			return err
		}
	} else {
		branch := "fugaro/" + spec.RunID
		if err := repo.CheckoutNewBranch(ctx, spec.Ref, branch); err != nil {
			return fmt.Errorf("checking out %s: %w", spec.Ref, err)
		}
		r.rec.Branch = branch
		data, err := os.ReadFile(filepath.Join(r.d.WorkDir, "fugaro.yaml"))
		if err != nil {
			return fmt.Errorf("reading fugaro.yaml at %s: %w", spec.Ref, err)
		}
		if cfg, err = parseConfig(data); err != nil {
			return err
		}
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return fmt.Errorf("selecting workflow: %w", err)
	}
	if err := spec.Apply(cfg, &wf); err != nil {
		return fmt.Errorf("applying task overrides: %w", err)
	}
	r.cfg, r.wf, r.rec.Workflow, r.rec.BaseBranch = cfg, wf, name, cfg.Git.BaseBranch
	r.rec.FinalizeReserveS = wf.Timeouts.FinalizeReserve.Seconds()
	dl := r.lockDeadline()
	r.rec.Deadline = &dl
	if r.follow == nil {
		// A read, so it comes before the lock; a follow-up fetched its
		// base, and read its configuration from it, above.
		if err := repo.FetchBase(ctx, cfg.Git.BaseBranch); err != nil {
			return fmt.Errorf("fetching base %s: %w", cfg.Git.BaseBranch, err)
		}
	}
	if err := r.checkProject(ctx, cfg); err != nil {
		return err
	}
	if err := r.resolvePolicy(ctx, cfg); err != nil {
		return err
	}
	// The run's Firebase identity, the shared caps and the kill switches:
	// a halt here is outcome none (nothing is locked, pushed or opened).
	if err := r.startBudgetBackend(ctx); err != nil {
		return err
	}
	if err := r.checkBudget(); err != nil {
		return err
	}
	if err := r.haltedErr(); err != nil {
		return err
	}
	// The lock comes before anything changes remote state: the clone and
	// checkout above are local, and the provider has only been asked for
	// credentials.
	if err := r.acquireLock(ctx); err != nil {
		return err
	}
	switch {
	case r.provider == nil:
		if err := r.openProvider(ctx, cfg.Git.Provider, "git.provider"); err != nil {
			return err
		}
	case r.providerKind != cfg.Git.Provider:
		return fmt.Errorf("fugaro.yaml sets git.provider to %s, but %s says %s", cfg.Git.Provider, r.providerFrom, r.providerKind)
	}
	if r.follow != nil {
		if err := r.checkPullRequest(ctx); err != nil {
			return err
		}
	}
	r.restoreCaches(ctx)
	if r.follow == nil {
		// A follow-up fetched its base, and read these from it, above.
		if r.instructions, err = r.readRepoFile(cfg.Agent.Instructions); err != nil {
			return fmt.Errorf("agent.instructions: %w", err)
		}
		if cfg.Agent.Review != "" && !strings.HasPrefix(cfg.Agent.Review, "/") {
			if r.reviewFile, err = r.readRepoFile(cfg.Agent.Review); err != nil {
				return fmt.Errorf("agent.review: %w", err)
			}
		}
	}

	if err := clearStateDir(r.d.StateDir); err != nil {
		return fmt.Errorf("clearing state dir %s: %w", r.d.StateDir, err)
	}
	if err := verify.WriteSettings(r.d.StateDir, verify.Settings{
		RepoDir: r.d.WorkDir, Build: wf.Commands.Build, Test: wf.Commands.Test,
		RerunFailed: wf.Commands.RerunFailed, Reports: wf.Commands.Reports,
		TimeoutS: int(wf.Timeouts.Verify.Seconds()),
	}); err != nil {
		return fmt.Errorf("writing verify settings: %w", err)
	}
	secretEnvs := make([]string, len(wf.Secrets))
	for i, s := range wf.Secrets {
		secretEnvs[i] = s.Env
	}
	// Claude Code's Bash tool defaults to a 2-minute timeout (10 minutes at
	// most), which would kill a long `fugaro verify` long before
	// timeouts.verify does.
	bashMS := strconv.FormatInt((wf.Timeouts.Verify.Duration + bashTimeoutSlack).Milliseconds(), 10)
	set := map[string]string{
		"FUGARO_STATE_DIR":        r.d.StateDir,
		"BASH_DEFAULT_TIMEOUT_MS": bashMS,
		"BASH_MAX_TIMEOUT_MS":     bashMS,
	}
	for k, v := range gitops.Identity {
		set[k] = v
	}
	if r.follow == nil {
		// A follow-up's agent gets no git credentials and no GH_TOKEN
		// (design §6.1): the runner fetched the branch and pushes it, and
		// the comments arrive in the prompt.
		agentAuthVars, err := r.authVars()
		if err != nil {
			return fmt.Errorf("building git credentials: %w", err)
		}
		maps.Copy(set, agentAuthVars)
	}
	if r.gatewayOn() {
		// After the lock and the provider, so a run that never gets as far
		// starts nothing; before the agent's environment, which needs the
		// gateway's URL and token.
		if err := r.startGateway(ctx); err != nil {
			return err
		}
	}
	env, secrets, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth: cfg.Agent.Auth, Secrets: secretEnvs, Set: set, PathPrepend: r.d.PathPrepend, Gateway: r.gateway(),
	})
	if err != nil {
		return fmt.Errorf("building agent environment: %w", err)
	}
	r.env = env
	for _, s := range secrets {
		r.addSecret(s)
	}
	r.budget = Budget{Start: r.rec.StartedAt, Total: wf.Timeouts.Total.Duration,
		Reserve: wf.Timeouts.FinalizeReserve.Duration, Stage: wf.Timeouts.Stage.Duration, Now: r.d.Now}
	if r.follow != nil {
		if err := r.prepareFollowUp(ctx); err != nil {
			return err
		}
	}
	// A kill switch or the grace may have halted the run while bootstrap
	// was busy: nothing is pushed yet, so it ends as a bootstrap halt.
	if err := r.haltedErr(); err != nil {
		return err
	}
	r.save(ctx)
	return nil
}

// haltedErr is the HaltError of a halt recorded so far, or nil.
func (r *run) haltedErr() error {
	if h := r.haltValue(); h != nil {
		return &HaltError{*h}
	}
	return nil
}

// parseConfig parses fugaro.yaml, joining every problem into one error.
func parseConfig(data []byte) (*config.Config, error) {
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		return nil, fmt.Errorf("fugaro.yaml is invalid: %s", strings.Join(msgs, "; "))
	}
	return cfg, nil
}

func (r *run) readRepoFile(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, rel))
	return string(data), err
}

func (r *run) agentLoop(ctx context.Context) {
	pd := PromptData{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, StateDir: r.d.StateDir}
	if r.follow != nil {
		pd.FollowUp = followup.SystemPromptLines(r.promptData())
	}
	sys := SystemPrompt(pd, r.instructions)
	req := agent.Request{Prompt: r.spec.Task, SessionID: agent.NewSessionID(), AppendSystemPrompt: sys}
	var opts stageOpts
	if f := r.follow; f != nil {
		req.Prompt = followup.ImplementPrompt(r.promptData(), f.sel)
		if f.restored.Resumed {
			req.SessionID, req.Resume = f.restored.ID, true
			// Claude Code may still not find the session it was given:
			// that is a reason to start fresh, not a failed stage.
			opts.Recoverable = func(err error) bool { return errors.Is(err, agent.ErrNoSession) }
		}
	}
	res, ok, err := r.stage(ctx, "implement", req, opts)
	r.noteSession(req, res)
	r.countTokens(res)
	// Only an error stage recovered from, and so recorded nothing about:
	// one that came with a timeout or a cancel is a failed stage.
	if rec := (recoveredError{}); !ok && errors.As(err, &rec) {
		r.fellBackFresh(ctx)
		req = agent.Request{Prompt: followup.ImplementPrompt(r.promptData(), r.follow.sel), SessionID: agent.NewSessionID(), AppendSystemPrompt: sys}
		res, ok, _ = r.stage(ctx, "implement", req, stageOpts{})
		r.noteSession(req, res)
		r.countTokens(res)
	}
	if !ok || r.capReached() {
		return
	}
	r.afterStage(ctx, "implement")
	if r.pr.gone {
		return // a person closed the PR: nothing more to spend on it
	}
	sessionID := req.SessionID
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.cfg.Git.BaseBranch)
	if f := r.follow; f != nil {
		if add := followup.ReviewAddendum(f.sel, f.nonce); add != "" {
			reviewPrompt += "\n\n" + add
		}
	}
	rounds := r.cfg.Agent.ReviewRounds
	for round := 1; round <= rounds; round++ {
		res, ok, _ := r.stage(ctx, "review", agent.Request{Prompt: reviewPrompt, SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}, stageOpts{})
		r.countTokens(res)
		if !ok {
			return
		}
		v := ParseVerdict(res)
		r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Verdict: v.Verdict, Findings: len(v.Findings)})
		r.save(ctx)
		if r.capReached() || v.Verdict == "ship" || round == rounds {
			return
		}
		r.afterStage(ctx, "review")
		if r.pr.gone {
			return
		}
		if r.sessionID != "" {
			sessionID = r.sessionID
		}
		req := agent.Request{Prompt: FixPrompt(v), SessionID: sessionID, Resume: true, AppendSystemPrompt: sys}
		res, ok, _ = r.stage(ctx, "fix", req, stageOpts{})
		r.noteSession(req, res)
		r.countTokens(res)
		if !ok || r.capReached() {
			return
		}
		r.afterStage(ctx, "fix")
		if r.pr.gone {
			return
		}
	}
}

// recoveredError is an agent error stage left to its caller, having
// recorded nothing about it (stageOpts.Recoverable).
type recoveredError struct{ err error }

func (e recoveredError) Error() string { return e.err.Error() }
func (e recoveredError) Unwrap() error { return e.err }

// stageOpts adjust how stage treats the agent's error.
type stageOpts struct {
	// Recoverable, when set, picks the agent errors the caller handles
	// itself: stage records nothing about them (no failure reason, no
	// log tail) and returns them.
	Recoverable func(error) bool
}

// stage runs one agent stage and reports whether the loop may continue,
// with the agent's error. The error is only for the caller to inspect: a
// failure is already recorded, unless it is a recoveredError.
func (r *run) stage(ctx context.Context, name string, req agent.Request, opts stageOpts) (agent.Result, bool, error) {
	if ctx.Err() != nil {
		r.fail(r.stageReason(ctx, name, StageError(name, ctx, r.budget, ctx.Err())))
		if errors.Is(context.Cause(ctx), ErrCancelled) {
			r.markCancelled()
		}
		return agent.Result{}, false, ctx.Err()
	}
	// A kill switch or the grace may have halted the run between stages.
	if h := r.haltValue(); h != nil {
		r.save(ctx)
		return agent.Result{}, false, &HaltError{*h}
	}
	if r.budget.Exhausted() {
		r.fail("time budget exhausted before stage " + name)
		return agent.Result{}, false, nil
	}
	// With the gateway on, nothing the agent or the repository wrote since
	// the last stage may send Claude Code around it.
	if r.gatewayOn() {
		if reason := r.checkSettingsRouting(); reason != "" {
			r.fail("stage " + name + ": " + reason)
			return agent.Result{}, false, errors.New(reason)
		}
	}
	pins := r.stagePins(name)
	if err := r.writeManagedSettings(pins); err != nil {
		r.fail("stage " + name + ": " + err.Error())
		return agent.Result{}, false, err
	}
	r.stageN[name]++
	n := r.stageN[name]
	started := r.d.Now()
	r.rec.Stage = name
	r.save(ctx)
	log := r.d.Log.With("stage", name)
	// Refresh before the redactors below are built, so they know the new
	// token. Bounded on its own: a slow or hanging provider call must not
	// eat into the stage's own budget.
	authCtx, cancelAuth := context.WithTimeout(ctx, authRefreshTimeout)
	err := r.refreshGitAuth(authCtx, authValidity(r.budget.Stage+gitAuthSlack))
	cancelAuth()
	if err != nil {
		r.warnAuthRefresh(err)
	}
	log.Info("stage started", "n", n)

	var transcript bytes.Buffer
	stderrTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	transcriptTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	// The relay logs the agent's events live (design §10). It sits behind
	// the transcript's redactor and redacts again after decoding. It logs
	// synchronously in the agent's stdout path on purpose, as the stderr
	// LineWriter does: a stalled log stalls the agent rather than dropping
	// events or buffering without bound.
	relay := agent.NewRelay(log, r.secretList())
	tw := agent.NewRedactor(io.MultiWriter(&transcript, transcriptTail, relay), r.secretList())
	sw := agent.NewRedactor(io.MultiWriter(NewLineWriter(log, "agent"), stderrTail), r.secretList())
	req.Dir, req.Env, req.Transcript, req.Stderr = r.d.WorkDir, gitops.WithVars(r.env, pins), tw, sw
	req.Model, req.MaxBudgetUSD = r.cfg.Agent.ModelFor(config.StageRole(name)), r.cfg.Agent.MaxBudgetUSD

	deadlineCtx, cancelDeadline := r.budget.StageContext(ctx)
	defer cancelDeadline()
	if dl, ok := deadlineCtx.Deadline(); ok {
		r.beginBudgetStage(name, n, dl)
	}
	// The halt's way to stop this stage: cancelling with the halt as the
	// cause, which StageError reads.
	stageCtx, cancelCause := context.WithCancelCause(deadlineCtx)
	r.mu.Lock()
	r.haltStage = cancelCause
	extra := r.stageExtra
	early := r.halt
	r.mu.Unlock()
	if early != nil {
		// A halt recorded between the check above and now found no stage to
		// stop: stop this one before the agent starts.
		cancelCause(&HaltError{*early})
	}
	defer func() {
		r.mu.Lock()
		r.haltStage = nil
		r.mu.Unlock()
		cancelCause(nil)
	}()
	var watching sync.WaitGroup
	stopWatch := func() {}
	if r.gw != nil {
		r.beginGatewayStage(name)
		done := make(chan struct{})
		watching.Add(1)
		go func() { defer watching.Done(); r.watchHalt(stageCtx, done) }()
		stopWatch = sync.OnceFunc(func() { close(done); watching.Wait() })
	}
	res, err := r.d.Agent.Run(stageCtx, req)
	stopWatch()
	// An oauth run has no gateway: its stage's own cost figure is its
	// notional spend, reported whatever became of the stage.
	r.reportNotional(ctx, res)
	_ = tw.Flush()
	relay.Flush()
	_ = sw.Flush()
	if perr := r.d.Store.PutFile(context.WithoutCancel(ctx), fmt.Sprintf("transcripts/%s-%d.jsonl", name, n), transcript.Bytes(), "application/x-ndjson"); perr != nil {
		log.Warn("storing transcript failed", "err", perr)
	}
	// What the gateway saw comes first, whatever the agent reported.
	var violations []string
	if extra != nil {
		var gw int64
		violations, gw = extra(name)
		r.mu.Lock()
		r.gatewayTokens = gw
		r.mu.Unlock()
	}
	if r.gw != nil {
		// The gateway's figure is the run's cost; Claude Code's is only a
		// cross-check. A halt it raised after the watcher looked is
		// recorded now.
		r.drainHalt()
		r.crossCheckCost(name, res.CostUSD, stageCtx.Err() != nil || r.haltValue() != nil)
		r.mu.Lock()
		r.rec.CostUSD = r.gwUsed.USD()
		r.mu.Unlock()
	} else {
		r.rec.CostUSD += res.CostUSD
	}
	r.updateCost()
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: name, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds()})
	log.Info("stage finished", "n", n, "cost_usd", res.CostUSD, "err", err)

	// A halt recorded first decides the stage, whatever the agent says:
	// is_error, any subtype (even success) or exit code, a kill by the
	// halt's own cancel included. No log tail: nothing failed.
	if h := r.haltValue(); h != nil {
		r.save(ctx)
		return res, false, &HaltError{*h}
	}
	if len(violations) > 0 {
		r.fail("stage " + name + ": " + violations[0])
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false, fmt.Errorf("stage %s: %s", name, violations[0])
	}

	switch {
	case err != nil && opts.Recoverable != nil && stageCtx.Err() == nil && opts.Recoverable(err):
		r.save(ctx)
		return res, false, recoveredError{err}
	case err != nil:
		reason := r.stageReason(ctx, name, StageError(name, stageCtx, r.budget, err))
		if w := r.lastWaited(); w > 0 && r.gw != nil {
			reason += fmt.Sprintf(" (the budget gateway told the agent to retry %d call(s) with a 429 because calls in flight held the run's budget; the run cap may be too small for parallel work)", w)
		}
		r.fail(reason)
		if errors.Is(context.Cause(stageCtx), ErrCancelled) {
			r.markCancelled()
		}
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false, err
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false, nil
	}
	r.save(ctx)
	return res, true, nil
}

// stageReason turns an "interrupted" stage reason into the cancel --now one
// when the cancel marker is there: cancel --now writes the marker, then stops
// the execution, so the runner gets SIGTERM before its poll sees the marker.
// Any other reason, and the recorded status, are left as they are.
func (r *run) stageReason(ctx context.Context, name, reason string) string {
	if !strings.HasPrefix(reason, interruptedPrefix) {
		return reason
	}
	marked := r.isCancelled()
	if !marked {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusCallTimeout)
		defer cancel()
		marked, _ = r.d.Store.CancelRequested(cctx)
	}
	if marked {
		return cancelNowPrefix + " during " + name
	}
	return reason
}

// keepTail remembers the failing stage's output for the draft PR (design
// §4.5): the end of its stderr, or of its transcript when stderr was empty.
// Both were redacted on the way in. Only the first failure is kept, as
// with fail.
func (r *run) keepTail(stage string, stderr, transcript *logtail.Writer) {
	if r.tail != nil {
		return
	}
	if lines := stderr.Lines(); len(lines) > 0 {
		r.tail = &LogTail{Source: "stage " + stage + ", stderr", Lines: lines}
	} else if lines := transcript.Lines(); len(lines) > 0 {
		r.tail = &LogTail{Source: "stage " + stage + ", transcript", Lines: lines}
	}
}

// logTail picks the log tail a draft PR carries: the failing stage's
// output if a stage failed, otherwise the output of the last verify run if
// that run failed. A ready PR carries none.
func (r *run) logTail(ready bool, records []verify.Record) *LogTail {
	if ready {
		return nil
	}
	if r.tail != nil {
		return r.tail
	}
	if len(records) == 0 {
		return nil
	}
	last := records[len(records)-1]
	if last.Passed {
		return nil
	}
	text, err := verify.LogTail(r.d.StateDir, last.N)
	if err != nil {
		r.d.Log.Warn("reading the verify log tail failed", "n", last.N, "err", err)
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// Redact whole lines first, then clip them to the published width, so
	// a secret straddling the cut is never partly published.
	lines := strings.Split(r.redact(text), "\n")
	for i, l := range lines {
		lines[i] = logtail.Clip(l, logtail.DefaultLineBytes)
	}
	return &LogTail{Source: fmt.Sprintf("fugaro verify %s #%d", last.Kind, last.N), Lines: lines}
}

func (r *run) finalize(ctx context.Context) error {
	r.rec.Stage = "finalize"
	r.save(ctx)
	base := r.cfg.Git.BaseBranch
	halt := r.haltValue()
	if halt != nil && r.isCancelled() {
		// haltNow and markCancelled exclude each other under the lock, so
		// this is a bug. The halt is kept.
		r.d.Log.Error("both a halt and a cancel were recorded; keeping the halt")
		if strictHaltCheck {
			panic("both a halt and a cancel were recorded")
		}
	}
	leftover := "fugaro: uncommitted work at finalize"
	if halt != nil {
		leftover = leftoverHaltMessage
	}
	committed, err := r.repo.CommitAll(ctx, leftover)
	if err != nil {
		return fmt.Errorf("committing leftover work: %w", err)
	}
	if r.follow == nil {
		// A follow-up's branch is already ahead of its base: when its
		// agent adds nothing, the report says so rather than an empty
		// commit.
		ahead, err := r.repo.AheadOf(ctx, base)
		if err != nil {
			return fmt.Errorf("counting commits ahead of %s: %w", base, err)
		}
		if ahead == 0 {
			r.fail("the agent made no commits")
			if err := r.repo.CommitEmpty(ctx, "fugaro: "+r.failure()); err != nil {
				return fmt.Errorf("recording an empty commit: %w", err)
			}
		}
	}
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("reading head sha: %w", err)
	}
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		return fmt.Errorf("reading verify records: %w", err)
	}
	// Stored in result.json and verify/<n>.json, like everything published.
	records = r.redactRecords(records)
	r.rec.HeadSHA, r.rec.Verify = sha, records
	r.uploadVerifyRecords(ctx, records)

	var last *runstore.ReviewSummary
	if n := len(r.rec.Reviews); n > 0 {
		last = &r.rec.Reviews[n-1]
	}
	ready, reason := Decide(records, sha, last)
	if committed && reason == ReasonNoVerifiedTest {
		reason = "uncommitted changes were committed at finalize, after the last verified test run"
	}
	if fr := r.failure(); fr != "" {
		ready, reason = false, fr
	}
	if halt != nil {
		// Never ready, and the reason is the halt's, whatever failed first.
		ready, reason = false, (&HaltError{*halt}).Error()
	}
	// Finalize's push must not outlive its token either; a short reserve
	// still asks for at least what bootstrap's fetch does.
	if err := r.refreshGitAuth(ctx, authValidity(max(r.wf.Timeouts.FinalizeReserve.Duration, bootstrapAuthMinValid))); err != nil {
		r.warnAuthRefresh(err)
	}
	if r.follow != nil {
		if done, err := r.pushFollowUp(ctx, records); done || err != nil {
			return err
		}
	} else if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		return fmt.Errorf("pushing %s: %w", r.rec.Branch, err)
	}
	// Saved at once, before anything is posted, so a run killed after
	// posting still records that it updated its pull request.
	r.rec.PushedHead = sha
	r.save(ctx)
	early := r.follow == nil && r.rec.PR != nil // opened at a stage boundary: settled by its number
	var spec gitprov.PRSpec
	switch {
	case r.follow != nil:
		// The pull request exists: only its draft state changes, and its
		// title and description stay as people may have edited them.
		spec = gitprov.PRSpec{Number: r.spec.PR, Branch: r.rec.Branch, Base: base, Draft: !ready}
	case early:
		// By the recorded number, never by branch: a PR a person closed
		// must not be answered with a second one.
		spec = gitprov.PRSpec{Number: r.rec.PR.Number, Branch: r.rec.Branch, Base: base, Draft: !ready}
	default:
		title, body := r.prText()
		if r.cfg.Git.PR.EarlyDraftOn() {
			// A section will be added, so the text is scrubbed of forged
			// markers and leaves it room.
			title, body = r.earlyPRText()
		}
		spec = gitprov.PRSpec{
			Branch: r.rec.Branch, Base: base, Title: title, Body: body, Draft: !ready,
		}
		if ready {
			// Only a PR created ready carries them; a draft spec never does.
			spec.Labels, spec.Reviewers = r.cfg.Git.PR.Labels, r.cfg.Git.PR.Reviewers
		}
	}
	pr, err := r.ensurePR(ctx, spec)
	r.notePR(ctx, pr) // the number is in the record before anything else is done
	if err != nil && early && pr.Number == 0 {
		// The PR is the early draft, whatever the failed call returned.
		pr = gitprov.PR{Number: r.rec.PR.Number, URL: r.rec.PR.URL, Draft: true}
	}
	if r.follow != nil {
		// The pull request bootstrap checked, whatever EnsurePR returned
		// alongside an error.
		pr = gitprov.PR{Number: r.spec.PR, URL: r.follow.pr.URL, Draft: pr.Draft}
	}
	if pr.DraftFallback {
		r.rec.DraftFallback = true
	}
	var partial *gitprov.PartialError
	switch {
	case errors.Is(err, gitprov.ErrPRNotOpen) && r.follow != nil:
		// Closed or merged after the push: nothing more may be posted.
		r.endUnchanged(ctx, fmt.Sprintf("PR #%d was closed during finalize; the branch was pushed", r.spec.PR), records)
		return nil
	case errors.Is(err, gitprov.ErrPRNotOpen) && early:
		// A person closed the PR during the run. Never open a second one.
		r.endUnchanged(ctx, fmt.Sprintf("PR #%d was %s during the run; the branch was pushed", r.rec.PR.Number, r.closedWord(ctx, r.rec.PR.Number)), records)
		return nil
	case errors.As(err, &partial):
		r.d.Log.Warn("pull request settings not fully applied", "err", r.redact(err.Error()))
		if ready && pr.Draft {
			// EnsurePR's adapters can only err this way in the
			// conservative direction: the PR was left looking more
			// like a draft than requested, never more ready. A ready
			// outcome must never be reported unless the PR really is
			// ready, so this is recorded as a draft with a reason
			// naming the failure.
			ready, reason = false, r.redact(err.Error())
		}
	case err != nil:
		if pr.Number != 0 && r.giveUpNoteAllowed(ctx) {
			// The PR exists but its state could not be settled, and it may
			// even look ready: say so on the PR itself, best effort.
			note := "**Fugaro:** this pull request is not ready. The run could not finish setting it up (" +
				r.redact(err.Error()) + "), so it may not reflect the run's outcome, and it may look ready when it is not. " +
				"Please check it by hand.\n" + gitprov.ReportMarker(r.rec.RunID) + "\n"
			// ensurePR may have given up because finalize's reserve ran
			// out, which is when this warning matters most: post it on a
			// short context of its own, detached from that deadline.
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
			cerr := r.provider.Comment(cctx, pr, note)
			cancel()
			if cerr != nil {
				r.d.Log.Warn("posting the not-ready comment failed", "err", r.redact(cerr.Error()))
			}
		}
		if early {
			// The draft still says it is running; best effort to say it stopped.
			stopped := "**Stopped:** the run could not finish setting up this pull request (" + inlineText(r.redact(err.Error())) + "). Check it by hand."
			if serr := r.settleText(ctx, r.rec.PR.Number, r.sectionLines(stopped, r.updated("stopped"))); serr != nil {
				r.d.Log.Warn("updating the pull request status failed", "err", r.redact(serr.Error()))
			}
		}
		err = fmt.Errorf("opening pull request: %w", err)
		if r.follow != nil {
			// The branch was pushed: the next follow-up quotes this run's
			// answer, and diagnose shows its report.
			r.storeUnposted(ctx, err, records)
		}
		return err
	}
	r.rec.Reason = reason
	switch {
	case ready:
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReady
	case halt != nil:
		r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeDraft, halt
	case r.isCancelled():
		r.rec.Status, r.rec.Outcome = runstore.StatusCancelled, runstore.OutcomeDraft
	default:
		r.rec.Status, r.rec.Outcome = runstore.StatusFailed, runstore.OutcomeDraft
	}
	r.updateCost()
	notes, gone := r.settlePR(ctx, pr, ready, reason, records)
	if gone {
		r.endUnchanged(ctx, fmt.Sprintf("PR #%d was %s during the run; the branch was pushed", pr.Number, r.closedWord(ctx, pr.Number)), records)
		return nil
	}
	var fu *FollowUpSection
	if r.follow != nil {
		fu = r.followUpSection()
		fu.MovedToDraft = r.follow.wasReady && !ready
	}
	report := agent.Redact(followUpReport(r.rec, r.d.Store.Prefix(), r.logTail(ready, records), fu, notes), r.secretList())
	// A follow-up's pull request is the one people watch: an earlier
	// attempt of this execution may already have posted this report.
	if r.follow != nil && r.reportPosted(ctx, pr) {
		r.d.Log.Info("the run report is already on the pull request; not posting it again")
	} else {
		// On a short context of its own: a slow status write or reviewer
		// request must not leave the report with an expired one.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
		err := r.provider.Comment(cctx, pr, report)
		cancel()
		if err != nil {
			r.d.Log.Warn("posting the run report failed", "err", r.redact(err.Error()))
		}
	}
	r.storeReport(ctx, report, fu)
	return nil
}

// closedWord says "merged" when the provider can tell the PR was merged,
// else "closed".
func (r *run) closedWord(ctx context.Context, n int) string {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusCallTimeout)
	defer cancel()
	if info, err := r.provider.PullRequest(cctx, n); err == nil && info.State == gitprov.PRMerged {
		return "merged"
	}
	return "closed"
}

// settlePR does what follows a PR's flip: the final status section (and, for
// a first run's early draft, the agent's final title and description), then,
// only if the PR is now ready, the reviewers and labels. gone is true when
// the PR turned out to be closed meanwhile; notes are for the report.
func (r *run) settlePR(ctx context.Context, pr gitprov.PR, ready bool, reason string, records []verify.Record) (notes []string, gone bool) {
	if pr.DraftFallback {
		notes = append(notes, "this host has no draft pull requests: the PR is a normal one marked [DRAFT], so reviews may already have been requested")
	}
	// early_draft=false keeps a first run's finalize exactly as it was: no
	// section, and the reviewers go with the creation of a ready PR.
	early := r.follow != nil || r.cfg.Git.PR.EarlyDraftOn()
	if early {
		section := r.finalSection(ready, reason, records)
		err := r.settleText(ctx, pr.Number, section)
		if err != nil && !errors.Is(err, gitprov.ErrPRNotOpen) {
			// One more try, so a ready PR does not keep saying Running.
			r.d.Log.Warn("updating the pull request status failed; retrying once", "pr", pr.Number, "err", r.redact(err.Error()))
			err = r.settleText(ctx, pr.Number, section)
		}
		if errors.Is(err, gitprov.ErrPRNotOpen) {
			return nil, true
		} else if err != nil {
			r.d.Log.Warn("updating the pull request status failed", "pr", pr.Number, "err", r.redact(err.Error()))
			notes = append(notes, "the pull request description could not be updated; its status section may be out of date")
		}
	}
	// Only a PR that is ready now gets reviewers: a follow-up only when it
	// is the one that flipped a draft to ready.
	if ready && !pr.Draft && (r.follow == nil && early || r.follow != nil && !r.follow.wasReady) {
		if note := r.applyReady(ctx, pr.Number); note != "" {
			notes = append(notes, note)
		}
	}
	return notes, false
}

// storeReport stores the report, and a follow-up's followup.md as the
// report quotes it, in the run's prefix.
func (r *run) storeReport(ctx context.Context, report string, fu *FollowUpSection) {
	if err := r.d.Store.PutFile(ctx, "report.md", []byte(report), "text/markdown"); err != nil {
		r.d.Log.Warn("storing the run report failed", "err", err)
	}
	if fu != nil && fu.Answer != "" {
		if err := r.d.Store.PutFile(ctx, followupFile, []byte(fu.Answer), "text/markdown"); err != nil {
			r.d.Log.Warn("storing followup.md failed", "err", err)
		}
	}
}

// ensurePR opens or updates the pull request, retrying a plain failure: a
// transient provider error at this point would otherwise leave a pushed
// branch with no pull request. EnsurePR finds what an earlier attempt
// created, so retrying never duplicates the PR. A *gitprov.PartialError is
// returned as is, with its populated PR, rather than retried: it means the
// PR exists but some settings, in the conservative direction, could not be
// applied. When retries run out on a plain error, the last populated PR is
// still returned alongside it, so the caller can keep it in the record.
func (r *run) ensurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	delay := r.d.RetryDelay
	if delay == 0 {
		delay = 3 * time.Second
	}
	var last gitprov.PR // the most recent populated PR seen across attempts
	for attempt := 1; ; attempt++ {
		pr, err := r.provider.EnsurePR(ctx, spec)
		if pr.Number != 0 {
			last = pr
			// Recorded the moment it exists, even when the call erred: a
			// retry, or a crash, then finds it by number.
			r.notePR(ctx, pr)
		}
		var partial *gitprov.PartialError
		if err == nil || errors.As(err, &partial) {
			return pr, err
		}
		if errors.Is(err, gitprov.ErrPRNotOpen) {
			// Merged or closed: retrying can't reopen it, and nothing
			// more may be done to it.
			return last, err
		}
		if attempt == prAttempts {
			return last, err
		}
		r.d.Log.Warn("opening the pull request failed; retrying", "attempt", attempt, "err", r.redact(err.Error()))
		select {
		case <-ctx.Done():
			return last, err
		case <-time.After(delay):
		}
	}
}

// uploadVerifyRecords stores each verify record as verify/<n>.json in the
// run's prefix (design §3.3). A failed upload is logged, not fatal: the
// records are also embedded in result.json.
func (r *run) uploadVerifyRecords(ctx context.Context, records []verify.Record) {
	for _, v := range records {
		data, err := json.MarshalIndent(v, "", "  ")
		if err == nil {
			err = r.d.Store.PutFile(ctx, fmt.Sprintf("verify/%d.json", v.N), data, "application/json")
		}
		if err != nil {
			r.d.Log.Warn("storing verify record failed", "n", v.N, "err", err)
		}
	}
}

// rawPRText returns the PR title and body the agent wrote to pr.md, or
// defaults built from the task. The agent's text is redacted before it is
// published.
func (r *run) rawPRText() (string, string) {
	if data, err := os.ReadFile(filepath.Join(r.d.StateDir, prFile)); err == nil {
		title, body, _ := strings.Cut(strings.TrimSpace(agent.Redact(string(data), r.secretList())), "\n")
		title = strings.TrimSpace(strings.TrimLeft(title, "# "))
		if title != "" {
			return title, strings.TrimSpace(body)
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(r.spec.Task), "\n")
	if runes := []rune(first); len(runes) > 72 {
		first = string(runes[:71]) + "…"
	}
	return first, fmt.Sprintf("Opened by Fugaro run `%s`.\n\n## Task\n\n%s", r.rec.RunID, r.spec.Task)
}

// Pull request text bounds. They keep well under GitHub's limits (titles
// of 256 characters, bodies of 65,536), so a long pr.md cannot fail
// finalize.
const (
	maxTitleRunes = 200
	maxBodyBytes  = 60000
)

const truncatedNote = "\n\n*(Truncated by Fugaro: the description was longer than a pull request allows.)*"

// prText returns the pull request's title and body, redacted and clipped
// to what every provider accepts.
func (r *run) prText() (string, string) {
	title, body := r.rawPRText()
	if runes := []rune(title); len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes-1]) + "…"
	}
	return title, clipBody(body, maxBodyBytes)
}

// clipBody cuts body to at most limit bytes, ending with a note when it
// was cut.
func clipBody(body string, limit int) string {
	if len(body) > limit {
		body = strings.ToValidUTF8(body[:limit-len(truncatedNote)], "") + truncatedNote
	}
	return body
}
