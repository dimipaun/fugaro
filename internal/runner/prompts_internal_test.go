package runner

import (
	"fmt"
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
// oauth run, the one credential the gateway does not carry (D3026, R12), and
// no mode takes a gateway away from the others any more (AB1b, D3009) — so its
// brief must not claim an enforcement that is not there, nor forbid a read it
// can make. The model here is one the run's price table has no row for, which
// the gateway does serve, priced at the table's maximum, and refuses an image
// for (forward.go's modelFor): the price row alone is not the question.
//
// Mutation (denyBinaryReads' price-row answer without its gatewayOn): the
// gateway-less oauth case gains the rule and its byte-for-byte assertion fails.
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
		return pd, SystemPrompt(pd, "", "")
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
		{"a coder without one on oauth, where there is no gateway", "deepseek/deepseek-v4-flash", "oauth", "enforce", false},
		{"a coder without one when the job names no mode", "deepseek/deepseek-v4-flash", "api-key", "", true},
	} {
		pd, got := mk(c.model, c.auth, c.mode)
		if pd.NoImageReads != c.want {
			t.Errorf("%s: NoImageReads = %v, want %v", c.name, pd.NoImageReads, c.want)
		}
		if c.want {
			continue
		}
		base := SystemPrompt(PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}, "", "")
		if got != base {
			t.Errorf("%s: the brief is not the one every run had before the image rule:\n%s", c.name, got)
		}
	}
}

// TestCheckpointPromptCarriesTheRunsOwnWork: the checkpoint call is a fresh
// session with no tools and no checkout of its own, so what the run had done
// reaches it only through the prompt — and the three fields it answers land in
// the record and the draft pull request a person reads to decide whether the run
// is worth continuing (T7.3, D329). The commits the run made and the paths it
// changed are therefore in the prompt, and the two ways a list can be empty are
// not the same: a list the runner read and found empty is answered "none", which
// is a fact the answer may rest on, and a list it could not read is left out
// rather than reported as a run that had done nothing.
//
// Mutation (drop the checkpointWorkSection call): the facts assertion fails.
// Mutation (render a nil list as "none"): the unread case gains the word and its
// assertion fails.
func TestCheckpointPromptCarriesTheRunsOwnWork(t *testing.T) {
	h := runstore.Halt{Reason: runstore.HaltStageBudget, Detail: "implement stage budget"}
	withWork := checkpointPromptFor("Add a feature", "implement", h,
		[]string{"Add the feature", "Fix the tests"}, []string{"feature.txt", "runner.go"})
	for _, want := range []string{
		"Commits it made, newest first:", "- Add the feature\n- Fix the tests\n",
		"Paths it changed:", "- feature.txt\n- runner.go\n",
	} {
		if !strings.Contains(withWork, want) {
			t.Errorf("the checkpoint prompt does not carry %q:\n%s", want, withWork)
		}
	}
	none := checkpointPromptFor("Add a feature", "implement", h, []string{}, []string{})
	for _, want := range []string{"Commits it made, newest first: none.", "Paths it changed: none."} {
		if !strings.Contains(none, want) {
			t.Errorf("a run with no work of its own is not answered %q:\n%s", want, none)
		}
	}
	unread := checkpointPromptFor("Add a feature", "implement", h, nil, nil)
	if strings.Contains(unread, "What this run itself had done") || strings.Contains(unread, "none.") {
		t.Errorf("a read that could not be made is reported as a run that had done nothing:\n%s", unread)
	}
}

// TestCheckpointPromptBoundsTheRunsOwnWork: a run that made a hundred commits or
// changed a thousand paths may not spend the checkpoint call's input bound on
// the list of them, so both lists are capped and each item is clipped; what did
// not fit is named, since a list that stopped silently would read as the whole
// of the run's work. Each item is also folded onto one line: a path may hold a
// newline, and a line of its own would let the branch's own content pass for a
// section of this prompt.
//
// Mutation (drop the cap loop, or the trailing count): the count assertion
// fails. Mutation (render the path whole, or through safetext.Text rather than
// OneLine): the clip and the one-line assertion fail.
func TestCheckpointPromptBoundsTheRunsOwnWork(t *testing.T) {
	h := runstore.Halt{Reason: runstore.HaltStageBudget}
	commits := make([]string, checkpointCommitsShown+3)
	for i := range commits {
		commits[i] = fmt.Sprintf("commit %d", i)
	}
	long := strings.Repeat("x", checkpointItemRunes+50)
	got := checkpointPromptFor("Add a feature", "implement", h, commits, []string{"a\nb.txt", long})
	if n := strings.Count(got, "\n- commit "); n != checkpointCommitsShown {
		t.Errorf("the prompt carries %d commits, want the bound %d", n, checkpointCommitsShown)
	}
	if !strings.Contains(got, "3 commits not shown.") {
		t.Errorf("the prompt does not name the commits it left out:\n%s", got)
	}
	if !strings.Contains(got, "- a?b.txt\n") {
		t.Errorf("a path holding a newline is not folded onto one line (safetext.OneLine):\n%s", got)
	}
	if strings.Contains(got, long) {
		t.Errorf("an item longer than %d runes was not clipped:\n%s", checkpointItemRunes, got)
	}
}

// TestBinaryCapWordsStatesACapTheRunEnforces: the size the brief gives is the
// one finalize compares a file against (stray.go's binaryRefusal), so a cap
// that is not a whole unit of its spelling is stated in bytes rather than
// rounded. fmtBytes is the artifact cap's spelling and rounds to the nearest
// unit — a 1600000-byte cap reads "2 MiB" — and a coder that trusts that number
// writes an asset the refusal turns down, which is the draft the clause exists
// to prevent. A cap that is a whole unit keeps fmtBytes' words, so no brief
// that reads well today changes.
//
// Mutation (return fmtBytes(maxBytes) here): the three non-round cases fail,
// each naming a size the run does not admit.
func TestBinaryCapWordsStatesACapTheRunEnforces(t *testing.T) {
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{1600000, "1600000 bytes"},
		{1536, "1536 bytes"},
		{1500, "1500 bytes"},
		{999, "999 bytes"},
		{1048576, "1 MiB"},
		{2097152, "2 MiB"},
		{2048, "2 KiB"},
	} {
		if got := binaryCapWords(c.bytes); got != c.want {
			t.Errorf("binaryCapWords(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}
