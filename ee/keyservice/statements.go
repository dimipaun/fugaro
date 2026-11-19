// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The monthly reseller statements (portal plan P6.3; D2189, D2190).
//
// **The direction is D2190's finding.** A reseller owes Fugaro, and a statement
// is one month of that debt: the accrual the licence records already carry
// (accrual.go), summed per customer — days times seats times the wholesale rate.
// There is no commission anywhere in this file, and nothing here pays anybody.
//
// **A statement is the month the run is for, and never the term.** The accrual
// itself runs over a licence's whole term (resellerAccrualMicros); a statement
// is the part of each term that fell inside one UTC month, which is why every
// licence is read with its term clipped to the window first. The days before the
// month belong to an earlier statement and the days after it to a later one, and
// a statement that charged whole terms would bill a year in the month a year
// began.
//
// **A month is issued once, and the run is what issues it.** Cloud Scheduler
// posts to the run's route once a month (P1.5's scheduler.tf, created paused and
// turned on by the deploy that ships the route), and the statement's own id —
// the reseller and the month — is what makes a second run ask for the statement
// that exists rather than write a second one. The month is the previous UTC
// calendar month: a month is whole only once it is over, and the run's whole
// business is a month nobody can still add a day to.
//
// **Nothing here is a payment.** Operations records what arrives
// (api_statements.go's route) and this file keeps the two figures apart: a
// statement is issued before it is paid, and a part payment leaves it open
// rather than changing what it was issued for.
//
// **A month that accrued nothing is no statement.** A reseller that sold nothing
// is owed nothing: a zero statement every month would be a row on a screen
// nobody can act on, and its absence says the same thing. What a run could not
// read is the other case and is not silence — the failure is joined into the
// run's answer so that the job's retry is asked again about exactly what is
// missing (reminders.go's own reading of a run's failure).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// statementsCollection holds one document per month that was issued and
// statementSendsCollection one per mail that went out. Both names are this
// area's own and not the deploy's, for the reason the tenancy's two are
// (orgs.go).
const (
	statementsCollection     = "statements"
	statementSendsCollection = "statement_sends"
)

// The two states a statement is in, spelled as the schema's own vocabulary. A
// statement follows its amounts rather than a state somebody sets, so nothing
// here moves one to paid without the money being there first.
const (
	statementOpen = "open"
	statementPaid = "paid"
)

// actionStatementPayment is the operations act that records money arriving. It
// is the one act of this area that writes a log line, and it is operations'
// because only a person sees the money arrive (audit.go's vocabulary).
const actionStatementPayment = "statement.payment"

// errNoSuchStatement is a payment for a statement no reseller in this portal
// holds. An id nobody has and one belonging to another reseller are one refusal,
// for the reason resellerOrganisation answers the ways an organisation can be
// missing with one: which statements exist is not something a caller learns from
// this service.
var errNoSuchStatement = errors.New("keyservice: no statement in this portal has that id")

// errStatementBadPayment is a payment no statement can be recorded with: an
// amount of nothing, a reference nobody can be given, or a total that would
// overflow.
var errStatementBadPayment = errors.New("keyservice: that is not a payment a statement can be recorded with")

// statementSend is one statement mailed to one person, and it is what keeps a
// second run quiet. The record is written after the mail it records and never
// before (remind's own ordering and its own reason: this way the window between
// the two costs a repeated mail rather than a mail nobody received), and it is
// named by the statement and the recipient, so each owner is recorded on their
// own.
//
// The recipient is the membership's own uid and never the address it was mailed
// to (§7.3's rule at this collection), which is what reminderRecord keeps too:
// the address is read from the membership each run, and a record that kept it
// would be a list of addresses the store has no use for.
type statementSend struct {
	ID                     string    `json:"id"`
	StatementID            string    `json:"statement_id"`
	ResellerOrganisationID string    `json:"reseller_organisation_id"`
	Recipient              string    `json:"recipient"`
	SentAt                 time.Time `json:"sent_at"`
}

// statementRun is what one run did, which is what its answer's log line says and
// nothing else.
type statementRun struct {
	Issued int
	Mailed int
}

// statementID is the name one reseller's month is filed under. It is derived
// rather than minted because it is what makes the run idempotent: a second run
// of the same month asks the store for this name and is answered the statement
// the first one wrote, where a minted id would leave a scan the only way to find
// a statement that may already exist.
//
// A reseller organisation's id is `newID`'s alphabet and a month is six digits
// and a hyphen: neither can hold the separator.
func statementID(resellerOrganisationID, month string) string {
	return resellerOrganisationID + ":" + month
}

