package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro user is a front for the installation's two IAM lists — F42's word
// for it: the lists exist, and `fugaro setup --launcher/--operator` already
// replaces a whole one, so this command is the way to change ONE member of
// one of them without holding the other members in a command line. It is the
// interim of D627: the roles themselves (E0, E4 onward) come later and are a
// live claim checked by a security service; until then a launcher or an
// operator is an IAM member on the installation's own roles.
//
// What it is, stated rather than implied:
//
//   - It changes the LOCAL config's `terraform.launchers` /
//     `terraform.operators` and nothing else. Those lists are what an
//     operator's `fugaro update` applies to the installation, so the change
//     takes effect then and not here. Nothing is applied, no IAM grant is
//     written and no invitation is sent: the command prints the invite for a
//     person to paste, which is the spec's rule (the CLI sends no email by
//     default).
//   - It writes `user:` members and nothing else. The launcher and operator
//     roles carry the token signer's own `token_minter` role (F44), and
//     localcfg's member rule admits `domain:` for these two lists — so a
//     `domain:` member would hand a whole domain the right to mint a
//     launcher's identity, which is F42's first trap. `fugaro setup
//     --launcher` can still write one (it is the owner's whole-list door,
//     with the confirmation that goes with it); this command refuses, which
//     is what lets a member be added here without reading the rest of the
//     list.
//   - It refuses a stale local list before it writes. The lists live in each
//     operator's own local config (the `terraform` block is not in the shared
//     config), so one operator's list that predates a member another operator
//     added would — on the next `fugaro update` — silently revoke that
//     member, and an add written from the stale list would do it in the same
//     breath. The two lists are therefore read back from the installation's
//     own outputs first, and a config that disagrees with them is refused
//     with the command that settles it (F42's second trap).
//   - It refuses in a coding agent's session, the way `fugaro allow-delete`
//     does: an agent holds the operator's credentials and could write the
//     config file itself, so the refusal is a speed bump (SECURITY.md's
//     Owner/Editor bypass) and not a boundary.
//
// It is registered on the root (root.go), and the working skill and the
// generated CLI reference name it.

// userListSource is what every refusal and every listing of this command says about
// where the lists live, so the sentence cannot drift between them.
const userListSource = "terraform.launchers and terraform.operators in this machine's own project config"

// userInstallLine is the one install line the invite carries, the same one the
// README and the console show.
const userInstallLine = "brew install dimipaun/tap/fugaro"

// newUserCmd is `fugaro user`.
func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "List, add and remove the installation's launchers and operators",
		Long: `List the installation's launchers and operators, and add a person to one of
the two lists or remove them from it.

The two lists are ` + userListSource + `. They are what the operator's next
fugaro update applies to the installation, so a change made here takes effect
then, and nothing is applied by this command: no IAM grant is written, no
repository is touched and no invitation is sent.

A member is one person, written as user:<email>. Only user: members are
written here: domain:, group:, serviceAccount:, allUsers,
allAuthenticatedUsers and wildcards are refused (reconciliation F42 — the
launcher and operator roles carry the token signer's own role, so a domain:
member would grant a whole domain the right to mint a launcher's identity).
fugaro setup --launcher/--operator remains the door for a whole list, group
members included.

add and rm read the installation's own outputs first and refuse when they
disagree with this machine's config: a list written here replaces the
installation's on the next fugaro update, so a config that has not seen the
members another operator added would drop them.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newUserLsCmd(), newUserAddCmd(), newUserRmCmd())
	return cmd
}

// newUserLsCmd is `fugaro user ls`: the two lists, and nothing else.
func newUserLsCmd() *cobra.Command {
	var o cloudOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the installation's launchers and operators",
		Long: `List the members of ` + userListSource + `, as they are written there now.

It is this machine's own copy of the two lists: no member is fetched from the
cloud and none is granted, and no Terraform runs. What the installation itself
holds can differ — a change another operator applied, or one made here and not
yet applied by a fugaro update — and only the outputs would tell, which is the
read add and rm make.

