package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

type modelsAddOptions struct {
	route       string
	priceIn     float64
	priceOut    float64
	priceInSet  bool
	priceOutSet bool
	yes         bool
	// project and config are the settings family's two aiming flags
	// (settingsAimFlags): a caller names the installation this write lands in
	// rather than whichever one this machine's environment would have picked
	// (desktop-console.md §3.4 rule 3). They are how the console aims a write
	// at the project it is showing rather than at the ambient one.
	project string
	config  string
	// dryRun is --dry-run: the change is computed and printed and nothing is
	// written — no config file, no published policy node, no saved apply step.
	// It is a read, so it asks nothing and it makes no change live.
	dryRun bool
	// jsonOut is --json: the dry run's own document
	// (schemas/models-add-plan.schema.json), refused without --dry-run for the
	// reason settings give the same pair — the two answer different questions,
	// so a caller that asked for a document must say so.
	jsonOut bool
	// noApply is --no-apply (D1565): the change is written and the steps that
	// make it live are saved for `fugaro apply` instead of run here. It is the
	// flag's own word, read at RunE's time (the flag is parsed after this
	// struct is built).
	noApply *bool
}

// modelsAddDoor is this command's own name: the door its chain's run is filed
// and printed under, so the lines a person reads name the command they ran and
// not `fugaro update` (accountsAddRunStages' rule).
const modelsAddDoor = "models add"

// modelsAddLocalFileWhat names these two commands in the refusal a project
// selected from a layer and with no file of its own reads. It is the same
// sentence modelsLocalFile passes for `models rm`: the two commands edit one
// file, and a project that has none of its own has nothing for either to
// write.
const modelsAddLocalFileWhat = "models add and models rm"

// modelsAddChanges is the change one `models add` makes, in apply's own closed
// vocabulary: the model of the project, whose one step is the policy publish.
// It is named once so the plan a dry run prints and the chain a write runs
// cannot come to disagree about which step the change owes — and the step table
// (apply.Steps) is the one place either reads it from.
var modelsAddChanges = []apply.Change{{Kind: apply.Resource, Key: apply.ResourceModel}}

func newModelsAddCmd() *cobra.Command {
	var o modelsAddOptions
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Let this project run a model: claim it on a route and price it",
		Long: "Add a model to this project's own config.\n\n" +
			"The model ID is appended to a route's models — the exact ID, never a\n" +
			"pattern — and a model the built-in price catalog does not price needs\n" +
			"--price-in and --price-out (dollars per million tokens), which write a\n" +
			"model_prices entry. When budget.allowed_models is set the ID is appended to\n" +
			"it; when it is absent it stays absent.\n\n" +
			"Only the keys it changes are rewritten: every comment and every other key of\n" +
			"the file is kept.\n\n" +
			"The change reaches a run when the policy node is published, which this command\n" +
			"does itself: a model_prices entry and a budget.allowed_models entry are what a\n" +
			"run reads from there. With --no-apply the change is saved and the publish is\n" +
			"owed, and `fugaro apply` runs it later. A route's own claim is not published —\n" +
			"a run routes by the copy of the providers block in its job's environment — so a\n" +
			"repository routes to the model once `fugaro update --repo .` has been run in\n" +
			"its checkout.\n\n" +
			"--dry-run says what the change would do — the layer and the file it would\n" +
			"write, the entry it would append to the route's models and to\n" +
			"budget.allowed_models, the model_prices entry with both rates, and the\n" +
			"follow-up step it would run or save — and writes nothing: no config file, no\n" +
			"published policy node and no saved apply step. It is answered with no terminal,\n" +
			"and a refusal the write would print (an unknown ID, a half-given price) is the\n" +
			"same refusal. With --json it prints the versioned document\n" +
			"schemas/models-add-plan.schema.json describes, which is what a caller that\n" +
			"draws the change reads; --json is the dry run's own shape and is refused\n" +
			"without --dry-run.\n\n" +
			"--project and --config name the installation this write lands in, the way\n" +
			"every settings write does (desktop-console.md §3.4 rule 3); without them the\n" +
			"selection is the ambient one and a machine with two installations refuses.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.priceInSet = cmd.Flags().Changed("price-in")
			o.priceOutSet = cmd.Flags().Changed("price-out")
			return runModelsAdd(cmd, args[0], &o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.route, "route", "", "the route that claims the model (default: the route that claims it already, or the only one)")
	f.Float64Var(&o.priceIn, "price-in", 0, "input price, dollars per million tokens (required for a model the catalog does not price)")
	f.Float64Var(&o.priceOut, "price-out", 0, "output price, dollars per million tokens (required for a model the catalog does not price)")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what the change would do and write nothing")
	f.BoolVar(&o.jsonOut, "json", false, "print the dry run's document (with --dry-run)")
	o.noApply = addNoApplyFlag(cmd)
	return cmd
}

