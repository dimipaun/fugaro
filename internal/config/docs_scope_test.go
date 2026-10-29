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

// rigorOrderRanks are the mode's layers, narrowest first, each with the
// phrase docs/project-layer.md §2 must name it by (between that section's
// rigor-order markers). The guard below reads them in this order, so a doc
// that reorders the sentence fails, and it asks rigor.Resolve about every
// adjacency, so a resolver that stops honouring the order fails too.
var rigorOrderRanks = []struct {
	phrase string
	from   rigor.Layer
}{
	{"an `rigor.areas` rule the run's paths match", rigor.FromArea},
	{"the repository's `rigor.default`", rigor.FromRepo},
	{"the project layer's `defaults.rigor.default`", rigor.FromProject},
	{"Fugaro's own default", rigor.FromDefault},
}

// TestProjectLayerDocStatesTheRigorOrder is F37's other half: the scope
// table below is pinned row by row, and §2's own statement of the mode's
// precedence — the one place the order is written for a reader (D6, D20) —
// was not. The doc states the order, so this test reads the order out of the
// doc and then holds the resolver to it, rather than restating either.
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

	// Each phrase, held against the resolver: with that layer and every
	// wider layer set and disagreeing, the narrower one is the answer.
	for _, r := range rigorOrderRanks {
		cfg, req := rigorOrderSetup(r.from)
		want := rigor.Critical
		if r.from == rigor.FromDefault {
			// Nothing sets a mode at all, so the built-in default is the
			// answer — which is what the last phrase claims.
			want = rigor.Default
		}
		got := rigor.Resolve(cfg, req)
		if got.From != r.from || got.Mode != want {
			t.Errorf("§2 says %q wins over the wider layers, but rigor.Resolve answers %q from %q", r.phrase, got.Mode, got.From)
		}
	}

	// The two claims the same sentence makes about a run's own input: a
	// request may raise and never lower, and a mode a run reached never
	// falls.
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Critical}, rigor.Request{Mode: rigor.POC}); got.Mode != rigor.Critical {
		t.Errorf("§2 says a run's own request may raise and never lower it, but a request for %q answered %q", rigor.POC, got.Mode)
	}
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Pro}, rigor.Request{Reached: rigor.Critical}); got.Mode != rigor.Critical {
		t.Errorf("§2 says a mode a run has reached never falls, but a reached %q answered %q", rigor.Critical, got.Mode)
	}
}

// rigorOrderSetup is a configuration in which the named layer and every
// wider layer set the mode, each wider layer the looser one, so the answer
// must name the narrowest of them — the layer the doc's phrase is about.
func rigorOrderSetup(narrowest rigor.Layer) (rigor.Config, rigor.Request) {
	var cfg rigor.Config
	var req rigor.Request
	switch narrowest {
	case rigor.FromArea:
		cfg.Project, cfg.Repo = rigor.POC, rigor.Startup
		cfg.RepoAreas = []rigor.Area{{Name: "area", Match: "area/**", Mode: rigor.Critical}}
		req.Paths = []string{"area/x"}
	case rigor.FromRepo:
		cfg.Project, cfg.Repo = rigor.POC, rigor.Critical
	case rigor.FromProject:
		cfg.Project = rigor.Critical
	}
	return cfg, req
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
