package cli

// workspace_wiring_test.go is I1's own two gates: the R11 linked-member list —
// the member table filtered by what the mechanism really holds, which is what
// D2818's stored grant is written into — and the order the member list is read
// in (A7, D1590: a write of this family reads no member before its own
// agent-session refusal).
//
// Both are hermetic: a roster table and a mechanism that answers per project, no
// cloud, no credentials.

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// TestLinkedMembersKeepsOnlyWhatTheWorkspaceHolds: the members a stored grant
// reaches are the ones the mechanism holds NOW (R11) — the member owner's own
// `project link` is the consent D2819 makes it stand for — and the member
// table is only the workspace's memory of what it once declared. A member whose
// owner never linked it is dropped, so `workspace user add --admin` writes no
// grant into a member the workspace does not hold.
func TestLinkedMembersKeepsOnlyWhatTheWorkspaceHolds(t *testing.T) {
	table := map[string]localcfg.WorkspaceMember{
		"linked":   {FirebaseProject: "member-linked"},
		"unlinked": {FirebaseProject: "member-unlinked"},
	}
	auth := &wu7Authority{
		kind:  workspace.AuthorityFolder,
		holds: map[string]bool{"member-linked": true},
	}
	side := &workspaceOwnerSide{
		name: "workspace-alpha", out: io.Discard, auth: auth, resolved: true,
		lc: &localcfg.Config{WorkspaceMembers: &table},
	}

	got, err := workspaceLinkedMembers(context.Background(), side)
	if err != nil {
		t.Fatalf("reading the members workspace-alpha holds: %v", err)
	}
	want := []workspace.MemberTarget{{Project: "member-linked"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("the members the workspace holds are %+v, want %+v: a member the mechanism answers Holds=false for is a project nobody linked, and it is not a member a stored grant may be written into (R11, D2819)", got, want)
	}
}

// TestFolderInFolderIsWhatHoldsMeans: the folder mechanism's `Holds` reduces
// WU2's FolderHold to the yes or no C2 asks for, and the yes is the member's
// project sitting in THIS workspace's folder (R11). A project under another
// parent — another folder, an organization, or no parent at all — is not held,
// whatever record the member's own database still carries, and neither is any
// project for a workspace whose init has not produced a folder yet.
func TestFolderInFolderIsWhatHoldsMeans(t *testing.T) {
	folders := wiringFolders{parent: map[string]string{
		"member-alpha":  "folders/123456",
		"member-beta":   "organizations/5678",
		"member-gamma":  "",
		"member-delta":  "folders/999999",
		"member-absent": "",
	}}
	side := workspaceFolderAuthority{FolderAuthority: &workspace.FolderAuthority{Folder: "folders/123456", CRM: folders}}
	ctx := context.Background()
	for _, tc := range []struct {
		project string
		want    bool
		why     string
	}{
		{"member-alpha", true, "the project sits in the workspace's own folder"},
		{"member-beta", false, "the project sits under an organization, which is not this workspace's folder"},
		{"member-gamma", false, "the project is under no parent to hold"},
		{"member-delta", false, "the project sits in another folder"},
		{"member-absent", false, "the project is not under this workspace's folder"},
	} {
		held, err := side.Holds(ctx, workspace.MemberTarget{Project: tc.project})
		if err != nil {
			t.Fatalf("reading where %s stands: %v", tc.project, err)
		}
		if held != tc.want {
			t.Errorf("%s is held=%v, want %v: %s (R11)", tc.project, held, tc.want, tc.why)
		}
	}

	// A workspace whose init has not run has no folder, and it holds nothing:
	// Holds answers where the project stands either way, but the answer is the
	// workspace's folder that has to match, and an empty one matches no project.
	none := workspaceFolderAuthority{FolderAuthority: &workspace.FolderAuthority{CRM: folders}}
	if held, err := none.Holds(ctx, workspace.MemberTarget{Project: "member-alpha"}); err != nil || held {
		t.Errorf("a workspace with no folder yet answered held=%v, err=%v, want false and no error (D2803: the folder is created by `fugaro workspace init`, and until it is, no member is held)", held, err)
	}
}

// TestWorkspaceUserAddReadsNoMemberBeforeItsRefusal drives the REAL wiring — the
// opener `newWorkspaceCmd` hands `workspace user` — over a config home that
// declares a member, in a coding agent's session. The refusal has to be the
// answer: resolving the members the workspace holds reads the workspace's roster
// node and asks the mechanism about every member, so an opener that resolved
// them would answer a cloud error — with no credentials, the error rather than
// the refusal — for a command that writes nothing (A7, D1590).
func TestWorkspaceUserAddReadsNoMemberBeforeItsRefusal(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	t.Setenv(agentMarkers[0], "1")

	_, _, err := execute(t, "workspace", "user", "add", "ada@example.com", "--admin", "--workspace", "workspace-alpha")
	if err == nil || !strings.Contains(err.Error(), "coding agent's session") {
		t.Fatalf("`workspace user add --admin` in a coding agent's session: err = %v, want the refusal that comes before anything is read (A7). A cloud error here is the members being resolved before the fence, which is what the opener must not do", err)
	}
}

// wiringFolders is the Resource Manager read a folder hold makes: where each
// project stands. The surface is WU2's, and the call `Holds` must not make goes
// to the embedded nil interface and panics there rather than answering
// something invented — the other six methods are `link`'s and `unlink`'s and
// belong to their own tests.
type wiringFolders struct {
	workspace.Folders
	parent map[string]string
}

func (f wiringFolders) Parent(_ context.Context, project string) (string, error) {
	return f.parent[project], nil
}

// TestWorkspaceWritesReadNoMemberBeforeTheirFence: the members the workspace
// holds are resolved when a command's body needs them and not when the
// workspace is opened, because the resolution is a roster read plus one
// mechanism call per member. A coding agent's session therefore meets its
// refusal (A7, D1590) instead of a cloud read it has no credentials for, and
// the commands that never need the list — `workspace domains add|rm`, and an
// `add` that sets no admin — make no such call at all.
func TestWorkspaceWritesReadNoMemberBeforeTheirFence(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People:  []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}}

	w.marker = "1"
	w.refuses([]string{"coding agent's session", "run this in your own terminal"},
		"workspace", "user", "add", "bob@other.test", "--admin", "--workspace", "workspace-alpha")
	w.refuses([]string{"coding agent's session", "run this in your own terminal"},
		"workspace", "domains", "add", "other.test", "--workspace", "workspace-alpha")
	if w.linkedCalls != 0 {
		t.Errorf("a coding agent's session resolved the member list %d time(s): the refusal comes before anything is read (A7), and resolving the list is a roster read and a mechanism call per member", w.linkedCalls)
	}

	w.marker = ""
	w.must("workspace", "domains", "add", "other.test", "--workspace", "workspace-alpha")
	w.must("workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	if w.linkedCalls != 0 {
		t.Errorf("the member list was resolved %d time(s) by a domains edit and an add that sets no admin: neither writes a stored grant, and D2818's list is read at the point a command uses it", w.linkedCalls)
	}

	// The one command that does write the grants reads the list, and reads it
	// once: it reports the count it reached and writes one grant per member.
	w.must("workspace", "user", "add", "ada@example.com", "--admin", "--workspace", "workspace-alpha")
	if w.linkedCalls != 1 {
		t.Errorf("`workspace user add --admin` resolved the member list %d time(s), want 1: its report and the grant it writes into each member are one list, read once", w.linkedCalls)
	}
}
