package cli

// workspace_project.go is WU5: `fugaro workspace project add`, the workspace
// project's owner declaring one project a member from the machine that holds the
// member table (D2802, D2821).
//
// It carries `workspace project add`'s declaration plus the two things the owner's
// half owes once the member's own owner has consented: the member's grantees
// become people of the workspace (D2805: adopt, never strip), and each workspace
// admin who also holds the live binding gets their stored grant written into the
// member's own roster, marked in the workspace node (D2818, D2823), with the
// member list mirrored into the member's database (D2813).
//
// Nothing here is the member owner's act. Linking is theirs, on their own
// machine, from their own credentials (D2800, D2819), so a member the workspace
// does not hold is DECLARED in the owner's table and told to link: this command
// writes no authority and reaches no IAM (A6).
//
// The writes run in the order the record and the members are compared in, so
// that a failure between them leaves drift a re-run finishes rather than a grant
// nobody recorded: the local table entry first (it is `member add`'s own write,
// and the fence a coding agent's session is refused at is in it), then the
// workspace's roster, then the member's mirror and its roster, and the audit
// event last, because the trail records a change that has happened and never one
// that was about to (C1's own rule for those two writes).
//
// The two lists this command keeps in step are different lists of the same
// workspace. The workspace node's People is who the workspace admits, and an
// adopted grantee joins it. The node's Grants is D2818's MARK -- what the
// workspace itself wrote into a member -- and adoption writes nothing there: a
// grant the member's own admins made is the member's own grant, which is exactly
// what D2822 says the mark exists to tell apart. So the mark holds the admins'
// grants alone, and the member's own grants are left exactly where they were
// (D2805).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
	"github.com/spf13/cobra"
)

// workspaceProjectAddSeams is how this command reaches the cloud: the
// workspace's own roster and audit trail (C1), the member it declares and the
// mechanism that holds it (C2), the credential's own address, and the clock and
// randomness an audit event id is minted from.
//
// They are function fields because each is opened with the workspace and the
// member the flags named, which only the command knows once it has parsed them.
// I1's wiring fills every field and registers the command; a test fills them with
// its own fakes, which is what keeps every test here hermetic. A nil field is a
// command wired without one, and the seam it stands for refuses rather than
// panicking.
type workspaceProjectAddSeams struct {
	store     func(ctx context.Context, ws string) (workspace.Store, error)
	audit     func(ctx context.Context, ws string) (workspace.AuditSink, error)
	authority func(ctx context.Context, ws string) (workspace.Authority, error)
	// member opens the member's own database. lc is the WORKSPACE's own config
	// -- the one whose project the member table is in -- because the writer it
	// returns reaches the member's project with the workspace owner's
	// credentials, which is what D2821 makes this command exactly the act of
	// that machine. region is where the member's Realtime Database lives, as
	// the owner's own table declares it (--database-region): C2's MemberTarget
	// names the member by its two projects and cannot carry a third fact, so
	// the region travels beside it.
	member func(ctx context.Context, lc *localcfg.Config, m workspace.MemberTarget, region string) (workspace.MemberOps, error)
	caller func(ctx context.Context) (string, error)
	now    func() time.Time
	rand   io.Reader
}

// workspaceProjectAddAttempts bounds every read-judge-write loop below. Four is
// the number the grants commands use for the same reason: a node two writers are
// racing is settled in one or two rounds, and a fifth round would be a fight
// rather than a judgement (D2817).
const workspaceProjectAddAttempts = 4

// workspaceMirrorVersion is the version of the member's mirror document (D2813):
// the version this command writes into a member's own database. The entries are
// C1's MirrorEntry, whose three keys the contract fixes; the document around them
// is C2's and C2 declares no type for it, so WU5 -- the first writer of one --
// spells the document and its version in workspaceMirrorDocument below.
const workspaceMirrorVersion = 1

type workspaceProjectAddOptions struct {
	workspace string
	// external is --external (D2812): the member's grantees outside the
	// workspace's allowed domains are adopted, marked external, only when it is
	// given. Nothing asks for it (D1540); its absence is a refusal that names it.
	external bool
	// member is the set of values this command
	// carries unchanged (D2802): one entry, built from the same flags by the same
	// validation, written in the same table.
	member workspaceMemberAddOptions
}

