package cli

// workspace_init.go is WU24: `fugaro workspace init`, the workspace's first
// write (D2803, D2818, R6, R8; spec.md §1).
//
// One command creates a workspace, and it creates all of it: the mechanism
// (D2803's folder where the caller's own credentials can see an organization to
// create one in, and the SA model where they cannot), the folder itself with
// the caller as its owner and the workspace's own project placed in it (R8: the
// roster node and the workspace's audit trail live in that project), the live
// binding that makes the caller the workspace's first admin, the roster that
// records the mechanism, the folder and the caller, and one `init` entry in the
// workspace's own audit trail. Nothing here asks a question: the mechanism is
// detected rather than chosen, and a workspace whose roster is already there is
// refused instead of prompted about.
//
// Four things about the ORDER are the whole of the care, and each of them is a
// refusal rather than a repair:
//
//   - The refusal that comes before everything is the one about who is running
//     (A7): this command creates a cloud folder and a create-once record with
//     the caller's own credentials, so a coding agent's session is turned away
//     before a client is built, before the roster is read and before the
//     organization search is made. The `GODEBUG=http2debug` refusal follows it,
//     as it does in every command that opens a cloud client.
//   - The roster is READ first, and the command stops if it is there. The node
//     is the workspace's own record that it exists (D2817), its mechanism and
//     its folder are never changed afterwards (R6), and the read is what makes
//     a second `init` create nothing at all rather than create a second folder.
//   - The live binding is made BEFORE the roster is written. An `Admins` entry
//     is a claim about a binding that lives in IAM and not a permission (D2823),
//     so a roster written first would name an admin who holds nothing if the
//     binding call were then refused. This is also why a failure after the
//     binding reports what is already real: the folder and the owner on it, or
//     the account's token creator, exist whatever the roster does.
//   - The audit entry is created AFTER the roster lands (C1's contract: an entry
//     is written after the change it records, never instead of it), and the id
//     is minted per event, so the trail is create-only and never rewritten
//     (D2811). An entry that cannot be created is reported with what did land,
//     because the trail is the fence and a missing line is not a thing a later
//     command writes.
//
// What this file does NOT carry is the wiring (I1's
// internal/cli/workspace_wiring.go): a workspace name is turned into the cloud
// seams by workspaceInitDeps.Open, which is also where the caller's own address
// is read from the credentials. The constructor is unregistered on purpose
// (the plan's "constructor only"): I1 registers it on `workspace`, wraps it
// with the reporter (D1354) and adds its census row in the same change.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// saAddressSuffix is what a service account's address ends in, and the one
// address shape this command refuses as the caller. It is the same check
// userEmail makes of the credentials (init_members.go), restated here because
// this is the other side of it: a wiring that handed an address it did not
// check would have `init` record a machine as the workspace's first PERSON, and
// the roster would carry a claim its own reader cannot see through — an address
// is an address, and only the shape says which of the two kinds it is.
const saAddressSuffix = ".gserviceaccount.com"

// workspaceInitFolders is the folder side of `init`: the organization search
// that detects which mechanism this workspace gets (D2803), and the one call
// WU2's folder authority makes for it — create the workspace's folder under the
// chosen organization, bind the caller to `roles/owner` on it, and place the
// workspace's own project in it (R8).
//
// It is declared here, in the task's own file, because it is nothing but the
// two calls this command makes: WU2's authority is the type that owns the
// second one (*workspace.FolderAuthority's CreateFolder) and *infra.Folders
// owns the first (internal/infra/folder.go, written for this detection), and
// the two packages cannot name each other's types. I1's wiring joins them in
// one small adapter — there is no interface to add to a contract here, and the
// placement of the workspace's own project is inside CreateFolder precisely so
// that `init` cannot make a folder and forget to put the project in it.
type workspaceInitFolders interface {
	// Organizations lists the organizations the caller's own credentials can
	// see, which is the whole of D2803's detection: an organization this
	// credential cannot see is one it cannot create a folder in. A caller with
	// no organization gets an empty list and not an error.
	Organizations(ctx context.Context) ([]string, error)
	// CreateFolder creates the workspace's folder under parent with owner bound
	// to roles/owner on it, places the workspace's own project in it, and
	// answers the folder's id (`folders/<number>`). A workspace that already has
	// a folder is refused rather than given a second one.
	CreateFolder(ctx context.Context, parent, owner string) (string, error)
}

// workspaceInitTarget is one workspace as `init` reaches it.
type workspaceInitTarget struct {
	// Store is the workspace's own roster node (C1) and Audit its audit trail:
	// both are the workspace's own project's, which is where R8 puts them.
	Store workspace.Store
	Audit workspace.AuditSink
	// Folders is the folder side above, which is reached only on the folder
	// path.
	Folders workspaceInitFolders
	// Authority is the mechanism's authority (C2) and the command's one call on
	// it is AddAdmin for the caller. It is reached only on the SA path: on the
	// folder path the admin's own binding is the folder's owner, which
	// CreateFolder makes in the call above, and AddAdmin on a folder authority
	// would be a second binding for a grant that is already there.
	Authority workspace.Authority
	// Caller is the address of the person the credentials belong to, as the
	// credentials answer it (init_members.go's authenticatedUser): the
	// workspace's first person and its first admin.
	Caller string
}