// statementSendID is the name one statement's mail to one person is filed under:
// one owner's address is refused where another's is not (mailStatement), so a
// record per recipient is what lets a retry hold off on the owners it reached.
func statementSendID(statement, recipient string) string {
	return statement + ":" + recipient
}

// statementMonthWindow answers the month one run is about and the window it
// covers: the previous UTC calendar month, from its first instant to the first
// instant of the next.
//
// The month is taken from the first of the month the run is in and not from the
// run's own day, because AddDate normalises: a month subtracted from the 31st of
// March answers the 3rd of March, which would state February over three days of
// it. A month boundary is a time zone's opinion (the schema's own sentence), so
// every boundary here is UTC.
func statementMonthWindow(now time.Time) (string, time.Time, time.Time) {
	utc := now.UTC()
	first := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := first.AddDate(0, -1, 0)
	return start.Format("2006-01"), start, first
}

// resellerOrganisations is every reseller organisation the portal holds, ordered
// by id, which is the list a run works through.
//
// It is a read of the organisations collection rather than a registry of its
// own: an organisation's type is what makes it a reseller (orgs.go), and a list
// beside it would be a second thing operations creating a reseller has to write
// — and the one it would forget.
func (s *Service) resellerOrganisations(ctx context.Context) ([]WireOrganisation, error) {
	held, err := recordsWhere[WireOrganisation](ctx, s.docs, organisationsCollection, Where{})
	if err != nil {
		return nil, err
	}
	resellers := make([]WireOrganisation, 0, len(held))
	for _, org := range held {
		if org.Type == orgTypeReseller {
			resellers = append(resellers, org)
		}
	}
	sort.Slice(resellers, func(i, j int) bool { return resellers[i].ID < resellers[j].ID })
	return resellers, nil
}

// statementAccrual is one reseller's month: one line per customer it accrued for
// and the sum of them, in micro-dollars.
//
// **The lines are the customers' and not the licences'.** The schema's own word
// is one line per customer, which is what a reseller reconciles a month with: a
// customer holding two workspaces is one figure it can look up, and the licences
// are the arithmetic behind it.
//
// **A customer who accrued nothing in the month is no line.**
// statementMonthAccrualMicros prices only the part of a term inside the window,
// so a licence that ended before the month began contributes nothing and its
// customer is left out rather than carried as a zero.
//
// The statements are read from the reseller's current links, which is the set
// `resellerUnpaidWith` sums as well: a customer linked after the month closed
// has no line in it, which is the same thing a sale made after the month would
// be.
func (s *Service) statementAccrual(ctx context.Context, resellerOrganisationID string, terms WireReseller, start, end time.Time) ([]WireStatementLine, int64, error) {
	links, err := recordsWhere[WireResellerCustomer](ctx, s.docs, resellerCustomersCollection,
		Where{Field: "reseller_organisation_id", Value: resellerOrganisationID})
	if err != nil {
		return nil, 0, err
	}
	lines := make([]WireStatementLine, 0, len(links))
	total := int64(0)
	for _, link := range links {
		amount, err := s.statementCustomerMicros(ctx, link.OrganisationID, terms, start, end)
		if err != nil {
			return nil, 0, err
		}
		if amount <= 0 {
			continue
		}
		lines = append(lines, WireStatementLine{OrganisationID: link.OrganisationID, AmountMicros: amount})
		total = resellerAccruedTotal(total, amount)
	}
	// The schema promises a stable order so that a reseller comparing two months
	// sees which customer moved: the organisation id is the one field two months'
	// lines for one customer share.
	sort.Slice(lines, func(i, j int) bool { return lines[i].OrganisationID < lines[j].OrganisationID })
	return lines, total, nil
}

