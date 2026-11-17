// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Self-serve deletion (portal plan P2.4; D2195, spec §Legal and privacy, F17).
//
// Two acts, and they are a person's own: deleting an organisation they own, and
// deleting their account.
//
// **Deleting an organisation ends everything it is running first and removes its
// records second.** The merchant of record is asked to cancel each of the
// organisation's subscriptions before a record of it moves, because a
// cancellation that failed after the deletion landed is a customer who is gone
// with a card still being charged and nobody left who can see it; a failure there
// refuses the whole request with nothing done, and the same request finishes the
// job. Then every entitlement the organisation holds is revoked (F29's own rule:
// the licence and every key it ever minted, whatever minted it), and only then
// are the records removed — the memberships, the invitations, the workspaces and
// the organisation itself.
//
// **What is kept is the sales record.** The checkout transactions, the
// merchant's origin records, the raw events, the ledger and the subscriptions
// stay where they stand (spec §Legal: "retaining only sales records legally
// required", and D457 — how long they are kept and whether their buyer fields
// are redacted on deletion — is counsel's and still open). The entitlements stay
// too, revoked: what a customer bought, for how many seats and until when is
// what a report is made of (P5.2), and a revoked licence is a record of a sale
// that ended rather than a live grant. Nothing here redacts an address, for
// D457's reason: redaction is the ruling the owner has not made.
//
// **Deleting an account is refused while the person is the only owner of an
// organisation** (D2195). An organisation with no owner is one nobody can invite
// into, buy for, bind or delete, and the person has not left the portal in that
// case so much as locked it: what they do instead is delete the organisation, or
// make somebody else an owner, and then delete their account. The refusal is
// checked on the memberships and not on a role in a token, which is why it holds
// across sign-ins.
//
// What an account deletion removes is the person's memberships and every
// invitation bound to their address — the two records that hold it — and then
// their Firebase Auth user, through the Identity Toolkit's `accounts:delete`.
// The sign-in goes last on purpose: it is the credential, and a request that
// fails after the records are gone is one the person can still sign in and make
// again, where a request that deleted the user first and then failed would leave
// records nobody can reach and an account nobody can retry.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// ReasonDeletion is the store's reason for a key withdrawn because the
	// organisation that held it was deleted. It is its own reason and not a
	// refund's: the money did not go back, and a person reading a withdrawal
	// later is asking which of the two happened.
	ReasonDeletion = "deletion"

	// identityToolkitRoot is the Identity Toolkit's API root (§6's own
	// endpoint, the one the history job's account sweep also reads).
	identityToolkitRoot = "https://identitytoolkit.googleapis.com"

	// identityToolkitScope is what the account deletion is authorized under.
	// The scope the history job's own Identity Toolkit client asks for.
	identityToolkitScope = "https://www.googleapis.com/auth/cloud-platform"

	// accountDeleteTimeout bounds one deletion call, which is a single request
	// that either removes one user or does not.
	accountDeleteTimeout = 30 * time.Second

	// accountDeleteBodyBytes bounds what is read of the Identity Toolkit's
	// answer: the call's answer is a user or an error, and one over this is one
	// this service has no use for.
	accountDeleteBodyBytes = 64 << 10
)

// The refusals a deletion makes, kept beside the acts that make them.
var (
	// errSoleOwner is D2195's rule: an account that is the only owner of an
	// organisation may not be deleted, because the organisation it would leave
	// behind is one nobody can act in.
	errSoleOwner = errors.New("keyservice: this account is the only owner of an organisation")
	// errDeletionNotPermitted is a caller who is not the organisation's owner.
	// Deleting an organisation cancels what it pays for and revokes what it
	// holds, and it is the one act in the portal that cannot be undone by
	// anybody but a person signing up again, so it is the owner's alone.
	errDeletionNotPermitted = errors.New("keyservice: only an organisation's owner may delete it")
	// errAccountNotDeleted is the Identity Toolkit's own failure: the sign-in
	// is still there, so the account is not gone and the request can be made
	// again.
	errAccountNotDeleted = errors.New("keyservice: the account's sign-in could not be deleted")
)

