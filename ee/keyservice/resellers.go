// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Resellers and their customers (portal plan P6.1; D2189, D2190, D2191, F5,
// F40).
//
// **A reseller organisation and a customer organisation are two tenants, and the
// link between them is one field.** D2191 gives the reseller the act that makes a
// customer's organisation — the customer signs up to nothing, the reseller makes
// the tenant and invites its first owner — and F40 is the line that act must not
// cross: the reseller is never a member of the organisation it made. So the
// organisation carries a pointer to its reseller (P2.1's WireOrganisation.
// ResellerID) and the reseller's list of customers is a record of its own
// (`reseller_customers`), and neither of the two is a membership: what a reseller
// reads of a customer is what it sold it (F5, `keys.customer`), and every act
// inside the customer's organisation is its own owners' from the moment its first
// owner accepts.
//
// **The reseller a request acts for is the caller's, and never the body's.**
// The SPA's reseller paths name the customer and no reseller
// (ee/portal/src/screens/reseller/api.ts), and that is the rule and not a
// convenience: a reseller that could name itself in a body could name another
// (resellerFor). Reads are a member's, acts are an owner's or an admin's
// (D2186, D2195), and the sign-in rule follows the reseller membership the
// request uses like every other (D2207, F10, P2.1's requireSSO).
//
// **Operations holds the money and the link, and both are audited.** A reseller
// organisation, its credit limit and its wholesale rates are operations' to set
// (D2189, and spec §Resellers' "no prepaid credit"), and so is the relink a
// customer moves between resellers with (D2191, F14's own resolution). Those
// three acts are operations actions and are written to the log before they act
// (D456), which is what makes "who made this reseller, and who moved this
// customer?" a question the log answers. The two acts that are the reseller's own
// — making a customer organisation and inviting its first owner — write the
// invitation's line and no line of their own: the log is the operations team's
// (audit.go), and a reseller's ordinary work is not what it exists to account
// for.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
)

// The two collections this area keeps. They are this area's own names and not the
// deploy's, for the reason the tenancy's two are (orgs.go): the store's ids are
// the two names a backend files a document under, and a record that moves
// between collections is a migration.
//
// A reseller's terms are filed under the reseller organisation's id, because
// there is exactly one terms record per reseller; a customer's link is filed
// under the CUSTOMER organisation's id, because a customer has exactly one
// reseller, and a relink is that record written again (resellers.schema.json's
// `linked_at`: "A relink writes a new one rather than editing this").
const (
	resellerTermsCollection     = "reseller_terms"
	resellerCustomersCollection = "reseller_customers"
)

// The refusals this area answers besides the API's own and the areas it reads
// from (`errNoSuchOrganisation`, `errInviteEmailRefused`, `errInviteRoleRefused`,
// `errInviteRateLimited`, `errOperationsBadRow`).
var (
	// errNotReseller is a caller who is a member of no reseller organisation at
	// all. It is a 403 and not a 404, like errNotOperations: the reseller screens
	// are a surface, and the caller is signed in and simply is not one of a
	// reseller's people.
	errNotReseller = errors.New("keyservice: the caller is not a member of a reseller organisation")
	// errResellerAmbiguous is a caller whose memberships are in more than one
	// reseller organisation. Every route of this area acts for one reseller, and
	// the request names none: serving it as the first of two would be acting for
	// a reseller nobody chose.
	errResellerAmbiguous = errors.New("keyservice: this account belongs to more than one reseller organisation")
	// errResellerNotPermitted is a plain member of a reseller organisation asking
	// to change something (D2186, D2195), a role other than owner on the
	// invitation that makes a customer's first owner, or an address that is
	// already one of the reseller's own people.
	errResellerNotPermitted = errors.New("keyservice: only a reseller organisation's owners and admins may do this")
	// errNoSuchResellerCustomer is a customer organisation this reseller does not
	// hold: one of another reseller's, one that was never linked, and one whose
	// id nobody has. The three are one answer on purpose, for the reason
	// errNoSuchOrganisation is: which ids exist is not something a stranger
	// learns from this service.
	errNoSuchResellerCustomer = errors.New("keyservice: this customer is not one this reseller holds")
	// errNotAReseller is an id that is an organisation and is not a reseller:
	// a customer's, the operations organisation's, and an id nobody has.
	errNotAReseller = errors.New("keyservice: no reseller organisation in this portal has that id")
	// errFirstOwnerSet is a customer organisation that has a member or a live
	// invitation: the reseller's part in it is done (F40), and from there its own
	// owners invite.
	errFirstOwnerSet = errors.New("keyservice: this customer organisation has its own people already")
	// errResellerBadCreditLimit is a credit limit that is not one: a negative
	// ceiling on accrual, where the schema's minimum is zero.
	errResellerBadCreditLimit = errors.New("keyservice: that credit limit is not one a reseller may be given")
	// errResellerBadRate is a wholesale rate that is not one: a price below zero,
	// or one row priced twice in the same request.
	errResellerBadRate = errors.New("keyservice: that is not a wholesale rate a reseller may be given")
	// errResellerAlreadyLinked is a relink to the reseller that already holds the
	// customer: there is nothing to move, and a write here would be a line in the
	// log about a move nobody made.
	errResellerAlreadyLinked = errors.New("keyservice: that customer is already linked to this reseller")
)