A machine with no project config of its own is refused rather than answered
with two empty lists: the lists are not in the shared config, and selection's
fallback to it is a fetch of the runs bucket that only this machine's own file
would have made unnecessary.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A coding agent may run ls, and selection on a machine with no
			// project config of its own fetches the shared config from the
			// runs bucket with the person's credentials before the refusal
			// below: GODEBUG=http2debug would print them to stderr.
			if err := refuseHTTP2Debug(os.Getenv); err != nil {
				return err
			}
			sel, lc, err := selectProject(cmd.Context(), o)
			if err != nil {
				return err
			}
			if err := userLocalConfig(sel, lc); err != nil {
				return err
			}
			return printUserLists(cmd.OutOrStdout(), lc, asJSON)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// newUserAddCmd is `fugaro user add MEMBER --role launcher|operator`.
func newUserAddCmd() *cobra.Command {
	return newUserEditCmd("add MEMBER", "Add a person to the installation's launchers or operators", true)
}

// newUserRmCmd is `fugaro user rm MEMBER --role launcher|operator`.
func newUserRmCmd() *cobra.Command {
	return newUserEditCmd("rm MEMBER", "Remove a person from the installation's launchers or operators", false)
}

// newUserEditCmd builds add and rm: one member, one list, one direction.
func newUserEditCmd(use, short string, add bool) *cobra.Command {
	var o cloudOptions
	var role string
	// The two share every sentence but one: the invite is add's alone, and a
	// paragraph of rm's own would be a claim about the other subcommand.
	long := `Change one member of one of the installation's two IAM lists: ` + userListSource + `.

The member is added to, or removed from, the list --role names, and the other
list is left exactly as it is. --role is required and has no default: adding
someone to the wrong list is a grant, and a default would decide that for the
person typing.

Both subcommands read the installation's own outputs before they write and
refuse when they disagree with this machine's config, because this machine's
lists are what the next fugaro update applies: a config that predates a member
another operator added would revoke that member. The refusal names the list
that differs and the command that settles it (fugaro update).
`
	if add {
		long += `
It prints an invite naming the project, the install line and the fact that the
repository's fugaro.yaml names the project, to paste wherever the person will
read it: no email is sent.
`
	}
	long += `
Run it in your own terminal, with your own credentials. A coding agent's
session is refused.`
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return userEdit(cmd, o, role, args[0], add)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().StringVar(&role, "role", "", "which list to change: launcher or operator (required)")
	return cmd
}

// userRole is --role's value: which of the installation's two lists a member
// is added to or removed from.
type userRole string

const (
	userLauncherRole userRole = "launcher"
	userOperatorRole userRole = "operator"
)

// userRoleFlag parses --role. "" is the flag not being passed, which is
// refused rather than defaulted: there is no safe default between the two.
func userRoleFlag(s string) (userRole, error) {
	switch userRole(s) {
	case userLauncherRole, userOperatorRole:
		return userRole(s), nil
	case "":
		return "", userErr("--role is required: launcher or operator (it names which of the installation's two lists this changes, and neither is the default)")
	default:
		return "", userErr("--role %q must be launcher or operator", safetext.Field(s))
	}
}

// members is the list this role names, as the config holds it now.
func (r userRole) members(lc *localcfg.Config) []string {
	if r == userOperatorRole {
		return lc.Terraform.Operators
	}
	return lc.Terraform.Launchers
}

// set writes list as this role's list in next.
func (r userRole) set(next *localcfg.Config, list []string) {
	if r == userOperatorRole {
		next.Terraform.Operators = list
		return
	}
	next.Terraform.Launchers = list
}

// holds reports whether member is on this role's list, read off the same two
// member predicates every other caller places an identity with
// (roles.go). It decides what to print and whether anything is written; it
// grants nothing.
func (r userRole) holds(member string, lc *localcfg.Config) bool {
	if r == userOperatorRole {
		return operatorMember(member, lc.Terraform.Operators)
	}
	return launcherMember(member, lc.Terraform.Launchers)
}

