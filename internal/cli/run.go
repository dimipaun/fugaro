package cli

import (
	"cmp"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
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
	Project string `json:"project"` // the Fugaro project
	Run     string `json:"run"`
	Repo    string `json:"repo"`
	RunID   string `json:"run_id"`
	Branch  string `json:"branch"`
	// Ref is the ref a new run's branch is cut from, which its review
	// measures the run's work against; empty for a follow-up and a review
	// run, whose review reads each pull request's own base.
	Ref       string `json:"ref,omitempty"`
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
	// pr is --pr, repeated: a single entry continues Fugaro's own pull
	// request N (the only meaning before Task 12 of the generic-tool
	// plan); 2 to 4 need a mode: review recipe such as pick (G13).
	pr []int
	// attach is --attach, repeated: files this launch uploads into the
	// run's own inputs/ for a mode: artifact run to download (design
	// artifact-runs.md §4, plan Task 6). It is refused outright on a run id
	// that already holds a task, refused unless the launch resolves to an
	// artifact recipe, and refused over any of the workflow's three
	// inputs caps before the first byte is uploaded.
	attach        []string
	asJSON        bool
	noBudgetCheck bool
	recipe        string
	// attempts is --attempts: 2 to 4 launches a best-of-N batch of
	// independent runs of the same task, workflow, ref and recipe (G13).
	// It is refused together with --pr, --run-id, --retry or a
	// review-mode recipe (the last already refused by chooseRecipe, since
	// a batch never sets --pr).
	attempts int
	// title is --title, as the task stores it: the run's own job title
	// (task.Spec.Title), the one line every surface that names a run shows
	// — fugaro watch's rows, the budget registry's entry, the pull
	// request's title, runs ls. runRun validates and sanitises it once
	// (titleFlag), so every spec builder below writes the same value and
	// the follow-up comparison reads it back. Empty (the default) is no
	// title at all: the task prompt's first line is the title, exactly as
	// before the flag existed. A follow-up (--pr) inherits its previous
	// run's title unless this is given (newFollowUpSpec).
	title string
	// token is the named Claude account (docs/design/named-oauth-tokens.md)
	// this run uses: fugaro run --token. A follow-up (--pr) inherits its
	// previous run's account when this is empty; an explicit value overrides
	// it instead (owner ruling, 2026-10-12, task 9b review), through the
	// same allow-list, agent.auth and version-gate checks a first run's does.
	token string
	// planTask is --plan-task, as the task stores it: the id of one task in
	// one plan ("setup-wizard/W1.3"), recorded so the console can join this
	// run to a node of that plan's dependency graph (console dependency
	// graph plan, task GV1.2, decision GD5). runRun validates it once
	// (planTaskFlag), so every spec builder below writes the same value and
	// the repeat comparison reads it back. Empty (the default) is no claim
	// at all: the launch writes no plan_task field anywhere, exactly as
	// before the flag existed. A follow-up (--pr) carries one only when this
	// is given: it never inherits its previous run's, and is otherwise
	// joined to the graph through its pull request number (GV1.6).
	planTask string
	// rigor is --mode: the rigor mode this run's own launcher asks for
	// (docs/rigor-modes implementation-plan T1.6). It is the one input that
	// may raise the mode the run resolves from its configuration and never
	// lower it (D6), so a launcher that says nothing cannot outvote a
	// repository that configured one. Empty (the default) is no request at
	// all: the launch resolves from its layers alone, exactly as it did
	// before the flag existed, and its task carries no such field. Kept as
	// the flag's own string and validated against the vocabulary once, in
	// rigorFlags, because a value outside it is a launcher's mistake rather
	// than a run's opinion.
	rigor string
	// tier is --tier: the expectation tier this run's own launcher dispatches
	// it at (docs/task-governance spec §3, implementation-plan T8.1): how much
	// the run is expected to cost and take, which the runner records with the
	// expectation the governance resolved for it. Empty (the default) is no
	// expectation at all: the launch writes no such field and the record
	// carries no tier block. Kept as the flag's own string and validated
	// against the vocabulary once, in tierFlag, because a value outside it is
	// a launcher's mistake rather than a run's opinion about its own size.
	tier string
	// noCompression is --no-compression: turn the repository's
	// agent.compression off for this run only (docs/design/
	// headroom-compression.md §4). The override is deliberately
	// one-directional — there is no --compression flag — because turning
	// compression on is a reviewed fugaro.yaml change, and a flag that could
	// force it on from the command line would undo that for every workflow a
	// launcher can already run. It writes task.Spec.CompressionOff when the
	// resolved configuration turns compression on: with compression already
	// off there is nothing to override, and the field would only add version
	// skew (an older runner refuses the whole task). A configuration the
	// launcher could not read leaves nothing to judge, so the launch is
	// refused rather than guessed at (finishLaunch).
	noCompression bool
	// paths is --path, repeated: the paths this launch declares it will
	// touch, which the runner matches against the area rules at D22's first
	// moment (bootstrap). Nil (the default) is no declaration, and a
	// declared path that matches no rule changes nothing. A path that does
	// match replaces the repository's value with the area's, which may be
	// lower: areas are defaults, not floors (docs/project-layer.md §2), and
	// finalize resolves again from the paths the run really changed.
	paths []string
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
			"optional then, and adds instructions.\n\n" +
			"--recipe NAME --pr N, NAME naming a mode: review recipe such as review-only or pick,\n" +
			"reviews any open pull request of the repository instead (not only Fugaro's own) and\n" +
			"never pushes or opens one; repeat --pr (2 to 4 times) for pick, which compares them.\n" +
			"TEXT, or --task-file, is optional then too, and adds instructions to the review.\n\n" +
			"--attempts N (2 to 4) launches N independent runs of the same task as a best-of-N\n" +
			"batch, under one batch label (bestof-<first run id> unless --batch is given); once\n" +
			"they have all opened a pull request, compare them with fugaro run --recipe pick.\n" +
			"It takes no --pr, --run-id or --retry.\n\n" +
			"--title TEXT names the run: one line, at most 100 characters. It is what\n" +
			"fugaro watch and runs ls list the run by, and the title (with the body's first\n" +
			"heading) of the pull request the run opens when the agent writes no pr.md of\n" +
			"its own. Without it the run is named by the task's first line, so briefs that\n" +
			"start with the same preamble all read alike. A follow-up (--pr) keeps its run's\n" +
			"title unless --title is given, and --retry takes none (the stored task keeps its\n" +
			"own).\n\n" +
			"--plan-task PLAN/TASK records which task of which plan this run works on, so\n" +
			"the console's graph can join the run to that node. One plan and one task id,\n" +
			"as the plan's own graph spells them, at most 128 bytes; what the run does is\n" +
			"unchanged by it. A follow-up (--pr) records one only when it is given, and\n" +
			"--retry takes none (the stored task keeps its own).\n\n" +
			"--no-compression turns the repository's agent.compression off for this run only,\n" +
			"overriding its fugaro.yaml. There is no --compression flag: turning compression\n" +
			"on is a reviewed fugaro.yaml change. A follow-up (--pr) keeps its run's choice,\n" +
			"and --retry takes none (the stored task keeps its own).\n\n" +
			"--mode MODE asks for a rigor mode for this run: " + rigor.ModeList() + ".\n" +
			"It may raise the mode the run resolves from its own configuration\n" +
			"(rigor.default and rigor.areas in fugaro.yaml, the project layer's defaults)\n" +
			"and never lower it, so a launch that asks for less cannot outvote a repository\n" +
			"that asked for more. A follow-up (--pr) keeps the mode the PR's runs recorded\n" +
			"and may only raise it: asking for less is refused, and so is hotfix, which\n" +
			"only an operator's own run reaches. --path PATH declares a path this run will\n" +
			"touch, repeatable, matched against those area rules before the run starts;\n" +
			"without either flag a first run resolves from its configuration alone, and a\n" +
			"follow-up from that and the mode it keeps. Both are\n" +
			"recorded in the run's record and named in the pull request's status line.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRun(cmd, &o, args) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	f.StringVar(&o.ref, "ref", "", "base branch or ref the run is cut from (default: the branch this checkout is on, when origin has it and it is not the base branch; else the base branch (fugaro.yaml git.base_branch, then the local config's, then the project's base_branch, then main))")
	f.StringVar(&o.workflow, "workflow", "", "workflow to run (default: the repo's only workflow)")
	f.StringVar(&o.runID, "run-id", "", "run ID to use; repeating one reports its launch instead of starting another")
	f.StringVar(&o.batch, "batch", "", "batch label, to group runs in fugaro runs ls")
	f.StringVar(&o.title, "title", "", "the run's title, one line: what fugaro watch and runs ls list the run by, and the pull request's title unless the agent wrote its own pr.md (default: the task's first line); a follow-up (--pr) keeps its run's title unless this is given")
	f.StringVar(&o.planTask, "plan-task", "", "record the plan task this run works on, as <plan>/<task> (for example setup-wizard/W1.3): the id the console's graph joins this run to a node by; it changes nothing about what the run does, and a follow-up (--pr) records one only when given")
	f.StringVar(&o.tf, "task-file", "", "read the task from this file, or - for stdin")
	f.StringVar(&o.retry, "retry", "", "launch the stored task of RUN (<repo-slug>/<run-id> or a run ID) that never started")
	f.StringVar(&o.totalTimeout, "total-timeout", "", "override the workflow's timeouts.total for this run, such as 45m")
	f.IntSliceVar(&o.pr, "pr", nil, "continue Fugaro PR N: act on its trusted review comments (TEXT adds instructions); with a mode: review --recipe, review PR N instead (repeatable, 1 to 4, any open PR)")
	f.StringVar(&o.recipe, "recipe", "", "the recipe (task loop) to run: .fugaro/recipes/NAME.yaml, the project's, or the catalog's ("+strings.Join(recipe.CatalogNames(), ", ")+"); default: agent.recipe in fugaro.yaml, else default; a follow-up (--pr) keeps its previous run's")
	f.StringVar(&o.token, "token", "", "named Claude account (claude_accounts in the local config) this run uses, for a workflow whose agent.auth is oauth; default: agent.oauth_token in fugaro.yaml, else the repository's default account; a follow-up (--pr) keeps its previous run's account unless this is given explicitly, which overrides it (checked the same way as a first run's)")
	f.StringArrayVar(&o.attach, "attach", nil, "attach a file to the run: repeatable, uploaded to the run's own inputs/ and downloaded by a mode: artifact run into the checkout's inputs/ (needs a mode: artifact recipe; refused on a run ID that already has a task)")
	f.StringVar(&o.rigor, "mode", "", "the rigor mode to ask for: "+rigor.ModeList()+"; it may raise the mode the run resolves from its configuration (rigor.default, rigor.areas, the project layer) and never lower it; a follow-up (--pr) keeps the mode the PR's runs recorded and may only raise it, and takes no hotfix; without it a first run resolves from those layers alone, and a follow-up from those and the mode it keeps")
	f.StringArrayVar(&o.paths, "path", nil, "declare a path this run will touch, matched against the rigor.areas rules in effect (the repository's, and the project layer's beside them): repeatable; a run that declares none matches no area rule")
	f.StringVar(&o.tier, "tier", "", "the expectation tier to dispatch this run at: "+tier.List()+". It is what the run is expected to cost and take (governance.tiers in fugaro.yaml and the project layer), and never a cap: the run's own budget cap stays the ceiling. A --pr follow-up keeps the tier its PR's runs recorded; without this flag a run carries no expectation at all")
	f.IntVar(&o.attempts, "attempts", 0, "launch N (2 to 4) independent runs of the same task, workflow, ref and recipe as a best-of-N batch (bestof-<first run id> unless --batch is given); refused with --pr, --run-id, --retry or a review-mode recipe")
	f.BoolVar(&o.noCompression, "no-compression", false, "turn agent.compression off for this run only, overriding fugaro.yaml; there is no flag to turn it on, which is a reviewed fugaro.yaml change")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.noBudgetCheck, "no-budget-check", false, "skip the launch pre-check of the budget (kill switches, missing caps, no headroom); the run is held to the same limits when it starts")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// taskFlags are the flags --retry excludes: the stored task.json supplies them.
var taskFlags = []string{"repo", "ref", "workflow", "run-id", "batch", "title", "plan-task", "task-file", "total-timeout", "pr", "recipe", "token", "attach", "mode", "path", "tier", "no-compression"}

// prFlags are the flags --pr excludes: the previous run on the PR supplies them.
var prFlags = []string{"ref", "workflow"}

