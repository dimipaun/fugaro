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
// Ready and stalled are decided by the run record's outcome, never by whether
// the host calls the pull request a draft. A host that refuses draft pull
// requests leaves an ordinary one with `[DRAFT]` in its title instead
// (gitprov.PR.DraftFallback, what reference/diagnose.md tells a reader to say),
// so a run halted there leaves a pull request no host draft flag marks and
// whose outcome is still `draft`: read off the host's flag it looks ready, and
// it is not.
//
// The deciding run is the last one that opened or followed the pull request,
// and an independent review job is not one of them even though it records the
// pull request as well: an artifact review writes the number it reviewed into
// its own record and ends `reviewed` (reviewartifact.go), so `fugaro runs ls
// --pr` lists it beside the run that opened the pull request. Read as that
// run it would leave a genuinely ready pull request in none of the four words,
// which is why every copy names the review job's outcome and says it changes
// nothing.
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
		"**ready** (no run is going, the pull request is not a draft, and the last run that opened or followed it ended with the outcome `ready`",
		"an independent review job records the pull request too, and its own outcome `reviewed` changes nothing",
		"so a pull request a review has since read stays ready",
		"**stalled** (it had a run, no run is going and it is not ready: the outcome `draft`",
		"a pull request on a host with no drafts, an ordinary one it marks `[DRAFT]` in its title that can look ready",
		"or a ready one a person turned back into a draft",
		"**orphan** (no run ever produced or follows it: a person's pull request, or one whose run records are gone)",
	}},
	{"plugin/skills/working/SKILL.md", []string{
		"**The state of a pull request.**",
		"when you report on a pull request",
		"**live** when a run is going on it",
		"**ready** when no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`",
		"a review job's own outcome `reviewed` changes nothing",
		"so a pull request a review has since read is still ready",
		"**stalled** when it had a run, no run is going and it is not ready — the outcome `draft`",
		"an ordinary pull request on a host with no drafts that marks it `[DRAFT]` in its title and can look ready",
		"or a ready one a person turned back into a draft",
		"**orphan** when no run ever produced or follows it",
	}},
	{"plugin/skills/parallelism/SKILL.md", []string{
		"the four words that skill's own paragraph defines",
		"**live** while a run is going on one",
		"**ready** when the last run that opened or followed it ended with the outcome `ready`",
		"a review job's own outcome `reviewed` changes nothing",
		"**stalled** when a run has been on it and it is not ready (`[DRAFT]`-titled ordinary pull requests on a host with no drafts included)",
		"**orphan** when no run produced or follows it",
	}},
	{"docs/status.md", []string{
		"A pull request is in one of four states, and the four words are the same ones everyone uses",
		`**live** (a run is going on it, which is what rule 3's "in a run" means)`,
		"**ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing)",
		"**stalled** (it had a run, no run is going and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft)",
		"**orphan** (no run ever produced or follows it)",
	}},
}

// prVocabularyRetired are wordings a copy of the vocabulary may not go back to,
// with the fact that rules each one out. All three were written, all three were
// found wrong by the runner's own behaviour, and any one coming back means the
// four words classify a pull request differently from the record they are read
// from.
var prVocabularyRetired = []struct {
	phrase string
	why    string
}{
	{
		"a draft whose last run succeeded with outcome",
		"it calls a draft ready: the runner writes the outcome `ready` only for a pull request it really left ready (finalize flips it back when the provider left the pull request looking like a draft), so a draft with that outcome behind it is a pull request a person turned back into a draft, and every copy files it under stalled",
	},
	{
		"the run that opened or last followed it ended with the outcome",
		"it leaves the deciding run ambiguous, and a reader taking the newest run on the pull request for it finds an independent review job: an artifact review records the number it reviewed (reviewartifact.go) and ends `reviewed`, so `fugaro runs ls --pr` lists it, and a genuinely ready pull request becomes neither ready nor stalled. Every copy says the last run that opened or followed it and names that job's outcome as changing nothing",
	},
	{
		"non-draft whose checks the owner accepts",
		"it reads readiness off the host's draft flag: a host that refuses draft pull requests leaves an ordinary one marked `[DRAFT]` in its title, which no draft flag marks and whose run's outcome is `draft`, so the pull request looks ready and is not (reference/diagnose.md tells a reader to say so)",
	},
}

// TestDocsPullRequestVocabularyIsOneVocabulary: every copy names the four
// words and means the same state by them, each copy files the `[DRAFT]`-titled
// fallback under stalled, every copy says a review job's outcome changes
// nothing, none has gone back to a wording the product rules out, and the
// paragraph the parallelism skill points at exists. A copy that drops a word,
// decides readiness by the host's draft flag instead of the run's own outcome,
// lets a review job decide it, or points at a paragraph nobody wrote fails
// here.
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
		for _, r := range prVocabularyRetired {
			if strings.Contains(text, r.phrase) {
				t.Errorf("%s says %q again: %s", c.path, r.phrase, r.why)
			}
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
