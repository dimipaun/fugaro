package cli

// workspace_user.go is WU7: `workspace user add|rm|ls` (D2802, D2806, D2812,
// D2818, D2823).
//
// Where a workspace's members live, and who may change them. The record is the
// workspace's own roster node (R5, `workspaceRosters/<name>`, C1), and every
// change to it is made here under the node's own ETag, so two workspace admins
// cannot lose each other's work. What a person may DO in a member is not here:
// that is each member's own roster at config/admin/access, written by
// `workspace user grant` (WU8) and applied by that member's own `fugaro
// update` (D2801). This file writes a member's roster in exactly one case —
// the stored project-scope admin grant D2818 writes for a workspace admin —
// and it writes nothing else of a member's.
//
// Four decisions shape the file, and they are the ones a reader should be able
// to check the code against.
//
//   - A workspace admin is the INTERSECTION of the roster's own `Admins` list
//     and the live binding the mechanism holds (folder owner, or the workspace
//     IAM account's token creator): `Admins` is a list the workspace project's
//     database admins can rewrite, and neither of them can set a binding, so an
//     entry no binding backs is trusted by nothing (D2823). The caller is the
//     credential (F21, userCallerLookup), never the local config.
//   - A machine (`--kind sa`) is recorded in the workspace's node with the
//     roles the workspace expects it to hold, and reaches no member at all
//     (D2806): the member roster accepts only `user:` members, so a machine's
//     real IAM stays where it is and this record is what drift and audit
//     compare against.
//   - `--admin` is D2818's pair, in both directions. Adding one gives the
//     person the live binding AT ONCE (`Authority.AddAdmin`, which includes the
//     owner grant on the workspace's own project, R8) and writes the stored
//     project-scope admin grant into every linked member's roster, where that
//     member's own update is what turns it into a binding. Removing one takes
//     the live binding away at once and queues every stored grant's removal the
//     same way; the workspace's own mark of what it wrote goes with it.
//   - Nothing here asks anything. A change that cannot be made is refused with
//     the sentence that names the flag or the command which can, and every write
//     refuses in a coding agent's session (A7, plan §1).
//
// What a refusal has to leave behind, since there is no transaction across the
// node, the members and IAM: a change is JUDGED against the node as it stands
// and the node is written under the revision it was read at, so a refusal
// before the write leaves every side untouched. `add` writes the node first and
// then makes the authority's and the members' side of it, which is the safe
// direction — an admin nobody recorded is worse than a record whose binding
// failed, and the failed half is re-made by `rm` followed by `add`. `rm` makes
// the member- and authority-side effects FIRST and writes the node last, which
// is the same direction from the other end: a person who has been offboarded but
// is still in the roster is a command to re-run, and a person who is out of the
// roster while their authority still stands is not reachable by any command at
// all.

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspaceRosterDeps is everything one `workspace user` or `workspace domains`
// command acts through, and it is a value the caller SUPPLIES rather than one
// this file builds. The reason is the shape of the work: the roster node, the
// audit trail, the members and the authority are four live cloud sides (C1's
// Store and AuditSink, C2's MemberOps and Authority), and the change that wires
// them to a real installation is I1's. Until it lands, every level-2 command of
// this plan is written and tested against its own double, and no command of this
// family reaches a cloud at all.
//
// The workspace's name is the flag's and not the store's: a store reads one
// workspace's node, and the name is what a sentence to a person prints.
type workspaceRosterDeps struct {
	// Workspace is the workspace these commands act on, whose roster the store
	// names (`--workspace <name>`).
	Workspace string
	// Store is the roster node (C1): a read answering the document and the
	// revision together, and a write conditional on it.
	Store workspace.Store
	// Audit is the workspace's own trail (C1), create-only (D2811).
	Audit workspace.AuditSink
	// Members is what this command does to one member project (C2): the stored
	// admin grant it writes, and the offboarding a removal runs.
	Members workspace.MemberOps
	// Sweep is the WORKSPACE's own intake (F21): the containers a person stored
	// at the workspace's project, which a removal deletes beside the members'
	// sweeps. A nil Sweep is a build that wired no intake, and removing a person
	// refuses on it rather than leaving their token behind (workspaceSweeper).
	Sweep workspaceSweeper
	// Authority is the workspace's mechanism (C2): the live admin binding, and
	// the binding list the admin gate is read against (D2823).
	Authority workspace.Authority
	// Linked answers the member projects the workspace holds now, which are the
	// ones a stored grant reaches. Resolving them is the wiring's business, and
	// it is a CALL and not a list the opener already holds: the same list is
	// what `Authority.Holds` answers for, and it costs a read of the roster
	// node plus one call per member.
	//
	// Deferred to the first use inside the body, because that is A7's order
	// (D1590): a command of this family may not make one of those calls before
	// its own agent-session refusal, and a command that never needs the list at
	// all -- `workspace domains add|rm` edits the node and nothing else, and an
	// `add` that sets no admin reaches no member — may not make one at all. A
	// nil Linked is a build that wired no list, and it is no members.
	Linked func(ctx context.Context) ([]workspace.MemberTarget, error)
	// getenv is the environment the agent-session fence reads, and now and rand
	// mint an audit event's id. A nil getenv is the process environment, a nil
	// now is the clock and a nil rand is the system's randomness: they are seams
	// for a test and every one of them has a right answer outside one.
	getenv func(string) string
	now    func() time.Time
	rand   io.Reader
}

// workspaceRosterOpen opens one workspace's members: the four sides above, for
// the workspace the flag named. It is the constructor's one parameter, so the
// change that wires this family to an installation (I1) supplies the real one
// and a test supplies its own.
type workspaceRosterOpen func(ctx context.Context, workspaceName string) (workspaceRosterDeps, error)

// userRosterAttempts bounds the ETag retries of one change (C1's contract: a
// node that moved under the writer is the ANSWER, and the caller reads again and
// judges against the bytes that are really there). It is the same bound
// `fugaro user grant` puts on its own pair writes (userPairAttempts), for the
// same reason: a node that moves on every attempt is a busy workspace, and a
// person retrying is the honest answer.
const userRosterAttempts = 4

// newWorkspaceUserCmd is `fugaro workspace user add|rm|ls`.
//
// The constructor takes its dependencies and builds no reporter: I1 wraps each
// command with the reporter (D1354) and adds its census row when it registers
// it, so this file changes nothing about what a run logs (plan §1).
func newWorkspaceUserCmd(open workspaceRosterOpen) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "The workspace's own members: who is in it, and who administers it",
		Long: `The workspace's members: the people it admits, the machines it records, and the
admins who may change either.

Who is a member is the workspace's own record — the roster node at
` + "workspaceRosters/<name>" + ` — and not any member's copy of it: a member reads
the mirror the workspace writes into its own database, and the workspace's node
stays the truth (D2813). What a person may do in a member project is that
member's own roster, written by ` + "`workspace user grant`" + ` and applied by that
member's own ` + "`fugaro update`" + ` (D2801).

Only a workspace admin changes the roster, and an admin is an entry of the
workspace's own admin list that ALSO holds the live binding the mechanism gives
it — ` + "`roles/owner`" + ` on the workspace's folder, or the workspace IAM account's
token creator (D2823). The list alone is not authority: the accounts that can
rewrite it cannot set a binding.

Every write here refuses in a coding agent's session and asks nothing: a change
that cannot be made is refused with the flag or the command that can make it.`,
	}
	// grant and revoke take the MEMBER side (C2) rather than the workspace's own
	// roster: the member is named by the command's own argument, and its database
	// is reached with the caller's own credentials, so what they are given is the
	// selected config's side (WU8's own doc, D2801).
	//
	// One side each, and not one between them: the side carries the sentence it
	// refuses an unreadable selection with, and that sentence names the command
	// that ran -- a shared side would hand a revoke the grant command's words.
	memberSide := func(what string) workspaceSides {
		return workspaceSides{side: workspaceAmbientSide(what)}
	}
	grant, revoke := memberSide("fugaro workspace user grant"), memberSide("fugaro workspace user revoke")
	cmd.AddCommand(
		workspaceReported(newWorkspaceUserAddCmd(open), "workspace-user-add"),
		workspaceReported(newWorkspaceUserRmCmd(open), "workspace-user-rm"),
		newWorkspaceUserLsCmd(open),
		workspaceReported(newWorkspaceUserGrantCmd(grant), "workspace-user-grant"),
		workspaceReported(newWorkspaceUserRevokeCmd(revoke), "workspace-user-revoke"),
	)
	return cmd
}

