package infra

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The two addresses the owner's run of 2026-11-27 refused on the sandbox: the
// job's own accessor on the App key's container, and the job account's invoker
// grant on the credentials service, one set per workflow. Both are ForceNew in
// their own module, so both are REPLACED — not retired — when an update moves
// the container or the service under them, which is what left the run with a
// next command its own guard refused.
const (
	repointAppKey = `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["github-app-key"]`
	repointInvoke = `module.repo.module.workflow["web"].google_cloud_run_v2_service_iam_member.credentials[0]`
)

// grantChange is one `*_iam_member` change, with the two halves terraform
// writes for it.
func grantChange(addr, typ string, actions []string, before, after map[string]any) tf.ResourceChange {
	return tf.ResourceChange{Address: addr, Mode: "managed", Type: typ,
		Change: tf.Change{Actions: actions, Before: before, After: after}}
}

// grant is one half of such a change: the principal and the role the binding
// is, and whatever else the case needs beside them.
func grant(member, role string, rest map[string]any) map[string]any {
	out := map[string]any{"member": member, "role": role}
	for k, v := range rest {
		out[k] = v
	}
	return out
}

const (
	jobAccount  = "serviceAccount:job@proj-1234.iam.gserviceaccount.com"
	appKeyRole  = "roles/secretmanager.secretAccessor"
	invokerRole = "roles/run.invoker"
)

// The rule, driven through the REAL guard: the list it computes admits the
// plan, and everything else the same plan could carry stays refused. The
// retirement's own list is asserted to refuse the re-point, because that is the
// hole the owner fell into — a re-pointed address is one this spec still
// produces, so it is not in the set `--retire` is bounded to, and the command
// the summary named could never admit it.
func TestRepointedGrantAllowDeleteAdmitsOnlyTheRepoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		rc   tf.ResourceChange
		want bool
	}{
		{
			name: "the App key's accessor re-pointed at the project's container",
			rc: grantChange(repointAppKey, "google_secret_manager_secret_iam_member",
				[]string{"delete", "create"},
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "fugaro-acme-sandbox-github-app-key-9f2"}),
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "fugaro-project-acme-github-app-key-3c1"})),
			want: true,
		},
		{
			name: "the invoker grant re-pointed at the service's new region",
			rc: grantChange(repointInvoke, "google_cloud_run_v2_service_iam_member",
				[]string{"delete", "create"},
				grant(jobAccount, invokerRole, map[string]any{"name": "fugaro-credentials", "location": "us-east5"}),
				grant(jobAccount, invokerRole, map[string]any{"name": "fugaro-credentials", "location": "europe-west1"})),
			want: true,
		},
		{
			name: "a replace that moves the principal is a different binding",
			rc: grantChange(repointAppKey, "google_secret_manager_secret_iam_member",
				[]string{"delete", "create"},
				grant("serviceAccount:old@proj-1234.iam.gserviceaccount.com", appKeyRole, map[string]any{"secret_id": "a"}),
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "b"})),
		},
		{
			name: "a replace that moves the role is a different binding",
			rc: grantChange(repointAppKey, "google_secret_manager_secret_iam_member",
				[]string{"delete", "create"},
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "a"}),
				grant(jobAccount, "roles/secretmanager.viewer", map[string]any{"secret_id": "b"})),
		},
		{
			name: "a pure delete is no replace and stays refused",
			rc: grantChange(repointAppKey, "google_secret_manager_secret_iam_member",
				[]string{"delete"},
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "a"}), nil),
		},
		{
			name: "a half terraform redacted is a comparison this rule cannot make",
			rc: grantChange(repointAppKey, "google_secret_manager_secret_iam_member",
				[]string{"delete", "create"},
				map[string]any{},
				grant(jobAccount, appKeyRole, map[string]any{"secret_id": "b"})),
		},
		{
			name: "another type's replace is not a grant's",
			rc: grantChange(`module.repo.google_service_account.job`, "google_service_account",
				[]string{"delete", "create"},
				grant(jobAccount, appKeyRole, map[string]any{"account_id": "before"}),
				grant(jobAccount, appKeyRole, map[string]any{"account_id": "after"})),
		},
		{
			name: "a grant type no retirement rule has decided",
			rc: grantChange(`module.repo.google_bigquery_dataset_iam_member.x`, "google_bigquery_dataset_iam_member",
				[]string{"delete", "create"},
				grant(jobAccount, "roles/viewer", map[string]any{"dataset_id": "before"}),
				grant(jobAccount, "roles/viewer", map[string]any{"dataset_id": "after"})),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &tf.Plan{FormatVersion: "1.2", ResourceChanges: []tf.ResourceChange{tc.rc}}

			var want []string
			if tc.want {
				want = []string{tc.rc.Address}
			}
			if got := RepointedGrantAllowDelete(plan); !slices.Equal(got, want) {
				t.Fatalf("RepointedGrantAllowDelete = %v, want %v", got, want)
			}
			// The real guard, over the list this rule computed: what the
			// caller admits, it must also apply — and what it does not, stays
			// refused. One assertion each way, so the case cannot pass by
			// naming nothing and refusing everything.
			err := tf.Guard(plan, RepointedGrantAllowDelete(plan))
			if tc.want && err != nil {
				t.Errorf("the real guard refuses a plan this rule admits: %v", err)
			}
			if !tc.want && err == nil {
				t.Errorf("the real guard applies a removal this rule did not admit")
			}
			if !tc.want {
				return
			}
			if err := tf.Guard(plan, RetireAllowDelete(plan, nil)); err == nil {
				t.Errorf("the retirement's list admits a re-pointed grant, so this rule is not what closed the hole")
			}
		})
	}
}
