package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the `fugaro workspace` family (D1303, spec.md §6): the reads,
// which are offline and read this machine's own files, and the writes, which
// are local config edits through the one write path the models and settings
// commands share (applySettingsEdit — the diff, the confirmation, the
// coding-agent fence, the backup and the atomic write).
//
// A membership has two halves and they live in two people's files, so this
// family writes each half where it belongs:
//
//   - `workspace add` and `workspace rm` edit the SELECTED project's config: the
//     membership key and, beside it, the pin — the one place a member decides
//     what it trusts (spec §2).
//   - `workspace member add`, `member rm` and `cap set` edit the config of the
//     project the workspace is NAMED AFTER, which is the workspace's own
//     project: its config holds the owner's member table and the workspace's cap
//     (spec §5, D1300), because the owner is the side that declares how the
//     workspace reaches a member.
//
// Neither half is ever taken from the other, and no command here reaches the
// cloud: the record the owner publishes is W2's `fugaro update` stage, so every
// write ends by printing `next: fugaro update`.

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Join this project to a workspace, or declare one",
		Long: `A workspace is a grouping of Fugaro projects. It holds a model spend its own
credentials pay for, and it admits its members' calls to them.

A membership has two halves, written on two machines. A member's own config
carries the membership key and its pin: the workspace's Firebase project, the
issuer its exchange requires, the key it verifies a member-bound assertion
against, and the workspace service account its own fugaro update binds its grant
to. The workspace owner's config carries the other half: the member table, which
is how the workspace reaches each member. This family writes the half that
belongs to the machine it runs on.

The reads are offline: ls and show read this machine's own project configs, no
bucket and no cloud node, so they work anywhere, a coding agent's session
included. The writes are local config edits — the diff, the backup and the
atomic write the fugaro models commands make — and each ends by naming the one
command that converges the installation: fugaro update.`,
	}
	cmd.AddCommand(
		newWorkspaceLsCmd(),
		newWorkspaceShowCmd(),
		newWorkspaceLoginCmd(),
		newWorkspaceAddCmd(),
		newWorkspaceRmCmd(),
		newWorkspaceMemberCmd(),
		newWorkspaceCapCmd(),
	)
	return cmd
}

// workspaceRefuseName refuses a name that is not a workspace or project name.
// Both use config.ProjectNameRE and not a second grammar (F21), so a workspace
// is spelled exactly as a project is.
func workspaceRefuseName(name, what string) error {
	if config.ProjectNameRE.MatchString(name) {
		return nil
	}
	return userErr("%q is not a %s name: it uses the project-name grammar (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit), because a workspace is named the way a project is (F21)", name, what)
}

// workspaceOwnerConfig is the config of the project a workspace is named
// after, and the file it lives in: where the owner's half is written (spec §5).
// A member's machine holds no config for it — a member holds no grant on the
// workspace's project at all — so the absence is a refusal that says whose file
// this is, and not an empty edit.
func workspaceOwnerConfig(name string) (*localcfg.Config, string, error) {
	if err := workspaceRefuseName(name, "workspace"); err != nil {
		return nil, "", err
	}
	lc, path, err := localcfg.LoadProject(os.Getenv, name)
	if errors.Is(err, localcfg.ErrMissing) {
		return nil, "", userErr("this machine holds no config for workspace %s's own project: the member table and the cap are written in the config of the project the workspace is named after, so it has to exist here first (fugaro setup --name %s); a member's own machine holds neither, and writes neither", name, name)
	}
	if err != nil {
		return nil, "", userErr("%v", err)
	}
	return lc, path, nil
}

// workspaceEditLocal is one edit of a local project config, checked and
// written: modelsEditLocal without the price table, which is the models
// family's own read-back. What this family writes into a config — a membership,
// a pin, a member entry, a cap — is no price and no route, so the check is the
// config's own strict loader and nothing else.
func workspaceEditLocal(cmd *cobra.Command, lc *localcfg.Config, path string, e settingsEdit) error {
	e.Path, e.Kind = path, settingsDocLocal
	if yamledit.Changed(e.Old, e.New) {
		if err := settingsReparse(settingsTarget{kind: settingsDocLocal, path: path}, e.New); err != nil {
			return err
		}
	}
	return applySettingsEdit(cmd, lc, e)
}

// workspaceSetScalar, workspaceAddItem and workspaceRemoveKey are the three
// yamledit operations this family writes with. The editor is the only writer:
// nothing here re-marshals a config, which would drop the comments and the key
// order of everything it does not touch (D790).
func workspaceSetScalar(path string, src []byte, at []string, value string) ([]byte, error) {
	out, err := yamledit.SetScalar(src, at, value)
	return workspaceEditResult(path, out, err)
}

func workspaceAddItem(path string, src []byte, at []string, value string) ([]byte, error) {
	out, err := yamledit.AddListItem(src, at, value)
	return workspaceEditResult(path, out, err)
}

