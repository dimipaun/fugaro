package pricing

// Gemini on Vertex: the non-Claude models this build may send to Vertex, the ID
// Vertex spells each of them under, and what Vertex charges for them. GV3 of
// docs/model-credentials-routing/implementation-plan.md, D3193 of its
// reconciliation — the spec's `gemini-* → Vertex` walks here.
//
// A model has a third spelling beside the canonical ID a pin carries and a
// route's own ID, and CS3 put the Claude ones in the map in canonical.go. This
// is the other half of that one map, and it is a table of its own rather than
// rows of claudeRoutes because those rows have two columns about the Claude
// switch that would have to lie about a Gemini model: RouteID is a route's own
// `anthropic/<id>` claim, and Unmapped is the reason there is none — both are
// what config's resolver reads to answer where routing.claude.models sends a
// call, a question no Gemini model is the subject of. What the two halves share
// is the question they answer — "how is this model written on Vertex?" — and it
// is still answered in one place: vertexIDFor (canonical.go) reads this table
// when the Claude map has no row for the model, so a caller cannot get an
// answer from one half that the other contradicts.
//
// It is data, checked in, and nothing derives it: the same rule the Claude
// map states for its column, and for the same reason. A case fold, a separator
// swap or a suffix that "looks like" a model is a second answer to which model
// a live call names, and Vertex would serve it or refuse it for reasons nobody
// wrote down here — a wrong route and a wrong price at once.
//
// Which spelling Vertex really answers to, and whether the wire around it
// carries a tool call and a streamed answer, is what Check 886 (GV4,
// scripts/live-checks/) settles live: this file is what that check reads, and a
// spelling or a price that moves is a one-line change in the table below. U8 of
// the reconciliation is that open question, and O11 its cost.

// The Vertex Gemini rows' provenance: Vertex's own pricing page, and the day
// the prices below were read off it. Each row carries both
// (Model.PriceSource, Model.PriceCheckedAt), so the surfaces that print a price
// can say where it came from: the embedded table's own Source and CheckedAt are
// the Claude API pricing page's, and they cover the Claude rows alone. The
// figures below are that page's model pricing table, transcribed line by line
// on the date above — the rate the page states for the global endpoint, in US
// dollars per million tokens — and what confirms a live call is billed at them
// is Check 886 (GV4), which is the owner's to run.
const (
	vertexGeminiSource    = "https://cloud.google.com/vertex-ai/generative-ai/pricing"
	vertexGeminiCheckedAt = "2026-11-22"
)

// geminiImageTokens is the per-image ceiling a Gemini row reserves. Vertex
// bills an image as tiles of 258 tokens per 768x768 crop, so a 4K screenshot is
// about 3,870 tokens and a 6K one about 6,200; this sits above both, which is
// the direction the Claude rows' ceilings take and the one that cannot
// under-reserve a call. Like theirs it is a guess until something billed is
// measured, and 0 would refuse every image instead — a coder that cannot read a
// screenshot is not the model this row is here to make routable.
const geminiImageTokens = 8_000 // unverified: no billed Gemini image measured yet

// geminiOnVertex is one row: the canonical ID a pin carries and this table is
// keyed by, Vertex's own spelling of the same model — the ID the publisher path
// names, `publishers/google/models/<id>`, which GV1's translation puts in the
// request — and the model's Vertex prices in US dollars per million tokens.
//
// The canonical ID is `google/<family>`, not the bare ID Vertex spells, and
// that is the same spelling rule every non-Claude model in this build already
// follows: a pin names a `vendor/model` ID (IsAlias: a bare name that is
// neither a `claude-` ID nor a provider ID is an alias, and with the budget on
// a role may not pin one), and a price row is keyed by what a pin may name.
// `deepseek-*` in the spec's example routing entry is the same shorthand for
// the same reason: what a project writes in `match` and pins is
// `deepseek/deepseek-v4-flash`. The vendor is "google" because that is the
// publisher Vertex itself names — `publishers/google/models/gemini-3-pro`. So
// the Vertex column is a translation here rather than an identity, which is
// what the map is for.
//
// The two prices are the page's list prices for the global endpoint, which is
// the endpoint this build's Vertex calls are made against; a regional endpoint
// prices higher, and that difference is `model_prices`' to state, exactly as it
// is for a Claude model on a premium region (table.go's own note).
//
// LongContext is the page's other price for a model it charges two of: Vertex
// bills a prompt past its own input threshold at higher rates, and a row with
// only the first price would cap such a call below what Vertex charges it
// (WorstCase takes the tier's rate once the bound passes the threshold). nil is
// a model Vertex prices one way.
type geminiOnVertex struct {
	Canonical       string
	Vertex          string
	InputPerM       float64
	OutputPerM      float64
	LongContext     *Tier
	ContextTokens   int64
	MaxOutputTokens int64
}

