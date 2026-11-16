// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The renewal and binding reminders (portal plan P7.1): what the daily run
// mails, and the record that makes it once per term.
//
// **The run is the API's and not a job's.** Cloud Scheduler posts to the
// reminder route once a day (P1.5's scheduler.tf, created paused and turned on
// by the deploy that ships the route), and everything the run decides is here.
// Nothing below mints, revokes or moves a term: a reminder is a mail and a
// record of having sent it, and a run that mailed nothing changed nothing.
//
// **A key that nothing renews runs out on a day, and its owners are told before
// it does.** What has to be told apart is which grants something takes on. A
// purchase renews at the merchant of record (P4.1, P4.2) and the free seat
// renews while the organisation lives (D2184, D2203; renewal follows the
// entitlement, F27's resolution), so neither is a licence anybody has to act on
// — while a trial, an operations grant, a reseller's sale and an open-source
// approval are terms somebody handed out once, and the installation running on
// one of them stops when its term does.
//
// A purchase whose Paddle subscription has been cancelled is the one case this
// file cannot see: the merchant stops renewing it, and the portal holds no
// mirror of the subscription to read that from — the entitlement names the grant
// and not the subscription behind it. P4.3's subscription state is what would
// answer it, and until it lands a cancelled purchase runs out with no reminder.
//
// **A paid entitlement nobody bound is a term running down unused.** The term
// runs from payment and not from the binding (D2197), so a customer who pays and
// sets their project up a month later holds the month they paid for — and one
// who never sets it up holds nothing at all. That is the second reminder, and it
// is the only one an entitlement with no key can be sent.
//
// **A reminder is sent once per term, and the store is what makes it so.** The
// run writes a record named by the entitlement, the reminder and the end of the
// term it was about, and a run that finds that record mails nothing: the
// schedule is Cloud Scheduler's, but nothing here depends on it firing once, on
// it firing at all, or on it firing in the order the terms end.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// remindersCollection holds one document per reminder that was sent. It is this
// area's own name and not the deploy's, for the reason the tenancy's two are
// (orgs.go).
const remindersCollection = "reminders"

// The two reminders, spelled as the record's own vocabulary. What the two are
// about is the whole of the difference between them, and the record says which
// one it was so that a person reading the collection can tell a customer who was
// told their term was ending from one who was told to bind.
const (
	reminderRenewal = "renewal"
	reminderBinding = "binding"
)

// reminderLead is how early the renewal reminder is mailed. It is the policy's
// own lead (policy.go's renewalLeadDays), which is how early a key the renewal
// job serves starts asking for its successor: a term nothing renews is the same
// window from the other side, and the owner has the same week to buy the next
// one. It is also what keeps a fourteen-day trial from being reminded the day it
// is granted.
const reminderLead = 7 * 24 * time.Hour

// routeWorkspaces is the portal page a reminder leads to: the screen that holds
// the workspaces, which is where a licence is bound and where the next term of
// one is bought. The applications' detail pages are that screen's own business,
// and a mail that deep-linked into one would be a link this file has to keep
// true as the SPA changes.
const routeWorkspaces = "/workspaces"

// reminderRun is what one run did, which is what its answer's log line says and
// nothing else: the counts are per reminder and not per mail, because an
// organisation with three owners was told once.
type reminderRun struct {
	Renewals int
	Bindings int
}

// reminderRecord is one reminder that was sent: what it was about, and when it
// went out. It holds no address and no key (§7.3's rule at this collection): the
// recipients are the organisation's owners as the run reads them, and a record
// that kept them would be a list of addresses the store has no use for.
type reminderRecord struct {
	ID             string `json:"id"`
	EntitlementID  string `json:"entitlement_id"`
	OrganisationID string `json:"organisation_id"`
	Kind           string `json:"kind"`
	// TermEndsAt is the end of the term the reminder was about, and it is part of
	// the record's own name (`reminderID`): a term that was extended or replaced
	// is a term nobody has been told about yet, and the record of the one that
	// ended says nothing about it.
	TermEndsAt time.Time `json:"term_ends_at"`
	SentAt     time.Time `json:"sent_at"`
}

