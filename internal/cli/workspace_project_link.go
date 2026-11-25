package cli

// This file is `fugaro workspace project link` and `unlink` (WU18): the two
// commands THE MEMBER OWNER runs, on their own machine, with their own
// credentials (D2800, D2819). It is the whole of the member's side of the
// authority: the workspace's own `project add` (WU5) records the member and
// writes the stored grants, but nothing on the workspace's side can create the
// authority, and nothing here can read the workspace's roster — a member's
// machine holds no grant on the workspace's project at all (F11).
//
// What each command does, and what it deliberately does not:
//
//   - `link` calls C2's Authority.Link, which moves the member's projects into
//     the workspace's folder or grants the workspace's service account
//     `roles/owner` on them where they stand, and it files one entry in the
//     MEMBER's own audit trail. It does NOT write the member's mirror: the
//     mirror is the workspace owner's `project add`, because the member list is
//     the workspace's and this machine cannot read it.
//   - `unlink` calls Authority.Unlink, which drops the authority and only the
//     authority (D2822), files one entry in the same trail, and says in as many
//     words that the workspace admins' stored grants stay in this member's own
//     roster until the workspace project's owner runs `workspace project rm`.
//   - In the folder model `link` reports the policy difference the move makes
//     BEFORE it is made: a GCP project takes the IAM and organization policies
//     of the parent it hangs under, so the member loses whatever its old parent
//     carried and takes on the workspace folder's (threat-model.md §1 and §7
//     item 10). The report names what this machine can read on both sides and
//     says what it cannot read, because linking cannot undo an inheritance it
//     has changed.
//
// Both are refused on a config that is not the member's own and on one whose
// pin names another workspace: the authority is given by the member owner's own
// statement of which workspace it trusts, and a command that guessed the
// workspace from anything else would be the direction rule 1 of the spec does
// not survive. The report is printed and never asked about — Fugaro asks for
// nothing (docs/no-prompt-policy).
//
// The commands are constructors only: I1 builds workspaceLinkDeps from the real
// WU2/WU4/WU20 code and registers both under `workspace project`
// (workspace_wiring.go), so nothing here reaches the cloud, builds a reporter
// or touches the command tree.

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspaceLinkFolders is the Resource Manager read the folder model's report
// needs, and the whole of it: the parent a project hangs under now, and the
// owners a parent carries, which is this machine's own IAM read of the old
// side. *infra.Folders satisfies it as it stands (WU2 writes its own interface
// the same way and for the same reason: internal/infra's folder surface is
// basic types).
//
// The workspace's side of the difference is not read here: C2's
// Authority.AdminBindings answers it, and it is the one reader of the live
// binding, so a second one would be a second answer to what a workspace admin
// holds (D2823).
type workspaceLinkFolders interface {
	Parent(ctx context.Context, project string) (string, error)
	Owners(ctx context.Context, parent string) ([]string, error)
}

// workspaceLinkDeps is what the two commands are handed: C2's two seams, the
// Resource Manager read the folder report makes, and the clock the audit
// entry's id carries. I1 builds it (workspace_wiring.go); the fakes of this
// task's own test are the other builder, and they are what make the refusals
// and the order of the report testable without a cloud.
type workspaceLinkDeps struct {
	Authority workspace.Authority
	Members   workspace.MemberOps
	// Folders is read for the folder model's report alone and may be nil; the
	// report says so rather than moving a project with the difference unread.
	Folders workspaceLinkFolders
	// Now is the clock the audit entry's id and moment are taken from; nil is
	// time.Now.
	Now func() time.Time
}

