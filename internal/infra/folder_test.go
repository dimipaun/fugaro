package infra

// The folder calls (WU2) against the Resource Manager fake: a create polled to
// its end, a folder's display name and parent read back, the roles/owner
// binding read, added and removed without disturbing the folder's other
// bindings, the organizations a credential can see, and a project's parent
// read and moved. The fake's v1 project policy write — added here for the
// end-to-end test that drives it — is pinned too.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The folder surface is what the folder authority takes (WU2's Folders
// interface): the satisfaction is pinned here, in a test, because the two
// packages are kept apart in production — the dependency between them would run
// through internal/projectpolicy, which imports internal/workspace — and the
// interface is what lets I1 hand *Folders in with no adapter.
var _ workspace.Folders = (*Folders)(nil)

// wu2FolderFake is a Resource Manager fake and the folder surface over it,
// built on the clients and the short wait the project tests already wire (the
// caller's own credentials against a fake root).
func wu2FolderFake(t *testing.T) (*gcpfake.CRM, *Folders) {
	t.Helper()
	f := newProjectFakes(t)
	return f.crm, NewFolders(f.pc.CRM)
}

// TestFoldersCreateGetAndOrganizations: a create is polled to its end and
// answers the folder it made, which reads back as the display name and the
// parent it was created with, and organizations.search answers what the
// credential can see (how `workspace init` tells whether a workspace can use a
// folder at all).
func TestFoldersCreateGetAndOrganizations(t *testing.T) {
	ctx := context.Background()
	f, fs := wu2FolderFake(t)
	f.AddOrganization("123")
	f.PendingPolls = 2

	folder, err := fs.Create(ctx, "fugaro-workspace-aurora", "organizations/123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := fmt.Sprintf("folders/%d", gcpfake.FirstCreatedFolderNumber); folder != want {
		t.Errorf("Create = %q, want %q", folder, want)
	}
	name, parent, err := fs.Get(ctx, folder)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if name != "fugaro-workspace-aurora" || parent != "organizations/123" {
		t.Errorf("Get = %q, %q, want the name and parent the folder was created with", name, parent)
	}
	orgs, err := fs.Organizations(ctx)
	if err != nil {
		t.Fatalf("Organizations: %v", err)
	}
	if !slices.Equal(orgs, []string{"organizations/123"}) {
		t.Errorf("Organizations = %v, want organizations/123", orgs)
	}

	// A name or a parent the API would refuse is refused before any call is
	// made: the create count stays where it is.
	for _, tc := range []struct{ name, display, parent string }{
		{"a name of 31 characters", strings.Repeat("a", 31), "organizations/123"},
		{"a name that starts with a hyphen", "-aurora", "organizations/123"},
		{"an empty name", "", "organizations/123"},
		{"a parent that is not an organization or a folder", "aurora", "projects/1"},
		{"no parent", "aurora", ""},
	} {
		if _, err := fs.Create(ctx, tc.display, tc.parent); err == nil {
			t.Errorf("Create(%s) = nil, want a refusal before the call", tc.name)
		}
	}
	if got := len(f.Creates()); got != 0 {
		t.Errorf("the refused creates sent %d create(s)", got)
	}
}

// TestFoldersOwnersAddedAndRemoved: the owner binding is read, added and
// removed, and every other binding of the folder is carried through the write
// untouched. A member Google marks deleted is no longer an owner.
func TestFoldersOwnersAddedAndRemoved(t *testing.T) {
	ctx := context.Background()
	f, fs := wu2FolderFake(t)
	f.AddFolder("9", "fugaro-workspace-aurora", "organizations/123")
	f.SetFolderPolicy("9",
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"user:viewer@example.com"}},
		gcpfake.Binding{Role: FolderOwnerRole, Members: []string{"user:founder@example.com", "deleted:user:gone@example.com"}})

	owners, err := fs.Owners(ctx, "folders/9")
	if err != nil {
		t.Fatalf("Owners: %v", err)
	}
	if !slices.Equal(owners, []string{"user:founder@example.com"}) {
		t.Errorf("Owners = %v, want the owner the folder had, with the deleted member left out", owners)
	}
	for range 2 {
		if err := fs.AddOwner(ctx, "folders/9", "user:ada@example.com"); err != nil {
			t.Fatalf("AddOwner: %v", err)
		}
	}
	if got, _ := fs.Owners(ctx, "folders/9"); !slices.Equal(got, []string{"user:founder@example.com", "user:ada@example.com"}) {
		t.Errorf("Owners = %v, want ada added once", got)
	}
	if err := fs.RemoveOwner(ctx, "folders/9", "user:ada@example.com"); err != nil {
		t.Fatalf("RemoveOwner: %v", err)
	}
	pol := f.FolderPolicy("9")
	if len(pol) != 2 || pol[0].Role != "roles/viewer" || !slices.Equal(pol[0].Members, []string{"user:viewer@example.com"}) {
		t.Errorf("the folder's policy is %+v, want the viewer binding carried through the owner writes", pol)
	}
	// The write carries the binding back whole, so the member Google marked
	// deleted is kept in the policy: an owner write changes who it is about and
	// nothing else.
	if !slices.Equal(pol[1].Members, []string{"user:founder@example.com", "deleted:user:gone@example.com"}) {
		t.Errorf("the owner binding is %+v, want the founder kept, ada dropped, and nothing else disturbed", pol[1])
	}

	// A folder the caller cannot read is refused with the API's own words.
	if _, err := fs.Owners(ctx, "folders/nope"); err == nil {
		t.Error("Owners of a folder that does not exist = nil, want the API's refusal")
	}
}