// reminderID is the name one reminder is filed under. Both halves of the term
// and the reminder are in it because they are what "once per term" is about, and
// the id is derived rather than minted so that the run reads one document
// instead of scanning the collection for a reminder it may already have sent.
//
// Neither an entitlement's id nor the two words can hold the separator
// (`newID`'s alphabet), so the three parts cannot be confused with each other.
func reminderID(entitlementID, kind string, termEndsAt time.Time) string {
	return entitlementID + ":" + kind + ":" + strconv.FormatInt(termEndsAt.Unix(), 10)
}

// runsOut answers whether this grant's term is the end of it: whether anything
// takes it on when the term is over.
//
// A purchase is the one grant the merchant of record stands behind (P4.1,
// P4.2): Paddle renews the subscription and the key is reissued without the
// customer doing anything, so its term is not a date anybody has to act on.
//
// The free seat is the one grant that renews without a merchant and without a
// project: D2184 gives it for as long as the organisation exists, and D2203 ends
// it only when a paid grant supersedes it in the organisation — a superseded
// seat is not an active entitlement and never reaches this function. A reminder
// about it would be about a licence that is not going anywhere, mailed to
// somebody who is not paying for it.
//
// Everything else — a trial, an operations grant, a reseller's sale, an
// open-source approval — is a term handed out once, and the run is what tells
// its holders their installation is about to stop.
func runsOut(entitlement WireEntitlement) bool {
	switch entitlement.Source {
	case entitlementSourcePurchase, entitlementSourceFreeSeat:
		return false
	}
	return true
}

// reminderDue answers which reminder, if any, one entitlement is due at this
// instant, and it is the whole of what the run decides.
//
// A paid grant nobody bound comes first: it is the state where the customer has
// already lost something (D2197 runs the term from payment), and the mail that
// tells them to bind is the useful one — the renewal reminder would be about a
// term they have not used at all. An unbound grant that is not paid is nobody's
// loss: a trial or an operations grant that was never bound licensed nothing.
//
// A renewal reminder is for a key that exists, which is a BOUND entitlement and
// nothing else: "a key runs out" is about the key an installation is running on,
// and an entitlement with no key has no installation to warn. What is left is
// the term's own end, and it is due from the moment it is inside the lead and
// stays due afterwards — a term that ended while the run was failing is one its
// owners still have to hear about.
func reminderDue(entitlement WireEntitlement, now time.Time) (string, bool) {
	if entitlement.Status != entitlementActive {
		return "", false
	}
	if entitlement.Kind == licence.KindPaid && entitlement.BoundAt == nil {
		return reminderBinding, true
	}
	if entitlement.BoundAt == nil || !runsOut(entitlement) {
		return "", false
	}
	if entitlement.EndsAt.After(now.Add(reminderLead)) {
		return "", false
	}
	return reminderRenewal, true
}

// runReminders mails every reminder that is due and answers what it sent. The
// order is the entitlements' own (oldest grant first), so two runs over one
// store mail in one order, and it stops at the first failure rather than
// carrying on: a transport that could not deliver is the run's failure (F33's
// retry is what asks it again), and a run that swallowed one would leave a
// customer who was never told with a service that believes they were.
func (s *Service) runReminders(ctx context.Context) (reminderRun, error) {
	origin, err := portalOrigin(s.cfg.URL)
	if err != nil {
		return reminderRun{}, err
	}
	held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection, Where{})
	if err != nil {
		return reminderRun{}, err
	}
	oldestFirst(held, func(one WireEntitlement) time.Time { return one.CreatedAt }, func(one WireEntitlement) string { return one.ID })
	var run reminderRun
	for _, entitlement := range held {
		kind, due := reminderDue(entitlement, s.now())
		if !due {
			continue
		}
		sent, err := s.remind(ctx, origin, entitlement, kind)
		if err != nil {
			return run, err
		}
		if !sent {
			continue
		}
		if kind == reminderRenewal {
			run.Renewals++
		} else {
			run.Bindings++
		}
	}
	return run, nil
}

