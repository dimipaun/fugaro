package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commentFragmentPairRE reads one replacement out of a comment-only fragment:
// the `**before**:` line (which may carry a parenthetical, as the website
// fragment's first one does), the fenced block under it, then `**after**:` and
// its fenced block. The blocks are compared as exact text, because that is how
// they are applied -- by hand, into a file no agent run may edit.
var commentFragmentPairRE = regexp.MustCompile(
	"(?s)\\*\\*before\\*\\*[^\\n]*:\\n\\n```\\n(.*?)```\\n\\n\\*\\*after\\*\\*:\\n\\n```\\n(.*?)```")

// TestCommentFragmentBlocksMatchTheirWorkflowFile: the three comment-only
// fragments under docs/pending-workflows/ are exact-text instructions. Each
// quotes a block of an applied workflow file and the block that replaces it, and
// a person splices them by hand, because no agent run may edit
// `.github/workflows/` -- GitHub refuses the push. Nothing else reads those
// blocks: the fragments are `.md` precisely so the two enumerators that walk
// that directory for `.ya?ml` leave them alone. So a later edit to the workflow
// file would leave a fragment quoting a block that is no longer there, and the
// person applying it would find out by hand, mid-edit, holding a diff that is
// half applied -- the one failure mode this file class has.
//
// This test is the pin. Every `before` block has to occur **exactly once** in its
// target file: not zero times (the file moved out from under the fragment) and
// not twice (the block is ambiguous, so a hand edit picks one of two places and
// silently leaves the other). And no `after` block may already be in the file:
// a fragment whose change has been applied is a stale instruction, and the commit
// that applies one deletes it -- which is what the fragment's own "How to apply"
// says, and which no other test can see, because a `.md` file here is invisible
// to every enumerator by design.
//
// The fragments are discovered rather than listed, because applying one deletes
// it: a list in this file would be the dangling reference the docs tests exist to
// refuse, and this test has to stay green through the copy-in that removes a
// fragment. When every fragment has been applied the glob is empty, this test
// asserts nothing, and that is its intended end state.
func TestCommentFragmentBlocksMatchTheirWorkflowFile(t *testing.T) {
	fragments, err := filepath.Glob("../../docs/pending-workflows/*-comments.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		// A fragment is named for the workflow it corrects: `<workflow>.yml`
		// with `-comments.md` where the extension was.
		name := strings.TrimSuffix(filepath.Base(fragment), "-comments.md")
		target := filepath.Join("../../.github/workflows", name+".yml")
		body, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		workflow, err := os.ReadFile(target)
		if err != nil {
			t.Errorf("%s corrects %s, which does not exist: a fragment is named for the workflow file it splices into, so either the file was renamed or the fragment is named for a workflow this repository does not have", fragment, target)
			continue
		}
		pairs := commentFragmentPairRE.FindAllStringSubmatch(string(body), -1)
		if len(pairs) == 0 {
			t.Errorf("%s quotes no `**before**:`/`**after**:` pair in the shape this test reads: the fragment is the instruction a person applies by hand, so a replacement that is not written as a fenced `before` block followed by a fenced `after` block is one nobody can apply, and one this test cannot hold to %s", fragment, target)
			continue
		}
		for i, pair := range pairs {
			before, after := pair[1], pair[2]
			switch n := strings.Count(string(workflow), before); {
			case n == 0:
				t.Errorf("%s replacement %d: its `before` block is not in %s any more, so the person applying it finds the block by hand and the edit is already half applied -- %s has to be updated in the same commit as whatever changed %s, or applied and deleted", fragment, i+1, target, fragment, target)
			case n > 1:
				t.Errorf("%s replacement %d: its `before` block occurs %d times in %s, so which one it replaces is a guess, and applying it leaves the other behind", fragment, i+1, n, target)
			}
			if strings.Contains(string(workflow), after) {
				t.Errorf("%s replacement %d: its `after` block is already in %s, so the change has been applied and the fragment is a stale instruction: delete %s in the commit that applies it, which is what its own \"How to apply\" says", fragment, i+1, target, fragment)
			}
		}
	}
}
