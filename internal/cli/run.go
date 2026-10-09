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
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
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
	//
	// Clock assumption: a claim's age is this machine's clock minus the
	// holder's (Claim.At). Every CLI's clock must be within
	// claimTTL - holdTimeout (5m) of every other's, or a live claim can be
	// judged stale and taken over. The object's server-side update time
	// would narrow that to this machine against Cloud Storage, not remove
	// it, and is not used. If the assumption fails, the result is a double
	// launch, which the runner's duplicate-execution check contains: the
	// second execution finds the first's record and exits writing nothing
	// (design §4.7).
	holdTimeout = claimTTL / 2
)

var (
	claimWait = 30 * time.Second // how long a loser waits for the winner's launch.json
	claimPoll = time.Second
)

// launchRecipe is the recipe a launch said it runs.
type launchRecipe struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// launchResult is what fugaro run reports, and its --json output.
type launchResult struct {
	Project   string `json:"project"` // the Fugaro project
	Run       string `json:"run"`
	Repo      string `json:"repo"`
	RunID     string `json:"run_id"`
	Branch    string `json:"branch"`
	Execution string `json:"execution"`
	LogURL    string `json:"log_url,omitempty"`
	Status    string `json:"status"` // launched | already-launched
	// PR and PreviousRun are a follow-up's pull request and the run it
	// continues; Branch is then the PR's branch.
	PR          int    `json:"pr,omitempty"`
	PreviousRun string `json:"previous_run,omitempty"`
	// Recipe is the run's recipe; nil when the runner chooses it (no
	// --recipe and no checkout of the repository: agent.recipe at the ref).
	Recipe *launchRecipe `json:"recipe,omitempty"`
	// ProjectLayer is the project layer the stored task actually carries,
	// nil when none applies (task.ProjectLayer, embedProjectLayer).
	ProjectLayer *launchProjectLayer `json:"project_layer,omitempty"`
}

// launchProjectLayer is launchResult's --json view of a task's project
// layer: just enough to tell which one it was, never its text.
type launchProjectLayer struct {
	SHA256     string `json:"sha256"`
	Generation int64  `json:"generation,omitempty"`
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
	totalTimeout                          string
	pr                                    int
	asJSON                                bool
	noBudgetCheck                         bool
	recipe                                string
}

