package tf

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
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
	// wsIAMEmail is the SA fallback's account (WU3): created by the root, granted
	// nothing by it.
	wsIAMEmail = WorkspaceIAMAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
	wsService  = "fugaro-workspace-credentials"
	wsRegion   = "us-central1"
	// wsBundle is the workspace's own bundle container's Secret Manager id as
	// internal/backend/gcp derives it (CredsBundleSecretID of the workspace's
	// name): a value the cover is GIVEN, because no plan carries the
	// derivation and a rule that read it out of the plan would accept
	// whatever container the plan named.
	wsBundle = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
	// wsImage is the released pin the root's service is written with: the
	// workspace's own registry, the remote (pull-through) repository, the
	// recorded owner, a fugaro-<kind> image, a release version tag and a
	// digest — the closure wsCover holds it to (credentialsImageOK), which is
	// the same shape an installation's credentials service is deployed with.
	wsImage  = "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:" + wsDigest
	wsDigest = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

	wsMemberA = "fugaro-run-alpha-a@member-alpha-1234.iam.gserviceaccount.com"
	wsMemberB = "fugaro-run-alpha-b@member-beta-5678.iam.gserviceaccount.com"

	// The roster's people (WC2, R6, D2971): the addresses the workspace's own
	// roster names as `human`, which the root binds one invoke grant to each and
	// which the cover is GIVEN — no plan carries the roster, and a rule that read
	// it out of the plan would accept whatever people the plan named.
	wsPersonA = "ada@example.com"
	wsPersonB = "grace@example.org"
)

// wsPeople is the roster the cover is given: the addresses a plan may bind an
// invoke grant to as a person, and no other.
var wsPeople = []string{wsPersonA, wsPersonB}

// wsCover is the image identity the workspace cover holds the service to: this
// run's registry, the remote (pull-through) repository and the owner the run's
// config records — tf.Cover's own three fields, the same three init's review and
// managed_apply read off the config (infra.RegistryHost, infra.GhcrRegistry,
// baseOwner), spelled as literals here because this package sits below both. An
// owner of "" would refuse every remote reference, which is the other half of
// what these cases test.
var wsCover = Cover{Registry: "us-central1-docker.pkg.dev", Ghcr: "fugaro-ghcr", Owner: "dimipaun"}

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

