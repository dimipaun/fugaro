package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the models family's two writers (D2613): `models provider
// set|rm`, which writes and removes the leaves of one entry of the providers
// block, and `models price set`, which writes the leaves of one model_prices
// entry. The reads stay where they were — `models ls` and `models search` still
// only read, and the group still runs in a coding agent's session — and what a
// route, a fee and a price are is the owner's own decision: the four leaves
// D2617 names are refused in that session whatever the gate says.
//
// Every write goes the way the rest of the family goes: the selected local
// project config is read whole, yamledit splices the leaves into those bytes,
// and the edited file is held against the strict loader before applySettingsEdit
// writes it — so the diff, the sentence, the coding-agent fence, the backup and
// the atomic write are that one file's copy of each (modelsEditLocal, F4). A
// value never travels through the writer registry
// (docs/contracts/config-ownership.md §9): writers_providers.go registers the
// command line that writes a key, and the value is in the file this command
// wrote.

// modelsProviderSetOptions is one `models provider set`: the eight leaves it
// can write, each one applied only when its flag was passed.
type modelsProviderSetOptions struct {
	kind        string
	baseURL     string
	auth        string
	secret      string
	pricePolicy string
	requireZDR  bool
	routeFeePct float64
	allowDataTo string
	layer       string
}

// modelsPriceSetOptions is one `models price set`.
type modelsPriceSetOptions struct {
	inputPerM      float64
	outputPerM     float64
	cacheWrite5m   float64
	cacheWrite1h   float64
	cacheRead      float64
	webSearchPer1k float64
	layer          string
}

// newModelsProviderCmd is `fugaro models provider`: the writes that change the
// routes behind the models. The reads are not repeated here — `models ls` names
// every route and what it serves — so this group is only what writes one and
// what removes one.
func newModelsProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Write and remove the provider routes of this project's config",
		Long: `Write or remove one entry of the project config's providers block.

A route is what a repository's code goes down: its endpoint, the credential it
is sent with, the repositories whose code may be sent there and the fee added to
its calls. ` + "`models provider set`" + ` writes the leaves of one entry and
` + "`models provider rm`" + ` takes one away whole; ` + "`models ls`" + ` shows the
routes this project has.

The change reaches a run through ` + "`fugaro update --repo .`" + ` in each
repository whose code should go down the route: a run routes by the copy of the
providers block in its own job's environment, and that command writes it.`,
	}
	cmd.AddCommand(newModelsProviderSetCmd(), newModelsProviderRmCmd())
	return cmd
}

func newModelsProviderSetCmd() *cobra.Command {
	var o modelsProviderSetOptions
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Write the leaves of one provider route in this project's config",
		Long: "Set the fields of one entry of the project config's providers block.\n\n" +
			"Only the flags you pass are written: every other leaf of the entry, every\n" +
			"comment and every other key of the file is kept. A name the config does not\n" +
			"carry yet is created, and the file's own rules are what it is held to — an\n" +
			"entry needs a kind, an endpoint, an auth and a secret name before it reads\n" +
			"back.\n\n" +
			"--secret names a stored secret and never carries the key itself: the value\n" +
			"belongs in Secret Manager (`fugaro secrets set <name>`), and what lands in\n" +
			"the file is that name. A value that looks like a key is refused, and the\n" +
			"refusal never repeats it.\n\n" +
			"--allow-data-to is the whole list of repositories (owner/name, or `*` for\n" +
			"every repository) whose code may be sent down the route: the names it gives\n" +
			"replace the ones the file carries.\n\n" +
			"Four of these leaves widen the route — --base-url, --allow-data-to,\n" +
			"--require-zdr and --route-fee-pct — so they are the owner's own step and a\n" +
			"coding agent's session is refused them (D2617).\n\n" +
			"`fugaro update --repo .` in a repository's own checkout is what puts the\n" +
			"route in that repository's runs.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runModelsProviderSet(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.kind, "kind", "", "the provider kind the endpoint speaks (anthropic-compat)")
	f.StringVar(&o.baseURL, "base-url", "", "the endpoint this route's calls go to (https)")
	f.StringVar(&o.auth, "auth", "", "how the credential is sent: bearer or x-api-key")
	f.StringVar(&o.secret, "secret", "", "the NAME of the secret holding the key (fugaro secrets set <name>), never the key")
	f.StringVar(&o.pricePolicy, "price-policy", "", "which end of the price range is charged: cheapest or highest")
	f.BoolVar(&o.requireZDR, "require-zdr", true, "price the route at the endpoints that hold no data (a boolean: --require-zdr=false turns it off)")
	f.Float64Var(&o.routeFeePct, "route-fee-pct", 0, "the percentage added to every charge this route makes (0 is none)")
	f.StringVar(&o.allowDataTo, "allow-data-to", "", "the repositories whose code may be sent down this route (owner/name, comma-separated; `*` is every repository)")
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	return cmd
}

