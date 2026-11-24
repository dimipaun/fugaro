package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	cleanupTeam = "version: 1\nname: team\nsteps:\n  - review: {}\n"
	cleanupLoop = "version: 1\nname: loop\nsteps:\n  - review: {}\n"
)

// cleanupLine returns the line about path, and whether there is one.
func cleanupLine(lines []config.Problem, path string) (config.Problem, bool) {
	for _, l := range lines {
		if l.Path == path {
			return l, true
		}
	}
	return config.Problem{}, false
}

// cleanupRemoval is the line offering the whole folder's removal, if any.
func cleanupRemoval(lines []config.Problem) (config.Problem, bool) {
	return cleanupLine(lines, recipe.RepoDir)
}

// TestRecipesCleanupOffersRemovalOnlyWhenEveryCopyIsStored: the folder may go
// only when the store holds every file in it, because git rm -r takes the ones
// it does not with it. A file is stored when a name recipe.LookupOrder gives
// for this repository holds its exact bytes — the repository's own copy first,
// then the flat name — so a file the store holds under either counts, and one
// the store holds under a name outside the order, or under different bytes,
// does not (the sums are what tell the two apart).
func TestRecipesCleanupOffersRemovalOnlyWhenEveryCopyIsStored(t *testing.T) {
	const repo, provider = "acme/other", "github"
	files := map[string]string{".fugaro/recipes/team.yaml": cleanupTeam, ".fugaro/recipes/loop.yaml": cleanupLoop}
	copyTeam, copyLoop := recipe.CopyName("team", provider, repo), recipe.CopyName("loop", provider, repo)
	if copyTeam == "" || copyLoop == "" {
		t.Fatalf("no copy name for %s/%s", provider, repo)
	}
	store := func(sums map[string]string) recipesStore {
		return recipesStore{provider: provider, repo: repo, sums: sums}
	}
	sum := func(text string) string { return recipe.Sum([]byte(text)) }

	// Every file has its copy: one line per file naming it, then the removal.
	// ReadDir's order is the file names', so loop is the first line.
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, files)
	lines := recipesCleanupLines(dir, store(map[string]string{copyTeam: sum(cleanupTeam), copyLoop: sum(cleanupLoop)}))
	if len(lines) != 3 {
		t.Fatalf("lines = %+v, want two files and the removal", lines)
	}
	if l := lines[0]; l.Path != ".fugaro/recipes/loop.yaml" || !strings.Contains(l.Message, "is in the project store as "+copyLoop) {
		t.Errorf("lines[0] = %+v, want loop's line naming "+copyLoop, l)
	}
	if l := lines[1]; l.Path != ".fugaro/recipes/team.yaml" || !strings.Contains(l.Message, "is in the project store as "+copyTeam) {
		t.Errorf("lines[1] = %+v, want team's line naming "+copyTeam, l)
	}
	removal, ok := cleanupRemoval(lines)
	if !ok || len(lines) != 3 {
		t.Fatalf("lines = %+v, want exactly one removal line when every copy is stored", lines)
	}
	if got := removal.String(); !strings.Contains(got, "git rm -r .fugaro/recipes") {
		t.Errorf("removal = %q, want the git rm -r .fugaro/recipes diff", got)
	}

	// One file missing from the store, one held under different bytes, one
	// held under a name the lookup order never gives: no removal at all, and
	// the file named with the publish that has to come first.
	for _, tc := range []struct {
		name string
		sums map[string]string
		miss string
	}{
		{"the copy is absent", map[string]string{copyTeam: sum(cleanupTeam)}, ".fugaro/recipes/loop.yaml"},
		{"the copy differs", map[string]string{copyTeam: sum(cleanupTeam), copyLoop: sum(cleanupTeam)}, ".fugaro/recipes/loop.yaml"},
		{"another repository's copy", map[string]string{copyTeam: sum(cleanupTeam), recipe.CopyName("loop", provider, "acme/elsewhere"): sum(cleanupLoop)}, ".fugaro/recipes/loop.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := recipesCleanupLines(dir, store(tc.sums))
			if _, ok := cleanupRemoval(lines); ok {
				t.Fatalf("lines = %+v, want no removal while a file has no copy", lines)
			}
			l, ok := cleanupLine(lines, tc.miss)
			if !ok || !strings.Contains(l.Message, "fugaro recipes publish "+tc.miss) {
				t.Fatalf("lines = %+v, want %s named with its publish", lines, tc.miss)
			}
		})
	}

	// The flat name is in the lookup order too, so a file the project holds
	// under its own name — no copy — still has its copy in the store.
	lines = recipesCleanupLines(dir, store(map[string]string{"team": sum(cleanupTeam), "loop": sum(cleanupLoop)}))
	if _, ok := cleanupRemoval(lines); !ok {
		t.Fatalf("lines = %+v, want the removal: both files are stored flat", lines)
	}

	// Nothing to clean: no folder, and an empty one.
	if lines := recipesCleanupLines(t.TempDir(), store(nil)); len(lines) != 0 {
		t.Errorf("no folder: lines = %+v, want none", lines)
	}
	empty := t.TempDir()
	testutil.WriteFiles(t, empty, map[string]string{".fugaro/recipes/.keep": ""})
	if lines := recipesCleanupLines(empty, store(nil)); len(lines) != 1 || lines[0].Path != recipe.RepoDir+"/.keep" {
		t.Errorf("a folder with no recipe in it: lines = %+v, want only .keep's", lines)
	}
}

