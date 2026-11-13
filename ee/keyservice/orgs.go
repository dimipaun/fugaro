// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Organisations and memberships (portal plan P2.1; spec §Tenancy): the tenant
// every other record of the portal hangs off, the act that makes one, and the
// reads a membership admits.
//
// **A person belongs to organisations, and their role is in the membership.**
// The account is Firebase Auth's and the membership here is the whole of what
// the portal knows about it, filed under a name made of the organisation and the
// uid (membershipID): the membership a request uses is one read and never a
// scan, and a uid is the one identifier a person keeps when an address changes
// hands. A role is never carried in the sign-in token, because a role only the
// identity provider can take away is a role revocation has to wait an hour for.
//
// **Sign-up** creates the customer organisation, records the terms of service
// the person accepted, makes them its owner, and runs the hooks P3.1 registers
// the free seat's entitlement through (D2199, F6). The whole of it is one
// transaction, so an organisation exists with its owner, its acceptance and
// whatever its hooks wrote, or it does not exist at all.
//
// **What a request may read** is decided by the membership it uses, and the SSO
// rule follows that membership and not the organisation the request is about
// (D2207, F10): a sign-in by email link holding an operations or a reseller
// membership is refused for every request that membership carries, including one
// about a customer organisation the rule has nothing to say about. The
// operations membership is the one that reads across organisations, and every
// such read is written to the audit log (D456, F9) by the function that answers
// it (accessTo), so no route can read across organisations and forget to.
//
// F35's abuse control lives here too: an account may create so many
// organisations in a day, and no more (refuseAnotherOrganisation). What is
// counted is the organisations it CREATED and not the ones it belongs to.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The collections the tenancy's records live in. They are this area's own names
// and not the deploy's: the store's ids are the two names a backend files a
// document under, and a record that moves between collections is a migration.
const (
	organisationsCollection = "organisations"
	membershipsCollection   = "memberships"
)

// What an organisation is and what a person may decide in one (spec §Tenancy,
// D2186). The type is set once and never moved, because the SSO rule
// (ssoRuleFor) and what a reseller may read are both read from it; the role is
// per organisation, because the same person is an owner of one and a member of
// another.
const (
	orgTypeOperations = "operations"
	orgTypeCustomer   = "customer"
	orgTypeReseller   = "reseller"

	roleOwner  = "owner"
	roleAdmin  = "admin"
	roleMember = "member"
)

// maxOrganisationName is what a name may be, in characters, and it is the
// schema's bound: a name is shown in a switcher and in a page title, and one of
// a hundred kilobytes is one this service would otherwise hold and render.
const maxOrganisationName = 64

// orgDailyLimit is F35's abuse control (D2184): how many organisations one
// account may create in a day. D2184 rules that there is a limit and names no
// number, so the number here is this task's and the owner's to change: three is
// enough for a person who runs a couple of companies and small enough that an
// account set up to mint tenants in a loop is stopped at the fourth.
const orgDailyLimit = 3

// orgLimitWindow is what "a day" counts over: the last 24 hours and not the
// calendar day, because a limit that resets at midnight is one an account can
// spend twice in ten minutes.
const orgLimitWindow = 24 * time.Hour

// The refusals this area answers besides the API's own.
var (
	// errNoSuchOrganisation is an organisation that is not there, or one the
	// caller does not belong to and is not operations over. The two are one
	// answer on purpose: which organisation ids exist is a list of the portal's
	// customers, and telling a stranger that is telling them who to attack.
	errNoSuchOrganisation = errors.New("keyservice: the caller may not read this organisation")
	// errOrgDailyLimit is an account that has created every organisation a day
	// allows (F35, D2184).
	errOrgDailyLimit = errors.New("keyservice: this account has created all the organisations a day allows")
)

// OrgCreatedHook is one thing that happens when an organisation is created
// (D2199). P3.1 registers the free seat's entitlement: every organisation gets a
// seat, the seat is minted when it is bound, and creating it here — inside the
// sign-up's own transaction and before the organisation is written — is what
// makes it a grant rather than a key somebody can be mailed before their
// organisation exists.
//
// It is handed the transaction the sign-up is, which is the only place a hook
// can read: the store's transactions read before they write (docstore), so a
// hook that ran after the organisation was written could not ask the store
// anything, and a hook that failed after it would leave an organisation without
// its seat.
type OrgCreatedHook func(ctx context.Context, tx Tx, org WireOrganisation) error

