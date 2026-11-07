package tf

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The workspace root's plan cover, tested over plan JSON the way
// deploy/terraform/gcp/roots/workspace plans it: the workspace's own service
// account, and the one grant that reaches the workspace's own database
// (workspace.go, docs/workspaces/implementation-plan.md W3).
const (
	wsProject = "workspace-alpha-1234"
	wsEmail   = WorkspaceServiceAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
)

// wsPlan is a plan of the resource changes below, in terraform's own JSON
// shape including the mode a change carries.
func wsPlan(t *testing.T, changes ...string) *Plan {
	t.Helper()
	return mustPlan(t, `{"resource_changes":[`+strings.Join(changes, ",")+`]}`)
}

func wsActions(acts ...string) string {
	q := make([]string, 0, len(acts))
	for _, a := range acts {
		q = append(q, `"`+a+`"`)
	}
	return "[" + strings.Join(q, ",") + "]"
}

// wsSA is the workspace's own service account as the root's plan writes it:
// both of its names literal, which is why the root is given them as variables.
func wsSA(project, accountID string, acts ...string) string {
	return `{"address":"google_service_account.workspace","mode":"managed","type":"google_service_account","change":{"actions":` +
		wsActions(acts...) + `,"after":{"project":"` + project + `","account_id":"` + accountID + `"}}}`
}

// wsKnownGrant is the root's one grant with the member terraform already knows.
func wsKnownGrant(project, member, role string, acts ...string) string {
	return `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"google_project_iam_member","change":{"actions":` +
		wsActions(acts...) + `,"after":{"project":"` + project + `","role":"` + role + `","member":"` + member + `"}}}`
}

// wsComputedGrant is the same grant as the root really plans it: the member is
// the address of the account this plan creates, so terraform knows it only at
// apply and the plan carries after_unknown instead of a value.
func wsComputedGrant(project, role string, acts ...string) string {
	return `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"google_project_iam_member","change":{"actions":` +
		wsActions(acts...) + `,"after":{"project":"` + project + `","role":"` + role + `"},"after_unknown":{"member":true}}}`
}

func wsWhy(t *testing.T, p *Plan) string {
	t.Helper()
	return strings.Join(WorkspaceNotCovered(p, wsProject, WorkspaceServiceAccountID), "; ")
}

func TestWorkspacePlanIsTheServiceAndItsOneGrant(t *testing.T) {
	covered := []struct {
		name    string
		changes []string
	}{
		{"the root's own plan, with the member terraform computes", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
		}},
		{"the same plan converged: both resources are no-ops", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "no-op"),
		}},
		{"the member known, bare", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant(wsProject, wsEmail, WorkspaceDatabaseRole, "create"),
		}},
		{"the member known, as the root writes it", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant(wsProject, "serviceAccount:"+wsEmail, WorkspaceDatabaseRole, "create"),
		}},
		{"the account's display name updated, the grant held", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "update"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "update"),
		}},
	}
	for _, c := range covered {
		t.Run(c.name, func(t *testing.T) {
			if why := wsWhy(t, wsPlan(t, c.changes...)); why != "" {
				t.Errorf("the workspace root's own plan must be covered, got: %s", why)
			}
		})
	}
}

// TestWorkspacePlanRefusesAJobABucketAndARepository holds spec §9's "nothing
// else": the workspace's own project carries no resource but its own service
// account and that account's one grant, and every type outside the root is
// refused by name however harmless it looks.
func TestWorkspacePlanRefusesAJobABucketAndARepository(t *testing.T) {
	other := []struct {
		typ  string
		addr string
	}{
		{"google_cloud_run_v2_job", "google_cloud_run_v2_job.workspace"},
		{"google_cloud_run_v2_service", "google_cloud_run_v2_service.workspace"},
		{"google_storage_bucket", "google_storage_bucket.runs"},
		{"google_project_service", "google_project_service.identitytoolkit"},
		{"google_firebase_project", "google_firebase_project.this"},
		{"google_artifact_registry_repository", "google_artifact_registry_repository.images"},
		{"google_secret_manager_secret", "google_secret_manager_secret.model"},
	}
	for _, c := range other {
		t.Run(c.typ, func(t *testing.T) {
			p := wsPlan(t,
				wsSA(wsProject, WorkspaceServiceAccountID, "create"),
				wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
				`{"address":"`+c.addr+`","mode":"managed","type":"`+c.typ+`","change":{"actions":["create"],"after":{}}}`)
			why := wsWhy(t, p)
			if why == "" {
				t.Fatalf("a %s in the workspace's own project must be refused: a workspace holds no job, no runs bucket and no repository (spec §9)", c.typ)
			}
			if !strings.Contains(why, "no job, no runs bucket and no repository") {
				t.Errorf("the refusal must say what a workspace does not hold, got: %s", why)
			}
		})
	}
}

