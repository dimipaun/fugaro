package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the `fugaro workspace` family (D1303, spec.md §6): the reads,
// which are offline and read this machine's own files, and the writes, which
// are local config edits through the one write path the models and settings
// commands share (applySettingsEdit — the diff, the sentence, the
// coding-agent fence, the backup and the atomic write).
//
// A membership has two halves and they live in two people's files, so this
// family writes each half where it belongs:
//
//   - `workspace add` and `workspace rm` edit the SELECTED project's config: the
//     membership key and, beside it, the pin — the one place a member decides
//     what it trusts (spec §2).
//   - `workspace project add`, `project rm`, `cap set`, `share` and `unshare`
//     edit the config of the project the workspace is NAMED AFTER, which is the
//     workspace's own project: its config holds the owner's member table, the
//     workspace's cap and the credentials the workspace offers its members
//     (spec §5, D1300), because the owner is the side that declares how the
//     workspace reaches a member.
//
// Neither half is ever taken from the other, and no command here reaches the
// cloud. What makes a write live differs by half and both are said out loud:
// the owner's half owes the workspace's own project and the record published
// from it, which the write chains (AP6), and the member's half owes this
// project's own `fugaro update`, which publishes the record its runs read
// (R3, R4) and which no command of this machine can run for that file.

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

