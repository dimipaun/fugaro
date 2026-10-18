package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDocsManagedHardStopDesignIsWritten (plan Task 6.0, SECURITY-SENSITIVE):
// the billing hard stop is the plan's unscheduled Task 6.0, whose own Files
// line is "a design of its own, if the owner wants one" — and the owner
// approved building it on 2026-10-18. The plan text fixes no mechanism, so the
// design is the deliverable: it must price the mechanism options, state a
// recommendation, answer the four questions the plan leaves open (what stops
// the money, who can undo it, how a false positive is recovered, how it is
// proven without a live cloud), say plainly that nothing follows it until the
// owner answers its decisions, and keep every constraint the reconciliation's
// §8 rules impose (no live action, never a secret, never `belong`).
func TestDocsManagedHardStopDesignIsWritten(t *testing.T) {
	const path = "docs/design/managed-projects-hard-stop.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The task it is, the answer it follows, and its own gate.
		"Task **6.0**",
		"no hard stop in the first managed release",
		"2026-10-18",
		"design only",
		"Nothing in this document is built",
		// The four questions the plan text does not fix, each answered.
		"disable billing, or only scale services down",
		"Who can undo it?",
		"false positive is recovered",
		"proven without a live cloud",
		// The mechanism, priced: the action, the trigger and the seam.
		"Unlink the billing account from the managed project",
		"Scale the workloads down",
		"Disable the project's billable APIs",
		"Delete the project",
		"The budget's own Pub/Sub notification",
		"all_updates_rule",
		// Where it runs, which is the finding that shapes it.
		"guardian project",
		"disarmable by the agent",
		// The credential, and what it may never be.
		"stop-only",
		"never the apply identity",
		// Recovery, in the shape the tree already has for a halt.
		"fugaro budget kill",
		"initflow.Typed",
		// The decisions, and the convention that they are unanswered.
		"empty until the owner answers it",
		"D79",
		"D86",
		// How it is proven, and the one live step.
		"internal/gcpfake",
		"Check 71",
		"one writer owns each",
		// The constraints that bind it.
		"never `belong`",
		"no secret is handled",
		"SECURITY-SENSITIVE",
		"## 8. UNVERIFIED",
		// Review of 2026-10-18: Google's native spend cap is priced as an
		// option, the unlink's data risk is stated in Google's words rather than
		// as "data left in place", and the design says why an agent can neither
		// fire the stop nor disarm it — the endpoint's caller check and the
		// managed project's setIamPolicy are the two holes it closes.
		"spendCap",
		"might be removed and become non-recoverable",
		"### 3.5 Why an agent can neither fire the stop nor disarm it",
		"the subscription's own push identity",
		"resourcemanager.projects.setIamPolicy",
		"costAmount",
		"forecastThresholdExceeded",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsManagedHardStopDecisionsAreNumberedAndUnanswered: the design takes
// D79 onward — the number docs/design/managed-projects.md §5 records as the
// next free one — and every decision it takes is present exactly once, with an
// empty answer, because a decision this design wrote and then answered itself
// would be an approval it does not have.
func TestDocsManagedHardStopDecisionsAreNumberedAndUnanswered(t *testing.T) {
	const path = "docs/design/managed-projects-hard-stop.md"
	doc := readDoc(t, path)
	// A number, once allocated, is never reused: the block is contiguous, so a
	// decision added later takes the next number rather than one inside it.
	//
	// Each row's OWNER'S ANSWER CELL is read as well as its number, because a
	// count of the number's own form cannot see an answer: a row that had been
	// answered — "yes", or the recommendation restated as a ruling — would
	// still hold exactly one "**D79**" and pass a count-only assertion, while
	// claiming an approval this document does not have. The row is a GFM table
	// line, so its cells are what "|" separates and the answer is the last of
	// them: the design's §5 table is written with five columns and the fifth is
	// the owner's.
	for _, d := range []string{"D79", "D80", "D81", "D82", "D83", "D84", "D85", "D86"} {
		if got := strings.Count(doc, "**"+d+"**"); got != 1 {
			t.Errorf("%s names %s %d times in the decision block's own form; want exactly 1", path, d, got)
			continue
		}
		row := decisionRow(t, path, doc, d)
		cells := strings.Split(row, "|")
		if len(cells) < 3 {
			t.Errorf("%s: %s's row is not a table line with cells: %q", path, d, row)
			continue
		}
		// cells[0] is the text before the row's first "|" and the last element
		// is what follows its final one, so the owner's answer is the
		// second-to-last.
		if answer := strings.TrimSpace(cells[len(cells)-2]); answer != "" {
			t.Errorf("%s: %s's owner's-answer cell holds %q; the column is empty until the owner answers it, and a design that answers its own decision claims an approval it does not have", path, d, answer)
		}
	}
	if strings.Contains(doc, "D87") {
		t.Errorf("%s names D87: the block is D79 to D86, and a decision added later takes the next number", path)
	}
	// The next free number is claimed from the design that states it, not
	// re-derived here: this assertion is what fails if managed-projects.md's
	// sentence moves and this design's claim stops matching it.
	if got := strings.Count(doc, "D79"); got < 2 {
		t.Errorf("%s names D79 %d times; it takes the number and must also quote the sentence that says it is free", path, got)
	}
	nextFree := readDoc(t, "docs/design/managed-projects.md")
	if !strings.Contains(nextFree, "the next free number in the D-series is D79") {
		t.Fatalf("docs/design/managed-projects.md no longer records D79 as the next free number: %s's allocation has to be re-derived and this test's claim re-read", path)
	}
}

// decisionRow is the one table line of the design's §5 that names the decision
// in the block's own form. A row is one line in this document — the table is
// written long-lined, one decision per line, so a reader sees the number, the
// question, both prices and the empty answer together — and a decision whose
// row has been wrapped across lines is a shape this test cannot read rather
// than one it should guess at.
func decisionRow(t *testing.T, path, doc, decision string) string {
	t.Helper()
	needle := "**" + decision + "**"
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("%s: %s is not on a line of its own that this test can read", path, decision)
	return ""
}

// TestDocsManagedHardStopCitationsExist: the design's load-bearing citations are
// paths a later implementer has to find — the budget and its thresholds, the
// only billing write in the tree, the marker and the apply gate, the halt the
// recovery copies, the fake that owes an unlink. A substring assertion cannot
// see one of them move, so each is stat'ed here as well as required in the text.
func TestDocsManagedHardStopCitationsExist(t *testing.T) {
	const path = "docs/design/managed-projects-hard-stop.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// What the hard stop sits beside.
		"deploy/terraform/gcp/modules/installation/budget.tf",
		"deploy/terraform/gcp/modules/installation/deployer.tf",
		"deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl",
		"scripts/gen-golden-plans.sh",
		// The billing write it is the inverse of, and the creation fact.
		"internal/infra/projectcreate.go",
		"internal/infra/managed/marker.go",
		"internal/infra/managed/read.go",
		// The apply gate, and the halt the recovery copies.
		"internal/managed/budget.go",
		"internal/managed/enable.go",
		"internal/budget/kill.go",
		"internal/cli/budget.go",
		"internal/cli/allow_delete.go",
		"internal/cli/initsecrets.go",
		"internal/cli/root.go",
		// The fake, the skill the command changes, and the documents it is read
		// against.
		"internal/gcpfake/billing.go",
		"plugin/skills/setup/SKILL.md",
		"SECURITY.md",
		"docs/mobile/README.md",
		"docs/design/managed-projects.md",
		"docs/design/managed-projects-spec.md",
		"docs/plans/2026-10-16-managed-projects.md",
		"docs/gcp-live-checklist.md",
	} {
		// The document links its neighbours relatively (../plans/…, the
		// sibling design by name), so a citation counts in either form.
		rel, err := filepath.Rel("docs/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", path, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", path, cited, err)
		}
	}

	// SECURITY.md is cited by LINE NUMBER, and a line number is the one
	// citation a path check cannot see move: "### Reserved secret names" was
	// inserted above the bullet this design rests on, and every citation of it
	// pointed seven lines short afterwards — at "Narrow git credentials", a
	// true sentence about a different subject. So each numbered citation is
	// resolved here against the file's own text: the range has to exist, and
	// the lines it names have to be the ones the design quotes them for.
	assertLineCitations(t, path, doc, "SECURITY.md", []string{
		// The claim all three citations are made for (§3.3, D81, §7 item 6):
		// the agent shares the runner's container, user, /proc and metadata
		// server. A citation that no longer lands on it is a citation of some
		// other sentence.
		"shares the container and its user with the runner",
	})
	// And the invariant §3.4 rests the project-scoped binding on: the
	// deployer holds no resourcemanager.projects.* permission, so no identity
	// a managed run can reach may remove the stop's binding.
	assertLineCitations(t, path, doc, "deploy/terraform/gcp/modules/installation/deployer.tf", []string{
		"no resourcemanager.projects.* at all",
	})
}