// userMember validates the MEMBER argument and is the command's ONE rule about
// what a member may be: one person, user:<email>, and nothing else.
//
// The list here is localcfg's member rule minus everything that is not one
// person — and localcfg's rule is deliberately NOT the check, because it
// admits `domain:` for these two lists while its own budget_admins refuses it
// (F42): a domain: member would give every account in a domain the launcher
// and operator roles, the token signer's role among them (F44). group: and
// serviceAccount: are refused for the same reason at this door — a group's
// membership is not visible here at all, and an account is not a person. The
// refusal is by the prefix as it is written, so a mixed-case Domain: is not a
// member of this command's either, and whitespace around it is refused rather
// than trimmed: what is written is what an apply would read, and a member that
// only equals user:<email> after being trimmed would be recorded as something
// else.
func userMember(m string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", userErr("a member is one person as user:<email> (%s): domain:, group:, serviceAccount:, allUsers, allAuthenticatedUsers and wildcards are refused, because the launcher and operator roles carry the token signer's own role (finding F42); fugaro setup --launcher/--operator is the door for a whole list", why)
	}
	switch {
	case m != strings.TrimSpace(m):
		return refuse("what was passed has whitespace around it")
	case !strings.HasPrefix(m, "user:"):
		return refuse(fmt.Sprintf("%s is not written as user:<email>", safetext.Field(m)))
	}
	addr := strings.TrimPrefix(m, "user:")
	switch {
	case addr == "" || !strings.Contains(addr, "@"):
		return refuse(fmt.Sprintf("%s names no address", safetext.Field(m)))
	case strings.ContainsAny(addr, "*?[] \t\r\n"):
		return refuse(fmt.Sprintf("%s is not one address", safetext.Field(m)))
	}
	return m, nil
}

// sameUserMemberFold reports whether two member texts name the same person,
// comparing the address with its case folded.
//
// It is the self-removal guard's own comparison and nothing else — a local-CLI
// guard, not the rule the lists are read or written by: roles.go places an
// identity on these lists exactly (operatorMember, meMember), and which members
// a list holds, what a change writes and what an apply grants are unchanged. The
// guard is the one reader that cannot afford the exact spelling, because the
// person it is about is spelled once by the machine that typed their identity
// and once by the member IAM handed back. The empty member is on no list and
// equals nothing here: meMember answers it for an identity that cannot be told,
// and reading that as "not the caller" is what the guard exists to prevent.
func sameUserMemberFold(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(a, b)
}

// userLocalConfig refuses a selection that has no file to write: a shared
// config carries no terraform block (F42), so its two "lists" are empty and
// would be a lie, and a change has nowhere to go.
func userLocalConfig(sel localcfg.Selection, lc *localcfg.Config) error {
	if sel.Path == "" {
		return userErr("fugaro user reads %s, and this machine has none: project %s was selected from the shared config (it carries no terraform block); run fugaro setup on this machine first", userListSource, safetext.Field(sel.Name))
	}
	if lc == nil {
		return userErr("no project config was read")
	}
	return nil
}

