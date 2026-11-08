package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// update_access.go is the roles step of the converge (RS3, D1581): the one
// place the installation's two IAM member lists meet the grants node.
//
// Until this release the lists WERE the roles: `terraform.launchers` said who
// may start runs and `terraform.operators` who administers the project, and two
// machines converging one installation could each hold their own idea of it with
// nothing to arbitrate. The node (internal/access, under config/admin/access) is
// that arbiter now, and this step is how an installation gets there:
//
//   - the node is ABSENT (an installation at the previous shape, or a fresh one):
//     the step MIGRATES. It reads the lists the installation itself carries —
//     its Terraform outputs, never the file on this machine, which may be one
//     operator's stale copy — refuses when that machine's copy disagrees with
//     them (userFresh, the same refusal `fugaro user ls` makes), turns each
//     `user:` member into a grant (operators at project scope, admins at project
//     scope) and writes the node conditionally on the ETag it read (F23).
//   - the node is PRESENT: the step DERIVES. The two lists are read off the
//     node's project-scope grants, and the spec builders get them, so an
//     installation whose roles the commands changed converges its IAM to what
//     the node says rather than to what a file remembers. A revoked member is
//     gone from the next apply for exactly that reason: the derive reads the
//     node fresh, in the run that applies, and never the copy the config held
//     (F29a, TestRevokeIsNotUndoneByAStaleDerive).
//
// The version-1 keys retire with the migration and are never written again:
// they are the file's spelling of the two lists, the file is written as version
// 2 after an apply (ConfigVersion), and the grants node is what the next run
// reads. A member form no grant can hold — `group:`, `domain:`,
// `serviceAccount:` — stays a LEGACY IAM-ONLY member (F25): the CLI cannot match
// a caller's groups, so the node cannot rank it, and the member keeps the IAM
// grant it had by being carried into every derived list.

// syncAccess is the run's roles step, called once by the converge before its
// first stage so that every spec this run builds carries the lists the node
// says.
//
// It is best-effort in exactly two states, and both are said out loud rather
// than passed over: an installation with no budget backend has nowhere to keep
// a node (the rolls stay the local config's), and a database this installation
// never adopted is not a database this run writes (the gate the rules stage and
// the policy stage hold, budgetDBUnadopted). Everything else is the run's own
// error: a node that cannot be read is not a node that grants nothing.
func (e *initEngine) syncAccess(ctx context.Context) error {
	lc := e.lc
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		// No Firebase budget backend, so no Realtime Database and no node: the
		// two lists stay the file's, which is the state every installation
		// before this release is in. It is not silent — the run's result says
		// so once (accessNoBackend) — because an operator who reads the grants
		// with `fugaro user ls` and finds them the config's must know why.
		e.r.warn(accessNoBackend)
		return nil
	}
	if err := e.setupCloud(ctx); err != nil {
		return err
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this run migrates no roles there", selfCommand()+" update"); why != "" {
		e.r.warn(why)
		return nil
	}
	node, err := accesslib.Read(ctx, db)
	if err != nil {
		return remote(err)
	}
	if node.Found {
		return e.deriveAccess(node)
	}
	return e.migrateAccess(ctx, db, node)
}

// accessNoBackend is the warning an installation without a budget backend gets:
// its two lists are still the config's, because there is no database to keep
// the roles in.
const accessNoBackend = "this installation has no Firebase budget backend, so it has no grants node: the operators and admins stay the local config's lists"

