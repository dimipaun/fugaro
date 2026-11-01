package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro budget preview (design generic-tool §1.1, G20): what a batch of runs
// may cost at most, against today's caps. It is a ceiling, not a forecast:
//
//	N × min(per-run cap, stages_max(recipe) × agent.max_budget_usd)
//
// and, when the repository has recorded enough runs, what one run of the
// recipe typically costs, from the real-dollar cost each stage of those runs
// recorded (a run whose model_usd was a subscription's rather than a bill is
// left out: agent.auth oauth, or a cascade run the plan served in full).
//
// Nothing here is written and no run is started: the caps and today's
// counters are read as budget show reads them, the recipe is resolved as
// run resolves it (the repository's .fugaro/recipes, the project's published
// recipes, the catalog), and the history is read from the runs bucket as
// runs ls reads it.

// previewMaxRuns bounds --runs: a larger batch is a typo, and the ceiling it
// would print means nothing.
const previewMaxRuns = 100000

const (
	// previewHistoryWindow is how far back the typical line looks: the
	// design's "the repository's last 30 days of result.json".
	previewHistoryWindow = 30 * 24 * time.Hour
	// previewMinRuns is how many recorded runs the typical line needs. With
	// fewer, a mean says more about the sample than about the recipe, so the
	// design omits the line.
	previewMinRuns = 5
)

type budgetPreviewOptions struct {
	cloud  cloudOptions
	recipe string
	runs   int
	repo   string
	json   bool
}

