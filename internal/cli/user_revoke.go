package cli

// user_revoke.go is the membership commands' own half of the ground-rules
// deletion gating (DG5, D3454): the commands that take a person's membership
// away revoke the IAM grants they can reach AT ONCE, IAM first and the list or
// roster edit second, and record the person's removal in the roster so that a
// later `fugaro update` admits the removal of any grant the command could not
// reach.
//
// WHY THE COMMANDS AND NOT `update`. The lists these commands edit are what a
// machine's next `fugaro update` applies, and the grants node is what an
// update derives them from: a person removed from either is, until that update
// runs, still bound in IAM. D3454 answers that the removal is the command's
// act, made from the credentials it already holds, so a person taken off the
// installation loses their cloud access now rather than at the next converge.
// The roster's record is the second half of that answer: a grant the command
// cannot reach — a removed workspace admin's grants on the member projects the
// workspace holds (D2818), which no workspace command reaches — is removed by
// the owning member's own `update`, and only because the record says the
// removal was intended.
//
// THE RECORD IS A SIBLING NODE OF THE ROSTER. It sits at PathRemoved, beside
// access.PathNode and access.PathMigrated, under the same fence config/admin
// carries (read and write denied to every client token, reached over IAM), and
// it holds the `user:<address>` members that have been removed, each in the
// spelling the command that removed it held — offboarding and a grant fold the
// address through access.Member, `fugaro user rm` keeps the case its argument
// carried, because the lists it edits hold that spelling and an exact match
// against it is what removes the member. No canonicalisation is done to the
// record, and none is needed: the READERS are what make one address one person,
// and every one of them folds the case (RemovalRecorded, dropMemberFromBindings).
// It is the one thing DG0's classifier reads as `people []string`: an IAM removal
// of a person's grant is admitted only when the record names that person, and
// refused in one line naming the command to re-run otherwise.
//
// WHAT IS REACHED, AND WHAT IS NOT. A command revokes a person's grants on the
// project ITS OWN CONFIG names — the installation for `fugaro user …`, the
// workspace for its own admin binding (`Authority.RemoveAdmin`). A member
// project's IAM is nobody else's to write (D2801, D2818): the workspace records
// the removal and the member's own update applies it. An installation with no
// Resource Manager endpoint to reach (a hermetic fake, or a project with no
// cloud behind it) revokes nothing and says so by making no call.
//
// A RE-RUN CONVERGES. The IAM write is a removal, so a second run finds nothing
// to take away and the node write proceeds: a command that failed between the
// two (the ignition revoked, the list or roster edit not yet written) is finished
// by running the same command again.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// PathRemoved is where a project's roster records the people it has removed: a
// sibling of access.PathNode and access.PathMigrated, in the same document as
// neither. DG0's classifier reads it as the `people` a person's grant removal is
// admitted against (DG5, D3454).
const PathRemoved = "config/admin/access_removed"

// removalRecordAttempts bounds one write of the record (F23's rule, the one
// every conditional write of the roster family takes): a node that moved under
// this writer costs a re-read and a re-judgement, and a node that keeps moving
// is a busier installation than one command should wait out.
const removalRecordAttempts = 3

// removalDoc is the record's wire shape: one object with one `removed` array of
// `user:<address>` members, folded on read rather than written folded. The name
// is new (CLAUDE.md, "a field never changes meaning"): `removed` is neither
// `grants` nor `grants_removed`, and the record is its own node rather than a
// fourth field of the grants node, whose own reader refuses a document it does
// not know whole.
type removalDoc struct {
	Removed []string `json:"removed"`
}

// revokeMemberGrants is the IAM half: it removes one member from every binding
// of the installation's own project policy and answers how many bindings it
// changed. It is a variable because a test's own function stands here, so no
// test needs a live Resource Manager call — and a test that wants the real
// function drives it against internal/gcpfake (TestUserRmRevokesAtOnce).
var revokeMemberGrants = revokeMemberGrantsOn

// memberRevokeAtOnce is the one sequence every membership command runs, in the
// order the acceptance pins: the reachable IAM grants first, and the roster's
// record of the removal second. db is the roster the caller already opened, or
// nil and a client is made for it here when the installation has one; an
// installation with no database has no roster to record into and records nothing.
//
// The two halves are ordered so a partial failure leaves the safe state: an IAM
// grant revoked but not yet recorded means a re-run records it, and the record
// never names a person still bound (which would admit an unexplained removal).
func memberRevokeAtOnce(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, member string) (int, error) {
	revoked, err := revokeMemberGrants(ctx, lc, member)
	if err != nil {
		return 0, err
	}
	if err := recordMemberRemoval(ctx, lc, db, member); err != nil {
		return 0, err
	}
	return revoked, nil
}

// recordMemberRemoval appends member to the installation's record of removed
// people, under the ETag the read carried (F23). A member already recorded is a
// no-op, so a re-run converges.
func recordMemberRemoval(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, member string) error {
	client, err := removalRecordClient(ctx, lc, db)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	for attempt := 0; attempt < removalRecordAttempts; attempt++ {
		var doc removalDoc
		etag, _, err := client.GetETag(ctx, PathRemoved, &doc)
		if err != nil {
			return remote(fmt.Errorf("reading the removal record at %s: %w", PathRemoved, err))
		}
		if slices.ContainsFunc(doc.Removed, func(m string) bool { return strings.EqualFold(m, member) }) {
			return nil
		}
		doc.Removed = append(doc.Removed, member)
		slices.Sort(doc.Removed)
		err = client.PutIfMatch(ctx, PathRemoved, etag, doc)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, rtdb.ErrPrecondition):
			continue
		default:
			return remote(fmt.Errorf("recording %s's removal at %s: %w", safetext.Field(member), PathRemoved, err))
		}
	}
	return remote(fmt.Errorf("the removal record at %s moved under this writer %d times: run the command again", PathRemoved, removalRecordAttempts))
}