type workspaceUserAddOptions struct {
	workspace string
	kind      string
	external  bool
	admin     bool
	roles     []string
}

func newWorkspaceUserAddCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceUserAddOptions{}
	cmd := &cobra.Command{
		Use:   "add ADDRESS --workspace <name>",
		Short: "Admit a person or record a machine in the workspace",
		Long: `Add one member to the workspace's roster.

An address outside the workspace's allowed domains is admitted only with
--external, which records the mark D2812 keeps; an address inside them is not
marked, and --external on one is refused rather than stored. The allowed domains
are the workspace's own list (` + "`workspace domains`" + `), and an empty list admits
nobody without the mark. What the workspace records is how the person was
admitted — nothing is granted to them here, and a member's own roster is where
their roles are written.

--kind sa records a MACHINE: a service account of CI, a coordinator or a central
orchestrator, with the per-project roles the workspace expects it to hold
(--role PROJECT=ROLE, repeatable). A machine is recorded in the workspace's node
and nowhere else (D2806): its real IAM stays where it is, in the member's own
Terraform or by hand, and drift and audit compare the record against it. A
machine is never external and never a workspace admin.

--admin makes the person a workspace ADMIN (D2818): their live binding — the
folder's ` + "`roles/owner`" + `, or the workspace IAM account's token creator — and
their owner grant on the workspace's own project are given at once (R8), and a
project-scope admin grant is written into every linked member's roster for that
member's own ` + "`fugaro update`" + ` to apply. It refuses in a workspace whose roster
no bound admin administers yet, and it asks for no confirmation: the change is
the one flag.

The change is written under the roster node's own revision, so two workspace
admins editing at once cannot lose each other's work: a node that moved under
this command is read again and the change is judged against what is really
there.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceUserAdd(cmd, open, o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.workspace, "workspace", "", "the workspace whose roster this is (required: a member belongs to one workspace)")
	f.StringVar(&o.kind, "kind", "human", "what the member is: human (the default) or sa for a machine")
	f.BoolVar(&o.external, "external", false, "admit an address outside the workspace's allowed domains, marked external (D2812)")
	f.BoolVar(&o.admin, "admin", false, "make the person a workspace admin: the live binding, the owner grant on the workspace's own project, and a stored admin grant on every linked member (D2818, R8)")
	f.StringArrayVar(&o.roles, "role", nil, "a machine's expected role, PROJECT=ROLE (repeatable; --kind sa only; the roles are viewer, operator and admin)")
	return cmd
}

type workspaceUserRmOptions struct {
	workspace string
	admin     bool
}

func newWorkspaceUserRmCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceUserRmOptions{}
	cmd := &cobra.Command{
		Use:   "rm ADDRESS --workspace <name>",
		Short: "Take a person or machine off the workspace",
		Long: `Remove one member from the workspace's roster.

The person is swept out of every member the workspace holds: their stored grants
are dropped from that member's own roster and their secrets are swept by that
member's intake, which is that member's own ` + "`fugaro update`" + ` away from being
applied (D2801). A machine holds nothing on a member, so removing one reaches no
member at all (D2806).

--admin removes a workspace ADMIN, and it is required for one: their live
binding — the folder's ` + "`roles/owner`" + `, or the workspace IAM account's token
creator — goes at once, so their authority over every member ends with the
command, and the stored admin grant in each linked member's roster is queued for
removal the same way it was written (D2818 as corrected). The last workspace
admin cannot be removed: a workspace nobody administers is one the roster itself
refuses, so add another admin first.

A member who is not a workspace admin is removed without --admin, and --admin on
one is refused rather than ignored: the flag is a statement about a binding, and
there is none to drop.

Every change is judged against the roster node as it stands and written under
the revision it was read at, so a removal that raced another admin's change is
re-judged rather than written over it. A member whose own update has not applied
the workspace's stored grant is refused by that member's own contract, and this
command reports that refusal rather than half-removing the person.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceUserRm(cmd, open, o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.workspace, "workspace", "", "the workspace whose roster this is (required: a member belongs to one workspace)")
	f.BoolVar(&o.admin, "admin", false, "remove a workspace admin: the live binding goes at once, and every stored grant is queued for removal (D2818)")
	return cmd
}

type workspaceUserLsOptions struct {
	workspace string
	asJSON    bool
}

func newWorkspaceUserLsCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceUserLsOptions{}
	cmd := &cobra.Command{
		Use:   "ls --workspace <name>",
		Short: "List the workspace's members and the grants they hold",
		Long: `List the workspace's members, and what each of them holds.

Every member of the roster node is a row: their address, whether they are a
person or a machine, whether they were admitted from outside the allowed domains
and whether they are a workspace admin. Adjacent to each row is what the
workspace knows about their access — a person's stored grants, read from each
linked member's own roster, and a machine's expected per-project roles, which
are the workspace's record and not a grant (D2806).

It reads and writes nothing of anybody's: no grant is made, no node is written,
and no member is changed. A coding agent's session may run it.

The document --json prints is schemas/workspace-users.schema.json, and its
version is the one that schema states.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runWorkspaceUserLs(cmd, open, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.workspace, "workspace", "", "the workspace whose members these are (required: a member belongs to one workspace)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	return cmd
}

// workspaceRosterCheck opens one workspace's members and refuses what this build
// cannot act on: no workspace named, a name that is not a workspace's, no
// wiring at all, and a wiring that left one of the four sides out. Each refusal
// names the command or the flag that fixes it, and none of them asks anything.
func workspaceRosterCheck(ctx context.Context, open workspaceRosterOpen, name string) (workspaceRosterDeps, error) {
	if name == "" {
		return workspaceRosterDeps{}, userErr("--workspace is required: a member belongs to one workspace, and it is never spelled globally (F10)")
	}
	if err := workspaceRefuseName(name, "workspace"); err != nil {
		return workspaceRosterDeps{}, err
	}
	if open == nil {
		return workspaceRosterDeps{}, userErr("this build has no workspace wiring for `fugaro workspace user`: the change that registers these commands supplies it (I1)")
	}
	d, err := open(ctx, name)
	if err != nil {
		return workspaceRosterDeps{}, err
	}
	switch {
	case d.Store == nil:
		return workspaceRosterDeps{}, userErr("workspace %s has no roster node to read: `fugaro workspace init` writes one, and this machine's wiring supplies the store that reaches it", safetext.Field(name))
	case d.Audit == nil:
		return workspaceRosterDeps{}, userErr("workspace %s has no audit trail to file this change in: every membership change is recorded (D2811), and this machine's wiring supplies the sink that reaches it", safetext.Field(name))
	}
	return d, nil
}

// env is the environment the agent-session fence reads.
func (d workspaceRosterDeps) env() func(string) string {
	if d.getenv == nil {
		return os.Getenv
	}
	return d.getenv
}

// refuseAgent is the fence every write of this family carries (A7, plan §1):
// clause says what was not done, and the refusal says where to run it instead.
func (d workspaceRosterDeps) refuseAgent(clause string) error {
	return AgentSessionRefusal(d.env(), clause)
}

// linkedMembers resolves the list of members the workspace holds now, and it is
// called from the point in the body that needs it and from nowhere earlier: the
// resolution reads the cloud, and the fence above comes first (A7).
func (d workspaceRosterDeps) linkedMembers(ctx context.Context) ([]workspace.MemberTarget, error) {
	if d.Linked == nil {
		return nil, nil
	}
	return d.Linked(ctx)
}

// readRoster reads the roster node once and decodes it, answering the revision
// the change will be written under.
//
// An absent node is a refusal rather than an empty roster: a workspace whose
// first write has not happened is one `fugaro workspace init` has not finished
// writing (WU24), and this file has no mechanism, no folder and no first admin
// to invent — a workspace created here would be one nothing else in the
// installation knows.
func (d workspaceRosterDeps) readRoster(ctx context.Context) (*workspace.Roster, workspace.Revision, error) {
	rev, raw, err := d.Store.Read(ctx)
	if err != nil {
		return nil, workspace.Revision{}, err
	}
	if !rev.Found {
		return nil, workspace.Revision{}, userErr("workspace %s has no roster node at %s yet: `fugaro workspace init` writes it — the mechanism, the folder and the first admin are its own decision (WU24)", safetext.Field(d.Workspace), d.Store.Path())
	}
	roster, err := workspace.DecodeRoster(raw)
	if err != nil {
		return nil, workspace.Revision{}, fmt.Errorf("reading %s: %w", d.Store.Path(), err)
	}
	return roster, rev, nil
}

// changeRoster makes one change to the roster node: judge is handed the roster
// the node holds and answers the document to write (or the refusal of a change
// this node may not take), and the write is made under the revision that read
// answered. A node that moved under this writer is rtdb.ErrPrecondition — C1's
// contract makes that the ANSWER and not a failure — so the node is read again
// and the change is judged again against the bytes that are really there,
// bounded by userRosterAttempts.
//
// The node that landed is returned, so a caller can report what it wrote.
func (d workspaceRosterDeps) changeRoster(ctx context.Context, judge func(*workspace.Roster) (*workspace.Roster, error)) (*workspace.Roster, error) {
	for attempt := 0; ; attempt++ {
		roster, rev, err := d.readRoster(ctx)
		if err != nil {
			return nil, err
		}
		next, err := judge(roster)
		if err != nil {
			return nil, err
		}
		raw, err := workspace.EncodeRoster(next)
		if err != nil {
			return nil, err
		}
		err = d.Store.Write(ctx, rev, raw)
		if err == nil {
			return next, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) || attempt+1 >= userRosterAttempts {
			return nil, err
		}
	}
}

// writeRoster re-writes the document judge builds, re-reading and re-judging
// whenever the node moved under this writer. It is changeRoster's second half
// for the commands whose member- and authority-side effects must happen before
// the node is written (`rm`): the effects are made once, and only the judgement
// and the write are repeated. What judge is handed on a retry is the node as it
// is really there, and what it judges is the CHANGE: a gate cannot be part of it,
// because the effects already made may be what changed the gate's own answer
// (`rm` passes workspaceRmJudged alone; the gate was read once, before them).
func (d workspaceRosterDeps) writeRoster(ctx context.Context, judge func(*workspace.Roster) (*workspace.Roster, error), roster *workspace.Roster, rev workspace.Revision) error {
	for attempt := 0; ; attempt++ {
		next, err := judge(roster)
		if err != nil {
			return err
		}
		raw, err := workspace.EncodeRoster(next)
		if err != nil {
			return err
		}
		err = d.Store.Write(ctx, rev, raw)
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) || attempt+1 >= userRosterAttempts {
			return err
		}
		if roster, rev, err = d.readRoster(ctx); err != nil {
			return err
		}
	}
}

// admin is the caller as a workspace admin, or the refusal that says why they
// are not one (D2823). It is the gate of every write this file makes.
//
// Three things are read and all three have to hold: the credential's own
// address (F21, never the local config), the roster's `Admins` list, and the
// LIVE bindings the mechanism lists. The list without the binding is the shape
// the threat model warns about — whoever can rewrite the node's admin list
// (the workspace project's database admins, the credentials service's account)
// cannot set a folder owner or a token creator — so the intersection is the
// whole of what counts (D2823), and a difference between the two is `status`'s
// tampering row rather than this command's decision.
func (d workspaceRosterDeps) admin(ctx context.Context, roster *workspace.Roster) (string, error) {
	if d.Authority == nil {
		return "", userErr("workspace %s has no authority this machine can read: the live bindings are what an admin entry is trusted beside (D2823), and this machine's wiring supplies the mechanism that holds them", safetext.Field(d.Workspace))
	}
	caller, err := userCallerLookup(ctx)
	if err != nil {
		return "", err
	}
	address, ok := strings.CutPrefix(caller, "user:")
	if !ok {
		return "", userErr("the caller is %s, which is not a person's own account: a workspace admin is a person, and the binding D2823 confirms one by is one a machine cannot hold for a person", safetext.Field(caller))
	}
	address, err = workspace.CanonicalAddress(address)
	if err != nil {
		return "", userErr("the credential's own address: %v", err)
	}
	if !slices.Contains(roster.Admins, address) {
		return "", userErr("%s is not a workspace admin of %s: only a workspace admin adds or removes members, and the admin list is the workspace's own (D2802)", safetext.Field(address), safetext.Field(d.Workspace))
	}
	bound, err := d.Authority.AdminBindings(ctx)
	if err != nil {
		return "", err
	}
	if !slices.Contains(bound, "user:"+address) {
		return "", userErr("%s is named among %s's workspace admins, and no live binding backs that entry: only an admin entry that also holds the workspace's own binding is trusted (D2823), and the binding is the folder's `roles/owner` or the workspace IAM account's token creator. `fugaro workspace user add %s --admin` gives both; an entry with no binding behind it is what `status` reports as tampering",
			safetext.Field(address), safetext.Field(d.Workspace), safetext.Field(address))
	}
	return address, nil
}

// file creates one audit event in the workspace's own trail (D2811). actor is
// the admin the gate admitted, and the id is minted per event, so a second write
// of one is the create-only refusal the trail is built on rather than a
// replacement of an entry already there.
//
// The randomness a nil seam falls back to is crypto/rand and never the process's
// own stdin: the id is minted on every write of this family, and a read of a
// stream a person's terminal holds would either block the command behind a
// prompt nothing prints or fail it after the roster node was already written.
func (d workspaceRosterDeps) file(ctx context.Context, actor string, action workspace.Action, project, subject string, role workspace.Role) error {
	now, r := time.Now, io.Reader(crand.Reader)
	if d.now != nil {
		now = d.now
	}
	if d.rand != nil {
		r = d.rand
	}
	at := now()
	id, err := workspace.NewAuditEventID(at, r)
	if err != nil {
		return err
	}
	event := workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    at,
		Actor:   actor,
		Action:  action,
		Project: project,
		Subject: subject,
		Role:    role,
	}
	raw, err := workspace.EncodeAuditEvent(event)
	if err != nil {
		return err
	}
	if err := d.Audit.Create(ctx, id, raw); err != nil {
		return fmt.Errorf("filing the %s event in the workspace's audit trail: %w", action, err)
	}
	return nil
}

// workspaceAddress is one address as the roster carries it: the canonical
// spelling (C1's own fold), refused with the sentence that names what was not
// an address.
func workspaceAddress(at, value string) (string, error) {
	address, err := workspace.CanonicalAddress(value)
	if err != nil {
		return "", userErr("%s: %q is not an address: %v", at, value, err)
	}
	return address, nil
}

// workspaceDomainOf is the domain of an address the roster holds: the part after
// its one `@`, folded (CanonicalAddress folds it as it is read, so the
// comparison below needs no fold of its own).
func workspaceDomainOf(address string) string {
	if at := strings.LastIndexByte(address, '@'); at >= 0 {
		return address[at+1:]
	}
	return ""
}

// workspaceUserKind is --kind as the roster's own vocabulary, refusing anything
// outside it. `human` is the default because the overwhelming majority of the
// people a workspace admits are people, and a flag with no value has to mean one
// of the two.
func workspaceUserKind(value string) (workspace.Kind, error) {
	switch value {
	case "", string(workspace.KindHuman):
		return workspace.KindHuman, nil
	case string(workspace.KindSA):
		return workspace.KindSA, nil
	}
	return "", userErr("--kind %q is neither %q nor %q: a member is a person or a machine (D2806)", value, workspace.KindHuman, workspace.KindSA)
}

// workspaceMachineRoles is --role as the node holds a machine's expected roles:
// PROJECT=ROLE, each project once, sorted by project (C1's own rule for the
// list). It is a machine's list and nothing else: a person's roles are grants in
// the members' own rosters, and writing them here would be a second place the
// same fact lives (D2806).
func workspaceMachineRoles(pairs []string) ([]workspace.ProjectRole, error) {
	roles := make([]workspace.ProjectRole, 0, len(pairs))
	for _, pair := range pairs {
		project, role, ok := strings.Cut(pair, "=")
		if !ok || project == "" || role == "" {
			return nil, userErr("--role %q is not PROJECT=ROLE: a machine's expected role names the project it is expected in, and the role it is expected to hold there (D2806)", pair)
		}
		if !slices.Contains(workspace.Roles(), workspace.Role(role)) {
			return nil, userErr("--role %q: %q is not one of the roles %v (D2806)", pair, role, workspace.Roles())
		}
		if slices.ContainsFunc(roles, func(r workspace.ProjectRole) bool { return r.Project == project }) {
			return nil, userErr("--role %q: project %s is named twice; a machine holds one expected role per project", pair, project)
		}
		roles = append(roles, workspace.ProjectRole{Project: project, Role: workspace.Role(role)})
	}
	slices.SortFunc(roles, func(a, b workspace.ProjectRole) int { return strings.Compare(a.Project, b.Project) })
	return roles, nil
}

// workspaceSortedGrants writes the node's grants list the way it holds it:
// sorted by project and then by user (C1's own rule).
func workspaceSortedGrants(grants []workspace.StoredGrant) []workspace.StoredGrant {
	slices.SortFunc(grants, func(a, b workspace.StoredGrant) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return strings.Compare(a.User, b.User)
	})
	return grants
}

// runWorkspaceUserAdd is add's body.
func runWorkspaceUserAdd(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceUserAddOptions, arg string) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	if err := d.refuseAgent(fmt.Sprintf("nothing was written: `fugaro workspace user add %s --workspace %s` changes the workspace's roster, which is a workspace admin's act", safetext.Field(arg), safetext.Field(o.workspace))); err != nil {
		return err
	}
	address, err := workspaceAddress("the address", arg)
	if err != nil {
		return err
	}
	kind, err := workspaceUserKind(o.kind)
	if err != nil {
		return err
	}
	var roles []workspace.ProjectRole
	switch {
	case kind == workspace.KindSA && o.external:
		return userErr("--external: %s is a machine, and the external mark is about the domain a person was admitted from (D2812). A machine is admitted by the workspace's own decision and comes from no domain, so leave the flag out", safetext.Field(address))
	case kind == workspace.KindSA && o.admin:
		return userErr("--admin: %s is a machine, and a machine is never a workspace admin: the binding D2823 confirms an admin by is one that can only be held for a person (D2806)", safetext.Field(address))
	case kind == workspace.KindHuman && len(o.roles) > 0:
		return userErr("--role: %s is a human, and the per-project roles of this node are a machine's expected ones (D2806). A person's roles are grants in the members' own rosters, written by `fugaro workspace user grant %s <project> <role>`", safetext.Field(address), address)
	}
	if kind == workspace.KindSA {
		if roles, err = workspaceMachineRoles(o.roles); err != nil {
			return err
		}
	}
	// The members the workspace holds are read HERE and only for an admin: it is
	// the list D2818's stored grant is written into, the fence above has already
	// refused a coding agent's session (A7), and every refusal this command makes
	// about its own arguments is made before the read.
	var linked []workspace.MemberTarget
	if o.admin {
		if linked, err = d.linkedMembers(ctx); err != nil {
			return err
		}
	}

	// The change is judged and written first, and the authority's and the
	// members' half of an admin comes after it: a record whose binding failed is
	// remade by `rm` and `add` again, and an admin nobody recorded is not
	// reachable by any command of this family (the header says why the order is
	// this way round).
	var actor string
	next, err := d.changeRoster(ctx, func(roster *workspace.Roster) (*workspace.Roster, error) {
		caller, err := d.admin(ctx, roster)
		if err != nil {
			return nil, err
		}
		judged, err := workspaceAddJudged(roster, workspaceAddWant{
			address:  address,
			kind:     kind,
			external: o.external,
			admin:    o.admin,
			roles:    roles,
			linked:   linked,
		})
		if err != nil {
			return nil, err
		}
		actor = caller
		return judged, nil
	})
	if err != nil {
		return err
	}

	// One event per change, and the admin half is one change and not a second
	// for each member: the stored grant D2818 writes is what the admin event
	// records (admin.add and admin.remove), while a grant `workspace user grant`
	// writes on its own is that command's event, filed into the member's own
	// trail (WU8, D2811).
	if err := d.file(ctx, actor, workspace.ActionUserAdd, "", address, ""); err != nil {
		return err
	}
	if o.admin {
		// The authority's own spelling of a person is the member form the live
		// binding is written and read in (`user:<address>`): AdminBindings
		// answers it that way, and both mechanisms bind it that way, so the
		// write and the read that confirms it are one value.
		member, err := accesslib.Member(address)
		if err != nil {
			return userErr("%s as a live binding: %v", safetext.Field(address), err)
		}
		if err := d.Authority.AddAdmin(ctx, member); err != nil {
			return fmt.Errorf("making %s a workspace admin: %w", safetext.FieldErr(err), err)
		}
		for _, m := range linked {
			if err := d.writeMemberAdminGrant(ctx, m, address); err != nil {
				return err
			}
		}
		if err := d.file(ctx, actor, workspace.ActionAdminAdd, "", address, workspace.RoleAdmin); err != nil {
			return err
		}
	}
	return workspaceUserAddSay(cmd.OutOrStdout(), d, next, address, o.admin, len(linked))
}

// workspaceAddWant is the change `add` judges: one person or machine, and
// whether the workspace also administers by them.
type workspaceAddWant struct {
	address  string
	kind     workspace.Kind
	external bool
	admin    bool
	roles    []workspace.ProjectRole
	linked   []workspace.MemberTarget
}

// workspaceAddJudged is the change as a pure function of the roster the node
// holds: the refusals a node may not take, and the document that takes them.
// Everything it decides is decided before anything outside the node is touched,
// which is what makes a refusal leave the members and the authority alone.
func workspaceAddJudged(roster *workspace.Roster, want workspaceAddWant) (*workspace.Roster, error) {
	if slices.ContainsFunc(roster.People, func(p workspace.Person) bool { return p.Address == want.address }) {
		return nil, userErr("%s is already a member of this workspace's roster: `fugaro workspace user rm %s` takes them off it, and what they may do in a member is that member's own roster (`fugaro workspace user grant`)",
			want.address, want.address)
	}
	if want.kind == workspace.KindHuman && want.external && slices.Contains(roster.Domains, workspaceDomainOf(want.address)) {
		return nil, userErr("--external: %s is in %s, which this workspace's allowed domains already admit. The mark states that somebody was admitted from OUTSIDE the list (D2812), and the drift check computes it from the list rather than trusting the mark, so a person marked external inside a listed domain is a difference the next check reports as tampering",
			want.address, workspaceDomainOf(want.address))
	}
	if want.kind == workspace.KindHuman && !want.external && !slices.Contains(roster.Domains, workspaceDomainOf(want.address)) {
		return nil, userErr("%s is not in this workspace's allowed domains (%s): admitting somebody from outside them takes --external, which records the mark D2812 keeps. `fugaro workspace domains add %s` admits the whole domain instead",
			want.address, workspaceDomainsWord(roster.Domains), workspaceDomainOf(want.address))
	}
	next := *roster
	next.People = slices.Clone(roster.People)
	next.People = append(next.People, workspace.Person{
		Address:  want.address,
		Kind:     want.kind,
		External: want.kind == workspace.KindHuman && want.external,
		Roles:    want.roles,
	})
	slices.SortFunc(next.People, func(a, b workspace.Person) int { return strings.Compare(a.Address, b.Address) })
	next.Admins = slices.Clone(roster.Admins)
	next.Domains = slices.Clone(roster.Domains)
	next.Grants = slices.Clone(roster.Grants)
	if !want.admin {
		return &next, nil
	}
	next.Admins = append(next.Admins, want.address)
	slices.Sort(next.Admins)
	// D2818's stored grant: the workspace records, in its own node, the grant it
	// wrote for this admin in every member it holds. The mark is what the drift
	// check compares a member's roster against, and it is the only place the
	// workspace says which grants are its own — a member's roster cannot carry
	// that (its format is closed, and an older member CLI would refuse a field
	// it does not know).
	for _, m := range want.linked {
		if slices.ContainsFunc(next.Grants, func(g workspace.StoredGrant) bool {
			return g.Project == m.Project && g.User == want.address
		}) {
			continue
		}
		next.Grants = append(next.Grants, workspace.StoredGrant{Project: m.Project, User: want.address, Role: workspace.RoleAdmin})
	}
	next.Grants = workspaceSortedGrants(next.Grants)
	return &next, nil
}

// workspaceDomainsWord is the allowed domains as a sentence names them, and the
// sentence an empty list gets: an empty list admits nobody without the mark, so
// it is said rather than printed as nothing.
func workspaceDomainsWord(domains []string) string {
	if len(domains) == 0 {
		return "none are listed"
	}
	return strings.Join(domains, ", ")
}

// writeMemberAdminGrant writes the workspace's stored admin grant into one
// member's own roster (D2818). It is the one place this file writes a member,
// and it writes the member's document the way that member's own CLI reads it
// (`internal/access`), under the member node's ETag: a node another writer moved
// under this one is read again and the grant is re-judged, exactly as the
// workspace's own node is.
//
// It refuses a member whose lists have never been migrated: an absent node is
// not "no grants" but an installation whose own `fugaro update` has not written
// its roster yet, and a grant written into it would be the one `fugaro user
// grant` refuses to write for the same reason.
//
// A grant the person already holds at project scope as admin is left as it is:
// the workspace's decision is that they are an admin here, and a second identical
// pair would be a node with one fact written twice.
func (d workspaceRosterDeps) writeMemberAdminGrant(ctx context.Context, m workspace.MemberTarget, address string) error {
	member, err := accesslib.Member(address)
	if err != nil {
		return userErr("the address %q is not one a member's own roster can hold: %v", address, err)
	}
	want := accesslib.Grant{User: member, Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}}
	for attempt := 0; ; attempt++ {
		doc, err := d.Members.ReadGrants(ctx, m)
		if err != nil {
			return fmt.Errorf("reading %s's own roster: %w", safetext.Field(m.Project), err)
		}
		if !doc.Found {
			return userErr("%s has no roster at %s yet: its own `fugaro update` migrates it, and the workspace's stored admin grant is written after that (D1940, D2801)", safetext.Field(m.Project), accesslib.PathNode)
		}
		grants, err := accesslib.Decode(doc.Raw)
		if err != nil {
			return fmt.Errorf("reading %s's own roster: %w", safetext.Field(m.Project), err)
		}
		if workspaceGrantHeld(grants, want) {
			return nil
		}
		raw, err := accesslib.Encode(append(slices.Clone(grants), want))
		if err != nil {
			return err
		}
		if err := d.Members.WriteGrants(ctx, m, doc, raw); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) && attempt+1 < userRosterAttempts {
				continue
			}
			return fmt.Errorf("writing %s's own roster: %w", safetext.Field(m.Project), err)
		}
		return nil
	}
}

