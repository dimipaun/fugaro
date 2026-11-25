package cli

// workspace_domains.go is WU7's second half: `workspace domains add|rm|ls`, the
// workspace's allowed domains (D2812).
//
// A workspace admits an address whose domain it lists, and refuses one from
// anywhere else unless the person adding them says --external. The list is a
// list of DOMAINS and not of addresses: a domain is the part after the one `@`,
// held in the folded spelling an address is folded into (C1's
// CanonicalAddress), so the comparison the roster makes — the domain of an
// address against this list — is one spelling against one spelling.
//
// Two decisions are worth the sentence.
//
//   - A domain a member's address sits in cannot be taken off the list while
//     that member is in the roster. The external mark is COMPUTED from this list
//     rather than trusted (C1 writes it on a person, and the drift comparison
//     reads Domains), so removing a domain somebody is in would report every one
//     of those members as a difference the next check calls tampering. The
//     refusal names the member and the two ways out: remove them, or keep the
//     domain.
//   - `ls` reads, and nothing else here does. Every edit runs through the same
//     admin gate `workspace user` runs through (D2823, the intersection of the
//     roster's admin list and the live binding), and every edit is filed in the
//     workspace's trail (D2811). The machine-readable form of this list is the
//     `domains` member of `workspace user ls --json` rather than a second
//     document with a second version: one document is one contract to keep
//     (CLAUDE.md, "a field never changes meaning"), and `ls` here is the shape
//     a person reads.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// newWorkspaceDomainsCmd is `fugaro workspace domains add|rm|ls`. Like its
// sibling it takes its dependencies and builds no reporter: I1 wraps it and
// registers it (D1354, plan §1).
func newWorkspaceDomainsCmd(open workspaceRosterOpen) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domains",
		Short: "The email domains the workspace admits people from",
		Long: `The domains the workspace admits people from.

An address whose domain is on this list is admitted as a workspace member
without a word; an address from anywhere else is admitted only with --external,
which records the mark that says so (D2812). The list is the workspace's own,
next to its roster, and every change to it is a workspace admin's act.

A domain is stored in its folded spelling — the lower case an address is folded
into — so one domain is one entry however it is typed.

The list is per DOMAIN: there is no entry for one address, and admitting a
single outsider is what the external mark is for rather than a one-address
exception. A domain somebody already in the roster sits in cannot be taken off
the list while that person is a member: whether they were admitted from outside
is computed from this list, so removing their domain would report them as a
difference the drift check calls tampering.`,
	}
	cmd.AddCommand(
		workspaceReported(newWorkspaceDomainsAddCmd(open), "workspace-domains-add"),
		workspaceReported(newWorkspaceDomainsRmCmd(open), "workspace-domains-rm"),
		newWorkspaceDomainsLsCmd(open),
	)
	return cmd
}

type workspaceDomainsAddOptions struct {
	workspace string
}

func newWorkspaceDomainsAddCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceDomainsAddOptions{}
	cmd := &cobra.Command{
		Use:   "add DOMAIN --workspace <name>",
		Short: "Admit every address of a domain",
		Long: `Add one domain to the workspace's allowed domains.

Every address whose domain is this one is then admitted as a member without
--external. The domain is stored folded, so Example.COM and example.com are one
entry, and adding one that is already there is refused rather than written
twice: the list holds each domain once.

Nothing is admitted by this command but the domain: nobody becomes a member
until they are added (the workspace's roster), and nothing is granted to anybody
by it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceDomainsAdd(cmd, open, o, args[0]) },
	}
	cmd.Flags().StringVar(&o.workspace, "workspace", "", "the workspace whose allowed domains these are (required: the list belongs to one workspace)")
	return cmd
}

type workspaceDomainsRmOptions struct {
	workspace string
}

func newWorkspaceDomainsRmCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceDomainsRmOptions{}
	cmd := &cobra.Command{
		Use:   "rm DOMAIN --workspace <name>",
		Short: "Stop admitting every address of a domain",
		Long: `Remove one domain from the workspace's allowed domains.

The members the workspace already holds are not touched: removing a domain stops
admitting NEW addresses of it, and everybody who is a member stays one. What
changes is that the next address from that domain needs --external, which
records the mark D2812 keeps.

A domain a member's address sits in is refused rather than removed: whether a
member was admitted from outside is computed from this list, so removing their
domain would report them as a difference the drift check calls tampering. Remove
them first (` + "`fugaro workspace user rm`" + `), or keep the domain.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceDomainsRm(cmd, open, o, args[0]) },
	}
	cmd.Flags().StringVar(&o.workspace, "workspace", "", "the workspace whose allowed domains these are (required: the list belongs to one workspace)")
	return cmd
}

