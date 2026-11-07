package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// update_workspace_test.go is the workspace record's publish and its migration
// in place (update_workspace.go): the stage the workspace owner's `fugaro
// update` runs, driven here directly. `fugaro update`'s own test is
// update_test.go's; what this file is about is what that run does — and never
// does — to the node at workspaces/<name>.
//
// Every test is hermetic: the gcpfake RTDB of each fixture installation, the
// fixture's own config home, and no cloud.

// wsOwnerTable is the workspace's own config declaring one member, in the shape
// `fugaro workspace member add` writes. The repositories are written OUT of
// order on purpose: the record that is published is sorted where it is built
// (internal/localcfg's own note beside this table), so the order of the file is
// not the order of the node.
const wsOwnerTable = `workspace_members:
  member-b:
    job_service_accounts:
      - job-account-b@member-b-gcp.iam.gserviceaccount.com
    firebase_project: member-b-fb
    fp: member-b
    exchange_url: https://exchange.example.com/v1
    repos:
      - owner/repo-b
      - owner/repo-a
`

// wsOwnerCap is the workspace's own cap, in dollars as the file writes it.
const wsOwnerCap = "workspace_cap:\n  monthly: 25\n"

// wsPin is a member's own half: the membership key and the pin beside it (W1).
// It is the config on this machine that names the workspace, and it carries no
// part of the owner's side.
const wsPin = `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`

// wsUpdateProject is one installation in the fixture's config home: its own
// project config and its own database.
type wsUpdateProject struct {
	name string
	path string
	db   *gcpfake.RTDB
}

// wsUpdateHome is a config home with installations in it: the workspace's own
// project (whose config carries the owner's half, and whose own database is
// what the node is published into) and whatever other project configs a test
// adds. A member's config here is what the two-sided membership check reads.
type wsUpdateHome struct {
	dir      string
	fs       *gcpfake.Firestore
	projects map[string]*wsUpdateProject
}

// newWSUpdateHome is the home with the workspace's own project in it and
// selected. ownerExtra is what its config declares on top of the installation:
// the member table, the cap, or nothing at all (a project that declares no
// workspace).
func newWSUpdateHome(t *testing.T, ownerExtra string) *wsUpdateHome {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	isolateProjects(t, dir)
	h := &wsUpdateHome{dir: dir, fs: gcpfake.NewFirestore(t), projects: map[string]*wsUpdateProject{}}
	h.add(t, "workspace-alpha", "ws-gcp", ownerExtra)
	h.selectProject(t, "workspace-alpha")
	return h
}

// add writes one installation's config and starts its database, marked as its
// own the way a real installation's is (infra.ProjectMarker).
func (h *wsUpdateHome) add(t *testing.T, name, gcp, extra string) *wsUpdateProject {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "version: 1\n" +
		"name: " + name + "\ngcp_project: " + gcp + "\nregion: us-east5\nruns_bucket: fugaro-runs-" + gcp + "\n" +
		"bucket_url: file://" + filepath.Join(h.dir, "runs-"+name) + "\n" +
		"user: person-1@example.com\nmax_parallel: 2\n" +
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: " + gcp + ", rtdb_url: " + db.URL + " }\n" +
		"endpoints: { firestore: " + h.fs.URL + ", no_auth: true }\n" +
		"repos:\n  owner/repo-a: {}\n" +
		extra
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &wsUpdateProject{name: name, path: path, db: db}
	db.Set(budget.PathMark, installMark(name, gcp))
	h.projects[name] = p
	return p
}

// selectProject is the config the run is for: what FUGARO_CONFIG names, which
// is the file `fugaro update` reads and the installation its stages converge.
func (h *wsUpdateHome) selectProject(t *testing.T, name string) {
	t.Helper()
	t.Setenv("FUGARO_CONFIG", h.project(t, name).path)
}

func (h *wsUpdateHome) project(t *testing.T, name string) *wsUpdateProject {
	t.Helper()
	p, ok := h.projects[name]
	if !ok {
		t.Fatalf("the fixture holds no installation %s", name)
	}
	return p
}

// wsUpdateRig is the stage, its engine and the command a run's options were
// taken from (migrationRig's own shape, for the stage that is not the policy's).
type wsUpdateRig struct {
	h   *wsUpdateHome
	s   *workspaceStage
	cmd *cobra.Command
	out *syncBuf
}

