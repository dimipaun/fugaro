package tf

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The workspace root's plan cover, tested over plan JSON the way
// deploy/terraform/gcp/roots/workspace plans it: the workspace's two accounts —
// the service the bundle runs as and the SA fallback's IAM account — its own
// role, and the one grant that binds the role to the first of them
// (workspace.go, docs/workspace-projects-users/implementation-plan.md WU3).
const (
	wsProject  = "workspace-alpha-1234"
	wsEmail    = WorkspaceServiceAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
	wsIAMEmail = WorkspaceIAMAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
)

// wsRole is the role path the root's grant names, which is what a plan of it
// carries: the cover builds it from the project it was given rather than
// reading it out of the plan, so the two are separate here for the same reason.
var wsRole = WorkspaceDatabaseRole(wsProject)

// wsPlan is a plan of the resource changes below, in terraform's own JSON
// shape including the mode a change carries.
func wsPlan(t *testing.T, changes ...string) *Plan {
	t.Helper()
	return mustPlan(t, `{"resource_changes":[`+strings.Join(changes, ",")+`]}`)
}

// wsPlanCfg is wsPlan with a configuration section: what terraform writes
// beside the changes, and where a computed grant's member expression is written
// as a reference to the account it reads (workspaceComputedMember).
func wsPlanCfg(t *testing.T, cfg string, changes ...string) *Plan {
	t.Helper()
	return mustPlan(t, `{"resource_changes":[`+strings.Join(changes, ",")+
		`],"configuration":{"root_module":{"resources":[`+cfg+`]}}}`)
}

