// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The operations API's domain half (portal plan P5.1; D2187, D2188, D2190,
// D2204, D2207, D454, D456, F9, F28, F29, F40).
//
// Operations is the one actor in the portal that acts across organisations: it
// grants a term to any workspace, changes a term an organisation holds, moves a
// project id from one workspace to another (D2204, TN-P1), sets the two lists
// the sign-in rule reads (D2207, F40) and decides what the checks parked. Who
// may do that is D2187: an owner or an admin of the operations organisation
// changes things, a member reads, and every one of those actions is written to
// the log, reads included (D456).
//
// **A change needs a sign-in from the last five minutes** (D2187), and it is
// applied here rather than through the sign-in seam: `SignInVerifier` carries
// `Verify` and `RequireSSO` and no age rule (api.go), so the window
// `portalauth.go`'s `FreshWithin` states for the portal's own doors is applied
// by this area to the caller the API's seam answered. It is one rule read
// twice, and the two must agree: `operationsFreshSignIn` is the five minutes
// the SPA draws its step-up over (`screens/ops/ops.tsx`), and the skew is
// P1.3's own constant.
//
// **What operations sees is metadata and no key** (F5): an entitlement, a
// workspace, an issuance record's own figures and a log line. The one thing
// operations may do with a secret is take it away, which is the handover cut
// below — it closes the window a reissue's successor can still be fetched in
// and never hands anything over.
//
// **Nothing in this file trusts a body**: the row is looked up rather than
// believed (F28), the seats are the row's where the row carries them (D2184),
// the term is bounded by the row's own (F30), and the check interval is inside
// F28's daily-to-monthly span. What a request cannot do is change what the
// minted key says: the policy is the row's, and a caller that could name the
// policy could mint a key that never checks in.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// operationsQueueCollection holds the operations mints D454's rate held back.
// It is this service's own name and not the deploy's, for the reason the
// tenancy's collections are (orgs.go): the store's ids are the two names a
// backend files a document under, and a record that moves between collections
// is a migration.
const operationsQueueCollection = "operations_queue"

// operationsFreshSignIn is D2187's five minutes: how old the `auth_time` of the
// sign-in behind a change may be. Reads are not held by it — a member reading
// the screens all afternoon is a member reading, and the log says who looked.
const operationsFreshSignIn = 5 * time.Minute

// maxOperationsBodyBytes is the cap on every body this area reads: the largest
// of them is two lists of short strings, so a body the SPA builds fits in it by
// orders of magnitude.
const maxOperationsBodyBytes = 4 << 10

// maxOperationsAuditEntries is how much of the log one read answers. The log
// only grows and this route has no cursor, so the read is the newest page and
// says so in its own comment: a screen that drew ten thousand lines would be
// drawing a page no person reads to the end.
const maxOperationsAuditEntries = 100

// maxOperationsLookupTerm bounds a lookup's term. A name is at most
// `maxOrganisationName` characters and an id is shorter than that, so a longer
// term is one that can match nothing — and it is also what a log line's detail
// is bounded by, since the term is what the line carries.
const maxOperationsLookupTerm = maxOrganisationName

// ReasonOperations is the store's reason for the keys an operations revoke
// withdraws (F29). It sits beside the merchant's own (§8's list is one list):
// what a licensor reads from it is which keys are out and why, and a refund's
// word for a licence operations ended would be a support ticket answered
// wrongly. What the operations member wrote about the action is the audit
// line's and not this (wire_ops.go's Reason).
const ReasonOperations = "operations"

// operationsSeatSource names the record of the key a seat change reissued. A
// record's Source is the issuance's identity and §5.2's idempotency is "has this
// source been minted for?", so it cannot be a name two reissues share: this act
// has no merchant event id to borrow the way `seatChanged` borrows one, and the
// id the prefix carries is the record's own.
const operationsSeatSource = "operations-seats:"

// What one operations request may do. The three are the three gates D2187
// states: a member reads, an owner or an admin changes, and the two lists the
// team's own door is configured with are the owner's alone (D455, F40).
type operationsNeed int

const (
	operationsMayRead operationsNeed = iota
	operationsMayChange
	operationsMaySetLists
)

// The four actions the one action route reads (wire_ops.go).
const (
	operationsExtend = "extend"
	operationsSeats  = "seats"
	operationsComp   = "comp"
	operationsRevoke = "revoke"
)

// The two decisions one parked event gets (wire_ops.go).
const (
	operationsAttach  = "attach"
	operationsDismiss = "dismiss"
)

// The actions this area writes, spelled `noun.verb` as wire_audit.go requires.
// They sit beside P2.1's two (audit.go) because the log's list is one list: a
// reader filters it by the name, and two names for one act would be two acts to
// count. `organisation.members.read` is P2.1's and is reused by `/ops/members`,
// which is the same read at a second door rather than a second act.
const (
	actionOperationsMembershipRead = "operations.membership.read"
	actionOperationsListsRead      = "operations.lists.read"
	actionOperationsListsSet       = "operations.lists.set"
	actionOrganisationSearch       = "organisation.search"
	actionWorkspaceList            = "workspace.list"
	actionEntitlementList          = "entitlement.list"
	actionKeyList                  = "key.list"
	actionQueueRead                = "queue.read"
	actionLedgerRead               = "ledger.read"
	actionAuditRead                = "audit.read"
	actionEntitlementMint          = "entitlement.mint"
	actionEntitlementQueued        = "entitlement.queued"
	actionEntitlementExtend        = "entitlement.extend"
	actionEntitlementSeats         = "entitlement.seats"
	actionEntitlementComp          = "entitlement.comp"
	actionEntitlementRevoke        = "entitlement.revoke"
	actionProjectTransfer          = "project.transfer"
	actionKeyHandoverCut           = "key.handover.cut"
	actionLedgerEventAttach        = "ledger.event.attach"
	actionLedgerEventDismiss       = "ledger.event.dismiss"
)

