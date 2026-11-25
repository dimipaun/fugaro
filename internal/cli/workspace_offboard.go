// workspace_offboard.go is `fugaro user offboard`'s core for a member of a
// workspace (WU9, F13, D2813, D2818).
//
// WHY IT IS A CORE AND NOT A SECOND COMMAND. On one installation, offboarding
// is an admin's act on one roster (offboard.go). In a workspace the same act
// is owed to EVERY member: a person holds a stored grant in each member's
// roster and, beside it, a personal credential under their own prefix in each
// member's credentials service, and nothing of the workspace's reaches either
// on their behalf (D2801, D2818). So the act takes the member its caller names
// -- workspace.MemberTarget, the member's GCP project and its Firebase
// project, which are one project only sometimes (F2, R10) -- and this file is
// that core. It is not a command: WU7's `fugaro user rm` calls it, I1 adapts
// it onto MemberOps.Offboard, and the reporter is the caller's (D1354).
//
// THE FOUR THINGS AN OFFBOARDING OF A MEMBER DOES, in the order that fails
// safe:
//
//  1. It places the CALLER, from the member's own roster. The caller holds a
//     project-scope admin grant of it (F17.3, D1940) -- an implicit project
//     admin holds no stored grant and is refused, as the member's own intake
//     refuses them -- and, the half a single installation has no counterpart
//     for, that grant must already have reached the member's DERIVED admin
//     list: the list the member's own `fugaro update` publishes out of its
//     roster, and the one that member's intake checks a shared-prefix writer
//     against (D2818, D1940). A stored grant that has not reached it is a
//     member whose update is pending, and this refuses there -- naming the
//     member whose `fugaro update` is owed -- and writes and sweeps nothing.
//     That is deliberately stricter than the single installation's order,
//     which lands the roster half and is refused by the intake second
//     (offboard.go says why): here the pending list is readable up front, so
//     the half-done state is avoidable, and an admin who cannot sweep has no
//     business taking the grant off first.
//  2. It drops every grant of the member's roster that names the person and
//     writes the roster back CONDITIONALLY on the ETag the read carried, so
//     two admins editing one member cannot lose each other's work (D2817,
//     F23): a node another writer moved is read again and the drop is
//     computed again, and a third attempt is refused rather than retried
//     forever.
//  3. It creates the entry in the MEMBER's own trail (PathMemberAudit,
//     D2811). A project admin holds no write on the workspace's project at all
//     (F11, D2813), so the member's trail is where a change to the member is
//     recorded, and it is created ONCE -- the write is create-only, so an
//     entry is never replaced. It is written as soon as the roster half has
//     landed and BEFORE the sweep, and that order is the point: an entry that
//     cannot be amended cannot carry the sweep's own answer, and what the
//     trail has to hold is the change that has consequences -- the dropped
//     grant the member's next `fugaro update` turns into bindings -- even when
//     the intake then refuses the sweep, which is the one half that leaves no
//     trace anywhere else.
//  4. It sweeps the person's own secrets through the member's intake
//     (memberSweeper): every container whose owner annotation is their address
//     is deleted by the intake's own account, and no value is read (R10,
//     DS2.3).
//
// WHAT IT DOES NOT DO is offboard.go's list, unchanged: no IAM write here (the
// member's own `fugaro update` takes the invoker grants off, F20), no end to
// an open slip (the service has no route that ends one, G10), and no policy
// published (a pool's text naming the person is inert once the roster does not
// place them).
//
// BESIDE THE MEMBERS' SWEEPS, ONE MORE INSTALLATION IS SWEPT. A person's token
// is stored once, at the workspace's own project (D2960), so `workspace user rm`
// deletes the containers they own there too (F21) -- not through this core, which
// is one member's, but through the workspace's own intake, and after the members'
// sweeps and whatever they answered (offboardWorkspacePerson, workspaceSweeper).
//
// NOTHING HERE PRINTS, and that is a decision rather than an omission: the
// caller owns the report (WU7's `user rm`, I1's command), and the record of
// the act is the audit entry this creates. A core that printed would print
// under a caller that prints too.

