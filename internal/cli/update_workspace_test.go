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

// wsOwnerTwoMembers is the same table with a project the published node does
// not carry yet: the write that admits a member, and so the one this stage
// reserves for the owner's own typed word.
const wsOwnerTwoMembers = wsOwnerTable + `  member-d:
    job_service_accounts:
      - job-account-d@member-d-gcp.iam.gserviceaccount.com
    firebase_project: member-d-fb
    fp: member-d
    exchange_url: https://exchange.example.com/v1
    repos:
      - owner/repo-d
`

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
func wsNode(t *testing.T, db *gcpfake.RTDB, name string) map[string]any {
	t.Helper()
	v := db.Value(projectpolicy.PathWorkspace(name))
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

// wsCeiling is the workspace's own ceiling as the node carries it, 0 when the
// node states none.
func wsCeiling(t *testing.T, db *gcpfake.RTDB, name string) int64 {
	t.Helper()
	cap, ok := wsNode(t, db, name)["cap"].(map[string]any)
	if !ok {
		return 0
	}
	return wsNumber(t, cap["monthly_micros"])
}

// wsPublishedNode seeds a node at the workspace's own path, as a release before
// this one wrote it: version 1 with the members and the cap the caller names.
// It is how a test says what is already published, which is the other half of
// every diff this stage makes.
func wsPublishedNode(t *testing.T, p *wsUpdateProject, name string, members []any, capMicros int64) {
	t.Helper()
	node := map[string]any{
		"version": projectpolicy.WorkspaceVersion,
		"name":    name,
		"members": members,
	}
	if capMicros > 0 {
		node["cap"] = map[string]any{"monthly_micros": capMicros}
	}
	p.db.Set(projectpolicy.PathWorkspace(name), node)
}

// wsPublishedMember is one member entry of a node a test seeded.
func wsPublishedMember(project string, repos ...string) map[string]any {
	out := make([]any, 0, len(repos))
	for _, r := range repos {
		out = append(out, r)
	}
	return map[string]any{"project": project, "repos": out}
}

// wsRepos is one member's repositories as the node carries them.
func wsRepos(t *testing.T, db *gcpfake.RTDB, name, project string) []string {
	t.Helper()
	raw, ok := wsNode(t, db, name)["members"].([]any)
	if !ok {
		t.Fatalf("the node at %s carries no members list", projectpolicy.PathWorkspace(name))
	}
	for _, m := range raw {
		entry, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("a member entry is %T (%v), not an object", m, m)
		}
		if entry["project"] != project {
			continue
		}
		repos, ok := entry["repos"].([]any)
		if !ok {
			t.Fatalf("%s's repos are %T (%v), not a list", project, entry["repos"], entry["repos"])
		}
		out := make([]string, 0, len(repos))
		for _, r := range repos {
			s, _ := r.(string)
			out = append(out, s)
		}
		return out
	}
	t.Fatalf("the node at %s lists no member %s: %v", projectpolicy.PathWorkspace(name), project, raw)
	return nil
}

