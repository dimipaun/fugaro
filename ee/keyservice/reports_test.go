// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The cases for the monthly sales reports (portal plan P5.2; spec §Operations,
// D2190).
//
// Every case reads the report through the route the operations screen calls
// (`GET /api/ops/reports?month=…`, P5.3's `api.ts`), because that is the whole of
// what a report is to a caller: one object, one month, two channels.
//
// The months are named by the case and the fixture's clock is moved to them
// (f.now), which is what makes the figures arithmetic a reader can check: a
// report that read the clock's own month, or a month off by one, is a case that
// fails rather than a case that agrees with itself. The direct half is driven
// through the merchant's own door (P4.2) and the reseller half through a
// reseller's sale and the statement run that bills it (P6.2, P6.3), so nothing
// here is summed over a fixture the report also reads.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

const (
	// The month the cases below are about, and the instant inside it that the
	// sales are made at.
	reportMonth = "2026-05"
	// What one purchase of the fixture's own body leaves in the ledger: the
	// body states a grand total of "12000" in minor units, which is 120000000
	// micro-dollars (`notificationBody`, weight 10000).
	reportSaleMicros = int64(120_000_000)
)

// reportOf reads one month's report the way the operations screen does, as the
// caller the fixture is signed in as.
func (f *fixture) reportOf(month string) WireReportMonth {
	f.t.Helper()
	rec := f.apiAsCaller(http.MethodGet, apiOpsReportsPath+"?month="+month, "")
	if rec.Code != http.StatusOK {
		f.t.Fatalf("the report of %q answered %d: %s", month, rec.Code, rec.Body)
	}
	return readAnswer[WireReportMonth](f, rec)
}

// reportCustomer answers one customer's line in one report, and fails the case
// when the month holds none: an absent line and a line of zero are two
// different readings of a month (reports.schema.json promises every customer the
// month had a line for), and a case that could not tell them apart would pass on
// either.
func reportCustomer(t *testing.T, report WireReportMonth, organisationID, channel string) WireReportCustomer {
	t.Helper()
	for _, customer := range report.Customers {
		if customer.OrganisationID == organisationID && customer.Channel == channel {
			return customer
		}
	}
	t.Fatalf("the report of %s holds no %s line for %s: %+v", report.Month, channel, organisationID, report.Customers)
	return WireReportCustomer{}
}

// reportAbsent fails the case when a report holds a line for one organisation at
// all, whichever channel it is on.
func reportAbsent(t *testing.T, report WireReportMonth, organisationID string) {
	t.Helper()
	for _, customer := range report.Customers {
		if customer.OrganisationID == organisationID {
			t.Fatalf("the report of %s holds the line %+v for %s, want the customer out of that month",
				report.Month, customer, organisationID)
		}
	}
}

// reportNoLine fails the case when one organisation appears in one channel of a
// report, which is what a split case reads: the customer is a line of the month,
// and which of the two halves it is in is the whole of the assertion.
func reportNoLine(t *testing.T, report WireReportMonth, organisationID, channel string) {
	t.Helper()
	for _, customer := range report.Customers {
		if customer.OrganisationID == organisationID && customer.Channel == channel {
			t.Fatalf("the %s report holds the %s line %+v for %s, want the customer in the other channel only",
				report.Month, channel, customer, organisationID)
		}
	}
}

// reportLinesTotal is one channel's lines added up, which is what the month's
// own figure has to be: a total that did not come to the customers behind it is
// a figure nobody can check.
func reportLinesTotal(report WireReportMonth, channel string) int64 {
	total := int64(0)
	for _, customer := range report.Customers {
		if customer.Channel == channel {
			total += customer.AmountMicros
		}
	}
	return total
}

// reportDelivery delivers one purchase whose `occurred_at` is written with the
// offset named rather than in UTC, which is what the merchant's own clock
// produces: the instant is `at`, and how the body spells it is the case's own
// business (TestReportMonthsAreUTC).
func (f *fixture) reportDelivery(e Event, at time.Time, occurredAt string) {
	f.t.Helper()
	e.OccurredAt = at
	e = f.claimsOf(e)
	body := reportBodyAt(f.t, e, occurredAt)
	rec := f.deliverRaw(body, signEvent(f.secret, f.now, body))
	if rec.Code != http.StatusOK {
		f.t.Fatalf("the purchase %s was answered %d, want 200: %s", e.ID, rec.Code, rec.Body)
	}
}