// orgCreatedRegistry is the table the areas register their hooks into, like the
// API's routes and the wire's shapes, and for the same reason: P3.1 registers
// the free seat's entitlement from its own file's init and edits no file of this
// one (the plan's shared-files table gives orgs.go to P2.1).
type orgCreatedRegistry struct {
	mu    sync.Mutex
	hooks []registeredOrgHook
}

type registeredOrgHook struct {
	name string
	run  OrgCreatedHook
}

// orgCreatedHooks is the one table.
var orgCreatedHooks = &orgCreatedRegistry{}

// registerOrgCreated mounts one hook. A mistake here is a panic, for the reason
// registerAPI's is: an init has nobody to return an error to, and a table
// holding a hook that cannot run, or two hooks under one name, is worse than a
// service that does not start — a hook's failure is reported by its name, and a
// name two hooks share is a failure read as the other one's.
func registerOrgCreated(name string, hook OrgCreatedHook) {
	if strings.TrimSpace(name) == "" {
		panic("keyservice: an organisation-created hook has no name, and the name is what a failure of it is reported by")
	}
	if hook == nil {
		panic("keyservice: the organisation-created hook " + name + " has nothing to run")
	}
	orgCreatedHooks.mu.Lock()
	defer orgCreatedHooks.mu.Unlock()
	for _, held := range orgCreatedHooks.hooks {
		if held.name == name {
			panic("keyservice: two organisation-created hooks are registered as " + name + ", and a failure of one would be read as the other's")
		}
	}
	orgCreatedHooks.hooks = append(orgCreatedHooks.hooks, registeredOrgHook{name: name, run: hook})
}

// run is every registered hook over one new organisation, in the order the areas
// registered them (which is the order their files were initialised): a hook that
// fails ends the sign-up, and the organisation is not created without what the
// hook writes.
func (rg *orgCreatedRegistry) run(ctx context.Context, tx Tx, org WireOrganisation) error {
	rg.mu.Lock()
	held := slices.Clone(rg.hooks)
	rg.mu.Unlock()
	for _, hook := range held {
		if err := hook.run(ctx, tx, org); err != nil {
			return fmt.Errorf("the organisation %s was not created: the %s hook failed: %w", org.ID, hook.name, err)
		}
	}
	return nil
}

// organisationAccess is one answered read of one organisation: which
// organisation, and which membership admitted the caller to it.
type organisationAccess struct {
	Organisation WireOrganisation
	// Membership is the membership the access came from: the caller's own in
	// that organisation, or the caller's in the operations organisation.
	Membership WireMembership
	// Operations is true when the access came from the operations membership:
	// the one membership that reads across organisations (spec §Operations), and
	// the one the audit log is written for (D456, F9).
	Operations bool
}

// reachableOrganisation is one organisation the caller belongs to, with the
// membership's own record of when they last worked in it. The switcher's list is
// ordered by that and answers the organisation.
type reachableOrganisation struct {
	org        WireOrganisation
	lastUsedAt *time.Time
}

// membershipID is the name one person's place in one organisation is filed
// under: the two ids a request names — the organisation it is about and the uid
// the sign-in proved — joined, so that the membership a request uses is one read
// by id and never a scan of a collection. Both halves are a uid or an opaque id
// this service minted, so neither can hold the separator.
func membershipID(organisationID, uid string) string {
	return organisationID + ":" + uid
}

// recordGetter is the read the store and one of its transactions both answer:
// one document, by id. Everything here that reads inside a transaction goes
// through it, and the store's transactions read by id and nothing else (api.go's
// Tx holds no query) — which is why the day's count is not taken inside the
// sign-up's transaction and the sign-up is serialised instead (signUpMu).
type recordGetter interface {
	Get(ctx context.Context, collection, id string) (Doc, error)
}

// recordReader is the same read plus the one-field query, which the store itself
// answers and a transaction does not.
type recordReader interface {
	recordGetter
	Query(ctx context.Context, collection string, where Where) ([]Doc, error)
}

// storedDoc is one record as the store holds it: the store's documents are JSON
// objects (api.go's Doc) and this package's records are the wire shapes, so the
// two are one marshalling apart in each direction.
func storedDoc(record any) (Doc, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("keyservice: a record is not JSON: %w", err)
	}
	var doc Doc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("keyservice: a record is not a JSON object: %w", err)
	}
	return doc, nil
}

// readDoc is the other direction: one stored document as the shape it was
// written as.
func readDoc(doc Doc, record any) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("keyservice: a stored document is not JSON: %w", err)
	}
	if err := json.Unmarshal(raw, record); err != nil {
		return fmt.Errorf("keyservice: a stored document is not the record it is read as: %w", err)
	}
	return nil
}

