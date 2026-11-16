// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The invitations and the operations seed (portal plan P2.2; spec §Tenancy,
// F9, F10, F40).
//
// **An invitation is an address, a role and a state, and nothing else.** It
// holds no token: a stored token would be a list of live credentials, and what
// makes an invitation usable is that the person controls the address — the
// sign-in link Firebase Auth sends there proves it (wire_invites.go). The role
// is on the invitation because it has to be chosen before the person exists,
// and the acceptance is what binds a membership to a uid, because nothing
// before it knows who the person is (F40).
//
// **Acceptance is one comparison and two writes.** The caller's verified
// address and the invitation's own go through `access.Member` on both sides
// (SameMember), the membership the invitation becomes is written under the
// accepting account's uid, and the invitation is marked accepted — one
// transaction, so an invitation is never accepted twice and a membership is
// never written for an invitation that is not.
//
// **An invitation is never a way around the SSO rule.** The rule follows the
// membership a request USES (D2207, F10, P2.1's requireSSO), and an invitation
// into a reseller or an operations organisation is that organisation's future
// membership: the owner who writes it acts through their own membership of that
// organisation, and the acceptance applies the rule to the organisation the
// membership is being created in — an email-link sign-in neither invites into
// such an organisation nor becomes a member of one by accepting.
//
// **Who may do what** is the organisation's own answer (accessTo): an owner or
// an admin invites, reads the list and withdraws one, a plain member decides
// nothing (D2186, D2195), and a caller of another organisation is a stranger to
// all three — the same 404 whether the invitation is not there or is not
// theirs. The operations membership reads across, as it reads everything, and
// that read is written to the log by accessTo (D456, F9).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
)

// The collection the invitations live in. It is this area's own name and not
// the deploy's, for the reason the tenancy's two are (orgs.go).
const invitesCollection = "invites"

// The four states an invitation is spelled in (wire_invites.go,
// invites.schema.json). Pending, accepted and revoked are what is WRITTEN;
// expired is derived from a pending one's own window and the clock, so the
// record says what was decided and a person reading the list sees what has
// happened to it.
const (
	invitePending  = "pending"
	inviteAccepted = "accepted"
	inviteRevoked  = "revoked"
	inviteExpired  = "expired"
)

// inviteTTL is how long an invitation stays acceptable. It is longer than a
// verification link's day because an invitation is a person's to answer when
// they get to it — a colleague on holiday, a customer who sets the portal up
// next week — and short enough that an invitation to somebody who has left is
// not a way in months later. The number is this task's and the owner's to
// change, as F35's limit is P2.1's.
const inviteTTL = 7 * 24 * time.Hour

// operationsOrganisationName is what the seeded operations organisation is
// called, and the whole of what the seed decides about it beyond its type: a
// name is what a switcher shows a person, and the operations organisation has
// nobody to sign up and type one.
const operationsOrganisationName = "Fugaro Operations"

// routeInvitation is the portal page an invitation's mail leads to. The
// invitation's id is in the link and it is not a secret (invites.schema.json):
// what makes the invitation usable is the sign-in Firebase Auth sends to the
// address, and the screen then accepts the invitation it names.
const routeInvitation = "/invite"

// invitationSubject is a constant, so that nothing an owner typed reaches a
// header through the invitation's mail (§7.2's rule, read at this mail path).
const invitationSubject = "You have been invited to a Fugaro organisation"

