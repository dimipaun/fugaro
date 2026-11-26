package cli

// workspace_wiring.go is I1: the join between this family's commands and the
// concrete sides the plan's other tasks built. Every declaration below is one of
// three things -- a command getting the sides it acts through, an adapter from a
// concrete side onto the interface its task declared (C1's Store and AuditSink,
// C2's Authority, MemberOps, StateWriter and CredentialProbe, and WU18's folder
// read), or the reporter wrapper every mutating command of this family is
// wrapped in (D1354).
//
// THE SIDES COME FROM A SELECTION AND ARE OPENED LAZILY. A workspace command
// acts for one of two machines, and which one is the config the command
// selected: the workspace's OWN machine, whose config is the config of the
// project the workspace is named after -- where the member table, the cap, the
// roster node, the audit trail and the mechanism live (D2821, D2823, R5, R8) --
// and a MEMBER's machine, which is where the member owner's own two acts,
// `project link` and `unlink`, run (D2819). Nothing is opened while the command
// tree is built and nothing is opened before the command's own fences: each part
// of a side (the database, the roster node, the mechanism, the member writer)
// is built on first use and remembered, so a command that refuses early -- an
// agent's session (A7), a missing flag, a name that is not a project -- touches
// no cloud at all.
//
// TWO COMMANDS RESOLVE THEIR SELECTION INSIDE THEIR BODY, NOT AT BUILD TIME.
// `link` and `unlink` carry --config and --project, and their sides are fixed
// values their constructors took (workspaceLinkDeps is a struct by value, so a
// side cannot be swapped afterwards: see workspaceLinkWiring). Their sides are
// therefore handed over empty, pointed at the flags, and resolved on the first
// use inside the run. Resolving them when the flags were parsed would read this
// machine's config before the body's own fences, and every write command of this
// family refuses in a coding agent's session FIRST (A7, D1590), so that order is
// the one thing that must not change.

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspaceReported wraps one mutating command of this family in the reporter:
// its own step, the log filed under the dashed command name (D1351) and the
// closing line. The name is a LITERAL at each call site, which is what the
// census reads (report_census_test.go matches the constructor call's second
// argument): a name computed there is a name nothing holds to the tree.
func workspaceReported(cmd *cobra.Command, name string) *cobra.Command {
	return workspaceReportedWith(cmd, name, nil)
}

// workspaceReportedWith is workspaceReported with a hook that runs once the
// flags are parsed and before the command's own body. It is how a command whose
// sides are fixed values -- one built with a deps struct rather than with seams
// -- is pointed at the config the command itself selected: a --config read while
// the tree is built is a flag nobody has parsed yet.
//
// The hook reads flags and nothing else. What it points at is resolved on the
// first use inside the body, so the body's fences still come first.
func workspaceReportedWith(cmd *cobra.Command, name string, point func(*cobra.Command) error) *cobra.Command {
	inner := cmd.RunE
	if inner == nil {
		return cmd
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		run := stepRunFor(c, name, c.OutOrStdout())
		defer run.close()
		step := run.rep.Step(name)
		if point != nil {
			if err := point(c); err != nil {
				step.Failed(err, nil)
				return err
			}
		}
		if err := inner(c, args); err != nil {
			step.Failed(err, nil)
			return err
		}
		step.Done("")
		return nil
	}
	return cmd
}

// newWorkspaceProjectCmd is `fugaro workspace project`: the projects the
// workspace holds, and the machinery that holds them. It is a parent with no
// body of its own, as this family's other groups are.
func newWorkspaceProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "The projects this workspace holds, and the machinery that holds them",
		Long: `Work with the projects a workspace holds.

add, rm, save and reconcile are the workspace owner's own commands and run on the
machine that holds the member table -- the config of the project the workspace is
named after (D2821). link and unlink are the member owner's own consent and run
on the MEMBER's machine (D2819). ls and status read the members of the project
this machine selected.

Nothing here asks a question, and every write refuses in a coding agent's session
(A7): the membership of a workspace and the people in it are a person's own act
at their own terminal.`,
	}
	rm := workspaceAmbientSide("fugaro workspace project rm")
	save := workspaceAmbientSide("fugaro workspace project save")
	reconcile := workspaceAmbientSide("fugaro workspace project reconcile")
	sides := func(s *workspaceOwnerSide) workspaceSides { return workspaceSides{side: s} }
	link, unlink := &workspaceLinkWiring{}, &workspaceLinkWiring{}
	cmd.AddCommand(
		workspaceReported(newWorkspaceProjectAddCmd(workspaceProjectAddSeamsFor()), "workspace-project-add"),
		newWorkspaceProjectLsCmd(),
		newWorkspaceProjectStatusCmd(),
		workspaceReportedWith(newWorkspaceProjectRmCmd(workspaceProjectRmDeps{
			Roster:    sides(rm),
			Audit:     sides(rm),
			Authority: sides(rm),
			Members:   sides(rm),
			State:     sides(rm),
		}), "workspace-project-rm", workspaceAmbientPoint(rm)),
		workspaceReportedWith(newWorkspaceProjectSaveCmd(workspaceProjectSaveDeps{
			Owner:  save.lc,
			Store:  sides(save),
			Writer: sides(save),
		}), "workspace-project-save", workspaceAmbientPoint(save)),
		workspaceReportedWith(newWorkspaceProjectReconcileCmd(workspaceReconcileDeps{
			Workspace: reconcile.name,
			Members:   workspaceMemberLookup(reconcile),
			Drift:     workspaceDriftFunc(reconcile),
			Ops:       sides(reconcile),
			Admins:    workspaceAdminBindings(reconcile),
			Audit:     sides(reconcile),
		}), "workspace-project-reconcile", workspaceAmbientPoint(reconcile)),
		workspaceReportedWith(newWorkspaceProjectLinkCmd(workspaceLinkDeps{
			Authority: sides(&link.owner),
			Members:   sides(&link.owner),
			Folders:   sides(&link.owner),
			Now:       time.Now,
		}), "workspace-project-link", link.point),
		workspaceReportedWith(newWorkspaceProjectUnlinkCmd(workspaceLinkDeps{
			Authority: sides(&unlink.owner),
			Members:   sides(&unlink.owner),
			Folders:   sides(&unlink.owner),
			Now:       time.Now,
		}), "workspace-project-unlink", unlink.point),
	)
	return cmd
}

// workspaceNameOf is the workspace a config belongs to: a member's own config
// names it in its membership key (W1), and the workspace's own config IS the
// config of the project the workspace is named after (F21, D1290).
func workspaceNameOf(lc *localcfg.Config) string {
	if lc == nil {
		return ""
	}
	if lc.Workspace != "" {
		return lc.Workspace
	}
	return lc.Name
}

// workspaceRosterStore adapts WU1's node store onto C1's Store: the node carries
// the bytes and the ETag in one value, where the contract asks for the two
// answers separately. Nothing else differs -- the write is the store's own
// conditional one, made under the revision Read answered.
type workspaceRosterStore struct {
	store *workspace.RosterStore
}

func (s workspaceRosterStore) Path() string { return s.store.Path() }

func (s workspaceRosterStore) Read(ctx context.Context) (workspace.Revision, []byte, error) {
	node, found, err := s.store.Read(ctx)
	if err != nil {
		return workspace.Revision{}, nil, err
	}
	return workspace.Revision{ETag: node.ETag, Found: found}, node.Raw, nil
}