func (d workspaceLinkDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// workspaceLinkMemberRef is the member the two commands act on, read from this
// machine's own config: the member's Fugaro project name for the sentences, the
// workspace its pin names, and the pair of projects C2's MemberTarget carries
// (F2, R10).
type workspaceLinkMemberRef struct {
	name      string
	workspace string
	target    workspace.MemberTarget
}

// projects is the member's projects in the order they move, the GCP project
// first: the same order WU2's FolderMember uses, and for the same reason — a
// project whose Firebase project is its own is one project.
func (r workspaceLinkMemberRef) projects() []string {
	if r.target.Firebase == "" || r.target.Firebase == r.target.Project {
		return []string{r.target.Project}
	}
	return []string{r.target.Project, r.target.Firebase}
}

func newWorkspaceProjectLinkCmd(deps workspaceLinkDeps) *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Give the workspace named in this project's pin authority over it",
		Long: `Give the workspace authority over the selected project, from this machine and
with your own credentials.

This is the member owner's own act and the only way the authority is created
(D2800, D2819): the workspace's own project add records the member and writes its
stored grants, but nothing on the workspace's side can create this. The
membership and the pin must already be in the selected config — fugaro workspace
add writes them — because the pin is this project's own statement of which
workspace it trusts, its key and the account that acts for it there.

In the folder model this moves the project into the workspace's folder, and a
project takes the IAM and organization policies of the parent it hangs under: it
loses whatever its old parent carried and takes on the workspace folder's. That
difference is reported here, before the move, with the policies this machine can
read on both sides named; what linking cannot do is undo an inheritance it has
changed. The service account model moves nothing — the account is granted on the
project where it stands — so it reports no policy change.

The mirror and the workspace admins' stored grants are not written here: the
member list is the workspace's, and this machine holds no grant on the
workspace's project at all. The workspace's owner runs fugaro workspace project
add on theirs, which is what writes them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceLink(cmd, deps, o, true)
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

func newWorkspaceProjectUnlinkCmd(deps workspaceLinkDeps) *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Drop the workspace's authority over this project, and only it",
		Long: `Drop the authority the workspace named in this project's pin holds over it.

The member owner's own act, made with their own credentials (D2819): in the
folder model the projects move back to the parent the link recorded, and in the
service account model the owner grant is removed. Nothing else changes.

What it does not do is remove the workspace admins' stored grants from this
member: unlink drops the authority and ONLY the authority (D2822). Those grants
stay in this member's own roster — they are rows in a document the member's own
config and update own — until the workspace project's owner runs fugaro
workspace project rm, which removes them and takes the mirror away. This command
says so and names that command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceLink(cmd, deps, o, false)
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// runWorkspaceLink is both commands' body: everything but the direction is one
// reading of this machine's own config and one judgement, so link and unlink
// cannot come to different answers about which workspace a config names or
// whether the authority may be given at all.
func runWorkspaceLink(cmd *cobra.Command, deps workspaceLinkDeps, o cloudOptions, link bool) error {
	ctx := cmd.Context()
	what, verb := "fugaro workspace project link", "link"
	if !link {
		what, verb = "fugaro workspace project unlink", "unlink"
	}
	// Before anything is read: the refusal is about who is running (D1590).
	// This command gives away a project owner's authority over this project, so
	// it is run by a person at their own terminal — a coding agent's session
	// holds the owner's credentials, and the refusal is a speed bump and not a
	// boundary, which is why the sentence names the command to run.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was %s: the workspace's authority over this project is the member owner's own act, made with the owner's own credentials on the owner's own machine (D2800, D2819), so it is run by a person at their own terminal. Run: %s. %s", workspaceLinkPast(link), what, initflow.AgentRefusal(m))
	}
	// GODEBUG=http2debug makes Go print HTTP requests with their credentials to
	// stderr, and it is read before main runs (refuseHTTP2Debug). This command
	// moves a project and writes a member's database with the caller's own
	// credentials.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	ref, err := workspaceLinkMember(sel, lc, what)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if link {
		if err := workspaceLinkPolicyReport(ctx, out, deps, ref); err != nil {
			return err
		}
		if err := deps.Authority.Link(ctx, ref.target); err != nil {
			return err
		}
	} else if err := deps.Authority.Unlink(ctx, ref.target); err != nil {
		return err
	}
	actor, err := workspaceLinkCaller(ctx, lc)
	if err != nil {
		return err
	}
	id, event, err := workspaceLinkEvent("project."+verb, ref, actor, deps.now())
	if err != nil {
		return err
	}
	if err := deps.Members.Audit(ctx, ref.target, id, event); err != nil {
		return err
	}
	workspaceLinkDone(out, deps, ref, link)
	return nil
}

