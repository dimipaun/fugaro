package cli

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Launch timing. claimWait and claimPoll are variables so tests can shrink them.
const (
	claimTTL      = runstore.ClaimTTL
	launchTimeout = 2 * time.Minute // well under claimTTL: a hung RunJob can't outlive its claim
	// holdTimeout bounds everything done while holding the claim (the
	// re-check, Launch and the launch.json write), so a claim is never
	// judged stale by another CLI while its holder is still at work.
	holdTimeout = claimTTL / 2
)

var (
	claimWait = 30 * time.Second // how long a loser waits for the winner's launch.json
	claimPoll = time.Second
)

// launchResult is what fugaro run reports, and its --json output.
type launchResult struct {
	Run       string `json:"run"`
	Repo      string `json:"repo"`
	RunID     string `json:"run_id"`
	Branch    string `json:"branch"`
	Execution string `json:"execution"`
	LogURL    string `json:"log_url,omitempty"`
	Status    string `json:"status"` // launched | already-launched
}

// launchHooks lets tests stop launchRun between its steps, to make the
// races of design §4.7 deterministic. Production code never sets them.
var launchHooks struct {
	beforeClaim    func() // after the first launch.json/result.json check
	beforeRead     func() // after a failed Claim, before reading the held claim
	beforeTakeover func() // after reading a stale claim, before replacing it
	afterClaim     func() // after taking the claim, before the re-check
}

func hook(f func()) {
	if f != nil {
		f()
	}
}

type runOptions struct {
	cloud                                 cloudOptions
	repo, ref, workflow, runID, batch, tf string
	retry                                 string
	pr                                    int
	asJSON                                bool
}

func newRunCmd() *cobra.Command {
	var o runOptions
	cmd := &cobra.Command{
		Use:   "run [TEXT]",
		Short: "Launch a task in the cloud; it always ends with a PR",
		Long: "Launch a task in the cloud. The task is TEXT or --task-file (- for stdin).\n\n" +
			"A repeated --run-id reports the existing launch instead of starting another, so\n" +
			"a caller that is unsure whether its launch went through can simply repeat it.\n" +
			"--retry RUN launches a run whose task was stored but never started.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRun(cmd, &o, args) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	f.StringVar(&o.ref, "ref", "", "base branch or ref (default: the repo's base_branch)")
	f.StringVar(&o.workflow, "workflow", "", "workflow to run (default: the repo's only workflow)")
	f.StringVar(&o.runID, "run-id", "", "run ID to use; repeating one reports its launch instead of starting another")
	f.StringVar(&o.batch, "batch", "", "batch label, to group runs in fugaro ls")
	f.StringVar(&o.tf, "task-file", "", "read the task from this file, or - for stdin")
	f.StringVar(&o.retry, "retry", "", "launch the stored task of RUN (<repo-slug>/<run-id> or a run ID) that never started")
	f.IntVar(&o.pr, "pr", 0, "continue the Fugaro PR N (M6)")
	_ = f.MarkHidden("pr")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// taskFlags are the flags --retry excludes: the stored task.json supplies them.
var taskFlags = []string{"repo", "ref", "workflow", "run-id", "batch", "task-file"}

func runRun(cmd *cobra.Command, o *runOptions, args []string) error {
	ctx := cmd.Context()
	if cmd.Flags().Changed("pr") {
		return userErr("follow-up runs (--pr) arrive in M6")
	}
	var text string
	if o.retry != "" {
		for _, name := range taskFlags {
			if cmd.Flags().Changed(name) {
				return userErr("--retry launches the stored task as it is; it takes no --%s", name)
			}
		}
		if len(args) > 0 {
			return userErr("--retry launches the stored task as it is; it takes no TEXT")
		}
	} else {
		var err error
		if text, err = taskText(cmd, o.tf, args); err != nil {
			return err
		}
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	var (
		spec *task.Spec
		slug string
	)
	if o.retry != "" {
		slug, spec, err = retrySpec(ctx, env, o.retry)
	} else {
		slug, spec, err = newSpec(ctx, env, o, text)
	}
	if err != nil {
		return err
	}
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	prior, err := existingLaunch(ctx, env, s, spec)
	if err != nil {
		return err
	}
	if prior == nil {
		if err := checkMaxParallel(ctx, env); err != nil {
			return err
		}
	}
	if o.retry == "" {
		if err := createTask(ctx, s, spec); err != nil {
			return err
		}
	}
	res, err := launchRun(ctx, env, slug, spec, time.Now())
	if err != nil {
		return err
	}
	if o.retry != "" && res.Status == "already-launched" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s launched already; --retry only starts a run that never launched. "+
			"To see how it went, run fugaro diagnose %s; to redo it, start a new run.\n", res.Run, res.Run)
	}
	return printLaunch(cmd.OutOrStdout(), res, o.asJSON)
}

// taskText is the task from exactly one of TEXT and --task-file.
func taskText(cmd *cobra.Command, file string, args []string) (string, error) {
	var text string
	switch {
	case file != "" && len(args) > 0:
		return "", userErr("pass the task as TEXT or with --task-file, not both")
	case file == "-":
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", userErr("reading the task from stdin: %v", err)
		}
		text = string(data)
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", userErr("reading the task: %v", err)
		}
		text = string(data)
	case len(args) == 1:
		text = args[0]
	default:
		return "", userErr("pass the task as TEXT or with --task-file (- for stdin)")
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", userErr("the task is empty")
	}
	return text, nil
}

