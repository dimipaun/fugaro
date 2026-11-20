package workspace

// The folder authority (WU2), tested against a fake Resource Manager and a
// fake member database: the link that moves a member's projects into the
// workspace's folder and records where they came from (R10, R14), the refusal
// of a project that sits in another workspace's folder (R2), the unlink that
// puts them back and the refusal of a record a member's own admin could have
// rewritten (WU0), the folder's owner binding (D2804, D2818) and the create
// `workspace init` makes (WU24).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// wu2Folders is the Resource Manager surface the authority is tested against:
// an in-memory organization of projects and folders, one IAM policy per folder,
// and the moves the authority made, in the order it made them. It is this
// task's fake (WU2), so what a link did is what a test can read back.
type wu2Folders struct {
	parents map[string]string   // a project or a folder -> its parent
	folders map[string]string   // a folder -> its display name
	owners  map[string][]string // a folder -> the members bound to roles/owner
	moves   []string            // "project to parent", as Move made them
	next    int
}

func wu2Cloud() *wu2Folders {
	return &wu2Folders{parents: map[string]string{}, folders: map[string]string{}, owners: map[string][]string{}}
}

// folder adds a folder to the cloud. A parent left empty is a folder directly
// under an organization.
func (f *wu2Folders) folder(id, displayName, parent string) {
	f.folders[id] = displayName
	f.parents[id] = parent
}

// project adds a project at parent, which may be empty: a project under no
// parent is one created outside any organization.
func (f *wu2Folders) project(id, parent string) { f.parents[id] = parent }

func (f *wu2Folders) Create(_ context.Context, displayName, parent string) (string, error) {
	f.next++
	id := fmt.Sprintf("folders/%d", 900+f.next)
	f.folder(id, displayName, parent)
	return id, nil
}

func (f *wu2Folders) Get(_ context.Context, folder string) (string, string, error) {
	name, ok := f.folders[folder]
	if !ok {
		return "", "", fmt.Errorf("wu2fake: no folder %s", folder)
	}
	return name, f.parents[folder], nil
}

func (f *wu2Folders) Owners(_ context.Context, folder string) ([]string, error) {
	return slices.Clone(f.owners[folder]), nil
}

func (f *wu2Folders) AddOwner(_ context.Context, folder, member string) error {
	if !slices.Contains(f.owners[folder], member) {
		f.owners[folder] = append(f.owners[folder], member)
	}
	return nil
}

func (f *wu2Folders) RemoveOwner(_ context.Context, folder, member string) error {
	f.owners[folder] = slices.DeleteFunc(f.owners[folder], func(o string) bool { return o == member })
	return nil
}

func (f *wu2Folders) Parent(_ context.Context, project string) (string, error) {
	parent, ok := f.parents[project]
	if !ok {
		return "", fmt.Errorf("wu2fake: no project %s", project)
	}
	return parent, nil
}

// Move records the move and puts the project at parent, as the real call does
// once its operation finishes: a test reads the moves back and the parent the
// next Holds or Link reads is the one the move left.
func (f *wu2Folders) Move(_ context.Context, project, parent string) error {
	if _, ok := f.parents[project]; !ok {
		return fmt.Errorf("wu2fake: no project %s", project)
	}
	f.parents[project] = parent
	f.moves = append(f.moves, project+" to "+parent)
	return nil
}

// wu2DB is the member's own database, where the link record lives (R14): the
// reads and writes are the member owner's own, over IAM.
func wu2DB(t *testing.T) *rtdb.Client {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "wu2-operator-token" }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// wu2Record is the record the member's database holds now, read the way the
// authority reads it.
func wu2Record(t *testing.T, db *rtdb.Client) (FolderLinkRecord, bool) {
	t.Helper()
	var raw json.RawMessage
	found, err := db.Get(t.Context(), PathWorkspaceLink, &raw)
	if err != nil {
		t.Fatalf("reading %s: %v", PathWorkspaceLink, err)
	}
	if !found {
		return FolderLinkRecord{}, false
	}
	rec, err := DecodeFolderLink(raw)
	if err != nil {
		t.Fatalf("decoding %s: %v", PathWorkspaceLink, err)
	}
	return rec, true
}

