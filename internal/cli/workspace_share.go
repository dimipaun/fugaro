package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// This file is the owner's shared list: the credentials the workspace's own
// credentials service may offer its members, written in the config of the
// project the workspace is NAMED AFTER (spec §5, D1298).
//
// It is the owner's declaration and never a member's, and it is local-only:
// a member reads nothing the workspace publishes, so the only copy of this list
// is the file these two commands write, and the record published from it is the
// owner's own `fugaro update`. What it grants is bounded from below and not
// only from here: the name this list holds is permission to serve, and a
// member's runs are served it only where the member's own policy admits it too
// (D1298's intersection).
//
// The kind of an entry is derived and never typed by a person: the credential's
// name is the bundle's own key, and credsvc.CredentialKey is the one place that
// spelling is turned into what the credential is for. That is what makes
// `--covers` required for a forge entry and refused for a model one without a
// --kind flag anyone could set inconsistently with the name beside it.

// workspaceSharedKinds maps a bundle credential's kind to the route record's
// own spelling (R4), and it is the whole of what may be shared: the three kinds
// a member's runs can be served. Every other key the bundle holds — the provider
// key, the review-signing keys, the Bitbucket read consumer, the read-only
// GitHub App key pair — is refused rather than offered, because a member's run
// has no route that reaches one.
func workspaceSharedKind(key string) (localcfg.WorkspaceShared, string, bool) {
	name, kind, _, ok := credsvc.CredentialKey(key)
	if !ok {
		return localcfg.WorkspaceShared{}, name, false
	}
	switch kind {
	case credsvc.KindModel:
		return localcfg.WorkspaceShared{Kind: localcfg.WorkspaceSharedModel}, name, true
	case credsvc.KindGit:
		return localcfg.WorkspaceShared{Kind: localcfg.WorkspaceSharedBitbucket}, name, true
	case credsvc.KindGitHubApp:
		return localcfg.WorkspaceShared{Kind: localcfg.WorkspaceSharedGitHub}, name, true
	}
	return localcfg.WorkspaceShared{}, name, false
}

type workspaceShareOptions struct {
	workspace string
	projects  []string
	covers    []string
	yes       bool
	// noApply is --no-apply (D1565), as workspaceMemberAddOptions'.
	noApply *bool
}