func runModelsAdd(cmd *cobra.Command, id string, o *modelsAddOptions) error {
	if err := modelsAddDryRunFlags(o); err != nil {
		return err
	}
	if err := modelsPriceFlags(o); err != nil {
		return err
	}
	if err := modelsPriceRange(o); err != nil {
		return err
	}
	// The aiming pair names the installation this write lands in rather than
	// whichever one the ambient environment selects: settingsAimedLocalFile is
	// the same resolution and the same refusal `settings set` takes, with this
	// family's own sentence for a project that has no file of its own.
	sel, lc, err := settingsAimedLocalFile(settingsWriteOptions{project: o.project, config: o.config}, modelsAddLocalFileWhat)
	if err != nil {
		return err
	}
	if err := modelsAddRefuseID(id); err != nil {
		return err
	}
	table, _, err := priceTable(lc, budgetNow())
	if err != nil {
		return err
	}
	route, err := modelsAddRoute(lc, id, o.route)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	next := old
	claim, err := modelsAddClaim(sel.Path, lc, route, id, &next)
	if err != nil {
		return err
	}
	price, priced, err := modelsAddPrice(sel.Path, table, id, o, &next)
	if err != nil {
		return err
	}
	allowed, err := modelsAddAllow(sel.Path, lc, id, &next)
	if err != nil {
		return err
	}
	if o.dryRun {
		return modelsAddDryRun(cmd, modelsAddDryRunInput{
			sel: sel, lc: lc, old: old, next: next, id: id, route: route,
			claim: claim, allowed: allowed, price: priced, o: o,
		})
	}

	// D846's two widening halves: the claim, which puts the model on a route a
	// repository may send its code to, and the allow-list entry, which is the key
	// `settings set budget.allowed_models --add` already makes the typed form.
	// Either one is a widening, and a widening is the owner's own step: it asks
	// nothing at their terminal and a coding agent's session is refused it
	// (modelsAddTypedWhy, D1552). A step that changed nothing (the note it
	// returned is empty) widens nothing.
	claimAppended, allowAppended := claim.by == "", allowed.summary != ""
	what := modelsAddWhat(id, route, claim, price, allowed)
	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    what,
		Command: modelsAddCommand(id, o),
		Yes:     o.yes,
	}
	if claimAppended || allowAppended {
		edit.Typed, edit.TypedWhy = true, modelsAddTypedWhy(claimAppended, allowAppended)
	}
	if err := modelsEditLocal(cmd, lc, sel, edit); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if claim.by != "" {
		fmt.Fprintf(out, "%s is already claimed by %s on route %s: the route is unchanged\n", oneLine(id), oneLine(claim.by), oneLine(route))
	}
	if price.full != "" {
		fmt.Fprintln(out, price.full)
	}
	if lc.Budget == nil || len(lc.Budget.AllowedModels) == 0 {
		fmt.Fprintln(out, "budget.allowed_models is not set, so it stays absent: every model the routes open is allowed")
	}
	if err := modelsAddHandoff(cmd, lc, id); err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		return nil
	}
	// The change's own follow-up (AP6): the policy stage publishes the price and
	// the allow-list entry into the node every run of the project is admitted by.
	// What it does NOT carry is the route's own claim — that lives in the copy of
	// the providers block each job holds, refreshed by that repository's own
	// redeploy, which is per checkout (D1574) and so is never queued by a
	// project-wide change (apply's ResourceModel row): `fugaro update --repo .` in
	// a checkout is still what carries a claim to a job.
	//
	// A model already claimed by a pattern the change did not touch owes the
	// publish for the same reason: the price and the allow-list entry are the
	// node's.
	return chainApply(cmd.Context(), cmd, lc, sel, modelsAddDoor, modelsAddChanges, *o.noApply)
}

