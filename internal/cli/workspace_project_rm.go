package cli

// workspace_project_rm.go is WU6: `workspace project rm` (D2808, D2821, D2822).
//
// The command DETACHES a project from a workspace and deletes nothing, and the
// separation of those two acts is the whole of its design (spec.md, "rm
// detaches and never deletes"). A detach is five things a person can repeat or
// walk away from — a snapshot of the member's state, the grants the workspace's
// own node marks as written into the member's roster, the mirror, the marks
// themselves, and the authority the workspace held over it — and a deletion is
// Google's own flow, with Google's own safeguards, run by the owner. So this
// command takes the five back, drops the member's entry from the owner's table,
// and says out loud what is still there and still costing money, with the
// console page the owner shuts the project down from.
// It opens nothing: printing a link is a person's decision left to the person.
//
// Four more rules shape it, and each of them is a decision of the plan rather
// than this file's own:
//
//   - It is the WORKSPACE project's owner's command, run on the machine whose
//     config holds the member table (D2821, as `workspace project add` is
//     today): the member table is that config's, and its owner's `fugaro
//     update` publishes it whole, so a second writer would republish a
//     different table. Every other workspace admin manages users, grants,
//     domains and `reconcile` instead.
//   - It saves the member's state by default (D2808): through the state writer,
//     to a local file it names and, when the member has a designated state
//     repository, as a pull request — and `--no-save-state` skips that. It asks
//     nothing either way, because a question is not a fence (the no-prompt
//     policy, D1540).
//   - It works after the member owner's `unlink` too (D2822). Unlink drops the
//     authority and only the authority, so the grants the workspace wrote into
//     the member's roster and the mirror are still there afterwards — the state
//     `status` shows as "unlinked, grants left" — and this command is what takes
//     them back. Which grants those are is the workspace's OWN node's answer
//     (D2818's mark: the member's project, the address and the role) and never a
//     guess from who is an admin today: a member's roster cannot tell the
//     workspace's grants from its own, and the mark is the record that can — and
//     it outlives the unlink because it is the workspace's own.
//   - It writes no binding on a member project (D2801, A6). Everything that
//     reaches the member goes through C2's own seams, and the one binding this
//     command drops — the SA mechanism's `roles/owner` for the workspace IAM
//     account — is dropped by the authority that made it. On the folder
//     mechanism nothing is dropped at all: the move back is the member
//     owner's, and until Check 720 settles whether a folder owner can make it,
//     the owner is told to run `fugaro workspace project unlink` (U6).
//
// Two things here are this file's own shape rather than the plan's words. The
// first is the ORDER: everything that reaches the member happens before the
// owner's own file is edited, so a failure anywhere in it leaves the member
// declared and the whole command repeatable — its steps are idempotent, and a
// second run finishes what the first began — while a table entry dropped ahead
// of them would be a member nothing could reach again. The running-cost warning
// is printed with them and not after the edit, for the same reason: it is about
// the detach, which is done by then, and a chain that cannot finish must not
// swallow the one paragraph that tells a person what is still costing money.
//
// The second is `Audit`, one seam beyond WU6's Interfaces line, which names
// C1's `Store` and C2's `Authority`, `MemberOps` and `StateWriter`: C1's
// contract lists WU6 among the roster's writers, a writer's change is recorded
// — "a change to the roster and the audit event it records are two writes and
// not one transaction", the entry written after the change — and
// `ActionProjectRemove` is the action that exists for this one. A writer that
// changed the roster and filed nothing would be the hole the trail is for.
//
// Everything this file prints is escaped, one rule for all of it: the plan's
// conventions make a member-controlled string on a terminal the hazard, and the
// two values that are not held to the project-name grammar — the writer's file
// and the pull-request URL — are the ones nobody here made.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspaceProjectShutdownPage is R3's page: the console screen a project is
// shut down from, with the member's own project appended. It is PRINTED and
// never opened — a real deletion goes through Google's own flow and its own
// safeguards, which is the promise this command's output makes — and Check 720
// is what confirms it is the shut-down page (U3). The project id is escaped
// into the query, as the SA authority escapes the workspace's into its own
// console link: an escaping for a terminal would be the wrong one here.
const workspaceProjectShutdownPage = "https://console.cloud.google.com/iam-admin/settings?project="