func runModelsProviderSet(cmd *cobra.Command, name string, o *modelsProviderSetOptions) error {
	if err := modelsProviderLayer("providers."+name+".kind", o.layer); err != nil {
		return err
	}
	if cmd.Flags().Changed("secret") {
		if err := modelsProviderSecretRef(o.secret); err != nil {
			return err
		}
	}
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	if err := modelsProviderRefuseNew(sel.Path, name, lc); err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	values := map[string]string{
		"kind":          o.kind,
		"base_url":      o.baseURL,
		"auth":          o.auth,
		"secret":        o.secret,
		"price_policy":  o.pricePolicy,
		"require_zdr":   strconv.FormatBool(o.requireZDR),
		"route_fee_pct": modelsPriceText(o.routeFeePct),
		"allow_data_to": o.allowDataTo,
	}
	next := old
	// line is the command as this run spells it, which a coding agent's session
	// is refused with: the words that work at the owner's own terminal. It
	// carries what was passed and never a value this file refuses to print.
	line := []string{"fugaro", "models", "provider", "set", quoteWord(name)}
	var wrote, widened []string
	for _, leaf := range modelsProviderWriterLeaves {
		if !cmd.Flags().Changed(leaf.flag) {
			continue
		}
		out, err := modelsProviderSplice(sel.Path, name, leaf.leaf, values[leaf.leaf], next, lc)
		if err != nil {
			return err
		}
		next = out
		wrote = append(wrote, leaf.leaf)
		line = append(line, leaf.flagArg(), quoteWord(values[leaf.leaf]))
		if _, ok := modelsProviderWidening[leaf.leaf]; ok {
			widened = append(widened, leaf.leaf)
		}
	}
	if len(wrote) == 0 {
		return userErr("models provider set %s: pass at least one leaf to write (%s)", oneLine(name), modelsProviderFlagList())
	}

	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("set providers.%s in the project config (%s)", name, strings.Join(wrote, ", ")),
		Command: strings.Join(line, " "),
	}
	if len(widened) > 0 {
		edit.Typed, edit.TypedWhy = true, modelsProviderTypedWhy(widened)
	}
	if err := modelsEditLocal(cmd, lc, sel, edit); err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		// A write that changed nothing — every flag's value is the one the file
		// already carries — owes no stage: there is no change to publish.
		return nil
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "providers.%s carries %s now: next: fugaro update --repo . in each repository whose code should go down this route\n", name, strings.Join(wrote, ", "))
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: "providers." + name + "." + wrote[0], Store: settings.StoreLocal})
}

// modelsProviderSplice is one leaf of `models provider set`, written into the
// bytes the command is building. The list leaf goes through the same list
// editor every list write in this program uses (settingsSetList, D1173) because
// a member of a flow sequence cannot be spliced and an owner's hand may write
// one: `allow_data_to: [acme/app]` is a list this command can still edit, and
// the diff shows the style it rewrites.
func modelsProviderSplice(path, name, leaf, value string, src []byte, lc *localcfg.Config) ([]byte, error) {
	if leaf != "allow_data_to" {
		out, err := yamledit.SetScalar(src, []string{"providers", name, leaf}, value)
		return modelsProviderSpliceResult(path, name, leaf, out, err)
	}
	want, err := modelsProviderAllowList(value)
	if err != nil {
		return nil, err
	}
	out, err := settingsSetList(src, []string{"providers", name, "allow_data_to"}, lc.Providers[name].AllowDataTo, want)
	return modelsProviderSpliceResult(path, name, leaf, out, err)
}

