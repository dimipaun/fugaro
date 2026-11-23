package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// update_workspace_node_test.go is WS4's own suite: the workspace's credentials
// node — the second document the workspace stage publishes, at
// workspace_credentials/<name> in the workspace's OWN database (contract C3,
// docs/workspace-service/contract-node.md) — and the writer that puts it there.
//
// The record at workspaces/<name> says who the group is (W2, its own suite is
// update_workspace_test.go); this node says how the workspace's own service
// reaches each member and which of the workspace's credentials that member's
// callers may be served, and it is the document that reaches into a member's run
// (R11, R13): a member the table no longer declares, or an entry the owner
// unshared, stops being served at the service's next read. So the assertions
// here are about the node's own values — the rows and the pool, canonical and
// sorted, so the service's comparison of a verified slug to a row is a string
// compare — and about the two rules the writer adds to them: an entry may name
// only a member of the workspace (F13), and a pool entry offered to every member
// needs the workspace's own ceiling (D2977).

// wsOwnerShared is the workspace's own list of the credentials its pool offers
// its members: one model entry and one forge entry, which are the two kinds an
// entry can be (F15, U2). The member it is offered to is the table's, because an
// entry naming anybody else is what F13 refuses.
const wsOwnerShared = `workspace_shared:
  bitbucket-token:
    kind: bitbucket
    projects:
      - member-b
    covers:
      - owner/repo-b
  claude-oauth-token:
    kind: model
    projects:
      - member-b
`

// wsOwnerForeignRepo is the owner's table with one change: the member's job
// service account is a job of a repository no config on this machine states a
// forge for (`owner/repo-x`, which no fixture states in its repos block). The
// local table writes a repository `owner/name` and the node carries it
// canonically (`<provider>/<owner>/<name>`, R6), so the provider is the half this
// file does not carry and the writer has to resolve — and a repository nothing
// here can spell canonically is refused rather than guessed (F42).
const wsOwnerForeignRepo = `workspace_members:
  member-b:
    job_service_accounts:
      - account: job-account-b@member-b-gcp.iam.gserviceaccount.com
        repos:
          - owner/repo-x
    firebase_project: member-b-fb
    fp: member-b
    exchange_url: https://exchange.example.com/v1
    repos:
      - owner/repo-b
`

// wsCredsRow is one member row of a credentials node a test seeds, in the shape
// the writer publishes: an account with the repositories it is a job of, and the
// member's Firebase project, fp and exchange URL.
func wsCredsRow(project string) projectpolicy.WorkspaceCredentialMember {
	account := "job-account-" + project + "@" + project + "-gcp.iam.gserviceaccount.com"
	return projectpolicy.WorkspaceCredentialMember{
		Project:         project,
		FP:              project,
		FirebaseProject: project + "-fb",
		JobAccounts:     []string{account},
		JobAccountRepos: []projectpolicy.WorkspaceJobAccount{{Account: account, Repos: []string{"github/owner/repo-b"}}},
		ExchangeURL:     "https://exchange.example.com/v1",
	}
}