func workspaceRemoveKey(path string, src []byte, at []string) ([]byte, error) {
	// DropEmptyParents, so removing the last member takes the now-empty
	// workspace_members block with it and a member added and removed leaves the
	// file as it was.
	out, err := yamledit.RemoveKeyWithOptions(src, at, yamledit.RemoveOptions{DropEmptyParents: true})
	return workspaceEditResult(path, out, err)
}

// workspaceEditResult is one editor call's result in this family's words: the
// splice refusal the models writes print, which is the one place a file the
// editor cannot splice is explained (D790).
func workspaceEditResult(path string, out []byte, err error) ([]byte, error) {
	if err == nil {
		return out, nil
	}
	if refusal, ok := modelsSpliceRefusal(path, err); ok {
		return nil, refusal
	}
	return nil, userErr("editing %s: %v", path, err)
}

// workspaceHandoff prints the one command a landed write hands off to. It is
// printed only when the file changed: a write that changed nothing converges
// nothing, and `next: fugaro update` beside a `no change` would be a promise
// about a write that did not happen.
func workspaceHandoff(cmd *cobra.Command, old, next []byte) {
	if yamledit.Changed(old, next) {
		fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	}
}

type workspaceAddOptions struct {
	firebaseProject string
	issuer          string
	assertionKey    string
	serviceAccount  string
	yes             bool
}

