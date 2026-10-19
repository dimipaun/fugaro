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
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
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
			"own).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRun(cmd, &o, args) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	f.StringVar(&o.ref, "ref", "", "base branch or ref (default: the repo's base_branch)")
	f.StringVar(&o.workflow, "workflow", "", "workflow to run (default: the repo's only workflow)")
	f.StringVar(&o.runID, "run-id", "", "run ID to use; repeating one reports its launch instead of starting another")
	f.StringVar(&o.batch, "batch", "", "batch label, to group runs in fugaro runs ls")
	f.StringVar(&o.title, "title", "", "the run's title, one line: what fugaro watch and runs ls list the run by, and the pull request's title unless the agent wrote its own pr.md (default: the task's first line); a follow-up (--pr) keeps its run's title unless this is given")
	f.StringVar(&o.tf, "task-file", "", "read the task from this file, or - for stdin")
	f.StringVar(&o.retry, "retry", "", "launch the stored task of RUN (<repo-slug>/<run-id> or a run ID) that never started")
	f.StringVar(&o.totalTimeout, "total-timeout", "", "override the workflow's timeouts.total for this run, such as 45m")
	f.IntSliceVar(&o.pr, "pr", nil, "continue Fugaro PR N: act on its trusted review comments (TEXT adds instructions); with a mode: review --recipe, review PR N instead (repeatable, 1 to 4, any open PR)")
	f.StringVar(&o.recipe, "recipe", "", "the recipe (task loop) to run: .fugaro/recipes/NAME.yaml, the project's, or the catalog's ("+strings.Join(recipe.CatalogNames(), ", ")+"); default: agent.recipe in fugaro.yaml, else default; a follow-up (--pr) keeps its previous run's")
	f.StringVar(&o.token, "token", "", "named Claude account (claude_accounts in the local config) this run uses, for a workflow whose agent.auth is oauth; default: agent.oauth_token in fugaro.yaml, else the repository's default account; a follow-up (--pr) keeps its previous run's account unless this is given explicitly, which overrides it (checked the same way as a first run's)")
	f.StringArrayVar(&o.attach, "attach", nil, "attach a file to the run: repeatable, uploaded to the run's own inputs/ and downloaded by a mode: artifact run into the checkout's inputs/ (needs a mode: artifact recipe; refused on a run ID that already has a task)")
	f.IntVar(&o.attempts, "attempts", 0, "launch N (2 to 4) independent runs of the same task, workflow, ref and recipe as a best-of-N batch (bestof-<first run id> unless --batch is given); refused with --pr, --run-id, --retry or a review-mode recipe")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.noBudgetCheck, "no-budget-check", false, "skip the launch pre-check of the budget (kill switches, missing caps, no headroom); the run is held to the same limits when it starts")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// taskFlags are the flags --retry excludes: the stored task.json supplies them.
