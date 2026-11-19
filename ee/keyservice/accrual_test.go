// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The cases for a reseller's accrual and the credit limit a sale is checked
// against (portal plan P6.2; D2189, D2190).
//
// Every case starts from the reseller `resellers_test.go` seeds — one reseller
// organisation with one owner and the terms operations would have set on it, a
// limit of `resellerLimit` micros and one wholesale rate of `resellerRate` a day
// for the monthly row — and the numbers are read as arithmetic on purpose: a sum
// that drifted from days times seats times the rate would still be a sum, and a
// case that asserted "more than nothing" would pass it.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	// The sale every case below is built from: twenty seats of the monthly row
	// for the row's own thirty days, which is 20 * 30 * resellerRate micros.
	saleSeats  = 20
	saleDays   = 30
	saleMicros = int64(saleSeats) * int64(saleDays) * resellerRate

	// The second reseller's own people, which `resellers_test.go` seeds with
	// `resellerOrg` for the cases about who a customer belongs to.
	rivalUID  = "uid-rival"
	rivalMail = "rival@partner.example"
)

// soldWorkspace seeds one workspace of one customer organisation, which is what
// a sale is for: seeded rather than bound, because binding is D2196's own act and
// mints a key (P3.2), and what these cases are about is the money.
func (f *fixture) soldWorkspace(organisationID, id string) string {
	f.t.Helper()
	f.seed(workspacesCollection, id, WireWorkspace{
		ID: id, OrganisationID: organisationID, Name: "Sold workspace " + id, CreatedAt: f.now,
	})
	return id
}

// mintSale posts one sale the way a reseller's screen does: the customer in the
// path and the licence in the body, which is the shape P5.1's mint reads too.
func (f *fixture) mintSale(customerID, workspaceID, row string, seats int, ends time.Time) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.apiAsCaller(http.MethodPost, resellerKeysPathOf(customerID), f.mintBody(workspaceID, row, seats, ends))
}

// saleOf is one sale asserted to have been made, and the licence it wrote: every
// case below needs the record it is about, and a case that carried on after a
// refusal would be reading a licence that is not there.
func (f *fixture) saleOf(customerID, workspaceID, row string, seats int, ends time.Time) WireEntitlement {
	f.t.Helper()
	rec := f.mintSale(customerID, workspaceID, row, seats, ends)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("the sale of %d seat(s) of %s in %s answered %d: %s", seats, row, workspaceID, rec.Code, rec.Body)
	}
	return readAnswer[WireEntitlement](f, rec)
}

// unpaid is one reseller's accrual as the service reads it, from the terms the
// store holds for it.
func (f *fixture) unpaid(resellerOrganisationID string) int64 {
	f.t.Helper()
	terms, found := f.terms(resellerOrganisationID)
	if !found {
		f.t.Fatalf("the reseller %s has no terms to accrue against", resellerOrganisationID)
	}
	total, err := f.svc.resellerUnpaidMicros(context.Background(), resellerOrganisationID, terms)
	if err != nil {
		f.t.Fatalf("reading the accrual of %s: %v", resellerOrganisationID, err)
	}
	return total
}

