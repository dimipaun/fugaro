// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The accrual, and the credit limit a reseller's mint is checked against
// (portal plan P6.2; D2189, D2190).
//
// **What a reseller has run up is the whole of what it has sold, and not the
// days gone by.** D2189's unit is a day — days times seats times the row's
// wholesale rate — and the sum below is every licence the reseller sold that has
// not stopped, over the whole term each of them was granted for, plus the one a
// request is asking for. The limit is the ceiling on what a reseller may have
// sold and not yet paid for (`resellers.schema.json`), and a figure that counted
// only the days elapsed would admit any number of sales made today: at one
// instant every licence's elapsed days are zero, so a burst of mints would each
// read a sum near nothing and pass a limit none of them is near. What a licence
// commits its seller to is the term it was sold for.
//
// **A revoked licence stops at the instant it was revoked** (D2189, and
// `resellers.schema.json`'s own reading of the rate: "it has to stop the day an
// entitlement is revoked rather than at the end of a term that was never
// served"). No record of a licence carries that instant, so it is written where
// the revocation happens, in the transaction that moves the status
// (entitlements.go's revokeEntitlement, through accrualStopToWrite): an accrual
// derived from anything else is a figure a refund leaves too high, and the days
// a reseller is no longer serving are days it must not be charged for. The stop
// is a record of its own rather than a field of the licence because the licence
// is a wire shape every reader of the portal decodes (F20, wire_entitlements.go)
// and this is a fact no reader outside this service has ever asked for — the
// same reason the operations queue is not one (ops.go's operationsQueued).
//
// **No total is stored.** The sum is derived from the licences it counts, every
// time it is asked, so there is no counter to drift from them: a relink moves a
// customer's licences into another reseller's sum (D2191), and a row whose price
// operations withdrew is a refusal rather than a price of zero.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// resellerAccrualStopsCollection holds one stop per revoked licence, keyed by
// the licence's own id: a licence has one stop, and a second revocation of the
// same licence leaves the first one standing (accrualStopToWrite).
const resellerAccrualStopsCollection = "reseller_accrual_stops"

// The two refusals this area answers besides the ones it reads from P5.1's mint
// (`errOperationsBadRow`, `errOperationsBadSeats`, `errOperationsBadTerm`,
// `errOperationsCheckInterval`), which are the row's own bounds and the same
// ones wherever a row is minted.
var (
	// errResellerRowNotPriced is a row this reseller has no wholesale price for.
	// `resellers.schema.json` reads a missing price as a row the reseller cannot
	// sell and not one it sells for nothing, and it is asked fail-closed: the
	// rate list is operations' and a rate withdrawn while licences priced under
	// it still serve leaves this reseller refused a further sale until one of
	// the two is set again, rather than accruing against a price nobody set.
	errResellerRowNotPriced = errors.New("keyservice: that row has no wholesale price for this reseller")
	// errResellerNoCredit is D2189's ceiling: the sale would pass the limit
	// operations set, and it is refused rather than queued
	// (`resellers.schema.json`: "a mint that would pass it is refused rather
	// than queued", and nothing is prepaid).
	errResellerNoCredit = errors.New("keyservice: this sale would pass the reseller's credit limit")
)

// resellerAccrualStop is the instant one licence stopped being charged for: the
// instant it was revoked, which is the end of the days its seller owes for. It
// is an internal record and not a wire shape (F20), for the reason the accrual's
// own header states.
type resellerAccrualStop struct {
	EntitlementID string    `json:"entitlement_id"`
	StoppedAt     time.Time `json:"stopped_at"`
}

// resellerMintMu serialises a reseller mint's limit check and its grant.
//
// The check is a read of the reseller's whole accrual and the grant is a write
// beside it, and the two are one act: two mints arriving together would each
// read a sum that does not hold the other's sale and the limit the second one
// was supposed to meet is the one the first already spent. The store has no
// counter to be read and written in one transaction — the sum is taken over the
// licences themselves, each read by its own query — so the two are read and
// written under one lock, which is what `admin.go`'s page gets from `signUpMu`'s
// pattern (orgs.go) and what `operationsMintMu` does for D454's cap (ops.go).
//
// It is a package-level mutex and not a field of Service for `operationsMintMu`'s
// own reason: the Service is the same value for every request it answers and a
// field would have to be added to it, which is P1.1's business and not this
// file's.
//
// What the lock bounds is one process's sales, which is what D454's cap is
// bounded by as well (ops.go's own sentence, "what one process serves here"):
// two replicas minting at once each spend the limit on their own. Reading the
// sum and writing the sale in one store transaction is what would bound both —
// and it is the thing the store cannot do here, because the sum is taken over
// the licences by queries of their own and a query inside a transaction is a
// read every concurrent write conflicts with.
var resellerMintMu sync.Mutex