// migrateAccess writes the grants node for an installation that has none, from
// the lists the INSTALLATION carries.
//
// The lists are read from the installation root's outputs (r.installationOutputs)
// and not from the local config, and the difference is the whole point: the
// config is one machine's copy, and a machine that has not converged since
// another operator changed the installation would migrate a roster that is not
// the installation's. The copy is still used, as the thing to DISAGREE with:
// userFresh refuses when the two do not hold the same members, and the refusal
// names the list and the command, so the fix is a `fugaro update` on the
// machine that changed it and not a node written from the wrong list.
//
// A first run has no outputs to read (nothing has been applied) and nothing to
// migrate: the run's own lists are the flags the operator typed, which the
// installation stage applies and the config records, and the node is written by
// the next converge, when the outputs hold them.
func (e *initEngine) migrateAccess(ctx context.Context, db *rtdb.Client, node accesslib.Node) error {
	if e.firstRun() {
		return e.seedAccess(ctx, db, node, e.spec.Launchers, e.spec.Operators)
	}
	outs, err := e.r.installationOutputs(ctx, e.lc, e.bin, e.spec.StateBucket)
	if err != nil {
		return err
	}
	if err := userFresh(e.lc, outs); err != nil {
		return userErr("%v", err)
	}
	return e.seedAccess(ctx, db, node, outs.Launchers, outs.Operators)
}

// seedAccess turns two IAM member lists into grants and writes them, printing
// the roster it wrote so the migration is a thing the operator can see rather
// than a silent rewrite of who may do what.
func (e *initEngine) seedAccess(ctx context.Context, db *rtdb.Client, node accesslib.Node, operators, admins []string) error {
	grants, err := migrationGrants(operators, admins)
	if err != nil {
		return userErr("%v", err)
	}
	if len(grants) == 0 {
		return nil // nothing to hold: an empty grants list is not a node (WriteIfMatch)
	}
	err = accesslib.WriteIfMatch(ctx, db, grants, node.ETag)
	switch {
	case errors.Is(err, rtdb.ErrPrecondition):
		// Someone else wrote the node between this read and this write: a
		// grant or a revoke from another machine, which judged against its own
		// read. The node is theirs now and this run may not overwrite it from a
		// list it read before that; re-read, and derive from what is there.
		again, rerr := accesslib.Read(ctx, db)
		if rerr != nil {
			return remote(rerr)
		}
		if !again.Found {
			return userErr("the grants node was written and removed by another run while this one migrated it: run %s again", selfCommand()+" update")
		}
		e.r.warn(accessNodeMoved)
		return e.deriveAccess(again)
	case err != nil:
		return remote(err)
	}
	e.r.noteAccess(accessMigrated(grants))
	// The migration is what retires the version-1 keys: the file this run
	// writes at the end of its apply is version 2's shape (ConfigVersion), and
	// a version-1 document is folded into it at parse. Nothing rewrites the
	// file here — the write that lands the node is Terraform's apply, and the
	// config follows it (/ the same run's own write) — so a run that stops
	// between the two leaves the keys where they are and the node holding the
	// roles, which is the state the next converge reads.
	return e.deriveAccess(accesslib.Node{Found: true, Grants: grants, ETag: node.ETag})
}

// accessNodeMoved is what a run says when another writer got to the node first.
const accessNodeMoved = "another run wrote the grants node while this one was migrating it: the roles this run uses are the node's, as it now stands"

// accessMigrated is the migration's own line: the roster it wrote, so an
// operator sees the roles move from a file to the installation.
func accessMigrated(grants []accesslib.Grant) string {
	lines := make([]string, 0, len(grants))
	for _, g := range grants {
		lines = append(lines, fmt.Sprintf("%s (%s)", g.User, g.Role))
	}
	slices.Sort(lines)
	return fmt.Sprintf("migrated the installation's member lists into the grants node: %s; the local config's terraform.launchers and terraform.operators are now derived from it",
		strings.Join(lines, ", "))
}

// noteAccess prints a line of the roles step, in the shape the door's own
// progress lines have: the program's prefix, then the sentence. It is not a
// warning — nothing is wrong — and not a stage's line, because the step runs
// before the loop and has no stage to report through.
func (r *initRun) noteAccess(msg string) {
	fmt.Fprintf(r.w, "%s%s\n", r.o.programPrefix(), msg)
}