var taskFlags = []string{"repo", "ref", "workflow", "run-id", "batch", "title", "task-file", "total-timeout", "pr", "recipe", "token", "attach"}

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
			if _, err := claudeAccountFor(env, repo, o.token, checkoutOf(ctx, repo)()); err != nil {
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
		// --attach, before the gates below: its recipe refusal, its caps,
		// D9's line and its image gate (design §4, plan P7/P9). It uploads
		// nothing here — attachUpload does that below, after the last gate
		// that can still refuse this launch, so a refused launch leaves
		// nothing in the bucket under a run id that never gets a task. A
		// launch with no --attach does nothing here at all.
		agentRecipe, kind, clone := "", "", false
		if co := checkoutConfig(ctx, spec.Repo); co != nil {
			agentRecipe = co.Agent.Recipe
			if w, ok := co.Workflows[spec.Workflow]; ok {
				kind = w.BaseKind()
				clone = w.Checkout == config.CheckoutClone
			}
		}
		caps, err := attachLaunch(ctx, env, slug, spec, s.Prefix(), o.attach, kind, clone, warn)
		if err != nil {
			return launchResult{}, err
		}
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
		if err := checkArtifactLaunch(ctx, spec); err != nil {
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

// attemptPlan is everything a new run's attempts share, resolved once: the
// repository, workflow, ref, account and recipe. newSpec (a single launch)
// and runAttempts (every attempt of a best-of-N batch) both build one, then
// stamp a fresh run id onto it per spec (buildAttemptSpec): a project recipe
// published mid-batch must not give a batch's later attempts a different
// recipe from its first (design generic-tool review, 2026-10-13).
type attemptPlan struct {
	repo, slug, workflow, ref, me, account, totalOverride string
	recipe                                                *task.Recipe
	// mode is the resolved recipe's mode when the runner cannot read it
	// from the task alone (mode: artifact today): buildAttemptSpec writes it
	// into the spec, and it is empty for implement and review, so those
	// specs stay byte-identical to what the previous release wrote.
	mode string
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
	ref := o.ref
	if ref == "" {
		if r, ok := env.localRepo(repo); ok && r.BaseBranch != "" {
			ref = r.BaseBranch
		} else if c := checkout(); c != nil && c.Git.BaseBranch != "" {
			ref = c.Git.BaseBranch
		} else {
			return nil, userErr("cannot tell the base branch of %s; pass --ref", repo)
		}
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
	account, err := claudeAccountFor(env, repo, o.token, checkout())
	if err != nil {
		return nil, err
	}
	rcp, mode, err := chooseRecipe(ctx, env, o.recipe, checkout, checkoutRoot(ctx, repo), warn, forAttempts)
	if err != nil {
		return nil, err
	}
	return &attemptPlan{repo: repo, slug: slug, workflow: workflow, ref: ref, me: me, account: account, totalOverride: totalOverride, recipe: rcp, mode: mode}, nil
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
	spec := &task.Spec{Version: 1, RunID: runID, Repo: plan.repo, Ref: plan.ref, Workflow: plan.workflow, Task: text, Title: o.title,
		RequestedBy: plan.me, Batch: o.batch, Recipe: plan.recipe, Mode: plan.mode}
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
	account, err := claudeAccountFor(env, repo, o.token, checkout())
	if err != nil {
		return "", nil, err
	}
	printRecipeNote(warn, rr, true)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, Workflow: workflow, Task: text, Title: o.title,
		RequestedBy: me, Batch: o.batch, Recipe: rr.taskRecipe(), ReviewPRs: append([]int(nil), o.pr...)}
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
// (fugaro run --token), else the checkout's fugaro.yaml agent.oauth_token,
// else the repository's default (config.DefaultClaudeAccount). A non-default
// name is refused (claudeAccountAllowed) unless config.AllowedClaudeAccounts
// (the local config's claude_accounts) lists it for repo, before any launch
// (design docs/design/named-oauth-tokens.md §3 T7) — checked whether or not
// a checkout is available here, so --repo X --token N outside any checkout
// is held to the same rule a checkout's own resolution would be.
func claudeAccountFor(env *cloudEnv, repo, flag string, co *config.Config) (string, error) {
	name := flag
	if name == "" && co != nil {
		name = co.Agent.OAuthToken
	}
	if name == "" {
		name = config.DefaultClaudeAccount
	}
	return name, claudeAccountAllowed(env, repo, name, co)
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
// membership in config.AllowedClaudeAccounts. When a checkout IS known and
// its workflow's agent.auth is not oauth, naming an account makes no sense
// at all (no secret is ever mounted for it, so the name would be silently
// unused) and is refused outright, not merely left unchecked.
func claudeAccountAllowed(env *cloudEnv, repo, name string, co *config.Config) error {
	if name == config.DefaultClaudeAccount {
		return nil
	}
	if co != nil && co.Agent.Auth != "oauth" {
		return userErr("named Claude account %s only applies to a workflow whose agent.auth is oauth; %s's agent.auth is %s",
			pluginwire.Printable(name), repo, co.Agent.Auth)
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

// checkArtifactLaunch refuses a mode: artifact launch whose caps cannot fit
// the workflow's memory, or whose render or probe step names a command the
// workflow leaves empty: the launch's arm of runner.RecipeGateProblems
// (design artifact-runs.md §5.2, Review Focus 12), called before createTask
// writes anything, so an over-memory artifact launch leaves no task and no
// execution behind. A launch whose recipe or workflow the launcher cannot
// resolve locally is judged by the runner's own bootstrap instead
// (checkGateCommands), which reports the same list.
func checkArtifactLaunch(ctx context.Context, spec *task.Spec) error {
	cfg := checkoutConfig(ctx, spec.Repo)
	if cfg == nil {
		return nil
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return nil
	}
	rcp := recipeForLaunch(ctx, cfg, spec)
	if rcp == nil || rcp.Mode != recipe.ModeArtifact {
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
	if err := claudeAccountAllowed(env, spec.Repo, cmp.Or(spec.OAuthToken, config.DefaultClaudeAccount), checkout()); err != nil {
		return "", nil, err
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

// checkMaxParallelFor is checkMaxParallel, generalized to a launch that
// needs room for `need` runs at once: a best-of-N batch (runAttempts) checks
// for all of N before its first launch, so it never starts part of a batch
// it cannot finish (design generic-tool G13). need is always 1 outside that
// one caller, which checkMaxParallel keeps as the common case.
func checkMaxParallelFor(ctx context.Context, env *cloudEnv, need int) error {
	longest, err := env.be.LongestTaskTimeout(ctx)
	if err != nil {
		return remote(err)
	}
	horizon := max(longest, OverrideCap) + backend.TaskTimeoutSlack + horizonMargin
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
		// bucket): never launch it again. Nothing to backfill.
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
	l := &runstore.Launch{Version: 1, RunID: runID, Backend: launchBackend(env.be), Execution: rec.Execution, LaunchedAt: rec.StartedAt}
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
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: launchBackend(env.be), Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, LaunchedAt: now.UTC()}
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
