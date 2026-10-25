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
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
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
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
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
	// ProvisionTimeout bounds a checkout: clone job's mise install
	// (provision.go); zero means 10 minutes. Tests inject a small one to
	// exercise the bound.
	ProvisionTimeout time.Duration
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
	// Local says this process is a local invocation (`fugaro exec
	// --task-file`), as the CLI resolved it (backend.Platform.Local, design
	// compute-backend.md §2.5). It is the exception, not the rule: the zero
	// value means in the cloud, so a caller that forgets the field gets the
	// cloud's checks -- a missing Project fails bootstrap and the
	// routing-refusal escape knob is refused -- rather than a run with them
	// switched off.
	Local bool
	// ManagedSettingsPath is where the managed Claude Code settings are
	// written before each stage; empty means agent.ManagedSettingsPath.
	ManagedSettingsPath string
	// Spend is the job's budget, from SpendFromEnv; the zero value is off.
	Spend Spend
	// Providers are the owner's model providers (local config, never
	// fugaro.yaml). A pin or allow-list entry that is not Claude's must be
	// claimed by one that lists the repository in allow_data_to, or
	// bootstrap refuses the run before any call; the zero value has none.
	Providers map[string]config.ModelProvider
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
	d    Deps
	rec  *runstore.Record
	spec *task.Spec
	cfg  *config.Config
	wf   config.Workflow
	// providers are r.d.Providers with the project layer's claude_models
	// merged in (config.ApplyClaudeClaims), set once in resolveConfig --
	// the one place both entry points (bootstrap and readBaseConfig)
	// resolve a config -- and read by every rule that asks which route
	// serves a model, planOf's first-line decision included. It is never
	// written into the environment: internal/infra/spec.go writes
	// FUGARO_MODEL_PROVIDERS from the local config alone, so the claim
	// travels with the layer, which is re-read on every launch.
	providers map[string]config.ModelProvider
	// plan is the loop after implement, from the recipe resolved at
	// bootstrap (resolveRecipe) with its rounds decided (planOf); it always
	// ends with a review step.
	plan []planStep
	// gateSteps are the recipe's leading check steps (gate.go), run before
	// implement; nil for a recipe with none.
	gateSteps []recipe.Step
	// review is whether this run is mode: review (reviewmode.go, Task 12
	// of the generic-tool plan): set from spec.ReviewPRs at bootstrap,
	// before the recipe is resolved, the same way follow is set from
	// spec.IsFollowUp(); cross-checked against the resolved recipe's own
	// Mode once it is known, since the two must always agree.
	review bool
	// reviewHeads is what bootstrap's checkReviewPRs resolved for a review
	// run: one entry per spec.ReviewPRs, in order, each already past the
	// fork and open checks. Empty for any other run.
	reviewHeads []reviewHead
	// commentsFailed are the pull request numbers whose review comment
	// (reviewmode.go, postReviewComments) failed to post; reported in
	// review mode's report.md, never retried in a loop.
	commentsFailed []int
	// bounce counts how many times a senior rejection has sent the run back
	// to the cheap loop (review: {bounce: first_line}); 0 until the first
	// one. Stamped onto each first-line ReviewSummary (firstLine) so the
	// record shows which cheap loop a review followed.
	bounce  int
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
	spend  Spend
	policy policy.Effective
	// ceiling is the layer the merge started from: the job environment's
	// budget, or — when the project has published a policy — the policy node
	// (resolvePolicy). Error messages name the right one.
	ceiling policy.Layer
	// projPolicy and ident memoize the run's read of the project's policy and
	// the Firebase identity it is read with, so the node is read once, decoded
	// once and its token taken once (projectpolicy.go).
	projPolicy policyRead
	// job is the identity this run adopted from the Cloud Run job it is in,
	// computed and matched in admissionCheck (projectjob.go, plan task 3.3):
	// the (profile, provider, account) the job's own name was computed from.
	// A zero value means the run is in a per-repository job — or is not on
	// Cloud Run at all — and r.job.Profile == "" is how every reader tells
	// them apart (projectJob). It is written once, before any git credential
	// exists, and never from the environment: the provider, the admission and
	// the clone URL of a project job all come from it.
	job          jobTriple
	ident        budgetIdentity
	defFile      defaultBranchData
	instructions string
	reviewFile   string
	stageN       map[string]int
	// mu guards halt, cancelled, failReason, tokens, gatewayTokens and
	// haltStage: the cancel watcher and, with the gateway, its halt
	// callback record into them from other goroutines. Whichever of a halt
	// and a cancel is recorded first wins (see haltNow and markCancelled).
	mu         sync.Mutex
	failReason string
	cancelled  bool
	halt       *runstore.Halt
	// haltDone is closed by the first halt recorded (recordHaltLocked), so a
	// caller waiting outside a stage stops at once: the credential wait
	// (C1.17) sleeps for a reset that may be hours away, and nothing else
	// would end it. It is never closed twice and never nil'd after Run
	// creates it, so it is either open (no halt) or closed (halted).
	// Guarded by mu; created by Run.
	haltDone      chan struct{}
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
	// what its stages cost; all guarded by mu. gwUsed is the money its calls
	// settled -- the run's billed model spend -- and gwNotional is the tier-A
	// part of them, which is the subscription's and never the run's (C1.16,
	// D140): the two are disjoint, and their sum is the run's whole model
	// figure (record.CostUSD, cost.model_usd). Only a cascade run's calls
	// carry a tier, so gwNotional is 0 for every other run.
	gw           *gateway.Server
	gwAgent      *agent.Gateway
	gwClosed     bool
	gwUsed       pricing.Micros
	gwNotional   pricing.Micros
	lastStage    *gateway.StageReport
	modelBy      map[string]pricing.Micros
	routeBy      map[string]pricing.Micros
	routed       bool // the gateway has provider routes, so route_by exists
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
	// reported: the one the next fix resumes, and the one saveSession
	// uploads as the run's session.
	sessionID string
	// reviewSession is the session the latest review stage ran in (C1.17,
	// D138). It is saved too, under its own objects, and never becomes the
	// run's session: a review runs in a session of its own, and a follow-up
	// resumes the work, not the review of it.
	reviewSession string
	// follow is a follow-up's state; nil for a first run.
	follow *followState
	// pr is the early draft pull request's state (prflow.go).
	pr prFlow
	// baseSHA is the base branch's commit as bootstrap fetched it (the
	// local origin/<base> read right after the fetch, before the agent
	// runs), and startSHA the run branch's first commit, read right after
	// the checkout; both empty for a follow-up, which never checkpoints. A
	// checkpoint's scans, schedule and "ahead" check use these, never a ref
	// name such as origin/<base>: the agent controls the checkout while a
	// stage runs and could repoint a local ref, but not these already-read
	// values (checkpoint.go). They differ when the run starts from another
	// ref than its base branch (--ref develop with base_branch main).
	baseSHA, startSHA string
	// forkBase is the branch a first run's work grew from, when that is not
	// the ref it was cut from (forkbase.go), and forkBaseSettled says it was
	// decided: once, before the review stage names its range, and kept for
	// every diff after it so the review and the guard never disagree.
	forkBase        string
	forkBaseSettled bool
	// rigorCfg is the run's rigor configuration *as the base branch has it*
	// (docs/rigor-modes D25), read once at bootstrap and reused at every
	// later moment (rigor.go); rigorRev is the revision it was read from, ""
	// when the read could not be made, which the record leaves `revision`
	// absent for. It is deliberately not r.cfg's own rigor: for a first run
	// r.cfg comes from the checked-out ref, which is the artifact under
	// review, and the mode must not come from there.
	rigorCfg rigor.Config
	rigorRev string
	// filedFrom is the commit finalize's backlog filing was committed on
	// top of, "" when the run wrote no item at all: the report names it as
	// the commit the suite ran on (report.go's testsLine).
	filedFrom string
	// closedBacklog is the ids of the backlog items this run closed as the
	// follow-up that fixed them (D31, plan T4.7), in the order it closed
	// them: the pull request body's status section names them beside the
	// ids it filed. It is not a record field: the record's own Backlog is
	// what the run *filed*, and a reader of the branch has the closed
	// items themselves, whose `## Closed by` names this run's pull request.
	closedBacklog []string
	// ckpt is the checkpoint pushes' state (checkpoint.go).
	ckpt checkpointState
	// skipPR is set when the check gate already ended the run (clean, or an
	// infra error of its own, gate.go): finalize, which still runs after,
	// does nothing more. A review run and an artifact run set it for their
	// whole lives: neither ever pushes or opens a pull request.
	skipPR bool
	// artifact is whether this run is a mode: artifact run (design
	// artifact-runs.md §3): set at bootstrap from the task spec's Mode,
	// beside r.review, and confirmed against the resolved recipe's mode
	// there (an empty spec.Mode takes the recipe's). out is that recipe's
	// outputs: block, artifactPlan whether its steps lead with a plan
	// stage, and useWhen its use_when text for the plan prompt.
	artifact     bool
	out          *recipe.Outputs
	artifactPlan bool
	useWhen      string
	// renderRec is what the render step did, stored as Record.Render: the
	// same record the report and result.json are written from.
	renderRec *runstore.RenderRecord
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
// infra_error), or when writing the final record itself fails. Whatever error
// it returns is redacted (redactedErr): the caller prints it — internal/cli's
// exec hands it to the command's error and cmd/fugaro/main.go writes that to
// the job's stderr — and a git failure quotes the remote it was given.
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
		d:        d,
		stageN:   map[string]int{},
		haltDone: make(chan struct{}),
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
			err = r.redactErr(err)
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
				// The error is the run's own bootstrap failure — the one this
				// execution met before it could claim anything — and the
				// caller prints it, so it is redacted here like the ones
				// above and below. It is the third of the not-the-owner
				// returns: ErrDuplicateExecution and errOwnerUnknown come
				// back earlier, and this is the execution whose bootstrap
				// failed outright while another execution's record was
				// already in the bucket.
				err = r.redactErr(err)
				return
			case cerr != nil && err == nil:
				err = fmt.Errorf("writing run record: %w", cerr)
			}
			rec = r.rec
			err = r.redactErr(err)
			return
		}
		wctx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
		defer cancelWrite()
		if werr := writeRecord(d.Store, wctx, r.recordSnapshot()); werr != nil && err == nil {
			err = fmt.Errorf("writing run record: %w", werr)
		}
		rec = r.rec
		err = r.redactErr(err)
	}()

	r.addSealKey()
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
	switch {
	case r.artifact:
		r.runArtifactMode(runCtx)
	case r.review:
		r.runReviewMode(runCtx)
	default:
		r.runAgentLoop(runCtx)
	}
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
	if err := writeRecord(r.d.Store, wctx, r.recordSnapshot()); err != nil {
		r.d.Log.Warn("writing run record failed", "err", err)
	}
}

// recordSnapshot is the record as a write encodes it: a copy taken under the
// run's lock, so the encode -- which reads every field of the record -- cannot
// read a field another goroutine is writing.
//
// The record has two writers that are not the goroutine a save runs on: the
// credentials service's per-call answer, which names the credential the call
// was served with (recordCredential, C1.19) and logs the switch it made
// (recordSwitch, C1.16), and nothing else. Both hold r.mu, so a copy taken
// under it is consistent with them; every other field is written by the run's
// own goroutine, which is the one that saves, and by the checkpoint poll,
// which is a goroutine of its own and writes the run branch's tip (C1.10's
// poll) — the poll's own writes are ordered with its own save, which is what
// makes the copy enough here rather than a lock held around the write.
//
// The copy is shallow, and that is deliberate: the callback goroutines only
// ever append to the record's slices and set scalars, both under the lock, so
// what a snapshot's slice header covers cannot be rewritten after it — an
// append writes past the length the header was copied with. A field a
// callback goroutine ever comes to mutate in place (a map entry, say) would
// have to be deep-copied here, and this is the comment that says so.
func (r *run) recordSnapshot() *runstore.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec == nil {
		return nil
	}
	cp := *r.rec
	return &cp
}

