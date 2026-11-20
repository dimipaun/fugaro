package tf

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The workspace root's plan cover, tested over plan JSON the way
// deploy/terraform/gcp/roots/workspace plans it (WS6,
// docs/workspace-service/implementation-plan.md): the workspace's own service
// account and its database role with the ONE grant that binds them, the
// workspace's own credentials service (D2980) running as that account, the ONE
// bundle accessor and one invoke grant per member job account (F4, F17, F25,
// R9).
const (
	wsProject = "workspace-alpha-1234"
	wsName    = "workspace-alpha"
	wsEmail   = WorkspaceServiceAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
	wsService = "fugaro-workspace-credentials"
	wsRegion  = "us-central1"
	// wsBundle is the workspace's own bundle container's Secret Manager id as
	// internal/backend/gcp derives it (CredsBundleSecretID of the workspace's
	// name): a value the cover is GIVEN, because no plan carries the
	// derivation and a rule that read it out of the plan would accept
	// whatever container the plan named.
	wsBundle = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
	wsImage  = "europe-west1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/fugaro/fugaro-base:1.2.3@sha256:" + wsDigest
	wsDigest = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

	wsMemberA = "fugaro-run-alpha-a@member-alpha-1234.iam.gserviceaccount.com"
	wsMemberB = "fugaro-run-alpha-b@member-beta-5678.iam.gserviceaccount.com"
)

// wsMembers is the member table the cover is given: the job accounts a plan may
// bind an invoke grant to, and no other principal.
var wsMembers = []string{wsMemberA, wsMemberB}

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

func wsActions(acts ...string) string {
	q := make([]string, 0, len(acts))
	for _, a := range acts {
		q = append(q, `"`+a+`"`)
	}
	return "[" + strings.Join(q, ",") + "]"
}

// wsChange is one resource_change of a plan, with `after` written as the given
// attributes and `before` left empty.
func wsChange(addr, typ string, after string, acts ...string) string {
	return `{"address":"` + addr + `","mode":"managed","type":"` + typ + `","change":{"actions":` +
		wsActions(acts...) + `,"after":{` + after + `}}}`
}

// wsDelete is a resource_change that deletes the resource, with `before` as the
// state it had: what terraform plans when a member leaves the table.
func wsDelete(addr, typ, before string) string {
	return `{"address":"` + addr + `","mode":"managed","type":"` + typ + `","change":{"actions":["delete"],"before":{` +
		before + `},"after":null}}`
}

// wsSA is the workspace's own service account as the root's plan writes it:
// both of its names literal, which is why the root is given them as variables.
func wsSA(project, accountID string, acts ...string) string {
	return wsChange("google_service_account.workspace", workspaceAccountType,
		`"project":"`+project+`","account_id":"`+accountID+`"`, acts...)
}

// wsRoleChange is the root's custom role as the root plans it: the project, the
// id and the permission list all literal, which is why this cover can hold the
// role to the pinned id and the pinned two.
func wsRoleChange(project, id string, perms ...string) string {
	q := make([]string, 0, len(perms))
	for _, p := range perms {
		q = append(q, `"`+p+`"`)
	}
	return wsChange("google_project_iam_custom_role.workspace_database", workspaceRoleType,
		`"project":"`+project+`","role_id":"`+id+`","permissions":[`+strings.Join(q, ",")+`]`, "create")
}

// wsPinnedRole is the root's own role, as the root declares it.
func wsPinnedRole() string {
	return wsRoleChange(wsProject, WorkspaceDatabaseRoleID, WorkspaceDatabasePermissions...)
}

// wsComputedGrant is the database grant as the root really plans it: the member
// is the address of the account this plan creates, so terraform knows it only
// at apply and the plan carries after_unknown instead of a value.
func wsComputedGrant(project, role string, acts ...string) string {
	return `{"address":"google_project_iam_member.workspace_database","mode":"managed","type":"` + workspaceGrantType + `","change":{"actions":` +
		wsActions(acts...) + `,"after":{"project":"` + project + `","role":"` + role + `"},"after_unknown":{"member":true}}}`
}