// The refusals this area answers besides the API's own and P2.1's
// (errNoSuchOrganisation).
var (
	// errNoSuchInvitation is an invitation that is not there, one of another
	// organisation, and one bound to another address. The three are one answer
	// on purpose, for the reason errNoSuchOrganisation is: an id is not a secret
	// and which ones exist is not something a stranger learns from this service.
	errNoSuchInvitation = errors.New("keyservice: the caller may not act on this invitation")
	// errInvitationClosed is an invitation that has been accepted or withdrawn,
	// which is not a state any action here moves it out of.
	errInvitationClosed = errors.New("keyservice: this invitation is no longer pending")
	// errInvitationExpired is a pending invitation whose window has closed.
	errInvitationExpired = errors.New("keyservice: this invitation has expired")
	// errInviteNotPermitted is a member of the organisation, who decides
	// nothing in the portal (D2186, D2195).
	errInviteNotPermitted = errors.New("keyservice: only an organisation's owners and admins may invite, read and withdraw")
	// errInviteEmailRefused is an address no member of this tree can be: an
	// invitation bound to one is one nobody could ever accept, because the
	// acceptance is the two addresses folded and compared.
	errInviteEmailRefused = errors.New("keyservice: this is not an address an invitation can be bound to")
	// errInviteDomainRefused is an address outside the domain list the
	// operations owner set (F9, P5.1).
	errInviteDomainRefused = errors.New("keyservice: this address is outside the domains the operations owner allows")
	// errInviteRoleRefused is a role that is not one of the portal's three.
	errInviteRoleRefused = errors.New("keyservice: this is not a role a membership can hold")
	// errOperationsSeeded is the seed asked for a deployment that is already
	// seeded.
	errOperationsSeeded = errors.New("keyservice: the operations organisation is already seeded")
)

// The actions this area writes, spelled `noun.verb` as wire_audit.go requires.
// What is audited is what an invitation's state changes by — the creating, the
// withdrawing, the accepting — and the reads across organisations, which
// accessTo writes for the operations membership alone (D456, F9, P2.1).
const (
	actionInvitationList   = "invitation.list"
	actionInvitationCreate = "invitation.create"
	actionInvitationRevoke = "invitation.revoke"
	actionInvitationAccept = "invitation.accept"
)

// seedMu is one seed at a time in this process. F40's seed reads whether a
// deployment is already seeded and then writes an organisation, and two of them
// in flight together would each read "nothing here" and each write one — two
// operations organisations, whose members read across every customer's. It is a
// package-level lock rather than a field on Service because this area's file
// may not write service.go (the plan gives it to P1.0), and it is this
// process's, like pageMu, for the same reason: a second instance is the store's
// to hold, and the seed is one operator's command.
var seedMu sync.Mutex

// WireInviteList is one organisation's invitations, the way its owners and its
// admins read them. It is a plain object rather than a bare array for the reason
// WireOrgList is: an answer is an object everywhere in this API, and a list that
// grows a field is then not a new shape.
//
// It is deliberately NOT registered as a wire shape, and that is the one place
// this area departs from P1.1's rule that every shape is: registration requires
// a fixture in ee/portal/schemas/fixtures/, that fixture requires a row in the
// SPA's test/types.test.ts and a TypeScript type beside it, and both are the
// portal's tree — a schema definition this task may not add without editing and
// running them (ee/portal has no toolchain in this task's container, and the
// plan gives the portal's screens to P2.3). The wrapper is what the members
// screen already pins in words (`GET /orgs/{id}/invites`, an object with the
// list under its own name), so the drift this leaves is a wrapper nobody has
// compared, and P2.3 registers it with its schema, fixture and type together.
type WireInviteList struct {
	Invites []WireInvite `json:"invites"`
}

// inviteStateAt answers the state an invitation is IN: what it was written as,
// or expired for a pending invitation whose window has closed. It is the one
// place the clock is read against an invitation, so that the list, the
// withdrawal and the acceptance cannot disagree about whether one is still
// good.
func inviteStateAt(invitation WireInvite, now time.Time) string {
	if invitation.State == invitePending && !now.Before(invitation.ExpiresAt) {
		return inviteExpired
	}
	return invitation.State
}

// validInviteRole answers whether a role is one a membership may hold. The set
// is orgs.go's three and not a second list: a role this service does not know
// is a membership it could not answer for.
func validInviteRole(role string) bool {
	switch role {
	case roleOwner, roleAdmin, roleMember:
		return true
	}
	return false
}

// invitationURL is the link an invitation's mail carries: the portal's own page
// for it, built from the deployment's origin and never from the request's Host
// header (§7.2's rule, as verificationMail's link is).
func invitationURL(origin, invitationID string) string {
	return strings.TrimSuffix(origin, "/") + routeInvitation + "?invitation=" + url.QueryEscape(invitationID)
}

