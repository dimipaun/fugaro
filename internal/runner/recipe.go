package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
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
		return nil, runstore.RecipeRecord{}, fmt.Errorf("recipe %s is a repository recipe (%s), but %s has no such file at its base: commit and push it to %s, then launch again", name, where, r.spec.Ref, r.spec.Ref)
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

// showRepoFile is the read of a base file; a test replaces it to prove the
// size check comes first.
var showRepoFile = func(ctx context.Context, repo *gitops.Repo, rev, rel string) ([]byte, error) {
	return repo.ShowFile(ctx, rev, rel)
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
		// Size the blob first: show would load all of it.
		n, err := r.repo.BlobSize(ctx, rev, rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, false, nil
		case err != nil:
			return nil, false, err
		case n > recipe.MaxBytes:
			return nil, false, fmt.Errorf("%s is %d bytes, over the 16 KiB limit", rel, n)
		}
		if data, err = showRepoFile(ctx, r.repo, rev, rel); err != nil {
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

// ApplyRoles makes the reviewer role run as the coder role does when the
// recipe maps reviewer to coder: the coder's model and per-call output
// limit. The review prompt, its fresh session and the readiness rule stay
// the reviewer's. It runs before every model check, so the pins, the
// allow-list and the prices see ordinary model IDs.
func ApplyRoles(a *config.Agent, rcp *recipe.Recipe) {
	if !rcp.ReviewerIsCoder {
		return
	}
	a.Models.Reviewer = a.ModelFor(config.RoleCoder)
	a.MaxOutputTokens.Reviewer = a.MaxOutputTokens.Coder
}
