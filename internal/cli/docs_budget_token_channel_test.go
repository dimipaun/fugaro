package cli

import (
	"os"
	"path/filepath"
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

	// The number the outline proposes, read from the register itself: a
	// `## Check 60` section or a `| 60 |` row in the checklist means another
	// document has taken it, and this design would be proposing a taken number.
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	for _, taken := range []string{"## Check 60", "| 60 |"} {
		if strings.Contains(checklist, taken) {
			t.Errorf("docs/gcp-live-checklist.md carries %q, so Check 60 is no longer free and %s must propose the next number", taken, path)
		}
	}
}

// TestDocsSecurityMdPointsAtTheTokenChannelDesign: the trust model's reader can
// find the design that replaces the token's delivery channel, and the pointer
// says the design is not built — which is the property, and it is carried by
// the pointer's own wording, not by the path string. A paragraph rewritten to
// imply the channel ships must fail this test.
func TestDocsSecurityMdPointsAtTheTokenChannelDesign(t *testing.T) {
	const (
		design = "docs/design/budget-token-channel.md"
		gate   = "no code follows that document until the owner approves it"
	)
	sec := readDoc(t, "SECURITY.md")
	// The pointer is one line of SECURITY.md's own list: assert on that line,
	// so wording somewhere else in the file cannot stand in for it.
	var pointer string
	for _, line := range strings.Split(sec, "\n") {
		if strings.Contains(line, design) {
			pointer = line
			break
		}
	}
	if pointer == "" {
		t.Fatalf("SECURITY.md does not point at %s", design)
	}
	for _, want := range []string{"awaiting the owner's approval", gate} {
		if !strings.Contains(pointer, want) {
			t.Errorf("SECURITY.md's line about %s does not say the design is not built: it must carry %q\n\nline: %s",
				design, want, pointer)
		}
	}
	if _, err := os.Stat("../../" + design); err != nil {
		t.Fatalf("SECURITY.md points at %s, which is not there: %v", design, err)
	}
}