package cli

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// memberSweeper deletes the secrets one person stored on one member project.
//
// The delete goes to the MEMBER's own intake in its admin-only form (DS2.3,
// DS2.4), and the address of that intake is the member's: a workspace admin's
// own config carries the workspace's credentials service URL, whose service
// label resolves the workspace's own intake and never a member's
// (offboardSweep, intakeURLOf). So reaching a member's intake is not something
// this file can do with the local config, and the seam is what carries it: the
// side that already reaches the member project provides it -- WU4's writer,
// adapted by I1 -- and both sides make the same one call, offboardSweepVia,
// which is the delete offboard.go's single-installation sweep also makes.
type memberSweeper interface {
	// Sweep deletes every container whose owner annotation is address on the
	// member's own intake and answers the names it removed. The address is the
	// one a person knows and not the roster's `user:` spelling, exactly as the
	// single installation's sweep takes it.
	Sweep(ctx context.Context, m workspace.MemberTarget, address string) ([]string, error)
}

// workspaceSweeper deletes the containers one person stored on the WORKSPACE's
// own project.
//
// It is memberSweeper's pattern for the other installation an offboarding
// reaches (F21): a person's token is stored once, at the workspace (D2960), so
// `workspace user rm` deletes every container they own there beside the members'
// sweeps, and the delete is the intake's own admin-only route (DS2.3, DS2.4) --
// the one call offboardSweepVia makes, at the workspace's own intake instead of
// a member's. The workspace's intake is reached at the address the workspace
// admin's own config carries (intakeURLOf, the project the workspace is named
// after, D2821), which is never a member's.
//
// The address is the person's own and the answer is the names the delete
// removed: nothing here reads a value, and no name travels in a log, an error or
// a header.
type workspaceSweeper interface {
	// Sweep deletes every container whose owner annotation is address at the
	// workspace's own intake and answers the names it removed.
	Sweep(ctx context.Context, address string) ([]string, error)
}

// memberOffboarder is the core itself: the seam to the member (C2), the seam
// to that member's own intake, and a clock. Every judgement it makes is this
// tree's own -- access's resolution, the roster's write rules, the audit
// entry's shape.
type memberOffboarder struct {
	ops   workspace.MemberOps
	sweep memberSweeper
	// at stamps the audit entry. It is a field so that a test reads the entry
	// it asserts on rather than the second the suite happened to run in.
	at func() time.Time
}

// newMemberOffboarder returns the core for a member reached through ops and
// its own intake through sweep. I1 constructs it; a test constructs its own
// with fakes.
func newMemberOffboarder(ops workspace.MemberOps, sweep memberSweeper) *memberOffboarder {
	return &memberOffboarder{ops: ops, sweep: sweep, at: func() time.Time { return time.Now().UTC() }}
}

// Offboard takes one person off one member project: their grants dropped from
// the member's roster, the act created in the member's own trail, and the
// secrets they stored under their own prefix swept by the member's intake. It
// is MemberOps.Offboard's implementation (C2), and it refuses -- writing
// nothing and sweeping nothing there -- until the caller's stored admin grant
// has reached the member's derived admin list (D2818, D1940).
//
// It takes the address a person knows, as the single installation's command
// does, and not the roster's `user:` spelling: an argument written as
// `user:<email>` names nobody and is refused rather than folded into a second
// one (offboardMember).
func (o *memberOffboarder) Offboard(ctx context.Context, m workspace.MemberTarget, address string) error {
	// The refusal every cloud write of this tree makes, before anything is
	// read: a coding agent's session holds the credential this writes the
	// member's roster with, and a run's own process neither holds that write
	// nor may delete a person's credentials. The core has no terminal of its
	// own, so the question is asked with terminal=false, which is the answer
	// that fails safe: an IDE terminal's marker is a coding agent's session
	// here rather than a person's terminal, and the command that owns the
	// stdin asks it again with the real answer (userOffboard).
	if err := offboardRefusal(os.Getenv, false); err != nil {
		return err
	}
	me, err := offboardCaller(ctx)
	if err != nil {
		return userErr("nothing was offboarded on %s: offboarding a member is a workspace admin's act and the member's own roster is what records its admins, so the command has to know which member this invocation runs as, and it could not read the identity the Google credential belongs to: %v", offboardMemberNamed(m), err)
	}
	person, address, err := offboardMember(address)
	if err != nil {
		return err
	}

	doc, err := o.ops.ReadGrants(ctx, m)
	if err != nil {
		return offboardMemberRemote(m, "reading the member's grants node", err)
	}
	grants, err := offboardMemberGrants(m, doc)
	if err != nil {
		return err
	}
	if !offboardIsAdmin(grants, me) {
		return offboardMemberNeedsAdmin(m, me)
	}
	// Self-removal is refused before the write, as it is on one installation
	// and for the same reason: the caller holds the admin grant that admitted
	// them, so this is the one act from here that can empty the member's
	// published admin list, and F17.2 gives it `fugaro user rm`'s answer.
	if me == person {
		return userErr("nothing was offboarded on %s: %s is the identity this invocation is running as, and an admin does not take themselves off a member's roster — the roster is who can be told to settle the member (run its update, add a member, offboard one), so another admin has to offboard you, or a project Owner from outside the roster (F17.2): nothing was written to %s's grants node, and none of your stored secrets were swept there",
			offboardMemberNamed(m), safetext.Field(address), offboardMemberNamed(m))
	}
	if err := o.offboardMemberPending(ctx, m, grants, me, address); err != nil {
		return err
	}

	dropped, err := o.offboardDropRoster(ctx, m, doc, grants, person)
	if err != nil {
		return err
	}
	if err := o.offboardRecord(ctx, m, me, person, dropped); err != nil {
		return err
	}
	if _, err := o.sweep.Sweep(ctx, m, address); err != nil {
		return remote(fmt.Errorf("%s's grants are off %s's roster (%d removed) and the offboarding is recorded in that member's audit trail, but its intake did not sweep the secrets %s stored under their own prefix: %w",
			safetext.Field(person), offboardMemberNamed(m), dropped, safetext.Field(address), err))
	}
	return nil
}

