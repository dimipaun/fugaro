package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsBudgetTokenChannelDesignIsWritten (plan task 3b.1,
// SECURITY-SENSITIVE):
// the design that moves the run's budget token out of reach of the other runs
// of a shared job exists, prices the four candidates the plan names, states a
// recommendation, says plainly that nothing follows it until the owner
// approves, and keeps the interim ruling D13's own words say the profile
// remains the isolation boundary — for secrets only, since every project job
// account holds the same whole bucket prefixes (Task 3.2's review), which
// leaves D13's "separate profiles" mitigation open for the owner. Its red state is this test: the document
// did not exist.
func TestDocsBudgetTokenChannelDesignIsWritten(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The subject and where it sits.
		"Phase 3b",
		"Task 3b.1",
		// The four candidates the plan lists, each priced.
		"signed URL",
		"Secret Manager",
		"tokens/",
		"execution identity",
		// The constraints it must not break.
		"launcher-overridable",
		"fail closed",
		"profile is the isolation boundary for secrets only",
		"open for the owner to",
		// The verdict and its gate.
		"capability, not a boundary",
		"no code follows this document until the owner approves it",
		// The probe that gates one candidate, and the check that has not run.
		"Check 52",
		// The owner's decisions and the live check the implementation needs.
		"## The owner's open decisions",
		"## The check outline",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsBudgetTokenChannelCitationsExist: the design's load-bearing citations
// are paths a later implementer has to find — the module that grants the prefix,
// the package the seal will live in, the runner that parses the marker, the
// register the check number is read from. A substring assertion cannot see one of
// them move, so each is stat'ed here as well as required in the text.
//
// Every path below was read on `next` on 2026-10-15 and is in this branch's tree.
// The two documents the check-number paragraph cites from `main` —
// docs/design/artifact-runs.md and docs/plans/2026-10-15-desktop-console.md — are
// deliberately not stat'ed: they are `main`'s files (#393 and #401 merged there),
// this branch is `next`'s, and the paragraph says so in its own words.
func TestDocsBudgetTokenChannelCitationsExist(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The grants the move changes, and the modules that carry them.
		"deploy/terraform/gcp/modules/installation/iam.tf",
		"deploy/terraform/gcp/modules/installation/bucket.tf",
		"deploy/terraform/gcp/modules/workflow/job.tf",
		"deploy/terraform/gcp/modules/workflow/sa.tf",
		// The channel itself: the object, the mint, the claims, the exchange.
		"internal/budget/token/object.go",
		"internal/budget/token/mint.go",
		"internal/budget/token/token.go",
		"internal/budget/token/exchange.go",
		// The two ends: the runner that takes and parses, the launcher that mints.
		"internal/runner/budget_session.go",
		"internal/runner/projectpolicy.go",
		"internal/runner/runner.go",
		"internal/cli/launch_budget.go",
		"internal/cli/run.go",
		"internal/cli/exec.go",
		// What the design leans on for its ordering and its check.
		"internal/cli/init_fugaroyaml.go",
		"internal/cli/init.go",
		"internal/imagecheck/record.go",
		"internal/infra/tf/cover.go",
		"internal/infra/tfvars.go",
		"internal/infra/hardening.go",
		"internal/backend/gcp/names.go",
		"internal/budget/rules/rules.go",
		// The documents it is read against.
		"SECURITY.md",
		"docs/gcp-live-checklist.md",
		"docs/plans/2026-10-14-project-centric.md",
		"docs/design/compute-backend.md",
		"docs/design/m9-budget-and-dashboard.md",
		"docs/design/bucket-iam.md",
		"docs/design/project-centric.md",
	} {
		// The document links its neighbours relatively (../plans/…,
		// ../../SECURITY.md), so a citation counts in either form.
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

	// The number the outline proposes is now written, by the seal's own
	// launcher half: the checklist's Check 60 is this design's, owner-run,
	// sandbox only, and never the belong installation.
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	i := strings.Index(checklist, "## Check 60: the sealed budget token")
	if i < 0 || !strings.Contains(checklist, "| 60 | The sealed budget token") {
		t.Fatalf("docs/gcp-live-checklist.md does not carry Check 60 for %s (its section and its summary row)", path)
	}
	// The section runs to the NEXT check's heading, not to the end of the
	// file: the file holds every check, and a slice that ran on would let a
	// later check's words answer for this one's — "run by you" and "NOT RUN"
	// are in the heading of every check that has not run, Check 69a's and
	// 69b's included, so two of these six assertions could not fail.
	section := checklist[i:]
	if j := strings.Index(section[1:], "\n## Check "); j >= 0 {
		section = section[:j+1]
	}
	for _, want := range []string{"never `belong`", "run by you", "NOT RUN", "**No agent runs any step of this check**", "P=fugaro-dev", "never a token, a key"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 60 does not say %q", want)
		}
	}

	// And the ROW above has to render as a row of the summary table, which a
	// substring match cannot see: every check's row is one line of one GFM
	// table, and a blank line inside that table ends it — everything below the
	// gap then renders as literal text beside the table, so a check's row (and
	// every row after it) stops being a row at all while the strings this test
	// greps for are still in the file. A blank line between two check rows is
	// therefore its own failure, and this is the file-wide scan for one: the
	// table has no such gap today, and the check these assertions are about is
	// the one whose row lost its table when it appeared.
	assertCheckRowsAreOneTable(t, "docs/gcp-live-checklist.md", checklist)
}

// assertCheckRowsAreOneTable refuses a blank line that sits between two rows of
// the live checklist's summary table — the shape that silently ends a GFM table
// and turns every row below the gap into plain text.
func assertCheckRowsAreOneTable(t *testing.T, path, doc string) {
	t.Helper()
	lines := strings.Split(doc, "\n")
	row := regexp.MustCompile(`^\| [0-9]+[a-z]? \| `)
	for i := 1; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" || !row.MatchString(lines[i-1]) || !row.MatchString(lines[i+1]) {
			continue
		}
		t.Errorf("%s:%d: a blank line between the check rows %q and %q ends the summary table, so every row below the gap renders as literal text instead of a table row",
			path, i+1, firstField(lines[i-1]), firstField(lines[i+1]))
	}
}