// wsMembers is the member list the node carries, as `project` names in order.
func wsMembers(t *testing.T, db *gcpfake.RTDB, name string) []string {
	t.Helper()
	raw, ok := wsNode(t, db, name)["members"].([]any)
	if !ok {
		t.Fatalf("the node at %s carries no members list: %v", projectpolicy.PathWorkspace(name), wsNode(t, db, name))
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
		// The group's first record takes the owner's typed word rather than
		// --yes (the whole membership is chosen where none was): the run here is
		// the owner's own, at a terminal, typing the workspace's name.
		fakeTerminal(t)
		r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
		oc, out, err := r.apply(t, false)
		if err != nil {
			t.Fatalf("the owner's run published nothing: %v\n%s", err, out)
		}
		if !oc.Changed {
			t.Errorf("the first publish reports no change: %s", oc.Detail)
		}
		n := wsNode(t, h.project(t, "workspace-alpha").db, "workspace-alpha")
		if got := wsNumber(t, n["version"]); got != int64(projectpolicy.WorkspaceVersion) {
			t.Errorf("the published node's version is %v, want %d", n["version"], projectpolicy.WorkspaceVersion)
		}
		if n["name"] != "workspace-alpha" {
			t.Errorf("the published node names %v, want workspace-alpha", n["name"])
		}
		if got := wsMembers(t, h.project(t, "workspace-alpha").db, "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
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
		n := wsNode(t, p.db, "workspace-alpha")
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
	// The same write two ways: refused by --yes (the group's first record takes
	// the typed name, so this is the refusal sentence), then published by the
	// owner's own word at a terminal. Both are sentences of this stage, and both
	// have to name the one command.
	_, out, err := r.apply(t, true)
	collect("apply refused", err, out)
	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
	_, out, err = r.apply(t, false)
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
	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
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
	if _, _, err := r.apply(t, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Neither half wins: the node this run publishes lists the table's member
	// and not the config that named the workspace.
	if got := wsMembers(t, h.project(t, "workspace-alpha").db, "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
		t.Errorf("the published members are %v, want [member-b]: member-c names the workspace here and no table declares it, and a member is in the workspace only when both halves say so", got)
	}
}

// TestUpdateDiffSaysWhatItRemoves: the write REPLACES the node, so what the
// record no longer declares is removed from the published document — a member,
// a member's repository, the workspace's own ceiling. The operator confirms a
// diff, and a diff that read "nothing is removed" while dropping a member would
// be the one silent change this stage must not make: every run of every member
// project reads the record this write replaces.
//
// A removal is not a widening and it does not take the typed name: the owner
// asked for it by editing their own table, and what the diff owes them is the
// statement of it. The second half is the other side of the same coin: a write
// that removes nothing says so, rather than leaving the reader to work it out
// against the node above.
func TestUpdateDiffSaysWhatItRemoves(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	p := h.project(t, "workspace-alpha")
	// The table declares member-b with owner/repo-a and owner/repo-b; the node
	// carries a repository that is no longer declared and a member that is not.
	wsPublishedNode(t, p, "workspace-alpha", []any{
		wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b", "owner/repo-z"),
		wsPublishedMember("member-c", "owner/repo-c"),
	}, 25_000_000)

	r := newWSUpdateRig(t, h)
	oc, out, err := r.apply(t, true)
	if err != nil {
		t.Fatalf("the run did not publish the node the record declares: %v\n%s", err, out)
	}
	if !oc.Changed {
		t.Errorf("the publish reports no change: %s", oc.Detail)
	}
	for _, want := range []string{
		"removed: member member-c",
		"removed: owner/repo-z from member-b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the diff does not say %q, so the operator confirmed a removal they were not shown:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nothing is removed") {
		t.Errorf("the diff says nothing is removed while it removes a member and a repository:\n%s", out)
	}
	if got := wsMembers(t, p.db, "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
		t.Errorf("the published members are %v, want [member-b]: member-c is not in this installation's table any more", got)
	}
	if got := wsRepos(t, p.db, "workspace-alpha", "member-b"); !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("member-b's published repositories are %v, want the two the table declares", got)
	}
}

// TestUpdateTheCeilingIsTheDirectionTheWideningIs: the cap half of the
// classifier, where getting the direction wrong is the defect that matters. A
// node that states a ceiling and a record that states none leaves the group
// with NO bound on what it spends through the workspace's own credentials, so
// it is the largest widening this node has and it takes the typed project name.
// A ceiling where there was none is the opposite direction — it BOUNDS the
// group — and it is an ordinary write, which is what keeps the typed name
// reserved for the writes that admit.
func TestUpdateTheCeilingIsTheDirectionTheWideningIs(t *testing.T) {
	t.Run("a dropped ceiling is a widening", func(t *testing.T) {
		h := newWSUpdateHome(t, wsOwnerTable) // the record states no cap
		p := h.project(t, "workspace-alpha")
		wsPublishedNode(t, p, "workspace-alpha", []any{
			wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"),
		}, 10_000_000)

		r := newWSUpdateRig(t, h)
		_, out, err := r.apply(t, true)
		if err == nil {
			t.Fatalf("--yes published a record that removes the workspace's own ceiling:\n%s", out)
		}
		if !strings.Contains(err.Error(), "spend through the workspace's own credentials becomes unbounded") {
			t.Errorf("the refusal does not say what the dropped ceiling admits: %v\n%s", err, out)
		}
		if !strings.Contains(out, "removed: the workspace's own ceiling of $10 a month") {
			t.Errorf("the diff does not show the ceiling as removed:\n%s", out)
		}
		// And the owner's own word completes it, which is what makes the typed
		// branch a way through rather than a wall.
		fakeTerminal(t)
		r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
		if _, out, err := r.apply(t, false); err != nil {
			t.Fatalf("the typed project name did not publish the record: %v\n%s", err, out)
		}
		if cap, ok := wsNode(t, p.db, "workspace-alpha")["cap"]; ok {
			t.Errorf("the published node still carries the ceiling the record does not state: %v", cap)
		}
	})

	t.Run("a ceiling where there was none bounds the group", func(t *testing.T) {
		h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
		p := h.project(t, "workspace-alpha")
		wsPublishedNode(t, p, "workspace-alpha", []any{
			wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"),
		}, 0)

		r := newWSUpdateRig(t, h)
		_, out, err := r.apply(t, true)
		if err != nil {
			t.Fatalf("--yes was refused a write that BOUNDS the group: %v\n%s", err, out)
		}
		cap, ok := wsNode(t, p.db, "workspace-alpha")["cap"].(map[string]any)
		if !ok {
			t.Fatalf("the published node carries no ceiling: %v", wsNode(t, p.db, "workspace-alpha"))
		}
		if got := wsNumber(t, cap["monthly_micros"]); got != 25_000_000 {
			t.Errorf("the published ceiling is %v micro-dollars, want 25000000 ($25)", cap["monthly_micros"])
		}
		if strings.Contains(out, "added: the workspace's own ceiling") {
			t.Errorf("the diff reports a ceiling where there was none as what this write ADDS to the group:\n%s", out)
		}
	})
}

// TestUpdateTheFirstRecordTakesTheTypedName: a workspace that has no published
// record is the whole membership chosen where none was, so the write is the
// widening the whole classifier is about — every member it names is admitted by
// it, and from the moment it lands every run of every member reads it. --yes
// does not stand in, the write happens on the owner's own typed word, and the
// refusal says what was chosen rather than naming a comparison that does not
// exist yet.
//
// The state it closes is the one a hand edit reaches: `workspace_members` in
// the YAML is a document this stage builds a node from, so a session that
// edited the table past the fenced `fugaro workspace member add` would
// otherwise publish a whole group behind a mark.
func TestUpdateTheFirstRecordTakesTheTypedName(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	p := h.project(t, "workspace-alpha")

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("--yes published the group's first record:\n%s", out)
	}
	if !strings.Contains(err.Error(), "has no published record at workspaces/workspace-alpha") {
		t.Errorf("the refusal does not say there is no record to be wider than: %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "the whole group: the member list, the repositories each member contributes and the workspace's own ceiling are chosen here for the first time") {
		t.Errorf("the refusal does not say what this write chooses: %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "does not stand in") {
		t.Errorf("the refusal does not tell the operator that --yes was not the answer: %v\n%s", err, out)
	}
	if p.db.Value(projectpolicy.PathWorkspace("workspace-alpha")) != nil {
		t.Error("the refused first record was published anyway")
	}

	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
	oc, out, err := r.apply(t, false)
	if err != nil {
		t.Fatalf("the typed project name did not publish the first record: %v\n%s", err, out)
	}
	if !oc.Changed {
		t.Errorf("the first publish reports no change: %s", oc.Detail)
	}
	if got := wsMembers(t, p.db, "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
		t.Errorf("the published members are %v, want [member-b]", got)
	}
}

// TestUpdateAdmittingAMemberTakesTheTypedName is the positive control under the
// widening classifier: a publish that admits a project the node does not carry
// is reserved for the owner's own typed word — --yes does not stand in, the
// no-terminal session is refused with the sentence that says so, and the typed
// name completes it. Without this the typed branch of the confirmation would be
// dead code that no test could tell from a classifier that had stopped
// classifying anything.
func TestUpdateAdmittingAMemberTakesTheTypedName(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTwoMembers+wsOwnerCap)
	p := h.project(t, "workspace-alpha")
	wsPublishedNode(t, p, "workspace-alpha", []any{
		wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"),
	}, 25_000_000)

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("--yes admitted a member to the workspace:\n%s", out)
	}
	if !strings.Contains(err.Error(), "member-d is admitted to the workspace") {
		t.Errorf("the refusal does not name the member this write admits: %v\n%s", err, out)
	}
	if got := wsMembers(t, p.db, "workspace-alpha"); !slices.Equal(got, []string{"member-b"}) {
		t.Errorf("the published members are %v, want [member-b]: nothing was confirmed", got)
	}
	// The session with no terminal is refused with the same sentence inside the
	// error, because that is the only place it can be read.
	if !strings.Contains(err.Error(), "this admits more to the workspace workspace-alpha than the published record does") {
		t.Errorf("the refusal does not say what the write admits: %v\n%s", err, out)
	}

	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
	oc, out, err := r.apply(t, false)
	if err != nil {
		t.Fatalf("the typed project name did not admit the member: %v\n%s", err, out)
	}
	if !oc.Changed {
		t.Errorf("the admitted publish reports no change: %s", oc.Detail)
	}
	if got := wsMembers(t, p.db, "workspace-alpha"); !slices.Equal(got, []string{"member-b", "member-d"}) {
		t.Errorf("the published members are %v, want [member-b member-d]", got)
	}
}

// TestUpdateRefusesANodeCarryingAKeyThisFugaroDoesNotKnow is P8 at this stage's
// write: a node a NEWER Fugaro wrote is read (the reader tolerates the key) and
// refused where this run would REWRITE it, because publishing the local record
// would write a document without that key — a value somebody else's release put
// there and this binary cannot carry. The count and the names are what the
// refusal says about it, and the node is left exactly as it was.
func TestUpdateRefusesANodeCarryingAKeyThisFugaroDoesNotKnow(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	p := h.project(t, "workspace-alpha")
	// The node differs from the record (member-c is not declared any more), so
	// this run would write — which is the moment the unknown key is lost. The
	// whole document is seeded here rather than through wsPublishedNode: the
	// extra key has to be in the stored value, and a map read back and edited
	// does not write through to the database.
	p.db.Set(projectpolicy.PathWorkspace("workspace-alpha"), map[string]any{
		"version": projectpolicy.WorkspaceVersion,
		"name":    "workspace-alpha",
		"members": []any{
			wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"),
			wsPublishedMember("member-c", "owner/repo-c"),
		},
		"cap":    map[string]any{"monthly_micros": 25_000_000},
		"rollup": map[string]any{"spend_micros": 1},
		// A key whose own name carries an escape and a bell, because these
		// names are another release's and reach the terminal: they are printed
		// through pluginwire.Printable like every other cloud-derived name in
		// this package, and a refusal that echoed them raw would let a document
		// this binary refuses to read rewrite the line that refuses it.
		"evil\x1b]52;c;x\x07": map[string]any{"spend_micros": 1},
	})

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("a publish wrote over a node carrying a key this Fugaro does not know:\n%s", out)
	}
	if !strings.Contains(err.Error(), "rollup") {
		t.Errorf("the refusal does not name the key it would drop: %v", err)
	}
	if !strings.Contains(err.Error(), `\u001b`) || strings.ContainsAny(err.Error()+out, "\x1b\x07") {
		t.Errorf("the unknown key's name reached the terminal unescaped: %v\n%s", err, out)
	}
	after := wsNode(t, p.db, "workspace-alpha")
	if _, still := after["rollup"]; !still {
		t.Errorf("the refusal wrote the node anyway: %v", after)
	}
	if got := wsMembers(t, p.db, "workspace-alpha"); !slices.Equal(got, []string{"member-b", "member-c"}) {
		t.Errorf("the refused write changed the published members: %v", got)
	}
}