// workspaceGrantHeld reports whether one member's roster already carries want:
// the same person, the same role, and the same scope. access.Grant holds a scope
// whose repository list is a slice, so the two cannot be compared as values.
func workspaceGrantHeld(grants []accesslib.Grant, want accesslib.Grant) bool {
	for _, g := range grants {
		if g.User == want.User && g.Role == want.Role && g.Scope.Kind == want.Scope.Kind && slices.Equal(g.Scope.Repos, want.Scope.Repos) {
			return true
		}
	}
	return false
}

// workspaceUserAddSay prints what happened, one line, and never a question.
func workspaceUserAddSay(w io.Writer, d workspaceRosterDeps, roster *workspace.Roster, address string, admin bool, linked int) error {
	marks := make([]string, 0, 3)
	for _, p := range roster.People {
		if p.Address != address {
			continue
		}
		if p.External {
			marks = append(marks, "external")
		}
		if admin {
			marks = append(marks, "workspace admin")
		}
		if p.Kind == workspace.KindSA {
			roles := make([]string, 0, len(p.Roles))
			for _, r := range p.Roles {
				roles = append(roles, fmt.Sprintf("%s=%s", r.Project, r.Role))
			}
			marks = append(marks, "machine, expected roles: "+workspaceRolesWord(roles))
		}
	}
	what := address
	if len(marks) > 0 {
		what += " (" + strings.Join(marks, ", ") + ")"
	}
	if _, err := fmt.Fprintf(w, "added %s to workspace %s\n", safetext.OneLine(what), safetext.Field(d.Workspace)); err != nil {
		return err
	}
	if admin {
		_, err := fmt.Fprintf(w, "their live binding and their owner grant on the workspace's own project were given at once (R8), and a project-scope admin grant was written into each of the %d member(s) the workspace holds: each member's own fugaro update is what applies it (D2801, D2818)\n", linked)
		return err
	}
	return nil
}

