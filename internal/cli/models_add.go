package cli

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

type modelsAddOptions struct {
	route       string
	priceIn     float64
	priceOut    float64
	priceInSet  bool
	priceOutSet bool
	yes         bool
}

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
			"The change reaches a run through `fugaro update`, which publishes the policy\n" +
			"node: a model_prices entry and a budget.allowed_models entry are what a run\n" +
			"reads from there. A route's own claim is not — a run routes by the copy of\n" +
			"the providers block in its job's environment — so a repository routes to the\n" +
			"model once `fugaro update --repo .` has been run in its checkout.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.priceInSet = cmd.Flags().Changed("price-in")
			o.priceOutSet = cmd.Flags().Changed("price-out")
			return runModelsAdd(cmd, args[0], &o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.route, "route", "", "the route that claims the model (default: the route that claims it already, or the only one)")
	f.Float64Var(&o.priceIn, "price-in", 0, "input price, dollars per million tokens (required for a model the catalog does not price)")
	f.Float64Var(&o.priceOut, "price-out", 0, "output price, dollars per million tokens (required for a model the catalog does not price)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runModelsAdd(cmd *cobra.Command, id string, o *modelsAddOptions) error {
	if err := modelsPriceFlags(o); err != nil {
		return err
	}
	if err := modelsPriceRange(o); err != nil {
		return err
	}
	sel, lc, err := modelsLocalFile()
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
	price, err := modelsAddPrice(sel.Path, table, id, o, &next)
	if err != nil {
		return err
	}
	allowed, err := modelsAddAllow(sel.Path, lc, id, &next)
	if err != nil {
		return err
	}

	// D846's two widening halves: the claim, which puts the model on a route a
	// repository may send its code to, and the allow-list entry, which is the key
	// `settings set budget.allowed_models --add` already makes the typed form.
	// Either one asks for the project's own name — or --yes, outside a coding
	// agent's session (D1107). A step that changed nothing (the note it returned
	// is empty) widens nothing.
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
	if yamledit.Changed(old, next) {
		// The hand-off names the one command this change really needs. The policy
		// node carries the price and the allow-list entry, so `fugaro update`
		// publishes those; the claim lives in the providers block each job holds
		// its own copy of, which only that repository's `fugaro update --repo .`
		// writes (jobConfigCopyPolicyNote). E2's converge is what makes the claim
		// reach every recorded repository from the one command, and the plan
		// records that M3/M4 do not ship in a release before it.
		if claim.by == "" {
			fmt.Fprintln(out, "next: fugaro update, then fugaro update --repo . in each repository that should run the model")
		} else {
			fmt.Fprintln(out, "next: fugaro update")
		}
	}
	if claim.by != "" {
		fmt.Fprintf(out, "%s is already claimed by %s on route %s: the route is unchanged\n", oneLine(id), oneLine(claim.by), oneLine(route))
	}
	if price.full != "" {
		fmt.Fprintln(out, price.full)
	}
	if lc.Budget == nil || len(lc.Budget.AllowedModels) == 0 {
		fmt.Fprintln(out, "budget.allowed_models is not set, so it stays absent: every model the routes open is allowed")
	}
	return modelsAddHandoff(cmd, lc, id)
}

// modelsAddNote is one sentence a write produces: the full text the owner
// reads, and the short half the confirmation names, so a long explanation is
// printed beside the write rather than pasted into the confirmation line.
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
func modelsAddPrice(path string, table *pricing.Table, id string, o *modelsAddOptions, next *[]byte) (modelsAddNote, error) {
	entry, priced := table.Lookup(id)
	if !priced && !o.priceInSet {
		why := table.Refusal(id)
		if why == "" {
			why = "the built-in price catalog does not price it for this route"
		}
		return modelsAddNote{}, userErr("%s has no price a run can use: %s. Pass --price-in and --price-out (dollars per million tokens): that writes model_prices.%s, and the cache fields stay at their defaults", config.CodeSpan(id), why, id)
	}
	if !o.priceInSet {
		if entry.Catalog == nil {
			return modelsAddNote{
				full:    fmt.Sprintf("model_prices already prices %s at $%g/M in and $%g/M out", id, entry.Rates.InputPerM, entry.Rates.OutputPerM),
				summary: "the model_prices entry already in the file prices it",
			}, nil
		}
		return modelsAddNote{full: modelsCatalogPriceNote(id, entry), summary: "the built-in catalog prices it"}, nil
	}
	out, err := yamledit.SetScalar(*next, []string{"model_prices", id, "input_per_m"}, modelsPriceText(o.priceIn))
	if err == nil {
		out, err = yamledit.SetScalar(out, []string{"model_prices", id, "output_per_m"}, modelsPriceText(o.priceOut))
	}
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return modelsAddNote{}, err
		}
		return modelsAddNote{}, userErr("model_prices.%s: %v", id, err)
	}
	*next = out
	if priced {
		return modelsAddNote{
			full:    fmt.Sprintf("model_prices.%s now carries $%s/M in and $%s/M out, which replaces the row %s was priced at", id, modelsPriceText(o.priceIn), modelsPriceText(o.priceOut), id),
			summary: "the price you gave replaces the row it had",
		}, nil
	}
	return modelsAddNote{
		full:    fmt.Sprintf("model_prices.%s carries $%s/M in and $%s/M out; the cache fields are left at their defaults", id, modelsPriceText(o.priceIn), modelsPriceText(o.priceOut)),
		summary: "priced at $" + modelsPriceText(o.priceIn) + "/M in and $" + modelsPriceText(o.priceOut) + "/M out",
	}, nil
}

// modelsCatalogPriceNote is what a catalog-priced model needs the owner to
// read: the end of the tier's range that is charged, the dearest endpoint of
// that range, and the switch that would charge that instead — which `fugaro
// update` does not accept yet, so the command writes none (F7, D795).
func modelsCatalogPriceNote(id string, m pricing.Model) string {
	c := m.Catalog
	note := fmt.Sprintf("%s needs no price of your own: the built-in catalog prices it, and a call is charged the cheapest end of its %s tier — $%g/M in and $%g/M out", id, c.Tier, m.Rates.InputPerM, m.Rates.OutputPerM)
	if spread := m.CatalogRange(); spread != "" {
		note += "; " + spread
	}
	return note + "; price_policy: highest would charge that tier's dearest endpoint, and fugaro update does not accept the switch yet, so this command writes none (D795)"
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

// modelsAddTypedWhy is the sentence D846's typed confirmation shows before it
// asks for the project's own name, for the wideness of the edit at hand: the
// claim is where code may go, the allow-list entry is what a run of this
// project may use, and an add that is both names both. It ends the way every
// typed confirmation's sentence does — the project's name, or the one flag that
// stands in for it outside a coding agent's session (D1107).
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
	return widens + ", so the project's own name is what confirms it, or --yes outside a coding agent's session"
}

// modelsAddWhat is the confirmation's own line: the short half of each step, so
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