// offboardWorkspacePerson is one person's removal from a workspace as the two
// sweeps F21 puts in order: every member the workspace holds is swept first
// (members.Offboard, which is this file's core, once per member), and then the
// workspace's OWN intake is swept, whatever the members answered.
//
// THE WORKSPACE'S SWEEP RUNS WHATEVER THE MEMBERS ANSWERED, and that is the
// point rather than an oversight: a person's token is stored once, at the
// workspace (D2960), so a member whose own update is pending -- a refusal this
// command has always reported and half-removed nobody on -- would leave the
// person's token behind at the workspace if it gated the workspace's sweep. The
// two answers are carried apart: the caller reports the member's refusal (the
// FIRST one, as the loop before this change returned it) and reports the
// workspace's own answer after the node is written, so a sweep that failed does
// not hide a member that refused and a member that refused does not hide the
// workspace's containers still being there.
//
// The member's answer is a refusal and not a stop: every member is attempted, so
// one refusal does not leave the members after it unswept, and the first refusal
// is the one a person reads.
func offboardWorkspacePerson(ctx context.Context, members workspace.MemberOps, sweep workspaceSweeper, linked []workspace.MemberTarget, address string) ([]string, error, error) {
	var memberErr error
	for _, m := range linked {
		if err := members.Offboard(ctx, m, address); err != nil && memberErr == nil {
			memberErr = fmt.Errorf("offboarding %s from %s: %w", safetext.Field(address), safetext.Field(m.Project), err)
		}
	}
	deleted, sweepErr := sweep.Sweep(ctx, address)
	return deleted, memberErr, sweepErr
}

// offboardMemberGrants reads the member's grants out of the document its
// database holds, and refuses the two states a read of it can be in without
// granting anybody anything: a node that is not there, and bytes this release
// cannot read.
//
// Neither is "everyone", and neither is a member this command may write: the
// first is an installation whose lists its own `fugaro update` has not
// migrated into the node (access's own reading of an absent node), and the
// second is a document this release cannot vouch for, which is exactly the
// document a judgement about a change must not be made on.
func offboardMemberGrants(m workspace.MemberTarget, doc workspace.MemberDoc) ([]accesslib.Grant, error) {
	if !doc.Found {
		return nil, userErr("nothing was offboarded on %s: it has no grants node yet (%s is absent), so no stored grant places anybody there and offboarding is a member admin's act on it (D1940). The node is what the member's own `%s update` writes out of its lists, and a workspace admin's grant is a row in it: run `%s update` on %s first",
			offboardMemberNamed(m), accesslib.PathNode, selfCommand(), selfCommand(), offboardMemberNamed(m))
	}
	grants, err := accesslib.Decode(doc.Raw)
	if err != nil {
		return nil, userErr("nothing was offboarded on %s: its grants node (%s) is not a document this release reads, so a change to it cannot be judged, and a reader that acted on part of one would be acting on a roster it only partly understood (F27): %v",
			offboardMemberNamed(m), accesslib.PathNode, err)
	}
	return grants, nil
}