// workspaceRolesWord is a machine's expected roles as a sentence names them.
func workspaceRolesWord(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

// runWorkspaceUserRm is rm's body.
func runWorkspaceUserRm(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceUserRmOptions, arg string) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	if err := d.refuseAgent(fmt.Sprintf("nothing was written: `fugaro workspace user rm %s --workspace %s` sweeps a person out of every member the workspace holds, which is a workspace admin's act", safetext.Field(arg), safetext.Field(o.workspace))); err != nil {
		return err
	}
	address, err := workspaceAddress("the address", arg)
	if err != nil {
		return err
	}

	// The gate is read ONCE, here, before any effect is made, and the actor it
	// answered is carried to the trail. It cannot be read again on the write
	// path: an admin who removes their OWN entry — allowed while another admin is
	// left — has taken their own live binding away by the time the node is
	// written, so a second gate read would refuse the command's own work and
	// leave a roster naming an admin no binding backs (the shape D2823 calls
	// tampering), with the binding dropped, the members offboarded and no event
	// filed. What a retry re-judges is the CHANGE against the bytes that are
	// really there; the caller's authority was established against the node this
	// command read.
	roster, rev, err := d.readRoster(ctx)
	if err != nil {
		return err
	}
	actor, err := d.admin(ctx, roster)
	if err != nil {
		return err
	}
	if _, err := workspaceRmJudged(roster, address, o.admin); err != nil {
		return err
	}
	person := workspacePersonOf(roster, address)

	// The members the workspace holds are read HERE, once, for the two things
	// that need them -- the offboarding a person's removal runs and the count the
	// report prints — and not by a removal that needs neither, which is a
	// machine's. The fence above has already refused a coding agent's session
	// (A7) and the gate reads that come before this one touch the workspace's
	// own node, not a member's.
	var linked []workspace.MemberTarget
	if person.Kind == workspace.KindHuman || o.admin {
		if linked, err = d.linkedMembers(ctx); err != nil {
			return err
		}
	}

	// The member- and authority-side effects come first, and the node last: a
	// person who is offboarded and still recorded is a command to re-run, and a
	// person out of the roster whose authority still stands is one no command of
	// this family can reach (the header says so at length).
	//
	// A person's removal sweeps TWO installations (F21): every member the
	// workspace holds, and then the workspace's own project, whose intake holds
	// the token they stored once (D2960). The workspace's sweep runs whatever the
	// members answered (offboardWorkspacePerson says why), and EVERY return after
	// it carries its answer out (workspaceUserRmCarry), because the containers are
	// already gone or already refused by then: a member's refusal is returned
	// before the node, as this command has always refused on one, and it does not
	// take the sweep's own answer down with it.
	var (
		deleted  []string
		sweepErr error
	)
	if person.Kind == workspace.KindHuman {
		if d.Members == nil {
			return userErr("workspace %s has no member operations this machine can read: removing a person sweeps them out of every member the workspace holds (C2's Offboard), and this machine's wiring supplies it", safetext.Field(d.Workspace))
		}
		if d.Sweep == nil {
			return userErr("workspace %s has no intake this machine can reach: a person's token is stored once, at the workspace (D2960), and removing them deletes every container they own there (F21), so this machine's wiring supplies the workspace's own intake", safetext.Field(d.Workspace))
		}
		// The IAM half of a workspace removal is a RECORD and not a write: no
		// binding on a member is written from here (D2801), so the grant a removed
		// workspace admin holds on a member's project (D2818) is dropped by that
		// member's own `fugaro update` -- and that update admits the removal only
		// against the member's own record of removed people (DG5, D3454). So the
		// record is written on EVERY member the workspace holds, before the members
		// are offboarded and before this workspace's own node: a re-run converges,
		// and a member already recorded is left as the record is.
		member, err := accesslib.Member(address)
		if err != nil {
			return workspaceUserRmCarry(userErr("%s as the member a removal is recorded under: %v", safetext.Field(address), err), address, nil)
		}
		for _, m := range linked {
			if err := d.Members.RecordRemoval(ctx, m, member); err != nil {
				return workspaceUserRmCarry(fmt.Errorf("recording the removal of %s on member %s: %w", safetext.Field(address), safetext.Field(m.Project), err), address, nil)
			}
		}
		var memberErr error
		deleted, memberErr, sweepErr = offboardWorkspacePerson(ctx, d.Members, d.Sweep, linked, address)
		if memberErr != nil {
			// The sweep has ALREADY run (offboardWorkspacePerson) and the node is
			// not written, so this is the one path that reports the two answers and
			// stops: what the workspace's own intake removed is said here -- a
			// person whose member refused still has to know their token at the
			// workspace is gone -- and a sweep that failed is returned beside the
			// refusal rather than behind it. The halting line is NOT said: the
			// roster was not written, so R3's minute has not started and they are
			// still a member whose run keeps going.
			if err := workspaceUserRmSweptSay(cmd.OutOrStdout(), address, deleted, sweepErr == nil, false); err != nil {
				return workspaceUserRmCarry(memberErr, address, err)
			}
			return workspaceUserRmCarry(memberErr, address, sweepErr)
		}
	}
	if o.admin {
		member, err := accesslib.Member(address)
		if err != nil {
			return workspaceUserRmCarry(userErr("%s as a live binding: %v", safetext.Field(address), err), address, sweepErr)
		}
		if err := d.Authority.RemoveAdmin(ctx, member); err != nil {
			return workspaceUserRmCarry(fmt.Errorf("removing %s's workspace admin binding: %w", safetext.Field(address), err), address, sweepErr)
		}
	}
	if err := d.writeRoster(ctx, func(current *workspace.Roster) (*workspace.Roster, error) {
		return workspaceRmJudged(current, address, o.admin)
	}, roster, rev); err != nil {
		return workspaceUserRmCarry(err, address, sweepErr)
	}
	if err := d.file(ctx, actor, workspace.ActionUserRemove, "", address, ""); err != nil {
		return workspaceUserRmCarry(err, address, sweepErr)
	}
	if o.admin {
		if err := d.file(ctx, actor, workspace.ActionAdminRemove, "", address, workspace.RoleAdmin); err != nil {
			return workspaceUserRmCarry(err, address, sweepErr)
		}
	}
	w := cmd.OutOrStdout()
	if err := workspaceUserRmSay(w, d, address, o.admin, len(linked)); err != nil {
		return workspaceUserRmCarry(err, address, sweepErr)
	}
	if person.Kind == workspace.KindHuman {
		if err := workspaceUserRmSweptSay(w, address, deleted, sweepErr == nil, true); err != nil {
			return workspaceUserRmCarry(err, address, sweepErr)
		}
	}
	return workspaceUserRmCarry(nil, address, sweepErr)
}