// The actions this area writes, spelled `noun.verb` as wire_audit.go requires.
// The three are operations' acts on a reseller; the acts that are the reseller's
// own write the invitation's own line (createInvitation's action, reused here for
// the same act) and nothing beside it.
const (
	actionResellerCreate   = "reseller.create"
	actionResellerTermsSet = "reseller.terms.set"
	actionResellerRelink   = "reseller.relink"
)

// resellerAccess is one answered reseller read: which reseller organisation the
// caller's own membership is in, and the membership it is.
//
// There is no Operations flag here, unlike organisationAccess: operations is not
// a second way into this area. Its acts on resellers are three routes of their
// own (api_resellers.go), gated by P5.1's operationsCaller, and a reseller's
// reads and its customers are never served through the operations membership —
// operations reads a customer's organisation through `/api/ops/**` (P5.1), and a
// reseller's list is the reseller's own.
type resellerAccess struct {
	Organisation WireOrganisation
	Membership   WireMembership
}

// write refuses a request that decides something, for the role D2186 and D2195
// give it to a reseller organisation as they do to any other: its owners and its
// admins. A plain member reads what the area reads (resellerFor) and decides
// nothing.
func (as resellerAccess) write() error {
	if as.Membership.Role != roleOwner && as.Membership.Role != roleAdmin {
		return errResellerNotPermitted
	}
	return nil
}

// resellerFor answers the reseller organisation the caller belongs to, which is
// where every route of this area that is the reseller's own starts.
//
// **Two reseller memberships are refused rather than guessed at.** P2.1 lets a
// person belong to as many organisations as they like and nothing forbids two of
// them being resellers, so a caller with two is one whose request names no
// reseller and means one of them: serving it as the first of the two in the
// store's order would be making a customer organisation, or inviting a customer's
// first owner, for a reseller the person did not choose. The refusal says so, and
// a client that must act for one of several names it — which is a shape neither
// the plan nor the SPA's paths have today.
//
// The rule of the organisation the membership is in is applied here and not to
// the customer the request is about (D2207, F10): what this request USES is the
// reseller membership, and a reseller whose people signed in by email link is
// refused every request of this area, including the ones about a customer
// organisation the rule has nothing to say about.
func (s *Service) resellerFor(ctx context.Context, actor Caller) (resellerAccess, error) {
	held, err := membershipsOf(ctx, s.docs, actor.UID)
	if err != nil {
		return resellerAccess{}, err
	}
	found := make([]resellerAccess, 0, 1)
	for _, membership := range held {
		org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, membership.OrganisationID)
		if err != nil {
			return resellerAccess{}, err
		}
		if !present || org.Type != orgTypeReseller {
			continue
		}
		found = append(found, resellerAccess{Organisation: org, Membership: membership})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Organisation.ID < found[j].Organisation.ID })
	switch len(found) {
	case 0:
		return resellerAccess{}, errNotReseller
	case 1:
	default:
		return resellerAccess{}, fmt.Errorf("%w: %s and %s", errResellerAmbiguous, found[0].Organisation.ID, found[1].Organisation.ID)
	}
	chosen := found[0]
	if err := s.requireSSO(actor, chosen.Organisation); err != nil {
		return resellerAccess{}, err
	}
	return chosen, nil
}