// offboardMemberPending is D2818's gate, and it is the whole difference between
// this core and the single installation's command: a workspace admin's stored
// grant is a row in the member's roster, and the member's own `fugaro update`
// is what publishes it -- as the derived admin list (access.PathAdminList,
// D1940) and, out of that, as the bindings.
//
// The comparison is PendingApply's and not a second reading of the member's
// objects here (C2): the want is the admin list the roster derives, and the
// answer's Admins are the entries of it the member does not carry yet. A
// caller among them is a caller whose grant the member has not applied, so the
// member's intake would refuse the shared-prefix write this offboarding is for
// (D1940) and the member's next `fugaro update` is what closes the gap -- so
// nothing is written and nothing is swept there, and the refusal names it.
//
// What it does NOT refuse on is Pending.NotMeasured: the marks are about
// viewers and repository-scope grants, which no output carries and which this
// act does not rest on, while the admin list it does rest on is compared.
func (o *memberOffboarder) offboardMemberPending(ctx context.Context, m workspace.MemberTarget, grants []accesslib.Grant, me, address string) error {
	admins := accesslib.Admins(grants)
	want := workspace.ApplyWant{Admins: make([]string, 0, len(admins))}
	for _, a := range admins {
		want.Admins = append(want.Admins, a.Email)
	}
	pending, err := o.ops.PendingApply(ctx, m, want)
	if err != nil {
		return offboardMemberRemote(m, "comparing the member's derived admin list with the grants its roster states", err)
	}
	if !slices.Contains(pending.Admins, strings.TrimPrefix(me, "user:")) {
		return nil
	}
	return userErr("nothing was offboarded on %s: %s is a stored project-scope admin of its roster, but that grant has not reached %s's derived admin list yet (%s), and the member's own `%s update` is what publishes it from the roster (D2818, D1940) — so the member's intake would not admit the sweep this offboarding makes, and nothing on %s was written or swept: run `%s update` on %s, and run this again",
		offboardMemberNamed(m), safetext.Field(me), offboardMemberNamed(m), accesslib.PathAdminList, selfCommand(), offboardMemberNamed(m), selfCommand(), offboardMemberNamed(m))
}

// offboardDropRoster is step 2: the member's roster as this offboarding leaves
// it, written under the ETag the read carried, with offboardWriteRoster's
// three-attempt rule (F23) and its last-admin rule read of the node that would
// land (F17.2) -- the same two rules, over the same format, through the
// member's own write instead of a database client this machine holds.
func (o *memberOffboarder) offboardDropRoster(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, grants []accesslib.Grant, person string) (int, error) {
	for range 3 {
		next, dropped := offboardDropGrants(grants, person)
		if dropped == 0 {
			return 0, nil
		}
		if err := offboardNoAdminLeft(m.Project, person, grants, next, true); err != nil {
			return 0, err
		}
		raw, err := accesslib.Encode(next)
		if err != nil {
			return 0, remote(fmt.Errorf("encoding %s for %s: %w", accesslib.PathNode, offboardMemberNamed(m), err))
		}
		err = o.ops.WriteGrants(ctx, m, doc, raw)
		switch {
		case err == nil:
			return dropped, nil
		case errors.Is(err, rtdb.ErrPrecondition):
			// Another writer landed between the read and this write: the picture
			// this drop was judged against is not the picture it would be written
			// over, so it is read again and judged again, from nothing.
			again, rerr := o.ops.ReadGrants(ctx, m)
			if rerr != nil {
				return 0, offboardMemberRemote(m, "reading the member's grants node again after another admin moved it", rerr)
			}
			if !again.Found {
				return 0, nil
			}
			grants, rerr = accesslib.Decode(again.Raw)
			if rerr != nil {
				return 0, offboardMemberGrantsErr(m, rerr)
			}
			doc = again
		case errors.Is(err, rtdb.ErrPermission):
			return 0, userErr("nothing was offboarded on %s: %s refused the write of its grants node — a workspace admin changes a member's roster with the workspace's own authority over that project, which is what `link` gives it (roles/owner on the workspace's folder, or, on the SA mechanism, the workspace IAM account's owner grant), and no binding on a member is written outside the member's own Terraform (D2800, D2801): %v",
				offboardMemberNamed(m), accesslib.PathNode, err)
		default:
			return 0, offboardMemberRemote(m, "writing the member's grants node", err)
		}
	}
	return 0, userErr("the grants node of %s was written by another admin every time this one tried to take %s off it: nothing was offboarded — run the command again", offboardMemberNamed(m), safetext.Field(person))
}

