package pricing

// The embedded table's source and the day it was last checked against it.
const (
	source    = "https://platform.claude.com/docs/en/about-claude/pricing"
	checkedAt = "2026-09-30"
)

// Limits shared by the rows below, from the models overview
// (https://platform.claude.com/docs/en/about-claude/models/overview) and
// the vision guide (https://platform.claude.com/docs/en/build-with-claude/vision).
const (
	context1M   = 1_000_000
	context200k = 200_000
	output128k  = 128_000
	output64k   = 64_000

	// The vision guide caps one image at 4,784 visual tokens on
	// high-resolution models (Claude 4.7 and later) and 1,568 on the
	// others; these ceilings sit above that, since the token count
	// billed for an image is not documented exactly.
	imageHighRes  = 6_000 // unverified: no billed image measured yet
	imageStandard = 2_000 // unverified: no billed image measured yet

	// Web search: $10 per 1,000 searches on every model.
	webSearchPer1k = 10
)

// rates builds a row's rates: writes at the list multipliers (1.25x for
// five minutes, 2x for an hour) and the model's own cache-read multiplier.
func rates(inputPerM, outputPerM, cacheRead float64) Rates {
	return Rates{
		InputPerM: inputPerM, OutputPerM: outputPerM,
		CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: cacheRead,
		WebSearchPer1k: webSearchPer1k,
	}
}

// Embedded is the price table built into Fugaro, a fresh copy on every
// call. None of these models has a long-context tier: the pricing page
// says Claude 4.6 and later models bill the full 1M context window at
// standard rates. Vertex serves the dateless models under the same ID;
// where Vertex's own price differs (regional endpoints carry a premium),
// owners set model_prices. Models still served but missing here (Fable 5,
// Opus 4.6 to 4.8, Sonnet 4.6, ...) must be added through model_prices
// before a budget can pin them.
func Embedded() *Table {
	models := []Model{
		// Claude Opus 5.5: $4 / $20; cache hits $0.20 (0.05x). Pricing page, model pricing table, footnote 2.
		{ID: "claude-opus-5-5", ContextTokens: context1M, MaxOutputTokens: output128k, ImageTokens: imageHighRes, Rates: rates(4, 20, 0.05)},
		// Claude Opus 5: $5 / $25; cache hits $0.50 (0.1x). Pricing page, model pricing table.
		{ID: "claude-opus-5", ContextTokens: context1M, MaxOutputTokens: output128k, ImageTokens: imageHighRes, Rates: rates(5, 25, 0.1)},
		// Claude Sonnet 5.5: $2 / $10; cache hits $0.20 (0.1x). Pricing page, model pricing table.
		{ID: "claude-sonnet-5-5", ContextTokens: context1M, MaxOutputTokens: output128k, ImageTokens: imageHighRes, Rates: rates(2, 10, 0.1)},
		// Claude Sonnet 5: $2 / $10 (the introductory price made standard, footnote 3); cache hits $0.20 (0.1x). Pricing page, model pricing table.
		{ID: "claude-sonnet-5", ContextTokens: context1M, MaxOutputTokens: output128k, ImageTokens: imageHighRes, Rates: rates(2, 10, 0.1)},
		// Claude Haiku 4.5: $1 / $5; cache hits $0.10 (0.1x). Pricing page, model pricing table.
		// The Claude API's dated snapshot and Vertex's "@" spelling are aliases (models overview).
		{ID: "claude-haiku-4-5", Aliases: []string{"claude-haiku-4-5-20251001", "claude-haiku-4-5@20251001"},
			ContextTokens: context200k, MaxOutputTokens: output64k, ImageTokens: imageStandard, Rates: rates(1, 5, 0.1)},
		// Claude Fable 5.1: $10 / $50; cache hits $0.25 (0.025x). Pricing page, model pricing table, footnote 1.
		{ID: "claude-fable-5-1", ContextTokens: context1M, MaxOutputTokens: output128k, ImageTokens: imageHighRes, Rates: rates(10, 50, 0.025)},
	}
	t := &Table{Source: source, CheckedAt: checkedAt, Models: make(map[string]Model, len(models))}
	for _, m := range models {
		t.Models[m.ID] = m
	}
	return t
}
