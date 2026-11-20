package rigor

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// personaBudget is the most bytes one role's text may take. The persona is
// prepended to a stage's prompt, and the prompt's own budget is what the run
// spends before it reaches the change; a persona that outgrew this would cost
// more than the level's stance is worth (reconciliation U1).
const personaBudget = 4096

// personaMinBytes is the least a role's text may take: a level's stance is a
// whole paragraph about what it cares about and what it hands back, and a
// sentence or two would not tell an agent what to do differently.
const personaMinBytes = 200

// personaCoderFirstSentence is the sentence each coder text opens with, pinned
// as words rather than read from the file: it is the first thing a stage's
// prompt says about how to work, so a change to it is a change to every run at
// that level.
var personaCoderFirstSentence = map[Rigor]string{
	POC:      "You are a smart, enthusiastic developer who wants to see an idea working as soon as possible.",
	Startup:  "You are the brilliant, slightly arrogant engineer everyone relies on, far faster and sharper than the people around you.",
	Pro:      "You are a senior consultant with twenty years in enterprises and banks.",
	Critical: "You have at least twenty years of experience and you have seen it all, and you have been burned several times.",
	Hotfix:   "You have been woken at 2am.",
}

// personaCoderHandsBack is what each coder text promises to hand back (spec
// §2, §6): the level's artefact, the heading the runner reads it under, and
// the reason a "hands back" list exists at all. POC is the level with none —
// he does not know what he skipped, and no hand-back file is required of him.
var personaCoderHandsBack = map[Rigor][]string{
	POC:      {"how to run it and play with it", "this is a prototype, do not ship it"},
	Startup:  {"a deferred list", "## Deferred"},
	Pro:      {"a deferred list", "## Deferred"},
	Critical: {"an open-risks list", "## Open risks", "## Verification"},
	Hotfix:   {"revert plan", "## Revert plan", "morning note", "## Morning note"},
}

// personaReviewerStandard is the reviewer standard each level's text carries
// (spec §5, LF14): the coder and the reviewer run at the same level, and the
// standard is the level's own. Hotfix's judge reads the same two sections the
// coder writes, so those headings are pinned here too.
var personaReviewerStandard = map[Rigor][]string{
	POC:      {"Do not report missing error handling"},
	Startup:  {"missing from the deferred list", "as a finding"},
	Pro:      {"missing from the deferred list", "as a finding"},
	Critical: {"adversarial"},
	Hotfix:   {"minimality", "revert plan", "## Revert plan", "morning note", "## Morning note"},
}

// personaHandBackLevels are the levels whose hand-back the runner reads by
// section (LV4.2, LV5.1): POC's note is the pull request's own body and needs
// no file, so it is not one of them.
var personaHandBackLevels = []Rigor{Startup, Pro, Critical, Hotfix}

// personaDeployRE and personaNegationRE hold LD7's rule as a test: a persona
// never tells an agent to deploy (D13), so every sentence that mentions
// deploying is a sentence that says it does not happen.
var (
	personaDeployRE   = regexp.MustCompile(`(?i)\bdeploy`)
	personaNegationRE = regexp.MustCompile(`(?i)(\bnever\b|\bnot\b|\bno\b|\bcannot\b|\bwithout\b|n't)`)
)

// personaPlaceholderRE matches the shapes a text written for interpolation
// carries. The personas are data, not templates: a file that spelled one of
// these would be a repository's content promised into a prompt that has none
// (the global constraint; reconciliation §4 item 2).
var personaPlaceholderRE = regexp.MustCompile(`\{\{|\}\}|\$\{|%[sdvq]|<[a-z][a-z0-9_.-]*>`)

