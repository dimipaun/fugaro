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
	"github.com/dimipaun/fugaro/internal/safetext"
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
//     admin's stale copy — turns each `user:` member into a grant (operators at
//     project scope, admins at project scope) and writes the node conditionally
//     on the ETag it read (F23). A copy that disagrees with the installation is
//     said out loud and not obeyed (accessStale): the migration is the one step
//     of `fugaro update` that may not be refused, because the refusal's own way
//     out is the command being refused (the livelock the plan's "refuses on
//     drift" would have been), and the roster written is the installation's
//     either way — a member the stale copy named and the installation does not
//     loses the role in this run's apply, which accessDropped says.
//   - the node is PRESENT: the step DERIVES. The two lists are read off the
//     node's project-scope grants, and the spec builders get them, so an
//     installation whose roles the commands changed converges its IAM to what
//     the node says rather than to what a file remembers. A revoked member is
//     gone from the next apply for exactly that reason, and the read the apply
//     itself carries is taken again by that apply (deriveAccessAgain), so a
//     revoke landing between this step and the plan is not undone by a list
//     that read an older node (F29a, TestRevokeIsNotUndoneByAStaleDerive).
//
// The version-1 keys retire with the migration and are never written again:
// they are the file's spelling of the two lists, the file is written as version
// 2 after an apply (ConfigVersion), and the grants node is what the next run
// reads. A member form no grant can hold — `group:`, `domain:`,
// `serviceAccount:` — stays a LEGACY IAM-ONLY member (F25): the CLI cannot match
// a caller's groups, so the node cannot rank it, and the member keeps the IAM
// grant it had by being carried into every derived list.
//
// The one thing the derive cannot do by itself is take a change: a list named on
// the command line would be applied once and reverted by the next converge, so
// the run's own --operator/--admin lands in the node too (foldFlagLists), which
// is the same write `fugaro user`'s own door makes (user.go).

// syncAccess is the run's roles step, called once by the converge before its
// first stage so that every spec this run builds carries the lists the node
// says.
//
// It is best-effort in exactly three states, and all of them are said out loud
// rather than passed over: an installation with no budget backend has nowhere to
// keep a node (the roles stay the local config's), a database this installation
// never adopted is not a database this run writes (the gate the rules stage and
// the policy stage hold, budgetDBUnadopted), and a migration whose write the
// caller's own IAM refuses (only the implicit admins hold the database write,
// F22/D1592) leaves the node absent for a project Owner's next update rather
// than stopping the one command the user runs. Everything else is the run's own
// error: a node that cannot be read is not a node that grants nothing.
func (e *initEngine) syncAccess(ctx context.Context) error {
	lc := e.lc
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		// No Firebase budget backend, so no Realtime Database and no node: the
		// two lists stay the file's, which is the state every installation
		// before this release is in. It is not silent — the run's result says
		// so once (accessNoBackend) — because an operator who reads the grants
		// with `fugaro user ls` and finds the config's must know why.
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
	// The client outlives the step: the installation root re-reads the node
	// through it for the lists its own apply carries (F29a, deriveAccessAgain),
	// and a nil client is exactly the "this run has no node" the root checks.
	e.accessDB = db
	e.r.accessDerive = e.deriveAccessAgain
	node, err := accesslib.Read(ctx, db)
	if err != nil {
		return remote(err)
	}
	if !node.Found {
		if e.r.o.planOnly {
			// --plan-only applies nothing, and every other writer in the run
			// reads the flag for exactly that reason (gateable, managedStage:
			// the plan the user is shown must not be the one thing that
			// changed the project). The migration is a write, so a preview of an
			// installation at the previous shape leaves the node absent and the
			// two lists the file's — the state the installation is in, which is
			// what a plan is a plan of — and the next update that runs migrates
			// them.
			return nil
		}
		if node, err = e.migrateAccess(ctx, db, node); err != nil {
			return err
		}
		if !node.Found {
			// Nothing was written: the lists hold no grant at all (a node of no
			// grants IS no node, WriteIfMatch, and the cloud grants such an
			// installation has are the legacy members'), or the write was
			// refused with the reason already printed (accessNeedsAdmin). Either
			// way there is nothing to derive from and the two lists stay the
			// file's for this run, which is the state the installation was in
			// before the step ran.
			return nil
		}
	}
	if e.r.o.operatorsChanged || e.r.o.adminsChanged {
		if e.r.o.planOnly {
			// A preview may name its lists too, and the fold is the same
			// computation with the write left out (foldedNode): the plan shows
			// the roles this run would land, and the node keeps the ones it has.
			if node, err = e.foldedNode(node); err != nil {
				return err
			}
		} else if node, err = e.foldFlagLists(ctx, db, node); err != nil {
			return err
		}
	}
	e.deriveAccess(node)
	return nil
}