// modelsAddNote is one sentence a write produces: the full text the owner
// reads, and the short half the sentence names, so a long explanation is
// printed beside the write rather than pasted into the sentence itself.
type modelsAddNote struct {
	full    string
	summary string
	// by is the claim that already covered the model, when one did.
	by string
}

// modelsAddRefuseID is step 1's refusal of what no route may serve: a Claude ID
// (D785), a ":" variant and an alias, the shapes `fugaro validate` refuses in
// a pin.
func modelsAddRefuseID(id string) error {
	switch {
	case isClaudeModel(id):
		return userErr("%s is a Claude model: Claude models go direct to Anthropic, or down the route the owner's routing.claude switch names, and a provider route never claims one (D785). To send it through a provider, set routing.claude.via and routing.claude.models in the project config", config.CodeSpan(id))
	case strings.Contains(id, ":"):
		return modelsRefuseVariant(id)
	case pricing.IsAlias(id):
		return userErr("%s is not an explicit model ID: name the vendor/model ID the route serves, as a pin must", config.CodeSpan(id))
	}
	if why := config.CheckModelID(id); why != "" {
		return userErr("%s: name a model ID a route may claim", why)
	}
	return nil
}

// modelsPriceFlags refuses a half-given price: a model needs both rates or
// neither, and one alone would be stored as a rate the owner never stated.
func modelsPriceFlags(o *modelsAddOptions) error {
	if o.priceInSet == o.priceOutSet {
		return nil
	}
	missing := "--price-out"
	if o.priceOutSet {
		missing = "--price-in"
	}
	return userErr("%s without %s: write both rates (dollars per million tokens), or neither and let the built-in catalog price the model", modelsPriceFlagGiven(o), missing)
}

// modelsPriceRange refuses a rate the price table cannot carry, before the file
// is built: every rate is dollars per million tokens and must be more than 0 and
// at most pricing.MaxPerM (pricing.Rates.Validate), and a rate outside that is
// refused by the read-back too — with the loader's words about a file the owner
// never wrote, which is the wrong place to learn that a model billed at nothing
// is not a price a budget can count.
func modelsPriceRange(o *modelsAddOptions) error {
	for _, f := range []struct {
		flag  string
		value float64
		set   bool
	}{
		{"--price-in", o.priceIn, o.priceInSet},
		{"--price-out", o.priceOut, o.priceOutSet},
	} {
		if !f.set || modelsRateCarries(f.value) {
			continue
		}
		return userErr("%s %s: a rate is US dollars per million tokens and must be more than 0 and at most %d, so a model billed at nothing — or at more than any provider charges — is not a price this config carries. Name the rate the provider charges", f.flag, modelsPriceText(f.value), pricing.MaxPerM)
	}
	return nil
}

// modelsRateCarries reports whether one rate is one pricing.Rates.Validate
// accepts for input_per_m and output_per_m.
func modelsRateCarries(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= pricing.MaxPerM
}

// modelsPriceFlagGiven is the half of the price pair the owner did type, so the
// refusal of a half-given price names what was passed beside what was not.
func modelsPriceFlagGiven(o *modelsAddOptions) string {
	if o.priceOutSet {
		return "--price-out"
	}
	return "--price-in"
}

// modelsAddRoute is the route the model is claimed on: --route, else the route
// whose models already claim the ID, else the only route there is (spec §4
// step 1). Anything else refuses and names the routes to choose from rather
// than guessing at where the owner's code may go.
func modelsAddRoute(lc *localcfg.Config, id, flag string) (string, error) {
	if flag != "" {
		if _, ok := lc.Providers[flag]; !ok {
			return "", userErr("--route %s: this project has no route of that name (it has %s)", oneLine(flag), modelsRouteNames(lc))
		}
		return flag, nil
	}
	if name, _, ok := config.ProviderFor(lc.Providers, id); ok {
		return name, nil
	}
	if names := sortedProviderNames(lc.Providers); len(names) == 1 {
		return names[0], nil
	}
	return "", userErr("no route claims %s and this project has more than one, so pass --route: %s", config.CodeSpan(id), modelsRouteNames(lc))
}