// wu2WriteRecord puts a record at the node by hand, as a member's budget admin
// rewriting it would (WU0).
func wu2WriteRecord(t *testing.T, db *rtdb.Client, rec FolderLinkRecord) {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	etag, found, err := db.GetETag(t.Context(), PathWorkspaceLink, nil)
	if err != nil {
		t.Fatalf("reading the etag of %s: %v", PathWorkspaceLink, err)
	}
	if found {
		t.Fatalf("%s already holds a record", PathWorkspaceLink)
	}
	if err := db.PutIfMatch(t.Context(), PathWorkspaceLink, etag, json.RawMessage(raw)); err != nil {
		t.Fatalf("writing %s: %v", PathWorkspaceLink, err)
	}
}

// wu2Linked is a cloud and an authority with one member linked: the state the
// unlink tests start from.
func wu2Linked(t *testing.T) (*wu2Folders, *rtdb.Client, *FolderAuthority) {
	t.Helper()
	cloud, db := wu2Cloud(), wu2DB(t)
	cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
	cloud.project("member-a", "organizations/123")
	a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}
	if err := a.Link(t.Context(), FolderMember{Project: "member-a"}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	return cloud, db, a
}

// TestFolderLinkMovesTheFirebaseProjectToo: a member whose Firebase project is
// a project of its own (F2, R10) is moved whole — both projects, each recorded
// at the parent it came from, so the one unlink puts both back. A member whose
// Firebase project is its GCP project is one move and a record that names no
// second project.
func TestFolderLinkMovesTheFirebaseProjectToo(t *testing.T) {
	ctx := t.Context()
	cloud, db := wu2Cloud(), wu2DB(t)
	cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
	cloud.project("member-a", "organizations/123")
	cloud.project("member-a-fb", "organizations/123")
	a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}

	m := FolderMember{Project: "member-a", Firebase: "member-a-fb"}
	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link: %v", err)
	}
	for _, p := range []string{"member-a", "member-a-fb"} {
		if got := cloud.parents[p]; got != "folders/9" {
			t.Errorf("%s is at %q, want folders/9: the member's Firebase project moves into the workspace's folder with it (R10)", p, got)
		}
	}
	if want := []string{"member-a to folders/9", "member-a-fb to folders/9"}; !slices.Equal(cloud.moves, want) {
		t.Errorf("moves %v, want %v", cloud.moves, want)
	}
	rec, found := wu2Record(t, db)
	if !found {
		t.Fatalf("Link wrote no record at %s", PathWorkspaceLink)
	}
	if want := (FolderLinkRecord{Project: "member-a", Parent: "organizations/123",
		Firebase: "member-a-fb", FirebaseParent: "organizations/123"}); rec != want {
		t.Errorf("the record at %s is %+v, want %+v: where each project came from is recorded (R14)", PathWorkspaceLink, rec, want)
	}

	// A member already in the folder is already linked: a second link does
	// nothing and disturbs neither the record nor the cloud.
	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("a second Link of a member already in the folder: %v", err)
	}
	if got := len(cloud.moves); got != 2 {
		t.Errorf("the second Link made %d move(s), want the two of the first", got-2)
	}
	if after, _ := wu2Record(t, db); after != rec {
		t.Errorf("the second Link rewrote the record as %+v", after)
	}

	var out bytes.Buffer
	a.Out = &out
	if err := a.Unlink(ctx, m); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	for _, p := range []string{"member-a", "member-a-fb"} {
		if got := cloud.parents[p]; got != "organizations/123" {
			t.Errorf("%s is at %q after the unlink, want organizations/123", p, got)
		}
	}
	if _, found := wu2Record(t, db); found {
		t.Errorf("%s still holds a record after the unlink that used it", PathWorkspaceLink)
	}
	if !strings.Contains(out.String(), "organizations/123") {
		t.Errorf("Unlink printed %q, want the parent it returns the project to", out.String())
	}

	// A member whose Firebase project is its GCP project is one move and one
	// parent recorded, with no second project named.
	cloud.project("member-b", "organizations/123")
	if err := a.Link(ctx, FolderMember{Project: "member-b", Firebase: "member-b"}); err != nil {
		t.Fatalf("Link of a member whose Firebase project is its own: %v", err)
	}
	if want := "member-b to folders/9"; cloud.moves[len(cloud.moves)-1] != want {
		t.Errorf("the last move is %q, want %q", cloud.moves[len(cloud.moves)-1], want)
	}
	if rec, _ := wu2Record(t, db); rec.Firebase != "" || rec.FirebaseParent != "" {
		t.Errorf("the record is %+v, want no Firebase project named for a member whose Firebase project is its own", rec)
	}
}

