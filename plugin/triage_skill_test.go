package plugin_test

import (
	"strings"
	"testing"
)

// TestWorkingSkillNamesTheTriageAndTheFollowUps: the triage stage lets a
// startup or pro run ship a pull request whose review left smaller findings
// open, and the follow-ups command is the only way those findings become
// work. An agent that reads a ready pull request as finished work never runs
// it, and a reader who is not told that ready is not merged treats a run as
// delivered when a person still has to merge it -- so the skill names the
// stage, the switch that turns it off, the list the deferrals land in, and
// both commands, with the flags that make the second one write anything.
func TestWorkingSkillNamesTheTriageAndTheFollowUps(t *testing.T) {
	files := workingSkillFiles(t)
	var all strings.Builder
	for _, text := range files {
		all.WriteString(text)
		all.WriteString("\n")
	}
	tree := all.String()

	for _, want := range []struct{ what, text string }{
		{"the stage between the review and the fix", "triage"},
		{"the repository's own switch for it", "agent.triage: false"},
		{"where the deferrals are listed", "Deferred (follow-ups)"},
		{"the command that files them", "fugaro followups"},
		{"the flag that makes it write, rather than plan", "--launch"},
		{"the cap on one call", "--max"},
		{"the listing of the work a review left behind", "fugaro runs ls --debt"},
		{"that a ready pull request is a person's to merge, never merged", "Ready is never merged"},
	} {
		if !strings.Contains(tree, want.text) {
			t.Errorf("the working skill never names %s (%q): a run can end ready with findings deferred, and an agent left without the command and its flags has no way to turn them into work", want.what, want.text)
		}
	}

	skill, ok := files["skills/working/SKILL.md"]
	if !ok {
		t.Fatal("the working skill has no SKILL.md")
	}
	for _, want := range []string{"fugaro followups", "fugaro runs ls --debt", "agent.triage: false"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skills/working/SKILL.md never says %q: the stage, its switch and the two commands are what the section on a run's leftover findings is read for", want)
		}
	}
}