func runRun(cmd *cobra.Command, o *runOptions, args []string) error {
	ctx := cmd.Context()
	attemptsSet := cmd.Flags().Changed("attempts")
	if attemptsSet {
		if o.attempts < 2 || o.attempts > 4 {
			return userErr("--attempts: use 2 to 4, got %d", o.attempts)
		}
		for _, name := range []string{"pr", "run-id", "retry"} {
			if cmd.Flags().Changed(name) {
				return userErr("--attempts launches a fresh batch; it takes no --%s", name)
			}
		}
	}
	prSet := cmd.Flags().Changed("pr") && o.retry == ""
	if prSet {
		if len(o.pr) == 0 {
			return userErr("--pr: use a pull request number")
		}
		if len(o.pr) > 4 {
			return userErr("--pr: at most 4 may be given")
		}
		for _, n := range o.pr {
			if n <= 0 {
				return userErr("--pr %d: use a pull request number", n)
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
		// --title, resolved once for every launch shape below (a first run,
		// a best-of-N batch, a review, a follow-up): o.title then holds the
		// title exactly as the task stores it, or "" for no title at all.
		if err = titleFlag(cmd, o); err != nil {
			return err
		}
		// --plan-task, resolved once for every launch shape below, like the
		// title: o.planTask then holds the id exactly as the task stores it,
		// or "" for no claim at all.
		if err = planTaskFlag(cmd, o); err != nil {
			return err
		}
		// --mode and --path, resolved once for every launch shape below, as
		// --title is: a request the launcher accepts here is the one every
		// spec builder writes, and a refused one leaves nothing behind.
		if err = rigorFlags(o); err != nil {
			return err
		}
		// --tier, resolved the same way and for the same reason: the
		// vocabulary is closed, and a launch that names none writes no field.
		if err = tierFlag(o); err != nil {
			return err
		}
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

	// --pr's second meaning (design generic-tool §3.6, G12): with an
	// explicit --recipe naming a mode: review recipe, --pr targets any
	// open pull request of the repository, not only Fugaro's own, and may
	// repeat (pick compares them). Detected only from an explicit --recipe,
	// never a bare agent.recipe default: the ordinary follow-up path below
	// (prSpec) already resolves that case from the PR's own history.
	var reviewRecipe *resolvedRecipe
	if prSet {
		switch {
		case o.recipe == "":
			if len(o.pr) > 1 {
				return userErr("several --pr need a review-mode recipe such as pick")
			}
		default:
			repo := o.repo
			if repo == "" {
				if repo, err = originRepo(ctx); err != nil {
					return err
				}
			}
			o.repo = repo
			// Checked before resolveRecipe (review finding E, the same
			// ordering newSpec/prSpec already enforce internally):
			// resolveRecipe can read a project or catalog recipe from the
			// bucket, a network call a disallowed --token should never
			// trigger. reviewSpec repeats this same check once a review
			// recipe is confirmed below (it is pure and local, so doing it
			// twice costs nothing); this call is what stops the read
			// itself from ever running for a refused account.
			// The ref is not known yet here (--pr's follow-up path takes it
			// from the previous run): the account is resolved from the flag
			// and the checkout's own value, and reviewSpec resolves it again
			// from the ref it names once one is.
			if _, err := claudeAccountFor(ctx, env, repo, o.token, o.ref, checkoutOf(ctx, repo)()); err != nil {
				return err
			}
			rr, rerr := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), o.recipe, time.Now(), false)
			switch {
			case rerr == nil && rr.Recipe.Mode == recipe.ModeReview:
				if lo, hi, ok := recipe.ReviewPRRange(rr.Name); ok {
					if n := len(o.pr); n < lo || n > hi {
						if lo == hi {
							return userErr("--recipe %s needs exactly %d --pr, got %d", rr.Name, lo, n)
						}
						return userErr("--recipe %s needs %d to %d --pr, got %d", rr.Name, lo, hi, n)
					}
				}
				reviewRecipe = rr
			case len(o.pr) > 1:
				return userErr("several --pr need a review-mode recipe such as pick")
			case rerr != nil:
				// A single --pr with a --recipe that does not resolve (or
				// resolves but is not mode: review) is an ordinary
				// follow-up candidate: prSpec below re-resolves --recipe
				// itself, but only after its own account check (review
				// finding E, named-oauth-tokens task 9b) — returning rerr
				// here instead, unconditionally, would run this read
				// before that check ever has a chance to refuse a
				// disallowed --token or inherited account.
			}
		}
	}
	if prSet && reviewRecipe == nil {
		for _, name := range prFlags {
			if cmd.Flags().Changed(name) {
				return userErr("--pr continues the PR's base branch and workflow; it takes no --%s", name)
			}
		}
	}
	if attemptsSet {
		return runAttempts(cmd, env, o, text, total)
	}

	var (
		spec   *task.Spec
		slug   string
		reused bool // a stored follow-up, which the PR's checks never saw run
	)
	switch {
	case o.retry != "":
		slug, spec, err = retrySpec(ctx, env, o.retry)
		reused = err == nil && spec.IsFollowUp()
	case reviewRecipe != nil:
		slug, spec, err = reviewSpec(ctx, env, o, reviewRecipe, text, total, cmd.ErrOrStderr())
	case prSet:
		slug, spec, reused, err = prSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
	default:
		slug, spec, err = newSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
	}
	if err != nil {
		return err
	}
	if o.retry == "" {
		noteRigorRequest(ctx, o, spec.Repo, cmd.ErrOrStderr())
	}
	res, err := finishLaunch(ctx, env, o, slug, spec, reused, cmd.ErrOrStderr(), false)
	if err != nil {
		return err
	}
	return printLaunch(cmd.OutOrStdout(), res, o.asJSON)
}

// finishLaunch is everything a launch does once its spec is built, however
// it was built (a new task, a follow-up, a review or a retry): embedding the
// project layer, the image and budget gates, storing the task and starting
// the execution. A best-of-N batch (runAttempts, through the launchAttempt
// seam) calls this once per attempt, each with its own fresh spec from
// buildUniqueAttemptSpec, and skipBudget true: the batch already leased its
// whole budget headroom once, before any attempt (design generic-tool
// review, 2026-10-13), so a per-attempt re-check here would both re-judge
// already-leased state and print its notes once per attempt instead of once
// per batch.
func finishLaunch(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, reused bool, warn io.Writer, skipBudget bool) (launchResult, error) {
	var layerData []byte
	var err error
	if o.retry == "" && !reused {
		if layerData, err = embedProjectLayer(ctx, env, spec, warn); err != nil {
			return launchResult{}, err
		}
	}
	// Whether the configuration this run will read turns compression on,
	// resolved once for every launch shape (a first run, a follow-up, a
	// review, a retry, each attempt of a batch) and used by both --no-compression
	// and the image gate below. Read after the layer is embedded, so it is the
	// resolution the run itself makes. compressionKnown is false when the
	// launcher could not read that configuration at all — a --repo launch from
	// outside a checkout of that repository, say — which is not the same as
	// reading one that leaves compression off.
	compression, compressionKnown := compressionRequested(ctx, env, spec)
	// --no-compression over a configuration the launcher could not read is
	// refused rather than guessed at: writing the field would put
	// compression_off on a task whose repository may never turn compression on,
	// and an older runner refuses the whole task for a field it does not know
	// (task.Parse decodes with DisallowUnknownFields) — so a flag that only
	// ever turns compression off would be what fails such a launch at
	// bootstrap. Skipping the image gate in exchange for the guess would let
	// that same run start on such a runner. Refused before anything is written
	// or uploaded, so nothing starts.
	if o.noCompression && !compressionKnown {
		return launchResult{}, userErr("--no-compression: the launcher cannot read %s's fugaro.yaml at %s to know whether compression is on; run from a checkout that has the ref, or drop the flag", spec.Repo, spec.Ref)
	}
	// --no-compression writes the task's own field only when there is
	// compression to turn off, the way the title and the plan task are
	// written: a launch that has nothing to override carries no field and
	// stays byte-identical to what the previous release wrote. Written before
	// createTask below, so a repeated --run-id with a flag the stored task
	// does not carry is refused as a different task rather than silently
	// dropped.
	if o.noCompression && compression {
		spec.CompressionOff = true
	}
	// The compression image gate's own condition, resolved here beside the
	// write and used by both launch branches below. It is the field's as much
	// as the configuration's, and the two halves catch different failures: a
	// run that will read a configuration turning compression on needs a runner
	// that can parse the key, and a task carrying compression_off needs one
	// that can parse the field — task.Parse decodes with DisallowUnknownFields,
	// so an older runner refuses the whole task either way. The field's half is
	// not implied by the configuration's: a follow-up (--pr) and a --retry
	// inherit the field from the run they continue, which carries it even after
	// the repository's key has been removed. A launch that writes no field and
	// reads no such key is never gated.
	compressionGate := compression || spec.CompressionOff
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	// The run-id freshness check, first of everything an --attach launch
	// does and before the first byte it could upload (design §4 step 1,
	// plan Review Focus 11). It is outside the prior == nil block below
	// because a repeated --run-id that already launched must be refused
	// here too rather than report the existing launch while holding
	// attachments that never went anywhere.
	if len(o.attach) > 0 {
		if err := attachRefusesStoredRun(ctx, s, spec.RunID); err != nil {
			return launchResult{}, err
		}
	}
	prior, err := existingLaunch(ctx, env, s, spec)
	if err != nil {
		return launchResult{}, err
	}
	// The other half of that check, and the last place --attach can still
	// refuse a run that exists: launch.json (or result.json's execution)
	// says this run id has launched while no task.json is there to read (a
	// task deleted after the fact, a record backfilled without one). Every
	// --attach step lives inside the prior == nil block below — the caps,
	// the image gate, the upload itself — so a launch that found a prior
	// would write a task naming no inputs while the files the owner named
	// went nowhere, silently. No attachment is added to a run that already
	// went: it started with the task it started with.
	if prior != nil && len(o.attach) > 0 {
		return launchResult{}, userErr("run ID %s has already launched; --attach uploads a run's files before it starts, so it never adds attachments to a run that already went — use a new run ID", spec.RunID)
	}
	if prior == nil && reused {
		if err := recheckFollowUp(ctx, env, slug, spec, warn); err != nil {
			return launchResult{}, err
		}
	}
	if o.retry == "" {
		// Before the image gate below, not after: a run ID whose earlier
		// attempt stored a task but never launched it (prior == nil, an
		// ambiguous launch failure, say) must be gated on the layer that
		// stored task actually carries, since createTask keeps it
		// regardless of what embedProjectLayer just freshly re-resolved
		// (repeatTaskBytes excludes the layer from "is this the same
		// task", and createTask adopts the stored one either way). Doing
		// this adoption only here, as a read-only peek, keeps a genuinely
		// new launch's image gate running before any write, as it always
		// has: createTask's own, later ReadTask only happens on ErrExists.
		if err := adoptStoredProjectLayer(ctx, s, spec); err != nil {
			return launchResult{}, err
		}
	}
	if prior == nil {
		// The cascade's own gate, first: nothing about a run this launch
		// cannot carry is worth an --attach upload, a cap check or a claim
		// (cascadeLaunchRefusal).
		if err := cascadeLaunchRefusal(ctx, env, spec); err != nil {
			return launchResult{}, err
		}
		// --attach, before the gates below: its recipe refusal, its caps,
		// D9's line and its image gate (design §4, plan P7/P9). It uploads
		// nothing here — attachUpload does that below, after the last gate
		// that can still refuse this launch, so a refused launch leaves
		// nothing in the bucket under a run id that never gets a task. A
		// launch with no --attach does nothing here at all.
		agentRecipe, kind, clone, shared := "", "", false, false
		// mode is the same answer for baseBranchSkew, and nil where clone is
		// false for the other gates because nothing was read: a launch for
		// another repository from a checkout that is not its own reads no
		// workflow of it, and baseBranchSkew asks both judges there rather
		// than take the one a derived workflow would use (its comment has why).
		var mode *bool
		if co := checkoutConfig(ctx, spec.Repo); co != nil {
			agentRecipe = co.Agent.Recipe
			if w, ok := co.Workflows[spec.Workflow]; ok {
				kind = w.BaseKind()
				clone = w.Checkout == config.CheckoutClone
				shared = w.Checkout == config.CheckoutShared
				m := clone
				mode = &m
			}
		}
		caps, err := attachLaunch(ctx, env, slug, spec, s.Prefix(), o.attach, kind, clone, warn)
		if err != nil {
			return launchResult{}, err
		}
		// The base branch's own gate, before anything is written: a runner
		// older than baseBranchSince cannot read the field and would refuse
		// the whole task, so the field is dropped rather than the launch
		// (baseBranchSkew). Here, and not with the gates below, because it
		// decides what the task createTask writes.
		baseBranchSkew(ctx, env, slug, spec, mode, warn)
		// checkRecipeImage and checkImageSince judge a workflow's derived
		// image by its build record, which a checkout: clone workflow never
		// has (it runs the installation's base image directly, not a
		// per-repository build): checkCloneImageSince judges base_images.base
		// itself instead. cloneSince is unconditional: a clone workflow needs
		// the runner's own blobless-clone bootstrap (plan Task 17,
		// FUGARO_CHECKOUT) whether or not it uses a recipe or a project
		// layer, and nothing else here enforces that base_images.base is
		// actually that recent: fugaro-base could first be published from an
		// older release, or --image-source could mirror one.
		if clone {
			if err := checkCloneImageSince(env.lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", warn); err != nil {
				return launchResult{}, err
			}
			if needsRecipeImage(spec, agentRecipe) {
				if err := checkCloneRecipeImage(env.lc, spec, warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.ProjectLayer != nil {
				if err := checkCloneImageSince(env.lc, spec, layeredSince, "the project layer", "the project layer", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.OAuthToken != "" {
				if err := checkCloneImageSince(env.lc, spec, namedTokensSince,
					"Claude account "+pluginwire.Printable(spec.OAuthToken), "named Claude accounts", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.Title != "" {
				if err := checkCloneImageSince(env.lc, spec, titlesSince,
					"a job title (--title, kept by --pr follow-ups and --retry)", "the job title", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.PlanTask != "" {
				if err := checkCloneImageSince(env.lc, spec, planTaskSince,
					"a plan task (--plan-task)", "the plan task", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The compression gate: a runner older than compressionSince knows
			// neither the key nor the field and refuses whichever it meets at
			// bootstrap, so the launch is judged on both (compressionGate
			// above). It fires whether or not --no-compression is set, since
			// the override turns the sidecar off and not the key the old
			// runner cannot read.
			if compressionGate {
				if err := checkCloneImageSince(env.lc, spec, compressionSince,
					"agent.compression", "context compression", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The rigor: gate, judged on the file the run will read rather
			// than on a task field: see checkRigorImage. Unconditional, since
			// the repository's file alone can carry the key.
			if err := checkCloneRigorImage(ctx, env, spec, warn); err != nil {
				return launchResult{}, err
			}
			// The governance: gate, judged the same way and for the same
			// reason: see checkGovernanceImage.
			if err := checkCloneGovernanceImage(ctx, env, spec, warn); err != nil {
				return launchResult{}, err
			}
			// The run's own rigor request, gated on the field the same way:
			// a runner older than rigorRequestSince refuses a task carrying
			// rigor or paths outright (task.Parse decodes with
			// DisallowUnknownFields). A launch that carries neither field is
			// never gated — but a follow-up that passed no flag still carries
			// one when its PR recorded a mode (T5.2), which is what
			// rigorGateSubject says in the refusal.
			if asksForRigor(spec) {
				if err := checkCloneImageSince(env.lc, spec, rigorRequestSince,
					rigorGateSubject(o, spec), "the rigor request", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The tier a task carries, judged on the field like the request
			// above and under the release that ships both the field and the
			// governance: block its expectation comes from (governanceSince):
			// a runner older than that refuses a task carrying a tier outright
			// (task.Parse decodes with DisallowUnknownFields). A launch that
			// carries none is never gated — but a follow-up that passed no
			// flag still carries one when its PR recorded a tier, which is
			// what tierGateSubject says in the refusal.
			if spec.Tier != "" {
				if err := checkCloneImageSince(env.lc, spec, governanceSince,
					tierGateSubject(o, spec), "the tier", "", warn); err != nil {
					return launchResult{}, err
				}
			}
		} else {
			if needsRecipeImage(spec, agentRecipe) {
				if err := checkRecipeImage(ctx, env, slug, spec, kind, warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.ProjectLayer != nil {
				if err := checkImageSince(ctx, env, slug, spec, kind, layeredSince, "the project layer", "the project layer", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			if spec.OAuthToken != "" {
				if err := checkImageSince(ctx, env, slug, spec, kind, namedTokensSince,
					"Claude account "+pluginwire.Printable(spec.OAuthToken), "named Claude accounts", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The job title's own gate, for the same reason as the two
			// above: task.Parse decodes with DisallowUnknownFields, so a
			// runner older than titlesSince refuses a task carrying a title
			// outright. A launch with no --title carries no such field (the
			// spec is byte-identical to what the previous release wrote) and
			// is never gated: the check is the field's, not the flag's.
			if spec.Title != "" {
				if err := checkImageSince(ctx, env, slug, spec, kind, titlesSince,
					"a job title (--title, kept by --pr follow-ups and --retry)", "the job title", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The plan task's own gate, for the same reason as the three
			// above: task.Parse decodes with DisallowUnknownFields, so a
			// runner older than planTaskSince refuses a task carrying one
			// outright, and the run would die at bootstrap over a field that
			// changes nothing about what it does. A launch with no
			// --plan-task carries no such field (the spec is byte-identical
			// to what the previous release wrote) and is never gated: the
			// check is the field's, not the flag's.
			if spec.PlanTask != "" {
				if err := checkImageSince(ctx, env, slug, spec, kind, planTaskSince,
					"a plan task (--plan-task)", "the plan task", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The compression gate: the same gate as the clone branch's above,
			// and for the same reason. It is judged from the deployed job's
			// build record because that record names the base image the image
			// runs the binary of.
			if compressionGate {
				if err := checkImageSince(ctx, env, slug, spec, kind, compressionSince,
					"agent.compression", "context compression", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The rigor: gate, judged on the file the run will read rather
			// than on a task field: see checkRigorImage. Unconditional, since
			// the repository's file alone can carry the key.
			if err := checkRigorImage(ctx, env, slug, spec, kind, warn); err != nil {
				return launchResult{}, err
			}
			// The governance: gate, the same shape as the clone branch's
			// above and for the same reason.
			if err := checkGovernanceImage(ctx, env, slug, spec, kind, warn); err != nil {
				return launchResult{}, err
			}
			// The same gate as the clone branch's above, and for the same
			// reason: the request is a new task field, and a runner that
			// predates it refuses the whole spec at bootstrap — bytes in the
			// bucket under a run that never starts. Judged from the deployed
			// job's build record, and never reached by a launch that carries
			// no such field at all.
			if asksForRigor(spec) {
				if err := checkImageSince(ctx, env, slug, spec, kind, rigorRequestSince,
					rigorGateSubject(o, spec), "the rigor request", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The same gate as the clone branch's above, and for the same
			// reason: the tier is a new task field, a runner older than
			// governanceSince refuses the whole spec at bootstrap, and a
			// launch that carries none is never gated.
			if spec.Tier != "" {
				if err := checkImageSince(ctx, env, slug, spec, kind, governanceSince,
					tierGateSubject(o, spec), "the tier", "", warn); err != nil {
					return launchResult{}, err
				}
			}
			// The digest drift, last of this branch's image reads, and the
			// only one that never refuses: a checkout: clone workflow has no
			// derived image and so no record to compare (the branch above),
			// and nothing here can stop a launch D619 ruled may not be
			// pinned.
			warnImageDigestDrift(ctx, env, slug, spec, warn)
		}
		// A checkout: shared workflow's job runs a shared dependency-layer
		// image, not a per-repository one: it is judged on the image the lock
		// says its job is on (checkSharedImageSince), in neither branch above —
		// the clone branch's judge is the local config's base image, which is
		// not what built the shared one, and the other branch's is a build
		// record a repository that adopted a sibling's image never wrote.
		if shared {
			if err := checkSharedImageSince(ctx, env, slug, spec, sharedSince, warn); err != nil {
				return launchResult{}, err
			}
		}
		if spec.Mode == recipe.ModeArtifact {
			// The mode: artifact gate: a task carrying mode is refused
			// outright by a runner older than artifactsSince (task.Parse
			// decodes with DisallowUnknownFields), so the launch must not
			// reach such a job at all. Judged from the deployed job's build
			// record exactly as the project layer's and the named token's
			// gates above are, and for an artifact recipe launched with
			// nothing attached just as much as for one launched with
			// --attach.
			if clone {
				if err := checkCloneImageSince(env.lc, spec, artifactsSince, "a mode: artifact recipe", "mode: artifact", "", warn); err != nil {
					return launchResult{}, err
				}
			} else if err := checkImageSince(ctx, env, slug, spec, kind, artifactsSince, "a mode: artifact recipe", "mode: artifact", "", warn); err != nil {
				return launchResult{}, err
			}
		}
		// The author, last of the fields this launch decides: the key that
		// opted the repository in is judged first and refuses an image that
		// cannot read it, the field is dropped rather than refused where the
		// image is not known to read it (authorSkew), and only then is the
		// identity resolved from this machine's local config.
		if err := authorGates(ctx, env, slug, spec, kind, mode, warn); err != nil {
			return launchResult{}, err
		}
		// The seal's gate (D19): a launch that mints hands its execution the
		// seal key in the marker, which only a runner at sealSince or later
		// parses without echoing and reads the token for. Judged here, before
		// the claim, the mint or the object, like the gates above.
		if err := env.sealGate(ctx, slug, spec, kind, clone, warn); err != nil {
			return launchResult{}, err
		}
		if err := checkMaxParallel(ctx, env); err != nil {
			return launchResult{}, err
		}
		// The artifact gate: locally computable (a recipe's caps against
		// the workflow's memory, a render or probe step against the
		// commands they need), so it runs before the budget read below and
		// before createTask writes anything at all.
		if err := checkArtifactLaunch(ctx, env, spec); err != nil {
			return launchResult{}, err
		}
		if !skipBudget {
			if err := env.budgetPrecheck(ctx, slug, spec, warn); err != nil {
				return launchResult{}, err
			}
		}
		// The project's policy, read with the operator's own credentials
		// before anything of this launch is written (plan task 1.5 step 4):
		// the repository must be admitted, and the policy must not be newer
		// than this binary. Before createTask and before the launch claim, so
		// a refused launch leaves no task, no token and no execution behind.
		// Outside the --no-budget-check branch, which is deliberate: neither
		// check is a budget check, so neither is an operator's to skip. After
		// the budget pre-check, so an unreachable database is refused by the
		// pre-check's own fail-closed message when that check runs; the gate
		// refuses it itself, with the node named, when it does not. Before the
		// attach upload below as well, so a repository the policy does not
		// admit never gets its inputs into the bucket either.
		if err := launchPolicyGates(ctx, env, slug, spec); err != nil {
			return launchResult{}, err
		}
		// The upload, last of everything an --attach launch does and still
		// before createTask: every refusal this launcher can make from what
		// it already has has been made above, so a refused launch leaves no
		// input object under a run id that never gets a task (design §4
		// step 1, plan Review Focus 11), and the task written next names
		// exactly the bytes that are in the bucket.
		if err := attachUpload(ctx, env, spec, s.Prefix(), o.attach, caps); err != nil {
			return launchResult{}, err
		}
	}
	if o.retry == "" {
		if err := createTask(ctx, s, spec); err != nil {
			return launchResult{}, err
		}
		if !reused {
			// After createTask, so a repeated --run-id announces whatever it
			// adopted (the stored task's layer), not whatever this call
			// happened to resolve first.
			announceProjectLayer(warn, spec, layerData)
		}
	}
	chooses := runnerChooses(ctx, spec)
	fmt.Fprintln(warn, recipeLine(spec, chooses))
	res, err := launchRun(ctx, env, slug, spec, time.Now())
	if err != nil {
		return launchResult{}, err
	}
	if o.retry != "" && res.Status == "already-launched" {
		fmt.Fprintf(warn, "%s launched already; --retry only starts a run that never launched. "+
			"To see how it went, run fugaro diagnose %s; to redo it, start a new run.\n", res.Run, res.Run)
	}
	res.Project = env.lc.Name
	res.Recipe = launchRecipeOf(spec, chooses)
	if spec.ProjectLayer != nil {
		res.ProjectLayer = &launchProjectLayer{SHA256: spec.ProjectLayer.SHA256, Generation: spec.ProjectLayer.Generation}
	}
	return res, nil
}

// warnImageDigestDrift warns, once, when the workflow's image is no longer at
// the digest its build record names: the record and the registry's latest tag
// disagree, so what the job runs and what the record says it runs are not the
// same image. It is a warning and never a refusal, and it pins nothing: Cloud
// Run cannot vary the image per execution, so a launch has no way to run the
// recorded digest (D619) — it only tells the operator, whose fix is the daily
// image check or fugaro image refresh. Neither side's read can refuse the
// launch either: a workflow with no build record at all — a checkout: clone
// workflow runs the installation's base image and is never built — is silent,
// anything else the launcher cannot read is a warning, and an identity the
// registry refuses is silent too (registryDenied).
//
// The reference it reads is the installation's own image for this workflow —
// the same derivation the job spec uses (Workflow's registryPath and
// ImageName) — and never the record's own Image field: the record comes from
// the runs bucket, so building the outbound read's host out of it would let a
// hand-edited or forged record choose where the launcher connects, and a
// registry-path change after the build would leave the record naming an image
// this installation no longer has. A record naming no digest of its own is
// nothing to compare, so the registry is not read at all.
func warnImageDigestDrift(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, warn io.Writer) {
	skip := func(why string) {
		fmt.Fprintf(warn, "warning: the image digest of %s workflow %s is not compared with its build record: %s\n", spec.Repo, spec.Workflow, why)
	}
	host, err := infra.RegistryHost(env.lc)
	if err != nil {
		skip(oneLine(err.Error()))
		return
	}
	image := gcp.ImageName(host+"/"+gcp.RegistryRepoID(slug), slug, spec.Workflow)
	b, err := env.recordBucket(ctx)
	if err != nil {
		skip(oneLine(err.Error()))
		return
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return
	case err != nil:
		skip(oneLine(err.Error()))
		return
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		skip("the record is unreadable (" + oneLine(err.Error()) + ")")
		return
	}
	if rec.ImageDigest == "" {
		return
	}
	live, err := checkRegistry().Digest(ctx, image+":latest")
	switch {
	// A launchers-only identity holds no artifactregistry read (the
	// operator role has it): every launch would otherwise warn about a
	// permission it was never meant to have, which is not drift and not
	// anything its reader can act on. Silence here says "not compared" by
	// saying nothing, exactly as a workflow with no build record does.
	case registryDenied(err):
		return
	// The tag the build promoted the recorded digest to is gone — the
	// daily check's own latest-drift case (LatestMissing) — which is the
	// one read failure the operator can act on, so it is named as such.
	case errors.Is(err, imagecheck.ErrManifestUnknown):
		fmt.Fprintf(warn, "warning: the registry has no %s:latest, so the digest the build record of %s workflow %s names (%s) is not the one that tag resolves to\n", image, spec.Repo, spec.Workflow, oneLine(rec.ImageDigest))
		return
	case err != nil:
		skip(oneLine(err.Error()))
		return
	}
	if rec.DigestMismatch(live) {
		fmt.Fprintf(warn, "warning: the image of %s workflow %s is at digest %s, not the %s its build record names\n", spec.Repo, spec.Workflow, oneLine(live), oneLine(rec.ImageDigest))
	}
}

// registryDenied reports whether err is the registry refusing the launcher's
// identity rather than failing to answer: a 401 or 403 from the manifest read.
// The launcher role is granted run.jobs, run.executions, run.operations.get
// and secretmanager.list, and artifactregistry read goes to operators alone
// (deploy/terraform/gcp/modules/repo/registry.tf), so a principal that may
// launch and may not read the registry is the expected state rather than a
// fault. The check is textual because the error crosses the OCI client, which
// reports the status in its message and wraps nothing a caller can match on.
func registryDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403")
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

// titleFlag resolves fugaro run --title into the title the task stores, in
// o.title: the flag trimmed, checked against task's ONE list of what a
// title may be (task.TitleProblem, the same list the runner applies when it
// reads a task back, so a title this accepts is never one the run refuses
// at bootstrap) and sanitised the way every other string this program shows
// is (safetext.Field: bidi and zero-width characters dropped, runs of
// whitespace collapsed to one space).
//
// The flag's absence is not a title, and leaves o.title empty — every
// surface then falls back to the task's first line, exactly as before the
// flag existed. Its PRESENCE with nothing in it is a mistake and is
// refused: --title "" would otherwise be silently the same as no --title at
// all, which is the one reading nobody meant. The emptiness check runs
// again on the sanitised value, because the characters safetext.Field drops
// (zero-width spaces and joiners, bidi embeddings and overrides, word
// joiners, soft hyphens, the BOM) are not whitespace: a title made only of
// them passes the trim and the check, and would otherwise be stored as no
// title at all — the same silent no-op, one layer down.
func titleFlag(cmd *cobra.Command, o *runOptions) error {
	if !cmd.Flags().Changed("title") {
		return nil
	}
	title := strings.TrimSpace(o.title)
	if p := task.TitleProblem(title); p != "" {
		return userErr("--title: %s", p)
	}
	if o.title = safetext.Field(title); strings.TrimSpace(o.title) == "" {
		return userErr("--title: %s", "is empty (or nothing but whitespace and invisible characters)")
	}
	return nil
}

// planTaskFlag resolves fugaro run --plan-task into the id the task stores,
// in o.planTask: the flag trimmed, checked against task's ONE list of what a
// plan task id may be (task.PlanTaskProblem, the same list the runner applies
// when it reads a task back, so an id this accepts is never one the run
// refuses at bootstrap).
//
// The flag's absence is not a claim about any plan, and leaves o.planTask
// empty — every launch before the flag existed, and every launch that does
// not use it. A present but empty value (--plan-task "") is refused: it is
// indistinguishable from no flag at all once stored, and the reading it would
// get silently — "this run was not asked to join anything" — is the one
// reading nobody meant.
//
// The value is NOT sanitised the way a title is: a plan task id is a shape,
// not prose, and the shape check already allows only ASCII letters, digits,
// '.', '_', '-' and the '/' between them (safetext.Field, which drops bidi
// overrides and invisible characters and re-spaces text, cannot change such a
// value — and a value it WOULD change is one PlanTaskProblem refuses).
func planTaskFlag(cmd *cobra.Command, o *runOptions) error {
	if !cmd.Flags().Changed("plan-task") {
		return nil
	}
	o.planTask = strings.TrimSpace(o.planTask)
	if p := task.PlanTaskProblem(o.planTask); p != "" {
		return userErr("--plan-task: %s", p)
	}
	return nil
}

// rigorFlags is the run's own rigor request, validated once for every launch
// shape that accepts it (a first run, a best-of-N batch, a review, a
// follow-up): the mode against the closed vocabulary (rigor.Modes, D20), and
// the declared paths against the two bounds a task spec carries
// (task.MaxDeclaredPaths, task.MaxDeclaredPathBytes).
//
// It runs before openCloud and before any write, so a near miss — "Poc", a
// trailing space, "sizzle" — is the launcher's own mistake, refused with the
// vocabulary in the message, rather than a value the runner reads as an
// unknown mode and quietly ignores, which is the fail-open direction. The
// same rules are applied again by task.Spec.Validate to whatever a task
// carries, so a hand-written task.json cannot smuggle one past a launcher.
func rigorFlags(o *runOptions) error {
	if o.rigor != "" && !rigor.Known(rigor.Mode(o.rigor)) {
		return userErr("--mode %q: use %s, or leave it off to resolve the mode from the run's own configuration", o.rigor, rigor.ModeList())
	}
	if len(o.paths) > task.MaxDeclaredPaths {
		return userErr("--path: at most %d paths may be declared, got %d", task.MaxDeclaredPaths, len(o.paths))
	}
	for i, p := range o.paths {
		switch {
		case strings.TrimSpace(p) == "":
			return userErr("--path: path %d is blank; a declared path is a repository-relative path such as internal/payments/x.go", i+1)
		case len(p) > task.MaxDeclaredPathBytes:
			return userErr("--path: path %d is longer than %d bytes", i+1, task.MaxDeclaredPathBytes)
		}
	}
	return nil
}

// tierFlag is the expectation tier the run's own launcher asks for, validated
// once for every launch shape that accepts it (a first run, a best-of-N batch,
// a review, a follow-up): the tier against the closed vocabulary (tier.Tiers,
// spec §3).
//
// Like rigorFlags it runs before openCloud and before any write, so a near
// miss — "Trivial", a trailing space, "huge" — is the launcher's own mistake,
// refused with the vocabulary in the message, rather than a value the runner
// reads as an unknown tier. The same rule is applied again by
// task.Spec.Validate to whatever a task carries, so a hand-written task.json
// cannot smuggle one past a launcher.
func tierFlag(o *runOptions) error {
	if o.tier != "" && !tier.Known(tier.Tier(o.tier)) {
		return userErr("--tier %q: use %s, or leave it off to dispatch the run with no expectation", o.tier, tier.List())
	}
	return nil
}

// noteRigorRequest says, once per launch, when --mode asks for a mode the
// layers already meet: the request may raise and never lower (D6), so such a
// request is recorded as asked and changes nothing, and a launcher that typed
// it — `--mode hotfix` above all, which ranks below every on-axis mode —
// would otherwise believe it took effect. It reads the checkout's fugaro.yaml
// with the project layer, the closest thing to the base branch the launcher
// has; without a checkout of repo it cannot tell, and says nothing. Never an
// error: the runner, reading the base branch, is the one that decides.
func noteRigorRequest(ctx context.Context, o *runOptions, repo string, warn io.Writer) {
	if o.rigor == "" {
		return
	}
	c := checkoutConfig(ctx, repo)
	if c == nil {
		return
	}
	cfg := c.RigorConfig()
	if rigor.Resolve(cfg, rigor.Request{Mode: rigor.Mode(o.rigor), Paths: o.paths}).From == rigor.FromRequest {
		return
	}
	resolved := rigor.Resolve(cfg, rigor.Request{Paths: o.paths}).Mode
	fmt.Fprintf(warn, "--mode %s is not higher than the repository's resolved mode %s: it has no effect\n", o.rigor, resolved)
}

// attemptPlan is everything a new run's attempts share, resolved once: the
// repository, workflow, ref, account and recipe. newSpec (a single launch)
// and runAttempts (every attempt of a best-of-N batch) both build one, then
// stamp a fresh run id onto it per spec (buildAttemptSpec): a project recipe
// published mid-batch must not give a batch's later attempts a different
// recipe from its first (design generic-tool review, 2026-10-13).
type attemptPlan struct {
	repo, slug, workflow, ref, me, account, totalOverride string
	// base is the task's base_branch field, the branch the runner resolves the
	// configuration and the pull request's base from: the layers the launcher
	// read when one of them (and not the checkout's own file) named it, and ""
	// otherwise, so the spec stays byte-identical to the previous release's for
	// a launch that changes nothing (specBaseBranch).
	base   string
	recipe *task.Recipe
	// mode is the resolved recipe's mode when the runner cannot read it
	// from the task alone (mode: artifact today): buildAttemptSpec writes it
	// into the spec, and it is empty for implement and review, so those
	// specs stay byte-identical to what the previous release wrote.
	mode string
	// digestRuns is the manifest of a report run (run_digest_manifest.go):
	// this repository's runs its digest step may read, listed once here so
	// every attempt of a batch carries the same list, and empty for every
	// other launch, whose task stores no manifest at all.
	digestRuns []string
}

// resolveAttemptPlan resolves everything a new run's spec needs except its
// run id, from the flags, the local config and the checkout in the working
// directory. forAttempts is true only for runAttempts: chooseRecipe then
// gives a batch-appropriate refusal for a mode: review recipe, since
// --attempts cannot take the --pr a plain launch's refusal suggests.
func resolveAttemptPlan(ctx context.Context, env *cloudEnv, o *runOptions, total time.Duration, warn io.Writer, forAttempts bool) (*attemptPlan, error) {
	repo := o.repo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return nil, userErr("--repo: %v", err)
	}
	if err := refuseSharedCheckout(ctx, repo); err != nil {
		return nil, err
	}
	checkout := checkoutOf(ctx, repo)
	slug, err := env.repoSlug(repo, checkout)
	if err != nil {
		return nil, err
	}
	workflow, err := resolveWorkflow(env, repo, o.workflow, checkout)
	if err != nil {
		return nil, err
	}
	var totalOverride string
	if total > 0 {
		if c := checkout(); c != nil {
			if w, ok := c.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
		totalOverride = total.String()
	}
	// The base branch is resolved before the ref. An explicit ref is the
	// branch the run is cut from, not its base, and the file that decides a
	// base under one is the file at that ref — which only the runner reads,
	// and it is read first there (ResolveConfig takes the task's base only
	// where the file writes none). So a --ref launch carries the two layers
	// below the file, and a launch without one carries all four.
	ref, base := o.ref, ""
	if ref == "" {
		b, source := launchBaseBranch(ctx, env, repo)
		base, ref = resolvedBaseBranch(b, source), newRunRef(ctx, repo, b, warn)
	} else {
		base = specBaseBranch(env.layersBaseBranch(ctx, repo))
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	// Resolved before chooseRecipe (review finding E): chooseRecipe's
	// resolveRecipe can read a project or catalog recipe from the bucket, a
	// network call a disallowed --token should never trigger — the account
	// is checked against config.AllowedClaudeAccounts first, using only the
	// local config and the checkout already in hand, no network of its own.
	account, err := claudeAccountFor(ctx, env, repo, o.token, ref, checkout())
	if err != nil {
		return nil, err
	}
	root := checkoutRoot(ctx, repo)
	rcp, mode, err := chooseRecipe(ctx, env, o.recipe, checkout, root, warn, forAttempts)
	if err != nil {
		return nil, err
	}
	// The report run's manifest is listed here, once per launch, and before
	// the launch itself: the listing and the horizon it needs are cloud
	// calls of their own, but they come before the task is stored and the
	// execution started, so a repository already writing its reports is
	// refused without launching anything. A batch never reaches this with a
	// report recipe (chooseRecipe refuses --attempts for one), so no
	// attempt reads another's stored task as a report run still going.
	digestRuns, err := digestManifest(ctx, env, slug, root, rcp, time.Now(), warn)
	if err != nil {
		return nil, err
	}
	return &attemptPlan{repo: repo, slug: slug, workflow: workflow, ref: ref, base: base, me: me, account: account, totalOverride: totalOverride, recipe: rcp, mode: mode, digestRuns: digestRuns}, nil
}

// buildAttemptSpec stamps a fresh run id (or o.runID, when given) onto plan,
// building one spec; runAttempts calls it once per attempt of a batch, each
// getting its own id over the same plan.
func buildAttemptSpec(plan *attemptPlan, o *runOptions, text string) (string, *task.Spec, error) {
	runID := o.runID
	if runID == "" {
		var err error
		if runID, err = runIDGenerator(); err != nil {
			return "", nil, err
		}
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: plan.repo, Ref: plan.ref, BaseBranch: plan.base, Workflow: plan.workflow, Task: text, Title: o.title,
		PlanTask: o.planTask, RequestedBy: plan.me, Batch: o.batch, Recipe: plan.recipe, Mode: plan.mode,
		Rigor: rigor.Mode(o.rigor), Paths: o.paths, Tier: tier.Tier(o.tier), DigestRuns: plan.digestRuns}
	if plan.totalOverride != "" {
		spec.Overrides.TotalTimeout = plan.totalOverride
	}
	if plan.account != config.DefaultClaudeAccount {
		spec.OAuthToken = plan.account
	}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return plan.slug, spec, nil
}

// runIDGenerator is task.NewRunID's call in production; a test swaps it to
// force a run id collision inside a best-of-N batch (buildUniqueAttemptSpec
// retries on one).
var runIDGenerator = func() (string, error) { return task.NewRunID(time.Now(), crand.Reader) }

// newSpec builds a new run's spec, and its repository's slug, from the
// flags, the local config and the checkout in the working directory.
func newSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string, total time.Duration, warn io.Writer) (string, *task.Spec, error) {
	plan, err := resolveAttemptPlan(ctx, env, o, total, warn, false)
	if err != nil {
		return "", nil, err
	}
	warnTaskCutsBranch(warn, text, plan.ref)
	return buildAttemptSpec(plan, o, text)
}

// batchResult is fugaro run --attempts's --json output: the batch label,
// how many attempts it was meant to launch, and each one's ordinary
// launchResult, in launch order. Incomplete is set only on a partial batch
// (a failure stopped the loop before Attempts were launched), so a reader
// never mistakes len(Runs) < Attempts for success.
type batchResult struct {
	Batch      string         `json:"batch"`
	Attempts   int            `json:"attempts"`
	Incomplete bool           `json:"incomplete,omitempty"`
	Runs       []launchResult `json:"runs"`
}

// launchAttempt is finishLaunch's call in production; a test swaps it to
// fail a chosen attempt of a batch, to exercise runAttempts's partial-
// failure reporting without a live fault in the backend fake.
var launchAttempt = finishLaunch

// resolveBatchPlan is runAttempts's one call to resolveAttemptPlan in
// production; a test swaps it to count calls and so prove it runs once per
// batch, not once per attempt (design generic-tool review, 2026-10-13: a
// recipe published mid-batch must not give a batch's later attempts a
// different recipe from its first).
var resolveBatchPlan = resolveAttemptPlan

// runAttempts launches o.attempts (2 to 4) independent runs of the same
// task, workflow, ref and recipe, as a best-of-N batch (design generic-tool
// G13): the plan (repository, workflow, ref, account and recipe) is
// resolved once and shared by every attempt, each stamped with its own run
// id over it (buildUniqueAttemptSpec), through the ordinary finishLaunch.
// It never partially launches a batch the max_parallel cap or the budget
// cannot hold, and it never counts a run it did not itself start: a failure
// after k launches (including finishLaunch reporting "already-launched",
// which here can only mean a run id collision slipped past the retry)
// reports those k and returns the error.
func runAttempts(cmd *cobra.Command, env *cloudEnv, o *runOptions, text string, total time.Duration) error {
	ctx := cmd.Context()
	warn := cmd.ErrOrStderr()
	n := o.attempts
	// Resolved, including the recipe, before checkMaxParallelFor's backend
	// listing call and the budget pre-check's database read (design
	// generic-tool review, 2026-10-13): a batch a review-mode recipe (or an
	// unresolvable one) refuses outright must not first pay for either.
	plan, err := resolveBatchPlan(ctx, env, o, total, warn, true)
	if err != nil {
		return err
	}
	noteRigorRequest(ctx, o, plan.repo, warn)
	warnTaskCutsBranch(warn, text, plan.ref)
	if err := checkMaxParallelFor(ctx, env, n); err != nil {
		return err
	}
	if err := env.batchBudgetPrecheck(ctx, plan.slug, plan.repo, n, warn); err != nil {
		return err
	}
	seen := make(map[string]bool, n)
	results := make([]launchResult, 0, n)
	for i := 0; i < n; i++ {
		slug, spec, err := buildUniqueAttemptSpec(plan, o, text, seen)
		if err != nil {
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, err)
		}
		if i == 0 && o.batch == "" {
			// With no --batch, the first attempt's own run id names the
			// batch (design generic-tool §3.6): set once buildUniqueAttemptSpec
			// has generated it, then kept in o.batch so every later
			// attempt's spec carries the same label.
			spec.Batch = "bestof-" + spec.RunID
			if err := spec.Validate(); err != nil {
				return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, userErr("%v", err))
			}
			o.batch = spec.Batch
		}
		seen[spec.RunID] = true
		// skipBudget: the batch's own budget pre-check above already leased
		// n * MinLaunchHeadroom once; a per-attempt re-check here would both
		// re-evaluate state it already judged and print its notes n times.
		res, err := launchAttempt(ctx, env, o, slug, spec, false, warn, true)
		if err != nil {
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, err)
		}
		if res.Status == "already-launched" {
			// Not a fresh launch: buildUniqueAttemptSpec's retry already
			// guards against a run id this loop generated twice, so this can
			// only be some other process's run reusing the id — too
			// improbable to recover from; the batch must not silently count
			// a run it did not itself start.
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, fmt.Errorf("run %s was already launched, not by this batch", oneLine(res.Run)))
		}
		if !o.asJSON {
			if err := printLaunch(cmd.OutOrStdout(), res, false); err != nil {
				return err
			}
		}
		results = append(results, res)
	}
	if o.asJSON {
		if err := printBatch(cmd.OutOrStdout(), o.batch, n, false, results); err != nil {
			return err
		}
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("--pr <%c>", 'A'+i)
	}
	fmt.Fprintf(warn, "pick: fugaro run --recipe pick %s once all have opened PRs (fugaro runs ls --batch %s)\n",
		strings.Join(parts, " "), o.batch)
	return nil
}

// buildUniqueAttemptSpec is buildAttemptSpec, retried while its fresh run id
// collides with one already seen in this batch (task.NewRunID's own
// resolution, a timestamp plus two random bytes, makes two attempts in the
// same second collide about 1 time in 65536: rare, but a silent duplicate
// would make the batch report N results for N-1 real runs). o.runID is
// always "" here (--attempts refuses --run-id), so each retry calls
// runIDGenerator again.
func buildUniqueAttemptSpec(plan *attemptPlan, o *runOptions, text string, seen map[string]bool) (string, *task.Spec, error) {
	const maxRetries = 8
	for try := 0; ; try++ {
		slug, spec, err := buildAttemptSpec(plan, o, text)
		if err != nil || !seen[spec.RunID] {
			return slug, spec, err
		}
		if try >= maxRetries {
			return "", nil, userErr("could not generate a run id that was not already in this batch after %d tries", maxRetries+1)
		}
	}
}

// reportBatchFailure prints the runs a batch already launched (never more:
// launched runs are never cancelled behind the user's back) before
// returning the error that stopped it, wrapped with which attempt failed,
// out of how many, and the run ids that did launch. Text mode already
// printed each as it launched; JSON mode holds everything for one document,
// so it prints the partial one here instead, marked Incomplete.
func reportBatchFailure(cmd *cobra.Command, batch string, n int, asJSON bool, results []launchResult, i int, err error) error {
	if asJSON {
		_ = printBatch(cmd.OutOrStdout(), batch, n, true, results)
	}
	ids := make([]string, len(results))
	for j, r := range results {
		ids[j] = r.RunID
	}
	return fmt.Errorf("attempt %d of %d: %w; launched %d (batch %s): %s", i+1, n, err, len(results), batch, strings.Join(ids, ", "))
}

// printBatch is fugaro run --attempts's --json output: one document for the
// whole batch, not one per run.
func printBatch(w io.Writer, batch string, attempts int, incomplete bool, results []launchResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(batchResult{Batch: batch, Attempts: attempts, Incomplete: incomplete, Runs: results})
}

// reviewSpec is the task of a review-mode launch: fugaro run --recipe NAME
// --pr A [--pr B ...], NAME naming a mode: review recipe (rr, already
// resolved and confirmed by the caller). It reviews any open pull request
// of the repository, not only Fugaro's own (design generic-tool §3.6,
// G12): there is no branch, no previous run and no follow-up chain, unlike
// prSpec's. --token is honoured the same way a first run's is (newSpec):
// there is no previous run's account to inherit, so a review-mode launch
// is always resolved from the flag, else agent.oauth_token, else default,
// never silently ignored.
func reviewSpec(ctx context.Context, env *cloudEnv, o *runOptions, rr *resolvedRecipe, text string, total time.Duration, warn io.Writer) (string, *task.Spec, error) {
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
	if err := refuseSharedCheckout(ctx, repo); err != nil {
		return "", nil, err
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
	// The layers, and the field, as a first run's (resolveAttemptPlan) with
	// one case short: a review run reads its own configuration from the branch
	// it is cut from — the runner's readBaseConfig refuses a file whose base
	// is not that branch — so an explicit --ref writes no field. There, the
	// field would name a branch other than the ref, and the run would be
	// refused rather than held to it; the file at that ref is the judge, as it
	// is for a first run.
	ref, base := o.ref, ""
	if ref == "" {
		b, source := launchBaseBranch(ctx, env, repo)
		base, ref = resolvedBaseBranch(b, source), b
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
	account, err := claudeAccountFor(ctx, env, repo, o.token, ref, checkout())
	if err != nil {
		return "", nil, err
	}
	printRecipeNote(warn, rr, true)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, BaseBranch: base, Workflow: workflow, Task: text, Title: o.title, PlanTask: o.planTask,
		RequestedBy: me, Batch: o.batch, Recipe: rr.taskRecipe(), ReviewPRs: append([]int(nil), o.pr...),
		Rigor: rigor.Mode(o.rigor), Paths: o.paths, Tier: tier.Tier(o.tier)}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if account != config.DefaultClaudeAccount {
		spec.OAuthToken = account
	}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return slug, spec, nil
}

// claudeAccountFor is the named Claude account a first run uses: flag
// (fugaro run --token), else agent.oauth_token of the fugaro.yaml at the ref
// the run checks out, else the repository's default
// (config.DefaultClaudeAccount). A non-default name is refused
// (claudeAccountAllowed) unless config.AllowedClaudeAccounts (the local
// config's claude_accounts) lists it for repo, before any launch (design
// docs/design/named-oauth-tokens.md §3 T7) — checked whether or not a checkout
// is available here, so --repo X --token N outside any checkout is held to the
// same rule a checkout's own resolution would be.
//
// Both halves are the run's, and neither is the working tree's. The account is
// read at the ref the run checks out, out of this checkout's git objects
// (agentAtRef/checkoutFugaroYAMLAtRef), because the committed fugaro.yaml is
// the only place that may name an account (docs/design/named-oauth-tokens.md
// T2) — a working tree left on another branch, or carrying an uncommitted
// edit, would otherwise select the account the launch runs as: the job it
// lands on holds that account's token, and that account's allow_repos is what
// decides whether this repository may use it. The auth is the *run's* too, and
// that means the ref's bytes resolved over the project layer (agentAtRef's
// second answer), not the ref's bytes alone: agent.auth is a project-scope,
// layerable key (docs/design/layered-config.md's scopes table), so the run
// resolves it layer-applied (projectJobAgentAuth) — a launcher that read the
// raw file would answer Fugaro's own default (applyDefaults' vertex) for the
// shape projects adopt accounts with, a layer whose defaults say
// `agent.auth: oauth` over a repository that only names an account, and refuse
// a launch the run resolves as oauth and doctor reports as fine.
//
// ref is "" when the launch has no ref yet — the pre-check runRun makes with
// --pr and an explicit --recipe, before the previous run's ref is known (which
// reviewSpec repeats with the ref in hand) — and reading it may also fail for a
// ref this checkout does not hold. Both leave the working tree's own value
// standing, which is what every launch had before this read existed: the run
// itself resolves the ref's fugaro.yaml after cloning it, and a launch onto a
// shared project job whose own credential cannot be resolved is refused
// outright (projectJobAgentAuth) rather than judged on a guess.
func claudeAccountFor(ctx context.Context, env *cloudEnv, repo, flag, ref string, co *config.Config) (string, error) {
	a, refAuth, refAgent := agentAtRef(ctx, env, repo, ref)
	name := flag
	if name == "" {
		switch {
		case refAgent:
			name = a.OAuthToken
		case co != nil:
			name = co.Agent.OAuthToken
		}
	}
	if name == "" {
		name = config.DefaultClaudeAccount
	}
	// The auth a name is judged against, as the run resolves it: the document
	// at the ref over the project layer when that could be read, and the
	// checkout's own, layer-applied resolution otherwise.
	auth := ""
	if co != nil {
		auth = co.Agent.Auth
	}
	if refAgent {
		auth = refAuth
	}
	return name, claudeAccountAllowed(env, repo, name, auth)
}

// agentAtRef is the two agent facts a launch judges a named account with, read
// at ref: the file's own `agent:` block (i) and agent.auth as the *run*
// resolves it (ii), with whether the ref could be read at all.
//
// The document is the checkout's own object database (checkoutFugaroYAMLAtRef:
// git cat-file at refs/remotes/origin/<ref>, else <ref>, never the network and
// never the working tree), and the two facts are read differently because the
// two keys have different scopes. agent.oauth_token is repository-scope — the
// committed fugaro.yaml is the only place that may name an account
// (docs/design/named-oauth-tokens.md T2) and no project layer may name one —
// so the file's own value is the run's, via the strict config.AgentOf. And
// agent.auth is project-scope and layerable (docs/design/layered-config.md's
// scopes table): the run composes it from the ref's bytes and the project
// layer its task carries, through runner.ResolveConfig, exactly as
// projectJobAgentAuth does, and a launcher that answered from the raw file
// would answer Fugaro's default where the layer is what says oauth.
//
// The layer is the one this command already resolved for that checkout
// (cachedProjectLayer: checkoutConfig resolved it to tell the repository's
// provider, and createTask's embedProjectLayer carries that same one into the
// task), never a fresh bucket read — this gate makes no network call of its
// own. A layer this command resolved nowhere leaves the file's own value
// standing, and a document that does not resolve over it leaves it standing
// too: such a document is one the run itself refuses at bootstrap, and for a
// shared job the launch gate re-resolves the run's own credential
// (projectJobAgentAuth) and refuses one that cannot be resolved.
//
// Nothing is fetched to make the read, so a ref this checkout does not hold, a
// repository with no checkout here and a file that does not decode all answer
// "no" rather than an error: the caller falls back to the working tree, which
// is the resolution every launch had before this read existed.
func agentAtRef(ctx context.Context, env *cloudEnv, repo, ref string) (config.Agent, string, bool) {
	if ref == "" {
		return config.Agent{}, "", false
	}
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return config.Agent{}, "", false
	}
	data, err := readFugaroYAMLAtRef(ctx, root, ref)
	if err != nil {
		return config.Agent{}, "", false
	}
	a, ok := config.AgentOf(data)
	if !ok {
		return config.Agent{}, "", false
	}
	auth := a.Auth
	fl, cached := cachedProjectLayer(ctx, root, env.lc)
	if cached && fl.Layer != nil {
		pl := &task.ProjectLayer{SHA256: fl.Layer.SHA256, Generation: fl.Generation, YAML: string(fl.Layer.Raw)}
		// No base branch: the task is not built yet at this point (this is the
		// read the launch resolves its account from), and only agent.auth is
		// read out of the resolution — a base changes git.base_branch and
		// nothing else.
		if res := runner.ResolveConfig(data, pl, env.lc.Name, ""); res.LayerErr == nil && len(res.Problems) == 0 {
			auth = res.Cfg.Agent.Auth
		}
	}
	return a, auth, true
}

// cascadeRefConfig is the document a cascade launch's own rules are read from
// here — its agent.auth and its budget mode both: the fugaro.yaml at the ref
// the run checks out (spec.Ref), read out of this checkout's object database,
// resolved over the project layer the task carries (spec.ProjectLayer) through
// runner.ResolveConfig, the runner's own function, exactly as
// projectJobAgentAuth resolves the credential.
//
// It is deliberately not the launcher's working tree (checkoutConfig). The run
// clones spec.Ref and resolves *that* file (runner.go's blobless checkout of
// origin/spec.Ref), so a launcher judging its working tree judges a document
// the run never reads: a checkout left on another branch, or carrying an
// uncommitted edit, can say budget.mode: observe over a ref that says off, and
// the launch this gate exists to refuse — one whose run cannot carry a single
// call — would take the claim, mint a budget token and start an execution that
// refuses in agent.BuildEnv instead. The reverse refuses a launch the run would
// carry. agentAtRef reads the ref for this very reason; the mode has to come
// from the same bytes as the auth, or the gate judges a run that does not
// exist.
//
// nil when the ref cannot be read or does not resolve: the caller falls back to
// the checkout's own document, as the auth read always has, and the run's own
// bootstrap refuses what the launcher could not read.
func cascadeRefConfig(ctx context.Context, env *cloudEnv, spec *task.Spec) *config.Config {
	if spec.Ref == "" {
		return nil
	}
	root := checkoutRoot(ctx, spec.Repo)
	if root == "" {
		return nil
	}
	data, err := readFugaroYAMLAtRef(ctx, root, spec.Ref)
	if err != nil {
		return nil
	}
	out := runner.ResolveConfig(data, spec.ProjectLayer, env.lc.Name, spec.BaseBranch)
	if out.LayerErr != nil || len(out.Problems) > 0 {
		return nil
	}
	return out.Cfg
}

// defaultBranchPolicyLayer is the default branch's policy as the run reads it:
// the policy keys of origin/<default branch>'s fugaro.yaml (runner's
// resolvePolicy, config.PolicyOf). The run merges three layers — the ceiling,
// this one and the ref's file — and a layer can only tighten, so leaving this
// one out is not neutral: a mode this layer raises (off < observe < enforce) is
// one the run has and the gate would not see, and a launch the run would carry
// would be refused. The security direction is safe the other way (no layer
// below the ceiling can lower a mode), so a default branch this launcher cannot
// read is an empty layer rather than a refusal: it has no fetch of its own — a
// fetch is a network write into a repository this command does not own — and
// the run's own bootstrap refuses the file it cannot read.
func defaultBranchPolicyLayer(ctx context.Context, repo string) policy.Layer {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return policy.Layer{}
	}
	db := defaultBranchParse(ctx, root)
	if !db.read {
		return policy.Layer{}
	}
	p, err := config.PolicyOf(db.data)
	if err != nil {
		return policy.Layer{}
	}
	return policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, PerDayUSD: p.PerDayUSD, MaxRunTokens: p.MaxRunTokens,
		AllowedModels: p.AllowedModels, MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer}
}

// checkoutAgentAuth is a checkout's own agent.auth as claudeAccountAllowed
// judges a name with it, "" when there is no checkout to read one from.
func checkoutAgentAuth(co *config.Config) string {
	if co == nil {
		return ""
	}
	return co.Agent.Auth
}

// claudeAccountAllowed refuses name (already resolved: flag, agent.oauth_token
// or an inherited follow-up value) unless it is the repository's default
// account. Checked before any network call, build-record read or task.json
// write (design §3 T7), and regardless of whether a local checkout exists:
// fugaro run --repo X --token N run from outside any checkout, and a
// follow-up's inherited name (bucket data a launcher can write directly),
// must both be checked exactly as a fresh resolution from a checkout would
// be — review finding: skipping the check just because there is no checkout
// here let either one through with its shape checked but never its
// membership in config.AllowedClaudeAccounts. When the auth a run resolves
// is known and is not oauth, naming an account makes no sense at all (no
// secret is ever mounted for it, so the name would be silently unused) and is
// refused outright, not merely left unchecked.
//
// auth is the agent.auth of the document the name came from (claudeAccountFor
// passes the ref's when it could read it, the checkout's own otherwise), and
// "" when the caller has no document at all: a name resolved with no checkout
// known is then only checked for membership, which is the rule the two
// pre-check callers (a follow-up's inherited account, --retry's stored task)
// depend on.
func claudeAccountAllowed(env *cloudEnv, repo, name, auth string) error {
	if name == config.DefaultClaudeAccount {
		return nil
	}
	// The two documents auth can come from are read differently, and only one
	// of them is validated. The checkout's own config went through
	// config.Validate (agent.auth is one of config.AgentAuths), while the
	// ref's bytes are read by config.AgentOf, which decodes strictly and
	// validates nothing — so an arbitrary string arrives here from a committed
	// fugaro.yaml. A value this Fugaro cannot place is refused, which is what
	// the run does with that same file at bootstrap (its own config.Validate)
	// and so only says so earlier, and it is printed through
	// pluginwire.Printable: it is a repository's own text, a newline in it
	// would add a line to a refusal a person reads (safetext.MultiLine keeps
	// newlines, by design), and an escape sequence would rewrite that line.
	// The message names what auth may be, because the file's author is the one
	// who can fix it.
	if auth != "" && !config.KnownAgentAuth(auth) {
		return userErr("named Claude account %s cannot be judged against %s's agent.auth, which is %s — not a value this Fugaro knows (agent.auth is one of %s)",
			pluginwire.Printable(name), repo, pluginwire.Printable(auth), strings.Join(config.AgentAuths, ", "))
	}
	if auth != "" && auth != "oauth" {
		return userErr("named Claude account %s only applies to a workflow whose agent.auth is oauth; %s's agent.auth is %s",
			pluginwire.Printable(name), repo, auth)
	}
	allowed := config.AllowedClaudeAccounts(env.lc.ClaudeAccounts, repo)
	if !slices.Contains(allowed, name) {
		return userErr("named Claude account %s is not one %s may use (allowed: %s); ask the operator to add it to claude_accounts in their local config",
			pluginwire.Printable(name), repo, strings.Join(allowed, ", "))
	}
	return nil
}

// chooseRecipe is the recipe of a new first run: --recipe, else the
// checkout's agent.recipe, else default, resolved over the checkout at root,
// the project and the catalog. With no flag and no checkout it resolves
// default (a project default included), and a catalog default leaves the
// choice to the runner, which reads agent.recipe at the task's ref.
// forAttempts gives a mode: review refusal that fits runAttempts's caller
// (--attempts refuses --pr, so the plain launch's "add --pr" advice would be
// wrong there).
func chooseRecipe(ctx context.Context, env *cloudEnv, flag string, checkout func() *config.Config, root string, warn io.Writer, forAttempts bool) (*task.Recipe, string, error) {
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
		return nil, "", err
	}
	if rr.Recipe.Mode == recipe.ModeReview {
		if forAttempts {
			return nil, "", userErr("recipe %s is mode: review, which only reviews pull requests; --attempts launches a fresh batch and cannot use it (review one with fugaro run --recipe %s --pr N instead)", rr.Name, rr.Name)
		}
		return nil, "", userErr("recipe %s is mode: review, which only reviews pull requests; launch it with --recipe %s --pr N (1 to 4 times)", rr.Name, rr.Name)
	}
	// A mode: artifact launch carries its mode in the task, and the runner
	// selects the run's shape from that field before it resolves the recipe
	// itself. So the launcher may only write it when it is sure of the
	// recipe the run will get: resolveRecipe has paths where what it
	// returned is NOT that recipe (a project layer this launcher may not
	// read, a stale cached project recipe standing in for an unreachable
	// bucket), and on those the mode would stay empty for a recipe that is
	// mode: artifact, the spec would be indistinguishable from an implement
	// one, and the image gate below would not fire: a silent pull request
	// where the owner asked for a file. The conservative direction is the
	// right one; the owner reruns when the layer is reachable.
	if rr.Recipe.Mode == recipe.ModeArtifact {
		if rr.Note != "" {
			return nil, "", userErr("recipe %s is mode: artifact, but %s; the launch is refused rather than run without the recipe's mode in its task (retry when the layer is reachable)", rr.Name, rr.Note)
		}
		if forAttempts {
			return nil, "", userErr("recipe %s is mode: artifact, which renders a deliverable into the run's bucket and opens no pull request; --attempts launches a fresh batch of pull requests and cannot use it", rr.Name)
		}
	}
	// A report recipe reads this repository's own run records and writes the
	// reports the repository is missing (task-governance plan T3.6), and the
	// repository has one report run at a time: a batch's attempts are
	// launched together, each carrying the same manifest, so they would all
	// write the same reports into their own pull requests. The batch is
	// refused here, where the recipe is known, rather than by the
	// in-flight check in resolveAttemptPlan, which a batch's own attempts
	// cannot see (they are stored only as they launch).
	if forAttempts && recipe.UsesDigest(rr.Recipe) {
		return nil, "", userErr("recipe %s writes this repository's run reports, and a repository has one report run at a time; --attempts launches a batch of runs at once and cannot use it", rr.Name)
	}
	printRecipeNote(warn, rr, explicit)
	// An explicit --recipe default overrides agent.recipe, which applies when
	// the checkout sets it, or when there is no checkout and the runner reads
	// it at the ref: then the default is carried in the task.
	override := flag == recipe.DefaultName && (cfg == nil || cfg.Agent.Recipe != "")
	mode := ""
	if rr.Recipe.Mode == recipe.ModeArtifact {
		mode = recipe.ModeArtifact
	}
	return rr.taskRecipeEmbedding(override), mode, nil
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

// cascadeLaunchRefusal is the launcher's own side of the cascade's rules
// (C1.19): a `agent.auth: cascade` workflow is refused here, before the claim
// is taken, before a budget token is minted and before any byte is written —
// so a repository that asks for a cascade no installation can carry leaves no
// task, no execution and no token behind.
//
// The runner refuses the rest of the same rules in the job (the missing
// service, serviceUpstream). What is refused here is refused here because a
// person is watching a terminal:
//
//   - an installation with no database, first of all: the switch the operator
//     publishes and the credentials rules marker both live there, so this CLI
//     can see neither and refuses rather than launching a run whose first
//     recorded credential the deployed rules may refuse. The model credential
//     is no longer mounted in such a job (C1.20), so there is nothing to fall
//     back to either (D143) — and without a database there is no policy to
//     read a mode from, so the gateway rule below could only guess.
//   - the O2 switch: the project's published policy must carry
//     routing.claude: {via: cascade}, or no run of this workflow is admitted
//     anywhere. The policy read below is the one this needs, and the refusal
//     is fugaro validate's cascadeProblems said again on this side — a
//     repository asks for the cascade, and only the operator can set it there
//     — except that here it is judged before the claim is taken, before a
//     budget token is minted and before an execution starts, rather than
//     leaving the runner's bootstrap to refuse a run already paid for.
//   - the gateway rule (config.CheckCascadeGateway): a cascade run has no
//     credential of its own, so a budget whose effective mode is off leaves it
//     with nothing to carry its calls. config.CheckCascadeGateway is the one
//     place that rule lives, and its own comment named this task as the
//     launch's caller of it.
//   - the project's published model allow-list (the owner's ruling of
//     2026-10-22): the credentials service holds every call it carries to the
//     project's budget.allowed_models and refuses a project that publishes
//     none (internal/credsvc's requestNoAllowList), so a cascade run of such a
//     project would have every call refused and halt on the first (D143). What
//     is judged is the *policy node's* list and not the run's merged one: the
//     node is the only list the service can read, and the environment's
//     ceiling and the repository's file never reach it. The rule has no skip
//     for a project layer this launcher cannot read — an absent node is
//     refused by the switch's arm above — and this read is the one all three
//     surfaces the cascade is judged on make: `fugaro validate` reports it to
//     the author (validate.go's cascadeProblems) and the run refuses it in
//     bootstrap, before any model call (internal/runner/routing.go's
//     applyRouting).
//   - the rules marker (budget/rules, C1.3): the deployed document must carry
//     the credentials family, or the run's first write of the credential's
//     name is denied and it halts named (HaltCredsRulesDenied, C1.17). This is
//     the same read `fugaro update --provision-credentials` makes before it
//     applies anything and the same pair `fugaro doctor` reports; here it is a
//     refusal, because a launch is the moment it can still save the run. The
//     marker's generation is wanted to be exactly this binary's, and the two
//     ways it can miss it are two different problems with two different fixes
//     (doctorCredsRules' pair): below this Fugaro's it is an old deploy, above
//     it a newer Fugaro's document — and a newer marker is not one this
//     binary may deploy over (internal/infra's DB.Plan refuses it), so telling
//     its owner to re-run init from this Fugaro would send them at a command
//     that refuses them.
//
// Both documents this gate judges are the run's: the auth *and* the mode come
// from the file the run checks out (spec.Ref, read out of this checkout's
// object database by cascadeRefConfig), never from the launcher's working tree,
// which the run does not read — projectJobAgentAuth's finding, and the reason
// the auth was read at the ref here from the start. The mode is judged over the
// same three layers the run merges (runner's resolvePolicy): the environment's
// ceiling, the default branch's policy (defaultBranchPolicyLayer) and the ref's
// own file.
func cascadeLaunchRefusal(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	runCfg := cascadeRefConfig(ctx, env, spec)
	if runCfg == nil {
		// The ref could not be read or does not resolve: the checkout's own
		// document stands in, as the auth read has always fallen back to it,
		// and the run's own bootstrap refuses what the launcher could not
		// read.
		runCfg = checkoutConfig(ctx, spec.Repo)
	}
	auth := checkoutAgentAuth(runCfg)
	if auth != config.AuthCascade {
		return nil
	}
	if env.lc.Budget == nil || env.lc.Budget.RTDBURL == "" {
		return userErr("agent.auth: cascade cannot run here: this installation has no database, so neither the policy switch that turns the cascade on (%s) nor the credentials rules marker (%s) can be read — and a job of such an installation mounts no model credential at all (C1.20), so this run would have nothing to fall back to (D143). Run 'fugaro update --provision-credentials' in your own terminal to enable the credentials service, or set agent.auth to api-key or oauth in this workflow",
			projectpolicy.PathShared(), rules.PathCredsRulesMarker())
	}
	ceiling := ceilingLayer(env.lc)
	def := defaultBranchPolicyLayer(ctx, spec.Repo)
	var layer policy.Layer
	if runCfg != nil {
		layer = runner.FileLayer(runCfg)
	}
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil {
		return policyReadErr(err)
	}
	// O2, on the launch's own side (validate.go's cascadeProblems is the
	// author's): the policy read above is the one the switch is judged from,
	// and it fails closed in every direction — nothing published, a policy
	// with no routing block, or a Claude switch written with another via
	// (`direct` names the direct path and `auto` keeps today's routing
	// meaning, O3) are all "not turned on".
	switch {
	case !found || p == nil:
		return userErr("agent.auth: cascade cannot run here: this project has published no policy at %s, so nothing turns the cascade on. Set routing.claude: {via: cascade} in the project policy and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)",
			projectpolicy.PathShared())
	case p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.ViaValue() != config.RouteCascade:
		return userErr("agent.auth: cascade cannot run here: this project's published policy (%s) does not turn the cascade on. Set routing.claude: {via: cascade} in the project policy and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)",
			projectpolicy.PathShared())
	}
	if p.Budget.Mode != "" {
		ceiling.Mode = p.Budget.Mode
	}
	if ps := config.CheckCascadeGateway(config.Agent{Auth: auth}, policy.Merge(ceiling, def, layer).Mode); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return userErr("agent.auth: cascade cannot run here: %s. A cascade run holds no model credential of its own — the gateway carries every call to the credentials service and accounts for it (D143) — so a budget that leaves the gateway off cannot run one", strings.Join(msgs, "; "))
	}
	// The allow-list the credentials service holds this run's calls to, read
	// from the policy node and refused fail closed: there is no fall-back to
	// the environment's ceiling or to the repository's file, because the
	// service never sees either and refuses every call of a project that
	// publishes none (the owner's ruling of 2026-10-22).
	if len(p.Budget.AllowedModels) == 0 {
		return userErr("agent.auth: cascade cannot run here: this project's published policy (%s) carries no budget.allowed_models, and the credentials service — which carries every call of a cascade run and accounts for it (D143) — refuses a project that publishes none, so this run's first call would be refused and halt it. agent.auth: cascade needs the project's budget.allowed_models; add the allow-list to the project layer and publish it (fugaro project policy publish)",
			projectpolicy.PathShared())
	}
	db, err := newBudgetClient(ctx, env.lc.Budget.RTDBURL, env.lc.Endpoints.NoAuth)
	if err != nil {
		return userErr("%v", err)
	}
	var raw json.RawMessage
	marker := rules.PathCredsRulesMarker()
	foundMarker, err := db.Get(ctx, marker, &raw)
	if err != nil {
		return dbErr(env.lc, fmt.Errorf("reading the credentials rules marker at %s: %w", marker, err), false)
	}
	if !foundMarker {
		return userErr("agent.auth: cascade cannot run here: the installation's database carries no credentials rules marker at %s, so its deployed rules predate the credentials service and the first credential this run records would be refused — the run would halt instead of running (C1.17). Re-run 'fugaro update --firebase-project <gcp-project-id>' in your own terminal from a Fugaro that carries the credentials rules (%s), then launch again",
			marker, credsServiceSince)
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil || projectpolicy.MarkerHandEdited(gen) {
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s is %s, which is not a generation number; fix it with 'fugaro update --firebase-project <gcp-project-id>'",
			marker, oneLineCLI(string(raw)))
	}
	switch {
	case gen < rules.CredsRulesVersion:
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s records generation %d, below the generation %d this Fugaro's credentials rules carry, so the deployed document predates the credentials family and the first credential this run records would be refused (C1.17); re-run 'fugaro update --firebase-project <gcp-project-id>' in your own terminal from this Fugaro (%s), then launch again",
			marker, gen, rules.CredsRulesVersion, credsServiceSince)
	case gen > rules.CredsRulesVersion:
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s records generation %d, above the generation %d this Fugaro's credentials rules carry, so a newer Fugaro deployed them and this release refuses to deploy its own rules over a newer marker; upgrade Fugaro first (brew upgrade dimipaun/tap/fugaro) and re-run 'fugaro update --firebase-project <gcp-project-id>' with the newer binary, then launch again",
			marker, gen, rules.CredsRulesVersion)
	}
	return nil
}

// checkArtifactLaunch refuses a mode: artifact launch whose caps cannot fit
// the workflow's memory, or whose render or probe step names a command the
// workflow leaves empty: the launch's arm of runner.RecipeGateProblems
// (design artifact-runs.md §5.2, Review Focus 12), called before createTask
// writes anything, so an over-memory artifact launch leaves no task and no
// execution behind. A launch whose recipe or workflow the launcher cannot
// resolve locally is judged by the runner's own bootstrap instead
// (checkGateCommands), which reports the same list.
//
// It is also the arm of the artifact boundary that survives the enable (Global
// Constraint 13, plan compute-backend.md Task 8 step 4): an artifact-producing
// run is never launched on cloud-batch, and this is where the backend and the
// recipe the launch names are both known -- `fugaro validate` judges the
// checkout's own recipe offline, so a launch naming an artifact recipe with
// --recipe is refused here rather than there. The two arms ask one function
// (artifactOnBatchRefusal), so the boundary cannot be lifted from one path
// while the other keeps it.
//
// What it judges is the recipe as recipeForLaunch resolves it, and that is not
// always the recipe the runner reads, because the runner resolves the
// repository layer at the task's ref FIRST (internal/runner/recipe.go's
// resolveRecipe, TestRecipeRepoLayerWins):
//
//   - a project or catalog recipe's text travels in the task, but a file of
//     that name at the ref is the recipe that runs, mode included, whatever
//     text the task carries. So the ref is asked for that name as the runner
//     asks it (carriedRecipeAtRef, recipeAtRef), and a ref this checkout has
//     none of is refused fail-closed (artifactShadowedOnBatchRefusal) -- the
//     text a task carries says nothing about what the base branch holds beside
//     it. TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch
//     pins both halves.
//   - a repository recipe (named, or the one agent.recipe names) is read from
//     this launcher's working tree, while the runner reads it at the task's
//     ref -- the repository's base branch unless the launch names another. A
//     working tree whose recipe is not mode: artifact therefore passes this
//     gate for a ref whose recipe is. That residual is recorded in the plan's
//     Task 10 step 5 inventory, which closes it before the name is selectable,
//     and TestTheArtifactLaunchGateJudgesTheWorkingTreeNotTheRef pins it.
//
// It has three shapes of its own, because a launch can arrive without a recipe
// this launcher can read at all: the refusal for a recipe known to be mode:
// artifact, and -- on cloud-batch only -- the refusal for a recipe that could
// not be resolved here (artifactUnreadOnBatchRefusal) and for a carried recipe
// whose repository layer at the ref could not be read
// (artifactShadowedOnBatchRefusal), since nothing on this path can then say the
// run is not an artifact one. All are asked before anything about this launch
// is written, so none leaves a task or an execution behind.
func checkArtifactLaunch(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	cfg := checkoutConfig(ctx, spec.Repo)
	// The boundary is asked here, before the checkout can bail out below and
	// with the recipe resolved as far as this checkout allows. Both of those
	// are the arm itself and not an ordering detail: recipeForLaunch needs no
	// checkout for a task that carries its own recipe text (a project or
	// catalog recipe -- `fugaro run --recipe`), so asking the boundary only
	// after the checkout had resolved left exactly that launch unrefused on
	// cloud-batch, from any directory whose origin is not spec.Repo. The
	// workflow is not consulted either: SelectWorkflow reads this checkout's
	// config, which a launch whose local configuration disagrees with its
	// repository would fail -- and the boundary is about the backend and the
	// recipe, not about the workflow.
	rcp := recipeForLaunch(ctx, cfg, spec)
	if msg, ok := artifactOnBatchRefusal(launchBackend(env.be), rcp); ok {
		// Before the caps below and before anything is written, like every
		// other refusal here: the run the boundary holds back leaves no task
		// and no execution behind.
		return userErr("%s", msg)
	}
	if launchBackend(env.be) == backend.CloudBatch && rcp == nil {
		// Fail closed, and this is the other shape of the same hole: a recipe
		// this launcher cannot read (a repository recipe with no checkout of
		// that repository here, or a first run whose recipe is the one this
		// checkout's agent.recipe names) reaches the boundary as nil, which
		// artifactOnBatchRefusal reads as "not an artifact run" -- and the
		// runner resolves that recipe at the task's ref, so on
		// cloud-batch an unread recipe is refused rather than passed on.
		return userErr("%s", artifactUnreadOnBatchRefusal(spec.Repo))
	}
	if launchBackend(env.be) == backend.CloudBatch && spec.Recipe != nil && spec.Recipe.YAML != "" {
		// A recipe whose text the task carries is not the run's recipe either,
		// and for the same reason: the runner reads the repository layer at the
		// task's ref FIRST and only falls back to the task's copy
		// (internal/runner/recipe.go's resolveRecipe, TestRecipeRepoLayerWins),
		// so a file of that name at the ref is the recipe that runs, its mode
		// included. So the ref is asked for that name, as the runner asks it:
		// the file it finds (if any) is judged on its own mode, and a ref this
		// checkout has none of is refused fail-closed -- a project or catalog
		// recipe's text says nothing about what the repository's base branch
		// holds beside it.
		refRcp, err := carriedRecipeAtRef(ctx, spec)
		if err != nil {
			return userErr("%s", artifactShadowedOnBatchRefusal(spec.Recipe.Name, spec.Repo, spec.Ref))
		}
		if msg, ok := artifactOnBatchRefusal(backend.CloudBatch, refRcp); ok {
			return userErr("%s", msg)
		}
	}
	if cfg == nil {
		return nil
	}
	if rcp == nil || rcp.Mode != recipe.ModeArtifact {
		return nil
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return nil
	}
	var msgs []string
	for _, p := range runner.RecipeGateProblems(rcp, wf) {
		msgs = append(msgs, fmt.Sprintf("workflows.%s.%s: the %s", name, p.Field, p.Message))
	}
	if len(msgs) == 0 {
		return nil
	}
	return userErr("recipe %s: %s", rcp.Name, strings.Join(msgs, "; "))
}

// carriedRecipeAtRef is the recipe the runner will use for a launch whose task
// carries its own recipe text: the repository layer at the task's ref when the
// ref has a file of that name -- which resolveRecipe reads FIRST and which wins
// over the task's copy, mode included (internal/runner/recipe.go,
// TestRecipeRepoLayerWins) -- else the text the task carries.
//
// err is non-nil whenever the ref's answer cannot be had at all: no checkout of
// the run's repository in this directory, a checkout that does not hold
// spec.Ref, or a file at the ref this launcher cannot parse. The caller refuses
// on it, because then nothing here can say whether the run is an artifact one.
func carriedRecipeAtRef(ctx context.Context, spec *task.Spec) (*recipe.Recipe, error) {
	root := checkoutRoot(ctx, spec.Repo)
	data, found, err := recipeAtRef(ctx, root, spec.Ref, spec.Recipe.Name)
	if err != nil {
		return nil, err
	}
	if !found {
		data = []byte(spec.Recipe.YAML)
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, fmt.Errorf("recipe %s at %s is invalid: %s", spec.Recipe.Name, spec.Ref, recipe.ProblemsText(ps))
	}
	if rcp.Name != spec.Recipe.Name {
		return nil, fmt.Errorf("recipe %s at %s names itself %q; the file name and the name must agree", spec.Recipe.Name, spec.Ref, rcp.Name)
	}
	return rcp, nil
}

// recipeAtRef reads .fugaro/recipes/<name>.yaml at ref out of this checkout's
// own object database -- git cat-file, never the network and never the working
// tree -- the file the runner resolves first (internal/runner/recipe.go's
// readRepoRecipe). found is false when the ref has no such file; err when the
// answer cannot be had at all (no checkout root, a ref this checkout does not
// hold, a path that is not a blob).
//
// The refs are tried the way checkoutFugaroYAMLAtRef tries them, and for its
// reason: refs/remotes/origin/<ref> first, because that is the ref the run
// clones, and the plain ref as the fallback for a checkout that has the branch
// but no remote-tracking ref of it. Nothing is fetched here -- a fetch is a
// network write into a repository this command does not own.
func recipeAtRef(ctx context.Context, root, ref, name string) (data []byte, found bool, err error) {
	if root == "" {
		return nil, false, errors.New("this directory is not a checkout of the run's repository")
	}
	if ref == "" {
		return nil, false, errors.New("the launch names no ref")
	}
	at := ""
	for _, candidate := range []string{"refs/remotes/origin/" + ref, ref} {
		sha, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		if err == nil && sha != "" {
			at = candidate
			break
		}
	}
	if at == "" {
		return nil, false, fmt.Errorf("this checkout holds no %s ref (fetch it, or check it out)", ref)
	}
	rel := recipe.RepoPath(name)
	if _, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", at+":"+rel); err != nil {
		return nil, false, nil
	}
	data, err = gitCmd(ctx, root, "cat-file", "blob", at+":"+rel).Output()
	if err != nil {
		return nil, false, fmt.Errorf("%s at %s is not a readable file", rel, at)
	}
	if len(data) > recipe.MaxBytes {
		return nil, false, fmt.Errorf("%s at %s is %d bytes, over the 16 KiB limit", rel, at, len(data))
	}
	return data, true, nil
}

// recipeForLaunch is the recipe this launch will run, parsed: the task's own
// (the text the launcher embedded for a project or catalog recipe, else the
// repository file a repo-sourced one names, read from this checkout), or,
// for a task that names none, the one the runner will resolve for it —
// agent.recipe from this checkout's config, for a FIRST run (localRecipe,
// exactly as `fugaro validate` resolves it), and the catalog default for a
// follow-up, which never takes agent.recipe at all (the runner's
// resolveRecipe, internal/runner/recipe.go: `r.follow == nil &&
// cfg.Agent.Recipe != ""`). nil when the launcher cannot read or parse it
// here: the runner resolves the same recipe at the task's ref and refuses it
// there.
//
// The follow-up arm is not a detail: --attach resolves the mode through this
// function (checkAttachRecipe), and reading agent.recipe for a follow-up made
// the launcher allow, upload for and write a task for an artifact recipe the
// runner would then not run — it takes the default (mode: implement) — so the
// run failed at bootstrap with the bytes already in the bucket.
func recipeForLaunch(ctx context.Context, cfg *config.Config, spec *task.Spec) *recipe.Recipe {
	root := checkoutRoot(ctx, spec.Repo)
	if spec.Recipe == nil {
		if spec.IsFollowUp() {
			// Resolved without cfg: agent.recipe is not consulted for a
			// follow-up, so the name is the default's and this checkout's
			// layer, when there is one, is read for it exactly as the runner
			// reads that layer at the base — from the working tree here, which
			// is the approximation every launcher-side read of a base file
			// makes, and the runner stays the backstop for a base whose file
			// differs from it.
			return localRecipeNamed(root, recipe.DefaultName)
		}
		if cfg == nil {
			return nil
		}
		return localRecipe(root, cfg)
	}
	data := []byte(spec.Recipe.YAML)
	if len(data) == 0 {
		// A repository recipe: task.json carries its name alone (the
		// runner reads the file at the task's ref), so the launcher reads
		// the same file from its own checkout of the repository.
		if root == "" {
			return nil
		}
		var found bool
		var err error
		if data, found, err = recipe.ReadRepoFile(root, spec.Recipe.Name); err != nil || !found {
			return nil
		}
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil
	}
	return rcp
}

// runnerChooses reports whether the runner, not this CLI, decides spec's
// recipe: a first run with no recipe in its task and no checkout here.
//
// checkoutConfig (Task 10) resolves the repository's project layer,
// lenient (validate.go's layerOptions{Lenient: true}): an unreadable
// bucket or no project selected leaves it nil for a minimal (layer-only)
// file, the same as no checkout at all, so this reports the runner as
// choosing even where a checkout is right here.
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
// --total-timeout: a runaway value is more likely a typo than a plan. It is
// the flag's own name for backend.MaxRunTime, which is where the bound is
// defined -- it is a fact about what a run may ask for rather than a CLI
// policy, and the reaper's deadlines have to account for it too
// (internal/backend cannot import this package).
const OverrideCap = backend.MaxRunTime

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
	checkout := checkoutOf(ctx, spec.Repo)
	if spec.Workflow == "" {
		if spec.Workflow, err = resolveWorkflow(env, spec.Repo, "", checkout); err != nil {
			return "", nil, err
		}
	}
	// spec is stored bucket data nobody vouches for (review finding A): its
	// OAuthToken's shape was checked when it was first written (task.Parse),
	// but whether this repository may still use that account was never
	// re-checked here, the one place --retry resolves what it is about to
	// launch, before any build-record read or the launch itself.
	if err := claudeAccountAllowed(env, spec.Repo, cmp.Or(spec.OAuthToken, config.DefaultClaudeAccount), checkoutAgentAuth(checkout())); err != nil {
		return "", nil, err
	}
	// A report run is refused, the way a follow-up carrying one is
	// (refuseFollowUpReportMode): --retry launches the task of a first
	// launch as it stands, so the manifest it carries is the one that launch
	// listed, stale by now, and none of the checks a fresh report launch
	// makes — digestManifest's in-flight refusal among them — run at all. A
	// repository has one report run at a time because both would write the
	// same reports, and a stale manifest writes a report of records that
	// have moved on; the launcher of a fresh report run lists them again.
	// The task says it is one two ways: the manifest itself (digest_runs is
	// accepted under no other recipe, task.Validate), which is the only one
	// that speaks for a repository recipe, and the recipe's text or file,
	// for a report run whose manifest came out empty.
	if spec.Recipe != nil && (len(spec.DigestRuns) > 0 || digestRecipe(checkoutRoot(ctx, spec.Repo), spec.Recipe)) {
		return "", nil, userErr("run %s/%s is a report run, and --retry launches the task of its first launch as it stands: its manifest is stale by now, and a repository has one report run at a time, because both would write the same reports — launch a new report run with fugaro run --recipe %s", slug, runID, spec.Recipe.Name)
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
	return checkMaxParallelFor(ctx, env, 1)
}

// activeHorizon is the longest a run can still be going: the longest job
// timeout, or the cap a per-run override may reach, whichever is more, plus
// the task timeout slack and the queueing margin. It bounds every "is a run
// still active" question, which is why the report run's launch shares it
// with checkMaxParallelFor below (run_digest_manifest.go).
func activeHorizon(ctx context.Context, env *cloudEnv) (time.Duration, error) {
	longest, err := env.be.LongestTaskTimeout(ctx)
	if err != nil {
		return 0, remote(err)
	}
	return max(longest, OverrideCap) + backend.TaskTimeoutSlack + horizonMargin, nil
}

// checkMaxParallelFor is checkMaxParallel, generalized to a launch that
// needs room for `need` runs at once: a best-of-N batch (runAttempts) checks
// for all of N before its first launch, so it never starts part of a batch
// it cannot finish (design generic-tool G13). need is always 1 outside that
// one caller, which checkMaxParallel keeps as the common case.
func checkMaxParallelFor(ctx context.Context, env *cloudEnv, need int) error {
	horizon, err := activeHorizon(ctx, env)
	if err != nil {
		return err
	}
	active, err := env.be.List(ctx, backend.ListFilter{ActiveOnly: true, Since: time.Now().Add(-horizon)})
	if err != nil {
		return remote(err)
	}
	if len(active)+need > env.lc.MaxParallel {
		if need > 1 {
			return userErr("%d runs active; max_parallel is %d (--attempts %d needs %d)", len(active), env.lc.MaxParallel, need, need)
		}
		return userErr("%d runs active; max_parallel is %d", len(active), env.lc.MaxParallel)
	}
	return nil
}

// adoptStoredProjectLayer peeks at whatever task is already stored for
// spec's run ID, without writing anything, and when it is spec's own task
// (repeatTaskBytes equal, ignoring the layer itself), adopts its project
// layer into spec before the image gate below runs: a repeat whose earlier
// attempt stored a task but never launched it must be gated on the layer
// that stored task actually carries, since createTask (below) keeps it
// either way. No stored task (ErrNotFound), or one for a different
// request, leaves spec untouched: createTask is still the one that refuses
// "a different task".
func adoptStoredProjectLayer(ctx context.Context, s *runstore.Store, spec *task.Spec) error {
	have, err := s.ReadTask(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return remote(err)
	}
	a, err1 := repeatTaskBytes(have)
	b, err2 := repeatTaskBytes(spec)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if string(a) == string(b) {
		spec.ProjectLayer = have.ProjectLayer
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
	branch := oneLine(res.Branch)
	if res.Ref != "" {
		branch += " cut from " + oneLine(res.Ref)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n  branch %s\n", verb, what, branch); err != nil {
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
// name launch.json holds. Either name must be the run's own execution: the
// run's service account can write both objects, and the execution's own
// FUGARO_RUN is what says whose it is (checkExecution).
func existingLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec) (*runstore.Launch, error) {
	runID := spec.RunID
	check := func(name string) error {
		if _, err := env.checkExecution(ctx, nil, name, s.Slug(), runID); err != nil {
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
		// bucket): never launch it again. Nothing to backfill, and nothing
		// is stored: a run with no launch.json has no plan task for any
		// reader to find, the same way it has no launcher (LaunchedBy).
		return &runstore.Launch{Version: 1, RunID: runID, LaunchedAt: rec.StartedAt}, nil
	}
	if err := check(rec.Execution); err != nil {
		return nil, err
	}
	// The backend is the one this command selected, read from its codec, not
	// the constant Cloud Run's: a backfilled launch of a run that ran
	// elsewhere must be recorded as that backend's, or fugaro logs, cancel
	// and diagnose read the record against the wrong platform's names
	// (design compute-backend.md §2.5).
	l := &runstore.Launch{Version: 1, RunID: runID, Backend: launchBackend(env.be), Execution: rec.Execution, PlanTask: spec.PlanTask, LaunchedAt: rec.StartedAt}
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
	if !spec.IsFollowUp() && len(spec.ReviewPRs) == 0 {
		res.Ref = spec.Ref
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
	// The job this launch runs on (project_job.go): the shared project job of
	// the profile the workflow selects, or nil for the per-repository job this
	// launch has always used. Resolved before the claim is taken, so a policy
	// this CLI cannot read costs no claim to release — and it is resolved
	// before the mint, because it is what the token's window is measured
	// against.
	job, err := env.launchProjectJobFor(ctx, spec)
	if err != nil {
		return res, err
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
	// The launch declares itself to the coordinator's record (P4), beside the
	// claim it just won: the caller the task already names, the branch the run
	// is about to work on and the release that branch targets. It is a
	// declaration to an ADVISORY service, so nothing here can fail the launch
	// — a project with no database to declare to is skipped, and a report that
	// fails is a warning on stderr — and it is bounded well inside the hold,
	// because the launch's own work must not wait on it (spec §3: the
	// coordinator never blocks anything). A write the rules refuse is quiet:
	// only budget admins may write the record, so a refusal is what most
	// launchers get on every run, and a warning each time would be noise about
	// something they cannot change (owner ruling 2026-10-24). The error text can
	// quote what the database answered, so it goes through oneLine. The call
	// goes through the build's declarer (extend.go): an untagged build
	// registers none, so its launches declare nothing (D535), and a tagged
	// build registers the coordinator's own writer.
	if err := declareLaunch(hctx, env, slug, spec); err != nil && !errors.Is(err, rtdb.ErrPermission) {
		fmt.Fprintf(launchWarn, "warning: the launch was not reported to the coordinator: %s\n", oneLine(err.Error()))
	}
	// The run's budget identity: minted, sealed under a key drawn for this
	// launch and left in the bucket before the execution can start, by the
	// one CLI that holds the claim. A failure here means nothing started, so
	// the claim goes back. sealKey is the only copy of the key: it goes into
	// this execution's marker and nowhere else (D15). job, when the launch
	// resolved a shared project job, is the job the identity window is
	// measured from (project_job.go).
	sealKey, err := env.mintBudgetToken(hctx, slug, spec, job)
	minted := sealKey != nil
	if err != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, err
	}
	variant, ok := jobWorkflow(spec.Workflow, spec.OAuthToken)
	if !ok {
		// spec.Workflow, unlike spec.OAuthToken, is never shape-checked by
		// task.Parse: a bad --workflow, a stored task from before this
		// feature validated anything about it, or tampered bucket data could
		// all reach here. Definitive, same cleanup as a platform refusal:
		// nothing started.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		if minted {
			env.dropBudgetToken(rctx, slug, spec.RunID)
		}
		rcancel()
		return res, userErr("workflow %s is not a plain workflow name, so it can't be combined with Claude account %s; nothing started", oneLineCLI(spec.Workflow), pluginwire.Printable(spec.OAuthToken))
	}
	lctx, cancel := context.WithTimeout(hctx, launchTimeout)
	lspec := backend.LaunchSpec{Repo: backend.RepoRef{Repo: spec.Repo, Slug: slug}, Workflow: variant, RunID: spec.RunID, Timeout: launchTimeoutOf(spec)}
	if job != nil {
		// The shared project job, by its own name. FUGARO_RUN is still set
		// (backend/gcp's Launch): on a job every repository shares it is the
		// only thing that says which run an execution is.
		lspec.Job = job.Name
	}
	if minted {
		// This execution carries a budget token, so the runner must never read
		// the token's absence as "this project has published no policy": with
		// the marker set, a missing token stops the run whatever the job's own
		// mode (internal/runner's BudgetTokenEnv, plan R2-8(a)). It is an
		// execution override — minting is a fact about this launch, not about
		// the job's deploy-time environment — and its value is the launch's
		// seal key, the one thing that opens the object (D15): handed to this
		// execution and no other. A backend that cannot carry a secret where
		// only the execution and the override's own audience read it refuses
		// the launch rather than carry it (backend.LaunchSpec.SecretEnv).
		lspec.SecretEnv = []string{sealMarker(sealKey)}
	}
	// The credentials service, when the installation has opted into one and
	// this execution is a repository's own job (job is the shared project job
	// otherwise, and the service refuses that caller: see the helper).
	jobName := ""
	if job != nil {
		jobName = job.Name
	}
	lspec.Env = credentialsServiceLaunchEnv(env.lc, jobName)
	ref, err := env.be.Launch(lctx, lspec)
	cancel()
	// A backend error may quote the request it was given; the key is in that
	// request, so it is taken out of the error before anything formats it.
	err = sealRedact(err, sealKey)
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
	// The backend the launch actually used, from the seam, as above: the
	// launch succeeded on env.be, so env.be's codec is what names it.
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: launchBackend(env.be), Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, PlanTask: spec.PlanTask, LaunchedAt: now.UTC()}
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

// credentialsServiceLaunchEnv is what this launch puts on the execution's
// environment for the credentials service (plan task C1.24, D146/D147): the
// service's URL, under the one name the runner and the keyless GitHub path
// both read, or nothing at all. job is the shared project job this launch
// resolved, or "" for a repository's own job.
//
// The block's presence in the selected config IS the opt-in. The config is the
// published shared one where this machine has no local config of its own
// (localcfg.Select), and the URL in it is the installation's own record --
// Cloud Run's assignment for the service the installation root created, which
// the credentials stage writes and MergeShared never lets a machine that lacks
// it erase (internal/localcfg/shared.go, the ledger item of PR #903). So a
// teammate's `fugaro run`, on a machine that never ran the opt-in, still hands
// the job the installation's service.
//
// A SHARED PROJECT JOB IS NOT TOLD, and that is the service's own rule rather
// than a policy chosen here: its admission refuses an account that belongs to
// a shared project job whole, before any route (internal/credsvc/auth.go's
// verifyJob, Global Constraint 6, C3.9). So the address would buy such a job
// nothing it could use -- a `cascade` run would fail at the service's door
// instead of at the runner's clear "this job has no credentials service" --
// and it would cost it something: the same variable is what opens the git
// provider keyless (internal/gitprov/providers/providers.go), so a shared
// project job told about the service would stop reading the App key it still
// mounts and be refused the mint that replaced it, breaking every GitHub run
// on it. The shared jobs keep their own credentials until C3.9 moves them,
// which is what SECURITY.md and the credentials stage's confirmation both say.
//
// It is plain Env and not SecretEnv, deliberately. The URL is not a secret:
// it is the service's public address, it is the audience every caller's ID
// token is minted for, and the service's own container carries it in plain
// environment (credentials_serve.go's envCredentialsServiceURL, the same
// name). A secret entry would also be the wrong carrier on the backend that
// matters: Batch refuses SecretEnv outright (backend.LaunchSpec.SecretEnv),
// so an installation on the Batch backend would either lose the variable or
// fail to launch, and the one thing this value must not do is depend on which
// backend a project happens to use.
//
// An installation with no block -- every installation until an owner runs
// `fugaro setup --provision-credentials` -- gets nil, so its launch spec is
// byte for byte the one it built before this variable existed: no entry, no
// empty value, nothing for a backend to merge.
func credentialsServiceLaunchEnv(lc *localcfg.Config, job string) []string {
	if job != "" {
		return nil
	}
	if lc == nil || lc.CredentialsService == nil || lc.CredentialsService.URL == "" {
		return nil
	}
	return []string{envCredentialsServiceURL + "=" + lc.CredentialsService.URL}
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

// launchWarn is where a launch's warnings go, os.Stderr in production. A
// variable so that a test reads the warning a failed declaration prints
// rather than leaking it into the test's own output.
var launchWarn io.Writer = os.Stderr

// reportIntentTimeout bounds one launch's declaration. It is short on purpose:
// the declaration is advisory, and a database that is slow or unreachable must
// cost the launch a few seconds rather than the hold it is working inside.
const reportIntentTimeout = 15 * time.Second

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
			return nil, userErr("a launch of %s by %s (claimed %s) is still in flight after %s; run fugaro runs ls, or fugaro run --retry %s after %s",
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

// authorGates is the launch's half of the authorship gates, run where
// finishLaunch knows the workflow's kind and checkout mode and before the task
// is written: the git.author key, whatever it says (gitAuthorSince), is judged
// first and refuses the launch, the field this launch would write (authorSince)
// is dropped rather than refused, and only then is the author resolved from
// this machine's local config — for a file that opted in by spelling the key
// launcher.
//
// clone is the workflow's checkout mode, nil where the launch could not read
// it: the key gate then takes the derived-image judge, as checkRigorImage and
// checkGovernanceImage do, and reaches no gate at all when the file itself
// could not be read. The key is judged for every mode — a review or artifact
// run's runner decodes the same file strictly — while the author is recorded
// for neither: neither pushes, so the identity such a task carried would be a
// claim about commits that are never made (D605). That is the two shapes
// budgetBranch reads as "pushes nothing" — a review run carries review_prs
// rather than a mode, and only an artifact recipe writes mode.
//
// Nothing is recorded without the opt-in: git.author is the repository's own
// decision whose name goes into its history (D606), and a repository that did
// not write the key keeps the run's own identity, as every repository does
// today, whether or not the launcher has an identity to offer.
func authorGates(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, clone *bool, warn io.Writer) error {
	// The file is read once and both questions are asked of it here: the key
	// (which images may run it) and the value (whether a run records an
	// author). Each read may run git, so a launch that opted in reads it once,
	// as checkRigorImage resolves its answer once for its own gate.
	cfg := launchFileConfig(ctx, env, spec)
	if gitAuthorSet(cfg) {
		if clone != nil && *clone {
			if err := checkCloneGitAuthorImage(ctx, env, spec, cfg, warn); err != nil {
				return err
			}
		} else if err := checkGitAuthorImage(ctx, env, slug, spec, kind, cfg, warn); err != nil {
			return err
		}
	}
	if !gitAuthorLauncher(cfg) || spec.ReviewPRs != nil || spec.Mode == recipe.ModeArtifact {
		return nil
	}
	author, err := launchAuthor(ctx, env.lc, spec.RequestedBy)
	if err != nil {
		return err
	}
	spec.Author = author
	authorSkew(ctx, env, slug, spec, clone, warn)
	return nil
}

// launchAuthor is the identity a launch records as its commits' author
// (task.Spec.Author, D604): the local config's commit_email when it is set,
// else the launcher's own identity me — the user: key, else git config
// user.email, which is what localcfg.Me answered — with, as the name, the local
// config's commit_name, else git's own user.name on this machine, else the
// address's local part.
//
// nil means the launch records nobody, so the run's commits author and commit
// under the run's own identity, which is what every run does today (D605): me
// is not an address (user: alice, a CI box, a bot account). A commit_email the
// launcher may not claim is the one case answered with a refusal instead of an
// omission (D649): it has to be the address the launcher launches as, or a
// forge noreply address, and anything else is a colleague's address — the one
// thing this rule exists to keep out of a history the forge links to that
// colleague's account.
func launchAuthor(ctx context.Context, lc *localcfg.Config, me string) (*task.Person, error) {
	email := me
	if lc.CommitEmail != "" {
		if lc.CommitEmail != me && !forgeNoreply(lc.CommitEmail) {
			return nil, userErr("the local config's commit_email %s is neither the address you launch as (%s) nor a forge noreply address, so a run must not author its commits with it: one launcher must not attribute commits to somebody else. Set user: to that address to launch as it, or leave commit_email out",
				pluginwire.Printable(lc.CommitEmail), pluginwire.Printable(me))
		}
		email = lc.CommitEmail
	}
	if !isAddress(email) {
		return nil, nil
	}
	name := lc.CommitName
	if name == "" {
		name = gitUserName(ctx)
	}
	if name == "" {
		name = email[:strings.IndexByte(email, '@')]
	}
	author := &task.Person{Name: name, Email: email}
	// The shape Spec.Validate enforces when the runner reads the task back
	// (task.AuthorProblem): the two fields become git's own header values, so a
	// commit_name holding a line break would forge the rest of the header
	// block, and a task the runner refuses empty-handed is worse than a launch
	// that says which key to fix.
	if p := task.AuthorProblem(*author); p != "" {
		return nil, userErr("the commit author this launch would record has a %s; fix the local config's commit_name or commit_email, or this machine's git config user.name, which is what the name falls back to", p)
	}
	return author, nil
}

// isAddress reports whether s is a commit address: the rule task.AuthorProblem
// applies to a Person's email — one @ with a local part and a domain around it
// — and the one D605's "not an email" means, so a launcher whose recorded
// identity is a bare name (user: alice) records no author at all rather than
// one git cannot read.
func isAddress(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at == strings.LastIndexByte(s, '@') && at < len(s)-1
}

// forgeNoreply reports whether email is one of a forge's own noreply addresses
// — GitHub's <id>+<login>@users.noreply.github.com and GitLab's
// <id>-<login>@users.noreply.gitlab.com — which a launcher may set commit_email
// to without claiming somebody else's address (D649): such an address is
// routed to the account that owns it and to no mailbox, which is the whole of
// what a launcher can arrange for itself. Only the domain is judged: the id and
// the login in the local part are the person's own, and nothing before E's
// authenticated identity could verify which login a launcher holds.
func forgeNoreply(email string) bool {
	dom := email[strings.IndexByte(email, '@')+1:]
	return dom == "users.noreply.github.com" || dom == "users.noreply.gitlab.com"
}

// gitUserName is this machine's git config user.name, "" when it is unset or
// git cannot be asked: a commit's display name is a nicety next to its address,
// and the address's local part stands in when git has none (launchAuthor), so a
// missing or slow git costs the author its name and never the launch.
func gitUserName(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "git", "config", "user.name")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
