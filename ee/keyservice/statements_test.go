// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The cases for the monthly reseller statements (portal plan P6.3; D2189,
// D2190).
//
// Every case starts from the reseller `resellers_test.go` seeds and sells
// through the reseller's own route (accrual_test.go's `saleOf`), because the
// records a statement reads are the licences that route wrote: a statement
// summed over fixtures would be a case about the fixture.
//
// The months are read as arithmetic on purpose. The fixture's clock is the 10th
// of November, so a run at it states October; the cases move the clock to the
// day a licence was sold on, which is what puts its days inside or outside the
// month the run is about.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// The month a run at the fixture's own clock states, and the accrual of the
	// sale the cases are built from: twenty seats of the monthly row for a whole
	// October, which is 30 days * 20 seats * resellerRate micros.
	statedMonth  = "2026-10"
	statedMicros = int64(30) * int64(saleSeats) * resellerRate
	// statementsCap is a credit limit an accrual of a year fits under: the
	// seeded one is a single month's.
	statementsCap = int64(100_000_000)
)

// priceRows writes one reseller's terms straight into the store: the statement
// cases need a limit their sales fit under and a price for every row they sell
// on, and operations' own route writes one row at a time.
func (f *fixture) priceRows(organisationID string, limit int64, rates ...WireResellerRate) {
	f.t.Helper()
	f.seed(resellerTermsCollection, organisationID, WireReseller{
		OrganisationID:    organisationID,
		CreditLimitMicros: limit,
		Rates:             rates,
		CreatedAt:         f.now,
	})
}

// runStatementJob posts one statement run the way the deployment's monthly job
// does. The token is the placeholder the reminder cases present: the fixture's
// scheduler seam is a spy, and what it answers is the account.
func (f *fixture) runStatementJob() *httptest.ResponseRecorder {
	f.t.Helper()
	return f.apiAsJob(apiStatementsRunPath, reminderJobToken)
}

// runAndRead runs one job and answers the month the caller's reseller holds,
// which every case below is about: a run that failed would otherwise be read as
// a store that never issued a statement at all.
func (f *fixture) runAndRead() WireStatement {
	f.t.Helper()
	if rec := f.runStatementJob(); rec.Code != http.StatusNoContent {
		f.t.Fatalf("the statement run answered %d: %s", rec.Code, rec.Body)
	}
	held := f.statementsOf()
	if len(held) != 1 {
		f.t.Fatalf("the run left %d statement(s), want the one it issued: %v", len(held), held)
	}
	return held[0]
}

// statementsOf reads the caller's own statements through the route the
// reseller's screen calls (P6.4).
func (f *fixture) statementsOf() []WireStatement {
	f.t.Helper()
	rec := f.apiAsCaller(http.MethodGet, apiStatementsPath, "")
	if rec.Code != http.StatusOK {
		f.t.Fatalf("the statements read answered %d: %s", rec.Code, rec.Body)
	}
	return readAnswer[[]WireStatement](f, rec)
}

// statementPaymentPathOf is the route a payment is recorded against one
// statement on: the statement's own id is in the path and is not a field of the
// body.
func statementPaymentPathOf(statementID string) string {
	return strings.Replace(apiStatementPaymentsPath, "{id}", statementID, 1)
}

// paymentBody is one `statements.payment` body as operations sends it: every
// field of the shape, because the wire refuses a body that leaves one out.
func paymentBody(t *testing.T, amount int64, paidAt time.Time, reference string) string {
	t.Helper()
	return `{"amount_micros":` + strconv.FormatInt(amount, 10) +
		`,"paid_at":` + jsonString(t, paidAt.UTC().Format(time.RFC3339)) +
		`,"reference":` + jsonString(t, reference) + `}`
}

// recordPayment posts one payment as operations is signed in and answers the
// statement: every case that carries on to read the figure it moved asks this.
func (f *fixture) recordPayment(statementID, body string) WireStatement {
	f.t.Helper()
	rec := f.apiAsCaller(http.MethodPost, statementPaymentPathOf(statementID), body)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("recording a payment against %s answered %d: %s", statementID, rec.Code, rec.Body)
	}
	return readAnswer[WireStatement](f, rec)
}