func newWorkspaceProjectAddCmd(seams workspaceProjectAddSeams) *cobra.Command {
	var o workspaceProjectAddOptions
	cmd := &cobra.Command{
		Use:   "add <project> --workspace <name>",
		Short: "Declare a member in the workspace's table and adopt it once it is linked",
		Long: `Write one member entry into the workspace's own project config, and, once the
member's own owner has linked their project, write the workspace's side into it.

The entry is the one fugaro workspace project add writes, built from the same
flags: the member's job service accounts with the repositories they are a job of,
its Firebase project and fp, its exchange URL, its database region and the
repositories it contributes. --workspace names the workspace whose config holds
the table, which is the project the workspace is named after, on the machine that
holds it (D2821); the owner's own fugaro update publishes the record the table
builds.

What the add does besides the entry depends on whether the workspace already
holds the member, and holding it is the member owner's own act (fugaro workspace
project link, D2819):

  - a member the workspace holds is adopted. Its grantees become people of the
    workspace's roster, and nothing of the member's own grants is touched: a
    grant that was there before the workspace arrived is the member's own
    (D2805). A grantee whose domain is not one of the workspace's allowed
    domains is refused unless --external is given, and is marked external then
    (D2812). Each workspace admin who also holds the live binding gets one
    stored grant of admin at project scope written into the member's own roster
    and marked in the workspace's node (D2818, D2823), and the workspace's member
    list is mirrored into the member's own database (D2813).

  - a member the workspace does not hold is declared and nothing is written into
    it: the member's own owner runs fugaro workspace project link on their own
    machine, from their own credentials, and this command finishes the add
    afterwards.

A member the workspace holds whose database already carries another workspace's
member list is refused: a project belongs to one workspace (D1291, R2, R14). The
two documents that reading is made from -- the member's mirror and the parent it
hangs under -- are readable once the workspace holds the member (R14), so a
project this workspace does not hold is refused by fugaro workspace project link,
on the member owner's own machine, and not here. Nothing is ever removed from a
member by this command.

Only the keys it writes are rewritten: every comment and every other key of the
config is kept, except inside the entry it replaces, which is written fresh.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceProjectAdd(cmd, args[0], &o, seams)
		},
	}
	// The two keys a member entry must never carry, answered by name rather than
	// by pflag's bare "unknown flag", in `member add`'s own words: this command
	// carries that command's flags unchanged, refusals included.
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
	f.StringVar(&o.workspace, "workspace", "", "the workspace this project is added to (required: a project belongs to one workspace, D1291)")
	f.BoolVar(&o.external, "external", false, "adopt the member's grantees outside the workspace's allowed domains, marking them external (D2812)")
	f.StringArrayVar(&o.member.jobServiceAccounts, "job-service-account", nil, "a job service account of the member, which the workspace's service resolves a caller by (repeatable)")
	f.StringArrayVar(&o.member.jobRepos, "job-repos", nil, "a repository a job service account is a job of, as <account>=<owner/name> (repeatable; every account needs at least one)")
	f.StringVar(&o.member.databaseRegion, "database-region", "", "the region of the member's Realtime Database; omit it to read it from the member's own budget.rtdb_url (a database in the project's default region has none)")
	f.StringVar(&o.member.firebaseProject, "firebase-project", "", "the member's Firebase project ID, which the caller's token is issued under")
	f.StringVar(&o.member.fp, "fp", "", "the member's Fugaro project name, which the token's fp claim must be")
	f.StringVar(&o.member.exchangeURL, "exchange-url", "", "the member's exchange endpoint, where the workspace's signer mints a member-bound assertion for")
	f.StringArrayVar(&o.member.repos, "repo", nil, "a repository the member contributes to the workspace, as owner/name (repeatable)")
	o.member.noApply = addNoApplyFlag(cmd)
	return cmd
}

// workspaceProjectAdd is one add: the workspace's roster as it was read, the
// member's own documents, and the set of changes the add makes. It is built by
// read, judged and written by the methods below.
type workspaceProjectAdd struct {
	seams   workspaceProjectAddSeams
	options *workspaceProjectAddOptions
	member  workspace.MemberTarget
	lc      *localcfg.Config
	path    string
	// declaredFrom and declaredTo are the table's bytes as the declaration read
	// and wrote them: what the chain below saves and applies.
	declaredFrom, declaredTo []byte
	// roster is the workspace's own node as this add read it: the picture the
	// external gate is judged against, and its mechanism and domains with it.
	roster *workspace.Roster
	// linked is whether the workspace holds the member now (R11): the member's
	// own owner has consented through `link`, so the add adopts into it.
	linked bool
	// mirror is the member's mirror as its database holds it, read only for a
	// member the workspace holds (R14). An entry of it that is not a person of
	// the workspace's own roster is another workspace's (R2).
	mirror workspace.MemberDoc
	// mirrorEntries are that document's entries, as they were read: what the
	// list the member holds now is compared against before this add decides
	// whether it has anything to write there. A member's list that does not
	// name a person the workspace's roster holds is one this add rewrites, so
	// the sentence it prints about the list is true of the list it leaves.
	mirrorEntries []workspace.MirrorEntry
	// adopt are the member's grantees that are not people of the workspace yet,
	// each with the external mark D2812 gives it.
	adopt []workspace.Person
	// admins are the workspace's admins that also hold the live binding (D2823):
	// the ones the add writes a stored grant for and marks.
	admins []string
}

func runWorkspaceProjectAdd(cmd *cobra.Command, project string, o *workspaceProjectAddOptions, seams workspaceProjectAddSeams) error {
	// The fence, before anything is read and for the whole command (A7, threat
	// model §5). It is here and not in the declaration below, because the
	// declaration is only one of this command's writes: once the workspace holds
	// the member, the add writes the workspace's roster, the member's own roster
	// and mirror and the workspace's trail, and `applySettingsEdit`'s own fence
	// stands behind its "no change" answer for an entry the table already
	// carries -- so a replay of the same add reaches every one of those writes
	// in a coding agent's session with nothing in the way.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: fugaro workspace project add declares a member in the workspace's own table and, once the workspace holds it, writes the workspace's people into the member's own roster and mirror with the workspace owner's credentials, so it is run by a person: %s", initflow.AgentRefusal(m))
	}
	if err := workspaceRefuseName(project, "project"); err != nil {
		return err
	}
	switch {
	case o.workspace == "":
		return userErr("--workspace is required: a project belongs to one workspace (D1291), and the workspace's own project is the one whose config holds the member table this command writes (D2821)")
	case o.workspace == project:
		return userErr("%s is the workspace's own project: a workspace is not a member of itself, and its config is where this table lives", o.workspace)
	}
	if err := workspaceMemberFlags(&o.member); err != nil {
		return err
	}
	// D2821's fence, and it is `member add`'s own: the member table lives in the
	// config of the project the workspace is named after, so everything below
	// reaches the workspace's own project from the machine that holds it.
	lc, path, err := workspaceOwnerConfig(o.workspace)
	if err != nil {
		return err
	}
	// --database-region is the flag's when it is given, the member's own
	// database's when this machine holds its config, and the entry's own
	// otherwise: what everything downstream states -- the write, the sentence
	// and the command line -- is the value the entry ends up carrying. It is
	// derived here, where the table's own document has just been read, and not
	// in a helper of its own: the derivation is one call against the entry this
	// command is about to write.
	declared := lc.WorkspaceMembers != nil && !(*lc.WorkspaceMembers)[project].IsZero()
	current := ""
	if declared {
		current = (*lc.WorkspaceMembers)[project].DatabaseRegion
	}
	if o.member.databaseRegion, err = workspaceMemberRegion(project, o.member.firebaseProject, o.member.databaseRegion, current); err != nil {
		return err
	}
	member := workspace.MemberTarget{Project: project, Firebase: o.member.firebaseProject}
	if member.Firebase == member.Project {
		// C2's own reading: Firebase is empty for a member whose Firebase project
		// is its GCP project, and the two spell one project then.
		member.Firebase = ""
	}
	add := &workspaceProjectAdd{seams: seams, options: o, member: member, lc: lc, path: path}
	// Everything is read and judged before anything is written, so an add that is
	// refused -- another workspace's member, a grantee outside the domains without
	// --external -- leaves the machine and the member exactly as it found them.
	if err := add.read(cmd.Context()); err != nil {
		return err
	}
	if err := add.declare(cmd, project); err != nil {
		return err
	}
	if add.linked {
		if err := add.adoptInto(cmd.Context(), cmd, project); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "the member's own owner runs fugaro workspace project link on the member's machine: until they do, this project is in workspace %s's table and nothing of the workspace's is written into it (D2819, R11)\n",
			safetext.Field(o.workspace))
	}
	return workspaceChain(cmd, lc, path, add.declaredFrom, add.declaredTo, workspaceDoor("project add"), workspaceOwnerHalf(), *o.member.noApply)
}

// readRoster is one read of the workspace's own roster node, decoded: the
// picture this add judges against, and every round of the conditional write
// below. It is one function because the two read the same document the same way
// -- a read that FAILED is not an absent node (C1's own rule) and a node nobody
// can decode is refused wherever it was read -- and the caller writes the
// sentence for an absent node itself, because "not written yet" and "removed
// under this writer" are the two states it can be absent in.
func (a *workspaceProjectAdd) readRoster(ctx context.Context, store workspace.Store) (workspace.Revision, *workspace.Roster, bool, error) {
	rev, raw, err := store.Read(ctx)
	if err != nil {
		return rev, nil, false, userErr("reading workspace %s's roster at %s: %v", safetext.Field(a.options.workspace), store.Path(), err)
	}
	if !rev.Found {
		return rev, nil, false, nil
	}
	roster, err := workspace.DecodeRoster(raw)
	if err != nil {
		return rev, nil, false, userErr("workspace %s's roster at %s: %v", safetext.Field(a.options.workspace), store.Path(), err)
	}
	return rev, roster, true, nil
}

// read is everything the add reads before it writes anything: the workspace's
// own roster (C1) and, where the workspace holds the member, the member's own
// database (C2). A member the workspace does not hold is read nowhere but the
// workspace's own node: the authority over a member's project is given at
// `link`, by the member's own owner on their own machine (D2800, D2819), so
// before it the workspace has no grant to read the member's database with --
// R14's own words are that the mirror and the parent folder are readable once
// linked -- and a read there would either fail on a denial or make a caller
// treat one as an absent document, which no contract here allows
// (internal/workspace/rosterstore.go).
func (a *workspaceProjectAdd) read(ctx context.Context) error {
	store, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	_, roster, found, err := a.readRoster(ctx, store)
	if err != nil {
		return err
	}
	if !found {
		return userErr("workspace %s's roster at %s is not there yet, so there is nothing to adopt into: the workspace's own machinery writes the first one, with its mechanism and its first admin (R7). Nothing was written",
			safetext.Field(a.options.workspace), store.Path())
	}
	a.roster = roster

	auth, err := a.openAuthority(ctx)
	if err != nil {
		return err
	}
	if a.linked, err = auth.Holds(ctx, a.member); err != nil {
		return userErr("asking whether workspace %s holds project %s: %v", safetext.Field(a.options.workspace), safetext.Field(a.member.Project), err)
	}
	if !a.linked {
		// Nothing of the member's is read and nothing is decided about it here:
		// R2's "another workspace" is read from the member's mirror and its
		// parent folder, and neither is readable before the workspace holds the
		// project (R14), so the refusal a project in another workspace meets is
		// the authority's own, made when the member's own owner links (C2 Link
		// refuses a member whose project is already another workspace's: WU2 by
		// the folder it sits in, WU18 by the rest). What is left here is the
		// declaration and the sentence that tells the owner to link.
		return nil
	}
	return a.readLinked(ctx, auth)
}

// readLinked is what only a member the workspace holds has to read: its mirror,
// which says whether this project is another workspace's (R2, R14), its own
// roster, which the grantees are adopted from, and the live bindings, which
// decide which of the workspace's admins the workspace may write a grant for
// (D2823).
func (a *workspaceProjectAdd) readLinked(ctx context.Context, auth workspace.Authority) error {
	ops, err := a.openMember(ctx)
	if err != nil {
		return err
	}
	if a.mirror, err = ops.ReadMirror(ctx, a.member); err != nil {
		return userErr("reading project %s's own member list at %s: %v", safetext.Field(a.member.Project), workspace.PathMemberMirror, err)
	}
	entries, err := workspaceProjectMirrorEntries(a.mirror)
	if err != nil {
		return userErr("project %s's own member list at %s: %v. It is the member's own document and this add replaces it with the workspace's own list, so it reads one it can trust or writes nothing",
			safetext.Field(a.member.Project), workspace.PathMemberMirror, err)
	}
	a.mirrorEntries = entries
	if foreign := workspaceProjectAddForeignPeople(a.roster, entries); len(foreign) > 0 {
		return userErr("project %s's own member list at %s names %s, none of which is a person of workspace %s: that list was written by another workspace (R2, R14), and a project belongs to one workspace (D1291), so the workspace it belongs to has to remove it there -- its own owner runs fugaro workspace project rm %s -- before this project is added to this one. Nothing was written",
			safetext.Field(a.member.Project), workspace.PathMemberMirror, workspaceAddressesPhrase(foreign),
			safetext.Field(a.options.workspace), safetext.Field(a.member.Project))
	}
	doc, err := ops.ReadGrants(ctx, a.member)
	if err != nil {
		return userErr("reading project %s's own roster at %s: %v", safetext.Field(a.member.Project), accesslib.PathNode, err)
	}
	if !doc.Found {
		return userErr("project %s's own grants node at %s is not there, so its lists were never migrated and there is nothing to adopt: the member's own fugaro update migrates them, and this command runs once it has. Nothing was written",
			safetext.Field(a.member.Project), accesslib.PathNode)
	}
	grants, err := accesslib.Decode(doc.Raw)
	if err != nil {
		return userErr("project %s's own roster at %s: %v", safetext.Field(a.member.Project), accesslib.PathNode, err)
	}
	bindings, err := auth.AdminBindings(ctx)
	if err != nil {
		return userErr("reading workspace %s's own live admin bindings: %v", safetext.Field(a.options.workspace), err)
	}
	// Only an entry of the node's Admins that also holds a live binding is
	// propagated (D2823): the accounts that can rewrite Admins cannot set a
	// binding, so an entry without one is a claim nothing confirms.
	a.admins = workspaceProjectAddBoundAdmins(a.roster.Admins, bindings)
	return a.judgeGrants(grants)
}

// judgeGrants is adoption's own judgement: the member's grantees that are not
// people of the workspace yet, each held to D2812's domains rule. A grantee
// outside the allowed domains is admitted only by --external and is marked
// external then -- the flag is the whole of the confirmation, because nothing
// here asks a question (D1540).
func (a *workspaceProjectAdd) judgeGrants(grants []accesslib.Grant) error {
	outsiders := []string{}
	for _, g := range grants {
		address, ok := strings.CutPrefix(g.User, "user:")
		if !ok {
			// accesslib.Decode refuses a member that is not a `user:` (F25), so this
			// is a document that reader would refuse too: it is named rather than
			// skipped, because a workspace adopting silently around it would be
			// reading a member's roster only partly.
			return userErr("project %s's own roster holds the grant %q, which names no user: a grant names a person, so this command cannot say who would become a person of the workspace. Nothing was written",
				safetext.Field(a.member.Project), safetext.Field(g.User))
		}
		person, err := workspace.CanonicalAddress(address)
		if err != nil {
			return userErr("project %s's own roster holds a grant for %q: %v. Nothing was written", safetext.Field(a.member.Project), safetext.Field(address), err)
		}
		if slices.ContainsFunc(a.roster.People, func(p workspace.Person) bool { return p.Address == person }) {
			// Already a person of the workspace: adoption adds people and never
			// rewrites them, so a kind or a mark the node already carries stands
			// (D2805).
			continue
		}
		external := !workspacePersonInDomains(a.roster.Domains, person)
		if external && !a.options.external {
			outsiders = append(outsiders, person)
			continue
		}
		a.adopt = append(a.adopt, workspace.Person{Address: person, Kind: workspace.KindHuman, External: external})
	}
	if len(outsiders) > 0 {
		slices.Sort(outsiders)
		outsiders = slices.Compact(outsiders)
		return userErr("project %s's own roster grants a role to %s, and that is outside workspace %s's allowed domains (%s): adopting it makes somebody from outside them a person of the workspace. --external states that this is intended, and marks each one external (D2812, D2805). Nothing was written; run this again with --external",
			safetext.Field(a.member.Project), safetext.FieldListLine(outsiders), safetext.Field(a.options.workspace), workspaceDomainsPhrase(a.roster.Domains))
	}
	slices.SortFunc(a.adopt, func(p, q workspace.Person) int { return strings.Compare(p.Address, q.Address) })
	return nil
}

// declare is its own write: one entry, built from the same
// flags by the same validation, replacing whole any entry the table already
// declares for the project.
//
// It is not where this command's fence stands. That write path does refuse a
// coding agent's session and print the diff for the person to run, but it
// answers "no change" for an entry the table already carries BEFORE that refusal
// (settings_write.go), and a replay of the same add still reaches the workspace's
// roster, the member's own database and the trail below -- so the refusal is the
// first thing runWorkspaceProjectAdd does, and this function is only one of the
// writes it covers (A7).
func (a *workspaceProjectAdd) declare(cmd *cobra.Command, project string) error {
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: a.path})
	if err != nil {
		return err
	}
	a.declaredFrom = old
	o := &a.options.member
	declared := a.lc.WorkspaceMembers != nil && !(*a.lc.WorkspaceMembers)[project].IsZero()
	next := old
	if declared {
		if next, err = workspaceRemoveKey(a.path, next, []string{"workspace_members", project}); err != nil {
			return err
		}
	}
	// `member add`'s own order: the lists first, so the entry is already there
	// when a scalar is written into it.
	for _, account := range o.jobAccountRepos() {
		if next, err = workspaceUpsertJobAccount(a.path, next, project, account.account, account.repos); err != nil {
			return err
		}
	}
	for _, repo := range o.repos {
		if next, err = workspaceAddItem(a.path, next, []string{"workspace_members", project, "repos"}, repo); err != nil {
			return err
		}
	}
	for _, f := range []struct{ key, value string }{
		{"firebase_project", o.firebaseProject},
		{"fp", o.fp},
		{"exchange_url", o.exchangeURL},
	} {
		if next, err = workspaceSetScalar(a.path, next, []string{"workspace_members", project, f.key}, f.value); err != nil {
			return err
		}
	}
	// The region is optional and written only when it is named: an entry with no
	// database_region is a member whose database is in its project's default
	// region.
	if o.databaseRegion != "" {
		if next, err = workspaceSetScalar(a.path, next, []string{"workspace_members", project, "database_region"}, o.databaseRegion); err != nil {
			return err
		}
	}
	a.declaredTo = next

	edit := settingsEdit{
		Old:     old,
		New:     next,
		What:    workspaceMemberAddWhat(a.options.workspace, project, o, declared),
		Command: workspaceProjectAddCommand(a.options.workspace, project, o),
	}
	// A replaced entry widens and narrows at once, exactly as `member add` reads
	// its own replace: the entry is written whole, so an account or a repository
	// it carries today and this declaration does not name is gone from the table.
	if declared {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("this replaces the entry workspace %s declares for %s: the entry is written whole, so a job service account or a repository it carries today and this declaration does not name is gone from the table",
			a.options.workspace, project)
	}
	return workspaceEditLocal(cmd, a.lc, a.path, edit)
}

// adoptInto is the linked half: the workspace's roster, the member's mirror, the
// member's own roster, and the audit event recording the change last, in that
// order (the file's own comment says why). Every failure after the declaration
// says that the entry is written, because it is, and names what did not land: a
// person who reads it runs this command again rather than puzzling over a table
// entry that is there and a member that is not.
func (a *workspaceProjectAdd) adoptInto(ctx context.Context, cmd *cobra.Command, project string) error {
	out := cmd.OutOrStdout()
	roster, wroteRoster, err := a.writeRoster(ctx)
	if err != nil {
		return a.partial(project, err)
	}
	// The member's own list is written when the roster moved (D2813's membership
	// write), when there is none, and when the one there does not name the
	// people the workspace's roster holds: the last is what makes the sentence
	// below true, because a member's list left as it is must be one that already
	// names them -- a list that names somebody else's people was refused above,
	// and one that names fewer of this workspace's was written by an add that
	// ran while the roster was smaller.
	mirrored := wroteRoster || !a.mirror.Found || !workspaceMirrorNames(a.mirrorEntries, roster.People)
	if mirrored {
		if err := a.writeMirror(ctx, roster); err != nil {
			return a.partial(project, err)
		}
	}
	wroteGrants, err := a.writeMemberGrants(ctx)
	if err != nil {
		return a.partial(project, err)
	}
	if len(a.adopt) > 0 {
		fmt.Fprintf(out, "adopted %s into workspace %s's roster: %s\n",
			workspacePeoplePhrase(len(a.adopt)), safetext.Field(a.options.workspace), workspaceAddressesPhrase(workspacePeopleAddresses(a.adopt)))
	} else {
		fmt.Fprintf(out, "no grantee of project %s is new to workspace %s's roster, so nothing was adopted (D2805)\n",
			safetext.Field(project), safetext.Field(a.options.workspace))
	}
	if len(a.admins) > 0 {
		verb := "wrote"
		if !wroteGrants {
			verb = "kept"
		}
		fmt.Fprintf(out, "%s the stored grant of workspace admin %s in project %s's own roster and the mark of it in workspace %s's roster: the member's own fugaro update applies it (D2801, D2818)\n",
			verb, safetext.FieldListLine(a.admins), safetext.Field(project), safetext.Field(a.options.workspace))
	} else {
		fmt.Fprintf(out, "no admin of workspace %s holds one of its live bindings, so no grant of the workspace's was written into project %s (D2823)\n",
			safetext.Field(a.options.workspace), safetext.Field(project))
	}
	mirror := "already named"
	if mirrored {
		mirror = "now names"
	}
	fmt.Fprintf(out, "project %s's own member list at %s %s %s of workspace %s, and none of the member's own grants was touched (D2805, D2813)\n",
		safetext.Field(project), workspace.PathMemberMirror, mirror, workspacePeoplePhrase(len(roster.People)), safetext.Field(a.options.workspace))
	return a.writeAudit(ctx, project)
}

// partial is what every failure after the declaration is wrapped in: the entry in
// the workspace's table IS written (the declaration ran, and nothing here rolls
// it back), and the sentence names what did not land and what to do about it.
func (a *workspaceProjectAdd) partial(project string, err error) error {
	return userErr("project %s is declared in workspace %s's table at %s, and the rest of the add did not land: %v. A re-run finishes it, and it removes nothing of the member's own grants (D2805)",
		safetext.Field(project), safetext.Field(a.options.workspace), a.path, err)
}

// writeRoster is the workspace node's own read-judge-write: the fresh node, the
// people adopted into it and the admins' marks. A node that moved under this
// writer is read again and judged again from the fresh bytes (D2817), and the
// roster that lands is the one the mirror is written from.
func (a *workspaceProjectAdd) writeRoster(ctx context.Context) (*workspace.Roster, bool, error) {
	store, err := a.openStore(ctx)
	if err != nil {
		return nil, false, err
	}
	for attempt := 0; attempt < workspaceProjectAddAttempts; attempt++ {
		rev, current, found, err := a.readRoster(ctx, store)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, userErr("workspace %s's roster at %s is gone: it was there when this add read it, and this command writes nothing into a workspace that has none", safetext.Field(a.options.workspace), store.Path())
		}
		next, changed := a.mergeRoster(current)
		if !changed {
			return current, false, nil
		}
		encoded, err := workspace.EncodeRoster(next)
		if err != nil {
			return nil, false, userErr("the roster this add would write: %v", err)
		}
		switch err := store.Write(ctx, rev, encoded); {
		case err == nil:
			return next, true, nil
		case errors.Is(err, rtdb.ErrPrecondition):
			continue
		default:
			return nil, false, userErr("writing workspace %s's roster at %s: %v", safetext.Field(a.options.workspace), store.Path(), err)
		}
	}
	return nil, false, userErr("workspace %s's roster at %s moved under this writer %d times: another writer is changing it as fast as this one reads it, so nothing was written — run this command again when it has stopped (D2817)",
		safetext.Field(a.options.workspace), store.Path(), workspaceProjectAddAttempts)
}

// mergeRoster is the change to the workspace's node: the adopted people, each
// once, and one mark per admin this add writes a stored grant for (D2818). Both
// lists are sets written sorted (C1), so the merge is a union and never an
// append.
func (a *workspaceProjectAdd) mergeRoster(r *workspace.Roster) (*workspace.Roster, bool) {
	next := *r
	next.People = slices.Clone(r.People)
	next.Grants = slices.Clone(r.Grants)
	changed := false
	for _, person := range a.adopt {
		if slices.ContainsFunc(next.People, func(p workspace.Person) bool { return p.Address == person.Address }) {
			continue
		}
		next.People = append(next.People, person)
		changed = true
	}
	for _, admin := range a.admins {
		at := slices.IndexFunc(next.Grants, func(g workspace.StoredGrant) bool { return g.Project == a.member.Project && g.User == admin })
		if at < 0 {
			next.Grants = append(next.Grants, workspace.StoredGrant{Project: a.member.Project, User: admin, Role: workspace.RoleAdmin})
			changed = true
			continue
		}
		// The mark is the workspace's record of the grant it wrote, and the grant
		// this add writes for an admin is an admin one (D2818): the member's own
		// project-scope grant is replaced with `RoleAdmin` whole, added where the
		// admin holds none. A mark that says another role -- a node written by an
		// earlier release, or by hand -- would be this workspace's record of a
		// grant it is not writing, so it is corrected here rather than skipped:
		// the mark and the grant agree, or drift would report the grant the
		// workspace itself just wrote as a member's own.
		if next.Grants[at].Role != workspace.RoleAdmin {
			next.Grants[at].Role = workspace.RoleAdmin
			changed = true
		}
	}
	if !changed {
		return r, false
	}
	slices.SortFunc(next.People, func(p, q workspace.Person) int { return strings.Compare(p.Address, q.Address) })
	slices.SortFunc(next.Grants, func(g, h workspace.StoredGrant) int {
		if c := strings.Compare(g.Project, h.Project); c != 0 {
			return c
		}
		return strings.Compare(g.User, h.User)
	})
	return &next, true
}

// writeMirror writes the workspace's member list into the member's own database
// (D2813): the addresses, their kinds and their external marks, and nothing else.
// It is the copy the member's own commands and people read, so it is written from
// the roster that landed and never from the picture this add read.
func (a *workspaceProjectAdd) writeMirror(ctx context.Context, roster *workspace.Roster) error {
	ops, err := a.openMember(ctx)
	if err != nil {
		return err
	}
	raw, err := workspaceMirrorDocument(roster.People)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < workspaceProjectAddAttempts; attempt++ {
		doc, err := ops.ReadMirror(ctx, a.member)
		if err != nil {
			return userErr("reading project %s's own member list at %s: %v", safetext.Field(a.member.Project), workspace.PathMemberMirror, err)
		}
		switch err := ops.WriteMirror(ctx, a.member, doc, raw); {
		case err == nil:
			return nil
		case errors.Is(err, rtdb.ErrPrecondition):
			continue
		default:
			return userErr("writing project %s's own member list at %s: %v", safetext.Field(a.member.Project), workspace.PathMemberMirror, err)
		}
	}
	return userErr("project %s's own member list at %s moved under this writer %d times: nothing was written — run this command again when the other writer has stopped (D2817)",
		safetext.Field(a.member.Project), workspace.PathMemberMirror, workspaceProjectAddAttempts)
}

// workspaceMirrorDocument renders the workspace's member list as the member's
// own mirror holds it (D2813): one entry per person -- the address, the kind and
// the external mark, and no role, no grant and no project, which is the whole of
// what the mirror discloses (threat-model.md §3).
//
// The entries are C1's MirrorEntry, whose three keys the contract fixes. The
// document around them is C2's and C2 declares no type for it ("the member
// roster's and the mirror's formats ... are C2's", contract-member.md §8, and C2
// moves the mirror's bytes), so WU5 -- the first writer of one and its only
// reader -- spells the document here, and this is where a reader of one finds
// its version.
func workspaceMirrorDocument(people []workspace.Person) ([]byte, error) {
	doc := workspaceMirrorDoc{Version: workspaceMirrorVersion, Members: make([]workspace.MirrorEntry, 0, len(people))}
	for _, p := range people {
		entry := workspace.MirrorEntry{Address: p.Address, Kind: p.Kind, External: p.External}
		if err := entry.Validate(); err != nil {
			return nil, userErr("the member list this add would mirror: %v", err)
		}
		doc.Members = append(doc.Members, entry)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, userErr("rendering the member list this add would mirror: %v", err)
	}
	return raw, nil
}

// workspaceMirrorNames reports whether a member's own list already names exactly
// the people a roster holds: the same addresses, the same kinds and the same
// external marks, in the order the document writes them -- which is the roster's
// own, because C1 keeps `People` sorted by address and this reader's entries come
// from a document written the same way.
//
// It is what lets this add leave a member's list alone and still say something
// true about it. A list that names a person this workspace's roster does not is
// refused before this is asked (R2); what is left is a list that names the same
// people, or fewer of them, or fewer with a mark that has moved since -- and the
// second and third are a list this add writes rather than one it calls already
// right.
func workspaceMirrorNames(entries []workspace.MirrorEntry, people []workspace.Person) bool {
	if len(entries) != len(people) {
		return false
	}
	for i, p := range people {
		if entries[i].Address != p.Address || entries[i].Kind != p.Kind || entries[i].External != p.External {
			return false
		}
	}
	return true
}

// workspaceMirrorDoc is the mirror document workspaceMirrorDocument renders and
// workspaceProjectMirrorEntries reads: the version this command writes and C1's
// entries.
type workspaceMirrorDoc struct {
	Version int                     `json:"version"`
	Members []workspace.MirrorEntry `json:"members"`
}

// workspaceProjectMirrorEntries reads a member's mirror, strictly, as every document a
// reader acts on is read here: the node is the member's own database, its own
// admins can rewrite it (WU0's threat model), and what this add does with the
// answer is refuse or replace the document whole -- so an unknown key, a second
// JSON value, a version this command does not write or an entry C1's Validate
// refuses is a refusal rather than a document read as the part of it that
// happened to parse. An absent node is a member the workspace has not mirrored
// into, which is no entries and not an error.
func workspaceProjectMirrorEntries(doc workspace.MemberDoc) ([]workspace.MirrorEntry, error) {
	if !doc.Found {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(doc.Raw))
	dec.DisallowUnknownFields()
	var m workspaceMirrorDoc
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("the document carries more than one JSON value")
	}
	if m.Version != workspaceMirrorVersion {
		return nil, fmt.Errorf("version: %d is not the version this command writes (%d)", m.Version, workspaceMirrorVersion)
	}
	for i, entry := range m.Members {
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("members[%d]: %w", i, err)
		}
	}
	return m.Members, nil
}

// workspaceProjectAddForeignPeople is R2's "another workspace" as it can be read
// once the workspace holds the member (R14): the entries of the member's mirror
// that are not people of the workspace's own roster. D2813 has every membership
// write and every converge rewrite the mirror from the roster, and the grantee
// an add adopts becomes a person of the roster in the same write, so this
// workspace's own mirror carries its own people and nothing else -- an entry the
// roster does not know was written by another workspace, into a project that
// belongs to one (D1291).
//
// It is a refusal to add rather than a fence, and deliberately: the mirror is
// the member's own database and its own admins may rewrite it (WU0's threat
// model, D2813), so a rewritten one can stop this workspace's own add and can
// still say nothing about who a member really is -- drift, read against the
// workspace node, is what answers that.
func workspaceProjectAddForeignPeople(roster *workspace.Roster, entries []workspace.MirrorEntry) []string {
	people := make(map[string]struct{}, len(roster.People))
	for _, p := range roster.People {
		people[p.Address] = struct{}{}
	}
	var foreign []string
	for _, entry := range entries {
		if _, ok := people[entry.Address]; !ok {
			foreign = append(foreign, entry.Address)
		}
	}
	slices.Sort(foreign)
	return slices.Compact(foreign)
}

// writeMemberGrants writes the workspace's own grants into the member's roster:
// one project-scope admin grant per admin that also holds the live binding
// (D2818, D2823). The member's own grants are kept exactly as they are, and the
// pair at project scope is REPLACED rather than added beside, because one role per
// member and scope is what a grant writes (`user pair`'s own rule): two pairs at
// one scope would be two roles the resolver has to choose between.
func (a *workspaceProjectAdd) writeMemberGrants(ctx context.Context) (bool, error) {
	if len(a.admins) == 0 {
		return false, nil
	}
	ops, err := a.openMember(ctx)
	if err != nil {
		return false, err
	}
	for attempt := 0; attempt < workspaceProjectAddAttempts; attempt++ {
		doc, err := ops.ReadGrants(ctx, a.member)
		if err != nil {
			return false, userErr("reading project %s's own roster at %s: %v", safetext.Field(a.member.Project), accesslib.PathNode, err)
		}
		if !doc.Found {
			return false, userErr("project %s's own grants node at %s is not there, so a grant cannot be written into it: the member's own fugaro update migrates its lists first", safetext.Field(a.member.Project), accesslib.PathNode)
		}
		grants, err := accesslib.Decode(doc.Raw)
		if err != nil {
			return false, userErr("project %s's own roster at %s: %v", safetext.Field(a.member.Project), accesslib.PathNode, err)
		}
		next, err := workspaceProjectAddMemberGrants(grants, a.admins)
		if err != nil {
			return false, err
		}
		if workspaceProjectAddSameGrants(grants, next) {
			return false, nil
		}
		raw, err := accesslib.Encode(next)
		if err != nil {
			return false, userErr("the member roster this add would write at %s: %v", accesslib.PathNode, err)
		}
		switch err := ops.WriteGrants(ctx, a.member, doc, raw); {
		case err == nil:
			return true, nil
		case errors.Is(err, rtdb.ErrPrecondition):
			continue
		default:
			return false, userErr("writing project %s's own roster at %s: %v", safetext.Field(a.member.Project), accesslib.PathNode, err)
		}
	}
	return false, userErr("project %s's own roster at %s moved under this writer %d times: nothing was written — run this command again when the other writer has stopped (D2817)",
		safetext.Field(a.member.Project), accesslib.PathNode, workspaceProjectAddAttempts)
}

// workspaceProjectAddMemberGrants is the member's roster with each admin's
// project-scope grant in it: the role replaced where the member already holds one
// at that scope, and appended where they hold none.
func workspaceProjectAddMemberGrants(grants []accesslib.Grant, admins []string) ([]accesslib.Grant, error) {
	next := slices.Clone(grants)
	for _, admin := range admins {
		member, err := accesslib.Member(admin)
		if err != nil {
			// An admin this command read is a person of the workspace's roster,
			// whose addresses are canonical already (C1), so a member it cannot
			// build is a roster this add cannot vouch for: it is refused rather
			// than written as a second spelling of a person.
			return nil, userErr("workspace admin %q: %v. Nothing of the workspace's was written into the member", safetext.Field(admin), err)
		}
		replaced := false
		for i, g := range next {
			if g.User == member && g.Scope.Kind == accesslib.ScopeProject {
				next[i].Role = accesslib.RoleAdmin
				replaced = true
			}
		}
		if !replaced {
			next = append(next, accesslib.Grant{User: member, Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}})
		}
	}
	return next, nil
}

// workspaceProjectAddSameGrants reports whether two grants lists are the same
// statement, so a re-run that changes nothing writes nothing: an unchanged roster
// is not rewritten, and the revision every other admin read stands.
func workspaceProjectAddSameGrants(a, b []accesslib.Grant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].User != b[i].User || a[i].Role != b[i].Role || a[i].Scope.String() != b[i].Scope.String() {
			return false
		}
	}
	return true
}

// writeAudit creates the add's own audit entry, last: the trail records a change
// that has happened (C1), and the entry names the caller, the project and what
// they did (D2811). The workspace's own trail is where a WORKSPACE admin's change
// is recorded; a member's own trail holds the member owner's act (`link`) and the
// grants a project admin writes there, which is WU8's.
func (a *workspaceProjectAdd) writeAudit(ctx context.Context, project string) error {
	sink, err := a.openAudit(ctx)
	if err != nil {
		return err
	}
	caller, err := a.openCaller(ctx)
	if err != nil {
		return err
	}
	now := a.now()
	id, err := workspace.NewAuditEventID(now, a.seams.rand)
	if err != nil {
		return userErr("minting the id of this add's audit entry: %v", err)
	}
	raw, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    now,
		Actor:   caller,
		Action:  workspace.ActionProjectAdd,
		Project: project,
	})
	if err != nil {
		return userErr("the audit entry this add would create: %v", err)
	}
	if err := sink.Create(ctx, id, raw); err != nil {
		return userErr("creating this add's audit entry %s: %v", id, err)
	}
	return nil
}

func (a *workspaceProjectAdd) now() time.Time {
	if a.seams.now == nil {
		return time.Now().UTC()
	}
	return a.seams.now().UTC()
}

func (a *workspaceProjectAdd) openStore(ctx context.Context) (workspace.Store, error) {
	if a.seams.store == nil {
		return nil, userErr("%s", workspaceProjectAddUnwired("the workspace's own roster"))
	}
	return a.seams.store(ctx, a.options.workspace)
}

func (a *workspaceProjectAdd) openAudit(ctx context.Context) (workspace.AuditSink, error) {
	if a.seams.audit == nil {
		return nil, userErr("%s", workspaceProjectAddUnwired("the workspace's own audit trail"))
	}
	return a.seams.audit(ctx, a.options.workspace)
}

func (a *workspaceProjectAdd) openAuthority(ctx context.Context) (workspace.Authority, error) {
	if a.seams.authority == nil {
		return nil, userErr("%s", workspaceProjectAddUnwired("the workspace's own authority"))
	}
	return a.seams.authority(ctx, a.options.workspace)
}

func (a *workspaceProjectAdd) openMember(ctx context.Context) (workspace.MemberOps, error) {
	if a.seams.member == nil {
		return nil, userErr("%s", workspaceProjectAddUnwired("the member's own database"))
	}
	return a.seams.member(ctx, a.lc, a.member, a.options.member.databaseRegion)
}

func (a *workspaceProjectAdd) openCaller(ctx context.Context) (string, error) {
	if a.seams.caller == nil {
		return "", userErr("%s", workspaceProjectAddUnwired("the credential's own address"))
	}
	caller, err := a.seams.caller(ctx)
	if err != nil {
		return "", userErr("this command records who made the change from the credential it is using, and it could not read who that credential belongs to: %v. The caller is the credential and never the local config's own key or git config user.email: sign in with gcloud auth application-default login and run this again", err)
	}
	address, err := workspace.CanonicalAddress(caller)
	if err != nil {
		return "", userErr("the credential's own address %q: %v", safetext.Field(caller), err)
	}
	return address, nil
}

// workspaceProjectAddUnwired is what a seam nobody filled answers: this command is
// wired by I1 and by nothing else, so a missing seam is a wiring bug and not a
// state a person reaches.
func workspaceProjectAddUnwired(what string) string {
	return "fugaro workspace project add is not wired to " + what + " in this build: nothing was written"
}

// workspaceProjectAddBoundAdmins is the admins the node's list and the live
// bindings agree on, in the node's own order: D2823's rule, and the whole of what
// makes an admin entry trusted -- the accounts that can rewrite the list cannot
// set a binding, so an entry without one is a claim nothing confirms.
//
// The two lists spell one person two ways. The node's `Admins` holds canonical
// addresses (C1), and a live binding is the `user:` member a grant names -- C2's
// own spelling, which is `accesslib.Member`'s and what the folder mechanism's
// owners and the account's token creators are read as. Compared as they arrive,
// the two spellings agree on nobody: no admin is ever bound by its own entry. So
// the prefix is taken off here, in the one place the two meet, and each side of
// the comparison is a canonical address again. A binding that is not a `user:`
// member is one this command cannot read as a person at all, and an admin it
// does not name is one nothing confirms -- D2823's answer is to trust neither.
func workspaceProjectAddBoundAdmins(admins, bindings []string) []string {
	bound := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		address, ok := strings.CutPrefix(binding, "user:")
		if !ok {
			continue
		}
		bound[address] = struct{}{}
	}
	out := []string{}
	for _, admin := range admins {
		if _, ok := bound[admin]; ok {
			out = append(out, admin)
		}
	}
	return out
}

// workspacePersonInDomains reports whether an address's domain is one of the
// workspace's allowed domains (D2812). A workspace that lists none admits nobody
// from a domain, which is the same answer the flag's absence gives everywhere
// else: --external is what states that somebody is admitted anyway.
func workspacePersonInDomains(domains []string, address string) bool {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return false
	}
	return slices.Contains(domains, strings.ToLower(address[at+1:]))
}

// workspaceDomainsPhrase says the allowed domains as a sentence fragment, so the
// refusal reads as a statement about this workspace and not about domains in
// general.
func workspaceDomainsPhrase(domains []string) string {
	if len(domains) == 0 {
		return "it lists no allowed domain"
	}
	return strings.Join(domains, ", ")
}

// workspacePeopleAddresses is the addresses of a set of people, in their own
// order.
func workspacePeopleAddresses(people []workspace.Person) []string {
	out := make([]string, 0, len(people))
	for _, p := range people {
		out = append(out, p.Address)
	}
	return out
}

// workspaceAddressesPhrase renders a member's own addresses for a line a person
// reads, each escaped and the listing bounded: an address of a member's roster is
// a member-controlled string, and a sentence this program prints is its own words
// (safetext's own rule).
func workspaceAddressesPhrase(addresses []string) string {
	return safetext.FieldListLine(addresses)
}

// workspacePeoplePhrase is a count of people as a sentence fragment.
func workspacePeoplePhrase(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

// workspaceProjectAddCommand is this command's own spelling of the invocation,
// for the refusal a coding agent's session reads and for the sentence the write
// prints: `member add`'s own arguments with a project where that command spells
// the workspace's name, because --workspace is this command's own flag (F10's
// rule for a command that belongs to one workspace).
func workspaceProjectAddCommand(workspaceName, project string, o *workspaceMemberAddOptions) string {
	cmd := fmt.Sprintf("fugaro workspace project add %s --workspace %s", project, workspaceName)
	for _, a := range o.jobAccountRepos() {
		cmd += " --job-service-account " + a.account
		for _, repo := range a.repos {
			cmd += " --job-repos " + a.account + "=" + repo
		}
	}
	cmd += fmt.Sprintf(" --firebase-project %s --fp %s --exchange-url %s", o.firebaseProject, o.fp, o.exchangeURL)
	if o.databaseRegion != "" {
		cmd += " --database-region " + o.databaseRegion
	}
	for _, repo := range o.repos {
		cmd += " --repo " + repo
	}
	return cmd
}