// userEdit is add and rm's body: one member, one list, one direction.
func userEdit(cmd *cobra.Command, o cloudOptions, role, member string, add bool) error {
	ctx := cmd.Context()
	// Before anything is read, and before the member is even looked at: the
	// refusal is about who is running, which allow_delete.go decides the same
	// way for the same reason — a coding agent's session holds the operator's
	// credentials and can write the config file directly.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro user add and rm change the installation's launcher and operator lists, which grant the roles a run is launched and a repository onboarded with, so they are run by a person at their own terminal: %s", initflow.AgentRefusal(m))
	}
	// GODEBUG=http2debug makes Go print HTTP requests with their credentials
	// to stderr, and it is read before main runs (refuseHTTP2Debug). This
	// command opens the cloud's own read of the installation's outputs.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	r, err := userRoleFlag(role)
	if err != nil {
		return err
	}
	who, err := userMember(member)
	if err != nil {
		return err
	}
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	if err := userLocalConfig(sel, lc); err != nil {
		return err
	}
	old, err := os.ReadFile(sel.Path)
	if err != nil {
		return userErr("reading the project config: %v", err)
	}
	// The config as its own file writes it, not the selection's copy: the
	// selection has this invocation's --region applied to it (selectProject →
	// announce → localcfg.Config.Override), so a document marshalled from it
	// would make an ephemeral --region the installation's own region on disk,
	// and the next fugaro update would apply it to every resource of the
	// project — a change to far more than the two lists this command is for.
	// --gcp-project needs no such care: Override refuses one that is not the
	// config's own. (restore.go's planConfig writes from the file for the same
	// reason.)
	onDisk, err := localcfg.Parse(old)
	if err != nil {
		return userErr("the project config %s: %v", safetext.Field(sel.Path), err)
	}
	outs, err := userOutputs(ctx, cmd, lc)
	if err != nil {
		return err
	}
	if err := userFresh(onDisk, outs); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if r.holds(who, onDisk) == add {
		// Nothing to do, and nothing is written: adding a member the list
		// already holds must not reorder or duplicate it, and removing one it
		// does not hold must not touch the file at all. The next fugaro
		// update applies the list as it is.
		fmt.Fprintf(w, "%s is %s %s of %s; nothing was written\n", safetext.Field(who), userAlready(add), r.phrase(), safetext.Field(lc.Name))
		return nil
	}
	next := *onDisk
	list := slices.Clone(r.members(onDisk))
	if add {
		list = append(list, who)
	} else {
		list = slices.DeleteFunc(list, func(m string) bool { return m == who })
	}
	// Removing yourself from the operators is refused, and refused before the
	// last-operator check below: a caller who is the only operator is told they
	// cannot take themselves off the list rather than that the list would be
	// left empty, because somebody else removing them is the way out of both
	// and the count is a consequence of whose member this is. meMember turns the
	// identity into the member text the two lists hold (roles.go), and the two
	// spellings it puts side by side come from different hands: this
	// invocation's identity keeps the case its owner typed (`user:` in the
	// config, else git config user.email), while a list written from the
	// members IAM hands back is lower-cased. An address's case is not part of
	// who it is, so both sides are placed with the case folded
	// (sameUserMemberFold) — a guard that read Person-1@Example.test and
	// user:person-1@example.test as two people would let an operator remove
	// themselves from the one list that is who can be told to settle the
	// installation. An identity lc.Me cannot tell is refused below rather than
	// placed: the empty member it would answer is on no list, so reading its
	// error as "not the caller" is what let an operator remove themselves.
	if !add && r == userOperatorRole {
		// An identity that cannot be read is refused rather than treated as
		// the empty member: the empty member is on no list (meMember), so the
		// guard below would let the caller's own member go — an operator
		// removing themselves is exactly what this refusal exists to stop.
		me, err := lc.Me(ctx)
		if err != nil {
			return userErr("nothing was written: fugaro user rm cannot tell whether %s is the identity this invocation is running as, because %v (set user in the local config %s, or git config user.email, and rerun this). An operator must not remove themselves from %s — the operators list is who can be told to settle the installation — so with the identity unread the removal is refused rather than compared against a member that is on no list",
				safetext.Field(who), err, safetext.Field(sel.Path), safetext.Field(onDisk.Name))
		}
		caller := meMember(me)
		if slices.ContainsFunc(lc.Terraform.Operators, func(m string) bool { return sameUserMemberFold(m, caller) }) && sameUserMemberFold(who, caller) {
			return userErr("you cannot remove yourself from the operators of %s: %s is the identity this invocation is running as, and the operators list is who can be told to settle the installation — run its update, add a member, read its budget — so another operator has to remove you, or a project Owner from outside the list: nothing was written",
				safetext.Field(onDisk.Name), safetext.Field(who))
		}
	}
	// The last operator is refused: the operators list is who can be told to
	// settle an installation — run its update, add a member, read its budget —
	// and a machine's next fugaro update would apply an empty one, dropping
	// the operator bindings of everyone who was on it. A project Owner can put
	// that right from outside, which is why this is a refusal and not a
	// break-glass (the launchers list may be emptied: an installation with no
	// launcher cannot start a run, which its operator can put right here).
	if !add && r == userOperatorRole && len(list) == 0 {
		return userErr("removing %s would leave %s with no operator at all: it is the only member of this machine's terraform.operators, and the next fugaro update would apply an empty list and drop every operator binding, so the people it lists would have to be restored by a project Owner from outside. Add the operator who is to take over first, then remove %s: nothing was written",
			safetext.Field(who), safetext.Field(onDisk.Name), safetext.Field(who))
	}
	r.set(&next, list)
	if err := userSave(w, sel.Path, old, &next); err != nil {
		return err
	}
	if !add {
		fmt.Fprintf(w, "%s is no longer %s of %s; the change takes effect on the operator's next fugaro update: nothing was applied here, and no email is sent\n", safetext.Field(who), r.phrase(), safetext.Field(lc.Name))
		return nil
	}
	fmt.Fprintf(w, "%s is now %s of %s\n", safetext.Field(who), r.phrase(), safetext.Field(lc.Name))
	userInvite(w, who, lc)
	fmt.Fprintf(w, "the change takes effect on the operator's next fugaro update: nothing was applied here, and no email is sent\n")
	return nil
}