// newSpec builds a new run's spec, and its repository's slug, from the
// flags, the local config and the checkout in the working directory.
func newSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string) (string, *task.Spec, error) {
	repo := o.repo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return "", nil, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", nil, userErr("--repo: %v", err)
	}
	checkout := checkoutOf(ctx, repo)
	slug, err := env.repoSlug(repo, checkout)
	if err != nil {
		return "", nil, err
	}
	workflow, err := resolveWorkflow(env, repo, o.workflow, checkout)
	if err != nil {
		return "", nil, err
	}
	ref := o.ref
	if ref == "" {
		if r, ok := env.localRepo(repo); ok && r.BaseBranch != "" {
			ref = r.BaseBranch
		} else if c := checkout(); c != nil && c.Git.BaseBranch != "" {
			ref = c.Git.BaseBranch
		} else {
			return "", nil, userErr("cannot tell the base branch of %s; pass --ref", repo)
		}
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return "", nil, userErr("%v", err)
	}
	runID := o.runID
	if runID == "" {
		if runID, err = task.NewRunID(time.Now(), crand.Reader); err != nil {
			return "", nil, err
		}
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, Workflow: workflow, Task: text, RequestedBy: me, Batch: o.batch}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return slug, spec, nil
}

// checkoutOf returns a memoized lookup of the working directory's
// fugaro.yaml, for repo only (see checkoutConfig).
func checkoutOf(ctx context.Context, repo string) func() *config.Config {
	var (
		done bool
		cfg  *config.Config
	)
	return func() *config.Config {
		if !done {
			cfg, done = checkoutConfig(ctx, repo), true
		}
		return cfg
	}
}

// resolveWorkflow is --workflow, else the local config's single workflow
// for repo, else the single workflow of the checkout's fugaro.yaml.
func resolveWorkflow(env *cloudEnv, repo, flag string, checkout func() *config.Config) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if r, ok := env.localRepo(repo); ok && len(r.Workflows) == 1 {
		return r.Workflows[0], nil
	}
	if c := checkout(); c != nil && len(c.Workflows) == 1 {
		for name := range c.Workflows {
			return name, nil
		}
	}
	return "", userErr("cannot tell which workflow of %s to run; pass --workflow", repo)
}

// retrySpec loads the stored task of an existing run for --retry. The
// slug is where the run is stored, so no provider lookup is needed.
func retrySpec(ctx context.Context, env *cloudEnv, ref string) (string, *task.Spec, error) {
	slug, runID, err := locateRun(ctx, env, ref)
	if err != nil {
		return "", nil, err
	}
	s := runstore.Open(env.bucket.Bucket, slug, runID)
	spec, err := s.ReadTask(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return "", nil, userErr("run %s/%s has no task.json", slug, runID)
	}
	if err != nil {
		return "", nil, remote(err)
	}
	if spec.RunID != runID {
		return "", nil, userErr("run %s/%s holds the task of run %s; it can't be retried", slug, runID, spec.RunID)
	}
	if spec.Workflow == "" {
		if spec.Workflow, err = resolveWorkflow(env, spec.Repo, "", checkoutOf(ctx, spec.Repo)); err != nil {
			return "", nil, err
		}
	}
	return slug, spec, nil
}