// workspaceLinkPast is the word the refusal's first clause needs, so the
// sentence reads as one about the invocation that was attempted.
func workspaceLinkPast(link bool) string {
	if link {
		return "linked"
	}
	return "unlinked"
}

// workspaceLinkMember reads this machine's own config into the member the
// commands act on, and it is where both refusals live (the acceptance's
// "a config that is not the member's or whose pin names another workspace").
//
// The pin is what names the workspace. The membership key and the pin are one
// statement (localcfg.validateWorkspace): a config that joins a workspace
// carries the four pin values, and the pin is the member's own record of which
// workspace it trusts — so a config that is not the member's own is exactly one
// that pins nothing, and the pin's Firebase project is the workspace's own
// project, which a workspace is named after (D1290, A1). Reading the key alone
// would be enough to refuse the first case and is not enough for the other two:
// a project named after the workspace's own Firebase project IS the workspace's
// own project, and a pin whose Firebase project is another name is a pin for
// another workspace.
func workspaceLinkMember(sel localcfg.Selection, lc *localcfg.Config, what string) (workspaceLinkMemberRef, error) {
	var ref workspaceLinkMemberRef
	if sel.Path == "" {
		return ref, userErr("no local project config file is selected: %s reads the MEMBER's own config, which is where the membership and the pin are written, and this machine has none for %s — the shared config carries neither (spec §2). Run `fugaro setup` on this machine first, or pass --config", what, oneLine(sel.Name))
	}
	if lc == nil {
		return ref, userErr("no project config was read: %s needs the selected project's own config", what)
	}
	pin := lc.WorkspacePin
	if lc.Workspace == "" || pin.IsZero() {
		return ref, userErr("this machine's config for %s is not a member of any workspace: %s completes a membership that the member's OWN config states — the `workspace:` key and, beside it, the pin (the workspace's Firebase project, the issuer, the key it verifies, and the account that acts for it there), which `fugaro workspace add <workspace> …` writes (spec §2) — and this config states none. Nothing was done", lc.Name, what)
	}
	if lc.Name == pin.FirebaseProject {
		return ref, userErr("this config is the workspace's own project and not a member's: %s is the workspace's own project, named after the workspace %s (D1290), and a workspace is not a member of itself. %s belongs on the machine that holds the MEMBER's own config; this one writes the workspace's member table instead (fugaro workspace project add <project>)", lc.Name, lc.Workspace, what)
	}
	if pin.FirebaseProject != lc.Workspace {
		return ref, userErr("the pin names another workspace: %s's config states the membership %s, and its pin names %s — a workspace's own project is named after the workspace (D1290), so a pin whose firebase_project is %s is a pin for another workspace, and one of the two is wrong. Nothing was done: re-pin this project against the workspace its membership names with `fugaro workspace add %s …`", lc.Name, lc.Workspace, pin.FirebaseProject, pin.FirebaseProject, lc.Workspace)
	}
	target := workspace.MemberTarget{Project: lc.GCPProject}
	if lc.Budget != nil {
		target.Firebase = lc.Budget.FirebaseProject
	}
	if target.Firebase == target.Project {
		target.Firebase = ""
	}
	if target.Project == "" {
		return ref, userErr("%s's config names no gcp_project, so there is no project for the workspace to hold: run `fugaro setup` to write it", lc.Name)
	}
	return workspaceLinkMemberRef{name: lc.Name, workspace: lc.Workspace, target: target}, nil
}