// TestRecipesCleanupNamesPublishFirst: a checkout whose files the store does
// not hold is told what to do first — per file, the publish of that file — and
// is offered no removal, for the whole folder or for a file of it. Everything
// in the folder counts: a file that is not <name>.yaml is a file with no copy
// like any other, and one git rm -r would have taken with the rest.
func TestRecipesCleanupNamesPublishFirst(t *testing.T) {
	const repo, provider = "acme/other", "github"
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		".fugaro/recipes/team.yaml":     cleanupTeam,
		".fugaro/recipes/notes.txt":     "notes\n",
		".fugaro/recipes/Bad_Name.yaml": cleanupLoop,
	})
	lines := recipesCleanupLines(dir, recipesStore{provider: provider, repo: repo})
	if len(lines) != 3 {
		t.Fatalf("lines = %+v, want one per file", lines)
	}
	for _, rel := range []string{".fugaro/recipes/team.yaml", ".fugaro/recipes/notes.txt", ".fugaro/recipes/Bad_Name.yaml"} {
		l, ok := cleanupLine(lines, rel)
		if !ok {
			t.Fatalf("lines = %+v, want a line for %s", lines, rel)
		}
		if !strings.Contains(l.Message, "fugaro recipes publish "+rel) || !strings.Contains(l.Message, "first") {
			t.Errorf("%s: message = %q, want the publish of that file first", rel, l.Message)
		}
	}
	for _, l := range lines {
		if strings.Contains(l.String(), "git rm") {
			t.Errorf("line %q offers a removal while a file has no copy", l.String())
		}
	}
}

// TestRecipesCleanupValidatePrintsTheLines: validate prints them for the
// checkout it is in, with the project store read through the selected config's
// own client (the road governanceEditions takes), and --offline reads no
// bucket, so it prints none: the flag promises the command reads neither the
// runs bucket nor the published policy, and an offer made without looking is
// not one.
func TestRecipesCleanupValidatePrintsTheLines(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	dir := configSkewCheckout(t, f, configSkewPins(configSkewHead)+configSkewWorkflow)
	testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/team.yaml": cleanupTeam})
	copyName := recipe.CopyName("team", "github", "acme/other")
	projectRecipesFixture(t, f, map[string]string{copyName: cleanupTeam})

	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file whose recipe folder is stored is valid: %v", err)
	}
	for _, want := range []string{"is in the project store as " + copyName, "git rm -r .fugaro/recipes"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
	if _, offErr, err := execute(t, "validate", "--offline"); err != nil || strings.Contains(offErr, "git rm") {
		t.Errorf("--offline: %v, stderr %q, want the lines left unread", err, offErr)
	}

	// The store without the copy: the publish comes first, and no removal.
	if err := os.Remove(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey(copyName)))); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = execute(t, "validate")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(errOut, "fugaro recipes publish .fugaro/recipes/team.yaml") || strings.Contains(errOut, "git rm") {
		t.Errorf("stderr %q, want the publish first and no removal", errOut)
	}
}