// activeHorizon bounds checkMaxParallel's listing: Cloud Run's longest task
// timeout plus a day for queueing. An older execution can't be active.
const activeHorizon = gcp.MaxTaskTimeout + 24*time.Hour

// checkMaxParallel refuses a new launch when max_parallel runs are active.
// It lists only executions created within activeHorizon (C-M7), so a
// --batch of N launches doesn't page through the region's history N times.
func checkMaxParallel(ctx context.Context, env *cloudEnv) error {
	active, err := env.be.List(ctx, backend.ListFilter{ActiveOnly: true, Since: time.Now().Add(-activeHorizon)})
	if err != nil {
		return remote(err)
	}
	if len(active) >= env.lc.MaxParallel {
		return userErr("%d runs active; max_parallel is %d", len(active), env.lc.MaxParallel)
	}
	return nil
}

// createTask stores task.json once. A repeated run ID must repeat its task.
func createTask(ctx context.Context, s *runstore.Store, spec *task.Spec) error {
	err := s.CreateTask(ctx, spec)
	if err == nil {
		return nil
	}
	if !errors.Is(err, runstore.ErrExists) {
		return remote(err)
	}
	have, err := s.ReadTask(ctx)
	if err != nil {
		return remote(err)
	}
	a, err1 := have.Marshal()
	b, err2 := spec.Marshal()
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if string(a) != string(b) {
		return userErr("run ID %s already holds a different task", spec.RunID)
	}
	return nil
}

func printLaunch(w io.Writer, res launchResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	verb := "launched"
	if res.Status == "already-launched" {
		verb = "already launched"
	}
	if _, err := fmt.Fprintf(w, "%s %s\n  branch %s\n", verb, oneLine(res.Run), oneLine(res.Branch)); err != nil {
		return err
	}
	if res.LogURL != "" {
		_, err := fmt.Fprintf(w, "  logs %s\n", oneLine(res.LogURL))
		return err
	}
	return nil
}

// existingLaunch is the run's launch, or nil when it has not launched. When
// the CLI that launched died before writing launch.json, it backfills one
// from result.json, where the runner records the same canonical execution
// name launch.json holds (C-1). Either name must be of the run's own job
// (S-I2): the run's service account can write both objects.
func existingLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec) (*runstore.Launch, error) {
	runID := spec.RunID
	check := func(name string) error {
		if err := env.checkExecution(name, s.Slug(), spec); err != nil {
			return remote(fmt.Errorf("run %s/%s: not following its execution: %w", s.Slug(), runID, err))
		}
		return nil
	}
	if l, err := s.ReadLaunch(ctx); err == nil {
		if err := check(l.Execution); err != nil {
			return nil, err
		}
		return l, nil
	} else if !errors.Is(err, runstore.ErrNotFound) {
		return nil, remote(err)
	}
	rec, err := s.ReadRecord(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, remote(err)
	}
	if rec.Execution == "" {
		// It ran already, with no cloud execution (a local run against this
		// bucket): never launch it again (C-M4). Nothing to backfill.
		return &runstore.Launch{Version: 1, RunID: runID, LaunchedAt: rec.StartedAt}, nil
	}
	if err := check(rec.Execution); err != nil {
		return nil, err
	}
	l := &runstore.Launch{Version: 1, RunID: runID, Backend: "cloud-run", Execution: rec.Execution, LaunchedAt: rec.StartedAt}
	if id, ok := backend.ParseExecution(rec.Execution); ok {
		l.Job = id.Job
	}
	if err := s.WriteLaunch(ctx, l); errors.Is(err, runstore.ErrExists) {
		if l, err = s.ReadLaunch(ctx); err != nil {
			return nil, remote(err)
		}
		return l, nil
	} else if err != nil {
		return nil, remote(err)
	}
	return l, nil
}