// workspaceUserRmCarry returns err with the workspace's own sweep's failure beside
// it, and returns err untouched when that sweep answered -- a nil err is the
// no-further-error case, so the tail of a removal that landed is the named
// refusal and nothing else.
//
// It exists because the sweep runs BEFORE every step below it and before the
// node: once offboardWorkspacePerson has returned, the person's containers at the
// workspace are deleted or the delete failed, and neither fact may be dropped by
// a later step that fails for a reason of its own. A person reading
// `removing X's workspace admin binding: ...` has to learn as well that the sweep
// did not do its half, or they will read the removal as a whole that failed
// cleanly (offboardWorkspacePerson's own doc says why the two answers are
// separate).
func workspaceUserRmCarry(err error, address string, sweepErr error) error {
	if sweepErr == nil {
		return err
	}
	named := fmt.Errorf("the workspace's own intake did not sweep %s's containers: %w", safetext.Field(address), sweepErr)
	if err == nil {
		return named
	}
	return errors.Join(err, named)
}

// workspaceRmJudged is a removal as a pure function of the roster the node
// holds, and the three refusals a removal may not make:
//
//   - an address the roster does not name is not a member, and removing one
//     would be a change about somebody the workspace never admitted;
//   - an admin entry has to name a person of the roster (C1's own rule), so a
//     person who administers is removed WITH --admin rather than half-removed —
//     the node a command without it would write is one this package's own
//     reader refuses;
//   - the last workspace admin cannot be removed: the roster names at least one
//     admin, and a workspace nobody administers is one `fugaro workspace init`
//     has not finished writing.
func workspaceRmJudged(roster *workspace.Roster, address string, admin bool) (*workspace.Roster, error) {
	person := workspacePersonOf(roster, address)
	if person.Address == "" {
		return nil, userErr("%s is not a member of this workspace's roster: `fugaro workspace user ls` lists the members it holds", safetext.Field(address))
	}
	isAdmin := slices.Contains(roster.Admins, address)
	switch {
	case isAdmin && !admin:
		return nil, userErr("%s is a workspace admin: removing them takes their live binding away, so `fugaro workspace user rm %s --admin` is the command that does it. Without the flag nothing was changed", safetext.Field(address), address)
	case !isAdmin && admin:
		return nil, userErr("--admin: %s is not a workspace admin of this workspace, so there is no binding to drop. `fugaro workspace user rm %s` removes them as a member", safetext.Field(address), address)
	case admin && len(roster.Admins) == 1:
		return nil, userErr("%s is this workspace's last workspace admin, and a workspace nobody administers is one the roster itself refuses: add another admin first, with `fugaro workspace user add <address> --admin`", safetext.Field(address))
	}
	next := *roster
	next.People = make([]workspace.Person, 0, len(roster.People)-1)
	for _, p := range roster.People {
		if p.Address != address {
			next.People = append(next.People, p)
		}
	}
	next.Admins = make([]string, 0, len(roster.Admins))
	for _, a := range roster.Admins {
		if a != address {
			next.Admins = append(next.Admins, a)
		}
	}
	next.Domains = slices.Clone(roster.Domains)
	// The workspace's own marks of the grants it wrote for this person go with
	// them: what the marks record is a grant that is being taken away, and a mark
	// left behind would be the drift check comparing a member's roster against a
	// grant the workspace no longer claims.
	next.Grants = make([]workspace.StoredGrant, 0, len(roster.Grants))
	for _, g := range roster.Grants {
		if g.User != address {
			next.Grants = append(next.Grants, g)
		}
	}
	return &next, nil
}