// resellerOrganisation reads one organisation that is a reseller, and answers
// anything else as absent: a customer's id, the operations organisation's and an
// id nobody has are one refusal, because a route that told the three apart would
// be a route that says which ids are which organisations.
func (s *Service) resellerOrganisation(ctx context.Context, id string) (WireOrganisation, error) {
	org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, strings.TrimSpace(id))
	if err != nil {
		return WireOrganisation{}, err
	}
	if !present || org.Type != orgTypeReseller {
		return WireOrganisation{}, errNotAReseller
	}
	return org, nil
}

// customerOrganisation reads one organisation the relink may act on, which is one
// that is a customer's: a reseller organisation is not relinked to a reseller and
// neither is the operations organisation, so both are refused the way an id
// nobody has is (resellerOrganisation's own reason).
func (s *Service) customerOrganisation(ctx context.Context, id string) (WireOrganisation, error) {
	org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, strings.TrimSpace(id))
	if err != nil {
		return WireOrganisation{}, err
	}
	if !present || org.Type != orgTypeCustomer {
		return WireOrganisation{}, errNoSuchOrganisation
	}
	return org, nil
}

// resellerTerms reads one reseller organisation's commercial terms.
func (s *Service) resellerTerms(ctx context.Context, org WireOrganisation) (WireReseller, error) {
	held, found, err := getRecord[WireReseller](ctx, s.docs, resellerTermsCollection, org.ID)
	if err != nil {
		return WireReseller{}, err
	}
	return resellerTermsFor(org, held, found), nil
}

// resellerTermsFor is one reseller's terms as the API answers them.
//
// A reseller organisation the store holds no terms record for is answered the
// fail-closed zero — no credit and no rates, so nothing it may sell — and not as
// an error: this is the reading `invitationDomains` takes for a list nobody set
// ("nothing is inside an empty list"), and the alternative is a reseller's own
// screen failing over a record that no route of this build can leave unwritten
// (createReseller writes the two together). CreatedAt is the organisation's own
// instant, which is what `resellers.schema.json` says the field is, rather than
// the record's copy of it.
func resellerTermsFor(org WireOrganisation, held WireReseller, found bool) WireReseller {
	terms := WireReseller{OrganisationID: org.ID, CreatedAt: org.CreatedAt, Rates: []WireResellerRate{}}
	if !found {
		return terms
	}
	terms.CreditLimitMicros = held.CreditLimitMicros
	terms.Rates = append(terms.Rates, ratesOrEmpty(held.Rates)...)
	return terms
}

// ratesOrEmpty is a rate list as it is written and answered: every row as the
// policy table spells it, and a list even where it is empty. A nil slice marshals
// as null, and a screen iterating the rates of a reseller that has none would be
// handed a null where its type says an array.
func ratesOrEmpty(rates []WireResellerRate) []WireResellerRate {
	held := make([]WireResellerRate, 0, len(rates))
	for _, rate := range rates {
		held = append(held, WireResellerRate{Row: strings.TrimSpace(rate.Row), DailyRateMicros: rate.DailyRateMicros})
	}
	return held
}

// validResellerTerms checks what operations set for one reseller: a credit limit
// that is not negative (D2189's ceiling, whose schema minimum is zero), and one
// rate per row of the policy table, priced at zero or more.
//
// The row is looked up and never trusted, as a mint's row is (F28, P5.1's
// operationsMint): a rate for a row the table does not carry is a price nothing
// can ever be charged against, and one row priced twice in a request would leave
// which of the two applies to the order of the list. A rate of zero is admitted
// and is not the same as no rate at all: `resellers.schema.json` reads a row with
// no rate as one this reseller cannot sell, which is a decision and not a price.
func validResellerTerms(asked WireReseller) error {
	if asked.CreditLimitMicros < 0 {
		return fmt.Errorf("%w: the limit is %d micros", errResellerBadCreditLimit, asked.CreditLimitMicros)
	}
	priced := make(map[string]bool, len(asked.Rates))
	for _, rate := range asked.Rates {
		row := strings.TrimSpace(rate.Row)
		if _, known := DefaultPolicy(row); !known {
			return fmt.Errorf("%w: %q", errOperationsBadRow, rate.Row)
		}
		if rate.DailyRateMicros < 0 {
			return fmt.Errorf("%w: the rate for %q is %d micros a day", errResellerBadRate, rate.Row, rate.DailyRateMicros)
		}
		if priced[row] {
			return fmt.Errorf("%w: %q is priced twice in this request", errResellerBadRate, rate.Row)
		}
		priced[row] = true
	}
	return nil
}

