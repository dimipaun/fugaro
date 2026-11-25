package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspace_project_link_test.go is WU18's own behaviour: the two commands the
// MEMBER OWNER runs on their own machine, and the two refusals that keep the
// authority from being given by anybody else (D2800, D2819, D2822). C2's two
// seams are faked here and nothing else is: the commands are handed their
// dependencies (I1 builds them from the real WU2/WU4/WU20 code,
// workspace_wiring.go), so a test states which mechanism it is about and reads
// back what was called, in what order, with what.

// wu18Pin is the membership block `fugaro workspace add` writes: the
// membership key and the four required pin values beside it (spec §2). account
// is the workspace's own project, which D1290 names after the workspace, so
// both the pin's Firebase project and the service account are built from it —
// the member's pin is its own record of which workspace it trusts.
func wu18Pin(joined, account string) string {
	return "workspace: " + joined + "\n" +
		"workspace_pin:\n" +
		"  firebase_project: " + account + "\n" +
		"  issuer: " + localcfg.WorkspaceIssuer(account) + "\n" +
		"  assertion_key: key-of-" + account + "\n" +
		"  service_account: fugaro-workspace@" + account + ".iam.gserviceaccount.com\n"
}

// wu18Home is this machine's own state for one project config: the project's
// file with the caller's block appended, selected by $FUGARO_CONFIG so the
// command under test picks it the way a member owner's shell does.
func wu18Home(t *testing.T, project, extra string) {
	t.Helper()
	noAgentSession(t)
	noTerminal(t)
	home := t.TempDir()
	isolateProjects(t, home)
	path := writeProject(t, project, project+"-gcp")
	if extra != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte(extra)...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FUGARO_CONFIG", path)
	settingsBucketAt(t)
	// The entry the command files names the actor, and the actor is the
	// credential's own address (F21) — never the config's `user:` key, which
	// this fixture does not write at all.
	oldUser := authenticatedUser
	t.Cleanup(func() { authenticatedUser = oldUser })
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return wu18Owner, nil }
}

// wu18Owner is the address the member owner's own credential belongs to.
const wu18Owner = "owner@example.com"

// wu18Box is one run of a command under test: its two streams, kept so a fake
// can read what the command had printed when it was called — which is how a
// test pins that the report comes BEFORE the move rather than after it.
type wu18Box struct {
	out, errOut bytes.Buffer
}

func (b *wu18Box) run(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	cmd.SetOut(&b.out)
	cmd.SetErr(&b.errOut)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func (b *wu18Box) stdout() string { return b.out.String() }

// wu18Authority is C2's Authority as the commands see it. It records every call
// and answers the mechanism the test names, so a test can assert that the one
// call it means was made, that no other was, and what the command had printed
// by then.
type wu18Authority struct {
	kind   workspace.AuthorityKind
	admins []string
	// out is the command's own stdout, read when Link is called: nil reads
	// nothing.
	out func() string

	links    []workspace.MemberTarget
	unlinks  []workspace.MemberTarget
	holds    []workspace.MemberTarget
	adds     []string
	removes  []string
	atLink   string
	linkErrs []error
}

func (a *wu18Authority) Kind() workspace.AuthorityKind { return a.kind }

func (a *wu18Authority) Link(_ context.Context, m workspace.MemberTarget) error {
	a.links = append(a.links, m)
	if a.out != nil {
		a.atLink = a.out()
	}
	if len(a.linkErrs) > 0 {
		return a.linkErrs[len(a.links)-1]
	}
	return nil
}

func (a *wu18Authority) Unlink(_ context.Context, m workspace.MemberTarget) error {
	a.unlinks = append(a.unlinks, m)
	return nil
}

func (a *wu18Authority) Holds(_ context.Context, m workspace.MemberTarget) (bool, error) {
	a.holds = append(a.holds, m)
	return false, nil
}

func (a *wu18Authority) AddAdmin(_ context.Context, admin string) error {
	a.adds = append(a.adds, admin)
	return nil
}

func (a *wu18Authority) RemoveAdmin(_ context.Context, admin string) error {
	a.removes = append(a.removes, admin)
	return nil
}

func (a *wu18Authority) AdminBindings(context.Context) ([]string, error) {
	return append([]string(nil), a.admins...), nil
}

// wu18Folders is WU2's Resource Manager read as the folder report uses it: the
// parent a project hangs under now, and the owners a parent carries. Every
// other call MemberOps carries is not this command's to make, so the fake
// records it and answers a refusal rather than pretending.
type wu18Folders struct {
	parent       string
	owners       []string
	parentCalls  int
	ownersCalled []string
}

func (f *wu18Folders) Parent(_ context.Context, project string) (string, error) {
	f.parentCalls++
	return f.parent, nil
}

func (f *wu18Folders) Owners(_ context.Context, parent string) ([]string, error) {
	f.ownersCalled = append(f.ownersCalled, parent)
	return append([]string(nil), f.owners...), nil
}

// wu18Audit is one entry the command filed in the member's own trail.
type wu18Audit struct {
	id    string
	event []byte
}

// wu18Members is C2's MemberOps as these two commands use it: an audit entry is
// what they write, and every other method is a write a member owner's link must
// never make — the mirror belongs to the workspace owner's `project add` — so
// the fake records the call and fails the test that makes it.
type wu18Members struct {
	audits []wu18Audit
	other  []string
}

func (m *wu18Members) Audit(_ context.Context, target workspace.MemberTarget, id string, event []byte) error {
	m.audits = append(m.audits, wu18Audit{id: id, event: append([]byte(nil), event...)})
	return nil
}

func (m *wu18Members) elsewhere(what string) error {
	m.other = append(m.other, what)
	return nil
}

func (m *wu18Members) ReadGrants(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, m.elsewhere("ReadGrants")
}

func (m *wu18Members) WriteGrants(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	return m.elsewhere("WriteGrants")
}

func (m *wu18Members) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, m.elsewhere("ReadMirror")
}