// reportBodyAt is one notification body with its `occurred_at` replaced: the
// fixture's own body writes the instant in UTC, so a case about the offset a
// body may carry states the offset itself rather than letting the fixture
// normalize it away.
func reportBodyAt(t *testing.T, e Event, occurredAt string) []byte {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(notificationBody(t, e, ""), &body); err != nil {
		t.Fatalf("decoding the notification %s: %v", e.ID, err)
	}
	body["occurred_at"] = occurredAt
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the notification %s: %v", e.ID, err)
	}
	return encoded
}

// reportDirectSale is one customer buying from Fugaro in one month, and answers
// the organisation it was bought by. The purchase is delivered through the
// merchant's own door, so the ledger's line for it is written by the path a real
// sale takes and not seeded by the case.
//
// The buyer's organisation is named as a domain (`acme.example`) because the
// address the licence is sent to is the buyer's own (the fixture's
// buyerAddress) and a purchase whose address no licence can be sent to is
// refused rather than booked: a name that is not one would leave this case
// asserting over a sale the merchant never made.
func (f *fixture) reportDirectSale(t *testing.T, id, name, workspace string, at time.Time) WireOrganisation {
	t.Helper()
	buyer := f.seededOrganisation(id+".example", name, orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	f.deliver(Event{
		ID: id, Type: EventPurchase, OccurredAt: at,
		Organisation: buyer.ID, WorkspaceID: workspace,
		Email: buyerAddress(buyer.ID),
		Seats: 5, TermDays: 365,
	})
	return buyer
}

// TestReportSplitsDirectAndReseller: the report is the month's sales split by
// channel (spec §Operations), and the two halves are read apart because the two
// are settled differently (D2190): what the customer bought from Fugaro is the
// ledger's own line, and what was sold through a reseller is the statement the
// month has already billed.
//
// One customer of each, in one month, so that every assertion is about the
// channel and not about which customer: the direct figure is the purchase's own
// amount and the reseller figure is the May statement's accrual, and neither is
// derived from the other.
func TestReportSplitsDirectAndReseller(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	buyer := f.reportDirectSale(t, "acme", "Acme Ltd", "ws-acme", f.now)

	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.priceRows(resellerID, statementsCap, WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate})
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	sold := f.soldBy("Borealis Inc")
	f.saleOf(sold.ID, f.soldWorkspace(sold.ID, "ws-sold"), PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))

	// The run that states May is the one in June, and the month that is over is
	// the month the report is asked for.
	f.now = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	statement := f.runAndStatement(reportMonth)

	f.operationsOrg(roleOwner)
	report := f.reportOf(reportMonth)

	if report.Month != reportMonth {
		t.Errorf("the report is for %q, want the month it was asked for (%q)", report.Month, reportMonth)
	}
	if len(report.Customers) != 2 {
		t.Fatalf("the report holds %d customer line(s), want the direct buyer and the reseller's customer: %+v",
			len(report.Customers), report.Customers)
	}
	direct := reportCustomer(t, report, buyer.ID, channelDirect)
	if direct.Name != "Acme Ltd" {
		t.Errorf("the direct line is drawn as %q, want the organisation's name", direct.Name)
	}
	if direct.AmountMicros != reportSaleMicros {
		t.Errorf("the direct line is %d micro-dollars, want the purchase's own %d", direct.AmountMicros, reportSaleMicros)
	}
	reseller := reportCustomer(t, report, sold.ID, channelReseller)
	if reseller.Name != "Borealis Inc" {
		t.Errorf("the reseller line is drawn as %q, want the customer organisation's name", reseller.Name)
	}
	if reseller.AmountMicros != statement.AccruedMicros {
		t.Errorf("the reseller line is %d micro-dollars, want the May statement's %d",
			reseller.AmountMicros, statement.AccruedMicros)
	}
	if report.DirectMicros != reportSaleMicros {
		t.Errorf("the month's direct figure is %d micro-dollars, want the purchase alone (%d)",
			report.DirectMicros, reportSaleMicros)
	}
	if report.ResellerMicros != statement.AccruedMicros {
		t.Errorf("the month's reseller figure is %d micro-dollars, want the May statement's %d",
			report.ResellerMicros, statement.AccruedMicros)
	}
	// The customer who bought from Fugaro is not in the reseller half, and the
	// customer sold through a reseller is not in the direct one: the split is
	// the month's and a customer cannot be in both halves of it.
	reportNoLine(t, report, sold.ID, channelDirect)
	reportNoLine(t, report, buyer.ID, channelReseller)
	if got := reportLinesTotal(report, channelDirect); got != report.DirectMicros {
		t.Errorf("the direct lines add up to %d micro-dollars, want the month's own figure %d", got, report.DirectMicros)
	}
	if got := reportLinesTotal(report, channelReseller); got != report.ResellerMicros {
		t.Errorf("the reseller lines add up to %d micro-dollars, want the month's own figure %d", got, report.ResellerMicros)
	}
}