// workspaceProjectRmDeps is what one run of this command is given: the seams
// the work is done through, and nothing else. The concrete sides are I1's —
// WU1's roster store and audit writer adapted onto C1, WU20's authority, WU4's
// member operations, WU15's state writer — which is why the command takes them
// rather than building them, and why a test drives the whole run against
// doubles with no database, no network and no cloud in it.
type workspaceProjectRmDeps struct {
	// Roster is the workspace's own node: read for the admins whose grants are
	// taken back, and written to drop the marks it keeps for the member.
	Roster workspace.Store
	// Audit is the workspace's audit trail, where the removal is recorded.
	Audit workspace.AuditSink
	// Authority is the mechanism the workspace holds its members by, and the
	// only thing here that may drop a live binding.
	Authority workspace.Authority
	// Members is the member's own database: its roster of grants and its mirror.
	Members workspace.MemberOps
	// State is the snapshot writer D2808 saves through by default.
	State workspace.StateWriter
}

// workspaceProjectRmOptions is one run's flags.
type workspaceProjectRmOptions struct {
	// noSaveState is --no-save-state (D2808): the member's state is saved by
	// default and this skips it. The coding-agent refusal names it rather than
	// being lifted by it: a snapshot is one of the things this command writes,
	// and a session that may not write the member's database may not write the
	// member's state either.
	noSaveState bool
	// noApply is --no-apply (D1565), the family's own flag: the owner's
	// declaration is written and the steps that make it live are left to
	// `fugaro apply`.
	noApply *bool
}