// modelsRouteNames is the project's route names for a message, or the words for
// a project with none: a write that has nowhere to land says so.
func modelsRouteNames(lc *localcfg.Config) string {
	names := sortedProviderNames(lc.Providers)
	if len(names) == 0 {
		return "no provider route at all — add one to the project config first"
	}
	return strings.Join(names, ", ")
}

// modelsAddClaim is step 2: a route that already claims the ID — by an exact
// entry or by a pattern — is not changed, and the claim is named; otherwise the
// exact ID is appended to that route's models, never a pattern (D785).
func modelsAddClaim(path string, lc *localcfg.Config, route, id string, next *[]byte) (modelsAddNote, error) {
	for _, pat := range lc.Providers[route].Models {
		if config.PatternMatches(pat, id) {
			return modelsAddNote{summary: "the route already claims it", by: pat}, nil
		}
	}
	out, err := yamledit.AddListItem(*next, []string{"providers", route, "models"}, id)
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return modelsAddNote{}, err
		}
		return modelsAddNote{}, userErr("providers.%s.models: %v", route, err)
	}
	*next = out
	return modelsAddNote{
		full:    fmt.Sprintf("providers.%s.models does not carry %s: the exact ID was appended, never a pattern, so the route claims this one model and no other", route, id),
		summary: "claim it on route " + route,
	}, nil
}

// modelsAddPrice is step 3: a model the table prices needs no price of its own
// and the command explains what it is charged at; one the table cannot price
// requires both rates (D782). Rates given for a priced model become a
// model_prices entry that replaces its row, and the command says so.
func modelsAddPrice(path string, table *pricing.Table, id string, o *modelsAddOptions, next *[]byte) (modelsAddNote, modelsAddPriceFact, error) {
	entry, priced := table.Lookup(id)
	if !priced && !o.priceInSet {
		why := table.Refusal(id)
		if why == "" {
			why = "the built-in price catalog does not price it for this route"
		}
		return modelsAddNote{}, modelsAddPriceFact{}, userErr("%s has no price a run can use: %s. Pass --price-in and --price-out (dollars per million tokens): that writes model_prices.%s, and the cache fields stay at their defaults", config.CodeSpan(id), why, id)
	}
	fact := modelsAddPriceFact{input: o.priceIn, output: o.priceOut, source: modelsAddPriceGiven}
	if !o.priceInSet {
		// The rates are the model's own, and where they come from is the second
		// half: a table row the owner's model_prices replaced carries no
		// catalog provenance (pricing.Table.With clears it), so `entry.Catalog`
		// naming nothing is exactly the file's own entry.
		fact = modelsAddPriceFact{input: entry.Rates.InputPerM, output: entry.Rates.OutputPerM, source: modelsAddPriceLocal}
		if entry.Catalog == nil {
			return modelsAddNote{
				full:    fmt.Sprintf("model_prices already prices %s at $%g/M in and $%g/M out", id, entry.Rates.InputPerM, entry.Rates.OutputPerM),
				summary: "the model_prices entry already in the file prices it",
			}, fact, nil
		}
		fact.source = modelsAddPriceCatalog
		return modelsAddNote{full: modelsCatalogPriceNote(id, entry), summary: "the built-in catalog prices it"}, fact, nil
	}
	out, err := yamledit.SetScalar(*next, []string{"model_prices", id, "input_per_m"}, modelsPriceText(o.priceIn))
	if err == nil {
		out, err = yamledit.SetScalar(out, []string{"model_prices", id, "output_per_m"}, modelsPriceText(o.priceOut))
	}
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return modelsAddNote{}, modelsAddPriceFact{}, err
		}
		return modelsAddNote{}, modelsAddPriceFact{}, userErr("model_prices.%s: %v", id, err)
	}
	*next = out
	if priced {
		return modelsAddNote{
			full:    fmt.Sprintf("model_prices.%s now carries $%s/M in and $%s/M out, which replaces the row %s was priced at", id, modelsPriceText(o.priceIn), modelsPriceText(o.priceOut), id),
			summary: "the price you gave replaces the row it had",
		}, fact, nil
	}
	return modelsAddNote{
		full:    fmt.Sprintf("model_prices.%s carries $%s/M in and $%s/M out; the cache fields are left at their defaults", id, modelsPriceText(o.priceIn), modelsPriceText(o.priceOut)),
		summary: "priced at $" + modelsPriceText(o.priceIn) + "/M in and $" + modelsPriceText(o.priceOut) + "/M out",
	}, fact, nil
}

