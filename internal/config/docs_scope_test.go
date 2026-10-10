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
