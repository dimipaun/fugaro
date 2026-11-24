package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspace_create_test.go is the workspace's own project's root (W3 of the
// workspaces plan), driven directly: the stage the workspace owner's `fugaro
// setup` and `fugaro update` run — the workspace's own service account and the
// ONE grant that reaches the workspace's database — what it does when this
// installation is a workspace's own, what it does when it is not (nothing, and
// never a grant), and what it refuses.
//
// Every test is hermetic: the fake terraform, the fake cloud the rig's config
// points at, a private XDG state home, and no cloud.
//
// The owner's half of the config is update_workspace_test.go's own fixture
// (wsOwnerTable and wsOwnerCap, the shape `fugaro workspace project add` and
// `cap set` write): the stage's predicate is W2's, so the config that makes
// this installation a workspace's own is the same document, and a second
// spelling of it here would be a second reading of what a workspace is.

// wsSplitFirebase is the budget block of an installation whose Firebase is
// ANOTHER project's — what `fugaro setup --firebase-project <other>` leaves in
// the file — which is the one shape in which this root's own grant cannot reach
// the workspace's database (workspaceSplitFirebase).
const wsSplitFirebase = "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-9999, rtdb_url: https://proj-9999-default-rtdb.firebaseio.com }\n"

// wsProjectName is the name the rig's config gives this installation: the
// workspace's own name, which is the word a typed confirmation asks for and the
// `workspace` the root's tfvars carry.
const wsProjectName = "workspace-alpha"

// wsProject is the GCP project the rig's config names (initConfig's own).
const wsProject = initProject

// wsCreateRig is the init rig (fake terraform on PATH, fake cloud, private XDG
// directories) with ONE installation's config, named wsProjectName, the extra
// YAML appended to it, and the stage built over an engine that has not set
// itself up yet.
type wsCreateRig struct {
	ri  *initRig
	e   *initEngine
	s   *workspaceProjectStage
	out *syncBuf
}

// newWSCreateRig builds the rig. stdin answers the stage's typed confirmation
// (the project's name), and extra is what the config declares on top of the
// installation — the owner's half, a member's pin, or nothing.
func newWSCreateRig(t *testing.T, stdin, extra string) *wsCreateRig {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	ri := newInitRig(t)
	b, err := os.ReadFile(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := wsWithRepos(strings.Replace(string(b), "name: aurora", "name: "+wsProjectName, 1))
	if err := os.WriteFile(ri.cfg, []byte(cfg+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The one command the stage's sentences name is the program as the user ran
	// it (selfCommand), which under `go test` is the test binary's path: pinned
	// to the bare name here, so the test reads the sentence an owner reads.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(stdin))
	run := newInitRun(cmd, &initOptions{door: updateDoor.name})
	run.setProject(lc)
	e := &initEngine{r: run, lc: lc, path: ri.cfg}
	return &wsCreateRig{ri: ri, e: e, s: newWorkspaceProjectStage(e), out: out}
}

// newWSCreateOwnerRig is the same for an installation that declares a workspace
// of its own: the member table and the cap.
func newWSCreateOwnerRig(t *testing.T, stdin string) *wsCreateRig {
	t.Helper()
	return newWSCreateRig(t, stdin, wsOwnerTable+wsOwnerCap)
}

func (g *wsCreateRig) check(t *testing.T) initflow.Status {
	t.Helper()
	st, err := g.s.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return st
}

func (g *wsCreateRig) plan(t *testing.T, env initflow.Env) (initflow.Plan, error) {
	t.Helper()
	return g.s.Plan(context.Background(), env)
}

func (g *wsCreateRig) apply(t *testing.T, env initflow.Env) (initflow.Outcome, error) {
	t.Helper()
	return g.s.Apply(context.Background(), env)
}

// workdir is the workspace root's own workdir: $XDG_STATE_HOME's project tree,
// beside the installation's, which is where a stage that reused the
// installation's would have written instead.
func (g *wsCreateRig) workdir() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", wsProject, "workspace")
}

func (g *wsCreateRig) root() string { return filepath.Join(g.workdir(), "gcp", "roots", "workspace") }

func (g *wsCreateRig) tfvars(t *testing.T) workspaceVars {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.root(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var v workspaceVars
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// noChanges makes the next plan of the workspace root find nothing to do,
// which is what a converged root plans.
func (g *wsCreateRig) noChanges(t *testing.T) {
	t.Helper()
	g.ri.script["plan"] = map[string]any{"exit": 0}
	g.ri.save(t)
}

// wsAccountChange is the workspace's own service account as the root plans it:
// both of its names literal, which is why the root is given them as variables.
func wsAccountChange(actions ...string) planChange {
	return planChange{
		"address": "google_service_account.workspace", "mode": "managed", "type": "google_service_account",
		"change": map[string]any{"actions": actions, "after": map[string]any{
			"project": wsProject, "account_id": tf.WorkspaceServiceAccountID}},
	}
}

// wsIAMAccountChange is the SA fallback's IAM account as the root plans it
// (WU3, D2803): the second account the root creates, whose id is the root's own
// literal rather than one of its variables. It is the ONLY change an
// installation that already has the service account and its one grant plans.
func wsIAMAccountChange(actions ...string) planChange {
	return planChange{
		"address": "google_service_account.workspace_iam", "mode": "managed", "type": "google_service_account",
		"change": map[string]any{"actions": actions, "after": map[string]any{
			"project": wsProject, "account_id": tf.WorkspaceIAMAccountID}},
	}
}

// wsGrantChange is the root's one grant as the root really plans it: the member
// is the address of the account this plan creates, so terraform knows it only
// at apply.
func wsGrantChange(actions ...string) planChange {
	return planChange{
		"address": "google_project_iam_member.workspace_database", "mode": "managed", "type": "google_project_iam_member",
		"change": map[string]any{"actions": actions,
			"after":         map[string]any{"project": wsProject, "role": tf.WorkspaceDatabaseRole(wsProject)},
			"after_unknown": map[string]any{"member": true}},
	}
}

// The workspace root's plan, resource by resource, as an owner's run really
// plans it: the shapes tf.WorkspaceNotCovered reads, in the map form the fake
// terraform is scripted with (setPlanAt). A test that handed the stage a plan
// the root does not make would be testing the cover rather than the stage, so
// every field below is one the root's own main.tf writes. The module-level
// fixture of the same shapes is internal/infra/tf/workspace_test.go's.

// wsProjectAccount is the workspace's own service account's address: the
// identity the root's service runs as and its one database grant binds.
func wsProjectAccount() string {
	return tf.WorkspaceServiceAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
}

// wsRolePlan is the root's own custom role, holding the ONE role's two pinned
// permissions.
func wsRolePlan() planChange {
	return planChange{
		"address": "google_project_iam_custom_role.workspace_database", "mode": "managed",
		"type": "google_project_iam_custom_role",
		"change": planChange{"actions": []string{"create"}, "after": planChange{
			"project": wsProject, "role_id": tf.WorkspaceDatabaseRoleID,
			"permissions": tf.WorkspaceDatabasePermissions}},
	}
}

// wsServicePlan is the workspace's own credentials service as the root plans it
// on image: the workspace's own project and region, IAM required, R9's limits,
// the zero minimum at both levels, one container on the image and the
// environment names the process reads — the one that tells a workspace's
// service from an installation's among them (tf.WorkspaceServiceEnv).
func wsServicePlan(image string) planChange {
	env := make([]any, 0, len(tf.WorkspaceServiceEnv))
	for _, n := range tf.WorkspaceServiceEnv {
		env = append(env, planChange{"name": n, "value": "v", "value_source": []any{}})
	}
	return planChange{
		"address": "google_cloud_run_v2_service.workspace", "mode": "managed",
		"type": "google_cloud_run_v2_service",
		"change": planChange{"actions": []string{"create"}, "after": planChange{
			"project": wsProject, "location": "us-east5", "name": creds.WorkspaceServiceName,
			"invoker_iam_disabled": false,
			"scaling":              []any{planChange{"scaling_mode": "AUTOMATIC", "min_instance_count": 0}},
			"template": []any{planChange{
				"service_account":                  wsProjectAccount(),
				"max_instance_request_concurrency": tf.WorkspaceConcurrency,
				"scaling":                          []any{planChange{"min_instance_count": 0, "max_instance_count": tf.WorkspaceMaxInstances}},
				"containers":                       []any{planChange{"image": image, "env": env}},
			}},
		}},
	}
}

// wsServicePlanWith is wsServicePlan under another action list: terraform plans
// the service as a create on a first run, as a no-op where it is listed and not
// moved, and as an update where the state already holds it and only one
// environment value changes — the audience, which is the one attribute this
// root can move in a converged state.
func wsServicePlanWith(image string, actions ...string) planChange {
	p := wsServicePlan(image)
	p["change"].(planChange)["actions"] = actions
	return p
}

// wsServiceAudiencePlan is what the audience write's own apply plans: the
// workspace's service and nothing else, the URL moving, which is the plan a
// converged root makes when the block it is planned with carries the audience
// the service is served with (fillServiceURL writes the tfvars first and plans
// that document).
func wsServiceAudiencePlan(image string) planChange {
	return wsServicePlanWith(image, "update")
}

// wsServiceNoOpPlan is the workspace's own service as a plan holds it on a run
// that is not moving it: terraform lists it with no action, and it is where the
// plan's own service name is read from (planWorkspaceServiceName) — so a plan
// whose changes are a member's grant going away and nothing else still carries
// the service the grant is on.
func wsServiceNoOpPlan(image string) planChange {
	return wsServicePlanWith(image, "no-op")
}

// wsAccessorPlan is the root's ONE bundle accessor: the workspace's own
// container, the read, and the workspace's own account. ws is the workspace's
// name, which is the project's own (localcfg's Name) and what the container is
// derived from.
func wsAccessorPlan(ws string) planChange {
	return planChange{
		"address": "google_secret_manager_secret_iam_member.workspace_bundle", "mode": "managed",
		"type": "google_secret_manager_secret_iam_member",
		"change": planChange{"actions": []string{"create"}, "after": planChange{
			"project": wsProject, "secret_id": credsvc.WorkspaceBundleSecretID(ws),
			"role": tf.WorkspaceBundleAccessorRole, "member": wsProjectAccount()}},
	}
}

// wsInvokerAfter is one member's invoke grant as the root writes it.
func wsInvokerAfter(member string) planChange {
	return planChange{
		"project": wsProject, "location": "us-east5", "name": creds.WorkspaceServiceName,
		"role": tf.WorkspaceInvokerRole, "member": member}
}

// wsInvokerPlan is one member's invoke grant on the workspace's service.
func wsInvokerPlan(member string) planChange {
	return planChange{
		"address": "google_cloud_run_v2_service_iam_member.workspace_invoker", "mode": "managed",
		"type":   "google_cloud_run_v2_service_iam_member",
		"change": planChange{"actions": []string{"create"}, "after": wsInvokerAfter(member)},
	}
}

// wsInvokerRemovedPlan is the same grant as terraform plans it when the member
// leaves the owner's table: the state it had, and no `after`. It is the ONE
// deletion the root's cover admits (F25), and the plan that makes it carries no
// other change for that member.
func wsInvokerRemovedPlan(member string) planChange {
	return planChange{
		"address": "google_cloud_run_v2_service_iam_member.workspace_invoker", "mode": "managed",
		"type":   "google_cloud_run_v2_service_iam_member",
		"change": planChange{"actions": []string{"delete"}, "before": wsInvokerAfter(member), "after": nil},
	}
}

// wsRootPlan is the whole workspace root as an owner's first apply plans it, on
// image, with one invoke grant per member job account the owner's table names.
// ws is the workspace's name (the project's own).
func wsRootPlan(ws, image string, members ...string) []planChange {
	out := []planChange{wsRolePlan(), wsAccountChange("create"), wsIAMAccountChange("create"),
		wsGrantChange("create"), wsServicePlan(image), wsAccessorPlan(ws)}
	for _, m := range members {
		out = append(out, wsInvokerPlan(m))
	}
	return out
}

// wsRootVars is the workspace root's tfvars as a converge left them: the
// document an owner's run writes into the workdir of its own
// (infra.WorkspaceWorkdir), read from the fixture's state home rather than from
// a rig's, because the tests that read it run the whole update door.
func wsRootVars(t *testing.T) workspaceVars {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var v workspaceVars
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// wsWorkdirRoot is the workspace root's own directory in the fixture's state
// home: $XDG_STATE_HOME's project tree, beside the installation's.
func wsWorkdirRoot() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", wsProject, "workspace", "gcp", "roots", "workspace")
}

// wsSeedRecorded writes the workspace root's tfvars as a run of an earlier
// release left them — the block this update either replaces or carries forward,
// and the image the in-flight note reads (workspaceServiceRevisionStale). It is
// the state a workdir is in before the run's own prepare rebuilds the tree:
// PrepareWorkdir snapshots the file first, and that snapshot is what a run that
// deploys nothing writes back.
//
// url is the audience that run read back from the root's own output, and an
// empty one is the state a run leaves behind when its second apply never landed
// — a create the apply never made, a `--plan-only` pass: the block names an
// image and no audience, so the revision it is the tfvars of admits nobody.
func wsSeedRecorded(t *testing.T, ws, image, url string) {
	t.Helper()
	if err := os.MkdirAll(wsWorkdirRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := workspaceVars{Project: wsProject, Workspace: ws, Members: map[string]workspaceMemberVar{}}
	doc.Names.WorkspaceAccountID = tf.WorkspaceServiceAccountID
	doc.Service = &workspaceServiceVar{
		Service: creds.WorkspaceServiceName, Image: image, Region: "us-east5",
		BundleSecret:    credsvc.WorkspaceBundleSecretID(ws),
		FirebaseProject: wsProject, RTDBURL: "https://proj-1234-default-rtdb.firebaseio.com",
		URL: url}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wsServiceURL is the address the fake root's outputs assign the workspace's
// service: Cloud Run's own uri, which the second apply writes into
// credentials_service.url and a member's run mints its ID token for. It is a
// URL and not an address anybody reaches: nothing in a run of these tests
// resolves it.
const wsServiceURL = "https://fugaro-workspace-credentials-abc123-ue.a.run.app"

// TestWorkspaceProjectStageSkipsAnInstallationThatIsNotAWorkspace: a member's
// project and a plain one carry no workspace of their own (spec §5), so the
// stage is a SKIP with the reason and never an error — it is most
// installations, and a Check that answered an error would stop their run — it
// plans nothing to do, applies nothing, and never reaches terraform at all.
//
// The third case is the one config that carries BOTH halves: a member's project
// whose file also writes workspace_cap, which is what `fugaro workspace cap set
// --workspace <that project>` leaves behind. Creating the workspace's own
// account and its one grant there is the widening spec §5 forbids, so the
// member's half decides and the owner's half is not this installation's
// (workspaceDeclared).
func TestWorkspaceProjectStageSkipsAnInstallationThatIsNotAWorkspace(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra string
	}{
		{"a plain installation", ""},
		{"a member's project", wsPin},
		{"a member's project that also declares the owner's half", wsPin + wsOwnerCap},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newWSCreateRig(t, wsProjectName+"\n", c.extra)
			st := g.check(t)
			if st.State != initflow.Skipped {
				t.Fatalf("state = %v (%s), want a skip", st.State, st.Detail)
			}
			// Nothing to do, and not work left undone: there is no account to
			// create and no grant to make in a project that has no workspace of
			// its own, so a step that asked for this stage is answered by this
			// check rather than left owed (initflow.Status.Undone, read by
			// applyAnswered).
			if st.Undone {
				t.Errorf("a skip that has no work to leave undone reads Undone: %s", st.Detail)
			}
			for _, want := range []string{"workspace_members", "workspace_cap", "spec §5", wsProjectName} {
				if !strings.Contains(st.Detail, want) {
					t.Errorf("the skip's reason does not name %q: %s", want, st.Detail)
				}
			}
			p, err := g.plan(t, initflow.Env{Interactive: true})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if !p.NothingToDo || p.Detail != st.Detail {
				t.Errorf("plan = %+v, want nothing to do with the skip's own reason", p)
			}
			oc, err := g.apply(t, initflow.Env{Interactive: true})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if oc.Changed || g.e.r.res.Applied {
				t.Errorf("apply changed something: %+v, applied %v", oc, g.e.r.res.Applied)
			}
			if calls := g.ri.calls(t); len(calls) != 0 {
				t.Errorf("the stage reached terraform for an installation that is not a workspace's own: %q", calls)
			}
			if _, err := os.Stat(g.root()); !os.IsNotExist(err) {
				t.Errorf("the workspace workdir was prepared: %v", err)
			}
		})
	}
}

// TestWorkspaceProjectStageSkipsAWorkspaceWhoseFirebaseIsAnotherProject: the
// workspace's own project holds the workspace's Firebase, its database and its
// service (spec §3), so an installation whose budget.firebase_project names
// another project is one where this root's one grant would bind on a database
// the workspace does not use — the record W2's publish stage writes and the
// workspace's own service reads lives in the Firebase project's — and where the
// plan check would accept it, because the stage holds the plan to the same
// wrong project.
//
// The stage SKIPS with that reason and does not fail the run: the record, the
// layer, the checkout and every stage behind this one are still the upgrade the
// owner ran `fugaro update` for, and one command is the whole of it (the HARD
// RULE — workspace_create.go's declared). Nothing is planned, no service
// account is created and no grant is made, and the reason names the two
// projects and the migration a split installation takes. All three paths answer
// it the same way, and terraform is never reached: no workdir, no init, no
// plan.
func TestWorkspaceProjectStageSkipsAWorkspaceWhoseFirebaseIsAnotherProject(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateRig(t, wsProjectName+"\n", wsOwnerTable+wsOwnerCap+wsSplitFirebase)

	st := g.check(t)
	if st.State != initflow.Skipped {
		t.Fatalf("check = %v (%s), want the skip a split installation takes", st.State, st.Detail)
	}
	// This skip is the other half of the rule the test above pins: THIS
	// project is the workspace's own, and the account and the single grant this
	// stage converges are the whole of what it is here for. Neither is created
	// on a database the workspace does not use, so the stage's work is undone
	// and a step that asked for this stage stays owed rather than being read as
	// answered (initflow.Status.Undone).
	if !st.Undone {
		t.Errorf("a skip that created no account and made no grant does not read Undone: %s", st.Detail)
	}
	for _, want := range []string{
		"budget.firebase_project", "proj-9999", "docs/workspaces/spec.md §3",
		wsProject, wsProjectName, "fugaro update --firebase-project " + wsProject,
		"the rest of `fugaro update` runs as usual",
	} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the skip does not name %q: %s", want, st.Detail)
		}
	}

	p, err := g.plan(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !p.NothingToDo || p.Detail != st.Detail {
		t.Errorf("plan = %+v, want nothing to do, with the skip's own reason", p)
	}

	// Apply is unreachable from a run whose Check skipped. Asked directly it
	// answers the same reason and writes nothing, which is the property that
	// matters: no grant on a database the workspace does not use.
	o, err := g.apply(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if o.Changed || o.Detail != st.Detail {
		t.Errorf("apply = %+v, want no change and the skip's own reason", o)
	}

	if calls := g.ri.calls(t); len(calls) != 0 {
		t.Errorf("the stage reached terraform for a workspace whose Firebase is another project's: %q", calls)
	}
	if _, err := os.Stat(g.root()); !os.IsNotExist(err) {
		t.Errorf("the workspace workdir was prepared: %v", err)
	}
}

// wsSplitFirebaseProject is the other project a split installation's config
// names in budget.firebase_project: the Firebase project it adopted before the
// one-project rule (design D3), which is the state workspaceSplitFirebase reads.
const wsSplitFirebaseProject = "proj-9999"

// splitWorkspaceOwnerConfig makes the selected installation a split workspace
// owner: the owner's half of the membership in its config, and
// budget.firebase_project naming another project than gcp_project.
//
// The database URL stays the rig's own fake: nothing in a plain `fugaro update`
// reads budget.firebase_project — the Firebase stage is skipped without
// --firebase-project, and the workspace stage is the one that reads the pair —
// so the fake stands for the adopted project's database exactly as
// updateBudgetRigFor's fixture has it.
func splitWorkspaceOwnerConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(b), "firebase_project: "+initProject, "firebase_project: "+wsSplitFirebaseProject, 1)
	if text == string(b) {
		t.Fatalf("the fixture's config does not name budget.firebase_project %s:\n%s", initProject, b)
	}
	if err := os.WriteFile(path, []byte(wsWithRepos(text)+wsOwnerTable+wsOwnerCap), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateConvergesASplitWorkspaceOwnerInOneCommand: the HARD RULE at the one
// state this root cannot converge, and the reason the stage skips rather than
// fails (workspace_create.go's declared). An installation that declares a
// workspace of its own while its Firebase is another project's has been
// updating without this stage all along — W2's publish shipped in v0.21.0 and
// the workspace command is public, so the release behind converges the state —
// and this release may not take that away: `fugaro update` skips the workspace's
// own project, names why, creates no service account and makes no grant, and
// every stage behind it converges in the same run.
//
// The fixture is the previous release's (update_converge_test.go): the same
// `fugaro update --yes` an owner runs, over a checkout whose rules generation,
// published policy and plugin pin are behind, with the owner's half in the
// config and budget.firebase_project naming another project.
//
// The record node is there already, which is what makes this an installation at
// the previous version rather than a fresh one: W2's publish shipped in v0.21.0,
// so a run of it left the node the config declares, and this release's update
// finds nothing to write (and so nothing to take to a terminal). That node is
// also the stage behind the skipped one, and what the update does with it is
// the proof that the run went on.
func TestUpdateConvergesASplitWorkspaceOwnerInOneCommand(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	currentRelease(t)
	splitWorkspaceOwnerConfig(t)
	r.db.Set(projectpolicy.PathWorkspace(initProjectName), map[string]any{
		"version": projectpolicy.WorkspaceVersion,
		"name":    initProjectName,
		"members": []any{wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b")},
		"cap":     map[string]any{"monthly_micros": 25_000_000},
	})

	// The state is doctor's own line too, before the update and after it: the
	// update does not change it, and the migration is the owner's.
	before := doctorChecks(t, r.root)
	if c, ok := doctorCheckByID(before.Checks, workspaceSplitCheckID); !ok || c.OK {
		t.Fatalf("doctor does not report the split workspace owner: %+v", c)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	// The step's line is the first clause of its reason and the log the whole of
	// it (report.Brief), so the owner sees the skip on the screen and reads why
	// it did not run — the pair, the config key and the way out — in the log.
	if !strings.Contains(out+newestLog(t, "update"), "– skipped      workspace-project this installation declares a workspace of its own") {
		t.Errorf("the update's screen does not show the workspace's own project skipped for the split:\n%s", out)
	}
	whole := out + newestLog(t, "update")
	for _, want := range []string{"workspace-project", "its Firebase is another project's", "budget.firebase_project", "fugaro update --firebase-project " + initProject} {
		if !strings.Contains(whole, want) {
			t.Errorf("the update's account does not carry %q:\n%s", want, whole)
		}
	}
	// Nothing reached the workspace's own root: the stage skipped before
	// opening a workdir, so no init, no plan and no apply happened there.
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initProject, "workspace")); !os.IsNotExist(err) {
		t.Errorf("the skipped stage opened the workspace root's workdir: %v", err)
	}
	for _, root := range r.applies(t) {
		if root == "workspace" {
			t.Errorf("the skipped stage applied the workspace root: %v", r.applies(t))
		}
	}
	// The stage behind it ran: the update read the node the previous release
	// left, found it carrying what this installation declares and went on to
	// the layer, the checkout and the rest (convergeOn's own assertions).
	if !strings.Contains(whole, "carries what this installation declares") {
		t.Errorf("the update's account does not carry the record stage's own line, so the run did not reach it:\n%s", whole)
	}
	if r.db.Value(projectpolicy.PathWorkspace(initProjectName)) == nil {
		t.Errorf("the workspace record the previous release published is gone")
	}

	after := doctorChecks(t, r.root)
	if c, ok := doctorCheckByID(after.Checks, workspaceSplitCheckID); !ok || c.OK {
		t.Errorf("after fugaro update, doctor no longer reports the split workspace owner: %+v", c)
	}
}

// TestWorkspaceProjectStageConvergesTheWorkspacesOwnProject: the owner's
// installation plans and applies the workspace root in its own workdir, under
// the installation's own state bucket and a state prefix of its own, with the
// three values the root takes from this machine's config; a second run finds
// nothing to do and applies nothing, and Verify re-reads the cloud the apply
// touched.
func TestWorkspaceProjectStageConvergesTheWorkspacesOwnProject(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateOwnerRig(t, wsProjectName+"\n")
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))

	st := g.check(t)
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "service account") {
		t.Fatalf("check = %v (%s), want a todo that says what the stage creates", st.State, st.Detail)
	}

	p, err := g.plan(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.NothingToDo || p.Detail == "" {
		t.Fatalf("plan = %+v, want the account and its grant", p)
	}
	if out := g.out.String(); !strings.Contains(out, "google_service_account.workspace") {
		t.Errorf("the plan the stage showed does not carry the root's own resources:\n%s", out)
	}
	if len(g.ri.ran(t, "apply")) != 0 {
		t.Fatal("plan applied something")
	}

	oc, err := g.apply(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !oc.Changed || !g.e.r.res.Applied {
		t.Errorf("apply = %+v, applied %v, want a change", oc, g.e.r.res.Applied)
	}
	out := g.out.String()
	account := tf.WorkspaceServiceAccountID + "@" + wsProject + ".iam.gserviceaccount.com"
	for _, want := range []string{account, tf.WorkspaceDatabaseRole(wsProject), "GRANT", initflow.AgentAdvice} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation the operator reads does not name %q:\n%s", want, out)
		}
	}

	v := g.tfvars(t)
	if v.Project != wsProject || v.Workspace != wsProjectName || v.Names.WorkspaceAccountID != tf.WorkspaceServiceAccountID {
		t.Errorf("the workspace root's tfvars = %+v, want project %s, workspace %s and account id %s",
			v, wsProject, wsProjectName, tf.WorkspaceServiceAccountID)
	}
	backend, err := os.ReadFile(filepath.Join(g.root(), infra.BackendFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`bucket = "fugaro-tfstate-` + wsProject + `"`, `prefix = "` + infra.StatePrefixWorkspace + `"`} {
		if !strings.Contains(string(backend), want) {
			t.Errorf("the workspace root's backend does not carry %s:\n%s", want, backend)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", wsProject, "installation", infra.VarsFile)); !os.IsNotExist(err) {
		t.Errorf("the installation's own tfvars were written by the workspace root: %v", err)
	}
	if applied := g.ri.ran(t, "apply"); len(applied) != 1 {
		t.Errorf("apply ran %d time(s), want one: %q", len(applied), applied)
	}

	// The converge re-reads what it applied: a fresh plan that finds nothing is
	// the apply being true, and a second run is a No-changes line and no write.
	g.noChanges(t)
	if err := g.s.Verify(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	oc, err = g.apply(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("the second apply: %v", err)
	}
	if oc.Changed {
		t.Errorf("the second apply = %+v, want no changes", oc)
	}
	if !strings.Contains(g.out.String(), "No changes: the workspace's own service account "+account) {
		t.Errorf("the second run does not say the account and its grant are there:\n%s", g.out.String())
	}
	if applied := g.ri.ran(t, "apply"); len(applied) != 1 {
		t.Errorf("the second run applied: %q", applied)
	}
}

// TestWorkspaceProjectStageNamesTheIAMAccountWhenThePlanCreatesOnlyIt: on an
// installation whose workspace already converges — the service account and its
// one grant are in the cloud — the ONLY change this stage's plan carries is
// creating the SA fallback's IAM account (WU3, D2803), the identity a member's
// owner gives roles/owner on their own project. The stage's own rule beside the
// confirmation (workspaceAccount) is that the sentence the owner types names
// the identity being created, so a sentence naming the service account alone
// would ask the owner to type their project's name for a change it never
// described — and would leave out the account that ends up holding authority
// over every linked member.
func TestWorkspaceProjectStageNamesTheIAMAccountWhenThePlanCreatesOnlyIt(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateOwnerRig(t, wsProjectName+"\n")
	g.ri.setPlanAt(t, "workspace", wsIAMAccountChange("create"))
	iamAccount := tf.WorkspaceIAMAccountID + "@" + wsProject + ".iam.gserviceaccount.com"

	st := g.check(t)
	if st.State != initflow.Todo {
		t.Fatalf("check = %v (%s), want a todo", st.State, st.Detail)
	}
	if !strings.Contains(st.Detail, iamAccount) {
		t.Errorf("the todo does not name the account the plan creates (%s): %s", iamAccount, st.Detail)
	}

	if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	oc, err := g.apply(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !oc.Changed {
		t.Errorf("apply = %+v, want the IAM account created", oc)
	}
	// The sentence the owner types is the CONFIRM line, read on its own: the
	// success line below names the same accounts, so a check over the whole
	// output would pass with the confirmation unchanged.
	var confirmation, created string
	for _, line := range strings.Split(g.out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "⚠ CONFIRM "):
			confirmation = line
		case strings.HasPrefix(line, "created "):
			created = line
		}
	}
	if confirmation == "" {
		t.Fatalf("the stage asked nothing: no CONFIRM line in the run's output:\n%s", g.out.String())
	}
	for _, want := range []string{workspaceAccount(wsProject), tf.WorkspaceDatabaseRole(wsProject), iamAccount} {
		if !strings.Contains(confirmation, want) {
			t.Errorf("the sentence the operator types does not name %q:\n%s", want, confirmation)
		}
	}
	if created == "" || !strings.Contains(created, iamAccount) {
		t.Errorf("the line the run prints after the apply does not name the account it created (%s): %q", iamAccount, created)
	}
	if !strings.Contains(oc.Detail, iamAccount) {
		t.Errorf("the apply's own detail does not name the account it created (%s): %s", iamAccount, oc.Detail)
	}
}

// TestWorkspaceApplyNamesTheRostersInvokeGrants: the sentence the owner types
// their project's name against describes what the run is going to do (WC2's
// review), and the invoke grants the root binds on the workspace's own service
// are part of that — one per member job account AND one per person of the
// workspace's roster. The clause is read from the same call the plan's own
// document was written from, so a confirmation that named one class and not the
// other would be a sentence about a run that is not this one.
func TestWorkspaceApplyNamesTheRostersInvokeGrants(t *testing.T) {
	fakeTerminal(t)
	db := gcpfake.NewRTDB(t)
	g := newWSCreateRig(t, wsProjectName+"\n", wsOwnerTable+wsOwnerCap+wsBudgetFor(db))
	db.Set("workspaceRosters/"+wsProjectName, wsRosterDoc(t))
	// The block a previous release's run deployed, so this run holds a service
	// to name in the sentence, and the URL its own outputs assign it, so the
	// second apply finds the audience already written back.
	wsSeedRecorded(t, wsProjectName, oldCredentialsRef("1.0.0"), wsServiceURL)
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))
	g.ri.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	g.ri.save(t)

	if _, err := g.apply(t, initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var confirmation string
	for _, line := range strings.Split(g.out.String(), "\n") {
		if strings.HasPrefix(line, "⚠ CONFIRM ") {
			confirmation = line
		}
	}
	if confirmation == "" {
		t.Fatalf("the stage asked nothing: no CONFIRM line in the run's output:\n%s", g.out.String())
	}
	for _, want := range []string{"one invoke grant on it per member job account", "one per person of the workspace's roster"} {
		if !strings.Contains(confirmation, want) {
			t.Errorf("the sentence the operator types does not name %q:\n%s", want, confirmation)
		}
	}
}

// TestWorkspaceProjectStageVerifyRefusesARootThatStillHasChanges: the apply's
// own re-read is a boundary and not a formality — a fresh plan that still finds
// work names the workdir the operator reads the plan in.
func TestWorkspaceProjectStageVerifyRefusesARootThatStillHasChanges(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateOwnerRig(t, wsProjectName+"\n")
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))
	if _, err := g.apply(t, initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	err := g.s.Verify(context.Background())
	if ExitCode(err) != ExitUserError {
		t.Fatalf("verify = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), g.root()) {
		t.Errorf("the refusal does not name where to read the plan (%s): %v", g.root(), err)
	}
}

// TestWorkspaceProjectStageConvergesACapWithoutAMember: a config the owner's
// own commands reach — `fugaro workspace cap set` writes the cap alone, and
// `workspace project rm` of the last member leaves the table empty — is this
// project's workspace, and the run converges it. W2 publishes nothing (a record
// that lists none admits nobody) and that is its stage's business; reading it
// as "no workspace" here would leave the account missing, and reading it as an
// error would stop the whole run.
func TestWorkspaceProjectStageConvergesACapWithoutAMember(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateRig(t, wsProjectName+"\n", wsOwnerCap)
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))

	st := g.check(t)
	if st.State != initflow.Todo {
		t.Fatalf("check = %v (%s), want the stage to converge a cap-only workspace", st.State, st.Detail)
	}
	oc, err := g.apply(t, initflow.Env{Interactive: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !oc.Changed {
		t.Errorf("apply = %+v, want the workspace's own account created", oc)
	}
}

// wsBudget is the budget block an owner's config carries once
// `fugaro update --firebase-project` has run in it: the two Firebase facts the
// workspace's own credentials service reads before it serves anything
// (workspaceServiceFacts). The tests that need the carry branches BEHIND that
// question — a resolve, an image this release's — are the ones that have to
// answer it first.
const wsBudget = "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: https://proj-1234-default-rtdb.firebaseio.com }\n"

// wsOtherImageSource is a config whose release images come from a host the
// installation's remote repository cannot mirror (baseSourceHost is the only one
// its upstream serves): the state a resolve is refused in, without a network.
const wsOtherImageSource = "image_source: example.com/acme\n"

// TestWorkspaceProjectStageCarriesTheRecordedBlockWhenItDeploysNothing is the
// security property serviceBlock's carry exists for, in the four states that
// deploy no service: the block the workdir already holds goes back exactly as it
// stands — its image and its audience — because var.credentials_service's
// default is null, and null plans the destruction of the running service with
// its bundle accessor and every member's invoke grant. The root's cover admits
// exactly one deletion (a member's grant, F25), so a run that deployed nothing
// would either hand the operator a plan that is refused or, where nothing gated
// it, take a working workspace's service away.
//
// Every other test that seeds a recorded block runs as the owner on a release,
// so it reaches the deploy branch or the not-stale one. These four are the only
// ones that reach carry, and each is reached with its own line:
//
//   - a coding agent's session (agentMarker),
//   - a development build (releaseVersion),
//   - a config that names no Firebase project and database
//     (workspaceServiceFacts),
//   - a release image this run cannot resolve through the installation's remote
//     repository.
//
// The mutation the assertions below are for is the one assignment inside carry:
// `s.svc = nil` writes `"credentials_service": null` into the tfvars of every
// state here, and no other test in this file fails on it.
func TestWorkspaceProjectStageCarriesTheRecordedBlockWhenItDeploysNothing(t *testing.T) {
	// The block a previous release's run deployed, stale on purpose: this
	// release's image is not what the workdir holds, so the resolve is worth
	// attempting (credentialsImageStale) and a run that reached it would move the
	// block — which is exactly what the carried states must not do.
	stale := oldCredentialsRef("1.0.0")
	// The block wsSeedRecorded wrote, field for field: what the workdir holds
	// before the run, and what every assertion below reads back out of it.
	recorded := workspaceServiceVar{
		Service:         creds.WorkspaceServiceName,
		Image:           stale,
		Region:          "us-east5",
		BundleSecret:    credsvc.WorkspaceBundleSecretID(wsProjectName),
		FirebaseProject: wsProject,
		RTDBURL:         "https://proj-1234-default-rtdb.firebaseio.com",
		URL:             wsServiceURL,
	}
	for _, c := range []struct {
		name string
		// version is what this run is: a released fugaro (releaseVersion), or a
		// build from a checkout, whose version is not a release number and whose
		// carry is the dev-build line.
		version string
		extra   string
		before  func(t *testing.T, g *wsCreateRig)
		reason  string
	}{
		{
			name:    "a coding agent's session",
			version: "1.9.9",
			extra:   wsOwnerTable + wsOwnerCap + wsBudget,
			before: func(t *testing.T, g *wsCreateRig) {
				t.Setenv(agentMarkers[0], "1")
			},
			reason: credentialsSessionHeld,
		},
		{
			name:    "a development build",
			version: "dev",
			extra:   wsOwnerTable + wsOwnerCap + wsBudget,
			reason:  credentialsDevBuildHeld,
		},
		{
			name:    "a config that names no Firebase project and database",
			version: "1.9.9",
			extra:   wsOwnerTable + wsOwnerCap,
			reason:  "budget.firebase_project",
		},
		{
			name:    "a resolve this run cannot make",
			version: "1.9.9",
			extra:   wsOwnerTable + wsOwnerCap + wsBudget + wsOtherImageSource,
			before: func(t *testing.T, g *wsCreateRig) {
				// The engine every door builds carries the images stage, and the
				// resolve reads the run's source through it (imagesStage.allow).
				g.e.images = newImagesStage(g.e)
			},
			reason: "is pinned through the installation's remote repository",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := Version
			Version = c.version
			t.Cleanup(func() { Version = old })
			g := newWSCreateRig(t, wsProjectName+"\n", c.extra)
			wsSeedRecorded(t, wsProjectName, stale, wsServiceURL)
			if c.before != nil {
				c.before(t, g)
			}
			// The plan the carry's own document makes: this run holds the block the
			// workdir carries, so the root it plans is converged and there is
			// nothing for an apply to do.
			g.noChanges(t)

			g.check(t)
			if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
				t.Fatalf("plan: %v", err)
			}

			raw, err := os.ReadFile(filepath.Join(g.root(), infra.VarsFile))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"credentials_service": null`) {
				t.Errorf("the workspace root's tfvars carry credentials_service: null after a run that deployed nothing, and null plans the destruction of the running service, its bundle accessor and every member's invoke grant:\n%s", raw)
			}
			v := g.tfvars(t)
			if v.Service == nil {
				t.Fatalf("a run that deploys nothing wrote no credentials_service at all: %s", raw)
			}
			// Field for field, because the block is a document a plan reads: the
			// image is which revision serves, the url is the audience a member's
			// run mints its ID token for, and the rest are the project, the
			// database and the bundle the running revision was deployed with.
			if got := *v.Service; got != recorded {
				t.Errorf("the carried credentials_service = %+v, want the block the workdir held %+v: a run that deploys nothing moves no revision (the image) and takes no audience away (the url)", got, recorded)
			}

			out := g.out.String()
			line := "the workspace's credentials service is not deployed: "
			var said []string
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, line) {
					said = append(said, l)
				}
			}
			if len(said) != 1 {
				t.Errorf("the run printed the not-deployed line %d time(s), want one: a run deploys nothing once, and that one line is what tells the owner which state held it\n%s", len(said), out)
			}
			if len(said) == 1 && !strings.Contains(said[0], c.reason) {
				t.Errorf("the not-deployed line does not carry this state's own reason (%q):\n%s", c.reason, said[0])
			}
			if applied := g.ri.ran(t, "apply"); len(applied) != 0 {
				t.Errorf("a run that deploys nothing applied the workspace root: %q", applied)
			}
		})
	}
}

// wsRosterPersonA and wsRosterPersonB are two people of the fixture roster: the
// addresses the root's `people` variable takes and binds one invoke grant per.
const (
	wsRosterPersonA = "ada@example.com"
	wsRosterPersonB = "grace@example.org"
)

// wsRosterMachine is the fixture roster's one machine: a member's job account,
// which the root's `members` variable already binds its own invoke grant to and
// which the `people` variable must never carry (a person is admitted by address
// and by nothing else).
const wsRosterMachine = "fugaro-run-borealis@member-borealis-1234.iam.gserviceaccount.com"

// wsBudgetFor is the budget block of an installation whose database is db: what
// the roster read needs, and the block the rig's own config carries none of.
func wsBudgetFor(db *gcpfake.RTDB) string {
	return "budget: { mode: observe, per_run_usd: 5, firebase_project: " + wsProject + ", rtdb_url: " + db.URL + " }\n"
}

// wsRosterDoc is a roster node as the workspace's own database carries it: two
// people and one machine, encoded by the package that owns the document's shape
// rather than written out here, because a node this reader refuses would test
// the carry path and not the read.
func wsRosterDoc(t *testing.T) map[string]any {
	t.Helper()
	roster := &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People: []workspace.Person{
			{Address: wsRosterPersonA, Kind: workspace.KindHuman},
			{Address: wsRosterMachine, Kind: workspace.KindSA},
			{Address: wsRosterPersonB, Kind: workspace.KindHuman},
		},
		Admins: []string{wsRosterPersonA},
	}
	b, err := workspace.EncodeRoster(roster)
	if err != nil {
		t.Fatalf("encoding the fixture roster: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// wsSeedRecordedPeople writes the workspace root's tfvars as a run of an earlier
// release left them, carrying the people that run's roster named: the list a run
// whose own read fails hands back verbatim.
func wsSeedRecordedPeople(t *testing.T, people []string) {
	t.Helper()
	if err := os.MkdirAll(wsWorkdirRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := workspaceVars{Project: wsProject, Workspace: wsProjectName, Members: map[string]workspaceMemberVar{}, People: people}
	doc.Names.WorkspaceAccountID = tf.WorkspaceServiceAccountID
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wsSeedTfvarsWithoutPeople writes the workspace root's tfvars as a release
// BEFORE var.people left them: a document with no `people` key at all, which is
// what every workspace whose tfvars predate WC2 holds on that first run.
func wsSeedTfvarsWithoutPeople(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(wsWorkdirRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"project":   wsProject,
		"workspace": wsProjectName,
		"names":     map[string]any{"workspace_account_id": tf.WorkspaceServiceAccountID},
		"members":   map[string]any{},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceVarsNameTheRostersPeople: var.people is the workspace roster's
// human addresses, read from the workspace's own roster node
// (`workspaceRosters/<name>`, R6, D2971) — so `fugaro update` renders the people
// the roster names and the root binds one invoke grant on the workspace's own
// service per address. A machine is never one of them: an `sa` entry of the
// roster is a member's job account, which the root's `members` variable already
// binds its own invoke grant to, and a person is admitted by address alone.
//
// The second case is the read that cannot be made, and it is the security
// property rather than an error: the people the workdir's own tfvars already
// hold go back VERBATIM, with one line saying why. An empty list there would
// plan the deletion of every person's invoke grant — a loosening of the
// workspace's own service that no run decided, and a deletion the root's cover
// does not admit — so the state that cannot read the roster keeps the list it
// already has.
func TestWorkspaceVarsNameTheRostersPeople(t *testing.T) {
	t.Run("the roster's own people and no machine", func(t *testing.T) {
		db := gcpfake.NewRTDB(t)
		g := newWSCreateRig(t, wsProjectName+"\n", wsOwnerTable+wsOwnerCap+wsBudgetFor(db))
		db.Set("workspaceRosters/"+wsProjectName, wsRosterDoc(t))
		g.noChanges(t)

		g.check(t)
		if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		want := []string{wsRosterPersonA, wsRosterPersonB}
		if got := g.tfvars(t).People; !slices.Equal(got, want) {
			t.Errorf("var.people = %v, want the roster's human addresses %v: the root binds one invoke grant per entry, and a machine or a person the roster does not name here is a grant nothing asked for", got, want)
		}
	})

	t.Run("a roster this run cannot read carries the recorded people", func(t *testing.T) {
		// Deliberately not the order a roster would carry: what the run writes
		// back is the document the workdir held, field for field, and not a list
		// this run derived again from something it could not read.
		recorded := []string{"zoe@example.com", wsRosterPersonA}
		g := newWSCreateOwnerRig(t, wsProjectName+"\n")
		wsSeedRecordedPeople(t, recorded)
		g.noChanges(t)

		g.check(t)
		if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := g.tfvars(t).People; !slices.Equal(got, recorded) {
			t.Errorf("var.people = %v, want the recorded list %v verbatim: a run that cannot read the roster must not empty it, because the empty list plans the deletion of every person's invoke grant", got, recorded)
		}
		out := g.out.String()
		if !strings.Contains(out, "the workspace's roster is not read: ") {
			t.Errorf("a run that could not read the roster said nothing about it, so an owner cannot tell a workspace with no people from a read that failed:\n%s", out)
		}
	})

	t.Run("a read made before the workdir exists does not empty the document", func(t *testing.T) {
		// Check answers Todo with a sentence even where its own read never
		// reached a plan, and it reads the roster for that sentence with no
		// workdir set — no recorded list to carry and no document in front of
		// it. That answer decides nothing and must not be kept: the call that
		// decides what the tfvars carry is prepare's own, made with the workdir
		// set, and an empty list kept from before it writes `people: []` over
		// the recorded list — the plan that deletes every person's invoke
		// grant. The first read below is made to fail before the workdir is
		// prepared at all (no state directory can be named), which is exactly
		// the state Check's own sentence comes from and the only one in which
		// the recorded document is still on disk for the next prepare.
		recorded := []string{"zoe@example.com", wsRosterPersonA}
		g := newWSCreateOwnerRig(t, wsProjectName+"\n")
		wsSeedRecordedPeople(t, recorded)
		g.noChanges(t)
		state := os.Getenv("XDG_STATE_HOME")
		home := os.Getenv("HOME")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")

		if st := g.check(t); st.State != initflow.Todo {
			t.Fatalf("check = %v, want a todo: a read that never reached a plan has not found the stage done", st.State)
		}
		if g.s.wd != nil {
			t.Fatal("the workdir was set by a prepare that failed before it: this case is about the read made before the workdir exists")
		}
		t.Setenv("XDG_STATE_HOME", state)
		t.Setenv("HOME", home)

		if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := g.tfvars(t).People; !slices.Equal(got, recorded) {
			t.Errorf("var.people = %v, want the recorded list %v: a read made before the workdir existed must not decide what the document carries, because the empty list plans the deletion of every person's invoke grant", got, recorded)
		}
	})

	t.Run("a roster this run cannot read and no recorded people carries the empty list", func(t *testing.T) {
		// The first run of this variable over a workspace whose tfvars predate
		// it: the read fails and there is no list to carry. The document must
		// still hold `[]` and NOT `null`, because the root's own variable walks
		// the list — a null is refused at plan time with "iteration over null
		// value" rather than carrying nothing, which is the one thing carrying
		// here is for — and because the empty list is the truth of a roster
		// nobody could read.
		g := newWSCreateOwnerRig(t, wsProjectName+"\n")
		wsSeedTfvarsWithoutPeople(t)
		g.noChanges(t)

		g.check(t)
		if _, err := g.plan(t, initflow.Env{Interactive: true}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(g.root(), infra.VarsFile))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		people, ok := doc["people"].([]any)
		if !ok || len(people) != 0 {
			t.Errorf("the written document carries people = %v, want the empty list []: the root's own validation walks the list, so a null here fails the workspace root's plan on the value's shape instead of converging the workspace", doc["people"])
		}
		out := g.out.String()
		if !strings.Contains(out, "the workspace's roster is not read: ") {
			t.Errorf("a run that could not read the roster said nothing about it, so an owner cannot tell a workspace with no people from a read that failed:\n%s", out)
		}
	})
}

// TestWorkspaceProjectStageNeverConfirmsByTheRunsOwnAnswer: what the stage
// applies is an IAM GRANT, and the run's own answer covers no grant (spec §6) —
// the typed word is the owner's. A coding agent's session is refused for the
// same reason: the confirmation cannot be given there at all.
func TestWorkspaceProjectStageNeverConfirmsByTheRunsOwnAnswer(t *testing.T) {
	for _, c := range []struct {
		name  string
		agent string
	}{
		{"the run's own answer", ""},
		{"a coding agent", agentMarkers[0]},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeTerminal(t)
			g := newWSCreateOwnerRig(t, wsProjectName+"\n")
			if c.agent != "" {
				t.Setenv(c.agent, "1")
			} else {
				g.e.r.o.yes = true
			}
			g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))

			_, err := g.apply(t, initflow.Env{Interactive: true})
			var needs *initflow.NeedsYouError
			if !asNeedsYou(err, &needs) {
				t.Fatalf("apply = %v, want a step left for the operator", err)
			}
			if needs.Left.Stage != initflow.WorkspaceProject || !strings.Contains(needs.Left.Text, "type the project's name") {
				t.Errorf("the step left for the operator is not this stage's own typed word: %+v", needs.Left)
			}
			// The door is the workspace's own project's config, so the command
			// is the one the record stage names: `fugaro update` in it (W2).
			if cmd := strings.Join(needs.Left.Commands, " "); cmd != "fugaro update" {
				t.Errorf("the command left for the operator is %q, want the door that converges this project", cmd)
			}
			if applied := g.ri.ran(t, "apply"); len(applied) != 0 {
				t.Errorf("%s applied the workspace root: %q", c.name, applied)
			}
		})
	}
}

// TestWorkspaceProjectStageAtTheApplyDoorNamesTheDoorThatCanConfirmIt: `fugaro
// apply` runs with no terminal of its own by construction, so this stage's
// question is never reached there — the run ends leaving the step. What it
// leaves has to name the command that reaches the question, and apply's own
// command is not one: it asks apply to run apply again, and the second run says
// what the first said (the no-prompt rule reaches every refusal this way). The
// door that asks is `fugaro update`, in the owner's own terminal.
func TestWorkspaceProjectStageAtTheApplyDoorNamesTheDoorThatCanConfirmIt(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateOwnerRig(t, wsProjectName+"\n")
	// The apply door's own run, opened the way `fugaro apply` opens it
	// (applyRunSteps): the door, the run's own answer, and no terminal.
	g.e.r.o.door, g.e.r.o.yes, g.e.r.o.nonInteractive = applyName, true, true
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))

	_, err := g.apply(t, initflow.Env{Interactive: false})
	var needs *initflow.NeedsYouError
	if !asNeedsYou(err, &needs) {
		t.Fatalf("apply = %v, want the step left for the operator", err)
	}
	if needs.Left.Stage != initflow.WorkspaceProject || needs.Left.Kind != initflow.LeftCommand {
		t.Errorf("the step left for the operator is not this stage's own command: %+v", needs.Left)
	}
	if cmd := strings.Join(needs.Left.Commands, " "); cmd != "fugaro update" {
		t.Errorf("the command left for the operator is %q, want the door that can ask this step's question", cmd)
	}
	if applied := g.ri.ran(t, "apply"); len(applied) != 0 {
		t.Errorf("the apply door applied the workspace root: %q", applied)
	}
}

// TestWorkspaceProjectStageRefusesAPlanThatIsNotTheAccountAndItsOneGrant: the
// root's whole cover (tf.WorkspaceNotCovered). A plan carrying a job — the
// thing a workspace never owns (spec §9) — is refused by name, and nothing is
// applied: the workspace's own project holds the account and its one grant, and
// an installation's converge is what would put anything else there.
func TestWorkspaceProjectStageRefusesAPlanThatIsNotTheAccountAndItsOneGrant(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateOwnerRig(t, wsProjectName+"\n")
	job := planChange{
		"address": "google_cloud_run_v2_job.workspace", "mode": "managed", "type": "google_cloud_run_v2_job",
		"change": map[string]any{"actions": []string{"create"}, "after": map[string]any{}},
	}
	g.ri.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"), job)

	_, err := g.plan(t, initflow.Env{Interactive: true})
	if err == nil {
		t.Fatal("a plan carrying a job was not refused")
	}
	if !strings.Contains(err.Error(), "google_cloud_run_v2_job.workspace") || !strings.Contains(err.Error(), "spec §9") {
		t.Errorf("the refusal does not name the resource and the rule: %v", err)
	}
	if _, err := g.apply(t, initflow.Env{Interactive: true}); err == nil {
		t.Fatal("apply went ahead on a plan that is not the root's")
	}
	if applied := g.ri.ran(t, "apply"); len(applied) != 0 {
		t.Errorf("a refused plan was applied: %q", applied)
	}
}

// TestSplitFirebaseGetsNoService is R12's own half: a workspace whose Firebase
// is another project's gets NO credentials service — the stage skips before it
// reads a plan, composes a block or opens the root's workdir, so no revision is
// ever deployed here — and the line the owner reads says why, naming the service
// among the things the split leaves alone.
//
// The run in flight is the other half of the same fact, and the half a later
// update reads: the note names the workspace's service step only where a revision
// of it is really being replaced (workspaceServiceRevisionStale), and a split
// workspace holds none however stale the document in its own workdir is. So an
// owner whose workspace deploys no service reads no such step — and the seed is
// what makes the assertion mean that, rather than the workdir being empty.
func TestSplitFirebaseGetsNoService(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	currentRelease(t)
	splitWorkspaceOwnerConfig(t)
	r.db.Set(projectpolicy.PathWorkspace(initProjectName), map[string]any{
		"version": projectpolicy.WorkspaceVersion,
		"name":    initProjectName,
		"members": []any{wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b")},
		"cap":     map[string]any{"monthly_micros": 25_000_000},
	})
	wsSeedRecorded(t, initProjectName, oldCredentialsRef(convergePrev), "")

	startRunInFlight(t, r)
	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	if line := inFlightLine(t, out); strings.Contains(line, "workspace service:") {
		t.Errorf("the note names the workspace's service over an installation that deploys none, so an owner reads a step no run makes (R12):\n%s", line)
	}
	// The reason travels the pair the skip always does: the first clause on the
	// screen, the whole sentence in the log (report.Brief).
	whole := out + newestLog(t, "update")
	for _, want := range []string{"its Firebase is another project's", wsSplitFirebaseProject,
		"no credentials service is deployed", "docs/workspaces/spec.md §3"} {
		if !strings.Contains(whole, want) {
			t.Errorf("the split's line does not carry %q, so nothing tells the owner why the workspace's own project holds no service:\n%s", want, whole)
		}
	}
	for _, root := range r.applies(t) {
		if root == "workspace" {
			t.Errorf("the split workspace applied the workspace root: %v", r.applies(t))
		}
	}
	b, err := os.ReadFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), oldCredentialsRef(convergePrev)) {
		t.Errorf("the workspace's own document was rewritten by a stage that deploys nothing here:\n%s", b)
	}
}
