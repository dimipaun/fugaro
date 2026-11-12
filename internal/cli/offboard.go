// offboard.go is `fugaro user offboard EMAIL` (DS2.4, D1935): the admin's own
// command for taking one member off the installation.
//
// WHY IT STANDS BESIDE `fugaro user rm`. `user rm` edits ONE MACHINE's copy of
// the operator and admin lists and the next `fugaro update` applies it: it is
// the list's door, and it writes a file. Offboarding is about a person, and what
// a person holds here is no longer only an IAM member: the roster at
// config/admin/access (RS3) is what places them — it is where a project-scope
// admin grant is read from, and the source of the list the intake checks a
// shared-prefix writer against (D1940) — and the credentials they stored under
// their own prefix (fugaro_u_<sub>_<name>) are theirs to leave behind. So this
// command does the two things an offboarding has to do, in the order that fails
// safe:
//
//  1. The roster drops them: every grant of the node that names their address is
//     removed and the node is written back, conditionally on the ETag the read
//     carried, so two admins changing the roster at once do not each land the
//     other's picture (F23; update_access.go's foldFlagLists makes the same
//     write the same way). The IAM half is NOT touched here: the invoker grants
//     are Terraform's, and the converge that follows this roster change takes
//     them off (F20) — `fugaro update` on an operator's machine, which is also
//     what writes this machine's own terraform.operators/admins out of the node.
//     The command says that rather than implying the person lost their cloud
//     access now.
//  2. The intake sweeps their prefix: every container whose owner annotation is
//     their address (R10) is deleted by the intake's own account, which is the
//     only writer holding a delete under the two prefixes (DS2.1, DS2.3). A
//     member who stored nothing is a sweep that found nothing, and the answer
//     names what it removed.
//
// WHAT IT DOES NOT DO is as much of the design as what it does, and each half is
// stated rather than left to be discovered:
//
//   - It does not end the member's open slips, and it could not: the slip routes
//     are the service's own contract, grant and claim and nothing else (G10), so
//     an open slip ends by the max-runtime cap the grant recorded (R10) and a run
//     that is in flight is not stopped by this command.
//   - It does not rewrite the published policy. A pool's owner and allowed users
//     are policy text, and the service resolves them at issue time for a launcher
//     it admits — a member the roster no longer places cannot ask for a slip at
//     all, so the text naming them is inert and no publish is owed.
//
// The refusals are the ones every door of this kind makes: a person at their own
// terminal (a coding agent's session holds the admin's credentials), no run's own
// process, and a caller that has to be a project-scope admin OF THE ROSTER — not
// an implicit project admin, which holds no stored grant and is therefore refused
// (F17.3, D1940), the same fail-closed direction the intake's own admin check
// takes.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// offboardCaller is who the command thinks it is, in the "user:<address>" form
// the roster stores a member in: access.Caller, which reads the credential the
// command will use and never the local config's `user:` key nor git config
// user.email — both are text a person or a coding agent can set (F21). It is a
// variable because a test's own function stands here, so no test reaches
// Google's userinfo endpoint.
var offboardCaller = func(ctx context.Context) (string, error) { return accesslib.Caller(ctx) }