// phrase is this role with its article, because the sentences this command
// prints name it: "a operator" is not one of them.
func (r userRole) phrase() string {
	if r == userOperatorRole {
		return "an operator"
	}
	return "a launcher"
}

// userAlready is the word the two do-nothing answers share.
func userAlready(add bool) string {
	if add {
		return "already"
	}
	return "not"
}

// userOutputs is the installation's own outputs: launchers, operators, the
// project name and the rest, read back from its state. It is the read
// init_new_machine.go's new-machine path makes of the same outputs, outside
// the converge: installationOutputs prepares a workdir of its own for the
// installation root, inits it and reads it, leaving the installation's own
// workdir as it is. The Terraform calls are init, show -json and output and
// nothing else — no plan and no apply — and no IAM member is written: the
// lists this command edits are the local config's.
func userOutputs(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config) (infra.InstallationOutputs, error) {
	// The state bucket's name is the spec's to resolve (infra.Installation:
	// the flag, the config's own terraform.state_bucket, else the default),
	// so the one place that answers for it is asked rather than a default
	// spelled again here.
	spec, err := infra.Installation(lc, infra.InstallOptions{})
	if err != nil {
		return infra.InstallationOutputs{}, initErr(err)
	}
	r := &initRun{cmd: cmd, o: &initOptions{door: "user"}, w: cmd.ErrOrStderr()}
	bin, err := r.checkEnv(lc)
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	return r.installationOutputs(ctx, lc, bin, spec.StateBucket)
}

