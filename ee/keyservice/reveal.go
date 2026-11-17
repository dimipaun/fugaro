// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Revealing a key (portal plan P3.2; D2182 as amended, D2187, F3, F4, F5, F30,
// F32, F35) and the file an air-gapped licence is downloaded as (D2206, F42).
//
// **A reveal is a rotation, and that is what makes it a reveal at all.** The
// portal holds no key to show (§7.3: the store keeps a renewal secret's digest
// and never the secret), so "show me the key again" has exactly one honest
// answer: mint the licence's next key and hand that over. The caller gets a new
// key id and a new renewal secret, and the secret the installation is holding
// stops renewing the moment the rotation is recorded — which is the trade D2182
// names, and the reason this act carries the four limits below rather than being
// a read.
//
// **The new key ends where the old one ended** (F4, F30). A reveal recovers a
// credential; it does not sell a term. The end handed to the minter is the
// predecessor's own `paid_through`, so a key revealed on day ten of a thirty-day
// term covers the twenty days that were left and never thirty more, and a
// reveal asked for on the last day of one is not a way to stay licensed.
//
// **Four limits, each of them a different harm** (D2182 as amended, D2187, F32,
// F35). Owners and admins only, because rotating an organisation's key is
// deciding for the organisation rather than working in it (D2186). A sign-in
// from the last five minutes, because a session somebody left open on a shared
// machine is not a person asking. One reveal per workspace per day, because two
// keys from one day is an installation nobody can tell which of them should
// run. And the organisation's other owners are mailed, because a key that
// changed without the people who paid for it knowing is the thing the notice
// exists for — and what they are mailed is a link and never a key (F3), which
// is also why the reveal's own answer goes to the caller alone.
//
// **An air-gapped licence has no reveal** (D2206, F42). Its key carries no
// secret and never contacts this service, so nothing can prove one and a second
// signature of it would be a second licence that no revocation reaches: what its
// holder is given is the file the binding issued, byte for byte, on every
// download.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	// revealsCollection holds one document per workspace, keyed by the
	// workspace's own portal id, recording the last reveal its key had. It is
	// this area's own name and not the deploy's, for the reason the tenancy's
	// two are (orgs.go): what it holds is this service's bookkeeping, and the
	// rule it carries (F32) is the service's and not a property of the
	// workspace a screen draws.
	revealsCollection = "reveals"

	// revealWindow is F32's one reveal per workspace per twenty-four hours,
	// counted from the reveal that set it. What it bounds is not the mint's
	// cost — a key is cheap — but the installation: the person asking for the
	// second reveal of a day is holding the first key, and a licence whose keys
	// were rotated twice in a day is one whose holder cannot say which of the
	// two an installation should be running.
	revealWindow = 24 * time.Hour

	// revealStepUp is D2187's five minutes, which is the window every act that
	// hands over a credential is held to. It is the **sign-in's** own instant
	// (`auth_time`) and not the token's: an ID token lives an hour, so a token
	// minted this minute can attest a sign-in from this morning.
	revealStepUp = 5 * time.Minute
)

// The refusals this area owns, each a sentinel so that the route can answer it
// with the status and the sentence its reader branches on.
var (
	// errRevealNotPermitted is a member who is not an owner or an admin. F5's
	// line: a reveal rotates the key an installation runs on, which is the
	// organisation's act and not the member's.
	errRevealNotPermitted = errors.New("keyservice: revealing a key is an owner's or an admin's act")
	// errAirGappedReveal is an air-gapped licence asked for a reveal. D2206: it
	// was issued once, at binding, and there is nothing to rotate.
	errAirGappedReveal = errors.New("keyservice: an air-gapped licence is issued once and has no key to reveal")
	// errNotAirGapped is a licence that is not air-gapped asked for its file.
	// There is no file for one: its key is revealed in the portal instead.
	errNotAirGapped = errors.New("keyservice: this licence has no licence file, and its key is revealed instead")
	// errNoLicence is a workspace that names no licence, which is a workspace
	// nobody has bound one to yet: there is no key to rotate.
	errNoLicence = errors.New("keyservice: that workspace has no licence")
	// errWorkspaceUnbound is a licence a workspace names and no Firebase project
	// to mint a key into (TN2). A workspace pointed at a fresh grant is in this
	// state until somebody binds it (entitlements.go's pointWorkspaceAt).
	errWorkspaceUnbound = errors.New("keyservice: that workspace's licence is not bound to a project")
	// errKeyLapsed is a workspace whose key has already ended while its licence
	// has not. The successor of a reveal ends where its predecessor did (F4), so
	// there is no term left to re-sign and nothing this act can hand over.
	errKeyLapsed = errors.New("keyservice: that workspace's key has already lapsed")
	// errRevealTooSoon is F32's window: this workspace's key was revealed inside
	// the last twenty-four hours.
	errRevealTooSoon = errors.New("keyservice: a workspace's key may be revealed once a day")
)