// accountDirectory is the Identity Toolkit admin API of one Firebase project, of
// which this file reaches the one call that removes a user: `accounts:delete`.
//
// It is a value carrying its own endpoint and client for D2205's reason, read at
// another seam: the answers the real API gives are the half of this call a test
// cannot check, so the boundary is visible here and a case installs a server of
// its own (deletion_test.go).
type accountDirectory struct {
	// endpoint is the API root (identityToolkitRoot), or a test server's.
	endpoint string
	// project is the Firebase project whose users this directory holds. It goes
	// into the request's path, so it is checked before it is used: a value with
	// a slash or a query in it is a value that names another address.
	project string
	client  *http.Client
}

// authUserRemoval is the one thing a deployment does to a customer's sign-in
// when their account is deleted, and it is a variable for the reason
// `probeInstallation` is: a case replaces it with a directory pointing at a
// server of its own (deletion_test.go), and nothing else in a build reaches
// Firebase Auth's admin API.
var authUserRemoval = func(ctx context.Context, uid string) error {
	directory, err := portalAccountDirectory(ctx)
	if err != nil {
		return err
	}
	return directory.delete(ctx, uid)
}

// portalAccountDirectory builds the deployment's directory: the project from the
// environment and the credentials from Application Default Credentials, which is
// what a Cloud Run revision's own service account gives it.
//
// The project is read from `FUGARO_PROJECT` through the deployment's own reader
// (DeployProject) and never guessed from the platform's environment, for the
// reason that reader states: the platform fills GOOGLE_CLOUD_PROJECT, and a
// service that fell back to it would delete users in another project the day the
// two differed. It is the variable the sign-in verifier's audience is pinned
// with, so the service cannot admit a sign-in from one project and delete the
// user in another.
func portalAccountDirectory(ctx context.Context) (accountDirectory, error) {
	project, err := DeployProject(os.Environ())
	if err != nil {
		return accountDirectory{}, err
	}
	tokens, err := google.DefaultTokenSource(ctx, identityToolkitScope)
	if err != nil {
		return accountDirectory{}, fmt.Errorf("keyservice: no credentials to delete an account's sign-in: %w", err)
	}
	return accountDirectory{
		endpoint: identityToolkitRoot,
		project:  project,
		client:   oauth2.NewClient(ctx, tokens),
	}, nil
}

// delete removes one Firebase Auth user. An account that is not there is the
// state this call is for and not a failure: the user is gone, which is what the
// caller asked for, and reading a second attempt as a failure would be a
// refusal with nothing left to do about it.
func (a accountDirectory) delete(ctx context.Context, uid string) error {
	if strings.TrimSpace(uid) == "" {
		return fmt.Errorf("%w: the account names no user", errAccountNotDeleted)
	}
	if a.project == "" || strings.ContainsAny(a.project, "/?#% ") {
		return fmt.Errorf("%w: %q is not a Firebase project id", errAccountNotDeleted, a.project)
	}
	body, err := json.Marshal(map[string]string{"localId": uid})
	if err != nil {
		return fmt.Errorf("%w: %v", errAccountNotDeleted, err)
	}
	ctx, cancel := context.WithTimeout(ctx, accountDeleteTimeout)
	defer cancel()
	address := strings.TrimRight(a.endpoint, "/") + "/v1/projects/" + a.project + "/accounts:delete"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", errAccountNotDeleted, err)
	}
	request.Header.Set("Content-Type", "application/json")
	answer, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", errAccountNotDeleted, err)
	}
	defer func() { _ = answer.Body.Close() }()
	held, err := io.ReadAll(io.LimitReader(answer.Body, accountDeleteBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: %v", errAccountNotDeleted, err)
	}
	switch {
	case answer.StatusCode == http.StatusNotFound, absentUser(string(held)):
		return nil
	case answer.StatusCode/100 != 2:
		// The status and not the body: an error body is the provider's own text
		// and this answer is logged, and a log line carrying a body the provider
		// wrote is a line nobody has reviewed (§7.2's rule, read at a seam this
		// service does not own).
		return fmt.Errorf("%w: the Identity Toolkit answered %s", errAccountNotDeleted, answer.Status)
	default:
		return nil
	}
}

// absentUser answers whether the Identity Toolkit's own error says the user it
// was asked about is not there. The code is the API's (USER_NOT_FOUND) and is
// matched rather than parsed, the way the project probe matches the database's
// two phrases.
func absentUser(answer string) bool {
	return strings.Contains(answer, "USER_NOT_FOUND")
}