// TestAccrualIsSeatsTimesTheDailyRate: D2189's arithmetic — what a reseller has
// run up is days times seats times its wholesale rate, over the term each sale
// was granted for, and nothing else is in the sum.
//
// The two sales are the same row at two sizes, so the case reads the figure both
// ways: the sum of the two terms, and half of it when the rate is halved, which
// is what "days times seats times the rate" means and what a flat price per sale
// would not answer.
func TestAccrualIsSeatsTimesTheDailyRate(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	first := f.soldWorkspace(bought.ID, "ws-sold-1")
	second := f.soldWorkspace(bought.ID, "ws-sold-2")

	// Twenty seats for the row's own month, and five for ten days: 600_000 and
	// 50_000 micros.
	f.saleOf(bought.ID, first, PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))
	f.saleOf(bought.ID, second, PolicyTeamMonthly, 5, f.now.Add(10*24*time.Hour))

	if got := f.unpaid(resellerID); got != saleMicros+50_000 {
		t.Errorf("the accrual is %d micros, want the two terms' own %d: 20*30*%d and 5*10*%d",
			got, saleMicros+50_000, resellerRate, resellerRate)
	}

	// The same licences at half the price are half the money, which is the rate's
	// own place in the sum and not a price per sale.
	terms, _ := f.terms(resellerID)
	terms.Rates = []WireResellerRate{{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate / 2}}
	half, err := f.svc.resellerUnpaidMicros(context.Background(), resellerID, terms)
	if err != nil {
		t.Fatalf("reading the accrual at half the rate: %v", err)
	}
	if want := (saleMicros + 50_000) / 2; half != want {
		t.Errorf("the accrual at half the rate is %d micros, want %d: the rate is what a day of a seat costs", half, want)
	}

	// A licence of the same organisation that no reseller sold is not the
	// reseller's to charge: a purchase in it is the customer's own money.
	if _, err := f.svc.grantEntitlement(context.Background(), entitlementGrant{
		At:             f.now,
		OrganisationID: bought.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	}); err != nil {
		t.Fatalf("granting the purchase beside the sales: %v", err)
	}
	if got := f.unpaid(resellerID); got != saleMicros+50_000 {
		t.Errorf("the accrual is %d micros after a purchase in the same organisation, want the sales' own %d: only what this reseller sold is its debt",
			got, saleMicros+50_000)
	}
}

// TestMintBeyondTheCreditLimitIsRefused: D2189's ceiling — a sale that would
// pass the limit is refused rather than queued, and a sale that reaches it
// exactly is not one that passed it.
//
// The boundary is read from both sides, because `>` and `>=` are one character
// apart and the difference is a sale a reseller may or may not make: with
// 4_800_000 committed, the 200_000 that lands on the limit is admitted and the
// 1_000 that would go past it is refused.
func TestMintBeyondTheCreditLimitIsRefused(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	// Eight sales of 600_000 fill 4_800_000 of the 5_000_000 the reseller may run
	// up, and the ninth would pass it.
	workspaces := make([]string, 0, 10)
	for i := range 10 {
		workspaces = append(workspaces, f.soldWorkspace(bought.ID, fmt.Sprintf("ws-limit-%d", i)))
	}
	ends := f.now.Add(saleDays * 24 * time.Hour)
	for i := range 8 {
		f.saleOf(bought.ID, workspaces[i], PolicyTeamMonthly, saleSeats, ends)
	}
	if got := f.unpaid(resellerID); got != 8*saleMicros {
		t.Fatalf("eight sales accrue %d micros, want %d", got, 8*saleMicros)
	}

	refused := f.mintSale(bought.ID, workspaces[8], PolicyTeamMonthly, saleSeats, ends)
	if refused.Code != http.StatusConflict || refusal(f, refused) != "over_credit_limit" {
		t.Errorf("the ninth sale of the month answered %d %s, want 409 over_credit_limit: %s",
			refused.Code, refusal(f, refused), refused.Body)
	}
	// A refusal is not a sale: nothing was written for it, and the money is what
	// it was.
	if held := f.entitlementsOfOrganisation(bought.ID); len(held) != 8 {
		t.Errorf("the refused sale left %d licence(s), want the eight that were sold", len(held))
	}
	if got := f.unpaid(resellerID); got != 8*saleMicros {
		t.Errorf("the refused sale moved the accrual to %d micros, want %d", got, 8*saleMicros)
	}

	// Exactly the room the limit has left: 20 seats for 10 days is 200_000, and
	// 4_800_000 + 200_000 is the limit itself rather than past it.
	f.saleOf(bought.ID, workspaces[8], PolicyTeamMonthly, saleSeats, f.now.Add(10*24*time.Hour))
	if got := f.unpaid(resellerID); got != resellerLimit {
		t.Fatalf("the sale that reaches the limit leaves %d micros, want the limit's own %d", got, resellerLimit)
	}
	// And one seat's day past it is one micro past it: refused.
	past := f.mintSale(bought.ID, workspaces[9], PolicyTeamMonthly, 1, f.now.Add(24*time.Hour))
	if past.Code != http.StatusConflict || refusal(f, past) != "over_credit_limit" {
		t.Errorf("one seat for one day at the limit answered %d %s, want 409 over_credit_limit: %s",
			past.Code, refusal(f, past), past.Body)
	}
}