// The refusals this area answers. Every one of them is a sentinel rather than a
// message built where it failed, so the routes can answer each with the status
// and the sentence its own reader branches on.
var (
	// errNotOperations is a caller who is not a member of the operations
	// organisation at all. It is a 403 and not a 404: the operations screens are
	// a surface everyone knows exists, and the caller is signed in and simply is
	// not one of the team (the SPA draws the sentence and nothing else).
	errNotOperations = errors.New("keyservice: the caller is not a member of the operations organisation")
	// errOperationsReadOnly is a member of the operations organisation asking to
	// change something. D2187 gives members the reads and no act.
	errOperationsReadOnly = errors.New("keyservice: changing a term is an operations owner's or admin's act")
	// errOperationsListsAreTheOwners is an admin asking to set the domain and
	// directory lists. D2187 leaves D455's owner-only rule standing for the
	// operations team's own door (D2207, F40).
	errOperationsListsAreTheOwners = errors.New("keyservice: the domain and directory lists are the operations owner's")
	// errStaleOperationsSignIn is D2187's five minutes: an `auth_time` older
	// than that is a session that has been open all afternoon, which is not the
	// same evidence as one opened a minute ago.
	errStaleOperationsSignIn = errors.New("keyservice: a change needs a sign-in from the last five minutes (D2187)")
	// errOperationsNoEntitlement is an id no licence has.
	errOperationsNoEntitlement = errors.New("keyservice: the store holds no licence with that id")
	// errOperationsNoWorkspace is an id no workspace has.
	errOperationsNoWorkspace = errors.New("keyservice: the store holds no workspace with that id")
	// errOperationsBadRow is a row the policy table does not carry. The row is
	// looked up and never trusted, because the tier, the term, the grace and the
	// check interval are the row's (D2133, F28).
	errOperationsBadRow = errors.New("keyservice: that row is not one the policy table carries")
	// errOperationsBadSeats is a seat count nobody can use, or one the row
	// carries its own of (D2184).
	errOperationsBadSeats = errors.New("keyservice: that seat count is not one this row may be granted with")
	// errOperationsNotActive is a term action asked of a licence that is not
	// serving: a revoked one, and a superseded one, which a grant in the same
	// workspace already replaced. F29's rule is that such a licence is one no key
	// is minted from again, and the seat change is the one action of the four that
	// mints.
	errOperationsNotActive = errors.New("keyservice: that licence is not active")
	// errOperationsBadTerm is an end outside the row's own term: F30 lets
	// operations shorten a term and never exceed the row.
	errOperationsBadTerm = errors.New("keyservice: that end is not inside the row's own term")
	// errOperationsCheckInterval is an interval outside F28's daily-to-monthly
	// span, or one asked for a row whose key never checks the server.
	errOperationsCheckInterval = errors.New("keyservice: that check interval is not one this row may be granted with")
	// errOperationsUnknownAction is one of the four words the action route reads
	// and none of them.
	errOperationsUnknownAction = errors.New("keyservice: that is not one of the four actions an entitlement takes")
	// errOperationsNoEvent is an event id the store does not hold.
	errOperationsNoEvent = errors.New("keyservice: the store holds no event with that id")
	// errOperationsEventAnswered is an event that is not parked: one already
	// attached, dismissed, or never parked at all.
	errOperationsEventAnswered = errors.New("keyservice: that event is not one waiting to be decided")
	// errOperationsBadDecision is a resolution that attaches nothing, or one
	// that dismisses an event while naming a term (wire_ops.go: a dismissal
	// attaches nothing).
	errOperationsBadDecision = errors.New("keyservice: that resolution is not an attach or a dismissal")
	// errOperationsProjectUnbound is a project id no workspace holds, and the
	// one a transfer read before its transaction held and its transaction no
	// longer does.
	errOperationsProjectUnbound = errors.New("keyservice: that Firebase project is bound to no workspace")
	// errOperationsSameWorkspace is a transfer to the workspace that already
	// holds the id: there is nothing to move and nothing to withdraw.
	errOperationsSameWorkspace = errors.New("keyservice: that workspace already holds this project id")
	// errOperationsDestinationBound is a transfer into a workspace bound to
	// another project. The record would then name one project while the key the
	// installation runs was minted for another, and nothing here can tell which
	// of the two the customer meant.
	errOperationsDestinationBound = errors.New("keyservice: that workspace is bound to another Firebase project")
	// errOperationsNoHandover is a key with no handover open, which is a key
	// whose window has already closed as well as one that never had one.
	errOperationsNoHandover = errors.New("keyservice: no handover is open for that key")
	// errOperationsBadQuery is the lookup's term: empty, or longer than a name
	// can be.
	errOperationsBadQuery = errors.New("keyservice: that lookup names nothing to look for")
	// errOperationsBadState is the queue's state: the only queue whose items
	// have a shape is the parked events (F20).
	errOperationsBadState = errors.New("keyservice: that is not the state of a queue this API serves")
	// errOperationsBadList is one of the two lists, refused by its own schema:
	// a domain that is not one, or a directory that is empty.
	errOperationsBadList = errors.New("keyservice: that list is not one the schema takes")
	// errOperationsOverCap is D454's rate: the tier's window is spent. It is an
	// answer and not a lost request — the mint is queued before this is
	// returned, and `admin.go`'s own word for the state is the sentence the
	// route answers with (refuseOverCap).
	errOperationsOverCap = errors.New("keyservice: this tier's mint limit for the window is reached")
)

// operationsMintMu serialises an operations mint's count and its grant.
//
// The count and the grant are two steps and D454's cap is one number: two mints
// arriving together both read the window and both write a grant, and the cap
// that the second one was supposed to meet is the one the first already spent.
// The store has no counter to be read and written in one transaction — the cap
// is counted over the entitlements themselves — so the two are read and written
// under one lock, which is what `admin.go`'s page gets from `signUpMu`'s
// pattern at a bigger scale (orgs.go) and what one process serves here.
//
// It is a package-level mutex and not a field of Service because the Service is
// the same value for every request it answers and a field would have to be
// added to it — which is P5.1's own business and not the shape the rest of this
// package reads.
var operationsMintMu sync.Mutex

// named is one string as a pointer, for the records whose fields are optional:
// an audit entry's organisation and target are nil where the action named none,
// and a local's address is how the writer says which of the two it is.
func named(value string) *string { return &value }

// operationsAccess is who is asking and what the operations organisation is:
// the two things every route of this area needs, read once.
type operationsAccess struct {
	Organisation WireOrganisation
	Membership   WireMembership
}

// operationsFor is the gate every route of this area passes: the caller is a
// member of the operations organisation, that organisation's sign-in rule is
// satisfied, and the caller holds the role the request's own kind needs.
//
// The rule is applied on the operations membership and not on the organisation
// a request is about (D2207): what a request USES is the membership that widens
// it across tenants, and the organisation it acts on is somebody else's.
func (s *Service) operationsFor(ctx context.Context, actor Caller, need operationsNeed) (operationsAccess, error) {
	org, membership, member, err := s.operationsMembership(ctx, actor)
	if err != nil {
		return operationsAccess{}, err
	}
	if !member {
		return operationsAccess{}, errNotOperations
	}
	if err := s.requireSSO(actor, org); err != nil {
		return operationsAccess{}, err
	}
	switch {
	case need == operationsMayChange && membership.Role != roleOwner && membership.Role != roleAdmin:
		return operationsAccess{}, errOperationsReadOnly
	case need == operationsMaySetLists && membership.Role != roleOwner:
		return operationsAccess{}, errOperationsListsAreTheOwners
	}
	return operationsAccess{Organisation: org, Membership: membership}, nil
}

// requireFreshOperationsSignIn applies D2187's five minutes to the caller the
// API's own seam answered.
//
// The rule is P1.3's (`portalauth.go`'s `FreshWithin`) read here on the shape
// this half of the service holds — a `Caller` and not a `VerifiedSignIn` — and
// that is why it is written out rather than called: the seam carries `Verify`
// and `RequireSSO` alone (api.go's SignInVerifier), and the age is what the
// caller's own `AuthTime` states. A sign-in with no readable `auth_time` is
// refused for `FreshWithin`'s own reason: a step-up over a fact nobody recorded
// is no step-up.
func (s *Service) requireFreshOperationsSignIn(actor Caller) error {
	now := s.now()
	if actor.AuthTime.IsZero() {
		return fmt.Errorf("%w: the token attests no time this sign-in happened", errStaleOperationsSignIn)
	}
	if actor.AuthTime.After(now.Add(signInClockSkew)) || now.Sub(actor.AuthTime) > operationsFreshSignIn {
		return fmt.Errorf("%w: this sign-in is older than %s", errStaleOperationsSignIn, operationsFreshSignIn)
	}
	return nil
}