func newWSUpdateRig(t *testing.T, h *wsUpdateHome) *wsUpdateRig {
	t.Helper()
	// The one command every sentence of this stage names is the program as the
	// user ran it (selfCommand), which under `go test` is the test binary's
	// path: pinned to the bare name here, so the test reads the sentence an
	// owner reads.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	run := &initRun{o: &initOptions{door: updateDoor.name}, w: out, in: bufio.NewReader(strings.NewReader("")), cmd: cmd}
	run.setProject(lc)
	return &wsUpdateRig{h: h, s: newWorkspaceStage(&initEngine{r: run, lc: lc, path: os.Getenv("FUGARO_CONFIG")}), cmd: cmd, out: out}
}

func (r *wsUpdateRig) check(t *testing.T) initflow.Status {
	t.Helper()
	st, err := r.s.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return st
}

// planText is the plan as the stage printed it, with the terminal a person's
// run has (the stage shows its diff there and nowhere else).
func (r *wsUpdateRig) planText(t *testing.T) string {
	t.Helper()
	if _, err := r.s.Plan(context.Background(), initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return r.out.String()
}

func (r *wsUpdateRig) apply(t *testing.T, yes bool) (initflow.Outcome, string, error) {
	t.Helper()
	oc, err := r.s.Apply(context.Background(), initflow.Env{Yes: yes, Interactive: !yes})
	return oc, r.out.String(), err
}

// wsNode is the node at workspaces/<name> as the database holds it.
func wsNode(t *testing.T, p *wsUpdateProject, name string) map[string]any {
	t.Helper()
	v := p.db.Value(projectpolicy.PathWorkspace(name))
	n, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T (%v), not an object", projectpolicy.PathWorkspace(name), v, v)
	}
	return n
}

// wsNumber is one number of the node read back from the fake, which answers
// json.Number for what a write stored and the Go value for what a test seeded.
func wsNumber(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			t.Fatalf("the node's number is not an integer: %v", err)
		}
		return i
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		t.Fatalf("the node's value is %T (%v), not a number", v, v)
		return 0
	}
}

// wsMembers is the member list the node carries, as `project` names in order.
func wsMembers(t *testing.T, p *wsUpdateProject, name string) []string {
	t.Helper()
	raw, ok := wsNode(t, p, name)["members"].([]any)
	if !ok {
		t.Fatalf("the node at %s carries no members list: %v", projectpolicy.PathWorkspace(name), wsNode(t, p, name))
	}
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		entry, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("a member entry is %T (%v), not an object", m, m)
		}
		project, _ := entry["project"].(string)
		out = append(out, project)
	}
	return out
}

