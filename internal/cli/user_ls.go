package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro user ls is the roster: every stored pair of the installation's grants
// node, the repositories each pattern matches among those this machine knows,
// and — with --repo — the role each person holds in one repository.
//
// It reads the node over IAM, which is the one reader every listed person has
// (D1585: the node is under config/admin, so no client token reaches it, and a
// `roles/firebasedatabase.viewer` holder reads it whatever the rules say). It
// writes nothing and it grants nothing.
//
// What it deliberately does not claim is completeness (F27): a pattern's
// matches are the repositories THIS MACHINE knows — the ones its own config
// lists — so they are labelled "known" and are never offered as every
// repository the pattern admits. The policy's allow_repos may hold patterns
// nothing can enumerate, and Fugaro has no repository directory to ask.
//
// The members this machine's own config carries that a grant cannot hold —
// group:, domain:, serviceAccount: — are listed as legacy, which is what F25
// makes of them: they keep the cloud grant RS3's derive carries into the
// installation's IAM lists, and the resolver ranks none of them, because the
// CLI cannot read a caller's groups.

type userLsOptions struct {
	cloudOptions
	repo   string
	me     bool
	asJSON bool
}

// whoamiDocVersion is `fugaro user ls --me --json`'s document version, and it
// is 1: the document is the console's own read (the `whoami` operation of
// docs/console-improvements/implementation-plan.md, AL), so its shape is
// published from its first release and a member added from here on is an
// addition a version-1 reader reads as it did (CLAUDE.md, "a field never
// changes meaning").
const whoamiDocVersion = 1

// whoamiDoc is `fugaro user ls --me --json`: the caller's own role, the scope
// that role is held at, and the patterns that scope names.
//
// It carries no member: the document is about the caller and nobody else, and
// the reader asking who they are is the caller. Every field is always written —
// a role of `none` is a fact and an absent member is not — and `repos` is an
// array seeded empty, never null (the console's own availability rule for a
// JSON document).
//
// The two vocabularies are the console's (D2907, the plan's own words): `role`
// is one of viewer, operator and admin, the closed set internal/access ranks,
// or `none`; `scope` is one of `project`, `all` and `repos` — the console
// spells all-repos `all` — or `none` for a caller holding nothing. The human
// sentence beside this document keeps the CLI's own spelling of a scope, so a
// person who reads it can copy it into `fugaro user grant --scope`.
type whoamiDoc struct {
	Version int      `json:"version"`
	Role    string   `json:"role"`
	Scope   string   `json:"scope"`
	Repos   []string `json:"repos"`
}

// The words whoamiDoc publishes. whoamiRoleNone is what an unknown or absent
// role reads as, which is the rule the console applies to a word it does not
// know: nothing this CLI writes is outside this set.
const (
	whoamiRoleNone     = "none"
	whoamiScopeNone    = "none"
	whoamiScopeProject = "project"
	whoamiScopeAll     = "all"
	whoamiScopeRepos   = "repos"
)

// userListDocVersion is `fugaro user ls --json`'s document version. Version 1
// carried the two lists under the keys the roles were spelled with before this
// release — `launchers` for the run-starters and `operators` for the admins —
// and both keys are gone: this document's names (`run_operators`, `admins`) are
// names it never carried, so no list of version 1 is published under a name a
// version-1 reader already reads, and the two removals are what the version is
// for (CLAUDE.md, "a field never changes meaning"). Version 2 is that shape.
//
// Version 2 is also RS5's scoped document, and it stays version 2 because every
// field RS5 adds is a NEW name — `grants`, `effective`, `legacy` — and none of
// them retypes a field a version-2 reader already reads: a document with more
// fields in it is one every version-2 reader reads as it did, which is what
// "adding a field is fine" means (CLAUDE.md), and there is nothing to announce.
// The `grants` name is the node's own (access's nodeDoc chooses it), so the
// node and this document spell one thing one way.
const userListDocVersion = 2

