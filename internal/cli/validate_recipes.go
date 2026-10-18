package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
		_, inCatalog := recipe.CatalogText(n)
		renamedTo, renamed := recipe.Renamed[n]
		switch {
		case have[n]:
		case renamed:
			warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: fmt.Sprintf("%s was renamed in 0.7.0: use %s (a project or repository recipe of this name still wins over the rename)", n, renamedTo)})
		case inCatalog:
			warnings = append(warnings, config.Problem{Path: "agent.recipe", Message: fmt.Sprintf("%s is a catalog recipe here, but a project recipe of this name takes precedence over the catalog for every run (fugaro recipes show %s)", n, n)})
		default:
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
	return localRecipeNamed(root, name)
}

// localRecipeNamed is localRecipe for a recipe NAME the caller resolved
// itself, rather than the one this file selects: the follow-up case, where
// agent.recipe is not consulted at all (recipeForLaunch, internal/cli/run.go,
// mirroring the runner's resolveRecipe), so the name is recipe.DefaultName
// whatever the file says. The repository layer is read first and the catalog
// second, the order the runner reads them in.
//
// An empty root is no checkout of the task's repository (checkoutRoot), and
// then there is no repository layer to read: ReadRepoFile would be handed a
// RELATIVE path and resolve it against the process's own working directory,
// so a directory that merely happens to hold .fugaro/recipes/<name>.yaml
// would decide the launch. The catalog is the only answer that can be given
// without a checkout, and the runner — which reads the repository layer at
// the task's ref — is the one that decides the run either way.
func localRecipeNamed(root, name string) *recipe.Recipe {
	if !recipe.NameRE.MatchString(name) {
		return nil
	}
	var data []byte
	found := false
	if root != "" {
		var err error
		if data, found, err = recipe.ReadRepoFile(root, name); err != nil {
			return nil
		}
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

// gateCheckProblems is `fugaro validate`'s check that the recipe the file
// selects (localRecipe) fits the file's workflows: the same refusals
// bootstrap's checkGateCommands makes at launch (runner.RecipeGateProblems)
// — a check step whose commands.* key is empty, and, for a mode: artifact
// recipe, its render and probe steps and the caps they declare against the
// workflow's memory — checked against every workflow since validate has no
// one run to pick a single workflow for. nil when the recipe can't be read
// locally (a project recipe).
func gateCheckProblems(root string, cfg *config.Config) []config.Problem {
	rcp := localRecipe(root, cfg)
	if rcp == nil {
		return nil
	}
	var problems []config.Problem
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		// Each problem is reported at the key it is about: commands.render
		// and commands.probe under workflows.<name>.commands, the caps that
		// cannot fit under workflows.<name>.resources.memory.
		for _, p := range runner.RecipeGateProblems(rcp, cfg.Workflows[name]) {
			problems = append(problems, config.Problem{Path: "workflows." + name + "." + p.Field, Message: "the " + p.Message})
		}
	}
	return problems
}

// withRecipeRoles is cfg as the runner will see it for the recipe the file
// selects: when that recipe maps reviewer to coder, or coder to reviewer,
// the aliased role takes the other's model and output limit
// (runner.ApplyRoles, the runner's own), so the pin, allow-list, provider
// and policy-tightening checks judge the models and limits the run really
// uses and not an agent.models.reviewer or agent.models.coder it ignores. A
// project recipe is not readable here, so cfg is returned as it is.
func withRecipeRoles(root string, cfg *config.Config) *config.Config {
	rcp := localRecipe(root, cfg)
	if rcp == nil || (!rcp.ReviewerIsCoder && !rcp.CoderIsReviewer) {
		return cfg
	}
	aliased := *cfg
	runner.ApplyRoles(&aliased.Agent, rcp)
	return &aliased
}

// annotateProjectRecipe tells, on the coder's and reviewer's problems, that
// they may not apply: agent.recipe names a recipe found neither in the
// checkout nor in the catalog, so a project recipe that maps reviewer to
// coder (or coder to reviewer) would ignore the key the problem is about.
func annotateProjectRecipe(root string, cfg *config.Config, problems []config.Problem) {
	n := cfg.Agent.Recipe
	if n == "" || localRecipe(root, cfg) != nil || !recipe.NameRE.MatchString(n) {
		return
	}
	for i, p := range problems {
		switch {
		case strings.HasPrefix(p.Path, "agent.models.reviewer"):
			problems[i].Message += fmt.Sprintf(" (if project recipe %s maps reviewer to coder, this key is ignored; remove it)", n)
		case strings.HasPrefix(p.Path, "agent.models.coder"):
			problems[i].Message += fmt.Sprintf(" (if project recipe %s maps coder to reviewer, this key is ignored; remove it)", n)
		}
	}
}