// workspaceInitDeps is what the constructor takes: how the workspace named on
// the command line is turned into the seams above. I1 supplies it, and it is a
// func rather than a value so that a name no installation knows is refused by
// the opener with the sentence that says so, before this command creates
// anything.
type workspaceInitDeps struct {
	Open func(ctx context.Context, workspace string) (workspaceInitTarget, error)
}

// newWorkspaceInitCmd builds `workspace init`. It is unregistered: I1 registers
// it on `workspace`, wraps it with the reporter (D1354) and adds its census row
// in the change that wires this plan (the plan's "constructor only").
func newWorkspaceInitCmd(d workspaceInitDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <workspace>",
		Short: "Create a workspace: its mechanism, its folder and its first admin (D2803, R8)",
		Long: `Create a workspace: the one command that sets one up, and the first write of
its roster node.

The mechanism is detected and not chosen (D2803). An organization the caller's
own credentials can see — an organization it can create a folder in — means the
folder mechanism: the workspace's folder is created under it, the caller is
bound to roles/owner on it, and the workspace's own project is placed in it (R8,
where the roster and the workspace's audit trail live). The first organization
the API lists is the one used. With no organization the workspace falls back to
the service-account model: the workspace IAM account's token creator and an
owner grant on the workspace's own project become the caller's, and every member
the workspace adds is reached by impersonating that account (D2803, F20).

Either way the caller is the workspace's first person and its first admin, the
roster records the mechanism, the folder and that person, and one init entry is
created in the workspace's own audit trail. The mechanism and the folder are
written once: a workspace whose roster is already there is refused, and nothing
is created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return workspaceInit(cmd, d, args[0])
		},
	}
	return cmd
}

// workspaceInit is newWorkspaceInitCmd's body.
func workspaceInit(cmd *cobra.Command, d workspaceInitDeps, name string) error {
	ctx := cmd.Context()
	// Before anything is read, in this order: the refusal is about who is
	// running, and an agent's session gets no answer at all — not the roster,
	// not the organization search, and nothing created. Then the refusal every
	// command that opens a cloud client makes, because GODEBUG is read before
	// main runs and cannot be unset in time (refuseHTTP2Debug, cloud.go).
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was created: fugaro workspace init creates the workspace's folder, its owner binding and its roster with your own credentials, so it is run by a person at their own terminal: %s", initflow.AgentRefusal(m))
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if err := workspaceRefuseName(name, "workspace"); err != nil {
		return err
	}
	t, err := d.Open(ctx, name)
	if err != nil {
		return err
	}
	// The name on the command line and the node this command writes are two
	// values that nothing but the wiring ties together, and the node is a
	// create-once record: an opener that resolved the wrong workspace would have
	// `init` create one workspace and record another, in a node no later command
	// corrects. It is a wiring fault and not a person's, so it is not userErr.
	want, err := workspace.PathRoster(name)
	if err != nil {
		return err
	}
	if t.Store.Path() != want {
		return fmt.Errorf("workspace init was handed the roster node %s for the workspace %s, whose node is %s: refusing before anything is created, because a roster is the workspace's own record and a node written under another name is a record of a workspace that does not exist", t.Store.Path(), name, want)
	}
	// The roster is read before anything is created. A workspace that has one is
	// one `init` has run for: its mechanism and its folder are written once and
	// never changed (R6), and the read is what makes a second run create no
	// folder, no binding and no entry.
	rev, err := workspaceInitReadRoster(ctx, t.Store)
	if err != nil {
		return err
	}
	if rev.Found {
		return userErr("nothing was created: %s already holds workspace %s's roster, so this workspace is initialized — its mechanism and, on the folder path, its folder are written once and never changed (D2803, R6), and what the roster holds is what every member of the workspace is held by", t.Store.Path(), name)
	}
	caller, err := workspace.CanonicalAddress(t.Caller)
	if err != nil {
		return userErr("nothing was created: the credentials belong to %q, which is not an address a roster can hold: %v", pluginwire.Printable(t.Caller), err)
	}
	if strings.HasSuffix(caller, saAddressSuffix) {
		return userErr("nothing was created: the credentials are a service account's (%s), and a workspace's first person is the human who runs this command — an admin entry names somebody the mechanism's binding confirms, and a machine is never a workspace admin (D2823)", pluginwire.Printable(caller))
	}
	// D2803's detection, and the whole of it: the search answers the
	// organizations THIS credential can see, so an organization it cannot create
	// a folder in is an organization it does not see. The first of several is
	// the one used, and the report names it, because a workspace's folder is
	// created once and a person who keeps their work in a second organization
	// has to be able to see which one this chose.
	orgs, err := t.Folders.Organizations(ctx)
	if err != nil {
		return err
	}
	mech, folder, live := workspace.MechanismSA, "", ""
	if len(orgs) > 0 {
		folder, err = t.Folders.CreateFolder(ctx, orgs[0], caller)
		if err != nil {
			return err
		}
		mech = workspace.MechanismFolder
		// What is real from here on, and what every later failure reports: the
		// folder exists and the caller owns it whether or not the roster lands,
		// and a person told only what failed would not know to look at it. The
		// project placed in the folder is the workspace's own, which is the
		// project the workspace is named after (A1, D1290) — WU2's CreateFolder
		// moves that project, and the name is the same value it was given.
		live = fmt.Sprintf("the folder %s was created under %s with %s bound to roles/owner on it, and the workspace's own project %s was placed in it", folder, orgs[0], pluginwire.Printable(caller), name)
	} else {
		// The SA fallback: the caller's own binding is the account's token
		// creator and, R8, roles/owner on the workspace's own project. It is one
		// call because it is one decision (WU20's AddAdmin), and it is made
		// before the roster for the reason this file's header gives.
		if err := t.Authority.AddAdmin(ctx, caller); err != nil {
			return err
		}
		live = fmt.Sprintf("%s is a token creator of the workspace's IAM account and an owner of the workspace's own project %s", pluginwire.Printable(caller), name)
	}
	roster, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: mech,
		FolderID:  folder,
		People:    []workspace.Person{{Address: caller, Kind: workspace.KindHuman}},
		Admins:    []string{caller},
	})
	if err != nil {
		// Nothing a person typed reaches this: the roster is built from the
		// values above, each of them already held to its grammar. A refusal here
		// is this command and the roster's own reader disagreeing, and it is a
		// fault to fix rather than a state to report.
		return fmt.Errorf("%s, and the roster this command built is not one the roster's own reader accepts: %w", live, err)
	}
	// The first write of a workspace is the one that CREATES the node, made
	// under the revision the read above answered for a node that was not there
	// (C1's contract). A lost race is an answer and not a failure: another
	// writer created this workspace's roster first, and the mechanism is the
	// workspace's once (R6), so this command refuses rather than overwriting it.
	if err := t.Store.Write(ctx, rev, roster); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("%s, and %s was created by another writer between this command's read and its write: the roster this command built was NOT written, and a workspace holds one mechanism (R6) — read what the node holds and decide from it", live, t.Store.Path())
		}
		return fmt.Errorf("%s, and writing the roster %s failed: %w", live, t.Store.Path(), err)
	}
	// The trail's first entry, created after the change it records and never
	// instead of it (C1's contract). Its id is minted per event (D2811): the
	// document is created and never updated, so a repeat of an id is a refusal
	// and not a lost line.
	at := time.Now().UTC()
	id, err := workspace.NewAuditEventID(at, rand.Reader)
	if err != nil {
		return fmt.Errorf("the roster %s was written and workspace %s is initialized; minting the id of its %s entry failed: %w", t.Store.Path(), name, workspace.ActionInit, err)
	}
	event, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    at,
		Actor:   caller,
		Action:  workspace.ActionInit,
	})
	if err != nil {
		return fmt.Errorf("the roster %s was written and workspace %s is initialized; the %s entry this command built is not one the audit event's own reader accepts: %w", t.Store.Path(), name, workspace.ActionInit, err)
	}
	if err := t.Audit.Create(ctx, id, event); err != nil {
		return fmt.Errorf("the roster %s was written and workspace %s IS initialized; creating its %s entry in the workspace's audit trail failed: %w — the workspace exists and the entry is the one thing missing, and the trail is created once and never rewritten (D2811), so nothing writes that line later", t.Store.Path(), name, workspace.ActionInit, err)
	}
	callerShown := pluginwire.Printable(caller)
	out := cmd.OutOrStdout()
	if mech == workspace.MechanismFolder {
		fmt.Fprintf(out, "initialized workspace %s: the folder mechanism — the folder %s created under %s with %s as its owner, and the workspace's own project placed in it (R8)\n", name, folder, orgs[0], callerShown)
	} else {
		fmt.Fprintf(out, "initialized workspace %s: the SA mechanism — this credential's organization search answered no organization to create a folder in, so %s holds the workspace IAM account's token creator and roles/owner on the workspace's own project (D2803, R8)\n", name, callerShown)
	}
	fmt.Fprintf(out, "the roster is at %s, with %s as the workspace's first admin: what it records is written once and never changed (D2803, R6)\n", t.Store.Path(), callerShown)
	return nil
}

// workspaceInitReadRoster reads the workspace's own roster node through C1's
// Store: the revision a conditional write is made under, and whether the node is
// there at all.
//
// It is a helper of its own rather than a call inside the command for the one
// reason the no-prompt lint gives it: `Read(ctx)` — one argument on a receiver —
// is a shape a person can be asked with, and the lint exempts a site by file and
// FUNCTION, skipping every read in it without looking at which kind it is. Keyed
// to the command's body the exemption would also silence a confirmation somebody
// added to any later branch of it; keyed to this line it covers the store read
// and nothing else.
func workspaceInitReadRoster(ctx context.Context, s workspace.Store) (workspace.Revision, error) {
	rev, _, err := s.Read(ctx)
	return rev, err
}
