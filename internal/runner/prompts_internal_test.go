package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestReviewPromptWithFocusEmptyChangesNothing: a level that names no focus is
// handed the review brief byte for byte. reviewPromptWithFocus is the one place
// a level's focus enters an ordinary review prompt, and an empty focus must add
// nothing — not a heading, not a blank block — or every run with no focus would
// carry a section the reviewer was never asked about.
//
// Mutation (return reviewPrompt + "\n\n" when the focus is empty): this test
// fails, the byte-identical cases gaining a trailing blank pair.
func TestReviewPromptWithFocusEmptyChangesNothing(t *testing.T) {
	const brief = "You are reviewing a pull request.\n\nReview the changes on this branch (see `git diff origin/main...HEAD`). Do not modify any files."
	for _, focus := range []string{"", "  ", "\n", "\t\n "} {
		if got := reviewPromptWithFocus(brief, focus); got != brief {
			t.Errorf("reviewPromptWithFocus(brief, %q) = %q, want the brief byte for byte", focus, got)
		}
	}
}

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

// TestSystemPromptIsUnchangedWithAnImageRate: the image rule follows the price
// row of the stage's own model and the gateway that refuses the image, and
// every run it is not for gets the brief every run got before it existed
// (D2016). A coder that HAS an image rate sends the image itself; a coder
// priced without one that the gateway carries is the run the rule is for.
//
// A coder whose calls reach no gateway is neither refused nor denied — an
// oauth run and a run with the budget off write no managed settings file at
// all (writeManagedSettings' first rule) — so its brief must not claim an
// enforcement that is not there, nor forbid a read it can make. The model here
// is one the run's price table has no row for, which the gateway does serve,
// priced at the table's maximum, and refuses an image for (forward.go's
// modelFor): the price row alone is not the question.
//
// Mutation (denyBinaryReads' price-row answer without its gatewayOn): the two
// gateway-off cases gain the rule and their byte-for-byte assertion fails.
func TestSystemPromptIsUnchangedWithAnImageRate(t *testing.T) {
	mk := func(model, auth, mode string) (PromptData, string) {
		r := &run{
			d:     Deps{StateDir: "/s"},
			rec:   &runstore.Record{Branch: "fugaro/x"},
			cfg:   &config.Config{},
			spec:  &task.Spec{},
			spend: Spend{Mode: mode},
		}
		r.cfg.Git.BaseBranch = "main"
		r.cfg.Agent.Auth = auth
		r.cfg.Agent.Models.Coder = model
		pd := r.systemPromptData()
		return pd, SystemPrompt(pd, "")
	}
	for _, c := range []struct {
		name  string
		model string
		auth  string
		mode  string
		want  bool
	}{
		{"a coder with an image rate", "claude-sonnet-5-5", "api-key", "enforce", false},
		{"a coder without one", "deepseek/deepseek-v4-flash", "api-key", "enforce", true},
		{"a coder without one on oauth, where the gateway is off", "deepseek/deepseek-v4-flash", "oauth", "enforce", false},
		{"a coder without one with the budget off", "deepseek/deepseek-v4-flash", "api-key", "off", false},
	} {
		pd, got := mk(c.model, c.auth, c.mode)
		if pd.NoImageReads != c.want {
			t.Errorf("%s: NoImageReads = %v, want %v", c.name, pd.NoImageReads, c.want)
		}
		if c.want {
			continue
		}
		base := SystemPrompt(PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}, "")
		if got != base {
			t.Errorf("%s: the brief is not the one every run had before the image rule:\n%s", c.name, got)
		}
	}
}