func (s workspaceRosterStore) Write(ctx context.Context, rev workspace.Revision, roster []byte) error {
	return s.store.Write(ctx, workspace.RosterNode{ETag: rev.ETag}, roster)
}

// workspaceRosterAt reads a workspace's roster, decoded. A node that is not
// there is refused rather than read as an empty workspace: the roster is what
// `fugaro workspace init` writes first (WU24), and every command that needs the
// mechanism, the folder or the people reads it here.
func workspaceRosterAt(ctx context.Context, store *workspace.RosterStore) (*workspace.Roster, error) {
	node, found, err := store.Read(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, userErr("workspace %s has no roster at %s: it is what `fugaro workspace init %s` writes first, and until it is there the workspace has no mechanism and no people to read", store.Name(), store.Path(), store.Name())
	}
	roster, err := workspace.DecodeRoster(node.Raw)
	if err != nil {
		return nil, userErr("workspace %s's roster at %s: %v", store.Name(), store.Path(), err)
	}
	return roster, nil
}

// workspaceFolderAuthority adapts WU2's FolderAuthority onto C2's Authority: the
// folder side names a member by FolderMember -- its two projects -- where the
// contract names a MemberTarget and asks a yes or no about it. The rest of the
// interface is the embedded value's own, which is why this is an adapter of four
// methods and not a wrapper of eight.
type workspaceFolderAuthority struct {
	*workspace.FolderAuthority
}

var _ workspace.Authority = workspaceFolderAuthority{}

func folderMemberOf(m workspace.MemberTarget) workspace.FolderMember {
	return workspace.FolderMember{Project: m.Project, Firebase: m.Firebase}
}

func (a workspaceFolderAuthority) Kind() workspace.AuthorityKind { return workspace.AuthorityFolder }

func (a workspaceFolderAuthority) Link(ctx context.Context, m workspace.MemberTarget) error {
	return a.FolderAuthority.Link(ctx, folderMemberOf(m))
}

func (a workspaceFolderAuthority) Unlink(ctx context.Context, m workspace.MemberTarget) error {
	return a.FolderAuthority.Unlink(ctx, folderMemberOf(m))
}

func (a workspaceFolderAuthority) Holds(ctx context.Context, m workspace.MemberTarget) (bool, error) {
	hold, err := a.FolderAuthority.Holds(ctx, folderMemberOf(m))
	if err != nil {
		return false, err
	}
	return hold.InFolder, nil
}

// workspaceAuthorityFor is the mechanism a roster names, built on o's own
// config: the folder mechanism moves projects with Resource Manager (WU2), and
// the service-account fallback grants the workspace's IAM account owner on them
// (WU20).
//
// The folder authority is built with NO member database, and that one gap is
// deliberate: WU2's link record is the MEMBER's own node (R14), so the value
// that moves a member is built per member (workspaceOwnerSide.memberAuthority)
// and this one serves Kind, AddAdmin, RemoveAdmin, AdminBindings and Holds --
// none of which reads that record.
func workspaceAuthorityFor(ctx context.Context, name string, o *workspaceOwnerSide, roster *workspace.Roster) (workspace.Authority, error) {
	switch roster.Mechanism {
	case workspace.MechanismFolder:
		if roster.FolderID == "" {
			return nil, userErr("workspace %s's roster records the folder mechanism and names no folder (D2803, R6): which folder its members are moved into cannot be guessed, so the roster has to be corrected by hand before anything is moved", name)
		}
		folders, err := o.folderClient(ctx)
		if err != nil {
			return nil, err
		}
		return workspaceFolderAuthority{FolderAuthority: &workspace.FolderAuthority{
			Workspace: name,
			Folder:    roster.FolderID,
			CRM:       folders,
			Out:       o.out,
		}}, nil
	case workspace.MechanismSA:
		cl, err := o.clients(ctx)
		if err != nil {
			return nil, err
		}
		return &workspace.SAAuthority{
			Workspace: name,
			Account:   workspaceIAMAccount(name),
			Projects:  infra.NewSAProjects(cl.CRM, cl.IAM),
			Out:       o.out,
		}, nil
	}
	return nil, userErr("workspace %s's roster records the mechanism %q, which this build does not know: a workspace holds its members by a folder or by its own IAM account (D2803), and nothing acts on a mechanism it cannot name", name, string(roster.Mechanism))
}

// workspaceOwnerSide is one workspace's own side of the world: the config of the
// project the workspace is named after, and everything opened from it. Every
// field below the config is a cache filled on first use (see this file's own
// doc), and err remembers the first failure so that a second caller is told the
// same thing rather than opening a second time.
type workspaceOwnerSide struct {
	name string
	out  io.Writer
	// resolve is how a side whose selection is a command's own flags learns it:
	// `link` and `unlink` are handed their sides before their flags are parsed,
	// and this runs once, on the first use inside the body.
	resolve func(ctx context.Context) error

	lc   *localcfg.Config
	db   *rtdb.Client
	fs   *firestore.Client
	auth workspace.Authority
	// memberWriter is the writer of the MEMBER's own documents, for the two
	// commands that act from the member's machine; nil is the workspace's own
	// side, whose member writer is built from the workspace's own config.
	memberWriter *workspaceMemberOps
	ops          *workspaceMemberOps
	folderAPI    *infra.Folders
	cl           *infra.Clients
	err          error
	resolved     bool
}

// openWorkspaceOwner opens the workspace's own side from a workspace's name, and
// refuses a name this machine holds no config for with WU5's own sentence
// (workspaceOwnerConfig).
func openWorkspaceOwner(name string, out io.Writer) (*workspaceOwnerSide, error) {
	if name == "" {
		return nil, userErr("no workspace is named: a workspace command acts on one workspace, which is the project the workspace is named after (F21)")
	}
	lc, _, err := workspaceOwnerConfig(name)
	if err != nil {
		return nil, err
	}
	return &workspaceOwnerSide{name: name, out: out, lc: lc, resolved: true}, nil
}

// workspaceAmbientSide opens the workspace's own side from the ambient
// selection, which is the selection `project rm`, `save` and `reconcile` make:
// they carry neither --config nor --project, so the selection is local files,
// the checkout and the environment, it is the same value at build time as at run
// time, and it never touches a cloud.
//
// A selection that fails is CARRIED and not printed: the command's own fences
// come first, and its refusal for an unreadable config is the same sentence it
// would print at the moment it read one.
//
// The writer it is built with is io.Discard, which is a placeholder and not a
// decision: the side is a fixed value built while the tree is built, and the
// writer that belongs to it is the command's own, which is not known until the
// command runs. workspaceAmbientPoint puts it there.
func workspaceAmbientSide(what string) *workspaceOwnerSide {
	_, lc, err := settingsLocalFile(what)
	if err != nil {
		return &workspaceOwnerSide{out: io.Discard, err: userErr("%v", err), resolved: true}
	}
	return &workspaceOwnerSide{out: io.Discard, lc: lc, name: workspaceNameOf(lc), resolved: true}
}

