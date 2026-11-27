package cli

import (
	"strings"
	"testing"
)

// prVocabularyWords are the names of the states of an open pull request: the
// four the owner fixed on 2026-11-12, and the fifth he ruled on 2026-11-21
// (D3195, a pull request whose chain is paused).
var prVocabularyWords = []string{"live", "paused", "ready", "stalled", "orphan"}

// prVocabularyPaused is what every copy of the vocabulary says the fifth word
// means, in the same words: it is one fact (D3195), and a copy that states it
// differently, or drops one of its two halves, is a copy whose reader files a
// paused pull request where another copy's reader does not.
var prVocabularyPaused = []string{
	"the orchestrator will resume it at a named time",
	"it needs nothing from a person, is not landed and takes no follow-up while the pause stands",
}

// prVocabularyCopies is every document that writes the vocabulary out, each
// with the phrases that carry a word's meaning there: the working skill's
// paragraph (what the user's own agent follows), the parallelism skill's
// one-sentence pointer to it, CLAUDE.md's bullet (what an agent working in
// this repository follows) and docs/status.md's top comment (what the next
// refresh of that page reads). They are pinned together because they are one
// vocabulary and not words that happen to appear in the same places: a
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
// run it would leave a genuinely ready pull request in none of the words,
// which is why every copy names the review job's outcome and says it changes
// nothing.
//
// Two of the copies also name **land**, the action on those states and not one
// of them (the owner's ruling of 2026-11-13; a sixth state since D3195 added
// the fifth). One of them defined it for a stalled pull request alone while the
// other landed a ready one too, and nothing failed: the words were pinned and
// the sentence beside them was not. So the definition is pinned here too, at
// the two copies that carry it, with the clause that makes it an action rather
// than a state -- and that clause counts the states, because a copy calling
// land the fifth word has its reader count five and find land among them.
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
		"**paused** (a run suspended itself on it and the orchestrator will resume it at a named time",
		"**ready** (no run is going, the pull request is not a draft, and the last run that opened or followed it ended with the outcome `ready`",
		"an independent review job records the pull request too, and its own outcome `reviewed` changes nothing",
		"so a pull request a review has since read stays ready",
		"**stalled** (it had a run, no run is going, no pause stands and it is not ready: the outcome `draft`",
		"a pull request on a host with no drafts, an ordinary one it marks `[DRAFT]` in its title that can look ready",
		"or a ready one a person turned back into a draft",
		"**orphan** (no run ever produced or follows it: a person's pull request, or one whose run records are gone)",
		// Land is the action on those states and not one of them (the
		// owner's ruling of 2026-11-13; a sixth state since D3195 added
		// the fifth), so the two copies of the vocabulary that name it
		// carry the same definition of it: what a land is, and that it
		// leaves the five words as they were.
		"**Land** is an action, not a sixth state",
		"to land a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks",
		"never for failing tests on the final commit",
		"never on a security path without the person's explicit word (the `landing` skill)",
	}},
	{"plugin/skills/working/SKILL.md", []string{
		"**The state of a pull request.**",
		"Five words name it, and use them when you report on a pull request",
		"**live** when a run is going on it",
		"**paused** when a run suspended itself on it and the orchestrator will resume it at a named time",
		"**ready** when no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`",
		"a review job's own outcome `reviewed` changes nothing",
		"so a pull request a review has since read is still ready",
		"**stalled** when it had a run, no run is going, no pause stands and it is not ready — the outcome `draft`",
		"an ordinary pull request on a host with no drafts that marks it `[DRAFT]` in its title and can look ready",
		"or a ready one a person turned back into a draft",
		"**orphan** when no run ever produced or follows it",
	}},
	{"plugin/skills/parallelism/SKILL.md", []string{
		"the five words that skill's own paragraph defines",
		"**live** while a run is going on one",
		"**paused** while a run of it suspended itself and the orchestrator will resume it at a named time",
		"**ready** when the last run that opened or followed it ended with the outcome `ready`",
		"a review job's own outcome `reviewed` changes nothing",
		"**stalled** when a run has been on it, no pause stands and it is not ready (`[DRAFT]`-titled ordinary pull requests on a host with no drafts included)",
		"**orphan** when no run produced or follows it",
	}},
	{"docs/status.md", []string{
		"A pull request is in one of five states, and the five words are the same ones everyone uses",
		`**live** (a run is going on it, which is what rule 3's "in a run" means)`,
		"**paused** (a run suspended itself on it and the orchestrator will resume it at a named time",
		"**ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing)",
		"**stalled** (it had a run, no run is going, no pause stands and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft)",
		"**orphan** (no run ever produced or follows it)",
		"Landing is an action, not a sixth state",
		"to **land** a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks, rather than running another fix round (the `landing` skill)",
	}},
}