// operationsAudit writes the line one operations action is and answers the
// failure of writing it rather than carrying on. Every caller of it writes
// before the act it describes, for the reason accessTo does the same: an entry
// written afterwards is one a failure can lose while the caller already holds
// what the entry is about, and an act that carries on after its line was not
// written is the one thing the log exists to prevent (D456).
func (s *Service) operationsAudit(ctx context.Context, actor Caller, action string, organisationID, targetID *string, read bool, detail *string) error {
	return s.recordAudit(ctx, actor, auditEntry{
		Action:         action,
		OrganisationID: organisationID,
		TargetID:       targetID,
		Read:           read,
		Detail:         detail,
	})
}

// operationsQueued is one mint D454's rate held back: the whole of the request,
// because the term is served from the instant it is served rather than from the
// instant it was asked for.
//
// It is an internal record and not a wire shape (F20): nothing outside this
// service reads it, the queue is drained by the service itself, and the id it
// carries is the entitlement's own — which is what makes serving it twice write
// one licence rather than two.
type operationsQueued struct {
	ID             string        `json:"id"`
	OrganisationID string        `json:"organisation_id"`
	WorkspaceID    string        `json:"workspace_id"`
	Row            string        `json:"row"`
	Tier           string        `json:"tier"`
	Seats          int           `json:"seats"`
	Term           time.Duration `json:"term"`
	QueuedAt       time.Time     `json:"queued_at"`
	ActorUID       string        `json:"actor_uid"`
	ActorEmail     string        `json:"actor_email"`
}

// operationsMint grants a term on the operations team's behalf, or queues it
// when D454's rate for the tier is spent. The two are told apart by the answer
// and not by a flag: a queued request is held back and answers the cap's own
// refusal, whose sentence says what happened to it, while a granted one answers
// the record it wrote.
//
// A queued request has no record yet, and that is deliberate: the term it is
// served with starts at the instant it is served (D2197's rule read the other
// way round — nothing here was paid for at the instant it was asked for), so
// writing the record first would be a licence whose term ran while it waited.
func (s *Service) operationsMint(ctx context.Context, actor Caller, asked WireEntitlementMint) (WireEntitlement, error) {
	row, known := DefaultPolicy(asked.Row)
	if !known {
		return WireEntitlement{}, fmt.Errorf("%w: %q", errOperationsBadRow, asked.Row)
	}
	if err := operationsIntervalOK(row, asked.CheckInterval); err != nil {
		return WireEntitlement{}, err
	}
	if asked.Seats < 1 || (row.Seats != 0 && asked.Seats != row.Seats) {
		return WireEntitlement{}, fmt.Errorf("%w: the %s row carries %d seat(s) and this request names %d", errOperationsBadSeats, row.Name, row.Seats, asked.Seats)
	}
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, asked.WorkspaceID)
	if err != nil {
		return WireEntitlement{}, err
	}
	if !present {
		return WireEntitlement{}, errOperationsNoWorkspace
	}
	now := s.now().UTC()
	ends := asked.EndsAt.UTC()
	if !ends.After(now) || ends.After(now.Add(row.Term)) {
		return WireEntitlement{}, fmt.Errorf("%w: a grant of the %s row runs from %s and at most %s from there", errOperationsBadTerm, row.Name, now, row.Term)
	}
	operationsMintMu.Lock()
	defer operationsMintMu.Unlock()
	s.serveQueuedOperations(ctx, now)
	within, err := s.operationsRoom(ctx, row.Tier, now)
	if err != nil {
		return WireEntitlement{}, err
	}
	if !within {
		if err := s.queueOperationsMint(ctx, actor, row, workspace, asked.Seats, ends.Sub(now), now); err != nil {
			return WireEntitlement{}, err
		}
		return WireEntitlement{}, errOperationsOverCap
	}
	id, err := newID()
	if err != nil {
		return WireEntitlement{}, err
	}
	if err := s.operationsAudit(ctx, actor, actionEntitlementMint, &workspace.OrganisationID, &id, false, nil); err != nil {
		return WireEntitlement{}, err
	}
	workspaceID := workspace.ID
	granted, err := s.grantEntitlement(ctx, entitlementGrant{
		ID:             id,
		At:             now,
		OrganisationID: workspace.OrganisationID,
		WorkspaceID:    &workspaceID,
		Source:         entitlementSourceOperations,
		Row:            row.Name,
		Seats:          asked.Seats,
		StartsAt:       now,
		EndsAt:         ends,
	})
	if err != nil {
		return WireEntitlement{}, err
	}
	return granted, nil
}

// operationsIntervalOK holds the check interval one mint names to F28's own
// bounds, and to the row's own connectedness.
//
// The interval **cannot reach the minted key**: neither the entitlement nor the
// binding's own request carries one (wire_entitlements.go, merchant.go's
// `paid`), and the row is what decides whether a key checks in at all
// (`entitlementRecord`'s `AirGapped`). What a request may therefore do is name
// an interval inside the span — which is the schema's own rule for the field —
// and never name one for a row whose key never checks the server, because that
// is the air-gapped row's and nothing else.
func operationsIntervalOK(row Policy, interval *string) error {
	if interval == nil {
		return nil
	}
	if row.CheckInterval == nil {
		return fmt.Errorf("%w: the %s row's key never contacts the server, so it carries no interval", errOperationsCheckInterval, row.Name)
	}
	if err := checkIntervalInBounds(*interval); err != nil {
		return fmt.Errorf("%w: %v", errOperationsCheckInterval, err)
	}
	return nil
}

// queueOperationsMint writes one request D454's rate held back, under the id
// the entitlement will have. The line is written before the record for the
// reason the whole area writes it before its act: a queue nobody is told about
// is a request that waits for an answer nobody made.
func (s *Service) queueOperationsMint(ctx context.Context, actor Caller, row Policy, workspace WireWorkspace, seats int, term time.Duration, now time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	if err := s.operationsAudit(ctx, actor, actionEntitlementQueued, &workspace.OrganisationID, &id, false, nil); err != nil {
		return err
	}
	doc, err := storedDoc(operationsQueued{
		ID:             id,
		OrganisationID: workspace.OrganisationID,
		WorkspaceID:    workspace.ID,
		Row:            row.Name,
		Tier:           row.Tier,
		Seats:          seats,
		Term:           term,
		QueuedAt:       now,
		ActorUID:       actor.UID,
		ActorEmail:     actor.Email,
	})
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, operationsQueueCollection, id, doc); err != nil {
		return fmt.Errorf("keyservice: the mint of the %s row was queued and the queue record could not be written: %w", row.Name, err)
	}
	return nil
}

// serveQueuedOperations serves what D454's rate held back, oldest first.
//
// It runs at the start of every mint attempt, which is the one thing that
// happens often enough to be the queue's own clock: nothing schedules a serve,
// the window opening is not an event this service is told about, and a queue
// served only when somebody looked at it would hold a customer's term until the
// next time operations minted anything at all.
//
// A record it cannot serve stays queued and the pass goes on, and the failure
// is logged rather than answered: refusing the new mint over an old record
// would let one unservable request stop every mint of its tier, and dropping
// the new request instead would be the cap answering a person with silence.
func (s *Service) serveQueuedOperations(ctx context.Context, now time.Time) {
	held, err := recordsWhere[operationsQueued](ctx, s.docs, operationsQueueCollection, Where{})
	if err != nil {
		log.Printf("keyservice: reading the operations queue: %v", err)
		return
	}
	sort.Slice(held, func(i, j int) bool {
		if held[i].QueuedAt.Equal(held[j].QueuedAt) {
			return held[i].ID < held[j].ID
		}
		return held[i].QueuedAt.Before(held[j].QueuedAt)
	})
	for _, queued := range held {
		within, err := s.operationsServeable(ctx, queued.Tier, now)
		if err != nil {
			log.Printf("keyservice: counting the operations mints of %s: %v", queued.Tier, err)
			return
		}
		if !within {
			// The window is still spent for this tier. A later record of
			// another tier is not, so the pass goes on rather than stopping.
			continue
		}
		if err := s.serveQueuedOperation(ctx, queued, now); err != nil {
			log.Printf("keyservice: serving the queued operations mint %s: %v", queued.ID, err)
		}
	}
}