// wsNodeWith is the credentials node a test seeds, as the database holds it.
// Every document here is built by the package's OWN encoder, so a fixture cannot
// declare a node this Fugaro would not write — which matters for the rules a
// reader refuses (a member's two halves disagreeing, an entry naming no kind, a
// pool entry offered to nobody).
func wsNodeWith(t *testing.T, name string, members []string, shared ...projectpolicy.WorkspaceShared) map[string]any {
	t.Helper()
	w := &projectpolicy.WorkspaceCredentials{Version: projectpolicy.WorkspaceCredentialsVersion, Name: name}
	for _, p := range slices.Sorted(slices.Values(members)) {
		w.Members = append(w.Members, wsCredsRow(p))
	}
	w.Shared = slices.Clone(shared)
	slices.SortFunc(w.Shared, func(a, b projectpolicy.WorkspaceShared) int { return strings.Compare(a.Name, b.Name) })
	raw, err := projectpolicy.WorkspaceCredentialsNode(w)
	if err != nil {
		t.Fatalf("the node this test seeds is not one this Fugaro publishes: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// wsCredsNode is the node at the workspace's own path, decoded with the
// package's own reader: what the workspace's service and this machine's next run
// read it as.
func wsCredsNode(t *testing.T, db *gcpfake.RTDB, name string) *projectpolicy.WorkspaceCredentials {
	t.Helper()
	path := projectpolicy.PathWorkspaceCredentials(name)
	v := db.Value(path)
	if v == nil {
		t.Fatalf("nothing was published at %s", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	node, found, err := projectpolicy.DecodeWorkspaceCredentials(raw)
	if err != nil || !found {
		t.Fatalf("the node at %s does not read back (found=%v): %v", path, found, err)
	}
	return node
}

// wsCredsEqual compares two credentials nodes by value, printing both when they
// differ: the whole document is what the service reads, so a test states the
// whole of what it expects rather than the field it happens to be about.
func wsCredsEqual(t *testing.T, got, want *projectpolicy.WorkspaceCredentials) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the published credentials node is\n  %s\nwant\n  %s", gotJSON, wantJSON)
	}
}

// TestUpdatePublishesTheNode is the task's own red step: before it, a run that
// published the workspace record wrote no credentials node at all, and the
// workspace's own service — which resolves a caller by that node — had nothing
// to read. It is the same run and the same declaration as the record's: the
// member table and the shared list, published for the second reader (R7).
func TestUpdatePublishesTheNode(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap+wsOwnerShared)
	r := newWSUpdateRig(t, h)
	// The group's first record is the owner's own session (D1555): this run is
	// the owner's, at a terminal, with nothing typed at them.
	fakeTerminal(t)
	oc, out, err := r.apply(t, false)
	if err != nil {
		t.Fatalf("the owner's run published nothing: %v\n%s", err, out)
	}
	if !oc.Changed {
		t.Errorf("the first publish reports no change: %s", oc.Detail)
	}
	p := h.project(t, "workspace-alpha")
	// The whole document, value by value: the row is the member table's (its
	// account, and the repository that account is a job of written canonically
	// — `github/owner/repo-b`, because the service compares the slug a run's
	// verified identity carries to this list, R6) and the pool is the shared
	// list's, in name order with each entry's kind, its project and, for the
	// forge entry, the repository it covers.
	account := "job-account-b@member-b-gcp.iam.gserviceaccount.com"
	wsCredsEqual(t, wsCredsNode(t, p.db, "workspace-alpha"), &projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    "workspace-alpha",
		Members: []projectpolicy.WorkspaceCredentialMember{{
			Project:         "member-b",
			FP:              "member-b",
			FirebaseProject: "member-b-fb",
			JobAccounts:     []string{account},
			JobAccountRepos: []projectpolicy.WorkspaceJobAccount{{
				Account: account,
				Repos:   []string{"github/owner/repo-b"},
			}},
			ExchangeURL: "https://exchange.example.com/v1",
		}},
		Shared: []projectpolicy.WorkspaceShared{
			{Name: "bitbucket-token", Kind: "bitbucket", Projects: []string{"member-b"}, Covers: []string{"bitbucket/owner/repo-b"}},
			{Name: "claude-oauth-token", Kind: projectpolicy.SharedKindModel, Projects: []string{"member-b"}},
		},
	})

	// A second run of the one command writes nothing: the node it would build is
	// the node that is published, value by value, and a stage of `fugaro update`
	// that rewrote it every run would be a write per run against a document that
	// is already right.
	r2 := newWSUpdateRig(t, h)
	if st := r2.check(t); st.State != initflow.Done {
		t.Errorf("the second run's check = %v (%s), want done: the node carries what this installation declares", st.State, st.Detail)
	}
	oc2, out2, err := r2.apply(t, true)
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out2)
	}
	if oc2.Changed {
		t.Errorf("the second run wrote the credentials node again: %s\n%s", oc2.Detail, out2)
	}
}

