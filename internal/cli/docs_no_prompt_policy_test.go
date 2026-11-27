package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The no-prompt policy folder reserves D1540–D1559; its reconciliation holds one
// row per decision, and a row with no answer is a decision nobody has made.
const npDocReconciliation = "docs/no-prompt-policy/reconciliation.md"

const npDocREADME = "docs/no-prompt-policy/README.md"

var npDecisionRow = regexp.MustCompile(`(?m)^\| \*\*D(\d+)\*\* \|(.*)$`)

func TestDocsNoPromptPolicyEveryDecisionIsAnsweredInRangeAndUnique(t *testing.T) {
	doc := readDoc(t, npDocReconciliation)
	seen := map[int]bool{}
	for _, m := range npDecisionRow.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if n < 1540 || n > 1559 {
			t.Errorf("%s: D%d is outside the reserved block D1540–D1559", npDocReconciliation, n)
		}
		if seen[n] {
			t.Errorf("%s: D%d has two rows", npDocReconciliation, n)
		}
		seen[n] = true
		cells := strings.Split(m[2], "|")
		answer := strings.TrimSpace(cells[len(cells)-2])
		if answer == "" || strings.Contains(answer, "(pending)") || !strings.Contains(answer, "2026-11-07") {
			t.Errorf("%s: D%d has no dated answer (%q)", npDocReconciliation, n, answer)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("%s: no decision rows found", npDocReconciliation)
	}
}

// npPolicyCopies is every document that reaches a person or an agent before the
// code does: the repository guidance, the two rules that load on a Terraform or
// credentials path, the security document, and the page generated from it. The
// spec's enforcement section asks these documents to state the policy and point
// at this folder as its authority, and to tell a coding agent not to add a
// confirmation step "for safety" -- which is the one instruction in this policy
// that is aimed at the reader rather than at the code, so nothing in the tree
// fails if it quietly goes missing.
//
// The cover-table rule is the copy the policy rewrites rather than adds: D1545
// (ruled 2026-11-07) makes those tables a tripwire that records and warns, so a
// rule file still saying a plan outside them is refused, or is uncovered until
// a row is added, tells an agent the opposite of what the gate does -- and an
// agent that believes it will widen a table to make a plan pass, which is the
// one thing the tripwire needs a person to do on purpose.
var npPolicyCopies = []struct {
	path  string
	wants []string
}{
	{"CLAUDE.md", []string{
		"**Fugaro asks for nothing.**",
		"no y/N, no typing a name back",
		"at the owner's terminal or in an agent's session",
		"no command may hang on a closed stdin",
		"`--ask` is the whole of the exemption",
		"never an agent session, never `--json`",
		"fails at once with no terminal rather than waiting",
		"docs/no-prompt-policy/",
		`Do not add a confirmation step "for safety".`,
	}},
	{".claude/rules/terraform.md", []string{
		"are a **tripwire, not a fence**",
		"that the tables do not carry **is applied**",
		"go into that step's audit record",
		"`fugaro doctor` prints the newest warned record",
		"a delete or replace of an irreplaceable type, or of one nobody has classified, refuses",
		"that review is the **pre-apply control**",
		"docs/no-prompt-policy/",
	}},
	{".claude/rules/credentials-service.md", []string{
		"The cover tables no longer refuse a grant they do not carry",
		"they warn, the plan applies and the finding goes into the step's audit record",
		"docs/no-prompt-policy/",
	}},
	{"SECURITY.md", []string{
		"**The managed gate records what it used to refuse, and the record is what a reader has afterwards.**",
		"The gate belongs to `fugaro update` on a managed project",
		"`fugaro setup` applies its Terraform with no gate at all",
		"a project that is not managed keeps the confirmations and the coding agent's refusals it had, and leaves no record",
		"that gate is audit-only for what a plan *contains*",
		"a delete or replace of an **irreplaceable or unclassified** type refuses",
		"the identity that applied the plan is the identity that could delete the record",
		"`fugaro doctor` prints the newest record that carries warnings, over the two days it reads (today and yesterday)",
		"is the pre-apply control this decision rests on",
	}},
}