// modelsProviderSpliceResult is what a splice answers, in the two refusals the
// family already states: one shape the editor cannot reach (modelsSpliceRefusal,
// D790) and the leaf's own path.
func modelsProviderSpliceResult(path, name, leaf string, out []byte, err error) ([]byte, error) {
	if err == nil {
		return out, nil
	}
	if err, ok := modelsSpliceRefusal(path, err); ok {
		return nil, err
	}
	return nil, userErr("providers.%s.%s: %v", name, leaf, err)
}

// modelsProviderAllowList is --allow-data-to as the list the file carries. It
// is a list and not a scalar, so it is written through the editor's own list
// operations, and an empty flag is refused rather than written: an
// allow_data_to with nothing under it is refused by the config itself as
// ambiguous — it is neither nobody nor every repository — and a route nothing
// may send code down is the absence of the key by hand, not the empty list.
func modelsProviderAllowList(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	want := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			want = append(want, part)
		}
	}
	if len(want) == 0 {
		return nil, userErr("--allow-data-to with no repository: an allow_data_to carrying none is refused by the config (it is neither nobody nor every repository). Name at least one repository (owner/name), or `*` for every repository")
	}
	return want, nil
}

// modelsProviderRefuseNew refuses a name the config does not carry: this
// command sets the fields of a provider entry, and an entry is not one until it
// lists the model IDs it serves — `providers.<name>.models`, which is the one
// leaf of the block no flag here writes (it is `fugaro models add`'s and
// `models rm`'s, leaf by leaf, and the registry's own writer for it is still
// T1's or L1's to state). The refusal names the two ways an entry comes to
// exist, because the person who typed one of the flags above is the person who
// needs them: the block written in the file (docs/multi-model.md §2), and an
// entry `fugaro models add` appends a further model to once it is there.
func modelsProviderRefuseNew(path, name string, lc *localcfg.Config) error {
	if _, ok := lc.Providers[name]; ok {
		return nil
	}
	return userErr("providers.%s is not an entry %s carries, and this command writes the fields of one that is: a provider entry lists the model IDs it serves (providers.%s.models), and no flag here writes that list. Write the entry in that file — kind, base_url, auth, secret and models, as docs/multi-model.md §2 spells it — and its fields are this command's from then on; `fugaro models add <id> --route %s` claims one more model on a route that already exists", name, path, name, name)
}

// modelsProviderSecretRef refuses a --secret that is not the name of a stored
// secret: the leaf carries the name the key is stored under, and a value that
// looks like a key belongs in Secret Manager. config.CheckSecretName is the
// config's own rule (so the file and this command refuse the same spellings),
// and neither its sentence nor this one repeats the value: the value IS a key
// when this refusal fires, and a message that echoed it would print it into a
// terminal, a log or a record (docs/contracts/config-ownership.md §9).
func modelsProviderSecretRef(name string) error {
	if why := config.CheckSecretName(name); why != "" {
		return userErr("--secret: %s. Store the key with `fugaro secrets set <name>` and pass that name here", why)
	}
	return nil
}

// modelsProviderWidening is the four leaves D2617 names, each with the sentence
// saying what it decides. The list is the whole of the rule and not a direction
// within one leaf: an agent's session has no business pointing a route at
// another endpoint, opening it to another repository, relaxing whether the
// provider must hold what it is sent, or adding a fee — and which way a value
// moves is not this command's to grade.
var modelsProviderWidening = map[string]string{
	"base_url":      "base_url is the endpoint the route's calls go to, so it is where a repository's code is sent",
	"allow_data_to": "allow_data_to decides whose code may be sent there",
	"require_zdr":   "require_zdr decides whether the provider must hold what it is sent",
	"route_fee_pct": "route_fee_pct is added to every charge the route makes",
}