// TestFolderLinkRefusesOtherWorkspaceFolder: R2 — a project belongs to at most
// one workspace, so a project that sits in another workspace's folder, or in a
// folder under one, is refused rather than moved. The refusal names the folder
// it found, and a refused link leaves the cloud and the database alone.
func TestFolderLinkRefusesOtherWorkspaceFolder(t *testing.T) {
	for _, tc := range []struct {
		name, parent, above string
	}{
		{"in another workspace's folder", "folders/8", "folders/8"},
		{"in a folder under another workspace's folder", "folders/7", "folders/8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloud, db := wu2Cloud(), wu2DB(t)
			cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
			cloud.folder("folders/8", WorkspaceFolderName("borealis"), "organizations/123")
			cloud.folder("folders/7", "team-a", "folders/8")
			cloud.project("member-a", tc.parent)
			a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}

			err := a.Link(t.Context(), FolderMember{Project: "member-a"})
			if err == nil {
				t.Fatalf("Link moved a project out of %s, another workspace's folder", tc.above)
			}
			for _, want := range []string{tc.above, "fugaro workspace project rm member-a"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %q", err, want)
				}
			}
			if len(cloud.moves) != 0 {
				t.Errorf("the refused link made the move(s) %v", cloud.moves)
			}
			if _, found := wu2Record(t, db); found {
				t.Errorf("the refused link wrote %s", PathWorkspaceLink)
			}
		})
	}
}

// TestFolderUnlinkRestoresParent: the unlink moves the project back to the
// parent its own record names, prints where it goes, drops the record, and
// Holds reports where the project stands on either side of it.
func TestFolderUnlinkRestoresParent(t *testing.T) {
	ctx := t.Context()
	cloud, db, a := wu2Linked(t)
	var out bytes.Buffer
	a.Out = &out

	hold, err := a.Holds(ctx, FolderMember{Project: "member-a"})
	if err != nil {
		t.Fatalf("Holds: %v", err)
	}
	if hold.Parent != "folders/9" || !hold.InFolder {
		t.Errorf("Holds = %+v, want the project in folders/9 and reported as in the folder", hold)
	}

	m := FolderMember{Project: "member-a"}
	if err := a.Unlink(ctx, m); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if want := []string{"member-a to folders/9", "member-a to organizations/123"}; !slices.Equal(cloud.moves, want) {
		t.Errorf("moves %v, want %v", cloud.moves, want)
	}
	if got := cloud.parents["member-a"]; got != "organizations/123" {
		t.Errorf("member-a is at %q, want the parent the record names", got)
	}
	if _, found := wu2Record(t, db); found {
		t.Errorf("%s still holds the record the unlink used", PathWorkspaceLink)
	}
	if want := "returning member-a to organizations/123 and no Firebase project of its own\n"; out.String() != want {
		t.Errorf("Unlink printed %q, want %q", out.String(), want)
	}

	hold, err = a.Holds(ctx, m)
	if err != nil {
		t.Fatalf("Holds after the unlink: %v", err)
	}
	if hold.Parent != "organizations/123" || hold.InFolder {
		t.Errorf("Holds = %+v, want the project back at organizations/123 and not in the folder", hold)
	}

	// Nothing is recorded any more, so a second unlink refuses rather than
	// moving the project somewhere nobody recorded.
	if err := a.Unlink(ctx, m); err == nil || !strings.Contains(err.Error(), "nothing is recorded") {
		t.Errorf("a second Unlink = %v, want the refusal that nothing is recorded", err)
	}
	if got := len(cloud.moves); got != 2 {
		t.Errorf("the refused second Unlink made %d move(s)", got-2)
	}
}