// modelsCatalogPriceNote is what a catalog-priced model needs the owner to
// read: the end of the tier's range that is charged, the numbers behind that
// range when the charge leaves room above it, and the switch that would charge
// the other end — which belongs on the route, where `fugaro update --repo`
// deploys it, so the command points there rather than writing one (F7, D795,
// F29).
func modelsCatalogPriceNote(id string, m pricing.Model) string {
	c := m.Catalog
	// The end charged is the row's own bound, not a default written down
	// here: highest is what an unset price_policy means (D2644), and the
	// note names the end that is charged and the switch that would move it
	// to the other one.
	end, other, otherPolicy := "dearest", "cheapest", config.PricePolicyCheapest
	if c.Bound == "min" {
		end, other, otherPolicy = "cheapest", "dearest", config.PricePolicyHighest
	}
	note := fmt.Sprintf("%s needs no price of your own: the built-in catalog prices it, and a call is charged the %s end of its %s tier — $%g/M in and $%g/M out", id, end, c.Tier, m.Rates.InputPerM, m.Rates.OutputPerM)
	if spread := m.CatalogRange(); spread != "" {
		note += "; " + spread
	}
	return note + fmt.Sprintf("; price_policy: %s would charge that tier's %s endpoint, which you set on the route in your project config and fugaro update --repo deploys; this command has no switch for it (D795, F29)", otherPolicy, other)
}

// modelsAddAllow is step 4: a set budget.allowed_models gains the ID, because
// no run could use the model otherwise; an absent one stays absent, because a
// list of one would make the owner's config narrower than it was (D786). The
// returned note is empty when the list already admitted the model.
func modelsAddAllow(path string, lc *localcfg.Config, id string, next *[]byte) (modelsAddNote, error) {
	if lc.Budget == nil || len(lc.Budget.AllowedModels) == 0 {
		return modelsAddNote{}, nil
	}
	if slices.Contains(lc.Budget.AllowedModels, id) {
		return modelsAddNote{}, nil
	}
	out, err := yamledit.AddListItem(*next, []string{"budget", "allowed_models"}, id)
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return modelsAddNote{}, err
		}
		return modelsAddNote{}, userErr("budget.allowed_models: %v", err)
	}
	*next = out
	return modelsAddNote{
		full:    "budget.allowed_models now names " + id + ": without it no run could use the model",
		summary: "budget.allowed_models gains it",
	}, nil
}

// modelsAddTypedWhy is the sentence the write prints for the wideness of the
// edit at hand: the claim is where code may go, the allow-list entry is what a
// run of this project may use, and an add that is both names both. It ends
// there and asks for nothing, because a widening is the owner's own step and a
// coding agent's session is what refuses it (D1552).
func modelsAddTypedWhy(claim, allow bool) string {
	var widens string
	switch {
	case claim && allow:
		widens = "this widens where code may go and raises a limit: the route then serves the model, so a repository of this project may pin it and have its code sent there, and a run of this project may use it"
	case claim:
		widens = "this widens where code may go: the route then serves the model, so a repository of this project may pin it and have its code sent there"
	default:
		widens = "this raises a limit: budget.allowed_models decides which models a run of this project may use, and it names one more"
	}
	return widens
}

// modelsAddWhat is the sentence's own line: the short half of each step, so
// the long explanations are printed beside the write and not pasted into it.
func modelsAddWhat(id, route string, notes ...modelsAddNote) string {
	parts := []string{"add " + id + " to the project config, on route " + route}
	for _, n := range notes {
		if n.summary != "" {
			parts = append(parts, n.summary)
		}
	}
	return strings.Join(parts, "; ")
}