// deleteOrganisation ends one organisation: the caller is its owner, the
// merchant is asked to cancel what it pays for, every licence it holds is
// revoked, and its records are removed.
func (s *Service) deleteOrganisation(ctx context.Context, actor Caller, organisationID string) error {
	org, membership, found, err := s.ownMembership(ctx, actor, organisationID)
	if err != nil {
		return err
	}
	if !found {
		// Not there and not theirs are one answer, api_orgs.go's rule and there
		// for the same reason: which organisation ids exist is a list of the
		// portal's customers.
		return errNoSuchOrganisation
	}
	if membership.Role != roleOwner {
		return errDeletionNotPermitted
	}
	// The merchant first and before any record moves: a cancellation that came
	// after the deletion would be one nobody could see. A failure here leaves
	// the organisation exactly as it was.
	if err := s.cancelSubscriptionsOf(ctx, org.ID); err != nil {
		return err
	}
	held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection,
		Where{Field: "organisation_id", Value: org.ID})
	if err != nil {
		return err
	}
	at := s.now()
	for _, entitlement := range held {
		if err := s.revokeLicence(ctx, entitlement, at); err != nil {
			return err
		}
	}
	return s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return deleteOrganisationRecords(ctx, tx, org.ID)
	})
}

// revokeLicence withdraws one licence and every key of it (F29), and leaves the
// record standing as a revoked one.
//
// The keys are withdrawn first and the licence last, which is the order
// `merchant.go`'s own withdrawal takes and for its reason: a store that fails on
// the licence's write leaves keys that are already dead and a licence a retry
// revokes again — this walk is idempotent — rather than a dead licence with live
// keys.
//
// The coverage is that file's own, step for step, and it is written out here
// rather than shared with it because the two are tasks of different areas (the
// plan's P2.4 gives this file and P4.2 gives that one) and because a deletion has
// no event to resolve: the licence is the whole of what the walk is handed.
//
// Every step is a route to a key the customer's installation may be running, and
// none of them subsumes another: a licence's own keys are the ones its bindings
// minted, a record naming the licence is one the reissue or the rotation wrote
// (whose source names the event and not the key it replaced, D2200), a record
// naming only the licence's SUBSCRIPTION is one written before the entitlement's
// id was on a record at all, and a rotation's record names the key it replaced
// and neither the licence nor the subscription.
func (s *Service) revokeLicence(ctx context.Context, entitlement WireEntitlement, at time.Time) error {
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	walk := []string{}
	revoke := func(keyID string) error {
		if seen[keyID] {
			return nil
		}
		seen[keyID] = true
		walk = append(walk, keyID)
		return s.store.Revoke(ctx, Revocation{KeyID: keyID, Reason: ReasonDeletion, At: at})
	}
	// The licence's own keys first, because they are the keys this licence
	// actually has: the binding's key, the one a seat change or a renewal
	// reissued, the key a trial mints (api_trial.go writes a binding's source and
	// no licence of its own) and the ones minted before P3.6 wrote the field at
	// all — licenceKeys reads the binding's own source as well.
	keys, err := s.licenceKeys(ctx, entitlement.ID)
	if err != nil {
		return err
	}
	for _, keyID := range keys {
		if err := revoke(keyID); err != nil {
			return err
		}
	}
	// Then every record that names this licence, whatever minted it and whatever
	// its source and subscription say (F29): a record the steps below cannot
	// follow — a comped or operations grant names no subscription at all — is
	// still a key the customer's installation may be running.
	for _, record := range entitlementRecords(records, entitlement.ID) {
		if err := revoke(record.KeyID); err != nil {
			return err
		}
	}
	// And every other issuance of the licence's own subscription, which is where
	// the keys of a licence sold before a record carried the entitlement's id
	// live: the origin record a purchase wrote names the subscription and the
	// licence, so the join back to the subscription is a query on the licence's
	// id. The empty subscription is not a licence — an unsold key names none, so
	// matching on "" would withdraw every hand-minted key there is.
	subscription, err := s.licenceSubscription(ctx, entitlement.ID)
	if err != nil {
		return err
	}
	if subscription != "" {
		for _, record := range records {
			if record.Subscription != subscription {
				continue
			}
			if err := revoke(record.KeyID); err != nil {
				return err
			}
		}
	}
	// A rotation's record names the key it replaced and not the licence, so the
	// step from a revoked key to what the endpoint minted from it is a lookup
	// rather than a scan: without it a successor of a rotated key is a live key
	// at an installation whose organisation no longer exists.
	successors := map[string][]string{}
	for _, record := range records {
		if from, rotation := strings.CutPrefix(record.Source, rotationPrefix); rotation {
			successors[from] = append(successors[from], record.KeyID)
		}
	}
	for len(walk) > 0 {
		keyID := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, next := range successors[keyID] {
			if err := revoke(next); err != nil {
				return err
			}
		}
	}
	return s.revokeEntitlement(ctx, entitlement)
}