// newUserOffboardCmd is `fugaro user offboard EMAIL`.
func newUserOffboardCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "offboard EMAIL",
		Short: "Take a member off the installation: their roster grants, and the secrets they stored",
		Long: `Take one member off the installation, named by their email address.

Two things happen, and nothing else does:

  - The roster (the installation's grants node) drops every grant that names
    the address. The IAM grants are not touched here: an operator's next
    ` + "`fugaro update`" + ` removes the invoker grants the roster no longer carries, and
    writes this machine's own operator and admin lists from it.
  - The installation's intake deletes the secrets stored under the member's own
    prefix, found by the owner annotation it wrote (R10). No value is read or
    printed anywhere: the intake has no route that answers one.

The member's open slips are not ended — the service has no route for that, by
design: a slip ends by the max-runtime cap the grant recorded, so a run in
flight is not stopped by this command. A pool that lists them among its allowed
users keeps the text, and no policy has to be published: a pool is resolved for
a launcher the roster admits, and this command is what stops admitting them.

Offboarding is an admin's act: the caller has to be a project-scope admin grant
of the roster itself. An implicit project admin — a GCP owner or editor, a
budget admin — holds no stored grant and is refused (D1940), exactly as the
intake refuses them; an admin of that kind takes the stored grant first.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("user offboard needs the member's EMAIL address")
			case len(args) > 1:
				return userErr("user offboard takes one EMAIL address")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return userOffboard(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// offboardRefusal is the refusal this command makes before anything is read: a
// coding agent's session, a run's own process, and the GODEBUG http2debug leak.
// It is userEdit's order and its reason — this command writes the roster with
// the admin's own credential, and an agent's session holds it — with the intake
// family's second check beside it, because the same command reaches the intake.
func offboardRefusal(getenv func(string) string, terminal bool) error {
	if m := agentMarker(getenv, terminal); m != "" {
		return userErr("nothing was offboarded: taking a member off the installation writes the roster and deletes the secrets they stored, so it is run by a person at their own terminal: %s", initflow.AgentRefusal(m))
	}
	if m := managedFlagRunMarker(getenv); m != "" {
		return userErr("nothing was offboarded: this process is a Fugaro run's (%s is set), and a run neither holds the roster's write nor may delete a person's credentials", m)
	}
	return refuseHTTP2Debug(getenv)
}

// userOffboard is the command's body, and the order is the whole of its safety:
// the refusals, the address, the installation, the caller's own role, the roster,
// and only then the member's prefix.
func userOffboard(cmd *cobra.Command, o cloudOptions, email string) error {
	ctx := cmd.Context()
	if err := offboardRefusal(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); err != nil {
		return err
	}
	member, address, err := offboardMember(email)
	if err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	// The one ownership question every writer into the database asks, before
	// the name check and before the roster is read: this command writes the
	// roster with the person's own database credential.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was offboarded"); err != nil {
		return err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	me, err := offboardCaller(ctx)
	if err != nil {
		return userErr("nothing was offboarded: offboarding is an admin's act and the roster records who the admins are, so the command has to know which member this invocation runs as, and it could not read the identity the Google credential belongs to: %v", err)
	}
	node, err := accesslib.Read(ctx, db)
	switch {
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("nothing was offboarded: %s refused the read — offboarding removes the member's grants there, which needs roles/firebasedatabase.admin, held by the project's owners, editors and budget admins: %v", accesslib.PathNode, err)
	case err != nil:
		return dbErr(lc, err, false)
	}
	if !offboardIsAdmin(node.Grants, me) {
		return offboardNeedsAdmin(node, lc, me)
	}

	w := cmd.OutOrStdout()
	_, dropped := offboardDropGrants(node.Grants, member)
	if dropped > 0 {
		if err := offboardWriteRoster(ctx, db, node, member); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s is off the roster of %s: %d grants removed\n", safetext.Field(address), safetext.Field(lc.Name), dropped)
		fmt.Fprintf(w, "the invoker grants come off on the installation's next `%s update`, which also writes this machine's operator and admin lists from the roster: nothing was applied to IAM here\n", selfCommand())
	} else {
		fmt.Fprintf(w, "%s is not on the roster of %s: no grant of theirs was there to remove\n", safetext.Field(address), safetext.Field(lc.Name))
	}
	fmt.Fprintf(w, "the member's open slips are not ended by this command: a slip ends by the max-runtime cap the grant recorded, so a run in flight is not stopped here\n")

	deleted, err := offboardSweep(ctx, o, member, address)
	if err != nil {
		// The roster half has already landed and is printed above: the error is
		// the intake's own refusal (userErr or remote) and is returned as it
		// is, so the exit code keeps the class the intake's answer gave it.
		return err
	}
	if len(deleted) == 0 {
		fmt.Fprintf(w, "%s stored no secrets of their own: the intake's sweep of their prefix removed nothing\n", safetext.Field(address))
		return nil
	}
	fmt.Fprintf(w, "removed %d secrets %s stored: %s\n", len(deleted), safetext.Field(address), strings.Join(printableAll(deleted), ", "))
	return nil
}

// offboardMember reads the EMAIL argument: the address the member is named by,
// and the member text the roster stores them as.
//
// A member is named here by the address a person knows and not by the roster's
// own spelling of it, so an argument written as `user:<email>` — the spelling
// the two local lists and this command family's other verbs take — is refused
// rather than read: access.Member would fold it into `user:user:<email>`, which
// names nobody, and a refusal that says so is the difference between a typo and
// a silent no-op. The fold is access.Member's: the domain is lower-cased and the
// local part is kept byte for byte, because that is the spelling both sides of
// the comparison go through (Caller and the grant a node stores).
func offboardMember(email string) (member, address string, err error) {
	switch {
	case email != strings.TrimSpace(email):
		return "", "", userErr("the address has whitespace around it: offboarding names one member, and %s is not an address", safetext.Field(email))
	case strings.HasPrefix(email, "user:"):
		return "", "", userErr("offboarding names a member by their email address and not by the roster's own spelling of it: pass %s rather than %s", safetext.Field(strings.TrimPrefix(email, "user:")), safetext.Field(email))
	}
	member, err = accesslib.Member(email)
	if err != nil {
		return "", "", userErr("offboarding names one member by their email address: %v", err)
	}
	return member, strings.TrimPrefix(member, "user:"), nil
}

// offboardNeedsAdmin is the refusal a caller who does not hold a project-scope
// admin grant of the roster gets, and it says what the roster is and where an
// admin comes from: a caller told only "not an admin" would go looking in the
// installation's own lists, which are derived from this node and not the thing
// that decides it.
func offboardNeedsAdmin(node accesslib.Node, lc *localcfg.Config, me string) error {
	if !node.Found {
		return userErr("nothing was offboarded: %s has no roster yet (%s is absent), so no caller is an admin of it, and offboarding is an admin's act. The node is what `%s update` writes from the installation's own lists, and an admin grant of it is what admits you: name one with `%s update --admin` on the installation, which writes the whole admin list",
			safetext.Field(lc.Name), accesslib.PathNode, selfCommand(), selfCommand())
	}
	return userErr("nothing was offboarded: %s is not a project-scope admin in %s's roster (%s), and offboarding is an admin's act. An installation's admins are the roster's own admin grants at project scope, which `%s update --admin` and `%s user add` write; the project's implicit admins — its GCP owners and editors, and its budget admins — hold no stored grant and are refused here and at the intake alike (D1940)",
		safetext.Field(me), safetext.Field(lc.Name), accesslib.PathNode, selfCommand(), selfCommand())
}

// offboardDropGrants is the roster as this offboarding leaves it: every grant
// that names member is gone, and the rest are kept in the node's own order.
//
// It is EVERY grant of the address and not the first one: an address can hold a
// grant at project scope and another over repositories, and leaving either
// behind is leaving the member placed. The comparison is exact, as the roster's
// own resolution is (access.EffectiveAtProject): a stored grant is written
// canonically by whoever wrote it (store.go's canonicalMember, the same fold
// this command's argument goes through), and a reader that folded further would
// be admitting a member the rest of the tree does not.
func offboardDropGrants(grants []accesslib.Grant, member string) ([]accesslib.Grant, int) {
	next := make([]accesslib.Grant, 0, len(grants))
	for _, g := range grants {
		if g.User == member {
			continue
		}
		next = append(next, g)
	}
	return next, len(grants) - len(next)
}

// offboardWriteRoster writes the node without the member's grants, conditional
// on the ETag the read carried (F23). A node that moved under this command is
// re-read and the drop is computed again — the second admin to write may have
// added or removed grants of their own, and landing the picture this command
// read would undo them — and a third attempt is refused rather than retried
// forever, which is foldFlagLists' rule and for its reason.
func offboardWriteRoster(ctx context.Context, db *rtdb.Client, node accesslib.Node, member string) error {
	for range 3 {
		next, _ := offboardDropGrants(node.Grants, member)
		err := accesslib.WriteIfMatch(ctx, db, next, node.ETag)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, rtdb.ErrPrecondition):
			again, rerr := accesslib.Read(ctx, db)
			if rerr != nil {
				return remote(rerr)
			}
			node = again
		case errors.Is(err, rtdb.ErrPermission):
			return userErr("nothing was offboarded: %s refused the write — taking a member off the roster needs roles/firebasedatabase.admin, held by the project's owners, editors and budget admins: %v", accesslib.PathNode, err)
		default:
			return remote(err)
		}
	}
	return userErr("the roster was written by another admin every time this one tried to take %s off it: nothing was offboarded — run the command again", safetext.Field(member))
}

// offboardSweep deletes the member's whole prefix through the installation's
// intake (DS2.3's seam, the one every call of that family goes through), and
// answers the names it removed.
//
// The call names the member by their ADDRESS and the sweep is the intake's own:
// this is the admin-only form of the delete route (DS2.4), the containers are
// found by the owner annotation the intake wrote when they were stored (R10),
// and the deletion is made by the intake's account, which is the only writer
// that holds one under the two prefixes. Nothing here reads a value, and the
// answer carries names and no version's payload.
func offboardSweep(ctx context.Context, o cloudOptions, member, address string) ([]string, error) {
	serviceURL, err := secretsIntakeServiceURL(ctx, o)
	if err != nil {
		return nil, err
	}
	var answer intake.DeleteAnswer
	if err := secretsIntakeDo(ctx, serviceURL, secretsIntakeCall{
		Path: intake.DeletePath,
		What: "a member's stored secrets are removed",
		Body: intake.DeleteRequest{Scope: intake.ScopeUser, Owner: address},
		Out:  &answer,
	}); err != nil {
		return nil, err
	}
	return answer.Deleted, nil
}

// offboardIsAdmin reports whether member holds a project-scope admin grant of
// the roster: the rule the command's own check and its test share, so the two
// cannot spell "an admin" two ways.
func offboardIsAdmin(grants []accesslib.Grant, member string) bool {
	return accesslib.EffectiveAtProject(accesslib.Input{Grants: grants}, member) == accesslib.RoleAdmin
}
