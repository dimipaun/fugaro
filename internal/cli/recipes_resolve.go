package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// resolvedRecipe is the recipe the CLI found for a name.
type resolvedRecipe struct {
	Name   string
	Source recipe.Source
	Text   []byte
	Recipe *recipe.Recipe
	Where  string
	Note   string
}

// taskRecipe is what task.json carries: nothing for the catalog default (so
// a default run needs no 0.5.0 runner), the name alone for a repository
// recipe (the runner reads it at the task's ref), else the exact text.
func (r *resolvedRecipe) taskRecipe() *task.Recipe {
	switch {
	case r.Name == recipe.DefaultName && r.Source == recipe.SourceCatalog:
		return nil
	case r.Source == recipe.SourceRepo:
		return &task.Recipe{Name: r.Name, Source: string(recipe.SourceRepo)}
	}
	return &task.Recipe{Name: r.Name, Source: string(r.Source), SHA256: recipe.Sum(r.Text), YAML: string(r.Text)}
}

// parseRecipeAt parses a recipe found at where under name.
func parseRecipeAt(data []byte, name, where string) (*recipe.Recipe, error) {
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, userErr("%s is invalid: %s", where, pluginwire.Printable(recipe.ProblemsText(ps)))
	}
	if rcp.Name != name {
		return nil, userErr("%s names itself %q; the file name and the name must agree", where, rcp.Name)
	}
	return rcp, nil
}

// projectRecipesNote is why lc's project recipes are not read, or "": as
// for the shared config, only the default runs bucket name is supported.
func projectRecipesNote(lc *localcfg.Config) string {
	if want := "fugaro-runs-" + lc.GCPProject; lc.RunsBucketName() != want {
		return fmt.Sprintf("project recipes need the default runs bucket name %s (this installation's is %q)", want, lc.RunsBucketName())
	}
	return ""
}

// fetchProjectRecipe reads project recipe name (fugaro/recipes/<name>.yaml in
// the runs bucket) through the cache (decision D9): a fresh entry of this
// bucket answers unless refresh; otherwise the bucket is read strictly, and
// only when it can't be reached does an entry up to 7 days old stand in, with
// a note. A not-found drops the entry; nil data means there is no such recipe.
func fetchProjectRecipe(ctx context.Context, env *cloudEnv, now time.Time, name string, refresh bool) ([]byte, string, error) {
	lc := env.lc
	bucket := "fugaro-runs-" + lc.GCPProject
	key := recipe.ObjectKey(name)
	cached, ok := localcfg.LoadRecipeCache(os.Getenv, lc.Name, name)
	ours := ok && cached.GCPProject == lc.GCPProject && cached.Bucket == bucket
	if ours && !refresh && cached.Fresh(now) {
		return []byte(cached.YAML), "", nil
	}
	data, gen, err := env.bucket.ReadMaxStrict(ctx, key, recipe.MaxBytes)
	switch {
	case err == nil:
		_ = localcfg.SaveRecipeCache(os.Getenv, lc.Name, name, localcfg.SharedCacheEntry{GCPProject: lc.GCPProject, Bucket: bucket, Generation: gen, CheckedAt: now, YAML: string(data)})
		return data, "", nil
	case errors.Is(err, blobx.ErrNotExist):
		_ = localcfg.DropRecipeCache(os.Getenv, lc.Name, name)
		return nil, "", nil
	case errors.Is(err, blobx.ErrTooLarge):
		_ = localcfg.DropRecipeCache(os.Getenv, lc.Name, name)
		return nil, "", userErr("gs://%s/%s is over the 16 KiB limit; publish it again with fugaro recipes publish", bucket, key)
	case isUnreachable(err) && ours && cached.UsableOffline(now):
		return []byte(cached.YAML), fmt.Sprintf("using the cached project recipe %s, %s old: gs://%s is unreachable", name, ageDays(now.Sub(cached.CheckedAt)), bucket), nil
	}
	return nil, "", bucketErr("gs://"+bucket, "reading "+key, err)
}

// resolveRecipe finds name in the repository checkout at root ("" for none),
// the project's runs bucket, and the catalog, in that order (docs/design/
// recipes.md §5). Anything malformed is an error naming where it is.
func resolveRecipe(ctx context.Context, env *cloudEnv, root, name string, now time.Time, refresh bool) (*resolvedRecipe, error) {
	if !recipe.NameRE.MatchString(name) {
		return nil, userErr("%q is not a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", pluginwire.Printable(name))
	}
	if root != "" {
		data, found, err := recipe.ReadRepoFile(root, name)
		if err != nil {
			return nil, userErr("%v", err)
		}
		if found {
			where := filepath.Join(root, filepath.FromSlash(recipe.RepoPath(name)))
			rcp, err := parseRecipeAt(data, name, where)
			if err != nil {
				return nil, err
			}
			return &resolvedRecipe{Name: name, Source: recipe.SourceRepo, Text: data, Recipe: rcp, Where: where}, nil
		}
	}
	var note string
	if projectRecipesNote(env.lc) == "" {
		data, n, err := fetchProjectRecipe(ctx, env, now, name, refresh)
		if err != nil {
			return nil, err
		}
		if data != nil {
			where := "gs://fugaro-runs-" + env.lc.GCPProject + "/" + recipe.ObjectKey(name)
			rcp, err := parseRecipeAt(data, name, where)
			if err != nil {
				_ = localcfg.DropRecipeCache(os.Getenv, env.lc.Name, name)
				return nil, err
			}
			return &resolvedRecipe{Name: name, Source: recipe.SourceProject, Text: data, Recipe: rcp, Where: where, Note: n}, nil
		}
		note = n
	}
	if text, ok := recipe.CatalogText(name); ok {
		rcp, err := parseRecipeAt(text, name, "catalog recipe "+name)
		if err != nil {
			return nil, err
		}
		return &resolvedRecipe{Name: name, Source: recipe.SourceCatalog, Text: text, Recipe: rcp, Where: "the catalog", Note: note}, nil
	}
	return nil, userErr("recipe %s is not in %s, not in project %s (fugaro recipes ls) and not in the catalog (%s)",
		name, recipe.RepoDir, env.lc.Name, strings.Join(recipe.CatalogNames(), ", "))
}