// accessNoBackend is the warning an installation without a budget backend gets:
// its two lists are still the config's, because there is no database to keep
// the roles in.
const accessNoBackend = "this installation has no Firebase budget backend, so it has no grants node: the operators and admins stay the local config's lists"

// accessNeedsAdmin is the migration's own warning: the node is an IAM-protected
// node (config/admin carries `.write: false`, so only a caller holding the
// database write reaches it at all), and the account running this update is not
// one of the installation's implicit admins.
const accessNeedsAdmin = "this run could not write the grants node, which needs roles/firebasedatabase.admin — held by the project's owners, editors and budget admins: the operators and admins stay this machine's lists, and a project Owner runs fugaro update once to migrate them into the node"

// accessFlagsNeedAdmin is the refusal a run's own --operator/--admin gets when
// it cannot be landed: unlike the migration, this one is not a step the run may
// carry on without. An operator who named a list asked for a change to a role,
// and a change that cannot be written may not look applied.
const accessFlagsNeedAdmin = "the grants node cannot be written with your credentials: writing it needs roles/firebasedatabase.admin, which the project's owners, editors and budget admins hold. A project Owner runs fugaro update on this installation, or adds you to terraform.budget_admins (fugaro update --budget-admin)"

// accessNoOutputs is the warning a migration gets when the installation's own
// outputs cannot be read: the migration has no roster to write the node from,
// and this run keeps the local config's lists. It never stops the run — `fugaro
// update` is the whole upgrade, and a roles step that could not read a state
// bucket may not be the thing that fails it — and the state is the one the
// installation was in before this release, so nothing is lost by waiting for a
// later update, which is what the sentence says.
const accessNoOutputs = "the installation's own outputs could not be read, so its roles were not migrated into the grants node: the operators and admins stay the local config's lists, and the next fugaro update migrates them"

// accessStale is what a migration says when this machine's copy of the two
// lists is not what the installation holds. It is not fatal and it is not
// silent: the node is written from the INSTALLATION's roster — the copy written
// by hand or by a door this release does not move is the one that would revoke
// a member — and the sentence names both lists, so an operator who edited the
// file sees what happened rather than finding it gone.
const accessStale = "this machine's %s is not what the installation's own outputs list, so this run migrates the installation's: the config lists %s, the installation lists %s"

