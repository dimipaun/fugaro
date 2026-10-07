package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// TestRecipesGuideMatchesTheCLI: docs/recipes.md must describe the commands
// and the catalog that exist, and nothing else.
func TestRecipesGuideMatchesTheCLI(t *testing.T) {
	data, err := os.ReadFile("../../docs/recipes.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	real := map[string]bool{}
	for _, c := range newRecipesCmd().Commands() {
		real[c.Name()] = true
		if !strings.Contains(doc, "fugaro recipes "+c.Name()) {
			t.Errorf("docs/recipes.md never names fugaro recipes %s", c.Name())
		}
	}
	for _, m := range regexp.MustCompile(`fugaro recipes ([a-z]+)`).FindAllStringSubmatch(doc, -1) {
		if !real[m[1]] {
			t.Errorf("docs/recipes.md names fugaro recipes %s, which does not exist", m[1])
		}
	}
	for _, name := range recipe.CatalogNames() {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/recipes.md never names the catalog's %s", name)
		}
	}
	for _, want := range []string{"fugaro run --recipe", "agent.recipe", ".fugaro/recipes/", "fugaro/recipes/", "16 KiB", "0.5.0", "fugaro init"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/recipes.md never says %q", want)
		}
	}
	readme, _ := os.ReadFile("../../README.md")
	if !strings.Contains(string(readme), "docs/recipes.md") {
		t.Error("README.md does not link docs/recipes.md")
	}
}