// prVocabularyRetired are wordings a copy of the vocabulary may not go back to,
// with the fact that rules each one out. Every one of them was written and every
// one was found wrong -- three by the runner's own behaviour, three once D3195
// named the fifth word -- and any one coming back means the words classify a
// pull request differently from the record they are read from.
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
		"not a fifth state",
		"land was never one of the states, and the fifth is `paused` (D3195): a copy that calls land the fifth word has its reader count five states and find land among them",
	},
	{
		"in one of four states",
		"the fifth word is `paused` (D3195): a copy that counts four words leaves a paused pull request outside the vocabulary",
	},
	{
		"the four words",
		"there are five of them since D3195 added `paused`, and a copy that counts four is a copy whose reader has lost one",
	},
	{
		"no run is going and it is not ready",
		"it reads a paused pull request as stalled: a run that suspended itself on the pull request is still in flight, so the pause is decided before ready and stalled (prGroup.state), and stalled's remedy -- a follow-up run's work, or a merge as-is -- is what a standing pause forbids. Every copy says no pause stands before stalled can",
	},
	{
		"non-draft whose checks the owner accepts",
		"it reads readiness off the host's draft flag: a host that refuses draft pull requests leaves an ordinary one marked `[DRAFT]` in its title, which no draft flag marks and whose run's outcome is `draft`, so the pull request looks ready and is not (reference/diagnose.md tells a reader to say so)",
	},
}

// TestDocsPullRequestVocabularyIsOneVocabulary: every copy names the five
// words and means the same state by them, every copy defines `paused` in the
// vocabulary's own words, each copy files the `[DRAFT]`-titled fallback under
// stalled, every copy says a review job's outcome changes nothing, none has
// gone back to a wording the product rules out, and the paragraph the
// parallelism skill points at exists. A copy that drops a word, states the
// pause differently, decides readiness by the host's draft flag instead of the
// run's own outcome, lets a review job decide it, or points at a paragraph
// nobody wrote fails here.
func TestDocsPullRequestVocabularyIsOneVocabulary(t *testing.T) {
	for _, c := range prVocabularyCopies {
		text := flattenDoc(readDoc(t, c.path))
		for _, word := range prVocabularyWords {
			if !strings.Contains(text, "**"+word+"**") {
				t.Errorf("%s never names %q as one of the words: the five are one vocabulary, and a copy that drops one leaves its reader with four", c.path, word)
			}
		}
		for _, want := range prVocabularyPaused {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not define `paused` in the vocabulary's own words (%q): the fifth state is one fact (D3195), and a copy that states it differently leaves its reader filing a paused pull request elsewhere", c.path, want)
			}
		}
		for _, want := range c.wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not carry %q: the wording of a meaning may change, but the meaning has to be in the copy that states it, and these are the same everywhere (the owner's list of 2026-11-12, and D3195 for the fifth)", c.path, want)
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
			t.Error("plugin/skills/parallelism/SKILL.md points a planner at the working skill's own paragraph for the five words, and plugin/skills/working/SKILL.md has no such paragraph: the one sentence a batch planner reads for them is a dead end")
		}
	}
}
