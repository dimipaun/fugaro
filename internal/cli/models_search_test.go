package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// TestModelsSearchFindsACatalogRow: a substring of a catalog model ID finds
// the row, with what a run would charge for it — the tiers the catalog carries,
// each tier's price range and endpoint count, and the model's context and
// output limits (spec §3).
func TestModelsSearchFindsACatalogRow(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"deepseek/deepseek-v4-flash", "catalog", "zdr", "all", "endpoints"} {
		if !strings.Contains(out, want) {
			t.Errorf("the hit does not show %q:\n%s", want, out)
		}
	}
	// The context and output limits the catalog carries for the row, in the
	// units a reader compares against a model's page.
	if !strings.Contains(out, "context") {
		t.Errorf("the hit does not show the context and output limits:\n%s", out)
	}
	// The search is a substring match on the ID, case-insensitively.
	upper, _, err := execute(t, "models", "search", "DEEPSEEK-V4-FLASH")
	if err != nil {
		t.Fatalf("%v\n%s", err, upper)
	}
	if !strings.Contains(upper, "deepseek/deepseek-v4-flash") {
		t.Errorf("the search is not case-insensitive:\n%s", upper)
	}
}

// TestModelsSearchFindsAnEmbeddedClaudeRow: the embedded Claude table is
// searched too, and its rows are marked embedded so a reader can tell a price
// this binary carries from one the catalog carries.
func TestModelsSearchFindsAnEmbeddedClaudeRow(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "claude-haiku")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "claude-haiku-4-5") {
		t.Errorf("the embedded Claude table is not searched:\n%s", out)
	}
	if !strings.Contains(out, "embedded") {
		t.Errorf("the hit does not say its price is the embedded table's:\n%s", out)
	}
}

// TestModelsSearchFindsAModelTheOwnerPriced: the owner's own model_prices is a
// source of hits like the catalog and the embedded table, and it has to be: it
// is what a miss tells a reader to write, so a model already priced that way is
// found rather than reported missing. The row carries the owner's own numbers
// and, when a route of this project serves the model, that route (D788, D782).
func TestModelsSearchFindsAModelTheOwnerPriced(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "vendor/model-y")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	row := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "vendor/model-y") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("a model the owner priced is not found:\n%s", out)
	}
	// The fixture prices it 1 in / 2 out, and no provider route of the project
	// claims it.
	for _, want := range []string{"local", "$1", "$2", "no route"} {
		if !strings.Contains(row, want) {
			t.Errorf("the hit does not show %q: %q", want, row)
		}
	}
}

// TestModelsSearchMissSaysHowToPrice: a model the catalog does not carry is
// not found, and the answer names the one way to use it anyway (D788, D782).
func TestModelsSearchMissSaysHowToPrice(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "vendor/nothing-here")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "no model matches") {
		t.Errorf("a miss does not say so:\n%s", out)
	}
	if !strings.Contains(out, "fugaro models add vendor/nothing-here --price-in X --price-out Y") {
		t.Errorf("a miss does not name the one way to use the model anyway:\n%s", out)
	}
}

// TestModelsSearchNamesTheClaimingRoute: a hit a route of the selected project
// already claims says which one, so a reader knows the model is usable before
// running anything.
func TestModelsSearchNamesTheClaimingRoute(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "openrouter") {
		t.Errorf("the hit does not name the route that claims it:\n%s", out)
	}
	// A model no route claims says so rather than staying silent: a row with no
	// route is refused at launch.
	other, _, err := execute(t, "models", "search", "kimi")
	if err != nil {
		t.Fatalf("%v\n%s", err, other)
	}
	if !strings.Contains(other, "no route") {
		t.Errorf("a hit no route claims does not say so:\n%s", other)
	}
}

