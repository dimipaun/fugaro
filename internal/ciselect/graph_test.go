package ciselect

import (
	"bytes"
	"os"
	"slices"
	"testing"
)

// The fixture is `go list -deps -test -json -tags
// terraform,firebase,docker,live ./...` over a throwaway module built to hold
// one of each shape this repository has: a chain of imports (core <- mid <-
// top), a package that reaches another from its own test file (probe <- tiny)
// and one that does from an external test file (ext <- tinyext), a package that
// embeds a data file and one whose test embeds a template, a package whose
// importers sit behind the four tags one file apiece and a file behind a tag
// none of them sets, and a testdata fixture no package owns. The capture leaves
// out the standard library's own entries, about 130 of them and none of which a
// repository file can change, and rewrites its module root to /mod; nothing
// else about it is edited.
const graphFixture = "testdata/graph.json"

func parseGraphFixture(t *testing.T) *Graph {
	t.Helper()
	raw, err := os.ReadFile(graphFixture)
	if err != nil {
		t.Fatalf("read %s: %v", graphFixture, err)
	}
	g, err := ParseGraph(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", graphFixture, err)
	}
	return g
}

func checkAffected(t *testing.T, g *Graph, changed []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := g.Affected(changed); !slices.Equal(got, want) {
		t.Errorf("Affected(%q) = %q, want %q", changed, got, want)
	}
}

func TestAffectedFollowsImportsTransitively(t *testing.T) {
	g := parseGraphFixture(t)
	checkAffected(t, g, []string{"internal/core/core.go"},
		"example.com/m/internal/core", "example.com/m/internal/mid", "example.com/m/internal/top")
	checkAffected(t, g, []string{"internal/mid/mid.go"},
		"example.com/m/internal/mid", "example.com/m/internal/top")
	// Nothing imports top, so the changed package is the whole answer.
	checkAffected(t, g, []string{"internal/top/top.go"},
		"example.com/m/internal/top")
	// Two files of one change union, and a package reached twice is named once.
	checkAffected(t, g, []string{"internal/mid/mid.go", "internal/core/core.go", "internal/core/core.go"},
		"example.com/m/internal/core", "example.com/m/internal/mid", "example.com/m/internal/top")
}

func TestAffectedIncludesTestOnlyImports(t *testing.T) {
	g := parseGraphFixture(t)
	// probe imports tiny from probe_test.go and from nowhere else: a graph that
	// read Imports alone would answer with tiny.
	checkAffected(t, g, []string{"internal/tiny/tiny.go"},
		"example.com/m/internal/probe", "example.com/m/internal/tiny")
	// ext reaches tinyext from ext_test.go, which is package ext_test.
	checkAffected(t, g, []string{"internal/tinyext/tinyext.go"},
		"example.com/m/internal/ext", "example.com/m/internal/tinyext")
}

func TestAffectedFollowsAnEmbeddedFile(t *testing.T) {
	g := parseGraphFixture(t)
	// No import list names the file; only go list's EmbedFiles ties it to emb,
	// and embuser reaches it through its import of emb.
	checkAffected(t, g, []string{"internal/emb/data/thing.txt"},
		"example.com/m/internal/emb", "example.com/m/internal/embuser")
	// A file only a test embeds is its own package's: nothing imports golden, so
	// nothing else can be in this answer.
	checkAffected(t, g, []string{"internal/golden/testdata/golden.txt"},
		"example.com/m/internal/golden")
}

func TestAffectedUnionsTheBuildTags(t *testing.T) {
	g := parseGraphFixture(t)
	// Each package below is imported by one file of internal/tagged gated on a
	// single tag; the fixture was listed under BuildTags, so the graph reaches
	// tagged for all four and would for one of them alone.
	for _, pkg := range []string{"tfonly", "fbonly", "dkonly", "lvonly"} {
		t.Run(pkg, func(t *testing.T) {
			checkAffected(t, g, []string{"internal/" + pkg + "/" + pkg + ".go"},
				"example.com/m/internal/"+pkg, "example.com/m/internal/tagged")
		})
	}
	// internal/tagged/ee.go is behind -tags ee, which none of the four sets: go
	// list reports it as ignored, and the package owns it all the same.
	checkAffected(t, g, []string{"internal/tagged/ee.go", "internal/tagged/tf.go"},
		"example.com/m/internal/tagged")
}

func TestAffectedOfAnUnmappedFileIsEmpty(t *testing.T) {
	g := parseGraphFixture(t)
	for _, f := range []string{
		"README.md",
		"docs/guide.md",
		"go.mod",
		"internal/core/testdata/input.json", // go list skips a testdata directory
	} {
		if got := g.Affected([]string{f}); len(got) != 0 {
			t.Errorf("Affected(%q) = %q, want no package", f, got)
		}
	}
	if got := g.Affected(nil); len(got) != 0 {
		t.Errorf("Affected(nil) = %q, want no package", got)
	}
}
