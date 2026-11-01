package cli

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
)

type modelsLsOptions struct {
	cloud cloudOptions
	json  bool
}

func newModelsLsCmd() *cobra.Command {
	var o modelsLsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Show this project's routes, its models and what each repository runs",
		Long: "Show, offline, what this project can run.\n\n" +
			"ROUTES is the owner's providers in the project config — the host, the models\n" +
			"each claims, its route_fee_pct and the repositories whose code may be sent to\n" +
			"it — beside the direct Anthropic route. MODELS is every model a run can name\n" +
			"today (a route's claim, a model_prices key, a budget.allowed_models entry, a\n" +
			"repository's pin), with the route that serves it, where its price comes from\n" +
			"and what it costs. ALLOW-LISTS is which repositories each model may be sent\n" +
			"to. USAGE is each recorded repository's pins, read out of the fugaro.yaml at\n" +
			"the base branch it runs against.\n\n" +
			"A repository this machine has no checkout for is unknown, never guessed at.\n" +
			"The command reaches no bucket, no database and no host.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runModelsLs(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// modelsLsDocVersion is the document's first version, and the number a member
// added later keeps: schemas/models-ls.schema.json pins it.
const modelsLsDocVersion = 1

// The price sources a model row may name, in the words the document prints:
// this binary's own Claude table, the built-in provider price catalog, the
// owner's model_prices, and nothing at all. The schema's enum is these four
// and nothing else, which is why they are constants rather than literals.
const (
	modelsSourceEmbedded = "embedded"
	modelsSourceCatalog  = "catalog"
	modelsSourceLocal    = "local"
	modelsSourceNone     = "none"
)

// modelsLsDoc is what `fugaro models ls --json` prints, under
// schemas/models-ls.schema.json (D784).
type modelsLsDoc struct {
	Version int `json:"version"`
	// Project is the selected project's name; "" when no project config is
	// selected, which leaves Routes with the direct route alone.
	Project string         `json:"project,omitempty"`
	Catalog *modelsCatalog `json:"catalog,omitempty"`
	Routes  []modelsRoute  `json:"routes"`
	Models  []modelsModel  `json:"models"`
	Repos   []modelsRepo   `json:"repositories"`
}

// modelsCatalog is the built-in provider price catalog's own provenance: the
// rows that carry it are priced from it, and it has its own source, date and
// 30-day staleness rule (design D10).
type modelsCatalog struct {
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	AgeDays   int    `json:"age_days"`
	Stale     bool   `json:"stale"`
}

// modelsRoute is one route a model may go down: a provider entry of the
// project config, or the direct Anthropic route.
type modelsRoute struct {
	Name string `json:"name"`
	// Kind and BaseURL are the provider entry's own; the direct route has
	// Kind "anthropic" and the Anthropic API's own base.
	Kind    string `json:"kind,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	// Host is the host of BaseURL: what a reader recognises the route by.
	Host   string   `json:"host,omitempty"`
	Models []string `json:"models"`
	// FeePct is route_fee_pct: what is added to every charge on the route.
	FeePct float64 `json:"route_fee_pct,omitempty"`
	// AllowDataTo is the repositories whose code may be sent here, as
	// written, or ["*"] for a route open to every repository.
	AllowDataTo []string `json:"allow_data_to"`
	OpenToAll   bool     `json:"open_to_every_repo"`
	// Note says what the entry's own keys do not, such as the switch that
	// routes a Claude ID here.
	Note string `json:"note,omitempty"`
}

// modelsModel is one model a run can name.
type modelsModel struct {
	ID    string `json:"id"`
	Route string `json:"route"`
	// Source is where the price comes from: catalog, local (the owner's
	// model_prices), embedded (this binary's Claude table) or none (the
	// model has no row at all).
	Source     string  `json:"source"`
	InputPerM  float64 `json:"input_per_m,omitempty"`
	OutputPerM float64 `json:"output_per_m,omitempty"`
	// Catalog is a catalog-priced row's own provenance.
	Catalog *priceCatalog `json:"catalog,omitempty"`
	// AllowedTo is the repositories this model may be used by, [] when none
	// may: a model no route serves, or one the project's allowed_models
	// leaves out.
	AllowedTo []string `json:"allowed_to"`
	// Refused is why the model has no price, when it has none.
	Refused string `json:"refused,omitempty"`
	// Note is the row's spread or its price's own qualification.
	Note string `json:"note,omitempty"`
}

// modelsRepo is one recorded repository's pins, read from the fugaro.yaml at
// the base branch it runs against.
type modelsRepo struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Coder string `json:"coder,omitempty"`
	// Reviewer is the senior review's model.
	Reviewer string `json:"reviewer,omitempty"`
	// Background is the file's own agent.models.background, and
	// BackgroundEffective the model Claude Code's background requests
	// actually reach (config.EffectiveBackground) when the two differ.
	Background          string   `json:"background,omitempty"`
	BackgroundEffective string   `json:"background_effective,omitempty"`
	Problems            []string `json:"problems,omitempty"`
}

func runModelsLs(cmd *cobra.Command, o *modelsLsOptions) error {
	lc, err := pricesConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	table, ov, err := priceTable(lc, budgetNow())
	if err != nil {
		return err
	}
	rows := priceRowsFor(table, ov)
	repos, err := modelsUsage(cmd.Context(), lc, table)
	if err != nil {
		return err
	}
	doc := modelsLsDoc{
		Version: modelsLsDocVersion,
		Catalog: modelsCatalogOf(rows),
		Routes:  modelsRouteRows(lc),
		Models:  modelsModelRows(lc, table, rows, repos),
		Repos:   repos,
	}
	if lc != nil {
		doc.Project = lc.Name
	}
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return writeModelsLs(cmd, lc, doc)
}

// modelsCatalogOf is the catalog provenance the models carry, taken from the
// first catalog-priced row: every row priced from the catalog carries the same
// source, date and staleness, and one header line states them for all of them.
func modelsCatalogOf(rows []priceRow) *modelsCatalog {
	for _, r := range rows {
		c := r.Catalog
		if c == nil {
			continue
		}
		doc := &modelsCatalog{Source: c.Source, CheckedAt: c.CheckedAt, Stale: c.Stale}
		if t, err := time.Parse("2006-01-02", c.CheckedAt); err == nil {
			doc.AgeDays = int(budgetNow().Sub(t).Hours() / 24)
		}
		return doc
	}
	return nil
}

// directRoute is the Anthropic API itself: the route every Claude ID takes
// unless the switch sends it elsewhere. Its claims are the embedded table's
// models, which is every Claude ID this binary can price, and it is open to
// every repository: no allow_data_to governs a call to Anthropic, whose
// credential the owner stores for the project rather than per repository.
func directRoute(lc *localcfg.Config) modelsRoute {
	embedded := pricing.Embedded()
	r := modelsRoute{
		Name:        config.RouteDirect,
		Kind:        "anthropic",
		BaseURL:     "https://api.anthropic.com",
		Host:        "api.anthropic.com",
		Models:      slices.Sorted(maps.Keys(embedded.Models)),
		AllowDataTo: []string{"*"},
		OpenToAll:   true,
	}
	if lc != nil && lc.Routing != nil && lc.Routing.Claude != nil && len(lc.Routing.Claude.Models) > 0 {
		r.Note = fmt.Sprintf("routing.claude.via: %s for %s", lc.Routing.Claude.ViaValue(), strings.Join(lc.Routing.Claude.Models, ", "))
	}
	return r
}

// modelsRouteRows is every route of the selected project: the direct route
// first, then the owner's providers in name order.
func modelsRouteRows(lc *localcfg.Config) []modelsRoute {
	rows := []modelsRoute{directRoute(lc)}
	if lc == nil {
		return rows
	}
	for _, name := range sortedProviderNames(lc.Providers) {
		p := lc.Providers[name]
		row := modelsRoute{
			Name: name, Kind: p.Kind, BaseURL: p.BaseURL, Host: hostOf(p.BaseURL),
			Models: slices.Clone(p.Models), FeePct: p.RouteFeePct,
			AllowDataTo: slices.Clone(p.AllowDataTo), OpenToAll: p.OpenToEveryRepo(),
		}
		if row.OpenToAll {
			row.AllowDataTo = []string{"*"}
		}
		if row.Models == nil {
			row.Models = []string{}
		}
		if row.AllowDataTo == nil {
			row.AllowDataTo = []string{}
		}
		if p.RequireZDROn() {
			row.Note = "require_zdr: prices the ZDR-eligible endpoints"
		}
		rows = append(rows, row)
	}
	return rows
}

// hostOf is the host of a route's base_url, or the URL itself when it does
// not parse: a reader recognises a route by its host, and a base_url that
// names none is shown as written rather than blanked.
func hostOf(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return baseURL
	}
	return u.Hostname()
}

// modelsModelRows is every model a run can name today, in ID order: the rows
// of the price table (the same rows `budget prices` prints, D794), the exact
// IDs a route claims, the owner's model_prices keys, the project's
// allowed_models entries and every model a recorded repository pins. A model
// with no price row is still listed, with the reason it has none.
func modelsModelRows(lc *localcfg.Config, table *pricing.Table, rows []priceRow, repos []modelsRepo) []modelsModel {
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if lc != nil {
		for _, name := range sortedProviderNames(lc.Providers) {
			for _, m := range lc.Providers[name].Models {
				if !strings.Contains(m, "*") {
					ids[m] = true
				}
			}
		}
		for m := range lc.ModelPrices {
			ids[m] = true
		}
		if lc.Budget != nil {
			for _, m := range lc.Budget.AllowedModels {
				ids[m] = true
			}
		}
	}
	for _, r := range repos {
		for _, m := range []string{r.Coder, r.Reviewer, r.Background, r.BackgroundEffective} {
			if m != "" {
				ids[m] = true
			}
		}
	}
	priced := map[string]priceRow{}
	for _, r := range rows {
		priced[r.ID] = r
	}
	out := make([]modelsModel, 0, len(ids))
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		out = append(out, modelsModelRow(lc, table, id, priced[id]))
	}
	return out
}

// modelsModelRow is one model's row: the route a run would send it down, the
// price the table holds for it (or why it holds none), and the repositories
// the project's own rules open it to.
func modelsModelRow(lc *localcfg.Config, table *pricing.Table, id string, row priceRow) modelsModel {
	m := modelsModel{ID: id, Route: modelsRouteOf(lc, id), AllowedTo: modelsAllowedTo(lc, id)}
	switch {
	case row.Local:
		m.Source = modelsSourceLocal
	case row.Catalog != nil:
		m.Source = modelsSourceCatalog
		m.Catalog = row.Catalog
		if entry, ok := table.Lookup(id); ok {
			m.Note = entry.CatalogRange()
		}
	case row.ID != "":
		m.Source = modelsSourceEmbedded
	default:
		m.Source = modelsSourceNone
		m.Refused = table.Refusal(id)
	}
	m.InputPerM, m.OutputPerM = row.InputPerM, row.OutputPerM
	if m.AllowedTo == nil {
		m.AllowedTo = []string{}
	}
	return m
}

// modelsAllowedTo is the repositories a model may be used by: [] when nothing
// serves it, [] when the project's budget.allowed_models is set and leaves it
// out, every repository on the direct route (no allow_data_to governs a call
// to Anthropic), and otherwise the serving route's own allow_data_to, with
// ["*"] for a route open to every repository.
func modelsAllowedTo(lc *localcfg.Config, id string) []string {
	route := modelsRouteOf(lc, id)
	if route == "" {
		return []string{}
	}
	if lc != nil && lc.Budget != nil && len(lc.Budget.AllowedModels) > 0 && !slices.Contains(lc.Budget.AllowedModels, id) {
		return []string{}
	}
	if route == config.RouteDirect {
		return []string{"*"}
	}
	p, ok := lc.Providers[route]
	if !ok {
		return []string{}
	}
	if p.OpenToEveryRepo() {
		return []string{"*"}
	}
	return slices.Clone(p.AllowDataTo)
}

// modelsUsage resolves each repository the project records, out of the
// checkout this machine remembers for it. A repository with no checkout here
// is unknown rather than guessed at (D793), and one whose checkout does not
// hold the ref its base branch names is unread, with the reason.
func modelsUsage(ctx context.Context, lc *localcfg.Config, table *pricing.Table) ([]modelsRepo, error) {
	out := []modelsRepo{}
	if lc == nil || len(lc.Repos) == 0 {
		return out, nil
	}
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		checkouts = localcfg.EmptyCheckouts()
	}
	remembered := map[string]string{}
	for _, c := range checkouts.ProjectCheckouts(lc.Name) {
		remembered[localcfg.CheckoutIdentity{Provider: c.Provider, Repo: c.Repo}.Key()] = checkoutPath(c)
	}
	layer := modelsLayer(lc.Name)
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		row := modelsRepo{Name: name, State: stateUnknown}
		dir := remembered[checkoutKeyFor(name, remembered)]
		if dir == "" {
			out = append(out, row)
			continue
		}
		data, err := readFugaroYAMLAtRef(ctx, dir, modelsBaseBranch(lc, name, dir))
		if err != nil {
			row.State = unreadState(err.Error())
			out = append(out, row)
			continue
		}
		cfg, _, ps := config.Resolve(data, layer)
		if len(ps) > 0 {
			row.State = unreadState(ps[0].String())
			out = append(out, row)
			continue
		}
		row.State = stateOK
		row.Coder = cfg.Agent.ModelFor(config.RoleCoder)
		row.Reviewer = cfg.Agent.ModelFor(config.RoleReviewer)
		row.Background = cfg.Agent.Models.Background
		if bg := config.EffectiveBackground(cfg.Agent, lc.Providers); bg != row.Background {
			row.BackgroundEffective = bg
		}
		var allowed []string
		if cfg.Budget != nil {
			allowed = cfg.Budget.AllowedModels
		}
		for _, p := range config.CheckProviderPolicy(cfg.Agent, allowed, name, lc.Providers, table) {
			row.Problems = append(row.Problems, p.String())
		}
		out = append(out, row)
	}
	return out, nil
}

// modelsBaseBranch is the ref the usage read reads a repository's fugaro.yaml
// at, through the one function every layer resolves through
// (config.EffectiveBaseBranch): the checkout's working-tree file when it
// writes git.base_branch, else the local config's repos.<r>.base_branch, else
// the project's own base_branch, else main. The published policy's base_branch
// is not consulted — reading it is a cloud read, and this command reaches
// nothing — so a project whose default lives only there reads as main here.
func modelsBaseBranch(lc *localcfg.Config, name, dir string) string {
	file, fileSets := "", false
	if data, err := readFugaroYAML(filepath.Join(dir, "fugaro.yaml")); err == nil && config.SetsGitBaseBranch(data) {
		if cfg, ps := config.Parse(data); len(ps) == 0 {
			file, fileSets = cfg.Git.BaseBranch, true
		}
	}
	repoBranch, projectBranch := "", lc.BaseBranch
	if r, ok := lc.Repos[name]; ok {
		repoBranch = r.BaseBranch
	}
	branch, _ := config.EffectiveBaseBranch(file, fileSets, repoBranch, projectBranch)
	return branch
}

// modelsLayer is the project layer the last cloud read cached, or nil when
// there is none: a repository's file resolves over it exactly as a run's does,
// and a machine that has never read the layer resolves the file alone.
func modelsLayer(project string) *config.ProjectLayer {
	e, ok := localcfg.LoadLayerCache(os.Getenv, project)
	if !ok {
		return nil
	}
	l, ps := config.ParseProjectLayer([]byte(e.YAML), config.LayerAnchor{Project: project, GCPProject: e.GCPProject})
	if len(ps) > 0 {
		return nil
	}
	return l
}

// writeModelsLs is the read's text form: the four sections, in the order a
// reader asks the questions.
func writeModelsLs(cmd *cobra.Command, lc *localcfg.Config, doc modelsLsDoc) error {
	w := cmd.OutOrStdout()
	errw := cmd.ErrOrStderr()
	if doc.Project == "" {
		fmt.Fprintln(w, "models: no project config selected, so the built-in prices and the direct route only")
	} else {
		fmt.Fprintf(w, "models for project %s (offline)\n", oneLine(doc.Project))
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "ROUTES")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tMODELS\tFEE\tALLOW DATA TO\tNOTES")
	for _, r := range doc.Routes {
		allow := strings.Join(r.AllowDataTo, ", ")
		if r.OpenToAll {
			allow = "every repository"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(r.Name), oneLine(r.Host),
			oneLine(routeModelsCell(r)), feeCell(r.FeePct), oneLine(allow), oneLine(r.Note))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	if doc.Catalog != nil {
		fmt.Fprintf(w, "provider price catalog: %s, checked %s (%d days ago)\n", doc.Catalog.Source, doc.Catalog.CheckedAt, doc.Catalog.AgeDays)
		if doc.Catalog.Stale {
			fmt.Fprintf(errw, "warning: the built-in provider price catalog (%s) was last checked %s, older than 30 days (%d days ago); regenerate it with scripts/gen-price-catalog.sh and commit the result (catalog-priced models are charged the dearest end of their tier's range until then)\n",
				doc.Catalog.Source, doc.Catalog.CheckedAt, doc.Catalog.AgeDays)
		}
	}
	fmt.Fprintln(w, "MODELS")
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tROUTE\tSOURCE\tINPUT\tOUTPUT\tNOTES")
	for _, m := range doc.Models {
		route := m.Route
		if route == "" {
			route = "no route"
		}
		notes := m.Note
		if m.Refused != "" {
			notes = m.Refused
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(m.ID), oneLine(route), m.Source,
			priceCell(m.InputPerM, m.Source), priceCell(m.OutputPerM, m.Source), oneLine(notes))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "ALLOW-LISTS")
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tALLOWED TO")
	for _, m := range doc.Models {
		allow := "no repository"
		if len(m.AllowedTo) > 0 {
			allow = strings.Join(m.AllowedTo, ", ")
			if slices.Contains(m.AllowedTo, "*") {
				allow = "every repository"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\n", oneLine(m.ID), oneLine(allow))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "USAGE")
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REPOSITORY\tCODER\tREVIEWER\tBACKGROUND\tSTATE\tNOTES")
	for _, r := range doc.Repos {
		background := r.Background
		if r.BackgroundEffective != "" {
			background = r.BackgroundEffective
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(r.Name), cell(r.Coder), cell(r.Reviewer),
			cell(background), oneLine(r.State), oneLine(strings.Join(r.Problems, "; ")))
	}
	return tw.Flush()
}

// routeModelsCell is the ROUTES table's MODELS cell. The direct route claims
// the whole embedded table, and six IDs there would set the width of the table
// for every other route, so it is said in words; the JSON document keeps the
// IDs themselves, which is where a consumer reads them.
func routeModelsCell(r modelsRoute) string {
	if r.Name == config.RouteDirect {
		return fmt.Sprintf("every Claude model (%d in the embedded table)", len(r.Models))
	}
	return strings.Join(r.Models, ", ")
}

// feeCell is a route's fee as a reader reads it: no fee is a dash rather than
// a row of zeroes.
func feeCell(pct float64) string {
	if pct == 0 {
		return "—"
	}
	return fmt.Sprintf("%g%%", pct)
}

// priceCell is one price, or a dash on a row that has none: a model with no
// price row must not read as free.
func priceCell(perM float64, source string) string {
	if source == modelsSourceNone {
		return "—"
	}
	return fmt.Sprintf("$%g", perM)
}

// cell is a usage cell that may be empty, which prints as a dash.
func cell(s string) string {
	if s == "" {
		return "—"
	}
	return oneLine(s)
}