// TestModelsSearchRefusesAPriceTableItCannotBuild: the search reads the owner's
// model_prices to know which rows they replace, so a table that will not build
// is the search's own error rather than a search that quietly answers without
// them — a catalog row shown where the owner set a price is a wrong price, not
// a missing one, and a model they priced would vanish from the hits.
// `models ls` and `budget prices` surface the same error, and a config that
// reaches the CLI clean cannot reach this at all (localcfg validates the
// overrides as it loads them), which is exactly why the branch is pinned here
// by the function's own contract.
func TestModelsSearchRefusesAPriceTableItCannotBuild(t *testing.T) {
	one := 1.0
	// An alias as a model_prices key: localcfg.Config.Overrides refuses it, and
	// the search must not answer from the catalog rows it could still read.
	lc := &localcfg.Config{ModelPrices: map[string]localcfg.ModelPrice{
		"sonnet": {InputPerM: &one, OutputPerM: &one},
	}}

	rows, err := modelsSearchRows(lc, "deepseek")
	if err == nil {
		t.Fatalf("the search answered from a price table it could not build: %d rows", len(rows))
	}
	if !strings.Contains(err.Error(), "alias") {
		t.Errorf("the search's error is %v, and it does not say what the table build refused", err)
	}
}

// TestModelsSearchSanitisesTheTerm: the search echoes the term it was given
// twice — quoted in the miss, and bare in the sentence naming the one way to
// use a model the catalog does not carry — and a term is a string the caller
// typed. A bidi override would let it read backwards and an ESC[31m would
// colour the rest of the terminal, so both echoes go through oneLine.
//
// Mutation (run, restored): print the term raw at models_search.go:72 and :79
// and this fails on the raw characters.
func TestModelsSearchSanitisesTheTerm(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "vend‮or\x1b[31m")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, raw := range []string{"‮", "\x1b"} {
		if strings.Contains(out, raw) {
			t.Errorf("the search echoes a raw %q from the term:\n%q", raw, out)
		}
	}
	if !strings.Contains(out, "vend?or") {
		t.Errorf("the term is not echoed at all:\n%s", out)
	}
}

// TestModelsSearchEscapesItsCells: a route name is a cell of the hit table,
// and a tab in it would open a column of its own and shift every column after
// it. The value is a repo's pin, so it is escaped rather than trusted
// (cellOneLine; TestModelsLsEscapesTableCells covers the same for `models ls`,
// whose writer the config also keeps tabs out of). The bounds are the catalog
// file's own strings, escaped the same way.
func TestModelsSearchEscapesItsCells(t *testing.T) {
	if got := routeCell("vendor/tab\tbed"); got != "vendor/tab?bed" {
		t.Errorf("a tab in a route name is not escaped: %q", got)
	}
	if got := routeCell(""); got != "no route" {
		t.Errorf("a model no route serves reads %q", got)
	}
	bounds := map[string]catalog.Bound{"input_per_m": {Min: "1\n2", Max: "3\t4"}}
	if got := rangeCell(bounds, "input_per_m"); got != "$1?2 – $3?4" {
		t.Errorf("a bound's own newline and tab are not escaped: %q", got)
	}
	if got := rangeCell(map[string]catalog.Bound{"input_per_m": {Min: "1", Max: "1"}}, "input_per_m"); got != "$1" {
		t.Errorf("one bound reads %q", got)
	}
	if got := rangeCell(bounds, "output_per_m"); got != "—" {
		t.Errorf("a dimension the tier does not price reads %q", got)
	}
}

