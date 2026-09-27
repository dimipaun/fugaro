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
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/logtail"
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
}

type run struct {
	d            Deps
	rec          *runstore.Record
	spec         *task.Spec
	cfg          *config.Config
	wf           config.Workflow
	repo         *gitops.Repo
	env          []string
	secrets      []string
	budget       Budget
	instructions string
	reviewFile   string
	stageN       map[string]int
	failReason   string
	cancelled    bool
	provider     gitprov.Provider
	providerKind string
	providerFrom string          // where providerKind came from, for mismatch errors
	credURL      string          // scheme://host of an HTTPS origin; "" when git needs no token
	auth         gitprov.GitAuth // current git credentials
	authWarned   bool            // whether a mid-run refresh failure has already been logged
	tail         *LogTail        // output of the first failed stage, for the draft PR
}

// Git credential lifetimes (design §6.2). A stage must not outlive its
// token, so before each stage the runner asks for one valid for the stage
// timeout plus gitAuthSlack; bootstrap's clone and fetch need only
// bootstrapAuthMinValid. authRefreshTimeout bounds how long a mid-run
// refresh itself may take, so a slow or hanging provider call cannot eat
// into the stage's own budget.
const (
	gitAuthSlack          = 5 * time.Minute
	bootstrapAuthMinValid = 10 * time.Minute
	authRefreshTimeout    = 30 * time.Second
)

// prAttempts is how many times finalize tries to open the pull request.
// EnsurePR finds what an earlier attempt created, so retrying is safe.
const prAttempts = 3

// Run executes the run whose task spec is in d.Store. It always returns the
// final record, reflecting the run's actual status and outcome whether or
// not Run itself returns an error. The error is non-nil for an infra_error,
// for a cancellation seen during bootstrap (before any stage or finalize
// runs, where the record's status is cancelled rather than infra_error), or
// when writing the final record itself fails.
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
		rec: &runstore.Record{Version: 1, Status: runstore.StatusRunning, Stage: "bootstrap",
			Outcome: runstore.OutcomeNone, StartedAt: d.Now().UTC()},
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
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
		finished := d.Now().UTC()
		r.rec.FinishedAt = &finished
		if werr := d.Store.WriteRecord(context.WithoutCancel(ctx), r.rec); werr != nil && err == nil {
			err = fmt.Errorf("writing run record: %w", werr)
		}
		rec = r.rec
	}()

	runCtx, stopWatch := WatchCancel(ctx, d.Store.CancelRequested, d.CancelPoll)
	defer stopWatch()
	if err := r.bootstrap(runCtx); err != nil {
		if errors.Is(context.Cause(runCtx), ErrCancelled) {
			r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, "cancelled during bootstrap"
		}
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	r.runAgentLoop(runCtx)
	if errors.Is(context.Cause(runCtx), ErrCancelled) {
		// The cancel may have landed after the last stage already returned
		// successfully; make sure it still turns into a draft PR.
		r.cancelled = true
		r.fail("cancelled")
	}
	finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.wf.Timeouts.FinalizeReserve.Duration)
	defer cancel()
	if err := r.finalize(finCtx); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}
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

func (r *run) save(ctx context.Context) {
	if err := r.d.Store.WriteRecord(context.WithoutCancel(ctx), r.rec); err != nil {
		r.d.Log.Warn("writing run record failed", "err", err)
	}
}

// redact removes every known secret value from s.
func (r *run) redact(s string) string { return agent.Redact(s, r.secrets) }

// addSecret adds v to the values redacted from everything the run publishes.
func (r *run) addSecret(v string) {
	if v != "" && !slices.Contains(r.secrets, v) {
		r.secrets = append(r.secrets, v)
	}
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
	if r.env != nil {
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
		if u, err := repo.OriginURL(ctx); err == nil {
			return u
		}
	}
	return r.d.Remote
}