// TestReportMonthsAreUTC: a month boundary is UTC (P5.2), and the instants a
// report places are the merchant's own — a Paddle body states its
// `occurred_at` with an offset of its clock, so a month read from the body's own
// location is a month the merchant chose and not the one the report is keyed by.
//
// The sale below is the same instant written two ways: half past midnight on the
// first of March in a zone two hours ahead is half past ten at night on the last
// day of February in UTC, and the month it belongs to is February. The second
// sale is the first instant of March in UTC, which belongs to March and to
// nothing before it.
func TestReportMonthsAreUTC(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	f.reportDelivery(Event{
		ID: "evt-feb", Type: EventPurchase, Organisation: "feb.example", WorkspaceID: "ws-feb",
		Email: buyerAddress("feb.example"), Seats: 5, TermDays: 365,
	}, time.Date(2026, 2, 28, 22, 30, 0, 0, time.UTC), "2026-03-01T00:30:00+02:00")
	f.reportDelivery(Event{
		ID: "evt-mar", Type: EventPurchase, Organisation: "mar.example", WorkspaceID: "ws-mar",
		Email: buyerAddress("mar.example"), Seats: 5, TermDays: 365,
	}, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "2026-03-01T00:00:00Z")

	// The instant the report reads is the one the body stated, and that body's
	// own location is March: a report that placed a line by the location it
	// arrived in would put this sale in the month the merchant spelled, which is
	// what the assertions below refuse.
	held := f.ledger(t)
	var boundary WireLedgerEntry
	for _, entry := range held {
		if entry.ID == "evt-feb" {
			boundary = entry
		}
	}
	if boundary.ID == "" {
		t.Fatalf("the ledger holds no line for evt-feb: %+v", held)
	}
	if boundary.At.Month() != time.March {
		t.Fatalf("the fixture's own body states %s, which is not the offset this case is about", boundary.At)
	}
	if boundary.At.UTC().Month() != time.February {
		t.Fatalf("the fixture's own body states the instant %s, which is not in February in UTC", boundary.At)
	}

	f.operationsOrg(roleOwner)
	february := f.reportOf("2026-02")
	march := f.reportOf("2026-03")

	if february.DirectMicros != reportSaleMicros {
		t.Errorf("February holds %d micro-dollars, want the sale the body stated at 22:30 UTC on its last day (%d)",
			february.DirectMicros, reportSaleMicros)
	}
	reportCustomer(t, february, "feb.example", channelDirect)
	reportAbsent(t, february, "mar.example")

	if march.DirectMicros != reportSaleMicros {
		t.Errorf("March holds %d micro-dollars, want the sale at the first instant of it (%d)",
			march.DirectMicros, reportSaleMicros)
	}
	reportCustomer(t, march, "mar.example", channelDirect)
	reportAbsent(t, march, "feb.example")
}

// TestCompedKeysAreNotRevenue: a grant is not a sale (D2199, D2203). The free
// seat every sign-up is given and a trial nobody paid for are terms the month
// granted and never money the month took, so neither moves either channel's
// figure — and the customer is still a line, with an amount of zero, because a
// report read as the month's customers is the one operations reconciles against.
func TestCompedKeysAreNotRevenue(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	comped := f.signedUp("uid-comped", "comped@example.com", "Comped Ltd")

	trial := f.seededOrganisation("org-trial", "Trial Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	if _, err := f.svc.grantEntitlement(context.Background(), entitlementGrant{
		At: f.now, OrganisationID: trial.ID, Source: entitlementSourceTrial,
		Row: PolicyTrial, Seats: 1, StartsAt: f.now,
	}); err != nil {
		t.Fatalf("granting the trial this case reads: %v", err)
	}

	buyer := f.reportDirectSale(t, "acme", "Acme Ltd", "ws-acme", f.now)

	f.operationsOrg(roleOwner)
	report := f.reportOf(reportMonth)

	if len(report.Customers) != 3 {
		t.Fatalf("the report holds %d customer line(s), want the two grants and the one buyer: %+v",
			len(report.Customers), report.Customers)
	}
	for _, granted := range []WireOrganisation{comped, trial} {
		line := reportCustomer(t, report, granted.ID, channelDirect)
		if line.Name != granted.Name {
			t.Errorf("%s's line is drawn as %q, want the organisation's name", granted.ID, line.Name)
		}
		if line.AmountMicros != 0 {
			t.Errorf("%s is %d micro-dollars of revenue, want none: a grant is not a sale (D2199)",
				granted.ID, line.AmountMicros)
		}
	}
	if report.DirectMicros != reportSaleMicros {
		t.Errorf("the month's direct figure is %d micro-dollars, want the one purchase (%d): a comped key is not revenue",
			report.DirectMicros, reportSaleMicros)
	}
	if report.ResellerMicros != 0 {
		t.Errorf("the month's reseller figure is %d micro-dollars, want none: nothing was sold through a reseller",
			report.ResellerMicros)
	}
	if bought := reportCustomer(t, report, buyer.ID, channelDirect); bought.AmountMicros != reportSaleMicros {
		t.Errorf("the buyer's line is %d micro-dollars, want the purchase's own %d", bought.AmountMicros, reportSaleMicros)
	}
}