// personaSentences splits a text into its sentences, breaking after a full
// stop, an exclamation mark or a question mark that ends a line or is followed
// by whitespace, so a dot inside a word ("pr.md") does not split one.
func personaSentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '.', '!', '?':
			if i+1 == len(text) || text[i+1] == ' ' || text[i+1] == '\n' || text[i+1] == '\t' {
				if s := strings.TrimSpace(text[start : i+1]); s != "" {
					out = append(out, s)
				}
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// TestPersonaEveryLevelAndRoleHasAText is the coverage half of the task's
// brief: every level and every role has a text, and every text is under the
// budget the prompt pays for it. A missing one would be a stage asked nothing;
// an oversized one would spend the budget before the change is read.
func TestPersonaEveryLevelAndRoleHasAText(t *testing.T) {
	for _, level := range Rigors {
		for _, role := range []Role{Coder, Reviewer} {
			text := Persona(level, role)
			if strings.TrimSpace(text) == "" {
				t.Errorf("Persona(%s, %s) is empty", level, role)
				continue
			}
			if len(text) < personaMinBytes {
				t.Errorf("Persona(%s, %s) is %d bytes, under the %d a stance takes", level, role, len(text), personaMinBytes)
			}
			if len(text) > personaBudget {
				t.Errorf("Persona(%s, %s) is %d bytes, over the %d a persona may take", level, role, len(text), personaBudget)
			}
		}
	}
}

// TestPersonaCoderOpensWithItsOwnSentenceAndHandsBack pins the two things a
// reader of the file would change first: the sentence the coder text opens
// with, and the artefact the level promises to leave behind. Both are read
// from the text, so a reworded file fails here rather than in a run.
func TestPersonaCoderOpensWithItsOwnSentenceAndHandsBack(t *testing.T) {
	if len(personaCoderFirstSentence) != len(Rigors) || len(personaCoderHandsBack) != len(Rigors) {
		t.Fatalf("the pinned tables cover %d and %d levels; want one row per level, %d",
			len(personaCoderFirstSentence), len(personaCoderHandsBack), len(Rigors))
	}
	for _, level := range Rigors {
		text := Persona(level, Coder)
		sentences := personaSentences(text)
		if len(sentences) == 0 {
			t.Errorf("Persona(%s, %s) has no sentence", level, Coder)
			continue
		}
		if got, want := sentences[0], personaCoderFirstSentence[level]; got != want {
			t.Errorf("Persona(%s, %s) opens with %q; want %q", level, Coder, got, want)
		}
		if !strings.Contains(text, "You hand back") {
			t.Errorf("Persona(%s, %s) never says what it hands back", level, Coder)
		}
		for _, want := range personaCoderHandsBack[level] {
			if !strings.Contains(text, want) {
				t.Errorf("Persona(%s, %s) does not hand back %q:\n%s", level, Coder, want, text)
			}
		}
	}
}

// TestPersonaNamesTheHandBackFileAndSections: a level's sections are read by
// the runner from the agent's own hand-back file, and that file is `pr.md` on
// a first run and `followup.md` on a follow-up (internal/followup's
// SystemPromptLines replaces the pr.md line, internal/runner's followupFile
// writes followup.md). A coder text that named only `pr.md` would send a
// follow-up's deferrals to a file the runner does not read; POC needs no file
// at all, so its text names none.
func TestPersonaNamesTheHandBackFileAndSections(t *testing.T) {
	if len(personaHandBackLevels) != 4 {
		t.Fatalf("the levels with a hand-back file are %d; want the four LV5.1 checks to need one, %d", len(personaHandBackLevels), 4)
	}
	for _, level := range personaHandBackLevels {
		text := Persona(level, Coder)
		for _, want := range []string{"pr.md", "followup.md"} {
			if !strings.Contains(text, want) {
				t.Errorf("Persona(%s, %s) does not name %q; a follow-up's hand-back file is followup.md, not pr.md", level, Coder, want)
			}
		}
	}
	if text := Persona(POC, Coder); strings.Contains(text, "pr.md") || strings.Contains(text, "followup.md") {
		t.Errorf("the POC text names a hand-back file, and none is required of it:\n%s", text)
	}
}

// TestPersonaReviewerCarriesTheLevelsStandard pins spec §5: the reviewer runs
// at the task's own level, so each reviewer text states that level's standard
// and no other.
func TestPersonaReviewerCarriesTheLevelsStandard(t *testing.T) {
	if len(personaReviewerStandard) != len(Rigors) {
		t.Fatalf("the pinned table covers %d levels; want one row per level, %d", len(personaReviewerStandard), len(Rigors))
	}
	for _, level := range Rigors {
		text := Persona(level, Reviewer)
		for _, want := range personaReviewerStandard[level] {
			if !strings.Contains(text, want) {
				t.Errorf("Persona(%s, %s) does not carry %q:\n%s", level, Reviewer, want, text)
			}
		}
	}
}

// TestPersonaPOCKeepsTheTwoGuardrails: POC drops everything production would
// ask for, with two exceptions that hold at every level — no real secret or
// credential, and nothing destructive to a shared resource (spec §2.1,
// reconciliation §4 item 2).
func TestPersonaPOCKeepsTheTwoGuardrails(t *testing.T) {
	text := Persona(POC, Coder)
	for _, want := range []string{
		"Never write a real secret or a real credential",
		"Never do anything destructive to a shared resource",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the POC coder text does not state the guardrail %q:\n%s", want, text)
		}
	}
}

// TestPersonaStartupKeepsTheDeadlineLine: the spec's "on the critical path for
// a deadline, cut harder" is persona wording, not a rule anything acts on — no
// task carries a deadline — and a reworded file would quietly drop it.
func TestPersonaStartupKeepsTheDeadlineLine(t *testing.T) {
	const want = "on the critical path for a deadline"
	if text := Persona(Startup, Coder); !strings.Contains(text, want) {
		t.Errorf("the Startup coder text does not carry %q:\n%s", want, text)
	}
}

// TestPersonaHotfixNamesRevertAndTheMorningNote pins LD7's two artefacts by
// name, on both halves of the level: the coder writes them, the reviewer
// judges them.
func TestPersonaHotfixNamesRevertAndTheMorningNote(t *testing.T) {
	for _, role := range []Role{Coder, Reviewer} {
		text := Persona(Hotfix, role)
		for _, want := range []string{"revert", "morning note"} {
			if !strings.Contains(text, want) {
				t.Errorf("the Hotfix %s text does not name %q:\n%s", role, want, text)
			}
		}
	}
}