// wsKnownGrant is that grant with the member terraform already knows.
func wsKnownGrant(project, member, role string, acts ...string) string {
	return wsChange("google_project_iam_member.workspace_database", workspaceGrantType,
		`"project":"`+project+`","role":"`+role+`","member":"`+member+`"`, acts...)
}

// wsServiceAfter is the service's plan body: the shape the root writes, which
// every service case below starts from and then breaks in one place.
type wsServiceAttrs struct {
	name        string
	project     string
	account     string
	image       string
	iam         string
	concurrency string
	maxScale    string
	minScale    string
	minSvc      string
	scaling     string // the whole template scaling block, when not the root's
	env         string // the whole env list, when not the root's
	command     string
}

func (a wsServiceAttrs) json() string {
	name, project, account, image := a.name, a.project, a.account, a.image
	if name == "" {
		name = wsService
	}
	if project == "" {
		project = wsProject
	}
	if account == "" {
		account = wsEmail
	}
	if image == "" {
		image = wsImage
	}
	iam := a.iam
	if iam == "" {
		iam = "false"
	}
	conc := a.concurrency
	if conc == "" {
		conc = strconv.Itoa(WorkspaceConcurrency)
	}
	minSvc := a.minSvc
	if minSvc == "" {
		minSvc = "0"
	}
	scaling := a.scaling
	if scaling == "" {
		maxScale, minScale := a.maxScale, a.minScale
		if maxScale == "" {
			maxScale = strconv.Itoa(WorkspaceMaxInstances)
		}
		if minScale == "" {
			minScale = "0"
		}
		scaling = `"scaling":[{"min_instance_count":` + minScale + `,"max_instance_count":` + maxScale + `}]`
	}
	env := a.env
	if env == "" {
		env = `"env":[{"name":"` + WorkspaceModeEnv + `","value":"1"}]`
	}
	return `"project":"` + project + `","location":"` + wsRegion + `","name":"` + name +
		`","invoker_iam_disabled":` + iam + `,"scaling":[{"scaling_mode":"AUTOMATIC","min_instance_count":` + minSvc + `}],` +
		`"template":[{"service_account":"` + account + `","max_instance_request_concurrency":` + conc + `,` + scaling +
		`,"containers":[{"image":"` + image + `"` + a.command + `,` + env + `}]}]`
}

// wsService is the workspace's own credentials service as the root plans it.
func wsServiceChange(a wsServiceAttrs, acts ...string) string {
	return wsChange("google_cloud_run_v2_service.workspace", workspaceServiceType, a.json(), acts...)
}

// wsAccessor is the ONE bundle accessor, which the cover is held to by the
// container the caller derived.
func wsAccessor(project, secret, role, member string, acts ...string) string {
	if project == "" {
		project = wsProject
	}
	if secret == "" {
		secret = wsBundle
	}
	if role == "" {
		role = WorkspaceBundleAccessorRole
	}
	if member == "" {
		member = wsEmail
	}
	return wsChange("google_secret_manager_secret_iam_member.workspace_bundle", workspaceAccessorType,
		`"project":"`+project+`","secret_id":"`+secret+`","role":"`+role+`","member":"`+member+`"`, acts...)
}

// wsInvokerAfter is an invoke grant's attributes as the root writes them.
func wsInvokerAfter(project, name, role, member string) string {
	if project == "" {
		project = wsProject
	}
	if name == "" {
		name = wsService
	}
	if role == "" {
		role = WorkspaceInvokerRole
	}
	return `"project":"` + project + `","location":"` + wsRegion + `","name":"` + name +
		`","role":"` + role + `","member":"` + member + `"`
}

// wsInvoker is one member's invoke grant on the workspace's service.
func wsInvoker(member string, acts ...string) string {
	return wsChange("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
		wsInvokerAfter("", "", "", member), acts...)
}

// wsInvokerDelete is the same grant as terraform plans it when the member
// leaves the table: the state it had, and no `after`.
func wsInvokerDelete(member string) string {
	return wsDelete("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
		wsInvokerAfter("", "", "", member))
}