// migrateAccess writes the grants node for an installation that has none, from
// the lists the INSTALLATION carries, and returns the node as it now is. A node
// that is not Found and holds no grants means nothing was written: the lists
// hold no grant to hold, or the write was refused with the reason printed.
//
// The lists are read from the installation root's outputs (r.installationOutputs)
// and not from the local config, and the difference is the whole point: the
// config is one machine's copy, and a machine that has not converged since
// another operator changed the installation would migrate a roster that is not
// the installation's. The copy is still used, as the thing to disagree with
// (accessStale), and never as the thing to write: the roster applied after this
// run is the installation's either way, because the derive that follows reads
// the node this wrote, and the file follows the derive.
//
// A first run has no outputs to read (nothing has been applied) and nothing to
// migrate: the run's own lists are the flags the operator typed, which the
// installation stage applies and the config records, and the node is written by
// the next converge, when the outputs hold them.
func (e *initEngine) migrateAccess(ctx context.Context, db *rtdb.Client, node accesslib.Node) (accesslib.Node, error) {
	if e.firstRun() {
		return e.seedAccess(ctx, db, node, e.runList(accesslib.RoleOperator), e.runList(accesslib.RoleAdmin))
	}
	// The state bucket the outputs live in is the one this run applies to (the
	// flag, the config's, else the default), resolved the way every other
	// reader of those outputs resolves it rather than spelled again here: a run
	// given --state-bucket must read the outputs of the installation it is
	// about to apply, not of the one the file remembers.
	spec, err := infra.Installation(e.lc, infra.InstallOptions{StateBucket: e.r.o.stateBucket})
	if err != nil {
		return accesslib.Node{}, initErr(err)
	}
	outs, err := e.r.installationOutputs(ctx, e.lc, e.bin, spec.StateBucket)
	if err != nil {
		e.r.warn(accessNoOutputs)
		return accesslib.Node{}, nil
	}
	for _, l := range []struct {
		what   string
		local  []string
		remote []string
	}{
		{"terraform.operators", e.lc.Terraform.Operators, outs.Launchers},
		{"terraform.admins", e.lc.Terraform.Admins, outs.Operators},
	} {
		if !sameUserMembers(l.local, l.remote) {
			e.r.warn(fmt.Sprintf(accessStale, l.what, safetext.FieldListLine(l.local), safetext.FieldListLine(l.remote)))
		}
	}
	return e.seedAccess(ctx, db, node, outs.Launchers, outs.Operators)
}

// seedAccess turns two IAM member lists into grants and writes them, printing
// the roster it wrote so the migration is a thing the operator can see rather
// than a silent rewrite of who may do what.
func (e *initEngine) seedAccess(ctx context.Context, db *rtdb.Client, node accesslib.Node, operators, admins []string) (accesslib.Node, error) {
	grants, err := migrationGrants(operators, admins)
	if err != nil {
		return accesslib.Node{}, userErr("%v", err)
	}
	if len(grants) == 0 {
		// Nothing to hold: an empty grants list is not a node (WriteIfMatch),
		// and the installation's own forms are legacy members the lists carry.
		return accesslib.Node{}, nil
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
			return accesslib.Node{}, remote(rerr)
		}
		if !again.Found {
			return accesslib.Node{}, userErr("the grants node was written and removed by another run while this one migrated it: run %s again", selfCommand()+" update")
		}
		e.r.warn(accessNodeMoved)
		return again, nil
	case errors.Is(err, rtdb.ErrPermission):
		e.r.warn(accessNeedsAdmin)
		return accesslib.Node{}, nil
	case err != nil:
		return accesslib.Node{}, remote(err)
	}
	e.r.noteAccess(accessMigrated(grants))
	// The ETag is the one this run read before writing, so the first writer
	// after this point is answered by a re-read (foldFlagLists). It is left as
	// it is rather than read back for the sake of one round trip: the node's
	// content below is exactly what was written, and the ETag of a write this
	// process made is a value nothing here judges with.
	return accesslib.Node{Found: true, Grants: grants, ETag: node.ETag}, nil
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
	return fmt.Sprintf("migrated the installation's member lists into the grants node: %s; the local config's terraform.operators and terraform.admins are now derived from it",
		strings.Join(lines, ", "))
}

// noteAccess prints a line of the roles step, in the shape the door's own
// progress lines have: the program's prefix, then the sentence. It is not a
// warning — nothing is wrong — and not a stage's line, because the step runs
// before the loop and has no stage to report through.
func (r *initRun) noteAccess(msg string) {
	fmt.Fprintf(r.w, "%s%s\n", r.o.programPrefix(), msg)
}