// workspaceAmbientPoint points an ambient side at the output of the command
// that ran it.
//
// What prints through it is not a detail of the screen: WU15's writer reports
// the file it wrote, and, before anything is pushed, the repository a snapshot
// was aimed at and the layer that aimed it (D2824) -- the one thing nothing can
// read back afterwards -- and WU6's save before a removal is the same writer
// under another command. Discarded, that report is a save whose target the
// operator never sees.
//
// It reads the command and nothing else, so the body's own fences still come
// first (A7), and it resolves nothing: the selection was made when the tree was
// built.
func workspaceAmbientPoint(side *workspaceOwnerSide) func(*cobra.Command) error {
	return func(c *cobra.Command) error {
		side.out = c.OutOrStdout()
		return nil
	}
}

// ensure runs the side's own resolution once, if it has one.
func (o *workspaceOwnerSide) ensure(ctx context.Context) error {
	if o.resolved {
		return o.err
	}
	o.resolved = true
	if o.resolve != nil {
		o.err = o.resolve(ctx)
	}
	if o.err == nil && o.lc == nil {
		o.err = userErr("no project config was selected, so there is no workspace here: a workspace command acts on the config of the project the workspace is named after (D2821)")
	}
	return o.err
}

// database is the selected project's own database: where the roster node lives
// (R5), where the workspace's spend counters are, and where its credential
// service keeps the marks a credential is broken or held under.
func (o *workspaceOwnerSide) database(ctx context.Context) (*rtdb.Client, error) {
	if o.db != nil {
		return o.db, nil
	}
	if err := o.ensure(ctx); err != nil {
		return nil, err
	}
	_, db, err := budgetDBFor(ctx, o.lc)
	if err != nil {
		o.err = err
		return nil, err
	}
	o.db = db
	return db, nil
}

// rosterNode is the workspace's roster node, read and written conditionally.
func (o *workspaceOwnerSide) rosterNode(ctx context.Context) (*workspace.RosterStore, error) {
	db, err := o.database(ctx)
	if err != nil {
		return nil, err
	}
	name := o.name
	if name == "" {
		name = workspaceNameOf(o.lc)
	}
	store, err := workspace.NewRosterStore(db, name)
	if err != nil {
		o.err = userErr("%v", err)
		return nil, o.err
	}
	o.name = name
	return store, nil
}

// rosterValue is the roster as the mechanism and the people, read once.
func (o *workspaceOwnerSide) rosterValue(ctx context.Context) (*workspace.Roster, error) {
	store, err := o.rosterNode(ctx)
	if err != nil {
		return nil, err
	}
	return workspaceRosterAt(ctx, store)
}

// firestoreClient is the selected project's Firestore, opened once: where the
// workspace's audit trail is created and read (D2811).
func (o *workspaceOwnerSide) firestoreClient(ctx context.Context) (*firestore.Client, error) {
	if o.fs != nil {
		return o.fs, nil
	}
	if err := o.ensure(ctx); err != nil {
		return nil, err
	}
	fs, err := newReportFirestore(ctx, o.lc)
	if err != nil {
		o.err = err
		return nil, err
	}
	o.fs = fs
	return fs, nil
}

// auditSink is the workspace's own trail (WU1's AuditWriter).
func (o *workspaceOwnerSide) auditSink(ctx context.Context) (workspace.AuditSink, error) {
	fs, err := o.firestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	return workspace.NewAuditWriter(fs), nil
}

// mechanism is the authority the roster names, opened once.
func (o *workspaceOwnerSide) mechanism(ctx context.Context) (workspace.Authority, error) {
	if o.auth != nil {
		return o.auth, nil
	}
	roster, err := o.rosterValue(ctx)
	if err != nil {
		o.err = err
		return nil, err
	}
	auth, err := workspaceAuthorityFor(ctx, o.name, o, roster)
	if err != nil {
		o.err = err
		return nil, err
	}
	o.auth = auth
	return auth, nil
}

// memberAuthority is the mechanism as ONE member is acted on through it: the
// folder mechanism records where the member's projects were in the MEMBER's own
// database (R14), so the value that moves a member carries that member's
// database and is built per member. The service-account mechanism moves nothing
// and records nothing, so it is one value for every member.
func (o *workspaceOwnerSide) memberAuthority(ctx context.Context, m workspace.MemberTarget) (workspace.Authority, error) {
	auth, err := o.mechanism(ctx)
	if err != nil {
		return nil, err
	}
	folder, ok := auth.(workspaceFolderAuthority)
	if !ok {
		return auth, nil
	}
	ops, err := o.memberOps(ctx)
	if err != nil {
		return nil, err
	}
	db, err := ops.memberDatabase(ctx, m)
	if err != nil {
		return nil, err
	}
	folder.FolderAuthority.DB = db
	return folder, nil
}

// memberOps is the member writer this side acts through: the MEMBER's own for
// the commands that run on the member's machine, and otherwise the workspace's
// own config with the workspace's IAM account beside it.
func (o *workspaceOwnerSide) memberOps(ctx context.Context) (*workspaceMemberOps, error) {
	if o.ops != nil {
		return o.ops, nil
	}
	if err := o.ensure(ctx); err != nil {
		return nil, err
	}
	if o.memberWriter != nil {
		o.ops = o.memberWriter
		return o.ops, nil
	}
	o.ops = newWorkspaceMemberOpsFor(o.lc)
	return o.ops, nil
}

// folderClient is Resource Manager's folder surface, opened once: WU2's
// mechanism moves with it, WU24's init creates the workspace's folder with it,
// and WU18's policy report reads a project's parent and the folder's owners
// through it.
func (o *workspaceOwnerSide) folderClient(ctx context.Context) (*infra.Folders, error) {
	if o.folderAPI != nil {
		return o.folderAPI, nil
	}
	if err := o.ensure(ctx); err != nil {
		return nil, err
	}
	cl, err := infra.NewProjectClients(ctx, infra.ProjectEndpoints{
		ResourceManager: o.lc.Endpoints.ResourceManager,
		Firebase:        o.lc.Endpoints.FirebaseManagement,
		Billing:         o.lc.Endpoints.CloudBilling,
		QuotaProject:    o.lc.GCPProject,
	}, o.lc.Endpoints.NoAuth, nil)
	if err != nil {
		o.err = remote(err)
		return nil, o.err
	}
	o.folderAPI = infra.NewFolders(cl.CRM)
	return o.folderAPI, nil
}

// folders is folderClient as WU18's own read sees it.
func (o *workspaceOwnerSide) folders(ctx context.Context) (workspaceLinkFolders, error) {
	return o.folderClient(ctx)
}

// clients is the installation's own clients, opened once: Cloud Audit Logs for
// WU12's cross-check of the trails, and the IAM and Resource Manager surfaces
// the service-account mechanism is granted through.
func (o *workspaceOwnerSide) clients(ctx context.Context) (*infra.Clients, error) {
	if o.cl != nil {
		return o.cl, nil
	}
	if err := o.ensure(ctx); err != nil {
		return nil, err
	}
	cl, err := newInitClients(ctx, o.lc)
	if err != nil {
		o.err = remote(err)
		return nil, o.err
	}
	o.cl = cl
	return cl, nil
}