// TestUpdateARaisedCeilingIsAWidening: the other half of the cap classifier,
// with the higher ceiling on the record's side. A record that states MORE than
// the node does raises what the group may spend through the workspace's own
// credentials, so it is a widening and takes the owner's typed word — which is
// what keeps a plan gate from publishing it (the class rule, D1026). Inverting
// the comparison, or dropping the branch, would report it as nothing and let
// --yes through.
func TestUpdateARaisedCeilingIsAWidening(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap) // $25 a month
	p := h.project(t, "workspace-alpha")
	wsPublishedNode(t, p, "workspace-alpha", []any{
		wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"),
	}, 10_000_000) // $10, the ceiling this record raises

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("--yes raised the workspace's own ceiling:\n%s", out)
	}
	if !strings.Contains(err.Error(), "the workspace's own ceiling rises from $10 to $25 a month") {
		t.Errorf("the refusal does not name the ceiling this write raises: %v\n%s", err, out)
	}
	if !strings.Contains(out, "added: the workspace's own ceiling rises from $10 to $25 a month") {
		t.Errorf("the diff does not show the raised ceiling:\n%s", out)
	}
	if got := wsCeiling(t, p.db, "workspace-alpha"); got != 10_000_000 {
		t.Errorf("the published ceiling is %d micro-dollars, want the 10 the node stated: nothing was confirmed", got)
	}
	// And the owner's own word completes it, which is what makes the typed
	// branch a way through rather than a wall.
	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
	if _, out, err := r.apply(t, false); err != nil {
		t.Fatalf("the typed project name did not raise the ceiling: %v\n%s", err, out)
	}
	if got := wsCeiling(t, p.db, "workspace-alpha"); got != 25_000_000 {
		t.Errorf("the published ceiling is %d micro-dollars, want 25000000 ($25)", got)
	}
}