// TestFolderUnlinkRefusesATamperedParent: the record is a member-controlled
// value — the member's own budget admins can rewrite it (WU0) — so a recorded
// parent inside ANY workspace's folder, this one's included, is refused rather
// than followed. The worst a rewrite achieves is a refusal. A record that
// names another project, or that cannot be read at all, is refused on the same
// terms: nothing moves, and the record stays for a person to correct.
func TestFolderUnlinkRefusesATamperedParent(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		rec        FolderLinkRecord
		member     FolderMember
	}{
		{"a parent in another workspace's folder",
			"folders/8",
			FolderLinkRecord{Project: "member-a", Parent: "folders/8"},
			FolderMember{Project: "member-a"}},
		{"a parent in this workspace's own folder",
			"folders/9",
			FolderLinkRecord{Project: "member-a", Parent: "folders/9"},
			FolderMember{Project: "member-a"}},
		{"a Firebase parent in another workspace's folder",
			"folders/8",
			FolderLinkRecord{Project: "member-a", Parent: "organizations/123",
				Firebase: "member-a-fb", FirebaseParent: "folders/8"},
			FolderMember{Project: "member-a", Firebase: "member-a-fb"}},
		{"a record that names another project",
			"member-b",
			FolderLinkRecord{Project: "member-b", Parent: "organizations/123"},
			FolderMember{Project: "member-a"}},
		{"a record that names another Firebase project",
			"member-b-fb",
			FolderLinkRecord{Project: "member-a", Parent: "organizations/123",
				Firebase: "member-b-fb", FirebaseParent: "organizations/123"},
			FolderMember{Project: "member-a", Firebase: "member-a-fb"}},
		{"a record with no parent at all",
			"names no parent",
			FolderLinkRecord{Project: "member-a"},
			FolderMember{Project: "member-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloud, db := wu2Cloud(), wu2DB(t)
			cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
			cloud.folder("folders/8", WorkspaceFolderName("borealis"), "organizations/123")
			cloud.project("member-a", "folders/9")
			cloud.project("member-a-fb", "folders/9")
			cloud.project("member-b", "folders/9")
			cloud.project("member-b-fb", "folders/9")
			a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}
			wu2WriteRecord(t, db, tc.rec)

			err := a.Unlink(t.Context(), tc.member)
			if err == nil {
				t.Fatalf("Unlink followed the record %+v", tc.rec)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal %q does not name %q", err, tc.want)
			}
			if len(cloud.moves) != 0 {
				t.Errorf("the refused unlink made the move(s) %v", cloud.moves)
			}
			if rec, found := wu2Record(t, db); !found || rec != tc.rec {
				t.Errorf("the record is %+v (found=%v) after the refusal, want it left as it was: a refusal does not drop a record a person has to correct", rec, found)
			}
		})
	}

	// A record that cannot be read at all is the same refusal: a document only
	// half understood is a document no caller may act on.
	cloud, db := wu2Cloud(), wu2DB(t)
	cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
	cloud.project("member-a", "folders/9")
	a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}
	etag, _, err := db.GetETag(t.Context(), PathWorkspaceLink, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutIfMatch(t.Context(), PathWorkspaceLink, etag, map[string]any{"project": "member-a", "parent": "organizations/123", "extra": true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Unlink(t.Context(), FolderMember{Project: "member-a"}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("Unlink of an unreadable record = %v, want the decode refusal", err)
	}
	if len(cloud.moves) != 0 {
		t.Errorf("the refused unlink made the move(s) %v", cloud.moves)
	}
}

// TestFolderAdminBindingAddedAndRemoved: D2804, D2818 — a workspace admin is
// bound to roles/owner on the workspace's folder, adding one twice adds it
// once, removing it drops it, and the list is what the workspace's roster is
// compared against. Every one of them refuses on a workspace whose init has
// not run.
func TestFolderAdminBindingAddedAndRemoved(t *testing.T) {
	ctx := t.Context()
	cloud, db := wu2Cloud(), wu2DB(t)
	cloud.folder("folders/9", WorkspaceFolderName("aurora"), "organizations/123")
	cloud.owners["folders/9"] = []string{"user:founder@example.com"}
	a := &FolderAuthority{Workspace: "aurora", Folder: "folders/9", CRM: cloud, DB: db}

	if got, err := a.AdminBindings(ctx); err != nil || !slices.Equal(got, []string{"user:founder@example.com"}) {
		t.Fatalf("AdminBindings = %v, %v, want the owner the folder already had", got, err)
	}
	for range 2 {
		if err := a.AddAdmin(ctx, "user:ada@example.com"); err != nil {
			t.Fatalf("AddAdmin: %v", err)
		}
	}
	if err := a.AddAdmin(ctx, "user:grace@example.com"); err != nil {
		t.Fatalf("AddAdmin: %v", err)
	}
	if got, _ := a.AdminBindings(ctx); !slices.Equal(got, []string{"user:founder@example.com", "user:ada@example.com", "user:grace@example.com"}) {
		t.Errorf("AdminBindings = %v, want each admin once", got)
	}
	if err := a.RemoveAdmin(ctx, "user:ada@example.com"); err != nil {
		t.Fatalf("RemoveAdmin: %v", err)
	}
	if got, _ := a.AdminBindings(ctx); !slices.Equal(got, []string{"user:founder@example.com", "user:grace@example.com"}) {
		t.Errorf("AdminBindings = %v, want ada dropped and the others kept", got)
	}

	// An empty admin is no binding, and a workspace whose init has not run has
	// no folder to bind one to: every entry point refuses, and names the
	// command that sets the folder up.
	if err := a.AddAdmin(ctx, ""); err == nil || !strings.Contains(err.Error(), "no admin given") {
		t.Errorf("AddAdmin with no admin = %v, want the refusal that it names a member", err)
	}
	uninitialised := &FolderAuthority{Workspace: "aurora", CRM: cloud, DB: db}
	if err := uninitialised.RemoveAdmin(ctx, "user:ada@example.com"); err == nil || !strings.Contains(err.Error(), "fugaro workspace init") {
		t.Errorf("RemoveAdmin on a workspace with no folder = %v, want the refusal that names `fugaro workspace init`", err)
	}
	if _, err := uninitialised.AdminBindings(ctx); err == nil || !strings.Contains(err.Error(), "fugaro workspace init") {
		t.Errorf("AdminBindings on a workspace with no folder = %v, want the refusal that names `fugaro workspace init`", err)
	}
}

// TestCreateFolderPlacesTheProject: WU24 — `workspace init` creates the
// workspace's folder under the organization, binds the member who ran it to
// roles/owner on it, and places the workspace's own project in it (R8: the
// roster and the audit live there). A workspace that already has a folder is
// refused rather than given a second one.
func TestCreateFolderPlacesTheProject(t *testing.T) {
	ctx := t.Context()
	cloud, db := wu2Cloud(), wu2DB(t)
	cloud.project("aurora", "organizations/123")
	a := &FolderAuthority{Workspace: "aurora", CRM: cloud, DB: db}

	folder, err := a.CreateFolder(ctx, "organizations/123", "user:ada@example.com")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if got := cloud.folders[folder]; got != WorkspaceFolderName("aurora") {
		t.Errorf("the folder is named %q, want %q", got, WorkspaceFolderName("aurora"))
	}
	if got := cloud.parents[folder]; got != "organizations/123" {
		t.Errorf("the folder's parent is %q, want organizations/123", got)
	}
	if got := cloud.owners[folder]; !slices.Equal(got, []string{"user:ada@example.com"}) {
		t.Errorf("the folder's owners are %v, want the member who created it", got)
	}
	if got := cloud.parents["aurora"]; got != folder {
		t.Errorf("the workspace's own project is at %q, want %s", got, folder)
	}
	if a.Folder != folder {
		t.Errorf("the authority holds the folder %q, want %s", a.Folder, folder)
	}
	hold, err := a.Holds(ctx, FolderMember{Project: "aurora"})
	if err != nil || !hold.InFolder || hold.Parent != folder {
		t.Errorf("Holds = %+v, %v, want the workspace's project in the folder it just made", hold, err)
	}
	if _, err := a.CreateFolder(ctx, "organizations/123", "user:ada@example.com"); err == nil || !strings.Contains(err.Error(), "already has the folder") {
		t.Errorf("a second CreateFolder = %v, want the refusal that the workspace already has one", err)
	}

	// The workspace's name is a cut one here (a folder display name is 30
	// characters): the mark survives the cut, so a long-named workspace's
	// folder is still recognised as one.
	longName := strings.Repeat("a", 30)
	cloud.project(longName, "organizations/123")
	long := &FolderAuthority{Workspace: longName, CRM: cloud, DB: db}
	made, err := long.CreateFolder(ctx, "organizations/123", "user:ada@example.com")
	if err != nil {
		t.Fatalf("CreateFolder of a workspace with a 30-character name: %v", err)
	}
	name := cloud.folders[made]
	if len(name) > folderNameMax || !isWorkspaceFolderName(name) {
		t.Errorf("the folder is named %q, want the mark and 30 characters at most", name)
	}
}