func (m *wu18Members) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	return m.elsewhere("WriteMirror")
}

func (m *wu18Members) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return nil, m.elsewhere("Bindings")
}

func (m *wu18Members) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	return workspace.Pending{}, m.elsewhere("PendingApply")
}

func (m *wu18Members) Offboard(context.Context, workspace.MemberTarget, string) error {
	return m.elsewhere("Offboard")
}

// The member's own config is the one this machine holds for aurora, joined to
// workspace-alpha: the pin names the workspace, so the command has everything
// the member owner's own act needs and nothing from the workspace's side.
func wu18Member(t *testing.T) string {
	t.Helper()
	wu18Home(t, "aurora", wu18Pin("workspace-alpha", "workspace-alpha"))
	return "aurora-gcp"
}

// TestLinkNeedsTheMembersOwnConfig: link completes a membership THIS MACHINE's
// own config states, so a config that is not the member's own — one that joins
// no workspace, or the workspace's own project's config — is refused before
// anything is reached. The second case is the pin's: a project named after the
// workspace's own Firebase project IS the workspace (D1290), and reading only
// the membership key would refuse it for the wrong reason and link it when the
// pin happens to be written.
func TestLinkNeedsTheMembersOwnConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		extra   string
		want    string
	}{
		{
			name:    "a config that joins no workspace",
			project: "aurora",
			want:    "not a member of any workspace",
		},
		{
			name:    "the workspace's own project",
			project: "workspace-alpha",
			extra:   wu18Pin("workspace-alpha", "workspace-alpha"),
			want:    "the workspace's own project",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wu18Home(t, tc.project, tc.extra)
			auth, members, box := &wu18Authority{}, &wu18Members{}, &wu18Box{}
			err := box.run(t, newWorkspaceProjectLinkCmd(workspaceLinkDeps{Authority: auth, Members: members}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("link on %s: err = %v, want a refusal naming %q", tc.name, err, tc.want)
			}
			if len(auth.links) != 0 {
				t.Errorf("link called Authority.Link %d times on a config that is not the member's own: %v", len(auth.links), auth.links)
			}
			if len(members.audits) != 0 {
				t.Errorf("link filed %d audit entries on a config that is not the member's own", len(members.audits))
			}
		})
	}
}

// TestLinkRefusesOtherWorkspacePin: the pin is the member's own record of WHICH
// workspace it trusts, so a pin that names another workspace is not this
// member's membership however the key above it reads — and the refusal must
// come before the authority is given, because the folder it would move the
// project into is the other workspace's.
func TestLinkRefusesOtherWorkspacePin(t *testing.T) {
	wu18Home(t, "aurora", wu18Pin("workspace-alpha", "workspace-beta"))
	auth, members, box := &wu18Authority{}, &wu18Members{}, &wu18Box{}
	err := box.run(t, newWorkspaceProjectLinkCmd(workspaceLinkDeps{Authority: auth, Members: members}))
	if err == nil || !strings.Contains(err.Error(), "names another workspace") {
		t.Fatalf("link with a pin naming workspace-beta: err = %v, want a refusal that says the pin names another workspace", err)
	}
	if !strings.Contains(err.Error(), "workspace-alpha") || !strings.Contains(err.Error(), "workspace-beta") {
		t.Errorf("the refusal names neither the membership nor the pin: %v", err)
	}
	if len(auth.links) != 0 {
		t.Errorf("link called Authority.Link %d times on a pin naming another workspace", len(auth.links))
	}
}

