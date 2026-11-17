package pricing

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// withCatalog points the undercut check at a stated file for the test's
// duration: the file a released binary reads is the embedded one, and no test
// edits that.
func withCatalog(t *testing.T, f *catalog.File) {
	t.Helper()
	prev := loadCatalog
	loadCatalog = func() (*catalog.File, error) { return f, nil }
	t.Cleanup(func() { loadCatalog = prev })
}

// claimed is a route whose model the owner has priced by hand: the shape
// internal/config hands the check for every key of model_prices, where the
// model stays in the route's Overridden list and no row is compiled for it.
func claimed(requireZDR bool) []CatalogRoute {
	return []CatalogRoute{{Name: "openrouter", Overridden: []string{"vendor/model"}, RequireZDR: requireZDR}}
}

// atZDRTier is an override at the zdr tier's own cheapest end of the fixture
// entry: input $2, output $4, cache read 0.25 of input, the list multipliers,
// the list search price.
func atZDRTier() Override {
	return Override{Rates: Rates{
		InputPerM: 2, OutputPerM: 4, CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h,
		CacheRead: 0.25, WebSearchPer1k: DefaultWebSearchPer1k,
	}}
}

// TestUndercutsCatalog pins the check R4 reports: every dimension of an
// override below the cheapest end of the route's own tier is named, with the
// override's price and the catalog's in dollars, and no other dimension is.
// The route's tier is what decides — a comparison against the file's cheapest
// tier would miss a zdr route's floor — and an override at the floor is not
// below it.
func TestUndercutsCatalog(t *testing.T) {
	withCatalog(t, catalogFile("2026-10-14", entry()))

	cases := []struct {
		name       string
		requireZDR bool
		override   Override
		want       []string
	}{
		{
			name: "only the dimension below the tier is named", requireZDR: true,
			override: Override{Rates: Rates{
				InputPerM: 2.5, OutputPerM: 4, CacheRead: 0.1,
				CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h, WebSearchPer1k: DefaultWebSearchPer1k,
			}},
			want: []string{
				"model_prices.vendor/model.cache_read_per_m is $0.25 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.5 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			},
		},
		{
			name: "under the all tier's floor is not under a zdr route's", requireZDR: false,
			override: Override{Rates: Rates{
				InputPerM: 1.5, OutputPerM: 3, CacheRead: 0.2,
				CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h, WebSearchPer1k: DefaultWebSearchPer1k,
			}},
		},
		{
			name: "the same override under a zdr route names each dimension the tier prices", requireZDR: true,
			override: Override{Rates: Rates{
				InputPerM: 1.5, OutputPerM: 3, CacheRead: 0.2,
				CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h, WebSearchPer1k: DefaultWebSearchPer1k,
			}},
			want: []string{
				"model_prices.vendor/model.input_per_m is $1.5 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $2 per million tokens (checked 2026-10-14): the override undercuts the catalog",
				"model_prices.vendor/model.output_per_m is $3 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $4 per million tokens (checked 2026-10-14): the override undercuts the catalog",
				"model_prices.vendor/model.cache_read_per_m is $0.3 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.5 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			},
		},
		{
			name: "an override at the tier's cheapest end is not below it", requireZDR: true,
			override: atZDRTier(),
		},
		{
			name: "a cache multiplier left at 0 is read as the table reads it", requireZDR: true,
			override: Override{Rates: Rates{
				InputPerM: 2, OutputPerM: 4, CacheWrite5m: 0, CacheWrite1h: 0, CacheRead: 0,
				WebSearchPer1k: DefaultWebSearchPer1k,
			}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UndercutsCatalog("vendor/model", c.override, claimed(c.requireZDR))
			if len(got) != len(c.want) {
				t.Fatalf("named %d dimensions %q, want %d %q", len(got), got, len(c.want), c.want)
			}
			for i, w := range c.want {
				if got[i] != w {
					t.Errorf("message %d:\n got %s\nwant %s", i, got[i], w)
				}
			}
		})
	}

	// Every dimension the file can price is compared, each in its own unit: a
	// search price is not a token price. The write dimensions are ratios of the
	// input price at both ends, so an override under the input price is under
	// them by the same multiple, and a free search is under a tier that prices
	// one.
	t.Run("every dimension the tier prices", func(t *testing.T) {
		e := entry()
		e.ZDR["cache_write_5m_per_m"] = bound("2", "4")
		e.ZDR["cache_write_1h_per_m"] = bound("3", "6")
		e.ZDR["web_search_per_1k"] = bound("10", "12")
		withCatalog(t, catalogFile("2026-10-14", e))
		ov := Override{Rates: Rates{
			InputPerM: 1, OutputPerM: 1, CacheRead: 0.1,
			CacheWrite5m: 0.5, CacheWrite1h: 0.5, WebSearchPer1k: 0,
		}}
		got := UndercutsCatalog("vendor/model", ov, claimed(true))
		want := []string{
			"model_prices.vendor/model.input_per_m is $1 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $2 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			"model_prices.vendor/model.output_per_m is $1 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $4 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			"model_prices.vendor/model.cache_read_per_m is $0.1 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.5 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			"model_prices.vendor/model.cache_write_5m_per_m is $0.5 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $2 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			"model_prices.vendor/model.cache_write_1h_per_m is $0.5 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $3 per million tokens (checked 2026-10-14): the override undercuts the catalog",
			"model_prices.vendor/model.web_search_per_1k is $0 per 1,000 searches and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $10 per 1,000 searches (checked 2026-10-14): the override undercuts the catalog",
		}
		if len(got) != len(want) {
			t.Fatalf("named %d dimensions %q, want %d", len(got), got, len(want))
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("message %d:\n got %s\nwant %s", i, got[i], w)
			}
		}
	})

	// A dimension the tier prices no endpoint of has no catalog price to fall
	// below: the row's value there is the compile's fold, and
	// reporting it would warn about the write multipliers of every model the
	// file prices no writes for.
	t.Run("a dimension the tier does not price", func(t *testing.T) {
		ov := Override{Rates: Rates{
			InputPerM: 2, OutputPerM: 4, CacheRead: 0.25,
			CacheWrite5m: 0.1, CacheWrite1h: 0.1, WebSearchPer1k: DefaultWebSearchPer1k,
		}}
		if got := UndercutsCatalog("vendor/model", ov, claimed(true)); got != nil {
			t.Errorf("named %q for dimensions the tier prices nothing of", got)
		}
	})

	// A route that claims the model outright is read the same way: the check
	// compiles the row the owner's override replaced, whichever of the route's
	// two lists the model is in.
	t.Run("a route that claims the model by name", func(t *testing.T) {
		routes := []CatalogRoute{{Name: "openrouter", Models: []string{"vendor/model"}, RequireZDR: true}}
		ov := atZDRTier()
		ov.InputPerM = 1
		// The dimensions that scale with the input price are held above the
		// tier's own, so only the input dimension is below it.
		ov.CacheRead, ov.CacheWrite5m, ov.CacheWrite1h = 0.6, 3, 5
		got := UndercutsCatalog("vendor/model", ov, routes)
		if len(got) != 1 || !strings.HasPrefix(got[0], "model_prices.vendor/model.input_per_m is $1 per million tokens") {
			t.Errorf("named %q, want the input dimension alone", got)
		}
	})

	// A file the check cannot read leaves it silent rather than comparing
	// against half of one.
	t.Run("a catalog that will not load", func(t *testing.T) {
		prev := loadCatalog
		loadCatalog = func() (*catalog.File, error) { return nil, errors.New("no catalog") }
		t.Cleanup(func() { loadCatalog = prev })
		ov := atZDRTier()
		ov.InputPerM = 1
		if got := UndercutsCatalog("vendor/model", ov, claimed(true)); got != nil {
			t.Errorf("named %q out of a file that would not load", got)
		}
	})
}

// TestUndercutUnknownModelSilent pins what the check does not report: a model
// the catalog does not price, one no route claims, one the catalog cannot price
// under the route's switches, and a Claude row of the embedded table, which no
// catalog row ever replaced — its Lo is its Rates, so the effective table
// cannot answer for it either, and neither can this.
func TestUndercutUnknownModelSilent(t *testing.T) {
	low := Override{Rates: Rates{
		InputPerM: 0.01, OutputPerM: 0.01, CacheRead: 0.01,
		CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h, WebSearchPer1k: DefaultWebSearchPer1k,
	}}

	t.Run("a model the catalog does not price", func(t *testing.T) {
		withCatalog(t, catalogFile("2026-10-14", entry()))
		routes := []CatalogRoute{{Name: "openrouter", Overridden: []string{"vendor/other"}, RequireZDR: true}}
		if got := UndercutsCatalog("vendor/other", low, routes); got != nil {
			t.Errorf("named %q for a model the catalog does not price", got)
		}
	})

	t.Run("a model no route claims", func(t *testing.T) {
		withCatalog(t, catalogFile("2026-10-14", entry()))
		if got := UndercutsCatalog("vendor/model", low, nil); got != nil {
			t.Errorf("named %q for a model no route claims", got)
		}
	})

	t.Run("a model the route's switches cannot price", func(t *testing.T) {
		e := entry()
		e.ZDR = nil
		withCatalog(t, catalogFile("2026-10-14", e))
		if got := UndercutsCatalog("vendor/model", low, claimed(true)); got != nil {
			t.Errorf("named %q for a model with no ZDR-eligible endpoint", got)
		}
	})

	t.Run("a row the price catalog does not carry", func(t *testing.T) {
		routes := []CatalogRoute{{Name: "openrouter", Overridden: []string{"claude-sonnet-5-5"}, RequireZDR: true}}
		if got := UndercutsCatalog("claude-sonnet-5-5", low, routes); got != nil {
			t.Errorf("named %q for a row the built-in price catalog does not carry", got)
		}
	})
}
