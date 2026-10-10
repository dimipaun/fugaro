package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
	// Every documented command and flag is in the real command tree.
	root := NewRootCmd()
	allFlags := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { allFlags[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, m := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(doc, -1) {
		if !allFlags[m[1]] {
			t.Errorf("docs/recipes.md names --%s, which no command has", m[1])
		}
	}
	for _, want := range []struct {
		path []string
		flag string
	}{{[]string{"run"}, "recipe"}, {[]string{"run"}, "pr"}, {[]string{"recipes", "ls"}, "json"}, {[]string{"recipes", "show"}, "json"}, {[]string{"recipes", "validate"}, "json"}, {[]string{"init"}, "base"}, {[]string{"init"}, "repo"}} {
		c, _, err := root.Find(want.path)
		if err != nil || c == nil || c.Name() != want.path[len(want.path)-1] {
			t.Errorf("no command %v", want.path)
			continue
		}
		if c.Flags().Lookup(want.flag) == nil {
			t.Errorf("fugaro %s has no --%s", strings.Join(want.path, " "), want.flag)
		}
	}
	for _, c := range []string{"fugaro validate", "fugaro watch", "fugaro ls", "fugaro image build", "fugaro init"} {
		if !strings.Contains(doc, c) {
			continue
		}
		if sub, _, err := root.Find(strings.Fields(c)[1:]); err != nil || sub == root {
			t.Errorf("docs/recipes.md names %q, which is not a command", c)
		}
	}
	// Each reserved key the guide lists is really refused with its own message.
	for _, key := range []string{"goto", "on_reject", "extends", "on_pass", "on_fail", "model", "models"} {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("docs/recipes.md never lists the reserved key %s", key)
		}
		_, ps := recipe.Parse([]byte("version: 1\nname: x\nsteps:\n  - review: {}\n" + key + ": 1\n"))
		if len(ps) == 0 || strings.Contains(recipe.ProblemsText(ps), "is not a recipe key") {
			t.Errorf("reserved key %s is not refused with a message of its own: %v", key, ps)
		}
	}
	// checks is refused only as a step name: the step type is check.
	if !strings.Contains(doc, "`checks`") {
		t.Error("docs/recipes.md never lists the reserved step name checks")
	}
	if _, ps := recipe.Parse([]byte("version: 1\nname: x\nsteps:\n  - checks: {}\n  - review: {}\n")); len(ps) == 0 || strings.Contains(recipe.ProblemsText(ps), "is not a recipe key") {
		t.Errorf("a checks step is not refused with a message of its own: %v", ps)
	}
	for _, sub := range []string{"ls", "show", "validate"} {
		if !strings.Contains(doc, "`"+sub+"`") || !strings.Contains(doc, "take `--json`") {
			t.Errorf("docs/recipes.md does not say that recipes %s takes --json", sub)
		}
	}
	readme, _ := os.ReadFile("../../README.md")
	if !strings.Contains(string(readme), "docs/recipes.md") {
		t.Error("README.md does not link docs/recipes.md")
	}
}