// statementCustomerMicros is one customer's accrual for the month: every licence
// this reseller sold it, priced over the part of its term inside the window and
// summed with the saturation the whole-term sum uses.
//
// The licences are read the way resellerUnpaidWith reads them — the customer
// organisation's own, and only the ones a reseller's sale wrote — so that the
// month a statement bills and the accrual the credit limit is measured against
// are one reading of one set of licences.
//
// A licence on a row the reseller's terms price nothing for is the whole month's
// failure rather than a line that is quietly too small: operations setting terms
// and a licence's row are two records, and a sum that skipped the second would
// be a statement nothing explains (accrual.go's own reading of the same case).
func (s *Service) statementCustomerMicros(ctx context.Context, customerOrganisationID string, terms WireReseller, start, end time.Time) (int64, error) {
	held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection,
		Where{Field: "organisation_id", Value: customerOrganisationID})
	if err != nil {
		return 0, err
	}
	amount := int64(0)
	for _, licence := range held {
		if licence.Source != entitlementSourceReseller {
			continue
		}
		rate, priced := rateFor(terms, licence.Row)
		if !priced {
			return 0, fmt.Errorf("%w: the licence %s was sold on the %s row and this reseller is priced for no such row",
				errResellerRowNotPriced, licence.ID, licence.Row)
		}
		stopped, err := s.resellerStoppedAt(ctx, licence.ID)
		if err != nil {
			return 0, err
		}
		amount = resellerAccruedTotal(amount, statementMonthAccrualMicros(licence, rate, stopped, start, end))
	}
	return amount, nil
}

// statementMonthAccrualMicros is one licence's accrual over the part of its term
// inside the window, by the accrual's own arithmetic rather than a second one:
// it is what the licence has accrued by the window's end less what it had
// accrued by the window's start, so the days a month is charged for and the
// saturation are the rules the credit limit already reads.
//
// **The two figures are cumulative from the term's own start, which is what
// keeps the months from overbilling the term.** The accrual rounds a part day up
// (daysBetween: a term is what a key has to cover), so pricing a term clipped to
// the window would round at both of the window's edges: the day a term was sold
// on would be charged in full in the month of the sale and the day it ends on in
// full in its last month, and a year sold at noon would be billed 366 days of
// 365 — every month of a term that does not start at midnight, which is nearly
// every term. The difference of the two cumulative accruals puts the rounding on
// the term's own instants instead, so the months telescope — each one's upper
// figure is the next one's lower — and the last month is left with whatever the
// term's own end still owes: the months of a term sum to the term exactly
// (TestStatementMonthsSumToTheTerm).
//
// A lower instant past the term's end is the term's own end, and one before the
// term's start prices no day, so a term lying wholly outside the window answers
// nothing and no month is ever charged a negative. The licence is copied and
// never written back: the window is the statement's, and the record stays the
// term the reseller sold.
func statementMonthAccrualMicros(licence WireEntitlement, rate int64, stopped *time.Time, start, end time.Time) int64 {
	by := licence
	if by.EndsAt.After(end) {
		by.EndsAt = end
	}
	before := licence
	if before.EndsAt.After(start) {
		before.EndsAt = start
	}
	return resellerAccrualMicros(by, rate, stopped) - resellerAccrualMicros(before, rate, stopped)
}

// issueStatement is one reseller's month as the store holds it, and whether this
// run is the one that wrote it.
//
// **A month already issued is read and not written again.** A statement the
// store holds at this id is answered as it stands, so a payment recorded against
// it and the figure the reseller was mailed cannot come apart — and a second run
// of the month, whether that is the job's retry, a hand-made request or a second
// replica, is a run that changes nothing.
//
// **A month that accrued nothing answers no statement at all**, which the zero
// id is and not an error: there is nothing to bill, nothing to mail, and nothing
// for a payment to be recorded against.
//
// The write is inside a transaction that re-reads the id, so that two runs
// cannot each find it absent and each write one. What the second run would write
// is the first run's statement with a later instant on it, which is exactly the
// record a statement must not have: it was issued once.
func (s *Service) issueStatement(ctx context.Context, reseller WireOrganisation, month string, start, end time.Time) (WireStatement, bool, error) {
	id := statementID(reseller.ID, month)
	held, present, err := getRecord[WireStatement](ctx, s.docs, statementsCollection, id)
	if err != nil {
		return WireStatement{}, false, err
	}
	if present {
		return held, false, nil
	}
	terms, err := s.resellerTerms(ctx, reseller)
	if err != nil {
		return WireStatement{}, false, err
	}
	lines, accrued, err := s.statementAccrual(ctx, reseller.ID, terms, start, end)
	if err != nil {
		return WireStatement{}, false, err
	}
	if accrued <= 0 {
		return WireStatement{}, false, nil
	}
	candidate := WireStatement{
		ID:                     id,
		ResellerOrganisationID: reseller.ID,
		Month:                  month,
		AccruedMicros:          accrued,
		PaidMicros:             0,
		State:                  statementOpen,
		Lines:                  lines,
		IssuedAt:               s.now().UTC(),
	}
	issued, fresh := candidate, true
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, present, err := getRecord[WireStatement](ctx, tx, statementsCollection, id)
		if err != nil {
			return err
		}
		if present {
			issued, fresh = held, false
			return nil
		}
		return putRecord(ctx, tx, statementsCollection, id, candidate)
	})
	if err != nil {
		return WireStatement{}, false, err
	}
	return issued, fresh, nil
}