type workspaceDomainsLsOptions struct {
	workspace string
}

func newWorkspaceDomainsLsCmd(open workspaceRosterOpen) *cobra.Command {
	o := &workspaceDomainsLsOptions{}
	cmd := &cobra.Command{
		Use:   "ls --workspace <name>",
		Short: "List the domains the workspace admits people from",
		Long: `List the workspace's allowed domains, folded, in one line.

An empty list is said rather than printed as nothing: a workspace that lists no
domain admits nobody without --external, which is the same rule read from the
other side. The command reads and writes nothing and may run in a coding agent's
session.

The machine-readable form of this list is the ` + "`domains`" + ` member of
` + "`fugaro workspace user ls --json`" + ` (schemas/workspace-users.schema.json),
which is the workspace's one published document.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runWorkspaceDomainsLs(cmd, open, o) },
	}
	cmd.Flags().StringVar(&o.workspace, "workspace", "", "the workspace whose allowed domains these are (required: the list belongs to one workspace)")
	return cmd
}

// workspaceDomain folds one domain value and refuses what cannot be a domain at
// all: whitespace, a control byte, a non-ASCII byte and the empty string, which
// are the values C1's own fold refuses in the domain half of an address
// (CanonicalAddress).
//
// What it does NOT do is restate C1's domain grammar (the labels, the hyphens,
// the length): the roster node's own reader holds the list to it (Roster.Validate
// → isDomain), and the write path of every command here encodes the roster, so a
// spelling this fold lets through is refused by the node's reader with the
// sentence that names the rule. One grammar, in one place, read from both sides.
func workspaceDomain(value string) (string, error) {
	domain := strings.ToLower(value)
	if domain == "" {
		return "", userErr("the domain is empty: an allowed domain is the part of an address after its one %q (D2812)", "@")
	}
	for i := 0; i < len(domain); i++ {
		if domain[i] < 0x21 || domain[i] > 0x7e {
			return "", userErr("%q is not a domain: it carries %#x at offset %d, and whitespace, a control byte, DEL and every non-ASCII byte are refused, because a domain is compared against the domain of an address and the two have to be spelled the same way", value, value[i], i)
		}
	}
	if len(domain) > workspace.MaxDomainBytes {
		return "", userErr("%q is not a domain: it is %d bytes, and nothing longer than %d can be one", value, len(domain), workspace.MaxDomainBytes)
	}
	return domain, nil
}

// workspaceDomainsJudgedAdd is one domain added as a pure function of the roster
// the node holds.
func workspaceDomainsJudgedAdd(roster *workspace.Roster, value, domain string) (*workspace.Roster, error) {
	if slices.Contains(roster.Domains, domain) {
		return nil, userErr("%s is already one of this workspace's allowed domains, so every address of it is admitted without --external. `fugaro workspace domains rm %s` takes it off the list, and what a member may do is not changed by this list either way", domain, domain)
	}
	next := *roster
	next.People = roster.People
	next.Admins = roster.Admins
	next.Grants = roster.Grants
	next.Domains = append(append([]string{}, roster.Domains...), domain)
	slices.Sort(next.Domains)
	// The node's own reader is what holds the list to the domain grammar, and the
	// error it answers names the value: the refusal is the rule's sentence rather
	// than a second copy of it here.
	if err := next.Validate(); err != nil {
		return nil, userErr("--domain %q: %v", value, err)
	}
	return &next, nil
}

// workspaceDomainsJudgedRm is one domain removed as a pure function of the roster
// the node holds, with the refusal a list may not take: a member whose address
// sits in the domain being removed (the header says why).
func workspaceDomainsJudgedRm(roster *workspace.Roster, domain string) (*workspace.Roster, error) {
	if !slices.Contains(roster.Domains, domain) {
		return nil, userErr("%s is not one of this workspace's allowed domains (%s), so there is nothing to remove. `fugaro workspace domains ls` lists the ones it holds", domain, workspaceDomainsWord(roster.Domains))
	}
	for _, p := range roster.People {
		if workspaceDomainOf(p.Address) != domain {
			continue
		}
		return nil, userErr("%s is the domain of %s, who is a member of this workspace: whether a member was admitted from outside is computed from this list (D2812), so removing it would report them as a difference the drift check calls tampering. `fugaro workspace user rm %s` takes them off the roster first, and the domain can go after that",
			domain, safetext.OneLine(p.Address), safetext.OneLine(p.Address))
	}
	next := *roster
	next.People = roster.People
	next.Admins = roster.Admins
	next.Grants = roster.Grants
	next.Domains = make([]string, 0, len(roster.Domains))
	for _, d := range roster.Domains {
		if d != domain {
			next.Domains = append(next.Domains, d)
		}
	}
	return &next, nil
}

// runWorkspaceDomainsAdd is add's body.
func runWorkspaceDomainsAdd(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceDomainsAddOptions, arg string) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	if err := d.refuseAgent(fmt.Sprintf("nothing was written: `fugaro workspace domains add %s --workspace %s` edits the workspace's allowed domains, which is a workspace admin's act", safetext.Field(arg), safetext.Field(o.workspace))); err != nil {
		return err
	}
	domain, err := workspaceDomain(arg)
	if err != nil {
		return err
	}
	var actor string
	next, err := d.changeRoster(ctx, func(roster *workspace.Roster) (*workspace.Roster, error) {
		caller, err := d.admin(ctx, roster)
		if err != nil {
			return nil, err
		}
		judged, err := workspaceDomainsJudgedAdd(roster, arg, domain)
		if err != nil {
			return nil, err
		}
		actor = caller
		return judged, nil
	})
	if err != nil {
		return err
	}
	if err := d.file(ctx, actor, workspace.ActionDomainAdd, "", domain, ""); err != nil {
		return err
	}
	return workspaceDomainsSay(cmd.OutOrStdout(), "added", domain, d.Workspace, len(next.Domains))
}

// runWorkspaceDomainsRm is rm's body.
func runWorkspaceDomainsRm(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceDomainsRmOptions, arg string) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	if err := d.refuseAgent(fmt.Sprintf("nothing was written: `fugaro workspace domains rm %s --workspace %s` edits the workspace's allowed domains, which is a workspace admin's act", safetext.Field(arg), safetext.Field(o.workspace))); err != nil {
		return err
	}
	domain, err := workspaceDomain(arg)
	if err != nil {
		return err
	}
	var actor string
	next, err := d.changeRoster(ctx, func(roster *workspace.Roster) (*workspace.Roster, error) {
		caller, err := d.admin(ctx, roster)
		if err != nil {
			return nil, err
		}
		judged, err := workspaceDomainsJudgedRm(roster, domain)
		if err != nil {
			return nil, err
		}
		actor = caller
		return judged, nil
	})
	if err != nil {
		return err
	}
	if err := d.file(ctx, actor, workspace.ActionDomainRemove, "", domain, ""); err != nil {
		return err
	}
	return workspaceDomainsSay(cmd.OutOrStdout(), "removed", domain, d.Workspace, len(next.Domains))
}

// workspaceDomainsSay prints one edit: what was done to which domain, and how
// many are left. It asks nothing.
func workspaceDomainsSay(w io.Writer, verb, domain, workspaceName string, left int) error {
	_, err := fmt.Fprintf(w, "%s %s from workspace %s's allowed domains: %d left\n", verb, safetext.OneLine(domain), safetext.Field(workspaceName), left)
	return err
}

// runWorkspaceDomainsLs is ls's body: it reads the roster node and prints the
// list, and it neither writes nor gates (there is nothing here an outsider
// could not read off the workspace's own record, and the listing is not a
// credential).
func runWorkspaceDomainsLs(cmd *cobra.Command, open workspaceRosterOpen, o *workspaceDomainsLsOptions) error {
	ctx := cmd.Context()
	d, err := workspaceRosterCheck(ctx, open, o.workspace)
	if err != nil {
		return err
	}
	roster, _, err := d.readRoster(ctx)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if len(roster.Domains) == 0 {
		_, err = fmt.Fprintf(w, "workspace %s: no allowed domain, so an address from anywhere is admitted only with --external (D2812)\n", safetext.Field(d.Workspace))
		return err
	}
	_, err = fmt.Fprintf(w, "workspace %s: allowed domains: %s\n", safetext.Field(d.Workspace), safetext.OneLine(strings.Join(roster.Domains, ", ")))
	return err
}