// resellerAccrualMicros is one licence's accrual in micro-dollars: whole days
// times its seats times its row's daily rate.
//
// The days run from the term's start to its end, or to the instant the licence
// stopped, whichever comes first. `daysBetween` rounds a part day up, which is
// the term's own convention (merchant.go) and the safe one for a charge: a
// licence serves the day it was revoked on, and a seller does not stop owing for
// it by the hour it happened at.
//
// The product saturates rather than wrapping. The seats are a reseller's own
// request's and the rate is operations', and a sum that wrapped would be a
// negative cost, which every ceiling admits: a licence whose arithmetic
// overflowed would be a sale nothing refuses.
func resellerAccrualMicros(licence WireEntitlement, rate int64, stopped *time.Time) int64 {
	if rate <= 0 || licence.Seats <= 0 {
		return 0
	}
	until := licence.EndsAt
	if stopped != nil && stopped.Before(until) {
		until = *stopped
	}
	days := int64(daysBetween(licence.StartsAt, until))
	if days <= 0 {
		return 0
	}
	seats := int64(licence.Seats)
	if seats > math.MaxInt64/rate {
		return math.MaxInt64
	}
	cost := seats * rate
	if days > math.MaxInt64/cost {
		return math.MaxInt64
	}
	return days * cost
}

// resellerAccruedTotal adds one licence's accrual to a running sum, saturating
// at the same ceiling and for the same reason resellerAccrualMicros does: a
// total that wrapped would be a sum no limit holds anything back against.
func resellerAccruedTotal(held, cost int64) int64 {
	if cost > math.MaxInt64-held {
		return math.MaxInt64
	}
	return held + cost
}

// accrualStopToWrite is the stop a revocation writes, or nil when there is
// nothing to write: a licence no reseller sold is not one this area charges for,
// and one that already carries a stop keeps the instant it has — the FIRST
// instant a licence stopped is the one its days stop at, so a refund that
// follows an operator's revocation must not move it later and charge for the
// days between the two.
//
// It reads the stop it would write and writes nothing, so that the transaction
// wrapping it takes this read before its first write, which the deploy's store
// requires (docstore's ErrReadAfterWrite).
func accrualStopToWrite(ctx context.Context, tx Tx, licence WireEntitlement, at time.Time) (*resellerAccrualStop, error) {
	if licence.Source != entitlementSourceReseller {
		return nil, nil
	}
	_, present, err := getRecord[resellerAccrualStop](ctx, tx, resellerAccrualStopsCollection, licence.ID)
	if err != nil {
		return nil, err
	}
	if present {
		return nil, nil
	}
	return &resellerAccrualStop{EntitlementID: licence.ID, StoppedAt: at.UTC()}, nil
}

// resellerStoppedAt answers the instant one licence stopped accruing, or nil for
// one that has not stopped.
func (s *Service) resellerStoppedAt(ctx context.Context, licenceID string) (*time.Time, error) {
	held, present, err := getRecord[resellerAccrualStop](ctx, s.docs, resellerAccrualStopsCollection, licenceID)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	stopped := held.StoppedAt.UTC()
	return &stopped, nil
}

// rateFor answers one reseller's wholesale price for one row, and whether it has
// one at all: a row with no rate is a row it cannot sell
// (`resellers.schema.json`).
func rateFor(terms WireReseller, row string) (int64, bool) {
	for _, rate := range terms.Rates {
		if rate.Row == row {
			return rate.DailyRateMicros, true
		}
	}
	return 0, false
}

// resellerUnpaidMicros is the whole of what one reseller has run up and not paid
// for: every licence it sold, to every customer it holds now, that has not
// stopped, each over the whole term it was granted for (the accrual's own
// header).
//
// The customers are its **current** links and not the ones a sale was made
// under, which is D2191's own reading of a relink ("a customer whose reseller
// changed is a customer whose accrual moves with it"): operations relinking a
// customer moves the money with it, and both resellers count the customer's
// licences from the instant the link is written.
//
// The kind is not asked. `kind` is `comped` for the free seat, which is the one
// comped row the policy table carries and is operations' to grant (policy.go's
// `Policy`); a licence a reseller's own mint wrote is a sale, and a comp that
// lands on one later is operations forgiving a debt it set rather than a licence
// a reseller may hand out for nothing — read here, it would be a row a reseller
// mints without accruing against the very ceiling D2189 puts on it. Which of the
// two a comp is, is D2190's question and P6.3's ledger to carry.
func (s *Service) resellerUnpaidMicros(ctx context.Context, resellerID string, terms WireReseller) (int64, error) {
	links, err := recordsWhere[WireResellerCustomer](ctx, s.docs, resellerCustomersCollection,
		Where{Field: "reseller_organisation_id", Value: resellerID})
	if err != nil {
		return 0, err
	}
	total := int64(0)
	for _, link := range links {
		held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection,
			Where{Field: "organisation_id", Value: link.OrganisationID})
		if err != nil {
			return 0, err
		}
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
			total = resellerAccruedTotal(total, resellerAccrualMicros(licence, rate, stopped))
		}
	}
	return total, nil
}

