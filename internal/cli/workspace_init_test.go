package cli

// workspace_init_test.go is WU24's tests (docs/workspace-projects-users/
// implementation-plan.md §3): `workspace init` against fakes and never a cloud.
//
// Every seam the command reaches is a fake declared here and nowhere else (the
// plan's rule: a task's tests use its own fakes and never the contract's stub,
// so I1 deletes the stubs without rewriting a test). The one that decides which
// mechanism the workspace gets is the folder seam's organization search, which
// is this task's red step: the folder test fails until that fake answers a
// search at all, and it answers one only because D2803's detection is what the
// command asks it for.
//
// The two fakes that stand in for the LIVE sides — wu24FakeFolders and
// wu24FakeAuthority — record what the real calls do rather than a bare "was
// called": the folder fake places the workspace's own project when its folder is
// created, because that placement IS WU2's CreateFolder and there is no second
// call `init` could forget to make (R8), and the authority fake answers one kind
// and fails the test on every method `init` must not reach.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu24TestName is the workspace every test initializes. It is a project name,
// because a workspace name IS one (F21).
const wu24TestName = "aurora"

// wu24TestCaller is the address the credentials belong to in every test.
const wu24TestCaller = "ada@example.com"

// wu24TestMember is that person as a live binding names them: `user:<address>`,
// the spelling both mechanisms hand to SetIamPolicy and the one D2823's gate
// reads back. The roster keeps the bare address beside it.
const wu24TestMember = "user:" + wu24TestCaller

// wu24FakeStore is C1's Store as these tests use it: one roster node that is
// absent or present, a read that answers the revision a write is made under, a
// write that records both, and a shared log of the order the two writes of an
// `init` landed in.
type wu24FakeStore struct {
	path     string
	found    bool
	readErr  error
	writeErr error
	reads    int
	writes   [][]byte
	written  workspace.Revision
	log      *[]string
}

func (f *wu24FakeStore) Path() string { return f.path }

func (f *wu24FakeStore) Read(context.Context) (workspace.Revision, []byte, error) {
	f.reads++
	if f.readErr != nil {
		return workspace.Revision{}, nil, f.readErr
	}
	if f.found {
		return workspace.Revision{ETag: "etag-1", Found: true}, []byte(`{"version":1}`), nil
	}
	return workspace.Revision{ETag: "null_etag", Found: false}, nil, nil
}

func (f *wu24FakeStore) Write(_ context.Context, rev workspace.Revision, roster []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = rev
	f.writes = append(f.writes, roster)
	if f.log != nil {
		*f.log = append(*f.log, "roster")
	}
	return nil
}

// wu24FakeAudit is C1's AuditSink: it creates the one document it is given and
// records the id and the bytes, which is what a test reads the `init` entry off.
type wu24FakeAudit struct {
	err    error
	ids    []string
	events [][]byte
	log    *[]string
}

func (f *wu24FakeAudit) Create(_ context.Context, id string, event []byte) error {
	if f.err != nil {
		return f.err
	}
	f.ids = append(f.ids, id)
	f.events = append(f.events, event)
	if f.log != nil {
		*f.log = append(*f.log, "audit")
	}
	return nil
}

// wu24FakeAuthority is C2's Authority as `init` reaches it: AddAdmin, and
// nothing else. The methods this command must never call fail the test rather
// than answering a zero value, so an `init` that read or linked something
// cannot pass on an answer no side made.
type wu24FakeAuthority struct {
	t      *testing.T
	kind   workspace.AuthorityKind
	addErr error
	admins []string
}

func (f *wu24FakeAuthority) Kind() workspace.AuthorityKind { return f.kind }

func (f *wu24FakeAuthority) AddAdmin(_ context.Context, admin string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.admins = append(f.admins, admin)
	return nil
}

func (f *wu24FakeAuthority) unexpected(what string) error {
	f.t.Helper()
	f.t.Errorf("workspace init called Authority.%s: its whole reach on the authority is AddAdmin for the caller, and a read or a link here is a call on a workspace that has no members yet", what)
	return fmt.Errorf("workspace init called Authority.%s", what)
}