// revealRecord is one workspace's last reveal. The instant is the rule: a reveal
// inside revealWindow of this one is refused, and the record is the store's own
// memory of it rather than a count in this instance, because the API runs on
// more than one.
type revealRecord struct {
	WorkspaceID string    `json:"workspace_id"`
	At          time.Time `json:"at"`
}

// reveal rotates one workspace's key: the licence's next key, ending where the
// key it replaces ended, with a handover left for the installation running on
// that key.
//
// The order is the one the four limits imply. Everything that can refuse is
// asked first and cheapest-first, the day is reserved before anything is minted
// — a mint spends a source and mails nobody, but it is the step that cannot be
// taken back — and the handover and the notice come after, both of them logged
// and neither of them able to fail the reveal. That last part is the plan's own
// accepted limit: an installation whose handover was lost sets the bundle by
// hand within the day, and a reveal that answered a failure there would take a
// key from a person who has already been handed it.
func (s *Service) reveal(ctx context.Context, actor Caller, workspaceID string) (MintedV1, error) {
	workspace, licence, err := s.revealTarget(ctx, actor, workspaceID)
	if err != nil {
		return MintedV1{}, err
	}
	// D2187's step-up, asked through the seam the way the SSO rule is (api.go):
	// the rule is P1.3's, and this area reads its answer rather than a second
	// copy of it.
	if err := s.signIn.FreshWithin(actor, revealStepUp); err != nil {
		return MintedV1{}, err
	}
	if licence.AirGapped {
		return MintedV1{}, errAirGappedReveal
	}
	now := s.now().UTC()
	if licence.Status != entitlementActive {
		return MintedV1{}, errEntitlementNotActive
	}
	if !licence.EndsAt.After(now) {
		return MintedV1{}, errEntitlementEnded
	}
	if workspace.ProjectID == nil {
		return MintedV1{}, errWorkspaceUnbound
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return MintedV1{}, err
	}
	predecessor, issued := newestIssuance(records, licence.ID)
	if !issued {
		return MintedV1{}, fmt.Errorf("keyservice: the licence %s is bound to the workspace %s and the store holds no key it issued", licence.ID, workspace.ID)
	}
	// A key that has already ended has nothing left to re-sign, and the minter
	// would refuse the end it would have to be given: what this answers is a
	// licence whose installation stopped being licensed before anybody asked,
	// which is a renewal and not a reveal (F27, F30).
	if !predecessor.Expires.After(now) {
		return MintedV1{}, errKeyLapsed
	}
	if err := s.reserveReveal(ctx, workspace.ID, now); err != nil {
		return MintedV1{}, err
	}
	// The subscription the licence was sold under, which the record keeps so
	// that `/renew` can tell a licence still being paid for from one that is
	// not (D2148): a successor minted without it is a key nothing renews, which
	// is the whole of what a reveal is for.
	subscription, err := s.licenceSubscription(ctx, licence.ID)
	if err != nil {
		return MintedV1{}, err
	}
	answer, err := s.paid(ctx, RequestV1{
		Environment: s.cfg.Environment,
		Policy:      licence.Row,
		// The project id is the key's workspace (TN2) and not the portal's own
		// id: the installation compares that claim with its own project.
		WorkspaceID:  *workspace.ProjectID,
		Organisation: licence.OrganisationID,
		Seats:        licence.Seats,
		// No term and no end of this request's own: the end is the
		// predecessor's, which is the minter's cap and never an extension of it
		// (F30). A fresh renewal secret is the point of the act, so no hash is
		// named and the minter makes one.
		RequestedEnd: predecessor.Expires,
	}, "", rotationSource(predecessor.KeyID), subscription, licence.ID)
	if err != nil {
		return MintedV1{}, err
	}
	s.leaveRevealHandover(ctx, predecessor, answer, now)
	s.tellOtherOwners(ctx, actor, licence, workspace, answer, now)
	return answer, nil
}