func newBudgetPreviewCmd() *cobra.Command {
	var o budgetPreviewOptions
	cmd := &cobra.Command{
		Use:   "preview --recipe NAME --runs N [--repo R]",
		Short: "Estimate a batch's worst-case model spend against today's caps",
		Long: "Estimate what N runs of a recipe may cost at most, and how that compares\n" +
			"with today's remaining daily caps. It is a ceiling, not a forecast: a run\n" +
			"is bounded by the smaller of budget.per_run_usd and the recipe's most\n" +
			"stages times the repository's agent.max_budget_usd.\n\n" +
			"That figure is the model ceiling and nothing else: a run also bills for\n" +
			"the machine it asked for, and no budget key bounds compute at all --\n" +
			"the run's own timeout is the cap.\n" +
			"So for every workflow the checkout defines the preview also prints a\n" +
			"compute ceiling -- the workflow's own resources over its own\n" +
			"timeouts.total, at this installation's compute prices -- and says out\n" +
			"loud that it is NOT CAPPED, because for a run whose expensive half is a\n" +
			"render on a big machine the model ceiling is not the worst case. The\n" +
			"basis is timeouts.total and not a render step's own timeout: a render\n" +
			"that overruns still burns the run's clock to the deadline. Without a\n" +
			"price table for the installation's region the line names what is\n" +
			"missing and prints no dollar figure, rather than $0.00.\n\n" +
			"When the repository's recorded runs of the last 30 days are at least 5,\n" +
			"the preview also prints, clearly labelled typical:, what one run of the\n" +
			"recipe usually costs: the mean recorded cost of each stage kind (on the\n" +
			"model the checkout pins for it, with the recipe's role mapping applied as\n" +
			"a run applies it), at the recipe's typical stage count -- one round of\n" +
			"each loop the recipe and its round knobs leave in, the two fix stages\n" +
			"sharing one mean -- over the runs of the window, whatever they ended as:\n" +
			"a stage counts once it recorded a cost. A kind the window has no cost\n" +
			"for on that model leaves the line out and names the model, rather than\n" +
			"pricing the pinned model with what another one cost. Runs the figure\n" +
			"cannot use are said to be left out, never counted as runs that cost\n" +
			"nothing: a run whose model_usd was a subscription's rather than a bill\n" +
			"(agent.auth oauth, or a cascade run the plan served in full), a\n" +
			"result.json that is missing or unreadable, and a record with no\n" +
			"per-stage cost at all (one from before the runner kept it, or\n" +
			"stages that all cost nothing).\n\n" +
			"The recipe resolves as it does for a run: the repository's\n" +
			".fugaro/recipes, the project's published recipes, then the catalog. The\n" +
			"knobs come from the checkout's fugaro.yaml; without a checkout of the\n" +
			"repository here, agent.max_budget_usd is unknown and only\n" +
			"budget.per_run_usd bounds the figure, so with neither the preview says it\n" +
			"is unbounded.\n\n" +
			"It reads the database's caps and today's counters (the Viewer role), and\n" +
			"the repository's runs (the runs bucket, for the typical line). A recipe\n" +
			"that is not in the checkout also reads the project's recipes from the\n" +
			"runs bucket, as recipes ls does, which a plain project Viewer cannot\n" +
			"read (the converge removes that access): not reading it costs the project layer,\n" +
			"and the ceiling with it. The typical line's own read of the bucket is\n" +
			"the one that costs the line and nothing else: a bucket it cannot read\n" +
			"leaves the ceiling standing. It writes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetPreview(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.recipe, "recipe", "", "the recipe to preview ("+strings.Join(recipe.CatalogNames(), ", ")+")")
	f.IntVar(&o.runs, "runs", 0, "how many runs to estimate")
	f.StringVar(&o.repo, "repo", "", "the repository (owner/name); default: the checkout's origin")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	for _, name := range []string{"recipe", "runs"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

// previewKnobs are a run's numbers that come from the repository: its two
// round knobs and Claude Code's per-stage cap. maxStageUSD is 0 (unknown)
// when there was no checkout to read one from, and from is "defaults" then.
//
// The rounds are the knobs where the repository set them and the rigor mode's
// own row where it set neither (rigor.DefaultRounds, the owner's ruling of
// 2026-10-31): an unset knob is no longer filled in with one flat default at
// config load, because the default depends on the mode. mode is what supplied
// them, and the printed figures name it.
type previewKnobs struct {
	first, rounds int
	maxStageUSD   float64
	from          string
	mode          rigor.Mode
}

// previewKnobsOf reads a checkout's numbers, if there is a checkout at all.
// The mode is the one the repository resolves with nothing narrower — the
// repository's own rigor default, else the project layer's, else rigor.Default
// (pro) — which is what rigor.Resolve answers for a run that declares no path.
// It is the same resolution internal/runner's plan uses, so the preview and
// the run agree on the number; what the preview cannot know is an area rule
// (the paths a run turns out to touch) or a task's own override, so a run of a
// repository whose areas are stricter spends more than this said.
//
// project is the project layer's half of that resolution, which the caller
// reads itself (previewProjectRigor) when there is no checkout to read the
// repository's file from: with no cfg at all the layer is the only layer there
// is, and leaving it out resolved every such preview at rigor.Default (pro)
// whatever the project had published.
func previewKnobsOf(cfg *config.Config, project rigor.Config) previewKnobs {
	k := previewKnobs{from: "defaults", mode: rigor.Resolve(project, rigor.Request{}).Mode}
	if cfg != nil {
		k = previewKnobs{from: "checkout", mode: rigor.Resolve(cfg.RigorConfig(), rigor.Request{}).Mode,
			first: cfg.Agent.FirstLineRounds, rounds: cfg.Agent.ReviewRounds, maxStageUSD: cfg.Agent.MaxBudgetUSD}
	}
	defaults := rigor.DefaultRounds(k.mode)
	if k.first == 0 {
		k.first = defaults.Inner
	}
	if k.rounds == 0 {
		k.rounds = defaults.Senior
	}
	return k
}

// previewProjectRigor is the project layer's rigor half, for a preview that
// has no checkout to resolve one from: cfg is nil there, so the pair of
// layers a run would resolve stops at the project layer. It is the layer
// embedProjectLayer embeds for a launch from outside a checkout (decision
// L8): the installation's own project and GCP project are the anchor, since
// the file that would anchor it (a checkout's fugaro.yaml) is not here, and
// the layer the bucket holds under that anchor is the one every such launch
// carries.
//
// Lenient, like every read that is not a launch, and a read that fails leaves
// rigor.Default standing rather than failing an estimate that worked before:
// the preview prints the mode it used, and its own note already says the
// numbers are the mode's defaults. A layer that is there but invalid parses
// under neither mode (findLayer), so this reports none rather than a wrong
// one.
func previewProjectRigor(ctx context.Context, lc *localcfg.Config) rigor.Config {
	if lc == nil || lc.Name == "" || lc.GCPProject == "" {
		return rigor.Config{}
	}
	anchor := []byte(fmt.Sprintf("version: 1\nproject: %s\ngcp_project: %s\n", lc.Name, lc.GCPProject))
	fl, err := findLayer(ctx, getenvOS, anchor, lc, layerOptions{Lenient: true}, time.Now())
	if err != nil || fl.Layer == nil {
		return rigor.Config{}
	}
	return (&config.Config{Layer: fl.Layer}).ProjectRigor()
}

// previewCompute is one workflow's compute ceiling: what a run of it can bill
// for the machine it asked for over the run's own clock. The model caps count
// model dollars and nothing else, so no cap covers this figure at all -- which
// is what D4 decides and what the printed line says out loud (design
// artifact-runs.md §6).
type previewCompute struct {
	Workflow string `json:"workflow"`
	VCPU     int    `json:"vcpu"`
	// Memory is the workflow's resources.memory as written, and GiB is it in
	// GiB (0 when it is not a memory size, which Unpriced then names).
	Memory string  `json:"memory"`
	GiB    float64 `json:"memory_gib"`
	// TotalS is the basis: timeouts.total in seconds, and deliberately not
	// timeouts.render. The line is the worst case a run can bill, and a
	// render that overruns its own step still burns the run's clock until
	// the deadline.
	TotalS float64 `json:"total_s"`
	// USD is that worst case at this installation's compute prices; null
	// when the workflow could not be priced, and then Unpriced says why and
	// the line prints no dollar figure rather than a wrong one.
	USD      *float64 `json:"usd"`
	Source   string   `json:"price_source,omitempty"`
	Unpriced string   `json:"unpriced,omitempty"`
}

// previewComputeCeilings is the compute ceiling of every workflow the
// checkout defines, in name order: the resources and the clock are the
// workflow's own, so a repository with several of them gets one line each,
// and a repository with none (no checkout here) gets none.
//
// The arithmetic is Prices.ComputeUSD over the workflow's own resources --
// what the runner's own estimate calls (internal/runner/cost.go:63) -- and
// deliberately not Prices.Quantities: that method answers a machine type's
// quantities for a table that bills one (internal/backend/backend.go:285-289),
// and this table is Cloud Run's, which bills whatever the workflow asked for.
// p is the price book every other compute figure in this CLI is priced with
// (cloudEnv.prices at the installation's region), so the preview's number and
// a run's recorded cost.compute_usd come from the same table.
func previewComputeCeilings(cfg *config.Config, p backend.Prices, region string) []previewCompute {
	out := []previewCompute{}
	if cfg == nil {
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		w := cfg.Workflows[name]
		c := previewCompute{Workflow: name, VCPU: w.Resources.CPU, Memory: w.Resources.Memory,
			TotalS: w.Timeouts.Total.Duration.Seconds()}
		gib, err := backend.MemoryGiB(w.Resources.Memory)
		if err != nil {
			c.Unpriced = fmt.Sprintf("resources.memory %q is not a memory size", oneLine(w.Resources.Memory))
			out = append(out, c)
			continue
		}
		c.GiB = gib
		switch {
		case w.Timeouts.Total.Duration <= 0:
			c.Unpriced = "timeouts.total is not set, so the run has no clock to price"
		case p.VCPUSecondUSD <= 0 || p.GiBSecondUSD <= 0:
			c.Unpriced = "no compute price table for region " + oneLine(region)
		default:
			usd := p.ComputeUSD(float64(w.Resources.CPU), gib, w.Timeouts.Total.Duration)
			c.USD, c.Source = &usd, p.Source
		}
		out = append(out, c)
	}
	return out
}

// previewHeadroom is what one scope has left under its daily cap today.
type previewHeadroom struct {
	usd   float64
	known bool
}

// previewPerRun is the per-run cap a run launched from here would be held
// to: $FUGARO_MAX_RUN_USD when it is set (the job's own cap, which
// update --repo writes from the project config's budget.per_run_usd), else
// that config value; and always the database's effective per-run cap (the
// repository's own or the default, and the global one where that side is
// unset, D6). ok is false when neither side is set, and then only the
// per-stage cap bounds a run. srcs names each one found, with its value, and
// the database's names the scope it came from (repo, default or global) --
// plan Task 0.3 step 7, because "the global cap is the ceiling here" and "the
// repository has its own smaller cap" are different facts about one number.
// origin is the short name of the one that binds: the scope for the
// database's, the variable for the other two.
//
// The two owner-side values are alternatives, not a min: a stale
// $FUGARO_MAX_RUN_USD tells the runner to ignore the config, so taking the
// smaller of the two would report a ceiling the runner never enforces -- the
// unsafe direction for a ceiling. The database's cap is a min, because the
// rules deny a lease that exceeds either.
func previewPerRun(lc *localcfg.Config, caps budget.Caps) (best budget.Micros, srcs []string, origin string, ok bool, err error) {
	take := func(m budget.Micros, what, short string) {
		srcs = append(srcs, what+" "+usdFixed(m.USD()))
		if !ok || m < best {
			best, ok, origin = m, true, short
		}
	}
	if v := os.Getenv(runner.MaxRunUSDEnv); v != "" {
		// The same checks the runner makes (runner.spendFromEnv): a
		// malformed cap is an infra error there, and the preview must not
		// quietly price a run at a cap it would refuse to start with.
		usd, perr := strconv.ParseFloat(v, 64)
		if perr != nil || math.IsNaN(usd) || usd <= 0 {
			return 0, nil, "", false, userErr("%s %q: it must be a number of US dollars, more than 0 and at most %d", runner.MaxRunUSDEnv, v, pricing.MaxUSD)
		}
		m, ferr := pricing.FromUSD(usd)
		if ferr != nil {
			return 0, nil, "", false, userErr("%s %q: %v", runner.MaxRunUSDEnv, v, ferr)
		}
		if m < 1 {
			return 0, nil, "", false, userErr("%s %q: it rounds to nothing; the smallest cap is $0.000001", runner.MaxRunUSDEnv, v)
		}
		take(m, runner.MaxRunUSDEnv, runner.MaxRunUSDEnv)
	} else if lc.Budget != nil && lc.Budget.PerRunUSD > 0 {
		if m, ferr := pricing.FromUSD(lc.Budget.PerRunUSD); ferr == nil {
			take(m, "budget.per_run_usd", "budget.per_run_usd")
		}
	}
	if m, capOK := caps.PerRun(); capOK {
		// The scope, not only the amount: the global fallback and the
		// repository's own cap bound a run the same way but are different
		// facts to the operator reading this (plan Task 0.3 step 7).
		scope := perRunCapSource(caps)
		take(m, "the database's per-run cap ("+scope+")", scope)
	}
	return best, srcs, origin, ok, nil
}

// previewDoc is --json's document.
type previewDoc struct {
	Project    string `json:"project"`
	GCPProject string `json:"gcp_project"`
	Repo       string `json:"repo"`
	Slug       string `json:"slug"`
	Recipe     string `json:"recipe"`
	// Source is where the recipe was found: repo, project or catalog.
	Source string `json:"recipe_source"`
	Runs   int    `json:"runs"`
	// Stages is the recipe's stage bound at these knobs: the most model
	// stages a run of it can start.
	Stages    int    `json:"stages"`
	KnobsFrom string `json:"knobs_from"`
	Knobs     struct {
		FirstLineRounds int `json:"first_line_rounds"`
		ReviewRounds    int `json:"review_rounds"`
	} `json:"knobs"`
	// RigorMode is the mode whose row supplied either round the checkout left
	// unset. It is not on the wire: knobs_from and knobs are the contract the
	// console already reads, and a field it does not know is a field a strict
	// reader refuses. The text output names it instead.
	RigorMode rigor.Mode `json:"-"`
	// PerStageCapUSD is agent.max_budget_usd; null when no checkout here
	// named one.
	PerStageCapUSD *float64 `json:"per_stage_cap_usd"`
	// PerRunCapUSD is the smallest per-run cap found, null when none is
	// set; PerRunCapSource names what was found.
	PerRunCapUSD    *float64 `json:"per_run_cap_usd"`
	PerRunCapSource []string `json:"per_run_cap_source"`
	// Bounded is false when nothing caps a run: then PerRunUSD, CeilingUSD
	// and Verdict are null or empty.
	Bounded    bool     `json:"bounded"`
	PerRunUSD  *float64 `json:"per_run_usd"`
	CeilingUSD *float64 `json:"ceiling_usd"`
	// BindingOrigin names what the per-run cap that binds comes from: repo,
	// default or global for the database's, else the variable that set it
	// (the text output names it beside the cap; PerRunCapSource already
	// carries it for --json).
	BindingOrigin string `json:"binding_origin,omitempty"`
	// Verdict is fits, may_exceed_repo or exceeds; "" when no daily cap
	// applies.
	Verdict  string `json:"verdict,omitempty"`
	Headroom struct {
		RepoUSD   *float64 `json:"repo_usd"`
		GlobalUSD *float64 `json:"global_usd"`
	} `json:"headroom"`
	// ComputeCeiling is what a run of each workflow can bill for its machine
	// over its own clock, which no cap covers (design artifact-runs.md §6,
	// D4). It is empty when there is no checkout here, since the figures are
	// the workflow's.
	ComputeCeiling []previewCompute `json:"compute_ceiling"`
	// Typical is what a run of the recipe typically costs, from the cost
	// the repository's recorded runs gave each stage; null when the window
	// holds fewer than previewMinRuns of them.
	Typical *previewTypical `json:"typical"`
	Notes   []string        `json:"notes"`
}

// previewTypical is the history figure: the mean recorded cost of each stage
// kind, at the recipe's typical stage count. Its runs are the real-dollar
// ones only; Skipped says how many the figure left out, and the three fields
// under it why, each counted once and never as a run that cost nothing.
type previewTypical struct {
	USD    float64 `json:"usd"`
	Stages int     `json:"stages"`
	// Runs is how many of the window's runs recorded a per-stage cost: the
	// history this figure was taken from.
	Runs int `json:"runs"`
	// Skipped is the runs the figure left out, the sum of the three below.
	Skipped    int `json:"skipped"`
	Notional   int `json:"notional"`   // runs whose model_usd was a subscription's: oauth, or a cascade run the plan served in full
	Unreadable int `json:"unreadable"` // runs with no readable result.json
	Unpriced   int `json:"unpriced"`   // runs whose record held no per-stage cost
}

func runBudgetPreview(cmd *cobra.Command, o *budgetPreviewOptions) error {
	ctx := cmd.Context()
	switch {
	case o.runs <= 0:
		return userErr("--runs must be more than 0")
	case o.runs > previewMaxRuns:
		return userErr("--runs %d is more than %d runs; estimate a batch, not a project's lifetime", o.runs, previewMaxRuns)
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	repo := o.repo
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return userErr("--repo: %v", err)
	}
	cfg := checkoutConfig(ctx, repo)
	root := checkoutRoot(ctx, repo)
	// A project recipe lives in the runs bucket, and reading the bucket is
	// not everyone's to do (init removes the plain Viewer's read). Open it
	// only when the project layer could hold this recipe at all: not when
	// its name is not the default one (projectRecipesNote), and not when the
	// repository's own checkout already holds it, since the repository layer
	// resolves first.
	env := &cloudEnv{lc: lc}
	if projectRecipesNote(lc) == "" && !repoHasRecipe(root, o.recipe) {
		if env, err = openCloudFor(ctx, lc); err != nil {
			return err
		}
		defer env.Close()
	}
	slug, err := env.repoSlug(repo, func() *config.Config { return cfg })
	if err != nil {
		return err
	}
	rr, err := resolveRecipe(ctx, env, root, o.recipe, budgetNow(), false)
	if err != nil {
		return err
	}

	var project rigor.Config
	if cfg == nil {
		project = previewProjectRigor(ctx, lc)
	}
	knobs := previewKnobsOf(cfg, project)
	stages := recipe.StageBound(rr.Recipe, knobs.first, knobs.rounds)
	doc := &previewDoc{Project: lc.Name, GCPProject: lc.GCPProject, Repo: repo, Slug: slug,
		Recipe: rr.Name, Source: string(rr.Source), Runs: o.runs, Stages: stages,
		KnobsFrom: knobs.from, PerRunCapSource: []string{}, Notes: []string{}}
	doc.Knobs.FirstLineRounds, doc.Knobs.ReviewRounds = knobs.first, knobs.rounds
	doc.RigorMode = knobs.mode
	// resolveRecipe's note says where the stages come from when the recipe
	// was not simply found: a stale cached project recipe standing in for an
	// unreachable bucket, or the project layer skipped. run prints it for the
	// same reason (printRecipeNote), and here it explains the figures.
	if rr.Note != "" {
		doc.Notes = append(doc.Notes, rr.Note)
	}
	// The ceiling's other half: the machine. No cap covers compute (D4), so
	// the preview prices the worst case a run of each workflow can bill at
	// the installation's own compute prices, beside the model ceiling rather
	// than inside it.
	doc.ComputeCeiling = previewComputeCeilings(cfg, env.prices(nil)(nil), lc.Region)
	if knobs.maxStageUSD > 0 {
		v := knobs.maxStageUSD
		doc.PerStageCapUSD = &v
	} else {
		doc.Notes = append(doc.Notes, fmt.Sprintf("no checkout of %s here: agent.max_budget_usd is unknown and the recipe's rounds are mode %s's defaults (agent.first_line_rounds %d, agent.review_rounds %d)", repo, knobs.mode, knobs.first, knobs.rounds))
	}

	// Today's caps and counters, read as budget show reads them.
	var dbCfg dbConfig
	if _, err := db.Get(ctx, "config", &dbCfg); err != nil {
		return dbErr(lc, err, false)
	}
	now := budgetNow()
	if sn, ok := db.ServerNow(); ok {
		now = sn
	}
	day := budget.Day(now)
	var global budget.Counters
	if _, err := db.Get(ctx, budget.PathSpendGlobal(day), &global); err != nil {
		return dbErr(lc, err, false)
	}
	spend := map[string]budget.Counters{}
	if _, err := db.Get(ctx, "spend/"+budget.DayKey(day)+"/repos", &spend); err != nil {
		return dbErr(lc, err, false)
	}
	caps := budget.Caps{Global: dbCfg.Caps.Global, Defaults: dbCfg.Caps.Defaults, Limits: dbCfg.Limits, Mode: dbCfg.Mode}
	if c, ok := dbCfg.Caps.Repos[budget.Key(slug)]; ok {
		caps.Repo = &c
	}
	repoHR := previewHeadroom{}
	if v, ok := caps.RepoDaily(); ok {
		repoHR = previewHeadroom{usd: max(v-spend[budget.Key(slug)].Counted, 0).USD(), known: true}
	}
	globalHR := previewHeadroom{}
	if v, ok := caps.GlobalDaily(); ok {
		globalHR = previewHeadroom{usd: max(v-global.Counted, 0).USD(), known: true}
	}
	doc.Headroom.RepoUSD, doc.Headroom.GlobalUSD = headroomPtr(repoHR), headroomPtr(globalHR)

	// The ceiling: N × the smaller side of a run's bound.
	var perRunUSD *float64
	if m, srcs, origin, ok, err := previewPerRun(lc, caps); err != nil {
		return err
	} else if ok {
		v := m.USD()
		perRunUSD, doc.PerRunCapUSD, doc.PerRunCapSource, doc.BindingOrigin = &v, &v, srcs, origin
	}
	stageUSD := doc.stageUSD()
	var used, ceiling *float64
	switch {
	case perRunUSD != nil && (stageUSD == nil || *perRunUSD <= *stageUSD):
		used, ceiling = perRunUSD, ptr(*perRunUSD*float64(o.runs))
	case stageUSD != nil:
		used, ceiling = stageUSD, ptr(*stageUSD*float64(o.runs))
	}
	doc.Bounded, doc.PerRunUSD, doc.CeilingUSD = ceiling != nil, used, ceiling
	doc.Verdict = previewVerdict(ceiling, repoHR, globalHR)

	previewAddTypical(ctx, doc, env, lc, cfg, knobs, rr.Recipe, repo, slug)

	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	printPreview(cmd, doc)
	return nil
}

// previewAddTypical fills doc.Typical in from the repository's recorded
// runs, and notes what stopped it when it can't: the ceiling above stands
// whatever history the bucket holds. The runs bucket is the one the recipe
// resolution may already have opened; when it didn't (a repository recipe,
// or a project layer that is not read), open it here, and read no further
// than a note if that fails: a Viewer without the bucket's read still gets
// the ceiling.
func previewAddTypical(ctx context.Context, doc *previewDoc, env *cloudEnv, lc *localcfg.Config, cfg *config.Config, knobs previewKnobs, rp *recipe.Recipe, repo, slug string) {
	bucket := env.bucket
	if bucket == nil {
		opened, err := openCloudFor(ctx, lc)
		if err != nil {
			doc.Notes = append(doc.Notes, "no typical line: "+oneLine(err.Error()))
			return
		}
		defer opened.Close()
		bucket = opened.bucket
	}
	h, err := readPreviewHistory(ctx, bucket, slug, budgetNow())
	if err != nil {
		doc.Notes = append(doc.Notes, "no typical line: reading "+repo+"'s runs: "+oneLine(err.Error()))
		return
	}
	// One note per group of runs the figure left out, in a fixed order, each
	// naming its own reason: "skipped" alone would say a run was no use
	// without saying why.
	total := h.total()
	if h.notional > 0 {
		doc.Notes = append(doc.Notes, fmt.Sprintf("%d of the %d runs of %s in the last 30 days ran on a subscription (agent.auth oauth, or a cascade run the plan served in full): their model_usd is notional and is not averaged into a real-dollar figure",
			h.notional, total, repo))
	}
	if h.unreadable > 0 {
		doc.Notes = append(doc.Notes, fmt.Sprintf("%d of the %d runs of %s in the last 30 days have no readable result.json; they are not counted",
			h.unreadable, total, repo))
	}
	if h.unpriced > 0 {
		doc.Notes = append(doc.Notes, fmt.Sprintf("%d of the %d runs of %s in the last 30 days recorded no cost for any stage (a result.json from before the runner kept one, or stages that all cost nothing); they are not counted",
			h.unpriced, total, repo))
	}
	usd, stages, missing, model := h.typical(rp, cfg, knobs)
	if missing != "" {
		on := ""
		if model != "" {
			on = " on " + model
		}
		doc.Notes = append(doc.Notes, fmt.Sprintf("no typical line: no %s stage of %s%s recorded a cost in the last 30 days", missing, repo, on))
		return
	}
	if stages == 0 { // the design's "with fewer than 5 runs, the line is omitted"
		return
	}
	doc.Typical = &previewTypical{USD: usd, Stages: stages, Runs: h.runs,
		Skipped: total - h.runs, Notional: h.notional, Unreadable: h.unreadable, Unpriced: h.unpriced}
}

// previewHistory is what the repository's runs of the last 30 days say about
// the cost of one stage.
type previewHistory struct {
	// runs counts the runs the means are taken from: the ones that recorded a
	// real-dollar cost for at least one stage.
	runs int
	// unpriced counts the runs whose record was read but held no per-stage
	// cost at all (a result.json from before the runner kept one, or a run
	// whose stages all cost nothing); unreadable the runs with no readable
	// result.json; and notional the runs whose model_usd was a subscription's
	// whole (agent.auth oauth, or a cascade run the plan served in full --
	// design §10.1, C1.16/P12), which counts against the plan's limits and is
	// no one's bill. Each is counted and said, and none is ever averaged in as
	// a run that cost nothing.
	unpriced, unreadable, notional int
	// means is the mean cost of the stages seen per (stage kind, model).
	means map[previewKind]previewMean
}

// total is every run of the window the read accounted for, whichever group it
// went into.
func (h previewHistory) total() int { return h.runs + h.unpriced + h.unreadable + h.notional }

// previewKind keys a mean by a stage's kind and the model it was pinned to;
// model "" is a stage whose config pinned none (Claude Code's own default).
type previewKind struct{ kind, model string }

// previewMean is the mean cost of the stages one previewKind saw.
type previewMean struct {
	total  float64
	stages int
}

func (m previewMean) mean() float64 { return m.total / float64(m.stages) }

// readPreviewHistory reads the repository's runs of the last 30 days: the run
// IDs under the slug with runstore.ListRunIDs, as runs ls lists them, and
// then each run's result.json, read the way runs ls reads a run's objects (in
// parallel, a bounded number at a time).
//
// It is not runs ls's own loader: loadRows also reads launch.json and the
// task, joins the backend's executions and prices compute, which is three
// objects and a backend call per run for figures a stage's cost does not use.
// The read here is bounded by one repository's runs of the last 30 days, and
// it reads no object but result.json.
//
// A run it cannot price is counted, not guessed at: unreadable (no readable
// result.json), unpriced (a record with no cost for any stage), or notional
// (a run whose model figure was a subscription's whole). Only a read that
// fails for some other reason fails the whole thing.
func readPreviewHistory(ctx context.Context, b *blobx.Bucket, slug string, now time.Time) (previewHistory, error) {
	h := previewHistory{means: map[previewKind]previewMean{}}
	ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, now.Add(-previewHistoryWindow))
	if err != nil {
		return h, err
	}
	recs := make([]*runstore.Record, len(ids))
	errs := make([]error, len(ids))
	parallel(len(ids), func(i int) {
		recs[i], errs[i] = runstore.Open(b.Bucket, slug, ids[i]).ReadRecord(ctx)
	})
	for i, id := range ids {
		if err := errs[i]; err != nil {
			if errors.Is(err, runstore.ErrNotFound) || corruptObject(err) {
				h.unreadable++
				continue
			}
			return h, fmt.Errorf("run %s: %w", id, err)
		}
		rec := recs[i]
		if rec.Cost != nil && rec.Cost.AllNotional() {
			// Notional dollars: they count against the plan's limits, not
			// against a bill, and a figure printed beside real caps must not
			// mix them in (design §10.1, as runs ls's own ~ marker says). The
			// split, not the basis: a cascade run the plan served in full has
			// an api-list basis and a whole notional figure, and read off the
			// basis it would be a run whose stages all cost nothing.
			h.notional++
			continue
		}
		priced := false
		for _, st := range rec.Stages {
			// The stage's money, not its whole figure: a cascade stage's
			// tier-A dollars are the subscription's (C1.16, P12) and must not
			// be averaged into a figure printed beside real caps.
			money := st.ModelUSD - min(st.ModelNotionalUSD, st.ModelUSD)
			if money <= 0 { // omitempty: a record from before Task 24, a stage that called no model, or a notional-only stage
				continue
			}
			priced = true
			k := previewKind{kind: st.Name, model: st.Model}
			m := h.means[k]
			m.total, m.stages = m.total+money, m.stages+1
			h.means[k] = m
		}
		if priced {
			h.runs++
		} else {
			h.unpriced++
		}
	}
	return h, nil
}

// typical is what a run of rp typically costs: every stage kind the recipe
// would run at one round each (previewTypicalCounts), times the mean cost of
// that kind's recorded stages on the model the checkout pins for it, with
// the recipe's roles mapping applied as the runner applies it. A stages of 0
// means there is no figure to print; missing names the kind whose stages the
// window recorded no cost for on that model, and is "" otherwise. knobs is
// the caller's, the same ones the ceiling's stage bound uses, so the two
// figures on one line are counted the same way.
//
// Only the pinned model's own stages are averaged: a kind the window holds no
// cost for on that model has no figure rather than another model's mean,
// which would price the model the run will use with what a cheaper one cost
// (the help's promise, and the rule a missing kind already follows). The
// design's "with fewer than 5 runs, the line is omitted" and a recipe that
// runs no agent stage at all are silent: there is nothing to explain.
func (h previewHistory) typical(rp *recipe.Recipe, cfg *config.Config, knobs previewKnobs) (usd float64, stages int, missing, model string) {
	counts := previewTypicalCounts(rp, knobs.first, knobs.rounds)
	for _, n := range counts {
		stages += n
	}
	if h.runs < previewMinRuns || stages == 0 {
		return 0, 0, "", ""
	}
	pinned := previewPinnedAgent(cfg, rp)
	for _, kind := range previewStageKinds { // a fixed order, so the note names one kind
		n := counts[kind]
		if n == 0 {
			continue
		}
		model := previewPinnedModel(pinned, kind)
		m, ok := h.means[previewKind{kind: kind, model: model}]
		if !ok {
			return 0, 0, kind, model
		}
		usd += m.mean() * float64(n)
	}
	return usd, stages, "", ""
}

// previewPinnedAgent is the checkout's agent as the recipe leaves it: when
// the recipe maps reviewer to coder, or coder to reviewer, the aliased role
// takes the other's model (runner.ApplyRoles, the runner's own, which the
// runner applies before it records a stage's model), so the pin below is the
// model the runs really recorded and not an agent.models key this recipe
// ignores. nil, when there is no checkout, pins nothing.
func previewPinnedAgent(cfg *config.Config, rp *recipe.Recipe) *config.Agent {
	if cfg == nil {
		return nil
	}
	a := cfg.Agent
	if rp != nil {
		runner.ApplyRoles(&a, rp)
	}
	return &a
}

// previewPinnedModel is the model a stage kind would run on here: the
// agent's model for the kind's role (implement, fix and the first-line
// review are the coder's, the senior review the reviewer's), "" when there
// is no checkout or it pins none.
func previewPinnedModel(a *config.Agent, kind string) string {
	if a == nil {
		return ""
	}
	return a.ModelFor(config.StageRole(kind))
}

// previewStageKinds is the order a run's stages are counted and named in: the
// order the runner first records each kind in (implement, review_first, fix,
// then the senior review, with another fix after it), held fixed so that a
// note about a kind the window has no cost for always names the same one when
// several are missing.
var previewStageKinds = [...]string{"implement", "review_first", "fix", "review"}

// previewTypicalCounts is the kind of every stage a run of rp records with
// one round of each loop — the design's typical run, and the count the
// history mean is multiplied by. It mirrors the runner's loop: an implement,
// then a first line of review_first and its fix, then a senior review, its
// fix, and, with bounce, one more cheap pair. A mode: review recipe runs the
// senior review alone, and a digest recipe has no implement stage at all:
// its first step is the runner's own work, which writes the run reports with
// no model call (run-digest).
//
// first and rounds are the checkout's first_line_rounds and review_rounds (a
// step's own max_rounds overriding its knob, as recipe.StageBound takes
// them), each read as at most one round: a loop the knobs leave at 0 never
// runs, so its stages are not counted -- the sum here is StageBound(rp, f, n)
// for the same f and n, which the tests pin for every catalog recipe.
func previewTypicalCounts(rp *recipe.Recipe, first, rounds int) map[string]int {
	if rp.Mode == recipe.ModeReview {
		return map[string]int{"review": 1}
	}
	f, n, bounce := 0, 0, false
	for _, s := range rp.Steps {
		switch s.Kind {
		case recipe.StepFirstLine:
			f = roundCount(first, s.MaxRounds)
		case recipe.StepReview:
			n, bounce = roundCount(rounds, s.MaxRounds), s.Bounce
		}
	}
	cheap := 0
	if bounce && f > 0 && n > 0 {
		cheap = f // the senior review sent it back to the cheap loop once
	}
	// The fix after the first line and the fix after the senior review are one
	// kind in result.json ("fix"), so they share one mean and one count here.
	counts := map[string]int{
		"review_first": f + cheap,     // the first line, and its bounce-back
		"review":       n,             // one senior round
		"fix":          f + n + cheap, // each review's demands answered
	}
	if !recipe.UsesDigest(rp) {
		counts["implement"] = 1 // the first_line step's first stage
	}
	return counts
}

// roundCount is how many rounds one loop gets, read as a typical run's count:
// one, however many the knob and the step allow, and none when they allow
// none (maxRounds 0 defers to the knob, as recipe.StageBound reads it).
func roundCount(knob, maxRounds int) int {
	if maxRounds > 0 {
		knob = maxRounds
	}
	return min(max(knob, 0), 1)
}

// repoHasRecipe reports whether the checkout at root holds name in
// .fugaro/recipes, which resolveRecipe takes before it reaches the runs
// bucket. A name that is not a recipe name is not one either.
func repoHasRecipe(root, name string) bool {
	if root == "" || !recipe.NameRE.MatchString(name) {
		return false
	}
	_, found, err := recipe.ReadRepoFile(root, name)
	return err == nil && found
}

func ptr(v float64) *float64 { return &v }

func headroomPtr(h previewHeadroom) *float64 {
	if !h.known {
		return nil
	}
	return ptr(h.usd)
}

// previewVerdict is the verdict the ceiling gets against the headroom that
// applies. Exceeding the project's cap is the stronger statement, so it is
// the one named when the ceiling is over both.
func previewVerdict(ceiling *float64, repo, global previewHeadroom) string {
	if ceiling == nil {
		return ""
	}
	switch {
	case global.known && *ceiling > global.usd:
		return "exceeds"
	case repo.known && *ceiling > repo.usd:
		return "may_exceed_repo"
	case repo.known || global.known:
		return "fits"
	}
	return ""
}

func printPreview(cmd *cobra.Command, d *previewDoc) {
	w := cmd.OutOrStdout()
	for _, n := range d.Notes {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", oneLine(n))
	}
	fmt.Fprintf(w, "recipe %s (%s): at most %d stages (mode %s, agent.first_line_rounds %d, agent.review_rounds %d)\n",
		oneLine(d.Recipe), d.Source, d.Stages, d.RigorMode, d.Knobs.FirstLineRounds, d.Knobs.ReviewRounds)
	if !d.Bounded {
		fmt.Fprintln(w, "rough worst case: unbounded: set budget.per_run_usd (or agent.max_budget_usd in the repository's fugaro.yaml)")
	} else {
		fmt.Fprintf(w, "rough worst case: %s\n", usdFixed(*d.CeilingUSD))
		binding := "per-run cap"
		if d.BindingOrigin != "" {
			binding = "per-run cap: " + d.BindingOrigin
		}
		if stageUSD := d.stageUSD(); stageUSD != nil && (d.PerRunCapUSD == nil || *stageUSD < *d.PerRunCapUSD) {
			binding = fmt.Sprintf("%d stages × %s (agent.max_budget_usd)", d.Stages, usdFixed(*d.PerStageCapUSD))
		}
		fmt.Fprintf(w, "  %d runs × %s (%s)\n", d.Runs, usdFixed(*d.PerRunUSD), binding)
	}
	if len(d.PerRunCapSource) > 0 {
		fmt.Fprintf(w, "per-run caps found: %s; the smallest counts\n", oneLine(strings.Join(d.PerRunCapSource, ", ")))
	}
	for _, c := range d.ComputeCeiling {
		printComputeCeiling(w, c)
	}
	fmt.Fprintf(w, "headroom left today: repository %s, project %s\n",
		usdFixedPtr(d.Headroom.RepoUSD), usdFixedPtr(d.Headroom.GlobalUSD))
	switch d.Verdict {
	case "fits":
		fmt.Fprintf(w, "fits the %s (%s left today)\n", capName(d), usdFixed(*narrowerHeadroom(d)))
	case "may_exceed_repo":
		fmt.Fprintf(w, "may exceed the repository's daily cap (%s left today)\n", usdFixed(*d.Headroom.RepoUSD))
	case "exceeds":
		fmt.Fprintf(w, "exceeds the project's daily cap (%s left today)\n", usdFixed(*d.Headroom.GlobalUSD))
	}
	if t := d.Typical; t != nil {
		fmt.Fprintf(w, "typical: %s a run (%d stages, from %d runs of the last 30 days)\n",
			usdFixed(t.USD), t.Stages, t.Runs)
	}
}

// printComputeCeiling prints one workflow's compute ceiling: the figure, and
// the two things that bound it. The words NOT CAPPED are the line's point and
// not decoration -- they are D4 said out loud, because the model ceiling above
// it is the one an owner reads as "the worst case", and for a run whose
// expensive half is a render it is not.
//
// The line states the ruling but cites nothing for it. SECURITY.md does not
// carry the paragraph yet -- artifact-runs.md Task 9 writes it -- and a
// pointer to a document that does not make the claim sends a reader to a
// page that contradicts them by silence. The pointer can come back with the
// paragraph.
func printComputeCeiling(w io.Writer, c previewCompute) {
	mem := c.Memory
	if c.GiB > 0 {
		mem = fmt.Sprintf("%g GiB", c.GiB)
	}
	fig := "not priced: " + c.Unpriced
	if c.USD != nil {
		fig = usdFixed(*c.USD) + " at this installation's compute prices"
	}
	fmt.Fprintf(w, "compute ceiling (workflow %s): %d vCPU x %s x %s = %s — NOT CAPPED\n",
		oneLine(c.Workflow), c.VCPU, mem, time.Duration(c.TotalS*float64(time.Second)), fig)
	// "not by any budget cap" and not the per-run key by name: the caps are
	// per-run, repository-daily and project-daily, and compute is outside all
	// three. Naming one of them here would also read, to a test that greps a
	// command's whole output, as though that key were the one binding.
	fmt.Fprintln(w, "  bounded by timeouts.total, not by any budget cap; compute is never capped")
}

// stageUSD is what the recipe's stage bound costs at the checkout's
// per-stage cap: nil when no checkout named one (agent.max_budget_usd).
func (d *previewDoc) stageUSD() *float64 {
	if d.PerStageCapUSD == nil {
		return nil
	}
	return ptr(*d.PerStageCapUSD * float64(d.Stages))
}

// narrowerHeadroom is the smaller of the two headrooms: the cap a fitting
// ceiling actually fits.
func narrowerHeadroom(d *previewDoc) *float64 {
	if d.Headroom.RepoUSD != nil && (d.Headroom.GlobalUSD == nil || *d.Headroom.RepoUSD <= *d.Headroom.GlobalUSD) {
		return d.Headroom.RepoUSD
	}
	return d.Headroom.GlobalUSD
}

func capName(d *previewDoc) string {
	if narrowerHeadroom(d) == d.Headroom.RepoUSD {
		return "repository's daily cap"
	}
	return "project's daily cap"
}

// usdFixedPtr is usdFixed for a figure that may be absent.
func usdFixedPtr(v *float64) string {
	if v == nil {
		return "not set"
	}
	return usdFixed(*v)
}

// usdFixed is a dollar figure with both decimals: the preview's numbers are
// estimates, and "$3" next to "$3.50" reads like a different quantity.
func usdFixed(v float64) string { return fmt.Sprintf("$%.2f", v) }
