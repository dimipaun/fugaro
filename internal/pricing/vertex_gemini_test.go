package pricing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Gemini catalog's content, written out here rather than read from the
// table it checks (canonical_test.go's reason for expectedPairs and
// expectedVertex): a swap between two rows round-trips perfectly, and so does a
// spelling or a price nobody decided.
//
// The canonical ID is the `google/<family>` a pin names and Vertex is what
// Vertex is sent, so the pair is a translation and not an identity — writing
// the two out separately is what holds vertex_gemini.go to that. The prices are
// Vertex's list prices for the global endpoint, in US dollars per million
// tokens, as read on vertexGeminiCheckedAt, and Long is the second price the
// page states for a model it charges two of: a flat row would cap what Vertex
// bills past the threshold below the real figure.
var expectedGemini = map[string]struct {
	Vertex string
	Input  float64
	Output float64
	Long   *Tier
}{
	"google/gemini-3-flash": {"gemini-3-flash", 0.5, 3, nil},
	"google/gemini-3-pro":   {"gemini-3-pro", 2, 12, &Tier{AboveInputTokens: 200_000, InputPerM: 4, OutputPerM: 18}},
}

// geminiNotCarried are the Gemini models this build does not carry: Vertex
// serves the family (the 2.5 one, and the 4 the spec's own example routing
// entry names), so each is a model somebody may reasonably write down, and
// every one of them must be refused rather than answered.
var geminiNotCarried = []string{
	"google/gemini-2.5-pro",
	"google/gemini-4",
	"google/gemini-3-pro-preview",
	"GOOGLE/GEMINI-3-PRO",
	"gemini-3-pro",
	"",
}

// TestSpellingForGeminiOnVertex is GV3's pin on the Vertex spelling of a Gemini
// model (docs/model-credentials-routing/implementation-plan.md): the checked-in
// catalog's pairs, exactly, and the fail-closed direction around them — a
// Gemini model this build does not carry has no Vertex spelling, and is never
// answered the ID that came in (D3175).
//
// Hand mutations this fails on: writing Vertex's spelling as the canonical ID
// (the pair is a translation, and the assertion that the two differ is what
// says so); dropping a row (the model then answers false); swapping two rows'
// spellings (the fixture is written out, not derived); and letting the second
// table shadow the first (a Claude row would stop answering).
func TestSpellingForGeminiOnVertex(t *testing.T) {
	for canonical, want := range expectedGemini {
		got, ok := SpellingFor("vertex", canonical)
		if !ok || got != want.Vertex {
			t.Errorf("SpellingFor(%q, %q) = (%q, %v), want (%q, true)", "vertex", canonical, got, ok, want.Vertex)
		}
		if got == canonical {
			t.Errorf("SpellingFor(%q, %q) answered the ID that came in: a pin names %q and Vertex is sent %q, and the map is what translates one into the other", "vertex", canonical, canonical, want.Vertex)
		}
	}
	if len(geminiOnVertexModels) != len(expectedGemini) {
		t.Fatalf("the Gemini catalog has %d rows and the fixture %d: every row is a decision somebody wrote down", len(geminiOnVertexModels), len(expectedGemini))
	}
	for _, g := range geminiOnVertexModels {
		if _, ok := expectedGemini[g.Canonical]; !ok {
			t.Errorf("the catalog carries %s, which the fixture does not name: a spelling for a model nobody decided is exactly the guess the map exists to remove", g.Canonical)
		}
	}

	// One reader, two tables: the Vertex column is the Claude map's rows and
	// the Gemini catalog's, and neither answers for the other's models. This is
	// the assertion a second table in vertexIDFor could quietly break — a
	// Gemini row keyed by a Claude ID, or a lookup that stopped at the wrong
	// half, would take a Claude call off its measured spelling.
	for _, r := range claudeRoutes {
		if _, ok := expectedGemini[r.Canonical]; ok {
			t.Errorf("%s is a Claude row and a Gemini row at once: two tables spelling one model is two answers to where its call goes", r.Canonical)
		}
		if got, ok := SpellingFor("vertex", r.Canonical); !ok || got != r.Vertex {
			t.Errorf("SpellingFor(%q, %q) = (%q, %v), want (%q, true): the Gemini catalog must not shadow a Claude row", "vertex", r.Canonical, got, ok, r.Vertex)
		}
	}

	// The fail-closed direction, for the kinds that translate: a Gemini model
	// this build does not carry, and a model whose spelling was written as
	// something near the map's own, are both "no spelling" — and a provider
	// route is refused a Google model in every case, because no route ID was
	// measured for one (RouteIDFor).
	for _, model := range geminiNotCarried {
		if got, ok := SpellingFor("vertex", model); ok {
			t.Errorf("SpellingFor(%q, %q) = (%q, true), want no answer: a source this map does not spell a model for may not be sent it", "vertex", model, got)
		}
	}
	for _, model := range append(geminiNotCarried, "google/gemini-3-pro") {
		if got, ok := SpellingFor("provider", model); ok {
			t.Errorf("SpellingFor(%q, %q) = (%q, true), want no answer: no provider route's ID has been measured for a Google model, and Vertex's own spelling is not one", "provider", model, got)
		}
	}
}