// workspaceSides is the single value every fixed-deps command of this family is
// handed: it implements each interface those commands take -- C1's Store and
// AuditSink, C2's Authority, MemberOps, StateWriter and CredentialProbe, WU18's
// folder read, and WU7's own-intake sweeper (F21) -- over one workspace's own
// side. One type rather than six wrappers, because every one of them answers
// from the same three reads (the config, its database, its roster) and six
// copies of that reading would be six places to keep in step.
//
// It holds a POINTER to the side, so a value handed to a command before the
// command's flags were parsed is filled in place and the command's own copy of
// it sees the change.
type workspaceSides struct {
	side *workspaceOwnerSide
}

var (
	_ workspace.Store           = workspaceSides{}
	_ workspace.AuditSink       = workspaceSides{}
	_ workspace.Authority       = workspaceSides{}
	_ workspace.MemberOps       = workspaceSides{}
	_ workspace.StateWriter     = workspaceSides{}
	_ workspace.CredentialProbe = workspaceSides{}
	_ workspaceLinkFolders      = workspaceSides{}
	_ workspaceSweeper          = workspaceSides{}
)

// errWorkspaceSideUnwired is what a sides value no command was given answers. It is a
// wiring bug and not a state a person reaches: every command of this family is
// registered with its sides.
var errWorkspaceSideUnwired = errors.New("no workspace side: this command was built without a workspace's own config (a wiring bug, not a state a person reaches)")

// --- C1: the roster node and the trail ---

func (s workspaceSides) Path() string {
	if s.side == nil {
		return ""
	}
	name := s.side.name
	if name == "" {
		return ""
	}
	path, err := workspace.PathRoster(name)
	if err != nil {
		return ""
	}
	return path
}

func (s workspaceSides) Read(ctx context.Context) (workspace.Revision, []byte, error) {
	if s.side == nil {
		return workspace.Revision{}, nil, errWorkspaceSideUnwired
	}
	store, err := s.side.rosterNode(ctx)
	if err != nil {
		return workspace.Revision{}, nil, err
	}
	return workspaceRosterStore{store: store}.Read(ctx)
}

func (s workspaceSides) Write(ctx context.Context, rev workspace.Revision, roster []byte) error {
	if s.side == nil {
		return errWorkspaceSideUnwired
	}
	store, err := s.side.rosterNode(ctx)
	if err != nil {
		return err
	}
	return workspaceRosterStore{store: store}.Write(ctx, rev, roster)
}

func (s workspaceSides) Create(ctx context.Context, id string, event []byte) error {
	if s.side == nil {
		return errWorkspaceSideUnwired
	}
	sink, err := s.side.auditSink(ctx)
	if err != nil {
		return err
	}
	return sink.Create(ctx, id, event)
}

// --- C2: the mechanism ---

// Kind answers the mechanism the workspace's roster records, or the empty kind
// when nothing is resolved (a workspace whose `init` has not run, which C2's
// contract names no constant for). It takes no context, as C2's Authority does,
// so the read it needs is made under a background one and a failure is
// remembered: the call that follows -- Link, Unlink or Holds -- takes a context
// and reports that failure itself.
func (s workspaceSides) Kind() workspace.AuthorityKind {
	if s.side == nil {
		return ""
	}
	auth, err := s.side.mechanism(context.Background())
	if err != nil {
		return ""
	}
	return auth.Kind()
}

func (s workspaceSides) Link(ctx context.Context, m workspace.MemberTarget) error {
	auth, err := s.memberAuthority(ctx, m)
	if err != nil {
		return err
	}
	return auth.Link(ctx, m)
}

func (s workspaceSides) Unlink(ctx context.Context, m workspace.MemberTarget) error {
	auth, err := s.memberAuthority(ctx, m)
	if err != nil {
		return err
	}
	return auth.Unlink(ctx, m)
}

func (s workspaceSides) Holds(ctx context.Context, m workspace.MemberTarget) (bool, error) {
	auth, err := s.mechanism(ctx)
	if err != nil {
		return false, err
	}
	return auth.Holds(ctx, m)
}

func (s workspaceSides) AddAdmin(ctx context.Context, admin string) error {
	auth, err := s.mechanism(ctx)
	if err != nil {
		return err
	}
	return auth.AddAdmin(ctx, admin)
}

func (s workspaceSides) RemoveAdmin(ctx context.Context, admin string) error {
	auth, err := s.mechanism(ctx)
	if err != nil {
		return err
	}
	return auth.RemoveAdmin(ctx, admin)
}

func (s workspaceSides) AdminBindings(ctx context.Context) ([]string, error) {
	auth, err := s.mechanism(ctx)
	if err != nil {
		return nil, err
	}
	return auth.AdminBindings(ctx)
}

// mechanism is the workspace's own mechanism, opened on first use.
func (s workspaceSides) mechanism(ctx context.Context) (workspace.Authority, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	return s.side.mechanism(ctx)
}

func (s workspaceSides) memberAuthority(ctx context.Context, m workspace.MemberTarget) (workspace.Authority, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	return s.side.memberAuthority(ctx, m)
}

// --- C2: the member's own documents ---

func (s workspaceSides) memberOps(ctx context.Context) (*workspaceMemberOps, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	return s.side.memberOps(ctx)
}

func (s workspaceSides) ReadGrants(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	return ops.ReadGrants(ctx, m)
}

func (s workspaceSides) WriteGrants(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return err
	}
	return ops.WriteGrants(ctx, m, doc, raw)
}

func (s workspaceSides) ReadMirror(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	return ops.ReadMirror(ctx, m)
}

func (s workspaceSides) WriteMirror(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return err
	}
	return ops.WriteMirror(ctx, m, doc, raw)
}

func (s workspaceSides) Bindings(ctx context.Context, m workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return nil, err
	}
	return ops.Bindings(ctx, m)
}

func (s workspaceSides) PendingApply(ctx context.Context, m workspace.MemberTarget, want workspace.ApplyWant) (workspace.Pending, error) {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return workspace.Pending{}, err
	}
	return ops.PendingApply(ctx, m, want)
}

func (s workspaceSides) Offboard(ctx context.Context, m workspace.MemberTarget, address string) error {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return err
	}
	return ops.Offboard(ctx, m, address)
}

// Sweep is the WORKSPACE's own intake (F21): the containers a person stored at
// the workspace's project, deleted by that project's intake's own account. It is
// memberSweeper's pattern at the other installation an offboarding reaches, and
// the address is the workspace's own config's -- the project the workspace is
// named after (D2821) -- which is never a member's, exactly as the
// single-installation offboard resolves it (offboardSweep).
func (s workspaceSides) Sweep(ctx context.Context, address string) ([]string, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	if err := s.side.ensure(ctx); err != nil {
		return nil, err
	}
	return offboardSweep(ctx, s.side.lc, address)
}

func (s workspaceSides) Audit(ctx context.Context, m workspace.MemberTarget, id string, event []byte) error {
	ops, err := s.memberOps(ctx)
	if err != nil {
		return err
	}
	return ops.Audit(ctx, m, id, event)
}

// --- C2: the state writer (WU15) ---

func (s workspaceSides) Save(ctx context.Context, snap workspace.Snapshot) (workspace.SnapshotWritten, error) {
	return workspaceSaveWriter{out: s.side.out}.Save(ctx, snap)
}

func (s workspaceSides) LastSaved(ctx context.Context, snap workspace.Snapshot) (time.Time, bool, error) {
	return workspaceSaveWriter{out: s.side.out}.LastSaved(ctx, snap)
}

// --- WU18: the folder read ---