// createReseller creates one reseller organisation and the terms operations set
// for it. D2191 gives operations the act — only operations creates a reseller
// organisation — and D2189's limit belongs to the organisation from the instant
// it exists, so the two are one transaction: an organisation nothing may be sold
// through, because a create that committed alone would leave it with no credit
// and no rates, is not one anybody asked for.
//
// The organisation carries no accepted terms (P2.1's WireTerms, D2199) and no
// membership. Nobody signed up to it: operations made it, and the terms a sign-up
// records are the person's acceptance of the wording a deployment serves, which
// this act is not. Its first owner is invited the way the operations
// organisation's own first owner is (SeedOperations), and until one is invited it
// holds nothing.
//
// The line is written before the act (D456, operationsAudit), and the id it names
// is minted here so that the line and the record are about the same one: what a
// create that then failed leaves is a line saying somebody asked for a reseller
// with this id, which is true.
func (s *Service) createReseller(ctx context.Context, actor Caller, name string, asked WireReseller) (WireReseller, error) {
	if err := validResellerTerms(asked); err != nil {
		return WireReseller{}, err
	}
	id, err := newID()
	if err != nil {
		return WireReseller{}, err
	}
	now := s.now()
	org := WireOrganisation{ID: id, Name: name, Type: orgTypeReseller, CreatedAt: now}
	terms := WireReseller{
		OrganisationID:    org.ID,
		CreditLimitMicros: asked.CreditLimitMicros,
		Rates:             ratesOrEmpty(asked.Rates),
		CreatedAt:         now,
	}
	if err := s.operationsAudit(ctx, actor, actionResellerCreate, &org.ID, &org.ID, false, &org.Name); err != nil {
		return WireReseller{}, err
	}
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := putRecord(ctx, tx, organisationsCollection, org.ID, org); err != nil {
			return err
		}
		return putRecord(ctx, tx, resellerTermsCollection, org.ID, terms)
	})
	if err != nil {
		return WireReseller{}, err
	}
	return terms, nil
}

// setResellerTerms sets one reseller's credit limit and wholesale rates, which is
// the whole of what operations decides about its money (D2189, D2190): the accrue
// it may run up unpaid, and what a seat of each row costs it a day. It is a set
// and not a patch — the list the caller sends is the list the reseller has, so a
// row operations meant to withdraw is one it leaves out — and it writes no
// accrual and no record of one (P6.2, P6.3).
func (s *Service) setResellerTerms(ctx context.Context, actor Caller, resellerID string, asked WireReseller) (WireReseller, error) {
	if err := validResellerTerms(asked); err != nil {
		return WireReseller{}, err
	}
	org, err := s.resellerOrganisation(ctx, resellerID)
	if err != nil {
		return WireReseller{}, err
	}
	terms := WireReseller{
		OrganisationID:    org.ID,
		CreditLimitMicros: asked.CreditLimitMicros,
		Rates:             ratesOrEmpty(asked.Rates),
		CreatedAt:         org.CreatedAt,
	}
	if err := s.operationsAudit(ctx, actor, actionResellerTermsSet, &org.ID, &org.ID, false, nil); err != nil {
		return WireReseller{}, err
	}
	doc, err := storedDoc(terms)
	if err != nil {
		return WireReseller{}, err
	}
	if err := s.docs.Put(ctx, resellerTermsCollection, org.ID, doc); err != nil {
		return WireReseller{}, fmt.Errorf("keyservice: the reseller's terms could not be written: %w", err)
	}
	return terms, nil
}