// TestAnUnpricedGeminiModelIsRefused is GV3's other half: the price table
// carries Vertex's price for every model the catalog lists — the page's second
// price too, where it states one — with the date those prices were read, and a
// Gemini model it does not carry has no price at all —
// refused in every budget mode, as any unpriced model is (always-a-budget
// D3020).
//
// "In every mode" is pinned by what this package does not have rather than by a
// loop over modes: internal/policy's modes are the only budget states a run can
// be in, and this package reads neither them nor anything else from the budget
// (below). The refusal is therefore the table's own, identical whatever the
// mode, and the model is charged only once somebody writes a price — which is
// what "no price" means everywhere else in this build, an unpriced Claude model
// included.
func TestAnUnpricedGeminiModelIsRefused(t *testing.T) {
	tbl := Embedded()
	for canonical, want := range expectedGemini {
		m, ok := tbl.Lookup(canonical)
		if !ok {
			t.Errorf("the price table carries no row for %s, which vertex_gemini.go lists and GV3 prices", canonical)
			continue
		}
		if m.Rates.InputPerM != want.Input || m.Rates.OutputPerM != want.Output {
			t.Errorf("%s: charged $%v / $%v a million, want $%v / $%v", canonical, m.Rates.InputPerM, m.Rates.OutputPerM, want.Input, want.Output)
		}
		// The long-context price, where the page states one: a row with the flat
		// price alone charges a call past the threshold about half of what Vertex
		// bills (WorstCase, and the ledger on what the call reports).
		got := m.Rates.LongContext
		switch {
		case want.Long == nil && got != nil:
			t.Errorf("%s: priced $%v / $%v above %d input tokens, and the page states one price for it", canonical, got.InputPerM, got.OutputPerM, got.AboveInputTokens)
		case want.Long != nil && got == nil:
			t.Errorf("%s: flat $%v / $%v, want the page's second price above %d input tokens ($%v / $%v)", canonical, m.Rates.InputPerM, m.Rates.OutputPerM, want.Long.AboveInputTokens, want.Long.InputPerM, want.Long.OutputPerM)
		case want.Long != nil && *got != *want.Long:
			t.Errorf("%s: above %d input tokens charged $%v / $%v, want $%v / $%v", canonical, got.AboveInputTokens, got.InputPerM, got.OutputPerM, want.Long.InputPerM, want.Long.OutputPerM)
		}
		// The provenance is Vertex's, and the date is the one the prices were
		// read: the table's own Source and CheckedAt are the Claude page's, so a
		// row that answered to those would say its price came from a page that
		// never quoted it.
		if m.PriceSource != vertexGeminiSource || m.PriceCheckedAt != vertexGeminiCheckedAt {
			t.Errorf("%s: provenance (%q, %q), want (%q, %q)", canonical, m.PriceSource, m.PriceCheckedAt, vertexGeminiSource, vertexGeminiCheckedAt)
		}
		if m.PriceSource == source || m.PriceCheckedAt == checkedAt {
			t.Errorf("%s: the row answers to the Claude table's own source and date (%q, %q): Vertex is a second publisher and its prices say so", canonical, source, checkedAt)
		}
		if _, err := time.Parse("2006-01-02", m.PriceCheckedAt); err != nil {
			t.Errorf("%s: checked at %q, which is not a date: %v", canonical, m.PriceCheckedAt, err)
		}
	}
	// The date is on the row and not only in the file: a surface that prints a
	// price without it (budget prices, models ls) would show Vertex's number
	// with the Claude page's date beside it.
	if vertexGeminiCheckedAt == "" || vertexGeminiCheckedAt == checkedAt {
		t.Errorf("vertexGeminiCheckedAt is %q: the day the Vertex prices were read is its own, and empty is not a date", vertexGeminiCheckedAt)
	}

	// The unpriced half: a Gemini model the catalog does not carry is in the
	// same state as an unpriced Claude model — no row, and no refusal recorded
	// by the built-in catalog either, so the fix every surface offers it is the
	// owner's own price under model_prices.
	for _, id := range append([]string{"claude-opus-4-8"}, geminiNotCarried[0:2]...) {
		if m, ok := tbl.Lookup(id); ok {
			t.Errorf("%s looked up in the embedded table as %+v: nothing prices it, which is what refuses a pin naming it", id, m)
		}
		if why := tbl.Refusal(id); why != "" {
			t.Errorf("%s is refused by the catalog (%q): the unpriced state is the absence of a row, and a build that recorded a refusal for a model it merely does not price would name the wrong fix", id, why)
		}
	}
	// An unpriced model is never free: the table's own maximum is what a caller
	// with no row charges, which is the direction that cannot undercount a call
	// the gateway sees anyway.
	if max := tbl.Max(); max.InputPerM <= 0 || max.OutputPerM <= 0 {
		t.Errorf("Max() = %+v: a model with no row would be charged nothing", max)
	}
	// The way out is the owner's own number, for a Google model exactly as for
	// any other: With adds the row, and the price is theirs.
	paid, err := tbl.With(Overrides{"google/gemini-4": {Rates: Rates{InputPerM: 1, OutputPerM: 3}}})
	if err != nil {
		t.Fatalf("pricing an unpriced Gemini model under model_prices: %v", err)
	}
	m, ok := paid.Lookup("google/gemini-4")
	if !ok || m.Rates.InputPerM != 1 || m.Rates.OutputPerM != 3 {
		t.Errorf("after the owner priced it, google/gemini-4 = (%+v, %v), want their own $1 / $3", m.Rates, ok)
	}

	// The Vertex spelling is not a price key: nothing may name it (IsAlias), so
	// a row reachable by it would be a price for a call that cannot exist.
	if m, ok := tbl.Lookup("gemini-3-pro"); ok {
		t.Errorf("the bare Vertex spelling gemini-3-pro priced a call as %+v: the table is keyed by the ID a pin may name", m)
	}

	// Every row that carries a provenance carries both halves of it: a row
	// saying where its price came from but not the day it was read is the date
	// this task is required to state, missing.
	for id, m := range tbl.Models {
		if (m.PriceSource == "") != (m.PriceCheckedAt == "") {
			t.Errorf("%s: source %q, checked %q — a row's provenance is both or neither", id, m.PriceSource, m.PriceCheckedAt)
		}
		if m.PriceSource == "" && (id == "google/gemini-3-pro" || id == "google/gemini-3-flash") {
			t.Errorf("%s has no provenance of its own: the table's Source and CheckedAt are the Claude page's", id)
		}
	}

	noBudgetModeIsReadHere(t)
}