func (f *wu24FakeAuthority) Link(context.Context, workspace.MemberTarget) error {
	return f.unexpected("Link")
}

func (f *wu24FakeAuthority) Unlink(context.Context, workspace.MemberTarget) error {
	return f.unexpected("Unlink")
}

func (f *wu24FakeAuthority) Holds(context.Context, workspace.MemberTarget) (bool, error) {
	return false, f.unexpected("Holds")
}

func (f *wu24FakeAuthority) RemoveAdmin(context.Context, string) error {
	return f.unexpected("RemoveAdmin")
}

func (f *wu24FakeAuthority) AdminBindings(context.Context) ([]string, error) {
	return nil, f.unexpected("AdminBindings")
}

// wu24FolderCall is one CreateFolder the command made: the parent it named and
// the member it bound as the folder's owner.
type wu24FolderCall struct {
	parent string
	owner  string
}

// wu24FakeFolders is the folder seam: the organization search that detects
// which mechanism this workspace gets (D2803) and the one call WU2's folder
// authority makes for `init`.
type wu24FakeFolders struct {
	t         *testing.T
	workspace string
	orgs      []string
	orgErr    error
	folder    string
	createErr error
	calls     []wu24FolderCall
	placed    string
}

func (f *wu24FakeFolders) Organizations(context.Context) ([]string, error) {
	if f.orgErr != nil {
		return nil, f.orgErr
	}
	return f.orgs, nil
}

// CreateFolder answers what WU2's own CreateFolder answers — a folder under
// parent, with owner bound to roles/owner on it, and the workspace's own project
// placed in it (R8) — because that call is the whole of what `init` asks of the
// folder side and the placement is not a second call it could forget to make.
func (f *wu24FakeFolders) CreateFolder(_ context.Context, parent, owner string) (string, error) {
	f.calls = append(f.calls, wu24FolderCall{parent: parent, owner: owner})
	if f.createErr != nil {
		return "", f.createErr
	}
	f.placed = f.workspace
	return f.folder, nil
}

// wu24Rig is one run of `workspace init <name>`: the seams it reached, what it
// printed, the error it answered and the order its two writes landed in.
type wu24Rig struct {
	t       *testing.T
	name    string
	caller  string
	store   *wu24FakeStore
	audit   *wu24FakeAudit
	auth    *wu24FakeAuthority
	folders *wu24FakeFolders
	log     *[]string
	opened  []string
	out     string
	err     error
}