// TestRevokedEntitlementStopsAccruing: D2189's own reading of the daily rate —
// the accrual stops the day a licence is revoked rather than at the end of a
// term that was never served — and the credit that frees.
//
// The licence is revoked through P5.1's own route and ten days into a month, so
// the three figures the case reads are all different: the whole term's 600_000
// before, the ten days served 200_000 after, and the same 200_000 twenty days
// later, which is the days the revocation took back.
func TestRevokedEntitlementStopsAccruing(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.operationsOrg(roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	sold := f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-stopped"), PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))
	if got := f.unpaid(resellerID); got != saleMicros {
		t.Fatalf("the month's sale accrues %d micros, want the whole term's %d", got, saleMicros)
	}

	// Ten days of it served, and the refund: operations revokes the licence.
	f.advance(10 * 24 * time.Hour)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	revoked := f.apiAsCaller(http.MethodPost, apiOpsEntitlementActionsPath,
		actionBody(t, sold.ID, operationsRevoke, "null", "null", "null"))
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoking the licence answered %d: %s", revoked.Code, revoked.Body)
	}
	if held := readAnswer[WireEntitlement](f, revoked); held.Status != entitlementRevoked {
		t.Fatalf("the licence is %q after the revocation, want %q", held.Status, entitlementRevoked)
	}
	// The stop is the revocation's own instant, and it is recorded: nothing else
	// in the store carries the day the licence stopped being served.
	stopped, err := f.svc.resellerStoppedAt(context.Background(), sold.ID)
	if err != nil {
		t.Fatalf("reading the stop of %s: %v", sold.ID, err)
	}
	if stopped == nil || !stopped.Equal(f.now) {
		t.Fatalf("the stop is %v, want the instant of the revocation %s", stopped, f.now)
	}

	// The ten days it served, and not the thirty it was sold for: 10 * 20 * 1000.
	served := int64(10) * int64(saleSeats) * resellerRate
	if got := f.unpaid(resellerID); got != served {
		t.Errorf("the accrual after the revocation is %d micros, want the ten days served %d", got, served)
	}
	// Twenty days later it is the same figure: the term went on the day the
	// licence stopped, and it is not charged for.
	f.advance(20 * 24 * time.Hour)
	if got := f.unpaid(resellerID); got != served {
		t.Errorf("the accrual twenty days after the revocation is %d micros, want the frozen %d: a revoked licence serves no day of the rest of its term", got, served)
	}

	// The credit the revocation freed is what the reseller may sell again:
	// 200_000 committed leaves exactly 4_800_000, which is 160 seats for a month.
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-freed"), PolicyTeamMonthly, 160, f.now.Add(saleDays*24*time.Hour))
	if got := f.unpaid(resellerID); got != resellerLimit {
		t.Fatalf("the sale that fills the freed credit leaves %d micros, want the limit's own %d", got, resellerLimit)
	}
	past := f.mintSale(bought.ID, f.soldWorkspace(bought.ID, "ws-past"), PolicyTeamMonthly, 161, f.now.Add(saleDays*24*time.Hour))
	if past.Code != http.StatusConflict || refusal(f, past) != "over_credit_limit" {
		t.Errorf("a sale one seat past the freed credit answered %d %s, want 409 over_credit_limit: %s",
			past.Code, refusal(f, past), past.Body)
	}
}