// getRecord reads one record of one collection. found is false for a document
// the store does not hold, which is an answer — an organisation nobody created, a
// membership nobody holds — and not a failure.
func getRecord[T any](ctx context.Context, from recordGetter, collection, id string) (T, bool, error) {
	var record T
	doc, err := from.Get(ctx, collection, id)
	if errors.Is(err, ErrNoDocument) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	if err := readDoc(doc, &record); err != nil {
		return record, false, err
	}
	return record, true, nil
}

// recordsWhere reads every record of one collection whose field holds one value.
func recordsWhere[T any](ctx context.Context, from recordReader, collection string, where Where) ([]T, error) {
	held, err := from.Query(ctx, collection, where)
	if err != nil {
		return nil, err
	}
	records := make([]T, 0, len(held))
	for _, doc := range held {
		var record T
		if err := readDoc(doc, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// putRecord writes one record under its own id.
func putRecord(ctx context.Context, tx Tx, collection, id string, record any) error {
	doc, err := storedDoc(record)
	if err != nil {
		return err
	}
	return tx.Put(ctx, collection, id, doc)
}

// membershipsOf is every membership one account holds, which is what the
// switcher's list and the day's count are both read from.
func membershipsOf(ctx context.Context, from recordReader, uid string) ([]WireMembership, error) {
	return recordsWhere[WireMembership](ctx, from, membershipsCollection, Where{Field: "uid", Value: uid})
}

// membersOf is one organisation's members, in the store's own order, which is the
// membership's name and therefore the organisation's and the uid's.
func membersOf(ctx context.Context, from recordReader, organisationID string) ([]WireMembership, error) {
	return recordsWhere[WireMembership](ctx, from, membershipsCollection, Where{Field: "organisation_id", Value: organisationID})
}

// ssoRuleFor is what an organisation's type demands of a sign-in, and false for a
// customer organisation, which demands nothing of one: the rule follows an
// operations and a reseller membership and no other (D2207, F10).
//
// A reseller's pinned directories are operations' to set (P5.1) and this area
// holds none, so until one is set D2207's own fallback stands: a reseller is
// Google-only, which is the rule's zero value — no pinned directory, so every
// Microsoft sign-in refused — and the state P1.3 recorded the recorded shape in.
func ssoRuleFor(org WireOrganisation) (SSORule, bool) {
	switch org.Type {
	case orgTypeOperations:
		return SSORule{Operations: true}, true
	case orgTypeReseller:
		return SSORule{}, true
	}
	return SSORule{}, false
}

// requireSSO applies the rule the membership in org demands to this request's
// sign-in (D2207, F10). It is told the organisation the membership is IN and
// never the organisation the request is about: a person who belongs to an
// operations or a reseller organisation is refused by email link for every
// request that membership carries, including one about a customer organisation.
//
// The rule itself is P1.3's and is applied through the sign-in seam, so a
// deployment's verifier answers it: what this file decides is which rule a
// membership's organisation carries and nothing about what a rule admits.
func (s *Service) requireSSO(actor Caller, org WireOrganisation) error {
	rule, follows := ssoRuleFor(org)
	if !follows {
		return nil
	}
	return s.signIn.RequireSSO(actor, rule)
}

// ownMembership reads one organisation and the caller's own membership in it,
// with the rule that organisation's type demands already applied. An organisation
// that is not there, and one the caller does not belong to, are both found=false:
// the caller is told the same thing about either, and the difference is only what
// this service did with the request.
func (s *Service) ownMembership(ctx context.Context, actor Caller, organisationID string) (WireOrganisation, WireMembership, bool, error) {
	org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, organisationID)
	if err != nil || !present {
		return WireOrganisation{}, WireMembership{}, false, err
	}
	membership, member, err := getRecord[WireMembership](ctx, s.docs, membershipsCollection, membershipID(org.ID, actor.UID))
	if err != nil || !member {
		return WireOrganisation{}, WireMembership{}, false, err
	}
	if err := s.requireSSO(actor, org); err != nil {
		return WireOrganisation{}, WireMembership{}, false, err
	}
	return org, membership, true, nil
}

// operationsMembership answers the operations organisation and the caller's own
// membership in it: the operations organisation is whose members read across
// every organisation in the portal (spec §Operations).
//
// Which organisation that is comes from the organisations themselves and not
// from configuration, because the operations organisation is seeded as an
// invitation (P2.2) and a deployment that seeded none has no operations member
// at all rather than one named by a config field nobody set.
func (s *Service) operationsMembership(ctx context.Context, actor Caller) (WireOrganisation, WireMembership, bool, error) {
	orgs, err := recordsWhere[WireOrganisation](ctx, s.docs, organisationsCollection, Where{Field: "type", Value: orgTypeOperations})
	if err != nil {
		return WireOrganisation{}, WireMembership{}, false, err
	}
	for _, org := range orgs {
		membership, member, err := getRecord[WireMembership](ctx, s.docs, membershipsCollection, membershipID(org.ID, actor.UID))
		if err != nil {
			return WireOrganisation{}, WireMembership{}, false, err
		}
		if member {
			return org, membership, true, nil
		}
	}
	return WireOrganisation{}, WireMembership{}, false, nil
}

// accessTo turns a request into a read of one organisation, and writes the audit
// line an operations read is (D456, F9) before it answers: an entry written after
// the answer is one a failure can lose while the caller already holds the
// organisation.
//
// A caller reaches an organisation in two ways, and they are not the same act.
// Their own membership is a person reading an organisation they belong to, which
// nobody is told about. The operations membership is the operations team reading
// somebody else's organisation, which is what the log exists for. Anything else
// is errNoSuchOrganisation, whatever the reason was.
//
// **What is audited is the read across organisations**, which is the capability
// the operations membership grants: a person who belongs to the organisation they
// are reading is reading their own, whatever else they belong to, and auditing
// every read of every operations member's own organisations would be a log of the
// operations team's ordinary work.
func (s *Service) accessTo(ctx context.Context, actor Caller, organisationID, action string) (organisationAccess, error) {
	org, membership, found, err := s.ownMembership(ctx, actor, organisationID)
	if err != nil {
		return organisationAccess{}, err
	}
	if found {
		return organisationAccess{Organisation: org, Membership: membership}, nil
	}
	operations, opsMembership, member, err := s.operationsMembership(ctx, actor)
	if err != nil {
		return organisationAccess{}, err
	}
	if !member {
		return organisationAccess{}, errNoSuchOrganisation
	}
	if err := s.requireSSO(actor, operations); err != nil {
		return organisationAccess{}, err
	}
	if operations.ID == organisationID {
		// The caller is not in the operations organisation (ownMembership said
		// so), and there is nothing above it to read it through: the operations
		// organisation is not one its own team reaches by reading across.
		return organisationAccess{}, errNoSuchOrganisation
	}
	target, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, organisationID)
	if err != nil {
		return organisationAccess{}, err
	}
	if !present {
		return organisationAccess{}, errNoSuchOrganisation
	}
	if err := s.recordAudit(ctx, actor, auditEntry{
		Action:         action,
		OrganisationID: &target.ID,
		Read:           true,
	}); err != nil {
		return organisationAccess{}, err
	}
	return organisationAccess{Organisation: target, Membership: opsMembership, Operations: true}, nil
}

// organisationsOf answers the organisations one account belongs to, the one they
// last worked in first: it is the switcher's list, and its head is where a person
// lands who has not chosen one in this session (spec §Tenancy).
//
// A membership whose organisation is not in the store is left out rather than
// answered as an organisation with no name: what such a pair is, is half of a
// deletion, and the list is not the place to describe one. The order is the last
// use, then the id, so that two organisations nobody has worked in still have an
// order a page and a test can rely on.
func (s *Service) organisationsOf(ctx context.Context, actor Caller) ([]WireOrganisation, error) {
	held, err := membershipsOf(ctx, s.docs, actor.UID)
	if err != nil {
		return nil, err
	}
	reached := make([]reachableOrganisation, 0, len(held))
	for _, membership := range held {
		org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, membership.OrganisationID)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		reached = append(reached, reachableOrganisation{org: org, lastUsedAt: membership.LastUsedAt})
	}
	sort.Slice(reached, func(i, j int) bool {
		left, right := reached[i], reached[j]
		switch {
		case left.lastUsedAt == nil && right.lastUsedAt == nil:
			return left.org.ID < right.org.ID
		case left.lastUsedAt == nil:
			return false
		case right.lastUsedAt == nil:
			return true
		case left.lastUsedAt.Equal(*right.lastUsedAt):
			return left.org.ID < right.org.ID
		default:
			return left.lastUsedAt.After(*right.lastUsedAt)
		}
	})
	orgs := make([]WireOrganisation, 0, len(reached))
	for _, one := range reached {
		orgs = append(orgs, one.org)
	}
	return orgs, nil
}

