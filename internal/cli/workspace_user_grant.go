package cli

// workspace_user_grant.go is WU8: `fugaro workspace user grant` and `revoke`
// (D2802, D2806, D2811, D2813, D2815, D2818, D2821, R11), the workspace's door
// onto ONE member's own roster.
//
// The command exists because a member's roles are its own record and its own
// door moved: on a linked member the installation-level `fugaro user grant`
// refuses and names this command (D2813). What it writes is the same node
// `fugaro user grant` writes -- the member's `config/admin/access`, decoded and
// encoded by internal/access -- and the judgement it applies under is the same
// judgement, for the same reason: a role is a pair of a role and a scope, a
// grant is a subset of the grantor's own scope at that role or higher (F17.1),
// a revoke or a demotion needs admin over the pair (F24), and a change that
// would empty the project's last project admin is refused (F17.2, D2815's
// "roles-scopes' subset and last-admin refusals"). Those rules live in one
// place -- user_grant.go's userPairJudge, userHoldsPair and userPairNext -- and
// this file calls them rather than restating them, because a second spelling of
// the subset rule is a second answer to "what may this person do here".
//
// Four things are this command's own, and it refuses or says each of them
// rather than guessing at one.
//
//   - **The member's own list is the gate (D2813).** Every membership write
//     mirrors the workspace's member list into the member's database
//     (`config/admin/workspace-members`, C2's PathMemberMirror), so a member's
//     own command -- and this one, run by anyone who can reach that member --
//     can tell whether the workspace knows the person being granted. An address
//     the mirror does not list is refused: the workspace never admitted them, and
//     a grant is not an admitting. The mirror is ADVISORY and a member's own
//     admins hold their database's write, so a rewritten mirror can hide the
//     whole list or land one that is not there; nothing here rests on it being
//     the record -- drift, run by a workspace admin against the workspace's own
//     node, is that fence (D2813, WU12), and the refusal says so. A revoke is
//     not gated on the list at all: a revoke only takes access away, and
//     refusing one leaves the grant it names in place.
//   - **A machine is never granted (D2806).** A machine member is recorded in
//     the workspace's node with the roles the workspace expects it to hold, and
//     gets no roster, no grant and no audit of its own: its real IAM is the
//     workspace owner's own, wherever that is. The mirror carries the kind, so
//     the refusal is made from the entry itself.
//   - **The audit entry goes to the MEMBER (D2811).** A project admin holds no
//     write on the workspace's own project, so D2811's amended answer puts the
//     entry where the roster write already reaches: the member's own
//     `config/admin/workspace-audit`, create-only, in C1's event shape. What a
//     project admin needs, then, is exactly what `fugaro user grant` needs today
//     -- `roles/firebasedatabase.admin` on the member -- and nothing on the
//     workspace's project at all. That is the whole of the "a project admin
//     needs no grant on the workspace project" claim, and it is a property of
//     this command's dependency list: it holds C2's MemberOps and no workspace
//     store, so there is no workspace node it could write.
//   - **The member's own `fugaro update` is what applies it (D2801).** Nothing
//     here binds anything: the grant lands in the member's record and the
//     member's own machine turns it into bindings at its next update. Applying
//     a member's Terraform from this machine is exactly what D2801's round-1
//     amendment forbade (F7), so the command's success sentence names the
//     member's own update, and never this machine's.
//
// The member operation travels as C2 does: this file reaches a member through
// `workspace.MemberOps` (ReadMirror, ReadGrants, WriteGrants, Audit), whose
// concrete side WU4 provides and I1 adapts. A test injects its own MemberOps
// (wu8FakeMemberOps, in this file's test), never the contract's stub.
//
// The MemberTarget this file builds names the member's GCP project and leaves
// its Firebase project empty, which C2 defines as the two being one project
// (F2, R10). Nothing this command can read holds the other name: the workspace's
// roster deliberately does not (roster.go's own list of what it leaves out), and
// a member's own config is on the member's machine, so a command that filled the
// field would be guessing at a fact of the member's installation. R-member-firebase
// -- the contract change the plan anticipates, reaching this task among others --
// is where filling it belongs if WU4 cannot read it from the member's own outputs.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
	"github.com/dimipaun/fugaro/internal/workspace"
)

type workspaceUserPairOptions struct {
	scope string
}

// newWorkspaceUserGrantCmd is `fugaro workspace user grant ADDRESS PROJECT ROLE`.
func newWorkspaceUserGrantCmd(ops workspace.MemberOps) *cobra.Command {
	return newWorkspaceUserPairCmd(ops, true)
}