// TestLinkRefusesInACodingAgentsSession: giving the authority is the member
// owner's own act (D2800, D2819), so an agent's session — which holds the
// owner's credentials — is refused before anything is read, and the sentence
// names the command the person runs.
func TestLinkRefusesInACodingAgentsSession(t *testing.T) {
	wu18Member(t)
	t.Setenv(agentMarkers[0], "1")
	auth, box := &wu18Authority{}, &wu18Box{}
	err := box.run(t, newWorkspaceProjectLinkCmd(workspaceLinkDeps{Authority: auth, Members: &wu18Members{}}))
	if err == nil || !strings.Contains(err.Error(), initflow.AgentRefusal(agentMarkers[0])) {
		t.Fatalf("link in an agent's session: err = %v, want the agent refusal", err)
	}
	if len(auth.links) != 0 {
		t.Errorf("an agent's session called Authority.Link %d times", len(auth.links))
	}
}

// TestLinkReportsThePolicyDifference: in the folder model the move changes what
// the project inherits, so the member owner sees it BEFORE the move — the
// acceptance's whole point — with the policies this machine can read on both
// sides named, and the ones it cannot read said to be unread rather than left
// to look like none.
func TestLinkReportsThePolicyDifference(t *testing.T) {
	wu18Member(t)
	folders := &wu18Folders{parent: "folders/77", owners: []string{"old-parent@example.com"}}
	auth := &wu18Authority{kind: workspace.AuthorityFolder, admins: []string{"admin@example.com"}}
	members := &wu18Members{}
	box := &wu18Box{}
	auth.out = box.stdout
	if err := box.run(t, newWorkspaceProjectLinkCmd(workspaceLinkDeps{Authority: auth, Members: members, Folders: folders})); err != nil {
		t.Fatal(err)
	}
	out := box.stdout()
	for _, want := range []string{"folders/77", "old-parent@example.com", "admin@example.com", "organization policies"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %q:\n%s", want, out)
		}
	}
	if folders.parentCalls != 1 {
		t.Errorf("the project's parent was read %d times, want once: the old parent is read before the move", folders.parentCalls)
	}
	if len(folders.ownersCalled) != 1 || folders.ownersCalled[0] != "folders/77" {
		t.Errorf("the owners read were %v, want the old parent's (folders/77) and only it", folders.ownersCalled)
	}
	if !strings.Contains(auth.atLink, "folders/77") {
		t.Errorf("the report was not printed before the move: Authority.Link saw\n%q", auth.atLink)
	}
	if len(auth.links) != 1 {
		t.Fatalf("Authority.Link was called %d times, want once", len(auth.links))
	}
	if got, want := auth.links[0], (workspace.MemberTarget{Project: "aurora-gcp"}); got != want {
		t.Errorf("Authority.Link was called with %+v, want %+v", got, want)
	}
	if len(members.audits) != 1 {
		t.Fatalf("link filed %d member audit entries, want exactly one", len(members.audits))
	}
	if len(members.other) != 0 {
		t.Errorf("link reached the member beyond the audit entry: %v (the mirror is the workspace owner's `project add`)", members.other)
	}
	// The entry is C1's own event, which is the shape the member's trail holds
	// and the shape its readers read it back with (contract.md, "The audit
	// event"): an entry in any other shape is one `status` and the drift check
	// refuse, so it is filed through the same reader they use.
	id, event := members.audits[0].id, members.audits[0].event
	filed, err := workspace.DecodeAuditEvent(event)
	if err != nil {
		t.Fatalf("the audit entry link filed is not an event this program reads back: %v\n%s", err, event)
	}
	if filed.ID != id {
		t.Errorf("the entry is filed under %q and its own id is %q: the id is the trail's key", id, filed.ID)
	}
	if filed.Action != workspace.ActionProjectLink {
		t.Errorf("the audit entry's action is %q, want %q", filed.Action, workspace.ActionProjectLink)
	}
	if filed.Actor != wu18Owner {
		t.Errorf("the audit entry's actor is %q, want the credential's own address %q", filed.Actor, wu18Owner)
	}
	if filed.Project != "aurora-gcp" {
		t.Errorf("the audit entry's project is %q, want the member the change was about (aurora-gcp)", filed.Project)
	}
}

