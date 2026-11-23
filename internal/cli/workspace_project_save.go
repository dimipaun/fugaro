package cli

// workspace_project_save.go is WU23: `fugaro workspace project save`.
//
// A member's state -- its settings and its spend summary -- and the workspace's
// own audit export are snapshots of things a person owns, and until D2824 the
// only moment they were written was `project rm`, which is to say the moment a
// member leaves. That made the spec's "the project's state repo is in sync" row
// unanswerable for a member still in the workspace (spec.md §1) and left the
// audit trail saved nowhere at all. This command is the saver: one member, every
// member at once, or the workspace's audit export, through C2's StateWriter and
// nothing else. It is the only saver besides `project rm` (D2824), and the two
// call the same writer, so a snapshot taken here and one taken there are the
// same snapshot.
//
// Four properties shape it, and each is why a line below is where it is.
//
//   - It writes through StateWriter and reaches nothing else. The writer owns
//     what a snapshot is, where it goes -- always a local file it names, and,
//     when E6's designated repository is set, a pull request from the owner's
//     own credentials (WU15, D2807) -- and the refusal of a public repository
//     with its `--repo` override (D2824). None of that is repeated here: a
//     second reading of the visibility rule in this file would be a second
//     answer to where a state snapshot may go.
//   - It reads the workspace's roster first (C1's Store). A save is filed
//     under a workspace whose roster has been written -- the audit export is
//     that workspace's own trail, and the member list it covers is the list the
//     workspace was made with -- so a node that is absent, or one this build
//     does not read as a roster, is refused before any member is touched rather
//     than after some of them have been saved. The read is the command's only
//     reach on the roster: a save writes no node of the workspace's (D2824's
//     row is answered by the writer's own time, not by a mark).
//   - It refuses in a coding agent's session (A7, D1555), before anything is
//     read: a snapshot carries the member's addresses, another project's spend
//     and the trail of who did what, and it is filed with the owner's own
//     credentials.
//   - It asks NOTHING. There is no confirmation, no report of what will be
//     saved and no question about a member the table does not hold: the command
//     saves what it was asked for, or refuses with the sentence that names the
//     fix.
//
// The member list is the owner's own member table (D2821), which is the table
// in the config of the project the workspace is NAMED AFTER -- the same file
// `workspace project add` writes and the same document the record is published
// from -- and each member's Firebase project is read beside its name, because a
// member's two projects travel together on every call that reaches one (F2,
// R10). An entry is named by the project name the table keys it by; a save by
// the member's own name, in the table's order, so two runs over one workspace
// save the same members in the same order.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// projectSaveAuditFlag is the flag that saves the workspace's audit export
// instead of a member's state (D2824). It is a flag of its own rather than a
// member that does not exist because the two are snapshots of two different
// projects: a member's settings and spend, and the workspace's own trail.
const projectSaveAuditFlag = "--audit"

// workspaceProjectSaveDeps is what `workspace project save` runs against: the
// owner's own config, C1's Store and C2's StateWriter. I1 adapts the concrete
// types onto them; a test declares its own fakes.
type workspaceProjectSaveDeps struct {
	// Owner is the config of the project the workspace is named after: the
	// member table D2821 says lives on the workspace owner's machine, and the
	// name a snapshot is filed under. A config that is not a workspace's own is
	// refused before anything is read.
	Owner *localcfg.Config
	// Store reads the workspace's roster (C1). It is read once, before the
	// first save, so a workspace that was never initialized saves nothing.
	Store workspace.Store
	// Writer is C2's StateWriter: the only thing a save writes through, and
	// the only saver besides `project rm` (D2824).
	Writer workspace.StateWriter
}

// workspaceProjectSaveOptions are this command's flags.
type workspaceProjectSaveOptions struct {
	audit bool
}