// foldFlagLists lands this run's own --operator/--admin in the node.
//
// It is what keeps the two flags a door: the derive that follows reads the node,
// so a list changed only on the command line would be applied once and reverted
// by the next converge — a grant that disappears a run later is worse than one
// that is refused. Naming a list is therefore a change to the role, written
// where the roles live.
//
// The write is conditional on the ETag the node was read with (F23) and a
// mismatch re-reads and folds again, because two operators naming lists at once
// must not each land the other's picture; two rounds are enough for any single
// writer's contention and a third is refused rather than retried forever. Only
// the `user:` members become grants — a `group:`, `domain:` or
// `serviceAccount:` member named on a flag is a legacy IAM-only member and
// stays in the list the derive builds (F25).
func (e *initEngine) foldFlagLists(ctx context.Context, db *rtdb.Client, node accesslib.Node) (accesslib.Node, error) {
	for range 3 {
		folded, err := e.foldedNode(node)
		if err != nil {
			return accesslib.Node{}, err
		}
		if sameGrants(folded.Grants, node.Grants) {
			return node, nil
		}
		err = accesslib.WriteIfMatch(ctx, db, folded.Grants, node.ETag)
		switch {
		case err == nil:
			return folded, nil
		case errors.Is(err, rtdb.ErrPrecondition):
			again, rerr := accesslib.Read(ctx, db)
			if rerr != nil {
				return accesslib.Node{}, remote(rerr)
			}
			node = again
		case errors.Is(err, rtdb.ErrPermission):
			return accesslib.Node{}, userErr("%s", accessFlagsNeedAdmin)
		default:
			return accesslib.Node{}, remote(err)
		}
	}
	return accesslib.Node{}, userErr("the grants node was written by another run every time this one tried to land its --operator/--admin list: run %s again", selfCommand()+" update")
}

// foldedNode is the node as this run's own --operator/--admin lists would leave
// it: the computation foldFlagLists writes, on its own, so a run that may not
// write — --plan-only — derives from the roles it named rather than from the
// ones the node happens to hold.
func (e *initEngine) foldedNode(node accesslib.Node) (accesslib.Node, error) {
	grants := node.Grants
	var err error
	if e.r.o.operatorsChanged {
		if grants, err = foldProjectGrants(grants, accesslib.RoleOperator, e.r.o.operators, "the --operator list"); err != nil {
			return accesslib.Node{}, userErr("%v", err)
		}
	}
	if e.r.o.adminsChanged {
		if grants, err = foldProjectGrants(grants, accesslib.RoleAdmin, e.r.o.admins, "the --admin list"); err != nil {
			return accesslib.Node{}, userErr("%v", err)
		}
	}
	return accesslib.Node{Found: true, Grants: grants, ETag: node.ETag}, nil
}

// foldProjectGrants is the node's grants with the project-scope grants of ONE
// role replaced by the members a list names, and nothing else touched: every
// other grant — the other role's, every non-project scope — keeps its place, so
// a run that names one list cannot drop a scoped pair another run made.
func foldProjectGrants(grants []accesslib.Grant, role accesslib.Role, members []string, what string) ([]accesslib.Grant, error) {
	out := make([]accesslib.Grant, 0, len(grants)+len(members))
	for _, g := range grants {
		if g.Scope.Kind == accesslib.ScopeProject && g.Role == role {
			continue
		}
		out = append(out, g)
	}
	added, err := projectGrants(members, role, what)
	if err != nil {
		return nil, err
	}
	return highestProject(append(out, added...)), nil
}

// migrationGrants is the grants a pair of IAM member lists becomes: every
// `user:` member of the first is an operator at project scope, every `user:`
// member of the second an admin at project scope.
//
// A member of any other form is left out (F25) — group:, domain: and
// serviceAccount: are IAM members a grant cannot hold, because the CLI cannot
// read a caller's groups — and deriveAccess carries each one forward into the
// list it came from, so the cloud grant it has is not touched by the migration.
func migrationGrants(operators, admins []string) ([]accesslib.Grant, error) {
	o, err := projectGrants(operators, accesslib.RoleOperator, "the installation's operator list")
	if err != nil {
		return nil, err
	}
	a, err := projectGrants(admins, accesslib.RoleAdmin, "the installation's admin list")
	if err != nil {
		return nil, err
	}
	return highestProject(append(o, a...)), nil
}