// signUp creates the customer organisation a person signs up with, and answers
// it. The whole of the act is one transaction — the hooks, the organisation, the
// owner's membership — so an organisation exists with its owner, its accepted
// terms and whatever P3.1's hook wrote (the free seat's entitlement, D2199), or
// it does not exist at all and the person is told so.
//
// The founder's membership is also their selection of the new organisation:
// sign-up is the one selection nobody makes, and it is where the switcher lands
// them the next time they arrive.
func (s *Service) signUp(ctx context.Context, actor Caller, name string) (WireOrganisation, error) {
	id, err := newID()
	if err != nil {
		return WireOrganisation{}, err
	}
	now := s.now()
	org := WireOrganisation{
		ID:   id,
		Name: name,
		Type: orgTypeCustomer,
		// The version is the deployment's and not the body's: the deployment
		// serves the wording, so the deployment says which wording an acceptance
		// names (D2199). Nothing in a sign-up's body can name a version, and the
		// shape it is read through has no field for one.
		Terms:     &WireTerms{Version: s.cfg.TermsVersion, AcceptedAt: now},
		CreatedAt: now,
	}
	owner := WireMembership{
		OrganisationID: id,
		UID:            actor.UID,
		Email:          actor.Email,
		Role:           roleOwner,
		CreatedAt:      now,
		LastUsedAt:     &now,
	}
	s.signUpMu.Lock()
	defer s.signUpMu.Unlock()
	if err := s.refuseAnotherOrganisation(ctx, actor.UID, now); err != nil {
		return WireOrganisation{}, err
	}
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := orgCreatedHooks.run(ctx, tx, org); err != nil {
			return err
		}
		if err := putRecord(ctx, tx, organisationsCollection, org.ID, org); err != nil {
			return err
		}
		return putRecord(ctx, tx, membershipsCollection, membershipID(org.ID, actor.UID), owner)
	})
	if err != nil {
		return WireOrganisation{}, err
	}
	return org, nil
}