// TestStatementSumsTheMonth: a statement is the month's accrual, and the month
// is the whole of what it is (D2189, D2190).
//
// Four licences are sold across four months so that every edge is a different
// answer: one running through October from September, one that ends in October,
// one that starts in it, and one that was over before it began. The figures are
// days times seats times the rate over the days inside October — 620_000,
// 180_000, 60_000 and no line at all — and the whole-term sum beside them is
// 8_350_000, which is the assertion that a statement bills the month and not the
// terms behind it.
func TestStatementSumsTheMonth(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.priceRows(resellerID, statementsCap,
		WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate},
		WireResellerRate{Row: PolicyTeamAnnual, DailyRateMicros: resellerRate})
	f.actsAs(sellerUID, sellerMail, signInGoogle)

	// A sale that started in September and runs for a year: October's thirty-one
	// days of it are 31 * 20 * 1000.
	f.now = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	long := f.soldBy("Acme Ltd")
	f.saleOf(long.ID, f.soldWorkspace(long.ID, "ws-long"), PolicyTeamAnnual, saleSeats, f.now.AddDate(1, 0, 0))

	// One that ends inside October, on the tenth: the month's first nine days of
	// it are 9 * 20 * 1000.
	f.now = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	f.saleOf(long.ID, f.soldWorkspace(long.ID, "ws-ends"), PolicyTeamMonthly, saleSeats, f.now.Add(30*24*time.Hour))

	// One that starts inside it, on the twentieth: 12 days * 5 seats * 1000.
	f.now = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	short := f.soldBy("Borealis Inc")
	f.saleOf(short.ID, f.soldWorkspace(short.ID, "ws-starts"), PolicyTeamMonthly, 5, f.now.Add(30*24*time.Hour))

	// One that was over before October began, which is no line at all.
	f.now = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	over := f.soldBy("Cinder Ltd")
	f.saleOf(over.ID, f.soldWorkspace(over.ID, "ws-over"), PolicyTeamMonthly, 10, f.now.Add(30*24*time.Hour))

	// The run's own instant, which is the month after the one it states.
	f.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	statement := f.runAndRead()

	if statement.Month != statedMonth {
		t.Errorf("the statement is for %q, want %q: a run states the month that is over", statement.Month, statedMonth)
	}
	if statement.ResellerOrganisationID != resellerID {
		t.Errorf("the statement is addressed to %q, want the reseller %q", statement.ResellerOrganisationID, resellerID)
	}
	if statement.ID != statementID(resellerID, statedMonth) {
		t.Errorf("the statement's id is %q, want the reseller and the month %q", statement.ID, statementID(resellerID, statedMonth))
	}
	if !statement.IssuedAt.Equal(f.now) {
		t.Errorf("the statement was issued at %s, want the run's own instant %s", statement.IssuedAt, f.now)
	}
	if statement.State != statementOpen || statement.PaidMicros != 0 || statement.PaidAt != nil {
		t.Errorf("a statement nothing has been paid against is %q with %d micros paid and %v, want open, none and null",
			statement.State, statement.PaidMicros, statement.PaidAt)
	}

	// The month's own arithmetic: Acme's two licences at 620_000 and 180_000,
	// Borealis's at 60_000, and nothing for the customer whose licence was over.
	want := map[string]int64{
		long.ID:  int64(31)*int64(saleSeats)*resellerRate + int64(9)*int64(saleSeats)*resellerRate,
		short.ID: int64(12) * int64(5) * resellerRate,
	}
	if len(statement.Lines) != len(want) {
		t.Fatalf("the statement has %d line(s) %v, want %d: one per customer the month accrued for",
			len(statement.Lines), statement.Lines, len(want))
	}
	if got, expected := statement.AccruedMicros, want[long.ID]+want[short.ID]; got != expected {
		t.Errorf("the statement accrued %d micros, want the lines' own %d", got, expected)
	}
	previous := ""
	for _, line := range statement.Lines {
		amount, known := want[line.OrganisationID]
		if !known {
			t.Errorf("the statement has a line for %s of %d micros, want no line for a customer the month accrued nothing for",
				line.OrganisationID, line.AmountMicros)
			continue
		}
		if line.AmountMicros != amount {
			t.Errorf("the line for %s is %d micros, want %d", line.OrganisationID, line.AmountMicros, amount)
		}
		if previous != "" && line.OrganisationID < previous {
			t.Errorf("the lines run %s then %s, want the customers' own order: a reseller reading two months reads one order in both",
				previous, line.OrganisationID)
		}
		previous = line.OrganisationID
	}

	// The whole term is a different figure, which is what "the month and not the
	// term" means: a year of Acme's long sale is 365 days, and the August and
	// September days of the others are in this sum too.
	whole := int64(365)*int64(saleSeats)*resellerRate +
		int64(30)*int64(saleSeats)*resellerRate +
		int64(30)*int64(5)*resellerRate +
		int64(30)*int64(10)*resellerRate
	if got := f.unpaid(resellerID); got != whole {
		t.Fatalf("the whole-term accrual is %d micros, want the case's own %d", got, whole)
	}
	if statement.AccruedMicros == whole {
		t.Errorf("the statement accrued the whole term's %d micros: a statement bills the days inside its month and not the terms behind them", whole)
	}
}