// modelsProviderTypedWhy is the sentence such a write is refused with in a
// coding agent's session, in the order the flags were passed.
func modelsProviderTypedWhy(widened []string) string {
	parts := make([]string, 0, len(widened))
	for _, leaf := range widened {
		parts = append(parts, modelsProviderWidening[leaf])
	}
	return "this widens a provider route, which is the owner's own step: " + strings.Join(parts, "; ")
}

// modelsProviderFlagList is the leaves this command writes, as flags, for the
// refusal of a call that passed none.
func modelsProviderFlagList() string {
	return modelsWriterFlagList(modelsProviderWriterLeaves)
}

// modelsWriterFlagList is one command's leaves as flags, for a refusal that
// names what it writes.
func modelsWriterFlagList(leaves []modelsWriterLeaf) string {
	flags := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		flags = append(flags, leaf.flagArg())
	}
	return strings.Join(flags, ", ")
}

// modelsProviderLayer refuses a --layer these keys do not have (T3). Every key
// this file writes lives in the local project config's providers or
// model_prices block, so the registry gives each of its rows the local config's
// layers and a write lands in one of them: the flag is read before the file is,
// so a --layer the key does not have is refused naming the layers it does have
// rather than growing a second one.
func modelsProviderLayer(key, layer string) error {
	if layer == "" {
		layer = settingsLayerLocal
	}
	if !slices.Contains(settingsWriteLayers, layer) {
		return userErr("--layer %s: a write lands in one of %s", oneLine(layer), strings.Join(settingsWriteLayers, ", "))
	}
	row, ok := modelsSettingsRow(key)
	if !ok {
		return userErr("%s: this build's settings registry has no row for that key, so no layer can carry it", key)
	}
	have := make([]string, 0, len(row.Layers))
	for _, l := range row.Layers {
		if slices.Contains(settingsWriteLayers, l) {
			have = append(have, l)
		}
	}
	if len(have) == 0 {
		have = row.Layers
	}
	if slices.Contains(have, layer) {
		return nil
	}
	return userErr("%s is not a key --layer %s writes: it is written in %s", key, layer, strings.Join(have, ", "))
}

// modelsSettingsRow is the registry row a provider or price key is a spelling
// of: the registry holds these rows as patterns with the provider's or the
// model's name varying, so the row's layers are read from the pattern the key
// matches. It is the models family's own lookup and takes the rows the generic
// path leaves to it (settingsWriteLookup's comment).
func modelsSettingsRow(key string) (settings.Row, bool) {
	if row, ok := settings.Lookup(key); ok {
		return row, true
	}
	for _, row := range settings.Rows() {
		if settingsPatternMatch(row.Key, key) {
			row.Key = key
			return row, true
		}
	}
	return settings.Row{}, false
}

// modelsProviderRmOptions is one `models provider rm`.
type modelsProviderRmOptions struct {
	layer string
}

func newModelsProviderRmCmd() *cobra.Command {
	var o modelsProviderRmOptions
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a provider route from this project's config",
		Long: "Remove one entry of the project config's providers block whole.\n\n" +
			"The route, its claim on the models it served, its fee and the repositories\n" +
			"it allowed go together: what is left of the entry is the entry.\n\n" +
			"A repository that pins a model only this route served is not refused (D783\n" +
			"is about a model, and this is the route behind one). Each such repository's\n" +
			"next run fails at launch with no provider for the model, until the model is\n" +
			"claimed elsewhere or the pin is changed with `fugaro models use`.\n\n" +
			"Only the key it removes is rewritten: every comment and every other key of\n" +
			"the file is kept. `fugaro update --repo .` in a repository's own checkout is\n" +
			"what takes the route out of that repository's runs.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runModelsProviderRm(cmd, args[0], &o) },
	}
	cmd.Flags().StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	return cmd
}