// wsDeployed is the whole root as a first apply plans it: the three resources
// of the workspace's own identity, the service, its one bundle accessor and one
// invoke grant per member job account.
func wsDeployed() []string {
	return []string{
		wsPinnedRole(),
		wsSA(wsProject, WorkspaceServiceAccountID, "create"),
		wsComputedGrant(wsProject, wsRole, "create"),
		wsServiceChange(wsServiceAttrs{}, "create"),
		wsAccessor("", "", "", "", "create"),
		wsInvoker(wsMemberA, "create"),
		wsInvoker(wsMemberB, "create"),
	}
}

func wsWhy(t *testing.T, p *Plan) string {
	t.Helper()
	return strings.Join(WorkspaceNotCovered(p, wsProject, WorkspaceServiceAccountID, wsBundle, wsMembers), "; ")
}

// wsAdd is the root's plan with one change appended, which every refusal case
// below is written as: the failure is the added change and not the plan.
func wsAdd(t *testing.T, extra string) *Plan {
	t.Helper()
	return wsPlan(t, append(wsDeployed(), extra)...)
}

// TestWorkspacePlanAdmitsTheService is the positive half: the plan the root
// really produces is covered, in every state it produces it in — the first
// apply, a converged update, a second apply that fills the service's own URL,
// and the workspace whose service is not deployed at all.
func TestWorkspacePlanAdmitsTheService(t *testing.T) {
	covered := []struct {
		name    string
		changes []string
	}{
		{"the root's own plan: the account, its role and grant, the service, its accessor and the members' invoke grants", wsDeployed()},
		{"the same plan converged: every resource a no-op", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
			wsServiceChange(wsServiceAttrs{}, "no-op"),
			wsAccessor("", "", "", "", "no-op"),
			wsInvoker(wsMemberA, "no-op"),
			wsInvoker(wsMemberB, "no-op"),
		}},
		{"the service's own URL filled on a second apply: the service is updated and nothing else changes", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
			wsServiceChange(wsServiceAttrs{env: `"env":[{"name":"` + WorkspaceModeEnv + `","value":"1"},{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://fugaro-workspace-credentials-abc.a.run.app"}]`}, "update"),
			wsAccessor("", "", "", "", "no-op"),
			wsInvoker(wsMemberA, "no-op"),
			wsInvoker(wsMemberB, "no-op"),
		}},
		{"the workspace's account, its role and its grant alone: no service is deployed (var.credentials_service unset)", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}},
		{"the member known, bare and as the root writes it", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant(wsProject, wsEmail, wsRole, "create"),
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

// TestWorkspacePlanAdmitsRemovingAMembersInvokeGrant is F25's sentence and the
// plan's own constraint ("Removing a member needs no operator repair"): the ONE
// deletion the workspace root's plan holds is a member's invoke grant, so
// `fugaro update` converges a shrunk member table with no repair — and the
// deletion of anything else stays refused, the workspace's service and the
// bundle accessor included.
func TestWorkspacePlanAdmitsRemovingAMembersInvokeGrant(t *testing.T) {
	if why := wsWhy(t, wsPlan(t,
		wsPinnedRole(),
		wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
		wsComputedGrant(wsProject, wsRole, "no-op"),
		wsServiceChange(wsServiceAttrs{}, "no-op"),
		wsAccessor("", "", "", "", "no-op"),
		wsInvokerDelete(wsMemberA),
		wsInvoker(wsMemberB, "no-op"),
	)); why != "" {
		t.Errorf("removing a member must be no operator repair (F25): the plan that deletes its invoke grant and nothing else is this root's, got: %s", why)
	}

	refused := []struct {
		name  string
		extra string
		want  string
	}{
		{"the bundle accessor deleted", wsDelete("google_secret_manager_secret_iam_member.workspace_bundle", workspaceAccessorType,
			`"project":"`+wsProject+`","secret_id":"`+wsBundle+`","role":"`+WorkspaceBundleAccessorRole+`","member":"serviceAccount:`+wsEmail+`"`),
			"actions delete"},
		{"the service deleted", wsDelete("google_cloud_run_v2_service.workspace", workspaceServiceType,
			`"project":"`+wsProject+`","location":"`+wsRegion+`","name":"`+wsService+`"`), "actions delete"},
		{"the workspace's database grant deleted", wsDelete("google_project_iam_member.workspace_database", workspaceGrantType,
			`"project":"`+wsProject+`","role":"`+wsRole+`","member":"serviceAccount:`+wsEmail+`"`), "actions delete"},
		{"the workspace's own account deleted", wsSA(wsProject, WorkspaceServiceAccountID, "delete"), "actions delete"},
		{"the workspace's own role deleted", wsChange("google_project_iam_custom_role.workspace_database", workspaceRoleType,
			`"project":"`+wsProject+`","role_id":"`+WorkspaceDatabaseRoleID+`"`, "delete"), "actions delete"},
		{"an invoke grant replaced rather than deleted", wsChange("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
			wsInvokerAfter("", "fugaro-workspace-credentials-v2", "", wsMemberA), "delete", "create"), "actions delete, create"},
		{"a member's invoke grant deleted for an account that is not a member", wsInvokerDelete("fugaro-run-stranger@member-gamma-9012.iam.gserviceaccount.com"),
			"not a member's"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsAdd(t, c.extra))
			if why == "" {
				t.Fatalf("this plan must be refused: %s", c.name)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("the refusal must say why, want a sentence with %q, got: %s", c.want, why)
			}
		})
	}
}

