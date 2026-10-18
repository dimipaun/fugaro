package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
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
	section := checklist[i:]
	for _, want := range []string{"never `belong`", "run by you", "NOT RUN", "**No agent runs any step of this check**", "P=fugaro-dev", "never a token, a key"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 60 does not say %q", want)
		}
	}
	// A step's commands have to be commands: `fugaro status` never existed
	// (internal/cli/root.go adds no such command), and the one run's own record
	// is `fugaro diagnose RUN --json`, whose row carries the execution
	// (internal/cli/diagnose.go's Diagnosis.Row).
	for _, want := range []string{"fugaro diagnose", "fugaro runs ls", ".row.execution"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 60 does not read a run through a command that exists: it does not say %q", want)
		}
	}
	if strings.Contains(section, "fugaro status") {
		t.Errorf("Check 60 reads a run with `fugaro status`, which no command implements: the one-run read is `fugaro diagnose RUN --json` (.row.execution), the list is `fugaro runs ls`")
	}
}

// TestDocsBudgetTokenChannelSection72IsTheMergedTree: Check 60 sends the owner
// to §7.2 for the moved object's grant, its lifecycle rule and the shape of its
// take, so that section has to describe the tree the owner is standing in
// rather than the two-step plan D19 priced — the 2026-10-18 review found it
// three versions behind (title `fugaro-launchers-runs` and a runs/-only
// expression, `matches_prefix = ["runs/"]`, and a take with "two shapes and one
// switch" shipping a runs/-keyed fatal delete). Each fact is asserted against
// the merged code's own name for it, so the section cannot drift from
// `gcp.LauncherBucketConditionTitle` or from `token.TakeObject`'s contract
// without this failing.
func TestDocsBudgetTokenChannelSection72IsTheMergedTree(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "### 7.2 ")
	j := strings.Index(doc, "### 7.3 ")
	if i < 0 || j < i {
		t.Fatalf("%s has no §7.2 followed by §7.3: the section the checklist points at is gone", path)
	}
	sec := doc[i:j]
	for _, want := range []string{
		// The launchers' widened condition and its new title, in Terraform
		// and in gcp.LauncherBucketConditionTitle (iam.tf's precondition
		// pins both).
		gcp.LauncherBucketConditionTitle,
		// The one-day rule that moved with the object.
		`matches_prefix = ["runs/", "tokens/"]`,
		// The one take shape, the read-only one.
		"never calls delete",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s §7.2 does not say %q: the checklist sends the owner here for the moved token's grant, its rule and its take", path, want)
		}
	}
	if strings.Contains(sec, "two shapes and one switch") {
		t.Errorf("%s §7.2 still describes the take as \"two shapes and one switch\": the merged tree has one shape, the read-only one that never deletes (token.TakeObject)", path)
	}
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