// serveQueuedOperation writes the grant one queued request asked for and drops
// the queue record.
//
// The grant is keyed by the queue record's own id, so a serve that wrote the
// licence and then failed to drop the record writes nothing the second time: it
// finds the record the first serve wrote, writes no second line for it, and
// drops the queue record the first serve could not. The record is dropped in a
// transaction of its own rather than beside the grant, because the grant is
// `grantEntitlement`'s and that is a transaction this one may not be inside.
func (s *Service) serveQueuedOperation(ctx context.Context, queued operationsQueued, now time.Time) error {
	_, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, queued.ID)
	if err != nil {
		return err
	}
	if !present {
		actor := Caller{UID: queued.ActorUID, Email: queued.ActorEmail}
		if err := s.operationsAudit(ctx, actor, actionEntitlementMint, &queued.OrganisationID, &queued.ID, false, nil); err != nil {
			return err
		}
		workspaceID := queued.WorkspaceID
		if _, err := s.grantEntitlement(ctx, entitlementGrant{
			ID:             queued.ID,
			At:             now,
			OrganisationID: queued.OrganisationID,
			WorkspaceID:    &workspaceID,
			Source:         entitlementSourceOperations,
			Row:            queued.Row,
			Seats:          queued.Seats,
			StartsAt:       now,
			EndsAt:         now.Add(queued.Term),
		}); err != nil {
			return err
		}
	}
	return s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return tx.Delete(ctx, operationsQueueCollection, queued.ID)
	})
}

// operationsRoom answers whether one more operations mint of a tier fits inside
// D454's window, counting what is queued as well as what is granted: a cap that
// counted only the grants would let a burst queue without bound, and the queue
// is what the excess waits in rather than somewhere to put it.
//
// This is the **admission** question, asked of a request that is not in the
// queue yet, and it is not the serve one (operationsServeable): the two count
// different things, and the record being served must not be counted at all.
//
// The count is over the **operations grants** of the tier and not over the
// keys, where `admin.go`'s page counts its issuances: an operations mint is a
// term, the key follows from the customer binding the workspace (D2196), and
// the term is what this team hands out.
func (s *Service) operationsRoom(ctx context.Context, tier string, now time.Time) (bool, error) {
	limit, capped := s.cfg.MintCaps[tier]
	if !capped {
		return true, nil
	}
	granted, err := s.operationsGrantedIn(ctx, tier, now)
	if err != nil {
		return false, err
	}
	if granted >= limit.Requests {
		return false, nil
	}
	waiting, err := s.operationsWaiting(ctx, tier)
	if err != nil {
		return false, err
	}
	return granted+waiting < limit.Requests, nil
}

// operationsServeable answers whether D454's window has room to serve one more
// record the queue is holding, and it counts the **grants alone**.
//
// Counting the waiting list here is the wedge this exists to avoid. The serve
// pass runs on the mint path, so the queue is at its fullest exactly when the
// pass runs, and a burst leaves the queue holding as many records as the window
// takes: with the waiting records counted as spent room, a tier in that state
// reads its own queue as full, serves nothing, drains nothing — and every later
// mint is refused and joins the queue. The tier stops minting for good. A
// grant, unlike a waiting record, fills the window for real: `granted` counts
// the served ones as they land (`operationsGrantedIn` reads the licences
// themselves), which is what bounds one pass and lets the rest wait.
func (s *Service) operationsServeable(ctx context.Context, tier string, now time.Time) (bool, error) {
	limit, capped := s.cfg.MintCaps[tier]
	if !capped {
		return true, nil
	}
	granted, err := s.operationsGrantedIn(ctx, tier, now)
	if err != nil {
		return false, err
	}
	return granted < limit.Requests, nil
}

// operationsWaiting is how many requests of one tier the queue is holding.
func (s *Service) operationsWaiting(ctx context.Context, tier string) (int, error) {
	queued, err := recordsWhere[operationsQueued](ctx, s.docs, operationsQueueCollection, Where{Field: "tier", Value: tier})
	if err != nil {
		return 0, err
	}
	return len(queued), nil
}

// operationsGrantedIn is how many operations grants of one tier were written
// inside that tier's window, counted from the instant the window is read at.
func (s *Service) operationsGrantedIn(ctx context.Context, tier string, now time.Time) (int, error) {
	limit, capped := s.cfg.MintCaps[tier]
	if !capped {
		return 0, nil
	}
	held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection, Where{Field: "source", Value: entitlementSourceOperations})
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-limit.Window)
	used := 0
	for _, one := range held {
		if one.Tier == tier && one.CreatedAt.After(cutoff) {
			used++
		}
	}
	return used, nil
}

// operationsAct is one change to a term operations already granted: the four
// words wire_ops.go's shape carries.
func (s *Service) operationsAct(ctx context.Context, actor Caller, asked WireEntitlementAction) (WireEntitlement, error) {
	entitlement, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, asked.EntitlementID)
	if err != nil {
		return WireEntitlement{}, err
	}
	if !present {
		return WireEntitlement{}, errOperationsNoEntitlement
	}
	row, known := DefaultPolicy(entitlement.Row)
	if !known {
		return WireEntitlement{}, fmt.Errorf("keyservice: the licence %s names the row %q, which the policy table does not carry", entitlement.ID, entitlement.Row)
	}
	now := s.now().UTC()
	changed := entitlement
	var action string
	switch asked.Action {
	case operationsExtend:
		if asked.EndsAt == nil {
			return WireEntitlement{}, fmt.Errorf("%w: extend names the end the term moves to", errOperationsBadTerm)
		}
		ends := asked.EndsAt.UTC()
		// F30: the row's own term is the bound, counted from the term's start,
		// and a new end inside it may shorten the term as well as lengthen it.
		// An end that is not after now is refused: a licence whose term is over
		// is ended, and ending one is `revoke`'s act rather than an extension of
		// a term nobody is in.
		if !ends.After(now) || ends.After(entitlement.StartsAt.Add(row.Term)) {
			return WireEntitlement{}, fmt.Errorf("%w: the %s row runs from %s for at most %s", errOperationsBadTerm, row.Name, entitlement.StartsAt, row.Term)
		}
		changed.EndsAt = ends
		action = actionEntitlementExtend
	case operationsSeats:
		// F29: a licence that is not active is one no key is minted from again,
		// and the seat change is the one action of the four that mints. A revoked
		// licence is one §8's own withdrawal already reached, and a superseded one
		// is one a later grant in this workspace replaced; the reissue below would
		// mint the key the binding route refuses on the same status
		// (`bindEntitlement`), so neither is a licence a count is moved on. Which
		// term it is comes back under is a grant's or a renewal's question.
		if entitlement.Status != entitlementActive {
			return WireEntitlement{}, fmt.Errorf("%w: the licence is %q", errOperationsNotActive, entitlement.Status)
		}
		if asked.Seats == nil || *asked.Seats < 1 || (row.Seats != 0 && *asked.Seats != row.Seats) {
			return WireEntitlement{}, fmt.Errorf("%w: the %s row carries %d seat(s)", errOperationsBadSeats, row.Name, row.Seats)
		}
		changed.Seats = *asked.Seats
		action = actionEntitlementSeats
	case operationsComp:
		// A comp is a licence nobody pays for: the kind moves and nothing else
		// does, so the term, the seats and the buyer stay what they were.
		changed.Kind = licence.KindComped
		action = actionEntitlementComp
	case operationsRevoke:
		changed.Status = entitlementRevoked
		action = actionEntitlementRevoke
	default:
		return WireEntitlement{}, fmt.Errorf("%w: %q", errOperationsUnknownAction, asked.Action)
	}
	if err := s.operationsAudit(ctx, actor, action, &entitlement.OrganisationID, &entitlement.ID, false, asked.Reason); err != nil {
		return WireEntitlement{}, err
	}
	if asked.Action == operationsRevoke {
		// The keys first, for the reason the merchant's own revocation writes
		// them first: a store that fails on the licence's own write leaves keys
		// that are already dead and a licence a retry withdraws again — the walk
		// is idempotent — rather than a licence that is dead with its keys still
		// live, which is the gap F29 states.
		if err := s.withdrawEveryKeyOf(ctx, entitlement.ID, now); err != nil {
			return WireEntitlement{}, err
		}
		if err := s.revokeEntitlement(ctx, entitlement); err != nil {
			return WireEntitlement{}, err
		}
		return changed, nil
	}
	if err := s.saveEntitlement(ctx, changed); err != nil {
		return WireEntitlement{}, err
	}
	if asked.Action == operationsSeats {
		if err := s.reissueSeatsOf(ctx, changed, now); err != nil {
			return WireEntitlement{}, err
		}
	}
	return changed, nil
}