// TestWriterRefusesASharedEntryNamingNoMember is F13: the pool entry's allow-list
// is the owner's declaration of which members a credential is offered to, and a
// project the workspace's member table does not declare is one the workspace's
// own service would be asked to resolve a caller of and find no member for. The
// refusal names the member's own command, and nothing is written — not the node
// and not the record beside it, the two being one declaration of one run.
func TestWriterRefusesASharedEntryNamingNoMember(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap+`workspace_shared:
  claude-oauth-token:
    kind: model
    projects:
      - member-z
`)
	r := newWSUpdateRig(t, h)
	fakeTerminal(t)
	_, out, err := r.apply(t, false)
	if err == nil {
		t.Fatalf("a pool entry offered to a project that is not a member was published:\n%s", out)
	}
	for _, want := range []string{"F13", "member-z", "fugaro workspace project add", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q, so the owner is not told what to do: %v", want, err)
		}
	}
	p := h.project(t, "workspace-alpha")
	if v := p.db.Value(projectpolicy.PathWorkspaceCredentials("workspace-alpha")); v != nil {
		t.Errorf("the run wrote a credentials node over the refusal: %v", v)
	}
	if v := p.db.Value(projectpolicy.PathWorkspace("workspace-alpha")); v != nil {
		t.Errorf("the run wrote the record of the same declaration over the other half's refusal: %v", v)
	}
}

// TestWriterRefusesAllMembersWithoutACap is D2977: a pool entry offered to every
// member of the workspace — the ones that join later included — is bounded by the
// workspace's ceiling alone, and the ceiling is the owner's fence over their own
// money. The node's own reader cannot ask it (the node carries no cap), so the
// writer does: a workspace that states no cap while such an entry exists is
// refused, and the entry that is PUBLISHED is asked beside the one this run would
// write — a cap the owner removed from their own file is exactly the state this
// refusal is about, and nothing is written either way.
func TestWriterRefusesAllMembersWithoutACap(t *testing.T) {
	// The table without its cap block: the workspace's own file, after the owner
	// took the ceiling out of it.
	h := newWSUpdateHome(t, wsOwnerTable)
	p := h.project(t, "workspace-alpha")
	p.db.Set(projectpolicy.PathWorkspaceCredentials("workspace-alpha"), wsNodeWith(t, "workspace-alpha", []string{"member-b"},
		projectpolicy.WorkspaceShared{Name: "claude-oauth-token", Kind: projectpolicy.SharedKindModel, AllMembers: true}))
	r := newWSUpdateRig(t, h)
	fakeTerminal(t)
	_, out, err := r.apply(t, false)
	if err == nil {
		t.Fatalf("a pool entry offered to every member was published by a workspace that states no cap:\n%s", out)
	}
	for _, want := range []string{"states no cap", "claude-oauth-token", "every member", "fugaro workspace cap set --workspace workspace-alpha --monthly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if v := p.db.Value(projectpolicy.PathWorkspaceCredentials("workspace-alpha")); v == nil {
		t.Error("the refusal took the published node with it: a refusal writes nothing and leaves what is there")
	}
}

// TestWriterRefusesAJobRepoWithoutAProvider is the writer's other refusal, and
// the one the documented path reaches: `fugaro workspace project add --job-repos`
// writes a job account's repositories as `owner/repo` with no forge
// (localcfg.WorkspaceJobAccount.Repos), while the node's rows carry them
// canonically because the workspace's own service holds the slug a run's verified
// identity yields to them (R6, F42). The provider is therefore resolved from this
// machine's own configs — the member's project config first, then this
// installation's and the repository's checkout — and a repository no config here
// states is a refusal and never a guess: an invented provider would hold a run of
// another forge's repository to this account, and a node named the way the file
// names it is one the reader refuses. Nothing is written, the record of the same
// declaration included.
func TestWriterRefusesAJobRepoWithoutAProvider(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerForeignRepo+wsOwnerCap)
	r := newWSUpdateRig(t, h)
	fakeTerminal(t)
	_, out, err := r.apply(t, false)
	if err == nil {
		t.Fatalf("a job account's repository with no stated forge was published:\n%s", out)
	}
	for _, want := range []string{"member-b", "owner/repo-x", "no project config on this machine states which forge", "repos.owner/name.provider", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q, so the owner is not told what to do: %v", want, err)
		}
	}
	p := h.project(t, "workspace-alpha")
	if v := p.db.Value(projectpolicy.PathWorkspaceCredentials("workspace-alpha")); v != nil {
		t.Errorf("the run wrote a credentials node over the refusal: %v", v)
	}
	if v := p.db.Value(projectpolicy.PathWorkspace("workspace-alpha")); v != nil {
		t.Errorf("the run wrote the record of the same declaration over the refusal: %v", v)
	}
}