// revealTarget answers the workspace one reveal rotates and the licence it holds,
// having checked that this caller may ask: the workspace is read by id, and what
// authorises the request is the caller's own membership of the organisation that
// holds it (checkout.go's shape at the same door).
//
// A workspace that is not there, one of an organisation the caller does not
// belong to, and one they belong to but only work in are three different
// answers, and the first two are one: which workspaces exist is a list of this
// portal's customers, so a stranger learns nothing from a 404 (api_orgs.go's
// rule, read at this door).
func (s *Service) revealTarget(ctx context.Context, actor Caller, workspaceID string) (WireWorkspace, WireEntitlement, error) {
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, workspaceID)
	if err != nil {
		return WireWorkspace{}, WireEntitlement{}, err
	}
	if !present {
		return WireWorkspace{}, WireEntitlement{}, errNoSuchWorkspace
	}
	_, membership, member, err := s.ownMembership(ctx, actor, workspace.OrganisationID)
	if err != nil {
		return WireWorkspace{}, WireEntitlement{}, err
	}
	if !member {
		return WireWorkspace{}, WireEntitlement{}, errNoSuchWorkspace
	}
	if membership.Role != roleOwner && membership.Role != roleAdmin {
		return WireWorkspace{}, WireEntitlement{}, errRevealNotPermitted
	}
	if workspace.EntitlementID == nil {
		return WireWorkspace{}, WireEntitlement{}, errNoLicence
	}
	licence, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, *workspace.EntitlementID)
	if err != nil {
		return WireWorkspace{}, WireEntitlement{}, err
	}
	if !present {
		return WireWorkspace{}, WireEntitlement{}, fmt.Errorf("keyservice: the workspace %s names the licence %s, which the store does not hold", workspace.ID, *workspace.EntitlementID)
	}
	return workspace, licence, nil
}

// reserveReveal spends this workspace's day, and answers errRevealTooSoon when
// the last reveal is still inside its window.
//
// The read and the write are one transaction, and the read is first: two reveals
// of one workspace that arrive together are two requests that read the same
// record, and the store's own rule for a transaction is that a write may not be
// followed by a read — so the second of them either sees the first's write here
// or is run again on the state that includes it.
//
// A reveal that is refused after this point has spent the day, and that is the
// safe direction: what F32's window is for is one rotation a day, and a mint that
// failed has already spent the source `rotation:<predecessor>`, so the retry
// that follows would mint nothing anyway. The other direction — reserving after
// the mint — is the one that lets two rotations through.
func (s *Service) reserveReveal(ctx context.Context, workspaceID string, now time.Time) error {
	return s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, present, err := getRecord[revealRecord](ctx, tx, revealsCollection, workspaceID)
		if err != nil {
			return err
		}
		if present && now.Before(held.At.Add(revealWindow)) {
			return errRevealTooSoon
		}
		return putRecord(ctx, tx, revealsCollection, workspaceID, revealRecord{WorkspaceID: workspaceID, At: now})
	})
}

// leaveRevealHandover leaves the installation running on the key this reveal
// replaced the answer to a question it has not asked yet.
//
// The key it is left against is the **predecessor**, and the bundle is the
// successor's: an installation whose refresh lost the answer asks `/renew` with
// the key and the secret it holds, and what it is handed is the key the portal
// just showed its owner (renew.go's `answerRotation`). That is also why the
// predecessor is not withdrawn here — a revoked key is answered its state before
// the handover is ever read, so withdrawing it would leave the installation
// holding a credential that answers nothing.
//
// The digest the handover is held against is the predecessor's own, read from
// its key state: a key whose state the store does not hold is one whose secret
// nothing can prove, so there is no window to reopen for it. A store that
// refuses the write, and a deployment that cannot seal one, are both logged and
// neither fails the reveal (sealHandover): the successor is signed and recorded,
// and the plan's own limit accepts that the bundle is then set by hand within
// the day.
func (s *Service) leaveRevealHandover(ctx context.Context, predecessor Record, answer MintedV1, now time.Time) {
	state, known, err := s.store.KeyState(ctx, predecessor.KeyID)
	if err != nil {
		log.Printf("keyservice: reading the key state of %s to leave its reveal a handover: %v", predecessor.KeyID, err)
		return
	}
	if !known {
		return
	}
	sealed, held := s.sealHandover(ctx, predecessor.KeyID, answer.Key, answer.RenewalSecret)
	if !held {
		return
	}
	if err := s.store.SaveHandover(ctx, Handover{
		KeyID:      predecessor.KeyID,
		SecretHash: state.RenewalSecretHash,
		Sealed:     sealed,
		Until:      now.Add(renewalHandover),
	}, now); err != nil {
		log.Printf("keyservice: keeping the answer the reveal of the key %s left for it: %v", predecessor.KeyID, err)
	}
}