// TestWorkspacePlanRefusesAGrantToAnotherPrincipal is the security half of the
// cover: the workspace's database role is bound to the workspace's own service
// account and to nothing else, because any other member of that grant can read
// the record and the admitted-token hashes (spec §2, §8).
func TestWorkspacePlanRefusesAGrantToAnotherPrincipal(t *testing.T) {
	others := []string{
		"user:someone@example.com",
		"group:ops@example.com",
		"domain:example.com",
		"allUsers",
		"allAuthenticatedUsers",
		"serviceAccount:" + FugaroAccounts[0] + "@" + wsProject + ".iam.gserviceaccount.com",
		"serviceAccount:fugaro-member@member-project-5678.iam.gserviceaccount.com",
	}
	for _, member := range others {
		t.Run(member, func(t *testing.T) {
			p := wsPlan(t,
				wsSA(wsProject, WorkspaceServiceAccountID, "create"),
				wsKnownGrant(wsProject, member, WorkspaceDatabaseRole, "create"))
			why := wsWhy(t, p)
			if why == "" {
				t.Fatalf("a grant of the workspace's database role to %s must be refused", member)
			}
			if !strings.Contains(why, "reached by "+wsEmail+" and by nobody else") {
				t.Errorf("the refusal must name the account the workspace's database is reached by, got: %s", why)
			}
		})
	}
}

func TestWorkspacePlanRefusesASecondRoleAnotherProjectAndAKey(t *testing.T) {
	refused := []struct {
		name    string
		changes []string
		want    string
	}{
		{"another role on the workspace's account", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant(wsProject, wsEmail, "roles/editor", "create"),
		}, "holds " + WorkspaceDatabaseRole + " and nothing else"},
		{"the database role in another project", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant("member-project-5678", wsEmail, WorkspaceDatabaseRole, "create"),
		}, "the workspace's own database is"},
		{"the account in another project", []string{
			wsSA("member-project-5678", WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
		}, "an account in another project"},
		{"another account id", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
		}, "the workspace's own service account is"},
		{"a service account key", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			`{"address":"google_service_account_key.workspace","mode":"managed","type":"google_service_account_key","change":{"actions":["create"],"after":{}}}`,
		}, "a service account key"},
		{"an authoritative binding", []string{
			`{"address":"google_project_iam_binding.workspace_database","mode":"managed","type":"google_project_iam_binding","change":{"actions":["create"],"after":{"role":"` + WorkspaceDatabaseRole + `"}}}`,
		}, "an authoritative IAM resource"},
		{"a delete of the workspace's account", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "delete"),
		}, "actions delete"},
		{"a replace of the workspace's account", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "delete", "create"),
		}, "actions delete, create"},
		{"a data source", []string{
			`{"address":"data.google_project.this","mode":"data","type":"google_project","change":{"actions":["read"],"after":{}}}`,
		}, "a data source"},
		{"the member computed with no account in the plan", []string{
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
		}, "does not create the workspace's own service account"},
		{"the member computed while another account is created", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, WorkspaceDatabaseRole, "create"),
		}, "the workspace's own service account is"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsPlan(t, c.changes...))
			if why == "" {
				t.Fatalf("this plan must be refused: %s", c.name)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("the refusal must say why, want a sentence with %q, got: %s", c.want, why)
			}
		})
	}
}

// TestWorkspacePlanRefusesNoPlan is the nil case every cover has: a plan that
// could not be read is not a plan to confirm.
func TestWorkspacePlanRefusesNoPlan(t *testing.T) {
	if why := WorkspaceNotCovered(nil, wsProject, WorkspaceServiceAccountID); len(why) == 0 {
		t.Fatal("a nil plan must be refused")
	}
}

// TestWorkspaceRootDeclaresTheTypesTheCoverHolds reads the root itself and
// holds its resources to the cover's two types: the file is what the embed
// ships and what an operator applies, so a third resource added there is a
// workspace holding something else, and it fails here rather than in a plan the
// cover would have refused anyway.
func TestWorkspaceRootDeclaresTheTypesTheCoverHolds(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/terraform/gcp/roots/workspace/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{`).FindAllStringSubmatch(string(raw), -1) {
		types[m[1]] = true
	}
	if len(types) != 2 || !types[workspaceAccountType] || !types[workspaceGrantType] {
		t.Fatalf("the workspace root must declare exactly the workspace's account and its one grant, got %v", types)
	}
}