func (s workspaceSides) Parent(ctx context.Context, project string) (string, error) {
	if s.side == nil {
		return "", errWorkspaceSideUnwired
	}
	folders, err := s.side.folders(ctx)
	if err != nil {
		return "", err
	}
	return folders.Parent(ctx, project)
}

func (s workspaceSides) Owners(ctx context.Context, parent string) ([]string, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	folders, err := s.side.folders(ctx)
	if err != nil {
		return nil, err
	}
	return folders.Owners(ctx, parent)
}

// --- WU22: the credential probe ---

func (s workspaceSides) Probe(ctx context.Context, m workspace.MemberTarget) ([]workspace.CredentialResult, error) {
	if s.side == nil {
		return nil, errWorkspaceSideUnwired
	}
	if err := s.side.ensure(ctx); err != nil {
		return nil, err
	}
	return workspaceMemberProbe{probe: NewWorkspaceProbe(s.side.lc)}.Probe(ctx, m)
}

// workspaceMemberProbe is C2's CredentialProbe over WU22's probe: the probe
// takes one GCP project and answers what each credential the member references
// said, and the contract names those answers by kind (D2825). A credential the
// probe does not classify is dropped rather than reported unplaced: C2's kind is
// a closed set of two, and an entry nothing can name is not one a status row can
// print.
type workspaceMemberProbe struct {
	probe *WorkspaceProbe
}

var _ workspace.CredentialProbe = workspaceMemberProbe{}

func (p workspaceMemberProbe) Probe(ctx context.Context, m workspace.MemberTarget) ([]workspace.CredentialResult, error) {
	results, err := p.probe.Probe(ctx, m.Project)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.CredentialResult, 0, len(results))
	for _, r := range results {
		kind, known := probeKindOf(r.Secret)
		if !known {
			continue
		}
		result := workspace.CredentialResult{
			Name:   r.Secret,
			OK:     r.Outcome == ProbePassed,
			Status: r.Detail,
		}
		if kind == probeClaudeAccount {
			result.Kind = workspace.CredentialClaudeAccount
		} else {
			result.Kind = workspace.CredentialModelKey
		}
		out = append(out, result)
	}
	return out, nil
}

// workspaceMemberOps is the MEMBER side of C2: WU4's writer with WU9's
// offboarding on top. It is a type of its own because C2 asks for one method WU4
// deliberately does not carry -- Offboard, whose core is WU9's -- and because
// WU9's core needs the member seam and the member's own intake, which are the
// two things this value is.
type workspaceMemberOps struct {
	*WorkspaceMemberOps
	offboard *memberOffboarder
}

var _ workspace.MemberOps = (*workspaceMemberOps)(nil)

func (o *workspaceMemberOps) Offboard(ctx context.Context, m workspace.MemberTarget, address string) error {
	return o.offboard.Offboard(ctx, m, address)
}

// newWorkspaceMemberOpsFor returns the member writer for lc's project, with the
// workspace's own IAM account beside it: lc is the config of the project whose
// credentials reach the member, which is the workspace's own for every command
// that acts from the workspace owner's machine and the member's own for the two
// that act from the member's (D2819, F11).
func newWorkspaceMemberOpsFor(lc *localcfg.Config) *workspaceMemberOps {
	ops := &workspaceMemberOps{WorkspaceMemberOps: NewWorkspaceMemberOps(lc, workspaceIAMAccount(workspaceNameOf(lc)))}
	ops.offboard = newMemberOffboarder(ops, ops)
	return ops
}

// Sweep is WU4's writer standing in for WU9's sweeper, which is where the plan
// puts it: the intake a member's own secrets are deleted through is that
// member's installation's, and the side that already reaches the member project
// is this one (C2's contract, memberSweeper's own doc). The address is resolved
// from the member's own Firebase state, as every other call here resolves it.
func (o *WorkspaceMemberOps) Sweep(ctx context.Context, m workspace.MemberTarget, address string) ([]string, error) {
	c, err := o.cloud(ctx)
	if err != nil {
		return nil, remote(err)
	}
	outs, applied, err := o.memberInstallationOutputs(ctx, c, m)
	if err != nil {
		return nil, err
	}
	if !applied || outs.Intake == nil || outs.Intake.URL == "" {
		return nil, userErr("nothing was swept: member project %s's installation publishes no intake service, and a person's own secrets are deleted through the intake their own installation runs (DS2.3, F13)", m.Project)
	}
	return offboardSweepVia(ctx, outs.Intake.URL, address)
}

// workspaceMemberTarget is the member one of the workspace's own commands names
// on its command line, as the member-reaching calls name it: the member table is
// keyed by the member's Fugaro project name, and the pair of projects C2 takes
// is read off the entry (F2, R10).
func workspaceMemberTarget(lc *localcfg.Config, project string) (workspace.MemberTarget, error) {
	name := workspaceNameOf(lc)
	table := workspaceTable(lc)
	entry, ok := table[project]
	if !ok {
		held := slices.Sorted(maps.Keys(table))
		if len(held) == 0 {
			return workspace.MemberTarget{}, userErr("workspace %s declares no member: a workspace with none holds nothing, so declare the projects it groups with `fugaro workspace project add`", name)
		}
		return workspace.MemberTarget{}, userErr("workspace %s declares no member named %q: the members it declares are %s", name, project, strings.Join(held, ", "))
	}
	return wsMemberTarget(entry), nil
}

// workspaceMemberLookup is the one read that turns the project named on
// `project reconcile`'s command line into the member the workspace holds.
func workspaceMemberLookup(side *workspaceOwnerSide) func(ctx context.Context, project string) (workspace.MemberTarget, error) {
	return func(ctx context.Context, project string) (workspace.MemberTarget, error) {
		if err := side.ensure(ctx); err != nil {
			return workspace.MemberTarget{}, err
		}
		return workspaceMemberTarget(side.lc, project)
	}
}

// workspaceAdminBindings is C2's `Authority.AdminBindings` for the workspace's
// own side: the members the LIVE bindings name, which is the only thing D2823
// trusts an admin by.
func workspaceAdminBindings(side *workspaceOwnerSide) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		auth, err := side.mechanism(ctx)
		if err != nil {
			return nil, err
		}
		return auth.AdminBindings(ctx)
	}
}

// workspaceLinkedMembers is the members the workspace holds NOW (R11: the member
// owner consented through `link`, and the mechanism's own read says so), which
// is the list a stored grant reaches. It is the member table's entries, each put
// to the mechanism, and it is read once per command rather than per grant.
func workspaceLinkedMembers(ctx context.Context, side *workspaceOwnerSide) ([]workspace.MemberTarget, error) {
	table := workspaceTable(side.lc)
	if len(table) == 0 {
		return nil, nil
	}
	auth, err := side.mechanism(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.MemberTarget, 0, len(table))
	for _, project := range slices.Sorted(maps.Keys(table)) {
		target := wsMemberTarget(table[project])
		held, err := auth.Holds(ctx, target)
		if err != nil {
			return nil, err
		}
		if held {
			out = append(out, target)
		}
	}
	return out, nil
}