// relinkResellerCustomer moves one customer organisation to another reseller, or
// links one that no reseller had (D2191, F14's resolution). Operations holds the
// act, as it holds every other thing about a reseller's money: a reseller that
// could link itself to a customer could claim its accrual (resellers.schema.json's
// own reading of the field).
//
// The link record and the organisation's own ResellerID are written together, in
// one transaction, because they are one fact with two readers: the reseller's list
// of customers is read from the records, the customer's channel is read from the
// organisation (P5.2's reports split on it), and a half of it written alone is a
// customer that appears in one reseller's list and reports under another.
//
// **A relink does not move money.** What it changes is which reseller the accrual
// from here belongs to; the ledger's lines carry the channel they had when they
// were written (ledger.schema.json), and the log's own line is what says the link
// moved.
func (s *Service) relinkResellerCustomer(ctx context.Context, actor Caller, asked WireResellerRelink) (WireResellerCustomer, error) {
	customer, err := s.customerOrganisation(ctx, asked.CustomerOrganisationID)
	if err != nil {
		return WireResellerCustomer{}, err
	}
	destination, err := s.resellerOrganisation(ctx, asked.ResellerOrganisationID)
	if err != nil {
		return WireResellerCustomer{}, err
	}
	if customer.ResellerID != nil && *customer.ResellerID == destination.ID {
		return WireResellerCustomer{}, errResellerAlreadyLinked
	}
	if err := s.operationsAudit(ctx, actor, actionResellerRelink, &customer.ID, &destination.ID, false, nil); err != nil {
		return WireResellerCustomer{}, err
	}
	link := WireResellerCustomer{
		OrganisationID:         customer.ID,
		Name:                   customer.Name,
		ResellerOrganisationID: destination.ID,
		LinkedAt:               s.now(),
	}
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		// The customer is read again inside the transaction, because what this
		// write is against is the organisation's own one ResellerID: a link that
		// landed between the read above and this write is a customer another
		// operator has just moved, and the second write would put the two links
		// back into disagreement rather than refusing.
		held, present, err := getRecord[WireOrganisation](ctx, tx, organisationsCollection, customer.ID)
		if err != nil {
			return err
		}
		if !present || held.Type != orgTypeCustomer {
			return errNoSuchOrganisation
		}
		if held.ResellerID != nil && *held.ResellerID == destination.ID {
			return errResellerAlreadyLinked
		}
		link.Name = held.Name
		held.ResellerID = &destination.ID
		if err := putRecord(ctx, tx, organisationsCollection, held.ID, held); err != nil {
			return err
		}
		return putRecord(ctx, tx, resellerCustomersCollection, held.ID, link)
	})
	if err != nil {
		return WireResellerCustomer{}, err
	}
	return link, nil
}

// createCustomerOrganisation makes one customer organisation for the reseller
// that asked, which is D2191's act: the reseller sells to somebody who has not
// signed up anywhere, so the tenant they will work in is the reseller's to make
// and theirs to own.
//
// **The organisation is written with no member at all, and that is the rule and
// not an omission.** F40: a reseller is never a member of its customers'
// organisations, and the membership this route could write — the reseller's own
// owner, as the first person in it — is exactly the one it must not. What it
// writes instead is the link, and the customer's first owner arrives by
// invitation (inviteCustomerFirstOwner), accepting it as their own membership of
// their own organisation.
//
// **It carries no accepted terms, and so no free seat.** The terms a sign-up
// records are the person's, of the wording the deployment serves (D2199), and
// nobody here accepted anything: the free seat's entitlement is granted by the
// organisation-created hook a sign-up runs (P3.1's freeSeatOnOrgCreated), and
// that hook refuses an organisation whose terms nobody accepted. A reseller's
// customer is one the reseller sells licences to, and it starts with the licence
// the reseller mints for it rather than with the free seat of a self-serve
// sign-up.
//
// **It is bounded by the day as a sign-up is.** F35's organisation limit (D2184)
// is asked here too, and twice: the account's own day, as a sign-up asks it, and
// the reseller's (refuseAnotherCustomer), because the count a sign-up takes reads
// the memberships an account owns and F40 means this path writes none.
func (s *Service) createCustomerOrganisation(ctx context.Context, actor Caller, held resellerAccess, name string) (WireOrganisation, error) {
	if err := held.write(); err != nil {
		return WireOrganisation{}, err
	}
	now := s.now()
	// F35's day (D2184), which this path owes as much as a sign-up does: an
	// account that may make three organisations a day may not make unlimited
	// ones by being a reseller, and every organisation this path makes is one a
	// person could otherwise mint in a loop. Both counts are taken before the id
	// is minted, so a refusal writes nothing at all.
	//
	// It is held from the counts to the write, as a sign-up holds it (signUpMu):
	// each count is a query and the write is a transaction beside it, so two
	// creations of a burst would each read a count that does not hold the other's
	// organisation yet and the day's limit would be passed by racing it. The
	// account's own count is the sign-up's own count, so this takes the sign-up's
	// lock rather than a second one: a sign-up in flight beside a creation reads
	// the same stale number.
	s.signUpMu.Lock()
	defer s.signUpMu.Unlock()
	if err := s.refuseAnotherOrganisation(ctx, actor.UID, now); err != nil {
		return WireOrganisation{}, err
	}
	if err := s.refuseAnotherCustomer(ctx, held.Organisation.ID, now); err != nil {
		return WireOrganisation{}, err
	}
	id, err := newID()
	if err != nil {
		return WireOrganisation{}, err
	}
	resellerID := held.Organisation.ID
	org := WireOrganisation{
		ID:   id,
		Name: name,
		Type: orgTypeCustomer,
		// The link, which D2191's act makes and which operations can move
		// (relinkResellerCustomer): it is a link and not a parent, and the
		// customer's own owners are its owners (P2.1's own reading of the field).
		ResellerID: &resellerID,
		CreatedAt:  now,
	}
	link := WireResellerCustomer{
		OrganisationID:         org.ID,
		Name:                   name,
		ResellerOrganisationID: resellerID,
		LinkedAt:               now,
	}
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := putRecord(ctx, tx, organisationsCollection, org.ID, org); err != nil {
			return err
		}
		return putRecord(ctx, tx, resellerCustomersCollection, org.ID, link)
	})
	if err != nil {
		return WireOrganisation{}, err
	}
	return org, nil
}

