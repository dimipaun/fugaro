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
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
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
		{"that a path is checked rather than guessed", "not one you guess at from a file's name"},
		{"that the change's own paths are checked", "Check the paths the change itself touches"},
		{"where those paths are read", "`changed_files.paths` in the run's row, step 1"},
		{"the fallback when the row carries none", "when the row carries none because the run died before it recorded them, the branch's own diff against the base"},
		{"that a finding is not needed for the question", "whether or not a leftover finding sits on it"},
		{"the unreadable-record case", "no row for it at all, a finding whose impact you cannot read"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): the three classes are the skill, and one of them losing its fact is a land that should have been a follow-up, or the other way round", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillReadsTheChangeRunsRecord: the listing a reader classifies
// from carries a finished independent review job's run as well, usually the
// newest row, and that run built nothing — no test records, no findings, no
// deferrals — so a reader taking "the last run" for the class's evidence
// measures a land against a record that has none of it. The skill has to say
// which row decides and which one is only a second opinion, and it has to name
// the paths the run changed, because the security question below is asked of
// the change and not only of the findings.
func TestLandingSkillReadsTheChangeRunsRecord(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"that a review job's run is listed too", "a finished independent review job's own run, which is usually the newest of them"},
		{"how to tell it apart", "A review job's run is the one whose `outcome` is `reviewed`"},
		{"that it is not the run a land is decided from", "it is not the run a land is decided from"},
		{"what it carries instead", "it carries no test records, no findings and no deferrals"},
		{"which row decides", "Take those from the run that built the change, the newest row whose `outcome` is not `reviewed`"},
		{"that its verdict still counts", "as the verdict of a reader who did not write the change"},
		{"the paths the run changed", "`runs[].changed_files.paths`, the JSON field of the paths the run changed"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): the row a land is read from is the run that built the change, and a class read off a review job's run is read off a record that carries none of its conditions", landingSkillPath, want.what, want.text)
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
// base merged into the branch, the tests run on the merge commit before
// anything is pushed (the class was read from tests passing on the final
// commit, and a merge commit nobody tested is a head that condition no longer
// covers), a plain push and never a force, and the distinction that makes the
// class honest — a conflict needing two designs reconciled is not trivial, and
// is a follow-up's work.
func TestLandingSkillResolvesATrivialConflictLocally(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"the worktree", "git worktree add ../land-<N> <branch>"},
		{"the base merged in", "`git fetch origin <base>`, then `git merge origin/<base>`"},
		{"what a trivial resolution is", "a lock file, a generated file, two additions in different places"},
		{"that the checks run on the merge commit", "run the repository's own checks on the merge commit"},
		{"the checks themselves", "fugaro verify build\nfugaro verify test"},
		{"that an untested head voids the class", "a merge commit nobody tested is a head that condition no longer covers"},
		{"that a refused resolution is not a land", "A resolution the tests refuse is not a trivially resolved conflict"},
		{"that the push waits for the tests", "When they pass, push the resolution"},
		{"the plain push", "git push origin HEAD"},
		{"that a push is never forced", "plain and never a force"},
		{"that a redesign is not a conflict to take", "one that needs two designs reconciled is a follow-up's work"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): landing a conflicted pull request without this is either a merge the branch cannot take, or a merge the class's own condition no longer covers", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillFilesTheLeftovers: the leftover findings reach a list, and the
// skill says both halves of how — and which list holds which, because the two
// are not the same one. `fugaro followups` reads the run's deferrals, which the
// pull request's status section carries and which `--launch` files as tasks
// (planned first, since it starts runs and spends); a finding the mode tolerated
// in a round that ended without a stop is in no deferral at all, so it is never
// filed by that command and goes one line each where the person keeps their
// list, like a finding the record does not carry.
func TestLandingSkillFilesTheLeftovers(t *testing.T) {
	skill := landingSkillMD(t)
	for _, want := range []struct{ what, text string }{
		{"where the run's deferrals are listed", "Deferred (follow-ups)"},
		{"the command that files them", "fugaro followups <run>"},
		{"that it reads that list and no other", "That list and no other is what `fugaro followups <run>` reads"},
		{"that it waits for the merge", "and it runs only once the pull request is merged"},
		{"the cap on one call", "--max"},
		{"that the plan is read before the launch", "Read the plan back with the person before the launch"},
		{"that launching spends", "--launch` starts runs and spends money"},
		{"where the tolerated leftovers are read", "the report's own Tolerated table"},
		{"that they are in no deferral", "leaves them there and not among the deferrals"},
		{"that followups files none of them", "`fugaro followups` files nothing for them"},
		{"where a tolerated one goes", "Write them one line each where the person keeps their list"},
		{"where a finding outside the record goes", "So does a finding the record does not carry at all"},
		{"that a comment is never copied into a task", "Never copy a pull request comment into a task's text"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("%s never says %s (%q): a land whose leftovers reach no list is a land that lost the review's findings", landingSkillPath, want.what, want.text)
		}
	}
}

// TestLandingSkillNamesTheFieldsTheRecordsCarry: the report the skill reads is
// two records under one call, and the commit each half covered is spelled
// differently in them — `head_sha` in the verify record and `head` in the
// review's. A skill that named one for the other would still read as prose and
// still be wrong, so each claim is held to the struct that carries it: the
// paths are the ones the cli's claims table holds to the same structs, and a
// rename on either side fails here too.
func TestLandingSkillNamesTheFieldsTheRecordsCarry(t *testing.T) {
	skill := landingSkillMD(t)
	verifyRecord := reflect.TypeOf(verify.Record{})
	review := reflect.TypeOf(runstore.ReviewSummary{})
	for _, c := range []struct {
		claim string
		typ   reflect.Type
		field string
	}{
		{"verify[].head_sha", verifyRecord, "head_sha"},
		{"reviews[].verdict", review, "verdict"},
		{"reviews[].head", review, "head"},
	} {
		if !strings.Contains(skill, "`"+c.claim+"`") {
			t.Errorf("%s no longer names `%s`: this test holds a claim the skill does not make", landingSkillPath, c.claim)
			continue
		}
		if !hasJSONTag(c.typ, c.field) {
			t.Errorf("%s claims `%s`, but %s has no %q field: the skill reads a field the record does not carry", landingSkillPath, c.claim, c.typ, c.field)
		}
	}
}

// hasJSONTag reports whether a struct or slice-of-struct carries name as a
// json tag.
func hasJSONTag(t reflect.Type, name string) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); tag == name {
			return true
		}
	}
	return false
}