func runModelsProviderRm(cmd *cobra.Command, name string, o *modelsProviderRmOptions) error {
	if err := modelsProviderLayer("providers."+name+".kind", o.layer); err != nil {
		return err
	}
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	provider, ok := lc.Providers[name]
	if !ok {
		return userErr("this project has no provider named %s, so there is nothing to remove: it has %s", oneLine(name), modelsRouteNames(lc))
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"providers", name}, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		if err, ok := modelsSpliceRefusal(sel.Path, err); ok {
			return err
		}
		return userErr("providers.%s: %v", name, err)
	}
	what := fmt.Sprintf("remove the provider %s from the project config", name)
	if len(provider.Models) > 0 {
		what += fmt.Sprintf(" (the route's claim on %s goes with it)", strings.Join(provider.Models, ", "))
	}
	if err := modelsEditLocal(cmd, lc, sel, settingsEdit{
		Old:     old,
		New:     next,
		What:    what,
		Command: "fugaro models provider rm " + quoteWord(name),
	}); err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		return nil
	}
	modelsProviderRmWarn(cmd, lc, name, provider)
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: "providers." + name + ".kind", Store: settings.StoreLocal})
}

// modelsProviderRmWarn names the break a removal makes, and only after it has
// happened: a refused write left every pin where it was, and a warning would
// send the owner to repair a break nothing caused. The repositories this
// machine cannot read are named too — one of them may pin a model this route
// was the only one to serve, which is exactly the case the owner has to know
// about — and a model another route still serves is not a break at all.
func modelsProviderRmWarn(cmd *cobra.Command, lc *localcfg.Config, name string, provider config.ModelProvider) {
	out := cmd.OutOrStdout()
	after := *lc
	after.Providers = make(map[string]config.ModelProvider, len(lc.Providers))
	for n, p := range lc.Providers {
		if n != name {
			after.Providers[n] = p
		}
	}
	var stranded []string
	cfgs, unseen := projectCheckoutConfigs(lc)
	for _, repo := range sortedRepoNames(lc) {
		cfg := cfgs[repo]
		if cfg == nil {
			continue
		}
		for _, role := range modelsRoleNames {
			id := modelsRoleModel(cfg.Agent, role)
			if id == "" || modelsRouteOf(&after, id) != "" {
				continue
			}
			stranded = append(stranded, fmt.Sprintf("%s (%s pins %s)", repo, role, id))
		}
	}
	if len(stranded) > 0 {
		fmt.Fprintf(out, "the route %s served models no other route serves now: %s. Each of those runs fails at launch with no provider for the model until the model is claimed elsewhere or the pin is changed with `fugaro models use`\n", name, strings.Join(stranded, ", "))
	}
	if len(unseen) > 0 {
		fmt.Fprintf(out, "this machine cannot read %s, so whether one of them pins a model only %s served is not known here\n", strings.Join(unseen, ", "), name)
	}
}

func newModelsPriceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "price",
		Short: "Write the price of one model in this project's config",
		Long: `Write the model_prices entry this project prices a model by.

A price is what a run charges a model's calls at, dollars per million tokens,
and the entry replaces the built-in catalog's row for that model whole — which
is what a model the catalog does not price needs at all. ` + "`models ls`" + ` shows
what each model is priced at today.`,
	}
	cmd.AddCommand(newModelsPriceSetCmd())
	return cmd
}

func newModelsPriceSetCmd() *cobra.Command {
	var o modelsPriceSetOptions
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Write the price leaves of one model in this project's config",
		Long: "Set the fields of one model_prices entry.\n\n" +
			"Only the flags you pass are written: every other leaf of the entry, every\n" +
			"comment and every other key of the file is kept.\n\n" +
			"An entry that is not there yet needs both --input-per-m and --output-per-m:\n" +
			"a model_prices entry states the two rates it is billed at, and the cache and\n" +
			"search rates default from them. On an entry the file already carries, any one\n" +
			"of the six leaves can be changed on its own.\n\n" +
			"Only the model_prices block is written: a model a provider route does not\n" +
			"serve yet is claimed by `fugaro models add`. `fugaro update` publishes the\n" +
			"policy node, which is what a run reads prices from.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runModelsPriceSet(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.Float64Var(&o.inputPerM, "input-per-m", 0, "input price, US dollars per million tokens")
	f.Float64Var(&o.outputPerM, "output-per-m", 0, "output price, US dollars per million tokens")
	f.Float64Var(&o.cacheWrite5m, "cache-write-5m", 0, "cache-write price for the 5-minute TTL, as a multiple of the input price")
	f.Float64Var(&o.cacheWrite1h, "cache-write-1h", 0, "cache-write price for the 1-hour TTL, as a multiple of the input price")
	f.Float64Var(&o.cacheRead, "cache-read", 0, "cache-read price, as a multiple of the input price")
	f.Float64Var(&o.webSearchPer1k, "web-search-per-1k", 0, "web-search price, US dollars per 1,000 searches")
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	return cmd
}