func newWorkspaceProjectSaveCmd(deps workspaceProjectSaveDeps) *cobra.Command {
	var o workspaceProjectSaveOptions
	cmd := &cobra.Command{
		Use:   "save [<project>]",
		Short: "Save a member's state, or the workspace's audit export",
		Long: `Save a state snapshot: one member's settings and spend summary, every
member's with no project named, or, with ` + projectSaveAuditFlag + `, the workspace's own audit
export.

A snapshot is written to a local file the command names, always, and to the
designated state repository as a pull request when the workspace's own project
has one: the repository and where its setting came from are printed before it is
pushed. The writer refuses a public repository, because a snapshot carries the
member's addresses, and names the --repo override that designates another one.

The members are the ones the member table of this workspace's own project
declares -- the file fugaro workspace project add writes -- and each is saved
under its own name. ` + projectSaveAuditFlag + ` saves the workspace's audit trail, which is
not any member's state, so it takes no project.

Run it in your own terminal, with your own credentials: it refuses a coding
agent's session, and it asks nothing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceProjectSave(cmd, deps, args, o)
		},
	}
	// The flag's name is read off projectSaveAuditFlag rather than written
	// twice: the refusal above names the flag a person types, and the two
	// spellings cannot come to disagree.
	cmd.Flags().BoolVar(&o.audit, strings.TrimPrefix(projectSaveAuditFlag, "--"), false,
		"save the workspace's audit export instead of a member's state")
	return cmd
}

func runWorkspaceProjectSave(cmd *cobra.Command, deps workspaceProjectSaveDeps, args []string, o workspaceProjectSaveOptions) error {
	// First, before anything is read: GODEBUG=http2debug makes Go print HTTP/2
	// requests with their headers — the OAuth bearer token of the owner's own
	// ADC, which is what the roster read and the writer's push both
	// authenticate with — to stderr, and GODEBUG is read before main runs, so
	// it cannot be unset in time. This command resolves the workspace's own
	// project and its own database rather than passing through openCloud or
	// openBudgetDB, which is where the project scopes get the refusal, so it
	// has to make it here (refuseHTTP2Debug, the rule every cloud command of
	// this repository follows).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was saved: a state snapshot carries a member's own settings, its spend and the addresses the workspace reaches it by, and it is filed with your own credentials, so a save is your own step at your own terminal (D2824): %s", initflow.AgentRefusal(m))
	}
	name, members, err := workspaceSaveTargets(deps.Owner)
	if err != nil {
		return err
	}
	if o.audit && len(args) == 1 {
		return userErr("%s saves workspace %s's own audit export -- the trail of who did what to the workspace -- and not any member's state, so it takes no project: fugaro workspace project save %q saves that member's state, and %s with no project saves the export (D2824)",
			projectSaveAuditFlag, name, args[0], projectSaveAuditFlag)
	}
	ctx := cmd.Context()
	if err := workspaceSaveRosterRead(ctx, deps, name); err != nil {
		return err
	}
	if o.audit {
		written, err := deps.Writer.Save(ctx, workspace.Snapshot{Workspace: name, Audit: true})
		if err != nil {
			return userErr("workspace %s's audit export was not saved: %v", name, err)
		}
		workspaceSavePrint(cmd, "workspace "+name+"'s audit export", written)
		return nil
	}
	targets := members
	if len(args) == 1 {
		target, ok := workspaceSaveMember(members, args[0])
		if !ok {
			return userErr("workspace %s's member table holds no project named %q, so nothing was saved: the members it holds are %s; a project is added to the workspace by fugaro workspace project add (D2821)",
				name, args[0], workspaceSaveNames(members))
		}
		targets = []workspace.MemberTarget{target}
	}
	if len(targets) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "workspace %s declares no members, so there is no state to save: fugaro workspace project add declares one\n", name)
		return nil
	}
	for _, m := range targets {
		written, err := deps.Writer.Save(ctx, workspace.Snapshot{Workspace: name, Member: m})
		if err != nil {
			return userErr("member %s's state was not saved: %v", oneLine(m.Project), err)
		}
		workspaceSavePrint(cmd, "member "+oneLine(m.Project), written)
	}
	if len(targets) > 1 {
		fmt.Fprintf(cmd.OutOrStdout(), "saved %d members' state of workspace %s\n", len(targets), name)
	}
	return nil
}

// workspaceSaveTargets reads the workspace's name and its member table from the
// owner's own config: the name is the config's own, which is what makes this
// read and `workspace project add`'s write agree about which workspace a member
// belongs to, and the table is D2821's, on the machine that holds it.
//
// A config that declares none of the owner's half is not a workspace's own and
// is refused: a save that read one as an empty workspace would answer "nothing
// to save" to the owner of a workspace whose file simply is not the one they
// meant. Which projects are a workspace's own is `workspace show`'s own
// predicate -- its table, its cap or its shared list written -- and not a
// second reading of the same question.
func workspaceSaveTargets(lc *localcfg.Config) (string, []workspace.MemberTarget, error) {
	if lc == nil || lc.Name == "" {
		return "", nil, userErr("no project is selected, so there is no workspace to save: the members of a workspace are declared in the config of the project the workspace is named after, and fugaro workspace project save is run from it")
	}
	if lc.WorkspaceMembers == nil && lc.WorkspaceCap == nil && lc.WorkspaceShared == nil {
		return "", nil, userErr("project %s declares no member table, no cap and no shared credential, so it is not a workspace's own project: the members of a workspace are declared in the config of the project the workspace is named after, by fugaro workspace project add (spec §5); a member's own machine holds that file's other half instead", lc.Name)
	}
	table := workspaceTable(lc)
	members := make([]workspace.MemberTarget, 0, len(table))
	for _, member := range slices.Sorted(maps.Keys(table)) {
		members = append(members, workspace.MemberTarget{
			// The table is keyed by the member's Fugaro project name -- the
			// name the published record's members[].project carries and the
			// workspace's credentials node is read by (spec.md §5) -- and that
			// is the name a snapshot is filed under, which is the name
			// Snapshot.Member's Project holds for a save. It is NOT the
			// member's GCP project: the owner's machine holds none (the table
			// declares the member's Firebase project and its fp, which is this
			// same name), and no snapshot reaches a member's GCP project.
			Project: member,
			// The member's Firebase project travels beside it on every call
			// that reaches the member (F2, R10), and it is the project the
			// member's own settings and spend summary are read from; it is the
			// empty name for a member whose Firebase project is its GCP
			// project, which is the declaration the table itself makes.
			Firebase: table[member].FirebaseProject,
		})
	}
	return lc.Name, members, nil
}

// workspaceSaveRosterRead holds the workspace to having been initialized: the
// roster is what `workspace init` writes first (WU24), and a save of a
// workspace whose node is absent -- or whose node is not a roster this build
// reads -- is refused here, before the first snapshot, rather than after some
// members have been saved.
func workspaceSaveRosterRead(ctx context.Context, deps workspaceProjectSaveDeps, name string) error {
	rev, raw, err := deps.Store.Read(ctx)
	if err != nil {
		return userErr("workspace %s's roster could not be read, so nothing was saved: %v", name, err)
	}
	if !rev.Found {
		return userErr("workspace %s has no roster, so nothing was saved: a workspace's first roster is written by fugaro workspace init, and the audit export and the member list a save covers are that workspace's own", name)
	}
	if _, err := workspace.DecodeRoster(raw); err != nil {
		return userErr("workspace %s's node is not a roster this build reads, so nothing was saved: %v", name, err)
	}
	return nil
}

// workspaceSaveMember finds one member of the table, by the project name the
// table keys it by.
func workspaceSaveMember(members []workspace.MemberTarget, name string) (workspace.MemberTarget, bool) {
	for _, m := range members {
		if m.Project == name {
			return m, true
		}
	}
	return workspace.MemberTarget{}, false
}

// workspaceSaveNames lists the members a refusal names, as one line.
func workspaceSaveNames(members []workspace.MemberTarget) string {
	if len(members) == 0 {
		return "none"
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, oneLine(m.Project))
	}
	return strings.Join(names, ", ")
}

// workspaceSavePrint says where one snapshot was left: the local file the
// writer always writes, and the pull request where it opened one.
func workspaceSavePrint(cmd *cobra.Command, what string, written workspace.SnapshotWritten) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "saved %s to %s\n", what, oneLine(written.File))
	if written.PullRequest != "" {
		fmt.Fprintf(out, "opened %s\n", oneLine(written.PullRequest))
	}
}
