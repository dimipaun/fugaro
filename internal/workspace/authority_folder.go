package workspace

// The folder authority (WU2): the mechanism a workspace with a GCP
// organization holds its members by. A member's project sits in the
// workspace's folder, and the workspace's admins are bound to roles/owner on
// that folder (D2803, D2804), so every one of them is an owner of every
// project in it, and a project added later inherits the same.
//
// Two writes here are the member owner's own (A6): the move into the folder
// and the move back. Both run with the member owner's credentials, on their
// own machine, and both refuse rather than ask. What the workspace's own side
// writes — the folder's owner binding — is AddAdmin and RemoveAdmin.
//
// The record of where a project came from lives in the member's own database,
// at PathWorkspaceLink, and it is a member-controlled value: the member's
// budget admins hold the member database's write and can rewrite it (WU0's
// threat model). What that costs is bounded here and nowhere else. Unlink
// refuses a recorded parent inside ANY workspace's folder — told apart by the
// display name a workspace folder carries, WorkspaceFolderName — so the worst
// a rewritten record achieves is a refusal, a denial of the move back rather
// than a project placed in another workspace's folder. That bound is the
// reason the mark is a name and not a label: Cloud Resource Manager exposes no
// label on a folder (crm3.Folder carries displayName, parent and tags, and
// tags are an organization's own keys), so the display name is the only mark
// the API can carry. A folder Fugaro did not create but that a person named
// into the mark is refused as a destination too, which is the fail-closed
// direction; a folder whose display name a person renames out of the mark is
// no longer recognized, which is the bound this design accepts and says out
// loud.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// PathWorkspaceLink is where a member's own database records what its
	// project's parent was before the move into the workspace's folder
	// (R14): the project's, the Firebase project's when that is a project of
	// its own (R10), and nothing else. It sits beside the member's roster
	// nodes under the same fence — config/admin carries `.read: false` and
	// `.write: false`, so no client token reaches it, and the CLI reaches it
	// over IAM (internal/access's PathNode explains why that is not a secret
	// from the project's own people).
	//
	// The workspace node holds no per-project entry: where a project came
	// from is the member's own fact, and the project's own Terraform is the
	// only thing that ever writes a binding on it (D2801, R14).
	PathWorkspaceLink = "config/admin/workspace-link"

	// FolderMark is what a workspace's folder is called, before the workspace's
	// name: WorkspaceFolderName. A folder whose display name carries this mark
	// is one Fugaro created for a workspace, and Unlink refuses to move a
	// project into one (the package comment says why the mark is a name).
	FolderMark = "fugaro-workspace"

	// folderNameMax is the API's own limit on a folder's display name
	// (infra.ValidateFolderName).
	folderNameMax = 30

	// folderWalkMax bounds the walk up a folder's parents in
	// workspaceFolderAbove. Google's own nesting is shallow (a handful of
	// levels), and the bound is what keeps a folder whose parent is itself —
	// a rewrite or a bug at the far end — from spinning here.
	folderWalkMax = 10
)

// folderParentRE is the shape of a recorded parent, the same grammar
// infra.ValidateParent holds a --parent to.
var folderParentRE = regexp.MustCompile(`^(organizations|folders)/[0-9]{1,20}$`)

// WorkspaceFolderName is the display name a workspace's folder carries: the
// mark and the workspace's name, cut to the API's 30-character limit (the
// mark is 16 characters, so a cut name keeps it whole). Two workspaces of one
// organization differ in the name that follows, which is what the API's
// sibling-uniqueness rule needs.
func WorkspaceFolderName(workspace string) string {
	name := FolderMark + "-" + workspace
	if len(name) > folderNameMax {
		name = strings.TrimRight(name[:folderNameMax], "-_ ")
	}
	return name
}

// isWorkspaceFolderName reports whether a folder's display name is one
// WorkspaceFolderName makes.
func isWorkspaceFolderName(name string) bool {
	return strings.HasPrefix(name, FolderMark+"-")
}

