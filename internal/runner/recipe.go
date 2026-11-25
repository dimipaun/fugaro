package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// resolveRecipe finds the run's recipe (docs/recipes/spec.md §5): the name
// from the task, else (first runs only) agent.recipe, else default; the text
// from the task's embedded copy, else the catalog. The repository's own
// .fugaro/recipes/ is never read (L8's release A, D3304): a task that leaves
// its recipe to that folder — Source: repo, or no embedded recipe where the
// ref still holds the file — is refused at bootstrap with the fix rather
// than run on whatever the folder says. Anything wrong is an error: there is
// no fallback to default.
func (r *run) resolveRecipe(ctx context.Context, cfg *config.Config) (*recipe.Recipe, runstore.RecipeRecord, error) {
	name, emb := recipe.DefaultName, r.spec.Recipe
	switch {
	case emb != nil:
		name = emb.Name
	case r.follow == nil && cfg.Agent.Recipe != "":
		name = cfg.Agent.Recipe
	}
	var data []byte
	var src recipe.Source
	var where string
	switch {
	case emb != nil && emb.Source == string(recipe.SourceRepo):
		return nil, runstore.RecipeRecord{}, repoRecipeRefusal(name, "the task names the repository's copy")
	case emb != nil:
		data, src, where = []byte(emb.YAML), recipe.Source(emb.Source), emb.Source+" recipe "+name+" (embedded in task.json)"
	default:
		present, err := r.hasRepoRecipe(ctx, name)
		if err != nil {
			return nil, runstore.RecipeRecord{}, fmt.Errorf("recipe %s: %w", name, err)
		}
		if present {
			return nil, runstore.RecipeRecord{}, repoRecipeRefusal(name, "the ref still holds "+recipe.RepoPath(name))
		}
		text, ok := recipe.CatalogText(name)
		if !ok {
			return nil, runstore.RecipeRecord{}, endClass{failure.ConfigInvalid, fmt.Errorf("recipe %s is neither embedded in the task nor in the catalog (%s); a repository or project recipe reaches a run only through fugaro run, which embeds it",
				name, strings.Join(recipe.CatalogNames(), ", "))}
		}
		data, src, where = text, recipe.SourceCatalog, "catalog recipe "+name
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, runstore.RecipeRecord{}, endClass{failure.ConfigInvalid, fmt.Errorf("%s is invalid: %s", where, recipe.ProblemsText(ps))}
	}
	if rcp.Name != name {
		return nil, runstore.RecipeRecord{}, endClass{failure.ConfigInvalid, fmt.Errorf("%s names itself %q; the file name and the name must agree", where, rcp.Name)}
	}
	sum := recipe.Sum(data)
	r.d.Log.Info("recipe", "name", name, "source", string(src), "sha256", sum)
	return rcp, runstore.RecipeRecord{Name: name, Source: string(src), SHA256: sum}, nil
}

// repoRecipeRefusal is the refusal of a task that leaves its recipe to the
// repository's folder, whose text a run no longer reads (D3304, R8). It
// names the one fix rather than the folder's content: the launcher embeds
// the copy the project resolves (B16 lifted it there), so a second launch on
// an upgraded CLI runs the same recipe the repository meant.
func repoRecipeRefusal(name, why string) error {
	return endClass{failure.ConfigInvalid, fmt.Errorf("recipe %s is the repository's own (%s) and a run no longer reads %s (D3237), so this task cannot be run as it stands: upgrade fugaro (brew upgrade dimipaun/tap/fugaro, then fugaro update) and launch again, which embeds the project's copy",
		name, why, recipe.RepoDir)}
}

