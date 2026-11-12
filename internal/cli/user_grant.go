package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro user grant and revoke are the roles model's own door (docs/roles-scopes,
// RS5): one member, one role, and the scope the role is held at, written into
// the installation's grants node at config/admin/access (D1585) rather than
// into a machine's two IAM lists.
//
// What that changes, said plainly, because two commands now look alike:
//
//   - `fugaro user add` and `rm` write THIS MACHINE's terraform.operators /
//     terraform.admins, which the next `fugaro update` overwrites from the node
//     (RS3, update_access.go's deriveAccess); they stay for an installation with
//     no Firebase backend, where there is no node to write and the two lists are
//     all the roles there are (accessNoBackend).
//   - `grant` and `revoke` write the node, which is the installation's own
//     record of who may do what. The IAM lists are derived from it by the next
//     `fugaro update`, which is what this command says when it succeeds.
//
// The judgement is the plan's (F17, F21–F24, F27), and every part of it is a
// refusal rather than a guess:
//
//   - The caller is the credential (F21): access.Caller, read through
//     userCallerLookup, never the local config's `user:` key and never
//     `git config user.email`. Both of those are text a person or a coding
//     agent can set, and a check built on either is a courtesy.
//   - The node is read once, judged, and written conditionally on that read's
//     ETag (F23). A writer that lost the race gets rtdb.ErrPrecondition and
//     re-reads and re-judges from scratch, so two grants cannot both pass the
//     last-admin rule against the same stale picture.
//   - A grant must be a subset of the grantor's own scope, at that role or
//     higher (F17.1), and a revoke or a demotion needs admin at a scope
//     covering the pair (F24) — so a repository admin can neither widen their
//     reach nor revoke a project-scope pair.
//   - The last project admin stays (F17.2), self-demotion included: a change
//     that would leave the node with no project-scope admin — and no implicit
//     one — is refused before it is written.
//   - An explicit admin@project holds no database write (F22, D1592): the write
//     is what refuses them, and the refusal names terraform.budget_admins and
//     the project Owner who can settle it (adminDenied).
//   - A coding agent's session is refused (D1590), and the refusal prints the
//     exact command for the person to run.
//
// Who counts as an implicit admin is the one place this command cannot read what
// the design names: the GCP project's owners and editors live in its IAM policy,
// which the CLI does not read here (a live CRM call, and the permission to make
// it is not one a budget admin holds). What the command reads instead is the
// one implicit list a machine holds — its own config's terraform.budget_admins —
// and, for a caller the node names NOWHERE, it lets the write itself be the
// answer: only a holder of roles/firebasedatabase.admin can write the node at
// all (F22's table), so an unplaced caller whose write lands is an implicit
// admin by that table, and an unplaced caller whose write is refused is refused
// with the sentence naming budget_admins. Nothing is widened by that: a caller
// the node DOES place is judged against their own grants only, so the subset
// rule and the last-admin rule bind every caller they can bind.

// userPairAttempts bounds one grant's or revoke's ETag retries (F23). A
// mismatch is another writer landing between the read and the write, which the
// re-read answers; a node that moves on every attempt is a busy installation
// and a person retrying is the honest answer.
const userPairAttempts = 4

// userCallerLookup is who the command thinks it is, in the "user:<address>" form
// the grants are written with: access.Caller, the verified address of the
// credential the command will use (F21). It is a variable because a test's own
// function stands here, so no test reaches Google's userinfo endpoint.
var userCallerLookup = func(ctx context.Context) (string, error) { return accesslib.Caller(ctx) }

type userGrantOptions struct {
	cloudOptions
	role, scope string
}

// newUserGrantCmd is `fugaro user grant MEMBER --role R --scope S`.
func newUserGrantCmd() *cobra.Command { return newUserPairCmd(true) }

// newUserRevokeCmd is `fugaro user revoke MEMBER --role R --scope S`.
func newUserRevokeCmd() *cobra.Command { return newUserPairCmd(false) }