// TestUpdateNamesAnUnshareWhenRunsAreInFlight is R11 for this stage: the one
// reach the workspace's documents have on a run in flight is through the service
// that re-reads the credentials node as it serves a call (R13), so a run whose
// member this write takes out of the table — or whose repository no entry offers
// a credential to any more — is refused at that service's next read, and the
// door's note says so in one short line. The negative half is the same run over a
// node that already carries the declaration: nothing is taken away, and the note
// names no such step.
func TestUpdateNamesAnUnshareWhenRunsAreInFlight(t *testing.T) {
	line := func(t *testing.T, node map[string]any) string {
		t.Helper()
		f := newProjectFixture(t, wsOwnerTable+wsOwnerCap+wsOwnerShared)
		f.db.Set(projectpolicy.PathWorkspaceCredentials("aurora"), node)
		f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
		h := newMigrationRig(t, f, &initOptions{})
		h.s.e.announceRunsInFlight(context.Background())
		return wsInFlightNote(t, h.out.String())
	}

	t.Run("a member removed and an entry unshared", func(t *testing.T) {
		// The published node carries member-b (the table's) and member-c (the
		// table's no longer), and the pool holds claude-oauth-token (the list's)
		// and claude-oauth-token-team (which the list has dropped): the write
		// this run would make takes both away.
		got := line(t, wsNodeWith(t, "aurora", []string{"member-b", "member-c"},
			projectpolicy.WorkspaceShared{Name: "bitbucket-token", Kind: "bitbucket", Projects: []string{"member-b"}, Covers: []string{"bitbucket/owner/repo-b"}},
			projectpolicy.WorkspaceShared{Name: "claude-oauth-token", Kind: projectpolicy.SharedKindModel, Projects: []string{"member-b"}},
			projectpolicy.WorkspaceShared{Name: "claude-oauth-token-team", Kind: projectpolicy.SharedKindModel, Projects: []string{"member-b"}}))
		if !strings.Contains(got, "1 run in flight") {
			t.Errorf("the note does not say a run is in flight: %q", got)
		}
		for _, want := range []string{"workspace:", "removed member", "unshared entry"} {
			if !strings.Contains(got, want) {
				t.Errorf("the note does not name %q, which is what this write takes away from a run in flight:\n%s", want, got)
			}
		}
	})

	t.Run("a node that already carries the declaration", func(t *testing.T) {
		got := line(t, wsNodeWith(t, "aurora", []string{"member-b"},
			projectpolicy.WorkspaceShared{Name: "bitbucket-token", Kind: "bitbucket", Projects: []string{"member-b"}, Covers: []string{"bitbucket/owner/repo-b"}},
			projectpolicy.WorkspaceShared{Name: "claude-oauth-token", Kind: projectpolicy.SharedKindModel, Projects: []string{"member-b"}}))
		if strings.Contains(got, "workspace:") {
			t.Errorf("the note names the workspace credentials node over a node that already carries the declaration:\n%s", got)
		}
		if !strings.Contains(got, "no step reaches them") {
			t.Errorf("the note does not say that no step reaches the runs:\n%s", got)
		}
	})
}