// TestResellerReceivableMatchesItsStatements: the reseller half of a report is
// the month's statements' own reading and not a second accrual computed beside
// them (D2190). The figure on the month is their total, and every customer's
// line is that statement's line for that customer — so a reseller reconciling a
// month against the report has nothing to reconcile.
//
// Two customers at two sizes, so the month is a sum of lines and not one
// customer's number written twice.
func TestResellerReceivableMatchesItsStatements(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.priceRows(resellerID, statementsCap, WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate})
	f.actsAs(sellerUID, sellerMail, signInGoogle)

	long := f.soldBy("Acme Ltd")
	f.saleOf(long.ID, f.soldWorkspace(long.ID, "ws-long"), PolicyTeamMonthly, saleSeats, f.now.Add(saleDays*24*time.Hour))
	short := f.soldBy("Borealis Inc")
	f.saleOf(short.ID, f.soldWorkspace(short.ID, "ws-short"), PolicyTeamMonthly, 5, f.now.Add(saleDays*24*time.Hour))

	f.now = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	statement := f.runAndStatement(reportMonth)
	if len(statement.Lines) != 2 {
		t.Fatalf("the statement carries %d line(s), want one per customer: %+v", len(statement.Lines), statement.Lines)
	}

	f.operationsOrg(roleOwner)
	report := f.reportOf(reportMonth)

	if report.ResellerMicros != statement.AccruedMicros {
		t.Errorf("the month's reseller figure is %d micro-dollars, want the statement's accrual %d",
			report.ResellerMicros, statement.AccruedMicros)
	}
	if got := reportLinesTotal(report, channelReseller); got != statement.AccruedMicros {
		t.Errorf("the reseller lines add up to %d micro-dollars, want the statement's accrual %d",
			got, statement.AccruedMicros)
	}
	for _, line := range statement.Lines {
		customer := reportCustomer(t, report, line.OrganisationID, channelReseller)
		if customer.AmountMicros != line.AmountMicros {
			t.Errorf("the line for %s is %d micro-dollars, want the statement's own %d",
				line.OrganisationID, customer.AmountMicros, line.AmountMicros)
		}
	}
	// The month before holds none of it: a report is the month it was asked for,
	// and the accrual that was billed in May is not April's revenue.
	previous := f.reportOf("2026-04")
	if previous.ResellerMicros != 0 || len(previous.Customers) != 0 {
		t.Errorf("April holds %d micro-dollars through resellers and %d line(s), want an empty month: %+v",
			previous.ResellerMicros, len(previous.Customers), previous.Customers)
	}
}

// TestReportRefusesNonOperations: the report is the operations team's read and
// nobody else's (D2187, D2188). A customer organisation's own owner is refused
// however they ask — the capability is not a field of a request — and a request
// with no sign-in at all is refused by the door.
func TestReportRefusesNonOperations(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	f.signedUp("uid-comped", "comped@example.com", "Comped Ltd")

	asked := apiOpsReportsPath + "?month=" + reportMonth
	refused := f.apiAsCaller(http.MethodGet, asked, "")
	if refused.Code != http.StatusForbidden || refusal(f, refused) != "not_operations" {
		t.Errorf("a customer's own owner reading the report got %d (%s), want a 403 not_operations",
			refused.Code, refused.Body)
	}
	if anonymous := f.api(http.MethodGet, asked, ""); anonymous.Code != http.StatusUnauthorized {
		t.Errorf("a request with no sign-in got %d, want %d: the report is behind the portal's own door",
			anonymous.Code, http.StatusUnauthorized)
	}

	f.operationsOrg(roleOwner)
	for _, month := range []string{"", "May", "2026-13", "2026-5"} {
		rec := f.apiAsCaller(http.MethodGet, apiOpsReportsPath+"?month="+month, "")
		if rec.Code != http.StatusUnprocessableEntity || refusal(f, rec) != "invalid_query" {
			t.Errorf("the month %q was answered %d (%s), want a 422 invalid_query: a month nobody bought in and a month spelled wrong are two answers",
				month, rec.Code, rec.Body)
		}
	}
}