// TestStatementMonthsSumToTheTerm: the months a term is billed over add up to
// the term, wherever in the day it was sold (D2189).
//
// A term starts at the instant it was sold and almost no sale happens at
// midnight, so the part day a term starts on is one of its days. Pricing each
// month by clipping the term to the month's own window rounds that day up in the
// month of the sale and the part day at the far end up in the last month, which
// bills a year sold at noon as 366 days of 365 — every sale, in both directions.
//
// The case sells one seat for exactly a year from noon on the 15th of September,
// so the term touches thirteen months, and runs the job for every one of them,
// which is what the deployment does over a year. The assertions are the two
// edges a rounding rule shows up at — September's sixteen days, the last
// September's fourteen — and the sum against the whole term the credit limit
// reads.
func TestStatementMonthsSumToTheTerm(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.priceRows(resellerID, statementsCap,
		WireResellerRate{Row: PolicyTeamAnnual, DailyRateMicros: resellerRate})
	f.actsAs(sellerUID, sellerMail, signInGoogle)

	// One seat for a year from noon, which is 365 days: the first of them is the
	// day it was sold on and the last the day it runs out on.
	sold := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	f.now = sold
	bought := f.soldBy("Acme Ltd")
	f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-year"), PolicyTeamAnnual, 1, sold.AddDate(1, 0, 0))
	whole := f.unpaid(resellerID)
	if want := int64(365) * resellerRate; whole != want {
		t.Fatalf("the year's whole accrual is %d micros, want its own %d", whole, want)
	}

	// A run states the month before its own, so the thirteen months from the
	// month of the sale to the month the term runs out in are each stated by a
	// run in the month after them.
	for run := sold.AddDate(0, 1, 0); !run.After(sold.AddDate(1, 1, 0)); run = run.AddDate(0, 1, 0) {
		f.now = run
		if rec := f.runStatementJob(); rec.Code != http.StatusNoContent {
			t.Fatalf("the statement run at %s answered %d: %s", run, rec.Code, rec.Body)
		}
	}

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	months := f.statementsOf()
	if len(months) != 13 {
		t.Fatalf("a year from September touches thirteen months and left %d statement(s): %v", len(months), months)
	}

	sum := int64(0)
	for _, statement := range months {
		sum += statement.AccruedMicros
	}
	if sum != whole {
		t.Errorf("the term's statements accrued %d micros between them, want the term's own %d: a part day at either end belongs to the term once",
			sum, whole)
	}

	// The term's own two ends, which is where the rounding shows: noon on the
	// 15th of September is fifteen and a half days of it, and the year that
	// starts there leaves fourteen and a half days of the September it ends in.
	if newest := months[0]; newest.Month != "2027-09" || newest.AccruedMicros != int64(14)*resellerRate {
		t.Errorf("the term's last month is %q of %d micros, want 2027-09 of the %d its last fourteen days come to",
			newest.Month, newest.AccruedMicros, int64(14)*resellerRate)
	}
	if oldest := months[len(months)-1]; oldest.Month != "2026-09" || oldest.AccruedMicros != int64(16)*resellerRate {
		t.Errorf("the term's first month is %q of %d micros, want 2026-09 of the %d its first sixteen days come to",
			oldest.Month, oldest.AccruedMicros, int64(16)*resellerRate)
	}
}