// reissueSeatsOf mints the successor a seat change is (D2200, §5.3).
//
// The count is a claim in the **key** and not in the term, so moving the
// licence's own record is not the change reaching the installation: the key in
// an installation's hand keeps the count it was minted with, and the renewal of
// a licence no subscription sold mints its successor from that key's own count
// (renew.go reads the record and not the licence), so a licence whose seats
// moved without a reissue would keep handing back the count the customer gave
// up. The merchant's own seat change mints here for the same reason
// (merchant.go's `seatChanged`), and this is that rule at a second door rather
// than a second rule: a new key under a new id, the licence's renewal secret
// kept (D2200), the key it supersedes left in the handover for `/renew` to hand
// the successor over with, and the term the licence has left — which for a
// connected row's key is D2202's own cap and never the whole licence.
//
// A licence no workspace has bound mints nothing: there is no key in anybody's
// hand and no installation to reach, and the count is read off the licence when
// the customer binds (D2196). A licence whose term is over mints nothing either,
// for `seatChanged`'s reason — the next term is a renewal's or a fresh grant's,
// and a key minted now would be expired before it arrived.
//
// No mail is sent. The merchant's reissue mails the buyer because the
// subscription carries an address and the change is the merchant's news; an
// operations seat change has no address to send to, and the handover is what
// makes it reach the installation — the successor answers the next `/renew` of
// the key the customer holds, whose secret D2200 leaves unspent.
func (s *Service) reissueSeatsOf(ctx context.Context, entitlement WireEntitlement, now time.Time) error {
	if entitlement.WorkspaceID == nil {
		return nil
	}
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, *entitlement.WorkspaceID)
	if err != nil {
		return err
	}
	if !present || workspace.ProjectID == nil {
		return nil
	}
	row, known := DefaultPolicy(entitlement.Row)
	if !known {
		return fmt.Errorf("keyservice: the licence %s names the row %q, which the policy table does not carry", entitlement.ID, entitlement.Row)
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return err
	}
	if _, minted := newestIssuance(records, entitlement.ID); !minted {
		// The licence has no key at all — the binding that mints one did not
		// finish, or the workspace was unbound again — and a reissue is the
		// successor of a key, not a first key. The count it now carries is what
		// the binding reads.
		return nil
	}
	days := daysBetween(now, entitlement.EndsAt)
	if days < 1 {
		// The term is over. The next one is a renewal's or a fresh grant's
		// (seatChanged's own reasoning), and a key minted into a term that has
		// ended would be expired before it arrived.
		return nil
	}
	subscription, err := s.licenceSubscription(ctx, entitlement.ID)
	if err != nil {
		return err
	}
	source, err := newID()
	if err != nil {
		return err
	}
	_, err = s.reissue(ctx, reissueRequest{
		Entitlement:  entitlement.ID,
		Request:      s.paidRequest(row.Name, *workspace.ProjectID, entitlement.OrganisationID, entitlement.Seats, days),
		Source:       operationsSeatSource + source,
		Subscription: subscription,
	})
	return err
}

// licenceKeys answers every key one licence ever minted (F29), which is the walk
// the withdrawal and a project transfer both take to them.
//
// Every record that names the licence is a key of it, whatever minted it: the
// binding's key, the one a seat change reissued, the one a renewal minted and
// the successor a rotation left. The walk below reaches the ones whose shape it
// can follow as well — the subscription the licence was sold under, and the
// rotations of every key it has collected — because a record the first step
// cannot see is still a key an installation may be running: a reissue's source
// names the event that caused it and not the key it replaced (D2200 keeps the
// secret, so nothing about that key is spent).
func (s *Service) licenceKeys(ctx context.Context, entitlementID string) ([]string, error) {
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	keys := []string{}
	note := func(keyID string) {
		if keyID == "" || seen[keyID] {
			return
		}
		seen[keyID] = true
		keys = append(keys, keyID)
	}
	bound, err := s.bindingKeys(ctx, entitlementID)
	if err != nil {
		return nil, err
	}
	for _, keyID := range bound {
		note(keyID)
	}
	for _, record := range entitlementRecords(records, entitlementID) {
		note(record.KeyID)
	}
	subscription, err := s.licenceSubscription(ctx, entitlementID)
	if err != nil {
		return nil, err
	}
	// The empty subscription is not a licence: an unsold key names none, so
	// matching on "" would withdraw every hand-minted key there is.
	if subscription != "" {
		for _, record := range records {
			if record.Subscription == subscription {
				note(record.KeyID)
			}
		}
	}
	successors := map[string][]string{}
	for _, record := range records {
		if from, rotation := strings.CutPrefix(record.Source, rotationPrefix); rotation {
			successors[from] = append(successors[from], record.KeyID)
		}
	}
	// The successors are followed from the keys noted so far, and a successor's
	// own rotations are followed in turn: a rotation chain is as long as the keys
	// the licence's term outlived, and the walk above reaches the first key of it
	// only.
	for i := 0; i < len(keys); i++ {
		for _, next := range successors[keys[i]] {
			note(next)
		}
	}
	return keys, nil
}

// withdrawEveryKeyOf withdraws every key one licence ever minted (F29), which is
// `licenceKeys`' walk written out as the store's own revocations.
//
// It is `merchant.go`'s walk read at a second door rather than a second rule:
// what a withdrawal is, is F29's, and the two callers must reach the same keys.
// The reason is the licensor's own word for it (store.go's Revocation) and
// never what reaches the holder.
func (s *Service) withdrawEveryKeyOf(ctx context.Context, entitlementID string, now time.Time) error {
	keys, err := s.licenceKeys(ctx, entitlementID)
	if err != nil {
		return err
	}
	for _, keyID := range keys {
		if err := s.store.Revoke(ctx, Revocation{KeyID: keyID, Reason: ReasonOperations, At: now}); err != nil {
			return err
		}
	}
	return nil
}

