package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
// (wsOwnerTable and wsOwnerCap, the shape `fugaro workspace member add` and
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
	cfg := strings.Replace(string(b), "name: aurora", "name: "+wsProjectName, 1)
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
	canConfirmTyped = initflow.CanConfirm
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

// TestWorkspaceProjectStageRefusesAWorkspaceWhoseFirebaseIsAnotherProject: the
// workspace's own project holds the workspace's Firebase, its database and its
// service (spec §3), so an installation whose budget.firebase_project names
// another project is one where this root's one grant would bind on a database
// the workspace does not use — the record W2's publish stage writes and the
// workspace's own service reads lives in the Firebase project's — and where the
// plan check would accept it, because the stage holds the plan to the same
// wrong project. All three paths refuse it before anything is planned, with the
// two projects named and the remedy a split installation takes, and terraform
// is never reached: no workdir, no init, no plan.
func TestWorkspaceProjectStageRefusesAWorkspaceWhoseFirebaseIsAnotherProject(t *testing.T) {
	fakeTerminal(t)
	g := newWSCreateRig(t, wsProjectName+"\n", wsOwnerTable+wsOwnerCap+wsSplitFirebase)
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"check", func() error { _, err := g.s.Check(context.Background()); return err }},
		{"plan", func() error { _, err := g.s.Plan(context.Background(), initflow.Env{Interactive: true}); return err }},
		{"apply", func() error { _, err := g.s.Apply(context.Background(), initflow.Env{Interactive: true}); return err }},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("%s = %v, want a refusal the operator reads", c.name, err)
			}
			for _, want := range []string{
				"budget.firebase_project", "proj-9999", "docs/workspaces/spec.md §3",
				wsProject, wsProjectName, "fugaro update --firebase-project " + wsProject,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
	if calls := g.ri.calls(t); len(calls) != 0 {
		t.Errorf("the stage reached terraform for a workspace whose Firebase is another project's: %q", calls)
	}
	if _, err := os.Stat(g.root()); !os.IsNotExist(err) {
		t.Errorf("the workspace workdir was prepared: %v", err)
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
// `workspace member rm` of the last member leaves the table empty — is this
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

// TestWorkspaceProjectStageNeverConfirmsByYes: what the stage applies is an IAM
// GRANT, and no run's one --yes covers a grant (spec §6). A coding agent's
// session is refused for the same reason: the confirmation cannot be given
// there at all.
func TestWorkspaceProjectStageNeverConfirmsByYes(t *testing.T) {
	for _, c := range []struct {
		name  string
		agent string
	}{
		{"--yes", ""},
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