// TestUpdateANewRepositoryOnAListedMemberIsAWidening: the third branch of the
// classifier, and the smallest one — a member the node already carries, with a
// repository the record adds to it. Every run of that repository reads the
// record, so a repository is as much of an admission as the member itself is,
// and it takes the same typed word. Disabling the branch reports the write as
// nothing and lets --yes through.
func TestUpdateANewRepositoryOnAListedMemberIsAWidening(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap) // member-b with repo-a and repo-b
	p := h.project(t, "workspace-alpha")
	wsPublishedNode(t, p, "workspace-alpha", []any{
		wsPublishedMember("member-b", "owner/repo-a"),
	}, 0)

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("--yes added a repository to a listed member:\n%s", out)
	}
	if !strings.Contains(err.Error(), "member-b contributes owner/repo-b") {
		t.Errorf("the refusal does not name the repository this write adds: %v\n%s", err, out)
	}
	if !strings.Contains(out, "added: member-b contributes owner/repo-b") {
		t.Errorf("the diff does not show the repository this write adds:\n%s", out)
	}
	if got := wsRepos(t, p.db, "workspace-alpha", "member-b"); !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("member-b's published repositories are %v, want the one the node carried: nothing was confirmed", got)
	}
	fakeTerminal(t)
	r.cmd.SetIn(strings.NewReader("workspace-alpha\n"))
	if _, out, err := r.apply(t, false); err != nil {
		t.Fatalf("the typed project name did not add the repository: %v\n%s", err, out)
	}
	if got := wsRepos(t, p.db, "workspace-alpha", "member-b"); !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("member-b's published repositories are %v, want the two the table declares", got)
	}
}

