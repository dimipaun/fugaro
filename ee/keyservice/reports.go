// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The monthly sales reports (portal plan P5.2; spec §Operations, D2190).
//
// A report is one UTC month of sales split by channel: what customers bought
// directly and what was sold through a reseller. It is **derived and never
// counted** — the direct half is the ledger's own lines added up (ledger.go),
// and the reseller half is the lines of the month's statements
// (statements.go) — so a report, the ledger and a reseller's statement cannot
// disagree, and every figure an operator reads is walkable back to the event or
// the statement it came from (wire_reports.go's own sentence).
//
// **A grant that books no money is not revenue.** The free seat, a trial and a
// licence operations mints outside a sale are grants and not purchases (D2199,
// D2203); nothing in this file prices one, so a month of comped seats adds
// nothing to either channel. What the customer still is, is a line — with an
// amount of zero — because a report read as the month's customers rather than
// as its money is the one operations reconciles against, and a customer left
// out would hide the month it never bought anything in.
//
// **The reseller half is the statements' own reading.** D2190's direction is
// resellers owing Fugaro, and the figure this file states for that channel is
// what the month's statements have already billed — their lines, their
// arithmetic — rather than a second accrual computed here, which would be the
// one way the two could come apart (TestResellerReceivableMatchesItsStatements).
//
// Nothing here is a screen's shape: the report is one object, the route that
// answers it is `api_reports.go`'s, and the month it is asked for is the read's
// own query.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The two channels a line is in, spelled as `reports.schema.json`'s enum spells
// them and as the ledger's own lines spell them (ledger.go).
const (
	channelDirect   = "direct"
	channelReseller = "reseller"
)

// actionReportRead is the read of this area. It is operations' own door (D2188),
// and D456's rule that the reads are logged holds for it as it does for every
// other read the operations membership widens.
const actionReportRead = "report.read"

// reportLine is one customer's month while it is being added up: what each
// channel booked for them, whether either did, and whether the month granted
// them a licence that booked nothing.
type reportLine struct {
	direct      int64
	reseller    int64
	hasDirect   bool
	hasReseller bool
	granted     bool
}

// reportMonthLines is one month while it is being added up: a line for every
// customer the month named, and the month's own two figures.
//
// The figures are not the sum of the lines, and that is the whole reason the
// two are one type rather than the one being read off the other. A customer
// line needs an organisation — `reports.schema.json` requires an id of every
// one — where a ledger line does not: an adjustment, which is the body the
// merchant posts for a refund or a chargeback, names the transaction and the
// subscription and no customer at all. Such a line is the month's money, so it
// is in the figure, and it is nobody's line, so it is not in the list (P5.2's
// `TestReportCountsARefundItsEventNamesNoCustomerFor`). The ledger's own read is
// where a person sees it, under the event it came from.
type reportMonthLines struct {
	customers map[string]*reportLine
	direct    int64
	reseller  int64
}

// reportWindow is the half-open window one report month covers: its first
// instant and the first instant of the month after it, both in UTC.
//
// UTC is the whole point of the spelling (P5.2). A month boundary read in the
// browser's zone or the host's would put one day's sales in two reports, and the
// statements beside them are UTC for the same reason (statements.go). A month
// that is not `YYYY-MM` is the read's own bad query — the same refusal the
// ledger's month read answers with — so a typo in the SPA's form is not a month
// no customer bought in.
func reportWindow(month string) (time.Time, time.Time, error) {
	start, err := time.Parse(statementMonthLayout, strings.TrimSpace(month))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: a month is `YYYY-MM` and %q is not one", errOperationsBadQuery, month)
	}
	start = start.UTC()
	return start, start.AddDate(0, 1, 0), nil
}

// inUTCmonth is whether one instant falls in a window, read in UTC and never in
// the instant's own location.
//
// The instants this file is handed are read out of bodies and records that
// carry an offset of their own — a Paddle notification's `occurred_at` does —
// so a boundary compared in the location it arrived in would place the same
// sale in one month for one reader and the next month for another, and a month
// read in two zones is two reports (P5.2's TestReportMonthsAreUTC).
func inUTCmonth(at, start, end time.Time) bool {
	utc := at.UTC()
	return !utc.Before(start) && utc.Before(end)
}

// reportMonth is one month's report: the two channels' totals and a line for
// every customer the month named. The two are one month read twice and not one
// read off the other — a line that named no customer is in the total and is
// nobody's line (reportMonthLines).
//
// The customers are drawn in the order of their organisation id and, for one
// customer, of the channel — a fixed order and not the store's, so two months
// read side by side line up and a customer that moved between them can be seen
// to have moved.
func (s *Service) reportMonth(ctx context.Context, month string) (WireReportMonth, error) {
	start, end, err := reportWindow(month)
	if err != nil {
		return WireReportMonth{}, err
	}
	held, err := s.reportLines(ctx, start, end)
	if err != nil {
		return WireReportMonth{}, err
	}
	orgs, err := s.reportOrganisations(ctx)
	if err != nil {
		return WireReportMonth{}, err
	}
	ids := make([]string, 0, len(held.customers))
	for id := range held.customers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// The two figures are the month's own and are not the customer lines below
	// added up: a line no customer was named for is money the month took or gave
	// back and is nobody's line (reportMonthLines).
	report := WireReportMonth{
		Month:          start.Format(statementMonthLayout),
		DirectMicros:   held.direct,
		ResellerMicros: held.reseller,
		Customers:      make([]WireReportCustomer, 0, len(ids)),
	}
	for _, id := range ids {
		line := held.customers[id]
		name := reportName(orgs, id)
		if line.hasDirect {
			report.Customers = append(report.Customers, WireReportCustomer{
				OrganisationID: id, Name: name, Channel: channelDirect, AmountMicros: line.direct,
			})
		}
		if line.hasReseller {
			report.Customers = append(report.Customers, WireReportCustomer{
				OrganisationID: id, Name: name, Channel: channelReseller, AmountMicros: line.reseller,
			})
		}
		if !line.hasDirect && !line.hasReseller && line.granted {
			// A grant booked no money, so the month's sales say nothing about
			// which channel it is in; the organisation's own link to a reseller
			// says it (D2191). A reseller's customer is drawn on the reseller's
			// channel — the licence it holds came from there whatever was paid
			// for it — and everyone else, which is every sign-up's free seat, is
			// Fugaro's own.
			channel := channelDirect
			if org, known := orgs[id]; known && org.ResellerID != nil {
				channel = channelReseller
			}
			report.Customers = append(report.Customers, WireReportCustomer{
				OrganisationID: id, Name: name, Channel: channel, AmountMicros: 0,
			})
		}
	}
	return report, nil
}