// deleteOrganisationRecords removes everything one organisation owns that is not
// a sales record: its memberships, its invitations, its workspaces and the
// organisation itself.
//
// Every read is taken before the first write, which is what a transaction of this
// document store requires (docstore's ErrReadAfterWrite) and what makes the
// whole removal one commit: an organisation whose memberships were removed but
// whose record stayed would be one the portal lists for nobody and that nobody
// can delete again.
//
// The workspaces go with the organisation because nothing else reaches them: a
// workspace is the organisation's own object — a name it chose, and the Firebase
// project its licence is bound to — and after the organisation is gone nobody can
// read, bind or delete one, so leaving them is leaving a customer's own records
// behind after they asked for them to be gone. The entitlements stay, revoked,
// for the reason the file's own comment gives.
func deleteOrganisationRecords(ctx context.Context, tx Tx, organisationID string) error {
	members, err := recordsWhere[WireMembership](ctx, tx, membershipsCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		return err
	}
	invitations, err := recordsWhere[WireInvite](ctx, tx, invitesCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		return err
	}
	workspaces, err := recordsWhere[WireWorkspace](ctx, tx, workspacesCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		return err
	}
	for _, member := range members {
		if err := tx.Delete(ctx, membershipsCollection, membershipID(organisationID, member.UID)); err != nil {
			return err
		}
	}
	for _, invitation := range invitations {
		if err := tx.Delete(ctx, invitesCollection, invitation.ID); err != nil {
			return err
		}
	}
	for _, workspace := range workspaces {
		if err := tx.Delete(ctx, workspacesCollection, workspace.ID); err != nil {
			return err
		}
	}
	return tx.Delete(ctx, organisationsCollection, organisationID)
}

// deleteAccount ends one person's account. The reads the refusals are made of
// and the records that go are one transaction, so a second owner deleting their
// account at the same moment is a conflict this runs again against rather than a
// race that leaves an organisation owned by nobody.
func (s *Service) deleteAccount(ctx context.Context, actor Caller) error {
	if err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, err := membershipsOf(ctx, tx, actor.UID)
		if err != nil {
			return err
		}
		// D2195's refusal, and it is asked of the organisations themselves: a
		// membership that names an organisation the store no longer holds is a
		// record left by a deletion that already removed one, and nothing is
		// left to leave ownerless.
		for _, membership := range held {
			if membership.Role != roleOwner {
				continue
			}
			_, present, err := getRecord[WireOrganisation](ctx, tx, organisationsCollection, membership.OrganisationID)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			owners, err := membersOf(ctx, tx, membership.OrganisationID)
			if err != nil {
				return err
			}
			if countOwners(owners) <= 1 {
				return errSoleOwner
			}
		}
		invitations, err := recordsWhere[WireInvite](ctx, tx, invitesCollection, Where{})
		if err != nil {
			return err
		}
		for _, membership := range held {
			if err := tx.Delete(ctx, membershipsCollection, membershipID(membership.OrganisationID, actor.UID)); err != nil {
				return err
			}
		}
		// Every invitation bound to the address this sign-in proved, in whatever
		// state it reached: an invitation is the address and nothing else
		// (wire_invites.go), so an accepted one holds it as much as a pending one
		// does, and the membership it became is going with the rest. The
		// comparison is `access.Member`'s fold and not a string compare, which is
		// the rule an invitation is accepted under (F10) — two readers of one
		// address that folded differently would leave an invitation behind that
		// its own address could never accept again.
		//
		// The whole collection is read, because the store's query is one field's
		// value and an invitation's address is folded by a rule no query can
		// express: what the index would be is a field the writer does not keep.
		for _, invitation := range invitations {
			if !invitedMember(invitation.Email, actor) {
				continue
			}
			if err := tx.Delete(ctx, invitesCollection, invitation.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return authUserRemoval(ctx, actor.UID)
}

// countOwners is how many owners one membership list holds, which is what D2195's
// refusal is measured with: a second owner anywhere in the organisation is
// somebody who can still act in it.
func countOwners(members []WireMembership) int {
	owners := 0
	for _, member := range members {
		if member.Role == roleOwner {
			owners++
		}
	}
	return owners
}