// geminiOnVertexModels is the catalog, in canonical-ID order, and it is not
// part of this package's API: the pairs are readable through SpellingFor and
// the price table alone, so a consumer cannot read the data and re-answer
// "where does this model go and what does it cost" its own way (canonical.go
// states that rule for the map this table is the other half of).
//
// Every row is priced: a Gemini model with a spelling and no price would be a
// model a chain may route to Vertex and no cap can charge, which is the state
// D3020 refuses a run in every mode rather than charging a guess
// (TestAnUnpricedGeminiModelIsRefused).
var geminiOnVertexModels = []geminiOnVertex{
	// Gemini 3 Flash: $0.50 / $3, one price for any prompt; 1M context, 64k
	// out. Pricing page, model pricing table, the global endpoint. Cache and
	// grounding have no price of the shape this table states (see
	// vertexGeminiRows).
	{Canonical: "google/gemini-3-flash", Vertex: "gemini-3-flash", InputPerM: 0.5, OutputPerM: 3, ContextTokens: context1M, MaxOutputTokens: output64k},
	// Gemini 3 Pro: $2 / $12 up to 200k input tokens and $4 / $18 past that;
	// 1M context, 64k out. Pricing page, model pricing table, the global
	// endpoint, both of the two rows it states for the model.
	{Canonical: "google/gemini-3-pro", Vertex: "gemini-3-pro", InputPerM: 2, OutputPerM: 12,
		LongContext:   &Tier{AboveInputTokens: 200_000, InputPerM: 4, OutputPerM: 18},
		ContextTokens: context1M, MaxOutputTokens: output64k},
}

// vertexGeminiID answers Vertex's own spelling of a canonical Gemini ID, and
// whether this build carries the model at all. The second half is the
// fail-closed direction: a model with no row has no Vertex spelling, and a
// caller that sent the ID it asked with anyway would be calling a model nobody
// checked Vertex for (D3175) — which is the whole of what this table is here to
// prevent.
func vertexGeminiID(canonical string) (string, bool) {
	for _, g := range geminiOnVertexModels {
		if g.Canonical == canonical {
			return g.Vertex, g.Vertex != ""
		}
	}
	return "", false
}

// vertexGeminiRows is the catalog as price-table rows, for Embedded to add:
// one row per model, keyed by the canonical ID a pin names.
//
// Vertex's own spelling is deliberately not an alias on the row, unlike
// claude-haiku-4-5's "@" snapshot: a Claude model's Vertex spelling is an ID a
// pin may name (modelIDRE admits it), and this one is not — a bare `gemini-3-pro`
// is an alias to IsAlias, so nothing may pin it, route it or charge it, and a
// row reachable by it would be a second answer to what a call naming it costs.
// The map is that spelling's only authority (vertexGeminiID), which is the
// point of it being one checked-in fact and not a grammar.
//
// Two dimensions are stated rather than left at 0, because a rate of 0 is
// tokens charged nothing:
//
//   - Cache. Vertex prices Gemini's cached tokens as a context cache per hour
//     per token, a shape this table has no field for, and nothing here has read
//     that figure; charging a cached token at the input rate (a multiplier of
//     1) over-charges a call rather than letting one through free. It is the
//     fold ApplyCatalog takes for a cache dimension the file does not price,
//     and it is the safe direction until someone measures what a Gemini call
//     through the service really reports.
//   - Grounding. Vertex bills search and grounding per request, not per 1,000
//     searches as the Claude API does, so WebSearchPer1k stays 0 — the one
//     dimension with no conservative fold (catalog_apply.go states the same
//     rule for a catalog row).
func vertexGeminiRows() []Model {
	rows := make([]Model, 0, len(geminiOnVertexModels))
	for _, g := range geminiOnVertexModels {
		m := Model{
			ID:              g.Canonical,
			ContextTokens:   g.ContextTokens,
			MaxOutputTokens: g.MaxOutputTokens,
			ImageTokens:     geminiImageTokens,
			PriceSource:     vertexGeminiSource,
			PriceCheckedAt:  vertexGeminiCheckedAt,
			Rates: Rates{
				InputPerM: g.InputPerM, OutputPerM: g.OutputPerM,
				CacheWrite5m: 1, CacheWrite1h: 1, CacheRead: 1,
			},
		}
		// The tier is copied rather than shared: Embedded() hands out its own
		// table, and a row of it that pointed at the catalog's own Tier would
		// let one caller's write change what every later table prices.
		if g.LongContext != nil {
			t := *g.LongContext
			m.Rates.LongContext = &t
		}
		rows = append(rows, m)
	}
	return rows
}