// wu24Init runs `workspace init` for a workspace with an organization, a
// folder, and a caller who can create it; setup adjusts the one answer a test
// is about. The agent markers the test binary may inherit are cleared first:
// whether this machine is a coding agent's session is not what these tests are
// about (the refusal has a test of its own).
func wu24Init(t *testing.T, setup func(r *wu24Rig)) *wu24Rig {
	t.Helper()
	clearAgentMarkers(t)
	log := &[]string{}
	r := &wu24Rig{
		t:      t,
		name:   wu24TestName,
		caller: wu24TestCaller,
		store:  &wu24FakeStore{path: "workspaceRosters/" + wu24TestName, log: log},
		audit:  &wu24FakeAudit{log: log},
		auth:   &wu24FakeAuthority{t: t, kind: workspace.AuthoritySA},
		folders: &wu24FakeFolders{
			t:         t,
			workspace: wu24TestName,
			orgs:      []string{"organizations/5678"},
			folder:    "folders/1234",
		},
		log: log,
	}
	if setup != nil {
		setup(r)
	}
	cmd := newWorkspaceInitCmd(workspaceInitDeps{
		Open: func(_ context.Context, name string) (workspaceInitTarget, error) {
			r.opened = append(r.opened, name)
			return workspaceInitTarget{
				Store:     r.store,
				Audit:     r.audit,
				Folders:   r.folders,
				Authority: r.auth,
				Caller:    r.caller,
			}, nil
		},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{r.name})
	r.err = cmd.Execute()
	r.out = out.String()
	return r
}

// roster decodes the one roster the run wrote, and fails the test when it wrote
// none or more than one: a workspace's first write is one document.
func (r *wu24Rig) roster() *workspace.Roster {
	r.t.Helper()
	if len(r.store.writes) != 1 {
		r.t.Fatalf("workspace init wrote %d rosters, and the workspace's first write is one (C1's contract)", len(r.store.writes))
	}
	got, err := workspace.DecodeRoster(r.store.writes[0])
	if err != nil {
		r.t.Fatalf("the roster workspace init wrote is not one its own reader accepts: %v (%s)", err, r.store.writes[0])
	}
	return got
}

// event decodes the one audit entry the run created.
func (r *wu24Rig) event() workspace.AuditEvent {
	r.t.Helper()
	if len(r.audit.events) != 1 {
		r.t.Fatalf("workspace init created %d audit entries, and its own rule is one `init` event (C1's contract)", len(r.audit.events))
	}
	got, err := workspace.DecodeAuditEvent(r.audit.events[0])
	if err != nil {
		r.t.Fatalf("the audit entry workspace init created is not one its own reader accepts: %v (%s)", err, r.audit.events[0])
	}
	return got
}

// liveBindings is what the run made live for the caller, which is what an
// `Admins` entry is trusted beside (D2823): the folder's owner binding in the
// folder mechanism, the account's token creator in the SA one.
func (r *wu24Rig) liveBindings() []string {
	if len(r.folders.calls) == 1 {
		return []string{r.folders.calls[0].owner}
	}
	return r.auth.admins
}

// nothingCreated fails the test when the run reached the cloud at all: no
// folder, no binding, no roster and no audit entry. Every refusal of this
// command is made before anything is created.
func (r *wu24Rig) nothingCreated(what string) {
	r.t.Helper()
	if len(r.folders.calls) != 0 {
		r.t.Errorf("%s, and workspace init created the folder %v", what, r.folders.calls)
	}
	if len(r.auth.admins) != 0 {
		r.t.Errorf("%s, and workspace init bound %v as an admin", what, r.auth.admins)
	}
	if len(r.store.writes) != 0 {
		r.t.Errorf("%s, and workspace init wrote the roster %s", what, r.store.writes)
	}
	if len(r.audit.events) != 0 {
		r.t.Errorf("%s, and workspace init created %d audit entries", what, len(r.audit.events))
	}
}

func TestWorkspaceInitPicksFolderWithOrg(t *testing.T) {
	r := wu24Init(t, nil)
	if r.err != nil {
		t.Fatalf("workspace init failed for a caller who can create a folder in %v: %v (%s)", r.folders.orgs, r.err, r.out)
	}
	roster := r.roster()
	if roster.Mechanism != workspace.MechanismFolder {
		t.Errorf("the roster says mechanism %q, and this credential's organization search answered %v: a workspace with an organization gets the folder, which is D2803's preferred mechanism and the one R8's roster and audit live under", roster.Mechanism, r.folders.orgs)
	}
	if roster.FolderID != "folders/1234" {
		t.Errorf("the roster records folder_id %q, and the folder created for this workspace is %q: a folder workspace names the folder it owns, and every link and the drift comparison read the id from the roster", roster.FolderID, "folders/1234")
	}
	// The write is the create: a first write is made under the revision a read
	// that found nothing answered (C1's contract), and a write under any other
	// revision is one this node would refuse.
	if r.store.written.Found || r.store.written.ETag != "null_etag" {
		t.Errorf("the roster was written under the revision %+v, and a workspace's first write is made under the one a read that found nothing answered", r.store.written)
	}
	if got := r.liveBindings(); len(got) != 1 {
		t.Errorf("the run made %v live for the caller, and a folder workspace's first admin holds the folder's owner binding and nothing else: the search that answered an organization is not a second binding", got)
	}
}

func TestWorkspaceInitPlacesTheWorkspaceProject(t *testing.T) {
	r := wu24Init(t, nil)
	if r.err != nil {
		t.Fatalf("workspace init failed: %v (%s)", r.err, r.out)
	}
	if len(r.folders.calls) != 1 {
		t.Fatalf("workspace init made %d folder creates, and WU2's CreateFolder is its one call: it is the call that creates the folder, binds the caller as its owner and places the workspace's own project in it (R8)", len(r.folders.calls))
	}
	if got := r.folders.calls[0].parent; got != "organizations/5678" {
		t.Errorf("the folder was created under %q, and the organization the search answered is %q: a folder under anything else is one this credential was not shown as able to create in (D2803)", got, "organizations/5678")
	}
	if got := r.folders.calls[0].owner; got != wu24TestMember {
		t.Errorf("the folder's owner is %q, and the caller is %q: the member who runs `init` is the workspace's first admin, and the binding names them the way a policy does — %q — because a bare address is one Google refuses", got, wu24TestCaller, wu24TestMember)
	}
	if r.folders.placed != wu24TestName {
		t.Errorf("the workspace's own project placed in the folder is %q, and it is %q: R8 puts the roster node and the workspace's audit trail in the workspace's own project, which is the project the workspace is named after (A1, D1290)", r.folders.placed, wu24TestName)
	}
	// The folder path's live binding is the folder's owner and nothing else, so
	// the mechanism's authority is never reached for one (D2823, R6): an AddAdmin
	// beside CreateFolder would be a second, SA-model grant for a workspace whose
	// roster records the folder mechanism.
	if len(r.auth.admins) != 0 {
		t.Errorf("workspace init bound %v as an admin through the mechanism's authority on the folder path, and the first admin's binding there is the folder's owner, which CreateFolder already made in the call above: a second AddAdmin is a duplicate grant, and a folder workspace's roster would then carry the SA model's bindings as well (D2823, R6)", r.auth.admins)
	}
	if got := r.roster().FolderID; got != r.folders.folder {
		t.Errorf("the roster records the folder %q, and the folder the authority created is %q: the roster names the folder that really exists, which is the id the authority answered and not a name this command composed", got, r.folders.folder)
	}
}

func TestWorkspaceInitRecordsSAWithoutOrg(t *testing.T) {
	r := wu24Init(t, func(r *wu24Rig) { r.folders.orgs = nil })
	if r.err != nil {
		t.Fatalf("workspace init failed for a caller with no organization: %v (%s)", r.err, r.out)
	}
	roster := r.roster()
	if roster.Mechanism != workspace.MechanismSA {
		t.Errorf("the roster says mechanism %q, and the organization search answered none: a credential that sees no organization has no folder to create a workspace in, so D2803's fallback is the SA model", roster.Mechanism)
	}
	if roster.FolderID != "" {
		t.Errorf("the roster records folder_id %q under the SA mechanism, and the two go together: a node carrying a folder id beside %q is refused by the roster's own reader", roster.FolderID, workspace.MechanismSA)
	}
	if len(r.folders.calls) != 0 {
		t.Errorf("workspace init created %v with no organization to create it under", r.folders.calls)
	}
	if got := r.liveBindings(); len(got) != 1 || got[0] != wu24TestMember {
		t.Errorf("the run made %v live for the caller, and the SA mechanism's admin binding is the caller's token creator on the workspace IAM account, named %q (and R8's owner grant on the workspace's own project, which the authority makes in the same call)", got, wu24TestMember)
	}
}

func TestWorkspaceInitWritesTheFirstAdmin(t *testing.T) {
	for _, tc := range []struct {
		mechanism string
		orgs      []string
	}{
		{"folder", []string{"organizations/5678"}},
		{"sa", nil},
	} {
		t.Run(tc.mechanism, func(t *testing.T) {
			r := wu24Init(t, func(r *wu24Rig) { r.folders.orgs = tc.orgs })
			if r.err != nil {
				t.Fatalf("workspace init failed: %v (%s)", r.err, r.out)
			}
			roster := r.roster()
			if len(roster.People) != 1 {
				t.Fatalf("the roster holds %d people, and `init` writes the caller and nobody else: the second person of a workspace is `workspace user add`'s", len(roster.People))
			}
			person := roster.People[0]
			if person.Address != wu24TestCaller || person.Kind != workspace.KindHuman {
				t.Errorf("the roster's first person is %+v, and the caller is the human %q: an admin entry is confirmed by a binding only a person can hold for one (D2823), and a machine is never a workspace admin", person, wu24TestCaller)
			}
			if person.External {
				t.Errorf("the caller is marked external, and the external mark is D2812's record that somebody was admitted from outside the workspace's domains: the workspace's own creator was not admitted from anywhere, and its allowed domains are empty")
			}
			if len(roster.Admins) != 1 || roster.Admins[0] != wu24TestCaller {
				t.Errorf("the roster's admins are %v, and the caller %q is its first and only admin: the roster refuses a workspace nobody administers", roster.Admins, wu24TestCaller)
			}
			// The entry is a claim about a live binding, and the binding is the
			// mechanism's own (D2823).
			if got := r.liveBindings(); len(got) != 1 || got[0] != wu24TestMember {
				t.Errorf("the run made %v live for %q, and an admin entry is trusted only beside the binding the mechanism gives an admin, which names the person as %q: %s", got, wu24TestCaller, wu24TestMember, tc.mechanism)
			}
		})
	}
}

func TestWorkspaceInitRefusesASecondInit(t *testing.T) {
	r := wu24Init(t, func(r *wu24Rig) { r.store.found = true })
	if r.err == nil {
		t.Fatalf("workspace init ran for a workspace whose roster is already there, and a workspace's mechanism and folder are written once (R6): it printed %q", r.out)
	}
	r.nothingCreated("the roster is already there")
	if got := ErrorText(r.err); !strings.Contains(got, r.store.path) {
		t.Errorf("the refusal does not name the node it read: %q, and the path %q is what a person checks to see which workspace this machine resolved", got, r.store.path)
	}
	if got := ErrorText(r.err); !strings.Contains(got, "already holds") {
		t.Errorf("the refusal reads %q, and it has to say that the workspace is already initialized: a second `init` changes nothing, and a person who cannot tell that from a failure goes looking for what broke", got)
	}
	if ExitCode(r.err) != ExitUserError {
		t.Errorf("the refusal exits %d, and a workspace that is already initialized is the caller's own state to read rather than a fault of the program", ExitCode(r.err))
	}
}

func TestWorkspaceInitRefusedInAnAgentSession(t *testing.T) {
	marker := agentenv.AgentMarkers[0]
	r := wu24Init(t, func(r *wu24Rig) { t.Setenv(marker, "1") })
	if r.err == nil {
		t.Fatalf("workspace init created a workspace in a coding agent's session (%s is set), and it creates the folder, the owner binding and the roster with the caller's own credentials: a person runs it at their own terminal (A7)", marker)
	}
	if got := ErrorText(r.err); !strings.Contains(got, initflow.AgentRefusal(marker)) {
		t.Errorf("the refusal reads %q, and it has to carry the standing sentence for the marker %s: every command that refuses in an agent's session refuses in the same words", got, marker)
	}
	if len(r.opened) != 0 {
		t.Errorf("the workspace was opened for %v before the refusal, and this command asks who is running it before it reads or creates anything: a refused session gets no answer at all", r.opened)
	}
	if r.store.reads != 0 {
		t.Errorf("the roster was read %d times in a refused session, and the refusal comes before the read: the read is the answer `init` gives about a workspace's state", r.store.reads)
	}
	r.nothingCreated("a coding agent's session was refused")
}

func TestWorkspaceInitRecordsTheInitEvent(t *testing.T) {
	r := wu24Init(t, nil)
	if r.err != nil {
		t.Fatalf("workspace init failed: %v (%s)", r.err, r.out)
	}
	event := r.event()
	if event.Version != workspace.AuditEventVersion {
		t.Errorf("the entry's version is %d and this build writes %d", event.Version, workspace.AuditEventVersion)
	}
	if event.Action != workspace.ActionInit {
		t.Errorf("the entry's action is %q, and the workspace's first write records %q: the trail's first line is what created the workspace", event.Action, workspace.ActionInit)
	}
	if event.Actor != wu24TestCaller {
		t.Errorf("the entry's actor is %q, and the caller who made the change is %q: an entry names whoever made the change and not the workspace it was made in", event.Actor, wu24TestCaller)
	}
	if want := event.Time.UTC().Format("20060102T150405Z"); !strings.HasPrefix(event.ID, want) {
		t.Errorf("the entry's id is %q and its instant is %s: an id opens with the instant its event was made at, which is what makes a listing in key order a listing in time order", event.ID, event.Time.UTC())
	}
	if got := r.audit.ids[0]; got != event.ID {
		t.Errorf("the document created is named %q and the entry inside it carries the id %q: the id is a document name and a database key at once, and the two are one value", got, event.ID)
	}
	if got := strings.Join(*r.log, ","); got != "roster,audit" {
		t.Errorf("the two writes landed in the order %q, and an audit entry is written after the change it records and never instead of it (C1's contract): an entry for a roster that never landed is the trail recording a workspace that does not exist", got)
	}
}

func TestWorkspaceInitWritesNoRosterWhenTheLiveBindingFails(t *testing.T) {
	r := wu24Init(t, func(r *wu24Rig) {
		r.folders.orgs = nil
		r.auth.addErr = errors.New("permission denied on the account's policy")
	})
	if r.err == nil {
		t.Fatalf("workspace init finished although the caller's own binding was refused, and it printed %q", r.out)
	}
	if len(r.store.writes) != 0 {
		t.Errorf("the roster was written although the caller's own binding was refused: the admin entry is a claim about a live binding (D2823), and a roster written without one names an admin who holds nothing")
	}
	if len(r.audit.events) != 0 {
		t.Errorf("an audit entry was created for a workspace whose roster was never written")
	}
	if got := ErrorText(r.err); !strings.Contains(got, "permission denied") {
		t.Errorf("the refusal reads %q, and it has to carry what the binding call answered: the person has to see whether the refusal was a permission, an account nobody named, or the API's own words", got)
	}
	if ExitCode(r.err) != ExitUserError {
		t.Errorf("the refusal exits %d, and an account this credential cannot write is the caller's own state to fix", ExitCode(r.err))
	}
}

// TestWorkspaceInitRefusesALostRace is C1's create-once contract at the one
// place a race really lands: the read answered that the node was absent, and by
// the write another writer had made it, so the database refuses the revision
// (rtdb.ErrPrecondition). That is an ANSWER and not a failure (D2817, R6) — a
// workspace holds one mechanism — so this command refuses, says the roster it
// built was not written, and names what is already real.
func TestWorkspaceInitRefusesALostRace(t *testing.T) {
	r := wu24Init(t, func(r *wu24Rig) { r.store.writeErr = rtdb.ErrPrecondition })
	if r.err == nil {
		t.Fatalf("workspace init finished although its conditional write was refused, and it printed %q", r.out)
	}
	if len(r.store.writes) != 0 {
		t.Errorf("the roster was written although the write was refused under the revision this command read: a lost race leaves the winner's roster and nothing of this one")
	}
	if len(r.audit.events) != 0 {
		t.Errorf("workspace init created %d audit entries for a roster that was never written: an entry is written after the change it records and never instead of it (C1's contract)", len(r.audit.events))
	}
	got := ErrorText(r.err)
	if !strings.Contains(got, "was NOT written") {
		t.Errorf("the refusal reads %q, and it has to say the roster was not written: the node holds the winner's record, and a person who cannot tell a lost race from a plain failure goes looking for a broken workspace", got)
	}
	if !strings.Contains(got, r.store.path) {
		t.Errorf("the refusal reads %q and does not name the node %q the race was lost on", got, r.store.path)
	}
	if !strings.Contains(got, r.folders.folder) {
		t.Errorf("the refusal reads %q and does not name the folder %q that was created: the folder and its owner binding are real whatever the roster does, and a person told only that the write failed would not know to look at them", got, r.folders.folder)
	}
	if ExitCode(r.err) != ExitUserError {
		t.Errorf("a lost race exits %d, and another writer creating this workspace first is the caller's own state to read rather than a fault of the program", ExitCode(r.err))
	}
}

// TestWorkspaceInitWritesNothingWhenASeamFails covers the four calls whose
// failure is not an answer: the roster read (C1's contract is explicit — a read
// that FAILS is not "no roster", and every caller refuses on it), the
// organization search, the folder create, and the roster write itself. A
// workspace is created whole or not at all, so each of them refuses with the
// call's own words and leaves no create-once roster behind; where the folder is
// already real, the refusal says so.
func TestWorkspaceInitWritesNothingWhenASeamFails(t *testing.T) {
	for _, tc := range []struct {
		branch  string
		setup   func(r *wu24Rig)
		want    string
		folders int
		real    bool
	}{
		{
			branch: "roster read",
			setup:  func(r *wu24Rig) { r.store.readErr = errors.New("the roster node answered 403") },
			want:   "the roster node answered 403",
		},
		{
			branch: "organization search",
			setup:  func(r *wu24Rig) { r.folders.orgErr = errors.New("the organization search answered 503") },
			want:   "the organization search answered 503",
		},
		{
			branch:  "folder create",
			setup:   func(r *wu24Rig) { r.folders.createErr = errors.New("the folder quota is exhausted") },
			want:    "the folder quota is exhausted",
			folders: 1,
		},
		{
			branch:  "roster write",
			setup:   func(r *wu24Rig) { r.store.writeErr = errors.New("the database is unavailable") },
			want:    "the database is unavailable",
			folders: 1,
			real:    true,
		},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			r := wu24Init(t, tc.setup)
			if r.err == nil {
				t.Fatalf("workspace init finished although the %s failed, and it printed %q", tc.branch, r.out)
			}
			if len(r.store.writes) != 0 {
				t.Errorf("the roster was written although the %s failed: this node is the workspace's create-once record, and a half-created workspace is one no later command can finish (C1's contract)", tc.branch)
			}
			if len(r.audit.events) != 0 {
				t.Errorf("an audit entry was created for a workspace whose %s failed and whose roster was never written", tc.branch)
			}
			if len(r.auth.admins) != 0 {
				t.Errorf("the caller's own binding was made although the %s failed: nothing holds an admin entry when no roster lands (D2823)", tc.branch)
			}
			if len(r.folders.calls) != tc.folders {
				t.Errorf("workspace init made %d folder creates with the %s failing, and this branch reaches the folder %d times", len(r.folders.calls), tc.branch, tc.folders)
			}
			got := ErrorText(r.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("the refusal reads %q, and it has to carry what the failing call answered (%q): the person has to see whether it was a permission, a state, or the API's own words", got, tc.want)
			}
			if tc.real && !strings.Contains(got, r.folders.folder) {
				t.Errorf("the refusal reads %q and does not name the folder %q that already exists: what is real survives the failed write, and a refusal that says only what failed hides it", got, r.folders.folder)
			}
		})
	}
}