// modelsAddCommand is the command an agent session prints for the person to run
// at their own terminal: the same flags, spelled out.
func modelsAddCommand(id string, o *modelsAddOptions) string {
	c := "fugaro models add " + id
	if o.route != "" {
		c += " --route " + o.route
	}
	if o.priceInSet {
		c += " --price-in " + modelsPriceText(o.priceIn) + " --price-out " + modelsPriceText(o.priceOut)
	}
	return c
}

// modelsAddHandoff is step 6: the one command that carries the change to the
// cloud (D780, D787), plus `models use` when no repository this machine can see
// already runs the model.
func modelsAddHandoff(cmd *cobra.Command, lc *localcfg.Config, id string) error {
	pins, _ := modelsModelPinned(lc, id)
	if len(pins) > 0 {
		return nil
	}
	repos := sortedRepoNames(lc)
	if len(repos) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "no repository pins %s yet: fugaro models use %s coder %s\n", oneLine(id), repos[0], oneLine(id))
	return nil
}

// A rate as the file writes it: the shortest decimal that reads back as the
// same number, so 1 is written 1 and never 1.000000.
func modelsPriceText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// The models add plan (MD1): what `--dry-run` answers with, and the shape a
// caller that draws the change — the console's Manage window — is written from.
// The document is the change as data: every fact a consumer would otherwise
// have to work out is one the write path already computed, and
// schemas/models-add-plan.schema.json is its contract. A member is only ever
// added within a version, and removing or retyping one bumps the version
// (D848), so a consumer that reads version 1 keeps reading it.
const modelsAddPlanVersion = 1

// Where the rates in the document come from, as `prices.source` names it: the
// rates the caller passed (which are written as a model_prices entry), the
// built-in catalog's row for the model, or the file's own model_prices entry.
// The second and third are the models read's own words (modelsSourceCatalog,
// modelsSourceLocal), so one vocabulary describes both surfaces.
const (
	modelsAddPriceGiven   = "given"
	modelsAddPriceCatalog = modelsSourceCatalog
	modelsAddPriceLocal   = modelsSourceLocal
)

// The follow-up step's two states: the step the write would run now, and the
// step --no-apply would leave saved for `fugaro apply`.
const (
	modelsAddPlanStepRun   = "run"
	modelsAddPlanStepSaved = "saved"
)

// modelsAddPlanDoc is the dry run's document. Refusal is empty in every
// document this CLI prints: a refusal is this program's own sentence with a
// non-zero exit and no document at all, which is what a consumer branches on,
// and it is written out because the shape is the contract.
type modelsAddPlanDoc struct {
	Version int    `json:"version"`
	Layer   string `json:"layer"`
	Path    string `json:"path"`
	Model   string `json:"model"`
	Route   string `json:"route"`
	// Changed is whether the file would change at all: false is a
	// classification and not an error — the command exits 0 with this document
	// — and it is the answer to an add whose claim, price and allow-list entry
	// the file already holds.
	Changed       bool                `json:"changed"`
	Claim         modelsAddPlanClaim  `json:"claim"`
	AllowedModels modelsAddPlanAllow  `json:"allowed_models"`
	Prices        modelsAddPlanPrice  `json:"prices"`
	Steps         []modelsAddPlanStep `json:"steps"`
	Refusal       string              `json:"refusal"`
}

// modelsAddPlanClaim is the `providers.<route>.models` entry the change would
// append: Entry is the exact ID that lands there, and ClaimedBy is the existing
// entry or pattern that already covers the model — the two are never both set,
// and one of them always is, because a model the route already claims is a
// claim this change leaves alone.
type modelsAddPlanClaim struct {
	Key       string `json:"key"`
	Entry     string `json:"entry"`
	ClaimedBy string `json:"claimed_by"`
}

// modelsAddPlanAllow is the `budget.allowed_models` entry the change would
// append. Present is whether the key is set at all: an absent list stays absent
// rather than becoming a list of one (D786), so a document whose Entry is empty
// and whose Present is false describes a project where every model a route
// opens is allowed.
type modelsAddPlanAllow struct {
	Key     string `json:"key"`
	Entry   string `json:"entry"`
	Present bool   `json:"present"`
}