// fail records the first reason the run cannot produce a ready PR.
func (r *run) fail(reason string) {
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
	if err := os.Remove(filepath.Join(stateDir, prFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale %s: %w", prFile, err)
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
	r.save(ctx)
	if spec.IsFollowUp() {
		return errors.New("follow-up runs are not supported by this version of fugaro")
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
	repo, err := gitops.OpenOrClone(ctx, r.d.WorkDir, r.d.Remote, gitops.WithVars(gitops.IdentityEnv(), cloneVars))
	if err != nil {
		return fmt.Errorf("opening checkout %s: %w", r.d.WorkDir, err)
	}
	branch := "fugaro/" + spec.RunID
	if err := repo.CheckoutNewBranch(ctx, spec.Ref, branch); err != nil {
		return fmt.Errorf("checking out %s: %w", spec.Ref, err)
	}
	r.repo = repo

	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, "fugaro.yaml"))
	if err != nil {
		return fmt.Errorf("reading fugaro.yaml at %s: %w", spec.Ref, err)
	}
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		return fmt.Errorf("fugaro.yaml is invalid: %s", strings.Join(msgs, "; "))
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return fmt.Errorf("selecting workflow: %w", err)
	}
	if err := spec.Apply(cfg, &wf); err != nil {
		return fmt.Errorf("applying task overrides: %w", err)
	}
	r.cfg, r.wf, r.rec.Workflow = cfg, wf, name
	switch {
	case r.provider == nil:
		if err := r.openProvider(ctx, cfg.Git.Provider, "git.provider"); err != nil {
			return err
		}
	case r.providerKind != cfg.Git.Provider:
		return fmt.Errorf("fugaro.yaml sets git.provider to %s, but %s says %s", cfg.Git.Provider, r.providerFrom, r.providerKind)
	}
	if err := repo.FetchBase(ctx, cfg.Git.BaseBranch); err != nil {
		return fmt.Errorf("fetching base %s: %w", cfg.Git.BaseBranch, err)
	}
	if r.instructions, err = r.readRepoFile(cfg.Agent.Instructions); err != nil {
		return fmt.Errorf("agent.instructions: %w", err)
	}
	if cfg.Agent.Review != "" && !strings.HasPrefix(cfg.Agent.Review, "/") {
		if r.reviewFile, err = r.readRepoFile(cfg.Agent.Review); err != nil {
			return fmt.Errorf("agent.review: %w", err)
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
	agentAuthVars, err := r.authVars()
	if err != nil {
		return fmt.Errorf("building git credentials: %w", err)
	}
	maps.Copy(set, agentAuthVars)
	env, secrets, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth: cfg.Agent.Auth, Secrets: secretEnvs, Set: set, PathPrepend: r.d.PathPrepend,
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
	r.rec.Branch = branch
	r.save(ctx)
	return nil
}

func (r *run) readRepoFile(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, rel))
	return string(data), err
}

func (r *run) agentLoop(ctx context.Context) {
	sessionID := agent.NewSessionID()
	sys := SystemPrompt(PromptData{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, StateDir: r.d.StateDir}, r.instructions)
	if _, ok := r.stage(ctx, "implement", agent.Request{Prompt: r.spec.Task, SessionID: sessionID, AppendSystemPrompt: sys}); !ok {
		return
	}
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.cfg.Git.BaseBranch)
	rounds := r.cfg.Agent.ReviewRounds
	for round := 1; round <= rounds; round++ {
		res, ok := r.stage(ctx, "review", agent.Request{Prompt: reviewPrompt, SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema})
		if !ok {
			return
		}
		v := ParseVerdict(res)
		r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Verdict: v.Verdict, Findings: len(v.Findings)})
		r.save(ctx)
		if v.Verdict == "ship" || round == rounds {
			return
		}
		if _, ok := r.stage(ctx, "fix", agent.Request{Prompt: FixPrompt(v), SessionID: sessionID, Resume: true, AppendSystemPrompt: sys}); !ok {
			return
		}
	}
}