// launchRun starts spec's execution, stored under slug, at most once, however many CLIs race
// on the same run ID (design §4.7, Review Focus 1):
//
//   - launch.json (or result.json's execution) present → already launched.
//   - Otherwise take the "launching" claim with create-if-absent. The claim
//     is never deleted after a launch, so a later caller always finds it,
//     then re-reads launch.json, which the claim holder writes before it
//     returns.
//   - A held claim is read once, content and generation together. If that
//     content is stale (older than claimTTL, its holder presumably dead), it
//     is taken over with an overwrite matched to that same generation, never
//     delete-then-create, so two takers can't both win (I-2, N-1). A claim
//     that vanished before the read counts as absent: claim once more (N-10).
//   - A fresh claim, or a lost takeover, means someone else is launching:
//     wait up to claimWait for their launch.json and report it (N-4), else
//     exit 1, "still in flight". A claim that disappears while we wait was
//     released after a refused launch: go back for it at once.
//   - A run with a cancel marker is never launched.
//   - Having won the claim, re-check launch.json and result.json: an earlier
//     holder may have launched just before going stale.
//   - Launch. Only a definitive refusal (backend.ErrRejected) releases our
//     claim; after an ambiguous error the execution may exist, so the claim
//     stays until it goes stale, by which time the runner's result.json says
//     whether it started (N-3).
//
// file:// buckets are single-user: fileblob's IfNotExist is not atomic.
func launchRun(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, now time.Time) (launchResult, error) {
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	res := launchResult{Run: slug + "/" + spec.RunID, Repo: spec.Repo, RunID: spec.RunID, Branch: "fugaro/" + spec.RunID}
	done := func(l *runstore.Launch, status string) (launchResult, error) {
		res.Execution, res.LogURL, res.Status = l.Execution, l.LogURL, status
		return res, nil
	}
	if l, err := existingLaunch(ctx, env, s, spec); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	if cancelled, err := s.CancelRequested(ctx); err != nil {
		return res, remote(err)
	} else if cancelled {
		return res, userErr("run %s was cancelled; start a new one", res.Run)
	}
	hook(launchHooks.beforeClaim)
	holder := claimHolder()
	for round := 0; ; round++ {
		won, other, err := takeClaim(ctx, env, s, holder, now)
		if err != nil {
			return res, err
		}
		if won {
			break
		}
		l, err := waitForLaunch(ctx, env, s, spec, res.Run, other)
		if errors.Is(err, errClaimReleased) {
			// The holder's launch was refused and it released the claim:
			// nothing started, so try for the claim again at once.
			if round < claimRounds-1 {
				continue
			}
			return res, userErr("the launch claim of %s keeps being released by other launches that were refused; fugaro run --retry %s tries again", res.Run, res.Run)
		}
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	// We hold the claim. Everything from here is bounded well inside
	// claimTTL, measured from the claim's own timestamp (now), which is what
	// other CLIs judge staleness on: none can take it over while we work.
	hctx, cancelHold := context.WithDeadline(ctx, now.Add(holdTimeout))
	defer cancelHold()
	hook(launchHooks.afterClaim)
	if l, err := existingLaunch(hctx, env, s, spec); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	// A cancel that landed while we claimed: never launch it, and release
	// the claim so a waiting cancel sees the launch end (C-I3).
	if cancelled, err := s.CancelRequested(hctx); err != nil {
		return res, remote(err)
	} else if cancelled {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, userErr("run %s was cancelled; start a new one", res.Run)
	}
	lctx, cancel := context.WithTimeout(hctx, launchTimeout)
	ref, err := env.be.Launch(lctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: spec.Repo, Slug: slug}, Workflow: spec.Workflow, RunID: spec.RunID})
	cancel()
	if err != nil {
		if errors.Is(err, backend.ErrRejected) {
			// Definitive: the platform refused, so nothing started. Drop our
			// claim so a corrected retry can go at once.
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			releaseClaim(rctx, env, s, holder)
			rcancel()
			// A GCP API error is a remote failure (exit 2); %w keeps
			// ErrRejected and ErrNotFound visible to errors.Is.
			return res, remote(fmt.Errorf("the launch of %s was refused, so nothing started; fix the cause, then fugaro run --retry %s: %w", res.Run, res.Run, err))
		}
		// Ambiguous (timeout, 5xx, dropped connection, unreadable reply): the
		// execution may exist. Keep the claim; the runner's result.json tells
		// the truth, and --retry reads it.
		return res, remote(fmt.Errorf("launching %s: the outcome is unknown (%w); don't relaunch before %s: fugaro run --retry %s then reports the execution if it started, or launches it",
			res.Run, err, now.Add(claimTTL).UTC().Format(time.RFC3339), res.Run))
	}
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: "cloud-run", Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, LaunchedAt: now.UTC()}
	if err := s.WriteLaunch(hctx, l); errors.Is(err, runstore.ErrExists) {
		theirs, rerr := s.ReadLaunch(hctx)
		if rerr != nil {
			return res, remote(rerr)
		}
		return done(theirs, "already-launched")
	} else if err != nil {
		// The claim stays: nobody may relaunch until it goes stale, by which
		// time the runner has written result.json's execution for --retry.
		return res, remote(fmt.Errorf("launched %s but could not record launch.json (fugaro run --retry %s records it from result.json): %w", ref.Name, res.Run, err))
	}
	return done(l, "launched") // the claim stays in place for good
}