// TestAZeroCreditLimitMayNotMintAtAll: `resellers.schema.json`'s own zero — a
// reseller whose limit is nothing "may not mint at all" — and the reason it is a
// check of its own rather than one the comparison answers.
//
// A row priced at nothing costs nothing, so a sale of any size leaves the sum
// where it was and a comparison of the sum against the limit admits it: the one
// sale a limit of nothing exists to refuse is the one sale of a free row. The
// case is therefore a row priced at zero a day, which is a price operations may
// set (validResellerTerms admits a rate of zero), and a sale big enough that no
// reading of it is small.
func TestAZeroCreditLimitMayNotMintAtAll(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.seed(resellerTermsCollection, resellerID, WireReseller{
		OrganisationID:    resellerID,
		CreditLimitMicros: 0,
		Rates:             []WireResellerRate{{Row: PolicyTeamMonthly, DailyRateMicros: 0}},
		CreatedAt:         f.now,
	})
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	held := f.soldWorkspace(bought.ID, "ws-no-credit")

	rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, 500, f.now.Add(saleDays*24*time.Hour))
	if rec.Code != http.StatusConflict || refusal(f, rec) != "over_credit_limit" {
		t.Errorf("a sale by a reseller whose credit limit is nothing answered %d %s, want 409 over_credit_limit: %s",
			rec.Code, refusal(f, rec), rec.Body)
	}
	if licences := f.entitlementsOfOrganisation(bought.ID); len(licences) != 0 {
		t.Errorf("the refused sale left %d licence(s), want none: a limit of nothing is a reseller that may not mint at all", len(licences))
	}
}

// TestASupersededSaleStopsAccruing: a licence another grant replaces serves no
// day of the term it was granted for, and its seller is charged for the day it
// served and not for the overlap — the accrual's own rule (accrual.go), which a
// revocation's stop alone did not hold for the other way a licence stops
// serving.
//
// The same workspace is sold twice, a day apart, which is the renewal or the
// upgrade a reseller's customer asks for: the second grant replaces the licence
// serving there (D2204), so the two figures the case reads are the whole month
// 600_000 and the day the replaced licence served beside the second term —
// 20 * 1 * 1000 + 600_000.
func TestASupersededSaleStopsAccruing(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	workspace := f.soldWorkspace(bought.ID, "ws-superseded")
	first := f.saleOf(bought.ID, workspace, PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))
	if got := f.unpaid(resellerID); got != saleMicros {
		t.Fatalf("the month's sale accrues %d micros, want the whole term's %d", got, saleMicros)
	}

	// A day later the same workspace is sold again, and the licence that was
	// serving there is the one the second sale replaces.
	f.advance(24 * time.Hour)
	second := f.saleOf(bought.ID, workspace, PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))
	if second.ID == first.ID {
		t.Fatalf("the second sale wrote the record the first one did, %s", first.ID)
	}
	replaced, found := heldRecord[WireEntitlement](f, entitlementsCollection, first.ID)
	if !found || replaced.Status != entitlementSuperseded {
		t.Fatalf("the licence the second sale replaced is %q (%v), want the superseded record", replaced.Status, found)
	}
	// The stop is the instant the replacement began, and it is recorded: nothing
	// else in the store carries the day the replaced licence stopped serving.
	stopped, err := f.svc.resellerStoppedAt(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("reading the stop of %s: %v", first.ID, err)
	}
	if stopped == nil || !stopped.Equal(f.now) {
		t.Fatalf("the stop of the replaced licence is %v, want the instant of the sale that replaced it %s", stopped, f.now)
	}

	// The day it served, and not the month it was granted for.
	served := int64(saleSeats) * resellerRate
	if got := f.unpaid(resellerID); got != served+saleMicros {
		t.Errorf("the accrual is %d micros, want the day the replaced licence served beside the second sale's term %d",
			got, served+saleMicros)
	}
	// And it stays there as the days go by: the replaced licence is not charged
	// for the rest of the term nobody served.
	f.advance(saleDays * 24 * time.Hour)
	if got := f.unpaid(resellerID); got != served+saleMicros {
		t.Errorf("the accrual a month later is %d micros, want the frozen %d: a replaced licence serves no further day", got, served+saleMicros)
	}
}