// firstField is a table row's first cell — the check's number — for a message
// that names the two rows the gap is between.
func firstField(row string) string {
	if cells := strings.Split(row, "|"); len(cells) > 1 {
		return strings.TrimSpace(cells[1])
	}
	return row
}

// TestDocsSecurityMdPointsAtTheTokenChannelDesign: the trust model's reader can
// find the design that sealed the token's delivery channel, from the D13
// paragraph that the seal narrows, and the paragraph says what the seal is —
// built, both halves, and a capability rather than a boundary — rather than
// overstating it. The pointer is asserted on the D13 paragraph's own line, so
// wording elsewhere in the file cannot stand in for it.
func TestDocsSecurityMdPointsAtTheTokenChannelDesign(t *testing.T) {
	const design = "docs/design/budget-token-channel.md"
	sec := readDoc(t, "SECURITY.md")
	var pointer string
	for _, line := range strings.Split(sec, "\n") {
		if strings.Contains(line, "D13") && strings.Contains(line, design) {
			pointer = line
			break
		}
	}
	if pointer == "" {
		t.Fatalf("SECURITY.md's D13 paragraph does not point at %s", design)
	}
	for _, want := range []string{"both halves built", "capability, not a boundary", "read-only", "sealed"} {
		if !strings.Contains(pointer, want) {
			t.Errorf("SECURITY.md's D13 paragraph does not say %q about the seal\n\nline: %s", want, pointer)
		}
	}
	// The marker's sentence and the launcher paragraph are rewritten
	// together (D15): the token is a bearer credential that does not travel
	// as an override, the key is not a credential and its audience is named.
	for _, want := range []string{"**bearer credential**", "**seal key**", "is not a credential", "Its audience is the override's", "not** a peer run"} {
		if !strings.Contains(sec, want) {
			t.Errorf("SECURITY.md does not carry %q: the marker's sentence and the launcher paragraph must say what the key is (D15)", want)
		}
	}
	if _, err := os.Stat("../../" + design); err != nil {
		t.Fatalf("SECURITY.md points at %s, which is not there: %v", design, err)
	}
}
