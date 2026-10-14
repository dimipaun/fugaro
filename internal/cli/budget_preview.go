package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro budget preview (design generic-tool §1.1, G20): what a batch of runs
// may cost at most, against today's caps. It is a ceiling, not a forecast:
//
//	N × min(per-run cap, stages_max(recipe) × agent.max_budget_usd)
//
// Nothing here is written and no run is started: the caps and today's
// counters are read as budget show reads them, and the recipe is resolved as
// run resolves it (the repository's .fugaro/recipes, the project's published
// recipes, the catalog).
//
// The design's second figure, the mean cost per stage over the repository's
// last 30 days of result.json, needs the runner to record a cost per stage
// (StageTiming.ModelUSD); until it does, the preview is the ceiling alone.

// previewMaxRuns bounds --runs: a larger batch is a typo, and the ceiling it
// would print means nothing.
const previewMaxRuns = 100000

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
			"The recipe resolves as it does for a run: the repository's\n" +
			".fugaro/recipes, the project's published recipes, then the catalog. The\n" +
			"knobs come from the checkout's fugaro.yaml; without a checkout of the\n" +
			"repository here, agent.max_budget_usd is unknown and only\n" +
			"budget.per_run_usd bounds the figure, so with neither the preview says it\n" +
			"is unbounded.\n\n" +
			"It reads the database's caps and today's counters (the Viewer role) and\n" +
			"writes nothing.",
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
// when there was no checkout to read one from, and from is "checkout" then.
type previewKnobs struct {
	first, rounds int
	maxStageUSD   float64
	from          string
}

func previewKnobsOf(cfg *config.Config) previewKnobs {
	if cfg == nil {
		return previewKnobs{first: config.DefaultFirstLineRounds, rounds: config.DefaultReviewRounds, from: "defaults"}
	}
	return previewKnobs{first: cfg.Agent.FirstLineRounds, rounds: cfg.Agent.ReviewRounds,
		maxStageUSD: cfg.Agent.MaxBudgetUSD, from: "checkout"}
}

// previewHeadroom is what one scope has left under its daily cap today.
type previewHeadroom struct {
	usd   float64
	known bool
}