// workspaceLinkPolicyReport prints the policy difference the move makes, before
// it is made (the acceptance's folder-model clause; threat-model.md §1 and §7
// item 10). It reads the old side itself — the parent each project hangs under
// now and the owners that parent carries — and takes the new side from C2's
// Authority.AdminBindings, the one reader of the live binding (D2823).
//
// What it does not read is said rather than left to look like nothing:
// organization policies have no reader in this program — a key-creation ban, a
// domain restriction and an allowed-locations constraint are the org policy's
// own, and the threat model names this as a residual risk rather than a gap
// this report closes. A failed read of the old side refuses the link instead of
// moving the project with the difference unreported, because the report is what
// the member owner consents on.
func workspaceLinkPolicyReport(ctx context.Context, out io.Writer, deps workspaceLinkDeps, ref workspaceLinkMemberRef) error {
	projects := strings.Join(ref.projects(), ", ")
	switch kind := deps.Authority.Kind(); kind {
	case workspace.AuthoritySA:
		fmt.Fprintf(out, "%s does not move: the workspace's authority is its service account's owner grant on %s where it stands (D2803), so what these projects inherit is unchanged.\n", projects, projects)
		return nil
	case workspace.AuthorityFolder:
	default:
		fmt.Fprintf(out, "this workspace's mechanism is not recorded on this machine (%q), so which policies %s inherit after the change cannot be reported here; `fugaro workspace init` records it.\n", string(kind), projects)
		return nil
	}
	if deps.Folders == nil {
		fmt.Fprintf(out, "moving %s into the workspace's folder changes the IAM and organization policies they inherit — what their old parents carried goes, and the workspace folder's takes its place — and this machine has no Resource Manager read to name either side with.\n", projects)
		return nil
	}
	admins, err := deps.Authority.AdminBindings(ctx)
	if err != nil {
		return userErr("nothing was moved: moving %s into the workspace's folder changes the policies they inherit, and the workspace's own bindings could not be read to report that change (%v). Run it again where that read succeeds", projects, err)
	}
	fmt.Fprintf(out, "moving %s into the workspace's folder changes the policies they inherit:\n", projects)
	for _, p := range ref.projects() {
		parent, err := deps.Folders.Parent(ctx, p)
		if err != nil {
			return userErr("nothing was moved: moving %s into the workspace's folder changes the policies it inherits, and the parent %s hangs under now could not be read (%v). Run it again where that read succeeds", p, p, err)
		}
		if parent == "" {
			fmt.Fprintf(out, "  now: %s hangs under no parent, so it inherits this organization's policies and no folder's\n", p)
			continue
		}
		owners, err := deps.Folders.Owners(ctx, parent)
		if err != nil {
			return userErr("nothing was moved: moving %s into the workspace's folder changes the policies it inherits, and the owners %s carries could not be read (%v). Run it again where that read succeeds", p, parent, err)
		}
		fmt.Fprintf(out, "  now: %s hangs under %s, which carries %s\n", p, oneLine(parent), workspaceLinkOwners(owners))
	}
	fmt.Fprintf(out, "  after: they sit in the workspace's folder and carry its bindings: the workspace's admins hold an owner's authority over them (D2804), and the live binding names %s\n", workspaceLinkOwners(admins))
	fmt.Fprintf(out, "  not read here: this machine has no reader for the organization policies either side applies — a key-creation ban, a domain restriction, an allowed-locations constraint — so they are named by neither side above, and linking cannot undo an inheritance it has changed (threat-model.md §7 item 10)\n")
	return nil
}

// workspaceLinkOwners is one side's carried principals as a sentence: an empty
// list is said out loud, because "the old parent carried nobody" and "nothing
// was read" are different statements and this report is what the member owner
// consents on.
func workspaceLinkOwners(owners []string) string {
	if len(owners) == 0 {
		return "no owner this machine can read"
	}
	quoted := make([]string, 0, len(owners))
	for _, o := range owners {
		quoted = append(quoted, oneLine(o))
	}
	return "owners " + strings.Join(quoted, ", ")
}