// createRecord creates result.json if absent, within recordWriteTimeout.
func (r *run) createRecord(ctx context.Context) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	return createRecord(r.d.Store, wctx, r.rec)
}

// recordCredential is the cascade's own record (C1.19, D140): the name -- never
// the value, which never leaves the service -- of the credential the run's
// cascade answered a call with. It is written in two places, each the one its
// reader has: the record the run ends with (runstore.Record.OAuthAccount,
// which `fugaro runs` reads when a record exists) and the registry entry the
// dashboard reads while the run is running (budget.AgentEntry.Credential).
//
// The name is whatever the service answered, so it is written as one word: a
// name carrying a control or bidi character would have the whole PATCH denied
// and freeze the run's registry, which clipEntry's clipString already strips
// (internal/budget) -- this is the second bound, for the record, which no
// clipEntry sees.
//
// A cascade run is the only run whose agent.auth makes a credential that is not
// its own visible to it, and the gateway only ever forwards to the credentials
// service for one, so no test of agent.auth is needed here to keep another
// run's record untouched: a non-cascade run's answers carry no credential, the
// gateway having answered them itself with its own route's key.
//
// Its one caller is serviceAnswer (internal/runner/gateway.go), the callback
// startGateway hands the gateway: the write is here, with the run's other
// record writers, and the answer that decides it arrives there.
func (r *run) recordCredential(name string) {
	name = safetext.Field(name)
	if name == "" {
		return
	}
	r.mu.Lock()
	if r.rec != nil && r.rec.OAuthAccount != name {
		r.rec.OAuthAccount = name
	}
	r.mu.Unlock()
	r.noteRegistry(func(e *budget.AgentEntry) { e.Credential = name })
}

// recordSwitch writes the run's switch log (C1.16, D140): the call that just
// was answered moved from one credential to another, and what the service said
// about it -- from, to and the reason word -- joins the record beside the
// cache-miss estimate only the gateway can make (F24, gateway.SwitchCost).
//
// raw is the contract's switch header exactly as it arrived (the gateway
// validates nothing of its content: a switch is the run's record, not a ledger
// input), so it is read back with the contract's own reader -- credsvc's, the
// one the service writes it with -- rather than a second reading kept here. A
// header that reader refuses is logged and dropped: the switch is a record of
// what the run did and decides nothing, so a call whose switch this code cannot
// read is still a call that was made, and nothing here echoes the value (it is
// text from another process).
//
// Its caller is serviceAnswer (gateway.go), the same hand-off recordCredential
// has, and for the same reason: the answer is the only place the switch exists,
// and one writer of the record's switch log is what keeps its order the order
// the gateway settled the calls in.
func (r *run) recordSwitch(raw string, cost gateway.SwitchCost) {
	if raw == "" {
		return
	}
	h := http.Header{}
	h.Set(credsvc.HeaderSwitch, raw)
	sw, ok, err := credsvc.SwitchHeader(h)
	if err != nil || !ok {
		r.d.Log.Warn("the service's switch could not be read; the call is recorded without it")
		return
	}
	e := runstore.Switch{
		From: sw.From, To: sw.To, Reason: sw.Reason,
		CacheMissUSD: cost.USD, CacheMissUnmeasured: cost.Unmeasured,
	}
	r.mu.Lock()
	if r.rec != nil {
		r.rec.Switches = append(r.rec.Switches, e)
	}
	r.mu.Unlock()
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

// redactedErr is an error whose text is the redacted one: Error returns it,
// Unwrap returns the error underneath, so errors.Is and errors.As still find
// what the caller was matching on (Run's callers match ErrDuplicateExecution
// and errOwnerUnknown that way, and internal/cli matches the run's own error
// kinds through the layers above).
type redactedErr struct {
	err  error
	text string
}

func (e redactedErr) Error() string { return e.text }
func (e redactedErr) Unwrap() error { return e.err }

// redactErr is the error Run returns: the same error with its text redacted.
// The value Run returns is not only recorded — internal/cli/exec.go hands it to
// the command's own error, which cmd/fugaro/main.go prints, so in a job it is
// the process's stderr and then Cloud Logging. Git's own failures quote the
// remote they were given (internal/gitops writes "git <args>" with the URL
// among the args, and git's stderr quotes it too), so that text is redacted the
// same way the record's reason is, not only where the run publishes it.
func (r *run) redactErr(err error) error {
	if err == nil {
		return nil
	}
	return redactedErr{err: err, text: r.redact(err.Error())}
}

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

// addSealKey registers the launch's seal key with the redactor, before
// anything can print it. The key arrives in the job's environment — it is the
// value of the launcher's marker (Backend.TokenKey, D15) — so the runner holds
// it from its first moment, long before takeBudgetToken unseals anything with
// it, and every reason the run publishes from here on is redacted of it.
// That is why it is registered at the top of Run and not where the key is
// used: the parse that reads the marker cannot register anything (it runs
// before this process has a redactor at all, internal/cli's runExec), so the
// runner is the earliest holder that can.
//
// The value registered is both spellings the key can be printed in: the
// encoded one, because that is the form it travels in and therefore the form a
// print of the environment value would show, and the raw key bytes, because
// the redactor derives a secret's base64 spellings from the secret itself —
// so the raw form also covers an encoding this code did not think of.
func (r *run) addSealKey() {
	if len(r.d.Backend.TokenKey) > 0 {
		r.addSecret(token.EncodeKey(r.d.Backend.TokenKey))
		r.addSecret(string(r.d.Backend.TokenKey))
	}
}

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
	// With the installation's credentials service on, the App's key is the
	// service's and the provider was opened without one
	// (providers.FromEnv): this is where the run hands it the service the
	// installation opted into, so its installation tokens are minted there
	// and nowhere else (C2.2, D145). The install must happen before the
	// first GitAuth below, and it is not optional -- a provider that took
	// ServiceHeldKey refuses every token until one is installed, so a run
	// that skipped this would stop rather than mint in-job.
	src, err := r.serviceTokens()
	if err != nil {
		return err
	}
	if src != nil {
		if s, ok := p.(gitprov.ServiceTokenSetter); ok {
			s.UseServiceTokens(src)
		}
	}
	r.provider, r.providerKind, r.providerFrom = p, kind, from
	if err := r.refreshGitAuth(ctx, bootstrapAuthMinValid); err != nil {
		return fmt.Errorf("getting git credentials from the %s provider: %w", kind, err)
	}
	return nil
}

// serviceTokens returns the installation's credentials service as the source
// of this run's installation tokens, or nil when the installation has not
// opted into one (providers.EnvCredentialsServiceURL unset): with the
// service off the App's key is the job's own and its provider mints tokens
// itself, as it always did.
//
// A LOCAL run whose environment names a service is refused rather than
// served, in the words serviceUpstream refuses the same situation in (D149):
// `fugaro exec` runs on the developer's own credentials and never through
// the credentials service, and a local run has no run identity for a mint to
// be verified by, so the only thing the service could do here is refuse the
// run later, further from the reason.
//
// The two identities a mint is verified by are the run's own Firebase ID
// token (runIDToken) and the job's Google ID token for the service's URL
// (metadataIDToken), and the job name is what the run reports it is in. The
// App's ID is the job's own environment value: a number, not a credential.
//
// It is built per provider, so each provider has the cache ServiceTokens
// documents -- one mint per hour per provider, not one per API call.
func (r *run) serviceTokens() (*github.ServiceTokens, error) {
	url := envLookup(r.d.Env, providers.EnvCredentialsServiceURL)
	if url == "" {
		return nil, nil
	}
	if r.d.Local {
		return nil, fmt.Errorf("this is a local run (%s) and its environment names a credentials service in %s: %s runs on the developer's own credentials and never through the credentials service (D149), and a local run has no run identity for a mint to be verified by. Unset %s to use the credentials this environment holds, or run this as a job", execName, providers.EnvCredentialsServiceURL, execName, providers.EnvCredentialsServiceURL)
	}
	// The job name is a claim the service holds to its own rule and decides
	// nothing from (C1.10), and a run that cannot name its job is a refusal
	// there rather than a call that named something else: a jobName failure
	// is reported as no job at all, which the service refuses.
	job, err := r.jobName()
	if err != nil {
		job = ""
	}
	return &github.ServiceTokens{
		URL:   url,
		AppID: envLookup(r.d.Env, providers.EnvGitHubAppID),
		IDToken: func(ctx context.Context) (string, error) {
			return metadataIDToken(ctx, r.d.Env, url)
		},
		RunIDToken: r.runIDToken,
		Job:        job,
		Now:        r.d.Now,
	}, nil
}