// TestUpdateEscapesTheRemovedRepositoryItPrints: a repository of the PUBLISHED
// node is the one string of this document nothing holds to a grammar. The name
// above it is a project name (config.ProjectNameRE), the cap is a number, and a
// repository the local table contributes is held to owner/name where the config
// is read (localcfg's repoRE) — but the node's own repositories are checked only
// for being non-empty (DecodeWorkspace), because a node a later Fugaro wrote may
// name them more freely. The removal line prints them, and that line is what the
// operator confirms, so they go through pluginwire.Printable like every other
// document-derived name in this package: a repository carrying an escape and a
// carriage return is a legal JSON string, and printed raw it would rewrite the
// line that says what this write removes.
func TestUpdateEscapesTheRemovedRepositoryItPrints(t *testing.T) {
	const evil = "owner/evil\x1b[2K\rmember-b"
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	p := h.project(t, "workspace-alpha")
	// The node carries a repository the table does not declare any more, so this
	// write removes it and the diff has to say so — in one line.
	wsPublishedNode(t, p, "workspace-alpha", []any{
		wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b", evil),
	}, 25_000_000)

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, `removed: owner/evil\u001b[2K\u000dmember-b from member-b`) {
		t.Errorf("the diff does not print the removed repository escaped:\n%s", out)
	}
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("a repository of the published node reached the terminal raw:\n%q", out)
	}
}

