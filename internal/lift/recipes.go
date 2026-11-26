package lift

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanRecipes is B16's migration item (PR4): the .fugaro/recipes/*.yaml files
// of a repository's default branch, published into the project's flat recipe
// store (D3295), and each file a name already answers with other text renamed
// to its repository's own copy (D3301).
//
// The store is one namespace keyed by recipe name (recipe.ObjectKey) and it
// outranks the catalog for every repository of the project, so publishing a
// repository's file flat under its own name hands that text to every other
// repository that resolves the name. That is what the clash rule keeps from
// happening: a name nothing else answers is published flat; a name another
// repository's file, an existing project recipe or a catalog recipe answers
// with different text is published as recipe.CopyName, with the file's own
// name: line rewritten and nothing else (R14), so the repository's runs
// resolve the same text as before through recipe.LookupOrder (D3303).
//
// It acts on a repository's first migration only (D3305): a repository whose
// record already exists publishes nothing, and the recipe files its default
// branch still holds are named for a person to delete. Every repository it
// judged is recorded, so a file that appears after the migration is named,
// never published.
func PlanRecipes(p Project, repos []Repo) (Change, error) {
	var ch Change
	byName := map[string][]*recipeCandidate{}
	for _, r := range repos {
		// A repository update could not read is named by update itself, and no
		// planner acts on it: its values are dropped, never moved (D3306).
		if r.Unreadable != "" {
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s/ is left: %v", r.Repo, recipe.RepoDir, err))
			continue
		}
		if p.Migrated[slug] {
			for _, f := range r.Recipes {
				if recipeYAML(f.Path) {
					ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s is left: %s is already migrated, and its runs read the project's copy", r.Repo, fileAt(f.Path), r.Repo))
				}
			}
			continue
		}
		if !slices.Contains(ch.Records, slug) {
			ch.Records = append(ch.Records, slug)
		}
		for _, f := range r.Recipes {
			c, line := judgeRecipe(r, f)
			switch {
			case line != "":
				ch.Lines = append(ch.Lines, line)
			case c != nil:
				byName[c.name] = append(byName[c.name], c)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		ch = planRecipeName(ch, p, name, byName[name])
	}
	return ch, nil
}

// recipeCandidate is one file of a repository's .fugaro/recipes/ that parses
// and may be published: where it came from, its bytes, and the recipe name the
// file and the recipe agree on.
type recipeCandidate struct {
	repo string
	prov string
	path string
	data []byte
	name string
}

// dataGroup is every candidate whose file holds the same bytes: one recipe
// text, which one store object answers however many repositories keep it.
type dataGroup struct {
	data  []byte
	cands []*recipeCandidate
}

// judgeRecipe judges one file of a repository's .fugaro/recipes/: the
// candidate it may be published as, or the one line saying why it is left
// alone. The runner reads a repository recipe by its file name, so a file the
// name does not agree with is one no run of that repository uses today, and
// publishing it under the recipe's own name would give every other repository
// a recipe nobody had. A copy name (recipe.IsCopyName) is not one a repository
// keeps: a copy is the project's own object, and a repository's file published
// under one would sit where another repository's copy is looked up.
func judgeRecipe(r Repo, f RecipeFile) (*recipeCandidate, string) {
	if !recipeYAML(f.Path) {
		return nil, ""
	}
	name := strings.TrimSuffix(f.Path, ".yaml")
	if !recipe.NameRE.MatchString(name) {
		return nil, fmt.Sprintf("%s: %s is left: %q is not a recipe name", r.Repo, fileAt(f.Path), safetext.OneLine(name))
	}
	rcp, ps := recipe.Parse(f.Data)
	switch {
	case len(ps) > 0:
		return nil, fmt.Sprintf("%s: %s is left: %s", r.Repo, fileAt(f.Path), safetext.OneLine(recipe.ProblemsText(ps)))
	case rcp.Name != name:
		return nil, fmt.Sprintf("%s: %s is left: the file is %s but the recipe names itself %q; the file name and the name must agree", r.Repo, fileAt(f.Path), safetext.OneLine(f.Path), safetext.OneLine(rcp.Name))
	}
	return &recipeCandidate{repo: r.Repo, prov: r.Provider, path: f.Path, data: f.Data, name: name}, ""
}

// planRecipeName publishes every candidate of one recipe name, or renames it.
// What answers the name decides which, in the order a run resolves it in
// (recipe.LookupOrder): the project's store first, then the catalog, then
// another repository's file of the same name. An answer that already holds this
// text means there is nothing to publish at all.
func planRecipeName(ch Change, p Project, name string, cs []*recipeCandidate) Change {
	storeSum, inStore := p.Recipes[name]
	catText, inCatalog := recipe.CatalogText(name)
	groups := dataGroups(cs)
	var clashing []*recipeCandidate
	for _, g := range groups {
		sum := recipe.Sum(g.data)
		answeredSame := (inStore && storeSum == sum) || (!inStore && inCatalog && bytes.Equal(catText, g.data))
		answeredOther := (inStore && storeSum != sum) || (!inStore && inCatalog && !bytes.Equal(catText, g.data))
		switch {
		case answeredSame:
			// The name already resolves to this text: a second update writes
			// nothing, and the runs that read it are where they were.
		case answeredOther || len(groups) > 1:
			clashing = append(clashing, g.cands...)
		default:
			ch.StoreWrites = append(ch.StoreWrites, StoreWrite{Key: recipe.ObjectKey(name), Data: g.data})
			for _, c := range g.cands {
				ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s is published as the project recipe %s", c.repo, fileAt(c.path), name))
			}
		}
	}
	return planCopies(ch, p, name, clashing)
}

// planCopies renames the files a clash left, one copy per repository (D3301,
// R14). A copy name that is already taken — by the store, or by another file
// whose copy would take it — is published by neither: every repository that
// wanted it is named instead, so the migration never overwrites a copy that is
// not this repository's.
func planCopies(ch Change, p Project, name string, clashing []*recipeCandidate) Change {
	if len(clashing) == 0 {
		return ch
	}
	byCopy := map[string][]*recipeCandidate{}
	for _, c := range clashing {
		copyName := recipe.CopyName(name, c.prov, c.repo)
		if copyName == "" {
			ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s is left: its copy name cannot be derived from %s", c.repo, fileAt(c.path), c.repo))
			continue
		}
		byCopy[copyName] = append(byCopy[copyName], c)
	}
	for _, copyName := range slices.Sorted(maps.Keys(byCopy)) {
		ch = planCopy(ch, p, copyName, byCopy[copyName])
	}
	return ch
}

// planCopy publishes the files whose copies would take one name, or names them
// and writes nothing.
func planCopy(ch Change, p Project, copyName string, cs []*recipeCandidate) Change {
	data := cs[0].data
	for _, c := range cs[1:] {
		if !bytes.Equal(c.data, data) {
			return leftLines(ch, cs, fmt.Sprintf("the copy name %s is claimed by another repository's file", copyName))
		}
	}
	out, err := renameRecipe(data, copyName)
	if err != nil {
		return leftLines(ch, cs, fmt.Sprintf("its name: line could not be rewritten: %v", err))
	}
	// The store holds a copy as it is published — the repository's file with
	// its name: line rewritten — so an update that wrote its copies and stopped
	// before its records meets its own work here rather than the repository's
	// file, and the sum it is compared against is the rewritten text's. Summing
	// the file instead would read every rerun's own copy as another's and leave
	// it, naming a name the migration itself took.
	if sum, inStore := p.Recipes[copyName]; inStore {
		if sum == recipe.Sum(out) {
			return ch
		}
		return leftLines(ch, cs, fmt.Sprintf("the copy name %s is already taken", copyName))
	}
	if len(out) > recipe.MaxBytes {
		// The rewritten copy is what a run parses, and one over the limit is
		// refused by the same parser that read the file: it is named, not
		// published, so a repository's runs keep the file they have.
		return leftLines(ch, cs, fmt.Sprintf("its copy is %d bytes, over the %d byte limit", len(out), recipe.MaxBytes))
	}
	ch.StoreWrites = append(ch.StoreWrites, StoreWrite{Key: recipe.ObjectKey(copyName), Data: out})
	for _, c := range cs {
		ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s is published as %s, its name: line rewritten and nothing else", c.repo, fileAt(c.path), copyName))
	}
	return ch
}

func leftLines(ch Change, cs []*recipeCandidate, reason string) Change {
	for _, c := range cs {
		ch.Lines = append(ch.Lines, fmt.Sprintf("%s: %s is left: %s", c.repo, fileAt(c.path), reason))
	}
	return ch
}

func dataGroups(cs []*recipeCandidate) []dataGroup {
	var out []dataGroup
	for _, c := range cs {
		at := -1
		for i := range out {
			if bytes.Equal(out[i].data, c.data) {
				at = i
				break
			}
		}
		if at < 0 {
			out = append(out, dataGroup{data: c.data, cands: []*recipeCandidate{c}})
			continue
		}
		out[at].cands = append(out[at].cands, c)
	}
	return out
}

// recipeYAML reports whether path is a recipe file of the migration's own
// scope: a *.yaml file directly in .fugaro/recipes/ (D3295). The folder's
// other files are the cleanup diff's (PR5), never this item's.
func recipeYAML(path string) bool {
	return !strings.Contains(path, "/") && strings.HasSuffix(path, ".yaml")
}

func fileAt(path string) string { return safetext.OneLine(recipe.RepoDir + "/" + path) }

// renameRecipe is recipe.Rename: the R14 rewrite the migration publishes a
// copy with, kept here under the name this package's planners and tests read
// it by.
func renameRecipe(data []byte, name string) ([]byte, error) {
	return recipe.Rename(data, name)
}

var _ Planner = PlanRecipes