// npPolicyRetired are wordings the policy retires, each with the fact that
// rules it out. A copy that goes back to one tells a reader a refusal exists
// where it does not, which is the failure this whole folder is about.
var npPolicyRetired = []struct {
	path   string
	phrase string
	why    string
}{
	{
		".claude/rules/terraform.md",
		"is not covered until you add it to",
		"it says a plan outside the tables is refused: D1545 makes the tables a tripwire, so an out-of-table type or grant applies, is recorded and is printed, and the row a person adds is what keeps the record readable rather than what admits the plan",
	},
	{
		".claude/rules/terraform.md",
		"decides what one `fugaro update` confirmation covers",
		"there is no confirmation to cover: the flags that stood in for one are gone (T11) and the gate's own note is what a step prints",
	},
	{
		"SECURITY.md",
		"the gate refuses a plan",
		"what the gate refuses is the integrity class and an irreplaceable or unclassified removal; everything else it records and warns, and a sentence saying otherwise is read as a boundary the product does not have",
	},
	{
		".claude/rules/terraform.md",
		"a delete or replace of a data-bearing type, or of one nobody has classified, refuses",
		"D3430 moves the refusal off D1551's type question and onto the class: a data-bearing type the same apply makes again (an artifact registry repository, a service account Fugaro made) is applied and recorded, so a rule file still saying a data-bearing removal refuses sends an agent widening a table for a refusal that is not there",
	},
	{
		"SECURITY.md",
		"a delete or replace of a **data-bearing or unclassified** type refuses",
		"the removal rule refuses the irreplaceable class, not every data-bearing type: D3430 makes an artifact registry repository and a Fugaro-made service account recreatable, and a security document promising a refusal a reader cannot find is the failure this folder exists to stop",
	},
}

// TestDocsNoPromptPolicyIsStatedWhereAgentsReadIt: the policy is in the
// documents that reach a person or an agent before the code, each points at
// this folder as its authority, and none of them has gone back to a wording the
// policy retires.
func TestDocsNoPromptPolicyIsStatedWhereAgentsReadIt(t *testing.T) {
	for _, copy := range npPolicyCopies {
		text := flattenDoc(readDoc(t, copy.path))
		for _, want := range copy.wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q: the no-prompt policy is stated where this document's readers need it (docs/no-prompt-policy/spec.md, Enforcement)", copy.path, want)
			}
		}
	}
	for _, retired := range npPolicyRetired {
		if strings.Contains(flattenDoc(readDoc(t, retired.path)), retired.phrase) {
			t.Errorf("%s says %q: %s", retired.path, retired.phrase, retired.why)
		}
	}
}

// TestDocsNoPromptPolicyStatusLineMoves: the folder's own status line carries
// the date it was last read and hands the current count to docs/status.md,
// which is where the count lives between reads. A line dated before a task
// merged under it is a reader's stale count, and the folder's row in
// docs/status.md is not a substitute: the line is what a reader of the folder
// sees first.
func TestDocsNoPromptPolicyStatusLineMoves(t *testing.T) {
	readme := readDoc(t, npDocREADME)
	line := ""
	for _, l := range strings.Split(readme, "\n") {
		if strings.HasPrefix(l, "**Status as of ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("%s carries no `**Status as of ` line: the folder's status has to be readable from its front page", npDocREADME)
	}
	for _, want := range []string{"in progress, 14/17 merged", "docs/status.md"} {
		if !strings.Contains(line, want) {
			t.Errorf("%s's status line does not say %q: a reader of the folder is sent to the count that is current (%s)", npDocREADME, want, docsStatusRow(t))
		}
	}
	if !strings.Contains(line, "2026-11-14") {
		t.Errorf("%s's status line is not dated 2026-11-14 (%q): the date is what tells a reader how stale the count under it is", npDocREADME, line)
	}
}

// docsStatusRow reads this spec's row out of docs/status.md, so a failure names
// the table the count is handed to rather than an assertion about it. The row
// is read, never asserted on: T13 does not write that file.
func docsStatusRow(t *testing.T) string {
	t.Helper()
	for _, l := range strings.Split(readDoc(t, "docs/status.md"), "\n") {
		if strings.HasPrefix(l, "| no-prompt-policy |") {
			return l
		}
	}
	return "docs/status.md carries no no-prompt-policy row"
}
