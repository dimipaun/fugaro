package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// TestScopeTableMatchesDocs keeps docs/project-layer.md's scope table equal
// to Scopes: every row "| `key` | layers |", in order, and nothing else
// between the table's markers.
func TestScopeTableMatchesDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-layer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, "<!-- scope-table:start -->"), strings.Index(doc, "<!-- scope-table:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/project-layer.md has no scope-table markers")
	}
	rowRE := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([a-z, ]+) \\|")
	var got []string
	for _, m := range rowRE.FindAllStringSubmatch(doc[start:end], -1) {
		got = append(got, m[1]+" = "+m[2])
	}
	var want []string
	for _, r := range Scopes {
		want = append(want, r.Key+" = "+r.In.String())
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("docs/project-layer.md's scope table differs from config.Scopes:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// rigorOrderRanks are the mode's ranks, weakest first, each with the phrase
// docs/project-layer.md §2 must name it by (between that section's
// rigor-order markers), the layer a resolution from it names, and the way a
// test sets that rank's level. The guard below reads them in this order, so a
// doc that reorders the sentence fails, and it asks rigor.Resolve for every
// rank, so a resolver that stops honouring the order fails too.
//
// Two ranks are areas and two are the project layer, which is why an entry
// carries the layer a resolution names rather than being keyed by it: the
// order is the doc's sentence, and the layer is what a run's record says about
// the rank that won.
var rigorOrderRanks = []struct {
	phrase string
	from   rigor.Layer
	set    func(*rigor.Config, *rigor.Request, rigor.Rigor)
}{
	{"Fugaro's own default", rigor.FromDefault,
		func(*rigor.Config, *rigor.Request, rigor.Rigor) {}},
	{"the project layer's `defaults.rigor.default`", rigor.FromProject,
		func(c *rigor.Config, _ *rigor.Request, l rigor.Rigor) { c.Project = l }},
	{"a `defaults.rigor.areas` rule the run's repository and paths match", rigor.FromArea,
		func(c *rigor.Config, r *rigor.Request, l rigor.Rigor) {
			c.ProjectAreas = []rigor.Area{{Name: "payments", Match: "payments/**", Level: l}}
			r.Paths = append(r.Paths, "payments/x.go")
		}},
	{"the project layer's `defaults.rigor.repos` entry for the run's repository", rigor.FromProject,
		func(c *rigor.Config, r *rigor.Request, l rigor.Rigor) {
			c.ProjectRepos = map[string]rigor.Rigor{"acme/app": l}
			r.Repo = "acme/app"
		}},
	{"the repository's own `rigor.default`", rigor.FromRepo,
		func(c *rigor.Config, _ *rigor.Request, l rigor.Rigor) { c.Repo = l }},
	{"a rule of the repository's own `rigor.areas`", rigor.FromArea,
		func(c *rigor.Config, r *rigor.Request, l rigor.Rigor) {
			c.RepoAreas = []rigor.Area{{Name: "own", Match: "own/**", Level: l}}
			r.Paths = append(r.Paths, "own/x.go")
		}},
	{"the run's own request", rigor.FromRequest,
		func(_ *rigor.Config, r *rigor.Request, l rigor.Rigor) { r.Level = l }},
}

// TestProjectLayerDocStatesTheRigorOrder is F37's other half: the scope
// table below is pinned row by row, and §2's own statement of the mode's
// precedence — the one place the order is written for a reader (D6, D20,
// LD3) — was not. The doc states the order, so this test reads the order out
// of the doc and then holds the resolver to it, rather than restating either.
//
// Mutation (swap two phrases in docs/project-layer.md §2, or make
// rigor.Resolve read the repository's value before the project's): this test
// fails on the phrases or on the resolver's answer, so the sentence cannot
// drift from the code it describes.
func TestProjectLayerDocStatesTheRigorOrder(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-layer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, "<!-- rigor-order:start -->"), strings.Index(doc, "<!-- rigor-order:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/project-layer.md §2 has no rigor-order markers")
	}
	body := doc[start:end]
	at := -1
	for _, r := range rigorOrderRanks {
		j := strings.Index(body, r.phrase)
		if j < 0 {
			t.Errorf("docs/project-layer.md §2 never names %q", r.phrase)
			continue
		}
		if j <= at {
			t.Errorf("docs/project-layer.md §2 names %q out of order: the narrowest layer that sets the mode must be named first", r.phrase)
		}
		at = j
	}
	// The built-in default the sentence names is the vocabulary's own, so a
	// change to D19's answer cannot leave the doc naming the old one.
	if !strings.Contains(body, "`"+string(rigor.Default)+"`") {
		t.Errorf("docs/project-layer.md §2 does not name the built-in default %q (internal/rigor's Default)", rigor.Default)
	}

	// Each rank, held against the resolver: with that rank set to Critical
	// and every weaker rank set to POC, the narrower one is the answer, and
	// it is the layer the phrase's rank names.
	for i, r := range rigorOrderRanks {
		var cfg rigor.Config
		var req rigor.Request
		for _, weaker := range rigorOrderRanks[:i] {
			weaker.set(&cfg, &req, rigor.POC)
		}
		r.set(&cfg, &req, rigor.Critical)
		want := rigor.Critical
		if i == 0 {
			// Nothing sets a mode at all, so the built-in default is the
			// answer — which is what the first phrase claims.
			want = rigor.Default
		}
		got := rigor.Resolve(cfg, req)
		if got.From != r.from || got.Level != want {
			t.Errorf("§2 says %q wins over every weaker rank, but rigor.Resolve answers %q from %q", r.phrase, got.Level, got.From)
		}
	}

	// The two claims the same sentence makes about a run's own input: a
	// request settles the level whether it raises or lowers what the layers
	// set (only a floor, outside every layer, holds it), and a level a run has
	// reached never falls.
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Critical}, rigor.Request{Level: rigor.POC}); got.Level != rigor.POC {
		t.Errorf("§2 says a run's own request settles the level a layer set, but a request for %q answered %q", rigor.POC, got.Level)
	}
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Pro}, rigor.Request{Reached: rigor.Critical}); got.Level != rigor.Critical {
		t.Errorf("§2 says a level a run has reached never falls, but a reached %q answered %q", rigor.Critical, got.Level)
	}
}

// TestExecutableKeysMatchDocs keeps docs/project-layer.md's executable-keys
// list (section 8, "--executable-changes") equal to config.ExecutableKeys,
// in order: a key added, removed or renamed in code without updating the
// doc's list fails this test instead of leaving the doc wrong about what
// --executable-changes actually gates.
func TestExecutableKeysMatchDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-layer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, "<!-- executable-keys:start -->"), strings.Index(doc, "<!-- executable-keys:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/project-layer.md has no executable-keys markers")
	}
	itemRE := regexp.MustCompile("(?m)^- `([^`]+)`$")
	var got []string
	for _, m := range itemRE.FindAllStringSubmatch(doc[start:end], -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, "\n") != strings.Join(ExecutableKeys, "\n") {
		t.Fatalf("docs/project-layer.md's executable-keys list differs from config.ExecutableKeys:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(ExecutableKeys, "\n"))
	}
}