// mintResellerLicence grants one term on a reseller's behalf, inside the credit
// limit operations set for it (D2189, D2190): what spec §Resellers promises it
// ("Each minted key or seat change is charged at their wholesale price,
// prorated by days") and the door `api_reseller_keys.go` answers.
//
// **The customer is the caller's own and the workspace is that customer's.** The
// reseller the act is for is the caller's membership (resellers.go's resellerFor)
// and the customer is read through `resellerCustomer`, which answers another
// reseller's customer, one nobody linked and an id nobody has with one refusal.
// The workspace is the customer's organisation's: a reseller that could mint
// into a workspace of its own choosing could grant a term to anybody's
// installation, and the licence is granted to the organisation the workspace
// belongs to and not to one the body names.
//
// **The bounds are the row's, as they are at every other door** (F28, D2184,
// F30): the row is looked up rather than trusted, the seats are the row's where
// the row carries them, the end is inside the row's own term, and the check
// interval is the span F28 states. They are the checks `operationsMint` makes,
// written again at this door and not shared with it: the two doors are two acts
// and each states the bounds its own caller meets.
//
// **The limit check and the write are one act** (resellerMintMu). The sum is
// read from the licences themselves and the grant is a transaction beside it, and
// two mints in flight together would each read a sum that does not hold the
// other's sale: the reseller would hold two licences whose terms together pass
// its limit, which is the one thing the limit is for. The lock is taken before
// the sum is read and held until the grant has landed.
//
// **No line is written in the log.** The log is the operations team's
// (audit.go, resellers.go's own header): the three acts of this area that write
// one are operations' acts on a reseller, and a reseller minting for its own
// customer is the reseller's own work. What records it is the licence itself —
// its source, its row, its seats, its term and the day it was written — which is
// what the accrual above and P6.3's statement are read from.
func (s *Service) mintResellerLicence(ctx context.Context, held resellerAccess, customerID string, asked WireEntitlementMint) (WireEntitlement, error) {
	if err := held.write(); err != nil {
		return WireEntitlement{}, err
	}
	org, _, err := s.resellerCustomer(ctx, held, customerID)
	if err != nil {
		return WireEntitlement{}, err
	}
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
	terms, err := s.resellerTerms(ctx, held.Organisation)
	if err != nil {
		return WireEntitlement{}, err
	}
	rate, priced := rateFor(terms, row.Name)
	if !priced {
		return WireEntitlement{}, fmt.Errorf("%w: the %s row", errResellerRowNotPriced, row.Name)
	}
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, asked.WorkspaceID)
	if err != nil {
		return WireEntitlement{}, err
	}
	// A workspace that is not there, of another organisation, and of the
	// caller's own reseller organisation are one refusal, for the reason
	// `resellerCustomer` answers the three ways a customer can be missing with
	// one: which workspaces exist, and which organisation holds one, is not
	// something a reseller learns from this service.
	if !present || workspace.OrganisationID != org.ID {
		return WireEntitlement{}, errNoSuchResellerCustomer
	}
	now := s.now().UTC()
	ends := asked.EndsAt.UTC()
	if !ends.After(now) || ends.After(now.Add(row.Term)) {
		return WireEntitlement{}, fmt.Errorf("%w: a sale of the %s row runs from %s and at most %s from there", errOperationsBadTerm, row.Name, now, row.Term)
	}
	resellerMintMu.Lock()
	defer resellerMintMu.Unlock()
	used, err := s.resellerUnpaidMicros(ctx, held.Organisation.ID, terms)
	if err != nil {
		return WireEntitlement{}, err
	}
	id, err := newID()
	if err != nil {
		return WireEntitlement{}, err
	}
	workspaceID := workspace.ID
	// One grant, priced and then handed over: the price is taken from the record
	// this grant makes and the same grant is what is written, so the sale the
	// limit was checked against and the sale that lands cannot come apart.
	grant := entitlementGrant{
		ID:             id,
		At:             now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspaceID,
		Source:         entitlementSourceReseller,
		Row:            row.Name,
		Seats:          asked.Seats,
		StartsAt:       now,
		EndsAt:         ends,
	}
	candidate, err := entitlementRecord(grant)
	if err != nil {
		return WireEntitlement{}, err
	}
	cost := resellerAccrualMicros(candidate, rate, nil)
	// The comparison is written this way round so that neither side can
	// overflow: a sum that reached its ceiling is refused here rather than
	// wrapped and admitted.
	if used > terms.CreditLimitMicros || cost > terms.CreditLimitMicros-used {
		return WireEntitlement{}, fmt.Errorf("%w: %d micros is committed and this sale of %s adds %d to the %d the reseller may run up",
			errResellerNoCredit, used, row.Name, cost, terms.CreditLimitMicros)
	}
	return s.grantEntitlement(ctx, grant)
}
