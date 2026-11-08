package plugin_test

// The decoupling pass: what the `parallelism` skill tells a planner to do when
// a dependency between two tasks is only an interface one
// (docs/contract-first-decoupling, D1810, D1816, D1818, D1819, D1821, D1822).
//
// There is no planner code behind the pass — the planner is the user's own
// agent — so the procedure is skill text and the only thing a test can hold is
// that the text carries it. These tests read SKILL.md's decoupling section and
// the reference it points at, and pin the rules a later edit could otherwise
// drop silently: the per-edge question, the rigor a contract task is launched
// at, the follow-up run that tells a launched dependent its contract changed,
// the CLI as the graph's only reader, and that the text stays generic.
//
// What they read is the text the pass adds. SKILL.md's graph section carries
// this repository's own paths in its example and predates the pass, so it is
// not a second thing to hold to a genericness rule.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	parallelismSkill    = "skills/parallelism/SKILL.md"
	decouplingReference = "skills/parallelism/reference/decoupling.md"
	decouplingHeading   = "## Break dependencies with a contract first"
)

// parallelismSkillFiles returns the parallelism skill's own files, SKILL.md and
// its reference/*.md, keyed by their repository-relative path.
func parallelismSkillFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(filepath.Join("skills", "parallelism"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(path)] = string(data)
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no parallelism skill files: %v", err)
	}
	return files
}

// decouplingSection returns SKILL.md's decoupling section, or "" when the
// heading is not there.
func decouplingSection(skill string) string {
	_, rest, ok := strings.Cut(skill, "\n"+decouplingHeading+"\n")
	if !ok {
		return ""
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	return section
}

// decouplingText is the pass's own text: SKILL.md's decoupling section and the
// reference file the section points at.
func decouplingText(t *testing.T) string {
	t.Helper()
	skill, ok := parallelismSkillFiles(t)[parallelismSkill]
	if !ok {
		t.Fatalf("the parallelism skill has no %s", parallelismSkill)
	}
	return decouplingSection(skill) + "\n" + decouplingReferenceText(t)
}

// decouplingReferenceText reads the reference the section points at.
func decouplingReferenceText(t *testing.T) string {
	t.Helper()
	ref, ok := parallelismSkillFiles(t)[decouplingReference]
	if !ok {
		t.Fatalf("the parallelism skill has no %s: the per-edge test and the change-request flow are longer than SKILL.md's section holds, so they live in the file the section points at", decouplingReference)
	}
	return ref
}

// TestParallelismSkillHasDecouplingSection: the pass has to be findable from the
// skill a planner is already reading, and the section has to carry the one
// question the whole pass turns on — whether the dependent needs the other
// task's interface or its behaviour. A section that is missing, or that names
// no reference, leaves a planner breaking edges on a hunch or never hearing of
// the procedure at all.
func TestParallelismSkillHasDecouplingSection(t *testing.T) {
	skill, ok := parallelismSkillFiles(t)[parallelismSkill]
	if !ok {
		t.Fatalf("the parallelism skill has no %s", parallelismSkill)
	}
	section := decouplingSection(skill)
	if section == "" {
		t.Fatalf("%s has no %q section", parallelismSkill, decouplingHeading)
	}
	if !strings.Contains(section, "reference/decoupling.md") {
		t.Errorf("%s's decoupling section does not point at `reference/decoupling.md`: the contract task's contents, the integration task and the change-request flow are there, and a reader who is not sent to the file never finds them", parallelismSkill)
	}
	const question = "does B need A's interface or A's behaviour?"
	if !strings.Contains(section, question) {
		t.Errorf("%s's decoupling section never asks %q: that question is the pass", parallelismSkill, question)
	}
}

// TestParallelismSkillContractTaskRigor: a contract is the one task both sides
// build against and the dearest thing to get wrong late, so it is reviewed at
// least as strictly as its strictest consumer (D1819). `fugaro run --mode` is
// that "at least": it may raise the mode a run resolves and never lower it. The
// mode a task resolves is read from the settings and matched to the task's
// footprint by the agent itself (F18), so the text has to name the read.
func TestParallelismSkillContractTaskRigor(t *testing.T) {
	text := decouplingText(t)
	for _, want := range []string{
		"the highest rigor of the tasks that consume it",
		"fugaro run --mode",
		"fugaro settings rigor.default rigor.areas --json",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a contract can be launched below the rigor of what depends on it", want)
		}
	}
}

// TestFollowupInstructionsNameTheContractFile: a task already launched against
// an amended contract cannot be reached by the plan, so it is driven by a
// follow-up run (D1812). The instructions are trusted text that must not relay
// anything, so they name the contract file on the base branch and ask the run
// to merge the base and conform — never paste the contract's own text, which
// would put plan text into instructions the runner trusts.
func TestFollowupInstructionsNameTheContractFile(t *testing.T) {
	text := decouplingText(t)
	for _, want := range []string{
		"fugaro run --pr",
		"names the contract file",
		"never pastes its text",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a launched dependent is never told its contract changed", want)
		}
	}
}

// TestParallelismSkillNamesTheCLIForTheGraph: plan text is untrusted, so the
// CLI is the graph's only reader (D1821). A skill that parsed the artifact
// itself would be a second reader that can disagree with the validator, and the
// one the agent acts on.
func TestParallelismSkillNamesTheCLIForTheGraph(t *testing.T) {
	text := decouplingText(t)
	for _, want := range []string{
		"fugaro plan graph",
		"never parse the graph",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q: the graph has to go through the CLI, not through the agent's own reading of the file", want)
		}
	}
}

// TestParallelismSkillIsGeneric: the pass ships to every user, so its text
// speaks of the plan's tasks and the CLI and of nothing of ours. A repository
// or organisation named here would be an assumption no other user's plan makes,
// which is the one thing the spec forbids in the bundled skill: project-specific
// tuning goes in project or repo settings, never in Fugaro's own text.
func TestParallelismSkillIsGeneric(t *testing.T) {
	text := decouplingText(t)
	for _, own := range []string{
		"Belong", "dimipaun",
		"internal/", "cmd/fugaro", "console/", "mobile/", "website/", "plugin/skills",
	} {
		if strings.Contains(text, own) {
			t.Errorf("the decoupling text names %q, which is one repository's own: the pass ships to every user", own)
		}
	}
}