// reportLines is one month while it is being added up: the figures the ledger's
// lines make, the month's statements' lines for the reseller half, and the
// licences the month granted, which are a line and never an amount.
func (s *Service) reportLines(ctx context.Context, start, end time.Time) (reportMonthLines, error) {
	held := reportMonthLines{customers: map[string]*reportLine{}}
	line := func(organisationID string) *reportLine {
		if held.customers[organisationID] == nil {
			held.customers[organisationID] = &reportLine{}
		}
		return held.customers[organisationID]
	}

	entries, err := s.ledgerEntries(ctx)
	if err != nil {
		return reportMonthLines{}, err
	}
	for _, entry := range entries {
		if !inUTCmonth(entry.At, start, end) {
			continue
		}
		// The month's figure is every line of the month, named or not, because
		// that is what the ledger's own read of the same month adds up
		// (wire_reports.go signs both figures for exactly this): an adjustment —
		// the body Paddle posts for a refund or a chargeback — names the
		// transaction and the subscription and no organisation at all, and a
		// figure that dropped it would overstate the month by the whole of its
		// refunds.
		//
		// The bucket is the line's own channel and not a choice made here: the
		// ledger states it, and a report that decided it again is the second
		// opinion the two would come apart on.
		if entry.Channel == channelReseller {
			held.reseller += entry.AmountMicros
		} else {
			held.direct += entry.AmountMicros
		}
		// What such a line cannot be is a customer's line: the report is a list
		// of customers and every line of it carries an id and a name
		// (reports.schema.json), where an event whose body named no organisation
		// has no customer to be drawn under. Nothing is invented to draw it —
		// the money is in the figure above, and the ledger's own month read is
		// where a person sees the line itself.
		if strings.TrimSpace(entry.OrganisationID) == "" {
			continue
		}
		if entry.Channel == channelReseller {
			by := line(entry.OrganisationID)
			by.hasReseller = true
			by.reseller += entry.AmountMicros
			continue
		}
		by := line(entry.OrganisationID)
		by.hasDirect = true
		by.direct += entry.AmountMicros
	}

	statements, err := recordsWhere[WireStatement](ctx, s.docs, statementsCollection,
		Where{Field: "month", Value: start.Format(statementMonthLayout)})
	if err != nil {
		return reportMonthLines{}, err
	}
	for _, statement := range statements {
		for _, one := range statement.Lines {
			held.reseller += one.AmountMicros
			if strings.TrimSpace(one.OrganisationID) == "" {
				continue
			}
			by := line(one.OrganisationID)
			by.hasReseller = true
			by.reseller += one.AmountMicros
		}
	}

	grants, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection, Where{})
	if err != nil {
		return reportMonthLines{}, err
	}
	for _, grant := range grants {
		if !inUTCmonth(grant.StartsAt, start, end) {
			continue
		}
		// A reseller's sale is billed by the month's statement and by nothing
		// else: the statement's line is already the reseller channel's, and a
		// grant that added a second one would state the same month twice.
		if grant.Source == entitlementSourceReseller {
			continue
		}
		if strings.TrimSpace(grant.OrganisationID) == "" {
			continue
		}
		line(grant.OrganisationID).granted = true
	}
	return held, nil
}

// reportOrganisations is every organisation the portal holds, by id: what a
// customer's line is named by and which of them a reseller's customers are.
func (s *Service) reportOrganisations(ctx context.Context) (map[string]WireOrganisation, error) {
	held, err := recordsWhere[WireOrganisation](ctx, s.docs, organisationsCollection, Where{})
	if err != nil {
		return nil, err
	}
	orgs := make(map[string]WireOrganisation, len(held))
	for _, org := range held {
		orgs[org.ID] = org
	}
	return orgs, nil
}

// reportName is what one customer's line is drawn as.
//
// An organisation the store does not hold — a sale whose organisation was never
// signed up, which a merchant event cannot prevent — is drawn by its own id:
// the shape needs a name of every line, and an id is a thing a person can look
// up, where a line left nameless fails the shape the SPA reads.
func reportName(orgs map[string]WireOrganisation, organisationID string) string {
	if org, held := orgs[organisationID]; held && strings.TrimSpace(org.Name) != "" {
		return org.Name
	}
	return organisationID
}