// TestWorkspacePlanRefusesAnythingElse holds spec §9's "nothing else" and every
// rule the root's four new resources are admitted under: a type outside the
// root, a service that is not this deployment (another project, another runtime
// account, an unpinned image, a command, an environment that is not the root's,
// or R9's limits moved), an accessor on another container, with another role or
// for another principal, an invoke grant with another role, on another service,
// in another project or to a principal that is not a member's job account — and
// the workspace's own role, account and grant held as they were.
func TestWorkspacePlanRefusesAnythingElse(t *testing.T) {
	other := []struct {
		typ  string
		addr string
	}{
		{"google_cloud_run_v2_job", "google_cloud_run_v2_job.workspace"},
		{"google_storage_bucket", "google_storage_bucket.runs"},
		{"google_project_service", "google_project_service.identitytoolkit"},
		{"google_firebase_project", "google_firebase_project.this"},
		{"google_artifact_registry_repository", "google_artifact_registry_repository.images"},
		{"google_secret_manager_secret", "google_secret_manager_secret.model"},
	}
	for _, c := range other {
		t.Run(c.typ, func(t *testing.T) {
			why := wsWhy(t, wsAdd(t, wsChange(c.addr, c.typ, ``, "create")))
			if why == "" {
				t.Fatalf("a %s in the workspace's own project must be refused: a workspace holds no job, no runs bucket and no repository (spec §9)", c.typ)
			}
			if !strings.Contains(why, "no job, no runs bucket and no repository") {
				t.Errorf("the refusal must say what a workspace does not hold, got: %s", why)
			}
		})
	}

	refused := []struct {
		name  string
		extra []string
		want  string
	}{
		{"the database role granted to another principal", []string{
			wsKnownGrant(wsProject, "user:someone@example.com", wsRole, "create"),
		}, "reached by " + wsEmail + " and by nobody else"},
		{"the database role granted to a member's own job account", []string{
			wsKnownGrant(wsProject, "serviceAccount:"+wsMemberA, wsRole, "create"),
		}, "reached by " + wsEmail + " and by nobody else"},
		{"another role on the workspace's account", []string{
			wsKnownGrant(wsProject, "serviceAccount:"+wsEmail, "roles/editor", "create"),
		}, "holds " + wsRole + " and nothing else"},
		{"the database role in another project", []string{
			wsKnownGrant("member-project-5678", "serviceAccount:"+wsEmail, WorkspaceDatabaseRole("member-project-5678"), "create"),
		}, "the workspace's own database is"},
		{"the account in another project", []string{
			wsSA("member-project-5678", WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "an account in another project"},
		{"another account id", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "the workspace's own service account is"},
		{"a service account key", []string{
			wsChange("google_service_account_key.workspace", "google_service_account_key", ``, "create"),
		}, "a service account key"},
		{"an authoritative binding of the workspace's role", []string{
			wsChange("google_project_iam_binding.workspace_database", "google_project_iam_binding",
				`"project":"`+wsProject+`","role":"`+wsRole+`"`, "create"),
		}, "an authoritative IAM resource"},
		{"a data source", []string{
			`{"address":"data.google_project.this","mode":"data","type":"google_project","change":{"actions":["read"],"after":{}}}`,
		}, "a data source"},
		{"the workspace's own account deleted", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "delete"),
		}, "actions delete"},
		{"a second service in the workspace's own project", []string{
			wsChange("google_cloud_run_v2_service.second", workspaceServiceType,
				wsServiceAttrs{name: "fugaro-workspace-second"}.json(), "create"),
		}, "one service"},
		{"the service in another project", []string{
			wsServiceChange(wsServiceAttrs{project: "member-project-5678"}, "create"),
		}, "a service in project"},
		{"the service running as another account", []string{
			wsServiceChange(wsServiceAttrs{account: "fugaro-credentials@" + wsProject + ".iam.gserviceaccount.com"}, "create"),
		}, "and as nobody else"},
		{"the service running as a job account of the workspace's own project", []string{
			wsServiceChange(wsServiceAttrs{account: wsMemberA}, "create"),
		}, "the workspace's own service runs as"},
		{"the service on an image with no digest", []string{
			wsServiceChange(wsServiceAttrs{image: "europe-west1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/fugaro/fugaro-base:latest"}, "create"),
		}, "not a digest pin"},
		{"the service with a command of its own", []string{
			wsServiceChange(wsServiceAttrs{command: `,"command":["/bin/sh"]`}, "create"),
		}, "sets a command or args"},
		{"the service whose environment takes a value from a source", []string{
			wsServiceChange(wsServiceAttrs{env: `"env":[{"name":"` + WorkspaceModeEnv + `","value":"1"},{"name":"FUGARO_RTDB_URL","value_source":[{"secret_key_ref":[{"secret":"bundle"}]}]}]`}, "create"),
		}, "value comes from a source"},
		{"the service with no workspace mode in its environment", []string{
			wsServiceChange(wsServiceAttrs{env: `"env":[{"name":"FUGARO_PROJECT","value":"` + wsName + `"}]`}, "create"),
		}, "does not carry " + WorkspaceModeEnv},
		{"the service with no IAM required", []string{
			wsServiceChange(wsServiceAttrs{iam: "true"}, "create"),
		}, "does not require IAM"},
		{"a maximum of twenty instances rather than R9's ten", []string{
			wsServiceChange(wsServiceAttrs{maxScale: "20"}, "create"),
		}, "maximum instance count is 20"},
		{"a concurrency of a hundred rather than R9's forty", []string{
			wsServiceChange(wsServiceAttrs{concurrency: "100"}, "create"),
		}, "concurrency is 100"},
		{"a template minimum of one instance", []string{
			wsServiceChange(wsServiceAttrs{minScale: "1"}, "create"),
		}, "minimum instance count is 1"},
		{"a service-level minimum of one instance", []string{
			wsServiceChange(wsServiceAttrs{minSvc: "1"}, "create"),
		}, "service-level scaling"},
		{"the service with no scaling block at all", []string{
			wsServiceChange(wsServiceAttrs{scaling: `"scaling":[]`}, "create"),
		}, "states no scaling"},
		{"a second service's worth: the workspace's service and another under the same type", []string{
			wsServiceChange(wsServiceAttrs{name: "fugaro-workspace-other"}, "create"),
		}, "one service"},
		{"the bundle accessor on another container", []string{
			wsAccessor("", "fugaro-project-workspace-alpha-ssh-key-0011223344556677", "", "", "create"),
		}, "the workspace's own bundle is the container"},
		{"the bundle accessor in another project", []string{
			wsAccessor("member-project-5678", "", "", "", "create"),
		}, "a bundle accessor in project"},
		{"the bundle accessor with another role", []string{
			wsAccessor("", "", "roles/secretmanager.secretVersionAdder", "", "create"),
		}, "roles/secretmanager.secretVersionAdder"},
		{"the bundle accessor for a member's job account", []string{
			wsAccessor("", "", "", "serviceAccount:"+wsMemberA, "create"),
		}, "read by " + wsEmail + " and by nobody else"},
		{"the bundle accessor for a person", []string{
			wsAccessor("", "", "", "user:someone@example.com", "create"),
		}, "read by " + wsEmail + " and by nobody else"},
		{"an invoke grant with another role", []string{
			wsChange("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
				wsInvokerAfter("", "", "roles/run.developer", "serviceAccount:"+wsMemberA), "create"),
		}, "roles/run.developer"},
		{"an invoke grant on another service", []string{
			wsChange("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
				wsInvokerAfter("", "fugaro-credentials", "", "serviceAccount:"+wsMemberA), "create"),
		}, "this plan's own workspace service"},
		{"an invoke grant in another project", []string{
			wsChange("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
				wsInvokerAfter("member-project-5678", "", "", "serviceAccount:"+wsMemberA), "create"),
		}, "an invoke grant in project"},
		{"an invoke grant to a person", []string{
			wsInvoker("user:someone@example.com", "create"),
		}, "invoked by a member's own job accounts alone"},
		{"an invoke grant to allUsers", []string{
			wsInvoker("allUsers", "create"),
		}, "invoked by a member's own job accounts alone"},
		{"an invoke grant to a member of another workspace", []string{
			wsInvoker("serviceAccount:fugaro-run-stranger@member-gamma-9012.iam.gserviceaccount.com", "create"),
		}, "invoked by a member's own job accounts alone"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsPlan(t, append(wsDeployed(), c.extra...)...))
			if why == "" {
				t.Fatalf("this plan must be refused: %s", c.name)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("the refusal must say why, want a sentence with %q, got: %s", c.want, why)
			}
		})
	}
}

