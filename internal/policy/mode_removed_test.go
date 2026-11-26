package policy_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// removedNames are the identifiers this release deleted — policy's constant for
// the word that was a budget mode, and the aliases config and localcfg gave it
// (I1, D3009). They are spelled in halves so that this file is itself clean
// when it scans the tree, which is the same reason mode.go compares against the
// literal instead of through the constant (docs/always-a-budget/contract-mode.md
// §3): what is left of the word is Mode.Removed and ModeOffRefusal, and nothing
// else in Go may name it.
var removedNames = []string{"Mode" + "Off", "Budget" + "Off"}

// TestNoCodeReadsTheRemovedMode is I1's own claim, the runnable form of the
// task's `! git grep -n -w -E` sweep: the tree's Go spells neither identifier
// any more. The build is the first half of it — a reader left behind does not
// compile — and this is the other: a name that survived in a comment or a
// string no compiler reads is still a name a later reader would copy, and the
// release's whole point is that a mode is observe or enforce and nothing else.
//
// The mutation: putting either constant back, in any file, fails here as well
// as at the build.
func TestNoCodeReadsTheRemovedMode(t *testing.T) {
	// The vocabulary still answers for the word, and it does it in the one place
	// the sweep leaves standing: without this assertion the test would pass over
	// a tree whose Removed had been deleted too, and every reader that meets the
	// word in an old config or record would read it as an ordinary unknown
	// string.
	if !policy.Mode("off").Removed() || !strings.Contains(policy.ModeOffRefusal, "off") {
		t.Fatalf("the word is not answered for any more: Removed() = %v, ModeOffRefusal = %q", policy.Mode("off").Removed(), policy.ModeOffRefusal)
	}
	root := moduleRoot(t)
	patterns := make([]*regexp.Regexp, 0, len(removedNames))
	for _, name := range removedNames {
		patterns = append(patterns, regexp.MustCompile(`\b`+name+`\b`))
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, re := range patterns {
				if re.MatchString(line) {
					found = append(found, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(found) > 0 {
		t.Errorf("the deleted identifier is still spelled in %d place(s):\n%s", len(found), strings.Join(found, "\n"))
	}
}

// moduleRoot is the checkout this test runs in: the directory `go test` compiled
// this file from, walked up to the one that holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("the test's own path could not be read, so the tree it scans is unknown")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}
