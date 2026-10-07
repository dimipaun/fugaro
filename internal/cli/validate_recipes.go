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
		if _, inCatalog := recipe.CatalogText(n); !have[n] && !inCatalog {
			warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: fmt.Sprintf("%s is neither in %s nor in the catalog, so it must be a project recipe (fugaro recipes ls); a run launched outside a checkout of this repository can't get one", n, recipe.RepoDir)})
		}
	}
	return problems, warnings
}

// withRecipeRoles is cfg as the runner will see it for the recipe the file
// selects (agent.recipe, else the default): when that recipe maps reviewer to
// coder, the reviewer role is the coder's (runner.ApplyRoles, the runner's
// own), so the pin, allow-list and provider checks judge the models the run
// uses and not an agent.models.reviewer it ignores. The recipe is the
// checkout's or the catalog's; a project recipe is not readable here, so cfg
// is returned as it is.
func withRecipeRoles(root string, cfg *config.Config) *config.Config {
	name := cfg.Agent.Recipe
	if name == "" {
		name = recipe.DefaultName
	}
	var rcp *recipe.Recipe
	if recipe.NameRE.MatchString(name) {
		data, found, err := recipe.ReadRepoFile(root, name)
		if err != nil {
			return cfg
		}
		if !found {
			data, _ = recipe.CatalogText(name)
		}
		if data == nil {
			return cfg
		}
		var ps []recipe.Problem
		if rcp, ps = recipe.Parse(data); len(ps) > 0 || rcp == nil {
			return cfg
		}
	}
	if rcp == nil || !rcp.ReviewerIsCoder {
		return cfg
	}
	aliased := *cfg
	runner.ApplyRoles(&aliased.Agent, rcp)
	return &aliased
}