// workspaceMemberGrants reads the member's own roster as WU12's comparison wants
// it: the project-scope grants, which are the statements the member's own
// project's IAM is derived from. A grant at another scope is the member's own
// business and drift does not read it (C2's MemberGrant says so).
//
// The bytes are internal/access's format and are read by that package's own
// reader, which is the one door this family has into a member's roster.
func workspaceMemberGrants(ctx context.Context, ops workspace.MemberOps, m workspace.MemberTarget) ([]workspace.MemberGrant, error) {
	doc, err := ops.ReadGrants(ctx, m)
	if err != nil {
		return nil, err
	}
	if !doc.Found {
		return nil, nil
	}
	grants, err := accesslib.Decode(doc.Raw)
	if err != nil {
		return nil, userErr("member project %s's grants node is not a document this build reads: %v", m.Project, err)
	}
	out := make([]workspace.MemberGrant, 0, len(grants))
	for _, g := range grants {
		if g.Scope.Kind != accesslib.ScopeProject {
			continue
		}
		out = append(out, workspace.MemberGrant{User: g.User, Role: workspace.Role(g.Role)})
	}
	return out, nil
}

// workspaceMemberMirror reads the member's mirror (C2's PathMemberMirror), for
// the one thing WU12 does with it: nothing. It is carried into the comparison
// and never compared against (D2813) -- the mirror is advisory and the member's
// own admins hold its write -- and it is read through this file's own reader
// because the document's shape is WU5's and not the contract's.
func workspaceMemberMirror(ctx context.Context, ops workspace.MemberOps, m workspace.MemberTarget) ([]workspace.MirrorEntry, error) {
	doc, err := ops.ReadMirror(ctx, m)
	if err != nil {
		return nil, err
	}
	if !doc.Found {
		return nil, nil
	}
	return DecodeMemberMirror(doc.Raw)
}

// workspaceMemberTrail reads the MEMBER's own trail: the entries its own
// database holds at PathMemberAudit, created and never updated (D2811, C2).
func workspaceMemberTrail(ctx context.Context, ops *workspaceMemberOps, m workspace.MemberTarget) ([]workspace.AuditEvent, error) {
	db, err := ops.memberDatabase(ctx, m)
	if err != nil {
		return nil, err
	}
	var docs map[string]json.RawMessage
	found, err := db.Get(ctx, workspace.PathMemberAudit, &docs)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	out := make([]workspace.AuditEvent, 0, len(docs))
	for _, raw := range docs {
		event, err := workspace.DecodeAuditEvent(raw)
		if err != nil {
			return nil, userErr("an entry of member project %s's trail at %s is not an event this build reads: %v", m.Project, workspace.PathMemberAudit, err)
		}
		out = append(out, event)
	}
	return out, nil
}

// workspaceTrail is both trails a change to one member could be held by: the
// member's own, and the workspace's own for the events it made about that member
// (D2811, R13). The workspace's events are read for THIS member alone -- the
// trail's `project` field names the member a change was about -- so a
// cross-check of one member never reads another's.
func workspaceTrail(ctx context.Context, side *workspaceOwnerSide, ops *workspaceMemberOps, m workspace.MemberTarget) ([]workspace.AuditEvent, error) {
	member, err := workspaceMemberTrail(ctx, ops, m)
	if err != nil {
		return nil, err
	}
	fs, err := side.firestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	docs, err := fs.Query(ctx, workspace.AuditCollection, "project", m.Project, m.Project)
	if err != nil {
		return nil, remote(err)
	}
	out := make([]workspace.AuditEvent, 0, len(docs)+len(member))
	for _, doc := range docs {
		raw, err := json.Marshal(doc.Fields)
		if err != nil {
			return nil, fmt.Errorf("reading the workspace's audit entry %s: %w", doc.ID, err)
		}
		event, err := workspace.DecodeAuditEvent(raw)
		if err != nil {
			return nil, userErr("the workspace's audit entry %s is not an event this build reads: %v", doc.ID, err)
		}
		out = append(out, event)
	}
	return append(out, member...), nil
}

// workspaceMemberDrift is WU12's comparison for one member of one workspace,
// assembled from the reads no one side can make alone (drift.go's own doc says
// so): the record from the workspace's roster, the member's own roster and
// mirror from its database, and the console changes from Cloud Audit Logs.
func workspaceMemberDrift(ctx context.Context, side *workspaceOwnerSide, m workspace.MemberTarget) (workspace.DriftReport, error) {
	roster, err := side.rosterValue(ctx)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	auth, err := side.mechanism(ctx)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	ops, err := side.memberOps(ctx)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	grants, err := workspaceMemberGrants(ctx, ops, m)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	bindings, err := ops.Bindings(ctx, m)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	mirror, err := workspaceMemberMirror(ctx, ops, m)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	trail, err := workspaceTrail(ctx, side, ops, m)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	cl, err := side.clients(ctx)
	if err != nil {
		return workspace.DriftReport{}, err
	}
	return workspace.ComputeDrift(ctx, workspace.DriftSources{
		Workspace: side.name,
		Node:      roster,
		Authority: auth,
		Grants:    grants,
		Bindings:  bindings,
		Mirror:    mirror,
		Trail:     trail,
		Changes:   infra.NewAuditLogs(cl.Logging),
	}, m)
}

// workspaceDriftFunc is C2's Drift for one workspace's own side.
func workspaceDriftFunc(side *workspaceOwnerSide) workspace.Drift {
	return func(ctx context.Context, m workspace.MemberTarget) (workspace.DriftReport, error) {
		return workspaceMemberDrift(ctx, side, m)
	}
}

// workspaceDesignation is WU15's reader of one project's committed-reports
// setting: the E6 designated repository (D2750, D2824) and the source it came
// from, which the save prints before it pushes.
func workspaceDesignation(side *workspaceOwnerSide) stateDesignationReader {
	return workspaceDesignationFor{side: side}
}

type workspaceDesignationFor struct {
	side *workspaceOwnerSide
}

func (d workspaceDesignationFor) Designate(ctx context.Context, _ workspace.Snapshot) (stateDesignation, error) {
	db, err := d.side.database(ctx)
	if err != nil {
		return stateDesignation{}, err
	}
	_, found, raw, err := readPolicyNode(ctx, db, d.side.lc)
	if err != nil {
		return stateDesignation{}, err
	}
	p, err := publishedPolicyOf(raw, found)
	if err != nil {
		return stateDesignation{}, err
	}
	// The two layers meet through the keep rule and not here: a config that
	// states a designation has it, a config that states a written empty block
	// takes the published one away (D987), and a config that says nothing keeps
	// the node's. The line the save prints names WHICH of the two answered,
	// which is the only thing the rule cannot tell a person.
	var published *projectpolicy.ReportsCommit
	if p != nil {
		published = p.ReportsCommit
	}
	eff := d.side.lc.ReportsCommitEffective(published)
	if eff == nil || eff.Repo == "" {
		return stateDesignation{}, userErr("project %s has designated no repository for committed reports, so a snapshot of it has nowhere to go: designate one with `fugaro reports commit set --repo <owner/name>`, or name one for this save with --repo (D2750, D2824)", d.side.lc.Name)
	}
	source := "the project's published policy"
	if d.side.lc.ReportsCommit != nil && !d.side.lc.ReportsCommit.SetsNothing() {
		source = "the project's own configuration"
	}
	repo, err := task.CanonicalRepo(eff.Repo)
	if err != nil {
		return stateDesignation{}, userErr("the repository %s designates for committed reports: %v", oneLine(eff.Repo), err)
	}
	provider := ""
	if entry, ok := d.side.lc.Repos[repo]; ok {
		provider = entry.Provider
	}
	return stateDesignation{Repo: repo, Dir: eff.Dir, Provider: provider, Source: source}, nil
}

