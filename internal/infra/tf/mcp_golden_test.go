package tf

import (
	"slices"
	"strings"
	"testing"
)

// The front door is the owner's second opt-in, and the goldens are real plans
// of installations that did not pass it.
//
// D934 is OFF by default, and the two things it keeps off an installation that
// asked only for the coordinator's read-only view are the ones asserted here:
// the Cloud Run service, and the `allUsers` invoker binding D920 puts on it.
// The variable is what decides both — the module's local is
// `var.mcp_server != null && local.coordinator_on`, so no fixture's tfvars
// carry an mcp_server key and no plan of one can create either resource.
//
// What this catches is the half a Terraform reader would have to notice by
// hand: a local that dropped the `var.mcp_server != null` term, which would put
// a public write endpoint on every installation whose coordinator is on — and
// nothing else in the tree would fail, because the resources are admitted
// types, the account is the coordinator's own, and the plan-level test in the
// module reads the expression rather than a plan. The non-vacuity check at the
// end is what keeps the assertion honest: the coordinator's own opt-in golden
// has to be in the set this test reads, and the coordinator is what the flag's
// other half is about (TestTheCoordinatorIsInTheOptInGoldensOnly lists those
// resources).
func TestNoGoldenPlanCreatesTheFrontDoor(t *testing.T) {
	goldens := []string{"installation", "installation-accounts", "installation-managed", "installation-credentials",
		"installation-adopt", "installation-coordinator", "firebase", "firebase-same-project", "firebase-adopt", "firebase-coordinator"}
	var sawCoordinator bool
	for _, name := range goldens {
		for _, rc := range goldenPlan(t, name).ResourceChanges {
			if strings.Contains(rc.Address, "front_door") {
				t.Errorf("%s plans %s: the front door is OFF until the owner passes the flag, and no golden's tfvars pass it (D934)", name, rc.Address)
			}
			if strings.Contains(rc.Address, "coordinator") {
				sawCoordinator = true
			}
			if rc.Type == "google_cloud_run_v2_service_iam_member" {
				if got := str(rc.Change.After["member"]); got == "allUsers" {
					t.Errorf("%s admits everyone to %s: the front door's public binding is D920's and only an installation that opted in gets it", name, rc.Address)
				}
			}
		}
	}
	if !sawCoordinator {
		t.Fatal("no golden plan carries a coordinator resource, so this test read the wrong plans: the flag's other half is the coordinator's own opt-in")
	}
	if !slices.Contains(goldens, "installation-coordinator") {
		t.Fatal("the coordinator's opt-in golden is not in the set this test reads")
	}
}
