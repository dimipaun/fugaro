package cli

// This folder's pin over the amendment the portal makes (P9.1 of
// docs/portal/implementation-plan.md, from its reconciliation's "What this
// changes in licence key v2").
//
// The three documents are one design. The folder's plan builds the licence
// service and the key-signing design is the service it was written for, so the
// portal's tasks touch both: the sign-in, the admin page and the account pages
// are the portal's surfaces now, and D2194 puts the API, the minter and the ring
// in `fugaro-prod` rather than a project of the service's own. Each document
// therefore carries a dated note saying which of its own sentences the portal's
// tasks and decisions supersede, and this pin holds those notes to the claims
// they make -- a note dropped in a later edit is a reader told the service is
// still built the way it was ruled in October.
//
// What this cannot do is judge the amendment. Whether the portal's operations
// API really carries the admin page's defences, and whether `fugaro-prod` is
// the right project, are the portal's own decisions (D2187, D2194) and its own
// plan's to hold; these tests read the sentences that record them here.

import (
	"strings"
	"testing"
)

const (
	licenceKeyV2Plan  = "docs/licence-key-v2/implementation-plan.md"
	licenceKeyV2Recon = "docs/licence-key-v2/reconciliation.md"
	keySigningDesign  = "docs/design/key-signing-service.md"
)

// licenceKeyV2Amendment is the date and the authorship every dated note in the
// three documents opens with. It is one string rather than three spellings so
// that a note written in a second wording fails here.
const licenceKeyV2Amendment = "Amended 2026-11-13 by the portal (P9.1,"

// TestDocsLicenceKeyV2PlanFollowsThePortal holds the plan's tasks to the
// amendment: the paragraph that lists every change, the note each affected task
// carries, and the two tables the changes move.
func TestDocsLicenceKeyV2PlanFollowsThePortal(t *testing.T) {
	plan := readDoc(t, licenceKeyV2Plan)
	if !strings.Contains(flattenDoc(plan), licenceKeyV2Amendment) {
		t.Errorf("%s carries no %q note: the plan's own tasks are amended by the portal, and a plan that does not say so is read as the one the portal replaced", licenceKeyV2Plan, licenceKeyV2Amendment)
	}
	for _, want := range []string{
		"K2.4 uses P1.3's verifier",
		"K2.6 is superseded by P5.1",
		"K5.1 is superseded by the portal's screens",
	} {
		if !strings.Contains(flattenDoc(plan), want) {
			t.Errorf("%s lacks %q: the amendment's own list of what the portal changes is what a reader of the plan goes by", licenceKeyV2Plan, want)
		}
	}

	// Each affected task says what the portal changes about it, in its own
	// bullet: a reader following one task never reads the list above.
	for _, tc := range []struct {
		task string
		want []string
	}{
		{"- **K2.4", []string{"P1.3's verifier", "registry", "P3.5"}},
		{"- **K2.5", []string{"carries a link", "never a key"}},
		{"- **K2.6", []string{"Superseded by the portal", "P5.1"}},
		{"- **K2.7", []string{"drops its edge on K2.6", "D2201"}},
		{"- **K3.3", []string{"Firebase project id", "binds the chosen one"}},
		{"- **K3.4", []string{"D2186", "array order"}},
		{"- **K4.1", []string{"keeps its secret", "D2200", "D2202"}},
		{"- **K5.1", []string{"Superseded by the portal", "ee/portal/src/screens"}},
	} {
		body := flattenDoc(bulletWith(plan, tc.task))
		if body == "" {
			t.Fatalf("%s has no %q bullet: this case reads that task's own note", licenceKeyV2Plan, tc.task)
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s's %s bullet lacks %q: the task is amended by the portal, and its own bullet is where a reader of it learns that", licenceKeyV2Plan, tc.task, want)
			}
		}
	}

	// The two tables the amendment moves. A superseded task scheduled in a
	// level, or a file a superseded task would have written still listed under
	// its writer, is the plan telling two readers two stories.
	levels := docSection(t, licenceKeyV2Plan, plan, "## Parallel execution")
	for _, row := range []string{"| 2 | K2.3, K2.5, K2.7 |", "| 4 | K3.3, K3.5, K4.1 |"} {
		if !strings.Contains(levels, row) {
			t.Errorf("%s's parallel-execution table has no %q row: K2.7's edge on the superseded admin page is gone, so its level follows its remaining edges and the rows that listed the superseded tasks move with it", licenceKeyV2Plan, row)
		}
	}
	for _, line := range strings.Split(levels, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		for _, gone := range []string{"K2.6", "K5.1"} {
			if strings.Contains(line, gone) {
				t.Errorf("%s' parallel-execution table still lists %s (%q): it is superseded by the portal, so it is neither scheduled nor a writer of a shared file", licenceKeyV2Plan, gone, strings.TrimSpace(line))
			}
		}
	}
}