// stage runs one agent stage and reports whether the loop may continue.
func (r *run) stage(ctx context.Context, name string, req agent.Request) (agent.Result, bool) {
	if ctx.Err() != nil {
		r.fail(StageError(name, ctx, r.budget, ctx.Err()))
		r.cancelled = errors.Is(context.Cause(ctx), ErrCancelled)
		return agent.Result{}, false
	}
	if r.budget.Exhausted() {
		r.fail("time budget exhausted before stage " + name)
		return agent.Result{}, false
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
	err := r.refreshGitAuth(authCtx, r.budget.Stage+gitAuthSlack)
	cancelAuth()
	if err != nil {
		r.warnAuthRefresh(err)
	}
	log.Info("stage started", "n", n)

	var transcript bytes.Buffer
	stderrTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	transcriptTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	tw := agent.NewRedactor(io.MultiWriter(&transcript, transcriptTail), r.secrets)
	sw := agent.NewRedactor(io.MultiWriter(NewLineWriter(log, "agent"), stderrTail), r.secrets)
	req.Dir, req.Env, req.Transcript, req.Stderr = r.d.WorkDir, r.env, tw, sw
	req.Model, req.MaxBudgetUSD = r.cfg.Agent.Model, r.cfg.Agent.MaxBudgetUSD

	stageCtx, cancel := r.budget.StageContext(ctx)
	defer cancel()
	res, err := r.d.Agent.Run(stageCtx, req)
	_ = tw.Flush()
	_ = sw.Flush()
	if perr := r.d.Store.PutFile(context.WithoutCancel(ctx), fmt.Sprintf("transcripts/%s-%d.jsonl", name, n), transcript.Bytes(), "application/x-ndjson"); perr != nil {
		log.Warn("storing transcript failed", "err", perr)
	}
	r.rec.CostUSD += res.CostUSD
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: name, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds()})
	log.Info("stage finished", "n", n, "cost_usd", res.CostUSD, "err", err)

	switch {
	case err != nil:
		r.fail(StageError(name, stageCtx, r.budget, err))
		r.cancelled = errors.Is(context.Cause(stageCtx), ErrCancelled)
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false
	}
	r.save(ctx)
	return res, true
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
	committed, err := r.repo.CommitAll(ctx, "fugaro: uncommitted work at finalize")
	if err != nil {
		return fmt.Errorf("committing leftover work: %w", err)
	}
	ahead, err := r.repo.AheadOf(ctx, base)
	if err != nil {
		return fmt.Errorf("counting commits ahead of %s: %w", base, err)
	}
	if ahead == 0 {
		r.fail("the agent made no commits")
		if err := r.repo.CommitEmpty(ctx, "fugaro: "+r.failReason); err != nil {
			return fmt.Errorf("recording an empty commit: %w", err)
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
	if r.failReason != "" {
		ready, reason = false, r.failReason
	}
	if err := r.refreshGitAuth(ctx, r.wf.Timeouts.FinalizeReserve.Duration); err != nil {
		r.warnAuthRefresh(err)
	}
	if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		return fmt.Errorf("pushing %s: %w", r.rec.Branch, err)
	}
	title, body := r.prText()
	pr, err := r.ensurePR(ctx, gitprov.PRSpec{
		Branch: r.rec.Branch, Base: base, Title: title, Body: body, Draft: !ready,
		Labels: r.cfg.Git.PR.Labels, Reviewers: r.cfg.Git.PR.Reviewers,
	})
	if pr.Number != 0 {
		r.rec.PR = &runstore.PRRef{Number: pr.Number, URL: pr.URL}
	}
	var partial *gitprov.PartialError
	switch {
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
		return fmt.Errorf("opening pull request: %w", err)
	}
	r.rec.Reason = reason
	switch {
	case ready:
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReady
	case r.cancelled:
		r.rec.Status, r.rec.Outcome = runstore.StatusCancelled, runstore.OutcomeDraft
	default:
		r.rec.Status, r.rec.Outcome = runstore.StatusFailed, runstore.OutcomeDraft
	}
	report := agent.Redact(Report(r.rec, r.d.Store.Prefix(), r.logTail(ready, records)), r.secrets)
	if err := r.provider.Comment(ctx, pr, report); err != nil {
		r.d.Log.Warn("posting the run report failed", "err", r.redact(err.Error()))
	}
	if err := r.d.Store.PutFile(ctx, "report.md", []byte(report), "text/markdown"); err != nil {
		r.d.Log.Warn("storing the run report failed", "err", err)
	}
	r.rec.Stage = "writeback" // cache write-back arrives in M4
	return nil
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
		}
		var partial *gitprov.PartialError
		if err == nil || errors.As(err, &partial) {
			return pr, err
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
		title, body, _ := strings.Cut(strings.TrimSpace(agent.Redact(string(data), r.secrets)), "\n")
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
	if len(body) > maxBodyBytes {
		body = strings.ToValidUTF8(body[:maxBodyBytes-len(truncatedNote)], "") + truncatedNote
	}
	return title, body
}
