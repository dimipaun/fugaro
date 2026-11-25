package cli

// The cloud tier of `fugaro troubleshoot` as the documents describe it
// (docs/troubleshoot-cloud, D2400): T-TC9's sentences, the recipe they run
// through and the release notes that carry them.
//
// The tier's three claims are sentences a person reads rather than behaviours a
// test can measure: which tier answers when, that the cloud one is asked for
// last and never runs on the person's own credential, and that a cloud answer
// says what it cost. Two of them live in the guide the diagnosis itself is
// given (docs/troubleshooting-for-agents.md, embedded as
// internal/diagnoseagent's guide.md by `go test ./internal/diagnoseagent -run
// Guide -update-guide`) and in the plugin skill an agent reads
// (plugin/skills/working/SKILL.md); the third is the cost line the record
// carries (internal/cli/troubleshoot.go's printer). The plugin's own lint
// (plugin/skills_lint_test.go) is what checks that the skill names no command
// or flag the CLI does not have — what is pinned here is that the sentence is
// there at all.

import (
	"strings"
	"testing"
)

const (
	troubleshootCloudGuide = "docs/troubleshooting-for-agents.md"
	troubleshootCloudSkill = "plugin/skills/working/SKILL.md"
	// The skill's own page for the command, and where its four tiers live: the
	// SKILL.md is at its byte budget (plugin/skills_lint_test.go's
	// maxSkillBytes), so the sentence the tier arrives with is one line there
	// and the detail behind it is a reference file, the way the setup skill's
	// managed rules moved to its own (docs/managed-projects/
	// task-3.0-decisions.md).
	troubleshootCloudSkillRef = "plugin/skills/working/reference/troubleshoot.md"
	// The catalog entry's row in the recipes guide: the one place a person reads
	// what the cloud tier launches (T-TC3 wrote the row, and this is the pin
	// that it is still there and still says what it is).
	troubleshootCloudRecipes = "docs/recipes.md"
	// The next release's notes. The CLI stream is at 0.26.1 and its next minor
	// is 0.27.0 (docs/release-streams.yaml), and a release file above the
	// stream's `current` is the next release's notes, written in the pull
	// request that makes the change and left out of the site's changelog until
	// the stream reaches it (docs/releases/README.md, website/src/lib/changelog.mjs).
	troubleshootCloudNotes = "docs/releases/v0.27.0.md"
)

// TestDocsTroubleshootCloudNamesTheTiers: the guide names D2400's four tiers in
// the order they are tried, says the cloud one is reached last, says the job
// never runs on the person's own credential, and says a cloud answer prints
// what it cost as the figure the run's record holds.
//
// The order is the whole design: it is what makes the command keep working when
// the cloud is what is broken, and it is the one property the spec's acceptance
// is written around. A guide that stopped naming a tier would leave a person
// reading a cloud answer as a local one, and one that still said a diagnosis
// never runs as a cloud job would describe a command this release no longer
// has.
func TestDocsTroubleshootCloudNamesTheTiers(t *testing.T) {
	doc := flattenDoc(readDoc(t, troubleshootCloudGuide))

	last := -1
	for _, want := range []struct{ marker, tier string }{
		{"**The rules, with no model", "the rules tier"},
		{"**The local diagnosis", "the local login"},
		{"**The cloud diagnosis job", "the cloud job"},
		{"**Evidence only", "the evidence-only path"},
	} {
		i := strings.Index(doc, want.marker)
		if i < 0 {
			t.Errorf("%s does not name %s: a reader who cannot tell the four tiers apart reads a cloud answer as a local one", troubleshootCloudGuide, want.tier)
			continue
		}
		if i < last {
			t.Errorf("%s names %s before the tier it follows, and the order is the whole design (D2400): rules, then the local login, then the cloud job, then the evidence alone", troubleshootCloudGuide, want.tier)
		}
		last = i
	}

	for _, want := range []struct{ phrase, why string }{
		{"tries them in this order", "the guide says the tier order is the command's own, not a preference"},
		{"asked for last", "the cloud tier is the one reached last: the tiers that cost nothing run first"},
		{"the person's own project", "a cloud diagnosis sends the evidence to the person's own project (D2404)"},
		{"never runs on the person's own credential", "the cloud job mounts no credential of the person's own (D2403)"},
		{"says what it cost", "a cloud answer prints what it cost (D2407, the spec's acceptance)"},
		{"the diagnosis run's record holds", "the cost is the run record's figure, not the job's meter (D2407, T4)"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", troubleshootCloudGuide, want.phrase, want.why)
		}
	}

	// The sentence this release replaces. A guide that still says the command
	// runs locally and never as a cloud job is a guide whose agent answers
	// "what you could not check" from the wrong premise (D2400, D2410).
	if strings.Contains(doc, "never as a cloud job") {
		t.Errorf("%s still says a diagnosis never runs as a cloud job, and the cloud job is D2400's third tier", troubleshootCloudGuide)
	}

	// The skill an agent reads carries the same facts: the tier arrives with
	// the release (F19), and an agent that read the old line would tell a user
	// with no login that the command explains nothing. The one line SKILL.md
	// has for the command names the tiers and points at the page that holds
	// them, and that page is where the order and the two rules live.
	skill := flattenDoc(readDoc(t, troubleshootCloudSkill))
	for _, want := range []struct{ phrase, why string }{
		{"`fugaro troubleshoot [ID]`", "the line for the command does not name it the way the CLI spells it"},
		{"reference/troubleshoot.md", "the line does not send the agent to the page that describes the tiers"},
		{"a diagnosis job in the user's project", "the line does not say what the last tier is"},
		{"never on the user's own credential", "the line does not say whose credential the cloud job does not use (D2403)"},
	} {
		if !strings.Contains(skill, want.phrase) {
			t.Errorf("%s does not say %q: %s", troubleshootCloudSkill, want.phrase, want.why)
		}
	}

	ref := flattenDoc(readDoc(t, troubleshootCloudSkillRef))
	last = -1
	for _, want := range []struct{ marker, tier string }{
		{"**The rules, with no model", "the rules tier"},
		{"**The local diagnosis", "the local login"},
		{"**The cloud diagnosis job", "the cloud job"},
		{"**Evidence only", "the evidence-only path"},
	} {
		i := strings.Index(ref, want.marker)
		if i < 0 {
			t.Errorf("%s does not name %s: an agent reading the tier it reached must be able to tell the four apart", troubleshootCloudSkillRef, want.tier)
			continue
		}
		if i < last {
			t.Errorf("%s names %s before the tier it follows, and the order is the whole design (D2400): rules, then the local login, then the cloud job, then the evidence alone", troubleshootCloudSkillRef, want.tier)
		}
		last = i
	}
	for _, want := range []struct{ phrase, why string }{
		{"asked for last", "the page does not say the cloud tier is the one reached last"},
		{"no login of its own", "the page does not say when the cloud tier is reached (D2400)"},
		{"the user's own project", "the page does not say the evidence goes to the user's own project (D2404)"},
		{"never runs on the user's own credential", "the page does not say whose credential the job spends (D2403)"},
		{"It says what it cost", "the page does not say a cloud answer carries its cost (D2407, the spec's acceptance)"},
		{"the diagnosis run's record holds", "the cost is the run record's figure, not the job's meter (D2407, T4)"},
		{"fugaro update", "the page does not name the one command that fixes the gates the tier meets (F18)"},
	} {
		if !strings.Contains(ref, want.phrase) {
			t.Errorf("%s does not say %q: %s", troubleshootCloudSkillRef, want.phrase, want.why)
		}
	}
}