// invitationMail is the invitation as the person receives it: who is asking,
// which address it is for, and the link that leads to it. The link is the whole
// of what the mail carries — never a key and never a secret (D2182, F3) — and
// the organisation's name is folded onto one line before it is written, because
// a name is text an owner typed and a mail line is one line.
func invitationMail(origin, organisation string, invitation WireInvite, ttl time.Duration) Message {
	name := strings.Join(strings.Fields(organisation), " ")
	return Message{
		To:      invitation.Email,
		Subject: invitationSubject,
		Body: "You have been invited to join " + name + " on Fugaro.\n\n" +
			invitationURL(origin, invitation.ID) + "\n\n" +
			"The invitation is for " + invitation.Email + ", and only that address can accept it.\n" +
			"The link works once, and it stops working " + humanTTL(ttl) + " from now.\n",
	}
}

// inviteAccess turns a request into one organisation's access and checks the
// role the act needs: the caller's own membership of it or the operations
// membership's read across (accessTo, which the audit line for that read is
// written by), and an owner's or an admin's standing in it. The action is what
// a read across organisations is filed under, and the acts that write file
// their own lines beside it.
func (s *Service) inviteAccess(ctx context.Context, actor Caller, organisationID, action string) (organisationAccess, error) {
	held, err := s.accessTo(ctx, actor, organisationID, action)
	if err != nil {
		return organisationAccess{}, err
	}
	if !held.Operations && held.Membership.Role != roleOwner && held.Membership.Role != roleAdmin {
		return organisationAccess{}, errInviteNotPermitted
	}
	return held, nil
}