// TestARenewalAtTheLimitIsNotRefused: the sum the limit is checked against is
// the one the sale leaves, and not the one that stands before it.
//
// A reseller's renewal is a sale into a workspace it already holds, and the
// grant it makes replaces and stops the licence serving there (D2204), so what
// the renewal really adds is one day of the licence it replaces plus the term it
// grants. Counted the other way — the whole term of the licence the renewal
// replaces beside the whole term the renewal grants — a reseller that has run up
// anything like its limit can never renew in place, which is the sale the
// renewal is.
//
// The figures are the boundary: 160 seats for the row's month is 4_800_000 of
// the 5_000_000 the reseller may run up, so the renewal a day later is admitted
// at 4_800_000 + 4_800_000 committed and 4_960_000 owed.
func TestARenewalAtTheLimitIsNotRefused(t *testing.T) {
	const renewalSeats = 160
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	workspace := f.soldWorkspace(bought.ID, "ws-renewed")
	committed := int64(renewalSeats) * saleDays * resellerRate
	if room := resellerLimit - committed; room != 200_000 {
		t.Fatalf("this case is about a sale that leaves 200_000 micros of the limit, and %d seats leaves %d: the figures moved", renewalSeats, room)
	}
	f.saleOf(bought.ID, workspace, PolicyTeamMonthly, renewalSeats, f.now.Add(saleDays*24*time.Hour))
	if got := f.unpaid(resellerID); got != committed {
		t.Fatalf("the month's sale accrues %d micros, want the whole term's %d", got, committed)
	}

	// The renewal, a day into the month: the licence it replaces served a day,
	// and that day is all it spends beside the term the renewal grants.
	f.advance(24 * time.Hour)
	f.saleOf(bought.ID, workspace, PolicyTeamMonthly, renewalSeats, f.now.Add(saleDays*24*time.Hour))
	served := int64(renewalSeats) * resellerRate
	if got := f.unpaid(resellerID); got != served+committed {
		t.Errorf("the accrual after the renewal is %d micros, want the day the replaced licence served beside the renewal's own term %d",
			got, served+committed)
	}
}

// TestConcurrentMintsCannotExceedTheLimit: D2189's ceiling under the one thing
// that can break it — sales arriving together. The sum is a read and the grant is
// a write beside it, and without the lock every request of the burst reads a sum
// that holds none of the others' sales and all of them are written.
//
// The window is widened at the sum's own first read (burstDocs), which is the
// read the write sits beside: the gate opens once the whole burst has arrived
// there, so a run without the lock is a run where all eight read the same stale
// figure rather than one a scheduler happened to order.
func TestConcurrentMintsCannotExceedTheLimit(t *testing.T) {
	const burst = 8
	const fits = 3
	f := newFixtureWith(t, Production, func(b *fixture) {
		b.deps.Docs = &burstDocs{
			burstCountDocs: newBurstCountDocs(b.docs, burst),
			gated:          []string{resellerCustomersCollection},
		}
	})
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	// A limit three of these sales fill exactly, so the fourth is the one that
	// would pass it.
	f.seed(resellerTermsCollection, resellerID, WireReseller{
		OrganisationID:    resellerID,
		CreditLimitMicros: fits * saleMicros,
		Rates:             []WireResellerRate{{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate}},
		CreatedAt:         f.now,
	})
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	workspaces := make([]string, 0, burst)
	for i := range burst {
		workspaces = append(workspaces, f.soldWorkspace(bought.ID, fmt.Sprintf("ws-burst-%d", i)))
	}

	ends := f.now.Add(saleDays * 24 * time.Hour)
	codes := together(burst, func(i int) *httptest.ResponseRecorder {
		return f.mintSale(bought.ID, workspaces[i], PolicyTeamMonthly, saleSeats, ends)
	})

	if got := answered(codes, http.StatusCreated); got != fits {
		t.Errorf("%d of %d concurrent sales were answered 201, want the %d the limit holds: %v", got, burst, fits, codes)
	}
	if got := answered(codes, http.StatusConflict); got != burst-fits {
		t.Errorf("%d of %d concurrent sales carried the limit's refusal, want %d: %v", got, burst, burst-fits, codes)
	}
	if held := f.entitlementsOfOrganisation(bought.ID); len(held) != fits {
		t.Errorf("the burst left %d licence(s), want the %d that fit: %+v", len(held), fits, held)
	}
	if got := f.unpaid(resellerID); got != fits*saleMicros {
		t.Errorf("the burst accrued %d micros, want the %d the limit holds", got, fits*saleMicros)
	}
}