// TestStatementMonthIsTheMonthBeforeTheRun: the run states the previous UTC
// month, whatever day of the month it fires on.
//
// The case runs on the 31st of March, the day that catches a month subtracted
// from the run's own day: the 31st of March less a month normalises to the 3rd
// of March, so a statement read off that day would bill February over three days
// of it. The window's own edges are asserted beside it, because the days a
// licence is priced over are the window's and not the run's.
func TestStatementMonthIsTheMonthBeforeTheRun(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)

	// One seat of the monthly row for a whole February, which is 28 days.
	f.now = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	bought := f.soldBy("Acme Ltd")
	f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-february"), PolicyTeamMonthly, 1, f.now.Add(30*24*time.Hour))

	f.now = time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	statement := f.runAndRead()

	if statement.Month != "2026-02" {
		t.Errorf("a run on the 31st of March states %q, want %q: the month before it, taken from the first of the month",
			statement.Month, "2026-02")
	}
	if want := int64(28) * resellerRate; statement.AccruedMicros != want {
		t.Errorf("February accrued %d micros, want the %d its own days come to", statement.AccruedMicros, want)
	}

	month, start, end := statementMonthWindow(f.now)
	if month != "2026-02" ||
		!start.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) ||
		!end.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the window of a run on the 31st of March is %s from %s to %s, want 2026-02 from the 1st of February to the 1st of March",
			month, start, end)
	}
}

// TestStatementRunIsIdempotent: a month is issued once, and a second run is
// answered with the statement the first one wrote.
//
// The figures a payment moved are the assertion: the run asks the store for the
// month's own id (statementID), so what a retry reads is the statement that
// exists — the money recorded against it, the instant it was issued at and the
// mail that already went out all stand, where a run that wrote a second record
// would bill the reseller twice and lose what it has paid.
func TestStatementRunIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	f.now = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	bought := f.soldBy("Acme Ltd")
	f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-october"), PolicyTeamMonthly, saleSeats, f.now.Add(30*24*time.Hour))

	f.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	first := f.runAndRead()
	if first.AccruedMicros != statedMicros {
		t.Fatalf("the statement accrued %d micros, want the month's own %d", first.AccruedMicros, statedMicros)
	}
	if mailed := len(f.mail.sent); mailed != 1 {
		t.Fatalf("the run mailed %d message(s), want the owner's one", mailed)
	}
	if msg := f.mail.sent[0]; msg.To != sellerMail || msg.Subject != statementSubject || !strings.Contains(msg.Body, "/reseller") {
		t.Errorf("the statement mail is to %q about %q:\n%s", msg.To, msg.Subject, msg.Body)
	}

	// A part payment, recorded by operations, which is what a second run must
	// not undo.
	f.operationsOrg(roleOwner)
	part := f.recordPayment(first.ID, paymentBody(t, statedMicros/6, f.now, "INV-2026-10"))
	if part.PaidMicros != statedMicros/6 || part.State != statementOpen {
		t.Fatalf("the statement after a part payment is %q with %d micros paid, want open with %d",
			part.State, part.PaidMicros, statedMicros/6)
	}
	mailed := len(f.mail.sent)

	// The same job again, a day later: the statement that exists is the answer,
	// and nothing about it moves.
	f.advance(24 * time.Hour)
	if rec := f.runStatementJob(); rec.Code != http.StatusNoContent {
		t.Fatalf("the second statement run answered %d: %s", rec.Code, rec.Body)
	}
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	held := f.statementsOf()
	if len(held) != 1 {
		t.Fatalf("two runs left %d statement(s), want the one the first issued", len(held))
	}
	second := held[0]
	if second.ID != first.ID || !second.IssuedAt.Equal(first.IssuedAt) {
		t.Errorf("the second run answered %s issued at %s, want %s issued at %s: a month is issued once",
			second.ID, second.IssuedAt, first.ID, first.IssuedAt)
	}
	if second.AccruedMicros != first.AccruedMicros || second.PaidMicros != part.PaidMicros || second.State != statementOpen {
		t.Errorf("the statement after the second run is %q with %d accrued and %d paid, want the figures the payment left",
			second.State, second.AccruedMicros, second.PaidMicros)
	}
	if len(f.mail.sent) != mailed {
		t.Errorf("the second run mailed %d message(s), want none: the statement was issued and mailed once", len(f.mail.sent)-mailed)
	}
}