// previewPerRun is the per-run cap a run would be held to: the smallest of
// the ceiling a job carries ($FUGARO_MAX_RUN_USD, or the project config's
// budget.per_run_usd when the variable is not set here) and the database's
// own per-run cap. ok is false when none is set, and then only the per-stage
// cap bounds a run. srcs names each one found, with its value.
func previewPerRun(lc *localcfg.Config, caps budget.Caps) (best budget.Micros, srcs []string, ok bool) {
	take := func(m budget.Micros, what string) {
		srcs = append(srcs, what+" "+usdFixed(m.USD()))
		if !ok || m < best {
			best, ok = m, true
		}
	}
	if v := os.Getenv(runner.MaxRunUSDEnv); v != "" {
		if usd, err := strconv.ParseFloat(v, 64); err == nil && usd > 0 {
			if m, err := pricing.FromUSD(usd); err == nil {
				take(m, runner.MaxRunUSDEnv)
			}
		}
	}
	if lc.Budget.PerRunUSD > 0 {
		if m, err := pricing.FromUSD(lc.Budget.PerRunUSD); err == nil {
			take(m, "budget.per_run_usd")
		}
	}
	if m, ok := caps.PerRun(); ok {
		take(m, "the database's per-run cap")
	}
	return best, srcs, ok
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
	// Verdict is fits, may_exceed_repo or exceeds; "" when no daily cap
	// applies.
	Verdict  string `json:"verdict,omitempty"`
	Headroom struct {
		RepoUSD   *float64 `json:"repo_usd"`
		GlobalUSD *float64 `json:"global_usd"`
	} `json:"headroom"`
	Notes []string `json:"notes"`
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
	// The project's recipes live in the runs bucket. It is opened only when
	// the project layer could hold one: projectRecipesNote says when it
	// cannot (a runs bucket that is not the default name).
	env := &cloudEnv{lc: lc}
	if projectRecipesNote(lc) == "" {
		if env, err = openCloudFor(ctx, lc); err != nil {
			return err
		}
		defer env.Close()
	}
	slug, err := env.repoSlug(repo, func() *config.Config { return cfg })
	if err != nil {
		return err
	}
	rr, err := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), o.recipe, budgetNow(), false)
	if err != nil {
		return err
	}

	knobs := previewKnobsOf(cfg)
	stages := recipe.StageBound(rr.Recipe, knobs.first, knobs.rounds)
	doc := &previewDoc{Project: lc.Name, GCPProject: lc.GCPProject, Repo: repo, Slug: slug,
		Recipe: rr.Name, Source: string(rr.Source), Runs: o.runs, Stages: stages,
		KnobsFrom: knobs.from, PerRunCapSource: []string{}, Notes: []string{}}
	doc.Knobs.FirstLineRounds, doc.Knobs.ReviewRounds = knobs.first, knobs.rounds
	if knobs.maxStageUSD > 0 {
		v := knobs.maxStageUSD
		doc.PerStageCapUSD = &v
	} else {
		doc.Notes = append(doc.Notes, fmt.Sprintf("no checkout of %s here: agent.max_budget_usd is unknown and the recipe's rounds are the defaults (agent.first_line_rounds %d, agent.review_rounds %d)", repo, knobs.first, knobs.rounds))
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
	var perRunUSD, stageUSD *float64
	if m, srcs, ok := previewPerRun(lc, caps); ok {
		v := m.USD()
		perRunUSD, doc.PerRunCapUSD, doc.PerRunCapSource = &v, &v, srcs
	}
	if knobs.maxStageUSD > 0 {
		v := knobs.maxStageUSD * float64(stages)
		stageUSD = &v
	}
	var used, ceiling *float64
	switch {
	case perRunUSD != nil && (stageUSD == nil || *perRunUSD <= *stageUSD):
		used, ceiling = perRunUSD, ptr(*perRunUSD*float64(o.runs))
	case stageUSD != nil:
		used, ceiling = stageUSD, ptr(*stageUSD*float64(o.runs))
	}
	doc.Bounded, doc.PerRunUSD, doc.CeilingUSD = ceiling != nil, used, ceiling
	doc.Verdict = previewVerdict(ceiling, repoHR, globalHR)

	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	printPreview(cmd, doc, doc.PerRunCapUSD, stageUSD)
	return nil
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

func printPreview(cmd *cobra.Command, d *previewDoc, perRunCap, stageUSD *float64) {
	w := cmd.OutOrStdout()
	for _, n := range d.Notes {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", oneLine(n))
	}
	fmt.Fprintf(w, "recipe %s (%s): at most %d stages (agent.first_line_rounds %d, agent.review_rounds %d)\n",
		oneLine(d.Recipe), d.Source, d.Stages, d.Knobs.FirstLineRounds, d.Knobs.ReviewRounds)
	if !d.Bounded {
		fmt.Fprintln(w, "rough worst case: unbounded: set budget.per_run_usd (or agent.max_budget_usd in the repository's fugaro.yaml)")
	} else {
		fmt.Fprintf(w, "rough worst case: %s\n", usdFixed(*d.CeilingUSD))
		binding := "per-run cap"
		if stageUSD != nil && (perRunCap == nil || *stageUSD < *perRunCap) {
			binding = fmt.Sprintf("%d stages × %s (agent.max_budget_usd)", d.Stages, usdFixed(*d.PerStageCapUSD))
		}
		fmt.Fprintf(w, "  %d runs × %s (%s)\n", d.Runs, usdFixed(*d.PerRunUSD), binding)
	}
	if len(d.PerRunCapSource) > 0 {
		fmt.Fprintf(w, "per-run caps found: %s; the smallest counts\n", oneLine(strings.Join(d.PerRunCapSource, ", ")))
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