// TestDocsTroubleshootCloudNamesTheRecipe: docs/recipes.md names the catalog
// recipe the tier launches, its step, its two output files and the job image it
// needs — the row a person reads to see that `troubleshoot` is not a recipe for
// a task, and the section-9 prose that says the pre-flight refuses a diagnosis
// against an image older than the release that can execute the step.
func TestDocsTroubleshootCloudNamesTheRecipe(t *testing.T) {
	doc := flattenDoc(readDoc(t, troubleshootCloudRecipes))
	for _, want := range []struct{ phrase, why string }{
		{"| `troubleshoot` |", "the catalog entry has no row: nothing tells a person what the tier launches"},
		{"`mode: artifact`; `diagnose: {}`; `outputs: { globs: [\"out/diagnosis.md\", \"out/diagnosis.json\"] }`",
			"the row does not name the step and the two files the answer is read back from (D2402)"},
		{"The cloud tier of `fugaro troubleshoot` launches it, on this machine's own project, when the machine has no login of its own",
			"the row does not say who launches the recipe and when"},
		{"Never the recipe for a task", "the row does not say it is a recipe no task runs"},
		{"its `diagnose` step is the runner's own, so the cloud tier's pre-flight refuses a diagnosis against a job image older than 0.26.0, naming that release and `fugaro update`",
			"the section-9 prose does not name the image gate the tier meets at the terminal (F18, T-TC6)"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", troubleshootCloudRecipes, want.phrase, want.why)
		}
	}
}

// TestDocsTroubleshootCloudReleaseNotesCarryTheTier: the release that ships the
// tier says so in its notes — what the command does now, that the answer is a
// record with a cost, that the job never runs on the person's own credential,
// and that the whole upgrade is the one documented command, `fugaro update`.
//
// It pins no `### For operators` line and no absence of one: the notes' operator
// section belongs to whatever else ships in that release, and this tier owes
// none — its only operator-facing fact is that the job images the tier needs
// are what `fugaro update` builds.
func TestDocsTroubleshootCloudReleaseNotesCarryTheTier(t *testing.T) {
	doc := flattenDoc(readDoc(t, troubleshootCloudNotes))
	for _, want := range []struct{ phrase, why string }{
		{"`fugaro troubleshoot`", "the notes never name the command the tier arrives on"},
		{"no Claude login", "the notes do not say which machine the tier is for (D2400)"},
		{"prints what it cost", "the notes do not say a cloud answer carries its cost (D2407)"},
		{"never runs on the person's own credential", "the notes do not say whose credential the job spends (D2403)"},
		{"Upgrading is `fugaro update`", "the notes do not say the whole upgrade is the one documented command"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", troubleshootCloudNotes, want.phrase, want.why)
		}
	}
}