// saveEntitlement writes a licence's record as it now stands, for the three
// changes that move a field of it and leave the status alone.
func (s *Service) saveEntitlement(ctx context.Context, entitlement WireEntitlement) error {
	doc, err := storedDoc(entitlement)
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, entitlementsCollection, entitlement.ID, doc); err != nil {
		return fmt.Errorf("keyservice: the licence %s was changed and its record could not be written: %w", entitlement.ID, err)
	}
	return nil
}

// operationsTransfer moves one Firebase project id from the workspace that
// bound it to another (D2204, TN-P1), which is the remedy for a squatted
// binding and for a customer who set the wrong project up.
//
// What it moves is the id and the key: the workspace that loses it is left
// unbound — no project id, no binding instant — and every key its binding
// minted is withdrawn, because a key whose claim names a project the customer
// no longer holds is a live licence somewhere nobody is looking. Its licence
// stays what it is and becomes bindable again: the rebind window is opened at
// once, since the person whose project was taken is the one person D2198's
// thirty days must not hold back.
//
// The destination is a workspace that holds no project of its own, and the
// transfer does not bind it: binding is what mints the key and it is the
// destination's own owner who does it (D2196), through the probe of D2205. What
// the transfer leaves behind is the id, which is what makes that binding pass
// the one-project rule (D2204) rather than meet it as a collision.
func (s *Service) operationsTransfer(ctx context.Context, actor Caller, asked WireProjectTransfer) (WireWorkspace, error) {
	if !validProjectID(asked.ProjectID) {
		return WireWorkspace{}, errProjectIDShape
	}
	destination, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, asked.WorkspaceID)
	if err != nil {
		return WireWorkspace{}, err
	}
	if !present {
		return WireWorkspace{}, errOperationsNoWorkspace
	}
	if destination.ProjectID != nil && *destination.ProjectID != asked.ProjectID {
		return WireWorkspace{}, errOperationsDestinationBound
	}
	source, found, err := s.workspaceHolding(ctx, asked.ProjectID)
	if err != nil {
		return WireWorkspace{}, err
	}
	if !found {
		return WireWorkspace{}, errOperationsProjectUnbound
	}
	if source.ID == destination.ID {
		return WireWorkspace{}, errOperationsSameWorkspace
	}
	now := s.now().UTC()
	var replacing []string
	if source.EntitlementID != nil {
		// Every key the licence minted, which is the walk a revocation takes and
		// not the binding's own keys alone (F29): the key an installation is
		// running may be a seat change's, a renewal's or a rotation's successor,
		// and each of those names the project this transfer is taking away.
		replacing, err = s.licenceKeys(ctx, *source.EntitlementID)
		if err != nil {
			return WireWorkspace{}, err
		}
	}
	if err := s.operationsAudit(ctx, actor, actionProjectTransfer, &destination.OrganisationID, &asked.ProjectID, false, asked.Reason); err != nil {
		return WireWorkspace{}, err
	}
	project := asked.ProjectID
	bound := now
	var answered WireWorkspace
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		heldSource, present, err := getRecord[WireWorkspace](ctx, tx, workspacesCollection, source.ID)
		if err != nil {
			return err
		}
		if !present {
			return errOperationsProjectUnbound
		}
		held, present, err := getRecord[WireWorkspace](ctx, tx, workspacesCollection, destination.ID)
		if err != nil {
			return err
		}
		if !present {
			return errOperationsNoWorkspace
		}
		// The state this transfer was decided on, re-read: a binding that
		// landed in the window between the reads above and this write is the
		// one thing that would otherwise withdraw the keys of a binding that
		// has since moved, or move an id somebody else has already moved.
		if heldSource.ProjectID == nil || *heldSource.ProjectID != asked.ProjectID {
			return errOperationsProjectUnbound
		}
		if held.ProjectID != nil && *held.ProjectID != asked.ProjectID {
			return errOperationsDestinationBound
		}
		// The licence is read here, with the two workspaces and before anything
		// is written: the deploy's store refuses a read after a write in one
		// transaction (docstore's ErrReadAfterWrite), and the withdrawal below is
		// the write.
		var licence WireEntitlement
		licenceHeld := false
		if heldSource.EntitlementID != nil {
			licence, licenceHeld, err = getRecord[WireEntitlement](ctx, tx, entitlementsCollection, *heldSource.EntitlementID)
			if err != nil {
				return err
			}
		}
		if err := s.revokeProjectKeys(ctx, tx, replacing, now); err != nil {
			return err
		}
		heldSource.ProjectID = nil
		heldSource.BoundAt = nil
		if err := putRecord(ctx, tx, workspacesCollection, heldSource.ID, heldSource); err != nil {
			return err
		}
		if licenceHeld {
			// D2198's clock starts with a binding, and this is the one place a
			// licence is left bindable without one: the workspace whose project
			// was taken is the person D2198's window must not hold back.
			licence.RebindableAt = &bound
			if err := putRecord(ctx, tx, entitlementsCollection, licence.ID, licence); err != nil {
				return err
			}
		}
		held.ProjectID = &project
		if err := putRecord(ctx, tx, workspacesCollection, held.ID, held); err != nil {
			return err
		}
		answered = held
		return nil
	})
	if err != nil {
		return WireWorkspace{}, err
	}
	return answered, nil
}

// revokeProjectKeys withdraws the keys of the licence whose project id the
// workspace held, through the transaction that moves that id (D2204).
//
// The store's own Revoke would open a transaction of its own, which the backend
// refuses inside this one (workspaces.go's revokeReplacedBindingKeys says the
// same about a rebind) — and a withdrawal that committed while the transfer
// that made it did not is the half of it that must not land alone.
func (s *Service) revokeProjectKeys(ctx context.Context, tx Tx, keyIDs []string, at time.Time) error {
	if len(keyIDs) == 0 {
		return nil
	}
	revocations := make([]Revocation, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		revocations = append(revocations, Revocation{KeyID: keyID, Reason: ReasonOperations, At: at})
	}
	return tx.Revoke(ctx, revocations)
}

// workspaceHolding answers the workspace a Firebase project id is bound to,
// across every organisation: D2204's one project, one installation, read from
// the other side.
func (s *Service) workspaceHolding(ctx context.Context, projectID string) (WireWorkspace, bool, error) {
	held, err := recordsWhere[WireWorkspace](ctx, s.docs, workspacesCollection, Where{Field: "project_id", Value: projectID})
	if err != nil {
		return WireWorkspace{}, false, err
	}
	// The store answers a query in no order, and there can be one workspace per
	// project id: a deterministic pick is what keeps two reads of a store that
	// somehow holds two answering the same one.
	sort.Slice(held, func(i, j int) bool { return held[i].ID < held[j].ID })
	if len(held) == 0 {
		return WireWorkspace{}, false, nil
	}
	return held[0], true, nil
}