// remind sends one entitlement's reminder to its organisation's owners and
// answers whether it mailed anybody.
//
// **The record is written after the mails, and that is a decision.** A run that
// failed halfway through an organisation's owners leaves no record, so the retry
// mails the ones who were already told — which is the failure worth having: the
// other order records a reminder nobody received, and nothing would ever ask the
// service about it again. The window is one account's owner list and one
// transport call long, and Cloud Scheduler retries a run that answered a failure
// rather than running one beside it.
//
// An organisation with no owner is nobody to tell and nothing is recorded, so an
// organisation that gains one is reminded at the next run rather than never.
func (s *Service) remind(ctx context.Context, origin string, entitlement WireEntitlement, kind string) (bool, error) {
	id := reminderID(entitlement.ID, kind, entitlement.EndsAt)
	_, recorded, err := getRecord[reminderRecord](ctx, s.docs, remindersCollection, id)
	if err != nil {
		return false, err
	}
	if recorded {
		return false, nil
	}
	organisation, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, entitlement.OrganisationID)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	owners, err := reminderOwners(ctx, s.docs, organisation.ID)
	if err != nil {
		return false, err
	}
	if len(owners) == 0 {
		return false, nil
	}
	for _, owner := range owners {
		if err := s.mail.Send(ctx, reminderMail(kind, owner.Email, organisation.Name, entitlement, origin)); err != nil {
			return false, err
		}
	}
	record := reminderRecord{
		ID:             id,
		EntitlementID:  entitlement.ID,
		OrganisationID: organisation.ID,
		Kind:           kind,
		TermEndsAt:     entitlement.EndsAt.UTC(),
		SentAt:         s.now().UTC(),
	}
	doc, err := storedDoc(record)
	if err != nil {
		return false, err
	}
	if err := s.docs.Put(ctx, remindersCollection, id, doc); err != nil {
		return false, err
	}
	return true, nil
}

// reminderOwners is the organisation's owners, in the order its memberships are
// filed in. The plan's word is owners: an admin may bind and buy as well
// (D2186), and the reminder is the owner's — they are the one who paid, and the
// mail that tells somebody what to do with an organisation's licence is one
// owner per organisation and not one per person who can act.
func reminderOwners(ctx context.Context, from recordReader, organisationID string) ([]WireMembership, error) {
	held, err := membersOf(ctx, from, organisationID)
	if err != nil {
		return nil, err
	}
	owners := make([]WireMembership, 0, len(held))
	for _, membership := range held {
		if membership.Role == roleOwner && strings.TrimSpace(membership.Email) != "" {
			owners = append(owners, membership)
		}
	}
	return owners, nil
}

// The two subjects are constants, so an organisation's name — text a person
// typed — cannot reach a header through either (§7.2's rule, read at this mail
// path as the invitations read it).
const (
	renewalReminderSubject = "Your Fugaro licence ends soon"
	bindingReminderSubject = "Your Fugaro licence is not set up yet"
)

// reminderMail is one reminder as the person receives it: what the organisation
// holds, when the term ends, and the link to the screen where it is acted on.
// The link is the whole of what the mail carries — never a key, never a secret
// (D2182, F3), and the run has neither to hand: it reads entitlements, and a key
// is minted at binding and never stored (§7.3). The organisation's name is folded
// onto one line before it is written, because a name is text an owner typed and
// a mail line is one line.
func reminderMail(kind, to, organisation string, entitlement WireEntitlement, origin string) Message {
	name := strings.Join(strings.Fields(organisation), " ")
	until := entitlement.EndsAt.UTC().Format("2 January 2006")
	link := strings.TrimSuffix(origin, "/") + routeWorkspaces
	if kind == reminderBinding {
		return Message{
			To:      to,
			Subject: bindingReminderSubject,
			Body: "The Fugaro licence held by " + name + " is paid for and not bound to a project yet.\n\n" +
				"Its term runs from the day it was paid for and ends on " + until + ", so every day it is unbound is a day of it nobody uses.\n" +
				"Bind it to your Firebase project in the portal:\n\n" + link + "\n",
		}
	}
	return Message{
		To:      to,
		Subject: renewalReminderSubject,
		Body: "The Fugaro licence held by " + name + " ends on " + until + ".\n\n" +
			"Nothing renews this one, so the key it issued is the last that licence issues.\n" +
			"Buy the next term in the portal:\n\n" + link + "\n",
	}
}
