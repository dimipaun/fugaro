package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// resolveRecipe finds the run's recipe (docs/design/recipes.md §5): the name
// from the task, else (first runs only) agent.recipe, else default; the text
// from the repository layer at the run's base, else the task's embedded
// copy, else the catalog. Anything wrong is an error: there is no fallback
// to default.
func (r *run) resolveRecipe(ctx context.Context, cfg *config.Config) (*recipe.Recipe, runstore.RecipeRecord, error) {
	name, emb := recipe.DefaultName, r.spec.Recipe
	switch {
	case emb != nil:
		name = emb.Name
	case r.follow == nil && cfg.Agent.Recipe != "":
		name = cfg.Agent.Recipe
	}
	where := recipe.RepoPath(name)
	data, found, err := r.readRepoRecipe(ctx, name)
	if err != nil {
		return nil, runstore.RecipeRecord{}, fmt.Errorf("recipe %s: %w", name, err)
	}
	src := recipe.SourceRepo
	switch {
	case found:
	case emb != nil && emb.Source == string(recipe.SourceRepo):
		return nil, runstore.RecipeRecord{}, fmt.Errorf("recipe %s came from %s in the launching checkout, but %s has no such file: commit and push it, then launch again", name, where, r.spec.Ref)
	case emb != nil:
		data, src, where = []byte(emb.YAML), recipe.Source(emb.Source), emb.Source+" recipe "+name+" (embedded in task.json)"
	default:
		text, ok := recipe.CatalogText(name)
		if !ok {
			return nil, runstore.RecipeRecord{}, fmt.Errorf("recipe %s is not in %s at %s nor in the catalog (%s); a project recipe reaches a run only through fugaro run, which embeds it",
				name, recipe.RepoDir, r.spec.Ref, strings.Join(recipe.CatalogNames(), ", "))
		}
		data, src, where = text, recipe.SourceCatalog, "catalog recipe "+name
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, runstore.RecipeRecord{}, fmt.Errorf("%s is invalid: %s", where, recipe.ProblemsText(ps))
	}
	if rcp.Name != name {
		return nil, runstore.RecipeRecord{}, fmt.Errorf("%s names itself %q; the file name and the name must agree", where, rcp.Name)
	}
	sum := recipe.Sum(data)
	r.d.Log.Info("recipe", "name", name, "source", string(src), "sha256", sum)
	return rcp, runstore.RecipeRecord{Name: name, Source: string(src), SHA256: sum}, nil
}

// readRepoRecipe reads .fugaro/recipes/<name>.yaml: for a first run from the
// checkout at the task's ref, for a follow-up from origin/<ref> (the base, as
// readBaseConfig reads fugaro.yaml). Only a regular file is read: a symbolic
// link anywhere on the path, or a directory, is an error. found is false when
// there is no such file.
func (r *run) readRepoRecipe(ctx context.Context, name string) (data []byte, found bool, err error) {
	rel := recipe.RepoPath(name)
	if r.follow != nil {
		rev := "origin/" + r.spec.Ref
		mode, err := r.repo.TreeEntryMode(ctx, rev, rel)
		switch {
		case err != nil:
			return nil, false, err
		case mode == "":
			return nil, false, nil
		case mode != "100644" && mode != "100755":
			return nil, false, fmt.Errorf("%s at %s is not a regular file", rel, rev)
		}
		if data, err = r.repo.ShowFile(ctx, rev, rel); err != nil {
			return nil, false, err
		}
	} else {
		return recipe.ReadRepoFile(r.d.WorkDir, name)
	}
	if len(data) > recipe.MaxBytes {
		return nil, false, fmt.Errorf("%s is %d bytes, over the 16 KiB limit", rel, len(data))
	}
	return data, true, nil
}