// assertLineCitations resolves every "path:N" and "path:N-M" citation in doc
// against the cited file: the range has to be inside it, and each of wants has
// to appear between the first and last line the citation names. It is what
// makes a line-number citation a claim that can fail, which a substring match
// against the path alone never is.
func assertLineCitations(t *testing.T, path, doc, file string, wants []string) {
	t.Helper()
	lines := strings.Split(readDoc(t, file), "\n")
	re := regexp.MustCompile(regexp.QuoteMeta(file) + `:(\d+)(?:-(\d+))?`)
	cites := re.FindAllStringSubmatch(doc, -1)
	if len(cites) == 0 {
		t.Errorf("%s cites no line of %s any more; this assertion exists because it does", path, file)
		return
	}
	for _, c := range cites {
		first, err := strconv.Atoi(c[1])
		if err != nil {
			t.Fatalf("%s: unreadable citation %q", path, c[0])
		}
		last := first
		if c[2] != "" {
			if last, err = strconv.Atoi(c[2]); err != nil {
				t.Fatalf("%s: unreadable citation %q", path, c[0])
			}
		}
		if first < 1 || last < first || last > len(lines) {
			t.Errorf("%s cites %s, and %s has %d lines", path, c[0], file, len(lines))
			continue
		}
		slice := strings.Join(lines[first-1:last], "\n")
		for _, want := range wants {
			if !strings.Contains(slice, want) {
				t.Errorf("%s cites %s for a claim about %q, and the lines it names do not hold it: the citation has gone stale, which is how it came to point at another section", path, c[0], want)
			}
		}
	}
}