// TestUpdatePublishesTheWorkspaceNodeAndMigratesAnOlderOneInPlace is the red
// step: the stage did not exist. It is the test the task exists for -- the
// workspace's record is published by the one command the owner runs, and an
// installation that already holds a node must converge rather than be asked to
// publish again.
func TestUpdatePublishesTheWorkspaceNodeAndMigratesAnOlderOneInPlace(t *testing.T) {
	t.Run("the first publish", func(t *testing.T) {
		h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
		r := newWSUpdateRig(t, h)
		oc, out, err := r.apply(t, true)
		if err != nil {
			t.Fatalf("the owner's run published nothing: %v\n%s", err, out)
		}
		if !oc.Changed {
			t.Errorf("the first publish reports no change: %s", oc.Detail)
		}
		n := wsNode(t, h.project(t, "workspace-alpha"), "workspace-alpha")
		if got := wsNumber(t, n["version"]); got != int64(projectpolicy.WorkspaceVersion) {
			t.Errorf("the published node's version is %v, want %d", n["version"], projectpolicy.WorkspaceVersion)
		}
		if n["name"] != "workspace-alpha" {
			t.Errorf("the published node names %v, want workspace-alpha", n["name"])
		}
		if got := wsMembers(t, h.project(t, "workspace-alpha"), "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
			t.Errorf("the published members are %v, want [member-b]", got)
		}
		// The cap is integer micro-dollars from the record onward (W0): the
		// node carries the micro-dollars, not the file's dollars.
		cap, ok := n["cap"].(map[string]any)
		if !ok {
			t.Fatalf("the published node carries no cap block: %v", n["cap"])
		}
		if got := wsNumber(t, cap["monthly_micros"]); got != 25_000_000 {
			t.Errorf("the published cap is %v micro-dollars, want 25000000 ($25)", cap["monthly_micros"])
		}
		if _, there := cap["monthly"]; there {
			t.Error("the published cap carries the file's dollar amount as well as the micro-dollars: the conversion happens once, on the way in")
		}
		// SEC: what the node must never carry. The record names the members and
		// carries the cap, and every other value is the member's own pin or the
		// owner's own table (spec §2, §5): a node that carried one of them could
		// aim a member's exchange or a member's grant at a party of its
		// choosing.
		for _, k := range []string{
			"workspace_members", "workspace_cap", "workspace_pin", "job_service_accounts",
			"firebase_project", "fp", "exchange_url", "issuer", "assertion_key", "service_account",
		} {
			if _, there := n[k]; there {
				t.Errorf("the published node carries %q, which is the owner's own table or a member's pin and not the workspace's record", k)
			}
		}
		if len(h.project(t, "workspace-alpha").db.Credentials()) == 0 {
			t.Error("the run reached no database at all")
		}
	})

	t.Run("an older node is migrated in place", func(t *testing.T) {
		h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
		p := h.project(t, "workspace-alpha")
		// The node a release before this one describes: the record's own file
		// shape (schemas/workspace.schema.json), with the cap in dollars and no
		// version key. Asking the owner to publish again would be a second
		// command, and the run that reads the node would be the one that
		// stopped.
		p.db.Set(projectpolicy.PathWorkspace("workspace-alpha"), map[string]any{
			"name": "workspace-alpha",
			"members": []any{
				map[string]any{"project": "member-b", "repos": []any{"owner/repo-a", "owner/repo-b"}},
			},
			"cap": map[string]any{"monthly": 25},
		})
		r := newWSUpdateRig(t, h)
		oc, out, err := r.apply(t, true)
		if err != nil {
			t.Fatalf("the older node was not migrated: %v\n%s", err, out)
		}
		if !oc.Changed {
			t.Errorf("the migration reports no change: %s", oc.Detail)
		}
		n := wsNode(t, p, "workspace-alpha")
		if got := wsNumber(t, n["version"]); got != int64(projectpolicy.WorkspaceVersion) {
			t.Errorf("the migrated node's version is %v, want %d", n["version"], projectpolicy.WorkspaceVersion)
		}
		cap, _ := n["cap"].(map[string]any)
		if got := wsNumber(t, cap["monthly_micros"]); got != 25_000_000 {
			t.Errorf("the migrated cap is %v micro-dollars, want 25000000 ($25)", cap["monthly_micros"])
		}
		if !strings.Contains(out, "version 0 to 1") {
			t.Errorf("the run does not name the migration it made, so the owner cannot tell it from a plain publish:\n%s", out)
		}
	})
}

// TestUpdateRefusesToPublishAWorkspaceWithNoMembers: a member is in the
// workspace only when the table lists it, so a record that lists none is a
// workspace that admits nobody, and it is refused here rather than published
// (W0's own note: this is the state W2's publish refuses).
func TestUpdateRefusesToPublishAWorkspaceWithNoMembers(t *testing.T) {
	h := newWSUpdateHome(t, "workspace_members: {}\n"+wsOwnerCap)
	r := newWSUpdateRig(t, h)
	for _, yes := range []bool{true, false} {
		_, out, err := r.apply(t, yes)
		if err == nil {
			t.Fatalf("a workspace with no members was published (yes=%v):\n%s", yes, out)
		}
		if !strings.Contains(err.Error(), "no member") {
			t.Errorf("the refusal is %q, and it does not say that the workspace declares no member", err)
		}
	}
	if h.project(t, "workspace-alpha").db.Value(projectpolicy.PathWorkspaces()) != nil {
		t.Error("a refused publish wrote into the database")
	}
}

// TestUpdateNamesTheOneCommandAndNoOther: every sentence this stage can print
// names `fugaro update` and no second command. The reconciliation's D1292
// refused a `fugaro workspace publish` on the HARD RULE's ground -- the whole
// upgrade is the one command -- so a stage of update that handed the owner a
// command of its own would be that refusal undone in a sentence.
func TestUpdateNamesTheOneCommandAndNoOther(t *testing.T) {
	var said []string
	collect := func(what string, err error, text string) {
		t.Helper()
		if text != "" {
			said = append(said, text)
		}
		if err != nil {
			said = append(said, err.Error())
		}
		_ = what
	}

	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	r := newWSUpdateRig(t, h)
	collect("check", nil, r.check(t).Detail)
	collect("plan", nil, r.planText(t))
	collect("left", nil, r.s.Left().Text)
	_, out, err := r.apply(t, true)
	collect("apply", err, out)

	// A workspace with no members: the refusal.
	h2 := newWSUpdateHome(t, "workspace_members: {}\n"+wsOwnerCap)
	_, _, err = newWSUpdateRig(t, h2).apply(t, true)
	collect("no members", err, "")

	// A project that declares no workspace: the skip.
	h3 := newWSUpdateHome(t, "")
	r3 := newWSUpdateRig(t, h3)
	collect("no workspace", nil, r3.check(t).Detail)

	joined := strings.Join(said, "\n")
	if !strings.Contains(joined, "fugaro update") {
		t.Errorf("nothing the stage says names fugaro update, the one command the owner runs:\n%s", joined)
	}
	if strings.Contains(joined, "workspace publish") {
		t.Errorf("the stage names a second command; fugaro update is the whole upgrade, and a workspace publish is the step the one command must not need:\n%s", joined)
	}
}