// newWorkspaceProjectRmCmd is WU6's command. I1 registers it under `workspace
// project` and wraps it with the reporter (D1354), so nothing here builds a
// reporter of its own.
func newWorkspaceProjectRmCmd(deps workspaceProjectRmDeps) *cobra.Command {
	var o workspaceProjectRmOptions
	cmd := &cobra.Command{
		Use:   "rm <project>",
		Short: "Detach a project from the workspace, deleting nothing",
		Long: `Detach one member project from the workspace. No Google Cloud or Firebase
resource is deleted.

The project's state is saved before it is detached: to a local file the command
names, and as a pull request to the member's designated state repository when it
has one. --no-save-state skips that, and nothing asks either way.

What the workspace holds on the project is then taken back: the grants its
admins were written into the member's own roster, the mirror of the workspace's
member list, the marks the workspace's own node keeps for it, and the authority
the workspace held over the project. A member owner who already ran fugaro
workspace project unlink has dropped the authority alone, so running this
afterwards is what takes the grants back.

The project itself is left as it is, and the command says so: what is still
deployed there keeps costing money until the project is shut down, and the
console page for that is printed rather than opened, because shutting a project
down is Google's own flow and your own decision.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceProjectRm(cmd, args[0], deps, &o)
		},
	}
	cmd.Flags().BoolVar(&o.noSaveState, "no-save-state", false,
		"do not save the project's settings and spending summary before detaching it")
	o.noApply = addNoApplyFlag(cmd)
	return cmd
}

func runWorkspaceProjectRm(cmd *cobra.Command, project string, deps workspaceProjectRmDeps, o *workspaceProjectRmOptions) error {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	if err := workspaceProjectRmRefuseSession(cmd, o); err != nil {
		return err
	}
	if err := workspaceRefuseName(project, "project"); err != nil {
		return err
	}
	sel, lc, err := settingsLocalFile("fugaro workspace project rm")
	if err != nil {
		return err
	}
	if !workspaceDeclared(lc) {
		return userErr("the selected project %s declares no member table, so it is not a workspace's own project: project add, ls, rm and status are run from the config of the project the workspace is named after, which is the machine that holds the member table (D2821); fugaro workspace ls lists the workspaces this machine's configs declare", safetext.Field(lc.Name))
	}
	entry, declared := workspaceTable(lc)[project]
	if !declared || entry.IsZero() {
		return userErr("workspace %s declares no member %s: there is no entry to remove (fugaro workspace show %s lists the ones its table holds)", safetext.Field(lc.Name), safetext.Field(project), safetext.Field(lc.Name))
	}
	target, err := workspaceProjectRmTarget(os.Getenv, project, entry)
	if err != nil {
		return err
	}
	// Who the change is made by is read before anything happens: the audit
	// entry has to name an actor, and an entry the trail refuses is one created
	// never (D2811). The refusal is the one every command that acts as a person
	// gives, and it names its own fix.
	actor, err := lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	actor, err = workspace.CanonicalAddress(actor)
	if err != nil {
		return userErr("the user this command would record in the workspace's audit trail is not an address: %v", err)
	}

	// The snapshot is the one record of the member this command MAKES, and it
	// is what a later state cannot reconstruct: the grants, the mirror and the
	// entry are the workspace's own records and they go. A failure after it
	// leaves the member declared, so running the command again repeats the
	// whole of this and writes the same file again (D2808).
	if o.noSaveState {
		fmt.Fprintf(out, "%s: nothing of its settings and spending summary was saved (--no-save-state)\n", safetext.Field(project))
	} else {
		written, err := deps.State.Save(ctx, workspace.Snapshot{Workspace: lc.Name, Member: target})
		if err != nil {
			return userErr("saving %s's state before detaching it: %v", safetext.Field(project), err)
		}
		fmt.Fprintf(out, "saved %s's state: %s\n", safetext.Field(project), safetext.Field(written.File))
		if written.PullRequest != "" {
			fmt.Fprintf(out, "the state repository's pull request: %s\n", safetext.Field(written.PullRequest))
		}
	}

	rev, roster, err := workspaceProjectRmReadRoster(ctx, deps.Roster)
	if err != nil {
		return err
	}

	// The marks are what tells this member's roster which of its grants are the
	// workspace's, and they are read before the grants are, because it is the
	// marks and not the member's roster that answer that question (D2818).
	marks := workspaceRosterMarksFor(roster, project)
	removed, err := workspaceProjectRmMemberGrants(ctx, deps.Members, target, marks)
	if err != nil {
		return err
	}
	switch {
	case len(removed) > 0:
		fmt.Fprintf(out, "took back the %d grant(s) the workspace recorded for %s: %s\n",
			len(removed), safetext.Field(project), safetext.Field(strings.Join(removed, ", ")))
	case len(marks) == 0:
		fmt.Fprintf(out, "the workspace's own node records no grant for %s, so none was taken out of its roster\n", safetext.Field(project))
	default:
		fmt.Fprintf(out, "no grant the workspace recorded for %s was still in its own roster\n", safetext.Field(project))
	}

	mirrored, err := workspaceProjectRmMemberMirror(ctx, deps.Members, target)
	if err != nil {
		return err
	}
	if mirrored {
		fmt.Fprintf(out, "removed the mirror of the workspace's member list from %s\n", safetext.Field(project))
	} else {
		fmt.Fprintf(out, "%s holds no mirror of the workspace's member list\n", safetext.Field(project))
	}

	if err := workspaceProjectRmAuthority(ctx, out, deps.Authority, target); err != nil {
		return err
	}

	changed, taken, err := workspaceProjectRmDropMarks(ctx, deps.Roster, deps.Members, target, rev, roster, project, marks)
	if err != nil {
		return err
	}
	// A mark written while this command ran is acted on rather than dropped
	// silently: the grant behind it is in the member's roster, and dropping the
	// mark without taking the grant would leave access this workspace wrote in a
	// member the owner's table no longer declares.
	if len(taken) > 0 {
		fmt.Fprintf(out, "the workspace's own roster moved under this command, so the %d grant(s) it recorded for %s in the meantime were taken back too: %s\n",
			len(taken), safetext.Field(project), safetext.Field(strings.Join(taken, ", ")))
	}
	if changed {
		fmt.Fprintf(out, "dropped the marks the workspace's own roster kept for %s\n", safetext.Field(project))
	}
	// The entry is filed after the change it records (C1), and it is filed
	// whether or not the node carried marks to drop: the member left the
	// workspace, and that is what the trail is asked about afterwards.
	if err := workspaceProjectRmAudit(ctx, deps.Audit, actor, project); err != nil {
		return err
	}

	workspaceProjectRmWarning(out, project, target.Project)

	// The owner's own declaration goes last, so that a failure anywhere above
	// leaves the member declared and the whole command repeatable: the table is
	// what the owner's `fugaro update` publishes, and a member dropped from it
	// while the member side still held the workspace's records would be a
	// detach nobody could finish.
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := workspaceRemoveKey(sel.Path, old, []string{"workspace_members", project})
	if err != nil {
		return err
	}
	if err := workspaceEditLocal(cmd, lc, sel.Path, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("stop declaring member %s in workspace %s: drop its entry from the workspace's member table", safetext.Field(project), safetext.Field(lc.Name)),
		Command: fmt.Sprintf("fugaro workspace project rm %s", safetext.Field(project)),
	}); err != nil {
		return err
	}
	return workspaceChain(cmd, lc, sel.Path, old, next, workspaceDoor("project rm"), workspaceOwnerHalf(), *o.noApply)
}

// workspaceProjectRmRefuseSession is A7's fence for this command: a coding
// agent's session writes none of what a detach writes — the workspace's roster
// and audit trail, the member's own database, a snapshot of the member's state
// — so the refusal comes before the first read. `--no-save-state` is named and
// does not lift it: a session that skipped the snapshot would still be writing
// the member's records, and a flag that lifted one half of a refusal would be
// the question this policy does not ask.
func workspaceProjectRmRefuseSession(cmd *cobra.Command, o *workspaceProjectRmOptions) error {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if marker == "" {
		return nil
	}
	return userErr("workspace project rm detaches a project from your workspace: it takes the workspace's records back out of the member's own database, writes this workspace's roster and audit trail, and saves a snapshot of the member's state, so it is refused in a coding agent's session (%s is set); run it yourself at your own terminal, where it asks nothing — --no-save-state skips the snapshot and not this refusal", marker)
}

// workspaceProjectRmTarget names the member the way every C2 call names one: its
// GCP project and its Firebase project, which are one project only sometimes
// (F2, R10).
//
// The member's own config on this machine is what names its GCP project. The
// workspace's member table deliberately does not: it holds the member's Fugaro
// project name — the name its own config carries and the key of the table — and
// the endpoints the workspace reaches it by, and a GCP project id is derivable
// from none of them. So the read is of the member's own config, and a machine
// that does not hold it is refused rather than guessed at: every call this
// command makes lands on the member's real project, and a wrong project id
// would write a grant into somebody else's database.
func workspaceProjectRmTarget(getenv func(string) string, project string, entry localcfg.WorkspaceMember) (workspace.MemberTarget, error) {
	lc, _, err := localcfg.LoadProject(getenv, project)
	if errors.Is(err, localcfg.ErrMissing) {
		return workspace.MemberTarget{}, userErr("this machine holds no config for the member project %s, so its GCP project is unknown: the workspace's member table names members by Fugaro project name, and every call this command makes reaches the member's own project, which only the member's config names (fugaro setup --name %s writes it); run this where that config is", safetext.Field(project), safetext.Field(project))
	}
	if err != nil {
		return workspace.MemberTarget{}, userErr("reading the member project %s's own config: %v", safetext.Field(project), err)
	}
	return workspace.MemberTarget{Project: lc.GCPProject, Firebase: entry.FirebaseProject}, nil
}

// workspaceProjectRmReadRoster reads the workspace's own node and decodes it,
// and answers nil for a workspace whose roster has never been written: that is
// the node a workspace that ran no `init` has, and it is read as no admins and
// no marks rather than as an empty roster — a workspace with a member table and
// no roster yet is exactly the state this plan's first release leaves behind,
// and `init` writes the roster beside the table that is already there.
func workspaceProjectRmReadRoster(ctx context.Context, store workspace.Store) (workspace.Revision, *workspace.Roster, error) {
	rev, raw, err := store.Read(ctx)
	if err != nil {
		return workspace.Revision{}, nil, userErr("reading the workspace's roster at %s: %v", safetext.Field(store.Path()), err)
	}
	if !rev.Found {
		return rev, nil, nil
	}
	roster, err := workspace.DecodeRoster(raw)
	if err != nil {
		return workspace.Revision{}, nil, userErr("the workspace's roster at %s: %v", safetext.Field(store.Path()), err)
	}
	return rev, roster, nil
}

// workspaceProjectRmMemberGrants takes the grants the workspace wrote back out of
// the member's own roster, and answers the grants it removed.
//
// Which grants those are is the workspace's OWN node's answer (D2818) and not a
// reading of the member's roster: the mark is the workspace's record of what it
// wrote — the member's project, the address and the role — and the grant this
// removes is the one the member's roster holds for that same address and role at
// the project scope. Matching on the addresses of today's admins instead would
// be wrong in both directions: a grant the member's own admins wrote for somebody
// who happens to be a workspace admin would go with it, and a marked grant for
// somebody who is no longer an admin would be left behind in the member while its
// only mark was dropped. The mark is also what makes the command work after the
// member owner's `unlink` (D2822): the unlink takes the authority and touches no
// workspace node, so the marks are still there and still say what to take back.
//
// A member whose grants node was never written holds nothing to take back: the
// workspace's own grants are written by commands that refuse an unmigrated list
// (C2's WriteGrants, as `fugaro user grant` does), so there is nothing here
// this workspace could have written, and the write is skipped rather than
// rewriting a node the member's own CLI has not migrated.
func workspaceProjectRmMemberGrants(ctx context.Context, members workspace.MemberOps, target workspace.MemberTarget, marks []workspace.StoredGrant) ([]string, error) {
	if len(marks) == 0 {
		return nil, nil
	}
	doc, err := members.ReadGrants(ctx, target)
	if err != nil {
		return nil, userErr("reading the grants in %s's own roster: %v", safetext.Field(target.Project), err)
	}
	if !doc.Found {
		return nil, nil
	}
	grants, err := accesslib.Decode(doc.Raw)
	if err != nil {
		return nil, userErr("the grants in %s's own roster: %v", safetext.Field(target.Project), err)
	}
	held := make(map[workspaceRosterGrantKey]bool, len(marks))
	for _, m := range marks {
		member, err := accesslib.Member(m.User)
		if err != nil {
			return nil, userErr("the address %s the workspace's own node records a grant for is not one a stored grant can name, so the workspace's grants cannot be told apart in %s's roster: %v", safetext.Field(m.User), safetext.Field(target.Project), err)
		}
		held[workspaceRosterGrantKey{user: member, role: string(m.Role)}] = true
	}
	kept, removed := make([]accesslib.Grant, 0, len(grants)), make([]string, 0, len(grants))
	for _, g := range grants {
		// The project scope is part of the match and not decoration: the scope the
		// workspace writes is the project's own (D2815, D2818), so a grant of the
		// same address and role at another scope is one somebody wrote afterwards,
		// and it is not this command's to take away.
		if g.Scope.Kind == accesslib.ScopeProject && held[workspaceRosterGrantKey{user: g.User, role: string(g.Role)}] {
			removed = append(removed, g.User)
			continue
		}
		kept = append(kept, g)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	raw, err := accesslib.Encode(kept)
	if err != nil {
		return nil, userErr("writing %s's own roster back without the workspace's grants: %v", safetext.Field(target.Project), err)
	}
	if err := members.WriteGrants(ctx, target, doc, raw); err != nil {
		return nil, userErr("writing %s's own roster without the workspace's grants: %v", safetext.Field(target.Project), err)
	}
	return removed, nil
}

// workspaceRosterMarksFor answers the marks one project of the workspace's own
// node holds: the grants the workspace recorded writing into that member's
// roster, in the node's own order. A workspace whose roster was never written
// marks nothing, which is a state this plan's first release leaves behind
// (`init` writes the roster beside a table that is already there).
func workspaceRosterMarksFor(roster *workspace.Roster, project string) []workspace.StoredGrant {
	if roster == nil {
		return nil
	}
	var marks []workspace.StoredGrant
	for _, g := range roster.Grants {
		if g.Project == project {
			marks = append(marks, g)
		}
	}
	return marks
}

// workspaceRosterGrantKey is one grant as the two sides name it: the member
// string a mark's address builds and the role. It is the pair a mark and a
// member's own grant are matched on, and it is a type of its own rather than a
// joined string because the address is a value with a grammar and a role is one
// of three words — joining them would be a lookup that depends on no address
// ever containing the join.
type workspaceRosterGrantKey struct {
	user string
	role string
}

// workspaceProjectRmMemberMirror removes the member's mirror, and answers
// whether there was one to remove.
//
// The document that removes it is the empty JSON object. The write replaces the
// whole node and refuses anything that is not a JSON object (C2), and the
// database prunes an empty object on the way in — the same pruning that makes
// an absent key and an empty list one thing — so `{}` is the one document that
// leaves the node absent, which is what a member the workspace has not mirrored
// into reads as, and what the installation-level refusals key on (R11). The
// mirror's own key inside the document is not this file's to name: it is C2's
// document, and an empty object removes the node whatever that key turns out to
// be. (`null` would be the obvious spelling and is the one C2 refuses.)
func workspaceProjectRmMemberMirror(ctx context.Context, members workspace.MemberOps, target workspace.MemberTarget) (bool, error) {
	doc, err := members.ReadMirror(ctx, target)
	if err != nil {
		return false, userErr("reading the mirror in %s's own database: %v", safetext.Field(target.Project), err)
	}
	if !doc.Found {
		return false, nil
	}
	if err := members.WriteMirror(ctx, target, doc, []byte("{}")); err != nil {
		return false, userErr("removing the mirror from %s's own database: %v", safetext.Field(target.Project), err)
	}
	return true, nil
}

// workspaceProjectRmAuthority drops the authority the workspace holds over the
// member, or names the person who has to.
//
// The SA mechanism's hold is the workspace IAM account's `roles/owner`, and the
// account's own authority is what removes it: Unlink is idempotent — "a member
// the account does not own is not an error" — so a member owner's unlink having
// already run is not a state this has to tell from any other (D2822).
//
// The folder mechanism's hold is where the project sits, and the move back is
// the member owner's act, so nothing is done here and their command is named
// instead: U6 leaves open whether a workspace admin holding `roles/owner` on
// the folder can move a project back to its original parent without rights
// there, and until Check 720 settles it the honest answer is the member owner's
// own command. An authority that names no mechanism at all is the same answer
// in weaker words: a workspace whose `init` never ran holds nothing live, and
// if the project sits in a folder that predates it, the member owner's unlink
// is still what takes it back.
func workspaceProjectRmAuthority(ctx context.Context, out io.Writer, authority workspace.Authority, target workspace.MemberTarget) error {
	switch authority.Kind() {
	case workspace.AuthoritySA:
		if err := authority.Unlink(ctx, target); err != nil {
			return userErr("dropping the workspace IAM account's roles/owner on %s: %v", safetext.Field(target.Project), err)
		}
		fmt.Fprintf(out, "dropped the workspace IAM account's roles/owner on %s\n", safetext.Field(target.Project))
	case workspace.AuthorityFolder:
		fmt.Fprintf(out, "the workspace still holds %s in its folder: the member's own owner runs `fugaro workspace project unlink` on their machine to move the project back, because a folder owner is not known to be able to do it for them (U6)\n", safetext.Field(target.Project))
	default:
		fmt.Fprintf(out, "this workspace records no mechanism (fugaro workspace init chooses it), so no authority of its own was dropped: if the project sits in a workspace folder, its own owner takes it back with `fugaro workspace project unlink`\n")
	}
	return nil
}

// workspaceProjectRmDropMarks drops the marks the workspace's own node keeps
// for one member's project, and answers whether the node was written and which
// grants the re-reads made it take back.
//
// The marks go with the member: they record grants the workspace wrote into a
// project it no longer holds, and the node is what the drift comparison reads
// the workspace's grants against. The node is written under the revision it was
// read at, and a write that lost a race is answered the way C1's contract says
// — by reading again and judging the change against what is really there, so a
// change this command never saw survives — up to a bound past which the node is
// being written faster than this command can converge on it, which is reported
// rather than looped on.
//
// `acted` is what the caller took the member's grants back with, and the
// re-read is where a mark written by somebody else while this command ran would
// otherwise be thrown away: the grant that mark records is still in the member's
// roster, and this command is the last thing that holds both halves — the
// marks, which it is about to write away, and the member's roster, which
// nothing reads against them afterwards. Dropping the mark and leaving the
// grant would leave access the workspace wrote in a project the owner's table
// no longer declares, with nothing left that could find it. So a re-read whose
// marks differ from the ones acted on is acted on too, before the node is
// written, and the addresses it took back are answered to the caller.
func workspaceProjectRmDropMarks(ctx context.Context, store workspace.Store, members workspace.MemberOps, target workspace.MemberTarget, rev workspace.Revision, roster *workspace.Roster, project string, acted []workspace.StoredGrant) (bool, []string, error) {
	var taken []string
	for attempt := 0; ; attempt++ {
		if !rev.Found || roster == nil || !workspaceRosterDropsProjectMarks(roster, project) {
			return false, taken, nil
		}
		raw, err := workspace.EncodeRoster(roster)
		if err != nil {
			return false, taken, userErr("the workspace's roster this command would write: %v", err)
		}
		err = store.Write(ctx, rev, raw)
		if err == nil {
			return true, taken, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return false, taken, userErr("writing the workspace's roster at %s: %v", safetext.Field(store.Path()), err)
		}
		if attempt == 2 {
			return false, taken, userErr("writing the workspace's roster at %s: the node moved under this write three times, so nothing was written; run this again", safetext.Field(store.Path()))
		}
		rev, roster, err = workspaceProjectRmReadRoster(ctx, store)
		if err != nil {
			return false, taken, err
		}
		// Both lists come from a node this package's own reader validated, so
		// the order is the document's own: sorted by project and then by address.
		if now := workspaceRosterMarksFor(roster, project); !slices.Equal(now, acted) {
			took, err := workspaceProjectRmMemberGrants(ctx, members, target, now)
			if err != nil {
				return false, taken, err
			}
			taken = append(taken, took...)
			acted = now
		}
	}
}

// workspaceRosterDropsProjectMarks removes one project's marks from a roster
// and answers whether it held any. The list is spliced in place rather than
// rebuilt, so the grants that stay keep the order EncodeRoster requires.
func workspaceRosterDropsProjectMarks(r *workspace.Roster, project string) bool {
	kept := r.Grants[:0]
	for _, g := range r.Grants {
		if g.Project == project {
			continue
		}
		kept = append(kept, g)
	}
	changed := len(kept) < len(r.Grants)
	r.Grants = kept
	return changed
}

// workspaceProjectRmAudit files the workspace's own record of the removal: one
// create-only entry, with the actor who ran the command, the action the node
// has for exactly this change, and the member it was about. The event names no
// subject, which is what an event about a project does (C1: the key is left
// out).
func workspaceProjectRmAudit(ctx context.Context, sink workspace.AuditSink, actor, project string) error {
	now := time.Now().UTC().Truncate(time.Second)
	id, err := workspace.NewAuditEventID(now, rand.Reader)
	if err != nil {
		return userErr("minting the id of the workspace's audit entry: %v", err)
	}
	raw, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    now,
		Actor:   actor,
		Action:  workspace.ActionProjectRemove,
		Project: project,
	})
	if err != nil {
		return userErr("the workspace's audit entry for removing %s: %v", safetext.Field(project), err)
	}
	if err := sink.Create(ctx, id, raw); err != nil {
		return userErr("filing the workspace's audit entry for removing %s: %v", safetext.Field(project), err)
	}
	return nil
}

// workspaceProjectRmWarning is what the command says once the member is
// detached: everything it has is still there, and it is still costing money.
// The two things the spec names are named — the Cloud Scheduler jobs that
// rebuild the project's images, and the credentials service — and the console
// page the owner shuts the project down from is printed and never opened (R3).
func workspaceProjectRmWarning(out io.Writer, project, gcpProject string) {
	fmt.Fprintf(out, "nothing was deleted: %s is still a Google Cloud project with everything deployed in it, and no Firebase resource was touched\n", safetext.Field(gcpProject))
	fmt.Fprintf(out, "what is still deployed there keeps costing money until the project is shut down: the Cloud Scheduler jobs that rebuild its images, and the credentials service\n")
	fmt.Fprintf(out, "shut %s down yourself at %s\n", safetext.Field(project), workspaceProjectShutdownPage+url.QueryEscape(gcpProject))
}