func newWorkspaceShareCmd() *cobra.Command {
	var o workspaceShareOptions
	cmd := &cobra.Command{
		Use:   "share <credential> --workspace <name> --projects a,b",
		Short: "Offer one of the workspace's credentials to some of its members",
		Long: `Write one entry into the workspace owner's own shared list.

The credential is named as the workspace's bundle holds it — a model credential
by its own name, the Bitbucket token as bitbucket-token, the GitHub App's key as
github-app-key — and what it is for is derived from that name, so there is no
kind to state and none to get wrong.

--projects names the members this credential may be served to, as a comma
separated list or repeated, and each must be a member the workspace's own table
declares: a shared entry naming a project the table does not is an entry the
workspace's own fugaro update refuses to publish. --covers names the
repositories a forge credential reaches, as owner/name, and it is required for
bitbucket and github and refused for a model credential, which a call names no
repository for.

An entry the list already holds is replaced whole, as a member entry is: the
projects and the coverage it ends up with are the ones given, so re-sharing is
how a project or a repository leaves the entry. Use fugaro workspace unshare to
drop it altogether.

Only the keys it writes are rewritten: every comment and every other key of the
file is kept. The write is local; the owner's fugaro update publishes the list
in the record the workspace's service reads.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceShare(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.workspace, "workspace", "", "the workspace whose list this is (required: an entry belongs to one workspace)")
	f.StringSliceVar(&o.projects, "projects", nil, "the member projects this credential may be served to, comma separated or repeated")
	f.StringArrayVar(&o.covers, "covers", nil, "a repository a forge credential reaches, as owner/name (repeatable; required for bitbucket and github)")
	o.noApply = addNoApplyFlag(cmd)
	return cmd
}

func runWorkspaceShare(cmd *cobra.Command, credential string, o *workspaceShareOptions) error {
	if o.workspace == "" {
		return userErr("--workspace is required: a shared entry belongs to one workspace and its own project config is where it is written")
	}
	if len(o.projects) == 0 {
		return userErr("--projects is required: a shared entry states the members it may be served to, and an entry that named none would be one the workspace's own update refuses to publish")
	}
	entry, name, ok := workspaceSharedKind(credential)
	if !ok {
		return userErr("%s is not a credential the workspace can offer its members: a member's runs are served a model credential (a Claude account's name, anthropic-api-key, a key credential's name) and the two forge credentials (bitbucket-token, github-app-key), and this name is none of them (fugaro creds list shows the workspace's own bundle)", credential)
	}
	if entry.Kind == localcfg.WorkspaceSharedModel {
		if len(o.covers) > 0 {
			return userErr("--covers %s cannot be given for %s, which is a model credential: a model call names no repository, so coverage belongs to a forge entry (bitbucket-token or github-app-key)", strings.Join(o.covers, ", "), credential)
		}
	} else if len(o.covers) == 0 {
		return userErr("--covers is required for %s: a %s route is decided per repository, so the entry names the repositories the workspace owner's credential reaches (repeat --covers owner/name)", credential, entry.Kind)
	}
	lc, path, err := workspaceOwnerConfig(o.workspace)
	if err != nil {
		return err
	}
	// Every project is held to the workspace's own member table first: the
	// published record is built from that table, so an entry naming a project it
	// does not declare is one the owner's next update refuses — refused here,
	// where the person can see which name is wrong and what the table holds.
	table := workspaceTable(lc)
	for _, p := range o.projects {
		if !config.ProjectNameRE.MatchString(p) {
			return userErr("--projects %s: %q is not a project name", strings.Join(o.projects, ","), p)
		}
		if _, ok := table[p]; !ok {
			return userErr("workspace %s declares no member %s, so %s may not be served to it: the shared list may only name projects the member table declares (fugaro workspace show %s lists them; add the member with fugaro workspace member add %s %s)",
				o.workspace, p, credential, o.workspace, o.workspace, p)
		}
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: path})
	if err != nil {
		return err
	}
	// An entry the list already holds is replaced WHOLE and not added to, as a
	// member entry is, and for the same reason: AddListItem appends, so a second
	// declaration would leave every project and every repository the first one
	// named standing, and the entry would end up wider than the sentence the
	// person just read.
	_, declared := workspaceShared(lc)[name]
	next := old
	if declared {
		if next, err = workspaceRemoveKey(path, next, []string{"workspace_shared", name}); err != nil {
			return err
		}
	}
	// The lists first, then the kind: AddListItem creates the sequence and the
	// mapping chain it hangs under, and the scalar then finds the entry already
	// there rather than creating a second one.
	for _, p := range o.projects {
		if next, err = workspaceAddItem(path, next, []string{"workspace_shared", name, "projects"}, p); err != nil {
			return err
		}
	}
	for _, repo := range o.covers {
		if next, err = workspaceAddItem(path, next, []string{"workspace_shared", name, "covers"}, repo); err != nil {
			return err
		}
	}
	if next, err = workspaceSetScalar(path, next, []string{"workspace_shared", name, "kind"}, entry.Kind); err != nil {
		return err
	}

	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    workspaceShareWhat(o, credential, entry.Kind, declared),
		Command: workspaceShareCommand(o, credential),
		Yes:     o.yes,
	}
	// Sharing is the widening direction D846 reserves for the owner's own step:
	// it lets another project spend a credential of the workspace's, and the
	// entry is written whole, so the same declaration that adds a project or a
	// repository drops the ones it does not name. The first entry for a
	// credential widens nothing anyone was admitted to — there was none — and
	// takes the ordinary write, as a first member declaration does.
	if declared {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("this replaces the entry workspace %s shares for %s: the entry is written whole, so a project or a repository it carries today and this declaration does not name is gone from the list",
			o.workspace, credential)
	}
	if err := workspaceEditLocal(cmd, lc, path, edit); err != nil {
		return err
	}
	return workspaceChain(cmd, lc, path, old, next, workspaceDoor("share"), workspaceOwnerHalf(), *o.noApply)
}

// workspaceShareWhat is the sentence the write prints for one shared entry: the
// credential, what it is for, the members it may be served to and the
// repositories it reaches, which is the whole of what the entry says afterwards.
func workspaceShareWhat(o *workspaceShareOptions, credential, kind string, replace bool) string {
	verb := "offer"
	if replace {
		verb = "replace what workspace " + o.workspace + " offers as"
	}
	what := fmt.Sprintf("%s %s (%s) in workspace %s to %s", verb, credential, kind, o.workspace, strings.Join(o.projects, ", "))
	if len(o.covers) > 0 {
		what += ", for the repositories " + strings.Join(o.covers, ", ")
	}
	if replace {
		what += "; anything the entry holds that is not named here is dropped"
	}
	return what
}

func workspaceShareCommand(o *workspaceShareOptions, credential string) string {
	cmd := fmt.Sprintf("fugaro workspace share %s --workspace %s", credential, o.workspace)
	for _, p := range o.projects {
		cmd += " --projects " + p
	}
	for _, repo := range o.covers {
		cmd += " --covers " + repo
	}
	return cmd
}

type workspaceUnshareOptions struct {
	workspace string
	yes       bool
	// noApply is --no-apply (D1565), as workspaceMemberAddOptions'.
	noApply *bool
}

func newWorkspaceUnshareCmd() *cobra.Command {
	var o workspaceUnshareOptions
	cmd := &cobra.Command{
		Use:   "unshare <credential> --workspace <name>",
		Short: "Stop offering one of the workspace's credentials to its members",
		Long: `Remove one entry from the workspace owner's own shared list.

The workspace's service stops offering the credential to every member, and a
member's run that was served it from here is refused at the front door until the
entry is written again. The member's own policy is not this command's to change,
and neither is its config.

Only the entry it removes is rewritten: every comment and every other key of the
file is kept. The write is local; the owner's fugaro update publishes the list
without it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceUnshare(cmd, args[0], &o) },
	}
	cmd.Flags().StringVar(&o.workspace, "workspace", "", "the workspace whose list this is (required: an entry belongs to one workspace)")
	o.noApply = addNoApplyFlag(cmd)
	return cmd
}

