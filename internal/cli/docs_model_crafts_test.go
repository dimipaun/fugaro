package cli

import (
	"regexp"
	"strings"
	"testing"
)

const modelCraftsDir = "docs/model-crafts/"

// TestDocsModelCrafts pins what the plan promises about itself: the number
// blocks it reserves, that no task is approved, and that the verbatim spec
// is still the spec.
func TestDocsModelCrafts(t *testing.T) {
	readme := readDoc(t, modelCraftsDir+"README.md")
	for _, want := range []string{"D1620–D1639", "Checks 420–429", "plan only; nothing is implemented; no task approved"} {
		if !strings.Contains(readme, want) {
			t.Errorf("%sREADME.md does not state %q", modelCraftsDir, want)
		}
	}
	plan := flattenDoc(readDoc(t, modelCraftsDir+"implementation-plan.md"))
	if !strings.Contains(plan, "No task is approved") {
		t.Errorf("the plan does not say no task is approved")
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