// TestDocsManagedHardStopClaimsNothingWasDone: the design is the deliverable and
// it is not the work. It must not claim a mechanism, a credential, a check row
// or a decision answer exists, and the live check it names must be recorded as
// unrun — a document that reads as finished work is the failure this whole task
// exists to avoid.
func TestDocsManagedHardStopClaimsNothingWasDone(t *testing.T) {
	const path = "docs/design/managed-projects-hard-stop.md"
	doc := readDoc(t, path)
	for _, banned := range []string{
		// No answer it does not have.
		"Owner answered",
		"the owner answered D79",
		// No row or section of the register: that is the implementation task's.
		"## Check 71",
		"| 71 |",
		// No hand-off that names a number the register does not hold.
		"Check 71, RUN",
	} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s contains %q: the design states the mechanism and the open decisions, and writes no answer, no check row and no check section of its own", path, banned)
		}
	}
	// The negative claim has to be readable in the document's own words, on the
	// status line's terms: it is a design, nothing follows it until the owner
	// answers, and its one live step has not run.
	for _, want := range []string{
		"no code follows it until the owner answers",
		"NOT RUN",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q", path, want)
		}
	}
}

// TestDocsPlanPointsAtTheHardStopDesign (plan Task 6.0): the design is
// discoverable from the task that asked for it. Task 6.0's row said the file was
// "a design of its own, if the owner wants one"; with the owner's approval of
// 2026-10-18 and the document written, the row has to name it, or the record of
// what was approved points at nothing.
func TestDocsPlanPointsAtTheHardStopDesign(t *testing.T) {
	const design = "docs/design/managed-projects-hard-stop.md"
	plan := readDoc(t, "docs/plans/2026-10-16-managed-projects.md")
	i := strings.Index(plan, "### Task 6.0:")
	if i < 0 {
		t.Fatal("docs/plans/2026-10-16-managed-projects.md has no Task 6.0 heading any more")
	}
	// The row runs to the next task's heading: the pointer has to be inside it,
	// so a mention elsewhere in the plan cannot stand in for it.
	row := plan[i:]
	if j := strings.Index(row[1:], "\n### Task 6."); j > 0 {
		row = row[:j+1]
	}
	if !strings.Contains(row, design) {
		t.Errorf("Task 6.0's row does not point at %s", design)
	}
	for _, want := range []string{"2026-10-18", "D79"} {
		if !strings.Contains(row, want) {
			t.Errorf("Task 6.0's row does not record %q", want)
		}
	}
	if _, err := os.Stat("../../" + design); err != nil {
		t.Fatalf("Task 6.0 points at %s, which is not there: %v", design, err)
	}
}