// FolderMember is the member a folder operation acts on: its GCP project, and
// its Firebase project when that is a project of its own (F2, R10). Firebase
// is empty for a member whose Firebase project is its GCP project, and both
// are then covered by one move.
type FolderMember struct {
	Project  string
	Firebase string
}

// projects is the member's projects, in the order they are moved and restored:
// the GCP project first.
func (m FolderMember) projects() []string {
	if m.Firebase == "" || m.Firebase == m.Project {
		return []string{m.Project}
	}
	return []string{m.Project, m.Firebase}
}

// FolderHold is where a member's project stands (Holds).
type FolderHold struct {
	// Parent is what the project's parent is now: organizations/N, folders/N,
	// or "" for a project under no parent.
	Parent string
	// InFolder is whether that parent is this workspace's folder.
	InFolder bool
}

// FolderLinkRecord is the document at PathWorkspaceLink: the parents the
// member's projects had before the move. It decodes strictly, as every stored
// document here does, and every field is written: a missing key is a record
// this reader cannot vouch for, not an empty one.
type FolderLinkRecord struct {
	Project  string `json:"project"`
	Parent   string `json:"parent"`
	Firebase string `json:"firebase"`
	// FirebaseParent is the Firebase project's own parent, meaningful only
	// when Firebase is set.
	FirebaseParent string `json:"firebase_parent"`
}

// EncodeFolderLink renders a record as the document the member's database
// holds, validating it first, so a writer cannot store a document its own
// reader refuses.
func EncodeFolderLink(rec FolderLinkRecord) ([]byte, error) {
	if err := validateFolderLink(rec); err != nil {
		return nil, err
	}
	return json.Marshal(rec)
}