func newRunCmd() *cobra.Command {
	var o runOptions
	cmd := &cobra.Command{
		Use:   "run [TEXT]",
		Short: "Launch a task in the cloud; it always ends with a PR",
		Long: "Launch a task in the cloud. The task is TEXT or --task-file (- for stdin).\n\n" +
			"A repeated --run-id reports the existing launch instead of starting another, so\n" +
			"a caller that is unsure whether its launch went through can simply repeat it.\n" +
			"--retry RUN launches a run whose task was stored but never started.\n\n" +
			"--pr N continues Fugaro's pull request N: the run acts on the PR's review comments\n" +
			"by the authors fugaro.yaml's followup.trusted lists, on the PR's own branch, with\n" +
			"the base branch and workflow of the runs before it. TEXT, or --task-file, is\n" +
			"optional then, and adds instructions.",
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
	f.StringVar(&o.totalTimeout, "total-timeout", "", "override the workflow's timeouts.total for this run, such as 45m")
	f.IntVar(&o.pr, "pr", 0, "continue Fugaro PR N: act on its trusted review comments (TEXT adds instructions)")
	f.StringVar(&o.recipe, "recipe", "", "the recipe (task loop) to run: .fugaro/recipes/NAME.yaml, the project's, or the catalog's (default, cheap-loop-senior, claude-solo); default: agent.recipe in fugaro.yaml, else default; a follow-up (--pr) keeps its previous run's")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.noBudgetCheck, "no-budget-check", false, "skip the launch pre-check of the budget (kill switches, missing caps, no headroom); the run is held to the same limits when it starts")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// taskFlags are the flags --retry excludes: the stored task.json supplies them.
var taskFlags = []string{"repo", "ref", "workflow", "run-id", "batch", "task-file", "total-timeout", "pr", "recipe"}

// prFlags are the flags --pr excludes: the previous run on the PR supplies them.
var prFlags = []string{"ref", "workflow"}

func runRun(cmd *cobra.Command, o *runOptions, args []string) error {
	ctx := cmd.Context()
	prSet := cmd.Flags().Changed("pr") && o.retry == ""
	if prSet {
		if o.pr <= 0 {
			return userErr("--pr %d: use a pull request number", o.pr)
		}
		for _, name := range prFlags {
			if cmd.Flags().Changed(name) {
				return userErr("--pr continues the PR's base branch and workflow; it takes no --%s", name)
			}
		}
	}
	var text string
	var total time.Duration
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
		if text, err = taskText(cmd, o.tf, args, prSet); err != nil {
			return err
		}
		if o.totalTimeout != "" {
			if total, err = parseTotalTimeout(o.totalTimeout); err != nil {
				return err
			}
		}
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	env.noBudgetCheck = o.noBudgetCheck

	var (
		spec   *task.Spec
		slug   string
		reused bool // a stored follow-up, which the PR's checks never saw run
	)
	switch {
	case o.retry != "":
		slug, spec, err = retrySpec(ctx, env, o.retry)
		reused = err == nil && spec.IsFollowUp()
	case prSet:
		slug, spec, reused, err = prSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
	default:
		slug, spec, err = newSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
	}
	if err != nil {
		return err
	}
	var layerData []byte
	if o.retry == "" && !reused {
		if layerData, err = embedProjectLayer(ctx, env, spec, cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	prior, err := existingLaunch(ctx, env, s, spec)
	if err != nil {
		return err
	}
	if prior == nil && reused {
		if err := recheckFollowUp(ctx, env, slug, spec, cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
	if prior == nil {
		agentRecipe, kind := "", ""
		if co := checkoutConfig(ctx, spec.Repo); co != nil {
			agentRecipe = co.Agent.Recipe
			if w, ok := co.Workflows[spec.Workflow]; ok {
				kind = w.Base
			}
		}
		if needsRecipeImage(spec, agentRecipe) {
			if err := checkRecipeImage(ctx, env, slug, spec, kind, cmd.ErrOrStderr()); err != nil {
				return err
			}
		}
		if spec.ProjectLayer != nil {
			if err := checkImageSince(ctx, env, slug, spec, kind, layeredSince, "the project layer", "the project layer", "", cmd.ErrOrStderr()); err != nil {
				return err
			}
		}
		if err := checkMaxParallel(ctx, env); err != nil {
			return err
		}
		if err := env.budgetPrecheck(ctx, slug, spec, cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
	if o.retry == "" {
		if err := createTask(ctx, s, spec); err != nil {
			return err
		}
		if !reused {
			// After createTask, so a repeated --run-id announces whatever it
			// adopted (the stored task's layer), not whatever this call
			// happened to resolve first.
			announceProjectLayer(cmd.ErrOrStderr(), spec, layerData)
		}
	}
	chooses := runnerChooses(ctx, spec)
	fmt.Fprintln(cmd.ErrOrStderr(), recipeLine(spec, chooses))
	res, err := launchRun(ctx, env, slug, spec, time.Now())
	if err != nil {
		return err
	}
	if o.retry != "" && res.Status == "already-launched" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s launched already; --retry only starts a run that never launched. "+
			"To see how it went, run fugaro diagnose %s; to redo it, start a new run.\n", res.Run, res.Run)
	}
	res.Project = env.lc.Name
	res.Recipe = launchRecipeOf(spec, chooses)
	if spec.ProjectLayer != nil {
		res.ProjectLayer = &launchProjectLayer{SHA256: spec.ProjectLayer.SHA256, Generation: spec.ProjectLayer.Generation}
	}
	return printLaunch(cmd.OutOrStdout(), res, o.asJSON)
}

// taskText is the task from exactly one of TEXT and --task-file. With
// optional (a follow-up, whose runner has instructions of its own) both
// may be absent, and the task may be empty.
func taskText(cmd *cobra.Command, file string, args []string, optional bool) (string, error) {
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
	case optional:
		return "", nil
	default:
		return "", userErr("pass the task as TEXT or with --task-file (- for stdin)")
	}
	if text = strings.TrimSpace(text); text == "" && !optional {
		return "", userErr("the task is empty")
	}
	return text, nil
}

// newSpec builds a new run's spec, and its repository's slug, from the
// flags, the local config and the checkout in the working directory.
func newSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string, total time.Duration, warn io.Writer) (string, *task.Spec, error) {
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
	if total > 0 {
		if c := checkout(); c != nil {
			if w, ok := c.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return "", nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
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
	rcp, err := chooseRecipe(ctx, env, o.recipe, checkout, checkoutRoot(ctx, repo), warn)
	if err != nil {
		return "", nil, err
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, Workflow: workflow, Task: text, RequestedBy: me, Batch: o.batch, Recipe: rcp}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return slug, spec, nil
}

// chooseRecipe is the recipe of a new first run: --recipe, else the
// checkout's agent.recipe, else default, resolved over the checkout at root,
// the project and the catalog. With no flag and no checkout it resolves
// default (a project default included), and a catalog default leaves the
// choice to the runner, which reads agent.recipe at the task's ref.
func chooseRecipe(ctx context.Context, env *cloudEnv, flag string, checkout func() *config.Config, root string, warn io.Writer) (*task.Recipe, error) {
	name, explicit := flag, flag != ""
	var cfg *config.Config
	if c := checkout(); c != nil {
		cfg = c
		if name == "" && c.Agent.Recipe != "" {
			name, explicit = c.Agent.Recipe, true
		}
	}
	if name == "" {
		name = recipe.DefaultName
	}
	rr, err := resolveRecipe(ctx, env, root, name, time.Now(), false)
	if err != nil {
		return nil, err
	}
	printRecipeNote(warn, rr, explicit)
	// An explicit --recipe default overrides agent.recipe, which applies when
	// the checkout sets it, or when there is no checkout and the runner reads
	// it at the ref: then the default is carried in the task.
	override := flag == recipe.DefaultName && (cfg == nil || cfg.Agent.Recipe != "")
	return rr.taskRecipeEmbedding(override), nil
}

// printRecipeNote prints rr's note when the user chose the recipe by name
// (--recipe or agent.recipe), that is not default, and the lookup fell
// through to the catalog; an offline-cache note (a project recipe in use)
// is always printed.
func printRecipeNote(warn io.Writer, rr *resolvedRecipe, explicit bool) {
	if rr.Note == "" {
		return
	}
	if rr.Source == recipe.SourceCatalog && (!explicit || rr.Name == recipe.DefaultName) {
		return
	}
	io.WriteString(warn, noteLine(rr.Note))
}

// runnerChooses reports whether the runner, not this CLI, decides spec's
// recipe: a first run with no recipe in its task and no checkout here.
func runnerChooses(ctx context.Context, spec *task.Spec) bool {
	return spec.Recipe == nil && !spec.IsFollowUp() && checkoutConfig(ctx, spec.Repo) == nil
}

// recipeLine is the launch's one-line recipe preview (decision D3).
func recipeLine(spec *task.Spec, chooses bool) string {
	switch {
	case spec.Recipe != nil:
		return fmt.Sprintf("recipe: %s (%s)", spec.Recipe.Name, spec.Recipe.Source)
	case chooses:
		return fmt.Sprintf("recipe: chosen by the runner (agent.recipe in fugaro.yaml at %s, else default)", oneLine(spec.Ref))
	}
	return "recipe: default (catalog)"
}

// launchRecipeOf is the --json form of recipeLine; nil when the runner chooses.
func launchRecipeOf(spec *task.Spec, chooses bool) *launchRecipe {
	switch {
	case spec.Recipe != nil:
		return &launchRecipe{Name: spec.Recipe.Name, Source: spec.Recipe.Source}
	case chooses:
		return nil
	}
	return &launchRecipe{Name: recipe.DefaultName, Source: string(recipe.SourceCatalog)}
}

// OverrideCap is the longest total time a run may ask for with
// --total-timeout: a runaway value is more likely a typo than a plan.
const OverrideCap = 24 * time.Hour

// parseTotalTimeout checks the parts of --total-timeout that need no
// checkout: the runner and the platform bound what is left.
func parseTotalTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, userErr("--total-timeout %q must be a duration such as 45m", s)
	}
	if d <= 0 {
		return 0, userErr("--total-timeout must be greater than 0")
	}
	if d > OverrideCap {
		return 0, userErr("--total-timeout %s is above the %s cap", d, OverrideCap)
	}
	if limit := gcp.MaxTaskTimeout - backend.TaskTimeoutSlack; d > limit {
		return 0, userErr("--total-timeout %s is above what a Cloud Run task allows (%s)", d, limit)
	}
	return d, nil
}

// launchTimeoutOf is the backend timeout of a stored task: its
// overrides.total_timeout, or zero for the job's own.
func launchTimeoutOf(spec *task.Spec) time.Duration {
	if spec.Overrides.TotalTimeout == "" {
		return 0
	}
	d, err := time.ParseDuration(spec.Overrides.TotalTimeout)
	if err != nil {
		return 0 // Validate refuses this earlier
	}
	return d
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

// horizonMargin is added to the longest job timeout for the max_parallel
// listing: the task timeout slack is added separately, and an hour more
// covers queueing.
const horizonMargin = time.Hour

// checkMaxParallel refuses a new launch when max_parallel runs are active.
// No run can be active for longer than the longest job timeout (or the
// override cap, whichever is more) plus the timeout slack and queueing, and
// the execution listing is newest first across every job, so it stops
// paging at that horizon. One jobs.list finds the longest timeout.
func checkMaxParallel(ctx context.Context, env *cloudEnv) error {
	longest, err := env.be.LongestTaskTimeout(ctx)
	if err != nil {
		return remote(err)
	}
	horizon := max(longest, OverrideCap) + backend.TaskTimeoutSlack + horizonMargin
	active, err := env.be.List(ctx, backend.ListFilter{ActiveOnly: true, Since: time.Now().Add(-horizon)})
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
	a, err1 := repeatTaskBytes(have)
	b, err2 := repeatTaskBytes(spec)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if string(a) != string(b) {
		return userErr("run ID %s already holds a different task", spec.RunID)
	}
	// The stored task is what the job actually reads: a repeat keeps its
	// project layer exactly as first launched, however spec just resolved
	// it (L8, L15 are about a first launch; a repeated run ID is the same
	// run, not a second resolution of it). Without this, a layer republished
	// between the first launch and the repeat would make this call embed a
	// different project_layer into the in-memory spec than what launchRun
	// and the --json report actually describe.
	spec.ProjectLayer = have.ProjectLayer
	return nil
}

// repeatTaskBytes is spec.Marshal with the project layer left out entirely,
// so a repeated --run-id is judged on everything the caller actually chose
// (repo, ref, workflow, task text, overrides, recipe...), never on the
// project layer: embedProjectLayer re-reads the bucket on every call
// (decision L13, no fresh window), so its content can legitimately differ
// between the first launch and a repeat (a republish, or just a new
// generation of unchanged bytes); without this, either would make a
// repeated --run-id spuriously look like "a different task".
func repeatTaskBytes(spec *task.Spec) ([]byte, error) {
	clone := *spec
	clone.ProjectLayer = nil
	return clone.Marshal()
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
	what := oneLine(res.Run)
	if res.PR > 0 {
		what += fmt.Sprintf(" (follow-up of PR #%d, after %s)", res.PR, oneLine(res.PreviousRun))
	}
	if _, err := fmt.Fprintf(w, "%s %s\n  branch %s\n", verb, what, oneLine(res.Branch)); err != nil {
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
// name launch.json holds. Either name must be of the run's own job: the
// run's service account can write both objects.
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
		// bucket): never launch it again. Nothing to backfill.
		return &runstore.Launch{Version: 1, RunID: runID, LaunchedAt: rec.StartedAt}, nil
	}
	if err := check(rec.Execution); err != nil {
		return nil, err
	}
	l := &runstore.Launch{Version: 1, RunID: runID, Backend: backend.CloudRun, Execution: rec.Execution, LaunchedAt: rec.StartedAt}
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
//     delete-then-create, so two takers can't both win. A claim that
//     vanished before the read counts as absent: claim once more.
//   - A fresh claim, or a lost takeover, means someone else is launching:
//     wait up to claimWait for their launch.json and report it, else
//     exit 1, "still in flight". A claim that disappears while we wait was
//     released after a refused launch: go back for it at once.
//   - A run with a cancel marker is never launched.
//   - Having won the claim, re-check launch.json and result.json: an earlier
//     holder may have launched just before going stale.
//   - Launch. Only a definitive refusal (backend.ErrRejected) releases our
//     claim; after an ambiguous error the execution may exist, so the claim
//     stays until it goes stale, by which time the runner's result.json says
//     whether it started.
//
// file:// buckets are single-user: fileblob's IfNotExist is not atomic.
func launchRun(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, now time.Time) (launchResult, error) {
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	res := launchResult{Run: slug + "/" + spec.RunID, Repo: spec.Repo, RunID: spec.RunID, Branch: "fugaro/" + spec.RunID,
		PR: spec.PR, PreviousRun: spec.PreviousRun}
	if spec.Branch != "" {
		res.Branch = spec.Branch
	}
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
	// the claim so a waiting cancel sees the launch end.
	if cancelled, err := s.CancelRequested(hctx); err != nil {
		return res, remote(err)
	} else if cancelled {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, userErr("run %s was cancelled; start a new one", res.Run)
	}
	// The run's budget identity: minted and left in the bucket before the
	// execution can start, by the one CLI that holds the claim. A failure
	// here means nothing started, so the claim goes back.
	minted, err := env.mintBudgetToken(hctx, slug, spec)
	if err != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, err
	}
	lctx, cancel := context.WithTimeout(hctx, launchTimeout)
	ref, err := env.be.Launch(lctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: spec.Repo, Slug: slug}, Workflow: spec.Workflow, RunID: spec.RunID, Timeout: launchTimeoutOf(spec)})
	cancel()
	if err != nil {
		if errors.Is(err, backend.ErrRejected) {
			// Definitive: the platform refused, so nothing started. Drop our
			// claim so a corrected retry can go at once.
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			releaseClaim(rctx, env, s, holder)
			if minted {
				env.dropBudgetToken(rctx, slug, spec.RunID) // nothing started, so nothing will take it
			}
			rcancel()
			// A GCP API error is a remote failure (exit 2); %w keeps
			// ErrRejected and ErrNotFound visible to errors.Is.
			return res, remote(fmt.Errorf("the launch of %s was refused, so nothing started; fix the cause, then fugaro run --retry %s: %w", res.Run, res.Run, err))
		}
		// Ambiguous (timeout, 5xx, dropped connection, unreadable reply): the
		// execution may exist. Keep the claim, and the token object: an
		// execution that did start needs it (a --retry mints another); the runner's result.json tells
		// the truth, and --retry reads it.
		return res, remote(fmt.Errorf("launching %s: the outcome is unknown (%w); don't relaunch before %s: fugaro run --retry %s then reports the execution if it started, or launches it",
			res.Run, err, now.Add(claimTTL).UTC().Format(time.RFC3339), res.Run))
	}
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: backend.CloudRun, Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, LaunchedAt: now.UTC()}
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
// its holder's launch was refused and released it.
var errClaimReleased = errors.New("launch claim released")

// claimRounds bounds how often launchRun goes back for a released claim.
const claimRounds = 3

// takeClaim tries to take the run's launch claim. won is true when we hold
// it; otherwise other is the claim someone else holds, to wait on.
//
// A held claim is read ONCE: staleness is judged on that content and the
// takeover's ReplaceIf matches that read's generation, so nobody can
// replace a claim that turned fresh after we judged it stale. A
// claim that vanished before the read counts as absent.
func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {
	key := s.ClaimKey()
	someone := runstore.Claim{Holder: "another CLI", At: now}
	for attempt := 0; ; attempt++ {
		ok, _, err := s.Claim(ctx, holder, now) // Claim itself retries once if the claim vanishes mid-call
		if err != nil {
			return false, other, claimError(s, key, err)
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
			return false, other, claimError(s, key, err)
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

// claimError is a failed read of the launch claim, as a remote error. An
// oversized claim is no claim a CLI wrote, and without its content and
// generation it can't be judged stale or taken over safely: say so.
func claimError(s *runstore.Store, key string, err error) error {
	if errors.Is(err, runstore.ErrTooLarge) || errors.Is(err, blobx.ErrTooLarge) {
		ref := s.Slug() + "/" + s.RunID()
		return remote(fmt.Errorf("the launch claim of %s is not one a CLI wrote (%w); delete %s, then fugaro run --retry %s", ref, err, key, ref))
	}
	return remote(err)
}

// waitForLaunch waits up to claimWait for the claim holder's launch.json
// so a concurrent "fugaro run --run-id X" then reports the winner's
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

// noteLine is a recipe's note as a stderr line; the note can carry text
// from the installation's config, so it is escaped like other such text.
func noteLine(note string) string {
	return "note: " + pluginwire.Printable(oneLineCLI(note)) + "\n"
}
