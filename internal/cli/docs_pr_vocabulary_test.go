package cli

import (
	"strings"
	"testing"
)

// prVocabularyWords are the four names the owner fixed (2026-11-12) for the
// state of an open pull request.
var prVocabularyWords = []string{"live", "ready", "stalled", "orphan"}

// prVocabularyCopies is every document that writes the vocabulary out, each
// with the phrases that carry a word's meaning there: the working skill's
// paragraph (what the user's own agent follows), the parallelism skill's
// one-sentence pointer to it, CLAUDE.md's bullet (what an agent working in
// this repository follows) and docs/status.md's top comment (what the next
// refresh of that page reads). They are pinned together because they are one
// vocabulary and not four words that happen to appear in the same places: a
// user told "ready" reads a pull request differently from an agent that meant
// "stalled", and nothing else in the tree fails if one copy drifts -- the
// skills' own lint checks bytes, commands and flags, never a meaning.
//
// The meanings pinned here are the ones the product can produce. A run records
// the outcome `ready` only for a pull request it really left ready: finalize
// refuses that outcome when the provider left the pull request looking like a
// draft (internal/runner/runner.go, the PartialError case that flips ready to
// false), and plugin/skills/working/reference/status.md states the same fact
// for a reader. So no copy may call a draft ready; a draft with a run behind
// it is stalled, whatever that run ended as, and the four words stay a
// partition of every open pull request.
var prVocabularyCopies = []struct {
	path string
	// wants are the phrases a copy must carry. The four bold words are checked
	// apart from these, so a copy that renames one fails even where its own
	// wording differs from its neighbours'.
	wants []string
}{
	{"CLAUDE.md", []string{
		"**Pull-request words.**",
		"**live** (a run is going on it)",
		"**ready** (no run is going and only a merge is left: a non-draft whose checks the owner accepts, which is the only shape a run records the outcome `ready` for)",
		"**stalled** (it had a run, no run is going and it is not ready: a draft with a run behind it, whatever that run ended as",
		"**orphan** (no run ever produced or follows it: a person's pull request, or one whose run records are gone)",
	}},
	{"plugin/skills/working/SKILL.md", []string{
		"**The state of a pull request.**",
		"when you report on a pull request",
		"**live** when a run is going on it",
		"**ready** when no run is going and it waits only on a merge — a non-draft whose checks the owner accepts, which is the only shape a run records the outcome `ready` for",
		"**stalled** when it had a run, no run is going and it is not ready — a draft with a run behind it, whatever that run ended as",
		"**orphan** when no run ever produced or follows it",
	}},
	{"plugin/skills/parallelism/SKILL.md", []string{
		"the four words that skill's own paragraph defines",
		"**live** while a run is going on one",
		"**ready** when only its merge is left",
		"**stalled** when a run has been on it and it is not ready",
		"**orphan** when no run produced or follows it",
	}},
	{"docs/status.md", []string{
		"A pull request is in one of four states, and the four words are the same ones everyone uses",
		`**live** (a run is going on it, which is what rule 3's "in a run" means)`,
		"**ready** (no run is going and only a merge is left, so it is never a draft)",
		"**stalled** (it had a run, no run is going and it is not ready — a draft with a run behind it, whatever that run ended as)",
		"**orphan** (no run ever produced or follows it)",
	}},
}

// TestDocsPullRequestVocabularyIsOneVocabulary: every copy names the four
// words and means the same state by them, the state the product cannot produce
// is in none of them, and the paragraph the parallelism skill points at
// exists. A copy that drops a word, rewords one into something the runner does
// not produce, or points at a paragraph nobody wrote fails here.
func TestDocsPullRequestVocabularyIsOneVocabulary(t *testing.T) {
	for _, c := range prVocabularyCopies {
		text := flattenDoc(readDoc(t, c.path))
		for _, word := range prVocabularyWords {
			if !strings.Contains(text, "**"+word+"**") {
				t.Errorf("%s never names %q as one of the words: the four are one vocabulary, and a copy that drops one leaves its reader with three", c.path, word)
			}
		}
		for _, want := range c.wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not carry %q: the wording of a meaning may change, but the meaning has to be in the copy that states it, and these four are the same everywhere (the owner's list of 2026-11-12)", c.path, want)
			}
		}
		// A draft whose last run ended `ready` is a pull request someone turned
		// back into a draft, never one a run left that way: finalize refuses the
		// outcome for a pull request its provider left looking like a draft. A
		// copy that calls it ready classifies a state nothing produces and
		// contradicts the sentence every other copy carries.
		if strings.Contains(text, "a draft whose last run succeeded with outcome") {
			t.Errorf("%s calls a draft ready: the runner records the outcome `ready` only for a pull request it really left ready, so a draft with the outcome `ready` behind it is a pull request a person turned back into a draft, and the other copies file it under stalled", c.path)
		}
	}
	// The parallelism skill repeats the vocabulary in one sentence and points
	// at working's paragraph for the definitions: a pointer to a paragraph
	// nobody wrote is worse than no pointer, so both ends are pinned.
	if p := flattenDoc(readDoc(t, "plugin/skills/parallelism/SKILL.md")); strings.Contains(p, "that skill's own paragraph defines") {
		if !strings.Contains(flattenDoc(readDoc(t, "plugin/skills/working/SKILL.md")), "**The state of a pull request.**") {
			t.Error("plugin/skills/parallelism/SKILL.md points a planner at the working skill's own paragraph for the four words, and plugin/skills/working/SKILL.md has no such paragraph: the one sentence a batch planner reads for them is a dead end")
		}
	}
}