// hasRepoRecipe reports whether .fugaro/recipes/<name>.yaml is at the run's
// ref: for a first run in the checkout at the task's ref, for a follow-up on
// origin/<ref> (the base, as readBaseConfig reads fugaro.yaml). Nothing is
// read from it — a task that embeds no recipe while the file is there is
// refused (D3304) — so any entry at that path counts, whatever its type or
// size.
func (r *run) hasRepoRecipe(ctx context.Context, name string) (bool, error) {
	rel := recipe.RepoPath(name)
	if r.follow != nil {
		mode, err := r.repo.TreeEntryMode(ctx, "origin/"+r.spec.Ref, rel)
		if err != nil {
			return false, err
		}
		return mode != "", nil
	}
	_, err := os.Lstat(filepath.Join(r.d.WorkDir, filepath.FromSlash(rel)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// ApplyRoles makes one role run as the other does, when the recipe maps
// them: the coder's model and per-call output limit become the reviewer's
// (roles: {reviewer: coder}), or the reviewer's become the coder's (roles:
// {coder: reviewer}, the mirror image: premium's "coder on the reviewer's
// model"). Parse refuses setting both at once. The review prompt, its fresh
// session and the readiness rule always stay the reviewer's. It runs before
// every model check, so the pins, the allow-list and the prices see
// ordinary model IDs.
func ApplyRoles(a *config.Agent, rcp *recipe.Recipe) {
	switch {
	case rcp.ReviewerIsCoder:
		a.Models.Reviewer = a.ModelFor(config.RoleCoder)
		a.MaxOutputTokens.Reviewer = a.MaxOutputTokens.Coder
	case rcp.CoderIsReviewer:
		a.Models.Coder = a.ModelFor(config.RoleReviewer)
		a.MaxOutputTokens.Coder = a.MaxOutputTokens.Reviewer
	}
}

// runDigestRecipe is the catalog recipe of a report run (digest.go, T3.2). It
// is the one recipe whose stage limits the runner sets rather than the
// repository: a report run is launched on a schedule by a routine, with nobody
// watching what one costs.
const runDigestRecipe = "run-digest"

// runDigestReviewerMaxOutput is the per-call output bound a report run's review
// carries (0.24.1): about $0.64 of reserved output at Opus's $20/M, which is
// what the recipe is after rather than the number itself. A review reserves its
// whole output allowance at the gateway before it spends anything, and a
// review with no bound set reserves the reviewer model's own maximum — 128k on
// claude-opus-5-5, $2.56 plus the input — which is more than a small
// repository's per-run cap holds, so its report run halted with run_cap having
// spent nothing (Check 136's run 20261114-135057-7d42).
const runDigestReviewerMaxOutput int64 = 32000

// applyRecipeOutputLimit tightens the per-call output bound one of the two
// recipes that carry one buys, and reports whether it dropped a larger bound
// the run had asked for. It only ever tightens, so a repository or a project
// that reserves less than the recipe does keeps its own figure. It runs beside
// ApplyRoles and before the policy merge, so the bound is the one the file
// layer carries into it: the merge reads the limits off the configuration as it
// stands, the way it reads the role mapping's aliased value.
//
// The bound is written into the slot the recipe's own stage READS, which is the
// two recipes' difference: a report run's stage is a review, so its bound is
// the reviewer's, while the diagnose stage is a stage whose role is the coder's
// (config.StageRole knows the review stages only) — so the tier's bound belongs
// on the coder's slot, or the reservation the gateway holds for the diagnosis,
// which is read off the same slot, would stay the model's own maximum. A
// diagnosis's coder slot is read by nothing else: the recipe's one step is the
// diagnose step.
//
// The diagnose arm is keyed on the step rather than on the recipe's name, the
// way the digest arm is: the bound is D2407's tier bound, and a repository's
// own recipe holding the step is a diagnosis by the same argument — one small
// question about one failure — wherever it got its name.
func applyRecipeOutputLimit(a *config.Agent, rcp *recipe.Recipe) (capped bool) {
	switch {
	case rcp.Name == runDigestRecipe:
		if n := a.MaxOutputTokens.Reviewer; n > 0 && n <= runDigestReviewerMaxOutput {
			return false
		}
		capped = a.MaxOutputTokens.Reviewer > runDigestReviewerMaxOutput
		a.MaxOutputTokens.Reviewer = runDigestReviewerMaxOutput
		return capped
	case recipe.UsesDiagnose(rcp):
		if n := a.MaxOutputTokens.Coder; n > 0 && n <= recipe.TroubleshootReviewerMaxOutput {
			return false
		}
		capped = a.MaxOutputTokens.Coder > recipe.TroubleshootReviewerMaxOutput
		a.MaxOutputTokens.Coder = recipe.TroubleshootReviewerMaxOutput
		return capped
	}
	return false
}