// userFresh refuses a local config whose two lists are not what the
// installation's outputs say, and names the list that differs and the command
// that settles it.
//
// The comparison is the members, not their order: a list is a set of grants,
// and a config that holds the same members in another order is not stale. A
// member repeated in one of them is a difference — nothing writes a duplicate,
// so one is a hand-edited file's and is refused rather than silently
// normalised.
//
// Why refuse at all: this machine's lists are what its operator's next
// `fugaro update` applies, so a change written from a list that predates
// another operator's member would revoke that member in the same apply, and
// nothing in the outputs would have said so (F42).
func userFresh(lc *localcfg.Config, outs infra.InstallationOutputs) error {
	for _, l := range []struct {
		name   string
		local  []string
		remote []string
	}{
		{"launchers", lc.Terraform.Launchers, outs.Launchers},
		{"operators", lc.Terraform.Operators, outs.Operators},
	} {
		if sameUserMembers(l.local, l.remote) {
			continue
		}
		return userErr("this machine's %s is not what the installation's own outputs list: the config lists %s, the installation lists %s. This machine's lists are what its next fugaro update applies, so a change written now would also replace the installation's list with this stale one: run fugaro update first, then rerun this",
			l.name, safetext.FieldListLine(l.local), safetext.FieldListLine(l.remote))
	}
	return nil
}

// sameUserMembers reports whether two member lists hold the same members. The
// order does not matter (a list is a set of grants) and a repeat does.
func sameUserMembers(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// userSave writes next over the project config at path, after the config's own
// validation accepts it, and backs the file it replaces up the way every other
// writer of this file does (writeLocalConfig): this machine's lists are the
// only copy of what the next apply will write.
func userSave(w io.Writer, path string, old []byte, next *localcfg.Config) error {
	data, err := next.Marshal()
	if err != nil {
		return err
	}
	// The config's own rules run on the document that is about to be written,
	// so a member the file's own validation refuses fails here instead of
	// being written.
	if _, err := localcfg.Parse(data); err != nil {
		return userErr("the new project config: %v", err)
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up the project config: %v", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return userErr("writing the project config: %v", err)
	}
	fmt.Fprintf(w, "wrote %s (the old file is at %s)\n", safetext.Field(path), safetext.Field(backup))
	return nil
}

// userInvite prints what the operator pastes to the person who was added: the
// project's name, the install line, and the fact that the checkout needs no
// setup — the repository's fugaro.yaml names the project. No email is sent and
// nothing else is carried: no address is minted here, no token, no key and no
// secret is part of the invitation (the person's own fugaro session is what
// their machine holds, not this text).
func userInvite(w io.Writer, who string, lc *localcfg.Config) {
	fmt.Fprintf(w, "invite for %s, to paste wherever they will read it (no email is sent):\n", safetext.Field(who))
	fmt.Fprintf(w, "  project: %s\n", safetext.Field(lc.Name))
	fmt.Fprintf(w, "  install: %s\n", userInstallLine)
	fmt.Fprintf(w, "  the repository's fugaro.yaml names the project; no setup is needed\n")
}

// userListDoc is `fugaro user ls --json`: the two lists of one installation.
// Both arrays are seeded empty and never null: "there are none" is a fact and
// a null is not (the console's own availability rule for a JSON document).
type userListDoc struct {
	Project    string   `json:"project"`
	GCPProject string   `json:"gcp_project"`
	Launchers  []string `json:"launchers"`
	Operators  []string `json:"operators"`
}

// printUserLists prints the two lists: the human listing, or the document.
func printUserLists(w io.Writer, lc *localcfg.Config, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(userListDoc{
			Project: lc.Name, GCPProject: lc.GCPProject,
			Launchers: userMembers(lc.Terraform.Launchers), Operators: userMembers(lc.Terraform.Operators),
		})
	}
	for _, l := range []struct {
		name    string
		members []string
	}{
		{"launchers", lc.Terraform.Launchers},
		{"operators", lc.Terraform.Operators},
	} {
		fmt.Fprintf(w, "%s of %s (%d), as this machine's config lists them:\n", l.name, safetext.Field(lc.Name), len(l.members))
		for _, m := range l.members {
			fmt.Fprintf(w, "  %s\n", pluginwire.Printable(m))
		}
	}
	return nil
}

// userMembers is a list as the document carries it: never nil, and every
// member escaped the way this program prints a value it did not write (the
// lists come out of the installation's state and out of a file on this
// machine, neither of which this process wrote).
func userMembers(list []string) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, pluginwire.Printable(m))
	}
	return out
}