// noBudgetModeIsReadHere pins the "in every budget mode" half of the test
// above: the price table cannot refuse a model in one mode and charge it in
// another, because it reads no mode at all. The two ways one could reach this
// package are the vocabulary's own package, which a file here would have to
// import, and the word spelled out; both are checked over this package's
// non-test files.
//
// It is the source-side guard this tree already uses for a rule that must have
// one site (internal/config's TestRoutingHasOneResolver): the property is about
// the code that exists, so the test reads the code rather than a value it
// exports.
func noBudgetModeIsReadHere(t *testing.T) {
	t.Helper()
	// The two live modes, as internal/policy's vocabulary spells them, written
	// out rather than imported: a test's imports are reads the affected-tests
	// map records, and a price-table test that pulled the policy package in
	// would have this package's tests run whenever anything under it changes.
	// The import half below is the half with teeth — reading a mode means
	// importing the package that defines one, whatever the word becomes.
	modes := map[string]bool{"observe": true, "enforce": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's own files: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		scanned++
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if p == "github.com/dimipaun/fugaro/internal/policy" || p == "github.com/dimipaun/fugaro/internal/budget" {
				t.Errorf("%s imports %s: the price table must not read a budget mode, or one model could be refused in observe and charged in enforce, and an unpriced model is refused in every mode (D3020)", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && modes[v] {
				t.Errorf("%s:%d spells the budget mode %q: the price table must not read a budget mode (D3020)", name, fset.Position(lit.Pos()).Line, v)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no file of this package was scanned: the guard would pass vacuously")
	}
}