// TestPersonaNeverTellsAnAgentToDeploy is LD7 as a rule over every text:
// Fugaro opens pull requests and never deploys (D13), so no sentence may ask
// an agent to deploy, and the Hotfix text — the one that would — says so in
// the negative ("Fugaro never deploys").
func TestPersonaNeverTellsAnAgentToDeploy(t *testing.T) {
	if !personaDeployRE.MatchString(Persona(Hotfix, Coder)) {
		t.Error("the Hotfix coder text says nothing about deploying: the rule that it never happens has gone unwritten")
	}
	for _, level := range Rigors {
		for _, role := range []Role{Coder, Reviewer} {
			for _, s := range personaSentences(Persona(level, role)) {
				if personaDeployRE.MatchString(s) && !personaNegationRE.MatchString(s) {
					t.Errorf("the %s %s text tells an agent to deploy: %q", level, role, s)
				}
			}
		}
	}
}

// TestPersonaHasNoTemplatePlaceholder: a persona is data, read whole into a
// prompt, and nothing fills a placeholder in it. A text that carried one would
// reach an agent as the literal braces.
func TestPersonaHasNoTemplatePlaceholder(t *testing.T) {
	for _, level := range Rigors {
		for _, role := range []Role{Coder, Reviewer} {
			if got := personaPlaceholderRE.FindString(Persona(level, role)); got != "" {
				t.Errorf("Persona(%s, %s) carries the placeholder %q", level, role, got)
			}
		}
	}
}

// TestPersonaUnknownLevelOrRoleIsEmpty: a caller passes a level Resolve has
// already checked and one of the two roles, so this is a programming error
// rather than a run's input. It answers "", never a default: a level that fell
// through to another level's stance would run under a rigour nobody chose.
func TestPersonaUnknownLevelOrRoleIsEmpty(t *testing.T) {
	for _, level := range []Rigor{"", "fast", "Poc", "pro ", "critical\n"} {
		for _, role := range []Role{Coder, Reviewer} {
			if got := Persona(level, role); got != "" {
				t.Errorf("Persona(%q, %s) = %q; want empty for a level outside the vocabulary", level, role, got)
			}
		}
	}
	for _, role := range []Role{"", "boss", "Coder", "Review"} {
		if got := Persona(Pro, role); got != "" {
			t.Errorf("Persona(%s, %q) = %q; want empty for a role outside coder and reviewer", Pro, role, got)
		}
	}
}

// TestPersonaEmbeddedOneFilePerLevel: the texts are embedded in the binary,
// one Markdown file per level and no more (LD10), so nothing under a
// repository's own tree can rewrite a run's level.
func TestPersonaEmbeddedOneFilePerLevel(t *testing.T) {
	entries, err := fs.ReadDir(personaFS, "personas")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	const want = "critical.md,hotfix.md,poc.md,pro.md,startup.md"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("the embedded personas are %s, want %s", got, want)
	}
	seen := map[string]Rigor{}
	for _, level := range Rigors {
		text := Persona(level, Coder)
		if other, dup := seen[text]; dup {
			t.Errorf("%s and %s carry the same coder text: a level whose stance is another level's is a level with no stance", level, other)
		}
		seen[text] = level
	}
}

// TestPersonaPartsRefuseAMalformedFile: the split is the only parsing here, so
// a file that lost a heading, emptied a part or grew a second one of the same
// name must fail loudly rather than hand a stage half a persona. The failure
// is at load, which the package turns into a panic: the files are embedded
// from this repository's own tree, so a shape the tests do not pin can only
// come from an edit that broke one.
func TestPersonaPartsRefuseAMalformedFile(t *testing.T) {
	const good = "# Title\n\n## Coder\n\ncode text\n\n## Reviewer\n\nreview text\n"
	if parts, err := personaParts("x.md", good); err != nil {
		t.Fatalf("a well-formed file is refused: %v", err)
	} else if parts[Coder] != "code text" || parts[Reviewer] != "review text" {
		t.Fatalf("a well-formed file reads as %+v", parts)
	}
	for name, src := range map[string]string{
		"no reviewer part":   "# Title\n\n## Coder\n\ncode text\n",
		"no coder part":      "# Title\n\n## Reviewer\n\nreview text\n",
		"empty part":         "# Title\n\n## Coder\n\n\n## Reviewer\n\nreview text\n",
		"duplicate coder":    "# Title\n\n## Coder\n\na\n\n## Coder\n\nb\n\n## Reviewer\n\nc\n",
		"unknown heading":    "# Title\n\n## Coder\n\na\n\n## Review\n\nb\n\n## Reviewer\n\nc\n",
		"reversed order":     "# Title\n\n## Reviewer\n\nb\n\n## Coder\n\na\n",
		"nothing at all":     "",
		"title only":         "# Title\n",
		"heading with extra": "# Title\n\n## Coder \n\na\n\n## Reviewer\n\nb\n",
	} {
		if _, err := personaParts("x.md", src); err == nil {
			t.Errorf("%s: personaParts accepted a malformed file", name)
		}
	}
}
