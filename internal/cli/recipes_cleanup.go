package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// recipesStore is the project recipe store as the cleanup diff reads it: which
// repository's copies are looked up (its provider and repository, which
// recipe.LookupOrder spells the copy's name from) and the sha256 the store
// holds under each name the order can give. A name the store does not hold is
// absent, never an empty sum, so a stored file whose text is empty is told
// apart from one that is not there.
type recipesStore struct {
	provider string
	repo     string
	sums     map[string]string
}

// recipesCleanupLines is the diff that deletes a checkout's .fugaro/recipes/,
// the offer validate makes (and doctor and the launch make, L5's move) once the
// folder is no longer a recipe's home: a run resolves the project store now
// (spec §5.5), and the files in the repository are the leftovers (D3296).
//
// A file whose text the store already holds is one line naming the name it is
// stored under, and the removal is offered — one line for the whole folder, and
// only when EVERY file in it is stored, because git rm -r takes the ones that
// are not with the ones that are. A file with no copy gets fugaro recipes
// publish <file> first instead, and no removal is offered at all: the file is
// the only place that text exists, so deleting the folder would lose it.
//
// Nothing is deleted and nothing is asked — the lines are the diff a person
// runs, the same offer the plugin pin removal makes. root is the checkout and
// store is the project store as a caller already read it, so this reaches
// nothing itself: a caller with no store in reach (--offline, no bucket) prints
// nothing rather than a guess.
func recipesCleanupLines(root string, store recipesStore) []config.Problem {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(recipe.RepoDir)))
	if err != nil {
		return nil
	}
	var lines []config.Problem
	stored := 0
	for _, e := range entries {
		rel := recipe.RepoDir + "/" + e.Name()
		// Everything in the folder is a file this line is about, including the
		// ones the runner never read: they are in the diff git rm -r applies,
		// so a folder that still holds one cannot go, and the publish named for
		// it is the command that says why it cannot be stored. A file the store
		// cannot hold (a stray name, a .txt, a directory) is left to that
		// refusal rather than being guessed at here.
		name, isYAML := strings.CutSuffix(e.Name(), ".yaml")
		sum := ""
		if isYAML {
			if data, found, rerr := recipe.ReadRepoFile(root, name); rerr == nil && found {
				sum = recipe.Sum(data)
			}
		}
		if n, ok := store.storedAs(name, sum); ok {
			lines = append(lines, config.Problem{Path: rel, Message: "is in the project store as " + n})
			stored++
			continue
		}
		lines = append(lines, config.Problem{Path: rel, Message: "has no copy in the project store: run `fugaro recipes publish " + rel + "` first"})
	}
	if len(entries) > 0 && stored == len(entries) {
		lines = append(lines, config.Problem{Path: recipe.RepoDir, Message: "every file here is in the project store: remove the folder with `git rm -r " + recipe.RepoDir + "`"})
	}
	return lines
}

// storedAs is the name of the lookup order whose store text has sum, if one
// has it (D3303): the repository's own copy first, then the flat name, which
// is where a name nothing else answers resolves. Both count as stored — a
// project recipe every repository reads is a copy of this file too, and the
// flat name is in this repository's order either way — and the sums are what
// keep two recipes of one name apart (a copy holding other text is not this
// file).
func (s recipesStore) storedAs(name, sum string) (string, bool) {
	if name == "" || sum == "" {
		return "", false
	}
	for _, n := range recipe.LookupOrder(name, s.provider, s.repo) {
		if got, ok := s.sums[n]; ok && got == sum {
			return n, true
		}
	}
	return "", false
}

// recipesStoreOf reads the project store for the checkout at root: for every
// <name>.yaml in .fugaro/recipes/, the names recipe.LookupOrder can resolve it
// to for this repository, and the sha256 the store holds under each.
//
// It reads only those keys — two at most per file — rather than listing the
// namespace, which grows a copy per repository: a folder of three files costs a
// handful of reads, not the whole store. A key that is not there, or cannot be
// read, is simply absent, and the cleanup diff then says the file has no copy:
// the safe way round, since a removal offered without the text in hand is the
// one mistake that loses a recipe.
func recipesStoreOf(ctx context.Context, env *cloudEnv, provider, repo, root string) recipesStore {
	store := recipesStore{provider: provider, repo: repo, sums: map[string]string{}}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(recipe.RepoDir)))
	if err != nil {
		return store
	}
	names := map[string]bool{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok {
			continue
		}
		for _, n := range recipe.LookupOrder(name, provider, repo) {
			if recipe.NameRE.MatchString(n) || recipe.IsCopyName(n) {
				names[n] = true
			}
		}
	}
	for n := range names {
		data, _, err := env.bucket.ReadMaxStrict(ctx, recipe.ObjectKey(n), recipe.MaxBytes)
		if err != nil {
			continue
		}
		store.sums[n] = recipe.Sum(data)
	}
	return store
}
