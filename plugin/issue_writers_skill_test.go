package plugin_test

// The issue family's writers, and who may run each: `fugaro issue sync`
// uploads this machine's pending issues and `dismiss` sets one aside, and both
// are a coding agent's to run like any other command (D3425); `submit --ask`
// files the post on GitHub and stays a person's, because `--ask` is never an
// agent's (D3428, D3425's keep list). The CLI refuses that one in a session
// (agentMarker, internal/cli/issue_submit.go), and the refusal is a mitigation
// rather than a barrier: the guard reads an environment marker a session can
// clear. A skill sentence that only predicts the refusal leaves an agent free
// to run the command and be told no, so the rule the paragraph carries has to
// forbid the run and say whose command it is — which is the wording these
// tests pin, because nothing else in the plugin does.
//
// The rule is one writer now, and the other two must not be inside it: a
// clause that fences `sync` or `dismiss` again tells a session not to run
// commands it may run.
//
// internal/cli pins the other half: the guard itself
// (TestIssueSubmitRefusesInAgentSession).

import (
	"strings"
	"testing"
)

// issueFamilyAnchor opens the one bullet of skills/working/SKILL.md that
// carries the issue family.
const issueFamilyAnchor = "The rest of the family is that same record from other angles"

// The rule is one sentence of that bullet, and that sentence is the whole
// subject of the checks below. The bullet names `submit` once before it —
// "`show`, `dismiss` and `submit` take either" — so a check over the whole
// bullet would find that name whatever the rule went on to cover, and a
// rewrite that dropped the writer from the rule would pass it.
const (
	ruleOpensOn  = "What it never runs is"
	ruleEndsWith = "never run it"
)

// issueWriterRules is what the rule clause must say about the one command a
// session must not run.
var issueWriterRules = []struct{ what, text string }{
	{"that the session never runs it", "never run it"},
	{"whose command it is", "the user's own command at their own terminal"},
}

// issueFencedWriter is the class the rule is about: the family's writer a
// session must not run, by the name the clause has to carry.
var issueFencedWriter = "`submit --ask`"

// issueLiftedWriters are the family's writers a session may run since D3425.
// They must not be inside the rule clause: one that fences them again tells a
// session not to run commands it may run.
var issueLiftedWriters = []string{"`sync`", "`dismiss`"}

// issueLiftedRule is the other half the bullet has to carry: what a session
// may run, in the words that say so.
const issueLiftedRule = "It may also run `sync` and `dismiss`"

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

// issueWriterViolations is every part of the rule a clause gets wrong, as
// phrases that read out of the error message.
func issueWriterViolations(clause string) []string {
	var out []string
	for _, rule := range issueWriterRules {
		if !strings.Contains(clause, rule.text) {
			out = append(out, rule.what)
		}
	}
	if !strings.Contains(clause, issueFencedWriter) {
		out = append(out, issueFencedWriter+" among the writers it covers")
	}
	for _, writer := range issueLiftedWriters {
		if strings.Contains(clause, writer) {
			out = append(out, writer+" fenced by the rule, though a session may run it")
		}
	}
	return out
}

// TestWorkingSkillIssueSubmitIsTheUsers: the clause names the one writer a
// session must not run — `submit --ask`, whose post goes on GitHub — and sends
// it to the user, in the same words the skill uses for every other command of
// that class (a coding agent's session is refused, so never run it — tell the
// user and let them run it). A writer that reaches no clause is one an agent
// is left free to run: the CLI's guard reads a marker its session can clear.
// And `sync` and `dismiss` must reach no clause, because D3425 lifted them:
// the bullet carries what a session may run instead.
func TestWorkingSkillIssueSubmitIsTheUsers(t *testing.T) {
	page := issueFamilyParagraph(t)
	clause, ok := issueWriterClause(page)
	if !ok {
		t.Fatalf("the issue-family paragraph carries no rule clause (%q … %q): what a session may not run, and whose that command is, is what it is read for:\n%s", ruleOpensOn, ruleEndsWith, page)
	}
	for _, violation := range issueWriterViolations(clause) {
		t.Errorf("the rule clause is wrong about %s:\n%s", violation, clause)
	}
	if !strings.Contains(page, issueLiftedRule) {
		t.Errorf("the paragraph never says what a session may run (%q): the rule that fenced `sync` and `dismiss` is gone, and a reader has to be told they are an agent's now:\n%s", issueLiftedRule, page)
	}
	// A clause-wide check a rewrite could satisfy by saying nothing: the
	// wording this rule replaced, a rule that stopped naming `submit --ask`,
	// and a rule that fenced the two lifted writers again must all fail it.
	for _, hostile := range []struct{ what, page string }{
		{"the rule that fenced all three writers", "What it never runs is the family that writes the project's store: `sync`, `dismiss` and `submit --ask` are refused in a session, so never run them — each is the user's own command at their own terminal, so never run it."},
		{"a rule that leaves `submit --ask` out", "What it never runs is the command that files the post on GitHub: it is the user's own command at their own terminal, so never run it."},
		{"a rule that fences the lifted writers again", "What it never runs is `sync`, `dismiss` and `submit --ask`: each is the user's own command at their own terminal, so never run it."},
	} {
		hostileClause, ok := issueWriterClause(hostile.page)
		if ok && len(issueWriterViolations(hostileClause)) == 0 {
			t.Errorf("%s is not caught by these checks, so the pass above proves nothing:\n%s", hostile.what, hostile.page)
		}
	}
}
