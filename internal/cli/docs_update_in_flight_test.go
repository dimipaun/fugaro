package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsUpdateInFlightInstallationRowCarriesTheOrchestrator (mobile plan task
// P5.1b): the resident orchestrator's account and section 4.3's bindings ride
// the installation step of `fugaro update`, so
// docs/design/update-in-flight.md's own row for that step has to say so — and
// say the half that matters for a run in flight, which is that an installation
// that has not opted in creates nothing.
//
// The row is the step's, and not a row of its own, because the document's table
// is one row per `initStages` step (its own sentence, "The steps are
// `initStages` … in the order the update door runs them") and this task adds no
// step: the account is one more resource in an apply that already happens, which
// is the whole of the HARD RULE — `fugaro update` is the upgrade, with no
// second operator command.
func TestDocsUpdateInFlightInstallationRowCarriesTheOrchestrator(t *testing.T) {
	const path = "docs/design/update-in-flight.md"
	doc := readDoc(t, path)
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| installation |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("%s has no `| installation |` row: the table is the audit of every update step, and the step the resident orchestrator's account is created by is gone", path)
	}
	for _, want := range []string{"orchestrator.tf", "P5.1b", "no-op", "has not opted in", "null", "CarriedOrchestrator"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's installation row does not say %q: the account rides that step, and the row is where a reader learns that the step creates nothing for an installation that did not ask for one\n\nrow: %s", path, want, row)
		}
	}

	// The row names a file this repository ships: a doc that cites the module
	// the account is created by is worth nothing if the module moved.
	if _, err := os.Stat("../../deploy/terraform/gcp/modules/installation/orchestrator.tf"); err != nil {
		t.Errorf("%s's installation row names deploy/terraform/gcp/modules/installation/orchestrator.tf, which this tree does not hold: %v", path, err)
	}

	// And the row's "a key no release writes yet" is a fact rather than a hope:
	// what writes a tfvars key is a field of the installation root's spec
	// (internal/infra's InstallationSpec, whose json tags are those keys), and
	// the release that adds the field is the one that turns the switch on for
	// installations that opt in. Until then no release hands the key to any
	// installation, which is what makes the step a no-op for every one of them —
	// and that release rewrites this row, and this assertion, with it.
	//
	// The carry the row names is the other direction and not a writer: it keeps
	// a block the installation's OWN document already holds, so an installation
	// whose owner opted in by hand is not planned away by the very update that
	// would otherwise rewrite the document without it. Both halves are read
	// here, because the row states both.
	tfvars, err := os.ReadFile("../../internal/infra/tfvars.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tfvars), `json:"orchestrator`) {
		t.Errorf("internal/infra/tfvars.go carries an orchestrator key in its spec: %s's installation row says no release writes one yet, so the step creates nothing for any installation — the release that adds the writer turns the switch on for installations that opt in, and updates that row in the same sweep", path)
	}
	if !strings.Contains(string(tfvars), "func CarriedOrchestrator(") {
		t.Errorf("internal/infra/tfvars.go has no CarriedOrchestrator: %s's installation row says a block the installation holds is carried across an update, and a prepare that drops it hands Terraform the document of the account's and its five bindings' removal — which D1551 refuses for the identity", path)
	}
}