// TestASaleIsTheCallersOwnCustomerAndNobodyElses: the gate and the tenant, which
// are the two things standing between a reseller and somebody else's
// organisation.
//
// The four refusals are the four a route that only checked "signed in" would
// admit: another reseller selling to a customer it does not hold, a plain member
// of the reseller deciding a sale, a customer's own owner, and the email-link
// sign-in a reseller organisation refuses (D2207, F10). The fifth is a workspace
// of another organisation named by a reseller that does hold the customer — the
// one way past the path's own check, and the one that would grant a term to an
// installation nobody sold anything to.
func TestASaleIsTheCallersOwnCustomerAndNobodyElses(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.resellerOrg(rivalID, "Rival Ltd", rivalUID, rivalMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	held := f.soldWorkspace(bought.ID, "ws-held")
	// A customer organisation this reseller does not hold, with a workspace of
	// its own: what a body could name if the workspace were trusted.
	other := f.seededOrganisation("org-stranger", "Stranger Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	stranger := f.soldWorkspace(other.ID, "ws-stranger")
	ends := f.now.Add(saleDays * 24 * time.Hour)

	// Another reseller's customer is not this reseller's to sell to, and the two
	// are told the same thing as an organisation nobody has.
	f.actsAs(rivalUID, rivalMail, signInGoogle)
	if rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, saleSeats, ends); rec.Code != http.StatusNotFound || refusal(f, rec) != "not_found" {
		t.Errorf("the rival's sale answered %d %s, want 404 not_found: %s", rec.Code, refusal(f, rec), rec.Body)
	}
	// A member of the reseller organisation reads its customers and decides
	// nothing (D2186, D2195).
	f.setRole(resellerID, sellerUID, roleMember)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, saleSeats, ends); rec.Code != http.StatusForbidden || refusal(f, rec) != "not_permitted" {
		t.Errorf("a member's sale answered %d %s, want 403 not_permitted: %s", rec.Code, refusal(f, rec), rec.Body)
	}
	// A reseller organisation's people sign in with Google or Microsoft: the
	// email link that is a fine way into a customer's own screens is refused
	// here (D2207, F10).
	f.actsAs(sellerUID, sellerMail, signInEmailLink)
	if rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, saleSeats, ends); rec.Code != http.StatusForbidden || refusal(f, rec) != "sso_required" {
		t.Errorf("the email-link sale answered %d %s, want 403 sso_required: %s", rec.Code, refusal(f, rec), rec.Body)
	}
	// An account that is a member of no reseller organisation at all.
	f.actsAs(buyerUID, buyerMail, signInGoogle)
	if rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, saleSeats, ends); rec.Code != http.StatusForbidden || refusal(f, rec) != "not_reseller" {
		t.Errorf("a stranger's sale answered %d %s, want 403 not_reseller: %s", rec.Code, refusal(f, rec), rec.Body)
	}

	// The reseller's own customer, and a workspace that is not that customer's:
	// refused, and nothing is written.
	f.setRole(resellerID, sellerUID, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if rec := f.mintSale(bought.ID, stranger, PolicyTeamMonthly, saleSeats, ends); rec.Code != http.StatusNotFound || refusal(f, rec) != "not_found" {
		t.Errorf("a sale into another organisation's workspace answered %d %s, want 404 not_found: %s", rec.Code, refusal(f, rec), rec.Body)
	}
	// A row the policy table does not carry, and one this reseller has no price
	// for: a missing price is not a price of zero (resellers.schema.json).
	for _, row := range []string{"bogus", PolicyEnterprise} {
		rec := f.mintSale(bought.ID, held, row, 1, ends)
		if rec.Code != http.StatusUnprocessableEntity || refusal(f, rec) != "invalid_row" {
			t.Errorf("a sale on the %q row answered %d %s, want 422 invalid_row: %s", row, rec.Code, refusal(f, rec), rec.Body)
		}
	}
	// The term is the row's own bound (F30), as it is at P5.1's own mint.
	if rec := f.mintSale(bought.ID, held, PolicyTeamMonthly, saleSeats, f.now.Add(31*24*time.Hour)); rec.Code != http.StatusUnprocessableEntity || refusal(f, rec) != "invalid_term" {
		t.Errorf("a sale past the row's own term answered %d %s, want 422 invalid_term: %s", rec.Code, refusal(f, rec), rec.Body)
	}
	if held := f.entitlementsOfOrganisation(bought.ID); len(held) != 0 {
		t.Errorf("the refused sales left %d licence(s): none of them was a sale", len(held))
	}
}
