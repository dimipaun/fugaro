package cli

import (
	"regexp"
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
		if n := m[1]; n < "1620" || n > "1639" {
			t.Errorf("decision D%s is outside the reserved block D1620-D1639", n)
		}
	}
	if !strings.HasPrefix(readDoc(t, modelCraftsDir+"spec.md"), "# Fugaro Model Roles and Credentials Spec") {
		t.Errorf("spec.md is not the spec as received")
	}
}