// operationsCutHandover closes the window a key's reissue can still be fetched
// in, which is the one thing operations may do about a secret (F5): the answer
// a rotation left is taken away rather than handed over.
//
// The window is closed by moving its own end to this instant — the record is
// the store's (store.go's Handover), and a handover is answered only while the
// instant it is read at is before that end. What the store holds of the
// successor's sealed bytes is dropped by the store's own sweep, on the next
// save or read of a handover, and no route of this service reads them out.
func (s *Service) operationsCutHandover(ctx context.Context, actor Caller, keyID string) error {
	now := s.now().UTC()
	held, present, err := s.store.Handover(ctx, keyID, now)
	if err != nil {
		return err
	}
	if !present {
		return errOperationsNoHandover
	}
	if err := s.operationsAudit(ctx, actor, actionKeyHandoverCut, nil, &keyID, false, nil); err != nil {
		return err
	}
	held.Until = now
	return s.store.SaveHandover(ctx, held, now)
}

// operationsLists answers the two lists the operations owner set. A deployment
// that has set none answers two empty lists rather than an absence: an empty
// list is a real answer and not a missing one (F40), because it refuses every
// sign-in it would have admitted.
func (s *Service) operationsLists(ctx context.Context, organisationID string) (WireOpsLists, error) {
	held, present, err := getRecord[WireOpsLists](ctx, s.docs, s.cfg.TenantsCollection, organisationID)
	if err != nil {
		return WireOpsLists{}, err
	}
	if !present {
		return WireOpsLists{EmailDomains: []string{}, MicrosoftDirectories: []string{}}, nil
	}
	return emptyListsFor(held), nil
}

// setOperationsLists writes both lists as one record, under the organisation
// whose door they configure: the operations organisation's own id, which is
// what the sign-in rule reads them by (invites.go's invitationDomains).
//
// A domain the schema will not take is refused here rather than trimmed into
// one: the two spellings differ, and a list that quietly lower-cased what the
// owner wrote would be a list nobody could check against the screen it was
// typed on. A directory is a non-empty string and nothing else is known about
// it here — the value compared is the `tid` of a verified token.
func (s *Service) setOperationsLists(ctx context.Context, actor Caller, organisationID string, asked WireOpsLists) (WireOpsLists, error) {
	domains := make([]string, 0, len(asked.EmailDomains))
	for _, held := range asked.EmailDomains {
		domain := strings.TrimSpace(held)
		if !validOperationsDomain(domain) {
			return WireOpsLists{}, fmt.Errorf("%w: %q is not a domain", errOperationsBadList, held)
		}
		domains = append(domains, domain)
	}
	directories := make([]string, 0, len(asked.MicrosoftDirectories))
	for _, held := range asked.MicrosoftDirectories {
		directory := strings.TrimSpace(held)
		if directory == "" {
			return WireOpsLists{}, fmt.Errorf("%w: a directory is an identifier and not an empty string", errOperationsBadList)
		}
		directories = append(directories, directory)
	}
	written := WireOpsLists{EmailDomains: domains, MicrosoftDirectories: directories}
	if err := s.operationsAudit(ctx, actor, actionOperationsListsSet, &organisationID, nil, false, nil); err != nil {
		return WireOpsLists{}, err
	}
	doc, err := storedDoc(written)
	if err != nil {
		return WireOpsLists{}, err
	}
	if err := s.docs.Put(ctx, s.cfg.TenantsCollection, organisationID, doc); err != nil {
		return WireOpsLists{}, fmt.Errorf("keyservice: the operations lists could not be written: %w", err)
	}
	return written, nil
}

// operationsDomain is the domain list's own pattern (`ops.schema.json`). It is
// the schema's and written again here because a stored list is read by the
// sign-in rule: a value the screen admits and this does not would be a domain
// in a record that refuses a sign-in nobody could explain, and the two
// spellings of the rule are one rule and have to be the same one.
var operationsDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// validOperationsDomain answers whether one entry of the domain list is a
// domain. The label the pattern's length is checked against is a name's, which
// is longer than any label may be — a domain a person types is bounded by what
// a log line may hold, and a stricter per-label rule would be this service
// inventing one the schema does not state.
func validOperationsDomain(domain string) bool {
	return len(domain) <= maxOrganisationName && operationsDomain.MatchString(domain)
}

// emptyListsFor answers a stored record with both lists spelled as lists: a
// record written by an older writer, or by hand, would otherwise answer a null
// where the shape says an array, and a screen draws it as one.
func emptyListsFor(held WireOpsLists) WireOpsLists {
	if held.EmailDomains == nil {
		held.EmailDomains = []string{}
	}
	if held.MicrosoftDirectories == nil {
		held.MicrosoftDirectories = []string{}
	}
	return held
}

// operationsParkedEvents answers the parked events, oldest first: the queue
// whose items have a shape (F20), which is what the screen decides and the
// comment on `api.ts` says the applications queue has not got.
func (s *Service) operationsParkedEvents(ctx context.Context, state string) ([]WireLedgerEvent, error) {
	if state != ledgerParked {
		return nil, errOperationsBadState
	}
	held, err := s.eventsReceived(ctx)
	if err != nil {
		return nil, err
	}
	parked := make([]WireLedgerEvent, 0, len(held))
	for _, event := range held {
		if event.State == ledgerParked {
			parked = append(parked, event)
		}
	}
	return parked, nil
}

// resolveOperationsEvent attaches one parked event to a licence or dismisses
// it. Both end the event and neither edits the body: what Paddle signed is the
// record of a sale (D457), and the state beside it is what this service makes
// of one.
//
// An attach answers the event as `processed`, which is what an event the API
// placed itself is: the licence it is attached to is what the money bought, and
// a second state for one fact would be a reader of the ledger with a case to
// add. A dismissal answers `dismissed`, which is its own state and not a
// processed event — nothing was resolved and the money is somebody else's.
func (s *Service) resolveOperationsEvent(ctx context.Context, actor Caller, asked WireEventResolution) (WireLedgerEvent, error) {
	held, present, err := s.storedEvent(ctx, asked.ID)
	if err != nil {
		return WireLedgerEvent{}, err
	}
	if !present {
		return WireLedgerEvent{}, errOperationsNoEvent
	}
	if held.State != ledgerParked {
		return WireLedgerEvent{}, errOperationsEventAnswered
	}
	var state, action string
	var target, organisation *string
	switch asked.Decision {
	case operationsAttach:
		if asked.EntitlementID == nil || strings.TrimSpace(*asked.EntitlementID) == "" {
			return WireLedgerEvent{}, fmt.Errorf("%w: an attach names the licence the event is attached to", errOperationsBadDecision)
		}
		entitlement, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, *asked.EntitlementID)
		if err != nil {
			return WireLedgerEvent{}, err
		}
		if !present {
			return WireLedgerEvent{}, errOperationsNoEntitlement
		}
		state = ledgerProcessed
		action = actionLedgerEventAttach
		target = named(entitlement.ID)
		organisation = named(entitlement.OrganisationID)
	case operationsDismiss:
		if asked.EntitlementID != nil {
			return WireLedgerEvent{}, fmt.Errorf("%w: a dismissal attaches nothing and names no licence", errOperationsBadDecision)
		}
		state = ledgerDismissed
		action = actionLedgerEventDismiss
	default:
		return WireLedgerEvent{}, fmt.Errorf("%w: %q", errOperationsBadDecision, asked.Decision)
	}
	if err := s.operationsAudit(ctx, actor, action, organisation, &held.ID, false, nil); err != nil {
		return WireLedgerEvent{}, err
	}
	if err := s.markEvent(ctx, held.ID, state, target); err != nil {
		return WireLedgerEvent{}, err
	}
	held.State = state
	if target != nil {
		held.EntitlementID = target
	}
	return held, nil
}

