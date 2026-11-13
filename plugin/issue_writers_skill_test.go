package plugin_test

// The issue family's three writers are a person's own and only a person's:
// `fugaro issue sync` uploads to the project's store, `dismiss` sets an issue
// aside and `submit --ask` uploads and files on GitHub. The CLI refuses all
// three in a coding agent's session (agentMarker, internal/cli/issue_sync.go,
// issue_dismiss.go, issue_submit.go), and that refusal is a mitigation rather
// than a barrier: the guard reads an environment marker a session can clear.
// A skill sentence that only predicts the refusal leaves an agent free to run
// the command and be told no, so the rule the paragraph carries has to forbid
// the run and say whose command it is — which is the wording these tests pin,
// because nothing else in the plugin does.
//
// internal/cli pins the other half: the guard itself
// (TestSyncRefusesInAgentSession, TestIssueSubmitRefusesInAgentSession).

import (
	"strings"
	"testing"
)

// issueFamilyAnchor opens the one bullet of skills/working/SKILL.md that
// carries the issue family.
const issueFamilyAnchor = "The rest of the family is that same record from other angles"

// The rule is one sentence of that bullet, and that sentence is the whole
// subject of the checks below. The bullet names `dismiss` once before it —
// "`show`, `dismiss` and `submit` take either" — so a check over the whole
// bullet would find that name whatever the rule went on to cover, and a
// rewrite that dropped a writer from the rule would pass it.
const (
	ruleOpensOn  = "writes none of it:"
	ruleEndsWith = "never run them"
)

// issueWriterRules is what the rule clause must say about the three commands a
// session writes nothing with.
var issueWriterRules = []struct{ what, text string }{
	{"that the session never runs them", "never run them"},
	{"whose commands they are", "the user's own command at their own terminal"},
}

// issueWriters is the class the rule is about: the family's writers, by the
// names the clause has to carry.
var issueWriters = []string{"`sync`", "`dismiss`", "`submit --ask`"}

// issueFamilyParagraph is the bullet, whole.
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

// issueWriterClause is a bullet's rule sentence — from ruleOpensOn to the
// first sentence end after it, ruleEndsWith included on the way — and whether
// the bullet carries one at all.
func issueWriterClause(page string) (string, bool) {
	_, rest, ok := strings.Cut(page, ruleOpensOn)
	if !ok || !strings.Contains(rest, ruleEndsWith) {
		return "", false
	}
	if end := strings.Index(rest, ". "); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// issueWriterViolations is every part of the rule a clause is missing, as
// phrases that read out of the error message.
func issueWriterViolations(clause string) []string {
	var out []string
	for _, rule := range issueWriterRules {
		if !strings.Contains(clause, rule.text) {
			out = append(out, rule.what)
		}
	}
	for _, writer := range issueWriters {
		if !strings.Contains(clause, writer) {
			out = append(out, writer+" among the writers it covers")
		}
	}
	return out
}

// TestWorkingSkillIssueWritersAreTheUsers: the clause names all three of the
// family's writers and sends them to the user, in the same words the skill
// uses for every other command of that class (a coding agent's session is
// refused, so never run it — tell the user and let them run it). The writer
// that reaches no clause is the one an agent is left free to run: the CLI's
// guard reads a marker its session can clear.
func TestWorkingSkillIssueWritersAreTheUsers(t *testing.T) {
	page := issueFamilyParagraph(t)
	clause, ok := issueWriterClause(page)
	if !ok {
		t.Fatalf("the issue-family paragraph carries no rule clause (%q … %q): what a session may not run, and whose those commands are, is what it is read for:\n%s", ruleOpensOn, ruleEndsWith, page)
	}
	for _, missing := range issueWriterViolations(clause) {
		t.Errorf("the rule clause never says %s:\n%s", missing, clause)
	}
	// A clause-wide check a rewrite could satisfy by saying nothing: the
	// wording this rule replaced, and a rule that stopped covering one of its
	// three writers, must both fail it.
	for _, hostile := range []struct{ what, page string }{
		{"the clause it replaced", "writes none of it: nothing is uploaded, `sync` and `dismiss` refuse, and `submit --ask` refuses. So what a `list` or a `show` prints for you is this machine's view alone."},
		{"a rule that leaves `dismiss` out", "writes none of it: nothing is uploaded, and `sync` and `submit --ask` are refused in a session, so never run them — each is the user's own command at their own terminal."},
	} {
		hostileClause, ok := issueWriterClause(hostile.page)
		if ok && len(issueWriterViolations(hostileClause)) == 0 {
			t.Errorf("%s is not caught by these checks, so the pass above proves nothing:\n%s", hostile.what, hostile.page)
		}
	}
}
