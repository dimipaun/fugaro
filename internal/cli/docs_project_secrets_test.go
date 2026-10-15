package cli

import (
	"os"
	"strings"
	"testing"
)

// TestChecklistHasCheck40: docs/gcp-live-checklist.md must carry the
// project-level-secrets live check (design project-secrets.md §8) as a
// user-run, not-yet-run, sandbox-only check, with every sub-step the plan
// numbers (40a to 40j) and the numbering ruling naming Check 40 as taken —
// so a reader does not mistake it for something CI already covers, and the
// number is not handed out again to a later plan.
func TestChecklistHasCheck40(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	section := docSection(t, path, doc, "## Check 40")
	for _, want := range []string{"NOT RUN", "run by you", "sandbox", "never `belong`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 40 section does not say %q", want)
		}
	}
	for _, sub := range []string{"40a", "40b", "40c", "40d", "40e", "40f", "40g", "40h", "40i", "40j"} {
		if !strings.Contains(section, sub) {
			t.Errorf("the Check 40 section has no %s sub-step", sub)
		}
	}
	// The ruling's own sentence wraps across lines in the file.
	ruling := strings.Join(strings.Fields(doc), " ")
	if !strings.Contains(ruling, "Check 40 is reserved by the project-secrets design") {
		t.Error("the Numbering ruling does not name Check 40 as reserved")
	}
}