// wsInFlightNote is the door's own note about the runs in flight, read off what
// it printed: the count's line and the indented step lines under it. It is
// inFlightLine's screen half alone, without the log half — that helper reads the
// run's own log beside the screen, and this fixture is a stage over a config with
// no run's workdir behind it.
func wsInFlightNote(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "update waits for none") {
			continue
		}
		block := []string{line}
		for _, under := range lines[i+1:] {
			if !strings.HasPrefix(under, "  ") {
				break
			}
			block = append(block, under)
		}
		return strings.Join(block, "\n")
	}
	t.Fatalf("the update printed nothing about the runs in flight:\n%s", out)
	return ""
}

// TestNodeIsPublishedOnlyAfterTheServiceDeploys is R7: the workspace's own
// project's root is the stage BEFORE the one that publishes this node (initStages'
// order — newWorkspaceProjectStage, then the workspace record and the node), and
// the node is what the identity that root creates reads. A run whose deploy of
// that root does not succeed stops there, and the credentials node is not
// published by a run whose service is not up: the member's runs would be served
// by nothing, and the node would say they are served.
//
// The second half is the same run with the root converged: the node is published
// in the same update, which is what "only after" means — not on a later one.
func TestNodeIsPublishedOnlyAfterTheServiceDeploys(t *testing.T) {
	// The installation whose Firebase is its own, with a database setup already
	// created and the workspace's own half in its config: the run that reaches
	// the workspace stage is the ordinary update.
	rig := func(t *testing.T) *fbRig {
		t.Helper()
		r := newFBRig(t)
		r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
		r.fs.SetDatabase("us-east5")
		appendWorkspaceConfig(t, r.initRig, wsOwnerTable+wsOwnerCap)
		return r
	}

	t.Run("a deploy that fails publishes no node", func(t *testing.T) {
		r := rig(t)
		// The workspace's own root: a real plan of that root (the cover checks
		// it), and an apply the cloud refuses.
		r.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))
		r.script["plan@workspace"] = map[string]any{"exit": 2}
		r.script["apply@workspace"] = map[string]any{"exit": 1, "stderr": "Error: the apply did not land\n"}
		r.save(t)
		fakeTerminal(t)
		// The typed name is the confirmation the root's own apply asks for; the
		// run stops inside it, so nothing else reads a line.
		out, errOut, err := executeStdin(t, wsProjectName+"\n", "update", "--firebase-project", fpID, "--budget-mode", "observe")
		s := out + errOut
		if err == nil {
			t.Fatalf("the run went on after the workspace's own deploy failed:\n%s", s)
		}
		if !strings.Contains(err.Error(), "the apply did not land") {
			t.Fatalf("the run stopped for another reason than the root's failed apply: %v\n%s", err, s)
		}
		if v := r.db.Value(projectpolicy.PathWorkspaceCredentials(initProjectName)); v != nil {
			t.Errorf("a run that could not deploy the workspace's own service published its credentials node anyway: %v\n%s", v, s)
		}
	})

	t.Run("the same run publishes it once the root is up", func(t *testing.T) {
		r := rig(t)
		r.setPlanAt(t, "workspace", wsAccountChange("create"), wsGrantChange("create"))
		// The root is planned three times in one run — the read-only preview
		// pass, the apply's own plan, and the fresh plan Verify reads the apply
		// against — and the last of them finds nothing to do, which is what a
		// root that was really applied plans.
		r.script["plan@workspace"] = []any{
			map[string]any{"exit": 2}, map[string]any{"exit": 2}, map[string]any{"exit": 0},
		}
		r.save(t)
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, wsProjectName+"\n", "update", "--firebase-project", fpID, "--budget-mode", "observe")
		s := out + errOut
		if err != nil {
			t.Fatalf("update --firebase-project failed: %v\n%s", err, s)
		}
		node := wsCredsNode(t, r.db, initProjectName)
		if len(node.Members) != 1 || node.Members[0].Project != "member-b" {
			t.Errorf("the node this run published resolves %+v, want the table's one member member-b:\n%s", node.Members, s)
		}
		if node.Members[0].JobAccountRepos[0].Repos[0] != "github/owner/repo-b" {
			t.Errorf("the row's account is a job of %v, want the canonical github/owner/repo-b:\n%s", node.Members[0].JobAccountRepos, s)
		}
	})
}