// TestFoldersParentAndMove: a project's parent is read, a move is polled to its
// end and the read after it answers the destination, and a destination the API
// would refuse is refused before any call is made.
func TestFoldersParentAndMove(t *testing.T) {
	ctx := context.Background()
	f, fs := wu2FolderFake(t)
	f.AddFolder("9", "fugaro-workspace-aurora", "organizations/123")
	f.AddProject("member-a", 900000000123)
	f.SetParent("member-a", "organizations/123")
	f.PendingPolls = 1

	parent, err := fs.Parent(ctx, "member-a")
	if err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if parent != "organizations/123" {
		t.Errorf("Parent = %q, want organizations/123", parent)
	}
	if err := fs.Move(ctx, "member-a", "folders/9"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got := f.ProjectParent("member-a"); got != "folders/9" {
		t.Errorf("the project is at %q, want the destination the move named", got)
	}
	if got := f.Moves(); len(got) != 1 || got[0] != (gcpfake.ProjectMove{Project: "member-a", Parent: "folders/9"}) {
		t.Errorf("Moves = %+v, want the one move made", got)
	}
	if parent, err = fs.Parent(ctx, "member-a"); err != nil || parent != "folders/9" {
		t.Errorf("Parent after the move = %q, %v, want folders/9", parent, err)
	}

	if err := fs.Move(ctx, "member-a", "projects/1"); err == nil {
		t.Error("Move to a destination that is not an organization or a folder = nil, want a refusal before the call")
	}
	if got := f.Moves(); len(got) != 1 {
		t.Errorf("the refused move sent %d move(s)", len(got)-1)
	}
}

// TestFoldersOperationError: an operation that ends in an error is that error,
// with the API's own message and code kept, and nothing is left behind — the
// folder a refused create had made is gone.
func TestFoldersOperationError(t *testing.T) {
	ctx := context.Background()
	f, fs := wu2FolderFake(t)
	f.FailOperation(9, "the organization policy forbids this folder here")

	_, err := fs.Create(ctx, "fugaro-workspace-aurora", "organizations/123")
	if err == nil {
		t.Fatal("Create under a failing operation = nil, want the operation's error")
	}
	for _, want := range []string{"the organization policy forbids this folder here", "code 9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not carry %q", err, want)
		}
	}
	if _, _, err := fs.Get(ctx, fmt.Sprintf("folders/%d", gcpfake.FirstCreatedFolderNumber)); err == nil {
		t.Error("the folder a refused create made is still there, want the create to have left nothing behind")
	}
}

// TestFoldersOperationPollFailure: a read of an operation that fails is that
// read's own error, all the way out of Create and Move — the operation the
// caller never got is not read for its name.
func TestFoldersOperationPollFailure(t *testing.T) {
	ctx := context.Background()
	f, fs := wu2FolderFake(t)
	f.AddProject("member-a", 900000000123)
	f.RefusePath("/v3/operations/", 503, "UNAVAILABLE", "UNAVAILABLE", "the service is unavailable")

	if _, err := fs.Create(ctx, "fugaro-workspace-aurora", "organizations/123"); err == nil {
		t.Error("Create = nil, want the operation read's error")
	} else if !strings.Contains(err.Error(), "the service is unavailable") {
		t.Errorf("Create = %v, want the operation read's own error", err)
	}
	if err := fs.Move(ctx, "member-a", "folders/9"); err == nil {
		t.Error("Move = nil, want the operation read's error")
	} else if !strings.Contains(err.Error(), "the service is unavailable") {
		t.Errorf("Move = %v, want the operation read's own error", err)
	}
}

// TestFolderFakeServesTheProjectPolicyWrite: the v1 projects.setIamPolicy the
// fake gains with this task (the plan puts it in crm.go, whose only writer this
// task is, for the end-to-end test WU21 drives against it), pinned here so the
// surface another level leans on is exercised by the task that added it.
func TestFolderFakeServesTheProjectPolicyWrite(t *testing.T) {
	ctx := context.Background()
	f, _ := wu2FolderFake(t)
	f.AddProject("member-a", 900000000123)
	svc, err := crm.NewService(ctx, option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	want := &crm.Policy{Bindings: []*crm.Binding{{Role: "roles/viewer", Members: []string{"user:ada@example.com"}}}}
	if _, err := svc.Projects.SetIamPolicy("member-a", &crm.SetIamPolicyRequest{Policy: want}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}
	got, err := svc.Projects.GetIamPolicy("member-a", &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/viewer" ||
		!slices.Equal(got.Bindings[0].Members, []string{"user:ada@example.com"}) {
		t.Errorf("the project's policy is %+v, want the binding that was written", got.Bindings)
	}
}

// TestValidateFolderName: the display-name grammar the API documents, held
// before any call is made.
func TestValidateFolderName(t *testing.T) {
	for name, ok := range map[string]bool{
		"aurora":                     true,
		"fugaro-workspace-aurora":    true,
		"Fugaro Workspace 2":         true,
		"a":                          true,
		"fugaro_workspace":           true,
		strings.Repeat("a", 30):      true,
		"":                           false,
		"-aurora":                    false,
		"aurora-":                    false,
		"_aurora":                    false,
		"aurora's":                   false,
		"aurora/2":                   false,
		strings.Repeat("a", 31):      false,
		"fugaro-workspace-aurora 2 ": false,
	} {
		if err := ValidateFolderName(name); (err == nil) != ok {
			t.Errorf("ValidateFolderName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
}