// refuseAnotherOrganisation refuses a sign-up when the account has created every
// organisation a day allows (F35, D2184), counted over the last 24 hours.
//
// What is counted is the memberships the account OWNS: being invited into
// somebody else's organisation is not creating one, and an account that belongs
// to ten has created none of them. The count reads the memberships' own
// timestamps because the organisation's instant is not on the membership, and a
// founder's membership is written by the sign-up itself — so the count is taken
// before the transaction and the whole of the sign-up is under signUpMu, which
// is what keeps two of them from reading one count (signUpMu's own comment).
func (s *Service) refuseAnotherOrganisation(ctx context.Context, uid string, now time.Time) error {
	held, err := membershipsOf(ctx, s.docs, uid)
	if err != nil {
		return err
	}
	since := now.Add(-orgLimitWindow)
	created := 0
	for _, membership := range held {
		if membership.Role == roleOwner && membership.CreatedAt.After(since) {
			created++
		}
	}
	if created >= orgDailyLimit {
		return errOrgDailyLimit
	}
	return nil
}

// selectOrganisation records that this person last worked in this organisation,
// which is what the switcher lands them on the next time (spec §Tenancy).
//
// It is one field of the caller's own record and it is still a transaction: the
// store has no field-level update (firestore_store.go), so the record is written
// back whole, and a record written back from a read taken outside a transaction
// could put back a role somebody changed in between.
func (s *Service) selectOrganisation(ctx context.Context, actor Caller, organisationID string) error {
	return s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		org, present, err := getRecord[WireOrganisation](ctx, tx, organisationsCollection, organisationID)
		if err != nil {
			return err
		}
		if !present {
			return errNoSuchOrganisation
		}
		if err := s.requireSSO(actor, org); err != nil {
			return err
		}
		membership, member, err := getRecord[WireMembership](ctx, tx, membershipsCollection, membershipID(org.ID, actor.UID))
		if err != nil {
			return err
		}
		if !member {
			return errNoSuchOrganisation
		}
		now := s.now()
		membership.LastUsedAt = &now
		return putRecord(ctx, tx, membershipsCollection, membershipID(org.ID, actor.UID), membership)
	})
}

// validOrganisationName answers whether a name is one an organisation may be
// created with. The bound is the schema's and it is counted in characters
// because that is what the schema counts: a name is text a person reads.
func validOrganisationName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maxOrganisationName
}