// workspacePersonOf is one person of a roster, and the zero Person for an
// address it does not name (Address is validated non-empty, so the zero value
// cannot be mistaken for a member).
func workspacePersonOf(roster *workspace.Roster, address string) workspace.Person {
	for _, p := range roster.People {
		if p.Address == address {
			return p
		}
	}
	return workspace.Person{}
}

// workspaceUserRmSay prints what was removed, one line, and names what the
// members' own updates still have to apply.
func workspaceUserRmSay(w io.Writer, d workspaceRosterDeps, address string, admin bool, linked int) error {
	who := safetext.OneLine(address)
	if admin {
		who += " (workspace admin)"
	}
	if _, err := fmt.Fprintf(w, "removed %s from workspace %s\n", who, safetext.Field(d.Workspace)); err != nil {
		return err
	}
	if admin {
		if _, err := fmt.Fprintf(w, "their live binding was removed at once; the stored admin grant was queued for removal in each of the %d member(s) the workspace holds, and each member's own fugaro update is what drops its resource-level bindings (D2818)\n", linked); err != nil {
			return err
		}
	}
	return nil
}

// workspaceUserRmSweptSay says what the workspace's own intake swept for the
// person (F21), and, when the removal landed, that a run in flight on their token
// halts within a minute (R3, D3146).
//
// swept is the sweep's own answer: the containers are reported only when the
// delete landed, and a failed delete is the error the caller returns (carried
// through every path by workspaceUserRmCarry), never a second line here.
//
// halt is whether the removal itself landed, and it is false on the one path that
// reports this and stops: a member's refusal. The halting line is about the
// REMOVAL and not about the sweep -- R3 is the workspace's service re-reading the
// roster as it re-reads its node (60 seconds) and refusing every call under a slip
// whose owner has left it -- so on that path there is nothing to say: the roster
// was not written, the person is still a member, and their run keeps going. Where
// the removal landed, the line is printed WHATEVER the sweep answered, because a
// sweep that failed does not make the halting false (WC0's threat model §7).
func workspaceUserRmSweptSay(w io.Writer, address string, deleted []string, swept, halt bool) error {
	if halt {
		if _, err := fmt.Fprintf(w, "a run in flight on %s's token halts within a minute: the workspace's service re-reads the roster as it re-reads its node and refuses every call under a slip whose owner has left it, so the removal stops the run and the sweep of their containers is not what does (R3, D3146)\n", safetext.Field(address)); err != nil {
			return err
		}
	}
	if !swept {
		return nil
	}
	if len(deleted) == 0 {
		_, err := fmt.Fprintf(w, "%s stored no secrets of their own at the workspace: its own intake's sweep removed nothing\n", safetext.Field(address))
		return err
	}
	_, err := fmt.Fprintf(w, "removed %d secrets %s stored at the workspace: %s\n", len(deleted), safetext.Field(address), strings.Join(printableAll(deleted), ", "))
	return err
}

// runWorkspaceUserLs is ls's body.
func runWorkspaceUserLs(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceUserLsOptions) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	roster, _, err := d.readRoster(ctx)
	if err != nil {
		return err
	}
	grants, err := d.memberGrants(ctx)
	if err != nil {
		return err
	}
	doc := workspaceUsersDocument(d.Workspace, roster, grants)
	if !o.asJSON {
		return workspaceUsersPrint(cmd.OutOrStdout(), d, roster, doc)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// memberGrants reads every linked member's own roster, once, and answers the
// grants it holds by project. A member whose node is not there yet is a member
// the workspace's grants have not reached, which is not an error: its own
// `fugaro update` is what writes it, and the listing says nothing about that
// member rather than failing the whole read.
//
// A read that FAILS is an error, and the command refuses on it: a listing that
// silently dropped a member would read as a member whose people hold nothing.
func (d workspaceRosterDeps) memberGrants(ctx context.Context) (map[string][]accesslib.Grant, error) {
	linked, err := d.linkedMembers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]accesslib.Grant, len(linked))
	if d.Members == nil {
		return out, nil
	}
	for _, m := range linked {
		doc, err := d.Members.ReadGrants(ctx, m)
		if err != nil {
			return nil, fmt.Errorf("reading %s's own roster: %w", safetext.Field(m.Project), err)
		}
		if !doc.Found {
			continue
		}
		grants, err := accesslib.Decode(doc.Raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s's own roster: %w", safetext.Field(m.Project), err)
		}
		out[m.Project] = grants
	}
	return out, nil
}