// releaseClaim drops our own claim after a failed launch, so a retry need
// not wait out claimTTL. It deletes only the exact object we hold.
func releaseClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string) {
	data, gen, err := env.bucket.Read(ctx, s.ClaimKey())
	var c runstore.Claim
	if err != nil || json.Unmarshal(data, &c) != nil || c.Holder != holder {
		return
	}
	_ = env.bucket.DeleteIf(ctx, s.ClaimKey(), gen, data)
}

// errClaimReleased means the claim a loser was waiting on disappeared:
// its holder's launch was refused and released it (N-12).
var errClaimReleased = errors.New("launch claim released")

// claimRounds bounds how often launchRun goes back for a released claim.
const claimRounds = 3

// takeClaim tries to take the run's launch claim. won is true when we hold
// it; otherwise other is the claim someone else holds, to wait on.
//
// A held claim is read ONCE: staleness is judged on that content and the
// takeover's ReplaceIf matches that read's generation, so nobody can
// replace a claim that turned fresh after we judged it stale (N-1). A
// claim that vanished before the read counts as absent (N-10).
func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {
	key := s.ClaimKey()
	someone := runstore.Claim{Holder: "another CLI", At: now}
	for attempt := 0; ; attempt++ {
		ok, _, err := s.Claim(ctx, holder, now) // Claim itself retries once if the claim vanishes mid-call
		if err != nil {
			return false, other, remote(err)
		}
		if ok {
			return true, other, nil
		}
		hook(launchHooks.beforeRead)
		prev, gen, err := env.bucket.Read(ctx, key)
		if errors.Is(err, blobx.ErrNotExist) {
			if attempt == 0 {
				continue // released between our Claim and this read: absent, so claim once more
			}
			return false, someone, nil // released twice in a row: never loop here
		}
		if err != nil {
			return false, other, remote(err)
		}
		var cur runstore.Claim
		if json.Unmarshal(prev, &cur) == nil && now.Sub(cur.At) < claimTTL {
			return false, cur, nil // fresh: someone is launching
		}
		// Stale (or unreadable): its holder presumably died. Take it over.
		hook(launchHooks.beforeTakeover)
		mine, err := json.Marshal(runstore.Claim{Holder: holder, At: now.UTC()})
		if err != nil {
			return false, other, err
		}
		if _, err := env.bucket.ReplaceIf(ctx, key, mine, gen, prev); errors.Is(err, blobx.ErrConflict) {
			return false, someone, nil // someone else changed the stale claim first: they launch
		} else if err != nil {
			return false, other, remote(err)
		}
		return true, other, nil
	}
}

// waitForLaunch waits up to claimWait for the claim holder's launch.json
// (N-4): a concurrent "fugaro run --run-id X" then reports the winner's
// launch with exit 0, as a repeated launch should. Otherwise exit 1. When
// the claim disappears (its holder was refused and released it), it
// returns errClaimReleased at once, so the caller can claim again.
func waitForLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec, run string, holder runstore.Claim) (*runstore.Launch, error) {
	deadline := time.Now().Add(claimWait)
	for {
		l, err := existingLaunch(ctx, env, s, spec)
		if err != nil {
			return nil, err
		}
		if l != nil {
			return l, nil
		}
		held, err := env.bucket.Exists(ctx, s.ClaimKey())
		if err != nil {
			return nil, remote(err)
		}
		if !held {
			return nil, errClaimReleased
		}
		if !time.Now().Before(deadline) {
			return nil, userErr("a launch of %s by %s (claimed %s) is still in flight after %s; run fugaro ls, or fugaro run --retry %s after %s",
				run, holder.Holder, holder.At.UTC().Format(time.RFC3339), claimWait, run, holder.At.Add(claimTTL).UTC().Format(time.RFC3339))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(claimPoll):
		}
	}
}

// claimHolder names this launch attempt: unique per call, even within one process.
func claimHolder() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%d", host, os.Getpid(), rand.Uint64())
}