// workspaceSaveWriter is C2's StateWriter for `workspace project save` and for
// WU6's save before a removal: one WU15 writer per snapshot, built out of THE
// SNAPSHOT'S OWN project's config.
//
// That is not a detail. A snapshot carries one project's own published settings
// and its spend, the workspace's audit export carries the workspace's, and
// WU15's own content reader refuses a snapshot of another project than the one
// it was built for (stateProjectContent.owns). A save of every member is
// therefore a save by the machine that holds each member's config -- the owner's
// own machine, which is where `setup` put them -- and a member this machine
// holds no config for is refused by name rather than read from the wrong
// project's database.
type workspaceSaveWriter struct {
	out io.Writer
}

var _ workspace.StateWriter = workspaceSaveWriter{}

func (w workspaceSaveWriter) Save(ctx context.Context, s workspace.Snapshot) (workspace.SnapshotWritten, error) {
	writer, err := w.writerFor(ctx, s)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	return writer.Save(ctx, s)
}

func (w workspaceSaveWriter) LastSaved(ctx context.Context, s workspace.Snapshot) (time.Time, bool, error) {
	writer, err := w.writerFor(ctx, s)
	if err != nil {
		return time.Time{}, false, err
	}
	return writer.LastSaved(ctx, s)
}

// writerFor is WU15's writer of the project one snapshot is about: the member
// the snapshot names, or the workspace itself for its audit export.
func (w workspaceSaveWriter) writerFor(ctx context.Context, s workspace.Snapshot) (*stateWriter, error) {
	name := s.Member.Project
	if s.Audit {
		name = s.Workspace
	}
	if name == "" {
		return nil, userErr("this snapshot names no project, so there is no project whose state it is: a member's snapshot names the member, and the audit export names the workspace")
	}
	lc, _, err := localcfg.LoadProject(os.Getenv, name)
	if errors.Is(err, localcfg.ErrMissing) {
		return nil, userErr("this machine holds no config for project %s, so its state cannot be read here: a snapshot carries that project's own published settings and its own spend, and it is read from that project's own configuration and database (D2824)", name)
	}
	if err != nil {
		return nil, userErr("%v", err)
	}
	side := &workspaceOwnerSide{lc: lc, name: name, out: w.out, resolved: true}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	var content stateContentReader = stateProjectContent{lc: lc, db: db}
	if s.Audit {
		fs, err := newReportFirestore(ctx, lc)
		if err != nil {
			return nil, err
		}
		content = stateProjectContent{lc: lc, db: db, fs: fs}
	}
	return newStateWriter(stateWriterOptions{
		Out:         w.out,
		Designation: workspaceDesignation(side),
		Content:     content,
		// A Bitbucket designation pushes with the project's own token, which is
		// read from the credentials service and never from the config: handing
		// "" here is what makes that host refuse by name rather than push with
		// a credential nothing read.
		Hosts: stateHostsFor(""),
	}), nil
}

// workspaceProjectAddSeamsFor is WU5's seams against a real installation. Each
// opens the workspace's own side by the name the command was given -- the name
// its --workspace flag carried -- which is why three of them are functions of
// the name rather than values.
func workspaceProjectAddSeamsFor() workspaceProjectAddSeams {
	side := func(name string, out io.Writer) (*workspaceOwnerSide, error) {
		return openWorkspaceOwner(name, out)
	}
	return workspaceProjectAddSeams{
		store: func(ctx context.Context, ws string) (workspace.Store, error) {
			o, err := side(ws, io.Discard)
			if err != nil {
				return nil, err
			}
			return workspaceSides{side: o}, nil
		},
		audit: func(ctx context.Context, ws string) (workspace.AuditSink, error) {
			o, err := side(ws, io.Discard)
			if err != nil {
				return nil, err
			}
			return workspaceSides{side: o}, nil
		},
		authority: func(ctx context.Context, ws string) (workspace.Authority, error) {
			o, err := side(ws, io.Discard)
			if err != nil {
				return nil, err
			}
			return workspaceSides{side: o}, nil
		},
		member: func(ctx context.Context, lc *localcfg.Config, _ workspace.MemberTarget, _ string) (workspace.MemberOps, error) {
			// The region the owner's table declares (--database-region) is not
			// read here: WU4's writer reaches the member's database through the
			// address that member's own Firebase state publishes, which names
			// the region with it (its memberFirebaseOutputs). The two cannot
			// disagree about one database, and there is no second place to read
			// it from.
			return newWorkspaceMemberOpsFor(lc), nil
		},
		caller: func(ctx context.Context) (string, error) {
			return workspaceCallerAddress(ctx, "fugaro workspace project add")
		},
		now:  time.Now,
		rand: crand.Reader,
	}
}

// workspaceCallerAddress is the address of the person this machine's credentials
// belong to, which is what `workspace init` records as the workspace's first
// admin and what `project add` records as the actor: never the local config's
// own key and never git's user.email (F21, init_members.go).
//
// what is the command reading it, and it is a parameter because the selection
// this reads through is both commands': a sentence that named the other one
// would send the person to a command they did not run.
func workspaceCallerAddress(ctx context.Context, what string) (string, error) {
	_, lc, err := settingsLocalFile(what)
	if err != nil {
		return "", err
	}
	return authenticatedUser(ctx, lc)
}

// workspaceInitDepsFor is WU24's opener: the workspace's own store, its trail,
// the folder side, the mechanism and the caller's own address, for the workspace
// `init` named.
//
// The Authority handed over is the SERVICE-ACCOUNT one, and it is built without
// reading the roster: `init` is what writes the first roster, so there is none
// to read the mechanism from. It is reached on the SA path alone (the folder
// path's admins are the folder's owners, which CreateFolder makes), and the
// value refuses with the workspace's own name if that path is ever taken with an
// account nobody read.
func workspaceInitDepsFor(out io.Writer) workspaceInitDeps {
	return workspaceInitDeps{Open: func(ctx context.Context, name string) (workspaceInitTarget, error) {
		o, err := openWorkspaceOwner(name, out)
		if err != nil {
			return workspaceInitTarget{}, err
		}
		cl, err := o.clients(ctx)
		if err != nil {
			return workspaceInitTarget{}, err
		}
		caller, err := workspaceCallerAddress(ctx, "fugaro workspace init")
		if err != nil {
			return workspaceInitTarget{}, err
		}
		return workspaceInitTarget{
			Store:   workspaceSides{side: o},
			Audit:   workspaceSides{side: o},
			Folders: workspaceInitFolderSide{side: o},
			Authority: &workspace.SAAuthority{
				Workspace: name,
				Account:   workspaceIAMAccount(name),
				Projects:  infra.NewSAProjects(cl.CRM, cl.IAM),
				Out:       out,
			},
			Caller: caller,
		}, nil
	}}
}

// workspaceInitFolderSide is WU24's folder side: the organization search, and
// the one call that creates the workspace's folder, binds the caller to it and
// places the workspace's own project inside it (WU2's CreateFolder does all
// three, which is why `init` cannot create a folder and forget the project).
type workspaceInitFolderSide struct {
	side *workspaceOwnerSide
}