// TestDocsLicenceKeyV2ReconciliationRecordsTheAmendment holds the findings the
// amended tasks were read from, and the decision the portal's D2201 amends, to
// the same notes.
func TestDocsLicenceKeyV2ReconciliationRecordsTheAmendment(t *testing.T) {
	recon := readDoc(t, licenceKeyV2Recon)
	for _, want := range []string{
		licenceKeyV2Amendment,
		"P1.3's verifier",
		"the portal's operations API and screens (P5.1, P5.3)",
		"D2201",
	} {
		if !strings.Contains(flattenDoc(recon), want) {
			t.Errorf("%s lacks %q: the findings the portal's tasks were read from and the decision it amends are recorded here, and a plan amended from an unamended reconciliation is read against the wrong facts", licenceKeyV2Recon, want)
		}
	}
}

// TestDocsKeySigningDesignFollowsThePortal holds the design's own four claims:
// the project is D2194's, the admin page is the portal's, the merchant timestamp
// is P4.2's, and the three open decisions are decided.
func TestDocsKeySigningDesignFollowsThePortal(t *testing.T) {
	doc := readDoc(t, keySigningDesign)
	if !strings.Contains(flattenDoc(doc), licenceKeyV2Amendment) {
		t.Errorf("%s carries no %q note in its status block: the design's project, page and timestamp claims are amended by the portal, and a reader of the design has to learn it there", keySigningDesign, licenceKeyV2Amendment)
	}

	for _, tc := range []struct {
		section string
		want    []string
	}{
		// Where the service's project is settled. The sentence names the
		// portal's plan as the place the accepted risk is written down.
		{"### 3.4 Where the code and the infrastructure live", []string{"Amended 2026-11-13", "D2194", "`fugaro-prod`"}},
		// The deliberate choice not to read the merchant's timestamp.
		{"### 5.2 Signature, idempotency and order", []string{"portal P4.2", "5-minute timestamp window", "fresh `ts`"}},
		// The page the portal's operations API replaces.
		{"## 6. The admin page and the manual queue", []string{"Replaced 2026-11-13 (portal P5.1, P5.3)", "D2187"}},
	} {
		section := flattenDoc(docSection(t, keySigningDesign, doc, tc.section))
		for _, want := range tc.want {
			if !strings.Contains(section, want) {
				t.Errorf("%s's %q lacks %q: the portal's tasks supersede what it settles, and the note is what a reader of the section goes by", keySigningDesign, tc.section, want)
			}
		}
	}

	// T13's threat-model row for the project, which D2194 rewrites.
	row := mdRow(t, keySigningDesign,
		docSection(t, keySigningDesign, doc, "### 9.1 The twelve T13 names, and what stops each"),
		"| 5 | Break into the project the service runs in |")
	for _, want := range []string{"D2194", "`fugaro-prod`"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §9.1 row 5 lacks %q: the row says a dedicated project stops the attempt, and D2194 puts everything in one project instead", keySigningDesign, want)
		}
	}

	// The three questions the design left open and the portal answers.
	open := docSection(t, keySigningDesign, doc, "### Open")
	for _, tc := range []struct{ prefix, decided string }{
		{"| D454 |", "D2187"},
		{"| D455 |", "D2187"},
		{"| D459 |", "D2194"},
	} {
		r := mdRow(t, keySigningDesign, open, tc.prefix)
		if strings.HasSuffix(strings.TrimSpace(r), "| open |") {
			t.Errorf("%s's %s row still ends with the open status: %s decided it on 2026-11-12, and a reader of the table is otherwise told the owner has not answered", keySigningDesign, tc.prefix, tc.decided)
		}
		if !strings.Contains(r, tc.decided) {
			t.Errorf("%s's %s row does not name %s as the decision: the row records who decided it and against what options", keySigningDesign, tc.prefix, tc.decided)
		}
	}

	// The ledger the census row's own rule asks for: a folder that writes a pin
	// records that it moved the row, in the same commit.
	ledger := flattenDoc(readDoc(t, "docs/rigor-modes/reconciliation.md"))
	if !strings.Contains(ledger, "docs_licence_key_v2_test.go`, the licence-key-v2 folder's pin") {
		t.Errorf("docs/rigor-modes/reconciliation.md carries no amendment naming internal/cli/docs_licence_key_v2_test.go: the census row's own text says a folder that writes a pin moves the row in its own commit and records it there")
	}
}