// invitationDomains is the domain list the operations organisation's
// invitations are limited to (F9): what the operations owner set, stored as the
// tenant's own record — wire_ops.go's WireOpsLists in the collection the
// deployment names (service.go's TenantsCollection), filed under the
// organisation it belongs to, which is the record P5.1's writer sets.
//
// A deployment that has set none answers no domains, and the list is read as
// the set of domains that are INSIDE it rather than as a filter over everything
// else: nothing is inside an empty list, so no address may be invited into the
// operations organisation. That is the reading wire_ops.go already takes for the
// directory list, and it costs a deployment nothing, because the operations
// organisation's own way in is the seed, which writes its first invitation
// before any list can exist (SeedOperations) and files its record for P5.1's
// writer to add domains to.
func (s *Service) invitationDomains(ctx context.Context, organisationID string) ([]string, error) {
	lists, found, err := getRecord[WireOpsLists](ctx, s.docs, s.cfg.TenantsCollection, organisationID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	domains := make([]string, 0, len(lists.EmailDomains))
	for _, held := range lists.EmailDomains {
		if domain := strings.ToLower(strings.TrimSpace(held)); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}

// allowedInviteDomain answers whether an address is on a domain list, folded
// through access.Member on both sides for the reason the acceptance is: a list
// the owner wrote and an address an owner typed are one domain where the two
// are one member, and a list that admitted a suffix or a lookalike would admit
// every domain somebody registered under it.
func allowedInviteDomain(address string, domains []string) bool {
	member, err := access.Member(address)
	if err != nil {
		return false
	}
	_, domain, found := strings.Cut(member, "@")
	return found && slices.Contains(domains, domain)
}

// createInvitation writes one invitation of one organisation and mails it. What
// is checked, in this order: the caller's standing in the organisation (an
// owner or an admin), the role, the address as a member this tree can compare,
// and — for the operations organisation alone — the domain list the owner set
// (F9). The invitation is written before the mail is sent, so a mail path that
// fails leaves an invitation that stands and can be withdrawn rather than a link
// to a record nothing holds; the failure is the caller's answer and never a
// silent success.
func (s *Service) createInvitation(ctx context.Context, actor Caller, organisationID string, asked WireInviteCreate) (WireInvite, error) {
	held, err := s.inviteAccess(ctx, actor, organisationID, actionOrganisationRead)
	if err != nil {
		return WireInvite{}, err
	}
	role := strings.TrimSpace(asked.Role)
	if !validInviteRole(role) {
		return WireInvite{}, errInviteRoleRefused
	}
	address := strings.TrimSpace(asked.Email)
	if _, err := access.Member(address); err != nil {
		return WireInvite{}, fmt.Errorf("%w: %v", errInviteEmailRefused, err)
	}
	if held.Organisation.Type == orgTypeOperations {
		domains, err := s.invitationDomains(ctx, held.Organisation.ID)
		if err != nil {
			return WireInvite{}, err
		}
		if !allowedInviteDomain(address, domains) {
			return WireInvite{}, errInviteDomainRefused
		}
	}
	id, err := newID()
	if err != nil {
		return WireInvite{}, err
	}
	now := s.now()
	invitation := WireInvite{
		ID:             id,
		OrganisationID: held.Organisation.ID,
		Email:          address,
		Role:           role,
		State:          invitePending,
		// The inviter is kept as the address a person reads, and it is the
		// address the sign-in proved: an inviter taken from a body would be one
		// the caller chose.
		InvitedBy: actor.Email,
		CreatedAt: now,
		ExpiresAt: now.Add(inviteTTL),
	}
	if err := s.putInvitation(ctx, invitation); err != nil {
		return WireInvite{}, err
	}
	if err := s.mail.Send(ctx, invitationMail(s.cfg.URL, held.Organisation.Name, invitation, inviteTTL)); err != nil {
		return WireInvite{}, err
	}
	if err := s.recordAudit(ctx, actor, auditEntry{
		Action:         actionInvitationCreate,
		OrganisationID: &invitation.OrganisationID,
		TargetID:       &invitation.ID,
		Detail:         &invitation.Email,
	}); err != nil {
		return WireInvite{}, err
	}
	return invitation, nil
}

// putInvitation writes one invitation under its own id.
func (s *Service) putInvitation(ctx context.Context, invitation WireInvite) error {
	doc, err := storedDoc(invitation)
	if err != nil {
		return err
	}
	return s.docs.Put(ctx, invitesCollection, invitation.ID, doc)
}

// listInvitations answers one organisation's invitations, newest first, with
// each one's state as of now: the list a person reads is of what was asked and
// what has happened to it, so an accepted, withdrawn or expired invitation
// stays in it (invites.schema.json's own reading of the state). The order is the
// creation instant and then the id, so that two invitations created in the same
// instant still have an order a page and a test can rely on.
func (s *Service) listInvitations(ctx context.Context, actor Caller, organisationID string) ([]WireInvite, error) {
	held, err := s.inviteAccess(ctx, actor, organisationID, actionInvitationList)
	if err != nil {
		return nil, err
	}
	found, err := recordsWhere[WireInvite](ctx, s.docs, invitesCollection,
		Where{Field: "organisation_id", Value: held.Organisation.ID})
	if err != nil {
		return nil, err
	}
	now := s.now()
	invitations := make([]WireInvite, 0, len(found))
	for _, invitation := range found {
		invitation.State = inviteStateAt(invitation, now)
		invitations = append(invitations, invitation)
	}
	sort.Slice(invitations, func(i, j int) bool {
		left, right := invitations[i], invitations[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	return invitations, nil
}

// revokeInvitation withdraws one invitation of one organisation: it stops being
// acceptable, and it is kept as what it is — a record that somebody was asked.
// The org it names and the invitation it names are both checked, because the
// invitation's id is the API's and a caller of one organisation naming another's
// id is a caller reaching past their own records.
func (s *Service) revokeInvitation(ctx context.Context, actor Caller, organisationID, invitationID string) (WireInvite, error) {
	held, err := s.inviteAccess(ctx, actor, organisationID, actionOrganisationRead)
	if err != nil {
		return WireInvite{}, err
	}
	var revoked WireInvite
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		invitation, found, err := getRecord[WireInvite](ctx, tx, invitesCollection, invitationID)
		if err != nil {
			return err
		}
		if !found || invitation.OrganisationID != held.Organisation.ID {
			return errNoSuchInvitation
		}
		// Only a pending invitation is the one a withdrawal acts on, and the two
		// ways it is not pending are told apart so that a person withdrawing one
		// that ran out of time is told that rather than that somebody accepted it.
		if state := inviteStateAt(invitation, s.now()); state != invitePending {
			if state == inviteExpired {
				return errInvitationExpired
			}
			return errInvitationClosed
		}
		invitation.State = inviteRevoked
		if err := putRecord(ctx, tx, invitesCollection, invitation.ID, invitation); err != nil {
			return err
		}
		revoked = invitation
		return nil
	})
	if err != nil {
		return WireInvite{}, err
	}
	if err := s.recordAudit(ctx, actor, auditEntry{
		Action:         actionInvitationRevoke,
		OrganisationID: &revoked.OrganisationID,
		TargetID:       &revoked.ID,
		Detail:         &revoked.Email,
	}); err != nil {
		return WireInvite{}, err
	}
	return revoked, nil
}

// acceptInvitation is the one act an invited person makes: the membership the
// invitation becomes, and the invitation marked accepted, in one transaction.
//
// What it refuses, and why the order is this one: an invitation that is not
// there and one bound to another address are the same answer (errNoSuchInvitation)
// and are decided BEFORE the organisation is read, so that a stranger holding a
// guessed id learns nothing about the organisation it names; the window and the
// state are next; and the SSO rule is last, applied to the organisation the
// membership is being created in.
//
// A caller who is already a member of the organisation is accepted without
// their membership being touched: the invitation is one way in and not a way to
// change what somebody holds — a role is moved by an owner and by nothing else
// (D2186, D2195) — so the invitation is spent and the record that exists stays
// as it is.
func (s *Service) acceptInvitation(ctx context.Context, actor Caller, invitationID string) (WireInvite, error) {
	var accepted WireInvite
	err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		invitation, found, err := getRecord[WireInvite](ctx, tx, invitesCollection, invitationID)
		if err != nil {
			return err
		}
		if !found || !SameMember(invitation.Email, actor.Email) {
			return errNoSuchInvitation
		}
		org, present, err := getRecord[WireOrganisation](ctx, tx, organisationsCollection, invitation.OrganisationID)
		if err != nil {
			return err
		}
		if !present {
			return errNoSuchInvitation
		}
		if state := inviteStateAt(invitation, s.now()); state != invitePending {
			if state == inviteExpired {
				return errInvitationExpired
			}
			return errInvitationClosed
		}
		if err := s.requireSSO(actor, org); err != nil {
			return err
		}
		now := s.now()
		_, member, err := getRecord[WireMembership](ctx, tx, membershipsCollection, membershipID(org.ID, actor.UID))
		if err != nil {
			return err
		}
		if !member {
			if err := putRecord(ctx, tx, membershipsCollection, membershipID(org.ID, actor.UID), WireMembership{
				OrganisationID: org.ID,
				UID:            actor.UID,
				Email:          actor.Email,
				Role:           invitation.Role,
				CreatedAt:      now,
				// The acceptance is also the person's arrival: it is the one
				// selection nobody makes, and it is what the switcher lands them
				// on the next time they come (P2.1's signUp's own reason).
				LastUsedAt: &now,
			}); err != nil {
				return err
			}
		}
		invitation.State = inviteAccepted
		invitation.AcceptedAt = &now
		if err := putRecord(ctx, tx, invitesCollection, invitation.ID, invitation); err != nil {
			return err
		}
		accepted = invitation
		return nil
	})
	if err != nil {
		return WireInvite{}, err
	}
	if err := s.recordAudit(ctx, actor, auditEntry{
		Action:         actionInvitationAccept,
		OrganisationID: &accepted.OrganisationID,
		TargetID:       &accepted.ID,
	}); err != nil {
		return WireInvite{}, err
	}
	return accepted, nil
}

// SeedOperations writes the operations organisation and its own first
// invitation, once, and answers the organisation it wrote. It is the deployment
// owner's one-time act (P1.7's `seed-operations <address>` subcommand calls it),
// and the operation is the intake's own: the operations organisation is never
// signed up to, so it records no acceptance of the terms (P2.1), and its first
// owner is bound at first sign-in like every other invitation (F40).
//
// **What "once" is, and why it is not "an operations organisation exists".**
// The seed refuses a deployment where seeding has taken — an operations
// organisation that holds a member or a live invitation. Reading it as the
// mere existence of the organisation would make one missed week permanent: the
// invitation's window closes, no member can be invited and no owner of the
// organisation exists to invite one, and SeedOperations — the only door that
// writes the organisation — would refuse to run again. So a deployment whose
// invitation expired unaccepted is seeded again, into the organisation that is
// already there rather than a second one: two operations organisations would be
// two teams reading across every customer's.
//
// The address is not checked against the operations domain list, and it cannot
// be: that list is keyed by the organisation's own id (invitationDomains),
// which does not exist until this runs. The seed is the one way in that the
// list does not gate, which is why it is one operator's one-time command.
//
// The invitation is mailed, so the person the seed names holds the link that
// leads to it; a mail path that fails is the caller's error and leaves the
// invitation pending, which the deployment answers by running the seed again
// (it is refused only once somebody has accepted, or while the invitation is
// live).
func (s *Service) SeedOperations(ctx context.Context, address string) (WireOrganisation, error) {
	seedMu.Lock()
	defer seedMu.Unlock()
	address = strings.TrimSpace(address)
	if _, err := access.Member(address); err != nil {
		return WireOrganisation{}, fmt.Errorf("%w: %v", errInviteEmailRefused, err)
	}
	held, err := recordsWhere[WireOrganisation](ctx, s.docs, organisationsCollection,
		Where{Field: "type", Value: orgTypeOperations})
	if err != nil {
		return WireOrganisation{}, err
	}
	// The store's order is not an order, and there is at most one of these: a
	// deterministic pick is what keeps a deployment that somehow holds two
	// answering the same one every time.
	sort.Slice(held, func(i, j int) bool { return held[i].ID < held[j].ID })
	now := s.now()
	var org WireOrganisation
	switch {
	case len(held) > 0:
		org = held[0]
		members, err := membersOf(ctx, s.docs, org.ID)
		if err != nil {
			return WireOrganisation{}, err
		}
		if len(members) > 0 {
			return WireOrganisation{}, errOperationsSeeded
		}
		invitations, err := recordsWhere[WireInvite](ctx, s.docs, invitesCollection,
			Where{Field: "organisation_id", Value: org.ID})
		if err != nil {
			return WireOrganisation{}, err
		}
		for _, invitation := range invitations {
			if inviteStateAt(invitation, now) == invitePending {
				return WireOrganisation{}, errOperationsSeeded
			}
		}
	default:
		id, err := newID()
		if err != nil {
			return WireOrganisation{}, err
		}
		org = WireOrganisation{
			ID:        id,
			Name:      operationsOrganisationName,
			Type:      orgTypeOperations,
			CreatedAt: now,
		}
	}
	id, err := newID()
	if err != nil {
		return WireOrganisation{}, err
	}
	invitation := WireInvite{
		ID:             id,
		OrganisationID: org.ID,
		Email:          address,
		Role:           roleOwner,
		State:          invitePending,
		// The operator who runs the seed is who asked for this: the address the
		// seed names is the one person the command knows about, and an audit of
		// the organisation's first invitation names nobody else.
		InvitedBy: address,
		CreatedAt: now,
		ExpiresAt: now.Add(inviteTTL),
	}
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := putRecord(ctx, tx, organisationsCollection, org.ID, org); err != nil {
			return err
		}
		return putRecord(ctx, tx, invitesCollection, invitation.ID, invitation)
	})
	if err != nil {
		return WireOrganisation{}, err
	}
	if err := s.mail.Send(ctx, invitationMail(s.cfg.URL, org.Name, invitation, inviteTTL)); err != nil {
		return WireOrganisation{}, err
	}
	return org, nil
}