// runIDToken is the run's own Firebase ID token: the identity the budget
// backend admitted the run by, which is what the credentials service
// verifies a mint with (C1.10). It is the session's token as the run has it
// now, and never a copy taken once -- the session refreshes it under the run.
//
// The session that admitted the run is the budget session (r.sess), and it
// does not exist yet when the run's first git credential is fetched: the
// clone comes before the budget opens, and the policy read is what exchanges
// the launcher's custom token first (exchangeIdentity), so that read's
// session is the run's identity until the budget session takes over. Both
// are the same identity -- the claim check of the second exchange is the
// first's own -- so which one answers is a question of what exists, not of
// which the service trusts.
//
// An empty answer is not a fallback: a run with neither session cannot be
// verified by the service, and ServiceTokens refuses rather than calling
// under the job's identity alone.
func (r *run) runIDToken() string {
	r.mu.Lock()
	sess, tok := r.sess, r.ident.tok
	r.mu.Unlock()
	if sess != nil {
		return sess.IDToken()()
	}
	if tok != nil {
		return tok.Token()
	}
	return ""
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
	// runner fetches and pushes for it. Neither does a review run's
	// (design generic-tool §3.6, G12): the runner fetches its checkout(s)
	// itself, and never pushes or opens anything on its behalf. Nor an
	// artifact run's (design artifact-runs.md §3): its deliverable goes to
	// the runs bucket, nothing is ever pushed, and the plan stage is the
	// only agent stage there is.
	if r.env != nil && r.follow == nil && !r.review && !r.artifact {
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

// openCheckout opens (or makes) the run's checkout at r.d.WorkDir: the one
// baked into the image when there is one, and otherwise a clone of the
// run's remote. A job that runs without a derived image
// (FUGARO_CHECKOUT=clone, design generic-tool.md §2, G4) has no baked
// checkout, so its clone is the blobless one gitops.CloneBlobless makes:
// full history, no file contents until the checkout of the task's ref needs
// them. cloneVars are the git credentials authVars built.
//
// The branch cloned is the remote's own default branch, not
// git.base_branch: that is in the repository's fugaro.yaml, which cannot be
// read before the checkout exists. It costs nothing — bootstrap's FetchBase
// brings origin/<base> up to date before anything reads it — and the task's
// own ref is fetched separately either way (CheckoutNewBranch).
//
// remote is where the clone comes from. It is r.cloneURL's answer for this run
// — computed from task.json, the job's provider and the policy on a project
// job, the job's environment's remote on a per-repository one (projectjob.go) —
// and never r.d.Remote read again here: the two must be the same string the
// admission check and the credential URL were built from.
func (r *run) openCheckout(ctx context.Context, remote string, cloneVars map[string]string) (*gitops.Repo, error) {
	env := gitops.WithVars(gitops.IdentityEnv(), cloneVars)
	// A checkout already here (baked into the image, or left by an earlier
	// run on the same container), or a baked job with none to open, is
	// OpenOrClone's case: there is no clone to choose the shape of.
	if _, err := gitops.Open(r.d.WorkDir, nil); err == nil || !r.cloneJob() {
		return gitops.OpenOrClone(ctx, r.d.WorkDir, remote, env, modelCredentialVars...)
	}
	if remote == "" {
		return nil, fmt.Errorf("%s is empty and this job has no repository to clone (FUGARO_CHECKOUT=%s with no remote)", r.d.WorkDir, config.CheckoutClone)
	}
	return gitops.CloneBlobless(ctx, r.d.WorkDir, remote, "", env, modelCredentialVars...)
}

func (r *run) bootstrap(ctx context.Context) error {
	spec, err := r.d.Store.ReadTask(ctx)
	if err != nil {
		return fmt.Errorf("reading task spec: %w", err)
	}
	r.spec = spec
	r.rec.RunID, r.rec.Repo = spec.RunID, spec.Repo
	// Read before the sync below moves the checkout off the baked commit.
	r.rec.Image = r.imageInfo(ctx)

	// Every credential the run's two remotes can carry — the job's own
	// FUGARO_REMOTE and the origin of a checkout already in the work
	// directory — is in the redactor before anything can quote either of
	// them: the run's own record, which the claim below is what writes, the
	// admission refusal further down, git's messages about a remote it could
	// not reach, and result.json.
	r.addRemoteSecrets(r.d.Remote)
	r.addRemoteSecrets(r.originURL(ctx))

	if r.owned {
		r.save(ctx)
	} else if err := r.claimRecord(ctx); err != nil {
		// Claimed before the lock, so two executions of one run are
		// always told apart here (design §4.7) — and before the admission
		// check below, so a duplicate is told it is a duplicate whatever
		// else about it would have been refused (fugaro exec exits a
		// duplicate as a remote error, and prints no record).
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
	if len(spec.ReviewPRs) > 0 {
		r.review = true
	}
	// An artifact run's shape comes from the TASK SPEC's mode, beside
	// r.review and for the same reason: resolveRecipe runs further down,
	// after the configuration is read, and the checkout arm above is
	// decided in between (design artifact-runs.md §3, plan Task 2). The
	// resolved recipe is cross-checked against this below, in both
	// directions.
	if spec.Mode == recipe.ModeArtifact {
		r.artifact = true
	}
	if err := stateDirOutsideCheckout(r.d.WorkDir, r.d.StateDir); err != nil {
		return err
	}

	// The run's admission, in the order the security property needs it:
	// task.json has been read, the project's policy is read here (memoized;
	// Task 1.3), and only then is the provider opened and the clone made. No
	// git credential is requested, nothing is cloned, and no lease, no
	// outcome and no spend is written, so a run that is not admitted for this
	// repository leaves exactly one trace: its own run record, claimed above
	// and carrying the refusal — which is what makes it visible to the
	// operator and to the launcher instead of looking like a run that never
	// started (R2-12). It is the record and nothing else: the claim above is
	// what makes this execution the run's owner, and a duplicate was turned
	// away there.
	if err := r.admissionCheck(ctx); err != nil {
		return err
	}

	// Where this run clones from (design §4.1(1), plan task 3.3 step 3). On a
	// project job the answer is computed from task.json's repository, the
	// job's own provider and the policy's clone URL for it, so FUGARO_REMOTE
	// is not read at all and a launcher who overrode it cannot point the run
	// at another repository; on a per-repository job it is the phase-1 answer
	// (a checkout's origin, else the job's remote), unchanged.
	remote, err := r.cloneURL(ctx)
	if err != nil {
		return err
	}
	if err := r.checkProjectCheckoutOrigin(ctx, remote); err != nil {
		return err
	}
	r.credURL = gitops.CredentialURL(remote)
	// The clone and fetch below may already need credentials, so the
	// provider is opened now if anything but fugaro.yaml names it.
	switch job, project := r.projectJob(); {
	case project:
		// On a project job the provider is the one the job's own name pins,
		// which admissionCheck matched (projectjob.go). FUGARO_GIT_PROVIDER is
		// not consulted here even as a default: a run that got this far has
		// already refused if the variable disagreed (admissionCheck).
		if err := r.openProvider(ctx, job.Provider, "the project job "+gcp.ProjectJobName(job.Profile, job.Provider, job.Account)); err != nil {
			return err
		}
	case r.d.ProviderKind != "":
		if err := r.openProvider(ctx, r.d.ProviderKind, "the --provider flag"); err != nil {
			return err
		}
	default:
		if kind := gitprov.KindForURL(remote); kind != "" {
			// remote may carry embedded userinfo (a token in the remote
			// URL); the message never quotes more than scheme://host.
			display := remote
			if r.credURL != "" {
				display = r.credURL
			}
			if err := r.openProvider(ctx, kind, "the origin URL "+display); err != nil {
				return err
			}
		}
	}
	cloneVars, err := r.authVars()
	if err != nil {
		return fmt.Errorf("building git credentials: %w", err)
	}
	repo, err := r.openCheckout(ctx, remote, cloneVars)
	if err != nil {
		return fmt.Errorf("opening checkout %s: %w", r.d.WorkDir, err)
	}
	r.repo = strip(repo)
	var cfg *config.Config
	var startErr error // reading a first run's start commit (checkpoints)
	switch {
	case r.follow != nil:
		// The pull request's branch, with the configuration of its base.
		if err := r.checkoutFollowUp(ctx, repo); err != nil {
			return err
		}
		r.rec.Branch = spec.Branch
		if cfg, err = r.readBaseConfig(ctx); err != nil {
			return err
		}
	case r.review:
		// mode: review skips CheckoutNewBranch entirely: there is no
		// branch of this run's own, and r.rec.Branch is set only as the
		// branch lock's key (acquireLock), never pushed or read back as a
		// ref. The configuration comes from the base branch, exactly as a
		// follow-up's does (readBaseConfig), since review mode never
		// trusts a pull request's own fugaro.yaml.
		r.rec.Branch = "fugaro/" + spec.RunID
		if cfg, err = r.readBaseConfig(ctx); err != nil {
			return err
		}
		// A clone job's clone is --no-checkout (gitops.CloneBlobless), and
		// review mode checks out no branch of its own, so it checks the
		// base out here — after readBaseConfig's fetch of origin/<ref>, and
		// before provision, which reads the mise config from the working
		// tree: the base branch's, the same branch the run's configuration
		// comes from, not whatever branch the remote happens to call its
		// default. (A review-only run checks the pull request's own head
		// out into this directory later, in checkReviewPRs.)
		if r.cloneJob() {
			if err := repo.CheckoutRef(ctx, "origin/"+spec.Ref); err != nil {
				return fmt.Errorf("checking out %s: %w", spec.Ref, err)
			}
		}
	default:
		branch := "fugaro/" + spec.RunID
		if err := repo.CheckoutNewBranch(ctx, spec.Ref, branch); err != nil {
			return fmt.Errorf("checking out %s: %w", spec.Ref, err)
		}
		r.rec.Branch = branch
		// The start commit, read at once and locally: a checkpoint never
		// pushes a tip at or behind it (nothing of the agent's yet), and it
		// is the base the workflow guard measures the run's changes
		// against (workBase) — the task's ref, which is not the base branch
		// when the run was started from another one. Recorded as it is read,
		// so what the guard compared against is in the record.
		r.startSHA, startErr = repo.CommitOf(ctx, "refs/heads/"+branch)
		r.rec.StartSHA = r.startSHA
		data, err := os.ReadFile(filepath.Join(r.d.WorkDir, "fugaro.yaml"))
		if err != nil {
			return fmt.Errorf("reading fugaro.yaml at %s: %w", spec.Ref, err)
		}
		if cfg, err = r.resolveConfig(data); err != nil {
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
	// The workflow's job profile, once the config is resolved and the
	// overrides are applied: the selection is a name the published policy must
	// define (plan task 3.1 step 1, the fail-closed half).
	if err := r.checkJobProfile(ctx, name, wf); err != nil {
		return err
	}
	// And the credential the job holds, against the one this run's own
	// configuration resolved to (plan task 3, design §4.4 rows 6 to 9): the
	// runner's own half of the launch gate, here because agent.auth is a fact
	// of the resolved config and the job identity is a fact of admissionCheck,
	// which ran before any git credential existed.
	if err := r.checkProjectJobAccount(ctx, cfg.Agent.Auth); err != nil {
		return err
	}
	rcp, rrec, err := r.resolveRecipe(ctx, cfg)
	if err != nil {
		return err
	}
	switch {
	case r.review && rcp.Mode != recipe.ModeReview:
		return fmt.Errorf("recipe %s is not mode: review, but the task names pull requests to review", rrec.Name)
	case !r.review && rcp.Mode == recipe.ModeReview:
		return fmt.Errorf("recipe %s is mode: review, but the task names no pull request to review", rrec.Name)
	}
	// A follow-up continues a pull request: it inherits that PR's branch
	// and base, pushes to the branch and reports on the PR. A mode:
	// artifact recipe renders a deliverable into the run's bucket and opens
	// no pull request at all (design artifact-runs.md §3), so there is no
	// pull request for one to continue — and a follow-up does not ask for
	// its recipe anyway: it INHERITS it (the previous task's embedded text,
	// a repository recipe carried by name, or the one the runner chose for
	// the previous run), so an artifact recipe can arrive here without
	// anyone choosing it for this task. Refused before any stage, and keyed
	// on the RESOLVED recipe rather than any mode the task carries, because
	// a follow-up spec carries none and an empty one takes the recipe's
	// (below). This is the backstop the launcher's own refusal cannot be:
	// a repository recipe by name is read here, at this run's base, and not
	// there (refuseFollowUpArtifactMode, internal/cli/recipes_resolve.go).
	if r.follow != nil && rcp.Mode == recipe.ModeArtifact {
		return fmt.Errorf("recipe %s is mode: artifact, which renders a deliverable into the run's bucket and opens no pull request; this task continues pull request #%d, so it cannot run that recipe", rrec.Name, spec.PR)
	}
	// The spec's mode and the resolved recipe's mode must agree, and an
	// EMPTY spec.Mode takes the resolved recipe's mode rather than
	// defaulting to implement: the launcher leaves the recipe to the runner
	// whenever it cannot resolve one (runnerChooses, internal/cli/run.go),
	// so a spec that carries no mode is a real launch and not a mistake.
	// Both directions are refused, because both are silent pull requests
	// otherwise: an artifact spec against a non-artifact recipe would run
	// the mode the recipe names instead, and a non-artifact spec against an
	// artifact recipe would run an implement loop and push a branch for a
	// recipe that asked for a file (design §8.2, Review Focus 10).
	mode := spec.Mode
	if mode == "" {
		mode = rcp.Mode
	}
	if mode != rcp.Mode {
		return fmt.Errorf("recipe %s is mode: %s, but the task says mode: %s", rrec.Name, rcp.Mode, spec.Mode)
	}
	r.artifact, r.out = rcp.Mode == recipe.ModeArtifact, rcp.Outputs
	// A task that attached files under a resolved recipe of another mode
	// fails the run before its first step (design §8.2): the launcher
	// refuses this for every launch that resolves a recipe, and this is the
	// arm that catches the ones it could not resolve — a recipe the runner
	// chose, or a hand-written task.json — where the inputs would otherwise
	// be downloaded into a run that commits a diff and opens a pull request,
	// and nothing in its steps reads inputs/. Keyed on the RESOLVED mode, not
	// on spec.Mode, because a spec is allowed to carry none.
	if len(spec.Inputs) > 0 && !r.artifact {
		return fmt.Errorf("the task attached %d file(s), but recipe %s is mode: %s: an attached file is only read by a mode: artifact run", len(spec.Inputs), rrec.Name, rcp.Mode)
	}
	if r.artifact {
		r.artifactPlan, r.useWhen = artifactHasPlan(rcp), r.redact(rcp.UseWhen)
		if err := r.downloadInputs(ctx, wf.Inputs); err != nil {
			return err
		}
	}
	if lo, hi, ok := recipe.ReviewPRRange(rrec.Name); ok {
		if n := len(spec.ReviewPRs); n < lo || n > hi {
			if lo == hi {
				return fmt.Errorf("recipe %s needs exactly %d pull request(s) to review, got %d", rrec.Name, lo, n)
			}
			return fmt.Errorf("recipe %s needs %d to %d pull requests to review, got %d", rrec.Name, lo, hi, n)
		}
	}
	r.rec.Recipe = &rrec
	derived := rrec.Name == recipe.DefaultName && rrec.Source == string(recipe.SourceCatalog)
	r.gateSteps = checkGateSteps(rcp)
	if err := checkGateCommands(rcp, wf, spec.Ref); err != nil {
		return fmt.Errorf("recipe %s: %w", rrec.Name, err)
	}
	if rcp.ReviewerIsCoder && cfg.Agent.Models.Reviewer != "" {
		r.d.Log.Info("agent.models.reviewer ignored: recipe maps reviewer to coder", "recipe", rrec.Name, "reviewer", cfg.Agent.Models.Reviewer)
	}
	if rcp.CoderIsReviewer && cfg.Agent.Models.Coder != "" {
		r.d.Log.Info("agent.models.coder ignored: recipe maps coder to reviewer", "recipe", rrec.Name, "coder", cfg.Agent.Models.Coder)
	}
	ApplyRoles(&cfg.Agent, rcp)
	r.cfg, r.wf, r.rec.Workflow, r.rec.BaseBranch = cfg, wf, name, cfg.Git.BaseBranch
	r.rec.FinalizeReserveS = wf.Timeouts.FinalizeReserve.Seconds()
	dl := r.lockDeadline()
	r.rec.Deadline = &dl
	if r.follow == nil && !r.review {
		// A read, so it comes before the lock; a follow-up or a review run
		// fetched its base, and read its configuration from it, above
		// (readBaseConfig); neither ever checkpoints.
		if err := repo.FetchBase(ctx, cfg.Git.BaseBranch); err != nil {
			return fmt.Errorf("fetching base %s: %w", cfg.Git.BaseBranch, err)
		}
		if fetchedBaseSeam != nil {
			fetchedBaseSeam()
		}
		// The commit just fetched, read locally right after the fetch and
		// before the agent ever runs: checkpoints use this SHA rather than
		// the local origin/<base> ref, which the agent could repoint once
		// it controls the checkout. A second ls-remote would name a newer
		// commit, one the checkout lacks, if the base moved meanwhile, and
		// every checkpoint's scan would fail. A failure only turns
		// checkpoints off for this run; it does not fail bootstrap.
		if cfg.Git.PR.CheckpointsOn() {
			sha, err := repo.CommitOf(ctx, "refs/remotes/origin/"+cfg.Git.BaseBranch)
			switch {
			case err != nil:
				r.d.Log.Warn("resolving the base commit failed; checkpoint pushes are off for this run", "err", r.redact(err.Error()))
			case startErr != nil:
				r.d.Log.Warn("resolving the run branch's start commit failed; checkpoint pushes are off for this run", "err", r.redact(startErr.Error()))
			default:
				r.baseSHA = sha
			}
		}
	}
	// The mode this run resolved, read from the base branch and recorded at
	// D22's first moment (rigor.go). Here, and not earlier: a first run has
	// just fetched its base above, and a follow-up's or a review run's
	// configuration already *is* the base's. A first run's own read asks
	// origin for the default branch, which is the file the project check and
	// the policy are read from — the checkout cannot choose it. It never
	// fails the run — the mode acts on nothing yet.
	r.bootstrapRigor(ctx)
	if err := r.checkProject(ctx, cfg); err != nil {
		return err
	}
	if err := r.resolvePolicy(ctx, cfg); err != nil {
		return err
	}
	// The plan is derived here, *after* the policy: the routing switch is
	// applied inside resolvePolicy and rewrites the pins the plan's
	// first-line decision is read off — Agent.FirstLineOn asks whether the
	// coder's model is claimed by a route and the reviewer's is not — so a
	// plan derived before it would answer from the pins the switch replaced.
	// Concretely, with `agent.first_line_review: auto` and a switch that
	// routes only the coder, the canonical coder pin is unclaimed before the
	// rewrite and claimed after it, and the earlier order silently dropped the
	// cheap first-line loop of exactly the runs a switch routed (the review
	// finding on this change). Nothing between here and the loop reads the
	// plan, so deriving it here changes nothing else, and the steps' rounds
	// and the trailing-review check are the same derivation as before.
	if !r.artifact {
		// An artifact run has no implement/fix/review loop: planOf builds
		// the ordinary loop's plan and checkPlan demands a trailing review
		// step, neither of which an artifact recipe's [plan], render shape
		// has. Its own stage list is read directly (artifactHasPlan), and
		// the render is the runner's (artifact.go).
		plan := planOf(rcp, derived, cfg.Agent, r.modelProviders(), spec.Overrides.ReviewRounds)
		if err := checkPlan(plan); err != nil {
			return fmt.Errorf("recipe %s: %w", rrec.Name, err)
		}
		r.plan = plan
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
		// Before provision, never after (review H1): checkPullRequest is
		// what refuses a pull request this run must not touch (its state,
		// its source repository, followup.allow_public), and provision runs
		// the *branch's own* mise config — code an earlier agent wrote,
		// which a mise config is free to make arbitrary. Nothing from that
		// branch may run before the checks that decide the branch may be
		// used at all.
		if err := r.checkPullRequest(ctx); err != nil {
			return err
		}
		// The branch the pull request targets NOW — read above, by
		// checkPullRequest, from the provider — is the base every diff this
		// follow-up takes is measured against (diffbase.go, diffBase). For
		// a pull request retargeted since the run before it (main ->
		// integration-0.9.6) that is not the ref this follow-up read its
		// configuration from. It is fetched below, with every other base the
		// run diffs against, once the review heads are resolved.
	}
	// A job that runs without a derived image installs the repository's
	// mise tools itself (provision.go, design generic-tool.md §2, G4): the
	// image installed none for it. It runs here, once the provider is
	// opened, so mise's own GitHub calls can use the run's git token where
	// provisionGitToken allows one, and before any stage records its own
	// time.
	if err := r.provision(ctx); err != nil {
		return err
	}
	if r.review {
		if err := r.checkReviewPRs(ctx); err != nil {
			return err
		}
	}
	// Every branch this run's diffs are measured against, brought in with
	// the run's own credentials (diffbase.go, fetchDiffBases): a first run's
	// --ref, which a clone job's --single-branch clone does not carry as a
	// remote-tracking ref at all, a follow-up's retargeted pull request base,
	// and each reviewed pull request's own base. Here, where all three are
	// resolved — the last of them by checkReviewPRs just above — and before
	// the prefetch reads them.
	r.fetchDiffBases(ctx)
	// A follow-up's and a review run's agent has no git credentials of its
	// own, and a blobless clone fetches file contents lazily through
	// origin: the runner brings in what the agent's own git will read while
	// it still has the credentials to do it (prefetch.go, review N1/M2).
	// Here, after the base and the review heads are resolved, and before
	// any stage runs.
	r.prefetchObjects(ctx)
	r.restoreCaches(ctx)
	if r.follow == nil && !r.review {
		// A follow-up or a review run fetched its base, and read these
		// from it, above (readBaseConfig).
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
		RepoDir: r.d.WorkDir, Build: wf.Commands.Build, Test: wf.Commands.Test, Lint: wf.Commands.Lint,
		RerunFailed: wf.Commands.RerunFailed, Reports: wf.Commands.Reports,
		TimeoutS: int(wf.Timeouts.Verify.Seconds()),
	}); err != nil {
		return fmt.Errorf("writing verify settings: %w", err)
	}
	// A review run's agent gets none of the workflow's secrets (design
	// generic-tool §3.6, G12): it runs a reviewed pull request's own,
	// untrusted checkout and prompt (a hook, an auto-loaded CLAUDE.md), so
	// a workflow secret in its environment would be reachable by that
	// code. The model credential it still needs (cfg.Agent.Auth, below)
	// is the one exception, and stays reachable by a trusted author's own
	// prompt injection: that is the residual risk the design accepts.
	var secretEnvs []string
	if !r.review {
		secretEnvs = make([]string, len(wf.Secrets))
		for i, s := range wf.Secrets {
			secretEnvs[i] = s.Env
		}
	} else {
		// Keeping secretEnvs empty stops agent.BuildEnv from putting these
		// in the agent's own environment, but the runner process still
		// holds their values in r.d.Env, readable by the agent through
		// /proc of a sibling process in the same container (SECURITY.md).
		// A review run's agent, unlike every other run's, reads an
		// untrusted checkout and prompt, so it must never be able to
		// exfiltrate one of these into the posted comment, the report or a
		// transcript unredacted: register the values for redaction
		// (r.secretList(), which every one of those goes through) even
		// though they never reach its environment. Because secretEnvs is
		// empty here, agent.BuildEnv never runs its own "secret %s is not
		// set" check for these (that check walks spec.Secrets, which
		// review mode leaves empty on purpose) — so this is the only place
		// a declared-but-unmounted workflow secret would ever be caught in
		// review mode; failing here, the same as BuildEnv would for every
		// other run, is this registration's whole point: a secret that
		// silently isn't here to redact is exactly the "last line of
		// defense" the comment above describes failing open.
		for _, s := range wf.Secrets {
			v := envLookup(r.d.Env, s.Env)
			if v == "" {
				return fmt.Errorf("secret %s is not set in the runner environment", s.Env)
			}
			r.addSecret(v)
		}
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
	if len(r.spec.Inputs) > 0 {
		// The directory the task's attachments were downloaded into, for
		// the run that has them (design §4 step 5). It is absolute, so a
		// step that changes directory still finds it, and it is set only
		// when the task attached something: a variable naming a directory
		// that does not exist is worse than no variable at all.
		set["FUGARO_INPUTS_DIR"] = filepath.Join(r.d.WorkDir, inputsDir)
	}
	for k, v := range gitops.Identity {
		set[k] = v
	}
	if r.follow == nil && !r.review && !r.artifact {
		// A follow-up's agent gets no git credentials and no GH_TOKEN
		// (design §6.1): the runner fetched the branch and pushes it, and
		// the comments arrive in the prompt. A review run's agent gets
		// none either (design generic-tool §3.6, G12): it only ever reads
		// the checkout, and the runner never pushes or opens anything on
		// its behalf. An artifact run's gets none for the same reason
		// (design artifact-runs.md §3): no branch is pushed, and the
		// deliverable is uploaded by the runner.
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
		// Docker stays false here, so the agent's environment has no
		// DOCKER_HOST: nothing yet tells this runner that its workflow asked
		// for `docker: true` (the workflow field and the runner's copy of it
		// are compute-backend Task 8's config plumbing). It fails closed: a
		// run with no socket mounted is a run with no daemon to point at,
		// and an operator's own exported DOCKER_HOST must not follow an
		// agent into one (design compute-backend.md §4.4 check 6).
		Docker: false,
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

// ConfigResolution is ResolveConfig's result: the pure core of
// (*run).resolveConfig, with everything the wrapper needs to set its own
// run record and log line, so both can share one resolution instead of
// two that could drift apart.
type ConfigResolution struct {
	Cfg      *config.Config
	Res      *config.Resolution
	Problems []config.Problem
	// Applied is whether Layer (below) was passed to config.Resolve: a
	// file whose project: and gcp_project: are not the layer's gets
	// Problems/Cfg as config.Resolve(data, nil) would.
	Applied bool
	// Layer is pl's own parsed layer, set whenever it parses (even when
	// it did not apply), nil only when pl itself was nil. LayerErr is
	// set instead when pl's own text fails to parse; Layer is nil then.
	Layer    *config.ProjectLayer
	LayerErr error
}

// ResolveConfig resolves fugaro.yaml (data) over pl, the task's embedded
// project layer (nil for none), anchored to the job's own project
// (project, r.d.Project — docs/design/layered-config.md §8). The layer
// applies only to a file whose project: and gcp_project: are the layer's;
// a launch from outside a checkout may carry one for a file that is not,
// and Applied says so. Exported so a consumer that must prove it resolves
// the same bytes the runner does (internal/cli/consumers_test.go,
// TestEveryConsumerResolvesTheSameBytes) calls the runner's own
// resolution, not a copy of its logic that could silently diverge from
// it; every other caller stays (*run).resolveConfig.
func ResolveConfig(data []byte, pl *task.ProjectLayer, project string) ConfigResolution {
	var out ConfigResolution
	var layer *config.ProjectLayer
	if pl != nil {
		l, ps := config.ParseProjectLayer([]byte(pl.YAML), config.LayerAnchor{Project: project})
		if len(ps) > 0 {
			out.LayerErr = fmt.Errorf("the task's project layer (sha256 %s) is invalid: %s", pl.SHA256, problemsText(ps))
			return out
		}
		out.Layer = l
		p, _ := config.ProjectOf(data)
		gcp, _ := config.GCPProjectOf(data)
		out.Applied = p == l.Project && gcp == l.GCPProject
		if out.Applied {
			layer = l
		}
	}
	out.Cfg, out.Res, out.Problems = config.Resolve(data, layer)
	return out
}

// resolveConfig resolves fugaro.yaml (data) over the task's project layer
// (docs/design/layered-config.md §8) and records which layer and which
// resolved config the run uses. The layer applies only to a file whose
// project: and gcp_project: are the layer's; a launch from outside a
// checkout may carry one for a file that is not, and the record says the
// layer was not applied. An invalid layer fails bootstrap, as an invalid
// fugaro.yaml does.
func (r *run) resolveConfig(data []byte) (*config.Config, error) {
	pl := r.spec.ProjectLayer
	out := ResolveConfig(data, pl, r.d.Project)
	if pl != nil {
		if out.LayerErr != nil {
			// Recorded before returning: an invalid layer still names the
			// run's layer for ls and diagnose (design §8, task 17), even
			// though it never resolved anything.
			r.rec.ProjectLayer = &runstore.ProjectLayerRecord{SHA256: pl.SHA256, Generation: pl.Generation}
			return nil, out.LayerErr
		}
		r.rec.ProjectLayer = &runstore.ProjectLayerRecord{SHA256: pl.SHA256, Generation: pl.Generation, Applied: out.Applied}
		if !out.Applied {
			r.d.Log.Info("project layer not applied: fugaro.yaml does not name its project and gcp_project", "layer_project", out.Layer.Project, "layer_gcp_project", out.Layer.GCPProject)
		}
	}
	if len(out.Problems) > 0 {
		return nil, fmt.Errorf("fugaro.yaml is invalid: %s", problemsText(out.Problems))
	}
	// The layer's claims are merged here, before anything reads a route:
	// bootstrap reaches this at runner.go's own first step, and the plan that
	// reads a claim — planOf's FirstLineOn about the reviewer, derived after
	// resolvePolicy returns, which is also where the routing switch rewrites
	// the pins it reads — is derived later still, well before checkBudget and
	// the gateway's routes. A merge any later leaves a layer-only claimed
	// senior looking unclaimed and the derived recipe keeps its cheap first
	// line. An overlap — a claim colliding with a route this run has — fails
	// bootstrap here, as a config error naming the key, before any call.
	//
	// ClaimsOnRoutes first, because the run's routes are the job's own
	// FUGARO_MODEL_PROVIDERS: written per repository, holding only the
	// providers that list that repository in allow_data_to. A claim naming
	// any other provider is a route *this* repository does not have, and
	// the run cannot tell that from a name nothing defines — refusing
	// either would break every repository the owner did not list for that
	// provider, including ones that never name the claimed model, while
	// `fugaro validate` calls the same layer valid. The name nothing
	// defines is refused there and by `fugaro config publish`, where the
	// installation config's whole table is known.
	r.providers = r.d.Providers
	if out.Applied {
		merged, ps := config.ApplyClaudeClaims(r.d.Providers, config.ClaimsOnRoutes(r.d.Providers, out.Layer.ClaudeModels))
		if len(ps) > 0 {
			return nil, fmt.Errorf("the project layer's claude_models: %s", problemsText(ps))
		}
		r.providers = merged
	}
	r.rec.ConfigSHA256 = out.Res.ConfigSHA256
	return out.Cfg, nil
}

// modelProviders are the providers this run routes by: Deps.Providers with
// the project layer's claims merged in when one applied (resolveConfig).
// The fallback is for a run that never resolved a config -- the unit tests
// that build one directly -- which reads the providers the environment
// carried, exactly as before.
func (r *run) modelProviders() map[string]config.ModelProvider {
	if r.providers != nil {
		return r.providers
	}
	return r.d.Providers
}

// problemsText is problems as one line, "; "-separated.
func problemsText(ps []config.Problem) string {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return strings.Join(msgs, "; ")
}

func (r *run) readRepoFile(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, rel))
	return string(data), err
}

// systemPromptData is what the implementing agent's system prompt is built
// from: the run's own state decides each conditional rule.
func (r *run) systemPromptData() PromptData {
	pd := PromptData{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, StateDir: r.d.StateDir,
		NoWorkflows: r.providerKind == gitprov.KindGitHub, Checkpoints: r.checkpointsOn(),
		LintConfigured: strings.TrimSpace(r.wf.Commands.Lint) != ""}
	if r.follow != nil {
		pd.FollowUp = followup.SystemPromptLines(r.promptData())
	}
	return pd
}

func (r *run) agentLoop(ctx context.Context) {
	sys := SystemPrompt(r.systemPromptData(), r.instructions)
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
	skipImplement := false
	if len(r.gateSteps) > 0 {
		r.rec.Stage = "check"
		r.save(ctx)
		switch g, err := r.runGate(ctx, r.gateSteps); {
		case err != nil:
			// Halt takes priority over everything else here, the same
			// convention the g.Clean case below follows: WatchCancel's
			// context is cancelled with ErrCancelled unconditionally, even
			// when markCancelled() loses the race to a concurrent haltNow
			// (a budget cap, a kill switch, the token cap), so
			// context.Cause(ctx) == ErrCancelled is not proof this was a
			// plain cancel — a halt may have landed first and must not be
			// lost as an infra_error.
			if h := r.haltValue(); h != nil {
				r.endHalted(ctx, h)
				return
			}
			// A cancel landing mid-command (verify.Run / procgroup.Run)
			// returns an ordinary context error, indistinguishable from any
			// other process failure by err alone: context.Cause(ctx) still
			// carries ErrCancelled, so it ends cancelled, not infra_error,
			// exactly as a cancel during any other stage does.
			if errors.Is(context.Cause(ctx), ErrCancelled) && r.markCancelled() {
				r.endCancelled(ctx, "cancelled during the check gate")
			} else {
				r.failInfra(ctx, err)
			}
			return
		case g.Clean:
			r.rec.Gate = "clean"
			// The gate's commands run outside r.stage(), so nothing re-checks
			// a halt (a budget cap, a kill switch, the token cap) recorded
			// while they ran; this path is the only one that would otherwise
			// finalize as succeeded despite it (fixed and red both still
			// reach a real r.stage() call before any outcome is decided,
			// which checks haltValue() at its own top). A halt ends the run
			// regardless of follow-up status.
			if h := r.haltValue(); h != nil {
				r.endHalted(ctx, h)
				return
			}
			if r.follow == nil {
				r.endWithoutPR(ctx, "every check passed: nothing to fix")
				return
			}
			// A follow-up: the launcher asked for a specific action (reply
			// to comments, apply instructions) that a clean gate must not
			// silently swallow. req.Prompt is already the follow-up's own
			// ImplementPrompt (set above); fall through to implement as
			// usual, on top of whatever the gate found (nothing, here).
		case g.Fixed:
			r.rec.Gate = "fixed"
			r.verifyOnce(ctx, verify.KindTest)
			// A follow-up still runs implement with its own instructions,
			// on top of the autofix commit the gate already made; only a
			// first run (nothing else asked of it) skips straight to review.
			if r.follow == nil {
				skipImplement = true
			}
		default:
			req.Prompt = GatePrompt(req.Prompt, g)
		}
	}
	// "" until implement actually runs and starts one: a Fixed gate skips
	// implement, so there is no session yet for fix (below) to resume.
	var sessionID string
	if !skipImplement {
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
		sessionID = req.SessionID
	}
	// The prompt as it is before the input each review round adds to it
	// (reviewPromptWithInput): the diff under review is built per round,
	// never once here, because a fix between two rounds changes the branch.
	// The base is the run's own base of work, not the branch its
	// configuration came from (diffbase.go, diffBase): a run started from
	// another ref must review its own change, not the whole of that ref's
	// divergence from the default branch.
	r.settleForkBase(ctx)
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.reviewPromptBase(), r.reviewDiffSize(ctx), r.baseUnreadable())
	if f := r.follow; f != nil {
		if add := followup.ReviewAddendum(f.sel, f.nonce); add != "" {
			reviewPrompt += "\n\n" + add
		}
	}
	tier := ""
	for _, st := range r.plan {
		switch st.Kind {
		case recipe.StepFirstLine:
			tier = runstore.TierSenior
			var ok bool
			if sessionID, ok = r.firstLine(ctx, st.Rounds, reviewPrompt, sys, sessionID); !ok {
				return
			}
		case recipe.StepReview:
			// The last step (recipe.Parse refuses any other order).
			r.seniorLoop(ctx, st, tier, reviewPrompt, sys, sessionID)
			return
		}
	}
}

// seniorLoop is a review step: up to st.Rounds senior reviews on the
// reviewer's model, each followed by a fix while it asks for changes. Its
// verdict alone decides readiness (seniorReview). tier is "senior" when a
// first line came before it, "" otherwise, as runs without a first line
// always recorded. When st.Bounce and st.FirstRounds > 0 (review:
// {bounce: first_line} with a first_line step before it), a rejection's fix
// is followed by another cheap loop (firstLine) instead of going straight to
// the next senior round; a bounce with FirstRounds == 0 behaves like a plain
// review step, since there is no cheap loop to bounce back to.
func (r *run) seniorLoop(ctx context.Context, st planStep, tier, reviewPrompt, sys, sessionID string) {
	for round := 1; round <= st.Rounds; round++ {
		req := agent.Request{Prompt: r.reviewPromptWithInput(ctx, reviewPrompt), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}
		res, ok, _ := r.stage(ctx, "review", req, stageOpts{})
		// The review's own session is saved like a work stage's (C1.17,
		// D138), and not made the one a fix resumes: see noteReviewSession.
		r.noteReviewSession(req, res)
		r.countTokens(res)
		if !ok {
			return
		}
		v := ParseVerdict(res)
		r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Tier: tier, Verdict: v.Verdict, Findings: len(v.Findings)})
		r.save(ctx)
		if r.capReached() || v.Verdict == "ship" || round == st.Rounds {
			return
		}
		r.afterStage(ctx, "review")
		if r.pr.gone {
			return
		}
		if sessionID, ok = r.fix(ctx, v, sys, sessionID); !ok {
			return
		}
		if st.Bounce && st.FirstRounds > 0 {
			r.bounce++
			if sessionID, ok = r.firstLine(ctx, st.FirstRounds, reviewPrompt, sys, sessionID); !ok {
				return
			}
		}
	}
}

// fix runs a fix stage for review v in the implement session and reports the
// session to go on with, and whether the loop may continue.
func (r *run) fix(ctx context.Context, v Verdict, sys, sessionID string) (string, bool) {
	if r.sessionID != "" {
		sessionID = r.sessionID
	}
	req := agent.Request{Prompt: FixPrompt(v), AppendSystemPrompt: sys}
	if sessionID == "" {
		// No session exists to resume: a Fixed check gate (gate.go) skipped
		// implement entirely, so this fix is the run's first real agent
		// stage. Start fresh, exactly as implement would have; resuming ""
		// (or any session no stage ever started) would raise
		// agent.ErrNoSession against the real binary, which this call,
		// unlike implement's, has no Recoverable handling for.
		req.SessionID = agent.NewSessionID()
	} else {
		req.SessionID, req.Resume = sessionID, true
	}
	res, ok, _ := r.stage(ctx, "fix", req, stageOpts{})
	r.noteSession(req, res)
	r.countTokens(res)
	if !ok || r.capReached() {
		return req.SessionID, false
	}
	r.afterStage(ctx, "fix")
	// D22's second moment: the fix just changed the tree, so the run resolves
	// its mode again from the diff it now carries — before the next round is
	// asked for, so a mode the run's own work reached is the one the rounds it
	// has left are spent at (rigor.go).
	r.roundRigor(ctx)
	return req.SessionID, !r.pr.gone
}

// firstLine is the first-line review (design m10 §11a): up to rounds
// reviews (the plan's first_line step) by the coder's model, each followed by a
// fix when it asks for changes. Its verdict never decides readiness: it is
// recorded with its tier, and the senior review that follows is told
// nothing of it. A first-line stage that fails or has no verdict is
// recorded and skipped, never failing the run; a halt or a cancel still
// ends it. It reports the session to go on with, and whether the run may
// go on to the senior review.
func (r *run) firstLine(ctx context.Context, rounds int, reviewPrompt, sys, sessionID string) (string, bool) {
	for round := 1; round <= rounds; round++ {
		req := agent.Request{Prompt: r.reviewPromptWithInput(ctx, reviewPrompt), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}
		res, ok, err := r.stage(ctx, "review_first", req, stageOpts{Skippable: true})
		r.noteReviewSession(req, res)
		r.countTokens(res)
		sum := runstore.ReviewSummary{Round: round, Tier: runstore.TierFirst, Verdict: "none", Bounce: r.bounce}
		var v Verdict
		switch skipped := (skippedError{}); {
		case errors.As(err, &skipped):
			r.d.Log.Warn("the first-line review failed; skipping it", "round", round, "err", r.redact(skipped.err.Error()))
		case !ok:
			return sessionID, false
		default:
			var parsed bool
			if v, parsed = TryVerdict(res); parsed {
				sum.Verdict, sum.Findings = v.Verdict, len(v.Findings)
			} else {
				r.d.Log.Warn("the first-line review produced no parseable verdict; skipping it", "round", round)
			}
		}
		r.rec.Reviews = append(r.rec.Reviews, sum)
		r.save(ctx)
		if r.capReached() {
			return sessionID, false
		}
		if sum.Verdict != "changes" {
			return sessionID, true
		}
		r.afterStage(ctx, "review_first")
		if r.pr.gone {
			return sessionID, false
		}
		var more bool
		if sessionID, more = r.fix(ctx, v, sys, sessionID); !more {
			return sessionID, false
		}
	}
	return sessionID, true
}

// seniorReview is the last review the readiness rule reads: the latest
// senior one, never a first-line review (a run that ended after only the
// first line has none, so it is not ready).
func seniorReview(rs []runstore.ReviewSummary) *runstore.ReviewSummary {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Tier != runstore.TierFirst {
			return &rs[i]
		}
	}
	return nil
}

// recoveredError is an agent error stage left to its caller, having
// recorded nothing about it (stageOpts.Recoverable).
type recoveredError struct{ err error }

func (e recoveredError) Error() string { return e.err.Error() }
func (e recoveredError) Unwrap() error { return e.err }

// skippedError is a failed stage the caller skips (stageOpts.Skippable):
// stage recorded no failure for it.
type skippedError struct{ err error }

func (e skippedError) Error() string { return e.err.Error() }
func (e skippedError) Unwrap() error { return e.err }

// stageOpts adjust how stage treats the agent's error.
type stageOpts struct {
	// Skippable marks a stage the run does without: when the agent fails
	// or reports an error (not a halt, a cancel, an exhausted time budget
	// or a violation), stage records no failure and returns a
	// skippedError.
	Skippable bool
	// Recoverable, when set, picks the agent errors the caller handles
	// itself: stage records nothing about them (no failure reason, no
	// log tail) and returns them.
	Recoverable func(error) bool
}

// stage runs one agent stage and reports whether the loop may continue,
// with the agent's error. The error is only for the caller to inspect: a
// failure is already recorded, unless it is a recoveredError.
//
// It is stageOnce in a loop, because of the credentials cascade (C1.17,
// D138): when the service answers its typed `exhausted_until` while a stage
// runs, that stage did not fail — the runner waits for the reset the answer
// names, inside the job's own timeout, and runs the very same stage again on
// the session it had started, with --resume. The stage's own session, not
// the run's: a review runs in a session of its own, and a wait during one
// resumes that one.
//
// The loop is bounded (maxCredentialResumes): each wait is bounded by
// waitForCredential's own check against the job's remaining time, but a
// reset that has already passed resumes the stage at once, and a service
// that kept answering that way would run the stage again and again with no
// wait between. The bound ends the run named (HaltNoCredential) instead.
func (r *run) stage(ctx context.Context, name string, req agent.Request, opts stageOpts) (agent.Result, bool, error) {
	// resumes is how many times this stage has already been resumed on an
	// exhausted answer, this one included.
	resumes := 0
	for {
		res, ok, err := r.stageOnce(ctx, name, req, opts)
		var ex *exhaustedError
		if !errors.As(err, &ex) {
			return res, ok, err
		}
		// The attempt that the exhausted answer ended spent what it spent,
		// and it is a whole agent process: its tokens go on the run's total
		// here, before the wait, because the caller's own countTokens only
		// ever sees the last attempt of this stage (countTokens, halt.go).
		// Without this a run could spend maxCredentialResumes attempts a
		// stage and have the ceiling count one.
		r.countTokens(res)
		if resumes++; resumes > maxCredentialResumes {
			h := runstore.Halt{Reason: runstore.HaltNoCredential, Scope: "run", At: r.d.Now().UTC(),
				Detail: fmt.Sprintf("the credential service answered that no credential was usable %d times while stage %s ran, the last until %s; the run stopped rather than resume the stage again (see 'fugaro accounts ls' for the accounts' states)",
					resumes, name, ex.Until.UTC().Format(time.RFC3339))}
			r.haltNow(h)
			return res, false, &HaltError{h}
		}
		// The session the ended stage ran in: what its result reported (the
		// result event's ID, else its init event's, so a stage killed before
		// its result still names it), or the one it was asked for when it
		// reported none — that one is the runner's own fresh ID, and its file
		// exists from the moment the agent started.
		session := res.SessionID
		if !agent.ValidSessionID(session) {
			session = req.SessionID
		}
		if werr := r.waitForCredential(ctx, name, ex.Until); werr != nil {
			return res, false, werr
		}
		r.d.Log.Info("the credential wait is over; resuming the stage", "stage", name, "session", session, "resume", resumes)
		// The resumed stage is the same stage, on the same session. A
		// further exhausted answer during it waits again, within the same
		// bound.
		req.SessionID, req.Resume = session, true
	}
}

// maxCredentialResumes bounds one stage's resume loop (C1.17). The reset an
// exhausted answer names is normally in the future, and the wait for it is
// the whole of the runner's reaction; but an answer naming an instant that
// has already passed resumes the stage at once, so a service that kept
// answering that way — a stale mark, a reset that keeps moving back — would
// run the stage again and again with no wait between, each attempt a whole
// agent process. Three is more than the cascade needs: the service's answer
// is already the wait-or-switch decision, and one wait covers every account
// that is exhausted at that instant. A fourth answer ends the run named.
const maxCredentialResumes = 3

// stageOnce is one attempt at a stage: the whole of what stage did before
// C1.17, including its own failure handling.
func (r *run) stageOnce(ctx context.Context, name string, req agent.Request, opts stageOpts) (agent.Result, bool, error) {
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
	if err := r.writeManagedSettings(name, pins); err != nil {
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
	relay.Dir = r.d.WorkDir
	relay.OnTool = func(s string) {
		r.mu.Lock()
		sess := r.sess
		r.mu.Unlock()
		if sess != nil {
			sess.Update(func(e *budget.AgentEntry) { e.Action = s })
		}
	}
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
	// Checkpoints run only while the agent does: stopped (and waited for)
	// the moment it returns, and by the defer if it panics.
	stopCheckpoints := r.startCheckpoints(stageCtx, name)
	defer stopCheckpoints()
	res, err := r.d.Agent.Run(stageCtx, req)
	stopCheckpoints()
	stopWatch()
	// An oauth run has no gateway: its stage's own cost figure is its
	// notional spend, reported whatever became of the stage.
	r.reportNotional(ctx, res)
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess != nil && err == nil {
		// Only a stage whose stream reached a result event has a real
		// token count: Usage/ModelUsage are set from that event alone, so
		// a killed, timed-out or crashed stage's res is a zero Result and
		// StageTokens() would be 0. Tokens is one key of the whole-entry
		// registry write, which a 0 (omitempty) would delete outright,
		// erasing the last completed stage's real total instead of simply
		// leaving it in place until a real completion overwrites it (the
		// same way beginBudgetStage leaves Tokens alone at stage start).
		tokens := res.StageTokens()
		sess.Update(func(e *budget.AgentEntry) { e.Tokens = tokens })
	}
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
		r.recordGatewayStageCost(name, res.CostUSD, stageCtx.Err() != nil || r.haltValue() != nil)
	} else {
		r.rec.CostUSD += res.CostUSD
	}
	r.updateCost()
	r.rec.Stages = append(r.rec.Stages, r.stageTiming(name, started, res.CostUSD))
	var errText any // the agent's error may carry a key an upstream echoed
	if err != nil {
		errText = r.redact(err.Error())
	}
	log.Info("stage finished", "n", n, "cost_usd", res.CostUSD, "err", errText)

	// A halt recorded first decides the stage, whatever the agent says:
	// is_error, any subtype (even success) or exit code, a kill by the
	// halt's own cancel included. No log tail: nothing failed.
	if h := r.haltValue(); h != nil {
		r.save(ctx)
		return res, false, &HaltError{*h}
	}
	// What the gateway saw a moment ago decides the stage before the
	// credential service's answer does: a call the gateway refused as a
	// violation — a model that is not pinned, a tool the policy forbids, a
	// request shape it will not forward — is a fact about what the agent
	// asked for, and a stage that is about to run again must not swallow it.
	// The stage fails, named, and the resume never happens (C1.17): the
	// refused call would simply be made again, and the record would lose the
	// reason the run is not to continue.
	if len(violations) > 0 {
		r.fail("stage " + name + ": " + violations[0])
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false, fmt.Errorf("stage %s: %s", name, violations[0])
	}
	// The credential service's typed answer (C1.17) ended this stage: it did
	// not fail, and nothing is recorded about it — stage waits for the reset
	// it names and resumes it. It comes after the halt check (a halt recorded
	// first still decides) and after the violations above.
	//
	// A stage whose work was already done when the answer arrived is not
	// resumed: the answer ends a stage that the exhaustion stopped, and an
	// agent that exited with a result event and no error finished its work,
	// so throwing that away to run the same prompt again would spend a stage
	// on nothing. The answer changes nothing for it; the next stage gets its
	// own.
	var exhausted *exhaustedError
	if errors.As(context.Cause(stageCtx), &exhausted) && (err != nil || res.IsError) {
		r.save(ctx)
		return res, false, exhausted
	}

	switch {
	case err != nil && opts.Recoverable != nil && stageCtx.Err() == nil && opts.Recoverable(err):
		r.save(ctx)
		return res, false, recoveredError{err}
	case err != nil && opts.Skippable && r.skippable(ctx, stageCtx):
		r.save(ctx)
		return res, false, skippedError{err}
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
	case res.IsError && opts.Skippable && r.skippable(ctx, stageCtx):
		r.save(ctx)
		return res, false, skippedError{fmt.Errorf("the agent reported an error (%s)", res.Subtype)}
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false, nil
	}
	r.save(ctx)
	return res, true, nil
}

// skippable reports whether a failed Skippable stage may be skipped: the run
// itself is fine (not cancelled or out of time) and only the stage failed.
// A halt is decided before this is asked.
func (r *run) skippable(ctx, stageCtx context.Context) bool {
	return ctx.Err() == nil && !r.budget.Exhausted() && !errors.Is(context.Cause(stageCtx), ErrCancelled)
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
	// The paths this run changed, recorded before anything else saves the
	// record, so every run that reaches finalize carries them — the ones
	// that end without a pull request included (design D27, V2.1.2).
	r.recordChangedFiles(ctx)
	r.save(ctx)
	if r.skipPR {
		// The check gate already ended the run (gate.go: endWithoutPR or
		// failInfra), status, outcome and the stored report all theirs:
		// there is nothing left to commit, push or open a pull request for.
		return nil
	}
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
		// commit. What is counted is the run's own commits — those after
		// the commit it was cut from that the base branch does not have —
		// not origin/<base>..HEAD: for a run cut from another ref (--ref
		// console) every one of that ref's commits is ahead of the base
		// branch, and "the agent made no commits" could never fire.
		ahead, err := r.runCommits(ctx)
		if err != nil {
			return fmt.Errorf("counting the run's own commits: %w", err)
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
	// Read once more now that the run's own commits are final: work the
	// agent left uncommitted was just committed above, and those paths are
	// this run's too. A failed read keeps the set read at the top of
	// finalize, which is still this run's work.
	r.recordChangedFiles(ctx)
	// The run's own diff is now measured, which is D22's third moment: the
	// mode is resolved from it before Decide reads the run's gates, so the
	// value a later slice's rules act on is the one the run's whole diff
	// answered (rigor.go).
	r.finalizeRigor()
	// What the run left open is filed so the branch a reviewer reads and the
	// pull request carry the items (D9). Filing commits, which moves HEAD, and
	// the suite is not run again on that commit: an item is Markdown rather
	// than code, so it cannot change the verdict the suite already gave, and a
	// suite slower than the finalize reserve would spend the context the push
	// needs. The readiness rule reads the commit the suite last ran on
	// (decided), which differs from the head this run pushes only by the items
	// — including when the filing follows a round that shipped, which is
	// Hotfix's deferral (D30, D15) and the ordinary case for that row.
	decided := sha
	if r.fileOpenItems(ctx) {
		r.filedFrom = decided
		if sha, err = r.repo.HeadSHA(ctx); err != nil {
			return fmt.Errorf("reading head sha after filing: %w", err)
		}
	}
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		return fmt.Errorf("reading verify records: %w", err)
	}
	// Stored in result.json and verify/<n>.json, like everything published.
	records = r.redactRecords(records)
	r.rec.HeadSHA, r.rec.Verify = sha, records
	r.uploadVerifyRecords(ctx, records)

	ready, reason := Decide(records, decided, seniorReview(r.rec.Reviews))
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
	} else if rej := r.workflowGuard(ctx); rej != nil {
		return r.endRefused(ctx, rej, r.prNumber(), records)
	} else if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		if rejected := r.asRefusal(err); rejected != nil {
			return r.endRefused(ctx, rejected, r.prNumber(), records)
		}
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
	report := agent.Redact(followUpReport(r.rec, r.d.Store.Prefix(), r.logTail(ready, records), fu, notes, r.filedFrom), r.secretList())
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

// openFindings is what this run left open: the last senior review, when it
// did not ship and its findings stand. A round that shipped left nothing, a
// run with no senior review at all — one that died before review, or ended
// after its first line — has nothing to file either, and a "none" verdict is
// a skipped review rather than a review with findings.
//
// One round's worth, not one per round: the fix loop answers an earlier
// round's findings, and only the last round's are what the run hands over.
func openFindings(rs []runstore.ReviewSummary) *runstore.ReviewSummary {
	last := seniorReview(rs)
	if last == nil || last.Findings == 0 || last.Verdict != "changes" {
		return nil
	}
	return last
}

// fileOpenItems writes this run's backlog items and closes the ones it fixed,
// and says whether it wrote anything (docs/rigor-modes/implementation-plan.md
// T4.5, T4.6, T4.7, D9, D26, D27, D30, D31). What it writes goes into this
// run's own checkout and one commit of its own — the same branch and the same
// pull request as the change they belong to, so the writing dirties nothing a
// reviewer was not already reading (D9) — and the ids it filed are recorded
// beside the round they came from.
//
// Whether a run writes at all is first the repository's opt-in — a rigor:
// block, or a project-layer rigor default (rigor.Config.Configured) — and then
// D30's per-mode table, read from internal/rigor (T4.6): a mode whose row files
// nothing of its own leaves the branch exactly as it was, so a Critical run's
// tree and pull request body are the ones main produces. That gate covers the
// closing too: the close is the same commit as a filing, and a mode with no row
// has no such commit. The table is asked rather than compared here, so a mode
// that files differently is a row in that package and not a branch in this one.
//
// A row carries two independent things a run can owe — the findings it
// tolerated and, for Hotfix alone, the fix it deferred (D30, D15) — and a run
// that owes both files both, in one commit: a hotfix that also could not
// satisfy its review owes the proper fix *and* the round, and writing one of
// them would drop the other exactly where the run has least to show for
// itself.
//
// A follow-up on a pull request is the other half of D31: the run that fixes an
// item is the run that closes it, so a follow-up whose own review shipped
// closes the items an earlier run on the same pull request filed for a round it
// answered (openRoundItems). Closing is the whole of what a follow-up does
// about an item that is already open: what a run files is D30's table and
// nothing else — one item per tolerated finding — so a follow-up that also left
// findings open files its own round's item beside the earlier one rather than
// suppressing it, and the pull request's status section says which ids this run
// filed and which it closed.
//
// The item is written before the run's diff is what a reviewer reads and
// after the record's own ChangedFiles was measured, so the item file is
// deliberately not part of "the paths this run changed" (D22's third moment
// reads that field, and an item about the run must not move the mode the run
// resolved).
//
// A write that cannot be made is a warning, never the run's outcome: the
// pull request, the report and the record are what the run is for, and an
// item that could not be written costs a follow-up, not the run.
//
// The title opens with the run's own id (filedTitle), because a repository is
// filed into by every run it has at once: two parallel runs that end at the
// same round with the same count would otherwise compose one title, one slug
// and one file name, and merging both branches would be a conflict over one
// path rather than a union of two.
func (r *run) fileOpenItems(ctx context.Context) bool {
	// A halted or cancelled run was stopped mid-loop: its last round is not
	// one it finished acting on, and its push needs the time a filing spends.
	if r.haltValue() != nil || r.isCancelled() {
		return false
	}
	// Filing is opt-in: a repository that configures no rigor resolves Pro
	// for its budget (D19) and files nothing, so its tree, pull request body
	// and record are the ones it had before modes existed.
	if !r.rigorCfg.Configured() {
		return false
	}
	files := rigor.Files(r.reachedRigor())
	if !files.Any() {
		return false
	}
	open := openFindings(r.rec.Reviews)
	// The items an earlier run on this pull request filed for a round and the
	// folder still holds open: this run's checkout carries them, because a run
	// writes into its own branch and a follow-up starts from that branch (D9).
	earlier := r.openRoundItems(ctx)
	// The deferral leads: it is the mode's own substance rather than a
	// review's residue, so it takes the day's first id.
	var owed []backlog.New
	var what []string
	if files.Deferred {
		owed = append(owed, r.filedItem(nil))
		what = append(what, "the follow-up this hotfix deferred")
	}
	// One item per tolerated finding, whatever the pull request already
	// carries (D30): a follow-up that left findings open is handing over a
	// round of its own, and an item open for an earlier run's round is not a
	// reason to leave this one with nothing. Round numbers restart per run, so
	// a later run cannot tell an earlier item's round from its own and could
	// not dedup on it; suppressing the filing while any item on the pull
	// request is open would instead lose a finding for good the moment that
	// item is closed (implementation-plan §7 Q5, Q8).
	if open != nil && files.Tolerated {
		owed = append(owed, r.filedItem(open))
		what = append(what, fmt.Sprintf("what review round %d left open", open.Round))
	}
	// What this run fixed: a follow-up whose review shipped answered the round
	// an earlier run left open, so those items are closed here, in the same
	// commit as anything this run files (D31).
	var closing []string
	if r.shippedItsRound() {
		closing = earlier
	}
	if len(owed) == 0 && len(closing) == 0 {
		return false
	}
	dir := filepath.Join(r.repo.Dir, backlog.Dir)
	ids := make([]string, 0, len(owed))
	for _, n := range owed {
		item, err := backlog.Write(dir, n)
		if err != nil {
			r.d.Log.Warn("filing the run's open items failed", "err", r.redact(err.Error()))
			return false
		}
		ids = append(ids, item.ID)
	}
	// A close that cannot be made is a warning per item rather than the end of
	// the sweep: the items of one pull request are independent, and one the
	// folder refuses must not leave the others open.
	closed := make([]string, 0, len(closing))
	for _, id := range closing {
		if _, err := backlog.Close(dir, id, r.pullRequestRef()); err != nil {
			r.d.Log.Warn("closing the item this run fixed failed", "item", id, "err", r.redact(err.Error()))
			continue
		}
		closed = append(closed, id)
	}
	if len(ids) == 0 && len(closed) == 0 {
		return false
	}
	if _, err := r.repo.CommitAll(ctx, backlogCommit(ids, what, closed)); err != nil {
		r.d.Log.Warn("committing the run's backlog items failed", "err", r.redact(err.Error()))
		return false
	}
	r.rec.Backlog = append(r.rec.Backlog, ids...)
	r.closedBacklog = append(r.closedBacklog, closed...)
	return true
}

// maxRunsOnOnePR bounds the walk back through the runs of one pull request
// (roundItemIDs). A chain longer than this is a loop or a pull request nothing
// else bounds; the walk stops either way, and what it has read is what the run
// acts on.
const maxRunsOnOnePR = 20

// openRoundItems is the ids an earlier run on this run's pull request filed for
// a review round, as the items this run's own checkout holds them and only
// while the folder still holds them open: the items D31's follow-up closes when
// its own review shipped.
//
// The checkout is the only place the statuses are read: an item is closed on
// the branch of the run that fixed it, so the folder a follow-up starts from
// carries the closes made before it (D9, D31). A folder that is not there holds
// none — the items have not reached this checkout — and the run then files what
// it owes rather than closing something it cannot see: a finding that
// disappears is the failure this feature risks (the plan's §7.5).
func (r *run) openRoundItems(ctx context.Context) []string {
	ids := r.roundItemsOnThisPR(ctx)
	if len(ids) == 0 {
		return nil
	}
	items, err := backlog.Load(filepath.Join(r.repo.Dir, backlog.Dir))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.d.Log.Warn("reading the backlog folder failed; the run closes nothing and files its own round", "err", r.redact(err.Error()))
		}
		return nil
	}
	open := map[string]bool{}
	for _, it := range items {
		if it.Status == "open" {
			open[it.ID] = true
		}
	}
	var out []string
	for _, id := range ids {
		if open[id] {
			out = append(out, id)
		}
	}
	return out
}

