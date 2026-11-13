package plugin_test

// The owner's ruling of 2026-11-13: **landing** is the name of merging a
// stalled pull request as-is and recording its leftovers as follow-ups, and
// the plugin carries a skill for it. The skill's whole value is the
// classification it teaches — which pull request may be landed, which needs a
// follow-up run, and which needs the person's word — so this file holds each
// class to the fact that decides it, and holds the one call the skill must
// never make (a merge) to the skill's own wording. Every command line it names
// is resolved against the CLI's own tree, and the plugin's lint
// (TestPluginSkillsLintClean) checks the flags on the same lines.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
)

const landingSkillPath = "skills/landing/SKILL.md"

// mergeOrForceRE matches the merge call written into a line and the push that
// overwrites the branch a person is about to merge — the two ways a landing
// skill hands its reader a call that is not the reader's to make.
var mergeOrForceRE = regexp.MustCompile(`(?i)(gh\s+pr\s+merge|fugaro\s+merge\b|git\s+push\b[^\n]*\s(--force\b|-f\b))`)

// landingSkillMD returns the landing skill's SKILL.md text.
func landingSkillMD(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(landingSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLandingSkillDefinesTheWord: the word the owner ruled on is written down
// where a reader meets it, with the two halves that make landing different
// from "merge": the leftovers become follow-up tasks, one line each, and no
// further fix round runs. A skill that taught the merge alone would have an
// agent land a pull request and leave what it still carried to nobody.
func TestLandingSkillDefinesTheWord(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"the definition", "To land a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks"},
		{"that the leftover list is one line each", "one line each, concrete enough to be a task"},
		{"that no further fix round runs", "instead of running another fix round"},
		{"that it is an action and not a state", "Landing is an action and not a fifth state"},
		{"where the four states are defined", "the `working` skill's"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): landing is a word the owner ruled on, and a skill that names it without its definition leaves the reader merging with no idea what happens to the findings the pull request still carries", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillClassifiesThreeWays: each of the three answers rests on a
// fact, and dropping any one of them makes the skill land something it must
// not (failing tests on the final commit, an open correctness finding, a
// security path) or follow up something that could have been landed.
func TestLandingSkillClassifiesThreeWays(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"the land class's condition", "it is ready, or it is stalled with its tests passing on the final commit and only leftover findings"},
		{"that a low-impact leftover may be landed", "a low-impact one"},
		{"that a product decision is the person's to own", "a product decision the person owns"},
		{"that a later plan task may be landed", "a later task of a plan"},
		{"the follow-up class's first trigger", "its tests fail on the final commit"},
		{"the follow-up class's second trigger", "a correctness or security problem that is not a product decision"},
		{"the follow-up class's third trigger", "the conflict with its base branch needs real reconciliation"},
		{"the security path the person must rule on", "credentials, auth, policy or a write guard"},
		{"that the security list is the repository's own", "`fugaro security paths` prints the list"},
		{"that a path is checked rather than judged", "not one you judge from the diff"},
		{"the unreadable-record case", "no row for it at all, a finding whose impact you cannot read"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): the three classes are the skill, and one of them losing its fact is a land that should have been a follow-up, or the other way round", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillNeverMerges: the merge is the one call the plugin may never
// make for the user (the skills' lint's `dangerous` rule, and the working
// skill's "ready is never merged"). The skill has to say so in its own words —
// an agent that read "land" as "merge it yourself" would make the call — and
// the file must name no merge command at all, however it is spelled.
func TestLandingSkillNeverMerges(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []string{
		"You never merge",
		"the person merges it",
		"let the person merge it in the provider's own interface",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("%s never says %q: landing hands the merge to the person, and an agent left without that sentence makes the call itself", landingSkillPath, want)
		}
	}
	// The two ways the call is written into a line, beyond the forge's own
	// command the lint's `dangerous` rule already refuses: a fugaro command
	// that merges, which does not exist, and a push that overwrites the branch
	// a person is about to merge.
	if m := mergeOrForceRE.FindString(skill); m != "" {
		t.Errorf("%s names %q: a skill never hands an agent a merge call, because the merge is the person's", landingSkillPath, m)
	}
}

// TestLandingSkillResolvesItsCommands: every command the skill names is one the
// released binary answers to, resolved here against the CLI's own tree rather
// than typed twice. The words after the first are the flags the line is written
// with, so a reviewer can read here what the lint reads there
// (TestPluginSkillsLintClean); resolution itself stops at the first flag, and
// the flags are that lint's to check.
func TestLandingSkillResolvesItsCommands(t *testing.T) {
	skill := landingSkillMD(t)
	root := cli.NewRootCmd()
	for _, c := range []struct {
		what string
		path []string // the command path, the words before the first flag or argument
		line string   // the line as the skill writes it, `fugaro ` included
	}{
		{"the pull request's runs", []string{"runs", "ls"}, "fugaro runs ls --pr N --repo <owner/name> --json"},
		{"the candidates, by state", []string{"runs", "ls"}, "fugaro runs ls --state stalled --json"},
		{"the last run's report", []string{"diagnose"}, "fugaro diagnose <run> --json"},
		{"the repository's security list", []string{"security", "paths"}, "fugaro security paths --check <path>"},
		{"the leftovers as tasks", []string{"followups"}, "fugaro followups <run>"},
		{"the follow-up run", []string{"run"}, "fugaro run --pr N --repo <owner/name>"},
		{"the follow-up's ruling", []string{"run"}, "fugaro run --owner-ruled"},
	} {
		if got := resolveSkillCommand(root, c.path...); !got.known() {
			t.Errorf("`%s` (%s) does not resolve in the CLI's own tree: the skill sends its reader to a command the released binary does not answer to", strings.Join(c.path, " "), c.what)
		}
		if !strings.Contains(skill, c.line) {
			t.Errorf("%s never writes %q (%s): this test resolves a line the skill does not carry, and the check would outlive the skill", landingSkillPath, c.line, c.what)
		}
	}
}

// TestLandingSkillResolvesATrivialConflictLocally: landing includes the
// conflict with the base branch when there is one, and the recipe is the one
// the repository's own rules allow: a worktree rather than the checkout, the
// base merged into the branch, a plain push and never a force, and the
// distinction that makes the class honest — a conflict needing two designs
// reconciled is not trivial, and is a follow-up's work.
func TestLandingSkillResolvesATrivialConflictLocally(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"the worktree", "git worktree add ../land-<N> <branch>"},
		{"the base merged in", "`git fetch origin <base>`, then `git merge origin/<base>`"},
		{"what a trivial resolution is", "a lock file, a generated file, two additions in different places"},
		{"the plain push", "git push origin HEAD"},
		{"that a push is never forced", "plain and never a force"},
		{"that a redesign is not a conflict to take", "one that needs two designs reconciled is a follow-up's work"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): landing a conflicted pull request without this is either a merge the branch cannot take or a redesign done in the dark", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillFilesTheLeftovers: the leftover findings reach a list, and
// the skill says both halves of how: the run's own deferrals are already in the
// pull request's status section and `fugaro followups` is what turns them into
// tasks — planned first, because `--launch` starts runs and spends — and a
// finding the record does not carry goes where the person keeps their list, one
// line each.
func TestLandingSkillFilesTheLeftovers(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"where the run's deferrals are listed", "Deferred (follow-ups)"},
		{"the command that files them", "fugaro followups <run>"},
		{"the cap on one call", "--max"},
		{"that the plan is read before the launch", "Read the plan back with the person before the launch"},
		{"that launching spends", "--launch` starts runs and spends money"},
		{"where a finding outside the record goes", "where the person keeps their list"},
		{"that a comment is never copied into a task", "Never copy a pull request comment into a task's text"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): a land whose leftovers reach no list is a land that lost the review's findings", landingSkillPath, want.what, want.text)
		}
	}
}