// newUserPairCmd builds grant and revoke: one member, one pair, one direction.
// The two share every sentence but the ones the direction decides, because a
// paragraph about the other subcommand would be a claim this one cannot make.
func newUserPairCmd(grant bool) *cobra.Command {
	o := &userGrantOptions{}
	use, short := "revoke MEMBER", "Take a role and scope pair away from a person"
	if grant {
		use, short = "grant MEMBER", "Give a person a role at a scope"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `` + pairWord(grant) + ` one role and scope pair to or from one person, in the
installation's own record of who may do what: the grants node at
config/admin/access, which is what its next fugaro update derives the
installation's IAM lists from.

A pair is a person, a role and the scope the role is held at. The roles are
viewer, operator and admin; a scope is project (the whole installation),
all-repos (every repository the published policy admits), or a comma-separated
list of repository patterns ("github/acme/api", "github/acme/*"). A person may
hold several pairs, and the highest role whose scope covers a repository is the
role they hold there.

--role and --scope are both required and neither has a default: a default would
decide a grant, and revoking a pair means naming the pair.

The command reads the grants node with its ETag, judges, and writes
conditionally on that ETag, so a change another admin made in the meantime is
re-read and re-judged rather than overwritten. It refuses:

  - a grant wider than your own scope, or at a role above your own: a grant is
    a subset of the grantor's scope (a repository admin holding github/acme/api-*
    cannot grant github/acme/*);
  - a revoke or a demotion outside the scope your own admin covers (a
    repository admin cannot revoke a project-scope pair);
  - a change that would leave the installation with no project admin at all,
    your own demotion or removal included.

Writing the node needs roles/firebasedatabase.admin, held by the GCP project's
owners and editors and by terraform.budget_admins — an admin stored in the node
holds no database write of its own (D1592), so the command refuses for them
with that sentence rather than pretending the change landed.

An agent session is refused: ` + pairDone(grant) + ` here is a change to who may do
what, and a person runs it at their own terminal.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return userPair(cmd, o, args[0], grant)
		},
	}
	addCloudFlags(cmd, &o.cloudOptions)
	cmd.Flags().StringVar(&o.role, "role", "", "the role: viewer, operator or admin (required)")
	cmd.Flags().StringVar(&o.scope, "scope", "", "what it covers: project, all-repos, or a comma-separated list of repository patterns (required)")
	return cmd
}

// pairWord and pairDone are the two halves of the direction's word, so a
// sentence about grant does not read as one about revoke.
func pairWord(grant bool) string {
	if grant {
		return "Give"
	}
	return "Take"
}

func pairDone(grant bool) string {
	if grant {
		return "granting"
	}
	return "revoking"
}

func pairPast(grant bool) string {
	if grant {
		return "granted"
	}
	return "revoked"
}

// userPair is grant and revoke's body: one member, one pair, one direction.
func userPair(cmd *cobra.Command, o *userGrantOptions, arg string, grant bool) error {
	ctx := cmd.Context()
	// Before anything is read: the refusal is about who is running (D1590), the
	// way allow_delete.go decides it for the same reason — a coding agent's
	// session holds the admin's credentials, so the refusal is a speed bump and
	// not a boundary, and the sentence it prints is the command to run.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: fugaro user %s changes who may do what in the installation, and the grants node is written with the credentials of one of its admins, so it is run by a person at their own terminal. Run: %s. %s",
			pairDone(grant), holderCommand(o, arg, grant), initflow.AgentRefusal(m))
	}
	// GODEBUG=http2debug makes Go print HTTP requests with their credentials to
	// stderr, and it is read before main runs (refuseHTTP2Debug). This command
	// opens the database and makes Google's userinfo call.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	member, err := userPairMember(arg)
	if err != nil {
		return err
	}
	role, err := userPairRole(o.role)
	if err != nil {
		return err
	}
	scope, err := userPairScope(o.scope)
	if err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was "+pairPast(grant)); err != nil {
		return err
	}
	return userPairWrite(ctx, cmd, lc, db, member, role, scope, grant)
}

// holderCommand is the command a person has to run instead of the agent: the
// subcommand, the member, both flags, and the project when the agent named one.
// It is the sentence D1590 asks for — the exact command, not a description of
// it.
func holderCommand(o *userGrantOptions, member string, grant bool) string {
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	line := fmt.Sprintf("%s user %s %s --role %s --scope %s", selfCommand(), verb, member, o.role, o.scope)
	if o.project != "" {
		line += " --project " + o.project
	}
	return line
}

// userPairMember validates the MEMBER argument and returns the member as the
// node stores it.
//
// It is one person, written user:<email>, and nothing else: a grant names a
// person, and group:, domain: and serviceAccount: members cannot be matched
// against a caller by the CLI, which cannot read a caller's groups (F25). The
// address is folded by accesslib.Member — the same function Caller's own member
// goes through — so the spelling written here and the spelling read there
// cannot differ, and a case or ".git" style variant is normalised rather than
// stored as typed (F17.4).
func userPairMember(m string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", userErr("a grant names one person as user:<email> (%s): group:, domain: and serviceAccount: members cannot be matched against a caller by this command — it cannot read a caller's groups — so only user: members are written here (finding F25)", why)
	}
	if m != strings.TrimSpace(m) {
		return refuse("what was passed has whitespace around it")
	}
	address, ok := strings.CutPrefix(m, "user:")
	if !ok {
		return refuse(fmt.Sprintf("%s is not written as user:<email>", safetext.Field(m)))
	}
	member, err := accesslib.Member(address)
	if err != nil {
		return "", userErr("%s is not a member a grant can name: %v", safetext.Field(m), err)
	}
	return member, nil
}

// userPairRole parses --role. "" is the flag not being passed, which is refused
// rather than defaulted: there is no safe default between the three roles.
func userPairRole(s string) (accesslib.Role, error) {
	if s == "" {
		return "", userErr("--role is required: %s (it names the role this pair holds, and none of them is the default)", roleNames())
	}
	role, ok := accesslib.ParseRole(s)
	if !ok {
		return "", userErr("--role %q must be one of %s", safetext.Field(s), roleNames())
	}
	return role, nil
}

// userPairScope parses --scope with accesslib's own grammar, so the scope this
// command writes is the scope the resolver reads back (F27's fail-closed half).
func userPairScope(s string) (accesslib.Scope, error) {
	if s == "" {
		return accesslib.Scope{}, userErr("--scope is required: %s, or the repositories the role covers (as in \"github/acme/*\")", scopeNames())
	}
	scope, err := accesslib.ParseScope(s)
	if err != nil {
		return accesslib.Scope{}, userErr("--scope: %v", err)
	}
	return scope, nil
}

// roleNames and scopeNames render the closed sets for a refusal, from the one
// place each is defined.
func roleNames() string {
	names := make([]string, 0, len(accesslib.Roles()))
	for _, r := range accesslib.Roles() {
		names = append(names, string(r))
	}
	return strings.Join(names, ", ")
}

func scopeNames() string {
	return `"project" (the whole installation) or "all-repos"`
}

// userPairWrite reads the node, judges the change against that read, and writes
// conditionally on its ETag, re-judging from a fresh read on a mismatch (F23).
func userPairWrite(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, db *rtdb.Client, member string, role accesslib.Role, scope accesslib.Scope, grant bool) error {
	caller, err := userCallerLookup(ctx)
	if err != nil {
		return userErr("this command decides what you may change from the credential it is using, and it could not read who that credential belongs to: %v. The caller is the credential and never the local config's `user:` key or git config user.email (finding F21): sign in with gcloud auth application-default login and run this again", err)
	}
	out := cmd.OutOrStdout()
	for attempt := 0; attempt < userPairAttempts; attempt++ {
		node, err := accesslib.Read(ctx, db)
		if err != nil {
			return dbErr(lc, err, false)
		}
		if err := userPairDrift(lc, node); err != nil {
			return err
		}
		if grant && userHoldsPair(node.Grants, member, role, scope) {
			fmt.Fprintf(out, "%s already holds %s at %s in project %s's grants node; nothing was written\n",
				safetext.Field(member), role, safetext.Field(scope.String()), safetext.Field(lc.Name))
			return nil
		}
		next, err := userPairJudge(lc, node.Grants, caller, member, role, scope, grant)
		if err != nil {
			return err
		}
		err = accesslib.WriteIfMatch(ctx, db, next, node.ETag)
		switch {
		case err == nil:
			return userPairDone(out, lc, member, role, scope, grant)
		case errors.Is(err, rtdb.ErrPrecondition):
			// Another writer landed between the read and this write: the node
			// has moved, so the picture this change was judged against is not
			// the picture it would be written over. Read it again and judge
			// again, from nothing.
			continue
		case errors.Is(err, rtdb.ErrPermission):
			return userPairDenied(lc, grant)
		default:
			return dbErr(lc, err, true)
		}
	}
	return userErr("nothing was %s: the grants node changed under every one of %d attempts, so another admin is writing it right now and the change cannot be judged against a picture that stays still. Read it with 'fugaro user ls' and run this again", pairPast(grant), userPairAttempts)
}

// userPairDrift refuses to create the grants node while this machine's own
// lists still hold members who are on neither side of this change.
//
// It is the one refusal that is about the state the node is in rather than
// about the change: an installation whose roles are still its IAM lists has no
// node at all (RS3 derives the lists FROM the node, and the migration is what
// puts them in it). A node this command created would hold the pair being
// written and nothing else, and the next `fugaro update` would derive the two
// lists from it — so every operator and admin this machine still names, none of
// whom the node knows, would lose their IAM grant in that run. A grant cannot
// quietly revoke the installation's roster, and the migration is one command
// away: it has to happen first.
//
// A machine whose lists hold nobody has nothing to lose, and is written: its
// roles are entirely the node's, whether the node is absent because it has no
// grants or because nothing has derived them yet.
func userPairDrift(lc *localcfg.Config, node accesslib.Node) error {
	if node.Found {
		return nil
	}
	held := append(slices.Clone(lc.Terraform.Operators), lc.Terraform.Admins...)
	if len(held) == 0 {
		return nil
	}
	return userErr("nothing was written: project %s has no grants node at %s yet, and this machine's own lists still hold %d member(s) — %s. Their roles are the installation's IAM lists, and the next fugaro update derives those lists FROM the node: a node written now would hold this change and nobody else, so that update would derive the lists from it and drop every member it does not name. Run fugaro update first — it migrates this installation's lists into the node — then run this again",
		safetext.Field(lc.Name), accesslib.PathNode, len(held), safetext.FieldListLine(held))
}

// userPairJudge is the judgement the write is made under: it returns the grants
// the node would hold, or the refusal.
//
// The order is the plan's, and it matters: the input checks first, then the
// authority the caller holds over the pair (F17.1, F24), then the last-admin
// rule (F17.2) over the document that would be written.
func userPairJudge(lc *localcfg.Config, grants []accesslib.Grant, caller, member string, role accesslib.Role, scope accesslib.Scope, grant bool) ([]accesslib.Grant, error) {
	implicit := userImplicitAdmins(lc)
	held := userGrantsOf(grants, caller)
	isImplicit := slices.Contains(implicit, caller)
	// A caller the node names nowhere and whose own machine does not list them
	// among its budget admins is not placed by either input this command can
	// read. It is not refused here: the write is the answer (see the file's
	// own doc), and the sentence it gets when the write is refused names
	// terraform.budget_admins.
	unplaced := len(held) == 0 && !isImplicit
	if !grant {
		if !userHoldsPair(grants, member, role, scope) {
			return nil, userPairNoSuchPair(grants, member, role, scope)
		}
		if !unplaced && !userMayAdmin(held, isImplicit, scope) {
			return nil, userPairNeedsAdmin(member, role, scope, userHeldText(held), false)
		}
	} else {
		old, replacing := userGrantAt(grants, member, scope)
		demotion := replacing && !role.AtLeast(old.Role)
		if !unplaced {
			if demotion && !userMayAdmin(held, isImplicit, scope) {
				return nil, userPairNeedsAdmin(member, role, scope, userHeldText(held), true)
			}
			if !userMayGrant(held, isImplicit, role, scope) {
				return nil, userPairNeedsRole(member, role, scope, userHeldText(held))
			}
		}
	}
	// The last project admin stays (F17.2), and a revoke is judged by it exactly
	// as a demotion is: both empty the role, and the verb is not what the rule
	// is about.
	//
	// The implicit admins the design names that this machine can read (its own
	// terraform.budget_admins, F17.3) stand beside the stored ones, and the
	// caller themselves stands there when the node places them nowhere: their
	// write is what proves them an implicit admin (F22's table), and a caller
	// who is not one never gets this far — their write is refused and nothing
	// lands.
	next := userPairNext(grants, member, role, scope, grant)
	if unplaced {
		implicit = append(slices.Clone(implicit), caller)
	}
	if err := userLastProjectAdmin(grants, next, implicit, member); err != nil {
		return nil, err
	}
	return next, nil
}

// userLastProjectAdmin refuses the change that TAKES the installation's last
// project admin away — the stored admin@project members, and the implicit
// admins beside them (F17.2, F22). The two documents are compared because the
// rule is about what the change removes, and the change is judged against the
// same read the write is conditional on, so the "before" it uses is the
// picture the write lands on (F23): another writer in between costs a
// re-read and a re-judgement, never a count of a state that has moved.
//
// A node holding NO project admin at all is not a change this refuses. That
// state is reachable and legitimate — a repository-scoped grant is a whole
// node's worth of roles (F22's table), and an implicit admin whose own role is
// not stored can make one — and refusing every later change on such a node
// would be a dead end: the repository admin it would refuse holds no
// project-scope role and could not add the admin the sentence asks for. What
// the rule protects is the admin the change is about to remove, and an
// installation that has none loses none.
func userLastProjectAdmin(grants, next []accesslib.Grant, implicit []string, member string) error {
	if len(implicit) > 0 || userProjectAdmins(grants) == 0 || userProjectAdmins(next) > 0 {
		return nil
	}
	return userErr("nothing was written: the grants node would hold no admin at project scope after this change — %s is the only member that carries it — and no owner, editor or terraform.budget_admin is named either, so the installation would have no project admin at all: nobody could grant a role, run its update or settle it. Add the admin who is to take over first, then make this change; a project Owner can also do it from outside the grants node, or add themselves to terraform.budget_admins (fugaro update --budget-admin)", safetext.Field(member))
}

// userProjectAdmins counts the pairs of a document that are a project-scope
// admin: the role the rule above is about.
func userProjectAdmins(grants []accesslib.Grant) int {
	n := 0
	for _, g := range grants {
		if g.Role == accesslib.RoleAdmin && g.Scope.Kind == accesslib.ScopeProject {
			n++
		}
	}
	return n
}

// userPairNext is the document the change makes: a grant replaces the role of
// the pair already stored at that scope and appends the pair when there is
// none, and a revoke removes the pair it names. The order of the other grants
// is kept, so a change to one member is one line of the node.
//
// One role per member and scope is what a grant writes, so the pair is replaced
// and never added beside the one it replaces: two pairs at one scope would be
// two roles the resolver has to choose between, and `Effective` would take the
// higher of them — a "demotion" that demotes nobody.
func userPairNext(grants []accesslib.Grant, member string, role accesslib.Role, scope accesslib.Scope, grant bool) []accesslib.Grant {
	next := make([]accesslib.Grant, 0, len(grants)+1)
	replaced := false
	for _, g := range grants {
		switch {
		case !grant && g.User == member && g.Role == role && g.Scope.String() == scope.String():
			continue
		case grant && g.User == member && g.Scope.String() == scope.String():
			g.Role = role
			replaced = true
		}
		next = append(next, g)
	}
	if grant && !replaced {
		next = append(next, accesslib.Grant{User: member, Role: role, Scope: scope})
	}
	return next
}

// userHoldsPair reports whether the node stores exactly this pair: the member,
// the role and the scope as the node spells it. It is what a revoke names, and
// what makes a repeated grant a no-op.
func userHoldsPair(grants []accesslib.Grant, member string, role accesslib.Role, scope accesslib.Scope) bool {
	_, ok := userPairAt(grants, member, role, scope)
	return ok
}

func userPairAt(grants []accesslib.Grant, member string, role accesslib.Role, scope accesslib.Scope) (accesslib.Grant, bool) {
	for _, g := range grants {
		if g.User == member && g.Role == role && g.Scope.String() == scope.String() {
			return g, true
		}
	}
	return accesslib.Grant{}, false
}

// userGrantAt is the pair already stored for member at this scope, whatever its
// role: a grant writes one role per scope, so a second role at the same scope
// is a replacement and, when it is lower, a demotion (F24, F23's self-demotion).
func userGrantAt(grants []accesslib.Grant, member string, scope accesslib.Scope) (accesslib.Grant, bool) {
	for _, g := range grants {
		if g.User == member && g.Scope.String() == scope.String() {
			return g, true
		}
	}
	return accesslib.Grant{}, false
}

// userGrantsOf is the node's grants that name one member: the caller's own
// pairs, which are the whole of what they may hand out (F17.1).
func userGrantsOf(grants []accesslib.Grant, member string) []accesslib.Grant {
	var out []accesslib.Grant
	for _, g := range grants {
		if g.User == member {
			out = append(out, g)
		}
	}
	return out
}

// userMayGrant reports whether these own grants let the caller hand out role at
// scope: some grant of theirs holds at least that role at a scope that covers
// it. It is F17.1 exactly — the subset test over canonical patterns — and an
// implicit project admin holds everything.
func userMayGrant(held []accesslib.Grant, implicit bool, role accesslib.Role, scope accesslib.Scope) bool {
	if implicit {
		return true
	}
	return slices.ContainsFunc(held, func(g accesslib.Grant) bool {
		return g.Role.AtLeast(role) && accesslib.SubsetOf(scope, g.Scope)
	})
}

// userMayAdmin reports whether the caller holds admin at a scope covering
// scope: what a revoke or a demotion needs (F24).
func userMayAdmin(held []accesslib.Grant, implicit bool, scope accesslib.Scope) bool {
	return userMayGrant(held, implicit, accesslib.RoleAdmin, scope)
}

// userImplicitAdmins is the implicit project admins this machine can read: the
// terraform.budget_admins of its own config, as members. The GCP project's
// owners and editors are implicit admins too (F17.3) and are NOT here: they
// live in its IAM policy, which this command does not read (see the file's own
// doc). A group or service account member is left out — a member the CLI cannot
// match against a caller is not one it can place (F25).
func userImplicitAdmins(lc *localcfg.Config) []string {
	var out []string
	if lc == nil {
		return nil
	}
	for _, m := range lc.Terraform.BudgetAdmins {
		address, ok := strings.CutPrefix(m, "user:")
		if !ok {
			continue
		}
		member, err := accesslib.Member(address)
		if err != nil {
			continue
		}
		out = append(out, member)
	}
	return out
}

// userHeldText renders the caller's own pairs for a refusal: what they hold,
// which is the sentence a person needs to see to know what to ask for.
func userHeldText(held []accesslib.Grant) string {
	if len(held) == 0 {
		return "nothing in the grants node"
	}
	pairs := make([]string, 0, len(held))
	for _, g := range held {
		pairs = append(pairs, fmt.Sprintf("%s at %s", g.Role, g.Scope.String()))
	}
	slices.Sort(pairs)
	return strings.Join(pairs, ", ")
}

// userPairNeedsRole is the refusal of a grant wider than the caller's own
// scope, or above their own role: it names the role and the scope the action
// needs and what the caller holds, and it never asks a question.
func userPairNeedsRole(member string, role accesslib.Role, scope accesslib.Scope, held string) error {
	return userErr("nothing was written: %s holds %s at %s would need %s at a scope covering %s, and you hold %s. A grant is a subset of the grantor's own scope at that role or higher (finding F17.1), so a narrower scope cannot hand out a wider one — ask a project admin (admin at project scope), who may grant %s at any scope", safetext.Field(member), role, safetext.Field(scope.String()), role, safetext.Field(scope.String()), held, role)
}

// userPairNeedsAdmin is the refusal of a revoke or a demotion outside the
// caller's own admin scope (F24).
func userPairNeedsAdmin(member string, role accesslib.Role, scope accesslib.Scope, held string, demotion bool) error {
	what := "revoking"
	if demotion {
		what = "replacing a higher role with a lower one, which is demoting"
	}
	return userErr("nothing was written: %s %s at %s needs admin at a scope covering %s (finding F24), and you hold %s. A repository admin can never revoke or demote a project-scope or all-repos pair, nor one outside the scope their own admin covers — ask a project admin (admin at project scope)", what, safetext.Field(member), role, safetext.Field(scope.String()), held)
}

// userPairNoSuchPair is the refusal of a revoke that names no stored pair: a
// revoke names the pair it removes, so the person is shown the pairs the node
// does hold for that member rather than a guess at what they meant.
func userPairNoSuchPair(grants []accesslib.Grant, member string, role accesslib.Role, scope accesslib.Scope) error {
	stored := userGrantsOf(grants, member)
	what := "no grant names them at all"
	if len(stored) > 0 {
		what = "the node holds " + userHeldText(stored)
	}
	return userErr("nothing was written: %s holds no %s at %s in the grants node — %s. A revoke names the pair it takes away, and the role and the scope have to be the ones stored, so read them with 'fugaro user ls' and revoke that pair", safetext.Field(member), role, safetext.Field(scope.String()), what)
}

// userPairDone prints what was stored, and the canonical scope this command
// writes: the scope as ParseScope stores it, so a person who typed a ".git"
// suffix or an upper-case owner sees the spelling the resolver reads back
// (F17.4).
func userPairDone(out io.Writer, lc *localcfg.Config, member string, role accesslib.Role, scope accesslib.Scope, grant bool) error {
	if !grant {
		fmt.Fprintf(out, "%s no longer holds %s at %s in project %s's grants node. The installation's IAM lists are derived from the node: run fugaro update (an implicit admin's task) for the change to reach them\n",
			safetext.Field(member), role, safetext.Field(scope.String()), safetext.Field(lc.Name))
		return nil
	}
	fmt.Fprintf(out, "%s now holds %s at %q in project %s's grants node. The installation's IAM lists are derived from the node: run fugaro update (an implicit admin's task) for the change to reach them\n",
		safetext.Field(member), role, scope.String(), safetext.Field(lc.Name))
	return nil
}

// userPairDenied is the refusal the database's own IAM gives an explicit
// admin@project (F22, D1592): the node is under config/admin, whose write is
// false for every client token, so only a holder of
// roles/firebasedatabase.admin reaches it — the project's owners, editors and
// terraform.budget_admins. It names them and the one command that settles it.
func userPairDenied(lc *localcfg.Config, grant bool) error {
	return userErr("nothing was %s: the grants node cannot be written with your credentials, and writing it needs roles/firebasedatabase.admin, which the GCP project's owners and editors and terraform.budget_admins hold. An admin stored in the grants node derives no database write of its own (finding F22, D1592), so it cannot grant either: a project Owner runs fugaro update on this installation, or adds you to terraform.budget_admins (fugaro update --budget-admin)", pairPast(grant))
}