// workspaceUsersDocVersion is `fugaro workspace user ls --json`'s document
// version, and it is 1: the document is published from its first release, so a
// member added from here on is an addition a version-1 reader reads as it did,
// and removing or re-meaning a member of it is what bumps this (CLAUDE.md, "a
// field never changes meaning").
const workspaceUsersDocVersion = 1

// workspaceUsersDoc is `fugaro workspace user ls --json`, and
// schemas/workspace-users.schema.json is its other half.
//
// It is a NAMED SET OF FIELDS and not a marshalled workspace.Roster (the `config
// list` rule): the node's own document is read by this program and by nothing
// else, and a consumer reads this one. Every array is seeded empty and never
// null — "there are none" is a fact and a null is not — and every value is a
// fact about a member's own record: no field here is a credential or a secret.
type workspaceUsersDoc struct {
	Version   int                    `json:"version"`
	Workspace string                 `json:"workspace"`
	Mechanism string                 `json:"mechanism"`
	Domains   []string               `json:"domains"`
	Members   []workspaceUsersMember `json:"members"`
}

// workspaceUsersMember is one member of the workspace's roster, with what the
// workspace knows about their access.
type workspaceUsersMember struct {
	Address string `json:"address"`
	// Kind is the roster's own word: `human` or `sa` (D2806).
	Kind string `json:"kind"`
	// External is D2812's mark: the person was admitted while their domain was
	// not one of the workspace's allowed domains.
	External bool `json:"external"`
	// Admin is the workspace's own admin list's answer, which is a claim about a
	// binding and not the binding itself: only an entry the live binding backs is
	// trusted (D2823), and `status` is what reports a difference.
	Admin bool `json:"admin"`
	// Roles are a MACHINE's expected per-project roles, which are the
	// workspace's record and not a grant (D2806). Empty for a person.
	Roles []workspaceUsersRole `json:"roles"`
	// Grants are the stored grants this member holds in the members' own
	// rosters, one entry per role held in a member project. Empty for a machine:
	// a member's roster accepts only `user:` members (D2806).
	Grants []workspaceUsersGrant `json:"grants"`
}

// workspaceUsersRole is one project a machine is expected to hold a role in.
type workspaceUsersRole struct {
	Project string `json:"project"`
	Role    string `json:"role"`
}

// workspaceUsersGrant is one stored grant of a member: the member project it is
// held in, the role, and the scope the role is held at (each member's own roster
// is the record, and its scope grammar is `internal/access`'s).
type workspaceUsersGrant struct {
	Project string `json:"project"`
	Role    string `json:"role"`
	Scope   string `json:"scope"`
}

// workspaceUsersDocument is the document, built from the node and the members'
// grants. The members are the node's own order (sorted by address), a machine's
// roles are the node's (sorted by project), and a member's grants are read
// member by member: the member projects in name order, and within one project
// the order that member's own roster holds, which is the order its own reader
// answered. Two runs over one unchanged installation print the same document.
func workspaceUsersDocument(name string, roster *workspace.Roster, grants map[string][]accesslib.Grant) workspaceUsersDoc {
	doc := workspaceUsersDoc{
		Version:   workspaceUsersDocVersion,
		Workspace: name,
		Mechanism: string(roster.Mechanism),
		Domains:   append([]string{}, roster.Domains...),
		Members:   make([]workspaceUsersMember, 0, len(roster.People)),
	}
	projects := make([]string, 0, len(grants))
	for project := range grants {
		projects = append(projects, project)
	}
	slices.Sort(projects)
	for _, p := range roster.People {
		row := workspaceUsersMember{
			Address:  p.Address,
			Kind:     string(p.Kind),
			External: p.External,
			Admin:    slices.Contains(roster.Admins, p.Address),
			Roles:    []workspaceUsersRole{},
			Grants:   []workspaceUsersGrant{},
		}
		if p.Kind == workspace.KindSA {
			for _, r := range p.Roles {
				row.Roles = append(row.Roles, workspaceUsersRole{Project: r.Project, Role: string(r.Role)})
			}
		} else if member, err := accesslib.Member(p.Address); err == nil {
			for _, project := range projects {
				for _, g := range grants[project] {
					if g.User == member {
						row.Grants = append(row.Grants, workspaceUsersGrant{Project: project, Role: string(g.Role), Scope: g.Scope.String()})
					}
				}
			}
		}
		doc.Members = append(doc.Members, row)
	}
	return doc
}

// workspaceUsersPrint prints the listing a person reads: the workspace, its
// allowed domains, and one line per member with what they hold.
//
// Nothing printed here needs escaping for the terminal: an address reaches the
// node through C1's own fold (printable ASCII, one `@`, no control byte), a
// domain through its grammar, and a project or a role through the closed sets
// they are written from — the same reading `fugaro user ls` takes of the values
// it prints from a node. safetext.OneLine still runs over each of them, because
// it is idempotent on a value like that and it is the one place a later change
// that loosened a grammar would be caught. The workspace's name is the flag's,
// so it is escaped as the field it is.
func workspaceUsersPrint(w io.Writer, d workspaceRosterDeps, roster *workspace.Roster, doc workspaceUsersDoc) error {
	if _, err := fmt.Fprintf(w, "workspace %s: %d member(s), %d admin(s), mechanism %s\n",
		safetext.Field(d.Workspace), len(roster.People), len(roster.Admins), safetext.OneLine(string(roster.Mechanism))); err != nil {
		return err
	}
	if len(roster.Domains) == 0 {
		if _, err := fmt.Fprintf(w, "  no allowed domain: an address from anywhere is admitted with --external (D2812)\n"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "  allowed domains: %s\n", safetext.OneLine(strings.Join(roster.Domains, ", "))); err != nil {
			return err
		}
	}
	if len(doc.Members) == 0 {
		_, err := fmt.Fprintf(w, "  no member is recorded: the roster names at least one person\n")
		return err
	}
	for _, m := range doc.Members {
		marks := []string{safetext.OneLine(m.Kind)}
		if m.Admin {
			marks = append(marks, "admin")
		}
		if m.External {
			marks = append(marks, "external")
		}
		line := fmt.Sprintf("  %s (%s)", safetext.OneLine(m.Address), strings.Join(marks, ", "))
		held := make([]string, 0, len(m.Grants)+len(m.Roles))
		for _, g := range m.Grants {
			held = append(held, fmt.Sprintf("%s: %s (%s)", safetext.OneLine(g.Project), safetext.OneLine(g.Role), safetext.OneLine(g.Scope)))
		}
		for _, r := range m.Roles {
			held = append(held, fmt.Sprintf("%s: %s (expected)", safetext.OneLine(r.Project), safetext.OneLine(r.Role)))
		}
		if len(held) == 0 {
			held = append(held, "no stored grant in a member the workspace holds")
		}
		if _, err := fmt.Fprintf(w, "%s — %s\n", line, strings.Join(held, ", ")); err != nil {
			return err
		}
	}
	return nil
}