func TestWorkspaceInitRefusesAMachineAsTheFirstAdmin(t *testing.T) {
	const machine = "fugaro-ws@aurora.iam.gserviceaccount.com"
	r := wu24Init(t, func(r *wu24Rig) { r.caller = machine })
	if r.err == nil {
		t.Fatalf("workspace init recorded a service account as the workspace's first person and admin (%s), and the roster's own reader refuses a machine as an admin: a machine cannot hold the binding D2823 confirms one by", machine)
	}
	r.nothingCreated("the credentials are a machine's")
	if got := ErrorText(r.err); !strings.Contains(got, "service account") {
		t.Errorf("the refusal reads %q, and it has to say that the credentials are a service account's: the fix is to run this as the person who will administer the workspace", got)
	}
}

func TestWorkspaceInitRefusesAnotherWorkspacesNode(t *testing.T) {
	r := wu24Init(t, func(r *wu24Rig) { r.store.path = "workspaceRosters/borealis" })
	if r.err == nil {
		t.Fatalf("workspace init wrote a node that is not the workspace it was given: the store holds %q and the workspace is %q", r.store.path, r.name)
	}
	r.nothingCreated("the seam holds another workspace's node")
	if got := ErrorText(r.err); !strings.Contains(got, r.store.path) {
		t.Errorf("the refusal reads %q, and it names neither node: a wiring that hands one workspace's node to another workspace's `init` is what this refusal is for, and the path is how it is seen", got)
	}
}
