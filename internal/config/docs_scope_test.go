package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