// roundItemsOnThisPR walks the run chain this follow-up hangs off — its
// previous run, and the runs before that while each record names one on this
// pull request (runstore.Record.FollowUp) — and collects the ids each run on
// the pull request filed for a review round. The chain is what makes "the same
// pull request" a fact rather than a guess: a record names the pull request it
// ran on and the run it followed, so the walk stops at the first run that was
// not on this one. A run's own record carries the ids it filed
// (runstore.Record.Backlog), which is what keeps this a read of what was filed
// rather than a reading of the item files' titles.
//
// A chain whose records cannot be read ends the walk where it is: what was read
// is what the run acts on, and an unreadable record is a warning rather than a
// refusal, like every other read on this path. A record that names a previous
// run that is not a run id ends it too, before that id names an object
// (runstore.Store.Sibling's own rule for its callers).
func (r *run) roundItemsOnThisPR(ctx context.Context) []string {
	pr, id := r.spec.PR, r.spec.PreviousRun
	if pr == 0 || id == "" || r.d.Store == nil {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for id != "" && !seen[id] && len(seen) < maxRunsOnOnePR {
		if !task.ValidRunID(id) {
			r.d.Log.Warn("a record names a previous run that is not a run id; the runs behind it are not read", "run", r.redact(id))
			return ids
		}
		seen[id] = true
		rec, err := r.d.Store.Sibling(id).ReadRecord(ctx)
		if err != nil {
			r.d.Log.Warn("an earlier run on this pull request has no readable record; the runs behind it are not read", "run", id, "err", r.redact(err.Error()))
			return ids
		}
		if rec.PR == nil || rec.PR.Number != pr {
			return ids
		}
		ids = append(ids, roundItemIDs(rec)...)
		if rec.FollowUp == nil {
			return ids
		}
		id = rec.FollowUp.PreviousRun
	}
	return ids
}

// roundItemIDs is the ids one run filed for a review round: what its record
// carries, less the deferral a hotfix's row files first (D30, fileOpenItems).
// A record whose last senior review left nothing open filed no round item, so
// whatever it filed — a hotfix's deferral alone — is not one, and a deferral is
// never closed by a follow-up that merely shipped its review: the run that
// revisits the area at its normal mode is the one that answers it (D30, D15).
func roundItemIDs(rec *runstore.Record) []string {
	if openFindings(rec.Reviews) == nil {
		return nil
	}
	if rec.Rigor != nil && rigor.Files(rec.Rigor.Mode).Deferred && len(rec.Backlog) > 0 {
		return rec.Backlog[1:]
	}
	return rec.Backlog
}

// shippedItsRound reports whether this run answered the round it was handed: it
// is a follow-up whose last senior review shipped. A run that left findings open
// did not fix them, and a run that never reached a senior review — one that died
// before review — fixed nothing, however empty its record's review list looks.
func (r *run) shippedItsRound() bool {
	if r.spec.PR == 0 || r.spec.PreviousRun == "" {
		return false
	}
	last := seniorReview(r.rec.Reviews)
	return last != nil && last.Verdict == "ship"
}

// pullRequestRef is the pull request this run is on, as D31's `## Closed by`
// section names one. Only a follow-up reaches it: an item is closed by the
// follow-up that fixed it, and a follow-up is the run whose spec names a pull
// request.
func (r *run) pullRequestRef() string { return fmt.Sprintf("#%d", r.spec.PR) }

// backlogCommit is the one commit message the run's backlog writes go into:
// what it filed and what it closed, each named by id.
func backlogCommit(ids, what, closed []string) string {
	var parts []string
	if len(ids) > 0 {
		parts = append(parts, fmt.Sprintf("file %s, %s", strings.Join(ids, " and "), strings.Join(what, " and ")))
	}
	if len(closed) > 0 {
		parts = append(parts, fmt.Sprintf("close %s, fixed by this pull request", strings.Join(closed, " and ")))
	}
	return "fugaro: " + strings.Join(parts, "; ")
}

// filedItem is one item this run files: the round it left open when the mode's
// row files what it tolerated, and the deferral Hotfix's row files whether or
// not a round was left open (D30). open is nil only in that second case, which
// is why the deferral's own text is what the item then carries.
func (r *run) filedItem(open *runstore.ReviewSummary) backlog.New {
	n := backlog.New{
		Mode:    r.filedMode(),
		Area:    r.filedArea(),
		FiledBy: r.filedBy(),
		FiledIn: "run:" + r.rec.RunID,
		Date:    r.d.Now().UTC().Format("2006-01-02"),
	}
	if open == nil {
		n.Title, n.Body = r.deferredTitle(), r.deferredBody()
		return n
	}
	n.Title, n.Body = r.filedTitle(open), r.filedBody(open)
	return n
}

// filedTitle is the item's title, and it opens with the run's id so that no
// two runs compose the same one (D26). A run's own checkout is all it can read
// — two parallel runs in one repository each allocate the day's sequence from
// their own tree, and a run cut from a base that does not yet carry another
// run's item sees nothing to differ from — so the run's id is the one part of
// the name that is run-unique. It leads rather than trails, because the slug
// is cut to MaxSlugRunes and a trailing id would be the first thing cut off a
// long title, which would put two runs back on one name.
//
// The id is also the one thing a reader of a shared backlog wants first: D29
// settles a project's items into one repository, where an item's run is not
// obvious from the title alone, and `filed-in` repeats it in the front matter.
func (r *run) filedTitle(open *runstore.ReviewSummary) string {
	return fmt.Sprintf("Run %s: review round %d left %s open", r.rec.RunID, open.Round, Plural(open.Findings, "finding"))
}

// filedMode is the mode the item records: the one the run resolved (D27), or
// `none` when the record carries no rigor block at all, which is the value a
// run that resolved nothing leaves — never a mode nobody chose.
func (r *run) filedMode() string {
	if r.rec.Rigor == nil || !rigor.Known(r.rec.Rigor.Mode) {
		return "none"
	}
	return string(r.rec.Rigor.Mode)
}

// filedArea is the area the item records: the names of the rules the run's
// paths matched, strictest first as the record carries them, or the layer
// that decided the mode when no rule matched — `project` for the project
// layer and `repo` for everything below it, since an item about no rule in
// particular is an item about the whole repository (D27).
func (r *run) filedArea() string {
	rg := r.rec.Rigor
	if rg == nil {
		return "repo"
	}
	if len(rg.Areas) > 0 {
		names := make([]string, 0, len(rg.Areas))
		for _, a := range rg.Areas {
			names = append(names, a.Name)
		}
		return strings.Join(names, ",")
	}
	if rg.From == rigor.FromProject {
		return "project"
	}
	return "repo"
}

// filedBy is the item's launcher: the login the task was requested by, which
// is who asked for the run (D27). An item is committed into the repository, so
// it never carries an email address (the owner's ruling of 2026-10-24): a
// launcher recorded as one — the cloud account `fugaro run` reads — is not
// shortened to a guessed login, and it records `unknown`, as does a task that
// names nobody, rather than the empty field D27's format refuses.
func (r *run) filedBy() string {
	if by := strings.TrimSpace(r.spec.RequestedBy); by != "" && !strings.Contains(by, "@") {
		return by
	}
	return "unknown"
}

// filedBody is the item's body: what the round left open, why it is filed
// rather than fixed here, and what closes it (D27). The findings' own text is
// not carried yet — a round's findings are a count on the record and their
// text is in the run's transcript (T3.4 owns persisting them) — so the item
// says which round and how many, and links the run it came from.
func (r *run) filedBody(open *runstore.ReviewSummary) string {
	who := "senior review"
	if open.Tier == runstore.TierFirst {
		who = "first-line review"
	}
	what := fmt.Sprintf("Round %d's %s ended `%s` with %s: the run did not ship them, so they are carried here rather than fixed in this pull request.",
		open.Round, who, open.Verdict, Plural(open.Findings, "finding"))
	why := "The run left them open: they are not what this pull request's outcome was decided on, and an item filed here is a record a later reader can cite, which a finding in a transcript is not (D9)."
	done := "The pull request that addresses them closes this item (D31)."
	links := []string{fmt.Sprintf("run `%s`", r.rec.RunID)}
	if r.rec.PR != nil && r.rec.PR.Number != 0 {
		links = append(links, fmt.Sprintf("pull request #%d", r.rec.PR.Number))
	}
	return backlog.Body(what, why, done, links)
}

// deferredTitle is the title of the item a hotfix files for its own deferral,
// which is the proper fix, because the one it shipped is the expedient one
// (spec.md, D15, D30). It opens with the run's id for the same reason
// filedTitle does (D26).
func (r *run) deferredTitle() string {
	return fmt.Sprintf("Run %s: revisit this hotfix in the area's normal mode", r.rec.RunID)
}

// deferredBody is that item's body: what the hotfix deferred, why it is filed
// rather than done here, and what closes it. Nothing else carries this — the
// deferred text the agent wrote is the agent's own free-form file, which no
// part of the run parses — so the item says what a hotfix is rather than
// inventing a summary of the change.
func (r *run) deferredBody() string {
	what := "This run was a hotfix: it made the smallest change that stops the bleeding, and revisiting the fix properly is what it deferred (spec.md, D15)."
	why := "Time is the dominant constraint in Hotfix mode, so the fix ships first and is done properly afterwards — in the area's normal mode, which is a separate run rather than another round of this one."
	done := "The pull request that revisits the fix in the area's normal mode closes this item (D31)."
	links := []string{fmt.Sprintf("run `%s`", r.rec.RunID)}
	if r.rec.PR != nil && r.rec.PR.Number != 0 {
		links = append(links, fmt.Sprintf("pull request #%d", r.rec.PR.Number))
	}
	return backlog.Body(what, why, done, links)
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
//
// Without a pr.md the title is the run's own, from the ONE place the text
// comes from (task.Spec.TitleLine): the title its launcher stored (fugaro
// run --title), else the task prompt's first line. A run launched with a
// title also carries it as the body's first heading, so a brief whose first
// line is a shared preamble is told apart in the pull request itself and not
// only in its title; a run without one keeps the body it always had, byte
// for byte.
func (r *run) rawPRText() (string, string) {
	if data, err := os.ReadFile(filepath.Join(r.d.StateDir, prFile)); err == nil {
		title, body, _ := strings.Cut(strings.TrimSpace(agent.Redact(string(data), r.secretList())), "\n")
		title = strings.TrimSpace(strings.TrimLeft(title, "# "))
		if title != "" {
			return title, strings.TrimSpace(body)
		}
	}
	first := r.spec.TitleLine()
	if runes := []rune(first); len(runes) > 72 {
		first = string(runes[:71]) + "…"
	}
	body := fmt.Sprintf("Opened by Fugaro run `%s`.\n\n## Task\n\n%s", r.rec.RunID, r.spec.Task)
	if r.spec.Title != "" {
		body = fmt.Sprintf("Opened by Fugaro run `%s`.\n\n# %s\n\n## Task\n\n%s", r.rec.RunID, first, r.spec.Task)
	}
	return first, body
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