func runWorkspaceUnshare(cmd *cobra.Command, credential string, o *workspaceUnshareOptions) error {
	if o.workspace == "" {
		return userErr("--workspace is required: a shared entry belongs to one workspace and its own project config is where it is written")
	}
	lc, path, err := workspaceOwnerConfig(o.workspace)
	if err != nil {
		return err
	}
	_, name, ok := workspaceSharedKind(credential)
	if !ok {
		// A name the bundle does not spell is still a name this list may hold:
		// it is what an older release wrote, or what a person typed into the
		// file, and a removal of something that is not there is reported as
		// such below rather than refused here.
		name = credential
	}
	shared := workspaceShared(lc)
	if _, ok := shared[name]; !ok {
		return userErr("workspace %s shares no credential %s: there is no entry to remove (fugaro workspace show %s lists what it offers)", o.workspace, name, o.workspace)
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: path})
	if err != nil {
		return err
	}
	next, err := workspaceRemoveKey(path, old, []string{"workspace_shared", name})
	if err != nil {
		return err
	}
	// No Typed: a removal takes an offering away, and D846 reserves the
	// project's own name for the writes that widen.
	if err := workspaceEditLocal(cmd, lc, path, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("stop offering %s in workspace %s: drop its entry from the shared list, so the workspace's service stops serving it", name, o.workspace),
		Command: fmt.Sprintf("fugaro workspace unshare %s --workspace %s", name, o.workspace),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	return workspaceChain(cmd, lc, path, old, next, workspaceDoor("unshare"), workspaceOwnerHalf(), *o.noApply)
}
