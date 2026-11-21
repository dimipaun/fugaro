package cli

import (
	"strings"
	"testing"
)

// TestDocsSpendAccuracy pins the sentences the spend-accuracy plan's SA6.1 is
// about, in the document that carries each one: what a provider-routed call
// settles on now — the provider's own reported cost with the route fee,
// floored at the table's charge for the same usage at the tier's cheapest end
// (D2640, D2651) — that `highest` is the default `price_policy` an owner
// leaves unset (D2644, superseding owner ruling 3), how a `model_prices` cache
// field is priced in dollars per million (D2645, D2646), and what the drift
// alarm reads over a project's days (D2649, D2650).
//
// The negative half is the red step the task ships with: "cheapest stays the
// default (owner ruling 3)" and the "never settles a call the table priced"
// sentence beside it are what the two rulings reversed, and a document that
// still carries one tells an owner the opposite of what the gateway does. The
// user doc's §2 and §6 claims are read out of their own sections (docSection),
// so a sentence that only exists elsewhere cannot satisfy them.
func TestDocsSpendAccuracy(t *testing.T) {
	const multiPath = "docs/multi-model.md"
	multi := readDoc(t, multiPath)
	flatMulti := flattenDoc(multi)
	section2 := flattenDoc(docSection(t, multiPath, multi, "## 2. The project config (owner only)"))
	section6 := flattenDoc(docSection(t, multiPath, multi, "## 6. Reading the cost"))
	catalog := flattenDoc(readDoc(t, "docs/provider-price-catalog/spec.md"))
	m10 := flattenDoc(readDoc(t, "docs/m10-multi-model/spec.md"))
	v1 := flattenDoc(readDoc(t, "docs/design/v1.md"))

	for _, c := range []struct {
		path, doc, what, phrase string
	}{
		{multiPath, section2, "that `highest` is the default `price_policy` (D2644)",
			"`price_policy` picks the end of that set's range — `cheapest` or `highest` (the default, since D2644)"},
		{multiPath, section2, "what the default costs an owner at the caps",
			"**`highest` is the default because it is the safe end, and what it costs is real:**"},
		{multiPath, section2, "the cap halt a dearest-end reservation brings sooner",
			"so a run whose cap cannot hold such a call halts with `run cap $X cannot hold a call that needs up to $Y`"},
		{multiPath, section2, "the retryable 429 a call that fits only alone is refused",
			"refused the retryable 429 that waits for the calls already out"},
		{multiPath, section2, "that `_per_m` is what a list price is printed in (D2645)",
			"**The cache fields go in either way, and the dollars-per-million one is what a list price is printed in.**"},
		{multiPath, flatMulti, "the dollars-per-million spelling in the config example",
			"cache_read_per_m: 0.014"},
		{multiPath, section2, "that `fugaro update` renames the old cache keys in the owner's own file (D2646)",
			"`fugaro update` renames them to `cache_read_mult` and its two siblings **in your own file**"},
		{multiPath, section6, "that a complete provider call settles on the provider's own cost (D2640)",
			"**a complete call on a provider route is charged the cost the response reported for it plus the route fee**"},
		{multiPath, section6, "the floor under that settle, and that a zero report is floored too (D2651)",
			"floored at the table's charge for the same usage at the tier's cheapest end, so a provider that under-reports or reports a zero cannot make a call cheaper than the table prices it (D2640, D2651;"},
		{multiPath, section6, "the `Settled:` line and the band it prints",
			"`Settled: $1.2345 (reported on 40 of 41 calls); table band $1.1000-$1.4000`"},
		{multiPath, section6, "the drift alarm and the window it reads (D2649, D2650)",
			"**The drift alarm reads the same band over the project's days, for a model rather than a run.**"},
		{multiPath, section6, "the drift alarm's 10% either side, over a model with at least $1 of spend",
			"more than **10% above** the table's dearest end or more than **10% below** its cheapest one, for a model with at least $1 of spend in that window (D2649, D2650)"},
		{"docs/provider-price-catalog/spec.md", catalog, "that the route switch defaults to `highest` (D2644)",
			"default **`highest`** since the spend-accuracy reconciliation's D2644"},
		{"docs/provider-price-catalog/spec.md", catalog, "that D2644 supersedes ruling 3",
			"**D2644 supersedes owner ruling 3:**"},
		{"docs/provider-price-catalog/spec.md", catalog, "that D2640 changed what bound (3) is",
			"**D2640 changes bound (3):**"},
		{"docs/m10-multi-model/spec.md", m10, "that the switch defaults to `highest` there too (D2644)",
			"default `highest` since the spend-accuracy reconciliation's D2644"},
		{"docs/m10-multi-model/spec.md", m10, "that the phase 3 question of trusting `reported` is answered",
			"the question this paragraph leaves open is answered, and it was answered on 2026-11-15 without waiting for the observed week — D2640 of"},
		{"docs/m10-multi-model/spec.md", m10, "the phase 3 line pointing at D2640",
			"**answered 2026-11-15: D2640 of [../spend-accuracy/reconciliation.md](../spend-accuracy/reconciliation.md) adopts it, and it is built**"},
		{"docs/design/v1.md", v1, "that the gateway settles a complete provider call on the report",
			"**settles a complete call on a provider route**"},
		{"docs/design/v1.md", v1, "which calls keep the table's charge",
			"the table price is what settles the calls that report none"},
		{"docs/design/v1.md", v1, "that a provider model is charged the reported cost with the fee",
			"a complete call on a provider route is charged the cost the provider reported for it plus the route fee"},
		{"docs/design/v1.md", v1, "that the report is read against the table's band",
			"What the report is read against is the table's band over the same calls"},
		{"docs/design/v1.md", v1, "that a gap is the owner's row to correct and never an automatic rewrite",
			"correcting it stays the owner's: Fugaro never rewrites a price row by itself"},
	} {
		if !strings.Contains(c.doc, c.phrase) {
			t.Errorf("%s does not state %s: %q is missing", c.path, c.what, c.phrase)
		}
	}

	for _, c := range []struct{ path, doc, banned, why string }{
		{"docs/provider-price-catalog/spec.md", catalog, "`cheapest` stays the default (owner ruling 3)",
			"D2644 supersedes owner ruling 3, and this is the sentence it reversed"},
		{"docs/provider-price-catalog/spec.md", catalog, "**never settles** a call the table priced",
			"D2640 settles a complete call on the provider's report"},
		{multiPath, flatMulti, "cheapest (the default)",
			"`highest` is the default `price_policy` since D2644"},
	} {
		if strings.Contains(c.doc, c.banned) {
			t.Errorf("%s still carries %q: %s", c.path, c.banned, c.why)
		}
	}
}
