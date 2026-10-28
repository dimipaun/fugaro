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
func (r *resolvedRecipe) taskRecipe() *task.Recipe { return r.taskRecipeEmbedding(false) }

// taskRecipeEmbedding is taskRecipe, except that with embed the catalog
// default is carried too: an explicit --recipe default that must beat
// agent.recipe, which the runner would otherwise apply to a task without one.
func (r *resolvedRecipe) taskRecipeEmbedding(embed bool) *task.Recipe {
	switch {
	case r.Name == recipe.DefaultName && r.Source == recipe.SourceCatalog && !embed:
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

// refuseFollowUpReviewMode refuses rcp for a follow-up (fugaro run --pr N
// continuing PR N, the only kind newFollowUpSpec and carryChosenRecipe
// build): mode: review only ever reviews an existing pull request and
// never pushes or opens one (design generic-tool.md §3.6, G12), so it
// cannot continue one either. The runner refuses the same mismatch again,
// at bootstrap, for a recipe this never sees — a repository recipe whose
// text isn't read until the run itself, or a follow-up that inherits no
// recipe at all, which gets no agent.recipe: the runner applies that key to
// first runs only (internal/runner/recipe.go) and falls back to the
// default name, .fugaro/recipes/default.yaml at the run's base; this is the
// CLI's own chance to say so before any cloud work starts.
func refuseFollowUpReviewMode(rcp *recipe.Recipe, name string) error {
	if rcp.Mode == recipe.ModeReview {
		return userErr("recipe %s is mode: review, which only reviews an existing pull request and cannot continue one; launch it with --recipe %s --pr N instead (no other --pr)", name, name)
	}
	return nil
}

// refuseFollowUpArtifactMode refuses rcp for a follow-up (fugaro run --pr N
// continuing PR N): a mode: artifact recipe renders a deliverable into the
// run's bucket and opens no pull request, so it has no pull request to
// continue. The follow-up path exists to resolve a pull request's chain —
// its branch, its previous run, its account, and the ref and workflow it
// inherits — and an artifact run has none of that to resolve, so the
// refusal is at the launch rather than a refusal to carry the mode. The
// runner refuses the same mismatch again, at bootstrap, for a recipe this
// never sees: it keys on the RESOLVED recipe (internal/runner/runner.go),
// which is the backstop for a follow-up that inherits a repository recipe by
// name — a task.Recipe with no text — since that file is read at the run's
// own base, and for one that inherits no recipe at all, which resolves to
// the repository's .fugaro/recipes/default.yaml (agent.recipe is a first-run
// key only). refuseFollowUpRepoRecipe reads the ones this checkout has, so
// most of those are refused here too.
func refuseFollowUpArtifactMode(rcp *recipe.Recipe, name string) error {
	if rcp.Mode == recipe.ModeArtifact {
		return userErr("recipe %s is mode: artifact, which renders a deliverable into the run's bucket and opens no pull request; it takes no --pr, and there is no pull request for it to continue", name)
	}
	return nil
}

// refuseFollowUpReportMode refuses rcp for a follow-up (fugaro run --pr N
// continuing PR N): a report recipe is the digest step's (task-governance
// T3.6), and the launch that runs one lists the records it may read and
// refuses it while another report run of the repository is going
// (digestManifest). A follow-up does neither — it carries no digest_runs at
// all, and its recipe arrives from the previous run, so even a fresh listing
// would be someone else's list — so the run would write the reports of a
// repository whose records it never read, and a second report run could write
// them beside the one already going. The refusal is chooseRecipe's own, from
// the other side: there it is a batch that cannot carry a report recipe, here
// a follow-up that cannot.
//
// The runner refuses the same mismatch again, at bootstrap, keyed on the
// recipe it resolved at the task's base (internal/runner/runner.go): this is
// the refusal of a recipe the launcher resolved itself, from this checkout or
// the project or the catalog.
func refuseFollowUpReportMode(rcp *recipe.Recipe, name string) error {
	if recipe.UsesDigest(rcp) {
		return refuseReportFollowUp(name)
	}
	return nil
}

// refuseReportFollowUp is refuseFollowUpReportMode's refusal for a caller
// that knows the recipe's name but has no text to parse: a repository recipe
// inherited by name, whose steps the launcher judges from this checkout's
// copy of the file when it has one (refuseFollowUpRepoRecipe) and from the
// previous run's manifest when it has not (prChain.PrevReport).
func refuseReportFollowUp(name string) error {
	return userErr("recipe %s writes this repository's run reports, and a repository has one report run at a time; a follow-up carries no manifest of the records it may read and skips the in-flight check a fresh launch makes, so it cannot use it — schedule a report run of its own with fugaro run --recipe %s", name, name)
}

// refuseFollowUpRepoRecipe refuses the same mismatches for a follow-up
// that INHERITS a repository recipe by name — the previous task's
// task.Recipe{Name, Source: repo}, and the chosen recipe carryChosenRecipe
// keeps the same way — which carries no text for the refusals above to
// read. The file is read from the working directory's checkout when there is
// one; a name that checkout does not have, or a file it cannot parse, is
// left alone, because the runner reads the name at THIS task's base
// (readRepoRecipe, internal/runner/recipe.go) and refuses a bad, artifact or
// report one there itself. The report mismatch is refused here without the
// file too: a launcher with no copy of it still knows whether the previous
// run's task carried a manifest, and so whether the recipe this follow-up
// inherits is a report recipe (newFollowUpSpec, prChain.PrevReport). Nothing
// but the checkout is consulted: the previous run ran a repository recipe,
// and a same-named project or catalog recipe is not the one it ran.
func refuseFollowUpRepoRecipe(ctx context.Context, repo, name string) error {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return nil
	}
	data, found, err := recipe.ReadRepoFile(root, name)
	if err != nil || !found {
		return nil
	}
	where := filepath.Join(root, filepath.FromSlash(recipe.RepoPath(name)))
	rcp, err := parseRecipeAt(data, name, where)
	if err != nil {
		return nil
	}
	if err := refuseFollowUpReviewMode(rcp, name); err != nil {
		return err
	}
	if err := refuseFollowUpArtifactMode(rcp, name); err != nil {
		return err
	}
	return refuseFollowUpReportMode(rcp, name)
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
	return nil, "", bucketErrFor("gs://"+bucket, "reading "+key, "the project recipe "+name, err)
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
	note := projectRecipesNote(env.lc)
	if note == "" {
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
	}
	if to, ok := recipe.Renamed[name]; ok {
		return nil, userErr("%s was renamed in 0.7.0: use %s", name, to)
	}
	if text, ok := recipe.CatalogText(name); ok {
		rcp, err := parseRecipeAt(text, name, "catalog recipe "+name)
		if err != nil {
			return nil, err
		}
		return &resolvedRecipe{Name: name, Source: recipe.SourceCatalog, Text: text, Recipe: rcp, Where: "the catalog", Note: note}, nil
	}
	msg := fmt.Sprintf("recipe %s is not in %s, not in project %s (fugaro recipes ls) and not in the catalog (%s)",
		name, recipe.RepoDir, env.lc.Name, strings.Join(recipe.CatalogNames(), ", "))
	if note != "" {
		msg += "; note: " + note
	}
	return nil, userErr("%s", msg)
}