// removalRecordClient is the database the record is written through: the one the
// caller already holds, else a client of the installation's own budget database,
// else nil — a project with no Firebase backend has no roster node to record
// into and records nothing (an installation whose roles ARE its IAM lists).
func removalRecordClient(ctx context.Context, lc *localcfg.Config, db *rtdb.Client) (*rtdb.Client, error) {
	if db != nil {
		return db, nil
	}
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, nil
	}
	return newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
}

// ReadRemoved is the roster's record of removed people, read off the database
// DG0's classifier reads it from. An absent node is no removal ever recorded,
// which is not an error.
func ReadRemoved(ctx context.Context, db *rtdb.Client) ([]string, error) {
	var doc removalDoc
	if _, _, err := db.GetETag(ctx, PathRemoved, &doc); err != nil {
		return nil, remote(fmt.Errorf("reading the removal record at %s: %w", PathRemoved, err))
	}
	return doc.Removed, nil
}

// RemovalRecorded reports whether the record names member, by the same folded
// comparison every reader of a member text uses. It is DG0's classifier's own
// rule: an IAM removal of a person's grant is admitted when this answers true,
// and refused otherwise.
func RemovalRecorded(removed []string, member string) bool {
	return slices.ContainsFunc(removed, func(m string) bool { return strings.EqualFold(m, member) })
}

// UnexplainedGrantRemoval is the refusal an update gives a person's IAM grant
// removal the roster does not record: one line, naming the command to re-run,
// because the removal is a membership command's act and an update that met one
// nobody made has no way to tell it from a mistake.
func UnexplainedGrantRemoval(project, member, command string) error {
	return userErr("no removal of %s on %s was recorded: run %s, then update again",
		safetext.Field(member), safetext.Field(project), command)
}

// revokeMemberGrantsOn is the real IAM removal: it reads the project's policy at
// version 3 (so a conditional binding is not silently dropped from the answer),
// takes member out of every binding, and writes the policy back conditionally on
// the ETag the read carried — so a policy another admin changed in the meantime
// is refused and the command can be run again rather than overwriting their
// change.
//
// It answers 0 and no error when there is nothing to reach: a project with no
// name, a member that is not one, an installation whose only endpoints are
// hermetic fakes, and a policy that does not bind the member at all are all
// "nothing was revoked" rather than a failure.
func revokeMemberGrantsOn(ctx context.Context, lc *localcfg.Config, member string) (int, error) {
	if lc == nil || lc.GCPProject == "" || member == "" {
		return 0, nil
	}
	// A no_auth installation that names no Resource Manager endpoint has no cloud
	// to write the policy on at all; nothing is attempted (config_publish_managed.go's
	// own rule for the same reason).
	if lc.Endpoints.NoAuth && lc.Endpoints.ResourceManager == "" {
		return 0, nil
	}
	svc, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return 0, remote(fmt.Errorf("reaching %s's IAM policy: %w", safetext.Field(lc.GCPProject), err))
	}
	pol, err := svc.Projects.GetIamPolicy(lc.GCPProject, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if err != nil {
		return 0, remote(fmt.Errorf("reading the IAM policy of %s: %w", safetext.Field(lc.GCPProject), err))
	}
	next, revoked := dropMemberFromBindings(pol.Bindings, member)
	if revoked == 0 {
		return 0, nil
	}
	// The policy READ is what is written back, with the bindings replaced and
	// nothing else touched: SetIamPolicy replaces the whole policy, so a policy
	// built from the bindings alone would drop the policy's auditConfigs — the
	// services whose data-access logs the project logs (asserted by
	// TestUserRmKeepsTheProjectsAuditConfigs).
	pol.Bindings = next
	if _, err := svc.Projects.SetIamPolicy(lc.GCPProject, &crm.SetIamPolicyRequest{
		Policy: pol,
	}).Context(ctx).Do(); err != nil {
		return 0, remote(fmt.Errorf("revoking %s's IAM grants on %s: %w", safetext.Field(member), safetext.Field(lc.GCPProject), err))
	}
	return revoked, nil
}

// dropMemberFromBindings is a policy without one member: the member is taken out
// of every binding that names them, and a binding left with nobody is dropped
// whole. The comparison folds case, because an address's case is not part of who
// it is (sameUserMemberFold's own reason) and IAM hands a member back in the
// spelling the grant was made with.
func dropMemberFromBindings(bindings []*crm.Binding, member string) ([]*crm.Binding, int) {
	out := make([]*crm.Binding, 0, len(bindings))
	revoked := 0
	for _, b := range bindings {
		kept := make([]string, 0, len(b.Members))
		for _, m := range b.Members {
			if strings.EqualFold(m, member) {
				revoked++
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) == 0 {
			continue
		}
		b.Members = kept
		out = append(out, b)
	}
	return out, revoked
}