// TestStatementEndpointRefusesOtherCallers: the run's door is the job's, the
// read's is the reseller organisation's, and neither admits the other's caller
// (F33, D2190).
//
// The run's cases are the reminder door's, one route over: the job's token is
// accepted, and every other caller is refused — no token at all, a token minted
// for another audience, a token signed for another account, and the SPA's own
// sign-in, which this route never puts to the sign-in verifier, because a
// customer's door and a job's are not one door. The read's cases are the other
// half: a customer organisation's owner is not a reseller, and a member of the
// operations organisation who belongs to no reseller is refused here exactly as
// anybody else is (resellerCaller's own sentence) — operations records money
// against a statement, and reading them is the reseller's own.
func TestStatementEndpointRefusesOtherCallers(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		// The real verifier, over the key P1.3's tests generate: what is refused
		// below is refused by the check a deployment runs, and not by a spy.
		f.deps.Scheduler = jobVerifier{verifier: schedulerVerifier(t), account: portalSchedulerAccount}
	})
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.seededOrganisation("org-customer", "Acme Ltd", orgTypeCustomer, buyerUID, buyerMail, roleOwner)
	f.operationsOrg(roleOwner)

	otherAudience := schedulerClaims()
	otherAudience["aud"] = portalOtherAudience
	otherAccount := schedulerClaims()
	otherAccount["email"] = portalOtherAccount

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "the job's own token", token: signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims()), want: http.StatusNoContent},
		{name: "no token at all", token: "", want: http.StatusUnauthorized},
		{name: "a token minted for another audience", token: signPortalToken(t, portalKey(), portalKid, "RS256", otherAudience), want: http.StatusUnauthorized},
		{name: "a token signed for another account", token: signPortalToken(t, portalKey(), portalKid, "RS256", otherAccount), want: http.StatusUnauthorized},
		{name: "the SPA's own sign-in", token: portalIDToken, want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.mail.sent)
			got := f.apiAsJob(apiStatementsRunPath, tc.token)
			if got.Code != tc.want {
				t.Fatalf("the statement run answered %d with %s, want %d: %s", got.Code, tc.name, tc.want, got.Body)
			}
			if tc.want == http.StatusUnauthorized {
				if code := refusal(f, got); code != "unauthenticated" {
					t.Errorf("the refusal's code is %q, want %q", code, "unauthenticated")
				}
				if len(f.mail.sent) != before {
					t.Errorf("%s mailed %d message(s), want none: a caller this route refuses does not run it", tc.name, len(f.mail.sent)-before)
				}
			}
		})
	}
	if len(f.signIn.tokens) != 0 {
		t.Errorf("the route put %d token(s) to the sign-in verifier, want none: a job's door reads the Scheduler's document and not a customer's sign-in",
			len(f.signIn.tokens))
	}

	for _, tc := range []struct {
		name  string
		uid   string
		email string
	}{
		{name: "a customer organisation's owner", uid: buyerUID, email: buyerMail},
		{name: "a member of the operations organisation", uid: opsUID, email: opsEmail},
	} {
		f.actsAs(tc.uid, tc.email, signInGoogle)
		got := f.apiAsCaller(http.MethodGet, apiStatementsPath, "")
		if got.Code != http.StatusForbidden || refusal(f, got) != "not_reseller" {
			t.Errorf("the statements read answered %d %s for %s, want 403 not_reseller: %s",
				got.Code, refusal(f, got), tc.name, got.Body)
		}
	}
}