// newWorkspaceUserRevokeCmd is `fugaro workspace user revoke ADDRESS PROJECT ROLE`.
func newWorkspaceUserRevokeCmd(ops workspace.MemberOps) *cobra.Command {
	return newWorkspaceUserPairCmd(ops, false)
}

// newWorkspaceUserPairCmd builds grant and revoke: one address, one member
// project, one pair, one direction. The repository's commands that write who may
// do what are built as a constructor taking what they reach (the shape I1
// registers), so nothing here reads a config or opens a cloud of its own: the
// member is named by the argument, and the caller's own credentials are what the
// member's database is reached with.
func newWorkspaceUserPairCmd(ops workspace.MemberOps, grant bool) *cobra.Command {
	o := &workspaceUserPairOptions{}
	use, short := "revoke ADDRESS PROJECT ROLE", "Take a role away from a person in a workspace member"
	if grant {
		use, short = "grant ADDRESS PROJECT ROLE", "Give a person a role in a workspace member"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `` + pairWord(grant) + ` one person a role in one member project of a workspace, in
that member's own record of who may do what.

The record is the member's grants node at config/admin/access -- the same node
fugaro user grant writes -- and it is written in the same format, so the
member's own fugaro update derives the same IAM lists from it. What differs is
this command's gate and its trail:

  - a grant's address must be in the member's own copy of the workspace's
    member list (config/admin/workspace-members), which the workspace mirrors
    into every member it manages. An address the list does not carry was never
    admitted to the workspace, and a grant is not an admitting. A revoke needs
    the member to be linked and reads the list no further: taking access away is
    never blocked by a list the member's own admins can rewrite;
  - the change is filed in the member's own audit trail at
    config/admin/workspace-audit, because a project admin holds no write on the
    workspace's own project (D2811).

The judgement is the installation command's: a grant is a subset of the
grantor's own scope at that role or higher, a revoke or a demotion needs admin
over the pair, and the member's last project admin stays.

Writing the member's grants node needs roles/firebasedatabase.admin on the
member, held by that project's owners and editors and by its
terraform.budget_admins. No grant on the workspace's own project is needed or
read.

The member's own fugaro update is what applies the change to its IAM: this
command writes the record and binds nothing (D2801), and it never runs
Terraform.

An agent session is refused: ` + pairDone(grant) + ` here is a change to who may do what, and a
person runs it at their own terminal.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return workspaceUserPair(cmd, ops, o, args, grant)
		},
	}
	cmd.Flags().StringVar(&o.scope, "scope", "", "what the role covers: project (the default), all-repos, or a comma-separated list of repository patterns")
	return cmd
}

// workspaceUserPair is grant and revoke's body: one address, one member, one
// pair, one direction.
func workspaceUserPair(cmd *cobra.Command, ops workspace.MemberOps, o *workspaceUserPairOptions, args []string, grant bool) error {
	ctx := cmd.Context()
	// Before anything is read: a change to who may do what is the member
	// owner's own act (A7, D1590), and the sentence is the command to run.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: fugaro workspace user %s changes who may do what in a member project, and the member's grants node is written with the credentials of one of that member's administrators, so it is run by a person at their own terminal. Run: %s. %s",
			pairDone(grant), workspaceUserHolderCommand(o, args, grant), initflow.AgentRefusal(m))
	}
	// GODEBUG=http2debug makes Go print HTTP requests with their credentials to
	// stderr, and it is read before main runs (refuseHTTP2Debug). This command
	// makes Google's userinfo call and reaches a database.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	address, err := workspaceGrantAddress(args[0])
	if err != nil {
		return err
	}
	project := args[1]
	if err := workspaceRefuseName(project, "member project"); err != nil {
		return err
	}
	role, ok := accesslib.ParseRole(args[2])
	if !ok {
		return userErr("the role %q must be one of %s (it names the role this pair holds, and none of them is the default)", safetext.Field(args[2]), roleNames())
	}
	scope, err := workspaceGrantScope(o.scope)
	if err != nil {
		return err
	}
	member, err := accesslib.Member(address)
	if err != nil {
		return userErr("%s is not a member a grant can name: %v", safetext.Field(address), err)
	}
	target := workspace.MemberTarget{Project: project}
	if err := workspaceGrantGate(ctx, ops, target, address, grant); err != nil {
		return err
	}
	caller, err := userCallerLookup(ctx)
	if err != nil {
		return userErr("this command decides what you may change from the credential it is using, and it could not read who that credential belongs to: %v. The caller is the credential and never the local config's `user:` key or git config user.email (finding F21): sign in with gcloud auth application-default login and run this again", err)
	}
	return workspaceGrantWrite(ctx, cmd, ops, target, caller, address, member, role, scope, grant)
}

// workspaceUserHolderCommand is the command a person has to run instead of the
// agent: the subcommand, the three arguments and the scope when one was given.
// It is printed for a shell to paste, so its values are the agent's own argv
// escaped as shell words (shellword.Quote), and it is built before any argument
// is validated -- the refusal is about who is running, so it comes first.
func workspaceUserHolderCommand(o *workspaceUserPairOptions, args []string, grant bool) string {
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	words := []string{selfCommand(), "workspace", "user", verb}
	for _, arg := range args {
		words = append(words, shellword.Quote(arg))
	}
	line := strings.Join(words, " ")
	if o.scope != "" {
		line += " --scope " + shellword.Quote(o.scope)
	}
	return line
}

// workspaceGrantAddress is the address a workspace grant names, in the canonical
// spelling the workspace's own list and the member's grants node both carry
// (workspace.CanonicalAddress and access.Member fold the domain the same way).
//
// The workspace writes addresses and not the `user:<address>` member form
// `fugaro user grant` takes, so a member prefix is refused rather than folded
// into a second spelling of one person: `access.Member` would prefix it a second
// time, and the mirror would list nobody by the name it wrote.
func workspaceGrantAddress(arg string) (string, error) {
	if arg != strings.TrimSpace(arg) {
		return "", userErr("the address %q has whitespace around it", safetext.Field(arg))
	}
	for _, prefix := range []string{"user:", "group:", "domain:", "serviceAccount:"} {
		if rest, ok := strings.CutPrefix(arg, prefix); ok {
			return "", userErr("the address is written as a member (%s), and a workspace grant names the address itself: run this with %s, which is the spelling the workspace's own member list carries", safetext.Field(arg), safetext.Field(rest))
		}
	}
	address, err := workspace.CanonicalAddress(arg)
	if err != nil {
		return "", userErr("%s is not an address a grant can name: %v", safetext.Field(arg), err)
	}
	return address, nil
}

// workspaceGrantScope parses --scope, whose default is the whole member project
// (D2815): the grammar is access's own, so the scope this command writes is the
// scope the member's resolver reads back.
func workspaceGrantScope(value string) (accesslib.Scope, error) {
	if value == "" {
		value = "project"
	}
	scope, err := accesslib.ParseScope(value)
	if err != nil {
		return accesslib.Scope{}, userErr("--scope: %v", err)
	}
	return scope, nil
}

// workspaceGrantGate is the refusals in front of a change: the member must be
// one the workspace has mirrored into, and -- for a GRANT -- the person must be
// one the member's own list names, and not a machine.
//
// A revoke stops at the link, and that asymmetry is the direction's. The list is
// the gate on admission, and a revoke is not an admission: refusing it can only
// leave access in place. Everything a revoke must remove passes through here --
// somebody taken out of the workspace whose offboarding failed, a person the
// workspace's own node no longer lists, one whose record in this member's copy
// was rewritten by that member's admins -- and none of them is a reason to keep
// the grant, least of all once D2813's refusal lands in `fugaro user revoke` and
// this command is the only door left. The list is advisory besides (a member's
// own admins hold their database's write), so refusing a revoke on it would hand
// them a way to freeze the grants they cannot otherwise keep.
//
// The read is the member's mirror and nothing else. R11's other half -- whether
// the workspace still holds the member -- is a fact of the workspace's own
// authority, and a member owner's unlink leaves this mirror where it is until
// the workspace project's owner runs project rm (D2822): the grants stay, and
// status is where "unlinked, grants left" is read.
func workspaceGrantGate(ctx context.Context, ops workspace.MemberOps, target workspace.MemberTarget, address string, grant bool) error {
	mirror, err := ops.ReadMirror(ctx, target)
	if err != nil {
		return workspaceMemberReadErr(fmt.Sprintf("the member project %s's member list at %s", safetext.Field(target.Project), workspace.PathMemberMirror), err)
	}
	if !mirror.Found {
		return userErr("nothing was written: %s holds no member list, so it is not a member of a workspace this command can grant in. A member the workspace manages carries the list at %s, written when the workspace added it, and a member whose owner has not linked it keeps fugaro user grant and no workspace command reaches it (D2819, R11). Run: fugaro workspace project link on the member's own machine -- linking a member is its owner's own act",
			safetext.Field(target.Project), workspace.PathMemberMirror)
	}
	if !grant {
		return nil
	}
	entries, err := workspaceMirrorEntries(mirror)
	if err != nil {
		return userErr("nothing was written: %s. The member's own admins can rewrite it, so a list this command cannot read is one it cannot judge a grant against (D2813)", err)
	}
	entry, listed := workspaceMirrorEntryAt(entries, address)
	switch {
	case !listed:
		return userErr("nothing was written: %s is not in %s's member list, so the workspace has not admitted them and this command does not admit them either (D2813). A grant is for a person the workspace lists: a workspace admin adds them with 'fugaro workspace user add %s'. The list is the member's own copy, so a person the workspace does list but whose copy was rewritten is refused here until the workspace writes the copy again -- the fence is drift, run by a workspace admin against the workspace's own record, and never this list",
			safetext.Field(address), safetext.Field(target.Project), safetext.Field(address))
	case entry.Kind != workspace.KindHuman:
		return userErr("nothing was written: %s is a machine (%s) in %s's member list, and the workspace records a machine in its own node and grants it nothing in a member (D2806): a machine's real IAM is its owner's own, wherever that is, and no grant, roster or audit of a member is written for one",
			safetext.Field(address), safetext.Field(string(entry.Kind)), safetext.Field(target.Project))
	}
	return nil
}

// workspaceMirrorEntries decodes the member's mirror -- C2's document at
// PathMemberMirror, C1's entry shape -- the way every other document here is
// read: strictly, so a list this command acts on is one it read whole. A
// document with no members key is a list of nobody rather than an unreadable
// document, because the database prunes an empty list and the absent key and
// the empty one are the same node (C1's contract).
func workspaceMirrorEntries(mirror workspace.MemberDoc) ([]workspace.MirrorEntry, error) {
	entries, err := DecodeMemberMirror(mirror.Raw)
	if err != nil {
		return nil, fmt.Errorf("the member's member list at %s is not a document this command reads: %v", workspace.PathMemberMirror, err)
	}
	return entries, nil
}

// workspaceMirrorEntryAt is the entry the list holds for one address, and
// whether the list holds one at all: the address of both sides is the canonical
// spelling (workspace.CanonicalAddress), so the comparison is of one spelling
// with one spelling.
func workspaceMirrorEntryAt(entries []workspace.MirrorEntry, address string) (workspace.MirrorEntry, bool) {
	for _, entry := range entries {
		if entry.Address == address {
			return entry, true
		}
	}
	return workspace.MirrorEntry{}, false
}

// workspaceGrantWrite reads the member's grants node, judges the change against
// that read, and writes it conditionally on the read's ETag, re-judging from a
// fresh read on a mismatch -- the same loop `fugaro user grant` makes (F23),
// because the rules it is judged under are the same rules.
//
// The implicit admins of the member are not among this command's inputs. They
// are the member project's GCP owners and editors (its IAM policy, a live read
// this command does not make) and its own terraform.budget_admins, which live in
// the member's config on the member's machine; a caller the node places nowhere
// is judged by the write itself, which only a holder of
// roles/firebasedatabase.admin can land -- F22's table, and the reason a project
// admin needs no grant of their own.
func workspaceGrantWrite(ctx context.Context, cmd *cobra.Command, ops workspace.MemberOps, target workspace.MemberTarget, caller, address, member string, role accesslib.Role, scope accesslib.Scope, grant bool) error {
	out := cmd.OutOrStdout()
	for attempt := 0; attempt < userPairAttempts; attempt++ {
		doc, err := ops.ReadGrants(ctx, target)
		if err != nil {
			return workspaceMemberReadErr(fmt.Sprintf("the member project %s's grants node at %s", safetext.Field(target.Project), accesslib.PathNode), err)
		}
		var held []accesslib.Grant
		if doc.Found {
			if held, err = accesslib.Decode(doc.Raw); err != nil {
				return userErr("nothing was %s: the member project %s's grants node at %s is not a document its own reader accepts, so this command writes nothing over it: %v",
					pairPast(grant), safetext.Field(target.Project), accesslib.PathNode, err)
			}
		}
		if grant && userHoldsPair(held, member, role, scope) {
			fmt.Fprintf(out, "%s already holds %s at %s in member project %s's grants node; nothing was written\n",
				safetext.Field(member), role, safetext.Field(scope.String()), safetext.Field(target.Project))
			return nil
		}
		next, err := userPairJudge(nil, held, caller, member, role, scope, grant)
		if err != nil {
			return err
		}
		raw, err := accesslib.Encode(next)
		if err != nil {
			return userErr("nothing was %s: the grants node this change would write is one the member's own reader refuses: %v", pairPast(grant), err)
		}
		err = ops.WriteGrants(ctx, target, doc, raw)
		switch {
		case err == nil:
			return workspaceGrantDone(ctx, out, ops, target, caller, address, role, scope, grant)
		case errors.Is(err, rtdb.ErrPrecondition):
			// Another writer landed between the read and this write: the node
			// has moved, so the picture this change was judged against is not
			// the one it would be written over. Read it again and judge again.
			continue
		case errors.Is(err, rtdb.ErrPermission):
			return userPairDenied(nil, grant)
		default:
			return workspaceMemberErr(fmt.Sprintf("the member project %s's grants node could not be written", safetext.Field(target.Project)), err)
		}
	}
	return userErr("nothing was %s: the member project %s's grants node changed under every one of %d attempts, so another administrator is writing it right now and the change cannot be judged against a picture that stays still. Read it with 'fugaro user ls' there and run this again",
		pairPast(grant), safetext.Field(target.Project), userPairAttempts)
}

// workspaceGrantDone files the audit entry and prints what the change was and
// what applies it.
//
// The entry is written AFTER the change and never instead of it (C1's contract):
// the roster is the change and the trail is its record, so a trail write that
// failed leaves the change landed and says so rather than reading as a change
// nothing wrote. The actor is the credential's own address, stripped of the
// `user:` member form, because C1's event carries the address.
func workspaceGrantDone(ctx context.Context, out io.Writer, ops workspace.MemberOps, target workspace.MemberTarget, caller, address string, role accesslib.Role, scope accesslib.Scope, grant bool) error {
	now := time.Now()
	action := workspace.ActionGrantWrite
	if !grant {
		action = workspace.ActionGrantRemove
	}
	id, err := workspace.NewAuditEventID(now, rand.Reader)
	if err != nil {
		return userErr("the change was written into the member project %s's grants node and its audit entry could not be minted, so nothing was filed: %v. The change stands; the trail has no entry for it", safetext.Field(target.Project), err)
	}
	raw, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    now,
		Actor:   strings.TrimPrefix(caller, "user:"),
		Action:  action,
		Project: target.Project,
		Subject: address,
		Role:    workspace.Role(role),
	})
	if err != nil {
		return userErr("the change was written into the member project %s's grants node and its audit entry is one the trail's own reader would refuse, so nothing was filed: %v. The change stands; the trail has no entry for it", safetext.Field(target.Project), err)
	}
	if err := ops.Audit(ctx, target, id, raw); err != nil {
		return workspaceMemberErr(fmt.Sprintf("the change was written into the member project %s's grants node and its audit entry at %s could not be filed", safetext.Field(target.Project), workspace.PathMemberAudit), err)
	}
	if !grant {
		fmt.Fprintf(out, "%s no longer holds %s at %s in member project %s's grants node, and the change is filed in that member's audit trail at %s. The member's own fugaro update is what takes the binding away (D2801): this command wrote the record and binds nothing\n",
			safetext.Field(address), role, safetext.Field(scope.String()), safetext.Field(target.Project), workspace.PathMemberAudit)
		return nil
	}
	fmt.Fprintf(out, "%s now holds %s at %s in member project %s's grants node, and the change is filed in that member's audit trail at %s. The member's own fugaro update is what applies it (D2801): this command wrote the member's record and binds nothing, so run fugaro update on the member's own machine for the grant to reach its IAM\n",
		safetext.Field(address), role, safetext.Field(scope.String()), safetext.Field(target.Project), workspace.PathMemberAudit)
	return nil
}

// workspaceMemberReadErr classifies a failed read of a member's own node: a
// refusal is the credential's, and says what reading a member takes -- the same
// distinction this machine's own dbErr makes for the installation's node. It
// matters more here: the read is what the change is judged against, so a
// credential that can write but not read would otherwise take a decision from a
// read it could not make.
func workspaceMemberReadErr(what string, err error) error {
	if errors.Is(err, rtdb.ErrPermission) {
		return userErr("nothing was written: %s refused this credential's read, and that read is what this command judges the change against. Reading a member's database needs roles/firebasedatabase.viewer on its Firebase project, which its owners and editors hold (D689): %v", what, err)
	}
	return workspaceMemberErr(what, err)
}

// workspaceMemberErr reports a failure against a member's own database. A
// refusal the layer below already classified keeps its own sentence and its own
// exit code -- WU4's concrete writer refuses an unmigrated list in its own words,
// and that refusal is the member's to fix rather than a remote failure -- and
// anything else is marked a remote failure, which is what a database that did not
// answer is.
func workspaceMemberErr(what string, err error) error {
	return remote(fmt.Errorf("%s: %w", what, err))
}
