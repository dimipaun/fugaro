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
// **A reminder is sent once per term to each owner, and the store is what makes
// it so.** The run writes a record for each owner it told, named by the
// entitlement, the reminder, the end of the term it was about and that owner,
// and a run that finds that record mails nothing to them: the schedule is Cloud
// Scheduler's, but nothing here depends on it firing once, on it firing at all,
// or on it firing in the order the terms end.
//
// **A reminder that could not be delivered stops nothing else.** One owner's
// address is that owner's failure, and the run carries on with the owners beside
// them and with the next organisation, joining what failed into its answer so
// that the job's retry is asked again about exactly what is missing.

import (
	"context"
	"errors"
	"fmt"
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
//
// It closes the window at the end as well (reminderDue): a term that ended
// longer ago than this is not mailed about at all. Nothing here marks an
// entitlement expired (entitlements.go leaves the status where the term left it),
// so without the bound the first run of a newly turned-on job — or the first run
// after an owner joins — would tell somebody their licence "ends soon" and give
// them a date years in the past, for every term the portal has ever held.
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

// reminderRecord is one reminder that was sent to one owner: what it was about,
// who was told, and when it went out. The recipient is the membership's own uid
// and never the address it was mailed to (§7.3's rule at this collection): the
// address is read from the membership each run, and a record that kept it would
// be a list of addresses the store has no use for.
type reminderRecord struct {
	ID             string `json:"id"`
	EntitlementID  string `json:"entitlement_id"`
	OrganisationID string `json:"organisation_id"`
	Recipient      string `json:"recipient"`
	Kind           string `json:"kind"`
	// TermEndsAt is the end of the term the reminder was about, and it is part of
	// the record's own name (`reminderID`): a term that was extended or replaced
	// is a term nobody has been told about yet, and the record of the one that
	// ended says nothing about it.
	TermEndsAt time.Time `json:"term_ends_at"`
	SentAt     time.Time `json:"sent_at"`
}

// reminderID is the name one reminder is filed under. The term and the reminder
// are in it because they are what "once per term" is about, and so is the
// recipient because one owner's address is refused where another's is not
// (remind): a record per owner is what lets a run that mailed them hold off
// while it keeps asking the provider about the owner it could not reach. The id
// is derived rather than minted so that the run reads one document instead of
// scanning the collection for a reminder it may already have sent.
//
// An entitlement's id is `newID`'s alphabet, the kind is one of two words, the
// term ends at a whole number of seconds, and the recipient is the uid
// membershipID already joins to an organisation's id: none of the four can hold
// the separator.
func reminderID(entitlementID, kind string, termEndsAt time.Time, recipient string) string {
	return entitlementID + ":" + kind + ":" + strconv.FormatInt(termEndsAt.Unix(), 10) + ":" + recipient
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
// the term's own end, and it is due from the moment it is inside the lead.
//
// **One lead past the end, the reminder stops being due, and that bound covers
// both kinds.** A term that ended while a run was failing is one its owners
// still have to hear about, which is what the lead's other side is for; but a
// term that ended a year ago is one nobody is waiting to be told about, and
// nothing marks those entitlements expired (entitlements.go: a status moves when
// a grant or a revocation moves it, not when a clock does). Without the bound,
// every term the portal has ever granted is reminded about again when the job is
// first turned on, and again for each owner an organisation adds — each of them
// reading that a licence which ran out years ago "ends soon", next to a date in
// the past. The bound is the same week the reminder is early by: a term is the
// run's business from a week before it ends until a week after.
func reminderDue(entitlement WireEntitlement, now time.Time) (string, bool) {
	if entitlement.Status != entitlementActive {
		return "", false
	}
	if !now.Before(entitlement.EndsAt.Add(reminderLead)) {
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
// store mail in one order.
//
// **A failure is carried, not thrown.** The loop goes on to the next entitlement
// and what failed is joined into the run's answer, because one organisation's
// undeliverable mail is that organisation's: a run that returned at the first
// failure would never remind any organisation the store files after it, and the
// order the store files them in is stable, so "after it" would be every run
// until somebody fixed that address by hand. The answer is still a failure
// (F33's retry is what asks it again), and only what is missing is missing: each
// owner is recorded as they are mailed (remind), so the retry asks again about
// the reminder that did not go out and about nothing else.
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
	var failures []error
	for _, entitlement := range held {
		kind, due := reminderDue(entitlement, s.now())
		if !due {
			continue
		}
		sent, err := s.remind(ctx, origin, entitlement, kind)
		if err != nil {
			failures = append(failures, fmt.Errorf("keyservice: the %s reminder for entitlement %s in organisation %s: %w",
				kind, entitlement.ID, entitlement.OrganisationID, err))
		}
		if sent == 0 {
			continue
		}
		if kind == reminderRenewal {
			run.Renewals++
		} else {
			run.Bindings++
		}
	}
	return run, errors.Join(failures...)
}

// remind sends one entitlement's reminder to the owners it has not already told
// and answers how many of them it mailed.
//
// **Each owner is tried, and each owner is recorded on their own.** One address
// the provider will not take is that address's failure: the owner beside them is
// mailed in the same run, and the record of each mail goes in as it goes, so a
// retry asks the provider again about what is missing and about nothing else. A
// single record for the reminder, written after every owner was mailed, cannot
// say "these were told, that one was not": the retry mails everybody again, and
// an address the provider keeps refusing costs the owners beside it a copy of
// the same reminder on every run the job's retries make.
//
// **The record is written after the mail it records and never before.** The
// other order records a reminder nobody received, and nothing would ever ask the
// service about it again; this one asks again, and the cost of the window
// between the send and the write is one repeated mail rather than one lost one.
//
// An organisation with no owner is nobody to tell and nothing is recorded, so an
// organisation that gains one is reminded at the next run rather than never.
func (s *Service) remind(ctx context.Context, origin string, entitlement WireEntitlement, kind string) (int, error) {
	organisation, present, err := getRecord[WireOrganisation](ctx, s.docs, organisationsCollection, entitlement.OrganisationID)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	owners, err := reminderOwners(ctx, s.docs, organisation.ID)
	if err != nil {
		return 0, err
	}
	sent := 0
	var failures []error
	for _, owner := range owners {
		id := reminderID(entitlement.ID, kind, entitlement.EndsAt, owner.UID)
		_, recorded, err := getRecord[reminderRecord](ctx, s.docs, remindersCollection, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if recorded {
			continue
		}
		if err := s.mail.Send(ctx, reminderMail(kind, owner.Email, organisation.Name, entitlement, origin, s.now())); err != nil {
			failures = append(failures, err)
			continue
		}
		record := reminderRecord{
			ID:             id,
			EntitlementID:  entitlement.ID,
			OrganisationID: organisation.ID,
			Recipient:      owner.UID,
			Kind:           kind,
			TermEndsAt:     entitlement.EndsAt.UTC(),
			SentAt:         s.now().UTC(),
		}
		doc, err := storedDoc(record)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := s.docs.Put(ctx, remindersCollection, id, doc); err != nil {
			failures = append(failures, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(failures...)
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

// The subjects are constants, so an organisation's name — text a person typed —
// cannot reach a header through any of them (§7.2's rule, read at this mail path
// as the invitations read it). There are two pairs and not one, because a term
// the run is inside the week past is mailed about after it has ended
// (reminderDue), and "ends soon" over a date that has been and gone is a mail
// that tells its reader the service does not know what day it is.
const (
	renewalReminderSubject = "Your Fugaro licence ends soon"
	renewalEndedSubject    = "Your Fugaro licence has ended"
	bindingReminderSubject = "Your Fugaro licence is not set up yet"
	bindingEndedSubject    = "Your Fugaro licence was not set up"
)

// reminderMail is one reminder as the person receives it: what the organisation
// holds, when the term ends, and the link to the screen where it is acted on.
// The link is the whole of what the mail carries — never a key, never a secret
// (D2182, F3), and the run has neither to hand: it reads entitlements, and a key
// is minted at binding and never stored (§7.3). The organisation's name is folded
// onto one line before it is written, because a name is text an owner typed and
// a mail line is one line.
//
// What a term that has already ended reads is the same mail in the past tense,
// which is the other half of the bound reminderDue puts on the window: the mail
// that goes out in the week after a term ends is the one that says so.
func reminderMail(kind, to, organisation string, entitlement WireEntitlement, origin string, now time.Time) Message {
	name := strings.Join(strings.Fields(organisation), " ")
	until := entitlement.EndsAt.UTC().Format("2 January 2006")
	link := strings.TrimSuffix(origin, "/") + routeWorkspaces
	ended := entitlement.EndsAt.Before(now)
	switch {
	case kind == reminderBinding && ended:
		return Message{
			To:      to,
			Subject: bindingEndedSubject,
			Body: "The Fugaro licence held by " + name + " was paid for and never bound to a project.\n\n" +
				"Its term ran from the day it was paid for and ended on " + until + ", so the whole of it went unused.\n" +
				"Buy the next term in the portal:\n\n" + link + "\n",
		}
	case kind == reminderBinding:
		return Message{
			To:      to,
			Subject: bindingReminderSubject,
			Body: "The Fugaro licence held by " + name + " is paid for and not bound to a project yet.\n\n" +
				"Its term runs from the day it was paid for and ends on " + until + ", so every day it is unbound is a day of it nobody uses.\n" +
				"Bind it to your Firebase project in the portal:\n\n" + link + "\n",
		}
	case ended:
		return Message{
			To:      to,
			Subject: renewalEndedSubject,
			Body: "The Fugaro licence held by " + name + " ended on " + until + ".\n\n" +
				"Nothing renewed this one, so the key it issued was the last that licence issued.\n" +
				"Buy the next term in the portal:\n\n" + link + "\n",
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