// TestLinkOnTheSAMechanismMovesNothing: the SA model grants the account on the
// project where it stands (D2803), so there is no policy difference to report
// and the command says so rather than reading a parent nothing will move.
func TestLinkOnTheSAMechanismMovesNothing(t *testing.T) {
	wu18Member(t)
	folders := &wu18Folders{parent: "folders/77"}
	auth := &wu18Authority{kind: workspace.AuthoritySA}
	box := &wu18Box{}
	if err := box.run(t, newWorkspaceProjectLinkCmd(workspaceLinkDeps{Authority: auth, Members: &wu18Members{}, Folders: folders})); err != nil {
		t.Fatal(err)
	}
	if folders.parentCalls != 0 {
		t.Errorf("the SA mechanism read a parent %d times, want none: no project moves", folders.parentCalls)
	}
	if len(auth.links) != 1 {
		t.Errorf("Authority.Link was called %d times, want once", len(auth.links))
	}
}

// TestUnlinkDropsOnlyTheAuthority: D2822's line at the command a member owner
// runs — the authority is dropped, the workspace admins' stored grants stay in
// this member's own roster until the workspace project's owner removes them,
// and the sentence says which command does that.
func TestUnlinkDropsOnlyTheAuthority(t *testing.T) {
	wu18Member(t)
	auth := &wu18Authority{kind: workspace.AuthorityFolder, admins: []string{"admin@example.com"}}
	members := &wu18Members{}
	box := &wu18Box{}
	if err := box.run(t, newWorkspaceProjectUnlinkCmd(workspaceLinkDeps{Authority: auth, Members: members})); err != nil {
		t.Fatal(err)
	}
	if len(auth.unlinks) != 1 {
		t.Fatalf("Authority.Unlink was called %d times, want once", len(auth.unlinks))
	}
	if got, want := auth.unlinks[0], (workspace.MemberTarget{Project: "aurora-gcp"}); got != want {
		t.Errorf("Authority.Unlink was called with %+v, want %+v", got, want)
	}
	if len(auth.links) != 0 || len(auth.adds) != 0 || len(auth.removes) != 0 {
		t.Errorf("unlink touched more than the authority: links %v, adds %v, removes %v", auth.links, auth.adds, auth.removes)
	}
	if len(members.other) != 0 {
		t.Errorf("unlink wrote the member beyond the audit entry: %v", members.other)
	}
	if len(members.audits) != 1 {
		t.Fatalf("unlink filed %d member audit entries, want exactly one", len(members.audits))
	}
	if filed, err := workspace.DecodeAuditEvent(members.audits[0].event); err != nil || filed.Action != workspace.ActionProjectUnlink {
		t.Errorf("the audit entry is not an event naming %q that this program reads back (err %v): %s", workspace.ActionProjectUnlink, err, members.audits[0].event)
	}
	out := box.stdout()
	if !strings.Contains(out, "fugaro workspace project rm aurora") {
		t.Errorf("unlink does not say how the stored grants go (D2822):\n%s", out)
	}
	if !strings.Contains(out, "grants") {
		t.Errorf("unlink does not say the workspace admins' stored grants stay:\n%s", out)
	}
}

// TestUnlinkRefusesOtherWorkspacePin: the two commands make one consent, so
// they are refused by one rule: a config that is not the member's own gives no
// authority to drop, and a pin naming another workspace is not this member's
// membership.
func TestUnlinkRefusesOtherWorkspacePin(t *testing.T) {
	wu18Home(t, "aurora", wu18Pin("workspace-alpha", "workspace-beta"))
	auth, box := &wu18Authority{}, &wu18Box{}
	err := box.run(t, newWorkspaceProjectUnlinkCmd(workspaceLinkDeps{Authority: auth, Members: &wu18Members{}}))
	if err == nil || !strings.Contains(err.Error(), "names another workspace") {
		t.Fatalf("unlink with a pin naming workspace-beta: err = %v, want a refusal that says the pin names another workspace", err)
	}
	if len(auth.unlinks) != 0 {
		t.Errorf("unlink called Authority.Unlink %d times on a pin naming another workspace", len(auth.unlinks))
	}
}