// projectGrants is one list of IAM members as project-scope grants: every
// `user:` member becomes one, the rest are left out, and a `user:` member that
// is not one address is refused naming the list it came from rather than
// dropped — a node written from part of a roster would grant less than the
// installation does, which is a revocation nobody asked for. Both callers pass
// a list they did not write themselves (Terraform's outputs, or a command line),
// so neither has been through localcfg's own member rule.
func projectGrants(members []string, role accesslib.Role, what string) ([]accesslib.Grant, error) {
	out := make([]accesslib.Grant, 0, len(members))
	for _, m := range members {
		if !isUserMember(m) {
			continue
		}
		user, err := accesslib.Member(strings.TrimPrefix(m, "user:"))
		if err != nil {
			return nil, fmt.Errorf("%s holds %q, which is not a user IAM member: %w", what, m, err)
		}
		out = append(out, accesslib.Grant{User: user, Role: role, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}})
	}
	return out, nil
}

// highestProject collapses the project-scope grants to one per person, the
// highest role, and lists them after the other grants in the order every list
// of them is kept in. A person named on both lists is one grant, which is the
// role the node expresses (the roles rank, and an admin grant subsumes an
// operator one); the order is not a meaning but the plan is, and a roster that
// comes back in another order is a diff the operator reads for nothing.
func highestProject(grants []accesslib.Grant) []accesslib.Grant {
	byUser := map[string]accesslib.Role{}
	out := make([]accesslib.Grant, 0, len(grants))
	for _, g := range grants {
		if g.Scope.Kind != accesslib.ScopeProject {
			out = append(out, g)
			continue
		}
		if prev, ok := byUser[g.User]; ok && prev.Rank() >= g.Role.Rank() {
			continue
		}
		byUser[g.User] = g.Role
	}
	users := make([]string, 0, len(byUser))
	for u := range byUser {
		users = append(users, u)
	}
	slices.Sort(users)
	for _, u := range users {
		out = append(out, accesslib.Grant{User: u, Role: byUser[u], Scope: accesslib.Scope{Kind: accesslib.ScopeProject}})
	}
	return out
}

// sameGrants reports whether two grant lists are the same set, order aside: it
// is what keeps a run that names the members the node already holds from
// writing it again, which is the common case for a flag repeated across runs.
func sameGrants(a, b []accesslib.Grant) bool {
	return slices.Equal(grantKeys(a), grantKeys(b))
}

func grantKeys(grants []accesslib.Grant) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.User+"\x00"+string(g.Role)+"\x00"+g.Scope.String())
	}
	slices.Sort(out)
	return out
}

// deriveAccess reads the two IAM lists off the node and hands them to the run.
//
// It writes nothing to the config: what it changes is the spec this run
// applies, and the config follows the apply (init.go's own write, which records
// the spec's lists). So the file ends the run holding what the node says —
// D1585's "the local lists become derived" — and a file that says otherwise
// (a hand edit, a machine that has not converged) is corrected rather than
// obeyed. The node this reads is the one the roles step ended with, after the
// migration and after the run's own flags landed; the installation root derives
// again for its own apply (deriveAccessAgain), later, and that later one is what
// the tfvars hold (F29a).
//
// The legacy members are the ones carried forward: a `group:` or `domain:`
// member is in no grant and would otherwise fall out of the installation on the
// first apply after the migration, which is a revocation nobody asked for
// (F25). They are read off the list THIS RUN would apply — the flag that names
// it, else the local config's — and kept in the list they came from.
//
// A machine's list that names a `user:` member the node does not is the one
// case worth a sentence (accessDropped): the derive is what makes the file
// follow the installation, so a member written into the file by hand — the one
// door RS3 leaves writing those keys, `fugaro user add`, which RS5 moves onto
// the node — would otherwise lose the role in this run's plan with nothing said.
func (e *initEngine) deriveAccess(node accesslib.Node) {
	ranOperators, ranAdmins := e.runList(accesslib.RoleOperator), e.runList(accesslib.RoleAdmin)
	operators := legacyMembers(ranOperators)
	admins := legacyMembers(ranAdmins)
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
	for _, l := range []struct {
		role string
		ran  []string
		now  []string
	}{
		{"operator", ranOperators, e.spec.Launchers},
		{"admin", ranAdmins, e.spec.Operators},
	} {
		if gone := droppedUsers(l.ran, l.now); len(gone) > 0 {
			e.r.warn(fmt.Sprintf(accessDropped, safetext.FieldListLine(gone), l.role))
		}
	}
}