// modelsAddPlanPrice is the model_prices entry the model lands at, with both
// rates: the two the caller gave, the catalog row's, or the file's own entry's.
type modelsAddPlanPrice struct {
	Key        string  `json:"key"`
	InputPerM  float64 `json:"input_per_m"`
	OutputPerM float64 `json:"output_per_m"`
	Source     string  `json:"source"`
}

// modelsAddPlanStep is one follow-up step the change owes, by the step table's
// own name, and whether the write would run it or leave it saved.
type modelsAddPlanStep struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// modelsAddDryRunInput is the facts a dry run prints, gathered where the write
// path has already computed them.
type modelsAddDryRunInput struct {
	sel     localcfg.Selection
	lc      *localcfg.Config
	old     []byte
	next    []byte
	id      string
	route   string
	claim   modelsAddNote
	allowed modelsAddNote
	price   modelsAddPriceFact
	o       *modelsAddOptions
}

// modelsAddPriceFact is the model_prices entry a change carries: both rates and
// where they come from.
type modelsAddPriceFact struct {
	input  float64
	output float64
	source string
}

// modelsAddDryRunFlags refuses the combination the command does not take:
// --json is the dry run's own shape, so a caller that asked for a document must
// say so (settingsDryRunFlags' rule, and the console always passes both).
func modelsAddDryRunFlags(o *modelsAddOptions) error {
	if o.jsonOut && !o.dryRun {
		return userErr("--json is the dry run's document: pass --dry-run with it, or read this project's models with `fugaro models ls --json`")
	}
	return nil
}

// modelsAddDryRun is --dry-run's whole answer: the refusals the write would
// make, then the document, and nothing written. It is reached after every
// refusal about the arguments (an unknown ID, a half-given price) has been
// made, so those are the write's own sentences, and it makes the write's own
// last two checks itself through modelsAddPrecheck.
//
// The follow-up is the change's own — the step table's names, not a second list
// — and its state is --no-apply's own word: a write that saves its steps prints
// the same document with them saved, which is what apply would then do.
func modelsAddDryRun(cmd *cobra.Command, in modelsAddDryRunInput) error {
	if err := modelsAddPrecheck(in.sel, in.old, in.next); err != nil {
		return err
	}
	changed := yamledit.Changed(in.old, in.next)
	steps := []modelsAddPlanStep{}
	if changed {
		names, err := chainSteps(modelsAddChanges)
		if err != nil {
			return err
		}
		state := modelsAddPlanStepRun
		if *in.o.noApply {
			state = modelsAddPlanStepSaved
		}
		for _, name := range names {
			steps = append(steps, modelsAddPlanStep{Name: safetext.Field(name), State: state})
		}
	}
	doc := modelsAddPlanDoc{
		Version: modelsAddPlanVersion,
		// The layer is the local project config and never another: this command
		// edits the selected installation's own file and nothing else (D780).
		Layer:   settingsLayerLocal,
		Path:    safetext.Field(in.sel.Path),
		Model:   safetext.Field(in.id),
		Route:   safetext.Field(in.route),
		Changed: changed,
		Claim: modelsAddPlanClaim{
			Key:       safetext.Field("providers." + in.route + ".models"),
			ClaimedBy: safetext.Field(in.claim.by),
		},
		AllowedModels: modelsAddPlanAllow{
			Key:     safetext.Field("budget.allowed_models"),
			Present: in.lc.Budget != nil && len(in.lc.Budget.AllowedModels) > 0,
		},
		Prices: modelsAddPlanPrice{
			Key:        safetext.Field("model_prices." + in.id),
			InputPerM:  in.price.input,
			OutputPerM: in.price.output,
			Source:     safetext.Field(in.price.source),
		},
		Steps: steps,
	}
	// A claim is appended exactly when nothing already covers the model, and
	// the allow-list entry exactly when the list is set and does not name it
	// (modelsAddClaim, modelsAddAllow): the notes carry which half happened,
	// and the document states the entry rather than a summary of it.
	if in.claim.by == "" {
		doc.Claim.Entry = safetext.Field(in.id)
	}
	if in.allowed.summary != "" {
		doc.AllowedModels.Entry = safetext.Field(in.id)
	}
	return writeModelsAddPlan(cmd.OutOrStdout(), doc, in.o.jsonOut)
}

