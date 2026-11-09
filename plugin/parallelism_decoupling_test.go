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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	decouplingReferencePath = "skills/parallelism/reference/decoupling.md"
	decouplingHeading       = "## Break dependencies with a contract first"
)

// parallelismSkillFiles returns the parallelism skill's own files, SKILL.md and
// its reference/*.md, keyed by their repository-relative path.
func parallelismSkillFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, path := range skillFiles(t, filepath.Join("skills", "parallelism")) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(path)] = string(data)
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
	skill, ok := parallelismSkillFiles(t)[parallelismSkillPath]
	if !ok {
		t.Fatalf("the parallelism skill has no %s", parallelismSkillPath)
	}
	return decouplingSection(skill) + "\n" + decouplingReferenceText(t)
}

// decouplingReferenceText reads the reference the section points at.
func decouplingReferenceText(t *testing.T) string {
	t.Helper()
	ref, ok := parallelismSkillFiles(t)[decouplingReferencePath]
	if !ok {
		t.Fatalf("the parallelism skill has no %s: the per-edge test and the change-request flow are longer than SKILL.md's section holds, so they live in the file the section points at", decouplingReferencePath)
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
	skill, ok := parallelismSkillFiles(t)[parallelismSkillPath]
	if !ok {
		t.Fatalf("the parallelism skill has no %s", parallelismSkillPath)
	}
	section := decouplingSection(skill)
	if section == "" {
		t.Fatalf("%s has no %q section", parallelismSkillPath, decouplingHeading)
	}
	if !strings.Contains(section, "reference/decoupling.md") {
		t.Errorf("%s's decoupling section does not point at `reference/decoupling.md`: the contract task's contents, the integration task and the change-request flow are there, and a reader who is not sent to the file never finds them", parallelismSkillPath)
	}
	const question = "does B need A's interface or A's behaviour?"
	if !strings.Contains(section, question) {
		t.Errorf("%s's decoupling section never asks %q: that question is the pass", parallelismSkillPath, question)
	}
}

// TestParallelismSkillContractTaskRigor: a contract is the one task both sides
// build against and the dearest thing to get wrong late, so it is reviewed at
// least as strictly as its strictest consumer (D1819). `fugaro run --mode` is
// that "at least": it may raise the mode a run resolves and never lower it. The
// mode a task resolves is read from the settings and matched to the task's
// footprint by the agent itself (F18), so the text has to name the read — and
// name it against the repository: an unaimed read covers the installation's own
// scopes and never opens the repository's fugaro.yaml, so it would report a mode
// that repository's runs do not resolve.
//
// The same table says what the mode does to the pass (D1818), and the two rows
// that decide whether the pass runs at all are the two a later edit is likeliest
// to smooth away: Hotfix skips it, and Critical takes a contract only where it is
// fully specified. Both are pinned here rather than left to CD5, which owns the
// table's agreement with `internal/rigor` and not the pass's own behaviour.
func TestParallelismSkillContractTaskRigor(t *testing.T) {
	text := decouplingText(t)
	for _, want := range []string{
		"the highest rigor of the tasks that consume it",
		"fugaro run --mode",
		"fugaro settings rigor.default rigor.areas --repo",
		`"scope": "repo:<owner/name>"`,
		"fugaro review --pr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a contract can be launched below the rigor of what depends on it", want)
		}
	}
	for _, want := range []string{
		"skip the pass",
		"fully specified contracts",
		"the end-to-end test that proves the wiring",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a mode's own behaviour towards the pass is gone: Hotfix skips it and Critical takes a contract only where it is fully specified", want)
		}
	}
}

// TestFollowupInstructionsNameTheContractFile: a task already launched against
// an amended contract cannot be reached by the plan, so it is driven by a
// follow-up run (D1812). The instructions are trusted text that must not relay
// anything, so they name the contract file on the base branch and ask the run
// to merge the base and conform — never paste the contract's own text, which
// would put plan text into instructions the runner trusts.
//
// The go-ahead rule is pinned here too, because this is where the pass's runs
// are: every launch it calls for is a paid job under the same caps as any other
// (F11, D1820), so the pass proposes them and the user's yes comes first. Text
// that read as an order to launch would spend without asking.
func TestFollowupInstructionsNameTheContractFile(t *testing.T) {
	text := decouplingText(t)
	for _, want := range []string{
		"fugaro run --pr",
		"name the contract file as it is on `origin/<base>`",
		"never paste its text",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a launched dependent is never told its contract changed", want)
		}
	}
	for _, want := range []string{
		"until the user says yes",
		"only after the user's yes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decoupling text never says %q, so a contract, an integration task or a follow-up can be launched without the user's go-ahead: the pass proposes its runs and adds no way to spend", want)
		}
	}

	// The go-ahead rule is read here on the section alone, never on the section
	// joined with the reference: both files carry a copy of the rule, so a pin
	// against the joined text stays green with the section's own copy gone —
	// which is the copy the planner reads first, and the only one a planner who
	// stops at the section ever sees. The section's enumeration then has to be
	// the whole of what the yes covers: a kind of run it leaves out is one
	// launched on a yes that never covered it, and the independent review is a
	// paid job like the rest.
	section := decouplingSection(parallelismSkillFiles(t)[parallelismSkillPath])
	for _, want := range []string{
		"proposes and never launches",
		"until the user says yes",
		"independent review",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's decoupling section never says %q, so the section a planner reads first no longer carries the go-ahead rule in full: a contract task, an integration task, an independent review or a follow-up can be launched on text the section never gated", parallelismSkillPath, want)
		}
	}
}

// TestParallelismSkillKeepsAContractFileToItsChangeRequest: the files a contract
// and its changes own are the ones both sides build against, so a task that has
// to change one raises the change request instead of editing it, and a reviewer
// treats any other task's diff to one as a contract change (F10). The rule has
// to be written as that obligation: a sentence that reported the boundary as
// unguarded — that nothing checks a run's real changes against its footprint —
// is the one an agent reads as "so nothing stops me editing a merged contract",
// which widens its scope past the plan it was given.
func TestParallelismSkillKeepsAContractFileToItsChangeRequest(t *testing.T) {
	ref := decouplingReferenceText(t)
	for _, want := range []string{
		"raises the change request instead of editing it",
		"treats it as a contract change",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("the decoupling reference never says %q, so a task that changes a contract on its own authority is neither stopped by the rule nor caught by a reviewer", want)
		}
	}
	if strings.Contains(ref, "nothing checks") {
		t.Errorf("the decoupling reference reports the footprint as unguarded (%q), which an agent reads as permission to edit a contract file it was not given", "nothing checks")
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
	for _, own := range []string{"Belong", "dimipaun"} {
		if strings.Contains(text, own) {
			t.Errorf("the decoupling text names %q, which is this installation's own organisation: the pass ships to every user", own)
		}
	}

	// The checkout's own trees, read off the disk rather than listed here: a
	// pass that named one of them would be written for this repository. docs/
	// is left out on purpose — it is where a contract document lives in any
	// repository, and the pass points at `docs/contracts/`.
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	trees := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "docs" {
			continue
		}
		trees++
		if strings.Contains(text, e.Name()+"/") {
			t.Errorf("the decoupling text names %s/, a tree of this repository: the pass ships to every user, and project-specific tuning goes in project or repo settings", e.Name())
		}
	}
	if trees == 0 {
		t.Fatal("no tree of the checkout was read, so this check cannot report anything")
	}
}
