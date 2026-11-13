package plugin_test

// The issue family's three writers are a person's own and only a person's:
// `fugaro issue sync` uploads to the project's store, `dismiss` sets an issue
// aside and `submit --ask` uploads and files on GitHub. The CLI refuses all
// three in a coding agent's session (agentMarker, internal/cli/issue_sync.go,
// issue_dismiss.go, issue_submit.go), and that refusal is a mitigation rather
// than a barrier: the guard reads an environment marker a session can clear.
// A skill sentence that only predicts the refusal leaves an agent free to run
// the command and be told no, so the paragraph the working skill carries has
// to forbid the run and say whose command it is — which is the wording these
// tests pin, because nothing else in the plugin does.
//
// internal/cli pins the other half: the guard itself
// (TestSyncRefusesInAgentSession, TestIssueSubmitRefusesInAgentSession).

import (
	"strings"
	"testing"
)

// issueFamilyAnchor opens the one bullet of skills/working/SKILL.md that
// carries the issue family. The tests read the paragraph out of that line
// rather than the whole file, so a phrase they require cannot be satisfied by
// another section.
const issueFamilyAnchor = "The rest of the family is that same record from other angles"

// issueWriterRules is what the issue-family paragraph must say about the three
// commands a session writes nothing with.
var issueWriterRules = []struct{ what, text string }{
	{"that the session never runs them", "never run them"},
	{"whose commands they are", "the user's own command at their own terminal"},
}

// issueFamilyParagraph is that bullet, whole.
func issueFamilyParagraph(t *testing.T) string {
	t.Helper()
	skill, ok := workingSkillFiles(t)["skills/working/SKILL.md"]
	if !ok {
		t.Fatal("the working skill has no SKILL.md")
	}
	for _, line := range strings.Split(skill, "\n") {
		if strings.HasPrefix(line, "- ") && strings.Contains(line, issueFamilyAnchor) {
			return line
		}
	}
	t.Fatalf("skills/working/SKILL.md has no paragraph opening %q: the issue family, its writers and who runs them are what it is read for", issueFamilyAnchor)
	return ""
}

// TestWorkingSkillIssueWritersAreTheUsers: the paragraph names the family's
// three writers and sends them to the user, in the same words the skill uses
// for every other command of that class (a coding agent's session is refused,
// so never run it — tell the user and let them run it).
func TestWorkingSkillIssueWritersAreTheUsers(t *testing.T) {
	page := issueFamilyParagraph(t)
	for _, rule := range issueWriterRules {
		if !strings.Contains(page, rule.text) {
			t.Errorf("the issue-family paragraph never says %s (%q): an agent that is told only that the command refuses runs it and is told no, and the record it writes is the user's to make:\n%s", rule.what, rule.text, page)
		}
	}
	// A rule about a class says nothing unless the paragraph names the class:
	// these are the three commands whose only writers they are.
	for _, writer := range []string{"`sync`", "`dismiss`", "`submit --ask`"} {
		if !strings.Contains(page, writer) {
			t.Errorf("the issue-family paragraph does not name %s among the commands the rule covers, so the rule has no subject:\n%s", writer, page)
		}
	}
	// A required phrase that a rewrite could drop while this test still passed
	// by matching nothing: the wording this rule replaced must fail it.
	replaced := "nothing is uploaded, `sync` and `dismiss` refuse, and `submit --ask` refuses."
	for _, rule := range issueWriterRules {
		if strings.Contains(replaced, rule.text) {
			t.Errorf("the rule %q is satisfied by the wording it replaced, so this check proves nothing", rule.text)
		}
	}
}
