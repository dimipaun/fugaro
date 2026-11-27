package cli

import (
	"strings"
	"testing"
)

// docs_update_anywhere_test.go is U7a of docs/update-from-anywhere: the two
// documents that describe what `fugaro update` does with a value a release
// before this one left on a repository's default branch.
//
// The pin clause. CLAUDE.md's HARD RULE used to promise that a leftover plugin
// pin "is offered its removal as a diff, which the user commits or does not",
// and the update skill described the same diff. The diff was a write to
// whatever working tree the run stood in — the directory dependence this whole
// plan removes — so the value is a notice now: `doctor` and `update` name the
// pin and the line to delete, and the repository's owner makes the edit
// (D3340, R11; repo_notices.go is the text both commands print).
//
// The `--check` line. `update --check` takes no PATH and reads this project's
// repositories through the installation's credentials service, its only calls
// being that service's read mint and the code host's API (D3349 as amended,
// R17): the skill's old "makes no cloud call and needs no credentials" is
// false of the mode it describes.
func TestDocsUpdateAnywherePinClause(t *testing.T) {
	// The clause both documents carry, verbatim: the two commands that name a
	// leftover pin, and the line to delete. It is one string so the skill and
	// the rule cannot drift into two readings of the same value.
	const clause = "named by `doctor` and `update`, with the line to delete"
	for _, path := range []string{"CLAUDE.md", "plugin/skills/update/SKILL.md"} {
		doc := readRepoFile(t, path)
		if !strings.Contains(doc, clause) {
			t.Errorf("%s does not carry the pin clause %q: a leftover pin is a notice naming the line to delete now (D3340, R11)", path, clause)
		}
		if strings.Contains(doc, "offered its removal as a diff") {
			t.Errorf("%s still offers a leftover pin's removal as a diff: nothing writes a working tree any more (D3340)", path)
		}
	}

	// README.md:61 follows the same change: the sentence it carried was the
	// HARD RULE's, in the reader's own words.
	readme := readRepoFile(t, "README.md")
	if strings.Contains(readme, "offered its removal as a diff") {
		t.Errorf("README.md still offers a leftover pin's removal as a diff: it is named with the line to delete (D3340)")
	}
	if !strings.Contains(readme, "the line to delete") {
		t.Errorf("README.md does not say a leftover pin is named with the line to delete (D3340)")
	}

	// The update skill's own `--check` line: the mode reads each repository
	// through the installation's credentials service (R17), so the sentence
	// that said it needs no credentials is gone.
	skill := readRepoFile(t, "plugin/skills/update/SKILL.md")
	if !strings.Contains(skill, "reads each repository through the installation's credentials service") {
		t.Errorf("the update skill does not say --check reads each repository through the installation's credentials service (R17)")
	}
	if strings.Contains(skill, "needs no credentials") {
		t.Errorf("the update skill still says --check needs no credentials: it reads each repository through the credentials service (R17)")
	}
}