func newWorkspaceAddCmd() *cobra.Command {
	var o workspaceAddOptions
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Join the selected project to a workspace, and pin what it trusts",
		Long: `Write the membership key and its pin into the selected project's own config.

The four pin values are this project's own. It pins the workspace's Firebase
project id, the issuer its exchange requires, the key it verifies a member-bound
assertion against, and the workspace service account its own fugaro update binds
its read-only database grant to. No code path reads one of them from the
workspace's record, which carries none of them: a record that could supply them
would aim this project's exchange and this project's database grant at a party
of its choosing.

The issuer must be exactly https://securetoken.google.com/<firebase-project>.
The signature is verified against Google's one published set of Firebase signing
keys, the same document for every project, so the aud/iss pair is the whole of
what tells one project's tokens from another's; a mistyped half is refused here
and refused again at the load.

Only the keys it writes are rewritten: every comment and every other key of the
file is kept. The write is local, and it reaches the installation through
fugaro update.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceAdd(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.firebaseProject, "firebase-project", "", "the workspace's Firebase project ID, which the token's aud must be")
	f.StringVar(&o.issuer, "issuer", "", "the issuer the exchange requires; must be https://securetoken.google.com/<firebase-project>")
	f.StringVar(&o.assertionKey, "assertion-key", "", "the key this project verifies a member-bound assertion against")
	f.StringVar(&o.serviceAccount, "service-account", "", "the workspace service account this project's grant binds to")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runWorkspaceAdd(cmd *cobra.Command, name string, o *workspaceAddOptions) error {
	if err := workspaceRefuseName(name, "workspace"); err != nil {
		return err
	}
	if err := workspacePinFlags(o); err != nil {
		return err
	}
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	if lc.Name == name {
		return userErr("the selected project IS workspace %s: a workspace's own project is the workspace and not a member of it, and its config carries the member table instead (fugaro workspace member add %s <project>)", name, name)
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	next := old
	for _, at := range [][]string{
		{"workspace"},
		{"workspace_pin", "firebase_project"},
		{"workspace_pin", "issuer"},
		{"workspace_pin", "assertion_key"},
		{"workspace_pin", "service_account"},
	} {
		if next, err = workspaceSetScalar(sel.Path, next, at, workspacePinValue(name, o, strings.Join(at, "."))); err != nil {
			return err
		}
	}

	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    workspaceAddWhat(lc.Name, name, o),
		Command: workspaceAddCommand(name, o),
		Yes:     o.yes,
	}
	// Re-pinning is the widening direction (D846): the membership the project
	// already states is replaced by another one, or by the same name with
	// different values, which is the owner's own step — asked nothing at their
	// terminal and refused in a coding agent's session (D1552). A first
	// membership widens nothing and is the ordinary write.
	if lc.Workspace != "" || !lc.WorkspacePin.IsZero() {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("this replaces what project %s trusts: the membership moves from %s to %s, or %s is re-pinned with different values",
			lc.Name, workspaceOrNone(lc.Workspace), name, name)
	}
	if err := workspaceEditLocal(cmd, lc, sel.Path, edit); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// workspaceOrNone is a config's membership key as a sentence names it.
func workspaceOrNone(name string) string {
	if name == "" {
		return "no workspace"
	}
	return name
}

// workspacePinValue is the value one of the five keys takes, by the joined path
// it is written at: the five are one statement, and this is the only place the
// mapping from a key to the flag that fills it lives.
func workspacePinValue(name string, o *workspaceAddOptions, at string) string {
	switch at {
	case "workspace":
		return name
	case "workspace_pin.firebase_project":
		return o.firebaseProject
	case "workspace_pin.issuer":
		return o.issuer
	case "workspace_pin.assertion_key":
		return o.assertionKey
	case "workspace_pin.service_account":
		return o.serviceAccount
	}
	return ""
}

// workspaceAddWhat is the sentence the confirmation shows, naming the
// membership this project is joining. The assertion key is a key, so the
// sentence says there is one and does not print it.
func workspaceAddWhat(project, name string, o *workspaceAddOptions) string {
	return fmt.Sprintf("join project %s to workspace %s: pin its Firebase project %s, its issuer %s, an assertion key, and the workspace service account %s",
		project, name, o.firebaseProject, o.issuer, o.serviceAccount)
}

// workspacePinFlags holds the four pin values to what `workspace add` writes:
// all four, and the issuer exactly the one its Firebase project mints. The
// loader refuses both again (localcfg.validateWorkspace), so a file written
// here and a file read there cannot disagree.
func workspacePinFlags(o *workspaceAddOptions) error {
	missing := []string{}
	for _, f := range []struct{ flag, value string }{
		{"--firebase-project", o.firebaseProject},
		{"--issuer", o.issuer},
		{"--assertion-key", o.assertionKey},
		{"--service-account", o.serviceAccount},
	} {
		if f.value == "" {
			missing = append(missing, f.flag)
		}
	}
	if len(missing) > 0 {
		return userErr("a membership is pinned by all four values, and %s %s not given: the pin is the workspace's Firebase project the token's aud must be, the issuer the exchange requires, the key a member-bound assertion is verified against, and the workspace service account the grant binds to",
			strings.Join(missing, ", "), wasWere(len(missing)))
	}
	if want := localcfg.WorkspaceIssuer(o.firebaseProject); o.issuer != want {
		return userErr("--issuer %s must be %s, the issuer of Firebase project %s: the signature is verified against Google's one published set of Firebase signing keys, the same document for every project, so the aud/iss pair is what tells one project's tokens from another's",
			o.issuer, want, o.firebaseProject)
	}
	return nil
}

// workspaceAddCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: the whole write, so
// nothing of what this command decided is left for them to reconstruct. The
// assertion key is the one value it does not spell, because it is a key.
func workspaceAddCommand(name string, o *workspaceAddOptions) string {
	return fmt.Sprintf("fugaro workspace add %s --firebase-project %s --issuer %s --assertion-key <key> --service-account %s",
		name, o.firebaseProject, o.issuer, o.serviceAccount)
}

type workspaceRmOptions struct {
	yes bool
}

func newWorkspaceRmCmd() *cobra.Command {
	var o workspaceRmOptions
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Leave a workspace: drop this project's membership key and its pin",
		Long: `Remove the membership key and the pin from the selected project's own config.

The project stops being a member of the workspace on this side. The workspace's
own record is the owner's to change, and its member table is the owner's config:
nothing here reaches either, and the owner's next fugaro update publishes the
record its own table builds.

Only the keys it removes are rewritten: every comment and every other key of the
file is kept. The removal reaches the installation through fugaro update.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceRm(cmd, args[0], &o) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runWorkspaceRm(cmd *cobra.Command, name string, o *workspaceRmOptions) error {
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	switch {
	case lc.Workspace == "":
		return userErr("project %s names no workspace: there is no membership to remove", lc.Name)
	case lc.Workspace != name:
		return userErr("project %s names workspace %s, not %s: pass the workspace this project is a member of, or fugaro workspace ls for the ones its config names", lc.Name, lc.Workspace, name)
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next := old
	// The pin first: the two keys are one statement, and the loader refuses a
	// pin without a membership, so this order is what leaves the file readable
	// between the two edits.
	for _, at := range [][]string{{"workspace_pin"}, {"workspace"}} {
		if next, err = workspaceRemoveKey(sel.Path, next, at); err != nil {
			return err
		}
	}
	// No Typed: a removal takes a trust away, and D846 reserves the project's
	// own name for the writes that widen.
	if err := workspaceEditLocal(cmd, lc, sel.Path, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("stop project %s being a member of workspace %s: drop the workspace key and the pin beside it", lc.Name, name),
		Command: "fugaro workspace rm " + name,
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

func newWorkspaceMemberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member",
		Short: "Declare how the workspace reaches one of its members",
		Long: `The owner's side of a membership.

The workspace's own config holds one entry per member: the member's job service
accounts, its Firebase project and fp, and its exchange URL, which is what the
workspace's own service resolves a caller by (spec §5). The member's database
URL is not among them: it is derived from the Firebase project the verified iss
names, so an owner cannot pair one member's accounts with another's database.
Neither is the runs bucket: the workspace holds no read of a member's runs.

A member is in the workspace when the owner's table lists it AND the member's
own config names the workspace. These commands write the owner's half, in the
config of the project the workspace is named after.`,
	}
	cmd.AddCommand(newWorkspaceMemberAddCmd(), newWorkspaceMemberRmCmd())
	return cmd
}

type workspaceMemberAddOptions struct {
	jobServiceAccounts []string
	firebaseProject    string
	fp                 string
	exchangeURL        string
	repos              []string
	yes                bool
}

func newWorkspaceMemberAddCmd() *cobra.Command {
	var o workspaceMemberAddOptions
	cmd := &cobra.Command{
		Use:   "add <workspace> <project>",
		Short: "Declare a member: how the workspace reaches it",
		Long: `Write one member entry into the workspace's own project config.

The four values are the workspace owner's declaration, made once per member: the
member's job service accounts (the identities the workspace's service resolves a
caller by), its Firebase project and fp (the project the caller's token is
issued under, and the Fugaro project the token's fp claim must name), and its
exchange URL (where the workspace's signer mints a member-bound assertion for).
--repo names a repository the member contributes to the workspace, and is
repeatable.

A member the table already declares is replaced whole: the entry ends up
holding the four values given and the repositories named, and nothing else. A
re-declaration is therefore how a retired or compromised job service account
leaves the table, and it asks for the workspace's own project name because what
it removes is what the workspace trusted. Use fugaro workspace member rm to drop
the entry altogether.

Only the keys it writes are rewritten: every comment and every other key of the
file is kept, except inside the entry it replaces, which is written fresh. The
write is local; the owner's fugaro update publishes the record the table
builds.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceMemberAdd(cmd, args[0], args[1], &o)
		},
	}
	// The two keys a member entry must never carry, answered by name rather
	// than by pflag's bare "unknown flag": a person who typed one of them read
	// a design that gave the owner such a value, and the sentence has to say
	// what happened to it (the shape init.go's retired --firebase refusal uses).
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		switch err.Error() {
		case "unknown flag: --database-url":
			return userErr("--database-url is not a value the owner may set: the member's database is derived from the Firebase project its verified iss names, so an owner cannot pair one member's accounts with another's database (docs/workspaces/spec.md §5); pass --firebase-project instead (fugaro %s --help)", c.CommandPath())
		case "unknown flag: --runs-bucket":
			return userErr("--runs-bucket is not a value the owner may set: the workspace holds no read of a member's runs, so the member table carries no bucket (docs/workspaces/spec.md §5); pass --job-service-account, --firebase-project, --fp and --exchange-url (fugaro %s --help)", c.CommandPath())
		}
		return err
	})
	f := cmd.Flags()
	f.StringArrayVar(&o.jobServiceAccounts, "job-service-account", nil, "a job service account of the member, which the workspace's service resolves a caller by (repeatable)")
	f.StringVar(&o.firebaseProject, "firebase-project", "", "the member's Firebase project ID, which the caller's token is issued under")
	f.StringVar(&o.fp, "fp", "", "the member's Fugaro project name, which the token's fp claim must be")
	f.StringVar(&o.exchangeURL, "exchange-url", "", "the member's exchange endpoint, where the workspace's signer mints a member-bound assertion for")
	f.StringArrayVar(&o.repos, "repo", nil, "a repository the member contributes to the workspace, as owner/name (repeatable)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runWorkspaceMemberAdd(cmd *cobra.Command, workspace, project string, o *workspaceMemberAddOptions) error {
	if err := workspaceRefuseName(project, "project"); err != nil {
		return err
	}
	if workspace == project {
		return userErr("%s is the workspace's own project: a workspace is not a member of itself, and its config is where this table lives", workspace)
	}
	if err := workspaceMemberFlags(o); err != nil {
		return err
	}
	lc, path, err := workspaceOwnerConfig(workspace)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: path})
	if err != nil {
		return err
	}

	// An entry the table already declares is replaced WHOLE and not added to:
	// AddListItem appends and leaves what is there alone, so a second
	// declaration would keep every account and every repository the first one
	// named. Re-declaring a member is how a retired or a compromised job
	// service account leaves the table, and a merge would leave that identity
	// admitted as a caller of the workspace's credentials while the
	// confirmation named only the new one. The removal comes first because the
	// entry is rebuilt from the values given, which is the only reading of the
	// command's own sentence.
	declared := lc.WorkspaceMembers != nil && !(*lc.WorkspaceMembers)[project].IsZero()
	next := old
	if declared {
		if next, err = workspaceRemoveKey(path, next, []string{"workspace_members", project}); err != nil {
			return err
		}
	}
	// The lists first, then the scalars: AddListItem creates the sequence and
	// the mapping chain it hangs under, and SetScalar then finds the entry
	// already there rather than creating a second one.
	for _, sa := range o.jobServiceAccounts {
		if next, err = workspaceAddItem(path, next, []string{"workspace_members", project, "job_service_accounts"}, sa); err != nil {
			return err
		}
	}
	for _, repo := range o.repos {
		if next, err = workspaceAddItem(path, next, []string{"workspace_members", project, "repos"}, repo); err != nil {
			return err
		}
	}
	for _, f := range []struct{ key, value string }{
		{"firebase_project", o.firebaseProject},
		{"fp", o.fp},
		{"exchange_url", o.exchangeURL},
	} {
		if next, err = workspaceSetScalar(path, next, []string{"workspace_members", project, f.key}, f.value); err != nil {
			return err
		}
	}

	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    workspaceMemberAddWhat(workspace, project, o, declared),
		Command: workspaceMemberAddCommand(workspace, project, o),
		Yes:     o.yes,
	}
	// A replaced entry widens and narrows at once, and which the owner is doing
	// is not something this command can tell: the same declaration that adds an
	// account drops the one it did not name. So it takes the widening
	// classification, like every other write that changes what this machine
	// trusts, and says what the replace does to the values the entry carries
	// today.
	if declared {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("this replaces the entry workspace %s declares for %s: the entry is written whole, so a job service account or a repository it carries today and this declaration does not name is gone from the table",
			workspace, project)
	}
	if err := workspaceEditLocal(cmd, lc, path, edit); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// workspaceMemberAddWhat is the sentence the confirmation shows for one member
// entry: what the entry will hold afterwards, which is the whole of it when it
// replaces one.
func workspaceMemberAddWhat(workspace, project string, o *workspaceMemberAddOptions, replace bool) string {
	verb := "declare member"
	if replace {
		verb = "replace the entry for member"
	}
	what := fmt.Sprintf("%s %s in workspace %s: job service accounts %s, Firebase project %s, fp %s, exchange URL %s",
		verb, project, workspace, strings.Join(o.jobServiceAccounts, ", "), o.firebaseProject, o.fp, o.exchangeURL)
	if len(o.repos) > 0 {
		what += ", repositories " + strings.Join(o.repos, ", ")
	}
	if replace {
		what += "; anything the entry holds that is not named here is dropped"
	}
	return what
}

// workspaceMemberFlags holds the four values a member entry is built from: all
// four, because the service resolves a caller by each of them and an entry with
// one missing is a member the workspace cannot reach at all.
func workspaceMemberFlags(o *workspaceMemberAddOptions) error {
	missing := []string{}
	if len(o.jobServiceAccounts) == 0 {
		missing = append(missing, "--job-service-account")
	}
	for _, f := range []struct{ flag, value string }{
		{"--firebase-project", o.firebaseProject},
		{"--fp", o.fp},
		{"--exchange-url", o.exchangeURL},
	} {
		if f.value == "" {
			missing = append(missing, f.flag)
		}
	}
	if len(missing) > 0 {
		return userErr("a member entry is built from all four values the workspace's service resolves a caller by, and %s %s not given", strings.Join(missing, ", "), wasWere(len(missing)))
	}
	if !config.ProjectNameRE.MatchString(o.fp) {
		return userErr("--fp %s is not a project name: the token's fp claim is the member's Fugaro project, not its GCP project", o.fp)
	}
	return nil
}

func workspaceMemberAddCommand(workspace, project string, o *workspaceMemberAddOptions) string {
	cmd := fmt.Sprintf("fugaro workspace member add %s %s", workspace, project)
	for _, sa := range o.jobServiceAccounts {
		cmd += " --job-service-account " + sa
	}
	cmd += fmt.Sprintf(" --firebase-project %s --fp %s --exchange-url %s", o.firebaseProject, o.fp, o.exchangeURL)
	for _, repo := range o.repos {
		cmd += " --repo " + repo
	}
	return cmd
}

type workspaceMemberRmOptions struct {
	yes bool
}

func newWorkspaceMemberRmCmd() *cobra.Command {
	var o workspaceMemberRmOptions
	cmd := &cobra.Command{
		Use:   "rm <workspace> <project>",
		Short: "Stop declaring a member: drop its entry from the workspace's table",
		Long: `Remove one member entry from the workspace's own project config.

The workspace's service can no longer resolve that project as a caller, so its
shared-credential calls are refused at the front door until it is declared
again. The member's own config is not this command's to change: a member leaves
by running its own fugaro workspace rm, and until it does the membership is
half-done — which is what fugaro workspace show reports.

Only the entry it removes is rewritten: every comment and every other key of the
file is kept. The owner's fugaro update publishes the record the table builds.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceMemberRm(cmd, args[0], args[1], &o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runWorkspaceMemberRm(cmd *cobra.Command, workspace, project string, o *workspaceMemberRmOptions) error {
	lc, path, err := workspaceOwnerConfig(workspace)
	if err != nil {
		return err
	}
	if lc.WorkspaceMembers == nil || (*lc.WorkspaceMembers)[project].IsZero() {
		return userErr("workspace %s declares no member %s: there is no entry to remove (fugaro workspace show %s lists the ones its table holds)", workspace, project, workspace)
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: path})
	if err != nil {
		return err
	}
	next, err := workspaceRemoveKey(path, old, []string{"workspace_members", project})
	if err != nil {
		return err
	}
	if err := workspaceEditLocal(cmd, lc, path, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("stop declaring member %s in workspace %s: drop its entry from the workspace's member table", project, workspace),
		Command: fmt.Sprintf("fugaro workspace member rm %s %s", workspace, project),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

func newWorkspaceCapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cap",
		Short: "The workspace's own spend ceiling",
		Long: `The cap bounds the spend of the workspace's own credentials, and nothing else
(D1300): it is not a bound on a member's total spend, and no member's own cap is
widened by it. A member that routes every model call through the workspace is
bounded by it as a consequence of how that member is configured, which is not
the same thing as the cap being a bound on the member.

It is never spelled globally: a cap belongs to one workspace, and the workspace
is named by --workspace (F10).`,
	}
	cmd.AddCommand(newWorkspaceCapSetCmd())
	return cmd
}

type workspaceCapOptions struct {
	workspace string
	monthly   float64
	yes       bool
}

func newWorkspaceCapSetCmd() *cobra.Command {
	var o workspaceCapOptions
	cmd := &cobra.Command{
		Use:   "set --workspace <name> --monthly <amount>",
		Short: "Record the workspace's own cap in the local config",
		Long: `Write the workspace's own cap into the config of the workspace's own project.

The amount is US dollars per month, and it is what the workspace's credentials
may spend: fugaro update publishes it with the record, and the workspace's own
service meters and enforces it there. It is more than zero — the smallest cap
this file can state is 0.000001 — and at most 100000.

Only the key it writes is rewritten: every comment and every other key of the
file is kept.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runWorkspaceCapSet(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.workspace, "workspace", "", "the workspace whose cap this is (required: a cap is never spelled globally)")
	f.Float64Var(&o.monthly, "monthly", 0, "the cap in US dollars per month, more than zero")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runWorkspaceCapSet(cmd *cobra.Command, o *workspaceCapOptions) error {
	if o.workspace == "" {
		return userErr("--workspace is required: a cap belongs to one workspace, and it is never spelled globally (F10)")
	}
	if !cmd.Flags().Changed("monthly") {
		return userErr("--monthly is required: the block states its amount, so a cap of zero is not how a workspace states none — leaving the block out is")
	}
	micros, err := pricing.FromUSD(o.monthly)
	if err != nil {
		return userErr("--monthly: %v", err)
	}
	if micros < 1 {
		return userErr("--monthly %v is less than one micro-dollar, which is the smallest amount this file can state: pass at least 0.000001, or leave the cap out to state none", o.monthly)
	}
	lc, path, err := workspaceOwnerConfig(o.workspace)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: path})
	if err != nil {
		return err
	}
	amount := strconv.FormatFloat(o.monthly, 'f', -1, 64)
	next, err := workspaceSetScalar(path, old, []string{"workspace_cap", "monthly"}, amount)
	if err != nil {
		return err
	}
	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("set workspace %s's own cap to $%s a month: what the workspace's credentials may spend, metered and enforced at its own service", o.workspace, amount),
		Command: fmt.Sprintf("fugaro workspace cap set --workspace %s --monthly %s", o.workspace, amount),
		Yes:     o.yes,
	}
	// Raising a ceiling is the widening direction D846 reserves for the owner's
	// own step: asked nothing at their terminal, and refused in a coding
	// agent's session (D1552). The first cap widens nothing — there was no
	// ceiling to loosen — and lowering one takes room away.
	if lc.WorkspaceCap != nil && o.monthly > lc.WorkspaceCap.Monthly {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("this raises workspace %s's own cap from $%v to $%s a month, which is the ceiling its credentials spend against",
			o.workspace, lc.WorkspaceCap.Monthly, amount)
	}
	if err := workspaceEditLocal(cmd, lc, path, edit); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

type workspaceLsOptions struct {
	json bool
}

func newWorkspaceLsCmd() *cobra.Command {
	var o workspaceLsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the workspaces this machine's project configs name",
		Long: `List, offline, every workspace this machine's own files name.

Two kinds of config name one. A config that carries the membership key names the
workspace it belongs to, with the pin it trusts; a config that carries a member
table or a cap is the workspace's own project, named after the workspace. The
listing says which of the two each workspace is here, how many members this
machine can see for it, and how many of this machine's configs name it.

A config that could not be read is reported rather than skipped, and so is a
half-done membership: a project the table lists that names no workspace, and one
that names the workspace whose table here does not list it. Nothing here opens a
bucket, calls an API or needs a credential.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runWorkspaceLs(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

func runWorkspaceLs(cmd *cobra.Command, o *workspaceLsOptions) error {
	m, err := workspaceReadMachine()
	if err != nil {
		return err
	}
	doc := m.document()
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, w := range doc.Warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	if len(doc.Workspaces) == 0 {
		fmt.Fprintln(out, "no workspace is named by a project config on this machine: fugaro workspace add joins this project to one, and fugaro workspace member add declares one in the config of the project it is named after")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKSPACE\tMEMBERS\tMEMBERSHIPS")
	for _, w := range doc.Workspaces {
		members := "-"
		if w.Owner {
			members = strconv.Itoa(len(w.Members))
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\n", cellOneLine(w.Name), members, len(w.Memberships))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	workspacePrintProblems(out, doc)
	if !slices.ContainsFunc(doc.Workspaces, func(w workspaceDocRow) bool { return w.Owner }) {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "MEMBERS is - for a workspace whose own project config this machine does not hold: the member table is written in that config, and a member's machine holds no grant on the workspace's project to read it from anywhere else (fugaro workspace show <name> says which half this machine has)")
	}
	return nil
}

// workspacePrintProblems prints the half-done memberships the listing can see
// from this machine's own files.
func workspacePrintProblems(out io.Writer, doc workspaceDoc) {
	total := 0
	for _, w := range doc.Workspaces {
		total += len(w.Problems)
	}
	if total == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "PROBLEMS")
	for _, w := range doc.Workspaces {
		for _, p := range w.Problems {
			fmt.Fprintf(out, "%s: %s\n", cellOneLine(w.Name), cellOneLine(p))
		}
	}
}

type workspaceShowOptions struct {
	json bool
}

func newWorkspaceShowCmd() *cobra.Command {
	var o workspaceShowOptions
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one workspace from this machine's own files",
		Long: `Show one workspace, offline, from what this machine holds.

This machine's half of a membership is the membership key and the pin, in the
config of each project that names the workspace. The workspace owner's half is
the member table and the cap, in the config of the project the workspace is
named after. Which of the two this machine holds decides what there is to show,
and the command says which it is rather than printing an empty member list:

  - the workspace's own config here: the member table, the cap, and the drift
    between the table and the configs that name the workspace;
  - no such config here: the memberships this machine names, each with the pin
    it carries, and the statement that the group's own record cannot be read
    from here — a member holds no grant on the workspace's project at all, so no
    command of this family reads one.

The pin's assertion key is never printed: the command says it is set. Nothing
here opens a bucket, calls an API or needs a credential.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceShow(cmd, args[0], &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

func runWorkspaceShow(cmd *cobra.Command, name string, o *workspaceShowOptions) error {
	m, err := workspaceReadMachine()
	if err != nil {
		return err
	}
	row, ok := m.known(name)
	if !ok {
		return userErr("no project config on this machine names workspace %s and none of them is its own project: fugaro workspace ls lists the workspaces this machine knows", name)
	}
	docRow := row.document(m)
	if o.json {
		return writeJSON(cmd.OutOrStdout(), workspaceDoc{
			Version:    workspaceLsDocVersion,
			Workspaces: []workspaceDocRow{docRow},
			// nonNil, so a machine with nothing to warn about prints [] and not
			// null: the schema requires an array, and one spelling is one case
			// for a consumer to handle.
			Warnings: nonNil(m.warnings),
		})
	}
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, w := range m.warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	fmt.Fprintf(out, "WORKSPACE %s\n", oneLine(row.name))
	if row.owner != nil {
		cap := "no cap"
		if row.owner.WorkspaceCap != nil {
			cap = fmt.Sprintf("cap $%v a month", row.owner.WorkspaceCap.Monthly)
		}
		fmt.Fprintf(out, "this machine holds the workspace's own project config, so the record it publishes is built from here (%s)\n", cap)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "MEMBERS (declared by the workspace's own config here)")
		table := workspaceTable(row.owner)
		if len(table) == 0 {
			fmt.Fprintln(out, "  none: the table here declares no member yet, and a member is in the workspace only when this table lists it")
		}
		for _, p := range slices.Sorted(maps.Keys(table)) {
			mm := table[p]
			fmt.Fprintf(out, "  %s\n", oneLine(p))
			fmt.Fprintf(out, "    job service accounts: %s\n", oneLine(listOrNone(mm.JobServiceAccounts)))
			fmt.Fprintf(out, "    firebase project: %s\tfp: %s\n", oneLine(mm.FirebaseProject), oneLine(mm.FP))
			fmt.Fprintf(out, "    exchange url: %s\n", oneLine(mm.ExchangeURL))
			fmt.Fprintf(out, "    repositories: %s\n", oneLine(listOrNone(mm.Repos)))
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "MEMBERSHIPS (project configs on this machine that name this workspace)")
	if len(row.memberships) == 0 {
		fmt.Fprintln(out, "  none")
	}
	for _, mem := range row.memberships {
		fmt.Fprintf(out, "  %s\n", oneLine(mem.project))
		fmt.Fprintf(out, "    firebase project: %s\n", oneLine(mem.pin.FirebaseProject))
		fmt.Fprintf(out, "    issuer: %s\n", oneLine(mem.pin.Issuer))
		fmt.Fprintf(out, "    service account: %s\n", oneLine(mem.pin.ServiceAccount))
		fmt.Fprintln(out, "    assertion key: set")
	}
	if row.owner == nil {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "the group's own record cannot be read from here: this machine holds no config for workspace %s's own project, so the members it lists are not readable here and nothing above is a member list — a member holds no grant on the workspace's project at all\n", oneLine(name))
	}
	workspacePrintProblems(out, workspaceDoc{Workspaces: []workspaceDocRow{docRow}})
	return nil
}

// listOrNone is a list as a line names it: "none" when it holds nothing, so an
// empty line never reads as a value that failed to print.
func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// workspaceMachine is what this machine's own files say about workspaces: one
// row per workspace, built from the configs that name one and from the config
// that declares one.
type workspaceMachine struct {
	byName map[string]*workspaceRow
	// held are the project configs this read could use, by project name: the
	// drift check needs them to tell "the member's config here names no
	// workspace" from "the member's config is on another machine".
	held map[string]*localcfg.Config
	// warnings are the configs this read could not use, in name order. A config
	// it cannot read is reported rather than skipped, because a workspace that
	// config names would otherwise vanish from the listing.
	warnings []string
}

// workspaceRow is one workspace as this machine holds it.
type workspaceRow struct {
	name string
	// owner is the config of the project the workspace is named after, when
	// this machine holds one and it declares the workspace: a member table or a
	// cap. Nil is a machine that holds no half of the owner's side.
	owner *localcfg.Config
	// memberships are the configs on this machine that name this workspace.
	memberships []workspaceMembership
}

// workspaceMembership is one config on this machine that names a workspace.
type workspaceMembership struct {
	project string
	pin     localcfg.WorkspacePin
}

// workspaceReadMachine reads every project config this machine has. It opens no
// bucket and calls nothing: everything it answers comes from those files, which
// is what lets `workspace ls` and `workspace show` run in a coding agent's
// session, where the cloud writes are refused.
func workspaceReadMachine() (*workspaceMachine, error) {
	names, err := localcfg.Projects(os.Getenv)
	if err != nil {
		return nil, userErr("reading this machine's project configs: %v", err)
	}
	m := &workspaceMachine{byName: map[string]*workspaceRow{}, held: map[string]*localcfg.Config{}}
	for _, name := range names {
		lc, _, err := localcfg.LoadProject(os.Getenv, name)
		if err != nil {
			m.warnings = append(m.warnings, fmt.Sprintf("project config %s could not be read, so a workspace it names or declares is not listed here: %v", name, err))
			continue
		}
		m.held[name] = lc
	}
	// The owner's half is the config of the project the workspace is NAMED
	// AFTER, which is the file `member add` and `cap set` write: keying the row
	// by the config's own name is what makes this read and those writes agree.
	for _, name := range slices.Sorted(maps.Keys(m.held)) {
		if lc := m.held[name]; lc.WorkspaceMembers != nil || lc.WorkspaceCap != nil {
			m.row(name).owner = lc
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.held)) {
		lc := m.held[name]
		if lc.Workspace == "" {
			continue
		}
		r := m.row(lc.Workspace)
		r.memberships = append(r.memberships, workspaceMembership{project: name, pin: lc.WorkspacePin})
	}
	return m, nil
}

// workspaceTable is a config's member table, empty when it declares a cap and
// no member: a config is the workspace's own when its table OR its cap is
// written, and a config that holds only a cap holds a table of no members —
// which is a state `workspace show` reports, not one it panics on.
func workspaceTable(lc *localcfg.Config) map[string]localcfg.WorkspaceMember {
	if lc == nil || lc.WorkspaceMembers == nil {
		return nil
	}
	return *lc.WorkspaceMembers
}

// row is the row of one workspace, made on first mention.
func (m *workspaceMachine) row(name string) *workspaceRow {
	if r, ok := m.byName[name]; ok {
		return r
	}
	r := &workspaceRow{name: name}
	m.byName[name] = r
	return r
}

// known is one workspace's row, and whether this machine knows it at all.
func (m *workspaceMachine) known(name string) (*workspaceRow, bool) {
	r, ok := m.byName[name]
	return r, ok
}

// document is the machine's read as the versioned document the reads print.
func (m *workspaceMachine) document() workspaceDoc {
	doc := workspaceDoc{Version: workspaceLsDocVersion, Workspaces: []workspaceDocRow{}, Warnings: m.warnings}
	if doc.Warnings == nil {
		doc.Warnings = []string{}
	}
	for _, name := range slices.Sorted(maps.Keys(m.byName)) {
		doc.Workspaces = append(doc.Workspaces, m.byName[name].document(m))
	}
	return doc
}

// problems is W2's two-sided membership check, as far as this machine's own
// files can make it: a member the workspace's table declares whose own config
// here names no workspace (or another one), and a config here that names the
// workspace whose table does not declare it. Both halves are printed, and
// neither silently wins. A machine that holds no owner's config can make no
// check at all — the member's config is on the member's own machine — and
// reports none.
func (r *workspaceRow) problems(m *workspaceMachine) []string {
	var out []string
	if r.owner == nil {
		return out
	}
	table := workspaceTable(r.owner)
	named := map[string]bool{}
	for _, mem := range r.memberships {
		named[mem.project] = true
	}
	for _, p := range slices.Sorted(maps.Keys(table)) {
		lc, here := m.held[p]
		if !here || lc.Workspace == r.name {
			continue
		}
		out = append(out, fmt.Sprintf("the workspace's table here declares member %s, and %s's own config here names %s: the membership is half-done until that config names %s", p, p, workspaceOrNone(lc.Workspace), r.name))
	}
	for _, p := range slices.Sorted(maps.Keys(named)) {
		if _, listed := table[p]; !listed {
			out = append(out, fmt.Sprintf("%s's own config here names workspace %s, and the workspace's table here declares no member %s: the membership is half-done until the workspace declares it", p, r.name, p))
		}
	}
	return out
}
