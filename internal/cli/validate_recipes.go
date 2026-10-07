package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
)

// recipeDirProblems checks the repository's .fugaro/recipes/ under root as
// the runner would read it, and warns about agent.recipe: older binaries
// refuse the key, and a name found neither here nor in the catalog can only
// be a project recipe, which a run launched without a checkout can't get.
func recipeDirProblems(root string, cfg *config.Config) (problems, warnings []config.Problem) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(recipe.RepoDir)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		problems = append(problems, config.Problem{Path: recipe.RepoDir, Message: err.Error()})
	}
	have := map[string]bool{}
	for _, e := range entries {
		path := recipe.RepoDir + "/" + e.Name()
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		switch {
		case !ok:
			warnings = append(warnings, config.Problem{Path: path, Message: "is not a .yaml file; the runner reads only <name>.yaml"})
			continue
		case !recipe.NameRE.MatchString(name):
			problems = append(problems, config.Problem{Path: path, Message: "the file name is not a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit"})
			continue
		}
		data, found, err := recipe.ReadRepoFile(root, name)
		if err != nil {
			problems = append(problems, config.Problem{Path: path, Message: err.Error()})
			continue
		}
		if !found {
			continue
		}
		rcp, ps := recipe.Parse(data)
		for _, p := range ps {
			msg := p.Message
			if p.Path != "" {
				msg = p.Path + ": " + msg
			}
			problems = append(problems, config.Problem{Path: path, Line: p.Line, Message: msg})
		}
		switch {
		case len(ps) > 0:
		case rcp.Name != name:
			problems = append(problems, config.Problem{Path: path, Message: fmt.Sprintf("names itself %q; the file name and the name must agree (the runner looks a recipe up by its file name)", rcp.Name)})
		default:
			have[name] = true
		}
	}
	if n := cfg.Agent.Recipe; n != "" {
		warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: "fugaro before " + recipesSince + " refuses this key: every teammate's CLI, every CI pin and every workflow's job image must be on " + recipesSince + " or later before you merge it"})
		if _, inCatalog := recipe.CatalogText(n); inCatalog && !have[n] {
			warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: fmt.Sprintf("%s is a catalog recipe here, but a project recipe of this name takes precedence over the catalog for every run (fugaro recipes show %s)", n, n)})
		} else if !have[n] && !inCatalog {
			warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: fmt.Sprintf("%s is neither in %s nor in the catalog, so it must be a project recipe (fugaro recipes ls); a run launched outside a checkout of this repository can't get one", n, recipe.RepoDir)})
		}
	}
	return problems, warnings
}

// localRecipe is the recipe the file selects (agent.recipe, else the default)
// as far as this checkout and the catalog know it: nil when it is neither
// (a project recipe, which is not readable offline) or is malformed (its own
// problem is reported elsewhere).
func localRecipe(root string, cfg *config.Config) *recipe.Recipe {
	name := cfg.Agent.Recipe
	if name == "" {
		name = recipe.DefaultName
	}
	if !recipe.NameRE.MatchString(name) {
		return nil
	}
	data, found, err := recipe.ReadRepoFile(root, name)
	if err != nil {
		return nil
	}
	if !found {
		data, _ = recipe.CatalogText(name)
	}
	if data == nil {
		return nil
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil
	}
	return rcp
}

// withRecipeRoles is cfg as the runner will see it for the recipe the file
// selects: when that recipe maps reviewer to coder, the reviewer role is the
// coder's (runner.ApplyRoles, the runner's own), so the pin, allow-list and
// provider checks judge the models the run uses and not an
// agent.models.reviewer it ignores. A project recipe is not readable here,
// so cfg is returned as it is.
func withRecipeRoles(root string, cfg *config.Config) *config.Config {
	rcp := localRecipe(root, cfg)
	if rcp == nil || !rcp.ReviewerIsCoder {
		return cfg
	}
	aliased := *cfg
	runner.ApplyRoles(&aliased.Agent, rcp)
	return &aliased
}

// annotateProjectRecipe tells, on the reviewer's problems, that they may not
// apply: agent.recipe names a recipe found neither in the checkout nor in the
// catalog, so a project recipe that maps reviewer to coder would ignore the
// key the problem is about.
func annotateProjectRecipe(root string, cfg *config.Config, problems []config.Problem) {
	n := cfg.Agent.Recipe
	if n == "" || localRecipe(root, cfg) != nil || !recipe.NameRE.MatchString(n) {
		return
	}
	for i, p := range problems {
		if strings.HasPrefix(p.Path, "agent.models.reviewer") {
			problems[i].Message += fmt.Sprintf(" (if project recipe %s maps reviewer to coder, this key is ignored; remove it)", n)
		}
	}
}