// TestModelsSearchEscapesAHitRowsIDCell: the MODEL cell of a hit is a model ID,
// and an ID is a string a catalog file wrote. A local config refuses one
// carrying a control or a bidi character as it loads (model_prices: an alias),
// so the row here is the catalog's: a regenerated file whose key carries U+202E,
// an ESC[31m and a tab, under the shipped deepseek row's prices. Each of the
// three is escaped rather than printed — the override would let the ID read
// backwards, the sequence would colour the rest of the table, and the tab would
// open a column of its own and shift SOURCE and every column after it
// (cellOneLine, the row's first cell).
//
// Mutation (run, restored): print the ID raw (drop cellOneLine at
// models_search.go:79) and this fails on the raw characters and on the columns.
func TestModelsSearchEscapesAHitRowsIDCell(t *testing.T) {
	modelsFixture(t)
	cat := shippedCatalog(t)
	const id = "vendor/mo‮del\x1b[31m\tbed"
	cat.Models[id] = cat.Models["deepseek/deepseek-v4-flash"]
	old := loadCatalog
	loadCatalog = func() (*catalog.File, error) { return cat, nil }
	t.Cleanup(func() { loadCatalog = old })

	out, _, err := execute(t, "models", "search", "vendor/mo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, raw := range []string{"‮", "\x1b"} {
		if strings.Contains(out, raw) {
			t.Errorf("the hit table carries a raw %q from the model ID:\n%q", raw, out)
		}
	}
	// What the cell shows instead: the bidi override and the tab are marked,
	// and the whole ANSI sequence is dropped (safetext skips it).
	if !strings.Contains(out, "vendor/mo?del?bed") {
		t.Errorf("the escaped ID is not in the read at all:\n%s", out)
	}
	// The columns, against the header's own: the ID's two rows are the
	// catalog's two tiers, and their SOURCE cell has to sit where the header
	// puts SOURCE. A raw tab in the ID is a cell separator, so the ID splits,
	// "catalog" lands in the TIER column, and the index moves.
	lines := strings.Split(out, "\n")
	header := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "MODEL ") {
			header = l
		}
	}
	if header == "" {
		t.Fatalf("the read has no hit table:\n%s", out)
	}
	want := strings.Index(header, "SOURCE")
	rows := 0
	for _, l := range lines {
		// The ID's own rows, whichever way the ID was written: they are the
		// two the catalog priced, and the local row below them is not one.
		if !strings.HasPrefix(l, "vendor/mo") || !strings.Contains(l, "catalog") {
			continue
		}
		rows++
		if got := strings.Index(l, "catalog"); got != want {
			t.Errorf("a tab in a model ID shifted the row's columns: SOURCE at %d and the header's at %d\n%s", got, want, out)
		}
	}
	if rows != 2 {
		t.Errorf("the ID's rows are %d, and the catalog prices two tiers:\n%s", rows, out)
	}
}

// TestModelsSearchIsOffline: the search reads this binary's own tables and the
// selected project config, and reaches nothing (D788). An empty PATH is not
// enough to say so — it stops git and gcloud, and not a Go SDK client — so the
// fakes the project config points at are installed and counted: a search that
// went to Cloud Run, Cloud Logging or the budget database is red here.
func TestModelsSearchIsOffline(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.appendConfig(t, modelsOfflineExtra)
	t.Setenv("PATH", t.TempDir())

	out, _, err := execute(t, "models", "search", "deepseek")
	if err != nil {
		t.Fatalf("the search is not offline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deepseek/deepseek-v4-flash") {
		t.Errorf("the offline search does not find a catalog row:\n%s", out)
	}
	if n := len(f.run.Requests()) + len(f.logging.Requests()) + len(f.db.Requests()); n != 0 {
		t.Errorf("models search made %d cloud call(s), want none: the search is this binary's own tables and the project config", n)
	}
	if _, _, err := execute(t, "models", "search", "deepseek", "--cloud", "gcp"); err == nil {
		t.Error("models search takes --cloud: the search has no cloud path to select")
	}
}

// modelsSearchTextRows is the read's hit table as the three cells a row is
// identified by — ID, SOURCE, TIER — in the order they are printed. It is what
// the document is held against: the two carry the same hits. A data row is one
// whose second cell is a source; the header, the "models matching" line and a
// miss's two sentences are not rows.
func modelsSearchTextRows(out string) [][3]string {
	rows := [][3]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		switch f[1] {
		case modelsSourceCatalog, modelsSourceEmbedded, modelsSourceLocal:
			rows = append(rows, [3]string{f[0], f[1], f[2]})
		}
	}
	return rows
}