The reads are offline: ls, show, project ls and status, and user ls and
domains ls read this machine's own project configs and the workspace's own
roster, no bucket and no cloud node beyond it, so they work anywhere, a coding
agent's session included. The writes are of three kinds, and each ends by
naming the one command that converges the installation: fugaro update — the
local config edits above, written the way the fugaro models commands write one
(the diff, the backup and the atomic write); the workspace's own roster, its
trail and the state of its members, in the workspace's own project; and each
member's own roster and mirror, written at its own Google Cloud project.`,
	}
	cmd.AddCommand(
		newWorkspaceLsCmd(),
		newWorkspaceShowCmd(),
		newWorkspaceLoginCmd(),
		newWorkspaceAddCmd(),
		newWorkspaceRmCmd(),
		workspaceReported(newWorkspaceInitCmd(workspaceInitDepsFor(cmd.OutOrStdout())), "workspace-init"),
		newWorkspaceProjectCmd(),
		newWorkspaceUserCmd(workspaceRosterOpenFor()),
		newWorkspaceDomainsCmd(workspaceRosterOpenFor()),
		newWorkspaceCapCmd(),
		newWorkspaceShareCmd(),
		newWorkspaceUnshareCmd(),
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

// workspaceUpsertJobAccount writes one job service account of a member and the
// repositories it is a job of as one sequence item. The two are one statement,
// so they are one operation: an item written with its list, replaced where it
// stands, keeps its place in the table and its comment beside the account name
// (UpsertSeqItemListByKey's own rule).
func workspaceUpsertJobAccount(path string, src []byte, project, account string, repos []string) ([]byte, error) {
	out, err := yamledit.UpsertSeqItemListByKey(src, []string{"workspace_members", project, "job_service_accounts"}, "account", account, "repos", repos)
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
//
// The owner's half of this family no longer calls it (AP6: those writes run
// their own steps, or declare none and are live once the file is written, and
// chainApply is what ends them). The member's half does: `workspace add` and
// `workspace rm` edit the pin and the membership, and this project's own
// `fugaro update` is what carries them into the record its runs read (R3, R4),
// which no step of this machine's apply can do for another project's file. The
// commands of the other families that still hand off — accounts allow, the
// project profile and routing writes, the image map, the clone URLs — are
// AP7's audit, and their texts stay as they are: none of them is one of
// D1566's management commands, so the audit classifies that line (b) rather
// than moving a step into the command that wrote it.
func workspaceHandoff(cmd *cobra.Command, old, next []byte) {
	if yamledit.Changed(old, next) {
		fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	}
}

// workspaceChain is what every write of this family does once its edit has
// landed: the steps the change owes, run here (AP6), or saved for `fugaro
// apply` under --no-apply.
//
// The changes are the family's own declaration, per write. The two writes that
// change only the MEMBER's half of a membership — `workspace add` and
// `workspace rm` — declare none, and they are the ones apply's table answers
// with no steps for a reason of its own (settingSteps' `workspace` and
// `workspace_pin.` rows): the member's record is read by that project's own
// commands and nothing publishes it, so the change is live when the file is
// written and this returns without opening a run.
//
// The owner's half is what the two stages behind the record are for: the
// workspace's own project, whose root creates its service account and the one
// grant that reaches its database, and the published record that service reads
// (apply's ResourceWorkspace row and the `workspace_members.`/`workspace_cap.`
// rows, in initflow's order).
func workspaceChain(cmd *cobra.Command, lc *localcfg.Config, path string, old, next []byte, door string, changes []apply.Change, noApply bool) error {
	if !yamledit.Changed(old, next) {
		return nil
	}
	return chainApply(cmd.Context(), cmd, lc, localcfg.Selection{Path: path}, door, changes, noApply)
}

// workspaceOwnerHalf is the change the owner's own declarations owe: the two
// stages the workspace's own project and its published record are.
func workspaceOwnerHalf() []apply.Change {
	return []apply.Change{{Kind: apply.Resource, Key: apply.ResourceWorkspace}}
}

// workspaceDoor is one command's own name, for the run its chain opens: the
// door the run is filed and printed under, so every line a person reads names
// the command they ran (modelsAddDoor's reason).
func workspaceDoor(name string) string { return "workspace " + name }

type workspaceAddOptions struct {
	firebaseProject string
	issuer          string
	assertionKey    string
	serviceAccount  string
	serviceAddress  string
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

--service-address is the address of the workspace's own credentials service, and
it is optional: a pin without one is valid, and means this project takes no
workspace credential. With it, this project's runs call that service for a
credential the workspace holds and the project does not. It is this project's own
value and never the workspace's, so nothing the workspace publishes can move
where this project's calls go; it is https, with no query and no fragment.

The issuer must be exactly https://securetoken.google.com/<firebase-project>.
The signature is verified against Google's one published set of Firebase signing
keys, the same document for every project, so the aud/iss pair is the whole of
what tells one project's tokens from another's; a mistyped half is refused here
and refused again at the load.

The values given are the whole statement: an address this invocation does not
name is dropped from the pin, so re-pinning without one is how a project stops
taking a workspace credential.

Only the keys it writes are rewritten: every comment and every other key of the
file is kept. The write is local, and this project's own fugaro update is what
carries it to the runs, which publish the membership and the address inside the
policy-side record they read at bootstrap.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceAdd(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.firebaseProject, "firebase-project", "", "the workspace's Firebase project ID, which the token's aud must be")
	f.StringVar(&o.issuer, "issuer", "", "the issuer the exchange requires; must be https://securetoken.google.com/<firebase-project>")
	f.StringVar(&o.assertionKey, "assertion-key", "", "the key this project verifies a member-bound assertion against")
	f.StringVar(&o.serviceAccount, "service-account", "", "the workspace service account this project's grant binds to")
	f.StringVar(&o.serviceAddress, "service-address", "", "the workspace's own credentials service, https with no query or fragment; omit it for a project that takes no workspace credential")
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
		return userErr("the selected project IS workspace %s: a workspace's own project is the workspace and not a member of it, and its config carries the member table instead (fugaro workspace project add <project> --workspace %s)", name, name)
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
	// The address is optional and it is still part of the statement: the values
	// this invocation names are the whole pin, so an address it does not name
	// goes. It is the removal and not an empty scalar, because a written
	// `service_address: ""` is a value the loader would have to hold to the URL
	// rule to learn is the absence it already reads an unwritten key as, and the
	// write after the four required keys is what keeps the removal from taking a
	// workspace_pin the scalars above have just filled with it.
	if o.serviceAddress == "" {
		if next, err = workspaceRemoveKey(sel.Path, next, []string{"workspace_pin", "service_address"}); err != nil {
			return err
		}
	} else if next, err = workspaceSetScalar(sel.Path, next, []string{"workspace_pin", "service_address"}, o.serviceAddress); err != nil {
		return err
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
	if err := workspaceChain(cmd, lc, sel.Path, old, next, workspaceDoor("add"), nil, false); err != nil {
		return err
	}
	// The member's half owes no apply step and it is not live either: the run
	// reads this project's membership, its grant and the address from the
	// policy-side record this project's own `fugaro update` publishes (R3, R4),
	// so the one command left is that update and this is where the person is
	// told. A write that changed nothing converges nothing and says so by
	// saying nothing (workspaceHandoff's own rule).
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

// workspaceAddWhat is the sentence the write prints, naming the
// membership this project is joining. The assertion key is a key, so the
// sentence says there is one and does not print it.
func workspaceAddWhat(project, name string, o *workspaceAddOptions) string {
	what := fmt.Sprintf("join project %s to workspace %s: pin its Firebase project %s, its issuer %s, an assertion key, and the workspace service account %s",
		project, name, o.firebaseProject, o.issuer, o.serviceAccount)
	if o.serviceAddress == "" {
		return what + ", and take no workspace credential (no service address)"
	}
	return what + ", and call the workspace's credentials service at " + o.serviceAddress
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
	if o.serviceAddress != "" {
		u, err := url.Parse(o.serviceAddress)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return userErr("--service-address %s must be an https URL with a host and no userinfo, query or fragment: it is where this project's runs call the workspace's own credentials service, so a signed POST to a URL with a query or a fragment would reach a resource this pin does not name", o.serviceAddress)
		}
	}
	return nil
}

// workspaceAddCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: the whole write, so
// nothing of what this command decided is left for them to reconstruct. The
// assertion key is the one value it does not spell, because it is a key.
func workspaceAddCommand(name string, o *workspaceAddOptions) string {
	cmd := fmt.Sprintf("fugaro workspace add %s --firebase-project %s --issuer %s --assertion-key <key> --service-account %s",
		name, o.firebaseProject, o.issuer, o.serviceAccount)
	if o.serviceAddress != "" {
		cmd += " --service-address " + o.serviceAddress
	}
	return cmd
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
	if err := workspaceChain(cmd, lc, sel.Path, old, next, workspaceDoor("rm"), nil, false); err != nil {
		return err
	}
	// The removal owes no apply step for the member's reason and it is not live
	// either: this project's own `fugaro update` is what republishes the record
	// its runs read, and until it runs they carry the membership this file no
	// longer states (R3, R4).
	workspaceHandoff(cmd, old, next)
	return nil
}

type workspaceMemberAddOptions struct {
	jobServiceAccounts []string
	jobRepos           []string
	databaseRegion     string
	firebaseProject    string
	fp                 string
	exchangeURL        string
	repos              []string
	yes                bool
	// noApply is --no-apply (D1565): the change is written and the steps that
	// make it live are saved for `fugaro apply` instead of run here.
	noApply *bool
}

// workspaceMemberAddWhat is the sentence the write prints for one member
// entry: what the entry will hold afterwards, which is the whole of it when it
// replaces one.
func workspaceMemberAddWhat(workspace, project string, o *workspaceMemberAddOptions, replace bool) string {
	verb := "declare member"
	if replace {
		verb = "replace the entry for member"
	}
	what := fmt.Sprintf("%s %s in workspace %s: job service accounts %s, Firebase project %s, fp %s, exchange URL %s",
		verb, project, workspace, workspaceJobAccountsPhrase(o), o.firebaseProject, o.fp, o.exchangeURL)
	if o.databaseRegion != "" {
		what += ", database region " + o.databaseRegion
	}
	if len(o.repos) > 0 {
		what += ", repositories " + strings.Join(o.repos, ", ")
	}
	if replace {
		what += "; anything the entry holds that is not named here is dropped"
	}
	return what
}

// workspaceJobAccount is one job service account of a member with the
// repositories it is a job of: the two halves the table writes as one item.
type workspaceJobAccount struct {
	account string
	repos   []string
}

// jobAccountRepos pairs each --job-service-account with the repositories its
// --job-repos entries name, in the order the accounts were given: the account
// list is the table's own order, and an account the flags name nothing for
// comes out with no repository, which workspaceMemberFlags refuses before
// anything is written.
func (o *workspaceMemberAddOptions) jobAccountRepos() []workspaceJobAccount {
	byAccount := map[string][]string{}
	for _, r := range o.jobRepos {
		account, repo, _ := strings.Cut(r, "=")
		byAccount[account] = append(byAccount[account], repo)
	}
	out := make([]workspaceJobAccount, 0, len(o.jobServiceAccounts))
	for _, sa := range o.jobServiceAccounts {
		out = append(out, workspaceJobAccount{account: sa, repos: byAccount[sa]})
	}
	return out
}

// workspaceJobAccountsPhrase is a member entry's accounts as the write's own
// sentence says them: each account with the repositories it is a job of, because
// the two are written as one entry and a sentence naming the accounts alone
// would not say which repositories the table is about to hold them to.
func workspaceJobAccountsPhrase(o *workspaceMemberAddOptions) string {
	parts := make([]string, 0, len(o.jobServiceAccounts))
	for _, a := range o.jobAccountRepos() {
		parts = append(parts, fmt.Sprintf("%s (a job of %s)", a.account, strings.Join(a.repos, ", ")))
	}
	return strings.Join(parts, ", ")
}

// workspaceMemberRegion is the region an entry records for a member: the flag's
// when it is given, and otherwise the region of the member's own Realtime
// Database, read out of its own config's budget.rtdb_url (F14, D3084).
// firebaseProject is the project this declaration names, and current is the
// region the entry carries today, "" when the table declares no member yet.
//
// A member's installation is never read from the cloud here and never changed:
// what this reads is the URL the installation already has, so a member that
// chose EU is linked at its own host with no flag and its database stays where it
// is. A member whose config this machine does not hold keeps the region its entry
// already records: nothing here says where that database is, and a
// re-declaration — a job account added, a repository dropped — is no reason for
// the workspace to read that member's database at another host. The flag is the
// owner's own word, for a member the table declared before this change or one
// whose config this machine never held. A member whose config is here and names
// no database records none, because that is what its own installation says —
// unless its entry already records one, which stands: an absent budget.rtdb_url
// is a member's own installation saying nothing about where its database is, and
// reading that silence as the default location would move a member the table had
// already placed, which is the same loss as a config this machine cannot read.
//
// The config is found by the member's name, which is a project id, so the file
// that answers may be another installation's — one that was declared once under
// the same name and left behind, or a member re-created under a new Firebase
// project. Such a config's database is not the member's, and the region it names
// would derive a host that is nobody's database at all (credsvc's
// workspaceDatabase builds `<firebase_project>-default-rtdb` from the region):
// the pair is checked before anything is read out of it, both halves of it — the
// budget's own firebase_project and the label of its rtdb_url — against the
// project the declaration names, and a mismatch is refused with the flag
// printed as the fix. That is the pair the history sweeper (checkDatabaseHost)
// and the rules stage (rulesForeignDatabase) already refuse.
func workspaceMemberRegion(member, firebaseProject, flag, current string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	lc, _, err := localcfg.LoadProject(os.Getenv, member)
	if errors.Is(err, localcfg.ErrMissing) {
		return current, nil
	}
	if err != nil {
		return "", userErr("member %s's own config could not be read, so where its database is cannot be told from it: %v; fix that file, or pass --database-region to record the region without reading it", member, err)
	}
	if lc.Budget == nil {
		return current, nil
	}
	if lc.Budget.FirebaseProject != "" && lc.Budget.FirebaseProject != firebaseProject {
		return "", userErr("member %s's own config names the Firebase project %s, and this declaration names %s: another Firebase project's config says nothing about where this member's database is, and the region read out of it would point the workspace's service at a host that is not the member's database; pass --database-region with the region that database is in (fugaro workspace project add --help)", member, lc.Budget.FirebaseProject, firebaseProject)
	}
	if lc.Budget.RTDBURL == "" {
		return current, nil
	}
	if checkDatabaseHost(lc.Budget.RTDBURL, firebaseProject) != nil {
		return "", userErr("member %s's own budget.rtdb_url (%s) is not the database of the Firebase project this declaration names (%s): a Firebase project's own database is <project>-default-rtdb, so that URL is another installation's and where it keeps its data is not where this member's database is; pass --database-region with the region the member's database is in (fugaro workspace project add --help)", member, oneLine(lc.Budget.RTDBURL), firebaseProject)
	}
	return workspaceDatabaseRegion(member, lc.Budget.RTDBURL)
}

// workspaceDatabaseRegion is the region a Realtime Database URL names, "" when
// the URL names none.
//
// The region is the host's and never the project's, and the host is the only
// place it is written: a database in a region is named there by the region alone
// (`<database>.<region>.firebasedatabase.app`), while the legacy
// `<database>.firebaseio.com` host names no region at all — neither does a
// loopback address, which is an emulator and not a place.
//
// A regional host in a place the Realtime Database does not exist in is refused
// rather than recorded: recording it would hand the workspace's service a host
// that is no database, and the owner is the one who can say which of the
// locations the member's database is in.
func workspaceDatabaseRegion(member, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback()) || host == "localhost" {
		return "", nil
	}
	rest, ok := strings.CutSuffix(host, ".firebasedatabase.app")
	if !ok {
		return "", nil
	}
	_, region, ok := strings.Cut(rest, ".")
	if !ok {
		return "", nil
	}
	if region == workspaceDatabaseDefaultRegion {
		return "", nil
	}
	if !slices.Contains(workspaceDatabaseLocations, region) {
		return "", userErr("member %s's budget.rtdb_url names the region %s, which is not a Realtime Database location: a database exists in %s and nowhere else, so recording %s would point the workspace's service at a host that is not the member's database; pass --database-region with the region that database is in (fugaro workspace project add --help)",
			member, region, strings.Join(workspaceDatabaseLocations, ", "), region)
	}
	return region, nil
}

var (
	// workspaceDatabaseLocations are the places a Realtime Database exists, read
	// 2026-11-20 (firebase.google.com/docs/database/locations).
	workspaceDatabaseLocations = []string{"us-central1", "europe-west1", "asia-southeast1"}
	// workspaceDatabaseDefaultRegion is the one of them that has no regional
	// host, so a database there is named by the legacy host and a URL naming it
	// derives no region at all: credsvc's workspaceDatabase builds a regional
	// host out of any non-empty region, and for this one there is none to build.
	workspaceDatabaseDefaultRegion = workspaceDatabaseLocations[0]
)

// workspaceMemberFlags holds the values a member entry is built from: all four
// the service resolves a caller by, because an entry with one missing is a
// member the workspace cannot reach at all, and at least one repository for each
// job service account, because the service holds a run's repository to the
// calling account's own list (R6, D2992).
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
	// The accounts are held to the ones this declaration names first, so a
	// repository written for an account that is not in the table — a typo, or an
	// account dropped from the invocation — is refused by name rather than
	// written beside the table where no caller would ever reach it.
	accounts := make(map[string]bool, len(o.jobServiceAccounts))
	for _, sa := range o.jobServiceAccounts {
		accounts[sa] = true
	}
	for _, r := range o.jobRepos {
		account, repo, ok := strings.Cut(r, "=")
		switch {
		case !ok || account == "" || repo == "":
			return userErr("--job-repos %s must be <account>=<owner/name>: it states which job service account is a job of which repository, and the two are written as one entry", r)
		case !accounts[account]:
			return userErr("--job-repos %s names %s, which is not one of this declaration's job service accounts (%s): an account's repositories are the jobs the workspace's service holds that account's runs to, so a repository written for an account the entry does not carry is one nothing reaches",
				r, account, strings.Join(o.jobServiceAccounts, ", "))
		}
	}
	bare := []string{}
	for _, a := range o.jobAccountRepos() {
		if len(a.repos) == 0 {
			bare = append(bare, a.account)
		}
	}
	if len(bare) > 0 {
		return userErr("no --job-repos is given for %s: every job service account carries the repositories it is a job of — one for a repository's own job, several for a shared project job — so name them as --job-repos <account>=<owner/name>",
			strings.Join(bare, ", "))
	}
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
	// noApply is --no-apply (D1565), as workspaceMemberAddOptions'.
	noApply *bool
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
	o.noApply = addNoApplyFlag(cmd)
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
	return workspaceChain(cmd, lc, path, old, next, workspaceDoor("cap set"), workspaceOwnerHalf(), *o.noApply)
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
		fmt.Fprintln(out, "no workspace is named by a project config on this machine: fugaro workspace add joins this project to one, and fugaro workspace project add declares one in the config of the project it is named after")
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
			fmt.Fprintf(out, "    job service accounts: %s\n", multiLine(workspaceJobAccountLines(mm.JobAccounts)))
			if mm.DatabaseRegion != "" {
				fmt.Fprintf(out, "    database region: %s\n", oneLine(mm.DatabaseRegion))
			}
			fmt.Fprintf(out, "    firebase project: %s\tfp: %s\n", oneLine(mm.FirebaseProject), oneLine(mm.FP))
			fmt.Fprintf(out, "    exchange url: %s\n", oneLine(mm.ExchangeURL))
			fmt.Fprintf(out, "    repositories: %s\n", oneLine(listOrNone(mm.Repos)))
		}
		shared := workspaceShared(row.owner)
		if len(shared) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "SHARED (what the workspace's credentials service may offer)")
			for _, name := range slices.Sorted(maps.Keys(shared)) {
				sh := shared[name]
				fmt.Fprintf(out, "  %s (%s)\n", oneLine(name), oneLine(sh.Kind))
				// Who the entry is offered to: the members the owner named, or
				// every member of the workspace (D2977). The two are exclusive
				// in the file, so an entry that carries all_members prints no
				// list: `projects: none` would read as an entry offered to
				// nobody, which is the opposite of what the owner declared.
				if sh.AllMembers {
					fmt.Fprintln(out, "    offered to: every member of the workspace, the ones that join later included")
				} else {
					fmt.Fprintf(out, "    projects: %s\n", oneLine(listOrNone(sh.Projects)))
				}
				if sh.Kind != localcfg.WorkspaceSharedModel {
					fmt.Fprintf(out, "    covers: %s\n", oneLine(listOrNone(sh.Covers)))
				}
			}
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
		if mem.pin.ServiceAddress == "" {
			fmt.Fprintln(out, "    service address: none — this project takes no workspace credential")
		} else {
			fmt.Fprintf(out, "    service address: %s\n", oneLine(mem.pin.ServiceAddress))
		}
		fmt.Fprintln(out, "    assertion key: set")
	}
	if row.owner == nil {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "the group's own record cannot be read from here: this machine holds no config for workspace %s's own project, so the members it lists are not readable here and nothing above is a member list — a member holds no grant on the workspace's project at all\n", oneLine(name))
	}
	workspacePrintProblems(out, workspaceDoc{Workspaces: []workspaceDocRow{docRow}})
	return nil
}

// workspaceJobAccountLines is a member's job service accounts as `workspace
// show` prints them: each account with the repositories it is a job of on its
// own line, because the two are one statement and a line naming the accounts
// alone would not say what the table holds them to.
func workspaceJobAccountLines(accounts []localcfg.WorkspaceJobAccount) string {
	if len(accounts) == 0 {
		return "none"
	}
	lines := make([]string, 0, len(accounts))
	for _, a := range accounts {
		lines = append(lines, fmt.Sprintf("%s (a job of %s)", a.Account, listOrNone(a.Repos)))
	}
	return strings.Join(lines, "\n      ")
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
	// AFTER, which is the file `workspace project add` and `cap set` write: keying the row
	// by the config's own name is what makes this read and those writes agree.
	for _, name := range slices.Sorted(maps.Keys(m.held)) {
		if lc := m.held[name]; lc.WorkspaceMembers != nil || lc.WorkspaceCap != nil || lc.WorkspaceShared != nil {
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