// migrationGrants is the grants a pair of IAM member lists becomes: every
// `user:` member of the first is an operator at project scope, every `user:`
// member of the second an admin at project scope.
//
// A member of any other form is left out (F25) — group:, domain: and
// serviceAccount: are IAM members a grant cannot hold, because the CLI cannot
// read a caller's groups — and deriveAccess carries each one forward into the
// list it came from, so the cloud grant it has is not touched by the migration.
// Deduplicated: a person on both lists is one admin grant, which is the role
// the node expresses (the roles rank, and admin outranks operator).
func migrationGrants(operators, admins []string) ([]accesslib.Grant, error) {
	byUser := map[string]accesslib.Role{}
	order := []string{}
	add := func(list, member string, role accesslib.Role) error {
		user, err := accesslib.Member(strings.TrimPrefix(member, "user:"))
		if err != nil {
			return fmt.Errorf("%s holds %q, which is not a user IAM member: %w", list, member, err)
		}
		if prev, ok := byUser[user]; ok {
			if role.Rank() > prev.Rank() {
				byUser[user] = role
			}
			return nil
		}
		byUser[user] = role
		order = append(order, user)
		return nil
	}
	for _, m := range operators {
		if !isUserMember(m) {
			continue
		}
		if err := add("the installation's launcher list", m, accesslib.RoleOperator); err != nil {
			return nil, err
		}
	}
	for _, m := range admins {
		if !isUserMember(m) {
			continue
		}
		if err := add("the installation's operator list", m, accesslib.RoleAdmin); err != nil {
			return nil, err
		}
	}
	slices.Sort(order)
	grants := make([]accesslib.Grant, 0, len(order))
	for _, user := range order {
		grants = append(grants, accesslib.Grant{User: user, Role: byUser[user], Scope: accesslib.Scope{Kind: accesslib.ScopeProject}})
	}
	return grants, nil
}

// deriveAccess reads the two IAM lists off the node and hands them to the run.
//
// It writes nothing to the config: what it changes is the spec this run
// applies, and the config follows the apply (init.go's own write, which records
// the spec's lists). So the file ends the run holding what the node says —
// D1585's "the local lists become derived" — and a file that says otherwise
// (a hand edit, a machine that has not converged) is corrected rather than
// obeyed.
//
// The legacy members are the ones carried forward: a `group:` or `domain:`
// member is in no grant and would otherwise fall out of the installation on the
// first apply after the migration, which is a revocation nobody asked for
// (F25). They are read off the lists this run started with and kept in the list
// they came from.
func (e *initEngine) deriveAccess(node accesslib.Node) error {
	operators, admins := legacyMembers(e.spec.Launchers), legacyMembers(e.spec.Operators)
	for _, g := range node.Grants {
		if g.Scope.Kind != accesslib.ScopeProject || !g.Role.Valid() {
			continue
		}
		switch {
		case g.Role == accesslib.RoleAdmin:
			admins = append(admins, g.User)
			operators = append(operators, g.User)
		case g.Role == accesslib.RoleOperator:
			operators = append(operators, g.User)
		}
	}
	e.spec.Launchers = sortedMembers(operators)
	e.spec.Operators = sortedMembers(admins)
	e.lc.Terraform.Operators = slices.Clone(e.spec.Launchers)
	e.lc.Terraform.Admins = slices.Clone(e.spec.Operators)
	return nil
}

// isUserMember reports whether an IAM member names a person the CLI can rank:
// the `user:` form and nothing else (F25).
func isUserMember(member string) bool { return strings.HasPrefix(member, "user:") }

// legacyMembers is the members of a list that are not `user:` ones: the
// group:, domain: and serviceAccount: forms, in the order they were written.
func legacyMembers(list []string) []string {
	kept := []string{}
	for _, m := range list {
		if !isUserMember(m) {
			kept = append(kept, m)
		}
	}
	return kept
}

// sortedMembers is a list of IAM members in the one order the lists are kept
// in, without repeats. The order is not a meaning (the members are a set) but
// the plan is: a list that comes back in another order is a diff the operator
// has to read for nothing.
func sortedMembers(members []string) []string {
	if len(members) == 0 {
		return nil
	}
	out := slices.Clone(members)
	slices.Sort(out)
	return slices.Compact(out)
}
