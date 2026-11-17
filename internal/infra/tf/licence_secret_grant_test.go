package tf

import (
	"strings"
	"testing"
)

// TestLicenceSecretGrantIsTheRenewalContainerAlone: the renewal job's one
// Secret Manager binding is the ONLY grant in this file whose target is checked
// — grantRules reads a principal kind and a role, never a secret_id — and
// licenceSecretGrant is the whole of that check (plan K4.2). What it fences is
// the one grant of the cover that is a read of a VALUE: `fugaro licence set`
// writes the per-key renewal secret into that container, so a plan that pointed
// this binding at another container of the project would hand the renewal job
// the credentials of some other part of the installation under the ordinary
// confirmation.
//
// The rows are the guard's four directions and its control: the container the
// run's own licence spec derived (accepted), another container of the same
// project, a cover with no container at all — the fail-closed direction, an
// installation that deploys no coordinator, where there is nothing to compare
// with — a secret_id the plan does not know, and a condition on the binding
// (the module writes none). The last three are refused by licenceSecretGrant
// itself, and the second by nothing else: member, role and type are the
// modules' own.
//
// Mutation (verified, run and restored): licenceSecretGrant returning "" at its
// first line leaves every other test of this package green — the golden plan
// still passes, because its container matches whatever the cover is handed —
// and this test alone fails, on each of the five refusals below, which is what
// makes it the pin on the check rather than on the rows around it.
func TestLicenceSecretGrantIsTheRenewalContainerAlone(t *testing.T) {
	const member = "serviceAccount:" + LicenceAccountID + "@proj-1.iam.gserviceaccount.com"
	const role = "projects/proj-1/roles/" + LicenceRenewalRole
	// The renewal container's ID is the run's own licence spec's derivation of
	// the Fugaro project's name (infra.LicenceRenewalSecretID, a
	// gcp.ProjectSecretID): this package cannot call it (internal/infra imports
	// this one), so the shape stands here as the plan carries it — a literal,
	// never a computed attribute.
	const container = "fugaro-project-proj-1-licence-renewal-0e382f732fd21bbd"

	coverWith := func(container string) Cover {
		c := testCover
		c.LicenceRenewalSecret = container
		return c
	}
	for _, tc := range []struct {
		name string
		// secretID is what the plan binds the role on; cover is the container
		// the cover was handed (the spec's, or none).
		secretID, cover string
		condition       []any
		unknown         map[string]any
		covered         bool
		reason          string
	}{
		{
			name: "the renewal container the spec derives", secretID: container, cover: container,
			covered: true,
		},
		{
			name:     "another container of the same project",
			secretID: "fugaro-project-proj-1-licence-renewal-1a2b3c4d5e6f7a8b", cover: container,
			reason: "not this installation's renewal container",
		},
		{
			name:     "the container by another spelling",
			secretID: "fugaro-licence-renew", cover: container,
			reason: "not this installation's renewal container",
		},
		{
			// No licence job in this run's spec at all: an installation that
			// deploys no coordinator. There is no container to compare with,
			// and a plan binding the role anyway is refused rather than
			// accepted for want of a value to check it against.
			name:     "a cover with no renewal container",
			secretID: container, cover: "",
			reason: "not this installation's renewal container",
		},
		{
			name:     "a container the plan does not know",
			secretID: "", cover: container,
			unknown: map[string]any{"secret_id": true},
			reason:  "not known",
		},
		{
			// The container is the fence, and the module adds no condition to
			// it: a condition here would be a second, unread narrowing.
			name:     "the right container under a condition",
			secretID: container, cover: container,
			condition: []any{map[string]any{"title": "fugaro-licence-renewal", "expression": "true", "description": nil}},
			reason:    "under a condition",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := map[string]any{"member": member, "role": role}
			if tc.secretID != "" {
				after["secret_id"] = tc.secretID
			}
			if tc.condition != nil {
				after["condition"] = tc.condition
			}
			ch := rc("google_secret_manager_secret_iam_member", after, tc.unknown, "create")
			got := coverWith(tc.cover).NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			if (len(got) == 0) != tc.covered {
				t.Fatalf("the renewal role on %s to the licence account: NotCovered = %q, covered want %v", tc.secretID, got, tc.covered)
			}
			if !tc.covered && !strings.Contains(strings.Join(got, "\n"), tc.reason) {
				t.Errorf("the renewal role on %s stopped for another reason: %q, want %q", tc.secretID, got, tc.reason)
			}
		})
	}
}