// workspaceLinkCaller is the actor the entry names: the address this machine's
// credentials belong to, read from the member's own selected config, which is
// who runs the command (F21 -- never the config's `user:` key and never git's
// user.email, which are text anybody can set). It is a package variable for the
// reason the family's other credential reads are: no test reaches the network
// it asks, and `workspace project add` records its caller the same way
// (workspaceProjectAdd.openCaller).
func workspaceLinkCaller(ctx context.Context, lc *localcfg.Config) (string, error) {
	caller, err := authenticatedUser(ctx, lc)
	if err != nil {
		return "", userErr("this command records who made the change from the credential it is using, and it could not read who that credential belongs to: %v. The caller is the credential and never the local config's own key or git config user.email: sign in with gcloud auth application-default login and run this again", err)
	}
	address, err := workspace.CanonicalAddress(caller)
	if err != nil {
		return "", userErr("the credential's own address %q: %v", safetext.Field(caller), err)
	}
	return address, nil
}

// workspaceLinkEvent is the entry the command files in the MEMBER's own trail
// (D2811): C1's own event, which is the shape the trail holds and the shape its
// readers read it back with (`status`, the drift check: workspaceMemberTrail).
// The contract puts the member's entry and the workspace's own in one shape
// (contract.md, "The audit event"), so an entry written as anything else is one
// no reader of this program can answer a question from.
//
// The id is minted (NewAuditEventID) rather than spelled from the act: the
// create is the trail's own precondition, so a repeat is a refusal and never a
// replacement, and two links in one second must be two entries.
func workspaceLinkEvent(act string, ref workspaceLinkMemberRef, actor string, now time.Time) (string, []byte, error) {
	moment := now.UTC()
	id, err := workspace.NewAuditEventID(moment, crand.Reader)
	if err != nil {
		return "", nil, userErr("minting the id of this %s's audit entry: %v", act, err)
	}
	event, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    moment,
		Actor:   actor,
		Action:  workspace.Action(act),
		Project: ref.target.Project,
	})
	if err != nil {
		return "", nil, userErr("the audit entry for %s could not be built: %v", ref.target.Project, err)
	}
	return id, event, nil
}

// workspaceLinkDone is the closing line: what stands now, and the one thing the
// other machine still has to do. Link names the workspace owner's `project add`
// — the mirror and the stored grants are written there, because a member owner
// cannot read the workspace's roster — and unlink names D2822's line, that the
// grants stay until that owner's `project rm`.
func workspaceLinkDone(out io.Writer, deps workspaceLinkDeps, ref workspaceLinkMemberRef, link bool) {
	held := "the project sits in the workspace's folder"
	if deps.Authority.Kind() == workspace.AuthoritySA {
		held = "the workspace service account's owner grant is on it"
	}
	if link {
		fmt.Fprintf(out, "%s now holds %s: %s, and the member owner's own audit entry is filed.\n", ref.workspace, ref.target.Project, held)
		fmt.Fprintf(out, "the workspace's owner runs `fugaro workspace project add %s` on the machine holding the workspace's own config: that is what writes this member's mirror and the stored grant of each workspace admin (D2818, D2823). A member owner cannot read the workspace's roster, so nothing here writes them.\n", ref.name)
		return
	}
	fmt.Fprintf(out, "%s no longer holds %s: the authority is gone, and only the authority (D2822).\n", ref.workspace, ref.target.Project)
	fmt.Fprintf(out, "the workspace admins' stored grants stay in this member's own roster — they are rows in a document this project's own update applies — and they go when the workspace project's owner runs `fugaro workspace project rm %s` there, which removes them and takes the mirror away.\n", ref.name)
}