// tellOtherOwners mails the organisation's owners that a key was revealed, the
// caller's own act excepted.
//
// **Owners and not every member**: the notice is for the people who paid and who
// decide for the organisation, which is the same list the reminders go to and
// not the list of everybody who may work in it (reminders.go's reminderOwners).
// **A link and never a key** (F3): the mail says what happened and where to look,
// and the bundle goes to the person who asked for it and to nobody else.
//
// Every failure here is logged and none of them is answered: the key is signed,
// recorded and about to be handed over, and a mail provider having a bad minute
// is not a reason to take it away from the person holding it.
func (s *Service) tellOtherOwners(ctx context.Context, actor Caller, licence WireEntitlement, workspace WireWorkspace, answer MintedV1, now time.Time) {
	organisation, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, licence.OrganisationID)
	if err != nil || !present {
		log.Printf("keyservice: reading the organisation %s to tell its owners about the reveal of %s: %v", licence.OrganisationID, answer.Record.KeyID, err)
		return
	}
	owners, err := reminderOwners(ctx, s.docs, organisation.ID)
	if err != nil {
		log.Printf("keyservice: reading the owners of %s to tell them about the reveal of %s: %v", organisation.ID, answer.Record.KeyID, err)
		return
	}
	origin, err := portalOrigin(s.cfg.URL)
	if err != nil {
		log.Printf("keyservice: building the link a reveal notice carries: %v", err)
		return
	}
	for _, owner := range owners {
		if owner.UID == actor.UID {
			continue
		}
		if err := s.mail.Send(ctx, revealMail(owner.Email, organisation.Name, workspace.Name, origin, answer.Record.Expires, now)); err != nil {
			log.Printf("keyservice: telling the owner %s about the reveal of the key %s: %v", owner.UID, answer.Record.KeyID, err)
		}
	}
}

// licenceFile answers the file an air-gapped licence is issued as (D2206), read
// through the caller's own membership of the organisation that holds it, the way
// a reveal reads its workspace.
//
// **Any member may read it**, and that is the difference from a reveal: the file
// carries no secret, never asks this service for anything, and is what the
// installation with no egress is set up from by hand (K3.3) — so the person who
// has to carry it there is not necessarily the person who bought it. What is
// stored is what the binding wrote, so a second download is the same bytes
// rather than a second issue.
func (s *Service) licenceFile(ctx context.Context, actor Caller, workspaceID string) (WireLicenceFile, error) {
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, workspaceID)
	if err != nil {
		return WireLicenceFile{}, err
	}
	if !present {
		return WireLicenceFile{}, errNoSuchWorkspace
	}
	_, _, member, err := s.ownMembership(ctx, actor, workspace.OrganisationID)
	if err != nil {
		return WireLicenceFile{}, err
	}
	if !member {
		return WireLicenceFile{}, errNoSuchWorkspace
	}
	if workspace.EntitlementID == nil {
		return WireLicenceFile{}, errNoLicence
	}
	licence, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, *workspace.EntitlementID)
	if err != nil {
		return WireLicenceFile{}, err
	}
	if !present {
		return WireLicenceFile{}, fmt.Errorf("keyservice: the workspace %s names the licence %s, which the store does not hold", workspace.ID, *workspace.EntitlementID)
	}
	if !licence.AirGapped {
		return WireLicenceFile{}, errNotAirGapped
	}
	file, present, err := getRecord[WireLicenceFile](ctx, s.docs, licenceFilesCollection, licence.ID)
	if err != nil {
		return WireLicenceFile{}, err
	}
	if !present {
		return WireLicenceFile{}, fmt.Errorf("keyservice: the air-gapped licence %s has no file, and its file is what its holder is given (D2206)", licence.ID)
	}
	return file, nil
}

// revealSubject is the notice's subject, a constant for the reason the reminders'
// four are: an organisation's name and a workspace's are text a person typed, and
// a constant is what keeps one out of a mail header (§7.2).
const revealSubject = "A Fugaro key was revealed for your organisation"

// revealMail is the notice one reveal sends the organisation's other owners: what
// happened, that the term did not move, and where to look. It carries no key and
// no secret (F3), and neither is passed to it — what it names is the term the
// successor covers, which is a date the owner already knows.
func revealMail(to, organisation, workspace, origin string, paidThrough, now time.Time) Message {
	name := strings.Join(strings.Fields(organisation), " ")
	where := strings.Join(strings.Fields(workspace), " ")
	until := paidThrough.UTC().Format("2 January 2006")
	link := strings.TrimSuffix(origin, "/") + routeWorkspaces
	return Message{
		To:      to,
		Subject: revealSubject,
		Body: "The Fugaro key of " + name + "'s workspace " + where + " was revealed on " +
			now.UTC().Format("2 January 2006") + ".\n\n" +
			"Somebody who decides for the organisation asked for it, so the installation there has a new key and the key it was running on no longer renews.\n" +
			"The term is unchanged: the new key runs to " + until + ", the same day the old one did.\n" +
			"If you did not expect this, change who is in the organisation and tell us:\n\n" + link + "\n",
	}
}