func runModelsPriceSet(cmd *cobra.Command, id string, o *modelsPriceSetOptions) error {
	if err := modelsPriceRefuseID(id); err != nil {
		return err
	}
	if err := modelsProviderLayer("model_prices."+id+".input_per_m", o.layer); err != nil {
		return err
	}
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	entry, exists := lc.ModelPrices[id]
	complete := exists && entry.InputPerM != nil && entry.OutputPerM != nil
	if !complete && !(cmd.Flags().Changed(modelsPriceInputFlag) && cmd.Flags().Changed(modelsPriceOutputFlag)) {
		return userErr("model_prices.%s is not an entry this file carries yet, and one states the two rates it is billed at: pass both --input-per-m and --output-per-m (US dollars per million tokens). The cache and search rates default from them, and each of the six can be changed on its own once the entry is there", oneLine(id))
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	values := map[string]string{
		"input_per_m":       modelsPriceText(o.inputPerM),
		"output_per_m":      modelsPriceText(o.outputPerM),
		"cache_write_5m":    modelsPriceText(o.cacheWrite5m),
		"cache_write_1h":    modelsPriceText(o.cacheWrite1h),
		"cache_read":        modelsPriceText(o.cacheRead),
		"web_search_per_1k": modelsPriceText(o.webSearchPer1k),
	}
	next := old
	line := []string{"fugaro", "models", "price", "set", quoteWord(id)}
	var wrote []string
	for _, leaf := range modelsPriceWriterLeaves {
		if !cmd.Flags().Changed(leaf.flag) {
			continue
		}
		out, err := yamledit.SetScalar(next, []string{"model_prices", id, leaf.leaf}, values[leaf.leaf])
		if err != nil {
			if err, ok := modelsSpliceRefusal(sel.Path, err); ok {
				return err
			}
			return userErr("model_prices.%s.%s: %v", id, leaf.leaf, err)
		}
		next = out
		wrote = append(wrote, leaf.leaf)
		line = append(line, leaf.flagArg(), values[leaf.leaf])
	}
	// The flags are checked before the file is read, so a call that names none
	// of them is a call with nothing to do rather than a write of the model's
	// current price back into itself.
	if len(wrote) == 0 {
		return userErr("models price set %s: pass at least one leaf to write (%s)", oneLine(id), modelsPriceFlagList())
	}
	if err := modelsEditLocal(cmd, lc, sel, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("set model_prices.%s in the project config (%s)", id, strings.Join(wrote, ", ")),
		Command: strings.Join(line, " "),
	}); err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "model_prices.%s carries %s now: next: fugaro update, which publishes the policy node a run reads prices from\n", id, strings.Join(wrote, ", "))
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: "model_prices." + id + "." + wrote[0], Store: settings.StoreLocal})
}

// modelsPriceRefuseID refuses a key no model_prices entry may carry: an alias
// names a family rather than a model, and a ":" variant is another price and
// another routing (modelsRefuseVariant). Both are the loader's own rules, said
// here so the owner reads them about the argument they typed.
func modelsPriceRefuseID(id string) error {
	switch {
	case strings.Contains(id, ":"):
		return modelsRefuseVariant(id)
	case pricing.IsAlias(id):
		return userErr("%s is not an explicit model ID: key model_prices by a model ID (such as claude-sonnet-5-5), which is what a pin and a call name", config.CodeSpan(id))
	}
	return nil
}

// modelsPriceFlagList is the leaves this command writes, as flags, for the
// refusal of a call that passed none.
func modelsPriceFlagList() string {
	return modelsWriterFlagList(modelsPriceWriterLeaves)
}