// wsIAM is the SA fallback's IAM account as the root's plan writes it: the
// account id is the root's own literal, which is why the cover can hold it
// without being told it.
func wsIAM(project, accountID string, acts ...string) string {
	return wsChange("google_service_account.workspace_iam", workspaceAccountType,
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

// wsPersonInvoker is one roster person's invoke grant on the workspace's
// service: the same resource the members' grants are, a different resource
// address and a `user:` member (WC2, R6).
func wsPersonInvoker(person string, acts ...string) string {
	return wsChange("google_cloud_run_v2_service_iam_member.workspace_person_invoker", workspaceInvokerType,
		wsInvokerAfter("", "", "", "user:"+person), acts...)
}

// wsPersonInvokerDelete is that grant as terraform plans it when a person
// leaves the roster: the state it had, and no `after`.
func wsPersonInvokerDelete(person string) string {
	return wsDelete("google_cloud_run_v2_service_iam_member.workspace_person_invoker", workspaceInvokerType,
		wsInvokerAfter("", "", "", "user:"+person))
}

// wsPersonAccessor is the ONE conditioned accessor the workspace's own account
// holds on a person's token (WC2, R6): a project-level grant of the bundle
// accessor's role, under the condition that is the fence. cond is the whole
// `condition` attribute, so a case can write none, a wider one or the exact
// fence; member is the principal, defaulting to the workspace's own account.
func wsPersonAccessor(project, member, cond string, acts ...string) string {
	if project == "" {
		project = wsProject
	}
	if member == "" {
		member = "serviceAccount:" + wsEmail
	}
	after := `"project":"` + project + `","role":"` + WorkspaceBundleAccessorRole + `","member":"` + member + `"`
	if cond != "" {
		after += `,"condition":` + cond
	}
	return wsChange("google_project_iam_member.workspace_person_token", workspaceGrantType, after, acts...)
}

// wsPersonAccessorExpr is the person accessor's member expression as the plan's
// configuration writes it when the member is the account resource named: the
// root writes google_service_account.workspace.member, which terraform knows
// only at apply (wsMemberExpr's own shape, another resource).
func wsPersonAccessorExpr(account string) string {
	return `{"address":"google_project_iam_member.workspace_person_token","mode":"managed","type":"google_project_iam_member","expressions":{"member":{"references":["` +
		account + `.member","` + account + `"]}}}`
}

// wsPersonFence is the condition the root writes, as the plan carries it: the
// title and the expression, taken from internal/backend/gcp so the case cannot
// drift from what the cover holds a plan to (gcp.WorkspacePersonTokenCondition).
func wsPersonFence() string {
	return `[{"title":"` + gcp.WorkspacePersonTokenConditionTitle + `","expression":"` +
		strings.ReplaceAll(gcp.WorkspacePersonTokenCondition(), `"`, `\"`) + `","description":null}]`
}

// wsInvokerDelete is the same grant as terraform plans it when the member
// leaves the table: the state it had, and no `after`.
func wsInvokerDelete(member string) string {
	return wsDelete("google_cloud_run_v2_service_iam_member.workspace_invoker", workspaceInvokerType,
		wsInvokerAfter("", "", "", member))
}

// wsDeployed is the whole root as a first apply plans it: the three resources
// of the workspace's own identity, the service, its one bundle accessor, the
// ONE conditioned accessor on a person's token, one invoke grant per member job
// account and one per person of the roster (WC2, R6).
func wsDeployed() []string {
	return []string{
		wsPinnedRole(),
		wsSA(wsProject, WorkspaceServiceAccountID, "create"),
		wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
		wsComputedGrant(wsProject, wsRole, "create"),
		wsServiceChange(wsServiceAttrs{}, "create"),
		wsAccessor("", "", "", "", "create"),
		wsPersonAccessor("", "", wsPersonFence(), "create"),
		wsInvoker(wsMemberA, "create"),
		wsInvoker(wsMemberB, "create"),
		wsPersonInvoker(wsPersonA, "create"),
		wsPersonInvoker(wsPersonB, "create"),
	}
}

func wsWhy(t *testing.T, p *Plan) string {
	t.Helper()
	return wsWhyMembers(t, p, wsMembers)
}

// wsWhyMembers is wsWhy against the member table the workspace holds now, which
// the removal case below gives as the shrunk table: the cover is handed the
// members of the workspace's own config, so a member who has left the table is
// by definition not one of them.
func wsWhyMembers(t *testing.T, p *Plan, members []string) string {
	t.Helper()
	return strings.Join(wsCover.WorkspaceNotCovered(p, wsProject, WorkspaceServiceAccountID, wsBundle, members, wsPeople), "; ")
}

// wsWhyPeople is wsWhyMembers' other half: the cover is handed the roster the
// workspace holds now, so a person who has left the roster is by definition not
// one of them (WC2, R6).
func wsWhyPeople(t *testing.T, p *Plan, people []string) string {
	t.Helper()
	return strings.Join(wsCover.WorkspaceNotCovered(p, wsProject, WorkspaceServiceAccountID, wsBundle, wsMembers, people), "; ")
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
		name string
		// cfg is the plan's configuration, as the resource entries terraform
		// writes it: the member of a computed grant is only held to the account
		// it reads where the plan carries one (workspaceComputedMember), so a
		// case with no cfg is a plan built by hand without a configuration.
		cfg     string
		changes []string
	}{
		{"the root's own plan: the accounts, the role and grant, the service, its accessor and the members' invoke grants", "", wsDeployed()},
		{"the root's own plan, with the configuration that writes the member terraform computes", wsMemberExpr("google_service_account.workspace"), wsDeployed()},
		{"the same plan converged: every resource a no-op", "", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
			wsServiceChange(wsServiceAttrs{}, "no-op"),
			wsAccessor("", "", "", "", "no-op"),
			wsPersonAccessor("", "", wsPersonFence(), "no-op"),
			wsInvoker(wsMemberA, "no-op"),
			wsInvoker(wsMemberB, "no-op"),
			wsPersonInvoker(wsPersonA, "no-op"),
			wsPersonInvoker(wsPersonB, "no-op"),
		}},
		{"the service's own URL filled on a second apply: the service is updated and nothing else changes", "", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
			wsServiceChange(wsServiceAttrs{env: `"env":[{"name":"` + WorkspaceModeEnv + `","value":"1"},{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://fugaro-workspace-credentials-abc.a.run.app"}]`}, "update"),
			wsAccessor("", "", "", "", "no-op"),
			wsPersonAccessor("", "", wsPersonFence(), "no-op"),
			wsInvoker(wsMemberA, "no-op"),
			wsInvoker(wsMemberB, "no-op"),
			wsPersonInvoker(wsPersonA, "no-op"),
			wsPersonInvoker(wsPersonB, "no-op"),
		}},
		{"the workspace's account, its role and its grant alone: no service is deployed (var.credentials_service unset)", "", []string{
			wsPinnedRole(),
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}},
		{"the SA fallback's IAM account created while the rest is converged", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "no-op"),
		}},
		{"the accounts' display names updated, the grant held", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "update"),
			wsIAM(wsProject, WorkspaceIAMAccountID, "update"),
			wsComputedGrant(wsProject, wsRole, "update"),
		}},
		{"the member known, bare and as the root writes it", "", []string{
			wsSA(wsProject, WorkspaceServiceAccountID, "create"),
			wsKnownGrant(wsProject, wsEmail, wsRole, "create"),
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

// TestWorkspacePlanAdmitsRemovingAMembersInvokeGrant is F25's sentence and the
// plan's own constraint ("Removing a member needs no operator repair"): the ONE
// deletion the workspace root's plan holds is a member's invoke grant, so
// `fugaro update` converges a shrunk member table with no repair — and the
// deletion of anything else stays refused, the workspace's service and the
// bundle accessor included.
//
// The cover is handed the table the workspace holds AFTER the removal, which is
// the case that matters: the member who left is exactly the account that table
// no longer names, so the deletion is held to the project, the role and the
// plan's own service and not to the members — a rule that asked for the deleted
// principal to be among them would refuse every real removal and leave the
// operator repair this design has none of.
func TestWorkspacePlanAdmitsRemovingAMembersInvokeGrant(t *testing.T) {
	// The table the cover is given is the workspace's OWN, after the removal:
	// wsMemberA is the member who has left, so its account is exactly what the
	// table no longer names — which is why the deletion is not held to it.
	if why := wsWhyMembers(t, wsPlan(t,
		wsPinnedRole(),
		wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
		wsComputedGrant(wsProject, wsRole, "no-op"),
		wsServiceChange(wsServiceAttrs{}, "no-op"),
		wsAccessor("", "", "", "", "no-op"),
		wsInvokerDelete(wsMemberA),
		wsInvoker(wsMemberB, "no-op"),
	), []string{wsMemberB}); why != "" {
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
		{"an invoke grant deleted for a principal that is not a service account", wsInvokerDelete("group:workspace-owners@example.com"),
			"neither a service account address nor a person"},
		// The class of the address, not the presence of a `:` in the member, is
		// what says an account: these three carry no colon at all, and a reader
		// that took the bare spelling for a service account address admitted the
		// deletion of a grant to the whole internet (WC2's review).
		{"an invoke grant deleted for allUsers", wsInvokerDelete("allUsers"),
			"neither a service account address nor a person"},
		{"an invoke grant deleted for allAuthenticatedUsers", wsInvokerDelete("allAuthenticatedUsers"),
			"neither a service account address nor a person"},
		{"an invoke grant deleted for a bare domain", wsInvokerDelete("example.com"),
			"neither a service account address nor a person"},
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

// TestWorkspacePlanAdmitsRemovingAPersonsInvokeGrant is WC2's half of F25's
// sentence ("Removing a person needs no operator repair"): the SECOND deletion
// the workspace root's plan holds is a person's invoke grant, so `fugaro update`
// converges a roster that has dropped a person with no repair — and the deletion
// of anything else stays refused.
//
// The cover is handed the roster the workspace holds AFTER the removal, exactly
// as the member case is: the person who left is the address that roster no
// longer names, so the deletion is held to the project, the role and the plan's
// own service and not to the roster.
func TestWorkspacePlanAdmitsRemovingAPersonsInvokeGrant(t *testing.T) {
	if why := wsWhyPeople(t, wsPlan(t,
		wsPinnedRole(),
		wsSA(wsProject, WorkspaceServiceAccountID, "no-op"),
		wsComputedGrant(wsProject, wsRole, "no-op"),
		wsServiceChange(wsServiceAttrs{}, "no-op"),
		wsAccessor("", "", "", "", "no-op"),
		wsPersonAccessor("", "", wsPersonFence(), "no-op"),
		wsInvoker(wsMemberA, "no-op"),
		wsPersonInvokerDelete(wsPersonA),
		wsPersonInvoker(wsPersonB, "no-op"),
	), []string{wsPersonB}); why != "" {
		t.Errorf("removing a person must be no operator repair (F25, WC2): the plan that deletes its invoke grant and nothing else is this root's, got: %s", why)
	}

	refused := []struct {
		name  string
		extra string
		want  string
	}{
		{"a person's invoke grant deleted for a principal that is not a person or an account", wsDelete("google_cloud_run_v2_service_iam_member.workspace_person_invoker", workspaceInvokerType,
			wsInvokerAfter("", "", "", "group:workspace-owners@example.com")),
			"neither a service account address nor a person"},
		{"a person's invoke grant deleted on another service", wsDelete("google_cloud_run_v2_service_iam_member.workspace_person_invoker", workspaceInvokerType,
			wsInvokerAfter("", "fugaro-workspace-credentials-v2", "", "user:"+wsPersonA)),
			"deleted, and the workspace's own service is this plan's own workspace service"},
		{"a person's invoke grant deleted in another project", wsDelete("google_cloud_run_v2_service_iam_member.workspace_person_invoker", workspaceInvokerType,
			wsInvokerAfter("member-project-5678", "", "", "user:"+wsPersonA)),
			"an invoke grant in project"},
		{"the person accessor deleted", wsDelete("google_project_iam_member.workspace_person_token", workspaceGrantType,
			`"project":"`+wsProject+`","role":"`+WorkspaceBundleAccessorRole+`","member":"serviceAccount:`+wsEmail+`"`),
			"actions delete"},
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

// TestTheWorkspaceAccessorIsThePersonsOneName is R6's own rule (WC2, D2971): the
// ONE conditioned accessor the workspace's own account holds is a person's
// Claude token and no other secret of the workspace's project, and the cover
// admits exactly the fence the root writes (gcp.WorkspacePersonTokenCondition)
// and refuses every wider one by name.
//
// Everything wider is here: no condition at all (every secret of the project),
// the per-person prefix alone (every personal name a person keeps there), the
// intake's own expression (which admits the project-shared class too), the
// shared class alone, another name's suffix, an expression that merely CONTAINS
// the fence, the right expression under another title, another member and
// another project.
func TestTheWorkspaceAccessorIsThePersonsOneName(t *testing.T) {
	fence := gcp.WorkspacePersonTokenCondition()
	prefix := `resource.name.extract("/secrets/{secret}").startsWith("fugaro_u_")`
	cond := func(title, expr string) string {
		return `[{"title":"` + title + `","expression":"` + strings.ReplaceAll(expr, `"`, `\"`) + `","description":null}]`
	}

	admitted := []struct {
		name string
		cfg  string
		rc   string
	}{
		{"the root's own accessor, the member known", "", wsPersonAccessor("", "", wsPersonFence(), "create")},
		{"the root's own accessor, the member terraform computes", wsPersonAccessorExpr("google_service_account.workspace"),
			`{"address":"google_project_iam_member.workspace_person_token","mode":"managed","type":"` + workspaceGrantType + `","change":{"actions":["create"],"after":{"project":"` + wsProject + `","role":"` + WorkspaceBundleAccessorRole + `","condition":` + wsPersonFence() + `},"after_unknown":{"member":true}}}`},
	}
	for _, c := range admitted {
		t.Run(c.name, func(t *testing.T) {
			var p *Plan
			if c.cfg == "" {
				p = wsPlan(t, c.rc)
			} else {
				p = wsPlanCfg(t, c.cfg, wsSA(wsProject, WorkspaceServiceAccountID, "create"), c.rc)
			}
			if why := wsWhy(t, p); why != "" {
				t.Errorf("the workspace's own person accessor must be covered, got: %s", why)
			}
		})
	}

	refused := []struct {
		name string
		cond string
		want string
	}{
		{"no condition at all", "", "with NO condition"},
		{"the per-person prefix alone", cond(gcp.WorkspacePersonTokenConditionTitle, prefix), "under a condition that is not"},
		{"the intake's own expression, which admits the shared class", cond(gcp.WorkspacePersonTokenConditionTitle, gcp.DynamicSecretNamesCondition()), "under a condition that is not"},
		{"the project-shared class alone", cond(gcp.WorkspacePersonTokenConditionTitle, `resource.name.extract("/secrets/{secret}").startsWith("fugaro_p_")`), "under a condition that is not"},
		{"another name's suffix", cond(gcp.WorkspacePersonTokenConditionTitle, prefix+` && resource.name.extract("/secrets/{secret}").endsWith("_github-token")`), "under a condition that is not"},
		{"an expression that merely contains the fence", cond(gcp.WorkspacePersonTokenConditionTitle, "true || ("+fence+")"), "under a condition that is not"},
		{"the right expression under another title", cond("fugaro-somebody-elses", fence), "under a condition that is not"},
		{"two conditions", `[{"title":"` + gcp.WorkspacePersonTokenConditionTitle + `","expression":"true"},{"title":"` + gcp.WorkspacePersonTokenConditionTitle + `","expression":"true"}]`, "more than one condition"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsPlan(t, wsPersonAccessor("", "", c.cond, "create")))
			if why == "" {
				t.Fatalf("this plan must be refused: %s", c.name)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("the refusal must say why, want a sentence with %q, got: %s", c.want, why)
			}
		})
	}

	refusedMember := []struct {
		name string
		rc   string
		want string
	}{
		{"the accessor for a member's job account", wsPersonAccessor("", "serviceAccount:"+wsMemberA, wsPersonFence(), "create"), "read by " + wsEmail + " and by nobody else"},
		{"the accessor for a person", wsPersonAccessor("", "user:"+wsPersonA, wsPersonFence(), "create"), "read by " + wsEmail + " and by nobody else"},
		{"the accessor in another project", wsPersonAccessor("member-project-5678", "", wsPersonFence(), "create"), "a project accessor in project"},
	}
	for _, c := range refusedMember {
		t.Run(c.name, func(t *testing.T) {
			why := wsWhy(t, wsPlan(t, c.rc))
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
// in another project or to a principal that is not a member's job account or a
// roster person — and the workspace's own role, account and grant held as they
// were.
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
		{"the database role granted to the SA fallback's account, bare", []string{
			wsKnownGrant(wsProject, wsIAMEmail, wsRole, "create"),
		}, "reached by " + wsEmail + " and by nobody else"},
		{"the database role granted to the SA fallback's account, as the root writes a member", []string{
			wsKnownGrant(wsProject, "serviceAccount:"+wsIAMEmail, wsRole, "create"),
		}, "reached by " + wsEmail + " and by nobody else"},
		{"the database role granted to the SA fallback's account's own address", []string{
			wsKnownGrant(wsProject, "google_service_account.workspace_iam.member", wsRole, "create"),
		}, "reached by " + wsEmail + " and by nobody else"},
		{"another role on the workspace's account", []string{
			wsKnownGrant(wsProject, "serviceAccount:"+wsEmail, "roles/editor", "create"),
		}, "carries two project grants and no third"},
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
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:latest"}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		// The image cases below are the closure's own half, one reference each:
		// the same digest under another registry (a foreign project's, which is
		// the minimum this rule has to refuse), another owner's path through the
		// remote repository, a mutable tag that is not a release, the
		// operators' own writable base repository rather than the remote
		// repository, and an image that is not a fugaro-<kind> at all. The
		// reference is what an operator chooses, and the account behind it reads
		// every model credential the workspace holds.
		{"the service on a digest under another registry", []string{
			wsServiceChange(wsServiceAttrs{image: "europe-west1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		{"the service on another project's registry", []string{
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/member-project-5678/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		{"the service on another owner's image through the remote repository", []string{
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/stranger/fugaro-base:1.2.3@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		{"the service on a mutable tag through the remote repository", []string{
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:latest@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		{"the service on the run's own base repository rather than the remote one", []string{
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-base/dimipaun/fugaro-base:1.2.3@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
		{"the service on an image that is not a fugaro-<kind>", []string{
			wsServiceChange(wsServiceAttrs{image: "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/base:1.2.3@sha256:" + wsDigest}, "create"),
		}, "released pin an installation's credentials service runs and nothing else"},
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
		{"an invoke grant to a person the roster does not name", []string{
			wsInvoker("user:someone@example.com", "create"),
		}, "invoked by a member's own job accounts and by the roster's own people alone"},
		{"an invoke grant to allUsers", []string{
			wsInvoker("allUsers", "create"),
		}, "invoked by a member's own job accounts and by the roster's own people alone"},
		{"an invoke grant to a member of another workspace", []string{
			wsInvoker("serviceAccount:fugaro-run-stranger@member-gamma-9012.iam.gserviceaccount.com", "create"),
		}, "invoked by a member's own job accounts and by the roster's own people alone"},
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
		}, "creates two and no third"},
		{"the member computed while only the SA fallback's account is created", []string{
			wsIAM(wsProject, WorkspaceIAMAccountID, "create"),
			wsComputedGrant(wsProject, wsRole, "create"),
		}, "does not create the workspace's own service account"},
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
	if why := wsCover.WorkspaceNotCovered(nil, wsProject, WorkspaceServiceAccountID, wsBundle, wsMembers, wsPeople); len(why) == 0 {
		t.Fatal("a nil plan must be refused")
	}
}

// TestWorkspacePlanRefusesAnImageWithoutARecordedOwner is the fail-closed end of
// the image rule, and the half that makes the owner a GIVEN rather than a read:
// the owner a pin is checked against is the one this machine's config records
// (init's baseOwner, over localcfg's image_source), so a cover built from a
// config that records none admits no reference through the remote repository at
// all — the root's own pin included. A rule that read the owner out of the
// reference it is judging would admit whoever published the image, run as the
// account that reads every model credential the workspace holds.
func TestWorkspacePlanRefusesAnImageWithoutARecordedOwner(t *testing.T) {
	unrecorded := wsCover
	unrecorded.Owner = ""
	why := strings.Join(unrecorded.WorkspaceNotCovered(wsPlan(t, wsDeployed()...), wsProject,
		WorkspaceServiceAccountID, wsBundle, wsMembers, wsPeople), "; ")
	if !strings.Contains(why, "released pin an installation's credentials service runs and nothing else") {
		t.Fatalf("a cover with no recorded owner must refuse the service's image, got: %s", why)
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
	accounts := 0
	for _, m := range regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{`).FindAllStringSubmatch(src, -1) {
		types[m[1]] = true
		if m[1] == workspaceAccountType {
			accounts++
		}
	}
	if accounts != 2 {
		t.Fatalf("the workspace root declares %d service accounts, want two: the workspace's own service (W6) and the SA fallback's IAM account (D2803, F20 — two accounts, never one)", accounts)
	}
	if !regexp.MustCompile(`(?m)^\s*account_id\s*=\s*"` + WorkspaceIAMAccountID + `"\s*$`).MatchString(src) {
		t.Errorf("the root does not declare the SA fallback's account id the cover pins (%s): the cover holds that account by its literal, because the root gives it no variable the account's own name could travel in", WorkspaceIAMAccountID)
	}
	for _, typ := range []string{workspaceRoleType, workspaceAccountType, workspaceGrantType, workspaceServiceType, workspaceAccessorType, workspaceInvokerType} {
		if !types[typ] {
			t.Errorf("the workspace root does not declare a %s, and the cover admits a plan of it", typ)
		}
	}
	if len(types) != 6 {
		t.Fatalf("the workspace root must declare exactly the workspace's role, its TWO accounts (the workspace's own service and the SA fallback's IAM account), that service's one grant, its credentials service, that service's bundle accessor and its one conditioned accessor, and its members' and its roster's people's invoke grants, got %v", types)
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
		{"the roster's people's invoke grants", `"user:${each.value}"`},
		{"the person accessor's condition title", `title = "` + gcp.WorkspacePersonTokenConditionTitle + `"`},
		{"the person accessor's fence", `expression = "` + strings.ReplaceAll(gcp.WorkspacePersonTokenCondition(), `"`, `\"`) + `"`},
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