// TestUpdateLeavesAMemberWithNoWorkspaceAlone: a config that declares no
// workspace is not a workspace's own, and the stage neither reads nor writes
// the database for it.
func TestUpdateLeavesAMemberWithNoWorkspaceAlone(t *testing.T) {
	h := newWSUpdateHome(t, "")
	r := newWSUpdateRig(t, h)
	if st := r.check(t); st.State != initflow.Skipped {
		t.Errorf("check = %v (%s), want skipped: this project's config declares no workspace", st.State, st.Detail)
	}
	oc, _, err := r.apply(t, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if oc.Changed {
		t.Errorf("a project that declares no workspace reports a change: %s", oc.Detail)
	}
	if n := len(h.project(t, "workspace-alpha").db.Credentials()); n != 0 {
		t.Errorf("the stage made %d requests to the database for a config that declares no workspace", n)
	}
}

// TestUpdateOnAMemberNeverWritesTheWorkspaceNode: a member holds no grant on
// the workspace's project at all (spec §5), so its `fugaro update` publishes
// nothing there and reads nothing there. A stage that reached for it is the
// defect this test names.
func TestUpdateOnAMemberNeverWritesTheWorkspaceNode(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	h.add(t, "aurora", "aurora-gcp", wsPin)
	h.selectProject(t, "aurora")
	r := newWSUpdateRig(t, h)
	if st := r.check(t); st.State != initflow.Skipped {
		t.Errorf("check on a member = %v (%s), want skipped: a member's run converges the member's own installation and nothing of the workspace's", st.State, st.Detail)
	}
	oc, _, err := r.apply(t, true)
	if err != nil {
		t.Fatalf("apply on a member: %v", err)
	}
	if oc.Changed {
		t.Errorf("a member's run reports a change: %s", oc.Detail)
	}
	if v := h.project(t, "workspace-alpha").db.Value(projectpolicy.PathWorkspaces()); v != nil {
		t.Errorf("a member's run wrote the workspace's own node: %v", v)
	}
	if n := len(h.project(t, "aurora").db.Credentials()); n != 0 {
		t.Errorf("a member's run made %d requests to its own database for a record it does not publish", n)
	}
}

// TestMembershipNeedsBothSides: a member is in the workspace when the owner's
// table lists it AND the member's own config names the workspace. Either alone
// is a half-done membership, and both are reported rather than one of them
// silently winning.
func TestMembershipNeedsBothSides(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	h.add(t, "member-b", "member-b-gcp", "")
	h.add(t, "member-c", "member-c-gcp", wsPin)
	r := newWSUpdateRig(t, h)
	out := r.planText(t)
	// The two sentences are the ones `fugaro workspace show` prints for the same
	// state (workspaceRow.problems, W1), verbatim: this stage reports the
	// half-done membership through W1's own function and words no second copy of
	// it, so the two reads of one state cannot come to describe it differently.
	for _, want := range []string{
		"the workspace's table here declares member member-b, and member-b's own config here names no workspace: the membership is half-done until that config names workspace-alpha",
		"member-c's own config here names workspace workspace-alpha, and the workspace's table here declares no member member-c: the membership is half-done until the workspace declares it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not report the half-done membership %q:\n%s", want, out)
		}
	}
	if _, _, err := r.apply(t, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Neither half wins: the node this run publishes lists the table's member
	// and not the config that named the workspace.
	if got := wsMembers(t, h.project(t, "workspace-alpha"), "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
		t.Errorf("the published members are %v, want [member-b]: member-c names the workspace here and no table declares it, and a member is in the workspace only when both halves say so", got)
	}
}