// runStatements issues the previous month for every reseller the portal holds
// and mails each statement to that reseller's owners, once.
//
// The order is the resellers' own (by organisation id), so two runs over one
// store issue and mail in one order.
//
// **A failure is carried, not thrown** (runReminders' own reading): one
// reseller's unreadable month — a licence on a row its terms do not price, an
// address the provider will not take — is that reseller's, and a run that
// returned at the first of them would never reach the resellers the store files
// after it, which is every run until somebody fixed it by hand. What failed is
// joined into the answer, so the retry F33 gives the job is asked again about
// exactly what is missing, and nothing is issued twice: the month's own id and
// the send records are what the retry reads.
func (s *Service) runStatements(ctx context.Context) (statementRun, error) {
	origin, err := portalOrigin(s.cfg.URL)
	if err != nil {
		return statementRun{}, err
	}
	month, start, end := statementMonthWindow(s.now())
	held, err := s.resellerOrganisations(ctx)
	if err != nil {
		return statementRun{}, err
	}
	var run statementRun
	var failures []error
	for _, reseller := range held {
		statement, fresh, err := s.issueStatement(ctx, reseller, month, start, end)
		if err != nil {
			failures = append(failures, fmt.Errorf("keyservice: the %s statement of the reseller %s was not issued: %w",
				month, reseller.ID, err))
			continue
		}
		if statement.ID == "" {
			continue
		}
		if fresh {
			run.Issued++
		}
		mailed, err := s.mailStatement(ctx, origin, reseller, statement)
		if err != nil {
			failures = append(failures, fmt.Errorf("keyservice: the %s statement of the reseller %s was not mailed to everybody: %w",
				month, reseller.ID, err))
		}
		run.Mailed += mailed
	}
	return run, errors.Join(failures...)
}

