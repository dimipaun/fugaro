package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const modelCraftsDir = "docs/model-crafts/"

// TestDocsModelCrafts pins what the plan promises about itself: the number
// blocks it reserves, the folder's current status line and its pointer to
// docs/status.md, and that the verbatim spec is still the spec.
func TestDocsModelCrafts(t *testing.T) {
	readme := readDoc(t, modelCraftsDir+"README.md")
	for _, want := range []string{"D1620–D1639", "Checks 420–429", "Status as of 2026-11-10: in progress, 4/10 merged; see [docs/status.md](../status.md) for the current table."} {
		if !strings.Contains(readme, want) {
			t.Errorf("%sREADME.md does not state %q", modelCraftsDir, want)
		}
	}
	plan := flattenDoc(readDoc(t, modelCraftsDir+"implementation-plan.md"))
	if !strings.Contains(plan, "Status as of 2026-11-10: in progress, 4/10 merged; see [docs/status.md](../status.md) for the current table.") {
		t.Errorf("the plan does not carry the folder's current status line")
	}
	rec := readDoc(t, modelCraftsDir+"reconciliation.md")
	decision := regexp.MustCompile(`(?m)^\| \*\*D(16[0-9]{2})\*\* \|`)
	for _, m := range decision.FindAllStringSubmatch(rec, -1) {
		// Two blocks, and the gap between them is the numbering rule the folder
		// states: D1639 is reserved and unused, docs/website-changelog holds
		// D1640–D1659, and the activation plan takes D1660–D1679. A row outside
		// both is a number taken from a sibling rather than this work's own, so
		// the scan reads the two ranges and not "this folder's block", which
		// would have to grow a third time the next time a plan extends it.
		if n := m[1]; (n < "1620" || n > "1639") && (n < "1660" || n > "1679") {
			t.Errorf("decision D%s is outside the blocks this folder holds, D1620-D1639 and D1660-D1679", n)
		}
	}
	if !strings.HasPrefix(readDoc(t, modelCraftsDir+"spec.md"), "# Fugaro Model Roles and Credentials Spec") {
		t.Errorf("spec.md is not the spec as received")
	}
}

// TestDocsModelCraftsActivation pins the folder's second plan the same way the
// first is pinned: the numbers it reserves (D1660–D1679, Checks 628–637), the
// fact that nothing of it is implemented and no task is approved, the four
// halves 0.24.0's known limits name and the owner's own order over them, and
// the pointer from the folder's README. What it does not pin is the tasks'
// text: a plan whose every sentence is asserted in Go is a plan nobody can
// amend, and the review of the first plan asked for the opposite.
func TestDocsModelCraftsActivation(t *testing.T) {
	const path = modelCraftsDir + "activation-plan.md"
	readme := readDoc(t, modelCraftsDir+"README.md")
	for _, want := range []string{
		"[activation-plan.md](activation-plan.md)",
		"D1620–D1639 and D1660–D1679",
		"Checks 420–429 and 628–637",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%sREADME.md does not point at the activation plan's numbers: %q", modelCraftsDir, want)
		}
	}
	plan := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		"Status as of 2026-11-15: plan only; nothing of this plan is implemented, no task is approved",
		"D1660–D1679",
		"Checks 628–637",
		// The four halves, in the owner's order of 2026-11-15, each by the
		// sentence 0.24.0's known limits left behind.
		"the model half",
		"the publish",
		"the crossing",
		"the key credentials",
		"### MC11 —",
		"### MC26 —",
		// The one task of the plan is the owner's to run, and it says so.
		"OWNER-RUN",
		"scripts/live-checks/",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not state %q", path, want)
		}
	}
	rec := readDoc(t, modelCraftsDir+"reconciliation.md")
	if !strings.Contains(rec, "D1660–D1679") {
		t.Errorf("%sreconciliation.md does not carry the activation plan's reserved block", modelCraftsDir)
	}
	// The questions are the plan's own and each carries a recommendation: a
	// decision row whose answer column is bare is a question nobody answered,
	// which is what the folder's rule about an unapproved task forbids a plan
	// to be built on.
	for _, want := range []string{"**D1660**", "**D1668**", "**D1672**"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%sreconciliation.md does not carry the decision %s the activation plan is written on", modelCraftsDir, want)
		}
	}
	// A finding the folder claims is one the reconciliation writes. The claims
	// run over two shapes — F1 to F26 are prose blocks in §2, the plan's own
	// finding is a row of §3b's table — so the claim is read against the rows
	// rather than pinned as a string: a range reaching past the last row
	// ("F27–F30") claims readings nobody wrote, and pinning the sentence would
	// only have caught the one wording that was wrong.
	written := map[int]bool{}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\*\*F([0-9]+) —`),
		regexp.MustCompile(`(?m)^\| \*\*F([0-9]+)\*\* \|`),
	} {
		for _, m := range re.FindAllStringSubmatch(rec, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil {
				written[n] = true
			}
		}
	}
	for _, m := range regexp.MustCompile(`F([0-9]+)–F?([0-9]+)`).FindAllStringSubmatch(readme+"\n"+rec, -1) {
		lo, errLo := strconv.Atoi(m[1])
		hi, errHi := strconv.Atoi(m[2])
		if errLo != nil || errHi != nil {
			continue
		}
		for n := lo; n <= hi; n++ {
			if !written[n] {
				t.Errorf("F%d is claimed as written (F%d–F%d) and reconciliation.md writes no such finding", n, lo, hi)
			}
		}
	}
}