// modelsAddPrecheck is the two refusals the write path makes over the edited
// bytes before it writes them (modelsEditLocal): the strict read-back the
// command's own read used, and the price table built from the edited bytes, so
// a claim the catalog refuses or a model_prices entry the compile cannot
// express is refused here rather than written into a file that would refuse
// every launch (design D5). Same calls, same bytes, same sentences — the dry
// run answers for the write, so it must be refused wherever the write would be
// (D1106's rule for the settings dry run).
func modelsAddPrecheck(sel localcfg.Selection, old, next []byte) error {
	if !yamledit.Changed(old, next) {
		return nil
	}
	after, err := modelsReparseLocal(sel.Path, next)
	if err != nil {
		return err
	}
	if _, _, err := priceTable(after, budgetNow()); err != nil {
		return userErr("the project config this would write does not price its models: %v", err)
	}
	return nil
}

// writeModelsAddPlan prints the dry run's document: the versioned JSON document
// with --json, and the same facts as a table without it. The two shapes carry
// the same facts or the table is a second contract, and a second contract is
// the one that drifts — which is why the table renders the document rather than
// the notes it was built from.
func writeModelsAddPlan(w io.Writer, doc modelsAddPlanDoc, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FIELD\tVALUE")
	for _, row := range [][2]string{
		{"version", strconv.Itoa(doc.Version)},
		{"layer", doc.Layer},
		{"path", doc.Path},
		{"model", doc.Model},
		{"route", doc.Route},
		{"changed", strconv.FormatBool(doc.Changed)},
		{"claim", modelsAddPlanClaimText(doc.Claim)},
		{"allowed_models", modelsAddPlanAllowText(doc.AllowedModels)},
		{"prices", modelsAddPlanPriceText(doc.Prices)},
		{"steps", modelsAddPlanStepsText(doc.Steps)},
		{"refusal", doc.Refusal},
	} {
		fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
	}
	return tw.Flush()
}

// modelsAddPlanClaimText renders the claim the one way a table cell carries it:
// the key and the entry that lands on it, or the entry that already covers the
// model.
func modelsAddPlanClaimText(c modelsAddPlanClaim) string {
	if c.Entry != "" {
		return c.Key + " += " + c.Entry
	}
	if c.ClaimedBy != "" {
		return "already claimed by " + c.ClaimedBy
	}
	return ""
}

// modelsAddPlanAllowText renders the allow-list entry the same way, with the
// third answer a list can give: the key is not set, so it stays absent.
func modelsAddPlanAllowText(a modelsAddPlanAllow) string {
	switch {
	case a.Entry != "":
		return a.Key + " += " + a.Entry
	case a.Present:
		return a.Key + " already names it"
	default:
		return a.Key + " is not set, so it stays absent"
	}
}

// modelsAddPlanPriceText renders the rates and where they come from, in words a
// reader who has not run the command can act on.
func modelsAddPlanPriceText(p modelsAddPlanPrice) string {
	return fmt.Sprintf("%s: $%s/M in and $%s/M out (%s)",
		p.Key, modelsPriceText(p.InputPerM), modelsPriceText(p.OutputPerM), modelsAddPlanPriceSourceText(p.Source))
}

// modelsAddPlanPriceSourceText is the source word in a sentence: the document's
// own three words, named rather than spelled a second time.
func modelsAddPlanPriceSourceText(source string) string {
	switch source {
	case modelsAddPriceGiven:
		return "the rates you gave"
	case modelsAddPriceCatalog:
		return "the built-in catalog prices it"
	case modelsAddPriceLocal:
		return "the file's own model_prices already prices it"
	}
	return source
}

// modelsAddPlanStepsText renders the follow-up: one step per name with its own
// state, and the words for a change that writes nothing, which owes none.
func modelsAddPlanStepsText(steps []modelsAddPlanStep) string {
	if len(steps) == 0 {
		return "nothing: this change writes nothing"
	}
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		parts = append(parts, s.Name+" ("+s.State+")")
	}
	return strings.Join(parts, ", ")
}