var _ workspaceInitFolders = workspaceInitFolderSide{}

func (f workspaceInitFolderSide) Organizations(ctx context.Context) ([]string, error) {
	folders, err := f.side.folderClient(ctx)
	if err != nil {
		return nil, err
	}
	return folders.Organizations(ctx)
}

func (f workspaceInitFolderSide) CreateFolder(ctx context.Context, parent, owner string) (string, error) {
	if err := f.side.ensure(ctx); err != nil {
		return "", err
	}
	folders, err := f.side.folderClient(ctx)
	if err != nil {
		return "", err
	}
	auth := &workspace.FolderAuthority{
		Workspace: f.side.name,
		CRM:       folders,
		Out:       f.side.out,
	}
	return auth.CreateFolder(ctx, parent, owner)
}

// workspaceRosterOpenFor is WU7's opener: the four sides `workspace user` and
// `workspace domains` act through, for the workspace the --workspace flag named,
// plus the loader that answers the members the workspace holds now (R11).
//
// The authority and the member writer print, and the seam takes no writer of its
// own, so they print where the command does: stdout. `workspace user rm` prints
// its own report, and what the mechanism says about a member is part of the same
// answer.
//
// The members ARE NOT READ HERE. Opening this workspace is a local config read
// and nothing else, so the command's own fences -- an agent's session (A7, D1590),
// a missing flag, a name that is not a project -- still run before the first
// cloud call, and a command that never needs the list (`workspace domains
// add|rm`) makes none.
func workspaceRosterOpenFor() workspaceRosterOpen {
	return func(ctx context.Context, name string) (workspaceRosterDeps, error) {
		o, err := openWorkspaceOwner(name, os.Stdout)
		if err != nil {
			return workspaceRosterDeps{}, err
		}
		sides := workspaceSides{side: o}
		return workspaceRosterDeps{
			Workspace: name,
			Store:     sides,
			Audit:     sides,
			Members:   sides,
			Authority: sides,
			Sweep:     sides,
			Linked:    workspaceLinkedOnce(o),
			getenv:    os.Getenv,
			now:       time.Now,
			rand:      crand.Reader,
		}, nil
	}
}

// workspaceLinkedOnce is workspaceLinkedMembers as the loader a command's
// dependencies hold: the first call resolves the list -- the roster node's
// member table, and one `Holds` call per member -- and every call after it
// answers the same list, or the same error.
//
// Once, because a command reports the count of the members it reached and then
// writes one stored grant per member (D2818): resolving again between those
// would be a second read of the node and a second call per member for a list
// that cannot have changed under a command that has not written it yet.
func workspaceLinkedOnce(side *workspaceOwnerSide) func(context.Context) ([]workspace.MemberTarget, error) {
	var (
		once    sync.Once
		members []workspace.MemberTarget
		err     error
	)
	return func(ctx context.Context) ([]workspace.MemberTarget, error) {
		once.Do(func() { members, err = workspaceLinkedMembers(ctx, side) })
		return members, err
	}
}

// workspaceLinkWiring is the sides `project link` and `project unlink` act
// through, pointed at the two configs they need and resolved on the first use
// inside the body.
//
// The two commands are the MEMBER owner's acts (D2800, D2819) and they are the
// one place this family needs both sides at once: the mechanism and the folder
// the workspace holds its members by are written in the WORKSPACE's own roster
// (R5, D2803) and in no member's config, and the member's own mirror and trail
// are in the MEMBER's own database (R14, D2811). So the authority is built from
// the workspace's own config, and the member writer from the member's.
//
// A machine that holds no config for the workspace's own project is refused by
// name, and that refusal is the design's own edge rather than a shortcut: F11
// gives a member's machine no grant on the workspace's project at all, so a
// member cannot read the workspace's roster from its own side either. A member
// owner who also holds the workspace's config -- which is who runs this on a
// machine where both are set up -- gets the whole command.
type workspaceLinkWiring struct {
	owner workspaceOwnerSide
}

// point reads the command's flags and points the side at them. It resolves
// nothing: the resolution below runs inside the body, after its fences.
func (w *workspaceLinkWiring) point(c *cobra.Command) error {
	config, err := c.Flags().GetString("config")
	if err != nil {
		return err
	}
	project, err := c.Flags().GetString("project")
	if err != nil {
		return err
	}
	out := c.OutOrStdout()
	w.owner.out = out
	w.owner.resolved = false
	w.owner.resolve = func(ctx context.Context) error {
		_, member, err := selectProject(ctx, cloudOptions{config: config, project: project, stderr: c.ErrOrStderr})
		if err != nil {
			return err
		}
		name := workspaceNameOf(member)
		if member.Workspace == "" || name == "" {
			return userErr("the selected config names no workspace, so there is no workspace this project belongs to: `fugaro workspace project link` and `fugaro workspace project unlink` read the workspace from this machine's own config (the `workspace:` key, spec §2), and this one states none")
		}
		ownerLC, _, err := workspaceOwnerConfig(name)
		if err != nil {
			return userErr("this machine holds no config for workspace %s's own project, and that is where the mechanism and the folder a link gives away are written: they are part of the workspace's own roster (R5, D2803) and of no member's config, and a member's machine holds neither that file nor a grant that could read the node (F11). Run this on a machine that holds the workspace's own config, or have the workspace's owner record the member on theirs first (fugaro workspace project add %s --workspace %s)", name, member.Name, name)
		}
		w.owner.lc = ownerLC
		w.owner.name = name
		w.owner.memberWriter = newWorkspaceMemberOpsFor(member)
		return nil
	}
	return nil
}

// init installs the C1 and C2 sides `workspace project ls` and `status` read
// through. WU11 keeps them behind a variable so that its two constructors stay
// argument-free, and this is the one assignment it waits for.
func init() {
	workspaceProjectWiring = func(lc *localcfg.Config) workspaceProjectSides {
		side := func() *workspaceOwnerSide {
			return &workspaceOwnerSide{lc: lc, name: workspaceNameOf(lc), out: io.Discard, resolved: true}
		}
		return workspaceProjectSides{
			// The store is named separately from the config: `status` reads the
			// roster of the project this machine selected, whose name is the
			// workspace's (F21).
			Store: func(ctx context.Context, lc *localcfg.Config, name string) (workspace.Store, error) {
				return workspaceSides{side: &workspaceOwnerSide{lc: lc, name: name, out: io.Discard, resolved: true}}, nil
			},
			Authority: func(ctx context.Context, lc *localcfg.Config) (workspace.Authority, error) {
				return workspaceSides{side: side()}, nil
			},
			Member: func(ctx context.Context, lc *localcfg.Config) (workspace.MemberOps, error) {
				return workspaceSides{side: side()}, nil
			},
			Writer: func(ctx context.Context, lc *localcfg.Config) (workspace.StateWriter, error) {
				return workspaceSides{side: side()}, nil
			},
			Drift: func(ctx context.Context, lc *localcfg.Config) (workspace.Drift, error) {
				return workspaceDriftFunc(side()), nil
			},
			Probe: func(ctx context.Context, lc *localcfg.Config) (workspace.CredentialProbe, error) {
				return workspaceSides{side: side()}, nil
			},
		}
	}
}