// wsMemberExpr is the grant's member expression as the plan's configuration
// writes it when the member is the address of the account resource named:
// terraform writes an attribute path as the path and the resource
// (testdata/managed_provider_wrapped_project_plan.json's own shape).
func wsMemberExpr(account string) string {
	return `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"google_project_iam_member","expressions":{"member":{"references":["` +
		account + `.member","` + account + `"]}}}`
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

// wsIAM is the SA fallback's IAM account as the root's plan writes it: the
// account id is the root's own literal, which is why the cover can hold it
// without being told it.
func wsIAM(project, accountID string, acts ...string) string {
	return `{"address":"google_service_account.workspace_iam","mode":"managed","type":"google_service_account","change":{"actions":` +
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

// wsRoleChange is the root's custom role as the root plans it: the project, the
// id and the permission list all literal, which is why this cover can hold the
// role to the pinned id and the pinned two.
func wsRoleChange(project, id string, perms ...string) string {
	q := make([]string, 0, len(perms))
	for _, p := range perms {
		q = append(q, `"`+p+`"`)
	}
	return `{"address":"google_project_iam_custom_role.workspace_database","mode":"managed","type":"google_project_iam_custom_role","change":{"actions":["create"],"after":{"project":"` +
		project + `","role_id":"` + id + `","permissions":[` + strings.Join(q, ",") + `]}}}`
}

// wsPinnedRole is the root's own role, as the root declares it.
func wsPinnedRole() string {
	return wsRoleChange(wsProject, WorkspaceDatabaseRoleID, WorkspaceDatabasePermissions...)
}

func wsWhy(t *testing.T, p *Plan) string {
	t.Helper()
	return strings.Join(WorkspaceNotCovered(p, wsProject, WorkspaceServiceAccountID), "; ")
}

func TestWorkspacePlanIsTheTwoAccountsAndItsOneGrant(t *testing.T) {
	covered := []struct {
		name string
		// cfg is the plan's configuration, as the resource entries terraform
		// writes it: the member of a computed grant is only held to the account
		// it reads where the plan carries one (workspaceComputedMember), so a
		// case with no cfg is a plan built by hand without a configuration.
		cfg     string
		changes []string
	}{
		{"the root's own plan, with the member terraform computes", "", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}},
		{"the root's own plan, with the configuration that writes the member terraform computes", wsMemberExpr("google_service_account.workspace"), []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}},
		{"the root's own plan converged: the role, the accounts and the grant are all no-ops", "", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
		}},
		{"the same plan converged: every resource is a no-op", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
		}},
		{"the SA fallback's IAM account created while the rest is converged", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
		}},
		{"the member known, bare", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsKnownGrant(wsProject, wsEmail, wsRole, "create"),
		}},
		{"the member known, as the root writes it", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsKnownGrant(wsProject, "serviceAccount:"+wsEmail, wsRole, "create"),
		}},
		{"the accounts' display names updated, the grant held", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "update"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "update"),
			wsComputedGrant(wsProject, wsRole, "update"),
		}},
	}
	for _, c := range covered {
		t.Run(c.name, func(t *testing.T) {
			p := wsPlan(t, c.changes...)
			if c.cfg != "" {
				p = wsPlanCfg(t, c.cfg, c.changes...)
			}
			if why := wsWhy(t, p); why != "" {
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
				wsComputedGrant(wsProject, wsRole, "create"),
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
		// The SA fallback's own account, in its three spellings. Terraform
		// grants it nothing, and that is the design rather than an omission:
		// its reach on a member is the member owner's `roles/owner` grant and
		// its token creators are the workspace admins, both written live by
		// the CLI (D2803, D2818, WU20). A grant of the workspace database role
		// to it here would be the workspace reading its own record through the
		// identity a member's owner handed authority to.
		wsIAMEmail,
		"serviceAccount:" + wsIAMEmail,
		"google_service_account.workspace_iam.member",
	}
	for _, member := range others {
		t.Run(member, func(t *testing.T) {
			p := wsPlan(t,
				wsSA(wsProject, WorkspaceServiceAccountID, "create"),
				wsKnownGrant(wsProject, member, wsRole, "create"))
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

// TestWorkspacePlanRefusesAComputedMemberThatReadsAnotherAccount is the half of
// the grant rule that the change alone cannot answer (workspaceComputedMember,
// WU3's second account): while a plan creates the workspace's own service
// account, a grant whose member terraform knows only at apply is the root's own
// shape — and a grant bound to the SA fallback's IAM account carries
// after_unknown.member exactly the same way, so the plan's configuration is what
// tells them apart. The account that would be bound is the one that ends up
// holding roles/owner on every linked member (D2803, F20).
func TestWorkspacePlanRefusesAComputedMemberThatReadsAnotherAccount(t *testing.T) {
	own := wsSA(wsProject, WorkspaceServiceAccountID, "create")
	grant := wsComputedGrant(wsProject, wsRole, "create")
	refused := []struct {
		name string
		cfg  string
		want string
	}{
		{"the member expression reads the SA fallback's IAM account", wsMemberExpr("google_service_account.workspace_iam"), "google_service_account.workspace_iam"},
		{"the member expression reads a variable", `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"google_project_iam_member","expressions":{"member":{"references":["var.member"]}}}`, "var.member"},
		{"the configuration has no member expression on the grant", `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"google_project_iam_member","expressions":{}}`, "no member expression"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsPlanCfg(t, c.cfg, own, grant))
			if why == "" {
				t.Fatalf("this plan must be refused: %s", c.name)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("the refusal must name what the member reads, want a sentence with %q, got: %s", c.want, why)
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
		}, "holds " + wsRole + " and nothing else"},
		{"the database role in another project", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant("member-project-5678", wsEmail, WorkspaceDatabaseRole("member-project-5678"), "create"),
		}, "the workspace's own database is"},
		{"the account in another project", []string{
			wsSA("member-project-5678", WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "an account in another project"},
		{"another account id", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "creates two and no third"},
		{"the SA fallback's account replaced by another id", []string{
			wsIAM(wsProject, "fugaro-workspace-admin", "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "creates two and no third"},
		{"the SA fallback's IAM account in another project", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsIAM("member-project-5678", WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "an account in another project"},
		{"an account id terraform knows only at apply", []string{
			`{"address":"google_service_account.workspace_iam","mode":"managed","type":"google_service_account","change":{"actions":["create"],"after":{"project":"` + wsProject + `"},"after_unknown":{"account_id":true}}}`,
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "knows only at apply"},
		{"a service account key", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			`{"address":"google_service_account_key.workspace","mode":"managed","type":"google_service_account_key","change":{"actions":["create"],"after":{}}}`,
		}, "a service account key"},
		{"an authoritative binding", []string{
			`{"address":"google_project_iam_binding.workspace_database","mode":"managed","type":"google_project_iam_binding","change":{"actions":["create"],"after":{"role":"` + wsRole + `"}}}`,
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
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "does not create the workspace's own service account"},
		{"the member computed while only the SA fallback's account is created", []string{
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "does not create the workspace's own service account"},
		{"the member computed while another account is created", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "creates two and no third"},
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

// TestWorkspacePlanRefusesAnotherRoleAThirdPermissionAndAnotherProject is the
// other half of the grant's reach: the plan that grants the role is also the
// plan that can WIDEN it, and Realtime Database IAM having no path-level scope
// makes the permission list the whole of the decision (spec §8). A third
// permission — the lifecycle admin carries, or the list — an update that adds
// one, another id and another project are all refused by name.
func TestWorkspacePlanRefusesAnotherRoleAThirdPermissionAndAnotherProject(t *testing.T) {
	refused := []struct {
		name    string
		changes []string
		want    string
	}{
		{"the instance lifecycle added to the role", []string{
			wsRoleChange(wsProject, WorkspaceDatabaseRoleID, append(append([]string{}, WorkspaceDatabasePermissions...), "firebasedatabase.instances.delete")...),
		}, "holds exactly two"},
		{"one permission dropped", []string{
			wsRoleChange(wsProject, WorkspaceDatabaseRoleID, WorkspaceDatabasePermissions[0]),
		}, "holds exactly two"},
		{"the read replaced by the viewer's list", []string{
			wsRoleChange(wsProject, WorkspaceDatabaseRoleID, "firebasedatabase.instances.list", "firebasedatabase.instances.update"),
		}, "does not hold firebasedatabase.instances.get"},
		{"another role id", []string{
			wsRoleChange(wsProject, "fugaroWorkspaceAdmin", WorkspaceDatabasePermissions...),
		}, "and no other role"},
		{"the role in another project", []string{
			wsRoleChange("member-project-5678", WorkspaceDatabaseRoleID, WorkspaceDatabasePermissions...),
		}, "a role in another project"},
		{"a role whose permissions terraform computes", []string{
			`{"address":"google_project_iam_custom_role.workspace_database","mode":"managed","type":"google_project_iam_custom_role","change":{"actions":["create"],"after":{"project":"` + wsProject + `","role_id":"` + WorkspaceDatabaseRoleID + `"},"after_unknown":{"permissions":true}}}`,
		}, "knows only at apply"},
		{"a delete of the role", []string{
			`{"address":"google_project_iam_custom_role.workspace_database","mode":"managed","type":"google_project_iam_custom_role","change":{"actions":["delete"],"after":{"project":"` + wsProject + `","role_id":"` + WorkspaceDatabaseRoleID + `","permissions":["` + strings.Join(WorkspaceDatabasePermissions, `","`) + `"]}}}`,
		}, "actions delete"},
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
// holds its resources to the cover's three types: the file is what the embed
// ships and what an operator applies, so a type added there is a workspace
// holding something else, and it fails here rather than in a plan the cover
// would have refused anyway.
//
// It also reads the root's literals — the role id, the grant's role path and
// the SA fallback's account id — against the Go side, because the cover builds
// the path from the project it was given and compares the ids it pins: a root
// that renamed any of them would leave every plan of it refused, and this is
// where that is said in one sentence rather than as a plan that will not
// confirm.
func TestWorkspaceRootDeclaresTheTypesTheCoverHolds(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/terraform/gcp/roots/workspace/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{`).FindAllStringSubmatch(string(raw), -1)
	types := map[string]bool{}
	accounts := 0
	for _, m := range blocks {
		types[m[1]] = true
		if m[1] == workspaceAccountType {
			accounts++
		}
	}
	if len(blocks) != 4 || len(types) != 3 || !types[workspaceRoleType] || !types[workspaceAccountType] || !types[workspaceGrantType] {
		t.Fatalf("the workspace root must declare exactly the workspace's role, its TWO accounts (the bundle's service and the SA fallback's IAM account) and that one grant, got %d blocks over %v", len(blocks), types)
	}
	if accounts != 2 {
		t.Fatalf("the workspace root declares %d service accounts, want two: the workspace's own service (W6) and the SA fallback's IAM account (D2803, F20 — two accounts, never one)", accounts)
	}
	if m := regexp.MustCompile(`(?m)^\s*account_id\s*=\s*"` + WorkspaceIAMAccountID + `"\s*$`); !m.Match(raw) {
		t.Errorf("the root does not declare the SA fallback's account id the cover pins (%s): the cover holds that account by its literal, because the root gives it no variable the account's own name could travel in", WorkspaceIAMAccountID)
	}
	if m := regexp.MustCompile(`(?m)^\s*role_id\s*=\s*"` + WorkspaceDatabaseRoleID + `"\s*$`); !m.Match(raw) {
		t.Errorf("the root does not declare the role id the cover pins (%s)", WorkspaceDatabaseRoleID)
	}
	if m := regexp.MustCompile(`(?m)^\s*role\s*=\s*"projects/\$\{var\.project\}/roles/` + WorkspaceDatabaseRoleID + `"\s*$`); !m.Match(raw) {
		t.Errorf("the root's grant does not name the role as the literal path the cover reads out of a plan (WorkspaceDatabaseRole): a role it cannot read is a role it cannot hold to the pinned id")
	}
	for _, p := range WorkspaceDatabasePermissions {
		if !strings.Contains(string(raw), `"`+p+`"`) {
			t.Errorf("the root's role does not hold %s, which the cover pins", p)
		}
	}
}

// TestWorkspaceRootMakesNoKeyForTheIAMAccount is the Go half of the tftest
// case beside the root, and it says what "no key" is: the ROOT writes none.
// That is a fact about what Fugaro writes and **not a claim that none can
// exist** — the account lives in a project Fugaro does not own alone, and two
// paths on that project make a key anyway, neither of them this root's:
//
//   - iam.serviceAccounts.actAs, which roles/editor and
//     roles/iam.serviceAccountUser carry, lets a holder deploy a workload that
//     runs as the account;
//   - iam.serviceAccountKeys.create, which roles/editor resolves to, mints a
//     user-managed key that outlives the binding that made it.
//
// The only control that closes the second is the organization policy
// constraints/iam.disableServiceAccountKeyCreation, and WU11's `status` is what
// takes both up. What this test holds is the root's own side: no
// google_service_account_key, no actAs or token-creator grant, and no grant of
// any kind naming the SA fallback's account — its reach on a member is written
// live by the CLI (D2803, D2818), never by Terraform.
// TestWorkspaceAccountsAreTwoAndNeitherIsTheCredentialsServices pins the three
// sentences the design's accounts are told apart by (F20, D2803, D1299):
//
//   - the workspace root creates TWO accounts and never one. The service reads
//     the workspace's own database and the members' ledgers; the SA fallback's
//     IAM account holds roles/owner on every member it is linked to. One
//     account doing both is a single compromise holding the workspace's state
//     AND its authority over every member.
//   - neither is the credentials service's account (CredentialsAccountID),
//     which holds no write on a member at all (D1299).
//   - neither is one of the installation's own (FugaroAccounts), which an
//     installation's toAccounts rows may grant a job's role to: the whole
//     reason WorkspaceServiceAccountID is outside that table, said again for
//     the account that holds roles/owner on the members.
func TestWorkspaceAccountsAreTwoAndNeitherIsTheCredentialsServices(t *testing.T) {
	if WorkspaceServiceAccountID == WorkspaceIAMAccountID {
		t.Fatalf("the workspace root's two accounts are one id (%s): F20 is two accounts, never one", WorkspaceServiceAccountID)
	}
	for _, id := range []string{WorkspaceServiceAccountID, WorkspaceIAMAccountID} {
		if id == CredentialsAccountID {
			t.Errorf("%s is the credentials service's account: that one holds no write on any member (D1299), and this one is created to hold authority over them", id)
		}
		if slices.Contains(FugaroAccounts, id) {
			t.Errorf("%s is one of FugaroAccounts: as one of them, an installation table's toAccounts row could have granted it a job's role", id)
		}
	}
}

// wsCode is the root's source with its own comments removed, so a test can
// hold what the file DOES to the text it may name in prose: the root's comments
// are where the design says what "no key" is and which two paths make one
// anyway, and a check over the raw text would refuse the sentence it is there
// to read. A `#` inside a string literal would cut the line early, and no line
// of this root has one.
func wsCode(raw string) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestWorkspaceRootMakesNoKeyForTheIAMAccount(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/terraform/gcp/roots/workspace/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := wsCode(string(raw))
	for _, bad := range []struct{ needle, why string }{
		{"google_service_account_key", "a key resource: the root creates the SA fallback's account and writes no key for it (D2803), so a key here would be a credential of the account that holds roles/owner on every member"},
		{"serviceAccountKeys", "a key permission or role literal"},
		{"iam.serviceAccounts.actAs", "an actAs grant: the root grants the workspace database role and nothing else"},
		{"roles/iam.serviceAccountUser", "an actAs role: the workspace root makes no impersonation grant at all"},
		{"roles/iam.serviceAccountTokenCreator", "a token-creator role: the SA fallback's token creators are the workspace admins, written live by `workspace user add --admin` (D2818), never by Terraform"},
		{"google_service_account_iam_member", "a service-account IAM resource: the root's one IAM resource is the project member that binds its database role"},
		{"workspace_iam.member", "a reference to the SA fallback's account as a grant's member: Terraform grants it nothing"},
	} {
		if strings.Contains(src, bad.needle) {
			t.Errorf("the workspace root's main.tf mentions %s (%s)", bad.needle, bad.why)
		}
	}
}