// refuseAnotherCustomer is F35's day (D2184) read for the organisations this path
// makes, which the sign-up's own count cannot see: that count reads the
// memberships an account owns, and a reseller is never a member of its customers
// (F40), so the customers it made are in no record of that shape. What holds them
// is the link this path writes for every one of them, whose instant is when the
// reseller took that customer on — so the day counted here is the reseller
// organisation's, shared by its owners and admins, and a relink counts against
// the reseller that took the customer on (relinkResellerCustomer writes a fresh
// instant, which is its own act and operations' decision, not this one's).
//
// The number and the window are the sign-up's own (orgDailyLimit, orgLimitWindow),
// because D2184 names one limit for an account's day and not a second one for
// resellers, and the refusal is that limit's own sentinel so that one sentence
// answers it wherever it is met. The whole record is read and not a count query:
// this store's reader takes one equality field, and a reseller's customers are a
// list the reseller's own screen draws whole anyway.
func (s *Service) refuseAnotherCustomer(ctx context.Context, resellerID string, now time.Time) error {
	links, err := recordsWhere[WireResellerCustomer](ctx, s.docs, resellerCustomersCollection,
		Where{Field: "reseller_organisation_id", Value: resellerID})
	if err != nil {
		return err
	}
	since := now.Add(-orgLimitWindow)
	taken := 0
	for _, link := range links {
		if link.LinkedAt.After(since) {
			taken++
		}
	}
	if taken >= orgDailyLimit {
		return errOrgDailyLimit
	}
	return nil
}

// resellerCustomer answers the link this reseller holds for one customer
// organisation, and the organisation itself: a customer of another reseller's,
// one that was never linked and an id nobody has are all errNoSuchResellerCustomer,
// which is what makes this the one check every reseller read of a customer passes.
//
// An organisation that is not in the store is that same refusal rather than an
// organisation with no name: such a pair is half of a deletion (P2.4), and a
// reseller's list is not the place to describe one (organisationsOf's own
// reading).
func (s *Service) resellerCustomer(ctx context.Context, held resellerAccess, customerID string) (WireOrganisation, WireResellerCustomer, error) {
	link, found, err := getRecord[WireResellerCustomer](ctx, s.docs, resellerCustomersCollection, strings.TrimSpace(customerID))
	if err != nil {
		return WireOrganisation{}, WireResellerCustomer{}, err
	}
	if !found || link.ResellerOrganisationID != held.Organisation.ID {
		return WireOrganisation{}, WireResellerCustomer{}, errNoSuchResellerCustomer
	}
	org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, link.OrganisationID)
	if err != nil {
		return WireOrganisation{}, WireResellerCustomer{}, err
	}
	if !present {
		return WireOrganisation{}, WireResellerCustomer{}, errNoSuchResellerCustomer
	}
	link.Name = org.Name
	return org, link, nil
}