// TestWorkspacePlanRefusesAnotherRoleAThirdPermissionAndTheUnknownService is
// the role half of the cover on its own: the plan that grants the role is also
// the plan that can WIDEN it, and Realtime Database IAM having no path-level
// scope makes the permission list the whole of the decision (spec §8). A third
// permission — the lifecycle admin carries, or the list — an update that adds
// one, another id and another project are all refused by name.
func TestWorkspacePlanRefusesAnotherRoleAThirdPermissionAndTheUnknownService(t *testing.T) {
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
			`{"address":"google_project_iam_custom_role.workspace_database","mode":"managed","type":"` + workspaceRoleType + `","change":{"actions":["create"],"after":{"project":"` + wsProject + `","role_id":"` + WorkspaceDatabaseRoleID + `"},"after_unknown":{"permissions":true}}}`,
		}, "knows only at apply"},
		{"a delete of the role", []string{
			wsChange("google_project_iam_custom_role.workspace_database", workspaceRoleType,
				`"project":"`+wsProject+`","role_id":"`+WorkspaceDatabaseRoleID+`","permissions":["`+strings.Join(WorkspaceDatabasePermissions, `","`)+`"]`, "delete"),
		}, "actions delete"},
		{"the member computed with no account in the plan", []string{
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "does not create the workspace's own service account"},
		{"the member computed while another account is created", []string{
			wsSA(wsProject, "fugaro-runner", "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "the workspace's own service account is"},
		{"a service whose runtime account terraform knows only at apply", []string{
			`{"address":"google_cloud_run_v2_service.workspace","mode":"managed","type":"` + workspaceServiceType + `","change":{"actions":["create"],"after":{` + wsServiceAttrs{}.json() + `},"after_unknown":{"template":[{"service_account":true}]}}}`,
		}, "a runtime account that is not known"},
		{"an invoke grant whose member terraform knows only at apply", []string{
			`{"address":"google_cloud_run_v2_service_iam_member.workspace_invoker","mode":"managed","type":"` + workspaceInvokerType + `","change":{"actions":["create"],"after":{"project":"` + wsProject + `","location":"` + wsRegion + `","name":"` + wsService + `","role":"` + WorkspaceInvokerRole + `"},"after_unknown":{"member":true}}}`,
		}, "knows only at apply"},
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
	if why := WorkspaceNotCovered(nil, wsProject, WorkspaceServiceAccountID, wsBundle, wsMembers); len(why) == 0 {
		t.Fatal("a nil plan must be refused")
	}
}

// TestWorkspaceRootDeclaresTheTypesTheCoverHolds reads the root itself and
// holds its resources to the cover's six types: the file is what the embed
// ships and what an operator applies, so a seventh type added there is a
// workspace holding something else, and it fails here rather than in a plan the
// cover would have refused anyway.
//
// It also reads the root's literals — the role id, the grant's role path, the
// two permissions, the accessor's role, the invoke grant's role, R9's three
// numbers and the workspace-mode environment name — against the Go side,
// because the cover holds a plan to the pins it builds rather than to what the
// plan says: a root that renamed or moved one would leave every plan of it
// refused, or a plan admitted that is not the one this design reviewed, and
// this is where that is said in one sentence rather than as a plan that will
// not confirm.
func TestWorkspaceRootDeclaresTheTypesTheCoverHolds(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/terraform/gcp/roots/workspace/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	types := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{`).FindAllStringSubmatch(src, -1) {
		types[m[1]] = true
	}
	for _, typ := range []string{workspaceRoleType, workspaceAccountType, workspaceGrantType, workspaceServiceType, workspaceAccessorType, workspaceInvokerType} {
		if !types[typ] {
			t.Errorf("the workspace root does not declare a %s, and the cover admits a plan of it", typ)
		}
	}
	if len(types) != 6 {
		t.Fatalf("the workspace root must declare exactly the workspace's role, its account, that account's one grant, its credentials service, that service's bundle accessor and its members' invoke grants, got %v", types)
	}
	// find is the root's text with runs of whitespace collapsed, so a literal
	// can be read wherever the formatter wrapped it.
	flat := regexp.MustCompile(`\s+`).ReplaceAllString(src, " ")
	for _, want := range []struct{ what, literal string }{
		{"the role id the cover pins", `role_id = "` + WorkspaceDatabaseRoleID + `"`},
		{"the role path the cover reads out of a plan", `role = "projects/${var.project}/roles/` + WorkspaceDatabaseRoleID + `"`},
		{"the accessor's role", `role = "` + WorkspaceBundleAccessorRole + `"`},
		{"the invoke grant's role", `role = "` + WorkspaceInvokerRole + `"`},
		{"the workspace-mode environment name", `name = "` + WorkspaceModeEnv + `"`},
		{"R9's maximum instance count", `max_instance_count = ` + strconv.Itoa(WorkspaceMaxInstances)},
		{"R9's concurrency", `max_instance_request_concurrency = ` + strconv.Itoa(WorkspaceConcurrency)},
		{"the zero minimum the managed gate prices", `min_instance_count = 0`},
		{"the service running as the workspace's own account", `service_account = google_service_account.workspace.email`},
		{"the bundle container the accessor is on", `secret_id = var.credentials_service.bundle_secret`},
		{"the member table the invoke grants come from", `"serviceAccount:${each.value}"`},
	} {
		if !strings.Contains(flat, want.literal) {
			t.Errorf("the root does not hold %s (%s), and the cover builds its rules from it", want.what, want.literal)
		}
	}
	for _, p := range WorkspaceDatabasePermissions {
		if !strings.Contains(src, `"`+p+`"`) {
			t.Errorf("the root's role does not hold %s, which the cover pins", p)
		}
	}
}
