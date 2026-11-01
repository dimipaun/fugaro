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
	"net/http"
	"net/url"
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
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/headroomproc"
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
	// CompressionForTest, for tests only, is the value a test's in-memory
	// config gets for agent.compression. It is applied after the config is
	// resolved and validated, so a test's fugaro.yaml never carries the key:
	// config.Validate refuses it while it is reserved (the plan's P11), and
	// what a test reaches through here is a run that is fully wired but
	// could not be asked for from repository content.
	CompressionForTest string
	// StartHeadroom, for tests only, starts the compression sidecar in place
	// of headroomproc.Start, so no test spawns a real process. Like every
	// Deps field it is built by Fugaro's own bootstrap code (cmd/fugaro,
	// internal/cli), never from repository content, so nothing on a run's
	// branch can reach it.
	StartHeadroom func(context.Context, headroomproc.Options) (SidecarProcess, error)
}

type run struct {
	d    Deps
	rec  *runstore.Record
	spec *task.Spec
	cfg  *config.Config
	wf   config.Workflow
	// cfgRes is how cfg was resolved: the layer each key cfg carries came
	// from (resolveConfig, set beside cfg). It is read to name the layer the
	// run's own timeouts.stage came from, which is the source of the bound a
	// dropped governance.stage_timeout is recorded against (T7.6); nothing
	// else reads it, and it is nil only for a run that resolved no config.
	cfgRes *config.Resolution
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
	// digest is whether the resolved recipe's first step is the digest step
	// (recipe.UsesDigest, T3.1): a report run, whose first step the runner
	// executes itself -- the reports it writes are its own work, with no
	// model call and no implement stage (digest.go, T3.2). Set once at
	// bootstrap, from the recipe the run resolved.
	digest bool
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
	// stampHooks and runnerHooks are the two hook directories bootstrap wrote
	// for this run's commits (writeStampHooks): the agent's stages commit
	// under stampHooks, chained to the repository's own hooks, and the
	// runner's own commits under runnerHooks, which chain nowhere. stampAuthor
	// is the author its commits claim, nil for a run that names none. All
	// three are empty for a run that never stamps — a review or an artifact
	// run, whose commits are not the run's (A3).
	stampHooks  string
	runnerHooks string
	stampAuthor *gitops.Person
	// secMu guards secrets: the budget session registers a refreshed ID
	// token from its own goroutine.
	secMu sync.RWMutex
	// spend and policy are the run's effective budget, merged once at
	// bootstrap (resolvePolicy) from the ceiling, the default branch and
	// the run's own fugaro.yaml. Nothing recomputes them, so an edit of
	// fugaro.yaml during the run changes neither.
	spend  Spend
	policy policy.Effective
	// govLayers are the three layers the policy merge consumed, kept as they
	// were handed to it: the effective policy cannot say which of them asked
	// for a governance key, and a build whose hook ignores governance resolves
	// none of them, so the layers themselves are what "somebody wrote this"
	// has to be read from (governance_hook.go, noteIgnoredStageBound).
	govLayers []policy.Layer
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
	// ignoredGovernanceSaid is whether the untagged build's one line about the
	// governance block it ignores has been written: the line is about the
	// build, so a run says it once however many stages it has (guarded by mu).
	ignoredGovernanceSaid bool
	stageExtra            func(stage string) (violations []string, tokens int64)
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
	gw *gateway.Server
	// compression is the context-compression sidecar Claude Code's calls go
	// through first, or nil when the run has none: startCompression sets it
	// from the sidecar it started, and it is what agent.BuildEnv and
	// writeManagedSettings point Claude Code at. No real run can have one
	// yet -- agent.compression is still refused by config.Validate (the
	// plan's P11) -- so Deps.CompressionForTest is the only way in.
	compression *agent.CompressionProxy
	// sidecar is the process behind compression, nil when there is none; it
	// is what fails a stage the sidecar dies under (watchSidecar) and what
	// stopCompression ends at run end.
	sidecar        SidecarProcess
	sidecarStopped bool
	gwAgent        *agent.Gateway
	gwClosed       bool
	gwUsed         pricing.Micros
	gwNotional     pricing.Micros
	// gwCacheRead and gwCacheWrite are the run's prompt-cache read and write
	// token totals, taken from the gateway's ledger when it closes: what the
	// run's record carries so a reader can see whether the cache behaved as
	// the design's compression risk (§3) says it should (P12).
	gwCacheRead  int64
	gwCacheWrite int64
	lastStage    *gateway.StageReport
	modelBy      map[string]pricing.Micros
	routeBy      map[string]pricing.Micros
	routed       bool // the gateway has provider routes, so route_by exists
	reported     pricing.Micros
	unreconciled pricing.Micros
	unparsed     int
	// serviceTiers are the service tiers the run's calls came back at that
	// the price table has no multiplier for, from every stage's report:
	// those calls were charged conservatively rather than failing the
	// stage, and the record carries the names so the figure they are in
	// explains itself (gateway.StageReport's ServiceTiers, design §6.1).
	// The set is bounded when the record is written, not while the run goes
	// on (gateway.ServiceTierSet.List).
	serviceTiers gateway.ServiceTierSet
	provider     gitprov.Provider
	providerKind string
	providerFrom string // where providerKind came from, for mismatch errors
	// identity is the author and committer this run's commits carry: nil
	// until resolveCommitIdentity reads one from the provider (a GitHub
	// App's bot account), and gitops.Identity for every run without one.
	identity   map[string]string
	credURL    string          // scheme://host of an HTTPS origin; "" when git needs no token
	auth       gitprov.GitAuth // current git credentials
	authWarned bool            // whether a mid-run refresh failure has already been logged
	tail       *LogTail        // output of the first failed stage, for the draft PR
	lock       *lock.Lock      // the branch lock, while held
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
	// shadow is whether this run is a challenger arm (shadow.go): its task
	// carried ab.publish: none, so nothing of it reaches the developer —
	// no push, no pull request, no comment, no git credential the agent
	// could push with. It is set at bootstrap, before the provider is even
	// opened, and never cleared.
	shadow bool
	// ab is the ab: block of the project layer this run read, nil when the
	// layer has none (or none applied): the list a challenger must be a
	// member of at bootstrap (applyChallenger), and what the one D522
	// capability line is about (noteIgnoredAB).
	ab *config.AB
	// abSaid is whether that line has been written; guarded by mu, for the
	// same reason as ignoredGovernanceSaid: it is about the build, so a run
	// says it once.
	abSaid bool
	// championCoder is a challenger arm's champion coder model, the one the
	// plan is derived from (applyChallenger); the routing switch rewrites it
	// with the run's own pins (pinSlots). Empty for every other run.
	championCoder string
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
	// narrowBase is the commit a first run's review range starts from when
	// it was narrowed to the run's own commits (diffbase.go,
	// narrowReviewRange), "" when the range is origin/<diffBase>. It is set
	// once, between stages, so no checkpoint goroutine reads it mid-write.
	narrowBase string
	// narrowText is narrowSummary's line, read once at finalize for both the
	// status section and the report.
	narrowText string
	// rigorCfg is the run's rigor configuration *as the base branch has it*
	// (docs/rigor-modes D25), read once at bootstrap and reused at every
	// later moment (rigor.go); rigorRev is the revision it was read from, ""
	// when the read could not be made, which the record leaves `revision`
	// absent for. It is deliberately not r.cfg's own rigor: for a first run
	// r.cfg comes from the checked-out ref, which is the artifact under
	// review, and the mode must not come from there.
	rigorCfg rigor.Config
	rigorRev string
	// rigorRaise is the last raise the run's mode took during the run, set by
	// the moment that took it (noteRigorRaise, rigor.go) and nil while the mode
	// has never risen above the one bootstrap resolved. It is kept here because
	// the record's block carries no per-moment history: which moment raised the
	// mode is what readiness reads (T3.5, outcome.go).
	rigorRaise *RigorRaise
	// lastSenior is the last senior review round as the loop read it: the
	// findings the record's summary only counts, kept because the mode's
	// threshold table reads their scores (outcome.go, T3.1). It is the round
	// seniorReview(r.rec.Reviews) returns, which is the only one readiness
	// reads.
	lastSenior seniorRound
	// ckpt is the checkpoint pushes' state (checkpoint.go).
	ckpt checkpointState
	// stageCkptStop is the running stage's checkpointer stop — startCheckpoints'
	// return — and nil between stages; guarded by mu. It is what lets a
	// mid-stage governance halt stop that goroutine and wait for it before it
	// writes any pull-request state of its own (governanceHalt, halt.go;
	// stopStageCheckpointer, checkpoint.go), so exactly one writer is left.
	stageCkptStop func()
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
		r.stopCompression()
		r.closeGateway()
		// The run is over and no commit of it is still to be made: the two
		// hook directories went with it. They are the one thing a run leaves
		// in a temporary directory every run writes to, and a directory left
		// behind is /tmp filling up with hooks of finished runs.
		r.removeStampHooks()
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
	// run's model cost is its ledger's. The sidecar in front of it goes with
	// it, for the same reason.
	r.stopCompression()
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
// poll) — the poll's own writes are ordered with its own save, and a save the
// governance halt makes while a poll is alive is ordered after that poll as
// well, because the halt stops it and waits for it first (governanceHalt,
// stopStageCheckpointer, checkpoint.go). Mid-stage that save is not the run
// goroutine's: a tripwire fires the halt on a goroutine of its own, which the
// run goroutine joins before its own unlocked writes (the paragraph below).
//
// What the copy does not cover is a writer that holds neither r.mu nor that
// ordering: the run goroutine's own unlocked writes between stages (the
// stage's cost and spend, afterStage's branch tip). No lock is held around
// them, so a save made from a goroutine they are not ordered with would encode
// a record they are still writing — which is why a governance halt must be
// called by the run goroutine or by one it joins before those writes
// (governanceHalt, halt.go). That is what makes the copy enough here rather
// than a lock held around the write.
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

// checkBitbucketAPIRoot refuses a cloud run whose Bitbucket API root
// (FUGARO_BITBUCKET_API_URL) is neither Bitbucket's own API nor a root the
// owner allow-listed in the published policy (D474, T2.8). It runs before the
// provider opens, so a launcher who overrode the job's environment -- the
// variable is launcher-writable -- cannot have the run send the token to a
// host of their own. A local invocation keeps the override: it runs on the
// developer's own credentials, not the job's (Deps.Local).
//
// The roots are the project's published bitbucket_api_roots, read through the
// run's one policy read (projectPolicy, memoized and already made by
// admissionCheck): a node the run cannot read stops it there, and no node, or
// a node with no roots, admits only the default.
func (r *run) checkBitbucketAPIRoot(ctx context.Context) error {
	if r.d.Local {
		return nil
	}
	root := envLookup(r.d.Env, providers.EnvBitbucketAPIURL)
	if root == "" {
		return nil
	}
	canonical := canonicalAPIRoot(root)
	if canonical != "" && canonical == canonicalAPIRoot(bitbucket.DefaultBaseURL) {
		return nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if found && p != nil {
		for _, allowed := range p.BitbucketAPIRoots {
			if c := canonicalAPIRoot(allowed); c != "" && c == canonical {
				return nil
			}
		}
	}
	where := "a root with no host"
	if u := parseAPIRoot(root); u != nil && u.Host != "" {
		where = "the host " + strconv.Quote(u.Host)
	}
	return fmt.Errorf("%s names %s, which is neither Bitbucket's own API (%s) nor a root this project's published policy allows: a cloud run sends its Bitbucket token to no other root. The owner allows the root by listing it in bitbucket_api_roots and running 'fugaro project policy publish'; until then, unset %s so the run uses Bitbucket's own API",
		providers.EnvBitbucketAPIURL, where, bitbucket.DefaultBaseURL, providers.EnvBitbucketAPIURL)
}

// canonicalAPIRoot is how a Bitbucket API root is compared: scheme and host
// case-folded and one trailing "/" trimmed, the normalization the node's own
// validator uses (projectpolicy.validBitbucketAPIRoots). It is an equality,
// never a prefix test -- a root that merely begins with an allowed one
// (https://api.bitbucket.org.evil.example/2.0) is another host and is refused.
// A value with no scheme or host canonicalizes to "" and matches nothing.
func canonicalAPIRoot(raw string) string {
	u := parseAPIRoot(raw)
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
}

// parseAPIRoot parses raw, nil when it is not a URL: a value's host is read
// for the refusal's message and its parts compared by canonicalAPIRoot, and
// neither is done on a string that is not a URL.
func parseAPIRoot(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}

// openProvider opens the provider of kind, which from names the source of
// (for error messages), and fetches its first git credentials.
func (r *run) openProvider(ctx context.Context, kind, from string) error {
	if kind == gitprov.KindBitbucket {
		if err := r.checkBitbucketAPIRoot(ctx); err != nil {
			return err
		}
	}
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
		if errors.Is(err, errNoReadOnlyToken) {
			// The arm's own boundary, not a provider that failed: the
			// provider this repository runs on has no read-only credential
			// to carry a challenger, so the run stops here, before the
			// clone (shadow.go, mintGitAuth).
			return r.refuseArm(err.Error())
		}
		return fmt.Errorf("getting git credentials from the %s provider: %w", kind, err)
	}
	r.resolveCommitIdentity(ctx)
	return nil
}

// resolveCommitIdentity sets the identity this run's commits carry, when the
// provider names one. GitHub links a commit's author and committer to
// accounts separately, by their own addresses, and a run sets both to that
// account's own noreply identity (gitops.BotIdentity): without this every
// run commit is authored by an address no account owns, so GitHub shows no
// author for it and CLA Assistant cannot tell whose it is (docs/git-providers.md).
//
// A provider with no account of its own (Bitbucket's, and the file-backed
// test provider) keeps gitops.Identity, and so does every failure here: the
// read is one API call a run can live without, so it is logged once and the
// run goes on rather than stopping over an identity.
//
// It runs in openProvider, which is called once per run and before the
// checkout when the provider's kind is known from the flag or the origin URL
// (bootstrap), and after it when fugaro.yaml names the provider. A checkout
// already open gets the identity in its environment here, so both orders
// commit as the same account.
func (r *run) resolveCommitIdentity(ctx context.Context) {
	reader, ok := r.provider.(gitprov.BotAccountReader)
	if !ok {
		return
	}
	account, err := reader.BotAccount(ctx)
	identity := gitops.BotIdentity(account.ID, account.Login)
	if err != nil || identity == nil {
		reason := "the provider named no account"
		if err != nil {
			reason = r.redact(err.Error())
		}
		r.d.Log.Warn("this run's commits carry the default identity: the git provider's bot account could not be read",
			"provider", r.providerKind, "reason", reason)
		return
	}
	r.identity = identity
	if r.repo != nil {
		r.repo.Env = gitops.WithVars(r.repo.Env, identity)
	}
}

// writeStampHooks writes the commit stamp of this run (A1, A3): two hook
// directories outside the checkout, one for the agent's own commits and one
// for the runner's, and the stamp the runner's own commits carry.
//
// Outside the checkout, because the hook directory is not part of the run's
// work: nothing in it is the agent's to read, commit, clean or push, and a
// directory inside the tree would put a file of the runner's in every diff of
// a commit the agent makes while the stamp is installed.
//
// Two directories, because the two kinds of commit want opposite things from
// the repository's own hooks. The agent's commits run under the directory
// chained to the repository's configured hooks — a repository's husky or
// lint-staged hook still runs on the agent's commits, which is the whole of
// constraint 2 — and the runner's own commits run under one that chains
// nowhere: the repository's configuration and $GIT_DIR/hooks are files and a
// directory the agent can write, and no code of the agent's runs inside a git
// call the runner makes.
//
// The runner's directory is made in a temporary directory of its own and not
// beside the agent's, because the agent's environment names the agent's one
// (StampEnv's core.hooksPath) and a sibling could be derived from it: an agent
// that could name the runner's directory could leave a prepare-commit-msg in
// it and have it run inside the runner's own commit. Nothing it writes in the
// checkout, or is told by its own stamp environment, reaches it.
//
// The chain directory is read once, here, so the 21 pass-throughs written for
// it each carry the answer instead of spawning a git of their own at every
// commit: `git rev-parse --git-path hooks` with the repository's own
// configuration, which is what core.hooksPath resolves to once the stamp's own
// environment entry is out of the way.
//
// The author is the launcher's, and only when the repository opted in
// (git.author: launcher) and the task carries one (Spec.Author, A2b): with no
// author here the commits are authored by the identity the run resolves, as
// every run's were before this (D606, constraint 3).
func (r *run) writeStampHooks(ctx context.Context) error {
	chain, err := r.repo.HooksDir(ctx)
	if err != nil {
		return fmt.Errorf("reading the repository's hook directory: %w", err)
	}
	agentRoot, err := os.MkdirTemp("", "fugaro-stamp-")
	if err != nil {
		return fmt.Errorf("making a directory for the run's commit hooks: %w", err)
	}
	r.stampHooks = filepath.Join(agentRoot, "agent")
	if err := gitops.WriteStampHooks(r.stampHooks, chain); err != nil {
		return fmt.Errorf("writing the run's commit hooks: %w", err)
	}
	// The second root is made once the first is written, and not beside it:
	// each root is named by the field removeStampHooks reads, so a failure
	// between the two would otherwise leave a directory nothing removes —
	// the one thing this pair of calls is here to prevent.
	runnerRoot, err := os.MkdirTemp("", "fugaro-stamp-")
	if err != nil {
		return fmt.Errorf("making a directory for the runner's commit hooks: %w", err)
	}
	r.runnerHooks = filepath.Join(runnerRoot, "runner")
	if err := gitops.WriteStampHooksNoChain(r.runnerHooks); err != nil {
		return fmt.Errorf("writing the runner's commit hooks: %w", err)
	}
	if r.cfg.Git.Author == config.GitAuthorLauncher && r.spec.Author != nil {
		r.stampAuthor = &gitops.Person{Name: r.spec.Author.Name, Email: r.spec.Author.Email}
	}
	r.repo.CommitHooks = r.runnerHooks
	r.repo.CommitStamp = func() gitops.Stamp {
		return gitops.Stamp{Author: r.stampAuthor, Trailers: r.stampTrailers("")}
	}
	return nil
}

// removeStampHooks takes the two hook directories writeStampHooks made back
// out of the temporary directory, root and all — the MkdirTemp root of each
// holds the one hook directory and nothing else. The run's last commit is
// already made by the time it runs, and a hook of a finished run is a file
// nothing is left to read: what the commit stamp travels in is the environment
// of the commit, which is gone with the run. It is called from Run's deferred
// handler, with the gateway's close and the lock's release: the paths that
// return early out of it are the ones where another execution owns the run,
// and those never reach bootstrap's writeStampHooks to have a directory to
// remove.
func (r *run) removeStampHooks() {
	temp := filepath.Clean(os.TempDir())
	for _, dir := range []string{r.stampHooks, r.runnerHooks} {
		if dir == "" {
			continue
		}
		// The root MkdirTemp made and nothing else: this removes the parent
		// of the directory it is given, so a hook directory anywhere but
		// directly under the temporary directory — a repository's own hooks,
		// if the two fields ever named one — is refused rather than having
		// its parent taken with it.
		root := filepath.Dir(dir)
		if filepath.Dir(root) != temp {
			r.d.Log.Warn("refusing to remove a commit hook directory that is not the run's own", "dir", dir)
			continue
		}
		if err := os.RemoveAll(root); err != nil {
			r.d.Log.Warn("removing the run's commit hook directory failed", "dir", dir, "err", err)
		}
	}
}

// stampTrailers is the trailer set every commit a run makes carries: the run's
// own ID and the rigor mode it has resolved (D601). model is the pin of the
// stage that made the commit and is left out when there is none — a stage that
// pinned no model, and every commit the runner makes itself, which belongs to
// no stage (D602).
func (r *run) stampTrailers(model string) []gitops.Trailer {
	trailers := []gitops.Trailer{
		{Key: gitops.TrailerRun, Value: r.rec.RunID},
		{Key: gitops.TrailerRigor, Value: string(r.reachedRigor())},
	}
	if model != "" {
		trailers = append(trailers, gitops.Trailer{Key: gitops.TrailerModel, Value: model})
	}
	return trailers
}

// stageEnv is the environment a stage's agent runs in: the run's own, the
// stage's pins, and the stamp every commit it makes carries (A3). The model
// trailer is this stage's pin, read the same way the record's StageTiming
// reads it (cost.go).
func (r *run) stageEnv(stage string, pins map[string]string) []string {
	env := gitops.WithVars(r.env, pins)
	if r.stampHooks == "" {
		return env
	}
	stamp := gitops.Stamp{Author: r.stampAuthor,
		Trailers: r.stampTrailers(r.cfg.Agent.ModelFor(config.StageRole(stage)))}
	return gitops.StampEnv(stamp, r.stampHooks, env)
}

// logUnstampedCommits writes the one line finalize owes about the commits it
// pushed: how many of the run's own commits carry no Fugaro-Run trailer
// (D603, constraint 6). The agent can unset its environment, name a hook
// directory of its own, or push the branch itself, so the trailers are a
// record and never proof — and the count is how a reader tells a run whose
// commits were stamped from one whose were not. Nothing is rewritten: a
// commit rewritten after it was made is a commit nothing verified.
func (r *run) logUnstampedCommits(ctx context.Context) {
	if r.repo == nil || r.stampHooks == "" {
		return
	}
	total, unstamped, err := r.unstampedCommits(ctx)
	if err != nil {
		r.d.Log.Warn("counting the run's commits without a Fugaro-Run trailer failed", "err", r.redactErr(err))
		return
	}
	r.d.Log.Info("the run's own commits and how many carry no Fugaro-Run trailer", "commits", total, "unstamped", unstamped)
}

// unstampedCommits counts the run's own commits and how many of them lack the
// trailer, in one git call: the boundary is guardBounds', which is the run's
// own work and never the checkout's origin/<base>, which the agent can repoint
// while a stage runs.
//
// The message is read through git's trailer placeholder rather than grepped:
// a Fugaro-Run line anywhere in the body is not a trailer, and a commit with
// an empty value has no run ID in it either. A trailer block prints as more
// than one line — one per value — so the lines that do not begin with a commit
// SHA belong to the commit before them.
func (r *run) unstampedCommits(ctx context.Context) (total, unstamped int, err error) {
	since, except := r.guardBounds(ctx)
	// --ignore-missing is what the other readers of the same bounds get from
	// gitops.readableBounds: a base branch's tip that this checkout never
	// fetched is a bound git ignores rather than a git that refuses the whole
	// range, so a base the remote moved on to cannot cost the run its count.
	args := []string{"--no-replace-objects", "log", "--ignore-missing",
		"--format=%H%x09%(trailers:key=" + gitops.TrailerRun + ",valueonly)",
		"--end-of-options", since + "..HEAD"}
	for _, e := range except {
		args = append(args, "^"+e)
	}
	out, err := sessionGit(ctx, r.repo, args...)
	if err != nil {
		return 0, 0, err
	}
	var shas []string
	stamped := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if sha, value, ok := strings.Cut(line, "\t"); ok && gitops.ValidCommitSHA(sha) {
			shas = append(shas, sha)
			stamped[sha] = strings.TrimSpace(value) != ""
			continue
		}
		if strings.TrimSpace(line) != "" && len(shas) > 0 {
			stamped[shas[len(shas)-1]] = true
		}
	}
	for _, sha := range shas {
		if !stamped[sha] {
			unstamped++
		}
	}
	return len(shas), unstamped, nil
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
	auth, err := r.mintGitAuth(ctx, minValid)
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
	// only agent stage there is. Nor a challenger arm's (A2.1): the runner
	// pushes nothing for it, and a credential in its environment would be
	// the one thing this store could still hand a developer's repository
	// through.
	if r.env != nil && r.follow == nil && !r.review && !r.artifact && !r.shadow {
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
	env := gitops.WithVars(gitops.IdentityEnvFor(r.identity), cloneVars)
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
	// The task's arm, before anything else the task's text can reach: an
	// arm refuses the run further down the very path an unsampled run
	// takes (shadow.go, readArm).
	if err := r.readArm(); err != nil {
		return err
	}
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
	// The digest step is the runner's own work (T3.2): it writes the run
	// reports the repository is missing and commits them, with no model call,
	// and planOf has no arm for it (it must be the first step of a mode:
	// implement recipe, recipe.order). Marked here, right after the recipe is
	// resolved and before any stage, and dispatched in agentLoop in place of
	// implement: a run that reached implement instead would spend a full
	// stage on the recipe's prompt and open an ordinary pull request in its
	// name — the opposite of what run-digest asks for. The launcher's image
	// gate refuses a digest launch against a release image that predates the
	// step (digestSince, internal/cli/recipes_skew.go).
	r.digest = recipe.UsesDigest(rcp)
	// A follow-up cannot run one whatever this image executes: a report run
	// is launched on its own, because it lists the records it may read and
	// refuses while another report run of the repository is going
	// (digestManifest, internal/cli/run_digest_manifest.go), and a follow-up
	// carries no manifest at all — the launcher writes one for a fresh
	// report launch only. Refused here, keyed on the RESOLVED recipe, which
	// is the arm the launcher's own report refusals cannot be: a repository
	// recipe by name is read here, at this run's base, and not there
	// (refuseFollowUpReportMode, internal/cli/recipes_resolve.go).
	if r.follow != nil && recipe.UsesDigest(rcp) {
		return fmt.Errorf("recipe %s holds the digest step, which writes this repository's run reports; this task continues pull request #%d, and a report run is launched on its own — it lists the records it may read and refuses while another report run of the repository is going — so it cannot continue one", rrec.Name, spec.PR)
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
	// The arm's coder model, after the recipe's own role mapping and before
	// anything reads a model: the routing switch and the pins resolvePolicy
	// applies, the stage loop, the budget session and checkBudget's
	// allow-list, price table and provider policy all judge the challenger
	// as the coder this run runs (shadow.go, applyChallenger).
	if err := r.applyChallenger(&r.cfg.Agent); err != nil {
		return err
	}
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
	// The tier the task carries and the expectation the policy just resolved
	// for it (tier.go). Here, and not earlier: the expectation is the run's
	// own governance's, which resolvePolicy settles, and the tier acts on
	// nothing yet.
	r.recordTier()
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
		//
		// The plan is derived from the champion's coder, never the arm's: the
		// arms share a shape (D555 keeps "the reviewer, recipe, mode, tier
		// and caps" the same), and the first-line step is derived from the
		// coder's own family — Agent.FirstLineOn asks whether the coder's
		// model is served by a route of this run's and the reviewer's is not
		// — so a challenger whose family is routed would otherwise gain a
		// stage the champion never had. Two runs whose stage sets differ are
		// not two runs that differ in the coder model, and their records,
		// costs and review findings would not be comparable. It is read after
		// resolvePolicy, as every other run's is, so the switch has rewritten
		// it like any pin.
		planAgent := r.cfg.Agent
		if r.shadow {
			planAgent.Models.Coder = r.championCoder
		}
		plan := planOf(rcp, derived, planAgent, r.modelProviders(), spec.Overrides.ReviewRounds, r.reachedRigor())
		if err := checkPlan(rcp, plan); err != nil {
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
		// These checks judged the challenger as this run's coder
		// (applyChallenger), so the policy refusing *that* pin is the arm's
		// own refusal and is recorded as one (D556) rather than as
		// infrastructure that failed. Only the coder pin is the arm's: the
		// pins both arms share, and everything else these checks can fail
		// over — a Vertex budget, a settings directory, a price table — fails
		// a challenger exactly as it fails a run carrying no arm, and is left
		// to end the run infra_error (modelRefusal, gateway.go). A halt is
		// not a refusal either: a cap the run has reached stops a shadow
		// exactly as it stops any other run.
		var refused *modelRefusal
		if r.shadow && errors.As(err, &refused) && refused.coderPin {
			return r.refuseArm(err.Error())
		}
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
	for k, v := range gitops.IdentityFor(r.identity) {
		set[k] = v
	}
	if r.follow == nil && !r.review && !r.artifact && !r.shadow {
		// A follow-up's agent gets no git credentials and no GH_TOKEN
		// (design §6.1): the runner fetched the branch and pushes it, and
		// the comments arrive in the prompt. A review run's agent gets
		// none either (design generic-tool §3.6, G12): it only ever reads
		// the checkout, and the runner never pushes or opens anything on
		// its behalf. An artifact run's gets none for the same reason
		// (design artifact-runs.md §3): no branch is pushed, and the
		// deliverable is uploaded by the runner. A challenger arm's gets
		// none because the arm pushes nothing at all (A2.1): the token in
		// this environment would be one an agent could push with, which is
		// exactly what no challenger may hold.
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
	// After the gateway, whose loopback URL is what the sidecar forwards to,
	// and before the agent's environment, which is pointed at the sidecar.
	if err := r.startCompression(ctx); err != nil {
		return err
	}
	// The commits this run makes are stamped from here on (A3): the hooks the
	// agent's stages and the runner's own commits run under, and the author
	// the repository's git.author opt-in names. A review run and an artifact
	// run write no trailer: neither pushes a branch of its own, so no commit
	// of theirs is one of this run's (A3).
	if !r.review && !r.artifact {
		if err := r.writeStampHooks(ctx); err != nil {
			return err
		}
	}
	env, secrets, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth: cfg.Agent.Auth, Secrets: secretEnvs, Set: set, PathPrepend: r.d.PathPrepend,
		Gateway: r.gateway(), Compression: r.compression,
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
//
// base is the task's base_branch, the project's default base branch a launch
// resolved (internal/config's EffectiveBaseBranch): a file that writes no
// git.base_branch of its own takes it, and the resolution says so
// (Sources["git.base_branch"] = SourceProject, ConfigSHA256 recomputed over
// the config that came out). A file that writes the key keeps it — the
// repository's own reviewed text is the branch the run is cut from and the
// one its pull request targets — and so does a task carrying none, which is
// every task that resolved no such default.
func ResolveConfig(data []byte, pl *task.ProjectLayer, project, base string) ConfigResolution {
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
	if base != "" && out.Cfg != nil && out.Res.SourceOf("git.base_branch") != config.SourceRepo {
		// The file's own value is asked for by its source, never by comparing
		// with the default: Resolve fills main in, so a file that writes
		// nothing and one that chose main resolve alike, and the layers below
		// must not be lost to the second.
		out.Cfg.Git.BaseBranch = base
		out.Res.Sources["git.base_branch"] = config.SourceProject
		out.Res.ConfigSHA256 = out.Cfg.SHA256()
	}
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
	out := ResolveConfig(data, pl, r.d.Project, r.spec.BaseBranch)
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
	if out.Layer != nil {
		// The layer's ab: block, read here because this is where every path
		// that resolves a configuration comes through (bootstrap's own read,
		// and readBaseConfig's for a follow-up or a review run). What a
		// challenger must be a member of (applyChallenger), and the one D522
		// line a build with no A/B feature owes the person who wrote it.
		r.ab = out.Layer.AB
		r.noteIgnoredAB()
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
	r.cfgRes = out.Res
	r.rec.ConfigSHA256 = out.Res.ConfigSHA256
	if r.d.CompressionForTest != "" {
		// The one way a test reaches a run with compression on. It is
		// applied here, on the resolved config and after Validate has
		// accepted the file the test committed, so that file never has to
		// carry the key config.Validate refuses while it is reserved (P11).
		// Every entry point that resolves a config -- bootstrap's own read,
		// readBaseConfig's for a follow-up or a review run -- comes through
		// here, so no path can miss it and none can set it from repository
		// content.
		out.Cfg.Agent.Compression = r.d.CompressionForTest
	}
	if r.spec.CompressionOff {
		// The run's own --no-compression, applied last so it wins over every
		// other input: the configuration it overrides, and (for a test)
		// Deps.CompressionForTest above. It is the one direction the
		// override goes -- nothing here can turn compression on for a
		// configuration that does not ask for it -- and it is applied on the
		// resolved config for the same reason CompressionForTest is: every
		// entry point that resolves one comes through here.
		out.Cfg.Agent.Compression = ""
	}
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
			r.verifyOnce(ctx, verify.KindTest, "the check gate's autofix commit")
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
	// The digest step takes implement's place (digest.go, T3.2): a report run's
	// work is the runner's own, so it starts no implement stage and no model
	// call before its review. Like the gate, it either ends the run (nothing is
	// due: no pull request to open) or hands what it committed to the review
	// loop below, which is the recipe's one senior round.
	if r.digest {
		if err := r.digestStep(ctx); err != nil {
			// A halt recorded while the step read or committed outranks the
			// error, exactly as it does around the check gate.
			if h := r.haltValue(); h != nil {
				r.endHalted(ctx, h)
				return
			}
			if errors.Is(context.Cause(ctx), ErrCancelled) && r.markCancelled() {
				r.endCancelled(ctx, "cancelled during the digest step")
			} else {
				r.failInfra(ctx, err)
			}
			return
		}
		if r.skipPR {
			// The step read and committed outside r.stage(), the way the
			// gate's own commands do, so nothing re-checked a halt (a budget
			// cap, a kill switch, the token cap) recorded while it ran: no
			// review round follows this path to check it (g.Clean's own
			// haltValue check is the same guard on the same race).
			if h := r.haltValue(); h != nil {
				r.endHalted(ctx, h)
			}
			return // nothing was due: the step ended the run with no pull request
		}
		skipImplement = true
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
	// divergence from the default branch — and a first run whose work was
	// cut from another branch than its ref is narrowed to its own commits
	// here, once, before the prompt and every round's input read the range.
	r.narrowReviewRange(ctx)
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.reviewRev(ctx), r.reviewDiffSize(ctx), r.baseUnreadable())
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

// seniorLoop is a review step: up to (*run).reviewRounds senior reviews on the
// reviewer's model, each followed by a fix while it asks for changes. Its
// verdict alone decides readiness (seniorReview). tier is "senior" when a
// first line came before it, "" otherwise, as runs without a first line
// always recorded. When st.Bounce and st.FirstRounds > 0 (review:
// {bounce: first_line} with a first_line step before it), a rejection's fix
// is followed by another cheap loop (firstLine) instead of going straight to
// the next senior round; a bounce with FirstRounds == 0 behaves like a plain
// review step, since there is no cheap loop to bounce back to.
//
// A round whose answer carries no parseable verdict is asked once more
// (seniorRound) and, failing that, recorded as the `none` a skipped review is
// recorded as: a reviewer that answered nothing asked for nothing, so it is not
// a finding, and no fix round and no phantom blocker are spent on it (D4's rule
// is about a round that names what it wants). The run then carries on to
// finalize, where the record and the pull request's status say what happened
// and the run ends a draft — a round with no verdict is not a review the run
// can be called ready on (Decide, reviewsPart).
func (r *run) seniorLoop(ctx context.Context, st planStep, tier, reviewPrompt, sys, sessionID string) {
	for round := 1; ; round++ {
		v, parsed, ok := r.seniorRound(ctx, round, reviewPrompt)
		if !ok {
			return
		}
		if !parsed {
			r.lastSenior = seniorRound{Round: round}
			r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Tier: tier, Verdict: "none"})
			r.save(ctx)
			r.d.Log.Warn("the review round returned no parseable verdict after a retry; the run carries on and ends a draft", "round", round)
			return
		}
		// The round is kept whole as well as counted: the mode's threshold
		// table reads the findings' scores, which the summary does not carry
		// (outcome.go, T3.1).
		r.lastSenior = seniorRound{Round: round, Findings: v.Findings}
		r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Tier: tier, Verdict: v.Verdict, Findings: len(v.Findings)})
		r.save(ctx)
		if r.capReached() || v.Verdict == "ship" || round >= r.reviewRounds(st) {
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

// noVerdictRetries is how many times one round's review is asked again after an
// answer with no parseable verdict: one, so a round costs at most two calls.
const noVerdictRetries = 1

// seniorRound runs one senior review round's stage calls, retrying the review
// when the answer carries no parseable verdict (TryVerdict). It reports the
// verdict, whether there was one to read, and whether the run may go on at all
// — a failed stage ends the loop whether or not the round had a verdict.
//
// The retry is a stage of its own, in a session of its own, exactly as another
// round would be: the reviewer is asked the same question again rather than
// reminded of the answer that did not parse, because what the run needs is a
// verdict, not a repair of the one it got. Being a stage, it is not run once
// the run has crossed its token cap (capReached): a round that costs more than
// the cap allows is where the review stops.
func (r *run) seniorRound(ctx context.Context, round int, reviewPrompt string) (Verdict, bool, bool) {
	for attempt := 1; attempt <= noVerdictRetries+1; attempt++ {
		// The retry is a stage the run pays for, and the cap that stops it is
		// the runner's own: the gateway enforces the dollar cap, and nothing
		// else in this loop reads the token one between two attempts of the
		// same round.
		if attempt > 1 && r.capReached() {
			return Verdict{}, false, false
		}
		req := agent.Request{Prompt: r.reviewPromptWithInput(ctx, reviewPrompt), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}
		res, ok, _ := r.stage(ctx, "review", req, stageOpts{})
		// The review's own session is saved like a work stage's (C1.17,
		// D138), and not made the one a fix resumes: see noteReviewSession.
		r.noteReviewSession(req, res)
		r.countTokens(res)
		if !ok {
			return Verdict{}, false, false
		}
		if v, parsed := TryVerdict(res); parsed {
			return v, true, true
		}
		r.d.Log.Warn("the review round produced no parseable verdict", "round", round, "attempt", attempt)
	}
	return Verdict{}, false, true
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

// stageTimeout is a stage's own wall-clock ceiling (T7.6), asked of the stage
// hook: the tripwires move to ee/governance with the block they read, and the
// stage's own bound is the first of them (R2, §5.3). The core default, and
// what the value means, is stageTimeoutOf in governance_hook.go.
func (r *run) stageTimeout(stage string) time.Duration {
	return stageHook().Timeout(r.stageView(stage))
}

// stageTimeoutKey is the policy key a stage's own governance.stage_timeout is
// read and recorded under (policy.KeyStageTimeout*, the spelling the merge's
// sources and a dropped value's key both use). It is the one stage-to-key
// mapping stageTimeout and noteStageTimeoutClamped are read through, so the
// bound a stage is given and the key its record names cannot disagree. A stage
// governance bounds none of answers "" (T7.1: an artifact run's plan and
// render steps).
func stageTimeoutKey(stage string) string {
	switch {
	case stage == "implement":
		return policy.KeyStageTimeoutImplement
	case config.IsReviewStage(stage):
		return policy.KeyStageTimeoutReview
	case stage == "fix":
		return policy.KeyStageTimeoutFix
	}
	return ""
}

// noteStageTimeoutClamped records and warns about a stage's own
// governance.stage_timeout that the run does not apply: the run's own
// timeouts.stage bounds the stage at least as tightly, so the stage's own
// bound is dropped rather than given a deadline of its own (stageOnce makes
// that comparison; this is only what the drop is recorded as).
//
// Dropping it in silence was the finding this answers. An operator who writes
// stage_timeout: { implement: 1h } under a run whose timeouts.stage is 40m has
// asked for a bound no run of that repository applies, and neither the log nor
// result.json said so. The entry is one of policy.ignored, so the report's
// policy line and the digest carry it the way they carry a value of
// fugaro.yaml that a stricter layer dropped.
//
// Source is the layer the run's own timeouts.stage came from, which is the
// bound actually in force: the project layer's governance (the ceiling) when
// its profile set it, the branch when either file set it, and the branch as
// well when neither did — workflows.*.timeouts.stage is InProfile | InRepo, so
// the common case, Fugaro's own 40m default, has no layer that set it and is
// recorded as the branch's, the layer the run's file would have said it in.
// Value is the bound the layer in From asked for. From is the layer the
// stage_timeout key itself came from,
// which for a governance key is the project layer's ceiling or one of the two
// files, so result.schema.json's `from` carries "ceiling" as well; the two are
// different layers, and the warning spells them as recordPolicy does
// ("from" the asking layer, "limit_from" the one that set what is in force).
//
// A stage whose bound is dropped more than once — review repeats, and a
// resumed stage runs again — is recorded and warned about once: the entry is
// keyed by the key and the value, which are the same every time.
func (r *run) noteStageTimeoutClamped(stage string, own time.Duration) {
	key := stageTimeoutKey(stage)
	if key == "" {
		return
	}
	from := r.policy.Sources[key]
	if from == "" {
		from = policy.SourceBranch
	}
	source := policy.SourceBranch
	if r.cfgRes != nil && config.IsLayerSource(r.cfgRes.SourceOf("workflows."+r.rec.Workflow+".timeouts.stage")) {
		source = policy.SourceCeiling
	}
	ig := runstore.PolicyIgnored{Key: key, Value: own.String(), Effective: r.budget.Stage.String(),
		Source: source, From: from, Clamped: true}
	r.mu.Lock()
	if r.rec.Policy == nil {
		r.rec.Policy = &runstore.PolicyRecord{}
	}
	for _, e := range r.rec.Policy.Ignored {
		if e.Key == ig.Key && e.Value == ig.Value {
			r.mu.Unlock()
			return
		}
	}
	r.rec.Policy.Ignored = append(r.rec.Policy.Ignored, ig)
	r.mu.Unlock()
	r.d.Log.Warn("policy: a stage timeout at or above the run's timeouts.stage was ignored",
		"key", PolicyKeyPath(key), "value", ig.Value, "effective", ig.Effective,
		"from", ig.From, "limit_from", ig.Source)
}

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
	req.Dir, req.Env, req.Transcript, req.Stderr = r.d.WorkDir, r.stageEnv(name, pins), tw, sw
	req.Model, req.MaxBudgetUSD = r.cfg.Agent.ModelFor(config.StageRole(name)), r.cfg.Agent.MaxBudgetUSD

	deadlineCtx, cancelDeadline := r.budget.StageContext(ctx)
	defer cancelDeadline()
	// The stage's own governance.stage_timeout, when the run resolved one: it
	// only ever shortens a stage, so a value at or above the run's own
	// timeouts.stage leaves that bound in charge and is dropped here rather
	// than given a deadline of its own — dropped loudly, on the run log and as
	// one key of result.json's policy.ignored, because a bound the operator
	// wrote and the run does not apply is exactly what ignoring it silently
	// means (noteStageTimeoutClamped). deadlineCtx stays the context that says
	// which of the two ended the stage (below): it is done only when the run's
	// own bound, or the finalize reserve under it, is what fired.
	//
	// ownCtx is built before the registry write below, so the deadline the
	// registry and the console show is the one the stage is actually under:
	// when a tighter stage_timeout applies, that is the stage's own bound, and
	// a registry naming the run's own later one would say the stage has time
	// it does not have.
	own := r.stageTimeout(name)
	r.noteIgnoredStageBound(name, own)
	ownCtx, cancelOwn := deadlineCtx, context.CancelFunc(func() {})
	if own > 0 && own < r.budget.Stage {
		ownCtx, cancelOwn = context.WithTimeout(deadlineCtx, own)
	} else {
		if own > 0 {
			r.noteStageTimeoutClamped(name, own)
		}
		own = 0
	}
	defer cancelOwn()
	if dl, ok := ownCtx.Deadline(); ok {
		r.beginBudgetStage(name, n, dl)
	}
	// The halt's way to stop this stage: cancelling with the halt as the
	// cause, which StageError reads.
	stageCtx, cancelCause := context.WithCancelCause(ownCtx)
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
		if p := r.sidecarProc(); p != nil {
			// The sidecar is watched with the same shape and for the same
			// reason, and it can only be here with the gateway: a sidecar
			// that dies or stops answering under a stage must end that stage
			// rather than leave it waiting on a process that never will
			// (P8, P10).
			watching.Add(1)
			go func() { defer watching.Done(); r.watchSidecar(stageCtx, done, name, p) }()
		}
		stopWatch = sync.OnceFunc(func() { close(done); watching.Wait() })
	}
	// Checkpoints run only while the agent does: stopped (and waited for)
	// the moment it returns, and by the defer if it panics. The stop is
	// registered while it is live so a mid-stage governance halt can stop it
	// too, before it writes pull-request state of its own (governanceHalt).
	stopCheckpoints := r.startCheckpoints(stageCtx, name)
	r.setStageCkptStop(stopCheckpoints)
	defer func() {
		// Stopped first, and only then unregistered: on the panic path — the
		// only path where this defer runs with the poll still alive — a halt
		// on another goroutine must find the stop and wait on it rather than
		// read a nil and write the record while the poll's in-flight tick
		// still is. stop is idempotent, so the normal path, where the stop
		// above already ran it, is unaffected.
		stopCheckpoints()
		r.setStageCkptStop(nil)
	}()
	res, err := r.d.Agent.Run(stageCtx, req)
	// What ended the stage is what was already true when the agent returned:
	// the stage's own deadline is the one that fired, and not the run's own
	// bound — or the finalize reserve under it — whose context is the parent
	// and which is done too whenever one of those fired. Read straight away,
	// so an agent that returned a failure of its own a moment before its
	// stage ran out keeps that failure rather than being read as a stop. A
	// stage whose result arrived as its deadline did is not stopped either:
	// its work is done, the same way a result that races the run's own
	// timeouts.stage is a stage that succeeded.
	cutByOwn := errors.Is(stageCtx.Err(), context.DeadlineExceeded) && deadlineCtx.Err() == nil
	ownTimedOut := own > 0 && err != nil && cutByOwn
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
	// The relay has seen the whole stream by now, so its counts of the
	// stage's sub-agent tool calls and of the most times one identical shell
	// command ran are final: the record carries them as the stage's agents
	// and repeats, for every auth mode (task-governance T1.3, T1.4).
	res.SubAgents = relay.SubAgents
	res.Repeats = relay.Repeats
	r.rec.Stages = append(r.rec.Stages, r.stageTiming(name, started, res.CostUSD, res.SubAgents, res.Repeats))
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
	// A stage that reached its own governance.stage_timeout is a governance
	// stop, not a failed stage: the run halts with the checkpoint report the
	// other tripwires leave, and the detail is the runner's own word for the
	// bound that fired (T7.6, D329, G7). The run's own timeouts.stage is not
	// this — a stage that ends at that bound is the failure it has always
	// been — which is why own is zero unless it was the tighter of the two.
	//
	// It runs here, on the run goroutine and after the stage's spend and
	// history above are recorded, which is the ordering governanceHalt
	// requires of its caller. A cancel that landed first keeps the run
	// cancelled: governanceHalt then records nothing, and the stage fails the
	// way it would have without the timeout.
	//
	// What the ceiling leaves is the stage hook's to decide (governance_hook.go,
	// §5.3): its default is the halt above, and a build whose hook raises
	// nothing ignores the block the ceiling came from — the stage then falls
	// through and fails on the bound the run's own timeouts.stage set, which is
	// the stage hook's answer and not a refusal of the run (D522, G2).
	if ownTimedOut {
		if d := stageHook().TimedOut(r.stageView(name)); d.Halt {
			// The reason is the core's to write, so a hook that left one
			// outside the vocabulary governanceHalt records — and
			// haltReasonText has words for, in result.schema.json's enum —
			// fails the stage named instead of writing a halt nobody can read
			// back (D521, D529, governance_hook.go).
			if !governanceHaltReasons[d.Reason] {
				r.fail(fmt.Sprintf("stage %s: the stage hook left the halt reason %q, which is not a governance stop this runner can record", name, d.Reason))
				r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
				return res, false, fmt.Errorf("stage %s: unknown halt reason %q", name, d.Reason)
			}
			if r.governanceHalt(ctx, d.Reason, d.Detail) {
				if h := r.haltValue(); h != nil {
					return res, false, &HaltError{*h}
				}
			}
		}
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
		//
		// A challenger arm stands down here too, and before shadowFinalize:
		// the gate ends both arms of a pair the same way, so the two records
		// agree (D559). An arm finalized from a checkout no stage ran in
		// would turn a clean gate's succeeded/none into "the agent made no
		// commits" and mask the gate's own infra_error behind it.
		return nil
	}
	if r.shadow {
		// A challenger arm's finalize is its own (shadow.go,
		// shadowFinalize): the same record it would otherwise carry, and
		// nothing that reaches the developer — no push, no pull request, no
		// comment.
		return r.shadowFinalize(ctx)
	}
	// A report run's last word before anything is pushed: the files it commits
	// under docs/fugaro-reviews/ are the runner's own, byte for byte
	// (digest.go, T3.2's call, which T3.3 fills). The commit it put the branch
	// back to is held to once more after the leftover commit below, because
	// that is the last moment before the push.
	step, err := r.digestFinalize(ctx)
	if err != nil {
		return err
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
	// A build's artifact the run left in the tree is never this commit's
	// (stray.go): it is refused by name, and held out of the commit, so what
	// the branch carries is the run's work and no compiled file beside it.
	//
	// The refusal is a failure and not a stop, like every other failure at this
	// point of a run: finalize carries on, and what a refused run leaves is the
	// same draft a failed run leaves — the work pushed, the pull request open,
	// the reason naming the file. That is what makes it actionable; a run that
	// returned here would put nothing on the branch and open nothing.
	strays, err := r.strayArtifacts(ctx)
	if err != nil {
		return fmt.Errorf("listing what the run left untracked: %w", err)
	}
	if len(strays) > 0 {
		r.fail(strayRefusal(strays))
	}
	committed, err := r.repo.CommitAllExcept(ctx, leftover, strayPaths(strays))
	if err != nil {
		return fmt.Errorf("committing leftover work: %w", err)
	}
	// A report run's branch must still be the commit the digest step made, and
	// nothing committed on top of it: what finalize commits is what the pull
	// request carries (digest.go's digestRefuseChangedBranch).
	r.digestRefuseChangedBranch(ctx, step, committed)
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
	// Nothing is written after the commit the suite ran on. The run used to
	// file the findings it left open as backlog items here, which committed
	// after that suite run and was not verified: a test that pins a fact
	// about the tree — the repository's own census of docs/backlog/ is the
	// one that bit (docs/rigor-modes/reconciliation.md) — then fails on the
	// commit the run pushes, though it passed on the commit it tested. What
	// the run left open stays in the record (Reviews, Tolerated) and in the
	// pull request's status section instead (reviewsPart, report.go).
	decided := sha
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		return fmt.Errorf("reading verify records: %w", err)
	}
	// Stored in result.json and verify/<n>.json, like everything published.
	records = r.redactRecords(records)
	r.rec.HeadSHA, r.rec.Verify = sha, records
	r.uploadVerifyRecords(ctx, records)
	// Both arms' patch objects (A2.1): the run's own tree diff, from the
	// commit it started at to the head it is about to push. sha is that head
	// — the leftover commit and the run's own commits are behind it — so the
	// champion's patch is the diff of the branch it publishes and the
	// challenger's is the diff it holds back.
	r.uploadPatch(ctx, sha)

	pol := r.findingsPolicy()
	ready, reason := Decide(records, decided, seniorReview(r.rec.Reviews), pol)
	r.rec.Tolerated = r.toleratedRecord(pol)
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
	// What the run pushed is final from here: the commits are all made, and
	// the count of the ones without the trailer is logged beside them (D603).
	r.logUnstampedCommits(ctx)
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
	r.narrowText = r.narrowSummary(ctx)
	notes, gone := r.settlePR(ctx, pr, ready, reason, records)
	if r.narrowText != "" {
		notes = append(notes, r.narrowText)
	}
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
