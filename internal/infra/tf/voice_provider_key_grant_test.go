package tf

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestVoiceProviderKeyGrantIsTheHopsContainerAlone is the hop's second and last
// grant, and the only credential the forwarding half holds (P5.3.5, the voice
// design's §4.4 point 4): the provider-key container secrets.tf creates, bound
// to the hop's own account and to no other member.
//
// It is the renewal job's test one container over, and it matters more here:
// the hop is the one account of this installation behind an endpoint ANYONE on
// the internet may call (voice.tf's allUsers binding, SECURITY.md), so a plan
// that pointed this binding at another container of the project — the
// credentials bundle, a repository's own secret, the front door's App key —
// would hand a public endpoint the credentials of another part of the
// installation under the ordinary confirmation. grantRules reads a principal
// kind and a role and never a secret_id, so voiceProviderKeyGrant is the whole
// of what stands between those two.
//
// The rows are the container's four directions and the two controls: the
// container the module writes (accepted), another container of the same
// project, the container by another spelling, a secret_id the plan does not
// know, the right container under a condition the module never writes, a
// PROJECT-wide accessor to this account (which is the credentials service's
// role and not the hop's — the per-secret binding is the only one this tree
// grants it) and the version-adder role on the right container (creating values
// is the operators' act, never the process that reads them).
//
// Mutation (verified, run and restored): voiceProviderKeyGrant returning "" at
// its first line leaves the golden plan passing — its container is the module's
// own — and this test alone fails, on every refusal below but the last two,
// which are the rows'.
func TestVoiceProviderKeyGrantIsTheHopsContainerAlone(t *testing.T) {
	const member = "serviceAccount:" + VoiceAccountID + "@proj-1.iam.gserviceaccount.com"
	const accessor = "roles/secretmanager.secretAccessor"
	// The container's ID is a literal in the module — Terraform cannot compute a
	// gcp.ProjectSecretID, whose suffix is a hash — and the Go constant is the
	// same string, which deploy/terraform's
	// TestVoiceProviderKeysContainerIsTheGoSpelling holds together. Nothing of
	// this test reads that file, so the literal is spelled here the way the plan
	// carries it: an ID and never a computed attribute.
	const container = "fugaro-voice-provider-keys"

	for _, tc := range []struct {
		name string
		// secretID is what the plan binds the role on.
		secretID  string
		condition []any
		unknown   map[string]any
		covered   bool
		reason    string
	}{
		{
			name: "the provider-key container the module writes", secretID: container,
			covered: true,
		},
		{
			name:     "another container of the same project",
			secretID: "fugaro-project-proj-1-creds-bundle-0e382f732fd21bbd",
			reason:   "not its provider-key container",
		},
		{
			// The nearest miss there is: the same name, one word short of the
			// one the process reads. A hop that read THIS container would find
			// no provider key in it and refuse every call, which is the shape a
			// hand edit drifts into.
			name:     "the container by another spelling",
			secretID: "fugaro-voice-provider-key",
			reason:   "not its provider-key container",
		},
		{
			name:     "a container the plan does not know",
			secretID: "", unknown: map[string]any{"secret_id": true},
			reason: "not known",
		},
		{
			// The container is the fence and the module adds no condition to
			// it: Secret Manager scopes a binding per secret, so a condition
			// here would be a second narrowing nobody reads.
			name:     "the right container under a condition",
			secretID: container,
			condition: []any{map[string]any{"title": "fugaro-voice-provider-keys",
				"expression": "true", "description": nil}},
			reason: "under a condition",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := map[string]any{"member": member, "role": accessor}
			if tc.secretID != "" {
				after["secret_id"] = tc.secretID
			}
			if tc.condition != nil {
				after["condition"] = tc.condition
			}
			ch := rc("google_secret_manager_secret_iam_member", after, tc.unknown, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			if (len(got) == 0) != tc.covered {
				t.Fatalf("the accessor on %s to the hop's account: NotCovered = %q, covered want %v", tc.secretID, got, tc.covered)
			}
			if !tc.covered && !strings.Contains(strings.Join(got, "\n"), tc.reason) {
				t.Errorf("the accessor on %s stopped for another reason: %q, want %q", tc.secretID, got, tc.reason)
			}
		})
	}

	// The controls, one row each, and both are the rows' rather than the
	// guard's: the hop holds the accessor on ONE container and no project-wide
	// one (that role's project row names the credentials service alone, DS2.2),
	// and it never ADDS a value (secretVersionAdder is the operators' row, the
	// person who holds the provider keys and types the gcloud line secrets.tf
	// documents).
	projectWide := rc("google_project_iam_member",
		map[string]any{"member": member, "role": accessor, "project": "proj-1"}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{projectWide}}, nil); len(got) == 0 {
		t.Errorf("a project-wide %s to the hop's account is covered: the hop reads ONE container, and the project-wide accessor is the credentials service's own role (D1931)", accessor)
	}
	adder := rc("google_secret_manager_secret_iam_member",
		map[string]any{"member": member, "role": "roles/secretmanager.secretVersionAdder", "secret_id": container},
		map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{adder}}, nil); len(got) == 0 {
		t.Errorf("the version-adder role on the provider-key container to the hop's account is covered: the value is added by the person who holds the provider keys (secrets.tf), never by the process that reads them")
	}

	// And the constant the guard compares against is the module's own literal:
	// one string in two languages, with the Terraform side's test beside it.
	if gcp.VoiceProviderKeysSecret != container {
		t.Errorf("gcp.VoiceProviderKeysSecret = %q, want %q: the guard above is only as good as this string", gcp.VoiceProviderKeysSecret, container)
	}
}