// operationsSearchOrganisations answers the organisations whose name holds the
// term, and the one whose id is it. It is the lookup the screens start every
// investigation from, so it reads across every organisation by construction:
// the operations membership is what makes the whole portal one list (D2188).
//
// A term of nothing is refused rather than answered with every customer: the
// list would be the whole tenancy, and a lookup nobody named is not a read
// anybody asked for.
func (s *Service) operationsSearchOrganisations(ctx context.Context, term string) ([]WireOrganisation, error) {
	term = strings.TrimSpace(term)
	if term == "" || len(term) > maxOperationsLookupTerm {
		return nil, errOperationsBadQuery
	}
	held, err := recordsWhere[WireOrganisation](ctx, s.docs, organisationsCollection, Where{})
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(term)
	found := make([]WireOrganisation, 0, len(held))
	for _, org := range held {
		if org.ID == term || strings.Contains(strings.ToLower(org.Name), needle) {
			found = append(found, org)
		}
	}
	sort.Slice(found, func(i, j int) bool {
		left, right := strings.ToLower(found[i].Name), strings.ToLower(found[j].Name)
		if left == right {
			return found[i].ID < found[j].ID
		}
		return left < right
	})
	return found, nil
}

// operationsOrganisation reads the organisation one lookup names, and refuses
// one the store does not hold: a list of nothing under a heading that says an
// organisation is one an operator reads as an organisation with nothing in it.
func (s *Service) operationsOrganisation(ctx context.Context, organisationID string) (WireOrganisation, error) {
	org, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, organisationID)
	if err != nil {
		return WireOrganisation{}, err
	}
	if !present {
		return WireOrganisation{}, errNoSuchOrganisation
	}
	return org, nil
}

// operationsWorkspaces answers one organisation's workspaces, oldest first.
func (s *Service) operationsWorkspaces(ctx context.Context, organisationID string) ([]WireWorkspace, error) {
	org, err := s.operationsOrganisation(ctx, organisationID)
	if err != nil {
		return nil, err
	}
	held, err := recordsWhere[WireWorkspace](ctx, s.docs, workspacesCollection, Where{Field: "organisation_id", Value: org.ID})
	if err != nil {
		return nil, err
	}
	oldestFirst(held, func(one WireWorkspace) time.Time { return one.CreatedAt }, func(one WireWorkspace) string { return one.ID })
	return held, nil
}

// operationsEntitlements answers one organisation's grants, oldest first, with
// the ended and superseded ones in it: a licence a later grant replaced is
// still the licence a key the customer holds was minted from.
func (s *Service) operationsEntitlements(ctx context.Context, organisationID string) ([]WireEntitlement, error) {
	org, err := s.operationsOrganisation(ctx, organisationID)
	if err != nil {
		return nil, err
	}
	held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection, Where{Field: "organisation_id", Value: org.ID})
	if err != nil {
		return nil, err
	}
	oldestFirst(held, func(one WireEntitlement) time.Time { return one.CreatedAt }, func(one WireEntitlement) string { return one.ID })
	return held, nil
}

// operationsMembers answers one organisation's members, oldest first.
func (s *Service) operationsMembers(ctx context.Context, organisationID string) ([]WireMembership, error) {
	org, err := s.operationsOrganisation(ctx, organisationID)
	if err != nil {
		return nil, err
	}
	held, err := membersOf(ctx, s.docs, org.ID)
	if err != nil {
		return nil, err
	}
	oldestFirst(held, func(one WireMembership) time.Time { return one.CreatedAt }, func(one WireMembership) string { return one.UID })
	return held, nil
}

// operationsKeys answers the metadata of every key one workspace's licences
// minted, oldest first, and nothing about a key that is not a record: F5's
// metadata is what the issuance holds, and the key itself is not in it.
//
// The workspace a key's own claim names is the Firebase project id (TN2), which
// is the workspace's and not the record's — an issuance keeps the organisation
// and the subscription, and the project id is the binding's. A workspace whose
// project has been transferred away has keys and no project id, and those keys
// are the withdrawn ones: they are answered with an empty project id rather
// than left out, because a key the customer still holds being invisible in the
// list is the state the list exists to prevent.
func (s *Service) operationsKeys(ctx context.Context, workspaceID string) ([]WireKeyMetadata, error) {
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, workspaceID)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errOperationsNoWorkspace
	}
	entitlements, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection, Where{Field: "workspace_id", Value: workspace.ID})
	if err != nil {
		return nil, err
	}
	issued, err := s.store.Issuances(ctx)
	if err != nil {
		return nil, err
	}
	project := ""
	if workspace.ProjectID != nil {
		project = *workspace.ProjectID
	}
	keys := make([]WireKeyMetadata, 0, len(entitlements))
	for _, entitlement := range entitlements {
		for _, record := range entitlementRecords(issued, entitlement.ID) {
			metadata := WireKeyMetadata{
				KeyID:         record.KeyID,
				EntitlementID: entitlement.ID,
				WorkspaceID:   project,
				Tier:          record.Tier,
				Kind:          entitlement.Kind,
				Seats:         entitlement.Seats,
				IssuedAt:      record.Issued,
				PaidThrough:   record.Expires,
			}
			state, held, err := s.store.KeyState(ctx, record.KeyID)
			if err != nil {
				return nil, err
			}
			if held && !state.LastSeen.IsZero() {
				seen := state.LastSeen.UTC()
				metadata.LastSeen = &seen
			}
			keys = append(keys, metadata)
		}
	}
	oldestFirst(keys, func(one WireKeyMetadata) time.Time { return one.IssuedAt }, func(one WireKeyMetadata) string { return one.KeyID })
	return keys, nil
}

// operationsLedger answers the settled lines of one UTC month, oldest first: a
// month is UTC because the reports are (P5.2), and a month read in the
// browser's own zone would be a different month from the report beside it.
func (s *Service) operationsLedger(ctx context.Context, month string) ([]WireLedgerEntry, error) {
	at, err := time.Parse("2006-01", strings.TrimSpace(month))
	if err != nil {
		return nil, fmt.Errorf("%w: a month is `YYYY-MM` and %q is not one", errOperationsBadQuery, month)
	}
	held, err := s.ledgerEntries(ctx)
	if err != nil {
		return nil, err
	}
	lines := make([]WireLedgerEntry, 0, len(held))
	for _, entry := range held {
		if entry.At.UTC().Year() == at.Year() && entry.At.UTC().Month() == at.Month() {
			lines = append(lines, entry)
		}
	}
	oldestFirst(lines, func(one WireLedgerEntry) time.Time { return one.At }, func(one WireLedgerEntry) string { return one.ID })
	return lines, nil
}

// operationsAudit answers the newest page of the log, newest first, which is
// the order a log is read in and the one thing this read decides: the log only
// grows, the shape has no cursor, and the answer says how much of it a reader
// gets (maxOperationsAuditEntries).
func (s *Service) operationsAuditLog(ctx context.Context) ([]WireAuditEntry, error) {
	held, err := recordsWhere[WireAuditEntry](ctx, s.docs, auditCollection, Where{})
	if err != nil {
		return nil, err
	}
	sort.Slice(held, func(i, j int) bool {
		if held[i].At.Equal(held[j].At) {
			return held[i].ID > held[j].ID
		}
		return held[i].At.After(held[j].At)
	})
	if len(held) > maxOperationsAuditEntries {
		held = held[:maxOperationsAuditEntries]
	}
	return held, nil
}
