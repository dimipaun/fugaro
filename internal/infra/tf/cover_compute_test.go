package tf

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The compute root is never covered by the run's one confirmation (its stage
// asks its own typed one), but its GRANTS are still held to the modules' rules:
// GrantsNotCovered is that half of the cover, and it is what makes the Batch
// Job Editor condition check reach the one root that grants the role.

const (
	ccReaper  = "serviceAccount:fugaro-reaper@" + cjProject + ".iam.gserviceaccount.com"
	ccPreload = "serviceAccount:fugaro-preload@" + cjProject + ".iam.gserviceaccount.com"
	// A run account as the project holds it: not of the derived job-account
	// shape, which is why the compute cover names it in Accounts.
	ccRun = "serviceAccount:fugaro-acme-web@" + cjProject + ".iam.gserviceaccount.com"
)

func ccRight() map[string]any {
	return jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, gcp.LauncherJobsCondition(cjProject, cjRegion))
}

// roles/batch.jobsEditor is the launchers' (people's) grant, and only under the
// Fugaro job-name condition: even under exactly that condition it is never
// covered for a Fugaro service account. A reaper that holds it can create
// jobs, which is a second launcher, not a janitor (design §3.7).
func TestCoverBatchJobsEditorNeverToAnAccount(t *testing.T) {
	c := Cover{Projects: []string{cjProject}, Region: cjRegion, Listed: []string{cbPerson}, Accounts: []string{ccRun}}
	if got := c.NotCovered(jobsGrant(cbPerson, cjProject, []any{ccRight()}), nil); len(got) != 0 {
		t.Fatalf("the launcher's conditioned grant is refused: %v", got)
	}
	for _, m := range []string{ccReaper, ccPreload, ccRun, cbJobSA} {
		t.Run(m, func(t *testing.T) {
			p := jobsGrant(m, cjProject, []any{ccRight()})
			if got := c.NotCovered(p, nil); len(got) == 0 {
				t.Error("NotCovered covers roles/batch.jobsEditor for a Fugaro service account")
			}
			if got := c.GrantsNotCovered(p); len(got) == 0 {
				t.Error("GrantsNotCovered covers roles/batch.jobsEditor for a Fugaro service account")
			}
		})
	}
}

func grantChange(addr, typ string, after map[string]any, actions ...string) ResourceChange {
	if len(actions) == 0 {
		actions = []string{"create"}
	}
	return ResourceChange{Address: addr, Type: typ, Change: Change{Actions: actions, After: after}}
}

// GrantsNotCovered checks every grant of a plan as NotCovered does, and
// nothing else: the compute root's scheduler job (which calls Cloud Build, not
// a Cloud Run job) and its network resources are its stage's own confirmation's
// business, not the grant rules'.
func TestGrantsNotCovered(t *testing.T) {
	c := Cover{Projects: []string{cjProject}, Region: cjRegion, Listed: []string{cbPerson, "user:operator@example.com"}, Accounts: []string{ccRun}}
	const proj = "google_project_iam_member"
	const sa = "google_service_account_iam_member"
	preloadName := "projects/" + cjProject + "/serviceAccounts/fugaro-preload@" + cjProject + ".iam.gserviceaccount.com"
	for _, tc := range []struct {
		name    string
		rc      ResourceChange
		covered bool
	}{
		{"the launchers' jobsEditor under the module's condition", jobsGrant(cbPerson, cjProject, []any{ccRight()}).ResourceChanges[0], true},
		{"the launchers' jobsEditor unconditioned", jobsGrant(cbPerson, cjProject, []any{}).ResourceChanges[0], false},
		{"the launchers' jobsEditor under another region's condition", jobsGrant(cbPerson, cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, gcp.LauncherJobsCondition(cjProject, "us-west1"))}).ResourceChanges[0], false},
		{"a run account's agentReporter", grantChange("a", proj, map[string]any{"project": cjProject, "role": "roles/batch.agentReporter", "member": ccRun}), true},
		{"agentReporter to an account the cover does not name", grantChange("a", proj, map[string]any{"project": cjProject, "role": "roles/batch.agentReporter",
			"member": "serviceAccount:stranger@" + cjProject + ".iam.gserviceaccount.com"}), false},
		{"agentReporter to a person", grantChange("a", proj, map[string]any{"project": cjProject, "role": "roles/batch.agentReporter", "member": cbPerson}), false},
		{"a grant in another project", grantChange("a", proj, map[string]any{"project": "proj-other", "role": "roles/batch.agentReporter", "member": ccRun}), false},
		{"an operator's actAs on the preload account", grantChange("a", sa, map[string]any{"service_account_id": preloadName, "role": "roles/iam.serviceAccountUser", "member": "user:operator@example.com"}), true},
		{"a domain's actAs on the preload account", grantChange("a", sa, map[string]any{"service_account_id": preloadName, "role": "roles/iam.serviceAccountUser", "member": "domain:example.com"}), false},
		{"an editor grant", grantChange("a", proj, map[string]any{"project": cjProject, "role": "roles/editor", "member": cbPerson}), false},
		{"an authoritative binding", grantChange("a", "google_project_iam_binding", map[string]any{"project": cjProject, "role": "roles/viewer"}), false},
		{"a service account key", grantChange("a", "google_service_account_key", map[string]any{}), false},
		{"a scheduler job that calls Cloud Build", grantChange("a", "google_cloud_scheduler_job", map[string]any{"project": cjProject,
			"http_target": []any{map[string]any{"uri": "https://cloudbuild.googleapis.com/v1/projects/" + cjProject + "/builds"}}}), true},
		{"a firewall rule", grantChange("a", "google_compute_firewall", map[string]any{"project": cjProject}), true},
		{"the delete of a grant (the guard's business)", grantChange("a", proj, nil, "delete"), true},
		{"the replace of a grant to a domain", grantChange("a", sa, map[string]any{"service_account_id": preloadName, "role": "roles/iam.serviceAccountUser", "member": "domain:example.com"}, "delete", "create"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.GrantsNotCovered(&Plan{ResourceChanges: []ResourceChange{tc.rc}})
			if tc.covered && len(got) != 0 {
				t.Fatalf("refused: %v", got)
			}
			if !tc.covered && len(got) == 0 {
				t.Fatal("covered, want refused")
			}
		})
	}
	if got := c.GrantsNotCovered(nil); len(got) == 0 {
		t.Error("a nil plan is covered")
	}
}
