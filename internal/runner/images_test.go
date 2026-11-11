package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// scanRepo clones a repository whose main branch holds files and returns the
// checkout, so the scan reads a real tree.
func scanRepo(t *testing.T, files map[string]string) *gitops.Repo {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, files)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestImageScanIsBounded (V5): the repository scan is bounded twice over. Its
// one git call names the image kinds as pathspecs, so git reports the matches
// and the tree's other files are never listed at all — a checkout of a
// thousand files costs one line per picture — and the list it hands back stops
// at imageScanLimit, so a repository that tracks thousands of pictures is not
// printed into the job log either.
//
// Mutation (list the tree whole and filter it here, `ls-tree -r --name-only`):
// the pathspec assertion fails, and every file of the checkout is read.
// Mutation (drop the imageScanLimit break): the cap case fails.
func TestImageScanIsBounded(t *testing.T) {
	t.Run("a checkout of many files", func(t *testing.T) {
		files := map[string]string{"docs/shot.png": "x\n", "mobile/assets/icon.png": "x\n"}
		for i := range 1000 {
			files[fmt.Sprintf("src/f%04d.txt", i)] = "x\n"
		}
		calls := RecordSessionGit(t)
		got, ok := imageFilesInTree(context.Background(), scanRepo(t, files))
		if !ok {
			t.Fatal("the tree could not be listed")
		}
		if want := []string{"docs/shot.png", "mobile/assets/icon.png"}; !slices.Equal(got, want) {
			t.Errorf("files = %v, want %v: the scan reports the image kinds, not the checkout", got, want)
		}
		args := calls()
		if len(args) != 1 {
			t.Fatalf("the scan ran %d git calls: %v", len(args), args)
		}
		if args[0][0] != "ls-files" {
			t.Errorf("the scan runs %v, want ls-files", args[0])
		}
		var pats []string
		for i, a := range args[0] {
			if a == "--" {
				pats = args[0][i+1:]
				break
			}
		}
		want := make([]string, 0, len(agent.ImageReadExtensions()))
		for _, ext := range agent.ImageReadExtensions() {
			want = append(want, "*."+ext)
		}
		if !slices.Equal(pats, want) {
			t.Errorf("the pathspecs are %v, want %v: they are what keeps the checkout from being listed whole", pats, want)
		}
	})
	t.Run("more pictures than the scan reports", func(t *testing.T) {
		files := map[string]string{}
		for i := range imageScanLimit + 3 {
			files[fmt.Sprintf("assets/p%02d.png", i)] = "x\n"
		}
		got, ok := imageFilesInTree(context.Background(), scanRepo(t, files))
		if !ok {
			t.Fatal("the tree could not be listed")
		}
		if len(got) != imageScanLimit {
			t.Errorf("files = %d, want %d: %v", len(got), imageScanLimit, got)
		}
	})
}

// TestImageNamesInTaskReadsNamesAndKindWords (V5, D2005): the task-text half
// of the check reads a name — a file whose last extension is one of the kinds
// agent.ImageReadExtensions holds, and the kind's own word — and it is
// deliberately willing to be wrong in one direction: a task that only mentions
// a PNG in prose is refused, because the check cannot tell the mention from a
// read, and a task refused for a mention costs one config line while a picture
// read that nobody foresaw costs the stage (D2005, D2006). A word that merely
// contains a kind's letters ("icon" holds "ico") is not a name, and neither is
// a file of another kind.
func TestImageNamesInTaskReadsNamesAndKindWords(t *testing.T) {
	for _, c := range []struct {
		why  string
		text string
		want []string
	}{
		{"a path", "Fix the console golden: docs/shot.png is out of date", []string{"docs/shot.png"}},
		{"a bare kind word", "The palette in the PNG is wrong", []string{"PNG"}},
		{"another case", "Update SHOT.JPEG before the release", []string{"SHOT.JPEG"}},
		{"a quoted name", `Re-render "docs/golden/grid.webp", please`, []string{"docs/golden/grid.webp"}},
		{"a name twice", "docs/a.png and docs/a.png again", []string{"docs/a.png"}},
		{"another kind of file", "Update docs/notes.md and the README", nil},
		{"a word holding a kind", "Refactor the palette handling and the icon font", nil},
		{"no file at all", "Add a feature", nil},
	} {
		t.Run(c.why, func(t *testing.T) {
			if got := imageNamesInTask(c.text); !slices.Equal(got, c.want) {
				t.Errorf("imageNamesInTask(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