// resellerCustomers answers the customers one reseller holds, newest link first,
// which is the list the reseller's screen draws (P6.4's customers section): the
// link, the organisation's name as it is now, and the day it was made.
//
// The name is read from the organisation and not from the link record's copy of
// it, because a name is a thing a customer's own owners change and a record
// written at linking time would answer the name the reseller typed months ago.
// The order is the link's instant and then the id, so that two customers linked
// in the same instant still have an order a page and a test can rely on.
func (s *Service) resellerCustomers(ctx context.Context, held resellerAccess) ([]WireResellerCustomer, error) {
	links, err := recordsWhere[WireResellerCustomer](ctx, s.docs, resellerCustomersCollection,
		Where{Field: "reseller_organisation_id", Value: held.Organisation.ID})
	if err != nil {
		return nil, err
	}
	customers := make([]WireResellerCustomer, 0, len(links))
	for _, link := range links {
		org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, link.OrganisationID)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		link.Name = org.Name
		customers = append(customers, link)
	}
	sort.Slice(customers, func(i, j int) bool {
		left, right := customers[i], customers[j]
		if left.LinkedAt.Equal(right.LinkedAt) {
			return left.OrganisationID < right.OrganisationID
		}
		return left.LinkedAt.After(right.LinkedAt)
	})
	return customers, nil
}

// customerKeyMetadata answers one customer organisation's keys as metadata, which
// is F5's whole of what a reseller may see of them and what spec §Resellers
// promises it ("those customers' keys and renewal dates").
//
// The metadata is read through operationsKeys and not by a second reader written
// here: a workspace's keys are read in one place in this service, and the shape
// that answers a reseller and the shape that answers the operations team are the
// same one (wire_keys.go's WireKeyMetadata). A second reader would be the place
// the two answers drift apart, and drifting apart here means one of them carrying
// something the other must not.
func (s *Service) customerKeyMetadata(ctx context.Context, customerID string) ([]WireKeyMetadata, error) {
	workspaces, err := recordsWhere[WireWorkspace](ctx, s.docs, workspacesCollection,
		Where{Field: "organisation_id", Value: customerID})
	if err != nil {
		return nil, err
	}
	keys := make([]WireKeyMetadata, 0)
	for _, workspace := range workspaces {
		held, err := s.operationsKeys(ctx, workspace.ID)
		if err != nil {
			return nil, err
		}
		keys = append(keys, held...)
	}
	oldestFirst(keys, func(one WireKeyMetadata) time.Time { return one.IssuedAt }, func(one WireKeyMetadata) string { return one.KeyID })
	return keys, nil
}

