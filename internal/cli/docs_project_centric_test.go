package cli

import (
	"os"
	"strings"
	"testing"
)

// TestChecklistHasCheck52: docs/gcp-live-checklist.md must carry phase 3's
// four UNVERIFIED probes (design project-centric.md §13 and §4.5; plan
// 2026-10-14-project-centric.md Task 3.0) as one owner-run, not-yet-run,
// sandbox-only check numbered 52 — with its row in the check list and Check
// 43's guard block (the three hard stops and the project pinning) inside its
// section, so a reader cannot mistake it for something CI covers and no step
// of it can run against a project other than fugaro-dev.
func TestChecklistHasCheck52(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	section := docSection(t, path, doc, "## Check 52")
	for _, want := range []string{"NOT RUN", "run by you", "sandbox", "never belong"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 52 section does not say %q", want)
		}
	}
	// Its row in the check list (the summary table), not only the section.
	var row bool
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 52 |") {
			row = true
			break
		}
	}
	if !row {
		t.Error("the check list has no Check 52 row")
	}
	// Check 43's guard block inside the section — the three hard stops and
	// the project pinning, not merely a ${P:?...}.
	for _, guard := range []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] \`,
		`|| { echo "Check 52 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 52 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 52 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
	} {
		if !strings.Contains(section, guard) {
			t.Errorf("the Check 52 section is missing the Check 43 guard line %q", guard)
		}
	}
}