// deriveAccessAgain is the derive the installation root takes for itself,
// immediately before its own tfvars are written (F29a): the node is read again
// and the two lists are handed back, so the apply carries what the node says
// NOW rather than what the roles step read before the run's stages.
//
// The window it closes is a real one and it is the one F29 names: the IAM
// lists are derived from a read, they are applied under Terraform's state lock,
// and a grant or a revoke landing between the read and the apply would be
// re-granted (or dropped) by a list that judged an older node. The read here is
// taken as late as the run can take one — the tfvars prepared from these lists
// are what the plan that holds the lock reads — and `ok` is false when there is
// no node to read, which leaves the lists the file's.
//
// It derives and never writes: landing a change is the roles step's own job
// (the migration, and foldFlagLists for the run's flags), and a step that wrote
// the node from the apply path could overwrite a grant another machine made
// while this run planned.
func (e *initEngine) deriveAccessAgain(ctx context.Context) (operators, admins []string, ok bool, err error) {
	if e.accessDB == nil {
		return nil, nil, false, nil
	}
	node, err := accesslib.Read(ctx, e.accessDB)
	if err != nil {
		return nil, nil, false, remote(err)
	}
	if !node.Found {
		return nil, nil, false, nil
	}
	if e.r.o.planOnly && (e.r.o.operatorsChanged || e.r.o.adminsChanged) {
		// A plan-only run lands no flag list, so the roster the node holds is
		// not the roster this run would apply: the plan a preview shows is the
		// one the run the user asked for would make, which is the folded node
		// (foldedNode) — the same computation the roles step makes with the
		// write left out. Without this the preview would report the member the
		// user just named as one this run drops, which is the opposite of what
		// the run it previews does.
		if node, err = e.foldedNode(node); err != nil {
			return nil, nil, false, err
		}
	}
	e.deriveAccess(node)
	return e.spec.Launchers, e.spec.Operators, true, nil
}

// accessDropped is the warning a converge prints when a machine's own list
// named a person the grants node does not: this run applies the node, so that
// person loses the role. The door named is the whole-list one, which writes the
// node (foldFlagLists), and it is named rather than left to be found because
// the other door on those keys — fugaro user add and rm — writes this machine's
// file alone, and a change written there is exactly what this sentence is about.
const accessDropped = "this machine's list holds %s as %s, which the installation's grants node does not: the node is what an installation's roles are, so this run applies its members and those lose the role. A change to a role is written to the node by naming the whole list (fugaro update --operator, --admin)"

// droppedUsers is the `user:` members of ran that now does not hold.
func droppedUsers(ran, now []string) []string {
	var gone []string
	for _, m := range ran {
		if isUserMember(m) && !slices.Contains(now, m) {
			gone = append(gone, m)
		}
	}
	return sortedMembers(gone)
}

// runList is one of the installation's two IAM lists as THIS run has it: the
// flag that names it, else the local config's — which a first run's default, and
// a new machine's copy of the installation's outputs, have already written. It
// is the list installOptions hands the installation's own apply (init.go), read
// here rather than spelled again so the two cannot choose differently.
func (e *initEngine) runList(role accesslib.Role) []string {
	if role == accesslib.RoleAdmin {
		if e.r.o.adminsChanged {
			return e.r.o.admins
		}
		return e.lc.Terraform.Admins
	}
	if e.r.o.operatorsChanged {
		return e.r.o.operators
	}
	return e.lc.Terraform.Operators
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