// userLsDoc is `fugaro user ls --json`. `run_operators` and `admins` are THIS
// MACHINE's own config lists under the names version 2 gave them — the lists its
// next `fugaro update` derives from the node (RS3), which is what `fugaro user
// add` and `rm` still write and what the listing prints when an installation has
// no grants node at all. Their meaning is unchanged, which is why they are not
// renamed here.
//
// Every array is seeded empty and never null: "there are none" is a fact and a
// null is not (the console's own availability rule for a JSON document).
type userLsDoc struct {
	Version      int      `json:"version"`
	Project      string   `json:"project"`
	GCPProject   string   `json:"gcp_project"`
	RunOperators []string `json:"run_operators"`
	Admins       []string `json:"admins"`
	// Grants are the stored pairs of the grants node, in the order it holds
	// them. It is the roster, and it is empty when nothing is stored — an
	// installation that has not migrated its lists and one whose lists name no
	// grant are one state (access.WriteIfMatch).
	Grants []userLsGrant `json:"grants"`
	// Effective is the role each member holds in one repository, and it is
	// filled only by --repo: without one there is no repository to answer
	// about, and an empty list says so rather than answering about a
	// repository nobody named.
	Effective []userLsEffective `json:"effective"`
	// Legacy are this machine's group:, domain: and serviceAccount: members
	// (F25): they keep the IAM grant the installation derives, and the
	// resolver ranks none of them.
	Legacy []userLsLegacyMember `json:"legacy"`
}

// userLsGrant is one stored pair, with the repositories its scope matches among
// those this machine knows. `known` is never a claim of completeness (F27): the
// machine's own list is all it can enumerate.
type userLsGrant struct {
	User  string   `json:"user"`
	Role  string   `json:"role"`
	Scope string   `json:"scope"`
	Known []string `json:"known"`
}

// userLsEffective is the role one member holds in one repository.
type userLsEffective struct {
	User string `json:"user"`
	Repo string `json:"repo"`
	Role string `json:"role"`
}

// userLsLegacyMember is one member of this machine's own lists that a grant
// cannot hold, with the role word of the list it sits on.
type userLsLegacyMember struct {
	Member string `json:"member"`
	Role   string `json:"role"`
}

// The values the roster is printed from are NOT run through
// pluginwire.Printable, and the reason is the reader that produced them:
// access.Decode refuses a member that is not access.Member's output (printable
// ASCII, one "@") and a scope that is not ParseScope's canonical grammar, so a
// member or a scope that reached this file through a read is a line of plain
// text already. Escaping one would only cut a legitimate long scope — a node
// may store many patterns — and rewrite the escapes of a value it did not
// touch. This machine's own lists are the other case, and they are escaped
// where they are printed: they are hand-editable files.