// mailStatement mails one statement to the reseller's owners it has not already
// told, and answers how many it mailed.
//
// The recipients are the organisation's owners, which is the list the renewal
// reminders go to (reminderOwners) and it is the same list for the same reason:
// the mail is about money the organisation owes, and the owner is the person who
// pays. An organisation with no owner is nobody to tell and nothing is recorded,
// so one that gains an owner before the next run is mailed by it.
//
// **Each owner is tried, and each is recorded on their own** (remind's own
// shape): one address the provider will not take is that address's failure, the
// owners beside them are mailed in the same run, and a retry asks the provider
// again about what is missing and about nothing else.
func (s *Service) mailStatement(ctx context.Context, origin string, reseller WireOrganisation, statement WireStatement) (int, error) {
	owners, err := reminderOwners(ctx, s.docs, reseller.ID)
	if err != nil {
		return 0, err
	}
	sent := 0
	var failures []error
	for _, owner := range owners {
		id := statementSendID(statement.ID, owner.UID)
		_, recorded, err := getRecord[statementSend](ctx, s.docs, statementSendsCollection, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if recorded {
			continue
		}
		if err := s.mail.Send(ctx, statementMail(owner.Email, reseller.Name, statement, origin)); err != nil {
			failures = append(failures, err)
			continue
		}
		record := statementSend{
			ID:                     id,
			StatementID:            statement.ID,
			ResellerOrganisationID: reseller.ID,
			Recipient:              owner.UID,
			SentAt:                 s.now().UTC(),
		}
		doc, err := storedDoc(record)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := s.docs.Put(ctx, statementSendsCollection, id, doc); err != nil {
			failures = append(failures, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(failures...)
}

// routeReseller is the portal page a statement leads to: the reseller's own
// screen, where the month, its lines and what has been paid against it are read
// (P6.4).
const routeReseller = "/reseller"

// The subject is a constant, so a reseller's name — text a person typed — cannot
// reach a header through it (§7.2's rule, read at this mail path the way the
// reminders read it).
const statementSubject = "Your Fugaro statement is ready"

// statementMail is one statement as the person receives it: which month, what it
// came to, and the link to the screen the lines are read on. The link is the
// whole of what the mail carries — never a key, never a secret (D2182, F3) — and
// the organisation's name is folded onto one line before it is written, because
// a name is text somebody typed and a mail line is one line (reminderMail's own
// reading of the same rule).
func statementMail(to, organisation string, statement WireStatement, origin string) Message {
	name := strings.Join(strings.Fields(organisation), " ")
	link := strings.TrimSuffix(origin, "/") + routeReseller
	return Message{
		To:      to,
		Subject: statementSubject,
		Body: "The Fugaro statement for " + name + " for " + statement.Month + " is " +
			statementAmount(statement.AccruedMicros) + ".\n\n" +
			"It is one line per customer you sold to that month, and what has been paid against it:\n\n" +
			link + "\n",
	}
}

// statementAmount writes one figure the way a sentence does: whole dollars and
// cents. Micro-dollars are the store's unit and the arithmetic's (accrual.go),
// and a nine-digit number of them is not a figure a person reconciles a bank
// statement against.
func statementAmount(micros int64) string {
	return fmt.Sprintf("$%d.%02d", micros/1_000_000, (micros%1_000_000)/10_000)
}

// resellerStatements is one reseller's statements, the newest month first, which
// is the order the reseller's screen draws them in (P6.4).
//
// The statements are read by the reseller the caller is a member of and never
// from the request (resellerCaller): which statements a caller sees is the
// token's own standing, as it is for the customers beside them.
func (s *Service) resellerStatements(ctx context.Context, resellerOrganisationID string) ([]WireStatement, error) {
	held, err := recordsWhere[WireStatement](ctx, s.docs, statementsCollection,
		Where{Field: "reseller_organisation_id", Value: resellerOrganisationID})
	if err != nil {
		return nil, err
	}
	sort.Slice(held, func(i, j int) bool {
		if held[i].Month == held[j].Month {
			return held[i].ID < held[j].ID
		}
		return held[i].Month > held[j].Month
	})
	return held, nil
}

// recordStatementPayment records money that arrived against one statement, as
// operations was told it (D2190).
//
// **The statement follows its amounts and nothing sets its state.** A payment
// that covers the accrual marks the statement paid and keeps the instant the
// money arrived; one that does not leaves it open with `paid_at` null and the
// payment in its figure, which is what a reseller reconciling a month is looking
// at (statements.schema.json's own sentence). Overpaying is admitted rather than
// refused: a reseller that transferred too much has paid the statement, and the
// difference is between it and whoever reconciles the account.
//
// **A reference is required and an amount of nothing is a mistake.** The
// reference is what matches a payment to a statement months later, and what
// makes one transfer entered twice visible as one reference in two places; a
// payment of zero is not a fact about money, and the schema refuses it at the
// minimum. The sum is checked for overflow before it is written, because a total
// that wrapped would read as a statement that is owed again.
//
// The audit line is written before the act (D456) and after the guards that
// decide whether there is an act at all: an id nobody holds is a refusal that
// leaves no entry in the log.
func (s *Service) recordStatementPayment(ctx context.Context, actor Caller, id string, asked WireStatementPayment) (WireStatement, error) {
	amount := asked.AmountMicros
	reference := strings.TrimSpace(asked.Reference)
	if amount < 1 || reference == "" {
		return WireStatement{}, errStatementBadPayment
	}
	id = strings.TrimSpace(id)
	held, present, err := getRecord[WireStatement](ctx, s.docs, statementsCollection, id)
	if err != nil {
		return WireStatement{}, err
	}
	if !present {
		return WireStatement{}, fmt.Errorf("%w: %s", errNoSuchStatement, id)
	}
	if err := s.operationsAudit(ctx, actor, actionStatementPayment, &held.ResellerOrganisationID, &held.ID, false, &reference); err != nil {
		return WireStatement{}, err
	}
	paidAt := asked.PaidAt.UTC()
	written := held
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		current, present, err := getRecord[WireStatement](ctx, tx, statementsCollection, id)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("%w: %s", errNoSuchStatement, id)
		}
		if current.PaidMicros > math.MaxInt64-amount {
			return errStatementBadPayment
		}
		current.PaidMicros += amount
		if current.PaidMicros >= current.AccruedMicros {
			current.State = statementPaid
			at := paidAt
			current.PaidAt = &at
		}
		written = current
		return putRecord(ctx, tx, statementsCollection, id, current)
	})
	if err != nil {
		return WireStatement{}, err
	}
	return written, nil
}