// TestPaymentIsRecordedByOperationsOnly: money against a statement is
// operations' to record and nobody else's (D2190), and the record follows the
// money.
//
// The refusals are the ones that leave nothing behind: a reseller may not write
// one, a payment of nothing and one with no reference are not payments, an id no
// statement has is a 404, and a sign-in older than D2187's five minutes is no
// sign-in for a change. A part payment leaves the statement open with the money
// in its figure, the payment that covers the rest settles it at the instant the
// money arrived, and each recorded payment is one line in the log.
func TestPaymentIsRecordedByOperationsOnly(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	f.now = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	bought := f.soldBy("Acme Ltd")
	f.saleOf(bought.ID, f.soldWorkspace(bought.ID, "ws-october"), PolicyTeamMonthly, saleSeats, f.now.Add(30*24*time.Hour))

	f.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	statement := f.runAndRead()
	route := statementPaymentPathOf(statement.ID)

	// The reseller whose statement it is may not record anything against it.
	refused := f.apiAsCaller(http.MethodPost, route, paymentBody(t, 1_000, f.now, "INV-2026-10"))
	if refused.Code != http.StatusForbidden || refusal(f, refused) != "not_operations" {
		t.Errorf("a reseller recording a payment answered %d %s, want 403 not_operations: %s",
			refused.Code, refusal(f, refused), refused.Body)
	}

	// Operations, signed in now: a part payment, which leaves the statement open
	// with the money in its figure.
	f.operationsOrg(roleOwner)
	sent := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	part := f.recordPayment(statement.ID, paymentBody(t, statedMicros/6, sent, "INV-2026-10"))
	if part.PaidMicros != statedMicros/6 || part.State != statementOpen || part.PaidAt != nil {
		t.Errorf("the part payment left %q with %d paid and %v, want open, %d and null",
			part.State, part.PaidMicros, part.PaidAt, statedMicros/6)
	}
	if part.AccruedMicros != statedMicros {
		t.Errorf("the statement's accrual moved to %d, want the month's own %d: a payment is recorded beside it and never netted off it",
			part.AccruedMicros, statedMicros)
	}

	// The rest of it, which settles the statement at the instant the money
	// arrived and not at the day somebody typed it in.
	settled := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	rest := f.recordPayment(statement.ID, paymentBody(t, statedMicros-statedMicros/6, settled, "INV-2026-10-REST"))
	if rest.PaidMicros != statedMicros || rest.State != statementPaid {
		t.Errorf("the payment that covers the rest left %q with %d paid, want paid with %d", rest.State, rest.PaidMicros, statedMicros)
	}
	if rest.PaidAt == nil || !rest.PaidAt.Equal(settled) {
		t.Errorf("the statement was settled at %v, want the instant the money arrived %s", rest.PaidAt, settled)
	}

	// The refusals that leave nothing behind, each asked with a fresh sign-in so
	// that what is refused is the request and not the age of the token.
	for _, tc := range []struct {
		name string
		id   string
		body string
		want int
		code string
	}{
		{name: "a payment of nothing", id: statement.ID, body: paymentBody(t, 0, f.now, "INV-ZERO"), want: http.StatusUnprocessableEntity, code: "invalid_payment"},
		{name: "a payment with no reference", id: statement.ID, body: paymentBody(t, 1_000, f.now, "   "), want: http.StatusUnprocessableEntity, code: "invalid_payment"},
		{name: "a statement no reseller has", id: resellerID + ":2026-09", body: paymentBody(t, 1_000, f.now, "INV-MISSING"), want: http.StatusNotFound, code: "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.operationsOrg(roleOwner)
			got := f.apiAsCaller(http.MethodPost, statementPaymentPathOf(tc.id), tc.body)
			if got.Code != tc.want || refusal(f, got) != tc.code {
				t.Fatalf("the payment answered %d %s, want %d %s: %s", got.Code, refusal(f, got), tc.want, tc.code, got.Body)
			}
		})
	}

	// A sign-in older than the five minutes D2187 gives a change is no sign-in
	// for one, which is the rule every other operations write meets.
	f.operationsOrg(roleOwner)
	f.advance(6 * time.Minute)
	stale := f.apiAsCaller(http.MethodPost, route, paymentBody(t, 1_000, f.now, "INV-LATE"))
	if stale.Code != http.StatusForbidden || refusal(f, stale) != "stale_sign_in" {
		t.Errorf("a payment with a six-minute-old sign-in answered %d %s, want 403 stale_sign_in: %s",
			stale.Code, refusal(f, stale), stale.Body)
	}

	// Two lines and no more: the two payments, and nothing from the five
	// requests that were refused.
	lines := f.auditedAction(actionStatementPayment)
	if len(lines) != 2 {
		t.Fatalf("the log holds %d statement.payment line(s), want the two recorded payments: %v", len(lines), lines)
	}
	for i, reference := range []string{"INV-2026-10", "INV-2026-10-REST"} {
		line := lines[i]
		if line.Action != actionStatementPayment || line.Read {
			t.Errorf("the %d line is %q with read %v, want a write named %q", i, line.Action, line.Read, actionStatementPayment)
		}
		if line.ActorUID != opsUID {
			t.Errorf("the %d line was written by %q, want the operations member %q", i, line.ActorUID, opsUID)
		}
		if line.OrganisationID == nil || *line.OrganisationID != resellerID {
			t.Errorf("the %d line names the organisation %v, want the reseller whose statement it is %q", i, line.OrganisationID, resellerID)
		}
		if line.TargetID == nil || *line.TargetID != statement.ID {
			t.Errorf("the %d line names the target %v, want the statement %q", i, line.TargetID, statement.ID)
		}
		if line.Detail == nil || *line.Detail != reference {
			t.Errorf("the %d line's detail is %v, want the reference operations was given %q", i, line.Detail, reference)
		}
	}
}
