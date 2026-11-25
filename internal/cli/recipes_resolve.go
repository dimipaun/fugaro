package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/lift"
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
// key only). A follow-up that inherits a name judges the project's copy of it
// (followUpRecipeOfName, followup.go), so most of those are refused here too.
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
// inherited by name, whose steps the launcher judges from the project's copy
// of the file when the store holds one (followUpRecipeOfName, followup.go)
// and from the previous run's manifest when it has not (prChain.PrevReport).
func refuseReportFollowUp(name string) error {
	return userErr("recipe %s writes this repository's run reports, and a repository has one report run at a time; a follow-up carries no manifest of the records it may read and skips the in-flight check a fresh launch makes, so it cannot use it — schedule a report run of its own with fugaro run --recipe %s", name, name)
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

// resolveRecipe finds name for the repository whose checkout is at root ("" for
// none): the project's copy of the repository's own recipe, then the project's
// name itself, then the catalog, in that order (D3303, docs/recipes.md §6). The
// checkout's .fugaro/recipes/ is never read: a repository's own recipe is the
// project's copy of it, which fugaro update publishes, and a repository that
// still holds the file and was never migrated is refused before anything is
// written (D3308, refuseUnmigratedFolderRecipe). Anything malformed is an error
// naming where it is.
func resolveRecipe(ctx context.Context, env *cloudEnv, root, name string, now time.Time, refresh bool) (*resolvedRecipe, error) {
	repo, provider := recipeLookupKeys(ctx, env, root)
	return resolveRecipeFor(ctx, env, repo, provider, root, name, now, refresh)
}

// resolveRecipeFor is resolveRecipe for a repository named rather than read off
// a checkout: repo and provider are the two recipe.LookupOrder is keyed by
// (D3303), and root is the checkout to refuse an unmigrated folder recipe by,
// "" when there is none. A launch knows the repository it launches — --repo,
// the pull request's chain — even where the directory it runs in is not a
// checkout of it, which is why the two are arguments here (resolveLaunchRecipe,
// run.go) and derived in resolveRecipe.
func resolveRecipeFor(ctx context.Context, env *cloudEnv, repo, provider, root, name string, now time.Time, refresh bool) (*resolvedRecipe, error) {
	if !recipe.IsNameOrCopy(name) {
		return nil, userErr("%q is not a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit, or the name of the project's copy of one (that name, a dot, the repository's slug and 8 hex)", pluginwire.Printable(name))
	}
	order := recipe.LookupOrder(name, provider, repo)
	note := projectRecipesNote(env.lc)
	if note == "" {
		if len(order) > 1 {
			// The repository has a copy of this name: that copy is the recipe
			// a run of the repository gets (D3303). When the store does not
			// hold it, the migration that publishes it may never have run over
			// this repository, which the default branch still holding the file
			// is what says (D3308).
			rr, err := projectRecipeNamed(ctx, env, order[0], now, refresh)
			if err != nil {
				return nil, err
			}
			if rr != nil {
				return rr, nil
			}
			if err := refuseUnmigratedFolderRecipe(ctx, env, repo, provider, root, name); err != nil {
				return nil, err
			}
		}
		rr, err := projectRecipeNamed(ctx, env, name, now, refresh)
		if err != nil {
			return nil, err
		}
		if rr != nil {
			return rr, nil
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
	msg := fmt.Sprintf("recipe %s is not in project %s (fugaro recipes ls) and not in the catalog (%s)",
		name, env.lc.Name, strings.Join(recipe.CatalogNames(), ", "))
	if note != "" {
		msg += "; note: " + note
	}
	return nil, userErr("%s", msg)
}

// projectRecipeNamed reads name from the project store through the cache, nil
// when the project holds no such recipe: the read, the parse and the cache drop
// each place a project recipe can be found makes.
func projectRecipeNamed(ctx context.Context, env *cloudEnv, name string, now time.Time, refresh bool) (*resolvedRecipe, error) {
	data, note, err := fetchProjectRecipe(ctx, env, now, name, refresh)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	where := "gs://fugaro-runs-" + env.lc.GCPProject + "/" + recipe.ObjectKey(name)
	rcp, err := parseRecipeAt(data, name, where)
	if err != nil {
		_ = localcfg.DropRecipeCache(os.Getenv, env.lc.Name, name)
		return nil, err
	}
	return &resolvedRecipe{Name: name, Source: recipe.SourceProject, Text: data, Recipe: rcp, Where: where, Note: note}, nil
}

// recipeLookupKeys is the repository the checkout at root is, and its git
// provider kind: the two the resolution's lookup order is keyed by (D3303).
// Both are "" when there is no checkout, or its origin names no repository this
// installation knows — and then the order is the name alone, which is where it
// resolved before the copies existed.
func recipeLookupKeys(ctx context.Context, env *cloudEnv, root string) (repo, provider string) {
	if root == "" {
		return "", ""
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return "", ""
	}
	return oi.Repo, repoProviderFor(ctx, env, oi.Repo)
}

// repoProviderFor is repo's git provider kind, read the way repoSlug reads it:
// the local config's entry for the repository first, the checkout's own
// fugaro.yaml when that entry names none. The copy name is keyed by it, so a
// lookup order built from another provider would miss the copy update
// published.
func repoProviderFor(ctx context.Context, env *cloudEnv, repo string) string {
	if p := env.localRepoProvider(repo); p != "" {
		return p
	}
	if cfg := checkoutConfig(ctx, repo); cfg != nil {
		return cfg.Git.Provider
	}
	return ""
}

// liftRecordMaxBytes bounds the migration record's read: the record is a small
// JSON document, and one over this is not one update wrote.
const liftRecordMaxBytes = 1 << 20

// refuseUnmigratedFolderRecipe refuses a resolution of name for repo when the
// repository's default branch still holds .fugaro/recipes/<name>.yaml and no
// migration record says fugaro update ran over the repository (D3308): the
// launcher resolves the name from the project store or the catalog, so the run
// would silently run another recipe than the repository's own file — the one
// case where a run changes under a repository update could not list (O6). The
// refusal names the repository, the recipe and the fix, and asks nothing.
//
// It is spec §5.5's release-A half: before the first release-A update, every
// repository that still holds its folder is refused with this fix, which is the
// one documented upgrade step. A repository with a record launches as before,
// and so does one whose default branch holds no such file — every launch from
// outside a checkout included, where there is no default branch to read.
func refuseUnmigratedFolderRecipe(ctx context.Context, env *cloudEnv, repo, provider, root, name string) error {
	if repo == "" || provider == "" || root == "" || env.bucket == nil {
		return nil
	}
	if !repoRecipeOnDefaultBranch(ctx, root, name) {
		return nil
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return nil
	}
	if _, _, err := env.bucket.ReadMaxStrict(ctx, lift.RecordPath(slug), liftRecordMaxBytes); err == nil {
		return nil
	} else if !errors.Is(err, blobx.ErrNotExist) {
		// The record cannot be read at all: the launch's own resolution meets
		// the same bucket, and refusing here would report the wrong failure.
		return nil
	}
	shown := pluginwire.Printable(repo)
	return userErr("recipe %s is still %s's own file: its default branch holds %s, and %s has no migration record, so this launch would run the project's or the catalog's recipe of that name instead; list %s in the project, then run fugaro update in your own terminal to publish the repository's recipes as the project's copies",
		name, shown, recipe.RepoPath(name), shown, shown)
}

// repoRecipeOnDefaultBranch reports whether the checkout at root's default
// branch holds .fugaro/recipes/<name>.yaml. It is the read S5's release-A gate
// makes too (R16), and the one D3308 needs: update migrates a repository's
// recipes from its default branch, so that is where the file that would have
// become the project's copy still stands. No default branch ref, or no such
// file there, answers false.
func repoRecipeOnDefaultBranch(ctx context.Context, root, name string) bool {
	ref := defaultBranchRef(ctx, root)
	if ref == "" {
		return false
	}
	if _, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/remotes/"+ref+":"+recipe.RepoPath(name)); err != nil {
		return false
	}
	return true
}