// inviteCustomerFirstOwner invites one address to be a customer organisation's
// first owner, which is the other half of D2191's act and the last thing a
// reseller does inside the customer's organisation (F40).
//
// **It is the FIRST owner and nothing after them.** The plan's own sentence, and
// the shape of F40's line: a reseller invites once, the person who accepts owns
// an organisation of their own, and from there its owners invite like any other
// organisation's (P2.2). So the act is refused for an organisation that already
// has a member or a live invitation, and it is not refused for one whose
// invitation expired or was withdrawn: an invitation nobody accepted leaves the
// organisation with nobody able to invite — the reseller is not a member and
// operations decides nothing for another organisation's people (P2.2's
// inviteAccess) — so refusing the reseller a second try would be an organisation
// nobody can ever reach.
//
// **A reseller's own people are not the answer to "who owns the customer".** An
// address that already holds a membership of the inviting reseller organisation is
// refused, whatever role it holds there: the membership the invitation becomes is
// filed under the accepting account (F40, P2.2), so a reseller that invited its
// own employee would have a person inside the customer's organisation as its
// owner — the customer's reveals, binds and purchases decided by the reseller,
// which is the line F40 draws read from the other side.
func (s *Service) inviteCustomerFirstOwner(ctx context.Context, actor Caller, held resellerAccess, customerID string, asked WireInviteCreate) (WireInvite, error) {
	if err := held.write(); err != nil {
		return WireInvite{}, err
	}
	org, _, err := s.resellerCustomer(ctx, held, customerID)
	if err != nil {
		return WireInvite{}, err
	}
	role := strings.TrimSpace(asked.Role)
	if role != roleOwner {
		return WireInvite{}, fmt.Errorf("%w: a reseller invites the customer's first owner and no other role", errResellerNotPermitted)
	}
	address := strings.TrimSpace(asked.Email)
	if _, err := access.Member(address); err != nil {
		return WireInvite{}, fmt.Errorf("%w: %v", errInviteEmailRefused, err)
	}
	members, err := membersOf(ctx, s.docs, org.ID)
	if err != nil {
		return WireInvite{}, err
	}
	if len(members) > 0 {
		return WireInvite{}, errFirstOwnerSet
	}
	invitations, err := recordsWhere[WireInvite](ctx, s.docs, invitesCollection,
		Where{Field: "organisation_id", Value: org.ID})
	if err != nil {
		return WireInvite{}, err
	}
	now := s.now()
	for _, invitation := range invitations {
		if inviteStateAt(invitation, now) == invitePending {
			return WireInvite{}, errFirstOwnerSet
		}
	}
	if err := s.refuseResellerOwnAddress(ctx, held.Organisation.ID, address); err != nil {
		return WireInvite{}, err
	}
	// The origin the mail's link is built from, read before anything is written,
	// for the reason createInvitation reads it there: a deployment this service
	// cannot build a usable link for refuses the invitation rather than leaving
	// one nobody can accept behind it.
	origin, err := portalOrigin(s.cfg.URL)
	if err != nil {
		return WireInvite{}, err
	}
	// F35's rate, spent last of all and before anything is written, exactly as
	// createInvitation spends it and for the same reason: the caller's standing is
	// known by now, so an account of another organisation cannot spend somebody
	// else's allowance by posting bodies that name it.
	//
	// It is spent in two buckets, and the second is the point: the customer's own
	// (which is where createInvitation spends it, and what bounds a customer's
	// invitations) and the reseller organisation's. Every customer this path makes
	// is a fresh organisation and so a fresh bucket, so without the reseller's own
	// an owner could invite once into each of as many customers as it could make
	// and meet no rate at all: refuseAnotherCustomer bounds the making, this
	// bounds the mailing, which is what the rate is for. The limiter's own map is
	// the second reason — a bound that added a bucket per organisation would be
	// the growth this rate exists to stop.
	if !s.invites.allow("organisation:"+org.ID, "organisation:"+held.Organisation.ID) {
		return WireInvite{}, errInviteRateLimited
	}
	id, err := newID()
	if err != nil {
		return WireInvite{}, err
	}
	invitation := WireInvite{
		ID:             id,
		OrganisationID: org.ID,
		Email:          address,
		Role:           roleOwner,
		State:          invitePending,
		// The inviter is the reseller's member who asked, as every invitation's
		// is: the person the customer's first owner is answering is the reseller
		// they bought from.
		InvitedBy: actor.Email,
		CreatedAt: now,
		ExpiresAt: now.Add(inviteTTL),
	}
	if err := s.putInvitation(ctx, invitation); err != nil {
		return WireInvite{}, err
	}
	// The invitation's own line, under the action P2.2 writes for every
	// invitation there is (invitation.create): the act is the same act, and a
	// second name for it would be a log a reader counts one thing under two.
	if err := s.recordAudit(ctx, actor, auditEntry{
		Action:         actionInvitationCreate,
		OrganisationID: &invitation.OrganisationID,
		TargetID:       &invitation.ID,
		Detail:         &invitation.Email,
	}); err != nil {
		return WireInvite{}, err
	}
	if err := s.mail.Send(ctx, invitationMail(origin, org.Name, invitation, inviteTTL)); err != nil {
		return WireInvite{}, err
	}
	return invitation, nil
}

// refuseResellerOwnAddress refuses an invitation whose address is already a member
// of the inviting reseller organisation. The two addresses are compared through
// access.Member on both sides, as every address comparison in this service is
// (invitedMember's own reading): the address as it was typed against the address
// a membership carries, so that a case difference is not a way past the rule and a
// capitalised local part is not one either.
//
// A membership whose address is no member at all is skipped rather than refused:
// such a record can admit nobody (F31's fail-closed half), and refusing the
// invitation over it would be refusing the invitation over somebody else's
// malformed record.
func (s *Service) refuseResellerOwnAddress(ctx context.Context, resellerID, address string) error {
	invited, err := access.Member(address)
	if err != nil {
		return fmt.Errorf("%w: %v", errInviteEmailRefused, err)
	}
	members, err := membersOf(ctx, s.docs, resellerID)
	if err != nil {
		return err
	}
	for _, member := range members {
		theirs, err := access.Member(member.Email)
		if err != nil {
			continue
		}
		if theirs == invited {
			return fmt.Errorf("%w: %s is already a member of the reseller organisation", errResellerNotPermitted, address)
		}
	}
	return nil
}