// offboardRecord is step 3: one create-only entry in the member's own trail
// (PathMemberAudit, D2811), created under an id minted for it (C1's
// NewAuditEventID), in the shape every reader of that trail reads back.
func (o *memberOffboarder) offboardRecord(ctx context.Context, m workspace.MemberTarget, me, person string, dropped int) error {
	at := o.at().UTC()
	id, err := workspace.NewAuditEventID(at, crand.Reader)
	if err != nil {
		return remote(fmt.Errorf("minting the name of the audit entry for %s on %s: %w", safetext.Field(person), offboardMemberNamed(m), err))
	}
	// The entry is C1's own event (contract.md, "The audit event"): the trail a
	// member's database carries is the same document the workspace's own
	// Firestore trail holds, so its readers -- `status`, the drift check -- read
	// an offboarding back with the reader they read every other change with. The
	// actor and the subject are addresses and never the roster's "user:" member
	// spelling (C1's Validate refuses one).
	event, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      id,
		Time:    at,
		Actor:   strings.TrimPrefix(me, "user:"),
		Action:  workspace.ActionUserOffboard,
		Project: m.Project,
		Subject: strings.TrimPrefix(person, "user:"),
	})
	if err != nil {
		return remote(fmt.Errorf("rendering the audit entry for %s on %s: %w", safetext.Field(person), offboardMemberNamed(m), err))
	}
	if err := o.ops.Audit(ctx, m, id, event); err != nil {
		return remote(fmt.Errorf("%s's grants are off %s's roster (%d removed), but the offboarding could not be created in that member's audit trail (%s): %w",
			safetext.Field(person), offboardMemberNamed(m), dropped, workspace.PathMemberAudit, err))
	}
	return nil
}

// offboardMemberNamed is the member as these refusals name it: both of its
// projects when its Firebase project is a project of its own, as MemberTarget
// travels them (F2, R10). Both are escaped, as every name this family prints
// is, and neither is a secret.
func offboardMemberNamed(m workspace.MemberTarget) string {
	if m.Firebase == "" {
		return safetext.Field(m.Project)
	}
	return fmt.Sprintf("%s (Firebase %s)", safetext.Field(m.Project), safetext.Field(m.Firebase))
}

// offboardMemberNeedsAdmin is the refusal a caller who holds no project-scope
// admin grant of the MEMBER's roster gets. It names the member and the
// direction an admin comes from: a workspace admin's grant is a row of that
// roster, written by `fugaro workspace user grant`, and a project's implicit
// admins -- its GCP owners and editors, its budget admins, and the workspace
// admin who is not on this member's roster -- hold no stored grant and are
// refused here and at the member's intake alike (F17.3, D1940).
func offboardMemberNeedsAdmin(m workspace.MemberTarget, me string) error {
	return userErr("nothing was offboarded on %s: %s is not a project-scope admin in that member's grants node (%s), and offboarding a member is an act on that member's roster. A workspace admin's authority there is a stored grant of the member's own roster, which `%s workspace user grant` writes; the member's implicit admins — its GCP owners and editors, its budget admins — hold no stored grant and are refused here and at its intake alike (F17.3, D1940)",
		offboardMemberNamed(m), safetext.Field(me), accesslib.PathNode, selfCommand())
}

// offboardMemberRemote classifies an error of a member call: the member's
// database answered something this command could not read or could not reach,
// which is a remote failure and never a refusal of the person's.
func offboardMemberRemote(m workspace.MemberTarget, what string, err error) error {
	return remote(fmt.Errorf("%s while offboarding on %s: %w", what, offboardMemberNamed(m), err))
}

// offboardMemberGrantsErr is offboardMemberGrants' malformed-document refusal
// for the re-read inside the write's retry, where the member and the node are
// already known.
func offboardMemberGrantsErr(m workspace.MemberTarget, err error) error {
	return userErr("nothing was offboarded on %s: its grants node (%s) is not a document this release reads, so a change to it cannot be judged, and a reader that acted on part of one would be acting on a roster it only partly understood (F27): %v",
		offboardMemberNamed(m), accesslib.PathNode, err)
}