// TestModelsSearchJSONMatchesSchema validates the document the command writes
// against schemas/models-search.schema.json, the contract a consumer reads it
// by (D784), and holds it to the read: one object per hit of the table, in the
// same order, carrying the row as data rather than as a cell.
func TestModelsSearchJSONMatchesSchema(t *testing.T) {
	modelsFixture(t)

	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "models-search.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/models-search.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	var hits struct {
		V      int    `json:"v"`
		Query  string `json:"query"`
		Models []struct {
			ID           string       `json:"id"`
			Source       string       `json:"source"`
			Tier         string       `json:"tier"`
			Route        string       `json:"route"`
			InputPerM    *searchRange `json:"input_per_m"`
			OutputPerM   *searchRange `json:"output_per_m"`
			Endpoints    int          `json:"endpoints"`
			ZDREndpoints int          `json:"zdr_endpoints"`
			Context      int64        `json:"context_tokens"`
			MaxOutput    int64        `json:"max_output_tokens"`
		} `json:"models"`
	}

	sources := map[string]bool{}
	// One term per source the fixture holds: the catalog's DeepSeek row, an
	// embedded Claude row, and a model only the owner's own model_prices
	// prices.
	for _, q := range []string{"deepseek", "claude-haiku", "vendor/model-y"} {
		out, _, err := execute(t, "models", "search", q, "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if err := sch.Validate(inst); err != nil {
			t.Fatalf("the document does not match schemas/models-search.schema.json: %v\n%s", err, out)
		}
		hits.Models = nil
		if err := json.Unmarshal([]byte(out), &hits); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if hits.V != 1 {
			t.Errorf("v = %d, and the document's first version is 1 (D784)", hits.V)
		}
		if hits.Query != q {
			t.Errorf("query = %q, and the search was given %q", hits.Query, q)
		}
		// The rows are the read's own, in the same order, so a consumer of the
		// document sees exactly what the table shows. The table's TIER cell is
		// a dash on a row no tier prices, which is the document's absent tier.
		text, _, err := execute(t, "models", "search", q)
		if err != nil {
			t.Fatalf("%v\n%s", err, text)
		}
		printed := modelsSearchTextRows(text)
		if len(printed) != len(hits.Models) {
			t.Fatalf("%q: the document has %d hits, and the read prints %d:\n%s", q, len(hits.Models), len(printed), text)
		}
		for i, m := range hits.Models {
			sources[m.Source] = true
			tier := m.Tier
			if tier == "" {
				tier = dash("")
			}
			if want := [3]string{m.ID, m.Source, tier}; printed[i] != want {
				t.Errorf("%q: hit %d is %v, and the read prints %v", q, i, want, printed[i])
			}
		}
	}
	for _, want := range []string{"catalog", "embedded", "local"} {
		if !sources[want] {
			t.Errorf("the document carries no %q row (it has %v)", want, sources)
		}
	}

	// A catalog hit carries the tier's own range as numbers and the endpoints
	// behind it, rather than the cell the read formats, and the route the
	// selected project claims the model on.
	out, _, err := execute(t, "models", "search", "deepseek", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	hits.Models = nil
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	tiers := map[string]bool{}
	for _, m := range hits.Models {
		if m.Source != modelsSourceCatalog {
			continue
		}
		tiers[m.Tier] = true
		if m.InputPerM == nil || m.OutputPerM == nil {
			t.Errorf("%s/%s: the hit carries no price range (%v, %v)", m.ID, m.Tier, m.InputPerM, m.OutputPerM)
			continue
		}
		if m.InputPerM.Min > m.InputPerM.Max || m.OutputPerM.Min > m.OutputPerM.Max {
			t.Errorf("%s/%s: the range runs backwards (%v, %v)", m.ID, m.Tier, m.InputPerM, m.OutputPerM)
		}
		if m.Endpoints < 1 || m.ZDREndpoints > m.Endpoints {
			t.Errorf("%s/%s: endpoints = %d and zdr_endpoints = %d", m.ID, m.Tier, m.Endpoints, m.ZDREndpoints)
		}
		if m.Context < 1 || m.MaxOutput < 1 {
			t.Errorf("%s/%s: context = %d and max_output_tokens = %d, and the catalog carries both", m.ID, m.Tier, m.Context, m.MaxOutput)
		}
		if m.Route != "openrouter" {
			t.Errorf("%s/%s: route = %q, and the fixture's one route claims the row", m.ID, m.Tier, m.Route)
		}
	}
	for _, tier := range []string{"zdr", "all"} {
		if !tiers[tier] {
			t.Errorf("the catalog hit carries no %q tier (it has %v)", tier, tiers)
		}
	}

	// A miss is an empty array, not a missing member: a consumer iterates it.
	miss, _, err := execute(t, "models", "search", "vendor/nothing-here", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, miss)
	}
	if !strings.Contains(miss, `"models": []`) {
		t.Errorf("a miss carries no empty models array:\n%s", miss)
	}
}

