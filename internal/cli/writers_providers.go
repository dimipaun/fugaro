package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// writers_providers.go registers what writes the models family's own rows —
// the provider block a route is and the price leaves a model's row carries
// (D2613; docs/contracts/config-ownership.md §4 puts `providers.*.*` and
// `model_prices.*.*` here).
//
// One pattern per leaf, because the argv differs per leaf. `<1>` is the key's
// segment at the pattern's first `*` — the provider's or the model's name, the
// word each command takes as its argument — and the whole key is never what a
// flag accepts: `providers.acme.base_url` is not a name. The leaf's own flag
// follows, with the contract's value placeholder where the value goes. A
// single `providers.*.*` would have to state one argv for every leaf, and
// `secret` is the leaf that makes that impossible: it names a stored secret,
// so what the flag takes is a reference and never the value.
//
// Two kinds of registry row are deliberately left unwritten here, for T1's
// golden or for L1: `providers.*.models`, whose writer is `models add`/`models
// rm` rather than either command below, and the model_prices leaves no flag of
// `models price set` takes (`image_tokens`, and the `long_context` pair under
// a second star).

// modelsProviderSegment is the contract's first segment placeholder: the word
// an Argv template writes where the key's segment at the pattern's first `*`
// goes (docs/contracts/config-ownership.md §2).
const modelsProviderSegment = "<1>"

// modelsValuePlaceholder is the contract's value placeholder: the word an Argv
// template writes where the value goes, which CR1 (#2842) names
// `settings.ValuePlaceholder`. It is spelled here rather than imported because
// that constant does not exist on the base this branch is written against; the
// string is the one the contract's own example carries, so a family reaching
// for either spells the same argv, and T1 is what fills it per row.
const modelsValuePlaceholder = "<value>"

// modelsWriterLeaf is one leaf a command below writes: the YAML key, and the
// flag name that writes it. The two differ for the price leaves, whose keys
// are underlined (`input_per_m`) and whose flags are dashed
// (`--input-per-m`).
type modelsWriterLeaf struct {
	leaf string
	flag string
}

// flagArg is the flag as a command line spells it.
func (l modelsWriterLeaf) flagArg() string { return "--" + l.flag }

// modelsProviderWriterLeaves are the provider leaves `models provider set`
// writes, in the order that command declares its flags: the order it writes
// them in, and the order a refusal or a confirmation names them in.
var modelsProviderWriterLeaves = []modelsWriterLeaf{
	{leaf: "kind", flag: "kind"},
	{leaf: "base_url", flag: "base-url"},
	{leaf: "auth", flag: "auth"},
	{leaf: "secret", flag: "secret"},
	{leaf: "price_policy", flag: "price-policy"},
	{leaf: "require_zdr", flag: "require-zdr"},
	{leaf: "route_fee_pct", flag: "route-fee-pct"},
	{leaf: "allow_data_to", flag: "allow-data-to"},
}

// modelsPriceInputFlag and modelsPriceOutputFlag are the two leaves a
// model_prices entry cannot do without, as the command spells them: an entry
// that is not there yet is refused unless both were passed, which is the
// loader's own rule (localcfg's Overrides).
const (
	modelsPriceInputFlag  = "input-per-m"
	modelsPriceOutputFlag = "output-per-m"
)

// modelsPriceWriterLeaves are the model_prices leaves `models price set`
// writes, in the same order.
var modelsPriceWriterLeaves = []modelsWriterLeaf{
	{leaf: "input_per_m", flag: modelsPriceInputFlag},
	{leaf: "output_per_m", flag: modelsPriceOutputFlag},
	{leaf: "cache_write_5m", flag: "cache-write-5m"},
	{leaf: "cache_write_1h", flag: "cache-write-1h"},
	{leaf: "cache_read", flag: "cache-read"},
	{leaf: "web_search_per_1k", flag: "web-search-per-1k"},
}

// modelsWriterRow is one registration: the pattern and the writer it answers
// for. The rows are a slice rather than a map so that the two loops below
// register in the order they are written, which is the order a panic names
// them in (the table itself is sorted by the registry).
type modelsWriterRow struct {
	pattern string
	writer  settings.Writer
}

// modelsWriterRows is every registration this file makes, provider leaves
// first.
var modelsWriterRows = func() []modelsWriterRow {
	rows := make([]modelsWriterRow, 0, len(modelsProviderWriterLeaves)+len(modelsPriceWriterLeaves))
	for _, block := range []struct {
		prefix  string
		group   string
		command string
		leaves  []modelsWriterLeaf
	}{
		{prefix: "providers.*.", group: "provider", command: "fugaro models provider set", leaves: modelsProviderWriterLeaves},
		{prefix: "model_prices.*.", group: "price", command: "fugaro models price set", leaves: modelsPriceWriterLeaves},
	} {
		for _, leaf := range block.leaves {
			rows = append(rows, modelsWriterRow{
				pattern: block.prefix + leaf.leaf,
				writer: settings.Writer{
					Command: block.command,
					Argv:    []string{"models", block.group, "set", modelsProviderSegment, leaf.flagArg(), modelsValuePlaceholder},
					Store:   settings.StoreLocal,
				},
			})
		}
	}
	return rows
}()

// The registration is a package init and its error is a programming error: a
// pattern another family claimed, or a writer outside the contract's
// vocabulary, fails every command of this binary loudly rather than leaving
// one of two registrants to win silently
// (docs/contracts/config-ownership.md §2).
func init() {
	for _, row := range modelsWriterRows {
		if err := settings.RegisterWriter(row.pattern, row.writer); err != nil {
			panic(err)
		}
	}
}
