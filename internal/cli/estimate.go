package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// estimateDocVersion is the document's first version, and the number a member
// added later keeps.
const estimateDocVersion = 1

// estimateOptions are `fugaro estimate`'s flags.
type estimateOptions struct {
	cloud cloudOptions
	run   string
	repo  string
	since string
	model string
	pr    int
	json  bool
}

// newEstimateCmd is "fugaro estimate": what one run would have cost on another
// model. The figure reprices the run's stored token counts and says it is an
// estimate (the go-to-market plan's G1.3, F7--F9).
func newEstimateCmd() *cobra.Command {
	var o estimateOptions
	cmd := &cobra.Command{
		Use:   "estimate",
		Short: "Reprice one run's recorded tokens on another model",
		Long: "Reprice one run on another model: the token counts the run recorded, priced\n" +
			"at the named model's list rates. Nothing is run and nothing is written.\n\n" +
			"The figure is an ESTIMATE and not the run's bill: it reprices the recorded\n" +
			"tokens and nothing else, so what a repricing cannot reproduce is left out.\n" +
			"A call that passed a long-context tier was priced per call, web searches are\n" +
			"priced apart from the tokens, and a charge settled from a reservation, one\n" +
			"recorded as unreconciled and a call whose usage could not be read are not in\n" +
			"the counts at all. The estimate assumes the run would have taken the same\n" +
			"cache ratio.\n\n" +
			"The run is named with --run, or found the way `runs ls --pr` finds one: --pr N\n" +
			"over one repository (--repo, or the local config's only one) and the last\n" +
			prLookback + " unless --since says otherwise. A pull request with several runs is not\n" +
			"one figure: they are listed, and one is named with --run.\n\n" +
			"The model is a row of the price table this project prices runs from -- the\n" +
			"built-in catalog, with the owner's model_prices over it -- named by its exact\n" +
			"ID or one of that row's aliases: no prefixes, and no guess at what a nickname\n" +
			"meant. A model the table has no row for is refused with the IDs it has. The\n" +
			"date of the table the figure was priced from is printed beside it, and a\n" +
			"table past its own check window warns.\n\n" +
			"A run whose record carries no per-model token counts -- one that never\n" +
			"finalized, or one that finalized before this binary recorded them -- is\n" +
			"refused: there is nothing to reprice, and no total is estimated in its place.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runEstimate(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.run, "run", "", "the run to reprice (<run-id>, <slug>/<run-id>, or an ending)")
	f.IntVar(&o.pr, "pr", 0, "the pull request whose run to reprice (needs one repository)")
	f.StringVar(&o.repo, "repo", "", "the repository a --pr lookup reads (owner/name, or a path inside its checkout)")
	f.StringVar(&o.since, "since", prLookback, "with --pr, only runs started within this long ("+prLookback+", 36h, 90m; 0 for all)")
	f.StringVar(&o.model, "model", "", "the model to price the run's tokens at (a price table model ID)")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// estimateDoc is what `fugaro estimate --json` prints.
type estimateDoc struct {
	Version int `json:"version"`
	// Estimate is always true. The figure a reader sees elsewhere is a
	// number in a document full of the run's own recorded ones, so the
	// document says which it is rather than leaving it to the reader.
	Estimate bool   `json:"estimate"`
	Run      string `json:"run"`
	RunID    string `json:"run_id"`
	Repo     string `json:"repo,omitempty"`
	PR       int    `json:"pr,omitempty"`
	// Model is the price table row the counts were priced at.
	Model string  `json:"model"`
	USD   float64 `json:"usd"`
	// RecordedModelUSD is the model figure the run itself recorded, which is
	// not this estimate: the two are printed together so the difference is
	// the reader's to see. RecordedBasis says what that figure was -- a bill
	// (api-list) or a plan's (subscription).
	RecordedModelUSD float64 `json:"recorded_model_usd"`
	RecordedBasis    string  `json:"recorded_model_basis,omitempty"`
	// Models is the run's stored counts by the model ID the run keyed them
	// under, and what each model's counts cost at the priced model's rates.
	// Those IDs are the run's own path's and need not be price table rows:
	// only the repriced model is looked up.
	Models []estimateModelRow `json:"models"`
	// Catalog is where those rates came from and when that source was last
	// checked.
	Catalog estimateCatalog `json:"catalog"`
}

// estimateModelRow is one of the run's models, as the document carries it.
type estimateModelRow struct {
	Model        string `json:"model"`
	Input        int64  `json:"input"`
	Output       int64  `json:"output"`
	CacheRead    int64  `json:"cache_read"`
	CacheWrite5m int64  `json:"cache_write_5m"`
	CacheWrite1h int64  `json:"cache_write_1h"`
	// USD is what these counts cost at the priced model's rates, and Micros
	// the same figure in µ$: the total and the table print the unit the rows
	// were added in rather than re-rounding the float.
	USD    float64        `json:"usd"`
	Micros pricing.Micros `json:"-"`
}

// estimateCatalog is the provenance of the rates the figure was priced at: the
// source line and date of the row's own catalog when the built-in provider
// price catalog priced it (its own 30-day rule, catalog.StaleAfter), and the
// embedded Claude price table's otherwise (priceStaleAfter). Same four facts
// `budget prices` and `models ls` state, by their own name because the rule
// that decides staleness is the row's.
type estimateCatalog struct {
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	AgeDays   int    `json:"age_days"`
	Stale     bool   `json:"stale"`
}

func runEstimate(cmd *cobra.Command, o *estimateOptions) error {
	ctx := cmd.Context()
	prSet := cmd.Flags().Changed("pr")
	switch {
	case o.run != "" && prSet:
		return userErr("--run and --pr name different runs: pass one of them")
	case o.run == "" && !prSet:
		return userErr("estimate needs a run: pass --run <run-id>, or --pr N --repo owner/name")
	case o.model == "":
		return userErr("estimate needs --model: the model to reprice the run's tokens at")
	case prSet && o.pr <= 0:
		return userErr("--pr %d: use a pull request number", o.pr)
	case !prSet && cmd.Flags().Changed("repo"):
		return userErr("--repo is a --pr lookup's repository: pass it with --pr, or not at all")
	case !prSet && cmd.Flags().Changed("since"):
		return userErr("--since is a --pr lookup's window: pass it with --pr, or not at all")
	}
	// The window is parsed before anything is opened, so a --since nothing
	// can read is refused without a project, a bucket or a backend.
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	now := budgetNow()
	table, _, err := priceTable(env.lc, now)
	if err != nil {
		return userErr("%v", err)
	}
	model, ok := table.Lookup(o.model)
	if !ok {
		return userErr("--model %s: the price table has no row for it, so nothing prices these tokens; it knows %s",
			oneLine(o.model), strings.Join(estimateKnownModels(table), ", "))
	}
	var objs runObjects
	if prSet {
		objs, err = estimateRunOnPR(ctx, cmd, env, o, since)
	} else if slug, id, rerr := resolveRun(ctx, env, o.run, resolveRunOpts(cmd, o.json)); rerr != nil {
		err = rerr
	} else {
		objs, err = readRun(ctx, env, slug, id)
	}
	if err != nil {
		return err
	}
	ref := objs.in.Slug + "/" + objs.in.RunID
	if objs.in.Problem != "" {
		return userErr("run %s: %s", ref, objs.in.Problem)
	}
	rec := objs.in.Record
	if rec == nil || rec.Cost == nil || len(rec.Cost.ModelTokens) == 0 {
		return userErr("run %s recorded no tokens by model to reprice: it never finalized, or it finalized before this binary recorded them", ref)
	}
	doc := estimateDoc{
		Version: estimateDocVersion, Estimate: true,
		Run: ref, RunID: objs.in.RunID, Repo: estimateRepo(objs), PR: estimatePR(objs, rec),
		Model: model.ID, RecordedModelUSD: rec.Cost.ModelUSD, RecordedBasis: rec.Cost.ModelBasis,
		Models:  []estimateModelRow{},
		Catalog: estimateCatalogOf(table, model, now),
	}
	var total pricing.Micros
	for _, id := range slices.Sorted(maps.Keys(rec.Cost.ModelTokens)) {
		c := rec.Cost.ModelTokens[id]
		// The run's own model ID is never looked up: what is repriced is the
		// usage the run recorded, at the model the reader named (F8).
		usd := model.Rates.Cost(pricing.Usage{
			Input: c.Input, Output: c.Output, CacheRead: c.CacheRead,
			CacheWrite5m: c.CacheWrite5m, CacheWrite1h: c.CacheWrite1h,
		})
		total += usd
		doc.Models = append(doc.Models, estimateModelRow{
			Model: id, Input: c.Input, Output: c.Output, CacheRead: c.CacheRead,
			CacheWrite5m: c.CacheWrite5m, CacheWrite1h: c.CacheWrite1h,
			USD: usd.USD(), Micros: usd,
		})
	}
	doc.USD = total.USD()
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return printEstimate(cmd, doc, rec.Cost, model)
}

// estimateRunOnPR is the one run a repository has on a pull request in the
// window, read the way `runs ls --pr` reads one: its task or its record places
// it on the pull request, and several runs are listed rather than chosen among
// (F16).
func estimateRunOnPR(ctx context.Context, cmd *cobra.Command, env *cloudEnv, o *estimateOptions, since time.Duration) (runObjects, error) {
	repo, err := repoArg(ctx, o.cloud, o.repo)
	if err != nil {
		return runObjects{}, err
	}
	if o.repo == "" && len(env.lc.Repos) != 1 {
		return runObjects{}, userErr("estimate --pr needs --repo: the local config lists %d repositories", len(env.lc.Repos))
	}
	slugs, err := lsSlugs(ctx, env, &lsOptions{cloud: o.cloud, repo: repo}, cmd.ErrOrStderr())
	if err != nil {
		return runObjects{}, err
	}
	if len(slugs) != 1 {
		return runObjects{}, userErr("estimate --pr needs --repo: %d repositories to list", len(slugs))
	}
	var from time.Time
	if since > 0 {
		from = lsNow().UTC().Add(-since)
	}
	ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slugs[0], from)
	if err != nil {
		return runObjects{}, remote(err)
	}
	runs := make([]runObjects, len(ids))
	errs := make([]error, len(ids))
	parallel(len(ids), func(i int) { runs[i], errs[i] = readRun(ctx, env, slugs[0], ids[i]) })
	if err := errors.Join(errs...); err != nil {
		return runObjects{}, remote(err)
	}
	on := slices.DeleteFunc(runs, func(r runObjects) bool { return !onPR(r.in, o.pr) })
	switch len(on) {
	case 0:
		return runObjects{}, userErr("no run of %s is on pull request %d: pass --since to widen the window, or --run <run-id>", slugs[0], o.pr)
	case 1:
		return on[0], nil
	}
	cands := make([]runCandidate, 0, len(on))
	for _, r := range on {
		c := runCandidate{slug: r.in.Slug, runID: r.in.RunID, title: "-"}
		if r.in.Task != nil && r.in.Task.Title != "" {
			c.title = cellOneLine(r.in.Task.Title)
		}
		cands = append(cands, c)
	}
	return runObjects{}, userErr("%d runs of %s are on pull request %d:\n%spass the one to reprice with --run", len(on), slugs[0], o.pr, fullRows(cands))
}

// estimateRepo is the repository the run ran on, as its own task recorded it
// (owner/name): a run whose task.json is unreadable has none to name.
func estimateRepo(objs runObjects) string {
	if objs.in.Task == nil {
		return ""
	}
	return objs.in.Task.Repo
}

// estimatePR is the pull request the run itself names: its task's, else its
// record's. 0 for a run on none.
func estimatePR(objs runObjects, rec *runstore.Record) int {
	if objs.in.Task != nil && objs.in.Task.PR != 0 {
		return objs.in.Task.PR
	}
	if rec.PR != nil {
		return rec.PR.Number
	}
	return 0
}

// estimateKnownModels are the IDs a refusal offers: the table's rows, sorted,
// under the name the table keys them by. An alias is another spelling of one
// of these and not a row of its own, so the list stays the short one a reader
// picks from (F7).
func estimateKnownModels(table *pricing.Table) []string {
	return slices.Sorted(maps.Keys(table.Models))
}

// estimateCatalogOf reads the provenance of the row that priced the figure:
// the row's own catalog source and date when the built-in provider price
// catalog priced it, the embedded Claude table's otherwise. An unreadable date
// leaves the age at 0 and nothing stale, as `budget prices` leaves it rather
// than failing the surface over a line of its own output.
func estimateCatalogOf(table *pricing.Table, m pricing.Model, now time.Time) estimateCatalog {
	if c := m.Catalog; c != nil {
		out := estimateCatalog{Source: c.Source, CheckedAt: c.CheckedAt, Stale: c.Stale}
		if t, err := time.Parse("2006-01-02", c.CheckedAt); err == nil {
			out.AgeDays = int(now.Sub(t).Hours() / 24)
		}
		return out
	}
	out := estimateCatalog{Source: table.Source, CheckedAt: table.CheckedAt}
	if t, err := time.Parse("2006-01-02", table.CheckedAt); err == nil {
		age := now.Sub(t)
		out.AgeDays = int(age.Hours() / 24)
		out.Stale = age > priceStaleAfter
	}
	return out
}

// printEstimate writes the human reading: the figure and the run it is about,
// the counts that priced it, the table's own date, and what the figure leaves
// out.
func printEstimate(cmd *cobra.Command, doc estimateDoc, cost *runstore.Cost, model pricing.Model) error {
	out := cmd.OutOrStdout()
	var total pricing.Micros
	for _, r := range doc.Models {
		total += r.Micros
	}
	if doc.PR != 0 {
		fmt.Fprintf(out, "%s  %s repriced at %s (PR %d)\n", money(total), doc.Run, doc.Model, doc.PR)
	} else {
		fmt.Fprintf(out, "%s  %s repriced at %s\n", money(total), doc.Run, doc.Model)
	}
	if cost.ModelUSD > 0 {
		usd, _ := pricing.FromUSD(cost.ModelUSD)
		basis := ""
		if cost.ModelBasis != "" {
			basis = " (" + cost.ModelBasis + ")"
		}
		fmt.Fprintf(out, "        the run recorded %s%s\n", money(usd), basis)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "MODEL\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE 5M\tCACHE WRITE 1H\tUSD")
	for _, r := range doc.Models {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\n", cellOneLine(r.Model), r.Input, r.Output, r.CacheRead, r.CacheWrite5m, r.CacheWrite1h, money(r.Micros))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nprices: %s, checked %s (%d days ago)\n", oneLine(doc.Catalog.Source), doc.Catalog.CheckedAt, doc.Catalog.AgeDays)
	fmt.Fprintf(out, "note: an estimate, not the run's bill: it prices the run's recorded tokens at %s's list rates,\n"+
		"assuming the same cache ratio; a re-run takes other token counts and retries, and long-context\n"+
		"tiers and web searches are priced apart per call and are not in it.\n", doc.Model)
	estimateStaleWarning(cmd, doc, model)
	return nil
}

// estimateStaleWarning says when the rates the figure was priced at are past
// their own check window. The row's rule decides the sentence, as it does in
// `budget prices` (D10): a catalog-priced row is stale at 30 days and is
// charged the dearest end of its tier until it is regenerated, and the
// embedded Claude table at 90.
func estimateStaleWarning(cmd *cobra.Command, doc estimateDoc, model pricing.Model) {
	errw := cmd.ErrOrStderr()
	if c := model.Catalog; c != nil {
		if !c.Stale {
			return
		}
		fmt.Fprintf(errw, "warning: the built-in provider price catalog (%s) was last checked %s, older than 30 days (%d days ago); this estimate is priced at the dearest end of %s's tier until it is regenerated\n",
			oneLine(c.Source), c.CheckedAt, doc.Catalog.AgeDays, oneLine(model.ID))
		return
	}
	if doc.Catalog.Stale {
		fmt.Fprintf(errw, "warning: the built-in price table was last checked %s, older than 90 days (%d days ago); check %s and set model_prices in the project config where it differs\n",
			doc.Catalog.CheckedAt, doc.Catalog.AgeDays, oneLine(doc.Catalog.Source))
	}
}