// TestUpdateEscapesWhatTheRecordMigrationEchoes: the version-0 node is read with
// the record's own parser (workspace.Parse, migrateWorkspaceV0ToV1), and that
// parser's messages echo the repositories they refuse — which reaches the
// operator's terminal through this stage's refusal. A repository is the one
// string of this document nothing holds to a grammar, so those echoes quote
// what they name: a hand-written node carrying an escape would otherwise
// rewrite the line that refuses it.
func TestUpdateEscapesWhatTheRecordMigrationEchoes(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap)
	p := h.project(t, "workspace-alpha")
	// A version-0 node (the record's own file shape) whose repositories are out
	// of order, the first of them carrying an escape.
	p.db.Set(projectpolicy.PathWorkspace("workspace-alpha"), map[string]any{
		"name": "workspace-alpha",
		"members": []any{
			map[string]any{"project": "member-b", "repos": []any{"owner/z\x1b[2K", "owner/a"}},
		},
		"cap": map[string]any{"monthly": 25},
	})

	r := newWSUpdateRig(t, h)
	_, out, err := r.apply(t, true)
	if err == nil {
		t.Fatalf("a version-0 node with unsorted repositories was migrated:\n%s", out)
	}
	if !strings.Contains(err.Error(), "out of order after") {
		t.Errorf("the refusal does not say what the record's own reader refused: %v\n%s", err, out)
	}
	if strings.ContainsAny(err.Error()+out, "\x1b\r") {
		t.Errorf("the repository the record's reader refused reached the terminal raw:\n%q\n%v", out, err)
	}
}

// TestUpdateSkipsAWorkspaceDatabaseThisInstallationDoesNotOwn: the stage's own
// D1017 read, which is the FIRST thing after the record is built from the
// config and the reason it is there rather than after the node read — this
// stage's whole work (the read, the ETag and the conditional write) lands in the
// workspace's database, and a config naming another Firebase project whose
// database carries another installation's mark is a database this stage must not
// touch. Disabling the predicate publishes this record into a stranger's
// database, over whatever node it holds; the policy stage's version of this test
// exercises its own call and not this one.
func TestUpdateSkipsAWorkspaceDatabaseThisInstallationDoesNotOwn(t *testing.T) {
	r, db := updateBudgetRigFor(t, "other-project")
	// Another installation's database: its mark names a Fugaro project and a
	// GCP project that are not this installation's.
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": "workspace-a", "gcp_project": "proj-9999", "version": 1})
	r.appendConfig(t, wsOwnerTable+wsOwnerCap)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("another installation's database stopped the whole update: %v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got := res.state(initflow.Workspace); got != "skipped" {
		t.Errorf("the workspace stage is %q, want skipped:\n%s", got, out)
	}
	detail := res.stageDetail(t, initflow.Workspace)
	for _, want := range []string{"workspace-a", "proj-9999"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the skip does not name the installation whose database this is (%q): %q", want, detail)
		}
	}
	if v := db.Value(projectpolicy.PathWorkspace(initProjectName)); v != nil {
		t.Errorf("the run published a workspace record into a database this installation never adopted: %v", v)
	}
}