// searchRange is one dimension's price range as the document carries it.
type searchRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// TestModelsSearchJSONKeepsItsValuesAsData: the document is data, so an ID and
// the term reach a consumer as the bytes the table and the caller hold —
// encoding/json escapes a control character rather than printing it raw, and
// hands back the same string — where the read marks them for the terminal
// (cellOneLine, oneLine). An ID is never a control character in either form.
func TestModelsSearchJSONKeepsItsValuesAsData(t *testing.T) {
	modelsFixture(t)
	cat := shippedCatalog(t)
	const id = "vendor/mo\u202edel\x1b[31m\tbed"
	cat.Models[id] = cat.Models["deepseek/deepseek-v4-flash"]
	old := loadCatalog
	loadCatalog = func() (*catalog.File, error) { return cat, nil }
	t.Cleanup(func() { loadCatalog = old })

	// The term is a substring of the ID the table holds and carries a control
	// character of its own, so one run covers both values.
	const term = "mo‮del\x1b[31m"
	out, _, err := execute(t, "models", "search", term, "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("the document carries a raw ESC, so it is not the data the table holds:\n%q", out)
	}
	var doc struct {
		Query  string `json:"query"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Query != term {
		t.Errorf("query = %q, and the search was given %q", doc.Query, term)
	}
	for _, m := range doc.Models {
		if m.ID == id {
			return
		}
	}
	t.Errorf("the document does not carry the ID the table holds (%q):\n%s", id, out)
}

// TestModelsSearchJSONCarriesTheEndpointCounts: a catalog row's two counts are
// the row's own, and a model whose endpoints are none of them ZDR-eligible has
// a real 0 there. A consumer that read the member's absence as "not a catalog
// row" would draw a wrong figure with no error, so the absence carries no
// meaning: a row the catalog does not price is the one with neither member.
// The shipped catalog holds both — qwen/qwen3.8-flash is served by endpoints
// no one of which is ZDR-eligible, and the embedded table's rows are the
// catalog's to price not at all.
func TestModelsSearchJSONCarriesTheEndpointCounts(t *testing.T) {
	modelsFixture(t)

	var doc struct {
		Models []struct {
			ID           string `json:"id"`
			Source       string `json:"source"`
			Tier         string `json:"tier"`
			Endpoints    *int   `json:"endpoints"`
			ZDREndpoints *int   `json:"zdr_endpoints"`
		} `json:"models"`
	}

	out, _, err := execute(t, "models", "search", "qwen/qwen3.8-flash", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rows := 0
	for _, m := range doc.Models {
		if m.ID != "qwen/qwen3.8-flash" {
			continue
		}
		rows++
		if m.Source != modelsSourceCatalog || m.Tier != "all" {
			t.Errorf("%s: the hit is %s/%s, and the catalog prices it on the all tier alone", m.ID, m.Source, m.Tier)
		}
		if m.Endpoints == nil || *m.Endpoints < 1 {
			t.Errorf("%s: endpoints = %v, and the catalog counts the endpoints that serve it", m.ID, m.Endpoints)
		}
		if m.ZDREndpoints == nil {
			t.Errorf("%s: the hit carries no zdr_endpoints member, so its 0 ZDR endpoints read as a row the catalog does not price", m.ID)
			continue
		}
		if *m.ZDREndpoints != 0 {
			t.Errorf("%s: zdr_endpoints = %d, and no endpoint of the row is ZDR-eligible", m.ID, *m.ZDREndpoints)
		}
	}
	if rows != 1 {
		t.Fatalf("the shipped catalog prices qwen/qwen3.8-flash on %d hits, and it carries one all-tier row:\n%s", rows, out)
	}

	// A row the catalog does not price carries neither member.
	priced := 0
	for _, q := range []string{"claude-haiku-4-5", "vendor/model-y"} {
		out, _, err := execute(t, "models", "search", q, "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		doc.Models = nil
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, m := range doc.Models {
			if m.Source == modelsSourceCatalog {
				continue
			}
			priced++
			if m.Endpoints != nil || m.ZDREndpoints != nil {
				t.Errorf("%s: a %s row carries endpoints %v and zdr_endpoints %v, and the catalog does not price it", m.ID, m.Source, m.Endpoints, m.ZDREndpoints)
			}
		}
	}
	if priced == 0 {
		t.Fatalf("the search found no row the catalog does not price, so nothing above was checked")
	}
}