// DecodeFolderLink reads the record. It is strict: an unknown field, a second
// JSON value, and a project or a parent whose spelling is not one this file
// writes are all refused, because the record is member-controlled (WU0) and a
// document only half understood is a document no caller may act on.
func DecodeFolderLink(raw []byte) (FolderLinkRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rec FolderLinkRecord
	if err := dec.Decode(&rec); err != nil {
		return FolderLinkRecord{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return FolderLinkRecord{}, errors.New("the record carries more than one JSON value")
	}
	if err := validateFolderLink(rec); err != nil {
		return FolderLinkRecord{}, err
	}
	return rec, nil
}

// validateFolderLink holds a record to the two things its shape does not: the
// projects are named, and every parent is a parent (or empty, for a project
// that had none — the state a project created outside an organization is in).
func validateFolderLink(rec FolderLinkRecord) error {
	if rec.Project == "" {
		return errors.New("project: the record names the project it moved")
	}
	if rec.Parent != "" && !folderParentRE.MatchString(rec.Parent) {
		return fmt.Errorf("parent: %q is not organizations/<number> or folders/<number>, and not empty", rec.Parent)
	}
	if rec.Firebase == "" && rec.FirebaseParent != "" {
		return fmt.Errorf("firebase_parent: %q is recorded with no firebase project beside it", rec.FirebaseParent)
	}
	if rec.FirebaseParent != "" && !folderParentRE.MatchString(rec.FirebaseParent) {
		return fmt.Errorf("firebase_parent: %q is not organizations/<number> or folders/<number>", rec.FirebaseParent)
	}
	// A recorded Firebase project and its parent go together: Unlink moves the
	// two projects back in one act, and a record that names the first without
	// the second is a record that would leave a project in the folder while the
	// other went home.
	if rec.Firebase != "" && rec.FirebaseParent == "" {
		return fmt.Errorf("firebase_parent: the record names the Firebase project %s with no parent for it", rec.Firebase)
	}
	return nil
}

// Folders is the Resource Manager surface the folder authority uses: the calls
// internal/infra/folder.go makes. It is an interface so this file's own
// behaviour — the refusal of another workspace's folder, the record written
// before a move and the walk that reads it back — is tested without a cloud in
// the way; *infra.Folders is the implementation the CLI hands in, and it
// satisfies this interface as it stands (the two packages cannot name each
// other's types: the dependency between them runs through
// internal/projectpolicy, which imports this one).
type Folders interface {
	Create(ctx context.Context, displayName, parent string) (string, error)
	// Get answers a folder's display name and its parent.
	Get(ctx context.Context, folder string) (displayName, parent string, err error)
	Owners(ctx context.Context, folder string) ([]string, error)
	AddOwner(ctx context.Context, folder, member string) error
	RemoveOwner(ctx context.Context, folder, member string) error
	Parent(ctx context.Context, project string) (string, error)
	Move(ctx context.Context, project, parent string) error
}

// FolderAuthority is this mechanism's implementation. Folder is empty for a
// workspace whose init has not run yet; every method that needs it refuses
// rather than acting on a name nobody set, except Holds, which answers where
// the project stands either way.
type FolderAuthority struct {
	// Workspace is the workspace's name, which is its own project's ID too
	// (A1, D1290): the folder is named after it and CreateFolder places it.
	Workspace string
	// Folder is the workspace's folder, folders/N.
	Folder string
	// CRM is the Resource Manager surface; nil is a programming error and
	// panics at the first call, as a nil client does everywhere else here.
	CRM Folders
	// DB is the member's own database, where the link record lives (R14).
	DB *rtdb.Client
	// Out receives what a command prints; nil discards it. Unlink prints the
	// parent it restores, so the member owner sees where their project goes
	// before it goes there.
	Out io.Writer
}

func (a *FolderAuthority) printf(format string, args ...any) {
	if a.Out != nil {
		fmt.Fprintf(a.Out, format, args...)
	}
}

// Holds reports where the member's project stands: its current parent, and
// whether that parent is this workspace's folder. It reads and writes nothing
// else — it is the read `status` and the installation-level refusals key on
// (R11).
func (a *FolderAuthority) Holds(ctx context.Context, m FolderMember) (FolderHold, error) {
	if m.Project == "" {
		return FolderHold{}, errors.New("no project to read: a folder hold names the member's GCP project")
	}
	parent, err := a.CRM.Parent(ctx, m.Project)
	if err != nil {
		return FolderHold{}, err
	}
	return FolderHold{Parent: parent, InFolder: a.Folder != "" && parent == a.Folder}, nil
}

// Link moves the member's projects into the workspace's folder, from the
// member owner's own credentials (D2800, D2819), after recording where they
// were.
//
// The record is written BEFORE the first move: a move that then fails leaves a
// project where it was with a record that names the parent it is still under,
// so Unlink is a no-op rather than a move to a place nobody recorded. The
// other order can lose the parent for good.
//
// It refuses a project that sits in another workspace's folder (R2) — a
// project belongs to at most one workspace (D1291) — one that sits in a folder
// of its own under this workspace's folder (the admins are inherited either
// way, and link places a project directly in the folder), one that is under no
// parent (there would be no parent to record and so no way back) and one whose
// record is already written, naming `unlink` as the way out. A member already
// in the workspace's folder is already linked: Link then does nothing, whatever
// the record says, rather than overwrite a parent it did not record.
func (a *FolderAuthority) Link(ctx context.Context, m FolderMember) error {
	if err := a.check(m); err != nil {
		return err
	}
	if a.Folder == "" {
		return errors.New("the workspace has no folder yet: `fugaro workspace init` sets the mechanism up before any member links")
	}
	parents := make(map[string]string, 2)
	linked := true
	for _, p := range m.projects() {
		parent, err := a.CRM.Parent(ctx, p)
		if err != nil {
			return err
		}
		parents[p] = parent
		if parent == a.Folder {
			// In this workspace's folder already: nothing to move for this one,
			// and nothing to refuse either.
			continue
		}
		linked = false
		above, err := a.workspaceFolderAbove(ctx, parent)
		if err != nil {
			return err
		}
		if above == a.Folder {
			// Under this workspace's own folder, but in a folder of its own
			// inside it: the workspace's admins are inherited either way, and a
			// link places a project directly in the folder, so this is refused
			// rather than recorded as though the project had moved.
			return fmt.Errorf("project %s is in the folder %s, under this workspace's own folder %s: link places a project directly in the workspace's folder, so move it out of %s first", p, parent, a.Folder, parent)
		}
		if above != "" {
			return fmt.Errorf("project %s is in the folder %s, which is another workspace's: a project belongs to at most one workspace, so have that workspace's owner run `fugaro workspace project rm %s`, or move the project out of its folder first, before linking it here", p, above, m.Project)
		}
	}
	// A project under no parent is not linked: the move back has to name a
	// destination, and the API takes no "no parent" for one, so a project moved
	// into the folder from there could never be moved out again.
	for _, p := range m.projects() {
		if parents[p] == "" {
			return fmt.Errorf("project %s is under no parent: a project moved into the workspace's folder is moved back to the parent it is recorded at, and there is none to record; put it under an organization or a folder first and link it then", p)
		}
	}
	if linked {
		return nil
	}
	if _, _, found, err := a.readLink(ctx); err != nil {
		return err
	} else if found {
		return fmt.Errorf("%s already holds a link record: this member was linked before and not unlinked, and a second link would lose the parent that record keeps; run `fugaro workspace project unlink` first", PathWorkspaceLink)
	}
	rec := FolderLinkRecord{Project: m.Project, Parent: parents[m.Project]}
	if m.Firebase != "" && m.Firebase != m.Project {
		rec.Firebase, rec.FirebaseParent = m.Firebase, parents[m.Firebase]
	}
	raw, err := EncodeFolderLink(rec)
	if err != nil {
		return fmt.Errorf("recording %s: %w", PathWorkspaceLink, err)
	}
	if err := a.writeLink(ctx, raw); err != nil {
		return err
	}
	for _, p := range m.projects() {
		if parents[p] == a.Folder {
			continue
		}
		if err := a.CRM.Move(ctx, p, a.Folder); err != nil {
			return fmt.Errorf("%s is recorded at %s; moving it into the workspace's folder failed: %w", p, PathWorkspaceLink, err)
		}
	}
	return nil
}

// Unlink moves the member's projects back to the parents its own record names,
// prints where they go, and drops the record. It drops only the authority: the
// workspace admins' stored grants on the member stay until the workspace
// project's owner runs `fugaro workspace project rm` (D2822).
//
// It refuses a recorded parent inside any workspace's folder, this one's
// included, because a record is a member-controlled value and moving a project
// into a workspace's folder is never a restore (WU0). It refuses a record that
// names another project, or another Firebase project, than the one it is
// called for, and a record that cannot be read at all: every one of those is a
// record no more trustworthy than a rewrite, and the answer to all of them is
// the same refusal rather than a move.
func (a *FolderAuthority) Unlink(ctx context.Context, m FolderMember) error {
	if err := a.check(m); err != nil {
		return err
	}
	rec, etag, found, err := a.readLink(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("nothing is recorded at %s: no link record says where %s came from, so there is no parent to move it back to; if it was linked before the record was lost, move the project out of the folder by hand", PathWorkspaceLink, m.Project)
	}
	if rec.Project != m.Project || rec.Firebase != m.Firebase {
		return fmt.Errorf("the record at %s names %s and %q, not this member (%s and %q): the record was rewritten or belongs to another project, and nothing here moves a project on a record it cannot trust", PathWorkspaceLink, rec.Project, rec.Firebase, m.Project, m.Firebase)
	}
	if rec.Parent == "" {
		return fmt.Errorf("the record at %s names no parent for %s: a project moved back needs a destination, and there is none; move it by hand", PathWorkspaceLink, m.Project)
	}
	for _, pair := range []struct{ what, parent string }{{"project", rec.Parent}, {"Firebase project", rec.FirebaseParent}} {
		if pair.parent == "" {
			// No Firebase project of its own, which is the common case: nothing
			// to check and nothing to move.
			continue
		}
		above, err := a.workspaceFolderAbove(ctx, pair.parent)
		if err != nil {
			return err
		}
		if above != "" {
			return fmt.Errorf("the record at %s names %s for the %s, which is a workspace's folder (%q): a member is never moved into a workspace's folder, and a member's budget admin can rewrite this record, so it is refused rather than followed; correct the record by hand or leave the project where it is", PathWorkspaceLink, pair.parent, pair.what, above)
		}
	}
	a.printf("returning %s to %s and %s\n", m.Project, orNone(rec.Parent), firebaseWords(rec, m))
	if err := a.CRM.Move(ctx, m.Project, rec.Parent); err != nil {
		return err
	}
	if rec.Firebase != "" && rec.FirebaseParent != "" {
		if err := a.CRM.Move(ctx, rec.Firebase, rec.FirebaseParent); err != nil {
			return fmt.Errorf("%s is back at %s; moving its Firebase project %s back failed: %w", m.Project, rec.Parent, rec.Firebase, err)
		}
	}
	if err := a.DB.PutIfMatch(ctx, PathWorkspaceLink, etag, nil); err != nil {
		return fmt.Errorf("%s is back at %s, and the record at %s could not be removed: %w", m.Project, rec.Parent, PathWorkspaceLink, err)
	}
	return nil
}

// firebaseWords names the Firebase project in Unlink's line when there is a
// separate one to move back, and nothing when there is not.
func firebaseWords(rec FolderLinkRecord, m FolderMember) string {
	if rec.Firebase == "" || rec.FirebaseParent == "" {
		return "no Firebase project of its own"
	}
	return fmt.Sprintf("its Firebase project %s to %s", m.Firebase, rec.FirebaseParent)
}

// orNone is a parent as a line prints it: "no parent" for the empty one.
func orNone(parent string) string {
	if parent == "" {
		return "no parent"
	}
	return parent
}

// AddAdmin binds a workspace admin to roles/owner on the workspace's folder
// (D2804, D2818): every project in the folder, and every project added to it
// later, makes them an owner. It writes only the folder's own policy — a
// storage grant on a member is that member's Terraform's (D2801).
func (a *FolderAuthority) AddAdmin(ctx context.Context, admin string) error {
	if err := a.checkFolder(); err != nil {
		return err
	}
	if admin == "" {
		return errors.New("no admin given: a folder binding names a member")
	}
	return a.CRM.AddOwner(ctx, a.Folder, admin)
}

// RemoveAdmin drops that binding, at once and everywhere it reaches (D2818 as
// corrected: what lags is the member-level grant, and `status` shows it as
// pending).
func (a *FolderAuthority) RemoveAdmin(ctx context.Context, admin string) error {
	if err := a.checkFolder(); err != nil {
		return err
	}
	if admin == "" {
		return errors.New("no admin given: a folder binding names a member")
	}
	return a.CRM.RemoveOwner(ctx, a.Folder, admin)
}

// AdminBindings lists the members bound to roles/owner on the folder, which is
// what the workspace's admin list is compared against (D2823). A person
// holding owner through a group or a domain is listed as that group or domain:
// the folder's policy names it, and neither the roster nor the drift reads
// through it (F25).
func (a *FolderAuthority) AdminBindings(ctx context.Context) ([]string, error) {
	if err := a.checkFolder(); err != nil {
		return nil, err
	}
	return a.CRM.Owners(ctx, a.Folder)
}

// CreateFolder creates the workspace's folder under parent with owner as its
// roles/owner, places the workspace's own project in it (R8: the roster and
// the audit live there), and remembers the folder. It is `workspace init`'s
// one call (WU24), and it refuses a workspace that already has a folder.
func (a *FolderAuthority) CreateFolder(ctx context.Context, parent, owner string) (string, error) {
	if a.Workspace == "" {
		return "", errors.New("the workspace has no name: a folder is named after it, and so is the project placed in it")
	}
	if a.Folder != "" {
		return "", fmt.Errorf("this workspace already has the folder %s", a.Folder)
	}
	if owner == "" {
		return "", errors.New("no owner given: the member who creates the folder is bound to roles/owner on it")
	}
	folder, err := a.CRM.Create(ctx, WorkspaceFolderName(a.Workspace), parent)
	if err != nil {
		return "", err
	}
	if err := a.CRM.AddOwner(ctx, folder, owner); err != nil {
		return "", fmt.Errorf("the folder %s was created; binding %s to roles/owner on it failed: %w", folder, owner, err)
	}
	if err := a.CRM.Move(ctx, a.Workspace, folder); err != nil {
		return "", fmt.Errorf("the folder %s was created and %s is an owner of it; placing the workspace's own project %s in it failed: %w", folder, owner, a.Workspace, err)
	}
	a.Folder = folder
	return folder, nil
}

// check refuses a Member this authority cannot act on: a move names the member's
// projects, and the record and the moves are the member's own.
func (a *FolderAuthority) check(m FolderMember) error {
	if m.Project == "" {
		return errors.New("no project given: a folder operation names the member's GCP project")
	}
	if a.DB == nil {
		return fmt.Errorf("%s is the member's own node, and no database is set to read or write it", PathWorkspaceLink)
	}
	return nil
}

// checkFolder refuses a folder operation on a workspace whose init has not run.
func (a *FolderAuthority) checkFolder() error {
	if a.Folder == "" {
		return errors.New("the workspace has no folder: `fugaro workspace init` sets the folder mechanism up first")
	}
	return nil
}

// workspaceFolderAbove walks up from parent and returns the first folder that
// is a workspace's, or "" when none of them is. A parent that is not a folder
// (an organization, or nothing at all) ends the walk.
//
// A parent still inside a folder when the bound is reached is refused rather
// than walked past: a workspace's folder may sit further up, and answering ""
// there reads as "no workspace's folder above" — which is what both callers
// take as leave to move the project. Unlink promises that the worst a rewritten
// record achieves is a refusal (SECURITY.md, the threat model), so a chain too
// deep to read is refused, never followed.
func (a *FolderAuthority) workspaceFolderAbove(ctx context.Context, parent string) (string, error) {
	from := parent
	for i := 0; i < folderWalkMax; i++ {
		if !strings.HasPrefix(parent, "folders/") {
			return "", nil
		}
		displayName, up, err := a.CRM.Get(ctx, parent)
		if err != nil {
			return "", err
		}
		if isWorkspaceFolderName(displayName) {
			return parent, nil
		}
		parent = up
	}
	if strings.HasPrefix(parent, "folders/") {
		return "", fmt.Errorf("the parents of %s are nested deeper than %d folders, so whether a workspace's folder is above it cannot be read: a parent this deep is refused rather than followed", from, folderWalkMax)
	}
	return "", nil
}

// readLink reads the member's record, with the ETag a write must carry. An
// absent node is (zero, the null ETag, false, no error).
func (a *FolderAuthority) readLink(ctx context.Context) (FolderLinkRecord, string, bool, error) {
	var raw json.RawMessage
	etag, found, err := a.DB.GetETag(ctx, PathWorkspaceLink, &raw)
	if err != nil {
		return FolderLinkRecord{}, "", false, fmt.Errorf("reading %s: %w", PathWorkspaceLink, err)
	}
	if !found {
		return FolderLinkRecord{}, etag, false, nil
	}
	rec, err := DecodeFolderLink(raw)
	if err != nil {
		return FolderLinkRecord{}, etag, false, fmt.Errorf("reading %s: %w", PathWorkspaceLink, err)
	}
	return rec, etag, true, nil
}

// writeLink creates the record, conditional on there being none: the ETag a
// create carries is the null one, so a record written by another writer
// between the read and this write is a refusal rather than a lost parent.
func (a *FolderAuthority) writeLink(ctx context.Context, raw []byte) error {
	_, etag, found, err := a.readLink(ctx)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("%s already holds a record", PathWorkspaceLink)
	}
	if err := a.DB.PutIfMatch(ctx, PathWorkspaceLink, etag, json.RawMessage(raw)); err != nil {
		return fmt.Errorf("writing %s: %w", PathWorkspaceLink, err)
	}
	return nil
}