// TestUpdatePublishesTheWorkspaceRecordInTheRunThatNamesTheBackend: the run
// that gives the installation a budget backend is the run that gives this stage
// a database, and the record is published into it in that same run.
//
// The stage's first answer is read in the loop's read-only preview pass, which
// is before the Firebase stage has written budget.firebase_project and
// budget.rtdb_url into the config — so the answer there is "the config names no
// Firebase budget backend", and it is true of the file at that moment. What the
// stage may not do is keep it: initRun.writeConfig drops this stage's read
// (update_workspace.go's forget), so the pass that publishes asks the config in
// force and finds the database the run has just named.
//
// Red before that: the run printed the "nowhere to publish it" skip over the
// config it had just written, and the record landed on the next update — the
// second run of the one documented command, which is what the hard rule forbids.
func TestUpdatePublishesTheWorkspaceRecordInTheRunThatNamesTheBackend(t *testing.T) {
	r := newFBRig(t)
	// The database the door writes: an installation's own, marked as its (the
	// Firebase stage refuses one that holds data and is not), with the Firestore
	// database setup already created so the run asks no location question.
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.fs.SetDatabase("us-east5")
	r.appendConfig(t, wsOwnerTable+wsOwnerCap)
	r.save(t)
	fakeTerminal(t)
	// The one thing --yes does not stand in for: the group's first record takes
	// the project's name typed.
	out, errOut, err := executeStdin(t, initProjectName+"\n", "update", "--firebase-project", fpID, "--budget-mode", "observe", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("update --firebase-project failed: %v\n%s", err, s)
	}
	if v := r.db.Value(projectpolicy.PathWorkspace(initProjectName)); v == nil {
		t.Fatalf("the run that named the budget backend published no workspace record: the record lands on the next update, which is the second run the hard rule forbids\n%s", s)
	}
	if got, want := wsMembers(t, r.db, initProjectName), []string{"member-b"}; !slices.Equal(got, want) {
		t.Errorf("the published record lists %v, want %v:\n%s", got, want, s)
	}
	if got, want := wsCeiling(t, r.db, initProjectName), int64(25_000_000); got != want {
		t.Errorf("the published record's ceiling is %d micro-dollars, want %d:\n%s", got, want, s)
	}
}

// TestUpdatePublishesIntoTheDatabaseTheRunHasJustPointedAt: the other half of
// the same staleness. A config can name a budget backend that the run then
// corrects — the Firebase project's database was rebuilt and the URL in the file
// is the old one (`update --firebase-project` rewrites it from the deployment's
// own outputs), and the stage's read is made in the preview pass, against the
// URL the file names THEN.
//
// What may not survive the run's own write is that read: the record would be
// published into the old database, which is a document no member's run reads and
// a write into a database this installation has just stopped using. forget drops
// it, and the pass that publishes reads the config in force.
//
// Red before that: the record landed in the database the config no longer names.
func TestUpdatePublishesIntoTheDatabaseTheRunHasJustPointedAt(t *testing.T) {
	r := newFBRig(t)
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.fs.SetDatabase("us-east5")
	// The database the config names before the run: this installation's own (its
	// mark is what the ownership gate reads), so nothing is skipped here — the
	// stage reads it and would publish into it.
	old := gcpfake.NewRTDB(t)
	old.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+old.URL+", firebase_project: \""+fpID+"\" }\n")
	r.appendConfig(t, wsOwnerTable+wsOwnerCap)
	r.save(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "update", "--firebase-project", fpID, "--budget-mode", "observe", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("update --firebase-project failed: %v\n%s", err, s)
	}
	if v := old.Value(projectpolicy.PathWorkspace(initProjectName)); v != nil {
		t.Errorf("the record was published into the database the run has just stopped pointing at: %v\n%s", v, s)
	}
	if v := r.db.Value(projectpolicy.PathWorkspace(initProjectName)); v == nil {
		t.Fatalf("the run published no workspace record into the database it now names:\n%s", s)
	}
	if got, want := wsMembers(t, r.db, initProjectName), []string{"member-b"}; !slices.Equal(got, want) {
		t.Errorf("the published record lists %v, want %v:\n%s", got, want, s)
	}
}