// newUserLsCmd is `fugaro user ls [--repo R] [--json]`.
func newUserLsCmd() *cobra.Command {
	o := &userLsOptions{}
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the installation's grants, and the role held in a repository",
		Long: `List the installation's grants: every stored role and scope pair, from the
grants node at ` + accesslib.PathNode + `.

Each pair is a person, a role and the scope the role is held at, and a pattern's
line names the repositories it matches among those this machine knows — its own
config's list — labelled "known": the answer is deliberately about this machine,
because a pattern may admit repositories nothing here can enumerate and Fugaro
has no directory to ask. Nothing here claims to be every repository a pattern
admits.

--repo names one repository and prints the role each person holds in it: the
highest role whose scope covers the repository, with the published policy's
allow_repos taken into account (a repository the policy does not admit cannot
run at all, so nobody holds a role in it). A repository outside allow_repos is
reported as such rather than as a role of nobody.

Legacy members are listed as legacy: a group:, domain: or serviceAccount:
member in this machine's own config keeps the IAM grant the installation
derives, and the resolver ranks none of them, because the CLI cannot read a
caller's groups.

--me prints your own role and the scope it is held at, and nothing about anybody
else. It reads the same grants node and the same resolver the commands gate on:
the role is the highest one a stored grant names you with, an implicit project
admin reads admin at project scope, and a caller no stored grant names reads
none. It is a read of your own pair alone, so unlike the grant and revoke
subcommands a coding agent's session may run it.

The command reads the grants node and the published policy with your own
credentials and writes nothing. An installation with no Firebase budget backend
has no grants node at all: its roles are still this machine's own
terraform.operators and terraform.admins, and this command prints those and
says so — but --me has no roster to read your own role from and refuses rather
than guess at one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return userLs(cmd, o) },
	}
	addCloudFlags(cmd, &o.cloudOptions)
	cmd.Flags().StringVar(&o.repo, "repo", "", "the repository to print the effective role in (provider/owner/name, or owner/name for a repository this machine's config lists)")
	cmd.Flags().BoolVar(&o.me, "me", false, "print the caller's own role and the scope it is held at")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	return cmd
}

// userLs is ls's body.
func userLs(cmd *cobra.Command, o *userLsOptions) error {
	ctx := cmd.Context()
	// A coding agent may run ls, and selection on a machine with no project
	// config of its own fetches the shared config from the runs bucket with the
	// person's credentials: GODEBUG=http2debug would print them to stderr.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if o.me && o.repo != "" {
		return userErr("--me and --repo cannot be combined: --me prints the caller's own role, and --repo prints the role every member holds in one repository. Run one of them")
	}
	sel, lc, err := selectProject(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	if o.me {
		return userLsMe(cmd, o, lc)
	}
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return userLsNoBackend(cmd, o, sel, lc)
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	node, err := accesslib.Read(ctx, db)
	if err != nil {
		return dbErr(lc, err, false)
	}
	repo := ""
	if o.repo != "" {
		if repo, err = userLsRepo(lc, o.repo); err != nil {
			return err
		}
	}
	allow, err := userLsAllowRepos(ctx, lc)
	if err != nil {
		return err
	}
	known := userKnownRepos(lc)
	doc := userLsDoc{
		Version:      userListDocVersion,
		Project:      lc.Name,
		GCPProject:   lc.GCPProject,
		Grants:       userLsGrants(node.Grants, known),
		Effective:    []userLsEffective{},
		Legacy:       userLsLegacy(lc),
		RunOperators: userMembers(lc.Terraform.Operators),
		Admins:       userMembers(lc.Terraform.Admins),
	}
	if repo != "" {
		doc.effectiveIn(lc, node.Grants, allow, repo)
	}
	if !o.asJSON {
		return userLsPrint(cmd.OutOrStdout(), lc, node, doc, repo, allow)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// userLsRepo is --repo as a canonical repository: one repository, and never a
// pattern. A pattern would be an "effective role" over a set, which is not a
// question anybody asked and not one the resolver answers.
func userLsRepo(lc *localcfg.Config, arg string) (string, error) {
	if strings.Contains(arg, "*") {
		return "", userErr("--repo %q is a pattern: --repo names one repository, whose effective role is the role a person holds in it. Read a pattern's matches off the grant's own line instead, or pass a concrete repository", safetext.Field(arg))
	}
	return canonicalRepoArg(lc, arg)
}

// userLsAllowRepos is the published policy's allow_repos, or an allow-list that
// admits everything when no policy is published: an installation that has
// published nothing has no allow-list to check, and reading that as an empty
// one would refuse every repository (the rule the launch gate makes, and it is
// the same one — a repository outside allow_repos cannot run, so nobody holds a
// role in it).
func userLsAllowRepos(ctx context.Context, lc *localcfg.Config) ([]string, error) {
	p, found, err := projectPolicyRead(ctx, lc)
	if err != nil {
		return nil, policyReadErr(err)
	}
	if !found {
		return []string{"*"}, nil
	}
	return p.AllowRepos, nil
}

// userKnownRepos is the repositories this machine knows, canonically: the ones
// its own config lists, with the provider each entry carries. A config entry
// with no provider names no canonical repository — the checkout's fugaro.yaml
// decides it at run time — so it is left out rather than guessed at (F27's
// "among those known to this machine", and the label's own honesty).
func userKnownRepos(lc *localcfg.Config) []string {
	out := []string{}
	for name, r := range lc.Repos {
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// userLsGrants is the document's grants: the node's pairs, in the order they
// are stored, each with the repositories its scope matches among the known
// ones.
func userLsGrants(grants []accesslib.Grant, known []string) []userLsGrant {
	out := make([]userLsGrant, 0, len(grants))
	for _, g := range grants {
		matches := accesslib.KnownMatches(g.Scope, known)
		if matches == nil {
			matches = []string{}
		}
		out = append(out, userLsGrant{
			User:  g.User,
			Role:  string(g.Role),
			Scope: g.Scope.String(),
			Known: matches,
		})
	}
	return out
}

// userLsLegacy is this machine's own group:, domain: and serviceAccount:
// members, with the list each sits on (F25).
func userLsLegacy(lc *localcfg.Config) []userLsLegacyMember {
	out := []userLsLegacyMember{}
	for _, l := range []struct {
		role    string
		members []string
	}{
		{"operator", legacyMembers(lc.Terraform.Operators)},
		{"admin", legacyMembers(lc.Terraform.Admins)},
	} {
		for _, m := range l.members {
			out = append(out, userLsLegacyMember{Member: pluginwire.Printable(m), Role: l.role})
		}
	}
	return out
}

// effectiveIn fills the document's effective list: the role every member this
// command knows holds in repo — the node's members, and the implicit admins its
// own config names — for the ones that hold a role at all. A member with no
// role in that repository is left out rather than listed with an empty role: a
// list of every member with "none" is noise, and the roster above already names
// them.
func (d *userLsDoc) effectiveIn(lc *localcfg.Config, grants []accesslib.Grant, allow []string, repo string) {
	in := accesslib.Input{Grants: grants, Implicit: userImplicitAdmins(lc), AllowRepos: allow}
	for _, user := range userLsMembers(grants, in.Implicit) {
		role := accesslib.Effective(in, user, repo)
		if role == accesslib.RoleNone {
			continue
		}
		d.Effective = append(d.Effective, userLsEffective{
			User: user, Repo: repo, Role: string(role),
		})
	}
	slices.SortFunc(d.Effective, func(a, b userLsEffective) int { return strings.Compare(a.User, b.User) })
}

// userLsMembers is every member this command can place: the node's, and the
// implicit admins of this machine's config, each once, sorted.
func userLsMembers(grants []accesslib.Grant, implicit []string) []string {
	out := make([]string, 0, len(grants)+len(implicit))
	for _, g := range grants {
		out = append(out, g.User)
	}
	out = append(out, implicit...)
	slices.Sort(out)
	return slices.Compact(out)
}

// userLsPrint prints the human listing: the grants, then the legacy members,
// then — with --repo — the effective role in it. allow is the published
// allow_repos the effective role was resolved under, and it is read here
// because a repository it does not admit is one the listing has to name as
// such: the answer is then nobody's for a reason no grant can change, which is
// not the sentence a repository nobody's scope covers gets.
func userLsPrint(w io.Writer, lc *localcfg.Config, node accesslib.Node, doc userLsDoc, repo string, allow []string) error {
	if !node.Found {
		// An absent node is both "this installation has not migrated its lists"
		// and "its lists name no grant" — the two are one state in the database
		// (access.Node) — and neither is "everyone".
		fmt.Fprintf(w, "project %s has no grants node at %s: no role is stored, so the installation's roles are what its own lists hold. Its next fugaro update migrates terraform.operators and terraform.admins into the node\n",
			safetext.Field(lc.Name), accesslib.PathNode)
		// The roles are then exactly these two lists, and they are what --json
		// carries under run_operators and admins: a person reading who holds what
		// has to see them, and a sentence naming a file they would have to open
		// themselves is not a roster (this is the listing `fugaro user ls`
		// printed before the node existed, and what it still prints with no
		// backend at all). The names this machine's own lists carry that a grant
		// cannot hold are listed again below, where they are marked as the legacy
		// members F25 makes of them — the two lists alone do not say the CLI
		// cannot rank one.
		if err := printUserLists(w, lc); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "grants of project %s (%d), from %s:\n", safetext.Field(lc.Name), len(doc.Grants), accesslib.PathNode)
		for _, g := range doc.Grants {
			fmt.Fprintf(w, "  %s (%s) at %s", g.User, g.Role, g.Scope)
			if len(g.Known) > 0 {
				// "known" is the label F27 requires: the answer is this
				// machine's list, never every repository the pattern admits.
				fmt.Fprintf(w, " — known: %s", strings.Join(g.Known, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	if len(doc.Legacy) > 0 {
		fmt.Fprintf(w, "legacy (IAM-only) members of this machine's own lists, which the CLI cannot rank (F25):\n")
		for _, m := range doc.Legacy {
			fmt.Fprintf(w, "  %s (%s)\n", m.Member, m.Role)
		}
	}
	if repo == "" {
		return nil
	}
	fmt.Fprintf(w, "the effective role in %s:\n", repo)
	// The published allow_repos gates this answer before any grant is read
	// (access.Effective), so a repository it does not admit is reported as the
	// policy's own refusal rather than as a role of nobody: a stored grant's
	// scope may well cover it, and "no stored grant covers it" would be false of
	// exactly that pair. It is also the one place the help text's promise — a
	// repository outside allow_repos is reported as such — is kept.
	if !projectpolicy.AdmitRepo(allow, repo) {
		fmt.Fprintf(w, "  nobody holds a role in %s, and no grant can: %s\n", repo, projectpolicy.NotAdmitted(allow, repo))
		return nil
	}
	if len(doc.Effective) == 0 {
		fmt.Fprintf(w, "  nobody holds a role in %s: no stored grant covers it, and no implicit admin is named here\n", repo)
		return nil
	}
	for _, e := range doc.Effective {
		fmt.Fprintf(w, "  %s: %s\n", e.User, e.Role)
	}
	return nil
}

// userLsNoBackend is what ls says (and prints) on an installation with no
// Firebase budget backend: there is no grants node to read, so the roles are
// still the two IAM lists of this machine's own config, which is what RS3's
// own step says (accessNoBackend) and what an operator reading the roles has to
// be told. The listing is the one this command printed before the node existed.
func userLsNoBackend(cmd *cobra.Command, o *userLsOptions, sel localcfg.Selection, lc *localcfg.Config) error {
	if o.repo != "" {
		return userErr("--repo %s needs the grants node and the published policy, and project %s has no Firebase budget backend (%s): an operator runs fugaro update --firebase-project %s --name %s, and the update after that migrates this machine's lists into the node",
			safetext.Field(o.repo), safetext.Field(lc.Name), accessNoBackend, firebaseProjectIDWord(lc.GCPProject), lc.Name)
	}
	if err := userLocalConfig(sel, lc); err != nil {
		return err
	}
	if o.asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(userLsDoc{
			Version: userListDocVersion, Project: lc.Name, GCPProject: lc.GCPProject,
			Grants: []userLsGrant{}, Effective: []userLsEffective{},
			Legacy:       userLsLegacy(lc),
			RunOperators: userMembers(lc.Terraform.Operators), Admins: userMembers(lc.Terraform.Admins),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", accessNoBackend)
	return printUserLists(cmd.OutOrStdout(), lc)
}

// userLsMe is `--me`: the caller's own role, and the scope it is held at
// (D2907). It reads nothing about anybody else, and it is deliberately the
// gate's own path: accessJudge builds the same Input (the grants node's pairs
// and this machine's implicit admins) and reads the same caller (the
// credential, never the config's `user:` key, F21) that accessGateProject and
// accessGateRepo judge with, so the answer a console disables a control on is
// the answer the CLI would refuse the write by.
//
// It reads no repository, so the published allow_repos is not read with it
// (accessJudge's repoLevel): whether a repository is admissible is the policy's
// answer at the moment of a check, and this read is not about one.
func userLsMe(cmd *cobra.Command, o *userLsOptions, lc *localcfg.Config) error {
	judged, err := accessJudge(cmd.Context(), lc, nil, false, "your own role is not read")
	if err != nil {
		return err
	}
	if judged == nil {
		return userLsMeNoRoster(lc)
	}
	role, scope := accesslib.Self(judged.in, judged.caller)
	if !o.asJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", userLsMeSentence(judged.caller, lc, role, scope))
		return nil
	}
	doc := whoamiDoc{
		Version: whoamiDocVersion,
		Role:    userLsMeRoleWord(role),
		Scope:   userLsMeScopeWord(scope),
		Repos:   userLsMeRepos(scope),
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// userLsMeRoleWord is role as whoamiDoc spells it: the resolver's own word for
// the three roles it ranks, and `none` for the empty role — the word a reader
// that does not know a role reads it as.
func userLsMeRoleWord(role accesslib.Role) string {
	if role == accesslib.RoleNone {
		return whoamiRoleNone
	}
	return string(role)
}

// userLsMeScopeWord is scope's kind as whoamiDoc spells it: the console's three
// words for the three kinds that cover something, and `none` for the zero scope
// a caller holding no role has.
func userLsMeScopeWord(scope accesslib.Scope) string {
	switch scope.Kind {
	case accesslib.ScopeProject:
		return whoamiScopeProject
	case accesslib.ScopeAllRepos:
		return whoamiScopeAll
	case accesslib.ScopeRepos:
		return whoamiScopeRepos
	}
	return whoamiScopeNone
}

// userLsMeRepos is the patterns the scope names, and an empty list for every
// kind that names none: the document's arrays are seeded rather than null, so
// "this scope names no pattern" is a fact a reader reads without a case for it.
func userLsMeRepos(scope accesslib.Scope) []string {
	if scope.Kind != accesslib.ScopeRepos {
		return []string{}
	}
	return scope.Repos
}

// userLsMeSentence is --me without --json: one sentence, naming the credential
// the role was read from and the pair the resolver placed it at, and never a
// question. The scope is spelled the way the CLI's own scope grammar spells it,
// so a person who reads it can copy it into `fugaro user grant --scope` — which
// is why this sentence does not use whoamiDoc's `all` for all-repos.
func userLsMeSentence(caller string, lc *localcfg.Config, role accesslib.Role, scope accesslib.Scope) string {
	if role == accesslib.RoleNone {
		return fmt.Sprintf("you (%s) hold no role in project %s: no stored grant names you, and you are not one of its implicit admins",
			safetext.Field(caller), safetext.Field(lc.Name))
	}
	where := scope.String()
	if scope.Kind == accesslib.ScopeProject {
		where = "project scope"
	}
	return fmt.Sprintf("you (%s) hold %s at %s in project %s",
		safetext.Field(caller), role, safetext.Field(where), safetext.Field(lc.Name))
}

// userLsMeNoRoster is --me's refusal on an installation with no roster to read
// the caller's own role from: no Firebase budget backend at all, or one whose
// grants node has never been written. Both are states where the gates make no
// judgement either (accessJudge answers nil and the commands behave as they did
// before the roles existed), so answering `none` would be a role claim no rule
// makes and would disable controls the CLI still allows. The sentence names the
// state and the one command that ends it, and it asks nothing.
func userLsMeNoRoster(lc *localcfg.Config) error {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return userErr("--me reads your own role from the grants node at %s, and project %s has no Firebase budget backend (%s): an operator runs fugaro update --firebase-project %s --name %s, and the update after that migrates this machine's lists into the node",
			accesslib.PathNode, safetext.Field(lc.Name), accessNoBackend, firebaseProjectIDWord(lc.GCPProject), lc.Name)
	}
	return userErr("--me reads your own role from the grants node at %s, and project %s has none: no role is stored there, and none of this machine's own lists can be read as one (they are text a person edits, finding F21). Its next fugaro update migrates terraform.operators and terraform.admins into the node, and this read answers from then on",
		accesslib.PathNode, safetext.Field(lc.Name))
}
