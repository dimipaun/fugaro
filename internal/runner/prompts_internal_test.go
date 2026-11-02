package runner

import (
	"strings"
	"testing"
)

// TestBaseInstructionsBlockCapsWhatItPastes: the base branch's instructions
// are read out of the base ref whole and pasted into the prompt, and nothing
// upstream bounds their size — a CLAUDE.md or a rule file of any length is a
// file a base branch can carry. The block keeps as many files as fit in
// maxBaseInstructionBytes, in the order baseInstructions gives them (the
// repository's CLAUDE.md first, its rules after), and names the rest: a
// reviewer given half its instructions with no line saying so cannot tell the
// base is bigger than what it was shown.
//
// Mutation (drop the maxBaseInstructionBytes argument, passing a cap larger
// than the total): this test fails, the over-cap base's rule files being
// pasted in whole and nothing named as left out.
func TestBaseInstructionsBlockCapsWhatItPastes(t *testing.T) {
	// The first file alone leaves less room than the next one needs, so the
	// cap falls between two files rather than inside one.
	first := strings.Repeat("x", maxBaseInstructionBytes-100)
	rule := strings.Repeat("y", 200)
	files := []baseInstruction{
		{Path: "CLAUDE.md", Text: first},
		{Path: ".claude/rules/a.md", Text: rule},
		{Path: ".claude/rules/b.md", Text: "Rule B: keep the diff small."},
	}
	block := baseInstructionsBlock("origin/main", false, files)
	if !strings.Contains(block, first) {
		t.Error("the block dropped the base's CLAUDE.md, which fits inside the cap")
	}
	for _, f := range files[1:] {
		if strings.Contains(block, f.Text) {
			t.Errorf("the block pasted %s, which is over the %s cap", f.Path, fmtBytes(int64(maxBaseInstructionBytes)))
		}
		if !strings.Contains(block, f.Path) {
			t.Errorf("the block does not name %s as left out", f.Path)
		}
	}
	total := 0
	for _, f := range files {
		total += len(f.Text)
	}
	if !strings.Contains(block, fmtBytes(int64(total))) {
		t.Errorf("the block does not say how large the base's instructions are (%s in all):\n%s",
			fmtBytes(int64(total)), block)
	}
	if !strings.Contains(block, "git show origin/main:<path>") {
		t.Error("the block does not say where the reviewer can read what was left out")
	}
}

// TestBaseInstructionsBlockKeepsEverythingUnderTheCap: a base whose
// instructions fit is given to the reviewer whole, with no line about a cap:
// the clipping is for a base that is over it, and a block that mentioned the
// limit under it would be telling the reviewer about a cut that was not made.
//
// Mutation (always write the "left out" line): this test fails, the block
// carrying a sentence naming no file.
func TestBaseInstructionsBlockKeepsEverythingUnderTheCap(t *testing.T) {
	files := []baseInstruction{
		{Path: "CLAUDE.md", Text: "This repository is owner/repo-a."},
		{Path: ".claude/rules/a.md", Text: "Rule A: never rewrite history."},
	}
	block := baseInstructionsBlock("origin/main", false, files)
	for _, f := range files {
		if !strings.Contains(block, f.Text) || !strings.Contains(block, f.Path) {
			t.Errorf("the block dropped %s, which fits inside the cap:\n%s", f.Path, block)
		}
	}
	if strings.Contains(block, "left out") {
		t.Errorf("the block claims it left something out of a base that fits:\n%s", block)
	}
}
